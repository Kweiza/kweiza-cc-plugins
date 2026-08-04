package gitreader

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/judge"
)

// 이 파일의 시험은 **실물 git 저장소**를 임시 디렉토리에 만들어서 돈다.
//
// 픽스처 문자열만 단정하면 "내가 만든 문자열을 내가 파싱한다"가 되어 아무것도 지키지 못한다 —
// git 이 판올림에서 출력을 바꾸거나, 우리가 인자를 잘못 준 것은 그 시험에 원리적으로 안 잡힌다.
// 순수 파서의 표 시험(parse_test.go)은 **그 표가 실물과 같은지**를 여기가 보증할 때만 의미가 있다.

// TestMain 은 시험이 도는 머신의 git 설정에서 격리한다.
// 전역 설정에 hooksPath·safe.directory·commit.gpgsign 이 걸려 있으면 시험이 사람마다 다르게 돈다.
func TestMain(m *testing.M) {
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	os.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	// 실패 경로 시험이 많아 기본 로거를 그대로 두면 ERROR 줄이 시험 출력을 덮는다.
	// 로그를 **단정하는** 시험(TestFailureIsLoggedWithCauseAndCoordinate)은
	// 자기 로거를 WithLogger 로 주입하므로 이 조치에 안 가려진다.
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

// ─────────────────────────────────────────────────────────────────────────────
// 실물 저장소 만들기
// ─────────────────────────────────────────────────────────────────────────────

// runGit 은 시험이 저장소를 **준비**할 때 쓰는 git 이다(피시험 코드가 아니다).
// 전역 설정에 기대지 않도록 신원을 매번 -c 로 준다.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{
		"-C", dir,
		"-c", "user.name=fd test",
		"-c", "user.email=fd@test.invalid",
		"-c", "commit.gpgsign=false",
	}, args...)
	out, err := exec.Command("git", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("준비용 git %v 실패: %v\n%s", args, err, out)
	}
	return string(out)
}

// newRepo 는 커밋 없는 빈 저장소를 만들고 그 경로를 돌려준다.
// 경로는 심볼릭 링크를 푼 것이다 — macOS 의 /tmp 는 링크라 git 이 돌려주는 경로와 안 맞는다.
func newRepo(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("임시 경로 해석 실패: %v", err)
	}
	repo := filepath.Join(base, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("디렉토리 생성 실패: %v", err)
	}
	runGit(t, repo, "init", "-q", "-b", "main", ".")
	return repo
}

func write(t *testing.T, repo, rel, content string) {
	t.Helper()
	p := filepath.Join(repo, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("디렉토리 생성 실패: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("파일 쓰기 실패(%s): %v", rel, err)
	}
}

// commit 은 전부 스테이징해 커밋하고 그 sha 를 돌려준다.
func commit(t *testing.T, repo, msg string) string {
	t.Helper()
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-q", "-m", msg)
	return strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
}

// twoCommitRepo 는 커밋 2개짜리 저장소를 만든다. (repo, 첫 sha, 둘째 sha)
func twoCommitRepo(t *testing.T) (string, string, string) {
	t.Helper()
	repo := newRepo(t)
	write(t, repo, "a.txt", "a\n")
	first := commit(t, repo, "첫 커밋")
	write(t, repo, "b.txt", "b\n")
	second := commit(t, repo, "둘째 커밋")
	return repo, first, second
}

func ctxT(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// ─────────────────────────────────────────────────────────────────────────────
// Ancestry — 이 함수를 만든 이유 전부가 여기 있다
// ─────────────────────────────────────────────────────────────────────────────

func TestAncestryYieldsThreeDistinctVerdictsOnARealRepo(t *testing.T) {
	repo, first, second := twoCommitRepo(t)

	// 결과를 읽기 전에 **대조가 성립했는지 먼저 단정한다.**
	// 두 sha 가 같으면(준비가 조용히 실패하면) 아래 셋 중 둘이 무의미해지는데,
	// 그래도 시험은 초록으로 지나간다.
	if first == second {
		t.Fatalf("준비 실패: 커밋 2개를 만들었는데 sha 가 같다(%s)", first)
	}
	if len(first) < 40 || len(second) < 40 {
		t.Fatalf("준비 실패: sha 가 sha 로 안 보인다: %q %q", first, second)
	}

	r := New(repo)
	ctx := ctxT(t)

	got, err := r.Ancestry(ctx, first, second)
	if err != nil || got != judge.AncestryYes {
		t.Errorf("조상인 sha: = %v, %v (want %v)", got, err, judge.AncestryYes)
	}

	got, err = r.Ancestry(ctx, second, first)
	if err != nil || got != judge.AncestryNo {
		t.Errorf("조상이 아닌 sha: = %v, %v (want %v)", got, err, judge.AncestryNo)
	}

	// 셋째가 이 함수의 존재 이유다. 128 을 1 로 접으면 여기가 AncestryNo 로 나오고,
	// 그러면 오타 난 선행을 가진 항목이 "기다리면 풀린다"는 얼굴로 영원히 굶는다.
	got, err = r.Ancestry(ctx, "no-such-ref-0000", second)
	if err != nil {
		t.Fatalf("존재하지 않는 ref 를 오류로 냈다(판정값이어야 한다): %v", err)
	}
	if got != judge.AncestryBadRef {
		t.Errorf("존재하지 않는 ref: = %v (want %v) — 128 을 1 로 접었다면 %v 가 나온다",
			got, judge.AncestryBadRef, judge.AncestryNo)
	}
}

func TestAncestryOnNonRepoIsAnErrorNotOneOfTheThree(t *testing.T) {
	// 넷째 경우(저장소가 아니다)를 셋 중 하나로 접지 않는다.
	// 접으면 설정 사고가 큐의 정상적인 미충족으로 위장한다.
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("임시 경로 해석 실패: %v", err)
	}
	// 대조 전제: 이 경로는 정말로 저장소가 아니어야 한다.
	if _, err := os.Stat(filepath.Join(base, ".git")); !os.IsNotExist(err) {
		t.Fatalf("준비 실패: 저장소가 아닌 경로를 기대했다: %v", err)
	}

	got, err := New(base).Ancestry(ctxT(t), "HEAD", "HEAD")
	if err == nil {
		t.Fatalf("저장소가 아닌 경로인데 오류가 없다(판정값 %v)", got)
	}
	for _, verdict := range []judge.AncestryResult{judge.AncestryYes, judge.AncestryNo, judge.AncestryBadRef} {
		if got == verdict {
			t.Errorf("실패가 판정값 %v 로 접혔다", verdict)
		}
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("오류가 git 의 stderr 를 안 날랐다: %v", err)
	}
}

func TestAncestryWhenGitCannotBeLaunched(t *testing.T) {
	// 종료코드 규약(0/1/128) 밖의 실패 — 프로세스가 아예 안 떴다.
	repo, first, second := twoCommitRepo(t)
	r := New(repo, WithGitBinary(filepath.Join(t.TempDir(), "없는-git")))

	got, err := r.Ancestry(ctxT(t), first, second)
	if err == nil {
		t.Fatalf("git 을 못 띄웠는데 오류가 없다(판정값 %v)", got)
	}
	if got == judge.AncestryYes || got == judge.AncestryNo || got == judge.AncestryBadRef {
		t.Errorf("실행 실패가 판정값 %v 로 접혔다", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 실패 경로 — stderr 와 로그
// ─────────────────────────────────────────────────────────────────────────────

func TestErrorCarriesGitStderr(t *testing.T) {
	repo, _, _ := twoCommitRepo(t)
	_, err := New(repo).Ref(ctxT(t), "no-such-branch")
	if err == nil {
		t.Fatal("없는 ref 인데 오류가 없다")
	}
	// 소비자의 좌표계 = 사람이 읽는 오류 문자열. "status 128" 만 남으면
	// 무엇이 틀렸는지 영영 모른다.
	msg := err.Error()
	if !strings.Contains(msg, "no-such-branch") {
		t.Errorf("오류에 대상 ref 가 없다: %s", msg)
	}
	if !strings.Contains(msg, "bad revision") {
		t.Errorf("오류에 git 의 stderr 가 없다: %s", msg)
	}
	if !strings.Contains(msg, "128") {
		t.Errorf("오류에 종료코드가 없다: %s", msg)
	}

	// 종료코드는 기계 판정 축이기도 하다 — 꺼낼 수 있어야 한다.
	var ce *CommandError
	if !errors.As(err, &ce) {
		t.Fatalf("오류 체인에 *CommandError 가 없다: %v", err)
	}
	if ce.ExitCode != 128 {
		t.Errorf("ExitCode = %d, want 128", ce.ExitCode)
	}
}

func TestFailureIsLoggedWithCauseAndCoordinate(t *testing.T) {
	// 로그 한 줄은 자급자족해야 한다. 시험 하네스의 텍스트 핸들러로 보면
	// 필드 유무 축을 원리적으로 못 보므로 **실물 JSON 한 줄**을 단정한다.
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	repo, _, _ := twoCommitRepo(t)
	if _, err := New(repo, WithLogger(logger)).Ref(ctxT(t), "no-such-branch"); err == nil {
		t.Fatal("없는 ref 인데 오류가 없다")
	}

	var line map[string]any
	found := false
	for _, raw := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if raw == "" {
			continue
		}
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("로그가 JSON 한 줄이 아니다: %q (%v)", raw, err)
		}
		if line["level"] == "ERROR" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("실패했는데 ERROR 줄이 없다: %q", buf.String())
	}
	for _, field := range []string{"component", "repo", "ref", "error"} {
		if _, ok := line[field]; !ok {
			t.Errorf("ERROR 줄에 %q 필드가 없다: %v", field, line)
		}
	}
	if e, _ := line["error"].(string); !strings.Contains(e, "bad revision") {
		t.Errorf("로그의 error 가 원인 전문이 아니다: %q", e)
	}
}

func TestTimeoutSurfacesAsCause(t *testing.T) {
	repo, _, _ := twoCommitRepo(t)
	// 1나노초. "느린 저장소"와 "고장난 저장소"가 로그에서 갈려야 한다.
	_, err := New(repo, WithTimeout(time.Nanosecond)).Refs(ctxT(t))
	if err == nil {
		t.Fatal("타임아웃이 안 났다")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("타임아웃 원인이 체인에 없다: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Refs
// ─────────────────────────────────────────────────────────────────────────────

func TestRefsListsLocalBranchesAndHead(t *testing.T) {
	repo, _, second := twoCommitRepo(t)
	runGit(t, repo, "branch", "feat/x")
	// 원격 추적 ref 를 하나 심는다 — Tier A 는 로컬만 보므로 이것이 목록에 오면 안 된다.
	runGit(t, repo, "update-ref", "refs/remotes/origin/main", second)

	before := time.Now().UTC().Add(-time.Second)
	refs, err := New(repo).Refs(ctxT(t))
	if err != nil {
		t.Fatalf("Refs 실패: %v", err)
	}

	byName := map[string]string{}
	for _, r := range refs {
		byName[r.Ref] = r.SHA
	}
	if byName["main"] != second {
		t.Errorf("main = %q, want %q", byName["main"], second)
	}
	if byName["feat/x"] != second {
		t.Errorf("feat/x = %q, want %q", byName["feat/x"], second)
	}
	if byName["HEAD"] != second {
		t.Errorf("HEAD 가 목록에 없다: %v", byName)
	}
	if _, ok := byName["origin/main"]; ok {
		t.Errorf("원격 추적 ref 가 섞였다: %v", byName)
	}
	if len(refs) != 3 {
		t.Errorf("3건(main, feat/x, HEAD)을 기대했는데 %d건: %v", len(refs), byName)
	}

	for _, r := range refs {
		if r.Subject != "둘째 커밋" {
			t.Errorf("%s 의 제목 = %q", r.Ref, r.Subject)
		}
		// At 은 관측 시각이다 — 화면이 "(파생: git@…, 12초 전)" 을 이 값으로 찍는다.
		if r.At.Before(before) || r.At.After(time.Now().UTC().Add(time.Second)) {
			t.Errorf("%s 의 At 이 관측 시각이 아니다: %v", r.Ref, r.At)
		}
	}
}

func TestRefsOnRepoWithNoCommits(t *testing.T) {
	// 프로젝트를 갓 등록한 순간이다. 여기서 오류를 내면 보드가 통째로 죽는다.
	repo := newRepo(t)
	// 대조 전제: 정말로 커밋이 0건이어야 한다.
	if out := strings.TrimSpace(runGit(t, repo, "for-each-ref")); out != "" {
		t.Fatalf("준비 실패: ref 가 이미 있다: %q", out)
	}

	refs, err := New(repo).Refs(ctxT(t))
	if err != nil {
		t.Fatalf("커밋 없는 저장소에서 오류가 났다: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("ref 0건을 기대했는데 %v", refs)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ChangedPaths
// ─────────────────────────────────────────────────────────────────────────────

func TestChangedPathsHandlesSpacesAndUnicodeAndNewlines(t *testing.T) {
	repo := newRepo(t)
	write(t, repo, "keep.txt", "keep\n")
	base := commit(t, repo, "기준")

	want := []string{"with space.txt", "문서/설계 초안.md", "quote\"name.txt"}
	for _, name := range want {
		write(t, repo, name, "x\n")
	}
	// 개행이 든 파일 이름 — -z 를 쓰는 이유 자체다. 파일시스템이 거부하면 그것만 뺀다.
	newlineName := "new\nline.txt"
	if err := os.WriteFile(filepath.Join(repo, newlineName), []byte("x\n"), 0o644); err == nil {
		want = append(want, newlineName)
	} else {
		t.Logf("개행 든 파일 이름을 못 만들었다(이 축만 건너뛴다): %v", err)
	}
	head := commit(t, repo, "이상한 이름들")

	if base == head {
		t.Fatal("준비 실패: 커밋이 안 생겼다")
	}

	got, err := New(repo).ChangedPaths(ctxT(t), base, head)
	if err != nil {
		t.Fatalf("ChangedPaths 실패: %v", err)
	}
	set := map[string]bool{}
	for _, p := range got {
		set[p] = true
	}
	for _, w := range want {
		if !set[w] {
			t.Errorf("경로 %q 가 결과에 없다: %q", w, got)
		}
	}
	if set["keep.txt"] {
		t.Errorf("안 바뀐 파일이 결과에 있다: %q", got)
	}
	if len(got) != len(want) {
		t.Errorf("%d건을 기대했는데 %d건: %q", len(want), len(got), got)
	}
}

func TestChangedPathsIsTwoDotDiff(t *testing.T) {
	// 두 커밋의 **직접 비교**다. 세 점(갈래 지점 기준)으로 바꾸면
	// "두 커밋 사이"라는 change_set 의 뜻이 조용히 달라진다.
	repo := newRepo(t)
	write(t, repo, "base.txt", "1\n")
	base := commit(t, repo, "기준")

	runGit(t, repo, "checkout", "-q", "-b", "feat")
	write(t, repo, "feat.txt", "1\n")
	head := commit(t, repo, "가지 작업")

	runGit(t, repo, "checkout", "-q", "main")
	write(t, repo, "main-only.txt", "1\n")
	mainTip := commit(t, repo, "본류 작업")

	got, err := New(repo).ChangedPaths(ctxT(t), mainTip, head)
	if err != nil {
		t.Fatalf("ChangedPaths 실패: %v", err)
	}
	set := map[string]bool{}
	for _, p := range got {
		set[p] = true
	}
	// 두 점이면 main 쪽 파일이 "지워진 것"으로 함께 나온다. 세 점이면 안 나온다.
	if !set["main-only.txt"] {
		t.Errorf("두 점 diff 를 기대했는데 main 쪽 변경이 없다: %q", got)
	}
	if !set["feat.txt"] {
		t.Errorf("가지 쪽 변경이 없다: %q", got)
	}
	if set["base.txt"] {
		t.Errorf("공통 조상의 파일이 섞였다: %q", got)
	}
	_ = base
}

func TestChangedPathsKeepsBothSidesOfARename(t *testing.T) {
	// git 의 이름 변경 탐지가 켜져 있으면 `--name-only` 가 **목적지 경로만** 찍는다.
	// 그러면 옮겨진 파일의 옛 경로가 변경집합에서 사라지고, 그 경로를 만지는 세션이
	// 겹침 축에 안 걸린다 — 침묵하는 손실이라 화면 어디에도 안 나온다.
	repo := newRepo(t)
	write(t, repo, "tools/old-name.sh", "#!/bin/sh\necho 같은 내용\n")
	base := commit(t, repo, "기준")

	runGit(t, repo, "mv", "tools/old-name.sh", "tools/new-name.sh")
	head := commit(t, repo, "이름만 바꿨다")

	// 대조 전제: git 이 이것을 정말 "이름 변경"으로 접는가.
	// 안 접히면 이 시험은 아무것도 안 지키면서 초록이 된다.
	raw := runGit(t, repo, "diff", "--name-status", base, head)
	if !strings.HasPrefix(strings.TrimSpace(raw), "R") {
		t.Fatalf("준비 실패: git 이 이름 변경으로 안 접었다: %q", raw)
	}

	got, err := New(repo).ChangedPaths(ctxT(t), base, head)
	if err != nil {
		t.Fatalf("ChangedPaths 실패: %v", err)
	}
	set := map[string]bool{}
	for _, p := range got {
		set[p] = true
	}
	if !set["tools/old-name.sh"] {
		t.Errorf("이름 변경의 **원본** 경로가 빠졌다: %q", got)
	}
	if !set["tools/new-name.sh"] {
		t.Errorf("이름 변경의 목적지 경로가 빠졌다: %q", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Worktrees
// ─────────────────────────────────────────────────────────────────────────────

func TestWorktreesReportsAllIncludingLockedAndPrunable(t *testing.T) {
	repo, _, _ := twoCommitRepo(t)
	base := filepath.Dir(repo)

	lockedPath := filepath.Join(base, "wt-locked")
	gonePath := filepath.Join(base, "wt-gone")
	detachedPath := filepath.Join(base, "wt-detached")

	runGit(t, repo, "worktree", "add", "-q", lockedPath, "-b", "feat")
	runGit(t, repo, "worktree", "add", "-q", gonePath, "-b", "gone")
	runGit(t, repo, "worktree", "add", "-q", "--detach", detachedPath)
	runGit(t, repo, "worktree", "lock", "--reason", "실험 중", lockedPath)
	// 디렉토리를 지우면 그 워크트리가 prunable 이 된다 — 규율상 실제로 자주 나는 상태다.
	if err := os.RemoveAll(gonePath); err != nil {
		t.Fatalf("준비 실패: %v", err)
	}

	got, err := New(repo).Worktrees(ctxT(t))
	if err != nil {
		t.Fatalf("Worktrees 실패: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("4건(주 워크트리 포함)을 기대했는데 %d건: %+v", len(got), got)
	}

	byPath := map[string]Worktree{}
	for _, w := range got {
		byPath[w.Path] = w
	}
	main, ok := byPath[repo]
	if !ok {
		t.Fatalf("주 워크트리(%s)가 목록에 없다: %+v", repo, got)
	}
	if main.ShortBranch() != "main" || main.Branch != "refs/heads/main" {
		t.Errorf("주 워크트리의 브랜치가 틀렸다: %+v", main)
	}
	if main.Locked || main.Prunable || main.Detached {
		t.Errorf("주 워크트리에 상태 플래그가 붙었다: %+v", main)
	}
	if main.HEAD == "" {
		t.Errorf("주 워크트리의 HEAD 가 비었다: %+v", main)
	}

	locked := byPath[lockedPath]
	if !locked.Locked {
		t.Errorf("잠긴 워크트리를 못 읽었다: %+v", locked)
	}
	// 사유가 없으면 그 워크트리를 어떻게 해야 하는지 아무도 모른다.
	// -z 로 읽으므로 C-quote 되지 않고 한글 그대로 와야 한다.
	if locked.LockReason != "실험 중" {
		t.Errorf("잠금 사유 = %q, want %q", locked.LockReason, "실험 중")
	}
	if locked.ShortBranch() != "feat" {
		t.Errorf("잠긴 워크트리의 브랜치 = %q", locked.ShortBranch())
	}

	gone := byPath[gonePath]
	if !gone.Prunable {
		t.Errorf("prunable 워크트리를 못 읽었다: %+v", gone)
	}
	if gone.PrunableReason == "" {
		t.Errorf("prunable 사유가 비었다: %+v", gone)
	}

	det := byPath[detachedPath]
	if !det.Detached {
		t.Errorf("detached 워크트리를 못 읽었다: %+v", det)
	}
	if det.Branch != "" {
		t.Errorf("detached 인데 브랜치가 있다: %+v", det)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// UncommittedPaths — 커밋 전 발자국의 유일한 원천
// ─────────────────────────────────────────────────────────────────────────────

func TestUncommittedPathsCoversStagedUnstagedAndUntracked(t *testing.T) {
	repo := newRepo(t)
	write(t, repo, "tracked.txt", "1\n")
	write(t, repo, "to-rename.txt", "1\n")
	commit(t, repo, "기준")

	write(t, repo, "tracked.txt", "2\n")                  // 미스테이징 수정
	write(t, repo, "staged.txt", "1\n")                   // 스테이징 추가
	runGit(t, repo, "add", "staged.txt")                  //
	write(t, repo, "untracked.txt", "1\n")                // 미추적 파일
	write(t, repo, "새 폴더/공백 든 이름.txt", "1\n")             // 미추적 디렉토리(유니코드·공백)
	runGit(t, repo, "mv", "to-rename.txt", "renamed.txt") // 이름 변경

	// 대조 전제: 준비가 실제로 더러운 상태를 만들었는가.
	if raw := runGit(t, repo, "status", "--porcelain"); strings.TrimSpace(raw) == "" {
		t.Fatal("준비 실패: 작업 트리가 깨끗하다")
	}

	got, err := New(repo).UncommittedPaths(ctxT(t), "")
	if err != nil {
		t.Fatalf("UncommittedPaths 실패: %v", err)
	}
	set := map[string]bool{}
	for _, p := range got {
		set[p] = true
	}
	for _, want := range []string{
		"tracked.txt",
		"staged.txt",
		"untracked.txt",
		"renamed.txt",
		"to-rename.txt", // 이름 변경의 **원본**도 발자국이다
		"새 폴더/",         // 미추적 디렉토리는 git 이 한 줄로 접어 준다
	} {
		if !set[want] {
			t.Errorf("%q 가 결과에 없다: %q", want, got)
		}
	}
	if len(got) != 6 {
		t.Errorf("6건을 기대했는데 %d건: %q", len(got), got)
	}
}

func TestUncommittedPathsReadsTheGivenWorktreeNotTheMainOne(t *testing.T) {
	// 인자가 무시되고 주 워크트리를 읽으면 **남의 발자국을 자기 것으로 보고**하게 된다.
	repo, _, _ := twoCommitRepo(t)
	wt := filepath.Join(filepath.Dir(repo), "wt")
	runGit(t, repo, "worktree", "add", "-q", wt, "-b", "feat")

	write(t, repo, "only-in-main.txt", "1\n")
	write(t, wt, "only-in-wt.txt", "1\n")

	got, err := New(repo).UncommittedPaths(ctxT(t), wt)
	if err != nil {
		t.Fatalf("UncommittedPaths 실패: %v", err)
	}
	if len(got) != 1 || got[0] != "only-in-wt.txt" {
		t.Errorf("= %q, want [only-in-wt.txt] — 주 워크트리를 읽었다면 only-in-main.txt 가 나온다", got)
	}
}

func TestUncommittedPathsOnCleanWorktree(t *testing.T) {
	repo, _, _ := twoCommitRepo(t)
	got, err := New(repo).UncommittedPaths(ctxT(t), "")
	if err != nil {
		t.Fatalf("UncommittedPaths 실패: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("깨끗한 트리인데 %q", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// AheadBehind
// ─────────────────────────────────────────────────────────────────────────────

func TestAheadBehindDirection(t *testing.T) {
	repo := newRepo(t)
	write(t, repo, "a.txt", "1\n")
	commit(t, repo, "기준")

	runGit(t, repo, "checkout", "-q", "-b", "feat")
	for _, n := range []string{"f1.txt", "f2.txt"} {
		write(t, repo, n, "1\n")
		commit(t, repo, "가지 "+n)
	}
	runGit(t, repo, "checkout", "-q", "main")
	write(t, repo, "m1.txt", "1\n")
	commit(t, repo, "본류 m1")

	// 대조 전제: ahead 와 behind 가 **서로 달라야** 방향을 판별할 수 있다.
	// 둘 다 같은 값이면 인자를 뒤집어도 시험이 초록으로 지나간다.
	r := New(repo)
	ahead, behind, err := r.AheadBehind(ctxT(t), "feat", "main")
	if err != nil {
		t.Fatalf("AheadBehind 실패: %v", err)
	}
	if ahead == behind {
		t.Fatalf("준비 실패: ahead(%d)와 behind(%d)가 같아 방향을 판별할 수 없다", ahead, behind)
	}
	if ahead != 2 || behind != 1 {
		t.Errorf("AheadBehind(feat, main) = (%d, %d), want (2, 1)", ahead, behind)
	}

	// 뒤집으면 값도 뒤집혀야 한다. 비대칭이 아니면 인자 순서를 아무도 안 지켜도 된다는 뜻이다.
	ahead, behind, err = r.AheadBehind(ctxT(t), "main", "feat")
	if err != nil {
		t.Fatalf("AheadBehind 실패: %v", err)
	}
	if ahead != 1 || behind != 2 {
		t.Errorf("AheadBehind(main, feat) = (%d, %d), want (1, 2)", ahead, behind)
	}

	// 같은 ref 는 (0, 0) 이다 — 이것만으로는 방향을 못 보므로 위 단정이 먼저 있어야 한다.
	ahead, behind, err = r.AheadBehind(ctxT(t), "main", "main")
	if err != nil || ahead != 0 || behind != 0 {
		t.Errorf("AheadBehind(main, main) = (%d, %d), %v", ahead, behind, err)
	}
}

func TestAheadBehindOnUnknownRefIsAnError(t *testing.T) {
	repo, _, _ := twoCommitRepo(t)
	_, _, err := New(repo).AheadBehind(ctxT(t), "no-such-branch", "main")
	if err == nil {
		t.Fatal("없는 ref 인데 (0,0) 을 조용히 냈다")
	}
	if !strings.Contains(err.Error(), "no-such-branch") {
		t.Errorf("오류에 대상 ref 가 없다: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 패키지 전체의 불변식
// ─────────────────────────────────────────────────────────────────────────────

func TestReaderNeverMutatesTheRepository(t *testing.T) {
	// 이 패키지는 읽기 전용이다. 한 번이라도 쓰면 세션의 작업 트리가 조용히 달라진다.
	repo, first, second := twoCommitRepo(t)
	runGit(t, repo, "branch", "feat")
	write(t, repo, "dirty.txt", "1\n")
	wt := filepath.Join(filepath.Dir(repo), "wt")
	runGit(t, repo, "worktree", "add", "-q", wt, "-b", "wtbranch")

	snapshot := func() string {
		return strings.Join([]string{
			runGit(t, repo, "for-each-ref"),
			runGit(t, repo, "status", "--porcelain"),
			runGit(t, repo, "worktree", "list", "--porcelain"),
			runGit(t, repo, "rev-parse", "HEAD"),
			runGit(t, repo, "stash", "list"),
		}, "\n")
	}
	before := snapshot()

	r := New(repo)
	ctx := ctxT(t)
	if _, err := r.Refs(ctx); err != nil {
		t.Fatalf("Refs: %v", err)
	}
	if _, err := r.Ref(ctx, "main"); err != nil {
		t.Fatalf("Ref: %v", err)
	}
	if _, err := r.ChangedPaths(ctx, first, second); err != nil {
		t.Fatalf("ChangedPaths: %v", err)
	}
	if _, err := r.Ancestry(ctx, first, second); err != nil {
		t.Fatalf("Ancestry: %v", err)
	}
	if _, err := r.Worktrees(ctx); err != nil {
		t.Fatalf("Worktrees: %v", err)
	}
	if _, _, err := r.AheadBehind(ctx, "feat", "main"); err != nil {
		t.Fatalf("AheadBehind: %v", err)
	}
	if _, err := r.UncommittedPaths(ctx, ""); err != nil {
		t.Fatalf("UncommittedPaths: %v", err)
	}
	if _, err := r.UncommittedPaths(ctx, wt); err != nil {
		t.Fatalf("UncommittedPaths(wt): %v", err)
	}

	if after := snapshot(); after != before {
		t.Errorf("저장소 상태가 달라졌다.\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
}

func TestReaderIgnoresInheritedGitEnvironment(t *testing.T) {
	// 훅에서 뜬 프로세스는 GIT_DIR·GIT_WORK_TREE 가 이미 걸려 있다.
	// 그대로 물려받으면 -C 를 줘도 **엉뚱한 저장소를 읽고**, 그 사실이 출력 어디에도 안 나온다.
	repo, _, second := twoCommitRepo(t)
	other := newRepo(t)
	write(t, other, "other.txt", "1\n")
	otherHead := commit(t, other, "남의 저장소")

	// 대조 전제: 두 저장소가 정말 다른 곳을 가리키는가.
	if otherHead == second {
		t.Fatal("준비 실패: 두 저장소의 HEAD 가 같다")
	}

	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(other, ".git", "index"))

	got, err := New(repo).Ref(ctxT(t), "HEAD")
	if err != nil {
		t.Fatalf("Ref 실패: %v", err)
	}
	if got.SHA != second {
		t.Errorf("HEAD sha = %s, want %s — 부모 환경의 GIT_DIR(%s)을 읽었다", got.SHA, second, otherHead)
	}
}

// TestMergeBaseGivesTheForkPointNotEitherTip 는 갈래 지점이 두 tip 중 어느 것도 아님을
// 단정하고, 그 sha 를 base 로 넘긴 ChangedPaths 가 갈래 기준 diff 와 같아짐을 확인한다.
//
// ★ 이 짝이 board 의 계약이다. ChangedPaths 는 두 점으로 남고(TestChangedPathsIsTwoDotDiff),
// 갈래 기준이 필요한 호출자가 MergeBase 로 base 를 만들어 넘긴다. 그래야 change_set 이
// 보관하는 `(base_sha, head_sha)` 가 실제로 diff 를 뜬 두 커밋과 일치한다.
func TestMergeBaseGivesTheForkPointNotEitherTip(t *testing.T) {
	repo := newRepo(t)
	write(t, repo, "base.txt", "1\n")
	fork := commit(t, repo, "기준")

	runGit(t, repo, "checkout", "-q", "-b", "feat")
	write(t, repo, "feat.txt", "1\n")
	head := commit(t, repo, "가지 작업")

	runGit(t, repo, "checkout", "-q", "main")
	write(t, repo, "main-only.txt", "1\n")
	mainTip := commit(t, repo, "본류 작업")

	r := New(repo)
	got, err := r.MergeBase(ctxT(t), "main", "feat")
	if err != nil {
		t.Fatalf("MergeBase 실패: %v", err)
	}
	if got != fork {
		t.Fatalf("갈래 지점이 %s 다, 기대 %s", got, fork)
	}
	if got == mainTip || got == head {
		t.Fatalf("갈래 지점이 어느 한쪽 tip 과 같다: %s", got)
	}

	// 그 sha 를 base 로 넘기면 남의 변경이 빠진다 — board 가 쓰는 조합 그대로.
	paths, err := r.ChangedPaths(ctxT(t), got, "feat")
	if err != nil {
		t.Fatalf("ChangedPaths 실패: %v", err)
	}
	set := map[string]bool{}
	for _, p := range paths {
		set[p] = true
	}
	if !set["feat.txt"] {
		t.Errorf("가지 쪽 변경이 없다: %q", paths)
	}
	if set["main-only.txt"] {
		t.Errorf("갈래 지점을 base 로 줬는데 main 쪽 변경이 섞였다: %q", paths)
	}
}

// TestMergeBaseIsAnErrorWhenHistoriesAreUnrelated 는 공통 조상이 없을 때
// **빈 문자열을 조용히 돌려주지 않는다**를 단정한다.
//
// 빈 base 를 그대로 ChangedPaths 에 넘기면 git 이 전혀 다른 것을 비교하거나
// 호출자가 두 점으로 물러서게 된다. 둘 다 침묵하는 오탐이다.
func TestMergeBaseIsAnErrorWhenHistoriesAreUnrelated(t *testing.T) {
	repo := newRepo(t)
	write(t, repo, "a.txt", "a\n")
	commit(t, repo, "본류")

	// 부모 없는 갈래 — 공통 조상이 없다.
	runGit(t, repo, "checkout", "-q", "--orphan", "alien")
	runGit(t, repo, "rm", "-rq", "--cached", ".")
	write(t, repo, "z.txt", "z\n")
	commit(t, repo, "관계 없는 이력")

	got, err := New(repo).MergeBase(ctxT(t), "main", "alien")
	if err == nil {
		t.Fatalf("공통 조상이 없는데 오류가 아니다: %q", got)
	}
	if got != "" {
		t.Fatalf("실패인데 sha 를 냈다: %q", got)
	}
}
