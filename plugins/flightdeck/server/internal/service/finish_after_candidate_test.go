package service

import (
	"context"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/gitreader"
	"github.com/kweiza/flightdeck/internal/model"
)

// ─────────────────────────────────────────────────────────────────────────────
// 선행 후보 sha — 랜딩됐을 때만, 그리고 **이 브랜치의** head 만
// ─────────────────────────────────────────────────────────────────────────────
//
// `landed_ref` 는 Tier A 에서 영영 NULL 이다(설계 §3). 그 대가로 후속을 쓰는 사람이
// 걸 `dep_sha` 를 못 얻는 것이 실측된 사고를 냈다 — 전제가 3일 미랜딩인 항목이 선행
// 없이 큐에 남아 기아 78h 1순위로 추천됐다.
//
// 이 시험들이 지키는 계약은 셋이다:
//   ① 랜딩됐을 때만 낸다 — 안 그러면 후속이 즉시 충족으로 읽혀 아무것도 안 기다린다
//   ② **메인 트리의 HEAD 를 절대 안 낸다** — §3 이 없앤 결함(3회 관측)이 그것이다
//   ③ 후속이 없으면 git 을 아예 안 부른다 — 마무리의 흔한 경로에 비용을 안 얹는다

// finishWithFollowup 은 항목 하나를 닫으면서 후속 하나를 만든다.
func finishWithFollowup(t *testing.T, s *Service, sessionID, itemID, followupID string) FinishResult {
	t.Helper()
	out, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: sessionID, ItemID: itemID,
		Outcome: model.ItemDone, Title: "끝냈다",
		Body:      "왜 그렇게 했나 · 무엇을 기각했나 · 일부러 안 한 것 · 확인했으나 못 한 것",
		Followups: []FollowupInput{{ID: followupID, Title: "후속", Body: "후속 본문"}},
	})
	if err != nil {
		t.Fatalf("마무리 실패: %v", err)
	}
	return out
}

// afterCandidateReasons 는 이 축이 남긴 사유만 모은다.
func afterCandidateReasons(r FinishResult) []string {
	var out []string
	for _, f := range r.Failures {
		if f.Axis == afterCandidateAxis {
			out = append(out, f.Detail)
		}
	}
	return out
}

// TestAfterCandidateIsTheBranchHeadWhenLanded 는 브랜치가 기본 브랜치의 조상일 때
// **그 브랜치의 head** 가 후보로 나오는지 본다.
//
// 워크트리를 갓 만들면 head 가 main tip 과 같으므로 조상 판정이 참이다 — 즉 이
// 시험에서 "랜딩됐다"는 상태가 자연히 성립한다.
func TestAfterCandidateIsTheBranchHeadWhenLanded(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "closing", nil, nil)
	claimed(t, s, "p", me.Session.ID, "closing")

	out := finishWithFollowup(t, s, me.Session.ID, "closing", "spawned")

	if out.AfterCandidate == "" {
		t.Fatalf("브랜치가 이미 조상인데 선행 후보가 비었다(사유 %v) — "+
			"이 값이 없으면 후속을 쓰는 사람이 dep_sha 를 어디서도 못 얻는다",
			afterCandidateReasons(out))
	}
	want := strings.TrimSpace(runGit(t, wt, "rev-parse", "HEAD"))
	if out.AfterCandidate != want {
		t.Fatalf("선행 후보가 이 브랜치의 head 가 아니다: got %q, want %q",
			out.AfterCandidate, want)
	}
}

// TestAfterCandidateStaysEmptyBeforeLanding 은 아직 랜딩 안 된 브랜치에서 후보를
// 안 내는지 본다.
//
// ★ 내면 더 나쁘다. 후속이 그 sha 를 선행으로 걸어도 `pick` 이 즉시 충족으로 읽어
// 아무것도 안 기다린다 — 선행이 걸린 것처럼 보이는데 실제로는 안 걸린 상태다.
func TestAfterCandidateStaysEmptyBeforeLanding(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	// 브랜치에 커밋을 얹는다 — 이제 head 는 main 의 조상이 아니다(= 아직 랜딩 전).
	writeFile(t, wt, "wip.txt", "작업 중\n")
	runGit(t, wt, "add", "-A")
	runGit(t, wt, "commit", "-q", "-m", "wip")

	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "closing", nil, nil)
	claimed(t, s, "p", me.Session.ID, "closing")

	out := finishWithFollowup(t, s, me.Session.ID, "closing", "spawned")

	if out.AfterCandidate != "" {
		t.Fatalf("랜딩 전인데 선행 후보를 냈다(%q) — 후속이 그것을 걸면 즉시 충족이라 "+
			"아무것도 안 기다린다", out.AfterCandidate)
	}
	reasons := strings.Join(afterCandidateReasons(out), " | ")
	if !strings.Contains(reasons, "조상이 아니다") {
		t.Errorf("왜 안 냈는지가 사유에 없다 — 침묵하면 '읽었는데 없다'와 '못 읽었다'가 "+
			"같은 화면이 된다: %q", reasons)
	}
}

// noWorktreeListReader 는 `worktree list` 가 **성공하지만 이 경로를 안 내는** 상태다.
// 심볼릭 링크·bind 마운트처럼 경로 해석이 갈리면 실제로 이렇게 된다.
type noWorktreeListReader struct{ GitReader }

func (r noWorktreeListReader) Worktrees(ctx context.Context) ([]gitreader.Worktree, error) {
	return nil, nil
}

// TestAfterCandidateRefusesTheMainTreeHead 는 이 관문의 **핵심**이다.
//
// `worktreeFacts` 는 워크트리 목록에서 경로를 못 찾으면 `Ref(ctx, "HEAD")` 로 sha 만
// 건진다. 그 HEAD 는 **저장소 경로(= 메인 트리)의 것**이지 이 세션 브랜치의 것이 아니다.
// 그것을 선행 후보로 내면 설계 §3 이 없앤 결함 — "메인 트리의 지금 HEAD 를 적어 남의
// 커밋이 박히던"(3회 관측) — 을 이 자리에서 되살린다.
//
// 그래서 branch 가 비면(= 폴백이면) 후보를 안 낸다. 이 시험은 폴백 sha 가 **실제로
// 존재하고 낼 수 있었다**는 것까지 확인한다 — 안 그러면 "막았다"가 아니라 "애초에
// 값이 없었다"를 초록으로 읽게 된다.
func TestAfterCandidateRefusesTheMainTreeHead(t *testing.T) {
	_, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	blind := New(st, nil, WithGitFactory(func(repoPath string) GitReader {
		return noWorktreeListReader{GitReader: gitreader.New(repoPath)}
	}))

	me := openSession(t, blind, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, blind, "p", "closing", nil, nil)
	claimed(t, blind, "p", me.Session.ID, "closing")

	out := finishWithFollowup(t, blind, me.Session.ID, "closing", "spawned")

	if out.AfterCandidate != "" {
		t.Fatalf("워크트리 목록에 경로가 없는데 후보를 냈다(%q) — 그 값은 이 브랜치가 아니라 "+
			"메인 트리의 HEAD 다. 설계 §3 이 없앤 결함(남의 커밋이 박힌다)이 여기서 되살아난다",
			out.AfterCandidate)
	}
	// 폴백 값이 실제로 존재했음을 못박는다 — "막았다"와 "값이 없었다"를 가른다.
	mainHead := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	if mainHead == "" {
		t.Fatalf("메인 트리 HEAD 를 못 읽었다 — 이 시험이 아무것도 안 막고 있다")
	}
	reasons := strings.Join(afterCandidateReasons(out), " | ")
	if !strings.Contains(reasons, "메인 트리의 HEAD 로 대신하지 않는다") {
		t.Errorf("거절 사유가 화면에 안 나왔다: %q", reasons)
	}
}

// TestAfterCandidateSkipsGitWithoutFollowups 는 후속이 없으면 git 을 아예 안 부르는지
// 본다. 마무리의 흔한 경로가 이쪽이고, 여기에 git 호출을 얹으면 모든 finish 가 그
// 비용을 낸다.
func TestAfterCandidateSkipsGitWithoutFollowups(t *testing.T) {
	_, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")

	var worktreeCalls int
	counting := New(st, nil, WithGitFactory(func(repoPath string) GitReader {
		return countingReader{GitReader: gitreader.New(repoPath), n: &worktreeCalls}
	}))

	me := openSession(t, counting, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, counting, "p", "closing", nil, nil)
	claimed(t, counting, "p", me.Session.ID, "closing")
	before := worktreeCalls // 세션 열기가 이미 몇 번 불렀다. 마무리 몫만 센다

	out, err := counting.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "closing",
		Outcome: model.ItemDone, Title: "끝냈다",
		Body: "왜 그렇게 했나 · 무엇을 기각했나 · 일부러 안 한 것 · 확인했으나 못 한 것",
	})
	if err != nil {
		t.Fatalf("마무리 실패: %v", err)
	}
	if out.AfterCandidate != "" {
		t.Errorf("후속이 0건인데 선행 후보를 냈다: %q", out.AfterCandidate)
	}
	if got := worktreeCalls - before; got != 0 {
		t.Errorf("후속 0건인데 worktree 조회를 %d회 했다 — 이 경로는 git 을 안 불러야 한다", got)
	}
	if reasons := afterCandidateReasons(out); len(reasons) > 0 {
		t.Errorf("아예 재지 않았는데 사유를 남겼다: %v", reasons)
	}
}

// countingReader 는 Worktrees 호출 횟수를 센다.
type countingReader struct {
	GitReader
	n *int
}

func (r countingReader) Worktrees(ctx context.Context) ([]gitreader.Worktree, error) {
	*r.n++
	return r.GitReader.Worktrees(ctx)
}
