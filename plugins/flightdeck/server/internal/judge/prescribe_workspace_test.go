package judge

import (
	"testing"
	"time"
)

// 워크트리 축 — **카드가 서 있는 작업공간이 곧 그 항목의 것**일 때 선점 판정이 무엇을 봐야 하는가.
//
// ★ 왜 형제 축(SiblingClaims)으로 안 되는가. 형제 축은 `s.cc_session_id` 로 조인한다.
// 그런데 실사용 경로는 사람이 **주 저장소 카드에서 pick 하고 워크트리 안에서 새 대화를
// 연다** — 그 순간 cc 가 갈리고 형제 조인은 원리적으로 못 본다.
//
// 실측(2026-08-11, 원장 unclaimed 발화 118건 전수 · 발화 시점 선점 상태를 추가전용 원장
// item.claim·item.finish·claim.reclaim 에서 복원):
//
//	형제 축 거짓 양성   08-06 을 끝으로 0건  ← 0ec08c7 이 닫았다
//	08-07 이후 "선점 0건" 발화 20건 중 16건이
//	  카드의 워크트리 = .flightdeck/worktrees/<항목 id> 이고
//	  그 항목이 그 순간 실제로 선점돼 있었다.
//	  16건 전수가 `같은 머신 · 다른 cc` 다. 예외가 없다.
//
// ★ 이것이 왜 거짓 양성인가. 처방문은 `pick(item_id=…)` 를 시키는데 그 항목은 남의 카드가
// 쥐고 있어 **그 pick 은 거절된다.** unclaimedPrescription 자신이 못박은 규칙이다 —
// "실행할 수 없는 지시를 싣는 것은 이 개정이 없애려는 결함(문구가 사람을 헛되이 움직이는
// 것)의 재발이다."

// TestWorkspaceClaimSilencesUnclaimed 는 이 축의 전부다 —
// 내가 서 있는 워크트리의 항목이 선점돼 있으면 미선점 처방을 안 낸다.
func TestWorkspaceClaimSilencesUnclaimed(t *testing.T) {
	in := PrescribeInput{
		Now:             time.Now(),
		SessionID:       "card-B",
		TurnPaths:       []string{"internal/service/prescribe.go"},
		WorkspaceClaims: []string{"fd-x"},
	}
	if p, ok := unclaimedPrescription(in); ok {
		t.Fatalf("서 있는 워크트리의 항목이 선점돼 있는데 미선점 처방이 떴다:\n  %s\n"+
			"이 처방이 시키는 pick 은 그 항목이 이미 쥐어져 있어 거절된다 — 실행할 수 없는 지시다", p.Reason)
	}

	// ── 짝: 축이 비면 **반드시** 떠야 한다. 이것이 없으면 위 단정은
	// "unclaimed 를 통째로 끈다"로도 초록불이 난다.
	off := in
	off.WorkspaceClaims = nil
	if _, ok := unclaimedPrescription(off); !ok {
		t.Fatal("아무도 안 쥔 상태인데 미선점 처방이 안 떴다 — 그물이 통째로 죽었다")
	}
}

// TestWorkspaceItemIDReadsOnlyTheConventionRoot 는 축의 **입구**를 잠근다.
//
// ★ 왜 `.claude/worktrees/` 를 빼는가. `pick` 응답이 출력하는 자리는
// `.flightdeck/worktrees/<항목 id>` 하나뿐이다 — 거기의 basename 만 항목 id 라는 보장이 있다.
// `.claude/worktrees/<이름>` 은 하네스가 만드는 자리라 basename 이 항목 id 가 아니다.
// judge/split.go 의 conventionRoots 가 둘을 같이 보는 것은 그쪽 질문이 "여기가 워크트리인가"
// 여서다. 이쪽 질문은 "여기가 **어느 항목**의 작업공간인가"라 더 좁아야 한다.
//
// ★ 거짓 음성 쪽으로 틀리게 둔다. 못 읽으면 처방이 뜰 뿐이고(옛 동작), 잘못 읽으면
// 남의 선점이 이 카드를 조용하게 만든다 — 뒤엣것이 훨씬 비싸다.
func TestWorkspaceItemIDReadsOnlyTheConventionRoot(t *testing.T) {
	cases := []struct {
		worktree string
		want     string
		why      string
	}{
		{"/home/a/repo/.flightdeck/worktrees/fd-x", "fd-x", "pick 이 출력하는 바로 그 자리다"},
		{"/home/a/repo/.flightdeck/worktrees/fd-x/", "fd-x", "끝의 구분자는 의미가 없다"},
		{"/home/a/repo", "", "주 저장소는 어느 항목의 것도 아니다"},
		{"/home/a/repo/.flightdeck/worktrees", "", "항목 이름 자리가 비었다"},
		{"/home/a/repo/.flightdeck/worktrees/fd-x/internal", "", "뿌리가 아니라 그 안이다"},
		{"/home/a/repo/.claude/worktrees/foo", "", "하네스가 만든 자리 — basename 이 항목 id 가 아니다"},
		{"/home/a/repo/.flightdeck/hooks/fd-x", "", ".flightdeck 아래라도 worktrees 가 아니면 항목이 아니다"},
		{"/home/a/repo/wt-fd-x", "", "관례 밖 경로는 안 읽는다"},
		{"", "", "빈 값은 근거가 아니다"},
	}
	for _, c := range cases {
		if got := WorkspaceItemID(c.worktree); got != c.want {
			t.Errorf("WorkspaceItemID(%q) = %q, 기대 %q — %s", c.worktree, got, c.want, c.why)
		}
	}
}
