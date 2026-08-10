package store

import (
	"context"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 닫힌 카드를 같은 3중키로 다시 열면 살아나야 한다.
//
// ★ 이 시험이 지키는 것은 "죽었다를 만들지 않는다"의 반쪽이다. 닫기는 관측이라 넣지만,
// 그 관측이 틀렸다는 것을 **다음 관측이 뒤집을 수 있어야** 한다. 되살리지 않으면
// /clear 에서 닫힌 카드가 rekey 로 이어진 뒤에도 done 인 채 남아, 살아서 일하는
// 세션이 보드에서 사라진다 — 이 저장소가 이미 겪은 사고다.
func TestOpenSessionRevivesDoneCard(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()

	first, _, err := s.OpenSession(ctx, "p", "m1", "/w/t", "cc-A", "", time.Time{})
	if err != nil {
		t.Fatalf("첫 등록 실패: %v", err)
	}
	if err := s.SetSessionState(ctx, first.ID, model.SessionDone, ""); err != nil {
		t.Fatalf("닫기 실패: %v", err)
	}

	again, created, err := s.OpenSession(ctx, "p", "m1", "/w/t", "cc-A", "", time.Time{})
	if err != nil {
		t.Fatalf("재등록 실패: %v", err)
	}
	if created {
		t.Fatal("같은 3중키인데 새 카드를 만들었다 — 선점이 고아가 된다")
	}
	if again.ID != first.ID {
		t.Fatalf("다른 카드다: %q vs %q", again.ID, first.ID)
	}
	if again.State != model.SessionActive {
		t.Fatalf("닫힌 카드를 다시 열었는데 state=%q 다 — 살아서 일하는 세션이 보드에서 사라진다", again.State)
	}
}

// blocked 는 사람이 사유와 함께 남긴 것이다. 여는 것이 그 사유를 지우면 안 된다.
//
// ★ 되살리기를 "state 를 무조건 active 로" 로 쓰면 이 시험이 잡는다. blocked 가 조용히
// 풀리면 막힘을 낸 세션의 판단이 화면에서 사라지고, 아무도 그것이 사라진 줄 모른다.
func TestOpenSessionKeepsBlockedStateAndReason(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()

	first, _, err := s.OpenSession(ctx, "p", "m1", "/w/t", "cc-B", "", time.Time{})
	if err != nil {
		t.Fatalf("첫 등록 실패: %v", err)
	}
	if err := s.SetSessionState(ctx, first.ID, model.SessionBlocked, "레인이 물렸다"); err != nil {
		t.Fatalf("막힘 표시 실패: %v", err)
	}

	again, _, err := s.OpenSession(ctx, "p", "m1", "/w/t", "cc-B", "", time.Time{})
	if err != nil {
		t.Fatalf("재등록 실패: %v", err)
	}
	if again.State != model.SessionBlocked {
		t.Fatalf("blocked 가 풀렸다: state=%q — 막힘은 사람이 낸 판단이라 여는 것이 못 지운다", again.State)
	}
	if again.BlockedWhy != "레인이 물렸다" {
		t.Fatalf("막힘 사유가 사라졌다: %q", again.BlockedWhy)
	}
}
