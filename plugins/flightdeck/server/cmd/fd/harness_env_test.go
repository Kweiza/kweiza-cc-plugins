package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 이 파일이 지키는 것은 **하네스 자신**이다.
//
// 시험 하네스가 환경 축을 한 값에 못박으면, 그 축의 결함은 전 시험 초록 상태로 산다 —
// 시험이 그 갈래를 원리적으로 평가하지 않기 때문이다. 실제로 그렇게 살았다:
// 머신 id 가 채널마다 갈려 한 세션이 보드에 카드 세 장으로 떴는데(306f9b7), 그때까지
// 전 시험이 초록이었던 이유가 harness 의 FD_STATE_DIR 고정이다.
//
// 그래서 축을 푸는 정식 갈래(unpinnedEnv)를 뒀고, 여기서 **그 갈래가 제 일을 하는지**와
// **더 큰 사고를 새로 열지 않는지**를 함께 단정한다.

// 비시험 코드가 읽는 환경 키 전수(2026-08-04 조사).
//
// ★ 목록을 여기 박아 두는 이유는 **새 축이 생겼을 때 알아채기 위해서**다.
// 코드가 새 환경 키를 읽기 시작하면 이 시험이 빨개지고, 그때 "하네스가 이 축도
// 고정하는가 / 고정하면 무엇이 안 보이게 되는가"를 한 번은 묻게 된다.
// 묻지 않으면 다음 사각은 다음 사고로만 드러난다.
var knownEnvAxes = []string{
	"CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_SSE_PORT",
	"CLAUDE_ENV_FILE", "CLAUDE_PLUGIN_DATA", "CLAUDE_PLUGIN_ROOT", "CLAUDE_PROJECT_DIR",
	"FD_ADDR", "FD_DB", "FD_LOG", "FD_PROJECT", "FD_SESSION", "FD_STATE_DIR",
	"FD_TIMEOUT", "FD_TOKEN", "FD_URL", "FD_WORKTREE",
	"HOME", "XDG_STATE_HOME",
}

// ★ 이 시험의 핵심. 축을 푸는 것 자체보다 **푸는 순간 열리는 문**이 위험하다.
//
// FD_STATE_DIR 를 빼면 MachineIDPath 의 둘째 가지(~/.flightdeck/machine-id)가 열리고,
// homeDir 은 주입된 HOME 이 없으면 os.UserHomeDir() 로 떨어진다 — 그것은 **프로세스
// 환경**이라 시험이 못 바꾼다. 즉 HOME 없이 축만 풀면 시험이 사용자의 진짜 홈에
// machine-id 를 읽고 쓴다. 지금 그 문을 막고 있는 유일한 것이 다름 아닌 그 고정이었다.
//
// 맵에 "HOME" 키가 있는지를 보는 것으로는 부족하다 — 실제로 위험한 것은 **합성 결과**인
// machine-id 경로이므로 그것을 계산해서 단정한다.
func TestUnpinnedEnvNeverReachesTheRealHome(t *testing.T) {
	h := newHarness(t)
	env := h.unpinnedEnv(nil)

	realHome, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(realHome) == "" {
		t.Skipf("이 머신에서 진짜 홈을 못 읽어 대조가 성립하지 않는다: %v", err)
	}

	// ── 대조가 성립했는지 먼저 단정한다 ─────────────────────────────────────
	// 축이 안 풀렸으면 아래 검사는 "위험한 가지에 아예 안 갔다"를 보는 것이라 무의미하다.
	if _, pinned := env["FD_STATE_DIR"]; pinned {
		t.Fatal("전제가 깨졌다 — unpinnedEnv 가 FD_STATE_DIR 를 여전히 들고 있다. 축이 안 풀렸다")
	}

	home := homeDir(envOf(env))
	if home == realHome {
		t.Fatalf("unpinnedEnv 가 **진짜 홈**을 준다(%s) — 시험이 사용자 상태를 오염시킨다", realHome)
	}

	path, source := MachineIDPath(envOf(env), home)
	if strings.HasPrefix(filepath.Clean(path), filepath.Clean(realHome)+string(filepath.Separator)) {
		t.Fatalf("machine-id 경로가 진짜 홈 아래다: %s (source=%s)\n"+
			"FD_STATE_DIR 를 뺐으면 HOME 을 함께 옮겨야 한다 — 안 그러면 시험이 "+
			"사용자의 ~/.flightdeck/machine-id 를 읽고 덮어쓴다.", path, source)
	}
	if !strings.HasPrefix(filepath.Clean(path), filepath.Clean(h.home)) {
		t.Errorf("machine-id 경로가 하네스의 가짜 홈(%s) 아래가 아니다: %s", h.home, path)
	}
}

// ★ FD_URL 은 **일부러 고정된 채로 남는다.** 풀면 DefaultURL(127.0.0.1:7420)로 떨어져
// 시험이 개발자 머신의 진짜 조정 서버를 친다. 사각을 여는 것과 사고를 여는 것은 다르다.
func TestUnpinnedEnvKeepsTheServerAddressPinned(t *testing.T) {
	h := newHarness(t)
	env := h.unpinnedEnv(nil)

	if got := env["FD_URL"]; got != h.srv.URL {
		t.Fatalf("FD_URL 이 시험 서버(%s)를 안 가리킨다: %q\n"+
			"이 축까지 풀면 시험이 %s 의 진짜 서버를 친다.", h.srv.URL, got, DefaultURL)
	}
}

// 축을 푼 갈래에서 **나머지 네 가지가 실제로 평가되는지**를 본다.
// 이것이 초록이어야 "사각이 열렸다"고 말할 수 있다.
func TestUnpinnedEnvReachesEveryStateDirBranch(t *testing.T) {
	h := newHarness(t)

	pluginData, xdg := t.TempDir(), t.TempDir()
	cases := []struct {
		name   string
		extra  map[string]string
		want   string
		source string
	}{
		{
			name:   "CLAUDE_PLUGIN_DATA (훅·MCP 프로세스에는 Claude Code 가 넣어 준다)",
			extra:  map[string]string{"CLAUDE_PLUGIN_DATA": pluginData, "XDG_STATE_HOME": xdg},
			want:   filepath.Join(pluginData, "flightdeck"),
			source: "CLAUDE_PLUGIN_DATA",
		},
		{
			name:   "XDG_STATE_HOME (CLAUDE_PLUGIN_DATA 가 없는 사용자 셸)",
			extra:  map[string]string{"XDG_STATE_HOME": xdg},
			want:   filepath.Join(xdg, "flightdeck"),
			source: "XDG_STATE_HOME",
		},
		{
			name:   "홈 폴백 — 둘 다 없다",
			extra:  nil,
			want:   filepath.Join(h.home, ".local", "state", "flightdeck"),
			source: "~/.local/state",
		},
	}

	seen := map[string]bool{}
	for _, c := range cases {
		env := h.unpinnedEnv(c.extra)
		sd := ResolveStateDir(envOf(env), homeDir(envOf(env)))
		if sd.Path != c.want {
			t.Errorf("%s: 상태 디렉토리가 %q 여야 하는데 %q 다", c.name, c.want, sd.Path)
		}
		if !strings.Contains(sd.Source, c.source) {
			t.Errorf("%s: 사유에 %q 가 없다 — 왜 그 자리인지 말하지 못한다: %q", c.name, c.source, sd.Source)
		}
		seen[sd.Path] = true
	}

	// ★ 세 갈래가 **서로 다른 자리**로 갔는지. 우연히 같은 곳으로 가면 이 시험은
	//   가지 셋을 다 돌았다고 믿으면서 실제로는 하나만 본 것이 된다.
	if len(seen) != len(cases) {
		t.Errorf("가지 %d개가 자리 %d곳으로만 갔다 — 갈림을 실제로 안 본 것이다: %v",
			len(cases), len(seen), seen)
	}
}

// 새 환경 축이 생기면 알아챈다 — 위 knownEnvAxes 주석에 이유가 있다.
func TestEnvAxisInventoryIsCurrent(t *testing.T) {
	known := map[string]bool{}
	for _, k := range knownEnvAxes {
		known[k] = true
	}
	// 하네스가 고정하는 축은 전부 알려진 축이어야 한다 — 목록이 낡으면 여기서 걸린다.
	h := newHarness(t)
	for k := range h.env {
		if !known[k] {
			t.Errorf("하네스가 고정하는 %q 가 knownEnvAxes 에 없다 — 목록이 낡았다", k)
		}
	}
}

// 축을 푼 갈래가 **진짜 run() 경로 끝까지** 도는지 본다.
//
// 위 시험들은 ResolveStateDir 를 직접 부른다 — 그것은 판정 함수가 맞다는 것만 말하고
// "fd 를 그 환경으로 돌리면 실제로 그 자리를 쓰나"는 말하지 못한다. 하네스 갈래의
// 존재 이유가 후자이므로 여기서 그것을 단정한다.
func TestUnpinnedRunActuallyUsesTheResolvedStateDir(t *testing.T) {
	h := newHarness(t)
	pluginData := t.TempDir()

	code, out := h.runUnpinned(map[string]string{"CLAUDE_PLUGIN_DATA": pluginData}, "", "doctor")
	if code != 0 {
		t.Fatalf("doctor 가 %d 로 끝났다:\n%s", code, out)
	}

	want := filepath.Join(pluginData, "flightdeck")
	if !strings.Contains(out, want) {
		t.Errorf("doctor 가 상태 디렉토리로 %q 를 안 찍었다 — 축이 실제 경로까지 안 닿았다:\n%s", want, out)
	}
	// ★ 하네스의 고정값이 아직 이기고 있으면 위 검사가 우연히 통과할 수 있으므로 함께 본다.
	if strings.Contains(out, h.state) {
		t.Errorf("doctor 가 여전히 하네스의 고정 상태 디렉토리(%s)를 쓴다 — 축이 안 풀렸다:\n%s", h.state, out)
	}
}
