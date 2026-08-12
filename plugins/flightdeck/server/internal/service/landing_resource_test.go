package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// landing_resource_test.go — Task 3: service.Land 가 자원 집합의 all-or-nothing 취득이 되는지를 잠근다.
// Task 4 는 반납(LandReport)·이탈(LandLeave)·회수(ReleaseLaneRow)가 같은 자원 집합을
// 따라가는지를 이어서 잠근다(파일 하단 "Task 4" 절).
//
// ★ 이 파일의 시험은 전부 landing_test.go 와 같은 이유로 실물 DB 로 돈다: 두 표
// (resource_hold · landing_queue)가 어긋나는 것이 유일한 치명적 실패 모양이라 가짜
// 저장층으로는 원리적으로 못 본다. 픽스처(twoSessions·newSvc·openSession·ctx·countRows·
// newRepoWithWorktree·addItem·claimed)는 landing_test.go·helper_test.go·finish_test.go·
// board_test.go 것을 그대로 쓴다.

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
// ★ A 를 빼는 수단은 s.LandReport(ok) 다 — Task 3 리포트가 남긴 "store 직접 호출로
// 흉내 냈다" 우회는 Task 4 가 LandReport 를 행 기준 자원 집합 반납으로 일반화하며
// 없앴다(A 는 "r1" 하나로 서서 그 자원만 쥐었고, LandReport 는 이제 자기 살아 있는 줄
// 행의 자원 집합을 읽어 그중 쥔 것을 전부 반납한다).
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

	if _, err := s.LandReport(ctx(), LandReportInput{Project: "p", SessionID: a, Kind: model.LandingLeftOK}); err != nil {
		t.Fatalf("A 의 report(ok) 가 실패했다: %v", err)
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

// ─────────────────────────────────────────────────────────────────────────────
// Task 4 — 반납(LandReport)·이탈(LandLeave)·회수(ReleaseLaneRow)가 행의 자원 집합을 따라간다.
// ─────────────────────────────────────────────────────────────────────────────

// divergentHoldsForAnyResource 는 **자원 무관** 어긋남 건수다. landing_test.go 의
// divergentHolds 는 `resource = 'landing'` 으로 고정돼 있어 r1·r2 같은 임의 자원의 어긋남을
// 못 본다 — Task 4 가 반납 계열을 자원 집합으로 넓히면서 그 하드코딩도 시험 쪽에서 걷어야
// 이 개편이 실제로 새는 자리(하나의 자원만 반납하고 나머지를 흘리는 것)를 잠글 수 있다.
//
// resource_hold 의 살아 있는 점유마다: 그 (project, session_id) 의 살아 있는 줄 행이 있고
// **그 행이 이 자원을 담고 있는지**까지 본다(landing_queue_resource 조인) — 자원 필터가
// 없으면 "행은 있는데 그 행의 자원 집합에 이 자원이 없다"는 어긋남(예: r2 만 쥐고 있는데
// 행은 r1 만 담은 경우)을 못 잡는다.
func divergentHoldsForAnyResource(t *testing.T, st *store.Store) int {
	t.Helper()
	return countRows(t, st, `
		SELECT count(*) FROM resource_hold h
		WHERE h.released_at IS NULL
		  AND NOT EXISTS (
		    SELECT 1 FROM landing_queue q
		    JOIN landing_queue_resource r ON r.row_id = q.id
		    WHERE q.project = h.project AND q.session_id = h.session_id AND q.left_at IS NULL
		      AND r.resource = h.resource)`)
}

// TestLandReportReleasesTheWholeResourceSet — ⑦ {r1,r2} 를 쥔 세션의 report(ok) →
// 두 자원 다 반납되고 행이 닫힌다.
func TestLandReportReleasesTheWholeResourceSet(t *testing.T) {
	s, st := newSvc(t)
	a, _ := twoSessions(t, s)

	mine, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a, Resources: []string{"r1", "r2"}})
	if err != nil {
		t.Fatal(err)
	}
	if mine.State != "turn" {
		t.Fatalf("사전 조건이 깨졌다 — 빈 레인에서 두 자원을 못 잡았다: %+v", mine)
	}

	rep, err := s.LandReport(ctx(), LandReportInput{Project: "p", SessionID: a, Kind: model.LandingLeftOK})
	if err != nil {
		t.Fatal(err)
	}
	if rep.State != "released" {
		t.Fatalf("state 가 %q 다(기대 released): %+v", rep.State, rep)
	}
	if rep.RowID != mine.RowID {
		t.Errorf("응답이 다른 줄 행을 가리킨다: %d(기대 %d)", rep.RowID, mine.RowID)
	}

	// ★ 이 시험의 심장 — 두 자원 다 반납됐다. 하나만 보고 통과시키면 나머지가 영영
	// 안 풀리는 레인으로 남는다.
	if _, err := st.HeldBy(ctx(), "p", "r1"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("r1 이 반납 안 됐다 — HeldBy 가 %v 다(기대 ErrNotFound)", err)
	}
	if _, err := st.HeldBy(ctx(), "p", "r2"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("r2 가 반납 안 됐다 — HeldBy 가 %v 다(기대 ErrNotFound)", err)
	}
	if n := countRows(t, st,
		`SELECT count(*) FROM landing_queue WHERE id = ? AND left_at IS NULL`, mine.RowID); n != 0 {
		t.Errorf("줄 행이 안 닫혔다(id=%d)", mine.RowID)
	}
}

// TestReleaseLaneRowTouchesOnlyTheRowsResources — ⑧ 자원 r2 만 걸린 줄 행을 회수해도
// landing 점유는 안 건드린다.
//
// ★ 개편 전엔 빨갛다 — ReleaseLaneRow 가 LaneResource="landing" 을 하드코딩해서 봤으므로,
// B(자원 r2)의 행을 회수해도 실제로는 "landing"(A 가 쥔 것)만 들여다보고 "다른 세션이
// 쥐고 있어 건드리지 않았다"로 접는다. 그러면 B 가 실제로 쥔 r2 는 반납되지 않은 채
// 남고, 행만 force 로 닫혀 **대응하는 줄 행이 없는 살아 있는 r2 점유**가 생긴다.
func TestReleaseLaneRowTouchesOnlyTheRowsResources(t *testing.T) {
	s, st := newSvc(t)
	a, b := twoSessions(t, s)

	holdA, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a})
	if err != nil {
		t.Fatal(err)
	}
	if holdA.State != "turn" {
		t.Fatalf("사전 조건이 깨졌다 — a 가 landing 을 못 잡았다: %+v", holdA)
	}
	holdB, err := s.Land(ctx(), LandInput{Project: "p", SessionID: b, Resources: []string{"r2"}})
	if err != nil {
		t.Fatal(err)
	}
	if holdB.State != "turn" {
		t.Fatalf("사전 조건이 깨졌다 — 겹치지 않는 자원인데 b 가 못 잡았다: %+v", holdB)
	}

	rel, err := s.ReleaseLaneRow(ctx(), "p", holdB.RowID, "aaron", "r2 회수 시험")
	if err != nil {
		t.Fatal(err)
	}
	if !rel.HeldRelease {
		t.Fatalf("r2 를 쥔 채 회수했는데 점유가 안 풀렸다: %+v", rel)
	}
	if _, err := st.HeldBy(ctx(), "p", "r2"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("r2 가 회수 뒤에도 쥐어 있다 — %v(기대 ErrNotFound)", err)
	}
	// ★ 이 시험의 심장 — landing 은 안 건드렸다.
	if held, err := st.HeldBy(ctx(), "p", "landing"); err != nil || held.SessionID != a {
		t.Errorf("landing 점유가 %+v(err=%v) 다(기대 %s) — r2 회수가 landing 을 건드렸다", held, err, a)
	}
	if n := divergentHoldsForAnyResource(t, st); n != 0 {
		t.Errorf("회수 뒤 어긋남이 %d건이다(기대 0)", n)
	}
}

// TestLaneReleaseBodyScopesQueueToOverlappingResources — ⑨ 회수 판단 본문의 "그때 줄에
// 있던 사람"에 다른 자원의 대기자가 안 섞인다.
//
// ★ 개편 전엔 빨갛다 — laneReleaseBody 의 옛 루프가 queue 전체(자원 무관)를 그대로
// 적어서, r2 만으로 선 C 가 r1 회수 판단에 끼어든다.
func TestLaneReleaseBodyScopesQueueToOverlappingResources(t *testing.T) {
	s, st := newSvc(t)
	a, b := twoSessions(t, s)
	dirC := tmpBase(t)
	c := openSession(t, s, "p", dirC, dirC, "cc-C", "트랙C").Session.ID

	holdA, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a, Resources: []string{"r1"}})
	if err != nil {
		t.Fatal(err)
	}
	if holdA.State != "turn" {
		t.Fatalf("사전 조건이 깨졌다: %+v", holdA)
	}
	waitB, err := s.Land(ctx(), LandInput{Project: "p", SessionID: b, Resources: []string{"r1"}})
	if err != nil {
		t.Fatal(err)
	}
	if waitB.State != "waiting" {
		t.Fatalf("사전 조건이 깨졌다 — b 가 r1 대기가 아니다: %+v", waitB)
	}
	turnC, err := s.Land(ctx(), LandInput{Project: "p", SessionID: c, Resources: []string{"r2"}})
	if err != nil {
		t.Fatal(err)
	}
	if turnC.State != "turn" {
		t.Fatalf("사전 조건이 깨졌다 — c 가 r2 를 못 잡았다: %+v", turnC)
	}

	rel, err := s.ReleaseLaneRow(ctx(), "p", holdA.RowID, "aaron", "r1 겹침 시험")
	if err != nil {
		t.Fatal(err)
	}
	j, err := st.GetJudgment(ctx(), rel.JudgmentID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(j.Body, b) {
		t.Errorf("판단 본문에 r1 을 같이 기다리던 %s 가 없다: %s", b, j.Body)
	}
	if strings.Contains(j.Body, c) {
		t.Errorf("판단 본문에 자원이 안 겹치는 c 가 섞였다: %s", j.Body)
	}
}

// TestLiveHoldAlwaysHasALiveRowForAnyResource — ⑩ 살아 있는 자원 점유가 있으면 반드시
// 대응하는 살아 있는 줄 행이 있다 — 임의 자원판.
//
// TestLiveLandingHoldAlwaysHasALiveQueueRow(landing_test.go) 와 같은 방식으로,
// {r1,r2} 시나리오에서 네 반납 경로(report·leave·release·finish) 를 전부 돈다.
// 반납 계열 중 하나라도 여전히 LaneResource="landing" 을 하드코딩해 다른 자원을 흘리면
// 그 경로 직후 divergentHoldsForAnyResource 가 0 이 아니게 된다.
func TestLiveHoldAlwaysHasALiveRowForAnyResource(t *testing.T) {
	s, st := newSvc(t)
	a, b := twoSessions(t, s)
	repo, wt := newRepoWithWorktree(t, "feat")
	fin := openSession(t, s, "p", repo, wt, "cc-fin", "트랙F").Session.ID
	addItem(t, s, "p", "batch9", nil, nil)
	claimed(t, s, "p", fin, "batch9")

	check := func(step string) {
		t.Helper()
		if n := divergentHoldsForAnyResource(t, st); n != 0 {
			t.Fatalf("%s 뒤에 두 표가 어긋났다: %d건 — 레인이 영영 안 풀린다", step, n)
		}
	}

	// ── ① report ──
	if _, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a, Resources: []string{"r1", "r2"}}); err != nil {
		t.Fatal(err)
	}
	check("a 가 {r1,r2} 를 잡은")
	if _, err := s.LandReport(ctx(), LandReportInput{
		Project: "p", SessionID: a, Kind: model.LandingLeftOK}); err != nil {
		t.Fatal(err)
	}
	check("a 가 report 로 반납한")
	if held, err := st.HeldBy(ctx(), "p", "r1"); err == nil {
		t.Fatalf("report 뒤에도 r1 이 쥐어 있다: %+v", held)
	}

	// ── ② leave(쥔 채) — 함정. 줄 행만 닫고 점유를 안 놓으면 여기서 어긋난다.
	turnB, err := s.Land(ctx(), LandInput{Project: "p", SessionID: b, Resources: []string{"r1", "r2"}})
	if err != nil {
		t.Fatal(err)
	}
	if turnB.State != "turn" {
		t.Fatalf("사전 조건이 깨졌다 — b 가 {r1,r2} 를 못 잡았다: %+v", turnB)
	}
	check("b 가 {r1,r2} 를 잡은")
	if _, err := s.LandLeave(ctx(), LandLeaveInput{
		Project: "p", SessionID: b, Detail: "레인을 쥔 채 포기한다"}); err != nil {
		t.Fatal(err)
	}
	check("b 가 쥔 채 leave 한")
	if held, err := st.HeldBy(ctx(), "p", "r2"); err == nil {
		t.Fatalf("leave 뒤에도 r2 가 쥐어 있다: %+v", held)
	}

	// ── ③ release(사람) ──
	turnA, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a, Resources: []string{"r1", "r2"}})
	if err != nil {
		t.Fatal(err)
	}
	if turnA.State != "turn" {
		t.Fatalf("사전 조건이 깨졌다 — leave 뒤 레인이 안 풀렸다: %+v", turnA)
	}
	check("a 가 다시 {r1,r2} 를 잡은")
	if _, err := s.ReleaseLaneRow(ctx(), "p", turnA.RowID, "aaron", "사람이 회수한다"); err != nil {
		t.Fatal(err)
	}
	check("사람이 회수한")
	if held, err := st.HeldBy(ctx(), "p", "r1"); err == nil {
		t.Fatalf("회수 뒤에도 r1 이 쥐어 있다: %+v", held)
	}

	// ── ④ finish ──
	turnFin, err := s.Land(ctx(), LandInput{Project: "p", SessionID: fin, Resources: []string{"r1", "r2"}})
	if err != nil {
		t.Fatal(err)
	}
	if turnFin.State != "turn" {
		t.Fatalf("사전 조건이 깨졌다 — 회수 뒤 레인이 안 풀렸다: %+v", turnFin)
	}
	check("fin 이 {r1,r2} 를 잡은")
	if _, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: fin, ItemID: "batch9",
		Outcome: model.ItemDone, Title: "batch9 랜딩", Body: "① 왜 그렇게 했나 …",
	}); err != nil {
		t.Fatal(err)
	}
	check("fin 이 마무리로 반납한")
	if held, err := st.HeldBy(ctx(), "p", "r2"); err == nil {
		t.Fatalf("finish 뒤에도 r2 가 쥐어 있다: %+v", held)
	}
}
