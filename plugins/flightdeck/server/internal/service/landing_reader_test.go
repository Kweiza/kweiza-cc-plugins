package service

import "testing"

// TestLandingQueueHasAProductionReader — landing_queue 를 읽는 프로덕션 호출자가 0건이면 빨강.
//
// ★ 왜 이 시험이 있나. session_workspace 는 이 모양의 시험이 없어서 "그 축은 이미 있다"가
// 저장에만 참이고 표시에는 거짓인 채로 근거로 쓰였다(workspace_test.go 의
// TestWorkspaceTableHoldsNothingNewYet 이 뒤늦게 그 사실을 박았다). landing_queue 도 Task 8
// 이전까지 정확히 같은 모양이었다 — LandingLane 의 비시험 호출자가 0건이었고
// (internal/mcpsrv/backend.go 의 "LandingLane 은 여기 없다" 주석과 handlers_landing.go 의
// "읽기(GET)가 없다" 주석이 그 사실을 그대로 증언한다), 그래서 큐에 실제로 줄을 서도
// 보드 어디에도 나타나지 않았다.
//
// 이 시험은 그 축을 **행동으로** 잠근다: land 로 실제 줄 행을 하나 만들고, 프로덕션 진입점인
// Board 를 그대로 불러 그 결과가 화면(BoardView.Lane)에 나타나는지를 본다. 소스를 grep 하지
// 않는 이유는, 그러면 "테스트 파일에서만 부른다"를 놓치기 쉽고 이 파일 자신도 grep 대상이 되어
// 자기 참조 오탐이 생기기 때문이다 — 실제로 값이 화면까지 도달하는지를 보는 편이 더 정직하다.
//
// ★★ **이 시험이 빨개지는 것은 결함이 아니라 주석의 만료 통지다.** Board 가 LandingLane 호출을
// 잃으면(리팩터 도중 실수로 지워지면) 이 시험이 그 순간 빨개진다. 그때 할 일은 둘 중 하나다:
//
//  1. Board(또는 다른 표시 계층)가 LandingLane 을 다시 부르게 되살린다.
//  2. 정말로 그 표시가 더 이상 필요 없다고 판단했다면, 이 표와 LandingLane 자체를 지우고
//     이 시험도 함께 지운다 — "저장만 하고 아무도 안 읽는 표"를 다시 남겨 두지 않는다.
func TestLandingQueueHasAProductionReader(t *testing.T) {
	s, _ := newSvc(t)
	dir := tmpBase(t)
	sess := openSession(t, s, "p", dir, dir, "cc-reader", "리더")

	if _, err := s.Land(ctx(), LandInput{Project: "p", SessionID: sess.Session.ID}); err != nil {
		t.Fatalf("land 실패: %v", err)
	}

	view, err := s.Board(ctx(), "p", BoardOptions{Self: sess.Session.ID})
	if err != nil {
		t.Fatalf("보드 조회 실패: %v", err)
	}

	if view.Lane == nil {
		t.Fatal("Board 가 Lane 을 안 채웠다 — landing_queue 를 읽는 프로덕션 호출자가 다시 0건이 됐다는 뜻이다.\n" +
			"위 함수 주석의 ①② 중 하나를 해라: Board 가 LandingLane 을 다시 부르게 되살리거나, " +
			"정말 안 쓰는 표라면 landing_queue·LandingLane·이 시험을 함께 지워라.")
	}
	if len(view.Lane.Entries) != 1 {
		t.Fatalf("레인 항목이 %d건이다 — 방금 land 로 하나 세웠으니 1건이어야 한다: %+v",
			len(view.Lane.Entries), view.Lane)
	}
	if view.Lane.Entries[0].SessionID != sess.Session.ID {
		t.Fatalf("레인 항목의 세션이 %q 다 — 기대 %q", view.Lane.Entries[0].SessionID, sess.Session.ID)
	}
	if view.Lane.Holder == nil || view.Lane.Holder.SessionID != sess.Session.ID {
		t.Fatalf("레인 점유자가 %+v 다 — 빈 레인의 첫 세션은 곧바로 차례를 받으므로 이 세션이어야 한다",
			view.Lane.Holder)
	}
}

// TestLandingLaneNilVsEmptyStayApart — Lane 이 nil 인 경우(안 읽었다)와 Entries 가 빈
// 슬라이스인 경우(질의는 돌았는데 아무도 없다)를 접지 않는다는 것을 값 수준에서 못박는다.
//
// Board 는 이제 항상 채우므로 Board 를 통해서는 nil 이 안 나온다 — 그 사실 자체가
// "안 읽었다"를 만들 수 있는 유일한 자리가 (표시 계층이 아예 안 부르는) 호출부뿐이라는
// 것을 보여준다. 이 시험은 그 대조를 명시한다.
func TestLandingLaneNilVsEmptyStayApart(t *testing.T) {
	s, _ := newSvc(t)
	dir := tmpBase(t)
	sess := openSession(t, s, "p", dir, dir, "cc-empty", "빈줄")

	view, err := s.Board(ctx(), "p", BoardOptions{Self: sess.Session.ID})
	if err != nil {
		t.Fatalf("보드 조회 실패: %v", err)
	}
	if view.Lane == nil {
		t.Fatal("아무도 안 서도 Board 는 레인을 읽었어야 한다(nil 이 아니라 빈 Entries 여야 한다)")
	}
	if view.Lane.Entries == nil {
		t.Fatal("Entries 가 nil 이다 — LandingLane 계약(항상 빈 슬라이스, 절대 nil 아님)을 위반했다")
	}
	if len(view.Lane.Entries) != 0 {
		t.Fatalf("아무도 안 섰는데 항목이 %d건이다", len(view.Lane.Entries))
	}
	if view.Lane.Holder != nil {
		t.Fatalf("아무도 안 섰는데 점유자가 %+v 다", view.Lane.Holder)
	}
}
