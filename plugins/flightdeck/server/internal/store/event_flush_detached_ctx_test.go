package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// 예약 이벤트는 요청이 끊겨도 흘러야 한다
// ─────────────────────────────────────────────────────────────────────────────
//
// event_tx_test.go 는 **살아 있는** ctx 로만 롤백 갈래를 밟았다. 그래서 "롤백돼도 남는다"가
// 초록인 채로, 실제 운영에서 가장 흔한 롤백 사유(요청이 끊겨 ctx 가 죽었다)에서는 한 건도
// 안 남는 상태를 아무 시험도 못 봤다.
//
// 실측(고치기 전): 롤백 갈래 기록 0건 · WARN 한 줄
// (`이벤트 기록 실패(kind="cancelled.rollback" …): context canceled`).
// 즉 **끊긴 시도일수록 원장에 안 남았다** — 남겨야 할 이유가 가장 큰 것이 정확히 그것이다.

func TestFlushDeferredSurvivesCancelledTxCtx(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "fd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// ── 롤백 갈래: 클로저 안에서 요청이 끊긴다 ──
	ctx, cancel := context.WithCancel(context.Background())
	boom := errors.New("일부러 실패")
	if err := s.Tx(ctx, func(tx *Tx) error {
		tx.LogEvent("cancelled.rollback", "p", "s1", map[string]int{"n": 1})
		cancel() // 클라이언트가 끊겼다
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("롤백 오류가 안 올라왔다: %v", err)
	}

	// ── 커밋 갈래: 커밋 직전에 끊긴다(커밋 자체가 실패한다) ──
	ctx2, cancel2 := context.WithCancel(context.Background())
	if err := s.Tx(ctx2, func(tx *Tx) error {
		tx.LogEvent("cancelled.commit", "p", "s1", nil)
		cancel2()
		return nil
	}); err == nil {
		t.Fatal("전제가 깨졌다 — 취소된 ctx 로 커밋이 성공했다")
	}

	for _, kind := range []string{"cancelled.rollback", "cancelled.commit"} {
		n, err := s.CountEvents(context.Background(), kind, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("%s 이벤트가 %d건이다 — 1건이어야 한다. "+
				"flushDeferred 가 트랜잭션의 죽은 ctx 를 타면 끊긴 시도가 원장에서 사라진다", kind, n)
		}
	}
}

// TestTxBeginFailureLeavesNothingReserved 는 **남는 구멍**을 이름으로 못박는다.
//
// 취소를 뗐어도 BeginTx 가 실패하는 갈래는 클로저를 아예 안 부르므로 예약 자체가 없다.
// 그래서 종료 선언 수를 쓰는 표면의 "하한이다" 단서는 이 수정 뒤에도 못 뗀다.
// 이 시험이 그 사실을 추론이 아니라 관측으로 둔다.
func TestTxBeginFailureLeavesNothingReserved(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "fd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	dead, cancel := context.WithCancel(context.Background())
	cancel()

	called := false
	if err := s.Tx(dead, func(tx *Tx) error {
		called = true
		tx.LogEvent("begin.failed", "p", "s1", nil)
		return nil
	}); err == nil {
		t.Fatal("전제가 깨졌다 — 취소된 ctx 로 BeginTx 가 성공했다")
	}
	if called {
		t.Fatal("전제가 깨졌다 — BeginTx 가 실패했는데 클로저가 불렸다")
	}
	n, err := s.CountEvents(context.Background(), "begin.failed", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("begin.failed 이벤트가 %d건이다 — 0건이어야 한다. "+
			"이 갈래가 0인 것이 '하한' 단서가 남는 이유다", n)
	}
}
