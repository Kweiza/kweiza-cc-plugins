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
	failChanged bool
	failAhead   bool
}

var errInjected = errors.New("주입된 실패: 변경 경로를 못 읽는다")

func (f flakyReader) ChangedPaths(ctx context.Context, base, head string) ([]string, error) {
	if f.failChanged {
		return nil, errInjected
	}
	return f.GitReader.ChangedPaths(ctx, base, head)
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
