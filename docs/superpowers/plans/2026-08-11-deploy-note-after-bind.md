# 배포 관측을 바인드 뒤로 — 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `fd serve` 가 **리스너를 실제로 연 뒤에만** `server.deploy` 를 원장에 남기게 한다.

**Architecture:** `api.Serve` 에서 `net.Listen` 을 `api.Listen` 으로 뽑아, 바인드 성공을 **콜백이 아니라 값**(`net.Listener`)으로 낸다. 호출부(`runServe`)는 `Listen → noteBuild → Serve` 순서가 되고, 바인드 실패는 원장에 닿기 전에 조기 반환한다.

**Tech Stack:** Go 1.x · `net/http` · SQLite(`internal/store`) · 표준 `testing`

**스펙:** `docs/superpowers/specs/2026-08-11-deploy-note-after-bind-design.md`

## Global Constraints

- 모듈 루트는 `plugins/flightdeck/server` 다. **모든 go 명령을 그 디렉토리에서 돌린다** — 밖에서 돌면 `gofmt -l` 이 빈 디렉토리를 검사하고 조용히 통과한다.
- 관문은 전부 "무출력이면 통과" 형태다. 매 커밋 전 다섯 줄을 다 돌린다:
  `gofmt -l .` · `go vet ./...` · `GOOS=windows GOARCH=amd64 go vet ./...` · `GOOS=darwin GOARCH=arm64 go vet ./...` · `go test ./internal/... ./cmd/fd/ -count=1`
- 교차 검증에 `go build` 를 쓰지 않는다 — `_test.go` 를 건너뛴다. 이 계획은 시험 파일 3개를 고치므로 특히 그렇다.
- `main` 에 직접 커밋하지 않는다. 작업 자리는 워크트리 `.flightdeck/worktrees/fd-deploy-note-precedes-bind` 다.
- 주석은 한글로, 이 저장소의 문체를 따른다 — **무엇을 했나가 아니라 왜 그렇게 했나**를 적는다.
- `DESIGN.md` 를 만지지 않는다(다른 세션이 576행을 쥐고 있다).

## 파일 구조

| 파일 | 책임 | 이 계획에서 |
|---|---|---|
| `internal/api/api.go` | REST 표면 · 리스너 수명 | `Listen` 신설(427행 위) · `Serve` 시그니처 변경 |
| `internal/api/listen_test.go` | **신설** — 바인드 성공/실패 계약 | `Listen` 이 값으로 답하는지 |
| `internal/api/serve_drain_test.go` | 드레인·셧다운 계약 | 호출 4곳 마이그레이션 · 헬퍼 주석 정정 |
| `cmd/fd/serve.go` | 배선과 **순서** | `runServe` 에 `Listen` · `serveWithWatcher` 가 `ln` 을 받음 · `noteBuild` 이동 |
| `cmd/fd/serve_test.go` | 감시기↔Serve 조합 | 호출 3곳 마이그레이션 |
| `cmd/fd/deploy_note_bind_test.go` | **신설** — 순서 회귀 | 바인드 실패 시 원장 무변경 |
| `internal/store/deploy.go` | 배포 원장 | 거짓이 된 ★ 주석 둘 정정 |

세 태스크로 가른다. **Task 1 은 배선 리팩터링**(서비스 동작은 그대로이지만 의도된 변화가 둘 있다 — 아래 Task 1 서술을 본다 — 기존 시험이 그물), **Task 2 가 실제 수정**(빨간불 → 초록), **Task 3 은 문서**. 이렇게 갈라야 리뷰어가 "배선이 맞나"와 "순서가 고쳐졌나"를 따로 기각할 수 있다.

---

### Task 1: `api.Listen` 을 뽑고 `Serve` 가 리스너를 받는다

**서비스 동작은 안 바뀐다.** 배선만 바꾸고, `noteBuild` 는 아직 원래 자리(`serve.go:216`)에 둔다.

의도된 변화는 **둘**이다. 둘 다 어느 기존 시험도 단정하지 않는 자리라 관문은 그대로
무출력·전부 초록이다:

1. `serveWithWatcher` 의 `serveErr` 갈래에서 `PortAdvice` 를 뗀다. 이 함수에 들어올 때는
   바인드가 이미 성공한 뒤이므로 그 갈래는 포트 선점이 아니라 리스너가 스스로 죽은
   것이다(포트 회수·fd 고갈) — 처방을 잘못 붙이면 사람이 엉뚱한 곳을 본다.
2. `api.Listen` 이 `go ledgerJob.Run(ctx)` 와 `log.Info("기동", ...)` 보다 **앞**으로
   온다. 바인드에 실패했는데 `"기동"` 이라고 찍는 것은 그 자체로 거짓이고, 곧바로 죽을
   프로세스가 백업 고루틴을 띄우는 것도 무의미하다.

**Files:**
- Modify: `plugins/flightdeck/server/internal/api/api.go:420-438`
- Modify: `plugins/flightdeck/server/cmd/fd/serve.go:228-242, 322-358`
- Create: `plugins/flightdeck/server/internal/api/listen_test.go`
- Test: `plugins/flightdeck/server/internal/api/serve_drain_test.go:22-40, 99-100, 201-202, 273-274`
- Test: `plugins/flightdeck/server/cmd/fd/serve_test.go:70, 119, 158`

**Interfaces:**
- Produces: `api.Listen(ctx context.Context, addr string, log *slog.Logger) (net.Listener, error)`
- Produces: `api.Serve(ctx context.Context, ln net.Listener, h Handler, log *slog.Logger) error`
- Produces: `serveWithWatcher(ctx context.Context, ln net.Listener, h api.Handler, log *slog.Logger, w *selfWatcher) int` (비공개, `cmd/fd`)

- [ ] **Step 1: 실패하는 시험을 쓴다 — `Listen` 이 바인드 실패를 값으로 낸다**

새 파일 `internal/api/listen_test.go`. 지금은 `Listen` 이 없어 **컴파일이 안 된다** — 그것이 이 시험의 빨간불이다.

★ 기존 `serve_drain_test.go` 에 얹지 않는다. 그쪽 헬퍼(`newEnv` · `syncBuffer`)를 안 쓰는 시험이라 import 를 스스로 선언하는 편이 깔끔하고, 이 시험은 **로그를 안 단정한다** — 바인드 성공/실패가 반환값에 실리는지만 본다.

```go
package api

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"testing"
)

// TestListenReportsBindFailure 는 바인드 성공/실패가 **값**임을 붙든다.
//
// ★ 이 시험이 가능해진 것 자체가 이 변경의 요지다. 앞선 판은 net.Listen 이 Serve 안에
// 묻혀 있어 "리스너가 열렸나"를 밖에서 물을 수 없었고, 그래서 배포 관측이 그 사실에
// 매달릴 수 없었다(cmd/fd 의 noteBuild).
func TestListenReportsBindFailure(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("자리를 못 잡았다: %v", err)
	}
	t.Cleanup(func() { busy.Close() })

	log := slog.New(slog.DiscardHandler)

	ln, err := Listen(context.Background(), busy.Addr().String(), log)
	if err == nil {
		ln.Close()
		t.Fatal("이미 물린 포트인데 Listen 이 성공했다 — 바인드 실패가 값으로 안 나온다")
	}
	if !strings.Contains(err.Error(), busy.Addr().String()) {
		t.Errorf("오류에 주소가 없다: %v — 호출부가 처방을 붙일 근거를 잃는다", err)
	}

	// 성공 갈래는 리스너를 실제로 준다.
	ok, err := Listen(context.Background(), "127.0.0.1:0", log)
	if err != nil {
		t.Fatalf("빈 포트인데 Listen 이 실패했다: %v", err)
	}
	t.Cleanup(func() { ok.Close() })
	if ok.Addr() == nil || ok.Addr().String() == "" {
		t.Error("리스너에 주소가 없다 — 호출부가 :0 의 실제 포트를 못 읽는다")
	}
}
```

- [ ] **Step 2: 돌려서 실패를 확인한다**

```bash
cd plugins/flightdeck/server && pwd
go test ./internal/api/ -run TestListenReportsBindFailure -count=1
```

Expected: FAIL — `undefined: Listen` (컴파일 오류)

- [ ] **Step 3: `api.Listen` 을 만들고 `Serve` 가 리스너를 받게 한다**

`internal/api/api.go` 의 `Serve` 독스트링(420-426행)과 본문 앞부분(427-438행)을 아래로 **교체**한다. `baseCtx, cutInflight := ...` 줄부터 아래는 손대지 않는다.

```go
// Listen 은 수신 주소를 연다. **바인드 성공을 값으로 낸다.**
//
// 포트 선점은 **사유를 남기고 종료한다**(설계 §7) — 조용히 실패하면
// 전 세션이 "서버 미도달"만 보고 원인을 모른다.
//
// ★ Serve 에서 뽑아낸 이유는 호출부가 "리스너가 실제로 열렸다"를 알아야 하기 때문이다.
// 배포 관측(cmd/fd 의 noteBuild)이 정확히 그 사실에 매달린다 — 뜨지도 못한 기동이 원장에
// 배포를 남기면 LastDeployAt 이 **한 번도 응답한 적 없는 바이너리**의 시각을 낸다.
//
// ★ **ready 콜백이 아니라 값이다.** 콜백을 받으면 시험이 넘길 수 있는 것이 nil 뿐이라
// 콜백 안이 안 잠긴다 — 이 저장소가 자기 갱신 축에서 이미 치른 값이다(2026-08-07 실측,
// cmd/fd 의 serveAPIOptions 주석). 값으로 내면 순서가 호출부에서 자명해지고, 그 순서를
// 어기는 것은 시험이 잡는다(cmd/fd 의 TestServeSkipsDeployNoteWhenBindFails).
//
// ★ ctx 를 받는 이유는 실패 로그가 ErrorContext 라서다 — 상관키를 잃지 않으려면 함께 온다.
func Listen(ctx context.Context, addr string, log *slog.Logger) (net.Listener, error) {
	if log == nil {
		log = slog.Default()
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.ErrorContext(ctx, "포트를 열 수 없다 — 이미 쓰고 있는 프로세스가 있는지 확인해라",
			"route", clip(addr, 120), "error", err.Error())
		return nil, fmt.Errorf("%s 를 열 수 없다: %w", addr, err)
	}
	return ln, nil
}

// Serve 는 핸들러를 **이미 열린 리스너**에 붙이고 ctx 가 끝날 때까지 돌린다.
//
// ★ 리스너 소유권을 가져간다 — srv.Serve 가 반환할 때 net/http 가 ln 을 닫으므로
// 호출부는 Listen 이 준 것을 넘긴 뒤 잊으면 된다. 정리 경로를 두 벌로 만들지 않는다.
//
// ★ h 가 http.Handler 가 아니라 Handler 인 이유는 Handler 의 독 코멘트에 있다.
func Serve(ctx context.Context, ln net.Listener, h Handler, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	// service.name 은 진입점이 이미 걸어 두었다 — 여기서 다시 걸면 한 줄에 두 번 찍힌다.

```

- [ ] **Step 4: 새 시험이 통과하는지 본다 (다른 곳은 아직 깨져 있다)**

```bash
cd plugins/flightdeck/server && pwd
go test ./internal/api/ -run TestListenReportsBindFailure -count=1
```

Expected: FAIL — 이번엔 `undefined` 가 아니라 **다른 시험들의 컴파일 오류**(`Serve` 호출 4곳이 여전히 문자열을 넘긴다). Step 5 에서 고친다.

- [ ] **Step 5: `internal/api` 시험 4곳을 마이그레이션한다**

`serve_drain_test.go` 의 99, 201, 273행은 같은 모양이다. **세 곳 모두** 아래처럼 바꾼다 — 바로 다음 줄의 `addr := serveAddrFromLog(t, e.logs)` 를 지우고 리스너에서 직접 읽는다.

```go
	ln, lerr := Listen(context.Background(), "127.0.0.1:0", e.srv.log)
	if lerr != nil {
		t.Fatalf("Listen: %v", lerr)
	}
	addr := ln.Addr().String()

	serveCtx, drain := context.WithCancel(context.Background())
	ret := make(chan error, 1)
	go func() { ret <- Serve(serveCtx, ln, h, e.srv.log) }()
```

325행(`TestServeShutdownLogsDrainMs`)은 **주소를 안 쓰고 동기화만 한다.** 거기서는 `serveAddrFromLog` 호출을 **그대로 남긴다** — Serve 가 실제로 떴음을 기다리는 것이 그 줄의 일이다:

```go
	ln, lerr := Listen(context.Background(), "127.0.0.1:0", e.srv.log)
	if lerr != nil {
		t.Fatalf("Listen: %v", lerr)
	}
	serveCtx, drain := context.WithCancel(context.Background())
	ret := make(chan error, 1)
	go func() { ret <- Serve(serveCtx, ln, h, e.srv.log) }()
	serveAddrFromLog(t, e.logs) // 주소가 아니라 "떴다"를 기다린다
```

그리고 헬퍼 주석(22-25행)을 정정한다 — 그 ★ 가 말하는 TOCTOU 는 **"잡았다 놓고 재사용"**의 것이라 리스너를 그대로 넘기는 지금 배선에는 없다. 오해를 남기지 않는다:

```go
// serveAddrFromLog 는 Serve 가 실제로 떴음을 로그로 기다린다.
//
// ★ 주소를 얻는 수단으로는 더 이상 안 쓴다 — 호출부가 Listen 이 준 ln.Addr() 를 직접
// 읽는다. 리스너를 **닫지 않고 그대로 넘기므로** 옛 주석이 걱정하던 TOCTOU("':0' 을
// 잡았다 놓고 주소만 재사용하면 그 사이 남이 잡는다")가 애초에 안 생긴다.
//
// 남은 쓰임은 하나뿐이다: 요청을 안 보내는 시험이 "서버가 떴다"를 기다리는 자리.
```

- [ ] **Step 6: `cmd/fd` 호출부를 맞춘다 — 순서는 아직 안 바꾼다**

`serve.go:322` 의 시그니처와 초입, 그리고 348·353-357행을 바꾼다:

```go
func serveWithWatcher(ctx context.Context, ln net.Listener, h api.Handler, log *slog.Logger, w *selfWatcher) int {
	// ★ 리스너가 닫힌 뒤에도 로그에 쓸 수 있도록 먼저 잡아 둔다.
	route := ln.Addr().String()
	watchCtx, stopWatch := context.WithCancel(context.Background())
```

348행:

```go
	serveErr := api.Serve(serveCtx, ln, h, log)
```

353-358행 — **`PortAdvice` 를 여기서 뗀다.** 바인드는 이미 성공했으므로 이 갈래는 포트 선점이 아니다(리스너가 스스로 죽은 것이다). 처방을 잘못 붙이면 사람이 엉뚱한 곳을 본다:

```go
	if serveErr != nil {
		// api.Serve 가 이미 원인 전문을 남겼다.
		// ★ 여기에 PortAdvice 를 안 붙인다 — 바인드는 이 함수에 들어오기 전에 이미
		// 성공했다. 이 갈래는 포트 선점이 아니라 리스너가 스스로 죽은 것이고(포트 회수·
		// fd 고갈), 거기에 "ss -ltnp 로 점유자를 확인해라"를 붙이면 사람을 엉뚱한 데로 보낸다.
		log.Error("서버가 멈춰 내려간다", "route", clip(route, 120), "error", serveErr.Error())
		return 1
	}
```

366행의 종료 로그도 `route` 를 쓴다:

```go
	log.Info("종료", "route", clip(route, 120))
```

`runServe` 쪽(228-242행) — `handler` 조립 **뒤**, `ctx` 생성 뒤에 `Listen` 을 넣는다:

```go
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ★ 리스너를 조립 **뒤에** 연다. 열린 순간부터 backlog 가 쌓이므로 받을 준비가
	// 끝난 뒤 여는 것이 맞다.
	ln, lerr := api.Listen(ctx, *addr, log)
	if lerr != nil {
		// 처방은 여기 붙는다 — 바인드 실패를 아는 유일한 자리다.
		log.Error("서버를 띄우지 못했다", "route", clip(*addr, 120),
			"error", lerr.Error(), "reason", PortAdvice(*addr, lerr))
		return 1
	}

	go ledgerJob.Run(ctx)

	log.Info("기동", "route", clip(ln.Addr().String(), 120), "db_path", clip(path, 200),
		"api_version", service.APIVersion, "auth_required", token != "",
		"ledger_out", clip(ledgerJob.route, 200))

	return serveWithWatcher(ctx, ln, handler, log, watcher)
```

`serve.go` 의 import 에 `"net"` 을 더한다.

- [ ] **Step 7: `cmd/fd` 시험 3곳을 마이그레이션한다**

`serve_test.go` 의 70·119·158행이 `"127.0.0.1:0"` 을 넘긴다. 세 곳 모두 앞에 리스너를 연다. 70행(goroutine 안):

```go
	ln, lerr := api.Listen(context.Background(), "127.0.0.1:0", log)
	if lerr != nil {
		t.Fatalf("Listen: %v", lerr)
	}
	done := make(chan int, 1)
	go func() {
		done <- serveWithWatcher(context.Background(), ln, newDrainProbe(nil), log, w)
	}()
```

119행:

```go
	ln, lerr := api.Listen(context.Background(), "127.0.0.1:0", log)
	if lerr != nil {
		t.Fatalf("Listen: %v", lerr)
	}
	got := serveWithWatcher(context.Background(), ln, newDrainProbe(nil), log, w)
```

158행:

```go
	ln, lerr := api.Listen(context.Background(), "127.0.0.1:0", log)
	if lerr != nil {
		t.Fatalf("Listen: %v", lerr)
	}
	if got := serveWithWatcher(context.Background(), ln, probe, log, w); got != 0 {
```

- [ ] **Step 8: 관문 다섯 줄을 돌린다**

```bash
cd plugins/flightdeck/server && pwd
gofmt -l .
go vet ./...
GOOS=windows GOARCH=amd64 go vet ./...
GOOS=darwin GOARCH=arm64 go vet ./...
go test ./internal/... ./cmd/fd/ -count=1
```

Expected: `gofmt -l` 과 vet 셋은 무출력, `go test` 는 전부 `ok`. **서비스 동작을 안 바꿨으므로 기존 시험이 하나도 안 빨개져야 한다** — 빨개지면 배선이 어긋난 것이다.

- [ ] **Step 9: 커밋**

```bash
git add plugins/flightdeck/server/internal/api/api.go \
        plugins/flightdeck/server/internal/api/listen_test.go \
        plugins/flightdeck/server/internal/api/serve_drain_test.go \
        plugins/flightdeck/server/cmd/fd/serve.go \
        plugins/flightdeck/server/cmd/fd/serve_test.go
git commit -m "refactor(flightdeck): 바인드 성공을 값으로 낸다 — 콜백이면 시험이 nil 밖에 못 넘긴다

api.Listen 을 뽑아 Serve 가 열린 리스너를 받게 했다. 행동은 안 바뀐다.
ready 콜백을 기각한 근거는 2026-08-07 실측이다 — 콜백을 받으면 시험이 넘길 수
있는 것이 nil 뿐이라 콜백 안이 통째로 안 잠긴다(serveAPIOptions 주석).

부수로 PortAdvice 가 제 갈래에만 붙는다. serveWithWatcher 의 serveErr 는 바인드
실패가 아니라 리스너가 스스로 죽은 것인데, 지금까지 거기에도 포트 처방이 붙었다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 2: `noteBuild` 를 바인드 성공 뒤로 옮긴다

**이 태스크가 실제 수정이다.** 코드 변경은 한 줄 이동이고, 값은 그것을 잠그는 시험에 있다.

**Files:**
- Create: `plugins/flightdeck/server/cmd/fd/deploy_note_bind_test.go`
- Modify: `plugins/flightdeck/server/cmd/fd/serve.go:216` (삭제) · `Listen` 성공 직후(삽입)

**Interfaces:**
- Consumes: `api.Listen` (Task 1) · `runServe(args []string, env func(string) (string, bool), log *slog.Logger) int` · `store.Open(path) (*store.Store, error)` · `(*store.Store).ListEvents(ctx, kind string, since time.Time, limit int)` · `quietLogger() *slog.Logger` (`cmd/fd` 시험 헬퍼, `deploy_note_test.go` 가 이미 쓴다)

- [ ] **Step 1: 회귀 시험을 쓴다**

새 파일 `cmd/fd/deploy_note_bind_test.go`:

```go
package main

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/store"
)

// TestServeSkipsDeployNoteWhenBindFails 는 **순서**를 붙든다 — 뜨지도 못한 기동은
// 원장에 배포를 안 남긴다.
//
// ★ runServe 를 실물로 부른다. 이 성질은 어느 순수 함수에도 안 살고 오직 runServe 의
// 호출 순서에만 살기 때문이다 — 조각을 따로 부르는 시험은 그 순서를 통째로 못 본다.
// 바인드 실패 갈래는 즉시 반환하므로(감시기도 백업 잡도 안 뜬다) 실물로 돌릴 수 있다.
//
// ★ 재현은 실제로 났던 것이다: 컨테이너가 :7420 을 물고 도는데 사람이 README 의
// `go run ./cmd/fd serve` 를 치면, compose 가 ~/.flightdeck:/data 를 마운트하므로
// 그 임시 바이너리가 **컨테이너와 같은 원장**에 배포를 적고 죽는다.
func TestServeSkipsDeployNoteWhenBindFails(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("자리를 못 잡았다: %v", err)
	}
	t.Cleanup(func() { busy.Close() })

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "fd.db")
	// FD_LEDGER 를 시험 자리로 돌린다 — 사람 홈에 백업 디렉토리를 안 만든다.
	env := func(k string) (string, bool) {
		if k == "FD_LEDGER" {
			return filepath.Join(dir, "ledger"), true
		}
		return "", false
	}

	got := runServe([]string{"--addr", busy.Addr().String(), "--db", dbPath}, env, quietLogger())
	if got != 1 {
		t.Fatalf("이미 물린 포트인데 runServe 가 %d 를 냈다 — 바인드 실패가 종료코드에 안 나온다", got)
	}

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("DB 를 못 열었다: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	evs, err := st.ListEvents(context.Background(), "server.deploy", time.Time{}, 10)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(evs) != 0 {
		t.Fatalf("뜨지도 못한 기동이 배포를 %d건 남겼다 — LastDeployAt 이 한 번도 응답한 적 "+
			"없는 바이너리의 시각을 낸다", len(evs))
	}
}
```

- [ ] **Step 2: 돌려서 실패를 확인한다**

```bash
cd plugins/flightdeck/server && pwd
go test ./cmd/fd/ -run TestServeSkipsDeployNoteWhenBindFails -count=1 -v
```

Expected: FAIL — `뜨지도 못한 기동이 배포를 1건 남겼다 …`

**이 빨간불을 실제로 눈으로 확인한다.** 여기서 통과하면 시험이 성질을 안 재고 있는 것이다.

- [ ] **Step 3: `noteBuild` 를 옮긴다**

`serve.go:216` 의 줄을 **지운다**:

```go
	noteBuild(context.Background(), st, log)
```

Task 1 에서 넣은 `api.Listen` 성공 직후, `go ledgerJob.Run(ctx)` **앞**에 넣는다:

```go
	// ★ **바인드 성공 뒤다.** 리스너가 열리기 전에 적으면 포트를 이미 물린 기동도
	// 배포로 남고, 그러면 LastDeployAt 이 한 번도 응답한 적 없는 바이너리의 시각을 낸다.
	// 이 순서가 계약이라 시험이 실물로 잠근다(TestServeSkipsDeployNoteWhenBindFails).
	//
	// ★ ctx 가 아니라 Background 를 준다 — 관측은 신호 컨텍스트의 수명과 무관하고,
	// SIGTERM 이 방금 왔다고 배포 관측이 잘려서는 안 된다.
	noteBuild(context.Background(), st, log)

	go ledgerJob.Run(ctx)
```

- [ ] **Step 4: 초록불을 확인한다**

```bash
cd plugins/flightdeck/server && pwd
go test ./cmd/fd/ -run 'TestServeSkipsDeployNoteWhenBindFails|TestNoteBuildObservesRealBinaryOnce' -count=1 -v
```

Expected: 둘 다 PASS. 성공 갈래(`TestNoteBuildObservesRealBinaryOnce`)가 함께 초록이어야 한다 — 순서만 옮겼지 관측 자체는 안 건드렸다.

- [ ] **Step 5: 관문 다섯 줄**

```bash
cd plugins/flightdeck/server && pwd
gofmt -l .
go vet ./...
GOOS=windows GOARCH=amd64 go vet ./...
GOOS=darwin GOARCH=arm64 go vet ./...
go test ./internal/... ./cmd/fd/ -count=1
```

Expected: 무출력 셋 + 전부 `ok`.

- [ ] **Step 6: 커밋**

```bash
git add plugins/flightdeck/server/cmd/fd/serve.go \
        plugins/flightdeck/server/cmd/fd/deploy_note_bind_test.go
git commit -m "fix(flightdeck): 뜨지도 못한 기동은 배포를 안 남긴다 — 관측을 바인드 뒤로

한 줄 이동이고 값은 시험에 있다. runServe 를 이미 물린 포트로 실물로 불러
종료코드 1 과 원장 0행을 함께 단정한다 — 이 성질은 어느 순수 함수에도 안 살고
오직 호출 순서에만 살기 때문이다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 3: 거짓이 된 주석을 정정한다

고쳐진 한계를 계속 "못 고쳤다"고 적어 두면, 다음 사람이 없는 결함을 고치러 오거나 있는 경계를 못 본다.

**Files:**
- Modify: `plugins/flightdeck/server/internal/store/deploy.go:35-41, 71-74`
- Modify: `plugins/flightdeck/server/cmd/fd/serve.go:136-146`
- Modify: `docs/superpowers/specs/2026-08-11-deploy-note-after-bind-design.md`

**Interfaces:** 없음 — 문서만 바뀐다.

- [ ] **Step 1: `NoteServerBuild` 의 ★ 절을 다시 쓴다**

`store/deploy.go:35-41` 의 "★ **아직 못 가르는 것: 뜨지 못한 기동.**" 절 전체를 아래로 교체한다:

```go
// ★ **뜨지 못한 기동은 여기까지 안 온다.** 호출부(cmd/fd 의 runServe)가 api.Listen 이
// 성공한 **뒤에만** noteBuild 를 부른다 — 포트를 이미 물려 곧바로 죽는 기동은 원장에
// 닿기 전에 조기 반환한다(TestServeSkipsDeployNoteWhenBindFails 가 그 순서를 붙든다).
//
// ★ **남는 경계 하나: 바인드에 성공한 임시 기동.** 다른 포트로 띄운 `go run` 은 실제로
// 리스너를 열므로 이 정의 안에서 배포로 적힌다. 그것이 실제 오염인 이유는 compose 가
// `~/.flightdeck:/data` 를 마운트해 **호스트의 임시 기동과 컨테이너가 같은 DB** 를 열기
// 때문이다. 그러면 그 임시 정체가 마지막 배포로 남고, 다음 컨테이너 재기동(배포가 아닌)이
// exe 불일치로 배포를 또 만든다. 가르는 축을 정하는 것은 후속이다.
```

- [ ] **Step 2: `LastDeployAt` 독스트링에서 임시 단서를 뗀다**

`store/deploy.go:71-74`. "지금 도는 실행 파일이 자리 잡은 시각"이 이제 참이므로 그 문장은 그대로 두고, 아래 ★ 밑에 한 줄을 더한다:

```go
// LastDeployAt 은 지금 도는 실행 파일이 자리 잡은 시각이다.
//
// ★ **못 잼과 0 을 가른다.** 배포 기록이 없으면 ok=false 이고, 그때 영값 시각을 창의
// 시작으로 쓰면 "전 역사"가 조용히 창이 된다 — 호출부가 그 갈래를 반드시 다뤄야 한다.
//
// ★ 이 값은 **리스너가 열린 기동**의 시각이다(NoteServerBuild 의 ★). 앞선 판에서 이
// 독스트링이 거짓이던 기간이 있었고 — 바인드 전에 적혀 뜨지도 못한 바이너리의 시각이
// 실렸다 — 지금은 순서가 그것을 막는다.
```

- [ ] **Step 3: `noteBuild` 주석에 순서 계약을 적는다**

`cmd/fd/serve.go:136` 의 첫 줄 아래에 ★ 를 하나 더한다:

```go
// noteBuild 는 이 기동이 새 실행 파일인지를 원장에 남긴다. **기동을 안 막는다.**
//
// ★ **호출 자리가 계약이다 — api.Listen 이 성공한 뒤여야 한다.** 리스너가 열리기 전에
// 부르면 포트를 이미 물린 기동도 배포를 남기고, LastDeployAt 이 한 번도 응답한 적 없는
// 바이너리의 시각을 낸다. 이 함수 안에는 그것을 막을 수단이 없다(바인드를 모른다) —
// 그래서 runServe 의 순서를 시험이 직접 잠근다(deploy_note_bind_test.go).
```

- [ ] **Step 4: 스펙의 부수 소득 서술을 정정한다**

스펙 `### ① 표면` 절의 부수 소득 첫 항목이 `serveAddrFromLog` 가 **죽는다**고 적었는데 사실이 아니다. 요청을 안 보내는 시험 하나(`TestServeShutdownLogsDrainMs`)가 그것을 **동기화**로 쓴다. 아래로 교체한다:

```markdown
- `serve_drain_test.go:22-30` 의 `serveAddrFromLog` 가 **주소를 얻는 수단으로는 죽는다**.
  주소를 쓰는 시험 3곳이 `ln.Addr()` 를 직접 읽어 로그 파싱과 5초 폴링이 사라진다. 헬퍼
  자체는 남는다 — 요청을 안 보내는 시험 하나가 "서버가 떴다"를 기다리는 데 쓴다.
```

- [ ] **Step 5: 관문 다섯 줄**

주석만 고쳤어도 돌린다 — `gofmt` 가 주석 정렬을 본다.

```bash
cd plugins/flightdeck/server && pwd
gofmt -l .
go vet ./...
GOOS=windows GOARCH=amd64 go vet ./...
GOOS=darwin GOARCH=arm64 go vet ./...
go test ./internal/... ./cmd/fd/ -count=1
```

- [ ] **Step 6: 커밋**

```bash
git add plugins/flightdeck/server/internal/store/deploy.go \
        plugins/flightdeck/server/cmd/fd/serve.go \
        docs/superpowers/specs/2026-08-11-deploy-note-after-bind-design.md
git commit -m "docs(flightdeck): 고쳐진 한계를 못 고쳤다고 적어 두지 않는다

deploy.go 의 ★ 가 '아직 못 가르는 것: 뜨지 못한 기동'이라고 적고 있었는데 이제
갈린다. 대신 **남는 경계**를 적는다 — 다른 포트로 뜬 임시 go run 은 바인드에
성공하므로 여전히 배포로 적히고, ~/.flightdeck:/data 마운트 때문에 그것이
컨테이너의 원장이다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## 마무리 — 랜딩과 후속

세 태스크가 다 끝나면:

1. **후속을 등록한다.** `finish` 의 `followups` 에 싣는다 — 미리 `add` 하면 판단과의 FK 연결을 영영 못 산다. 후속 하나:
   > **임시 `go run` 이 공유 원장을 오염시킨다.** 바인드에 성공한 임시 기동은 배포로 적히고, `~/.flightdeck:/data` 마운트 때문에 그것이 컨테이너의 원장이다. 그 결과 다음 컨테이너 재기동이 배포로 잡힌다. 가르는 축(정본 주소? 실행 파일 자리?)과 그것이 과잉인지를 따로 판단해야 한다.
2. **판단을 남긴다** — 무엇을 기각했고(ready 콜백 · 정본 주소 · 첫 응답 · 닫기) 왜인지, 그리고 **실물로 못 해 본 것**: 컨테이너를 실제로 갱신해 `server.deploy` 가 한 행 생기는지는 이 브랜치에서 확인 못 한다.
3. **`land`** 로 랜딩한다.
