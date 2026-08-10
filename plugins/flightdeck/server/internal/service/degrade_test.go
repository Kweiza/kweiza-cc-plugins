package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/gitreader"
	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
)

// flakyReader 는 축 하나만 실패하는 리더다.
//
// 실물 저장소로는 "브랜치는 읽히는데 diff 만 실패한다"를 만들기 어렵다.
// 그 **부분 실패** 구간이 신선도 판정의 세 상태 중 하나이므로 주입으로 덮는다 —
// 나머지 축은 전부 실물 git 이 답한다(가짜가 실물을 대신하지 않는다).
type flakyReader struct {
	GitReader
	failChanged          bool
	failAhead            bool
	failMergeBase        bool
	failUncommittedDelta bool
}

var errInjected = errors.New("주입된 실패: 변경 경로를 못 읽는다")

func (f flakyReader) MergeBase(ctx context.Context, a, b string) (string, error) {
	if f.failMergeBase {
		return "", errInjected
	}
	return f.GitReader.MergeBase(ctx, a, b)
}

func (f flakyReader) ChangedPaths(ctx context.Context, base, head string) ([]string, map[string]model.LineDelta, error) {
	if f.failChanged {
		return nil, nil, errInjected
	}
	return f.GitReader.ChangedPaths(ctx, base, head)
}

// ★ **UncommittedPaths 는 안 감싼다.** 이 fake 의 요점이 "규모 축만 죽여도 경로 축이 사는가"라서,
// 둘을 같이 죽이면 그 단정이 성립하지 않는다.
func (f flakyReader) UncommittedDelta(ctx context.Context, worktree string) (map[string]model.LineDelta, error) {
	if f.failUncommittedDelta {
		return nil, errInjected
	}
	return f.GitReader.UncommittedDelta(ctx, worktree)
}

func (f flakyReader) AheadBehind(ctx context.Context, ref, base string) (int, int, error) {
	if f.failAhead {
		return 0, 0, errInjected
	}
	return f.GitReader.AheadBehind(ctx, ref, base)
}

func TestBoardKeepsCoordinatingWhenOneDerivedAxisFails(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	writeFile(t, wt, "pipeline/run.py", "print(1)\n")
	runGit(t, wt, "add", "-A")
	runGit(t, wt, "commit", "-q", "-m", "add pipeline")

	sess := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")

	// 축 둘만 죽인 서비스를 다시 만든다(같은 DB, 같은 저장소).
	broken := New(st, nil, WithGitFactory(func(repoPath string) GitReader {
		return flakyReader{GitReader: gitreader.New(repoPath), failChanged: true, failAhead: true}
	}))

	view, err := broken.Board(ctx(), "p", BoardOptions{Self: sess.Session.ID})
	if err != nil {
		t.Fatalf("축 하나가 죽었다고 보드가 죽으면 안 된다: %v", err)
	}
	if len(view.Sessions) != 1 {
		t.Fatalf("세션이 %d건이다", len(view.Sessions))
	}
	card := view.Sessions[0]

	// 읽힌 축은 살아 있다.
	if !card.BranchKnown || card.View.Branch != "feat" {
		t.Fatalf("읽을 수 있는 축까지 죽었다: %+v", card)
	}
	// 못 읽은 축은 **모른다고** 말한다. 0 으로 채우면 "main 과 같다"로 읽힌다.
	if card.AheadKnown {
		t.Fatalf("못 읽은 ahead 를 안다고 표시했다")
	}
	if !strings.Contains(card.DeriveError, "변경 경로") || !strings.Contains(card.DeriveError, "ahead") {
		t.Fatalf("어느 축이 반쪽인지가 카드에 없다: %q", card.DeriveError)
	}
	// 부분 실패 — 읽은 것이 있으므로 출처는 git 이고, 실패가 있으므로 낡음이다.
	if view.Freshness.Source != "git" || !view.Freshness.Stale {
		t.Fatalf("부분 실패의 신선도가 %+v 다", view.Freshness)
	}
	var found bool
	for _, f := range view.Failures {
		if strings.HasPrefix(f.Axis, "changed-paths:") && strings.Contains(f.Detail, "주입된 실패") {
			found = true
		}
	}
	if !found {
		t.Fatalf("실패 사유 전문이 안 실렸다: %+v", view.Failures)
	}
}

func TestPickStillClaimsWhenGitIsGone(t *testing.T) {
	s, _ := newSvc(t)
	dir := tmpBase(t) // git 저장소가 아니다
	me := openSession(t, s, "p", dir, dir, "cc-1", "트랙2")
	addItem(t, s, "p", "batch7", []string{"pipeline/"}, nil)

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID, ItemID: "batch7"})
	if err != nil {
		t.Fatalf("git 이 없다고 선점이 죽으면 안 된다 — 배타는 DB 가 지킨다: %v", err)
	}
	if res.Mode != PickClaimed {
		t.Fatalf("mode = %s", res.Mode)
	}
	if !res.Freshness.Stale {
		t.Fatalf("파생이 통째로 죽었는데 신선도가 %+v 다", res.Freshness)
	}
	if len(res.Failures) == 0 {
		t.Fatalf("파생 실패가 침묵했다")
	}
	// 겹침 축은 발자국(DB)만으로도 돈다 — git 이 없어도 이 축이 죽지 않는다.
	if err := s.Beat(ctx(), me.Session.ID, model.SignalTool, []string{"pipeline/run.py"}); err != nil {
		t.Fatalf("비트 실패: %v", err)
	}
	other := openSession(t, s, "p", dir, dir, "cc-2", "트랙7")
	res2, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: other.Session.ID})
	if err != nil {
		t.Fatalf("추천 실패: %v", err)
	}
	// batch7 은 남이 쥐고 있으므로 적격 0건이고, 그 사유가 남아야 한다.
	if res2.Mode != PickNone {
		t.Fatalf("mode = %s", res2.Mode)
	}
	if _, ok := reasonCodes(res2.Rejected)["batch7/"+judge.RejectClaimed]; !ok {
		t.Fatalf("git 이 없다고 탈락 사유까지 사라졌다: %+v", res2.Rejected)
	}
}

// TestBoardEmptiesChangeSetAxisWhenForkPointIsUnreadable 는 갈래 지점을 못 읽었을 때
// **두 점 diff 로 되돌아가지 않는다**를 단정한다.
//
// ★ 이 시험이 지키는 것은 성능도 정확도도 아니라 **되돌아가지 않음**이다.
// merge-base 가 실패했을 때 `ChangedPaths(기본브랜치, 브랜치)` 로 물러서면 화면에는
// 경로가 그대로 차 있어 아무도 눈치채지 못한 채 오탐이 부활한다 — 침묵하는 회귀다.
// 그래서 못 구했으면 그 축을 비우고 못 읽었다고 말한다. 발자국·미커밋이 덮는다.
func TestBoardEmptiesChangeSetAxisWhenForkPointIsUnreadable(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")

	writeFile(t, wt, "branch/only.py", "print(1)\n")
	runGit(t, wt, "add", "-A")
	runGit(t, wt, "commit", "-q", "-m", "branch touches its own file")

	// main 이 앞선다. 두 점으로 물러서면 이 파일이 브랜치 경로에 나타난다.
	writeFile(t, repo, "main/only.md", "main 만 고쳤다\n")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-q", "-m", "main moves ahead")

	sess := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")

	broken := New(st, nil, WithGitFactory(func(repoPath string) GitReader {
		return flakyReader{GitReader: gitreader.New(repoPath), failMergeBase: true}
	}))
	view, err := broken.Board(ctx(), "p", BoardOptions{Self: sess.Session.ID})
	if err != nil {
		t.Fatalf("갈래 지점을 못 읽었다고 보드가 죽으면 안 된다: %v", err)
	}
	card := view.Sessions[0]

	// ① 두 점으로 물러서지 않았다 — 남의 파일도, 자기 커밋도 이 축으로는 안 온다.
	for _, p := range []string{"main/only.md", "branch/only.py"} {
		if contains(card.View.Paths, p) {
			t.Fatalf("갈래 지점을 못 읽었는데 변경집합 축이 채워졌다(%q): %v\n"+
				"두 점 diff 로 되돌아가면 오탐이 침묵 속에 부활한다", p, card.View.Paths)
		}
	}
	// ② 그리고 침묵하지 않는다.
	if !strings.Contains(card.DeriveError, "갈래 지점") {
		t.Fatalf("어느 축이 반쪽인지가 카드에 없다: %q", card.DeriveError)
	}
	var found bool
	for _, f := range view.Failures {
		if strings.HasPrefix(f.Axis, "merge-base:") && strings.Contains(f.Detail, "주입된 실패") {
			found = true
		}
	}
	if !found {
		t.Fatalf("실패 사유 전문이 안 실렸다: %+v", view.Failures)
	}
	if view.Freshness.Source != "git" || !view.Freshness.Stale {
		t.Fatalf("부분 실패의 신선도가 %+v 다", view.Freshness)
	}
}

// ★ 이 시험이 Task 2 에서 UncommittedPaths 와 UncommittedDelta 를 가른 이유를 잠근다.
func TestUncommittedDeltaFailureKeepsThePathAxisAlive(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	writeFile(t, wt, "pipeline/run.py", "print(1)\n")
	runGit(t, wt, "add", "-A")
	runGit(t, wt, "commit", "-q", "-m", "add pipeline")
	writeFile(t, wt, "pipeline/run.py", "print(1)\nprint(2)\n") // 미커밋

	sess := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")

	// 같은 DB·같은 저장소에, 규모 축만 죽인 서비스를 다시 만든다
	// (degrade_test.go 의 TestBoardKeepsCoordinatingWhenOneDerivedAxisFails 와 같은 관용).
	broken := New(st, nil, WithGitFactory(func(repoPath string) GitReader {
		return flakyReader{GitReader: gitreader.New(repoPath), failUncommittedDelta: true}
	}))
	view, err := broken.Board(ctx(), "p", BoardOptions{Self: sess.Session.ID})
	if err != nil {
		t.Fatalf("Board 실패: %v", err)
	}
	card := view.Sessions[0]

	// 경로 축은 산다 — UncommittedPaths 는 따로 돌기 때문이다.
	var found bool
	for _, p := range card.View.Paths {
		if p == "pipeline/run.py" {
			found = true
		}
	}
	if !found {
		t.Fatalf("규모를 못 읽었다고 경로까지 사라졌다: %q — 두 호출을 가른 이유가 이것이다", card.View.Paths)
	}
	// 그리고 침묵하지 않는다.
	if !strings.Contains(card.DeriveError, "미커밋 규모를 못 읽었다") {
		t.Errorf("열화 사유가 안 남았다: %q", card.DeriveError)
	}
}
