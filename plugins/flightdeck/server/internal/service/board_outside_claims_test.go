package service

import (
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 창 밖인데 선점을 든 세션을 보드가 **따로 낸다.**
//
// ★ 왜 이 필드가 필요한가. 화면 ①은 선점을 든 카드만 내고 **창을 안 건다** —
// 창을 함께 걸면 회수가 가장 필요한 카드(오래 조용한데 항목을 쥔 세션)가 먼저 사라진다.
// 그런데 view.Sessions 는 sessionCards(cut) 의 결과라 **이미 창으로 잘린 집합**이라
// 그 카드는 렌더가 거를 대상에 애초에 없다. 실측 사례: 마지막 활동 709분 전인 세션이
// 항목 하나를 12시간째 쥐고 있었다.
//
// ★ 이 필드는 **아무것도 안 거른다.** view.Sessions 도 OutOfWindow 도 그대로다.
// 이미 도는 순회(OldestOutside)에 조건 하나를 얹은 것뿐이라 질의도 git 호출도 안 는다.
func TestBoardNamesClaimHoldersOutsideTheWindow(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	now := time.Now().UTC()

	// 창 안에서 일하는 세션 — 선점 없음
	inside := openSession(t, s, "p", repo, repo, "cc-inside", "안")
	if err := s.Beat(ctx(), inside.Session.ID, model.SignalPrompt, nil); err != nil {
		t.Fatalf("비트 실패: %v", err)
	}

	// 창 밖에서 항목을 쥔 세션 — 개시 시각도 신호도 전부 3시간 전으로 되돌린다
	outside := openSession(t, s, "p", repo, repo, "cc-outside", "밖")
	addItem(t, s, "p", "it-locked", nil, nil)
	if _, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: outside.Session.ID, ItemID: "it-locked"}); err != nil {
		t.Fatalf("선점 실패: %v", err)
	}
	old := stamp(now.Add(-3 * time.Hour))
	if _, err := st.DB().ExecContext(ctx(),
		`UPDATE session SET opened_at = ? WHERE id = ?`, old, outside.Session.ID); err != nil {
		t.Fatalf("개시 시각 되돌리기 실패: %v", err)
	}
	if _, err := st.DB().ExecContext(ctx(),
		`UPDATE signal SET at = ? WHERE session_id = ?`, old, outside.Session.ID); err != nil {
		t.Fatalf("신호 시각 되돌리기 실패: %v", err)
	}

	view, err := s.Board(ctx(), "p", BoardOptions{})
	if err != nil {
		t.Fatalf("보드 실패: %v", err)
	}

	// ① 기존 축은 그대로여야 한다 — 이 변경은 아무것도 안 거른다.
	if len(view.Sessions) != 1 {
		t.Fatalf("창 안 카드가 %d장이다 — 1장이어야 한다(이 변경은 아무것도 안 거른다)", len(view.Sessions))
	}
	if view.Sessions[0].View.Session.ID != inside.Session.ID {
		t.Fatalf("창 안 카드가 %q 다 — %q 여야 한다", view.Sessions[0].View.Session.ID, inside.Session.ID)
	}
	if view.OutOfWindow != 1 {
		t.Fatalf("창 밖 건수가 %d 다 — 1이어야 한다", view.OutOfWindow)
	}

	// ② 새 축 — 창 밖 선점자를 선점 목록까지 낸다.
	if len(view.OutsideClaims) != 1 {
		t.Fatalf("창 밖 선점자가 %d건이다 — 1건이어야 한다(회수가 가장 필요한 카드가 화면에 없으면 이 기능의 값어치가 사라진다)",
			len(view.OutsideClaims))
	}
	got := view.OutsideClaims[0]
	if got.Session.ID != outside.Session.ID {
		t.Fatalf("창 밖 선점자가 %q 다 — %q 여야 한다", got.Session.ID, outside.Session.ID)
	}
	if len(got.Claims) != 1 || got.Claims[0] != "it-locked" {
		t.Fatalf("선점 목록이 %v 다 — [it-locked] 여야 한다. 무엇이 잠겼는지가 이 줄의 존재 이유다", got.Claims)
	}
}

// 창 밖이어도 **선점이 없으면 안 낸다.** 이 필드는 "무엇이 잠겼나"를 말하는 자리이지
// 창 밖 세션을 통째로 되살리는 자리가 아니다 — 그것이면 창이 무의미해진다.
func TestBoardOutsideClaimsSkipsSessionsWithoutClaims(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	now := time.Now().UTC()

	quiet := openSession(t, s, "p", repo, repo, "cc-quiet", "조용")
	old := stamp(now.Add(-3 * time.Hour))
	if _, err := st.DB().ExecContext(ctx(),
		`UPDATE session SET opened_at = ? WHERE id = ?`, old, quiet.Session.ID); err != nil {
		t.Fatalf("개시 시각 되돌리기 실패: %v", err)
	}
	if _, err := st.DB().ExecContext(ctx(),
		`UPDATE signal SET at = ? WHERE session_id = ?`, old, quiet.Session.ID); err != nil {
		t.Fatalf("신호 시각 되돌리기 실패: %v", err)
	}

	view, err := s.Board(ctx(), "p", BoardOptions{})
	if err != nil {
		t.Fatalf("보드 실패: %v", err)
	}
	if view.OutOfWindow != 1 {
		t.Fatalf("창 밖 건수가 %d 다 — 1이어야 한다", view.OutOfWindow)
	}
	if len(view.OutsideClaims) != 0 {
		t.Fatalf("선점 없는 창 밖 세션을 냈다: %d건", len(view.OutsideClaims))
	}
}

// 창 **안**의 선점자는 이 필드에 안 들어간다 — 그 카드는 view.Sessions 에 이미 있다.
// 두 자리에 같은 세션이 들어가면 화면이 같은 카드를 두 번 그린다.
func TestBoardOutsideClaimsExcludesSessionsAlreadyShown(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)

	live := openSession(t, s, "p", repo, repo, "cc-live", "안")
	addItem(t, s, "p", "it-held", nil, nil)
	if _, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: live.Session.ID, ItemID: "it-held"}); err != nil {
		t.Fatalf("선점 실패: %v", err)
	}

	view, err := s.Board(ctx(), "p", BoardOptions{})
	if err != nil {
		t.Fatalf("보드 실패: %v", err)
	}
	if len(view.Sessions) != 1 {
		t.Fatalf("카드가 %d장이다 — 1장이어야 한다", len(view.Sessions))
	}
	if len(view.OutsideClaims) != 0 {
		t.Fatalf("창 안 선점자가 OutsideClaims 에도 들어갔다 — 화면이 같은 카드를 두 번 그린다: %d건",
			len(view.OutsideClaims))
	}
}
