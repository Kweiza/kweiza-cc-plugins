package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// 설정 — 서버 주소와 토큰. **채널 환경과 무관한 고정 자리에 둔다.**
//
// ★ 왜 상태 디렉토리가 아닌가. ResolveStateDir 은 CLAUDE_PLUGIN_DATA(훅·MCP 프로세스에만
// Claude Code 가 넣어 준다)와 XDG_STATE_HOME|~/.local/state(사용자 셸)로 **일부러 갈리게**
// 만든 축이다 — **응답 캐시**는 채널마다 따로여도 되기 때문이다. 받은 시각을 값과 같은
// 파일에 달고 다녀서(cache.go 의 CacheEntry.At) 갈린 사본이 각자 자기 시점의 참이다.
//
// ★ 이 주석은 **두 번** 틀렸다. 두 번 다 원인이 같다 — **부류를 너무 넓게 잡았다.**
//
//  1. 예전에는 "캐시·아웃박스는 채널마다 따로여도 된다"고 적었는데, 아웃박스는 설계 §7 이
//     "재생성 불가한 유일한 자산"이라 부른 것을 담는다. 가르는 축은 **열화 여부가 아니라
//     재생성 가능성**이고, 아웃박스는 그래서 고정 자리로 갔다(OutboxPath).
//  2. 그다음에는 "캐시"가 한 부류인 줄 알았는데, 그 안에 **응답 캐시**와 런처의
//     **바이너리 캐시**가 섞여 있었다. 둘 다 재생성은 가능하다. 그런데 바이너리는 exec 되고
//     나면 자기가 어느 판인지 안 말하고 답해서, 두 벌이 갈리면 하나가 최신인 척하는 옛
//     코드가 된다(2026-08-06 실측: 두 자리의 빌드 시각이 55분 어긋난 창에서 한 응답의
//     서버 축과 렌더 축이 갈렸다). 그래서 축이 둘로 넓어졌고(① 재생성 가능한가
//     ② 갈린 사본이 각자 옳은가 — 전문은 env.go 의 OutboxPath 주석), 바이너리 캐시도
//     고정 자리로 갔다(BinCacheDir). **이제 이 레포에서 "캐시"라고만 적으면 안 된다** —
//     응답 캐시인지 바이너리 캐시인지 밝혀 적는다.
//
// 서버 주소는 정반대 요구다: **같은 머신이면 어느 채널에서 물어도 같아야 한다.**
//
// 이 판정은 새 규칙이 아니다. MachineIDPath 가 이미 같은 이유로 같은 결론을 냈고
// (env.go 의 그 주석 — 파일이 두 벌이 돼 한 머신이 서로 다른 id 를 갖게 된 사고가 실재한다),
// 여기서는 그 규칙을 따를 뿐이다.

// Config 는 파일에 저장되는 것 전부다. **작게 유지한다** —
// 파생 가능한 값(역할·도달성)은 여기 두지 않는다. 저장하면 의도와 현실이 갈린다.
type Config struct {
	URL   string `json:"url,omitempty"`
	Token string `json:"token,omitempty"`
}

// Endpoint 는 이번 실행이 실제로 쓸 좌표와 **그것을 어디서 읽었는지**다.
//
// 출처를 함께 나르는 이유: 값이 예상과 다를 때 "왜 저 주소인가"에 답할 자리가 필요하다.
// fd doctor 가 이것을 찍는다 — machineSrc 가 이미 그 선례다.
type Endpoint struct {
	URL         string
	Token       string
	URLSource   string
	TokenSource string
	Warn        string // 설정을 못 읽었거나 권한이 이상할 때. 비어 있을 수 있다
}

// ConfigPath 는 설정 파일 자리를 고른다. 순수 함수다.
//
// FD_STATE_DIR 만 존중한다 — 그것은 채널이 아니라 **사람이** 명시 지정하는 축이라
// 프로세스마다 갈리지 않고, 시험이 진짜 홈을 건드리지 않게 막는 유일한 자리이기도 하다
// (MachineIDPath 가 같은 예외를 같은 이유로 둔다).
func ConfigPath(get func(string) (string, bool), home string) (path, source string) {
	if v, ok := get("FD_STATE_DIR"); ok && strings.TrimSpace(v) != "" {
		return filepath.Join(filepath.Clean(strings.TrimSpace(v)), "config.json"), "FD_STATE_DIR (명시 지정)"
	}
	if strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".flightdeck", "config.json"), "~/.flightdeck — 채널 환경과 무관한 고정 자리"
	}
	return filepath.Join(os.TempDir(), "flightdeck", "config.json"),
		"임시 디렉토리 — HOME 이 없다. 재부팅하면 설정이 사라진다"
}

// LoadConfig 는 설정 하나를 읽는다. **없는 것은 오류가 아니다**(아직 설정 안 한 것뿐이다).
//
// ★ 깨진 파일에 죽지 않는다. 오타 하나로 모든 fd 호출이 멈추면 그쪽이 더 나쁘다.
// 다만 **조용히 넘어가지도 않는다** — 사유를 warn 으로 올려 fd doctor 가 찍게 한다.
// (값, 출처, 경고) 셋을 내는 이 모양은 MachineID 가 이미 쓰는 것이다.
func LoadConfig(path string) (cfg Config, found bool, warn string) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, false, ""
		}
		return Config{}, false, fmt.Sprintf("설정 파일을 못 읽었다(%s): %s", clip(path, 200), clip(err.Error(), 200))
	}
	if fi, serr := os.Stat(path); serr == nil {
		// 토큰이 들어 있는 파일이다. 넓은 권한은 경고하되 **거절하지는 않는다** —
		// 못 읽으면 도구가 멈추고, 멈추는 쪽이 더 나쁘다.
		if perm := fi.Mode().Perm(); perm&0o077 != 0 {
			warn = fmt.Sprintf("설정 파일 권한이 %04o 다(%s) — 토큰이 들어 있으니 0600 으로 좁혀라: chmod 600 %s",
				perm, clip(path, 200), clip(path, 200))
		}
	}
	if uerr := json.Unmarshal(b, &cfg); uerr != nil {
		return Config{}, false, fmt.Sprintf("설정 파일 config.json 을 해석하지 못했다(%s): %s — "+
			"이번 실행은 환경변수와 기본값으로 간다", clip(path, 200), clip(uerr.Error(), 200))
	}
	cfg.URL = strings.TrimRight(strings.TrimSpace(cfg.URL), "/")
	cfg.Token = strings.TrimSpace(cfg.Token)
	return cfg, true, warn
}

// SaveConfig 는 설정을 원자적으로 쓴다. 0600 이다(토큰이 들어 있다).
//
// 원자 교체인 이유: 다른 채널이 같은 순간 이 파일을 읽고 있을 수 있다.
// 부분 기록된 JSON 은 다음 읽기에서 "해석 실패"가 되고, 그러면 방금 설정한 사람이
// 자기가 무엇을 잘못했는지 모른 채 경고를 본다.
func SaveConfig(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("설정 디렉토리를 못 만들었다(%s): %w", clip(filepath.Dir(path), 200), err)
	}
	buf, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("설정 직렬화 실패: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(buf, '\n'), 0o600); err != nil {
		return fmt.Errorf("설정을 못 썼다(%s): %w", clip(tmp, 200), err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("설정을 제자리에 못 놨다(%s): %w", clip(path, 200), err)
	}
	return nil
}

// ResolveEndpoint 는 이번 실행이 쓸 주소·토큰을 정한다.
//
// 우선순위: **환경변수 > 설정 파일 > 기본값.**
// 환경변수가 이기는 것이 이 레포의 기존 규율이다 — 사람이 그 자리에서 명시 지정한 축은
// 저장된 값을 이긴다(FD_STATE_DIR·FD_PROJECT 가 같은 규칙을 쓴다).
//
// ★ 축마다 **따로** 판정한다. URL 만 환경으로 덮고 토큰은 파일에서 오는 조합이 실제로 있다
// (원격 서버를 잠깐 가리켜 보는 경우). 둘을 한 덩어리로 접으면 그때 토큰이 조용히 사라진다.
func ResolveEndpoint(get func(string) (string, bool), home string) Endpoint {
	path, _ := ConfigPath(get, home)
	cfg, found, warn := LoadConfig(path)

	ep := Endpoint{URL: DefaultURL, URLSource: "기본값 " + DefaultURL, Warn: warn}
	if found && cfg.URL != "" {
		ep.URL, ep.URLSource = cfg.URL, "config.json ("+clip(path, 200)+")"
	}
	if v, ok := get("FD_URL"); ok && strings.TrimSpace(v) != "" {
		ep.URL, ep.URLSource = strings.TrimRight(strings.TrimSpace(v), "/"), "FD_URL 환경변수"
	}

	ep.TokenSource = "없음 — 인증이 꺼져 있거나 루프백 면제로 도는 중이다"
	if found && cfg.Token != "" {
		ep.Token, ep.TokenSource = cfg.Token, "config.json ("+clip(path, 200)+")"
	}
	if v, ok := get("FD_TOKEN"); ok && strings.TrimSpace(v) != "" {
		ep.Token, ep.TokenSource = strings.TrimSpace(v), "FD_TOKEN 환경변수"
	}
	return ep
}
