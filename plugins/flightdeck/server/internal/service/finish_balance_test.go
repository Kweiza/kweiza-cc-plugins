package service

import (
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 배선이 실제로 이어졌는지 본다 — **계산은 되는데 읽는 쪽이 0건**인 실패를
// 이 저장소가 여러 번 겪었다(LandingLane · footprints 엔드포인트).
//
// store 단위 시험(QueueReproduction)과 렌더 단위 시험(finishBalanceLines)만 있으면
// `out.QueueBalance` 를 **아무도 안 채우는** 상태가 둘 다 통과한다.
func TestFinishFillsQueueBalance(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "closing", nil, nil)
	addItem(t, s, "p", "staying-open", nil, nil)
	claimed(t, s, "p", me.Session.ID, "closing")

	out, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "closing",
		Outcome: model.ItemDone, Title: "끝냈다",
		Body:      "왜 그렇게 했나 · 무엇을 기각했나 · 일부러 안 한 것 · 확인했으나 못 한 것",
		Followups: []FollowupInput{{ID: "spawned", Title: "후속", Body: "후속 본문"}},
	})
	if err != nil {
		t.Fatalf("마무리 실패: %v", err)
	}
	if out.QueueBalance == nil {
		t.Fatalf("큐 수지가 nil 이다 — 계산은 있는데 아무도 안 채웠다")
	}
	b := out.QueueBalance

	if b.Closed != 1 || b.Added != 1 {
		t.Fatalf("이번 호출의 델타가 틀렸다: closed=%d added=%d, want 1/1", b.Closed, b.Added)
	}
	if got := b.Delta(); got != 0 {
		t.Fatalf("순증이 틀렸다: got %+d, want 0 (1건 닫고 1건 만들었다)", got)
	}
	// ★ **커밋 뒤에** 읽어야 방금 만든 후속이 열린 목록에 들어온다.
	// 앞에서 읽으면 staying-open 하나만 세고 "이 마무리 직후의 큐"가 아니게 된다.
	if b.Open != 2 {
		t.Fatalf("열린 항목 수가 틀렸다: got %d, want 2 (staying-open + spawned) — "+
			"커밋 전에 읽으면 방금 만든 후속이 빠진다", b.Open)
	}
	// 이 마무리 자체가 표본 1건이다. 0이면 원장 집계가 안 돌았다.
	if b.Repro.Finishes == 0 {
		t.Fatalf("재생산율 표본이 0이다 — 이 마무리 자체가 표본에 들어와야 한다")
	}
	if _, ok := b.Rate(); !ok {
		t.Fatalf("표본이 있는데 비율을 못 냈다: %+v", b.Repro)
	}
	if b.ReproWindow != ReproWindow {
		t.Fatalf("표본 크기를 응답에 안 실었다: got %d, want %d", b.ReproWindow, ReproWindow)
	}
}

// Rate 는 표본 0에서 **못 쟀다**를 낸다 — 0.0 이 아니다.
//
// 0으로 접으면 "큐가 안 는다"로 읽힌다. 저장 계층이 원자료만 내고 나눗셈을 여기 둔 이유다.
func TestQueueBalanceRateSeparatesZeroSampleFromZeroRate(t *testing.T) {
	var empty QueueBalance
	if _, ok := empty.Rate(); ok {
		t.Fatalf("표본 0인데 잰 것처럼 답했다")
	}

	// 진짜 R=0 — 마무리는 있었고 아무것도 안 만들었다. 이것은 **잰 값**이다.
	zero := QueueBalance{}
	zero.Repro.Finishes = 3
	got, ok := zero.Rate()
	if !ok {
		t.Fatalf("표본이 있는데 못 쟀다고 답했다")
	}
	if got != 0 {
		t.Fatalf("R 이 0이어야 한다: got %v", got)
	}
}
