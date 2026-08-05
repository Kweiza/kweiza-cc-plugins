package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 훅과 MCP 는 **저장소 하위 디렉토리에서 열어도** 같은 워크트리를 답해야 한다.
//
// ★ 앞선 판은 갈렸다. 세션 정체 3중키 (machine_id, worktree, cc_session_id) 의 **둘째** 축을
// 두 계층이 다른 규칙으로 만들었다 —
//
//	· 훅 : 53c18ba 이후 `git rev-parse --show-toplevel` → 트리 루트
//	· MCP: 여전히 자기 cwd
//
// 그래서 Claude Code 를 `<repo>/plugins/flightdeck/server` 같은 하위 디렉토리에서 열면
// 한 창이 카드 두 장으로 열린다. 실물 원장에서 확인됐다(2026-08-04 실측):
// 같은 cc·같은 머신인데 상하위 경로로 갈린 카드쌍 **60건**, session.worktree 에
// `…/kweiza-cc-plugins`(19장)와 `…/kweiza-cc-plugins/plugins/flightdeck/server`(8장)가 나란히 있었다.
//
// 머신 축이 먼저 같은 사고를 겪고 주입으로 고쳤다(TestOneClaudeSessionIsOneRowAcrossChannels).
// 이 시험은 그 교정에서 빠져 있던 **워크트리 축**을 같은 방식으로 붙든다.
//
// ★ 가드(sameWorktree)를 느슨하게 만드는 쪽으로는 안 고친다 — DESIGN §3 이 일부러 없앤
// 접두 일치가 되살아난다. 두 계층이 같은 질문에 같은 답을 하게 만드는 것이 이 시험의 단정이다.
func TestHookAndMCPAgreeOnWorktreeFromSubdir(t *testing.T) {
	h := newHarness(t)
	const cc = "cc-worktree-axis-1"

	// 진짜 git 저장소여야 한다 — --show-toplevel 이 답을 안 하면 이 시험은 아무것도 안 본다.
	root := filepath.Join(filepath.Dir(h.state), h.project)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("저장소 디렉토리 생성 실패: %v", err)
	}
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
	git(root, "init", "-q")
	git(root, "commit", "-q", "--allow-empty", "-m", "init")

	// 창이 열린 자리는 **하위 디렉토리**다. 이 제품에서 흔한 자리이고(서버 모듈 루트),
	// 실측에서 카드가 갈린 자리도 정확히 이 모양이다.
	sub := filepath.Join(root, "plugins", "flightdeck", "server")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("하위 디렉토리 생성 실패: %v", err)
	}

	// ★ FD_WORKTREE 를 **안 넣는다.** 넣으면 cwd 축이 우회돼 이 시험이 재려는 갈림이 사라진다.
	env := h.unpinnedEnv(map[string]string{
		"CLAUDE_CODE_SESSION_ID": cc,
		"CLAUDE_PROJECT_DIR":     root,
		"CLAUDE_PLUGIN_DATA":     t.TempDir(),
	})

	// ── 대조 전제 ①: 훅이 정말 하위 디렉토리를 루트로 접는가.
	// git 이 없거나 --show-toplevel 이 실패하면 여기서 멈춘다 — 그 상태로 아래를 돌리면
	// 두 계층이 우연히 같은 값(둘 다 cwd)이 되어 **거짓 초록**이 난다.
	hookApp := newApp(envOf(env), quietLogger(), sub, strings.NewReader(""))
	if hookApp.proj.Worktree != root {
		t.Fatalf("대조 전제가 깨졌다 — 훅이 하위 디렉토리를 루트로 안 접었다: %q (기대 %q · %s)",
			hookApp.proj.Worktree, root, hookApp.proj.Detail)
	}
	if root == sub {
		t.Fatal("대조 전제가 깨졌다 — 루트와 하위 디렉토리가 같아 갈림을 볼 수 없다")
	}

	// ── 채널 ① 훅. 페이로드의 cwd 도 하위 디렉토리다(운영 조건).
	payload := `{"session_id":"` + cc + `","cwd":"` + sub + `","hook_event_name":"SessionStart"}`
	if code, out := h.runEnv(env, payload, "hook", "session-start"); code != 0 {
		t.Fatalf("훅 채널 실패(%d): %s", code, out)
	}

	// ── 채널 ② MCP. **운영 진입점(runMCP)을 그대로 탄다.**
	// 여기서 mcpsrv.New 를 시험이 직접 조립하면 "mcp.go 가 정말 워크트리를 주입하는가"를
	// 원리적으로 못 본다 — 시험이 만든 배선에 시험이 단정하게 된다.
	// runMCP 는 옵션을 안 받으므로 mcpsrv 가 **프로세스의** cwd 를 읽는다(운영 조건이 그것이다).
	t.Setenv("CLAUDE_CODE_SESSION_ID", cc)
	t.Setenv("CLAUDE_PROJECT_DIR", root)
	t.Chdir(sub)
	mcpApp := newApp(envOf(env), quietLogger(), sub, strings.NewReader(""))
	var out strings.Builder
	if code := runMCP(context.Background(), mcpApp, quietLogger(),
		strings.NewReader(mcpCall("board", map[string]any{})+"\n"), &out); code != 0 {
		t.Fatalf("MCP 채널 실패(종료코드 %d):\n%s", code, out.String())
	}
	// 도달 전제: MCP 가 정말 서버까지 갔는가. 조용히 실패해 카드가 1장이 되는 거짓 초록을 막는다.
	if s := out.String(); !strings.Contains(s, `"result"`) {
		t.Fatalf("MCP 채널이 서버까지 못 갔다:\n%s", s)
	}

	// ── 본 단정: 한 창이면 카드도 한 장이다.
	view, err := h.svc.Board(t.Context(), h.project, service0BoardOptions())
	if err != nil {
		t.Fatalf("보드 조회 실패: %v", err)
	}
	if len(view.Sessions) != 1 {
		trees := make([]string, 0, len(view.Sessions))
		for _, c := range view.Sessions {
			trees = append(trees, c.View.Session.ID+"@"+c.View.Session.Worktree)
		}
		t.Fatalf("한 창인데 보드 카드가 %d 장이다 — %s", len(view.Sessions), strings.Join(trees, " · "))
	}
	if got := view.Sessions[0].View.Session.Worktree; got != root {
		t.Errorf("카드의 워크트리가 트리 루트가 아니다: %q (기대 %q)", got, root)
	}
}
