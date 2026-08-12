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
	// ★ 분모는 `Shown` 이다 — 원장에 남는 것이 그것뿐이기 때문이다(2026-08-06 개정).
	//   `All` 로 비교하면 이 입력에 접힘이 없어 **우연히** 초록이고, 접힘이 생기는 순간
	//   틀린 이유로 빨개진다.
	if len(evs) != len(first.Shown) {
		t.Fatalf("발화 기록 수가 표시분과 다르다: events=%d, shown=%d(all=%d)",
			len(evs), len(first.Shown), len(first.All))
	}
	if !strings.Contains(evs[0].Payload, `"key"`) {
		t.Fatalf("payload 에 key 가 없다: %s", evs[0].Payload)
	}
}

// TestFoldedPrescriptionsAreNotRecordedAndComeBack — **접힌 것은 발화로 안 센다.**
//
// 앞선 판은 정반대를 계약으로 두고("요약된 것도 이미 낸 것") 접힌 것까지 기록했다.
// 그 조합이 접힌 처방을 **영구히 지웠다**: 기록되면 suppressed 가 그 키를 누르고
// (해제 규칙은 silent 에만 있다), 세션은 그 문구를 한 번도 못 본 채 원장에는
// "정상적으로 접혔다"로만 남는다. 사라지는 것이 `outside`(남이 보는 겹침 입력이 낡았다)나
// `unclaimed` 면 그 사실을 아무도 못 듣는다.
//
// ★ 상한이 무의미해지지 않는다는 것까지 여기서 단정한다 — **순환**이 그 답이다.
// 표시된 셋은 기록되어 눌리므로, 다음 턴에 같은 조건이 다시 오면 접혔던 것이 올라온다.
// 그래서 둘째 턴에 **다섯 경로를 전부 다시** 만지고도 뜨는 것이 접혔던 둘뿐인지를 본다.
// 이 단정이 상시 점등(설계 §4: 같은 것이 매 턴 반복)과 이 동작을 가르는 자리다.
//
// ★ 이 시험이 둘째 턴에 경로를 다시 만지는 것은 **일하는 세션의 정상 흐름을 그대로
// 재현하는 것**이다(훅은 매 턴 부르고, 그 사이 세션은 파일을 만진다).
//
// ★ **여기 있던 "정확한 조건" 문단은 2026-08-09 에 조건이 없어져 걷어냈다.** 원래는
// "다음 턴에 다시 뜬다의 정확한 조건은 그 축의 입력이 다시 생길 때다 — TurnPaths 가
// `f.LastAt.After(since)` 로 뽑히고 since 가 마지막 발화 시각이라 아무것도 안 만진 턴에는
// 축 자체가 안 돈다"였다. 그 조건이 실재했고 그래서 다발이 끝나면 밀린 것이 사라졌다.
// 접힌 턴이 창을 물려주게 되면서 조건이 사라졌고, **아무것도 안 만진 턴**을 재현하는 것은
// 이제 이 시험이 아니라 TestFoldedTurnKeepsTheWindowUntilTheBacklogDrains 다.
// 둘은 다른 것을 잰다: 이쪽은 억제가 표시분만 누른다, 저쪽은 창이 밀린 것을 기다린다.
func TestFoldedPrescriptionsAreNotRecordedAndComeBack(t *testing.T) {
	svc, st := newSvc(t)

	paths := []string{"a/1.go", "b/2.go", "c/3.go", "d/4.go", "e/5.go"}
	sess := openSessionForPrescribeTest(t, svc)
	claimItemForPrescribeTest(t, svc, st, sess, "fd-x", []string{"internal/judge"})
	for _, p := range paths {
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
	if len(evs) != len(res.Shown) {
		t.Fatalf("발화 기록이 표시분과 다르다: events=%d, shown=%d, all=%d\n"+
			"접힌 것을 기록하면 suppressed 가 눌러서 그 문구는 영영 안 나간다",
			len(evs), len(res.Shown), len(res.All))
	}

	firstShown := map[string]bool{}
	for _, p := range res.Shown {
		firstShown[p.Key] = true
	}
	folded := map[string]bool{}
	for _, p := range res.All[len(res.Shown):] {
		folded[p.Key] = true
	}

	// 둘째 턴 — 같은 다섯을 전부 다시 만진다. 표시됐던 셋은 눌려 있어야 하고,
	// 접혔던 둘은 올라와야 한다.
	for _, p := range paths {
		touchPathForPrescribeTest(t, st, sess, p)
	}
	second, err := svc.Prescriptions(ctx(), sess)
	if err != nil {
		t.Fatalf("둘째 호출 실패: %v", err)
	}
	if len(second.Shown) == 0 {
		t.Fatalf("접힌 것이 다음 턴에 안 올라왔다 — 그대로 소실이다(첫 턴 접힘 %d건)", res.Folded)
	}
	for _, p := range second.Shown {
		if firstShown[p.Key] {
			t.Fatalf("첫 턴에 이미 표시된 %q 가 다시 떴다 — 이것이 설계 §4 의 상시 점등이다.\n"+
				"눌려야 할 것은 **표시된 것**이고, 다시 떠야 할 것은 접힌 것뿐이다", p.Key)
		}
	}
	for key := range folded {
		var seen bool
		for _, p := range second.Shown {
			if p.Key == key {
				seen = true
			}
		}
		if !seen {
			t.Fatalf("접혔던 %q 가 같은 조건이 다시 왔는데도 안 떴다 — 이 수리가 막으려는 소실 그대로다:\n"+
				"둘째 턴 표시분 %v", key, keysOf(second.Shown))
		}
	}
}

// keysOf 는 실패 메시지용이다.
func keysOf(ps []judge.Prescription) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Key)
	}
	return out
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
	if _, err := st.ClaimItem(ctx(), sess.Project, itemID, sessionID, time.Time{}); err != nil {
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

// ackedIn 은 ack payload 가 그 키를 담았는지다. **따옴표째 본다** —
// `lane-turn:1` 은 `lane-turn:12` 의 부분 문자열이라 맨 문자열 비교는 줄 행이 두 자리로
// 넘어가는 순간 오탐한다. 그러면 "응답하지 않은 행까지 닫았나"를 묻는 단정이 조용히 무의미해진다.
func ackedIn(payload, key string) bool {
	return strings.Contains(payload, `"`+key+`"`)
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
// 국면 넷을 한 시험에 넣는다. 차례의 정의가 **곱**이라 두 인자를 각각 눌러 봐야 하기 때문이다.
// 점유 인자는 갈래가 둘(남이 쥠 · 내가 쥠)이라 둘 다 눌러야 곱이 다 눌린다:
//
//	⓪ 맨 앞도 나고 내가 쥐었다   → 안 뜬다. land 응답이 방금 turn 으로 답했으니 같은 말을
//	                             두 번 하는 것이고, 문구가 "land() 로 레인을 쥐고 랜딩을
//	                             시작해라"라 이미 쥔 세션에게는 거짓이다
//	① 앞에 사람이 있다          → 안 뜬다
//	② 맨 앞은 나인데 남이 쥐었다 → 안 뜬다. 두 표가 어긋난 상태이고, 여기서 뜨면 세션을
//	                             AcquireResource 가 반드시 실패할 자리로 보낸다
//	③ 맨 앞이 나고 레인이 비었다 → 뜬다. 그리고 두 번째 호출에는 안 뜬다
func TestLaneTurnFiresOnceWhenTheLaneBecomesMine(t *testing.T) {
	svc, st := newSvc(t)
	a, b := twoSessions(t, svc)

	held := landOrFail(t, svc, a, "turn")
	mine := landOrFail(t, svc, b, "waiting")

	// ⓪ 맨 앞도 a 고 레인을 쥔 것도 a 다 — laneTurnRow 주석이 "쥔 사람이 있으면 안 낸다,
	//    **남이든 나든**"이라 명시한 그 "나든" 쪽이다.
	//
	//    ★ **이 단정을 넣기 전에는 "나든" 쪽을 밟는 시험이 하나도 없었다.** prescribe.go 의
	//      점유 갈래를 `held.SessionID != sessionID` 로 좁히는 변이(= 남의 점유만 막는다)를
	//      넣어도 이 모듈의 12개 패키지가 전부 초록이었다. 그 변이 아래에서 실제로 나가는 것은
	//      **이미 레인을 쥔 세션에게 "land() 로 레인을 쥐고 랜딩을 시작해라"** 라는 거짓 문구고,
	//      lane-turn 은 judge.Prescribe 의 맨 앞이라 그 턴의 표시 상한 셋 중 첫 칸도 먹는다.
	if keys := laneTurnKeys(prescribeOrFail(t, svc, a)); len(keys) != 0 {
		t.Fatalf("레인을 이미 쥔 세션에게 차례 처방이 떴다: %v\n"+
			"land 가 turn 으로 답한 직후다 — 같은 말을 두 번 하고, "+
			"그 문구는 이미 쥔 세션에게 거짓이다(land() 로 쥐라고 시킨다)", keys)
	}

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
	//
	// ★ releaseLaneOrFail(=s.LandReport)을 못 쓴다 — 위 ②에서 a 의 줄 행을 store 로
	// 직접 닫아 "점유는 있는데 행이 없는" 어긋남을 만들었는데, Task 4 이후 LandReport 는
	// 행을 먼저 읽어 반납 대상 자원 집합을 정하므로 행이 없으면 **무엇을 반납할지 몰라**
	// laneNotMine 으로 빠지고 점유를 안 놓는다(landing.go 의 LandReport 독스트링 "★ 옛
	// hold-without-row 어긋남 치유는 없앴다" 참고). 이 시험이 필요한 것은 "레인이 실제로
	// 비었다"는 사실 하나뿐이라, 그 어긋남을 만든 수법과 짝을 맞춰 store 를 직접 불러
	// 놓는다 — 실제 운영에서 이 상태의 유일한 복구 수단(landing.go 머리 주석의
	// "sqlite3 직접 UPDATE")과 같은 층이다.
	if err := st.ReleaseResource(ctx(), "p", LaneResource, store.Holder{SessionID: a}); err != nil {
		t.Fatalf("어긋난 점유의 직접 반납이 실패했다: %v", err)
	}

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
// 이 대조가 없으면 억제 키에 줄 행 번호를 실었다는 결정이 배선 계층에서는 안 잠긴다
// (TestUnclaimedStillFiresForNewWorkAfterFinish 가 세운 선례). 그 결정이 풀리면 억제는 세션
// 카드 수명 전체에 걸쳐 1회로 굳고, 차례를 받고 랜딩에 실패해 맨 뒤에 다시 선 세션에게
// 두 번째 차례는 영영 안 온다.
//
// ★ **앞 시험이 그 결정을 대신 잠그지 못한다** — 이것이 이 대조가 필요한 정확한 이유다.
// 처음에 이 자리에 "이 대조가 없으면 위 시험은 lane-turn 을 통째로 껐다로도 초록불이 난다"고
// 적었는데 **거짓이었다.** 변이로 확인했다: 축을 통째로 끄면 위 시험은 초록이 아니라 **빨갛다**
// (그 시험은 국면 ③ 에서 처방이 뜨는 것을 단정한다). 참인 변이는 더 좁다 — 행 번호는 키에
// 그대로 두고 **suppressed 만 lane-turn 접두를 통째로 누르게** 하면 위 시험은 초록인 채
// 이 시험만 빨개진다. 억제 키의 접미를 잠그는 것은 이 대조 하나뿐이라는 뜻이다.
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

// TestLaneTurnIsClosedByLandAndUnrelatedJudgmentsLeaveItOpen — **확인은 처방이 지정한 행동을
// 잰다.** 2026-08-09 개정으로 통로가 뚫렸고, 이 시험은 그 전에 여기 있던 시험을 뒤집은 것이다.
//
// ★ **뒤집기 전에 무엇이 있었나.** `TestLaneTurnAckMeasuresJudgmentsNotTheLandItPrescribed`
// 가 "land 경로는 ackPrescriptions 를 한 번도 안 지난다 · 상관없는 note 한 줄이
// `lane-turn:<행>` 을 확인 처리한다"를 **현재 사실로** 잠그고 있었다. 그 축은 정확히
// 반대 신호를 쟀다: 처방대로 랜딩한 세션은 미확인으로 남고, 처방을 무시하고 판단만 남긴
// 세션이 확인으로 잡혔다. 그 시험은 스스로 "통로를 뚫으면 이 시험이 먼저 빨개진다.
// 그때 고칠 것은 시험이 아니라 여기 적힌 사실"이라고 적어 뒀고, 이것이 그 자리다.
//
// ★ **수리가 둘인 이유.** land 가 ack 을 남기게 하는 것만으로는 축이 안 선다 —
// note 가 여전히 먼저 닫아 버리면 "처방을 무시하고 판단만 남긴 세션이 확인으로 잡힌다"가
// 그대로 남기 때문이다. 그래서 `judge.AckedByLand` 가 가리는 키를 note·finish 쪽에서 뺀다.
// 그 두 축을 한 시험에서 함께 보는 이유가 이것이다: 한쪽만 하면 다른 쪽이 결과를 덮는다.
//
// ★ **note 의 "한 번이 전부를 닫는다"는 안 뒤집었다.** 그 판정(ackPrescriptions 주석:
// 처방마다 대응 판단을 요구하면 형식적 note 를 양산한다)은 여전히 유효하고, 여기서 좁힌 것은
// **행동이 판단이 아닌 처방** 하나뿐이다. 그래서 이 시험은 lane-turn 외의 키가 note 로
// 닫히는 것도 함께 단정한다 — 안 그러면 "전부 안 닫는다"로도 초록이 난다.
//
// ★ AckReach(board.go)는 이것과 **다른 축**이다 — 키를 안 보고 대화 단위로 센다.
// 그래서 "lane-turn 확인율"이라는 수치는 코드 어디에도 없다. 설계 §10 이 인용하는
// "overlap 0/31" 은 사람이 따로 잰 값이다. 이 구분을 안 적으면 다음 사람이 §10 의
// 수치를 키별 확인율로 읽는다.
func TestLaneTurnIsClosedByLandAndUnrelatedJudgmentsLeaveItOpen(t *testing.T) {
	svc, st := newSvc(t)
	a, b := twoSessions(t, svc)

	// a 가 레인을 쥐고 b 가 뒤에 선다. a 가 놓으면 b 의 차례다.
	landOrFail(t, svc, a, "turn")
	mine := landOrFail(t, svc, b, "waiting")
	releaseLaneOrFail(t, svc, a)

	// b 가 경로를 하나 만진다 — lane-turn 과 **나란히** 열리는 다른 축을 만들어야
	// "키를 가린다"와 "통째로 안 닫는다"가 갈린다.
	touchPathForPrescribeTest(t, st, b, "cmd/fd/hook.go")

	want := fmt.Sprintf("%s:%d", judge.PrescribeLaneTurn, mine.RowID)
	res := prescribeOrFail(t, svc, b)
	if got := laneTurnKeys(res); len(got) != 1 || got[0] != want {
		t.Fatalf("차례 처방이 안 떴다(기대 %q): %v — 이 시험의 전제가 깨졌다", want, got)
	}
	var other string
	for _, p := range res.Shown {
		if p.Key != want {
			other = p.Key
		}
	}
	if other == "" {
		t.Fatalf("lane-turn 말고 열린 처방이 없다 — 이 시험의 전제가 깨졌다: %v", keysOf(res.Shown))
	}

	// ① 처방과 아무 상관 없는 판단 한 줄. 다른 키는 닫고 lane-turn 은 **열어 둬야** 한다.
	if _, err := svc.Note(ctx(), NoteInput{
		Project: "p", SessionID: b, Kind: model.JudgmentDecision,
		Title: "레인과 무관한 판단", Body: "랜딩과 아무 상관 없는 내용이다",
	}); err != nil {
		t.Fatalf("note 실패: %v", err)
	}
	acks, err := st.ListSessionEvents(ctx(), b, "prescribe_ack", time.Time{})
	if err != nil {
		t.Fatalf("ack 조회 실패: %v", err)
	}
	if len(acks) != 1 {
		t.Fatalf("note 가 남긴 ack 이 %d건이다(기대 1): %+v", len(acks), acks)
	}
	if ackedIn(acks[0].Payload, want) {
		t.Fatalf("상관없는 note 가 %q 를 확인 처리했다: %s\n"+
			"이 키가 지정하는 행동은 land() 다 — 판단 한 줄로 닫히면 확인은 "+
			"**처방을 무시한 세션**을 확인으로 잡는다", want, acks[0].Payload)
	}
	if !ackedIn(acks[0].Payload, other) {
		t.Fatalf("note 가 %q 마저 안 닫았다: %s\n"+
			"좁힌 것은 행동이 판단이 아닌 처방 하나뿐이고, "+
			"'note 한 번이 전부를 닫는다'는 나머지에 그대로 유효하다", other, acks[0].Payload)
	}

	// ② 처방이 시킨 그대로 한다 — land() 가 자기가 응답한 그 키를 닫는다.
	landOrFail(t, svc, b, "turn")
	acks, err = st.ListSessionEvents(ctx(), b, "prescribe_ack", time.Time{})
	if err != nil {
		t.Fatalf("ack 조회 실패: %v", err)
	}
	var closed bool
	for _, e := range acks {
		if ackedIn(e.Payload, want) {
			closed = true
		}
	}
	if !closed {
		t.Fatalf("처방대로 land 했는데 %q 가 안 닫혔다: %+v\n"+
			"이 통로가 없으면 처방을 정확히 따른 세션이 미확인으로 남는다", want, acks)
	}
}

// TestLandAcksOnlyTheRowItAnswered — land 는 **자기가 응답한 줄 행**만 닫는다.
//
// 억제 키에 줄 행 번호가 실려 있으므로(laneTurnPrescription) 한 세션에 서로 다른 행의
// lane-turn 이 여럿 열릴 수 있다 — 차례를 받고 안 쓰고 빠졌다가 다시 서면 그 모양이다.
// 접두만 보고 닫으면 **아직 응답하지 않은 차례까지** 확인 처리되고, 그러면 확인율은
// 다시 행동이 아니라 접두 일치를 재게 된다. 겹침 축의 좌우 교환과 같은 부류다.
func TestLandAcksOnlyTheRowItAnswered(t *testing.T) {
	svc, st := newSvc(t)
	a, b := twoSessions(t, svc)

	// 1차 — b 가 차례를 받고 **안 쓰고** 줄에서 빠진다. 그 행의 처방은 열린 채 남는다.
	landOrFail(t, svc, a, "turn")
	first := landOrFail(t, svc, b, "waiting")
	releaseLaneOrFail(t, svc, a)
	firstKey := fmt.Sprintf("%s:%d", judge.PrescribeLaneTurn, first.RowID)
	if got := laneTurnKeys(prescribeOrFail(t, svc, b)); len(got) != 1 || got[0] != firstKey {
		t.Fatalf("1차 차례 처방이 %v 다(기대 [%s])", got, firstKey)
	}
	if _, err := svc.LandLeave(ctx(), LandLeaveInput{
		Project: "p", SessionID: b, Detail: "차례를 안 쓰고 빠진다"}); err != nil {
		t.Fatalf("줄에서 빠지기 실패: %v", err)
	}

	// 2차 — 새 행을 받고 이번에는 실제로 쥔다.
	landOrFail(t, svc, a, "turn")
	second := landOrFail(t, svc, b, "waiting")
	releaseLaneOrFail(t, svc, a)
	secondKey := fmt.Sprintf("%s:%d", judge.PrescribeLaneTurn, second.RowID)
	if got := laneTurnKeys(prescribeOrFail(t, svc, b)); len(got) != 1 || got[0] != secondKey {
		t.Fatalf("2차 차례 처방이 %v 다(기대 [%s])", got, secondKey)
	}
	landOrFail(t, svc, b, "turn")

	acks, err := st.ListSessionEvents(ctx(), b, "prescribe_ack", time.Time{})
	if err != nil {
		t.Fatalf("ack 조회 실패: %v", err)
	}
	var sawSecond bool
	for _, e := range acks {
		if ackedIn(e.Payload, firstKey) {
			t.Fatalf("응답하지 않은 줄 행의 처방 %q 까지 닫았다: %s\n"+
				"그 차례는 흘린 것이고 원장은 그대로 적어야 한다", firstKey, e.Payload)
		}
		if ackedIn(e.Payload, secondKey) {
			sawSecond = true
		}
	}
	if !sawSecond {
		t.Fatalf("응답한 줄 행의 처방 %q 가 안 닫혔다: %+v", secondKey, acks)
	}
}
