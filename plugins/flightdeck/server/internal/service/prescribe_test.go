package service

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// 처방이 발화되면 event 에 남고, 두 번째 호출에는 안 뜬다.
// **이것이 이 서비스의 유일한 불변식이다** — 억제가 DB 를 통해 돌아야 세션이 재시작해도 유효하다.
func TestPrescriptionsAreEmittedOnceAcrossCalls(t *testing.T) {
	svc, st := newSvc(t)

	sess := openSessionForPrescribeTest(t, svc)
	touchPathForPrescribeTest(t, st, sess, "cmd/fd/hook.go")

	first, err := svc.Prescriptions(ctx(), sess)
	if err != nil {
		t.Fatalf("첫 호출 실패: %v", err)
	}
	if len(first.All) == 0 {
		t.Fatal("선점 없이 편집했는데 처방이 0건이다")
	}

	second, err := svc.Prescriptions(ctx(), sess)
	if err != nil {
		t.Fatalf("둘째 호출 실패: %v", err)
	}
	if len(second.All) != 0 {
		t.Fatalf("같은 키가 다시 떴다: %+v", second.All)
	}

	evs, err := st.ListSessionEvents(ctx(), sess, "prescribe", time.Time{})
	if err != nil {
		t.Fatalf("이벤트 조회 실패: %v", err)
	}
	if len(evs) != len(first.All) {
		t.Fatalf("발화 기록 수가 다르다: events=%d, prescriptions=%d", len(evs), len(first.All))
	}
	if !strings.Contains(evs[0].Payload, `"key"`) {
		t.Fatalf("payload 에 key 가 없다: %s", evs[0].Payload)
	}
}

// 접힌 것도 발화 기록된다. 요약된 것은 "안 낸 것"이 아니다.
func TestFoldedPrescriptionsAreStillRecorded(t *testing.T) {
	svc, st := newSvc(t)

	sess := openSessionForPrescribeTest(t, svc)
	claimItemForPrescribeTest(t, svc, st, sess, "fd-x", []string{"internal/judge"})
	for _, p := range []string{"a/1.go", "b/2.go", "c/3.go", "d/4.go", "e/5.go"} {
		touchPathForPrescribeTest(t, st, sess, p)
	}

	res, err := svc.Prescriptions(ctx(), sess)
	if err != nil {
		t.Fatalf("호출 실패: %v", err)
	}
	if res.Folded == 0 {
		t.Fatalf("5개 경로가 선언 밖인데 안 접혔다: shown=%d", len(res.Shown))
	}
	evs, _ := st.ListSessionEvents(ctx(), sess, "prescribe", time.Time{})
	if len(evs) != len(res.All) {
		t.Fatalf("접힌 것이 발화 기록에서 빠졌다: events=%d, all=%d", len(evs), len(res.All))
	}
}

// note 하나가 그 시점 열린 처방 전부를 닫고, 무엇이 열려 있었는지가 ack 에 남는다.
func TestNoteAcksOpenPrescriptions(t *testing.T) {
	svc, st := newSvc(t)

	sess := openSessionForPrescribeTest(t, svc)
	touchPathForPrescribeTest(t, st, sess, "cmd/fd/hook.go")

	first, err := svc.Prescriptions(ctx(), sess)
	if err != nil || len(first.All) == 0 {
		t.Fatalf("처방이 안 나왔다: %v / %+v", err, first)
	}

	if _, err := svc.Note(ctx(), NoteInput{
		Project: "p", SessionID: sess, Kind: model.JudgmentDecision,
		Title: "무엇을 하는지", Body: "훅에서 처방을 낸다",
	}); err != nil {
		t.Fatalf("note 실패: %v", err)
	}

	acks, err := st.ListSessionEvents(ctx(), sess, "prescribe_ack", time.Time{})
	if err != nil {
		t.Fatalf("ack 조회 실패: %v", err)
	}
	if len(acks) != 1 {
		t.Fatalf("ack 이 1건이 아니다: %d", len(acks))
	}
	if !strings.Contains(acks[0].Payload, first.All[0].Key) {
		t.Fatalf("ack payload 에 열려 있던 키가 없다: %s", acks[0].Payload)
	}
}

// 열린 처방이 없으면 ack 도 안 남는다. 빈 ack 는 확인율 분모를 오염시킨다.
func TestNoteWithoutOpenPrescriptionsLeavesNoAck(t *testing.T) {
	svc, st := newSvc(t)
	sess := openSessionForPrescribeTest(t, svc)

	if _, err := svc.Note(ctx(), NoteInput{
		Project: "p", SessionID: sess, Kind: model.JudgmentDecision,
		Title: "t", Body: "b",
	}); err != nil {
		t.Fatalf("note 실패: %v", err)
	}
	acks, _ := st.ListSessionEvents(ctx(), sess, "prescribe_ack", time.Time{})
	if len(acks) != 0 {
		t.Fatalf("열린 처방이 없는데 ack 이 남았다: %d", len(acks))
	}
}

// finish 도 note 처럼 handoff 판단을 남긴다 — 그 판단이 열린 처방을 닫아야 한다.
// **재현**: 선점한 항목의 선언 경로 밖을 편집해 outside:b.go 처방을 발화시킨 뒤
// finish 로 마무리한다. finish 가 ack 를 안 부르면 이 처방은 영영 열린 채로 남는다 —
// 억제는 judgment 표가 아니라 이 ack 이벤트를 보므로 발화율 계측만 어긋나지만,
// 설계 §10 은 그 값이 떨어지면 "조건을 줄인다"는 교정을 걸므로 거짓 값은 교정을 그르친다.
func TestFinishAcksOpenPrescriptions(t *testing.T) {
	svc, st := newSvc(t)

	sess := openSessionForPrescribeTest(t, svc)
	claimItemForPrescribeTest(t, svc, st, sess, "batch7", []string{"a.go"})
	touchPathForPrescribeTest(t, st, sess, "b.go") // 선언 경로(a.go) 밖 — outside:b.go

	first, err := svc.Prescriptions(ctx(), sess)
	if err != nil || len(first.All) == 0 {
		t.Fatalf("처방이 안 나왔다: %v / %+v", err, first)
	}

	if _, err := svc.Finish(ctx(), FinishInput{
		Project: "p", SessionID: sess, ItemID: "batch7",
		Outcome: model.ItemDone, Title: "batch7 랜딩",
		Body: "① 왜 그렇게 했나 … ② 무엇을 기각했나 … ③ 일부러 안 한 것 … ④ 확인했으나 못 한 것 …",
	}); err != nil {
		t.Fatalf("마무리 실패: %v", err)
	}

	acks, err := st.ListSessionEvents(ctx(), sess, "prescribe_ack", time.Time{})
	if err != nil {
		t.Fatalf("ack 조회 실패: %v", err)
	}
	if len(acks) != 1 {
		t.Fatalf("finish 뒤에도 ack 이 %d건이다 — handoff 판단이 열린 처방을 안 닫았다", len(acks))
	}
	if !strings.Contains(acks[0].Payload, first.All[0].Key) {
		t.Fatalf("ack payload 에 열린 키(%s)가 없다: %s", first.All[0].Key, acks[0].Payload)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 헬퍼 — 이 패키지의 기존 헬퍼(newSvc·openSession·addItem)를 조립한 것뿐이다.
// ─────────────────────────────────────────────────────────────────────────────

// openSessionForPrescribeTest 는 실물 저장소로 세션 하나를 열고 id 를 낸다.
func openSessionForPrescribeTest(t *testing.T, s *Service) string {
	t.Helper()
	repo := newRepo(t)
	res := openSession(t, s, "p", repo, repo, "cc-1", "처방시험")
	return res.Session.ID
}

// touchPathForPrescribeTest 는 origin=observed 발자국을 하나 남긴다 —
// PostToolUse 훅이 실제 편집을 봤을 때와 같은 모양이다.
func touchPathForPrescribeTest(t *testing.T, st *store.Store, sessionID, path string) {
	t.Helper()
	if err := st.Touch(ctx(), sessionID, path, model.OriginObserved, time.Now()); err != nil {
		t.Fatalf("발자국 기록 실패(%s): %v", path, err)
	}
}

// claimItemForPrescribeTest 는 항목을 등록하고 이 세션이 바로 선점하게 한다.
func claimItemForPrescribeTest(t *testing.T, s *Service, st *store.Store, sessionID, itemID string, paths []string) {
	t.Helper()
	sess, err := st.GetSession(ctx(), sessionID)
	if err != nil {
		t.Fatalf("세션 조회 실패: %v", err)
	}
	addItem(t, s, sess.Project, itemID, paths, nil)
	if _, err := st.ClaimItem(ctx(), sess.Project, itemID, sessionID); err != nil {
		t.Fatalf("선점 실패(%s): %v", itemID, err)
	}
}

// TestFinishedItemDoesNotLookLikeUnclaimedWork 는 **제대로 끝낸 세션이 잔소리를 안 듣는다**를
// 배선 전체(store → service → judge)로 단정한다.
//
// ★ 왜 순수 함수 시험만으론 부족한가. judge 는 in.Closed 를 보고 접지만, 그 축을 아무도
// 안 채우면 판정은 초록불인 채 화면은 그대로 틀린다. 이 시험이 그 배선을 잡는다.
//
// ★ 무엇이 문제였나. finish 는 선점을 반납한다. 그래서 그 직후 ClaimedItems 가 비고,
// "선점 0건인데 경로를 편집했다"가 참이 된다 — 방금 그 항목의 일을 끝냈는데도.
// `len(Claims)==0` 하나로는 "한 번도 안 집었다"와 "방금 제대로 끝냈다"가 안 갈린다.
func TestFinishedItemDoesNotLookLikeUnclaimedWork(t *testing.T) {
	svc, st := newSvc(t)
	sess := openSessionForPrescribeTest(t, svc)
	claimItemForPrescribeTest(t, svc, st, sess, "fd-x", []string{"cmd/fd"})
	touchPathForPrescribeTest(t, st, sess, "cmd/fd/hook.go")

	// 규율대로 끝낸다 — 판단·종료·반납이 한 트랜잭션이다.
	if _, err := svc.Finish(ctx(), FinishInput{
		Project: "p", SessionID: sess, ItemID: "fd-x", Outcome: model.ItemDone,
		Title: "끝냈다", Body: "무엇을 정했고 무엇을 기각했나",
	}); err != nil {
		t.Fatalf("finish 실패: %v", err)
	}

	res, err := svc.Prescriptions(ctx(), sess)
	if err != nil {
		t.Fatalf("처방 실패: %v", err)
	}
	for _, p := range res.All {
		if p.Key == "unclaimed" {
			t.Fatalf("방금 제대로 끝낸 세션에게 미선점 처방이 떴다:\n  %s\n"+
				"finish 가 선점을 반납한다는 이유로 '한 번도 안 집었다'와 같은 취급을 받는다\n"+
				"전체: %+v", p.Reason, res.All)
		}
	}
}

// TestUnclaimedStillFiresForNewWorkAfterFinish 는 반대 방향이다 —
// 끝낸 뒤 **다른** 일을 시작하면 처방이 살아 있어야 한다.
//
// 이 시험이 없으면 위 시험은 "unclaimed 를 통째로 끈다"로도 초록불이 난다.
// 그러면 진짜 미선점 작업을 잡을 마지막 그물이 사라진다.
func TestUnclaimedStillFiresForNewWorkAfterFinish(t *testing.T) {
	svc, st := newSvc(t)
	sess := openSessionForPrescribeTest(t, svc)
	claimItemForPrescribeTest(t, svc, st, sess, "fd-x", []string{"cmd/fd"})
	touchPathForPrescribeTest(t, st, sess, "cmd/fd/hook.go")

	if _, err := svc.Finish(ctx(), FinishInput{
		Project: "p", SessionID: sess, ItemID: "fd-x", Outcome: model.ItemDone,
		Title: "끝냈다", Body: "무엇을 정했고 무엇을 기각했나",
	}); err != nil {
		t.Fatalf("finish 실패: %v", err)
	}
	// 끝낸 항목이 선언하지 않은 자리에 새 일을 시작한다.
	touchPathForPrescribeTest(t, st, sess, "internal/store/item.go")

	res, err := svc.Prescriptions(ctx(), sess)
	if err != nil {
		t.Fatalf("처방 실패: %v", err)
	}
	var found bool
	for _, p := range res.All {
		if p.Key == "unclaimed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("끝낸 항목의 선언 경로 밖에서 새 일을 시작했는데 미선점 처방이 안 떴다: %+v\n"+
			"이 그물이 죽으면 진짜 미선점 작업을 잡을 자리가 없다", res.All)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// lane-turn — judge 의 판정을 실제 줄(landing_queue · resource_hold)과 잇는 배선
//
// 이 두 시험은 **실물 DB** 로 돈다. 차례 판정의 두 인자가 서로 다른 표에 있고,
// 그 둘이 어긋난 상태가 이 축의 유일한 오발화 경로라 가짜 저장층으로는 원리적으로 못 본다
// (landing_test.go 머리의 같은 판정).
// ─────────────────────────────────────────────────────────────────────────────

// laneTurnKeys 는 처방에서 lane-turn 키만 고른다.
//
// 접두 문자열을 여기 다시 쓰지 않고 judge 의 상수를 쓴다 — 사본을 두면 judge 가 키를 바꾼 날
// 이 시험은 0건을 세면서 초록불이 난다("안 떴다"를 단정하는 쪽이 조용히 무의미해진다).
func laneTurnKeys(res PrescribeResult) []string {
	var out []string
	for _, p := range res.All {
		if strings.HasPrefix(p.Key, judge.PrescribeLaneTurn+":") {
			out = append(out, p.Key)
		}
	}
	return out
}

// landOrFail 은 줄에 서고, 기대한 갈래가 아니면 시험을 세운다.
func landOrFail(t *testing.T, s *Service, sessionID, want string) LandResult {
	t.Helper()
	res, err := s.Land(ctx(), LandInput{Project: "p", SessionID: sessionID})
	if err != nil {
		t.Fatalf("land 실패(%s): %v", sessionID, err)
	}
	if res.State != want {
		t.Fatalf("land 갈래가 %q 다(기대 %q): %+v", res.State, want, res)
	}
	return res
}

// releaseLaneOrFail 은 레인을 쥔 세션이 ok 로 보고해 놓는다. ok 는 사유가 면제된다.
func releaseLaneOrFail(t *testing.T, s *Service, sessionID string) {
	t.Helper()
	if _, err := s.LandReport(ctx(), LandReportInput{
		Project: "p", SessionID: sessionID, Kind: model.LandingLeftOK,
	}); err != nil {
		t.Fatalf("레인 반납 실패(%s): %v", sessionID, err)
	}
}

// prescribeOrFail 은 처방 한 번이다.
func prescribeOrFail(t *testing.T, s *Service, sessionID string) PrescribeResult {
	t.Helper()
	res, err := s.Prescriptions(ctx(), sessionID)
	if err != nil {
		t.Fatalf("처방 실패(%s): %v", sessionID, err)
	}
	return res
}

// TestLaneTurnFiresOnceWhenTheLaneBecomesMine 은 judge 의 lane-turn 판정이 **실제 줄**과
// 이어져 있는지를 배선 전체(store → service → judge)로 단정한다.
//
// ★ 왜 순수 함수 시험만으론 부족한가. judge 는 in.LaneTurnRow 를 보고 내지만 그 축을
// 아무도 안 채우면 판정 시험은 초록불인 채 차례는 영영 아무에게도 안 간다 —
// TestFinishedItemDoesNotLookLikeUnclaimedWork 가 세운 것과 같은 자리다.
//
// 국면 셋을 한 시험에 넣는다. 차례의 정의가 **곱**이라 두 인자를 각각 눌러 봐야 하기 때문이다:
//
//	① 앞에 사람이 있다          → 안 뜬다
//	② 맨 앞은 나인데 남이 쥐었다 → 안 뜬다. 두 표가 어긋난 상태이고, 여기서 뜨면 세션을
//	                             AcquireResource 가 반드시 실패할 자리로 보낸다
//	③ 맨 앞이 나고 레인이 비었다 → 뜬다. 그리고 두 번째 호출에는 안 뜬다
func TestLaneTurnFiresOnceWhenTheLaneBecomesMine(t *testing.T) {
	svc, st := newSvc(t)
	a, b := twoSessions(t, svc)

	held := landOrFail(t, svc, a, "turn")
	mine := landOrFail(t, svc, b, "waiting")

	// ① 앞사람이 줄에 있다.
	if keys := laneTurnKeys(prescribeOrFail(t, svc, b)); len(keys) != 0 {
		t.Fatalf("앞에 사람이 선 채인데 차례 처방이 떴다: %v", keys)
	}

	// ② 두 표를 일부러 어긋낸다 — 점유는 a 가 그대로 쥔 채 a 의 줄 행만 닫는다.
	//    맨 앞은 이제 b 지만 레인은 여전히 a 의 것이다.
	if err := st.CloseLandingRow(ctx(), "p", held.RowID, model.LandingLeftForce,
		"두 표를 일부러 어긋낸다(점유는 두고 줄 행만 닫는다)"); err != nil {
		t.Fatalf("어긋난 상태를 못 만들었다: %v", err)
	}
	if keys := laneTurnKeys(prescribeOrFail(t, svc, b)); len(keys) != 0 {
		t.Fatalf("맨 앞이지만 레인은 남이 쥔 상태에서 차례 처방이 떴다: %v\n"+
			"이 처방을 믿은 세션은 취득이 반드시 실패하는 자리로 간다 — 그 상태를 푸는 것은 사람의 회수다", keys)
	}

	// ③ a 가 놓는다. 이제 맨 앞도 나고 레인도 비었다.
	releaseLaneOrFail(t, svc, a)

	// ★ 줄에서 나간 a 에게는 아무 일도 안 일어난다. **지금이 정확히 그 오발화 상태다** —
	//   줄은 비지 않았고(b 가 맨 앞) 레인도 비었으니, "맨 앞이 나인가" 비교를 빼먹으면
	//   줄에 안 선 세션 전원이 남의 차례를 자기 것으로 받는다.
	if keys := laneTurnKeys(prescribeOrFail(t, svc, a)); len(keys) != 0 {
		t.Fatalf("줄에서 나간 세션에게 차례 처방이 떴다: %v", keys)
	}

	want := fmt.Sprintf("%s:%d", judge.PrescribeLaneTurn, mine.RowID)
	got := laneTurnKeys(prescribeOrFail(t, svc, b))
	if len(got) != 1 || got[0] != want {
		t.Fatalf("차례가 왔는데 처방이 %v 다(기대 [%s])", got, want)
	}

	// 같은 줄 행에는 다시 안 뜬다.
	if keys := laneTurnKeys(prescribeOrFail(t, svc, b)); len(keys) != 0 {
		t.Fatalf("같은 줄 행에 차례 처방이 다시 떴다: %v", keys)
	}

	// 억제의 정본은 event 표다 — 거기 안 남으면 세션이 재시작한 순간 다시 뜬다.
	evs, err := st.ListSessionEvents(ctx(), b, "prescribe", time.Time{})
	if err != nil {
		t.Fatalf("이벤트 조회 실패: %v", err)
	}
	var recorded bool
	for _, e := range evs {
		if strings.Contains(e.Payload, want) {
			recorded = true
		}
	}
	if !recorded {
		t.Fatalf("발화 기록에 %s 가 없다: %+v", want, evs)
	}
}

// TestLaneTurnReturnsForANewQueueRow 는 반대 방향이다 — 같은 세션이 **새 줄 행**을 받으면
// 차례 처방이 다시 떠야 한다.
//
// 이 대조가 없으면 위 시험은 "lane-turn 을 통째로 껐다"로도 초록불이 나고, 억제 키에
// 줄 행 번호를 실었다는 결정이 배선 계층에서는 안 잠긴다(TestUnclaimedStillFiresForNewWorkAfterFinish
// 가 세운 선례). 그 결정이 풀리면 억제는 세션 카드 수명 전체에 걸쳐 1회로 굳고, 차례를 받고
// 랜딩에 실패해 맨 뒤에 다시 선 세션에게 두 번째 차례는 영영 안 온다.
func TestLaneTurnReturnsForANewQueueRow(t *testing.T) {
	svc, _ := newSvc(t)
	a, b := twoSessions(t, svc)

	// 1차 — a 가 쥐고 b 가 서고, a 가 놓는다.
	landOrFail(t, svc, a, "turn")
	first := landOrFail(t, svc, b, "waiting")
	releaseLaneOrFail(t, svc, a)

	wantFirst := fmt.Sprintf("%s:%d", judge.PrescribeLaneTurn, first.RowID)
	if got := laneTurnKeys(prescribeOrFail(t, svc, b)); len(got) != 1 || got[0] != wantFirst {
		t.Fatalf("1차 차례 처방이 %v 다(기대 [%s])", got, wantFirst)
	}

	// b 가 차례를 안 쓰고 줄에서 빠진다 — 그 줄 행이 닫힌다.
	if _, err := svc.LandLeave(ctx(), LandLeaveInput{
		Project: "p", SessionID: b, Detail: "랜딩할 것이 없어졌다"}); err != nil {
		t.Fatalf("줄에서 빠지기 실패: %v", err)
	}

	// 2차 — 같은 세션이 다시 선다. 살아 있던 행이 닫혔으므로 새 행이 발급된다.
	landOrFail(t, svc, a, "turn")
	second := landOrFail(t, svc, b, "waiting")
	releaseLaneOrFail(t, svc, a)

	if second.RowID == first.RowID {
		t.Fatalf("다시 줄을 섰는데 같은 행 번호(%d)를 받았다 — 이 시험의 전제가 깨졌다", second.RowID)
	}
	wantSecond := fmt.Sprintf("%s:%d", judge.PrescribeLaneTurn, second.RowID)
	if got := laneTurnKeys(prescribeOrFail(t, svc, b)); len(got) != 1 || got[0] != wantSecond {
		t.Fatalf("새 줄 행(%d)에 차례가 왔는데 처방이 %v 다(기대 [%s])\n"+
			"억제 키에 줄 행 번호가 안 실리면 재시도 세션에게 두 번째 차례는 영영 안 온다",
			second.RowID, got, wantSecond)
	}
}
