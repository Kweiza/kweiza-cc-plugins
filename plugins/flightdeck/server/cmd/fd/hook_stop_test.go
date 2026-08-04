package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeServer 는 이 파일 전용 가짜 서버다.
//
// cmd/fd 의 기존 시험(harness_test.go)은 실물 store+service+web 을 붙인 서버를 쓴다 —
// wire.go 의 필드 이름이 internal/api 와 어긋나는 것을 잡기 위해서다. 이 파일의 시험은
// 그것과 다른 축을 본다: **서버가 낸 JSON 의 필드 이름이 클라이언트 구조체와 맞는가.**
// 그 축은 응답 문구를 원하는 그대로 낼 수 있어야 보이므로, 실물 서비스 계층을 거치지 않고
// routes 에 준 문자열을 그대로 낸다.
//
// POST /api/v1/sessions 가 routes 에 없으면 세션 S1 을 내는 기본 응답을 준다 —
// 그래야 hookStop 이 이어서 부르는 /api/v1/sessions/S1/prescriptions 가 맞물린다.
func fakeServer(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, body := range routes {
		body := body
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, body)
		})
	}
	if _, ok := routes["/api/v1/sessions"]; !ok {
		mux.HandleFunc("/api/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"session":{"id":"S1"},"created":false}`)
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// runHookForTest 는 `fd hook <event>` 한 번을 실물 진입점(run)으로 돌리고 stdout 을 낸다.
// FD_STATE_DIR 를 매번 새 임시 디렉토리로 줘 시험 간 캐시·아웃박스가 안 섞인다.
//
// ★ HOME 도 임시로 준다. 안 주면 homeDir 이 os.UserHomeDir()(프로세스 환경)로 떨어지고,
// 그 값으로 만들어지는 옛 채널 자리 후보에 개발자의 진짜 ~/.local/state/flightdeck/outbox 가
// 들어간다 — 훅이 재생을 돌리므로 **그 판단이 시험 서버로 나간다.**
// 이 함수는 하네스를 안 쓰므로 하네스의 HOME 고정이 여기까지 안 온다.
func runHookForTest(t *testing.T, url, event, stdin string) string {
	t.Helper()
	env := envOf(map[string]string{
		"FD_URL":       url,
		"FD_STATE_DIR": t.TempDir(),
		"FD_PROJECT":   "testproj",
		"FD_LOG":       "error",
		"HOME":         t.TempDir(),
	})
	var out, errb bytes.Buffer
	run([]string{"hook", event}, env, strings.NewReader(stdin), &out, &errb)
	return out.String()
}

func TestRenderPrescriptionsNamesTheCall(t *testing.T) {
	got := RenderPrescriptions([]PrescriptionLine{
		{Key: "overlap:01OTHER", Text: "cmd/fd/hook.go 를 만졌는데 세션 01OTHER 도 잡고 있다.\n  → note(kind='ask', …)"},
	}, 0)

	if !strings.Contains(got, "flightdeck 처방") {
		t.Fatalf("머리글이 없다: %q", got)
	}
	if !strings.Contains(got, "note(kind='ask'") {
		t.Fatalf("부를 도구가 없다: %q", got)
	}
	if strings.Contains(got, "접었다") {
		t.Fatalf("접힌 게 없는데 접었다고 말한다: %q", got)
	}
}

func TestRenderPrescriptionsSaysWhatItFolded(t *testing.T) {
	got := RenderPrescriptions([]PrescriptionLine{{Key: "outside:a", Text: "a"}}, 7)
	if !strings.Contains(got, "7") {
		t.Fatalf("접힌 수를 안 말한다: %q", got)
	}
}

// 처방이 0건이면 **아무것도 안 낸다.** 빈 머리글을 내면 매 턴 컨텍스트를 먹고,
// 그러면 세션이 이 채널 자체를 읽지 않게 된다.
func TestRenderPrescriptionsEmptyIsSilent(t *testing.T) {
	if got := RenderPrescriptions(nil, 0); got != "" {
		t.Fatalf("0건인데 뭔가 냈다: %q", got)
	}
}

// 서버 응답의 필드 이름이 클라이언트 구조체와 맞는지 본다.
// 태그가 어긋나면 처방이 조용히 빈 목록이 된다 — 이 시험이 없으면 아무도 모른다.
func TestHookStopReadsServerFieldNames(t *testing.T) {
	srv := fakeServer(t, map[string]string{
		"/api/v1/sessions/S1/prescriptions": `{"shown":[{"key":"unclaimed","text":"XYZ-MARK"}],"folded":2}`,
	})
	defer srv.Close()

	out := runHookForTest(t, srv.URL, "stop", `{"session_id":"cc-1","cwd":"."}`)
	if !strings.Contains(out, "XYZ-MARK") {
		t.Fatalf("서버가 준 문구가 stdout 에 없다: %q", out)
	}
	if !strings.Contains(out, "2") {
		t.Fatalf("접힌 수가 stdout 에 없다: %q", out)
	}
}

// 서버가 죽어도 훅은 조용히 성공한다. 훅이 세션을 막으면 안 된다.
func TestHookStopFailsOpen(t *testing.T) {
	out := runHookForTest(t, "http://127.0.0.1:1", "stop", `{"session_id":"cc-1","cwd":"."}`)
	if strings.TrimSpace(out) != "" {
		t.Fatalf("서버가 없는데 뭔가 냈다: %q", out)
	}
}

// ★ 이 시험이 이 파일에서 가장 중요하다.
//
// stop_hook_active 면 아무것도 안 낸다. 안 그러면 주입이 모델을 다시 부르고,
// 그 턴이 끝나면 Stop 이 또 불리고, 또 주입한다 — 무한 루프다.
// 2026-08-04 에 무가드 판으로 실제로 재현했고 사람이 인터럽트로 끊었다.
func TestHookStopIsSilentOnReentry(t *testing.T) {
	srv := fakeServer(t, map[string]string{
		"/api/v1/sessions/S1/prescriptions": `{"shown":[{"key":"unclaimed","text":"XYZ-MARK"}],"folded":0}`,
	})
	defer srv.Close()

	out := runHookForTest(t, srv.URL, "stop",
		`{"session_id":"cc-1","cwd":".","stop_hook_active":true}`)
	if strings.TrimSpace(out) != "" {
		t.Fatalf("재진입인데 뭔가 냈다 — 이게 무한 루프의 씨앗이다: %q", out)
	}
}

// ★ 두 번째로 중요한 시험이다.
//
// Stop 페이로드가 JSON 으로 못 읽히면 runHook 은(session-start 가 페이로드 없이도
// 배너를 내야 하므로) 경고만 남기고 **제로값** HookPayload 로 계속 진행한다 — 그러면
// StopHookActive 는 항상 false 로 읽힌다. 플랫폼이 Stop 페이로드 모양을 바꾸는 날
// 이 훅의 모든 호출이 파싱에 실패하고, 그러면 재진입 가드가 **매번** 꺼진다.
// 그때 유일한 방벽은 우연한 edge-triggering(억제가 이미 걸린 키는 다시 안 뜬다)뿐이고,
// 재진입 턴이 새 경로를 편집하면(새 outside:<path> 키) 그 방벽도 뚫린다.
//
// hookStop 은 파싱 성패로 가드를 걸어야 한다 — 억제가 우연히 막아 주는 것에 기대면 안 된다.
func TestHookStopStaysSilentWhenPayloadFailsToParse(t *testing.T) {
	h := newHarness(t)

	// 세션을 실제로 연다. 환경(CLAUDE_CODE_SESSION_ID)과 같은 id 를 페이로드에도 써서
	// "페이로드가 깨져 환경으로 떨어져도 같은 세션을 가리킨다"는 실제 조건을 맞춘다.
	//
	// ★ 페이로드에 cwd 를 안 싣는다 — 실으면(예: "/tmp") 이 훅 호출의 App 이 그 값으로
	// a.proj 를 덮어써, 파싱이 실패해 cwd 를 못 읽는 마지막 호출(App 이 새로 만들어지며
	// 실제 프로세스 cwd 로 되돌아간다)과 워크트리가 어긋난다 — 그러면 세 호출이 서로 다른
	// 세션을 봐 이 시험이 재현하려는 조건(같은 세션, 파싱만 실패) 자체가 안 선다.
	// cwd 를 생략하면 세 호출 전부 App 생성 시점의 실제 프로세스 cwd 로 일치한다.
	const cc = "cc-session-uuid-1"
	if code, _ := h.run(`{"session_id":"`+cc+`","hook_event_name":"SessionStart"}`,
		"hook", "session-start"); code != 0 {
		t.Fatal("세션 열기 실패")
	}
	// 선점 없이 편집 — "unclaimed" 처방 조건을 실제로 성립시킨다(재진입 턴이
	// 새 경로를 편집하는 상황과 같은 모양이다).
	if code, _ := h.run(`{"session_id":"`+cc+`","tool_name":"Edit",`+
		`"tool_input":{"file_path":"pipeline/x.py"}}`, "hook", "post-tool"); code != 0 {
		t.Fatal("post-tool 훅 실패")
	}

	// ★ Stop 페이로드가 깨졌다 — 플랫폼이 모양을 바꾼 날을 흉내낸다.
	// session_id 는 페이로드가 안 읽히므로 환경의 CLAUDE_CODE_SESSION_ID 로 떨어진다 —
	// 하네스 기본 env 가 그 값을 "cc-session-uuid-1" 로 준다(harness_test.go).
	code, out := h.run("이건 JSON 이 아니다", "hook", "stop")
	if code != 0 {
		t.Fatalf("훅이 종료코드 %d 를 냈다 — fail-open 이 깨졌다", code)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("Stop 페이로드가 안 읽혔는데 처방을 냈다 — "+
			"파싱 실패가 재진입 가드를 0값(false)으로 만들었다:\n%s", out)
	}
}
