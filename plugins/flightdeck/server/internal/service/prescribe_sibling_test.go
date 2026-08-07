package service

import (
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 형제 카드 축 — 한 대화가 카드 여러 장일 때 선점 판정이 무엇을 봐야 하는가.
//
// ★ 왜 이 축이 따로 필요한가. 세션 정체는 3중키(machine·worktree·cc)라 `git worktree add`
// 로 트리를 옮기면 **정의상 새 카드**가 난다. 그 갈림은 결함이 아니라 설계다 —
// DESIGN §3 · cmd/fd/env.go 의 resolveProject · cmd/fd/coord_test.go 가 "서로 다른
// 워크트리는 여전히 갈린다"를 셋이 같은 문장으로 잠갔고, judge/split.go 는 이 배치를
// 조상-자손 거짓 양성 실측 때문에 **일부러** 갈림에서 뺐다. 그러니 여기서 카드를
// 합치려 들면 안 된다.
//
// 문제는 갈림이 아니라 그 귀결이다: 선점은 MCP 가 쓰고(프로세스 기동 시 워크트리가
// 고정된다) 발자국·처방은 훅이 쓴다(매 이벤트 cwd 로 다시 푼다). 그래서 **선점은 카드 A
// 에, 발자국은 카드 B 에** 쌓인다. `pick` 응답 자신이 `git worktree add` 를 지시하고
// 선점이 그보다 먼저 오는 규율 순서상, 규율대로 일한 세션은 전부 이 배치가 된다.
//
// 실측(2026-08-06, 원장 unclaimed 처방 80건 전수): 카드 단위로 선점을 쥔 채 발화 **0건**,
// 대화 단위로 선점을 쥔 채 발화 **10건**. 앞의 0 은 게이트가 한 번도 안 뚫렸다는 뜻이고,
// 뒤의 10 은 그 열 번 모두 선점이 달린 카드와 발자국이 쌓인 카드가 달랐다는 뜻이다.
// 두 수의 차이가 이 파일이 잠그는 것이다.

// openSiblingCards 는 같은 대화·같은 머신인데 워크트리만 다른 카드 둘을 낸다.
//
// ★ **전제를 단정하고 시작한다.** 카드가 실제로 갈리지 않으면 이 파일의 시험은
// 아무것도 안 재면서 초록불이 난다. 더구나 그 단정은 "워크트리 축을 접어서 고치는"
// 오답을 이 자리에서 빨갛게 만든다 — 그 오답은 DESIGN §3 을 되돌리는 것이다.
func openSiblingCards(t *testing.T, s *Service, cc string) (cardA, cardB string) {
	t.Helper()
	repo, wt := newRepoWithWorktree(t, "fd-x")
	a := openSession(t, s, "p", repo, repo, cc, "주 트리")
	b := openSession(t, s, "p", repo, wt, cc, "워크트리")
	if a.Session.ID == b.Session.ID {
		t.Fatalf("카드가 안 갈렸다 — 이 시험은 갈린 상태를 재는 것이라 전제가 깨졌다\n"+
			"worktree %q 와 %q 가 한 좌표로 접혔다면 DESIGN §3(조상 트리 등록을 물려받지 않는다)이 무너진 것이다",
			repo, wt)
	}
	return a.Session.ID, b.Session.ID
}

// TestSiblingWorktreeCardIsNotCalledUnclaimed 는 이 항목의 전부다 —
// **형제 카드가 쥔 선점을 판정이 봐야 한다.**
//
// 선점은 카드 A, 발자국은 카드 B. 오늘은 B 에서 "항목을 선점하지 않고 …" 가 뜬다.
// 그 문구는 B 기준으로 정직하다 — service/prescribe.go 가 in.Claims 를 `ClaimedItems(sessionID)`
// 로 **이 카드 하나**만 조회하기 때문이다. 형제의 선점은 그 판정에 원리적으로 안 들어온다.
func TestSiblingWorktreeCardIsNotCalledUnclaimed(t *testing.T) {
	svc, st := newSvc(t)
	cardA, cardB := openSiblingCards(t, svc, "cc-1")

	// 규율 순서 그대로다 — 선점이 워크트리 생성보다 먼저다.
	claimItemForPrescribeTest(t, svc, st, cardA, "fd-x", []string{"cmd/fd"})
	// 그 뒤 워크트리로 들어가 일한다. 훅은 그쪽 카드에 쓴다.
	touchPathForPrescribeTest(t, st, cardB, "cmd/fd/hook.go")

	res, err := svc.Prescriptions(ctx(), cardB)
	if err != nil {
		t.Fatalf("처방 실패: %v", err)
	}
	for _, p := range res.All {
		if p.Key == "unclaimed" {
			t.Fatalf("형제 카드가 fd-x 를 쥐고 있는데 미선점 처방이 떴다:\n  %s\n"+
				"선점 카드 %s · 발자국 카드 %s — 같은 대화(cc-1)·같은 머신이고 워크트리만 다르다\n"+
				"이것이 규율대로 일한 세션이 매번 받는 거짓 양성이다\n전체: %+v",
				p.Reason, cardA, cardB, res.All)
		}
	}
}

// TestUnclaimedFiresWhenNoSiblingHoldsAClaim 은 짝이다 —
// 형제가 아무것도 안 쥐고 있으면 처방이 **반드시** 떠야 한다.
//
// 이 시험이 없으면 위 시험은 "unclaimed 를 통째로 끈다"로도 초록불이 난다.
func TestUnclaimedFiresWhenNoSiblingHoldsAClaim(t *testing.T) {
	svc, st := newSvc(t)
	_, cardB := openSiblingCards(t, svc, "cc-1")

	touchPathForPrescribeTest(t, st, cardB, "cmd/fd/hook.go")

	res, err := svc.Prescriptions(ctx(), cardB)
	if err != nil {
		t.Fatalf("처방 실패: %v", err)
	}
	for _, p := range res.All {
		if p.Key == "unclaimed" {
			return
		}
	}
	t.Fatalf("아무도 선점을 안 쥔 대화인데 미선점 처방이 안 떴다: %+v\n"+
		"이 그물이 죽으면 진짜 미선점 작업을 잡을 자리가 없다", res.All)
}

// TestAnotherConversationsClaimDoesNotSilenceUnclaimed 는 반대 짝이다 —
// **남의 대화**가 쥔 선점은 이 카드를 조용하게 만들면 안 된다.
//
// 이 시험이 없으면 "선점이 어디엔가 있으면 끈다"로 새는 수정이 초록불이 난다.
// 그러면 대화 축이 아니라 프로젝트 축으로 넓힌 것이고, 배타 방벽이 통째로 사라진다.
func TestAnotherConversationsClaimDoesNotSilenceUnclaimed(t *testing.T) {
	svc, st := newSvc(t)
	_, cardB := openSiblingCards(t, svc, "cc-1")

	// 같은 프로젝트·같은 머신이지만 **다른 대화**가 항목을 쥔다.
	// ★ openSessionForPrescribeTest 를 쓰면 안 된다 — 그 헬퍼는 cc 를 "cc-1" 로 고정하므로
	// 형제 카드가 되어 이 시험이 재려는 것을 안 재게 된다(처음에 그렇게 썼다가 잡혔다).
	otherRepo := newRepo(t)
	other := openSession(t, svc, "p", otherRepo, otherRepo, "cc-2", "남의 대화").Session.ID
	claimItemForPrescribeTest(t, svc, st, other, "fd-x", []string{"cmd/fd"})

	touchPathForPrescribeTest(t, st, cardB, "cmd/fd/hook.go")

	res, err := svc.Prescriptions(ctx(), cardB)
	if err != nil {
		t.Fatalf("처방 실패: %v", err)
	}
	for _, p := range res.All {
		if p.Key == "unclaimed" {
			return
		}
	}
	t.Fatalf("남의 대화가 쥔 선점이 이 카드의 미선점 처방을 껐다: %+v\n"+
		"대화 축이 아니라 프로젝트 축으로 넓힌 것이다 — 배타 방벽이 사라진다", res.All)
}

// TestSiblingFinishDoesNotLookLikeUnclaimedWork 는 같은 갈림의 **마무리 쪽 갈래**다.
//
// finish 는 MCP 로 간다(카드 A). 그래서 선점이 반납되는 순간 형제 선점도 0이 되고,
// 훅이 처방을 내는 카드 B 는 "한 번도 안 집었다"와 똑같은 상태가 된다 — 방금 그 항목의
// 일을 제대로 끝냈는데도. 카드 단위에서 이미 한 번 고친 결함(Closed 축)이 대화 단위에서
// 그대로 남아 있는 것이다.
//
// 이것이 `fd-footprint-paths-keep-the-worktree-prefix` 가 "규율대로 마무리한 세션 전부가
// 오탐 처방을 맞는다"고 부른 증상의 한 갈래다 — 그쪽은 경로 접두 축, 이쪽은 선점 축이다.
func TestSiblingFinishDoesNotLookLikeUnclaimedWork(t *testing.T) {
	svc, st := newSvc(t)
	cardA, cardB := openSiblingCards(t, svc, "cc-1")

	claimItemForPrescribeTest(t, svc, st, cardA, "fd-x", []string{"cmd/fd"})
	touchPathForPrescribeTest(t, st, cardB, "cmd/fd/hook.go")

	// 규율대로 끝낸다 — MCP 로 가므로 카드 A 다.
	if _, err := svc.Finish(ctx(), FinishInput{
		Project: "p", SessionID: cardA, ItemID: "fd-x", Outcome: model.ItemDone,
		Title: "끝냈다", Body: "무엇을 정했고 무엇을 기각했나",
	}); err != nil {
		t.Fatalf("finish 실패: %v", err)
	}

	res, err := svc.Prescriptions(ctx(), cardB)
	if err != nil {
		t.Fatalf("처방 실패: %v", err)
	}
	for _, p := range res.All {
		if p.Key == "unclaimed" {
			t.Fatalf("형제 카드가 방금 제대로 끝냈는데 미선점 처방이 떴다:\n  %s\n"+
				"finish 는 MCP(카드 %s)로 가고 처방은 훅(카드 %s)에서 뜬다 — 반납이 형제 축에서 안 보인다\n전체: %+v",
				p.Reason, cardA, cardB, res.All)
		}
	}
}

// TestSiblingClaimDoesNotTurnOnOutside 는 **넓히면서 다른 축을 켜지 않는다**를 잠근다.
//
// ★ 왜 이것이 따로 필요한가. 형제의 선점을 `in.Claims` 에 합쳐 넣으면 unclaimed 는
// 닫히지만 그 순간 `outside` 축이 **형제의 선언 경로**를 기준으로 돌기 시작한다.
// 실측: 그러면 11카드에 선언 경로 밖 발자국 82개(한 카드 최대 45)가 켜지는데,
// 전 기간 outside 발화 총계가 22건이다 — 4배가 한 판에 쏟아진다.
//
// 그래서 형제 선점은 **경로 없는 별도 축**으로 들어와야 한다. 축에 경로를 안 싣는 것
// 자체가 그 결정을 코드 모양으로 못박는다(judge 가 Closed 를 Claims 와 안 합친 것과 같은 논거).
func TestSiblingClaimDoesNotTurnOnOutside(t *testing.T) {
	svc, st := newSvc(t)
	cardA, cardB := openSiblingCards(t, svc, "cc-1")

	// 형제가 쥔 항목은 cmd/fd 만 선언한다.
	claimItemForPrescribeTest(t, svc, st, cardA, "fd-x", []string{"cmd/fd"})
	// 이 카드는 그 선언 밖을 만진다 — 형제 경로를 기준으로 삼으면 여기서 outside 가 켜진다.
	touchPathForPrescribeTest(t, st, cardB, "internal/store/item.go")

	res, err := svc.Prescriptions(ctx(), cardB)
	if err != nil {
		t.Fatalf("처방 실패: %v", err)
	}
	for _, p := range res.All {
		if p.Key == "outside" {
			t.Fatalf("형제의 선언 경로를 기준으로 outside 가 켜졌다:\n  %s\n"+
				"형제 선점은 경로를 안 실어야 한다 — 실으면 실측상 82개 발자국이 한 판에 켜진다\n전체: %+v",
				p.Reason, res.All)
		}
	}
	// 이 카드 자신은 아무것도 안 쥐었지만 형제가 쥐었으므로 unclaimed 도 조용해야 한다.
	for _, p := range res.All {
		if p.Key == "unclaimed" {
			t.Fatalf("형제가 쥐고 있는데 미선점 처방이 떴다: %s\n전체: %+v", p.Reason, res.All)
		}
	}
}
