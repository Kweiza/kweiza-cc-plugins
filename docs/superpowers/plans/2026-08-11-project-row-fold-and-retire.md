# 프로젝트 줄 접기·잔해 치우기 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 웹 대시보드 헤더의 프로젝트 줄에 핀/보관 축을 주어 실제 쓰는 것만 남기고, 오등록된 잔해를 CLI 로 지울 수 있게 한다.

**Architecture:** `project` 테이블에 `pinned_at`·`archived_at` 두 컬럼을 더한다(마이그레이션 007). 화면은 핀만 줄에 내고 나머지를 `<details>` 로 접으며, 폼 하나(`POST /actions/project-view`)가 네 축(pin·unpin·archive·unarchive)을 명시적 값으로 받는다. 진짜 삭제는 화면에 없고 `fd project rm` 에만 있으며, 항목이나 판단이 있으면 거절한다.

**Tech Stack:** Go 1.x · `modernc.org/sqlite`(순수 Go, CGO 없음) · `html/template` · 표준 `net/http` `ServeMux` · JS 라이브러리 없음

**설계 원문:** `docs/superpowers/specs/2026-08-11-project-row-fold-and-retire-design.md`

## Global Constraints

- **작업 디렉토리는 `plugins/flightdeck/server/` 다.** 모든 `go` 명령을 거기서 돌린다 — 모듈 밖에서 돌면 gofmt/vet 이 빈 디렉토리를 검사하고 조용히 통과한다.
- **랜딩 전 관문 다섯 줄** — `gofmt -l .`(빈 출력) · `go vet ./...` · `go test ./...` · `GOOS=darwin GOARCH=arm64 go vet ./...` · `GOOS=windows GOARCH=amd64 go vet ./...`. `go build` 는 `_test.go` 를 건너뛰므로 교차 검증은 반드시 `go vet` 이다.
- **주석과 문구는 전부 한글이다.** 이 저장소의 모든 주석·오류 문구·화면 문구가 한글이고, 판단의 근거를 주석에 적는 것이 이 저장소의 규율이다. 「왜 이렇게 했나」와 「무엇을 기각했나」를 함께 적는다.
- **`schema.sql` 을 고치지 않는다.** 표·컬럼의 정의는 한 자리에만 둔다 — 빈 DB 도 `schema.sql` → 증분 전부를 거쳐 올라가므로, 증분에 넣은 것을 `schema.sql` 에도 적으면 신규 설치와 업그레이드가 다른 모양의 DB 를 갖는다(`store.go` 의 `BaseSchemaVersion` 주석).
- **시각 표기는 `timeLayout = "2006-01-02T15:04:05.000000Z"`** 다. `time.RFC3339Nano` 를 쓰면 폭이 흔들려 사전순 정렬이 시간순과 어긋난다. 저장은 `fmtTime`, 해석은 `parseTime` 을 쓴다.
- **커밋 메시지는 한글이고 `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>` 로 끝난다.**
- 브랜치는 `fd-project-row-cannot-fold-or-retire-dead-projects` 이고 워크트리는 `.flightdeck/worktrees/fd-project-row-cannot-fold-or-retire-dead-projects` 다. `main` 에 직접 커밋하지 않는다.

## 파일 구조

| 파일 | 책임 | 태스크 |
|---|---|---|
| `internal/store/migrations/007_project_view_axis.sql` (새) | 컬럼 둘을 더한다 | 1 |
| `internal/store/store.go:47-80` | 증분 등록 · `SchemaVersion` 6→7 | 1 |
| `internal/model/types.go:112-120` | `Project` 에 필드 둘 | 1 |
| `internal/store/project.go` | 컬럼 목록 · `Scan` · `SetProjectView` · `RemoveProject` · `ProjectRefCounts` | 1, 5 |
| `internal/store/project_view_test.go` (새) | 핀·보관의 원장 단정 | 1 |
| `internal/store/project_remove_test.go` (새) | 삭제 거절·순서·고아 단정 | 5 |
| `internal/web/page.go` | `ProjectRow` 뷰모델 · `ProjectNav` 조립 | 2 |
| `internal/web/dashboard.gohtml:89-93` | 줄 렌더 · `<details>` · 폼 | 2, 3 |
| `internal/web/actions.go` | `JudgeProjectView` · `projectView` 핸들러 | 3 |
| `internal/web/web.go:100-128` | 라우트 등록 · 주석 갱신 | 3 |
| `internal/web/project_nav_test.go` (새) | 접힘 규율 넷 | 2 |
| `internal/web/project_view_test.go` (새) | 판정·쓰기 단정 | 3 |
| `internal/web/render_test.go:433-495` | 폼 락 갱신 | 3 |
| `internal/service/project.go` (새) | `ListProjectsWithCounts` · `RemoveProject` | 4, 5 |
| `internal/api/handlers_projects.go` (새) | `GET /api/v1/projects` · `POST /api/v1/projects/{id}/remove` | 4, 5 |
| `internal/api/api.go:311-350` | 라우트 등록 | 4, 5 |
| `cmd/fd/project.go` (새) | `fd project ls` · `fd project rm` | 4, 5 |
| `cmd/fd/main.go:138-152` | 서브명령 분기 | 4 |
| `cmd/fd/wire.go` | 요청·응답 타입 | 4, 5 |
| `plugins/flightdeck/DESIGN.md` | §웹UI · §CLI 갱신 | 6 |

---

### Task 1: 원장에 핀·보관 축을 만든다

**Files:**
- Create: `internal/store/migrations/007_project_view_axis.sql`
- Create: `internal/store/project_view_test.go`
- Modify: `internal/store/store.go:47-80` (embed · `SchemaVersion` · `migrations`)
- Modify: `internal/model/types.go:112-120` (`Project` 구조체)
- Modify: `internal/store/project.go:24` (`projectCols`), `:33-58` (`UpsertProject`), `:97-130` (`ListProjects`), `getProject`

**Interfaces:**
- Produces:
  - `model.Project.PinnedAt time.Time` · `model.Project.ArchivedAt time.Time` — 제로값이 「아님」이다
  - `func (t *Tx) SetProjectView(id string, pinned, archived time.Time) error` — 제로값을 주면 NULL 로 쓴다
  - `func (s *Store) SetProjectView(ctx context.Context, id string, pinned, archived time.Time) error`
  - `store.SchemaVersion == 7`

- [ ] **Step 1: 증분 SQL 을 쓴다**

`internal/store/migrations/007_project_view_axis.sql`:

```sql
-- 007 · 프로젝트에 표시 축을 준다 — 핀과 보관 (schema_version 6 → 7)
--
-- ★ 무엇을 위한 컬럼인가: 헤더의 프로젝트 줄이 ListProjects 전부를 그대로 내는데,
--   실측 11건 중 일이 도는 것은 2건이고 4건은 워크트리·프로브 경로가 프로젝트로 잘못
--   등록된 잔해다. 사람이 고른 것만 줄에 남기고 나머지를 접기 위한 자리다.
--
-- ★ 불리언이 아니라 시각인 이유는 created_at 과 같다 — 언제 접었는지가 없으면 되짚을 수
--   없다. 보관 목록이 "언제 보관했는지"와 "그 뒤에 세션이 열렸는지"를 견주려면 시각이 있어야
--   한다. NULL 이 "아님"이다.
--
-- ★ 이 축은 표시 계층이다. 항목·판단·선점·랜딩 어디에도 안 닿고, 접힌 프로젝트도
--   ?project= 로 그대로 열린다. 그래서 화면의 이 폼은 "파생물에 쓰는 폼" 상한 넷에서 빠진다
--   (web/render_test.go 의 그 자리에 근거를 적어 뒀다).
--
-- ★ 순수 가산이다 — ALTER TABLE ADD COLUMN 뿐이라 migrate_guard_test.go 의
--   destructiveOps 여섯 축(DROP TABLE·DROP COLUMN·RENAME·DELETE FROM·UPDATE…SET·
--   INSERT…SELECT) 어느 것에도 안 걸린다. 예외 등재가 필요 없다.
--
-- ★ 멱등이 아니다(ALTER 는 두 번 돌면 "duplicate column name" 으로 죽는다). 그것으로 족하다 —
--   증분은 schema_version 으로 정확히 한 번만 돈다.

ALTER TABLE project ADD COLUMN pinned_at   TEXT;
ALTER TABLE project ADD COLUMN archived_at TEXT;
```

- [ ] **Step 2: 증분을 등록한다**

`internal/store/store.go` — embed 를 `migration006` 아래에 더한다:

```go
//go:embed migrations/007_project_view_axis.sql
var migrationProjectViewAxis string
```

`SchemaVersion` 을 올린다:

```go
const SchemaVersion = 7
```

`migrations` 목록 끝에 더한다:

```go
	{To: 7, Name: "프로젝트에 핀·보관 축", SQL: migrationProjectViewAxis},
```

- [ ] **Step 3: 모델에 필드 둘을 더한다**

`internal/model/types.go` 의 `Project`:

```go
type Project struct {
	ID            string
	Path          string
	RemoteURL     string // 비어 있어도 Tier A 는 완전히 돈다
	DefaultBranch string
	Config        string // .flightdeck.yaml 캐시(JSON). 정본은 레포 안의 파일
	ConfigFromSHA string
	CreatedAt     time.Time
	// PinnedAt·ArchivedAt 는 **표시 축**이다. 제로값이 "아님"이고, 둘 다 사람이 화면에서
	// 정한다. 판정 경로(겹침·처방·추천)는 이 둘을 안 읽는다 — 읽는 순간 접어 둔 프로젝트가
	// 조용히 조율에서 빠진다.
	PinnedAt   time.Time
	ArchivedAt time.Time
}
```

- [ ] **Step 4: 실패하는 시험을 쓴다**

`internal/store/project_view_test.go`:

```go
package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// openTestStore 는 빈 DB 를 하나 연다.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "fd.db"))
	if err != nil {
		t.Fatalf("DB 열기 실패: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestProjectViewAxisSurvivesUpsert 는 이 증분의 **유일한 함정**을 잡는다.
//
// ★ UpsertProject 는 세션이 열릴 때마다 돈다(service/session.go 의 자동 등록). 핀·보관을
// 그 ON CONFLICT DO UPDATE 목록에 넣으면 훅이 세션을 열 때마다 사람이 고른 것이 날아가고,
// 그 손실은 어느 화면에도 안 뜬다 — 다음에 볼 때 그냥 안 켜져 있을 뿐이다.
// created_at 이 같은 이유로 이미 그 목록 밖에 있다.
func TestProjectViewAxisSurvivesUpsert(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	base := model.Project{ID: "p1", Path: "/tmp/p1", DefaultBranch: "main"}
	if err := s.UpsertProject(ctx, base); err != nil {
		t.Fatalf("첫 등록 실패: %v", err)
	}

	pin := time.Date(2026, 8, 11, 3, 4, 5, 0, time.UTC)
	if err := s.SetProjectView(ctx, "p1", pin, time.Time{}); err != nil {
		t.Fatalf("핀 설정 실패: %v", err)
	}

	// 세션이 다시 열린 것과 같다 — 경로가 바뀐 재등록.
	again := model.Project{ID: "p1", Path: "/tmp/p1-moved", DefaultBranch: "main"}
	if err := s.UpsertProject(ctx, again); err != nil {
		t.Fatalf("재등록 실패: %v", err)
	}

	got, err := s.GetProject(ctx, "p1")
	if err != nil {
		t.Fatalf("조회 실패: %v", err)
	}
	if !got.PinnedAt.Equal(pin) {
		t.Fatalf("upsert 가 핀을 지웠다: %v (기대 %v) — "+
			"ON CONFLICT DO UPDATE 목록에 pinned_at 이 들어갔는지 보라", got.PinnedAt, pin)
	}
	if got.Path != "/tmp/p1-moved" {
		t.Fatalf("upsert 가 path 를 안 고쳤다: %q — 이 시험이 전제하는 재등록이 안 일어났다", got.Path)
	}
}

// TestProjectViewRoundTrip 은 두 값이 목록 조회에서도 제자리에 오는지 본다.
//
// ★ projectCols 와 Scan 순서가 어긋나면 전부 문자열이라 타입 오류 없이 조용히 엉뚱한 값이
// 들어간다(그 상수의 주석이 경고하는 실패다). 컬럼을 더한 이 회차가 정확히 그 부류다.
func TestProjectViewRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	pin := time.Date(2026, 8, 11, 1, 2, 3, 0, time.UTC)
	arc := time.Date(2026, 8, 12, 4, 5, 6, 0, time.UTC)
	for _, p := range []model.Project{
		{ID: "a", Path: "/tmp/a", DefaultBranch: "main"},
		{ID: "b", Path: "/tmp/b", DefaultBranch: "main"},
	} {
		if err := s.UpsertProject(ctx, p); err != nil {
			t.Fatalf("등록 실패: %v", err)
		}
	}
	if err := s.SetProjectView(ctx, "a", pin, time.Time{}); err != nil {
		t.Fatalf("핀 실패: %v", err)
	}
	if err := s.SetProjectView(ctx, "b", time.Time{}, arc); err != nil {
		t.Fatalf("보관 실패: %v", err)
	}

	list, err := s.ListProjects(ctx)
	if err != nil {
		t.Fatalf("목록 실패: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("프로젝트 %d건, 기대 2건", len(list))
	}
	byID := map[string]model.Project{}
	for _, p := range list {
		byID[p.ID] = p
	}
	if !byID["a"].PinnedAt.Equal(pin) || !byID["a"].ArchivedAt.IsZero() {
		t.Fatalf("a 의 축이 틀렸다: pinned=%v archived=%v", byID["a"].PinnedAt, byID["a"].ArchivedAt)
	}
	if !byID["b"].ArchivedAt.Equal(arc) || !byID["b"].PinnedAt.IsZero() {
		t.Fatalf("b 의 축이 틀렸다: pinned=%v archived=%v", byID["b"].PinnedAt, byID["b"].ArchivedAt)
	}
	// path 가 그대로인지도 본다 — 컬럼 순서가 밀리면 여기가 시각 문자열로 오염된다.
	if byID["a"].Path != "/tmp/a" {
		t.Fatalf("a 의 path 가 %q 다 — projectCols 와 Scan 순서가 어긋났다", byID["a"].Path)
	}
}

// TestSetProjectViewClearsWithZero 는 제로값이 NULL 로 간다는 단정이다 — 핀 해제의 경로다.
func TestSetProjectViewClearsWithZero(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.UpsertProject(ctx, model.Project{ID: "p", Path: "/tmp/p", DefaultBranch: "main"}); err != nil {
		t.Fatalf("등록 실패: %v", err)
	}
	pin := time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)
	if err := s.SetProjectView(ctx, "p", pin, time.Time{}); err != nil {
		t.Fatalf("핀 실패: %v", err)
	}
	if err := s.SetProjectView(ctx, "p", time.Time{}, time.Time{}); err != nil {
		t.Fatalf("해제 실패: %v", err)
	}
	got, err := s.GetProject(ctx, "p")
	if err != nil {
		t.Fatalf("조회 실패: %v", err)
	}
	if !got.PinnedAt.IsZero() {
		t.Fatalf("핀이 안 풀렸다: %v", got.PinnedAt)
	}
}

// TestSetProjectViewUnknownProject 는 없는 프로젝트에 쓰면 ErrNotFound 라는 단정이다.
// UPDATE 는 0행이어도 성공하므로 이 확인이 없으면 오타가 조용히 성공한다.
func TestSetProjectViewUnknownProject(t *testing.T) {
	s := openTestStore(t)
	err := s.SetProjectView(context.Background(), "없다", time.Now().UTC(), time.Time{})
	if err == nil {
		t.Fatal("없는 프로젝트에 쓰는데 성공했다 — UPDATE 0행을 확인하지 않았다")
	}
}
```

- [ ] **Step 5: 시험이 실패하는 것을 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/store/ -run 'TestProjectView|TestSetProjectView' -v`
Expected: 컴파일 실패 — `s.SetProjectView undefined` · `got.PinnedAt undefined`

- [ ] **Step 6: `project.go` 를 고친다**

`projectCols` 에 둘을 더한다(순서가 `Scan` 과 짝이다):

```go
const projectCols = `id, path, remote_url, default_branch, config, config_from_sha, created_at, pinned_at, archived_at`
```

`UpsertProject` 의 `ON CONFLICT DO UPDATE` 목록은 **그대로 둔다.** 그 자리에 근거를 적는다:

```go
// UpsertProject 는 프로젝트를 등록하거나 갱신한다.
//
// created_at 은 첫 등록 시각을 보존한다 — 재등록이 나이를 0으로 되돌리면
// "언제부터 있던 프로젝트인가"가 사라진다.
//
// ★ pinned_at·archived_at 도 같은 이유로 **갱신 목록 밖**이다. 이 함수는 세션이 열릴 때마다
// 돌고(service/session.go 의 자동 등록), 목록에 넣으면 훅이 세션을 열 때마다 사람이 고른
// 표시 축이 날아간다. 그 손실은 어느 화면에도 안 뜬다 — 다음에 볼 때 그냥 안 켜져 있을 뿐이다.
// 그 축을 쓰는 문은 SetProjectView 하나뿐이다.
```

행 해석 함수(`ListProjects` 와 `getProject` 가 공유하는 자리)에 둘을 더한다. 두 자리가 각자
`Scan` 을 적고 있으면 **먼저 하나로 모은다** — 컬럼이 늘 때마다 두 벌이 갈리는 자리다:

```go
// scanProject 는 projectCols 순서의 한 행을 읽는다.
//
// ★ ListProjects 와 getProject 가 이것을 공유한다. Scan 목록이 두 벌이면 컬럼을 더할 때
// 한쪽만 고쳐지고, 전부 문자열이라 타입 오류도 안 난다 — projectCols 주석이 경고하는 실패다.
func scanProject(sc interface{ Scan(...any) error }) (model.Project, error) {
	var p model.Project
	var remote, config, fromSHA, pinned, archived sql.NullString
	var created string
	if err := sc.Scan(&p.ID, &p.Path, &remote, &p.DefaultBranch, &config, &fromSHA,
		&created, &pinned, &archived); err != nil {
		return model.Project{}, err
	}
	p.RemoteURL, p.Config, p.ConfigFromSHA = str(remote), str(config), str(fromSHA)
	var err error
	if p.CreatedAt, err = parseTime(created); err != nil {
		return model.Project{}, err
	}
	if p.PinnedAt, err = parseNullTime(pinned); err != nil {
		return model.Project{}, err
	}
	if p.ArchivedAt, err = parseNullTime(archived); err != nil {
		return model.Project{}, err
	}
	return p, nil
}

// parseNullTime 은 NULL 을 제로값으로 읽는다. 제로값이 "아님"이다.
func parseNullTime(v sql.NullString) (time.Time, error) {
	if !v.Valid || strings.TrimSpace(v.String) == "" {
		return time.Time{}, nil
	}
	return parseTime(v.String)
}
```

`SetProjectView` 를 더한다:

```go
// SetProjectView 는 프로젝트의 표시 축(핀·보관)을 정한다. 제로값은 NULL 이다.
//
// ★ 이 축은 표시 계층이라 사유를 안 받는다. 이 화면에서 사유가 필수인 셋(선점 회수 ·
// 항목 폐기 · 줄 회수)은 전부 남의 일을 뺏거나 되돌릴 수 없는 것인데, 핀과 보관은 둘 다
// 아니다 — 내 판이고 클릭 하나로 돌아온다. 되짚을 거리는 시각과 event 가 남긴다.
func (t *Tx) SetProjectView(id string, pinned, archived time.Time) error {
	res, err := t.tx.ExecContext(t.ctx, `
		UPDATE project SET pinned_at = ?, archived_at = ? WHERE id = ?`,
		nullTime(pinned), nullTime(archived), id)
	if err != nil {
		return fmt.Errorf("프로젝트 표시 축 갱신 실패(id=%q): %w", clip(id, 64), err)
	}
	// ★ UPDATE 는 0행이어도 성공한다. 확인하지 않으면 프로젝트 id 오타가 조용히 성공하고,
	//   화면은 "눌렀는데 아무 일도 안 일어난다"가 된다 — 그 증상에서 원인이 안 보인다.
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("프로젝트 표시 축 갱신 결과 확인 실패(id=%q): %w", clip(id, 64), err)
	}
	if n == 0 {
		return fmt.Errorf("프로젝트 %q: %w", clip(id, 64), ErrNotFound)
	}
	return nil
}

// SetProjectView 는 단발 트랜잭션으로 감싼 것이다.
func (s *Store) SetProjectView(ctx context.Context, id string, pinned, archived time.Time) error {
	return s.Tx(ctx, func(t *Tx) error { return t.SetProjectView(id, pinned, archived) })
}

// nullTime 은 제로값을 NULL 로 낸다.
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return fmtTime(t)
}
```

- [ ] **Step 7: 시험이 통과하는 것을 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/store/ -run 'TestProjectView|TestSetProjectView' -v`
Expected: PASS 4건

- [ ] **Step 8: store 전건과 스키마 락을 돌린다**

Run: `cd plugins/flightdeck/server && go test ./internal/store/`
Expected: PASS. 특히 `TestBundledMigrationsAreAdditive`(증분이 가산인가) ·
`TestDeclaredTablesMatchDesign`(표 수 — 컬럼만 늘었으니 안 바뀐다) ·
`TestFreshInstallAndUpgradeProduceTheSameSchema`(신규와 업그레이드가 같은 모양인가)가 초록이어야 한다.

- [ ] **Step 9: 관문 다섯 줄을 돌린다**

Run: `cd plugins/flightdeck/server && gofmt -l . && go vet ./... && go test ./... && GOOS=darwin GOARCH=arm64 go vet ./... && GOOS=windows GOARCH=amd64 go vet ./...`
Expected: `gofmt -l .` 은 빈 출력, 나머지 전부 성공

- [ ] **Step 10: 커밋**

```bash
git add internal/store/migrations/007_project_view_axis.sql internal/store/store.go \
        internal/store/project.go internal/store/project_view_test.go internal/model/types.go
git commit -F - <<'EOF'
feat(flightdeck): 프로젝트에 핀·보관 축을 준다 — 증분 007

헤더의 프로젝트 줄이 ListProjects 전부를 내는데 실측 11건 중 일이 도는 것은 2건이다.
사람이 고른 것만 남기기 위한 자리를 원장에 만든다. 불리언이 아니라 시각인 이유는
created_at 과 같다 — 언제 접었는지가 없으면 되짚을 수 없다.

★ 함정 하나를 시험으로 못박았다. UpsertProject 는 세션이 열릴 때마다 도는데, 두 컬럼을
그 갱신 목록에 넣으면 훅이 세션을 열 때마다 사람이 고른 것이 날아가고 그 손실은 어느
화면에도 안 뜬다. created_at 이 같은 이유로 이미 그 목록 밖에 있다.

Scan 을 scanProject 하나로 모았다 — 두 벌이면 컬럼을 더할 때 한쪽만 고쳐지고,
전부 문자열이라 타입 오류도 안 난다.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 2: 프로젝트 줄을 접어 렌더한다 (읽기만)

**Files:**
- Modify: `internal/web/page.go` (`Page` 구조체 · `buildPage`)
- Modify: `internal/web/dashboard.gohtml:89-93`
- Create: `internal/web/project_nav_test.go`

**Interfaces:**
- Consumes: `model.Project.PinnedAt`·`ArchivedAt` (Task 1)
- Produces:
  - `type ProjectRow struct { ID string; On bool; Pinned bool; Archived bool; LastSession string }`
  - `type ProjectNav struct { Shown []ProjectRow; Folded []ProjectRow; Archived []ProjectRow; FoldedLine string; NoPins string }`
  - `Page.Nav ProjectNav`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/web/project_nav_test.go`:

```go
package web

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// pin 은 프로젝트 하나를 핀으로 만든다.
func (f *fixture) pin(id string) {
	f.t.Helper()
	if err := f.st.SetProjectView(context.Background(), id, time.Now().UTC(), time.Time{}); err != nil {
		f.t.Fatalf("핀 실패(%s): %v", id, err)
	}
}

// archive 는 프로젝트 하나를 보관한다.
func (f *fixture) archive(id string) {
	f.t.Helper()
	if err := f.st.SetProjectView(context.Background(), id, time.Time{}, time.Now().UTC()); err != nil {
		f.t.Fatalf("보관 실패(%s): %v", id, err)
	}
}

// TestProjectNavShowsAllWhenNoPins 는 **핀이 0이면 아무것도 안 접는다**는 단정이다.
//
// ★ 이것이 이 화면의 정직함이다. 핀이 없다는 사실을 자동 판정(활동이 있는 것만 편다)으로
// 덮으면, 사람이 접은 것과 규칙이 접은 것이 화면에서 같은 모양이 되고 "왜 사라졌나"에
// 답할 수 없게 된다.
func TestProjectNavShowsAllWhenNoPins(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	f.addProject("other-a")
	f.addProject("other-b")

	_, html := f.get("")

	nav := navOf(t, html)
	for _, want := range []string{testProject, "other-a", "other-b"} {
		if !strings.Contains(nav, want) {
			t.Fatalf("핀이 0인데 %q 가 줄에 없다 — 접으면 안 된다", want)
		}
	}
	mustContain(t, nav, "핀이 없다",
		"핀이 하나도 없다는 사실을 화면이 말해야 한다 — 안 그러면 이 축이 있는 줄 모른다")
	if strings.Contains(nav, "<details") {
		t.Fatal("핀이 0인데 접는 자리가 생겼다")
	}
}

// TestProjectNavFoldsUnpinnedAndSaysHowMany 는 접은 수를 반드시 말한다는 단정이다.
//
// ★ §웹UI 가 OutOfWindow·Folded 에 이미 건 규율과 같은 것이다 — 몇 건인지를 감추면
// "없다"와 "접혀 있다"가 구분되지 않는다.
func TestProjectNavFoldsUnpinnedAndSaysHowMany(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	f.addProject("keep")
	f.addProject("dead-a")
	f.addProject("dead-b")
	f.addProject("gone")
	f.pin(testProject)
	f.pin("keep")
	f.archive("gone")

	_, html := f.get("")
	nav := navOf(t, html)

	mustContain(t, nav, "<details", "핀이 있으면 나머지가 접혀야 한다")
	// 접힌 것은 셋이다: dead-a · dead-b · gone(보관).
	mustContain(t, nav, "나머지 3", "접은 수를 말해야 한다")
	mustContain(t, nav, "보관 1", "그중 보관이 몇 건인지도 말해야 한다")
	// 접힌 것들도 HTML 에는 있다(열면 보인다) — "없다"가 아니라 "접혀 있다"여야 한다.
	for _, want := range []string{"dead-a", "dead-b", "gone"} {
		if !strings.Contains(nav, want) {
			t.Fatalf("접힌 %q 가 HTML 에서 통째로 사라졌다 — 접는 것과 지우는 것은 다르다", want)
		}
	}
}

// TestProjectNavAlwaysShowsCurrentProject 는 보고 있는 프로젝트가 핀이 아니어도
// 줄에 있다는 단정이다. 없으면 화면이 자기가 어디 있는지를 안 말한다.
func TestProjectNavAlwaysShowsCurrentProject(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	f.addProject("pinned-one")
	f.pin("pinned-one")
	// testProject 는 핀이 아니다. 그런데 지금 보고 있는 것이다.

	_, html := f.get("?project=" + testProject)
	nav := navOf(t, html)

	before, _, found := strings.Cut(nav, "<details")
	if !found {
		t.Fatal("접는 자리가 없다 — 이 시험의 전제가 깨졌다")
	}
	if !strings.Contains(before, testProject) {
		t.Fatalf("보고 있는 프로젝트 %q 가 접힌 쪽에 있다 — 화면이 자기 위치를 안 말한다", testProject)
	}
}

// TestArchivedProjectStillOpens 는 **보관이 접근 차단이 아니라는** 단정이다.
//
// ★ 이 단정이 이 축을 "표시 계층"이라 부를 수 있는 근거고, render_test.go 의 폼 락이
// 이 폼을 상한에서 빼는 판정이 그 위에 선다. 여기가 빨개지면 그 판정도 함께 무너진다.
func TestArchivedProjectStillOpens(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	f.archive(testProject)

	code, html := f.get("?project=" + testProject)
	if code != 200 {
		t.Fatalf("보관된 프로젝트를 여니 %d 다 — 보관은 목록에서 빼는 것이지 접근 차단이 아니다", code)
	}
	mustContain(t, html, "① 지금", "보관된 프로젝트도 페이지 전체가 그대로 나와야 한다")
}
```

`navOf` 헬퍼와 `addProject` 헬퍼를 같은 파일에 더한다:

```go
// navOf 는 헤더의 프로젝트 줄만 잘라 낸다. 페이지 다른 곳의 프로젝트 id 언급(카드·표)이
// 이 시험들의 단정을 우연히 통과시키는 것을 막는다.
func navOf(t *testing.T, html string) string {
	t.Helper()
	_, rest, ok := strings.Cut(html, `<nav`)
	if !ok {
		t.Fatal("HTML 에 <nav> 가 없다 — 프로젝트 줄이 사라졌다")
	}
	nav, _, ok := strings.Cut(rest, `</nav>`)
	if !ok {
		t.Fatal("<nav> 가 안 닫혔다")
	}
	return nav
}

// addProject 는 프로젝트 하나를 원장에 등록한다. 세션도 항목도 없는 빈 프로젝트다 —
// 접기 규율은 실적을 안 보므로 그것으로 족하다.
func (f *fixture) addProject(id string) {
	f.t.Helper()
	if err := f.st.UpsertProject(context.Background(), model.Project{
		ID: id, Path: "/tmp/" + id, DefaultBranch: "main",
	}); err != nil {
		f.t.Fatalf("프로젝트 등록 실패(%s): %v", id, err)
	}
}
```

- [ ] **Step 2: 시험이 실패하는 것을 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/web/ -run 'TestProjectNav|TestArchivedProject' -v`
Expected: FAIL — `f.st.SetProjectView` 는 이미 있으나 `<details>` 도 `핀이 없다` 도 HTML 에 없다

- [ ] **Step 3: 뷰모델을 만든다**

`internal/web/page.go` 의 `SearchPanel` 아래에 더한다:

```go
// ProjectRow 는 프로젝트 줄의 한 칸이다.
type ProjectRow struct {
	ID       string
	On       bool // 지금 보고 있는 것
	Pinned   bool
	Archived bool
	// LastSession 은 마지막 세션의 나이다("3일 전"). 보관 목록에서만 쓴다 —
	// 보관해 둔 것이 다시 돌기 시작하면 그 사실이 보여야 사람이 풀 수 있다.
	LastSession string
}

// ProjectNav 는 헤더의 프로젝트 줄 전부다.
//
// ★ 접는 것과 지우는 것은 다르다. 접힌 것도 Folded·Archived 에 그대로 실려 HTML 에
// 나가고 <details> 가 닫아 둘 뿐이다 — 열면 보이고, ?project= 로는 언제나 열린다.
type ProjectNav struct {
	Shown    []ProjectRow // 핀 + 지금 보고 있는 것
	Folded   []ProjectRow // 핀도 보관도 아닌 것
	Archived []ProjectRow
	// FoldedLine 은 "나머지 3 (보관 1 포함)" 이다. 비면 접을 것이 없다는 뜻이다.
	FoldedLine string
	// NoPins 는 핀이 하나도 없을 때의 안내다. 그때는 아무것도 안 접는다.
	NoPins string
}
```

`Page` 에 필드를 더한다(`Projects []model.Project` 는 **그대로 둔다** — 다른 소비자가 있는지
확실치 않은 필드를 이 회차에 지우지 않는다):

```go
	Projects   []model.Project
	// Nav 는 위 Projects 를 화면 모양으로 접은 것이다. 템플릿은 이쪽만 읽는다.
	Nav        ProjectNav
```

- [ ] **Step 4: 조립 함수를 쓴다**

`internal/web/page.go` 에 순수 함수로 둔다 — 시각과 DB 를 안 만지므로 시험이 쉽다:

```go
// buildProjectNav 는 프로젝트 목록을 화면 모양으로 접는다. 순수 함수다.
//
// ★ 핀이 0이면 아무것도 안 접는다. 이것이 이 화면의 정직함이다 — 핀이 없다는 사실을
// 자동 판정(활동이 있는 것만 편다)으로 덮으면, 사람이 접은 것과 규칙이 접은 것이 화면에서
// 같은 모양이 되고 "왜 사라졌나"에 답할 수 없다.
//
// ★ 지금 보고 있는 것은 핀이 아니어도 편다. 안 그러면 화면이 자기 위치를 안 말한다.
func buildProjectNav(projects []model.Project, current string, ages map[string]string) ProjectNav {
	var nav ProjectNav
	pinned := 0
	for _, p := range projects {
		if !p.PinnedAt.IsZero() {
			pinned++
		}
	}

	for _, p := range projects {
		row := ProjectRow{
			ID:          p.ID,
			On:          p.ID == current,
			Pinned:      !p.PinnedAt.IsZero(),
			Archived:    !p.ArchivedAt.IsZero(),
			LastSession: ages[p.ID],
		}
		switch {
		case pinned == 0:
			nav.Shown = append(nav.Shown, row)
		case row.Pinned || row.On:
			nav.Shown = append(nav.Shown, row)
		case row.Archived:
			nav.Archived = append(nav.Archived, row)
		default:
			nav.Folded = append(nav.Folded, row)
		}
	}

	if pinned == 0 {
		nav.NoPins = "핀이 없다 — ★ 로 남길 것을 고르면 나머지가 접힌다"
		return nav
	}
	if n := len(nav.Folded) + len(nav.Archived); n > 0 {
		nav.FoldedLine = fmt.Sprintf("나머지 %d", n)
		if a := len(nav.Archived); a > 0 {
			nav.FoldedLine += fmt.Sprintf(" (보관 %d 포함)", a)
		}
	}
	return nav
}
```

`buildPage` 에서 `p.Projects = projects` 바로 뒤에 붙인다. 마지막 세션 나이는 보관된 것만
필요하므로 그 몇 건만 조회한다:

```go
	p.Projects = projects
	p.Nav = buildProjectNav(projects, req.project, h.archivedSessionAges(ctx, projects, now))
```

그리고 조회 헬퍼:

```go
// archivedSessionAges 는 **보관된 프로젝트만** 마지막 세션 나이를 읽는다.
//
// ★ 전건을 안 읽는다. 이 줄은 페이지 머리라 매 렌더 도는데, 프로젝트가 늘수록 질의가
// 함께 느는 자리를 머리에 두면 화면 전체가 그 비용을 문다. 보관 목록에만 필요한 값이다 —
// 보관해 둔 것이 다시 돌기 시작하면 사람이 그것을 보고 풀어야 하기 때문이다.
func (h *handler) archivedSessionAges(ctx context.Context, projects []model.Project,
	now time.Time) map[string]string {

	ages := map[string]string{}
	for _, p := range projects {
		if p.ArchivedAt.IsZero() {
			continue
		}
		at, err := h.svc.Store().LastSessionAt(ctx, p.ID)
		if err != nil {
			h.log.ErrorContext(ctx, "마지막 세션 시각 조회 실패",
				"project", Clip(p.ID, 64), "error", err.Error())
			ages[p.ID] = "모름"
			continue
		}
		if at.IsZero() {
			ages[p.ID] = "세션 없음"
			continue
		}
		ages[p.ID] = Age(now.Sub(at)) + " 전"
	}
	return ages
}
```

`internal/store/session.go` 에 조회를 더한다:

```go
// LastSessionAt 은 이 프로젝트에서 마지막으로 열린 세션의 시각이다. 없으면 제로값이다.
func (s *Store) LastSessionAt(ctx context.Context, project string) (time.Time, error) {
	var at sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT max(opened_at) FROM session WHERE project = ?`, project).Scan(&at)
	if err != nil {
		return time.Time{}, fmt.Errorf("마지막 세션 조회 실패(project=%q): %w", clip(project, 64), err)
	}
	if !at.Valid || strings.TrimSpace(at.String) == "" {
		return time.Time{}, nil
	}
	return parseTime(at.String)
}
```

나이 문구는 **`web.Age(d)`**(`internal/web/format.go:55`)를 쓴다. 새로 만들지 않는다 —
이 화면의 다른 나이가 전부 그 함수를 거치므로, 새 함수를 만들면 같은 페이지가 같은 종류의
값을 두 가지 말로 낸다.

- [ ] **Step 5: 템플릿을 고친다**

`internal/web/dashboard.gohtml:89-93` 을 통째로 갈아 끼운다(폼은 Task 3 에서 더한다):

```gotemplate
  {{if .Projects}}
  <nav>프로젝트:
    {{range .Nav.Shown}}<a class="{{if .On}}on{{end}}" href="?project={{.ID}}">{{.ID}}</a>{{end}}
    {{with .Nav.NoPins}}<span class="k">{{.}}</span>{{end}}
    {{/* 접기는 <details> 다 — JS 를 안 쓴다. 이 페이지의 다른 접기(.fold/.more)는 스크립트로
         접어서 JS 가 없으면 전부 펴지는데, 그 폴백은 긴 목록에는 옳지만 이 줄에는 반대다:
         스크립트가 없어도 접혀 있어야 이 축이 성립한다. */}}
    {{with .Nav.FoldedLine}}
    <details class="pfold"><summary>{{.}}</summary>
      {{range $.Nav.Folded}}<a href="?project={{.ID}}">{{.ID}}</a>{{end}}
      {{if $.Nav.Archived}}
      <div class="k">보관:
        {{range $.Nav.Archived}}<a class="dim" href="?project={{.ID}}">{{.ID}}</a>
          <span class="k">({{.LastSession}})</span>{{end}}
      </div>
      {{end}}
    </details>
    {{end}}
  </nav>
  {{end}}
```

CSS 를 `<style>` 끝(`.count` 아래)에 더한다:

```css
  /* 프로젝트 줄 접기 — JS 없이 <details> 하나로 닫는다. */
  details.pfold { display:inline-block; vertical-align:baseline; margin-left:6px; }
  details.pfold > summary { cursor:pointer; color:var(--dim); font-size:12px; }
  details.pfold[open] { display:block; margin:6px 0 0; }
  details.pfold a { margin-right:10px; }
```

- [ ] **Step 6: 시험이 통과하는 것을 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/web/ -run 'TestProjectNav|TestArchivedProject' -v`
Expected: PASS 4건

- [ ] **Step 7: web 전건을 돌린다**

Run: `cd plugins/flightdeck/server && go test ./internal/web/`
Expected: PASS. `TestWriteFormsAreAtMostFourAndAllRequireReason` 은 아직 폼을 안 더했으니 그대로 초록이다.

- [ ] **Step 8: 관문 다섯 줄 + 커밋**

Run: `cd plugins/flightdeck/server && gofmt -l . && go vet ./... && go test ./... && GOOS=darwin GOARCH=arm64 go vet ./... && GOOS=windows GOARCH=amd64 go vet ./...`

```bash
git add internal/web/page.go internal/web/dashboard.gohtml internal/web/project_nav_test.go internal/store/session.go
git commit -F - <<'EOF'
feat(flightdeck): 프로젝트 줄이 핀만 펴고 나머지를 접는다 — JS 는 0줄이다

핀이 0이면 아무것도 안 접는다. 핀이 없다는 사실을 자동 판정으로 덮으면 사람이 접은 것과
규칙이 접은 것이 화면에서 같은 모양이 되고 "왜 사라졌나"에 답할 수 없다.

접기는 <details> 다. 이 페이지의 다른 접기(.fold/.more)는 JS 로 접어서 스크립트가 없으면
전부 펴지는데, 그 폴백은 긴 목록에는 옳지만 이 줄에는 반대다 — 스크립트가 없어도 접혀
있어야 이 축이 성립한다.

접은 수를 반드시 말하고(§웹UI 의 기존 규율), 보고 있는 프로젝트는 핀이 아니어도 펴고,
보관된 것도 ?project= 로 그대로 열린다. 마지막 단정이 이 축을 "표시 계층"이라 부를 수 있는
근거다 — 다음 회차의 폼 락 판정이 그 위에 선다.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 3: 화면에서 핀·보관을 켜고 끈다

**Files:**
- Modify: `internal/web/actions.go` (판정 + 핸들러)
- Modify: `internal/web/web.go:100-128` (라우트 · 주석)
- Modify: `internal/web/dashboard.gohtml` (폼 · CSS)
- Modify: `internal/web/render_test.go:433-495` (폼 락)
- Create: `internal/web/project_view_test.go`

**Interfaces:**
- Consumes: `store.SetProjectView` (Task 1) · `ProjectNav`/`ProjectRow` (Task 2)
- Produces:
  - `type ProjectViewInput struct { Project string; Target string; Axis string }` — `Axis` 는 `pin`·`unpin`·`archive`·`unarchive`
  - `func JudgeProjectView(in ProjectViewInput, known []string) ActionVerdict`
  - `func ParseProjectAxis(raw string) (axis, target string)` — `"pin:foo"` → `("pin","foo")`

- [ ] **Step 1: 판정의 실패하는 시험을 쓴다**

`internal/web/project_view_test.go`:

```go
package web

import (
	"net/url"
	"strings"
	"testing"
)

// TestParseProjectAxis 는 버튼 값의 해석이다. 순수 함수라 표로 본다.
func TestParseProjectAxis(t *testing.T) {
	for _, c := range []struct {
		raw, axis, target string
	}{
		{"pin:kweiza-cc-plugins", "pin", "kweiza-cc-plugins"},
		{"unpin:a", "unpin", "a"},
		{"archive:machine-probe", "archive", "machine-probe"},
		{"unarchive:x", "unarchive", "x"},
		// 프로젝트 id 에 콜론이 들어와도 축은 첫 콜론에서만 갈린다.
		{"pin:a:b", "pin", "a:b"},
		{"", "", ""},
		{"pin", "", ""},
		{":x", "", ""},
		{"pin:", "", ""},
	} {
		axis, target := ParseProjectAxis(c.raw)
		if axis != c.axis || target != c.target {
			t.Fatalf("ParseProjectAxis(%q) = (%q,%q), 기대 (%q,%q)", c.raw, axis, target, c.axis, c.target)
		}
	}
}

// TestJudgeProjectViewFillsReason 은 **거절 사유가 항상 다르다**는 단정이다.
//
// ★ 불리언으로 접으면 "그런 프로젝트가 없다"와 "축이 이상하다"와 "빈 값이다"가 같은 실패가
// 되고, 화면은 세 경우에 똑같은 말을 한다. ActionVerdict 가 사유를 담는 이유와 같다.
func TestJudgeProjectViewFillsReason(t *testing.T) {
	known := []string{"a", "b"}
	seen := map[string]string{}
	for name, in := range map[string]ProjectViewInput{
		"빈 축":       {Project: "a", Target: "a", Axis: ""},
		"모르는 축":     {Project: "a", Target: "a", Axis: "delete"},
		"빈 대상":      {Project: "a", Target: "", Axis: "pin"},
		"없는 대상":     {Project: "a", Target: "없다", Axis: "pin"},
		"빈 현재 프로젝트": {Project: "", Target: "a", Axis: "pin"},
	} {
		v := JudgeProjectView(in, known)
		if v.OK {
			t.Fatalf("%s: 통과했다 — 거절해야 한다", name)
		}
		if strings.TrimSpace(v.Reason) == "" {
			t.Fatalf("%s: 사유가 비었다 — 사유 없는 거절은 화면에서 원인이 안 보인다", name)
		}
		if prev, dup := seen[v.Reason]; dup {
			t.Fatalf("%s 와 %s 가 같은 사유를 낸다(%q) — 세 경우가 한 실패로 접혔다", name, prev, v.Reason)
		}
		seen[v.Reason] = name
	}

	for _, in := range []ProjectViewInput{
		{Project: "a", Target: "a", Axis: "pin"},
		{Project: "a", Target: "b", Axis: "unpin"},
		{Project: "a", Target: "b", Axis: "archive"},
		{Project: "a", Target: "b", Axis: "unarchive"},
	} {
		if v := JudgeProjectView(in, known); !v.OK {
			t.Fatalf("%+v 가 거절됐다: %s", in, v.Reason)
		}
	}
}

// TestProjectViewWritePinsAndRedirects 는 실제 쓰기 한 바퀴다.
func TestProjectViewWritePinsAndRedirects(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	f.addProject("dead")

	rec := f.post("/actions/project-view", url.Values{
		"project": {testProject},
		"axis":    {"pin:" + testProject},
	})
	if rec.Code != 303 {
		t.Fatalf("응답 %d, 기대 303 — 쓰기는 리다이렉트로 돌아온다(더블 제출 방지)", rec.Code)
	}

	_, html := f.get("")
	nav := navOf(t, html)
	before, _, found := strings.Cut(nav, "<details")
	if !found {
		t.Fatal("핀을 찍었는데 접는 자리가 안 생겼다")
	}
	if !strings.Contains(before, testProject) {
		t.Fatalf("핀을 찍은 %q 가 펴진 쪽에 없다", testProject)
	}
	if strings.Contains(before, "dead") {
		t.Fatal("핀이 아닌 dead 가 펴진 쪽에 있다")
	}
}

// TestProjectViewWriteRefusesUnknownTarget 은 없는 프로젝트를 400 으로 거절한다는 단정이다.
func TestProjectViewWriteRefusesUnknownTarget(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	rec := f.post("/actions/project-view", url.Values{
		"project": {testProject},
		"axis":    {"pin:없는프로젝트"},
	})
	if rec.Code != 400 {
		t.Fatalf("응답 %d, 기대 400", rec.Code)
	}
}

// TestProjectViewArchiveThenUnarchive 는 되돌리는 길이 실제로 도는지 본다.
// 보관에 사유를 안 받는 근거가 "클릭 하나로 돌아온다"이므로 그 문장을 시험이 든다.
func TestProjectViewArchiveThenUnarchive(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	f.addProject("junk")
	f.pin(testProject)

	if rec := f.post("/actions/project-view", url.Values{
		"project": {testProject}, "axis": {"archive:junk"},
	}); rec.Code != 303 {
		t.Fatalf("보관 응답 %d, 기대 303", rec.Code)
	}
	_, html := f.get("")
	mustContain(t, navOf(t, html), "보관 1", "보관 뒤에는 그 수가 줄에 나야 한다")

	if rec := f.post("/actions/project-view", url.Values{
		"project": {testProject}, "axis": {"unarchive:junk"},
	}); rec.Code != 303 {
		t.Fatalf("되돌리기 응답 %d, 기대 303", rec.Code)
	}
	_, html = f.get("")
	nav := navOf(t, html)
	if strings.Contains(nav, "보관 1") {
		t.Fatal("되돌렸는데 보관 수가 그대로다")
	}
	mustContain(t, nav, "나머지 1", "되돌린 것은 보통으로 돌아가 접힌 쪽에 남는다")
}
```

- [ ] **Step 2: 시험이 실패하는 것을 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/web/ -run 'TestParseProjectAxis|TestJudgeProjectView|TestProjectView' -v`
Expected: 컴파일 실패 — `ParseProjectAxis` · `JudgeProjectView` · `ProjectViewInput` 없음

- [ ] **Step 3: 판정을 쓴다**

`internal/web/actions.go` 끝에 더한다. **`ActionKind` 와 안 섞는다**:

```go
// ─────────────────────────────────────────────────────────────────────────────
// 표시 축 — 핀·보관
//
// ★ 위의 ActionKind 셋과 **한 자리에 안 섞는다.** 그 셋은 사유를 요구하는 판정이고
// (JudgeAction 이 reasonMin 을 든다), 이 축은 사유가 없다. 섞으면 "사유가 없으면 거절"이
// 이 축 때문에 헐거워지고, 그 헐거움은 회수·폐기·줄 회수 쪽에서 터진다.
//
// ★ 이 축이 사유를 안 받는 근거: 사유가 필수인 셋은 전부 **남의 일을 뺏거나 되돌릴 수
// 없는** 것이다. 핀과 보관은 둘 다 아니다 — 내 판이고 클릭 하나로 돌아온다. 요구하면
// 원장에 의미 없는 문자열만 쌓이고 화면에는 프로젝트마다 입력칸이 붙는다.
// ─────────────────────────────────────────────────────────────────────────────

// ProjectViewInput 은 표시 축 폼에서 온 것 전부다.
type ProjectViewInput struct {
	Project string // 눌렀을 때 보고 있던 프로젝트. 돌아갈 자리다
	Target  string // 축을 바꿀 프로젝트
	Axis    string // pin · unpin · archive · unarchive
}

// projectAxes 는 허용하는 축 전부다. 목록 밖은 거절이다 —
// 화이트리스트가 아니면 오타가 조용히 아무 일도 안 하는 성공이 된다.
var projectAxes = map[string]bool{"pin": true, "unpin": true, "archive": true, "unarchive": true}

// ParseProjectAxis 는 버튼 값 "pin:<id>" 를 축과 대상으로 가른다. 순수 함수다.
//
// ★ 토글("이 프로젝트를 뒤집어라")이 아니라 **명시적 값**인 이유는 멱등이다. 화면 쓰기는
// 렌더 시각 키로 멱등을 걸고 있는데(Page.WriteKey), 토글이면 같은 키의 재전송이 상태를
// 뒤집어 그 멱등이 거짓말이 된다. pin 을 두 번 보내면 두 번 다 핀이다.
//
// ★ 첫 콜론에서만 가른다 — 프로젝트 id 는 [A-Za-z0-9._/-] 이라 콜론이 없어야 정상이지만,
// 여기서 뒤를 통째로 대상으로 두면 이상한 id 가 "없는 프로젝트"로 정확히 거절된다.
// 뒤에서 또 자르면 엉뚱한 프로젝트에 맞을 수 있다.
func ParseProjectAxis(raw string) (axis, target string) {
	a, t, ok := strings.Cut(strings.TrimSpace(raw), ":")
	if !ok || a == "" || t == "" {
		return "", ""
	}
	return a, t
}

// JudgeProjectView 는 표시 축 요청이 성립하는지 판정한다. 순수 함수다.
// known 은 등록된 프로젝트 id 전부다.
func JudgeProjectView(in ProjectViewInput, known []string) ActionVerdict {
	if strings.TrimSpace(in.Project) == "" {
		return ActionVerdict{Reason: "돌아갈 프로젝트가 비었다 — 폼의 project 필드가 안 왔다"}
	}
	if strings.TrimSpace(in.Axis) == "" {
		return ActionVerdict{Reason: "축이 비었다 — 버튼 값이 pin:<id> 꼴이어야 한다"}
	}
	if !projectAxes[in.Axis] {
		return ActionVerdict{Reason: fmt.Sprintf("모르는 축 %q — pin·unpin·archive·unarchive 넷뿐이다",
			Clip(in.Axis, 32))}
	}
	if strings.TrimSpace(in.Target) == "" {
		return ActionVerdict{Reason: "대상 프로젝트가 비었다"}
	}
	for _, k := range known {
		if k == in.Target {
			return ActionVerdict{OK: true, Reason: "등록된 프로젝트다"}
		}
	}
	return ActionVerdict{Reason: fmt.Sprintf("프로젝트 %q 가 등록돼 있지 않다", Clip(in.Target, 64))}
}
```

- [ ] **Step 4: 핸들러를 쓴다**

같은 파일에 이어서:

```go
// projectView 는 표시 축을 바꾼다. 사유가 없으므로 formInput 을 안 탄다.
func (h *handler) projectView(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "폼을 읽지 못했다: "+Clip(err.Error(), 200), http.StatusBadRequest)
		return
	}
	axis, target := ParseProjectAxis(r.PostFormValue("axis"))
	in := ProjectViewInput{
		Project: strings.TrimSpace(r.PostFormValue("project")),
		Target:  target,
		Axis:    axis,
	}

	ctx := r.Context()
	st := h.svc.Store()
	projects, err := st.ListProjects(ctx)
	if err != nil {
		h.log.ErrorContext(ctx, "프로젝트 목록 조회 실패",
			"route", "POST /actions/project-view", "error", err.Error())
		http.Error(w, "프로젝트 목록을 읽지 못했다. 잠시 뒤 다시 하라.", http.StatusInternalServerError)
		return
	}
	known := make([]string, 0, len(projects))
	for _, p := range projects {
		known = append(known, p.ID)
	}
	if v := JudgeProjectView(in, known); !v.OK {
		http.Error(w, fmt.Sprintf("표시 축 거절: %s\n대상: %s\n뒤로 가서 다시 하라.",
			v.Reason, Clip(in.Target, 64)), http.StatusBadRequest)
		return
	}

	// 지금 값을 읽어 바꿀 축 하나만 갈아 끼운다 — 다른 축을 덮지 않는다.
	cur, err := st.GetProject(ctx, in.Target)
	if err != nil {
		h.log.ErrorContext(ctx, "프로젝트 조회 실패",
			"route", "POST /actions/project-view", "project", Clip(in.Target, 64), "error", err.Error())
		http.Error(w, "프로젝트를 읽지 못했다.", http.StatusInternalServerError)
		return
	}
	pinned, archived := cur.PinnedAt, cur.ArchivedAt
	now := h.now()
	switch in.Axis {
	case "pin":
		pinned = now
		// ★ 핀과 보관은 함께 설 수 없다. 핀은 "줄에 남긴다"이고 보관은 "줄에서 뺀다"라
		//   둘 다 켜지면 화면이 둘 중 하나를 말없이 이긴다. 핀을 찍으면 보관이 풀린다.
		archived = time.Time{}
	case "unpin":
		pinned = time.Time{}
	case "archive":
		archived = now
		pinned = time.Time{}
	case "unarchive":
		archived = time.Time{}
	}

	if err := st.SetProjectView(ctx, in.Target, pinned, archived); err != nil {
		h.log.ErrorContext(ctx, "표시 축 갱신 실패",
			"route", "POST /actions/project-view", "project", Clip(in.Target, 64), "error", err.Error())
		http.Error(w, "표시 축을 바꾸지 못했다.", http.StatusInternalServerError)
		return
	}
	st.LogEvent(ctx, "web.project.view", in.Target, "", map[string]any{"axis": in.Axis})
	h.log.InfoContext(ctx, "프로젝트 표시 축", "route", "POST /actions/project-view",
		"project", Clip(in.Target, 64), "axis", in.Axis)

	q := url.Values{}
	q.Set("project", in.Project)
	q.Set("notice", "project-view")
	http.Redirect(w, r, "../?"+q.Encode(), http.StatusSeeOther)
}
```

**주의:** `st.LogEvent` 의 실제 시그니처를 `internal/store/event.go` 에서 확인해 맞춘다
(`actions.go` 의 `drop` 이 `h.svc.Store().LogEvent(ctx, kind, project, sessionID, payload)` 로
부르고 있으므로 그 모양이다).

- [ ] **Step 5: 라우트를 등록한다**

`internal/web/web.go` 의 `New` 에 한 줄, 그리고 위 주석을 고친다:

```go
// 라우트는 다섯이다: GET / (한 장) · POST actions/reclaim · POST actions/drop ·
// POST actions/lane-release · POST actions/project-view.
//
// ★ 앞의 셋만 "쓰기"다. project-view 는 표시 축(핀·보관)이라 파생물에도 원장의 사실에도
// 안 쓴다 — 그것이 render_test.go 의 폼 상한에서 그 폼을 빼는 근거고, 그 근거의 증거는
// **접힌 프로젝트도 ?project= 로 그대로 열린다**는 것이다(project_nav_test.go 가 든다).
```

```go
	h.mux.HandleFunc("POST /actions/project-view", h.projectView)
```

- [ ] **Step 6: 템플릿에 폼을 더한다**

`<nav>` 를 폼으로 감싼다. `dashboard.gohtml` 의 `<nav>` 블록을 이렇게 바꾼다:

```gotemplate
  {{if .Projects}}
  {{/* ★ 폼 하나에 버튼 여럿이다. 프로젝트마다 폼을 만들면 폼이 프로젝트 수만큼 늘고,
       render_test.go 의 상한이 세는 축이 통째로 무의미해진다. */}}
  <form class="pview" method="post" action="actions/project-view?key={{.WriteKey "project-view"}}">
  <input type="hidden" name="project" value="{{.Project.ID}}">
  <nav>프로젝트:
    {{range .Nav.Shown}}<a class="{{if .On}}on{{end}}" href="?project={{.ID}}">{{.ID}}</a
      >{{if .Pinned}}<button class="ax" name="axis" value="unpin:{{.ID}}" title="핀 해제">★</button
      >{{else}}<button class="ax" name="axis" value="pin:{{.ID}}" title="핀">☆</button>{{end}}{{end}}
    {{with .Nav.NoPins}}<span class="k">{{.}}</span>{{end}}
    {{with .Nav.FoldedLine}}
    <details class="pfold"><summary>{{.}}</summary>
      {{range $.Nav.Folded}}<a href="?project={{.ID}}">{{.ID}}</a
        ><button class="ax" name="axis" value="pin:{{.ID}}" title="핀">☆</button
        ><button class="ax" name="axis" value="archive:{{.ID}}" title="보관 — 줄에서 뺀다">보관</button>{{end}}
      {{if $.Nav.Archived}}
      <div class="k">보관:
        {{range $.Nav.Archived}}<a class="dim" href="?project={{.ID}}">{{.ID}}</a>
          <span class="k">({{.LastSession}})</span
          ><button class="ax" name="axis" value="unarchive:{{.ID}}" title="되돌린다">되돌리기</button>{{end}}
      </div>
      {{end}}
    </details>
    {{end}}
  </nav>
  </form>
  {{end}}
```

CSS 에 버튼 모양을 더한다:

```css
  /* 표시 축 버튼 — 사유가 없는 클릭 하나짜리라 쓰기 폼(.write)과 다른 모양이다. */
  button.ax { border:none; background:transparent; color:var(--dim); cursor:pointer;
              font-size:12px; padding:0 4px; }
  button.ax:hover { color:var(--fg); }
```

- [ ] **Step 7: 폼 락을 갱신한다**

`internal/web/render_test.go:433` 의 `TestWriteFormsAreAtMostFourAndAllRequireReason` 을 고친다.
`logoutForms` 확인 바로 아래에 같은 모양으로 더한다:

```go
	// ★ 표시 축 폼(핀·보관)도 이 셈에서 뺀다. 로그아웃과 **같은 부류의 다른 근거**다.
	//
	// 이 관문이 세는 것은 파생물에 쓰는 폼이다(actions.go 머리의 그 규율). 로그아웃은
	// 쿠키 하나를 지울 뿐 원장에 아무것도 안 남겨서 빠졌다. 이 폼은 원장에 쓴다 —
	// project.pinned_at·archived_at 이다. 그래서 그 근거를 그대로 못 쓴다.
	//
	// 빼는 근거는 **그 두 컬럼이 무엇에 닿는가**다. 둘은 누가 무엇을 보느냐만 정하고
	// 항목·판단·선점·랜딩 어디에도 안 닿는다. 그 증거는 화면 밖에 있다:
	// project_nav_test.go 의 TestArchivedProjectStillOpens 가 **접힌 프로젝트도
	// ?project= 로 그대로 열린다**를 든다. 접근이 안 막히면 그것은 표시 계층이다.
	// 그 시험이 빨개지면 이 면제의 근거도 함께 무너진다 — 두 자리가 한 판정을 나눠 든다.
	//
	// 로그아웃과 똑같이 **먼저 정확히 하나인지 확인하고** 뺀다. 없거나 여럿이면
	// 아래 뺄셈이 조용히 틀린 수를 통과시킨다.
	viewForms := strings.Count(html, projectViewFormOpen)
	if viewForms != 1 {
		t.Fatalf("표시 축 폼(%s)이 %d개다 — 정확히 하나여야 아래 셈이 뺄 수 있다",
			projectViewFormOpen, viewForms)
	}
	exempt := logoutForms + viewForms
```

그리고 아래 세 단정에서 `logoutForms` 를 `exempt` 로 바꾼다:

```go
	if n := strings.Count(html, "<form") - exempt; n > 4 {
		t.Fatalf("폼 %d개 — 넷을 넘었다. 파생물에 손대는 폼이 늘면 대시보드가 다시 손 기재 저장소가 된다", n)
	}
	if n := strings.Count(html, `method="post"`) - exempt; n != 3 {
		t.Fatalf("POST 폼 %d개, 기대 3개(선점 회수·항목 폐기·랜딩 줄 행 회수). "+
			"남은 하나(잡 우회 기록)는 Tier B 라 비활성 버튼이다. "+
			"로그아웃과 표시 축 폼은 파생물 쓰기가 아니라 이 셈에서 뺐다", n)
	}
```

`name="reason" required` 단정은 **그대로 3** 이다 — 표시 축 폼에는 사유 입력이 없다.

★ 같은 자리의 주석 하나를 정정한다:

```go
	// 폼은 넷이다: Tier A 쓰기 셋 + **판단 검색 GET 하나**.
	//
	// ★ 이 자리의 옛 주석은 넷째를 "프로젝트 고르기 GET" 이라 적었는데 틀렸다 —
	//   프로젝트 고르기는 <a> 링크였고 폼이 아니었다(수는 맞고 이름이 틀렸다).
	//   지금은 그 자리에 표시 축 폼이 생겼고, 그것은 위에서 이름으로 뺐다.
```

`projectViewFormOpen` 상수를 `project_view_test.go` 에 둔다(`logoutFormOpen` 이 `login_test.go` 에
있는 것과 같은 배치 — 그 축을 단정하는 파일이 문자열을 소유한다):

```go
// projectViewFormOpen 은 표시 축 폼의 여는 태그 통째다.
// ★ 클래스만 세면 이 폼이 GET 으로 바뀌어도 하나로 세어져, render_test.go 의 POST 뺄셈이
// 하나 어긋나 **이 폼과 무관한 자리에서** 빨개진다(로그아웃이 같은 함정을 겪었다).
const projectViewFormOpen = `<form class="pview" method="post" action="actions/project-view`
```

- [ ] **Step 8: 시험이 통과하는 것을 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/web/ -v -run 'TestParseProjectAxis|TestJudgeProjectView|TestProjectView|TestWriteForms'`
Expected: PASS 전건

- [ ] **Step 9: web 전건 + 관문 다섯 줄**

Run: `cd plugins/flightdeck/server && go test ./internal/web/ && gofmt -l . && go vet ./... && go test ./... && GOOS=darwin GOARCH=arm64 go vet ./... && GOOS=windows GOARCH=amd64 go vet ./...`

- [ ] **Step 10: 커밋**

```bash
git add internal/web/actions.go internal/web/web.go internal/web/dashboard.gohtml \
        internal/web/project_view_test.go internal/web/render_test.go
git commit -F - <<'EOF'
feat(flightdeck): 프로젝트 줄에서 핀·보관을 켜고 끈다 — 폼 하나, 사유는 안 받는다

폼 하나에 버튼 여럿이다. 프로젝트마다 폼을 만들면 폼이 프로젝트 수만큼 늘고 폼 상한이
세는 축이 통째로 무의미해진다.

토글이 아니라 명시적 값(pin:/unpin:/archive:/unarchive:)인 이유는 멱등이다. 화면 쓰기는
렌더 시각 키로 멱등을 거는데, 토글이면 같은 키의 재전송이 상태를 뒤집어 그 멱등이
거짓말이 된다.

사유를 안 받는다. 사유가 필수인 셋은 전부 남의 일을 뺏거나 되돌릴 수 없는 것인데
핀·보관은 둘 다 아니다. 판정도 ActionKind 와 안 섞는다 — 섞으면 "사유가 없으면 거절"이
이 축 때문에 헐거워지고 그 헐거움은 회수·폐기 쪽에서 터진다.

폼 락은 상한 넷을 그대로 두고 이 폼을 이름으로 뺐다. 근거는 로그아웃과 다르다(이쪽은
원장에 쓴다) — 두 컬럼이 항목·판단·선점·랜딩 어디에도 안 닿고 접힌 프로젝트도 URL 로
열린다는 것이 근거고, 그 증거를 project_nav_test.go 가 든다. 두 자리가 한 판정을 나눠 든다.

같은 자리의 주석도 정정했다: 넷째 폼은 「프로젝트 고르기 GET」이 아니라 판단 검색 폼이다.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 4: `fd project ls` — 터미널에서 이 축을 읽는다

**Files:**
- Create: `internal/service/project.go`
- Create: `internal/api/handlers_projects.go`
- Create: `cmd/fd/project.go`
- Create: `cmd/fd/project_test.go`
- Modify: `internal/api/api.go` (라우트 한 줄)
- Modify: `cmd/fd/main.go:138-152` (분기 한 줄)
- Modify: `cmd/fd/wire.go` (응답 타입)

**Interfaces:**
- Consumes: `store.ListProjects` (Task 1)
- Produces:
  - `type ProjectSummary struct { ID, Path string; Pinned, Archived bool; Items, Sessions, Judgments int; LastSessionAt time.Time }`
  - `func (s *Service) ListProjectSummaries(ctx context.Context) ([]ProjectSummary, error)`
  - `func (s *Store) ProjectRefCounts(ctx context.Context, id string) (map[string]int, error)` — 표 이름 → 행 수
  - CLI: `fd project ls`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/service/project_test.go` — 픽스처는 이 패키지의 `newSvc(t) (*Service, *store.Store)` 다:

```go
package service

import (
	"context"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// TestListProjectSummariesCounts 는 요약의 수치가 실제 행 수와 맞는지 본다.
// 지우기 전에 무엇이 있는지 보는 유일한 길이라, 이 수가 틀리면 사람이 틀린 판단을 한다.
func TestListProjectSummariesCounts(t *testing.T) {
	ctx := context.Background()
	svc, st := newSvc(t)

	for _, id := range []string{"live", "empty"} {
		if err := st.UpsertProject(ctx, model.Project{
			ID: id, Path: "/tmp/" + id, DefaultBranch: "main",
		}); err != nil {
			t.Fatalf("프로젝트 등록 실패(%s): %v", id, err)
		}
	}
	// live 에만 항목 둘을 붙인다.
	for _, itemID := range []string{"a", "b"} {
		if err := st.Tx(ctx, func(tx *store.Tx) error {
			return tx.AddItem(model.Item{
				Project: "live", ID: itemID, Title: itemID, Body: "본문",
				State: "open", CreatedAt: time.Now().UTC(),
			})
		}); err != nil {
			t.Fatalf("항목 등록 실패(%s): %v", itemID, err)
		}
	}
	if err := st.SetProjectView(ctx, "empty", time.Time{}, time.Now().UTC()); err != nil {
		t.Fatalf("보관 실패: %v", err)
	}

	list, err := svc.ListProjectSummaries(ctx)
	if err != nil {
		t.Fatalf("요약 실패: %v", err)
	}
	by := map[string]ProjectSummary{}
	for _, p := range list {
		by[p.ID] = p
	}
	if by["live"].Items != 2 {
		t.Fatalf("live 의 항목이 %d건, 기대 2건 — 이 수를 보고 사람이 지울지 정한다", by["live"].Items)
	}
	if by["empty"].Items != 0 {
		t.Fatalf("empty 의 항목이 %d건, 기대 0건", by["empty"].Items)
	}
	if !by["empty"].Archived {
		t.Fatal("empty 가 보관으로 안 나온다")
	}
	if by["live"].Archived || by["live"].Pinned {
		t.Fatal("live 는 핀도 보관도 아니어야 한다")
	}
}
```

**주의:** `store.Tx` 의 항목 추가 메서드 실제 이름을 확인해 맞춘다
(`grep -n "func (t \*Tx) AddItem\|func (t \*Tx) InsertItem" internal/store/item.go`).
이 시험이 세는 것은 항목 **수**뿐이라, 추가 경로는 `svc` 의 공개 API(`svc.AddItem` 류)를
써도 무방하다 — 그쪽이 더 짧으면 그것을 쓴다.

`cmd/fd/project_test.go` — 하네스는 `newHarness(t)` 이고 **실물 서버**를 띄운다.
`h.run(stdin, args...) (int, string)` 이 종료코드와 출력을 낸다:

```go
package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// TestProjectLsPrintsAxisAndCounts 는 ls 의 출력 계약이다.
// 사람이 이 표를 보고 무엇을 보관하고 무엇을 지울지 정한다.
func TestProjectLsPrintsAxisAndCounts(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.st.UpsertProject(ctx, model.Project{
		ID: "junk", Path: "/tmp/junk", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("프로젝트 등록 실패: %v", err)
	}
	if err := h.st.SetProjectView(ctx, "junk", time.Time{}, time.Now().UTC()); err != nil {
		t.Fatalf("보관 실패: %v", err)
	}

	code, out := h.run("", "project", "ls")
	if code != 0 {
		t.Fatalf("종료코드 %d, 기대 0\n%s", code, out)
	}
	for _, want := range []string{h.project, "junk", "보관"} {
		if !strings.Contains(out, want) {
			t.Fatalf("출력에 %q 가 없다 — 사람이 이 표로 판단한다\n%s", want, out)
		}
	}
	// ★ 지울 수 있는지를 출력이 말해야 한다. 안 그러면 사람이 rm 을 쳐 보고서야 안다.
	if !strings.Contains(out, "판단") {
		t.Fatalf("출력이 삭제의 한계를 안 말한다\n%s", out)
	}
}
```

- [ ] **Step 2: 시험이 실패(또는 skip)하는 것을 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/service/ ./cmd/fd/ -run 'TestListProjectSummaries|TestProjectLs' -v`
Expected: 컴파일 실패 — `svc.ListProjectSummaries` 없음 · `fd project ls` 가 「모르는 명령」

- [ ] **Step 3: store 에 셈을 더한다**

`internal/store/project.go`:

```go
// projectRefTables 는 project(id) 를 참조하거나 project 컬럼으로 프로젝트에 묶이는 표
// 전부다. **삭제 순서이기도 하다** — 자식부터 부모 순이다.
//
// ★ 뒤의 둘(item_dependents · pick_eval)은 FK 가 아니라 컬럼으로만 묶인다. FK 가 안 우니
// 안 지워도 삭제는 성공하고, 그래서 더 위험하다 — 조용히 고아 행이 남는다.
//
// ★ judgment 는 여기 있지만 **지우지 않는다**. judgment_no_delete 트리거가 원리적으로
// 막는다(schema.sql). 그래서 RemoveProject 는 판단이 하나라도 있으면 거절한다.
//
// ★ event 는 여기 없다. event.project 는 FK 가 아니라 그냥 컬럼이고(schema.sql 의 그 자리),
// 프로젝트가 사라져도 남는다 — 그것이 옳다. "이런 프로젝트가 있었고 언제 지워졌다"가
// 원장에 남는 유일한 길이다.
var projectRefTables = []string{
	"session_workspace",
	"session",
	"ref_state",
	"change_set",
	"item",
	"judgment",
	"snapshot",
	"counter",
	"resource_hold",
	"job",
	"item_dependents",
	"pick_eval",
}

// ProjectRefCounts 는 이 프로젝트에 묶인 행 수를 표별로 센다.
// 지우기 전에 무엇이 함께 갈지 보여주는 자리다.
func (s *Store) ProjectRefCounts(ctx context.Context, id string) (map[string]int, error) {
	out := make(map[string]int, len(projectRefTables)+1)
	for _, tbl := range projectRefTables {
		var n int
		// ★ 표 이름은 위 상수 목록에서만 온다 — 외부 입력이 아니라 문자열 결합이 안전하다.
		if err := s.db.QueryRowContext(ctx,
			`SELECT count(*) FROM `+tbl+` WHERE project = ?`, id).Scan(&n); err != nil {
			return nil, fmt.Errorf("행 수 조회 실패(table=%s, project=%q): %w", tbl, clip(id, 64), err)
		}
		out[tbl] = n
	}
	var ev int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM event WHERE project = ?`, id).Scan(&ev); err != nil {
		return nil, fmt.Errorf("이벤트 수 조회 실패(project=%q): %w", clip(id, 64), err)
	}
	out["event"] = ev // 세기만 한다 — 안 지운다
	return out, nil
}
```

- [ ] **Step 4: service 에 요약을 더한다**

`internal/service/project.go`:

```go
package service

import (
	"context"
	"time"
)

// ProjectSummary 는 프로젝트 하나의 상태와 실적이다.
// 사람이 이 표를 보고 무엇을 보관하고 무엇을 지울지 정한다.
type ProjectSummary struct {
	ID            string    `json:"id"`
	Path          string    `json:"path"`
	Pinned        bool      `json:"pinned"`
	Archived      bool      `json:"archived"`
	Items         int       `json:"items"`
	Sessions      int       `json:"sessions"`
	Judgments     int       `json:"judgments"`
	Events        int       `json:"events"`
	LastSessionAt time.Time `json:"last_session_at"`
}

// ListProjectSummaries 는 전 프로젝트의 요약이다.
//
// ★ 프로젝트 수는 사람이 등록한 만큼이라(store.ListProjects 의 그 주석) 프로젝트당
// 질의 몇 개는 감당한다. 이 경로는 화면 머리가 아니라 CLI 와 REST 전용이다 —
// 매 렌더 도는 자리에 두면 프로젝트가 늘수록 화면 전체가 느려진다.
func (s *Service) ListProjectSummaries(ctx context.Context) ([]ProjectSummary, error) {
	projects, err := s.st.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ProjectSummary, 0, len(projects))
	for _, p := range projects {
		counts, err := s.st.ProjectRefCounts(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		last, err := s.st.LastSessionAt(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, ProjectSummary{
			ID: p.ID, Path: p.Path,
			Pinned: !p.PinnedAt.IsZero(), Archived: !p.ArchivedAt.IsZero(),
			Items: counts["item"], Sessions: counts["session"],
			Judgments: counts["judgment"], Events: counts["event"],
			LastSessionAt: last,
		})
	}
	return out, nil
}
```

- [ ] **Step 5: REST 라우트를 더한다**

`internal/api/handlers_projects.go`:

```go
package api

import "net/http"

// handleListProjects 는 프로젝트 요약 전부를 낸다.
//
// ★ 읽기 전용이라 화면 쓰기 사슬을 안 탄다. 이 경로가 내는 것은 등록된 프로젝트와 그 실적뿐이고,
// 그것을 읽을 자격은 이미 게이트가 판정했다.
func (s *server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	list, err := s.svc.ListProjectSummaries(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{"projects": list})
}
```

헬퍼 이름은 확인된 것이다 — 서버 타입은 **소문자 `server`** 이고, `s.fail(w, r, err)` 는 종류
문자열을 안 받으며 `s.writeJSON(w, r, status, v)` 는 `r` 을 함께 받는다(`handlers_meta.go:352`
의 `handleNotices` 가 그 모양이다). 400 을 직접 내야 하면 `s.writeError(w, r, badRequest(code,
msg, guidance))` 다.

`internal/api/api.go` 의 라우트 등록에 한 줄:

```go
	mux.HandleFunc("GET /api/v1/projects", s.handleListProjects)
```

- [ ] **Step 6: CLI 를 더한다**

`cmd/fd/project.go`:

```go
package main

// runProject 는 `fd project <하위명령>` 이다.
//
// ★ 이 명령군은 **사람의 표면**이다(runClaim 과 같은 갈래). 세션이 프로젝트를 만드는 것은
// 자동 등록이고 그것은 여기 없다 — 여기 있는 것은 등록된 것을 보고, 접고, 치우는 길뿐이다.
func (a *App) runProject(ctx context.Context, args []string, out io.Writer) int {
	const help = "fd project ls  — 등록된 프로젝트와 그 실적을 낸다\n" +
		"  fd project rm --project <id> --reason \"...\" [--yes]  — 잔해를 지운다"
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(out, "project 하위 명령을 줘라:")
		fmt.Fprintln(out, "  "+help)
		return 2
	}
	switch args[0] {
	case "ls":
		return a.runProjectLs(ctx, args[1:], out)
	default:
		fmt.Fprintf(out, "모르는 project 하위 명령: %s\n  %s\n", clip(args[0], 40), help)
		return 2
	}
}

// runProjectLs 는 `fd project ls` 다.
func (a *App) runProjectLs(ctx context.Context, args []string, out io.Writer) int {
	fs := newFlagSet("project ls")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rr, err := a.cli.Read(ctx, "/api/v1/projects")
	if err != nil {
		fmt.Fprintf(out, "프로젝트 목록을 읽지 못했다: %v\n", err)
		return 1
	}
	var resp struct {
		Projects []service.ProjectSummary `json:"projects"`
	}
	if err := json.Unmarshal(rr.Body, &resp); err != nil {
		fmt.Fprintf(out, "응답 해석 실패: %v\n", err)
		return 1
	}
	if len(resp.Projects) == 0 {
		fmt.Fprintln(out, "등록된 프로젝트가 없다.")
		return 0
	}
	fmt.Fprintf(out, "%-34s %-6s %6s %8s %8s %s\n", "프로젝트", "상태", "항목", "세션", "판단", "마지막 세션")
	for _, p := range resp.Projects {
		state := "-"
		switch {
		case p.Pinned:
			state = "핀"
		case p.Archived:
			state = "보관"
		}
		last := "없음"
		if !p.LastSessionAt.IsZero() {
			last = humanAge(time.Since(p.LastSessionAt)) + " 전"
		}
		fmt.Fprintf(out, "%-34s %-6s %6d %8d %8d %s\n",
			clip(p.ID, 34), state, p.Items, p.Sessions, p.Judgments, last)
	}
	// ★ 지울 수 있는지를 여기서 말한다 — 사람이 rm 을 쳐 보고서야 알게 두지 않는다.
	fmt.Fprintln(out, "\n항목이나 판단이 있는 프로젝트는 지울 수 없다(판단은 원장이라 삭제 금지다).")
	fmt.Fprintln(out, "줄에서만 빼려면 대시보드에서 보관하라 — 되돌릴 수 있다.")
	return 0
}
```

**주의:** `humanAge` 의 실제 이름을 `cmd/fd/env.go` 에서 확인한다(그 파일 끝에 `humanBytes` 와
나란히 있다는 주석이 있다).

`cmd/fd/main.go` 의 분기에 한 줄(`case "claim":` 아래):

```go
	case "project":
		return app.runProject(ctx, args[1:], stdout)
```

- [ ] **Step 7: 시험이 통과하는 것을 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/service/ ./cmd/fd/ ./internal/api/ -run 'TestListProjectSummaries|TestProjectLs' -v`
Expected: PASS 2건. **`--- SKIP` 이 하나라도 나오면 그 시험은 아무것도 안 본 것이다** — 통과로 세지 않는다.

- [ ] **Step 8: 관문 다섯 줄 + 커밋**

```bash
git add internal/service/project.go internal/service/project_test.go \
        internal/api/handlers_projects.go internal/api/api.go \
        internal/store/project.go cmd/fd/project.go cmd/fd/project_test.go cmd/fd/main.go
git commit -F - <<'EOF'
feat(flightdeck): fd project ls — 등록된 프로젝트와 그 실적을 한 표로 낸다

화면 없이 이 축을 읽는 유일한 길이고, 지우기 전에 무엇이 있는지 보는 자리다.
출력이 지울 수 있는지를 함께 말한다 — rm 을 쳐 보고서야 알게 두지 않는다.

projectRefTables 를 store 에 두었다. project 에 묶이는 표 전부이자 삭제 순서다.
뒤의 둘(item_dependents·pick_eval)은 FK 가 아니라 컬럼으로만 묶여서, 안 지워도 삭제가
성공하고 조용히 고아가 남는다 — 그래서 더 위험하고 그래서 목록에 있다.
event 는 목록에 없다. FK 가 아니라 프로젝트가 사라져도 남고, 그것이 옳다.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 5: `fd project rm` — 잔해를 지운다

**Files:**
- Modify: `internal/store/project.go` (`RemoveProject`)
- Create: `internal/store/project_remove_test.go`
- Modify: `internal/service/project.go` (`RemoveProject`)
- Modify: `internal/api/handlers_projects.go` (`POST /api/v1/projects/{id}/remove`)
- Modify: `internal/api/api.go` (라우트 한 줄)
- Modify: `cmd/fd/project.go` (`rm` 하위명령)
- Modify: `cmd/fd/wire.go` (요청·응답 타입)

**Interfaces:**
- Consumes: `store.ProjectRefCounts`·`projectRefTables` (Task 4)
- Produces:
  - `func JudgeProjectRemoval(counts map[string]int) (ok bool, reason string)` — 순수 함수
  - `func (s *Store) RemoveProject(ctx context.Context, id string) error`
  - `func (s *Service) RemoveProject(ctx context.Context, id, actor, reason string, confirm bool) (ProjectRemoval, error)`
  - `type ProjectRemoval struct { Project string; Counts map[string]int; Removed bool; Refusal string }`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/store/project_remove_test.go`:

```go
package store

import (
	"context"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// TestProjectRefTablesCoverSchema 는 **삭제 순서 목록이 스키마와 맞는지**를 본다.
//
// ★ 표가 하나 늘고 목록에 안 들어가면, FK 가 있는 표는 삭제를 FK 위반으로 죽이고
// FK 가 없는 표는 조용히 고아 행을 남긴다. 뒤쪽이 더 나쁘다 — 아무 화면에도 안 뜬다.
// 그래서 사람의 기억이 아니라 스키마가 이 목록을 지킨다.
func TestProjectRefTablesCoverSchema(t *testing.T) {
	src, err := os.ReadFile("schema.sql")
	if err != nil {
		t.Fatalf("schema.sql 을 못 읽었다: %v", err)
	}
	// CREATE TABLE 블록마다 project(id) 참조가 있는지 본다.
	blocks := regexp.MustCompile(`(?im)^CREATE\s+(?:VIRTUAL\s+)?TABLE\s+(?:IF NOT EXISTS\s+)?([A-Za-z_][A-Za-z0-9_]*)`).
		FindAllStringSubmatchIndex(string(src), -1)
	if len(blocks) == 0 {
		t.Fatal("전제가 깨졌다 — schema.sql 에서 표를 하나도 못 찾았다")
	}
	inList := map[string]bool{}
	for _, tbl := range projectRefTables {
		inList[tbl] = true
	}

	var missing []string
	for i, b := range blocks {
		name := string(src[b[2]:b[3]])
		end := len(src)
		if i+1 < len(blocks) {
			end = blocks[i+1][0]
		}
		body := string(src[b[1]:end])
		if !strings.Contains(body, "REFERENCES project(id)") {
			continue
		}
		if !inList[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("project(id) 를 참조하는데 projectRefTables 에 없는 표: %v — "+
			"삭제가 FK 위반으로 죽거나 고아 행이 남는다", missing)
	}
}

// TestRemoveProjectRefusesWithItems 는 항목이 있으면 안 지운다는 단정이다.
func TestRemoveProjectRefusesWithItems(t *testing.T) {
	ok, reason := JudgeProjectRemoval(map[string]int{"item": 1, "judgment": 0})
	if ok {
		t.Fatal("항목이 있는데 통과했다 — 639항목짜리를 한 명령으로 날리는 길이 열린다")
	}
	if !strings.Contains(reason, "항목") {
		t.Fatalf("사유가 무엇이 막았는지를 안 말한다: %q", reason)
	}
}

// TestRemoveProjectRefusesWithJudgments 는 판단이 있으면 안 지운다는 단정이다.
//
// ★ 이것은 정책이 아니라 **원장이 정한 제약**이다. judgment_no_delete 트리거가 판단 삭제를
// 원리적으로 막고, judgment.project 가 FK 라 프로젝트 행을 붙잡는다. 우회하지 않는다.
func TestRemoveProjectRefusesWithJudgments(t *testing.T) {
	ok, reason := JudgeProjectRemoval(map[string]int{"item": 0, "judgment": 3})
	if ok {
		t.Fatal("판단이 있는데 통과했다 — FK 위반으로 죽는다")
	}
	if !strings.Contains(reason, "판단") {
		t.Fatalf("사유가 무엇이 막았는지를 안 말한다: %q", reason)
	}
}

// TestRemoveProjectDeletesChildrenAndKeepsEvents 는 실제 삭제 한 바퀴다.
func TestRemoveProjectDeletesChildrenAndKeepsEvents(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.UpsertProject(ctx, model.Project{
		ID: "junk", Path: "/tmp/junk", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("등록 실패: %v", err)
	}
	s.LogEvent(ctx, "project.test", "junk", "", map[string]any{"why": "삭제 뒤에도 남아야 한다"})

	if err := s.RemoveProject(ctx, "junk"); err != nil {
		t.Fatalf("삭제 실패: %v", err)
	}
	if _, err := s.GetProject(ctx, "junk"); err == nil {
		t.Fatal("프로젝트가 그대로 있다")
	}

	// ★ event 는 남는다. event.project 는 FK 가 아니라 컬럼이라 FK 도 안 울고,
	//   "이런 프로젝트가 있었다"가 원장에 남는 유일한 길이다.
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM event WHERE project = ?`, "junk").Scan(&n); err != nil {
		t.Fatalf("이벤트 조회 실패: %v", err)
	}
	if n == 0 {
		t.Fatal("삭제가 이벤트까지 가져갔다 — 그러면 지웠다는 사실 자체가 원장에서 사라진다")
	}
}
```

- [ ] **Step 2: 시험이 실패하는 것을 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/store/ -run 'TestProjectRefTables|TestRemoveProject' -v`
Expected: 컴파일 실패 — `JudgeProjectRemoval` · `RemoveProject` 없음

- [ ] **Step 3: 판정과 삭제를 쓴다**

`internal/store/project.go`:

```go
// JudgeProjectRemoval 은 프로젝트를 지워도 되는지 판정한다. 순수 함수다.
//
// ★ 막는 축이 둘이고 성격이 다르다.
//   ⒜ 항목 — **정책**이다. 639항목짜리 프로젝트를 한 명령으로 날리는 길을 안 만든다.
//      강제 플래그도 안 만든다: 한 번 만들면 다음 사람이 그것을 쓴다.
//   ⒝ 판단 — **원장이 정한 제약**이다. judgment_no_delete 트리거가 판단 삭제를 원리적으로
//      막고 judgment.project 가 FK 라 프로젝트 행을 붙잡는다. 우회는 기각이다 —
//      PRAGMA foreign_keys=OFF 도 트리거 드롭도 잔해 몇 건과 바꿀 값이 아니다.
//      줄에서만 빼면 되는 경우라 화면의 보관이 그 자리를 받는다.
func JudgeProjectRemoval(counts map[string]int) (bool, string) {
	if n := counts["item"]; n > 0 {
		return false, fmt.Sprintf("큐 항목이 %d건 있다 — 항목이 있는 프로젝트는 지우지 않는다. "+
			"줄에서만 빼려면 대시보드에서 보관하라", n)
	}
	if n := counts["judgment"]; n > 0 {
		return false, fmt.Sprintf("판단이 %d건 있다 — 판단은 원장이라 삭제 금지 트리거가 있고, "+
			"그것이 이 프로젝트 행을 붙잡는다(FK). 줄에서만 빼려면 대시보드에서 보관하라", n)
	}
	return true, "지울 수 있다 — 항목도 판단도 없다"
}

// RemoveProject 는 프로젝트와 거기 묶인 행 전부를 지운다.
//
// ★ 판정은 부르는 쪽이 한다(JudgeProjectRemoval). 여기서 다시 세면 판정이 두 벌이 되고,
//   두 벌은 반드시 표류한다.
//
// ★ event 는 안 지운다 — projectRefTables 의 그 주석대로다.
func (s *Store) RemoveProject(ctx context.Context, id string) error {
	return s.Tx(ctx, func(t *Tx) error {
		// 지운다는 사실을 **먼저** 예약한다. 예약 이벤트는 롤백 뒤에도 흘러서
		// 아래가 통째로 죽어도 "무엇을 지우려 했나"가 원장에 남는다.
		t.LogEvent("project.remove", id, "", map[string]any{"project": clip(id, 64)})

		for _, tbl := range projectRefTables {
			if tbl == "judgment" {
				// 판단은 트리거가 막는다. 여기 오면 판정이 먼저 거절했어야 한다 —
				// 0건이면 DELETE 가 무해하므로 그대로 두면 조용히 통과하고,
				// 0건이 아니면 트리거가 정확한 말로 죽인다. 건너뛰지 않는 이유가 그것이다.
				continue
			}
			if _, err := t.tx.ExecContext(t.ctx,
				`DELETE FROM `+tbl+` WHERE project = ?`, id); err != nil {
				return fmt.Errorf("행 삭제 실패(table=%s, project=%q): %w", tbl, clip(id, 64), err)
			}
		}
		res, err := t.tx.ExecContext(t.ctx, `DELETE FROM project WHERE id = ?`, id)
		if err != nil {
			return fmt.Errorf("프로젝트 삭제 실패(id=%q): %w", clip(id, 64), err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("프로젝트 삭제 결과 확인 실패(id=%q): %w", clip(id, 64), err)
		}
		if n == 0 {
			return fmt.Errorf("프로젝트 %q: %w", clip(id, 64), ErrNotFound)
		}
		return nil
	})
}
```

- [ ] **Step 4: 시험이 통과하는 것을 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/store/ -run 'TestProjectRefTables|TestRemoveProject' -v`
Expected: PASS 4건

- [ ] **Step 5: service · REST · CLI 를 잇는다**

`internal/service/project.go`:

```go
// ProjectRemoval 은 삭제 요청의 결과다. **세기만 한 경우와 지운 경우가 같은 타입이다** —
// 두 타입으로 가르면 CLI 가 둘을 각자 그리게 되고 문구가 갈린다.
type ProjectRemoval struct {
	Project string         `json:"project"`
	Counts  map[string]int `json:"counts"`
	Removed bool           `json:"removed"`
	Refusal string         `json:"refusal,omitempty"`
}

// RemoveProject 는 프로젝트를 지운다. confirm 이 false 면 세기만 한다.
//
// ★ 세는 것과 지우는 것이 같은 함수인 이유: 다른 함수로 세면 세고 나서 지우기 전에 바뀐
// 것을 못 본다. 같은 자리에서 세고 판정하고 지운다.
func (s *Service) RemoveProject(ctx context.Context, id, actor, reason string,
	confirm bool) (ProjectRemoval, error) {

	if strings.TrimSpace(reason) == "" {
		return ProjectRemoval{}, &RefusedError{What: "project remove",
			Reason:   "사유가 비었다",
			Guidance: "되돌릴 수 없는 삭제다 — 왜 지우는지를 적어라. 원장에 남는다."}
	}
	counts, err := s.st.ProjectRefCounts(ctx, id)
	if err != nil {
		return ProjectRemoval{}, err
	}
	out := ProjectRemoval{Project: id, Counts: counts}
	if ok, why := store.JudgeProjectRemoval(counts); !ok {
		out.Refusal = why
		return out, nil
	}
	if !confirm {
		out.Refusal = "확인이 없다 — 무엇이 함께 지워질지 위에 있다. 지우려면 --yes 를 붙여라"
		return out, nil
	}
	if err := s.st.RemoveProject(ctx, id); err != nil {
		return ProjectRemoval{}, err
	}
	s.st.LogEvent(ctx, "project.removed", id, "", map[string]any{
		"actor": clip(actor, 120), "reason": clip(reason, 400), "counts": counts,
	})
	out.Removed = true
	return out, nil
}
```

**주의:** `RefusedError` 와 `clip` 의 실제 이름·시그니처를 `internal/service/` 에서 확인해 맞춘다
(`session.go:161` 이 `&RefusedError{What:, Reason:, Guidance:}` 로 쓰고 있다).

`internal/api/handlers_projects.go` 에 더한다:

```go
// handleRemoveProject 는 프로젝트를 지운다. 사유가 필수고, confirm 이 없으면 세기만 한다.
func (s *server) handleRemoveProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Actor   string `json:"actor"`
		Reason  string `json:"reason"`
		Confirm bool   `json:"confirm"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	res, err := s.svc.RemoveProject(r.Context(), r.PathValue("id"), req.Actor, req.Reason, req.Confirm)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, res)
}
```

`internal/api/api.go`:

```go
	mux.HandleFunc("POST /api/v1/projects/{id}/remove", s.handleRemoveProject)
```

`cmd/fd/project.go` 의 `switch` 에 `rm` 을 더하고 구현한다:

```go
	case "rm":
		return a.runProjectRm(ctx, args[1:], out)
```

```go
// runProjectRm 은 `fd project rm --project <id> --reason "..." [--yes]` 다.
//
// ★ --yes 없이 부르면 **세기만 한다.** 무엇이 함께 지워질지를 먼저 보여주는 것이
// 이 명령의 절반이다 — 되돌릴 수 없기 때문이다.
func (a *App) runProjectRm(ctx context.Context, args []string, out io.Writer) int {
	fs := newFlagSet("project rm")
	project := fs.String("project", "", "지울 프로젝트 id")
	reason := fs.String("reason", "", "왜 지우나 — 필수. 원장에 남는다")
	yes := fs.Bool("yes", false, "실제로 지운다. 없으면 무엇이 지워질지 세기만 한다")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	set := flagsSet(fs)
	if !set["project"] || !set["reason"] {
		fmt.Fprintln(out, "지울 대상과 사유를 줘라: fd project rm --project <id> --reason \"...\"")
		fmt.Fprintln(out, "무엇이 있는지는 `fd project ls` 가 낸다. 되돌릴 수 없는 삭제다.")
		return 2
	}

	a.cli.Flush(ctx)
	user, _ := a.env("USER")
	res, err := a.cli.Write(ctx, "project-remove",
		"/api/v1/projects/"+url.PathEscape(*project)+"/remove", projectRemoveReq{
			Actor: user, Reason: *reason, Confirm: *yes,
		})
	if err != nil {
		fmt.Fprintf(out, "지우지 못했다: %v\n", err)
		return 1
	}
	if !res.Sent {
		fmt.Fprintf(out, "%s: %s\n", res.Mode, res.Reason)
		return 1
	}
	var rr service.ProjectRemoval
	if err := json.Unmarshal(res.Body, &rr); err != nil {
		fmt.Fprintf(out, "응답 해석 실패: %v\n", err)
		return 1
	}

	fmt.Fprintf(out, "프로젝트 %s 에 묶인 것:\n", rr.Project)
	for _, k := range sortedKeys(rr.Counts) {
		if rr.Counts[k] > 0 {
			fmt.Fprintf(out, "  %-20s %d\n", k, rr.Counts[k])
		}
	}
	// ★ event 는 지워지지 않는다는 사실을 여기서 말한다. 안 그러면 사람이 "다 지웠다"고
	//   믿고, 나중에 event 에서 그 프로젝트 이름을 보고 삭제가 실패한 줄 안다.
	fmt.Fprintln(out, "  (event 는 안 지운다 — 지웠다는 사실 자체가 거기 남는다)")

	if rr.Refusal != "" {
		fmt.Fprintf(out, "\n안 지웠다: %s\n", rr.Refusal)
		return 1
	}
	if rr.Removed {
		fmt.Fprintf(out, "\n지웠다. 프로젝트 %s 는 원장에서 사라졌다.\n", rr.Project)
		fmt.Fprintln(out, "그 경로에서 세션이 다시 열리면 자동 등록으로 다시 생긴다 — "+
			"워크트리 잔해라면 그 경로부터 없애라.")
	}
	return 0
}

// sortedKeys 는 표 이름을 정렬해 낸다. 출력 순서가 흔들리면 시험이 흔들린다.
func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
```

`cmd/fd/wire.go` 에:

```go
// projectRemoveReq 는 프로젝트 삭제 요청이다.
type projectRemoveReq struct {
	Actor   string `json:"actor"`
	Reason  string `json:"reason"`
	Confirm bool   `json:"confirm"`
}
```

- [ ] **Step 6: CLI 시험을 더한다**

`cmd/fd/project_test.go` 에 이어 붙인다. 하네스가 **실물 서버**라 이 셋이 진짜 경로를 돈다:

```go
// TestProjectRmNeedsReason 은 사유 없는 삭제를 CLI 가 먼저 막는다는 단정이다.
func TestProjectRmNeedsReason(t *testing.T) {
	h := newHarness(t)
	code, out := h.run("", "project", "rm", "--project", "junk")
	if code != 2 {
		t.Fatalf("종료코드 %d, 기대 2\n%s", code, out)
	}
	if !strings.Contains(out, "사유") {
		t.Fatalf("무엇이 없어서 막혔는지를 안 말한다\n%s", out)
	}
}

// TestProjectRmWithoutYesOnlyCounts 는 --yes 없이는 세기만 한다는 단정이다.
// 되돌릴 수 없는 일이라 이 한 단계가 이 명령의 절반이다.
func TestProjectRmWithoutYesOnlyCounts(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.st.UpsertProject(ctx, model.Project{
		ID: "junk", Path: "/tmp/junk", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("등록 실패: %v", err)
	}

	code, out := h.run("", "project", "rm", "--project", "junk", "--reason", "워크트리 잔해다")
	if code != 1 {
		t.Fatalf("종료코드 %d, 기대 1(안 지웠다)\n%s", code, out)
	}
	if !strings.Contains(out, "--yes") {
		t.Fatalf("어떻게 실제로 지우는지를 안 말한다\n%s", out)
	}
	// ★ 실물 서버라 여기서 원장을 직접 본다 — "안 지웠다"가 출력이 아니라 사실이어야 한다.
	if _, err := h.st.GetProject(ctx, "junk"); err != nil {
		t.Fatalf("--yes 가 없는데 지워졌다: %v", err)
	}
}

// TestProjectRmRefusesWhenJudgmentsExist 는 판단이 있으면 --yes 로도 안 지워진다는 단정이다.
// 이것은 정책이 아니라 원장이 정한 제약이다(judgment_no_delete + FK).
func TestProjectRmRefusesWhenJudgmentsExist(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	// 하네스의 기본 프로젝트에 판단을 하나 남긴다.
	// ★ 판단을 만드는 경로는 service 의 공개 API 를 쓴다 — store 직접 INSERT 로 만들면
	//   이 시험이 실제 사용 경로와 다른 모양의 행을 두고 단정하게 된다.
	if _, err := h.svc.Note(ctx, service.NoteInput{
		Project: h.project, Kind: "decision",
		Title: "판단 하나", Body: "이 프로젝트는 지울 수 없어야 한다",
	}); err != nil {
		t.Fatalf("판단 남기기 실패: %v", err)
	}

	code, out := h.run("", "project", "rm", "--project", h.project,
		"--reason", "지워질 리 없다", "--yes")
	if code != 1 {
		t.Fatalf("종료코드 %d, 기대 1(거절)\n%s", code, out)
	}
	if !strings.Contains(out, "판단") {
		t.Fatalf("무엇이 막았는지를 안 말한다\n%s", out)
	}
	if _, err := h.st.GetProject(ctx, h.project); err != nil {
		t.Fatalf("판단이 있는데 지워졌다: %v", err)
	}
}
```

**주의:** `service.NoteInput` 의 실제 필드 이름을 확인해 맞춘다
(`grep -n "type NoteInput struct" -A 12 internal/service/*.go`). 판단을 만드는 더 짧은 경로가
그 패키지에 있으면 그것을 쓴다 — 이 시험이 필요한 것은 「판단이 한 건 있다」는 상태뿐이다.

- [ ] **Step 7: 전건 + 관문 다섯 줄**

Run: `cd plugins/flightdeck/server && go test ./... && gofmt -l . && go vet ./... && GOOS=darwin GOARCH=arm64 go vet ./... && GOOS=windows GOARCH=amd64 go vet ./...`

- [ ] **Step 8: 커밋**

```bash
git add internal/store/project.go internal/store/project_remove_test.go \
        internal/service/project.go internal/api/handlers_projects.go internal/api/api.go \
        cmd/fd/project.go cmd/fd/project_test.go cmd/fd/wire.go
git commit -F - <<'EOF'
feat(flightdeck): fd project rm — 잔해를 지운다. 항목이나 판단이 있으면 거절한다

--yes 없이 부르면 무엇이 함께 지워질지 세어 보여주고 멈춘다. 세는 것과 지우는 것이 같은
함수인 이유는, 다른 함수로 세면 세고 나서 지우기 전에 바뀐 것을 못 보기 때문이다.

막는 축이 둘이고 성격이 다르다. 항목은 정책이다 — 639항목짜리를 한 명령으로 날리는 길을
안 만들고 강제 플래그도 안 만든다. 판단은 원장이 정한 제약이다 — judgment_no_delete
트리거가 판단 삭제를 원리적으로 막고 judgment.project 가 FK 라 프로젝트 행을 붙잡는다.
우회(foreign_keys=OFF·트리거 드롭)는 기각했다. 그 경우는 화면의 보관이 받는다.

삭제 순서 목록을 시험이 schema.sql 과 대조한다. 표가 하나 늘고 목록에 안 들어가면
FK 있는 표는 삭제를 죽이고 FK 없는 표는 조용히 고아를 남긴다 — 뒤쪽이 더 나쁘다.

event 는 안 지운다. FK 가 아니라 프로젝트가 사라져도 남고, 그것이 "이런 프로젝트가
있었고 언제 지워졌다"가 원장에 남는 유일한 길이다. CLI 가 그 사실을 출력에 말한다.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 6: DESIGN.md 를 맞추고 별건을 큐에 낸다

**Files:**
- Modify: `plugins/flightdeck/DESIGN.md` (§웹UI · §CLI · §7)

**Interfaces:**
- Consumes: Task 1~5 전부

★ **이 파일은 다른 세션 넷과 겹친다**(pick 응답의 겹침 4건 중 넷 다 `DESIGN.md`).
**커밋 직전에 `git fetch && git log --oneline origin/main -5` 로 남이 먼저 랜딩했는지 보고,
그렇다면 리베이스한 뒤 §웹UI 소절이 아직 자기 자리에 있는지 확인한다.**

- [ ] **Step 1: §웹UI 1번 절 앞에 프로젝트 줄 소절을 더한다**

`DESIGN.md` 의 `### 웹 UI — 읽기 전용 HTML 한 장, 섹션 6개, 쓰기 버튼 5개(살아 있는 것은 셋)`
바로 아래, `1. **지금 — 잡혀 있는 작업.**` 앞에 넣는다:

```markdown
**헤더의 프로젝트 줄은 사람이 고른 것만 편다.** `project.pinned_at`·`archived_at` 두 축이고,
핀만 줄에 남으며 나머지는 `<details>` 하나로 접힌다. **JS 를 안 쓴다** — 이 페이지의 다른
접기(`.fold`/`.more`)는 스크립트로 접어서 없으면 전부 펴지는데, 그 폴백은 긴 목록에는 옳지만
이 줄에는 반대다.

- **핀이 0이면 아무것도 안 접는다.** 핀이 없다는 사실을 자동 판정(활동이 있는 것만 편다)으로
  덮으면, 사람이 접은 것과 규칙이 접은 것이 화면에서 같은 모양이 되고 「왜 사라졌나」에
  답할 수 없다. 그래서 이 축에 자동 판정을 안 둔다.
- **접은 수를 반드시 말한다**(`OutOfWindow`·`Folded` 와 같은 규율). 보관이 몇 건인지도 함께.
- **보고 있는 프로젝트는 핀이 아니어도 편다.** 아니면 화면이 자기 위치를 안 말한다.
- **보관은 접근 차단이 아니다** — `?project=` 로 그대로 열린다. 이것이 이 축을 표시 계층이라
  부를 수 있는 근거고, 아래 폼 상한이 이 폼을 빼는 판정이 그 위에 선다.
- **보관은 자동으로 안 풀린다.** 풀면 훅이 연 세션 하나로 잔해가 다시 튀어나온다. 대신 보관
  목록이 마지막 세션 시각을 함께 내고, 푸는 것은 사람이 한다.

**표시 축 폼은 「파생물에 쓰는 폼」 상한 넷에서 빠진다.** 로그아웃과 같은 부류의 다른
근거다 — 로그아웃은 원장에 아무것도 안 남겨서 빠졌고, 이쪽은 원장에 쓰지만 그 두 컬럼이
항목·판단·선점·랜딩 어디에도 안 닿는다. 그 증거는 화면 밖에 있다: 접힌 프로젝트도 URL 로
열린다(`web/project_nav_test.go` 의 `TestArchivedProjectStillOpens`). 그 시험이 빨개지면
이 면제의 근거도 함께 무너진다 — **두 자리가 한 판정을 나눠 든다.**
사유도 안 받는다: 사유가 필수인 셋은 전부 남의 일을 뺏거나 되돌릴 수 없는 것인데
핀·보관은 둘 다 아니다.
```

- [ ] **Step 2: §CLI 목록에 `project` 를 더한다**

`### CLI `bin/fd`` 절의 명령 목록에 `project` 를 넣고, 그 아래에 한 소절:

```markdown
**`fd project ls|rm` 은 사람의 표면이다**(`claim release` 와 같은 갈래). 세션이 프로젝트를
만드는 것은 자동 등록이라 여기 없고, 여기 있는 것은 등록된 것을 보고 치우는 길뿐이다.

**진짜 삭제는 화면에 없다.** 되돌릴 수 없는 일을 클릭 하나에 두지 않는다. 그리고 지울 수
있는 것에 한계가 있다 — **항목이 있으면 정책으로 거절**하고(639항목짜리를 한 명령으로 날리는
길을 안 만든다. 강제 플래그도 안 만든다), **판단이 있으면 원장이 거절한다**
(`judgment_no_delete` 트리거가 판단 삭제를 막고 `judgment.project` FK 가 프로젝트 행을
붙잡는다). 그 우회(`PRAGMA foreign_keys=OFF` · 트리거 드롭)는 기각이다 — 잔해 몇 건과
바꿀 값이 아니고, 증분 가드의 `neverExempt` 가 노리는 것과 같은 부류의 손실이다.
그 경우는 화면의 보관이 받는다.

**`event` 는 안 지운다.** `event.project` 는 FK 가 아니라 컬럼이라 프로젝트가 사라져도 남고,
그것이 「이런 프로젝트가 있었고 언제 지워졌다」가 원장에 남는 유일한 길이다.
```

- [ ] **Step 3: §7 에 이번 회차의 판정을 적는다**

§7(판정 기록 절)에 세 줄을 더한다: ⑴ 자동 활동 판정을 기각한 이유 ⑵ 표시 축 폼을 상한에서
뺀 근거와 그것을 지탱하는 시험 ⑶ 판단이 있는 프로젝트를 못 지운다는 원장 제약과 우회 기각.

- [ ] **Step 4: 문서 관문을 돌린다**

Run: `cd plugins/flightdeck/server && go test ./... -run 'InDesign|MatchDesign|DesignSort'`
Expected: PASS. 이 저장소에는 DESIGN 본문을 실제로 읽는 관문이 다섯 있다 —
`TestDesignSortKeyParagraphNamesEveryLiveAxis`(judge) · `TestItemBodyImmutabilityIsNamedInDesign` ·
`TestLedgerDerivedAxisIsNamedInDesign` · `TestDeclaredTablesMatchDesign` ·
`TestTxOutcomeAxisIsNamedInDesign`(전부 store). 문구를 옮기거나 절을 끼워 넣다가 이들이
찾는 문장을 밀면 여기서 빨개진다.

그 다음 전건: `go test ./...`

- [ ] **Step 5: 별건 항목을 큐에 낸다**

MCP `add` 로 등록한다. id `fd-worktree-paths-register-as-separate-projects`,
제목 「워크트리 경로가 별도 프로젝트로 등록된다 — 잔해 4건의 출처」.
본문에 적을 것: `service/session.go:159` 가 세션을 열 때 없는 프로젝트를 자동 등록한다 ·
클라이언트의 `ProjectIDFromPath`(`cmd/fd/env.go:654`)는 `MainRepoRoot` 로 주 저장소를 찾는데도
실측 잔해 3건이 `context-platform` 의 워크트리 이름으로 등록됐다(2026-08-06~08) ·
그 경위가 안 밝혀졌다 · `machine-probe` 의 `/tmp` 경로는 아직 살아 있어 다시 생길 수 있다.
경로: `plugins/flightdeck/server/internal/service/session.go` ·
`plugins/flightdeck/server/cmd/fd/env.go`.

- [ ] **Step 6: 커밋**

```bash
git add ../DESIGN.md
git commit -F - <<'EOF'
docs(flightdeck): 프로젝트 줄의 규율 다섯과 삭제의 한계 둘을 DESIGN 에 적는다

화면 절에 프로젝트 줄 소절을 넣었다 — 핀이 0이면 안 접는다 · 접은 수를 말한다 ·
보고 있는 것은 언제나 편다 · 보관은 접근 차단이 아니다 · 보관은 자동으로 안 풀린다.

폼 상한 면제의 근거를 적었다. 로그아웃과 같은 부류의 다른 근거다 — 그쪽은 원장에 아무것도
안 남겨서, 이쪽은 원장에 쓰지만 두 컬럼이 항목·판단·선점·랜딩 어디에도 안 닿아서다.
그 증거는 시험이 든다(접힌 프로젝트도 URL 로 열린다). 두 자리가 한 판정을 나눠 든다.

CLI 절에 삭제의 한계 둘을 적었다: 항목은 정책이 막고 판단은 원장이 막는다. 우회는 기각.
event 는 안 지운다 — 지웠다는 사실이 남는 유일한 길이다.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
```

---

## 랜딩 전 마지막 확인

- [ ] `cd plugins/flightdeck/server && gofmt -l .` — 빈 출력이어야 한다. **무출력이 통과가 아니다**: 먼저 `pwd` 로 모듈 안인지 확인한다(모듈 밖이면 빈 디렉토리를 검사하고 조용히 통과한다).
- [ ] `go vet ./...` · `go test ./...`
- [ ] `GOOS=darwin GOARCH=arm64 go vet ./...` · `GOOS=windows GOARCH=amd64 go vet ./...` — `go build` 는 `_test.go` 를 건너뛰므로 교차 검증은 `go vet` 이어야 한다.
- [ ] 실물 확인: 서버를 새 이미지로 올리고(`docker compose up -d --build`, 갱신은 **옛 버전 디렉토리에서 `down` 을 먼저**) 브라우저로 프로젝트 줄을 눌러 본다. 핀 · 접힘 · 보관 · 되돌리기 넷이 실제로 도는지.
- [ ] `fd project ls` 가 11건을 상태와 함께 내는지, `fd project rm --project machine-probe --reason "…"` 이 `--yes` 없이 셈만 내는지.
