package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/service"
)

// 배선 시험 — mcpBackend.Pick 이 실물 서버를 상대로 item_ids 를 실제로 쓰기까지 옮기는가.
//
// 태스크 9 까지는 이 도달이 없었다. mcpBackend.Pick 은 `in.ItemID == ""` 이면
// **추천**(GET /items/next, 읽기)으로 분기했는데, item_ids 만 채운 요청은 ItemID 가
// 비어 있으니 그 분기를 그대로 타 아무것도 안 집고 추천을 돌려줬다 — 태스크 7 의
// Critical 이 배포 경로에서 되살아나는 자리였다.
//
// mcpsrv 의 시험(newServer)은 service 를 **직접 주입**해서 이 결함을 못 봤다. 여기서는
// `fd mcp` 와 같은 순서(newApp → newMCPBackend)로 조립하고, 실물 서버(newHarness)에
// 실제로 HTTP 를 보내 **store 에 무엇이 남았는지**를 본다.
func TestMCPBackendPickBundleDoesNotFallToRecommend(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if code, out := h.run("", "open", "--label", "배선묶음"); code != 0 {
		t.Fatalf("세션 열기 실패(%d):\n%s", code, out)
	}
	for _, id := range []string{"wire-lead", "wire-m1"} {
		if code, out := h.run("", "add", "--id", id, "--title", id+" 제목", "--body", id+" 본문"); code != 0 {
			t.Fatalf("항목 등록 실패(%s, %d):\n%s", id, code, out)
		}
	}

	// mcpBackend 를 운영과 같은 순서로 조립한다(가짜 백엔드를 끼우지 않는다) —
	// 이 시험이 막으려는 결함이 바로 "배선이 딴 데를 본다" 이다.
	dir := t.TempDir()
	app := newApp(envOf(h.env), quietLogger(), dir, strings.NewReader(""))
	openRes, _, err := app.OpenSession(ctx, "cc-session-uuid-1", "")
	if err != nil {
		t.Fatalf("세션 좌표 확보 실패: %v", err)
	}
	sess := openRes.Session.ID

	backend := newMCPBackend(app)
	res, err := backend.Pick(ctx, service.PickInput{
		Project: h.project, SessionID: sess,
		ItemID:  "", // ★ 일부러 비운다 — 결함이 나던 갈래가 정확히 이것이다.
		ItemIDs: []string{"wire-lead", "wire-m1"},
	})
	if err != nil {
		t.Fatalf("묶음 선점 실패: %v", err)
	}
	if res.Mode == service.PickRecommended {
		t.Fatalf("item_ids 만 줬는데 추천 경로로 빠졌다(mode=%s) — Pick 의 분기 순서를 의심해라", res.Mode)
	}
	if res.Mode != service.PickClaimed {
		t.Fatalf("mode 가 %q 다(claimed 를 기대)", res.Mode)
	}
	if res.Bundle == nil {
		t.Fatal("묶음으로 집었는데 응답에 Bundle 절이 없다")
	}

	// 좌표계는 "서버가 실제로 무엇을 갖게 됐나"다 — 응답을 셌다는 것만으로는
	// "보냈다"만 말하고 "저장됐다"는 말하지 못한다.
	for _, id := range []string{"wire-lead", "wire-m1"} {
		cl, err := h.st.GetClaim(ctx, h.project, id)
		if err != nil || cl.ReleasedAt != nil {
			t.Fatalf("항목 %s 의 선점 행이 서버에 없다: %+v %v", id, cl, err)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 리뷰 라운드 2 finding 1 — **보낸 것과 돌아온 것을 대조한다**
// ─────────────────────────────────────────────────────────────────────────────

// serverIgnoringItemIDs 는 **item_ids 를 모르는 서버**를 실물로 세운다.
//
// cae53bd 판이 정확히 이랬다: 그 필드를 조용히 버리고 URL 경로의 선두 하나만 집은 뒤
// 200 을 낸다. api_version 은 양쪽 다 "1" 이라 SkewBanner 도 안 뜬다 — 즉 클라이언트가
// 스스로 대조하지 않으면 이 스큐를 **원리적으로** 못 본다.
//
// 가짜 핸들러를 쓰지 않는다: 뒤에 있는 것은 실물 서버이고, 이 프록시가 하는 일은
// 구서버가 하던 일 하나(모르는 필드를 버린다) 뿐이다.
func serverIgnoringItemIDs(t *testing.T, target string) *httptest.Server {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatalf("대상 주소 해석 실패: %v", err)
	}
	stripped := 0
	proxy := httputil.NewSingleHostReverseProxy(u)
	inner := proxy.Director
	proxy.Director = func(r *http.Request) {
		inner(r)
		if r.Body == nil {
			return
		}
		body, err := io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil {
			return
		}
		var m map[string]any
		if json.Unmarshal(body, &m) == nil {
			if _, had := m["item_ids"]; had {
				delete(m, "item_ids")
				stripped++
				if b, err := json.Marshal(m); err == nil {
					body = b
				}
			}
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		r.Header.Set("Content-Length", strconv.Itoa(len(body)))
	}
	srv := httptest.NewServer(proxy)
	t.Cleanup(func() {
		// 대조가 성립했는지를 **결과를 읽은 뒤에** 확인한다: 한 번도 안 벗겨냈으면
		// 이 시험은 구서버를 흉내 낸 적이 없고, 그러면 아무것도 안 지킨 것이다.
		if stripped == 0 {
			t.Error("전제가 깨졌다 — item_ids 를 한 번도 안 벗겨냈다(구서버를 흉내 내지 못했다)")
		}
		srv.Close()
	})
	return srv
}

// TestPickCLIFailsWhenServerSilentlyDropsItemIDs 는 finding 1 의 CLI 쪽이다.
//
// 실측된 결함: `fd pick sk-a sk-b sk-c` 가 **종료코드 0** 으로 sk-a 만 찍고 끝났다.
// sk-b·sk-c 는 선점되지도, 이름이 불리지도 않았다 — 선점이 존재하는 이유가 정확히
// 그 상황을 막는 것인데, 그 상황이 성공으로 보고됐다.
func TestPickCLIFailsWhenServerSilentlyDropsItemIDs(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if code, out := h.run("", "open", "--label", "구서버대조"); code != 0 {
		t.Fatalf("세션 열기 실패(%d):\n%s", code, out)
	}
	ids := []string{"sk-a", "sk-b", "sk-c"}
	for _, id := range ids {
		if code, out := h.run("", "add", "--id", id, "--title", id+" 제목", "--body", id+" 본문"); code != 0 {
			t.Fatalf("항목 등록 실패(%s, %d):\n%s", id, code, out)
		}
	}

	old := serverIgnoringItemIDs(t, h.srv.URL)
	env := map[string]string{}
	for k, v := range h.env {
		env[k] = v
	}
	env["FD_URL"] = old.URL

	code, out := h.runEnv(env, "", "pick", "sk-a", "sk-b", "sk-c")

	// 대조 전제: 서버는 정말 선두만 집었나(그래야 아래 단정이 의미가 있다).
	for _, id := range []string{"sk-b", "sk-c"} {
		if cl, err := h.st.GetClaim(ctx, h.project, id); err == nil && cl.ReleasedAt == nil {
			t.Fatalf("전제가 깨졌다 — %s 가 실제로 선점됐다: %+v", id, cl)
		}
	}
	if cl, err := h.st.GetClaim(ctx, h.project, "sk-a"); err != nil || cl.ReleasedAt != nil {
		t.Fatalf("전제가 깨졌다 — 선두 sk-a 조차 안 집혔다: %+v %v", cl, err)
	}

	if code == 0 {
		t.Fatalf("아무도 안 쥔 항목 둘을 두고 종료코드 0 을 냈다 — "+
			"스크립트와 에이전트가 읽는 유일한 기계 신호가 이것이다:\n%s", out)
	}
	for _, id := range []string{"sk-b", "sk-c"} {
		if !strings.Contains(out, id) {
			t.Fatalf("%s 가 출력 어디에도 없다 — 이름조차 안 불렸다:\n%s", id, out)
		}
	}
	// 선두의 맥락은 지워지면 안 된다 — 그것은 실제로 집혔다.
	if !strings.Contains(out, "브랜치: sk-a") {
		t.Fatalf("성공한 절반(선두)의 맥락까지 버렸다:\n%s", out)
	}
}

// 정상 서버에서는 이 경고가 **안 나오고** 종료코드가 0 이어야 한다.
// 대조가 없으면 "항상 실패한다"도 위 시험을 통과한다.
func TestPickCLISucceedsQuietlyAgainstACurrentServer(t *testing.T) {
	h := newHarness(t)
	if code, out := h.run("", "open", "--label", "정상대조"); code != 0 {
		t.Fatalf("세션 열기 실패(%d):\n%s", code, out)
	}
	for _, id := range []string{"ok-a", "ok-b"} {
		if code, out := h.run("", "add", "--id", id, "--title", id+" 제목", "--body", id+" 본문"); code != 0 {
			t.Fatalf("항목 등록 실패(%s, %d):\n%s", id, code, out)
		}
	}
	code, out := h.run("", "pick", "ok-a", "ok-b")
	if code != 0 {
		t.Fatalf("정상 서버인데 종료코드가 %d 다:\n%s", code, out)
	}
	if strings.Contains(out, "설명하지 않는다") {
		t.Fatalf("전부 설명된 응답에 경고가 붙었다:\n%s", out)
	}
}
