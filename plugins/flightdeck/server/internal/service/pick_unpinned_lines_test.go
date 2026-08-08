package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/gitreader"
	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
)

// pick 응답에서 **카탈로그만 있고 봉인이 없던 자리**를 닫는다
// (항목 fd-pick-response-lines-still-unpinned).
//
// ★ 규율: 여기 있는 시험은 전부 **변이를 먼저 넣고 전 스위트가 초록인 것을 눈으로 본 뒤**
// 쓴 것이다. 이 축은 "코드는 맞고 시험이 없다"라 보통의 RED 가 안 나오고, 순서를 뒤집는
// 것 말고는 "이 시험이 무엇을 잡는가"를 알 방법이 없다. 각 시험 주석의 "실측" 줄이
// 그 관측이다(2026-08-09, `go test ./internal/... ./cmd/fd/`).
//
// ★ 안 닫은 것도 하나 있다 — candidates() 의 정렬 **방향**이다. 아래
// TestCandidateOrderDoesNotDependOnWhichSourceSuppliedTheItem 의 주석을 보라.

// TestPickExplicitReasonNamesTheItemItClaimed 은 단독 선점의 Reason 이 **실제 값**을
// 싣는다는 것을 못박는다.
//
// 이 저장소는 "Reason 은 실제 값을 싣는다"를 규율로 세워 뒀다 — 묶음 Reason 이 정렬 네
// 키의 실제 값을 싣는 것이 그 규율이다. 단독 경로의 Reason 만 안 물려 있었다: `item.ID`
// 를 빼고 "항목을 선점했다"로 바꿔도 전 스위트가 초록이었다(실측).
//
// ★ 항목 **둘**을 각각 집어 교차로 본다. 하나만 보면 두 id 를 다 싣는 문장도, 그 id 를
// 우연히 담은 상수 문자열도 통과한다 — 있음만 재는 단정은 출력을 넓히는 변경에 전부 눈감는다.
func TestPickExplicitReasonNamesTheItemItClaimed(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "alpha-item", nil, nil)
	addItem(t, s, "p", "beta-item", nil, nil)

	reasons := map[string]string{}
	for _, id := range []string{"alpha-item", "beta-item"} {
		res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID, ItemID: id})
		if err != nil {
			t.Fatalf("선점 실패(%s): %v", id, err)
		}
		if res.Mode != PickClaimed {
			t.Fatalf("%s 의 mode 가 %s 다(기대 %s)", id, res.Mode, PickClaimed)
		}
		reasons[id] = res.Reason
	}

	if !strings.Contains(reasons["alpha-item"], "alpha-item") {
		t.Fatalf("사유가 집은 항목의 id 를 안 싣는다: %q", reasons["alpha-item"])
	}
	if !strings.Contains(reasons["beta-item"], "beta-item") {
		t.Fatalf("사유가 집은 항목의 id 를 안 싣는다: %q", reasons["beta-item"])
	}
	// 짝 단정 — **남의 id 는 없다.** 이게 없으면 두 id 를 다 싣는 문장이 통과하고,
	// 그러면 사유는 "무엇을 집었나"에 답하지 못한다.
	if strings.Contains(reasons["alpha-item"], "beta-item") {
		t.Fatalf("alpha 를 집은 사유가 beta 도 말한다: %q", reasons["alpha-item"])
	}
	if strings.Contains(reasons["beta-item"], "alpha-item") {
		t.Fatalf("beta 를 집은 사유가 alpha 도 말한다: %q", reasons["beta-item"])
	}
}

// TestPickResumeReasonNamesTheSessionThatHoldsIt 은 **재개** 사유가 쥔 세션을 이름으로
// 말한다는 것을 못박는다.
//
// 항목 본문은 "단독 경로의 Reason 만 규율이 안 물려 있다"고 적었는데, 그 단독 경로는 둘로
// 갈린다 — 새 선점과 재개. 재개 쪽도 세션 id 를 빼면 전 스위트가 초록이었다(실측).
//
// 이 갈래에서 세션 id 가 값을 하는 이유가 따로 있다: 재개는 **컨텍스트가 날아간 세션이
// 같은 워크트리로 돌아오는 자리**이고, 정체가 3중키(머신·워크트리·cc)라 한 워크트리에
// 카드가 여럿일 수 있다. "이미 이 세션의 선점이다"에서 '이 세션'이 어느 카드인지가
// 실제로 모호한 판이라, 이름이 빠지면 돌아온 세션은 자기 것인지 형제 카드 것인지 못 가른다.
func TestPickResumeReasonNamesTheSessionThatHoldsIt(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	one := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	two := openSession(t, s, "p", repo, wt, "cc-2", "트랙3")
	addItem(t, s, "p", "held-by-one", nil, nil)
	addItem(t, s, "p", "held-by-two", nil, nil)

	for _, tc := range []struct{ item, holder, other string }{
		{"held-by-one", one.Session.ID, two.Session.ID},
		{"held-by-two", two.Session.ID, one.Session.ID},
	} {
		if _, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: tc.holder,
			ItemID: tc.item}); err != nil {
			t.Fatalf("선점 준비 실패(%s): %v", tc.item, err)
		}
		again, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: tc.holder, ItemID: tc.item})
		if err != nil {
			t.Fatalf("재개 실패(%s): %v", tc.item, err)
		}
		if again.Mode != PickResumed {
			t.Fatalf("%s 의 mode 가 %s 다(기대 %s)", tc.item, again.Mode, PickResumed)
		}
		if !strings.Contains(again.Reason, tc.holder) {
			t.Fatalf("재개 사유가 쥔 세션을 안 말한다: %q", again.Reason)
		}
		// 짝 단정 — 남의 세션 id 는 없다. 없으면 두 카드를 다 싣는 문장이 통과하고,
		// 그러면 '이 세션'의 모호함이 그대로 남는다.
		if strings.Contains(again.Reason, tc.other) {
			t.Fatalf("재개 사유가 쥐지 않은 세션(%s)도 말한다: %q", tc.other, again.Reason)
		}
	}
}

// TestPickBundleReasonNamesTheLeadThatBecomesTheBranch 는 묶음 사유가 **선두를
// 이름으로** 말한다는 것을 못박는다.
//
// 그 문장이 브랜치 이름의 근거다: 선두 id 가 곧 브랜치이고 워크트리 디렉토리다
// (pickBundle 의 "선두는 원자다" 계약). "선두 %s 가 브랜치가 된다"를 "첫 항목이
// 브랜치가 된다"로 바꿔도 전 스위트가 초록이었다(실측) — 그러면 응답을 받은 세션은
// 자기가 준 순서를 기억해야만 브랜치 이름을 알 수 있고, 컨텍스트가 날아간 채 돌아온
// 세션(이 경로가 존재하는 이유)은 그 순서를 모른다.
//
// ★ 묶음 **둘**을 각각 집어 교차로 본다. 그리고 res.Branch 와 대조한다 — 사유가 말하는
// 선두와 서버가 실제로 브랜치로 삼은 것이 같아야 그 문장이 근거 노릇을 한다.
func TestPickBundleReasonNamesTheLeadThatBecomesTheBranch(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	for _, id := range []string{"lead-one", "member-one", "lead-two", "member-two"} {
		addItem(t, s, "p", id, nil, nil)
	}

	for _, tc := range []struct{ lead, member string }{
		{"lead-one", "member-one"},
		{"lead-two", "member-two"},
	} {
		res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID,
			ItemIDs: []string{tc.lead, tc.member}})
		if err != nil {
			t.Fatalf("묶음 선점 실패(선두 %s): %v", tc.lead, err)
		}
		if res.Bundle == nil {
			t.Fatalf("묶음 축이 nil 이다(선두 %s) — nil 은 '이 축을 안 읽었다'는 뜻이다", tc.lead)
		}
		if res.Branch != tc.lead {
			t.Fatalf("브랜치가 %q 다(기대 %q)", res.Branch, tc.lead)
		}
		if !strings.Contains(res.Bundle.Reason, tc.lead) {
			t.Fatalf("묶음 사유가 브랜치가 될 선두(%s)를 안 말한다: %q", tc.lead, res.Bundle.Reason)
		}
		// 짝 단정 — 구성원 id 는 그 문장에 없다. 구성원까지 실으면 "이 중 어느 것이
		// 브랜치인가"가 다시 모호해져 그 문장이 근거이기를 멈춘다.
		if strings.Contains(res.Bundle.Reason, tc.member) {
			t.Fatalf("묶음 사유가 구성원(%s)도 선두처럼 말한다: %q", tc.member, res.Bundle.Reason)
		}
	}
}

// midPickWriter 는 **Ancestry 가 불리는 순간**에 훅을 거는 리더다.
//
// ★ 시계 주입(newSvcWithClock)과 같은 규율이다: 주입한 함수 안에서 DB 를 건드리면 그
// 지점이 결정론적인 경합 창이 된다. Pick 의 추천 경로에서 afterFacts 는 candidates()
// **뒤**·응답 조립 **앞**에 돈다 — 다른 세션의 add 가 그 사이에 착지하는 실물 상황이
// 여기 한 자리로 압축된다. 실물로는 시험이 못 만드는 실패라 주입을 쓴다.
type midPickWriter struct {
	GitReader
	onAncestry func()
}

func (r midPickWriter) Ancestry(ctx context.Context, sha, tip string) (judge.AncestryResult, error) {
	r.onAncestry()
	return r.GitReader.Ancestry(ctx, sha, tip)
}

// TestRecommendationQueueOpenIsTheCandidateObservationNotARecount 는 추천 응답의
// 큐 수가 **후보를 센 그 관측**이라는 것을 못박는다.
//
// fillQueueOpen 의 `if res.QueueOpen != nil { return }` 가드를 지워도 전 스위트가
// 초록이었다(실측). 이웃한 TestPickRecommendationQueueSizeCannotDivergeFromItsOwnScopeLine
// 이 이름으로는 바로 이 사실을 말하는데도 그렇다 — 그 시험은 **두 줄이 갈릴 수 있는
// 창을 안 만들기** 때문이다: 쓰기가 없으면 ListOpen 과 CountOpen 은 술어가 같아
// 언제나 같은 수를 내고, 재계수는 값으로 드러나지 않는다.
//
// 그래서 이 시험은 그 창을 연다. 창이 열린 판에서만 "두 번 세면 응답의 두 줄이 갈린다"가
// 관측 가능한 사실이 된다.
func TestRecommendationQueueOpenIsTheCandidateObservationNotARecount(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	head := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))

	// 선행 sha 가 있어야 afterFacts 가 git 을 부른다 — 그 호출이 창의 좌표다.
	addItem(t, s, "p", "a-item", nil, []model.After{{SHA: head}})
	addItem(t, s, "p", "b-item", nil, nil)

	opened := 0
	racing := New(st, nil, WithGitFactory(func(repoPath string) GitReader {
		return midPickWriter{GitReader: gitreader.New(repoPath), onAncestry: func() {
			if opened > 0 {
				return // 후보마다 불려도 창은 한 번만 연다
			}
			opened++
			addItem(t, s, "p", "c-added-mid-pick", nil, nil)
		}}
	}))

	res, err := racing.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("추천 실패: %v", err)
	}

	// ★ 대조 먼저 — 창이 진짜 열렸나. 이걸 안 보면 아래 단정이 "아무 일도 안 일어났다"에
	// 대해 공짜로 참이 되고, 이 시험은 상시 통과하는 소음이 된다.
	if opened == 0 {
		t.Fatal("Ancestry 가 안 불렸다 — 창이 안 열렸으므로 이 시험은 아무것도 재지 않았다")
	}
	if n, cerr := st.CountOpen(ctx(), "p"); cerr != nil || n != 3 {
		t.Fatalf("창 뒤 열린 항목이 %d건이다(기대 3, err=%v) — 삽입이 착지하지 않았다", n, cerr)
	}

	if res.QueueOpen == nil {
		t.Fatal("큐 열림 수가 안 실렸다 — nil 은 '이 응답에 없다'는 뜻이다")
	}
	// 후보를 셀 때 열린 항목은 둘이었다. 중간에 착지한 셋째는 이 응답의 후보가 아니었으므로
	// 이 응답의 수에 들어오면 안 된다 — 들어오면 같은 응답이 "후보 2건"이라 적어 놓고
	// 큐를 3으로 센다.
	if *res.QueueOpen != 2 {
		t.Fatalf("큐 열림 %d건(기대 2) — 후보를 센 뒤 다시 셌다", *res.QueueOpen)
	}
	// 관계로 단정한다 — 수를 두 번 하드코딩하지 않는다. 재계수는 이 응답의 두 줄을
	// 갈라놓는 방식으로만 관측되므로, 봐야 할 것은 "둘이 같은 수를 말하는가"다.
	if want := fmt.Sprintf("열린 항목 %d건", *res.QueueOpen); !strings.Contains(res.Scope, want) {
		t.Fatalf("범위 문장이 %q 라 큐 수(%d)와 갈렸다 — 같은 관측에서 나와야 한다",
			res.Scope, *res.QueueOpen)
	}
}

// TestCandidateOrderDoesNotDependOnWhichSourceSuppliedTheItem 은 후보 순서가
// **출처와 무관**하다는 것을 못박는다.
//
// candidates() 는 두 출처를 합친다 — 열린 항목(ListOpen)과 살아 있는 세션이 쥔 항목
// (ClaimedItems, 뒤에 덧붙는다). 정렬이 없으면 순서가 "열린 것 먼저, 쥔 것 나중"이 되고
// 그 안에서 다시 live 순회 순서에 의존한다. 그러면 같은 큐 상태가 **누가 무엇을 쥐고
// 있느냐에 따라** 다른 순서를 내고, 그 순서는 부적격 후보의 탈락 사유 목록 순서로 그대로
// 나간다(judge.Eligible 은 부적격을 입력 순서대로 쌓는다).
//
// ★ **정렬 방향은 일부러 안 못박는다.** 방향을 뒤집어도 전 스위트가 초록이고(실측),
// 그것이 참인 사실이 아니기 때문이다: judge 가 lessCandidate(의존자 → 나이 → id)로
// 완전 순서를 다시 만들므로 추천 결과는 방향과 무관하고, 남는 것은 사람이 읽는 목록의
// 표시 순서뿐이다. 사전순 오름차순은 관습이지 시스템의 참이 아니다 — 그것을 문자열로
// 못박으면 목록 순서를 다듬는 정상 변경마다 시험이 붉어지고, 그러면 시험이 소음이 된다.
// 여기서 닫는 것은 그 아래에 있는 **참인 사실**이다: 출처가 순서를 정하지 않는다.
func TestCandidateOrderDoesNotDependOnWhichSourceSuppliedTheItem(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	other := openSession(t, s, "p", repo, wt, "cc-2", "트랙3")
	for _, id := range []string{"a-first", "b-middle", "c-last"} {
		addItem(t, s, "p", id, nil, nil)
	}

	// 가운데 것만 남이 쥔다 — 그래야 두 출처의 순서(a,c 다음에 b)와 통합 순서가 갈린다.
	if _, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: other.Session.ID,
		ItemID: "b-middle"}); err != nil {
		t.Fatalf("남의 선점 준비 실패: %v", err)
	}

	proj, err := s.st.GetProject(ctx(), "p")
	if err != nil {
		t.Fatalf("프로젝트 조회 실패: %v", err)
	}
	cands, _, _, err := s.candidates(ctx(), proj, []judge.LiveSession{{ID: other.Session.ID}})
	if err != nil {
		t.Fatalf("후보 수집 실패: %v", err)
	}

	ids := make([]string, 0, len(cands))
	for _, c := range cands {
		ids = append(ids, c.Item.ID)
	}
	// 대조 — 남이 쥔 항목이 후보에 **들어 있다**. 이게 없으면 그 출처를 통째로 빼는
	// 변경이 아래 순서 단정을 공짜로 통과한다(남은 둘은 어떤 순서든 정렬돼 보인다).
	if len(ids) != 3 {
		t.Fatalf("후보가 %v 다(기대 3건) — 남이 쥔 항목이 후보에서 빠졌다", ids)
	}

	// 방향은 묻지 않는다. **한 방향으로 정렬돼 있는가**만 묻는다 —
	// 출처가 순서를 정하면 [a-first, c-last, b-middle] 이 되어 어느 방향도 아니다.
	asc, desc := true, true
	for i := 1; i < len(ids); i++ {
		if ids[i-1] > ids[i] {
			asc = false
		}
		if ids[i-1] < ids[i] {
			desc = false
		}
	}
	if !asc && !desc {
		t.Fatalf("후보 순서가 %v 다 — 어느 방향으로도 정렬돼 있지 않다: "+
			"출처(열린 항목 먼저, 쥔 항목 나중)가 순서를 정하고 있다", ids)
	}
}
