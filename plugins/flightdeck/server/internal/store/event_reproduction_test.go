package store

import (
	"context"
	"testing"
)

// 재생산율의 **원자료**를 센다 — 비율은 여기서 안 만든다(0으로 나누는 갈래를 저장 계층에 두지 않는다).
//
// ★ 왜 이 축이 필요한가. 실측(kweiza-cc-plugins · event 원장): finish 88건이 followups 61건과
// 독립 add 53건을 낳아 R=1.30 이다. 사이클 1회마다 큐가 +0.29 이고, 그래서 **pickup 을 더
// 돌려서는 큐가 안 준다**. 그 사실을 세션이 마무리하는 그 자리에서 볼 수 있어야 한다.
func TestQueueReproductionCountsFollowupsAndAdds(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	a := mustSession(t, s, "p", "cc-A")

	// 표본 구간의 시작이 되는 첫 마무리.
	s.LogEvent(ctx, "item.finish", "p", a.ID, map[string]any{"item": "i1", "count": 0})
	s.LogEvent(ctx, "item.add", "p", a.ID, map[string]any{"item": "x1"})
	s.LogEvent(ctx, "item.finish", "p", a.ID, map[string]any{"item": "i2", "count": 2})
	s.LogEvent(ctx, "item.add", "p", a.ID, map[string]any{"item": "x2"})
	s.LogEvent(ctx, "item.finish", "p", a.ID, map[string]any{"item": "i3", "count": 1})

	got, err := s.QueueReproduction(ctx, "p", 10)
	if err != nil {
		t.Fatalf("조회 실패: %v", err)
	}
	if got.Finishes != 3 {
		t.Fatalf("마무리 수가 다르다: got %d, want 3", got.Finishes)
	}
	if got.Followups != 3 {
		t.Fatalf("후속 합이 다르다: got %d, want 3 (0+2+1)", got.Followups)
	}
	if got.Adds != 2 {
		t.Fatalf("독립 add 수가 다르다: got %d, want 2", got.Adds)
	}
}

// 표본을 N 으로 자른다 — 그리고 **add 구간도 함께 잘린다**.
//
// ★ 이 둘이 어긋나면 지표가 조용히 틀린다: 마무리는 최근 N회만 세면서 add 는 전 기간을
// 세면 R 이 실제보다 크게 나온다. AckReach 가 시각 절단 없이 전 기간을 누적해 겪은 것과
// 같은 부류다(fd-ack-reach-needs-time-window).
func TestQueueReproductionWindowCutsAddsToo(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	a := mustSession(t, s, "p", "cc-A")

	// 창 **밖**이 될 옛 구간: add 가 셋 있다.
	s.LogEvent(ctx, "item.add", "p", a.ID, map[string]any{"item": "old1"})
	s.LogEvent(ctx, "item.add", "p", a.ID, map[string]any{"item": "old2"})
	s.LogEvent(ctx, "item.add", "p", a.ID, map[string]any{"item": "old3"})
	s.LogEvent(ctx, "item.finish", "p", a.ID, map[string]any{"item": "old", "count": 9})
	// 창 **안**: 마무리 둘과 add 하나.
	s.LogEvent(ctx, "item.finish", "p", a.ID, map[string]any{"item": "i1", "count": 1})
	s.LogEvent(ctx, "item.add", "p", a.ID, map[string]any{"item": "new1"})
	s.LogEvent(ctx, "item.finish", "p", a.ID, map[string]any{"item": "i2", "count": 0})

	got, err := s.QueueReproduction(ctx, "p", 2)
	if err != nil {
		t.Fatalf("조회 실패: %v", err)
	}
	if got.Finishes != 2 {
		t.Fatalf("표본이 N 으로 안 잘렸다: got %d, want 2", got.Finishes)
	}
	if got.Followups != 1 {
		t.Fatalf("창 밖 마무리의 후속 9건이 섞였다: got %d, want 1", got.Followups)
	}
	if got.Adds != 1 {
		t.Fatalf("창 밖 add 3건이 섞였다 — 마무리만 자르고 add 는 전 기간을 셌다: got %d, want 1", got.Adds)
	}
}

// 프로젝트를 넘지 않는다. 한 DB 에 프로젝트가 여럿이고 R 은 프로젝트마다 갈린다
// (실측: kweiza-cc-plugins 1.30 vs context-platform 0.79).
func TestQueueReproductionIsPerProject(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	seed(t, s, "q")
	a := mustSession(t, s, "p", "cc-A")
	b := mustSession(t, s, "q", "cc-B")

	s.LogEvent(ctx, "item.finish", "p", a.ID, map[string]any{"item": "i1", "count": 1})
	s.LogEvent(ctx, "item.finish", "q", b.ID, map[string]any{"item": "j1", "count": 5})
	s.LogEvent(ctx, "item.add", "q", b.ID, map[string]any{"item": "j2"})

	got, err := s.QueueReproduction(ctx, "p", 10)
	if err != nil {
		t.Fatalf("조회 실패: %v", err)
	}
	if got.Finishes != 1 || got.Followups != 1 || got.Adds != 0 {
		t.Fatalf("남의 프로젝트가 섞였다: %+v", got)
	}
}

// 마무리가 0건이면 **0값을 그대로 낸다** — 여기서 비율을 만들지 않으므로 나눗셈이 없다.
// 호출자가 Finishes==0 을 보고 "못 쟀다"로 낸다.
func TestQueueReproductionEmptyIsZeroNotError(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")

	got, err := s.QueueReproduction(ctx, "p", 10)
	if err != nil {
		t.Fatalf("마무리 0건은 오류가 아니다: %v", err)
	}
	if got.Finishes != 0 || got.Followups != 0 || got.Adds != 0 {
		t.Fatalf("빈 원장인데 0이 아니다: %+v", got)
	}
}
