package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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
	}
	if len(hf.Hooks) != len(want) {
		t.Fatalf("훅 이벤트가 %d개다 — %d개여야 한다: %v", len(hf.Hooks), len(want), keysOf(hf.Hooks))
	}
	// ★ SessionEnd 는 **없어야 한다.** reason 열거에 크래시·컨텍스트 소진이 없어서
	//   그 위에 아무것도 세울 수 없다(설계 §6).
	if _, bad := hf.Hooks["SessionEnd"]; bad {
		t.Fatal("SessionEnd 훅이 있다 — 세션 종료를 신뢰성 있게 감지할 수단이 없다는 것이 이 설계의 전제다")
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
		"stop": true,
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
	cmd := exec.Command("/bin/bash", script, "status")
	cmd.Env = []string{"PATH=", "HOME=" + t.TempDir(), "FD_STATE_DIR=" + state}
	out, err := cmd.CombinedOutput()

	// 대조 전제: 정말 Go 없이 돌았나. 캐시된 바이너리가 있으면 이 시험은 아무것도 안 본다.
	if _, serr := os.Stat(filepath.Join(state, "bin", "fd")); serr == nil {
		t.Fatal("대조 전제가 깨졌다 — Go 없이 돌렸는데 바이너리가 만들어졌다")
	}
	if err != nil {
		t.Fatalf("런처가 실패했다(종료코드 0 이어야 한다): %v\n%s", err, out)
	}
	got := string(out)
	mustContain(t, "런처 안내", got,
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
	cmd.Env = append(os.Environ(), "FD_STATE_DIR="+state)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("런처가 빌드·실행에 실패했다: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "fd api=") {
		t.Fatalf("빌드된 바이너리가 안 돌았다:\n%s", out)
	}
	if _, serr := os.Stat(filepath.Join(state, "bin", "fd")); serr != nil {
		t.Fatalf("바이너리가 캐시되지 않았다 — 매 훅마다 다시 빌드하게 된다: %v", serr)
	}
}

// 스킬은 **60줄 미만**이다. 컨텍스트 예산이 이 설계의 제약이고,
// 스킬 목록은 항목당 잘리므로 규율 산문을 넣으면 그것이 도구 설명을 밀어낸다.
func TestSkillsStayWithinTheContextBudget(t *testing.T) {
	root := pluginRoot(t)
	for _, name := range []string{"fd-pickup", "fd-handoff", "fd-setup"} {
		path := filepath.Join(root, "skills", name, "SKILL.md")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s 를 못 읽었다: %v", name, err)
		}
		lines := strings.Count(strings.TrimRight(string(raw), "\n"), "\n") + 1
		if lines >= 60 {
			t.Fatalf("%s 가 %d줄이다 — 60줄 미만이어야 한다", name, lines)
		}
		// frontmatter 의 name·description 이 없으면 스킬이 목록에 안 뜬다.
		head := string(raw)
		if !strings.HasPrefix(head, "---\n") {
			t.Fatalf("%s 에 frontmatter 가 없다", name)
		}
		mustContain(t, name+" frontmatter", head, "name: "+name, "description:")
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
