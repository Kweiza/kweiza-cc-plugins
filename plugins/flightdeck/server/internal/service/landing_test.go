package service

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

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

// TestReentrantLandByTheHolderStaysTurn — 이미 레인을 쥔 세션이 land 를 다시 불러도 turn 이다.
//
// ★ 저장층 둘의 재진입 성질이 **반대**라 서비스가 그것을 이어야 한다:
// EnqueueLanding 은 재진입 안전이라 기존 행을 그대로 내주는데, AcquireResource 는 같은
// 점유자여도 유니크 위반을 ResourceHeldError 로 바꾼다. 안 이으면 점유자가
// {waiting, position:1, holder:자기 자신} 을 듣고 **자기 자신을 기다린다** —
// 그 세션은 "레인을 못 받았다"고 믿으므로 report·leave 를 안 부르고, 레인은 교착한다.
func TestReentrantLandByTheHolderStaysTurn(t *testing.T) {
	s, st := newSvc(t)
	a, b := twoSessions(t, s)

	first, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a})
	if err != nil {
		t.Fatal(err)
	}
	if first.State != "turn" {
		t.Fatalf("사전 조건이 깨졌다 — 첫 land 가 차례를 못 받았다: %+v", first)
	}

	again, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a})
	if err != nil {
		t.Fatalf("점유자의 재진입 land 가 오류가 됐다: %v", err)
	}
	if again.State != "turn" {
		t.Fatalf("점유자가 다시 부르자 %q 로 답했다 — 이 세션은 자기 자신을 기다리게 되고 "+
			"report·leave 를 안 불러 레인이 교착한다: %+v", again.State, again)
	}
	if again.Holder != nil {
		t.Errorf("점유자에게 앞사람을 실어 보냈다 — 그 앞사람은 자기 자신이다: %+v", again.Holder)
	}
	if again.RowID != first.RowID || again.Position != 1 {
		t.Errorf("재진입이 자리를 바꿨다: %+v (기대 행 %d · 순번 1)", again, first.RowID)
	}

	// 재진입이 줄이나 점유를 늘리지 않는다.
	if n := liveQueue(t, st); n != 1 {
		t.Errorf("재진입 뒤 살아 있는 줄 행이 %d개다(기대 1)", n)
	}
	if n := laneHolders(t, st, a); n != 1 {
		t.Errorf("재진입 뒤 점유가 %d건이다(기대 1)", n)
	}
	// 뒤에 선 세션의 순서도 안 흔들린다.
	second, err := s.Land(ctx(), LandInput{Project: "p", SessionID: b})
	if err != nil {
		t.Fatal(err)
	}
	if second.State != "waiting" || second.Position != 2 {
		t.Errorf("재진입이 뒷사람의 순서를 흔들었다: %+v", second)
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

// TestSecondReportAfterReleasingSaysTheRowIsAlreadyClosed — **이미 닫힌 줄 행**에 다시
// 보고하면 사유가 "줄 행이 <종류> 로 이미 닫혀 있다"다. reclaimed 의 넷째 갈래이고,
// 이 커밋 전까지 어느 계층에도 시험이 없었다.
//
// ★ 이 갈래만 사유를 laneLeftReason 이 **지어낸다**. ok·finish 는 사유가 면제라
// left_detail 이 비어 있고(store.ValidateLandingLeave), 그래서 여기서 빈 문자열이 새면
// 화면에는 "land · 이 레인은 네 것이 아니다 — " 까지만 찍힌다 — 세션이 할 수 있는 것은
// 같은 호출을 반복하는 것뿐이다. laneLeftReason 이 스스로 못박은 "절대 빈 문자열을
// 내지 않는다"를 동작으로 잠그는 자리다.
//
// ★ 그리고 **두 번째 보고는 원장을 한 글자도 안 건드린다.** 닫힌 종류를 ok 로 덮으면
// 마무리가 닫은 행이 "세션이 성공적으로 반납했다"로 둔갑한다.
func TestSecondReportAfterReleasingSaysTheRowIsAlreadyClosed(t *testing.T) {
	s, st := newSvc(t)
	a, b := twoSessions(t, s)

	mine, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a})
	if err != nil {
		t.Fatal(err)
	}
	waiting, err := s.Land(ctx(), LandInput{Project: "p", SessionID: b})
	if err != nil {
		t.Fatal(err)
	}
	if mine.State != "turn" || waiting.State != "waiting" {
		t.Fatalf("사전 조건이 깨졌다: %+v · %+v", mine, waiting)
	}

	// ── ① 마무리가 닫은 행. `fd-handoff` 뒤 습관적으로 `fd land --ok` 를 치는 것이 실제 경로다.
	//    b 는 레인을 쥔 적이 없으므로 점유는 앞사람 것 그대로다 — 어긋난 상태가 아니다.
	if err := st.CloseLandingRowBySession(ctx(), "p", b, model.LandingLeftFinish, ""); err != nil {
		t.Fatal(err)
	}
	fin, err := s.LandReport(ctx(), LandReportInput{Project: "p", SessionID: b, Kind: model.LandingLeftOK})
	if err != nil {
		t.Fatalf("닫힌 행에 보고했더니 오류가 됐다 — 사실을 그대로 답해야 한다: %v", err)
	}
	if fin.State != "reclaimed" {
		t.Fatalf("state 가 %q 다(기대 reclaimed): %+v", fin.State, fin)
	}
	if strings.TrimSpace(fin.Reason) == "" {
		t.Fatalf("사유가 비었다 — 화면은 \"네 것이 아니다 — \" 까지만 찍고 세션은 폴링밖에 못 한다: %+v", fin)
	}
	for _, want := range []string{"이미 닫혀", string(model.LandingLeftFinish)} {
		if !strings.Contains(fin.Reason, want) {
			t.Errorf("사유에 %q 가 없다(%q) — 이 갈래는 종류 자체가 \"왜\"인데 그것이 안 실렸다",
				want, fin.Reason)
		}
	}
	if fin.RowID != waiting.RowID {
		t.Errorf("응답이 다른 줄 행을 가리킨다: %d(기대 %d)", fin.RowID, waiting.RowID)
	}
	if n := countRows(t, st,
		`SELECT count(*) FROM landing_queue WHERE id = ? AND left_kind = 'finish'`, waiting.RowID); n != 1 {
		t.Errorf("보고가 닫힌 종류를 덮었다(id=%d) — 마무리가 닫은 행이 세션의 반납으로 둔갑한다", waiting.RowID)
	}

	// ── ② 정상 반납 뒤 한 번 더 보고 ──
	rel, err := s.LandReport(ctx(), LandReportInput{Project: "p", SessionID: a, Kind: model.LandingLeftOK})
	if err != nil || rel.State != "released" {
		t.Fatalf("사전 조건이 깨졌다 — 점유자의 ok 보고가 %+v (오류 %v) 다", rel, err)
	}
	rowsBefore := countRows(t, st, `SELECT count(*) FROM landing_queue WHERE project = 'p'`)

	again, err := s.LandReport(ctx(), LandReportInput{Project: "p", SessionID: a, Kind: model.LandingLeftOK})
	if err != nil {
		t.Fatalf("두 번째 보고가 오류가 됐다: %v", err)
	}
	if again.State != "reclaimed" {
		t.Fatalf("state 가 %q 다(기대 reclaimed): %+v", again.State, again)
	}
	if strings.TrimSpace(again.Reason) == "" {
		t.Fatalf("사유가 비었다: %+v", again)
	}
	for _, want := range []string{"이미 닫혀", string(model.LandingLeftOK)} {
		if !strings.Contains(again.Reason, want) {
			t.Errorf("사유에 %q 가 없다(%q)", want, again.Reason)
		}
	}
	if again.RowID != mine.RowID {
		t.Errorf("응답이 다른 줄 행을 가리킨다: %d(기대 %d)", again.RowID, mine.RowID)
	}
	// ★ 두 번째 보고는 아무것도 안 만든다. 줄 행이 늘면 습관적 폴링이 원장을 부풀린다.
	if n := countRows(t, st, `SELECT count(*) FROM landing_queue WHERE project = 'p'`); n != rowsBefore {
		t.Errorf("두 번째 보고가 줄 행을 %d개로 늘렸다(기대 %d)", n, rowsBefore)
	}
	if n := laneHolders(t, st, ""); n != 0 {
		t.Errorf("아무도 안 쥐어야 하는데 점유가 %d건이다", n)
	}
}

// TestLandReportRefusesKindsThatOnlyOtherPathsMayWrite — 보고 종류 관문(ok|fail).
//
// ★ 이 관문에 **도달하는 표면은 MCP 하나**다: toolLand 가 result 인자를 그대로 Kind 로
// 옮긴다. CLI 는 ok|fail 을 하드코딩해 보내므로 원리적으로 여기 못 온다 — 그래서 계약은
// service 에서 잠근다. 관문이 없으면 `land result=finish` 한 번이 세션 명의로
// **left_kind='finish'(= 마무리가 닫았다)를 원장에 위조한다.** finish 는 사유가 면제라
// 뒤의 store.ValidateLandingLeave 도 그것을 안 막는다.
//
// ★ 사유를 **함께 준다.** 안 주면 사유 필수 판정에 먼저 걸려, 이 관문을 통째로 지워도
// 시험이 초록인 채로 남는다.
//
// ★ 진짜 단정은 거절 문구가 아니라 **거절이 아무것도 하지 않았다**는 것이다.
func TestLandReportRefusesKindsThatOnlyOtherPathsMayWrite(t *testing.T) {
	s, st := newSvc(t)
	a, _ := twoSessions(t, s)

	mine, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a})
	if err != nil || mine.State != "turn" {
		t.Fatalf("사전 조건이 깨졌다 — 레인을 못 잡았다: %+v (오류 %v)", mine, err)
	}

	for _, kind := range []model.LandingLeftKind{
		model.LandingLeftFinish, model.LandingLeftForce, model.LandingLeftLeave, "", "bogus",
	} {
		out, rerr := s.LandReport(ctx(), LandReportInput{
			Project: "p", SessionID: a, Kind: kind, Detail: "사유는 있다",
		})
		var ref *RefusedError
		if !errors.As(rerr, &ref) {
			t.Fatalf("종류 %q 가 거절되지 않았다: %+v (오류 %v)", kind, out, rerr)
		}
		// 거절은 처방까지 낸다 — 무엇을 대신 하라는 말이 없으면 같은 호출이 반복된다.
		if !strings.Contains(ref.Guidance, "leave") {
			t.Errorf("종류 %q 의 거절이 대신 무엇을 하라는지 말하지 않는다: %q", kind, ref.Guidance)
		}
	}

	// ★ 거절은 아무것도 하지 않는다.
	if n := laneHolders(t, st, a); n != 1 {
		t.Errorf("거절당한 보고가 점유를 풀었다(점유 %d건)", n)
	}
	if n := countRows(t, st,
		`SELECT count(*) FROM landing_queue WHERE project = 'p' AND left_kind IS NOT NULL`); n != 0 {
		t.Errorf("거절당한 보고가 줄 행을 %d건 닫았다 — 세션이 남의 낱말을 원장에 박았고 "+
			"landing_queue 는 이력이라 되돌릴 수 없다", n)
	}
	if n := countRows(t, st,
		`SELECT count(*) FROM landing_queue WHERE id = ? AND left_at IS NULL`, mine.RowID); n != 1 {
		t.Errorf("거절당한 보고가 자기 줄 행을 닫았다(id=%d)", mine.RowID)
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

// TestLaneReleaseJudgmentSaysWhenTheSignalCouldNotBeRead — 신호를 못 읽은 것을
// "신호가 없다"로 적지 않는다.
//
// ★ 판단은 불변으로 남는 기록이라, 관측 실패를 사실로 적으면 그 거짓이 영구히 박히고
// 되짚을 방법이 없다. 표시용 응답(LastSignalAt)은 둘 다 빈칸으로 접어도 되지만
// 원장은 아니다 — 못 읽은 축은 값으로 채우지 않고 그 사실을 적는다(pick.go 의 규율).
func TestLaneReleaseJudgmentSaysWhenTheSignalCouldNotBeRead(t *testing.T) {
	s, st := newSvc(t)
	a, _ := twoSessions(t, s)

	mine, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a})
	if err != nil {
		t.Fatal(err)
	}
	// 신호 조회만 실패시킨다. 이 세션은 신호를 **실제로 남겼으므로**(세션 열기가 Beat 한다)
	// "없음"이 나오면 그것은 거짓이다.
	if n := countRows(t, st, `SELECT count(*) FROM signal WHERE session_id = ?`, a); n == 0 {
		t.Fatalf("사전 조건이 깨졌다 — 이 세션에 신호가 하나도 없다")
	}
	if _, err := st.DB().ExecContext(ctx(), `ALTER TABLE signal RENAME TO signal_hidden`); err != nil {
		t.Fatalf("신호 조회를 실패시키지 못했다: %v", err)
	}

	rel, err := s.ReleaseLaneRow(ctx(), "p", mine.RowID, "aaron", "신호를 못 읽는 상태에서의 회수")
	if err != nil {
		t.Fatalf("신호 조회 실패가 회수를 죽였다 — 그러면 물린 레인을 푸는 유일한 길이 막힌다: %v", err)
	}
	j, err := st.GetJudgment(ctx(), rel.JudgmentID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(j.Body, "신호를 한 번도 안 남겼다") {
		t.Fatalf("못 읽은 것을 \"신호가 없다\"는 사실로 원장에 적었다:\n%s", j.Body)
	}
	if !strings.Contains(j.Body, "읽지 못했다") {
		t.Fatalf("관측 실패를 판단에 안 적었다 — 나중에 이 회수가 무엇을 보고 한 것인지 알 수 없다:\n%s", j.Body)
	}
}

// TestLandingLaneStillReportsRealDivergenceAfterTheRecheck — 재확인이 **진짜 어긋남을
// 숨기지 않는다.**
//
// LandingLane 은 줄과 점유자를 트랜잭션 밖 별개 질의로 읽어서, 사이에 land 가 커밋되면
// "점유자는 있는데 줄 행이 없다"를 거짓으로 낼 수 있다. 그래서 어긋나 보일 때 줄을 한 번
// 더 읽어 재확인하는데 — 그 재확인이 과하면 정반대의 사고가 된다: 진짜 어긋남
// (레인은 영영 잡혀 있고 줄은 비어 보이는 상태)이 조용히 "비어 있음"으로 접히고,
// 그러면 그 프로젝트의 랜딩이 전원 정지한 사실을 아무도 못 본다.
//
// 여기서는 점유는 그대로 둔 채 줄 행만 저장층으로 직접 닫아 그 상태를 실제로 만든다.
func TestLandingLaneStillReportsRealDivergenceAfterTheRecheck(t *testing.T) {
	s, st := newSvc(t)
	a, _ := twoSessions(t, s)

	if _, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a}); err != nil {
		t.Fatal(err)
	}
	// 점유는 안 건드리고 줄 행만 닫는다 — 이것이 정확히 그 어긋난 모양이다.
	if err := st.CloseLandingRowBySession(ctx(), "p", a, model.LandingLeftFinish, ""); err != nil {
		t.Fatal(err)
	}
	if n := divergentHolds(t, st); n != 1 {
		t.Fatalf("사전 조건이 깨졌다 — 어긋난 점유가 %d건이다(기대 1)", n)
	}

	lane, err := s.LandingLane(ctx(), "p")
	if err != nil {
		t.Fatalf("어긋난 상태에서 레인 조회가 실패했다 — 그러면 그 상태를 볼 유일한 창이 닫힌다: %v", err)
	}
	if lane.Holder == nil {
		t.Fatalf("재확인이 진짜 어긋남을 삼켰다 — 점유자를 nil 로 접으면 화면이 \"비어 있음\"을 " +
			"내고 랜딩 전원 정지가 안 보인다")
	}
	if lane.Holder.SessionID != a {
		t.Errorf("점유자가 %q 다(기대 %q)", lane.Holder.SessionID, a)
	}
	if lane.Entries == nil {
		t.Fatalf("0건이 nil 로 나왔다 — \"안 읽었다\"와 구분되지 않는다")
	}
	if len(lane.Entries) != 0 {
		t.Errorf("줄 행을 다 닫았는데 항목이 %d건 보인다: %+v", len(lane.Entries), lane.Entries)
	}

	// ★ 여기서부터는 나중에 더한 단정이다(위 단정은 그대로 둔다). 이 상태가 곧
	//   **LandingLane 이 점유자 신호를 위해 질의를 한 번 더 도는 유일한 갈래**라서,
	//   그 갈래를 이미 만들어 둔 이 시험이 그 값을 잠그기에 가장 값싼 자리다.
	//
	//   정상 경로는 줄 루프에서 읽은 값을 재사용하므로 이 갈래의 질의를 지워도(값을 nil 로)
	//   service·web·mcpsrv·cmd/fd 가 전부 초록이었다 — 변이로 확인했다. 그런데 이 값에는
	//   실물 독자가 있다: 줄이 0건인 지금 같은 모양에서 mcpsrv/render.go 의 어긋남 문장과
	//   web/page.go 의 Missing 행이 회수 판정용 "신호 나이"로 이 필드만 읽는다.
	//   빈칸이 되면 두 화면이 조용히 "신호 없음"이 되고, 사람이 회수를 판정할 두 숫자 중
	//   하나가 사라진다.
	//
	//   기대값을 시험이 지어내지 않고 저장층에 되묻는다 — 여기서 상수를 심으면 잠그는 것이
	//   "서비스가 원장을 읽어 왔나"가 아니라 "시험이 심은 값과 같나"가 된다.
	want, ok, err := st.LastSignal(ctx(), a)
	if err != nil {
		t.Fatalf("점유자의 마지막 신호를 원장에서 못 읽었다: %v", err)
	}
	if !ok {
		t.Fatalf("사전 조건이 깨졌다 — 이 세션에 신호가 하나도 없다(세션 열기가 비트를 남긴다)")
	}
	if lane.Holder.LastSignalAt == nil {
		t.Fatalf("어긋난 갈래에서 점유자의 마지막 신호가 비었다 — 원장에는 %v 가 있다. "+
			"줄이 0건이라 이 필드가 나이를 말하는 유일한 자리인데, 화면이 \"신호 없음\"을 낸다", want)
	}
	if !lane.Holder.LastSignalAt.Equal(want) {
		t.Errorf("점유자의 마지막 신호가 %v 다(원장은 %v)", lane.Holder.LastSignalAt, want)
	}
}

// TestLandingLaneHolderSignalIsTheHoldersOwnNotAQueueMates — 점유자의 마지막 신호가
// **그 세션 자신의 값**인지 잠근다(정상 경로 — 점유자가 줄에 있을 때).
//
// ★ 이 시험 전까지 LaneView.Holder.LastSignalAt 은 **채워지는지 자체가 시험 밖**이었다.
// 정상 경로의 채움을 통째로 죽여도(값을 nil 로) service·web·mcpsrv·cmd/fd 가 전부 초록이었다 —
// 변이로 확인했다. 화면 쪽 시험은 LaneView 를 손으로 만들어 넣으므로 서비스가 실제로 채우는지를
// 원리적으로 못 본다. 그래서 잠그는 자리는 여기다.
//
// ★ 단정을 "nil 이 아니다"로 두지 않는 이유: LandingLane 은 점유자가 줄에 있으면 줄 루프에서
// 읽은 값을 **재사용한다**(점유자 몫 질의를 아낀다). 재사용이 엉뚱한 줄 행의 값을 집어도
// nil 검사는 통과한다 — 그 실수가 정확히 이 최적화가 새로 만든 실패 모양이다. 그래서 두 세션의
// 신호 시각을 다르게 심어 값 자체를 가른다.
func TestLandingLaneHolderSignalIsTheHoldersOwnNotAQueueMates(t *testing.T) {
	s, st := newSvc(t)
	a, b := twoSessions(t, s)

	if _, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Land(ctx(), LandInput{Project: "p", SessionID: b}); err != nil {
		t.Fatal(err)
	}

	// 두 세션에 **서로 다른** 신호 시각을 심는다.
	//
	// 시각을 미래로 주는 이유: 세션 열기가 이미 비트를 남겼고 Beat 는 시각을 뒤로 안 돌리므로
	// (store/session.go), 심는 값이 그 비트보다 뒤여야 MAX 가 바뀐다. LastSignal 은 생존 창으로
	// 거르지 않으니 미래 값도 그대로 나온다 — 여기서 재는 것은 나이가 아니라 **어느 행의 값인가**다.
	//
	// 심은 값이 정말 최신이 됐는지 저장층에 되물어 확인한다: 확인을 빼면 이 시험은 자기가 무엇을
	// 비교하는지 모르는 채 초록일 수 있다.
	plant := func(session string, ahead time.Duration) time.Time {
		t.Helper()
		at := time.Now().Add(ahead).UTC().Truncate(time.Microsecond)
		if err := st.Beat(ctx(), session, model.SignalPush, at); err != nil {
			t.Fatalf("신호 심기 실패(%s): %v", session, err)
		}
		got, ok, err := st.LastSignal(ctx(), session)
		if err != nil || !ok || !got.Equal(at) {
			t.Fatalf("사전 조건이 깨졌다 — 심은 신호가 최신이 아니다(session=%s got=%v ok=%v err=%v)",
				session, got, ok, err)
		}
		return at
	}
	sigA := plant(a, time.Minute)
	sigB := plant(b, 2*time.Minute)
	if sigA.Equal(sigB) {
		t.Fatalf("사전 조건이 깨졌다 — 두 신호가 같은 값(%v)이다. 그러면 엉뚱한 세션의 값을 실어도 통과한다", sigA)
	}

	lane, err := s.LandingLane(ctx(), "p")
	if err != nil {
		t.Fatalf("레인 조회가 실패했다: %v", err)
	}
	if lane.Holder == nil || lane.Holder.SessionID != a {
		t.Fatalf("사전 조건이 깨졌다 — 점유자가 %+v 다(기대 %s)", lane.Holder, a)
	}
	entry := func(session string) LaneEntry {
		t.Helper()
		for _, e := range lane.Entries {
			if e.SessionID == session {
				return e
			}
		}
		t.Fatalf("줄에 %s 의 행이 없다: %+v", session, lane.Entries)
		return LaneEntry{}
	}
	entA, entB := entry(a), entry(b)
	if entA.LastSignalAt == nil || entB.LastSignalAt == nil {
		t.Fatalf("줄 행의 신호 시각이 비었다(a=%v b=%v) — 비교 축이 무너졌다",
			entA.LastSignalAt, entB.LastSignalAt)
	}
	if !entA.LastSignalAt.Equal(sigA) || !entB.LastSignalAt.Equal(sigB) {
		t.Fatalf("줄 행이 심은 신호를 안 낸다 — 비교 축이 무너졌다: a=%v(기대 %v) b=%v(기대 %v)",
			entA.LastSignalAt, sigA, entB.LastSignalAt, sigB)
	}

	// ★ 이 시험의 심장 — 점유자 값이 **자기 줄 행과 같은 값**이다.
	if lane.Holder.LastSignalAt == nil {
		t.Fatalf("점유자의 마지막 신호가 비었다 — 같은 세션의 줄 행은 %v 를 내는데, "+
			"사람이 회수를 판정할 두 숫자 중 하나가 화면에서 사라진다", sigA)
	}
	if !lane.Holder.LastSignalAt.Equal(sigA) {
		extra := ""
		if lane.Holder.LastSignalAt.Equal(sigB) {
			extra = " — 이것은 대기자의 신호다. 재사용이 엉뚱한 줄 행을 집었다"
		}
		t.Errorf("점유자의 마지막 신호가 %v 다(기대 %v — 자기 줄 행의 값)%s",
			lane.Holder.LastSignalAt, sigA, extra)
	}
}

// TestReportReleasesTheLaneEvenWhenTheQueueRowIsGone — 점유는 있는데 줄 행이 없는
// **어긋난 상태에서도 보고는 반납한다.**
//
// ★ 여기서 멈추면(반납을 건너뛰고 응답만 released 로 내면) 아무도 못 잡는 레인이 그대로
// 남아 그 프로젝트의 랜딩이 전원 정지하고, 복구 수단이 sqlite3 직접 UPDATE 뿐이 된다.
// LandReport 가 이 갈래에 "그래도 **반납은 한다**"고 적어 둔 이유가 그것인데,
// 이 커밋 전까지 그 판정을 잠그는 것이 하나도 없었다 —
// TestLiveLandingHoldAlwaysHasALiveQueueRow 는 어긋난 상태를 만들어 놓고 report 를 부르는
// 경로가 없어 이 갈래에 **도달하지 않는다.**
//
// 어긋난 상태를 만드는 수법은 바로 위 시험과 같다: 점유는 두고 줄 행만 저장층으로 닫는다.
func TestReportReleasesTheLaneEvenWhenTheQueueRowIsGone(t *testing.T) {
	s, st := newSvc(t)
	a, b := twoSessions(t, s)

	mine, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a})
	if err != nil {
		t.Fatal(err)
	}
	if mine.State != "turn" {
		t.Fatalf("사전 조건이 깨졌다 — 레인을 못 잡았다: %+v", mine)
	}
	if err := st.CloseLandingRowBySession(ctx(), "p", a, model.LandingLeftFinish, ""); err != nil {
		t.Fatal(err)
	}
	if n := divergentHolds(t, st); n != 1 {
		t.Fatalf("사전 조건이 깨졌다 — 어긋난 점유가 %d건이다(기대 1)", n)
	}

	rep, err := s.LandReport(ctx(), LandReportInput{Project: "p", SessionID: a, Kind: model.LandingLeftOK})
	if err != nil {
		t.Fatalf("어긋난 상태의 보고가 오류가 됐다 — 오류로 올려 롤백하면 레인이 영영 안 풀린다: %v", err)
	}
	if rep.State != "released" {
		t.Fatalf("state 가 %q 다(기대 released): %+v", rep.State, rep)
	}
	if rep.RowID != 0 {
		t.Errorf("줄 행이 없는데 행 번호 %d 를 지어냈다", rep.RowID)
	}

	// ★ 이 시험의 심장 — 점유가 **실제로** 풀렸다.
	if n := laneHolders(t, st, ""); n != 0 {
		t.Fatalf("보고했는데 랜딩 점유가 %d건 남았다 — 아무도 못 잡는 레인이다", n)
	}
	if n := divergentHolds(t, st); n != 0 {
		t.Errorf("보고 뒤에도 두 표가 %d건 어긋나 있다", n)
	}
	// 조용히 고치지 않는다 — 사실이 원장에 남아야 왜 그 상태가 생겼는지 되짚을 수 있다.
	if n := countRows(t, st,
		`SELECT count(*) FROM event WHERE project = 'p' AND kind = 'lane.divergent'`); n != 1 {
		t.Errorf("어긋남이 원장에 %d건 남았다(기대 1)", n)
	}
	// 이미 닫힌 행은 ok 로 덮이지 않는다(CloseLandingRowBySession 은 살아 있는 행만 본다).
	if n := countRows(t, st,
		`SELECT count(*) FROM landing_queue WHERE id = ? AND left_kind = 'finish'`, mine.RowID); n != 1 {
		t.Errorf("닫혀 있던 행이 다른 종류로 덮였다(id=%d)", mine.RowID)
	}

	// 대조: 레인이 **진짜로** 열렸다. 위 카운트가 0이어도 다음 사람이 못 서면 의미가 없다.
	next, err := s.Land(ctx(), LandInput{Project: "p", SessionID: b})
	if err != nil {
		t.Fatal(err)
	}
	if next.State != "turn" {
		t.Fatalf("반납했다는데 다음 세션이 차례를 못 받았다: %+v — 레인이 물린 것이다", next)
	}
}

// TestWaitingSessionsReportIsToldItNeverHeldTheLane — **아직 대기 중인** 세션이 보고하면
// state 는 reclaimed 지만 사유는 "회수됐다"가 아니라 "쥔 적이 없다"여야 한다.
//
// ★ laneNotMine 이 "내가 점유자가 아니다" 전부를 reclaimed 한 낱말로 접는다 — 도달 갈래가
// 셋이고 이것이 그중 둘째다. 사유가 회수를 말하는 순간 그 문장은 거짓이 되고, 대기자는
// 자기 줄 행이 아직 살아 있는데도 회수됐다고 믿고 줄을 떠난다.
//
// ★ 그리고 **줄 행을 건드리면 안 된다.** 남의 레인에 보고했다고 자기 자리를 잃으면
// 오타 한 번이 순번을 통째로 날린다.
func TestWaitingSessionsReportIsToldItNeverHeldTheLane(t *testing.T) {
	s, st := newSvc(t)
	a, b := twoSessions(t, s)

	if _, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a}); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.Land(ctx(), LandInput{Project: "p", SessionID: b})
	if err != nil {
		t.Fatal(err)
	}
	if waiting.State != "waiting" {
		t.Fatalf("사전 조건이 깨졌다 — 둘째 세션이 %q 다(기대 waiting): %+v", waiting.State, waiting)
	}

	rep, err := s.LandReport(ctx(), LandReportInput{
		Project: "p", SessionID: b, Kind: model.LandingLeftFail, Detail: "검증 실패",
	})
	if err != nil {
		t.Fatalf("대기 중 세션의 보고가 오류가 됐다 — 사실을 그대로 답해야 한다: %v", err)
	}
	if rep.State != "reclaimed" {
		t.Fatalf("대기 중 세션에게 %q 라고 답했다(기대 reclaimed): %+v", rep.State, rep)
	}
	if !strings.Contains(rep.Reason, "쥔 적이 없다") {
		t.Errorf("대기 중 세션의 사유가 %q 다 — 아직 살아 있는 줄 행인데 회수를 말하면 거짓이다", rep.Reason)
	}
	if strings.Contains(rep.Reason, "회수") {
		t.Errorf("한 번도 쥔 적 없는 세션에게 회수를 말했다: %q", rep.Reason)
	}
	if rep.RowID != waiting.RowID {
		t.Errorf("응답이 다른 줄 행을 가리킨다: %d(기대 %d)", rep.RowID, waiting.RowID)
	}

	// 줄 행은 그대로 살아 있어야 한다 — 남의 레인에 보고한 것이 자기 자리를 못 없앤다.
	if n := countRows(t, st,
		`SELECT count(*) FROM landing_queue WHERE id = ? AND left_at IS NULL`, waiting.RowID); n != 1 {
		t.Errorf("대기 중 세션의 보고가 자기 줄 행을 닫았다(id=%d) — 오타 한 번이 순번을 날린다", waiting.RowID)
	}
	// 앞사람의 점유도 그대로여야 한다.
	if n := laneHolders(t, st, a); n != 1 {
		t.Errorf("대기자의 보고가 점유자의 레인을 건드렸다(점유 %d건)", n)
	}
}

// TestReportFromASessionThatNeverQueuedSaysExactlyThat — 줄에 **선 적이 없는** 세션이
// 보고하면 사유는 "줄에 선 기록이 없다"다. reclaimed 의 셋째 갈래다.
//
// ★ 여기서 회수를 말하면 그 세션은 자기가 레인을 잃었다고 믿고 land 를 다시 부르는 대신
// 물러난다 — 실제로 해야 할 일(먼저 줄을 서라)과 정반대다.
func TestReportFromASessionThatNeverQueuedSaysExactlyThat(t *testing.T) {
	s, st := newSvc(t)
	a, b := twoSessions(t, s)

	if _, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a}); err != nil {
		t.Fatal(err)
	}
	// b 는 land 를 한 번도 안 불렀다.
	rep, err := s.LandReport(ctx(), LandReportInput{
		Project: "p", SessionID: b, Kind: model.LandingLeftOK,
	})
	if err != nil {
		t.Fatalf("줄에 선 적 없는 세션의 보고가 오류가 됐다: %v", err)
	}
	if rep.State != "reclaimed" {
		t.Fatalf("state 가 %q 다(기대 reclaimed): %+v", rep.State, rep)
	}
	if !strings.Contains(rep.Reason, "줄에 선 기록이 없다") {
		t.Errorf("사유가 %q 다 — 줄에 선 적조차 없는 세션에게 그 사실을 말해야 한다", rep.Reason)
	}
	if strings.Contains(rep.Reason, "회수") {
		t.Errorf("줄에 선 적 없는 세션에게 회수를 말했다: %q", rep.Reason)
	}
	if rep.RowID != 0 {
		t.Errorf("줄 행이 없는데 행 번호 %d 를 지어냈다", rep.RowID)
	}
	if n := liveQueue(t, st); n != 1 {
		t.Errorf("보고가 남의 줄 행을 건드렸다(살아 있는 행 %d개, 기대 1)", n)
	}
}

// TestReclaimedReasonAlwaysTellsTheSessionWhatToDoNext — reclaimed 사유의 **처방 절반**을
// 잠근다.
//
// ★ 바로 위 두 시험(TestWaitingSessionsReportIsToldItNeverHeldTheLane ·
// TestReportFromASessionThatNeverQueuedSaysExactlyThat)은 사유의 **앞 절반**(무슨 일이
// 일어났나)만 본다. 뒤 절반("— … land 로 차례를 확인해라" · "— 먼저 land 로 줄을 서라")을
// 통째로 지워도 둘 다 초록이다 — 변이로 확인했다.
//
// ★ 그 절반이 왜 잠길 값어치가 있나: laneNotMine 이 `*out = LandResult{State:"reclaimed"}`
// 로 응답을 통째로 덮어써서 이 갈래의 Position 은 0, Holder 는 nil 이다(지금 사실이다).
// 그래서 세션이 받는 안내는 이 사유 한 줄이 전부이고, 처방이 빠지면 남는 행동은
// 같은 호출을 반복하는 폴링뿐이다.
//
// ★ 단정을 "비지 않았다"로 두지 않는 이유: 빈 문자열만 막으면 "회수됐다" 한 낱말도
// 통과한다. **다음에 부를 도구 이름(land)을 실제로 담는가**가 처방의 최소 조건이다.
//
// 진짜 회수 갈래(left_detail)는 여기 없다 — 그 문장은 사람이 쓰는 것이라 서버가
// 도구 이름을 약속할 수 없다. 여기서 잠그는 것은 **서버가 스스로 짓는 두 문장**이다.
func TestReclaimedReasonAlwaysTellsTheSessionWhatToDoNext(t *testing.T) {
	s, _ := newSvc(t)
	a, b := twoSessions(t, s)
	dirC := tmpBase(t)
	c := openSession(t, s, "p", dirC, dirC, "cc-C", "트랙C").Session.ID

	if _, err := s.Land(ctx(), LandInput{Project: "p", SessionID: a}); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.Land(ctx(), LandInput{Project: "p", SessionID: b})
	if err != nil {
		t.Fatal(err)
	}
	if waiting.State != "waiting" {
		t.Fatalf("사전 조건이 깨졌다 — 둘째가 %q 다: %+v", waiting.State, waiting)
	}

	for _, tc := range []struct {
		name    string
		session string
		want    string // 처방 절반 — 무엇을 하면 되나
	}{
		{"대기 중인 세션", b, "차례를 확인"},
		{"줄에 선 적 없는 세션", c, "먼저 land 로 줄을 서라"},
	} {
		rep, err := s.LandReport(ctx(), LandReportInput{
			Project: "p", SessionID: tc.session, Kind: model.LandingLeftOK,
		})
		if err != nil {
			t.Fatalf("%s 의 보고가 오류가 됐다 — 사실을 그대로 답해야 한다: %v", tc.name, err)
		}
		if rep.State != "reclaimed" {
			t.Fatalf("%s 에게 %q 라고 답했다(기대 reclaimed): %+v", tc.name, rep.State, rep)
		}
		if strings.TrimSpace(rep.Reason) == "" {
			t.Fatalf("%s 의 사유가 비었다 — 화면은 \"네 것이 아니다 — \" 까지만 찍는다: %+v", tc.name, rep)
		}
		if !strings.Contains(rep.Reason, tc.want) {
			t.Errorf("%s 의 사유에 처방 %q 가 없다: %q", tc.name, tc.want, rep.Reason)
		}
		// ★ 처방은 **부를 수 있는 이름**이어야 한다. 이름이 없으면 세션은 폴링만 남는다.
		if !strings.Contains(rep.Reason, "land") {
			t.Errorf("%s 의 사유가 다음에 부를 도구 이름을 안 말한다: %q", tc.name, rep.Reason)
		}
	}
}
