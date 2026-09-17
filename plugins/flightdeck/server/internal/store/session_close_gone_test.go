package store

import (
	"context"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// closeIfActiveUnclaimed 는 시험용 단발 트랜잭션이다.
func closeIfActiveUnclaimed(t *testing.T, s *Store, id string) bool {
	t.Helper()
	var closed bool
	if err := s.Tx(context.Background(), func(tx *Tx) error {
		var err error
		closed, err = tx.CloseSessionIfActiveUnclaimed(id)
		return err
	}); err != nil {
		t.Fatalf("조건부 닫기 실패: %v", err)
	}
	return closed
}

// 서버가 워크트리가 사라진 카드를 닫는 쓰기는 **판정 뒤에 조건을 한 번 더** 건다.
//
// ★ 왜 판정(judge.MayCloseGoneCard)만으로 부족한가. 판정은 ListLive 스냅숏을 보고, 그 읽기와
// 이 쓰기 사이에 사람이 blocked 를 걸거나 그 세션이 항목을 집을 수 있다. 이 시험은 그 창에서
// 바뀐 상태를 **UPDATE 한 문장이** 스스로 거르는지 본다 — 판정 쪽 조건을 지워도 여기가 막고,
// 여기를 지워도 판정이 막는다. 둘 중 하나만 남기면 창 하나가 열린다.
func TestCloseSessionIfActiveUnclaimedRefusesBlockedClaimedAndDone(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()

	// ① active · 선점 0 → 닫힌다
	free := mustSession(t, s, "p", "cc-free")
	if !closeIfActiveUnclaimed(t, s, free.ID) {
		t.Fatal("active · 선점 0 인 카드를 안 닫았다")
	}
	if got, _ := s.GetSession(ctx, free.ID); got.State != model.SessionDone {
		t.Fatalf("닫았다고 했는데 state=%q 다", got.State)
	}
	// ★ 되돌릴 수 있어야 한다 — 같은 3중키로 열면 살아난다(Tx.OpenSession).
	again, created, err := s.OpenSession(ctx, "p", "m1", free.Worktree, free.CCSessionID, "", time.Time{})
	if err != nil || created || again.ID != free.ID || again.State != model.SessionActive {
		t.Fatalf("자동으로 닫힌 카드가 다시 열기로 안 살아난다: id=%q created=%v state=%q err=%v",
			again.ID, created, again.State, err)
	}

	// ② blocked → 안 닫는다. 사유도 그대로다
	blocked := mustSession(t, s, "p", "cc-blocked")
	if err := s.SetSessionState(ctx, blocked.ID, model.SessionBlocked, "레인이 물렸다"); err != nil {
		t.Fatalf("막힘 표시 실패: %v", err)
	}
	if closeIfActiveUnclaimed(t, s, blocked.ID) {
		t.Fatal("blocked 카드를 닫았다 — 사람이 사유와 함께 남긴 판단이다")
	}
	if got, _ := s.GetSession(ctx, blocked.ID); got.State != model.SessionBlocked || got.BlockedWhy != "레인이 물렸다" {
		t.Fatalf("blocked 카드가 바뀌었다: state=%q why=%q", got.State, got.BlockedWhy)
	}

	// ③ 선점을 든 active → 안 닫는다
	holder := mustSession(t, s, "p", "cc-holder")
	if err := s.AddItem(ctx, model.Item{Project: "p", ID: "x", Title: "t", Body: "b", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimItem(ctx, "p", "x", holder.ID, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if closeIfActiveUnclaimed(t, s, holder.ID) {
		t.Fatal("선점을 든 카드를 닫았다 — ListLive 에서 빠져 그 선점이 아무에게도 안 보인다")
	}
	if got, _ := s.GetSession(ctx, holder.ID); got.State != model.SessionActive {
		t.Fatalf("선점 카드의 state 가 %q 로 바뀌었다", got.State)
	}

	// ④ 이미 done → 안 닫았다고 말한다(한 번 더 닫았다고 세면 원장에 헛 닫기가 남는다)
	done := mustSession(t, s, "p", "cc-done")
	if err := s.SetSessionState(ctx, done.ID, model.SessionDone, ""); err != nil {
		t.Fatal(err)
	}
	if closeIfActiveUnclaimed(t, s, done.ID) {
		t.Fatal("이미 done 인 카드를 닫았다고 한다 — 호출부가 그것을 원장에 닫기로 적는다")
	}
}
