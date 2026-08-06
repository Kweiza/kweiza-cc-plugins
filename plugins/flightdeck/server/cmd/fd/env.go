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

	"github.com/kweiza/flightdeck/internal/window"
)

// 좌표 — 상태 디렉토리 · 머신 정체 · 프로젝트.
//
// 여기 있는 판정은 전부 순수 함수로 빼고 환경 조회를 인자로 받는다.
// os.Getenv 를 본문에 박으면 시험이 전역 환경을 흔들어야 하고, 그러면 병렬 시험이 서로를 깬다
// (service.ProbePlatform 과 같은 규율).

// StateDir 는 **재생성 가능한** 열화 상태(**응답 캐시**)를 두는 자리다.
//
// ★ "캐시"라고만 적지 않는다. 이 낱말이 이제 두 가지를 가리키기 때문이다 — 서버
// **응답 캐시**(여기)와 런처의 **바이너리 캐시**(BinCacheDir). 둘은 재생성 가능하다는
// 점은 같은데 자리 규칙이 반대다. 이 사다리에 남는 것은 응답 캐시뿐이고, 왜 그것만
// 남는지는 OutboxPath 주석의 축 목록에 전문이 있다.
//
// ★ 아웃박스는 여기 없다. 그것은 재생성 불가한 판단을 담아서 채널마다 갈리면 안 되고,
// 그래서 OutboxPath 가 고정 자리를 준다 — 그 주석에 판정 전문이 있다.
//
// ★ 빌드 산출물(런처가 짓는 정적 바이너리)도 여기 없다. 재생성은 가능하지만 exec 되고
// 나면 자기가 어느 판인지 안 말하고 답해서, 두 채널의 사본이 갈리면 하나가 **최신인 척하는
// 옛 코드**가 된다 — BinCacheDir 이 그래서 고정 자리를 준다.
//
// ★ ${CLAUDE_PLUGIN_ROOT} 에 두지 않는다. 그 경로에는 플러그인 **버전이 들어가서**
// 갱신될 때마다 자리가 바뀌고, 그러면 쌓아 둔 응답 캐시가 갱신 한 번에 사라진다(설계 §7).
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
			"재부팅하면 응답 캐시가 사라진다(잃어도 다시 만들면 된다)",
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
// 두 축은 요구가 정반대다. 상태 디렉토리는 "**재생성 가능한** 열화 상태(응답 캐시)가
// 플러그인 갱신을 넘어 살아남는가"라 환경 의존이 **설계 의도**다(설계 §7). machine id 는
// "같은 머신이면 같은가"라 환경 의존이 **곧 결함**이다. 둘을 한 디렉토리에 뭉갠 것이
// 이 사고의 전부다. (아웃박스도 같은 이유로 나중에 떨어져 나갔다 — OutboxPath 를 보라.)
//
// ★ 소급 표기: 여기 쓴 "같은 머신이면 같은가"가 실은 **축 ②(갈린 사본이 각자 옳은가)의
// 첫 적용**이었다. 당시에는 축이 "재생성 가능한가" 하나뿐이라고 적었는데, machine-id 는
// crypto/rand 로 언제든 다시 만들 수 있어 그 축 하나로는 **왜 고정 자리인지 설명이 안 된다**
// — 실제로 이 판정을 낸 것은 "두 벌이 갈리면 하나는 거짓"이라는 다른 축이었다.
// 그 축에 이름을 붙인 것은 넷째 적용(BinCacheDir)에 와서다. 목록은 OutboxPath 주석에 있다.
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
// 가르는 축은 "열화 상태인가"가 아니라 **둘**이다 — ① **재생성 가능한가**
// ② **갈린 사본이 각자 옳은가**. **둘 다 "예"여야** 채널 사다리(StateDir)에 남는다.
//
// (②는 새로 발명한 축이 아니라 **이미 쓰고 있던 축의 이름**이다. MachineIDPath 가
// "같은 머신이면 같은가"로 내린 판정이 정확히 ②였다 — machine-id 는 crypto/rand 로
// 언제든 다시 만들 수 있어 ①은 통과하는데도 고정 자리로 갔으니, ① 하나로는 그 판정을
// 설명할 수 없다. 넷째 적용(BinCacheDir)에 와서 이름을 붙였다.)
//
//   - 응답 캐시 — ①통과: 재생성 가능하다. ②통과: CacheEntry 가 받은 시각을 **값과 같은
//     파일에** 달고 다니고(cache.go 의 At 필드 — 파일 mtime 에 안 기댄다), 읽기가
//     StaleBanner 로 "마지막 접속 16:10 · 37분 전"을 함께 찍는다. 그래서 두 채널의 사본이
//     달라도 **각자 자기 시점의 참**이고, 낡은 쪽이 최신인 척할 수가 없다. 그래서 StateDir
//     에 남는다 — ${CLAUDE_PLUGIN_ROOT} 를 피하라는 설계 §7 의 원래 논거도 그대로 유효하다.
//   - 바이너리 캐시 — ①통과, **②탈락**. exec 되고 나면 자기가 어느 판인지 안 말하고 답한다.
//     두 벌이 다르면 하나는 **최신인 척하는 옛 코드**다(2026-08-06 실측: 같은 소스가 두 자리에
//     지어져 빌드 시각이 16:10:44 ↔ 17:05:29 로 55분 어긋났고, 그 창에서 한 응답의 서버 축과
//     렌더 축이 서로 다른 판을 봤다). 그래서 BinCacheDir 이 고정 자리를 준다.
//   - 아웃박스·격리 — **①에서 이미 탈락**한다. 설계 §7 이 "재생성 불가한 유일한 자산"이라
//     부른 것을 담는다. 갈린 자리에 두면 셸에서 쌓인 판단을 훅·MCP 가 영영 못 보낸다(실측:
//     8/3 판단 하나가 그렇게 셸 쪽에 갇혀 있었다).
//
// 설계 §7 이 이것을 막지 않았던 이유도 적어 둔다: §7 은 `${CLAUDE_PLUGIN_ROOT} 는
// 업데이트마다 경로가 바뀐다` 를 근거로 **CLAUDE_PLUGIN_ROOT 를 피하라**고 했을 뿐,
// 채널 분기 자체를 방어한 적이 없다.
//
// ★ 다만 **이 알리바이는 캐시 부류에는 안 선다.** 아웃박스·machine-id·설정에 대해 §7 은
// 침묵했고(그래서 앞선 셋은 §7 의 구멍을 메우는 것이었다), 캐시에 대해서는 "채널마다
// 갈려도 된다"고 **명시로 허가**했다. 그러니 바이너리 캐시를 사다리에서 떼는 것은 침묵을
// 메우는 것이 아니라 §7 의 **문장을 뒤집는 개정**이다 — 그 사실을 숨기지 않는다.
// 뒤집는 근거는 위 목록의 ② 다: §7 은 '캐시'를 한 부류로 봤는데 그 안에 ②를 통과하는 것
// (응답 캐시)과 탈락하는 것(바이너리)이 섞여 있었다. 통과하는 쪽은 **그대로 둔다** —
// 응답 캐시까지 옮기면 "경로가 바뀌면 쌓아 둔 캐시가 갱신 한 번에 사라진다"는 §7 의
// 살아 있는 논거를 실제로 위반한다.
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

// LegacyOutboxDirs 는 재생이 **함께 돌아 줘야 하는** 다른 아웃박스 자리다. 순수 함수다.
//
// 아웃박스가 채널마다 갈려 있던 시절의 자리와, 이 실행이 목표를 바꿨을 때 뒤에 남는 자리다.
//
// ★ **파일을 옮기지 않는다.** 재생이 각 큐를 제자리에서 돌려 **전송으로** 비우고,
// 마지막 줄까지 나가면 keep() 이 그 파일을 지운다 — 이미 있는 동작이다.
// (앞선 판에서는 os.Rename 청구로 고정 자리에 흡수하려 했는데, 그 설계가 반증됐다.
// 스펙 §4 "왜 옮기지 않기로 했나"에 재현 결과가 있다. 되살리지 마라.)
//
// ★ **~/.claude/plugins/data/*/flightdeck 를 glob 하지 않는다.** 그 경로에는 플러그인
// 버전과 마켓 이름이 들어가고, 설계 §13 이 "버전이 경로에 들어가므로 그 경로를 어디에도
// 저장하지 않는다"고 판정했다. 추측해 박으면 마켓 이름이 바뀌는 날 조용히 빗나간다.
//
// 그래서 수렴은 이렇게 일어난다: 훅·MCP 채널은 CLAUDE_PLUGIN_DATA 가 있으니
// SessionStart 마다 제 자리를 비우고, 셸 채널은 제 자리를 비운다.
// **정직한 구멍**: 어떤 채널이 이 변경 뒤 fd 를 한 번도 안 돌리면 그 자리는 영영
// 안 비워진다. 그 사실은 runDoctor 가 말로 찍는다 — 안 잰 축을 잰 척하지 않는다(§13).
//
// ★ **임시 디렉토리(<tmp>/flightdeck/outbox)는 후보에서 뺀다.** 이 자리는 세 번
// 갈렸다(스펙 §5) — 안 적으면 다음 사람이 같은 왕복을 또 한다.
//
//  1. (뺐다) "그 갈래가 걸리는 조건(HOME 이 없다)에서는 목표도 같은 자리라 어차피
//     걸러진다"고 적었다. **틀렸다.** 이 목록이 판정하는 것은 **과거 실행의** 환경이고
//     목표를 정하는 것은 **지금** 환경이라, 걸러진다는 보장이 없다 — HOME 없이(데몬·
//     컨테이너 진입점) 돌아 tmp 에 쌓은 머신이 나중에 HOME 을 갖게 되면 그 판단이
//     영영 안 나간다.
//  2. (넣었다) "공용 머신에서 남의 것을 건드리는 위험은 파일 권한이 막는다 — 아웃박스
//     파일은 0600 이라 읽기가 실패한다"고 적었다. **이 근거도 틀렸다.** 0600 은
//     *내가 남의 보호된 파일을 읽는* 방향만 막는다. 반대 방향 — 남이
//     `<tmp>/flightdeck/outbox/pending.jsonl` 을 **0644 로 심어 두는** 것 — 은
//     안 막는다. `/tmp` 는 부모가 world-writable 이라 아무나 그 디렉토리를 먼저
//     만들 수 있다.
//  3. (다시 뺐다 — 지금) **근거가 또 바뀌었다.** 과제 5부터 이 목록의 각 자리는
//     "읽어서 사용자의 토큰으로 서버에 POST 하는" 자리가 됐다. 그러면 심어 둔 줄이
//     그대로 원장에 들어가고, 판단은 추가 전용이라(트리거가 UPDATE·DELETE 를 막는다)
//     **회수할 방법이 없다.** 나머지 네 후보는 `$HOME` 아래거나 사용자 자신의
//     프로세스만 세팅하는 환경변수(CLAUDE_PLUGIN_DATA·XDG_STATE_HOME)에서 오므로
//     종류가 다르다 — 부모 디렉토리가 world-writable 인 것은 tmp 뿐이다.
//     잃는 것은 "HOME 없이 돌아 tmp 에 쌓은 판단"인데, 그러려면 fd 가 HOME 없이
//     돌아야 하고 이 제품의 채널(훅·MCP·셸) 셋 다 HOME 이 있다 — 가설적 손실과
//     구체적 주입을 견주면 후자가 무겁다. 그 갈래가 실제로 생기는 날(데몬·컨테이너
//     진입점) tmp 를 다시 넣되, 그때는 **디렉토리 소유자 검사를 함께 단다** —
//     넣는 것만으로는 다시 안 된다는 것이 이 기록의 요점이다.
func LegacyOutboxDirs(get func(string) (string, bool), home, target string) []string {
	var out []string
	tgt := filepath.Clean(target)
	add := func(p string) {
		if strings.TrimSpace(p) == "" {
			return
		}
		p = filepath.Clean(p)
		if p == tgt {
			return // 목표와 같은 자리는 재생이 이미 돈다
		}
		for _, x := range out {
			if x == p {
				return
			}
		}
		out = append(out, p)
	}
	if v, ok := get("CLAUDE_PLUGIN_DATA"); ok && strings.TrimSpace(v) != "" {
		add(filepath.Join(filepath.Clean(strings.TrimSpace(v)), "flightdeck", "outbox"))
	}
	if v, ok := get("XDG_STATE_HOME"); ok && strings.TrimSpace(v) != "" {
		add(filepath.Join(filepath.Clean(strings.TrimSpace(v)), "flightdeck", "outbox"))
	}
	if strings.TrimSpace(home) != "" {
		add(filepath.Join(home, ".local", "state", "flightdeck", "outbox"))
		// ★ 고정 자리 자신. FD_STATE_DIR 를 새로 켜면 목표가 그쪽으로 옮겨 가는데,
		// 이 줄이 없으면 그때까지 여기 쌓인 판단이 조용히 안 보이게 된다.
		add(filepath.Join(home, ".flightdeck", "outbox"))
	}
	return out
}

// BinCacheDir 는 셸 런처가 빌드한 **정적 바이너리를 두는 디렉토리**다. 순수 함수다.
//
// ★ **상태 디렉토리를 일부러 안 쓴다** — MachineIDPath·ConfigPath·OutboxPath 와 **같은
// 규칙의 넷째 적용**이다. 새 규칙이 아니다. **앞선 셋이 한 축에서 걸린 것은 아니다**:
// ①(재생성 가능한가)에서 걸린 것은 아웃박스뿐이고, machine-id 는 crypto/rand 로 언제든
// 다시 만들 수 있어 ①을 **통과한 뒤** ②(갈린 사본이 각자 옳은가)에서 걸렸다(MachineIDPath
// 주석의 소급 표기). config.json 도 ②가 결정적이다 — "같은 머신이면 어느 채널에서 물어도
// 같아야 한다"(config.go). 바이너리는 machine-id 와 **같은 자리에서** 걸린다: ①통과, ②탈락.
// 그래서 이것은 ②의 첫 적용이 아니라 **셋째 적용**이고, 그 사실이 "②는 사후에 발명한 축이
// 아니다"의 근거 자체다(설계 §7 의 "왜 축이 둘인가"). 두 축의 전문은 OutboxPath 주석에 있다.
//
// ②를 탈락하는 이유는 하나다: **exec 되고 나면 자기가 어느 판인지 안 말하고 답한다.**
// 응답 캐시는 받은 시각을 값과 같은 파일에 달고 다녀서(cache.go 의 CacheEntry.At) 두 채널의
// 사본이 달라도 각자 자기 시점의 참이지만, 바이너리는 두 벌이 다르면 하나가 **최신인 척하는
// 옛 코드**다. 2026-08-06 실측: 같은 소스가 CLAUDE_PLUGIN_DATA 자리와 XDG_STATE_HOME 자리에
// 두 번 지어져 빌드 시각이 16:10:44 ↔ 17:05:29 로 55분 어긋났고, 그 창에서 **한 응답의
// 서버 축과 렌더 축이 서로 다른 판을 봤다**. 그 두 환경변수는 채널마다 있고 없다 — 훅·MCP
// 프로세스에는 Claude Code 가 넣어 주고 Bash 도구·확장 호스트에는 없다(실측).
//
// ★ **`~/.flightdeck` 이 아니다.** 앞선 세 형제와 뿌리가 갈리는 유일한 자리라 이유를 적는다.
// compose.yaml 이 그 자리를 컨테이너에 `/data` 로 **쓰기 가능**하게 물리고, 재생성 불가한
// fd.db 와 판단 백업이 거기 산다. 실행 파일을 그 아래 두면 ⑴ 네트워크에 열린 컨테이너가
// **호스트가 exec 하는 파일**을 쓸 수 있게 되는 seam 이 새로 생기고 ⑵ 백업이 22MB×N 을
// 함께 뜬다. 마운트를 좁히는 쪽으로 풀려면 compose·시험·fd.db 이관까지 가야 하는데,
// 부모를 바꾸면 그 전부가 0원이다 — 문제를 푸는 대신 **문제를 안 만든다**.
//
// ★ 뿌리의 **모드는 물려받는 것이 아니라 짓는 것이다.** 앞선 판은 "실측 `~/.cache` 는 0700 이라
// `~/.local/state` 가 우연히 주던 보호막이 따라온다"고 적었는데 **그 근거는 갈래를 안 셌다**:
// 0700 은 이 머신에 **이미 있던** 디렉토리의 성질이지 자리의 성질이 아니다. `~/.cache` 가 아직
// 없는 머신(새 컨테이너 이미지·CI 러너·갓 만든 계정)에서는 런처의 mkdir 이 첫 생성자이고, 맨
// `mkdir -p` 는 ambient umask 를 타 umask 002 아래 **0775** 가 된다(실측 재현: .cache·flightdeck·
// bin·산출물 넷 다 775). 그것은 방금 거절한 `~/.flightdeck`(MachineID 의 MkdirAll 이 0o755 로
// 짓는다)보다 **오히려 넓다.** 그래서 모드를 정하는 것은 이 함수가 아니라 **런처의 mkdir** 이다
// (bin/fd) — 여기는 자리만 답하고, 보호막을 우연에 안 맡긴다는 판정은 그쪽에 산다.
//
// "채널 무관"은 두 뿌리가 똑같이 만족한다. **뿌리를 가른 것은 각 항목의 ① 판정이 아니다** —
// machine-id 도 비콘도 ①을 통과하고서 `~/.flightdeck` 에 산다(window/dir.go 가 같은 사다리를
// 같은 이유로 쓴다). 가른 것은 **그 자리에 사는 자산의 등급과 산출물의 부피**다: 재생성 불가한
// fd.db 와 판단 백업 옆에 22MB×N 짜리 실행 파일을 안 쌓는다.
//
// ★ **HOME 이 없으면 짓지 않는다 — 빈 문자열을 낸다.** 앞선 세 형제는 임시 디렉토리로
// 떨어지는데 이것만 안 떨어진다. 부모가 world-writable 인 자리는 **남이 심어 둔 것을 내가
// exec 하는 길**이고, 잃는 것(HOME 없는 실행에서 캐시가 없어 매번 다시 빌드한다)보다
// 무겁다. LegacyOutboxDirs 가 tmp 를 후보에서 뺀 것과 **같은 축**이며(그 주석의 3번 기록),
// 실행 파일은 그쪽보다 무겁다 — 심어진 줄은 전송되지만 심어진 실행 파일은 **돈다**.
// 그래서 dir 이 비어도 source 는 **항상 채운다**: 호출부가 빈 문자열을 "자리가 없다"로 읽고
// 사람에게 사유를 찍어야 한다. 값을 안 내는 것과 왜 안 내는지를 안 말하는 것은 다르다.
//
// ★ **파일 이름 규칙(소스 트리 경로를 단사로 접는 키)은 여기 담지 않는다.** 이 함수는
// **디렉토리**만 답한다. 키의 유일한 주인은 셸 런처(bin/fd)다 — 같은 판단이 두 자리에 살면
// 한쪽만 고칠 때 조용히 어긋나고, 이 레포는 그 사고를 세 번 겪었다(client.go 의 newClient
// 주석). Go 쪽은 키를 만들지도 **해독하지도** 않는다: 진단과 GC 는 `fd-` 접두와 mtime 만 본다.
// 이름에 소스가 들어가는 **이유**는 런처 소관이지만 여기서도 참조해 둔다 — 재빌드 판정이
// mtime 이라 한 이름을 여러 소스가 나눠 쓰면 **먼저 지은 쪽이 전 채널을 대표한다**(실험으로
// 재현했다: 워크트리에서 런처를 한 번 부르는 것만으로 모든 세션의 훅·MCP 가 그 브랜치 빌드로
// 갈아 끼워진다). 그것이 ②의 배신이 아닌 이유도 같이 적는다: 채널 분기는 **같은 입력이 다른
// 출력**을 내는 것이고(주소 어디에도 안 적힌다), 소스 분기는 **다른 입력이 다른 출력**을 내는
// 것이다(주소에 적혀 있다). ②가 금지하는 것은 전자뿐이다.
//
// FD_STATE_DIR 만 예외로 남긴다 — 채널이 아니라 **사람이** 명시 지정하는 축이라 프로세스마다
// 갈리지 않고, 시험이 진짜 홈에 22MB 짜리 산출물을 짓지 않게 막는 유일한 자리이기도 하다.
//
// ★ **FD_STATE_DIR 를 읽는 법은 런처와 맺은 한 계약이다.** 자리 규칙이 두 언어에 사는 것은
// 여기서만 피할 수 없다 — 런처는 Go 를 못 부르고(Go 가 없어도 돌아야 한다), Go 는 런처보다
// 늦게 뜬다. 그래서 "판단은 한 자리"를 못 지키는 대신 계약을 여기 못 박는다:
//
//	⑴ 값의 **앞뒤 공백을 트림**한다(스페이스·탭, 그리고 CRLF 로 편집된 env 파일의 `\r`).
//	⑵ 트림 후 빈 문자열이면 **미설정**으로 접는다 — HOME 갈래로 간다.
//	⑶ 트림한 값 + `/bin`. 끝 슬래시는 자리를 안 바꾼다(Clean·Join 이 흡수하고, 런처가 그 답에
//	   맞춰 벗긴다). 그 이상의 정규화는 안 한다 — `/x/./y` 부류는 견주는 쪽이 filepath.Dir 로
//	   같이 Clean 하므로 실측상 안 갈린다.
//
// 갈리면 **셋이 동시에** 틀어진다(실측: 꼬리 공백 하나로 셋 다 났다). ⑴ pruneBinCache 가 없는
// 디렉토리를 훑어 GC 가 영영 안 돌고(22MB×N 이 상한 없이 쌓인다 — 이 GC 의 존재 이유가 무효가
// 된다), ⑵ doctor 가 없는 자리를 "바이너리 캐시"로 찍고, ⑶ ExeLines 가 제자리에 있는 프로세스에
// **재기동해도 안 없어지는** "자리 밖" 거짓 경보를 낸다. 셋 다 이 브랜치가 새로 만든 소비부라,
// 잠자던 불일치에 이빨을 단 것도 이 브랜치다. Go 쪽 갈래는 env_test.go 의 표가 잠그고, 런처가
// **같은 답**을 내는지는 plugin_test.go 가 런처를 실제로 돌려 이 함수의 답과 디렉토리째 견준다.
func BinCacheDir(get func(string) (string, bool), home string) (dir, source string) {
	if v, ok := get("FD_STATE_DIR"); ok && strings.TrimSpace(v) != "" {
		return filepath.Join(filepath.Clean(strings.TrimSpace(v)), "bin"), "FD_STATE_DIR (명시 지정)"
	}
	if strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".cache", "flightdeck", "bin"),
			"~/.cache/flightdeck — 채널 환경과 무관한 고정 자리"
	}
	return "", "자리 없음 — HOME 도 FD_STATE_DIR 도 없다. " +
		"공용 임시 디렉토리에는 실행 파일을 안 짓는다(남이 심은 것을 exec 하게 된다)"
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

// BeaconDir 는 창 비콘을 둘 디렉토리다.
//
// ★ 판단은 window.Dir 하나가 갖는다. 여기 사본을 만들면 훅과 MCP 가 서로 다른 자리를 보게 되고,
// 그 어긋남은 어느 화면에도 안 뜬다 — client.go 의 newClient 주석이 적어 둔 그대로,
// 같은 판단이 두 자리에 살면 한쪽만 고칠 때 조용히 어긋난다(이 레포는 그 사고를 세 번 겪었다).
func BeaconDir(get func(string) (string, bool), home string) (string, string) {
	return window.Dir(get, home)
}

// ProjectCoord 는 이 실행이 어느 프로젝트의 어느 워크트리인지다.
type ProjectCoord struct {
	ID       string // 프로젝트 id. 서버의 좌표계
	Path     string // **주 저장소** 경로. 서버가 worktree list 를 이 경로로 돌린다
	Worktree string // 이 세션의 작업 트리 절대경로
	Detail   string // 어떻게 알아냈나 · 무엇을 못 알아냈나. 항상 채운다
}

// RevParseCoords 는 `git rev-parse --git-common-dir --show-toplevel` 출력을 가른다. 순수 함수다.
//
// git 은 인자를 **준 순서대로** 한 줄씩 낸다. 순서를 여기 한 자리에 못 박아 두는 이유는,
// 호출부에서 줄 번호로 집으면 인자를 하나 더할 때 두 값이 조용히 뒤바뀌기 때문이다 —
// 그러면 워크트리 자리에 `.git` 경로가 들어가고 그 사실이 어느 화면에도 안 뜬다.
//
// 줄이 하나만 오면 그것을 --git-common-dir 로 본다(그쪽이 먼저 온다). 빈 줄은 버린다 —
// bare 저장소에서는 --show-toplevel 이 빈 줄을 내고, 그때 워크트리는 접지 않는 것이 맞다.
func RevParseCoords(out string) (commonDir, topLevel string) {
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > 0 {
		commonDir = lines[0]
	}
	if len(lines) > 1 {
		topLevel = lines[1]
	}
	return commonDir, topLevel
}

// MainRepoRoot 는 `git rev-parse --git-common-dir` 결과에서 주 저장소 경로를 낸다. 순수 함수다.
//
// ★ 워크트리에서 부르면 --git-dir 는 `<주저장소>/.git/worktrees/<이름>` 을 주지만
// --git-common-dir 는 `<주저장소>/.git` 을 준다. 서버는 주 저장소로 worktree list 를 돌려야
// 전 세션의 브랜치를 한 번에 얻으므로(service.worktreeIndex), 링크된 워크트리 경로를 주면
// **다른 세션들이 통째로 안 보인다.**
//
// ★ 이것은 **프로젝트** 축이다. 세션의 워크트리 축은 --show-toplevel 이 답한다(resolveProject
// 참조) — 둘을 같은 값으로 접으면 링크된 워크트리가 주 저장소에 흡수돼 §3 이 없앤
// 조상 트리 상속이 돌아온다.
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
	// ★ 한 프로세스에서 값 **둘**을 받는다. 훅 이벤트마다 이 함수가 도는데(beatFromHook →
	//   OpenSession) 여기에 git 호출을 하나 더 얹으면 가장 잦은 경로에 프로세스가 하나 는다.
	//   `git rev-parse` 는 인자를 준 순서대로 한 줄씩 내므로 --show-toplevel 은 공짜다.
	if out, err := gitOut(wt, "rev-parse", "--path-format=absolute", "--git-common-dir", "--show-toplevel"); err == nil {
		common, top := RevParseCoords(out)
		if root := MainRepoRoot(common); root != "" {
			c.Path = root
			c.Detail = "git rev-parse --git-common-dir 로 주 저장소를 찾았다"
		}
		// ★ 워크트리 좌표를 **git 이 답한 트리 루트**로 접는다.
		//
		//   세션 유니크 키가 (machine_id, worktree, cc_session_id) 3중키인데 여기에 날 cwd 가
		//   들어가면 대화 하나가 cwd 수만큼 세션 행을 만든다 — 서브에이전트가 하위 디렉토리에서
		//   go test 를 돌리는 것만으로 새 정체가 발급된다(실측: 원장에 행 50개, 실제 대화 18개).
		//   그러면 처방이 자기 자신과 조율하라 하고, 보드의 세션 수가 부풀고, 선점은 한 행에
		//   발자국은 다른 행에 쌓인다.
		//
		//   ★ 접두 일치가 아니다. DESIGN §3 이 접두 일치를 **일부러** 없앴고(조상 트리의 등록을
		//   물려받는 사고를 겨냥했다) 그것을 되살리면 안 된다. --show-toplevel 은 링크된
		//   워크트리 안에서 **그 워크트리의 루트**를 답한다 — 주 저장소가 아니다. 그래서
		//   하위 디렉토리는 접히고 서로 다른 워크트리는 여전히 갈린다. §3 이 지키려던 것 그대로다.
		if top != "" {
			c.Worktree = filepath.Clean(top)
			c.Detail += " · 워크트리는 --show-toplevel 로 트리 루트에 맞췄다"
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
