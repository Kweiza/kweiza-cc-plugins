package main

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kweiza/flightdeck/internal/mcpsrv"
)

// CLI 와 MCP 가 **같은 프로젝트**를 봐야 한다.
//
// ★ 앞선 판은 갈렸다. mcpsrv 가 경로의 마지막 성분을 프로젝트로 삼아,
// `.claude/worktrees/track2` 에서 띄운 세션이 프로젝트를 `track2` 로 봤다 —
// **워크트리마다 유령 프로젝트가 생긴다.** 워크트리로 일하는 것이 이 제품의 핵심 흐름이라
// 그 자리에서 바로 깨지는 규칙이었다. 실물로 재현했다(CLI=kweiza-cc-plugins, MCP=wt-probe).
//
// 옳은 규칙은 git 주 저장소이고, 그것은 진입점이 푼다. 이 시험은 **둘이 같은 답을 내는지**를 본다.
func TestCLIAndMCPAgreeOnProjectInsideAWorktree(t *testing.T) {
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
	wt := filepath.Join(root, "detached-name")
	git(main, "worktree", "add", "-q", wt, "-b", "feat")

	env := func(k string) (string, bool) {
		switch k {
		case "CLAUDE_PROJECT_DIR":
			return wt, true
		case "CLAUDE_CODE_SESSION_ID":
			return "cc-1", true
		}
		return "", false
	}

	// ── 대조 전제: 워크트리 이름이 프로젝트 이름과 **달라야** 이 시험이 무언가를 본다.
	if filepath.Base(wt) == filepath.Base(main) {
		t.Fatal("전제가 깨졌다 — 워크트리 이름이 주 저장소와 같아 갈림을 볼 수 없다")
	}

	cli := resolveProject(env, wt)
	if cli.ID != "myproject" {
		t.Fatalf("CLI 가 주 저장소를 못 찾았다: %q", cli.ID)
	}

	// MCP 쪽: 주입 없이 스스로 풀면 워크트리 이름이 나온다(옛 결함).
	bare := mcpsrv.ResolveIdentity(env, wt, nil, "h", nil)
	if bare.ProjectID != filepath.Base(wt) {
		t.Logf("참고: 주입 없는 해석 = %q", bare.ProjectID)
	}

	// 주입하면 둘이 같아야 한다 — 이것이 이 시험의 본 단정이다.
	srv := mcpsrv.New(nil, nil,
		mcpsrv.WithEnv(env), mcpsrv.WithCwd(wt, nil),
		mcpsrv.WithProject(cli.ID, cli.Path))
	if got := srv.Identity().ProjectID; got != cli.ID {
		t.Errorf("MCP=%q CLI=%q — 같은 워크트리에서 서로 다른 프로젝트를 본다", got, cli.ID)
	}
	// 워크트리 관측은 여전히 MCP 몫이다 — 프로젝트만 주입했지 트리까지 갈아치우지 않는다.
	if got := srv.Identity().Worktree; got != wt {
		t.Errorf("워크트리 관측이 바뀌었다: %q (기대 %q)", got, wt)
	}
}
