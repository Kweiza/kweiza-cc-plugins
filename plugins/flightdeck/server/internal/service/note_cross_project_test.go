package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 이 파일이 지키는 것은 **판단이 조용히 죽지 않는다**이다.
//
// 여기까지 Note 는 item_id 를 검증 없이 링크로 붙였다. 그 id 가 이 프로젝트에 없어도,
// 다른 프로젝트의 것이어도 성공을 돌려줬다. 그런데 읽는 쪽은 프로젝트로 자르므로 그
// 판단은 그 항목을 집는 세션에게 영영 안 보였다. 판단은 추가 전용이라(judgment_no_delete)
// 되돌릴 수도 없다 — 복구 경로가 0인 조용한 실패다.
//
// 원장 전수 실측(2026-08-19): 죽은 item 링크 12행/고유 11개. 그 중 **10개가 다른
// 프로젝트에 실재하는 항목**이고(교차 프로젝트 판단이라는 진짜 요구), **1개만이 어느
// 프로젝트에도 없는 오타**다. 그 두 갈래가 아래 시험 둘로 갈라진다.

// 두 프로젝트를 세운다. 프로젝트 등록은 세션 열기가 한다.
func openPQSessions(t *testing.T, s *Service) (pSession, qSession SessionResult) {
	t.Helper()
	repoP, repoQ := newRepo(t), newRepo(t)
	return openSession(t, s, "p", repoP, repoP, "cc-p", "P 세션"),
		openSession(t, s, "q", repoQ, repoQ, "cc-q", "Q 세션")
}

// 어느 프로젝트에도 없는 id 는 **거절한다**.
//
// 이것이 A 축이다. 성공으로 받아 주면 그 판단은 어디서도 안 읽히는데, 부른 세션은
// 남겼다고 믿고 지나간다 — 실측된 오타 1건이 정확히 그 모양으로 원장에 박혀 있다.
func TestNoteRefusesItemIDThatExistsInNoProject(t *testing.T) {
	s, _ := newSvc(t)
	me, _ := openPQSessions(t, s)

	_, err := s.Note(ctx(), NoteInput{
		Project: "p", SessionID: me.Session.ID, Kind: model.JudgmentDecision,
		Body: "본문", ItemID: "fd-typo-nowhere",
	})
	if err == nil {
		t.Fatal("어느 프로젝트에도 없는 항목에 판단이 걸렸다 — 그 판단은 영영 안 읽히고 지울 수도 없다")
	}
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("거절이 아니라 다른 오류다(호출자가 고칠 거리인데 서버 고장으로 읽힌다): %v", err)
	}
	if !strings.Contains(refused.Error(), "fd-typo-nowhere") {
		t.Errorf("거절 문구가 어느 id 인지 안 말한다: %s", refused.Error())
	}
}

// 자기 프로젝트에 없고 **다른 한 프로젝트에만** 있으면 그리로 건다.
//
// 실측 10건이 전부 이 모양이다(실재처가 정확히 1곳). 그리고 그 판단은 대상 프로젝트에서
// 읽혀야 한다 — 링크만 옮기고 읽히지 않으면 고친 게 없다.
func TestNoteResolvesItemIDToTheOnlyProjectThatHasIt(t *testing.T) {
	s, st := newSvc(t)
	me, _ := openPQSessions(t, s)
	addItem(t, s, "q", "fd-lock-gap-review", nil, nil)

	res, err := s.Note(ctx(), NoteInput{
		Project: "p", SessionID: me.Session.ID, Kind: model.JudgmentDecision,
		Body: "저쪽 항목에 거는 정정", ItemID: "fd-lock-gap-review",
	})
	if err != nil {
		t.Fatalf("다른 프로젝트에 유일하게 있는 항목인데 거절했다: %v", err)
	}
	if res.CrossProject != "q" {
		t.Errorf("응답이 교차 사실을 안 말한다 — CrossProject=%q, 기대 %q", res.CrossProject, "q")
	}

	got, err := st.JudgmentsForItem(ctx(), "q", "fd-lock-gap-review")
	if err != nil {
		t.Fatalf("대상 프로젝트에서 조회 실패: %v", err)
	}
	if len(got) != 1 || got[0].ID != res.Judgment.ID {
		t.Fatalf("그 항목을 집는 세션이 이 판단을 못 읽는다: %d건 %+v", len(got), got)
	}
}

// 여러 프로젝트에 같은 id 가 있으면 **고르지 않고 거절한다**.
//
// 서버가 아무거나 고르면 판단이 엉뚱한 프로젝트에 걸리고, 그것도 되돌릴 수 없다.
// 이 원장에는 접두 없는 동명 id 가 실제로 여럿 있다. 거절은 명시 인자를 가리켜야 한다.
func TestNoteRefusesItemIDThatExistsInSeveralProjects(t *testing.T) {
	s, st := newSvc(t)
	me, _ := openPQSessions(t, s)
	addItem(t, s, "q", "t4-flags-e2e-block", nil, nil)
	// p 에도 같은 id 를 두되 **닫아** 둔다 — 자기 프로젝트에 있으면 애초에 모호하지 않다.
	// 모호함은 "내게 없고 남들 여럿에게 있다"에서만 생기므로 셋째 프로젝트로 만든다.
	repoR := newRepo(t)
	openSession(t, s, "r", repoR, repoR, "cc-r", "R 세션")
	addItem(t, s, "r", "t4-flags-e2e-block", nil, nil)

	_, err := s.Note(ctx(), NoteInput{
		Project: "p", SessionID: me.Session.ID, Kind: model.JudgmentDecision,
		Body: "본문", ItemID: "t4-flags-e2e-block",
	})
	if err == nil {
		t.Fatal("동명 항목이 둘인데 서버가 하나를 골랐다 — 틀린 프로젝트에 걸려도 되돌릴 수 없다")
	}
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("거절이 아니라 다른 오류다: %v", err)
	}
	if !strings.Contains(refused.Error(), "item_project") {
		t.Errorf("거절이 무엇을 하면 되는지(item_project) 를 안 가르친다: %s", refused.Error())
	}

	// 명시하면 통과해야 한다 — 거절이 가리킨 길이 실제로 열려 있어야 한다.
	res, err := s.Note(ctx(), NoteInput{
		Project: "p", SessionID: me.Session.ID, Kind: model.JudgmentDecision,
		Body: "본문", ItemID: "t4-flags-e2e-block", ItemProject: "r",
	})
	if err != nil {
		t.Fatalf("거절이 가리킨 명시 경로가 막혀 있다: %v", err)
	}
	got, err := st.JudgmentsForItem(ctx(), "r", "t4-flags-e2e-block")
	if err != nil || len(got) != 1 || got[0].ID != res.Judgment.ID {
		t.Fatalf("명시한 프로젝트에서 안 읽힌다: err=%v got=%+v", err, got)
	}
}

// 자기 프로젝트 항목이면 지금까지와 **똑같이** 걸린다 — target_project 는 안 실린다.
//
// 옛 링크 4240건이 NULL 인 채로 읽히는 근거가 이 대칭이다. 자기 것에까지 값을 실으면
// 새 행과 옛 행이 다른 모양이 되고, 그 차이는 아무 데도 안 보이다가 조회 갈래에서 터진다.
func TestNoteKeepsOwnProjectLinkWithoutTargetProject(t *testing.T) {
	s, st := newSvc(t)
	me, _ := openPQSessions(t, s)
	addItem(t, s, "p", "mine", nil, nil)

	res, err := s.Note(ctx(), NoteInput{
		Project: "p", SessionID: me.Session.ID, Kind: model.JudgmentDecision,
		Body: "내 항목에 건다", ItemID: "mine",
	})
	if err != nil {
		t.Fatalf("자기 프로젝트 항목에 판단이 안 걸린다: %v", err)
	}
	if res.CrossProject != "" {
		t.Errorf("자기 프로젝트인데 교차라고 말한다: %q", res.CrossProject)
	}
	back, err := st.GetJudgment(ctx(), res.Judgment.ID)
	if err != nil {
		t.Fatalf("판단 되읽기 실패: %v", err)
	}
	for _, l := range back.Links {
		if l.TargetKind == "item" && l.TargetProject != "" {
			t.Errorf("자기 프로젝트 링크에 target_project 가 실렸다: %+v", l)
		}
	}
	if got, _ := st.JudgmentsForItem(ctx(), "p", "mine"); len(got) != 1 {
		t.Fatalf("자기 프로젝트에서 안 읽힌다: %d건", len(got))
	}
}
