# 열림 비트가 mcp 신호로 위장하는 것을 끝낸다 — 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `service.OpenSession` 과 `service.SetState` 가 `mcp` 신호를 찍지 않게 해서, 화면의 `mcp` 가 "세션을 열었다"를 뜻하지 않게 만든다.

**Architecture:** 신호 종류를 더하지 않는다. `signal.kind` 열거와 `schema.sql` 의 CHECK 는 그대로 두고, **잘못된 종류로 찍던 두 호출을 지운다.** 그 결과 신호가 0건인 세션이 흔해지므로, 그 전제에 얹혀 있던 시험 픽스처가 신호를 **직접** 세우도록 먼저 고친다(그 수정은 변경 전에도 초록이라 독립 커밋이 된다). 마지막으로 사라진 코드를 근거로 삼던 산문 아홉 자리를 고친다.

**Tech Stack:** Go 1.x · SQLite(modernc.org/sqlite) · 표준 `testing`

## Global Constraints

- **랜딩 관문은 `go vet ./...` 0 + `go test ./... -count=1` 전 패키지 초록**이다. `-count=1` 을 빼지 마라 — 이 저장소는 `.md` 만 바꿨을 때 `cmd/fd` 가 `(cached)` 로 떠서 실패를 한 번 가린 전례가 있다.
- 작업 디렉토리는 `plugins/flightdeck/server` 다. 명시가 없으면 모든 경로가 그 기준이다.
- **`internal/store/schema.sql` 을 건드리지 않는다.** 그 파일은 `BaseSchemaVersion = 1` 을 만드는 정의이고, 증분이 그 위에 얹힌다(`store.go:48-54` 가 그 규약을 못박는다).
- **증분 마이그레이션을 만들지 않는다.** `SchemaVersion`·`//go:embed`·`migrations` 슬라이스 전부 무변경이다. 옛 `mcp` 행을 지우는 백필은 **범위 밖**이다 — 근거는 아래 "안 하는 것".
- **`internal/model/types.go` 에 `SignalKind` 상수를 더하지 않는다.** 종류를 더하려면 `schema.sql` 의 CHECK 를 바꿔야 하고, SQLite 는 그것을 테이블 재생성으로만 할 수 있으며, 재생성은 `store/schema_table_count_test.go` 의 `declaredTables`(같은 표를 두 자리에서 선언하면 `t.Fatalf`)와 `store/migrate_guard_test.go` 의 `destructiveOps`(`DROP TABLE`·`RENAME TO`·`INSERT … SELECT` 를 전부 잡는다) 양쪽에 걸린다.
- **`internal/service/finish.go:426` 의 `t.Beat(in.SessionID, model.SignalMCP, now)` 를 건드리지 않는다.** 대신 산문이 그 사실을 말한다(Task 4).
- **`activityKinds` 와 `signalOrder` 슬라이스의 원소를 바꾸지 않는다**(`web/format.go`·`mcpsrv/render.go`). 주석만 고친다. `mcp` 를 활동에 넣을지는 별개 판단이고, 그 주석 자신이 "늘리기 전에 먼저 물어라"라고 못박는다.
- 커밋 메시지: `type(flightdeck): 한글 제목 — 부연`. 끝에 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>` 을 붙인다.
- 태스크마다 커밋한다. 각 태스크는 끝난 시점에 전 패키지 초록이어야 한다.

## 안 하는 것 — 그리고 왜

- **`signal.kind` 에 `open` 을 더하지 않는다.** 항목 본문이 지시한 길인데 못 간다. `schema.sql` 은 버전 1 고정이고(`pick_eval` 에 `picked_with` 가 없다), SQLite 는 CHECK 를 `ALTER` 로 못 바꾸며, 재생성은 위 두 가드에 정면으로 걸린다.
- **옛 거짓 행 백필을 안 한다.** 원래 제안한 술어 `at = (SELECT opened_at …)` 는 **0행을 지운다.** 신호의 `at` 은 `service/session.go:119` 가 git 파생 **전에** 잡은 `s.now()` 이고 `opened_at` 은 그 뒤 `store/session.go:85` 의 `nowStamp()` 라, 마이크로초 고정폭(`timeLayout`)에서 절대 같아질 수 없다. 실측(운영 DB, mcp 223건): 정확 일치 **0건** · `at < opened_at` 34건(간격 2.47~11.38ms) · `at > opened_at` 189건. 게다가 PK 가 `(session_id, kind)` 이고 `Beat` 가 단조 upsert 라 **세션당 mcp 행은 하나**이므로 어떤 술어도 한 행 안의 거짓분과 진짜분을 못 쪼갠다. 그리고 어떤 `DELETE FROM` 이든 `migrate_guard_test.go` 의 `destructiveOps` 에 걸리는데 거기엔 예외 기제가 아예 없어서, 예외 자리 신설 + `DESIGN.md` §7 + `store.go:361-374` 개정이 딸려 온다. 거짓 행의 수명은 창(기본 2시간)이다 — 그 대가를 치를 값이 아니다.
- **`mcp` 를 `activityKinds` 에 넣지 않는다.** 뺄 근거가 바뀔 뿐 사라지지 않는다(Task 4 참조).

## 이 변경이 실제로 잃는 것 — 계획서가 먼저 말한다

지우는 `Beat` 는 `if created` **밖**에 있어 재개에서도 무조건 돌고 시각을 앞으로 밀었다. 그래서 이 한 줄이 `SessionStart`·`Stop`·`PreCompact`·`SessionEnd` 훅 4종과 세션 좌표를 여는 거의 모든 `fd` CLI 명령의 **하트비트**였다. 훅이 직접 `beat` 하는 것은 `UserPromptSubmit`(prompt)과 `PostToolUse`(tool, matcher `Edit|Write`)뿐이다.

삭제 뒤, 프롬프트도 편집도 MCP 도구 호출도 없이 2시간을 넘긴 세션은 `ListLive` 에서 빠진다. 그러면 보드 카드에서만 사라지는 것이 아니라 `prescribe.go` 의 `Others`(남의 겹침 판정 입력)에서도 빠진다 — `DESIGN.md:464-468` 이 "조용한 오탐이 아니라 조용한 미탐"이라고 경계한 그 방향이다.

이 대가를 받아들이는 근거: 그런 카드는 신호가 열림뿐이라 **지금도 아무 일을 안 했다는 것 말고는 말하는 바가 없었고**, 후속 항목 `fd-board-folds-open-only-cards` 가 정확히 그 카드를 접으려 한다. 다만 이 판단은 원장에 남겨야 한다 — Task 5 가 그것을 한다.

부수 효과 하나 더: `SetState` 의 비트를 지우면 `blocked`/`paused` 를 선언한 카드의 창이 "전이 시각"이 아니라 "개시 시각" 기준으로 되돌아간다. 다만 `blocked` 판단을 `note` 로 남기면 `finish.go:426` 이 여전히 mcp 를 찍으므로 실무에서는 대개 보전된다.

---

### Task 1: service 픽스처가 열림 비트 대신 자기 신호를 세운다

이 태스크의 편집은 **변경 전 코드에서도 초록**이다(신호가 하나 더 생길 뿐이다). 그래서 Task 3 과 분리해 먼저 랜딩한다 — 그러면 Task 3 의 diff 가 "코드 삭제"만 남아 리뷰가 축을 하나만 본다.

**Files:**
- Modify: `internal/service/landing_test.go:22-27` (`twoSessions`)
- Modify: `internal/service/board_test.go:244-254` (`TestBoardOldestOutsideOnlyCountsHiddenSessions` 의 `hidden` 픽스처)

**Interfaces:**
- Consumes: `Service.Beat(ctx, sessionID string, kind model.SignalKind, paths []string) error` — 이미 있다.
- Produces: 없음. 시험 파일만 만진다.

- [ ] **Step 1: `landing_test.go` 가 `model` 을 import 하는지 확인한다**

Run: `grep -n 'internal/model' internal/service/landing_test.go`

없으면 import 블록에 `"github.com/kweiza/flightdeck/internal/model"` 을 더한다(파일 머리의 다른 import 와 같은 그룹).

- [ ] **Step 2: `twoSessions` 가 신호를 직접 세우게 한다**

`internal/service/landing_test.go` 의 이 함수를

```go
func twoSessions(t *testing.T, s *Service) (a, b string) {
	t.Helper()
	dirA, dirB := tmpBase(t), tmpBase(t)
	return openSession(t, s, "p", dirA, dirA, "cc-A", "트랙A").Session.ID,
		openSession(t, s, "p", dirB, dirB, "cc-B", "트랙B").Session.ID
}
```

이렇게 바꾼다:

```go
func twoSessions(t *testing.T, s *Service) (a, b string) {
	t.Helper()
	dirA, dirB := tmpBase(t), tmpBase(t)
	idA := openSession(t, s, "p", dirA, dirA, "cc-A", "트랙A").Session.ID
	idB := openSession(t, s, "p", dirB, dirB, "cc-B", "트랙B").Session.ID

	// ★ 신호를 **명시적으로** 남긴다. 이 파일의 여러 시험이 "점유자의 마지막 신호
	// 나이"를 단정하는데, 그 값은 세션 열기가 찍던 mcp 비트에 얹혀 있었다.
	// 열기는 도구 호출이 아니므로 더는 신호가 아니다 — 픽스처가 재려는 축을
	// 픽스처가 직접 세운다. 신호를 안 세우고 단정을 nil 허용으로 낮추면
	// "나이를 못 재면 회수 판정을 사람이 할 수 없다"는 그 줄들의 존재 이유가 사라진다.
	for _, id := range []string{idA, idB} {
		if err := s.Beat(ctx(), id, model.SignalTool, nil); err != nil {
			t.Fatalf("픽스처 신호 실패: %v", err)
		}
	}
	return idA, idB
}
```

- [ ] **Step 3: `board_test.go` 의 숨은 세션에 신호를 심는다**

`internal/service/board_test.go` 에서 `숨은 세션 —` 주석으로 시작하는 블록의 **끝**, 즉

```go
	if _, err := st.DB().ExecContext(ctx(),
		`UPDATE signal SET at = ? WHERE session_id = ?`, hiddenAt, hidden.Session.ID); err != nil {
		t.Fatalf("신호 시각 되돌리기 실패: %v", err)
	}
```

바로 **뒤에** 이것을 더한다(위 `UPDATE` 는 이 태스크에서 지우지 않는다 — 지금은 그것이 열림 비트를 미는 일을 하고, Task 3 에서 0행이 되면 그때 지운다):

```go
	// ★ 신호를 **심는다**. 이 시험이 재는 것은 OldestOutside 이고 그 재료가 신호인데,
	// 그 신호가 지금까지는 세션 열기가 찍던 mcp 비트였다. 열기가 신호를 안 찍게 되면
	// 위 UPDATE 는 0행이 되고 이 세션의 신호는 0건이 된다 — 그러면 board.go 의
	// `if lastSeen.IsZero() { continue }` 가 걸려 OldestOutside 가 비고,
	// 이 시험은 "화면이 침묵한다"를 통과로 읽는다. 재는 축을 픽스처가 직접 세운다.
	if _, err := st.DB().ExecContext(ctx(),
		`INSERT INTO signal(session_id, kind, at) VALUES (?, 'prompt', ?)`,
		hidden.Session.ID, hiddenAt); err != nil {
		t.Fatalf("숨은 세션 신호 심기 실패: %v", err)
	}
```

- [ ] **Step 4: 변경 전 코드에서 초록임을 확인한다**

Run: `go test ./internal/service/ -count=1`
Expected: PASS. 이 태스크는 아직 아무 코드도 안 지웠으므로 **반드시** 초록이어야 한다. 빨간불이면 픽스처가 기존 축을 바꾼 것이니 되돌리고 다시 본다.

- [ ] **Step 5: 전 패키지가 여전히 초록임을 확인한다**

Run: `go vet ./... && go test ./... -count=1`
Expected: vet 무출력, 전 패키지 `ok`.

- [ ] **Step 6: 커밋**

```bash
git add internal/service/landing_test.go internal/service/board_test.go
git commit -m "$(cat <<'EOF'
test(flightdeck): 줄·보드 픽스처가 신호를 직접 세운다 — 열림 비트에 얹혀 있던 축을 되찾는다

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: 레인 패널 픽스처와 그 오표기를 바로잡는다

`lane_panel_test.go` 는 파일 머리에서 "분 단위 단정 둘이 거짓 초록이었다"를 고발하는데, **같은 종류의 오표기를 하나 더 들고 있다.** 남아 있는 `"7분 전"` 단정의 실패 문구가 그 값을 "대기자의 대기 경과"라고 부르는데, 실제로 레인 절에 찍히는 7분은 **대기자의 마지막 신호 나이**다(대기 경과 칸은 실시계와 픽스처 시계가 갈려 `미래 36분 (시계 어긋남)` 으로 찍힌다).

**Files:**
- Modify: `internal/web/lane_panel_test.go:63-68` (`newLaneFixture` 의 대기자 블록)
- Modify: `internal/web/lane_panel_test.go:117` (단정의 실패 문구)

**Interfaces:**
- Consumes: `service.Service.Beat` · `laneFixture` 의 `f.svc`, `clk` — 이미 있다.
- Produces: 없음.

> **개정 (Task 2 리뷰 뒤 · 커밋 `d8d88b0` 을 고친다).** 초판은 대기자 `Beat` 를 `Land` 직후(12:03)에 찍어 "최종 좌표 7분을 안 흔든다"를 노렸다. 리뷰가 그 결과를 짚었다 — 그러면 대기자 행의 **대기 경과와 신호 나이가 둘 다 7분**이 되어, 패널이 대기 경과를 신호 칸에 찍는 결함을 이 시험이 못 잡는다. 그것은 이 파일 머리의 `laneFixture` 계약("경과 셋을 **서로 다른 값**으로 벌려 둔다 — 같은 값이면 패널이 한 숫자를 세 칸에 찍어도 시험이 못 본다")이 명시적으로 경계하는 상황이고, 점유자 행에 대해서는 픽스처가 이미 막아 둔 것이다. 사람이 "발견이 지배한다"로 판정했다. 아래는 개정판 — 마지막 `clk.advance(4 * time.Minute)` 을 2+2로 쪼개고 그 사이에서 대기자를 찍는다.

- [ ] **Step 1: 대기자 신호를 점유자 신호 뒤·렌더 시점 앞으로 옮긴다**

`d8d88b0` 이 넣은 이 블록을 **통째로 지운다**(대기자 `Land` 직후에 있다):

```go
	// ★ 대기자의 신호도 픽스처가 세운다. 이 값(아래 최종 좌표의 7분)은 세션 열기가
	// 찍던 mcp 비트였는데, 열기는 도구 호출이 아니므로 더는 신호가 아니다.
	// 시계를 안 밀고 여기서 찍으므로 최종 좌표는 그대로 7분이다.
	if err := f.svc.Beat(ctx, waiter, model.SignalTool, nil); err != nil {
		t.Fatalf("대기자 신호 실패: %v", err)
	}
```

그리고 **점유자 `Beat` 블록 뒤, 최종 좌표 주석 앞**에 이것을 넣는다:

```go
	// ★ 대기자의 신호도 픽스처가 세운다. 예전에는 세션 열기가 찍던 mcp 비트가
	//   이 자리를 대신했는데, 열기는 도구 호출이 아니므로 더는 안 찍는다.
	//
	//   시각을 대기 경과(7분)와 **일부러 벌린다.** 같은 값이면 패널이 대기 경과를
	//   신호 칸에 그대로 찍어도 시험이 초록이다 — 바로 위에서 점유자 행에 대해
	//   막은 것과 같은 결함이 대기자 행에만 남는다.
	clk.advance(2 * time.Minute)
	if err := f.svc.Beat(ctx, waiter, model.SignalTool, nil); err != nil {
		t.Fatalf("대기자 신호 실패: %v", err)
	}
```

`model` import 는 이미 있다(파일 머리 `"github.com/kweiza/flightdeck/internal/model"`).

- [ ] **Step 2: 최종 좌표 주석과 마지막 시계 이동을 새 좌표에 맞춘다**

같은 함수 끝의 이것을

```go
	// 최종 좌표: 점유자 대기·획득 = 10분 · 점유자 신호 = 4분 · 대기자 대기·신호 = 7분
	//
	// ★ 신호 둘은 이제 **픽스처가 직접 찍은 값**이다(전에는 세션 열기의 mcp 비트가
	//   대기자 쪽을 대신했다). 대기 경과는 여전히 실시계라 화면에서 안 맞는다 —
	//   후속 `fd-lane-timestamps-ignore-injected-clock`.
	clk.advance(4 * time.Minute)
```

이렇게 바꾼다:

```go
	// 최종 좌표: 점유자 대기·획득 = 10분 · 점유자 신호 = 4분 · 대기자 대기 = 7분 · 대기자 신호 = 2분
	//
	// ★ 신호 둘은 **픽스처가 직접 찍은 값**이다(전에는 세션 열기의 mcp 비트가
	//   대기자 쪽을 대신했다). 넷이 전부 다른 값인 것이 이 픽스처의 계약이다.
	//   대기 경과는 여전히 실시계라 화면에서 안 맞는다 —
	//   후속 `fd-lane-timestamps-ignore-injected-clock`.
	clk.advance(2 * time.Minute)
```

**검산**(전부 이 값이어야 한다): `12:00` 점유자 open·Land → `+3` `12:03` 대기자 open·Land → `+3` `12:06` 점유자 Beat → `+2` `12:08` 대기자 Beat → `+2` `12:10` 렌더 시점. 점유자 대기·획득 10분 · 점유자 신호 4분 · 대기자 대기 7분 · 대기자 신호 2분.

- [ ] **Step 3: 실패 문구를 바로잡고 단정 값을 새 좌표에 맞춘다**

`TestLanePanelDrawsEveryRowWithItsAxes` 안에서 `d8d88b0` 이 남긴 이 줄을

```go
	mustContain(t, lane, "7분 전", "대기자의 마지막 신호 나이(7분)가 없다 — 대기 경과가 아니다(그 칸은 실시계라 시험에서 안 맞는다)")
```

이렇게 바꾼다:

```go
	mustContain(t, lane, "2분 전", "대기자의 마지막 신호 나이(2분)가 없다 — 대기 경과(7분)가 아니다. 그 둘이 같은 값이면 이 단정은 아무것도 안 지킨다")
```

★ 단정 문자열이 `"7분 전"` → `"2분 전"` 으로 바뀐다. 바로 위의 `"4분 전"`(점유자 신호) 단정과 아래의 `"10분 전"` 부재 검사는 **그대로 둔다** — 개정된 좌표에서도 참이다.

- [ ] **Step 4: 변경 전 코드에서 초록임을 확인한다**

Run: `go test ./internal/web/ -count=1 -run TestLanePanelDrawsEveryRowWithItsAxes -v`
Expected: PASS.

- [ ] **Step 5: 전 패키지가 여전히 초록임을 확인한다**

Run: `go vet ./... && go test ./... -count=1`
Expected: vet 무출력, 전 패키지 `ok`.

- [ ] **Step 6: 커밋**

```bash
git add internal/web/lane_panel_test.go
git commit -m "$(cat <<'EOF'
test(flightdeck): 레인 픽스처가 대기자 신호를 세우고, 신호 나이를 대기 경과라 부르던 문구를 고친다

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: 열림과 상태 전이가 더는 mcp 를 찍지 않는다

**Files:**
- Modify: `internal/service/session_test.go` (새 시험 추가 — 파일 끝)
- Modify: `internal/service/session.go:216-219` (`OpenSession` 의 Beat)
- Modify: `internal/service/session.go:447` (`SetState` 의 `now` 선언)
- Modify: `internal/service/session.go:459-462` (`SetState` 의 Beat)
- Modify: `internal/service/board_test.go` (Task 1 이 남겨 둔 죽은 `UPDATE` 제거)
- Modify: `internal/service/landing_test.go` (이 삭제가 폐기하는 주석 한 구절)

**Interfaces:**
- Consumes: `Store.Signals(ctx, sessionID) (map[model.SignalKind]time.Time, error)` · `Service.SetState(ctx, sessionID string, st model.SessionState, why string) error` — 둘 다 이미 있다.
- Produces: 없음. 시그니처는 하나도 안 바뀐다.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/service/session_test.go` 끝에 더한다:

```go
// 세션을 열거나 상태를 바꾸는 것은 **도구 호출이 아니다.**
//
// 그 둘이 mcp 를 찍던 동안 화면은 "mcp 0초"라고 내면서 실제로는 MCP 도구를 한 번도
// 안 부른 세션을 가리켰다 — 설계 §4 의 신호 표가 mcp 를 "도구 호출"로 정의하는데
// 그것과 어긋났다(실측: 카드 26장 중 16장이 mcp 하나뿐이었다).
//
// ★ **다른 종류로 옮겼는지가 아니라 안 찍는지를 본다.** 종류를 더하려면 schema.sql 의
// CHECK 를 바꿔야 하고, SQLite 에서 그것은 표 재생성이며, 재생성은 declaredTables 와
// destructiveOps 양쪽 가드에 걸린다. "언제 열렸나"는 session.opened_at 이 이미 담고,
// ListLive 의 창 판정도 그 컬럼을 따로 본다 — signal 표에 같은 사실을 두 벌 둘 이유가 없다.
func TestOpenSessionAndSetStateLeaveNoSignal(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	sess := openSession(t, s, "p", repo, repo, "cc-quiet", "조용")

	sig, err := st.Signals(ctx(), sess.Session.ID)
	if err != nil {
		t.Fatalf("신호 조회 실패: %v", err)
	}
	if len(sig) != 0 {
		t.Fatalf("세션을 열기만 했는데 신호가 %v 다 — 화면이 '이 세션이 도구를 불렀다'고 거짓말한다", sig)
	}

	if err := s.SetState(ctx(), sess.Session.ID, model.SessionBlocked, "막힌 사유"); err != nil {
		t.Fatalf("상태 변경 실패: %v", err)
	}
	sig, err = st.Signals(ctx(), sess.Session.ID)
	if err != nil {
		t.Fatalf("신호 조회 실패: %v", err)
	}
	if _, ok := sig[model.SignalMCP]; ok {
		t.Fatalf("상태를 바꿨더니 mcp 가 생겼다 — 상태 전이는 도구 호출이 아니다: %v", sig)
	}
}
```

- [ ] **Step 2: 시험이 실패하는지 본다**

Run: `go test ./internal/service/ -count=1 -run TestOpenSessionAndSetStateLeaveNoSignal -v`
Expected: FAIL — `세션을 열기만 했는데 신호가 map[mcp:...] 다` 로 첫 단정에서 죽는다.

- [ ] **Step 3: `OpenSession` 의 Beat 를 지운다**

`internal/service/session.go` 의 `OpenSession` 안, `AddWorkspace` 블록 바로 뒤에 있는 이 네 줄을 **통째로 지운다**:

```go
		// 세션이 열렸다는 것 자체가 신호다. 훅이 한 번도 안 불려도 "언제 열렸나"는 남는다.
		if err := t.Beat(sess.ID, model.SignalMCP, now); err != nil {
			return err
		}
```

`return nil` 과 그 위 `AddWorkspace` 블록은 그대로 둔다. **이 함수의 `now`(함수 머리 `now := s.now()`)는 지우지 마라** — `proj.CreatedAt`·`UpsertMachine.LastSeen`·`d.result(now)` 가 계속 쓴다.

- [ ] **Step 4: `SetState` 의 Beat 와 고아가 된 `now` 를 지운다**

같은 파일 `SetState` 안에서 이 네 줄을 지우고

```go
			// 상태를 바꾸는 것도 살아 있다는 사실이다.
			if err := t.Beat(sessionID, model.SignalMCP, now); err != nil {
				return err
			}
```

**같은 함수 머리의 이 줄도 함께 지운다:**

```go
	now := s.now()
```

지우지 않으면 `declared and not used: now` 로 `internal/service` 가 통째로 빌드에 실패한다 — 그러면 `api`·`web`·`mcpsrv`·`cmd/fd` 시험이 전부 빌드 실패로 죽는다. `SetState` 안에서 `now` 를 읽는 유일한 자리가 방금 지운 Beat 였다.

- [ ] **Step 5: 시험이 통과하는지 본다**

Run: `go test ./internal/service/ -count=1 -run TestOpenSessionAndSetStateLeaveNoSignal -v`
Expected: PASS.

- [ ] **Step 6: Task 1 이 남겨 둔 죽은 `UPDATE` 를 지운다**

`internal/service/board_test.go` 에서 이제 0행이 되는 이 블록을 지운다(바로 아래 `INSERT INTO signal` 이 그 일을 대신한다):

```go
	if _, err := st.DB().ExecContext(ctx(),
		`UPDATE signal SET at = ? WHERE session_id = ?`, hiddenAt, hidden.Session.ID); err != nil {
		t.Fatalf("신호 시각 되돌리기 실패: %v", err)
	}
```

그리고 `INSERT` 위 주석에서 미래형으로 적힌 문장을 현재형으로 고친다 — `열기가 신호를 안 찍게 되면 위 UPDATE 는 0행이 되고` → `열기가 신호를 안 찍으므로`, 그리고 `위 UPDATE 는 0행이 되고` 절을 지운다.

- [ ] **Step 7: 이 삭제가 폐기하는 주석 한 구절을 고친다**

`internal/service/landing_test.go` 의 `TestLaneReleaseJudgmentSaysWhenTheSignalCouldNotBeRead` 안, `countRows` 사전 조건 검사 **바로 위**에 이 주석이 있다:

```go
	// 신호 조회만 실패시킨다. 이 세션은 신호를 **실제로 남겼으므로**(세션 열기가 Beat 한다)
	// "없음"이 나오면 그것은 거짓이다.
```

`(세션 열기가 Beat 한다)` 는 방금 Step 3 이 지운 코드를 가리킨다. 그 사전 조건이 지금도 성립하는 이유는 Task 1 이 `twoSessions` 에 넣은 명시적 `Beat` 다. 괄호 안만 바꾼다:

```go
	// 신호 조회만 실패시킨다. 이 세션은 신호를 **실제로 남겼으므로**(twoSessions 가 Beat 한다)
	// "없음"이 나오면 그것은 거짓이다.
```

이 파일에서 이 한 구절 말고는 아무것도 안 건드린다. Task 1 이 만든 `twoSessions` 의 Beat 루프도 그대로 둔다.

- [ ] **Step 8: 전 패키지 관문을 통과하는지 본다**

Run: `go vet ./... && go test ./... -count=1`
Expected: vet 무출력, 전 패키지 `ok`.

빨간불이 나면 아래를 기대값으로 삼는다 — 조사에서 실측한 회귀는 **정확히 이 다섯이고 Task 1·2 가 전부 미리 막았다.** 그 밖의 실패는 새로운 것이니 멈추고 조사한다.

| 시험 | 막은 태스크 |
|---|---|
| `internal/service/landing_test.go:91` | Task 1 |
| `internal/service/landing_test.go:484` | Task 1 |
| `internal/service/landing_test.go:576` | Task 1 |
| `internal/service/board_test.go:263` | Task 1 |
| `internal/web/lane_panel_test.go:117` | Task 2 |

- [ ] **Step 9: 커밋**

```bash
git add internal/service/session.go internal/service/session_test.go \
        internal/service/board_test.go internal/service/landing_test.go
git commit -m "$(cat <<'EOF'
fix(flightdeck): 열림과 상태 전이가 mcp 를 안 찍는다 — 화면이 "도구를 불렀다"고 하던 거짓말을 끝낸다

세션을 여는 것도 상태를 바꾸는 것도 MCP 도구 호출이 아니다. "언제 열렸나"는
session.opened_at 이 이미 담고 ListLive 의 창 판정도 그 컬럼을 따로 본다.

신호 종류를 더하지 않는다 — schema.sql 의 CHECK 를 바꾸려면 표 재생성이 필요한데
declaredTables 와 destructiveOps 양쪽 가드에 걸린다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: 사라진 코드를 근거로 삼던 산문이 사실을 말한다

시험은 하나도 안 깨지는 자리들이다. **그래서 아무도 안 알려 준다** — 이 항목의 존재 이유가 "화면이 거짓말하지 않게"인 이상 같은 거짓을 주석에 남겨 두는 것은 앞뒤가 안 맞는다.

`mcp` 를 활동에서 계속 뺄 **새 근거**는 성립한다. 코드로 확인한 것 둘: ① `mcpsrv.go` 의 `callTool` 은 도구 이름을 안 가리고 dispatch **전에** `Beat` 하므로 읽기 전용 `board` 만 불러도 점등된다. ② `service.Note`(`finish.go:426`)도 mcp 를 찍는데, 그 문은 `POST /api/v1/judgments` 로 열려 있어 **PreCompact 훅의 자동 초안**과 CLI `fd note` 가 들어온다. 아래 문구는 그 둘을 근거로 쓴다.

**Files:**
- Modify: `internal/web/format.go:131-142`
- Modify: `internal/mcpsrv/render.go:136-142`
- Modify: `internal/web/activity_test.go:12-15` 와 `:48`
- Modify: `internal/mcpsrv/board_claim_filter_test.go:56-57`
- Modify: `internal/web/claim_filter_test.go:74`
- Modify: `internal/model/types.go:30-44`
- Modify: `../DESIGN.md:244` · `:275` · `:456-459`

**Interfaces:** 없음. 산문만 만진다.

- [ ] **Step 1: `web/format.go` 의 `activityKinds` 근거를 갈아 끼운다**

이 주석 블록을

```go
// activityKinds 는 "이 세션이 일하고 있나"에 답하는 신호다.
//
// ★ **mcp 와 push 는 일부러 뺐다.**
//
//	mcp  — 서비스가 세션을 열 때와 상태를 바꿀 때마다 찍는다(service/session.go 의
//	       t.Beat(..., SignalMCP, now)). 포함하면 아무것도 안 한 세션도 점등돼
//	       배지의 판별력이 0이 된다. 실측: 카드 26장 중 16장이 신호가 mcp 하나뿐이고
//	       그 시각이 opened_at 과 같았다.
//	push — 랜딩하고 떠난 세션이 계속 일하는 것처럼 보인다.
//
// 이 목록을 늘리기 전에 그 신호가 **사람이나 에이전트의 작업**을 뜻하는지 먼저 물어라.
```

이렇게 바꾼다:

```go
// activityKinds 는 "이 세션이 일하고 있나"에 답하는 신호다.
//
// ★ **mcp 와 push 는 일부러 뺐다.**
//
//	mcp  — 도구 호출이면 무엇이든 찍는다. mcpsrv 의 callTool 이 이름을 안 가리고
//	       dispatch **전에** 찍으므로 읽기 전용 board 하나로도 점등되고,
//	       service.Note 도 찍는데 그 문은 REST 로 열려 있어 PreCompact 훅의
//	       **자동 초안**과 CLI fd note 가 들어온다. 사람도 에이전트도 아무 일을
//	       안 한 시점에 켜지는 신호라 배지의 판별력이 0이 된다.
//	push — 랜딩하고 떠난 세션이 계속 일하는 것처럼 보인다.
//
// ★ 옛 근거는 "세션 열기와 상태 전이가 mcp 를 찍는다"였다(실측: 카드 26장 중 16장이
// mcp 하나뿐이었다). 그 두 자리는 지웠다 — 열기는 도구 호출이 아니기 때문이다.
// 근거가 바뀌었을 뿐 결론은 그대로다.
//
// 이 목록을 늘리기 전에 그 신호가 **사람이나 에이전트의 작업**을 뜻하는지 먼저 물어라.
```

- [ ] **Step 2: `mcpsrv/render.go` 의 쌍둥이 주석을 같은 근거로 맞춘다**

이 주석 블록을

```go
// activityKinds 는 "이 세션이 일하고 있나"에 답하는 신호다.
//
// ★ **mcp 와 push 는 일부러 뺐다.** mcp 는 서비스가 세션을 열 때와 상태를 바꿀 때마다
// 찍으므로(service/session.go) 포함하면 아무것도 안 한 세션도 점등돼 판별력이 0이 된다 —
// 실측: 카드 26장 중 16장이 신호가 mcp 하나뿐이고 그 시각이 opened_at 과 같았다.
// push 는 랜딩하고 떠난 세션이 계속 일하는 것처럼 보인다.
```

이렇게 바꾼다(`web/format.go` 와 같은 판단이므로 근거가 갈리면 안 된다):

```go
// activityKinds 는 "이 세션이 일하고 있나"에 답하는 신호다.
//
// ★ **mcp 와 push 는 일부러 뺐다.** mcp 는 도구 호출이면 무엇이든 찍는다 —
// 이 파일과 짝인 callTool 이 이름을 안 가리고 dispatch 전에 찍으므로 읽기 전용
// board 하나로도 점등되고, service.Note 의 문은 REST 로 열려 있어 PreCompact 훅의
// 자동 초안까지 들어온다. 포함하면 아무 일도 안 한 세션이 점등돼 판별력이 0이 된다.
// push 는 랜딩하고 떠난 세션이 계속 일하는 것처럼 보인다.
// (옛 근거였던 "세션 열기·상태 전이가 찍는다"는 그 두 자리를 지워 사라졌다 —
//  web/format.go 의 같은 주석과 함께 읽어라.)
```

- [ ] **Step 3: `web/activity_test.go` 의 머리말과 갈래 이름을 고친다**

머리말의

```go
// ★ mcp 를 활동에서 뺀 것이 이 함수의 핵심이다. 서비스가 세션을 열 때·상태를 바꿀 때
// 마다 mcp 를 찍으므로(service/session.go 의 t.Beat(..., SignalMCP, now)), mcp 를
// 포함하면 배지가 상시 점등돼 판별력이 0이 된다 — 화면 ①이 선점만 내기로 한 이상
// 이 배지가 "쥐고만 있고 안 하는 세션"을 가리키는 유일한 축이다.
```

를

```go
// ★ mcp 를 활동에서 뺀 것이 이 함수의 핵심이다. mcp 는 도구 호출이면 무엇이든 찍어서
// 읽기 전용 board 하나로도, PreCompact 훅의 자동 초안 하나로도 켜진다(근거 전문은
// format.go 의 activityKinds 주석). 포함하면 배지가 상시 점등돼 판별력이 0이 된다 —
// 화면 ①이 선점만 내기로 한 이상 이 배지가 "쥐고만 있고 안 하는 세션"을 가리키는
// 유일한 축이다.
```

로 바꾸고, 갈래 이름

```go
			name:    "mcp 뿐이면 활동이 아니다 — 열림·상태 전이가 그 신호를 찍는다",
```

을

```go
			name:    "mcp 뿐이면 활동이 아니다 — 조회 도구와 훅의 자동 초안이 그 신호를 찍는다",
```

로 바꾼다.

- [ ] **Step 4: `mcpsrv/board_claim_filter_test.go` 의 픽스처 주석을 고친다**

```go
			// mcp 뿐이다 — 열림·상태 전이가 찍는 신호라 **활동이 아니다.**
			// 이것이 "쥐고만 있고 아무것도 안 한 세션"의 실제 모양이다(실측 16/26).
```

를

```go
			// mcp 뿐이다 — 조회 도구(board)를 부르기만 해도 찍히는 신호라 **활동이 아니다.**
			// 이것이 "쥐고만 있고 아무것도 안 한 세션"의 실제 모양이다.
```

로 바꾼다. 실측 `16/26` 은 열림이 mcp 를 찍던 판의 값이라 지금은 재현되지 않으므로 뺀다.

- [ ] **Step 5: `web/claim_filter_test.go` 의 이미 틀린 주석을 고친다**

```go
	f.claimOne(quiet.ID, "it-quiet") // pick 은 mcp 신호를 남기지만 활동은 아니다
```

이 주석은 **오늘 기준으로도 틀렸다** — `service.Pick` 은 `Beat` 를 안 부른다(`SignalMCP` 생산자는 `session.go` 두 자리·`finish.go:426`·`mcpsrv.go` 넷뿐이었고, 이 변경 뒤엔 둘이다). 이 세션의 mcp 는 `f.openSession` 이 남긴 것이었고, 이제는 신호가 0건이다.

```go
	f.claimOne(quiet.ID, "it-quiet") // 선점만 있고 신호는 0건이다 — pick 은 신호를 안 남긴다
```

- [ ] **Step 6: `model/types.go` 의 `SignalKind` 머리말이 mcp 를 정의하게 한다**

이 머리말은 거짓이 되지는 않는다 — 대신 **mcp 에 대해 아무 말도 안 한다.** 다섯 중 뜻이 자명하지 않은 유일한 값인데 정의가 열거 옆에 없으면 다음 사람이 다시 grep 으로 생산자를 세게 된다. 이 변경이 정확히 그 작업이었다.

```go
// SignalKind 는 생존 '사실'의 종류다. 판정이 아니다.
//
// 넷을 나란히 두고 합치지 않는다. 하나만 보면 반드시 오판한다 —
// 에이전트가 긴 도구를 돌리는 동안 Prompt 는 안 오지만 Tool 은 오고,
// 사람이 읽기만 하는 동안은 Prompt 만 온다.
// Commit·Push 는 서버의 git 리더가 직접 관측하는 유일한 신호다(클라이언트를 안 믿는다).
```

뒤에 한 문단을 더한다:

```go
//
// ★ MCP 는 **도구 호출**이다 — 그 이상도 이하도 아니다. 세션을 여는 것과 상태를
// 바꾸는 것은 여기 안 들어간다(그렇게 찍던 두 자리를 지웠다). 다만 조회 전용 도구도,
// 판단 저장(service.Note — REST 로 열려 있어 PreCompact 훅의 자동 초안이 들어온다)도
// 이 신호를 찍는다. 그래서 MCP 는 "살아 있다"이지 "일하고 있다"가 아니다.
```

- [ ] **Step 7: `DESIGN.md` §4 신호 표의 `mcp` 행을 고친다**

이 행은 지금 **거짓**이고(열림·상태 전이·판단 저장도 찍는다) 이 변경으로 참에 가까워지지만, 판단 저장이 남으므로 완전한 참은 아니다.

```
| `mcp` | 아무 도구 호출 | 세션이 살아 있다 |
```

를

```
| `mcp` | MCP 도구 호출 · 판단 저장(`fd note`·PreCompact 훅 포함) | 세션이 살아 있다 — 일하고 있다는 뜻은 아니다 |
```

로 바꾼다.

- [ ] **Step 8: `DESIGN.md` 의 실측 문장에 단서를 단다**

```
있는 claude 프로세스는 5개였고, 그중 16장은 신호가 열림 하나뿐(발자국 0)이었다.
```

관측 기록이므로 수치는 지우지 않되, 지금 DB 에서 같은 모양을 찾다가 못 찾는 일을 막는다:

```
있는 claude 프로세스는 5개였고, 그중 16장은 신호가 열림 하나뿐(발자국 0)이었다
(**당시 판**: 세션 열기가 `mcp` 를 찍었다. 그 자리는 지웠으므로 지금 그 카드는 신호가 0건이다).
```

- [ ] **Step 9: `DESIGN.md` §4 화면 ① 의 `mcp` 근거를 갈아 끼운다**

```
   - **`mcp` 는 활동이 아니다.** `OpenSession` 과 상태 전이가 그 신호를 찍으므로
     포함하면 아무것도 안 한 세션도 점등된다(실측: 카드 26장 중 16장이 `mcp` 하나뿐이고
     그 시각이 `opened_at` 과 같았다). `push` 도 뺐다 — 랜딩하고 떠난 세션이 일하는
     것처럼 보인다.
```

를

```
   - **`mcp` 는 활동이 아니다.** 도구 호출이면 무엇이든 찍는다 — `callTool` 이 이름을
     안 가리고 dispatch 전에 찍으므로 읽기 전용 `board` 하나로 켜지고, `service.Note`
     의 문은 REST 로 열려 있어 **`PreCompact` 훅의 자동 초안**까지 들어온다. 사람도
     에이전트도 아무 일을 안 한 시점에 켜지는 신호다. `push` 도 뺐다 — 랜딩하고 떠난
     세션이 일하는 것처럼 보인다.
     (옛 근거는 "`OpenSession` 과 상태 전이가 찍는다"였다. 그 두 자리는 지웠다 —
      세션을 여는 것은 도구 호출이 아니기 때문이다.)
```

- [ ] **Step 10: 전 패키지 관문을 통과하는지 본다**

Run: `go vet ./... && go test ./... -count=1`
Expected: vet 무출력, 전 패키지 `ok`. 이 태스크는 산문만 만졌으므로 **반드시** 초록이다. 빨간불이면 주석 밖을 건드린 것이니 diff 를 다시 본다.

- [ ] **Step 11: 커밋**

```bash
git add internal/web/format.go internal/mcpsrv/render.go internal/web/activity_test.go \
        internal/mcpsrv/board_claim_filter_test.go internal/web/claim_filter_test.go \
        internal/model/types.go ../DESIGN.md
git commit -m "$(cat <<'EOF'
docs(flightdeck): mcp 를 활동에서 뺀 근거를 갈아 끼운다 — 지워진 코드를 가리키던 아홉 자리

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: 남은 거짓과 잃은 축을 원장에 남기고 후속을 올린다

이 항목이 **의도적으로 안 고친 것**이 둘 있다. 적어 두지 않으면 다음 사람이 그 자리를 결함으로 보고 고치러 가거나, "왜 카드가 사라지나"를 처음부터 다시 판다.

**Files:** 없음 — `fd` 도구만 쓴다.

- [ ] **Step 1: 후속 항목 둘을 큐에 올린다**

`add` 로 등록한다. 제목·본문은 아래를 그대로 쓴다.

1. **`fd-note-beat-masquerades-as-mcp`** — `service.Note`(`finish.go:426`)가 `SignalMCP` 를 찍는데 그 문은 MCP 전용이 아니다. `POST /api/v1/judgments` 로 열려 있어 **PreCompact 훅의 자동 초안**과 CLI `fd note` 가 MCP 를 한 번도 안 부르고 mcp 를 찍는다. 진짜 MCP 도구 호출은 `mcpsrv.go` 의 `callTool` 이 이미 따로 찍으므로 이 자리는 중복이기도 하다. 지울지, 남기고 근거를 적을지가 판단 대상이다. 경로: `internal/service/finish.go` · `plugins/flightdeck/DESIGN.md`.

2. **`fd-open-signal-backfill-needs-migrate-command`** — 옛 거짓 `mcp` 행(운영 DB 223건)을 지우려면 `DELETE FROM` 이 필요한데 `migrate_guard_test.go` 의 `destructiveOps` 에 걸리고 거기엔 예외 기제가 없다. 그 시험이 지정한 처방은 (a) `fd migrate [--to N]`/`--rollback` 으로 적용을 기동에서 분리하거나 (b) 예외 자리를 신설하고 `DESIGN.md` §7 + `store.go:361-374` 의 "증분은 순수 가산" 판단을 함께 개정하는 것이다. **덤으로 확인된 사실**: `store.go:362` 와 `DESIGN.md:558` 의 "증분이 한 단(`002_idempotency`)이고" 는 **이미 낡았다**(실제 002·003·004 세 단). 그리고 `DESIGN.md:559` 는 "fail-open 훅 6종", `store.go:363` 은 "4종" 으로 서로 갈려 있다. 경로: `internal/store/store.go` · `internal/store/migrate_guard_test.go` · `plugins/flightdeck/DESIGN.md` · `cmd/fd/migrate.go`.

- [ ] **Step 2: 판단을 남긴다**

`note(kind: "decision", item_id: "fd-open-signal-masquerades-as-mcp")` 로 남긴다. 반드시 담을 것:

- **왜 항목 본문의 길(`signal.kind` 에 `open` 추가)을 안 갔나** — `schema.sql` 이 버전 1 고정이라는 규약, SQLite 가 CHECK 를 `ALTER` 로 못 바꾼다는 사실, 재생성이 `declaredTables`(중복 선언 `Fatalf`)와 `destructiveOps`(`DROP TABLE`·`RENAME TO`·`INSERT … SELECT`) 양쪽에 걸린다는 실측.
- **왜 백필을 안 했나** — `at = opened_at` 술어가 **0행**을 지운다는 실측(정확 일치 0 · `at < opened_at` 34 · `at > opened_at` 189), 그 원인이 두 시각이 서로 다른 순간에 찍힌다는 것(`service/session.go:119` vs `store/session.go:85`, 사이에 git 파생 2.47~11.38ms), PK 가 `(session_id, kind)` 이고 `Beat` 가 단조 upsert 라 **행 단위 분리가 원리적으로 불가능**하다는 것, 그리고 거짓 행의 수명이 창(2시간)이라는 것.
- **무엇을 잃었나** — 이 비트가 재개에서도 도는 **전 클라이언트 경로의 하트비트**였다는 것. 삭제 뒤 프롬프트·편집·MCP 호출 없이 2시간을 넘긴 세션은 `ListLive` 에서 빠지고, 그러면 `prescribe.go` 의 `Others` 에서도 빠진다(`DESIGN.md:464-468` 이 경계한 "조용한 미탐"). 받아들인 근거와, 후속 `fd-board-folds-open-only-cards` 가 같은 방향이라는 것.
- **일부러 안 고친 자리** — `finish.go:426`(위 후속 1), `activityKinds` 슬라이스(원소 무변경, 주석만).
- **뒤집은 것** — `web/claim_filter_test.go:74` 의 "pick 은 mcp 신호를 남긴다" 는 오늘 기준으로도 틀렸다. `lane_panel_test.go:117` 의 실패 문구는 신호 나이를 대기 경과라고 부르고 있었다(그 파일 머리말이 고발한 것과 같은 종류의 오표기).

---

## Self-Review

**1. 스펙 coverage.** 항목 본문의 "해야 할 일" 셋 중 — `signal.kind` 에 `open` 추가는 **의도적으로 안 한다**(Global Constraints 와 Task 5 Step 2 가 근거를 적는다). `OpenSession` 이 `SignalMCP` 를 안 찍게 하는 것은 Task 3 Step 3. "옵션으로 잎지 말 것"으로 지목된 `SetState` 는 Task 3 Step 4. 항목의 "주의" 둘 중 증분 004 건은 무의미해졌고(증분을 안 만든다), `ListLive` 창 판정이 안 바뀐다는 것은 그대로 참이다.

**2. Placeholder scan.** 모든 코드 스텝이 실제 before/after 를 담는다. "적절히 처리한다" 류 문구 없음.

**3. Type consistency.** `Service.Beat(ctx, sessionID, kind, paths)` · `Store.Signals(ctx, sessionID)` · `Service.SetState(ctx, sessionID, st, why)` 세 시그니처만 쓰고 전부 기존 것이다. 새 심볼을 만들지 않는다.
