package service

import (
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// TestFinishRefusedIsRecordedButIsNotACloseDeclaration 은 두 요구를 **동시에** 잰다.
//
// ① 트랜잭션에 들어가지도 못한 거절이 원장에 남는다. 지금은 WARN 로그로만 나가서
// "몇 번 시도해서 몇 번 끊겼나"의 분모가 원리적으로 없다 — 관문의 효과를 사후에 재는
// 방법이 사람의 신고뿐이다.
// ② 그런데 그것은 **종료 선언이 아니다.** kind 를 item.finish 와 가르는 것이 그 안전 축의
// 전부다: 표류 탐지(CloseDeclarationsByItem)와 재생산율(QueueReproduction)이 둘 다
// kind = 'item.finish' 로 정확히 거르므로(store/event.go), 접미가 붙은 kind 는 두 질의에
// 원리적으로 안 걸린다. 거절을 그 kind 로 남기면 쓰지도 않은 종료 선언이 두 축에
// 들어가 멀쩡한 항목이 "롤백된 종료 선언"으로 강등된다.
func TestFinishRefusedIsRecordedButIsNotACloseDeclaration(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")

	// body 가 없다 — JudgeFinish 가 tx 진입 전에 끊는다.
	if _, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone,
	}); err == nil {
		t.Fatalf("본문 없는 마무리는 거절돼야 한다 — 전제가 깨졌다")
	}

	p := readEventPayload(t, st, "item.finish.refused")
	if p["gate"] != "judge" {
		t.Fatalf("어느 관문이 끊었는지 안 말한다: %v", p)
	}
	if p["item"] != "batch7" || p["mode"] != string(model.ItemDone) {
		t.Fatalf("거절 이벤트의 좌표가 틀렸다: %v", p)
	}

	// ★ 종료 선언 축은 하나도 안 움직여야 한다.
	if n := countRows(t, st, `SELECT count(*) FROM event WHERE kind='item.finish'`); n != 0 {
		t.Fatalf("트랜잭션에 들어가지도 않았는데 종료 선언이 %d건 남았다", n)
	}
	decls, err := st.CloseDeclarationsByItem(ctx(), "p")
	if err != nil {
		t.Fatalf("종료 선언 조회 실패: %v", err)
	}
	if _, ok := decls["batch7"]; ok {
		t.Fatalf("거절이 표류 탐지에 먹혔다: %+v", decls)
	}
	repro, err := st.QueueReproduction(ctx(), "p", 20)
	if err != nil {
		t.Fatalf("재생산율 원자료 조회 실패: %v", err)
	}
	if repro.Finishes != 0 {
		t.Fatalf("거절이 재생산율 분모에 들어갔다: %+v", repro)
	}
}

// TestFinishRefusedNamesTheGateThatStoppedIt 은 관문이 **여럿**이라는 사실을 잠근다.
//
// 하나만 물려 두면 나머지 여섯은 다시 자국 없이 끊긴다. 그리고 관문 이름을 안 실으면
// 거절 100건이 한 덩어리가 되어 "어느 문이 실제로 무나"에 답하지 못한다 — 이 축을 만든
// 이유가 그 질문이다.
func TestFinishRefusedNamesTheGateThatStoppedIt(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")

	// 선점 뒤에 이 세션이 항목을 하나 만든다 — 후속 관문이 무는 조건이다.
	// helper 의 addItem 은 세션 id 를 안 실어 item.add 이벤트가 이 세션에 안 붙는다.
	if _, err := s.AddItem(ctx(), AddItemInput{
		Project: "p", SessionID: me.Session.ID, ID: "spawn1",
		Title: "작업 중 발견", Body: "별도 축이라 지금 못 한다",
	}); err != nil {
		t.Fatalf("후속 후보 준비 실패: %v", err)
	}

	if _, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone, Body: "①…②…③…④…",
	}); err == nil {
		t.Fatalf("바닥에 떨어뜨린 후속이 있으면 한 번은 막아야 한다 — 전제가 깨졌다")
	}

	p := readEventPayload(t, st, "item.finish.refused")
	if p["gate"] != "followups-pending" {
		t.Fatalf("관문 이름이 %v 다(기대 followups-pending): %v", p["gate"], p)
	}
	// ★ 억제용 이벤트와 **따로** 남는다. 둘은 다른 질문에 답한다 —
	//   item.finish_followups_missing 은 "이 (세션·항목)에 이미 발화했나"(억제 판정)이고,
	//   item.finish.refused 는 "몇 번 끊겼나"(분모)다. 하나로 합치면 억제를 고치는 개정이
	//   분모를 조용히 뒤집는다.
	if n := countRows(t, st,
		`SELECT count(*) FROM event WHERE kind='item.finish_followups_missing'`); n != 1 {
		t.Fatalf("억제 판정용 이벤트가 %d건이다(기대 1)", n)
	}
}
