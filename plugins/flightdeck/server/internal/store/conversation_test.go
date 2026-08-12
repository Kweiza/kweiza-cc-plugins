package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestConversationLifecycleSpansSiblingCards 는 대화 접기가 카드 갈림을 넘는지 잠근다.
//
// 같은 (machine, cc_session_id) 의 카드 둘 중 하나(A)가 선점을, 다른 하나(B)가 줄 행을
// 가져도 한 ConvLifecycle 로 모여야 한다 — 그렇지 않으면 카드마다 라이프사이클이 반쪽으로
// 보여 처방이 "선점도 없고 줄도 안 섰다"는 거짓 관측을 낸다(DESIGN:1645 이 가리키는 갈림).
func TestConversationLifecycleSpansSiblingCards(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")

	a, _, err := s.OpenSession(ctx, "p", "m1", "/w/A", "cc-shared", "", time.Time{})
	if err != nil {
		t.Fatalf("카드 A 등록 실패: %v", err)
	}
	b, _, err := s.OpenSession(ctx, "p", "m1", "/w/B", "cc-shared", "", time.Time{})
	if err != nil {
		t.Fatalf("카드 B 등록 실패: %v", err)
	}

	mustItem(t, s, "p", "it-x")
	if _, err := s.ClaimItem(ctx, "p", "it-x", a.ID, time.Time{}); err != nil {
		t.Fatalf("카드 A 선점 실패: %v", err)
	}
	if err := s.Tx(ctx, func(tx *Tx) error {
		_, err := tx.EnqueueLanding("p", b.ID, []string{"landing"}, time.Time{})
		return err
	}); err != nil {
		t.Fatalf("카드 B 줄서기 실패: %v", err)
	}

	// 어느 카드로 물어도 같은 대화가 나와야 한다 — A 로 물어 B 의 줄 행이, B 로 물어
	// A 의 선점이 보이는지가 이 시험의 핵심이다.
	got, err := s.ConversationLifecycle(ctx, "p", a.ID)
	if err != nil {
		t.Fatalf("대화 라이프사이클 조회 실패(A 로 물음): %v", err)
	}
	if len(got.SessionIDs) != 2 {
		t.Fatalf("형제 카드가 안 모였다: %v (want A·B 둘)", got.SessionIDs)
	}
	if len(got.LiveClaims) != 1 || got.LiveClaims[0] != "it-x" {
		t.Fatalf("A 의 선점이 안 보인다(B 로 물었어도 보여야 한다): %v", got.LiveClaims)
	}
	if got.LaneRow == nil || got.LaneRow.SessionID != b.ID {
		t.Fatalf("B 의 줄 행이 안 보인다(A 로 물었는데도 보여야 한다): %+v", got.LaneRow)
	}
	if len(got.LaneRow.Resources) != 1 || got.LaneRow.Resources[0] != "landing" {
		t.Fatalf("줄 행의 자원이 안 실렸다: %v", got.LaneRow.Resources)
	}

	got2, err := s.ConversationLifecycle(ctx, "p", b.ID)
	if err != nil {
		t.Fatalf("대화 라이프사이클 조회 실패(B 로 물음): %v", err)
	}
	if len(got2.LiveClaims) != 1 || got2.LiveClaims[0] != "it-x" {
		t.Fatalf("B 로 물었는데 A 의 선점이 안 보인다: %v", got2.LiveClaims)
	}
}

// TestConversationLifecycleWindowsDoneItemsByOpenedAt 는 done 항목이 EarliestOpen 이후의
// item.finish(mode=done, tx!=rolled_back)만 세는지 잠근다. 창 밖(이전 대화 몫)의 마무리는
// 안 들어와야 한다 — 안 그러면 옛날에 닫은 항목이 매 턴 "land 해라"로 영구히 되살아난다.
func TestConversationLifecycleWindowsDoneItemsByOpenedAt(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")

	a, _, err := s.OpenSession(ctx, "p", "m1", "/w/A", "cc-win", "", time.Time{})
	if err != nil {
		t.Fatal(err)
	}

	// 창 밖: EarliestOpen(=a.OpenedAt) 이전에 찍힌 item.finish — "이전 대화가 닫은 항목".
	before := a.OpenedAt.Add(-time.Hour)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO event(at, project, session_id, kind, payload) VALUES (?, ?, ?, 'item.finish', ?)`,
		fmtTime(before), "p", a.ID, `{"item":"it-old","mode":"done","tx":"committed"}`); err != nil {
		t.Fatalf("창 밖 이벤트 심기 실패: %v", err)
	}

	// 창 안: EarliestOpen 이후 커밋된 done.
	if err := s.Tx(ctx, func(tx *Tx) error {
		tx.LogEvent("item.finish", "p", a.ID, map[string]any{"item": "it-new", "mode": "done"})
		return nil
	}); err != nil {
		t.Fatalf("창 안 이벤트 심기 실패: %v", err)
	}

	got, err := s.ConversationLifecycle(ctx, "p", a.ID)
	if err != nil {
		t.Fatalf("대화 라이프사이클 조회 실패: %v", err)
	}
	if len(got.DoneItems) != 1 || got.DoneItems[0] != "it-new" {
		t.Fatalf("done 항목이 %v 다 — want [it-new] 하나뿐(it-old 는 창 밖이라 빠져야 한다)", got.DoneItems)
	}
}

// TestConversationLifecycleFallsBackToSelfWithoutCC 는 cc_session_id 가 빈 카드가
// 자기 하나로만 접히는지 잠근다 — AckReach 와 같은 폴백이다. 이 가드가 없으면 빈 cc 를
// 공유하는 서로 무관한 카드 전부가 한 "대화"로 합쳐져 형제 폭발이 난다.
//
// ★ 세션을 **직접 SQL 로 심는다.** OpenSession 은 빈 cc 를 거절한다(session.go:45 —
// 3중키 전부를 요구한다). 그래서 이 모양은 지금의 정상 경로로는 못 만들지만, 스키마
// 자체는 cc_session_id 에 빈 문자열을 막지 않고(NOT NULL 일 뿐 CHECK 로 빈 값을 막지
// 않는다) 옛 데이터·복구 경로가 이 모양을 남길 수 있다 — item_sibling_claims_test.go 의
// 같은 전제("빈 cc 행이 존재할 수 없다")는 OpenSession 경로 한정이지 DB 전체가 아니다.
// 이 시험은 바로 그 방어가 실제로 도는지를 확인한다.
func TestConversationLifecycleFallsBackToSelfWithoutCC(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")

	insertBareSession := func(id, worktree string) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO session(id, project, machine_id, worktree, cc_session_id, label, state, blocked_why, opened_at)
			VALUES (?, 'p', 'm1', ?, '', NULL, 'active', NULL, ?)`,
			id, worktree, fmtTime(nowStamp())); err != nil {
			t.Fatalf("빈 cc 세션 심기 실패(%s): %v", id, err)
		}
	}
	insertBareSession("me-bare", "/w/me")
	// cc 가 똑같이 빈 남의 카드 — 형제로 잡히면 안 된다.
	insertBareSession("other-bare", "/w/other")

	mustItem(t, s, "p", "it-other")
	if _, err := s.ClaimItem(ctx, "p", "it-other", "other-bare", time.Time{}); err != nil {
		t.Fatal(err)
	}

	got, err := s.ConversationLifecycle(ctx, "p", "me-bare")
	if err != nil {
		t.Fatalf("대화 라이프사이클 조회 실패: %v", err)
	}
	if len(got.SessionIDs) != 1 || got.SessionIDs[0] != "me-bare" {
		t.Fatalf("빈 cc 카드가 자기 하나로 안 접혔다: %v", got.SessionIDs)
	}
	if len(got.LiveClaims) != 0 {
		t.Fatalf("남의 선점이 형제로 잡혔다(빈 cc 폭발): %v", got.LiveClaims)
	}
}

// TestConversationLifecycleAllHeldSuppressesLaneWaitSignal 은 store 층에서 HeldRes 가
// 실제로 채워지는지를 본다(판정은 service 몫이지만, 그 판정이 옳으려면 이 축이 정확해야
// 한다) — 줄 행의 자원을 대화가 전부 쥐면 HeldRes 에 그 자원이 실린다.
func TestConversationLifecycleAllHeldSuppressesLaneWaitSignal(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")

	a, _, err := s.OpenSession(ctx, "p", "m1", "/w/A", "cc-held", "", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Tx(ctx, func(tx *Tx) error {
		_, err := tx.EnqueueLanding("p", a.ID, []string{"landing"}, time.Time{})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcquireResource(ctx, "p", "landing", Holder{SessionID: a.ID}, time.Time{}); err != nil {
		t.Fatalf("자원 획득 실패: %v", err)
	}

	got, err := s.ConversationLifecycle(ctx, "p", a.ID)
	if err != nil {
		t.Fatalf("대화 라이프사이클 조회 실패: %v", err)
	}
	if len(got.HeldRes) != 1 || got.HeldRes[0] != "landing" {
		t.Fatalf("쥔 자원이 안 실렸다: %v", got.HeldRes)
	}
	if !got.EverEnqueued {
		t.Fatalf("EverEnqueued 가 false 다 — 이 대화는 방금 줄에 섰다")
	}
	if got.LaneRow == nil {
		t.Fatalf("LaneRow 가 nil 이다 — 아직 안 빠졌는데 살아 있는 행이 안 보인다")
	}
}

// TestConversationLifecycleExcludesDroppedAndRolledBackFinishes 는 DoneItems 가 정말로
// dropped 와 롤백된 시도를 거르는지 잠근다(리뷰 지적: 이 축을 뮤테이션으로 지워도
// lifecycle_test.go 의 표만으로는 전 시험이 초록이었다 — DoneItems 는 store 층에서만
// 걸러지므로 그 축을 잠그는 시험도 store 층에 있어야 한다).
//
// 대조군으로 정상 커밋된 done 하나를 같이 심는다 — dropped·rolled_back 이 빠진 것이
// "필터가 다 지웠다"가 아니라 "그 둘만 걸러졌다"인지 확인하기 위해서다.
func TestConversationLifecycleExcludesDroppedAndRolledBackFinishes(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")

	a, _, err := s.OpenSession(ctx, "p", "m1", "/w/A", "cc-filter", "", time.Time{})
	if err != nil {
		t.Fatal(err)
	}

	// dropped 로 닫은 항목 — 커밋은 됐지만 mode 가 done 이 아니므로 DoneItems 에 안
	// 들어와야 한다(스펙: land 게이트는 outcome='done' 에만 반응한다).
	if err := s.Tx(ctx, func(tx *Tx) error {
		tx.LogEvent("item.finish", "p", a.ID, map[string]any{"item": "it-dropped", "mode": "dropped"})
		return nil
	}); err != nil {
		t.Fatalf("dropped 이벤트 심기 실패: %v", err)
	}

	// 롤백된 시도 — mode 는 done 이지만 그 트랜잭션이 롤백돼 markTxOutcome 이
	// tx="rolled_back" 을 찍는다. 그 후속(count)이 실제로 안 만들어졌으므로 이 시도는
	// "닫았다"로 안 세야 한다.
	boom := errors.New("일부러 실패")
	if err := s.Tx(ctx, func(tx *Tx) error {
		tx.LogEvent("item.finish", "p", a.ID, map[string]any{"item": "it-rolled", "mode": "done"})
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("롤백 오류가 안 올라왔다: %v", err)
	}

	// 대조: 정상적으로 커밋된 done.
	if err := s.Tx(ctx, func(tx *Tx) error {
		tx.LogEvent("item.finish", "p", a.ID, map[string]any{"item": "it-done", "mode": "done"})
		return nil
	}); err != nil {
		t.Fatalf("done 이벤트 심기 실패: %v", err)
	}

	got, err := s.ConversationLifecycle(ctx, "p", a.ID)
	if err != nil {
		t.Fatalf("대화 라이프사이클 조회 실패: %v", err)
	}
	if len(got.DoneItems) != 1 || got.DoneItems[0] != "it-done" {
		t.Fatalf("done 항목이 %v 다 — want [it-done] 하나뿐(it-dropped·it-rolled 는 빠져야 한다)",
			got.DoneItems)
	}
}
