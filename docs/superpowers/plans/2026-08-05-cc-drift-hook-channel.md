# cc 표류 비콘 통로 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 이미 떠 있는 MCP 프로세스가 `/clear`·compact 로 갈린 대화의 현재 `cc_session_id` 를 알아내어, 한 창이 보드에 카드 두 장으로 뜨는 것을 멈춘다.

**Architecture:** 같은 창의 훅과 MCP 는 **같은 claude 프로세스의 자손**이다. MCP 가 `os.Getppid()` 로 그 pid 를 알아내 `~/.flightdeck/windows/<machine>-<pid>-<started>.json` 에 비콘을 심고, 훅은 자기 조상 사슬을 걸어 그 파일을 찾는다. cc 가 갈렸으면 훅이 `POST /api/v1/sessions/{id}/rekey` 로 **카드의 cc 컬럼만 갈아끼운다** — 선점·판단·발자국이 전부 `session.id` 를 참조하므로 그대로 따라온다.

**Tech Stack:** Go 1.24 · stdlib only(`net/http` ServeMux 패턴 라우팅, `encoding/json`, `os`) · SQLite(`modernc.org/sqlite`) · 시험은 표준 `testing`.

## Global Constraints

- **작업 트리는 하나다:** `/home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-cc-drift-hook-channel`. 이 문서의 모든 `file:line` 이 그 기준이다. 브랜치 `fd-cc-drift-hook-channel`.
- **Go 작업 디렉토리:** `plugins/flightdeck/server` (모듈 `github.com/kweiza/flightdeck`).
- **매 태스크 종료 전 필수:** `gofmt -l .` 이 **빈 출력**, `go vet ./...` 통과, `go test ./... -race` 전 패키지 ok.
- **스키마 변경 금지.** `SchemaVersion` 은 2 그대로. 마이그레이션 파일을 만들지 않는다.
- **주석은 한국어 산문.** 판단이 있는 자리에만 쓴다. 되짚어야 할 결정에는 `★` 를 붙인다 — 기존 파일들의 밀도를 따른다.
- **커밋 제목은 영어 소문자 관례:** `feat(flightdeck): …` · `fix(flightdeck): …` · `test(flightdeck): …`. 본문은 무엇을 왜 했는지.
- **`internal/window` 는 `cmd/fd` 를 임포트하지 않는다.** `cmd/fd` 는 `package main` 이다(`cmd/fd/env.go:1`). 의존 방향은 언제나 `cmd/fd → internal/window` 이고 `internal/mcpsrv → internal/window` 다.
- **`Backend` 인터페이스를 넓히지 않는다**(`internal/mcpsrv/backend.go:26`). 넓히면 컴파일로 고정된 `serialProbe`(`serial_test.go:142`) 포함 네 곳이 따라 바뀐다.
- **시험은 진짜 `$HOME` 에 파일을 쓰지 않는다.** 언제나 `t.TempDir()` 를 주입한다.
- **판정기가 찍는 문자열로 시험 전제를 세우지 않는다.** 카드 수·선점 보존은 서비스/스토어를 직접 쳐서 단정한다.

---

## 파일 구조

| 파일 | 책임 |
|---|---|
| `internal/window/beacon.go` (신규) | `Key`·`Beacon` 타입, 파일명 조립, JSON 인코딩/디코딩. 순수. |
| `internal/window/dir.go` (신규) | 비콘 디렉토리 사다리(`Dir`). 순수. |
| `internal/window/ancestry.go` (신규) | `Ancestors` — 주입된 `ppidOf` 로 사슬을 걷는다. 순수. |
| `internal/window/proc_linux.go` (신규) | `//go:build linux` — `/proc` 에서 PPid·시작틱. |
| `internal/window/proc_other.go` (신규) | `//go:build !linux` — **오류를 낸다**(빈 값 금지). |
| `internal/window/store.go` (신규) | `Plant`(병합) · `SaveIdentity` · `Load` · `Find` · `Prune`. 파일 I/O. |
| `internal/store/session.go` | `Tx.Rekey` · `Store.Rekey` 추가. |
| `internal/service/session.go` | `(*Service).Rekey` 추가. |
| `internal/api/handlers_session.go` | `rekeyRequest` · `handleRekey` 추가. |
| `internal/api/api.go` | `routes()` 에 한 줄. |
| `plugins/flightdeck/DESIGN.md` | §6 REST 표에 한 줄(`:318-325`). |
| `internal/mcpsrv/mcpsrv.go` | `WithBeaconDir` 옵션 · 가드 걸린 병합 심기 · `ensureSession` 이 비콘 cc 우선. |
| `internal/mcpsrv/drift.go` | `RenderDrift` 가 사유를 받는다. |
| `cmd/fd/env.go` | `BeaconDir` 를 `window.Dir` 에 위임. |
| `cmd/fd/app.go` | `(*App).Rekey` — `a.cli.do` 로 POST. |
| `cmd/fd/hook.go` | `hookSessionStart` 배선 + 캐시 키 이전 + 가지치기. |

---

### Task 1: `internal/window` — 타입과 파일명

**Files:**
- Create: `plugins/flightdeck/server/internal/window/beacon.go`
- Test: `plugins/flightdeck/server/internal/window/beacon_test.go`

**Interfaces:**
- Consumes: 없음(이 패키지의 첫 태스크).
- Produces: `type Key struct { MachineID string; ClaudePID int; Started string }`, `func (Key) FileName() string`, `type Beacon struct{…}`, `func Encode(Beacon) ([]byte, error)`, `func Decode([]byte) (Beacon, error)`.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/window/beacon_test.go`:

```go
package window

import "testing"

func TestKeyFileNameIsStableAndPathSafe(t *testing.T) {
	k := Key{MachineID: "m-abc", ClaudePID: 3980399, Started: "544443873"}
	if got, want := k.FileName(), "m-abc-3980399-544443873.json"; got != want {
		t.Fatalf("FileName() = %q, want %q", got, want)
	}
}

// ★ machine id 는 hostname 에서 오므로 경로 구분자가 들어올 수 있다.
// 그대로 파일명에 쓰면 디렉토리를 벗어난다.
func TestKeyFileNameScrubsSeparators(t *testing.T) {
	k := Key{MachineID: "a/../b c", ClaudePID: 7, Started: "9"}
	got := k.FileName()
	for _, bad := range []string{"/", "\\", ".."} {
		if contains(got, bad) {
			t.Fatalf("FileName() = %q, 경계 문자 %q 가 남았다", got, bad)
		}
	}
}

func TestEncodeDecodeRoundTrips(t *testing.T) {
	in := Beacon{
		ClaudePID: 3980399, ClaudeStarted: "544443873", MachineID: "m-abc",
		Worktree: "/home/aaron/w", CCSessionID: "cc-new", SessionID: "01ABC",
		UpdatedAt: "2026-08-05T10:00:00Z",
	}
	raw, err := Encode(in)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	out, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if out != in {
		t.Fatalf("round trip 이 값을 바꿨다\n got %+v\nwant %+v", out, in)
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	if _, err := Decode([]byte("not json")); err == nil {
		t.Fatal("깨진 JSON 인데 Decode 가 오류를 안 냈다")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: 빨간지 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/window/ -run 'TestKey|TestEncode|TestDecode' -v
```

Expected: 컴파일 실패 — `undefined: Key` 등.

- [ ] **Step 3: 최소 구현**

`internal/window/beacon.go`:

```go
// Package window 는 Claude Code 창 하나를 가리키는 비콘을 다룬다.
//
// ★ 왜 이 패키지가 internal 에 있나. 쓰는 쪽이 둘이다 — 심는 것은 internal/mcpsrv,
// 읽고 고치는 것은 cmd/fd 의 훅이다. cmd/fd 는 package main 이라 mcpsrv 가 임포트할 수 없으므로
// 두 쪽이 공유하는 판단은 여기 말고 살 자리가 없다.
//
// ★ 그리고 그 판단은 **한 벌이어야 한다.** claude_started 를 얻는 헬퍼가 두 벌이 되면
// 쓰는 쪽과 읽는 쪽의 문자열이 갈려 pid 재사용 방어가 조용히 죽는다.
package window

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Key 는 창 하나의 좌표다. 파일명이 곧 이 값이다.
//
// ★ 축이 셋인 이유. machine 은 여러 머신이 한 홈을 공유(NFS)할 때, pid 는 창끼리,
// started 는 pid 재사용을 가른다. 셋 중 하나라도 빠지면 남의 창 비콘을 자기 것으로 읽는 길이 열린다.
type Key struct {
	MachineID string
	ClaudePID int
	Started   string
}

// FileName 은 이 좌표의 파일 이름이다.
//
// ★ machine id 는 hostname 에서 온다(mcpsrv/identity.go). 즉 **외부 입력**이고
// 경로 구분자가 들어올 수 있다. 그대로 쓰면 디렉토리를 벗어나므로 안전 문자만 남긴다.
func (k Key) FileName() string {
	return scrub(k.MachineID) + "-" + strconv.Itoa(k.ClaudePID) + "-" + scrub(k.Started) + ".json"
}

// Valid 는 이 좌표로 파일을 만들어도 되는지다. 빈 축이 있으면 안 된다 —
// 빈 값끼리는 서로 같아 보여서 다른 창이 한 파일을 공유하게 된다.
func (k Key) Valid() bool {
	return strings.TrimSpace(k.MachineID) != "" && k.ClaudePID > 0 && strings.TrimSpace(k.Started) != ""
}

// ★ 접지 않고 **이스케이프한다.** 불허 문자를 전부 같은 '-' 로 뭉개면 단사가 아니게 되고,
// hostname 에 점이 흔하므로 web-1.corp 와 web.1.corp 가 같은 파일명이 된다 — 그러면
// 한 홈을 공유하는 두 머신이 서로의 비콘을 덮는다. 그것을 막으려고 있는 축이 MachineID 다.
//
// ★ **바이트 단위**다. 룬 단위로 %02x 를 쓰면 자릿수가 가변이라 _4e2d 가 룬 0x4e2d 인지
// 룬 0x4e2 뒤의 'd' 인지 갈리지 않는다. 바이트는 언제나 두 자리라 모호함이 없다.
// 그래서 '_' 는 허용 문자가 아니다 — _5f 로 이스케이프되어야 표식 노릇을 한다.
func scrub(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "_%02x", c)
		}
	}
	// ★ 빈 입력의 표식이 '_' 인 것에 이유가 있다. scrub 은 홑 '_' 를 절대 안 낸다 —
	// '_' 는 허용 문자가 아니라 언제나 _5f 로 이스케이프되므로, 출력의 모든 '_' 는
	// 세 바이트 _XX 토큰의 첫 글자다. 그래서 한 바이트짜리 "_" 는 어떤 입력으로도 도달 불가이고,
	// 무엇과도 겹치지 않는다. "x" 나 "none" 처럼 평범한 글자로 바꾸면 그 글자를 그대로 담은
	// 입력과 충돌한다 — 실제로 그렇게 뒀다가 걸렸다.
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}

// Beacon 은 창 하나가 남긴 내용이다.
//
// ★ CCSessionID 와 SessionID 는 **훅만 쓴다.** MCP 는 자리가 비었을 때 초벌로 채울 뿐
// 이미 있는 값을 덮지 않는다 — MCP 는 대화 중간에 재기동되고, 그때 덮으면 훅이 방금 고친
// 값을 낡은 env 값으로 되돌린다(설계 개정 ②).
type Beacon struct {
	ClaudePID     int    `json:"claude_pid"`
	ClaudeStarted string `json:"claude_started"`
	MachineID     string `json:"machine_id"`
	Worktree      string `json:"worktree"`
	CCSessionID   string `json:"cc_session_id"`
	SessionID     string `json:"session_id"`
	UpdatedAt     string `json:"updated_at"`
}

// Key 는 이 비콘이 주장하는 좌표다.
func (b Beacon) Key() Key {
	return Key{MachineID: b.MachineID, ClaudePID: b.ClaudePID, Started: b.ClaudeStarted}
}

func Encode(b Beacon) ([]byte, error) {
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("비콘 직렬화 실패: %w", err)
	}
	return append(raw, '\n'), nil
}

func Decode(raw []byte) (Beacon, error) {
	var b Beacon
	if err := json.Unmarshal(raw, &b); err != nil {
		return Beacon{}, fmt.Errorf("비콘이 JSON 이 아니다(%d바이트): %w", len(raw), err)
	}
	return b, nil
}
```

- [ ] **Step 4: 초록인지 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/window/ -v
```

Expected: PASS 4건.

- [ ] **Step 5: 커밋**

```bash
cd plugins/flightdeck/server && gofmt -l . && go vet ./internal/window/
git add plugins/flightdeck/server/internal/window/
git commit -m "feat(flightdeck): give a Claude Code window a coordinate a file name can carry"
```

---

### Task 2: `internal/window` — 디렉토리 사다리

**Files:**
- Create: `plugins/flightdeck/server/internal/window/dir.go`
- Test: `plugins/flightdeck/server/internal/window/dir_test.go`

**Interfaces:**
- Consumes: Task 1 의 패키지.
- Produces: `func Dir(get func(string) (string, bool), home string) (path, source string)`.

**왜 이 모양인가:** `cmd/fd/env.go:84-93` 의 `MachineIDPath` 와 **같은 사다리**다 — `FD_STATE_DIR` → `$HOME/.flightdeck` → 임시. `ResolveStateDir` 을 쓰면 안 된다. 그것은 `CLAUDE_PLUGIN_DATA`·`XDG_STATE_HOME` 로 갈리는데 그 둘은 **채널마다 있고 없어서**, 훅과 MCP 가 서로 다른 디렉토리를 보게 된다. 그 사고가 이미 한 번 나서 machine-id 파일이 두 벌이 됐다.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/window/dir_test.go`:

```go
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
```

- [ ] **Step 2: 빨간지 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/window/ -run TestDir -v
```

Expected: `undefined: Dir`.

- [ ] **Step 3: 최소 구현**

`internal/window/dir.go`:

```go
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
```

- [ ] **Step 4: 초록인지 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/window/ -v
```

Expected: PASS.

- [ ] **Step 5: 커밋**

```bash
cd plugins/flightdeck/server && gofmt -l . && go vet ./internal/window/
git add plugins/flightdeck/server/internal/window/
git commit -m "feat(flightdeck): put the beacon where both channels can see it, not where the state dir lands"
```

---

### Task 3: `internal/window` — 계보 걷기와 OS 씰

**Files:**
- Create: `plugins/flightdeck/server/internal/window/ancestry.go`
- Create: `plugins/flightdeck/server/internal/window/proc_linux.go`
- Create: `plugins/flightdeck/server/internal/window/proc_other.go`
- Test: `plugins/flightdeck/server/internal/window/ancestry_test.go`

**Interfaces:**
- Consumes: Task 1·2.
- Produces: `func Ancestors(pid int, ppidOf func(int) (int, error), max int) []int`, `func PPidOf(pid int) (int, error)`, `func StartedOf(pid int) (string, error)`, `var ErrUnsupported error`.

**선례:** `internal/service/disk_unix.go:1` / `disk_other.go:1` 의 `//go:build` 짝. 그 선례의 규칙이 핵심이다 — **지원 안 되는 플랫폼은 빈 값이 아니라 오류를 낸다**(`disk_other.go:11-12`). `StartedOf` 가 빈 문자열을 돌려주면 모든 비콘이 "시작 시각 일치"로 통과해 pid 재사용 방어가 조용히 사라진다.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/window/ancestry_test.go`:

```go
package window

import (
	"errors"
	"os"
	"testing"
)

// fakeTable 은 가짜 프로세스 표다. 시험이 진짜 /proc 을 안 건드리게 하는 씰이다.
func fakeTable(m map[int]int) func(int) (int, error) {
	return func(pid int) (int, error) {
		pp, ok := m[pid]
		if !ok {
			return 0, errors.New("그런 pid 가 없다")
		}
		return pp, nil
	}
}

func TestAncestorsWalksToTheTop(t *testing.T) {
	// 훅 → sh → claude → bash → tmux → init
	table := fakeTable(map[int]int{100: 200, 200: 300, 300: 400, 400: 500, 500: 1, 1: 0})
	got := Ancestors(100, table, 16)
	want := []int{100, 200, 300, 400, 500}
	if len(got) != len(want) {
		t.Fatalf("Ancestors = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Ancestors = %v, want %v", got, want)
		}
	}
}

func TestAncestorsStopsAtMaxDepth(t *testing.T) {
	table := fakeTable(map[int]int{1: 1}) // ★ 자기 자신이 부모인 표 — 무한 순회 유발
	got := Ancestors(1, table, 4)
	if len(got) > 4 {
		t.Fatalf("깊이 제한을 안 지켰다: %v", got)
	}
}

func TestAncestorsStopsWhenTheTableBreaks(t *testing.T) {
	table := fakeTable(map[int]int{7: 8}) // 8 은 표에 없다
	got := Ancestors(7, table, 16)
	if len(got) != 2 || got[0] != 7 || got[1] != 8 {
		t.Fatalf("Ancestors = %v, want [7 8] — 읽히는 데까지는 내야 한다", got)
	}
}

// ★ 이 시험은 실제 OS 씰을 친다. 리눅스에서는 자기 부모를 읽어야 하고,
// 다른 플랫폼에서는 **오류여야 한다**(빈 값이 아니라).
func TestProcSealAgreesWithTheRuntime(t *testing.T) {
	pp, err := PPidOf(os.Getpid())
	if errors.Is(err, ErrUnsupported) {
		t.Skip("이 플랫폼은 계보를 못 읽는다 — 오류를 냈으므로 계약은 지켜졌다")
	}
	if err != nil {
		t.Fatalf("PPidOf: %v", err)
	}
	if pp != os.Getppid() {
		t.Fatalf("PPidOf(self) = %d, os.Getppid() = %d", pp, os.Getppid())
	}
	st, err := StartedOf(os.Getpid())
	if err != nil {
		t.Fatalf("StartedOf: %v", err)
	}
	if st == "" {
		t.Fatal("StartedOf 가 빈 문자열을 냈다 — 빈 값은 모든 비콘을 통과시킨다")
	}
}
```

- [ ] **Step 2: 빨간지 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/window/ -run 'TestAncestors|TestProcSeal' -v
```

Expected: `undefined: Ancestors`, `undefined: PPidOf`.

- [ ] **Step 3: 최소 구현 — 순수 부분**

`internal/window/ancestry.go`:

```go
package window

import "errors"

// ErrUnsupported 는 이 플랫폼에서 계보를 읽을 수 없다는 뜻이다.
//
// ★ 빈 값이 아니라 오류인 것이 계약이다(internal/service/disk_other.go 선례).
// StartedOf 가 빈 문자열을 내면 대조가 언제나 통과해 pid 재사용 방어가 조용히 사라진다.
var ErrUnsupported = errors.New("이 플랫폼에서는 프로세스 계보를 읽을 수 없다")

// Ancestors 는 pid 에서 위로 올라가며 만난 pid 를 순서대로 낸다(자기 자신을 포함한다).
// 순수 함수다 — 프로세스 표를 인자로 받으므로 시험이 진짜 /proc 을 안 건드린다.
//
// ★ 깊이 제한이 필수다. PPid 가 자기 자신을 가리키는 표(컨테이너 pid 네임스페이스에서
// 실제로 나온다)를 만나면 제한이 없을 때 영원히 돈다.
//
// ★ 중간에 못 읽어도 **읽은 데까지 낸다.** 조상 하나를 못 읽었다고 전부 버리면
// 그 아래에서 이미 찾은 비콘까지 못 쓰게 된다.
func Ancestors(pid int, ppidOf func(int) (int, error), max int) []int {
	if pid <= 0 || max <= 0 {
		return nil
	}
	out := make([]int, 0, max)
	seen := make(map[int]bool, max)
	cur := pid
	for len(out) < max {
		if cur <= 1 || seen[cur] {
			break
		}
		seen[cur] = true
		out = append(out, cur)
		pp, err := ppidOf(cur)
		if err != nil {
			break
		}
		cur = pp
	}
	return out
}
```

- [ ] **Step 4: 최소 구현 — OS 씰 두 벌**

`internal/window/proc_linux.go`:

```go
//go:build linux

package window

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// PPidOf 는 /proc/<pid>/stat 에서 부모 pid 를 읽는다.
//
// ★ 필드를 공백으로 자르면 안 된다. 2번 필드가 실행파일 이름이고 괄호로 싸여 있는데
// 그 안에 공백이 들어갈 수 있다("(fd mcp)"). 그래서 **마지막 ')' 뒤부터** 자른다.
func PPidOf(pid int) (int, error) {
	f, err := statFields(pid)
	if err != nil {
		return 0, err
	}
	// f[0] 은 3번 필드(state)다. PPid 는 4번 필드이므로 f[1].
	if len(f) < 2 {
		return 0, fmt.Errorf("/proc/%d/stat 이 짧다(필드 %d개)", pid, len(f))
	}
	pp, err := strconv.Atoi(f[1])
	if err != nil {
		return 0, fmt.Errorf("/proc/%d/stat 의 PPid 가 수가 아니다(%q): %w", pid, f[1], err)
	}
	return pp, nil
}

// StartedOf 는 부팅 뒤 경과 틱(22번 필드)을 문자열 그대로 낸다.
//
// ★ 파싱하지 않는다. 쓰는 쪽과 읽는 쪽이 같은 헬퍼를 쓰므로 **일관성만 있으면 되고**
// 이식 가능한 의미는 필요 없다. 수로 바꾸면 오버플로·단위 해석이라는 틀릴 거리만 는다.
func StartedOf(pid int) (string, error) {
	f, err := statFields(pid)
	if err != nil {
		return "", err
	}
	// 22번 필드는 3번 필드부터 세어 20번째 → f[19].
	if len(f) < 20 {
		return "", fmt.Errorf("/proc/%d/stat 이 짧다(필드 %d개, 20개 이상이어야 한다)", pid, len(f))
	}
	if strings.TrimSpace(f[19]) == "" {
		return "", fmt.Errorf("/proc/%d/stat 의 시작 틱이 비었다", pid)
	}
	return f[19], nil
}

// statFields 는 /proc/<pid>/stat 을 3번 필드부터 자른 조각으로 낸다.
func statFields(pid int) ([]string, error) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return nil, fmt.Errorf("/proc/%d/stat 을 못 읽었다: %w", pid, err)
	}
	s := string(raw)
	i := strings.LastIndex(s, ")")
	if i < 0 || i+2 > len(s) {
		return nil, fmt.Errorf("/proc/%d/stat 의 꼴이 예상과 다르다", pid)
	}
	return strings.Fields(s[i+2:]), nil
}
```

`internal/window/proc_other.go`:

```go
//go:build !linux

package window

// ★ 지원 안 되는 플랫폼은 **오류를 낸다.** 빈 값을 돌려주면 호출부가 "읽었는데 비어 있다"와
// "못 읽었다"를 구분 못 하고, 그러면 시작 시각 대조가 언제나 통과해 방어가 사라진다
// (internal/service/disk_other.go 와 같은 규율).
func PPidOf(pid int) (int, error) { return 0, ErrUnsupported }

func StartedOf(pid int) (string, error) { return "", ErrUnsupported }
```

- [ ] **Step 5: 초록인지 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/window/ -v
GOOS=darwin go build ./internal/window/   # 다른 플랫폼도 컴파일되는지
```

Expected: PASS 전부, darwin 빌드 성공.

- [ ] **Step 6: 커밋**

```bash
cd plugins/flightdeck/server && gofmt -l . && go vet ./internal/window/
git add plugins/flightdeck/server/internal/window/
git commit -m "feat(flightdeck): walk the process chain that both a hook and its MCP share"
```

---

### Task 4: `internal/window` — 심기(병합)와 읽기

**Files:**
- Create: `plugins/flightdeck/server/internal/window/store.go`
- Test: `plugins/flightdeck/server/internal/window/store_test.go`

**Interfaces:**
- Consumes: Task 1·2·3.
- Produces:
  - `func Plant(dir string, k Key, worktree, cc string, now time.Time) (Beacon, error)`
  - `func SaveIdentity(dir string, k Key, cc, sessionID string, now time.Time) (Beacon, error)`
  - `func Load(dir string, k Key) (Beacon, error)`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/window/store_test.go`:

```go
package window

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func k1() Key { return Key{MachineID: "m1", ClaudePID: 42, Started: "1000"} }

func TestPlantCreatesWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	b, err := Plant(dir, k1(), "/w", "cc-old", at("2026-08-05T10:00:00Z"))
	if err != nil {
		t.Fatalf("Plant: %v", err)
	}
	if b.CCSessionID != "cc-old" || b.Worktree != "/w" {
		t.Fatalf("심은 값이 안 들어갔다: %+v", b)
	}
	if _, err := os.Stat(filepath.Join(dir, k1().FileName())); err != nil {
		t.Fatalf("파일이 안 생겼다: %v", err)
	}
}

// ★ 이 설계에서 가장 중요한 회귀 시험이다(설계 개정 ②).
// MCP 는 대화 중간에 재기동된다 — 실측: pid 643548 이 부모 claude 보다 6.6시간 늦게 떴다.
// 그때 통째로 덮으면 훅이 방금 고친 {새 cc, 카드A} 가 그 프로세스의 낡은 env cc 로 되돌아가고,
// 이어서 ensureSession 이 그 낡은 값을 집어 **이 기능이 고치려는 버그를 그대로 재현한다.**
func TestPlantNeverOverwritesTheHooksIdentity(t *testing.T) {
	dir := t.TempDir()
	if _, err := Plant(dir, k1(), "/w", "cc-old", at("2026-08-05T10:00:00Z")); err != nil {
		t.Fatalf("Plant: %v", err)
	}
	if _, err := SaveIdentity(dir, k1(), "cc-new", "card-A", at("2026-08-05T11:00:00Z")); err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}

	// MCP 재기동 — 자기 낡은 env cc 로 다시 심는다.
	got, err := Plant(dir, k1(), "/w", "cc-old", at("2026-08-05T12:00:00Z"))
	if err != nil {
		t.Fatalf("두 번째 Plant: %v", err)
	}
	if got.CCSessionID != "cc-new" {
		t.Fatalf("재기동 심기가 cc 를 덮었다: %q — 버그가 되살아난다", got.CCSessionID)
	}
	if got.SessionID != "card-A" {
		t.Fatalf("재기동 심기가 session_id 를 덮었다: %q", got.SessionID)
	}
}

func TestPlantRefusesAnIncompleteKey(t *testing.T) {
	dir := t.TempDir()
	// ★ 설계 개정 ① — 정체가 반쪽이면 파일을 만들지 않는다.
	// Cursor 가 띄운 MCP 는 부모가 claude 가 아니고 CLAUDE_* 가 하나도 없다.
	// 거기서 심으면 어떤 훅도 영영 못 맞추는 pid 로 키가 잡힌다.
	if _, err := Plant(dir, Key{MachineID: "m1", ClaudePID: 0, Started: "1000"}, "/w", "cc", at("2026-08-05T10:00:00Z")); err == nil {
		t.Fatal("pid 가 없는 좌표인데 Plant 가 통과했다")
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 0 {
		t.Fatalf("거절했는데 파일이 %d개 생겼다", len(ents))
	}
}

func TestPlantRefusesAnEmptyCC(t *testing.T) {
	dir := t.TempDir()
	if _, err := Plant(dir, k1(), "/w", "", at("2026-08-05T10:00:00Z")); err == nil {
		t.Fatal("cc 가 빈데 Plant 가 통과했다")
	}
}

func TestLoadReportsAbsenceDistinctly(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(dir, k1()); err == nil {
		t.Fatal("없는 비콘인데 Load 가 오류를 안 냈다")
	}
}

func TestSaveIdentityKeepsCoordinates(t *testing.T) {
	dir := t.TempDir()
	if _, err := Plant(dir, k1(), "/w", "cc-old", at("2026-08-05T10:00:00Z")); err != nil {
		t.Fatalf("Plant: %v", err)
	}
	b, err := SaveIdentity(dir, k1(), "cc-new", "card-A", at("2026-08-05T11:00:00Z"))
	if err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}
	if b.Worktree != "/w" || b.ClaudePID != 42 || b.ClaudeStarted != "1000" {
		t.Fatalf("좌표가 지워졌다: %+v", b)
	}
}
```

- [ ] **Step 2: 빨간지 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/window/ -run 'TestPlant|TestLoad|TestSaveIdentity' -v
```

Expected: `undefined: Plant`.

- [ ] **Step 3: 최소 구현**

`internal/window/store.go`:

```go
package window

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const stamp = "2006-01-02T15:04:05Z"

// Load 는 좌표 하나의 비콘을 읽는다. 없으면 오류다.
func Load(dir string, k Key) (Beacon, error) {
	raw, err := os.ReadFile(filepath.Join(dir, k.FileName()))
	if err != nil {
		return Beacon{}, fmt.Errorf("비콘을 못 읽었다(%s): %w", k.FileName(), err)
	}
	return Decode(raw)
}

// Plant 는 MCP 가 자기 창의 자리를 표시하는 일이다.
//
// ★ **병합이다, 덮어쓰기가 아니다**(설계 개정 ②). MCP 는 대화 중간에 재기동되고,
// 그때 통째로 쓰면 훅이 방금 고쳐 놓은 cc·session_id 를 이 프로세스의 낡은 env 값으로
// 되돌린다 — 그리고 ensureSession 이 그 낡은 값을 집어 고치려던 버그를 재현한다.
// 그래서 이미 있는 비콘의 **정체 두 필드는 절대 안 건드린다.**
//
// ★ 좌표가 반쪽이거나 cc 가 없으면 **아무것도 안 쓴다**(설계 개정 ①).
// Cursor 처럼 claude 가 아닌 부모 밑에서 뜬 MCP 는 어떤 훅도 못 맞추는 pid 로 키를 잡게 되므로,
// 그 자리에 파일을 남기면 가지치기 대상만 늘고 얻는 것이 없다.
func Plant(dir string, k Key, worktree, cc string, now time.Time) (Beacon, error) {
	if !k.Valid() {
		return Beacon{}, fmt.Errorf("창 좌표가 반쪽이라 심지 않는다(machine=%q pid=%d started=%q)",
			k.MachineID, k.ClaudePID, k.Started)
	}
	if strings.TrimSpace(cc) == "" {
		return Beacon{}, fmt.Errorf("cc_session_id 가 없어 심지 않는다 — 이 프로세스는 자기가 어느 대화인지 모른다")
	}

	b := Beacon{
		ClaudePID: k.ClaudePID, ClaudeStarted: k.Started, MachineID: k.MachineID,
		Worktree: worktree, CCSessionID: cc, UpdatedAt: now.UTC().Format(stamp),
	}
	if old, err := Load(dir, k); err == nil {
		if strings.TrimSpace(old.CCSessionID) != "" {
			b.CCSessionID = old.CCSessionID
		}
		b.SessionID = old.SessionID
	}
	if err := write(dir, k, b); err != nil {
		return Beacon{}, err
	}
	return b, nil
}

// SaveIdentity 는 훅이 현재 cc 와 그 카드를 적는 일이다. 좌표는 보존한다.
func SaveIdentity(dir string, k Key, cc, sessionID string, now time.Time) (Beacon, error) {
	if !k.Valid() {
		return Beacon{}, fmt.Errorf("창 좌표가 반쪽이라 적지 않는다")
	}
	b, err := Load(dir, k)
	if err != nil {
		// 비콘이 없으면 훅이 처음 만드는 것이다. 좌표는 인자에서 온다.
		b = Beacon{ClaudePID: k.ClaudePID, ClaudeStarted: k.Started, MachineID: k.MachineID}
	}
	if strings.TrimSpace(cc) != "" {
		b.CCSessionID = cc
	}
	if strings.TrimSpace(sessionID) != "" {
		b.SessionID = sessionID
	}
	b.UpdatedAt = now.UTC().Format(stamp)
	if err := write(dir, k, b); err != nil {
		return Beacon{}, err
	}
	return b, nil
}

// write 는 비콘 하나를 원자적으로 적는다.
//
// ★ 임시 파일 이름에 **자기 pid 를 넣는다.** 키마다 고정 임시 경로를 쓰면 같은 창의 훅과 MCP 가
// 동시에 쓸 때 서로의 임시 파일을 덮는다 — cmd/fd 의 Cache.Put 이 가진 바로 그 결함이다
// (cmd/fd/client.go:67-83 이 스스로 동시성 안전하지 않다고 적어 뒀다).
func write(dir string, k Key, b Beacon) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("비콘 디렉토리를 못 만들었다(%s): %w", dir, err)
	}
	raw, err := Encode(b)
	if err != nil {
		return err
	}
	final := filepath.Join(dir, k.FileName())
	tmp := final + ".tmp." + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("비콘 임시 파일을 못 적었다: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("비콘을 제자리로 못 옮겼다: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: 초록인지 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/window/ -v
```

Expected: PASS 전부.

- [ ] **Step 5: 변이 검증 — 병합 시험이 진짜 잡는지 본다**

`store.go` 의 `Plant` 에서 `if old, err := Load(dir, k); err == nil { … }` 블록을 잠시 지우고:

```bash
cd plugins/flightdeck/server && go test ./internal/window/ -run TestPlantNeverOverwrites -v
```

Expected: **FAIL** — `재기동 심기가 cc 를 덮었다: "cc-old"`. 확인했으면 블록을 되돌리고 다시 초록인지 본다.

- [ ] **Step 6: 커밋**

```bash
cd plugins/flightdeck/server && gofmt -l . && go vet ./internal/window/
git add plugins/flightdeck/server/internal/window/
git commit -m "feat(flightdeck): plant a beacon by merging, because the MCP restarts mid-conversation"
```

---

### Task 5: `internal/window` — 찾기(계보 대조)와 가지치기

**Files:**
- Modify: `plugins/flightdeck/server/internal/window/store.go`
- Test: `plugins/flightdeck/server/internal/window/find_test.go`

**Interfaces:**
- Consumes: Task 4.
- Produces:
  - `type Match struct { Beacon Beacon; Key Key }`
  - `func Find(dir, machineID string, ancestors []int, startedOf func(int) (string, error)) (Match, bool, string)` — 셋째 반환값은 **못 찾은 사유**다.
  - `func Prune(dir string, alive func(int) bool) (removed int, err error)`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/window/find_test.go`:

```go
package window

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func startedTable(m map[int]string) func(int) (string, error) {
	return func(pid int) (string, error) {
		s, ok := m[pid]
		if !ok {
			return "", ErrUnsupported
		}
		return s, nil
	}
}

func TestFindMatchesAnAncestorEvenThroughAShell(t *testing.T) {
	dir := t.TempDir()
	k := Key{MachineID: "m1", ClaudePID: 300, Started: "1000"}
	if _, err := Plant(dir, k, "/w", "cc-old", time.Unix(0, 0)); err != nil {
		t.Fatalf("Plant: %v", err)
	}
	// 훅(100) → sh(200) → claude(300)
	m, ok, why := Find(dir, "m1", []int{100, 200, 300}, startedTable(map[int]string{100: "x", 200: "y", 300: "1000"}))
	if !ok {
		t.Fatalf("조상 사슬에 비콘이 있는데 못 찾았다: %s", why)
	}
	if m.Beacon.CCSessionID != "cc-old" || m.Key.ClaudePID != 300 {
		t.Fatalf("엉뚱한 비콘을 찾았다: %+v", m)
	}
}

// ★ 이것이 설계에서 가장 위험했던 자리다(개정 ③).
// 같은 머신·같은 워크트리에 창이 다섯이다. 조상이 아닌 창의 비콘을 집으면
// 그 창의 카드를 이 대화의 cc 로 rekey 하게 되고, 그 창의 선점과 판단이 통째로 딴 대화에 붙는다.
func TestFindRefusesABeaconThatIsNotAnAncestor(t *testing.T) {
	dir := t.TempDir()
	other := Key{MachineID: "m1", ClaudePID: 999, Started: "1000"}
	if _, err := Plant(dir, other, "/w", "cc-other", time.Unix(0, 0)); err != nil {
		t.Fatalf("Plant: %v", err)
	}
	// 디렉토리에 비콘이 딱 하나뿐이어도 조상이 아니면 안 쓴다.
	_, ok, why := Find(dir, "m1", []int{100, 200, 300}, startedTable(map[int]string{100: "x", 200: "y", 300: "1000"}))
	if ok {
		t.Fatal("조상이 아닌 창의 비콘을 집었다 — 남의 카드를 rekey 하게 된다")
	}
	if why == "" {
		t.Fatal("못 찾은 사유가 비었다 — 폴백 문구가 무엇을 말할지 알 수 없다")
	}
}

func TestFindRefusesWhenStartTimeDisagrees(t *testing.T) {
	dir := t.TempDir()
	k := Key{MachineID: "m1", ClaudePID: 300, Started: "1000"}
	if _, err := Plant(dir, k, "/w", "cc-old", time.Unix(0, 0)); err != nil {
		t.Fatalf("Plant: %v", err)
	}
	// pid 는 같지만 시작 시각이 다르다 → pid 가 재사용된 것이다.
	_, ok, why := Find(dir, "m1", []int{300}, startedTable(map[int]string{300: "9999"}))
	if ok {
		t.Fatalf("pid 재사용인데 통과했다 (%s)", why)
	}
}

func TestFindRefusesAnotherMachine(t *testing.T) {
	dir := t.TempDir()
	k := Key{MachineID: "m1", ClaudePID: 300, Started: "1000"}
	if _, err := Plant(dir, k, "/w", "cc-old", time.Unix(0, 0)); err != nil {
		t.Fatalf("Plant: %v", err)
	}
	if _, ok, _ := Find(dir, "m2", []int{300}, startedTable(map[int]string{300: "1000"})); ok {
		t.Fatal("다른 머신의 비콘을 집었다")
	}
}

func TestPruneRemovesDeadWindowsOnly(t *testing.T) {
	dir := t.TempDir()
	live := Key{MachineID: "m1", ClaudePID: 300, Started: "1000"}
	dead := Key{MachineID: "m1", ClaudePID: 301, Started: "1000"}
	for _, k := range []Key{live, dead} {
		if _, err := Plant(dir, k, "/w", "cc", time.Unix(0, 0)); err != nil {
			t.Fatalf("Plant: %v", err)
		}
	}
	n, err := Prune(dir, func(pid int) bool { return pid == 300 })
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 1 {
		t.Fatalf("Prune 이 %d개 지웠다, 1개여야 한다", n)
	}
	if _, err := os.Stat(filepath.Join(dir, live.FileName())); err != nil {
		t.Fatal("살아 있는 창의 비콘을 지웠다")
	}
}

func TestPruneOnAMissingDirIsNotAnError(t *testing.T) {
	if _, err := Prune(filepath.Join(t.TempDir(), "nope"), func(int) bool { return true }); err != nil {
		t.Fatalf("없는 디렉토리는 조용히 넘어가야 한다: %v", err)
	}
}
```

- [ ] **Step 2: 빨간지 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/window/ -run 'TestFind|TestPrune' -v
```

Expected: `undefined: Find`.

- [ ] **Step 3: 최소 구현 — `store.go` 끝에 덧붙인다**

```go
// Match 는 계보 대조를 통과한 비콘 하나다.
type Match struct {
	Beacon Beacon
	Key    Key
}

// Find 는 내 조상 사슬 위에 있는 비콘을 찾는다.
//
// ★ **디렉토리를 훑지 않는다.** 조상 pid 로 파일 이름을 조립해 직접 연다.
// 훑어서 "하나뿐이니 이것이겠지"로 고르면, 같은 머신·같은 워크트리에 창이 다섯인 이 환경에서
// **남의 창 비콘을 집어 그 창의 카드를 이 대화의 cc 로 rekey 한다**(설계 개정 ③).
// 그 창의 선점과 판단이 통째로 딴 대화에 붙는 것이라 이 설계에서 가장 나쁜 결과다.
//
// 못 찾은 사유를 함께 낸다. 사유가 없으면 폴백 문구가 "왜 수리가 안 됐나"에 답할 수 없다.
func Find(dir, machineID string, ancestors []int, startedOf func(int) (string, error)) (Match, bool, string) {
	if len(ancestors) == 0 {
		return Match{}, false, "조상 사슬을 못 걸었다 — 이 플랫폼에서 프로세스 계보를 읽을 수 없다"
	}
	var last string
	for _, pid := range ancestors {
		started, err := startedOf(pid)
		if err != nil {
			last = "조상 " + strconv.Itoa(pid) + " 의 시작 시각을 못 읽었다: " + err.Error()
			continue
		}
		k := Key{MachineID: machineID, ClaudePID: pid, Started: started}
		b, err := Load(dir, k)
		if err != nil {
			continue // 이 조상은 비콘을 안 남겼다. 위로 계속 간다
		}
		if b.ClaudePID != pid || b.ClaudeStarted != started || b.MachineID != machineID {
			// 파일 이름은 맞는데 내용이 딴 좌표다 — 손상됐거나 남의 것이다.
			last = "비콘 내용의 좌표가 파일 이름과 안 맞는다(pid " + strconv.Itoa(pid) + ")"
			continue
		}
		return Match{Beacon: b, Key: k}, true, ""
	}
	if last == "" {
		last = "조상 사슬 어디에도 이 머신의 비콘이 없다(조상 " + strconv.Itoa(len(ancestors)) + "개를 봤다)"
	}
	return Match{}, false, last
}

// Prune 은 죽은 창의 비콘을 지운다.
//
// ★ **지우기만 한다.** 남의 파일을 고쳐 쓰지 않으므로 다른 창의 rename 과 안 싸운다.
// 지우려는 순간 그 창이 살아 있었다면 다음 심기가 다시 만든다 — 손해가 없다.
//
// 디렉토리가 없는 것은 오류가 아니다. 첫 실행이 그렇다.
func Prune(dir string, alive func(int) bool) (removed int, err error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("비콘 디렉토리를 못 읽었다(%s): %w", dir, err)
	}
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		raw, rerr := os.ReadFile(filepath.Join(dir, name))
		if rerr != nil {
			continue
		}
		b, derr := Decode(raw)
		if derr != nil {
			continue // 못 읽는 파일을 지우지 않는다 — 남이 쓰는 중일 수 있다
		}
		if b.ClaudePID > 0 && !alive(b.ClaudePID) {
			if os.Remove(filepath.Join(dir, name)) == nil {
				removed++
			}
		}
	}
	return removed, nil
}
```

- [ ] **Step 4: 초록인지 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/window/ -v -race
```

Expected: PASS 전부.

- [ ] **Step 5: 변이 검증 — 계보 가드가 진짜 잡는지 본다**

`Find` 안의 `if b.ClaudePID != pid || …` 검사와 조상 순회를 잠시 "디렉토리에서 아무거나 하나 고르기"로 바꾸면 `TestFindRefusesABeaconThatIsNotAnAncestor` 가 **FAIL** 해야 한다. 확인 후 되돌린다.

- [ ] **Step 6: 커밋**

```bash
cd plugins/flightdeck/server && gofmt -l . && go vet ./internal/window/
git add plugins/flightdeck/server/internal/window/
git commit -m "feat(flightdeck): match a beacon by ancestry only, never by being the only one there"
```

---

### Task 6: `store.Rekey` — 카드의 cc 를 옮긴다

**Files:**
- Modify: `plugins/flightdeck/server/internal/store/session.go` (`Tx.SetSessionState` 옆, `:204-235` 부근)
- Test: `plugins/flightdeck/server/internal/store/session_rekey_test.go`

**Interfaces:**
- Consumes: 없음(서버 계층 첫 태스크).
- Produces: `func (t *Tx) Rekey(id, ccSessionID string) (model.Session, error)`, `func (s *Store) Rekey(ctx context.Context, id, ccSessionID string) (model.Session, error)`.

**따라야 할 꼴:** `Tx.SetSessionState`(`session.go:204`)와 그 `Store` 짝(`:225`). `RowsAffected()==0` 판정은 `affectedOne`(`item.go:550`), 오류 감싸기는 `writeErr(…, writeTarget{Target: TargetSession, …})`, 이벤트는 **`Tx.LogEvent`**(`store.go:148`) — `Store.LogEvent` 는 별도 연결이라 Tx 안에서 부르면 교착한다.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/store/session_rekey_test.go`:

```go
package store

import (
	"context"
	"errors"
	"testing"
)

func TestRekeyMovesTheCCAndKeepsTheCard(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	a := mustSession(t, s, "p", "cc-old")

	got, err := s.Rekey(ctx, a.ID, "cc-new")
	if err != nil {
		t.Fatalf("Rekey: %v", err)
	}
	if got.ID != a.ID {
		t.Fatalf("카드 id 가 바뀌었다: %q → %q", a.ID, got.ID)
	}
	if got.CCSessionID != "cc-new" {
		t.Fatalf("cc 가 안 옮겨졌다: %q", got.CCSessionID)
	}
	// ★ 렌더된 문자열이 아니라 저장소를 직접 쳐서 단정한다.
	again, err := s.GetSession(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if again.CCSessionID != "cc-new" {
		t.Fatalf("다시 읽으니 %q 다", again.CCSessionID)
	}
}

// ★ 이 시험이 설계의 핵심 주장을 지킨다 — "합치기는 컬럼 하나다".
// 선점과 판단이 session.id 를 참조하므로 cc 를 갈아도 그대로 붙어 있어야 한다.
func TestRekeyKeepsClaimsAndJudgments(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	a := mustSession(t, s, "p", "cc-old")
	it := mustItem(t, s, "p", "item-1")

	claimBefore := claimItem(t, s, it.ID, a.ID)
	noteBefore := addJudgment(t, s, a.ID, "decision", "왜 그렇게 했나")

	if _, err := s.Rekey(ctx, a.ID, "cc-new"); err != nil {
		t.Fatalf("Rekey: %v", err)
	}

	if got := claimHolder(t, s, it.ID); got != a.ID {
		t.Fatalf("선점이 따라오지 않았다: holder=%q, want %q (claim %v)", got, a.ID, claimBefore)
	}
	if n := judgmentCount(t, s, a.ID); n != 1 {
		t.Fatalf("판단이 %d건이다, 1건이어야 한다 (note %v)", n, noteBefore)
	}
}

func TestRekeyRefusesACCAnotherCardHolds(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	a := mustSession(t, s, "p", "cc-a")
	_ = mustSession(t, s, "p", "cc-b") // 같은 machine·worktree, 다른 cc

	_, err := s.Rekey(ctx, a.ID, "cc-b")
	if err == nil {
		t.Fatal("UNIQUE 를 깨는 rekey 가 통과했다")
	}
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("ConflictError 가 아니라 %T 가 왔다 — api 가 409 로 못 바꾼다: %v", err, err)
	}
	if ce.Kind != ConflictDuplicate {
		t.Fatalf("Kind = %q, want %q", ce.Kind, ConflictDuplicate)
	}
}

func TestRekeyOnAMissingCardIsNotFound(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")

	_, err := s.Rekey(ctx, "no-such-card", "cc-new")
	if err == nil {
		t.Fatal("없는 카드인데 통과했다")
	}
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("NotFoundError 가 아니라 %T 가 왔다: %v", err, err)
	}
}

func TestRekeyRefusesAnEmptyCC(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	a := mustSession(t, s, "p", "cc-old")
	if _, err := s.Rekey(ctx, a.ID, "  "); err == nil {
		t.Fatal("빈 cc 로 rekey 가 통과했다 — 정체가 사라진 카드가 남는다")
	}
}

func TestRekeyLeavesAnEvent(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	a := mustSession(t, s, "p", "cc-old")
	if _, err := s.Rekey(ctx, a.ID, "cc-new"); err != nil {
		t.Fatalf("Rekey: %v", err)
	}
	evs, err := s.ListSessionEvents(ctx, a.ID)
	if err != nil {
		t.Fatalf("ListSessionEvents: %v", err)
	}
	found := false
	for _, e := range evs {
		if e.Kind == "session.rekey" {
			found = true
		}
	}
	if !found {
		t.Fatalf("session.rekey 이벤트가 없다 — cc 가 조용히 바뀌면 원인에 도달할 길이 없다 (%d건)", len(evs))
	}
}
```

> **구현자에게:** 위 시험이 쓰는 헬퍼 중 `newStore`·`seed`·`mustSession`·`mustItem` 은 `store_test.go` 에 이미 있다. `claimItem`·`claimHolder`·`addJudgment`·`judgmentCount` 는 **없을 수 있다.** 먼저 `grep -n 'func claimItem\|func addJudgment\|func claimHolder\|func judgmentCount' internal/store/*_test.go` 로 확인하고, 없으면 기존 시험이 선점·판단을 만드는 방식(`grep -rn 'Claim\|AddJudgment' internal/store/*_test.go | head`)을 그대로 따라 이 파일 안에 작은 헬퍼로 만든다. `ListSessionEvents` 의 정확한 시그니처와 이벤트 필드 이름도 `grep -n 'func (s \*Store) ListSessionEvents' -A 12 internal/store/event.go` 로 확인해 맞춘다.

- [ ] **Step 2: 빨간지 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/store/ -run TestRekey -v
```

Expected: `s.Rekey undefined`.

- [ ] **Step 3: 최소 구현 — `session.go` 의 `SetSessionState` 아래에 넣는다**

```go
// Rekey 는 카드의 cc_session_id 만 갈아끼운다.
//
// ★ 이것이 "카드 두 장을 하나로 합치기"의 전부다. 선점·판단·발자국·자원이 전부
// session(id) 를 참조하고 cc_session_id 는 UNIQUE (machine_id, worktree, cc_session_id) 에만
// 쓰이므로, 이 한 줄로 원장이 통째로 따라온다. 표를 옮기는 코드가 필요 없다.
//
// ★ 언제 이것이 필요한가. /clear·compact 로 대화의 cc 가 갈리면 이미 떠 있는 MCP 프로세스는
// 옛 값을 계속 쓴다(environ 은 exec 뒤 안 바뀐다). 훅만 새 값을 보므로, 훅이 이 갈래로
// 카드를 따라오게 한다.
//
// 빈 cc 를 거절한다 — 정체가 사라진 카드는 3중키에서 다른 빈 카드와 충돌하고,
// 그 카드가 쥔 선점은 아무도 회수할 수 없다.
func (t *Tx) Rekey(id, ccSessionID string) (model.Session, error) {
	cc := strings.TrimSpace(ccSessionID)
	if cc == "" {
		return model.Session{}, fmt.Errorf("cc_session_id 가 비었다 — 정체 없는 카드를 만들 수 없다")
	}
	res, err := t.tx.ExecContext(t.ctx,
		`UPDATE session SET cc_session_id = ? WHERE id = ?`, cc, id)
	if err != nil {
		return model.Session{}, writeErr(err, writeTarget{Target: TargetSession, ID: id},
			"세션 cc 갈아끼우기 실패(session=%s cc=%s)", clip(id, 64), clip(cc, 64))
	}
	if err := affectedOne(res, NFSession, "", id); err != nil {
		return model.Session{}, err
	}
	s, err := t.GetSession(id)
	if err != nil {
		return model.Session{}, err
	}
	// ★ 이벤트를 남긴다. 카드의 cc 가 조용히 바뀌면 나중에 아무도 원인에 도달 못 한다 —
	// 이 기능을 만들기 위해 /proc 을 뒤져야 했던 것이 정확히 그 이유였다.
	// Tx 안에서는 Tx.LogEvent 다. Store.LogEvent 는 별도 연결이라 여기서 부르면 교착한다.
	t.LogEvent("session.rekey", s.Project, s.ID, map[string]any{"cc_session_id": cc})
	return s, nil
}

// Rekey 는 Tx.Rekey 의 단독 실행 짝이다.
func (s *Store) Rekey(ctx context.Context, id, ccSessionID string) (model.Session, error) {
	var out model.Session
	err := s.Tx(ctx, func(t *Tx) error {
		var err error
		out, err = t.Rekey(id, ccSessionID)
		return err
	})
	return out, err
}
```

> **구현자에게:** `affectedOne` 의 정확한 시그니처를 `grep -n 'func affectedOne' -A 8 internal/store/item.go` 로 확인하고 인자를 맞춰라. `writeTarget` 의 필드 이름도 `grep -n 'type writeTarget' -A 6 internal/store/constraint.go` 로 확인해라. `strings`·`fmt` 임포트가 이미 있는지 보고 없으면 추가한다.

- [ ] **Step 4: 초록인지 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/store/ -run TestRekey -v -race
```

Expected: PASS 6건.

- [ ] **Step 5: 전 패키지 확인 + 커밋**

```bash
cd plugins/flightdeck/server && gofmt -l . && go vet ./... && go test ./... -race
git add plugins/flightdeck/server/internal/store/
git commit -m "feat(flightdeck): move a card's cc without moving anything else, because the ledger hangs off its id"
```

---

### Task 7: service + api — rekey 경로

**Files:**
- Modify: `plugins/flightdeck/server/internal/service/session.go` (`SetState` 옆, `:343` 부근)
- Modify: `plugins/flightdeck/server/internal/api/handlers_session.go`
- Modify: `plugins/flightdeck/server/internal/api/api.go` (`routes()`, `:181-187` 세션 블록)
- Modify: `plugins/flightdeck/DESIGN.md` (§6 REST 표, `:318-325`)
- Test: `plugins/flightdeck/server/internal/api/rekey_test.go`

**Interfaces:**
- Consumes: Task 6 의 `(*Store).Rekey`.
- Produces: `func (s *Service) Rekey(ctx context.Context, sessionID, ccSessionID string) (model.Session, error)`, 라우트 `POST /api/v1/sessions/{id}/rekey`.

**따라야 할 꼴:** `handlePrescriptions`(라우트 `api.go:186` `POST /api/v1/sessions/{id}/prescriptions`)가 경로 파라미터 있는 POST 의 본보기다. 핸들러는 `id := r.PathValue("id")` → `infoFrom(r.Context()).setSession(id)` → `s.decode(w, r, &req)` → 서비스 호출 → `s.fail(w, r, err)` → `s.publish(...)` → `s.writeJSON(...)`. 오류→상태 변환은 손대지 않는다 — `ClassifyError`(`api/errors.go:57`)가 `*store.ConflictError` 를 이미 409 로, `*store.NotFoundError` 를 404 로 바꾼다.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/api/rekey_test.go`:

```go
package api

import (
	"net/http"
	"testing"
)

// ★ 헬퍼 이름은 이 패키지의 기존 시험을 따른다.
// 먼저 `grep -n 'func newHarness\|func (h \*harness)' internal/api/helper_test.go` 로 확인하고
// 아래 h.post/h.openSession 자리를 그 이름으로 바꿔라.

func TestRekeyMovesTheCardAndReturnsIt(t *testing.T) {
	h := newHarness(t)
	card := h.openSession(t, "cc-old")

	res := h.post(t, "/api/v1/sessions/"+card.ID+"/rekey", map[string]any{"cc_session_id": "cc-new"})
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", res.Code, res.Body.String())
	}
	if got := h.sessionCC(t, card.ID); got != "cc-new" {
		t.Fatalf("저장소의 cc = %q, want %q", got, "cc-new")
	}
}

func TestRekeyToATakenCCIs409(t *testing.T) {
	h := newHarness(t)
	a := h.openSession(t, "cc-a")
	_ = h.openSession(t, "cc-b")

	res := h.post(t, "/api/v1/sessions/"+a.ID+"/rekey", map[string]any{"cc_session_id": "cc-b"})
	if res.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 — 훅이 폴백 사유를 구분 못 한다\n%s", res.Code, res.Body.String())
	}
}

func TestRekeyOnAMissingCardIs404(t *testing.T) {
	h := newHarness(t)
	res := h.post(t, "/api/v1/sessions/no-such/rekey", map[string]any{"cc_session_id": "cc-new"})
	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404\n%s", res.Code, res.Body.String())
	}
}

func TestRekeyWithAnEmptyCCIs4xx(t *testing.T) {
	h := newHarness(t)
	card := h.openSession(t, "cc-old")
	res := h.post(t, "/api/v1/sessions/"+card.ID+"/rekey", map[string]any{"cc_session_id": ""})
	if res.Code < 400 || res.Code >= 500 {
		t.Fatalf("status = %d, want 4xx\n%s", res.Code, res.Body.String())
	}
}
```

- [ ] **Step 2: 빨간지 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/api/ -run TestRekey -v
```

Expected: 404(라우트 없음) 또는 컴파일 실패.

- [ ] **Step 3: service 메서드 — `session.go` 의 `SetState` 아래**

```go
// Rekey 는 카드 하나의 cc_session_id 를 갈아끼운다.
//
// ★ 왜 서비스에 판단이 없나. 이 갈래는 "무엇이 옳은가"를 정하지 않는다 — 어느 카드를 어떤 cc 로
// 옮길지는 훅이 계보 대조로 이미 정했고, 여기서 다시 물으면 같은 판단이 두 자리에 산다.
// 서버가 지키는 것은 3중키 무결성뿐이고 그건 UNIQUE 가 한다.
func (s *Service) Rekey(ctx context.Context, sessionID, ccSessionID string) (model.Session, error) {
	out, err := s.st.Rekey(ctx, sessionID, ccSessionID)
	if err != nil {
		s.logFail(ctx, "session.rekey", "", sessionID, err)
		return model.Session{}, err
	}
	return out, nil
}
```

> **구현자에게:** `logFail` 의 시그니처는 `service/service.go:203` 이다. `s.st` 가 스토어 필드 이름인지 `grep -n 'st \+\*store.Store' internal/service/service.go` 로 확인해라.

- [ ] **Step 4: api 핸들러 — `handlers_session.go` 의 `patchSessionRequest` 옆**

```go
type rekeyRequest struct {
	CCSessionID string `json:"cc_session_id"`
}

// handleRekey 는 /clear 로 갈린 대화의 새 cc 를 카드에 반영한다.
//
// ★ 훅만 이걸 부른다. MCP 는 자기 environ 이 안 바뀌므로 새 cc 를 알 길이 없다.
func (s *server) handleRekey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	infoFrom(r.Context()).setSession(id)

	var req rekeyRequest
	if !s.decode(w, r, &req) {
		return
	}
	out, err := s.svc.Rekey(r.Context(), id, req.CCSessionID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.publish(r, "session.rekey", out.Project, out.ID, map[string]any{"cc_session_id": out.CCSessionID})
	s.writeJSON(w, r, http.StatusOK, out)
}
```

> **구현자에게:** `s.publish` 의 정확한 시그니처를 `grep -n 'func (s \*server) publish' -A 4 internal/api/*.go` 로 확인해 인자를 맞춰라. 빈 cc 가 4xx 로 나오는지 확인하고, 만약 스토어의 오류가 500 으로 분류되면 `ClassifyError` 를 고치는 대신 **핸들러에서 `service.RefusedError` 로 감싸** 400 이 되게 한다(`ClassifyError` 의 첫 분기다).

- [ ] **Step 5: 라우트 한 줄 — `api.go` 의 세션 블록**

```go
	mux.HandleFunc("POST /api/v1/sessions/{id}/rekey", s.handleRekey)
```

- [ ] **Step 6: `DESIGN.md` §6 REST 표에 한 줄**

`plugins/flightdeck/DESIGN.md` 의 세션 라우트 목록(`:318-325`)에 기존 줄들과 같은 꼴로 넣는다:

```
| `POST /api/v1/sessions/{id}/rekey` | /clear·compact 로 갈린 대화의 새 cc 를 카드에 반영한다. 훅만 부른다 |
```

> **왜 이 단계가 있나:** `api.go:175` 가 "`routes()` 는 설계 §6 의 REST 표와 1:1"이라고 선언하는데 **그것을 강제하는 시험이 없다.** 안 적으면 조용히 어긋난다. 표의 정확한 열 모양은 그 자리를 읽고 맞춰라.

- [ ] **Step 7: 초록인지 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/api/ ./internal/service/ -v -race
```

Expected: PASS.

- [ ] **Step 8: 커밋**

```bash
cd plugins/flightdeck/server && gofmt -l . && go vet ./... && go test ./... -race
git add plugins/flightdeck/server/internal/ plugins/flightdeck/DESIGN.md
git commit -m "feat(flightdeck): expose rekey as a POST, and put it in the REST table it claims 1:1 with"
```

---

### Task 8: `mcpsrv` — `WithBeaconDir` 와 가드 걸린 심기

**Files:**
- Modify: `plugins/flightdeck/server/internal/mcpsrv/mcpsrv.go` (`builder` `:68-78`, 옵션들 `:81-100`·`:836-849`, `New` `:121`)
- Test: `plugins/flightdeck/server/internal/mcpsrv/beacon_test.go`

**Interfaces:**
- Consumes: Task 4 의 `window.Plant`, Task 3 의 `window.PPidOf`/`StartedOf`.
- Produces: `func WithBeaconDir(dir string) Option`, `func (s *Server) BeaconKey() (window.Key, bool)`.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/mcpsrv/beacon_test.go`:

```go
package mcpsrv

import (
	"os"
	"testing"
)

// ★ 이 시험이 개정 ①(가드)과 시험 격리를 함께 지킨다.
// mcpsrv 에는 cmd/fd 의 TestUnpinnedEnvNeverReachesTheRealHome 같은 방어가 없다.
// WithBeaconDir 이 없을 때 기본 경로로 떨어지면 go test 가 개발자의 진짜
// ~/.flightdeck/windows/ 에 파일을 쓴다.
func TestNoBeaconDirMeansNoWrite(t *testing.T) {
	s := newServerWithoutBeaconDir(t) // 아래 주석 참고
	if _, ok := s.BeaconKey(); ok {
		t.Fatal("비콘 디렉토리를 안 줬는데 심을 좌표가 있다고 한다")
	}
}

func TestPlantsWhenIdentityIsWhole(t *testing.T) {
	dir := t.TempDir()
	s := newServerWithBeacon(t, dir, "cc-1")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(ents) != 1 {
		t.Fatalf("비콘 파일이 %d개다, 1개여야 한다", len(ents))
	}
	_ = s
}

// ★ 설계 개정 ① — Cursor 가 띄운 MCP 는 부모가 claude 가 아니고 CLAUDE_CODE_SESSION_ID 가 없다.
// 거기서 심으면 어떤 훅도 영영 못 맞추는 pid 로 키가 잡힌다.
func TestDoesNotPlantWhenIdentityIsHalf(t *testing.T) {
	dir := t.TempDir()
	_ = newServerWithBeacon(t, dir, "") // cc 없음
	ents, _ := os.ReadDir(dir)
	if len(ents) != 0 {
		t.Fatalf("정체가 반쪽인데 비콘을 %d개 심었다", len(ents))
	}
}
```

> **구현자에게:** `newServerWithBeacon` / `newServerWithoutBeaconDir` 는 이 파일 안에 만들어라. 기존 `newServer`(`internal/mcpsrv/server_test.go:74`)를 먼저 읽고(`grep -n 'func newServer' -A 20 internal/mcpsrv/server_test.go`) 그 꼴을 그대로 따르되 `WithBeaconDir(dir)` 를 더하고, cc 는 `WithEnv` 로 `CLAUDE_CODE_SESSION_ID` 를 주거나 빼서 조절한다.

- [ ] **Step 2: 빨간지 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/mcpsrv/ -run 'TestNoBeaconDir|TestPlants|TestDoesNotPlant' -v
```

Expected: `undefined: WithBeaconDir`.

- [ ] **Step 3: 구현 — `builder` 에 필드, 옵션, `New` 끝에서 심기**

`builder` 구조체(`:68-78`)에 추가:

```go
	beaconDir string
```

옵션(`WithMachine` 옆, `:846` 부근):

```go
// WithBeaconDir 는 창 비콘을 둘 디렉토리를 준다.
//
// ★ **이 옵션이 없으면 심지 않는다.** 여기서 기본 경로로 떨어지면 go test 가 개발자의
// 진짜 ~/.flightdeck/windows/ 에 파일을 쓴다 — cmd/fd 는 그 사고를
// TestUnpinnedEnvNeverReachesTheRealHome 으로 막지만 이 패키지에는 그런 방어가 없다.
// 경로를 고르는 판단은 window.Dir 하나가 갖고, 그것을 부르는 것은 배선(cmd/fd)의 일이다.
func WithBeaconDir(dir string) Option {
	return func(b *builder) { b.beaconDir = dir }
}
```

`New` 안, 정체(`s.id`)가 만들어진 뒤:

```go
	s.beaconDir = b.beaconDir
	s.plantBeacon()
```

그리고 메서드:

```go
// BeaconKey 는 이 프로세스가 심을 창 좌표다. 심을 수 없으면 ok=false 다.
//
// ★ 부모가 claude 라는 보장이 없다. 실측: Cursor 가 띄운 fd mcp 의 부모는 node 이고
// 조상 사슬 어디에도 claude 가 없으며 CLAUDE_* 환경이 하나도 없다. 그 자리에 심으면
// 어떤 훅도 못 맞추는 pid 로 키가 잡히므로, 정체가 온전할 때만 심는다.
func (s *Server) BeaconKey() (window.Key, bool) {
	if s.beaconDir == "" || !s.canAttribute() {
		return window.Key{}, false
	}
	ppid := os.Getppid()
	started, err := window.StartedOf(ppid)
	if err != nil {
		return window.Key{}, false
	}
	k := window.Key{MachineID: s.id.MachineID, ClaudePID: ppid, Started: started}
	return k, k.Valid()
}

// plantBeacon 은 이 창의 자리를 표시한다. 실패해도 서버를 막지 않는다 —
// 비콘이 없으면 훅이 폴백하고, 그 폴백이 오늘 거동이다.
func (s *Server) plantBeacon() {
	k, ok := s.BeaconKey()
	if !ok {
		return
	}
	if _, err := window.Plant(s.beaconDir, k, s.id.Worktree, s.id.CCSessionID, s.now()); err != nil {
		s.log.Warn("창 비콘 심기 실패", "error", err.Error())
	}
}
```

> **구현자에게:** `Server` 구조체(`:53-62`)에 `beaconDir string` 필드를 더해라. `s.log`·`s.now` 의 실제 이름을 `grep -n 'type Server struct' -A 14 internal/mcpsrv/mcpsrv.go` 로 확인해 맞춰라. `os`·`window` 임포트를 추가한다.

- [ ] **Step 4: 초록인지 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/mcpsrv/ -v -race
```

Expected: PASS 전부(기존 시험 포함).

- [ ] **Step 5: 진짜 홈을 안 건드리는지 확인한다**

```bash
cd plugins/flightdeck/server && ls ~/.flightdeck/windows/ 2>/dev/null | wc -l   # 시험 전 개수
go test ./internal/mcpsrv/ ./internal/window/ -count=1
ls ~/.flightdeck/windows/ 2>/dev/null | wc -l                                   # 같아야 한다
```

- [ ] **Step 6: 커밋**

```bash
cd plugins/flightdeck/server && gofmt -l . && go vet ./... && go test ./... -race
git add plugins/flightdeck/server/internal/mcpsrv/
git commit -m "feat(flightdeck): let the MCP mark its window, but only when it knows which window it is"
```

---

### Task 9: `mcpsrv` — `ensureSession` 이 비콘의 cc 를 우선한다

**Files:**
- Modify: `plugins/flightdeck/server/internal/mcpsrv/mcpsrv.go` (`ensureSession` `:436-449`)
- Test: `plugins/flightdeck/server/internal/mcpsrv/beacon_test.go` (덧붙임)

**Interfaces:**
- Consumes: Task 8 의 `BeaconKey`, Task 4 의 `window.Load`.
- Produces: 없음(내부 거동 변경).

**결정적 제약:** `s.id` 는 뮤텍스 없이 여러 곳에서 읽힌다(`callTool`·`toolBoard`·`errText`·`recentNotes`·`tail`). **`s.id.CCSessionID` 를 바꾸면 지금 코드에 없는 경쟁이 생긴다.** 그러니 `s.id` 는 건드리지 말고, `ensureSession` 이 만드는 `service.OpenSessionInput` 의 `CCSessionID` **자리에만** 비콘 값을 끼운다. `Backend` 인터페이스는 그대로다.

- [ ] **Step 1: 실패하는 시험을 쓴다 (`beacon_test.go` 에 덧붙임)**

```go
// ★ 이것이 이 기능의 본체다. 훅이 비콘에 새 cc 를 적어 두면,
// 옛 cc 를 든 MCP 프로세스가 카드를 열 때 **새 cc 로 연다** — 그래서 카드가 한 장이 된다.
func TestEnsureSessionPrefersTheBeaconCC(t *testing.T) {
	dir := t.TempDir()
	s := newServerWithBeacon(t, dir, "cc-stale") // 프로세스의 env cc 는 낡았다
	k, ok := s.BeaconKey()
	if !ok {
		t.Fatal("비콘 좌표가 없다")
	}
	if _, err := window.SaveIdentity(dir, k, "cc-fresh", "card-A", time.Unix(0, 0)); err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}

	// 도구를 한 번 부른다 — ensureSession 은 게을러서 그 전에는 안 돈다.
	callBoardOnce(t, s)

	if got := lastOpenSessionCC(t, s); got != "cc-fresh" {
		t.Fatalf("MCP 가 %q 로 카드를 열었다, 비콘의 %q 여야 한다", got, "cc-fresh")
	}
}

// ★ 비콘이 없으면 오늘 거동이다 — 자기 env cc 로 연다. 새 실패 모드를 만들지 않는다.
func TestEnsureSessionFallsBackToItsOwnCC(t *testing.T) {
	dir := t.TempDir()
	s := newServerWithBeacon(t, dir, "cc-own")
	os.RemoveAll(dir) // 비콘을 없앤다
	callBoardOnce(t, s)
	if got := lastOpenSessionCC(t, s); got != "cc-own" {
		t.Fatalf("폴백이 %q 로 열었다, %q 여야 한다", got, "cc-own")
	}
}
```

> **구현자에게:** `callBoardOnce` 와 `lastOpenSessionCC` 는 이 파일 안에 만들어라. 기존 시험이 가짜 백엔드로 `OpenSession` 인자를 잡아내는 방식이 이미 있다 — `grep -n 'OpenSession' internal/mcpsrv/*_test.go | head` 로 찾아 그 꼴을 그대로 쓴다. 도구 호출은 기존 시험이 `callTool` 이나 JSON-RPC 로 하는 방식을 따른다.

- [ ] **Step 2: 빨간지 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/mcpsrv/ -run TestEnsureSession -v
```

Expected: FAIL — `cc-stale` 로 열었다.

- [ ] **Step 3: 구현 — `ensureSession` 의 입력 조립부만 고친다**

```go
func (s *Server) ensureSession(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessionID != "" {
		return s.sessionID, nil
	}

	// ★ 비콘이 있으면 **cc 만** 그 값을 쓴다.
	//
	// 왜 cc 만인가. 비콘의 session_id 로 카드를 직접 잡으려면 Backend.OpenSession 을 우회해야 하고,
	// 그러면 닫아 둔 인터페이스(backend.go 머리)가 열려 컴파일로 고정된 serialProbe 를 포함해
	// 네 자리가 따라 바뀐다. 그럴 필요가 없다 — 3중키가 같으면 서버가 알아서 같은 카드를 준다.
	//
	// 왜 s.id 를 안 고치는가. s.id 는 뮤텍스 없이 여러 곳에서 읽힌다(callTool·toolBoard·tail…).
	// 그 필드를 가변으로 만들면 지금 코드에 없는 경쟁이 생긴다. 여기 지역 변수로만 쓴다.
	cc := s.id.CCSessionID
	if k, ok := s.BeaconKey(); ok {
		if b, err := window.Load(s.beaconDir, k); err == nil && strings.TrimSpace(b.CCSessionID) != "" {
			cc = b.CCSessionID
		}
	}

	res, err := s.be.OpenSession(ctx, service.OpenSessionInput{
		Project:     s.id.ProjectID,
		ProjectPath: s.id.ProjectPath,
		MachineID:   s.id.MachineID,
		Hostname:    s.id.Hostname,
		Worktree:    s.id.Worktree,
		CCSessionID: cc,
		// … 나머지 필드는 기존 그대로 둔다
	})
	// … 이하 기존 코드 그대로
}
```

> **구현자에게:** `:442-449` 의 기존 필드 목록을 **그대로 두고** `CCSessionID` 한 줄만 `cc` 로 바꿔라. 필드를 다시 타이핑하지 말고 원본을 읽어서 최소 편집한다.

- [ ] **Step 4: 초록인지 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/mcpsrv/ -v -race
```

Expected: PASS 전부.

- [ ] **Step 5: 커밋**

```bash
cd plugins/flightdeck/server && gofmt -l . && go vet ./... && go test ./... -race
git add plugins/flightdeck/server/internal/mcpsrv/
git commit -m "feat(flightdeck): open the card with the cc the hook last saw, not the one exec froze in"
```

---

### Task 10: `RenderDrift` 가 사유를 받는다 (지금 초록인 단정 둘을 다시 쓴다)

**Files:**
- Modify: `plugins/flightdeck/server/internal/mcpsrv/drift.go` (`RenderDrift` `:62-76`)
- Modify: `plugins/flightdeck/server/internal/mcpsrv/drift_test.go` (`:112-131`, `:182`)
- Modify: `RenderDrift` 호출부(`toolBoard` 안 — `grep -n 'RenderDrift' internal/mcpsrv/*.go`)

**Interfaces:**
- Consumes: 없음.
- Produces: `func RenderDrift(twins []CoordinateTwin, mineCC, why string) string`.

⚠ **이 태스크는 지금 초록인 단정 둘을 일부러 깬다.** `TestRenderDriftNamesTheAxisAndWhatToDo`(`drift_test.go:112-131`)가 `기동`·`재기동` 을, `TestBoardShowsCCDriftInTheResponse`(`:182`)가 `재기동` 을 요구한다. 그 문구는 **"고칠 길이 재기동뿐"이라는 옛 사실**을 굳혀 둔 것이고 이 작업이 그 사실을 바꾼다. 시험을 초록으로 되돌리려고 옛 문구를 복원하면 **틀린 조언이 되살아난다.**

- [ ] **Step 1: 단정을 새 사실로 다시 쓴다**

`drift_test.go` 의 두 시험에서 `재기동` 요구를 지우고, 대신 **사유가 화면에 나오는지**를 단정한다:

```go
func TestRenderDriftNamesTheAxisAndWhyRepairDidNotHappen(t *testing.T) {
	twins := []CoordinateTwin{{SessionID: "s-2", CCSessionID: "cc-2"}}
	got := RenderDrift(twins, "cc-1", "조상 사슬 어디에도 이 머신의 비콘이 없다")

	for _, want := range []string{"cc-1", "cc-2", "s-2", "조상 사슬"} {
		if !strings.Contains(got, want) {
			t.Fatalf("표류 문구에 %q 가 없다:\n%s", want, got)
		}
	}
	// ★ 옛 조언("재기동해라")은 이제 틀렸다 — 훅이 다음 SessionStart 에 고친다.
	if strings.Contains(got, "재기동") {
		t.Fatalf("고쳐진 뒤에도 재기동을 권한다:\n%s", got)
	}
}

func TestRenderDriftIsEmptyWithoutDrift(t *testing.T) {
	if got := RenderDrift(nil, "cc-1", ""); got != "" {
		t.Fatalf("표류가 없는데 문구가 나왔다: %q", got)
	}
}
```

`TestBoardShowsCCDriftInTheResponse`(`:182`)의 `재기동` 단정도 같은 이유로 바꾼다 — 보드 본문에 **갈린 cc 두 값**이 뜨는지로 단정하면 렌더 문구 변화에 안 깨진다.

- [ ] **Step 2: 빨간지 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/mcpsrv/ -run TestRenderDrift -v
```

Expected: 인자 개수 불일치로 컴파일 실패.

- [ ] **Step 3: 구현**

`drift.go` 의 `RenderDrift` 를 고친다:

```go
// RenderDrift 는 표류 하나를 사람이 읽는 문구로 만든다. 순수 함수다.
//
// 표류가 없으면 **빈 문자열**이다 — 매 board 마다 빈 절이 붙으면 예산이 토큰인 화면이 상한다.
//
// ★ why 는 **수리가 왜 안 됐나**다. 이제 표류는 훅이 비콘으로 고친다(설계 §3).
// 그래도 여기 문구가 뜬다면 그 수리가 어딘가에서 멈춘 것이고, 그 자리를 이름으로 말하지 않으면
// 사람이 원인에 도달할 길이 없다 — 이 기능을 만들려고 /proc 을 뒤져야 했던 것이 그 증거다.
func RenderDrift(twins []CoordinateTwin, mineCC, why string) string {
	if len(twins) == 0 {
		return ""
	}
	s := "⚠ 이 워크트리에 cc_session_id 가 갈린 세션이 " +
		itoa(len(twins)) + "건 더 있다 — 카드가 여러 장인 이유가 이것이다.\n" +
		"  이 MCP 프로세스가 든 값: " + clip(mineCC, 64) + " (기동 시 주입된 뒤 안 바뀐다)\n"
	for _, t := range twins {
		s += "  갈린 카드: " + clip(t.SessionID, 64) + " · cc=" + clip(t.CCSessionID, 64) + "\n"
	}
	s += "  훅이 다음 SessionStart 에 이것을 합친다."
	if strings.TrimSpace(why) != "" {
		s += " 이번에 못 합친 사유: " + clip(why, 200)
	}
	return s
}
```

호출부(`toolBoard`)는 `Find` 가 낸 사유를 넘긴다. MCP 는 훅이 아니라 사유를 직접 못 만드므로, **비콘을 못 읽은 사유**를 그 자리에서 만들어 넘긴다(비콘 없음 / 좌표 없음 / 읽기 실패).

- [ ] **Step 4: 초록인지 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/mcpsrv/ -v -race
```

- [ ] **Step 5: 커밋**

```bash
cd plugins/flightdeck/server && gofmt -l . && go vet ./... && go test ./... -race
git add plugins/flightdeck/server/internal/mcpsrv/
git commit -m "fix(flightdeck): stop telling people to restart, and say where the repair stopped instead"
```

---

### Task 11: `cmd/fd` — 비콘 자리 위임과 rekey 클라이언트 갈래

**Files:**
- Modify: `plugins/flightdeck/server/cmd/fd/env.go`
- Modify: `plugins/flightdeck/server/cmd/fd/app.go`
- Test: `plugins/flightdeck/server/cmd/fd/beacon_wiring_test.go`

**Interfaces:**
- Consumes: Task 2 의 `window.Dir`, Task 7 의 라우트.
- Produces: `func BeaconDir(get func(string) (string, bool), home string) (string, string)`, `func (a *App) Rekey(ctx context.Context, sessionID, cc string) (model.Session, error)`.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`cmd/fd/beacon_wiring_test.go`:

```go
package main

import (
	"testing"

	"github.com/kweiza/flightdeck/internal/window"
)

// ★ 사다리의 주인은 하나다. cmd/fd 가 자기 판본을 갖게 두면
// client.go:110-114 가 "같은 판단이 두 자리에 살아 세 번 데였다"고 적어 둔 그 실수가 반복된다.
func TestBeaconDirDelegatesToTheOneOwner(t *testing.T) {
	env := envOf(map[string]string{"FD_STATE_DIR": "/pin"})
	got, _ := BeaconDir(env, "/home/u")
	want, _ := window.Dir(env, "/home/u")
	if got != want {
		t.Fatalf("BeaconDir = %q, window.Dir = %q — 사다리가 두 벌이다", got, want)
	}
}
```

`(*App).Rekey` 시험은 기존 하니스(가짜 서버)를 쓴다:

```go
func TestRekeyPostsAndDoesNotQueueOffline(t *testing.T) {
	// ★ rekey 는 a.cli.do 로 보낸다. a.cli.Write 로 보내면 JudgeOffline 과
	// IdempotencyStable 의 default 가 "정책이 정의되어 있지 않다"로 거절해
	// 서버가 안 닿을 때마다 실패한다. 그리고 rekey 는 아웃박스에 쌓을 것이 아니다 —
	// 다음 SessionStart 훅이 어차피 다시 시도한다.
	//
	// 구현자: 기존 하니스로 가짜 서버를 세우고 (a) 경로가
	// /api/v1/sessions/<id>/rekey 인지 (b) 서버 불통일 때 아웃박스가 안 늘어나는지 단정한다.
	// grep -n 'func newHarness\|func (h \*harness)' cmd/fd/harness_test.go 로 헬퍼를 찾아라.
	t.Skip("구현자가 하니스 헬퍼 이름을 확인한 뒤 채운다 — Step 3 전에 반드시 채울 것")
}
```

> **구현자에게:** 위 `t.Skip` 은 **자리표시가 아니라 지시다.** Step 3 을 시작하기 전에 하니스 헬퍼 이름을 확인하고 시험 본문을 채워라. 채우지 않은 채 다음 태스크로 넘어가면 안 된다.

- [ ] **Step 2: 빨간지 확인한다**

```bash
cd plugins/flightdeck/server && go test ./cmd/fd/ -run 'TestBeaconDir|TestRekeyPosts' -v
```

- [ ] **Step 3: 구현 — `env.go` 에 위임 한 갈래**

```go
// BeaconDir 는 창 비콘 디렉토리다.
//
// ★ 판단은 window.Dir 하나가 갖는다. 여기 사본을 만들면 훅과 MCP 가 서로 다른 자리를 보게 되고,
// 그 어긋남은 어느 화면에도 안 뜬다 — machine-id 가 두 벌이 됐던 사고와 같은 꼴이다.
func BeaconDir(get func(string) (string, bool), home string) (string, string) {
	return window.Dir(get, home)
}
```

`app.go` 에 rekey 갈래:

```go
// Rekey 는 /clear 로 갈린 대화의 새 cc 를 카드에 반영한다.
//
// ★ a.cli.do 를 쓴다 — a.cli.Write 가 아니다. Write 는 JudgeOffline·IdempotencyStable 을 거치는데
// 둘 다 모르는 cmd 를 "정책이 정의되어 있지 않다"로 거절하므로 서버가 안 닿을 때마다 실패한다.
// 그리고 rekey 는 오프라인 큐에 쌓을 일이 아니다 — 다음 SessionStart 훅이 어차피 다시 시도하고,
// 그때는 그 시점의 cc 가 맞다. 낡은 rekey 를 나중에 재생하면 오히려 틀린다.
func (a *App) Rekey(ctx context.Context, sessionID, cc string) (model.Session, error) {
	raw, _, err := a.cli.do(ctx, "POST", "/api/v1/sessions/"+sessionID+"/rekey",
		map[string]string{"cc_session_id": cc}, FreshKey(a.cli.Session))
	if err != nil {
		return model.Session{}, err
	}
	var out model.Session
	if uerr := json.Unmarshal(raw, &out); uerr != nil {
		return model.Session{}, fmt.Errorf("rekey 응답 해석 실패: %w", uerr)
	}
	return out, nil
}
```

> **구현자에게:** `a.cli.do` 의 정확한 시그니처를 `grep -n 'func (c \*Client) do' -A 6 cmd/fd/client.go` 로, `FreshKey` 를 `grep -n 'func FreshKey' -A 4 cmd/fd/*.go` 로 확인해 맞춰라. `sessionID` 를 경로에 넣기 전에 URL 이스케이프가 필요한지 기존 경로 조립 코드를 보고 판단해라.

- [ ] **Step 4: 초록인지 확인한다**

```bash
cd plugins/flightdeck/server && go test ./cmd/fd/ -v -race
```

- [ ] **Step 5: 커밋**

```bash
cd plugins/flightdeck/server && gofmt -l . && go vet ./... && go test ./... -race
git add plugins/flightdeck/server/cmd/fd/
git commit -m "feat(flightdeck): send rekey down the path that does not need an offline policy"
```

---

### Task 12: 훅 배선 — 계보 → 비콘 → rekey → OpenSession

**Files:**
- Modify: `plugins/flightdeck/server/cmd/fd/hook.go` (`hookSessionStart` `:140-194`)
- Modify: `plugins/flightdeck/server/cmd/fd/app.go` (세션 캐시 키 이전)
- Test: `plugins/flightdeck/server/cmd/fd/hook_beacon_test.go`

**Interfaces:**
- Consumes: Task 5 의 `window.Find`/`Prune`, Task 11 의 `BeaconDir`·`(*App).Rekey`, Task 4 의 `window.SaveIdentity`.
- Produces: 없음(훅 거동).

**순서가 전부다:** `rekey` 를 **`OpenSession` 앞에** 둔다. 뒤에 두면 훅의 upsert 가 새 cc 로 카드 B 를 먼저 만들고, 그러면 rekey 가 UNIQUE 에 걸려 두 카드를 진짜로 병합하는 경로가 또 필요해진다.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`cmd/fd/hook_beacon_test.go`:

```go
package main

import "testing"

// ★ 이 기능의 인수 시험이다. /clear 를 훅 페이로드의 session_id 를 바꿔 흉내낸다.
//
// 단정은 **서비스를 직접 쳐서** 한다 — 렌더된 배너 문자열로 세면
// 판정기가 도는 순간 전제도 함께 통과하는 순환 전제가 된다(직전 작업이 여기서 걸렸다).
func TestClearKeepsOneCardAndItsClaim(t *testing.T) {
	// 1. 비콘 디렉토리를 t.TempDir() 로 고정하고 MCP 심기를 흉내낸다
	//    (window.Plant 로 {claude pid = 이 시험 프로세스의 어떤 조상, cc = cc-old}).
	// 2. SessionStart 훅을 cc-old 로 한 번 돌린다 → 카드 A 가 생기고 비콘에 session_id 가 적힌다.
	// 3. 카드 A 로 항목 하나를 선점한다.
	// 4. SessionStart 훅을 cc-new 로 다시 돌린다(/clear).
	// 5. 단정: 이 (machine, worktree) 의 카드가 **한 장**이다.
	// 6. 단정: 그 카드의 cc 가 cc-new 다.
	// 7. 단정: 선점이 그대로 그 카드에 붙어 있다.
	t.Skip("구현자가 하니스 헬퍼 이름을 확인한 뒤 채운다 — Step 3 전에 반드시 채울 것")
}

func TestNoBeaconMeansTodaysBehaviour(t *testing.T) {
	// 비콘 없이 훅을 cc-old → cc-new 로 돌리면 카드가 두 장이다(오늘 거동).
	// 새 실패 모드가 없다는 것을 고정한다.
	t.Skip("구현자가 채운다 — Step 3 전에 반드시 채울 것")
}
```

> **구현자에게:** 두 `t.Skip` 은 **지시다.** `grep -n 'func newHarness\|hookSessionStart\|runHook' cmd/fd/harness_test.go cmd/fd/hook_test.go` 로 기존 훅 시험이 어떻게 서고 어떻게 카드를 세는지 먼저 읽고, 위 주석의 7단계를 그대로 코드로 옮겨라. 조상 pid 는 `os.Getpid()` 자신을 써도 된다 — `Find` 는 조상 사슬에 자기 자신을 포함한다.

- [ ] **Step 2: 빨간지 확인한다**

```bash
cd plugins/flightdeck/server && go test ./cmd/fd/ -run 'TestClearKeeps|TestNoBeacon' -v
```

- [ ] **Step 3: 구현 — `hookSessionStart` 안, `cc` 를 얻은 직후·`OpenSession` 앞**

```go
	cc := a.ccSessionID(p.SessionID)
	if cc == "" {
		// … 기존 코드 그대로
	}

	// ★ 표류 수리. 순서가 전부다 — rekey 가 OpenSession 보다 **앞**이어야 한다.
	// 뒤에 두면 아래 upsert 가 새 cc 로 카드 B 를 먼저 만들고, 그러면 rekey 가 UNIQUE 에 걸려
	// 두 카드를 진짜로 병합하는 경로가 또 필요해진다. 앞에 두면 그 upsert 가 이미 고쳐진
	// 카드 A 로 그냥 떨어진다.
	beaconKey, beacon, haveBeacon := a.findWindow()
	if haveBeacon && beacon.SessionID != "" && beacon.CCSessionID != cc {
		if _, err := a.Rekey(ctx, beacon.SessionID, cc); err != nil {
			// ★ 삼키고 알린다. 409 는 ErrUnreachable 이 아니라 *APIError 로 오므로
			// 기존 열화 경로가 이걸 안 잡아 준다 — OpenSession 실패를 다루는 아래 꼴과 같이 간다.
			a.log.Warn("세션 rekey 실패", "error", err.Error())
			in.Notice = strings.TrimSpace(in.Notice + " cc 가 갈렸는데 카드를 못 합쳤다: " + clip(err.Error(), 200))
		} else {
			a.moveSessionCache(beacon.CCSessionID, cc)
		}
	}

	res, stale, err := a.OpenSession(ctx, cc, "")
	// … 기존 코드 그대로

	// 비콘에 이번 cc 와 카드를 적어 둔다. 다음 전환에서 이 값이 rekey 대상이 된다.
	if haveBeacon && in.SessionID != "" {
		if _, werr := window.SaveIdentity(a.beaconDir, beaconKey, cc, in.SessionID, a.now()); werr != nil {
			a.log.Warn("창 비콘 갱신 실패", "error", werr.Error())
		}
	}
	a.pruneWindows()
```

그리고 도우미 셋:

```go
// findWindow 는 내 조상 사슬 위의 비콘을 찾는다. 못 찾아도 오류가 아니다 — §5 폴백이다.
func (a *App) findWindow() (window.Key, window.Beacon, bool) {
	if a.beaconDir == "" {
		return window.Key{}, window.Beacon{}, false
	}
	anc := window.Ancestors(os.Getpid(), window.PPidOf, 24)
	m, ok, why := window.Find(a.beaconDir, a.machine, anc, window.StartedOf)
	if !ok {
		a.log.Debug("창 비콘을 못 찾았다", "why", why)
		return window.Key{}, window.Beacon{}, false
	}
	return m.Key, m.Beacon, true
}

// moveSessionCache 는 세션 캐시를 새 cc 키로 옮긴다.
//
// ★ openSession 은 응답을 "/local/session/<cc>" 에 캐시한다. cc 가 바뀌면 오프라인 읽기가
// 빗나가 "이 세션의 캐시도 없다"가 된다 — 서버가 안 닿는 순간에만 드러나는 결함이라
// 안 옮기면 아무도 못 찾는다.
func (a *App) moveSessionCache(oldCC, newCC string) {
	if oldCC == "" || newCC == "" || oldCC == newCC {
		return
	}
	ent, err := a.cli.Cache.Get(sessionCachePath + "/" + oldCC)
	if err != nil {
		return
	}
	if err := a.cli.Cache.Put(sessionCachePath+"/"+newCC, ent.Body, a.now()); err != nil {
		a.log.Warn("세션 캐시 키 이전 실패", "error", err.Error())
	}
}

// pruneWindows 는 죽은 창의 비콘을 치운다. 훅에서만 한다 — SessionStart 타임아웃이 10초라
// 여유가 있는 쪽이고, MCP 는 도구 응답 지연에 민감하다.
func (a *App) pruneWindows() {
	if a.beaconDir == "" {
		return
	}
	if _, err := window.Prune(a.beaconDir, processAlive); err != nil {
		a.log.Debug("비콘 가지치기 실패", "error", err.Error())
	}
}
```

> **구현자에게:** `a.beaconDir` 를 `App` 에 더하고 `newApp`(`app.go:34` 부근, `ResolveStateDir` 을 부르는 자리)에서 `BeaconDir(env, home)` 로 채워라. `processAlive(pid int) bool` 은 `internal/window` 에 두는 편이 낫다 — `syscall.Kill(pid, 0)` 이 `ESRCH` 가 아니면 살아 있다. 플랫폼 분기가 필요하면 Task 3 의 `proc_linux.go`/`proc_other.go` 짝에 넣어라. `a.log.Debug` 가 있는지 확인하고 없으면 `Warn` 을 쓴다.

- [ ] **Step 4: 초록인지 확인한다**

```bash
cd plugins/flightdeck/server && go test ./cmd/fd/ -v -race
```

- [ ] **Step 5: 커밋**

```bash
cd plugins/flightdeck/server && gofmt -l . && go vet ./... && go test ./... -race
git add plugins/flightdeck/server/cmd/fd/
git commit -m "feat(flightdeck): repair the card before opening it, so the upsert lands on the same one"
```

---

### Task 13: 전체 검증과 실물 확인

**Files:** 없음(검증 전용).

- [ ] **Step 1: 전 패키지 검증**

```bash
cd plugins/flightdeck/server
gofmt -l .            # 빈 출력이어야 한다
go vet ./...
go test ./... -race
GOOS=darwin go build ./...
```

- [ ] **Step 2: 시험이 진짜 홈을 안 건드렸는지 확인**

```bash
ls ~/.flightdeck/windows/ 2>/dev/null | wc -l
cd plugins/flightdeck/server && go test ./... -count=1 >/dev/null
ls ~/.flightdeck/windows/ 2>/dev/null | wc -l   # 같아야 한다
```

- [ ] **Step 3: 스펙 대조**

`docs/superpowers/specs/2026-08-05-cc-drift-hook-channel-design.md` 를 열고 개정 ①~⑤ 각각에 대응하는 회귀 시험이 실제로 있는지 확인한다:

| 개정 | 시험 |
|---|---|
| ① 심기 가드 | `TestDoesNotPlantWhenIdentityIsHalf` · `TestPlantRefusesAnIncompleteKey` |
| ② 병합 심기 | `TestPlantNeverOverwritesTheHooksIdentity` |
| ③ 계보 필수 | `TestFindRefusesABeaconThatIsNotAnAncestor` |
| ④ 사다리 주인 하나 | `TestBeaconDirDelegatesToTheOneOwner` |
| ⑤ cc 만 우선 | `TestEnsureSessionPrefersTheBeaconCC` · `Backend` 인터페이스 무변경 |

빠진 것이 있으면 그 자리에서 시험을 더한다.

- [ ] **Step 4: 커밋(있으면)하고 판단을 남긴다**

```bash
git add -A && git commit -m "test(flightdeck): cover the five premises measurement broke"   # 변경이 있을 때만
```

그리고 `note(kind: "verified")` 로 무엇을 실제로 돌려 확인했는지 남긴다 — 명령과 결과를 적는다. 초록이라는 주장만 남기지 않는다.

---

## 자기 점검 (계획 작성자가 이미 수행)

**스펙 대조:** §1 자리 → Task 2·11 · §2 내용/플랫폼 → Task 1·3 · §3 심기·계보·cc우선 → Task 4·5·8·9 · §4 서버 → Task 6·7 · §5 실패·RenderDrift → Task 10·11·12 · 정리/동시성 → Task 5·12 · DESIGN §6 → Task 7.

**알려진 빈칸(의도적):** Task 11·12 의 세 시험 본문은 `t.Skip` 과 함께 **단계별 지시**로 남겼다. 이 저장소의 하니스 헬퍼 이름을 계획 작성 시점에 확정할 수 없었고, 틀린 이름을 코드로 박으면 구현자가 그것을 고치느라 시험 의도까지 바꾸기 때문이다. 각 자리에 무엇을 단정해야 하는지는 문장으로 완전히 적었고, Step 3 전에 채우라는 지시를 붙였다.

**타입 일관성:** `window.Key`·`window.Beacon`·`window.Match` 는 Task 1·5 에서 정의되어 Task 8·9·12 에서 그대로 쓰인다. `RenderDrift` 는 Task 10 에서 인자가 셋이 되고 그 뒤로 안 바뀐다. `(*App).Rekey` 는 Task 11 에서 정의되어 Task 12 에서만 불린다.
