package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// stamp 는 t 시각을 store 가 쓰는 표기로 옮긴다. 시험이 신호·개시 시각을 직접
// 되돌릴 때 쓴다(store.fmtTime 과 같은 자리수 — 그쪽은 비공개다).
func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000Z") }

// newRepoWithWorktree 는 저장소 하나와 브랜치 하나짜리 워크트리를 만든다.
// 워크트리는 저장소 **밖**에 둔다 — 안에 두면 주 워크트리의 status 에 미추적으로 떠서
// 시험이 무엇을 재고 있는지 흐려진다.
func newRepoWithWorktree(t *testing.T, branch string) (repo, wt string) {
	t.Helper()
	repo = newRepo(t)
	wt = filepath.Join(filepath.Dir(repo), "wt-"+branch)
	runGit(t, repo, "worktree", "add", "-q", "-b", branch, wt)
	return repo, wt
}

func TestBoardSurvivesDirectoryWithoutGitAndSaysSo(t *testing.T) {
	s, _ := newSvc(t)
	dir := tmpBase(t) // git 저장소가 아니다
	sess := openSession(t, s, "p", dir, dir, "cc-1", "트랙2")

	view, err := s.Board(ctx(), "p", BoardOptions{Self: sess.Session.ID, IncludeQueue: true})
	if err != nil {
		t.Fatalf("git 이 없다고 보드가 죽으면 안 된다: %v", err)
	}

	// ① 조정은 산다 — 세션 행이 그대로 나온다.
	if len(view.Sessions) != 1 {
		t.Fatalf("세션 %d건, 기대 1건 — DB 만으로 완결되는 축이 파생 실패에 끌려 죽었다", len(view.Sessions))
	}
	card := view.Sessions[0]
	if card.View.Session.ID != sess.Session.ID || !card.IsSelf {
		t.Fatalf("세션 카드가 틀렸다: %+v", card)
	}

	// ② 그리고 침묵하지 않는다.
	if !view.Freshness.Stale || view.Freshness.Source != "db" {
		t.Fatalf("파생 실패가 신선도에 안 나타났다: %+v", view.Freshness)
	}
	if len(view.Failures) == 0 {
		t.Fatalf("실패 사유가 비었다")
	}
	var axes []string
	for _, f := range view.Failures {
		axes = append(axes, f.Axis)
	}
	if !contains(axes, "worktrees") {
		t.Fatalf("못 읽은 축이 이름으로 안 나왔다: %v", axes)
	}
	if card.DeriveError == "" {
		t.Fatalf("세션 카드에 파생 실패 표시가 없다 — 어느 세션의 값이 반쪽인지 알 수 없다")
	}

	// ③ 파생을 지어내지 않는다.
	if card.BranchKnown || card.AheadKnown || card.View.Branch != "" {
		t.Fatalf("못 읽은 파생을 안다고 표시했다: %+v", card)
	}
	// ④ 발자국 없음을 명시한다.
	if card.View.HasFootprint {
		t.Fatalf("발자국이 없는데 있다고 했다: %v", card.View.Paths)
	}
}

func TestBoardDerivesBranchAheadAndUnionOfPaths(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")

	// 브랜치에 커밋 하나 — change_set 축
	writeFile(t, wt, "pipeline/run.py", "print(1)\n")
	runGit(t, wt, "add", "-A")
	runGit(t, wt, "commit", "-q", "-m", "add pipeline")
	// 미커밋 하나 — 커밋 전 의도를 나르는 유일한 축
	writeFile(t, wt, "docs/plan.md", "초안\n")

	sess := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	// 훅이 준 발자국(절대경로) — footprint 축
	if err := s.Beat(ctx(), sess.Session.ID, model.SignalTool,
		[]string{filepath.Join(wt, "tools", "x.sh")}); err != nil {
		t.Fatalf("비트 실패: %v", err)
	}
	// 아무것도 안 만지는 세션 — 조사·판정만 하는 세션이 이 모양이다
	idle := openSession(t, s, "p", repo, repo, "cc-2", "트랙7")

	view, err := s.Board(ctx(), "p", BoardOptions{Self: sess.Session.ID})
	if err != nil {
		t.Fatalf("보드 실패: %v", err)
	}
	if view.Freshness.Source != "git" || view.Freshness.Stale {
		t.Fatalf("git 을 다 읽었는데 신선도가 %+v 다 (실패: %+v)", view.Freshness, view.Failures)
	}

	byID := map[string]SessionCard{}
	for _, c := range view.Sessions {
		byID[c.View.Session.ID] = c
	}
	work := byID[sess.Session.ID]
	if !work.BranchKnown || work.View.Branch != "feat" {
		t.Fatalf("브랜치 파생이 틀렸다: %+v (%s)", work, work.DeriveError)
	}
	if !work.AheadKnown || work.View.AheadMain != 1 {
		t.Fatalf("ahead 파생이 틀렸다: known=%v ahead=%d", work.AheadKnown, work.View.AheadMain)
	}
	// footprint ∪ change_set ∪ 미커밋 이 한 축으로 합쳐진다.
	//
	// ★ 미커밋 항목이 "docs/plan.md" 가 아니라 "docs/" 인 것은 git 이 **미추적 디렉토리를
	//   한 줄로 접기** 때문이다(실물로 확인했다). 손실이 아니다 — judge.PathsOverlap 이
	//   성분 단위로 보므로 "docs/" 는 "docs/plan.md" 의 조상으로 겹친다.
	//   여기서 파일 단위로 펼치려 들면 status 를 두 번 부르게 되고, 그 두 벌이 표류한다.
	for _, want := range []string{"pipeline/run.py", "docs/", "tools/x.sh"} {
		if !contains(work.View.Paths, want) {
			t.Fatalf("경로 %q 가 빠졌다: %v", want, work.View.Paths)
		}
	}
	if !work.View.HasFootprint {
		t.Fatalf("경로가 있는데 발자국 없음으로 표시됐다")
	}

	// ★ 커밋도 편집도 안 하는 세션은 경로 축에서 아무도 안 막는다.
	//   **안 막는다는 사실이 화면에 있어야 한다**(설계 §5).
	rest := byID[idle.Session.ID]
	if rest.View.HasFootprint || len(rest.View.Paths) != 0 {
		t.Fatalf("발자국이 없어야 하는 세션에 경로가 있다: %v", rest.View.Paths)
	}
	if !rest.BranchKnown || rest.View.Branch != "main" {
		t.Fatalf("주 워크트리의 브랜치가 안 잡혔다: %+v", rest)
	}
}

func TestBoardCarriesSignalsWithoutJudgingLiveness(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	sess := openSession(t, s, "p", repo, repo, "cc-1", "트랙2")
	if err := s.Beat(ctx(), sess.Session.ID, model.SignalPrompt, nil); err != nil {
		t.Fatalf("비트 실패: %v", err)
	}

	view, err := s.Board(ctx(), "p", BoardOptions{})
	if err != nil {
		t.Fatalf("보드 실패: %v", err)
	}
	sig := view.Sessions[0].View.Signals
	if _, ok := sig[model.SignalPrompt]; !ok {
		t.Fatalf("prompt 신호 시각이 없다: %v", sig)
	}
	if _, ok := sig[model.SignalCommit]; ok {
		t.Fatalf("관측하지 않은 commit 신호가 생겼다 — 넷을 합치면 반드시 오판한다")
	}
	// 창을 좁혀도 "죽었다"가 생기지 않는다. 목록에서 빠질 뿐이고, 그 판단은 호출자의 창이다.
	narrow, err := s.Board(ctx(), "p", BoardOptions{Window: 1})
	if err != nil {
		t.Fatalf("보드 실패: %v", err)
	}
	if len(narrow.Sessions) != 0 {
		t.Fatalf("1나노초 창에는 아무도 안 걸려야 한다: %d건", len(narrow.Sessions))
	}
}

func TestBoardIncludesQueueAndNotesOnlyWhenAsked(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	sess := openSession(t, s, "p", repo, repo, "cc-1", "")
	addItem(t, s, "p", "batch7", []string{"pipeline/"}, nil)
	if _, err := s.Note(ctx(), NoteInput{
		Project: "p", SessionID: sess.Session.ID, Kind: model.JudgmentBlocked,
		Body: "계약 개정 대기",
	}); err != nil {
		t.Fatalf("판단 저장 실패: %v", err)
	}

	plain, err := s.Board(ctx(), "p", BoardOptions{})
	if err != nil {
		t.Fatalf("보드 실패: %v", err)
	}
	if len(plain.OpenItems) != 0 || len(plain.Blocked) != 0 {
		t.Fatalf("요청하지 않은 절이 실렸다(토큰 예산): items=%d blocked=%d",
			len(plain.OpenItems), len(plain.Blocked))
	}

	full, err := s.Board(ctx(), "p", BoardOptions{IncludeQueue: true, IncludeNotes: true})
	if err != nil {
		t.Fatalf("보드 실패: %v", err)
	}
	if len(full.OpenItems) != 1 || full.OpenItems[0].ID != "batch7" {
		t.Fatalf("큐가 안 실렸다: %+v", full.OpenItems)
	}
	if len(full.Blocked) != 1 || !strings.Contains(full.Blocked[0].Body, "계약 개정") {
		t.Fatalf("막힘이 안 실렸다: %+v", full.Blocked)
	}
	if full.Sessions[0].View.LastNote == nil {
		t.Fatalf("세션의 마지막 판단이 안 실렸다")
	}
}

func TestBoardRefusesUnknownProject(t *testing.T) {
	s, _ := newSvc(t)
	if _, err := s.Board(ctx(), "없는프로젝트", BoardOptions{}); err == nil {
		t.Fatalf("미등록 프로젝트는 파생 실패가 아니라 설정 오류다 — 접지 말고 올려야 한다")
	}
}

func TestDefaultLiveWindowIsTwoHours(t *testing.T) {
	if DefaultLiveWindow != 2*time.Hour {
		t.Fatalf("기본 창이 2시간이 아니다: %v", DefaultLiveWindow)
	}
}

// TestBoardOldestOutsideOnlyCountsHiddenSessions 는 화면에 **보이는** 세션의 옛 신호가
// OldestOutside 를 오염시키지 않는다는 것을 단정한다.
//
// 세션 하나가 최근 prompt 신호로 창 안에 있으면서 동시에 훨씬 오래된 commit 신호를
// 가질 수 있다(신호는 종류별 최신 시각 하나씩만 남으므로 이 조합이 실물에서 난다).
// OldestOutside 는 **숨은 세션**(그 세션의 신호 중 어느 것도 창 안에 없는 세션)의
// "마지막으로 언제 봤나"(그 세션 신호의 최댓값) 중 최솟값이어야 한다 — 보이는 세션의
// 신호 하나하나를 훑어 그중 창 밖인 것을 집으면 안 된다.
func TestBoardOldestOutsideOnlyCountsHiddenSessions(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	now := time.Now().UTC()

	// 보이는 세션 — 최근 prompt 로 창 안에 있다. 그런데 6시간 전 commit 신호도 있다.
	visible := openSession(t, s, "p", repo, repo, "cc-visible", "보이는")
	if err := s.Beat(ctx(), visible.Session.ID, model.SignalPrompt, nil); err != nil {
		t.Fatalf("비트 실패: %v", err)
	}
	if _, err := st.DB().ExecContext(ctx(),
		`INSERT INTO signal(session_id, kind, at) VALUES (?, 'commit', ?)`,
		visible.Session.ID, stamp(now.Add(-6*time.Hour))); err != nil {
		t.Fatalf("옛 신호 심기 실패: %v", err)
	}

	// 숨은 세션 — 개시 시각도 신호도 전부 창(2시간) 밖인 3시간 전이다.
	hidden := openSession(t, s, "p", repo, repo, "cc-hidden", "숨은")
	hiddenAt := stamp(now.Add(-3 * time.Hour))
	if _, err := st.DB().ExecContext(ctx(),
		`UPDATE session SET opened_at = ? WHERE id = ?`, hiddenAt, hidden.Session.ID); err != nil {
		t.Fatalf("세션 개시 시각 되돌리기 실패: %v", err)
	}
	if _, err := st.DB().ExecContext(ctx(),
		`UPDATE signal SET at = ? WHERE session_id = ?`, hiddenAt, hidden.Session.ID); err != nil {
		t.Fatalf("신호 시각 되돌리기 실패: %v", err)
	}
	// ★ 신호를 **심는다**. 이 시험이 재는 것은 OldestOutside 이고 그 재료가 신호인데,
	// 그 신호가 지금까지는 세션 열기가 찍던 mcp 비트였다. 열기가 신호를 안 찍게 되면
	// 위 UPDATE 는 0행이 되고 이 세션의 신호는 0건이 된다 — 그러면 board.go 의
	// `if lastSeen.IsZero() { continue }` 가 걸려 OldestOutside 가 비고,
	// 이 시험은 "화면이 침묵한다"를 통과로 읽는다. 재는 축을 픽스처가 직접 세운다.
	if _, err := st.DB().ExecContext(ctx(),
		`INSERT INTO signal(session_id, kind, at) VALUES (?, 'prompt', ?)`,
		hidden.Session.ID, hiddenAt); err != nil {
		t.Fatalf("숨은 세션 신호 심기 실패: %v", err)
	}

	view, err := s.Board(ctx(), "p", BoardOptions{})
	if err != nil {
		t.Fatalf("보드 실패: %v", err)
	}
	if view.OutOfWindow != 1 {
		t.Fatalf("창 밖 건수 = %d, 기대 1 (숨은 세션 하나)", view.OutOfWindow)
	}
	if view.OldestOutside.IsZero() {
		t.Fatalf("창 밖 가장 오래된 신호가 비었다")
	}
	want := now.Add(-3 * time.Hour)
	if d := view.OldestOutside.Sub(want); d < -2*time.Second || d > 2*time.Second {
		t.Fatalf("OldestOutside = %v, 기대 약 %v(숨은 세션 것) — 보이는 세션의 옛 신호가 섞였다",
			view.OldestOutside, want)
	}
}

// TestBoardRecordsFailureWhenOutOfWindowCountFails 는 창 밖 건수 질의가 실패해도
// 조회 자체는 죽지 않고, 실패가 축 이름으로 남는다는 것을 단정한다.
//
// 이 질의는 카드용 질의와 같은 표·같은 함수를 쓰므로 실물 DB 로는 이 질의 하나만 골라
// 실패시킬 수 없다 — GitReader 주입과 같은 이유로 여기서도 후크를 주입한다.
func TestBoardRecordsFailureWhenOutOfWindowCountFails(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	openSession(t, s, "p", repo, repo, "cc-1", "트랙2")

	boom := errors.New("out-of-window 질의 실패(시험)")
	s.outOfWindowLister = func(_ context.Context, _ string, _ time.Time) ([]model.SessionView, error) {
		return nil, boom
	}

	view, err := s.Board(ctx(), "p", BoardOptions{})
	if err != nil {
		t.Fatalf("파생 실패가 조회 자체를 죽이면 안 된다: %v", err)
	}
	var axes []string
	for _, f := range view.Failures {
		axes = append(axes, f.Axis)
	}
	if !contains(axes, "out-of-window") {
		t.Fatalf("out-of-window 실패가 축 이름으로 안 나왔다: %v", axes)
	}
	if view.OutOfWindow != 0 {
		t.Fatalf("실패했는데 창 밖 건수가 채워졌다: %d", view.OutOfWindow)
	}
	if !view.Freshness.Stale {
		t.Fatalf("파생 실패가 신선도에 안 나타났다: %+v", view.Freshness)
	}
}

// TestBoardChangeSetIsForkPointNotTwoDot 는 **브랜치가 손대지 않은 파일이 그 세션의
// 경로로 나오지 않는다**를 소비자 좌표계에서 단정한다.
//
// ★ 왜 이 시험이 있나. sessionCards 가 `ChangedPaths(main, branch)` 를 부르면 그것은
// **두 점 diff** 라 두 끝점을 비교한다 — main 만 바꾼 파일도 브랜치의 변경으로 들어온다.
// 그러면 main 에 커밋이 하나 랜딩할 때마다 그 커밋이 건드린 파일이 **살아 있는 모든
// 브랜치**의 발자국에 더해진다. 브랜치가 오래 살수록, main 이 바쁠수록 오탐이 는다 —
// 단조 악화다. 설계 §5 가 겹침을 "거르지 않고 알린다"이므로 그 오탐은 곧바로 화면에
// 나가고, 거짓 겹침이 늘면 세션들이 겹침 줄 자체를 안 읽게 된다(실측: 겹침 6건 중 3건 오탐).
func TestBoardChangeSetIsForkPointNotTwoDot(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")

	// 브랜치가 실제로 만진 파일.
	writeFile(t, wt, "branch/only.py", "print(1)\n")
	runGit(t, wt, "add", "-A")
	runGit(t, wt, "commit", "-q", "-m", "branch touches its own file")

	// main 이 그 뒤로 앞선다. 이 파일을 브랜치는 **한 번도 안 만졌다**.
	writeFile(t, repo, "main/only.md", "main 만 고쳤다\n")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-q", "-m", "main moves ahead")

	sess := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	view, err := s.Board(ctx(), "p", BoardOptions{Self: sess.Session.ID})
	if err != nil {
		t.Fatalf("보드 실패: %v", err)
	}

	var card SessionCard
	for _, c := range view.Sessions {
		if c.View.Session.ID == sess.Session.ID {
			card = c
		}
	}
	if !card.BranchKnown || card.View.Branch != "feat" {
		t.Fatalf("브랜치 파생이 틀렸다: %+v (%s)", card, card.DeriveError)
	}

	// ① 자기가 만진 것은 남는다.
	if !contains(card.View.Paths, "branch/only.py") {
		t.Fatalf("브랜치가 만진 경로가 빠졌다: %v", card.View.Paths)
	}
	// ② 남이 만진 것은 안 붙는다. 이것이 이 시험의 전부다.
	if contains(card.View.Paths, "main/only.md") {
		t.Fatalf("브랜치가 한 번도 안 만진 파일이 그 세션의 경로로 나왔다: %v\n"+
			"두 점 diff 는 두 끝점을 비교한다 — 갈래 지점(merge-base)을 base 로 넘겨야 한다", card.View.Paths)
	}
}

// TestBoardRemembersChangeSetKeyedByForkPoint 는 보관된 change_set 의 base_sha 가
// **실제로 diff 를 뜬 그 커밋**인지 단정한다.
//
// ★ change_set 은 (base_sha, head_sha) 를 키로 "두 커밋 사이"를 불변 보관한다.
// 갈래 기준 경로를 담으면서 base_sha 에 main 의 tip 을 적으면 그 행은 거짓이 된다 —
// 나중에 그 키로 읽는 쪽은 두 점 diff 를 기대하는데 내용은 갈래 기준이기 때문이다.
// merge-base 를 적으면 뜻이 정확히 보존된다: 갈래 기준 diff 는 merge-base 로부터의
// 두 점 diff 와 **문자 그대로 같다**. 그래서 base 를 merge-base 로 통일한다.
func TestBoardRemembersChangeSetKeyedByForkPoint(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")

	forkSHA := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))

	writeFile(t, wt, "branch/only.py", "print(1)\n")
	runGit(t, wt, "add", "-A")
	runGit(t, wt, "commit", "-q", "-m", "branch touches its own file")
	headSHA := strings.TrimSpace(runGit(t, wt, "rev-parse", "HEAD"))

	writeFile(t, repo, "main/only.md", "main 만 고쳤다\n")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-q", "-m", "main moves ahead")

	sess := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	if _, err := s.Board(ctx(), "p", BoardOptions{Self: sess.Session.ID}); err != nil {
		t.Fatalf("보드 실패: %v", err)
	}

	cs, err := st.GetChangeSet(ctx(), "p", forkSHA, headSHA)
	if err != nil {
		t.Fatalf("갈래 지점을 base 로 한 change_set 이 없다(base=%s head=%s): %v", forkSHA, headSHA, err)
	}
	if !contains(cs.Paths, "branch/only.py") {
		t.Fatalf("보관된 경로가 틀렸다: %v", cs.Paths)
	}
	if contains(cs.Paths, "main/only.md") {
		t.Fatalf("보관된 change_set 에 브랜치가 안 만진 파일이 있다: %v", cs.Paths)
	}
}
