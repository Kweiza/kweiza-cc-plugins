package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// ─────────────────────────────────────────────────────────────────────────────
// 준비 헬퍼 — claimItem·claimHolder·addJudgment·judgmentCount 는 이 패키지의
// 다른 시험에 없다(grep 으로 확인). ClaimItem·GetClaim·AddJudgment·
// ListJudgmentsBySession 의 기존 시그니처를 그대로 감싼 것뿐이다.
// ─────────────────────────────────────────────────────────────────────────────

func claimItem(t *testing.T, s *Store, itemID, sessionID string) model.Claim {
	t.Helper()
	c, err := s.ClaimItem(context.Background(), "p", itemID, sessionID, time.Time{})
	if err != nil {
		t.Fatalf("claimItem 준비 실패: %v", err)
	}
	return c
}

func claimHolder(t *testing.T, s *Store, itemID string) string {
	t.Helper()
	c, err := s.GetClaim(context.Background(), "p", itemID)
	if err != nil {
		t.Fatalf("claimHolder 준비 실패: %v", err)
	}
	return c.SessionID
}

func addJudgment(t *testing.T, s *Store, sessionID, kind, body string) model.Judgment {
	t.Helper()
	j, err := s.AddJudgment(context.Background(), model.Judgment{
		Project:   "p",
		SessionID: sessionID,
		Kind:      model.JudgmentKind(kind),
		Body:      body,
	})
	if err != nil {
		t.Fatalf("addJudgment 준비 실패: %v", err)
	}
	return j
}

func judgmentCount(t *testing.T, s *Store, sessionID string) int {
	t.Helper()
	js, err := s.ListJudgmentsBySession(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("judgmentCount 준비 실패: %v", err)
	}
	return len(js)
}

func TestRekeyMovesTheCCAndKeepsTheCard(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	a := mustSession(t, s, "p", "cc-old")

	got, err := s.Rekey(ctx, a.ID, "cc-new")
	if err != nil {
		t.Fatalf("Rekey: %v", err)
	}
	if got.ID != a.ID {
		t.Fatalf("카드 id 가 바뀌었다: %q → %q", a.ID, got.ID)
	}
	if got.CCSessionID != "cc-new" {
		t.Fatalf("cc 가 안 옮겨졌다: %q", got.CCSessionID)
	}
	// ★ 렌더된 문자열이 아니라 저장소를 직접 쳐서 단정한다.
	again, err := s.GetSession(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if again.CCSessionID != "cc-new" {
		t.Fatalf("다시 읽으니 %q 다", again.CCSessionID)
	}
}

// ★ 이 시험이 설계의 핵심 주장을 지킨다 — "합치기는 컬럼 하나다".
// 선점과 판단이 session.id 를 참조하므로 cc 를 갈아도 그대로 붙어 있어야 한다.
func TestRekeyKeepsClaimsAndJudgments(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	a := mustSession(t, s, "p", "cc-old")
	mustItem(t, s, "p", "item-1")

	claimBefore := claimItem(t, s, "item-1", a.ID)
	noteBefore := addJudgment(t, s, a.ID, "decision", "왜 그렇게 했나")

	if _, err := s.Rekey(ctx, a.ID, "cc-new"); err != nil {
		t.Fatalf("Rekey: %v", err)
	}

	if got := claimHolder(t, s, "item-1"); got != a.ID {
		t.Fatalf("선점이 따라오지 않았다: holder=%q, want %q (claim %v)", got, a.ID, claimBefore)
	}
	if n := judgmentCount(t, s, a.ID); n != 1 {
		t.Fatalf("판단이 %d건이다, 1건이어야 한다 (note %v)", n, noteBefore)
	}
}

// ★ mustSession 은 cc 마다 워크트리를 새로 잡으므로(worktree = "/w/"+cc), 두 세션을
// mustSession 으로만 만들면 워크트리가 갈려 UNIQUE(machine_id, worktree, cc_session_id)
// 충돌이 재현되지 않는다. 실제 충돌("같은 machine·worktree, 다른 cc")을 내려면 두 번째
// 세션을 a 와 같은 워크트리로 직접 열어야 한다.
func TestRekeyRefusesACCAnotherCardHolds(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	a := mustSession(t, s, "p", "cc-a")
	if _, _, err := s.OpenSession(ctx, "p", "m1", a.Worktree, "cc-b", "", time.Time{}); err != nil {
		t.Fatalf("전제 세팅 실패(같은 워크트리로 두번째 세션): %v", err)
	}

	_, err := s.Rekey(ctx, a.ID, "cc-b")
	if err == nil {
		t.Fatal("UNIQUE 를 깨는 rekey 가 통과했다")
	}
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("ConflictError 가 아니라 %T 가 왔다 — api 가 409 로 못 바꾼다: %v", err, err)
	}
	if ce.Kind != ConflictDuplicate {
		t.Fatalf("Kind = %q, want %q", ce.Kind, ConflictDuplicate)
	}
}

func TestRekeyOnAMissingCardIsNotFound(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")

	_, err := s.Rekey(ctx, "no-such-card", "cc-new")
	if err == nil {
		t.Fatal("없는 카드인데 통과했다")
	}
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("NotFoundError 가 아니라 %T 가 왔다: %v", err, err)
	}
}

func TestRekeyRefusesAnEmptyCC(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	a := mustSession(t, s, "p", "cc-old")
	if _, err := s.Rekey(ctx, a.ID, "  "); err == nil {
		t.Fatal("빈 cc 로 rekey 가 통과했다 — 정체가 사라진 카드가 남는다")
	}
}

func TestRekeyLeavesAnEvent(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	a := mustSession(t, s, "p", "cc-old")
	if _, err := s.Rekey(ctx, a.ID, "cc-new"); err != nil {
		t.Fatalf("Rekey: %v", err)
	}
	evs, err := s.ListSessionEvents(ctx, a.ID, "", time.Time{})
	if err != nil {
		t.Fatalf("ListSessionEvents: %v", err)
	}
	found := false
	for _, e := range evs {
		if e.Kind == "session.rekey" {
			found = true
		}
	}
	if !found {
		t.Fatalf("session.rekey 이벤트가 없다 — cc 가 조용히 바뀌면 원인에 도달할 길이 없다 (%d건)", len(evs))
	}
}
