package window

import (
	"path/filepath"
	"testing"
)

func envOf(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestDirPrefersExplicitStateDir(t *testing.T) {
	got, src := Dir(envOf(map[string]string{"FD_STATE_DIR": "/pin"}), "/home/u")
	if want := filepath.Join("/pin", "windows"); got != want {
		t.Fatalf("Dir = %q, want %q", got, want)
	}
	if src == "" {
		t.Fatal("source 가 비었다 — 왜 여기냐에 답할 자리가 없다")
	}
}

func TestDirUsesFixedHomeNotTheStateDir(t *testing.T) {
	// ★ 채널 환경(CLAUDE_PLUGIN_DATA·XDG_STATE_HOME)이 있어도 **이겨서는 안 된다.**
	// 그 둘은 훅에는 오고 사용자 셸에는 안 와서, 이겼다면 같은 창의 두 채널이
	// 서로 다른 디렉토리를 본다.
	env := envOf(map[string]string{
		"CLAUDE_PLUGIN_DATA": "/plugin/data",
		"XDG_STATE_HOME":     "/xdg/state",
	})
	got, _ := Dir(env, "/home/u")
	if want := filepath.Join("/home/u", ".flightdeck", "windows"); got != want {
		t.Fatalf("Dir = %q, want %q — 채널 환경이 이겼다", got, want)
	}
}

func TestDirFallsBackToTempAndSaysSo(t *testing.T) {
	got, src := Dir(envOf(nil), "")
	if got == "" {
		t.Fatal("홈이 없어도 경로는 나와야 한다")
	}
	if src == "" {
		t.Fatal("임시 디렉토리로 떨어진 사실이 source 에 없다")
	}
}
