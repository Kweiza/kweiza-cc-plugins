package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 플러그인 배선 시험 — **파일을 실제로 파싱해 단정한다.**
//
// 여기 있는 것들은 Go 코드가 아니라 JSON·셸이라 컴파일러가 안 봐 준다.
// 그리고 틀리면 조용히 죽는다: type 이 없는 MCP 항목은 서버가 통째로 스킵되고,
// 훅 경로가 틀리면 세션이 그냥 아무것도 안 한다. 그래서 여기가 유일한 검사다.

func pluginRoot(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("플러그인 루트를 못 찾았다: %v", err)
	}
	if _, err := os.Stat(filepath.Join(p, ".claude-plugin", "plugin.json")); err != nil {
		t.Fatalf("여기가 플러그인 루트가 아니다(%s): %v", p, err)
	}
	return p
}

type hooksFile struct {
	Hooks map[string][]struct {
		Matcher string `json:"matcher"`
		Hooks   []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
			Async   bool   `json:"async"`
			Timeout int    `json:"timeout"`
		} `json:"hooks"`
	} `json:"hooks"`
}

func TestHooksJSONIsWiredAsDesigned(t *testing.T) {
	root := pluginRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "hooks", "hooks.json"))
	if err != nil {
		t.Fatalf("hooks.json 을 못 읽었다: %v", err)
	}
	var hf hooksFile
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&hf); err != nil {
		t.Fatalf("hooks.json 이 유효한 JSON 이 아니거나 모르는 키가 있다: %v", err)
	}

	want := map[string]struct {
		matcher string
		async   bool
	}{
		"SessionStart":     {"startup|resume|clear|compact", false},
		"UserPromptSubmit": {"", false},
		"PostToolUse":      {"Edit|Write", true},
		"PreCompact":       {"", true},
		// ★ Stop 은 async 면 안 된다 — 이 훅의 출력이 곧 처방 배달이고, async 는
		//   그 출력의 운명을 안 정해 준다(설계 §6).
		"Stop": {"", false},
		// ★ SessionEnd 는 **clear 만** 받는다. 아래 단정이 그 폭을 붙들고 있다.
		"SessionEnd": {"clear", true},
	}
	if len(hf.Hooks) != len(want) {
		t.Fatalf("훅 이벤트가 %d개다 — %d개여야 한다: %v", len(hf.Hooks), len(want), keysOf(hf.Hooks))
	}

	// ★★ SessionEnd 의 폭을 여기서 못박는다. **이 단정이 이 파일에서 가장 중요하다.**
	//
	// 앞선 판은 SessionEnd 를 통째로 금지했고 그 사유는 "세션 종료를 신뢰성 있게 감지할
	// 수단이 없다"였다. **그 전제는 지금도 참이다** — 설치본 2.1.221·2.1.222 를 뜯어 보면
	// executeSessionEndHooks 를 부르는 자리가 `o3t("clear", …)` 와 `o3t("resume", …)` 둘뿐이고,
	// logout·prompt_input_exit·other·bypass_permissions_disabled 는 zod 열거값에만 있고
	// 아무도 안 쏜다. 훅 이벤트 31종에도 프로세스 종료를 알리는 것이 없다.
	//
	// 바뀐 것은 **쓰임**이다. 이 훅은 죽음을 감지하지 않는다. /clear 로 떠나는 대화의 카드를
	// 닫을 뿐이고, 그 판정은 되돌릴 수 있다(Tx.OpenSession 이 닫힌 카드를 되살린다).
	//
	// 그래서 matcher 를 넓히면 안 된다:
	//   · logout·prompt_input_exit·other — 아무도 안 쏜다. 넣으면 "잡고 있다"는 착각만 생긴다
	//     (prompt_input_exit 옆에는 "Session keeps running. Use /stop to end it." 이 박혀 있다)
	//   · resume — **/fork 도 같은 사유로 온다.** fork 에서 원본 카드를 닫는 것이 옳은지는
	//     별도 판단이고 지금 그 근거가 없다
	se := hf.Hooks["SessionEnd"]
	if len(se) != 1 {
		t.Fatalf("SessionEnd 그룹이 %d개다 — 하나여야 한다", len(se))
	}
	if se[0].Matcher != "clear" {
		t.Fatalf("SessionEnd 의 matcher 가 %q 다 — clear 하나여야 한다. "+
			"이 훅은 프로세스 종료를 못 잡는다(사유 넷은 아무도 안 쏘고, resume 은 /fork 와 공유한다)",
			se[0].Matcher)
	}

	for ev, w := range want {
		groups, ok := hf.Hooks[ev]
		if !ok || len(groups) == 0 {
			t.Fatalf("%s 훅이 없다", ev)
		}
		g := groups[0]
		if g.Matcher != w.matcher {
			t.Fatalf("%s 의 matcher 가 %q 다, %q 를 기대했다", ev, g.Matcher, w.matcher)
		}
		if len(g.Hooks) != 1 {
			t.Fatalf("%s 에 훅이 %d개다", ev, len(g.Hooks))
		}
		h := g.Hooks[0]
		if h.Type != "command" {
			t.Fatalf("%s 의 type 이 %q 다", ev, h.Type)
		}
		// ★ 절대경로여야 한다. 훅 실행 환경이 Bash 도구와 같다는 보장이 없다(설계 §13).
		if !strings.Contains(h.Command, "${CLAUDE_PLUGIN_ROOT}/bin/fd") {
			t.Fatalf("%s 의 명령이 ${CLAUDE_PLUGIN_ROOT}/bin/fd 절대경로가 아니다: %q", ev, h.Command)
		}
		if h.Async != w.async {
			t.Fatalf("%s 의 async 가 %v 다, %v 를 기대했다", ev, h.Async, w.async)
		}
		if h.Timeout <= 0 {
			t.Fatalf("%s 에 타임아웃이 없다 — 훅이 끊기지 않으면 세션이 멈춘다", ev)
		}
	}

	// 훅 이름이 fd 가 아는 것과 **같아야** 한다. 여기가 어긋나면 fd 는 fail-open 이라
	// 조용히 아무것도 안 하고 0 을 낸다 — 그것이 이 단정을 두는 이유다.
	known := map[string]bool{
		"session-start": true, "user-prompt": true, "post-tool": true, "pre-compact": true,
		"stop": true, "session-end": true,
	}
	for ev, groups := range hf.Hooks {
		cmd := groups[0].Hooks[0].Command
		fields := strings.Fields(cmd)
		name := fields[len(fields)-1]
		if !known[name] {
			t.Fatalf("%s 가 fd 가 모르는 훅 이름 %q 를 부른다", ev, name)
		}
	}
}

func keysOf[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestMCPJSONHasTypeStdio(t *testing.T) {
	root := pluginRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if err != nil {
		t.Fatalf(".mcp.json 을 못 읽었다: %v", err)
	}
	var f struct {
		MCPServers map[string]struct {
			Type    string   `json:"type"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf(".mcp.json 이 유효한 JSON 이 아니다: %v", err)
	}
	srv, ok := f.MCPServers["fd"]
	if !ok {
		t.Fatalf("mcpServers 에 fd 가 없다: %v", keysOf(f.MCPServers))
	}
	// ★ type 이 없으면 **서버가 통째로 스킵된다.** 조용히 사라지므로 여기서 막는다.
	if srv.Type != "stdio" {
		t.Fatalf("type 이 %q 다 — stdio 여야 한다(없으면 서버가 통째로 스킵된다)", srv.Type)
	}
	if srv.Command != "${CLAUDE_PLUGIN_ROOT}/bin/fd" {
		t.Fatalf("command 가 %q 다", srv.Command)
	}
	if len(srv.Args) != 1 || srv.Args[0] != "mcp" {
		t.Fatalf("args 가 %v 다 — [\"mcp\"] 여야 한다", srv.Args)
	}
}

// 런처는 **Go 가 없어도 종료코드 0** 이다. 훅이 세션을 막으면 안 된다.
func TestLauncherIsFailOpenWithoutGo(t *testing.T) {
	root := pluginRoot(t)
	script := filepath.Join(root, "bin", "fd")
	info, err := os.Stat(script)
	if err != nil {
		t.Fatalf("bin/fd 가 없다: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("bin/fd 에 실행 권한이 없다: %v", info.Mode())
	}

	// PATH 를 비워 Go 를 없앤다. 스크립트 자체는 절대경로 bash 로 띄운다
	// (shebang 의 /usr/bin/env 도 PATH 를 타므로).
	state := t.TempDir()
	stdout, stderr, code := runLauncher(t, map[string]string{
		"PATH": "", "HOME": t.TempDir(), "FD_STATE_DIR": state,
	}, "status")

	// 대조 전제: 정말 Go 없이 돌았나. 캐시된 바이너리가 있으면 이 시험은 아무것도 안 본다.
	//
	// ★★ **파일 이름이 아니라 디렉토리를 본다 — 이름으로 되돌리지 마라.**
	//    앞선 판은 `os.Stat(state/bin/fd)` 의 **실패**를 이 전제로 썼다. 그런데 산출물
	//    이름에 소스 트리 지문이 붙자(`fd-%2f…`) 그 stat 은 **어떤 경우에도** 실패하게 되어
	//    전제가 영영 참이 됐다 — 시험은 초록인 채로 아무것도 안 보게 되고, 그 무력화는
	//    아무 신호도 안 낸다(안 고쳐도 초록이라 놓치기 쉬운 유일한 자리였다).
	//    이름 규칙의 주인은 런처 하나이고 앞으로도 바뀔 수 있다. 디렉토리는 안 바뀐다.
	assertEmptyOrAbsent(t, filepath.Join(state, "bin"),
		"대조 전제가 깨졌다 — Go 없이 돌렸는데 바이너리 캐시 자리에 무언가 생겼다")

	if code != 0 {
		t.Fatalf("런처가 실패했다(종료코드 0 이어야 한다): code=%d\n%s%s", code, stdout, stderr)
	}
	// stdout 은 훅 계약과 MCP 프레임의 자리다 — 안내 한 줄이라도 섞이면 그 둘이 통째로 깨진다.
	if stdout != "" {
		t.Fatalf("런처가 stdout 에 %d바이트를 썼다: %q", len(stdout), stdout)
	}
	mustContain(t, "런처 안내", stderr,
		"Go 툴체인이 없어",
		"조정 기능 없이 그대로 진행된다",
	)
}

// 정상 환경에서는 실제로 빌드해 exec 한다.
func TestLauncherBuildsAndRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("빌드가 걸린다")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("이 머신에 Go 가 없다")
	}
	root := pluginRoot(t)
	state := t.TempDir()
	cmd := exec.Command(filepath.Join(root, "bin", "fd"), "version")
	// ★ 여기만 runLauncher(통제된 환경)를 **안 쓴다 — 그렇게 고치지 마라.** 이 시험은 실제로
	//   `go build` 를 돌리므로 PATH·GOMODCACHE·HOME 같은 툴체인 환경이 통째로 필요하다.
	//   대신 FD_STATE_DIR 가 사다리의 첫 갈래라 자리는 t.TempDir() 로 못박히고, 진짜 홈의
	//   `~/.cache/flightdeck` 은 이 갈래에서 아예 계산되지 않는다.
	cmd.Env = append(os.Environ(), "FD_STATE_DIR="+state)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("런처가 빌드·실행에 실패했다: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "fd api=") {
		t.Fatalf("빌드된 바이너리가 안 돌았다:\n%s", out)
	}
	// ★ **이름을 박아 단정하지 않는다.** 키 규칙(소스 트리를 단사로 접어 이름에 박는다)의
	//   주인은 런처 하나다. Go 에 그 규칙을 다시 구현하면 같은 판정이 두 자리에 살게 되고,
	//   한쪽만 고칠 때 조용히 어긋난다(client.go newClient 주석의 규율). 여기서 잠그는 것은
	//   둘뿐이다 — **캐시됐다** + **이름이 `fd-` 접두를 따른다**.
	//
	// ★ 정확히 1개여야 한다. 런처의 빌드 임시 파일 `$bin.$$` 도 같은 접두를 갖기 때문에,
	//   1개라는 것은 곧 `mv -f` 로 제자리에 놓였다(임시 파일이 안 남았다)는 뜻이기도 하다.
	cached, gerr := filepath.Glob(filepath.Join(state, "bin", "fd-*"))
	if gerr != nil {
		t.Fatalf("바이너리 캐시 자리를 못 훑었다: %v", gerr)
	}
	if len(cached) != 1 {
		t.Fatalf("캐시된 바이너리가 %d개다 — 정확히 1개여야 한다(0이면 매 훅마다 다시 빌드하게 된다): %v",
			len(cached), cached)
	}
}

// ── 런처의 **자리 규칙** ────────────────────────────────────────────────────
//
// 아래 셋은 2026-08-06 의 두 사고에 대한 회귀 시험이다.
//
//	① 07:15 — 같은 소스가 채널마다 다른 자리에 두 번 지어져 빌드 시각이 55분 어긋났고
//	   (16:10:44 ↔ 17:05:29), 그 창에서 한 응답의 서버 축과 렌더 축이 서로 다른 판을 봤다.
//	   바이너리는 exec 되고 나면 자기가 어느 판인지 **안 말하고 답한다** — 그래서 두 벌이
//	   다르면 하나는 최신인 척하는 옛 코드다. → TestLauncherAddressIgnoresChannel
//	② 워크트리 오염 — 재빌드 판정이 mtime 이라 한 이름을 여러 소스가 나눠 쓰면 **먼저 지은
//	   쪽이 전 채널을 대표한다**. 워크트리에서 런처를 한 번 부르는 것만으로 모든 세션의
//	   훅·MCP 가 그 브랜치 빌드로 갈아 끼워진다. → TestLauncherAddressSplitsBySource
//
// 셋 다 `FD_PRINT_BIN` 이음매를 쓴다 — 런처가 자리를 정한 직후 stderr 에 `bin=<절대경로>`
// 한 줄만 찍고 아무것도 안 지은 채 종료코드 0 으로 끝나는 자리다. **Go 도 빌드도 안 필요해서**
// `-short` 에서도 돌고, 22MB 를 안 지으므로 자리 규칙만 초 단위로 잠글 수 있다.
//
// ★★ **이 시험들은 사용자의 진짜 홈을 절대 건드리면 안 된다.** 런처는 이제 기본으로
//
//	`$HOME/.cache/flightdeck/bin` 에 짓는다. 시험이 HOME 을 잊고 부르면 그 자리에 22MB 를
//	남기고, 그건 시험의 부작용이 아니라 **결함**이다 — 다음 세션의 훅이 시험이 지은 판을
//	exec 하게 된다. 그래서 환경은 map 하나로 통째로 만들고(`env -i` 상당) os.Environ() 을
//	섞지 않는다. 섞으면 CLAUDE_PLUGIN_DATA·XDG_STATE_HOME 이 시험이 모르는 채 딸려 들어와
//	정확히 이 시험들이 잠그려는 축을 오염시킨다. 그 위에 sealedEnv(입력)와 launcherBin(출력)이
//	두 겹으로 막는다 — 통제가 새면 시험이 조용히 통과하는 대신 그 자리에서 죽는다.

// seamToken 은 FD_PRINT_BIN 이음매를 여는 **정확한 값**이다. 런처는 `-n` 이 아니라 이 값과의
// 일치를 본다(bin/fd 의 `[ "${FD_PRINT_BIN:-}" = ... ]`) — 아무 값에나 열리면 셸 프로필에 남은
// 디버그용 export 하나로 플러그인 전체가 **조용한 no-op** 이 되고, 그 무력화는 종료코드 0 이라
// 아무 신호도 안 낸다(이 파일이 다른 자리에서 결함으로 못박은 축과 같다).
//
// ★ 값이 사는 자리는 여기와 런처 **둘뿐**이고, 그것은 피할 수 없다(이음매의 양쪽 끝이다).
// 바꿀 거면 두 자리를 같이 바꿔라 — 한쪽만 고치면 아래 시험들이 이음매를 못 타고 빌드 경로로
// 내려가 "소스를 못 찾았다"로 죽는다(실측: 실제로 그렇게 죽어 있었다).
//
// ★ **"이 값에만 열린다"를 재는 것은 TestLauncherSeamOpensOnlyForItsToken 이다.** 위 문단은
// 한동안 산문뿐이었다 — 아래 시험들이 언제나 정확한 토큰을 주는 탓에 **조여지는 방향**만
// 잠겨 있었고, `= "__fd_addr__"` 를 `-n` 으로 되돌리는 **느슨해지는 방향**은 패키지 전체가
// 초록이었다(뮤테이션으로 확인). 금지를 두 번 선언하고 아무도 안 재는 것이 이 파일이
// 다른 자리에서 결함으로 못박은 축이다.
const seamToken = "__fd_addr__"

// runLauncher 는 런처를 **완전히 통제된 환경**으로 부른다. env 는 통째로 쓰인다.
//
// stdout·stderr 를 안 합친다. 훅 계약과 MCP 프레임이 stdout 을 쓰므로 "stdout 이 0바이트"가
// 그 자체로 단정 대상이고, CombinedOutput 은 그 축을 지운다.
//
// cwd 는 시험 프로세스의 것(= 소스 트리)을 물려받는다. 상대경로가 **어디에 걸리는지**가
// 곧 단정인 자리만 runLauncherIn 으로 cwd 를 통제한다 — TestLauncherRefusesRelativeStateDir.
func runLauncher(t *testing.T, env map[string]string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	return runLauncherIn(t, "", env, args...)
}

// runLauncherIn 은 runLauncher 에 cwd 하나만 더한다(빈 값이면 물려받는다).
// 호출 방식(절대경로 bash · sealedEnv · stdout/stderr 분리)이 두 자리에 갈리면 그 차이가
// 그대로 시험의 사각이 되므로, 실물은 여기 **하나**다.
func runLauncherIn(t *testing.T, dir string, env map[string]string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	script := filepath.Join(pluginRoot(t), "bin", "fd")
	// 절대경로 bash 로 띄운다 — shebang 의 /usr/bin/env 도 PATH 를 타는데, 여기서는
	// PATH 를 비운 채로 부르는 갈래가 있다.
	cmd := exec.Command("/bin/bash", append([]string{script}, args...)...)
	cmd.Env = sealedEnv(t, env)
	cmd.Dir = dir
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("런처를 못 띄웠다: %v", err)
		}
	}
	return o.String(), e.String(), cmd.ProcessState.ExitCode()
}

// sealedEnv 는 시험이 넘긴 map 을 그대로 환경으로 만들되, **진짜 홈이 새어 들어왔는지** 본다.
// 여기가 첫 겹이다. 둘째 겹은 launcherBin 이 계산된 자리를 보고 친다.
func sealedEnv(t *testing.T, kv map[string]string) []string {
	t.Helper()
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if kv["HOME"] == home {
			t.Fatalf("시험이 HOME 으로 진짜 홈(%s)을 준다 — t.TempDir() 를 써라", home)
		}
	}
	out := make([]string, 0, len(kv))
	for k, v := range kv {
		out = append(out, k+"="+v)
	}
	return out
}

// launcherBin 은 FD_PRINT_BIN 이음매로 런처가 **계산만 한** 바이너리 경로를 받아 온다.
// 이음매 계약(정확히 한 줄·stderr·exit 0·stdout 0바이트)을 여기서 함께 단정하므로,
// 아래 시험들은 경로 비교에만 집중하면 된다.
func launcherBin(t *testing.T, env map[string]string) string {
	t.Helper()
	sealed := make(map[string]string, len(env)+1)
	for k, v := range env {
		sealed[k] = v
	}
	sealed["FD_PRINT_BIN"] = seamToken

	stdout, stderr, code := runLauncher(t, sealed, "status")
	if code != 0 {
		t.Fatalf("이음매가 종료코드 %d 를 냈다 — 0 이어야 한다:\n%s", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("이음매가 stdout 에 %d바이트를 썼다 — 그 자리는 훅 계약과 MCP 프레임의 것이다: %q",
			len(stdout), stdout)
	}
	line := strings.TrimSuffix(stderr, "\n")
	if strings.Count(stderr, "\n") != 1 || !strings.HasPrefix(line, "bin=") {
		t.Fatalf("이음매 출력이 `bin=<절대경로>` 한 줄이 아니다(안내가 섞였을 수 있다):\n%s", stderr)
	}
	bin := strings.TrimPrefix(line, "bin=")
	if !filepath.IsAbs(bin) {
		t.Fatalf("이음매가 절대경로가 아닌 %q 를 냈다", bin)
	}

	// ★★ 둘째 겹 — 계산된 자리가 사용자의 진짜 캐시 안이면 그 자체가 결함이다.
	//    이 단정이 없으면 환경 통제가 샜을 때 시험은 **조용히 통과하면서** 진짜 홈에 짓는다.
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if underDir(bin, filepath.Join(home, ".cache", "flightdeck")) {
			t.Fatalf("시험이 사용자의 진짜 바이너리 캐시를 겨눴다(%s) — 환경 통제가 샜다", bin)
		}
	}
	return bin
}

// underDir 은 path 가 dir 안(또는 dir 자신)인지 본다. 심볼릭 링크는 안 푼다 —
// 여기 쓰임은 "시험이 진짜 홈을 겨눴나"라서 문자열 축만으로 충분하다.
func underDir(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// assertEmptyOrAbsent 는 dir 이 **없거나 비었음**을 단정한다.
// 이름이 아니라 디렉토리를 보는 이유는 위 TestLauncherIsFailOpenWithoutGo 주석에 있다.
func assertEmptyOrAbsent(t *testing.T, dir, why string) {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatalf("%s: %s 를 못 읽었다: %v", why, dir, err)
	}
	if len(ents) != 0 {
		names := make([]string, 0, len(ents))
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Fatalf("%s: %s 에 %d개가 있다(%s)", why, dir, len(ents), strings.Join(names, ", "))
	}
}

// 바이너리 자리는 **채널 환경의 함수가 아니다** — 07:15 사고의 회귀 시험.
//
// 옛 런처는 FD_STATE_DIR → CLAUDE_PLUGIN_DATA → XDG_STATE_HOME → HOME 사다리를 탔다.
// 뒤의 둘은 **채널마다 있고 없다**: 훅·MCP 에는 Claude Code 가 넣어 주고 Bash 도구와
// Cursor 확장 호스트에는 없다. 그래서 같은 소스가 같은 머신에서 두 자리에 두 번 지어졌다.
func TestLauncherAddressIgnoresChannel(t *testing.T) {
	home := t.TempDir()
	// 소스 축을 고정한다 — 여기서 흔들리면 아래 SplitsBySource 와 축이 섞인다.
	base := map[string]string{
		"PATH":               "",
		"HOME":               home,
		"CLAUDE_PLUGIN_ROOT": "/opt/flightdeck",
	}

	cases := []struct {
		name  string
		extra map[string]string
	}{
		{"CLAUDE_PLUGIN_DATA 있음", map[string]string{"CLAUDE_PLUGIN_DATA": t.TempDir()}},
		{"XDG_STATE_HOME 있음", map[string]string{"XDG_STATE_HOME": t.TempDir()}},
		{"둘 다 있음", map[string]string{"CLAUDE_PLUGIN_DATA": t.TempDir(), "XDG_STATE_HOME": t.TempDir()}},
		{"둘 다 없음", nil},
	}

	var want string
	for _, c := range cases {
		env := make(map[string]string, len(base)+len(c.extra))
		for k, v := range base {
			env[k] = v
		}
		for k, v := range c.extra {
			env[k] = v
		}
		got := launcherBin(t, env)
		if want == "" {
			want = got
			continue
		}
		if got != want {
			t.Fatalf("%s 에서 자리가 달라졌다:\n  %s\n  %s\n"+
				"바이너리 자리가 채널 환경을 타면 같은 소스가 두 번 지어지고, 두 판의 빌드 시각이\n"+
				"어긋난 창에서 한 응답의 서버 축과 렌더 축이 서로 다른 판을 본다(2026-08-06 07:15).",
				c.name, want, got)
		}
	}

	// 이음매는 **아무것도 안 짓는다** — 그래야 이 시험이 -short 에서도 돌고, 자리 규칙을
	// 재는 행위 자체가 자리를 만들어 버리는 일이 없다.
	assertEmptyOrAbsent(t, filepath.Join(home, ".cache"),
		"FD_PRINT_BIN 이음매가 무언가를 지었다")
}

// 다른 소스 트리는 **다른 자리**를 갖는다 — 워크트리 오염의 회귀 시험.
//
// 재빌드 판정이 mtime 이므로 한 이름을 여러 소스가 나눠 쓰면 먼저 지은 쪽이 전 채널을
// 대표한다. 이름 규칙을 여기서 다시 구현하지 않는다(주인은 런처 하나다) — 단정하는 것은
// **갈린다**는 것뿐이고, 갈리는 방식은 런처의 것이다.
func TestLauncherAddressSplitsBySource(t *testing.T) {
	home := t.TempDir()

	cases := []struct {
		name, a, b string
	}{
		{"릴리스가 다르다", "/opt/flightdeck-0.10.0", "/opt/flightdeck-0.9.0"},
		// ★ 단사 적대 쌍. `/` 를 그냥 `-` 로 접으면 이 둘이 **한 이름**이 되어
		//   방금 없앤 결함(먼저 지은 쪽이 전 채널을 대표한다)이 그대로 돌아온다.
		{"구분자가 충돌한다", "/a/b-c", "/a-b/c"},
		// ★ 이스케이프 문자가 소스 경로에 이미 있는 경우. 접기 전에 이스케이프를
		//   두 배로 안 만들면 이 둘도 한 이름이 된다.
		{"% 가 이미 소스에 있다", "/x%2fy", "/x/y"},
	}

	for _, c := range cases {
		env := map[string]string{"PATH": "", "HOME": home}
		env["CLAUDE_PLUGIN_ROOT"] = c.a
		a := launcherBin(t, env)
		env["CLAUDE_PLUGIN_ROOT"] = c.b
		b := launcherBin(t, env)

		if a == b {
			t.Fatalf("%s: 서로 다른 소스(%s · %s)가 한 자리를 쓴다 — %s\n"+
				"재빌드 판정이 mtime 이라 이러면 **먼저 지은 쪽이 전 채널을 대표한다**.",
				c.name, c.a, c.b, a)
		}
		// 같은 HOME 이면 **디렉토리는 같고 이름만 갈려야** 한다. 갈림이 자리(뿌리)로
		// 새면 채널 무관이라는 위 시험의 성질이 조용히 무너진다.
		if filepath.Dir(a) != filepath.Dir(b) {
			t.Fatalf("%s: 같은 HOME 인데 뿌리가 갈렸다:\n  %s\n  %s", c.name, filepath.Dir(a), filepath.Dir(b))
		}
	}
}

// 런처와 BinCacheDir 은 FD_STATE_DIR 을 **같은 법으로 읽는다.**
//
// ★ 이 시험이 env.go(BinCacheDir 주석 꼬리)와 env_test.go(TestBinCacheDir 머리)가 **가리키는
// 실물**이다. 그 두 자리는 "런처가 같은 답을 내는지는 plugin_test.go 가 런처를 실제로 돌려
// 디렉토리째 견준다"라고 적어 두었고, 그 문장이 참이려면 대조가 여기 있어야 한다 — 없으면
// 계약의 절반은 **아무도 안 재는 문장**이 된다(env_test.go 가 그 사고를 이미 기록해 뒀다:
// 한동안 대조가 없었고 그사이 둘은 공백 처리에서 실제로 갈려 있었다).
//
// 자리 규칙이 두 언어에 사는 것은 여기서만 피할 수 없다 — 런처는 Go 를 못 부르고(Go 가 없어도
// 돌아야 한다) Go 는 런처보다 늦게 뜬다. 계약 전문(⑴트림 ⑵트림 후 비면 미설정 ⑶끝 슬래시는
// 자리를 안 바꾼다)의 주인은 env.go 의 BinCacheDir 주석 **하나**다. 여기서 다시 적지 않는다 —
// 이 시험이 하는 일은 두 구현을 같은 입력에 세워 **바이트로** 견주는 것뿐이다.
//
// 갈리면 무엇이 깨지는지도 그 주석에 있다(GC 가 없는 디렉토리를 훑고 · doctor 가 없는 자리를
// 찍고 · ExeLines 가 재기동해도 안 없어지는 "자리 밖" 거짓 경보를 낸다). 셋 다 이 브랜치가
// 새로 만든 소비부라, 잠자던 불일치에 이빨을 단 것도 이 브랜치다.
//
// ★ **상대경로 갈래는 일부러 표에 없다 — 다만 둘이 갈려서가 아니다.** 이 자리에는 한때
// "런처는 거부하고 BinCacheDir 은 `state/bin` 을 낸다, 그것이 **의도된 비대칭**이다"라고
// 적혀 있었다. **지금은 양쪽 다 거부한다**(env.go 의 계약 ⑷). 그 문장이 세워 둔 근거 둘이
// 실측으로 무너졌다: ⑴ 자리에 **쓰는 것이 런처뿐이 아니다** — pruneBinCache 가 거기서
// 지운다(상대 FD_STATE_DIR 로 훅을 돌리면 cwd 기준 자리의 남의 `fd-*` 가 실제로 사라졌다),
// ⑵ "런처가 거부하면 바이너리가 없어 Go 가 뜨지도 않는다"는 컨테이너 이미지
// (`/usr/local/bin/fd`)·`go run`·손빌드에서 안 선다 — 그 배치는 ExeLines 주석이 이미 1급으로
// 인정한 것이다. 관문이 없던 동안 `fd doctor` 는 `바이너리 캐시 relpath/bin` 을 사실처럼 찍었다.
//
// 그래도 표에서 빼는 이유는 이제 **좌표계**다: 이 표가 견주는 것은 런처의 `bin=` 과 Go 의
// 자리인데 상대경로에서는 **양쪽 다 자리를 안 내므로 견줄 값이 없다**(launcherBin 이 `bin=` 을
// 못 받아 죽는다). 두 절반은 각자 자기 자리에서 잠긴다 — 런처는 아래
// TestLauncherRefusesRelativeStateDir, Go 는 env_test.go 의 TestBinCacheDirRefusesRelativePlaces.
// 배제는 사각이 아니라 자리 이동이다. 다만 **"둘이 같은 답을 낸다"는 것 자체는 아직 아무도
// 안 견준다** — 지금은 두 시험이 각자 "안 낸다"를 따로 말할 뿐이다(후속).
func TestLauncherAndBinCacheDirAgree(t *testing.T) {
	home := t.TempDir()

	// 소스 축은 고정한다 — 여기서 견주는 것은 **디렉토리**뿐이고, 이름(파일명)의 주인은
	// 런처 하나다(env.go 가 "Go 쪽은 키를 만들지도 해독하지도 않는다"고 못박은 축).
	const src = "/opt/flightdeck"

	cases := []struct {
		name string
		set  bool // FD_STATE_DIR 를 아예 안 준다 vs 값을 준다(빈 값도 '준 것'이다)
		val  string
	}{
		{"미설정이면 HOME 갈래", false, ""},
		{"명시 지정", true, "/x"},
		{"앞뒤 공백은 값의 일부가 아니다", true, " /x "},
		{"끝 슬래시는 자리를 안 바꾼다", true, "/x/"},
		{"끝 슬래시가 여럿이어도 같다", true, "/x//"},
		{"루트", true, "/"},
		{"공백뿐인 값은 미설정과 같다", true, "  "},
		{"빈 값도 미설정과 같다", true, ""},
		// ★ CRLF 로 편집된 env 파일이 실제로 만드는 값. 셸의 `[[:space:]]` 와 Go 의
		//   TrimSpace 가 여기서 같은 집합을 봐야 한다(탭·CR 둘 다).
		{"탭과 CR 도 공백이다", true, "\t/x\r"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := map[string]string{"PATH": "", "HOME": home, "CLAUDE_PLUGIN_ROOT": src}
			if c.set {
				env["FD_STATE_DIR"] = c.val
			}

			// 런처의 답 — 실제로 돌려 받는다. 이음매라 아무것도 안 짓는다.
			got := filepath.Dir(launcherBin(t, env))

			// Go 의 답 — 런처가 본 것과 **같은 환경**을 그대로 먹인다.
			want, binSrc := BinCacheDir(envOf(env), env["HOME"])
			if want == "" {
				t.Fatalf("이 표에는 HOME 이 항상 있다 — BinCacheDir 이 자리를 안 냈다(%s)", binSrc)
			}

			if got != want {
				t.Fatalf("FD_STATE_DIR=%q 에서 두 구현이 갈렸다:\n  런처: %s\n  Go  : %s\n"+
					"갈리면 셋이 동시에 틀어진다 — pruneBinCache 가 없는 디렉토리를 훑어 GC 가 영영\n"+
					"안 돌고(22MB×N 이 상한 없이 쌓인다), doctor 가 없는 자리를 찍고, ExeLines 가\n"+
					"제자리의 프로세스에 재기동해도 안 없어지는 '자리 밖' 거짓 경보를 낸다.\n"+
					"계약 전문은 env.go 의 BinCacheDir 주석에 있다.", c.val, got, want)
			}
		})
	}

	// 대조 자체가 자리를 만들면 안 된다 — 이음매는 아무것도 안 짓는다.
	assertEmptyOrAbsent(t, filepath.Join(home, ".cache"),
		"FD_STATE_DIR 대조가 무언가를 지었다")
}

// 이름 상한은 **바이트로 잰다** — 글자로 재면 한글 경로에서 관문이 통째로 헛돈다.
//
// ★ 이 시험이 잠그는 것은 240 이라는 숫자가 아니라 **셈법**이다. 숫자는 런처의 것이고
// (`.$$` 여유까지 그쪽이 계산한다) 여기서 다시 적으면 판정이 두 자리에 산다. 재는 것은
// "한글이 든 깊은 소스에서 관문이 정말 열리는가" 하나다.
//
// ★ 이 갈래는 여기가 생기기 전까지 커버가 **0** 이었다(뮤테이션으로 확인: bin/fd 의
// `nbytes="$(LC_ALL=C; printf %s "${#name}")"` 를 맨 `${#name}` 으로 바꿔도 cmd/fd 가 통째로
// 초록이다). 커버 0 인 갈래는 "서브셸 하나 아끼자"는 다음 편집 한 줄에 조용히 무너진다 —
// 그리고 `LC_ALL=C printf …`(접두 대입)는 bash 에서 `${#name}` **확장 시점의** 로케일을
// 안 바꾸므로 그 편집은 고쳐 보이면서 안 고쳐진다(실측: `fd-`+`가`×150 이 서브셸 판 453 ·
// 접두 대입 판 153). 무너졌을 때 사람이 보는 것은 이 관문이 준비한 네 줄이 아니라
// `go build` 의 원문 ENAMETOOLONG 이고, 훅은 프롬프트마다 도니까 **프롬프트마다** 본다.
//
// ★★ **LC_ALL 을 반드시 준다 — 지우면 이 시험이 공허해진다.** sealedEnv 는 map 만 환경으로
// 쓴다(os.Environ 을 안 섞는다). 로케일을 안 주면 런처의 bash 가 C 로케일로 돌고, 거기서는
// `${#name}` 이 **어차피 바이트**라 두 셈법이 안 갈린다 — 실측: `env -i` 에서 `가`×3 의
// `${#s}` 가 **9** 다. 같은 환경에 위 뮤테이션을 걸어 런처를 직접 불러 보면 관문이 그대로
// 열려(= 시험은 초록) 아무것도 안 잡고, LC_ALL=C.UTF-8 을 주면 `bin=` 이 찍혀 빨간불이 된다.
// 그 로케일이 없는 머신에서 **조용히** 공허해지는 대신 프로브로 확인하고 밝히며 건너뛴다.
func TestLauncherNameCapCountsBytesNotRunes(t *testing.T) {
	const utf8Locale = "C.UTF-8"

	// 프로브 — 이 머신의 bash 가 그 로케일에서 정말 **글자로** 세는지 본다.
	probe := exec.Command("/bin/bash", "-c", `s=가가가; printf %s "${#s}"`)
	probe.Env = []string{"LC_ALL=" + utf8Locale}
	if out, err := probe.Output(); err != nil || string(out) != "3" {
		t.Skipf("이 머신의 bash 가 %s 에서 글자로 안 센다(%q, %v) — 두 셈법이 안 갈려 대조가 공허하다",
			utf8Locale, out, err)
	}

	home := t.TempDir()

	// 한글 90자 + `/server` → 98자 / 278바이트. 런처가 접으면 105자 / 285바이트다.
	// **바이트로 재면 240 을 넘어 관문이 열리고, 글자로 재면 안 열린다** — 그 갈림이 이 시험이다.
	// (접는 방식은 런처의 것이라 여기서 다시 구현하지 않는다. 필요한 것은 두 셈법이
	//  240 을 사이에 두고 갈리는 길이 하나뿐이다.)
	deep := "/" + strings.Repeat("가", 90)
	stdout, stderr, code := runLauncher(t, map[string]string{
		"PATH": "", "HOME": home,
		"LC_ALL":             utf8Locale,
		"CLAUDE_PLUGIN_ROOT": deep,
		// 관문이 안 열리면 이음매가 `bin=` 을 찍는다 — 그것이 이 시험의 빨간불이다.
		"FD_PRINT_BIN": seamToken,
	}, "status")

	if code != 0 {
		t.Fatalf("종료코드가 %d 다 — 0 이어야 한다(훅이 세션을 막으면 안 된다):\n%s", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout 에 %d바이트를 썼다 — 그 자리는 훅 계약과 MCP 프레임의 것이다: %q",
			len(stdout), stdout)
	}
	if strings.Contains(stderr, "bin=") {
		t.Fatalf("한글 %d자(%d바이트) 소스인데 상한 갈래가 안 열렸다 — 글자로 세고 있다.\n"+
			"이러면 관문을 통과한 뒤 go build 가 ENAMETOOLONG 으로 죽고, 사람이 보는 사유는\n"+
			"이 관문이 준비한 네 줄이 아니라 go 의 원문이다 — 그것도 프롬프트마다:\n%s",
			len([]rune(deep)), len(deep), stderr)
	}
	mustContain(t, "상한 갈래의 안내", stderr,
		"소스 경로가 너무 깊어",
		"더 얕은 자리에 두고",
	)

	// 대조군 — 짧은 소스는 **같은 로케일에서** 그대로 자리를 낸다.
	// 없으면 이 시험은 "런처가 늘 거절한다"로도 통과한다.
	if got := launcherBin(t, map[string]string{
		"PATH": "", "HOME": home,
		"LC_ALL":             utf8Locale,
		"CLAUDE_PLUGIN_ROOT": "/opt/flightdeck",
	}); got == "" {
		t.Fatal("대조군이 자리를 안 냈다")
	}

	// 상한 갈래도 이음매도 **아무것도 안 짓는다.**
	assertEmptyOrAbsent(t, filepath.Join(home, ".cache"), "상한 갈래 시험이 무언가를 지었다")
}

// HOME 도 FD_STATE_DIR 도 없으면 **짓지 않는다.**
//
// 옛 런처는 `${HOME:-/tmp}/.local/state/flightdeck` 로 떨어졌다. 부모가 world-writable 인
// 자리는 남이 심어 둔 것을 내가 exec 하는 길이다 — env.go 의 LegacyOutboxDirs 가 tmp 를
// 후보에서 뺀 것과 같은 축이고, 실행 파일은 그쪽보다 무겁다. 그 사다리가 되살아나는 것을
// 여기서 막는다. 대신 세션은 계속돼야 하므로 종료코드는 **0** 이다(훅은 세션을 막지 않는다).
func TestLauncherRefusesWithoutHome(t *testing.T) {
	tmp := t.TempDir()
	// ★ PATH 는 **진짜 것을 준다.** Go 가 있는데도 안 짓는다는 것이 이 시험의 단정이다.
	//   PATH 를 비우면 "Go 가 없어서 안 지었다"와 구별이 안 돼 단정이 공허해진다.
	env := map[string]string{
		"PATH":               os.Getenv("PATH"),
		"TMPDIR":             tmp,
		"CLAUDE_PLUGIN_ROOT": pluginRoot(t),
	}

	// 옛 자리의 **정확한 좌표**를 스냅숏으로 잡는다. 그 줄은 TMPDIR 을 안 보고 `/tmp` 를
	// 박아 뒀으므로, 사다리가 되살아나면 위 TMPDIR 주입으로는 안 잡힌다.
	// 진짜 /tmp 를 통째로 훑지는 않는다 — 이 머신에서 세션 20~30건이 동시에 도는 자리라
	// 남이 만든 파일과 경주하게 된다. 전후 비교라 이 시험이 만든 것만 걸린다.
	const legacyTmp = "/tmp/.local/state/flightdeck/bin"
	before, _ := filepath.Glob(filepath.Join(legacyTmp, "*"))

	for _, seam := range []bool{false, true} {
		e := make(map[string]string, len(env)+1)
		for k, v := range env {
			e[k] = v
		}
		what := "평범한 호출"
		if seam {
			// 이음매도 거부를 **우회하면 안 된다** — 자리 결정 뒤에 찍히므로 여기선 안 찍힌다.
			e["FD_PRINT_BIN"] = seamToken
			what = "FD_PRINT_BIN 이음매"
		}

		stdout, stderr, code := runLauncher(t, e, "status")
		if code != 0 {
			t.Fatalf("%s: 종료코드가 %d 다 — 0 이어야 한다(훅이 세션을 막으면 안 된다):\n%s", what, code, stderr)
		}
		if stdout != "" {
			t.Fatalf("%s: stdout 에 %d바이트를 썼다: %q", what, len(stdout), stdout)
		}
		mustContain(t, what+"의 안내", stderr,
			"HOME 도 FD_STATE_DIR 도 없어",
			"조정 기능 없이 세션은 계속된다",
		)
		if strings.Contains(stderr, "bin=") {
			t.Fatalf("%s: 자리가 없는데 자리를 냈다:\n%s", what, stderr)
		}
	}

	assertEmptyOrAbsent(t, tmp, "HOME 없이 부른 런처가 임시 자리에 무언가를 남겼다")

	after, _ := filepath.Glob(filepath.Join(legacyTmp, "*"))
	if strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Fatalf("옛 /tmp 사다리가 되살아났다(%s):\n  전: %v\n  후: %v", legacyTmp, before, after)
	}
}

// 이음매는 **정확한 토큰에만** 열린다 — `-n` 으로 되돌리는 회귀를 여기서 막는다.
//
// ★ 위 시험들은 언제나 seamToken 을 준다. 그래서 **조여지는 방향**(토큰 값이 바뀐다)은
// 셋이 즉시 잡지만, **느슨해지는 방향**은 아무도 안 쟀다 — 실측: bin/fd 의
// `[ "${FD_PRINT_BIN:-}" = "__fd_addr__" ]` 를 `[ -n … ]` 으로 바꿔도 cmd/fd 가 통째로 초록이다.
// 런처 주석과 seamToken 주석이 둘 다 산문으로 「-n 으로 되돌리지 마라」를 적어 뒀는데
// 그 금지를 재는 단정이 0개였다. 「없는 대조를 있다고 약속하는 주석」은 이 커밋이
// BinCacheDir 축에서 이미 결함으로 못박은 것이고, 이음매 토큰이 같은 상황이었다.
//
// 느슨해지면 셸 프로필·CI 에 남은 디버그용 `export FD_PRINT_BIN=1` 하나로 훅 6개와 MCP
// stdio 서버가 전부 `bin=` 한 줄만 stderr 에 찍고 exit 0 으로 끝난다(실측: hook session-start ·
// mcp · doctor · status 넷 다 code=0 / stdout 0바이트). 종료코드가 0 이라 그 무력화는
// **아무 신호도 안 낸다** — `fd doctor` 마저 여기서 먼저 끝나 진단으로 갈 실이 없다.
//
// 이음매를 안 타므로 빌드도 안 돌고 `-short` 에서도 밀리초다.
func TestLauncherSeamOpensOnlyForItsToken(t *testing.T) {
	cases := []string{
		"1", "true", // 디버그용 export 가 실제로 남기는 값들
		seamToken + "x", "x" + seamToken, " " + seamToken, // 부분 일치로도 안 열린다
		// ★ 빈 값은 `-n` 판에서도 안 열린다 — 이 갈래가 잠그는 것은 회귀가 아니라
		//   「값을 준 빈 문자열은 미설정과 같다」는 `${FD_PRINT_BIN:-}` 의 읽는 법이다.
		"",
	}
	for _, v := range cases {
		t.Run("FD_PRINT_BIN="+v, func(t *testing.T) {
			state := t.TempDir()
			stdout, stderr, code := runLauncher(t, map[string]string{
				"PATH": "", "HOME": t.TempDir(), "FD_STATE_DIR": state,
				"FD_PRINT_BIN": v,
			}, "status")

			if strings.Contains(stderr, "bin=") {
				t.Fatalf("FD_PRINT_BIN=%q 에 이음매가 열렸다 — 이것은 토큰이 아니다:\n%s\n"+
					"아무 값에나 열리면 디버그용 export 하나로 훅 6개와 MCP 서버가 전부\n"+
					"조용한 no-op 이 되고, 종료코드 0 이라 아무 신호도 안 난다.", v, stderr)
			}
			// 이음매를 안 탔으면 **정상 갈래로 그대로 내려가야** 한다 — PATH 를 비웠으니
			// 여기서 Go 부재 안내가 나온다. 이 대조가 없으면 "런처가 그냥 죽었다"로도 통과한다.
			mustContain(t, "정상 갈래의 안내", stderr,
				"Go 툴체인이 없어",
				"조정 기능 없이 그대로 진행된다",
			)
			if code != 0 {
				t.Fatalf("종료코드가 %d 다 — 0 이어야 한다(훅이 세션을 막으면 안 된다):\n%s", code, stderr)
			}
			if stdout != "" {
				t.Fatalf("stdout 에 %d바이트를 썼다 — 그 자리는 훅 계약과 MCP 프레임의 것이다: %q",
					len(stdout), stdout)
			}
			assertEmptyOrAbsent(t, filepath.Join(state, "bin"),
				"토큰이 아닌 값으로 부른 런처가 무언가를 지었다")
		})
	}
}

// 자리가 **상대경로면 짓지 않는다.**
//
// 빌드는 `(cd "$src" && go build -o "$tmp")` 안에서 돈다 — 상대 산출물은 **플러그인 소스
// 트리 아래**로 떨어지고, 뒤이은 `mv` 도 정리용 `rm -f` 도 원래 cwd 기준이라 둘 다 빗나간다
// (실측: 22MB 고아가 훅마다, 곧 프롬프트마다 하나씩 쌓인다. 이름에 `.$$` 가 붙어 호출마다
// 새것이 생기고, GC 는 BinCacheDir 자리만 훑으므로 그 자리를 영영 모른다. 종료코드는 0 이라
// 아무도 안 멈춘다). 자리가 없다고 **말하는** 쪽이 조용히 새는 쪽보다 낫다.
//
// ★ **런처 절반을 재는 곳은 여기 하나뿐이다.** TestLauncherAndBinCacheDirAgree 의 대조표는
// 이 갈래를 뺐다 — 상대경로에서는 **양쪽 다** 자리를 안 내서 견줄 값이 없고(그 주석을 본다),
// launcherBin 이 `bin=` 을 못 받아 죽는다. 그 배제가 곧 사각이었다: 런처의 거부 네 줄을
// 통째로 지워도 패키지 시험이 전부 초록이었다(뮤테이션으로 확인).
//
// Go 절반의 주인은 여기가 아니다 — env_test.go 의 TestBinCacheDirRefusesRelativePlaces 다.
// 여기서 BinCacheDir 을 함께 부르지 않는 이유는 그것이 같은 판정의 둘째 화면이 되기 때문이고,
// 이 라운드의 blocker 가 정확히 그 사고였다.
func TestLauncherRefusesRelativeStateDir(t *testing.T) {
	for _, seam := range []bool{false, true} {
		what := "평범한 호출"
		// ★ PATH 는 **비운다 — 진짜 것으로 되돌리지 마라.** 거부가 사라지면 이 갈래는
		//   `go build` 까지 내려가고, 그 산출물 22MB 가 cwd 아래에 고아로 떨어진다(실측).
		//   Go 가 없으면 거기 못 가고, 그때 나오는 안내가 "Go 툴체인이 없어"로 갈리므로
		//   회귀는 아래 mustContain 이 그대로 잡는다.
		env := map[string]string{
			"PATH": "", "HOME": t.TempDir(), "FD_STATE_DIR": "state",
		}
		if seam {
			// 이음매도 거부를 **우회하면 안 된다** — 자리 판정 뒤에 찍히므로 여기선 안 찍힌다.
			env["FD_PRINT_BIN"] = seamToken
			what = "FD_PRINT_BIN 이음매"
		}

		// cwd 를 통제한다 — 상대경로가 **어디에 걸리는지**가 이 시험의 단정이고,
		// 물려받으면 그 "어디"가 시험이 도는 소스 트리가 된다.
		cwd := t.TempDir()
		stdout, stderr, code := runLauncherIn(t, cwd, env, "status")

		if code != 0 {
			t.Fatalf("%s: 종료코드가 %d 다 — 0 이어야 한다(훅이 세션을 막으면 안 된다):\n%s",
				what, code, stderr)
		}
		if stdout != "" {
			t.Fatalf("%s: stdout 에 %d바이트를 썼다: %q", what, len(stdout), stdout)
		}
		mustContain(t, what+"의 안내", stderr,
			"상대경로다",
			"조정 기능 없이 세션은 계속된다",
		)
		if strings.Contains(stderr, "bin=") {
			t.Fatalf("%s: 자리가 상대경로인데 자리를 냈다:\n%s", what, stderr)
		}
		// HOME 으로 조용히 떨어져도 안 된다 — 거부는 **거부**지 폴백이 아니다.
		assertEmptyOrAbsent(t, filepath.Join(env["HOME"], ".cache"),
			what+": 상대 FD_STATE_DIR 를 거부하는 대신 HOME 갈래로 떨어졌다")
		assertEmptyOrAbsent(t, cwd,
			what+": 상대 FD_STATE_DIR 로 부른 런처가 cwd 에 무언가를 남겼다")
	}
}

// 스킬 본문의 줄 수 상한은 **호출 빈도로 갈린다.**
//
// ★ 앞선 판의 주석은 "스킬 목록은 항목당 잘리므로"를 60줄의 근거로 댔는데, 그 문장이
// 맞다면 **본문을 아무리 길게 써도 목록은 안 밀린다** — 근거와 결론이 안 이어진다.
// 그래서 상한을 "부른 턴의 예산"으로 다시 세우고 호출 빈도로 갈랐다. 그 비용은 부르는
// 만큼 반복해서 들기 때문이다.
//
// ★ **다만 그 재정의가 기대는 플랫폼 동작은 아직 안 쟀다 — 측정 전 잠정이다.**
// "목록에는 frontmatter 의 `description`(항목당 1,536자)만 실리고 본문은 스킬을 부른
// 뒤에 실린다"는 이 레포 어디에도 측정 기록이 없다(§13 「아직 아님」에 올려 뒀다).
// 앞선 판이 두 축을 섞은 자리를 **또 다른 미측정값**으로 메우지 않기 위해 여기 적어 둔다 —
// §13 의 첫 줄이 "추측을 사실로 적지 않는다"다. 80 을 지탱하는 실질 근거는 아래 회귀선이고,
// 그쪽은 이 측정과 무관하게 선다.
//
// ★ 머신 스킬의 80은 계산이 아니라 **회귀선**이다. `fd-update` 가 지금 72줄이고, 그 산문은
// 갱신 판정을 코드로 안 뺐기 때문에 있다(DESIGN §1 의 2026-08-06 개정). 여기서 더 늘면
// 줄을 깎을 것이 아니라 `fd update` 를 만들지를 다시 판정해야 한다 — 그 판정을 미루는 대신
// 상한을 올리는 것이 이 항목이 고발한 표류다.
var skillLineCaps = map[string]int{
	"fd-pickup":  60, // 매 세션 부른다
	"fd-handoff": 60, // 매 세션 부른다
	"fd-setup":   80, // 머신당 1회
	"fd-update":  80, // 머신당 1회
}

func TestSkillsStayWithinTheContextBudget(t *testing.T) {
	root := pluginRoot(t)

	// ★ 스킬 **전수**를 표에 물린다. 이 항목의 뿌리가 "넷째(`fd-update`)가 이 표에서 빠진
	// 채로 DESIGN 도 셋이라 적고 아무도 안 세었다"이다. 표를 손으로만 유지하면 다섯째에
	// 같은 일이 그대로 난다 — 빠진 스킬은 줄 수도 frontmatter 도 검사받지 않는다.
	ents, err := os.ReadDir(filepath.Join(root, "skills"))
	if err != nil {
		t.Fatalf("skills 디렉토리를 못 읽었다: %v", err)
	}
	onDisk := map[string]bool{}
	for _, e := range ents {
		if e.IsDir() {
			onDisk[e.Name()] = true
		}
	}
	for name := range onDisk {
		if _, ok := skillLineCaps[name]; !ok {
			t.Fatalf("스킬 %s 가 상한 표에 없다 — 표에 없는 스킬은 줄 수도 frontmatter 도\n"+
				"검사받지 않는다. 수를 늘리기 전에 DESIGN §1 이 그것을 정당화하는지부터 정해라", name)
		}
	}
	for name := range skillLineCaps {
		if !onDisk[name] {
			t.Fatalf("상한 표의 %s 가 skills/ 에 없다 — 죽은 이름이 표에 남으면\n"+
				"그 표를 근거로 센 수가 전부 틀린다", name)
		}
	}

	for name, limit := range skillLineCaps {
		path := filepath.Join(root, "skills", name, "SKILL.md")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s 를 못 읽었다: %v", name, err)
		}
		lines := strings.Count(strings.TrimRight(string(raw), "\n"), "\n") + 1
		if lines >= limit {
			t.Fatalf("%s 가 %d줄이다 — %d줄 미만이어야 한다", name, lines, limit)
		}
		// frontmatter 의 name·description 이 없으면 스킬이 목록에 안 뜬다.
		head := string(raw)
		if !strings.HasPrefix(head, "---\n") {
			t.Fatalf("%s 에 frontmatter 가 없다", name)
		}
		mustContain(t, name+" frontmatter", head, "name: "+name, "description:")
	}
}

// 문서가 세는 스킬 수와 실재하는 수가 어긋나면 여기서 걸린다.
//
// ★ 이 시험이 없어서 난 일이 이 항목이다: `cdce59d` 가 넷째를 만들었고 README 는 넷으로
// 고쳤는데 DESIGN §1 만 셋으로 남았다. 그 문장 바로 아래 문단이 **"셋인 근거"**를 대고
// 있었으므로, 수만 고치면 그 문단이 통째로 거짓이 되는 자리였다 — 즉 이 어긋남은
// 오탈자가 아니라 **설계 판정이 밀린 흔적**이고, 그래서 조용히 오래 남았다.
//
// ★ 잠그는 것은 **수**뿐이다. 근거 산문까지 잠그면 개정할 때마다 시험이 깨져서
// 근거를 안 고치고 수만 고치는 쪽으로 사람을 민다.
//
// ★ **"어딘가에 그 수가 있다"로는 부족하다 — 모든 출현을 본다.** 앞선 판은
// `strings.Contains(파일 전체, "스킬은 4개")` 였는데, 그러면 문서 끝에 "스킬은 3개"를
// 덧붙여도 초록이다(격리 사본에서 실증). 이 저장소는 옛 문단을 안 지우고 개정 블록을
// 얹는 습관이 있어서(§1 이 그 예다) 낡은 수가 남기 쉽고, 그게 정확히 이 항목의 뿌리였다.
// 그래서 정규식으로 **출현 전부**를 뽑아 하나라도 다르면 실패시킨다. 0건도 실패다.
// §1 이 보존한 옛 문단의 "셋"·"넷"은 한글이라 이 정규식에 안 걸린다 — 사료 서술은
// 살아남고 현재형 수만 잠긴다.
func TestDocsCountTheSkillsThatActuallyExist(t *testing.T) {
	root := pluginRoot(t)
	// ★ 수를 상한 표에서 뽑는다. 표는 위 시험이 skills/ 전수에 물려 뒀으므로, 다섯째가
	// 생기면 연쇄가 정확히 돈다: 전수 검사가 "표에 없다"로 먼저 걸리고 → 표에 넣으면
	// 이 시험이 문서의 수를 요구한다. 어느 한 자리만 고치고 지나가는 길이 없다.
	want := len(skillLineCaps)
	// ★ README 가 영문 기본 + `.ko.md` 짝으로 갈린 뒤(2026-08-14)에도 **세 자리 전부** 본다.
	// 한글이 정본이고 영문이 번역본이라, 잠글 것이 늘었지 줄지 않았다 — 번역본만 낡는 것이
	// 이 갈림이 새로 만든 실패 모양이고, 영문 패턴을 안 걸면 그 자리는 아무도 안 센다.
	for _, doc := range []struct{ file, pattern string }{
		{"DESIGN.md", `스킬은 (\d+)개`},
		{"README.ko.md", `스킬 (\d+)개`},
		{"README.md", `(\d+) skills`},
	} {
		raw, err := os.ReadFile(filepath.Join(root, doc.file))
		if err != nil {
			t.Fatalf("%s 를 못 읽었다: %v", doc.file, err)
		}
		hits := regexp.MustCompile(doc.pattern).FindAllStringSubmatch(string(raw), -1)
		if len(hits) == 0 {
			t.Fatalf("%s 가 스킬 수를 아예 안 말한다(정규식 %q) — 실재하는 스킬은 %d개다(skills/).\n"+
				"수를 고칠 때는 그 수의 **근거**를 대는 문단이 같이 거짓이 되는지 보고,\n"+
				"거짓이 되면 근거부터 다시 써라(DESIGN §1 의 2026-08-06 개정이 그 예다)",
				doc.file, doc.pattern, want)
		}
		for _, h := range hits {
			if h[1] != fmt.Sprintf("%d", want) {
				t.Fatalf("%s 가 %q 라고 말한다 — 실재하는 스킬은 %d개다(skills/).\n"+
					"출현 %d건 중 하나라도 어긋나면 실패다. 개정 블록을 얹을 때 **낡은 수를 현재형으로**\n"+
					"남기지 마라 — 이 항목의 뿌리가 바로 그것이다(사료로 남길 것은 한글로 적어라)",
					doc.file, h[0], want, len(hits))
			}
		}
	}
}

// compose 와 Dockerfile 이 설계가 못박은 좌표를 지키는가.
func TestContainerFilesKeepTheDesignedCoordinates(t *testing.T) {
	root := pluginRoot(t)
	df, err := os.ReadFile(filepath.Join(root, "server", "Dockerfile"))
	if err != nil {
		t.Fatalf("Dockerfile 이 없다: %v", err)
	}
	mustContain(t, "Dockerfile", string(df),
		"CGO_ENABLED=0",
		"distroless",
		"HEALTHCHECK",
		"EXPOSE 7420",
	)
	cf, err := os.ReadFile(filepath.Join(root, "compose.yaml"))
	if err != nil {
		t.Fatalf("compose.yaml 이 없다: %v", err)
	}
	mustContain(t, "compose.yaml", string(cf),
		"7420:7420",
		"~/.flightdeck:/data",
		"restart: unless-stopped",
		"healthcheck:",
	)
}
