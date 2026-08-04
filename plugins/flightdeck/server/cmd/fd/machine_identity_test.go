package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/mcpsrv"
)

// 세 채널(훅·CLI·MCP)이 **같은 머신 id** 를 써야 한다.
//
// ★ 앞선 판은 갈렸다. 세션 정체는 3중키 (machine_id, worktree, cc_session_id) 인데
// 그 첫 축을 채널마다 다른 규칙으로 만들었다 —
//
//	· MCP  : hostname 을 그대로 machine id 로 썼다(identity.go).
//	· 훅·CLI: 상태 디렉토리의 machine-id 파일. 그런데 그 디렉토리를 CLAUDE_PLUGIN_DATA·
//	          XDG_STATE_HOME 으로 고르는데, 그 둘은 **채널마다 있고 없다**(훅·MCP 프로세스에는
//	          Claude Code 가 넣어 주고 사용자 셸에는 없다). 그래서 파일이 두 벌이 됐다.
//
// 실물로 재현됐다 — 한 Claude 세션이 보드에 세션 행 3개로 떴고 machine_id 만 셋이었다
// (hostname 하나 · machine-id 파일 두 벌에서 온 값 둘).
//
// 조회 자체는 정상이다(store/session.go 는 3중키로 찾고 없을 때만 INSERT 한다).
// 키를 바꾸지 않는 이유: worktree 축을 키에서 빼면 보드 카드의 브랜치·변경경로가
// 첫 관측으로 동결되고(service/board.go), 조상 트리 등록을 물려받지 않는다는 보증도 사라진다.
// 고칠 것은 **클라이언트가 그 축에 진실을 넣는가**다.
func TestAllChannelsAgreeOnMachineID(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()

	base := func(extra map[string]string) map[string]string {
		e := map[string]string{
			"HOME":                   home,
			"CLAUDE_CODE_SESSION_ID": "cc-machine-1",
			"CLAUDE_PROJECT_DIR":     dir,
			"FD_WORKTREE":            dir,
			"FD_LOG":                 "error",
		}
		for k, v := range extra {
			e[k] = v
		}
		return e
	}
	// 훅·MCP 프로세스에는 Claude Code 가 CLAUDE_PLUGIN_DATA 를 넣어 준다. 사용자 셸에는 없다.
	hookEnv := base(map[string]string{"CLAUDE_PLUGIN_DATA": t.TempDir()})
	cliEnv := base(map[string]string{"XDG_STATE_HOME": t.TempDir()})

	// ── 대조 전제: 두 채널의 상태 디렉토리가 **정말 갈렸는가.**
	// 여기가 같으면 이 시험은 아무것도 안 본다(하네스가 FD_STATE_DIR 를 고정해
	// 이 축이 평가조차 되지 않던 것이 이 결함이 전 시험 초록으로 산 이유다).
	hookSD := ResolveStateDir(envOf(hookEnv), home).Path
	cliSD := ResolveStateDir(envOf(cliEnv), home).Path
	if hookSD == cliSD {
		t.Fatalf("대조 전제가 깨졌다 — 훅과 CLI 의 상태 디렉토리가 %q 로 같아 갈림을 볼 수 없다", hookSD)
	}

	hookApp := newApp(envOf(hookEnv), quietLogger(), dir, strings.NewReader(""))
	cliApp := newApp(envOf(cliEnv), quietLogger(), dir, strings.NewReader(""))

	// ── 본 단정 ①: 상태 디렉토리가 갈려도 머신 id 는 같아야 한다.
	if hookApp.machine == "" || cliApp.machine == "" {
		t.Fatalf("머신 id 가 비었다 — 훅=%q CLI=%q", hookApp.machine, cliApp.machine)
	}
	if hookApp.machine != cliApp.machine {
		t.Errorf("훅과 CLI 가 서로 다른 머신 id 를 쓴다: 훅=%q(%s) CLI=%q(%s)",
			hookApp.machine, hookSD, cliApp.machine, cliSD)
	}

	// ── 본 단정 ②: MCP 도 같은 값을 써야 한다.
	// hostname 을 **일부러 다른 값**으로 준다 — machine 축이 다시 hostname 으로 새면 빨간불이 난다.
	mcpEnv := base(map[string]string{"CLAUDE_PLUGIN_DATA": t.TempDir()})
	mcpApp := newApp(envOf(mcpEnv), quietLogger(), dir, strings.NewReader(""))
	srv := mcpsrv.New(nil, quietLogger(),
		mcpsrv.WithEnv(envOf(mcpEnv)),
		mcpsrv.WithCwd(dir, nil),
		mcpsrv.WithHostname("some-other-host", nil),
		mcpsrv.WithProject(mcpApp.proj.ID, mcpApp.proj.Path),
		mcpsrv.WithMachine(mcpApp.machine),
	)
	if got := srv.Identity().MachineID; got != hookApp.machine {
		t.Errorf("MCP=%q 훅=%q — 같은 머신인데 서로 다른 머신 id 를 본다", got, hookApp.machine)
	}
	// 워크트리 관측은 여전히 MCP 몫이다 — 머신만 주입했지 트리까지 갈아치우지 않는다.
	if got := srv.Identity().Worktree; got != dir {
		t.Errorf("워크트리 관측이 바뀌었다: %q (기대 %q)", got, dir)
	}
}

// fd doctor 는 머신 id 와 **그것을 읽은 자리**를 함께 찍어야 한다.
//
// 값만 찍으면 부족하다. 이 축이 갈렸을 때 사람이 원인에 도달하려면 "어느 파일에서 왔나"가
// 있어야 한다 — 그 줄이 없어서 이번에는 /proc/<pid>/environ 을 뒤져야 원인이 나왔다.
func TestDoctorReportsMachineAxisWithSource(t *testing.T) {
	h := newHarness(t)
	code, out := h.run("", "doctor")
	if code != 0 {
		t.Fatalf("doctor 종료코드 %d:\n%s", code, out)
	}
	want := newApp(envOf(h.env), quietLogger(), h.state, strings.NewReader("")).machine
	if want == "" {
		t.Fatal("대조 전제가 깨졌다 — App 의 머신 id 가 비었다")
	}
	// ★ 값과 자리를 **한 줄로** 단정한다. 따로 찾으면 공허해진다 —
	// "FD_STATE_DIR" 는 바로 위 상태 디렉토리 줄에도 있어서, 머신 줄이 통째로 없어도
	// 통과하는 단정이 된다.
	line := "  머신 " + want + " (FD_STATE_DIR"
	if !strings.Contains(out, line) {
		t.Errorf("doctor 에 머신 축 줄(%q)이 없다:\n%s", line, out)
	}
}

// 한 Claude 세션은 채널이 몇 개든 보드에 **카드 한 장**이어야 한다.
//
// 좌표계가 보드인 이유: 사용자가 보는 것이 그것이고, 저장층을 직접 부르는 단정은
// "채널이 무엇을 보냈나"를 원리적으로 못 본다.
func TestOneClaudeSessionIsOneRowAcrossChannels(t *testing.T) {
	h := newHarness(t)
	const cc = "cc-fanout-uuid-1"

	// ★ 홈은 하네스의 가짜 홈을 쓴다. 여기서 t.TempDir() 로 따로 만들면
	// unpinnedEnv 가 주는 HOME 과 갈려, 아래 ResolveStateDir 대조가 딴 자리를 잰다.
	home := h.home
	// MCP 의 프로젝트 좌표는 CLAUDE_PROJECT_DIR 의 마지막 성분이다(설계 §13).
	// 하네스의 프로젝트와 같은 이름의 디렉토리를 만들어 좌표를 맞춘다.
	dir := filepath.Join(filepath.Dir(h.state), h.project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("프로젝트 디렉토리 생성 실패: %v", err)
	}

	// ★ 상태 디렉토리 축을 푸는 일은 **하네스의 정식 갈래**가 한다(unpinnedEnv).
	// 앞선 판은 여기서 손으로 FD_STATE_DIR 를 지우고 HOME 을 끼웠는데, 그것은 이 시험
	// 하나의 국소 해법이라 다음 시험은 그 짝(HOME)을 잊는다 — 잊으면 시험이 사용자의
	// 진짜 ~/.flightdeck/machine-id 를 읽고 덮어쓴다. 짝을 강제하는 것은
	// TestUnpinnedEnvNeverReachesTheRealHome 이다.
	base := func(extra map[string]string) map[string]string {
		e := map[string]string{
			"CLAUDE_CODE_SESSION_ID": cc,
			"CLAUDE_PROJECT_DIR":     dir,
			"FD_WORKTREE":            dir,
		}
		for k, v := range extra {
			e[k] = v
		}
		return h.unpinnedEnv(e)
	}
	hookEnv := base(map[string]string{"CLAUDE_PLUGIN_DATA": t.TempDir()})
	cliEnv := base(map[string]string{"XDG_STATE_HOME": t.TempDir()})
	mcpEnv := base(map[string]string{"CLAUDE_PLUGIN_DATA": t.TempDir()})

	// ── 대조 전제 ①: 상태 디렉토리가 정말 갈렸는가.
	if a, b := ResolveStateDir(envOf(hookEnv), home).Path, ResolveStateDir(envOf(cliEnv), home).Path; a == b {
		t.Fatalf("대조 전제가 깨졌다 — 훅과 CLI 의 상태 디렉토리가 %q 로 같다", a)
	}
	// ── 대조 전제 ②: **남은 갈림 축이 machine 뿐인가.** worktree·project 가 갈리면
	// 이 시험은 machine 축이 아니라 그쪽을 재게 된다.
	hookCoord := resolveProject(envOf(hookEnv), dir)
	cliCoord := resolveProject(envOf(cliEnv), dir)
	if hookCoord.Worktree != cliCoord.Worktree || hookCoord.ID != cliCoord.ID {
		t.Fatalf("대조 전제가 깨졌다 — 훅과 CLI 의 좌표가 다르다: 훅=%s@%s CLI=%s@%s",
			hookCoord.ID, hookCoord.Worktree, cliCoord.ID, cliCoord.Worktree)
	}
	if hookCoord.ID != h.project {
		t.Fatalf("대조 전제가 깨졌다 — 채널이 본 프로젝트 %q 가 하네스의 %q 와 다르다", hookCoord.ID, h.project)
	}

	// ── 채널 ① 훅
	payload := `{"session_id":"` + cc + `","cwd":"` + dir + `","hook_event_name":"SessionStart"}`
	if code, out := h.runEnv(hookEnv, payload, "hook", "session-start"); code != 0 {
		t.Fatalf("훅 채널 실패(%d): %s", code, out)
	}

	// ── 채널 ② CLI
	if code, out := h.runEnv(cliEnv, "", "status"); code != 0 {
		t.Fatalf("CLI 채널 실패(%d): %s", code, out)
	}

	// ── 채널 ③ MCP. **운영 진입점(runMCP)을 그대로 탄다.**
	// 여기서 mcpsrv.New 를 시험이 직접 조립하면 "mcp.go 가 정말 주입하는가"라는
	// 축을 원리적으로 못 본다 — 시험이 만든 배선에 시험이 단정하게 된다.
	//
	// runMCP 는 옵션을 안 받으므로 mcpsrv 가 **프로세스의** env·cwd 를 읽는다(운영 조건이
	// 그것이다 — MCP stdio 서버의 cwd 가 프로젝트 디렉토리다). 시험 프로세스의 그 둘을
	// 운영과 같게 맞춘다. 안 맞추면 MCP 만 다른 cc_session_id·워크트리로 열려,
	// **고쳐야 할 축이 아닌 것 때문에** 카드가 갈린다.
	t.Setenv("CLAUDE_CODE_SESSION_ID", cc)
	t.Setenv("CLAUDE_PROJECT_DIR", dir)
	t.Chdir(dir)
	mcpApp := newApp(envOf(mcpEnv), quietLogger(), dir, strings.NewReader(""))
	var out strings.Builder
	if code := runMCP(context.Background(), mcpApp, quietLogger(),
		strings.NewReader(mcpCall("board", map[string]any{})+"\n"), &out); code != 0 {
		t.Fatalf("MCP 채널 실패(종료코드 %d):\n%s", code, out.String())
	}

	// ── 도달 전제: 세 채널이 정말 서버까지 갔는가.
	// 한 채널이 조용히 실패해 행이 1개가 되는 **거짓 초록**을 여기서 막는다.
	if s := out.String(); !strings.Contains(s, `"result"`) {
		t.Fatalf("MCP 채널이 서버까지 못 갔다:\n%s", s)
	}

	// ── 본 단정: 보드에 카드가 한 장인가.
	view, err := h.svc.Board(t.Context(), h.project, service0BoardOptions())
	if err != nil {
		t.Fatalf("보드 조회 실패: %v", err)
	}
	if len(view.Sessions) != 1 {
		ids := make([]string, 0, len(view.Sessions))
		for _, c := range view.Sessions {
			ids = append(ids, c.View.Session.ID+"@"+c.View.Session.MachineID)
		}
		t.Fatalf("한 세션인데 보드 카드가 %d 장이다 — %s", len(view.Sessions), strings.Join(ids, " · "))
	}
	// ── 곁 단정: 그 한 행이 준 cc_session_id 를 글자 그대로 갖고 있는가.
	// (훅 stdin 의 session_id 가 실제로 행에 실렸는지를 단정하는 시험이 지금 0건이다.)
	if got := view.Sessions[0].View.Session.CCSessionID; got != cc {
		t.Errorf("행의 cc_session_id 가 %q 다 — 기대 %q", got, cc)
	}
}
