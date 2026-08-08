package service

import (
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/store"
)

// R 집계가 실패한 것과 표본이 0인 것은 **다른 사실**이다.
//
// ★ 앞 판은 둘을 한 값으로 접었다 — 집계가 실패하면 b.Repro 가 제로값으로 남고,
// Rate() 의 둘째 반환값이 "표본 0"과 같은 false 가 되어 화면이
// "R 은 못 쟀다(최근 마무리 표본 0)"로 **원인을 단정**했다. 실제로는 마무리가 20회
// 쌓여 있을 수도 있다. DESIGN §5「쓰기 뒤 조회가 실패하면」이 묻는 "빈 값이 거짓말을
// 하나"에 이 자리는 **예**라고 답한다 — 표본 0은 참일 수 있는 사실이라 실패와 섞이면 안 된다.
//
// ★ 격리 수법: event 표를 숨긴다. ListOpen 은 item 표를 읽어 살아남고,
// QueueReproduction 만 event 를 읽어 죽는다. queueBalance 를 직접 부르는 이유는
// Finish 를 통하면 트랜잭션이 event 를 **쓰다가** 먼저 죽어 다른 갈래를 재게 되기 때문이다.
func TestQueueBalanceSeparatesUnmeasuredReproFromZeroSample(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	openSession(t, s, "p", repo, repo, "cc-1", "")
	addItem(t, s, "p", "left-open", nil, nil)

	if _, err := st.DB().ExecContext(ctx(), `ALTER TABLE event RENAME TO event_hidden`); err != nil {
		t.Fatalf("event 표 숨기기 실패(시험 전제 준비): %v", err)
	}

	b := s.queueBalance(ctx(), "p", 0, time.Now())
	if b == nil {
		t.Fatal("큐 상태(ListOpen)는 읽을 수 있는데 수지 전체가 nil 이다 — " +
			"재생산율 하나가 실패했다고 나머지를 버리면 세션은 굶은 항목 수도 못 본다")
	}
	if b.Open != 1 {
		t.Fatalf("열린 항목 %d건, 원하는 것 1 — 큐 상태 축이 안 살았다", b.Open)
	}
	if b.Repro != nil {
		t.Fatalf("집계가 실패했는데 Repro 가 채워졌다: %+v — 제로값으로 두면 표본 0과 같아진다",
			b.Repro)
	}
	rate, v := b.Rate()
	if v != RateUnmeasured {
		t.Fatalf("Rate 판정이 %v 다(원하는 것 RateUnmeasured) — 집계 실패가 '표본 0'으로 접혔다. rate=%v",
			v, rate)
	}
}

// 표본이 정말 0일 때는 그렇게 말한다 — 못 쟀다와 갈라야 판별력이 생긴다.
func TestQueueBalanceReportsZeroSampleWhenReadable(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	openSession(t, s, "p", repo, repo, "cc-1", "")
	addItem(t, s, "p", "left-open", nil, nil)

	// 마무리가 한 번도 없었다 — event 는 읽히지만 item.finish 가 0건이다.
	b := s.queueBalance(ctx(), "p", 0, time.Now())
	if b == nil {
		t.Fatal("큐 수지가 nil 이다")
	}
	if b.Repro == nil {
		t.Fatal("event 를 읽을 수 있는데 Repro 가 nil 이다 — 못 쟀다와 표본 0을 반대로 접었다")
	}
	if _, v := b.Rate(); v != RateNoSample {
		t.Fatalf("Rate 판정이 %v 다(원하는 것 RateNoSample)", v)
	}
}

// 표본이 있으면 값을 낸다.
func TestQueueBalanceRatesWhenSampleExists(t *testing.T) {
	var b QueueBalance
	b.ReproWindow = ReproWindow
	b.Repro = &store.Reproduction{Finishes: 4, Followups: 2, Adds: 1}
	rate, v := b.Rate()
	if v != RateMeasured {
		t.Fatalf("표본이 있는데 판정이 %v 다", v)
	}
	if rate != 0.75 {
		t.Fatalf("R=%v, 원하는 것 0.75((2+1)/4)", rate)
	}
}
