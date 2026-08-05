# 서버 자기 재기동 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `fd serve` 가 자기 실행 파일이 교체된 것을 감지해, 검증에 통과하면 스스로 `syscall.Exec` 로 재기동한다. 그 결과 `plugin update` 만으로 서버 코드와 DB 스키마가 따라온다.

**Architecture:** 서버는 빌드를 모른다 — `bin/fd` 런처가 이미 빌드한다. 서버는 `os.Executable()` 자리의 `dev·inode·size·mtime` 이 달라졌는지만 본다. 달라졌으면 그 파일을 자식 프로세스로 한 번 돌려(`fd selfcheck`) 실행 가능성과 DB 증분 계획을 확인하고, 통과할 때만 드레인 후 `syscall.Exec` 한다. 판정은 전부 순수 함수로 빼고 `exec` 는 주입받아 시험이 프로세스를 안 죽이고 단언한다.

**Tech Stack:** Go 1.25 · `modernc.org/sqlite`(순수 Go, CGO 0) · 표준 라이브러리만. 새 의존성 0.

## Global Constraints

- **설계 문서:** `docs/superpowers/specs/2026-08-05-fd-server-self-restart-design.md` 가 정본이다. 충돌하면 스펙이 이긴다.
- **큐 항목:** `fd-server-self-restart` · 브랜치 `fd-server-self-restart` (이미 선점됨).
- **새 의존성 0.** `go.mod` 를 건드리지 않는다.
- **`CGO_ENABLED=0` 에서 빌드된다.** `syscall` 은 쓰되 cgo 는 안 쓴다.
- **MCP 도구 수는 6개 그대로.** `selfcheck` 는 CLI 서브명령이다(DESIGN §1 원칙 ②).
- **주석은 한국어**, 기존 파일의 밀도와 어조를 따른다. `★` 는 "안 그러면 조용히 망가지는 자리"에만 쓴다.
- **DESIGN.md 를 건드리지 않는다.** 지금 6개 세션이 그 파일을 협상 중이다. 계약 반영은 별도 항목으로 낸다(마지막 절 참조).
- **커밋 메시지는 한국어**, `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>` 로 끝낸다.
- **매 태스크 끝에서:** `gofmt -l ./cmd ./internal` 무출력 · `go vet ./...` 무출력 · `go test ./...` 초록.

## 파일 구조

| 파일 | 책임 | 태스크 |
|---|---|---|
| `internal/store/probe.go` | DB 를 **읽기만** 해서 `MigrationPlan` 을 낸다. 적용하지 않는다 | 1 |
| `internal/store/probe_test.go` | 상태별 DB 로 계획을 단언 | 1 |
| `cmd/fd/selfwatch.go` | **태그 없음** — `ExeID` · `Same` · `Action` · `Decide` · 감시기 본체 | 2, 4 |
| `cmd/fd/selfwatch_unix.go` | `//go:build unix` — `exeIDOfPath` · `selfWatchSupported` · `execSelf` | 2, 4 |
| `cmd/fd/selfwatch_other.go` | `//go:build !unix` — 같은 셋을 오류로 | 2, 4 |
| `cmd/fd/selfwatch_test.go` | 순수 함수 표 구동 + 루프 시험(스텁 주입) | 2, 4 |
| `cmd/fd/selfcheck.go` | `fd selfcheck --db` 서브명령 | 3 |
| `cmd/fd/selfcheck_test.go` | 종료코드 갈래 | 3 |
| `cmd/fd/main.go` | 서브명령 표에 `selfcheck` 한 줄 | 3 |
| `cmd/fd/serve.go` | 감시기 기동·배선 | 4 |
| `internal/api/handlers_meta.go` | `SelfUpdateStatus` 타입 + `/healthz` 필드 | 5 |
| `internal/api/api.go` | `Options.SelfUpdate` 콜백 | 5 |
| `cmd/fd/client.go` | `healthzResponse` 에 필드 | 5 |
| `cmd/fd/render.go` | `fd doctor` 서버 절의 자동 갱신 축 | 6 |

---

### Task 1: `ProbeMigration` — 적용하지 않고 계획만 읽는다

**Files:**
- Create: `plugins/flightdeck/server/internal/store/probe.go`
- Test: `plugins/flightdeck/server/internal/store/probe_test.go`

**Interfaces:**
- Consumes: 기존 `PlanMigration(hasVersionTable bool, dbVersion, objectCount, codeVersion int) MigrationPlan` · `SchemaVersion` · 비공개 `dsn(path string) string` (같은 패키지라 쓸 수 있다)
- Produces: `func ProbeMigration(ctx context.Context, path string) (MigrationPlan, error)`

**왜 필요한가:** `store.Open` 은 **열면서 적용한다.** 검증이 증분을 붙여 버리면, 그 뒤 `exec` 가 실패했을 때 옛 프로세스가 새 스키마 위에서 돌게 된다.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/store/probe_test.go`:

```go
package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// probeDB 는 시험용 DB 를 만든다. sqls 를 순서대로 실행한다(비면 파일만 만든다).
func probeDB(t *testing.T, sqls ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fd.db")
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("열기 실패: %v", err)
	}
	defer db.Close()
	for _, q := range sqls {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("준비 SQL 실패(%q): %v", q, err)
		}
	}
	if len(sqls) == 0 {
		// 파일만 만들고 객체는 0으로 둔다
		if _, err := db.Exec(`SELECT 1`); err != nil {
			t.Fatalf("빈 DB 준비 실패: %v", err)
		}
	}
	return path
}

func TestProbeMigrationEmptyDBPlansApply(t *testing.T) {
	plan, err := ProbeMigration(context.Background(), probeDB(t))
	if err != nil {
		t.Fatalf("probe 실패: %v", err)
	}
	if plan.Action != MigrateApply {
		t.Fatalf("빈 DB 인데 %q 다 — 사유 %q", plan.Action, plan.Reason)
	}
}

// ★ 이 갈래가 selfcheck 의 존재 이유다 — 강등하면 여기서 걸린다.
func TestProbeMigrationRejectsWhenDBIsAheadOfCode(t *testing.T) {
	path := probeDB(t,
		`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL, applied_at TEXT NOT NULL)`,
		`INSERT INTO schema_version VALUES (9999, '2026-01-01T00:00:00Z')`,
		`CREATE TABLE filler (a TEXT)`,
	)
	plan, err := ProbeMigration(context.Background(), path)
	if err != nil {
		t.Fatalf("probe 실패: %v", err)
	}
	if plan.Action != MigrateReject {
		t.Fatalf("DB 가 코드보다 앞선데 %q 다 — 사유 %q", plan.Action, plan.Reason)
	}
}

func TestProbeMigrationRejectsForeignDB(t *testing.T) {
	path := probeDB(t, `CREATE TABLE somebody_elses (a TEXT)`)
	plan, err := ProbeMigration(context.Background(), path)
	if err != nil {
		t.Fatalf("probe 실패: %v", err)
	}
	if plan.Action != MigrateReject {
		t.Fatalf("남의 DB 인데 %q 다", plan.Action)
	}
}

// ★ 없는 파일에 probe 하면 **만들면 안 된다.** sql.Open 은 파일을 만든다.
func TestProbeMigrationDoesNotCreateMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.db")
	if _, err := ProbeMigration(context.Background(), path); err == nil {
		t.Fatal("없는 파일인데 오류가 없다")
	} else if !strings.Contains(err.Error(), "없다") {
		t.Fatalf("사유가 부재를 안 말한다: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("probe 가 DB 파일을 만들었다 — 읽기 전용이어야 한다")
	}
}
```

import 은 `context` · `database/sql` · `os` · `path/filepath` · `strings` · `testing` 이다.

- [ ] **Step 2: 시험이 실패하는 것을 확인한다**

```
cd plugins/flightdeck/server
go test ./internal/store/ -run TestProbeMigration -v
```

기대: `undefined: ProbeMigration` 로 컴파일 실패.

- [ ] **Step 3: 최소 구현**

`internal/store/probe.go`:

```go
package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
)

// ProbeMigration 은 DB 를 **읽기만 해서** 이 바이너리가 그것을 어떻게 다룰지를 낸다.
//
// ★ store.Open 과 다른 점이 전부다: **적용하지 않는다.** 검증 도중 증분이 붙어 버리면,
// 그 뒤 재기동이 실패했을 때 옛 프로세스가 새 스키마 위에서 돌게 된다 —
// 조용히 망가지는 경로이고, 그때는 되돌릴 자리도 없다.
//
// 없는 파일에는 오류를 낸다. sql.Open 은 파일을 **만들기** 때문에, 부재를 확인 안 하고
// 열면 검증이 빈 DB 를 하나 만들어 놓고 "빈 DB 다"라고 답한다.
func ProbeMigration(ctx context.Context, path string) (MigrationPlan, error) {
	if _, err := os.Stat(path); err != nil {
		return MigrationPlan{}, fmt.Errorf("DB 파일이 없다(path=%q): %w", path, err)
	}
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return MigrationPlan{}, fmt.Errorf("sqlite 열기 실패(path=%q): %w", path, err)
	}
	defer db.Close()

	var hasTable int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schema_version'`,
	).Scan(&hasTable); err != nil {
		return MigrationPlan{}, fmt.Errorf("schema_version 존재 확인 실패: %w", err)
	}

	var dbVersion int
	if hasTable > 0 {
		var v sql.NullInt64
		if err := db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_version`).Scan(&v); err != nil {
			return MigrationPlan{}, fmt.Errorf("schema_version 읽기 실패: %w", err)
		}
		if v.Valid {
			dbVersion = int(v.Int64)
		}
	}

	var objects int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type IN ('table','index','trigger','view')`,
	).Scan(&objects); err != nil {
		return MigrationPlan{}, fmt.Errorf("sqlite_master 읽기 실패: %w", err)
	}

	// 판정은 migrate 와 **같은 순수 함수**를 쓴다. 여기서 다시 판정하면 두 벌이 된다.
	return PlanMigration(hasTable > 0, dbVersion, objects, SchemaVersion), nil
}
```

- [ ] **Step 4: 시험이 통과하는 것을 확인한다**

```
go test ./internal/store/ -run TestProbeMigration -v
```

기대: 4건 전부 PASS.

- [ ] **Step 5: 커밋**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-server-self-restart
gofmt -l plugins/flightdeck/server/internal/store
git add plugins/flightdeck/server/internal/store/probe.go plugins/flightdeck/server/internal/store/probe_test.go
git commit -m "$(cat <<'EOF'
feat(flightdeck): 증분 계획을 적용 없이 읽는 ProbeMigration 을 만든다

store.Open 은 열면서 적용한다. 재기동 검증이 그것을 쓰면, 검증 도중 증분이 붙고
그 뒤 exec 가 실패했을 때 옛 프로세스가 새 스키마 위에서 돌게 된다.

판정은 migrate 와 같은 PlanMigration 을 쓴다 — 두 벌로 두면 표류한다.
없는 파일은 오류다: sql.Open 이 파일을 만들기 때문에, 부재를 확인 안 하면
검증이 빈 DB 를 만들어 놓고 "빈 DB 다"라고 답한다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: `ExeID` · `Replaced` · `Decide` — 판정을 순수 함수로

**Files:**
- Create: `plugins/flightdeck/server/cmd/fd/selfwatch.go`
- Create: `plugins/flightdeck/server/cmd/fd/selfwatch_other.go`
- Test: `plugins/flightdeck/server/cmd/fd/selfwatch_test.go`

**Interfaces:**
- Produces:
  - `type ExeID struct { OK bool; Dev, Ino uint64; Size int64; MtimeNano int64 }`
  - `func (e ExeID) Same(o ExeID) bool`
  - `type Action int` — `ActNothing`, `ActVerify`, `ActExec`, `ActRefuse`
  - `func Decide(start, now, lastFailed ExeID, statErr error) (Action, string)` — **`ActNothing` 또는 `ActVerify` 만 낸다.** `ActExec`·`ActRefuse` 는 Task 4 의 검증 단계가 낸다.
  - `func exeIDOfPath(path string) (ExeID, error)` (유닉스) / 비유닉스는 `selfwatch_other.go` 에서 `ErrSelfWatchUnsupported`
  - `func selfWatchSupported() bool`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`cmd/fd/selfwatch_test.go`:

```go
package main

import (
	"errors"
	"strings"
	"testing"
)

func id(ino uint64, mtime int64) ExeID {
	return ExeID{OK: true, Dev: 1, Ino: ino, Size: 100, MtimeNano: mtime}
}

func TestDecideDoesNothingWhenUnchanged(t *testing.T) {
	a := id(10, 1000)
	got, why := Decide(a, a, ExeID{}, nil)
	if got != ActNothing {
		t.Fatalf("안 바뀌었는데 %v 다 — %s", got, why)
	}
}

func TestDecideVerifiesWhenInodeChanged(t *testing.T) {
	got, why := Decide(id(10, 1000), id(11, 1000), ExeID{}, nil)
	if got != ActVerify {
		t.Fatalf("아이노드가 바뀌었는데 %v 다 — %s", got, why)
	}
	if strings.TrimSpace(why) == "" {
		t.Fatal("사유가 비었다")
	}
}

func TestDecideVerifiesWhenOnlyMtimeChanged(t *testing.T) {
	// 같은 자리에 같은 크기로 덮어써도 교체다. mv 가 아니라 cp 로 배포하는 경로가 있다.
	if got, _ := Decide(id(10, 1000), id(10, 2000), ExeID{}, nil); got != ActVerify {
		t.Fatalf("mtime 만 바뀌었는데 %v 다", got)
	}
}

// ★ stat 실패는 교체가 아니다. exec 할 대상이 없는데 exec 로 가면 서버가 사라진다.
func TestDecideDoesNothingWhenStatFails(t *testing.T) {
	got, why := Decide(id(10, 1000), ExeID{}, ExeID{}, errors.New("no such file"))
	if got != ActNothing {
		t.Fatalf("stat 이 실패했는데 %v 다 — %s", got, why)
	}
	if !strings.Contains(why, "no such file") {
		t.Fatalf("사유가 원인을 안 나른다: %q", why)
	}
}

// ★ 같은 고장난 바이너리를 30초마다 다시 돌리지 않는다.
func TestDecideSkipsAlreadyFailedBuild(t *testing.T) {
	bad := id(11, 1000)
	if got, _ := Decide(id(10, 1000), bad, bad, nil); got != ActNothing {
		t.Fatalf("이미 실패한 판인데 %v 다", got)
	}
}

// 사람이 고쳐서 파일이 **또** 바뀌면 다시 시도한다.
func TestDecideRetriesAfterTheFileChangesAgain(t *testing.T) {
	bad := id(11, 1000)
	if got, _ := Decide(id(10, 1000), id(12, 3000), bad, nil); got != ActVerify {
		t.Fatalf("고친 뒤인데 %v 다", got)
	}
}

func TestSameRequiresBothOK(t *testing.T) {
	a := id(10, 1000)
	if a.Same(ExeID{}) {
		t.Fatal("관측 안 된 값과 같다고 했다")
	}
	if (ExeID{}).Same(ExeID{}) {
		t.Fatal("둘 다 관측 안 됐는데 같다고 했다 — 그것은 '같다'가 아니라 '모른다'다")
	}
}
```

- [ ] **Step 2: 시험이 실패하는 것을 확인한다**

```
go test ./cmd/fd/ -run 'TestDecide|TestSame' -v
```

기대: `undefined: ExeID` 로 컴파일 실패.

- [ ] **Step 3: 최소 구현**

`cmd/fd/selfwatch.go`:

```go
//go:build unix

package main

import (
	"fmt"
	"os"
	"syscall"
)

// ExeID 는 실행 파일 하나의 정체다. 순수 값이다.
//
// ★ **OK 가 먼저다.** false 면 나머지 필드는 값이 아니라 빈칸이다.
// 관측 못 한 것을 0 으로 접으면 "둘 다 0이니 같다"가 되고, 그 순간 이 축의 판별력이 사라진다.
//
// Dev·Ino 는 유닉스 전제다. 이 파일 전체가 unix 빌드 태그 뒤에 있고,
// 비유닉스는 selfwatch_other.go 의 no-op 이 받는다.
type ExeID struct {
	OK        bool
	Dev, Ino  uint64
	Size      int64
	MtimeNano int64
}

// Same 은 두 관측이 같은 파일을 가리키는지다. 순수 함수다.
// **한쪽이라도 관측 안 됐으면 같지 않다** — 모르는 것은 같은 것이 아니다.
func (e ExeID) Same(o ExeID) bool {
	if !e.OK || !o.OK {
		return false
	}
	return e.Dev == o.Dev && e.Ino == o.Ino && e.Size == o.Size && e.MtimeNano == o.MtimeNano
}

func (e ExeID) String() string {
	if !e.OK {
		return "관측 안 됨"
	}
	return fmt.Sprintf("ino=%d size=%d mtime=%d", e.Ino, e.Size, e.MtimeNano)
}

// Action 은 감시기가 이번 회차에 할 일이다.
type Action int

const (
	ActNothing Action = iota // 아무것도 안 한다
	ActVerify                // 후보다 — 자식으로 검증한다
	ActExec                  // 검증 통과 — 드레인 후 exec
	ActRefuse                // 검증 실패 — 그대로 산다
)

func (a Action) String() string {
	switch a {
	case ActVerify:
		return "verify"
	case ActExec:
		return "exec"
	case ActRefuse:
		return "refuse"
	default:
		return "nothing"
	}
}

// Decide 는 이번 회차에 무엇을 할지 정한다. 순수 함수다.
//
// **ActNothing 또는 ActVerify 만 낸다.** 검증 결과(ActExec·ActRefuse)는 이 함수가 모른다 —
// 그것은 자식 프로세스를 돌려 봐야 아는 사실이고, 순수 함수에 부수효과를 들이면
// 이 판정을 시험이 못 준다.
func Decide(start, now, lastFailed ExeID, statErr error) (Action, string) {
	if statErr != nil {
		// 교체가 아니라 삭제·권한 문제다. exec 할 대상이 없는데 가면 서버가 사라진다.
		return ActNothing, fmt.Sprintf("실행 파일을 못 쟀다: %v", statErr)
	}
	if !now.OK {
		return ActNothing, "실행 파일을 못 쟀다(사유 없음)"
	}
	if now.Same(start) {
		return ActNothing, "그대로다"
	}
	if now.Same(lastFailed) {
		return ActNothing, "이미 검증에 실패한 판이다 — 파일이 또 바뀌면 다시 본다"
	}
	return ActVerify, fmt.Sprintf("실행 파일이 교체됐다: %s → %s", start, now)
}

// exeIDOfPath 는 경로 하나를 잰다.
func exeIDOfPath(path string) (ExeID, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return ExeID{}, err
	}
	sys, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return ExeID{}, fmt.Errorf("stat 을 해석하지 못했다(path=%q)", path)
	}
	return ExeID{
		OK: true, Dev: uint64(sys.Dev), Ino: uint64(sys.Ino),
		Size: fi.Size(), MtimeNano: fi.ModTime().UnixNano(),
	}, nil
}

// selfWatchSupported 는 이 플랫폼에서 자기 재기동이 가능한가다.
func selfWatchSupported() bool { return true }
```

`cmd/fd/selfwatch_other.go`:

```go
//go:build !unix

package main

import (
	"errors"
	"fmt"
)

// ★ 비유닉스는 **오류를 낸다.** 빈 값을 돌려주면 호출부가 "쟀는데 안 바뀌었다"와
// "못 쟀다"를 구분 못 하고, 그러면 감시기가 조용히 아무것도 안 하는 상태로 산다
// (internal/window/proc_other.go 와 같은 규율).
//
// syscall.Exec 이 없는 플랫폼이라 애초에 자기 재기동을 할 수 없다.
var errSelfWatchUnsupported = errors.New("이 플랫폼은 자기 재기동을 지원하지 않는다(syscall.Exec 부재)")

type ExeID struct{ OK bool }

func (e ExeID) Same(o ExeID) bool { return false }
func (e ExeID) String() string    { return "관측 안 됨" }

type Action int

const (
	ActNothing Action = iota
	ActVerify
	ActExec
	ActRefuse
)

func (a Action) String() string { return "nothing" }

func Decide(start, now, lastFailed ExeID, statErr error) (Action, string) {
	return ActNothing, errSelfWatchUnsupported.Error()
}

func exeIDOfPath(path string) (ExeID, error) {
	return ExeID{}, fmt.Errorf("%w (path=%q)", errSelfWatchUnsupported, path)
}

func selfWatchSupported() bool { return false }
```

- [ ] **Step 4: 시험이 통과하는 것을 확인한다**

```
go test ./cmd/fd/ -run 'TestDecide|TestSame' -v
GOOS=windows GOARCH=amd64 go vet ./cmd/fd/
GOOS=darwin  GOARCH=arm64 go vet ./cmd/fd/
GOOS=windows GOARCH=amd64 go build ./...
GOOS=darwin  GOARCH=arm64 go build ./...
```

기대: 7건 PASS. 교차 빌드 둘 다 통과(비유닉스 갈래가 컴파일된다).

- [ ] **Step 5: 커밋**

```bash
gofmt -l plugins/flightdeck/server/cmd/fd
git add plugins/flightdeck/server/cmd/fd/selfwatch.go plugins/flightdeck/server/cmd/fd/selfwatch_other.go plugins/flightdeck/server/cmd/fd/selfwatch_test.go
git commit -m "$(cat <<'EOF'
feat(flightdeck): 실행 파일 교체 판정을 순수 함수로 세운다

ExeID 는 OK 를 맨 앞에 둔다. 관측 못 한 것을 0으로 접으면 "둘 다 0이니 같다"가
되고 그 순간 이 축의 판별력이 사라진다.

Decide 는 ActNothing 또는 ActVerify 만 낸다. 검증 결과는 자식 프로세스를 돌려야
아는 사실이라 순수 함수가 모른다 — 부수효과를 들이면 이 판정을 시험이 못 준다.

stat 실패를 교체로 보지 않는다. exec 할 대상이 없는데 가면 서버가 사라진다.
이미 실패한 판은 다시 안 본다. 파일이 또 바뀌면(사람이 고쳤으면) 다시 본다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: `fd selfcheck` 서브명령

**Files:**
- Create: `plugins/flightdeck/server/cmd/fd/selfcheck.go`
- Test: `plugins/flightdeck/server/cmd/fd/selfcheck_test.go`
- Modify: `plugins/flightdeck/server/cmd/fd/main.go` (서브명령 switch 에 한 줄)

**Interfaces:**
- Consumes: Task 1 의 `store.ProbeMigration(ctx, path) (store.MigrationPlan, error)` · 기존 `buildinfo.Self() buildinfo.Coord` · `buildinfo.Short(Coord) string`
- Produces: `func runSelfcheck(args []string, out io.Writer) int` — 0 이면 재기동해도 된다

**계약:** stdout 첫 줄은 `fd selfcheck ok build=<Short(coord)>` 다. 감시기가 이 줄에서 새 판의 좌표를 읽어 `from → to` 를 만든다.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`cmd/fd/selfcheck_test.go`:

```go
package main

import (
	"bytes"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// sqlite 드라이버는 internal/store 가 blank import 로 등록한다(cmd/fd 가 그것을 쓴다).
// 여기서 다시 import 하지 않는다.
func selfcheckDB(t *testing.T, sqls ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fd.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("열기 실패: %v", err)
	}
	defer db.Close()
	for _, q := range sqls {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("준비 실패(%q): %v", q, err)
		}
	}
	if len(sqls) == 0 {
		if _, err := db.Exec(`SELECT 1`); err != nil {
			t.Fatalf("빈 DB 실패: %v", err)
		}
	}
	return path
}

func TestSelfcheckPassesOnCleanDB(t *testing.T) {
	var out bytes.Buffer
	code := runSelfcheck([]string{"--db", selfcheckDB(t)}, &out)
	if code != 0 {
		t.Fatalf("깨끗한 DB 인데 종료코드 %d — %s", code, out.String())
	}
	if !strings.HasPrefix(out.String(), "fd selfcheck ok build=") {
		t.Fatalf("계약된 첫 줄이 아니다: %q", out.String())
	}
}

// ★ 이 갈래가 이 명령의 존재 이유다.
func TestSelfcheckFailsWhenMigrationWouldBeRejected(t *testing.T) {
	path := selfcheckDB(t, `CREATE TABLE somebody_elses (a TEXT)`)
	var out bytes.Buffer
	code := runSelfcheck([]string{"--db", path}, &out)
	if code == 0 {
		t.Fatalf("증분이 거절될 DB 인데 통과했다 — %s", out.String())
	}
	if !strings.Contains(out.String(), "schema_version") {
		t.Fatalf("사유가 원인을 안 나른다: %q", out.String())
	}
}

func TestSelfcheckFailsWhenDBMissing(t *testing.T) {
	var out bytes.Buffer
	if code := runSelfcheck([]string{"--db", filepath.Join(t.TempDir(), "nope.db")}, &out); code == 0 {
		t.Fatalf("없는 DB 인데 통과했다 — %s", out.String())
	}
}

func TestSelfcheckRequiresDBFlag(t *testing.T) {
	var out bytes.Buffer
	if code := runSelfcheck(nil, &out); code == 0 {
		t.Fatal("--db 없이 통과했다")
	}
}

// main 의 서브명령 표에 실제로 걸렸는가 — 없으면 감시기가 부를 때 usage 만 나온다.
//
// run 의 시그니처는 main.go:55 다:
//   func run(args []string, env func(string) (string, bool), stdin io.Reader, stdout, stderr io.Writer) int
func TestSelfcheckIsWiredIntoMain(t *testing.T) {
	var out, errBuf bytes.Buffer
	noEnv := func(string) (string, bool) { return "", false }
	code := run([]string{"selfcheck", "--db", selfcheckDB(t)},
		noEnv, strings.NewReader(""), &out, &errBuf)
	if code != 0 {
		t.Fatalf("main 경유 종료코드 %d — out=%q err=%q", code, out.String(), errBuf.String())
	}
	if !strings.Contains(out.String(), "fd selfcheck ok") {
		t.Fatalf("계약된 첫 줄이 stdout 으로 안 나왔다: %q", out.String())
	}
}
```

> `run` 은 `stdout` 을 `runSelfcheck` 에 그대로 넘긴다(Task 3 Step 3 의 switch 참조).
> 그래서 이 시험은 배선과 계약 문구를 **한 번에** 본다.

- [ ] **Step 2: 시험이 실패하는 것을 확인한다**

```
go test ./cmd/fd/ -run TestSelfcheck -v
```

기대: `undefined: runSelfcheck` 로 컴파일 실패.

- [ ] **Step 3: 최소 구현**

`cmd/fd/selfcheck.go`:

```go
package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/buildinfo"
	"github.com/kweiza/flightdeck/internal/store"
)

// runSelfcheck 는 `fd selfcheck --db <경로>` 다.
//
// **재기동해도 되는가**에만 답한다. 이 명령이 0 을 내면 감시기가 syscall.Exec 로 넘어간다.
// 그래서 여기서 하는 일은 최소여야 한다 — 무엇을 더 볼수록 이 명령 자체가 실패 원인이 된다.
//
// 보는 것 셋:
//  1. 이 바이너리가 실행된다(이 프로세스가 떴다는 것 자체)
//  2. 자기 빌드 좌표를 낸다
//  3. DB 증분 계획이 거절이 아니다 — **적용하지 않는다**
//
// ★ 3번이 강등도 막는다. 옛 바이너리로 되돌리면 DB 버전이 그 바이너리의 SchemaVersion
// 보다 높고, PlanMigration 이 이미 그 경우를 거절로 낸다. 규칙을 새로 만들지 않는다.
func runSelfcheck(args []string, out io.Writer) int {
	fs := newFlagSet("selfcheck")
	dbPath := fs.String("db", "", "검사할 SQLite 파일 경로")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*dbPath) == "" {
		fmt.Fprintln(out, "selfcheck: --db 가 비었다 — 무엇을 검사할지가 없다")
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	plan, err := store.ProbeMigration(ctx, *dbPath)
	if err != nil {
		fmt.Fprintf(out, "selfcheck: DB 증분 계획을 읽지 못했다: %v\n", err)
		return 1
	}
	if plan.Action == store.MigrateReject {
		fmt.Fprintf(out, "selfcheck: 이 판으로 DB 를 열면 거절된다 — %s\n", plan.Reason)
		return 1
	}

	// 계약: 첫 줄에서 감시기가 새 판의 좌표를 읽는다.
	fmt.Fprintf(out, "fd selfcheck ok build=%s\n", buildinfo.Short(buildinfo.Self()))
	fmt.Fprintf(out, "  증분 계획: %s — %s\n", plan.Action, plan.Reason)
	return 0
}
```

`cmd/fd/main.go` 의 switch 에 한 줄 더한다 (`case "serve":` 바로 아래):

```go
	case "selfcheck":
		// ★ App 을 만들지 않는다. 이 명령은 재기동 검증의 피험자라, 서버 도달·세션 열기
		// 같은 축이 끼면 그 축의 실패가 "새 판이 고장났다"로 오독된다.
		return runSelfcheck(args[1:], stdout)
```

- [ ] **Step 4: 시험이 통과하는 것을 확인한다**

```
go test ./cmd/fd/ -run TestSelfcheck -v
```

기대: 5건 PASS.

- [ ] **Step 5: 커밋**

```bash
gofmt -l plugins/flightdeck/server/cmd/fd
git add plugins/flightdeck/server/cmd/fd/selfcheck.go plugins/flightdeck/server/cmd/fd/selfcheck_test.go plugins/flightdeck/server/cmd/fd/main.go
git commit -m "$(cat <<'EOF'
feat(flightdeck): fd selfcheck — 재기동해도 되는가에만 답한다

syscall.Exec 는 프로세스 이미지를 갈아치운다. 새 바이너리가 못 뜨면 서버가 그냥
사라지고 bare 경로에는 되살릴 감시자가 없다. 그래서 exec 전에 그 파일을 자식으로
한 번 돌려 본다.

보는 것은 셋뿐이다 — 실행되나, 빌드 좌표를 내나, 증분 계획이 거절이 아닌가.
무엇을 더 볼수록 이 명령 자체가 실패 원인이 된다. App 을 안 만드는 것도 같은
이유다: 서버 도달이 끼면 그 실패가 "새 판이 고장났다"로 오독된다.

3번이 강등도 막는다. 옛 판으로 되돌리면 DB 버전 > 코드 버전이라 PlanMigration 이
이미 거절을 낸다. 규칙을 새로 만들지 않는다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: 감시 루프 + `serve` 배선

**Files:**
- Modify: `plugins/flightdeck/server/cmd/fd/selfwatch.go` (루프를 더한다)
- Modify: `plugins/flightdeck/server/cmd/fd/serve.go`
- Test: `plugins/flightdeck/server/cmd/fd/selfwatch_test.go` (시험을 더한다)

**Interfaces:**
- Consumes: Task 2 의 `ExeID`·`Decide`·`exeIDOfPath`·`selfWatchSupported` · Task 3 의 `fd selfcheck`
- Produces:
  - `type selfWatcher struct { ... }`
  - `func newSelfWatcher(log *slog.Logger, dbPath string) *selfWatcher`
  - `func (w *selfWatcher) Status() selfUpdateStatus`
  - `func (w *selfWatcher) Run(ctx context.Context, drain func())`
  - `type selfUpdateStatus struct { Watching bool; Reason, From, To, Outcome, Detail string; LastAt time.Time }`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`cmd/fd/selfwatch_test.go` 에 더한다:

```go
기존 import 블록에 `"context"` · `"errors"` · `"log/slog"` · `"os"` 를 더한다
(`"strings"` · `"testing"` 은 Task 2 에서 이미 있다). **`"time"` 은 더하지 마라** — 아래
시험 어디에서도 안 쓴다.

```go
// tick 은 감시기를 정확히 한 회차만 돌린다.
func (w *selfWatcher) tick(ctx context.Context, drain func()) Action {
	return w.step(ctx, drain)
}

func newTestWatcher(t *testing.T) *selfWatcher {
	t.Helper()
	w := newSelfWatcher(slog.New(slog.DiscardHandler), "/tmp/does-not-matter.db")
	w.start = id(10, 1000)
	w.exePath = "/fake/fd"
	return w
}

func TestWatcherDoesNothingWhenBinaryUnchanged(t *testing.T) {
	w := newTestWatcher(t)
	w.stat = func(string) (ExeID, error) { return id(10, 1000), nil }
	w.verify = func(context.Context, string, string) (string, error) { t.Fatal("검증하면 안 된다"); return "", nil }
	w.execSelf = func(string, []string, []string) error { t.Fatal("exec 하면 안 된다"); return nil }

	if got := w.tick(context.Background(), func() { t.Fatal("드레인하면 안 된다") }); got != ActNothing {
		t.Fatalf("%v 다", got)
	}
}

// ★ 이 시험이 이 태스크의 본체다 — 프로세스를 안 죽이고 exec 를 단언한다.
func TestWatcherExecsAfterVerifyPasses(t *testing.T) {
	w := newTestWatcher(t)
	w.stat = func(string) (ExeID, error) { return id(11, 2000), nil }
	w.verify = func(context.Context, string, string) (string, error) { return "1d044b2 · 2026-08-05T00:11:57Z", nil }

	drained := false
	var gotExe string
	var gotArgs []string
	w.execSelf = func(exe string, argv, env []string) error {
		if !drained {
			t.Fatal("드레인 전에 exec 했다 — 인플라이트 요청이 통째로 끊긴다")
		}
		gotExe, gotArgs = exe, argv
		return nil
	}

	if got := w.tick(context.Background(), func() { drained = true }); got != ActExec {
		t.Fatalf("%v 다", got)
	}
	if gotExe != "/fake/fd" {
		t.Fatalf("exec 경로가 %q 다", gotExe)
	}
	if len(gotArgs) == 0 || gotArgs[0] != os.Args[0] {
		t.Fatalf("argv 를 그대로 안 넘겼다: %v", gotArgs)
	}
}

func TestWatcherRefusesAndRemembersFailedBuild(t *testing.T) {
	w := newTestWatcher(t)
	w.stat = func(string) (ExeID, error) { return id(11, 2000), nil }
	calls := 0
	w.verify = func(context.Context, string, string) (string, error) {
		calls++
		return "", errors.New("selfcheck exit 1 — 증분 계획이 거절된다")
	}
	w.execSelf = func(string, []string, []string) error { t.Fatal("검증에 실패했는데 exec 했다"); return nil }

	if got := w.tick(context.Background(), func() {}); got != ActRefuse {
		t.Fatalf("1회차가 %v 다", got)
	}
	// 2회차 — 같은 판이면 다시 검증하지 않는다
	if got := w.tick(context.Background(), func() {}); got != ActNothing {
		t.Fatalf("2회차가 %v 다", got)
	}
	if calls != 1 {
		t.Fatalf("같은 고장난 판을 %d번 검증했다", calls)
	}

	st := w.Status()
	if st.Outcome != "refused" || !strings.Contains(st.Detail, "selfcheck") {
		t.Fatalf("거절이 상태에 안 남았다: %+v", st)
	}
	if st.LastAt.IsZero() {
		t.Fatal("시도 시각이 안 남았다")
	}
}

// ★ 컨테이너에서는 감시를 아예 안 켠다. 켜 두면 "보는 중"이라고 말하면서
// 영원히 안 올 교체를 기다린다 — 침묵보다 나쁘다(따라오고 있다고 믿게 만든다).
func TestContainerIsNotWatched(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		hasDockerEnv, hasDataDir bool
		wantContainer            bool
	}{
		{"맨몸", false, false, false},
		{"/.dockerenv 있음", true, false, true},
		{"/data 볼륨만 있음", false, true, true},
		{"둘 다", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, why := containerVerdict(tc.hasDockerEnv, tc.hasDataDir)
			if got != tc.wantContainer {
				t.Fatalf("컨테이너 판정 = %v, 기대 %v (사유 %q)", got, tc.wantContainer, why)
			}
			if got && !strings.Contains(why, "컨테이너") {
				t.Fatalf("사유가 컨테이너를 안 말한다: %q", why)
			}
			if !got && strings.TrimSpace(why) != "" {
				t.Fatalf("컨테이너가 아닌데 사유가 있다: %q", why)
			}
		})
	}
}

func TestWatcherStatusSaysWatchingFalseWhenUnsupported(t *testing.T) {
	w := newSelfWatcher(slog.New(slog.DiscardHandler), "/tmp/x.db")
	w.watching = false
	w.reason = "이 플랫폼은 자기 재기동을 지원하지 않는다"
	st := w.Status()
	if st.Watching {
		t.Fatal("안 보고 있는데 watching=true 다")
	}
	if strings.TrimSpace(st.Reason) == "" {
		t.Fatal("왜 안 보는지가 비었다 — 빈 상태는 '아직 갱신이 없었다'로 읽힌다")
	}
}
```

- [ ] **Step 2: 시험이 실패하는 것을 확인한다**

```
go test ./cmd/fd/ -run TestWatcher -v
```

기대: `undefined: selfWatcher` 로 컴파일 실패.

- [ ] **Step 3: 최소 구현**

`cmd/fd/selfwatch.go` 에 더한다:

```go
// defaultSelfWatchInterval 은 실행 파일을 다시 재는 주기다.
//
// ★ **근거 있는 값이 아니다.** 갱신은 사람이 `plugin update` 를 누른 뒤에만 오고,
// 그때 이 정도 안에 따라오면 충분하다는 판단이다. 근거를 만들 수 있으면 그때 고친다
// (fd-live-window-baseline 이 같은 종류의 부채다).
const defaultSelfWatchInterval = 30 * time.Second

// selfVerifyTimeout 은 자식 selfcheck 하나에 주는 시간이다.
const selfVerifyTimeout = 15 * time.Second

// selfUpdateStatus 는 자동 갱신 축의 현재 상태다.
//
// ★ **Watching 이 먼저다.** 감시기가 안 떴는데 나머지가 비어 있으면 "아직 갱신이
// 없었다"로 읽힌다 — 그것은 "안 보고 있다"와 전혀 다르다. buildinfo.Coord 의 Known 이
// 같은 규율이고, 그 규율을 안 지켜 화면에 빈칸이 찍힌 적이 있다.
//
// **성공은 여기 안 남는다.** 성공하면 프로세스가 갈아치워져 새 프로세스는 그 사실을
// 모른다. 남는 것은 build 좌표가 바뀐 것뿐이고 그것으로 충분하다.
type selfUpdateStatus struct {
	Watching bool
	Reason   string    // Watching=false 일 때 왜 안 보는지
	LastAt   time.Time // 시도가 없었으면 제로값
	From, To string
	Outcome  string // "refused" | "failed"
	Detail   string
}

type selfWatcher struct {
	log      *slog.Logger
	dbPath   string
	exePath  string
	start    ExeID
	lastFail ExeID
	watching bool
	reason   string
	interval time.Duration

	mu     sync.Mutex
	status selfUpdateStatus

	// 주입 자리 — 시험이 프로세스를 안 죽이고 단언한다.
	stat     func(string) (ExeID, error)
	verify   func(ctx context.Context, exe, db string) (buildLine string, err error)
	execSelf func(exe string, argv, env []string) error
}

// containerVerdict 는 이 프로세스가 컨테이너 안인가다. 순수 함수다.
//
// ★ 컨테이너에서는 감시를 **아예 안 켠다.** 이미지는 불변이라 실행 파일이 영원히 안 바뀌고,
// 그 상태로 "보는 중"이라고 말하면 읽는 쪽은 따라오고 있다고 믿는다. 침묵보다 나쁘다 —
// 틀린 안심을 준다. 컨테이너의 갱신은 `docker compose up -d --build` 로 사람이 한다.
//
// /data 를 신호로 쓰는 것은 이 저장소의 기존 관용구다(DefaultDBPath 가 같은 축을 본다).
func containerVerdict(hasDockerEnv, hasDataDir bool) (bool, string) {
	switch {
	case hasDockerEnv:
		return true, "이 서버는 컨테이너다(/.dockerenv) — 자기 이미지를 다시 만들 수 없어 자기 갱신을 안 한다. " +
			"`docker compose up -d --build` 가 갱신 경로다"
	case hasDataDir:
		return true, "이 서버는 컨테이너로 보인다(/data 볼륨) — 자기 갱신을 안 한다. " +
			"`docker compose up -d --build` 가 갱신 경로다"
	}
	return false, ""
}

// newSelfWatcher 는 감시기를 만든다. **기준값을 여기서 정한다.**
func newSelfWatcher(log *slog.Logger, dbPath string) *selfWatcher {
	w := &selfWatcher{
		log: log, dbPath: dbPath, interval: defaultSelfWatchInterval,
		stat: exeIDOfPath, verify: verifyWithSelfcheck, execSelf: execSelf,
	}
	if !selfWatchSupported() {
		w.reason = "이 플랫폼은 자기 재기동을 지원하지 않는다(syscall.Exec 부재)"
		return w
	}
	_, dockerErr := os.Stat("/.dockerenv")
	_, dataErr := os.Stat("/data")
	if isContainer, why := containerVerdict(dockerErr == nil, dataErr == nil); isContainer {
		w.reason = why
		return w
	}
	exe, err := os.Executable()
	if err != nil {
		w.reason = fmt.Sprintf("실행 파일 자리를 못 읽었다: %v", err)
		return w
	}
	w.exePath = exe

	// ★ 기준값을 /proc/self/exe 에서 읽는다. 그 자리는 **지금 도는 이미지**를 가리키고,
	// 파일이 이미 교체된 뒤라면 경로를 stat 한 값과 다르다. 경로만 재면
	// "이미 낡은 채로 시작한 서버"가 자기를 최신으로 기억해 영원히 트리거하지 않는다.
	if id, err := w.stat("/proc/self/exe"); err == nil {
		w.start = id
	} else if id, err := w.stat(exe); err == nil {
		w.start = id
		log.Warn("/proc/self/exe 를 못 읽어 경로로 기준을 잡는다 — "+
			"이미 교체된 채 시작한 경우를 이 프로세스는 못 본다", "error", err.Error())
	} else {
		w.reason = fmt.Sprintf("실행 파일을 못 쟀다: %v", err)
		return w
	}
	w.watching = true
	return w
}

// Status 는 /healthz 가 실을 값이다. 동시 호출된다.
func (w *selfWatcher) Status() selfUpdateStatus {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := w.status
	s.Watching, s.Reason = w.watching, w.reason
	return s
}

func (w *selfWatcher) setStatus(f func(*selfUpdateStatus)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	f(&w.status)
}

// Run 은 감시 루프다. ctx 가 끝나면 돌아온다.
//
// drain 은 "서버를 정상 종료시키고 그것이 끝날 때까지 기다린다"이다.
// exec 는 프로세스 이미지를 갈아치우므로 **인플라이트 요청이 통째로 끊기기 전에** 불러야 한다.
func (w *selfWatcher) Run(ctx context.Context, drain func()) {
	if !w.watching {
		w.log.Info("자기 재기동 감시를 안 켠다", "reason", clip(w.reason, 200))
		return
	}
	w.log.Info("자기 재기동 감시 시작",
		"exe", clip(w.exePath, 200), "interval", w.interval.String())
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if w.step(ctx, drain) == ActExec {
				return // 여기 도달하면 exec 가 실패한 것이다. 호출부가 처리한다
			}
		}
	}
}

// step 은 한 회차다. 시험이 이것을 직접 부른다.
func (w *selfWatcher) step(ctx context.Context, drain func()) Action {
	now, statErr := w.stat(w.exePath)
	act, why := Decide(w.start, now, w.lastFail, statErr)
	if act != ActVerify {
		return act
	}
	w.log.Info("실행 파일이 교체됐다 — 검증한다", "reason", clip(why, 300))

	vctx, cancel := context.WithTimeout(ctx, selfVerifyTimeout)
	defer cancel()
	buildLine, err := w.verify(vctx, w.exePath, w.dbPath)

	from := buildinfo.Short(buildinfo.Self())
	if err != nil {
		w.lastFail = now
		w.setStatus(func(s *selfUpdateStatus) {
			s.LastAt, s.From, s.To = time.Now().UTC(), from, buildLine
			s.Outcome, s.Detail = "refused", err.Error()
		})
		w.log.Warn("자동 갱신 거절 — 그대로 산다",
			"from", clip(from, 120), "reason", clip(err.Error(), 400))
		return ActRefuse
	}

	w.log.Info("검증 통과 — 드레인 후 재기동한다",
		"from", clip(from, 120), "to", clip(buildLine, 120))
	drain()
	if err := w.execSelf(w.exePath, os.Args, os.Environ()); err != nil {
		// 리스너는 이미 닫혔다. 되살리는 척하지 않는다 — 호출부가 비0으로 죽는다.
		w.setStatus(func(s *selfUpdateStatus) {
			s.LastAt, s.From, s.To = time.Now().UTC(), from, buildLine
			s.Outcome, s.Detail = "failed", err.Error()
		})
		w.log.Error("재기동 실패 — 리스너는 이미 닫혔다",
			"exe", clip(w.exePath, 200), "error", err.Error())
	}
	return ActExec
}

// verifyWithSelfcheck 는 새 바이너리를 자식으로 한 번 돌린다.
func verifyWithSelfcheck(ctx context.Context, exe, db string) (string, error) {
	cmd := exec.CommandContext(ctx, exe, "selfcheck", "--db", db)
	out, err := cmd.CombinedOutput()
	line := firstLine(string(out))
	if err != nil {
		return "", fmt.Errorf("selfcheck 실패(%v): %s", err, clip(strings.TrimSpace(string(out)), 400))
	}
	// 계약: `fd selfcheck ok build=<좌표>`
	if b, ok := strings.CutPrefix(line, "fd selfcheck ok build="); ok {
		return strings.TrimSpace(b), nil
	}
	return "", fmt.Errorf("selfcheck 가 계약된 첫 줄을 안 냈다: %s", clip(line, 200))
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// execSelf 는 진짜 syscall.Exec 다. 성공하면 **돌아오지 않는다.**
func execSelf(exe string, argv, env []string) error {
	return syscall.Exec(exe, argv, env)
}
```

import 에 `"context"`, `"log/slog"`, `"os/exec"`, `"strings"`, `"sync"`, `"syscall"`, `"time"`,
`"github.com/kweiza/flightdeck/internal/buildinfo"` 를 더한다.

**감시기 본체는 중립 파일에 한 벌만 둔다 — 비유닉스에 복제하지 마라.**

> **계획 정정 (Task 2 리뷰가 드러낸 것).** 원래 이 자리는 `selfwatch_other.go` 에
> `selfUpdateStatus`·`selfWatcher`·`newSelfWatcher`·`Status`·`Run` 을 **복제**하라고 적혀 있었다.
> 그것이 Task 2 에서 실제로 깨진 결함과 **같은 모양**이다 — 구조체를 빌드 태그로 복제하면
> 필드 집합이 갈리고, 태그 없는 시험 파일이 그 순간 다른 플랫폼에서만 컴파일에 실패한다.
> `GOOS=windows go vet` 이 그것을 잡았고 `go build` 는 못 잡았다(`_test.go` 를 건너뛴다).
>
> 그래서 구조를 바꾼다. `selfWatcher` 의 필드와 `step`·`Run`·`Status`·`verifyWithSelfcheck`·
> `containerVerdict` 는 **전부 플랫폼 중립**이다. 진짜로 갈려야 하는 것은 `execSelf` 하나뿐이다.

위 코드(`selfUpdateStatus` · `selfWatcher` · `newSelfWatcher` · `Status` · `setStatus` ·
`Run` · `step` · `verifyWithSelfcheck` · `firstLine` · `containerVerdict` · 두 상수)를
**태그 없는 `cmd/fd/selfwatch.go`** 에 넣는다. `execSelf` 만 빼서 태그별로 가른다:

`cmd/fd/selfwatch_unix.go` (`//go:build unix`) 에 더한다:

```go
// execSelf 는 진짜 syscall.Exec 다. 성공하면 **돌아오지 않는다.**
func execSelf(exe string, argv, env []string) error {
	return syscall.Exec(exe, argv, env)
}
```

`cmd/fd/selfwatch_other.go` (`//go:build !unix`) 에 더한다:

```go
// ★ 비유닉스에는 syscall.Exec 이 없다. **빈 성공을 돌려주면 안 된다** —
// 호출부가 드레인까지 마친 뒤 "재기동했다"로 읽고 서버는 내려간 채로 남는다.
func execSelf(exe string, argv, env []string) error {
	return fmt.Errorf("%w (exe=%q)", errSelfWatchUnsupported, exe)
}
```

비유닉스에서 `newSelfWatcher` 는 `selfWatchSupported()` 가 거짓이라 **감시기를 안 켜고**
사유만 들고 돌아온다(위 구현 그대로). 그래서 no-op 을 따로 만들 필요가 없다.

`selfwatch.go` 의 import 에 `"context"`, `"log/slog"`, `"os/exec"`, `"strings"`, `"sync"`, `"time"`,
`"github.com/kweiza/flightdeck/internal/buildinfo"` 를 더한다. **`"syscall"` 은 `selfwatch_unix.go` 에만** 들어간다.

`cmd/fd/serve.go` 의 `runServe` 를 고친다. `ctx, stop := signal.NotifyContext(...)` **다음**에:

```go
	// ★ 감시기에게는 **자기만의 취소 손잡이**를 준다. 서버 ctx 를 그대로 주면
	// 드레인(= 그 ctx 취소)이 감시기 자신도 죽여서 exec 까지 못 간다.
	watchCtx, stopWatch := context.WithCancel(context.Background())
	defer stopWatch()
	serveCtx, drainServe := context.WithCancel(ctx)
	defer drainServe()

	served := make(chan struct{})
	watcher := newSelfWatcher(log, path)
	go watcher.Run(watchCtx, func() {
		drainServe()  // api.Serve 가 srv.Shutdown 으로 인플라이트를 마무리한다
		<-served      // 그것이 실제로 끝날 때까지 기다린다
	})
```

`handler` 를 만드는 `api.Options` 에 `SelfUpdate: func() api.SelfUpdateStatus { ... }` 를 더하는 것은 **Task 5** 다. 이 태스크에서는 배선만 한다.

`api.Serve` 호출부를 고친다:

```go
	serveErr := api.Serve(serveCtx, *addr, handler, log)
	close(served)
	if serveErr != nil {
		log.Error("서버를 띄우지 못했다", "route", clip(*addr, 120),
			"error", serveErr.Error(), "reason", PortAdvice(*addr, serveErr))
		return 1
	}
	// 드레인이 자동 갱신 때문이었으면 exec 가 이미 이 프로세스를 갈아치웠다.
	// 여기에 도달했다는 것은 exec 가 실패했거나 사람이 껐다는 뜻이다.
	if st := watcher.Status(); st.Outcome == "failed" {
		log.Error("자동 갱신이 실패해 서버가 내려간 상태다 — 재기동이 필요하다",
			"detail", clip(st.Detail, 400))
		return 1
	}
	log.Info("종료", "route", clip(*addr, 120))
	return 0
```

- [ ] **Step 4: 시험이 통과하는 것을 확인한다**

```
go test ./cmd/fd/ -run TestWatcher -v
go test ./... 
GOOS=windows GOARCH=amd64 go vet ./cmd/fd/
GOOS=darwin  GOARCH=arm64 go vet ./cmd/fd/
GOOS=windows GOARCH=amd64 go build ./...
GOOS=darwin  GOARCH=arm64 go build ./...
```

기대: `TestWatcher*` 4건 PASS, 전체 스위트 초록, 교차 빌드 통과.

- [ ] **Step 5: 커밋**

```bash
gofmt -l plugins/flightdeck/server/cmd/fd
git add plugins/flightdeck/server/cmd/fd/selfwatch.go plugins/flightdeck/server/cmd/fd/selfwatch_other.go plugins/flightdeck/server/cmd/fd/selfwatch_test.go plugins/flightdeck/server/cmd/fd/serve.go
git commit -m "$(cat <<'EOF'
feat(flightdeck): 서버가 자기 실행 파일 교체를 감지해 스스로 재기동한다

감시기에게 자기만의 취소 손잡이를 준다. 서버 ctx 를 그대로 주면 드레인이 감시기
자신도 죽여서 exec 까지 못 간다.

드레인은 api.Serve 가 이미 갖고 있는 srv.Shutdown 을 쓴다. 감시기는 그것이 실제로
끝날 때까지 기다린 뒤에 exec 한다 — 안 기다리면 인플라이트 요청이 통째로 끊긴다.

exec·검증·stat 을 전부 주입받는다. 그래야 시험이 "이 경로·이 argv 로 exec 하려
했다"를 프로세스를 안 죽이고 단언한다. 진짜 syscall.Exec 는 시험할 수 없다.

기준값을 /proc/self/exe 에서 읽는다. 그 자리는 지금 도는 이미지를 가리키고, 경로만
재면 이미 낡은 채 시작한 서버가 자기를 최신으로 기억해 영원히 트리거하지 않는다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: `/healthz` 에 `self_update` 를 싣는다

**Files:**
- Modify: `plugins/flightdeck/server/internal/api/api.go` (`Options.SelfUpdate`)
- Modify: `plugins/flightdeck/server/internal/api/handlers_meta.go` (`SelfUpdateStatus` · `HealthzBody` · `HealthzOf`)
- Modify: `plugins/flightdeck/server/internal/api/pure_test.go` (호출부 인자 하나)
- Modify: `plugins/flightdeck/server/cmd/fd/client.go` (`healthzResponse`)
- Modify: `plugins/flightdeck/server/cmd/fd/serve.go` (콜백 배선)
- Test: `plugins/flightdeck/server/internal/api/pure_test.go` (시험 추가)

**Interfaces:**
- Consumes: Task 4 의 `selfUpdateStatus`
- Produces: `api.SelfUpdateStatus` · `api.Options.SelfUpdate func() SelfUpdateStatus` · `HealthzOf(h, tokenSet, loopbackOpen bool, build buildinfo.Coord, su SelfUpdateStatus) HealthzBody`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/api/pure_test.go` 에 더한다:

```go
func TestHealthzCarriesSelfUpdateRefusal(t *testing.T) {
	at := time.Date(2026, 8, 5, 0, 31, 2, 0, time.UTC)
	body := HealthzOf(service.Health{OK: true, APIVersion: "1", DBOK: true},
		false, true, buildinfo.Coord{}, SelfUpdateStatus{
			Watching: true, LastAt: &at,
			From: "07e5df4", To: "1d044b2",
			Outcome: "refused", Detail: "selfcheck exit 1 — 증분 계획이 거절된다",
		})
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("직렬화 실패: %v", err)
	}
	for _, want := range []string{"self_update", "refused", "1d044b2"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("%q 가 응답에 없다: %s", want, raw)
		}
	}
}

// ★ 안 보고 있다는 사실이 '아직 갱신이 없었다'로 접히면 안 된다.
func TestHealthzSaysWhenItIsNotWatching(t *testing.T) {
	body := HealthzOf(service.Health{OK: true, APIVersion: "1", DBOK: true},
		false, true, buildinfo.Coord{}, SelfUpdateStatus{
			Watching: false, Reason: "이 플랫폼은 자기 재기동을 지원하지 않는다",
		})
	if body.SelfUpdate.Watching {
		t.Fatal("watching 이 참이다")
	}
	if strings.TrimSpace(body.SelfUpdate.Reason) == "" {
		t.Fatal("왜 안 보는지가 비었다")
	}
}
```

- [ ] **Step 2: 시험이 실패하는 것을 확인한다**

```
go test ./internal/api/ -run TestHealthz -v
```

기대: `undefined: SelfUpdateStatus` 및 `HealthzOf` 인자 수 불일치로 컴파일 실패.

- [ ] **Step 3: 최소 구현**

`internal/api/handlers_meta.go`:

```go
// SelfUpdateStatus 는 서버의 자동 갱신 축이다.
//
// ★ **Watching 이 먼저다.** 감시기가 안 떴는데 나머지가 비어 있으면
// "아직 갱신이 없었다"로 읽힌다 — 그것은 "안 보고 있다"와 전혀 다르다.
// buildinfo.Coord 의 Known 이 같은 규율이다.
//
// **성공은 여기 안 남는다.** 성공하면 프로세스가 갈아치워져 새 프로세스는 그 사실을 모른다.
type SelfUpdateStatus struct {
	Watching bool       `json:"watching"`
	Reason   string     `json:"reason,omitempty"`
	LastAt   *time.Time `json:"last_at,omitempty"`
	From     string     `json:"from,omitempty"`
	To       string     `json:"to,omitempty"`
	Outcome  string     `json:"outcome,omitempty"` // refused | failed
	Detail   string     `json:"detail,omitempty"`
}
```

`HealthzBody` 에 필드를 더한다 (`Build` 바로 아래):

```go
	// SelfUpdate 는 이 서버가 자기 판을 따라가고 있는가다.
	SelfUpdate SelfUpdateStatus `json:"self_update"`
```

`HealthzOf` 시그니처와 본문:

```go
func HealthzOf(h service.Health, tokenSet, loopbackOpen bool,
	build buildinfo.Coord, su SelfUpdateStatus) HealthzBody {
	b := HealthzBody{
		OK: h.OK, APIVersion: h.APIVersion, DBOK: h.DBOK, Build: build, SelfUpdate: su,
		DiskFreePct: h.DiskFreePct, DiskKnown: h.DiskKnown, At: h.At,
		// … 이하 기존 그대로
```

`handleHealthz` 를 고친다:

```go
	su := SelfUpdateStatus{Watching: false, Reason: "이 서버는 자동 갱신 축을 배선하지 않았다"}
	if s.opt.SelfUpdate != nil {
		su = s.opt.SelfUpdate()
	}
	body := HealthzOf(h, s.opt.Token != "", !s.opt.RequireTokenOnLoopback, buildinfo.Self(), su)
```

`internal/api/api.go` 의 `Options` 에 (`Fallback` 바로 위):

```go
	// SelfUpdate 는 자동 갱신 축의 현재 상태를 낸다. nil 이면 "배선 안 됨"으로 답한다.
	//
	// ★ 콜백인 이유: 이 값은 **계속 변한다.** 조립 시점의 스냅숏을 박으면
	// /healthz 가 영원히 기동 직후 상태를 낸다.
	SelfUpdate func() SelfUpdateStatus
```

`cmd/fd/client.go` 의 `healthzResponse` 에 (`Build` 아래):

```go
	SelfUpdate struct {
		Watching bool   `json:"watching"`
		Reason   string `json:"reason"`
		LastAt   string `json:"last_at"`
		From     string `json:"from"`
		To       string `json:"to"`
		Outcome  string `json:"outcome"`
		Detail   string `json:"detail"`
	} `json:"self_update"`
```

`cmd/fd/serve.go` 의 `api.Options` 에:

```go
		SelfUpdate: func() api.SelfUpdateStatus {
			st := watcher.Status()
			out := api.SelfUpdateStatus{
				Watching: st.Watching, Reason: st.Reason,
				From: st.From, To: st.To, Outcome: st.Outcome, Detail: st.Detail,
			}
			if !st.LastAt.IsZero() {
				at := st.LastAt
				out.LastAt = &at
			}
			return out
		},
```

`watcher` 를 `handler` 조립보다 **먼저** 만들어야 한다 — Task 4 의 배선 순서를 그렇게 옮긴다.

`internal/api/pure_test.go` 의 기존 `HealthzOf(...)` 호출에 `, SelfUpdateStatus{}` 를 더한다.

- [ ] **Step 4: 시험이 통과하는 것을 확인한다**

```
go test ./internal/api/ ./cmd/fd/ -v -run 'TestHealthz|TestSelfcheck|TestWatcher'
go test ./...
```

기대: 전부 PASS.

- [ ] **Step 5: 커밋**

```bash
gofmt -l plugins/flightdeck/server/cmd plugins/flightdeck/server/internal
git add plugins/flightdeck/server/internal/api plugins/flightdeck/server/cmd/fd/client.go plugins/flightdeck/server/cmd/fd/serve.go
git commit -m "$(cat <<'EOF'
feat(flightdeck): /healthz 가 자동 갱신 축을 낸다 — 거절이 조용히 묻히지 않게

Watching 을 맨 앞에 둔다. 감시기가 안 떴는데 나머지가 비어 있으면 "아직 갱신이
없었다"로 읽히는데, 그것은 "안 보고 있다"와 전혀 다르다. buildinfo.Coord 의 Known 이
같은 규율이고, 그 규율을 안 지켜 화면에 빈칸이 찍힌 적이 있다.

성공은 이 필드에 안 남는다. 성공하면 프로세스가 갈아치워져 새 프로세스는 그 사실을
모른다. 남는 것은 build 좌표가 바뀐 것뿐이고 그것으로 충분하다.

Options.SelfUpdate 를 콜백으로 받는다. 이 값은 계속 변하므로 조립 시점의 스냅숏을
박으면 /healthz 가 영원히 기동 직후 상태를 낸다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: `fd doctor` 가 자동 갱신 축을 찍는다

**Files:**
- Modify: `plugins/flightdeck/server/cmd/fd/render.go`
- Test: `plugins/flightdeck/server/cmd/fd/render_selfupdate_test.go` (신규)

**Interfaces:**
- Consumes: Task 5 의 `healthzResponse.SelfUpdate`
- Produces: `func SelfUpdateLines(su ...) []string` — 순수 함수, `RenderHealth` 가 부른다

- [ ] **Step 1: 실패하는 시험을 쓴다**

`cmd/fd/render_selfupdate_test.go`:

```go
package main

import (
	"strings"
	"testing"
)

func TestRenderHealthShowsRefusal(t *testing.T) {
	var h healthzResponse
	h.OK, h.APIVersion, h.DBOK = true, "1", true
	h.SelfUpdate.Watching = true
	h.SelfUpdate.LastAt = "2026-08-05T00:31:02Z"
	h.SelfUpdate.From, h.SelfUpdate.To = "07e5df4", "1d044b2"
	h.SelfUpdate.Outcome = "refused"
	h.SelfUpdate.Detail = "selfcheck exit 1 — 증분 계획이 거절된다"

	got := RenderHealth(h, true, "http://x:7420")
	for _, want := range []string{"자동 갱신", "거절", "07e5df4", "1d044b2", "selfcheck"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q 가 화면에 없다:\n%s", want, got)
		}
	}
}

// ★ 안 보고 있는 서버는 그 사실을 말해야 한다. 침묵하면 '따라오고 있다'로 읽힌다.
func TestRenderHealthSaysNotWatching(t *testing.T) {
	var h healthzResponse
	h.OK, h.APIVersion, h.DBOK = true, "1", true
	h.SelfUpdate.Watching = false
	h.SelfUpdate.Reason = "이 서버는 컨테이너다 — 자기 이미지를 다시 만들 수 없다"

	got := RenderHealth(h, true, "http://x:7420")
	if !strings.Contains(got, "자동 갱신") || !strings.Contains(got, "컨테이너") {
		t.Fatalf("안 보고 있다는 사실이 화면에 없다:\n%s", got)
	}
}

// 정상일 때는 한 줄만 — 배경이 된 경고는 안 읽힌다.
func TestRenderHealthIsQuietWhenWatchingAndNothingHappened(t *testing.T) {
	var h healthzResponse
	h.OK, h.APIVersion, h.DBOK = true, "1", true
	h.SelfUpdate.Watching = true

	got := RenderHealth(h, true, "http://x:7420")
	if strings.Contains(got, "거절") || strings.Contains(got, "실패") {
		t.Fatalf("아무 일도 없었는데 경고가 있다:\n%s", got)
	}
	if !strings.Contains(got, "자동 갱신  보는 중") {
		t.Fatalf("감시 중이라는 사실이 없다:\n%s", got)
	}
}
```

- [ ] **Step 2: 시험이 실패하는 것을 확인한다**

```
go test ./cmd/fd/ -run TestRenderHealth -v
```

기대: `h.SelfUpdate` 는 Task 5 에서 생겼으므로 컴파일은 되고, **단언이 실패**한다("자동 갱신" 문자열 없음).

- [ ] **Step 3: 최소 구현**

`cmd/fd/render.go` — `서버 판` 줄 **바로 아래**에 넣는다:

```go
	for _, line := range selfUpdateLines(h) {
		fmt.Fprintf(&b, "\n    %s", line)
	}
```

같은 파일 아래쪽에 순수 함수를 더한다:

```go
// selfUpdateLines 는 자동 갱신 축을 사람이 읽을 줄로 옮긴다. 순수 함수다.
//
// ★ **안 보고 있다는 사실을 침묵으로 두지 않는다.** 컨테이너·비유닉스·감시기 기동 실패는
// 전부 "이 서버는 자기를 안 따라간다"이고, 그 상태에서 아무 줄도 안 내면
// 읽는 쪽은 따라오고 있다고 믿는다(설계 §13).
func selfUpdateLines(h healthzResponse) []string {
	su := h.SelfUpdate
	if !su.Watching {
		reason := strings.TrimSpace(su.Reason)
		if reason == "" {
			reason = "사유를 안 냈다 — 이 축을 알리기 전 판일 수 있다"
		}
		return []string{"자동 갱신  **안 본다** — " + clip(reason, 300)}
	}
	if su.Outcome == "" {
		return []string{"자동 갱신  보는 중 — 아직 교체를 못 봤다"}
	}
	label := map[string]string{"refused": "**거절**", "failed": "**실패**"}[su.Outcome]
	if label == "" {
		label = clip(su.Outcome, 40)
	}
	head := "자동 갱신  " + label
	if strings.TrimSpace(su.LastAt) != "" {
		head += " (" + clip(su.LastAt, 40) + ")"
	}
	lines := []string{head}
	if su.From != "" || su.To != "" {
		lines = append(lines, "  "+clip(su.From, 80)+" → "+clip(su.To, 80))
	}
	if strings.TrimSpace(su.Detail) != "" {
		lines = append(lines, "  "+clip(su.Detail, 400))
	}
	return lines
}
```

- [ ] **Step 4: 시험이 통과하는 것을 확인한다**

```
go test ./cmd/fd/ -run TestRenderHealth -v
go vet ./... && gofmt -l ./cmd ./internal
go test -race ./...
GOOS=windows GOARCH=amd64 go vet ./cmd/fd/ && GOOS=darwin GOARCH=arm64 go vet ./cmd/fd/
GOOS=windows GOARCH=amd64 go build ./... && GOOS=darwin GOARCH=arm64 go build ./...
```

기대: 3건 PASS · vet·gofmt 무출력 · 전체 스위트 초록 · 교차 빌드 통과.

- [ ] **Step 5: 실물로 한 번 확인한다 (시험이 못 보는 축)**

**공유 서버 `:7420` 은 건드리지 마라.** 격리 인스턴스로만 한다.

```bash
cd plugins/flightdeck/server
SCR=$(mktemp -d)
go build -o "$SCR/fd" ./cmd/fd
mkdir -p "$SCR/state"
nohup "$SCR/fd" serve --addr 127.0.0.1:7461 --db "$SCR/fd.db" > "$SCR/serve.log" 2>&1 &
sleep 3

# (1) 감시 중이라고 말하는가
FD_URL=http://127.0.0.1:7461 FD_STATE_DIR="$SCR/state" FD_PROJECT=probe "$SCR/fd" doctor | grep "자동 갱신"

# (2) 바이너리를 교체하면 스스로 재기동하는가
BEFORE=$(curl -s http://127.0.0.1:7461/healthz | grep -o '"revision":"[^"]*"')
touch cmd/fd/main.go && go build -o "$SCR/fd.new" ./cmd/fd && mv -f "$SCR/fd.new" "$SCR/fd"
sleep 40   # 감시 주기 30초 + 여유
AFTER=$(curl -s http://127.0.0.1:7461/healthz | grep -o '"revision":"[^"]*"')
echo "before=$BEFORE after=$AFTER"
grep -E "실행 파일이 교체|검증 통과|자동 갱신" "$SCR/serve.log"

# 정리 — :7461 만 죽인다
kill $(ss -ltnp 'sport = :7461' | grep -o 'pid=[0-9]*' | head -1 | cut -d= -f2)
ss -ltn 'sport = :7420' | grep -q 7420 && echo ":7420 무사"
```

관측한 것을 그대로 적어라. **재기동이 안 일어났으면 안 일어났다고 적어라** — 그 관측이
시험이 못 보는 축의 유일한 증거다.

- [ ] **Step 6: 커밋**

```bash
git add plugins/flightdeck/server/cmd/fd/render.go plugins/flightdeck/server/cmd/fd/render_selfupdate_test.go
git commit -m "$(cat <<'EOF'
feat(flightdeck): fd doctor 가 자동 갱신 축을 찍는다

안 보고 있다는 사실을 침묵으로 두지 않는다. 컨테이너·비유닉스·감시기 기동 실패는
전부 "이 서버는 자기를 안 따라간다"이고, 그 상태에서 아무 줄도 안 내면 읽는 쪽은
따라오고 있다고 믿는다.

정상일 때는 한 줄만 낸다. 배경이 된 경고는 안 읽힌다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

## 마무리

- [ ] `fd finish fd-server-self-restart` 로 판단을 남기고 닫는다. 판단에 **Task 6 Step 5 의 실물 관측값**을 그대로 싣는다 — 관측 못 했으면 못 했다고 적는다.
- [ ] 후속으로 낼 것 둘:
  - **`fd-design-self-restart-contract`** — DESIGN.md 에 자동 갱신 축을 적는다. 이번에 안 한 이유는 그 파일을 6개 세션이 협상 중이라서다. `paths: [plugins/flightdeck/DESIGN.md]`
  - **`fd-selfwatch-interval-baseline`** — 30초에 근거가 없다. `fd-live-window-baseline`·`fd-prescribe-threshold-baseline` 과 같은 종류의 부채다. `paths: [plugins/flightdeck/server/cmd/fd/selfwatch.go]`

## 이 계획이 안 덮는 것

- **docker 경로의 자기 갱신.** 컨테이너는 자기 이미지를 다시 못 만든다. `fd doctor` 가 "안 본다"를 내는 것까지가 이 계획이다.
- **MCP 프로세스의 자기 재기동.** Claude Code 가 그 프로세스의 주인이다. 별개 항목이다.
- **진짜 `syscall.Exec`.** 주입된 스텁으로 "무엇을 부르려 했나"까지만 단언한다. 실물 확인은 Task 6 Step 5 다.
