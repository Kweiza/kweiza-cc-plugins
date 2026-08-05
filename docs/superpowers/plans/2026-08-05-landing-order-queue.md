# 랜딩 순서 큐 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 서버가 "지금 랜딩해도 된다"를 순서대로 내주고, 그 줄을 사람이 볼 수 있고 풀 수 있게 만든다.

**Architecture:** 배타는 기존 `resource_hold` 의 부분 유니크 인덱스가 그대로 지킨다. 새 표 `landing_queue` 는 **순서만** 갖고 점유 여부를 갖지 않는다("쥐었나"는 `HeldBy(project,"landing")` 로만 파생). 순서 집행 지점은 `FrontLandingRow().ID == 내 ID` 비교 하나다. 회수 대상은 레인이 아니라 줄 행이라 점유 중이든 대기 중이든 같은 문법으로 빠진다.

**Tech Stack:** Go · SQLite(`_txlock=immediate`) · 내장 마이그레이션(`internal/store/migrations/NNN_*.sql`) · MCP stdio · net/http `ServeMux` · `html/template`

스펙: `docs/superpowers/specs/2026-08-05-landing-order-queue-design.md`

---

## Global Constraints

이 절의 모든 줄은 **모든 태스크의 요구사항에 암묵적으로 포함된다.**

1. **`schema.sql` 을 고치지 않는다.** 새 표는 `internal/store/migrations/003_landing_queue.sql` 이고 `store.go` 세 자리를 함께 고친다. 근거: `schema.sql:1-6` 이 명시적으로 금지하고, 표를 더한 유일한 선례 커밋 `523b21d` 의 diff 가 그것을 증명한다(`002_idempotency.sql` 신규 + `schema.sql` 은 주석만).
2. **자동 회수도 만료도 만들지 않는다.** `store/resource.go:14-19` 의 규율. 나이는 숫자로만 낸다.
3. **`granted_at`·`seq`·`item_id` 컬럼을 만들지 않는다.** 각각 `resource_hold` 의 사본 · `id` 의 사본 · 선점의 사본이다.
4. **Tx 메서드 + Store 래퍼 쌍**이 쓰기의 관용이다. 래퍼 주석은 한 줄 고정: `// X 는 단발 트랜잭션으로 감싼 것이다.`(`resource.go:200-203`) 읽기는 `s.db` 직행이고, Tx 안팎에서 같은 질의가 필요하면 `func x(ctx, q dbtx, …)` 자유 함수를 두고 둘이 각각 부른다(`resource.go:146` `heldBy` 가 선례).
5. **시각**: 저장 `fmtTime`, 자기가 만들어 돌려줄 값 `nowStamp()`, 읽기 `parseTime`/`parseNullTime`.
6. **오류**: 쓰기는 `writeErr(err, writeTarget{…}, "…실패(project=%q …=%q)", clip(...), …)` 를 지난다. 없음은 `notFound(NFxxx, …)`/`notFoundNote(...)`. UPDATE 뒤 0행은 `affectedOne(res, NFxxx, …)`. 외부 문자열은 전부 `clip(s, 64)`(경로·본문 200).
7. **다중 조건 판정은 불리언이 아니라 사유를 돌려주는 순수 함수로 뺀다**(`ValidateHolder` 가 본). 시험이 그 함수를 직접 부른다.
8. **시험은 실물 SQLite 로 돈다.** 목 금지. 헬퍼는 `store_test.go` 의 `newStore(t)`·`seed`·`mustSession`·`mustItem`.
9. **대조 전제를 먼저 단정한다.** `"전제가 깨졌다 — …"` 로 `t.Fatalf` 한 뒤 본 판정을 둔다(`store_test.go:158-166` 이 그 규율이 없어서 났던 사고를 적는다).
10. **레포를 더럽히지 않는다.** 태스크마다 커밋하고, 커밋 사이에 `git status --short` 가 비어야 한다.

### 이름 — 계층 간 유일한 합의 지점 (정합성 검토가 13건의 충돌을 잡았다. 아래가 정본이다)

| 것 | 정본 이름 | 소유 계층 |
|---|---|---|
| 이탈 종류 열거 | `model.LandingLeftOK/Fail/Leave/Finish/Force` (`model.LandingLeftKind`) | model **하나만.** service 에 두 벌 만들지 마라 |
| 줄 행 | `model.LandingRow{ID, Project, SessionID, EnqueuedAt, LeftAt *time.Time, LeftKind, LeftDetail}` | model |
| 없음 좌표 | `store.NFLiveLandingRow` | store |
| 충돌 대상 | `store.TargetLandingQueue` | store |
| 서비스 파일 | `internal/service/landing.go` **하나.** `land.go` 를 따로 만들지 마라 | service |
| 보드 뷰 | 타입 `service.LaneView`, 필드 `BoardView.Lane *LaneView` — **nil = 안 읽었다, 빈 슬라이스 = 0건** | service |
| 백엔드 | `mcpsrv.Backend` 에 `Land`·`LandReport`·`LandLeave` 셋 | mcpsrv |
| REST 경로 | `POST /api/v1/landing` · `POST /api/v1/landing/rows/{id}/release` | api |
| 열화 명령 상수 | `CmdLandAcquire`·`CmdLandReport`·`CmdLandLeave`·`CmdLaneRelease` — **문자열 리터럴 금지**, 상수를 import 해 쓴다 | cmd/fd |
| 응답 계약 | `service.LandResult` 의 json 태그가 REST 계약이자 CLI 파싱 대상이다(`respond.go:20-23` 이 그대로 직렬화한다) | service |

### 단계마다 반드시 같은 커밋에 들어가야 하는 강제 동반 변경

전수/개수 락이라 **하나만 빠져도 그 자리에서 빨강**이다. 정합성 검토가 실물로 확인했다.

- `internal/api/errors.go` 두 표 — `notFoundGuidance` 와 `conflictWordTable`. `notfound_test.go:165` · `conflict_test.go:109` 가 `store.NotFoundKinds()`/`store.ConflictTargets()` 로 전수 확인한다. conflict 쪽은 **역방향**(목록에 없는 키가 표에 남으면 죽은 코드)도 본다.
- `internal/store/migrate_test.go` 의 `makeV1DB`(:101-109) — DROP 목록에 `landing_queue` 인덱스 둘과 표를 추가. 안 하면 재열기에서 003 이 `table already exists` 로 죽는다.
- `internal/store/rollback_test.go`(:89-92) — 깨뜨린 증분 스텁을 한 단이 아니라 `BaseSchemaVersion+1 … SchemaVersion` 전 구간으로. 안 하면 `UpgradeSteps` 가 적용 전에 거절해 시험이 보려던 경로에 안 들어간다.
- mcpsrv 개수 락 셋 — `protocol_test.go:62` · `add_coordinate_test.go:67` · `server_test.go:244`.
- `internal/mcpsrv/serial_test.go` 의 `serialProbe`(`var _ Backend` 가 :141) — 새 세 메서드를 감싼다.

---

# 단계 ① — 레인이 실제로 돈다

**끝났을 때 성립하는 것:** 세션이 `land()` 로 줄을 서고 차례를 받고 `land(result:…)` 로 놓는다. 보드가 줄을 낸다. 물린 레인을 사람이 `fd lane release` 로 푼다.

**왜 `fd lane release` 가 여기 있나:** 오늘 자원 점유를 푸는 프로덕션 표면이 **0건**이고 자동 만료도 없다. 세션 정체가 `(machine, worktree, cc_session_id)` 라 **죽은 세션 명의로 `land(leave)` 를 부를 방법이 없다.** 레인은 프로젝트당 하나뿐이라 한 번 물리면 그 프로젝트의 랜딩이 전원 정지하고, 복구가 `sqlite3` 직접 UPDATE 뿐이 된다 — 판단 한 줄 안 남는 경로다. 웹 폼·미들웨어·출처 대조는 ② 에 남겨도 되지만 **탈출구는 ① 에 있어야 한다.**

---

### Task 1: 마이그레이션 003 — 표를 만들고 바이너리에 물린다

**Files:**
- Create: `plugins/flightdeck/server/internal/store/migrations/003_landing_queue.sql`
- Modify: `plugins/flightdeck/server/internal/store/store.go` (세 자리: `:35` 아래 · `:40` · `:61-63`)
- Modify: `plugins/flightdeck/server/internal/store/migrate_test.go:101-109, 143-145, 162-164`
- Modify: `plugins/flightdeck/server/internal/store/rollback_test.go:89-92`

**Interfaces:**
- Produces: 표 `landing_queue`, `store.SchemaVersion == 3`

- [ ] **Step 1: 증분 파일을 만든다**

`internal/store/migrations/003_landing_queue.sql`:

```sql
-- 003 · 랜딩 순서 큐 (schema_version 2 → 3)
--
-- ★ id 가 곧 순번이다. 별도 발번(counter)을 두지 않는 이유: 발번기는 뒤따르는 삽입이 거절되면
--   번호만 소각되는데 그 번호를 회수하는 함수가 이 레포에 없다. id 는 같은 INSERT 안에서 원자적이다.
--
-- ★ granted_at 이 없다. "내가 레인을 쥐었나"는 resource_hold 의 부분 유니크 인덱스
--   (resource_one_holder)가 정본이고 HeldBy(project,'landing') 로만 파생한다.
--   사본을 두면 갈릴 자리가 생기는데, 갈렸을 때 어느 쪽이 참인지 정하는 문장이 없다.
--
-- ★ item_id 도 없다. 세션의 선점에서 파생 가능하고, 읽는 쪽이 없는 컬럼은 session_workspace 가
--   이미 밟은 함정이다 — 쓰기만 있는 표는 나중에 "그 축은 이미 있다"의 거짓 근거가 된다.
--
-- ★ 만료도 자동 회수도 없다. resource_hold 와 같은 규율이다.

CREATE TABLE landing_queue (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,   -- 이것이 순번이다
  project     TEXT NOT NULL REFERENCES project(id),
  session_id  TEXT NOT NULL REFERENCES session(id),
  enqueued_at TEXT NOT NULL,
  left_at     TEXT,
  left_kind   TEXT,        -- ok | fail | leave | finish | force
  left_detail TEXT,

  -- 빠진 시각과 빠진 종류는 함께 있거나 함께 없다. 한쪽만 채워지면
  -- "아직 줄에 있다"와 "빠졌는데 종류를 모른다"가 같은 행 모양이 된다.
  CHECK ((left_at IS NULL) = (left_kind IS NULL)),

  -- ★ 사유 없는 회수는 나중에 되짚을 수 없다. 그 규율을 산문이 아니라 제약으로 만든다
  --   (item.state='dropped' → close_reason 이 그 본이다).
  --   ok·finish 만 면제된다 — 정상 종료라 "왜"가 종류 자체에 들어 있다.
  CHECK (left_kind IS NULL
         OR left_kind IN ('ok','finish')
         OR (left_detail IS NOT NULL AND left_detail <> ''))
);

-- ★ 한 세션은 살아 있는 줄 행을 하나만 가진다. 재진입이 줄을 두 자리 차지하면 순번이 거짓이 된다.
--   배타와 같은 규율로 애플리케이션 판정이 아니라 부분 유니크 인덱스가 지킨다.
CREATE UNIQUE INDEX landing_queue_one_live_per_session
  ON landing_queue(project, session_id) WHERE left_at IS NULL;

-- 순서 집행 지점(맨 앞 조회)과 줄 전체 나열이 이 인덱스를 탄다.
CREATE INDEX landing_queue_waiting
  ON landing_queue(project, id) WHERE left_at IS NULL;
```

- [ ] **Step 2: 바이너리에 물린다 — `store.go` 세 자리**

```go
// ── (1) :35 `var migration002 string` 바로 아래 ─────────────────────────────
//go:embed migrations/003_landing_queue.sql
var migration003 string

// ── (2) :40 ────────────────────────────────────────────────────────────────
const SchemaVersion = 3

// ── (3) :61-63 ─────────────────────────────────────────────────────────────
var migrations = []Migration{
	{To: 2, Name: "멱등 기록을 DB 로", SQL: migration002},
	{To: 3, Name: "랜딩 순서 큐", SQL: migration003},
}
```

- [ ] **Step 3: 시험을 돌려 무엇이 깨지는지 본다**

Run: `cd plugins/flightdeck/server && go test ./internal/store/ -run 'Migrat|Upgrade|Rollback|Fresh' -v`
Expected: `TestOpenUpgradesVersion1Database` 와 `TestFreshInstallAndUpgradeProduceTheSameSchema` 가 `table landing_queue already exists` 로 FAIL, `TestFailedUpgradeNamesTheBackup` 이 백업 단정에서 FAIL.

**이 실패는 결함이 아니라 예정된 것이다.** 두 시험이 현행 스키마를 적용한 뒤 손으로 v1 로 되돌리는 방식이라, 새 표를 걷어내는 줄을 안 더하면 반드시 이렇게 된다.

- [ ] **Step 4: `makeV1DB` 의 DROP 목록을 넓힌다** (`migrate_test.go:101-109`)

```go
	// v1 로 되돌린다 — 증분이 만든 객체를 전부 걷어낸다.
	// ★ 새 증분을 더할 때마다 여기에 그 객체를 더해야 한다. 안 더하면 재열기에서
	//   "table already exists" 로 죽고, 그 실패는 마이그레이션 결함처럼 보이지만 이 목록의 누락이다.
	for _, stmt := range []string{
		`DROP INDEX IF EXISTS idempotency_by_at`,
		`DROP TABLE IF EXISTS idempotency`,
		`DROP INDEX IF EXISTS landing_queue_waiting`,
		`DROP INDEX IF EXISTS landing_queue_one_live_per_session`,
		`DROP TABLE IF EXISTS landing_queue`,
		`DELETE FROM schema_version WHERE version > 1`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("v1 로 되돌리기 실패(%s): %v", stmt, err)
		}
	}
```

같은 파일의 v1 전제 단정(:143-145)과 업그레이드 뒤 단정(:162-164)을 두 표(`idempotency`, `landing_queue`)를 도는 루프로 넓힌다.

- [ ] **Step 5: `rollback_test.go` 의 증분 스텁을 전 구간으로 만든다** (:89-92)

한 단짜리(`[]Migration{{To: 2, …}}`)로 두면 `SchemaVersion=3` 일 때 `UpgradeSteps` 가 **적용을 시작하기도 전에** "3 으로 올리는 증분이 없다"로 거절해, 이 시험이 보려던 "증분이 돌다 깨진" 경로에 안 들어간다.

```go
	// 커밋된 기준선 위의 변이다 — defer 로 원복한다.
	// ★ 한 단만 두면 UpgradeSteps 가 적용 전에 거절해 시험이 보려던 경로에 안 들어간다.
	//   BaseSchemaVersion+1 부터 SchemaVersion 까지 전 구간을 채우되 마지막 단을 깨뜨린다.
	orig := migrations
	defer func() { migrations = orig }()
	var stub []Migration
	for v := BaseSchemaVersion + 1; v <= SchemaVersion; v++ {
		m := Migration{To: v, Name: "일부러 깨뜨린 증분", SQL: `SELECT 1;`}
		if v == SchemaVersion {
			m.SQL = `THIS IS NOT SQL;`
		}
		stub = append(stub, m)
	}
	migrations = stub
```

- [ ] **Step 6: 시험이 초록인지 확인**

Run: `go test ./internal/store/ -run 'Migrat|Upgrade|Rollback|Fresh|Bundled' -v`
Expected: PASS 전부

- [ ] **Step 7: 커밋**

```bash
git add plugins/flightdeck/server/internal/store/migrations/003_landing_queue.sql \
        plugins/flightdeck/server/internal/store/store.go \
        plugins/flightdeck/server/internal/store/migrate_test.go \
        plugins/flightdeck/server/internal/store/rollback_test.go
git commit -m "feat(flightdeck): landing_queue 증분 003 — 순서만 담고 점유는 안 담는다"
```

---

### Task 2: 모델과 오류 좌표

**Files:**
- Modify: `internal/model/types.go` (`ResourceHold`:258 와 `Event`:260 사이)
- Modify: `internal/store/notfound.go` (`:35` const + `:44-48` `NotFoundKinds()`)
- Modify: `internal/store/constraint.go` (`:65` const + `:72-80` `ConflictTargets()`)
- Modify: `internal/api/errors.go` (`notFoundGuidance`:147-164 · `conflictWordTable`:216-289) ← **강제 동반**

**Interfaces:**
- Produces: `model.LandingLeftKind`(5값) · `model.LandingRow` · `store.NFLiveLandingRow` · `store.TargetLandingQueue`

- [ ] **Step 1: 모델을 더한다**

```go
// LandingLeftKind 는 랜딩 줄에서 빠진 종류다. schema 의 CHECK 와 문자열이 정확히 일치해야 한다.
//
// 종류를 사유와 한 컬럼에 뭉개지 않는다 — `force:<사유>` 접두 파싱은
// api/idempotency.go 가 이미 기각한 방식이다. 종류는 CHECK 로, 사유는 별도 컬럼으로 둔다.
type LandingLeftKind string

const (
	// LandingLeftOK 는 **"랜딩됐다"가 아니다.** 세션이 ok 로 보고하고 레인을 놓았다는 뜻뿐이다.
	// 랜딩 sha 의 출처는 러너가 실제로 fast-forward 한 sha 하나이고(설계 §5),
	// 클라이언트 자기 보고를 그 자리에 넣으면 "남의 커밋이 이 항목의 랜딩 sha 로 박힌"
	// 결함(3회 관측)이 이름만 바꿔 부활한다. Item.LandedRef 를 이 값으로 채우지 마라.
	LandingLeftOK LandingLeftKind = "ok"

	LandingLeftFail   LandingLeftKind = "fail"   // 검증 실패. left_detail 필수(스키마 CHECK)
	LandingLeftLeave  LandingLeftKind = "leave"  // 줄 서 놓고 스스로 빠졌다. left_detail 필수
	LandingLeftFinish LandingLeftKind = "finish" // 세션이 마무리하며 함께 닫혔다
	LandingLeftForce  LandingLeftKind = "force"  // 사람이 회수했다. left_detail 필수
)

// LandingRow 는 랜딩 레인 줄의 한 자리다. **ID 가 곧 순번이다.**
// GrantedAt 이 없다 — "쥐었나"는 resource_hold 의 부분 유니크 인덱스가 정본이다.
type LandingRow struct {
	ID         int64
	Project    string
	SessionID  string
	EnqueuedAt time.Time
	LeftAt     *time.Time // nil 이면 아직 줄에 있다
	LeftKind   LandingLeftKind
	LeftDetail string
}
```

- [ ] **Step 2: 좌표 둘을 더하고 목록 함수를 함께 고친다**

`notfound.go`: `NFLiveLandingRow NotFoundKind = "살아 있는 랜딩 줄 행"` 을 const 에, `NotFoundKinds()` 반환 슬라이스 끝에 추가.
`constraint.go`: `TargetLandingQueue ConflictTarget = "landing_queue"` 를 const 에, `ConflictTargets()` 반환 슬라이스 끝에 추가.

주석은 왜 `NFLiveClaim` 과 같은 자리인지를 적는다: 이미 빠진 줄 행은 이력으로 남으므로 "행이 아예 없다"와 "이미 빠졌다"를 가르지 않으면 회수 화면이 둘을 같은 문구로 낸다.

- [ ] **Step 3: 전수 시험을 돌려 api 가 빨간지 확인**

Run: `go test ./internal/api/ -run 'NotFoundGuidanceCoversEveryKind|ConflictWordsCoverEveryTarget' -v`
Expected: FAIL — 새 종류/대상에 대응하는 문구가 `errors.go` 에 없다

- [ ] **Step 4: `api/errors.go` 두 표에 항목을 더한다**

`notFoundGuidance` 에 `store.NFLiveLandingRow` 항목(처방: "줄에 선 적이 없거나 이미 빠졌다 — `land` 로 다시 서라"), `conflictWordTable` 에 `store.TargetLandingQueue` 항목(문구: "이미 살아 있는 줄 행이 있다 — 한 세션은 한 자리만 선다"). 기존 항목들의 어투를 그대로 따른다.

- [ ] **Step 5: 초록 확인 후 커밋**

Run: `go test ./internal/... `
Expected: PASS 전부

```bash
git commit -am "feat(flightdeck): 랜딩 줄 행의 모델과 오류 좌표"
```

---

### Task 3: 저장층 `landing.go`

**Files:**
- Create: `internal/store/landing.go`
- Create: `internal/store/landing_test.go`
- Modify: `internal/store/resource.go` — `ListHeld` 본문을 `listHeld(ctx, q dbtx, project)` 자유 함수로 빼고 `Store.ListHeld`/**신규 `Tx.ListHeld`** 가 그것을 부른다. **신규 `Tx.HeldBy`** 는 기존 자유 함수 `heldBy`(:146) 위임 3줄.
- Modify: `internal/store/session.go` — `Signals`(:429) 뒤에 `LastSignal` 추가

**Step 0 (Task 2 리뷰가 잡은 자리): 003 증분의 CHECK 에 값 열거를 더한다.**

지금 `003_landing_queue.sql` 의 CHECK 는 `ok`·`finish` 를 **사유 면제 대상으로만** 특별 취급하고, `left_kind` 가 다섯 값 중 하나인지는 **아무것도 안 막는다.** `left_kind='bogus'` 가 그대로 들어간다. 이 레포는 `job.fail_kind` 를 값 열거 CHECK 로 잡는 선례가 있고, "판정을 애플리케이션이 아니라 DB 제약으로 둔다"가 `resource.go:78-81` 의 규율이다.

증분이 **아직 랜딩 안 됐으므로 003 을 직접 고친다**(004 를 새로 만들지 마라 — 안 나간 증분을 쪼개면 이력만 는다). 표 정의에 CHECK 를 하나 더한다:

```sql
  -- ★ 종류는 다섯뿐이다. Go 쪽 ValidateLandingLeave 가 1차 방어이고 이것이 최종 방어다 —
  --   판정을 애플리케이션에만 두면 우회할 코드가 언제든 생긴다(resource.go:78-81 규율).
  --   job.fail_kind 가 같은 모양으로 값을 열거한다.
  CHECK (left_kind IS NULL
         OR left_kind IN ('ok','fail','leave','finish','force'))
```

시험은 매번 새 DB 를 만들므로 영향이 없다. 손으로 띄운 서버의 DB 가 이미 003 을 적용했다면 `schema_version` 이 3 이라 다시 안 도니 그 파일을 지우고 다시 만들어라.

**왜 `Tx.ListHeld`/`Tx.HeldBy` 가 필요한가:** `finish` 의 holds 읽기를 트랜잭션 안으로 옮기는 것과 `ReleaseLaneRow` 의 "점유가 있을 때만 회수" 판정을 트랜잭션 안에 두는 데 필요하다. 밖에서 판정하면 그 사이에 남이 잡아 **남의 점유를 반납한다.**

**Interfaces:**
- Produces:
  - `store.ValidateLandingLeave(kind model.LandingLeftKind, detail string) error`
  - `(t *Tx) EnqueueLanding(project, sessionID string) (model.LandingRow, error)` — 재진입 안전
  - `(t *Tx) LiveLandingRow(project, sessionID string) (model.LandingRow, error)` / `(s *Store)` 판
  - `(t *Tx) FrontLandingRow(project string) (model.LandingRow, error)` / `(s *Store)` 판
  - `(t *Tx) CloseLandingRow(project string, id int64, kind model.LandingLeftKind, detail string) error` / `(s *Store)` 판
  - `(t *Tx) CloseLandingRowBySession(project, sessionID string, kind model.LandingLeftKind, detail string) error` / `(s *Store)` 판 — **살아 있는 행이 없으면 무동작 통과**
  - `(s *Store) ListLandingQueue(ctx, project string) ([]model.LandingRow, error)` — 창으로 안 거른다
  - `(t *Tx) ListHeld(project string) ([]model.ResourceHold, error)` · `(t *Tx) HeldBy(project, resource string) (model.ResourceHold, error)`
  - `(s *Store) LastSignal(ctx, sessionID string) (time.Time, bool, error)` — **창 밖 세션도 답한다**

- [ ] **Step 1: 실패하는 시험을 먼저 쓴다** (`landing_test.go`)

여덟 개다. 각각이 잠그는 것을 이름이 말한다:

```go
// TestEnqueueLandingIsReentrantWithinTheSameLiveRow — 같은 세션이 두 번 서면 같은 행이다.
//   재진입이 새 행을 만들면 부를 때마다 맨 뒤로 밀린다.
// TestEnqueueLandingAfterLeavingGoesToTheBack — 닫힌 뒤 다시 서면 새 행이고 id 가 더 크다.
//   굶주림 판정(검증 실패 = 맨 뒤)이 이 성질 위에 있다.
// TestEnqueueLandingReentryDoesNotPoisonTheTransaction — 재진입이 SQLite 제약 위반을 거치는데
//   그 뒤 같은 트랜잭션에서 다른 쓰기가 계속 성립한다(문장 단위 롤백 확인).
// TestValidateLandingLeave — ok·finish 는 사유 면제, fail·leave·force 는 사유 필수, 모르는 종류는 거절.
// TestCloseLandingRowRefusesForceWithoutReason — 사유 없는 회수는 되짚을 수 없다.
// TestLandingQueueOneLivePerSessionIsEnforcedByTheIndex — 앱 판정이 아니라 인덱스가 막는다.
//   (인덱스를 DROP 하고 같은 삽입이 통과하는지로 변이 검증)
// TestFrontLandingRowIsTheSmallestLiveID — 순서 집행이 걸리는 유일한 자리.
// TestListLandingQueueKeepsOrderAndDoesNotFilterByWindow — 창 밖 세션이 맨 앞에서 막는 상황이야말로
//   사람이 봐야 하는 상황이다. 거르면 "줄이 비었는데 아무도 못 잡는다"가 된다.
// TestCloseLandingRowBySessionClosesTheGhostAndIsIdempotent — 줄을 안 선 세션이 마무리하는 것은 정상이고
//   여기서 ErrNotFound 를 올리면 finish 트랜잭션이 롤백돼 핸드오프 판단이 사라진다.
// TestLastSignalAnswersForSessionsOutsideTheWindow — 레인 점유자가 창 밖일 때가 정확히 알아야 할 때다.
```

- [ ] **Step 2: 시험이 컴파일 실패로 죽는지 확인**

Run: `go test ./internal/store/ -run Landing`
Expected: FAIL — `undefined: EnqueueLanding` 등

- [ ] **Step 3: `landing.go` 를 쓴다**

핵심은 셋이다. 나머지(스캔 헬퍼·조회 3종·Store 래퍼)는 `resource.go` 의 모양을 그대로 베낀다.

**① 컬럼 목록을 한 자리에 둔다** — 질의가 셋이라 두 벌이 되면 컬럼을 늘린 날 한쪽만 고쳐지고 그 비대칭은 스캔이 죽을 때까지 안 보인다:

```go
const landingCols = `id, project, session_id, enqueued_at, left_at, left_kind, left_detail`
```

**② 재진입을 오류가 아니라 정상 결과로 만든다** — 이 함수의 요점이다:

```go
// EnqueueLanding 은 랜딩 줄에 선다. **재진입 안전하다** — 이미 살아 있는 줄 행이 있으면
// 새로 넣지 않고 그것을 그대로 돌려준다.
//
// ★ 먼저 조회해 판정하지 않는다(AcquireResource 와 같은 이유). 그냥 넣고 **부분 유니크
// 인덱스의 위반을 받아** 이미 있는 행을 조회한다. 재진입을 앱 판정으로 두면 조회와 삽입
// 사이에 같은 세션의 다른 호출이 끼어들 수 있고, 그때 한 세션이 줄을 두 자리 차지해
// 순번 자체가 거짓이 된다.
func (t *Tx) EnqueueLanding(project, sessionID string) (model.LandingRow, error) {
	if project == "" || sessionID == "" {
		return model.LandingRow{}, fmt.Errorf("랜딩 줄 좌표가 비었다(project=%q session=%q)",
			clip(project, 64), clip(sessionID, 64))
	}
	now := nowStamp()
	res, err := t.tx.ExecContext(t.ctx, `
		INSERT INTO landing_queue(project, session_id, enqueued_at, left_at, left_kind, left_detail)
		VALUES (?, ?, ?, NULL, NULL, NULL)`,
		project, sessionID, fmtTime(now))
	if err != nil {
		row, qErr := liveLandingRow(t.ctx, t.tx, project, sessionID)
		if qErr == nil {
			// 이미 서 있다. 순번(id)과 대기 시작 시각을 그대로 낸다 —
			// 여기서 새 행을 만들면 다시 부를 때마다 맨 뒤로 밀린다.
			return row, nil
		}
		if !errors.Is(qErr, ErrNotFound) {
			return model.LandingRow{}, fmt.Errorf(
				"랜딩 줄 서기 실패(project=%q session=%q): %w (살아 있는 줄 행 조회도 실패: %v)",
				clip(project, 64), clip(sessionID, 64), err, qErr)
		}
		// 살아 있는 행이 없는데 삽입이 실패했다 = 재진입이 아닌 다른 오류(FK·CHECK 등).
		return model.LandingRow{}, writeErr(err, writeTarget{
			Target: TargetLandingQueue, Project: project, ID: sessionID,
			RefHint: fmt.Sprintf("프로젝트 %s · 세션 %s", clip(project, 64), clip(sessionID, 64)),
		}, "랜딩 줄 서기 실패(project=%q session=%q)", clip(project, 64), clip(sessionID, 64))
	}
	id, err := res.LastInsertId()
	if err != nil {
		return model.LandingRow{}, fmt.Errorf("랜딩 줄 순번 확인 실패(project=%q session=%q): %w",
			clip(project, 64), clip(sessionID, 64), err)
	}
	return model.LandingRow{ID: id, Project: project, SessionID: sessionID, EnqueuedAt: now}, nil
}
```

**③ 자원 반납을 여기서 하지 않는다** — `CloseLandingRow` 는 줄 행만 닫는다. 레인 점유의 정본은 `resource_hold` 이고 그 반납과 이 호출을 같은 트랜잭션에 묶는 것은 service 의 몫이다. 여기서 함께 건드리면 **"줄 행만 닫고 싶은" 대기 중 회수가 점유가 없다는 이유로 실패한다.**

```go
func (t *Tx) CloseLandingRow(project string, id int64, kind model.LandingLeftKind, detail string) error {
	if err := ValidateLandingLeave(kind, detail); err != nil {
		return err
	}
	res, err := t.tx.ExecContext(t.ctx, `
		UPDATE landing_queue SET left_at = ?, left_kind = ?, left_detail = ?
		WHERE project = ? AND id = ? AND left_at IS NULL`,
		fmtTime(time.Now()), string(kind), nullStr(detail), project, id)
	if err != nil {
		return fmt.Errorf("랜딩 줄 행 닫기 실패(project=%q id=%d kind=%q): %w",
			clip(project, 64), id, kind, err)
	}
	return affectedOne(res, NFLiveLandingRow, project, strconv.FormatInt(id, 10))
}
```

`CloseLandingRowBySession` 은 **`affectedOne` 을 쓰지 않는다.** 살아 있는 행이 없으면 무동작으로 통과한다(`ReleaseClaim` 과 같은 규율, `item.go:684-686`) — 이유를 주석에 박아라: 줄을 한 번도 안 선 세션이 마무리하는 것은 정상이고, 여기서 `ErrNotFound` 를 올리면 finish 트랜잭션이 통째로 롤백돼 핸드오프 판단이 사라진다.

- [ ] **Step 4: `resource.go` 에 Tx 판 둘을 더한다**

`ListHeld` 본문을 `listHeld(ctx context.Context, q dbtx, project string)` 자유 함수로 빼고, `Store.ListHeld` 와 새 `Tx.ListHeld` 가 각각 부른다. `Tx.HeldBy` 는 기존 `heldBy`(:146) 에 위임하는 3줄.

- [ ] **Step 5: `session.go` 에 `LastSignal` 을 더한다**

`Signals`(:429) 바로 뒤, `// footprint` 구분선 앞. **창으로 거르지 않는다** — 레인 점유자가 창 밖일 때가 정확히 그 나이를 알아야 할 때다.

- [ ] **Step 6: 시험 초록 확인**

Run: `go test ./internal/store/ -race`
Expected: PASS 전부

- [ ] **Step 7: 커밋**

```bash
git add plugins/flightdeck/server/internal/store/
git commit -m "feat(flightdeck): 랜딩 줄 저장층 — 배타는 안 건드리고 순서만 다룬다"
```

---

### Task 4: 서비스 `landing.go` — 전이와 응답 계약

**Files:**
- Create: `internal/service/landing.go`
- Create: `internal/service/landing_test.go`

**Interfaces:**
- Consumes: Task 3 의 store 함수 전부
- Produces (**이 태스크가 REST·CLI·MCP 의 계약을 확정한다 — json 태그가 곧 계약이다**):

```go
type LandInput struct{ Project, SessionID string }
type LandReportInput struct {
	Project, SessionID string
	Kind               model.LandingLeftKind // ok | fail
	Detail             string
}
type LandLeaveInput struct{ Project, SessionID, Detail string }

// LandResult 는 land 세 갈래가 공유하는 응답이다.
// ★ respond.go:20-23 이 이 타입을 그대로 직렬화하므로 json 태그가 REST 계약이자
//   CLI 파싱 대상이다. 태그가 어긋나면 CLI 가 오류 없이 0값을 찍는다.
type LandResult struct {
	State    string      `json:"state"`              // turn | waiting | released | left | reclaimed
	RowID    int64       `json:"row_id"`
	Position int         `json:"position"`           // 1이면 맨 앞. waiting 일 때만 의미 있다
	Reason   string      `json:"reason,omitempty"`   // reclaimed 일 때 회수 사유
	Holder   *LaneHolder `json:"holder,omitempty"`   // waiting 일 때 앞사람
}

type LaneHolder struct {
	SessionID    string     `json:"session_id"`
	AcquiredAt   time.Time  `json:"acquired_at"`
	LastSignalAt *time.Time `json:"last_signal_at,omitempty"` // nil = 신호가 하나도 없다
}

// LaneView 는 보드·화면이 읽는 레인 전체다.
// ★ BoardView.Lane 은 *LaneView 다 — nil = 안 읽었다, Entries 빈 슬라이스 = 0건.
//   둘을 한 값으로 접으면 "질의가 안 돌았다"와 "아무도 안 섰다"가 화면에서 같아진다.
type LaneView struct {
	Holder  *LaneHolder `json:"holder,omitempty"`
	Entries []LaneEntry `json:"entries"`
}

type LaneEntry struct {
	RowID        int64      `json:"row_id"`
	SessionID    string     `json:"session_id"`
	EnqueuedAt   time.Time  `json:"enqueued_at"`
	LastSignalAt *time.Time `json:"last_signal_at,omitempty"`
}

type LaneReleaseResult struct {
	RowID       int64  `json:"row_id"`
	SessionID   string `json:"session_id"`
	HeldRelease bool   `json:"held_release"` // 점유까지 회수했나(대기 중이면 false)
	JudgmentID  string `json:"judgment_id"`
}

func (s *Service) Land(ctx context.Context, in LandInput) (LandResult, error)
func (s *Service) LandReport(ctx context.Context, in LandReportInput) (LandResult, error)
func (s *Service) LandLeave(ctx context.Context, in LandLeaveInput) (LandResult, error)
func (s *Service) LandingLane(ctx context.Context, project string) (LaneView, error)
func (s *Service) ReleaseLaneRow(ctx context.Context, project string, rowID int64, actor, reason string) (LaneReleaseResult, error)
```

`actor` 는 회수를 누가 했나다 — CLI 는 세션 id 또는 사용자, 웹은 빈 문자열(판단 본문에 "대시보드(사람)"). 기존 웹 쓰기 둘이 그렇게 한다(`web/actions.go:145~`).

- [ ] **Step 1: 실패하는 시험을 먼저 쓴다** (`landing_test.go`)

```go
// TestTwoSessionsBothKeepTheirRowWhenOnlyOneGetsTheLane — 동시에 서면 하나는 turn, 하나는 waiting.
//   ★ 둘 다 줄 행이 남는다. ResourceHeldError 를 롤백으로 접으면 큐에 영원히 한 명만 남는다.
// TestSecondInLineGetsTheLaneOnNextLandAfterFrontLeaves — 맨 앞이 ok 로 빠진 뒤 2번째가 부르면 부여된다.
// TestFailedReportSendsTheSessionToTheBack — fail 로 빠지고 다시 서면 id 가 더 크다.
// TestReclaimedSessionIsToldSoAndItsRowStaysForce — 회수된 세션의 ok 보고가 'reclaimed' 로 답하고
//   줄 행은 force 로 남는다. ok 로 덮으면 "성공적으로 랜딩했다"는 거짓 기록이 된다.
// TestLeaveWorksWithoutHoldingTheLane — 줄 서 놓고 포기한 세션이 스스로 빠지는 유일한 길.
// TestReleaseLaneRowWorksOnAWaitingRow — 점유가 없어도 회수가 성립한다.
//   ★ 이것이 "죽은 선두가 큐를 영구히 막는" 것을 막는 자리다.
// TestReleaseLaneRowLeavesAJudgment — 회수 기록이 원장에서 빠지지 않는다.
// TestLandingLaneSeparatesZeroFromUnobserved — Lane 이 nil 인 것과 Entries 가 빈 것이 다르다.
// TestLiveLandingHoldAlwaysHasALiveQueueRow — 두 표가 어긋난 상태를 잡는다.
//   ★ 어긋나면 ListLandingQueue 는 아무도 안 보여 주는데 레인은 영영 잡혀 있다.
```

- [ ] **Step 2: 컴파일 실패 확인**

Run: `go test ./internal/service/ -run Land`
Expected: FAIL — undefined

- [ ] **Step 3: `Land` 의 전이를 쓴다 — 이 계획에서 가장 조심할 자리**

```go
// Land 는 랜딩 줄에 서거나, 이미 서 있으면 내 자리를 다시 낸다.
//
// ★ 전부 한 트랜잭션 안에서 한다. DSN 이 _txlock=immediate 라 land 끼리 직렬화된다
//   (store.go:100-113). 그래서 "맨 앞인가" 판정과 취득 사이에 남이 끼어들 수 없다.
//
// ★ **ResourceHeldError 는 오류가 아니라 정상 결과다.** 삼켜서 "너는 N번째"로 바꾸고
//   트랜잭션은 커밋한다. 여기서 롤백하면 줄 행과 순번이 함께 사라져 큐에 영원히
//   한 명만 남고 "순서 큐"라는 이름이 거짓이 된다.
//
// ★ 순서 집행 지점은 front.ID == mine.ID 비교 **하나**다. 이 비교가 없으면 순번은
//   표시용이 되고 아무것도 집행하지 않는다.
func (s *Service) Land(ctx context.Context, in LandInput) (LandResult, error) {
	if strings.TrimSpace(in.Project) == "" || strings.TrimSpace(in.SessionID) == "" {
		return LandResult{}, &RefusedError{What: "land", Reason: "프로젝트나 세션 좌표가 비었다"}
	}
	var out LandResult
	err := s.st.Tx(ctx, func(t *store.Tx) error {
		mine, err := t.EnqueueLanding(in.Project, in.SessionID)
		if err != nil {
			return err
		}
		out = LandResult{State: "waiting", RowID: mine.ID}

		front, err := t.FrontLandingRow(in.Project)
		if err != nil {
			return err // 방금 넣었으므로 ErrNotFound 는 불가능하다
		}
		if front.ID == mine.ID {
			_, aerr := t.AcquireResource(in.Project, LaneResource, store.Holder{SessionID: in.SessionID})
			if aerr == nil {
				out.State = "turn"
				out.Position = 1
				t.LogEvent("lane.grant", in.Project, in.SessionID,
					map[string]any{"row": mine.ID})
				return nil
			}
			var held *store.ResourceHeldError
			if !errors.As(aerr, &held) {
				return aerr
			}
			// 맨 앞인데 남이 쥐고 있다 = 두 표가 어긋난 상태다. 오류로 올리지 않고
			// 점유자를 그대로 실어 보낸다 — 그 상태를 푸는 것은 사람의 회수다.
		}
		pos, holder, err := s.lanePosition(t, in.Project, mine.ID)
		if err != nil {
			return err
		}
		out.Position, out.Holder = pos, holder
		return nil
	})
	return out, err
}
```

`lanePosition` 은 `ListLandingQueue` 순서에서 내 자리(1-based)와 현재 점유자(`t.HeldBy` + `LastSignal`)를 낸다.

`LaneResource` 는 이 패키지의 상수 하나다: `const LaneResource = "landing"`. **web 이나 다른 패키지에 두 벌 만들지 마라.**

- [ ] **Step 4: `LandReport`·`LandLeave`·`LandingLane`·`ReleaseLaneRow` 를 쓴다**

`LandReport` 의 첫 판정이 핵심이다 — **내가 아직 점유자인지 먼저 본다:**

```go
	held, herr := t.HeldBy(in.Project, LaneResource)
	if errors.Is(herr, store.ErrNotFound) || (herr == nil && held.SessionID != in.SessionID) {
		// 내 레인이 아니다. 회수됐다는 뜻이므로 그 사실을 그대로 답한다 —
		// 여기서 줄 행을 ok 로 닫으면 "성공적으로 랜딩했다"는 거짓 기록이 남는다.
		row, rerr := t.LiveLandingRow(in.Project, in.SessionID)
		...
		out = LandResult{State: "reclaimed", Reason: <닫힌 행의 LeftDetail>}
		return nil
	}
```

`ReleaseLaneRow` 는 한 트랜잭션에서 셋을 한다: 줄 행 조회 → 그 세션이 레인을 쥐고 있으면 `ForceReleaseResource`(점유가 없으면 건너뛴다) → `CloseLandingRow(kind=force, detail=reason)` → `AddJudgment(kind=decision)`. 판단 본문에 서버가 관측한 것(점유 경과·마지막 신호 나이·그때 줄에 있던 사람)을 적는다 — **`left_detail` 의 사본이 아니라 더 넓은 기록이다.**

- [ ] **Step 5: 초록 확인**

Run: `go test ./internal/service/ -race`

- [ ] **Step 6: 커밋**

```bash
git commit -am "feat(flightdeck): 랜딩 레인 전이 — 점유 실패는 오류가 아니라 순번이다"
```

---

### Task 5: `finish` 접합 — 이 항목이 살리는 죽은 코드

**Files:**
- Modify: `internal/service/finish.go:128-134`(holds 읽기) · `:179-190`(④ 반납)
- Modify: `internal/service/finish_test.go:150-195`

**왜 지금인가:** `land` 가 `AcquireResource` 의 첫 프로덕션 호출자가 되는 순간 ④ 가 살아난다. Task 4 까지 랜딩하고 이걸 안 고치면 **랜딩 후 마무리한 세션이 유령 행을 남기고 영영 다시 줄을 못 선다.**

- [ ] **Step 1: 실패하는 시험 둘을 먼저 쓴다**

```go
// TestFinishWhileHoldingTheLaneLetsTheSessionQueueAgain
//   ★ 단정은 "자원이 반납됐나"가 아니라 **"그 세션이 다시 줄을 설 수 있나"** 다.
//     자원만 보면 줄 행이 유령으로 남는 결함을 못 잡는다.
// TestFinishSurvivesAForcedReleaseRacingIt
//   ListHeld 와 Tx 사이에 강제 회수를 끼우고 finish 를 부른다 → **판단 행이 남아 있다.**
//   오늘 이 시험은 빨갛다: ErrNotFound 가 그대로 올라가 판단까지 롤백된다.
```

- [ ] **Step 2: 빨간지 확인**

Run: `go test ./internal/service/ -run 'FinishWhileHolding|FinishSurvives' -v`
Expected: 둘 다 FAIL

- [ ] **Step 3: ④ 를 고친다 — 셋을 한꺼번에**

```go
	// ④ 이 세션이 쥔 자원 반납.
	//
	// ★ holds 를 **트랜잭션 안에서** 읽는다. 밖에서 읽으면(앞선 판) 그 사이에 남이 회수했을 때
	//   아래 반납이 ErrNotFound 를 올리고, 그 오류가 ①②③ 을 통째로 롤백시켜
	//   **원리적으로 파생 불가한 유일한 자산인 판단이 사라진다.**
	holds, err := t.ListHeld(in.Project)
	if err != nil {
		return err
	}
	for _, h := range holds {
		if h.SessionID != in.SessionID {
			continue
		}
		if err := t.ReleaseResource(in.Project, h.Resource, store.Holder{SessionID: in.SessionID}); err != nil {
			var held *store.ResourceHeldError
			if errors.Is(err, store.ErrNotFound) || errors.As(err, &held) {
				// ★ 남이 이미 회수했다. 그것은 finish 를 실패시킬 이유가 아니다 —
				//   원장에만 남기고 Released 에서 뺀다(ReleaseClaim 과 같은 규율).
				t.LogEvent("lane.release_skipped", in.Project, in.SessionID,
					map[string]any{"resource": h.Resource, "why": "이미 반납되었거나 남이 회수했다"})
				continue
			}
			return fmt.Errorf("자원 %s 반납 실패: %w", clip(h.Resource, 64), err)
		}
		out.Released = append(out.Released, h.Resource)
	}

	// ★ 줄 행 닫기는 **반납 루프 밖에서 조건 없이** 한다. 루프 안에 두면 레인을 안 쥔 채
	//   줄만 서 있던 세션(대기 중 마무리)의 유령 행이 안 닫힌다.
	//   살아 있는 행이 없으면 무동작으로 통과하므로 줄을 안 선 세션에도 안전하다.
	if err := t.CloseLandingRowBySession(
		in.Project, in.SessionID, model.LandingLeftFinish, ""); err != nil {
		return err
	}
```

- [ ] **Step 4: 초록 확인. 기존 finish 시험이 깨졌으면 함께 고친다**

Run: `go test ./internal/service/ -race`

- [ ] **Step 5: 커밋**

```bash
git commit -am "fix(flightdeck): finish 가 레인을 놓을 때 줄 행도 닫는다 — 유령 선두를 만들지 않는다"
```

---

### Task 6: MCP `land` 도구 — 일곱 번째

**Files:**
- Modify: `internal/mcpsrv/tools.go`(머리 주석 · 표에 `alloc`:130-136 뒤 · `landArgs`) · `mcpsrv.go`(패키지 주석 :11 · 디스패치 `case "alloc"`:415 뒤 · `toolLand`) · `render.go`(`RenderBoard` foot 분기 :221-225 뒤 한 줄 · 파일 끝 `RenderLand`) · `backend.go`(인터페이스 셋 · 머리 주석의 "도구 6개") · `identity.go`(`sessionBoundTools`:169-176 · 배너 :145)
- Modify: `protocol_test.go:62-85` · `add_coordinate_test.go:61-76` · `server_test.go:244-250` · `serial_test.go:130-133` · `identity_test.go:172-184`
- Create: `internal/mcpsrv/land_test.go`

**Interfaces:**
- Consumes: `service.Land`/`LandReport`/`LandLeave`/`LandingLane`
- Produces: `mcpsrv.Backend` 에 `Land`·`LandReport`·`LandLeave`

- [ ] **Step 1: 정체 게이트에 넣는다 — 빼먹으면 아무 시험도 안 깨진다**

```go
// 세션 귀속이 필요한 도구. 여기 있는 것은 원장에 세션 id 로 행을 남긴다.
//
// ★ 표에 없는 도구는 거절이 아니라 **통과**다(GateTool 의 `if !sessionBoundTools[tool]`).
//   land 는 resource_hold.session_id 와 landing_queue.session_id 로 행을 남기므로 반드시 넣는다.
//   빼먹으면 세션 좌표 없이 레인을 잡고, 실패는 "정체가 없다"가 아니라 FK/CHECK 위반으로 나온다.
var sessionBoundTools = map[string]bool{
	"pick": true, "note": true, "add": true, "finish": true, "land": true,
}
```

`identity.go:145` 배너의 "안 되는 것" 목록과 `mcpsrv.go:11` 패키지 주석의 같은 목록도 함께 고친다 — **표와 문구가 갈리면 그 문구가 화면에서 거짓이 된다.**

- [ ] **Step 2: `release` 를 거절한다 — 기존 판정을 뒤집지 않는다**

`pick` 의 `steal_reason` 거절(`mcpsrv.go:565-575`)을 그대로 베낀다. 한 서버가 선점 회수는 거절하고 레인 회수는 허용하면 그 거절 문구가 화면에서 거짓이 된다. 처방은 `fd lane release --row <id> --reason "..."` 을 가리킨다.

- [ ] **Step 3: `RenderLand` 세 갈래**

`turn` / `waiting`(앞사람 세션·획득 경과·마지막 신호 나이) / `reclaimed`(사유). **`lane-turn` 처방을 언급하지 마라** — 그 통로는 ③ 에서 생긴다. 없는 통로를 가리키는 문구는 이 레포가 결함으로 분류하는 부류다.

- [ ] **Step 4: 개수 락 셋을 고친다**

`TestToolTableIsSix` → **`TestToolTableIsSeven`** 으로 이름까지 바꾼다(일곱을 단정하면서 이름이 여섯이면 grep 하는 사람이 틀린 답을 얻는다). `add_coordinate_test.go` 의 개수 단정은 **뺀다** — 개수의 정본은 한 시험이어야 한다. `server_test.go:244` 도 갱신. 도구 설명 90자 상한을 지킨다.

- [ ] **Step 5: 초록 확인 후 커밋**

Run: `go test ./internal/mcpsrv/ -race`

```bash
git commit -am "feat(flightdeck): MCP land 도구 — 회수는 열지 않는다"
```

---

### Task 7: REST · CLI · 열화 — 탈출구를 만든다

**Files:**
- Modify: `internal/api/api.go`(라우트 둘) · 새 `internal/api/handlers_landing.go` · `idempotency_persist_test.go` 표
- Modify: `cmd/fd/wire.go`(요청 타입) · `cmds.go`(`runLand`·`runLaneRelease`) · `main.go`(case) · `offline.go` · `client.go` · `outbox.go` · `mcpbackend.go`
- Create: `cmd/fd/land_seam_test.go`

**Interfaces:**
- Produces: `POST /api/v1/landing` · `POST /api/v1/landing/rows/{id}/release` · `fd land` · `fd lane release`

- [ ] **Step 1: 이음매 시험을 먼저 쓴다**

`wire.go` 의 요청 구조체와 `internal/api` 의 것이 **필드명으로만 이어져 있고 시험이 없다**는 알려진 결함이 있다(판단 `01KZ56B7…`). 갈라지면 서버가 오류 없이 0값을 받는다. `harness_test.go`·`mcp_seam_test.go` 관용으로 실제 왕복을 눌러 확인하는 시험을 **먼저** 쓴다.

- [ ] **Step 2: 열화 표에 넷을 넣는다 — 각각 다른 사유로**

```go
	case CmdLandAcquire:
		return Offline{Mode: OfflineRefuse,
			Reason: "레인 취득은 오프라인에 성립할 수 없다 — 배타의 정본이 서버의 DB 제약이라 " +
				"여기서 '내 차례'를 만들면 두 세션이 동시에 랜딩한다"}
	case CmdLandReport, CmdLandLeave:
		return Offline{Mode: OfflineRefuse,
			Reason: "레인 반납은 재생 대상이 아니다 — 재생 시점에 이미 남이 잡았을 수 있고, " +
				"그러면 남의 점유를 반납한다"}
	case CmdLaneRelease:
		return Offline{Mode: OfflineRefuse,
			Reason: "회수는 사람의 판단이라 재생 대상이 아니다"}
```

**사유를 뭉개면 다음 사람이 반납만 아웃박스로 연다.**

- [ ] **Step 3: 아웃박스 방어를 명시적으로 만든다**

지금 `offline.go` 의 아웃박스 가지에 `"land"` 한 낱말을 끼워 넣으면 그대로 아웃박스로 가고 **막는 코드가 없다.** 순수 함수를 두고 `client.go` 의 아웃박스 진입 직전에 부른다:

```go
// OutboxEligible 은 이 명령이 아웃박스에 들어가도 되는지 본다. 순수 함수다.
//
// ★ 적격 집합이 {note} 하나인 것이 설계다 — 판단만이 원리적으로 파생 불가하다.
//   여기를 넓히려면 이 함수와 그 시험을 **함께** 고쳐야 한다. JudgeOffline 한 자리만
//   고쳐서 새 명령이 아웃박스로 새는 경로를 이 함수가 막는다.
func OutboxEligible(cmd, path string) (bool, string)
```

시험: "아웃박스 적격 집합 == {note}, 적격 경로 == `/api/v1/judgments`".

- [ ] **Step 4: `land` 응답을 캐시에 넣지 않는다**

`Client.Read` 는 `JudgeOffline` 을 안 보고 성공한 GET 을 조건 없이 캐시한다. **`land` 는 전 가지가 POST 이고 `Healthz`(`client.go:312-315`) 처럼 `c.do` 직행이다.** 주석에 그 이유를 옮긴다: "'지금 내 차례인가'에 캐시로 답하면 그 질문 자체가 무의미해진다."

`IdempotencyStable` 에는 넣지 않는다(FreshKey 쪽) — 응답에 "지금 상태"가 실리므로 `pick` 과 같은 처지다.

- [ ] **Step 5: CLI 둘을 만든다**

`fd land`(무인자/`--ok`/`--fail <사유>`/`--leave <사유>`) 와 `fd lane release --row <id> --reason "..."`.

- [ ] **Step 6: 전체 초록 + 조립 확인**

Run: `go build ./... && go vet ./... && gofmt -l . && go test ./... -race`

- [ ] **Step 7: 커밋**

```bash
git commit -am "feat(flightdeck): land REST·CLI 와 열화 정책 — 물린 레인의 탈출구를 연다"
```

---

### Task 8: 보드가 줄을 낸다 + 문서 — 단계 ① 마감

**Files:**
- Modify: `internal/service/board.go` — `BoardView.Lane *LaneView` 채우기 ⚠ **다른 세션(01KZ71DM)이 이 파일을 쥐고 있다. 착수 전 `board` 로 확인하고 `note(kind='ask')` 로 알려라.**
- Modify: `plugins/flightdeck/DESIGN.md` — `:58`·`:290`·`:303`(도구 6→7) · `:156`(테이블 수) · §2 Tier B(① 착수됨)
- Modify: `internal/mcpsrv/render.go` — 보드 렌더의 레인 절

- [ ] **Step 1: "읽는 쪽이 사라지면 빨개지는" 시험을 쓴다**

```go
// TestLandingQueueHasAProductionReader — landing_queue 를 읽는 프로덕션 호출자가 0이 되면 빨강.
//   ★ session_workspace 는 이 시험이 없어서 "그 축은 이미 있다"의 거짓 근거가 됐다.
//     빨개지는 것은 결함이 아니라 **주석의 만료 통지**다: 그때 할 일은 읽는 쪽을 되살리거나
//     이 표를 지우는 것 둘 중 하나다.
```

- [ ] **Step 2: 보드에 레인을 싣는다.** `Lane` 이 nil 인 것과 `Entries` 가 빈 것을 렌더가 다르게 낸다("질의는 돌았다"를 0건 문구에 적는다).

- [ ] **Step 3: DESIGN.md 를 고친다.** 도구 수 6→7 과 그 근거(§6 컨텍스트 예산 재계산), 테이블 수를 실제 값으로.

  ★ **테이블 수는 숫자만 적지 마라.** 실측: 사람이 선언한 것 23(`CREATE TABLE` 21 + `CREATE VIRTUAL TABLE` 1 + 증분 1), `landing_queue` 를 더하면 **24**. 그런데 살아 있는 DB 의 `sqlite_master` 는 **29**(FTS5 그림자 넷 + `sqlite_sequence`)를 낸다. §3 은 데이터 모델을 말하므로 24 를 적되 **두 수가 왜 다른지를 그 자리에 함께 적는다.** 안 적으면 다음 사람이 `sqlite_master` 를 세고 또 어긋났다고 판단한다 — 실제로 다른 세션이 "28개"로 관측해 넘겨 왔다(판단 `01KZ7DKQ3QHKH75X4XY0YDPFMC`).
 §2 Tier B 의 "아직 정하지 않은 것: ①을 실제로 착수할지"를 착수 사실로 갱신한다. ⚠ `DESIGN.md` 는 두 세션과 겹친다 — 01KZ5PBT 는 §7 398행, 01KZ7214 는 §6 297행만 만진다고 답해 왔다. 그 줄들을 피한다.

- [ ] **Step 4: 전체 초록 + 커밋**

```bash
go test ./... -race && git commit -am "feat(flightdeck): 보드가 랜딩 줄을 낸다 + 설계 문서 갱신"
```

**단계 ① 완료 조건:** `go test ./... -race` 초록 · `fd land` 로 줄을 서고 차례를 받을 수 있다 · `fd lane release` 로 물린 레인을 풀 수 있다 · 보드가 줄을 낸다.

---

# 단계 ② — 사람이 화면에서 본다

**끝났을 때:** 대시보드가 레인을 그리고 회수 버튼이 **실제로 동작한다**(오늘 기존 버튼 둘은 400 이다).

### Task 9: 화면 쓰기를 되살린다 — 기존 결함 수정

**Files:** `internal/api/middleware.go`·`api.go`(chain) · 새 `internal/api/screen.go` · `internal/web/dashboard.gohtml`·`page.go` · 새 `cmd/fd/web_form_gate_test.go`

- [ ] **Step 1: 지금 400 인 것을 시험으로 박는다.** 조립된 서버(`api.NewServer` + Fallback)에 폼 인코딩 POST 를 보내 `POST /actions/drop` 이 400 `idempotency_key_required` 인지 확인한다. **이 시험은 지금 빨갛다(정확히는 400 을 단정하면 초록, 200 을 단정하면 빨강) — ② 가 끝나면 200 이다.** `actions_test.go` 는 사슬 밖 핸들러를 눌러 이 축을 원리적으로 못 본다.
- [ ] **Step 2: `withScreenWrite` 를 만든다.** 폼 action 쿼리의 키를 헤더로 올린다(본문은 안 건드린다 — `withIdempotency` 의 본문 읽기와 안 부딪힌다). chain 에서 `withIdempotency` **앞**에 꽂는다. 키는 렌더 시각 기반(`web:<대상>:<렌더 unix>`)이라 더블클릭은 접히고 새로고침은 다시 눌린다.
- [ ] **Step 3: 출처 대조를 같은 커밋에 넣는다.** 이 레포엔 CSRF 토큰·SameSite·Origin 검사가 0건이고 **지금은 헤더 요구가 우연히 그 역할을 한다.** 쿼리로 우회하면 그 방어가 사라지므로 화면 액션 경로 한정 `Sec-Fetch-Site`/`Origin` 대조를 함께 넣는다. **없애는 것을 대체물 없이 없애지 않는다.**
- [ ] **Step 4: 기존 버튼 둘이 200 인지 확인하고 커밋.**

### Task 10: 레인 절과 회수 버튼

**Files:** `internal/web/page.go`·`actions.go`·`format.go`·`web.go`·`dashboard.gohtml` · `render_test.go`

- [ ] **Step 1:** 레인 절을 **④ 랜딩 이력 안쪽 `<h3>`** 로 넣는다(새 `<section>` 은 `render_test.go:176` 의 절 개수 6을 깬다). 줄은 전부 낸다(창으로 안 거른다). 0건 문장은 자기 절이 따로 가진다 — `blockedPanel` 이 자원 0건을 패널 전체 Empty 로 뭉개는 모양을 베끼지 마라.
- [ ] **Step 2:** 조회는 `h.svc.LandingLane(ctx, project)` 하나로 한다. **`query.go` 에 생 SQL 을 두 번째로 만들지 마라** — 판정을 두 자리에 두면 한쪽만 고치는 순간 조용히 어긋난다(`board.go:144-147`).
- [ ] **Step 3:** 새 `ActionKind` 는 `lane` 계열 이름을 피한다(`actions.go:59-60` 이 `"lane"·"lane-stop"·"lane-resume"` 를 이름으로 거절하고 그 거절은 여전히 참이다 — 레인 정지/재개는 Tier B). `lane-release` 를 통과 목록에 더하고 거절 문구를 "정지/재개는 여전히 Tier B, 회수는 열렸다"로 가른다.
- [ ] **Step 4:** `render_test.go` 의 POST==2 → 3, reason required==2 → 3 갱신. `format.go:1-16`·`actions.go:14-18`·`web.go:95-97` 의 "쓰기는 넷/라우트는 셋" 독스트링 셋도 함께 — 버튼을 더하는 순간 셋 다 거짓이 된다. DESIGN `:358`·`:367`(버튼 넷→다섯).

---

# 단계 ③ — 차례가 왔음을 민다

### Task 11: `lane-turn` 처방

> ★ 개정(2026-08-05) — **이 과제는 끝났다.** 브랜치 `fd-lane-residuals`(base `64037d1`)의
> 커밋 셋이다: `034f47d`(judge 처방 + 접힘 시험) · `50cd987`(service 배선) ·
> `7e1c15f`(`RenderLand` 대기 문구). 아래 상자를 닫았고, **계획과 다르게 한 것 하나**를 함께 적는다.
>
> - **Step 3 은 계획의 답을 기각했다.** 계획은 "`lane-turn` 은 이 세션에게만 의미 있는 사건이므로
>   `overlap` 뒤"라고 적었으나 랜딩판은 **맨 앞**이다(`lane-turn → overlap → outside → unclaimed
>   → silent`). 근거 판단 `01KZ8ZYHV0DHS237FNBCRMY2MJ`: `FoldPrescriptions` 는 `ps[:PrescribeMax]`
>   로 **뒤를 자르고**, `PrescribeMax` 주석이 "접힌 것도 호출자가 전부 발화 기록한다"를 계약으로
>   못박았고, `suppressed` 는 `silent` 외 모든 키를 무조건 누른다 — 셋을 이으면 한 번 접힌
>   `lane-turn` 은 그 줄 행에 대해 **영구히** 사라지고, 그 세션은 레인을 안 쥔 채 남아 뒤에 선
>   전원의 랜딩이 선다. 그리고 그 실패는 화면에 안 뜨고 원장에는 "정상적으로 접혔다"로만 남는다.
>   대가는 명시했다: 상한을 넘는 턴에서 접히는 쪽이 `overlap` 이 된다. `Prescribe` 독스트링의
>   "`overlap` 이 맨 앞인 이유는 **그것만이** 남이 알아야 하는 사건이기 때문"도 이 커밋에서
>   거짓이 되므로 개정 블록으로 함께 고쳤다. 기각한 대안 둘(`FoldPrescriptions` 에 예외를 파는
>   것 · 접힌 처방을 발화 기록에서 빼는 것)은 그 판단에 근거와 함께 남아 있다.
> - **Step 4 는 복원이 아니라 교체였다.** ①·② 가 넣어 둔 "차례는 서버가 밀어주지 않는다"가
>   통로가 서는 순간 **거짓**이 돼서, 문장을 되살린 것이 아니라 그 줄을 갈아 끼웠다. 새 문구는
>   `lane-turn` 이 `(세션 × 키)` **1회**라는 사실과 다시 묻는 길을 함께 말한다 — 폴링을 닫는
>   문장을 쓰면 "가만히 있어도 된다"로 읽히고 그 세션 뒤로 줄 전원이 선다. 허용은 `waiting`
>   하나뿐이고 나머지 넷은 여전히 금지다(근거가 "통로가 없다"에서 "그 자리에서 할 말이
>   아니다"로 바뀌었다).
> - **만진 파일이 위 Files 보다 둘 많다** — 시험 둘(`internal/service/prescribe_test.go` ·
>   `internal/mcpsrv/land_test.go`)이 함께 움직였다.
> - **Step 5 를 잠근 것 — `TestPrescribe` 표 케이스 셋 + 시험 함수 넷:**
>   `judge/prescribe_test.go:20 TestPrescribe` 의 케이스 셋("레인 차례가 오면 lane-turn 이
>   뜬다" · "같은 줄 행에는 다시 안 뜬다" · "새 줄 행에는 다시 뜬다") · `judge/prescribe_test.go:296
>   TestLaneTurnSurvivesFolding`(overlap 을 상한 이상으로 깔고 생존과 맨 앞을 함께 단정) ·
>   `service/prescribe_test.go:332 TestLaneTurnFiresOnceWhenTheLaneBecomesMine` ·
>   `service/prescribe_test.go:399 TestLaneTurnReturnsForANewQueueRow` ·
>   `mcpsrv/land_test.go:128 TestRenderLandWaitingPointsAtLaneTurn`(옛
>   `TestRenderLandNeverMentionsLaneTurn` 을 지우지 않고 뒤집은 것).
>
> ★ **이 파일에서 `[x]` 는 여기가 처음이다.** 단계 ①·②(Task 1~10)는 이미 랜딩했는데도 상자가
> 전부 `[ ]` 로 남아 있고, 이 레포의 계획 문서 열한 개 중 `[x]` 를 쓴 것이 하나도 없다.
> 즉 **이 문서에서 빈 상자는 "안 했다"는 뜻이 아니다** — 완료의 정본은 커밋과 판단이다.

**Files:** `internal/judge/prescribe.go` · `prescribe_test.go` · `internal/service/prescribe.go` · `internal/mcpsrv/render.go`(대기 문구 복원)

- [x] **Step 1:** `PrescribeLaneTurn = "lane-turn"` 을 키에 더한다. 처방은 상태가 아니라 **전이**에서만, `(세션 × Key)` 당 1회 뜬다.
- [x] **Step 2:** **억제 키에 줄 행 id 를 넣는다** — `lane-turn:<row id>`. 안 넣으면 한 번 차례를 받고 실패해 다시 선 세션에게 두 번째 차례가 영영 안 뜬다.
- [x] **Step 3:** `Prescribe` 의 순서에 어디에 끼울지 정한다. `overlap` 이 맨 앞인 이유(그것만이 남이 알아야 하는 사건)를 읽고, `lane-turn` 은 **이 세션에게만 의미 있는 사건**이므로 그 뒤다. — **정하는 일은 끝났고 답은 반대였다(맨 앞). 위 개정 참고.**
- [x] **Step 4:** `RenderLand` 의 대기 문구에 "차례가 오면 처방이 알린다"를 되살린다. ①·② 동안 뺐던 문장이다. — **복원이 아니라 교체로 했다. 위 개정 참고.**
- [x] **Step 5:** 시험 — 차례가 오면 정확히 한 번 뜨고, 같은 줄 행에는 다시 안 뜨고, 새 줄 행에는 다시 뜬다.

---

## 랜딩 순서와 조율

각 단계 끝에서 main 에 ff 로 넣고 다음 단계를 그 위에서 시작한다. **①②③ 를 한 덩어리로 다시 묶지 마라** — 그 제시 방식이 애초에 Tier B 착수를 막은 원인이다(DESIGN §2).

지금 겹치는 세션과 자리:

| 파일 | 쥔 세션 | 답해 온 범위 |
|---|---|---|
| `service/board.go` | 01KZ71DM | 미확인 — Task 8 전에 물어라 |
| `service/pick.go` | 01KZ7214·01KZ73Z0 | 이 계획은 안 건드린다 |
| `api/api.go` | 01KZ5J2H | 미확인 — Task 7 전에 물어라 |
| `DESIGN.md` | 01KZ5PBT(§7 398행) · 01KZ7214(§6 297행) | 그 줄들을 피한다 |

`fd-design-table-count-drift` 항목이 §3 테이블 수를 고치려 한다 — 이 계획의 Task 8 이 실제 값으로 고치므로 그 항목은 랜딩 뒤 확인하고 닫는다(판단 `01KZ74PF3RPG9ZFHHJ9YGHM9BK`).
