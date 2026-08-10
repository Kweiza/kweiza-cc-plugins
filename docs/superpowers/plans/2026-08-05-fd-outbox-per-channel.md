# 아웃박스 채널 무관 고정 자리 — 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 새 판단은 채널 무관한 고정 자리(`~/.flightdeck/outbox`)에 쌓고, 옛 채널별 자리에 남은 큐는 **옮기지 말고 재생이 함께 돌려 전송으로 비운다.**

**Architecture:** `env.go` 에 `OutboxPath`·`LegacyOutboxDirs` 순수 함수를 둔다(`MachineIDPath`·`ConfigPath` 와 같은 규칙의 셋째 적용). `Outbox` 는 `path` 대신 `dir` 을 들고, 옛 자리마다 같은 타입의 값을 하나씩 만들어 **기존 `Replay` 를 그대로** 돌린다. 새 기구가 없다 — 청구·고아·보존본이라는 개념 자체가 없다.

**Tech Stack:** Go, 표준 라이브러리만. 시험은 `go test ./... -race`.

> **⚠ 실행 중 정정 (2026-08-05).** 과제 3의 `LegacyOutboxDirs` 는 `<tmp>/flightdeck/outbox` 를
> 후보에 **넣지 않는다.** 아래 과제 3 본문과 과제 5 시험의 tmp 관련 대목은 이 정정이 이긴다.
> 사유: 과제 5가 옛 자리를 "읽어서 사용자 토큰으로 POST 하는" 자리로 만드는데, `/tmp` 는 부모가
> world-writable 이라 다른 로컬 사용자가 `0644` 로 줄을 심을 수 있고 판단은 추가 전용이라 회수가 안 된다.
> 계획이 적었던 방어("아웃박스 파일은 `0600` 이라 읽기가 실패한다")는 *남의 보호된 파일을 내가 읽는*
> 방향만 막는다. 전문은 스펙 §5 에 3판까지의 왕복이 다 적혀 있다.
>
> **⚠ 이 계획은 2판이다.** 1판은 옛 자리 파일을 `os.Rename` 으로 "청구"해 고정 자리로 흡수하려 했고, 그 설계가 적대적 검토에서 **반증됐다**(재현: `-race -count=50` 중 28회 실패). 무엇이 왜 틀렸는지는 스펙 §4 의 "왜 옮기지 않기로 했나"에 있다. **읽고 시작해라** — 안 읽으면 "왜 이렇게 안 하고 저렇게 했나"를 다시 묻게 되고, 최악은 청구를 되살리러 간다.

## Global Constraints

- **모듈 루트**: `plugins/flightdeck/server`. 모든 `go` 명령은 이 디렉토리에서 돈다.
- **작업 트리**: `/home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-outbox-per-channel` (브랜치 `fd-outbox-per-channel`).
- **`cmd/fd` 비시험 코드에 `sync`·`atomic` 을 새로 들이지 않는다.** `client.go:67-83` 이 "한 번에 한 호출" 전제를 명시했고 `internal/mcpsrv` 의 `TestServeNeverOverlapsBackend` 가 지킨다.
- **`server/internal/` 은 하나도 안 만진다.** 겹치는 세션들에 그렇게 알렸다.
- **판정은 순수 함수로 빼고 환경 조회를 인자로 받는다**(`env.go:16-18`). `os.Getenv` 를 본문에 박지 않는다.
- **조용히 버리는 것이 하나도 없어야 한다**(설계 §9).
- **주석은 "무엇을"이 아니라 "왜 그렇게 정했나"를 적는다.** 이 레포는 주석이 판단의 저장소다.
- 매 과제 끝에 커밋한다. 검증은 `go build ./... && go vet ./... && gofmt -l . && go test ./... -race`.
  **`go build ./...` 는 `_test.go` 를 안 본다** — 시험 컴파일 오류는 `go vet` 이나 `go test` 에서만 난다.

## 파일 구조

| 파일 | 책임 | 변경 |
|---|---|---|
| `cmd/fd/env.go` | 좌표 판정(순수 함수) | `OutboxPath`·`LegacyOutboxDirs` 추가. `StateDir`·`MachineIDPath` 주석 정정 |
| `cmd/fd/outbox.go` | 대기열·격리·재생 | `Outbox.path` → `dir`. `Dir`·`Source`·`LegacyLeftovers` 추가. **재생 로직은 안 바꾼다** |
| `cmd/fd/client.go` | 조립·재생 구동 | `newOutbox(get,home)` · `Legacy []*Outbox` · `Flush` 가 큐 전부를 돌고 침묵을 막는다 |
| `cmd/fd/config.go` | 설정 자리 | 반증된 주석 정정만 |
| `cmd/fd/cmds.go` | `runDoctor` | 자리·잔량·사각 출력 |
| `cmd/fd/harness_test.go` | 시험 하네스 | `HOME` 을 기본 env 에 고정(과제 1) |
| `cmd/fd/hook_stop_test.go` | 훅 시험 | 손으로 만든 env 에 `HOME` 추가(과제 1) |
| `cmd/fd/degrade_test.go` | 열화 시험 | `newOutbox` 호출부 2곳(과제 4) |
| `cmd/fd/degrade_path_test.go` | 열화 경로 시험 | `newOutbox` 호출부 4곳 + `sd.sub("outbox")` 좌표계 + 아웃박스 단정 |
| `cmd/fd/outbox_legacy_test.go` | **새 파일** — 옛 자리 재생 시험 | 신규 |
| `cmd/fd/env_test.go` | 좌표 시험 | 채널 무관 시험 추가 |
| `cmd/fd/outbox_stuck_test.go` | 재생 시험 | `mkOutbox` 를 `dir` 기반으로 |
| `DESIGN.md` | 설계 | §7 의 398행 |

---

### Task 1: 하네스가 진짜 홈에 쓰는 구멍을 먼저 막는다

**왜 첫 과제인가.** `LegacyOutboxDirs` 는 `~/.local/state/flightdeck/outbox` 를 후보로 내고 재생이 **거기 있는 것을 서버로 보낸다.** 하네스 기본 env(`harness_test.go:71-77`)에는 `HOME` 이 없고 `homeDir` 은 주입된 HOME 이 없으면 `os.UserHomeDir()`(프로세스 환경)로 떨어진다. 안 막으면 **시험이 개발자의 진짜 판단을 시험 서버로 보내고 큐를 비운다.**

추측이 아니다. 실측(2026-08-05): `HOME=<임시>` 로 `TestOfflineStateLandsUnderPluginDataNotPluginRoot` 를 돌리면 `<임시>/.flightdeck/machine-id` 가 **생성된다.** 그 시험이 `unpinnedEnv` 대신 손으로 env 를 만들어 `TestUnpinnedEnvNeverReachesTheRealHome` 의 감시를 우회하기 때문이다 — `runEnv` 주석(`harness_test.go:221-222`)이 정확히 그 위험을 경고해 뒀다.

**Files:**
- Modify: `plugins/flightdeck/server/cmd/fd/harness_test.go:71-78`
- Modify: `plugins/flightdeck/server/cmd/fd/degrade_path_test.go:350-358`
- Modify: `plugins/flightdeck/server/cmd/fd/hook_stop_test.go:45-56`
- Test: `plugins/flightdeck/server/cmd/fd/harness_env_test.go` (추가)

**Interfaces:**
- Consumes: 없음(첫 과제)
- Produces: `harness.env["HOME"] == harness.home` 불변. 뒤 과제 전부가 이것에 기댄다.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`harness_env_test.go` 끝에 붙인다:

```go
// 하네스 기본 환경이 HOME 을 고정하는지.
//
// ★ 왜 이 축이 생겼나. 옛 채널 자리 재생(Client.Flush)이 ~/.local/state/flightdeck/outbox 를
// 후보로 삼아 **거기 있는 판단을 서버로 보내고 큐를 비운다.** HOME 이 안 고정되면
// 시험이 개발자의 진짜 판단을 시험 서버로 보낸다 — 사각이 아니라 사고다.
//
// TestUnpinnedEnvNeverReachesTheRealHome 은 unpinnedEnv 갈래만 지킨다. 그런데
// degrade_path_test.go 와 hook_stop_test.go 가 손으로 env 를 만들어 그 감시를 우회한
// 전례가 있다(실측 2026-08-05: HOME=<임시> 로 돌리면 <임시>/.flightdeck/machine-id 가 생겼다).
// 그래서 **기본 env 자체**를 단정한다.
func TestHarnessPinsHomeSoLegacyReplayNeverReachesTheRealHome(t *testing.T) {
	h := newHarness(t)
	if got := h.env["HOME"]; got != h.home {
		t.Fatalf("하네스 기본 환경의 HOME 이 %q 다 — 가짜 홈 %q 여야 한다.\n"+
			"안 고정하면 옛 자리 재생이 개발자의 진짜 ~/.local/state 를 비운다", got, h.home)
	}
}
```

- [ ] **Step 2: 실패를 확인한다**

Run: `cd plugins/flightdeck/server && go test ./cmd/fd/ -run TestHarnessPinsHome -count=1`
Expected: FAIL — `하네스 기본 환경의 HOME 이 "" 다`

- [ ] **Step 3: 하네스에 HOME 을 고정한다**

`harness_test.go` 의 `hs.env = map[string]string{...}` 블록(71-77행)을 이것으로 바꾸고, 바로 뒤에 디렉토리 생성을 넣는다:

```go
	hs.env = map[string]string{
		"FD_URL":                 srv.URL,
		"FD_STATE_DIR":           hs.state,
		"FD_PROJECT":             hs.project,
		"FD_LOG":                 "error",
		"CLAUDE_CODE_SESSION_ID": "cc-session-uuid-1",
		// ★ HOME 을 **기본 env 에서** 고정한다. FD_STATE_DIR 만으로는 부족하다 —
		// 옛 채널 자리 재생이 ~/.local/state/flightdeck/outbox 를 후보로 삼아 거기 있는
		// 판단을 **보내고 큐를 비우므로**, HOME 이 안 잡히면 homeDir 이 os.UserHomeDir()
		// (프로세스 환경, 시험이 못 바꾼다)로 떨어져 개발자의 진짜 판단을 보낸다.
		// unpinnedEnv 는 이 값을 그대로 물려받고 FD_STATE_DIR 만 뺀다.
		"HOME": hs.home,
	}
	if err := os.MkdirAll(hs.home, 0o755); err != nil {
		t.Fatalf("가짜 홈을 못 만들었다(%s): %v", hs.home, err)
	}
	return hs
```

`harness_test.go` 의 import 에 `"os"` 가 없으면 넣는다.

- [ ] **Step 4: 손으로 만든 env 둘을 고친다**

**① `degrade_path_test.go:350-358`** — `env := map[string]string{}` / 복사 루프 / `delete(env,"FD_STATE_DIR")` / 두 대입을 이것으로 대체한다:

```go
	// ★ 손으로 env 를 만들지 않는다 — runEnv 주석이 경고하는 그대로, 손으로 만들면
	// HOME 을 잊고 시험이 진짜 홈을 건드린다(실측 2026-08-05).
	// unpinnedEnv 가 FD_STATE_DIR 를 빼고 가짜 홈을 함께 주는 정식 갈래다.
	env := h.unpinnedEnv(map[string]string{
		"CLAUDE_PLUGIN_DATA": data,
		"CLAUDE_PLUGIN_ROOT": root,
	})
```

359-362행의 전제 검사와 그 뒤 본문은 이 과제에서 손대지 않는다.

**② `hook_stop_test.go:45-56`** 의 `runHookForTest` — 하네스를 안 쓰고 실물 `run()` 을 타므로 하네스 가드가 원리적으로 못 잡는다. `HOME` 을 준다:

```go
// runHookForTest 는 `fd hook <event>` 한 번을 실물 진입점(run)으로 돌리고 stdout 을 낸다.
// FD_STATE_DIR 를 매번 새 임시 디렉토리로 줘 시험 간 캐시·아웃박스가 안 섞인다.
//
// ★ HOME 도 임시로 준다. 안 주면 homeDir 이 os.UserHomeDir()(프로세스 환경)로 떨어지고,
// 그 값으로 만들어지는 옛 채널 자리 후보에 개발자의 진짜 ~/.local/state/flightdeck/outbox 가
// 들어간다 — 훅이 재생을 돌리므로 **그 판단이 시험 서버로 나간다.**
// 이 함수는 하네스를 안 쓰므로 하네스의 HOME 고정이 여기까지 안 온다.
func runHookForTest(t *testing.T, url, event, stdin string) string {
	t.Helper()
	env := envOf(map[string]string{
		"FD_URL":       url,
		"FD_STATE_DIR": t.TempDir(),
		"FD_PROJECT":   "testproj",
		"FD_LOG":       "error",
		"HOME":         t.TempDir(),
	})
	var out, errb bytes.Buffer
	run([]string{"hook", event}, env, strings.NewReader(stdin), &out, &errb)
	return out.String()
}
```

- [ ] **Step 5: 전 시험 초록을 확인한다**

Run: `cd plugins/flightdeck/server && go test ./cmd/fd/ -count=1`
Expected: PASS 전부. `HOME` 이 붙어서 깨지는 시험이 있으면 그것은 지금까지 진짜 홈을 보고 있었다는 뜻이므로, 그 시험을 고쳐라(하네스 고정이 옳다).

- [ ] **Step 6: 진짜 홈을 안 건드리는지 실측한다**

Run:
```bash
cd plugins/flightdeck/server
SB=$(mktemp -d) && env HOME="$SB" GOMODCACHE="$(go env GOMODCACHE)" GOCACHE="$(go env GOCACHE)" \
  go test ./cmd/fd/ -count=1 >/dev/null 2>&1
echo "flightdeck 흔적:"; find "$SB" -path '*flightdeck*'
chmod -R u+w "$SB" 2>/dev/null; rm -rf "$SB"
```
Expected: `flightdeck 흔적:` 뒤에 **아무것도 안 나온다.**

★ 판정을 `-path '*flightdeck*'` 로 좁히는 이유: go 툴체인이 `$HOME/.config/go/telemetry/…` 를
항상 쓰므로 "아무 파일도 안 생긴다"는 원리적으로 성립하지 않는다. 그대로 두면 없는 누수를
쫓거나, 반대로 이 검증을 못 믿게 돼 진짜 누수를 놓친다.

- [ ] **Step 7: 커밋**

```bash
git add plugins/flightdeck/server/cmd/fd/harness_test.go \
        plugins/flightdeck/server/cmd/fd/harness_env_test.go \
        plugins/flightdeck/server/cmd/fd/degrade_path_test.go \
        plugins/flightdeck/server/cmd/fd/hook_stop_test.go
git commit -m "test(flightdeck): 하네스가 HOME 을 고정하게 해 시험이 진짜 홈에 못 닿게 한다

옛 채널 자리 재생이 ~/.local/state/flightdeck/outbox 의 판단을 **보내고 큐를 비우므로**,
HOME 이 안 고정된 시험은 개발자의 진짜 판단을 시험 서버로 보낸다.

degrade_path_test 와 hook_stop_test 가 손으로 env 를 만들어
TestUnpinnedEnvNeverReachesTheRealHome 의 감시를 우회하고 있었다 — 실측하면
HOME=<임시> 에서 <임시>/.flightdeck/machine-id 가 생긴다."
```

---

### Task 2: `OutboxPath` — 채널 무관한 고정 자리

**Files:**
- Modify: `plugins/flightdeck/server/cmd/fd/env.go` (93행 뒤, `MachineID` 앞)
- Test: `plugins/flightdeck/server/cmd/fd/env_test.go`

**Interfaces:**
- Consumes: 없음
- Produces: `func OutboxPath(get func(string) (string, bool), home string) (dir, source string)`

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
// 부른 것을 담는다.
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
// (MachineIDPath·ConfigPath 가 같은 예외를 같은 이유로 둔다).
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

`env.go` 의 `MachineIDPath` 바로 뒤(93행 다음)에 넣는다:

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
```

- [ ] **Step 4: 통과를 확인한다**

Run: `cd plugins/flightdeck/server && go test ./cmd/fd/ -run TestOutboxPath -count=1 -v`
Expected: PASS 3건

- [ ] **Step 5: 커밋**

```bash
git add plugins/flightdeck/server/cmd/fd/env.go plugins/flightdeck/server/cmd/fd/env_test.go
git commit -m "feat(flightdeck): OutboxPath — 아웃박스 자리를 채널 환경에서 떼어낸다

MachineIDPath·ConfigPath 와 같은 규칙의 셋째 적용이다. 가르는 축은 '열화 상태인가'가
아니라 '재생성 가능한가'였다 — 캐시는 갈려도 되고 아웃박스는 안 된다."
```

---

### Task 3: `LegacyOutboxDirs` — 재생이 함께 돌 옛 자리

**Files:**
- Modify: `plugins/flightdeck/server/cmd/fd/env.go` (`OutboxPath` 바로 뒤)
- Test: `plugins/flightdeck/server/cmd/fd/env_test.go`

**Interfaces:**
- Consumes: `OutboxPath`(과제 2)
- Produces: `func LegacyOutboxDirs(get func(string) (string, bool), home, target string) []string`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`env_test.go` 에 붙인다:

```go
// 옛 자리 목록은 **이 채널이 계산할 수 있는 것만** 담는다.
//
// ★ ~/.claude/plugins/data/*/flightdeck 를 glob 하지 않는다. 그 경로에는 플러그인
// 버전과 마켓 이름이 들어가고, 설계 §13 이 "그 경로를 어디에도 저장하지 않는다"고
// 판정했다. 대신 각 채널이 제 자리를 **전송으로** 비우고, 그래서 어느 채널이 한 번도
// 안 돌면 그 자리는 안 비워진다 — 그 구멍은 fd doctor 가 말로 찍는다.
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
		filepath.Join(os.TempDir(), "flightdeck", "outbox"),
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

// ★ FD_STATE_DIR 를 **새로 켠** 사용자를 위한 자리.
//
// 그러면 목표가 FD_STATE_DIR/outbox 로 바뀌는데, ~/.flightdeck/outbox 가 후보에 없으면
// 그때까지 고정 자리에 쌓인 판단 전량이 조용히 안 보이게 된다(doctor 가 '대기 0건'을 찍는다).
// home 은 이미 인자로 들어오므로 계산 불가한 것이 아니라 그냥 빠뜨리기 쉬운 자리다.
func TestLegacyOutboxDirsIncludesFixedPlaceWhenStateDirIsExplicit(t *testing.T) {
	home := "/h"
	got := LegacyOutboxDirs(envOf(map[string]string{"FD_STATE_DIR": "/explicit"}),
		home, filepath.Join("/explicit", "outbox"))
	fixed := filepath.Join(home, ".flightdeck", "outbox")
	for _, d := range got {
		if d == fixed {
			return
		}
	}
	t.Fatalf("FD_STATE_DIR 를 켰는데 고정 자리(%s)가 후보에 없다 — "+
		"그때까지 쌓인 판단이 조용히 안 보이게 된다: %v", fixed, got)
}

// 목표와 같은 자리는 돌 것이 없다. 넣으면 같은 큐를 두 번 재생한다.
func TestLegacyOutboxDirsExcludesTheTarget(t *testing.T) {
	target := filepath.Join("/h", ".flightdeck", "outbox")
	got := LegacyOutboxDirs(envOf(map[string]string{}), "/h", target)
	for _, d := range got {
		if filepath.Clean(d) == filepath.Clean(target) {
			t.Fatalf("목표 자리(%s)가 옛 자리 목록에 들어 있다 — 같은 큐를 두 번 돈다", target)
		}
	}
}

// 같은 자리를 두 축이 가리켜도 한 번만 돈다.
func TestLegacyOutboxDirsDeduplicates(t *testing.T) {
	got := LegacyOutboxDirs(envOf(map[string]string{
		"CLAUDE_PLUGIN_DATA": "/same",
		"XDG_STATE_HOME":     "/same",
	}), "", "/target/outbox")
	n := 0
	for _, d := range got {
		if d == filepath.Join("/same", "flightdeck", "outbox") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("같은 자리를 %d번 돈다: %v", n, got)
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
// 임시 디렉토리도 넣는다. **앞선 판에서 이것을 뺐던 근거가 틀렸다** — "그 갈래가 걸리는
// 조건에서는 목표도 같은 자리라 어차피 걸러진다"고 적었는데, 이 목록이 판정하는 것은
// **과거 실행의** 환경이고 목표를 정하는 것은 **지금** 환경이다. HOME 없이(데몬·컨테이너
// 진입점) 돌아 tmp 에 쌓은 머신이 나중에 HOME 을 갖게 되면 그 판단이 영영 안 나간다.
// 공용 머신에서 남의 것을 건드리는 위험은 파일 권한이 막는다(아웃박스 파일은 0600 이라
// 읽기가 실패하고, 그 실패는 사유로 올라온다).
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
	add(filepath.Join(os.TempDir(), "flightdeck", "outbox"))
	return out
}
```

- [ ] **Step 4: 통과를 확인한다**

Run: `cd plugins/flightdeck/server && go test ./cmd/fd/ -run TestLegacyOutboxDirs -count=1 -v`
Expected: PASS 4건

- [ ] **Step 5: 커밋**

```bash
git add plugins/flightdeck/server/cmd/fd/env.go plugins/flightdeck/server/cmd/fd/env_test.go
git commit -m "feat(flightdeck): LegacyOutboxDirs — 재생이 함께 돌 옛 자리를 센다

파일을 옮기지 않는다. 재생이 제자리에서 돌려 전송으로 비운다.
고정 자리 자신도 후보에 넣는다 — FD_STATE_DIR 를 새로 켜면 목표가 옮겨 가고,
그때 이 줄이 없으면 쌓인 판단이 조용히 안 보이게 된다.
plugins/data/* 를 glob 하지 않는다(§13)."
```

---

### Task 4: `Outbox` 를 디렉토리 기반으로 바꾸고 호출부를 **전부** 고친다

동작을 안 바꾼다. 자리만 옮기고 기존 시험이 초록으로 남는지 본다.

**⚠ `newOutbox` 호출부가 시험에 6곳 있다.** 하나라도 빠뜨리면 **시험 바이너리가 컴파일되지 않아** 이 과제의 검증이 원리적으로 불가능해진다. `go build ./...` 는 `_test.go` 를 안 보므로 통과한다 — 반드시 `go vet ./...` 나 `go test` 로 확인해라.

**Files:**
- Modify: `cmd/fd/outbox.go` (169-190, 198-287, 369-372, 394-415행)
- Modify: `cmd/fd/client.go:115, 130`
- Modify: `cmd/fd/outbox_stuck_test.go:31-38`
- Modify: `cmd/fd/degrade_test.go:38, 132`
- Modify: `cmd/fd/degrade_path_test.go:84, 232-233, 425, 458`

**Interfaces:**
- Consumes: `OutboxPath`·`LegacyOutboxDirs`(과제 2·3)
- Produces:
  - `func newOutbox(get func(string) (string, bool), home string) *Outbox`
  - `func newOutboxAt(dir string) *Outbox` — 자리를 직접 주는 생성자. 옛 자리 큐와 시험이 쓴다.
  - `func (o *Outbox) Dir() string` · `func (o *Outbox) Source() string`
  - 필드 `dir string` · `source string` · `now func() time.Time`
  - 상수 `pendingName = "pending.jsonl"` · `rejectedName = "rejected.jsonl"`
  - `func readEntries(path string) ([]OutboxEntry, error)` · `func readRejected(path string) ([]RejectedEntry, error)`

- [ ] **Step 1: `Outbox` 구조를 바꾼다**

`outbox.go:169-190` 을 교체한다:

```go
// 대기열·격리 파일의 이름. 한 자리에 모은다 — 옛 자리 재생이 이 이름으로 큐를 찾으므로
// 두 자리에 흩어 두면 한쪽만 고칠 때 그 큐가 조용히 안 보이게 된다.
const (
	pendingName  = "pending.jsonl"
	rejectedName = "rejected.jsonl"
)

// Outbox 는 디렉토리 하나의 대기열이다. 파일 하나에 JSONL 로 쌓는다.
//
// ★ 예전에는 상태 디렉토리 아래였고, 그 자리가 채널마다 갈려서 셸에서 쌓인 판단을
// 훅·MCP 가 영영 못 보내는 결함이 있었다(OutboxPath 주석에 판정 전문이 있다).
// 지금은 새 쓰기가 고정 자리로 가고, 옛 자리는 **같은 타입의 값을 하나씩 만들어**
// 재생이 함께 돈다(Client.Legacy). 큐 하나가 이 값 하나다.
type Outbox struct {
	dir    string // 대기열·격리 파일이 있는 디렉토리
	source string // 왜 이 자리인가. fd doctor 가 찍는다 — machineSrc 가 선례다
	// now 는 격리 시각을 찍는 시계다. 시험이 갈아 끼울 자리이기도 하다.
	now func() time.Time
}

func newOutbox(get func(string) (string, bool), home string) *Outbox {
	dir, src := OutboxPath(get, home)
	o := newOutboxAt(dir)
	o.source = src
	return o
}

// newOutboxAt 은 자리를 직접 주는 생성자다. 옛 자리 큐(Client.Legacy)와 시험이 쓴다.
func newOutboxAt(dir string) *Outbox {
	return &Outbox{
		dir:    dir,
		source: "직접 지정",
		now:    func() time.Time { return time.Now().UTC() },
	}
}

// Dir·Source 는 fd doctor 가 "어디를, 왜"를 찍기 위한 자리다.
func (o *Outbox) Dir() string    { return o.dir }
func (o *Outbox) Source() string { return o.source }

// pendingPath·rejectedPath 는 두 파일의 자리다. 같은 디렉토리에 둔다 —
// 같은 축의 같은 자산이고, 격리는 제 큐 옆에 남아야 '어디서 온 것인가'가 안 사라진다.
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

- [ ] **Step 2: `o.path` 사용처를 바꾸고 읽기를 함수로 뺀다**

`outbox.go` 안 `o.path` → `o.pendingPath()` (`Append` 208·215행, `keep` 265·279·283행).

`List`(230-260행)와 `Rejected`(394-415행)를 이렇게 바꾼다 — 옛 자리 잔량을 **읽기만** 세는
`LegacyLeftovers`(과제 6)가 같은 규칙으로 읽어야 "여기서는 버려지고 저기서는 안 버려지는"
자리가 안 생긴다:

```go
// List 는 대기 중인 전부를 순서대로 낸다. 파일이 없으면 빈 목록이다(오류가 아니다).
func (o *Outbox) List() ([]OutboxEntry, error) { return readEntries(o.pendingPath()) }

// readEntries 는 JSONL 대기열 파일 하나를 읽는다.
//
// ★ 깨진 줄을 **조용히 버리지 않는다.** 이 파일은 재생성 불가한 자산이므로
// 해석 실패는 **읽은 데까지와 함께** 오류로 올려 사람이 보게 한다(설계 §9).
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

// readRejected 는 격리 파일 하나를 읽는다. doctor 의 잔량 합산도 이것을 쓴다.
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

**`Replay` 는 한 글자도 안 바꾼다.** 이 계획의 핵심이 그것이다 — 이미 있고 이미 시험된 절차를 그대로 쓴다.

- [ ] **Step 3: 배선을 바꾼다**

`client.go:130`:

```go
		Outbox:   newOutbox(get, home),
```

`newClient` 의 `sd StateDir` 인자는 **그대로 둔다** — `newCache(sd)` 가 계속 쓴다.

- [ ] **Step 4: `newOutbox` 호출부 6곳을 전부 고친다**

**① `outbox_stuck_test.go:31-38`**:

```go
func mkOutbox(t *testing.T) *Outbox {
	t.Helper()
	at := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
	o := newOutboxAt(filepath.Join(t.TempDir(), "outbox"))
	o.now = func() time.Time { return at }
	return o
}
```

**② `degrade_test.go:38` 과 `degrade_test.go:132`** — 둘 다 같은 모양이다:

```go
	ob := newOutbox(envOf(h.env), h.home)
```

**③ `degrade_path_test.go:84`** — 바로 위 `sd := ResolveStateDir(...)` 는 남는다(다른 데서 쓸 수 있다. 안 쓰면 컴파일러가 알려 준다):

```go
	ob := newOutbox(envOf(h.env), h.home)
```

**④ `degrade_path_test.go:232-233`** — 여기가 중요하다. `outboxPath` 가
`sd.sub("outbox")` 로 **이 항목이 없애려는 결합을 좌표계로 삼고 있다.** 지금 초록인 이유는
하네스가 `FD_STATE_DIR` 를 고정해 두 경로가 우연히 같기 때문이지 시험이 새 자리를 보기
때문이 아니다:

```go
	// ★ 아웃박스 자리를 sd.sub("outbox") 로 구하지 않는다 — 그 결합이 이 항목이 없앤 것이다.
	//   하네스가 FD_STATE_DIR 를 고정해 우연히 같은 경로가 나오더라도, 시험이 단정하는
	//   좌표계는 **소비자가 실제로 쓰는 자리**여야 한다.
	ob := newOutbox(envOf(h.env), h.home)
	outboxPath := filepath.Join(ob.Dir(), pendingName)
```

바로 위 `sd := ResolveStateDir(envOf(h.env), "")` 줄은 안 쓰이면 지운다.

**⑤ `degrade_path_test.go:425`**:

```go
	pend, err := newOutbox(envOf(h.env), h.home).List()
```

**⑥ `degrade_path_test.go:458`**:

```go
	if left, err := newOutbox(envOf(h.env), h.home).List(); err != nil || len(left) != 2 {
```

- [ ] **Step 5: 컴파일과 시험을 확인한다**

Run:
```bash
cd plugins/flightdeck/server
go vet ./...            # ← _test.go 를 본다. 호출부를 빠뜨렸으면 여기서 잡힌다
go test ./cmd/fd/ -count=1 2>&1 | grep -E '^(---|ok|FAIL|# )' | head -20
```
Expected: `go vet` 무출력. 시험은 `TestOfflineStateLandsUnderPluginDataNotPluginRoot` **하나만** 빨강(과제 7에서 고친다). 다른 것이 빨갛거나 `# github.com/...` 빌드 오류가 나오면 호출부를 빠뜨린 것이다.

- [ ] **Step 6: 커밋**

```bash
git add plugins/flightdeck/server/cmd/fd/outbox.go plugins/flightdeck/server/cmd/fd/client.go \
        plugins/flightdeck/server/cmd/fd/outbox_stuck_test.go \
        plugins/flightdeck/server/cmd/fd/degrade_test.go \
        plugins/flightdeck/server/cmd/fd/degrade_path_test.go
git commit -m "refactor(flightdeck): Outbox 를 고정 자리 디렉토리 기반으로 바꾼다

동작은 안 바꾼다. Replay 는 한 글자도 안 건드렸다.

degrade_path_test 가 아웃박스 자리를 sd.sub(\"outbox\") 로 구하고 있었다 —
이 항목이 없애려는 결합을 시험이 좌표계로 삼은 것이다. 지금 초록인 이유는 하네스가
FD_STATE_DIR 를 고정해 우연히 같은 경로가 나오기 때문이었다."
```

---

### Task 5: 재생이 옛 자리 큐를 함께 돈다

**Files:**
- Modify: `plugins/flightdeck/server/cmd/fd/client.go:84-134, 290-310`
- Create: `plugins/flightdeck/server/cmd/fd/outbox_legacy_test.go`

**Interfaces:**
- Consumes: `newOutbox`·`newOutboxAt`·`LegacyOutboxDirs`(과제 3·4)
- Produces: `Client.Legacy []*Outbox` · `Client.Flush` 가 큐 전부를 돌고 각 큐의 사유를 낸다.

- [ ] **Step 1: 실패하는 시험을 쓴다**

새 파일 `outbox_legacy_test.go`:

```go
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 이 파일이 지키는 것은 하나다: **어느 채널에서 쌓인 판단도 결국 나간다.**
//
// ★ 옮기지 않는다. 재생이 각 큐를 제자리에서 돌려 전송으로 비우고, 마지막 줄까지
// 나가면 keep() 이 그 파일을 지운다. 앞선 판에서는 os.Rename 청구로 고정 자리에
// 흡수하려 했는데 그 설계가 반증됐다 — 스펙 §4 "왜 옮기지 않기로 했나"를 보라.

// seedQueue 는 옛 자리 하나와 그 안의 대기열 파일을 만든다.
func seedQueue(t *testing.T, dir string, keys ...string) {
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

// queuedKeys 는 큐에 남은 키를 순서대로 낸다.
//
// ★ 이름에 주의. keysOf 는 plugin_test.go 에 제네릭으로 이미 있어서 못 쓴다
// (같은 패키지라 재선언 오류가 난다).
func queuedKeys(t *testing.T, o *Outbox) []string {
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

// ── 옛 자리 큐가 전송으로 비고 파일이 사라진다 ───────────────────────────────

func TestFlushDrainsLegacyQueuesBySending(t *testing.T) {
	h := newHarness(t)

	legacyA := filepath.Join(t.TempDir(), "chanA", "outbox")
	legacyB := filepath.Join(t.TempDir(), "chanB", "outbox")
	seedQueue(t, legacyA, "a1", "a2")
	seedQueue(t, legacyB, "b1")

	cli := newClient(ResolveStateDir(envOf(h.env), h.home), envOf(h.env), h.home, quietLogger())
	cli.Legacy = []*Outbox{newOutboxAt(legacyA), newOutboxAt(legacyB)}

	var sent []string
	res := cli.flushAll(t.Context(), func(_ *Outbox, e OutboxEntry) error {
		sent = append(sent, e.Key)
		return nil
	})
	if len(sent) != 3 {
		t.Fatalf("보낸 것이 %d건이다 — 옛 자리 큐가 안 돌았다: %v", len(sent), sent)
	}
	if res.Sent != 3 {
		t.Errorf("Sent 가 %d 다 — 3 이어야 한다", res.Sent)
	}

	// ★ 큐 파일이 **사라진다** — keep() 의 기존 동작이다.
	for _, d := range []string{legacyA, legacyB} {
		if _, err := os.Stat(filepath.Join(d, pendingName)); !os.IsNotExist(err) {
			t.Errorf("%s 의 큐가 안 비었다(err=%v)", d, err)
		}
	}
	// ★ 고정 자리에는 **아무것도 안 생긴다** — 옮기는 게 아니라 보내는 것이다.
	if got := queuedKeys(t, cli.Outbox); len(got) != 0 {
		t.Errorf("고정 자리에 %v 가 생겼다 — 옮기지 않기로 했다", got)
	}
}

// 옛 큐가 막혀도 고정 큐는 나간다. 한 큐의 정체가 다른 큐를 인질로 잡지 않는다.
func TestStuckLegacyQueueDoesNotBlockTheFixedQueue(t *testing.T) {
	h := newHarness(t)
	legacy := filepath.Join(t.TempDir(), "chan", "outbox")
	seedQueue(t, legacy, "stuck1", "stuck2")

	cli := newClient(ResolveStateDir(envOf(h.env), h.home), envOf(h.env), h.home, quietLogger())
	cli.Legacy = []*Outbox{newOutboxAt(legacy)}
	if err := cli.Outbox.Append(entry("fixed1")); err != nil {
		t.Fatalf("고정 큐에 못 쌓았다: %v", err)
	}

	var sent []string
	cli.flushAll(t.Context(), func(o *Outbox, e OutboxEntry) error {
		if o.Dir() == legacy {
			return ErrUnreachable // 옛 큐만 막는다
		}
		sent = append(sent, e.Key)
		return nil
	})
	if len(sent) != 1 || sent[0] != "fixed1" {
		t.Errorf("고정 큐가 %v 를 보냈다 — 옛 큐가 막혔다고 고정 큐가 막히면 안 된다", sent)
	}
	if got := queuedKeys(t, newOutboxAt(legacy)); len(got) != 2 {
		t.Errorf("막힌 옛 큐가 %v 다 — 2건 그대로 남아야 한다", got)
	}
}

// 옛 큐의 영구 거절은 **그 자리의** 격리 파일로 간다. 보관소가 제 큐 옆에 남는다.
func TestLegacyQueueQuarantinesIntoItsOwnDir(t *testing.T) {
	h := newHarness(t)
	legacy := filepath.Join(t.TempDir(), "chan", "outbox")
	seedQueue(t, legacy, "bad1")

	cli := newClient(ResolveStateDir(envOf(h.env), h.home), envOf(h.env), h.home, quietLogger())
	cli.Legacy = []*Outbox{newOutboxAt(legacy)}

	cli.flushAll(t.Context(), func(*Outbox, OutboxEntry) error {
		return &APIError{Status: 409, Message: "이미 있다"}
	})

	rej, err := newOutboxAt(legacy).Rejected()
	if err != nil {
		t.Fatalf("옛 자리 격리를 못 읽었다: %v", err)
	}
	if len(rej) != 1 {
		t.Fatalf("옛 자리에 격리가 %d건이다 — 1건이어야 한다", len(rej))
	}
	if fixed, _ := cli.Outbox.Rejected(); len(fixed) != 0 {
		t.Errorf("고정 자리에 격리가 %d건 생겼다 — 보관소는 제 큐 옆에 남아야 한다", len(fixed))
	}
}
```

- [ ] **Step 2: 실패를 확인한다**

Run: `cd plugins/flightdeck/server && go test ./cmd/fd/ -run 'TestFlushDrains|TestStuckLegacy|TestLegacyQueueQuarantines' -count=1`
Expected: FAIL — `cli.Legacy undefined` · `cli.flushAll undefined`

- [ ] **Step 3: `Client` 에 옛 큐를 달고 `Flush` 를 큐 전부로 넓힌다**

`client.go` 의 `Client` 구조체에 필드를 넣는다(`Outbox` 필드 바로 아래):

```go
	// Legacy 는 아웃박스가 채널마다 갈려 있던 시절의 큐다. **옮기지 않는다** —
	// 재생이 제자리에서 돌려 전송으로 비우고, 마지막 줄까지 나가면 keep() 이 파일을 지운다.
	// (os.Rename 청구로 고정 자리에 흡수하려던 앞선 설계는 반증됐다 — 스펙 §4.)
	Legacy []*Outbox
```

`newClient` 의 `return &Client{...}` 를 바꾼다:

```go
	ob := newOutbox(get, home)
	var legacy []*Outbox
	for _, d := range LegacyOutboxDirs(get, home, ob.Dir()) {
		legacy = append(legacy, newOutboxAt(d))
	}
	return &Client{
		Endpoint: ep,
		URL:      url,
		Token:    token,
		HTTP:     &http.Client{Timeout: timeout},
		Cache:    newCache(sd),
		Outbox:   ob,
		Legacy:   legacy,
		Log:      log,
		Now:      func() time.Time { return time.Now().UTC() },
	}
```

`Flush`(294-310행)를 이렇게 바꾼다:

```go
// Flush 는 쌓인 판단을 재생한다. **모든 명령의 앞에서 불린다** —
// 재연결을 감지하는 별도 기구를 만들지 않는다(감지 기구는 자기가 안 돌 때 조용하다).
//
// ★ 고정 큐를 돌고 **옛 채널 자리 큐도 함께 돈다.** 큐마다 독립이라 한쪽이 막혀도
// 다른 쪽은 나간다 — 한 큐의 정체가 다른 큐를 인질로 잡지 않는다.
func (c *Client) Flush(ctx context.Context) ReplayResult {
	return c.flushAll(ctx, func(_ *Outbox, e OutboxEntry) error {
		var body any
		if uerr := json.Unmarshal(e.Body, &body); uerr != nil {
			// 해석 불가한 줄은 보낼 수 없다. 버리지 않고 남긴 채 사유를 올린다.
			return fmt.Errorf("본문 해석 실패: %w", uerr)
		}
		_, _, err := c.do(ctx, http.MethodPost, e.Path, body, e.Key)
		return err
	})
}

// flushAll 은 큐 전부를 돌고 결과를 합산한다. 전송 함수를 인자로 받는 이유는
// 시험이 서버 없이 갈래를 볼 수 있어야 해서다(하네스를 띄우면 그 갈래가 안 보인다).
func (c *Client) flushAll(ctx context.Context, send func(*Outbox, OutboxEntry) error) ReplayResult {
	var total ReplayResult
	var details []string
	for _, ob := range append([]*Outbox{c.Outbox}, c.Legacy...) {
		res, err := ob.Replay(ctx, func(ctx context.Context, e OutboxEntry) error {
			return send(ob, e)
		})
		total.Sent += res.Sent
		total.Rejected += res.Rejected
		total.Remaining += res.Remaining
		switch {
		case err != nil:
			c.Log.Error("아웃박스 재생 실패", "dir", ob.Dir(), "error", err.Error(), "count", res.Sent)
			details = append(details, ob.Dir()+": "+err.Error())
		case res.Remaining > 0 || res.Rejected > 0:
			// ★ 이 가지가 없어서 **완전 침묵**이었다. 옛 코드는 err!=nil 이거나 Sent>0
			// 일 때만 로그를 냈는데, 남거나 격리만 된 경우가 정확히 err==nil·Sent==0 이다.
			// 큐가 여럿이 된 지금 그 침묵은 "어느 큐가 왜 안 나갔나"에 답할 자리를 없앤다(§9).
			c.Log.Warn("아웃박스가 안 비었다", "dir", ob.Dir(),
				"sent", res.Sent, "remaining", res.Remaining, "rejected", res.Rejected)
			details = append(details, ob.Dir()+": "+res.Detail)
		case res.Sent > 0:
			c.Log.Info("아웃박스 재생", "dir", ob.Dir(), "count", res.Sent)
		}
	}
	switch {
	case len(details) > 0:
		total.Detail = strings.Join(details, " · ")
	case total.Sent > 0:
		total.Detail = fmt.Sprintf("판단 %d건을 재생했다", total.Sent)
	default:
		total.Detail = "대기 중인 판단이 없다"
	}
	return total
}
```

`client.go` 의 import 에 `"strings"` 가 없으면 넣는다(이미 있다 — 확인만).

- [ ] **Step 4: 통과를 확인한다**

Run: `cd plugins/flightdeck/server && go test ./cmd/fd/ -run 'TestFlushDrains|TestStuckLegacy|TestLegacyQueueQuarantines' -race -count=1 -v`
Expected: PASS 3건

그리고 회귀를 본다:
Run: `go test ./cmd/fd/ -count=1 2>&1 | grep -E '^(---|ok|FAIL)' | head`
Expected: `TestOfflineStateLandsUnderPluginDataNotPluginRoot` 만 빨강.

- [ ] **Step 5: 커밋**

```bash
git add plugins/flightdeck/server/cmd/fd/client.go \
        plugins/flightdeck/server/cmd/fd/outbox_legacy_test.go
git commit -m "feat(flightdeck): 재생이 옛 채널 자리 큐를 함께 돈다 — 옮기지 않고 보낸다

큐 하나가 Outbox 값 하나다. 기존 Replay 를 그대로 재사용하므로 새 기구가 없다.
큐마다 독립이라 한쪽이 막혀도 다른 쪽은 나간다.

함께 막은 것: Flush 가 err!=nil 이거나 Sent>0 일 때만 로그를 내서, 남거나 격리만 된
경우(err==nil·Sent==0)가 **완전 침묵**이었다. 큐가 여럿이 된 지금 그 침묵은
'어느 큐가 왜 안 나갔나'에 답할 자리를 없앤다(§9)."
```

---

### Task 6: `fd doctor` 가 자리·잔량·**못 보는 범위**를 찍는다

**Files:**
- Modify: `plugins/flightdeck/server/cmd/fd/outbox.go` (`LegacyLeftovers` 추가)
- Modify: `plugins/flightdeck/server/cmd/fd/cmds.go:444-464`
- Test: `plugins/flightdeck/server/cmd/fd/outbox_legacy_test.go` (추가)

**Interfaces:**
- Consumes: `readEntries`·`readRejected`·`Client.Legacy`(과제 4·5)
- Produces: `type Leftover struct { Dir string; Pending, Rejected int; Err string }` · `func (c *Client) LegacyLeftovers() []Leftover` — **읽기만 한다.**

- [ ] **Step 1: 실패하는 시험을 쓴다**

`outbox_legacy_test.go` 에 붙인다:

```go
// doctor 가 옛 자리 잔량을 세되 **아무것도 보내지 않는다.** 진단이 부작용을 가지면
// "찍어 봤더니 상태가 달라졌다"가 되고, 그러면 진단을 믿을 수 없다.
func TestLegacyLeftoversCountsWithoutSending(t *testing.T) {
	h := newHarness(t)
	legacy := filepath.Join(t.TempDir(), "chan", "outbox")
	seedQueue(t, legacy, "k1", "k2")

	cli := newClient(ResolveStateDir(envOf(h.env), h.home), envOf(h.env), h.home, quietLogger())
	cli.Legacy = []*Outbox{newOutboxAt(legacy)}

	got := cli.LegacyLeftovers()
	if len(got) != 1 {
		t.Fatalf("잔량 보고가 %d건이다 — 1건이어야 한다: %+v", len(got), got)
	}
	if got[0].Pending != 2 {
		t.Errorf("대기 %d건으로 셌다 — 2건이어야 한다", got[0].Pending)
	}
	if _, err := os.Stat(filepath.Join(legacy, pendingName)); err != nil {
		t.Errorf("큐가 사라졌다 — 진단이 보냈다: %v", err)
	}
}

// 빈 자리는 안 찍는다. 없는 것을 찍으면 사람이 헛것을 쫓는다.
func TestLegacyLeftoversIsEmptyWhenNothingLeft(t *testing.T) {
	h := newHarness(t)
	cli := newClient(ResolveStateDir(envOf(h.env), h.home), envOf(h.env), h.home, quietLogger())
	cli.Legacy = []*Outbox{newOutboxAt(filepath.Join(t.TempDir(), "nope", "outbox"))}
	if got := cli.LegacyLeftovers(); len(got) != 0 {
		t.Errorf("빈 자리를 %d건으로 보고했다: %+v", len(got), got)
	}
}

// doctor 는 자리와 **못 보는 범위**를 함께 찍는다.
//
// ★ 후자가 이 시험의 핵심이다. 옛 자리 목록은 이 채널이 계산할 수 있는 것만이라
// 다른 채널의 자리는 원리적으로 안 보인다. 그 사실을 안 찍으면 "0건"이 '깨끗하다'로
// 읽히고, 그것은 안 잰 축을 잰 척하는 것이다(§13).
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
	// ★ 단정 문자열은 **그 줄 고유의 문구**여야 한다. "채널" 로 단정하면 기존
	//   `처방 채널   Stop 훅 stdout` 줄(cmds.go:446)에 걸려서, 사각 문장을 통째로
	//   빼먹어도 초록이 된다 — 이 레포가 반복해서 경계한 '전 시험 초록 상태로 사는 결함'이다.
	if !strings.Contains(out, "옛 자리 탐색") {
		t.Errorf("doctor 가 못 보는 범위를 안 말한다 — 0건이 '깨끗하다'로 읽힌다:\n%s", out)
	}
}
```

- [ ] **Step 2: 실패를 확인한다**

Run: `cd plugins/flightdeck/server && go test ./cmd/fd/ -run 'TestLegacyLeftovers|TestDoctorReportsOutbox' -count=1`
Expected: FAIL — `cli.LegacyLeftovers undefined`

- [ ] **Step 3: `LegacyLeftovers` 를 구현한다**

`outbox.go` 의 `Rejected` 뒤에 넣는다:

```go
// Leftover 는 옛 자리 하나에 아직 남아 있는 것이다.
type Leftover struct {
	Dir      string
	Pending  int    // 대기열 줄 수
	Rejected int    // 격리 줄 수 — 이것은 안 비워진다(보관소는 제 큐 옆에 남는다)
	Err      string // 셀 수 없었으면 그 사유. 비어 있을 수 있다
}

// leftover 는 이 큐에 남은 것을 **읽기만 해서** 센다.
//
// ★ 보내지 않는다. 진단이 부작용을 가지면 "찍어 봤더니 상태가 달라졌다"가 되고,
// 그러면 진단을 믿을 수 없다. 재생은 Flush 경로에서만 돈다.
func (o *Outbox) leftover() Leftover {
	lo := Leftover{Dir: o.dir}
	if es, err := o.List(); err != nil {
		lo.Err = err.Error()
	} else {
		lo.Pending = len(es)
	}
	if rs, err := o.Rejected(); err != nil {
		lo.Err = strings.TrimSpace(lo.Err + " " + err.Error())
	} else {
		lo.Rejected = len(rs)
	}
	return lo
}
```

`client.go` 에 넣는다(`flushAll` 뒤):

```go
// LegacyLeftovers 는 옛 자리 큐에 아직 남아 있는 것이다. **읽기만 한다.**
//
// 빈 자리는 안 낸다 — 없는 것을 찍으면 사람이 헛것을 쫓는다.
func (c *Client) LegacyLeftovers() []Leftover {
	var out []Leftover
	for _, ob := range c.Legacy {
		lo := ob.leftover()
		if lo.Pending == 0 && lo.Rejected == 0 && lo.Err == "" {
			continue
		}
		out = append(out, lo)
	}
	return out
}
```

- [ ] **Step 4: `runDoctor` 의 아웃박스 절을 바꾼다**

`cmds.go` 의 `처방 채널` 줄(446행) **다음부터** 451-464행까지를 이것으로 교체한다:

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
	// ★ 옛 채널 자리에 남은 것. 대기는 다음 재생이 **보내서** 비우고, 격리는 그 자리에 남는다
	// (보관소는 제 큐 옆에 남는 것이 설계다 — '어디서 온 것인가'가 사라지면 안 된다).
	for _, lo := range a.cli.LegacyLeftovers() {
		fmt.Fprintf(out, "  ! 옛 자리 %s — 대기 %d건(다음 재생이 보낸다) · 격리 %d건(그 자리에 남는다)\n",
			lo.Dir, lo.Pending, lo.Rejected)
		if lo.Err != "" {
			fmt.Fprintf(out, "      ! 세다 걸렸다: %s\n", clip(lo.Err, 200))
		}
	}
	// ★ **이 목록이 못 보는 범위를 함께 찍는다.** 빼면 "0건"이 '깨끗하다'로 읽히는데,
	// 그것은 안 잰 축을 잰 척하는 것이다(§13). 이 문장이 그 축의 유일한 파수꾼이다.
	fmt.Fprintln(out, "  옛 자리 탐색은 이 채널이 계산할 수 있는 자리만이다 — "+
		"다른 채널(훅·MCP 는 CLAUDE_PLUGIN_DATA)의 자리는 여기서 안 보인다.")
```

- [ ] **Step 5: 통과를 확인한다**

Run: `cd plugins/flightdeck/server && go test ./cmd/fd/ -count=1 2>&1 | grep -E '^(---|ok|FAIL)' | head`
Expected: `TestOfflineStateLandsUnderPluginDataNotPluginRoot` 만 빨강.

**시험이 진짜로 무언가를 지키는지 확인한다** — 사각 문장 두 줄을 잠시 지우고 돌려서
`TestDoctorReportsOutboxPlaceAndItsOwnBlindness` 가 **빨개지는지** 본다. 안 빨개지면
단정이 공허한 것이다. 확인 후 되돌린다.

- [ ] **Step 6: 커밋**

```bash
git add plugins/flightdeck/server/cmd/fd/outbox.go plugins/flightdeck/server/cmd/fd/client.go \
        plugins/flightdeck/server/cmd/fd/cmds.go \
        plugins/flightdeck/server/cmd/fd/outbox_legacy_test.go
git commit -m "feat(flightdeck): doctor 가 아웃박스 자리·옛 자리 잔량·못 보는 범위를 찍는다

잔량 세기는 읽기만 한다 — 진단이 부작용을 가지면 진단을 믿을 수 없다.
'못 보는 범위' 문장이 핵심이라 단정 문자열을 그 줄 고유의 문구로 잡았다.
'채널' 로 잡으면 기존 '처방 채널' 줄에 걸려 문장을 빼먹어도 초록이 된다."
```

---

### Task 7: 반증된 주석과 설계를 고치고, 열화 경로 시험을 갈라진 축에 맞춘다

**Files:**
- Modify: `cmd/fd/config.go:11-21` · `cmd/fd/env.go:20-27, 74-76`
- Modify: `cmd/fd/degrade_path_test.go:330-401`
- Modify: `plugins/flightdeck/DESIGN.md:398`

- [ ] **Step 1: `config.go` 의 반증된 주장을 고친다**

`config.go:13-17` 을 바꾼다:

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
```

- [ ] **Step 2: `env.go` 의 주석 둘을 좁힌다**

`env.go:20-23`:

```go
// StateDir 는 **재생성 가능한** 열화 상태(캐시)를 두는 자리다.
//
// ★ 아웃박스는 여기 없다. 그것은 재생성 불가한 판단을 담아서 채널마다 갈리면 안 되고,
// 그래서 OutboxPath 가 고정 자리를 준다 — 그 주석에 판정 전문이 있다.
//
// ★ ${CLAUDE_PLUGIN_ROOT} 에 두지 않는다. 그 경로에는 플러그인 **버전이 들어가서**
// 갱신될 때마다 자리가 바뀌고, 그러면 쌓아 둔 캐시가 갱신 한 번에 사라진다(설계 §7).
```

`env.go:74-76`:

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
    옛 자리에 남은 큐는 **옮기지 않는다** — 재생이 제자리에서 돌려 전송으로 비운다.
    옮기려면 "읽고·쓰고·원본을 치운다"는 세 단계를 원자화해야 하는데 `os.Rename` 하나로는
    그것이 안 된다(이 자리에서 한 번 틀렸다가 재현으로 뒤집혔다).
```

- [ ] **Step 4: 열화 경로 시험을 갈라진 축에 맞춘다**

`degrade_path_test.go:330-338` 의 절 제목·주석·함수 이름을 바꾼다:

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

379-382행의 아웃박스 단정을 바꾼다:

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

- [ ] **Step 5: 전 시험 초록을 확인한다**

Run: `cd plugins/flightdeck/server && go build ./... && go vet ./... && gofmt -l . && go test ./... -race -count=1`
Expected: `gofmt -l .` 무출력, 전 패키지 ok.

- [ ] **Step 6: 커밋**

```bash
git add plugins/flightdeck/server/cmd/fd/config.go plugins/flightdeck/server/cmd/fd/env.go \
        plugins/flightdeck/server/cmd/fd/degrade_path_test.go plugins/flightdeck/DESIGN.md
git commit -m "docs(flightdeck): 반증된 '캐시·아웃박스는 갈려도 된다'를 전 자리에서 고친다

이 레포는 주석이 판단의 저장소라, 틀린 주석을 남기면 다음 사람이 그 위에서 돈다.
축은 열화 여부가 아니라 재생성 가능성이었다.

degrade_path 시험은 본 판정(PLUGIN_ROOT 아래에 아무것도 안 생긴다)을 그대로 두고
아웃박스 단정만 고정 자리로 옮겼다."
```

---

### Task 8: 실물로 확인하고 마무리한다

- [ ] **Step 1: 이 머신의 실제 상태를 본다**

이 머신 `~/.local/state/flightdeck/outbox/rejected.jsonl` 에 격리 1건이 있다(8/3 판단, 409).

```bash
cd plugins/flightdeck/server
echo "== 전"; ls -la ~/.local/state/flightdeck/outbox/ ~/.flightdeck/outbox/ 2>&1 | head -20
go run ./cmd/fd doctor 2>&1 | head -30
```
Expected: doctor 가 고정 자리(`~/.flightdeck/outbox`)를 찍고, 옛 자리 줄에 `격리 1건(그 자리에 남는다)` 을 찍고, 사각 문장을 찍는다. **doctor 는 아무것도 안 보낸다.**

- [ ] **Step 2: 재생을 돌려 본다**

```bash
cd plugins/flightdeck/server && go run ./cmd/fd status >/dev/null 2>&1
echo "== 후"; ls -la ~/.local/state/flightdeck/outbox/ ~/.flightdeck/outbox/ 2>&1
```
Expected: 옛 자리의 `rejected.jsonl` 이 **그대로 있다**(격리는 안 옮긴다). `pending.jsonl` 은 원래 없었다. 고정 자리에는 새로 생기는 것이 없다.

- [ ] **Step 3: 전 검증**

```bash
cd plugins/flightdeck/server && go build ./... && go vet ./... && gofmt -l . && go test ./... -race -count=1
```

- [ ] **Step 4: 시험이 진짜 홈을 안 건드리는지 마지막 확인**

```bash
cd plugins/flightdeck/server
SB=$(mktemp -d) && env HOME="$SB" GOMODCACHE="$(go env GOMODCACHE)" GOCACHE="$(go env GOCACHE)" \
  go test ./... -count=1 >/dev/null 2>&1
echo "flightdeck 흔적:"; find "$SB" -path '*flightdeck*'
chmod -R u+w "$SB" 2>/dev/null; rm -rf "$SB"
```
Expected: **아무것도 안 나온다.**

- [ ] **Step 5: 판단을 남긴다**

`fd note --kind verified` 로 무엇을 실제로 돌려 확인했고 무엇은 못 쟀는지를 적는다.
특히 **동시 재생 경합은 안 닫았다**는 사실을 다시 적는다 — 스펙 §4 끝의 그 문단이
구현에서도 그대로임을 확인하는 것이 이 단계의 일이다.

> ★ **개정(2026-08-10) — 이 계획이 랜딩한 뒤 그 경합을 별개 항목이 닫았다.**
> 커밋 `9b2f0d4`(`flock` + 되쓰기를 병합으로). 이 계획을 되짚어 읽는 사람에게:
> **"동시 재생 경합은 안 닫았다"는 이 계획의 시점에서만 참이다.** 지금 무엇이 닫혔고
> 무엇이 안 닫혔는지는 스펙 §4·§8 의 개정 블록과 DESIGN §7 에 실측과 함께 있다.
> 요약하면 **되쓰기 계열 넷은 닫혔고, fail-open · NFS · 격리 중복 셋은 열려 있다.**

## 자체 검토

**스펙 범위 대조:**

| 스펙 절 | 과제 |
|---|---|
| §3 자리(`OutboxPath`, `StateDir` 은 캐시만) | 2, 4, 7 |
| §4 옮기지 말고 보낸다 | 5 |
| §4 왜 옮기지 않기로 했나(반증 기록) | 7 Step 3(DESIGN), 3·5 의 주석 |
| §4 24시간 TTL — 닫았다고 안 적는다 | 5 주석 + 8 Step 5 |
| §5 후보 다섯 자리(고정 자리·tmp 포함) | 3 |
| §5 정직한 구멍 | 6 |
| §6 반증된 주석 | 7 |
| §7 시험 + 침묵 구멍 + 기존 시험 ①②③④ | 1, 2, 3, 5, 6, 7 |
| §8 안 하는 것 | 계획 전체가 `internal/` 을 안 건드린다 |

**1판 검토에서 확정된 26건 대조:**

| 결함 | 어디서 없앴나 |
|---|---|
| 청구가 살아 있는 청구를 뺏는다 | 청구 자체를 없앴다(과제 5) |
| `Append` 멱등이 재전송을 못 막는다 | 옮기지 않으므로 재청구 경로가 없다 |
| `newOutbox` 호출부 6곳 | 과제 4 Step 4 에 6곳 전부 |
| `keysOf` 재선언 | `queuedKeys` 로 개명(과제 5 Step 1) |
| `withAdopt` 를 읽는 코드 없음 | `flushAll` 이 로그를 내고 doctor 가 잔량을 찍는다(과제 5·6) |
| `FD_STATE_DIR` 전환 시 고정 자리 고아 | `LegacyOutboxDirs` 가 고정 자리를 후보에 넣는다(과제 3) |
| tmp 를 뺀 근거가 틀렸다 | 후보에 되넣었다(과제 3) |
| doctor 단정이 `채널` 에 걸려 공허 | `옛 자리 탐색` 으로 좁혔고 과제 6 Step 5 가 그것을 실측한다 |
| `moveRejected` 가 오류를 삼킨다 | 그 함수가 없어졌다(격리를 안 옮긴다) |
| `hook_stop_test.go` 에 HOME 없음 | 과제 1 Step 4 ② |
| `degrade_path_test.go:233` 의 `sd.sub("outbox")` | 과제 4 Step 4 ④ |
| telemetry 때문에 `find` 가 항상 실패 | `-path '*flightdeck*'` 로 좁혔다(과제 1 Step 6, 과제 8 Step 4) |
| 청구 뺏긴 쪽이 틀린 사유를 올린다 | 청구가 없어졌다 |

**타입 일관성**: `Outbox.dir`·`source`·`now` 는 과제 4에서 정의하고 5·6이 쓴다.
`newOutboxAt` 은 과제 4, `Client.Legacy`·`flushAll` 은 과제 5, `Leftover`·`leftover`·
`LegacyLeftovers` 는 과제 6이다. 시험 도우미 `seedQueue`·`queuedKeys` 는 과제 5에서
만들고 6이 쓴다. `entry(key)`·`mkOutbox(t)`·`quietLogger()`·`ErrUnreachable`·`APIError` 는
기존 것을 쓴다.
