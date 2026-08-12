package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// git 을 못 읽으면 프로젝트 id 를 **안 짓는다.**
//
// ★ 이 시험이 잠그는 것은 "빈 값이 낫다"가 아니라 **지어낸 좌표가 서버로 나가지 않는다**이다.
// 옛 동작은 `ProjectIDFromPath(cwd)` — 즉 **디렉토리 이름**을 프로젝트 id 로 냈고, 실패 사실은
// Detail 에만 적혔다. 그러면 서버는 그것을 믿고 프로젝트를 자동 등록한다: 워크트리가 막
// 만들어지는 중이거나 지워지는 중이거나, 아예 git 저장소가 아닌 디렉토리(machine-probe 의
// /tmp 경로가 실제로 그랬다)에서 fd 를 부르면 `wt` 같은 유령 프로젝트가 원장에 하나 는다.
//
// 서버 쪽 방어(a168c20)는 **3중키 세션이 이미 있을 때만** 듣는다 — 그 세션의 프로젝트로
// 이어 붙인다. 처음 열리는 세션에는 안 듣는다. 그래서 이 자리에서 막는다.
func TestUnresolvedProjectIsNotInvented(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wt")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	noEnv := func(string) (string, bool) { return "", false }

	// ── 대조 전제: 이 디렉토리는 정말 git 저장소가 아니어야 한다.
	if _, err := gitOut(dir, "rev-parse", "--git-common-dir"); err == nil {
		t.Fatal("전제가 깨졌다 — 이 디렉토리가 git 저장소라 실패 갈래를 볼 수 없다")
	}

	c := resolveProject(noEnv, dir)
	if c.ID != "" {
		t.Fatalf("git 을 못 읽었는데 프로젝트 id 를 지어냈다: %q (옛 결함 그대로다)", c.ID)
	}
	// 침묵하지 않는다 — 왜 없는지가 사람에게 닿아야 고칠 거리가 생긴다.
	if !strings.Contains(c.Detail, "git rev-parse 실패") {
		t.Fatalf("사유가 Detail 에 없다: %q", c.Detail)
	}

	// ★ 워크트리 축은 **그대로 살아 있어야 한다.** 3중키의 둘째 축이라 여기까지 비우면
	//   세션 정체가 통째로 죽는다. 이 항목이 막으려는 것은 프로젝트 id 하나다.
	if c.Worktree != dir {
		t.Fatalf("워크트리 좌표까지 잃었다: %q (프로젝트 축만 비워야 한다)", c.Worktree)
	}
}

// FD_PROJECT 가 명시로 오면 **git 실패와 무관하게 그것을 믿는다.**
//
// 항목 본문이 "정해야 할 것"으로 남긴 축이다. 유지가 답이다 — 명시는 사람이 정한 것이고,
// git 저장소 밖에서 fd 를 쓰는 유일한 탈출구다. 그것마저 막으면 이 고침이
// "지어내지 않는다"를 넘어 "쓸 수 없게 한다"가 된다.
func TestExplicitProjectStillWinsWhenGitIsUnreadable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wt")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	env := func(k string) (string, bool) {
		if k == "FD_PROJECT" {
			return "declared-project", true
		}
		return "", false
	}
	c := resolveProject(env, dir)
	if c.ID != "declared-project" {
		t.Fatalf("명시 지정이 안 이겼다: %q", c.ID)
	}
	if !strings.Contains(c.Detail, "FD_PROJECT") {
		t.Fatalf("무엇이 이겼는지가 Detail 에 없다: %q", c.Detail)
	}
}

// 좌표를 못 푼 사실이 **사람이 보는 자리**에 뜬다.
//
// ★ 이것이 항목 본문의 「지금 거절 문구가 사람에게 고칠 거리를 주는가」에 대한 클라이언트 쪽
// 답이다. 서버의 거절은 CLI 에서만 화면에 뜬다 — 훅은 `log.Warn` 뒤 조용히 넘어가고(훅이
// 세션을 막으면 안 된다), MCP 는 도구를 부를 때까지 아무 말이 없다. 그러면 이 고침이
// **조용한 오등록을 조용한 무등록으로** 바꾸는 데 그친다.
//
// notice 는 그 셋을 한 자리로 모은다: SessionStart 배너(hook.go) · `fd doctor`(cmds.go) ·
// 기동 로그(main.go). 사람이 찾아가지 않아도 보는 유일한 표면이 첫째다.
func TestUnresolvedProjectSurfacesInTheClientNotice(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wt")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	// FD_STATE_DIR·HOME 을 시험 자리로 묶는다 — 안 그러면 newApp 이 개발자의 진짜 홈에
	// machine-id 를 적는다(env.go 의 MachineIDPath 주석과 같은 규율).
	app := newApp(envOf(map[string]string{
		"FD_STATE_DIR": state, "HOME": state,
	}), quietLogger(), dir, strings.NewReader(""))

	if app.proj.ID != "" {
		t.Fatalf("전제가 깨졌다 — 좌표가 풀렸다: %q", app.proj.ID)
	}
	if !strings.Contains(app.notice, "프로젝트 좌표") {
		t.Fatalf("좌표를 못 푼 사실이 notice 에 없다 — 어느 화면에도 안 뜬다:\n%q", app.notice)
	}
	// 고칠 거리를 준다: 원인과 탈출구.
	for _, want := range []string{"git", "FD_PROJECT"} {
		if !strings.Contains(app.notice, want) {
			t.Fatalf("notice 에 %q 가 없어 사람이 고칠 수 없다:\n%q", want, app.notice)
		}
	}
}

// 좌표가 **잘 풀리면** notice 에 이 축의 잡음이 없다.
//
// ★ 위 시험만 있으면 문구를 무조건 붙이는 구현으로도 통과한다. 그러면 모든 정상 세션의
// 배너에 거짓 경고가 한 줄 뜨고, 그 잡음이 진짜 경고를 덮는다.
func TestResolvedProjectAddsNoNoticeNoise(t *testing.T) {
	git := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(c.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	root := t.TempDir()
	main := filepath.Join(root, "myproject")
	git(root, "init", "-q", main)
	git(main, "commit", "-q", "--allow-empty", "-m", "init")

	state := t.TempDir()
	app := newApp(envOf(map[string]string{
		"FD_STATE_DIR": state, "HOME": state,
	}), quietLogger(), main, strings.NewReader(""))

	if app.proj.ID != "myproject" {
		t.Fatalf("전제가 깨졌다 — 좌표가 안 풀렸다: %q", app.proj.ID)
	}
	if strings.Contains(app.notice, "프로젝트 좌표") {
		t.Fatalf("정상 경로인데 경고가 붙었다:\n%q", app.notice)
	}
}

// git 을 **잘** 읽은 경로는 그대로다 — 이 시험이 없으면 위 둘은 `c.ID = ""` 한 줄로도 통과한다.
//
// ★ ProjectIDFromPath 자체는 이 항목의 사정권 밖이다(항목 본문의 ★). 정상 경로의 좌표다.
func TestResolvedProjectStillNamesTheMainRepo(t *testing.T) {
	git := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(c.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	root := t.TempDir()
	main := filepath.Join(root, "myproject")
	git(root, "init", "-q", main)
	git(main, "commit", "-q", "--allow-empty", "-m", "init")

	c := resolveProject(func(string) (string, bool) { return "", false }, main)
	if c.ID != "myproject" {
		t.Fatalf("정상 경로가 깨졌다: %q", c.ID)
	}
}
