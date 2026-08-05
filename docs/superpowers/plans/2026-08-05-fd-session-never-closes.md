# 세션을 닫는 경로 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 세션을 `done` 으로 내리는 경로를 만든다 — 사람이 부르는 `fd close` 와 `/clear` 때 도는 `SessionEnd` 훅. 그리고 그것이 살아 있는 세션을 죽이지 않도록 "열면 살아난다" 안전핀을 먼저 깐다.

**Architecture:** 서버·서비스·API 계층은 **안 고친다** — `PATCH /api/v1/sessions/{id}` 는 이미 있고 부르는 클라이언트만 없었다. 변경은 `internal/store/session.go` 한 자리(안전핀), `cmd/fd` 넷(클라이언트·명령·훅·디스패치), 문서 둘이다. 스키마 변경 없음, 마이그레이션 없음.

**Tech Stack:** Go 1.x · SQLite · `net/http` · 표준 `testing`

## Global Constraints

- **스키마를 안 바꾼다.** `schema.sql` 도 `internal/store/migrations/` 도 건드리지 않는다 — 증분 004 는 다른 세션이 쓰는 중이다.
- **`internal/service`·`internal/api`·`internal/web`·`internal/mcpsrv` 를 안 건드린다.** 겹침이 그쪽에 몰려 있고, 이 항목은 그쪽 변경 없이 성립한다.
- **훅은 절대 세션을 막지 않는다.** `cmd/fd/hook.go` 의 `runHook` 은 항상 종료코드 0이다. 새 갈래도 같다.
- **죽음을 판정하지 않는다.** 이 계획의 어떤 코드도 무응답·pid·나이에서 죽음을 추론하지 않는다. 닫기는 **관측된 사실**을 적는 것뿐이다.
- 검증 관문은 `go vet ./...` 과 `go test ./...` 둘 다다 — `go build` 는 `_test.go` 를 건너뛴다.
- 작업 디렉토리: `plugins/flightdeck/server` (Go 모듈 루트). 문서는 `plugins/flightdeck/`.
- 커밋 메시지는 한글, 본문에 **왜**를 적는다.

---

### Task 1: 안전핀 — `OpenSession` 이 `done` 카드를 되살린다

이것이 먼저다. 이게 없으면 Task 4 가 살아 있는 세션을 죽인다.

**Files:**
- Modify: `plugins/flightdeck/server/internal/store/session.go` (`Tx.OpenSession` 의 `case err == nil:` 갈래)
- Test: `plugins/flightdeck/server/internal/store/session_revive_test.go` (신규)

**Interfaces:**
- Consumes: `newStore(t)`, `seed(t, s, "p")` — 같은 패키지의 기존 시험 헬퍼
- Produces: `Tx.OpenSession` 의 거동 변경뿐. 시그니처는 그대로
  `func (t *Tx) OpenSession(project, machineID, worktree, ccSessionID, label string) (model.Session, bool, error)`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`plugins/flightdeck/server/internal/store/session_revive_test.go`:

```go
package store

import (
	"context"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 닫힌 카드를 같은 3중키로 다시 열면 살아나야 한다.
//
// ★ 이 시험이 지키는 것은 "죽음을 판정하지 않는다"의 반쪽이다. 닫기는 관측이라 넣지만,
// 그 관측이 틀렸다는 것을 **다음 관측이 뒤집을 수 있어야** 한다. 되살리지 않으면
// /clear 에서 닫힌 카드가 rekey 로 이어진 뒤에도 done 인 채 남아, 살아서 일하는
// 세션이 보드에서 사라진다 — 이 저장소가 이미 겪은 사고다.
func TestOpenSessionRevivesDoneCard(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()

	first, _, err := s.OpenSession(ctx, "p", "m1", "/w/t", "cc-A", "")
	if err != nil {
		t.Fatalf("첫 등록 실패: %v", err)
	}
	if err := s.SetSessionState(ctx, first.ID, model.SessionDone, ""); err != nil {
		t.Fatalf("닫기 실패: %v", err)
	}

	again, created, err := s.OpenSession(ctx, "p", "m1", "/w/t", "cc-A", "")
	if err != nil {
		t.Fatalf("재등록 실패: %v", err)
	}
	if created {
		t.Fatal("같은 3중키인데 새 카드를 만들었다 — 선점이 고아가 된다")
	}
	if again.ID != first.ID {
		t.Fatalf("다른 카드다: %q vs %q", again.ID, first.ID)
	}
	if again.State != model.SessionActive {
		t.Fatalf("닫힌 카드를 다시 열었는데 state=%q 다 — 살아서 일하는 세션이 보드에서 사라진다", again.State)
	}
}

// blocked 는 사람이 사유와 함께 남긴 것이다. 여는 것이 그 사유를 지우면 안 된다.
//
// ★ 되살리기를 "state 를 무조건 active 로" 로 쓰면 이 시험이 잡는다. blocked 가 조용히
// 풀리면 막힘을 낸 세션의 판단이 화면에서 사라지고, 아무도 그것이 사라진 줄 모른다.
func TestOpenSessionKeepsBlockedStateAndReason(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()

	first, _, err := s.OpenSession(ctx, "p", "m1", "/w/t", "cc-B", "")
	if err != nil {
		t.Fatalf("첫 등록 실패: %v", err)
	}
	if err := s.SetSessionState(ctx, first.ID, model.SessionBlocked, "레인이 물렸다"); err != nil {
		t.Fatalf("막힘 표시 실패: %v", err)
	}

	again, _, err := s.OpenSession(ctx, "p", "m1", "/w/t", "cc-B", "")
	if err != nil {
		t.Fatalf("재등록 실패: %v", err)
	}
	if again.State != model.SessionBlocked {
		t.Fatalf("blocked 가 풀렸다: state=%q — 막힘은 사람이 낸 판단이라 여는 것이 못 지운다", again.State)
	}
	if again.BlockedWhy != "레인이 물렸다" {
		t.Fatalf("막힘 사유가 사라졌다: %q", again.BlockedWhy)
	}
}
```

- [ ] **Step 2: 실패를 확인한다**

```
cd plugins/flightdeck/server && go test ./internal/store/ -run 'TestOpenSessionRevivesDoneCard|TestOpenSessionKeepsBlockedStateAndReason' -v
```
기대: `TestOpenSessionRevivesDoneCard` 가 `state="done"` 으로 FAIL. 두 번째는 PASS(지금도 안 건드리니까) — **그래도 남긴다.** 이 시험은 Task 1 의 구현이 과녁을 넘어가는 것을 막는 회귀 방어다.

- [ ] **Step 3: 최소 구현**

`internal/store/session.go`, `Tx.OpenSession` 의 `case err == nil:` 갈래 — label 갱신 블록 **바로 아래**에 넣는다:

```go
	case err == nil:
		if label != "" && label != existing.Label {
			if _, err := t.tx.ExecContext(t.ctx,
				`UPDATE session SET label = ? WHERE id = ?`, label, existing.ID); err != nil {
				return model.Session{}, false, fmt.Errorf("세션 label 갱신 실패(id=%q): %w", existing.ID, err)
			}
			existing.Label = label
		}
		// ★ 닫힌 카드를 다시 열면 **살아난다.** 이 자리가 없으면 닫기를 넣는 순간
		// 살아서 일하는 세션이 보드에서 사라진다 — /clear 는 카드를 닫고 곧바로
		// 같은 카드를 rekey 로 이어받는데, 그때 state 가 done 이면 ListLive 가 그것을
		// 통째로 뺀다. 이 도구가 이미 두 번 겪은 오판(설계: "죽었다"를 만들지 않는다)이다.
		//
		// ★ **되살리기만 한다. 죽이지 않는다.** 그래서 done 일 때만 손댄다:
		// blocked 는 사람이 사유와 함께 남긴 판단이라 여는 것이 조용히 지우면 안 되고,
		// active 는 이미 맞다.
		if existing.State == model.SessionDone {
			if _, err := t.tx.ExecContext(t.ctx,
				`UPDATE session SET state = ? WHERE id = ?`,
				string(model.SessionActive), existing.ID); err != nil {
				return model.Session{}, false, fmt.Errorf("세션 되살리기 실패(id=%q): %w", existing.ID, err)
			}
			existing.State = model.SessionActive
		}
		return existing, false, nil
```

- [ ] **Step 4: 통과를 확인한다**

```
cd plugins/flightdeck/server && go test ./internal/store/ -run 'TestOpenSession' -v
```
기대: 되살리기·blocked 유지·3중키 기존 시험 전부 PASS.

- [ ] **Step 5: 커밋**

```bash
git add plugins/flightdeck/server/internal/store/session.go plugins/flightdeck/server/internal/store/session_revive_test.go
git commit -m "$(cat <<'EOF'
fix(flightdeck): 닫힌 카드를 다시 열면 살아나게 한다 — 닫기보다 이것이 먼저다

OpenSession 은 3중키로 기존 행을 찾으면 label 말고는 안 건드렸다. 그래서
done 카드는 그 세션이 계속 일해도 영원히 done 이었다.

이 구멍을 놔둔 채 닫기를 넣으면 /clear 가 카드를 닫고 rekey 로 이어받은
그 순간 살아 있는 세션이 ListLive 에서 통째로 빠진다 — 이 저장소가 이미
두 번 겪은 오판이다.

되살리기만 한다. blocked 는 사람이 사유와 함께 남긴 판단이라 안 건드린다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: `fd close` — 사람이 닫는다

**Files:**
- Modify: `plugins/flightdeck/server/cmd/fd/app.go` (`Rekey` 아래에 `CloseSession` 추가)
- Modify: `plugins/flightdeck/server/cmd/fd/cmds.go` (`cmdClose` 추가)
- Modify: `plugins/flightdeck/server/cmd/fd/main.go` (`case "close"` + 사용법 한 줄)
- Test: `plugins/flightdeck/server/cmd/fd/close_test.go` (신규)

**Interfaces:**
- Consumes: `a.OpenSession(ctx, cc, "") (service.SessionResult, bool, error)` — `res.Session.ID` 와 `res.Claims []string` 을 한 번에 준다. `a.ccSessionID(fromFlag) string`. `a.cli.do(ctx, method, path, body, key) ([]byte, *http.Response, error)`. `FreshKey(sessionID)`.
- Produces:
  - `func (a *App) CloseSession(ctx context.Context, sessionID, why string) (model.Session, error)`
  - `func (a *App) cmdClose(ctx context.Context, args []string, out io.Writer) int`
  - 서버 계약: `PATCH /api/v1/sessions/{id}` body `{"state":"done","why":"…"}` → `{"session":{…}}`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`plugins/flightdeck/server/cmd/fd/close_test.go`:

```go
package main

import (
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 선점이 없으면 닫는다. 그리고 **DB 가 실제로 done 이어야** 한다 —
// "보냈다"는 단정은 "저장됐다"를 말하지 못한다(harness_test.go 머리말).
func TestCloseSetsSessionDone(t *testing.T) {
	h := newHarness(t)
	code, out := h.run("", "open")
	if code != 0 {
		t.Fatalf("open 실패(%d): %s", code, out)
	}

	code, out = h.run("", "close", "--why", "핸드오프 끝")
	if code != 0 {
		t.Fatalf("close 실패(%d): %s", code, out)
	}

	sessions := h.liveSessions()
	if len(sessions) != 0 {
		t.Fatalf("닫았는데 살아 있는 세션이 %d건 남았다: %+v", len(sessions), sessions)
	}
}

// 선점이 남아 있으면 **거절한다.**
//
// ★ done 카드는 ListLive 에서 빠지고, 그러면 그 세션이 든 선점이 아무에게도 안 보인다 —
// 항목을 아무도 못 집는데 누가 잡았는지도 안 보이는 상태가 된다. 그래서 우회 플래그를
// 두지 않는다: 우회할 필드가 있으면 우회된다.
func TestCloseRefusesWhileHoldingClaims(t *testing.T) {
	h := newHarness(t)
	if code, out := h.run("", "add", "--id", "it-1", "--title", "제목", "--body", "본문"); code != 0 {
		t.Fatalf("add 실패(%d): %s", code, out)
	}
	if code, out := h.run("", "pick", "it-1"); code != 0 {
		t.Fatalf("pick 실패(%d): %s", code, out)
	}

	code, out := h.run("", "close")
	if code == 0 {
		t.Fatalf("선점을 든 채 닫혔다 — 그 선점이 보드에서 사라진다:\n%s", out)
	}
	if !strings.Contains(out, "it-1") {
		t.Errorf("무엇이 남았는지를 안 냈다 — 사유 없는 거절은 다음 사람이 못 푼다:\n%s", out)
	}
	if !strings.Contains(out, "fd finish") {
		t.Errorf("처방(fd finish)을 안 냈다:\n%s", out)
	}
	if len(h.liveSessions()) != 1 {
		t.Error("거절했는데 세션이 사라졌다")
	}
}

// 닫은 뒤에도 신호 하나면 살아난다 — Task 1 의 안전핀이 cmd 계층에서도 도는지 본다.
func TestClosedSessionRevivesOnNextBeat(t *testing.T) {
	h := newHarness(t)
	if code, out := h.run("", "open"); code != 0 {
		t.Fatalf("open 실패(%d): %s", code, out)
	}
	if code, out := h.run("", "close"); code != 0 {
		t.Fatalf("close 실패(%d): %s", code, out)
	}
	if code, out := h.run("", "beat", "--kind", "prompt"); code != 0 {
		t.Fatalf("beat 실패(%d): %s", code, out)
	}
	if got := len(h.liveSessions()); got != 1 {
		t.Fatalf("닫힌 세션이 신호를 냈는데 안 살아났다(살아 있는 세션 %d건) — 그 세션의 발자국을 아무도 못 본다", got)
	}
}

// liveSessions 는 서버가 실제로 들고 있는 살아 있는 세션이다.
func (h *harness) liveSessions() []model.SessionView {
	h.t.Helper()
	live, err := h.st.ListLive(ctxBG(), h.project, timeZero())
	if err != nil {
		h.t.Fatalf("살아 있는 세션 조회 실패: %v", err)
	}
	return live
}
```

`ctxBG()`·`timeZero()` 헬퍼가 없으면 같은 파일에 넣는다:

```go
func ctxBG() context.Context { return context.Background() }
func timeZero() time.Time    { return time.Time{} }
```
(`context`·`time` 임포트 추가)

- [ ] **Step 2: 실패를 확인한다**

```
cd plugins/flightdeck/server && go test ./cmd/fd/ -run 'TestClose|TestClosedSession' -v
```
기대: `close` 명령이 없어 FAIL(사용법이 나오고 종료코드 2).

- [ ] **Step 3: 클라이언트 메서드**

`cmd/fd/app.go`, `Rekey` 함수 **바로 아래**:

```go
// CloseSession 은 카드를 done 으로 내린다. **관측이지 판정이 아니다** —
// 사람이(또는 /clear 가) "이 세션은 끝났다"고 말해 준 것을 적는 것뿐이다.
//
// ★ a.cli.do 를 쓴다 — Rekey 와 같은 이유이고, 하나가 더 있다.
// 닫기는 **지금의 사실이지 나중에 재생할 사실이 아니다.** 오프라인 큐에 쌓아 두면
// 그 사이 되살아나 일하고 있는 세션을 나중에 다시 죽인다.
// 서버가 안 닿으면 그 사실을 그대로 올린다 — 조용히 성공한 척하지 않는다.
func (a *App) CloseSession(ctx context.Context, sessionID, why string) (model.Session, error) {
	raw, _, err := a.cli.do(ctx, "PATCH", "/api/v1/sessions/"+urlPath(sessionID),
		patchStateReq{State: string(model.SessionDone), Why: why}, FreshKey(a.cli.Session))
	if err != nil {
		return model.Session{}, err
	}
	var out struct {
		Session model.Session `json:"session"`
	}
	if uerr := json.Unmarshal(raw, &out); uerr != nil {
		return model.Session{}, fmt.Errorf("세션 닫기 응답 해석 실패: %w", uerr)
	}
	return out.Session, nil
}
```

요청 구조체는 `wire.go` 의 다른 요청들 옆에 둔다(`rekeyReq` 가 있는 자리):

```go
// patchStateReq 는 PATCH /api/v1/sessions/{id} 의 본문이다.
// 필드 이름은 internal/api 의 patchSessionRequest 와 **글자 그대로** 같아야 한다 —
// 어긋나면 서버가 조용히 0값을 받고 상태가 안 바뀐 채 200 이 돌아온다.
type patchStateReq struct {
	State string `json:"state"`
	Why   string `json:"why"`
}
```

- [ ] **Step 4: 명령**

`cmd/fd/cmds.go`, `cmdFinish` 아래:

```go
// cmdClose 는 이 세션을 닫는다.
//
// ★ 선점이 남아 있으면 거절한다. done 카드는 ListLive 에서 빠지고, 그러면 그 선점이
// 아무에게도 안 보인다 — 항목을 아무도 못 집는데 누가 잡았는지도 안 보이는 상태가 된다.
// 우회 플래그는 두지 않는다: 우회할 필드가 있으면 우회된다.
func (a *App) cmdClose(ctx context.Context, args []string, out io.Writer) int {
	fs := newFlagSet("close")
	why := fs.String("why", "", "닫는 사유(표시 전용)")
	session := fs.String("cc-session", "", "Claude Code 세션 id")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cc := a.ccSessionID(*session)
	if cc == "" {
		fmt.Fprintln(out, "CLAUDE_CODE_SESSION_ID 를 못 읽었다 — 그 탐지가 깨진 것이다(fd doctor 가 그 축을 잰다).")
		return 1
	}
	// 선점 목록은 이 응답에 실려 온다. 따로 묻지 않는다 — 두 번 물으면 그 사이가 창이다.
	res, _, err := a.OpenSession(ctx, cc, "")
	if err != nil {
		fmt.Fprintf(out, "세션 좌표를 못 얻어 닫지 못했다: %v\n", err)
		return 1
	}
	if len(res.Claims) > 0 {
		fmt.Fprintf(out, "안 닫았다 — 선점 %d건이 남아 있다: %s\n",
			len(res.Claims), strings.Join(res.Claims, ", "))
		fmt.Fprintln(out, "닫으면 이 선점이 보드에서 사라진다(닫힌 카드는 살아 있는 세션에서 빠진다).")
		fmt.Fprintln(out, "먼저 끝내라: fd finish <item-id> --body …")
		return 1
	}

	sess, err := a.CloseSession(ctx, res.Session.ID, *why)
	if err != nil {
		fmt.Fprintf(out, "닫지 못했다: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "close · 세션 %s 를 닫았다 [%s]\n", sess.ID, sess.State)
	fmt.Fprintln(out, "다음 프롬프트·도구·MCP 호출이 오면 이 카드는 다시 살아난다 — 닫기는 판정이 아니라 관측이다.")
	return 0
}
```

- [ ] **Step 5: 디스패치와 사용법**

`cmd/fd/main.go` — `case "finish":` 바로 아래:

```go
	case "close":
		return app.cmdClose(ctx, rest, stdout)
```

같은 파일 사용법 블록, `fd finish …` 줄 아래:

```
  fd close [--why …]                      이 세션을 닫는다. 선점이 남아 있으면 거절한다
```

- [ ] **Step 6: 통과를 확인한다**

```
cd plugins/flightdeck/server && go test ./cmd/fd/ -run 'TestClose|TestClosedSession' -v && go vet ./...
```
기대: 셋 다 PASS.

- [ ] **Step 7: 커밋**

```bash
git add plugins/flightdeck/server/cmd/fd/
git commit -m "$(cat <<'EOF'
feat(flightdeck): fd close — 세션을 닫는 첫 클라이언트

PATCH /api/v1/sessions/{id} 는 서버에 있었는데 부르는 자리가 없었다.
그래서 세션이 done 이 되는 경로가 하나도 없었고, 죽은 세션의 카드는
창 2시간으로만 사라졌다.

선점이 남아 있으면 거절한다. 닫힌 카드는 ListLive 에서 빠지므로 그 선점이
아무에게도 안 보이게 되기 때문이다 — 항목을 아무도 못 집는데 이유도 안
보이는 상태가 된다. 우회 플래그는 두지 않는다.

오프라인 큐에 안 쌓는다. 낡은 닫기를 나중에 재생하면 그 사이 되살아나
일하는 세션을 다시 죽인다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: `fd finish --close`

**Files:**
- Modify: `plugins/flightdeck/server/cmd/fd/cmds.go` (`cmdFinish` 의 플래그와 꼬리)
- Test: `plugins/flightdeck/server/cmd/fd/close_test.go` (Task 2 의 파일에 추가)

**Interfaces:**
- Consumes: Task 2 의 `a.CloseSession(ctx, sessionID, why)`
- Produces: `fd finish <item> --close` 의 거동. 새 함수 없음

- [ ] **Step 1: 실패하는 시험을 쓴다**

`close_test.go` 에 추가:

```go
// finish 는 **기본으로 세션을 안 닫는다.**
//
// ★ 항목 하나를 끝내도 세션은 다음 항목으로 갈 수 있다. 거기서 자동으로 닫으면
// 살아 있는 세션이 보드에서 사라지고, 그 사이 남들의 겹침 판정이 이 세션을 못 본다.
func TestFinishDoesNotCloseSessionByDefault(t *testing.T) {
	h := newHarness(t)
	if code, out := h.run("", "add", "--id", "it-1", "--title", "제목", "--body", "본문"); code != 0 {
		t.Fatalf("add 실패(%d): %s", code, out)
	}
	if code, out := h.run("", "pick", "it-1"); code != 0 {
		t.Fatalf("pick 실패(%d): %s", code, out)
	}
	if code, out := h.run("", "finish", "it-1", "--body", "왜 그렇게 했나"); code != 0 {
		t.Fatalf("finish 실패(%d): %s", code, out)
	}
	if got := len(h.liveSessions()); got != 1 {
		t.Fatalf("finish 가 세션을 닫았다(살아 있는 세션 %d건) — 다음 항목으로 갈 세션이 보드에서 사라진다", got)
	}
}

// --close 를 주면 항목을 끝내고 세션도 닫는다.
func TestFinishWithCloseClosesSession(t *testing.T) {
	h := newHarness(t)
	if code, out := h.run("", "add", "--id", "it-1", "--title", "제목", "--body", "본문"); code != 0 {
		t.Fatalf("add 실패(%d): %s", code, out)
	}
	if code, out := h.run("", "pick", "it-1"); code != 0 {
		t.Fatalf("pick 실패(%d): %s", code, out)
	}
	code, out := h.run("", "finish", "it-1", "--body", "왜 그렇게 했나", "--close")
	if code != 0 {
		t.Fatalf("finish --close 실패(%d): %s", code, out)
	}
	if got := len(h.liveSessions()); got != 0 {
		t.Fatalf("--close 인데 세션이 %d건 살아 있다", got)
	}
}
```

- [ ] **Step 2: 실패를 확인한다**

```
cd plugins/flightdeck/server && go test ./cmd/fd/ -run 'TestFinish.*Close' -v
```
기대: `TestFinishDoesNotCloseSessionByDefault` PASS(지금 거동), `TestFinishWithCloseClosesSession` 은 `flag provided but not defined: -close` 로 FAIL.

- [ ] **Step 3: 구현**

`cmdFinish` 의 플래그 선언에 한 줄 추가(`closeReason` 아래):

```go
	closeSession := fs.Bool("close", false, "항목을 끝낸 뒤 이 세션도 닫는다")
```

그리고 `fmt.Fprintln(out, mcpsrv.RenderFinish(fr))` **아래**, `return 0` 앞:

```go
	// ★ 호출 둘이다. 한 트랜잭션이 아니므로 **끝났는데 못 닫은 상태를 그대로 낸다** —
	// 둘 다 성공한 척하면 다음 사람이 보드에서 이 카드를 보고 "아직 일하는 중"으로 읽는다.
	if *closeSession {
		if _, cerr := a.CloseSession(ctx, sess, "finish --close"); cerr != nil {
			fmt.Fprintf(out, "\n항목은 끝났으나 세션을 못 닫았다: %v\n", cerr)
			fmt.Fprintln(out, "이 카드는 아직 살아 있는 세션으로 보인다. 다시 닫으려면: fd close")
			return 1
		}
		fmt.Fprintln(out, "\n그리고 이 세션을 닫았다. 다음 신호가 오면 다시 살아난다.")
	}
	return 0
```

- [ ] **Step 4: 통과를 확인한다**

```
cd plugins/flightdeck/server && go test ./cmd/fd/ -run 'TestFinish|TestClose' -v
```
기대: 전부 PASS.

- [ ] **Step 5: 커밋**

```bash
git add plugins/flightdeck/server/cmd/fd/cmds.go plugins/flightdeck/server/cmd/fd/close_test.go
git commit -m "$(cat <<'EOF'
feat(flightdeck): fd finish --close — 마무리가 세션까지 닫는다

기본은 안 닫는다. 항목 하나를 끝내도 세션은 다음 항목으로 갈 수 있고,
거기서 자동으로 닫으면 살아 있는 세션이 보드에서 사라져 남들의 겹침
판정이 이 세션을 못 본다.

호출이 둘이라 한 트랜잭션이 아니다. 끝났는데 못 닫으면 그 사실을 그대로
낸다 — 둘 다 성공한 척하면 다음 사람이 이 카드를 "아직 일하는 중"으로 읽는다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: `session-end` 훅 — `/clear` 가 닫는다

**Files:**
- Modify: `plugins/flightdeck/server/cmd/fd/hook.go` (`HookPayload` 필드 · `runHook` 갈래 · 새 함수 `hookSessionEnd`)
- Modify: `plugins/flightdeck/hooks/hooks.json` (`SessionEnd` 블록)
- Test: `plugins/flightdeck/server/cmd/fd/hook_session_end_test.go` (신규)

**Interfaces:**
- Consumes: Task 2 의 `a.CloseSession`. 기존 `a.OpenSession`, `a.ccSessionID`, `resolveProject`
- Produces: `func (a *App) hookSessionEnd(ctx context.Context, p HookPayload)` · `HookPayload.Reason`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`plugins/flightdeck/server/cmd/fd/hook_session_end_test.go`:

```go
package main

import (
	"encoding/json"
	"testing"
)

// reason=clear 면 그 cc 의 카드를 닫는다.
func TestSessionEndClearClosesCard(t *testing.T) {
	h := newHarness(t)
	if code, out := h.run("", "open"); code != 0 {
		t.Fatalf("open 실패(%d): %s", code, out)
	}

	if code, out := h.run(sessionEndPayload(t, "clear"), "hook", "session-end"); code != 0 {
		t.Fatalf("훅은 항상 0 이어야 한다(%d): %s", code, out)
	}
	if got := len(h.liveSessions()); got != 0 {
		t.Fatalf("/clear 인데 카드가 %d건 살아 있다 — 죽은 cc 의 고아가 남는다", got)
	}
}

// ★ matcher 를 못 믿는 경우의 이중 방어. hooks.json 의 matcher 가 바뀌거나 플랫폼이
// 다른 사유를 쏘기 시작하면, 이 갈래가 없을 때 살아 있는 세션이 조용히 닫힌다.
func TestSessionEndIgnoresEveryReasonButClear(t *testing.T) {
	for _, reason := range []string{"resume", "logout", "prompt_input_exit", "other", ""} {
		t.Run(reason, func(t *testing.T) {
			h := newHarness(t)
			if code, out := h.run("", "open"); code != 0 {
				t.Fatalf("open 실패(%d): %s", code, out)
			}
			if code, out := h.run(sessionEndPayload(t, reason), "hook", "session-end"); code != 0 {
				t.Fatalf("훅은 항상 0 이어야 한다(%d): %s", code, out)
			}
			if got := len(h.liveSessions()); got != 1 {
				t.Fatalf("reason=%q 인데 카드를 닫았다(살아 있는 세션 %d건)", reason, got)
			}
		})
	}
}

// 선점을 든 카드는 안 닫는다 — rekey 가 거절되면 그 선점이 통째로 안 보이게 된다.
func TestSessionEndKeepsCardHoldingClaims(t *testing.T) {
	h := newHarness(t)
	if code, out := h.run("", "add", "--id", "it-1", "--title", "제목", "--body", "본문"); code != 0 {
		t.Fatalf("add 실패(%d): %s", code, out)
	}
	if code, out := h.run("", "pick", "it-1"); code != 0 {
		t.Fatalf("pick 실패(%d): %s", code, out)
	}
	if code, out := h.run(sessionEndPayload(t, "clear"), "hook", "session-end"); code != 0 {
		t.Fatalf("훅은 항상 0 이어야 한다(%d): %s", code, out)
	}
	if got := len(h.liveSessions()); got != 1 {
		t.Fatalf("선점을 든 카드를 닫았다 — 그 선점이 보드에서 사라진다(살아 있는 세션 %d건)", got)
	}
}

// 페이로드가 깨져도 종료코드는 0 이다. 훅이 세션을 막으면 안 된다.
func TestSessionEndNeverBlocksTheSession(t *testing.T) {
	h := newHarness(t)
	if code, out := h.run("이건 JSON 이 아니다", "hook", "session-end"); code != 0 {
		t.Fatalf("깨진 페이로드에 종료코드 %d 를 냈다 — 훅이 세션을 막는다: %s", code, out)
	}
}

// sessionEndPayload 는 플랫폼이 주는 SessionEnd stdin 이다.
// 필드 이름은 설치본 2.1.222 의 zod 스키마와 같다: 기본 훅 필드 + hook_event_name + reason.
func sessionEndPayload(t *testing.T, reason string) string {
	t.Helper()
	buf, err := json.Marshal(map[string]any{
		"session_id":      "cc-session-uuid-1",
		"cwd":             ".",
		"hook_event_name": "SessionEnd",
		"reason":          reason,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(buf)
}
```

- [ ] **Step 2: 실패를 확인한다**

```
cd plugins/flightdeck/server && go test ./cmd/fd/ -run TestSessionEnd -v
```
기대: `TestSessionEndClearClosesCard` 가 카드 1건으로 FAIL(`session-end` 를 모르는 훅 이름으로 처리). 나머지 셋은 PASS — **회귀 방어라 남긴다.**

- [ ] **Step 3: 페이로드 필드**

`cmd/fd/hook.go` 의 `HookPayload`, `Trigger` 아래:

```go
	// Reason 은 SessionEnd 의 사유다. **clear 와 resume 말고는 아무도 안 쏜다** —
	// 설치본 2.1.221·2.1.222 를 뜯어 확인했다(executeSessionEndHooks 호출부가 둘뿐이다).
	// 그래서 이 훅은 프로세스 종료를 못 잡는다. 그 한계는 DESIGN.md 가 말한다.
	Reason string `json:"reason"`
```

- [ ] **Step 4: 디스패치**

`runHook` 의 `case "pre-compact":` 아래:

```go
	case "session-end":
		a.hookSessionEnd(ctx, p)
```

같은 `switch` 의 `default:` 오류 문구를 고친다:

```go
			"error", "session-start|user-prompt|post-tool|pre-compact|stop|session-end 중 하나여야 한다")
```

- [ ] **Step 5: 훅 본문**

`cmd/fd/hook.go` 파일 끝, `beatFromHook` 아래:

```go
// hookSessionEnd 는 /clear 로 떠나는 대화의 카드를 닫는다.
//
// ★ reason 은 **끝나는 이유**이지 시작하는 것의 이름이 아니다. 설치본 2.1.221·2.1.222 실측:
// executeSessionEndHooks 를 부르는 자리는 clear 와 resume 둘뿐이고, 프로세스 종료를 알리는
// 훅 이벤트는 31종 어디에도 없다. 그래서 이 훅으로는 창을 닫고 나간 세션을 못 잡는다.
//
// ★ **clear 만 본다.** hooks.json 의 matcher 가 이미 거르지만 여기서 한 번 더 본다 —
// matcher 가 바뀌거나 플랫폼이 다른 사유를 쏘기 시작한 날, 이 갈래가 없으면 살아 있는
// 세션이 조용히 닫힌다. resume 은 /fork 도 같은 사유로 오므로 일부러 뺐다.
//
// ★ 이것이 안전한 것은 store 의 "열면 살아난다"가 있기 때문이다. clear 직후 SessionStart 가
// 같은 카드를 rekey 로 이어받고 그 OpenSession 이 되살린다. 그 안전핀 없이 여기만 넣으면
// 살아서 일하는 세션이 보드에서 사라진다.
func (a *App) hookSessionEnd(ctx context.Context, p HookPayload) {
	if p.Reason != "clear" {
		a.log.Debug("session-end: clear 가 아니라 아무것도 안 한다", "reason", clip(p.Reason, 40))
		return
	}
	if strings.TrimSpace(p.CWD) != "" {
		a.proj = resolveProject(a.env, p.CWD)
	}
	cc := a.ccSessionID(p.SessionID)
	if cc == "" {
		a.log.Warn("session-end: 세션 id 를 못 읽어 카드를 못 닫았다")
		return
	}
	res, _, err := a.OpenSession(ctx, cc, "")
	if err != nil {
		a.log.Warn("session-end: 세션 좌표를 못 얻었다", "error", err.Error())
		return
	}
	// ★ 선점을 든 카드는 안 닫는다. rekey 가 거절되면 그 선점이 통째로 안 보이게 되고,
	// 그러면 항목을 아무도 못 집는데 누가 잡았는지도 안 보인다.
	if len(res.Claims) > 0 {
		a.log.Info("session-end: 선점이 남아 있어 카드를 안 닫는다",
			"session_id", clip(res.Session.ID, 64), "claims", len(res.Claims))
		return
	}
	if _, cerr := a.CloseSession(ctx, res.Session.ID, "/clear"); cerr != nil {
		a.log.Warn("session-end: 카드를 못 닫았다",
			"session_id", clip(res.Session.ID, 64), "error", cerr.Error())
	}
}
```

- [ ] **Step 6: 훅 등록**

`plugins/flightdeck/hooks/hooks.json` 의 `"Stop"` 블록 **뒤**에 더한다(JSON 이라 앞 블록 끝에 쉼표가 필요하다):

```json
    "SessionEnd": [
      {
        "matcher": "clear",
        "hooks": [
          {
            "type": "command",
            "command": "\"${CLAUDE_PLUGIN_ROOT}/bin/fd\" hook session-end",
            "async": true,
            "timeout": 5
          }
        ]
      }
    ]
```

- [ ] **Step 7: 통과를 확인한다**

```
cd plugins/flightdeck/server && go test ./cmd/fd/ -run TestSessionEnd -v
python3 -c "import json;h=json.load(open('../hooks/hooks.json'))['hooks'];print(list(h));assert h['SessionEnd'][0]['matcher']=='clear'"
```
기대: 시험 전부 PASS, 훅 목록에 `SessionEnd` 가 있고 matcher 가 `clear`.

- [ ] **Step 8: 커밋**

```bash
git add plugins/flightdeck/server/cmd/fd/hook.go plugins/flightdeck/server/cmd/fd/hook_session_end_test.go plugins/flightdeck/hooks/hooks.json
git commit -m "$(cat <<'EOF'
feat(flightdeck): /clear 가 떠나는 카드를 닫는다 — matcher 는 clear 만

SessionEnd 훅을 붙인다. 다만 이 훅으로 프로세스 종료를 잡을 수는 없다:
설치본 2.1.221·2.1.222 를 뜯어 보니 executeSessionEndHooks 를 부르는
자리가 clear 와 resume 둘뿐이고, 나머지 사유 넷은 열거값에만 있고 아무도
안 쏜다. 훅 이벤트 31종에도 프로세스 종료를 알리는 것이 없다.

resume 은 뺐다. /fork 도 같은 사유로 오고, 실효는 rekey 거절 갈래뿐인데
그건 clear 가 이미 덮는다.

reason 을 코드에서 한 번 더 본다. matcher 가 바뀌는 날 이 갈래가 없으면
살아 있는 세션이 조용히 닫힌다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: 화면이 한계를 말한다

**Files:**
- Modify: `plugins/flightdeck/DESIGN.md` (세션 수명 문단 추가)
- Modify: `plugins/flightdeck/skills/fd-handoff/SKILL.md` (마지막 단계 추가 · "안 하는 것" 한 줄 제거)
- Modify: `plugins/flightdeck/server/cmd/fd/main.go` 사용법은 Task 2 에서 이미 했다

**Interfaces:**
- Consumes: Task 2·4 의 최종 거동
- Produces: 문서뿐

- [ ] **Step 1: DESIGN.md 에 문단을 더한다**

세션 수명을 다루는 절 끝에 붙인다(절이 없으면 세션을 다루는 절 뒤에 새 소절로):

```markdown
### 세션은 어떻게 닫히나 — 그리고 무엇이 안 닫히나

세션이 `done` 이 되는 경로는 둘이다. **둘 다 관측이지 판정이 아니다.**

- `fd close` / `fd finish --close` — 사람이 "끝났다"고 말한 것
- `SessionEnd` 훅(matcher `clear`) — /clear 로 그 대화가 떠난 것

**닫는 것은 되돌릴 수 있다.** `Tx.OpenSession` 은 닫힌 카드를 다시 열면 `active` 로
되살린다. 그래서 /clear 직후 rekey 로 이어진 카드가 done 인 채 사라지지 않는다.
`blocked` 는 안 건드린다 — 사람이 사유와 함께 남긴 판단이라 여는 것이 지우면 안 된다.

**★ 안 닫히는 것을 반드시 알아야 한다.** `SessionEnd` 는 `clear` 와 `resume` 에서만 온다
(설치본 2.1.221·2.1.222 실측: `executeSessionEndHooks` 호출부가 그 둘뿐이고,
`logout`·`prompt_input_exit`·`other`·`bypass_permissions_disabled` 는 zod 열거값에만 있고
아무도 안 쏜다). 훅 이벤트 31종에도 프로세스 종료를 알리는 것이 없다.

그래서 **창을 닫고 나간 세션·`tmux kill`·SIGKILL 은 이 경로로 안 닫힌다.**
그 카드는 여전히 창(기본 2시간)으로만 사라진다. 실측 하나: 2026-08-05 context-platform
보드가 카드 26장을 냈을 때 실제로 살아 있는 claude 프로세스는 5개였다.

이 한계를 안 적으면 다음 사람은 "닫히니까 카드는 항상 정확하다"고 믿는다. 그 믿음이
회수·회피 판단의 상류가 되면 이 도구가 이미 두 번 겪은 오판이 다시 난다.

선점을 든 세션은 **어느 경로로도 안 닫힌다.** 닫힌 카드는 `ListLive` 에서 빠지고, 그러면
그 선점이 아무에게도 안 보인다 — 항목을 아무도 못 집는데 누가 잡았는지도 안 보인다.
```

- [ ] **Step 2: fd-handoff 스킬을 고친다**

`plugins/flightdeck/skills/fd-handoff/SKILL.md`:

① "## 안 하는 것" 목록에서 이 줄을 **지운다**:

```
- 세션 해제 — 해제라는 개념이 없다(신호의 나이만 있다)
```

② "## 한 번에 끝낸다" 절 끝에 더한다:

```markdown
## 그리고 세션을 닫는다

이 세션에서 더 할 일이 없으면 마지막에 닫는다.

```
fd finish <항목> --body … --close     # 항목을 끝내고 세션도 닫는다
fd close                              # 항목 없이 세션만 닫을 때
```

**선점이 남아 있으면 거절한다.** 닫힌 카드는 살아 있는 세션에서 빠지므로 그 선점이
아무에게도 안 보이게 되기 때문이다. 먼저 `fd finish` 로 항목을 끝내라.

닫아도 **되돌릴 수 있다** — 다음 프롬프트·도구·MCP 호출이 오면 카드가 다시 살아난다.
닫기는 판정이 아니라 관측이다.

**닫아도 안 사라지는 것이 있다.** 창을 닫고 나가거나 `tmux kill` 로 죽은 세션은
이 경로를 안 지난다(플랫폼이 프로세스 종료를 알려 주지 않는다). 그 카드는 창으로만 사라진다.
```

- [ ] **Step 3: 확인**

```
cd plugins/flightdeck && grep -c "SIGKILL" DESIGN.md && grep -c "세션 해제" skills/fd-handoff/SKILL.md
```
기대: DESIGN.md 는 1 이상, SKILL.md 는 0(`grep -c` 가 0을 내면 종료코드 1이므로 `|| true` 를 붙여 확인).

- [ ] **Step 4: 전체 관문**

```
cd plugins/flightdeck/server && go vet ./... && go test ./...
```
기대: 전부 통과.

- [ ] **Step 5: 커밋**

```bash
git add plugins/flightdeck/DESIGN.md plugins/flightdeck/skills/fd-handoff/SKILL.md
git commit -m "$(cat <<'EOF'
docs(flightdeck): 세션이 어떻게 닫히나 — 그리고 무엇이 안 닫히나

닫는 경로가 생겼으니 그 한계를 같은 자리에 적는다. SessionEnd 는 clear 와
resume 에서만 오고, 프로세스 종료를 알리는 훅 이벤트는 없다. 그래서 창을
닫고 나간 세션은 여전히 창으로만 사라진다.

이 한계를 안 적으면 다음 사람은 "닫히니까 카드는 항상 정확하다"고 믿고,
그 믿음이 회수·회피 판단의 상류가 된다.

fd-handoff 의 "세션 해제 — 해제라는 개념이 없다" 한 줄은 이 항목이
뒤집었으므로 지운다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

## 자기 점검

**spec 대응:** §3 안전핀 → Task 1. §4 `fd close`·`--close`·선점 거절·fd-handoff → Task 2·3·5. §5 `/clear` 훅 → Task 4. §6 DESIGN.md → Task 5. §8 시험 11개 → Task 1(①②) · Task 2(③④ + 되살아남) · Task 3(⑤⑥) · Task 4(⑦⑧⑨⑩) · ⑪ 은 Task 4 의 `TestSessionEndClearClosesCard` + Task 2 의 `TestClosedSessionRevivesOnNextBeat` 가 양쪽 반씩 덮는다.

**빈칸:** 없다. 모든 단계에 실제 코드가 있다.

**이름 일관성:** `CloseSession`(app.go) · `cmdClose`(cmds.go) · `hookSessionEnd`(hook.go) · `patchStateReq`(wire.go) · `HookPayload.Reason` · `liveSessions()`(close_test.go, hook_session_end_test.go 가 함께 쓴다 — 같은 패키지라 한 번만 정의한다).
