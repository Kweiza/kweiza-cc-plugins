package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/store"
)

// 404 의 좌표 — 소비자 좌표계는 **HTTP 응답 본문**이다.
//
// ★ 이 시험이 막는 것: REST 를 건너오면 "무엇이 없었는지"가 사라져
// 오타 난 항목 id 와 프로젝트 미등록과 이미 반납된 선점이 **글자 그대로 같은 화면**이 되는 것.
// 404 는 여러 사유를 하나로 합류시키므로, 좌표가 없으면 조사가 사용자 신고 충실도에 의존한다.

func TestNotFoundCarriesWhatWasMissing(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-notfound-1")
	ctx := context.Background()

	// ── 대조가 성립했는지 먼저 단정한다 ─────────────────────────────────────
	// 프로젝트가 실제로 등록돼 있어야 "항목만 없다"는 축이 성립한다. 이것이 거짓이면
	// '항목 오타' 케이스도 프로젝트 미등록으로 404 가 나고, 그러면 이 시험은
	// 기대한 상태코드를 그대로 내면서 아무것도 검사하지 않는다.
	if _, err := e.st.GetProject(ctx, testProject); err != nil {
		t.Fatalf("전제가 깨졌다 — 프로젝트 %q 가 등록돼 있어야 한다: %v", testProject, err)
	}
	if _, err := e.st.GetItem(ctx, testProject, "t9-nonexistent"); err == nil {
		t.Fatal("전제가 깨졌다 — 항목 t9-nonexistent 는 없어야 한다")
	}
	if _, err := e.st.GetProject(ctx, "없는프로젝트"); err == nil {
		t.Fatal("전제가 깨졌다 — 프로젝트 없는프로젝트 는 없어야 한다")
	}
	if _, err := e.st.GetSession(ctx, "S-없는세션"); err == nil {
		t.Fatal("전제가 깨졌다 — 세션 S-없는세션 은 없어야 한다")
	}

	cases := []struct {
		name  string
		send  func() *httptest.ResponseRecorder
		coord string // 문구에 반드시 있어야 할 좌표(호출자가 방금 보낸 값)
		kind  string // 문구에 반드시 있어야 할 종류 이름
	}{
		{
			name: "프로젝트 미등록",
			send: func() *httptest.ResponseRecorder {
				return e.write(http.MethodPost, "/api/v1/items/t9-nonexistent/claim",
					map[string]any{"project": "없는프로젝트", "session_id": sess})
			},
			coord: "없는프로젝트", kind: "프로젝트",
		},
		{
			name: "항목 id 오타",
			send: func() *httptest.ResponseRecorder {
				return e.write(http.MethodPost, "/api/v1/items/t9-nonexistent/claim",
					map[string]any{"project": testProject, "session_id": sess})
			},
			coord: testProject + "/t9-nonexistent", kind: "항목",
		},
		{
			name: "세션 미등록",
			send: func() *httptest.ResponseRecorder {
				return e.write(http.MethodPatch, "/api/v1/sessions/S-없는세션",
					map[string]any{"state": "paused", "why": "시험"})
			},
			coord: "S-없는세션", kind: "세션",
		},
		{
			name: "스냅숏 미존재",
			send: func() *httptest.ResponseRecorder {
				return e.do(http.MethodGet, "/api/v1/snapshots/part3.pct?project="+testProject, nil)
			},
			coord: testProject + "/part3.pct", kind: "스냅숏",
		},
	}

	msgs := map[string]string{}
	guides := map[string]string{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := c.send()
			if w.Code != http.StatusNotFound {
				t.Fatalf("전제가 깨졌다 — 404 가 아니라 %d 다: %s", w.Code, w.Body.String())
			}
			body := errorOf(t, w)
			msg, _ := body["message"].(string)
			guide, _ := body["guidance"].(string)
			code, _ := body["code"].(string)

			if code != "not_found" {
				t.Fatalf("code=%q, 기대 not_found", code)
			}
			if !strings.Contains(msg, c.coord) {
				t.Errorf("문구에 좌표 %q 가 없다 — 무엇이 없었는지 알 수 없다: %q", c.coord, msg)
			}
			if !strings.Contains(msg, c.kind) {
				t.Errorf("문구에 종류 %q 가 없다: %q", c.kind, msg)
			}
			if strings.TrimSpace(guide) == "" {
				t.Errorf("처방이 비었다 — 무엇을 고쳐야 하는지가 없다: %q", msg)
			}
			msgs[c.name] = msg
			guides[c.name] = guide
		})
	}

	// ★ 이 단정이 이 시험의 핵심이다. 좌표를 실었어도 문구가 같으면
	//   네 사유는 여전히 같은 화면이다.
	assertAllDistinct(t, "문구", msgs)
	assertAllDistinct(t, "처방", guides)
}

func assertAllDistinct(t *testing.T, what string, got map[string]string) {
	t.Helper()
	if len(got) < 2 {
		t.Fatalf("전제가 깨졌다 — %s 를 비교할 케이스가 %d개뿐이다", what, len(got))
	}
	seen := map[string]string{}
	for name, v := range got {
		if prev, dup := seen[v]; dup {
			t.Errorf("%s 가 %q 와 %q 에서 글자 그대로 같다 — 두 사유가 같은 화면이다: %q",
				what, prev, name, v)
		}
		seen[v] = name
	}
}

// ★ 좌표는 호출자가 보낸 값이라 **외부 입력**이다. 제어문자가 그대로 응답에 실리면
// 그 줄이 로그·터미널에서 다른 줄로 보이고(로그 주입), 길이도 상한이 없어진다.
func TestNotFoundClipsHostileCoordinate(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-notfound-2")

	// %0A = 개행. 라우터가 디코딩해 PathValue 로 넘긴다.
	w := e.write(http.MethodPost, "/api/v1/items/t9-%0Abad"+strings.Repeat("a", 400)+"/claim",
		map[string]any{"project": testProject, "session_id": sess})
	if w.Code != http.StatusNotFound {
		t.Fatalf("전제가 깨졌다 — 404 가 아니라 %d 다: %s", w.Code, w.Body.String())
	}
	msg, _ := errorOf(t, w)["message"].(string)
	if msg == "" {
		t.Fatal("문구가 비었다")
	}
	if strings.ContainsAny(msg, "\n\r\t") {
		t.Errorf("문구에 제어문자가 그대로 실렸다: %q", msg)
	}
	if !strings.Contains(msg, "t9- bad") {
		t.Errorf("제어문자를 공백으로 바꾼 흔적이 없다 — 좌표가 통째로 사라졌을 수 있다: %q", msg)
	}
	if n := len([]rune(msg)); n > 260 {
		t.Errorf("문구가 안 잘렸다(%d룬) — 400자 좌표가 그대로 실렸다", n)
	}
}

// ★ 종류 전수 확인. 종류를 하나 늘리고 처방을 안 채우면 그 하나만
// "처방이 표에 없다"로 새어 나가는데, 그 문구는 무엇을 고칠지 말하지 못한다.
func TestNotFoundGuidanceCoversEveryKind(t *testing.T) {
	kinds := store.NotFoundKinds()
	if len(kinds) == 0 {
		t.Fatal("전제가 깨졌다 — 종류 목록이 비었으면 이 시험은 아무것도 안 본다")
	}
	for _, k := range kinds {
		g, ok := notFoundGuidance[k]
		if !ok {
			t.Errorf("종류 %q 의 처방이 없다 — 기본 문구로 새어 나간다", k)
			continue
		}
		if strings.TrimSpace(g) == "" {
			t.Errorf("종류 %q 의 처방이 비었다", k)
		}
	}
}

// NotFoundAdvice 가 좌표 조합 전부에서 죽지 않고 무엇이 없었는지를 낸다. 순수 함수 시험이다.
func TestNotFoundAdviceShapes(t *testing.T) {
	cases := []struct {
		name string
		err  *store.NotFoundError
		want string
	}{
		{"프로젝트+id", &store.NotFoundError{Kind: store.NFItem, Project: "cp", ID: "t9-x"}, "항목 cp/t9-x 가 없다"},
		{"id 만", &store.NotFoundError{Kind: store.NFSession, ID: "S1"}, "세션 S1 가 없다"},
		{"좌표 없음", &store.NotFoundError{Kind: store.NFSession, Note: "3중키에 해당하는"}, "3중키에 해당하는 세션 가 없다"},
		{"모르는 종류", &store.NotFoundError{Kind: store.NotFoundKind("무엇"), ID: "x"}, "무엇 x 가 없다"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NotFoundAdvice(c.err)
			if got.Status != http.StatusNotFound || got.Code != "not_found" {
				t.Fatalf("status=%d code=%q", got.Status, got.Code)
			}
			if got.Message != c.want {
				t.Fatalf("문구가 %q, 기대 %q", got.Message, c.want)
			}
			if strings.TrimSpace(got.Guidance) == "" {
				t.Fatal("처방이 비었다 — 모르는 종류에도 무엇을 볼지는 말해야 한다")
			}
		})
	}
	// 표 밖: nil 을 넣어도 죽지 않는다(호출부가 실수해도 500 을 흘리지 않는다).
	if got := NotFoundAdvice(nil); got.Status != http.StatusOK {
		t.Fatalf("nil 에 status=%d", got.Status)
	}
}
