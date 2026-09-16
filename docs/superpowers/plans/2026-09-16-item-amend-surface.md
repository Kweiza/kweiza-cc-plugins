# 항목을 제자리에서 고치는 표면 — 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `item` 의 `title`·`body`·`paths` 를 제자리에서 고치는 동사 `amend` 를 REST·MCP·CLI 에 열고, 옛 값을 추가 전용 표 `item_revision` 에 보존한다.

**Architecture:** 계산과 쓰기를 가르고(`service` → `store`), 읽기부터 쓰기까지를 한 트랜잭션에 묶는다(`service.SetLabels` 와 같은 모양). 옛 값은 UPDATE 직전 같은 트랜잭션에서 `item_revision` 에 INSERT 한다. 부재를 지키던 관문(`item_body_immutable_test.go`)은 지우지 않고 **유일 작성자 관문**으로 전환한다.

**Tech Stack:** Go 1.25+ · SQLite(modernc) · 표준 `net/http` mux · 자체 MCP 서버(`internal/mcpsrv`)

**Spec:** `docs/superpowers/specs/2026-09-16-item-amend-surface-design.md`

## Global Constraints

이 절의 모든 줄은 **모든 태스크에 암묵적으로 포함된다.**

- **작업 위치는 워크트리다.** `/Users/kweiza/Developments/kweiza-cc-plugins/.flightdeck/worktrees/fd-item-amend-surface`. 주 체크아웃은 읽기 전용이다. `AGENTS.md`·`CLAUDE.md` 는 이미 워크트리에 있고 **절대 스테이징하지 않는다**(`git add -f` 도 금지).
- **커밋 메시지에 AI 귀속을 넣지 않는다.** `Co-authored-by`·`Generated with`·모델 이름·도구 서명·이모지 전부 금지. 기술적 변경·근거·검증만 적는다. 이 지침 파일들의 존재도 커밋에 언급하지 않는다.
- **커밋 신원은 하나다.** 훅이 강제한다. `--no-verify` 로 넘기지 않는다.
- **gofmt 관문은 종료코드를 안 믿는다.** `gofmt -l .` 은 위반을 뱉고도 0을 낸다. 반드시 `[ -z "$(gofmt -l .)" ]` 로 잰다.
- **`go build` 는 관문이 아니다.** `_test.go` 를 건너뛴다. 교차 검증은 `go vet ./...` 로 한다.
- **시험은 떼어 돌린다.** Bash 기본 타임아웃 2분을 넘긴다 — 시험 명령에는 `timeout: 480000` 을 준다. 묶어 돌리면 `exit 143` 인데 앞줄의 초록만 남는다.
- **cwd 를 모듈 안에 둔다.** `plugins/flightdeck/server` 밖에서 `gofmt` 를 돌리면 빈 디렉토리를 검사하고 조용히 통과한다.
- **모든 주석·거절 문구·응답 문구는 한국어다.** 이 저장소의 문체를 따른다.
- **새 거절은 `service.RefusedError`(What·Reason·Guidance)다.** `errors.New` 는 `api.ClassifyError` 화이트리스트에 안 걸려 500 으로 나간다.
- **MCP 도구 설명은 90 runes 이하다.** `mcpsrv/protocol_test.go` 가 잰다.
- **새 시험은 망가진 것을 넣어 빨간불을 먼저 본다.** 그리고 **어느 줄이 잡았는지**까지 확인한다 — 옆 축이 잡으면 재려던 주장은 미검증이다.

**공통 경로 약어:** 이하 모든 경로는 `plugins/flightdeck/server/` 기준이다. `DESIGN.md` 만 `plugins/flightdeck/DESIGN.md` 다.

---

## 파일 구조

| 파일 | 책임 | 태스크 |
|---|---|---|
| `internal/store/migrations/016_item_revision.sql` | 증분 — 표 하나 + 트리거 둘 | 1 |
| `internal/store/schema.sql` | 신규 설치용 선언 (증분과 같은 모양) | 1 |
| `internal/store/amend.go` | **`item` 본문을 고치는 유일한 자리.** UPDATE 와 revision INSERT 가 여기 함께 산다 | 2 |
| `internal/store/item_body_single_writer_test.go` | 부재 관문 → 유일 작성자 관문 (`git mv`) | 2 |
| `internal/service/amend.go` | 입력 검증·거절·트랜잭션 조립·겹침 계산 | 3 |
| `internal/api/handlers_items.go` | REST 라우트 하나 | 4 |
| `internal/mcpsrv/render_amend.go` | 응답 렌더러 — **규율은 전부 여기 산다** | 5 |
| `internal/mcpsrv/tools.go`·`mcpsrv.go`·`backend.go` | 9번째 도구 배선 | 5 |
| `cmd/fd/cmds.go`·`main.go`·`wire.go`·`offline.go`·`mcpbackend.go` | CLI `fd amend` + 오프라인 거절 | 6 |

---

## Task 1: 증분 016 — 표와 트리거

**Files:**
- Create: `internal/store/migrations/016_item_revision.sql`
- Modify: `internal/store/schema.sql` (`item_after` 선언 **앞**에 넣는다 — 선언 순서는 FK 순서다)
- Modify: `internal/store/schema_table_count_test.go` (`want` 목록)
- Modify: `plugins/flightdeck/DESIGN.md` (§3 제목 줄 + Q 계층)
- Test: `internal/store/constraint_test.go`

**Interfaces:**
- Consumes: 없음 (첫 태스크)
- Produces: 표 `item_revision(project, item_id, rev, at, session_id, title, body, paths, reason)`. 뒤 태스크가 이 컬럼 이름에 의존한다.

- [ ] **Step 1: 트리거가 무는지 재는 실패 시험을 쓴다**

`internal/store/constraint_test.go` 끝에 붙인다. 이 파일의 기존 헬퍼(`openTestStore` 류)를 먼저 읽고 같은 것을 쓴다 — 이름이 다르면 컴파일이 안 된다.

```go
// TestItemRevisionIsAppendOnly 는 개정 이력이 고쳐지거나 지워지지 않는지 본다.
//
// 이 표의 존재 이유가 「본문을 제자리에서 고치는 대신 옛 값이 사라지지 않는다」는
// 보장 하나다. 그 보장이 트리거 없이 주석으로만 있으면, 고치는 코드가 생기는 날
// 아무도 그것을 못 본다 — judgment 가 같은 이유로 같은 트리거를 갖는다.
func TestItemRevisionIsAppendOnly(t *testing.T) {
	st := openTestStore(t) // ← 이 파일의 기존 헬퍼 이름으로 바꿔라
	ctx := context.Background()

	seedProjectAndItem(t, st, "p1", "i1") // ← 기존 시드 헬퍼로 바꿔라

	err := st.Tx(ctx, func(tx *Tx) error {
		_, err := tx.tx.ExecContext(ctx,
			`INSERT INTO item_revision(project, item_id, rev, at, session_id, title, body, paths, reason)
			 VALUES (?,?,?,?,?,?,?,?,?)`,
			"p1", "i1", 1, "2026-09-16T00:00:00Z", nil, "옛 제목", "옛 본문", "[]", "오타")
		return err
	})
	if err != nil {
		t.Fatalf("개정 행을 못 넣었다: %v", err)
	}

	for _, tc := range []struct {
		name string
		sql  string
		args []any
	}{
		{"UPDATE", `UPDATE item_revision SET body = ? WHERE project = ? AND item_id = ? AND rev = ?`,
			[]any{"덮어씀", "p1", "i1", 1}},
		{"DELETE", `DELETE FROM item_revision WHERE project = ? AND item_id = ? AND rev = ?`,
			[]any{"p1", "i1", 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := st.Tx(ctx, func(tx *Tx) error {
				_, e := tx.tx.ExecContext(ctx, tc.sql, tc.args...)
				return e
			})
			if err == nil {
				t.Fatalf("%s 가 통과했다 — 개정 이력은 추가 전용이어야 한다", tc.name)
			}
			if !strings.Contains(err.Error(), "추가 전용") {
				t.Errorf("거절은 됐는데 사유가 이 트리거의 것이 아니다: %v\n"+
					"다른 제약이 잡은 것이면 이 시험은 재려던 것을 안 재고 있다", err)
			}
		})
	}
}

// TestItemRevisionRequiresReason 은 사유 없는 개정을 CHECK 가 막는지 본다.
func TestItemRevisionRequiresReason(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	seedProjectAndItem(t, st, "p1", "i1")

	err := st.Tx(ctx, func(tx *Tx) error {
		_, e := tx.tx.ExecContext(ctx,
			`INSERT INTO item_revision(project, item_id, rev, at, session_id, title, body, paths, reason)
			 VALUES (?,?,?,?,?,?,?,?,?)`,
			"p1", "i1", 1, "2026-09-16T00:00:00Z", nil, "t", "b", "[]", "")
		return e
	})
	if err == nil {
		t.Fatal("빈 사유가 통과했다 — 되짚을 수 없는 개정을 남기게 된다")
	}
}
```

- [ ] **Step 2: 시험이 실패하는지 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/store/ -run 'TestItemRevision' -v
```

기대: `no such table: item_revision` 으로 FAIL. **이 사유가 아니면 멈춰라** — 다른 이유로 실패하는 것은 시험이 잘못 쓰인 것이다.

- [ ] **Step 3: 증분 파일을 쓴다**

`internal/store/migrations/016_item_revision.sql`. 기존 증분(예: `015_item_after_dep_project.sql`)의 머리말 형식을 먼저 읽고 맞춘다.

```sql
-- 016_item_revision.sql — 항목 본문의 개정 이력.
--
-- DESIGN §11 「항목 본문 수정」이 2026-09-16 에 열렸다. 본문을 제자리에서 고치는 값을
-- 치르는 대신, 옛 값이 사라지지 않는다는 보장을 이 표가 산다.
--
-- ★ 이 행이 담는 것은 **고치기 직전의 값**이지 새 값이 아니다. 현재 값은 item 한 곳에만
--   있어서 두 표가 어긋날 자리가 원리적으로 없고, rev 를 역순으로 이으면 원문까지 복원된다.
--   새 값을 쌓으면 "현재"가 두 곳에 생긴다 — judgment_link.target_project 가 없던 동안
--   링크는 들어가고 응답은 성공인데 수신자에게는 안 보였던 그 부류다.
--
-- 파괴적 조작이 없다 — CREATE 뿐이라 migrate_guard 의 destructiveExempt 등록이 필요 없다.

CREATE TABLE item_revision (
  project    TEXT NOT NULL,
  item_id    TEXT NOT NULL,
  rev        INTEGER NOT NULL,              -- 1부터. 이 항목에서 몇 번째 개정인가
  at         TEXT NOT NULL,
  session_id TEXT REFERENCES session(id),   -- 누가 고쳤나. 세션을 못 얻어도 개정은 남긴다
  title      TEXT NOT NULL,                 -- ★ 고치기 직전의 값
  body       TEXT NOT NULL,
  paths      TEXT NOT NULL,                 -- JSON 배열
  reason     TEXT NOT NULL CHECK (reason <> ''),
  PRIMARY KEY (project, item_id, rev),
  FOREIGN KEY (project, item_id) REFERENCES item(project, id)
);

-- 이력을 고칠 수 있으면 이력이 아니다. judgment 와 같은 규율이다.
CREATE TRIGGER item_revision_no_update BEFORE UPDATE ON item_revision
BEGIN SELECT RAISE(ABORT, 'item_revision 은 추가 전용이다 — 개정은 새 rev 로 쌓아라'); END;

CREATE TRIGGER item_revision_no_delete BEFORE DELETE ON item_revision
BEGIN SELECT RAISE(ABORT, 'item_revision 은 추가 전용이다 — 지우면 복구 경로가 0이 된다'); END;
```

- [ ] **Step 4: `schema.sql` 에 같은 선언을 넣는다**

증분은 기존 DB 를, `schema.sql` 은 신규 설치를 담당한다. **둘이 갈리면 새 설치와 기존 설치가 다른 DB 가 된다.** 위 SQL 에서 머리말 주석만 줄여 `CREATE TABLE item (` 블록 **뒤**, `CREATE TABLE item_after (` **앞**에 넣는다.

- [ ] **Step 5: 표 목록 시험을 고친다**

`internal/store/schema_table_count_test.go` 의 `want` 슬라이스는 **정렬된 목록**이다. `"item"` 과 `"item_after"` 사이에 `"item_revision"` 을 넣는다.

```go
		"item",
		"item_after",
		"item_dependents",
```
→
```go
		"item",
		"item_after",
		"item_dependents",
		"item_revision",
```

**정렬 위치를 직접 확인하라** — `item_after` < `item_dependents` < `item_revision` 이 Go 문자열 정렬 순서다. 시험이 `got[i] != want[i]` 로 자리까지 재므로 위치가 틀리면 수가 맞아도 빨갛다.

- [ ] **Step 6: DESIGN 을 고친다**

`plugins/flightdeck/DESIGN.md`:

1. 234행 제목: `## 3. 데이터 모델 — SQLite 파일 하나, 테이블 26개, 계층 셋` → `테이블 27개`
2. §3 Q 계층 절(`### Q 계층`)에서 `item_after` 설명 뒤에 한 항목을 더한다:

```markdown
- `item_revision` — 항목 본문의 개정 이력. **추가 전용** — `BEFORE UPDATE`·`BEFORE DELETE`
  트리거가 `RAISE(ABORT)` 한다. 담는 것은 **고치기 직전의 값**이라 현재 값은 `item` 한 곳에만
  있고, `rev` 를 역순으로 이으면 원문까지 복원된다. `reason` 은 `CHECK (reason <> '')` 로
  강제한다 — 사유 없는 수정은 되짚을 수 없고, 되짚을 사람이 이 표를 여는 유일한 이유가 그것이다.
  설계 정본은 `docs/superpowers/specs/2026-09-16-item-amend-surface-design.md`.
```

- [ ] **Step 7: 시험이 통과하는지 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/store/ -run 'TestItemRevision|TestDeclaredTables|TestSchemaVersionTableIsCounted|TestMigrat' -v
```

기대: 전부 PASS. 증분 가드(`TestMigrat…`)가 016 에 대해 예외를 요구하면 그건 SQL 에 파괴적 조작이 섞인 것이다 — 예외를 등록하지 말고 SQL 을 고쳐라.

- [ ] **Step 8: 관문 다섯 줄을 돌린다**

```bash
cd plugins/flightdeck/server && [ -z "$(gofmt -l .)" ] && echo "gofmt ok" && go vet ./... && echo "vet ok"
```

- [ ] **Step 9: 커밋**

```bash
git add plugins/flightdeck/server/internal/store/migrations/016_item_revision.sql \
        plugins/flightdeck/server/internal/store/schema.sql \
        plugins/flightdeck/server/internal/store/schema_table_count_test.go \
        plugins/flightdeck/server/internal/store/constraint_test.go \
        plugins/flightdeck/DESIGN.md
git commit -m "$(cat <<'EOF'
feat(flightdeck): 항목 본문의 개정 이력 표를 만든다 — 스키마가 26→27 로 오른다

증분 016. item_revision 은 추가 전용이고 담는 것은 고치기 직전의 값이다. 현재 값은 item
한 곳에만 있어 두 표가 어긋날 자리가 없고, rev 를 역순으로 이으면 원문까지 복원된다.

reason 은 CHECK 로 강제한다. 사유 없는 수정은 되짚을 수 없고, 되짚을 사람이 이 표를 여는
유일한 이유가 그것이다.

트리거 둘은 judgment 와 같은 규율이다 — 이력을 고칠 수 있으면 이력이 아니다.
EOF
)"
```

---

## Task 2: `store.AmendItem` — 그리고 관문 전환

**이 둘은 한 태스크다.** `amend.go` 가 `UPDATE item SET title` 을 치는 순간 기존 관문 `TestItemBodyHasNoUpdateSurface` 가 빨개진다. 그 빨간불은 **관문이 살아 있다는 증거**이고, 그것을 본 뒤 관문을 전환하는 것이 이 태스크의 전부다. 나눠서 커밋하면 중간 커밋이 빨간 상태로 남는다.

**Files:**
- Create: `internal/store/amend.go`
- Create: `internal/store/amend_test.go`
- Rename: `internal/store/item_body_immutable_test.go` → `internal/store/item_body_single_writer_test.go` (`git mv`)
- Modify: 그 파일의 판정 로직
- Modify: `plugins/flightdeck/DESIGN.md` §11

**Interfaces:**
- Consumes: Task 1 의 `item_revision` 컬럼들
- Produces:
  ```go
  type AmendPatch struct {
      Title  *string
      Body   *string
      Paths  *[]string
      Reason string
  }
  type AmendRecord struct {
      Rev     int
      Before  model.Item
      After   model.Item
      Changed []string // "title"·"body"·"paths" 중 실제로 값이 달라진 것. 순서 고정
  }
  func (t *Tx) AmendItem(project, itemID string, p AmendPatch, sessionID string) (AmendRecord, error)
  ```
  Task 3 이 이 시그니처를 그대로 부른다.

- [ ] **Step 1: 실패 시험을 쓴다**

`internal/store/amend_test.go`. 기존 헬퍼 이름은 `internal/store/labels_test.go` 를 읽어 맞춘다.

```go
package store

import (
	"context"
	"strings"
	"testing"
)

func strp(s string) *string      { return &s }
func pathsp(v []string) *[]string { return &v }

// TestAmendItemWritesPreviousValueToRevision 은 이 표의 존재 이유를 잰다 —
// 개정 행에 들어가는 것이 **새 값이 아니라 옛 값**인가.
func TestAmendItemWritesPreviousValueToRevision(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	seedProjectAndItem(t, st, "p1", "i1") // 제목 "원래 제목" · 본문 "원래 본문" 으로 시드하도록 헬퍼를 맞춰라

	var rec AmendRecord
	err := st.Tx(ctx, func(tx *Tx) error {
		var e error
		rec, e = tx.AmendItem("p1", "i1", AmendPatch{
			Title: strp("고친 제목"), Reason: "오타",
		}, "sess-1")
		return e
	})
	if err != nil {
		t.Fatalf("고치지 못했다: %v", err)
	}
	if rec.Rev != 1 {
		t.Errorf("첫 개정의 rev 가 %d다 — 1이어야 한다", rec.Rev)
	}
	if rec.After.Title != "고친 제목" {
		t.Errorf("항목 제목이 %q다 — 고친 값이어야 한다", rec.After.Title)
	}

	// ★ 이 단정이 이 시험의 전부다. 개정 행은 옛 값을 담아야 한다.
	var gotTitle, gotReason string
	row := st.db.QueryRowContext(ctx,
		`SELECT title, reason FROM item_revision WHERE project=? AND item_id=? AND rev=1`, "p1", "i1")
	if err := row.Scan(&gotTitle, &gotReason); err != nil {
		t.Fatalf("개정 행을 못 읽었다: %v", err)
	}
	if gotTitle != "원래 제목" {
		t.Errorf("개정 행의 제목이 %q다 — **고치기 직전의 값**(\"원래 제목\")이어야 한다.\n"+
			"새 값을 쌓으면 현재가 두 곳에 생기고 둘이 갈리는 날 아무도 못 본다", gotTitle)
	}
	if gotReason != "오타" {
		t.Errorf("사유가 %q다 — 요청한 것이어야 한다", gotReason)
	}
}

// TestAmendItemLeavesOmittedAxesAlone 은 안 준 축이 안 변하는지 본다.
func TestAmendItemLeavesOmittedAxesAlone(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	seedProjectAndItem(t, st, "p1", "i1")

	var rec AmendRecord
	err := st.Tx(ctx, func(tx *Tx) error {
		var e error
		rec, e = tx.AmendItem("p1", "i1", AmendPatch{Title: strp("새 제목"), Reason: "r"}, "s")
		return e
	})
	if err != nil {
		t.Fatalf("고치지 못했다: %v", err)
	}
	if rec.After.Body != rec.Before.Body {
		t.Errorf("본문이 %q → %q 로 변했다 — 안 준 축은 안 건드려야 한다", rec.Before.Body, rec.After.Body)
	}
	if len(rec.Changed) != 1 || rec.Changed[0] != "title" {
		t.Errorf("Changed 가 %v다 — [title] 이어야 한다", rec.Changed)
	}
}

// TestAmendItemSameValueIsNoChange 는 같은 값 재지정이 변화로 안 세지는지 본다.
//
// 거절하지는 않는다 — 다만 "고쳤다"고만 말하면 안 바뀐 것을 바뀐 줄 안다.
func TestAmendItemSameValueIsNoChange(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	seedProjectAndItem(t, st, "p1", "i1")

	var rec AmendRecord
	err := st.Tx(ctx, func(tx *Tx) error {
		cur, e := tx.GetItem("p1", "i1")
		if e != nil {
			return e
		}
		rec, e = tx.AmendItem("p1", "i1", AmendPatch{Title: strp(cur.Title), Reason: "r"}, "s")
		return e
	})
	if err != nil {
		t.Fatalf("고치지 못했다: %v", err)
	}
	if len(rec.Changed) != 0 {
		t.Errorf("Changed 가 %v다 — 같은 값 재지정은 변화가 아니다", rec.Changed)
	}
}

// TestAmendItemRevStacks 는 두 번 고치면 rev 가 쌓이고 역순 복원이 원문을 내는지 본다.
func TestAmendItemRevStacks(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	seedProjectAndItem(t, st, "p1", "i1")

	for i, title := range []string{"둘째", "셋째"} {
		want := i + 1
		var rec AmendRecord
		err := st.Tx(ctx, func(tx *Tx) error {
			var e error
			rec, e = tx.AmendItem("p1", "i1", AmendPatch{Title: strp(title), Reason: "r"}, "s")
			return e
		})
		if err != nil {
			t.Fatalf("%d번째 개정에서 실패했다: %v", want, err)
		}
		if rec.Rev != want {
			t.Fatalf("%d번째 개정의 rev 가 %d다", want, rec.Rev)
		}
	}

	// rev 1 이 원문을 들고 있어야 한다 — 역순 복원의 종점이다.
	var oldest string
	if err := st.db.QueryRowContext(ctx,
		`SELECT title FROM item_revision WHERE project=? AND item_id=? AND rev=1`,
		"p1", "i1").Scan(&oldest); err != nil {
		t.Fatalf("rev 1 을 못 읽었다: %v", err)
	}
	if oldest != "원래 제목" {
		t.Errorf("rev 1 의 제목이 %q다 — 원문이어야 한다", oldest)
	}
}

// TestAmendItemClosedItemIsAllowed 는 닫힌 항목도 고쳐지는지 본다.
//
// ★ label 과 **다른** 판정이다. label 은 끝난 항목을 거절한다(꼬리표가 뜻을 갖는 굶김
// 축이 열린 항목만 보기 때문이다). amend 는 거절하지 않는다 — 스펙 §2 ⓒ 가 여는 근거
// 중 하나이고, 닫힌 항목의 틀린 본문은 note 로도 못 닿는 자리다.
func TestAmendItemClosedItemIsAllowed(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	seedProjectAndItem(t, st, "p1", "i1")
	closeTestItem(t, st, "p1", "i1") // ← item_state_guard_test.go 의 기존 헬퍼로 바꿔라

	err := st.Tx(ctx, func(tx *Tx) error {
		_, e := tx.AmendItem("p1", "i1", AmendPatch{Body: strp("닫힌 뒤 정정"), Reason: "r"}, "s")
		return e
	})
	if err != nil {
		t.Fatalf("닫힌 항목을 못 고쳤다: %v\n"+
			"amend 는 label 과 달리 종료 상태를 안 본다 — 그것이 이 표면을 연 근거 중 하나다", err)
	}
}

// TestAmendItemUnknownItem 은 없는 항목에 대한 거절이 표준 not-found 인지 본다.
func TestAmendItemUnknownItem(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	seedProjectAndItem(t, st, "p1", "i1")

	err := st.Tx(ctx, func(tx *Tx) error {
		_, e := tx.AmendItem("p1", "없는거", AmendPatch{Title: strp("x"), Reason: "r"}, "s")
		return e
	})
	if err == nil {
		t.Fatal("없는 항목을 고쳤다고 답했다")
	}
	if !strings.Contains(err.Error(), "없는거") {
		t.Errorf("거절에 항목 id 가 없다: %v", err)
	}
}
```

- [ ] **Step 2: 시험이 실패하는지 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/store/ -run 'TestAmendItem' -v
```

기대: `undefined: AmendPatch` 컴파일 실패.

- [ ] **Step 3: `internal/store/amend.go` 를 쓴다**

```go
package store

import (
	"fmt"

	"github.com/kweiza/flightdeck/internal/model"
)

// ─────────────────────────────────────────────────────────────────────────────
// 항목 본문을 고치는 **유일한 자리**
// ─────────────────────────────────────────────────────────────────────────────
//
// ★ 이 파일 밖에서 `UPDATE item SET title|body|paths` 를 치면 관문이 빨개진다
// (`item_body_single_writer_test.go`). 그 관문이 함께 재는 것이 하나 더 있다:
// **이 UPDATE 와 `INSERT INTO item_revision` 이 같은 함수 안에 있어야 한다.**
// 이력 없는 수정 경로가 조용히 생기는 것이 item_revision 의 값을 통째로 무효로
// 만드는 유일한 길이다.
//
// ★ 원장도 **여기서** 남긴다(item.amend). before 를 아는 것은 같은 트랜잭션 안에서
// 읽은 쪽뿐이고, API 로 올려 보내면 원장의 정확성이 응답 왕복에 의존하게 된다 —
// SetLabels·RemoveAfter 가 같은 이유로 같은 자리에 있다.

// AmendPatch 는 고칠 축이다. **nil 은 "안 건드린다"** 이고, 빈 값을 가리키는
// 포인터는 "빈 값으로 바꿔라"다. 둘을 가르려고 포인터로 받는다.
type AmendPatch struct {
	Title  *string
	Body   *string
	Paths  *[]string
	Reason string
}

// AmendRecord 는 고친 결과다.
//
// Changed 는 **요청한 것이 아니라 실제로 값이 달라진 축**이다. 같은 값 재지정은
// 거절하지 않지만, 그때 화면이 "고쳤다"고만 말하면 사람은 안 바뀐 것을 바뀐 줄 안다
// (LabelResult 의 Added·Removed 와 같은 규율).
type AmendRecord struct {
	Rev     int
	Before  model.Item
	After   model.Item
	Changed []string
}

// AmendItem 은 항목의 title·body·paths 를 제자리에서 고치고 옛 값을 개정 이력에 쌓는다.
//
// ★ **종료 상태를 안 본다.** label 은 끝난 항목을 거절하지만(꼬리표가 뜻을 갖는 굶김
// 축이 열린 항목만 본다) 본문은 다르다 — 닫힌 항목의 틀린 본문은 note 로도 못 닿고,
// 그 도달 불가가 이 표면을 연 근거 중 하나다(설계 §2 ⓒ).
func (t *Tx) AmendItem(project, itemID string, p AmendPatch, sessionID string) (AmendRecord, error) {
	var out AmendRecord

	before, err := t.GetItem(project, itemID)
	if err != nil {
		return out, err
	}
	out.Before = before

	after := before
	if p.Title != nil {
		after.Title = *p.Title
	}
	if p.Body != nil {
		after.Body = *p.Body
	}
	if p.Paths != nil {
		after.Paths = *p.Paths
	}
	out.After = after
	out.Changed = amendChanged(before, after)

	// ★ 개정 행을 **UPDATE 앞에** 넣는다. 순서가 뒤집히면 옛 값을 읽을 자리가 이미
	//   사라져 있다 — before 를 변수에 들고 있더라도, 실패 시 어느 쪽이 남는지가
	//   순서로 결정된다. 같은 트랜잭션이라 둘 다 커밋되거나 둘 다 안 된다.
	rev, err := t.nextItemRev(project, itemID)
	if err != nil {
		return out, err
	}
	out.Rev = rev

	beforePathsJSON, err := marshalStrings(before.Paths)
	if err != nil {
		return out, fmt.Errorf("개정 이력 paths 직렬화 실패(id=%q): %w", clip(itemID, 64), err)
	}
	var sess any
	if sessionID != "" {
		sess = sessionID
	}
	if _, err := t.tx.ExecContext(t.ctx,
		`INSERT INTO item_revision(project, item_id, rev, at, session_id, title, body, paths, reason)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		project, itemID, rev, t.now(), sess,
		before.Title, before.Body, beforePathsJSON, p.Reason); err != nil {
		return out, fmt.Errorf("개정 이력 적재 실패(project=%q id=%q rev=%d): %w",
			clip(project, 64), clip(itemID, 64), rev, err)
	}

	afterPathsJSON, err := marshalStrings(after.Paths)
	if err != nil {
		return out, fmt.Errorf("항목 paths 직렬화 실패(id=%q): %w", clip(itemID, 64), err)
	}
	res, err := t.tx.ExecContext(t.ctx,
		`UPDATE item SET title = ?, body = ?, paths = ? WHERE project = ? AND id = ?`,
		after.Title, after.Body, afterPathsJSON, project, itemID)
	if err != nil {
		return out, fmt.Errorf("항목 본문 갱신 실패(project=%q id=%q): %w",
			clip(project, 64), clip(itemID, 64), err)
	}
	if err := affectedOne(res, NFItem, project, itemID); err != nil {
		return out, err
	}

	t.LogEvent("item.amend", project, sessionID, map[string]any{
		"item":    clip(itemID, 100),
		"rev":     rev,
		"changed": out.Changed,
		"reason":  clip(p.Reason, 200),
	})
	return out, nil
}

// nextItemRev 는 이 항목의 다음 개정 번호다.
//
// 트랜잭션 안에서 뽑는다 — `_txlock=immediate` 가 두 세션의 동시 amend 사이를 닫는다.
// 그래도 새면 PK 가 제약 위반으로 터진다(조용한 덮어쓰기가 아니다).
func (t *Tx) nextItemRev(project, itemID string) (int, error) {
	var n int
	if err := t.tx.QueryRowContext(t.ctx,
		`SELECT COALESCE(MAX(rev), 0) FROM item_revision WHERE project = ? AND item_id = ?`,
		project, itemID).Scan(&n); err != nil {
		return 0, fmt.Errorf("개정 번호 조회 실패(project=%q id=%q): %w",
			clip(project, 64), clip(itemID, 64), err)
	}
	return n + 1, nil
}

// amendChanged 는 실제로 값이 달라진 축이다. 순수 함수이고 순서는 title·body·paths 로 고정한다.
func amendChanged(before, after model.Item) []string {
	out := make([]string, 0, 3)
	if before.Title != after.Title {
		out = append(out, "title")
	}
	if before.Body != after.Body {
		out = append(out, "body")
	}
	if !sameStrings(before.Paths, after.Paths) {
		out = append(out, "paths")
	}
	return out
}

// sameStrings 는 두 문자열 슬라이스가 **순서까지** 같은지 본다.
//
// 집합이 아니라 순서를 보는 이유: paths 는 선언이라 순서가 사람이 쓴 그대로 남고,
// 순서만 바꾼 수정도 화면에서는 바뀐 것으로 보이는 편이 옳다.
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
```

**확인할 것:** `t.now()`·`marshalStrings`·`clip`·`affectedOne`·`NFItem` 은 이 패키지에 이미 있다. 이름이 다르면 `internal/store/item.go` 의 `SetLabels` 를 읽어 맞춰라. `sameStrings` 와 같은 헬퍼가 이미 있으면 새로 만들지 말고 그것을 써라(`grep -rn "func sameStrings\|func equalStrings" internal/store`).

- [ ] **Step 4: 시험이 통과하는지, 그리고 기존 관문이 빨개지는지 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/store/ -run 'TestAmendItem' -v
```
기대: PASS.

```bash
cd plugins/flightdeck/server && go test ./internal/store/ -run 'TestItemBodyHasNoUpdateSurface' -v
```
기대: **FAIL** — `항목 본문을 고치는 SQL 이 1 곳이다(… amend.go …)`.

**이 빨간불을 반드시 눈으로 확인하라.** 안 빨개지면 그물이 죽은 것이고, 그 상태로 다음 단계를 하면 전환한 관문도 죽은 채로 초록이 된다.

- [ ] **Step 5: 관문 파일을 옮긴다**

```bash
git mv plugins/flightdeck/server/internal/store/item_body_immutable_test.go \
       plugins/flightdeck/server/internal/store/item_body_single_writer_test.go
```

- [ ] **Step 6: 관문을 전환한다**

`item_body_single_writer_test.go` 를 아래 넷으로 고친다. **기존 인프라(`itemBodyGuardRoot`·`itemBodyGuardInScope`·`itemBodyExecutableSQL`·정규식들)는 그대로 쓴다** — 바꾸는 것은 판정뿐이다.

**⒜ 머리말 주석 교체.** 기존 머리말은 "부재"를 설명한다. 다음으로 바꾼다:

```go
// ─────────────────────────────────────────────────────────────────────────────
// 항목 본문(title·body·paths)을 고치는 자리는 **하나뿐이다**
// ─────────────────────────────────────────────────────────────────────────────
//
// 2026-09-16 이전 이 파일은 "그런 자리가 **없다**"를 지켰다. DESIGN §11 이 그 부재를
// 판정으로 적었고 이 관문이 그 문장을 지켰다. 그 판정이 열렸다(설계 정본:
// docs/superpowers/specs/2026-09-16-item-amend-surface-design.md).
//
// **그래서 이 파일을 지우지 않았다.** §11 이 경고한 실패는 "본문을 못 고치는 것"이 아니라
// **"표면마다 무엇을 고칠 수 있나가 갈리는 것"**이고, 그 경고는 표면이 열린 뒤에 오히려
// 더 유효하다. 지키는 명제만 바꾼다:
//
//   전: title·body 를 무는 `UPDATE item SET` 은 0건이다
//   후: 그 UPDATE 는 store/amend.go 의 AmendItem 한 곳이고, **같은 함수 안에
//       `INSERT INTO item_revision` 이 있다**
//
// 둘째 절이 핵심이다. 이력 없는 수정 경로가 조용히 생기는 것이 item_revision 의 값을
// 통째로 무효로 만드는 유일한 길이고, 그 경로는 UPDATE 하나만 세는 그물에 안 걸린다.
//
// paths 가 감시 목록에 들어온 것도 이때다 — 이제 수정 축이고, 무엇보다 **겹침 판정의
// 입력**이라 몰래 고쳐지면 남의 화면이 조용히 움직인다.
//
// 방향은 여전히 양쪽이다:
//   ① 허용된 자리 밖에서 본문을 고치면        → 빨간불
//   ② §11 에서 이 표면의 이름이 사라지면      → 관문의 좌표가 밀린 것이므로 빨간불
```

**⒝ 감시 컬럼에 `paths` 를 넣는다.**

```go
var itemBodyColumns = []string{"title", "body", "paths"}
```

**⒞ 전수 시험의 판정을 바꾼다.** `TestItemBodyHasNoUpdateSurface` 를 `TestItemBodyHasSingleWriter` 로 고치고, 위반 판정을 "0건"에서 "허용된 파일 밖"으로 바꾼다. 기존 walk 안의 offenders 수집 자리에 파일 판정을 더한다:

```go
// amendWriterFile 은 본문을 고쳐도 되는 **유일한** 파일이다.
const amendWriterFile = "plugins/flightdeck/server/internal/store/amend.go"

// …walk 안…
		for _, m := range clauses {
			cols := itemBodyOffenders(m[1])
			if len(cols) == 0 {
				continue
			}
			rels := filepath.ToSlash(rel)
			if rels == amendWriterFile {
				writerHits += len(cols)
				continue
			}
			for _, col := range cols {
				offenders = append(offenders, fmt.Sprintf("%s  UPDATE item SET … %s = …", rels, col))
			}
		}
```

그리고 자기 점검에 한 줄을 더한다 — **허용된 자리가 실제로 잡혔는지**:

```go
	if writerHits == 0 {
		t.Fatalf("허용된 자리(%s)에서 본문 UPDATE 를 한 건도 못 봤다(파일 %d개를 훑었다) — "+
			"그물이나 좌표가 밀렸다. 이 상태의 offenders 0 은 '깨끗하다'가 아니라 '아무것도 안 봤다'다",
			amendWriterFile, scanned)
	}
```

위반 메시지도 새 명제로 바꾼다:

```go
	if len(offenders) > 0 {
		t.Errorf("허용된 자리 밖에서 항목 본문을 고치는 SQL 이 %d 곳이다(파일 %d개, UPDATE %d건을 훑었다):\n  %s\n\n"+
			"이 저장소에서 item 의 title·body·paths 를 고치는 자리는 %s 의 AmendItem 하나다 — "+
			"거기서만 개정 이력(item_revision)이 함께 쌓이기 때문이다.\n"+
			"새 수정 경로가 정말로 필요하면 그 함수를 부르게 하고, 표면을 늘리는 것이라면 "+
			"**DESIGN §11 을 함께 고쳐라** — 안 고치면 설계가 조용히 거짓이 된다.",
			len(offenders), scanned, updates, strings.Join(offenders, "\n  "), amendWriterFile)
	}
```

**⒟ 새 시험 둘을 더한다.**

```go
// TestAmendWriterAlsoWritesRevision 은 본문 UPDATE 와 개정 INSERT 가 **같은 함수** 안에
// 있는지 본다. 파일 단위로 세면 통과하는 구멍이 있다 — 같은 파일의 다른 함수가 이력
// 없이 UPDATE 만 치는 경우다.
func TestAmendWriterAlsoWritesRevision(t *testing.T) {
	root := itemBodyGuardRoot(t)
	path := filepath.Join(root, filepath.FromSlash(amendWriterFile))

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("%s 를 파싱 못 했다: %v", amendWriterFile, err)
	}

	var checked int
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			return true
		}
		var sql strings.Builder
		ast.Inspect(fn.Body, func(m ast.Node) bool {
			lit, ok := m.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, uerr := strconv.Unquote(lit.Value)
			if uerr != nil {
				s = lit.Value
			}
			sql.WriteString(s)
			sql.WriteString("\n")
			return true
		})
		body := sql.String()
		if len(itemBodyGuardHits(body)) == 0 {
			return true
		}
		checked++
		if !regexp.MustCompile(`(?is)INSERT\s+INTO\s+item_revision\s*\(`).MatchString(body) {
			t.Errorf("%s 의 %s 가 항목 본문을 고치면서 개정 이력을 안 쌓는다.\n"+
				"이력 없는 수정 경로는 item_revision 의 값을 통째로 무효로 만든다 — "+
				"같은 함수 안에서 INSERT INTO item_revision 을 함께 해라",
				amendWriterFile, fn.Name.Name)
		}
		return true
	})

	if checked == 0 {
		t.Fatalf("%s 에서 본문을 고치는 함수를 한 개도 못 찾았다 — "+
			"이 시험이 아무것도 안 보고 있다(함수가 옮겨졌거나 그물이 죽었다)", amendWriterFile)
	}
}

// TestAmendSurfaceIsNamedInDesign 은 §11 이 이 표면을 이름으로 부르는지 본다.
//
// 아래 문자열은 **앵커**다. 문서의 표현을 바꾸려면 이 시험도 같이 고쳐라.
func TestAmendSurfaceIsNamedInDesign(t *testing.T) {
	p := filepath.Join("..", "..", "..", "DESIGN.md")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("DESIGN.md 를 못 읽었다(%s) — 이 시험의 좌표가 틀렸다: %v", p, err)
	}
	design := string(b)

	for _, want := range []string{
		"항목 본문(`title`·`body`) 수정", // §11 표의 그 행이 아직 거기 있는가
		"열었다 (2026-09-16)",          // 그 행의 **지위** — 이제 열렸다
		"item_revision",              // 무엇이 그 값을 받치는가
	} {
		if strings.Contains(design, want) {
			continue
		}
		t.Errorf("코드에 항목 본문 수정 표면이 있는데 DESIGN 에 %q 가 없다 — "+
			"§11 이 무엇이 열렸고 무엇은 여전히 안 열렸는지를 말해야 한다. "+
			"안 적으면 다음 사람이 경로를 전부 평가하고서야 답에 도달한다", want)
	}
}
```

**임포트를 더해야 한다:** `go/ast`·`go/parser`·`go/token`·`strconv` 는 이미 있다. `regexp`·`os`·`strings`·`path/filepath`·`fmt` 도 있다. 없는 것만 더해라.

**⒠ `TestItemBodyGuardActuallyCatches` 의 `passed` 목록을 고친다.** 지금 이 목록에는 `UPDATE item_after SET body = ?` 가 있는데, 감시 컬럼에 `paths` 가 들어와도 `item_after` 는 표 이름 경계에 안 걸리므로 그대로 둔다. 다만 `caught` 에 한 줄을 더한다:

```go
		`UPDATE item SET paths = ? WHERE project = ? AND id = ?`,
```

- [ ] **Step 7: DESIGN §11 을 고친다**

`plugins/flightdeck/DESIGN.md` §11 표의 「항목 본문(`title`·`body`) 수정 — 일반 amend」 행을, `label` 행과 같은 꼴(취소선 + 열린 날짜 + 근거)로 바꾼다. **왼쪽 칸의 「항목 본문(`title`·`body`) 수정」 문자열은 앵커이므로 지우지 마라.**

```markdown
| ~~항목 본문(`title`·`body`) 수정 — 일반 amend~~ — **열었다 (2026-09-16)** | **판정의 지위가 원래 미결이었다** — 이 행은 "영구 결정으로 못박지 않는다"를 함께 적고, 열 때 물을 것까지 지정했다: "amend 를 만들까"가 아니라 **"열린 항목의 틀린 전제를 다음 `pick` 이 보게 하려면 무엇이 필요한가"**. 그 질문의 답이 실사용에서 나왔다 — 정정을 얹는 경로(`note`)가 네 자리에서 샌다: 원문이 정본처럼 읽힌다 · 제목만 나는 화면엔 안 보인다 · **닫힌 항목엔 안 닿는다**(큐가 `state='open'`) · 오타와 리네임 추종까지 판단 한 건을 쌓아 원장이 정정 잡음으로 불어난다. 그래서 `amend` 를 열되 **축 셋으로 못박는다** — `title`·`body`·`paths`. 옛 값은 `item_revision`(증분 016, 추가 전용)에 **고치기 직전의 값**으로 쌓이므로 복구 경로가 0이 아니다. **여전히 안 여는 것:** 판단 본문(트리거 그대로) · 선행(`item_after`, 아래 행) · `state`·`close_reason`. 부재를 지키던 관문은 지우지 않고 **유일 작성자 관문**으로 전환했다(`store/item_body_single_writer_test.go`) — §11 이 경고한 것은 "본문을 못 고치는 것"이 아니라 "표면마다 무엇을 고칠 수 있나가 갈리는 것"이고 그 경고는 지금 더 유효하다. 설계 정본은 `docs/superpowers/specs/2026-09-16-item-amend-surface-design.md` |
```

- [ ] **Step 8: 관문이 초록인지, 그리고 살아 있는지 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/store/ -run 'TestItemBody|TestAmend' -v
```
기대: 전부 PASS.

**변이 둘을 넣어 빨간불을 본다. 반드시 두 번 다 한다:**

1. `internal/store/item.go` 의 `SetLabels` 안 UPDATE 를 잠시 `UPDATE item SET labels = ?, title = ?` 로 바꾼다(인자도 하나 더해 컴파일되게) → `TestItemBodyHasSingleWriter` 가 빨개져야 한다. **그 시험이 잡았는지**를 이름으로 확인하고 되돌린다.
2. `amend.go` 의 `INSERT INTO item_revision` 문장을 잠시 주석 처리한다 → `TestAmendWriterAlsoWritesRevision` 이 빨개져야 한다. 되돌린다.

- [ ] **Step 9: 관문 다섯 줄 + 전체 시험**

```bash
cd plugins/flightdeck/server && [ -z "$(gofmt -l .)" ] && echo "gofmt ok" && go vet ./... && echo "vet ok"
```

```bash
cd plugins/flightdeck/server && go test ./internal/store/
```
(이 명령은 `timeout: 480000` 으로 돌려라.)

- [ ] **Step 10: 커밋**

```bash
git add -A plugins/flightdeck/server/internal/store plugins/flightdeck/DESIGN.md
git commit -m "$(cat <<'EOF'
feat(flightdeck): 항목 본문을 제자리에서 고친다 — 그리고 부재 관문을 유일 작성자 관문으로 바꾼다

store.AmendItem 이 title·body·paths 를 고치고 옛 값을 item_revision 에 쌓는다. 개정 행은
고치기 직전의 값을 담고, UPDATE 앞에 들어가며, 같은 트랜잭션이라 둘 다 커밋되거나 둘 다
안 된다. Changed 는 요청한 것이 아니라 실제로 값이 달라진 축이다.

label 과 달리 종료 상태를 안 본다. 닫힌 항목의 틀린 본문은 note 로도 못 닿고, 그 도달
불가가 이 표면을 연 근거 중 하나다.

item_body_immutable_test.go 를 지우지 않고 옮겨 전환했다. §11 이 경고한 것은 본문을 못
고치는 것이 아니라 표면마다 무엇을 고칠 수 있나가 갈리는 것이고, 그 경고는 표면이 열린
뒤에 더 유효하다. 새 명제는 둘이다 — 그 UPDATE 는 amend.go 한 곳이고, 같은 함수 안에
item_revision INSERT 가 있다. 둘째가 핵심이다: 이력 없는 수정 경로는 UPDATE 하나만 세는
그물에 안 걸리면서 이 표의 값을 통째로 무효로 만든다.

검증: 변이 둘로 빨간불을 확인했다 — SetLabels 에 title 을 끼우면 유일 작성자 관문이,
amend.go 의 revision INSERT 를 걷으면 동반 관문이 각각 잡는다.
EOF
)"
```

---

## Task 3: `service.AmendItem` — 검증·거절·겹침

**Files:**
- Create: `internal/service/amend.go`
- Create: `internal/service/amend_test.go`

**Interfaces:**
- Consumes: `store.AmendPatch`·`store.AmendRecord`·`(*store.Tx).AmendItem` (Task 2)
- Produces:
  ```go
  type AmendInput struct {
      Project, SessionID, ItemID string
      Title *string
      Body  *string
      Paths *[]string
      Reason string
  }
  type AmendResult struct {
      Item     model.Item      `json:"item"`
      Rev      int             `json:"rev"`
      Changed  []string        `json:"changed"`
      Before   model.Item      `json:"before"`
      Overlaps []judge.Overlap `json:"overlaps"`
      Derived
  }
  func (s *Service) AmendItem(ctx context.Context, in AmendInput) (AmendResult, error)
  ```
  Task 4·5·6 이 이 타입을 그대로 쓴다.

- [ ] **Step 1: 실패 시험을 쓴다**

`internal/service/amend_test.go`. 헬퍼는 `internal/service/label_test.go` 를 읽어 맞춘다.

```go
package service

import (
	"context"
	"strings"
	"testing"
)

// TestAmendRefusesEmptyPatch 는 축을 하나도 안 준 요청을 거절하는지 본다.
//
// MCP 에서 정상 도달 가능한 갈래다 — amend 도구의 필수 인자는 item_id·reason 뿐이라
// 셋을 다 안 준 호출이 그대로 여기까지 온다. RefusedError 가 아니면 500 으로 나가고
// 이 문구 대신 "서버 내부 오류다"만 사용자에게 간다.
func TestAmendRefusesEmptyPatch(t *testing.T) {
	s := newTestService(t)
	_, err := s.AmendItem(context.Background(), AmendInput{
		Project: "p1", ItemID: "i1", Reason: "r",
	})
	var re *RefusedError
	if !errorsAs(err, &re) { // ← 이 파일의 기존 관용구(errors.As)로 바꿔라
		t.Fatalf("RefusedError 가 아니다: %#v", err)
	}
	if !strings.Contains(re.Reason, "하나는") {
		t.Errorf("사유가 %q다 — 무엇을 줘야 하는지 말해야 한다", re.Reason)
	}
	if re.Guidance == "" {
		t.Error("Guidance 가 비었다 — 이 저장소는 거절에 처방을 함께 낸다")
	}
}

// TestAmendRefusesEmptyReason 은 사유 없는 수정을 거절하는지 본다.
func TestAmendRefusesEmptyReason(t *testing.T) {
	s := newTestService(t)
	title := "새 제목"
	_, err := s.AmendItem(context.Background(), AmendInput{
		Project: "p1", ItemID: "i1", Title: &title, Reason: "   ",
	})
	var re *RefusedError
	if !errorsAs(err, &re) {
		t.Fatalf("RefusedError 가 아니다: %#v", err)
	}
	if !strings.Contains(re.Reason, "사유") {
		t.Errorf("사유가 %q다", re.Reason)
	}
}

// TestAmendReportsActualChange 는 응답이 요청이 아니라 실제 변화분을 내는지 본다.
func TestAmendReportsActualChange(t *testing.T) {
	s := newTestService(t)
	seedItem(t, s, "p1", "i1", "원래 제목", "원래 본문") // ← 기존 시드 헬퍼로

	same := "원래 제목"
	res, err := s.AmendItem(context.Background(), AmendInput{
		Project: "p1", ItemID: "i1", Title: &same, Reason: "r",
	})
	if err != nil {
		t.Fatalf("고치지 못했다: %v", err)
	}
	if len(res.Changed) != 0 {
		t.Errorf("Changed 가 %v다 — 같은 값 재지정은 변화가 아니다", res.Changed)
	}
	if res.Rev != 1 {
		t.Errorf("rev 가 %d다 — 변화가 없어도 개정 행은 쌓인다(사유가 원장에 남아야 한다)", res.Rev)
	}
}

// TestAmendPathsReportsOverlaps 는 경로를 고쳤을 때 겹치게 된 세션이 응답에 오는지 본다.
//
// 이 축이 없으면 고친 사람이 남의 화면을 움직였다는 사실을 알 경로가 하나도 없다.
func TestAmendPathsReportsOverlaps(t *testing.T) {
	s := newTestService(t)
	seedItem(t, s, "p1", "i1", "t", "b")
	seedLiveSessionTouching(t, s, "p1", "다른세션", []string{"server/internal/api"}) // ← 기존 헬퍼로

	paths := []string{"server/internal/api"}
	res, err := s.AmendItem(context.Background(), AmendInput{
		Project: "p1", ItemID: "i1", Paths: &paths, Reason: "리네임 추종",
	})
	if err != nil {
		t.Fatalf("고치지 못했다: %v", err)
	}
	if len(res.Overlaps) == 0 {
		t.Error("겹침이 비었다 — 방금 남의 경로로 옮겨 놓고 그 사실을 응답이 안 낸다")
	}
}
```

- [ ] **Step 2: 실패 확인**

```bash
cd plugins/flightdeck/server && go test ./internal/service/ -run 'TestAmend' -v
```
기대: `undefined: AmendInput`.

- [ ] **Step 3: `internal/service/amend.go` 를 쓴다**

`internal/service/label.go` 를 **옆에 띄워 놓고** 같은 골격으로 쓴다.

```go
package service

import (
	"context"
	"strings"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// AmendInput 은 이미 있는 항목의 본문을 고치는 요청이다.
//
// ★ 범위는 **title·body·paths 셋**이다(설계 §3). labels 는 label 이 이미 가졌고,
// 선행·state·close_reason 은 안 연다 — "무엇을 고칠 수 있나"가 표면마다 갈리는 것이
// §11 이 경고한 실패이고, 그 경고는 표면이 열린 뒤에 더 유효하다.
//
// nil 은 "안 건드린다"다. 빈 문자열을 가리키는 포인터는 "빈 값으로 바꿔라"다.
type AmendInput struct {
	Project   string
	SessionID string
	ItemID    string
	Title     *string
	Body      *string
	Paths     *[]string
	Reason    string
}

// AmendResult 는 고친 결과다.
//
// ★ Changed 는 **요청한 것이 아니라 실제로 값이 달라진 축**이다(LabelResult 의
// Added·Removed 와 같은 규율). Rev 는 되돌릴 좌표라 응답에 싣는다.
//
// ★ Overlaps 는 paths 를 고쳤을 때만 뜻이 있다. 이 동사는 세 축 중 유일하게 **남의
// 화면을 움직이므로**, 고친 사람이 그 사실을 알 경로를 여기서 만든다.
type AmendResult struct {
	Item     model.Item      `json:"item"`
	Rev      int             `json:"rev"`
	Changed  []string        `json:"changed"`
	Before   model.Item      `json:"before"`
	Overlaps []judge.Overlap `json:"overlaps"`

	// Derived 는 쓰기 **뒤** 파생의 신선도다. 본문 고침은 되돌리는 코드가 없으므로
	// 겹침 계산이나 되읽기가 실패해도 결과를 버리지 않는다(DESIGN §5, LabelResult 와 같은 자리).
	Derived
}

// AmendItem 은 항목의 본문을 제자리에서 고친다.
//
// 읽기부터 쓰기까지가 한 트랜잭션이어야 한다 — 벌어지면 두 세션이 서로의 수정을
// 덮어쓴다(SetLabels 와 같은 이유로 store 의 Tx 를 직접 연다).
func (s *Service) AmendItem(ctx context.Context, in AmendInput) (AmendResult, error) {
	var res AmendResult
	in.Project = strings.TrimSpace(in.Project)
	in.ItemID = strings.TrimSpace(in.ItemID)
	in.Reason = strings.TrimSpace(in.Reason)

	if in.Project == "" {
		return res, &RefusedError{
			What:     "amend",
			Reason:   "프로젝트가 비었다",
			Guidance: "요청 본문의 project 를 채워라. CLI 는 `.flightdeck.yaml` 의 프로젝트를 자동으로 싣는다.",
		}
	}
	if err := s.GateTargetProject(ctx, in.SessionID, in.Project); err != nil {
		return res, err
	}
	if in.ItemID == "" {
		return res, &RefusedError{
			What:     "amend",
			Reason:   "고칠 항목 id 가 비었다",
			Guidance: "고칠 항목 id 를 줘라: `fd amend <item-id> --title/--body/--path … --reason <사유>`",
		}
	}
	// ★ 빈 요청을 **쓰기 전에** 거절한다. 서버까지 갔다 와도 같은 결론이지만 그 왕복은
	//   원장에 개정 행 하나를 남긴다 — 아무것도 안 바꾸는 개정이 이력에 쌓이면 나중에
	//   그 이력을 읽는 사람이 무엇이 일어났는지를 못 가린다.
	if in.Title == nil && in.Body == nil && in.Paths == nil {
		return res, &RefusedError{
			What:   "amend",
			Reason: "고칠 축을 하나는 줘라 — title·body·paths 중 하나도 안 주면 개정 이력만 늘어난다",
			Guidance: "--title·--body·--path 중 하나 이상을 줘라. 꼬리표는 `fd label` 이고, " +
				"선행과 상태는 이 동사가 안 고친다.",
		}
	}
	if in.Reason == "" {
		return res, &RefusedError{
			What:   "amend",
			Reason: "고친 사유가 비었다 — 사유 없는 수정은 나중에 되짚을 수 없다",
			Guidance: "--reason 에 한 구절이면 된다(\"경로 리네임 추종\" · \"전제가 틀렸다\"). " +
				"이 값은 개정 이력에 남고, 되짚을 사람이 그 표를 여는 유일한 이유가 그것이다.",
		}
	}

	var rec store.AmendRecord
	err := s.st.Tx(ctx, func(t *store.Tx) error {
		var e error
		rec, e = t.AmendItem(in.Project, in.ItemID, store.AmendPatch{
			Title: in.Title, Body: in.Body, Paths: in.Paths, Reason: in.Reason,
		}, in.SessionID)
		return e
	})
	if err != nil {
		return res, err
	}

	res = AmendResult{
		Rev: rec.Rev, Changed: rec.Changed, Before: rec.Before, Item: rec.After,
	}

	// ★ 여기서부터는 **쓰기 뒤 파생**이다. 실패해도 결과를 버리지 않는다 — 쓰기는
	//   이미 커밋됐고 되돌리는 코드가 없다(DESIGN §5).
	d := &derive{}

	// 저장된 값을 다시 읽는다 — 요청 값을 그대로 돌려주면 무엇이 저장됐는지가 아니라
	// 무엇을 보냈는지를 화면에 내게 된다.
	if it, gerr := s.st.GetItem(ctx, in.Project, in.ItemID); gerr != nil {
		s.log.WarnContext(ctx, "본문 고친 뒤 되읽기 실패 — 쓰기는 커밋됐다",
			"project", clip(in.Project, 64), "item", clip(in.ItemID, 64), "error", gerr.Error())
		d.fail("item", gerr)
	} else {
		res.Item = it
	}

	// ★ 겹침은 paths 를 **실제로 바꿨을 때만** 센다. 안 바꿨으면 이 항목의 겹침은
	//   이 수정의 결과가 아니라 원래 있던 사실이고, 그것을 여기 내면 고친 사람은
	//   자기가 방금 만든 겹침이라고 읽는다.
	if containsString(rec.Changed, "paths") { // ★ 이 헬퍼는 landing.go:1004 에 이미 있다
		live, lerr := s.liveSessions(ctx, in.Project) // ← pick.go 가 live 를 얻는 그 경로로 맞춰라
		if lerr != nil {
			s.log.WarnContext(ctx, "겹침을 못 셌다 — 수정은 커밋됐다",
				"project", clip(in.Project, 64), "item", clip(in.ItemID, 64), "error", lerr.Error())
			d.fail("overlaps", lerr)
		} else {
			res.Overlaps = judge.OverlapsWithLive(res.Item.Paths, live, in.SessionID, "")
		}
	}

	res.Derived = d.result(s.now())
	return res, nil
}
```

**맞춰야 할 것 셋:**
1. `s.liveSessions(ctx, project)` 는 **가정한 이름이다.** `internal/service/pick.go:457` 근처에서 `live` 를 어떻게 얻는지 읽고 그 경로를 그대로 써라. `selfCC` 인자도 거기서 무엇을 넘기는지 보고 맞춰라.
2. **`containsString` 을 새로 만들지 마라.** `internal/service/landing.go:1004` 에 이미 있다 — 같은 패키지라 중복 선언으로 컴파일이 깨진다. 그대로 부르면 된다.
3. `derive`·`d.fail`·`d.result`·`s.now`·`clip` 은 이 패키지에 있다 — `label.go` 와 같은 사용법이다.

- [ ] **Step 4: 통과 확인**

```bash
cd plugins/flightdeck/server && go test ./internal/service/ -run 'TestAmend' -v
```

- [ ] **Step 5: 관문 + 커밋**

```bash
cd plugins/flightdeck/server && [ -z "$(gofmt -l .)" ] && go vet ./...
```

```bash
git add plugins/flightdeck/server/internal/service/amend.go plugins/flightdeck/server/internal/service/amend_test.go
git commit -m "$(cat <<'EOF'
feat(flightdeck): amend 의 검증과 겹침 파생을 붙인다

거절 넷은 전부 RefusedError 다 — errors.New 는 ClassifyError 화이트리스트에 안 걸려
500 으로 나가고, 이 갈래들은 MCP 에서 정상 도달 가능하다(필수 인자가 item_id·reason 뿐이라
축을 하나도 안 준 호출이 그대로 온다).

빈 요청을 쓰기 전에 거절한다. 아무것도 안 바꾸는 개정이 이력에 쌓이면 그 이력을 읽는
사람이 무엇이 일어났는지를 못 가린다.

겹침은 paths 를 실제로 바꿨을 때만 센다. 안 바꿨는데 내면 고친 사람은 원래 있던 겹침을
자기가 방금 만든 것으로 읽는다. 세다 실패해도 결과는 안 버린다 — 쓰기는 커밋됐고
되돌리는 코드가 없다.
EOF
)"
```

---

## Task 4: REST 라우트

**Files:**
- Modify: `internal/api/api.go` (라우트 등록)
- Modify: `internal/api/handlers_items.go` (요청 타입 + 핸들러)
- Test: `internal/api/http_test.go` 에 붙이거나 `internal/api/amend_test.go` 신규

**Interfaces:**
- Consumes: `service.AmendInput`·`service.AmendResult` (Task 3)
- Produces: `POST /api/v1/items/{id}/amend`, 요청 필드 `project`·`session_id`·`title`·`body`·`paths`·`reason`·`item_project`. Task 6 의 `cmd/fd/wire.go` 가 **이 JSON 태그와 글자까지 같아야 한다.**

- [ ] **Step 1: 실패 시험을 쓴다**

`internal/api/amend_test.go`. 헬퍼는 `internal/api/helper_test.go` 와 `label_seam_test.go` 를 읽어 맞춘다.

```go
package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestAmendRouteExists 는 라우트가 붙었고 service 로 값이 그대로 넘어가는지 본다.
func TestAmendRouteExists(t *testing.T) {
	srv, fake := newTestServer(t) // ← 기존 헬퍼 이름으로

	body := map[string]any{
		"project": "p1", "session_id": "s1",
		"title": "새 제목", "reason": "오타",
	}
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/items/i1/amend", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("상태가 %d다 — 200 이어야 한다. 본문: %s", rec.Code, rec.Body.String())
	}
	if fake.lastAmend.ItemID != "i1" {
		t.Errorf("service 가 받은 item_id 가 %q다 — 경로의 {id} 가 안 넘어갔다", fake.lastAmend.ItemID)
	}
	if fake.lastAmend.Title == nil || *fake.lastAmend.Title != "새 제목" {
		t.Errorf("title 이 안 넘어갔다: %#v", fake.lastAmend.Title)
	}
	if fake.lastAmend.Body != nil {
		t.Errorf("안 준 body 가 %#v 로 넘어갔다 — 생략은 nil 이어야 한다", fake.lastAmend.Body)
	}
}

// TestAmendDistinguishesOmittedFromEmpty 는 생략과 빈 문자열이 갈리는지 본다.
//
// 이 축이 무너지면 "본문을 비워라"가 원리적으로 불가능해지거나, 반대로 title 만 고치려던
// 요청이 본문을 통째로 지운다.
func TestAmendDistinguishesOmittedFromEmpty(t *testing.T) {
	srv, fake := newTestServer(t)

	raw := []byte(`{"project":"p1","session_id":"s1","body":"","reason":"r"}`)
	rec := doRaw(t, srv, http.MethodPost, "/api/v1/items/i1/amend", raw)
	if rec.Code != http.StatusOK {
		t.Fatalf("상태가 %d다. 본문: %s", rec.Code, rec.Body.String())
	}
	if fake.lastAmend.Body == nil {
		t.Fatal("빈 문자열로 준 body 가 nil 로 왔다 — 생략과 안 갈린다")
	}
	if *fake.lastAmend.Body != "" {
		t.Errorf("body 가 %q다 — 빈 문자열이어야 한다", *fake.lastAmend.Body)
	}
	if fake.lastAmend.Title != nil {
		t.Error("안 준 title 이 non-nil 로 왔다")
	}
}

// TestAmendResponseCarriesRevAndChanged 는 응답이 되돌릴 좌표와 실제 변화분을 내는지 본다.
func TestAmendResponseCarriesRevAndChanged(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/items/i1/amend", map[string]any{
		"project": "p1", "session_id": "s1", "title": "새 제목", "reason": "r",
	})
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("응답을 못 읽었다: %v", err)
	}
	for _, k := range []string{"rev", "changed", "item"} {
		if _, ok := got[k]; !ok {
			t.Errorf("응답에 %q 가 없다 — 되돌릴 좌표와 실제 변화분이 화면에 나야 한다", k)
		}
	}
}
```

**`fake` 백엔드에 `lastAmend` 를 더해야 한다** — `helper_test.go` 의 기존 fake 가 `SetLabels` 를 어떻게 기록하는지 보고 같은 모양으로 `AmendItem` 을 더해라.

- [ ] **Step 2: 실패 확인**

```bash
cd plugins/flightdeck/server && go test ./internal/api/ -run 'TestAmend' -v
```

- [ ] **Step 3: 요청 타입과 핸들러를 쓴다**

`internal/api/handlers_items.go` 의 `handleLabelItem` **바로 뒤**에 붙인다.

```go
// amendRequest 는 항목 본문을 고치는 요청이다.
//
// label·move·after/cut 과 같은 규율로 **전용 동사**다 — 일반 PATCH 를 열면 "무엇까지
// 고칠 수 있나"가 다시 열린 질문이 된다. 이 동사가 무는 축은 셋으로 못박혀 있고
// (DESIGN §11, 2026-09-16 에 열렸다) 그 좁기를 store 의 유일 작성자 관문이 지킨다.
//
// ★ 포인터 셋이 핵심이다. **생략과 "빈 값으로 바꿔라"를 가른다** — 값 타입으로 받으면
// title 만 고치려던 요청이 본문을 통째로 지운다.
//
// 필드 이름이 cmd/fd 의 amendReq 와 어긋나면 서버가 조용히 0값을 받는다(이음매 시험이 잠근다).
type amendRequest struct {
	Project     string    `json:"project"`
	SessionID   string    `json:"session_id"`
	Title       *string   `json:"title"`
	Body        *string   `json:"body"`
	Paths       *[]string `json:"paths"`
	Reason      string    `json:"reason"`
	ItemProject string    `json:"item_project"`
}

func (s *server) handleAmendItem(w http.ResponseWriter, r *http.Request) {
	var req amendRequest
	if !s.decode(w, r, &req) {
		return
	}
	infoFrom(r.Context()).setSession(req.SessionID)
	res, err := s.svc.AmendItem(r.Context(), service.AmendInput{
		Project: req.Project, SessionID: req.SessionID,
		ItemID: r.PathValue("id"),
		Title:  req.Title, Body: req.Body, Paths: req.Paths,
		Reason: req.Reason,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// ★ SSE 알림용이다. 원장 행 자체는 store 가 트랜잭션 안에서 남긴다(item.amend) —
	// before 를 아는 것이 거기뿐이기 때문이다. 여기서 다시 publish 하면 같은 사실이
	// 원장에 두 줄이 되므로, 이 호출은 **알림 축만** 태운다.
	s.publish(r, "item.amend", req.Project, req.SessionID, map[string]any{
		"item": clip(res.Item.ID, 100), "rev": res.Rev, "changed": res.Changed,
	})
	s.writeJSON(w, r, http.StatusOK, res)
}
```

**`ItemProject` 는 지금 안 쓴다.** `service.AmendInput` 에 그 축이 없기 때문이다 — 필드만 받아 두고 넘기지 않으면 조용한 무시가 된다. **둘 중 하나를 골라라:** ⓐ 필드를 지운다(권장 — 이 동사는 프로젝트를 cwd 로 정한다), ⓑ `service.AmendInput` 에 축을 더하고 `resolveItemProject` 를 태운다. ⓐ 를 고르면 위 구조체에서 그 줄과 아래 CLI 의 `--item-project` 플래그를 함께 뺀다.

- [ ] **Step 4: 라우트를 등록한다**

`internal/api/api.go` 의 `POST /api/v1/items/{id}/label` 바로 아래:

```go
	mux.HandleFunc("POST /api/v1/items/{id}/amend", s.handleAmendItem)
```

- [ ] **Step 5: 통과 확인 + 라우트 표 관문**

```bash
cd plugins/flightdeck/server && go test ./internal/api/ -run 'TestAmend|TestDesignRouteTable' -v
```

`TestDesignRouteTable` 이 빨개지면 DESIGN §6 의 REST 표에 이 라우트를 더해야 한다는 뜻이다. 그 시험의 메시지가 어느 좌표를 요구하는지 읽고 거기에 적어라.

- [ ] **Step 6: 관문 + 커밋**

```bash
cd plugins/flightdeck/server && [ -z "$(gofmt -l .)" ] && go vet ./...
git add plugins/flightdeck/server/internal/api plugins/flightdeck/DESIGN.md
git commit -m "$(cat <<'EOF'
feat(flightdeck): POST /items/{id}/amend 를 연다

포인터 셋이 생략과 빈 값을 가른다. 값 타입으로 받으면 title 만 고치려던 요청이 본문을
통째로 지운다.

원장은 store 가 트랜잭션 안에서 남기므로(item.amend) 여기 publish 는 SSE 알림 축만
태운다 — 여기서 또 남기면 같은 사실이 원장에 두 줄이 된다.
EOF
)"
```

---

## Task 5: MCP 9번째 도구와 렌더러

**Files:**
- Create: `internal/mcpsrv/render_amend.go`
- Create: `internal/mcpsrv/render_amend_test.go`
- Modify: `internal/mcpsrv/tools.go` (도구 정의 + `amendArgs`)
- Modify: `internal/mcpsrv/mcpsrv.go` (디스패치 + `toolAmend`)
- Modify: `internal/mcpsrv/backend.go` (인터페이스)
- Modify: `internal/mcpsrv/protocol_test.go` (`TestToolTableIsEight` → `…IsNine`)
- Modify: `plugins/flightdeck/DESIGN.md` §6 MCP 표

**Interfaces:**
- Consumes: `service.AmendInput`·`service.AmendResult` (Task 3)
- Produces: `func RenderAmend(res service.AmendResult) string` — Task 6 의 CLI 가 이것을 그대로 부른다. `Backend` 인터페이스에 `AmendItem(ctx, service.AmendInput) (service.AmendResult, error)`.

- [ ] **Step 1: 렌더러 시험을 쓴다**

`internal/mcpsrv/render_amend_test.go`. **단정은 소비자의 좌표계** — 응답 문자열 실물이다.

```go
package mcpsrv

import (
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// TestRenderAmendNamesActualChange 는 실제로 바뀐 축만 말하는지 본다.
func TestRenderAmendNamesActualChange(t *testing.T) {
	got := RenderAmend(service.AmendResult{
		Item:    model.Item{ID: "i1", Title: "새 제목"},
		Rev:     3,
		Changed: []string{"title"},
	})
	if !strings.Contains(got, "title") {
		t.Errorf("바뀐 축을 안 말한다:\n%s", got)
	}
	if strings.Contains(got, "body") {
		t.Errorf("안 바뀐 축을 말한다:\n%s", got)
	}
	if !strings.Contains(got, "3") {
		t.Errorf("rev 가 없다 — 되돌릴 좌표가 그 자리에서 나와야 한다:\n%s", got)
	}
}

// TestRenderAmendSaysNothingChanged 는 무변화를 **무변화로** 말하는지 본다.
//
// "고쳤다"고만 말하면 사람은 안 바뀐 것을 바뀐 줄 안다 — RenderLabel 이 같은 이유로
// 같은 것을 한다.
func TestRenderAmendSaysNothingChanged(t *testing.T) {
	got := RenderAmend(service.AmendResult{
		Item: model.Item{ID: "i1"}, Rev: 1, Changed: nil,
	})
	if !strings.Contains(got, "안 바뀌었다") {
		t.Errorf("무변화를 무변화로 안 말한다:\n%s", got)
	}
}

// TestRenderAmendWarnsOnPathsChange 는 경로를 고쳤을 때 겹침 파급을 그 자리에서 내는지 본다.
func TestRenderAmendWarnsOnPathsChange(t *testing.T) {
	got := RenderAmend(service.AmendResult{
		Item: model.Item{ID: "i1", Paths: []string{"a"}},
		Rev:  1, Changed: []string{"paths"},
		Overlaps: []judgeOverlapStub(), // ← judge.Overlap 실제 타입으로 채워라
	})
	if !strings.Contains(got, "겹") {
		t.Errorf("경로를 고쳤는데 겹침 파급을 안 낸다:\n%s", got)
	}
}

// TestRenderAmendSaysOverlapUnknown 은 못 센 것과 0을 가르는지 본다.
func TestRenderAmendSaysOverlapUnknown(t *testing.T) {
	res := service.AmendResult{
		Item: model.Item{ID: "i1"}, Rev: 1, Changed: []string{"paths"},
	}
	markDeriveFailed(&res, "overlaps") // ← Derived 를 실패 상태로 만드는 기존 관용구로
	got := RenderAmend(res)
	if !strings.Contains(got, "못 셌다") {
		t.Errorf("못 센 것을 0으로 낸다 — 둘은 다른 사실이다:\n%s", got)
	}
}
```

**`judgeOverlapStub`·`markDeriveFailed` 는 자리표시다.** `internal/mcpsrv/render_label_test.go` 와 `render.go` 에서 `Overlap` 과 `Derived` 를 시험이 어떻게 채우는지 읽고 그 관용구로 바꿔라.

- [ ] **Step 2: 실패 확인**

```bash
cd plugins/flightdeck/server && go test ./internal/mcpsrv/ -run 'TestRenderAmend' -v
```

- [ ] **Step 3: `internal/mcpsrv/render_amend.go` 를 쓴다**

`internal/mcpsrv/render_label.go` 를 옆에 두고 같은 문체로 쓴다.

```go
package mcpsrv

import (
	"fmt"
	"strings"

	"github.com/kweiza/flightdeck/internal/service"
)

// RenderAmend 는 본문 수정의 응답이다.
//
// ★ **이 동사의 규율은 전부 여기 있다.** 도구 설명(90자)에도 스킬에도 안 넣는다 —
// 세션 시작 컨텍스트는 이름 하나만 받고, 규율은 필요할 때 그 자리에서만 실린다.
// 여기 실리는 것 셋: ⓐ 실제 변화분(요청이 아니라) ⓑ 되돌릴 좌표(rev) ⓒ 경로를
// 고쳤을 때의 겹침 파급.
func RenderAmend(res service.AmendResult) string {
	var b strings.Builder

	if len(res.Changed) == 0 {
		fmt.Fprintf(&b, "amend · %s — **아무것도 안 바뀌었다**(준 값이 지금 값과 같다)\n",
			res.Item.ID)
	} else {
		fmt.Fprintf(&b, "amend · %s 의 %s 를 고쳤다\n",
			res.Item.ID, strings.Join(res.Changed, "·"))
	}
	fmt.Fprintf(&b, "개정 %d — 옛 값은 그대로 남는다(item_revision 은 추가 전용이다)\n", res.Rev)

	if containsAxis(res.Changed, "title") {
		fmt.Fprintf(&b, "제목: %s\n", res.Item.Title)
	}
	if containsAxis(res.Changed, "paths") {
		fmt.Fprintf(&b, "경로 %d: %s\n", len(res.Item.Paths), strings.Join(res.Item.Paths, ", "))
		// ★ 이 축은 **남의 화면을 움직인다.** 고친 사람이 그 사실을 알 다른 경로가 없다.
		switch {
		case deriveFailed(res.Derived, "overlaps"):
			b.WriteString("겹침: 이 수정으로 겹치게 된 세션을 **못 셌다** — 0이라는 뜻이 아니다. " +
				"`board` 가 그 축을 다시 읽는다\n")
		case len(res.Overlaps) == 0:
			b.WriteString("겹침: 지금 이 경로를 만지는 다른 세션은 없다\n")
		default:
			fmt.Fprintf(&b, "겹침: 이 수정으로 **세션 %d개와 경로가 겹친다** — "+
				"경로는 겹침 판정의 입력이라 남의 화면도 함께 움직였다\n", len(res.Overlaps))
		}
	}

	return b.String()
}
```

**`containsAxis` 는 `internal/mcpsrv/identity.go:466` 에 이미 있다** — 새로 만들지 마라(같은 패키지다). 이름도 맞다: `Changed` 가 담는 것이 축 목록이다.

**`deriveFailed(res.Derived, "overlaps")` 는 가정한 이름이다.** `render.go` 에서 파생 실패를 어떻게 읽는지(`Derived` 의 실제 필드·메서드) 확인하고 맞춰라. 같은 이름의 헬퍼가 이미 있으면 새로 만들지 마라.

- [ ] **Step 4: 도구를 배선한다**

**⒜ `internal/mcpsrv/tools.go`** — `label` 도구 정의 **뒤**에:

```go
	{
		Name:        "amend",
		Description: "항목의 제목·본문·경로를 고친다. 옛 값은 개정 이력에 남는다.",
		InputSchema: obj(map[string]any{
			"item_id": str("고칠 항목 id"),
			"title":   str("새 제목(안 주면 안 고친다)"),
			"body":    str("새 본문(안 주면 안 고친다)"),
			"paths":   strArr("새 경로 목록 — **통째로 교체한다**. 겹침 판정의 입력이라 남의 화면도 움직인다"),
			"reason":  str("왜 고치나. 개정 이력에 남는다"),
			"project": projectArg(),
		}, "item_id", "reason"),
	},
```

그리고 같은 파일의 args 구조체 자리에:

```go
type amendArgs struct {
	ItemID  string    `json:"item_id"`
	Title   *string   `json:"title"`
	Body    *string   `json:"body"`
	Paths   *[]string `json:"paths"`
	Reason  string    `json:"reason"`
	Project string    `json:"project"`
}
```

**⒝ `internal/mcpsrv/backend.go`** — `SetLabels` 선언 뒤:

```go
	// AmendItem 은 항목의 제목·본문·경로를 고치고 옛 값을 개정 이력에 남긴다.
	// 고칠 수 있는 축은 그 셋뿐이다(DESIGN §11).
	AmendItem(ctx context.Context, in service.AmendInput) (service.AmendResult, error)
```

**⒞ `internal/mcpsrv/mcpsrv.go`** — `case "label":` 뒤에:

```go
	case "amend":
		res = s.toolAmend(ctx, sessionID, args)
```

그리고 `toolLabel` 뒤에:

```go
// toolAmend 는 항목의 제목·본문·경로를 고친다.
//
// 축 셋으로 못박혀 있다 — 꼬리표는 label 이고, 선행과 상태는 이 동사가 안 고친다.
func (s *Server) toolAmend(ctx context.Context, sessionID string, raw json.RawMessage) toolResult {
	var a amendArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return textResult(s.withTail(ctx, s.errText("amend", err), tailOpts{}), true)
	}
	res, err := s.be.AmendItem(ctx, service.AmendInput{
		Project:   s.projectOf(sessionID, a.Project), // ← toolLabel 이 project 를 푸는 그 방식으로
		SessionID: sessionID,
		ItemID:    a.ItemID,
		Title:     a.Title, Body: a.Body, Paths: a.Paths,
		Reason: a.Reason,
	})
	if err != nil {
		if r, ok := s.degradedResult(ctx, "amend", err); ok {
			return r
		}
		return textResult(s.withTail(ctx, s.errText("amend", err), tailOpts{}), true)
	}
	return textResult(s.withTail(ctx, RenderAmend(res), tailOpts{}), false)
}
```

**`s.projectOf(...)` 는 가정한 이름이다** — `toolLabel`(`mcpsrv.go:1082` 근처)이 `service.LabelInput.Project` 를 어떻게 채우는지 그대로 베껴라.

**⒟ 세션 귀속 목록.** `mcpsrv.go:11` 의 주석("세션 귀속이 필요한 도구(pick·note·add·finish·land·label)를 거절한다")과 **그 주석이 설명하는 실제 목록**에 `amend` 를 더한다. 목록 상수를 `grep -n "label" internal/mcpsrv/identity.go` 로 찾아라.

- [ ] **Step 5: 도구 표 관문을 고친다**

`internal/mcpsrv/protocol_test.go`:

```go
func TestToolTableIsNine(t *testing.T) {
	got := ToolNames()
	want := []string{"board", "pick", "note", "add", "finish", "alloc", "land", "label", "amend"}
	if len(got) != len(want) {
		t.Fatalf("도구가 %d개다(%v) — 항목 본문 수정 표면이 amend 를 더해 9개다", len(got), got)
	}
	…이하 기존과 동일…
```

**같은 파일 안에서 `TestToolTableIsEight` 라는 이름을 참조하는 다른 시험·주석이 있는지 찾아라:**
```bash
grep -rn "TestToolTableIsEight" plugins/flightdeck
```
`DESIGN.md` 본문은 그 이름을 **3곳**에서 인용한다(실측) — 전부 고친다.

- [ ] **Step 6: DESIGN §6 을 고친다**

1. `### MCP 도구 8개` → `### MCP 도구 9개`
2. 표 마지막에 행 하나:

```markdown
| `amend` | `item_id`, `title?`, `body?`, `paths?`, `reason` | 이미 있는 항목의 **제목·본문·경로**를 고친다 — 고칠 수 있는 축은 그 셋뿐이다(꼬리표는 `label`, 선행·상태는 못 바꾼다, §11). 준 것만 고치고 응답은 요청이 아니라 **실제 변화분**을 낸다. 옛 값은 `item_revision` 에 **고치기 직전의 값**으로 쌓이고 그 표는 추가 전용이다. `paths` 를 고치면 **겹침 판정의 입력이 바뀌므로** 그 파급(겹치게 된 세션 수 · 또는 못 셌다는 사실)을 그 자리에서 낸다 |
```
3. 8→9 로 늘어난 값을 적은 문단에 `label` 문장과 같은 꼴로 한 줄을 더한다:

```markdown
8개에서 9개(`amend`)도 같다 — 늘어난 것은 이름 하나이고, 그 설명도 90자 상한
(`mcpsrv/protocol_test.go` 의 `TestToolTableIsNine`)을 그대로 지킨다. 이 도구가 나르는
규율(실제 변화분 · 되돌릴 좌표 · 경로 수정의 겹침 파급)은 도구 설명이 아니라 **응답**
(`RenderAmend`)에만 있다.
```
4. `TestToolTableIsEight` 인용 **3곳**을 `TestToolTableIsNine` 으로 고친다(`grep -c` 로 0이 될 때까지 확인하라).
5. **`note` 행의 인자에 `supersedes` 를 더한다** — 코드에 이미 있는데 표에 없었다:

```markdown
| `note` | `kind`, `body`, `item_id?`, `supersedes?` | … 기존 설명 … **`supersedes` 는 정정 대상 판단 id 다 — 덮어쓰기는 없고 새 행이 옛 행을 가리킨다** |
```

- [ ] **Step 7: 통과 확인**

```bash
cd plugins/flightdeck/server && go test ./internal/mcpsrv/ -v
```
(`timeout: 480000`)

- [ ] **Step 8: 관문 + 커밋**

```bash
cd plugins/flightdeck/server && [ -z "$(gofmt -l .)" ] && go vet ./...
git add plugins/flightdeck/server/internal/mcpsrv plugins/flightdeck/DESIGN.md
git commit -m "$(cat <<'EOF'
feat(flightdeck): MCP 에 amend 를 더한다 — 도구가 8→9 다

늘어난 고정비는 이름 하나다. 규율은 전부 응답(RenderAmend)에 있다 — 실제 변화분, 되돌릴
좌표(rev), 그리고 경로를 고쳤을 때의 겹침 파급.

겹침은 못 센 것과 0을 가른다. 경로는 겹침 판정의 입력이라 이 수정이 남의 화면을 움직이고,
고친 사람이 그 사실을 알 다른 경로가 없다.

DESIGN §6 의 note 행에 supersedes 를 함께 적었다 — 코드에는 있는데 표에 없었다.
EOF
)"
```

---

## Task 6: CLI `fd amend`

**Files:**
- Modify: `cmd/fd/cmds.go` (`runAmend`)
- Modify: `cmd/fd/main.go` (디스패치)
- Modify: `cmd/fd/wire.go` (`amendReq`·`amendPath`)
- Modify: `cmd/fd/offline.go` (`CmdAmend` + `JudgeOffline` 갈래)
- Modify: `cmd/fd/mcpbackend.go` (`AmendItem` — MCP 서버가 CLI 를 통해 도는 경로)
- Modify: `plugins/flightdeck/DESIGN.md` §6 CLI 목록
- Test: `cmd/fd/amend_seam_test.go` 신규 + `cmd/fd/offline_test.go`

**Interfaces:**
- Consumes: `POST /api/v1/items/{id}/amend` 의 JSON 필드(Task 4) · `mcpsrv.RenderAmend`(Task 5)
- Produces: `CmdAmend = "amend"` 상수 — 없음(마지막 소비자)

- [ ] **Step 1: 이음매 시험과 오프라인 시험을 쓴다**

`cmd/fd/amend_seam_test.go`. `cmd/fd/label_seam_test.go` 를 그대로 베껴 amend 로 바꾼다 — 그 시험이 재는 것은 **CLI 요청 타입과 서버 요청 타입의 JSON 태그가 글자까지 같은가**다. 어긋나면 서버가 조용히 0값을 받는다.

`cmd/fd/offline_test.go` 에 더한다:

```go
// TestAmendIsRefusedOffline 은 amend 가 아웃박스에 안 쌓이는지 본다.
//
// ★ note 와 다르다. amend 는 읽고-고치는 쓰기라, 재생 시점의 현재 값이 쌓을 때와 다르면
// item_revision 이 **거짓 이전값**을 담는다. 옛 값을 지키려 만든 표가 거짓을 담는 것이
// 이 기능의 최악 실패다.
func TestAmendIsRefusedOffline(t *testing.T) {
	v := JudgeOffline(CmdAmend)
	if v.Mode != OfflineRefuse {
		t.Fatalf("열화 처방이 %q다 — 거절이어야 한다", v.Mode)
	}
	if !strings.Contains(v.Reason, "이전값") && !strings.Contains(v.Reason, "옛 값") {
		t.Errorf("사유가 %q다 — 왜 재생이 위험한지를 말해야 한다", v.Reason)
	}
	// 둘째 방어도 같은 답을 내야 한다 — 두 정책이 어긋나면 그 자체가 사고다.
	if ok, _ := OutboxEligible(CmdAmend, "/api/v1/items/i1/amend"); ok {
		t.Error("아웃박스 적격으로 판정했다 — 적격은 note 하나뿐이다")
	}
}
```

- [ ] **Step 2: 실패 확인**

```bash
cd plugins/flightdeck/server && go test ./cmd/fd/ -run 'TestAmend' -v
```

- [ ] **Step 3: `wire.go` 에 경로와 요청 타입을 더한다**

```go
// amendPath 는 POST /api/v1/items/{id}/amend 의 경로다.
func amendPath(itemID string) string {
	return "/api/v1/items/" + urlPath(itemID) + "/amend"
}

// amendReq 는 그 본문이다.
// 필드 이름이 internal/api 의 amendRequest 와 어긋나면 서버가 조용히 0값을 받는다
// (amend_seam_test.go 가 잠근다).
//
// ★ 포인터 셋은 **생략과 빈 값을 가르기 위한 것**이다. omitempty 를 붙이지 마라 —
// 빈 문자열을 가리키는 포인터가 통째로 사라져 "본문을 비워라"가 원리적으로 불가능해진다.
type amendReq struct {
	Project   string    `json:"project"`
	SessionID string    `json:"session_id"`
	Title     *string   `json:"title"`
	Body      *string   `json:"body"`
	Paths     *[]string `json:"paths"`
	Reason    string    `json:"reason"`
}
```

- [ ] **Step 4: `offline.go` 에 상수와 갈래를 더한다**

```go
	// CmdAmend 는 이미 있는 항목의 본문을 고치는 것이다(`fd amend`).
	CmdAmend = "amend"
```

`JudgeOffline` 의 `case "add":` 뒤에:

```go
	case CmdAmend:
		return OfflineVerdict{OfflineRefuse,
			"본문 수정은 읽고-고치는 쓰기다 — 재생 시점의 현재 값이 쌓을 때와 다르면 " +
				"개정 이력이 거짓 이전값을 담는다. 옛 값을 지키려 만든 표가 거짓을 담는 것이 최악이다"}
```

- [ ] **Step 5: `runAmend` 를 쓴다**

`cmd/fd/cmds.go` 의 `runLabel` **뒤**에. `runLabel` 과 `runAdd` 를 옆에 두고 쓴다.

```go
// runAmend 는 `fd amend` 다.
//
// ★ 고칠 수 있는 축은 title·body·paths **셋**이다(DESIGN §11, 2026-09-16 에 열렸다).
// 꼬리표는 `fd label` 이고 선행·상태는 이 동사가 안 고친다.
//
// ★ 오프라인에서 거절된다 — 재생 시점의 값이 달라지면 개정 이력이 거짓을 담는다.
func (a *App) runAmend(ctx context.Context, args []string, out io.Writer) int {
	fs := newFlagSet("amend")
	project := fs.String("project", "", "워크스페이스 멤버 프로젝트에 건다(비면 이 세션의 것). 명부 밖 이름은 서버가 거절한다")
	title := fs.String("title", "", "새 제목(안 주면 안 고친다)")
	body := fs.String("body", "", "새 본문(- 이면 stdin 에서 읽는다. 안 주면 안 고친다)")
	var paths stringList
	fs.Var(&paths, "path", "새 경로(반복 지정 가능). **한 번이라도 주면 목록 전체를 이것으로 바꾼다**")
	reason := fs.String("reason", "", "왜 고치나(필수). 개정 이력에 남는다")
	session := fs.String("cc-session", "", "Claude Code 세션 id")
	itemID, rest := TakeFirstPositional(args)
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if itemID == "" {
		itemID = fs.Arg(0)
	}
	if strings.TrimSpace(itemID) == "" {
		fmt.Fprintln(out, "고칠 항목 id 를 줘라:")
		fmt.Fprintln(out, "  "+amendHelp)
		return 2
	}

	// ★ 어느 플래그가 **실제로 주어졌는가**를 본다. fs.Visit 는 준 것만 돈다 —
	//   기본값 비교로 가르면 `--title ""`(빈 제목으로 바꿔라)가 생략과 안 갈린다.
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })

	req := amendReq{Reason: strings.TrimSpace(*reason)}
	if given["title"] {
		t := *title
		req.Title = &t
	}
	if given["body"] {
		text := a.resolveBody(*body) // `-` 면 stdin
		req.Body = &text
	}
	if given["path"] {
		p := []string(paths)
		req.Paths = &p
	}

	// ★ 빈 요청과 빈 사유를 **여기서** 막는다. 서버도 거절하지만 그 왕복은 오프라인에서
	//   미도달 오류가 되어 사용자에게 "서버에 못 닿았다"로 보인다 — 실제 원인은 인자다.
	if req.Title == nil && req.Body == nil && req.Paths == nil {
		fmt.Fprintln(out, "고칠 축을 하나는 줘라 — --title·--body·--path 중 하나 이상:")
		fmt.Fprintln(out, "  "+amendHelp)
		return 2
	}
	if req.Reason == "" {
		fmt.Fprintln(out, "고친 사유를 줘라(--reason) — 사유 없는 수정은 나중에 되짚을 수 없다.")
		fmt.Fprintln(out, "  한 구절이면 된다: --reason '경로 리네임 추종'")
		return 2
	}

	sess, _ := a.sessionID(ctx, *session)
	a.cli.Session = sess
	req.Project, req.SessionID = a.TargetProject(*project), sess

	res, err := a.cli.Write(ctx, CmdAmend, amendPath(itemID), req)
	if err != nil {
		fmt.Fprintf(out, "못 고쳤다: %v\n", err)
		return 1
	}
	// ★ mcpsrv.RenderAmend 로 낸다 — 손으로 다시 짜면 겹침 파급 문구를 CLI 사용자만
	//   못 보는 결함이 난다(label 이 정확히 그 결함을 겪었다).
	var got service.AmendResult
	if uerr := json.Unmarshal(res.Body, &got); uerr != nil {
		fmt.Fprintf(out, "고쳤으나 응답을 못 읽었다: %v\n", uerr)
		return 1
	}
	fmt.Fprint(out, mcpsrv.RenderAmend(got))
	return 0
}
```

`amendHelp` 상수를 `labelHelp` 옆에 더한다:

```go
const amendHelp = "fd amend <item-id> --title <제목> --body <본문> --path <경로> --reason <사유>"
```

**`flag` 임포트를 확인하라** — `fs.Visit(func(f *flag.Flag))` 에 필요하다.

- [ ] **Step 6: `main.go` 에 디스패치를 더한다**

`case "label":` 뒤에:

```go
	case "amend":
		return app.runAmend(ctx, rest, out)
```
(기존 case 들의 실제 호출 모양을 보고 맞춰라.)

- [ ] **Step 7: `mcpbackend.go` 에 `AmendItem` 을 더한다**

`SetLabels`(494행 근처)를 그대로 베껴 amend 로 바꾼다. 이 경로는 MCP 서버가 CLI 프로세스를 통해 도는 갈래라, **없으면 `Backend` 인터페이스를 안 만족해 컴파일이 깨진다.**

- [ ] **Step 8: DESIGN §6 CLI 목록을 고친다**

```
`status open beat note next pick add amend finish alloc project doctor export import watch`
```

- [ ] **Step 9: 통과 확인**

```bash
cd plugins/flightdeck/server && go test ./cmd/fd/
```
(`timeout: 480000`)

- [ ] **Step 10: 실물로 한 번 돌린다**

```bash
cd /Users/kweiza/Developments/kweiza-cc-plugins/.flightdeck/worktrees/fd-item-amend-surface
./plugins/flightdeck/bin/fd amend --help
```

시험이 아니라 **사람이 보는 화면**을 본다. 플래그 설명이 읽히는지, `--reason` 이 필수로 보이는지.

- [ ] **Step 11: 관문 + 커밋**

```bash
cd plugins/flightdeck/server && [ -z "$(gofmt -l .)" ] && go vet ./...
git add plugins/flightdeck/server/cmd/fd plugins/flightdeck/DESIGN.md
git commit -m "$(cat <<'EOF'
feat(flightdeck): fd amend 를 연다 — 오프라인에서는 거절한다

fs.Visit 로 실제로 주어진 플래그를 가른다. 기본값 비교로 판정하면 --title "" (빈 제목으로
바꿔라)이 생략과 안 갈린다.

빈 요청과 빈 사유를 클라이언트에서 막는다. 서버도 거절하지만 그 왕복은 오프라인에서
미도달 오류가 되어 사용자에게 "서버에 못 닿았다"로 보인다 — 실제 원인은 인자다.

응답은 mcpsrv.RenderAmend 로 낸다. 손으로 다시 짜면 겹침 파급 문구를 CLI 사용자만 못 보는
결함이 난다 — label 이 정확히 그 결함을 겪었다.
EOF
)"
```

---

## Task 7: `fd note --supersedes`

**Files:**
- Modify: `cmd/fd/cmds.go` (`runNote`)
- Test: `cmd/fd/note_supersedes_test.go` 신규

**Interfaces:**
- Consumes: 없음 (REST·MCP 에 이미 있다)
- Produces: 없음

- [ ] **Step 1: 실패 시험을 쓴다**

```go
// TestNoteCarriesSupersedes 는 CLI 가 정정 대상을 서버로 넘기는지 본다.
//
// ★ 이 축은 REST 와 MCP 에는 있었고 CLI 에만 없었다. 고칠 수단이 있는데 한 표면에서만
// 못 부르는 것이 --item-project 가 없어서 "거절이 막힌 길을 가리켰던" 것과 같은 모양이다.
func TestNoteCarriesSupersedes(t *testing.T) {
	app, sent := newTestApp(t) // ← 기존 헬퍼로. 보낸 본문을 붙잡는 fake 서버
	code := app.runNote(context.Background(),
		[]string{"--kind", "decision", "--body", "정정한다", "--supersedes", "01OLD"},
		io.Discard)
	if code != 0 {
		t.Fatalf("종료코드가 %d다", code)
	}
	var got map[string]any
	if err := json.Unmarshal(sent.lastBody(), &got); err != nil {
		t.Fatalf("보낸 본문을 못 읽었다: %v", err)
	}
	if got["supersedes"] != "01OLD" {
		t.Errorf("supersedes 가 %v다 — 서버로 안 넘어갔다", got["supersedes"])
	}
}
```

- [ ] **Step 2: 실패 확인**

```bash
cd plugins/flightdeck/server && go test ./cmd/fd/ -run 'TestNoteCarriesSupersedes' -v
```

- [ ] **Step 3: 구현한다**

**`cmd/fd/wire.go` 는 안 고친다.** `noteReq.Supersedes` 는 이미 있다(`wire.go:103`, `json:"supersedes,omitempty"`). 없는 것은 **CLI 플래그 하나뿐**이고, 그래서 이 축이 REST·MCP 에서만 닿았다.

`runNote` 의 플래그 선언에:

```go
	// ★ REST·MCP 에는 있었고 여기만 없었다. 판단은 추가 전용이라 덮어쓰기가 아니라
	//   새 행이 옛 행을 가리키는 방식이다(DESIGN §3 J 계층).
	supersedes := fs.String("supersedes", "", "정정 대상 판단 id. 덮어쓰기는 없다 — 새 행이 옛 행을 가리킨다")
```

그리고 요청 조립에 `Supersedes: strings.TrimSpace(*supersedes),` 를 더한다.

- [ ] **Step 4: 통과 확인 + 전체 시험**

```bash
cd plugins/flightdeck/server && go test ./cmd/fd/ -run 'TestNote' -v
```

```bash
cd plugins/flightdeck/server && go test ./...
```
(`timeout: 480000` — 전체 시험은 여기서 한 번 돈다.)

- [ ] **Step 5: 관문 다섯 줄 전부**

```bash
cd plugins/flightdeck/server && [ -z "$(gofmt -l .)" ] && echo "gofmt ok" && go vet ./... && echo "vet ok"
```

- [ ] **Step 6: 커밋**

```bash
git add plugins/flightdeck/server/cmd/fd
git commit -m "$(cat <<'EOF'
feat(flightdeck): fd note 에 --supersedes 를 연다

이 축은 REST 와 MCP 에는 있었고 CLI 에만 없었다. 고칠 수단이 있는데 한 표면에서만 못 부르는
것은 --item-project 가 없어서 거절이 막힌 길을 가리켰던 것과 같은 모양이다.

판단은 추가 전용이라 덮어쓰기가 아니다 — 새 행이 옛 행을 가리킨다.
EOF
)"
```

---

## 완료 후

1. **전체 시험을 한 번 더 돌린다**(`timeout: 480000`):
   ```bash
   cd plugins/flightdeck/server && go test ./...
   ```
2. **설치본에 실렸는지 확인한다** — 소스만 고치고 끝내면 돌고 있는 바이너리는 옛 코드다:
   ```bash
   cd /Users/kweiza/Developments/kweiza-cc-plugins/.flightdeck/worktrees/fd-item-amend-surface
   ./plugins/flightdeck/bin/fd amend --help
   ```
3. **`fd finish`** — 판단·후속·종료·반납이 한 호출이다. 후속으로 등록할 것(설계 §10):
   - 닫힌 항목에 걸린 판단을 읽을 경로 (큐가 `state='open'` 이라 `pick` 이 안 준다)
   - `pick` 이 내는 판단 전문에서 정정된 행을 접고 정정본을 먼저 내는 것
4. **`fd land`** 로 줄을 선 뒤 차례에 랜딩한다.
5. **워크트리 카드를 닫는다** — 워크트리에서 `fd` 를 불렀으면 세션 카드가 하나 더 생겨 있다.

---

## 계획 자기 검토 결과

**스펙 커버리지:** §3 축 셋 → Task 2·3 · §4 저장 → Task 1·2 · §5 표면 셋 → Task 4·5·6 · §5 오프라인 → Task 6 · §6 관문 전환 → Task 2 · §7 겹침 파급 → Task 3·5 · §8 낡는 문장 → Task 1(§3)·2(§11)·5(§6 MCP·note)·6(§6 CLI) · §9 판단 불변 → Task 7 이 플래그만 더한다 · §10 범위 밖 → 완료 후 3번의 후속.

**미해결로 남긴 결정 하나:** Task 4 Step 3 의 `ItemProject` — ⓐ 필드를 지우거나 ⓑ `service.AmendInput` 에 축을 더한다. 스펙이 이 축을 정하지 않았고, 구현자가 `resolveItemProject` 의 실제 모양을 본 뒤 고르는 편이 낫다. **어느 쪽이든 "필드만 받고 안 쓰는" 상태로 두지 마라** — 조용한 무시가 된다.

**가정한 이름들** (구현자가 실물로 맞춰야 하는 것): `openTestStore`·`seedProjectAndItem`·`closeTestItem`·`newTestService`·`seedItem`·`seedLiveSessionTouching`·`newTestServer`·`doJSON`·`doRaw`·`newTestApp` (시험 헬퍼) · `s.liveSessions`·`s.projectOf`·`deriveFailed` (프로덕션). 각 자리에 어느 파일을 읽어 맞출지 적어 뒀다.

**실물로 확인해 고친 것 넷** (초안이 틀렸던 자리):

| 초안 | 실측 |
|---|---|
| `service.containsString` 을 새로 만든다 | **이미 있다**(`landing.go:1004`) — 만들면 중복 선언으로 컴파일이 깨진다 |
| `mcpsrv.containsStr` 를 새로 만든다 | `containsAxis`(`identity.go:466`)를 쓴다 — 이름도 더 맞다 |
| `noteReq` 에 `Supersedes` 를 더한다 | **이미 있다**(`wire.go:103`) — 없는 것은 CLI 플래그 하나뿐이고 그것이 이 축이 한 표면에서만 닿은 이유다 |
| DESIGN 의 `TestToolTableIsEight` 인용 2곳 | **3곳**이다 |

`store.sameStrings` 는 실측으로 중복이 없어 새로 만든다.
