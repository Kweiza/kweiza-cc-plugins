package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// 리뷰가 찾은 거짓 초록을 닫는다.
// 앞선 시험은 **닫힌** Store 에 LogEvent 를 걸어 WARN 이 나가는지만 봤다.
// 실제 운영에서 실패하는 경로는 그것이 아니라 트랜잭션 **안에서의 호출**이었고,
// 그 경로를 타는 시험이 하나도 없어서 계측이 구조적으로 항상 0이 되는 것을 못 봤다.

func TestTxLogEventDoesNotDeadlockAndSurvivesRollback(t *testing.T) {
	dbp := filepath.Join(t.TempDir(), "fd.db")
	mustMigrate(t, dbp)
	s, err := Open(dbp)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	// ── 커밋 경로 ──
	start := time.Now()
	if err := s.Tx(ctx, func(tx *Tx) error {
		tx.LogEvent("committed.kind", "p", "s1", map[string]int{"n": 1})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// 교착이 있었다면 busy_timeout(5초)을 통째로 기다린다. 여유 있게 2초로 문다.
	if el := time.Since(start); el > 2*time.Second {
		t.Errorf("트랜잭션 안 LogEvent 가 %v 걸렸다 — 쓰기 잠금 교착이다", el)
	}

	// ── 롤백 경로: 실패한 시도도 원장에 남아야 한다 ──
	boom := errors.New("일부러 실패")
	if err := s.Tx(ctx, func(tx *Tx) error {
		tx.LogEvent("rolledback.kind", "p", "s1", nil)
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("롤백 오류가 안 올라왔다: %v", err)
	}

	for _, kind := range []string{"committed.kind", "rolledback.kind"} {
		n, err := s.CountEvents(ctx, kind, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("%s 이벤트가 %d건이다 — 1건이어야 한다", kind, n)
		}
	}
}

func TestStoreLogEventInsideTxIsTheTrapThisReplaces(t *testing.T) {
	// 이 시험은 함정이 실재함을 못박는다. Tx.LogEvent 가 없으면 사람은 s.LogEvent 를 부르고,
	// 그러면 busy_timeout 만큼 멈춘 뒤 조용히 아무것도 안 남긴다.
	dbp := filepath.Join(t.TempDir(), "fd.db")
	mustMigrate(t, dbp)
	s, err := Open(dbp)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	start := time.Now()
	_ = s.Tx(ctx, func(tx *Tx) error {
		return s.TryLogEvent(ctx, "trap.kind", "p", "s1", nil) // ← 하면 안 되는 호출
	})
	elapsed := time.Since(start)
	n, _ := s.CountEvents(ctx, "trap.kind", time.Time{})

	// 대조 전제: 이 경로가 정말로 막히는가. 안 막히면 이 시험은 아무것도 안 지킨다.
	if n != 0 || elapsed < 500*time.Millisecond {
		t.Skipf("이 판의 SQLite 설정에서는 트랜잭션 안 별도 커넥션 쓰기가 막히지 않는다"+
			"(경과 %v, 기록 %d건) — Tx.LogEvent 를 쓰는 이유는 그래도 유효하다", elapsed, n)
	}
	t.Logf("확인: 트랜잭션 안 s.TryLogEvent 는 %v 걸리고 %d건 남는다 — 그래서 Tx.LogEvent 가 있다", elapsed, n)
}
