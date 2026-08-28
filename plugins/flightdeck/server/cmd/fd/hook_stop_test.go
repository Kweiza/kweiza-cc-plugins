package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

// ★ **접힘 줄은 조건 없는 약속을 하면 안 된다(2026-08-12 실측).** 배포 뒤 이틀치 원장에서
// 접힌 키 25개 중 **9개(36%)가 끝내 안 올라왔고**, 9개 전부가 "그 카드에 다음 Stop 훅이
// 안 온" 경우다. 처방은 Stop 훅에서만 뜨므로 대화가 거기서 끝나면 접힌 것은 영영 안 나온다.
//
// 사람이 접힘 시점에 받는 신호는 이 한 줄뿐이다 — 그래서 이 줄이 "다음 턴에 올라온다"로
// 끝나면 36% 에 대해 거짓 약속이 된다. 조건을 적는 것이 아니라 **한계를 적는 것**이라
// (2026-08-09 이 기각한 "같은 조건이면"과 다르다) 세션이 안 해도 될 일을 하게 만들지 않는다:
// 대화를 이어 갈 사람에게는 아무 일도 안 시키고, 끝낼 사람에게만 지금 보라고 말한다.
func TestRenderPrescriptionsDoesNotPromiseATurnThatMayNotCome(t *testing.T) {
	got := RenderPrescriptions([]PrescriptionLine{{Key: "outside:a", Text: "a"}}, 2)
	if !strings.Contains(got, "끝나면") {
		t.Fatalf("접힘 줄이 조건 없는 약속을 한다 — 여기서 끝나면 안 온다는 것을 안 적었다: %q", got)
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

// 처방 응답에 lifecycle 이 실려 오면 decision:block 이 나간다. 처방 문구는 reason 꼬리에 붙는다.
func TestHookStopBlocksOnLifecycleGate(t *testing.T) {
	srv := fakeServer(t, map[string]string{
		"/api/v1/sessions/S1/prescriptions": `{"shown":[{"key":"unclaimed","text":"XYZ-MARK"}],"folded":0,` +
			`"lifecycle":{"stage":"land","reason":"LANE-GATE-REASON"}}`,
	})
	defer srv.Close()

	out := runHookForTest(t, srv.URL, "stop", `{"session_id":"cc-1","cwd":"."}`)
	if !strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("lifecycle 이 있는데 decision:block 이 안 나왔다: %q", out)
	}
	if !strings.Contains(out, "LANE-GATE-REASON") {
		t.Fatalf("라이프사이클 사유가 없다: %q", out)
	}
	if !strings.Contains(out, "XYZ-MARK") {
		t.Fatalf("block 텍스트에 처방 본문이 없다 — 꼬리에 안 붙었다: %q", out)
	}
	if strings.Contains(out, "hookSpecificOutput") {
		t.Fatalf("block 턴에서 additionalContext 도 같이 나갔다: %q", out)
	}
}

// stop_hook_active=true 면 lifecycle 이 있어도 **아무것도** 안 나간다 — 이 가드가
// 루프 방벽의 전부다(억제표는 방벽이 아니다 — emitBlock 의 ★ 문단이 명시로 기각했다).
// ★ 정정: "전부"는 **한 query 호출 안**에서만 참이다. 백그라운드 기상 턴은 새 호출이라
// 이 가드가 안 걸린다 — 그 축은 hookStop 의 대기 가드(background_tasks)가 맡는다.
func TestHookStopNeverBlocksItsOwnTurn(t *testing.T) {
	srv := fakeServer(t, map[string]string{
		"/api/v1/sessions/S1/prescriptions": `{"shown":[{"key":"unclaimed","text":"XYZ-MARK"}],"folded":0,` +
			`"lifecycle":{"stage":"land","reason":"LANE-GATE-REASON"}}`,
	})
	defer srv.Close()

	out := runHookForTest(t, srv.URL, "stop",
		`{"session_id":"cc-1","cwd":".","stop_hook_active":true}`)
	if strings.TrimSpace(out) != "" {
		t.Fatalf("재진입인데 lifecycle 로 뭔가 냈다 — 이게 무한 루프의 씨앗이다: %q", out)
	}
}

// 페이로드 해석 실패면 block 도 additionalContext 도 없다(fail-close 유지).
func TestHookStopStaysSilentOnParseFailure(t *testing.T) {
	out := runHookForTest(t, "http://127.0.0.1:1", "stop", "이건 JSON 이 아니다")
	if strings.TrimSpace(out) != "" {
		t.Fatalf("페이로드 파싱이 실패했는데 뭔가 냈다: %q", out)
	}
}

// lifecycle 이 없으면 기존 그대로 additionalContext 처방만 나간다(회귀 방지).
func TestHookStopStillEmitsPrescriptionsWithoutGate(t *testing.T) {
	srv := fakeServer(t, map[string]string{
		"/api/v1/sessions/S1/prescriptions": `{"shown":[{"key":"unclaimed","text":"XYZ-MARK"}],"folded":0}`,
	})
	defer srv.Close()

	out := runHookForTest(t, srv.URL, "stop", `{"session_id":"cc-1","cwd":"."}`)
	if strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("lifecycle 이 없는데 block 이 나갔다: %q", out)
	}
	if !strings.Contains(out, "hookSpecificOutput") {
		t.Fatalf("lifecycle 이 없는데 기존 additionalContext 도 안 나갔다: %q", out)
	}
	if !strings.Contains(out, "XYZ-MARK") {
		t.Fatalf("처방 본문이 없다: %q", out)
	}
}

// countingServer 는 fakeServer 와 같지만 경로별 요청 수를 함께 낸다.
//
// ★ 이 계수기가 아래 대기 가드 시험들의 **본 축**이다. "출력이 비었나"만 보면 통과하는
// 판이 여럿 있다(호출해 놓고 출력만 삼키는 판도 통과한다) — 그런데 그 판은 틀렸다:
// service/prescribe.go 의 LogEvent(eventPrescribe)/LogEvent(eventPrescribeFolded) 가
// **응답을 만들면서** (세션×키) 억제표를 태우므로, 부르고 나서 삼키면 그 처방은 영영
// 소실된다. 그래서 가드는 출력부가 아니라 **서버 호출 앞**에 있어야 하고, 그 자리를
// 잠그는 것은 이 계수기뿐이다.
func countingServer(t *testing.T, routes map[string]string) (*httptest.Server, func(string) int) {
	t.Helper()
	var mu sync.Mutex
	hits := map[string]int{}
	mux := http.NewServeMux()
	record := func(path string) {
		mu.Lock()
		hits[path]++
		mu.Unlock()
	}
	for path, body := range routes {
		path, body := path, body
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			record(path)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, body)
		})
	}
	if _, ok := routes["/api/v1/sessions"]; !ok {
		mux.HandleFunc("/api/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
			record("/api/v1/sessions")
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"session":{"id":"S1"},"created":false}`)
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, func(path string) int {
		mu.Lock()
		defer mu.Unlock()
		return hits[path]
	}
}

// gateRoutes 는 lifecycle 관문(stage=finish)이 켜진 서버 응답이다 — 가드가 없으면
// 반드시 decision:block 이 나가는 조건이라, 무출력이 관측되면 그것은 가드 때문이다.
func gateRoutes() map[string]string {
	return map[string]string{
		"/api/v1/sessions/S1/prescriptions": `{"shown":[{"key":"unclaimed","text":"XYZ-MARK"}],"folded":0,` +
			`"lifecycle":{"stage":"finish","reason":"FINISH-GATE-REASON"}}`,
	}
}

// ★★ 이 시험이 이 파일에서 stop_hook_active 가드 다음으로 중요하다.
//
// **살아 있는 백그라운드 작업이 있으면 아무것도 안 낸다 — 서버도 안 부른다.**
//
// stop_hook_active 가드는 **한 query 호출 안**의 재진입만 끊는다. 백그라운드 작업이
// 끝나며 만드는 기상 턴은 새 query 호출이라 stop_hook_active 가 false 로 시작하고,
// 그래서 그 턴 끝에 관문이 또 발화한다 — block 이 턴을 되살리고, 되살아난 턴이
// 기다리려고 또 대기 프로세스를 띄우고, 그것이 끝나며 또 기상 턴을 만든다.
// 2026-08-28 실측(figma-agent 전사): block 76회 중 67회(88.2%)가 하네스 추적
// 백그라운드 작업이 살아 있는 동안 떴고, 대기 셸 59개 중 26개(44%)가 block 이
// 되살린 턴에서 났다. 동시 생존 최대 11개.
//
// 하네스는 이 축을 이미 준다 — Stop 페이로드의 background_tasks 는 스키마 설명이
// 문자 그대로 `Lets hooks distinguish "session is done" from "session is paused
// waiting for background work to wake it"` 이다(2.1.250). 마지막 작업이 끝난 턴에는
// 배열이 비므로 관문은 **정확히 한 번** 뜬다 — 공짜 edge-trigger 다.
func TestHookStopStaysSilentWhileBackgroundWorkIsLive(t *testing.T) {
	srv, hits := countingServer(t, gateRoutes())

	out := runHookForTest(t, srv.URL, "stop", `{"session_id":"cc-1","cwd":".",`+
		`"background_tasks":[{"id":"bg-1","type":"local_agent","status":"running"}]}`)

	if strings.TrimSpace(out) != "" {
		t.Fatalf("백그라운드 작업이 살아 있는데 뭔가 냈다 — 이게 대기 셸 증식의 씨앗이다: %q", out)
	}
	if n := hits("/api/v1/sessions/S1/prescriptions"); n != 0 {
		t.Fatalf("처방을 %d번 조회했다 — 가드가 출력부에 있다. 그 판은 억제표를 태우고 "+
			"처방을 영영 잃는다(가드는 서버 호출 앞에 있어야 한다)", n)
	}
}

// ★ **대조군이다 — 이게 없으면 위 시험은 변이가 안 닿아도 초록이다.**
// background_tasks 가 빈 배열이면 가드가 안 걸리고 관문이 예전처럼 block 한다.
// 위 시험이 잡는 것이 "가드가 걸렸다"인지 "훅이 그냥 조용해졌다"인지를 가르는 자리다.
func TestHookStopStillBlocksWhenNoBackgroundWork(t *testing.T) {
	srv, hits := countingServer(t, gateRoutes())

	out := runHookForTest(t, srv.URL, "stop",
		`{"session_id":"cc-1","cwd":".","background_tasks":[],"session_crons":[]}`)

	if !strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("살아 있는 백그라운드 작업이 없는데 block 이 안 나왔다 — "+
			"가드가 과발화해 관문을 통째로 껐다: %q", out)
	}
	if !strings.Contains(out, "FINISH-GATE-REASON") {
		t.Fatalf("관문 사유가 없다: %q", out)
	}
	if n := hits("/api/v1/sessions/S1/prescriptions"); n != 1 {
		t.Fatalf("처방 조회가 %d회다 — 1회여야 한다", n)
	}
}

// session_crons 도 같은 축이다 — ScheduleWakeup·CronCreate·/loop 가 걸어 둔 것이
// 있으면 그 세션은 끝난 것이 아니라 **나중에 깨어날** 세션이다. 스키마 설명이 그렇게
// 말한다("Session-scoped cron tasks … that will wake this session later").
// 크론이 깨우는 턴도 새 query 호출이라 stop_hook_active 가 false 다 — 같은 사슬이다.
func TestHookStopStaysSilentWhileSessionCronIsScheduled(t *testing.T) {
	srv, hits := countingServer(t, gateRoutes())

	out := runHookForTest(t, srv.URL, "stop", `{"session_id":"cc-1","cwd":".",`+
		`"session_crons":[{"id":"cron-1","schedule":"*/5 * * * *","recurring":true}]}`)

	if strings.TrimSpace(out) != "" {
		t.Fatalf("크론이 이 세션을 깨울 예정인데 뭔가 냈다: %q", out)
	}
	if n := hits("/api/v1/sessions/S1/prescriptions"); n != 0 {
		t.Fatalf("처방을 %d번 조회했다 — 가드가 서버 호출 앞에 없다", n)
	}
}

// ★ **가드는 대기 중에 처방도 함께 삼킨다 — 그것이 의도다.** lifecycle 관문이 없는
// (=block 이 애초에 안 나가는) 경우에도 조용해야 한다: 2026-08-04 실측대로
// additionalContext **도** 모델을 다시 부르기 때문이다(하네스 2.1.250 집계부가
// additionalContexts 를 blockingErrors 배열에 함께 담아 턴을 continue 한다).
// 그러니 block 만 막고 처방은 내는 판은 증식을 못 끊는다 — 그 판을 잠그는 자리다.
func TestHookStopSwallowsPlainPrescriptionsWhileWaiting(t *testing.T) {
	srv, hits := countingServer(t, map[string]string{
		"/api/v1/sessions/S1/prescriptions": `{"shown":[{"key":"unclaimed","text":"XYZ-MARK"}],"folded":0}`,
	})

	out := runHookForTest(t, srv.URL, "stop", `{"session_id":"cc-1","cwd":".",`+
		`"background_tasks":[{"id":"bg-1","type":"local_bash","status":"pending"}]}`)

	if strings.TrimSpace(out) != "" {
		t.Fatalf("대기 중인데 처방을 냈다 — additionalContext 도 턴을 되살린다: %q", out)
	}
	if n := hits("/api/v1/sessions/S1/prescriptions"); n != 0 {
		t.Fatalf("처방을 %d번 조회했다 — 억제표를 태웠다", n)
	}
}
