package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// 랜딩 레인의 전이 시험. 아홉이 각각 무엇을 잠그는지는 시험 이름 위 주석에 있다.
//
// ★ 이 파일의 시험은 전부 **실물 DB** 로 돈다. 두 표(resource_hold · landing_queue)가
// 어긋나는 것이 이 기능의 유일한 치명적 실패 모양이라, 가짜 저장층으로는 그 축을
// 원리적으로 못 본다.

// twoSessions 는 같은 프로젝트에 세션 둘을 연다. 줄 시험은 언제나 둘 이상이 필요하다.
//
// 워크트리를 따로 주는 이유: 세션 정체가 (machine, worktree, cc_session_id) 3중키라
// 같은 워크트리에 두 세션을 열면 정체 어긋남 관측이 함께 켜져 시험의 축이 흐려진다.
func twoSessions(t *testing.T, s *Service) (a, b string) {
	t.Helper()
	dirA, dirB := tmpBase(t), tmpBase(t)
	return openSession(t, s, "p", dirA, dirA, "cc-A", "트랙A").Session.ID,
		openSession(t, s, "p", dirB, dirB, "cc-B", "트랙B").Session.ID
}

// liveQueue 는 지금 줄에 서 있는 행 수다(창 무시).
func liveQueue(t *testing.T, st *store.Store) int {
	t.Helper()
	return countRows(t, st,
		`SELECT count(*) FROM landing_queue WHERE project = 'p' AND left_at IS NULL`)
}

// laneHolders 는 지금 랜딩 레인을 쥐고 있는 세션 수다.
func laneHolders(t *testing.T, st *store.Store, sessionID string) int {
	t.Helper()
	return countRows(t, st, `
		SELECT count(*) FROM resource_hold
		WHERE project = 'p' AND resource = 'landing' AND released_at IS NULL
		  AND (? = '' OR session_id = ?)`, sessionID, sessionID)
}

// divergentHolds 는 **두 표가 어긋난 건수**다. 항상 0이어야 한다 —
// 살아 있는 랜딩 점유에는 반드시 대응하는 살아 있는 줄 행이 있어야 한다.
// 어긋나면 ListLandingQueue 는 아무도 안 보여 주는데 레인은 영영 잡혀 있다.
func divergentHolds(t *testing.T, st *store.Store) int {
	t.Helper()
	return countRows(t, st, `
		SELECT count(*) FROM resource_hold h
		WHERE h.resource = 'landing' AND h.released_at IS NULL
		  AND NOT EXISTS (
		    SELECT 1 FROM landing_queue q
		    WHERE q.project = h.project AND q.session_id = h.session_id AND q.left_at IS NULL)`)
}

// TestTwoSessionsBothKeepTheirRowWhenOnlyOneGetsTheLane — 동시에 서면 하나는 turn, 하나는 waiting.
//
// ★ 둘 다 줄 행이 남는다. ResourceHeldError 를 롤백으로 접으면 줄 행과 순번이 함께 사라져
// 큐에 영원히 한 명만 남고 "순서 큐"라는 이름이 거짓이 된다.
func TestTwoSessionsBothKeepTheirRowWhenOnlyOneGetsTheLane(t *testing.T) {
	s, st := newSvc(t)
	a, b := twoSessions(t, s)

	first, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a})
	if err != nil {
		t.Fatalf("첫 세션의 land 가 실패했다: %v", err)
	}
	if first.State != "turn" || first.Position != 1 {
		t.Fatalf("빈 레인의 첫 세션이 차례를 못 받았다: %+v", first)
	}

	second, err := s.Land(ctx(), LandInput{Project: "p", SessionID: b})
	if err != nil {
		t.Fatalf("점유 실패는 오류가 아니라 순번이어야 한다: %v", err)
	}
	if second.State != "waiting" {
		t.Fatalf("두 번째 세션의 상태가 %q 다(기대 waiting): %+v", second.State, second)
	}
	if second.RowID == 0 {
		t.Errorf("두 번째 세션에 줄 행 번호가 없다 — 줄 행이 롤백으로 사라졌다: %+v", second)
	}
	if second.Position != 2 {
		t.Errorf("두 번째 세션의 순번이 %d 다(기대 2)", second.Position)
	}
	if second.Holder == nil || second.Holder.SessionID != a {
		t.Fatalf("앞사람이 응답에 없다 — 누구에게 물어야 하는지 답이 없다: %+v", second.Holder)
	}
	if second.Holder.LastSignalAt == nil {
		t.Errorf("앞사람의 마지막 신호 시각이 없다 — 나이를 못 재면 회수 판정을 사람이 할 수 없다")
	}

	// ★ 이 시험의 핵심. 두 줄 행이 **둘 다** 살아 있어야 한다.
	if n := liveQueue(t, st); n != 2 {
		t.Fatalf("살아 있는 줄 행이 %d개다(기대 2) — 점유 실패를 롤백으로 접으면 "+
			"큐에 영원히 한 명만 남는다", n)
	}
	if n := laneHolders(t, st, ""); n != 1 {
		t.Errorf("레인 점유가 %d건이다(기대 1)", n)
	}

	// json 태그가 곧 REST·CLI 계약이다. 어긋나면 CLI 가 오류 없이 0값을 찍는다.
	buf, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("응답 직렬화 실패: %v", err)
	}
	for _, want := range []string{`"state":"waiting"`, `"position":2`, `"row_id":`, `"holder":`, `"session_id":`} {
		if !strings.Contains(string(buf), want) {
			t.Errorf("응답 json 에 %s 가 없다 — 태그가 어긋나면 CLI 가 조용히 0값을 찍는다:\n%s",
				want, buf)
		}
	}

	// ★ 취득 실패(ResourceHeldError)를 실제로 밟는 유일한 경로를 여기서 함께 잠근다.
	//   맨 앞이 아닌 세션은 애초에 취득을 시도하지 않으므로(front.ID == mine.ID 가 막는다),
	//   AcquireResource 가 실패하는 것은 **두 표가 어긋난 상태**뿐이다: 점유는 남아 있는데
	//   그 세션의 줄 행이 닫혀 다음 사람이 맨 앞이 된 상태.
	//   그 상태에서 오류로 올려 롤백하면 부를 때마다 줄 행이 사라져 큐가 영원히 빈다.
	if err := st.CloseLandingRow(ctx(), "p", first.RowID, model.LandingLeftForce,
		"두 표를 일부러 어긋낸다(점유는 그대로 두고 줄 행만 닫는다)"); err != nil {
		t.Fatalf("어긋난 상태를 못 만들었다: %v", err)
	}
	stuck, err := s.Land(ctx(), LandInput{Project: "p", SessionID: b})
	if err != nil {
		t.Fatalf("맨 앞인데 남이 쥔 상태에서 land 가 오류를 냈다 — 이 오류는 정상 결과여야 한다: %v", err)
	}
	if stuck.State != "waiting" || stuck.RowID != second.RowID {
		t.Fatalf("어긋난 상태의 응답이 틀렸다: %+v (기대 waiting · 행 %d)", stuck, second.RowID)
	}
	if stuck.Holder == nil || stuck.Holder.SessionID != a {
		t.Errorf("어긋난 상태에서 점유자를 안 실어 보냈다 — 그 상태를 푸는 것은 사람의 회수인데 "+
			"누구를 회수할지 화면이 모른다: %+v", stuck.Holder)
	}
	if n := liveQueue(t, st); n != 1 {
		t.Fatalf("취득 실패 뒤 살아 있는 줄 행이 %d개다(기대 1) — 롤백으로 줄 행이 사라졌다", n)
	}
}

// TestSecondInLineGetsTheLaneOnNextLandAfterFrontLeaves — 맨 앞이 ok 로 빠진 뒤
// 2번째가 부르면 부여된다. 차례를 미는 주체는 다음 호출이다(지연 부여).
func TestSecondInLineGetsTheLaneOnNextLandAfterFrontLeaves(t *testing.T) {
	s, _ := newSvc(t)
	a, b := twoSessions(t, s)

	if _, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a}); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.Land(ctx(), LandInput{Project: "p", SessionID: b})
	if err != nil {
		t.Fatal(err)
	}
	if waiting.State != "waiting" {
		t.Fatalf("사전 조건이 깨졌다 — 두 번째가 대기 상태가 아니다: %+v", waiting)
	}

	rep, err := s.LandReport(ctx(), LandReportInput{Project: "p", SessionID: a, Kind: model.LandingLeftOK})
	if err != nil {
		t.Fatalf("점유자의 ok 보고가 실패했다: %v", err)
	}
	if rep.State != "released" {
		t.Fatalf("보고 뒤 상태가 %q 다(기대 released): %+v", rep.State, rep)
	}

	got, err := s.Land(ctx(), LandInput{Project: "p", SessionID: b})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "turn" || got.Position != 1 {
		t.Fatalf("맨 앞이 빠졌는데 2번째가 차례를 못 받았다: %+v", got)
	}
	if got.RowID != waiting.RowID {
		t.Errorf("차례를 받으며 줄 행이 바뀌었다: 대기 %d → 차례 %d — 재진입은 같은 행이어야 한다",
			waiting.RowID, got.RowID)
	}
}

// TestFailedReportSendsTheSessionToTheBack — fail 로 빠지고 다시 서면 id 가 더 크다.
// 굶주림을 만드는 대신 재시도를 맨 뒤로 보내는 것이 순서 큐의 값이다.
func TestFailedReportSendsTheSessionToTheBack(t *testing.T) {
	s, _ := newSvc(t)
	a, b := twoSessions(t, s)

	mine, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a})
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := s.Land(ctx(), LandInput{Project: "p", SessionID: b})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.LandReport(ctx(), LandReportInput{
		Project: "p", SessionID: a, Kind: model.LandingLeftFail,
		Detail: "검증에서 시험 2건이 깨졌다",
	}); err != nil {
		t.Fatalf("실패 보고가 실패했다: %v", err)
	}

	again, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a})
	if err != nil {
		t.Fatal(err)
	}
	if again.RowID <= theirs.RowID {
		t.Fatalf("실패한 세션이 맨 뒤로 안 갔다: 다시 선 행 %d ≤ 기다리던 행 %d "+
			"(처음 행은 %d)", again.RowID, theirs.RowID, mine.RowID)
	}
	if again.State != "waiting" || again.Position != 2 {
		t.Fatalf("실패한 세션이 다시 서자마자 차례를 받았다: %+v", again)
	}

	next, err := s.Land(ctx(), LandInput{Project: "p", SessionID: b})
	if err != nil {
		t.Fatal(err)
	}
	if next.State != "turn" {
		t.Fatalf("기다리던 세션이 차례를 못 받았다: %+v", next)
	}
}

// TestReclaimedSessionIsToldSoAndItsRowStaysForce — 회수된 세션의 ok 보고가 'reclaimed' 로
// 답하고 줄 행은 force 로 남는다.
//
// ★ ok 로 덮으면 "성공적으로 랜딩했다"는 거짓 기록이 원장에 남는다.
func TestReclaimedSessionIsToldSoAndItsRowStaysForce(t *testing.T) {
	s, st := newSvc(t)
	a, _ := twoSessions(t, s)

	mine, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a})
	if err != nil {
		t.Fatal(err)
	}
	if mine.State != "turn" {
		t.Fatalf("사전 조건이 깨졌다 — 첫 세션이 차례를 못 받았다: %+v", mine)
	}

	const reason = "4시간째 무응답이라 사람이 회수한다"
	rel, err := s.ReleaseLaneRow(ctx(), "p", mine.RowID, "aaron", reason)
	if err != nil {
		t.Fatalf("회수가 실패했다: %v", err)
	}
	if !rel.HeldRelease {
		t.Fatalf("점유 중인 줄 행을 회수했는데 점유가 안 풀렸다: %+v", rel)
	}

	rep, err := s.LandReport(ctx(), LandReportInput{Project: "p", SessionID: a, Kind: model.LandingLeftOK})
	if err != nil {
		t.Fatalf("회수된 세션의 보고가 오류가 됐다 — 사실을 그대로 답해야 한다: %v", err)
	}
	if rep.State != "reclaimed" {
		t.Fatalf("회수된 세션에게 %q 라고 답했다(기대 reclaimed): %+v", rep.State, rep)
	}
	if !strings.Contains(rep.Reason, "무응답") {
		t.Errorf("회수 사유가 응답에 없다 — 세션이 왜 레인을 잃었는지 알 길이 없다: %q", rep.Reason)
	}

	if n := countRows(t, st,
		`SELECT count(*) FROM landing_queue WHERE id = ? AND left_kind = 'force'`, mine.RowID); n != 1 {
		t.Errorf("회수된 줄 행이 force 로 안 남았다(id=%d)", mine.RowID)
	}
	if n := countRows(t, st,
		`SELECT count(*) FROM landing_queue WHERE left_kind = 'ok'`); n != 0 {
		t.Errorf("회수를 ok 로 덮었다 — \"성공적으로 랜딩했다\"는 거짓 기록이 %d건 남았다", n)
	}
}

// TestLeaveWorksWithoutHoldingTheLane — 줄 서 놓고 포기한 세션이 스스로 빠지는 유일한 길이다.
func TestLeaveWorksWithoutHoldingTheLane(t *testing.T) {
	s, st := newSvc(t)
	a, b := twoSessions(t, s)

	if _, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a}); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.Land(ctx(), LandInput{Project: "p", SessionID: b})
	if err != nil {
		t.Fatal(err)
	}

	left, err := s.LandLeave(ctx(), LandLeaveInput{
		Project: "p", SessionID: b, Detail: "다른 항목으로 옮긴다",
	})
	if err != nil {
		t.Fatalf("레인 미보유 세션의 이탈이 실패했다 — 그러면 스스로 빠질 길이 없다: %v", err)
	}
	if left.State != "left" {
		t.Fatalf("이탈 상태가 %q 다(기대 left): %+v", left.State, left)
	}
	if left.RowID != waiting.RowID {
		t.Errorf("이탈이 다른 줄 행을 닫았다: %d(기대 %d)", left.RowID, waiting.RowID)
	}

	if n := countRows(t, st,
		`SELECT count(*) FROM landing_queue WHERE session_id = ? AND left_kind = 'leave'`, b); n != 1 {
		t.Errorf("이탈한 줄 행이 leave 로 안 닫혔다")
	}
	// 앞사람의 점유는 건드리면 안 된다.
	if n := laneHolders(t, st, a); n != 1 {
		t.Errorf("남의 이탈이 점유자의 레인을 건드렸다(점유 %d건)", n)
	}
	if n := liveQueue(t, st); n != 1 {
		t.Errorf("이탈 뒤 살아 있는 줄 행이 %d개다(기대 1)", n)
	}
}

// TestReleaseLaneRowWorksOnAWaitingRow — 점유가 없어도 회수가 성립한다.
//
// ★ 이것이 "죽은 대기자가 큐에 영원히 남는" 것을 막는 자리다. 대상이 레인이 아니라
// 줄 행이라 점유 중이든 대기 중이든 같은 문법으로 빠진다.
func TestReleaseLaneRowWorksOnAWaitingRow(t *testing.T) {
	s, st := newSvc(t)
	a, b := twoSessions(t, s)

	if _, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a}); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.Land(ctx(), LandInput{Project: "p", SessionID: b})
	if err != nil {
		t.Fatal(err)
	}

	rel, err := s.ReleaseLaneRow(ctx(), "p", waiting.RowID, "", "8시간째 신호가 없는 대기자")
	if err != nil {
		t.Fatalf("대기 중 줄 행의 회수가 실패했다 — 점유가 없다는 이유로 막히면 안 된다: %v", err)
	}
	if rel.HeldRelease {
		t.Errorf("대기 중 행을 회수했는데 점유까지 풀었다고 답했다: %+v", rel)
	}
	if rel.SessionID != b || rel.RowID != waiting.RowID {
		t.Errorf("회수 결과의 좌표가 틀렸다: %+v", rel)
	}
	// 점유자의 레인은 그대로여야 한다.
	if n := laneHolders(t, st, a); n != 1 {
		t.Errorf("대기 행 회수가 점유자의 레인을 건드렸다(점유 %d건)", n)
	}
	if n := countRows(t, st,
		`SELECT count(*) FROM landing_queue WHERE id = ? AND left_kind = 'force'`, waiting.RowID); n != 1 {
		t.Errorf("회수된 대기 행이 force 로 안 닫혔다")
	}
}

// TestReleaseLaneRowLeavesAJudgment — 회수 기록이 원장에서 빠지지 않는다.
//
// ★ 판단은 left_detail 의 사본이 아니다 — 서버가 관측한 것(점유 경과·마지막 신호·
// 그때 줄에 있던 사람·행위자)을 더한 더 넓은 기록이다.
func TestReleaseLaneRowLeavesAJudgment(t *testing.T) {
	s, st := newSvc(t)
	a, b := twoSessions(t, s)

	mine, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Land(ctx(), LandInput{Project: "p", SessionID: b}); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 0 {
		t.Fatalf("사전 조건이 깨졌다 — 판단이 이미 %d건 있다", n)
	}

	const reason = "레인이 3시간째 안 풀린다"
	rel, err := s.ReleaseLaneRow(ctx(), "p", mine.RowID, "aaron", reason)
	if err != nil {
		t.Fatal(err)
	}
	if rel.JudgmentID == "" {
		t.Fatalf("회수가 판단 id 를 안 냈다: %+v", rel)
	}

	j, err := st.GetJudgment(ctx(), rel.JudgmentID)
	if err != nil {
		t.Fatalf("회수 판단을 원장에서 못 읽었다: %v", err)
	}
	if j.Kind != model.JudgmentDecision {
		t.Errorf("회수 판단의 종류가 %q 다(기대 decision)", j.Kind)
	}
	for _, want := range []string{reason, a, "aaron"} {
		if !strings.Contains(j.Body, want) {
			t.Errorf("회수 판단 본문에 %q 가 없다:\n%s", want, j.Body)
		}
	}
	// 서버가 관측한 것이 들어 있어야 한다 — 사유 한 줄의 사본이면 이 판단은 아무것도 안 더한다.
	if !strings.Contains(j.Body, b) {
		t.Errorf("그때 줄에 있던 다른 세션(%s)이 판단에 없다:\n%s", b, j.Body)
	}
	if n := countRows(t, st, `
		SELECT count(*) FROM judgment_link
		WHERE judgment_id = ? AND target_kind = 'session' AND target_id = ?`, j.ID, a); n != 1 {
		t.Errorf("회수 판단이 대상 세션에 안 걸렸다 — 나중에 좌표로 못 찾는다")
	}
}

// TestLandingLaneSeparatesZeroFromUnobserved — 0건과 "안 읽었다"가 다르다.
//
// ★ Entries 가 nil 이면 json 이 null 이 되고, 화면에서 "질의가 안 돌았다"와
// "아무도 안 섰다"가 같아진다.
func TestLandingLaneSeparatesZeroFromUnobserved(t *testing.T) {
	s, _ := newSvc(t)
	a, _ := twoSessions(t, s)

	empty, err := s.LandingLane(ctx(), "p")
	if err != nil {
		t.Fatalf("빈 레인 조회가 실패했다: %v", err)
	}
	if empty.Entries == nil {
		t.Fatalf("0건이 nil 로 나왔다 — 화면에서 \"안 읽었다\"와 구분되지 않는다")
	}
	if len(empty.Entries) != 0 || empty.Holder != nil {
		t.Fatalf("아무도 안 섰는데 레인이 비어 있지 않다: %+v", empty)
	}
	buf, err := json.Marshal(empty)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(buf), `"entries":[]`) {
		t.Fatalf("빈 레인의 json 이 %s 다 — entries 는 [] 여야 한다", buf)
	}

	if _, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a}); err != nil {
		t.Fatal(err)
	}
	one, err := s.LandingLane(ctx(), "p")
	if err != nil {
		t.Fatal(err)
	}
	if len(one.Entries) != 1 || one.Entries[0].SessionID != a {
		t.Fatalf("한 명이 섰는데 목록이 %+v 다", one.Entries)
	}
	if one.Holder == nil || one.Holder.SessionID != a {
		t.Fatalf("점유자가 안 보인다: %+v", one.Holder)
	}
	if one.Entries[0].LastSignalAt == nil {
		t.Errorf("줄에 선 세션의 마지막 신호 시각이 없다 — 나이를 못 재면 사람이 회수를 판정할 수 없다")
	}
	if one.Entries[0].EnqueuedAt.IsZero() {
		t.Errorf("대기 시작 시각이 비었다")
	}
}

// TestLiveLandingHoldAlwaysHasALiveQueueRow — 두 표가 어긋난 상태를 잡는다.
//
// ★ resource_hold(resource='landing') 의 살아 있는 점유와 landing_queue 의 살아 있는
// 행은 같은 사실을 표현한다. 어긋나면 ListLandingQueue 는 아무도 안 보여 주는데
// 레인은 영영 잡혀 있고, 그 프로젝트의 랜딩이 전원 정지한다.
func TestLiveLandingHoldAlwaysHasALiveQueueRow(t *testing.T) {
	s, st := newSvc(t)
	a, b := twoSessions(t, s)

	check := func(step string) {
		t.Helper()
		if n := divergentHolds(t, st); n != 0 {
			t.Fatalf("%s 뒤에 두 표가 어긋났다: 대응하는 줄 행이 없는 랜딩 점유 %d건 — "+
				"레인이 영영 안 풀린다", step, n)
		}
	}

	if _, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a}); err != nil {
		t.Fatal(err)
	}
	check("a 가 차례를 받은")

	if _, err := s.Land(ctx(), LandInput{Project: "p", SessionID: b}); err != nil {
		t.Fatal(err)
	}
	check("b 가 줄을 선")

	if _, err := s.LandReport(ctx(), LandReportInput{
		Project: "p", SessionID: a, Kind: model.LandingLeftOK}); err != nil {
		t.Fatal(err)
	}
	check("a 가 ok 로 보고한")

	turn, err := s.Land(ctx(), LandInput{Project: "p", SessionID: b})
	if err != nil {
		t.Fatal(err)
	}
	if turn.State != "turn" {
		t.Fatalf("사전 조건이 깨졌다 — b 가 차례를 못 받았다: %+v", turn)
	}
	check("b 가 차례를 받은")

	// ★ 함정. 레인을 **쥔 채** 이탈하는 세션이다. 줄 행만 닫고 점유를 안 놓으면
	//   여기서 두 표가 어긋나고, 그 뒤로는 아무도 레인을 못 잡는다.
	if _, err := s.LandLeave(ctx(), LandLeaveInput{
		Project: "p", SessionID: b, Detail: "레인을 쥔 채 포기한다"}); err != nil {
		t.Fatal(err)
	}
	check("점유자가 이탈한")

	back, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a})
	if err != nil {
		t.Fatal(err)
	}
	if back.State != "turn" {
		t.Fatalf("점유자가 이탈했는데 레인이 안 풀렸다: %+v — 이것이 두 표가 어긋난 결과다", back)
	}
	check("a 가 다시 차례를 받은")

	if _, err := s.ReleaseLaneRow(ctx(), "p", back.RowID, "aaron", "사람이 회수한다"); err != nil {
		t.Fatal(err)
	}
	check("사람이 회수한")
	if n := laneHolders(t, st, ""); n != 0 {
		t.Fatalf("회수 뒤에도 랜딩 점유가 %d건 남았다", n)
	}
}
