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
func runHookForTest(t *testing.T, url, event, stdin string) string {
	t.Helper()
	env := envOf(map[string]string{
		"FD_URL":       url,
		"FD_STATE_DIR": t.TempDir(),
		"FD_PROJECT":   "testproj",
		"FD_LOG":       "error",
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
