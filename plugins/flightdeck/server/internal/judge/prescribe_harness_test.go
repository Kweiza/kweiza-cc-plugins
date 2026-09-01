package judge

import (
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 처방은 **그 창이 실제로 부를 수 있는 문법**을 말해야 한다.
//
// ★ 왜 이것이 중요한가. 처방은 매 턴 주입된다 — 그래서 그것이 올바른 문법을 말하면
// MCP 도구 설명이 상주하는 것과 비슷한 효과를 낸다. 반대로 없는 문법을 말하면
// 그 창은 도구를 영영 못 찾는다: codex 는 오늘 훅 전용이라 MCP 표면이 **0**이고,
// 거기에 `land()` 라고 적으면 "그런 함수가 없다"조차 안 나온다. 아무 일도 안 일어난다.

// 처방 하나를 확실히 띄우는 입력 — lane-turn 은 단독으로 나온다.
func harnessPrescription(t *testing.T, harness string) string {
	t.Helper()
	ps := Prescribe(PrescribeInput{
		Now: pt0, SessionID: "me", LaneTurnRow: 3, LastJudgment: pt0, Harness: harness,
	})
	if len(ps) != 1 {
		t.Fatalf("lane-turn 하나만 나와야 한다: %v", keys(ps))
	}
	return ps[0].Text
}

// codex 창은 CLI 문법을 받는다.
func TestPrescriptionsSpeakCLISyntaxForCodex(t *testing.T) {
	got := harnessPrescription(t, model.HarnessCodex)
	for _, want := range []string{"fd land", "fd land --ok", "fd land --leave"} {
		if !strings.Contains(got, want) {
			t.Fatalf("codex 처방에 %q 가 없다:\n%s", want, got)
		}
	}
	// ★ MCP 문법이 **남아 있으면 안 된다** — 둘 다 적으면 그 창이 없는 쪽을 먼저 시도한다.
	for _, bad := range []string{"land()", "land(result=", "land(leave="} {
		if strings.Contains(got, bad) {
			t.Fatalf("codex 처방에 MCP 문법 %q 가 남았다:\n%s", bad, got)
		}
	}
}

// claude 창은 MCP 문법 그대로다.
func TestPrescriptionsKeepMCPSyntaxForClaude(t *testing.T) {
	got := harnessPrescription(t, model.HarnessClaude)
	for _, want := range []string{"land()", "land(result='ok')", "land(leave="} {
		if !strings.Contains(got, want) {
			t.Fatalf("claude 처방에 %q 가 없다:\n%s", want, got)
		}
	}
}

// ★ **미상(빈 값)은 MCP 문법이다.** 우연히 정해지면 안 되는 갈래라 이름으로 못박는다.
//
// 근거: 미선언으로 오는 것은 오늘 사실상 Claude 뿐이다 — codex 훅 다섯은 전부
// `--harness codex` 를 싣기 때문이다(codex-hooks.json). 그리고 MCP 문법을 받은 쪽이
// codex 였을 때의 손해(도구를 못 찾는다)보다, CLI 문법을 받은 쪽이 Claude 였을 때의
// 손해(있는 MCP 도구를 놔두고 Bash 를 쓴다)가 이 함대에서 더 잦다.
func TestUndeclaredHarnessKeepsMCPSyntax(t *testing.T) {
	got := harnessPrescription(t, "")
	if !strings.Contains(got, "land()") {
		t.Fatalf("미상인데 MCP 문법이 아니다:\n%s", got)
	}
}

// note·pick 문구도 같은 규율을 따른다 — lane-turn 만 고치면 나머지가 그대로 샌다.
func TestNoteAndPickPrescriptionsAlsoSwitch(t *testing.T) {
	ps := Prescribe(PrescribeInput{
		Now: pt0, SessionID: "me", TurnPaths: []string{"cmd/fd/hook.go"},
		Others:       []LiveSession{{ID: "01OTHER", Paths: []string{"cmd/fd/hook.go"}}},
		LastJudgment: pt0, NewPaths: 1, Harness: model.HarnessCodex,
	})
	if len(ps) == 0 {
		t.Fatal("처방이 하나도 안 나왔다")
	}
	all := ""
	for _, p := range ps {
		all += p.Text + "\n"
	}
	if strings.Contains(all, "note(kind=") || strings.Contains(all, "pick(item_id=") {
		t.Fatalf("codex 처방에 MCP 문법이 남았다:\n%s", all)
	}
	if !strings.Contains(all, "fd note") {
		t.Fatalf("codex 처방이 CLI 문법을 안 낸다:\n%s", all)
	}
}
