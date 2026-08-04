package main

import (
	"os"
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

// TestSubdirectoryOfAWorktreeIsTheSameSessionCoordinate 는 **같은 워크트리의 하위
// 디렉토리에서 훅이 불려도 세션 좌표가 갈리지 않는다**를 단정한다.
//
// ★ 왜. beatFromHook 은 훅 이벤트마다 resolveProject(cwd) 를 다시 풀어 OpenSession 을
// 부르고, 세션 유니크 키가 (machine_id, worktree, cc_session_id) 3중키다. Worktree 에
// 날 cwd 가 들어가면 **대화 하나가 cwd 수만큼 세션 행을 만든다** — 서브에이전트가
// 하위 디렉토리에서 go test 를 돌리는 것만으로 새 정체가 발급된다. 그 결과:
// 처방이 자기 자신과 조율하라 하고(실측 100% 오탐), 보드의 세션 수가 부풀고,
// 선점은 1행에 발자국은 다른 행에 쌓인다. 실측: 원장에 행 50개, 실제 대화 18개.
//
// ★ 그런데 §3 이 없앤 접두 일치를 되살리면 안 된다 — 그것이 겨냥한 사고(조상 트리의
// 등록을 물려받는 것)가 돌아온다. 그래서 접두 일치가 아니라 **git 이 답한 워크트리
// 루트**로 접는다. 아래 두 단정이 그 둘을 동시에 지킨다.
func TestSubdirectoryOfAWorktreeIsTheSameSessionCoordinate(t *testing.T) {
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
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("임시 경로 해석 실패: %v", err)
	}
	main := filepath.Join(root, "myproject")
	git(root, "init", "-q", main)
	git(main, "commit", "-q", "--allow-empty", "-m", "init")
	wt := filepath.Join(root, "detached-name")
	git(main, "worktree", "add", "-q", wt, "-b", "feat")

	sub := filepath.Join(main, "plugins", "flightdeck", "server")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("하위 디렉토리 생성 실패: %v", err)
	}
	wtSub := filepath.Join(wt, "plugins", "flightdeck")
	if err := os.MkdirAll(wtSub, 0o755); err != nil {
		t.Fatalf("하위 디렉토리 생성 실패: %v", err)
	}

	env := func(k string) (string, bool) { return "", false }

	// ① 주 워크트리와 그 하위 디렉토리가 **같은 좌표**여야 한다.
	atRoot := resolveProject(env, main)
	atSub := resolveProject(env, sub)
	if atRoot.Worktree != atSub.Worktree {
		t.Fatalf("같은 워크트리의 하위 디렉토리가 다른 세션 좌표를 냈다:\n"+
			"  루트   %q\n  하위   %q\n"+
			"3중키가 worktree 를 그대로 쓰므로 이대로면 대화 하나가 행 둘이 된다\n"+
			"  (%s / %s)", atRoot.Worktree, atSub.Worktree, atRoot.Detail, atSub.Detail)
	}
	if atRoot.Worktree != main {
		t.Fatalf("워크트리 루트가 %q 다, 기대 %q", atRoot.Worktree, main)
	}

	// ② 링크된 워크트리와 그 하위도 서로 같아야 한다.
	atWT := resolveProject(env, wt)
	atWTSub := resolveProject(env, wtSub)
	if atWT.Worktree != atWTSub.Worktree {
		t.Fatalf("링크된 워크트리의 하위가 갈렸다: %q vs %q", atWT.Worktree, atWTSub.Worktree)
	}

	// ③ ★ 그러나 서로 **다른 워크트리는 여전히 갈려야 한다**. 이것이 §3 이 지키려던 것이고,
	//    여기서 접두 일치로 접으면 링크된 워크트리가 주 저장소에 흡수돼 그 사고가 돌아온다.
	if atRoot.Worktree == atWT.Worktree {
		t.Fatalf("서로 다른 워크트리가 한 좌표로 접혔다(%q) — §3 이 없앤 조상 트리 상속이 돌아왔다",
			atRoot.Worktree)
	}
	// 그리고 프로젝트는 둘 다 주 저장소다(기존 계약).
	if atRoot.ID != "myproject" || atWT.ID != "myproject" {
		t.Fatalf("프로젝트가 갈렸다: %q vs %q", atRoot.ID, atWT.ID)
	}
}
