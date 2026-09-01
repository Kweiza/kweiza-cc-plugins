package service

import (
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 배선 — 원장의 `session.harness` 가 **처방문의 문법까지** 닿는가.
//
// ★ judge 쪽 시험(prescribe_harness_test.go)은 "하네스를 주면 문법이 갈린다"를 잡는다.
// 그것만으로는 부족하다: 이 파일이 없으면 `sess.Harness` 를 안 싣는 한 줄짜리 회귀가
// **아무 시험도 안 깨고** 지나간다 — 판정은 초록인데 codex 창은 그대로 없는 문법을 받는다.
// 이 레포가 워크트리 축에서 정확히 같은 모양을 한 번 겪었다(prescribe_workspace_test.go).

// openWithHarness 는 선언된 하네스로 카드를 연다.
func openWithHarness(t *testing.T, s *Service, harness string) string {
	t.Helper()
	repo := newRepo(t)
	res, err := s.OpenSession(ctx(), OpenSessionInput{
		Project: "p", ProjectPath: repo, MachineID: "m1", Hostname: "box",
		Worktree: repo, CCSessionID: "cc-" + harness + "-창", Harness: harness,
	})
	if err != nil {
		t.Fatalf("세션 열기 실패(harness=%q): %v", harness, err)
	}
	if res.Session.Harness != harness {
		t.Fatalf("원장의 하네스가 %q 다 — %q 여야 한다(이 시험의 전제가 깨졌다)",
			res.Session.Harness, harness)
	}
	return res.Session.ID
}

// 처방 하나를 확실히 띄운다 — 선점 없이 경로를 만지면 unclaimed 가 나온다.
func prescriptionTextFor(t *testing.T, harness string) string {
	t.Helper()
	svc, st := newSvc(t)
	card := openWithHarness(t, svc, harness)
	touchPathForPrescribeTest(t, st, card, "cmd/fd/hook.go")

	res, err := svc.Prescriptions(ctx(), card)
	if err != nil {
		t.Fatalf("처방 실패: %v", err)
	}
	if len(res.All) == 0 {
		t.Fatal("처방이 하나도 안 나왔다 — 이 시험의 전제가 깨졌다")
	}
	var b strings.Builder
	for _, p := range res.All {
		b.WriteString(p.Text)
		b.WriteString("\n")
	}
	return b.String()
}

// codex 카드는 CLI 문법을 받는다.
func TestPrescriptionsReachCodexWithCLISyntax(t *testing.T) {
	got := prescriptionTextFor(t, model.HarnessCodex)
	if !strings.Contains(got, "fd ") {
		t.Fatalf("codex 카드가 CLI 문법을 못 받았다 — 원장→처방 배선이 끊겼다:\n%s", got)
	}
	for _, bad := range []string{"pick(item_id=", "note(kind=", "land()"} {
		if strings.Contains(got, bad) {
			t.Fatalf("codex 카드에 MCP 문법 %q 가 남았다:\n%s", bad, got)
		}
	}
}

// claude 카드는 MCP 문법 그대로다 — 이 축이 오늘 함대의 기본이다.
func TestPrescriptionsReachClaudeWithMCPSyntax(t *testing.T) {
	got := prescriptionTextFor(t, model.HarnessClaude)
	if !strings.Contains(got, "(") || strings.Contains(got, "fd pick <item-id>") {
		t.Fatalf("claude 카드가 MCP 문법이 아니다:\n%s", got)
	}
}
