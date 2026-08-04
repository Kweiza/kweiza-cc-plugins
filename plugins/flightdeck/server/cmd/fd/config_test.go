package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 설정(서버 주소·토큰)의 자리와 우선순위.
//
// ★ **왜 상태 디렉토리가 아니라 ~/.flightdeck 인가.** MachineIDPath 가 이미 같은 판정을
// 내렸고(env.go 의 그 주석), 그 근거가 여기에도 그대로 적용된다: 상태 디렉토리는
// CLAUDE_PLUGIN_DATA(훅·MCP 에만 있다)와 XDG_STATE_HOME|~/.local/state(사용자 셸)로
// **일부러 갈리게** 만든 축이다. 채널마다 다른 자리에 서버 주소를 저장하면
// 셸에서 설정한 값을 훅·MCP 가 못 보고 그 반대도 마찬가지다 — 이 머신에 실제로 두 벌이 있다.
//
// 그래서 "같은 머신이면 같아야 하는" 값은 채널 환경과 무관한 고정 자리에 둔다.
// 이것은 새 규칙이 아니라 machine-id 가 이미 따르는 규칙이다.

func TestConfigPathIsChannelIndependent(t *testing.T) {
	home := "/h"
	// 채널마다 다른 환경을 줘도 **같은 자리**가 나와야 한다.
	envs := []map[string]string{
		{},
		{"CLAUDE_PLUGIN_DATA": "/plugin/data"}, // 훅·MCP 채널
		{"XDG_STATE_HOME": "/xdg/state"},       // 사용자 셸 채널
		{"CLAUDE_PLUGIN_DATA": "/plugin/data", "XDG_STATE_HOME": "/xdg/state"},
	}
	want := filepath.Join(home, ".flightdeck", "config.json")
	for i, e := range envs {
		got, src := ConfigPath(envOf(e), home)
		if got != want {
			t.Errorf("%d번 환경에서 설정 자리가 %q 다 — %q 여야 한다.\n"+
				"채널마다 갈리면 셸에서 저장한 값을 훅·MCP 가 못 본다", i, got, want)
		}
		if strings.TrimSpace(src) == "" {
			t.Errorf("%d번 환경에서 출처가 비었다 — '왜 여기냐'에 답할 자리가 없다", i)
		}
	}
}

// FD_STATE_DIR 는 **사람이 명시 지정하는 축**이라 이것만은 존중한다
// (MachineIDPath 가 같은 예외를 둔다 — 시험이 진짜 홈에 쓰지 않게 막는 유일한 자리이기도 하다).
func TestConfigPathHonoursExplicitStateDir(t *testing.T) {
	got, src := ConfigPath(envOf(map[string]string{"FD_STATE_DIR": "/explicit"}), "/h")
	if want := filepath.Join("/explicit", "config.json"); got != want {
		t.Errorf("FD_STATE_DIR 를 줬는데 %q 다 — %q 여야 한다", got, want)
	}
	if !strings.Contains(src, "FD_STATE_DIR") {
		t.Errorf("출처가 FD_STATE_DIR 를 안 말한다: %q", src)
	}
}

// ── 우선순위 ────────────────────────────────────────────────────────────────

func TestResolveEndpointPrefersEnvOverFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := SaveConfig(path, Config{URL: "http://file:7420", Token: "file-token"}); err != nil {
		t.Fatalf("저장 실패: %v", err)
	}

	cases := []struct {
		name      string
		env       map[string]string
		wantURL   string
		wantToken string
		urlSrc    string
	}{
		{
			// ★ 기존 규율과 같은 결 — 사람이 명시 지정한 축이 이긴다(FD_STATE_DIR·FD_PROJECT).
			name:      "환경변수가 파일을 이긴다",
			env:       map[string]string{"FD_STATE_DIR": dir, "FD_URL": "http://env:7420", "FD_TOKEN": "env-token"},
			wantURL:   "http://env:7420",
			wantToken: "env-token",
			urlSrc:    "FD_URL",
		},
		{
			name:      "환경변수가 없으면 파일",
			env:       map[string]string{"FD_STATE_DIR": dir},
			wantURL:   "http://file:7420",
			wantToken: "file-token",
			urlSrc:    "config.json",
		},
		{
			name:      "둘 다 없으면 기본값",
			env:       map[string]string{"FD_STATE_DIR": t.TempDir()},
			wantURL:   DefaultURL,
			wantToken: "",
			urlSrc:    "기본값",
		},
		{
			// 축이 따로 논다 — URL 만 환경으로 덮고 토큰은 파일에서 오는 조합이 실제로 있다.
			name:      "축마다 따로 이긴다",
			env:       map[string]string{"FD_STATE_DIR": dir, "FD_URL": "http://env:7420"},
			wantURL:   "http://env:7420",
			wantToken: "file-token",
			urlSrc:    "FD_URL",
		},
	}

	for _, c := range cases {
		ep := ResolveEndpoint(envOf(c.env), "/h")
		if ep.URL != c.wantURL {
			t.Errorf("%s: URL 이 %q 다 — %q 여야 한다", c.name, ep.URL, c.wantURL)
		}
		if ep.Token != c.wantToken {
			t.Errorf("%s: 토큰이 %q 다 — %q 여야 한다", c.name, ep.Token, c.wantToken)
		}
		if !strings.Contains(ep.URLSource, c.urlSrc) {
			t.Errorf("%s: URL 출처가 %q 라 %q 를 안 말한다 — fd doctor 가 이걸 찍어야 "+
				"'왜 저 주소인가'에 답할 수 있다", c.name, ep.URLSource, c.urlSrc)
		}
	}
}

// ── 깨진 파일이 도구를 죽이면 안 된다 ───────────────────────────────────────

// ★ 오타 하나로 모든 fd 호출이 죽으면 그게 더 나쁘다. 다만 **조용히 넘어가지도 않는다** —
// warn 을 채워 올리고 fd doctor 가 그것을 찍는다. MachineID 가 같은 모양이다(값, 출처, 경고).
func TestBrokenConfigWarnsInsteadOfKilling(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("준비 실패: %v", err)
	}
	ep := ResolveEndpoint(envOf(map[string]string{"FD_STATE_DIR": dir}), "/h")
	if ep.URL != DefaultURL {
		t.Errorf("깨진 파일인데 URL 이 %q 다 — 기본값으로 가야 한다", ep.URL)
	}
	if strings.TrimSpace(ep.Warn) == "" {
		t.Error("깨진 설정 파일을 조용히 넘어갔다 — 사유가 있어야 사람이 고칠 수 있다")
	}
	if !strings.Contains(ep.Warn, "config.json") {
		t.Errorf("경고가 어느 파일인지 안 말한다: %q", ep.Warn)
	}
}

// 파일이 아예 없는 것은 오류가 아니다 — 아직 설정 안 한 것뿐이다.
func TestMissingConfigIsNotAnError(t *testing.T) {
	ep := ResolveEndpoint(envOf(map[string]string{"FD_STATE_DIR": t.TempDir()}), "/h")
	if strings.TrimSpace(ep.Warn) != "" {
		t.Errorf("설정 파일이 없는 것으로 경고를 냈다: %q", ep.Warn)
	}
}

// ── 저장 ────────────────────────────────────────────────────────────────────

// 토큰은 비밀이라 소유자 전용으로 쓴다.
func TestSaveConfigWritesOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.json")
	if err := SaveConfig(path, Config{URL: "http://x:7420", Token: "s3cret"}); err != nil {
		t.Fatalf("저장 실패: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat 실패: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("권한이 %04o 다 — 0600 이어야 한다(토큰이 들어 있다)", perm)
	}
	got, _, warn := LoadConfig(path)
	if got.URL != "http://x:7420" || got.Token != "s3cret" {
		t.Errorf("왕복이 안 맞는다: %+v (warn=%q)", got, warn)
	}
}

// 권한이 넓으면 **경고하되 거절하지 않는다** — 못 읽으면 도구가 멈추고, 멈추는 쪽이 더 나쁘다.
func TestWideOpenConfigWarnsButStillLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := SaveConfig(path, Config{URL: "http://x:7420", Token: "s3cret"}); err != nil {
		t.Fatalf("저장 실패: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod 실패: %v", err)
	}
	got, _, warn := LoadConfig(path)
	if got.URL != "http://x:7420" {
		t.Errorf("권한이 넓다고 값을 안 냈다: %+v", got)
	}
	if !strings.Contains(warn, "권한") {
		t.Errorf("넓은 권한을 경고하지 않았다: %q", warn)
	}
}

// 저장은 원자적이어야 한다 — 다른 채널이 같은 순간 읽고 있을 수 있다.
func TestSaveConfigDoesNotLeaveTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	for i := 0; i < 3; i++ {
		if err := SaveConfig(path, Config{URL: "http://x:7420"}); err != nil {
			t.Fatalf("저장 실패: %v", err)
		}
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("읽기 실패: %v", err)
	}
	for _, e := range ents {
		if e.Name() != "config.json" {
			t.Errorf("찌꺼기 파일이 남았다: %s", e.Name())
		}
	}
}
