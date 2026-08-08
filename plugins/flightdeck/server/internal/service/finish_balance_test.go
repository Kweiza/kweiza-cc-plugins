package service

import (
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
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
	if b.Repro == nil {
		t.Fatalf("재생산율을 못 쟀다 — 이 마무리 자체가 표본에 들어와야 한다")
	}
	if b.Repro.Finishes == 0 {
		t.Fatalf("재생산율 표본이 0이다 — 이 마무리 자체가 표본에 들어와야 한다")
	}
	if _, v := b.Rate(); v != RateMeasured {
		t.Fatalf("표본이 있는데 비율을 못 냈다(판정 %v): %+v", v, b.Repro)
	}
	if b.ReproWindow != ReproWindow {
		t.Fatalf("표본 크기를 응답에 안 실었다: got %d, want %d", b.ReproWindow, ReproWindow)
	}
}

// 티클러는 굶김 축(Starved·Oldest)에서 빠진다 — 기한 대기 항목이 굶김 경고를 상시
// 점등시키면 §4 가 고발한 판별력 0 을 수지 절이 재현한다. Open 수에는 그대로 든다.
func TestQueueBalanceExcludesTicklerFromStarvation(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-tick", "트랙2")
	addItem(t, s, "p", "closing", nil, nil)
	claimed(t, s, "p", me.Session.ID, "closing")
	// 40시간 묵은 티클러와 25시간 묵은 실질 항목 — 최고령이 티클러를 따라가면 안 된다.
	if err := st.AddItem(ctx(), model.Item{Project: "p", ID: "tick-due", Title: "기한 대기", Body: "b",
		CreatedAt: time.Now().Add(-40 * time.Hour), Labels: []string{"tickler"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddItem(ctx(), model.Item{Project: "p", ID: "old-real", Title: "실질", Body: "b",
		CreatedAt: time.Now().Add(-25 * time.Hour)}); err != nil {
		t.Fatal(err)
	}

	out, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "closing",
		Outcome: model.ItemDone, Title: "끝냈다",
		Body: "왜 그렇게 했나 · 무엇을 기각했나 · 일부러 안 한 것 · 확인했으나 못 한 것",
	})
	if err != nil {
		t.Fatalf("마무리 실패: %v", err)
	}
	b := out.QueueBalance
	if b == nil {
		t.Fatal("큐 수지가 nil 이다")
	}
	if b.Open != 2 {
		t.Fatalf("Open 이 %d 다(기대 2) — 티클러가 열린 수에서까지 빠지면 재고가 거짓이 된다", b.Open)
	}
	if b.Starved != 1 {
		t.Fatalf("Starved 가 %d 다(기대 1: old-real 만) — 티클러가 굶김에 세어졌거나 실질이 빠졌다", b.Starved)
	}
	if b.Oldest >= 39*time.Hour {
		t.Fatalf("Oldest 가 %s 다 — 티클러의 나이가 최고령으로 나왔다(기대 ~25h)", b.Oldest)
	}
}

// Rate 는 표본 0에서 **못 쟀다**를 낸다 — 0.0 이 아니다.
//
// 0으로 접으면 "큐가 안 는다"로 읽힌다. 저장 계층이 원자료만 내고 나눗셈을 여기 둔 이유다.
//
// ★ 갈래가 셋이 된 뒤로는 "표본 0"과 "집계 실패"도 여기서 갈린다 — 그 축은
// repro_unmeasured_test.go 가 잠근다. 이 시험은 **잰 0** 과 그 밖을 가른다.
func TestQueueBalanceRateSeparatesZeroSampleFromZeroRate(t *testing.T) {
	var empty QueueBalance
	empty.Repro = &store.Reproduction{} // 읽었고 표본이 0이다
	if _, v := empty.Rate(); v != RateNoSample {
		t.Fatalf("표본 0인데 판정이 %v 다", v)
	}

	// 진짜 R=0 — 마무리는 있었고 아무것도 안 만들었다. 이것은 **잰 값**이다.
	zero := QueueBalance{Repro: &store.Reproduction{Finishes: 3}}
	got, v := zero.Rate()
	if v != RateMeasured {
		t.Fatalf("표본이 있는데 판정이 %v 다", v)
	}
	if got != 0 {
		t.Fatalf("R 이 0이어야 한다: got %v", got)
	}
}
