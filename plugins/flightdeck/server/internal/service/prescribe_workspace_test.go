package service

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
)

// 워크트리 축의 **배선** — judge 가 봐야 한다고 정한 것을 아무도 안 채우면
// 판정은 초록불인 채 화면은 그대로 틀린다. 이 파일이 그 배선을 잡는다.
//
// 재는 배치는 실측된 실사용 경로다(2026-08-11, 08-07 이후 "선점 0건" 발화 20건 중 16건):
// 사람이 **주 저장소 카드에서 pick 하고**, 그 워크트리 안에서 **새 대화**를 연다.
// cc 가 갈리므로 형제 축(0ec08c7)은 원리적으로 못 본다.

// openConventionWorktreeCard 는 `<repo>/.flightdeck/worktrees/<항목 id>` 에 선 카드를 낸다.
//
// ★ newRepoWithWorktree 를 쓰면 안 된다 — 그 헬퍼는 `wt-<브랜치>` 를 만들어 **관례 경로가
// 아니다.** 그러면 이 파일의 시험이 아무것도 안 재면서 초록불이 난다.
func openConventionWorktreeCard(t *testing.T, s *Service, itemID, cc string) (repo, card string) {
	t.Helper()
	repo = newRepo(t)
	wt := filepath.Join(repo, ".flightdeck", "worktrees", itemID)
	runGit(t, repo, "worktree", "add", "-q", "-b", itemID, wt)
	res := openSession(t, s, "p", repo, wt, cc, "워크트리 대화")
	if res.Session.Worktree != wt {
		t.Fatalf("카드가 관례 경로에 안 섰다: %q (기대 %q) — 전제가 깨졌다", res.Session.Worktree, wt)
	}
	return repo, res.Session.ID
}

// TestWorkspaceItemHeldByAnotherConversationIsNotCalledUnclaimed 는 이 항목의 전부다.
func TestWorkspaceItemHeldByAnotherConversationIsNotCalledUnclaimed(t *testing.T) {
	svc, st := newSvc(t)
	repo, card := openConventionWorktreeCard(t, svc, "fd-x", "cc-워크트리")

	// 주 저장소에서 **다른 대화**가 그 항목을 쥔다 — 규율 순서 그대로다.
	holder := openSession(t, svc, "p", repo, repo, "cc-주트리", "주 트리 대화").Session.ID
	claimItemForPrescribeTest(t, svc, st, holder, "fd-x", []string{"cmd/fd"})

	touchPathForPrescribeTest(t, st, card, "cmd/fd/hook.go")

	res, err := svc.Prescriptions(ctx(), card)
	if err != nil {
		t.Fatalf("처방 실패: %v", err)
	}
	for _, p := range res.All {
		if p.Key == "unclaimed" {
			t.Fatalf("fd-x 의 워크트리에 서 있고 fd-x 는 선점돼 있는데 미선점 처방이 떴다:\n  %s\n"+
				"쥔 카드 %s(cc-주트리) · 이 카드 %s(cc-워크트리) — cc 가 달라 형제 축이 못 본다\n"+
				"이 처방이 시키는 pick 은 거절된다\n전체: %+v", p.Reason, holder, card, res.All)
		}
	}
}

// TestUnclaimedFiresWhenTheWorkspaceItemIsNotHeld 는 짝이다 —
// 워크트리 이름이 항목이어도 **아무도 안 쥐었으면** 처방이 반드시 떠야 한다.
//
// 이 시험이 없으면 위 시험은 "관례 워크트리에서는 unclaimed 를 끈다"로도 초록불이 난다.
// 그 오답은 진짜 미선점 작업을 잡을 자리를 통째로 없앤다.
func TestUnclaimedFiresWhenTheWorkspaceItemIsNotHeld(t *testing.T) {
	svc, st := newSvc(t)
	_, card := openConventionWorktreeCard(t, svc, "fd-x", "cc-워크트리")

	touchPathForPrescribeTest(t, st, card, "cmd/fd/hook.go")

	res, err := svc.Prescriptions(ctx(), card)
	if err != nil {
		t.Fatalf("처방 실패: %v", err)
	}
	for _, p := range res.All {
		if p.Key == "unclaimed" {
			return
		}
	}
	t.Fatalf("fd-x 를 아무도 안 쥐었는데 미선점 처방이 안 떴다: %+v\n"+
		"워크트리 이름만으로 껐다면 그물이 통째로 죽은 것이다", res.All)
}

// TestUnrelatedClaimDoesNotSilenceTheWorkspaceCard 는 **축이 워크트리이지 프로젝트가 아니다**를
// 잠근다.
//
// 0ec08c7 이 프로젝트 축으로 넓히는 것을 일부러 막았다("남의 대화가 쥔 것은 이 카드를
// 조용하게 만들지 않는다"). 이 축은 그 방벽을 안 건드린다 — 조용해지는 것은
// **내가 서 있는 그 워크트리의 항목**이 쥐어졌을 때뿐이다.
func TestUnrelatedClaimDoesNotSilenceTheWorkspaceCard(t *testing.T) {
	svc, st := newSvc(t)
	repo, card := openConventionWorktreeCard(t, svc, "fd-x", "cc-워크트리")

	// 남의 대화가 쥔 것은 **다른 항목**이다.
	other := openSession(t, svc, "p", repo, repo, "cc-남", "남의 대화").Session.ID
	claimItemForPrescribeTest(t, svc, st, other, "fd-y", []string{"cmd/fd"})

	touchPathForPrescribeTest(t, st, card, "cmd/fd/hook.go")

	res, err := svc.Prescriptions(ctx(), card)
	if err != nil {
		t.Fatalf("처방 실패: %v", err)
	}
	for _, p := range res.All {
		if p.Key == "unclaimed" {
			return
		}
	}
	t.Fatalf("남이 fd-y 를 쥔 것이 fd-x 워크트리 카드의 미선점 처방을 껐다: %+v\n"+
		"워크트리 축이 아니라 프로젝트 축으로 넓힌 것이다 — 배타 방벽이 사라진다", res.All)
}

// TestReleasedWorkspaceClaimDoesNotSilenceForever 는 축의 **시간 경계**를 잠근다.
//
// claim 은 (project, item_id) 업서트 한 행이라 반납해도 행이 남는다. 반납분까지 보면
// 한 번 선점됐던 항목의 워크트리는 **영원히** 조용해진다 — 그 워크트리를 재사용하는
// 다음 세션 전원이 그물 밖으로 빠진다. 살아 있는 선점만 근거다.
//
// ★ 여기서 Closed 축이 대신 막지 못한다. 끝낸 것은 **다른 대화**이므로 SiblingReleasedItems
// 도 cc 로 갈려 못 본다 — 그래서 이 경계는 이 축이 스스로 지켜야 한다.
func TestReleasedWorkspaceClaimDoesNotSilenceForever(t *testing.T) {
	svc, st := newSvc(t)
	repo, card := openConventionWorktreeCard(t, svc, "fd-x", "cc-워크트리")

	holder := openSession(t, svc, "p", repo, repo, "cc-주트리", "주 트리 대화").Session.ID
	claimItemForPrescribeTest(t, svc, st, holder, "fd-x", []string{"cmd/fd"})
	if _, err := svc.Finish(ctx(), FinishInput{
		Project: "p", SessionID: holder, ItemID: "fd-x", Outcome: model.ItemDone,
		Title: "끝냈다", Body: "무엇을 정했고 무엇을 기각했나",
	}); err != nil {
		t.Fatalf("finish 실패: %v", err)
	}

	touchPathForPrescribeTest(t, st, card, "cmd/fd/hook.go")

	res, err := svc.Prescriptions(ctx(), card)
	if err != nil {
		t.Fatalf("처방 실패: %v", err)
	}
	for _, p := range res.All {
		if p.Key == "unclaimed" {
			return
		}
	}
	t.Fatalf("반납된 선점이 이 워크트리를 계속 조용하게 만든다: %+v\n"+
		"claim 행은 반납해도 남는다 — 살아 있는 선점만 근거여야 한다", res.All)
}

// TestWorkspaceClaimDoesNotTurnOnOutside 는 **넓히면서 다른 축을 켜지 않는다**를 잠근다.
//
// 형제 축이 경로를 안 실은 것과 같은 논거다(0ec08c7 의 실측: 형제 경로를 기준으로 삼는
// 순간 11카드에 선언 밖 발자국 82개가 켜지는데 전 기간 outside 총계가 22건이다).
// 워크트리 축도 **항목 id 만** 나르고 선언 경로를 안 나른다.
func TestWorkspaceClaimDoesNotTurnOnOutside(t *testing.T) {
	svc, st := newSvc(t)
	repo, card := openConventionWorktreeCard(t, svc, "fd-x", "cc-워크트리")

	holder := openSession(t, svc, "p", repo, repo, "cc-주트리", "주 트리 대화").Session.ID
	claimItemForPrescribeTest(t, svc, st, holder, "fd-x", []string{"cmd/fd"})

	// 이 카드는 fd-x 의 선언 밖을 만진다 — 그 선언을 기준으로 삼으면 여기서 outside 가 켜진다.
	touchPathForPrescribeTest(t, st, card, "internal/store/item.go")

	res, err := svc.Prescriptions(ctx(), card)
	if err != nil {
		t.Fatalf("처방 실패: %v", err)
	}
	for _, p := range res.All {
		if strings.HasPrefix(p.Key, judge.PrescribeOutside+":") {
			t.Fatalf("워크트리 항목의 선언 경로를 기준으로 outside 가 켜졌다:\n  %s (키 %s)\n"+
				"이 축은 항목 id 만 날라야 한다\n전체: %+v", p.Reason, p.Key, res.All)
		}
	}
}
