package service

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// TestAmendRefusesEmptyPatch 는 축을 하나도 안 준 요청을 거절하는지 본다.
//
// MCP 에서 정상 도달 가능한 갈래다 — amend 도구의 필수 인자는 item_id·reason 뿐이라
// 셋을 다 안 준 호출이 그대로 여기까지 온다. RefusedError 가 아니면 500 으로 나가고
// 이 문구 대신 "서버 내부 오류다"만 사용자에게 간다.
func TestAmendRefusesEmptyPatch(t *testing.T) {
	s, st := newSvc(t)
	mustAddItem(t, st, model.Item{Project: "p1", ID: "i1", Title: "제목", Body: "본문"})

	_, err := s.AmendItem(ctx(), AmendInput{
		Project: "p1", ItemID: "i1", Reason: "r",
	})
	var re *RefusedError
	if !errors.As(err, &re) {
		t.Fatalf("RefusedError 가 아니다: %#v", err)
	}
	if !strings.Contains(re.Reason, "하나는") {
		t.Errorf("사유가 %q다 — 무엇을 줘야 하는지 말해야 한다", re.Reason)
	}
	if re.Guidance == "" {
		t.Error("Guidance 가 비었다 — 이 저장소는 거절에 처방을 함께 낸다")
	}
}

// TestAmendRefusesEmptyReason 은 사유 없는 수정을 거절하는지 본다.
func TestAmendRefusesEmptyReason(t *testing.T) {
	s, st := newSvc(t)
	mustAddItem(t, st, model.Item{Project: "p1", ID: "i1", Title: "제목", Body: "본문"})

	title := "새 제목"
	_, err := s.AmendItem(ctx(), AmendInput{
		Project: "p1", ItemID: "i1", Title: &title, Reason: "   ",
	})
	var re *RefusedError
	if !errors.As(err, &re) {
		t.Fatalf("RefusedError 가 아니다: %#v", err)
	}
	if !strings.Contains(re.Reason, "사유") {
		t.Errorf("사유가 %q다", re.Reason)
	}
}

// TestAmendReportsActualChange 는 응답이 요청이 아니라 실제 변화분을 내는지 본다.
func TestAmendReportsActualChange(t *testing.T) {
	s, st := newSvc(t)
	mustAddItem(t, st, model.Item{Project: "p1", ID: "i1", Title: "원래 제목", Body: "원래 본문"})

	same := "원래 제목"
	res, err := s.AmendItem(ctx(), AmendInput{
		Project: "p1", ItemID: "i1", Title: &same, Reason: "r",
	})
	if err != nil {
		t.Fatalf("고치지 못했다: %v", err)
	}
	if len(res.Changed) != 0 {
		t.Errorf("Changed 가 %v다 — 같은 값 재지정은 변화가 아니다", res.Changed)
	}
	if res.Rev != 1 {
		t.Errorf("rev 가 %d다 — 변화가 없어도 개정 행은 쌓인다(사유가 원장에 남아야 한다)", res.Rev)
	}
}

// TestAmendPathsReportsOverlaps 는 경로를 고쳤을 때 겹치게 된 세션이 응답에 오는지 본다.
//
// 이 축이 없으면 고친 사람이 남의 화면을 움직였다는 사실을 알 경로가 하나도 없다.
// 살아 있는 세션은 pick_test.go 의 TestPickReportsOverlapWithoutFilteringIt 과 같은
// 방식으로 만든다 — 세션을 열고 그 세션이 문제의 경로를 만졌다고 비트를 찍는다.
func TestAmendPathsReportsOverlaps(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")

	me := openSession(t, s, "p1", repo, wt, "cc-1", "나")
	other := openSession(t, s, "p1", repo, repo, "cc-2", "다른세션")
	if err := s.Beat(ctx(), other.Session.ID, model.SignalTool,
		[]string{filepath.Join(repo, "server", "internal", "api", "x.go")}); err != nil {
		t.Fatalf("비트 실패: %v", err)
	}
	addItem(t, s, "p1", "i1", []string{"old/"}, nil)

	paths := []string{"server/internal/api/"}
	res, err := s.AmendItem(ctx(), AmendInput{
		Project: "p1", SessionID: me.Session.ID, ItemID: "i1", Paths: &paths, Reason: "리네임 추종",
	})
	if err != nil {
		t.Fatalf("고치지 못했다: %v", err)
	}
	if len(res.Overlaps) == 0 {
		t.Error("겹침이 비었다 — 방금 남의 경로로 옮겨 놓고 그 사실을 응답이 안 낸다")
	}
	if len(res.Overlaps) != 1 || res.Overlaps[0].SessionID != other.Session.ID {
		t.Fatalf("겹침 대상이 틀렸다: %+v", res.Overlaps)
	}
}

// TestAmendPathsUnchangedReportsNoOverlaps 는 paths 를 안 건드리면 겹침을 아예 안
// 내는지 본다 — 냈으면 그 겹침은 이 수정이 만든 것이 아니라 원래 있던 사실인데,
// 응답에 실으면 고친 사람이 자기가 방금 만든 겹침으로 잘못 읽는다.
func TestAmendPathsUnchangedReportsNoOverlaps(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")

	me := openSession(t, s, "p1", repo, wt, "cc-1", "나")
	other := openSession(t, s, "p1", repo, repo, "cc-2", "다른세션")
	if err := s.Beat(ctx(), other.Session.ID, model.SignalTool,
		[]string{filepath.Join(repo, "shared", "x.go")}); err != nil {
		t.Fatalf("비트 실패: %v", err)
	}
	// 항목의 경로가 이미 겹친다 — 하지만 이번 amend 는 title 만 고친다.
	addItem(t, s, "p1", "i1", []string{"shared/"}, nil)

	title := "새 제목"
	res, err := s.AmendItem(ctx(), AmendInput{
		Project: "p1", SessionID: me.Session.ID, ItemID: "i1", Title: &title, Reason: "제목만 고친다",
	})
	if err != nil {
		t.Fatalf("고치지 못했다: %v", err)
	}
	if len(res.Overlaps) != 0 {
		t.Errorf("Overlaps 가 %+v다 — paths 를 안 건드렸으니 비어야 한다", res.Overlaps)
	}
}
