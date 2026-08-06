# 절대경로 발자국 전삭제 + 파괴적 증분 예외 기제 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 마이그레이션 005 로 `footprint` 의 절대경로 행을 전부 지우고, 그 증분이 통과할 수 있도록 파괴적 증분의 예외 기제를 연다.

**Architecture:** 예외 기제를 먼저 세운다(Task 1) — op 를 타입 있는 상수로 만들고, 증분 번호 → (허용 op 집합, 사유) 표를 두고, 예외로도 못 여는 op 를 따로 둔다. 그 다음 005 를 넣고 그 표의 첫 항목으로 등록한다(Task 2). 마지막으로 이 변경 때문에 낡는 문서 세 자리를 고친다(Task 3).

**Tech Stack:** Go · SQLite(`modernc.org/sqlite` v1.55.0, `sqlite_version()` 3.53.3) · 표준 `testing`

## Global Constraints

- 작업 디렉토리는 `plugins/flightdeck/server` 다. 모든 `go` 명령은 거기서 돈다.
- 주석·커밋·시험 메시지는 **한글**이다. 이 저장소 전체가 그렇다.
- 시험 메시지는 **무엇이 왜 틀렸는지**를 적는다. `t.Fatalf("실패")` 는 이 저장소의 규율 위반이다.
- 랜딩 관문: `go vet ./...` 무출력 · `gofmt -l .` 무출력 · `go test ./...` 새 실패 0건.
  `go build` 는 `_test.go` 를 안 보므로 **`go vet` 이 관문**이다.
- **운영 DB(`/home/aaron/.flightdeck/fd.db`)를 절대 쓰기로 열지 마라.** 읽을 일이 있으면
  `sqlite3.connect('file:…?mode=ro', uri=True)` 다. 이 계획의 어떤 단계도 운영 DB 를 안 만진다.
- `SchemaVersion` 을 올릴 때 **리터럴로 적는다.** `migrate_test.go:216` 이 그 규율을 주석으로
  못박아 뒀다 — `SchemaVersion-1` 로 적으면 다음 증분을 쓰는 사람이 무관한 실패를 물려받는다.

---

## File Structure

| 파일 | 책임 | 변경 |
|---|---|---|
| `internal/store/migrate_guard_test.go` | 파괴적 증분 탐지 + 예외 판정(순수 함수) | 수정 |
| `internal/store/migrations/005_footprint_absolute_backfill.sql` | 절대경로 행 삭제 + 그 삭제의 원장 | 신규 |
| `internal/store/store.go` | 증분 등록·`SchemaVersion`·마이그레이션 절 주석 | 수정(32–70행 근처, 362행) |
| `internal/store/migrate_footprint_backfill_test.go` | 005 가 무엇을 지우고 무엇을 안 지우는지 | 신규 |
| `internal/judge/prescribe.go` | `comparablePath` 가드의 존치 사유 주석 | 수정(393·424행, **본문 무변경**) |
| `plugins/flightdeck/DESIGN.md` | §7 각주 | 수정(691–692행 근처) |

예외 기제를 `migrate_guard_test.go` 안에 두는 이유: 판정이 **시험 전용**이고 프로덕션 코드가
그것을 안 부른다. `_test.go` 밖으로 내면 바이너리에 안 쓰이는 심볼이 생기고,
이 저장소는 그 비용을 이미 항목으로 세워 뒀다(`fd-eligible-dead-function-disposal`).

---

### Task 1: 파괴적 증분의 예외 기제

**Files:**
- Modify: `internal/store/migrate_guard_test.go:27-93` (`destructiveOps` 시그니처), `:145-174` (`TestBundledMigrationsAreAdditive`)
- Test: 같은 파일에 `TestExemptionMechanism` 추가

**Interfaces:**
- Produces: `type op int` 와 상수 `opDropTable`·`opDropColumn`·`opRename`·`opDeleteFrom`·`opUpdateSet`·`opInsertSelect`
- Produces: `func destructiveOps(sql string) []op` (반환형이 `[]string` → `[]op` 로 바뀐다)
- Produces: `func opLabels(ops []op) []string`
- Produces: `type exemption struct { ops []op; why string }`
- Produces: `var destructiveExempt map[int]exemption` (이 태스크에서는 **빈 표**)
- Produces: `var neverExempt []op`
- Produces: `func exemptReason(table map[int]exemption, to int, o op) (why string, ok bool)`
- Task 2 가 `destructiveExempt` 에 `5` 를 등록한다.

- [ ] **Step 1: 기제 시험을 먼저 쓴다**

`internal/store/migrate_guard_test.go` 파일 **끝**에 붙인다:

```go
// 예외 기제 자체를 시험한다.
//
// ★ 이 시험이 없으면 기제의 분기가 **한 번도 안 돈다.** 지금 예외에 오른 증분은 하나뿐이고
// 그 하나는 금지 목록에 안 걸리므로, 거절 경로는 시험이 직접 돌지 않으면 영영 안 돈다 —
// 그러면 "막는다고 적혀 있는데 실은 안 막는" 상태가 조용히 생긴다.
//
// ★ 표를 **인자로 받는다.** 전역 destructiveExempt 를 시험이 바꿔치기하면 그 시험은
// 다른 시험과 순서로 얽히고, 얽힌 시험은 언젠가 이유 없이 빨개진다.
func TestExemptionMechanism(t *testing.T) {
	ok5 := map[int]exemption{5: {ops: []op{opDeleteFrom}, why: "되돌릴 수 있고 읽는 쪽이 이미 배제한 행이다"}}

	cases := []struct {
		name   string
		table  map[int]exemption
		to     int
		o      op
		wantOK bool
	}{
		{"등록된 조작은 통과한다", ok5, 5, opDeleteFrom, true},
		{"등록 안 된 조작은 못 지나간다", ok5, 5, opUpdateSet, false},
		{"다른 증분으로 새지 않는다", ok5, 6, opDeleteFrom, false},
		{"표에 없는 증분은 예외가 없다", map[int]exemption{}, 5, opDeleteFrom, false},

		// ★ 사유가 없으면 예외가 아니다. 예외를 여는 값은 "허용 목록에 있다" 가 아니라
		//   "왜 안전한지가 적혀 있다" 다 — 근거 없는 예외는 다음 사람이 판정할 수 없다.
		{"사유가 비면 예외가 아니다",
			map[int]exemption{5: {ops: []op{opDeleteFrom}, why: ""}}, 5, opDeleteFrom, false},
		{"사유가 공백뿐이어도 예외가 아니다",
			map[int]exemption{5: {ops: []op{opDeleteFrom}, why: "   \n\t"}}, 5, opDeleteFrom, false},

		// ★ 예외로도 못 여는 것들. 데이터가 아니라 **구조**가 사라지는 조작이다.
		{"DROP TABLE 은 예외에 올려도 거절한다",
			map[int]exemption{5: {ops: []op{opDropTable}, why: "사유는 있다"}}, 5, opDropTable, false},
		{"DROP COLUMN 은 예외에 올려도 거절한다",
			map[int]exemption{5: {ops: []op{opDropColumn}, why: "사유는 있다"}}, 5, opDropColumn, false},
		{"RENAME 은 예외에 올려도 거절한다",
			map[int]exemption{5: {ops: []op{opRename}, why: "사유는 있다"}}, 5, opRename, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			why, ok := exemptReason(c.table, c.to, c.o)
			if ok != c.wantOK {
				t.Fatalf("예외 판정이 %v 여야 하는데 %v 다 (증분 %d, 조작 %s, 사유 %q)",
					c.wantOK, ok, c.to, c.o, why)
			}
			if ok && strings.TrimSpace(why) == "" {
				t.Fatalf("통과시키면서 사유가 비었다 — 사유 없는 예외는 이 기제가 막으라고 있는 바로 그것이다 (증분 %d, 조작 %s)",
					c.to, c.o)
			}
			if !ok && why != "" {
				t.Fatalf("거절하면서 사유를 냈다 %q — 거절의 사유를 통과의 사유 자리로 흘리면 호출부가 둘을 못 가른다", why)
			}
		})
	}
}

// neverExempt 에 오른 것이 destructiveOps 가 실제로 찾는 조작인지 확인한다.
//
// ★ 이 대조가 없으면 금지 목록이 **없는 조작을 금지**할 수 있다. 그러면 목록은 있는데
// 아무것도 안 막고, 그 사실은 아무 데도 안 보인다.
func TestNeverExemptOpsAreActuallyDetected(t *testing.T) {
	samples := map[op]string{
		opDropTable:  "DROP TABLE t;",
		opDropColumn: "ALTER TABLE t DROP COLUMN b;",
		opRename:     "ALTER TABLE t RENAME TO t_old;",
	}
	for _, banned := range neverExempt {
		sql, has := samples[banned]
		if !has {
			t.Fatalf("금지 목록의 %s 에 대응하는 표본이 이 시험에 없다 — 목록을 늘렸으면 표본도 늘려라", banned)
		}
		found := destructiveOps(sql)
		if len(found) == 0 {
			t.Fatalf("금지 목록에 %s 가 있는데 destructiveOps 가 %q 에서 아무것도 못 찾는다 — 금지 목록이 없는 조작을 막고 있다",
				banned, sql)
		}
	}
}
```

- [ ] **Step 2: 컴파일 실패를 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/store/ -run 'TestExemptionMechanism|TestNeverExempt' 2>&1 | head -20`
Expected: FAIL — `undefined: exemption` · `undefined: op` · `undefined: exemptReason` · `undefined: neverExempt`

- [ ] **Step 3: op 를 상수로 만든다**

`internal/store/migrate_guard_test.go` 의 `destructiveOps` 함수(27–66행)를 통째로 아래로 **교체**한다.
바로 위 문단 주석(`// destructiveOps 는 …` 로 시작하는 블록)은 그대로 두고 마지막 두 줄만 아래처럼 잇는다.

```go
// ★ 반환은 사람이 읽는 라벨이 아니라 **op 상수**다. 예외 표가 라벨 문자열에 결합돼 있으면
// 라벨을 한 글자만 고쳐도 예외가 조용히 안 맞게 되고, 그때 방어가 사라진 것을 아무도 모른다.
// 라벨은 String() 으로 내려 표시 전용이 된다.
type op int

const (
	opDropTable op = iota
	opDropColumn
	opRename
	opDeleteFrom
	opUpdateSet
	opInsertSelect
)

// String 은 표시 전용이다. **어떤 판정의 축도 아니다** — 축이 되는 순간 위 ★ 가 무너진다.
func (o op) String() string {
	switch o {
	case opDropTable:
		return "DROP TABLE (표 삭제)"
	case opDropColumn:
		return "DROP COLUMN (컬럼 삭제)"
	case opRename:
		return "RENAME (표·컬럼 개명 — 표 재작성 관용구의 자취)"
	case opDeleteFrom:
		return "DELETE FROM (행 삭제)"
	case opUpdateSet:
		return "UPDATE … SET (데이터 이행)"
	case opInsertSelect:
		return "INSERT … SELECT (데이터 이행)"
	}
	return fmt.Sprintf("op(%d) — 이름 없는 조작. 상수를 늘렸으면 String 도 늘려라", int(o))
}

func destructiveOps(sql string) []op {
	s := strings.ToUpper(stripSQLComments(sql))

	// ★ UPDATE 를 낱말 하나로 찾으면 안 된다. 이 스키마의 외래 키는 `ON UPDATE NO ACTION`
	// 을 쓰고, 그러면 **제약을 적은 증분이 전부 데이터 이행으로 걸린다.**
	// 그래서 `UPDATE <표> SET` 모양일 때만 센다.
	checks := []struct {
		op op
		re *regexp.Regexp
	}{
		{opDropTable, regexp.MustCompile(`\bDROP\s+TABLE\b`)},
		{opDropColumn, regexp.MustCompile(`\bDROP\s+COLUMN\b`)},
		{opRename, regexp.MustCompile(`\bRENAME\s+(TO|COLUMN)\b`)},
		{opDeleteFrom, regexp.MustCompile(`\bDELETE\s+FROM\b`)},
		{opUpdateSet, regexp.MustCompile(`\bUPDATE\s+[A-Z_][A-Z0-9_]*\s+SET\b`)},
		{opInsertSelect, regexp.MustCompile(`\bINSERT\s+INTO\b[\s\S]*?\bSELECT\b`)},
	}

	var found []op
	for _, c := range checks {
		if c.re.MatchString(s) {
			found = append(found, c.op)
		}
	}
	return found
}

// opLabels 는 표시용 라벨을 낸다. 오류 메시지 전용이다.
func opLabels(ops []op) []string {
	out := make([]string, 0, len(ops))
	for _, o := range ops {
		out = append(out, o.String())
	}
	return out
}
```

`import` 블록에 `"fmt"` 를 더한다(현재 `regexp`·`strings`·`testing` 셋뿐이다).

- [ ] **Step 4: 예외 표와 판정을 더한다**

같은 파일에서 `opLabels` 바로 아래에 붙인다:

```go
// ─────────────────────────────────────────────────────────────────────────────
// 예외
// ─────────────────────────────────────────────────────────────────────────────

// exemption 은 증분 하나가 여는 예외다.
//
// ★ 사유가 값의 일부다. 위 시험(TestBundledMigrationsAreAdditive)의 갈래 (b) 는
// "그 증분이 **왜** 되돌릴 수 있는지를 적고 예외로 둔다" 이지 "예외 목록에 올린다" 가 아니다.
// 사유를 선택 항목으로 두면 목록은 자라고 근거는 안 자란다.
type exemption struct {
	ops []op
	why string
}

// destructiveExempt 는 증분 번호 → 그 증분이 여는 예외다.
//
// ★ 여기 오르는 것은 **그 증분에 한정**된다. 조작 단위 허용이 아니라 증분 단위 허용이다 —
// "DELETE FROM 은 이제 괜찮다" 가 아니라 "005 의 DELETE FROM 은 이런 이유로 괜찮다" 여야
// 다음 증분이 같은 판정을 다시 받는다.
var destructiveExempt = map[int]exemption{}

// neverExempt 는 예외로도 못 여는 조작이다. 데이터가 아니라 **구조**가 사라지는 것들이다.
//
// ★ 이 목록이 필요한 이유는 실측이다. 이 파일의 옛 주석은 "영구 표가 사라지는 쪽은 다른
// 시험이 본다 — TestFreshInstallAndUpgradeProduceTheSameSchema 와 TestDeclaredTablesMatchDesign"
// 이라고 적어 뒀는데, 증분에 `DROP TABLE` 을 넣어 실제로 돌려 보니 **둘 다 통과했다.**
// 전자는 신규 설치와 판올림이 양쪽 다 그 증분을 돌아 스키마가 똑같이 줄기 때문이고,
// 후자는 증분 텍스트의 CREATE TABLE 만 세기 때문이다. 즉 그 안전망은 없었다.
// 없는 안전망을 믿는 대신 여기서 막는다.
var neverExempt = []op{opDropTable, opDropColumn, opRename}

// exemptReason 은 증분 to 가 조작 o 를 써도 되는지와 그 사유를 낸다. 순수 함수다.
//
// ★ 표를 인자로 받는다. 전역을 직접 읽으면 이 판정을 시험할 때 전역을 바꿔치기해야 하고,
// 그렇게 얽힌 시험은 실행 순서에 따라 빨개진다.
func exemptReason(table map[int]exemption, to int, o op) (why string, ok bool) {
	for _, banned := range neverExempt {
		if o == banned {
			return "", false
		}
	}
	ex, found := table[to]
	if !found || strings.TrimSpace(ex.why) == "" {
		return "", false
	}
	for _, allowed := range ex.ops {
		if o == allowed {
			return ex.why, true
		}
	}
	return "", false
}
```

- [ ] **Step 5: 기존 두 시험을 새 시그니처에 맞춘다**

`TestDestructiveOpsNamesWhatItFound` 는 `len(destructiveOps(...)) > 0` 만 보므로 **그대로 컴파일된다.**
`%v` 로 찍는 자리(`찾은 것: %v`)는 `[]op` 를 받아도 `String()` 이 불려 사람이 읽을 수 있다. 손대지 않는다.

`TestBundledMigrationsAreAdditive` 의 마지막 `if` 블록(현재 `if ops := destructiveOps(m.SQL); len(ops) > 0 {` 부터
`}` 까지)을 아래로 교체한다:

```go
		// 예외에 오른 조작은 사유와 함께 통과시킨다. 나머지는 그대로 만료 통지다.
		var unexcused []op
		for _, o := range destructiveOps(m.SQL) {
			if _, ok := exemptReason(destructiveExempt, m.To, o); ok {
				continue
			}
			unexcused = append(unexcused, o)
		}
		if len(unexcused) > 0 {
			t.Errorf("증분 %d(%s) 에 예외 없는 파괴적 조작이 있다: %s\n\n"+
				"이것은 시험의 결함이 아니라 **판단의 만료 통지**다.\n"+
				"설계 §7 과 store.go 의 마이그레이션 절은 적용을 fd serve 기동 경로(store.Open)\n"+
				"안에 남겨 두기로 했고, 그 근거는 \"증분이 순수 가산이라 실질 위험이 낮다\"\n"+
				"였다. 파괴적 증분이 들어온 지금 그 근거가 사라졌다.\n\n"+
				"둘 중 하나를 하고 그 근거를 §7 과 store.go 주석에 함께 적어라:\n"+
				"  (a) 적용을 기동에서 분리한다 — fd migrate [--to N] / fd migrate --rollback.\n"+
				"      §7 이 이 순간을 위해 미리 이름 붙여 둔 처방이다. 그 명령은 아직 없다.\n"+
				"  (b) 이 증분이 왜 되돌릴 수 있는지를 destructiveExempt 에 **사유와 함께** 올린다.\n"+
				"      사유가 비면 예외로 안 쳐 준다. neverExempt 에 오른 조작은 (b) 로도 못 연다.\n\n"+
				"어느 쪽이든 **문서와 코드를 갈린 채로 두지 마라.**",
				m.To, m.Name, strings.Join(opLabels(unexcused), ", "))
		}
```

- [ ] **Step 6: 시험을 돌려 전부 통과하는지 본다**

Run: `cd plugins/flightdeck/server && go test ./internal/store/ -run 'Exemption|NeverExempt|DestructiveOps|Additive' -v 2>&1 | tail -30`
Expected: PASS 넷 전부. 지금 번들된 증분 002·003·004 는 파괴적 조작이 없으므로
`TestBundledMigrationsAreAdditive` 는 **예외 표가 비어 있는 채로** 통과한다.

- [ ] **Step 7: 관문을 돌린다**

Run: `cd plugins/flightdeck/server && go vet ./... && gofmt -l . && go test ./internal/store/ 2>&1 | tail -5`
Expected: `go vet` 무출력 · `gofmt -l` 무출력 · store 패키지 새 실패 0건

- [ ] **Step 8: 커밋**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-footprint-rows-backfill-after-gate-runs
git add plugins/flightdeck/server/internal/store/migrate_guard_test.go
git commit -m "feat(flightdeck): 파괴적 증분에 예외 기제를 연다 — 사유가 값의 일부이고, 구조를 지우는 조작은 예외로도 못 연다

TestBundledMigrationsAreAdditive 가 갈래 (b)('되돌릴 수 있는 근거를 적고 예외로 둔다')를
제시하면서 정작 예외 자리가 코드에 없었다. fd-open-signal-masquerades-as-mcp 가 그 때문에
백필을 통째로 포기한 전례가 있다.

예외의 키를 라벨 문자열이 아니라 op 상수로 둔다 — 라벨에 결합하면 한 글자만 고쳐도 방어가
조용히 사라진다. 사유가 비면 예외로 안 쳐 준다. DROP TABLE·DROP COLUMN·RENAME 은 예외로도
못 연다: 그 셋을 '다른 시험이 본다'고 적혀 있었으나 실제로 증분에 넣어 보니 지목된 두 시험이
둘 다 통과했다(신규 설치와 판올림이 양쪽 다 그 증분을 돌아 스키마가 똑같이 줄고, 표 수 시험은
텍스트의 CREATE TABLE 만 센다).

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 2: 마이그레이션 005 — 절대경로 발자국을 지운다

**Files:**
- Create: `internal/store/migrations/005_footprint_absolute_backfill.sql`
- Create: `internal/store/migrate_footprint_backfill_test.go`
- Modify: `internal/store/store.go:41-42` (embed), `:46` (SchemaVersion), `:66-70` (migrations)
- Modify: `internal/store/migrate_guard_test.go` (`destructiveExempt` 에 5 등록)

**Interfaces:**
- Consumes: Task 1 의 `exemption`·`opDeleteFrom`·`destructiveExempt`
- Consumes: `seed(t, s, "P")` — 프로젝트 `P`(path `/repo/P`)와 머신 `m1` 을 만든다 (`store_test.go:37`)
- Consumes: `mustSession(t, s, "P", "cc1")` — worktree `/w/cc1` 인 세션을 만들고 `model.Session` 을 낸다 (`store_test.go:48`)
- Consumes: `dsn(path)` — 서버와 같은 DSN 을 만든다 (`store.go:206`)
- Produces: `SchemaVersion = 5`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/store/migrate_footprint_backfill_test.go` 를 새로 만든다:

```go
package store

import (
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
)

// 005 는 절대경로 발자국을 전부 지우고 상대경로 행은 한 톨도 안 건드린다.
//
// ★ 전제를 **결과를 읽기 전에** 단정한다. 옛 상태가 실제로 안 만들어졌으면 아래 단정은
// 아무것도 안 지킨다 — migrate_test.go 의 시험들이 같은 규율을 갖고 있다.
//
// ★ 지우는 시험에서 어려운 것은 "지웠다" 가 아니라 **"안 지울 것을 안 지웠다"** 다.
// 표본 D 가 그 축이고, 그것이 없으면 `DELETE FROM footprint` 한 줄도 이 시험을 통과한다.
func TestMigration005DeletesAbsoluteFootprints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	s, err := OpenWithLogger(path, log)
	if err != nil {
		t.Fatalf("Open 실패: %v", err)
	}
	seed(t, s, "P")
	sess := mustSession(t, s, "P", "cc1") // worktree = /w/cc1, project.path = /repo/P

	// 표본. 절대경로는 전부 사라져야 하고 상대경로는 남아야 한다.
	type fp struct {
		path   string
		origin string
		keep   bool
		why    string
	}
	samples := []fp{
		{"/tmp/scratch/commitmsg.txt", "observed", false, "저장소 밖 — 스크래치패드"},
		{"/repo/P/other/tree.go", "observed", false, "프로젝트 안이지만 이 세션의 트리 밖"},
		{"/w/cc1/inside.go", "observed", false, "세션 워크트리 **안**이어도 예외가 없다 — 좌표계가 갈리므로 살리지 않는다"},
		{"/tmp/claimed/x.go", "claimed", false, "origin 으로 안 가른다"},
		{"/tmp/a_b%c d.go", "observed", false, "경로의 _ 와 % 는 와일드카드가 아니다"},
		{"keep/relative.go", "observed", true, "원래부터 상대경로 — 이 행이 이 시험의 핵심이다"},
		{"keep/second.go", "claimed", true, "상대경로는 origin 과 무관하게 남는다"},
	}
	for i, f := range samples {
		at := fmt.Sprintf("2026-08-01T00:00:%02d.000000Z", i)
		if _, err := s.db.Exec(
			`INSERT INTO footprint(session_id, path, origin, first_at, last_at) VALUES (?,?,?,?,?)`,
			sess.ID, f.path, f.origin, at, at); err != nil {
			t.Fatalf("표본 심기 실패(%s): %v", f.path, err)
		}
	}

	// ★ 리터럴 4다. SchemaVersion-1 이 아니다.
	//   이 시험이 재현하려는 옛 상태는 "005 직전", 즉 4 다. 그 사실은
	//   005_footprint_absolute_backfill.sql 에 고정돼 있고 뒤에 무슨 증분이 더 붙든 안 변한다.
	//   migrate_test.go:216 이 같은 함정을 주석으로 못박아 뒀다.
	const prev = 4
	if _, err := s.db.Exec(
		fmt.Sprintf(`DELETE FROM schema_version WHERE version > %d`, prev)); err != nil {
		t.Fatalf("옛 DB 구성 실패: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("닫기 실패: %v", err)
	}

	// ── 전제 확인 ──
	raw, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("raw 열기 실패: %v", err)
	}
	var v, abs int
	if err := raw.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&v); err != nil {
		t.Fatalf("전제 확인 실패: %v", err)
	}
	if err := raw.QueryRow(
		`SELECT count(*) FROM footprint WHERE substr(path,1,1)='/'`).Scan(&abs); err != nil {
		t.Fatalf("전제 확인 실패: %v", err)
	}
	raw.Close()
	if v != prev {
		t.Fatalf("전제가 성립하지 않았다: schema_version=%d — 이 상태로는 아래 단정이 무의미하다", v)
	}
	if abs != 5 {
		t.Fatalf("전제가 성립하지 않았다: 절대경로 표본이 %d건이다, want 5 — 심기가 실패했다면 '지웠다'는 단정이 공허하다", abs)
	}

	// ── 판올림 ──
	s2, err := OpenWithLogger(path, log)
	if err != nil {
		t.Fatalf("판올림 Open 실패: %v", err)
	}
	defer s2.Close()

	var left int
	if err := s2.db.QueryRow(
		`SELECT count(*) FROM footprint WHERE substr(path,1,1)='/'`).Scan(&left); err != nil {
		t.Fatalf("판올림 뒤 조회 실패: %v", err)
	}
	if left != 0 {
		t.Errorf("절대경로 발자국이 %d건 남았다 — 005 가 겨냥한 것이 정확히 그 행들이다", left)
	}

	for _, f := range samples {
		if !f.keep {
			continue
		}
		var n int
		if err := s2.db.QueryRow(
			`SELECT count(*) FROM footprint WHERE session_id=? AND path=? AND origin=?`,
			sess.ID, f.path, f.origin).Scan(&n); err != nil {
			t.Fatalf("조회 실패(%s): %v", f.path, err)
		}
		if n != 1 {
			t.Errorf("남아야 할 행이 %d건이다 (%s, origin=%s) — %s", n, f.path, f.origin, f.why)
		}
	}

	var total int
	if err := s2.db.QueryRow(`SELECT count(*) FROM footprint`).Scan(&total); err != nil {
		t.Fatalf("조회 실패: %v", err)
	}
	if total != 2 {
		t.Errorf("발자국이 %d건 남았다, want 2 — 상대경로 둘만 남아야 한다", total)
	}
}

// 두 번 적용해도 결과가 같다.
//
// ★ 멱등이 필요한 이유는 되돌리기가 백업 파일 손 복사뿐이기 때문이다. 복구 도중 증분이
// 다시 도는 경로가 실재하므로, 두 번째 적용이 무해해야 그 복구가 안전하다.
func TestMigration005IsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	s, err := OpenWithLogger(path, log)
	if err != nil {
		t.Fatalf("Open 실패: %v", err)
	}
	seed(t, s, "P")
	sess := mustSession(t, s, "P", "cc1")
	for _, p := range []string{"/tmp/x.go", "keep/y.go"} {
		if _, err := s.db.Exec(
			`INSERT INTO footprint(session_id, path, origin, first_at, last_at)
			   VALUES (?,?,'observed','2026-08-01T00:00:00.000000Z','2026-08-01T00:00:00.000000Z')`,
			sess.ID, p); err != nil {
			t.Fatalf("표본 심기 실패(%s): %v", p, err)
		}
	}
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version > 4`); err != nil {
		t.Fatalf("옛 DB 구성 실패: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("닫기 실패: %v", err)
	}

	count := func(tag string) int {
		t.Helper()
		st, err := OpenWithLogger(path, log)
		if err != nil {
			t.Fatalf("%s Open 실패: %v", tag, err)
		}
		defer st.Close()
		var n int
		if err := st.db.QueryRow(`SELECT count(*) FROM footprint`).Scan(&n); err != nil {
			t.Fatalf("%s 조회 실패: %v", tag, err)
		}
		return n
	}

	first := count("1회차")
	if first != 1 {
		t.Fatalf("1회차 뒤 발자국이 %d건이다, want 1 — 이 값이 틀리면 멱등 비교가 무의미하다", first)
	}
	// 2회차는 schema_version 이 이미 5라 증분이 안 돈다. 그래도 결과가 같아야 한다 —
	// 판올림이 다시 도는 경로(백업 복구)에서 이 단정이 값을 갖는다.
	if _, err := func() (sql.Result, error) {
		raw, err := sql.Open("sqlite", dsn(path))
		if err != nil {
			return nil, err
		}
		defer raw.Close()
		return raw.Exec(`DELETE FROM schema_version WHERE version > 4`)
	}(); err != nil {
		t.Fatalf("2회차 구성 실패: %v", err)
	}
	if second := count("2회차"); second != first {
		t.Errorf("2회차 뒤 발자국이 %d건이다, want %d — 증분이 멱등이 아니다", second, first)
	}
}
```

- [ ] **Step 2: 시험이 실패하는지 본다**

Run: `cd plugins/flightdeck/server && go test ./internal/store/ -run 'Migration005' 2>&1 | tail -20`
Expected: FAIL — `절대경로 발자국이 5건 남았다` (005 가 아직 없다)

- [ ] **Step 3: 005 SQL 을 쓴다**

`internal/store/migrations/005_footprint_absolute_backfill.sql` 을 새로 만든다:

```sql
-- 005 · 관문 이전에 들어온 절대경로 발자국을 지운다 (schema_version 4 → 5)
--
-- ★ 왜 지금 지울 수 있나: **상류가 막혔고 신규 유입이 멎었다.**
--   footprint 에 쓰는 살아 있는 문은 둘뿐이다 — service/session.go 의 Beat(observed)와
--   service/pick.go 의 Pick(claimed). 둘 다 service.RelPathWithin 을 태운다.
--   실측(2026-08-06, 운영 DB 읽기 전용): 마지막 절대경로 발자국은 2026-08-05T04:28:17Z 이고
--   그 뒤 34시간 동안 0건이다. 그 사이 **상대경로 행은 계속 들어왔다**(가장 최근
--   2026-08-06T14:32:52Z) — 관측이 멎은 것이 아니라 절대경로만 안 들어온다는 뜻이다.
--
--   ★ 경계는 관문 커밋 시각이 아니다. 관문 4530e3c 는 2026-08-05T01:51:07Z 인데 그 뒤에
--   들어온 절대행이 42건이고 마지막 절대행은 그보다 2시간 37분 뒤다. 코드가 랜딩한 시점과
--   그 코드가 도는 시점은 다르다 — 앞선 판단이 "전부 관문 이전"이라고 적었던 것을 정정한다.
--
-- ★ 무엇을 지우나(2026-08-06 실측 174건, 전부 origin='observed').
--   **이 머리말이 그 삭제의 유일한 원장이다** — 지운 뒤에는 어떤 질의도 이 분포를 못 낸다.
--
--     /tmp 스크래치패드                       72
--     프로젝트 저장소 안이지만 그 세션의 트리 밖  35
--     다른 저장소·기타                        31
--     .claude (memory·plugins)                27
--     .wt-kweiza (옛 워크트리 레이아웃)          9
--                                           ────
--                                            174
--
--   발자국이 0이 되는 세션은 18곳(state: done 12 · active 6). active 6곳은 전부
--   2026-08-03~08-05 에 열렸고 가장 최근이 08-05T03:22 다 — state 컬럼이 active 로 남아
--   있을 뿐 일하는 세션이 아니다.
--
--   ★ 계약은 어떤 수도 아니다. **"절대경로 행이 0이 된다"** 가 계약이고, 위 수치는 이 시점의
--   원장이다. 랜딩 시점에는 다를 수 있고, 시험은 수가 아니라 갈래를 표본으로 지킨다.
--
-- ★ 왜 상대화해서 살리지 않나: **좌표계가 갈리기 때문이다.**
--   세션의 worktree 를 저장소 루트로 정규화하면 174건 중 32건이 그 뿌리 안에 들어온다.
--   그런데 살아 있는 관문은 뿌리가 아니라 **세션의 worktree 원본**을 쓴다
--   (service/session.go 의 Beat, service/pick.go 의 Pick — 둘 다 RelPathWithin(sess.Worktree, p)).
--   그 좌표계로 상대화 가능한 절대행은 **0건**이다. 그리고 그것은 우연이 아니라 정의다 —
--   절대경로로 남았다는 사실 자체가 RelPathWithin 이 within=false 를 냈다는 뜻,
--   곧 그 세션의 워크트리 밖이었다는 뜻이다.
--
--   뿌리 기준으로 살리면 한 세션 안에 원점이 둘이 된다(그 32건이 속한 세션 16곳 중 6곳은
--   이미 worktree 기준 상대행을 갖고 있다). 그러면 4530e3c 가 세운 판단 —
--   "형제 워크트리의 같은 이름 파일은 **다른 파일**이다" — 이 그 세션 안에서 깨진다.
--   좌표계를 하나로 만드는 것은 이 증분의 축이 아니다:
--   항목 fd-footprint-paths-keep-the-worktree-prefix 가 그 축을 들고 있다.
--
-- ★ 왜 origin 으로 안 가르나: 지금 절대행은 전부 observed 지만 그것은 이 순간의 사실이지
--   계약이 아니다. 절대경로는 어느 origin 이든 겹침 좌표계 밖이므로, origin 을 조건에 넣으면
--   랜딩까지의 사이에 다른 origin 의 절대행이 들어온 경우에만 조용히 남는다 —
--   조건을 좁혀서 사는 것이 없고 놓치는 것만 있다.
--
-- ★ 왜 되돌릴 수 있나(= TestBundledMigrationsAreAdditive 의 갈래 (b) 근거. 같은 근거를
--   store.go 마이그레이션 절과 설계 §7 에 함께 적었다 — 셋 중 하나만 고치면 그 시험이
--   없애려던 "문서와 코드가 갈린 상태"를 그대로 재생산한다):
--     ⒜ footprint 는 D(파생) 계층이다. 추가 전용 트리거는 judgment 와 event 에만 있고,
--        footprint 는 session 이 사라지면 CASCADE 로 함께 사라지도록 스키마가 삭제를 이미
--        전제한다. footprint 를 참조하는 표는 없다.
--     ⒝ 지우는 것은 **읽는 쪽이 이미 배제한 행**이다. judge/prescribe.go 의 comparablePath
--        가 절대경로를 처방 축에서 빼고 있고, 그 주석이 이 정리를 별개 항목으로 예고해 뒀다.
--     ⒞ 판올림 전에 PlanMigration 이 Backup:true 로 VACUUM INTO 백업을 뜨고, 깨지면
--        RollbackHint 가 그 파일 경로와 -wal·-shm 제거 절차까지 낸다.
--        **되돌리기 서브명령은 없다** — 되돌릴 유일한 길은 그 백업 파일 손 복사다.
--
-- ★ substr(path,1,1)='/' 다. LIKE '/%' 도 같은 일을 하지만, 이 파일에서 접두를 보는 자리는
--   여기 하나뿐이고 첫 글자 하나만 보는 판정은 substr 이 더 정확히 그렇게 읽힌다.
--   (경로에 _ 나 % 가 들어 있어도 이 판정은 영향을 안 받는다 — 실측 174건 중 36건이
--   _ 를 담고 있다.)
--
-- ★ 멱등이다. 두 번 돌려도 결과가 같다 — 1회차 뒤 절대경로가 0건이라 2회차는 0행에 돈다.
--   세션이 0행인 DB(신규 설치)에서도 안전하다.

DELETE FROM footprint WHERE substr(path, 1, 1) = '/';
```

- [ ] **Step 4: store.go 에 등록한다**

세 자리를 고친다.

⑴ `internal/store/store.go:41-42` 뒤에 embed 를 더한다:

```go
//go:embed migrations/004_pick_bundle.sql
var migrationPickBundle string

//go:embed migrations/005_footprint_absolute_backfill.sql
var migrationFootprintPurge string
```

⑵ `internal/store/store.go:46` 의 상수를 올린다:

```go
const SchemaVersion = 5
```

⑶ `internal/store/store.go:66-70` 의 슬라이스에 한 줄 더한다:

```go
var migrations = []Migration{
	{To: 2, Name: "멱등 기록을 DB 로", SQL: migration002},
	{To: 3, Name: "랜딩 순서 큐", SQL: migration003},
	{To: 4, Name: "pick_eval 이 묶음을 담는다", SQL: migrationPickBundle},
	{To: 5, Name: "절대경로 발자국을 지운다", SQL: migrationFootprintPurge},
}
```

- [ ] **Step 5: 시험을 돌린다 — 백필은 통과하고 가드는 빨개져야 한다**

Run: `cd plugins/flightdeck/server && go test ./internal/store/ -run 'Migration005|Additive' 2>&1 | tail -20`
Expected: `TestMigration005DeletesAbsoluteFootprints` PASS · `TestMigration005IsIdempotent` PASS ·
**`TestBundledMigrationsAreAdditive` FAIL** — `증분 5(절대경로 발자국을 지운다) 에 예외 없는 파괴적 조작이 있다: DELETE FROM (행 삭제)`

이 실패는 결함이 아니라 **만료 통지가 제대로 작동한다는 증거**다. 다음 단계가 갈래 (b)로 답한다.

- [ ] **Step 6: 예외 표에 005 를 등록한다**

`internal/store/migrate_guard_test.go` 의 `destructiveExempt` 를 채운다:

```go
var destructiveExempt = map[int]exemption{
	// 005 · 절대경로 발자국 삭제.
	5: {
		ops: []op{opDeleteFrom},
		why: "footprint 는 D(파생) 계층이고 참조하는 표가 없다. 지우는 행은 judge.comparablePath 가 " +
			"이미 처방 축에서 배제한 절대경로뿐이며, 그 좌표계로는 관문(Beat·Pick)이 쓰는 " +
			"session.worktree 기준으로 상대화할 수 있는 행이 0건이라 살릴 방법 자체가 없다. " +
			"판올림 전 VACUUM INTO 백업이 자동으로 뜨고 RollbackHint 가 복구 절차를 낸다. " +
			"근거 전문은 005 머리말과 설계 §7 에 있다.",
	},
}
```

- [ ] **Step 7: 전부 통과하는지 본다**

Run: `cd plugins/flightdeck/server && go test ./internal/store/ 2>&1 | tail -10`
Expected: store 패키지 새 실패 0건. 특히 `TestFreshInstallAndUpgradeProduceTheSameSchema` 와
`TestBundledMigrationsReachSchemaVersion` 이 통과해야 한다(005 는 스키마를 안 바꾸므로 전자는
영향이 없고, 후자는 `SchemaVersion` 과 증분 목록이 맞는지를 본다).

- [ ] **Step 8: 관문을 돌린다**

Run: `cd plugins/flightdeck/server && go vet ./... && gofmt -l . && go test ./... 2>&1 | grep -E "^(FAIL|ok)" | grep -v "^ok" | head`
Expected: `go vet` 무출력 · `gofmt -l` 무출력 · 새 FAIL 0건

- [ ] **Step 9: 커밋**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-footprint-rows-backfill-after-gate-runs
git add plugins/flightdeck/server/internal/store/migrations/005_footprint_absolute_backfill.sql \
        plugins/flightdeck/server/internal/store/migrate_footprint_backfill_test.go \
        plugins/flightdeck/server/internal/store/store.go \
        plugins/flightdeck/server/internal/store/migrate_guard_test.go
git commit -m "feat(flightdeck): 005 — 절대경로 발자국을 지운다. 상대화해서 살리는 안은 좌표계가 깼다

관문이 쓰는 session.worktree 기준으로 상대화 가능한 절대행은 0건이다 — 절대경로로 남았다는
사실 자체가 그 워크트리 밖이었다는 뜻이라 정의상 그렇다. 뿌리 기준으로 살리면 한 세션 안에
원점이 둘이 되고, 형제 워크트리의 같은 이름 파일은 다른 파일이라는 4530e3c 의 판단이 그
세션 안에서 깨진다. 좌표계 통일은 fd-footprint-paths-keep-the-worktree-prefix 의 축이다.

지우는 174건의 분포를 머리말에 남긴다 — 지운 뒤에는 어떤 질의도 그것을 못 낸다.
경계가 관문 커밋이 아니라 배포 시각이라는 것도 함께 정정했다(커밋 뒤에 42건이 더 들어왔다).

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 3: 낡은 문서 세 자리를 고친다

**Files:**
- Modify: `internal/store/store.go:362-363`
- Modify: `plugins/flightdeck/DESIGN.md:691-692` (앵커 문자열로 찾아라 — 다른 세션이 §7 에 순삽입 중이라 줄 번호가 밀렸을 수 있다)
- Modify: `internal/judge/prescribe.go:393`, `:424`

**Interfaces:**
- Consumes: Task 2 가 등록한 `SchemaVersion = 5` 와 증분 네 단

이 태스크가 독립인 이유: 앞의 둘은 코드고 이것은 **문서와 코드가 갈린 자리를 메우는 것**이라,
리뷰어가 앞의 둘을 통과시키면서 이것만 거절할 수 있다. `TestBundledMigrationsAreAdditive` 의
실패 메시지가 "문서와 코드를 갈린 채로 두지 마라"를 명시적으로 요구하므로 **같은 브랜치**에 있어야 한다.

- [ ] **Step 1: store.go 의 마이그레이션 절 주석을 고친다**

`internal/store/store.go:362-363` 의 두 줄을 찾는다:

```go
// 증분이 한 단(002)이고 순수 가산이라 실질 위험이 낮다. 반면 적용을 기동에서 떼면
// **모든 명령**(fail-open 훅 4종 포함)이 "스키마가 아직 안 올라간 DB" 를 만나는 새 경로가 생기고,
```

아래로 교체한다:

```go
// 증분은 이제 **네 단**(002·003·004·005)이고, 005 에서 **순수 가산이 아니게 됐다** —
// 절대경로 발자국을 지운다. 그래도 적용을 기동에 남기는 판단은 유지한다: 적용을 떼면
// **모든 명령**(fail-open 훅 4종 포함)이 "스키마가 아직 안 올라간 DB" 를 만나는 새 경로가 생기고,
```

- [ ] **Step 2: 같은 주석에 예외 기제를 가리키는 문장을 더한다**

Step 1 에서 고친 문단의 **끝**(그 문단의 마지막 줄 뒤)에 붙인다:

```go
//
// ★ 그래서 파괴적 증분은 무조건 막는 대신 **근거를 요구하는 예외**로 통과시킨다.
// migrate_guard_test.go 의 destructiveExempt 가 증분 번호마다 (허용 조작, 사유) 를 담고,
// 사유가 비면 예외로 안 쳐 준다. 구조가 사라지는 조작(DROP TABLE·DROP COLUMN·RENAME)은
// neverExempt 라 그 예외로도 못 연다 — 그 셋을 "다른 시험이 본다" 고 적어 뒀던 것이
// 실측으로 거짓이었기 때문이다(지목된 두 시험이 DROP TABLE 을 둘 다 통과시켰다).
```

- [ ] **Step 3: DESIGN.md §7 을 고친다**

앵커로 찾는다: `grep -n "증분이 한 단" plugins/flightdeck/DESIGN.md`

찾은 두 줄:

```markdown
**지금 구조를 유지하기로 한 판단과 근거.** 증분이 한 단(`002_idempotency`)이고 순수 가산이라
실질 위험이 낮다. 반면 적용을 기동에서 떼면 **모든 명령**(fail-open 훅 6종 포함)이
```

아래로 교체한다(`6종` → `4종` 도 함께 고친다 — `store.go` 와 갈려 있었고 실제 수는 4다):

```markdown
**지금 구조를 유지하기로 한 판단과 근거.** 증분은 이제 **네 단**(`002`·`003`·`004`·`005`)이고,
`005` 에서 **순수 가산이 아니게 됐다**(절대경로 발자국 삭제). 그래도 적용을 기동에 남긴다 —
적용을 떼면 **모든 명령**(fail-open 훅 4종 포함)이
```

- [ ] **Step 4: DESIGN.md 의 만료 조건 문단을 갱신한다**

앵커로 찾는다: `grep -n "이 판단이 뒤집히는 조건은 하나다" plugins/flightdeck/DESIGN.md`

그 문단(`**이 판단이 뒤집히는 조건은 하나다 …**` 로 시작해 `그때 fd migrate …` 로 끝나는 두 줄)
**뒤에** 아래를 삽입한다. 기존 두 줄은 지우지 않는다 — 그 조건은 여전히 참이고, 아래가 그것이
실제로 일어났을 때 무엇을 했는지를 잇는다:

```markdown
**그 순간이 실제로 왔다(2026-08-06, 증분 `005`).** `fd migrate` 를 짓는 대신 시험이 제시한
다른 갈래를 택했다 — 증분마다 **왜 되돌릴 수 있는지를 적고 예외로 두는 것**이다.
`migrate_guard_test.go` 의 `destructiveExempt` 가 증분 번호 → (허용 조작, 사유) 를 담고,
**사유가 비면 예외로 안 쳐 준다.** 예외의 키는 사람이 읽는 라벨이 아니라 op 상수다 —
라벨 문자열에 결합하면 라벨을 한 글자만 고쳐도 예외가 조용히 안 맞게 된다.
구조가 사라지는 조작(`DROP TABLE`·`DROP COLUMN`·`RENAME`)은 `neverExempt` 라 그 예외로도 못 연다:
그 셋은 "다른 시험이 본다"고 적혀 있었으나 증분에 넣어 실제로 돌려 보니
`TestFreshInstallAndUpgradeProduceTheSameSchema` 와 `TestDeclaredTablesMatchDesign` 이
**둘 다 통과했다**(신규 설치와 판올림이 양쪽 다 그 증분을 돌아 스키마가 똑같이 줄고,
표 수 시험은 증분 텍스트의 `CREATE TABLE` 만 센다). 없는 안전망을 믿는 대신 목록으로 막는다.

`fd migrate [--to N]` / `--rollback` 은 **여전히 없다.** 되돌릴 길은 판올림 전 자동 백업
파일을 손으로 복사하는 것뿐이고, 그것이 `005` 의 예외를 정당화하는 근거 ⒞ 다.
```

- [ ] **Step 5: prescribe.go 의 낡은 수치 두 자리를 고친다**

`internal/judge/prescribe.go:393` 한 줄:

```go
	// 실측(2026-08-05): observed 발자국 406개 중 108개(27%)가 절대경로다.
```

교체:

```go
	// 실측: 2026-08-05 에 observed 406개 중 108개(27%)였고, 2026-08-06 에 1592개 중 174개(10.9%)였다.
	// 증분 005(절대경로 발자국 삭제)가 랜딩한 뒤로는 **0개**다 — 그래도 이 가드는 존치한다(아래).
```

`internal/judge/prescribe.go:419-429` 의 존치 사유 블록에서 사유 ①·② 를 아래로 교체한다.
`comparablePath` **함수 본문은 안 건드린다.**

```go
//	① (소멸했다) 이미 들어온 것이 DB 에 남아 있었다 — 실측 시점(2026-08-05) observed 발자국
//	   406개 중 108개(27%)가 절대경로였다. **증분 005 가 그 행들을 전부 지웠으므로 이 사유는
//	   더 이상 존치 근거가 아니다.** 지운다면 ② 만 보고 판단해야 한다.
//	② 발자국의 원천이 훅 하나가 아니다. 선언 경로(declared)·항목 경로도 같은 컬럼에
//	   들어오고, 그중 이관(legacy)은 좌표계만 보고 포함 축은 안 본다 —
//	   legacy/plan.go 가 "포함 축은 기준 트리를 알아야 하는데 PlanOptions 에 그것이 없다.
//	   **그리고 없는 것이 옳다**" 고 적고 fail-open 으로 통과시킨다. 즉 005 뒤에도 절대경로가
//	   다시 들어올 문이 하나 열려 있고, **그것은 의도적으로 열어 둔 문이다.**
```

- [ ] **Step 6: 관문을 돌린다**

Run: `cd plugins/flightdeck/server && go vet ./... && gofmt -l . && go test ./... 2>&1 | grep -E "^FAIL" | head`
Expected: 전부 무출력

- [ ] **Step 7: 커밋**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-footprint-rows-backfill-after-gate-runs
git add plugins/flightdeck/server/internal/store/store.go \
        plugins/flightdeck/DESIGN.md \
        plugins/flightdeck/server/internal/judge/prescribe.go
git commit -m "docs(flightdeck): 파괴적 증분이 실제로 왔다 — §7 과 store.go 주석을 그 사실로 갱신한다

TestBundledMigrationsAreAdditive 의 실패 메시지가 '문서와 코드를 갈린 채로 두지 마라'를
명시적으로 요구한다. 세 자리를 고쳤다.

낡아 있던 것들: '증분이 한 단' (실제로는 네 단), fail-open 훅이 DESIGN.md 에는 6종
store.go 에는 4종으로 갈려 있던 것(4가 맞다), comparablePath 주석의 406/108(지금 1592/174,
005 뒤로는 0).

comparablePath 가드는 존치한다. 존치 사유 ①(옛 행이 DB 에 남아 있다)은 005 로 소멸하지만
②는 살아 있다 — legacy.PlanImport 가 기준 트리를 모른다는 이유로 포함 축을 일부러
fail-open 으로 뒀다. 사유가 바뀐 것을 안 적으면 다음 사람이 ①을 근거로 지우러 온다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 4: 랜딩 전 재측정과 전수 확인

**Files:** 없음(측정과 확인만)

- [ ] **Step 1: 운영 DB 를 읽기 전용으로 다시 잰다**

Run:

```bash
python3 - <<'PY'
import sqlite3, collections
db = sqlite3.connect('file:/home/aaron/.flightdeck/fd.db?mode=ro', uri=True)
q = lambda s: list(db.execute(s))
print("절대행:", q("select count(*) from footprint where substr(path,1,1)='/'")[0][0])
print("origin 분포:", q("select origin, count(*) from footprint where substr(path,1,1)='/' group by origin"))
print("마지막 절대행:", q("select max(first_at) from footprint where substr(path,1,1)='/'")[0][0])
print("가장 최근 상대행:", q("select max(first_at) from footprint where substr(path,1,1)<>'/'")[0][0])
PY
```

Expected: 절대행 수가 174 근처 · **마지막 절대행이 여전히 `2026-08-05T04:28:17Z`** ·
가장 최근 상대행이 방금.

**절대행이 새로 늘었으면 멈춰라** — 그것은 상류에 안 막힌 문이 있다는 뜻이고,
005 머리말의 전제가 거짓이 된다. 그 경우 판단(`note`)으로 남기고 사람에게 물어라.

- [ ] **Step 2: 머리말의 수치를 재측정값으로 갱신한다**

Step 1 의 수가 174와 다르면 `005_footprint_absolute_backfill.sql` 머리말의 분포표를 그 값으로 고친다.
갈래별 분류는 §3 의 질의를 다시 돌려 얻는다. 수가 같으면 아무것도 안 고친다.

- [ ] **Step 3: 전 패키지 시험**

Run: `cd plugins/flightdeck/server && go build ./... && go vet ./... && gofmt -l . && go test ./... 2>&1 | tail -30`
Expected: 새 FAIL 0건

- [ ] **Step 4: 위치 의존 실패가 기준선과 같은지 확인한다**

이 저장소에는 실행 위치에 따라 빨개지는 시험이 있다(적대 검증이 `TestSignalTableIsNotProposedAsHistory`
하나를 확인했다). 기준선과 비교한다:

Run:

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-footprint-rows-backfill-after-gate-runs/plugins/flightdeck/server
go test ./... 2>&1 | grep -E "^(--- )?FAIL" | sort > /tmp/claude-1000/-home-aaron-cdo-dev-kweiza-cc-plugins/2a21e081-9eb4-4c52-ad9d-6fcf5e27b820/scratchpad/after.txt
git stash && go test ./... 2>&1 | grep -E "^(--- )?FAIL" | sort > /tmp/claude-1000/-home-aaron-cdo-dev-kweiza-cc-plugins/2a21e081-9eb4-4c52-ad9d-6fcf5e27b820/scratchpad/before.txt; git stash pop
diff /tmp/claude-1000/-home-aaron-cdo-dev-kweiza-cc-plugins/2a21e081-9eb4-4c52-ad9d-6fcf5e27b820/scratchpad/before.txt /tmp/claude-1000/-home-aaron-cdo-dev-kweiza-cc-plugins/2a21e081-9eb4-4c52-ad9d-6fcf5e27b820/scratchpad/after.txt
```

Expected: `diff` 무출력 — 실패 집합이 무변경이다.

**주의:** `git stash` 는 커밋 안 된 변경만 담는다. Task 1~3 을 전부 커밋했으면 `git stash` 대신
`git checkout main -- .` 로 비교하거나, 이 단계를 생략하고 Step 3 의 결과만 쓴다.

- [ ] **Step 5: 판단을 남긴다**

```
note(kind='verified', item_id='fd-footprint-rows-backfill-after-gate-runs',
     title='구현 완료 — 랜딩 전 재측정과 전수 확인 결과',
     body='재측정값 · go vet/gofmt/go test 결과 · 실패 집합 대조 결과를 수치와 함께')
```

---

## Self-Review

**스펙 대조** — 설계 문서의 각 절이 어느 태스크에 담겼는가:

| 스펙 절 | 태스크 |
|---|---|
| §2 착수 조건 재측정 | Task 4 Step 1 (랜딩 직전 재확인) |
| §3 174건의 분포(원장) | Task 2 Step 3 (005 머리말) |
| §4 처분 결정과 기각한 갈래 넷 | Task 2 Step 3 (머리말의 ★ 문단들) |
| §5 `comparablePath` 존치 | Task 3 Step 5 |
| §6 예외 기제 | Task 1 전체 + Task 2 Step 6 |
| §7 만질 자리 | File Structure |
| §8 시험(표본 A~F·멱등) | Task 2 Step 1 |
| §9 안 하는 것 | 어느 태스크도 그것을 안 한다 — `fd migrate` 없음, 좌표계 안 건드림 |
| §10 남는 위험 | Task 2 Step 3(머리말) · Task 4(재측정) |

**스펙에서 정정한 것 하나:** §7 은 `DESIGN.md` 의 §6·§10 에도 발자국 실측 수치가 있어
고쳐야 할 수 있다고 적었다. 확인 결과 **그 두 절에 절대경로 수치는 없다.**
`DESIGN.md` 에서 만질 자리는 §7 뿐이다(691행 근처, 그리고 만료 조건 문단).

**타입 일관성:** `op`·`exemption`·`exemptReason`·`opLabels`·`destructiveExempt`·`neverExempt` 가
Task 1 에서 정의되고 Task 2 Step 6 이 그 이름 그대로 쓴다. `destructiveOps` 의 반환형이
`[]string` → `[]op` 로 바뀌는 것을 `TestDestructiveOpsNamesWhatItFound`(변경 불필요, `len()` 만 봄)와
`TestBundledMigrationsAreAdditive`(Task 1 Step 5 에서 교체)가 모두 반영한다.

**남은 조율:** `judge/prescribe.go` 는 01KZBG57·01KZBH5M 이, `DESIGN.md` §7 은 01KZBDRV 가,
`store.go`·`migrate_guard_test.go` 는 01KZ8Y25 가 함께 잡고 있다. Task 3 을 시작하기 전에
`board` 로 그들의 확정 앵커를 다시 읽어라 — 줄 번호가 밀렸을 수 있으므로 **앵커 문자열로 찾는다.**
