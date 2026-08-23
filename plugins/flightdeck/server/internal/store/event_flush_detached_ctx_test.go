package store

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
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
	dbp := filepath.Join(t.TempDir(), "fd.db")
	mustMigrate(t, dbp)
	s, err := Open(dbp)
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

// TestFlushCtxKeepsValuesAndDropsCancelAndBoundsWait 는 흘리기 ctx 의 성질 셋을 직접 잰다.
//
// 동작으로만 재려면 관측점이 없다 — store 는 ctx 값을 아무 데서도 안 읽으므로 "값이
// 보존됐다"를 밖에서 볼 길이 없고, 예산은 8초라 동작 시험으로 밟으면 시험이 8초를 쓴다.
// 그래서 판정을 순수 함수로 빼고 시험이 그 함수를 직접 부른다(패키지 독 코멘트의 규율).
func TestFlushCtxKeepsValuesAndDropsCancelAndBoundsWait(t *testing.T) {
	type key struct{}
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "req-42"))
	cancel() // 요청은 이미 죽었다

	ctx, done := flushCtx(parent)
	defer done()

	if err := ctx.Err(); err != nil {
		t.Fatalf("부모의 취소가 그대로 넘어왔다: %v — 그러면 INSERT 가 문을 보내기도 전에 죽는다", err)
	}
	if got, _ := ctx.Value(key{}).(string); got != "req-42" {
		t.Fatalf("값이 %q 다 — 상관 정보가 끊겼다. context.Background() 로 갈아탄 것과 같다", got)
	}
	dl, ok := ctx.Deadline()
	if !ok {
		t.Fatal("마감이 없다 — 취소를 떼면 마감도 떨어지므로 여기서 다시 걸어야 한다")
	}
	if left := time.Until(dl); left <= 0 || left > DeferredFlushBudget {
		t.Fatalf("남은 마감이 %v 다 — (0, %v] 여야 한다", left, DeferredFlushBudget)
	}
}

// TestDeferredFlushBudgetExceedsBusyTimeout 은 부등식의 **아래쪽**을 붙든다.
//
// 예산이 busy_timeout 보다 작거나 같으면, 정상적으로 줄 선 쓰기를 예산이 먼저 자른다 —
// 고치려던 유실을 다른 사유로 다시 만드는 것이다. 위쪽 부등식(< api.ShutdownGrace)은
// store 가 api 를 못 import 하므로 그쪽 패키지에 있다(api/flush_budget_test.go).
func TestDeferredFlushBudgetExceedsBusyTimeout(t *testing.T) {
	ms, err := strconv.Atoi(wantPragmas["busy_timeout"])
	if err != nil {
		t.Fatalf("busy_timeout 을 못 읽었다(%q) — 이 시험의 좌표가 틀렸다: %v", wantPragmas["busy_timeout"], err)
	}
	busy := time.Duration(ms) * time.Millisecond
	if DeferredFlushBudget <= busy {
		t.Fatalf("예산 %s 가 busy_timeout %s 보다 크지 않다 — "+
			"잠금을 정상적으로 기다리는 쓰기를 예산이 먼저 자른다", DeferredFlushBudget, busy)
	}
}

// TestTxBeginFailureLeavesNothingReserved 는 **남는 구멍**을 이름으로 못박는다.
//
// 취소를 뗐어도 BeginTx 가 실패하는 갈래는 클로저를 아예 안 부르므로 예약 자체가 없다.
// 그래서 종료 선언 수를 쓰는 표면의 "하한이다" 단서는 이 수정 뒤에도 못 뗀다.
// 이 시험이 그 사실을 추론이 아니라 관측으로 둔다.
func TestTxBeginFailureLeavesNothingReserved(t *testing.T) {
	dbp := filepath.Join(t.TempDir(), "fd.db")
	mustMigrate(t, dbp)
	s, err := Open(dbp)
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
