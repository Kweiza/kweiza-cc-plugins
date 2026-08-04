package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// 좌표 — 상태 디렉토리 · 머신 정체 · 프로젝트.
//
// 여기 있는 판정은 전부 순수 함수로 빼고 환경 조회를 인자로 받는다.
// os.Getenv 를 본문에 박으면 시험이 전역 환경을 흔들어야 하고, 그러면 병렬 시험이 서로를 깬다
// (service.ProbePlatform 과 같은 규율).

// StateDir 는 열화 상태(캐시·아웃박스)를 두는 자리다.
//
// ★ ${CLAUDE_PLUGIN_ROOT} 에 두지 않는다. 그 경로에는 플러그인 **버전이 들어가서**
// 갱신될 때마다 자리가 바뀌고, 그러면 오프라인에 쌓아 둔 판단이 갱신 한 번에 사라진다(설계 §7).
type StateDir struct {
	Path   string
	Source string // 어디서 골랐나. **항상 채운다** — 사유가 없으면 "왜 여기냐"에 답할 자리가 없다
}

// ResolveStateDir 는 상태 디렉토리를 고른다. 순수 함수다.
//
// 우선순위와 **그것을 고른 사유**를 함께 낸다. 마지막 폴백(임시 디렉토리)은 값은 나오지만
// 재기동하면 사라지므로, 그 사실을 Source 에 적어 조용히 잃지 않게 한다.
func ResolveStateDir(get func(string) (string, bool), home string) StateDir {
	pick := func(key, why string, sub ...string) (StateDir, bool) {
		v, ok := get(key)
		if !ok || strings.TrimSpace(v) == "" {
			return StateDir{}, false
		}
		parts := append([]string{filepath.Clean(v)}, sub...)
		return StateDir{Path: filepath.Join(parts...), Source: why}, true
	}
	if sd, ok := pick("FD_STATE_DIR", "FD_STATE_DIR (명시 지정)"); ok {
		return sd
	}
	if sd, ok := pick("CLAUDE_PLUGIN_DATA", "CLAUDE_PLUGIN_DATA", "flightdeck"); ok {
		return sd
	}
	if sd, ok := pick("XDG_STATE_HOME", "XDG_STATE_HOME — CLAUDE_PLUGIN_DATA 가 없다", "flightdeck"); ok {
		return sd
	}
	if strings.TrimSpace(home) != "" {
		return StateDir{
			Path:   filepath.Join(home, ".local", "state", "flightdeck"),
			Source: "~/.local/state — CLAUDE_PLUGIN_DATA 도 XDG_STATE_HOME 도 없다",
		}
	}
	return StateDir{
		Path: filepath.Join(os.TempDir(), "flightdeck"),
		Source: "임시 디렉토리 — 홈도 CLAUDE_PLUGIN_DATA 도 없다. " +
			"재부팅하면 캐시와 **아직 못 보낸 판단**이 사라진다",
	}
}

func (s StateDir) sub(name string) string { return filepath.Join(s.Path, name) }

// MachineIDPath 는 machine-id 파일 자리를 고른다. 순수 함수다.
//
// ★ **상태 디렉토리를 일부러 안 쓴다.** 그 자리는 ResolveStateDir 이 CLAUDE_PLUGIN_DATA·
// XDG_STATE_HOME 으로 고르는데, 그 둘은 **채널마다 있고 없다** — Claude Code 가 훅·MCP
// 프로세스에는 넣어 주고 사용자 셸에는 안 넣는다. 그래서 machine-id 파일이 두 벌이 됐고
// 같은 머신이 서로 다른 id 를 갖게 됐다(실물 확인: 파일 두 벌, 값 두 개).
// 그러면 세션 정체 3중키의 첫 축이 갈려 **한 Claude 세션이 보드에 카드 세 장**으로 뜬다.
//
// 두 축은 요구가 정반대다. 상태 디렉토리는 "열화 상태(캐시·아웃박스)가 플러그인 갱신을
// 넘어 살아남는가"라 환경 의존이 **설계 의도**다(설계 §7). machine id 는 "같은 머신이면
// 같은가"라 환경 의존이 **곧 결함**이다. 둘을 한 디렉토리에 뭉갠 것이 이 사고의 전부다.
//
// FD_STATE_DIR 만 남긴다 — 채널이 아니라 **사람이** 명시 지정하는 축이라 프로세스마다
// 갈리지 않고, 시험이 진짜 홈에 machine-id 를 적지 않게 막는 유일한 자리이기도 하다.
//
// 옛 두 자리의 값을 **물려받지 않는다.** 물려받으려면 후보 목록을 훑어야 하는데
// 그 목록이 다시 채널 환경에서 오므로, 채택 순서가 채널마다 갈려 같은 결함이 되살아난다.
// 이 머신의 id 가 한 번 바뀌는 대신 다시는 안 갈린다.
func MachineIDPath(get func(string) (string, bool), home string) (path, source string) {
	if v, ok := get("FD_STATE_DIR"); ok && strings.TrimSpace(v) != "" {
		return filepath.Join(filepath.Clean(strings.TrimSpace(v)), "machine-id"), "FD_STATE_DIR (명시 지정)"
	}
	if strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".flightdeck", "machine-id"), "~/.flightdeck — 채널 환경과 무관한 고정 자리"
	}
	return filepath.Join(os.TempDir(), "flightdeck", "machine-id"),
		"임시 디렉토리 — HOME 이 없다. 재부팅하면 머신 id 가 바뀌어 세션이 갈린다"
}

// OutboxPath 는 아웃박스와 격리 파일을 두는 디렉토리다. 순수 함수다.
//
// ★ **상태 디렉토리를 일부러 안 쓴다** — MachineIDPath·ConfigPath 와 **같은 규칙의
// 셋째 적용**이다. 새 규칙이 아니다.
//
// 앞선 두 판정은 "같은 머신이면 같아야 하는 값"을 갈린 자리에 두면 안 된다고 했다.
// 아웃박스가 그 부류인 줄 몰랐던 것이 이 사고다. config.go 의 옛 주석은
// "열화 상태(캐시·아웃박스)는 채널마다 따로여도 된다"고 적었는데 **그 주장이 반증됐다.**
//
// 가르는 축은 "열화 상태인가"가 아니라 **"재생성 가능한가"**다:
//
//   - 캐시 — 재생성 가능하다. 채널마다 갈려도 되고, ${CLAUDE_PLUGIN_ROOT} 를 피하라는
//     설계 §7 의 원래 논거가 그대로 유효하다. 그래서 StateDir 에 남는다.
//   - 아웃박스·격리 — 설계 §7 이 "재생성 불가한 유일한 자산"이라 부른 것을 담는다.
//     갈린 자리에 두면 셸에서 쌓인 판단을 훅·MCP 가 영영 못 보낸다(실측: 8/3 판단 하나가
//     그렇게 셸 쪽에 갇혀 있었다).
//
// 설계 §7 이 이것을 막지 않았던 이유도 적어 둔다: §7 은 `${CLAUDE_PLUGIN_ROOT} 는
// 업데이트마다 경로가 바뀐다` 를 근거로 **CLAUDE_PLUGIN_ROOT 를 피하라**고 했을 뿐,
// 채널 분기 자체를 방어한 적이 없다.
//
// FD_STATE_DIR 만 예외로 남긴다 — 채널이 아니라 **사람이** 명시 지정하는 축이라
// 프로세스마다 갈리지 않고, 시험이 진짜 홈의 판단을 건드리지 않게 막는 유일한 자리다.
func OutboxPath(get func(string) (string, bool), home string) (dir, source string) {
	if v, ok := get("FD_STATE_DIR"); ok && strings.TrimSpace(v) != "" {
		return filepath.Join(filepath.Clean(strings.TrimSpace(v)), "outbox"), "FD_STATE_DIR (명시 지정)"
	}
	if strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".flightdeck", "outbox"), "~/.flightdeck — 채널 환경과 무관한 고정 자리"
	}
	return filepath.Join(os.TempDir(), "flightdeck", "outbox"),
		"임시 디렉토리 — HOME 이 없다. 재부팅하면 **아직 못 보낸 판단**이 사라진다"
}

// MachineID 는 이 머신의 안정 id 다. 세션 정체 3중키의 첫 축이라 재기동해도, 그리고
// **어느 채널에서 불러도** 같아야 한다.
//
// 없으면 만들어 적는다. **적기에 실패해도 값을 낸다** — 조정이 파일 쓰기 실패로 죽으면
// 이 도구의 존재 이유가 사라진다. 다만 사유를 함께 돌려주므로 호출부가 침묵하지 않는다.
// source 는 어느 자리에서 읽었는지다 — fd doctor 가 그것을 찍어야 값이 갈렸을 때
// 사람이 원인에 도달할 수 있다(이번에는 그 줄이 없어 /proc 을 뒤져야 했다).
func MachineID(get func(string) (string, bool), home string) (id, source, warn string) {
	path, source := MachineIDPath(get, home)
	if b, err := os.ReadFile(path); err == nil {
		if v := strings.TrimSpace(string(b)); v != "" {
			return v, source, ""
		}
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// 난수가 없으면 시각으로라도 만든다. 유일성은 떨어지지만 값이 없는 것보다 낫다.
		return fmt.Sprintf("m-%d", time.Now().UnixNano()), source,
			"난수를 못 읽어 시각 기반 id 를 만들었다: " + err.Error()
	}
	id = "m-" + hex.EncodeToString(buf)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return id, source, "machine-id 디렉토리를 못 만들어 이 실행에서만 유효하다: " + err.Error()
	}
	if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
		return id, source, "machine-id 를 못 적어 다음 실행에 다른 값이 된다: " + err.Error()
	}
	return id, source, ""
}

// ProjectCoord 는 이 실행이 어느 프로젝트의 어느 워크트리인지다.
type ProjectCoord struct {
	ID       string // 프로젝트 id. 서버의 좌표계
	Path     string // **주 저장소** 경로. 서버가 worktree list 를 이 경로로 돌린다
	Worktree string // 이 세션의 작업 트리 절대경로
	Detail   string // 어떻게 알아냈나 · 무엇을 못 알아냈나. 항상 채운다
}

// MainRepoRoot 는 `git rev-parse --git-common-dir` 결과에서 주 저장소 경로를 낸다. 순수 함수다.
//
// ★ 워크트리에서 부르면 --git-dir 는 `<주저장소>/.git/worktrees/<이름>` 을 주지만
// --git-common-dir 는 `<주저장소>/.git` 을 준다. 서버는 주 저장소로 worktree list 를 돌려야
// 전 세션의 브랜치를 한 번에 얻으므로(service.worktreeIndex), 링크된 워크트리 경로를 주면
// **다른 세션들이 통째로 안 보인다.**
func MainRepoRoot(gitCommonDir string) string {
	p := strings.TrimSpace(gitCommonDir)
	if p == "" {
		return ""
	}
	p = filepath.Clean(p)
	if filepath.Base(p) == ".git" {
		return filepath.Dir(p)
	}
	return p // bare 저장소는 그 자체가 루트다
}

// ProjectIDFromPath 는 경로에서 기본 프로젝트 id 를 만든다. 순수 함수다.
//
// 항목 id 와 달리 이 값은 셸·git ref 로 나가지 않지만, URL 질의 문자열과 파일 이름으로는
// 나가므로 경계 문자를 걷어낸다.
func ProjectIDFromPath(p string) string {
	base := filepath.Base(filepath.Clean(strings.TrimSpace(p)))
	if base == "." || base == string(filepath.Separator) || base == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-.")
}

// resolveProject 는 cwd 에서 프로젝트 좌표를 읽는다.
//
// git 호출이 실패해도 **좌표를 낸다** — 그 경우 주 저장소 경로를 워크트리로 두고
// 그 사실을 Detail 에 적는다. 침묵하면 "링크된 워크트리라 남이 안 보이는 것"과
// "정말 아무도 없는 것"이 구분되지 않는다.
func resolveProject(get func(string) (string, bool), cwd string) ProjectCoord {
	wt := cwd
	if v, ok := get("FD_WORKTREE"); ok && strings.TrimSpace(v) != "" {
		wt = v
	} else if v, ok := get("CLAUDE_PROJECT_DIR"); ok && strings.TrimSpace(v) != "" && strings.TrimSpace(cwd) == "" {
		wt = v
	}
	wt = filepath.Clean(wt)

	c := ProjectCoord{Worktree: wt, Path: wt, Detail: "git 을 못 읽어 워크트리를 주 저장소로 뒀다"}
	if out, err := gitOut(wt, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil {
		if root := MainRepoRoot(out); root != "" {
			c.Path = root
			c.Detail = "git rev-parse --git-common-dir 로 주 저장소를 찾았다"
		}
	} else {
		c.Detail = "git rev-parse 실패(" + clip(err.Error(), 200) + ") — 워크트리를 주 저장소로 뒀다"
	}

	c.ID = ProjectIDFromPath(c.Path)
	if v, ok := get("FD_PROJECT"); ok && strings.TrimSpace(v) != "" {
		c.ID = strings.TrimSpace(v)
		c.Detail += " · 프로젝트 id 는 FD_PROJECT 가 이겼다"
	}
	return c
}

// gitOut 은 git 한 줄을 읽는다. 이 클라이언트가 git 을 부르는 유일한 자리다.
func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var errb strings.Builder
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, clip(errb.String(), 200))
	}
	return strings.TrimSpace(string(out)), nil
}

// clip 은 외부에서 온 문자열을 자르고 제어문자를 걷어낸다(로그 주입 방지).
func clip(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n]) + "…"
}

// humanAge 는 경과를 한국어 한 마디로 낸다. 순수 함수다.
//
// 배너 문구가 **시험이 단정하는 소비자 좌표계**라 여기 모은다 —
// 여러 자리에서 각자 포맷하면 문구가 갈라지고, 그러면 시험이 사본을 단정하게 된다.
func humanAge(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d초 전", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d분 전", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d시간 %d분 전", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%d일 전", int(d.Hours()/24))
	}
}
