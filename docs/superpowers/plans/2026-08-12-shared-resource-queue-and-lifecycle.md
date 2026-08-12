# 공유자원 줄서기 + 대기 자동 재개 + 라이프사이클 관문 — 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 랜딩 큐를 임의 공유자원(자원 집합)으로 일반화하고, 대기 세션이 `fd lane wait` 로 차례에 자동 재개하며, Stop 훅이 finish→land 라이프사이클을 대화 단위로 강제한다.

**Architecture:** 새 표 `landing_queue_resource` 가 줄 행 하나에 자원 N개를 붙이고, 취득은 all-or-nothing(모든 자원에서 내가 최선두 + 전부 빈 상태일 때만) 이다. 대기는 캐시를 안 타는 읽기 전용 조회의 백오프 폴링이고, 취득의 정본은 여전히 `land` 트랜잭션 하나다. Stop 훅은 서버가 대화(machine+cc_session_id) 단위로 판정한 라이프사이클 단계를 `decision:"block"` 으로 낸다 — 무한루프 방벽은 기존 `stop_hook_active` 가드 하나다.

**Tech Stack:** Go 1.x · modernc.org/sqlite · 표준 net/http. 새 의존성 0.

**스펙:** `docs/superpowers/specs/2026-08-12-shared-resource-queue-and-lifecycle-design.md` (같은 브랜치). 모든 판정의 근거가 거기 있다 — 태스크가 "왜"를 물으면 스펙의 제약 1~5 를 읽어라.

## Global Constraints

- **작업 위치**: 워크트리 `/home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-shared-resource-queue-and-lifecycle-gates`. Go 명령은 전부 그 아래 `plugins/flightdeck/server` 에서 돈다(cwd 가 모듈 밖이면 gofmt 가 빈 디렉토리를 조용히 통과한다 — 관문의 무출력은 통과가 아니다).
- **schema.sql 을 고치지 않는다.** 증분은 `internal/store/migrations/008_resource_queue.sql` 하나. `SchemaVersion` 7→**8**.
- **기존 부분 유니크 인덱스 `landing_queue_one_live_per_session` 을 건드리지 않는다** — 세션당 살아 있는 줄 행 1개가 순환대기를 막는 자리다.
- **자원 이름**: `[A-Za-z0-9._/:-]` 1~200자. 빈 집합 거절. 고정 목록·yaml 로더 없음.
- **자동 만료·자동 회수·우선순위 축·세션 R·후속 생성 관문을 만들지 않는다**(스펙 "범위 밖").
- **MCP 도구 수 7 불변**(`land` 에 인자만 추가). **처방 축 5 불변.** **스킬 4개 불변**(새 스킬 금지).
- **R≥1 을 발화 조건으로 삼는 행동 요구 문구를 추가하지 않는다**(`render.go:1480-1483` 의 기각 판정 — 굿하트 실물 1건).
- 주석은 이 저장소의 관례대로 **판정 근거와 실측**을 적는다. 한글이다.
- 커밋 메시지도 한글, 이 저장소의 어투(`feat(flightdeck): …`)를 따른다.
- 각 태스크 끝에 `gofmt -l .` 무출력 + `go build ./...` + 해당 패키지 시험 초록을 확인하고 커밋한다.
- **DESIGN.md 은 208·221행(§3 테이블 수)과 §6 훅 표·REST 표 행 추가만 만진다.** 다른 세션 01KZQE2V 가 576행 REST 표 한 자리를 만지는 중이다 — 랜딩 리베이스에서 충돌하면 그쪽 판을 남기고 내 행을 더한다.

---

### Task 1: 마이그레이션 008 + SchemaVersion 8 + DESIGN §3

**Files:**
- Create: `plugins/flightdeck/server/internal/store/migrations/008_resource_queue.sql`
- Modify: `plugins/flightdeck/server/internal/store/store.go:40-84` (embed + SchemaVersion + migrations 목록)
- Modify: `plugins/flightdeck/server/internal/store/schema_table_count_test.go` (이름 목록에 `landing_queue_resource` 추가)
- Modify: `plugins/flightdeck/DESIGN.md:208` (§3 표제 "테이블 24개"→"테이블 25개"), `:221` 내역(증분 3개로)

**Interfaces:**
- Produces: 표 `landing_queue_resource(row_id INTEGER REFERENCES landing_queue(id), resource TEXT, PK(row_id,resource))`. 이후 태스크 전부가 이 표를 전제한다.

- [ ] **Step 1: DESIGN.md 을 먼저 고친다** (시험이 "문서를 같이 고쳐라"를 요구하는 구조라 순서를 뒤집으면 첫 커밋이 "시험이 시키는 대로 몰래 고친 커밋"이 된다)

`DESIGN.md:208`:
```
## 3. 데이터 모델 — SQLite 파일 하나, 테이블 25개, 계층 셋
```
`DESIGN.md:221` 부근 내역(현행 문장을 읽고 수를 맞춘다):
```
사람이 선언한 것 = `schema.sql` 의 `CREATE TABLE` 21 + `CREATE VIRTUAL TABLE` 1(`judgment_fts`) +
증분 3(`idempotency` · `landing_queue` · `landing_queue_resource`) = 25
```
(그 아래 `sqlite_master` 수를 말하는 문장이 있으면 25+5=30 으로 함께 고친다 — §3 이 두 수의 차이를 설명하는 절이다.)

- [ ] **Step 2: 마이그레이션 파일 작성**

`internal/store/migrations/008_resource_queue.sql`:
```sql
-- 008 · 줄 행이 자원 집합을 갖는다 (schema_version 7 → 8)
--
-- ★ landing_queue 를 ALTER 하지 않고 표를 더한다. 컬럼 하나로는 자원 집합(경로 여럿)을
--   표현할 수 없고, 기존 행 백필을 UPDATE 로 하면 파괴적 조작(opUpdateSet)으로
--   마이그레이션 가드에 걸린다 — INSERT … SELECT 는 가산이라 통과한다
--   (migrate_guard_test.go 의 판정. 실측 2026-08-12).
--
-- ★ 세션당 살아 있는 줄 행 1개(landing_queue_one_live_per_session)는 그대로다.
--   "자원 A 를 쥔 채 자원 B 를 기다린다"가 성립하지 않아 순환대기의 전제가 사라진다.
--   다만 데드락 부재의 전체 증명은 스키마가 아니라 service 의 불변식이다(lane.divergent).
--
-- ★ 자원별 left_at 이 없다. 취득이 all-or-nothing 이라 부분 이탈이 없고,
--   줄 행이 닫히면 그 행의 자원 전부가 함께 빠진다.

CREATE TABLE landing_queue_resource (
  row_id   INTEGER NOT NULL REFERENCES landing_queue(id),
  resource TEXT NOT NULL,
  PRIMARY KEY (row_id, resource),
  CHECK (resource <> '')
);

-- 자원 하나의 줄 맨 앞(FrontLandingRowFor)이 이 인덱스를 탄다.
CREATE INDEX landing_queue_resource_by_name
  ON landing_queue_resource(resource, row_id);

-- 기존 줄 행은 전부 랜딩 줄이다.
INSERT INTO landing_queue_resource(row_id, resource)
  SELECT id, 'landing' FROM landing_queue;
```

- [ ] **Step 3: store.go 세 자리**

`internal/store/store.go` — 007 embed 아래에:
```go
//go:embed migrations/008_resource_queue.sql
var migrationResourceQueue string
```
`const SchemaVersion = 7` → `const SchemaVersion = 8`.
`migrations` 슬라이스 끝에:
```go
	{To: 8, Name: "줄 행이 자원 집합을 갖는다", SQL: migrationResourceQueue},
```

- [ ] **Step 4: 시험 실행 — 테이블 수 시험이 빨간지 확인**

Run: `cd plugins/flightdeck/server && go test ./internal/store/ -run TestDeclaredTablesMatchDesign -count=1`
Expected: FAIL — want 목록에 `landing_queue_resource` 가 없다.

- [ ] **Step 5: `schema_table_count_test.go` 의 want 목록에 `landing_queue_resource` 를 사전순 자리에 추가**

- [ ] **Step 6: store 패키지 전건**

Run: `go test ./internal/store/ -count=1`
Expected: PASS — `TestFreshInstallAndUpgradeProduceTheSameSchema` 가 신규 설치와 업그레이드가 같은 모양임을, `TestBundledMigrationsAreAdditive` 가 008 이 가산임을 잠근다.

- [ ] **Step 7: Commit** — `feat(flightdeck): 줄 행이 자원 집합을 갖는다 — 008 증분과 §3 테이블 25`

---

### Task 2: store 계층 — 자원 축 읽기·쓰기

**Files:**
- Modify: `plugins/flightdeck/server/internal/model/types.go:305-312` (`LandingRow.Resources` 추가)
- Modify: `plugins/flightdeck/server/internal/store/landing.go` (EnqueueLanding 시그니처 · FrontLandingRowFor · attachResources · ValidateResourceName)
- Test: `plugins/flightdeck/server/internal/store/landing_resource_test.go` (새 파일)

**Interfaces:**
- Consumes: Task 1 의 표.
- Produces (이후 태스크가 그대로 쓴다):
  - `model.LandingRow.Resources []string`
  - `func ValidateResourceName(name string) error` (순수 함수)
  - `func (t *Tx) EnqueueLanding(project, sessionID string, resources []string, at time.Time) (model.LandingRow, error)` — **시그니처 변경.** 재진입이면 기존 행을 자원 집합까지 채워 그대로 낸다(집합 대조는 service 몫).
  - `func (t *Tx) FrontLandingRowFor(project, resource string) (model.LandingRow, error)` + Store 짝 — **Resources 를 안 채운다**(ID·SessionID 비교 전용. 채우면 자원 수만큼 질의가 늘고 쓰는 곳이 0이다).
  - `LiveLandingRow`·`LastLandingRow`·`ListLandingQueue`(Tx/Store 전부)가 `Resources` 를 채워서 낸다.
  - 기존 `FrontLandingRow(project)` 는 **이 태스크에서 지우지 않는다**(호출자 교체는 Task 3·5). 제거는 Task 5 Step 6.

- [ ] **Step 1: 실패하는 시험 작성** — `internal/store/landing_resource_test.go`

```go
package store

import (
	"errors"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 픽스처 헬퍼(mustProject·mustSession·s.Tx 사용법)는 landing 계열 기존 시험
// (store_test.go 의 TestListLandingQueueKeepsOrderAndDoesNotFilterByWindow 부근)을 그대로 베낀다.

func TestEnqueueLandingCarriesItsResourceSet(t *testing.T) {
	s := openTestStore(t)
	// … 프로젝트 p·세션 a 픽스처 …
	var row model.LandingRow
	if err := s.Tx(ctx, func(tx *Tx) error {
		var err error
		row, err = tx.EnqueueLanding("p", "a", []string{"path:x.go", "landing"}, time.Time{})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// 정렬해 저장·반환된다 — 집합 대조(service)가 순서에 흔들리면 안 된다.
	if got, want := row.Resources, []string{"landing", "path:x.go"}; !equalStrings(got, want) {
		t.Fatalf("자원 집합이 %v 다 — want %v", got, want)
	}
}

func TestEnqueueLandingReentryReturnsTheSameRowWithItsOriginalResources(t *testing.T) {
	// 같은 세션이 **다른** 집합으로 다시 서도 store 는 기존 행+기존 집합을 그대로 낸다.
	// 거절은 service 의 몫이다(집합 대조) — store 가 거절하면 재진입 안전이 깨진다.
	// … a 가 {landing} 으로 선 뒤 {path:y.go} 로 EnqueueLanding → 같은 row.ID, Resources == {landing} 단정 …
}

func TestEnqueueLandingRefusesEmptyOrBadResourceNames(t *testing.T) {
	// 빈 집합 → 오류. "" 포함 → 오류. 201자 이름 → 오류. "a b"(공백) → 오류.
	// ValidateResourceName 을 직접도 찌른다: "path:internal/api/x.go" · "deploy-staging" 통과.
}

func TestFrontLandingRowForSplitsByResource(t *testing.T) {
	// a 가 {r1} 로, b 가 {r2} 로 선다.
	// FrontLandingRowFor(p, "r1") == a 의 행, FrontLandingRowFor(p, "r2") == b 의 행.
	// FrontLandingRowFor(p, "r3") → ErrNotFound.
	// a 의 행을 닫으면 r1 의 front 가 사라진다(ErrNotFound).
}

func TestListLandingQueueAttachesResources(t *testing.T) {
	// 두 행을 넣고 ListLandingQueue → 각 행의 Resources 가 넣은 그대로다(N+1 이 아니라
	// 한 질의로 붙는 것은 여기서 단정할 수 없다 — 그 축은 attachResources 의 IN 질의 주석에 남긴다).
}
```
`equalStrings`·`openTestStore` 는 기존 시험 파일의 것을 쓴다(없으면 이 파일에 지역으로 둔다).

- [ ] **Step 2: 실행해 실패 확인**

Run: `go test ./internal/store/ -run 'TestEnqueueLanding|TestFrontLandingRowFor|TestListLandingQueueAttaches' -count=1`
Expected: FAIL — `EnqueueLanding` 인자 수 불일치(컴파일 오류).

- [ ] **Step 3: 구현**

`internal/model/types.go` — `LandingRow` 에:
```go
	Resources  []string // 이 행이 줄 선 자원들(정렬). 008 이전 행은 마이그레이션이 'landing' 을 백필했다
```

`internal/store/landing.go`:

```go
// laneResourceMax 는 자원 이름 길이 상한이다. 오류 문구의 clip(…, 64) 관례보다 넉넉하되
// 무한하지 않게 둔다 — path:<경로> 가 들어오는 자리라 64는 좁다.
const laneResourceMax = 200

// ValidateResourceName 은 자원 이름 하나가 성립하는지 본다. 순수 함수다.
// `:` 는 path:<경로> 규약, `/`·`.` 는 경로 자체 때문에 허용한다.
func ValidateResourceName(name string) error {
	if name == "" {
		return errors.New("자원 이름이 비었다")
	}
	if len(name) > laneResourceMax {
		return fmt.Errorf("자원 이름이 %d자다 — 상한은 %d자다: %s", len(name), laneResourceMax, clip(name, 64))
	}
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '.', c == '_', c == '/', c == ':', c == '-':
		default:
			return fmt.Errorf("자원 이름에 허용되지 않는 문자 %q 가 있다([A-Za-z0-9._/:-] 만): %s",
				c, clip(name, 64))
		}
	}
	return nil
}
```

`EnqueueLanding` 시그니처 변경 + 본문:
```go
func (t *Tx) EnqueueLanding(project, sessionID string, resources []string, at time.Time) (model.LandingRow, error) {
	if project == "" || sessionID == "" { /* 기존 그대로 */ }
	if len(resources) == 0 {
		return model.LandingRow{}, errors.New("자원 집합이 비었다 — 무엇에 줄을 서는지 없이 줄 행을 만들 수 없다")
	}
	sorted := append([]string(nil), resources...)
	sort.Strings(sorted)
	for _, r := range sorted {
		if err := ValidateResourceName(r); err != nil {
			return model.LandingRow{}, err
		}
	}
	at = atStamp(at)
	res, err := t.tx.ExecContext(t.ctx, `INSERT INTO landing_queue(…) VALUES (…)`, …) // 기존 그대로
	if err != nil {
		row, qErr := liveLandingRow(t.ctx, t.tx, project, sessionID)
		if qErr == nil {
			// 이미 서 있다. **자원 집합까지 채워** 그대로 낸다 — 요청 집합과 다른지의
			// 판정(거절)은 service 몫이다. 여기서 거절하면 재진입 안전이 깨진다.
			return row, nil // liveLandingRow 가 Resources 를 채운다(아래)
		}
		/* 기존 오류 갈래 그대로 */
	}
	id, err := res.LastInsertId()
	if err != nil { /* 기존 그대로 */ }
	for _, r := range sorted {
		if _, err := t.tx.ExecContext(t.ctx,
			`INSERT INTO landing_queue_resource(row_id, resource) VALUES (?, ?)`, id, r); err != nil {
			return model.LandingRow{}, fmt.Errorf("줄 행 %d 의 자원 %s 기록 실패: %w", id, clip(r, 64), err)
		}
	}
	return model.LandingRow{ID: id, Project: project, SessionID: sessionID, EnqueuedAt: at, Resources: sorted}, nil
}
```

자원 붙이기(한 질의 IN — 행마다 질의하지 않는다):
```go
// attachResources 는 줄 행들에 자원 집합을 붙인다. 행 수와 무관하게 질의 하나다 —
// 줄 길이는 실무상 한 자릿수지만, N+1 을 여기 두면 ListLandingQueue 를 부르는
// 보드가 줄 길이에 비례해 느려지고 그 원인이 이 파일 밖에서는 안 보인다.
func attachResources(ctx context.Context, q dbtx, rows []model.LandingRow) error {
	if len(rows) == 0 {
		return nil
	}
	ids := make([]any, len(rows))
	marks := make([]string, len(rows))
	for i, r := range rows {
		ids[i], marks[i] = r.ID, "?"
	}
	rs, err := q.QueryContext(ctx, `
		SELECT row_id, resource FROM landing_queue_resource
		WHERE row_id IN (`+strings.Join(marks, ",")+`) ORDER BY resource`, ids...)
	if err != nil {
		return fmt.Errorf("줄 행 자원 조회 실패: %w", err)
	}
	defer rs.Close()
	byID := map[int64][]string{}
	for rs.Next() {
		var id int64
		var name string
		if err := rs.Scan(&id, &name); err != nil {
			return fmt.Errorf("줄 행 자원 해석 실패: %w", err)
		}
		byID[id] = append(byID[id], name)
	}
	if err := rs.Err(); err != nil {
		return fmt.Errorf("줄 행 자원 순회 실패: %w", err)
	}
	for i := range rows {
		rows[i].Resources = byID[rows[i].ID]
	}
	return nil
}
```
`liveLandingRow`·`lastLandingRow`·`listLandingQueue` 의 성공 반환 직전에 `attachResources` 호출을 넣는다(단일 행은 `[]model.LandingRow{r}` 로 감싸서).

`FrontLandingRowFor`:
```go
// FrontLandingRowFor 는 자원 하나의 줄 맨 앞이다. 순서 집행(land 의 all-or-nothing 판정과
// 처방의 차례 판정)이 자원마다 이 함수를 본다. **Resources 를 안 채운다** — 쓰는 곳이
// ID·SessionID 비교뿐이고, 채우면 자원 수만큼 질의가 는다.
func frontLandingRowFor(ctx context.Context, q dbtx, project, resource string) (model.LandingRow, error) {
	row := q.QueryRowContext(ctx, `
		SELECT q.id, q.project, q.session_id, q.enqueued_at, q.left_at, q.left_kind, q.left_detail
		FROM landing_queue q
		JOIN landing_queue_resource r ON r.row_id = q.id
		WHERE q.project = ? AND r.resource = ? AND q.left_at IS NULL
		ORDER BY q.id LIMIT 1`, project, resource)
	r, err := scanLandingRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return r, notFoundNote(NFLiveLandingRow,
			fmt.Sprintf("프로젝트 %s · 자원 %s 줄의 맨 앞에 해당하는", clip(project, 64), clip(resource, 64)))
	}
	if err != nil {
		return r, fmt.Errorf("자원 줄 맨 앞 조회 실패(project=%q resource=%q): %w",
			clip(project, 64), clip(resource, 64), err)
	}
	return r, nil
}

func (t *Tx) FrontLandingRowFor(project, resource string) (model.LandingRow, error) {
	return frontLandingRowFor(t.ctx, t.tx, project, resource)
}
func (s *Store) FrontLandingRowFor(ctx context.Context, project, resource string) (model.LandingRow, error) {
	return frontLandingRowFor(ctx, s.db, project, resource)
}
```
(import 에 `sort` 추가.)

- [ ] **Step 4: 기존 호출자 컴파일 수선(최소)** — `service/landing.go:126` 과 `finish` 계열은 아직 옛 시그니처를 부른다. 이 태스크에서는 `service/landing.go:126` 의 호출을 `t.EnqueueLanding(in.Project, in.SessionID, []string{LaneResource}, now)` 로만 바꿔 컴파일을 유지한다(로직 개편은 Task 3).

Run: `go build ./... && go test ./internal/store/ -count=1`
Expected: PASS (새 시험 포함 전건).

- [ ] **Step 5: 기존 landing 시험 전건** — `go test ./internal/... -count=1` 로 service 쪽도 초록인지 확인(옛 동작은 `{landing}` 단일 집합으로 보존된다).

- [ ] **Step 6: Commit** — `feat(flightdeck): store 가 줄 행의 자원 집합을 읽고 쓴다 — front 는 자원마다 갈린다`

---

### Task 3: service.Land — all-or-nothing 취득

**Files:**
- Modify: `plugins/flightdeck/server/internal/service/landing.go:36-205` (LandInput·LandResult·Land·lanePosition)
- Test: `plugins/flightdeck/server/internal/service/landing_resource_test.go` (새 파일)

**Interfaces:**
- Consumes: Task 2 의 store 함수들.
- Produces:
  - `LandInput{Project, SessionID string; Resources []string}` — 빈 Resources 는 `["landing"]` 으로 정규화.
  - `LandResult` 에 `Resources []string`, `Blockers []LaneBlocker` 추가(기존 필드 유지).
  - `type LaneBlocker struct { Resource string; Position int; Holder *LaneHolder; FrontRowID int64; FrontSessionID string }` (json 태그는 snake_case).
  - 집합 불일치 거절: `RefusedError{What:"land"}` — 문구에 기존 집합과 빠지는 길(`leave`) 포함.

- [ ] **Step 1: 실패하는 시험 작성** — `internal/service/landing_resource_test.go`

픽스처는 기존 `landing_test.go` 계열(서비스 생성·프로젝트·세션 셋업)을 베낀다. 시험 여섯:

```go
// ① 서로 다른 자원을 요구한 두 세션 → 둘 다 turn (자원별로 갈린다)
func TestLandGrantsDisjointResourceSetsIndependently(t *testing.T)

// ② A 가 {r1} 을 쥔 상태에서 B 가 {r1,r2} → waiting 이고 r2 도 안 잡는다(all-or-nothing).
//    단정: B 의 Blockers 에 r1(holder=A)이 있고, HeldBy(p,"r2") 는 ErrNotFound 다.
func TestLandDoesNotPartiallyAcquire(t *testing.T)

// ③ ②의 상태에서 C 가 {r2} → waiting (B 가 r2 의 최선두라 뒤로 안 밀린다 = 굶주림 없음).
//    단정: C 의 Blockers[0] 이 {Resource:"r2", FrontSessionID:B}.
func TestLandFrontOfAQueueIsNotOvertaken(t *testing.T)

// ④ ③에서 A 가 report(ok) 로 빠지면 B 의 다음 land 가 {r1,r2} 를 **한 번에** 잡는다.
//    그 뒤 C 는 여전히 waiting.
func TestLandGrantsAllResourcesAtOnceWhenAllFree(t *testing.T)

// ⑤ 같은 세션이 다른 집합으로 다시 서면 거절(RefusedError), 같은 집합이면 같은 RowID.
//    거절 문구에 기존 집합과 "leave" 가 있다.
func TestLandRefusesAChangedResourceSetOnReentry(t *testing.T)

// ⑥ 재진입: turn 인 세션이 같은 집합으로 다시 land → turn 그대로, grant 이벤트는 한 번만.
func TestLandReentryOfHolderStaysTurn(t *testing.T)
```

- [ ] **Step 2: 실행해 실패 확인**

Run: `go test ./internal/service/ -run 'TestLand(Grants|DoesNot|Front|Refuses|Reentry)' -count=1`
Expected: FAIL — `LandInput` 에 `Resources` 없음(컴파일 오류).

- [ ] **Step 3: 구현** — `internal/service/landing.go`

타입:
```go
type LandInput struct {
	Project, SessionID string
	Resources          []string // 빈 값 = ["landing"]
}

// LaneBlocker 는 waiting 일 때 나를 막는 자원 하나다.
type LaneBlocker struct {
	Resource       string      `json:"resource"`
	Position       int         `json:"position"`                  // 그 자원 줄에서 내 순번(1-based). 0 = 내 행이 그 줄에 안 보인다(어긋남)
	Holder         *LaneHolder `json:"holder,omitempty"`          // 그 자원을 쥔 세션
	FrontRowID     int64       `json:"front_row_id,omitempty"`    // 점유는 없지만 내 앞인 줄 행
	FrontSessionID string      `json:"front_session_id,omitempty"`
}
```
`LandResult` 에 `Resources []string \`json:"resources,omitempty"\`` 와 `Blockers []LaneBlocker \`json:"blockers,omitempty"\`` 추가. 기존 `Position`·`Holder` 는 유지한다 — Position 은 **가장 뒤인 자원 기준**(최악 순번), Holder 는 **첫 blocker 의 점유자**(렌더·CLI 하위 호환).

정규화:
```go
// normalizeResources 는 빈 집합을 {landing} 으로 접고 정렬·중복 제거한다. 순수 함수다.
func normalizeResources(in []string) []string {
	if len(in) == 0 {
		return []string{LaneResource}
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, r := range in {
		r = strings.TrimSpace(r)
		if r == "" || seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	if len(out) == 0 {
		return []string{LaneResource}
	}
	sort.Strings(out)
	return out
}
```

`Land` 본문 — 트랜잭션 안을 다음으로 교체(이벤트·시각 규율은 기존 그대로):
```go
	resources := normalizeResources(in.Resources)
	now := s.now()
	var out LandResult
	err := s.st.Tx(ctx, func(t *store.Tx) error {
		t.LogEvent("lane.land", in.Project, in.SessionID,
			map[string]any{"mode": "acquire", "resources": len(resources)})

		mine, err := t.EnqueueLanding(in.Project, in.SessionID, resources, now)
		if err != nil {
			return err
		}
		if !equalStringSlices(mine.Resources, resources) {
			// 재진입인데 집합이 다르다 — store 는 기존 행을 그대로 냈고(재진입 안전),
			// 거절은 여기서 한다. 조용히 기존 집합으로 진행하면 세션은 r2 를 기다린다고
			// 믿는데 실제로는 r1 줄에 서 있다(스펙 제약 4 의 재진입 결함 그 자체다).
			return &RefusedError{What: "land",
				Reason: fmt.Sprintf("이미 자원 %s 로 줄에 서 있다(행 %d) — 요청한 %s 와 다르다",
					strings.Join(mine.Resources, " "), mine.ID, strings.Join(resources, " ")),
				Guidance: "집합을 바꾸려면 land(leave:\"사유\") 로 빠진 뒤 다시 서라 — 순번은 잃는다(맨 뒤)."}
		}
		out = LandResult{State: "waiting", RowID: mine.ID, Resources: mine.Resources}

		// all-or-nothing 판정: 모든 자원에서 (내 행이 최선두) 그리고 (빈 레인이거나 내가 점유자).
		// tx 가 _txlock=immediate 로 직렬화되므로 판정과 취득 사이에 남이 못 끼어든다.
		grantable := true
		var blockers []LaneBlocker
		heldByMe := map[string]bool{}
		for _, r := range mine.Resources {
			front, ferr := t.FrontLandingRowFor(in.Project, r)
			if ferr != nil {
				return ferr // 방금 넣었으므로 ErrNotFound 는 불가능하다
			}
			held, herr := t.HeldBy(in.Project, r)
			switch {
			case herr == nil && held.SessionID == in.SessionID:
				heldByMe[r] = true // 재진입 — 이미 내 것이다. front 와 무관하게 통과다
			case herr == nil:
				// 남이 쥐었다(정상 대기 또는 "맨 앞인데 남이 쥔" 어긋남 — 어느 쪽이든
				// 취득 불가라는 사실은 같고, 어긋남을 푸는 것은 사람의 회수다).
				grantable = false
				blockers = append(blockers, LaneBlocker{Resource: r,
					Holder: &LaneHolder{SessionID: held.SessionID, AcquiredAt: held.AcquiredAt}})
			case errors.Is(herr, store.ErrNotFound):
				if front.ID != mine.ID {
					grantable = false
					blockers = append(blockers, LaneBlocker{Resource: r,
						FrontRowID: front.ID, FrontSessionID: front.SessionID})
				}
			default:
				return herr
			}
		}

		if grantable {
			for _, r := range mine.Resources {
				if heldByMe[r] {
					continue // 재확인이지 부여가 아니다 — grant 를 다시 세지 않는 기존 규율
				}
				if _, aerr := t.AcquireResource(in.Project, r, store.Holder{SessionID: in.SessionID}, now); aerr != nil {
					return aerr // 판정 직후라 ResourceHeldError 는 어긋남뿐이다 — 그대로 올려 롤백한다
				}
			}
			out.State = "turn"
			out.Position = 1
			if len(heldByMe) < len(mine.Resources) {
				t.LogEvent("lane.grant", in.Project, in.SessionID,
					map[string]any{"row": mine.ID, "resources": len(mine.Resources)})
			}
			return nil
		}

		// 자원별 순번을 채운다. Position(대표값)은 최악 순번이다.
		for i := range blockers {
			pos, perr := s.resourcePosition(t, in.Project, blockers[i].Resource, mine.ID)
			if perr != nil {
				return perr
			}
			blockers[i].Position = pos
			if pos > out.Position {
				out.Position = pos
			}
		}
		out.Blockers = blockers
		if len(blockers) > 0 && blockers[0].Holder != nil {
			out.Holder = blockers[0].Holder
		}
		return nil
	})
```
커밋 뒤 신호 채움(기존 `out.Holder` 갈래)을 Blockers 전체로 넓힌다:
```go
	if err == nil {
		for i := range out.Blockers {
			if h := out.Blockers[i].Holder; h != nil {
				h.LastSignalAt, _ = s.lastSignal(ctx, h.SessionID)
			}
		}
		if out.Holder != nil && out.Holder.LastSignalAt == nil && len(out.Blockers) > 0 {
			out.Holder = out.Blockers[0].Holder // 같은 포인터라 이미 채워졌다 — 이 줄은 사실상 무동작이고, 대표 필드가 사본이 되지 않게 포인터를 공유한다는 사실을 여기 적는다
		}
	}
```
(`out.Holder` 를 blockers[0].Holder 포인터로 공유했으므로 별도 채움이 필요 없다 — 위 두 번째 갈래는 지우고 주석만 남겨도 된다. 구현 시 포인터 공유를 확인하고 중복 질의를 만들지 마라.)

`lanePosition` 을 자원별로 바꾼 헬퍼:
```go
// resourcePosition 은 자원 하나의 줄에서 내 행의 순번(1-based)이다. 트랜잭션 안에서 센다
// (밖에서 읽으면 방금 넣은 내 행이 안 보인다 — 기존 lanePosition 의 규율 그대로).
func (s *Service) resourcePosition(t *store.Tx, project, resource string, rowID int64) (int, error) {
	rows, err := t.ListLandingQueue(project)
	if err != nil {
		return 0, err
	}
	pos := 0
	for _, r := range rows {
		if !containsString(r.Resources, resource) {
			continue
		}
		pos++
		if r.ID == rowID {
			return pos, nil
		}
	}
	return 0, nil // 내 행이 그 줄에 안 보인다 — 없는 자리를 1로 채우면 "맨 앞"이라는 거짓이 된다
}
```
기존 `lanePosition` 은 삭제한다(호출자가 Land 하나였다). `equalStringSlices`·`containsString` 은 이 파일 하단에 순수 함수로 둔다.

- [ ] **Step 4: 실행**

Run: `go test ./internal/service/ -count=1`
Expected: PASS — 새 여섯 + 기존 landing 시험 전건(기존 시험은 `{landing}` 단일 집합 경로를 지나므로 거동이 보존된다). 기존 시험 중 `LandResult.Holder` 를 단정하는 것이 있으면 실패할 수 있다 — blockers[0].Holder 공유가 그 단정을 지키는지 확인하고, 실패하면 **시험이 아니라 공유 로직을** 고친다.

- [ ] **Step 5: Commit** — `feat(flightdeck): land 취득이 자원 집합의 all-or-nothing 이 된다 — 부분 취득은 데드락의 재료다`

---

### Task 4: 반납·이탈·회수가 자원 집합을 따라간다

**Files:**
- Modify: `plugins/flightdeck/server/internal/service/landing.go:207-336` (LandReport·LandLeave), `:492-727` (ReleaseLaneRow·laneReleaseBody)
- Test: `plugins/flightdeck/server/internal/service/landing_resource_test.go` (추가)

**Interfaces:**
- Consumes: Task 2·3.
- Produces: 외부 시그니처 불변. 동작만 자원 집합으로 넓어진다.

- [ ] **Step 1: 실패하는 시험 추가**

```go
// ⑦ {r1,r2} 를 쥔 세션의 report(ok) → 두 자원 다 반납되고 행이 닫힌다.
func TestLandReportReleasesTheWholeResourceSet(t *testing.T)

// ⑧ 자원 r2 만 걸린 줄 행을 회수해도 landing 점유는 안 건드린다.
//    (지금 코드는 LaneResource 하드코딩이라 이 시험이 개편 전엔 빨갛다 — 스펙 제약 4)
func TestReleaseLaneRowTouchesOnlyTheRowsResources(t *testing.T)

// ⑨ 회수 판단 본문의 "그때 줄에 있던 사람"에 다른 자원의 대기자가 안 섞인다.
func TestLaneReleaseBodyScopesQueueToOverlappingResources(t *testing.T)

// ⑩ 살아 있는 자원 점유가 있으면 반드시 대응하는 살아 있는 줄 행이 있다 — 임의 자원판.
//    기존 TestLiveLandingHoldAlwaysHasALiveQueueRow 를 읽고 같은 방식으로
//    {r1,r2} 시나리오(report·leave·release·finish 네 반납 경로)를 돈다.
func TestLiveHoldAlwaysHasALiveRowForAnyResource(t *testing.T)
```

- [ ] **Step 2: 실행해 실패 확인** — ⑧·⑨가 빨갛다(하드코딩 때문). ⑦·⑩은 구현에 따라 초록일 수 있다 — 빨간 것이 둘뿐이어도 진행한다(⑦·⑩은 회귀 방지 락이다).

- [ ] **Step 3: 구현**

`LandReport` — `HeldBy(in.Project, LaneResource)` 한 번 보던 것을 **행 기준**으로:
```go
		// 내 살아 있는 줄 행을 먼저 읽는다 — 이 행의 자원 집합이 반납 대상이다.
		row, rerr := t.LiveLandingRow(in.Project, in.SessionID)
		switch {
		case rerr == nil:
			out.RowID, out.Resources = row.ID, row.Resources
		case errors.Is(rerr, store.ErrNotFound):
			// 줄 행이 없다 — 회수됐거나 선 적이 없다. 사실만 답한다.
			return s.laneNotMine(t, in.Project, in.SessionID, &out)
		default:
			return rerr
		}
		// 행 자원 중 내가 쥔 것을 전부 반납한다. 하나도 안 쥐었으면 "내 레인이 아니다"다.
		mineCount := 0
		for _, r := range row.Resources {
			held, herr := t.HeldBy(in.Project, r)
			if herr != nil && !errors.Is(herr, store.ErrNotFound) {
				return herr
			}
			if herr == nil && held.SessionID == in.SessionID {
				if err := t.ReleaseResource(in.Project, r, store.Holder{SessionID: in.SessionID}); err != nil {
					return err
				}
				mineCount++
			}
		}
		if mineCount == 0 {
			// 줄엔 있는데 아무것도 안 쥐었다 = 아직 차례가 아니거나 회수됐다.
			return s.laneNotMine(t, in.Project, in.SessionID, &out)
		}
		if mineCount < len(row.Resources) {
			// all-or-nothing 이 지켜졌다면 없는 모양이다 — 어긋남을 원장에 남기고 계속한다
			// (기존 lane.divergent 규율: 여기서 멈추면 아무도 못 잡는 레인이 남는다).
			t.LogEvent("lane.divergent", in.Project, in.SessionID,
				map[string]any{"mode": "report", "state": "partial-hold", "count": mineCount})
		}
		if err := t.CloseLandingRowBySession(in.Project, in.SessionID, in.Kind, in.Detail); err != nil {
			return err
		}
		out.State = "released"
		return nil
```
기존 "hold-without-row" divergent 갈래는 위 구조로 대체된다(행이 없으면 laneNotMine 으로 빠지는데, 그 세션이 점유를 쥔 채 행만 없는 어긋남은 ⑩의 불변식 시험과 회수 표면이 담당한다 — `laneNotMine` 은 읽기만 하므로 안전하다).

`LandLeave` — 점유 반납 갈래를 행 자원 루프로(위와 같은 모양, 행이 없으면 기존처럼 멱등 통과).

`ReleaseLaneRow` — `held/holdLine` 갈래를 target 행의 자원 루프로:
```go
			var holdLines []string
			for _, res := range target.Resources {
				held, herr := t.HeldBy(project, res)
				if herr != nil && !errors.Is(herr, store.ErrNotFound) {
					return herr
				}
				switch {
				case herr == nil && held.SessionID == target.SessionID:
					if err := t.ForceReleaseResource(project, res, reason); err != nil {
						return err
					}
					out.HeldRelease = true
					holdLines = append(holdLines, fmt.Sprintf("점유(%s): 회수함(획득 %s · 경과 %s)",
						res, held.AcquiredAt.Format(time.RFC3339), now.Sub(held.AcquiredAt).Round(time.Second)))
				case herr == nil:
					holdLines = append(holdLines, fmt.Sprintf("점유(%s): 다른 세션 %s 가 쥐고 있어 건드리지 않았다", res, held.SessionID))
				default:
					holdLines = append(holdLines, fmt.Sprintf("점유(%s): 없음(대기 중이라 반납할 것이 없다)", res))
				}
			}
			holdLine := strings.Join(holdLines, "\n")
```
`laneReleaseBody` — "그때 줄에 있던 사람" 루프 앞에 필터:
```go
	b.WriteString("그때 줄에 있던 사람(자원이 겹치는 줄만):")
	for _, r := range queue {
		if !resourcesOverlap(r.Resources, target.Resources) {
			continue // 다른 자원의 대기자를 불변 기록에 박지 않는다 — :525-527 의 금지 부류
		}
		…기존 포맷…
	}
```
`resourcesOverlap(a, b []string) bool` 순수 함수를 하단에 추가. 판단 본문에 `fmt.Fprintf(&b, "자원: %s\n", strings.Join(target.Resources, " "))` 한 줄을 사유 위에 더한다.

- [ ] **Step 4: 실행** — `go test ./internal/service/ -count=1` PASS.

- [ ] **Step 5: Commit** — `feat(flightdeck): 반납·이탈·회수가 행의 자원 집합을 따라간다 — landing 하드코딩 10곳 중 반납 계열을 걷는다`

---

### Task 5: 처방 차례 판정(laneTurnRow)이 자원 집합을 본다 + FrontLandingRow(project) 제거

**Files:**
- Modify: `plugins/flightdeck/server/internal/service/prescribe.go:355-403` (laneTurnRow)
- Modify: `plugins/flightdeck/server/internal/store/landing.go:190-214` (구 frontLandingRow 삭제)
- Test: `plugins/flightdeck/server/internal/service/prescribe_test.go` 계열에 추가 (기존 lane-turn 시험 파일을 찾아 그 옆에)

**Interfaces:**
- Consumes: `Store.LiveLandingRow`(Resources 포함) · `Store.FrontLandingRowFor`.
- Produces: `laneTurnRow` 시그니처 불변(`func (s *Service) laneTurnRow(ctx, project, sessionID string) int64`). 구 `FrontLandingRow(project)`(Tx·Store 짝 모두)는 **이 태스크에서 삭제** — 이후 태스크가 호출하면 컴파일 오류로 잡힌다.

- [ ] **Step 1: 실패하는 시험** — 기존 `TestLaneTurn*` 시험이 있는 파일을 `grep -rn "laneTurnRow\|LaneTurnRow" internal/service/*_test.go` 로 찾고 그 옆에:

```go
// 자원 r2 줄의 맨 앞인 세션은, r1(landing) 레인을 남이 쥐고 있어도 차례다.
func TestLaneTurnIsJudgedPerResource(t *testing.T)

// {r1,r2} 로 선 세션은 r1 이 비어도 r2 를 남이 쥐면 차례가 아니다(all-or-nothing 과 정합).
func TestLaneTurnRequiresEveryResourceOfTheRow(t *testing.T)
```

- [ ] **Step 2: 실행해 실패 확인** — 첫 시험이 빨갛다(현행은 project 전체 front 하나만 본다).

- [ ] **Step 3: 구현** — `laneTurnRow` 교체:

```go
// laneTurnRow 는 이 세션 차례가 된 줄 행의 번호다. 0 이면 차례가 아니다.
//
// 차례의 정의가 자원 집합의 곱으로 넓어졌다(2026-08-12): 내 살아 있는 줄 행이 있고,
// 그 행의 **모든** 자원에서 (맨 앞이 그 행) 그리고 (레인이 비었다).
// 남이든 나든 쥔 자원이 하나라도 있으면 0 이다 — 남이 쥔 것은 어긋남(사람의 회수가 푼다),
// 내가 쥔 것은 land 가 이미 turn 으로 답한 것이라 같은 말을 두 번 하는 것이다.
// (오류를 안 올리는 규율·LandingLane 기각 근거는 개편 전 주석 그대로다.)
func (s *Service) laneTurnRow(ctx context.Context, project, sessionID string) int64 {
	row, err := s.st.LiveLandingRow(ctx, project, sessionID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.log.WarnContext(ctx, "처방: 내 줄 행을 못 읽었다",
				"session_id", sessionID, "project", project, "error", err.Error())
		}
		return 0 // 줄에 안 섰다(정상) 또는 못 읽었다
	}
	for _, r := range row.Resources {
		front, ferr := s.st.FrontLandingRowFor(ctx, project, r)
		if ferr != nil || front.ID != row.ID {
			return 0 // 그 자원 줄에 앞사람이 있거나 못 읽었다
		}
		if _, herr := s.st.HeldBy(ctx, project, r); herr == nil {
			return 0 // 누가 쥐고 있다(나든 남이든)
		} else if !errors.Is(herr, store.ErrNotFound) {
			s.log.WarnContext(ctx, "처방: 레인 점유를 못 읽었다",
				"session_id", sessionID, "project", project, "error", herr.Error())
			return 0
		}
	}
	return row.ID
}
```

- [ ] **Step 4: 구 `frontLandingRow`·`FrontLandingRow`(Tx·Store) 삭제** 후 `go build ./...` — 남은 호출자가 있으면 컴파일 오류가 알려 준다(시험 픽스처 포함 전부 `FrontLandingRowFor` 로 옮긴다).

- [ ] **Step 5: 실행** — `go test ./internal/... -count=1` PASS.

- [ ] **Step 6: Commit** — `feat(flightdeck): 차례 처방이 자원 집합의 곱으로 판정된다 — project 전체 front 는 지웠다`

---

### Task 6: LaneView 자원별 개편 + 보드·웹 렌더

**Files:**
- Modify: `plugins/flightdeck/server/internal/service/landing.go:68-90, 360-490` (LaneView·LandingLane)
- Modify: `plugins/flightdeck/server/internal/mcpsrv/render.go:761-830 부근` (renderLane)
- Modify: `plugins/flightdeck/server/internal/web/page.go:1021-1063` (fillLane) + `web/dashboard.gohtml` 의 레인 절
- Test: 기존 `renderLane`·`fillLane`·`LandingLane` 시험들(grep 으로 찾는다) + 새 단정

**Interfaces:**
- Produces:
```go
type LaneView struct {
	Resources []ResourceLane `json:"resources"`
}
type ResourceLane struct {
	Resource string      `json:"resource"`
	Holder   *LaneHolder `json:"holder,omitempty"`
	Entries  []LaneEntry `json:"entries"`
}
```
`LaneEntry` 는 기존 그대로. `LandingLane(ctx, project)` 시그니처 불변. **`Resources` 는 절대 nil 이 아니다**(0건 = 빈 슬라이스, "안 읽었다"는 호출부의 `*LaneView` nil — 기존 규율 유지). Task 9·10 의 `fd lane wait` 가 이 JSON 을 파싱한다.

- [ ] **Step 1: 실패하는 시험** — `internal/service/landing_resource_test.go` 에:

```go
// LandingLane 이 자원별로 갈라 낸다: {r1} 점유 + {r1,r2} 대기 하나면
// Resources 가 [r1(holder, entries 2), r2(entries 1)] 이다(이름순).
func TestLandingLaneSplitsByResource(t *testing.T)

// hold 만 있고 줄 행이 없는 자원도 ResourceLane 으로 나온다(어긋남을 화면이 봐야 한다).
func TestLandingLaneShowsHoldWithoutRowPerResource(t *testing.T)
```

- [ ] **Step 2: 실행해 실패 확인** (컴파일 오류 — LaneView 모양).

- [ ] **Step 3: `LandingLane` 재구현**

```go
func (s *Service) LandingLane(ctx context.Context, project string) (LaneView, error) {
	if strings.TrimSpace(project) == "" {
		return LaneView{}, &RefusedError{What: "lane", Reason: "project 가 비었다"}
	}
	rows, err := s.st.ListLandingQueue(ctx, project) // Resources 포함
	if err != nil {
		return LaneView{}, err
	}
	holds, err := s.st.ListHeld(ctx, project) // 살아 있는 점유 전부(자원 무관)
	if err != nil {
		return LaneView{}, err
	}
	// 재확인(기존 규율의 자원판): 점유가 있는데 그 세션의 행이 어느 줄에도 없으면 한 번만 다시 읽는다.
	if holdWithoutRow(holds, rows) {
		if fresh, ferr := s.st.ListLandingQueue(ctx, project); ferr == nil {
			rows = fresh
		} else {
			return LaneView{}, ferr
		}
	}
	// 자원 우주 = 줄 행들의 자원 ∪ 점유의 자원. 이름순.
	names := map[string]bool{}
	for _, r := range rows {
		for _, res := range r.Resources {
			names[res] = true
		}
	}
	holdBy := map[string]model.ResourceHold{} // ListHeld 는 []model.ResourceHold 를 낸다(store/resource.go:295)
	for _, h := range holds {
		names[h.Resource] = true
		holdBy[h.Resource] = h
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)

	// 신호는 세션당 한 번만 묻는다(자원 여럿에 겹치는 세션이 정상이라 맵이 맞다).
	signals := map[string]*time.Time{}
	sig := func(sid string) *time.Time {
		if v, ok := signals[sid]; ok {
			return v
		}
		at, _ := s.lastSignal(ctx, sid)
		signals[sid] = at
		return at
	}

	out := LaneView{Resources: make([]ResourceLane, 0, len(sorted))}
	for _, name := range sorted {
		lane := ResourceLane{Resource: name, Entries: []LaneEntry{}}
		if h, ok := holdBy[name]; ok {
			lane.Holder = &LaneHolder{SessionID: h.SessionID, AcquiredAt: h.AcquiredAt,
				LastSignalAt: sig(h.SessionID)}
		}
		for _, r := range rows {
			if !containsString(r.Resources, name) {
				continue
			}
			lane.Entries = append(lane.Entries, LaneEntry{RowID: r.ID, SessionID: r.SessionID,
				EnqueuedAt: r.EnqueuedAt, LastSignalAt: sig(r.SessionID)})
		}
		out.Resources = append(out.Resources, lane)
	}
	return out, nil
}
```
(`holdWithoutRow(holds, rows) bool` 순수 함수 추가. `rowsHaveSession` 은 그 안으로 흡수하거나 유지. `ListHeld` 반환 원소의 실제 필드명은 `store/resource.go:282-306` 을 열어 맞춘다.)

- [ ] **Step 4: `renderLane` 자원별로** — 기존 문장·락(0건 문구, ⚠ 어긋남 문구, 신호 나이)을 자원 절 안으로 옮긴다:

```
랜딩 레인 0건(질의는 돌았다)                          ← len(l.Resources)==0
r1: 점유 <sid> 획득 3분전 · 줄 1.<sid>(행2·대기 3분전·신호 1분전◀점유) 2.…
r2: ⚠ 정합 어긋남: 점유자 <sid> 는 있는데 줄 행이 하나도 없다 · …
```
기존 단정 시험(`grep -rn "renderLane\|랜딩 레인" internal/mcpsrv/*_test.go`)을 열어 새 형태로 고친다 — **락의 의미(0건 문구 고정·⚠ 문구·신호 나이 존재)는 유지하고 표면형만 바꾼다.** `TestBoardDefaultOutputWithinBudget` 이 있으면 자원 1개(landing)일 때의 출력 길이가 늘지 않게 접두를 `landing:` 처럼 짧게 유지한다.

- [ ] **Step 5: `web/page.go` fillLane + `dashboard.gohtml`** — 현행 `fillLane`(1021-1063)을 읽고 같은 정보를 `ResourceLane` 루프로 옮긴다. 템플릿의 레인 `<h3>` 절 안을 자원별 `<h4>`(또는 목록)로. `web/render_test.go` 의 관련 단정(레인 절 존재·회수 폼 수)을 열어 유지되는지 확인 — 회수 폼은 줄 행 단위라 **개수 락(POST==3 등)이 변하지 않아야 한다.**

- [ ] **Step 6: 실행** — `go test ./internal/... -count=1` PASS.

- [ ] **Step 7: Commit** — `feat(flightdeck): 레인 화면이 자원별로 갈린다 — 보드·웹이 같은 LaneView 를 읽는다`

---

### Task 7: REST — resources 인자 + 읽기 전용 줄 조회

**Files:**
- Modify: `plugins/flightdeck/server/internal/api/handlers_landing.go` (landRequest.Resources · handleLandingQueue · 머리 주석)
- Modify: `plugins/flightdeck/server/internal/api/api.go:331-333` (라우트 한 줄)
- Test: `plugins/flightdeck/server/internal/api/` 의 landing 시험 파일(grep 으로 찾는다)에 추가

**Interfaces:**
- Produces:
  - `landRequest` 에 `Resources []string \`json:"resources"\`` — `LandModeAcquire` 갈래가 `service.LandInput.Resources` 로 넘긴다.
  - `GET /api/v1/landing/queue?project=…` → `service.LandingLane` 의 `LaneView` JSON. Task 9 의 클라이언트가 이 경로를 폴링한다.
  - `lane.*` publish detail 에 `"resources": len(res.Resources)` 추가.

- [ ] **Step 1: 실패하는 시험**

```go
// POST /api/v1/landing mode=acquire 에 resources 를 실으면 응답 resources 에 그대로 돌아온다.
func TestLandCarriesResources(t *testing.T)

// GET /api/v1/landing/queue?project=p 가 LaneView(resources 배열)를 낸다.
// 멱등 미들웨어(쓰기 전용)에 안 걸리고 Idempotency-Key 없이 200 이다.
func TestLandingQueueReadIsAPlainGet(t *testing.T)
```
(조립 방식은 이 패키지의 기존 landing 시험을 베낀다.)

- [ ] **Step 2: 실행해 실패 확인.**

- [ ] **Step 3: 구현**

`landRequest` 에 `Resources []string \`json:"resources"\``; acquire 갈래에 `Resources: req.Resources` 전달. publish 에 `"resources": len(res.Resources)` 추가.

핸들러:
```go
// handleLandingQueue 는 줄 전체의 읽기 전용 조회다 — fd lane wait 의 폴링이 이것을 친다.
//
// ★ 머리 주석의 "읽기(GET)가 없다"는 2026-08-12 에 좁혀졌다: **취득 판정**은 여전히
// POST(mode=acquire)만 한다. 이 GET 은 "차례로 보이는가"의 힌트일 뿐이고, 클라이언트는
// 캐시를 안 타는 직행(client.ReadFresh — Healthz 선례)으로만 읽는다. 캐시된 줄 상태로
// 취득을 판정하면 배타가 우회된다는 원판정은 그대로다.
func (s *server) handleLandingQueue(w http.ResponseWriter, r *http.Request) {
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	view, err := s.svc.LandingLane(r.Context(), project)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, view)
}
```
`api.go:333` 아래에:
```go
	mux.HandleFunc("GET /api/v1/landing/queue", s.handleLandingQueue)
```
`handlers_landing.go` 머리 주석의 "표면이 둘뿐이다" 문단을 셋으로 고치고 위 좁힘을 적는다.

- [ ] **Step 4: 실행** — `go test ./internal/api/ -count=1` PASS. REST 표를 기계로 재는 시험(`rest table` 계열 — c2140d3 커밋이 만든 것)이 있으면 빨갛다: **그 시험이 시키는 대로 DESIGN 의 REST 표에 행을 더한다**(576행 부근 — 01KZQE2V 겹침 주의, Global Constraints 참조).

- [ ] **Step 5: Commit** — `feat(flightdeck): REST 에 자원 축과 줄 읽기 전용 조회 — 취득 판정은 여전히 POST 하나다`

---

### Task 8: MCP — land 도구 resources 인자 + RenderLand 자원 표시

**Files:**
- Modify: `plugins/flightdeck/server/internal/mcpsrv/tools.go:141-150, 238-248` (스키마·landArgs)
- Modify: `plugins/flightdeck/server/internal/mcpsrv/mcpsrv.go:897-952` (toolLand)
- Modify: `plugins/flightdeck/server/internal/mcpsrv/render.go:1858-1915` (RenderLand)
- Modify: `plugins/flightdeck/server/internal/mcpsrv/backend.go` (be.Land 인터페이스에 이미 LandInput 을 넘기므로 시그니처 확인만)
- Test: `plugins/flightdeck/server/internal/mcpsrv/land_test.go` 에 추가

**Interfaces:**
- Consumes: Task 3 의 `LandInput.Resources`·`LandResult.Blockers`.
- Produces: `land` 도구 인자 `resources: []string`. **도구 수 7 불변** — `TestToolTableIsSeven` 이 지킨다(설명 90자 상한 주의).

- [ ] **Step 1: 실패하는 시험**

```go
// land(resources:["path:a.go"]) 가 서비스까지 그 집합으로 도착한다(가짜 백엔드로 캡처).
func TestToolLandForwardsResources(t *testing.T)

// waiting 렌더가 자원별 blocker 를 낸다: "r1 에서 2번째 · 점유 <sid> · 신호 …".
func TestRenderLandShowsPerResourceBlockers(t *testing.T)
```

- [ ] **Step 2: 실행해 실패 확인.**

- [ ] **Step 3: 구현**

`tools.go` — `landArgs` 에 `Resources []string \`json:"resources"\``; 스키마에:
```go
			"resources": arrStr("줄을 설 자원들. 비면 landing. 경로 자원은 path:<경로>"),
```
(`arrStr` 헬퍼가 없으면 이 파일의 기존 배열 인자(예: pick 의 item_ids)가 쓰는 헬퍼 이름을 grep 해 그것을 쓴다. 설명은 90자 이내.)

`mcpsrv.go` toolLand 의 default 갈래:
```go
	default:
		res, err = s.be.Land(ctx, service.LandInput{
			Project: s.id.ProjectID, SessionID: sessionID, Resources: a.Resources,
		})
```
`result`·`leave` 갈래에 `a.Resources` 가 함께 오면 거절한다(보고·이탈은 행 기준이라 자원 인자가 무의미하다 — 조용히 버리면 세션이 "r2 만 반납했다"고 믿는다):
```go
	if (result != "" || leave != "") && len(a.Resources) > 0 {
		return textResult(s.withTail(ctx, RenderRefusal("land",
			"resources 는 줄 서기(acquire)에만 성립한다 — 보고·이탈은 네 줄 행 전체에 걸린다",
			"반납·이탈은 행의 자원 집합 전부가 한 번에 움직인다(all-or-nothing 의 짝)."), tailOpts{}), true)
	}
```

`RenderLand` waiting 갈래 — Holder 한 줄 자리를 Blockers 루프로 교체(기존 두 문장 락 — lane-turn 안내 — 은 유지하되 `fd lane wait` 안내를 더한다. Task 13 에서 문구를 최종 확정하므로 여기서는 blocker 표시만):
```go
	case "waiting":
		fmt.Fprintf(&b, "land · 너는 %d번째다 (줄 행 %d · 자원 %s)\n",
			r.Position, r.RowID, strings.Join(r.Resources, " "))
		if len(r.Blockers) == 0 {
			b.WriteString("지금 막는 것이 안 보인다 — 다시 land 를 부르면 차례일 수 있다.\n")
		}
		for _, bl := range r.Blockers {
			switch {
			case bl.Holder != nil:
				fmt.Fprintf(&b, "%s: 점유 %s · 획득 %s 전", bl.Resource,
					ShortID(bl.Holder.SessionID), FormatAge(now.Sub(bl.Holder.AcquiredAt)))
				if bl.Holder.LastSignalAt != nil {
					fmt.Fprintf(&b, " · 마지막 신호 %s 전\n", FormatAge(now.Sub(*bl.Holder.LastSignalAt)))
				} else {
					b.WriteString(" · 마지막 신호 없음\n")
				}
			default:
				fmt.Fprintf(&b, "%s: %d번째 · 앞 줄 행 %d(%s)\n", bl.Resource, bl.Position,
					bl.FrontRowID, ShortID(bl.FrontSessionID))
			}
		}
		…기존 lane-turn 두 문장 유지…
```
turn 갈래 머리도 자원을 낸다: `"land · 네 차례다 — %s 를 쥐었다 (줄 행 %d)\n", strings.Join(r.Resources, " "), r.RowID` (Resources 가 비면 기존 문장 폴백 — 구서버 응답 호환).

- [ ] **Step 4: 실행** — `go test ./internal/mcpsrv/ -count=1` PASS. `TestToolTableIsSeven`(이름·순서·설명 상한)과 `render_lines` 계열이 초록인지 본다.

- [ ] **Step 5: Commit** — `feat(flightdeck): MCP land 에 자원 축 — waiting 이 자원별로 무엇이 막는지 말한다`

---

### Task 9: client.ReadFresh — 캐시를 안 타는 GET

**Files:**
- Modify: `plugins/flightdeck/server/cmd/fd/client.go` (Healthz 부근에 ReadFresh 추가)
- Test: `plugins/flightdeck/server/cmd/fd/client_test.go` 계열(기존 캐시 시험 파일을 grep 으로 찾는다)

**Interfaces:**
- Produces: `func (c *Client) ReadFresh(ctx context.Context, path string) ([]byte, error)` — Task 10 이 쓴다.

- [ ] **Step 1: 실패하는 시험**

```go
// ReadFresh 는 성공을 캐시에 안 넣고, 서버가 죽으면 캐시에서 안 꺼내고 그대로 오류다.
// (Client.Read 의 무조건 캐시와 정반대 — Healthz 와 같은 판정이다.)
func TestReadFreshNeverTouchesTheCache(t *testing.T) {
	// httptest 서버: 첫 호출 200 "live", 그 뒤 서버 Close.
	// ReadFresh → "live". Close 후 ReadFresh → 오류(캐시 재생이 아니라).
	// 같은 경로를 Read 로 부르면 캐시가 재생되는 것과 대조해 두 함수의 차이를 못박는다.
}
```

- [ ] **Step 2: 실행해 실패 확인** (ReadFresh 미정의).

- [ ] **Step 3: 구현** — `client.go` 의 `Healthz`(312-315 부근) 바로 아래. **`c.do` 의 실제 시그니처를 그 자리에서 읽고 맞춘다**(Healthz 가 부르는 모양 그대로):

```go
// ReadFresh 는 캐시를 전혀 안 거치는 GET 이다 — Healthz 와 같은 판정이다:
// "지금 줄이 어떤가"에 캐시로 답하면 그 질문 자체가 무의미해진다.
// fd lane wait 의 폴링 전용이다. 캐시된 줄 상태로 "내 차례"를 판정하면
// 배타가 깨지는 게 아니라 우회된다(Client.Read 주석의 그 사고).
// 아웃박스와도 무관하다 — GET 이라 잃을 쓰기가 없다.
func (c *Client) ReadFresh(ctx context.Context, path string) ([]byte, error) {
	// 본문 모양은 Healthz 를 그대로 따른다(c.do 직행 · 캐시 Put/Get 호출 0).
}
```

- [ ] **Step 4: 실행** — `go test ./cmd/fd/ -run TestReadFresh -count=1` PASS. 그리고 `go test ./cmd/fd/ -run TestCliDoWrite -count=1` — AST 관문이 `client.go` 안의 `c.do` 를 문제 삼지 않는지 확인(리시버가 `a.cli` 가 아니라 `c` 라 잡히지 않아야 정상).

- [ ] **Step 5: Commit** — `feat(flightdeck): ReadFresh — 줄 폴링이 캐시를 원리적으로 못 탄다`

---

### Task 10: fd lane wait

**Files:**
- Modify: `plugins/flightdeck/server/cmd/fd/cmds.go` (runLane 에 wait 갈래 + runLaneWait)
- Modify: `plugins/flightdeck/server/cmd/fd/wire.go` (landReq.Resources)
- Modify: `plugins/flightdeck/server/cmd/fd/main.go:53 부근` (도움말 한 줄)
- Modify: `plugins/flightdeck/server/cmd/fd/offline.go` (주석 한 줄 — 새 상수는 안 만든다: wait 의 쓰기는 CmdLandAcquire 그대로다)
- Test: `plugins/flightdeck/server/cmd/fd/lane_wait_test.go` (새 파일)

**Interfaces:**
- Consumes: `Client.ReadFresh`(Task 9) · `GET /api/v1/landing/queue`(Task 7) · `service.LaneView` JSON(Task 6) · `LandExitCode`(기존).
- Produces: `fd lane wait [--resource <이름>]… [--timeout 9m] [--stale 30m] [--interval 2s] [--cc-session <id>]`.

- [ ] **Step 1: 실패하는 시험** — `lane_wait_test.go`. 서버는 httptest 로 손수 조립한다(이 패키지의 seam 시험 방식 — `sse_seam_test.go`·`land` 계열 시험의 조립부를 베낀다). 시계·sleep 은 주입한다:

```go
// 판정 로직을 순수 함수로 뽑아 시험한다 — 폴링 루프 자체는 얇은 접착이다.
// myTurn(view, myRow, mySession): 모든 내 자원에서 entries[0].RowID==myRow && holder==nil
func TestLaneWaitTurnJudgement(t *testing.T)        // 차례 판정: 자원별 표를 돌린다
func TestLaneWaitStaleUsesSignalThenEnqueueAge(t *testing.T) // 신호 nil 이면 대기 시작 나이로
// 취득이 실제로 POST 로만 일어난다: 조회 N번 동안 landing POST 0번,
// 차례로 보인 뒤 정확히 1번 — 서버 핸들러 호출 계수로 단정.
func TestLaneWaitAcquiresOnlyWhenItLooksLikeMyTurn(t *testing.T)
// waiting 으로 끝나면(타임아웃) 종료코드 1 — LandExitCode 와 같은 규약.
func TestLaneWaitTimeoutExitsOne(t *testing.T)
```

- [ ] **Step 2: 실행해 실패 확인.**

- [ ] **Step 3: 구현** — `cmds.go` 의 `runLane` switch 에 `case "wait": return a.runLaneWait(ctx, args[1:], out)` 추가, help 문자열에 wait 한 줄 추가. 본체:

```go
// laneWaitDeps 는 폴링의 시계 이음매다 — 시험이 sleep 을 없애고 시각을 민다.
type laneWaitDeps struct {
	now   func() time.Time
	sleep func(time.Duration)
}

// runLaneWait 는 `fd lane wait` 다 — 줄에 서고, 차례가 될 때까지 **턴 안에서** 기다린다.
//
// ★ 대기의 통로는 읽기 전용 조회(ReadFresh)다. 취득의 정본은 여전히 land(POST) 하나다 —
// 조회는 "차례로 보인다"까지만 말하고, 그 순간 POST 가 트랜잭션 안에서 다시 판정한다.
// 조회가 낡아 틀렸어도 잃는 것은 POST 한 번이다(멱등 키를 고정하지 않는 기존 판정 —
// outbox.go 의 CmdLandAcquire 갈래).
//
// ★ 서버 미도달이면 그 자리에서 1 로 끝낸다. 캐시로 줄을 재생하면 배타가 우회된다
// (ReadFresh 가 그 통로 자체를 안 갖는다). 아웃박스에도 안 싣는다 — GET 이다.
//
// ★ --background 는 없다. 백그라운드는 부르는 쪽(하네스)이 이미 갖고 있고,
// fd 가 데몬화하면 수명·정리·중복 실행이 전부 새 문제가 된다(스펙 결정 표).
func (a *App) runLaneWait(ctx context.Context, args []string, out io.Writer) int {
	return a.runLaneWaitWith(ctx, args, out, laneWaitDeps{now: a.now, sleep: time.Sleep})
}

func (a *App) runLaneWaitWith(ctx context.Context, args []string, out io.Writer, d laneWaitDeps) int {
	fs := newFlagSet("lane wait")
	var resources stringListFlag // flag.Value 구현 — 반복 가능 --resource
	fs.Var(&resources, "resource", "줄을 설 자원(반복 가능). 비면 landing")
	timeout := fs.Duration("timeout", 9*time.Minute, "이 시간까지 차례가 안 오면 1로 끝낸다(다시 부르면 이어진다)")
	stale := fs.Duration("stale", 30*time.Minute, "막는 세션의 무신호가 이 나이를 넘으면 1로 끝내고 사람을 부른다")
	interval := fs.Duration("interval", 2*time.Second, "첫 폴링 간격(변화가 없으면 1.5배씩, 상한 10초)")
	session := fs.String("cc-session", "", "Claude Code 세션 id")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// ① 줄에 선다 — fd land 와 같은 쓰기 경로(열화 표·정체 규율을 그대로 탄다).
	sess, err := a.sessionID(ctx, *session)
	if err != nil {
		fmt.Fprintf(out, "lane wait 하지 못했다: %v\n", err)
		return 1
	}
	a.cli.Session = sess
	acquire := func() (service.LandResult, bool) {
		res, err := a.cli.Write(ctx, CmdLandAcquire, landingPath, landReq{
			Project: a.proj.ID, SessionID: sess, Mode: api.LandModeAcquire, Resources: resources,
		})
		if err != nil || !res.Sent {
			if err != nil {
				fmt.Fprintf(out, "land 하지 못했다: %v\n", err)
			} else {
				fmt.Fprintf(out, "%s: %s\n", res.Mode, res.Reason)
			}
			return service.LandResult{}, false
		}
		var lr service.LandResult
		if err := json.Unmarshal(res.Body, &lr); err != nil {
			fmt.Fprintf(out, "랜딩 응답 해석 실패: %v\n", err)
			return service.LandResult{}, false
		}
		return lr, true
	}
	lr, ok := acquire()
	if !ok {
		return 1
	}
	if lr.State != "waiting" {
		fmt.Fprintln(out, strings.TrimRight(mcpsrv.RenderLand(lr, d.now()), "\n"))
		return LandExitCode(lr.State)
	}
	myRow := lr.RowID
	fmt.Fprintln(out, strings.TrimRight(mcpsrv.RenderLand(lr, d.now()), "\n"))

	// ② 차례까지 조회 폴링. 변화가 있으면 한 줄 내고 간격을 처음으로 되돌린다.
	deadline := d.now().Add(*timeout)
	wait := *interval
	lastLine := ""
	for {
		if d.now().After(deadline) {
			fmt.Fprintf(out, "아직 차례가 아니다(%s 경과) — fd lane wait 를 다시 불러라.\n", timeout)
			return 1
		}
		d.sleep(wait)
		body, rerr := a.cli.ReadFresh(ctx, landingQueuePath+"?project="+urlQuery(a.proj.ID))
		if rerr != nil {
			fmt.Fprintf(out, "줄을 읽지 못했다(%v) — 서버가 살아 있어야 대기가 성립한다. 캐시로는 판정하지 않는다.\n", rerr)
			return 1
		}
		var view service.LaneView
		if err := json.Unmarshal(body, &view); err != nil {
			fmt.Fprintf(out, "줄 응답 해석 실패: %v\n", err)
			return 1
		}
		st := judgeLaneWait(view, myRow, sess, d.now())
		if st.line != lastLine && st.line != "" {
			fmt.Fprintln(out, st.line)
			lastLine = st.line
			wait = *interval // 변화가 있었다 — 간격을 되돌린다
		} else if wait = wait * 3 / 2; wait > 10*time.Second {
			wait = 10 * time.Second
		}
		if st.myTurn {
			lr, ok := acquire() // 정본 판정 — 조회가 낡았으면 여기서 waiting 으로 돌아온다
			if !ok {
				return 1
			}
			fmt.Fprintln(out, strings.TrimRight(mcpsrv.RenderLand(lr, d.now()), "\n"))
			if lr.State != "waiting" {
				return LandExitCode(lr.State)
			}
			myRow = lr.RowID
		}
		if st.staleFor > *stale {
			fmt.Fprintf(out, "%s 가 %s 무신호다. 자동 회수는 없다 — 사람이 판정한다:\n  fd lane release --row %d --reason \"…\"\n그대로 더 기다리려면 fd lane wait 를 다시 불러라.\n",
				st.staleWho, st.staleFor.Round(time.Minute), st.staleRow)
			return 1
		}
	}
}

// laneWaitState 는 조회 한 번의 판정이다. judgeLaneWait 는 순수 함수다 — 시험은 이것만 찌른다.
type laneWaitState struct {
	myTurn   bool          // 모든 내 자원에서 entries[0].RowID==myRow 이고 holder 가 없다
	line     string        // 사람이 읽는 현황 한 줄(변화 감지용 — 같으면 안 낸다)
	staleWho string        // 가장 오래 막는 상대(세션 짧은 id 와 자원)
	staleFor time.Duration // 그 상대의 무신호 나이(신호가 없으면 대기 시작 나이)
	staleRow int64         // 회수 대상 줄 행
}

func judgeLaneWait(view service.LaneView, myRow int64, mySession string, now time.Time) laneWaitState {
	// 내 자원 = view 에서 entries 에 myRow 가 있는 자원들. 각 자원에서:
	//   entries[0].RowID == myRow && holder == nil        → 그 자원은 통과
	//   holder != nil && holder.SessionID == mySession    → 통과(재진입)
	//   그 밖 → 막힘: 막는 이는 holder ?? entries[0]. 신호 nil 이면 EnqueuedAt 나이.
	// line 은 "r1: 2번째·점유 01KZ…·신호 3분 전 | r2: 1번째" 모양으로 자원별 조각을 잇는다.
	// 전부 통과면 myTurn=true. 구현은 이 계약 그대로 — 시험 표가 정본이다.
}
```
`landingQueuePath = "/api/v1/landing/queue"` 상수와 `urlQuery`(기존 헬퍼 grep — 없으면 `url.QueryEscape`)를 wire.go 에 둔다. `stringListFlag` 는 `[]string` 에 `String()/Set()` 을 구현한 지역 타입. `wire.go` 의 `landReq` 에 `Resources []string \`json:"resources,omitempty"\`` 추가.

`offline.go` 의 `CmdLandAcquire` 상수 주석에 한 줄 추가:
```go
	// CmdLandAcquire 는 줄 서기 · 내 자리 재확인이다(mode=acquire). fd lane wait 의
	// 취득도 이 이름으로 온다 — wait 전용 쓰기 명령은 없다(조회는 ReadFresh 라 이 표 밖이다).
```

`main.go` 도움말(`:53` 부근)에:
```
  fd lane wait [--resource <이름>]…            줄을 서고 차례까지 턴 안에서 기다린다(폴링은 조회, 취득은 land)
```

- [ ] **Step 4: 실행** — `go test ./cmd/fd/ -count=1` PASS. `write_cmd_table_coverage_test.go` 가 새 Write 호출(`CmdLandAcquire` — 기존 표에 있음)을 문제 삼지 않는지 확인.

- [ ] **Step 5: Commit** — `feat(flightdeck): fd lane wait — 대기 세션이 턴 안에서 차례를 기다려 사람 없이 이어간다`

---

### Task 11: 서버 — 대화 단위 라이프사이클 판정

**Files:**
- Create: `plugins/flightdeck/server/internal/store/conversation.go`
- Modify: `plugins/flightdeck/server/internal/service/prescribe.go` (PrescribeResult.Lifecycle + Prescriptions 에서 판정)
- Create: `plugins/flightdeck/server/internal/service/lifecycle.go` (판정 순수 함수)
- Test: `plugins/flightdeck/server/internal/store/conversation_test.go` · `plugins/flightdeck/server/internal/service/lifecycle_test.go`

**Interfaces:**
- Produces:
```go
// store
type ConvLifecycle struct {
	SessionIDs   []string
	EarliestOpen time.Time         // 대화 카드들의 가장 이른 opened_at — 관측 구간의 하한
	LiveClaims   []string          // 살아 있는 선점 중 항목이 아직 done/dropped 가 아닌 것의 item id
	LaneRow      *model.LandingRow // 대화의 살아 있는 줄 행(자원 포함). 없으면 nil
	HeldRes      []string          // 대화가 쥔 자원 이름들
	DoneItems    []string          // EarliestOpen 이후 이 대화가 done 으로 닫은 항목(롤백 제외)
	EverEnqueued bool              // EarliestOpen 이후 줄에 선 적(닫힌 행 포함)이 있나
}
func (s *Store) ConversationLifecycle(ctx context.Context, project, sessionID string) (ConvLifecycle, error)

// service
type LifecycleGate struct {
	Stage  string `json:"stage"`  // lane-wait | finish | land
	Reason string `json:"reason"`
}
func judgeLifecycleGate(c store.ConvLifecycle) *LifecycleGate // 순수 함수
```
`PrescribeResult` 에 `Lifecycle *LifecycleGate \`json:"lifecycle,omitempty"\`` — Task 12 의 hookStop 이 이 필드를 읽는다(추가 왕복 0: 훅이 이미 이 POST 를 부른다).

- [ ] **Step 1: 실패하는 시험 — store**

```go
// 대화 단위: 같은 (machine, cc_session_id) 의 카드 둘 중 하나가 선점을,
// 다른 하나가 줄 행을 가져도 한 ConvLifecycle 로 모인다 — 카드 갈림(DESIGN:1645)을 넘는 자리다.
func TestConversationLifecycleSpansSiblingCards(t *testing.T)

// done 항목은 EarliestOpen 이후의 item.finish(mode=done, tx!=rolled_back)만 센다.
// 이전 대화가 닫은 항목은 안 들어온다.
func TestConversationLifecycleWindowsDoneItemsByOpenedAt(t *testing.T)

// cc_session_id 가 빈 카드는 자기 하나로 접는다(형제 폭발 방지 — AckReach 의 폴백과 같은 판정).
func TestConversationLifecycleFallsBackToSelfWithoutCC(t *testing.T)
```

- [ ] **Step 2: 실행해 실패 확인.**

- [ ] **Step 3: store 구현** — `internal/store/conversation.go`. 대화 접기는 `prescribe_reach.go` 의 `machine_id || char(31) || cc_session_id` 판정을 따르되, 여기는 한 대화만 보므로 형제 목록 질의로 편다:

```go
func (s *Store) ConversationLifecycle(ctx context.Context, project, sessionID string) (ConvLifecycle, error) {
	var out ConvLifecycle
	// ① 형제 카드: 같은 (machine_id, cc_session_id). cc 가 비면 자기 하나다.
	rows, err := s.db.QueryContext(ctx, `
		SELECT s2.id, s2.opened_at FROM session s1
		JOIN session s2 ON s2.machine_id = s1.machine_id
		 AND s2.cc_session_id = s1.cc_session_id AND s1.cc_session_id <> ''
		WHERE s1.id = ?
		UNION
		SELECT id, opened_at FROM session WHERE id = ?`, sessionID, sessionID)
	// … 스캔: SessionIDs, EarliestOpen(min opened_at) …

	// 이하 전부 SessionIDs 의 IN (…) 자리표를 만들어 돈다(attachResources 의 marks 방식).
	// ② 살아 있는 선점 + 항목이 여전히 열려 있는 것:
	//   SELECT c.item_id FROM claim c JOIN item i ON i.project=c.project AND i.id=c.item_id
	//   WHERE c.project=? AND c.released_at IS NULL AND c.session_id IN (…)
	//     AND i.state NOT IN ('done','dropped')
	// ③ 줄 행: SELECT <landingCols> FROM landing_queue WHERE project=? AND left_at IS NULL
	//     AND session_id IN (…) LIMIT 1 → attachResources
	//   EverEnqueued: SELECT EXISTS(SELECT 1 FROM landing_queue WHERE project=?
	//     AND session_id IN (…) AND enqueued_at >= ?)
	// ④ 쥔 자원: SELECT resource FROM resource_hold WHERE project=? AND released_at IS NULL
	//     AND session_id IN (…)
	// ⑤ done 항목: SELECT payload FROM event WHERE project=? AND kind='item.finish'
	//     AND session_id IN (…) AND at >= ?
	//   payload 해석: item · mode=='done' · tx != 'rolled_back' (QueueReproduction 의
	//   payload 필드명을 grep 해 그대로 쓴다 — 두 벌로 적지 않는다).
}
```
(각 질의의 정확한 컬럼은 `schema.sql` 의 해당 표를 열고 맞춘다 — claim(project,item_id,session_id,at,released_at) · resource_hold(project,resource,session_id,released_at) 은 위에서 실측했다.)

- [ ] **Step 4: 실패하는 시험 — service 판정** (`lifecycle_test.go`, 순수 함수라 표 시험):

```go
func TestJudgeLifecycleGate(t *testing.T) {
	cases := []struct{ name string; in store.ConvLifecycle; wantStage string }{
		{"줄에 섰는데 하나도 안 쥠 → lane-wait", …, "lane-wait"},
		{"줄 행의 자원 일부만 쥠 → lane-wait 아님(부분 점유는 어긋남 — block 이 판정할 자리가 아니다)", …, ""},
		{"전부 쥠 → 통과(쥔 채 끝내는 것은 block 안 한다)", …, ""},
		{"선점 있고 항목 열림 → finish", …, "finish"},
		{"done 닫았고 줄 선 적 없음 → land", …, "land"},
		{"done 닫았고 줄 선 적 있음 → 통과", …, "land 아님"},
		{"dropped 만 닫음 → 통과", …, ""},   // DoneItems 가 done 만 담으므로 자연 통과
		{"아무것도 없음 → nil", …, ""},
	}
}
```

- [ ] **Step 5: service 구현** — `internal/service/lifecycle.go`:

```go
// judgeLifecycleGate 는 대화 하나의 라이프사이클 단계를 본다. 순수 함수다.
//
// ★ 위에서부터 먼저 맞는 것 하나만 낸다 — 셋이 겹치면(줄에 서 있으면서 선점도 있다)
// 가장 급한 것은 줄이다: 차례를 흘리면 뒤 전원이 선다.
//
// ★ 쥔 자원이 하나라도 있으면 lane-wait 를 안 낸다. 전부 쥔 것은 정당한 대화 중일 수
// 있고(스펙 §4 — 랜딩 중 사람과 상의), 일부만 쥔 것은 all-or-nothing 이 깨진 어긋남이라
// block 이 아니라 사람의 회수가 푸는 자리다.
func judgeLifecycleGate(c store.ConvLifecycle) *LifecycleGate {
	if c.LaneRow != nil && len(c.HeldRes) == 0 {
		res := strings.Join(c.LaneRow.Resources, " ")
		return &LifecycleGate{Stage: "lane-wait", Reason: fmt.Sprintf(
			"자원 %s 줄에 서 있다(행 %d). 지금 턴을 끝내면 차례가 와도 못 받는다 — "+
				"`fd lane wait` 로 이어라. 줄에서 빠지려면 land(leave:\"사유\") 다.",
			res, c.LaneRow.ID)}
	}
	if len(c.LiveClaims) > 0 {
		return &LifecycleGate{Stage: "finish", Reason: fmt.Sprintf(
			"선점 중인 항목 %s 가 아직 열려 있다. 끝났으면 finish 로 닫아라(판단·후속·반납이 한 호출이다). "+
				"끝나지 않았으면 이어서 하라 — 이 알림은 턴 끝마다 온다.",
			strings.Join(c.LiveClaims, " "))}
	}
	if len(c.DoneItems) > 0 && !c.EverEnqueued {
		return &LifecycleGate{Stage: "land", Reason: fmt.Sprintf(
			"%s 를 done 으로 닫았는데 이 대화는 랜딩 줄에 선 기록이 없다. "+
				"land 로 줄을 서고 차례에 랜딩해라 — 기다림은 `fd lane wait` 가 잇는다.",
			strings.Join(c.DoneItems, " "))}
	}
	return nil
}
```
`Prescriptions`(prescribe.go)의 반환 조립 직전에:
```go
	if conv, cerr := s.st.ConversationLifecycle(ctx, sess.Project, sessionID); cerr != nil {
		// 판정 실패가 처방 전체를 죽이면 안 된다 — laneTurnRow 와 같은 관용(WARN 뒤 계속).
		s.log.WarnContext(ctx, "라이프사이클 판정 실패", "session_id", sessionID, "error", cerr.Error())
	} else {
		result.Lifecycle = judgeLifecycleGate(conv)
	}
```
(`result` 는 그 함수의 실제 반환 변수명에 맞춘다.)

- [ ] **Step 6: 실행** — `go test ./internal/store/ ./internal/service/ -count=1` PASS.

- [ ] **Step 7: Commit** — `feat(flightdeck): 처방 응답이 대화 단위 라이프사이클 단계를 싣는다 — 카드 갈림을 AckReach 의 단위로 넘는다`

---

### Task 12: hookStop — decision:block

**Files:**
- Modify: `plugins/flightdeck/server/cmd/fd/hook.go:92-114` (emitBlock 추가) · `:534-583` (hookStop)
- Test: `plugins/flightdeck/server/cmd/fd/hook_stop_test.go` (추가)

**Interfaces:**
- Consumes: Task 11 의 `lifecycle` 필드.
- Produces: Stop 훅 stdout 에 `{"decision":"block","reason":"…"}` — Claude Code 가 턴을 되살린다.

- [ ] **Step 1: 실패하는 시험** — `hook_stop_test.go` 의 기존 조립(가짜 서버로 hookStop 을 부르는 방식)을 베껴서:

```go
// 처방 응답에 lifecycle 이 실려 오면 decision:block 이 나간다. 처방 문구는 reason 꼬리에 붙는다.
func TestHookStopBlocksOnLifecycleGate(t *testing.T)

// stop_hook_active=true 면 lifecycle 이 있어도 **아무것도** 안 나간다 — 이 가드가
// 루프 방벽의 전부다(억제표는 방벽이 아니다 — hook.go:525-533 이 명시로 기각했다).
func TestHookStopNeverBlocksItsOwnTurn(t *testing.T)

// 페이로드 해석 실패면 block 도 additionalContext 도 없다(fail-close 유지).
func TestHookStopStaysSilentOnParseFailure(t *testing.T)

// lifecycle 이 없으면 기존 그대로 additionalContext 처방만 나간다(회귀 방지).
func TestHookStopStillEmitsPrescriptionsWithoutGate(t *testing.T)
```

- [ ] **Step 2: 실행해 실패 확인.**

- [ ] **Step 3: 구현**

`hook.go` — `emitContext` 아래에:
```go
// emitBlock 은 Stop 훅의 decision:block 이다 — 턴을 끝내려는 모델을 reason 과 함께 되살린다.
//
// ★ 무한루프 방벽은 이 함수가 아니라 **호출자의 stop_hook_active 가드**다. block 이 만든
// 턴이 끝나면 Stop 이 stop_hook_active=true 로 다시 오고, hookStop 은 그 자리에서 반환한다 —
// 그래서 block 은 사람이 몰던 턴의 끝마다 최대 한 번이다. 2026-08-04 의 additionalContext
// 무한루프(hook.go 위 ★★ 실측)와 같은 사슬이고 같은 가드가 끊는다.
// (세션×키) 억제표를 방벽으로 쓰지 않는 이유는 ★★★ 문단이 이미 적었다 — 그것은
// 우연한 edge-triggering 이라 방벽이 못 되고, 여기서는 지속(매 프롬프트 한 번)이 목적이라
// 애초에 억제가 반대 방향이다.
func emitBlock(out io.Writer, reason string) {
	buf, err := json.Marshal(struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}{Decision: "block", Reason: reason})
	if err != nil {
		slog.Error("hook block 직렬화 실패", "error", err.Error())
		return
	}
	fmt.Fprintln(out, string(buf))
}
```
(`slog` 사용부는 이 파일의 기존 로그 관용(`a.log`)과 다르면 그쪽에 맞춘다 — emitContext 의 오류 갈래를 베낀다.)

`hookStop` 의 응답 구조체와 꼬리:
```go
	var got struct {
		Shown     []PrescriptionLine `json:"shown"`
		Folded    int                `json:"folded"`
		Lifecycle *struct {
			Stage  string `json:"stage"`
			Reason string `json:"reason"`
		} `json:"lifecycle"`
	}
	if err := json.Unmarshal(wr.Body, &got); err != nil { /* 기존 그대로 */ }

	text := RenderPrescriptions(got.Shown, got.Folded)
	if got.Lifecycle != nil && strings.TrimSpace(got.Lifecycle.Reason) != "" {
		reason := got.Lifecycle.Reason
		if text != "" {
			reason += "\n\n" + text // 처방을 잃지 않는다 — block 턴에서 additionalContext 는 안 나간다
		}
		emitBlock(out, reason)
		return
	}
	if text != "" {
		emitContext(out, "Stop", text)
	}
```

- [ ] **Step 4: 실행** — `go test ./cmd/fd/ -count=1` PASS.

- [ ] **Step 5: Commit** — `feat(flightdeck): Stop 훅이 라이프사이클 단계를 block 으로 잇는다 — 방벽은 stop_hook_active 하나다`

---

### Task 13: 랜딩의 문 — instructions · pick 꼬리 · RenderLand 대기 안내

**Files:**
- Modify: `plugins/flightdeck/server/internal/mcpsrv/protocol.go:26-28` (Instructions)
- Modify: `plugins/flightdeck/server/internal/mcpsrv/render.go` (RenderPick 워크트리 절 꼬리 + RenderLand waiting 꼬리)
- Test: `plugins/flightdeck/server/internal/mcpsrv/protocol_test.go` · `render_test.go` 계열 (기존 락 갱신)

**Interfaces:** 없음(문구만).

- [ ] **Step 1: 시험 먼저 고친다** — `protocol_test.go` 에서 Instructions 본문을 단정하는 시험(grep `Instructions`)을 찾아 새 문구로 바꾸고 실행해 **빨간 것을 확인**한다(문구 락이 없으면 이 스텝은 새 단정을 더한다: `strings.Contains(Instructions, "land")`).

- [ ] **Step 2: 구현**

`protocol.go`:
```go
const Instructions = "작업은 `pick`, 판단은 `note`, 끝나면 `finish`, 랜딩 전에 `land` 로 줄을 선다. 락은 없다.\n" +
	/* 나머지 두 줄은 현행 그대로 */
```
(300자 상한을 시험이 지킨다 — 초과하면 문구를 줄이지 상한을 늘리지 마라.)

`RenderPick` — 워크트리 준비 명령(grep `git worktree add` in render.go)을 내는 절의 꼬리에:
```go
	b.WriteString("끝나면: finish → land 로 줄 서기 → 차례에 랜딩. 기다림은 `fd lane wait` 가 턴 안에서 잇는다.\n")
```

`RenderLand` waiting 꼬리의 둘째 문장을 교체(첫 문장 — lane-turn 처방 안내 — 은 유지):
```go
		b.WriteString("한 번 지나가면 같은 줄 행에는 다시 안 온다 — `fd lane wait` 가 턴 안에서 차례까지 기다린다(취득은 land 가 다시 판정한다).\n")
```
기존 그 문장을 단정하는 시험(grep `"land 를 다시 불러"` 등)을 찾아 함께 고친다.

- [ ] **Step 3: 실행** — `go test ./internal/mcpsrv/ -count=1` PASS.

- [ ] **Step 4: Commit** — `feat(flightdeck): 랜딩의 문 셋 — instructions·pick 꼬리·대기 안내. lane-turn 0건의 원인은 문이 없던 것이다`

---

### Task 14: R≥1 기각 판정 락 + DESIGN 정리 + 랜딩 관문

**Files:**
- Modify: `plugins/flightdeck/server/internal/mcpsrv/render_finish_balance_test.go` (기각 판정 시험)
- Modify: `plugins/flightdeck/DESIGN.md` (§6 훅 표 Stop 행 · land 도구 설명 행 · REST 표 — Task 7 에서 이미 했다면 확인만)
- Test: 전 관문.

- [ ] **Step 1: R≥1 기각 시험** — `render_finish_balance_test.go` 의 기존 픽스처(R=1.30 을 만드는 QueueBalance 조립)를 베껴서:

```go
// R≥1 을 발화 조건으로 삼는 행동 요구가 없다는 판정(render.go:1480-1483 — 전 관측창에서
// 상시 참이라 판별력 0 · 도입 당일 굿하트 실물 1건)을 시험으로 잠근다.
// 지금까지 이 판정의 방벽은 주석 한 줄뿐이었다 — 주석은 다음 개정에서 지워져도 관문이 안 운다.
func TestRateAboveOneAddsNoActionDemand(t *testing.T) {
	for _, rate := range []float64{1.30, 2.00} {
		got := /* 그 rate 의 finish 렌더 */
		for _, banned := range []string{"줄여라", "만들지 마라", "등록하지 마라", "경고", "⚠"} {
			if strings.Contains(got, banned) {
				t.Fatalf("R=%.2f 렌더에 행동 요구 %q 가 붙었다 — R≥1 조건은 기각된 판정이다(render.go 참조)", rate, banned)
			}
		}
		if !strings.Contains(got, "큐가 준다면 R<1 이어야 한다") {
			t.Fatalf("R=%.2f 에서 기준 서술이 사라졌다", rate)
		}
	}
}
```

- [ ] **Step 2: 실행** — 초록이어야 한다(현행이 이미 그렇다 — 이 시험은 회귀 방지 락이다). 빨갛다면 누가 이미 경고를 넣은 것이므로 **시험이 아니라 그 문구를** 지운다.

- [ ] **Step 3: DESIGN.md** — §6 훅 표(683행 부근)의 Stop 행을 갱신:
```
| Stop | — | fd hook stop → 처방(발화 5조건, 전이 1회) 주입 + 라이프사이클 단계면 decision:block(대화 단위 판정 · 방벽은 stop_hook_active). **fail-open, 3초** |
```
REST 표에 `GET /api/v1/landing/queue` 행이 들어갔는지 확인(Task 7). land 도구를 설명하는 행이 있으면 resources 인자를 반영.

- [ ] **Step 4: 전 관문** — 다섯 줄을 전부, 모듈 루트에서:

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-shared-resource-queue-and-lifecycle-gates/plugins/flightdeck/server && pwd
gofmt -l .                                   # 무출력이어야 한다(cwd 를 pwd 로 확인했다)
go vet ./...
GOOS=windows GOARCH=amd64 go vet ./...
GOOS=darwin GOARCH=arm64 go vet ./...
go test ./internal/... ./cmd/fd/ -count=1
```
Expected: gofmt 무출력 · vet 셋 무출력 · 시험 전건 PASS.

- [ ] **Step 5: Commit** — `test(flightdeck): R≥1 기각 판정을 시험으로 잠근다 + DESIGN 훅·REST 표 정리`

---

## Self-Review 기록

- **스펙 커버리지**: 설계 A(Task 1-6) · B(Task 7·9·10) · C(Task 11·12) · D(Task 13) · §8 시험 락(Task 1·14). 스펙의 시험 목록 22건이 Task 2·3·4·5·6·7·8·9·10·11·12·14 의 시험명으로 전부 대응된다.
- **타입 일관성**: `LandInput.Resources` → `landRequest.Resources` → `landReq.Resources` → `landArgs.Resources` 네 벌이 전부 `[]string`/`resources`. `LaneView{Resources []ResourceLane}` 는 Task 6 정의를 Task 7(JSON)·10(파싱)이 그대로 쓴다. `LifecycleGate{Stage,Reason}` 은 Task 11 정의를 Task 12 가 익명 구조체로 읽는다(wire 호환).
- **미정 지점(태스크가 현장에서 맞출 것)**: `c.do` 시그니처(Task 9 — Healthz 를 베낀다) · `ListHeld` 원소 필드명(Task 6) · `item.finish` payload 필드명(Task 11 — QueueReproduction 을 베낀다) · 기존 시험의 정확한 파일명(각 태스크의 grep 지시). 전부 "어디를 열어 무엇을 베낄지"가 지정돼 있다.
