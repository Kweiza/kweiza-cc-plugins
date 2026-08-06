# 판단 원장 백업 (`fd export --judgments`) 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `judgment`·`judgment_link`·`snapshot` 세 표 전량을 무손실 JSONL 로 내보내는 `fd export --judgments` 를 만들고, DESIGN §7 이 약속만 하고 구현이 없던 사실을 문서에 실측으로 적는다.

**Architecture:** `store` 패키지에 백업 전용 열기·읽기·되쓰기를 두고(원문 DTO, NULL 보존), 새 `internal/ledger` 패키지가 JSONL 인코딩·파일 쓰기·되읽기를 맡는다. `cmd/fd` 는 기존 `runExport` 에 분기 하나를 더한다. 새 서브명령이 아니라 기존 명령의 플래그다.

**Tech Stack:** Go · modernc SQLite(WAL) · 표준 `encoding/json` · 시험은 실물 SQLite 파일(`t.TempDir()`)

설계 문서: `docs/superpowers/specs/2026-08-06-fd-judgment-backup-design.md` (커밋 `8dc59f4`)

---

## Global Constraints

- **대화·주석·오류 문구는 전부 한글이다.** 이 저장소의 모든 코드가 그렇다.
- **행번호를 앵커로 쓰지 마라.** 형제 브랜치 둘이 `DESIGN.md` 의 30줄 블록을 지우고 있어 §7 이하가 랜딩 순서에 따라 ~30줄 이동한다. 인용 문구로 자리를 찾아라.
- **판정은 순수 함수로 빼고 사유 문자열을 담는다.** 불리언 반환은 이 저장소의 관례에 어긋난다(`OutTargetVerdict`·`MigrationPlan` 이 선례 — 둘 다 `Reason` 을 **항상** 채운다).
- **사람이 읽는 줄은 전부 `out`(stdout)으로 낸다.** 시험 하네스 `h.run` 은 stderr 를 버리므로, stderr 로 낸 문구는 시험이 원리적으로 못 본다.
- **종료 코드**: 2 = 인자·가드 거절, 1 = 실행 실패, 0 = 성공. `runExport` 안에서 이미 일관된다.
- **오류·로그에 외부 문자열(경로·id·본문)을 실을 때는 `clip` 을 거친다.**
- **"백업"이라는 낱말을 이 코드에 쓰지 마라.** 이 저장소에서 `backup`·`BackupSuffix`·`<db>.bak-*` 는 이미 마이그레이션 직전 `VACUUM INTO` DB 파일 사본을 뜻한다. 새 것은 **원장(ledger)** 이라 부른다. 문서에서는 "판단 원장 내보내기", 코드에서는 `Ledger*`·`internal/ledger`.
- **교차 빌드 관문은 `go vet` 로 돈다.** `go build` 는 `_test.go` 를 건너뛴다.
- 작업 디렉토리: `plugins/flightdeck/server`. 시험은 `go test ./...`.

---

## File Structure

| 파일 | 책임 |
|---|---|
| `internal/store/backup.go` (새) | 원장 DTO · 백업 전용 열기 · 단일 스냅숏 읽기 · 되쓰기 |
| `internal/store/backup_test.go` (새) | 위의 시험 |
| `internal/ledger/export.go` (새) | JSONL 인코딩 · manifest · tmp→rename 쓰기 |
| `internal/ledger/read.go` (새) | JSONL 되읽기 |
| `internal/ledger/losses.go` (새) | 안 덮는 것 목록 — 순수 함수 |
| `internal/ledger/outguard.go` (새) | `IsOurOutput` — 자기 산출물을 알아본다 |
| `internal/ledger/*_test.go` (새) | 위의 시험 + 왕복 무손실 |
| `internal/store/judgment.go` (수정) | `snapshotCols` 상수 + `ListSnapshots` 순삽입 |
| `internal/web/query.go` (수정) | `snapshots` 를 지우고 `store.ListSnapshots` 로 |
| `internal/web/page.go` (수정) | 위 호출부 한 줄 |
| `cmd/fd/migrate.go` (수정) | `runExport` 에 `--judgments` 분기 |
| `cmd/fd/main.go` (수정) | `usage` 상수 한 줄 |
| `cmd/fd/migrate_test.go` (수정) | 배선 시험 |
| `plugins/flightdeck/DESIGN.md` (수정) | §7 두 곳 + §9 한 줄 |

---

## Task 1: `snapshot` 나열을 `store` 로 올린다

`snapshot` 을 나열하는 SQL 이 지금 저장소에 딱 하나 있는데 그게 `internal/web` 의 unexported 함수다. 원장 읽기가 같은 SQL 을 또 쓰면 두 벌이 되고, `cmds.go` 가 그것을 금지한다("같은 판정을 두 자리에 두면 한쪽만 고칠 때 조용히 어긋난다").

**Files:**
- Modify: `internal/store/judgment.go` (파일 끝에 순삽입)
- Modify: `internal/web/query.go` (`snapshots` 함수 삭제)
- Modify: `internal/web/page.go` (호출부 한 줄)
- Test: `internal/store/judgment_snapshots_test.go` (새)

**Interfaces:**
- Produces: `const snapshotCols` · `func (s *Store) ListSnapshots(ctx context.Context, project string) ([]model.Snapshot, error)`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/store/judgment_snapshots_test.go` 를 새로 만든다. `newStore`·`seed` 는 같은 패키지의 `store_test.go` 에 이미 있으니 **다시 정의하지 마라**(이름 충돌로 빌드가 깨진다).

```go
package store

import (
	"context"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 스냅숏 나열은 키 순이고 프로젝트로 갈린다. 이 함수가 없던 동안 유일한 나열 SQL 이
// internal/web 안에 있었고, 원장 내보내기가 그것을 또 적으면 두 벌이 된다.
func TestListSnapshotsIsKeyOrderedAndProjectScoped(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seed(t, s, "p1")
	seed(t, s, "p2")

	put := func(project, key, value string, method model.SnapshotMethod, evidence string) {
		t.Helper()
		if err := s.PutSnapshot(ctx, model.Snapshot{
			Project: project, Key: key, Value: value, Method: method, Evidence: evidence,
		}); err != nil {
			t.Fatalf("스냅숏 저장 실패(%s/%s): %v", project, key, err)
		}
	}
	put("p1", "zeta", "3", model.SnapshotCommand, "")
	put("p1", "alpha", "1", model.SnapshotManual, "손으로 셌다")
	put("p2", "other", "9", model.SnapshotCommand, "")

	got, err := s.ListSnapshots(ctx, "p1")
	if err != nil {
		t.Fatalf("ListSnapshots 실패: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("p1 스냅숏이 %d개다 — 2개를 기대한다: %+v", len(got), got)
	}
	if got[0].Key != "alpha" || got[1].Key != "zeta" {
		t.Errorf("키 순이 아니다: %q, %q", got[0].Key, got[1].Key)
	}
	if got[0].Project != "p1" {
		t.Errorf("project 가 안 채워졌다: %q", got[0].Project)
	}
	if got[0].Evidence != "손으로 셌다" {
		t.Errorf("evidence 가 유실됐다: %q", got[0].Evidence)
	}
	if got[1].Evidence != "" {
		t.Errorf("NULL evidence 가 %q 로 나왔다 — str() 이 빈 문자열로 접어야 한다", got[1].Evidence)
	}
}

// 없는 프로젝트는 오류가 아니라 빈 목록이다 — GetSnapshot 은 notFound 를 내지만
// 나열은 "아직 없다"와 "그런 프로젝트가 없다"를 구분할 필요가 없다.
func TestListSnapshotsEmptyIsNotAnError(t *testing.T) {
	s := newStore(t)
	got, err := s.ListSnapshots(context.Background(), "없는프로젝트")
	if err != nil {
		t.Fatalf("빈 목록이 오류가 됐다: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("%d개가 나왔다", len(got))
	}
}
```

- [ ] **Step 2: 시험이 실패하는지 본다**

```
cd plugins/flightdeck/server && go test ./internal/store/ -run TestListSnapshots -v
```

기대: 컴파일 실패 — `s.ListSnapshots undefined`.

- [ ] **Step 3: `snapshotCols` 와 `ListSnapshots` 를 `judgment.go` 끝에 순삽입한다**

기존 함수는 한 줄도 고치지 않는다. `GetSnapshot` 의 컬럼 순서를 그대로 쓴다.

```go
// snapshotCols 는 스냅숏 조회의 컬럼 목록이다.
//
// judgmentCols 와 같은 이유로 상수다 — 목록을 손으로 다시 적으면 순서가 어긋나는 순간
// Scan 이 조용히 엉뚱한 값을 채운다(전부 문자열이라 타입 오류도 안 난다).
const snapshotCols = `project, key, value, method, evidence, input_digest, computed_at`

// ListSnapshots 는 프로젝트의 스냅숏 전부를 키 순으로 낸다.
//
// 수는 사람이 넣은 만큼이라 페이징이 없다. 없는 프로젝트는 오류가 아니라 빈 목록이다 —
// GetSnapshot 과 달리 "아직 없다"와 "그런 프로젝트가 없다"를 가를 필요가 없다.
func (s *Store) ListSnapshots(ctx context.Context, project string) ([]model.Snapshot, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+snapshotCols+` FROM snapshot WHERE project = ? ORDER BY key`, project)
	if err != nil {
		return nil, fmt.Errorf("스냅숏 목록 조회 실패(project=%q): %w", clip(project, 64), err)
	}
	defer rows.Close()

	var out []model.Snapshot
	for rows.Next() {
		sn, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sn)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("스냅숏 목록 순회 실패: %w", err)
	}
	return out, nil
}

// scanSnapshot 은 snapshotCols 순서의 한 행을 읽는다.
// scanJudgment 과 같은 면(*sql.Row 와 *sql.Rows 둘 다)을 받는다.
func scanSnapshot(sc interface{ Scan(...any) error }) (model.Snapshot, error) {
	var sn model.Snapshot
	var evidence, digest sql.NullString
	var method, at string
	if err := sc.Scan(&sn.Project, &sn.Key, &sn.Value, &method, &evidence, &digest, &at); err != nil {
		return sn, fmt.Errorf("스냅숏 행 해석 실패: %w", err)
	}
	sn.Method = model.SnapshotMethod(method)
	sn.Evidence, sn.InputDigest = str(evidence), str(digest)
	var err error
	if sn.ComputedAt, err = parseTime(at); err != nil {
		return sn, err
	}
	return sn, nil
}
```

- [ ] **Step 4: 시험이 통과하는지 본다**

```
cd plugins/flightdeck/server && go test ./internal/store/ -run TestListSnapshots -v
```

기대: PASS 둘.

- [ ] **Step 5: `web` 이 새 함수를 쓰게 고친다**

`internal/web/query.go` 에서 `snapshots` 함수 전체를 **지운다**(주석 두 줄 포함). 그 함수는 `parseStamp` 를 쓰는데 다른 함수도 쓰므로 `parseStamp` 는 그대로 둔다.

`internal/web/page.go` 의 호출부 한 줄을 바꾼다:

```go
	sns, err := snapshots(ctx, st.DB(), proj.ID)
```

를

```go
	sns, err := st.ListSnapshots(ctx, proj.ID)
```

로. `st` 는 이미 `*store.Store` 다.

- [ ] **Step 6: 전 시험을 돌린다**

```
cd plugins/flightdeck/server && go vet ./... && go test ./...
```

기대: 전부 PASS. `query.go` 에서 안 쓰는 import 가 생기면 지운다(`database/sql` 이 다른 함수에도 쓰이는지 확인하고 판단).

- [ ] **Step 7: 커밋**

```bash
git add internal/store/judgment.go internal/store/judgment_snapshots_test.go internal/web/query.go internal/web/page.go
git commit -m "refactor(flightdeck): 스냅숏 나열을 store 로 올린다 — 유일한 나열 SQL 이 web 안에 있었다

원장 내보내기가 같은 SQL 을 또 적으면 두 벌이 되고, 한쪽만 고칠 때 조용히 어긋난다.
snapshotCols 상수는 judgmentCols 선례를 따른다 — 컬럼 목록을 손으로 다시 적으면
순서가 어긋나는 순간 Scan 이 조용히 엉뚱한 값을 채운다(전부 문자열이라 타입 오류도 안 난다).

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 2: 원장 DTO 와 단일 스냅숏 읽기

무손실이려면 `model` 을 안 거쳐야 한다. `nullStr`/`str` 이 NULL↔`""` 를 접는데, 그 접힘이 왕복으로 닫힌다는 보장이 "빈 문자열이 저장된 행이 원리적으로 없다"는 별도 논증에 의존한다. 원문 DTO 로 가면 그 논증 자체가 필요 없다.

**Files:**
- Create: `internal/store/backup.go`
- Test: `internal/store/backup_test.go`

**Interfaces:**
- Consumes: `judgmentCols`·`snapshotCols`(Task 1)·`clip`
- Produces:
  - `type LedgerJudgment struct{ ID string; Project, SessionID *string; At, Kind string; Title *string; Body string; Supersedes *string }`
  - `type LedgerLink struct{ JudgmentID, TargetKind, TargetID string }`
  - `type LedgerSnapshot struct{ Project, Key, Value, Method string; Evidence, InputDigest *string; ComputedAt string }`
  - `type LedgerDump struct{ Judgments []LedgerJudgment; Links []LedgerLink; Snapshots []LedgerSnapshot }`
  - `func (s *Store) ReadLedger(ctx context.Context) (LedgerDump, error)`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/store/backup_test.go`:

```go
package store

import (
	"context"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 원장 읽기는 DB 전량이다 — 프로젝트로 안 거른다.
// project 가 NULL 인 판단이 스키마상 가능하고, WHERE project = ? 는 그런 행을 절대 못 잡는다.
func TestReadLedgerCoversAllProjectsAndNullProject(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seed(t, s, "p1")
	seed(t, s, "p2")

	linkJudgment(t, s, "p1", model.JudgmentDecision, "i1")
	linkJudgment(t, s, "p2", model.JudgmentAsk, "i2")
	// project 를 비우면 nullStr 이 NULL 로 넣는다. FK 를 아예 안 탄다.
	if _, err := s.AddJudgment(ctx, model.Judgment{Kind: model.JudgmentNow, Body: "프로젝트 없는 판단"}); err != nil {
		t.Fatalf("project 없는 판단 저장 실패: %v", err)
	}

	d, err := s.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("ReadLedger 실패: %v", err)
	}
	if len(d.Judgments) != 3 {
		t.Fatalf("판단이 %d건이다 — 3건을 기대한다", len(d.Judgments))
	}
	var nullProject int
	for _, j := range d.Judgments {
		if j.Project == nil {
			nullProject++
		}
	}
	if nullProject != 1 {
		t.Errorf("project=NULL 판단이 %d건 — 1건을 기대한다. 포인터가 아니면 NULL 과 \"\" 가 안 갈린다", nullProject)
	}
}

// 판단 정렬은 id 순이다. ULID 라 생성순이고, 같은 DB 면 같은 바이트가 나와야 한다.
func TestReadLedgerIsDeterministicallyOrdered(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seed(t, s, "p")
	for i := 0; i < 5; i++ {
		linkJudgment(t, s, "p", model.JudgmentDecision, "i1", "i2")
	}

	first, err := s.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("ReadLedger 실패: %v", err)
	}
	second, err := s.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("ReadLedger 재실행 실패: %v", err)
	}
	for i := range first.Judgments {
		if first.Judgments[i].ID != second.Judgments[i].ID {
			t.Fatalf("두 번 읽었더니 순서가 달라졌다: %d번째 %q vs %q",
				i, first.Judgments[i].ID, second.Judgments[i].ID)
		}
		if i > 0 && first.Judgments[i-1].ID >= first.Judgments[i].ID {
			t.Fatalf("id 오름차순이 아니다: %q >= %q", first.Judgments[i-1].ID, first.Judgments[i].ID)
		}
	}
	for i := 1; i < len(first.Links); i++ {
		p, c := first.Links[i-1], first.Links[i]
		if p.JudgmentID > c.JudgmentID {
			t.Fatalf("링크가 judgment_id 순이 아니다: %q > %q", p.JudgmentID, c.JudgmentID)
		}
	}
}

// 시각은 DB 원문 문자열 그대로다. time.Time 으로 접으면 마셜이 후행 0을 지워
// 폭이 흔들리고, 그러면 사전순 정렬이 시간순과 어긋난다(store.go 의 timeLayout 주석).
func TestReadLedgerKeepsRawTimestampString(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seed(t, s, "p")
	linkJudgment(t, s, "p", model.JudgmentDecision, "i1")

	d, err := s.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("ReadLedger 실패: %v", err)
	}
	at := d.Judgments[0].At
	if len(at) != len("2006-01-02T15:04:05.000000Z") {
		t.Fatalf("at 이 폭 고정이 아니다(%q, %d자) — DB 원문을 그대로 실어야 한다", at, len(at))
	}
}
```

> `model.JudgmentNow` 는 `kind='now'` 다(`internal/model/types.go` 에서 실측). 쓸 수 있는 상수는 아홉이다: `JudgmentHandoff`·`JudgmentDecision`·`JudgmentBlocked`·`JudgmentAsk`·`JudgmentNow`·`JudgmentRejected`·`JudgmentNotDone`·`JudgmentVerified`·`JudgmentDraft`.

- [ ] **Step 2: 시험이 실패하는지 본다**

```
cd plugins/flightdeck/server && go test ./internal/store/ -run TestReadLedger -v
```

기대: 컴파일 실패 — `s.ReadLedger undefined`.

- [ ] **Step 3: `internal/store/backup.go` 를 만든다**

```go
package store

import (
	"context"
	"database/sql"
	"fmt"
)

// 판단 원장 내보내기 — `fd export --judgments` 의 저장 계층.
//
// ★ 이름을 "백업"이라고 하지 않는다. 이 패키지에서 backup·BackupSuffix·<db>.bak-* 는
// 이미 마이그레이션 직전 VACUUM INTO 로 뜨는 **DB 파일 사본**을 뜻한다. 두 개념이
// 같은 낱말을 쓰면 오류 문구와 로그에서 섞인다.
//
// ★ model 을 거치지 않는다. nullStr/str 이 NULL 과 빈 문자열을 접기 때문이다.
// 그 접힘이 왕복으로 닫히는지는 "빈 문자열이 저장된 행이 원리적으로 없다"는 별도 논증에
// 의존하는데, 원문 DTO 로 가면 그 논증 자체가 필요 없다. 포인터가 nil 이면 NULL 이다.

// LedgerJudgment 는 judgment 표 한 행의 원문이다.
type LedgerJudgment struct {
	ID         string  `json:"id"`
	Project    *string `json:"project"`
	SessionID  *string `json:"session_id"`
	At         string  `json:"at"` // DB 원문 문자열(폭 고정 마이크로초). time.Time 으로 접지 않는다
	Kind       string  `json:"kind"`
	Title      *string `json:"title"`
	Body       string  `json:"body"`
	Supersedes *string `json:"supersedes"`
}

// LedgerLink 는 judgment_link 표 한 행이다.
type LedgerLink struct {
	JudgmentID string `json:"judgment_id"`
	TargetKind string `json:"target_kind"`
	TargetID   string `json:"target_id"`
}

// LedgerSnapshot 은 snapshot 표 한 행의 원문이다.
type LedgerSnapshot struct {
	Project     string  `json:"project"`
	Key         string  `json:"key"`
	Value       string  `json:"value"`
	Method      string  `json:"method"`
	Evidence    *string `json:"evidence"`
	InputDigest *string `json:"input_digest"`
	ComputedAt  string  `json:"computed_at"`
}

// LedgerDump 는 한 순간의 세 표 전량이다.
type LedgerDump struct {
	Judgments []LedgerJudgment
	Links     []LedgerLink
	Snapshots []LedgerSnapshot
}

// ReadLedger 는 세 표를 **한 트랜잭션 안에서** 전량 읽는다.
//
// ★ 왜 트랜잭션인가. 표를 따로 읽으면 그 사이 서버가 커밋한 판단의 **링크만** 산출물에
// 들어간다(judgment 를 읽은 뒤 link 를 읽으므로). 그 링크가 가리키는 판단이 없으니
// 되읽기가 FK 로 죽는다. 트랜잭션 원자성은 DB 상태를 보장하지 서로 다른 시점의 두 읽기를
// 보장하지 않는다 — 동시 세션이 스물이 넘는 이 저장소에서 실제로 열리는 창이다.
//
// ★ 왜 Store.Tx 를 안 쓰는가. 그 함수는 DSN 의 _txlock=immediate 때문에 BEGIN IMMEDIATE 라
// 읽기만 해도 쓰기 잠금을 잡는다(Tx 주석이 그 대가를 적어 뒀다). 전량 읽기가 도는 동안
// 서버의 모든 쓰기가 busy_timeout 안에서 줄을 선다. 여기서는 BeginTx 를 직접 부르고,
// 백업 전용 열기(OpenLedger)가 DSN 을 deferred 로 준다. 기본 DSN 으로 열린 Store 에서
// 불러도 정확성은 같고(스냅숏 일관성은 유지된다) 잠금만 세진다.
//
// ★ 상한이 없다. ListJudgmentsByKind 는 limit<=0 을 50 으로 바꾸고, legacy 되쓰기는
// 100000 이라는 수를 손으로 넣는다 — 상한에 걸려 조용히 잘리면 산출물이 원본보다
// 적어지고 그 차이는 세어 보기 전에는 안 보인다. 원장은 전량이 목적이라 WHERE 절도 없다.
func (s *Store) ReadLedger(ctx context.Context) (LedgerDump, error) {
	var d LedgerDump
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return d, fmt.Errorf("원장 읽기 트랜잭션 시작 실패: %w", err)
	}
	// 읽기만 하므로 언제나 롤백으로 끝낸다. 커밋할 것이 없다.
	defer func() { _ = tx.Rollback() }()

	if d.Judgments, err = readLedgerJudgments(ctx, tx); err != nil {
		return d, err
	}
	if d.Links, err = readLedgerLinks(ctx, tx); err != nil {
		return d, err
	}
	if d.Snapshots, err = readLedgerSnapshots(ctx, tx); err != nil {
		return d, err
	}
	return d, nil
}

// readLedgerJudgments 는 판단 전량을 id 순으로 읽는다.
// id 는 ULID 라 정렬이 곧 생성순이고, 같은 DB 면 같은 바이트가 나오는 근거가 이것이다.
func readLedgerJudgments(ctx context.Context, q dbtx) ([]LedgerJudgment, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+judgmentCols+` FROM judgment ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("원장 판단 조회 실패: %w", err)
	}
	defer rows.Close()

	var out []LedgerJudgment
	for rows.Next() {
		var j LedgerJudgment
		var project, session, title, supersedes sql.NullString
		if err := rows.Scan(&j.ID, &project, &session, &j.At, &j.Kind, &title, &j.Body, &supersedes); err != nil {
			return nil, fmt.Errorf("원장 판단 행 해석 실패: %w", err)
		}
		j.Project, j.SessionID = ptrOf(project), ptrOf(session)
		j.Title, j.Supersedes = ptrOf(title), ptrOf(supersedes)
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("원장 판단 순회 실패: %w", err)
	}
	return out, nil
}

// readLedgerLinks 는 링크 전량을 읽는다.
// 정렬 셋은 PK 와 같은 순서다 — 재현성을 위해 완전 순서여야 한다.
func readLedgerLinks(ctx context.Context, q dbtx) ([]LedgerLink, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT judgment_id, target_kind, target_id FROM judgment_link
		ORDER BY judgment_id, target_kind, target_id`)
	if err != nil {
		return nil, fmt.Errorf("원장 링크 조회 실패: %w", err)
	}
	defer rows.Close()

	var out []LedgerLink
	for rows.Next() {
		var l LedgerLink
		if err := rows.Scan(&l.JudgmentID, &l.TargetKind, &l.TargetID); err != nil {
			return nil, fmt.Errorf("원장 링크 행 해석 실패: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("원장 링크 순회 실패: %w", err)
	}
	return out, nil
}

// readLedgerSnapshots 는 스냅숏 전량을 읽는다. PK 와 같은 (project, key) 순이다.
func readLedgerSnapshots(ctx context.Context, q dbtx) ([]LedgerSnapshot, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT `+snapshotCols+` FROM snapshot ORDER BY project, key`)
	if err != nil {
		return nil, fmt.Errorf("원장 스냅숏 조회 실패: %w", err)
	}
	defer rows.Close()

	var out []LedgerSnapshot
	for rows.Next() {
		var sn LedgerSnapshot
		var evidence, digest sql.NullString
		if err := rows.Scan(&sn.Project, &sn.Key, &sn.Value, &sn.Method,
			&evidence, &digest, &sn.ComputedAt); err != nil {
			return nil, fmt.Errorf("원장 스냅숏 행 해석 실패: %w", err)
		}
		sn.Evidence, sn.InputDigest = ptrOf(evidence), ptrOf(digest)
		out = append(out, sn)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("원장 스냅숏 순회 실패: %w", err)
	}
	return out, nil
}

// ptrOf 는 NULL 을 nil 로, 값을 포인터로 낸다.
// str() 과 다르다 — str 은 NULL 을 "" 로 접어 둘을 구분 불가능하게 만든다.
func ptrOf(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	v := ns.String
	return &v
}
```

- [ ] **Step 4: 시험이 통과하는지 본다**

```
cd plugins/flightdeck/server && go test ./internal/store/ -run TestReadLedger -v
```

기대: PASS 셋.

- [ ] **Step 5: 커밋**

```bash
git add internal/store/backup.go internal/store/backup_test.go
git commit -m "feat(flightdeck): 판단 원장 DTO 와 단일 스냅숏 읽기

model 을 안 거친다 — nullStr/str 이 NULL 과 빈 문자열을 접고, 그 접힘이 왕복으로 닫히는지가
별도 논증에 의존한다. 포인터가 nil 이면 NULL 이다.

세 표를 한 트랜잭션에서 읽는다. 따로 읽으면 그 사이 커밋된 판단의 링크만 산출물에 들어가고,
되읽기가 FK 로 죽는다. Store.Tx 는 안 쓴다 — _txlock=immediate 라 읽기만 해도 쓰기 잠금을 잡는다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 3: 백업 전용 열기 — 이행이 필요하면 거절한다

`store.Open` 은 `verifyPragmas` 다음에 **반드시 `s.migrate`** 를 돈다. 즉 원장 내보내기가 낡은 DB 를 만나면 백업하기 전에 스키마를 바꾼다. 백업이 그 계기가 되어서는 안 된다.

**Files:**
- Modify: `internal/store/backup.go` (순삽입)
- Modify: `internal/store/backup_test.go` (순삽입)

**Interfaces:**
- Consumes: `ProbeMigration`·`MigrationPlan`·`MigrateNone`·`dsn`·`verifyPragmas`·`Store`
- Produces: `func OpenLedger(ctx context.Context, path string, log *slog.Logger) (*Store, error)`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/store/backup_test.go` 끝에 붙인다:

```go
// 원장 열기는 스키마를 바꾸지 않는다. store.Open 은 반드시 migrate 를 돌고
// 그 앞에서 VACUUM INTO 를 뜨는데, 백업이 그 계기가 되면 안 된다.
func TestOpenLedgerDoesNotMigrateOrBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fd.db")

	// 먼저 정상 Open 으로 스키마를 올린다.
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	s, err := OpenWithLogger(path, quiet)
	if err != nil {
		t.Fatalf("초기 Open 실패: %v", err)
	}
	seed(t, s, "p")
	linkJudgment(t, s, "p", model.JudgmentDecision, "i1")
	if err := s.Close(); err != nil {
		t.Fatalf("닫기 실패: %v", err)
	}

	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat 실패: %v", err)
	}

	ls, err := OpenLedger(context.Background(), path, quiet)
	if err != nil {
		t.Fatalf("OpenLedger 실패: %v", err)
	}
	d, err := ls.ReadLedger(context.Background())
	if err != nil {
		t.Fatalf("ReadLedger 실패: %v", err)
	}
	if len(d.Judgments) != 1 {
		t.Fatalf("판단이 %d건 — 1건을 기대한다", len(d.Judgments))
	}
	if err := ls.Close(); err != nil {
		t.Fatalf("원장 핸들 닫기 실패: %v", err)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat 실패: %v", err)
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Errorf("DB 파일이 바뀌었다: %d/%v → %d/%v",
			before.Size(), before.ModTime(), after.Size(), after.ModTime())
	}
	// 새 .bak 이 뜨지 않았는지 본다.
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("디렉토리 훑기 실패: %v", err)
	}
	for _, e := range ents {
		if strings.Contains(e.Name(), ".bak-") {
			t.Errorf("원장 열기가 백업 파일을 만들었다: %s", e.Name())
		}
	}
}

// 없는 파일은 만들지 않고 거절한다. sql.Open 은 파일을 만들기 때문에,
// 부재를 확인 안 하면 백업이 빈 DB 를 하나 만들어 놓고 "0건 백업했다"고 말한다.
func TestOpenLedgerRejectsMissingFile(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	path := filepath.Join(t.TempDir(), "없다.db")
	if _, err := OpenLedger(context.Background(), path, quiet); err == nil {
		t.Fatal("없는 파일을 열었다 — 부재는 오류여야 한다")
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("없는 파일을 만들어 버렸다")
	}
}
```

import 에 `io`·`log/slog`·`os`·`path/filepath`·`strings` 를 더한다.

- [ ] **Step 2: 시험이 실패하는지 본다**

```
cd plugins/flightdeck/server && go test ./internal/store/ -run TestOpenLedger -v
```

기대: 컴파일 실패 — `OpenLedger undefined`.

- [ ] **Step 3: `OpenLedger` 를 `backup.go` 에 순삽입한다**

```go
// ledgerDSN 은 원장 읽기 전용 접속 문자열이다. 기본 dsn() 과 두 곳이 다르다.
//
//	_txlock=deferred      기본은 immediate 다. 읽기 스냅숏만 잡고 서버 쓰기를 안 막는다.
//	                      deferred 의 알려진 위험(읽기 뒤 쓰기 승격이 SQLITE_BUSY 로 즉시
//	                      실패하고 busy_timeout 이 안 듣는다)은 원장이 쓰기를 하지 않으므로
//	                      발생할 자리가 없다.
//	journal_mode 를 안 건다  그것이 이 DSN 에서 파일을 바꿀 수 있는 유일한 pragma 다.
//	                      이미 WAL 인 파일은 되읽기가 그대로 wal 을 내므로 verifyPragmas 는 통과한다.
func ledgerDSN(path string) string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Set("_txlock", "deferred")
	return "file:" + path + "?" + q.Encode()
}

// OpenLedger 는 원장을 읽기 위해 DB 를 연다. **스키마를 바꾸지 않는다.**
//
// ★ store.Open 을 쓰지 않는 이유. 그것은 verifyPragmas 다음에 반드시 s.migrate 를 돌고,
// 판정에 따라 증분을 적용하며 그 앞에서 VACUUM INTO 백업을 뜬다. 즉 낡은 DB 를 만나면
// **백업하기 전에 스키마를 바꾼다.** 백업이 그 계기가 되면 안 된다.
//
// ★ mode=ro 를 쓰지 않는 이유. 서버가 죽은 채 -wal 이 남아 있으면 읽기 전용 연결은
// WAL 복구를 못 해 **열기 자체가 실패한다.** 원장 내보내기는 정확히 그 상황에서 돌아야 한다.
// "이행이 필요하면 거절"이 mode=ro 보다 강하다 — 애초에 바꿀 수 있는 상태로 안 들어간다.
//
// 없는 파일에는 오류를 낸다. sql.Open 은 파일을 **만들기** 때문에, 부재를 확인 안 하고 열면
// 내보내기가 빈 DB 를 하나 만들어 놓고 "0건 내보냈다"고 말한다(ProbeMigration 과 같은 판단).
func OpenLedger(ctx context.Context, path string, log *slog.Logger) (*Store, error) {
	if log == nil {
		log = slog.Default()
	}
	plan, err := ProbeMigration(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("원장을 읽기 전에 DB 상태를 재지 못했다(path=%q): %w", clip(path, 200), err)
	}
	if plan.Action != MigrateNone {
		// Reason 은 PlanMigration 이 항상 채운다 — 새 문구를 지어내지 않는다.
		return nil, fmt.Errorf("이 바이너리로 열면 DB 가 바뀐다(%s) — 원장 내보내기를 거절한다: %s "+
			"(먼저 fd serve 를 이 바이너리로 올려 스키마를 맞춰라)", plan.Action, plan.Reason)
	}

	db, err := sql.Open("sqlite", ledgerDSN(path))
	if err != nil {
		return nil, fmt.Errorf("원장용 sqlite 열기 실패(path=%q): %w", clip(path, 200), err)
	}
	// 읽기 한 갈래뿐이라 커넥션을 늘릴 이유가 없다. 트랜잭션이 커넥션 하나에 묶인다.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("원장용 sqlite 접속 실패(path=%q): %w", clip(path, 200), err)
	}

	s := &Store{db: db, path: path, log: log}
	// DSN pragma 가 실제로 걸렸는지 되읽어 확인한다 — 드라이버는 모르는 pragma 를 조용히 무시한다.
	if err := s.verifyPragmas(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
```

import 에 `log/slog`·`net/url` 을 더한다.

- [ ] **Step 4: 시험이 통과하는지 본다**

```
cd plugins/flightdeck/server && go test ./internal/store/ -run TestOpenLedger -v
```

기대: PASS 둘. `verifyPragmas` 가 `journal_mode=wal` 을 요구하는데 앞선 `OpenWithLogger` 가 이미 파일을 WAL 로 만들었으므로 통과한다. 실패하면 `journal_mode` 되읽기 값을 로그로 찍어 확인하라.

- [ ] **Step 5: 커밋**

```bash
git add internal/store/backup.go internal/store/backup_test.go
git commit -m "feat(flightdeck): 원장 열기는 스키마를 바꾸지 않는다 — 이행이 필요하면 거절한다

store.Open 은 반드시 migrate 를 돌고 그 앞에서 VACUUM INTO 를 뜬다. 백업이 그 계기가 되면 안 된다.
mode=ro 는 안 쓴다 — 서버가 죽은 채 -wal 이 남으면 읽기 전용 연결이 WAL 복구를 못 해 열기가 실패하고,
원장 내보내기는 정확히 그 상황에서 돌아야 한다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 4: 되쓰기 — 무손실을 증명할 반대 방향

되읽는 코드가 없으면 "무손실"은 검증된 적 없는 주장이다. CLI 에는 안 붙인다 — 후속에서 배선만 하면 된다.

**Files:**
- Modify: `internal/store/backup.go` (순삽입)
- Modify: `internal/store/backup_test.go` (순삽입)

**Interfaces:**
- Consumes: `LedgerDump`·`Store.Tx`
- Produces: `func (s *Store) WriteLedger(ctx context.Context, d LedgerDump) error`

- [ ] **Step 1: 실패하는 시험을 쓴다**

```go
// 되쓰기는 원문을 그대로 되살린다 — id·at·NULL 까지.
// AddJudgment 를 안 쓰는 이유가 이것이다(그것은 빈 ID/At 을 자기가 채운다).
func TestWriteLedgerRestoresRowsVerbatim(t *testing.T) {
	src := newStore(t)
	ctx := context.Background()
	seed(t, src, "p")
	first := linkJudgment(t, src, "p", model.JudgmentDecision, "i1", "i2")
	// supersedes 가 실제로 걸린 행을 만든다 — 자기참조 FK 라 삽입 순서 제약이 여기서 드러난다.
	if _, err := src.AddJudgment(ctx, model.Judgment{
		Project: "p", Kind: model.JudgmentDecision, Title: "정정", Body: "앞 판단을 대체한다",
		Supersedes: first,
	}); err != nil {
		t.Fatalf("supersedes 판단 저장 실패: %v", err)
	}
	if err := src.PutSnapshot(ctx, model.Snapshot{
		Project: "p", Key: "k", Value: "1", Method: model.SnapshotCommand,
	}); err != nil {
		t.Fatalf("스냅숏 저장 실패: %v", err)
	}

	want, err := src.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("원본 읽기 실패: %v", err)
	}

	// 빈 DB 에 되쓴다. project·session·machine 은 원장 밖이라 미리 만든다.
	dst := newStore(t)
	seed(t, dst, "p")
	if err := dst.WriteLedger(ctx, want); err != nil {
		t.Fatalf("WriteLedger 실패: %v", err)
	}

	got, err := dst.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("복원본 읽기 실패: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("원본과 복원본이 다르다:\n원본 %+v\n복원 %+v", want, got)
	}
}

// 같은 id 를 두 번 넣으면 거절한다. judgment 는 트리거로 UPDATE·DELETE 가 금지돼 있어
// 잘못 넣은 행을 고치거나 지울 수 없다 — 조용히 넘어가면 되돌릴 방법이 없다.
func TestWriteLedgerRejectsDuplicateID(t *testing.T) {
	src := newStore(t)
	ctx := context.Background()
	seed(t, src, "p")
	linkJudgment(t, src, "p", model.JudgmentDecision, "i1")
	d, err := src.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("원본 읽기 실패: %v", err)
	}

	dst := newStore(t)
	seed(t, dst, "p")
	if err := dst.WriteLedger(ctx, d); err != nil {
		t.Fatalf("첫 되쓰기 실패: %v", err)
	}
	if err := dst.WriteLedger(ctx, d); err == nil {
		t.Fatal("같은 원장을 두 번 되썼는데 통과했다 — 판단은 추가 전용이라 되돌릴 수 없다")
	}

	// 실패한 되쓰기가 부분 적용으로 남지 않는지 본다.
	after, err := dst.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("재확인 실패: %v", err)
	}
	if len(after.Judgments) != len(d.Judgments) {
		t.Errorf("판단이 %d건으로 늘었다 — 한 트랜잭션이라 그대로여야 한다", len(after.Judgments))
	}
}
```

import 에 `reflect` 를 더한다.

- [ ] **Step 2: 시험이 실패하는지 본다**

```
cd plugins/flightdeck/server && go test ./internal/store/ -run TestWriteLedger -v
```

기대: 컴파일 실패 — `dst.WriteLedger undefined`.

- [ ] **Step 3: `WriteLedger` 를 `backup.go` 에 순삽입한다**

```go
// WriteLedger 는 원장을 DB 에 되쓴다. **CLI 표면이 없다** — 지금은 무손실을 시험이 증명하는
// 데 쓰이고, 후속에서 `fd import --judgments` 를 배선만 하면 된다.
//
// ★ AddJudgment 를 쓰지 않는다. 그것은 빈 ID 에 새 ULID 를, 빈 At 에 지금 시각을 채운다 —
// 복원은 원문을 그대로 되살려야 하므로 raw INSERT 로 간다. 트리거와 CHECK 는 그대로 걸린다
// (그것이 안전핀이다).
//
// ★ 정책은 "빈 표 전제"다. judgment 는 judgment_no_update·judgment_no_delete 트리거로
// UPDATE·DELETE 가 물리적으로 금지돼 있어, 잘못 넣은 행을 고치거나 지울 수 없다.
// 그래서 중복 id 를 건너뛰지 않고 거절한다 — 조용히 넘어가면 무엇이 반쯤 들어갔는지 모른다.
//
// ★ project·session·machine 은 원장 밖이다. 이 셋이 이미 있는 DB 를 전제한다.
// 없으면 FK 위반으로 트랜잭션 전체가 거절되고, 그 거절이 곧 "전제가 안 맞다"는 신호다.
func (s *Store) WriteLedger(ctx context.Context, d LedgerDump) error {
	return s.Tx(ctx, func(t *Tx) error {
		// ★ supersedes 는 judgment 를 자기참조하고, 원장의 id 순서가 그 참조 순서와 같다는
		//   보장이 없다. FK 검사를 커밋 시점으로 미뤄 순서 제약을 없앤다(move.go 와 같은 수단).
		//   이 pragma 는 **이 트랜잭션에만** 걸리고 커밋 때 전부 검사된다.
		if _, err := t.tx.ExecContext(t.ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
			return fmt.Errorf("FK 검사 미루기 실패: %w", err)
		}
		for _, j := range d.Judgments {
			if _, err := t.tx.ExecContext(t.ctx, `
				INSERT INTO judgment(id, project, session_id, at, kind, title, body, supersedes)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				j.ID, j.Project, j.SessionID, j.At, j.Kind, j.Title, j.Body, j.Supersedes); err != nil {
				return fmt.Errorf("원장 판단 되쓰기 실패(id=%q kind=%q): %w",
					clip(j.ID, 64), clip(j.Kind, 32), err)
			}
		}
		for _, l := range d.Links {
			if _, err := t.tx.ExecContext(t.ctx,
				`INSERT INTO judgment_link(judgment_id, target_kind, target_id) VALUES (?, ?, ?)`,
				l.JudgmentID, l.TargetKind, l.TargetID); err != nil {
				return fmt.Errorf("원장 링크 되쓰기 실패(judgment=%q target=%s/%s): %w",
					clip(l.JudgmentID, 64), clip(l.TargetKind, 32), clip(l.TargetID, 64), err)
			}
		}
		for _, sn := range d.Snapshots {
			if _, err := t.tx.ExecContext(t.ctx, `
				INSERT INTO snapshot(project, key, value, method, evidence, input_digest, computed_at)
				VALUES (?, ?, ?, ?, ?, ?, ?)`,
				sn.Project, sn.Key, sn.Value, sn.Method,
				sn.Evidence, sn.InputDigest, sn.ComputedAt); err != nil {
				return fmt.Errorf("원장 스냅숏 되쓰기 실패(project=%q key=%q): %w",
					clip(sn.Project, 64), clip(sn.Key, 64), err)
			}
		}
		return nil
	})
}
```

> `*string` 을 `ExecContext` 인자로 그대로 넘기면 `database/sql` 이 nil 포인터를 NULL 로, 값이 있으면 그 문자열로 바인딩한다. `nullStr` 을 거칠 필요가 없다 — 그것은 `""` 를 NULL 로 접는 함수이고, 여기서는 접힘을 피하는 것이 목적이다.

- [ ] **Step 4: 시험이 통과하는지 본다**

```
cd plugins/flightdeck/server && go test ./internal/store/ -run TestWriteLedger -v
```

기대: PASS 둘.

- [ ] **Step 5: 커밋**

```bash
git add internal/store/backup.go internal/store/backup_test.go
git commit -m "feat(flightdeck): 원장 되쓰기 — CLI 표면 없이 함수만 둔다

되읽는 코드가 없으면 '무손실'은 검증된 적 없는 주장이다. AddJudgment 를 안 쓴다 —
그것은 빈 ID 와 At 을 자기가 채우는데 복원은 원문을 그대로 되살려야 한다.
중복 id 는 건너뛰지 않고 거절한다: judgment 는 트리거로 UPDATE·DELETE 가 금지돼
잘못 넣은 행을 되돌릴 수 없다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 5: `internal/ledger` — 인코딩 · manifest · 손실 목록

**Files:**
- Create: `internal/ledger/export.go`
- Create: `internal/ledger/losses.go`
- Test: `internal/ledger/export_test.go`

**Interfaces:**
- Consumes: `store.LedgerDump`·`store.LedgerJudgment`·`store.LedgerLink`·`store.LedgerSnapshot`
- Produces:
  - `const FormatName = "fd-judgment-backup"` · `const FormatVersion = 1` · `const ManifestName = "manifest.json"`
  - `type Manifest struct{ Format string; FormatVersion, SchemaVersion int; ExportedAt string; Counts Counts }`
  - `type Counts struct{ Judgments, Links, Snapshots int }`
  - `func Encode(d store.LedgerDump, schemaVersion int, exportedAt string) (map[string][]byte, Manifest, error)`
  - `func Losses() []string`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/ledger/export_test.go`:

```go
package ledger

import (
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/store"
)

func ptr(s string) *string { return &s }

func sampleDump() store.LedgerDump {
	return store.LedgerDump{
		Judgments: []store.LedgerJudgment{
			{ID: "01A", Project: ptr("p"), SessionID: nil,
				At: "2026-08-06T00:00:01.000000Z", Kind: "decision",
				Title: nil, Body: "본문", Supersedes: nil},
			{ID: "01B", Project: ptr("p"), SessionID: ptr("s1"),
				At: "2026-08-06T00:00:02.000000Z", Kind: "ask",
				Title: ptr("제목"), Body: "둘째", Supersedes: ptr("01A")},
		},
		Links: []store.LedgerLink{
			{JudgmentID: "01A", TargetKind: "item", TargetID: "i1"},
		},
		Snapshots: []store.LedgerSnapshot{
			{Project: "p", Key: "k", Value: "1", Method: "command",
				Evidence: nil, InputDigest: nil, ComputedAt: "2026-08-06T00:00:00.000000Z"},
		},
	}
}

// 같은 입력은 같은 바이트를 낸다. 이것이 없으면 매시간 git 커밋이 무의미한 커밋을 쌓는다.
func TestEncodeIsDeterministic(t *testing.T) {
	d := sampleDump()
	a, _, err := Encode(d, 4, "2026-08-06T00:00:00.000000Z")
	if err != nil {
		t.Fatalf("Encode 실패: %v", err)
	}
	b, _, err := Encode(d, 4, "2026-08-06T00:00:00.000000Z")
	if err != nil {
		t.Fatalf("Encode 재실행 실패: %v", err)
	}
	for name, av := range a {
		if string(av) != string(b[name]) {
			t.Errorf("%s 가 두 번 인코딩에서 달라졌다", name)
		}
	}
}

// NULL 은 JSON null 이다. "" 로 나가면 되읽기가 NULL 과 빈 문자열을 못 가른다.
func TestEncodeKeepsNullAsJSONNull(t *testing.T) {
	files, _, err := Encode(sampleDump(), 4, "2026-08-06T00:00:00.000000Z")
	if err != nil {
		t.Fatalf("Encode 실패: %v", err)
	}
	first := strings.SplitN(string(files["judgments.jsonl"]), "\n", 2)[0]
	for _, want := range []string{`"session_id":null`, `"title":null`, `"supersedes":null`} {
		if !strings.Contains(first, want) {
			t.Errorf("%s 가 없다:\n%s", want, first)
		}
	}
	if strings.Contains(first, `"session_id":""`) {
		t.Error("NULL 이 빈 문자열로 나갔다")
	}
}

// 시각은 DB 원문 그대로, 폭 고정이다.
func TestEncodeKeepsRawTimestamp(t *testing.T) {
	files, _, err := Encode(sampleDump(), 4, "2026-08-06T00:00:00.000000Z")
	if err != nil {
		t.Fatalf("Encode 실패: %v", err)
	}
	if !strings.Contains(string(files["judgments.jsonl"]), `"at":"2026-08-06T00:00:01.000000Z"`) {
		t.Errorf("시각이 원문이 아니다:\n%s", files["judgments.jsonl"])
	}
}

// 한 줄이 한 행이다. 그리고 파일 넷이 나온다.
func TestEncodeProducesFourFilesAndLinePerRow(t *testing.T) {
	files, m, err := Encode(sampleDump(), 4, "2026-08-06T00:00:00.000000Z")
	if err != nil {
		t.Fatalf("Encode 실패: %v", err)
	}
	for _, name := range []string{"judgments.jsonl", "judgment_links.jsonl", "snapshots.jsonl", ManifestName} {
		if _, ok := files[name]; !ok {
			t.Errorf("%s 가 없다", name)
		}
	}
	lines := strings.Count(strings.TrimRight(string(files["judgments.jsonl"]), "\n"), "\n") + 1
	if lines != 2 {
		t.Errorf("판단 줄이 %d개 — 2개를 기대한다", lines)
	}
	if m.SchemaVersion != 4 || m.FormatVersion != FormatVersion || m.Format != FormatName {
		t.Errorf("manifest 가 이상하다: %+v", m)
	}
	if m.Counts.Judgments != 2 || m.Counts.Links != 1 || m.Counts.Snapshots != 1 {
		t.Errorf("건수가 틀리다: %+v", m.Counts)
	}
}

// 74KB 본문이 한 줄로 나간다 — 지금 DB 의 실제 최댓값이 74,227B 다.
// 읽는 쪽이 bufio.Scanner 기본 상한(64KB)을 쓰면 여기서 곧바로 죽는다.
func TestEncodeHandlesBodyOverScannerDefault(t *testing.T) {
	d := sampleDump()
	d.Judgments[0].Body = strings.Repeat("가", 30000) // UTF-8 로 90,000B
	files, _, err := Encode(d, 4, "2026-08-06T00:00:00.000000Z")
	if err != nil {
		t.Fatalf("Encode 실패: %v", err)
	}
	first := strings.SplitN(string(files["judgments.jsonl"]), "\n", 2)[0]
	if len(first) < 64*1024 {
		t.Fatalf("픽스처가 상한보다 작다(%dB) — 이 시험이 아무것도 안 본다", len(first))
	}
}

// 손실 목록은 순수 함수다. 산문에만 적어 두면 코드가 더 잃기 시작해도 아무도 모른다.
func TestLossesNamesTheKnownGaps(t *testing.T) {
	joined := strings.Join(Losses(), "\n")
	for _, want := range []string{"아웃박스", "judgment_fts", "project", "session", "machine"} {
		if !strings.Contains(joined, want) {
			t.Errorf("손실 목록에 %q 축이 없다: %v", want, Losses())
		}
	}
}
```

- [ ] **Step 2: 시험이 실패하는지 본다**

```
cd plugins/flightdeck/server && go test ./internal/ledger/ -v
```

기대: 패키지가 없어 실패.

- [ ] **Step 3: `internal/ledger/losses.go` 를 만든다**

```go
// Package ledger 는 판단 원장(judgment · judgment_link · snapshot)을 JSONL 로 내보내고
// 되읽는다.
//
// ★ 이름이 "backup" 이 아닌 이유: internal/store 에서 backup·BackupSuffix·<db>.bak-* 는
// 이미 마이그레이션 직전 VACUUM INTO 로 뜨는 DB 파일 사본을 뜻한다. 두 개념이 같은 낱말을
// 쓰면 오류 문구와 로그에서 섞인다.
package ledger

// Losses 는 이 내보내기가 **덮지 않는 것** 전량이다.
//
// 순수 함수로 두는 이유: 시험이 이 목록을 직접 부르고, 명령이 그대로 출력한다.
// 산문에만 적어 두면 코드가 더 잃기 시작해도 아무도 모른다
// (internal/legacy 의 RoundTripLosses 가 같은 규율의 선례다).
func Losses() []string {
	return []string{
		"아웃박스에 갇힌 판단(pending·rejected) — 이 명령은 DB 만 읽는다. " +
			"서버가 거절해 DB 에 못 들어간 판단은 상태 디렉토리의 JSONL 에만 남는다",
		"`judgment_fts` 와 그림자 표 넷 — judgment_fts_ins 가 AFTER INSERT 트리거라 " +
			"되읽기 때 자동으로 다시 채워진다. 손실 0이다",
		"`rowid` — 복원 후 원본과 달라진다. 안정 식별자는 judgment.id 뿐이고 " +
			"FTS 조인은 트리거가 같은 rowid 로 맞춘다",
		"`project`·`session`·`machine` 표 — 무손실 복원의 FK 폐포에 필요하지만 원장 밖이다. " +
			"되읽기는 이 셋이 이미 있는 DB 를 전제한다",
	}
}
```

- [ ] **Step 4: `internal/ledger/export.go` 를 만든다**

```go
package ledger

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/kweiza/flightdeck/internal/store"
)

const (
	// FormatName 은 manifest 의 형식 이름이다. 출력 자리 판정이 자기 산출물을
	// 알아보는 데도 쓰인다(outguard.go).
	FormatName = "fd-judgment-backup"
	// FormatVersion 은 이 산출물 배치의 버전이다. 파일 이름이나 줄 구조가 바뀌면 올린다.
	FormatVersion = 1
	// ManifestName 은 매니페스트 파일 이름이다.
	ManifestName = "manifest.json"

	judgmentsFile = "judgments.jsonl"
	linksFile     = "judgment_links.jsonl"
	snapshotsFile = "snapshots.jsonl"
)

// Counts 는 내보낸 행 수다.
type Counts struct {
	Judgments int `json:"judgments"`
	Links     int `json:"judgment_links"`
	Snapshots int `json:"snapshots"`
}

// Manifest 는 산출물의 머리다.
//
// SchemaVersion 이 무손실의 안전핀이다 — 스키마가 오른 뒤 옛 버전으로 뜬 원장을 되읽으면
// 조용히 깨지는데, 이 값이 있으면 거절할 수 있다.
type Manifest struct {
	Format        string `json:"format"`
	FormatVersion int    `json:"format_version"`
	SchemaVersion int    `json:"schema_version"`
	ExportedAt    string `json:"exported_at"`
	Counts        Counts `json:"counts"`
}

// Encode 는 원장을 파일 이름 → 바이트로 인코딩한다. 파일을 쓰지 않는다.
//
// ★ 같은 입력은 같은 바이트를 낸다. exportedAt 을 인자로 받는 이유가 그것이다 —
// 함수 안에서 time.Now() 를 부르면 결정성이 깨지고, 그러면 매시간 git 커밋이
// 내용이 안 바뀌어도 새 커밋을 쌓는다.
//
// ★ json.Encoder 대신 json.Marshal + 손수 개행을 쓴다. Encoder 는 Encode 마다 개행을
// 붙여 주지만 SetEscapeHTML(false) 를 안 끄면 본문의 <, >, & 를 이스케이프한다 —
// 판단 본문에 코드가 들어가는 이 저장소에서 그 치환은 원문 대조를 깨뜨린다.
func Encode(d store.LedgerDump, schemaVersion int, exportedAt string) (map[string][]byte, Manifest, error) {
	m := Manifest{
		Format:        FormatName,
		FormatVersion: FormatVersion,
		SchemaVersion: schemaVersion,
		ExportedAt:    exportedAt,
		Counts: Counts{
			Judgments: len(d.Judgments),
			Links:     len(d.Links),
			Snapshots: len(d.Snapshots),
		},
	}

	judgments, err := encodeLines(len(d.Judgments), func(i int) any { return d.Judgments[i] })
	if err != nil {
		return nil, m, fmt.Errorf("판단 인코딩 실패: %w", err)
	}
	links, err := encodeLines(len(d.Links), func(i int) any { return d.Links[i] })
	if err != nil {
		return nil, m, fmt.Errorf("링크 인코딩 실패: %w", err)
	}
	snapshots, err := encodeLines(len(d.Snapshots), func(i int) any { return d.Snapshots[i] })
	if err != nil {
		return nil, m, fmt.Errorf("스냅숏 인코딩 실패: %w", err)
	}

	// 매니페스트는 사람이 열어 보는 파일이라 들여쓴다. JSONL 셋은 한 줄 한 행이라 안 들여쓴다.
	manifest, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, m, fmt.Errorf("매니페스트 인코딩 실패: %w", err)
	}
	manifest = append(manifest, '\n')

	return map[string][]byte{
		judgmentsFile: judgments,
		linksFile:     links,
		snapshotsFile: snapshots,
		ManifestName:  manifest,
	}, m, nil
}

// encodeLines 는 n개 행을 한 줄에 하나씩 JSON 으로 쓴다.
func encodeLines(n int, at func(int) any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// ★ HTML 이스케이프를 끈다. 켜져 있으면 판단 본문의 <, >, & 가 < 류로 바뀌어
	//   DB 원문과 글자가 달라지고, 그러면 원문 대조가 불가능해진다.
	enc.SetEscapeHTML(false)
	for i := 0; i < n; i++ {
		if err := enc.Encode(at(i)); err != nil { // Encode 가 줄 끝에 개행을 붙인다
			return nil, err
		}
	}
	return buf.Bytes(), nil
}
```

- [ ] **Step 5: 시험이 통과하는지 본다**

```
cd plugins/flightdeck/server && go test ./internal/ledger/ -v
```

기대: PASS 여섯.

- [ ] **Step 6: 커밋**

```bash
git add internal/ledger/export.go internal/ledger/losses.go internal/ledger/export_test.go
git commit -m "feat(flightdeck): 판단 원장 JSONL 인코딩과 손실 목록

같은 입력은 같은 바이트를 낸다 — exportedAt 을 인자로 받는 이유다. 함수 안에서 time.Now 를
부르면 결정성이 깨지고, 매시간 git 커밋이 내용이 안 바뀌어도 새 커밋을 쌓는다.
SetEscapeHTML(false) — 켜져 있으면 판단 본문의 <, >, & 가 \\u003c 류로 바뀌어 원문 대조가 깨진다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 6: 파일 쓰기 · 자기 산출물 인식 · 되읽기

**Files:**
- Create: `internal/ledger/write.go`
- Create: `internal/ledger/outguard.go`
- Create: `internal/ledger/read.go`
- Test: `internal/ledger/write_test.go`

**Interfaces:**
- Consumes: Task 5 의 `Encode`·`Manifest`·`ManifestName`·`Losses`
- Produces:
  - `func Write(files map[string][]byte, dir string) ([]string, error)`
  - `func IsOurOutput(dir string) bool`
  - `func Read(dir string) (store.LedgerDump, Manifest, error)`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/ledger/write_test.go`:

```go
package ledger

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// 쓰기는 원자적이다 — tmp 에 쓰고 rename 한다. 중간에 죽어도 반쪽 파일이 안 남는다.
func TestWriteLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	files, _, err := Encode(sampleDump(), 4, "2026-08-06T00:00:00.000000Z")
	if err != nil {
		t.Fatalf("Encode 실패: %v", err)
	}
	written, err := Write(files, dir)
	if err != nil {
		t.Fatalf("Write 실패: %v", err)
	}
	if len(written) != 4 {
		t.Errorf("파일이 %d개 — 4개를 기대한다: %v", len(written), written)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("훑기 실패: %v", err)
	}
	for _, e := range ents {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("임시 파일이 남았다: %s", e.Name())
		}
	}
	if len(ents) != 4 {
		t.Errorf("디렉토리에 %d개가 있다 — 4개를 기대한다", len(ents))
	}
}

// 목록은 정렬돼 나온다 — 출력이 실행마다 흔들리면 안 된다.
func TestWriteReturnsSortedNames(t *testing.T) {
	dir := t.TempDir()
	files, _, _ := Encode(sampleDump(), 4, "2026-08-06T00:00:00.000000Z")
	written, err := Write(files, dir)
	if err != nil {
		t.Fatalf("Write 실패: %v", err)
	}
	for i := 1; i < len(written); i++ {
		if written[i-1] > written[i] {
			t.Fatalf("정렬이 안 됐다: %v", written)
		}
	}
}

// 자기 산출물을 알아본다 — 두 번째 실행이 --force 없이 돌아야 한다.
func TestIsOurOutput(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, dir string)
		want  bool
	}{
		{"빈 자리", func(t *testing.T, dir string) {}, false},
		{"우리 산출물", func(t *testing.T, dir string) {
			files, _, _ := Encode(sampleDump(), 4, "2026-08-06T00:00:00.000000Z")
			if _, err := Write(files, dir); err != nil {
				t.Fatalf("Write 실패: %v", err)
			}
		}, true},
		{"남의 manifest", func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, ManifestName),
				[]byte(`{"format":"남의것"}`), 0o600); err != nil {
				t.Fatalf("쓰기 실패: %v", err)
			}
		}, false},
		{"깨진 manifest", func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, ManifestName),
				[]byte(`{{{`), 0o600); err != nil {
				t.Fatalf("쓰기 실패: %v", err)
			}
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			c.setup(t, dir)
			if got := IsOurOutput(dir); got != c.want {
				t.Errorf("IsOurOutput=%v — 기대 %v", got, c.want)
			}
		})
	}
}

// 파일 왕복: 인코딩 → 쓰기 → 되읽기가 원본과 같아야 한다.
func TestReadRoundTripsEncodedFiles(t *testing.T) {
	dir := t.TempDir()
	want := sampleDump()
	files, wantM, err := Encode(want, 4, "2026-08-06T00:00:00.000000Z")
	if err != nil {
		t.Fatalf("Encode 실패: %v", err)
	}
	if _, err := Write(files, dir); err != nil {
		t.Fatalf("Write 실패: %v", err)
	}
	got, gotM, err := Read(dir)
	if err != nil {
		t.Fatalf("Read 실패: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("원장이 왕복에서 달라졌다:\n원본 %+v\n되읽음 %+v", want, got)
	}
	if !reflect.DeepEqual(wantM, gotM) {
		t.Errorf("매니페스트가 달라졌다:\n%+v\n%+v", wantM, gotM)
	}
}

// 64KB 를 넘는 줄을 읽는다 — 지금 DB 의 최장 본문이 74,227B 다.
func TestReadHandlesLongLines(t *testing.T) {
	dir := t.TempDir()
	want := sampleDump()
	want.Judgments[0].Body = strings.Repeat("가", 30000)
	files, _, _ := Encode(want, 4, "2026-08-06T00:00:00.000000Z")
	if _, err := Write(files, dir); err != nil {
		t.Fatalf("Write 실패: %v", err)
	}
	got, _, err := Read(dir)
	if err != nil {
		t.Fatalf("긴 줄 되읽기 실패(bufio.Scanner 기본 상한 64KB 를 넘었는가): %v", err)
	}
	if got.Judgments[0].Body != want.Judgments[0].Body {
		t.Error("긴 본문이 달라졌다")
	}
}
```

- [ ] **Step 2: 시험이 실패하는지 본다**

```
cd plugins/flightdeck/server && go test ./internal/ledger/ -run 'TestWrite|TestIsOurOutput|TestRead' -v
```

기대: 컴파일 실패 — `Write`·`IsOurOutput`·`Read` 가 없다.

- [ ] **Step 3: `internal/ledger/write.go` 를 만든다**

```go
package ledger

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Write 는 인코딩된 파일들을 dir 에 원자적으로 쓴다. 만든 파일 이름을 정렬해 낸다.
//
// ★ tmp 에 쓰고 rename 한다. os.WriteFile 직접 쓰기는 중간 실패 시 반쪽 파일을 남긴다 —
// 판단 자산에는 맞지 않는다(cmd/fd/outbox.go 의 Outbox.keep 이 같은 규율의 선례다).
func Write(files map[string][]byte, dir string) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("원장 디렉토리 생성 실패(%q): %w", clip(dir, 200), err)
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	// ★ map 순회는 순서가 흔들린다. 정렬해야 반환 목록과 출력이 실행마다 같다.
	sort.Strings(names)

	for _, name := range names {
		if err := writeAtomic(filepath.Join(dir, name), files[name]); err != nil {
			return nil, err
		}
	}
	return names, nil
}

// writeAtomic 은 tmp+rename 으로 쓴다.
func writeAtomic(path string, body []byte) error {
	// ★ tmp 이름은 프로세스마다 다르다. 고정 이름이면 떨어진 갈래 둘이 같은 tmp 에
	//   O_TRUNC 로 쓰고, 그러면 서로의 바이트가 섞인 채 rename 된다.
	tmp := fmt.Sprintf("%s.%s.tmp", path, tmpNonce())
	// 0600 — 판단 본문이라 남이 못 읽게 한다.
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		// ★ 실패해도 치운다. os.WriteFile 은 O_CREATE|O_TRUNC 로 먼저 만들고 쓰므로
		//   ENOSPC 로 죽어도 파일은 남고, 이름이 유일해진 뒤로는 아무도 안 치운다.
		os.Remove(tmp)
		return fmt.Errorf("원장 기록 실패(%q): %w", clip(path, 200), err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("원장 교체 실패(%q): %w", clip(path, 200), err)
	}
	return nil
}

// tmpNonce 는 임시 파일 이름에 붙일 조각이다. pid 만으로는 부족하다 —
// 한 프로세스 안 두 고루틴도 같은 tmp 를 다툴 수 있다.
// (cmd/fd/outbox.go 에 같은 함수가 있지만 그것은 package main 이라 여기서 못 쓴다.)
func tmpNonce() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d-%x", os.Getpid(), time.Now().UnixNano())
	}
	return fmt.Sprintf("%d-%s", os.Getpid(), hex.EncodeToString(b[:]))
}

// clip 은 오류에 실을 외부 문자열을 자른다.
// (store·legacy 에 같은 함수가 있으나 둘 다 unexported 다.)
func clip(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n]) + "…"
}
```

- [ ] **Step 4: `internal/ledger/outguard.go` 를 만든다**

```go
package ledger

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// IsOurOutput 은 이 디렉토리가 앞선 원장 내보내기의 산출물인지 본다.
//
// ★ 왜 필요한가. 원장은 같은 자리에 계속 쓰는 것이 정상 사용인데, legacy 의 not-empty
// 가드를 그대로 태우면 두 번째 실행부터 매번 --force 를 요구받는다. 자기 산출물이면
// 갱신으로 보는 것이 백업 도구의 관례다.
//
// ★ 판정을 느슨하게 하지 않는다. 매니페스트가 있어도 format 이 우리 것이 아니면 거짓이다 —
// 남의 manifest.json 이 있는 디렉토리를 조용히 덮으면 그 순간 이 가드는 없는 것과 같다.
func IsOurOutput(dir string) bool {
	body, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		return false
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return false
	}
	return m.Format == FormatName
}
```

- [ ] **Step 5: `internal/ledger/read.go` 를 만든다**

```go
package ledger

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kweiza/flightdeck/internal/store"
)

// maxLineBytes 는 한 줄의 상한이다.
//
// ★ bufio.Scanner 의 기본 상한은 64KB 인데 지금 DB 의 최장 판단 본문이 74,227B 다 —
// 기본값을 쓰면 실제 데이터 한 행에서 곧바로 "token too long" 이 난다.
// cmd/fd/outbox.go 가 같은 이유로 8MB 를 준다.
const maxLineBytes = 8 << 20

// Read 는 dir 의 원장 파일 넷을 되읽는다.
func Read(dir string) (store.LedgerDump, Manifest, error) {
	var d store.LedgerDump
	var m Manifest

	body, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		return d, m, fmt.Errorf("매니페스트를 읽지 못했다(%q): %w", clip(dir, 200), err)
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return d, m, fmt.Errorf("매니페스트 해석 실패(%q): %w", clip(dir, 200), err)
	}
	if m.Format != FormatName {
		return d, m, fmt.Errorf("이 디렉토리는 판단 원장이 아니다(format=%q)", clip(m.Format, 64))
	}
	if m.FormatVersion != FormatVersion {
		return d, m, fmt.Errorf("원장 형식 버전이 %d 인데 이 바이너리는 %d 를 안다",
			m.FormatVersion, FormatVersion)
	}

	if d.Judgments, err = readLines[store.LedgerJudgment](dir, judgmentsFile); err != nil {
		return d, m, err
	}
	if d.Links, err = readLines[store.LedgerLink](dir, linksFile); err != nil {
		return d, m, err
	}
	if d.Snapshots, err = readLines[store.LedgerSnapshot](dir, snapshotsFile); err != nil {
		return d, m, err
	}
	return d, m, nil
}

// readLines 는 JSONL 한 파일을 T 슬라이스로 읽는다.
func readLines[T any](dir, name string) ([]T, error) {
	f, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		return nil, fmt.Errorf("원장 파일을 열지 못했다(%q): %w", clip(name, 64), err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)

	var out []T
	for line := 0; sc.Scan(); line++ {
		raw := bytes.TrimSpace(sc.Bytes())
		if len(raw) == 0 {
			continue
		}
		var v T
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("원장 행 해석 실패(%s 의 %d번째 줄): %w", clip(name, 64), line+1, err)
		}
		out = append(out, v)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("원장 파일 순회 실패(%q): %w", clip(name, 64), err)
	}
	return out, nil
}
```

- [ ] **Step 6: 시험이 통과하는지 본다**

```
cd plugins/flightdeck/server && go test ./internal/ledger/ -v
```

기대: 전부 PASS.

> `TestReadRoundTripsEncodedFiles` 가 `nil` 슬라이스와 빈 슬라이스 차이로 실패하면, `sampleDump()` 의 모든 슬라이스가 비어 있지 않은지 확인하라. `readLines` 는 행이 0개면 `nil` 을 낸다.

- [ ] **Step 7: 커밋**

```bash
git add internal/ledger/write.go internal/ledger/outguard.go internal/ledger/read.go internal/ledger/write_test.go
git commit -m "feat(flightdeck): 원장 원자 쓰기 · 자기 산출물 인식 · 되읽기

tmp+rename 이다 — os.WriteFile 직접 쓰기는 중간 실패 시 반쪽 파일을 남기고 판단 자산에는 안 맞는다.
IsOurOutput 은 두 번째 실행이 --force 없이 돌게 한다. 남의 manifest 는 알아보지 못한다.
Scanner 버퍼를 8MB 로 키운다 — 지금 DB 의 최장 본문이 74,227B 라 기본 64KB 상한에 곧바로 걸린다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 7: 왕복 무손실 통합 시험

사용자가 고른 등급을 증명하는 자리다. 앞의 어느 시험도 "DB → 파일 → DB" 를 끝까지 안 돈다.

**Files:**
- Create: `internal/ledger/roundtrip_test.go`

**Interfaces:**
- Consumes: `store.OpenLedger`·`store.ReadLedger`·`store.WriteLedger`·`Encode`·`Write`·`Read`

- [ ] **Step 1: 시험을 쓴다**

```go
package ledger_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/kweiza/flightdeck/internal/ledger"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// ★ 이 시험이 "무손실"의 유일한 증명이다.
// DB → 원장 읽기 → JSONL → 파일 → 되읽기 → 빈 DB → 다시 원장 읽기 가 원본과 같아야 한다.
// 이것이 없으면 이 저장소가 만든 것은 "복원해 본 적 없는 백업"이다.
func TestLedgerSurvivesFullRoundTrip(t *testing.T) {
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	srcPath := filepath.Join(t.TempDir(), "src.db")
	src, err := store.OpenWithLogger(srcPath, quiet)
	if err != nil {
		t.Fatalf("원본 DB 열기 실패: %v", err)
	}
	seedLedgerFixture(t, src)

	want, err := src.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("원장 읽기 실패: %v", err)
	}
	if len(want.Judgments) < 3 || len(want.Links) < 2 || len(want.Snapshots) < 1 {
		t.Fatalf("픽스처가 빈약하다 — 이 시험이 아무것도 안 본다: %d/%d/%d",
			len(want.Judgments), len(want.Links), len(want.Snapshots))
	}
	if err := src.Close(); err != nil {
		t.Fatalf("원본 닫기 실패: %v", err)
	}

	// 내보낸다.
	files, m, err := ledger.Encode(want, store.SchemaVersion, "2026-08-06T00:00:00.000000Z")
	if err != nil {
		t.Fatalf("Encode 실패: %v", err)
	}
	dir := filepath.Join(t.TempDir(), "out")
	if _, err := ledger.Write(files, dir); err != nil {
		t.Fatalf("Write 실패: %v", err)
	}

	// 되읽는다.
	got, gotM, err := ledger.Read(dir)
	if err != nil {
		t.Fatalf("Read 실패: %v", err)
	}
	if gotM.SchemaVersion != m.SchemaVersion {
		t.Errorf("스키마 버전이 달라졌다: %d → %d", m.SchemaVersion, gotM.SchemaVersion)
	}

	// ★ 완전히 빈 DB 에 되쓴다. seed 를 부르지 않는다 — Task 10 이 폐포를 닫았으므로
	//   machine·project·session 까지 원장이 다 갖고 와야 한다. 미리 심어 줘야 통과한다면
	//   그것은 폐포가 안 닫힌 것이고, 이 시험의 존재 이유가 바로 그것을 잡는 것이다.
	dstPath := filepath.Join(t.TempDir(), "dst.db")
	dst, err := store.OpenWithLogger(dstPath, quiet)
	if err != nil {
		t.Fatalf("복원 DB 열기 실패: %v", err)
	}
	defer dst.Close()

	if err := dst.WriteLedger(ctx, got); err != nil {
		t.Fatalf("빈 DB 되쓰기 실패 — 폐포가 안 닫혔다: %v", err)
	}

	final, err := dst.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("복원본 읽기 실패: %v", err)
	}
	if !reflect.DeepEqual(want, final) {
		t.Fatalf("왕복에서 원장이 달라졌다:\n원본 %+v\n복원 %+v", want, final)
	}
}

// 복원한 DB 에서 전문검색이 실제로 동작하는지 본다.
// judgment_fts 는 내보내지 않는데, 그것을 손실 0이라고 주장하는 근거가
// "AFTER INSERT 트리거가 다시 채운다" 하나뿐이다 — 그 주장을 여기서 실측한다.
func TestRestoredDBHasWorkingFullTextSearch(t *testing.T) {
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	src, err := store.OpenWithLogger(filepath.Join(t.TempDir(), "src.db"), quiet)
	if err != nil {
		t.Fatalf("원본 열기 실패: %v", err)
	}
	seedLedgerFixture(t, src)
	dump, err := src.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("원장 읽기 실패: %v", err)
	}
	src.Close()

	dst, err := store.OpenWithLogger(filepath.Join(t.TempDir(), "dst.db"), quiet)
	if err != nil {
		t.Fatalf("복원 DB 열기 실패: %v", err)
	}
	defer dst.Close()
	// 여기도 seed 를 안 부른다 — 폐포가 닫혔으므로 원장만으로 복원된다.
	if err := dst.WriteLedger(ctx, dump); err != nil {
		t.Fatalf("WriteLedger 실패: %v", err)
	}

	hits, err := dst.SearchJudgments(ctx, "p", "고유낱말", 10)
	if err != nil {
		t.Fatalf("복원본 검색 실패: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("복원 후 전문검색이 아무것도 못 찾는다 — judgment_fts 가 안 채워졌다")
	}
}

// seedLedgerRefs 는 원장 밖 표(project·session·machine)를 만든다.
func seedLedgerRefs(t *testing.T, s *store.Store) {
	t.Helper()
	ctx := context.Background()
	if err := s.UpsertProject(ctx, model.Project{ID: "p", Path: "/repo/p"}); err != nil {
		t.Fatalf("프로젝트 등록 실패: %v", err)
	}
	if err := s.UpsertMachine(ctx, model.Machine{ID: "m1", Hostname: "dev"}); err != nil {
		t.Fatalf("머신 등록 실패: %v", err)
	}
	if _, _, err := s.OpenSession(ctx, "p", "m1", "/w/cc1", "cc1", ""); err != nil {
		t.Fatalf("세션 등록 실패: %v", err)
	}
}

// seedLedgerFixture 는 왕복이 실제로 무언가를 보게 하는 데이터를 넣는다 —
// NULL 과 값, supersedes, 여러 target_kind, 전문검색용 고유 낱말.
func seedLedgerFixture(t *testing.T, s *store.Store) {
	t.Helper()
	ctx := context.Background()
	seedLedgerRefs(t, s)

	sess, _, err := s.OpenSession(ctx, "p", "m1", "/w/cc1", "cc1", "")
	if err != nil {
		t.Fatalf("세션 재개 실패: %v", err)
	}
	if err := s.AddItem(ctx, model.Item{
		Project: "p", ID: "i1", Title: "i1", Body: "본문", Paths: []string{"services/"},
	}); err != nil {
		t.Fatalf("항목 등록 실패: %v", err)
	}

	// ① 세션·제목이 있고 링크가 둘(종류가 다르다)
	first, err := s.AddJudgment(ctx, model.Judgment{
		Project: "p", SessionID: sess.ID, Kind: model.JudgmentDecision,
		Title: "결정", Body: "고유낱말 이 들어간 본문",
		Links: []model.JudgmentLink{
			{TargetKind: "item", TargetID: "i1"},
			{TargetKind: "commit", TargetID: "deadbeef"},
		},
	})
	if err != nil {
		t.Fatalf("판단① 저장 실패: %v", err)
	}

	// ② supersedes 가 걸린 정정
	if _, err := s.AddJudgment(ctx, model.Judgment{
		Project: "p", SessionID: sess.ID, Kind: model.JudgmentDecision,
		Title: "정정", Body: "앞 판단을 대체한다", Supersedes: first.ID,
	}); err != nil {
		t.Fatalf("판단② 저장 실패: %v", err)
	}

	// ③ project·session·title 이 전부 NULL 인 판단 — 포인터가 아니면 여기서 티가 난다
	if _, err := s.AddJudgment(ctx, model.Judgment{
		Kind: model.JudgmentDecision, Body: "좌표 없는 판단",
	}); err != nil {
		t.Fatalf("판단③ 저장 실패: %v", err)
	}

	// 스냅숏 둘 — evidence 가 있는 것과 없는 것
	if err := s.PutSnapshot(ctx, model.Snapshot{
		Project: "p", Key: "manual-key", Value: "12", Method: model.SnapshotManual,
		Evidence: "손으로 셌다", InputDigest: "abc",
	}); err != nil {
		t.Fatalf("스냅숏① 저장 실패: %v", err)
	}
	if err := s.PutSnapshot(ctx, model.Snapshot{
		Project: "p", Key: "cmd-key", Value: "7", Method: model.SnapshotCommand,
	}); err != nil {
		t.Fatalf("스냅숏② 저장 실패: %v", err)
	}
}
```

> 이 파일은 `package ledger_test`(외부 시험 패키지)다 — `store` 와 `ledger` 를 둘 다 import 해야 하고, `ledger` 안에 두면 `store` 가 `ledger` 를 안 쓰므로 순환은 없지만 내부 시험과 헬퍼 이름이 겹친다. `sampleDump`·`ptr` 은 여기서 안 보인다.

- [ ] **Step 2: 시험을 돌린다**

```
cd plugins/flightdeck/server && go test ./internal/ledger/ -run 'TestLedgerSurvives|TestRestoredDB' -v
```

기대: PASS 둘. 실패하면 `reflect.DeepEqual` 이 무엇이 다른지 출력하므로 그 필드를 좇아라. 흔한 원인 둘: `nil` 슬라이스 vs 빈 슬라이스, `*string` 이 아니라 값으로 받은 필드.

- [ ] **Step 3: 커밋**

```bash
git add internal/ledger/roundtrip_test.go
git commit -m "test(flightdeck): 왕복 무손실 — 이것이 등급의 유일한 증명이다

DB → 원장 → JSONL → 파일 → 되읽기 → 빈 DB → 다시 원장 이 원본과 같은지 본다.
그리고 복원한 DB 에서 전문검색이 실제로 도는지 잰다 — judgment_fts 를 안 내보내면서
'손실 0'이라 주장하는 근거가 AFTER INSERT 트리거 하나뿐이라, 그 주장을 실측한다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 8: `fd export --judgments` 배선

**Files:**
- Modify: `cmd/fd/migrate.go` (`runExport`)
- Modify: `cmd/fd/main.go` (`usage` 상수)
- Test: `cmd/fd/migrate_test.go` (순삽입)

**Interfaces:**
- Consumes: `store.OpenLedger`·`store.SchemaVersion`·`ledger.Encode`·`ledger.Write`·`ledger.Losses`·`ledger.IsOurOutput`·`legacy.InspectOutTarget`·`legacy.JudgeOutTarget`·`legacy.ForceAllows`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`cmd/fd/migrate_test.go` 끝에 붙인다:

```go
// 형식을 하나도 안 고르면 거절한다. 둘을 함께 골라도 거절한다 —
// 어느 쪽인지 알 수 없으므로 아무것도 하지 않는다.
func TestExportRequiresExactlyOneFormat(t *testing.T) {
	h := newHarness(t)
	h.closeStore()
	defer h.openStore()

	rc, out := h.run("", "export", "--out", filepath.Join(t.TempDir(), "o"), "--db", h.db)
	if rc == 0 {
		t.Fatalf("형식 없이 통과했다: %s", out)
	}
	mustContain(t, "형식 없음 거절", out, "--to-legacy", "--judgments")

	rc, out = h.run("", "export", "--to-legacy", "--judgments",
		"--out", filepath.Join(t.TempDir(), "o"), "--db", h.db)
	if rc == 0 {
		t.Fatalf("둘을 함께 줬는데 통과했다: %s", out)
	}
	mustContain(t, "형식 둘 거절", out, "함께")
}

// --judgments 는 DB 전량이다. --project 를 명시하면 거절한다 —
// 조용히 무시하면 백업이 반쪽인 걸 아무도 모른다.
func TestExportJudgmentsRejectsExplicitProject(t *testing.T) {
	h := newHarness(t)
	h.closeStore()
	defer h.openStore()

	rc, out := h.run("", "export", "--judgments", "--project", "p",
		"--out", filepath.Join(t.TempDir(), "o"), "--db", h.db)
	if rc == 0 {
		t.Fatalf("--project 를 줬는데 통과했다: %s", out)
	}
	mustContain(t, "--project 거절", out, "전량")
}

// 실제로 내보낸다. FD_PROJECT 는 환경에 있지만 거절 사유가 아니다.
func TestExportJudgmentsWritesFilesAndPrintsLosses(t *testing.T) {
	h := newHarness(t)

	// 판단을 REST 로 넣는다 — 실물 경로다. closeStore 뒤에는 REST 를 못 쓴다.
	rc, out := h.run("", "note", "--kind", "decision", "--body", "왕복 대상 판단")
	if rc != 0 {
		t.Fatalf("판단 등록 실패(rc=%d): %s", rc, out)
	}

	h.closeStore()
	defer h.openStore()

	outDir := filepath.Join(t.TempDir(), "ledger-out")
	rc, out = h.run("", "export", "--judgments", "--out", outDir, "--db", h.db)
	if rc != 0 {
		t.Fatalf("내보내기 실패(rc=%d): %s", rc, out)
	}
	mustContain(t, "내보내기 출력", out,
		"fd export --judgments", "DB 전량", "이 백업이 안 덮는 것", "아웃박스")

	for _, name := range []string{
		"judgments.jsonl", "judgment_links.jsonl", "snapshots.jsonl",
		"machines.jsonl", "projects.jsonl", "sessions.jsonl", "manifest.json",
	} {
		if _, err := os.Stat(filepath.Join(outDir, name)); err != nil {
			t.Errorf("%s 가 안 났다: %v", name, err)
		}
	}
}

// 같은 자리에 두 번 내보내도 --force 가 필요 없다 — 자기 산출물을 알아본다.
func TestExportJudgmentsRerunNeedsNoForce(t *testing.T) {
	h := newHarness(t)
	rc, out := h.run("", "note", "--kind", "decision", "--body", "판단")
	if rc != 0 {
		t.Fatalf("판단 등록 실패: %s", out)
	}
	h.closeStore()
	defer h.openStore()

	outDir := filepath.Join(t.TempDir(), "ledger-out")
	if rc, out := h.run("", "export", "--judgments", "--out", outDir, "--db", h.db); rc != 0 {
		t.Fatalf("첫 내보내기 실패: %s", out)
	}
	rc, out = h.run("", "export", "--judgments", "--out", outDir, "--db", h.db)
	if rc != 0 {
		t.Fatalf("두 번째 내보내기가 거절됐다 — 자기 산출물을 알아봐야 한다(rc=%d): %s", rc, out)
	}
}

// 내보내기는 DB 를 안 건드린다.
func TestExportJudgmentsLeavesDBUntouched(t *testing.T) {
	h := newHarness(t)
	rc, out := h.run("", "note", "--kind", "decision", "--body", "판단")
	if rc != 0 {
		t.Fatalf("판단 등록 실패: %s", out)
	}
	h.closeStore()
	defer h.openStore()

	before, err := os.Stat(h.db)
	if err != nil {
		t.Fatalf("stat 실패: %v", err)
	}
	if rc, out := h.run("", "export", "--judgments",
		"--out", filepath.Join(t.TempDir(), "o"), "--db", h.db); rc != 0 {
		t.Fatalf("내보내기 실패: %s", out)
	}
	after, err := os.Stat(h.db)
	if err != nil {
		t.Fatalf("stat 실패: %v", err)
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Errorf("DB 가 바뀌었다: %d/%v → %d/%v",
			before.Size(), before.ModTime(), after.Size(), after.ModTime())
	}
}
```

- [ ] **Step 2: 시험이 실패하는지 본다**

```
cd plugins/flightdeck/server && go test ./cmd/fd/ -run TestExport -v
```

기대: `TestExportRequiresExactlyOneFormat` 이 "지금 있는 되쓰기 형식은 --to-legacy 하나다" 만 받아 `--judgments` 문자열이 없어 실패. 나머지도 실패.

- [ ] **Step 3: `runExport` 를 고친다**

`cmd/fd/migrate.go` 에서 **`--to-legacy` 분기의 본문은 한 줄도 안 건드린다.** 플래그 정의에 하나를 더하고, 맨 앞 가드를 두 판정으로 바꾸고, 형식별로 갈라진다.

플래그 정의에 한 줄 추가:

```go
	toLegacy := fs.Bool("to-legacy", false, "옛 형식(.claude/{sessions,queue,handoffs})으로 되쓴다")
	toJudgments := fs.Bool("judgments", false, "판단 원장(judgment·judgment_link·snapshot)을 JSONL 로 낸다 — **DB 전량**이다")
```

`if !*toLegacy { … return 2 }` 블록 **전체**를 아래로 교체:

```go
	// ★ 형식은 정확히 하나여야 한다. 둘 다 없으면 무엇을 낼지 모르고, 둘 다 있으면
	//   어느 쪽인지 알 수 없으므로 아무것도 하지 않는다(runImport 의 --apply/--dry-run 과 같은 규율).
	switch {
	case !*toLegacy && !*toJudgments:
		fmt.Fprintln(out, "되쓰기 형식을 골라라 — --to-legacy(옛 형식) 또는 --judgments(판단 원장 JSONL)")
		return 2
	case *toLegacy && *toJudgments:
		fmt.Fprintln(out, "--to-legacy 와 --judgments 를 함께 줬다 — 어느 쪽인지 알 수 없으므로 아무것도 하지 않는다")
		return 2
	}
```

`--out` 가드는 그대로 둔다(두 형식 모두 필요하다).

출력 자리 판정 블록에서 `JudgeOutTarget` 호출을 아래로 바꾼다:

```go
	if v := legacy.JudgeOutTarget(outExists, inGit, hasLegacy, outEntries); !v.OK {
		// ★ 원장은 같은 자리에 계속 쓰는 것이 정상 사용이다. 자기 산출물이면 갱신으로 본다 —
		//   안 그러면 두 번째 실행부터 매번 --force 를 요구받는다.
		//   git 작업 트리는 여기서도 안 뚫린다(ForceAllows 가 그것을 허용하지 않는다).
		ours := *toJudgments && v.Code == "not-empty" && ledger.IsOurOutput(*outDir)
		if !ours && (!*force || !legacy.ForceAllows(v.Code)) {
			fmt.Fprintf(out, "되쓰기 거절 [%s]: %s\n", v.Code, v.Reason)
			return 2
		}
		if !ours {
			a.log.Warn("되쓰기 자리가 비어 있지 않은데 --force 로 진행한다",
				"route", clip(*outDir, 200), "reason", v.Code)
		}
	}
```

> `--judgments` 는 `has-legacy` 판정을 안 본다고 설계에 적었으나, `JudgeOutTarget` 은 `git-worktree` → `has-legacy` → `not-empty` 순으로 판정하므로 `.claude/` 가 있는 디렉토리에 원장을 쓰려 하면 `has-legacy` 가 뜬다. 그 경우 `--force` 가 필요하고, 그것은 **바람직한 동작**이다(원장을 레거시 트리에 쏟는 것은 실수일 가능성이 높다). 설계 문서의 "안 본다"는 문장은 이 구현으로 대체된다.

프로젝트 결정 블록을 형식별로 가른다. 기존 블록을 아래로 교체:

```go
	proj := strings.TrimSpace(*project)
	if *toJudgments {
		// ★ 판단 원장은 DB 전량이다. --project 를 **명시**하면 거절한다 —
		//   조용히 무시하면 백업이 반쪽인 걸 아무도 모른다.
		//   환경변수(FD_PROJECT)는 훅이 항상 심으므로 거절 조건에 넣지 않는다.
		explicit := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "project" {
				explicit = true
			}
		})
		if explicit {
			fmt.Fprintln(out, "판단 원장은 DB 전량이다 — --project 를 받지 않는다(프로젝트가 섞여 있어도 전부 나간다)")
			return 2
		}
	} else {
		if proj == "" {
			proj = envOr(a.env, "FD_PROJECT", "")
		}
		if proj == "" {
			fmt.Fprintln(out, "프로젝트 id 를 정하지 못했다 — --project 로 줘라")
			return 2
		}
	}
```

DB 열기 이후를 형식별로 가른다. 기존 `openDB` 블록과 `ExportLegacy` 호출 사이에 갈래를 넣는다:

```go
	if *toJudgments {
		return a.exportJudgments(ctx, *dbPath, *outDir, out)
	}

	st, path, err := openDB(a.env, a.log, *dbPath)
	// … 이하 기존 --to-legacy 경로 그대로 …
```

그리고 `runExport` 아래에 새 함수를 더한다:

```go
// exportJudgments 는 `fd export --judgments` 다.
//
// ★ openDB 를 쓰지 않는다. 그것은 store.OpenWithLogger 를 부르고, 그 안에서 반드시
// migrate 가 돈다 — 낡은 DB 를 만나면 원장을 뜨기 전에 스키마를 바꾼다.
// store.OpenLedger 는 ProbeMigration 으로 먼저 재고 이행이 필요하면 거절한다.
func (a *App) exportJudgments(ctx context.Context, dbFlag, outDir string, out io.Writer) int {
	path := strings.TrimSpace(dbFlag)
	if path == "" {
		home, _ := os.UserHomeDir()
		_, derr := os.Stat("/data")
		path = DefaultDBPath(a.env, home, derr == nil)
	}

	st, err := store.OpenLedger(ctx, path, a.log)
	if err != nil {
		a.log.Error("원장용 DB 를 열지 못했다", "db_path", path, "error", err.Error())
		fmt.Fprintf(out, "%s\n", err)
		return 1
	}
	defer func() {
		if cerr := st.Close(); cerr != nil {
			a.log.Error("DB 닫기 실패", "error", cerr.Error())
		}
	}()

	dump, err := st.ReadLedger(ctx)
	if err != nil {
		a.log.Error("원장 읽기 실패", "db_path", path, "error", err.Error())
		fmt.Fprintf(out, "원장 읽기 실패: %s\n", err)
		return 1
	}

	files, m, err := ledger.Encode(dump, store.SchemaVersion, nowStampString())
	if err != nil {
		a.log.Error("원장 인코딩 실패", "error", err.Error())
		fmt.Fprintf(out, "원장 인코딩 실패: %s\n", err)
		return 1
	}
	names, err := ledger.Write(files, outDir)
	if err != nil {
		a.log.Error("원장 쓰기 실패", "route", clip(outDir, 200), "error", err.Error())
		fmt.Fprintf(out, "원장 쓰기 실패: %s\n", err)
		return 1
	}

	losses := ledger.Losses()
	fmt.Fprintf(out, "fd export --judgments · DB 전량 → %s\n\n", outDir)
	// ★ 여섯 표 전부를 낸다. Task 10 이 FK 폐포를 닫으면서 machine·project·session 이 늘었다 —
	//   그 셋이 없으면 세션 걸린 판단(실측 85%)이 복원에서 전부 롤백된다.
	fmt.Fprintf(out, "  판단 %d · 링크 %d · 스냅숏 %d · 세션 %d · 프로젝트 %d · 머신 %d (파일 %d)\n",
		m.Counts.Judgments, m.Counts.Links, m.Counts.Snapshots,
		m.Counts.Sessions, m.Counts.Projects, m.Counts.Machines, len(names))
	fmt.Fprintf(out, "\n── 이 백업이 안 덮는 것 (%d건)\n", len(losses))
	for _, l := range losses {
		fmt.Fprintf(out, "  - %s\n", l)
	}
	a.log.Info("원장 내보내기 완료", "db_path", path, "mode", "judgments",
		"targets", m.Counts.Judgments, "count", len(names))
	return 0
}

// nowStampString 은 매니페스트에 실을 지금 시각이다. DB 의 저장 표기와 같은 폭 고정
// 마이크로초를 쓴다 — 산출물 안에서 시각 표기가 두 벌이 되면 안 된다.
func nowStampString() string {
	return time.Now().UTC().Truncate(time.Microsecond).Format("2006-01-02T15:04:05.000000Z")
}
```

import 에 `flag`·`time`·`github.com/kweiza/flightdeck/internal/ledger` 를 더한다(`os`·`io`·`context`·`store` 는 이미 있다).

- [ ] **Step 4: `usage` 에 한 줄을 더한다**

`cmd/fd/main.go` 의 `usage` 상수에서 export 줄 **바로 아래**에 넣는다. 설명이 시작하는 열을 위 줄과 눈으로 맞춰라(gofmt 가 안 잡아 준다).

```
  fd export --to-legacy --out <디렉토리>   옛 형식으로 되쓴다(완전 왕복은 아니다 — 출력이 목록을 낸다)
  fd export --judgments --out <디렉토리>   판단 원장을 JSONL 로 낸다. **DB 전량**이라 --project 를 안 받는다
```

- [ ] **Step 5: 시험을 돌린다**

```
cd plugins/flightdeck/server && go vet ./... && go test ./cmd/fd/ -run TestExport -v
```

기대: 새 시험 다섯 + 기존 `TestExportRequiresExplicitOutDir`·`TestExportPrintsRoundTripLosses` 전부 PASS.

> 기존 `TestExportRequiresExplicitOutDir` 은 `--to-legacy` 를 주고 `--out` 을 안 준다. 새 형식 가드가 `--to-legacy` 를 통과시키므로 그대로 통과해야 한다. 실패하면 가드 순서를 확인하라 — 형식 판정이 `--out` 판정보다 **먼저**다.

- [ ] **Step 6: 전 시험**

```
cd plugins/flightdeck/server && go vet ./... && go test ./...
```

- [ ] **Step 7: 커밋**

```bash
git add cmd/fd/migrate.go cmd/fd/main.go cmd/fd/migrate_test.go
git commit -m "feat(flightdeck): fd export --judgments — 판단 원장을 JSONL 로 낸다

새 서브명령이 아니라 기존 export 의 형식 하나다. §6 의 CLI 목록은 안 바뀌고
원칙 ②의 'MCP 도구를 안 늘렸다'가 그대로 성립한다.

--project 는 명시하면 거절한다(조용히 무시하면 백업이 반쪽인 걸 아무도 모른다).
거절은 fs.Visit 로 본 명시적 플래그에만 건다 — FD_PROJECT 는 훅이 항상 심으므로
환경변수까지 거절하면 정상 세션에서 이 명령이 아예 안 돈다.

openDB 대신 store.OpenLedger 를 쓴다 — 전자는 반드시 migrate 를 돌아 원장을 뜨기 전에
스키마를 바꾼다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 9: DESIGN.md — 문서가 실제를 말하게 한다

이것이 항목 본문의 ①이다. 순서상 마지막인 이유: 각주 표의 "있음/없음" 이 앞 태스크들의 결과와 맞아야 한다.

**Files:**
- Modify: `plugins/flightdeck/DESIGN.md`

- [ ] **Step 1: §7 실패·처방 표의 볼륨 손상 행에 각주 포인터를 단다**

인용문 `| 볼륨·DB 손상 | 6시간 \`VACUUM INTO\` + **매시간 판단 git 백업** |` 을 찾아 아래로 바꾼다. 마이그레이션 행이 쓴 서식(처방 문장 뒤에 `. ` 로 볼드 포인터를 잇는다)을 그대로 따른다.

```
| 볼륨·DB 손상 | 6시간 `VACUUM INTO` + **매시간 판단 git 백업**. **넷 중 하나만 구현돼 있다 — 아래 각주** |
```

- [ ] **Step 2: 마이그레이션 각주 바로 뒤에 볼륨 손상 각주를 넣는다**

마이그레이션 각주의 마지막 문단(인용문 `업그레이드 경로가 백업 경로를 지역 변수로 버려` 로 끝나는 문단) **다음**, 소제목 `### 서버는 자기 실행 파일 교체를 감지해 스스로 재기동한다` **앞**에 넣는다.

````markdown
**⚠ 볼륨 손상 행도 지금 구현과 어긋난다(2026-08-06 실측).** 이 행은 처방 넷을 약속하는데
그중 하나만 있다. 마이그레이션 행과 같은 이유로 여기 적는다 — 이 문서를 근거로
"백업이 돌고 있다"고 믿는 것이 지금의 진짜 위험이다.

| 처방 | 상태 | 실제 |
|---|---|---|
| 판단을 DB 밖으로 내보내기 | 있음 | `fd export --judgments` 가 FK 폐포 여섯 표(`judgment`·`judgment_link`·`snapshot`·`session`·`project`·`machine`) 전량을 JSONL 로 낸다. 손으로 부른다 |
| 매시간 자동 실행 | **없음** | 주기 작업 자리가 없다. 티커는 SSE 하트비트와 `selfwatch` 둘뿐이고, 컨테이너에 cron 이 없으며 `compose.yaml` 은 서비스가 하나다 |
| DB 와 다른 볼륨 | **없음** | `compose.yaml` 이 "DB 와 백업을 같은 볼륨에 두는 것은 Tier A 의 한계다 — 백업 잡이 생기는 시점에 별도 볼륨으로 가른다"로 접어 뒀다 |
| 6시간 `VACUUM INTO` | **없음** | `VACUUM INTO` 는 있지만 마이그레이션 직전 1회다(`Store.backup`). 정기 작업이 아니다 |

**실측 (2026-08-06).** `judgment` 1,157행 · `judgment_link` 1,734행 · `snapshot` 12행 ·
`session` 302행 · `project` 8행 · `machine` 8행이
`fd.db` 하나에 있다. 이틀 전 관측은 330행이었다 — **사흘에 3배**다.
`~/.flightdeck` 에 `journal.git` 도 `logs/` 도 없고, `.bak` 계열 4개 38MB 가 정리 코드 없이
쌓여 있으며 그중 하나(`fd.db.before-purge-aaron`)는 `BackupSuffix` 가 만들 수 없는 이름이다 —
**사람이 손으로 만들었다.**

**이 내보내기가 안 덮는 것.** 아웃박스에 갇힌 판단은 안 잡힌다(이 머신에 실제로 1건).
무손실 복원의 FK 폐포는 여섯 표이고 **원장은 그것을 통째로 담는다** — 빈 DB 에 되쓰면
미리 심어 둘 것이 하나도 없다. 대신 폐포 **밖** 표(`item`·`job`·`counter`·`event`·`landing_queue` 등)는
안 담으므로, 복원된 DB 에서 `judgment_link` 가 가리키는 항목은 없다(링크 자체는 복원된다 —
`target_id` 는 FK 가 아니라 CHECK 뿐이다). 코드가 `ledger.Losses()` 로 이 목록을 열거하고
시험이 그것을 문다.

**`grep -rn "journal" server/ --include=*.go` 를 근거로 쓰지 마라.** 그 grep 은 지금 9건을
내는데 전부 SQLite `journal_mode(WAL)` 이다. 판단 저널과 무관하다.

**이 판단이 뒤집히는 조건은 하나다 — 손으로 부르는 것을 아무도 안 부르는 순간.**
그때 주기 실행(호스트 cron · `serve` 안 티커 · compose 두 번째 서비스 중 하나)과
별도 볼륨을 세운다. 셋 다 지금 코드에 선례가 0건이라 이번에 함께 하지 않았다(§11 —
"전부 아니면 무로 제시하면 1인 운영자가 착수하지 못한다").

**그 조건은 시험이 못 지킨다** — "매시간 도는가"는 코드 안에서 관측되지 않는다.
그래서 만료 조건의 보관소는 fd 큐다: 후속 항목이 이 각주를 대체할 때까지 이 표가 정본이다.
````

> 후속 항목 id 는 Task 9 커밋 시점에 `fd finish` 의 `followups` 로 등록된 실제 id 로 바꿔라. 등록 전이라면 이 문장을 그대로 두고, 등록 후 한 줄 커밋으로 id 를 채운다.

- [ ] **Step 3: §7 의 판단 백업 문단을 고친다**

인용문 `**판단 백업이 유일하게 재생성 불가한 자산이다.**` 로 시작하는 두 줄 문단을 찾아, 그 뒤에 문단 하나를 **순삽입**한다(기존 두 줄은 안 지운다 — 그것은 여전히 목표를 말한다).

```markdown
**다만 그 문장의 대상은 좁다.** `judgment`+`snapshot` 만으로는 복원이 안 된다 —
`judgment.project → project`, `judgment.session_id → session`, `session → machine`,
`snapshot.project → project`, `judgment.supersedes → judgment` 가 전부 FK 이고
`foreign_keys=1` 이 항상 걸린다. 그리고 **`session.id` 는 서버 발급 ULID 라 새 DB 에서 재현할 수
없다** — `project`·`machine` 은 사람과 클라이언트가 정한 이름이라 다시 부를 수 있지만 세션은 아니고,
판단의 85%가 그것을 가리킨다. 그래서 구현된 `fd export --judgments` 는 **폐포 여섯 표를 통째로 담는다.**
그리고 형식은 마크다운이 아니라 **JSONL 하나**다.
```

- [ ] **Step 4: §9 에 한 줄 더한다**

인용문 `\`fd export --to-legacy\` 가 옛 형식으로 되쓴다.` 를 찾아 그 뒤에 이어 붙인다.

```markdown
`fd export --judgments` 는 판단 원장을 JSONL 로 낸다(§7 의 백업 축이다 — 이관과 무관하다).
```

- [ ] **Step 5: 문서가 코드와 맞는지 확인한다**

```
cd plugins/flightdeck/server && go test ./... && ./bin/fd export --judgments --out /tmp/ledger-check --db ~/.flightdeck/fd.db
```

> 이 명령은 실제 DB 를 읽는다. `--out` 은 **레포 밖**이어야 한다 — `insideGitWorktree` 가 조상을 끝까지 올라가므로 저장소 안 어느 경로도 거절된다. 출력의 건수가 각주에 적은 984/1,413/12 와 다르면 **각주의 수를 실제로 맞춰라**(그 사이 늘었을 수 있다).

- [ ] **Step 6: 커밋**

```bash
git add plugins/flightdeck/DESIGN.md
git commit -m "docs(flightdeck): §7 볼륨 손상 행의 실제를 적는다 — 처방 넷 중 하나만 있다

마이그레이션 행이 받은 것과 같은 ⚠ 각주 형식이다. 그 행은 각주를 받았고 이 행은 안 받아서,
같은 절 안에서 한 행은 검산됐고 다른 행은 안 된 상태로 있었다.

판단 백업 문단도 고친다 — judgment+snapshot 만으로는 복원이 안 된다. 두 표에 FK 가 걸려 있고
foreign_keys=1 이 항상 켜져 있어, 무손실 폐포는 machine·project·session 까지 여섯이다.

만료 조건은 시험이 못 지킨다('매시간 도는가'는 코드 안에서 관측되지 않는다). 보관소는 fd 큐다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 10: FK 폐포를 닫는다 — `session`·`machine`·`project` 를 원장에 더한다

> **실행 순서: 이 태스크는 Task 7 보다 먼저 돈다.** Task 7 이 이 결함을 찾았고(왕복이 FK 로 죽었다),
> Task 7 의 시험은 이 태스크가 끝나야 통과한다. 순서는 1→2→3→4→5→6→**10**→7→8→9 다.

Task 7 의 통합 시험이 계획 결함을 찾았다. 스펙의 결정 ⑫("`machine`·`project`·`session` 이 있는 DB 를
복원 전제로 삼는다")가 셋을 같은 것으로 취급했는데 **셋이 다르다**:

| 가리키는 것 | 값의 성질 | 새 DB 에서 같은 값을 만들 수 있나 |
|---|---|---|
| `project` | 사람이 정한 **이름** | 가능 — 그대로 다시 등록하면 된다 |
| `machine_id` | 클라이언트가 만들어 로컬에 보관하는 안정 id | 가능 |
| `session_id` | **서버 발급 ULID**(`NewID()`) | **불가능** — 같은 3중키로 다시 열어도 새 값이 나온다 |

실측: `judgment` 1,141행 중 **973행(85%)** 이 `session_id` 를 갖는다. 복원은 한 트랜잭션이라
973행이 FK 로 실패하면 **나머지 168행도 함께 롤백된다** — 부분 복원이 아니라 전부 실패다.

폐포를 닫는 비용은 `project` 8 + `machine` 8 + `session` 300 = **316행**. `machine` 과 `project` 는
FK 가 하나도 없는 leaf 라 여기서 닫힌다.

**Files:**
- Modify: `internal/store/backup.go` (DTO 3개 · `LedgerDump` 필드 3개 · 조회 3개 · 되쓰기 3개)
- Modify: `internal/store/project.go` (`projectCols`·`machineCols` 상수를 뽑고 기존 SELECT 가 쓰게)
- Modify: `internal/ledger/export.go` (파일 3개 · `Counts` 3필드 · `FormatVersion` 2)
- Modify: `internal/ledger/read.go` (`readLines` 3개)
- Test: `internal/store/backup_test.go` · `internal/ledger/export_test.go` · `internal/ledger/write_test.go`

**Interfaces:**
- Consumes: `sessionCols`(`internal/store/session.go:115` 에 **이미 있다** — 정확히 필요한 9컬럼이다), `dbtx`, `ptrOf`, `clip`
- Produces:
  - `store.LedgerMachine{ID, Hostname, FirstSeen, LastSeen string}`
  - `store.LedgerProject{ID, Path string; RemoteURL *string; DefaultBranch string; Config, ConfigFromSHA *string; CreatedAt string}`
  - `store.LedgerSession{ID, Project, MachineID, Worktree, CCSessionID string; Label *string; State string; BlockedWhy *string; OpenedAt string}`
  - `store.LedgerDump` 에 `Machines`·`Projects`·`Sessions` 필드
  - `const projectCols`·`const machineCols` (`internal/store/project.go`)

- [ ] **Step 1: `projectCols`·`machineCols` 를 뽑고 기존 호출부가 쓰게 한다**

`internal/store/project.go` 에 컬럼 상수가 없어서 SELECT 목록을 여러 자리가 손으로 적는다(`:62`·`:91`·`:161`).
Task 1 에서 리뷰가 잡았던 것과 같은 결함이니 **여기서 미리 없앤다** — 상수만 만들고 기존 SELECT 를 안 고치면
"이름만 선례"가 되어 같은 지적을 다시 받는다.

`internal/store/project.go` 에 추가하고, 그 파일의 기존 `SELECT` 들이 이 상수를 쓰게 고친다:

```go
// projectCols 는 프로젝트 조회의 컬럼 목록이다.
// judgmentCols·sessionCols 와 같은 이유로 상수다 — 목록을 손으로 다시 적으면
// 순서가 어긋나는 순간 Scan 이 조용히 엉뚱한 값을 채운다(전부 문자열이라 타입 오류도 안 난다).
const projectCols = `id, path, remote_url, default_branch, config, config_from_sha, created_at`

// machineCols 는 머신 조회의 컬럼 목록이다.
const machineCols = `id, hostname, first_seen, last_seen`
```

기존 SELECT 를 `SELECT `+projectCols+` FROM project …` / `SELECT `+machineCols+` FROM machine …` 형태로
바꾼다. **Scan 인자 순서는 건드리지 마라** — 컬럼 순서가 같으므로 그대로 동작한다.

- [ ] **Step 2: store 계층 시험을 쓴다 (실패해야 한다)**

`internal/store/backup_test.go` 끝에 붙인다.

```go
// 원장은 FK 폐포를 통째로 담는다. session.id 는 서버 발급 ULID 라 새 DB 에서 재현할 수 없고,
// 그래서 세션을 안 담으면 세션 걸린 판단(실측 85%)이 FK 위반으로 전부 롤백된다.
func TestReadLedgerCoversTheFullFKClosure(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seed(t, s, "p1")
	sess := mustSession(t, s, "p1", "cc-closure")

	if _, err := s.AddJudgment(ctx, model.Judgment{
		Project: "p1", SessionID: sess.ID, Kind: model.JudgmentDecision, Body: "세션 걸린 판단",
	}); err != nil {
		t.Fatalf("판단 저장 실패: %v", err)
	}

	d, err := s.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("ReadLedger 실패: %v", err)
	}
	if len(d.Machines) == 0 {
		t.Error("machine 이 원장에 없다 — session.machine_id 가 가리킬 대상이 사라진다")
	}
	if len(d.Projects) == 0 {
		t.Error("project 가 원장에 없다")
	}
	if len(d.Sessions) == 0 {
		t.Fatal("session 이 원장에 없다 — 판단의 85%가 이것을 가리킨다")
	}
	var found bool
	for _, x := range d.Sessions {
		if x.ID == sess.ID {
			found = true
			if x.Project != "p1" || x.MachineID != "m1" {
				t.Errorf("세션 필드가 틀리다: %+v", x)
			}
		}
	}
	if !found {
		t.Errorf("판단이 가리키는 세션 %q 가 원장에 없다", sess.ID)
	}
}

// 빈 DB 에 되쓰면 폐포가 통째로 복원된다 — 미리 심어 둘 것이 하나도 없어야 한다.
// 이것이 "무손실" 등급의 실제 의미다.
func TestWriteLedgerRestoresIntoATrulyEmptyDB(t *testing.T) {
	src := newStore(t)
	ctx := context.Background()
	seed(t, src, "p1")
	sess := mustSession(t, src, "p1", "cc-restore")

	if _, err := src.AddJudgment(ctx, model.Judgment{
		Project: "p1", SessionID: sess.ID, Kind: model.JudgmentAsk,
		Title: "제목", Body: "세션 걸린 판단",
	}); err != nil {
		t.Fatalf("판단 저장 실패: %v", err)
	}
	if err := src.PutSnapshot(ctx, model.Snapshot{
		Project: "p1", Key: "k", Value: "1", Method: model.SnapshotCommand,
	}); err != nil {
		t.Fatalf("스냅숏 저장 실패: %v", err)
	}

	want, err := src.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("원본 읽기 실패: %v", err)
	}

	// ★ seed 를 부르지 않는다. 원장이 project·machine·session 을 다 갖고 와야 한다.
	dst := newStore(t)
	if err := dst.WriteLedger(ctx, want); err != nil {
		t.Fatalf("빈 DB 되쓰기 실패 — 폐포가 안 닫혔다: %v", err)
	}
	got, err := dst.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("복원본 읽기 실패: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("왕복에서 원장이 달라졌다:\n원본 %+v\n복원 %+v", want, got)
	}
}

// state='blocked' 세션은 CHECK(state<>'blocked' OR blocked_why 가 비지 않음) 를 통과해야 한다.
// 지금 실 DB 에 blocked 세션이 0건이라 이 축은 시험이 만들어야만 검증된다.
func TestWriteLedgerRestoresBlockedSession(t *testing.T) {
	src := newStore(t)
	ctx := context.Background()
	seed(t, src, "p1")
	sess := mustSession(t, src, "p1", "cc-blocked")
	if err := src.SetSessionState(ctx, sess.ID, model.SessionBlocked, "왜 막혔는지"); err != nil {
		t.Fatalf("세션을 막힘으로 못 바꿨다: %v", err)
	}

	want, err := src.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("원본 읽기 실패: %v", err)
	}
	var blocked bool
	for _, x := range want.Sessions {
		if x.State == "blocked" {
			blocked = true
			if x.BlockedWhy == nil || *x.BlockedWhy == "" {
				t.Fatalf("blocked 세션인데 사유가 비었다: %+v", x)
			}
		}
	}
	if !blocked {
		t.Fatal("전제가 깨졌다 — blocked 세션이 원장에 없다")
	}

	dst := newStore(t)
	if err := dst.WriteLedger(ctx, want); err != nil {
		t.Fatalf("blocked 세션 되쓰기 실패(CHECK 위반?): %v", err)
	}
}
```

> `mustSession` 은 `internal/store/store_test.go` 에 이미 있다(`mustSession(t, s, project, cc) model.Session`).
> 세션 상태를 바꾸는 메서드의 정확한 이름·시그니처는 `grep -n "func (s \*Store).*Session" internal/store/session.go`
> 로 확인해 맞춰라 — 위 코드의 `SetSessionState` 는 추정이다. 이름이 다르면 실제 것을 쓰고,
> 상태를 바꾸는 공개 경로가 없으면 `s.DB()` 대신 **`store` 패키지 안이라 가능한 raw UPDATE** 로
> 픽스처를 만들되 그 사실을 주석에 적어라.

- [ ] **Step 3: 시험이 실패하는지 본다**

```
cd plugins/flightdeck/server && go test ./internal/store/ -run 'ClosureFull|TrulyEmptyDB|BlockedSession' -v
```

기대: 컴파일 실패(`d.Machines` 등이 없다) 또는 `TestWriteLedgerRestoresIntoATrulyEmptyDB` 가 FK 위반으로 실패.

- [ ] **Step 4: DTO 셋과 `LedgerDump` 필드를 더한다**

`internal/store/backup.go` 의 `LedgerSnapshot` 뒤, `LedgerDump` 앞에 순삽입한다.

```go
// LedgerMachine 은 machine 표 한 행의 원문이다. NULL 가능 컬럼이 없다.
type LedgerMachine struct {
	ID        string `json:"id"`
	Hostname  string `json:"hostname"`
	FirstSeen string `json:"first_seen"`
	LastSeen  string `json:"last_seen"`
}

// LedgerProject 는 project 표 한 행의 원문이다.
type LedgerProject struct {
	ID            string  `json:"id"`
	Path          string  `json:"path"`
	RemoteURL     *string `json:"remote_url"`
	DefaultBranch string  `json:"default_branch"`
	Config        *string `json:"config"`
	ConfigFromSHA *string `json:"config_from_sha"`
	CreatedAt     string  `json:"created_at"`
}

// LedgerSession 은 session 표 한 행의 원문이다.
//
// ★ 이 표가 원장에 있는 이유. session.id 는 서버 발급 ULID 라 같은 3중키로 다시 열어도
// 새 값이 나온다 — project·machine 처럼 "이름을 다시 부르면 같은 것"이 아니다.
// 판단의 85%(실측 973/1141)가 session_id 를 갖고, 복원이 한 트랜잭션이라 그 FK 가 하나만
// 깨져도 판단 전체가 롤백된다.
type LedgerSession struct {
	ID          string  `json:"id"`
	Project     string  `json:"project"`
	MachineID   string  `json:"machine_id"`
	Worktree    string  `json:"worktree"`
	CCSessionID string  `json:"cc_session_id"`
	Label       *string `json:"label"`
	State       string  `json:"state"`
	BlockedWhy  *string `json:"blocked_why"`
	OpenedAt    string  `json:"opened_at"`
}
```

`LedgerDump` 를 고친다. **필드 순서가 FK 의존 순서다** — 읽는 사람이 복원 순서를 바로 안다:

```go
// LedgerDump 는 한 순간의 FK 폐포 전량이다.
//
// 여섯 표다. 앞 셋(machine·project·session)이 뒤 셋의 FK 대상이고, machine·project 는
// 아무것도 참조하지 않는 leaf 라 폐포가 여기서 닫힌다.
type LedgerDump struct {
	Machines  []LedgerMachine
	Projects  []LedgerProject
	Sessions  []LedgerSession
	Judgments []LedgerJudgment
	Links     []LedgerLink
	Snapshots []LedgerSnapshot
}
```

- [ ] **Step 5: 조회 셋을 더하고 `ReadLedger` 에 잇는다**

`ReadLedger` 본문의 `d.Judgments` 읽기 **앞**에 셋을 넣는다(같은 `tx` 를 쓴다):

```go
	if d.Machines, err = readLedgerMachines(ctx, tx); err != nil {
		return d, err
	}
	if d.Projects, err = readLedgerProjects(ctx, tx); err != nil {
		return d, err
	}
	if d.Sessions, err = readLedgerSessions(ctx, tx); err != nil {
		return d, err
	}
```

그리고 `readLedgerSnapshots` 뒤에 함수 셋을 순삽입한다:

```go
// readLedgerMachines 는 머신 전량을 id 순으로 읽는다.
func readLedgerMachines(ctx context.Context, q dbtx) ([]LedgerMachine, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+machineCols+` FROM machine ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("원장 머신 조회 실패: %w", err)
	}
	defer rows.Close()

	var out []LedgerMachine
	for rows.Next() {
		var m LedgerMachine
		if err := rows.Scan(&m.ID, &m.Hostname, &m.FirstSeen, &m.LastSeen); err != nil {
			return nil, fmt.Errorf("원장 머신 행 해석 실패: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("원장 머신 순회 실패: %w", err)
	}
	return out, nil
}

// readLedgerProjects 는 프로젝트 전량을 id 순으로 읽는다.
func readLedgerProjects(ctx context.Context, q dbtx) ([]LedgerProject, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+projectCols+` FROM project ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("원장 프로젝트 조회 실패: %w", err)
	}
	defer rows.Close()

	var out []LedgerProject
	for rows.Next() {
		var p LedgerProject
		var remote, config, fromSHA sql.NullString
		if err := rows.Scan(&p.ID, &p.Path, &remote, &p.DefaultBranch,
			&config, &fromSHA, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("원장 프로젝트 행 해석 실패: %w", err)
		}
		p.RemoteURL, p.Config, p.ConfigFromSHA = ptrOf(remote), ptrOf(config), ptrOf(fromSHA)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("원장 프로젝트 순회 실패: %w", err)
	}
	return out, nil
}

// readLedgerSessions 는 세션 전량을 id 순으로 읽는다.
func readLedgerSessions(ctx context.Context, q dbtx) ([]LedgerSession, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+sessionCols+` FROM session ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("원장 세션 조회 실패: %w", err)
	}
	defer rows.Close()

	var out []LedgerSession
	for rows.Next() {
		var x LedgerSession
		var label, blockedWhy sql.NullString
		if err := rows.Scan(&x.ID, &x.Project, &x.MachineID, &x.Worktree, &x.CCSessionID,
			&label, &x.State, &blockedWhy, &x.OpenedAt); err != nil {
			return nil, fmt.Errorf("원장 세션 행 해석 실패: %w", err)
		}
		x.Label, x.BlockedWhy = ptrOf(label), ptrOf(blockedWhy)
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("원장 세션 순회 실패: %w", err)
	}
	return out, nil
}
```

- [ ] **Step 6: `WriteLedger` 에 되쓰기 셋을 더한다**

`WriteLedger` 안, `PRAGMA defer_foreign_keys` 직후·판단 루프 **앞**에 넣는다. `defer_foreign_keys` 가
켜져 있어 순서는 무관하지만, FK 의존 순서대로 두면 읽는 사람이 폐포를 바로 안다.

```go
		for _, m := range d.Machines {
			if _, err := t.tx.ExecContext(t.ctx, `
				INSERT INTO machine(id, hostname, first_seen, last_seen)
				VALUES (?, ?, ?, ?)`,
				m.ID, m.Hostname, m.FirstSeen, m.LastSeen); err != nil {
				return fmt.Errorf("원장 머신 되쓰기 실패(id=%q): %w", clip(m.ID, 64), err)
			}
		}
		for _, p := range d.Projects {
			if _, err := t.tx.ExecContext(t.ctx, `
				INSERT INTO project(id, path, remote_url, default_branch, config, config_from_sha, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?)`,
				p.ID, p.Path, p.RemoteURL, p.DefaultBranch,
				p.Config, p.ConfigFromSHA, p.CreatedAt); err != nil {
				return fmt.Errorf("원장 프로젝트 되쓰기 실패(id=%q): %w", clip(p.ID, 64), err)
			}
		}
		for _, x := range d.Sessions {
			if _, err := t.tx.ExecContext(t.ctx, `
				INSERT INTO session(id, project, machine_id, worktree, cc_session_id,
				                    label, state, blocked_why, opened_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				x.ID, x.Project, x.MachineID, x.Worktree, x.CCSessionID,
				x.Label, x.State, x.BlockedWhy, x.OpenedAt); err != nil {
				return fmt.Errorf("원장 세션 되쓰기 실패(id=%q project=%q): %w",
					clip(x.ID, 64), clip(x.Project, 64), err)
			}
		}
```

그리고 `WriteLedger` 의 doc 주석에서 **거짓이 된 문장을 고친다**:

```go
// ★ project·session·machine 은 원장 밖이다. 이 셋이 이미 있는 DB 를 전제한다.
// 없으면 FK 위반으로 트랜잭션 전체가 거절되고, 그 거절이 곧 "전제가 안 맞다"는 신호다.
```

를 아래로 바꾼다:

```go
// ★ 폐포를 통째로 되쓴다. machine·project·session 이 판단보다 먼저 들어가고, 그 셋은
// 아무것도 참조하지 않거나 서로만 참조하므로 여기서 닫힌다. 빈 DB 에 되쓰면 미리 심어 둘
// 것이 하나도 없다 — 그것이 이 함수가 증명하는 "무손실"의 실제 의미다.
```

- [ ] **Step 7: `internal/ledger` 에 파일 셋을 더하고 형식 버전을 올린다**

`internal/ledger/export.go` 의 상수 블록:

```go
	// FormatVersion 은 이 산출물 배치의 버전이다. 파일 이름이나 줄 구조가 바뀌면 올린다.
	//
	// 2: FK 폐포를 닫으며 machines·projects·sessions 셋이 늘었다. 버전 1 산출물로 복원하면
	//    세션 걸린 판단(실측 85%)이 FK 위반으로 전부 롤백되므로, Read 가 그것을 거절하는 것이 맞다.
	FormatVersion = 2

	judgmentsFile = "judgments.jsonl"
	linksFile     = "judgment_links.jsonl"
	snapshotsFile = "snapshots.jsonl"
	machinesFile  = "machines.jsonl"
	projectsFile  = "projects.jsonl"
	sessionsFile  = "sessions.jsonl"
```

`Counts` 를 확장한다(필드 순서는 `LedgerDump` 와 같게):

```go
// Counts 는 내보낸 행 수다.
type Counts struct {
	Machines  int `json:"machines"`
	Projects  int `json:"projects"`
	Sessions  int `json:"sessions"`
	Judgments int `json:"judgments"`
	Links     int `json:"judgment_links"`
	Snapshots int `json:"snapshots"`
}
```

`Encode` 의 `Counts` 리터럴에 셋을 더하고, 인코딩 셋을 더하고, 반환 맵에 셋을 더한다:

```go
	machines, err := encodeLines(len(d.Machines), func(i int) any { return d.Machines[i] })
	if err != nil {
		return nil, m, fmt.Errorf("머신 인코딩 실패: %w", err)
	}
	projects, err := encodeLines(len(d.Projects), func(i int) any { return d.Projects[i] })
	if err != nil {
		return nil, m, fmt.Errorf("프로젝트 인코딩 실패: %w", err)
	}
	sessions, err := encodeLines(len(d.Sessions), func(i int) any { return d.Sessions[i] })
	if err != nil {
		return nil, m, fmt.Errorf("세션 인코딩 실패: %w", err)
	}
```

반환 맵에 `machinesFile: machines`·`projectsFile: projects`·`sessionsFile: sessions` 를 더한다.

`internal/ledger/read.go` 의 `Read` 에서 판단 읽기 **앞**에 셋을 넣는다:

```go
	if d.Machines, err = readLines[store.LedgerMachine](dir, machinesFile); err != nil {
		return d, m, err
	}
	if d.Projects, err = readLines[store.LedgerProject](dir, projectsFile); err != nil {
		return d, m, err
	}
	if d.Sessions, err = readLines[store.LedgerSession](dir, sessionsFile); err != nil {
		return d, m, err
	}
```

- [ ] **Step 8: 손실 목록을 갱신한다 — 한 항목이 거짓이 됐다**

`internal/ledger/losses.go` 의 마지막 항목이 이제 거짓이다:

```go
		"`project`·`session`·`machine` 표 — 무손실 복원의 FK 폐포에 필요하지만 원장 밖이다. " +
			"되읽기는 이 셋이 이미 있는 DB 를 전제한다",
```

**지우고** 그 자리에 지금 참인 것을 적는다 — 폐포 밖 표들이 새 손실이다:

```go
		"폐포 밖 표 전부(`item`·`job`·`counter`·`event`·`landing_row` 등) — 원장은 판단의 FK 폐포 " +
			"여섯 표만 담는다. `judgment_link.target_id` 는 FK 가 아니라(CHECK 만) 링크 자체는 " +
			"복원되지만, 그것이 가리키는 항목은 복원된 DB 에 없다",
```

`Losses()` 는 순수 함수이고 명령이 그대로 출력하므로, **거짓 항목을 남기면 사용자가 화면에서 거짓을 읽는다.**
그리고 `TestLossesNamesTheKnownGaps` 가 기대하는 키워드 목록도 실제에 맞게 고쳐라 — 지금은
`"project"`·`"session"`·`"machine"` 을 요구하는데 그 축이 손실이 아니게 됐다.

- [ ] **Step 9: `ledger` 시험을 갱신한다**

`export_test.go` 의 `sampleDump()` 에 세 표를 채운다 — 안 채우면 새 인코딩 경로가 한 번도 안 돈다.
`machine` 은 NULL 가능 컬럼이 없고, `project` 는 셋(`remote_url`·`config`·`config_from_sha`),
`session` 은 둘(`label`·`blocked_why`)이 NULL 가능하니 **각 표에 NULL 인 행과 값이 있는 행을 섞어라.**

파일 개수를 단정하는 기존 시험(`TestEncodeProducesFourFilesAndLinePerRow`)이 **넷을 기대하므로 깨진다.**
이름과 기대값을 일곱(JSONL 여섯 + manifest)으로 고쳐라. 시험 이름도 실제를 말하게 바꾼다.

`write_test.go` 의 `TestWriteLeavesNoTempFiles` 도 `len(ents) != 4` 를 일곱으로 고쳐야 한다.

- [ ] **Step 10: 전 시험을 돌린다**

```
cd plugins/flightdeck/server && go vet ./... && go test ./...
```

기대: 전부 PASS. `internal/ledger` 의 왕복 시험과 `internal/store` 의 새 시험 셋 포함.

- [ ] **Step 11: 커밋**

```bash
git add internal/store/backup.go internal/store/backup_test.go internal/store/project.go \
        internal/ledger/export.go internal/ledger/read.go \
        internal/ledger/export_test.go internal/ledger/write_test.go
git commit -m "feat(flightdeck): FK 폐포를 닫는다 — session·machine·project 를 원장에 더한다

Task 7 의 왕복 시험이 찾은 결함이다. 스펙은 이 셋이 '있는 DB 를 전제한다'고 적었는데
셋이 다르다: project 와 machine 은 이름이라 다시 부를 수 있지만 session.id 는 서버 발급
ULID 라 같은 3중키로 다시 열어도 새 값이 나온다.

실측: 판단 1,141행 중 973행(85%)이 session_id 를 갖는다. 복원은 한 트랜잭션이라
그 FK 가 깨지면 나머지 168행도 함께 롤백된다 — 부분 복원이 아니라 전부 실패다.

폐포 비용은 316행(project 8 + machine 8 + session 300)이고, 두 leaf 표가
아무것도 참조하지 않아 여기서 닫힌다.

형식 버전을 2로 올린다. 버전 1 산출물로 복원하면 세션 걸린 판단이 전부 롤백되므로
Read 가 그것을 거절하는 것이 맞다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## 최종 확인

- [ ] `cd plugins/flightdeck/server && go vet ./... && go test ./...` 가 전부 초록
- [ ] `git log --oneline main..HEAD` 가 커밋 9개(Task 1~9)
- [ ] `note(kind: "verified")` 로 실측 결과를 남긴다 — 실제 DB 에서 몇 건이 나왔고, 왕복 시험이 무엇을 증명했는지
- [ ] `fd finish` 의 `followups` 에 아래 넷을 싣는다:
  - 주기 실행과 별도 볼륨(③) — `compose.yaml` 이 걸어 둔 "백업 잡이 생기는 시점"
  - 아웃박스 합류 — `rejected.jsonl` 에 갇힌 판단이 실재한다
  - `fd import --judgments` CLI 배선 — `store.WriteLedger` 는 있고 표면만 없다
  - `legacy/outguard.go` 의 범용 판정이 `legacy` 패키지에 사는 것
