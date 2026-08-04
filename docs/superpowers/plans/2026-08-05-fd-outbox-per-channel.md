# 아웃박스 채널 무관 고정 자리 — 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 아웃박스와 격리 파일을 채널 무관한 고정 자리(`~/.flightdeck/outbox`)로 옮기고, 옛 채널별 자리에 남은 줄을 원자적 청구로 흡수한다.

**Architecture:** `env.go` 에 `OutboxPath`·`LegacyOutboxDirs` 순수 함수를 두고(`MachineIDPath`·`ConfigPath` 와 같은 규칙의 셋째 적용), `Outbox` 가 `path` 대신 `dir` 을 들게 바꾼다. 흡수는 `os.Rename` 청구로 원자화해 `Replay` 맨 앞에서 돈다. `fd doctor` 는 자리·잔량·**못 보는 범위**를 함께 찍는다.

**Tech Stack:** Go 1.x, 표준 라이브러리만. 시험은 `go test ./... -race`.

## Global Constraints

- **모듈 루트**: `plugins/flightdeck/server`. 모든 `go` 명령은 이 디렉토리에서 돈다.
- **작업 트리**: `/home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-outbox-per-channel` (브랜치 `fd-outbox-per-channel`).
- **`cmd/fd` 비시험 코드에 `sync`·`atomic` 을 새로 들이지 않는다.** `client.go:67-83` 이 "한 번에 한 호출" 전제를 명시했고 `internal/mcpsrv` 의 `TestServeNeverOverlapsBackend` 가 그것을 지킨다. 이 작업의 동시성 안전은 **잠금이 아니라 `os.Rename` 의 원자성**으로 얻는다.
- **판정은 순수 함수로 빼고 환경 조회를 인자로 받는다**(`env.go:16-18` 의 규율). `os.Getenv` 를 본문에 박지 않는다.
- **조용히 버리는 것이 하나도 없어야 한다**(설계 §9). 흡수한 원본은 지우지 않고 `.migrated-*` 로 남긴다.
- **주석은 "무엇을"이 아니라 "왜 그렇게 정했나"를 적는다.** 이 레포는 주석이 판단의 저장소다.
- 매 과제 끝에 커밋한다. 검증은 `go build ./... && go vet ./... && gofmt -l . && go test ./... -race`.

## 파일 구조

| 파일 | 책임 | 변경 |
|---|---|---|
| `cmd/fd/env.go` | 좌표 판정(순수 함수) | `OutboxPath`·`LegacyOutboxDirs` 추가. `StateDir` 주석 정정 |
| `cmd/fd/outbox.go` | 대기열·격리·재생·흡수 | `Outbox.path` → `dir`. `adopt`·`claimAndDrain`·`LegacyLeftovers` 추가 |
| `cmd/fd/client.go` | 조립 | `newOutbox(sd)` → `newOutbox(get, home)` |
| `cmd/fd/config.go` | 설정 자리 | 반증된 주석 정정만 |
| `cmd/fd/cmds.go` | `runDoctor` | 자리·잔량·사각 출력 |
| `cmd/fd/harness_test.go` | 시험 하네스 | **`HOME` 을 기본 env 에 고정**(과제 1) |
| `cmd/fd/outbox_adopt_test.go` | **새 파일** — 흡수 시험 전부 | 신규 |
| `cmd/fd/env_test.go` | 좌표 시험 | 채널 무관 시험 추가 |
| `cmd/fd/outbox_stuck_test.go` | 재생 시험 | `mkOutbox` 를 `dir` 기반으로 |
| `cmd/fd/degrade_path_test.go` | 열화 경로 통합 시험 | 아웃박스 단정만 고정 자리로 |
| `DESIGN.md` | 설계 | §7 의 398행 한 줄 |

---

### Task 1: 하네스가 진짜 홈에 쓰는 구멍을 먼저 막는다

**왜 이것이 첫 과제인가.** `LegacyOutboxDirs` 는 `~/.local/state/flightdeck/outbox` 를 훑고 `adopt` 는 거기 있는 것을 **옮긴다.** 하네스 기본 환경(`harness_test.go:71-77`)에는 `HOME` 이 없고, `homeDir` 은 주입된 HOME 이 없으면 `os.UserHomeDir()`(프로세스 환경)로 떨어진다. 이 구멍을 안 막고 뒤 과제를 하면 **시험이 개발자의 진짜 아웃박스를 임시 디렉토리로 옮겨 버린다.**

이것은 추측이 아니다. 실측(2026-08-05): `HOME=<임시>` 로 `TestOfflineStateLandsUnderPluginDataNotPluginRoot` 를 돌리면 `<임시>/.flightdeck/machine-id` 가 **생성된다.** 그 시험이 `unpinnedEnv` 대신 손으로 env 를 만들어 `TestUnpinnedEnvNeverReachesTheRealHome` 의 감시를 우회하기 때문이다 — `runEnv` 의 주석(`harness_test.go:221-222`)이 정확히 그 위험을 경고해 뒀는데 이 시험이 그것을 어긴 상태다.

**Files:**
- Modify: `plugins/flightdeck/server/cmd/fd/harness_test.go:71-77`
- Modify: `plugins/flightdeck/server/cmd/fd/degrade_path_test.go:350-358`
- Test: `plugins/flightdeck/server/cmd/fd/harness_env_test.go` (추가)

**Interfaces:**
- Consumes: 없음(첫 과제)
- Produces: `harness.env["HOME"] == harness.home` 불변. 뒤 과제 전부가 이것에 기댄다.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`harness_env_test.go` 끝에 붙인다:

```go
// 하네스 기본 환경이 HOME 을 고정하는지.
//
// ★ 왜 이 축이 생겼나. 아웃박스 흡수(Outbox.adopt)가 옛 채널 자리
// ~/.local/state/flightdeck/outbox 를 훑고 거기 있는 줄을 **옮긴다.** 그래서 HOME 이
// 안 고정되면 시험이 개발자의 진짜 판단을 임시 디렉토리로 옮겨 버린다 —
// 사각이 아니라 사고다.
//
// TestUnpinnedEnvNeverReachesTheRealHome 은 unpinnedEnv 갈래만 지킨다. 그런데
// degrade_path_test.go 가 손으로 env 를 만들어 그 감시를 우회한 전례가 있다
// (실측 2026-08-05: HOME=<임시> 로 돌리면 <임시>/.flightdeck/machine-id 가 생겼다).
// 그래서 **기본 env 자체**를 단정한다.
func TestHarnessPinsHomeSoAdoptNeverReachesTheRealHome(t *testing.T) {
	h := newHarness(t)
	if got := h.env["HOME"]; got != h.home {
		t.Fatalf("하네스 기본 환경의 HOME 이 %q 다 — 가짜 홈 %q 여야 한다.\n"+
			"안 고정하면 아웃박스 흡수가 개발자의 진짜 ~/.local/state 를 훑어 옮긴다", got, h.home)
	}
	// 값만 보면 부족하다 — 실제로 위험한 것은 **합성 결과**이므로 그것을 계산해 단정한다.
	dir, _ := OutboxPath(envOf(h.env), homeDir(envOf(h.env)))
	if !strings.HasPrefix(filepath.Clean(dir), filepath.Clean(h.home)) &&
		!strings.HasPrefix(filepath.Clean(dir), filepath.Clean(h.state)) {
		t.Errorf("아웃박스 자리가 하네스 밖이다: %s", dir)
	}
}
```

- [ ] **Step 2: 실패를 확인한다**

Run: `cd plugins/flightdeck/server && go test ./cmd/fd/ -run TestHarnessPinsHomeSoAdoptNeverReachesTheRealHome -count=1`
Expected: FAIL — `OutboxPath` 가 아직 없어 **컴파일 오류**(`undefined: OutboxPath`). 그것이 이 시점의 정상이다. `OutboxPath` 를 쓰는 두 줄을 잠시 주석 처리하고 돌리면 `HOME 이 "" 다` 로 빨강이 나야 한다 — 확인 후 주석을 되돌린다.

- [ ] **Step 3: 하네스에 HOME 을 고정한다**

`harness_test.go:71-77` 을 이렇게 바꾼다:

```go
	hs.env = map[string]string{
		"FD_URL":                 srv.URL,
		"FD_STATE_DIR":           hs.state,
		"FD_PROJECT":             hs.project,
		"FD_LOG":                 "error",
		"CLAUDE_CODE_SESSION_ID": "cc-session-uuid-1",
		// ★ HOME 을 **기본 env 에서** 고정한다. FD_STATE_DIR 만으로는 부족하다 —
		// Outbox.adopt 가 옛 채널 자리(~/.local/state/flightdeck/outbox)를 훑어
		// 거기 있는 줄을 옮기므로, HOME 이 안 잡히면 homeDir 이 os.UserHomeDir()
		// (프로세스 환경, 시험이 못 바꾼다)로 떨어져 **개발자의 진짜 판단을 옮긴다.**
		// unpinnedEnv 는 이 값을 그대로 물려받고 FD_STATE_DIR 만 뺀다.
		"HOME": hs.home,
	}
	if err := os.MkdirAll(hs.home, 0o755); err != nil {
		t.Fatalf("가짜 홈을 못 만들었다(%s): %v", hs.home, err)
	}
```

`newHarnessAuth` 안, `return hs` 바로 앞에 둔다.

- [ ] **Step 4: 손으로 만든 env 를 고친다**

`degrade_path_test.go:350-358` 의 env 조립을 `unpinnedEnv` 로 바꾼다:

```go
	// ★ 손으로 env 를 만들지 않는다 — runEnv 의 주석이 경고하는 그대로,
	// 손으로 만들면 HOME 을 잊고 시험이 진짜 홈에 쓴다(실측 2026-08-05).
	// unpinnedEnv 가 FD_STATE_DIR 를 빼고 가짜 홈을 함께 주는 정식 갈래다.
	env := h.unpinnedEnv(map[string]string{
		"CLAUDE_PLUGIN_DATA": data,
		"CLAUDE_PLUGIN_ROOT": root,
	})
```

기존 `env := map[string]string{}` / 복사 루프 / `delete(env, "FD_STATE_DIR")` / 두 줄의 대입을 이것으로 대체한다. **아래 전제 검사(359-362행)와 그 뒤 본문은 손대지 않는다.**

- [ ] **Step 5: 시험이 통과하는지 본다**

Run: `cd plugins/flightdeck/server && go test ./cmd/fd/ -count=1`
Expected: `TestHarnessPinsHomeSoAdoptNeverReachesTheRealHome` 은 아직 `OutboxPath` 가 없어 컴파일이 안 된다. **이 과제에서는 그 시험을 아직 넣지 않고**(Step 1 의 파일을 저장하지 않은 채 두거나 `OutboxPath` 부분을 뺀 축소판으로 넣고), 나머지 전 시험이 초록인지만 본다. 과제 2에서 온전한 형태로 되살린다.

축소판(이 과제에서 넣을 것):

```go
func TestHarnessPinsHomeSoAdoptNeverReachesTheRealHome(t *testing.T) {
	h := newHarness(t)
	if got := h.env["HOME"]; got != h.home {
		t.Fatalf("하네스 기본 환경의 HOME 이 %q 다 — 가짜 홈 %q 여야 한다.\n"+
			"안 고정하면 아웃박스 흡수가 개발자의 진짜 ~/.local/state 를 훑어 옮긴다", got, h.home)
	}
}
```

- [ ] **Step 6: 진짜 홈을 안 건드리는지 실측으로 확인한다**

Run:
```bash
cd plugins/flightdeck/server
SB=$(mktemp -d) && env HOME="$SB" GOMODCACHE="$(go env GOMODCACHE)" GOCACHE="$(go env GOCACHE)" \
  go test ./cmd/fd/ -count=1 >/dev/null 2>&1; find "$SB" -type f | head; chmod -R u+w "$SB"; rm -rf "$SB"
```
Expected: `find` 가 **아무것도 안 낸다.** 파일이 나오면 아직 새는 자리가 있는 것이다.
(`GOMODCACHE`·`GOCACHE` 를 넘기는 이유: 안 넘기면 go 가 가짜 홈에 모듈 캐시를 만들어 결과가 안 읽힌다.)

- [ ] **Step 7: 커밋**

```bash
git add plugins/flightdeck/server/cmd/fd/harness_test.go \
        plugins/flightdeck/server/cmd/fd/harness_env_test.go \
        plugins/flightdeck/server/cmd/fd/degrade_path_test.go
git commit -m "test(flightdeck): 하네스가 HOME 을 고정하게 해 시험이 진짜 홈에 못 쓰게 한다

아웃박스 흡수가 옛 채널 자리(~/.local/state/flightdeck/outbox)를 훑어 줄을 옮기므로,
HOME 이 안 고정된 시험은 개발자의 진짜 판단을 임시 디렉토리로 옮긴다.

degrade_path_test 가 손으로 env 를 만들어 TestUnpinnedEnvNeverReachesTheRealHome 의
감시를 우회하고 있었다 — 실측하면 HOME=<임시> 에서 <임시>/.flightdeck/machine-id 가
생긴다. unpinnedEnv 를 쓰게 고쳤고, 기본 env 자체를 단정하는 시험을 뒀다."
```

---

### Task 2: `OutboxPath` — 채널 무관한 고정 자리

**Files:**
- Modify: `plugins/flightdeck/server/cmd/fd/env.go` (93행 뒤, `MachineID` 앞)
- Test: `plugins/flightdeck/server/cmd/fd/env_test.go`
- Test: `plugins/flightdeck/server/cmd/fd/harness_env_test.go` (과제 1의 시험을 온전한 형태로)

**Interfaces:**
- Consumes: 없음
- Produces: `func OutboxPath(get func(string) (string, bool), home string) (dir, source string)` — 디렉토리와 그것을 고른 사유. 과제 4가 `newOutbox` 에서 쓴다.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`env_test.go` 에 붙인다:

```go
// 아웃박스 자리는 **채널 환경과 무관해야 한다.**
//
// ★ 이 시험이 이 항목의 회귀를 원리적으로 막는다. TestConfigPathIsChannelIndependent ·
// TestAllChannelsAgreeOnMachineID 와 같은 모양이다 — 산문이 아니라 이것이 규칙을 지킨다.
//
// 왜 상태 디렉토리가 아닌가: ResolveStateDir 은 CLAUDE_PLUGIN_DATA(훅·MCP 에만 있다)와
// XDG_STATE_HOME|~/.local/state(사용자 셸)로 **일부러 갈리게** 만든 축이다. 캐시는
// 재생성 가능하니 갈려도 되지만 아웃박스는 설계 §7 이 "재생성 불가한 유일한 자산"이라
// 부른 것을 담는다. 갈린 자리에 두면 셸에서 쌓인 판단을 훅·MCP 가 영영 못 보낸다.
func TestOutboxPathIsChannelIndependent(t *testing.T) {
	home := "/h"
	envs := []map[string]string{
		{},
		{"CLAUDE_PLUGIN_DATA": "/plugin/data"}, // 훅·MCP 채널
		{"XDG_STATE_HOME": "/xdg/state"},       // 사용자 셸 채널
		{"CLAUDE_PLUGIN_DATA": "/plugin/data", "XDG_STATE_HOME": "/xdg/state"},
	}
	want := filepath.Join(home, ".flightdeck", "outbox")
	for i, e := range envs {
		got, src := OutboxPath(envOf(e), home)
		if got != want {
			t.Errorf("%d번 환경에서 아웃박스 자리가 %q 다 — %q 여야 한다.\n"+
				"채널마다 갈리면 셸에서 쌓인 판단을 훅·MCP 가 영영 못 보낸다", i, got, want)
		}
		if strings.TrimSpace(src) == "" {
			t.Errorf("%d번 환경에서 출처가 비었다 — '왜 여기냐'에 답할 자리가 없다", i)
		}
	}
}

// FD_STATE_DIR 는 채널이 아니라 **사람이** 지정하는 축이라 이것만은 존중한다
// (MachineIDPath·ConfigPath 가 같은 예외를 같은 이유로 둔다 — 시험이 진짜 홈에
// 쓰지 않게 막는 유일한 자리이기도 하다).
func TestOutboxPathHonoursExplicitStateDir(t *testing.T) {
	got, src := OutboxPath(envOf(map[string]string{"FD_STATE_DIR": "/explicit"}), "/h")
	if want := filepath.Join("/explicit", "outbox"); got != want {
		t.Errorf("FD_STATE_DIR 를 줬는데 %q 다 — %q 여야 한다", got, want)
	}
	if !strings.Contains(src, "FD_STATE_DIR") {
		t.Errorf("출처가 FD_STATE_DIR 를 안 말한다: %q", src)
	}
}

// HOME 도 FD_STATE_DIR 도 없으면 임시 디렉토리로 떨어지되 **그 사실을 사유에 적는다.**
// 값은 나오지만 재부팅하면 사라지므로, 조용히 잃지 않게 사유가 그것을 말해야 한다.
func TestOutboxPathSaysWhenItWillNotSurviveReboot(t *testing.T) {
	_, src := OutboxPath(envOf(map[string]string{}), "")
	if !strings.Contains(src, "임시") {
		t.Errorf("임시 디렉토리 폴백인데 사유가 그것을 안 말한다: %q", src)
	}
}
```

`env_test.go` 의 import 에 `"strings"` 가 없으면 넣는다.

- [ ] **Step 2: 실패를 확인한다**

Run: `cd plugins/flightdeck/server && go test ./cmd/fd/ -run TestOutboxPath -count=1`
Expected: FAIL — `undefined: OutboxPath`

- [ ] **Step 3: `OutboxPath` 를 쓴다**

`env.go` 의 `MachineIDPath` 함수 바로 뒤(93행 다음)에 넣는다:

```go
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
// 프로세스마다 갈리지 않고, 시험이 진짜 홈에 판단을 쓰지 않게 막는 유일한 자리다.
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
```

- [ ] **Step 4: 통과를 확인하고 과제 1의 시험을 온전한 형태로 되살린다**

`harness_env_test.go` 의 `TestHarnessPinsHomeSoAdoptNeverReachesTheRealHome` 을 과제 1 Step 1 의 **온전한 판본**(합성 결과까지 단정하는 것)으로 바꾼다.

Run: `cd plugins/flightdeck/server && go test ./cmd/fd/ -count=1`
Expected: PASS 전부

- [ ] **Step 5: 커밋**

```bash
git add plugins/flightdeck/server/cmd/fd/env.go \
        plugins/flightdeck/server/cmd/fd/env_test.go \
        plugins/flightdeck/server/cmd/fd/harness_env_test.go
git commit -m "feat(flightdeck): OutboxPath — 아웃박스 자리를 채널 환경에서 떼어낸다

MachineIDPath·ConfigPath 와 같은 규칙의 셋째 적용이다. 가르는 축은 '열화 상태인가'가
아니라 '재생성 가능한가'였다 — 캐시는 갈려도 되고 아웃박스는 안 된다."
```

---

### Task 3: `LegacyOutboxDirs` — 이 채널이 계산할 수 있는 옛 자리만

**Files:**
- Modify: `plugins/flightdeck/server/cmd/fd/env.go` (`OutboxPath` 바로 뒤)
- Test: `plugins/flightdeck/server/cmd/fd/env_test.go`

**Interfaces:**
- Consumes: `OutboxPath`(과제 2)
- Produces: `func LegacyOutboxDirs(get func(string) (string, bool), home, target string) []string` — 훑을 옛 디렉토리 목록. 목표와 같은 자리와 중복은 뺀다. 과제 4가 `newOutbox` 에서, 과제 5가 `adopt` 에서 쓴다.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`env_test.go` 에 붙인다:

```go
// 옛 자리 목록은 **이 채널이 계산할 수 있는 것만** 담는다.
//
// ★ ~/.claude/plugins/data/*/flightdeck 를 glob 하지 않는다. 그 경로에는 플러그인
// 버전과 마켓 이름이 들어가고, 설계 §13 이 "그 경로를 어디에도 저장하지 않는다"고
// 판정했다. 추측해 박으면 그 판정을 어긴다.
//
// 대신 각 채널이 제 자리만 비우고 고정 자리로 모인다 — 훅·MCP 는 CLAUDE_PLUGIN_DATA 가
// 있으니 SessionStart 마다, 셸은 사람이 부를 때. 어느 채널이 한 번도 안 돌면 그 자리는
// 안 비워지고, 그 구멍은 fd doctor 가 말로 찍는다.
func TestLegacyOutboxDirsCoversOnlyWhatThisChannelCanCompute(t *testing.T) {
	home := "/h"
	target := filepath.Join(home, ".flightdeck", "outbox")

	got := LegacyOutboxDirs(envOf(map[string]string{
		"CLAUDE_PLUGIN_DATA": "/plugin/data",
		"XDG_STATE_HOME":     "/xdg/state",
	}), home, target)

	want := []string{
		filepath.Join("/plugin/data", "flightdeck", "outbox"),
		filepath.Join("/xdg/state", "flightdeck", "outbox"),
		filepath.Join(home, ".local", "state", "flightdeck", "outbox"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("옛 자리 목록이\n  %v\n여야 하는데\n  %v\n다", want, got)
	}
	for _, d := range got {
		if strings.Contains(d, "*") {
			t.Errorf("glob 패턴이 목록에 들어갔다(%q) — §13 은 그 경로를 저장하지 말라고 했다", d)
		}
	}
}

// 목표와 같은 자리는 옮길 것이 없다. 넣으면 자기 자신을 청구해 대기열을 흔든다.
func TestLegacyOutboxDirsExcludesTheTarget(t *testing.T) {
	target := filepath.Join("/xdg/state", "flightdeck", "outbox")
	got := LegacyOutboxDirs(envOf(map[string]string{"XDG_STATE_HOME": "/xdg/state"}), "", target)
	for _, d := range got {
		if filepath.Clean(d) == filepath.Clean(target) {
			t.Fatalf("목표 자리(%s)가 옛 자리 목록에 들어 있다 — 자기 자신을 청구한다", target)
		}
	}
}

// 같은 자리를 두 축이 가리켜도 한 번만 훑는다.
func TestLegacyOutboxDirsDeduplicates(t *testing.T) {
	got := LegacyOutboxDirs(envOf(map[string]string{
		"CLAUDE_PLUGIN_DATA": "/same",
		"XDG_STATE_HOME":     "/same",
	}), "", "/target/outbox")
	if len(got) != 1 {
		t.Errorf("같은 자리를 %d번 훑는다: %v", len(got), got)
	}
}

// ★ 임시 디렉토리는 **일부러 목록에 안 넣는다.**
//
// ResolveStateDir 의 마지막 폴백이 <tmp>/flightdeck 이지만, 그 갈래가 실제로 걸리는
// 조건(HOME 도 FD_STATE_DIR 도 없다)에서는 **목표 자리도 <tmp>/flightdeck/outbox** 라
// 어차피 목표와 같아 걸러진다. 즉 넣어도 아무 때도 쓸모가 없다.
// 반면 여러 사용자가 쓰는 머신에서 /tmp/flightdeck 은 **남의 것일 수 있고**, 훑으면
// 남의 판단을 내 자리로 옮기게 된다. 쓸모 0 · 위험 있음이라 안 넣는다.
func TestLegacyOutboxDirsNeverScansTempDir(t *testing.T) {
	got := LegacyOutboxDirs(envOf(map[string]string{}), "/h", "/h/.flightdeck/outbox")
	for _, d := range got {
		if strings.HasPrefix(filepath.Clean(d), filepath.Clean(os.TempDir())) {
			t.Errorf("임시 디렉토리를 훑는다(%q) — 공용 머신에서 남의 판단을 옮긴다", d)
		}
	}
}
```

`env_test.go` 의 import 에 `"os"`·`"reflect"` 가 없으면 넣는다.

- [ ] **Step 2: 실패를 확인한다**

Run: `cd plugins/flightdeck/server && go test ./cmd/fd/ -run TestLegacyOutboxDirs -count=1`
Expected: FAIL — `undefined: LegacyOutboxDirs`

- [ ] **Step 3: 구현한다**

`env.go` 의 `OutboxPath` 바로 뒤에 넣는다:

```go
// LegacyOutboxDirs 는 아웃박스가 채널마다 갈려 있던 시절의 자리 후보다. 순수 함수다.
//
// ★ **이 채널이 계산할 수 있는 것만 담는다.** ~/.claude/plugins/data/*/flightdeck 를
// glob 하지 않는다 — 그 경로에는 플러그인 버전과 마켓 이름이 들어가고, 설계 §13 이
// "버전이 경로에 들어가므로 그 경로를 어디에도 저장하지 않는다"고 판정했다.
// 추측해 박으면 그 판정을 어기고, 마켓 이름이 바뀌는 날 조용히 빗나간다.
//
// 그래서 수렴은 이렇게 일어난다: 훅·MCP 채널은 CLAUDE_PLUGIN_DATA 가 있으니
// SessionStart 마다 제 자리를 비우고, 셸 채널은 제 자리를 비운다. 서로의 배치를
// 추측하지 않고 양쪽이 고정 자리로 모인다.
//
// **정직한 구멍**: 어떤 채널이 이 변경 뒤 fd 를 한 번도 안 돌리면 그 자리는 영영
// 안 비워진다. 그 사실은 runDoctor 가 말로 찍는다 — 안 잰 축을 잰 척하지 않는다(§13).
//
// 임시 디렉토리는 안 넣는다. 그 갈래가 걸리는 조건에서는 목표도 같은 자리라 쓸모가 없고,
// 공용 머신에서는 /tmp/flightdeck 이 남의 것일 수 있어 훑으면 남의 판단을 옮긴다.
func LegacyOutboxDirs(get func(string) (string, bool), home, target string) []string {
	var out []string
	tgt := filepath.Clean(target)
	add := func(p string) {
		if strings.TrimSpace(p) == "" {
			return
		}
		p = filepath.Clean(p)
		if p == tgt {
			return // 목표와 같은 자리는 옮길 것이 없다
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
	}
	return out
}
```

- [ ] **Step 4: 통과를 확인한다**

Run: `cd plugins/flightdeck/server && go test ./cmd/fd/ -run TestLegacyOutboxDirs -count=1 -v`
Expected: PASS 4건

- [ ] **Step 5: 커밋**

```bash
git add plugins/flightdeck/server/cmd/fd/env.go plugins/flightdeck/server/cmd/fd/env_test.go
git commit -m "feat(flightdeck): LegacyOutboxDirs — 이 채널이 계산할 수 있는 옛 자리만 훑는다

plugins/data/* 를 glob 하지 않는다(§13). 임시 디렉토리도 안 넣는다 —
그 갈래가 걸리는 조건에서는 목표와 같은 자리라 쓸모가 없고, 공용 머신에서는
남의 판단을 옮기게 된다."
```

---

### Task 4: `Outbox` 를 디렉토리 기반으로 바꾸고 배선한다

이 과제는 **동작을 바꾸지 않는다.** 자리만 옮기고 기존 시험이 전부 초록으로 남는지 본다.

**Files:**
- Modify: `plugins/flightdeck/server/cmd/fd/outbox.go:169-190, 198-287, 369-372`
- Modify: `plugins/flightdeck/server/cmd/fd/client.go:115, 130`
- Modify: `plugins/flightdeck/server/cmd/fd/outbox_stuck_test.go:31-38`

**Interfaces:**
- Consumes: `OutboxPath`·`LegacyOutboxDirs`(과제 2·3)
- Produces:
  - `func newOutbox(get func(string) (string, bool), home string) *Outbox`
  - `func (o *Outbox) Dir() string` · `func (o *Outbox) Source() string`
  - `Outbox` 필드 `dir string` · `source string` · `legacy []string` · `now func() time.Time`
  - 상수 `pendingName = "pending.jsonl"` · `rejectedName = "rejected.jsonl"`
  - `func readEntries(path string) ([]OutboxEntry, error)` · `func readRejected(path string) ([]RejectedEntry, error)`

- [ ] **Step 1: `Outbox` 구조와 파일 이름 상수를 바꾼다**

`outbox.go:169-190` 을 이것으로 교체한다:

```go
// 대기열·격리 파일의 이름. 한 자리에 모은다 — 흡수(adopt)가 이 이름으로 옛 자리를 훑으므로
// 두 자리에 흩어 두면 한쪽만 고칠 때 흡수가 조용히 빗나간다.
const (
	pendingName  = "pending.jsonl"
	rejectedName = "rejected.jsonl"
)

// Outbox 는 **채널 무관한 고정 자리**의 대기열 하나다. 파일 하나에 JSONL 로 쌓는다.
//
// ★ 예전에는 상태 디렉토리 아래였다. 그 자리가 채널마다 갈려서 셸에서 쌓인 판단을
// 훅·MCP 가 영영 못 보내는 결함이 있었다 — OutboxPath 주석에 그 판정이 있다.
type Outbox struct {
	dir    string   // 대기열·격리 파일이 있는 디렉토리
	source string   // 왜 이 자리인가. fd doctor 가 찍는다 — machineSrc 가 선례다
	legacy []string // 갈려 있던 시절의 자리. adopt 가 비운다
	// now 는 격리·보존 시각을 찍는 시계다. 시험이 갈아 끼울 자리이기도 하다.
	now func() time.Time
}

func newOutbox(get func(string) (string, bool), home string) *Outbox {
	dir, src := OutboxPath(get, home)
	return &Outbox{
		dir:    dir,
		source: src,
		legacy: LegacyOutboxDirs(get, home, dir),
		now:    func() time.Time { return time.Now().UTC() },
	}
}

// Dir·Source 는 fd doctor 가 "어디를, 왜"를 찍기 위한 자리다.
func (o *Outbox) Dir() string    { return o.dir }
func (o *Outbox) Source() string { return o.source }

// pendingPath·rejectedPath 는 두 파일의 자리다. 같은 디렉토리에 둔다 — 같은 축의 같은 자산이다.
func (o *Outbox) pendingPath() string  { return filepath.Join(o.dir, pendingName) }
func (o *Outbox) rejectedPath() string { return filepath.Join(o.dir, rejectedName) }

// stamp 는 지금이다. 시계가 안 꽂혔어도 값을 낸다.
func (o *Outbox) stamp() time.Time {
	if o.now == nil {
		return time.Now().UTC()
	}
	return o.now()
}
```

기존 369-372행의 `rejectedPath` 는 **지운다**(위로 옮겼다).

- [ ] **Step 2: `o.path` 를 쓰던 자리를 전부 바꾼다**

`outbox.go` 안에서 `o.path` → `o.pendingPath()`. 걸리는 자리는 `Append`(208·215행), `List`(231행), `keep`(265·279·283행)이다.

`List` 와 `Rejected` 는 읽기 부분을 재사용 가능한 함수로 뺀다 — 흡수가 **옛 자리의 파일**을 같은 규칙으로 읽어야 하기 때문이다:

```go
// List 는 대기 중인 전부를 순서대로 낸다. 파일이 없으면 빈 목록이다(오류가 아니다).
func (o *Outbox) List() ([]OutboxEntry, error) { return readEntries(o.pendingPath()) }

// readEntries 는 JSONL 대기열 파일 하나를 읽는다.
//
// ★ 깨진 줄을 **조용히 버리지 않는다.** 이 파일은 재생성 불가한 자산이므로
// 해석 실패는 **읽은 데까지와 함께** 오류로 올려 사람이 보게 한다
// (설계 §9 "조용히 버리는 것이 하나도 없어야 한다").
//
// 흡수(adopt)가 옛 자리의 파일에도 이 함수를 쓴다 — 같은 규칙으로 읽어야
// "여기서는 버려지고 저기서는 안 버려지는" 자리가 안 생긴다.
func readEntries(path string) ([]OutboxEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("아웃박스 읽기 실패: %w", err)
	}
	defer f.Close()

	var out []OutboxEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // 판단 본문은 길 수 있다
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var e OutboxEntry
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			return out, fmt.Errorf("아웃박스 %d번째 줄을 해석하지 못했다: %w", line, err)
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return out, fmt.Errorf("아웃박스 주사 실패: %w", err)
	}
	return out, nil
}

// Rejected 는 격리된 줄 전부다. 파일이 없으면 빈 목록이다(오류가 아니다).
func (o *Outbox) Rejected() ([]RejectedEntry, error) { return readRejected(o.rejectedPath()) }

// readRejected 는 격리 파일 하나를 읽는다. 흡수가 옛 자리에도 쓴다.
func readRejected(path string) ([]RejectedEntry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("격리 파일 읽기 실패: %w", err)
	}
	var out []RejectedEntry
	for i, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r RejectedEntry
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return out, fmt.Errorf("격리 %d번째 줄 해석 실패: %w", i, err)
		}
		out = append(out, r)
	}
	return out, nil
}
```

기존 `List`(230-260행)와 `Rejected`(394-415행)의 본문을 위 형태로 대체한다.

- [ ] **Step 3: 배선을 바꾼다**

`client.go:130` 을 바꾼다:

```go
		Outbox:   newOutbox(get, home),
```

`newClient` 의 `sd StateDir` 인자는 **그대로 둔다** — `newCache(sd)` 가 계속 쓴다.

- [ ] **Step 4: 시험 도우미를 고친다**

`outbox_stuck_test.go:31-38` 을 바꾼다:

```go
func mkOutbox(t *testing.T) *Outbox {
	t.Helper()
	at := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
	return &Outbox{
		dir: filepath.Join(t.TempDir(), "outbox"),
		now: func() time.Time { return at },
	}
}
```

- [ ] **Step 5: 전 시험이 초록인지 본다**

Run: `cd plugins/flightdeck/server && go build ./... && go vet ./... && gofmt -l . && go test ./... -race -count=1`
Expected: 전 패키지 ok. **`degrade_path_test.go` 의 `TestOfflineStateLandsUnderPluginDataNotPluginRoot` 는 여기서 빨강이 난다** — 아웃박스가 이제 plugin-data 아래가 아니기 때문이다. 그것이 이 시점의 **정상**이고, 과제 8에서 고친다. 다른 시험이 빨갛다면 그것은 회귀다.

빨강이 그 하나뿐인지 확인:
```bash
go test ./cmd/fd/ -count=1 2>&1 | grep -E '^(---|ok|FAIL)' | head -20
```

- [ ] **Step 6: 커밋**

```bash
git add plugins/flightdeck/server/cmd/fd/outbox.go \
        plugins/flightdeck/server/cmd/fd/client.go \
        plugins/flightdeck/server/cmd/fd/outbox_stuck_test.go
git commit -m "refactor(flightdeck): Outbox 를 고정 자리 디렉토리 기반으로 바꾼다

동작은 안 바꾼다. 읽기를 readEntries·readRejected 로 빼는 이유는 흡수가 옛 자리의
파일을 **같은 규칙으로** 읽어야 해서다 — 규칙이 갈리면 한쪽에서만 줄이 조용히 버려진다.

degrade_path_test 의 아웃박스 단정은 여기서 빨강이다. 그 시험은 자리가 옮겨진 것을
아직 모른다 — 다음 커밋에서 고친다."
```

---

### Task 5: `adopt` — 청구 후 흡수

**Files:**
- Modify: `plugins/flightdeck/server/cmd/fd/outbox.go` (`Replay` 앞)
- Create: `plugins/flightdeck/server/cmd/fd/outbox_adopt_test.go`

**Interfaces:**
- Consumes: `readEntries`·`readRejected`·`Append`·`quarantine`·`Outbox.legacy`(과제 4)
- Produces:
  - `func (o *Outbox) adopt() []string` — 못 한 것의 사유 목록(빈 슬라이스면 전부 됐다)
  - `func claimSuffix() string`
  - `func claimables(dir, base string) []string`

- [ ] **Step 1: 실패하는 시험을 쓴다**

새 파일 `outbox_adopt_test.go`:

```go
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// 이 파일이 지키는 것은 하나다: **어느 채널에서 쌓인 판단도 결국 나간다.**
//
// 아웃박스가 채널마다 갈려 있던 시절의 자리에 남은 줄을 고정 자리로 흡수한다.
// 그 흡수가 판단을 잃지 않고, 두 번 만들지 않고, 도중에 죽어도 스스로 낫는지를 본다.

// mkAdoptable 은 옛 자리 하나와 그 안의 대기열 파일을 만든다.
func mkAdoptable(t *testing.T, dir string, keys ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("옛 자리를 못 만들었다(%s): %v", dir, err)
	}
	var b strings.Builder
	for _, k := range keys {
		buf, err := json.Marshal(entry(k))
		if err != nil {
			t.Fatalf("직렬화 실패: %v", err)
		}
		b.Write(buf)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, pendingName), []byte(b.String()), 0o600); err != nil {
		t.Fatalf("옛 대기열을 못 썼다: %v", err)
	}
}

// keysOf 는 대기열의 키를 순서대로 낸다.
func keysOf(t *testing.T, o *Outbox) []string {
	t.Helper()
	es, err := o.List()
	if err != nil {
		t.Fatalf("대기열을 못 읽었다: %v", err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Key)
	}
	return out
}

// ── 흡수가 판단을 옮기고 원본을 보존한다 ─────────────────────────────────────

func TestAdoptDrainsLegacyOutboxes(t *testing.T) {
	o := mkOutbox(t)
	legacyA := filepath.Join(t.TempDir(), "chanA", "outbox")
	legacyB := filepath.Join(t.TempDir(), "chanB", "outbox")
	o.legacy = []string{legacyA, legacyB}

	mkAdoptable(t, legacyA, "a1", "a2")
	mkAdoptable(t, legacyB, "b1")

	if notes := o.adopt(); len(notes) != 0 {
		t.Fatalf("흡수가 사유를 냈다 — 전부 됐어야 한다: %v", notes)
	}

	got := keysOf(t, o)
	want := []string{"a1", "a2", "b1"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("고정 자리의 키가 %v 다 — %v 여야 한다", got, want)
	}

	// ★ 원본을 **지우지 않았는지.** 큐를 비우는 것과 기록을 없애는 것은 다르다(§9).
	for _, dir := range []string{legacyA, legacyB} {
		if _, err := os.Stat(filepath.Join(dir, pendingName)); !os.IsNotExist(err) {
			t.Errorf("%s 의 원본이 아직 정규 이름으로 있다 — 흡수가 안 끝났다", dir)
		}
		ms, _ := filepath.Glob(filepath.Join(dir, pendingName+".migrated-*"))
		if len(ms) != 1 {
			t.Errorf("%s 에 보존본이 %d개다 — 정확히 1개여야 한다(지웠거나 안 옮겼다)", dir, len(ms))
		}
		cs, _ := filepath.Glob(filepath.Join(dir, pendingName+".claimed-*"))
		if len(cs) != 0 {
			t.Errorf("%s 에 청구본이 %d개 남았다 — 성공했으면 없어야 한다", dir, len(cs))
		}
	}
}

// 격리 파일도 함께 온다. 보관소가 채널마다 갈리면 '어디에 뭐가 있나'가 채널별 질문으로 남는다.
func TestAdoptDrainsLegacyRejected(t *testing.T) {
	o := mkOutbox(t)
	legacy := filepath.Join(t.TempDir(), "chan", "outbox")
	o.legacy = []string{legacy}
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatalf("옛 자리를 못 만들었다: %v", err)
	}
	r := RejectedEntry{Entry: entry("r1"), Reason: "서버가 409 로 거절했다", At: time.Unix(0, 0).UTC()}
	buf, _ := json.Marshal(r)
	if err := os.WriteFile(filepath.Join(legacy, rejectedName), append(buf, '\n'), 0o600); err != nil {
		t.Fatalf("옛 격리를 못 썼다: %v", err)
	}

	if notes := o.adopt(); len(notes) != 0 {
		t.Fatalf("흡수가 사유를 냈다: %v", notes)
	}
	got, err := o.Rejected()
	if err != nil {
		t.Fatalf("격리를 못 읽었다: %v", err)
	}
	if len(got) != 1 || got[0].Entry.Key != "r1" {
		t.Fatalf("격리가 안 왔다: %+v", got)
	}
	if got[0].Reason == "" {
		t.Error("격리 사유가 비었다 — 왜 격리됐는지가 사라지면 보관의 의미가 없다")
	}
}

// ── 두 번 돌려도 판단이 두 벌이 되지 않는다 ──────────────────────────────────

func TestAdoptIsIdempotent(t *testing.T) {
	o := mkOutbox(t)
	legacy := filepath.Join(t.TempDir(), "chan", "outbox")
	o.legacy = []string{legacy}
	mkAdoptable(t, legacy, "k1", "k2")

	o.adopt()
	o.adopt() // 두 번째는 집을 것이 없다

	got := keysOf(t, o)
	if len(got) != 2 {
		t.Errorf("두 번 흡수했더니 키가 %d개다 — 2개여야 한다: %v", len(got), got)
	}
}

// ── 도중에 죽어도 스스로 낫는다 ──────────────────────────────────────────────

// 흡수 도중 실패하면 **원본으로 되돌리지 않는다.** 그 사이 새 오프라인 쓰기가 원본을
// 만들었으면 rename 이 그것을 덮어써 판단을 잃기 때문이다. 청구본을 그대로 두고
// 다음 흡수가 그 고아를 집는다.
func TestAdoptFailureKeepsClaimAndSelfHeals(t *testing.T) {
	o := mkOutbox(t)
	legacy := filepath.Join(t.TempDir(), "chan", "outbox")
	o.legacy = []string{legacy}
	mkAdoptable(t, legacy, "k1")

	// 고정 자리를 **파일로** 만들어 쓰기를 막는다 — MkdirAll 이 실패한다.
	if err := os.MkdirAll(filepath.Dir(o.dir), 0o755); err != nil {
		t.Fatalf("상위 디렉토리를 못 만들었다: %v", err)
	}
	if err := os.WriteFile(o.dir, []byte("나는 디렉토리가 아니다"), 0o600); err != nil {
		t.Fatalf("막이 파일을 못 만들었다: %v", err)
	}

	notes := o.adopt()
	if len(notes) == 0 {
		t.Fatal("흡수가 실패했는데 사유가 안 나왔다 — 침묵하면 판단이 조용히 갇힌다")
	}
	cs, _ := filepath.Glob(filepath.Join(legacy, pendingName+".claimed-*"))
	if len(cs) != 1 {
		t.Fatalf("청구본이 %d개다 — 실패했으면 정확히 1개가 남아야 한다", len(cs))
	}
	if _, err := os.Stat(filepath.Join(legacy, pendingName)); !os.IsNotExist(err) {
		t.Error("원본이 되돌려졌다 — 새 오프라인 쓰기를 덮어쓸 수 있어 되돌리면 안 된다")
	}

	// ── 낫는다 ──
	if err := os.Remove(o.dir); err != nil {
		t.Fatalf("막이를 못 치웠다: %v", err)
	}
	if notes := o.adopt(); len(notes) != 0 {
		t.Fatalf("복구 후에도 사유가 났다: %v", notes)
	}
	if got := keysOf(t, o); len(got) != 1 || got[0] != "k1" {
		t.Errorf("고아를 안 집었다 — 판단이 그 자리에 영원히 남는다: %v", got)
	}
}

// ── 동시에 흡수해도 한쪽만 이긴다 ────────────────────────────────────────────

// ★ 이것이 §4 의 TTL 구멍을 닫았다는 단정이다.
//
// 판단 POST 는 DB 에 남는 멱등이지만 그 표의 TTL 이 24시간이고 판단은 추가 전용이다.
// 즉 같은 키가 24시간을 넘겨 두 번 재생되면 **되돌릴 수 없는 판단 한 줄**이 생긴다.
// 잠금 없이 흡수하면 두 프로세스가 같은 줄을 각자 쌓아 그 경로가 열린다.
// os.Rename 청구가 그것을 원리적으로 막는다 — 원본 하나는 한 쪽만 집는다.
func TestConcurrentAdoptClaimsOnce(t *testing.T) {
	o := mkOutbox(t)
	legacy := filepath.Join(t.TempDir(), "chan", "outbox")
	o.legacy = []string{legacy}
	mkAdoptable(t, legacy, "k1", "k2", "k3")

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// 같은 디렉토리를 보는 **다른 Outbox 값**이다 — 프로세스 여럿을 흉내 낸다.
			// 필드를 공유하지 않으므로 -race 가 볼 것은 파일시스템 경합뿐이다.
			other := &Outbox{dir: o.dir, legacy: o.legacy, now: o.now}
			other.adopt()
		}()
	}
	wg.Wait()

	ms, _ := filepath.Glob(filepath.Join(legacy, pendingName+".migrated-*"))
	if len(ms) != 1 {
		t.Errorf("보존본이 %d개다 — 청구가 원자였다면 정확히 1개다: %v", len(ms), ms)
	}
	got := keysOf(t, o)
	seen := map[string]int{}
	for _, k := range got {
		seen[k]++
	}
	for k, n := range seen {
		if n != 1 {
			t.Errorf("키 %q 가 대기열에 %d줄이다 — 24시간을 넘겨 재생되면 판단이 두 벌이 된다", k, n)
		}
	}
	if len(got) != 3 {
		t.Errorf("대기열에 %d줄이다 — 3줄이어야 한다: %v", len(got), got)
	}
}

// ── 보존본은 다시 안 집는다 ──────────────────────────────────────────────────

// .migrated-* 를 다시 집으면 흡수가 무한히 반복되고, 그때마다 Append 가 중복 검사를
// 도느라 대기열 전체를 읽는다. 집지 않는 것이 옳다.
func TestAdoptIgnoresMigratedFiles(t *testing.T) {
	o := mkOutbox(t)
	legacy := filepath.Join(t.TempDir(), "chan", "outbox")
	o.legacy = []string{legacy}
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatalf("옛 자리를 못 만들었다: %v", err)
	}
	buf, _ := json.Marshal(entry("old"))
	if err := os.WriteFile(filepath.Join(legacy, pendingName+".migrated-20260101T000000Z-abc"),
		append(buf, '\n'), 0o600); err != nil {
		t.Fatalf("보존본을 못 만들었다: %v", err)
	}
	if notes := o.adopt(); len(notes) != 0 {
		t.Fatalf("사유가 났다: %v", notes)
	}
	if got := keysOf(t, o); len(got) != 0 {
		t.Errorf("보존본을 다시 집었다 — 흡수가 무한 반복된다: %v", got)
	}
}
```

- [ ] **Step 2: 실패를 확인한다**

Run: `cd plugins/flightdeck/server && go test ./cmd/fd/ -run 'TestAdopt|TestConcurrentAdopt' -count=1`
Expected: FAIL — `o.adopt undefined`

- [ ] **Step 3: 구현한다**

`outbox.go` 의 `ReplayResult` 정의 앞에 넣는다:

```go
// ── 흡수 ─────────────────────────────────────────────────────────────────────
//
// 아웃박스가 채널마다 갈려 있던 시절의 자리에 남은 줄을 고정 자리로 옮긴다.
// 옮기지 않으면 그 줄은 그 채널이 다시 돌 때까지 안 나가고, 채널이 안 돌면 영영 안 나간다.

// claimSuffix 는 청구 이름의 유일값이다.
//
// ★ **시각을 쓰지 않는다.** 같은 초에 둘이 들어오면 이름이 겹쳐 청구가 청구가 아니게 된다
// (FreshKey 가 같은 이유로 같은 선택을 한다). 난수를 못 읽으면 나노초로 대신한다 —
// 유일성은 떨어지지만 값이 없는 것보다 낫다.
func claimSuffix() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("n%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

// claimables 는 이 디렉토리에서 청구할 수 있는 파일 전부다. 순수 함수는 아니다(glob 을 돈다).
//
// 정규 이름 하나와 **고아 청구본 전부**다. 고아는 앞선 실행이 흡수 도중 죽으며 남긴 것이라
// (claimAndDrain 의 실패 가지) 안 집으면 판단이 그 자리에 영원히 남는다.
//
// ★ .migrated-* 는 **안 집는다.** 흡수가 끝난 보존본이라 다시 집으면 무한히 반복된다.
func claimables(dir, base string) []string {
	out := []string{filepath.Join(dir, base)}
	orphans, err := filepath.Glob(filepath.Join(dir, base+".claimed-*"))
	if err != nil {
		return out // 패턴이 상수라 여기 오지 않는다. 와도 정규 이름은 집는다
	}
	sort.Strings(orphans) // 순서를 고정한다 — 시험이 단정할 수 있어야 한다
	return append(out, orphans...)
}

// claimAndDrain 은 파일 하나를 청구해 옮긴다. 못 한 사유를 낸다(빈 문자열이면 됐다).
//
// ★ **청구가 이 설계의 핵심이다.** os.Rename 은 원자라 둘이 동시에 들어와도 한쪽만 이긴다.
// 그것이 없으면 두 프로세스가 같은 줄을 각자 쌓고, 판단 POST 의 멱등 표 TTL(24시간)을
// 넘겨 재생되는 순간 **되돌릴 수 없는 판단 한 줄**이 생긴다(판단은 추가 전용이다).
func (o *Outbox) claimAndDrain(src, dir, base string, move func(claimed string) error) string {
	sfx := claimSuffix()
	claimed := filepath.Join(dir, base+".claimed-"+sfx)
	if err := os.Rename(src, claimed); err != nil {
		if os.IsNotExist(err) {
			return "" // 없거나 남이 먼저 집었다. 둘 다 정상이다
		}
		return fmt.Sprintf("%s 를 청구하지 못했다: %v", clip(src, 200), err)
	}
	if err := move(claimed); err != nil {
		// ★ 원본으로 **되돌리지 않는다.** 그 사이 새 오프라인 쓰기가 원본을 만들었으면
		// rename 이 그것을 덮어써 판단을 잃는다. 청구본을 그대로 두면 다음 흡수가 집는다.
		return fmt.Sprintf("%s 를 옮기지 못해 청구본 %s 를 남겼다: %v",
			clip(src, 200), base+".claimed-"+sfx, err)
	}
	// 보존 이름에 청구 유일값을 함께 넣는다 — 같은 초에 둘이 끝나면 이름이 겹쳐
	// 뒤엣것이 앞엣것을 덮어쓰고, 그러면 보존한다면서 기록을 지우게 된다.
	done := filepath.Join(dir, base+".migrated-"+o.stamp().UTC().Format("20060102T150405Z")+"-"+sfx)
	if err := os.Rename(claimed, done); err != nil {
		return fmt.Sprintf("%s 를 옮겼지만 보존 이름으로 못 바꿨다 — 청구본 %s 가 남는다: %v",
			clip(src, 200), base+".claimed-"+sfx, err)
	}
	return ""
}

// movePending 은 청구한 대기열 파일의 줄을 고정 자리로 옮긴다.
//
// Append 가 키 중복 검사를 하므로 재시도가 겹쳐도 한 줄이다.
//
// ★ 읽기 오류는 **읽은 데까지 옮긴 뒤에** 올린다. 깨진 줄 하나 때문에 앞의 멀쩡한 줄을
// 통째로 남기면 그쪽이 더 나쁘다. 오류를 올리면 청구본이 남고 다음 흡수가 다시 집는데,
// 그때도 같은 자리에서 깨지므로 **사람이 볼 때까지 사유가 계속 나온다** — 그것이 옳다.
// 조용히 지우면 재생성 불가한 판단이 사라진다(§9).
func (o *Outbox) movePending(claimed string) error {
	es, rerr := readEntries(claimed)
	for _, e := range es {
		if err := o.Append(e); err != nil {
			return err
		}
	}
	return rerr
}

// moveRejected 는 청구한 격리 파일의 줄을 고정 보관소로 옮긴다.
//
// 격리는 큐가 아니라 보관소라 순서·중복의 의미가 다르다. 다만 재시도가 겹쳤을 때
// 같은 줄이 두 번 남지 않게 **이미 보관된 키는 건너뛴다** — 한 번 격리된 키가 큐로
// 돌아가는 경로는 없으므로 키가 같으면 같은 줄이다.
func (o *Outbox) moveRejected(claimed string) error {
	rs, rerr := readRejected(claimed)
	have := map[string]bool{}
	if cur, err := o.Rejected(); err == nil {
		for _, r := range cur {
			have[r.Entry.Key] = true
		}
	}
	for _, r := range rs {
		if have[r.Entry.Key] {
			continue
		}
		if err := o.quarantine(r); err != nil {
			return err
		}
		have[r.Entry.Key] = true
	}
	return rerr
}

// adopt 는 옛 채널별 자리에 남은 줄을 고정 자리로 흡수한다. 못 한 것을 사유로 낸다.
//
// 실패해도 **다음 자리로 계속 간다** — 한 자리가 막혔다고 나머지를 인질로 잡지 않는다
// (Replay 가 영구 거절에 대해 내린 것과 같은 판정이다).
func (o *Outbox) adopt() []string {
	var notes []string
	for _, dir := range o.legacy {
		for _, base := range []string{pendingName, rejectedName} {
			move := o.movePending
			if base == rejectedName {
				move = o.moveRejected
			}
			for _, src := range claimables(dir, base) {
				if n := o.claimAndDrain(src, dir, base, move); n != "" {
					notes = append(notes, n)
				}
			}
		}
	}
	return notes
}
```

`outbox.go` 의 import 에 `"sort"` 를 넣는다. `crypto/rand`·`encoding/hex`·`fmt`·`os`·`path/filepath`·`time` 은 이미 있다.

- [ ] **Step 4: 통과를 확인한다**

Run: `cd plugins/flightdeck/server && go test ./cmd/fd/ -run 'TestAdopt|TestConcurrentAdopt' -race -count=1 -v`
Expected: PASS 6건. `TestConcurrentAdoptClaimsOnce` 는 `-race` 로 돌려야 의미가 있다.

- [ ] **Step 5: 커밋**

```bash
git add plugins/flightdeck/server/cmd/fd/outbox.go \
        plugins/flightdeck/server/cmd/fd/outbox_adopt_test.go
git commit -m "feat(flightdeck): adopt — 옛 채널 자리의 판단을 원자적 청구로 흡수한다

os.Rename 청구가 핵심이다. 판단 POST 의 멱등 표 TTL 이 24시간이고 판단은 추가 전용이라,
잠금 없이 흡수하면 중복 사본이 24시간을 넘겨 재생될 때 되돌릴 수 없는 한 줄이 생긴다.

실패하면 원본으로 되돌리지 않는다 — 그 사이 새 오프라인 쓰기가 원본을 만들었으면
rename 이 그것을 덮어써 판단을 잃는다. 청구본을 남기고 다음 흡수가 고아를 집는다."
```

---

### Task 6: `Replay` 가 흡수를 먼저 돌린다

**Files:**
- Modify: `plugins/flightdeck/server/cmd/fd/outbox.go` (`Replay` 296-367행)
- Test: `plugins/flightdeck/server/cmd/fd/outbox_adopt_test.go` (추가)

**Interfaces:**
- Consumes: `adopt`(과제 5)
- Produces: `ReplayResult.Detail` 이 흡수 사유를 함께 나른다. 없으면 지금과 같다.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`outbox_adopt_test.go` 에 붙인다:

```go
// 재생이 흡수를 **먼저** 돌린다. 안 그러면 옛 자리의 줄은 이번 재생에서 안 나가고,
// 그 채널이 다시 돌 때까지 기다린다 — 안 돌면 영영이다.
func TestReplayAdoptsBeforeSending(t *testing.T) {
	o := mkOutbox(t)
	legacy := filepath.Join(t.TempDir(), "chan", "outbox")
	o.legacy = []string{legacy}
	mkAdoptable(t, legacy, "old1", "old2")

	var sent []string
	res, err := o.Replay(context.Background(), func(_ context.Context, e OutboxEntry) error {
		sent = append(sent, e.Key)
		return nil
	})
	if err != nil {
		t.Fatalf("재생 실패: %v", err)
	}
	if len(sent) != 2 {
		t.Fatalf("보낸 것이 %d건이다 — 흡수가 재생 앞에 안 돈 것이다: %v", len(sent), sent)
	}
	if res.Sent != 2 {
		t.Errorf("Sent 가 %d 다 — 2 여야 한다", res.Sent)
	}
}

// 흡수가 못 한 것이 있으면 **재생 결과에 사유가 실린다.** 침묵하면 판단이 조용히 갇힌다.
func TestReplayReportsAdoptFailures(t *testing.T) {
	o := mkOutbox(t)
	legacy := filepath.Join(t.TempDir(), "chan", "outbox")
	o.legacy = []string{legacy}
	mkAdoptable(t, legacy, "k1")

	if err := os.MkdirAll(filepath.Dir(o.dir), 0o755); err != nil {
		t.Fatalf("상위 디렉토리를 못 만들었다: %v", err)
	}
	if err := os.WriteFile(o.dir, []byte("나는 디렉토리가 아니다"), 0o600); err != nil {
		t.Fatalf("막이 파일을 못 만들었다: %v", err)
	}

	res, _ := o.Replay(context.Background(), func(context.Context, OutboxEntry) error { return nil })
	if !strings.Contains(res.Detail, "흡수") {
		t.Errorf("흡수가 실패했는데 재생 사유가 그것을 안 말한다: %q", res.Detail)
	}
}
```

import 에 `"context"` 를 넣는다.

- [ ] **Step 2: 실패를 확인한다**

Run: `cd plugins/flightdeck/server && go test ./cmd/fd/ -run 'TestReplayAdopts|TestReplayReports' -count=1`
Expected: FAIL — 흡수가 안 돌아 `보낸 것이 0건이다`

- [ ] **Step 3: `Replay` 에 흡수를 넣는다**

`Replay` 의 첫 두 줄을 바꾼다:

```go
func (o *Outbox) Replay(ctx context.Context, send func(context.Context, OutboxEntry) error) (ReplayResult, error) {
	// ★ 흡수를 **먼저** 돈다. 옛 채널 자리의 줄을 여기서 안 집으면 그 줄은 이번 재생에서
	// 안 나가고, 그 채널이 다시 돌 때까지 기다린다 — 안 돌면 영영이다.
	adopted := o.adopt()
	entries, err := o.List()
	if err != nil {
		return ReplayResult{Detail: withAdopt("", adopted)}, err
	}
	if len(entries) == 0 {
		return ReplayResult{Detail: withAdopt("대기 중인 판단이 없다", adopted)}, nil
	}
```

그리고 `Replay` 끝의 `switch` 뒤, `return res, nil` 앞에 한 줄 넣는다:

```go
	res.Detail = withAdopt(res.Detail, adopted)
	return res, nil
```

`keep` 실패로 일찍 도는 가지(352-354행)도 사유를 잃지 않게 고친다:

```go
	if err := o.keep(left); err != nil {
		return ReplayResult{Sent: sent, Remaining: len(left), Rejected: rejected,
			Detail: withAdopt("", adopted)}, err
	}
```

그리고 helper 를 `adopt` 옆에 둔다:

```go
// withAdopt 는 재생 사유에 흡수가 못 한 것을 붙인다.
//
// 별도 필드를 만들지 않는 이유: 이 값을 읽는 자리가 전부 사람에게 보여 주는 문장이라
// (fd doctor · SessionStart 배너 · MCP 열화 사유) 필드를 늘리면 소비자마다
// 붙일지 말지를 각자 정하게 되고, 그러면 어딘가에서 조용히 사라진다.
func withAdopt(detail string, notes []string) string {
	if len(notes) == 0 {
		return detail
	}
	msg := "옛 자리 흡수에서 못 한 것: " + strings.Join(notes, " · ")
	if strings.TrimSpace(detail) == "" {
		return msg
	}
	return detail + " · " + msg
}
```

- [ ] **Step 4: 통과를 확인한다**

Run: `cd plugins/flightdeck/server && go test ./cmd/fd/ -run 'TestReplay|TestAdopt|TestConcurrent|TestPermanent|TestReachable|TestAlwaysFailing|TestOldEntries|TestLongOffline' -race -count=1`
Expected: PASS 전부. 기존 재생 시험이 빨개지면 회귀다.

- [ ] **Step 5: 커밋**

```bash
git add plugins/flightdeck/server/cmd/fd/outbox.go \
        plugins/flightdeck/server/cmd/fd/outbox_adopt_test.go
git commit -m "feat(flightdeck): 재생이 흡수를 먼저 돌리고 못 한 것을 사유에 싣는다"
```

---

### Task 7: `fd doctor` 가 자리·잔량·**못 보는 범위**를 찍는다

**Files:**
- Modify: `plugins/flightdeck/server/cmd/fd/outbox.go` (`LegacyLeftovers` 추가)
- Modify: `plugins/flightdeck/server/cmd/fd/cmds.go:444-464`
- Test: `plugins/flightdeck/server/cmd/fd/outbox_adopt_test.go` (추가)

**Interfaces:**
- Consumes: `Outbox.legacy`·`Dir()`·`Source()`(과제 4)
- Produces: `type Leftover struct { Dir string; Pending, Rejected, Claimed int; Err string }` · `func (o *Outbox) LegacyLeftovers() []Leftover` — **읽기만 한다.**

- [ ] **Step 1: 실패하는 시험을 쓴다**

`outbox_adopt_test.go` 에 붙인다:

```go
// doctor 가 옛 자리 잔량을 세되 **아무것도 옮기지 않는다.** 진단이 부작용을 가지면
// "찍어 봤더니 상태가 달라졌다"가 되고, 그러면 진단을 믿을 수 없다.
func TestLegacyLeftoversCountsWithoutMoving(t *testing.T) {
	o := mkOutbox(t)
	legacy := filepath.Join(t.TempDir(), "chan", "outbox")
	o.legacy = []string{legacy}
	mkAdoptable(t, legacy, "k1", "k2")

	got := o.LegacyLeftovers()
	if len(got) != 1 {
		t.Fatalf("잔량 보고가 %d건이다 — 1건이어야 한다", len(got))
	}
	if got[0].Pending != 2 {
		t.Errorf("대기 %d건으로 셌다 — 2건이어야 한다", got[0].Pending)
	}
	if _, err := os.Stat(filepath.Join(legacy, pendingName)); err != nil {
		t.Errorf("원본이 사라졌다 — 진단이 옮겼다: %v", err)
	}
	if got := keysOf(t, o); len(got) != 0 {
		t.Errorf("고정 자리에 %v 가 생겼다 — 진단이 옮겼다", got)
	}
}

// 옛 자리가 없으면 보고도 없다. 없는 것을 있다고 찍으면 사람이 헛것을 쫓는다.
func TestLegacyLeftoversIsEmptyWhenNothingLeft(t *testing.T) {
	o := mkOutbox(t)
	o.legacy = []string{filepath.Join(t.TempDir(), "nope", "outbox")}
	if got := o.LegacyLeftovers(); len(got) != 0 {
		t.Errorf("빈 자리를 %d건으로 보고했다: %+v", len(got), got)
	}
}
```

그리고 doctor 출력 시험을 `machine_identity_test.go` 옆의 스타일로 새 파일에 둔다 — 여기서는 `outbox_adopt_test.go` 에 붙인다:

```go
// doctor 는 자리와 **못 보는 범위**를 함께 찍는다.
//
// ★ 후자가 이 시험의 핵심이다. 옛 자리 목록은 이 채널이 계산할 수 있는 것만이라
// (LegacyOutboxDirs), 다른 채널의 자리는 여기서 원리적으로 안 보인다. 그 사실을 안 찍으면
// "0건"이 '깨끗하다'로 읽히고, 그것은 안 잰 축을 잰 척하는 것이다(§13).
func TestDoctorReportsOutboxPlaceAndItsOwnBlindness(t *testing.T) {
	h := newHarness(t)
	code, out := h.run("", "doctor")
	if code != 0 {
		t.Fatalf("doctor 가 %d 로 끝났다:\n%s", code, out)
	}
	dir, _ := OutboxPath(envOf(h.env), homeDir(envOf(h.env)))
	if !strings.Contains(out, dir) {
		t.Errorf("doctor 가 아웃박스 자리(%s)를 안 찍었다:\n%s", dir, out)
	}
	if !strings.Contains(out, "채널") {
		t.Errorf("doctor 가 못 보는 범위를 안 말한다 — 0건이 '깨끗하다'로 읽힌다:\n%s", out)
	}
}
```

- [ ] **Step 2: 실패를 확인한다**

Run: `cd plugins/flightdeck/server && go test ./cmd/fd/ -run 'TestLegacyLeftovers|TestDoctorReportsOutbox' -count=1`
Expected: FAIL — `o.LegacyLeftovers undefined`

- [ ] **Step 3: `LegacyLeftovers` 를 구현한다**

`outbox.go` 의 `adopt` 뒤에 넣는다:

```go
// Leftover 는 옛 자리 하나에 아직 남아 있는 것이다.
type Leftover struct {
	Dir      string
	Pending  int    // 대기열 줄 수
	Rejected int    // 격리 줄 수
	Claimed  int    // 고아 청구본 파일 수 — 앞선 흡수가 도중에 죽은 흔적이다
	Err      string // 셀 수 없었으면 그 사유. 비어 있을 수 있다
}

// LegacyLeftovers 는 옛 자리에 남은 것을 **읽기만 해서** 센다.
//
// ★ 옮기지 않는다. 진단이 부작용을 가지면 "찍어 봤더니 상태가 달라졌다"가 되고,
// 그러면 진단을 믿을 수 없다. 흡수는 Replay 경로에서만 돈다.
func (o *Outbox) LegacyLeftovers() []Leftover {
	var out []Leftover
	for _, dir := range o.legacy {
		lo := Leftover{Dir: dir}
		if es, err := readEntries(filepath.Join(dir, pendingName)); err != nil {
			lo.Err = err.Error()
		} else {
			lo.Pending = len(es)
		}
		if rs, err := readRejected(filepath.Join(dir, rejectedName)); err != nil {
			lo.Err = strings.TrimSpace(lo.Err + " " + err.Error())
		} else {
			lo.Rejected = len(rs)
		}
		for _, base := range []string{pendingName, rejectedName} {
			if cs, err := filepath.Glob(filepath.Join(dir, base+".claimed-*")); err == nil {
				lo.Claimed += len(cs)
			}
		}
		if lo.Pending == 0 && lo.Rejected == 0 && lo.Claimed == 0 && lo.Err == "" {
			continue // 빈 자리는 안 찍는다 — 없는 것을 찍으면 사람이 헛것을 쫓는다
		}
		out = append(out, lo)
	}
	return out
}
```

- [ ] **Step 4: `runDoctor` 의 아웃박스 절을 바꾼다**

`cmds.go:444-464` 의 아웃박스·격리 부분을 이것으로 교체한다(`처방 채널` 줄 다음부터):

```go
	// ★ 아웃박스는 이제 **채널 무관한 고정 자리**에 있다(OutboxPath). 그래서 이 줄이 세는
	// 것은 이 채널의 대기가 아니라 **이 머신의 대기**다 — 예전에는 채널마다 달랐다.
	// 자리와 사유를 함께 찍는 것은 그대로다: 값이 예상과 다를 때 "왜 저기냐"에 답할 자리다.
	if pend, err := a.cli.Outbox.List(); err != nil {
		fmt.Fprintf(out, "  ! 아웃박스를 못 읽었다: %v\n", err)
	} else {
		fmt.Fprintf(out, "  아웃박스 대기 %d건 (%s · %s)\n",
			len(pend), a.cli.Outbox.Dir(), a.cli.Outbox.Source())
	}
	// 격리된 판단은 **버려진 것이 아니라 옮겨진 것**이다. 안 찍으면 조용히 사라진 것과 같다.
	if rej, err := a.cli.Outbox.Rejected(); err != nil {
		fmt.Fprintf(out, "  ! 격리 파일을 못 읽었다: %v\n", err)
	} else if len(rej) > 0 {
		fmt.Fprintf(out, "  ! 격리된 판단 %d건 — 영구 거절이라 큐에서 뺐다(버리지 않았다)\n", len(rej))
		for _, r := range rej {
			fmt.Fprintf(out, "      %s · %s\n", r.At.Format(time.RFC3339), clip(r.Reason, 200))
		}
	}
	// ★ 옛 채널별 자리에 남은 것과 **이 목록이 못 보는 범위**를 함께 찍는다.
	// 후자를 빼면 "0건"이 '깨끗하다'로 읽히는데, 그것은 안 잰 축을 잰 척하는 것이다(§13).
	for _, lo := range a.cli.Outbox.LegacyLeftovers() {
		fmt.Fprintf(out, "  ! 옛 자리에 남았다 %s — 대기 %d · 격리 %d · 청구본 %d (다음 재생이 흡수한다)\n",
			lo.Dir, lo.Pending, lo.Rejected, lo.Claimed)
		if lo.Err != "" {
			fmt.Fprintf(out, "      ! 세다 걸렸다: %s\n", clip(lo.Err, 200))
		}
	}
	fmt.Fprintln(out, "  옛 자리 탐색은 **이 채널이 계산할 수 있는 자리**만이다 — "+
		"다른 채널(훅·MCP 는 CLAUDE_PLUGIN_DATA, 셸은 XDG_STATE_HOME)의 자리는 여기서 안 보인다.")
```

- [ ] **Step 5: 통과를 확인한다**

Run: `cd plugins/flightdeck/server && go test ./cmd/fd/ -count=1`
Expected: `TestOfflineStateLandsUnderPluginDataNotPluginRoot` 하나만 빨강(과제 8에서 고친다).

`fd doctor` 출력을 눈으로 본다:
```bash
cd plugins/flightdeck/server && go run ./cmd/fd doctor 2>&1 | head -25
```
Expected: `아웃박스 대기 0건 (/home/aaron/.flightdeck/outbox · ~/.flightdeck — 채널 환경과 무관한 고정 자리)` 와 옛 자리 잔량 한 줄(이 머신에는 `~/.local/state/flightdeck/outbox` 에 격리 1건이 있다), 그리고 사각 문장.

- [ ] **Step 6: 커밋**

```bash
git add plugins/flightdeck/server/cmd/fd/outbox.go plugins/flightdeck/server/cmd/fd/cmds.go \
        plugins/flightdeck/server/cmd/fd/outbox_adopt_test.go
git commit -m "feat(flightdeck): doctor 가 아웃박스 자리·옛 자리 잔량·못 보는 범위를 찍는다

잔량 세기는 읽기만 한다 — 진단이 부작용을 가지면 진단을 믿을 수 없다.
'못 보는 범위' 문장이 핵심이다. 빼면 0건이 '깨끗하다'로 읽힌다(§13)."
```

---

### Task 8: 반증된 주석과 설계를 고치고, 열화 경로 시험을 갈라진 축에 맞춘다

**Files:**
- Modify: `plugins/flightdeck/server/cmd/fd/config.go:11-21`
- Modify: `plugins/flightdeck/server/cmd/fd/env.go:20-27, 74-76`
- Modify: `plugins/flightdeck/server/cmd/fd/degrade_path_test.go:330-401`
- Modify: `plugins/flightdeck/DESIGN.md:398`
- Modify: `docs/superpowers/specs/2026-08-05-fd-outbox-per-channel-design.md`

**Interfaces:**
- Consumes: 앞 과제 전부
- Produces: 없음(문서·주석·시험만)

- [ ] **Step 1: `config.go` 의 반증된 주장을 고친다**

`config.go:13-17` 의 두 문장을 바꾼다:

```go
// ★ 왜 상태 디렉토리가 아닌가. ResolveStateDir 은 CLAUDE_PLUGIN_DATA(훅·MCP 프로세스에만
// Claude Code 가 넣어 준다)와 XDG_STATE_HOME|~/.local/state(사용자 셸)로 **일부러 갈리게**
// 만든 축이다 — **재생성 가능한 열화 상태(캐시)** 는 채널마다 따로여도 되기 때문이다.
//
// ★ 이 주석은 한 번 틀렸다. 예전에는 "캐시·아웃박스는 채널마다 따로여도 된다"고 적었는데,
// 아웃박스는 설계 §7 이 "재생성 불가한 유일한 자산"이라 부른 것을 담는다. 가르는 축은
// **열화 여부가 아니라 재생성 가능성**이고, 아웃박스는 그래서 고정 자리로 갔다(OutboxPath).
//
// 서버 주소는 정반대 요구다: **같은 머신이면 어느 채널에서 물어도 같아야 한다.**
// 갈린 자리에 두면 셸에서 설정한 주소를 훅·MCP 가 못 보고, 그 반대도 마찬가지다.
```

- [ ] **Step 2: `env.go` 의 `StateDir` 주석을 좁힌다**

`env.go:20-23` 을 바꾼다:

```go
// StateDir 는 **재생성 가능한** 열화 상태(캐시)를 두는 자리다.
//
// ★ 아웃박스는 여기 없다. 그것은 재생성 불가한 판단을 담아서 채널마다 갈리면 안 되고,
// 그래서 OutboxPath 가 고정 자리를 준다 — 그 주석에 판정 전문이 있다.
//
// ★ ${CLAUDE_PLUGIN_ROOT} 에 두지 않는다. 그 경로에는 플러그인 **버전이 들어가서**
// 갱신될 때마다 자리가 바뀌고, 그러면 쌓아 둔 캐시가 갱신 한 번에 사라진다(설계 §7).
```

그리고 `env.go:74-76`(MachineIDPath 주석의 "두 축은 요구가 정반대다" 문단)을 이렇게 좁힌다:

```go
// 두 축은 요구가 정반대다. 상태 디렉토리는 "**재생성 가능한** 열화 상태(캐시)가 플러그인
// 갱신을 넘어 살아남는가"라 환경 의존이 **설계 의도**다(설계 §7). machine id 는
// "같은 머신이면 같은가"라 환경 의존이 **곧 결함**이다. 둘을 한 디렉토리에 뭉갠 것이
// 이 사고의 전부다. (아웃박스도 같은 이유로 나중에 떨어져 나갔다 — OutboxPath 를 보라.)
```

- [ ] **Step 3: `DESIGN.md` §7 의 398행을 가른다**

```markdown
- 상태를 두는 자리는 **재생성 가능한가**로 가른다.
  - **캐시** — `${CLAUDE_PLUGIN_DATA}` 아래(`${CLAUDE_PLUGIN_ROOT}` 는 업데이트마다 경로가 바뀐다).
    채널마다 갈려도 된다. 잃어도 다시 만들면 된다.
  - **아웃박스·격리** — `~/.flightdeck/outbox`, **채널 환경과 무관한 고정 자리**.
    상태 디렉토리는 훅·MCP(`CLAUDE_PLUGIN_DATA`)와 사용자 셸(`XDG_STATE_HOME`)로 갈리는데,
    거기 두면 **셸에서 쌓인 판단을 훅·MCP 가 영영 못 보낸다**(2026-08-03 실측: 판단 하나가
    그렇게 갇혔다). `machine-id`·`config.json` 이 이미 같은 이유로 같은 자리에 있다.
    옛 자리에 남은 줄은 `os.Rename` 청구로 흡수한다 — 두 프로세스가 같은 줄을 각자 재생하면
    멱등 표 TTL(24시간)을 넘긴 순간 되돌릴 수 없는 판단 한 줄이 생긴다.
```

- [ ] **Step 4: 열화 경로 시험을 갈라진 축에 맞춘다**

`degrade_path_test.go:330-338` 의 절 제목과 주석을 바꾼다:

```go
// ─────────────────────────────────────────────────────────────────────────────
// ③ 상태 파일은 ${CLAUDE_PLUGIN_ROOT} 아래가 **아니다** — 축은 재생성 가능성으로 갈린다
// ─────────────────────────────────────────────────────────────────────────────

// ${CLAUDE_PLUGIN_ROOT} 에는 플러그인 **버전이 들어간다**(설계 §13). 갱신되면 경로가
// 바뀌고 옛 자리는 지워지므로, 거기 쌓인 것은 갱신 한 번에 사라진다.
//
// ★ 단정의 좌표계는 판정 함수가 아니라 **파일시스템에 실제로 생긴 파일**이다.
// ResolveStateDir·OutboxPath 단위 시험은 "무엇을 고르는가"만 보고, 소비자(캐시·아웃박스)가
// 그 값을 실제로 쓰는지는 원리적으로 못 본다.
//
// ★ 두 소비자가 **서로 다른 자리로 간다**는 것이 이 시험이 지키는 둘째 축이다:
//   - 캐시는 재생성 가능하니 CLAUDE_PLUGIN_DATA 아래(채널마다 갈려도 된다)
//   - 아웃박스는 재생성 불가한 판단을 담으니 고정 자리(OutboxPath)
// 예전에는 둘 다 plugin-data 아래라고 단정했는데, 그것이 이 항목이 없앤 결함이다.
func TestOfflineStateSplitsByRegenerabilityAndNeverLandsUnderPluginRoot(t *testing.T) {
```

`379-382행`의 아웃박스 단정을 바꾼다:

```go
	// ── L1 쓰기: 아웃박스는 **고정 자리**로 간다(plugin-data 아래가 아니다) ──
	outDir, _ := OutboxPath(envOf(env), homeDir(envOf(env)))
	pending := filepath.Join(outDir, pendingName)
	if _, err := os.Stat(pending); err != nil {
		t.Fatalf("아웃박스가 %s 에 없다 — 판단이 어디에 쌓였는지 모른다: %v", pending, err)
	}
	// ★ 갈렸는지를 직접 본다. 같은 자리로 가면 이 시험은 축이 갈린 것을 못 본다.
	if strings.HasPrefix(filepath.Clean(outDir), filepath.Clean(filepath.Join(data, "flightdeck"))) {
		t.Errorf("아웃박스가 아직 상태 디렉토리 아래다(%s) — 채널마다 갈린다", outDir)
	}
```

**368-372행의 캐시 단정과 384-400행의 PLUGIN_ROOT 순회는 손대지 않는다.** 이 시험의 본 판정이 후자다.

import 에 `"strings"` 가 없으면 넣는다.

- [ ] **Step 5: 설계 문서에 구현 중 드러난 사실을 덧붙인다**

`docs/superpowers/specs/2026-08-05-fd-outbox-per-channel-design.md` 의 §4 끝에 붙인다:

```markdown
### 구현 중 드러난 것 (2026-08-05)

- **하네스가 진짜 홈에 쓰고 있었다.** `degrade_path_test.go` 가 `unpinnedEnv` 대신 손으로
  env 를 만들어 `TestUnpinnedEnvNeverReachesTheRealHome` 의 감시를 우회했고, 그래서
  `os.UserHomeDir()` 로 떨어져 프로세스의 진짜 홈에 `machine-id` 를 만들었다(실측 확인).
  흡수가 `~/.local/state` 를 훑으므로 그대로 두면 **시험이 개발자의 진짜 판단을 옮긴다.**
  하네스 기본 env 에 `HOME` 을 고정하는 것이 이 작업의 첫 과제가 된 이유다.
- **깨진 줄은 영원히 사유를 낸다.** 옛 자리 파일에 해석 불가한 줄이 있으면 흡수가 읽은
  데까지 옮기고 청구본을 남기며, 다음 흡수도 같은 자리에서 걸린다. **그것이 옳다** —
  조용히 지우면 재생성 불가한 판단이 사라진다(§9). 사람이 볼 때까지 doctor 가 찍는다.
- **보존 이름에 청구 유일값을 함께 넣는다.** 시각만 쓰면 같은 초에 끝난 둘이 이름이 겹쳐
  뒤엣것이 앞엣것을 덮어쓰고, 그러면 보존한다면서 기록을 지우게 된다.
- **격리 흡수는 키로 중복을 거른다.** 한 번 격리된 키가 큐로 돌아가는 경로는 없으므로
  키가 같으면 같은 줄이다. 재시도가 겹쳐도 보관소에 같은 줄이 두 번 안 남는다.
```

- [ ] **Step 6: 전 시험 초록을 확인한다**

Run:
```bash
cd plugins/flightdeck/server && go build ./... && go vet ./... && gofmt -l . && go test ./... -race -count=1
```
Expected: `gofmt -l .` 출력 없음, 전 패키지 ok.

- [ ] **Step 7: 커밋**

```bash
git add plugins/flightdeck/server/cmd/fd/config.go plugins/flightdeck/server/cmd/fd/env.go \
        plugins/flightdeck/server/cmd/fd/degrade_path_test.go plugins/flightdeck/DESIGN.md \
        docs/superpowers/specs/2026-08-05-fd-outbox-per-channel-design.md
git commit -m "docs(flightdeck): 반증된 '캐시·아웃박스는 갈려도 된다'를 전 자리에서 고친다

이 레포는 주석이 판단의 저장소라, 틀린 주석을 남기면 다음 사람이 그 위에서 돈다.
축은 열화 여부가 아니라 재생성 가능성이었다.

degrade_path 시험은 본 판정(PLUGIN_ROOT 아래에 아무것도 안 생긴다)을 그대로 두고
아웃박스 단정만 고정 자리로 옮겼다. 보호를 약화한 것이 아니라 갈라진 두 축을
시험에서도 가른 것이다."
```

---

### Task 9: 실물로 확인하고 마무리한다

**Files:** 없음(검증만)

**Interfaces:**
- Consumes: 과제 1-8 전부
- Produces: 없음

- [ ] **Step 1: 이 머신의 실제 흡수를 눈으로 본다**

이 머신에는 `~/.local/state/flightdeck/outbox/rejected.jsonl` 에 격리 1건이 있다(8/3 판단, 409).

```bash
cd plugins/flightdeck/server
echo "== 흡수 전"; ls -la ~/.local/state/flightdeck/outbox/ ~/.flightdeck/outbox/ 2>&1 | head -20
go run ./cmd/fd doctor 2>&1 | head -25
```
Expected: doctor 가 옛 자리 잔량 `격리 1` 을 찍는다. **doctor 는 안 옮긴다.**

- [ ] **Step 2: 재생을 한 번 돌려 흡수를 일으킨다**

```bash
cd plugins/flightdeck/server && go run ./cmd/fd status >/dev/null 2>&1
echo "== 흡수 후"; ls -la ~/.local/state/flightdeck/outbox/ ~/.flightdeck/outbox/ 2>&1
go run ./cmd/fd doctor 2>&1 | grep -A3 '격리\|옛 자리'
```
Expected:
- `~/.flightdeck/outbox/rejected.jsonl` 에 그 1건이 생긴다.
- `~/.local/state/flightdeck/outbox/rejected.jsonl.migrated-*` 가 생기고 **정규 이름은 사라진다.**
- doctor 가 격리 1건을 고정 자리에서 찍고, 옛 자리 잔량 줄은 사라진다.

**원본이 지워졌으면 그것은 결함이다** — `.migrated-*` 로 남아야 한다.

- [ ] **Step 3: 전 검증을 한 번 더 돌린다**

```bash
cd plugins/flightdeck/server && go build ./... && go vet ./... && gofmt -l . && go test ./... -race -count=1
```
Expected: 전부 통과, `gofmt -l` 무출력.

- [ ] **Step 4: 시험이 진짜 홈을 안 건드리는지 마지막으로 확인한다**

```bash
cd plugins/flightdeck/server
SB=$(mktemp -d) && env HOME="$SB" GOMODCACHE="$(go env GOMODCACHE)" GOCACHE="$(go env GOCACHE)" \
  go test ./... -count=1 >/dev/null 2>&1; echo "가짜 홈에 생긴 파일:"; find "$SB" -type f
chmod -R u+w "$SB" 2>/dev/null; rm -rf "$SB"
```
Expected: **아무것도 안 나온다.**

- [ ] **Step 5: 커밋이 필요하면 하고, 판단을 남긴다**

검증 과정에서 고친 것이 있으면 커밋한다. 그리고 `fd note` 로 `verified` 판단을 남긴다 —
무엇을 실제로 돌려 확인했고 무엇은 못 쟀는지를 함께 적는다.

## 자체 검토

**스펙 범위 대조** — 설계 문서의 절마다 담당 과제가 있는지:

| 스펙 절 | 과제 |
|---|---|
| §3 자리(`OutboxPath`, `StateDir` 은 캐시만) | 2, 4, 8 |
| §4 이전(청구·흡수·보존·중간 실패) | 5 |
| §4 24시간 TTL 구멍 | 5 (`TestConcurrentAdoptClaimsOnce`) |
| §4 순서는 뒤에 붙인다 | 5 (`movePending` 이 `Append` 로 뒤에 붙인다) |
| §5 후보 목록·고아·`.migrated-*` 제외 | 3, 5 |
| §5 정직한 구멍(doctor 가 사각을 찍는다) | 7 |
| §6 반증된 주석 6자리 | 8 |
| §7 시험 6종 + 기존 시험 정정 | 2, 5, 6, 7, 8 |
| §8 안 하는 것 | 3(tmp 제외·glob 안 함), 계획 전체가 `internal/` 을 안 건드린다 |

**빠졌던 것을 채웠다**: 스펙에 없던 **하네스 HOME 고정**(과제 1)이 필요하다는 것이 구현 준비 중 실측으로 드러났다. 과제 8 Step 5 가 그 사실을 스펙에 되먹인다.

**타입 일관성**: `Outbox.dir`·`source`·`legacy`·`now` 는 과제 4에서 정의하고 5·6·7이 그대로 쓴다. `readEntries`·`readRejected` 는 과제 4에서 만들고 5·7이 쓴다. `pendingName`·`rejectedName` 는 과제 4에서 정의하고 5·7·8이 쓴다. `claimSuffix`·`claimables`·`claimAndDrain`·`movePending`·`moveRejected`·`adopt` 는 과제 5, `withAdopt` 는 과제 6, `Leftover`·`LegacyLeftovers` 는 과제 7이다. 시험 도우미 `mkAdoptable`·`keysOf` 는 과제 5에서 만들고 6·7이 쓴다. `entry(key)` 와 `mkOutbox(t)` 는 `outbox_stuck_test.go` 의 기존 것을 쓴다.
