package store

import (
	"context"
	"errors"
	"testing"
)

// 재생산율의 **원자료**를 센다 — 비율은 여기서 안 만든다(0으로 나누는 갈래를 저장 계층에 두지 않는다).
//
// ★ 왜 이 축이 필요한가. 실측(kweiza-cc-plugins · event 원장): finish 88건이 followups 61건과
// 독립 add 53건을 낳아 R=1.30 이다. 사이클 1회마다 큐가 +0.29 이고, 그래서 **pickup 을 더
// 돌려서는 큐가 안 준다**. 그 사실을 세션이 마무리하는 그 자리에서 볼 수 있어야 한다.
//
// ★ 그 1.30 의 시점은 **2026-08-06 무렵**이다(원장의 88번째 kweiza 마무리가 id 13002 ·
// 08-06T00:40Z(KST 09:40) · 재현 확인 2026-08-10 01:58 KST · mode=ro). 창 88 이 아니라 원장이
// 88건이던 날의 전 기간 값이라, 지금 다시 재면 다른 수가 나온다 — event.go 의 같은 ★ 가
// 현재값과 나머지 인용 자리를 함께 적는다.
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
// (실측: kweiza-cc-plugins 1.30 vs context-platform 0.79 — 둘 다 2026-08-06 무렵의 전 기간
// 값이다. 2026-08-10 01:58 KST 에 mode=ro 로 다시 재면 최근 20 창 기준 0.80 vs 2.15 로
// **순서가 뒤집혀 있다.** 갈린다는 사실이 이 시험의 논지이고, 어느 쪽이 큰지는 시점이 정한다).
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
// 그리고 **오류가 아니다**: 호출자는 이것을 "표본 0"으로 읽고, "못 쟀다"는 조회 자체가
// 실패해 원자료가 없을 때만 낸다(service.QueueBalance.Repro 의 nil).
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

// 롤백된 시도는 마무리가 아니다 — 분모에도 분자에도 안 든다.
//
// ★ 왜 이것이 지표의 정합성 문제인가. R 은 DESIGN §10 이 이 설계의 판정 축으로 세운 값이고
// (2026-08-21 반증 기한), 롤링 창이 20이라 **한 건이 5%다.** 롤백된 시도의 count 는 만들어진
// 적이 없는 후속이므로, 그것이 분자에 들면 화면이 관측하지 않은 유입을 단정한다.
//
// ★ 이벤트를 Store.LogEvent 로 심지 않고 **Tx 를 실제로 롤백시켜** 만든다. 결말 표시를
// 찍는 것이 flushDeferred 라서, 손으로 심으면 이 시험은 자기가 만든 문자열만 확인하게 된다.
func TestQueueReproductionExcludesRolledBackFinishes(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	a := mustSession(t, s, "p", "cc-A")
	boom := errors.New("일부러 실패")

	// 성공한 마무리 — 후속 1건이 실제로 큐에 들어갔다.
	if err := s.Tx(ctx, func(tx *Tx) error {
		tx.LogEvent("item.finish", "p", a.ID, map[string]any{"item": "i1", "mode": "done", "count": 1})
		return nil
	}); err != nil {
		t.Fatalf("커밋 갈래 실패: %v", err)
	}
	// 롤백된 시도 — count 2 는 **하나도 안 만들어졌다.**
	if err := s.Tx(ctx, func(tx *Tx) error {
		tx.LogEvent("item.finish", "p", a.ID, map[string]any{"item": "i2", "mode": "done", "count": 2})
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("롤백 오류가 안 올라왔다: %v", err)
	}
	// 롤백된 add — 항목이 안 들어갔는데 이벤트만 남는다(Service.AddItem 도 Tx.LogEvent 다).
	if err := s.Tx(ctx, func(tx *Tx) error {
		tx.LogEvent("item.add", "p", a.ID, map[string]any{"item": "x1"})
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("롤백 오류가 안 올라왔다: %v", err)
	}
	// 성공한 add.
	if err := s.Tx(ctx, func(tx *Tx) error {
		tx.LogEvent("item.add", "p", a.ID, map[string]any{"item": "x2"})
		return nil
	}); err != nil {
		t.Fatalf("커밋 갈래 실패: %v", err)
	}

	got, err := s.QueueReproduction(ctx, "p", 10)
	if err != nil {
		t.Fatalf("조회 실패: %v", err)
	}
	if got.Finishes != 1 {
		t.Fatalf("롤백된 시도가 분모에 들었다: got %d, want 1", got.Finishes)
	}
	if got.Followups != 1 {
		t.Fatalf("만들어진 적 없는 후속이 분자에 들었다: got %d, want 1", got.Followups)
	}
	if got.Adds != 1 {
		t.Fatalf("롤백된 add 가 분자에 들었다: got %d, want 1", got.Adds)
	}
}

// 창의 아래 끝은 **센 것 중** 가장 오래된 id 다.
//
// ★ 롤백된 시도의 id 로 잡으면 분모는 그대로인데 add 구간만 넓어져 **분자가 분모 없이**
// 커진다. 창을 자르면서 add 구간을 안 자르면 R 이 실제보다 크게 나온다는
// TestQueueReproductionWindowCutsAddsToo 와 같은 부류의 실패이고, 방향도 같다.
func TestQueueReproductionWindowFloorSkipsRolledBackFinishes(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	a := mustSession(t, s, "p", "cc-A")
	boom := errors.New("일부러 실패")

	// 롤백된 시도가 **가장 오래된** 마무리 이벤트다.
	if err := s.Tx(ctx, func(tx *Tx) error {
		tx.LogEvent("item.finish", "p", a.ID, map[string]any{"item": "i0", "count": 0})
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("롤백 오류가 안 올라왔다: %v", err)
	}
	// 그 뒤, 그러나 성공한 마무리 **앞**의 add — 창 밖이어야 한다.
	s.LogEvent(ctx, "item.add", "p", a.ID, map[string]any{"item": "old1"})
	if err := s.Tx(ctx, func(tx *Tx) error {
		tx.LogEvent("item.finish", "p", a.ID, map[string]any{"item": "i1", "count": 0})
		return nil
	}); err != nil {
		t.Fatalf("커밋 갈래 실패: %v", err)
	}
	s.LogEvent(ctx, "item.add", "p", a.ID, map[string]any{"item": "new1"})

	got, err := s.QueueReproduction(ctx, "p", 10)
	if err != nil {
		t.Fatalf("조회 실패: %v", err)
	}
	if got.Finishes != 1 {
		t.Fatalf("롤백된 시도가 분모에 들었다: got %d, want 1", got.Finishes)
	}
	if got.Adds != 1 {
		t.Fatalf("창의 아래 끝을 롤백된 시도의 id 로 잡았다 — 창 밖 add 가 섞였다: got %d, want 1", got.Adds)
	}
}

// 결말 표시가 **없는** 옛 행은 센다.
//
// ★ 그것은 "커밋됐다"가 아니라 "롤백 여부를 관측 못 했다"이다. 이 저장소의 규율은 그 둘을
// 가르는 것인데, 여기서는 **세는 쪽으로 접는다** — 안 세면 표시 이전 구간의 R 이 통째로 0이
// 되고, 0은 "큐가 안 는다"로 읽혀 반증 기한의 판정을 거꾸로 뒤집는다. 접었다는 사실 자체는
// DESIGN §10 이 적는다(그것이 이 접기를 정당화하는 유일한 근거다).
func TestQueueReproductionCountsUnmarkedLegacyRows(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	a := mustSession(t, s, "p", "cc-A")

	// Store.LogEvent 직행 — 트랜잭션을 안 거치므로 결말 표시가 없다(표시 이전 원장과 같은 모양).
	s.LogEvent(ctx, "item.finish", "p", a.ID, map[string]any{"item": "i1", "count": 2})
	s.LogEvent(ctx, "item.add", "p", a.ID, map[string]any{"item": "x1"})

	got, err := s.QueueReproduction(ctx, "p", 10)
	if err != nil {
		t.Fatalf("조회 실패: %v", err)
	}
	if got.Finishes != 1 || got.Followups != 2 || got.Adds != 1 {
		t.Fatalf("표시 없는 옛 행이 버려졌다 — 표시 이전 구간의 R 이 통째로 0이 된다: %+v", got)
	}
}

// 창은 **최근 n개의 마무리 이벤트**를 보고, 그중 롤백된 것을 뺀다 — 표본이 n 보다 작아진다.
//
// ★ 이것이 이 고침이 R 의 의미에 낸 유일한 변화다. 그래서 못박는다. 대안은 "성공한 마무리가
// n개가 될 때까지 더 긁는" 것인데 안 골랐다: 그러면 창의 시간 폭이 롤백 수만큼 조용히 늘어나
// **add 구간도 함께 늘어난다**(창을 id 로 자르는 이 함수의 규율이 그 대가로 지키는 것이 바로
// "분자와 분모가 같은 구간을 본다"이다). 표본이 줄면 R 의 분산이 커질 뿐 방향이 안 틀리고,
// 분자·분모가 같은 구간이면 방향이 안 틀린다 — 틀리지 않는 쪽을 골랐다.
func TestQueueReproductionWindowCountsEventsNotSuccesses(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	a := mustSession(t, s, "p", "cc-A")
	boom := errors.New("일부러 실패")

	// 창 **밖**의 성공한 마무리 — 창이 이벤트 2개라 여기까지 안 온다.
	if err := s.Tx(ctx, func(tx *Tx) error {
		tx.LogEvent("item.finish", "p", a.ID, map[string]any{"item": "old", "count": 9})
		return nil
	}); err != nil {
		t.Fatalf("커밋 갈래 실패: %v", err)
	}
	// 창 **안**의 이벤트 둘 — 하나는 롤백이다.
	if err := s.Tx(ctx, func(tx *Tx) error {
		tx.LogEvent("item.finish", "p", a.ID, map[string]any{"item": "i1", "count": 3})
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("롤백 오류가 안 올라왔다: %v", err)
	}
	if err := s.Tx(ctx, func(tx *Tx) error {
		tx.LogEvent("item.finish", "p", a.ID, map[string]any{"item": "i2", "count": 1})
		return nil
	}); err != nil {
		t.Fatalf("커밋 갈래 실패: %v", err)
	}

	got, err := s.QueueReproduction(ctx, "p", 2)
	if err != nil {
		t.Fatalf("조회 실패: %v", err)
	}
	if got.Finishes != 1 {
		t.Fatalf("표본이 이벤트 창 밖으로 넓어졌다 — 롤백만큼 더 긁었다: got %d, want 1", got.Finishes)
	}
	if got.Followups != 1 {
		t.Fatalf("창 밖 마무리의 후속 9건이나 롤백된 3건이 섞였다: got %d, want 1", got.Followups)
	}
}
