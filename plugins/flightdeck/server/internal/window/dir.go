package window

import (
	"os"
	"path/filepath"
	"strings"
)

// Dir 는 비콘 디렉토리를 고른다. 순수 함수다.
//
// ★ **ResolveStateDir 을 쓰지 않는다.** 그 사다리는 CLAUDE_PLUGIN_DATA·XDG_STATE_HOME 로
// 갈리는데 그 둘은 **채널마다 있고 없다** — Claude Code 가 훅·MCP 에는 넣어 주고 사용자 셸에는
// 안 넣는다. machine-id 를 거기 뒀다가 파일이 두 벌이 됐고 한 세션이 카드 세 장으로 떴다
// (cmd/fd/env.go 의 MachineIDPath 주석).
//
// 비콘의 요구는 machine-id 와 **정확히 같다**: 같은 창이면 어느 채널에서 봐도 같아야 한다.
// 그래서 같은 사다리를 쓴다 — FD_STATE_DIR(사람이 명시) → $HOME/.flightdeck → 임시.
func Dir(get func(string) (string, bool), home string) (path, source string) {
	if v, ok := get("FD_STATE_DIR"); ok && strings.TrimSpace(v) != "" {
		return filepath.Join(filepath.Clean(strings.TrimSpace(v)), "windows"), "FD_STATE_DIR (명시 지정)"
	}
	if strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".flightdeck", "windows"), "~/.flightdeck — 채널 환경과 무관한 고정 자리"
	}
	return filepath.Join(os.TempDir(), "flightdeck", "windows"),
		"임시 디렉토리 — HOME 이 없다. 재부팅하면 창 정체가 끊긴다"
}
