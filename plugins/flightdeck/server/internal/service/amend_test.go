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
	// Before·Item 을 지워도(할당 자체를 빼도) 위 두 단정은 여전히 초록이다 —
	// 이 값들이 실제로 채워지는지는 따로 재야 한다.
	if res.Before.Title != "원래 제목" || res.Before.Body != "원래 본문" {
		t.Errorf("Before 가 %+v다 — 고치기 직전의 값이어야 한다", res.Before)
	}
	if res.Item.Title != "원래 제목" || res.Item.Body != "원래 본문" {
		t.Errorf("Item 이 %+v다 — 되읽은 값이어야 한다", res.Item)
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

// TestAmendPathsReportsSiblingProjectOverlap 은 **형제 프로젝트**의 살아 있는 세션도
// 겹침에 잡히는지 본다(board.go 의 liveOverlapSessions 안 siblingLive 블록).
//
// TestAmendPathsReportsOverlaps·TestAmendPathsUnchangedReportsNoOverlaps 둘 다 같은
// 프로젝트 안의 세션만 쓴다 — 그래서 siblingLive 블록을 통째로 지워도 그 둘은
// 초록으로 남는다(리뷰 I-2). 워크스페이스 명부(newWSFixture, workspace_behavior_test.go)
// 위에서 형제 프로젝트의 세션이 실제로 겹침에 실리는지를 이 시험이 잠근다.
func TestAmendPathsReportsSiblingProjectOverlap(t *testing.T) {
	f := newWSFixture(t)

	// 형제 프로젝트(search-api, 디렉토리는 member-a) 세션이 자기 좌표로 파일을 만진다.
	if err := f.svc.Beat(ctx(), f.memberASes, model.SignalTool,
		[]string{filepath.Join(f.root, "member-a", "server", "foo.go")}); err != nil {
		t.Fatalf("비트 실패: %v", err)
	}
	addItem(t, f.svc, "repo", "i1", []string{"old/"}, nil)

	// 루트 좌표계에서 형제 세션이 만진 자리로 옮긴다(PathAsSeenFrom 의 그 변환 —
	// TestOverlapCrossesTheWorkspace 가 같은 변환을 처방 경로에서 이미 확인했다).
	paths := []string{"member-a/"}
	res, err := f.svc.AmendItem(ctx(), AmendInput{
		Project: "repo", SessionID: f.rootSess, ItemID: "i1", Paths: &paths, Reason: "형제 겹침 시험",
	})
	if err != nil {
		t.Fatalf("고치지 못했다: %v", err)
	}
	found := false
	for _, ov := range res.Overlaps {
		if ov.SessionID == f.memberASes {
			found = true
		}
	}
	if !found {
		t.Fatalf("형제 프로젝트 세션(%s)과의 겹침이 안 잡혔다: %+v", f.memberASes, res.Overlaps)
	}
}

// TestAmendExcludesSiblingCardFromOverlap 은 **같은 대화(cc)의 다른 카드**를 겹침에서
// 빼는지 본다(board.go 의 liveOverlapSessions 안 selfCCOf(cards, self) 호출).
//
// TestAmendPathsReportsOverlaps 는 self 카드가 하나뿐이라 selfCC 를 `""` 로 되돌려도
// 초록이다(리뷰 I-3) — 그 mutant 를 실제로 잡으려면 같은 cc 로 카드 두 장을 만들어야
// 한다. 재현하는 모양은 pick_test.go 의 TestPickDoesNotReportSiblingCardAsOverlap 과
// 같다(cc 표류·워크트리 갈림으로 한 대화가 카드 두 장이 된 실측 상태).
func TestAmendExcludesSiblingCardFromOverlap(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")

	me := openSession(t, s, "p1", repo, wt, "cc-1", "내 카드")
	sibling := openSession(t, s, "p1", repo, repo, "cc-1", "같은 대화의 다른 카드")
	other := openSession(t, s, "p1", repo, repo, "cc-2", "진짜 남")

	// 형제와 남이 같은 경로를 만진다 — selfCC 판정이 안 돌면 둘 다 겹침으로 나온다.
	for _, id := range []string{sibling.Session.ID, other.Session.ID} {
		if err := s.Beat(ctx(), id, model.SignalTool,
			[]string{filepath.Join(repo, "server", "internal", "api", "x.go")}); err != nil {
			t.Fatalf("비트 실패: %v", err)
		}
	}
	addItem(t, s, "p1", "i1", []string{"old/"}, nil)

	paths := []string{"server/internal/api/"}
	res, err := s.AmendItem(ctx(), AmendInput{
		Project: "p1", SessionID: me.Session.ID, ItemID: "i1", Paths: &paths, Reason: "겹침 재기",
	})
	if err != nil {
		t.Fatalf("고치지 못했다: %v", err)
	}
	for _, ov := range res.Overlaps {
		if ov.SessionID == sibling.Session.ID {
			t.Fatalf("형제 카드가 겹침으로 나왔다 — 세션이 자기 자신과 조율하라는 화면이다: %+v", res.Overlaps)
		}
	}
	// ★ 형제를 뺀 것과 축을 통째로 꺼서 아무도 안 걸린 것을 가른다 — 진짜 남은 남아야 한다.
	if len(res.Overlaps) != 1 || res.Overlaps[0].SessionID != other.Session.ID {
		t.Fatalf("진짜 남과의 겹침이 사라졌다 — 형제를 빼면서 축을 통째로 껐다: %+v", res.Overlaps)
	}
}

// TestAmendOverlapsDeriveFailureUsesTheOverlapsAxis 는 겹침 계산이 실패했을 때
// Derived.Failures 의 축 이름이 정확히 "overlaps" 인지 잠근다.
//
// 이 이름은 다음 태스크(렌더러)가 deriveFailed(res.Derived, "overlaps") 로 읽을
// 이름이다 — 이름이 조용히 바뀌면 렌더가 "못 셌다"를 "0건"으로 찍는데 아무도 안
// 잡는다(0 과 못 잼을 가르는 것이 이 축의 요점이다, 리뷰 M-3).
//
// 실패는 실물로 만든다: signal 표를 지운다. sessionCards→ListLive 가 그 표를
// 직접 질의문에 넣어 쓰므로 표가 없으면 하드 에러가 난다. item·item_revision·
// project 는 안 건드리므로 **쓰기와 되읽기는 그대로 성공한다** — 겹침 축만 골라
// 실패시키는 것이 이 시험의 핵심이다(item 축까지 같이 죽으면 재려던 것을 못 가른다).
func TestAmendOverlapsDeriveFailureUsesTheOverlapsAxis(t *testing.T) {
	s, st := newSvc(t)
	mustAddItem(t, st, model.Item{Project: "p1", ID: "i1", Title: "t", Body: "b", Paths: []string{"old/"}})

	if _, err := st.DB().ExecContext(ctx(), "DROP TABLE signal"); err != nil {
		t.Fatalf("signal 표 제거 실패: %v", err)
	}

	paths := []string{"new/"}
	res, err := s.AmendItem(ctx(), AmendInput{
		Project: "p1", ItemID: "i1", Paths: &paths, Reason: "파생 실패 축 이름 고정",
	})
	if err != nil {
		t.Fatalf("쓰기까지 실패했다 — 파생 실패가 결과를 죽이면 안 된다: %v", err)
	}
	if res.Rev != 1 {
		t.Fatalf("rev=%d — 쓰기 자체는 성공했어야 한다(signal 표는 item 과 무관하다)", res.Rev)
	}
	if res.Item.Title != "t" {
		t.Fatalf("되읽기(item 축)까지 실패한 것 같다 — 겹침 축만 죽였어야 한다: %+v", res.Item)
	}
	found := false
	for _, f := range res.Derived.Failures {
		if f.Axis == "overlaps" {
			found = true
		}
	}
	if !found {
		t.Fatalf("겹침 파생이 실패했는데 축 이름이 'overlaps' 로 안 잡혔다: %+v", res.Derived.Failures)
	}
}
