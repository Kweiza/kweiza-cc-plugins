package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 훅 시험 — 좌표계는 **훅 stdout 과 종료코드**다.
//
// 이것이 깨지면 세션이 안 뜬다. 그래서 단정은 "함수가 오류를 안 냈다"가 아니라
// "종료코드가 0 이고 stdout 이 훅 계약을 만족한다"이다.

// ★ 어떤 입력에도 종료코드 0. 이 표가 이 파일의 존재 이유다.
func TestHooksAreFailOpenForEveryInput(t *testing.T) {
	h := newHarness(t)
	h.down() // 서버까지 죽여 둔다 — 가장 나쁜 조건에서 단정한다

	valid := `{"session_id":"cc-1","cwd":"/tmp","hook_event_name":"SessionStart","source":"startup"}`
	inputs := []struct{ name, stdin string }{
		{"빈 stdin", ""},
		{"공백만", "   \n  "},
		{"깨진 JSON", "{이건 JSON 이 아니다"},
		{"JSON 이지만 배열", `["세션"]`},
		{"필드가 하나도 없는 객체", `{}`},
		{"session_id 가 숫자", `{"session_id":123}`},
		{"정상", valid},
		{"거대한 tool_input", `{"session_id":"cc-1","tool_input":{"file_path":"` + strings.Repeat("a", 5000) + `"}}`},
	}
	events := []string{"session-start", "user-prompt", "post-tool", "pre-compact", "stop",
		"없는-훅-이름"} // ★ 표 밖: 우리가 모르는 훅 이름도 세션을 막으면 안 된다

	for _, ev := range events {
		for _, in := range inputs {
			t.Run(ev+"/"+in.name, func(t *testing.T) {
				code, _ := h.run(in.stdin, "hook", ev)
				if code != 0 {
					t.Fatalf("훅 %s(%s) 가 종료코드 %d 를 냈다 — 세션이 안 뜬다", ev, in.name, code)
				}
			})
		}
	}
	// 훅 이름 자체가 없는 경우도.
	if code, _ := h.run("", "hook"); code != 0 {
		t.Fatalf("훅 이름 없이 불렀을 때 종료코드 %d — fail-open 이 깨졌다", code)
	}
}

// 서버 미도달일 때 SessionStart 는 **배너를 실제로 낸다.**
// 조용히 두면 에이전트가 조정 기구가 있는 줄 알고 움직인다(설계 §7).
func TestSessionStartEmitsBannerWhenServerIsDown(t *testing.T) {
	h := newHarness(t)
	h.down()

	code, out := h.run(`{"session_id":"cc-1","cwd":"/tmp","hook_event_name":"SessionStart"}`,
		"hook", "session-start")
	if code != 0 {
		t.Fatalf("종료코드 %d", code)
	}
	var payload struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
		t.Fatalf("훅 stdout 이 JSON 이 아니다: %v\n%s", err, out)
	}
	if payload.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Fatalf("hookEventName 이 %q 다", payload.HookSpecificOutput.HookEventName)
	}
	ctx := payload.HookSpecificOutput.AdditionalContext
	mustContain(t, "additionalContext", ctx,
		"⚠ 조정 서버 미도달",
		"되는 것: 코드 작성·커밋·조사 전부",
		"안 되는 것: 새 항목 선점",
		"내 선점:", // 선점 축을 침묵하지 않는다
	)
	// 배너는 **맨 앞**이어야 한다. 뒤에 있으면 긴 보드에 묻힌다.
	if !strings.HasPrefix(strings.TrimSpace(ctx), "⚠") {
		t.Fatalf("배너가 맨 앞이 아니다:\n%s", ctx)
	}
}

// 서버가 살아 있으면 세션을 실제로 등록하고, 그 사실이 additionalContext 에 나온다.
func TestSessionStartRegistersSessionWhenServerIsUp(t *testing.T) {
	h := newHarness(t)
	code, out := h.run(`{"session_id":"cc-live-1","cwd":"/tmp","hook_event_name":"SessionStart"}`,
		"hook", "session-start")
	if code != 0 {
		t.Fatalf("종료코드 %d: %s", code, out)
	}
	if !strings.Contains(out, "flightdeck 세션 신규") {
		t.Fatalf("세션 등록이 화면에 안 나온다:\n%s", out)
	}
	if strings.Contains(out, "조정 서버 미도달") {
		t.Fatalf("서버가 살아 있는데 미도달 배너를 냈다:\n%s", out)
	}
	// 좌표계: 서버가 실제로 세션을 갖게 됐나.
	view, err := h.svc.Board(t.Context(), h.project, service0BoardOptions())
	if err != nil {
		t.Fatalf("보드 조회 실패: %v", err)
	}
	if len(view.Sessions) != 1 {
		t.Fatalf("살아 있는 세션이 %d개다 — 훅이 등록하지 않았다", len(view.Sessions))
	}
}

// PostToolUse 는 **미커밋 발자국의 유일한 원천**이다. 경로가 실제로 저장돼야 한다.
func TestPostToolHookRecordsFootprintPath(t *testing.T) {
	h := newHarness(t)
	if code, out := h.run(`{"session_id":"cc-1","cwd":"/tmp","hook_event_name":"SessionStart"}`,
		"hook", "session-start"); code != 0 {
		t.Fatalf("세션 열기 실패(%d): %s", code, out)
	}
	payload := `{"session_id":"cc-1","cwd":"/tmp","tool_name":"Edit",` +
		`"tool_input":{"file_path":"/tmp/pipeline/x.py"}}`
	if code, _ := h.run(payload, "hook", "post-tool"); code != 0 {
		t.Fatalf("post-tool 훅이 실패했다")
	}
	view, err := h.svc.Board(t.Context(), h.project, service0BoardOptions())
	if err != nil {
		t.Fatalf("보드 조회 실패: %v", err)
	}
	if len(view.Sessions) == 0 {
		t.Fatal("세션이 없다")
	}
	found := false
	for _, p := range view.Sessions[0].View.Paths {
		if strings.Contains(p, "pipeline/x.py") {
			found = true
		}
	}
	if !found {
		t.Fatalf("편집 경로가 발자국에 없다: %v", view.Sessions[0].View.Paths)
	}
}

// PreCompact 는 초안 판단을 남긴다. 무엇을 못 하는지도 본문이 말해야 한다.
func TestPreCompactLeavesDraftJudgment(t *testing.T) {
	h := newHarness(t)
	if code, _ := h.run(`{"session_id":"cc-1","cwd":"/tmp"}`, "hook", "session-start"); code != 0 {
		t.Fatal("세션 열기 실패")
	}
	if code, _ := h.run(`{"session_id":"cc-1","cwd":"/tmp","trigger":"auto"}`,
		"hook", "pre-compact"); code != 0 {
		t.Fatal("pre-compact 훅 실패")
	}
	js := h.judgments(model.JudgmentDraft)
	if len(js) != 1 {
		t.Fatalf("초안이 %d건이다", len(js))
	}
	mustContain(t, "초안 본문", js[0].Body,
		"압축 직전 자동 초안", "trigger=auto", "이 세션이 쥔 항목",
		"판단 본문은 이 초안에 없다")
}

func TestEditedPathsReadsEveryToolShape(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want []string
	}{
		{"Edit", map[string]any{"file_path": "/a/b.go"}, []string{"/a/b.go"}},
		{"Write", map[string]any{"file_path": "/a/c.go", "content": "x"}, []string{"/a/c.go"}},
		{"NotebookEdit", map[string]any{"notebook_path": "/a/n.ipynb"}, []string{"/a/n.ipynb"}},
		{"nil", nil, nil},
		{"경로 없는 도구", map[string]any{"command": "ls"}, nil},
		// ★ 표 밖: 값이 문자열이 아닌 경우(플랫폼이 형식을 바꾼 날). 죽지 않고 0건이어야 한다.
		{"file_path 가 숫자", map[string]any{"file_path": 42}, nil},
		{"file_path 가 빈 문자열", map[string]any{"file_path": "  "}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := EditedPaths(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("%v → %v, %v 를 기대했다", c.in, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("%v → %v, %v 를 기대했다", c.in, got, c.want)
				}
			}
		})
	}
}

func TestParseHookPayloadSeparatesEmptyFromBroken(t *testing.T) {
	if _, err := ParseHookPayload(nil); err == nil ||
		!strings.Contains(err.Error(), "비었다") {
		t.Fatalf("빈 입력의 사유가 '비었다'가 아니다: %v", err)
	}
	if _, err := ParseHookPayload([]byte("{깨짐")); err == nil ||
		!strings.Contains(err.Error(), "JSON 이 아니다") {
		t.Fatalf("깨진 JSON 의 사유가 다르다: %v", err)
	}
	p, err := ParseHookPayload([]byte(`{"session_id":"x","cwd":"/y"}`))
	if err != nil || p.SessionID != "x" || p.CWD != "/y" {
		t.Fatalf("정상 입력을 못 읽었다: %+v %v", p, err)
	}
	// 모르는 필드는 무시한다 — 플랫폼이 필드를 늘리는 날 훅이 죽으면 세션이 안 뜬다.
	if _, err := ParseHookPayload([]byte(`{"session_id":"x","새필드":1}`)); err != nil {
		t.Fatalf("모르는 필드에 죽었다: %v", err)
	}
}
