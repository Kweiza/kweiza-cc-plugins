package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// landing_resource_test.go — Task 3: service.Land 가 자원 집합의 all-or-nothing 취득이 되는지를 잠근다.
//
// ★ 이 파일의 여섯 시험도 landing_test.go 와 같은 이유로 전부 실물 DB 로 돈다: 두 표
// (resource_hold · landing_queue)가 어긋나는 것이 유일한 치명적 실패 모양이라 가짜
// 저장층으로는 원리적으로 못 본다. 픽스처(twoSessions·newSvc·openSession·ctx·countRows)는
// landing_test.go·helper_test.go 것을 그대로 쓴다.

// TestLandGrantsDisjointResourceSetsIndependently — ① 서로 다른 자원을 요구한 두 세션은
// 서로를 안 막는다. 줄이 자원마다 갈린다는 것이 이 시험의 전제다.
func TestLandGrantsDisjointResourceSetsIndependently(t *testing.T) {
	s, _ := newSvc(t)
	a, b := twoSessions(t, s)

	first, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a, Resources: []string{"r1"}})
	if err != nil {
		t.Fatal(err)
	}
	if first.State != "turn" {
		t.Fatalf("r1 을 요구한 첫 세션이 차례를 못 받았다: %+v", first)
	}

	second, err := s.Land(ctx(), LandInput{Project: "p", SessionID: b, Resources: []string{"r2"}})
	if err != nil {
		t.Fatal(err)
	}
	if second.State != "turn" {
		t.Fatalf("겹치지 않는 자원을 요구한 둘째 세션이 막혔다 — 줄이 자원별로 갈려야 한다: %+v", second)
	}
	if len(second.Blockers) != 0 {
		t.Errorf("겹치는 자원이 없는데 blockers 가 있다: %+v", second.Blockers)
	}
}

// TestLandDoesNotPartiallyAcquire — ② A 가 {r1} 을 쥔 상태에서 B 가 {r1,r2} 로 서면
// waiting 이고 **r2 도 안 잡는다**(all-or-nothing). 부분 취득을 허용하면 그 자체가
// 데드락의 재료다(A 가 r2 를 원하는 순간 서로가 서로를 기다린다).
func TestLandDoesNotPartiallyAcquire(t *testing.T) {
	s, st := newSvc(t)
	a, b := twoSessions(t, s)

	if _, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a, Resources: []string{"r1"}}); err != nil {
		t.Fatal(err)
	}

	got, err := s.Land(ctx(), LandInput{Project: "p", SessionID: b, Resources: []string{"r1", "r2"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "waiting" {
		t.Fatalf("r1 이 이미 쥐어져 있는데 부여됐다 — all-or-nothing 위반: %+v", got)
	}
	var sawR1 bool
	for _, bl := range got.Blockers {
		if bl.Resource != "r1" {
			continue
		}
		sawR1 = true
		if bl.Holder == nil || bl.Holder.SessionID != a {
			t.Errorf("r1 blocker 의 점유자가 %v 다(기대 %s)", bl.Holder, a)
		}
	}
	if !sawR1 {
		t.Fatalf("blockers 에 r1 이 없다: %+v", got.Blockers)
	}

	// ★ 이 시험의 심장 — r2 는 비어 있었는데도 **잡히지 않았다.**
	if _, err := st.HeldBy(ctx(), "p", "r2"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("r2 를 부분 취득했다 — HeldBy(p,r2) 가 %v 다(기대 ErrNotFound)", err)
	}
}

// TestLandFrontOfAQueueIsNotOvertaken — ③ ②의 상태(A 가 r1, B 가 {r1,r2} 로 waiting)에서
// C 가 {r2} 로 서면 waiting 이다. r2 만 보면 아무도 안 쥐고 있지만, B 가 r2 줄의 최선두라
// C 가 새치기하면 굶주림이 생긴다 — 이 시험이 그 새치기를 막는다.
func TestLandFrontOfAQueueIsNotOvertaken(t *testing.T) {
	s, _ := newSvc(t)
	a, b := twoSessions(t, s)
	dirC := tmpBase(t)
	c := openSession(t, s, "p", dirC, dirC, "cc-C", "트랙C").Session.ID

	if _, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a, Resources: []string{"r1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Land(ctx(), LandInput{Project: "p", SessionID: b, Resources: []string{"r1", "r2"}}); err != nil {
		t.Fatal(err)
	}

	got, err := s.Land(ctx(), LandInput{Project: "p", SessionID: c, Resources: []string{"r2"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "waiting" {
		t.Fatalf("r2 는 아직 아무도 안 쥐었는데 C 가 바로 부여받았다 — B 를 새치기했다: %+v", got)
	}
	if len(got.Blockers) != 1 {
		t.Fatalf("blockers 개수가 %d 다(기대 1): %+v", len(got.Blockers), got.Blockers)
	}
	bl := got.Blockers[0]
	if bl.Resource != "r2" || bl.FrontSessionID != b {
		t.Errorf("blocker 가 %+v 다(기대 resource=r2 front_session_id=%s)", bl, b)
	}
	if bl.Holder != nil {
		t.Errorf("r2 는 아무도 안 쥐었는데 holder 가 실렸다: %+v", bl.Holder)
	}
}

// TestLandGrantsAllResourcesAtOnceWhenAllFree — ④ ③에서 A 가 빠지면 B 의 다음 land 가
// {r1,r2} 를 **한 번에** 잡는다. 그 뒤 C 는 여전히 waiting 이다(B 가 r2 도 함께 가져갔으므로).
//
// ★ A 를 빼는 수단은 s.LandReport 가 아니라 store 직접 호출이다 — 아래 본문 주석 참조
// (LandReport 는 아직 LaneResource 하나만 안다. 반납의 일반화는 Task 3 범위 밖이다).
func TestLandGrantsAllResourcesAtOnceWhenAllFree(t *testing.T) {
	s, st := newSvc(t)
	a, b := twoSessions(t, s)
	dirC := tmpBase(t)
	c := openSession(t, s, "p", dirC, dirC, "cc-C", "트랙C").Session.ID

	if _, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a, Resources: []string{"r1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Land(ctx(), LandInput{Project: "p", SessionID: b, Resources: []string{"r1", "r2"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Land(ctx(), LandInput{Project: "p", SessionID: c, Resources: []string{"r2"}}); err != nil {
		t.Fatal(err)
	}

	// ★ s.LandReport 는 안 쓴다 — Task 3 은 Land(취득)만 자원 집합을 다루고, LandReport
	// (반납)는 아직 LaneResource="landing" 하나만 본다(A 는 "landing" 을 쥔 적이 없으므로
	// LandReport 를 부르면 "내 레인이 아니다"로 reclaimed 만 답하고 r1 은 그대로 쥔 채
	// 남는다 — 반납의 일반화는 이 태스크 범위 밖이다). 그래서 A 의 반납은 이 시험이
	// store 를 직접 불러 흉내 낸다(landing_test.go 의 기존 시험들이 어긋난 상태를 만들
	// 때 쓰는 것과 같은 수법).
	if err := st.ReleaseResource(ctx(), "p", "r1", store.Holder{SessionID: a}); err != nil {
		t.Fatalf("A 의 r1 반납 흉내가 실패했다: %v", err)
	}
	if err := st.CloseLandingRowBySession(ctx(), "p", a, model.LandingLeftOK, ""); err != nil {
		t.Fatalf("A 의 줄 행 닫기 흉내가 실패했다: %v", err)
	}

	got, err := s.Land(ctx(), LandInput{Project: "p", SessionID: b, Resources: []string{"r1", "r2"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "turn" {
		t.Fatalf("A 가 빠졌는데 B 가 두 자원을 한 번에 못 잡았다: %+v", got)
	}
	if held, err := st.HeldBy(ctx(), "p", "r1"); err != nil || held.SessionID != b {
		t.Errorf("r1 점유자가 %+v(err=%v) 다(기대 %s)", held, err, b)
	}
	if held, err := st.HeldBy(ctx(), "p", "r2"); err != nil || held.SessionID != b {
		t.Errorf("r2 점유자가 %+v(err=%v) 다(기대 %s)", held, err, b)
	}

	// 대조: C 는 여전히 대기다 — B 가 r2 도 함께 가져갔으니 새로 잡을 것이 없다.
	still, err := s.Land(ctx(), LandInput{Project: "p", SessionID: c, Resources: []string{"r2"}})
	if err != nil {
		t.Fatal(err)
	}
	if still.State != "waiting" {
		t.Fatalf("B 가 r2 를 가져갔는데 C 가 %q 다(기대 waiting): %+v", still.State, still)
	}
}

// TestLandRefusesAChangedResourceSetOnReentry — ⑤ 같은 세션이 다른 집합으로 다시 서면
// 거절(RefusedError)이고, 같은 집합이면 같은 RowID 를 낸다.
func TestLandRefusesAChangedResourceSetOnReentry(t *testing.T) {
	s, _ := newSvc(t)
	a, _ := twoSessions(t, s)

	first, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a, Resources: []string{"r1"}})
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.Land(ctx(), LandInput{Project: "p", SessionID: a, Resources: []string{"r2"}})
	var ref *RefusedError
	if !errors.As(err, &ref) {
		t.Fatalf("자원 집합을 바꿔 다시 섰는데 거절되지 않았다: %v", err)
	}
	msg := ref.Error()
	for _, want := range []string{"r1", "leave"} {
		if !strings.Contains(msg, want) {
			t.Errorf("거절 문구에 %q 가 없다: %q", want, msg)
		}
	}

	same, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a, Resources: []string{"r1"}})
	if err != nil {
		t.Fatalf("같은 집합으로 다시 섰는데 거절됐다: %v", err)
	}
	if same.RowID != first.RowID {
		t.Errorf("같은 집합의 재진입인데 행 번호가 바뀌었다: %d(기대 %d)", same.RowID, first.RowID)
	}
}

// TestLandReentryOfHolderStaysTurn — ⑥ 재진입: turn 인 세션이 같은 집합으로 다시 land 하면
// turn 그대로이고, grant 이벤트는 한 번만 남는다(재진입은 재확인이지 부여가 아니다).
func TestLandReentryOfHolderStaysTurn(t *testing.T) {
	s, st := newSvc(t)
	a, _ := twoSessions(t, s)

	first, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a, Resources: []string{"r1", "r2"}})
	if err != nil {
		t.Fatal(err)
	}
	if first.State != "turn" {
		t.Fatalf("사전 조건이 깨졌다 — 빈 레인에서 차례를 못 받았다: %+v", first)
	}

	again, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a, Resources: []string{"r1", "r2"}})
	if err != nil {
		t.Fatalf("점유자의 재진입 land 가 오류가 됐다: %v", err)
	}
	if again.State != "turn" {
		t.Fatalf("점유자가 같은 집합으로 재진입했는데 %q 다(기대 turn): %+v", again.State, again)
	}
	if again.RowID != first.RowID {
		t.Errorf("재진입이 행 번호를 바꿨다: %d(기대 %d)", again.RowID, first.RowID)
	}

	if n := countRows(t, st,
		`SELECT count(*) FROM event WHERE project = 'p' AND session_id = ? AND kind = 'lane.grant'`, a); n != 1 {
		t.Errorf("grant 이벤트가 %d건이다(기대 1) — 재진입은 재확인이지 부여가 아니다", n)
	}
}
