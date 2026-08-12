package service

import (
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 정정한 판단은 알림 목록에서 내려가야 한다.
//
// 원장은 추가 전용이라 정정이 **새 행 + supersedes** 로 남는다(schema.sql 의 트리거가
// UPDATE 를 물리적으로 막는다). 쓰기 규율은 그렇게 서 있는데 읽는 쪽이 그 축을 안 보면
// 옛 행이 영원히 선다 — 2026-08-12 원장 실측에서 **ask 30건 + blocked 3건**이 그렇게
// 떠 있었다. 그중 하나는 이미 해소된 막힘이라, 정정을 낸 세션이 "걷었다"고 보고했다가
// 실측에 반증당했다.
func TestBoardDropsSupersededNotes(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	sess := openSession(t, s, "p", repo, repo, "cc-1", "")

	first, err := s.Note(ctx(), NoteInput{
		Project: "p", SessionID: sess.Session.ID, Kind: model.JudgmentBlocked,
		Body: "관문이 릴리스라 내가 혼자 못 연다",
	})
	if err != nil {
		t.Fatalf("판단 저장 실패: %v", err)
	}

	// 막힘이 해소됐다. 종류를 바꿔 정정한다 — 이러면 **둘 다** 안 떠야 한다.
	if _, err := s.Note(ctx(), NoteInput{
		Project: "p", SessionID: sess.Session.ID, Kind: model.JudgmentDecision,
		Body: "관문 세 조건이 다 찼다 — 막힘을 걷는다", Supersedes: first.Judgment.ID,
	}); err != nil {
		t.Fatalf("정정 저장 실패: %v", err)
	}

	view, err := s.Board(ctx(), "p", BoardOptions{IncludeNotes: true})
	if err != nil {
		t.Fatalf("보드 실패: %v", err)
	}
	if len(view.Blocked) != 0 {
		t.Fatalf("정정된 막힘이 보드에 남았다: %d건 %+v", len(view.Blocked), view.Blocked)
	}

	// ★ 같은 함수가 MCP 응답 꼬리도 채운다. 여기를 따로 재는 이유는 board.go 스스로
	//   적듯 "판정을 두 자리에 두면 한쪽만 고치는 순간 조용히 어긋나"기 때문이다.
	notes, err := s.RecentNotes(ctx(), "p", 20)
	if err != nil {
		t.Fatalf("RecentNotes 실패: %v", err)
	}
	for _, j := range notes {
		if j.ID == first.Judgment.ID {
			t.Fatalf("정정된 옛 행이 미확인 채널에 실렸다 — 매 프롬프트에 나가는 자리다")
		}
	}
}

// 같은 종류로 정정하면 **새 행이 그 자리를 잇는다.** 정정이 목록을 비우는 것이 아니다.
func TestSupersedingWithSameKindLeavesTheCorrection(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	sess := openSession(t, s, "p", repo, repo, "cc-1", "")

	first, err := s.Note(ctx(), NoteInput{
		Project: "p", SessionID: sess.Session.ID, Kind: model.JudgmentAsk,
		Body: "내 세 자리는 §5·§8·§11 이다",
	})
	if err != nil {
		t.Fatalf("판단 저장 실패: %v", err)
	}
	second, err := s.Note(ctx(), NoteInput{
		Project: "p", SessionID: sess.Session.ID, Kind: model.JudgmentAsk,
		Body: "정정 — §5 라 부른 것은 실제로 §8 안이다", Supersedes: first.Judgment.ID,
	})
	if err != nil {
		t.Fatalf("정정 저장 실패: %v", err)
	}

	view, err := s.Board(ctx(), "p", BoardOptions{IncludeNotes: true})
	if err != nil {
		t.Fatalf("보드 실패: %v", err)
	}
	if len(view.Asks) != 1 {
		t.Fatalf("요청이 %d건이다 — 정정 하나만 서야 한다: %+v", len(view.Asks), view.Asks)
	}
	if view.Asks[0].ID != second.Judgment.ID {
		t.Fatalf("남은 것이 옛 행이다(%s) — 새 행(%s)이 서야 한다",
			view.Asks[0].ID, second.Judgment.ID)
	}
}

// ★ 회귀 관문 — 이것은 **지금도 통과한다.** 고치는 것이 아니라 잠그는 시험이다.
//
// 전수 조회(ListJudgmentsByKind)는 정정된 행도 그대로 내야 한다. 원장은 추가 전용이고
// legacy/export.go 의 백업이 이 함수를 쓴다 — 여기서 걸러 버리면 백업이 조용히 얇아진다.
// 알림을 거르는 자리는 service 계층이지 store 가 아니다.
func TestSupersededJudgmentsStayInTheFullKindListing(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	sess := openSession(t, s, "p", repo, repo, "cc-1", "")

	first, err := s.Note(ctx(), NoteInput{
		Project: "p", SessionID: sess.Session.ID, Kind: model.JudgmentAsk, Body: "원문",
	})
	if err != nil {
		t.Fatalf("판단 저장 실패: %v", err)
	}
	if _, err := s.Note(ctx(), NoteInput{
		Project: "p", SessionID: sess.Session.ID, Kind: model.JudgmentAsk,
		Body: "정정문", Supersedes: first.Judgment.ID,
	}); err != nil {
		t.Fatalf("정정 저장 실패: %v", err)
	}

	all, err := st.ListJudgmentsByKind(ctx(), "p", model.JudgmentAsk, 50)
	if err != nil {
		t.Fatalf("전수 조회 실패: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("전수가 %d건이다 — 원장은 추가 전용이라 2건이어야 한다(백업이 이 함수를 쓴다)", len(all))
	}
}
