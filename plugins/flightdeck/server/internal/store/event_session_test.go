package store

import (
	"context"
	"testing"
	"time"
)

func TestListSessionEventsFiltersBySessionAndKind(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	a := mustSession(t, s, "p", "cc-A")
	b := mustSession(t, s, "p", "cc-B")

	s.LogEvent(ctx, "prescribe", "p", a.ID, map[string]any{"key": "unclaimed"})
	s.LogEvent(ctx, "prescribe", "p", a.ID, map[string]any{"key": "overlap:x"})
	s.LogEvent(ctx, "prescribe", "p", b.ID, map[string]any{"key": "unclaimed"})
	s.LogEvent(ctx, "prescribe_ack", "p", a.ID, map[string]any{"keys": []string{"unclaimed"}})

	got, err := s.ListSessionEvents(ctx, a.ID, "prescribe", time.Time{})
	if err != nil {
		t.Fatalf("조회 실패: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("건수가 다르다: got %d, want 2 (%+v)", len(got), got)
	}
	// **오래된 순이어야 한다** — 억제 판정이 "언제 냈나"를 보므로 최신순이면 호출자가 뒤집어야 하고,
	// 그 뒤집기를 잊으면 조용히 틀린다.
	if got[0].At.After(got[1].At) {
		t.Fatalf("오래된 순이 아니다: %v then %v", got[0].At, got[1].At)
	}
}

func TestListSessionEventsEmptyKindMeansAll(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	a := mustSession(t, s, "p", "cc-A")

	s.LogEvent(ctx, "prescribe", "p", a.ID, nil)
	s.LogEvent(ctx, "prescribe_ack", "p", a.ID, nil)

	got, err := s.ListSessionEvents(ctx, a.ID, "", time.Time{})
	if err != nil {
		t.Fatalf("조회 실패: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("kind 가 비면 전 종류여야 한다: got %d", len(got))
	}
}
