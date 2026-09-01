package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 실물 SQLite 파일로 돌린다. 목(mock) 을 만들지 않는다 —
// 이 계층이 지키는 것의 대부분(트리거·부분 유니크 인덱스·FK·원자적 발번)이
// **DB 엔진의 동작**이라, 흉내로 바꾸는 순간 시험이 사본을 단정하게 된다.

func newStore(t *testing.T) *Store {
	t.Helper()
	// 로그를 버린다 — 마이그레이션 INFO 가 시험 출력을 덮는다.
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dbp := filepath.Join(t.TempDir(), "fd.db")
	mustMigrate(t, dbp)
	mustMigrate(t, dbp)
	s, err := OpenWithLogger(dbp, log)
	if err != nil {
		t.Fatalf("Open 실패: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// seed 는 project·machine 을 넣는다. 세션의 FK 대상이라 없으면 아무것도 못 한다.
func seed(t *testing.T, s *Store, project string) {
	t.Helper()
	ctx := context.Background()
	if err := s.UpsertProject(ctx, model.Project{ID: project, Path: "/repo/" + project}); err != nil {
		t.Fatalf("프로젝트 등록 실패: %v", err)
	}
	if err := s.UpsertMachine(ctx, model.Machine{ID: "m1", Hostname: "dev"}); err != nil {
		t.Fatalf("머신 등록 실패: %v", err)
	}
}

func mustSession(t *testing.T, s *Store, project, cc string) model.Session {
	t.Helper()
	sess, _, err := s.OpenSession(context.Background(), project, "m1", "/w/"+cc, cc, "", time.Time{})
	if err != nil {
		t.Fatalf("세션 등록 실패(cc=%s): %v", cc, err)
	}
	return sess
}

// mustSessionAtOldSchema 는 **옛 판 DB** 에 세션 한 행을 직접 넣는다.
//
// ★ 증분 시험에는 mustSession 을 쓰면 안 되는 자리가 있다. 그 시험들은 특정 판(예: 11)을
// 재현한 DB 를 openRaw 로 열고 쓰는데, store 의 질의는 **지금 판의 칼럼**을 SELECT 하므로
// 그 DB 에서 "no such column" 으로 죽는다 — 그 실패는 증분 결함처럼 보이지만 실은
// 시험의 좌표가 틀린 것이다(013 이 session.harness 를 더하면서 실제로 그렇게 죽었다).
//
// 그래서 여기서는 **v1 부터 있던 칼럼만** 쓴다. 뒤에 무엇이 더해져도 이 헬퍼는 안 깨진다.
func mustSessionAtOldSchema(t *testing.T, s *Store, project, cc string) string {
	t.Helper()
	id := NewID()
	if _, err := s.db.Exec(
		`INSERT INTO session(id, project, machine_id, worktree, cc_session_id, label, state, blocked_why, opened_at)
		 VALUES (?, ?, ?, ?, ?, NULL, 'active', NULL, ?)`,
		id, project, "m1", "/w/"+cc, cc, fmtTime(time.Now().UTC())); err != nil {
		t.Fatalf("옛 판 스키마에 세션 등록 실패(cc=%s): %v", cc, err)
	}
	return id
}

func mustItem(t *testing.T, s *Store, project, id string) {
	t.Helper()
	err := s.AddItem(context.Background(), model.Item{
		Project: project, ID: id, Title: id, Body: "본문", Paths: []string{"services/"},
	})
	if err != nil {
		t.Fatalf("항목 등록 실패(%s): %v", id, err)
	}
}

// dropNonIdempotentColumns 는 "옛 버전인 척" 픽스처가 schema_version 행을 되돌리기
// **직전에** 불러야 한다.
//
// ★ 왜 필요한가. 이 패키지의 마이그레이션 시험들은 전부 같은 기법을 쓴다 — 먼저
// OpenWithLogger 로 최신판까지 물리적으로 다 올린 뒤, schema_version 표의 행만 지워
// 옛 버전인 척한다(makeV1DB 의 주석이 이 규율을 이미 적어 뒀다). DELETE FROM 류 증분은
// 재적용이 안전하지만 ALTER TABLE ADD COLUMN 류(007…)는 이미 있는 컬럼을 또 만들려다
// "duplicate column name" 으로 죽는다 — 007 SQL 자신의 주석이 "멱등이 아니어도 족하다,
// 증분은 schema_version 으로 정확히 한 번만 돈다" 고 적은 전제가 이 픽스처 기법과
// 정확히 어긋나는 자리다.
//
// ★ 왜 한 자리인가. 이 함수가 없으면 되돌리기 지점마다(지금 여섯 곳) 같은 ALTER 두 줄을
// 복제해야 하고, 새 비멱등 증분이 생길 때마다 그 여섯 곳을 전부 찾아 고쳐야 한다 —
// 하나라도 빠뜨리면 그 시험만 "duplicate column name" 으로 조용히 깨진다. scanProject 를
// 하나로 모은 것과 같은 이유다: 컬럼을 나열하는 자리가 여럿이면 하나만 고쳐지고
// 나머지는 다음 증분에서 깨진다. 새 비멱등 증분은 여기 목록 한 줄만 늘리면 된다.
//
// exec 는 `*sql.DB.Exec`·`*sql.Tx.Exec` 를 감싼 것이다 — 두 타입 다 variadic 이라
// `func(string) (sql.Result, error)` 에 그대로 안 맞으므로 호출부가 감싼다.
// ★ 이름이 2026-08-23 에 바뀌었다(dropNonIdempotentColumns → undoNonIdempotentMigrations).
// 증분 011 이 **표를 지우는** 첫 증분이라, v1 로 되돌리기가 더는 "걷기"만이 아니다 —
// 지워진 것은 **다시 만들어야** 하고, 안 만들면 증분 010 이 없는 표에 DELETE 를 걸어
// v1 판올림 경로가 통째로 죽는다. 이름을 그대로 뒀으면 다음 사람이 CREATE 를 여기 둘
// 생각을 못 한다.
//
// ★ 새 증분을 더할 때마다 그 **짝**을 여기 더해야 한다. 안 더하면 재판올림이
// "table already exists" 나 "no such table" 로 죽는데, 그 실패는 마이그레이션 결함처럼
// 보이지만 실제로는 이 목록의 누락이다.
func undoNonIdempotentMigrations(t *testing.T, exec func(string) (sql.Result, error)) {
	t.Helper()
	for _, q := range []string{
		// 007 · project.pinned_at·archived_at
		`ALTER TABLE project DROP COLUMN pinned_at`,
		`ALTER TABLE project DROP COLUMN archived_at`,
		// 009 · judgment_link.target_project
		`ALTER TABLE judgment_link DROP COLUMN target_project`,
		// 013 · session.harness
		`ALTER TABLE session DROP COLUMN harness`,
		// 011 · item_dependents 를 **되살린다**(schema.sql 의 v1 정의 그대로).
		`CREATE TABLE IF NOT EXISTS item_dependents (
  project TEXT NOT NULL,
  item_id TEXT NOT NULL,
  n       INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (project, item_id)
)`,
	} {
		if _, err := exec(q); err != nil {
			t.Fatalf("비멱등 증분 되돌리기 실패(%s): %v", q, err)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 열기 · 마이그레이션
// ─────────────────────────────────────────────────────────────────────────────

func TestEmbeddedSchemaIsReal(t *testing.T) {
	// 임베드가 조용히 비면 스키마가 하나도 안 들어간 DB 로 전 시험이 초록이 될 수 있다.
	// 여기서 못박는다.
	if len(schemaSQL) < 1000 {
		t.Fatalf("임베드된 schema.sql 이 너무 짧다(%d바이트) — //go:embed 가 안 걸렸을 수 있다", len(schemaSQL))
	}
	for _, want := range []string{"CREATE TRIGGER judgment_no_update", "resource_one_holder", "UNIQUE (machine_id, worktree, cc_session_id)"} {
		if !strings.Contains(schemaSQL, want) {
			t.Errorf("임베드된 스키마에 %q 가 없다", want)
		}
	}
}

func TestOpenAppliesSchemaAndPragmas(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	var v int
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_version`).Scan(&v); err != nil {
		t.Fatalf("schema_version 읽기 실패: %v", err)
	}
	if v != SchemaVersion {
		t.Errorf("schema_version = %d, want %d", v, SchemaVersion)
	}

	// pragma 는 **되읽어** 확인한다. 드라이버가 모르는 pragma 이름을 조용히 무시하므로
	// "DSN 에 적었다"는 것은 "걸렸다"의 근거가 아니다.
	for name, want := range wantPragmas {
		var got string
		if err := s.db.QueryRowContext(ctx, "PRAGMA "+name).Scan(&got); err != nil {
			t.Fatalf("PRAGMA %s 실패: %v", name, err)
		}
		if !strings.EqualFold(got, want) {
			t.Errorf("PRAGMA %s = %q, want %q", name, got, want)
		}
	}

	// FK 가 실제로 무는지 — pragma 되읽기만으로는 "값이 1이다"까지고 "문다"는 아니다.
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO session(id,project,machine_id,worktree,cc_session_id,state,opened_at)
		 VALUES ('x','없는프로젝트','없는머신','/w','cc','active','2026-01-01T00:00:00.000000Z')`)
	if err == nil {
		t.Error("없는 project·machine 을 가리키는 세션이 들어갔다 — FK 가 안 물고 있다")
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fd.db")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mustMigrate(t, path)
	s1, err := OpenWithLogger(path, log)
	if err != nil {
		t.Fatalf("첫 Open 실패: %v", err)
	}
	seed(t, s1, "p")
	s1.Close()

	mustMigrate(t, path)
	s2, err := OpenWithLogger(path, log)
	if err != nil {
		t.Fatalf("두 번째 Open 실패: %v", err)
	}
	defer s2.Close()
	if _, err := s2.GetProject(context.Background(), "p"); err != nil {
		t.Errorf("두 번째 Open 후 데이터가 사라졌다: %v", err)
	}
	// 백업이 생기면 안 된다 — 적용할 마이그레이션이 없으므로.
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak-") {
			t.Errorf("마이그레이션이 없는데 백업이 생겼다: %s", e.Name())
		}
	}
}

// ★ 스키마 버전 불일치 거절.
// 대조가 성립했는지를 **결과를 읽기 전에** 단정한다 — 버전 행이 실제로 안 들어갔으면
// 재열기가 성공해도 그건 "거절이 안 됐다"가 아니라 "시험이 아무것도 안 했다"이다.
func TestOpenRejectsFutureSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fd.db")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mustMigrate(t, path)
	s, err := OpenWithLogger(path, log)
	if err != nil {
		t.Fatalf("첫 Open 실패: %v", err)
	}
	future := SchemaVersion + 7
	if _, err := s.db.Exec(`INSERT INTO schema_version(version, applied_at) VALUES (?, ?)`,
		future, fmtTime(time.Now())); err != nil {
		t.Fatalf("미래 버전 주입 실패: %v", err)
	}
	// ── 대조 전제 확인 ──
	var maxV int
	if err := s.db.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&maxV); err != nil {
		t.Fatalf("주입 확인 실패: %v", err)
	}
	if maxV != future {
		t.Fatalf("전제가 성립하지 않았다: MAX(version)=%d, 기대 %d — 이 상태로는 아래 단정이 무의미하다", maxV, future)
	}
	s.Close()

	// ── 본 판정 ──
	s2, err := OpenWithLogger(path, log)
	if err == nil {
		s2.Close()
		t.Fatal("구 바이너리가 신 DB 를 열었다 — 조용히 망가지는 경로가 열려 있다")
	}
	// 소비자(운영자)가 보는 것은 이 문자열이다. 무엇을 해야 하는지가 담겨 있어야 한다.
	for _, want := range []string{"스키마", "바이너리를 올려라"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("거절 사유에 %q 가 없다: %v", want, err)
		}
	}
}

func TestOpenRejectsForeignDatabase(t *testing.T) {
	// schema_version 이 없는데 객체가 있는 DB. 스키마를 덮어 적용하면 남의 DB 를 망가뜨린다.
	path := filepath.Join(t.TempDir(), "other.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE unrelated(x TEXT)`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := raw.QueryRow(`SELECT count(*) FROM sqlite_master`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatalf("전제가 성립하지 않았다: 객체가 0개다")
	}
	raw.Close()

	s, err := OpenWithLogger(path, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil {
		s.Close()
		t.Fatal("남의 DB 위에 스키마를 얹었다")
	}
	if !strings.Contains(err.Error(), "schema_version 표가 없는데") {
		t.Errorf("거절 사유가 다르다: %v", err)
	}
}

// 앞선 적용이 schema_version 만 만들고 곧바로 끊긴 상태. 다시 적용하되 **먼저 백업한다.**
func TestMigrationBacksUpBeforeApplying(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fd.db")

	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE schema_version(version INTEGER NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	// ── 대조 전제 확인: 버전 행 0건, 객체는 그 표 하나뿐 ──
	var rows, objects int
	if err := raw.QueryRow(`SELECT count(*) FROM schema_version`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if err := raw.QueryRow(`SELECT count(*) FROM sqlite_master`).Scan(&objects); err != nil {
		t.Fatal(err)
	}
	if rows != 0 || objects != 1 {
		t.Fatalf("전제가 성립하지 않았다: schema_version 행 %d개, 객체 %d개 — 이 상태가 아니면 아래는 다른 경로를 본다", rows, objects)
	}
	raw.Close()

	mustMigrate(t, path)
	s, err := OpenWithLogger(path, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("재적용 Open 실패: %v", err)
	}
	defer s.Close()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var backups []string
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak-") {
			backups = append(backups, e.Name())
		}
	}
	if len(backups) != 1 {
		t.Fatalf("백업이 정확히 1개여야 하는데 %d개: %v", len(backups), backups)
	}
	// 백업은 **진짜 열리는 DB** 여야 한다 — 0바이트 파일을 만들어 놓고 초록이 나면 안 된다.
	bak, err := sql.Open("sqlite", "file:"+filepath.Join(dir, backups[0]))
	if err != nil {
		t.Fatal(err)
	}
	defer bak.Close()
	var n int
	if err := bak.QueryRow(`SELECT count(*) FROM schema_version`).Scan(&n); err != nil {
		t.Fatalf("백업이 열리지 않는다(빈 파일을 만들어 놓은 것과 구분돼야 한다): %v", err)
	}
	// 그리고 스키마는 적용됐다.
	seed(t, s, "p")
	if _, err := s.GetProject(context.Background(), "p"); err != nil {
		t.Errorf("재적용 후 스키마가 없다: %v", err)
	}
}

// ★ 객체가 있는데 버전 기록이 없는 DB 는 **다시 적용하지 않는다.**
// schema.sql 은 멱등이 아니다(IF NOT EXISTS 가 schema_version 하나뿐) —
// 조용히 시도하면 "table already exists" 로 죽으면서 DB 를 반쯤 만진 상태로 남긴다.
func TestMigrationRefusesToReapplyOverExistingObjects(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fd.db")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	mustMigrate(t, path)
	s, err := OpenWithLogger(path, log)
	if err != nil {
		t.Fatalf("첫 Open 실패: %v", err)
	}
	seed(t, s, "p")
	if _, err := s.db.Exec(`DELETE FROM schema_version`); err != nil {
		t.Fatal(err)
	}
	// ── 대조 전제 확인 ──
	var rows, objects int
	if err := s.db.QueryRow(`SELECT count(*) FROM schema_version`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master`).Scan(&objects); err != nil {
		t.Fatal(err)
	}
	if rows != 0 || objects <= 1 {
		t.Fatalf("전제가 성립하지 않았다: 버전 행 %d개, 객체 %d개", rows, objects)
	}
	s.Close()

	s2, err := OpenWithLogger(path, log)
	if err == nil {
		s2.Close()
		t.Fatal("멱등하지 않은 스키마를 기존 객체 위에 다시 적용했다")
	}
	for _, want := range []string{"멱등이 아니라", "백업"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("거절 사유에 %q 가 없다: %v", want, err)
		}
	}
	// 데이터는 그대로 남아 있어야 한다 — 거절이지 파괴가 아니다.
	chk, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer chk.Close()
	var pid string
	if err := chk.QueryRow(`SELECT id FROM project WHERE id = 'p'`).Scan(&pid); err != nil {
		t.Errorf("거절 과정에서 데이터가 사라졌다: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 세션 3중키
// ─────────────────────────────────────────────────────────────────────────────

// ★ 재개 경로. 같은 3중키면 **같은 세션**이어야 한다.
func TestOpenSessionTripleKey(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()

	first, created, err := s.OpenSession(ctx, "p", "m1", "/w/track2", "cc-A", "파이프라인", time.Time{})
	if err != nil {
		t.Fatalf("첫 등록 실패: %v", err)
	}
	if !created {
		t.Error("첫 등록인데 created=false")
	}

	again, created, err := s.OpenSession(ctx, "p", "m1", "/w/track2", "cc-A", "파이프라인", time.Time{})
	if err != nil {
		t.Fatalf("재등록 실패: %v", err)
	}
	if created {
		t.Error("같은 3중키인데 새로 만들었다 — 재개가 새 세션이 되면 앞 세션의 선점이 고아가 된다")
	}
	if again.ID != first.ID {
		t.Fatalf("같은 3중키인데 다른 세션이다: %q vs %q", again.ID, first.ID)
	}
	if again.OpenedAt != first.OpenedAt {
		t.Errorf("재개가 opened_at 을 되돌렸다: %v → %v", first.OpenedAt, again.OpenedAt)
	}

	// label 은 표시 전용이라 최신 선언이 이긴다. 그 외는 안 건드린다.
	relabeled, _, err := s.OpenSession(ctx, "p", "m1", "/w/track2", "cc-A", "파이프라인 완성", time.Time{})
	if err != nil {
		t.Fatalf("label 갱신 실패: %v", err)
	}
	if relabeled.ID != first.ID {
		t.Errorf("label 만 바뀌었는데 새 세션이 됐다")
	}
	if relabeled.Label != "파이프라인 완성" {
		t.Errorf("label 이 안 바뀌었다: %q", relabeled.Label)
	}
}

// 3중키의 각 축이 **하나만 달라도** 새 세션이어야 한다.
// 특히 worktree 재사용(지우고 다시 만든다)이 옛 세션 행과 합쳐지면 안 된다.
func TestOpenSessionEachAxisSeparates(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	if err := s.UpsertMachine(ctx, model.Machine{ID: "m2", Hostname: "laptop"}); err != nil {
		t.Fatal(err)
	}

	base, _, err := s.OpenSession(ctx, "p", "m1", "/w/track2", "cc-A", "", time.Time{})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name            string
		machine, wt, cc string
	}{
		{"cc_session_id 만 다름(같은 워크트리 재사용)", "m1", "/w/track2", "cc-B"},
		{"machine 만 다름", "m2", "/w/track2", "cc-A"},
		{"worktree 만 다름", "m1", "/w/track3", "cc-A"},
		// ★ 접두 일치가 없다 = 조상 트리의 등록을 물려받는 것이 원리적으로 불가능하다.
		{"하위 경로는 조상의 등록을 물려받지 않는다", "m1", "/w/track2/sub", "cc-A"},
		{"상위 경로도 마찬가지", "m1", "/w", "cc-A"},
	}
	seen := map[string]string{base.ID: "base"}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, created, err := s.OpenSession(ctx, "p", c.machine, c.wt, c.cc, "", time.Time{})
			if err != nil {
				t.Fatalf("등록 실패: %v", err)
			}
			if !created {
				t.Fatalf("새 세션이어야 하는데 기존 행(%q)을 돌려줬다", got.ID)
			}
			if prev, dup := seen[got.ID]; dup {
				t.Fatalf("id 가 %s 와 겹친다: %q", prev, got.ID)
			}
			seen[got.ID] = c.name
		})
	}
}

func TestSessionStateAndListLive(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	a := mustSession(t, s, "p", "cc-A")
	b := mustSession(t, s, "p", "cc-B")

	now := time.Now()
	if err := s.Beat(ctx, a.ID, model.SignalPrompt, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Touch(ctx, a.ID, "services/data-api/main.go", model.OriginObserved, now); err != nil {
		t.Fatal(err)
	}
	// b 는 신호도 발자국도 없다 — "발자국 없음"이 화면에 나와야 하는 세션이다.

	views, err := s.ListLive(ctx, "p", now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("ListLive 실패: %v", err)
	}
	if len(views) != 2 {
		t.Fatalf("세션 2개를 기대했는데 %d개", len(views))
	}
	byID := map[string]model.SessionView{}
	for _, v := range views {
		byID[v.Session.ID] = v
	}
	if !byID[a.ID].HasFootprint {
		t.Error("발자국을 남긴 세션이 HasFootprint=false")
	}
	if byID[b.ID].HasFootprint {
		t.Error("발자국이 없는 세션이 HasFootprint=true — 침묵과 겹침 없음이 뭉개진다")
	}
	// ★ 없는 신호는 **키가 없어야** 한다. 0값으로 채우면 "한 번도 안 옴"이 "1970년에 옴"이 된다.
	if _, ok := byID[a.ID].Signals[model.SignalCommit]; ok {
		t.Error("온 적 없는 commit 신호에 키가 있다")
	}
	if got := byID[a.ID].Signals[model.SignalPrompt]; got.IsZero() {
		t.Error("온 prompt 신호가 비어 있다")
	}

	// done 은 살아 있는 목록에서 빠진다.
	if err := s.SetSessionState(ctx, b.ID, model.SessionDone, ""); err != nil {
		t.Fatal(err)
	}
	views, err = s.ListLive(ctx, "p", now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].Session.ID != a.ID {
		t.Errorf("done 세션이 살아 있는 목록에 남았다: %+v", views)
	}

	// blocked 는 사유 없이 못 쓴다 — 저장 계층이 1차로 막는다.
	err = s.SetSessionState(ctx, a.ID, model.SessionBlocked, "")
	if err == nil {
		t.Error("사유 없는 blocked 가 통과했다")
	} else if !strings.Contains(err.Error(), "사유") {
		t.Errorf("거절 사유가 무엇이 빠졌는지 말하지 않는다: %v", err)
	}
}

func TestBeatAndTouchDoNotGoBackwards(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	a := mustSession(t, s, "p", "cc-A")

	late := time.Now()
	early := late.Add(-10 * time.Minute)

	if err := s.Beat(ctx, a.ID, model.SignalTool, late); err != nil {
		t.Fatal(err)
	}
	if err := s.Beat(ctx, a.ID, model.SignalTool, early); err != nil {
		t.Fatal(err)
	}
	sig, err := s.Signals(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 훅은 비동기라 순서가 뒤집혀 도착한다. 옛 비트가 최신을 덮으면
	// 살아 있는 세션이 남에게 낡은 것으로 보인다.
	if !sig[model.SignalTool].Equal(late.UTC().Truncate(time.Microsecond)) {
		t.Errorf("늦게 온 옛 신호가 최신 시각을 덮었다: %v (기대 %v)", sig[model.SignalTool], late.UTC())
	}

	if err := s.Touch(ctx, a.ID, "a.go", model.OriginObserved, late); err != nil {
		t.Fatal(err)
	}
	if err := s.Touch(ctx, a.ID, "a.go", model.OriginObserved, early); err != nil {
		t.Fatal(err)
	}
	fps, err := s.Footprints(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(fps) != 1 {
		t.Fatalf("발자국 1건을 기대했는데 %d건", len(fps))
	}
	if !fps[0].FirstAt.Equal(late.UTC().Truncate(time.Microsecond)) {
		t.Errorf("first_at 이 보존되지 않았다: %v", fps[0].FirstAt)
	}
	if fps[0].LastAt.Before(fps[0].FirstAt) {
		t.Errorf("last_at 이 first_at 보다 앞이다: %v < %v", fps[0].LastAt, fps[0].FirstAt)
	}

	// origin 은 뭉개지 않는다 — 같은 경로라도 origin 이 다르면 별개 행이다.
	if err := s.Touch(ctx, a.ID, "a.go", model.OriginDeclared, late); err != nil {
		t.Fatal(err)
	}
	fps, _ = s.Footprints(ctx, a.ID)
	if len(fps) != 2 {
		t.Errorf("origin 이 다른 발자국이 합쳐졌다: %d건", len(fps))
	}
	paths, err := s.FootprintPaths(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 {
		t.Errorf("경로 축에서는 origin 을 접어야 한다: %v", paths)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 선점 경쟁
// ─────────────────────────────────────────────────────────────────────────────

// ★ 같은 항목을 여러 세션이 동시에 잡으면 **하나만** 성공하고,
// 실패한 쪽은 **점유자 이름**을 받아야 한다. 불리언 실패로 접으면 다시 추측이 시작된다.
func TestClaimRaceOnlyOneWinsAndLosersLearnTheHolder(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	mustItem(t, s, "p", "t5-x")

	const n = 12
	sessions := make([]string, n)
	for i := range sessions {
		sessions[i] = mustSession(t, s, "p", fmt.Sprintf("cc-%02d", i)).ID
	}

	var wg sync.WaitGroup
	errs := make([]error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = s.ClaimItem(ctx, "p", "t5-x", sessions[i], time.Time{})
		}(i)
	}
	close(start)
	wg.Wait()

	var winners []string
	var losers []error
	for i, err := range errs {
		if err == nil {
			winners = append(winners, sessions[i])
			continue
		}
		losers = append(losers, err)
	}
	if len(winners) != 1 {
		t.Fatalf("성공이 정확히 1건이어야 하는데 %d건: %v", len(winners), winners)
	}
	holder := winners[0]

	// DB 도 같은 답을 해야 한다.
	held, err := s.GetClaim(ctx, "p", "t5-x")
	if err != nil {
		t.Fatal(err)
	}
	if held.SessionID != holder || held.ReleasedAt != nil {
		t.Fatalf("DB 의 점유자가 다르다: %+v (기대 %s)", held, holder)
	}

	if len(losers) != n-1 {
		t.Fatalf("실패가 %d건이어야 하는데 %d건", n-1, len(losers))
	}
	for _, err := range losers {
		var he *ClaimHeldError
		if !errors.As(err, &he) {
			t.Fatalf("실패가 *ClaimHeldError 가 아니다: %T %v", err, err)
		}
		if he.Holder != holder {
			t.Errorf("오류가 가리키는 점유자가 틀렸다: %q (실제 %q)", he.Holder, holder)
		}
		// 소비자(사람)가 보는 것은 이 문자열이다. 여기에 점유자가 없으면
		// 누구에게 물어야 하는지 알 수 없다.
		if !strings.Contains(err.Error(), holder) {
			t.Errorf("오류 문구에 점유자가 없다: %v", err)
		}
	}
}

func TestClaimResumeAndRelease(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	mustItem(t, s, "p", "t5-x")
	a := mustSession(t, s, "p", "cc-A")
	b := mustSession(t, s, "p", "cc-B")

	c1, err := s.ClaimItem(ctx, "p", "t5-x", a.ID, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	// 재개: 이미 자기 것이면 거절이 아니라 같은 선점이 돌아온다 —
	// 컨텍스트가 날아가 돌아온 세션이 여기서 막히면 맥락을 되찾을 길이 없다.
	c2, err := s.ClaimItem(ctx, "p", "t5-x", a.ID, time.Time{})
	if err != nil {
		t.Fatalf("자기 선점 재개가 거절됐다: %v", err)
	}
	if !c1.At.Equal(c2.At) {
		t.Errorf("재개가 선점 시각을 바꿨다: %v → %v", c1.At, c2.At)
	}

	// 남의 선점은 반납할 수 없다.
	err = s.ReleaseClaim(ctx, "p", "t5-x", b.ID)
	var he *ClaimHeldError
	if !errors.As(err, &he) {
		t.Fatalf("남의 선점 반납이 *ClaimHeldError 를 안 냈다: %T %v", err, err)
	}
	if !strings.Contains(err.Error(), "ForceReleaseClaim") {
		t.Errorf("탈출구를 안내하지 않는다: %v", err)
	}

	// 자기 것은 반납되고, 항목이 다시 열린다.
	if err := s.ReleaseClaim(ctx, "p", "t5-x", a.ID); err != nil {
		t.Fatal(err)
	}
	it, err := s.GetItem(ctx, "p", "t5-x")
	if err != nil {
		t.Fatal(err)
	}
	if it.State != model.ItemOpen {
		t.Errorf("반납 후 항목 상태 = %q, want open", it.State)
	}
	// 반납된 항목을 남이 다시 잡을 수 있어야 한다(선점 행이 PK 로 남아 있어도).
	if _, err := s.ClaimItem(ctx, "p", "t5-x", b.ID, time.Time{}); err != nil {
		t.Fatalf("반납된 항목을 다시 못 잡는다: %v", err)
	}
}

func TestForceReleaseClaimRequiresReason(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	mustItem(t, s, "p", "t5-x")
	a := mustSession(t, s, "p", "cc-A")
	if _, err := s.ClaimItem(ctx, "p", "t5-x", a.ID, time.Time{}); err != nil {
		t.Fatal(err)
	}

	if err := s.ForceReleaseClaim(ctx, "p", "t5-x", ""); err == nil {
		t.Fatal("사유 없는 강제 반납이 통과했다")
	}
	if err := s.ForceReleaseClaim(ctx, "p", "t5-x", "세션 머신이 재부팅됐다"); err != nil {
		t.Fatalf("사유 있는 강제 반납이 실패했다: %v", err)
	}
	c, err := s.GetClaim(ctx, "p", "t5-x")
	if err != nil {
		t.Fatal(err)
	}
	if c.ForceReason == "" || c.ReleasedAt == nil {
		t.Errorf("강제 반납 흔적이 안 남았다: %+v", c)
	}
}

func TestClaimRefusalsThatAreNotAboutAHolder(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	a := mustSession(t, s, "p", "cc-A")

	// 없는 항목 · 끝난 항목은 점유자 축이 아니다. 오류 타입을 가른다 — 처방이 다르다.
	_, err := s.ClaimItem(ctx, "p", "없는항목", a.ID, time.Time{})
	var re *ClaimRefusedError
	if !errors.As(err, &re) {
		t.Fatalf("없는 항목 선점이 *ClaimRefusedError 가 아니다: %T %v", err, err)
	}

	mustItem(t, s, "p", "t5-done")
	if err := s.FinishItem(ctx, "p", "t5-done", a.ID, model.ItemDone, ""); err != nil {
		t.Fatal(err)
	}
	_, err = s.ClaimItem(ctx, "p", "t5-done", a.ID, time.Time{})
	if !errors.As(err, &re) {
		t.Fatalf("끝난 항목 선점이 *ClaimRefusedError 가 아니다: %T %v", err, err)
	}
	if !strings.Contains(err.Error(), "이미 끝난") {
		t.Errorf("사유가 왜인지 말하지 않는다: %v", err)
	}
}

func TestFinishReleasesClaimAtomically(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	mustItem(t, s, "p", "t5-x")
	a := mustSession(t, s, "p", "cc-A")
	if _, err := s.ClaimItem(ctx, "p", "t5-x", a.ID, time.Time{}); err != nil {
		t.Fatal(err)
	}

	// dropped 는 사유 필수다. 사유 없이 부르면 **아무것도 안 바뀌어야** 한다 —
	// 절반만 적용되면 선점만 풀리고 항목은 열린 채 남는다.
	if err := s.FinishItem(ctx, "p", "t5-x", a.ID, model.ItemDropped, ""); err == nil {
		t.Fatal("사유 없는 폐기가 통과했다")
	}
	c, err := s.GetClaim(ctx, "p", "t5-x")
	if err != nil {
		t.Fatal(err)
	}
	if c.ReleasedAt != nil {
		t.Error("거절된 종료가 선점을 풀었다 — 원자성이 깨졌다")
	}

	if err := s.FinishItem(ctx, "p", "t5-x", a.ID, model.ItemDone, ""); err != nil {
		t.Fatal(err)
	}
	c, _ = s.GetClaim(ctx, "p", "t5-x")
	if c.ReleasedAt == nil {
		t.Error("종료했는데 선점이 안 풀렸다")
	}
	it, _ := s.GetItem(ctx, "p", "t5-x")
	if it.State != model.ItemDone || it.ClosedAt == nil {
		t.Errorf("종료 상태가 안 찍혔다: %+v", it)
	}
	claimed, err := s.ClaimedItems(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 0 {
		t.Errorf("끝난 항목이 선점 목록에 남았다: %v", claimed)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 항목 · 의존 종속 수
// ─────────────────────────────────────────────────────────────────────────────

func TestItemAfterAndDependents(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	mustItem(t, s, "p", "base")

	err := s.AddItem(ctx, model.Item{
		Project: "p", ID: "dep1", Title: "t", Body: "b",
		After: []model.After{{Item: "base"}, {SHA: "c8206a9"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddItem(ctx, model.Item{
		Project: "p", ID: "dep2", Title: "t", Body: "b",
		After: []model.After{{Item: "base"}},
	}); err != nil {
		t.Fatal(err)
	}

	n, err := s.Dependents(ctx, "p", "base")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("base 에 기대는 항목 수 = %d, want 2", n)
	}
	// sha 의존은 종속 수에 안 들어간다(항목이 아니다).
	if n, _ := s.Dependents(ctx, "p", "c8206a9"); n != 0 {
		t.Errorf("sha 의존이 종속 수에 들어갔다: %d", n)
	}
	// 없는 항목의 종속 수는 0이다(오류가 아니다).
	if n, err := s.Dependents(ctx, "p", "없음"); err != nil || n != 0 {
		t.Errorf("없는 항목의 종속 수 = %d, err=%v", n, err)
	}

	got, err := s.GetItem(ctx, "p", "dep1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.After) != 2 {
		t.Fatalf("선행 조건 2건을 기대했는데 %d건: %+v", len(got.After), got.After)
	}

	// 삭제하면 종속 수가 되돌아온다 — item_after 가 CASCADE 로 사라지므로 파생이 저절로 맞는다.
	if err := s.DeleteItem(ctx, "p", "dep1"); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.Dependents(ctx, "p", "base"); n != 1 {
		t.Errorf("삭제 후 종속 수 = %d, want 1", n)
	}
	if _, err := s.GetItem(ctx, "p", "dep1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("삭제된 항목이 조회된다: %v", err)
	}
}

// 선행 조건은 정확히 한 축만 채워야 한다. **브랜치 이름을 담을 컬럼이 없다** —
// 랜딩이 끝나면 브랜치가 지워져 조건이 충족되는 바로 그 순간 해석 불가가 되기 때문이다.
func TestItemAfterRejectsAmbiguousDependency(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()

	err := s.AddItem(ctx, model.Item{
		Project: "p", ID: "bad", Title: "t", Body: "b",
		After: []model.After{{Item: "x", SHA: "abc"}},
	})
	if err == nil {
		t.Fatal("축 두 개를 채운 선행 조건이 통과했다")
	}
	// 롤백됐어야 한다 — 항목만 남고 의존이 없으면 조용히 잘못된 큐가 된다.
	if _, err := s.GetItem(ctx, "p", "bad"); !errors.Is(err, ErrNotFound) {
		t.Errorf("실패한 AddItem 이 항목을 남겼다: %v", err)
	}
}

func TestListOpenAndSetState(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	mustItem(t, s, "p", "a")
	mustItem(t, s, "p", "b")
	mustItem(t, s, "p", "c")

	if err := s.SetItemState(ctx, "p", "b", model.ItemDropped, "계약 개정으로 무의미"); err != nil {
		t.Fatal(err)
	}
	open, err := s.ListOpen(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 2 {
		t.Fatalf("열린 항목 2건을 기대했는데 %d건", len(open))
	}
	for _, it := range open {
		if it.ID == "b" {
			t.Error("폐기된 항목이 열린 목록에 있다")
		}
		if len(it.Paths) != 1 || it.Paths[0] != "services/" {
			t.Errorf("paths 가 왕복하지 않았다: %v", it.Paths)
		}
	}
	// 사유 없는 폐기는 거절이다(스키마 CHECK 전에 여기가 1차 방어).
	if err := s.SetItemState(ctx, "p", "c", model.ItemDropped, ""); err == nil {
		t.Error("사유 없는 폐기가 통과했다")
	}
}

func TestSetLandedRefRefusesEmpty(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	mustItem(t, s, "p", "a")

	// 빈 sha 를 받아 "지금 HEAD"로 채우는 경로가 있으면 남의 커밋이 박힌다(3회 관측).
	// 이 함수는 sha 를 인자로만 받고 빈 값을 거절한다.
	err := s.Tx(ctx, func(tx *Tx) error { return tx.SetLandedRef("p", "a", "") })
	if err == nil {
		t.Fatal("빈 랜딩 sha 가 통과했다")
	}
	if err := s.Tx(ctx, func(tx *Tx) error { return tx.SetLandedRef("p", "a", "9d2ada8") }); err != nil {
		t.Fatal(err)
	}
	it, _ := s.GetItem(ctx, "p", "a")
	if it.LandedRef != "9d2ada8" {
		t.Errorf("landed_ref = %q", it.LandedRef)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 발번기
// ─────────────────────────────────────────────────────────────────────────────

// ★ 이 시험이 이 제품의 핵심 주장 하나를 지킨다.
// 락은 파일 접근을 직렬화할 뿐이라 논리 발번을 지키지 못한다 —
// 두 세션이 각자 "지금 값"을 읽고 각자 +1 하면 둘 다 같은 번호를 쓴다.
// 실제로 같은 날 두 세션이 같은 개정 차수를 써서 뒤가 물렀다.
func TestCounterNextIsAtomicUnderConcurrency(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()

	const n = 64
	got := make([]int64, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			got[i], errs[i] = s.NextCounter(ctx, "p", "contract_revision")
		}(i)
	}
	close(start)
	wg.Wait()

	// ── 대조 전제 확인: 하나도 실패하지 않았어야 한다.
	//    실패를 그냥 걸러 내면 "겹침 0건"이 "성공이 1건뿐이라 겹칠 수가 없었다"로 접힌다.
	for i, err := range errs {
		if err != nil {
			t.Fatalf("%d번째 발번이 실패했다: %v", i, err)
		}
	}

	seen := map[int64]int{}
	for i, v := range got {
		if prev, dup := seen[v]; dup {
			t.Errorf("번호 %d 가 %d번째와 %d번째에 겹쳤다", v, prev, i)
		}
		seen[v] = i
	}
	if len(seen) != n {
		t.Fatalf("서로 다른 번호가 %d개여야 하는데 %d개", n, len(seen))
	}
	// 1..n 이 빠짐없이 나와야 한다. 겹침만 보면 "1,2,4,5…"처럼 건너뛴 것을 못 본다.
	for want := int64(1); want <= n; want++ {
		if _, ok := seen[want]; !ok {
			t.Errorf("번호 %d 가 발급되지 않았다", want)
		}
	}
	// 표 밖: 다른 이름의 카운터는 서로를 안 건드린다.
	other, err := s.NextCounter(ctx, "p", "other")
	if err != nil {
		t.Fatal(err)
	}
	if other != 1 {
		t.Errorf("다른 이름의 첫 발번 = %d, want 1", other)
	}
	if v, _ := s.PeekCounter(ctx, "p", "contract_revision"); v != n {
		t.Errorf("PeekCounter = %d, want %d", v, n)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 자원 배타
// ─────────────────────────────────────────────────────────────────────────────

// ★ 배타는 애플리케이션 판정이 아니라 **부분 유니크 인덱스**가 지킨다.
// 그래서 "먼저 조회해 없으면 잡는다" 사이의 창이 존재하지 않는다.
func TestAcquireResourceRaceOnlyOneWins(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()

	const n = 10
	sessions := make([]string, n)
	for i := range sessions {
		sessions[i] = mustSession(t, s, "p", fmt.Sprintf("cc-%02d", i)).ID
	}

	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = s.AcquireResource(ctx, "p", "staging", Holder{SessionID: sessions[i]}, time.Time{})
		}(i)
	}
	close(start)
	wg.Wait()

	var winner string
	var losers []error
	for i, err := range errs {
		if err == nil {
			if winner != "" {
				t.Fatalf("성공이 둘이다: %s, %s", winner, sessions[i])
			}
			winner = sessions[i]
			continue
		}
		losers = append(losers, err)
	}
	if winner == "" {
		t.Fatalf("아무도 못 잡았다: %v", errs)
	}
	if len(losers) != n-1 {
		t.Fatalf("실패가 %d건이어야 하는데 %d건", n-1, len(losers))
	}
	for _, err := range losers {
		var he *ResourceHeldError
		if !errors.As(err, &he) {
			t.Fatalf("실패가 *ResourceHeldError 가 아니다: %T %v", err, err)
		}
		if he.Holder.SessionID != winner {
			t.Errorf("오류가 가리키는 점유자가 틀렸다: %q (실제 %q)", he.Holder.SessionID, winner)
		}
		// 소비자가 보는 문자열에 점유자·경과·해제법이 다 있어야 한다.
		msg := err.Error()
		for _, want := range []string{winner, "경과", "ForceReleaseResource"} {
			if !strings.Contains(msg, want) {
				t.Errorf("오류 문구에 %q 가 없다: %s", want, msg)
			}
		}
	}

	held, err := s.ListHeld(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if len(held) != 1 || held[0].SessionID != winner {
		t.Errorf("점유 목록이 틀렸다: %+v", held)
	}
}

// TestListHeldOrdersByAcquisitionThenResource — 점유 목록의 정렬을 잠근다.
//
// ★ ListHeld 의 godoc 이 "획득이 오래된 순, 같은 시각이면 자원 이름순"이라고 말하는데
// 그 산문을 잠그는 시험이 레포에 하나도 없었다 — ORDER BY 를 DESC 로 뒤집어도
// store·service·mcpsrv·web·api 다섯 패키지가 전부 초록이었다. 코드로 이 순서에
// **분기하는** 호출자가 없기 때문이다(pick 은 자원→점유자 맵으로 접고, finish 는 자기
// 세션 것만 걸러 반납한다). 그런데 이 슬라이스는 재정렬 없이 사람에게 나간다(보드의 막힘
// 절 · MCP 꼬리의 자원 점유 줄). 즉 순서를 바꾸면 시험이 아니라 **화면이** 조용히 바뀐다.
// 산문에 정렬을 적는 순간 그것을 잠그는 시험이 있어야 한다는 규율(ListSessionEvents 의
// "오래된 순"을 event_session_test.go 가 잠그는 자리)을 여기서 갚는다.
//
// acquired_at 은 raw INSERT 로 직접 넣는다 — AcquireResource 를 연달아 부르면 시각이
// 마이크로초 단위로 갈려 **동률 갈래를 원리적으로 못 만든다**. 넣는 순서(rowid)는 시간순과도
// 이름순과도 다르게 뒀다.
//
// ★ 이 시험이 **무엇을 못 잠그는지**까지 적는다(그것을 안 적는 것이 이 브랜치가 고치는
// 결함이다). 넣어 본 변이 넷의 실측:
//   - `acquired_at DESC, resource DESC`  → 빨강(여섯 줄이 통째로 뒤집힌다)
//   - `acquired_at, resource DESC`       → 빨강(tie-a·tie-z 만 뒤집힌다 = 둘째 키의 **방향**은 잠긴다)
//   - `ORDER BY` 통째 삭제                → 빨강(시간축이 사라지고 이름순만 남는다)
//   - `, resource` 만 삭제               → **초록. 못 잡는다.**
//
// 마지막 갈래의 이유는 질의 계획이다: `WHERE project = ? AND released_at IS NULL` 이
// 부분 유니크 인덱스 resource_one_holder(project, resource) 를 타서 행이 이미 이름순으로
// 나오고, ORDER BY 의 임시 B-tree 정렬이 그 순서를 동률에서 그대로 보존한다
// (EXPLAIN QUERY PLAN: SEARCH … USING INDEX resource_one_holder / USE TEMP B-TREE FOR ORDER BY).
// 즉 둘째 키는 지금 **인덱스와 겹쳐서 없어도 티가 안 난다** — 그래도 질의에 남겨 두는 편이
// 옳다. 인덱스가 바뀌면 그때부터 값이 달라지는데, 이 시험은 그 변화를 못 알린다.
func TestListHeldOrdersByAcquisitionThenResource(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	a := mustSession(t, s, "p", "cc-A")

	// 전부 과거로 둔다 — 아래에서 **진짜 경로**로 하나 더 잡아 맨 뒤에 오는지 볼 것이다.
	base := nowStamp().Add(-time.Hour)
	fixture := []struct {
		resource string
		at       time.Time
	}{
		{"prod", base.Add(2 * time.Minute)},
		{"staging", base},
		{"docs", base.Add(time.Minute)},
		// 동률 둘 — 이름 **역순**으로 넣는다. 넣은 순서와 이름순을 갈라 둬야 둘째 키가
		// 하는 일이 보인다. 다만 이 두 줄이 실제로 잠그는 것은 그 키의 **방향**이지
		// 존재가 아니다 — 위 실측 표 참고.
		{"tie-z", base.Add(3 * time.Minute)},
		{"tie-a", base.Add(3 * time.Minute)},
	}
	for _, f := range fixture {
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO resource_hold(project, resource, session_id, job_id, acquired_at, released_at)
			VALUES (?, ?, ?, NULL, ?, NULL)`, "p", f.resource, a.ID, fmtTime(f.at)); err != nil {
			t.Fatalf("점유 삽입 실패(%s): %v", f.resource, err)
		}
	}
	// 대조 전제 — raw 로 넣은 시각이 진짜 경로가 만드는 시각과 같은 축 위에 있는지.
	// 없으면 위 다섯 줄이 정렬 규칙이 아니라 **시험 전용 표기**만 잠글 수 있다.
	if _, err := s.AcquireResource(ctx, "p", "방금-잡은-것", Holder{SessionID: a.ID}, time.Time{}); err != nil {
		t.Fatalf("전제가 깨졌다 — 진짜 경로의 획득이 실패했다: %v", err)
	}

	want := []string{"staging", "docs", "prod", "tie-a", "tie-z", "방금-잡은-것"}
	names := func(hs []model.ResourceHold) []string {
		out := make([]string, 0, len(hs))
		for _, h := range hs {
			out = append(out, h.Resource)
		}
		return out
	}

	held, err := s.ListHeld(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if got := names(held); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("정렬이 다르다(획득이 오래된 순, 같은 시각이면 자원 이름순):\n got  %v\n want %v", got, want)
	}

	// Tx 짝도 같은 순서여야 한다 — godoc 이 "질의를 공유하므로 갈릴 자리가 없다"고
	// 말하므로, 그 문장도 여기서 함께 잠근다.
	var inTx []string
	if err := s.Tx(ctx, func(t *Tx) error {
		h, err := t.ListHeld("p")
		inTx = names(h)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(inTx, ",") != strings.Join(want, ",") {
		t.Errorf("Tx 짝의 정렬이 Store 짝과 다르다:\n got  %v\n want %v", inTx, want)
	}
}

func TestReleaseResourceOnlyByHolder(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	a := mustSession(t, s, "p", "cc-A")
	b := mustSession(t, s, "p", "cc-B")

	if _, err := s.AcquireResource(ctx, "p", "staging", Holder{SessionID: a.ID}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseResource(ctx, "p", "staging", Holder{SessionID: b.ID}); err == nil {
		t.Fatal("남의 자원을 반납했다 — 배타가 거짓이 된다")
	}
	if err := s.ReleaseResource(ctx, "p", "staging", Holder{SessionID: a.ID}); err != nil {
		t.Fatalf("자기 자원 반납 실패: %v", err)
	}
	// 반납했으면 남이 잡을 수 있어야 한다(부분 유니크 인덱스가 released 행을 안 막는다).
	if _, err := s.AcquireResource(ctx, "p", "staging", Holder{SessionID: b.ID}, time.Time{}); err != nil {
		t.Fatalf("반납된 자원을 다시 못 잡는다: %v", err)
	}
	// 강제 반납은 사유 필수.
	if err := s.ForceReleaseResource(ctx, "p", "staging", ""); err == nil {
		t.Error("사유 없는 강제 반납이 통과했다")
	}
	if err := s.ForceReleaseResource(ctx, "p", "staging", "세션이 죽은 것을 다섯 축으로 확인함"); err != nil {
		t.Fatalf("강제 반납 실패: %v", err)
	}
	if held, _ := s.ListHeld(ctx, "p"); len(held) != 0 {
		t.Errorf("강제 반납 후에도 점유가 남았다: %+v", held)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 판단 — 추가 전용
// ─────────────────────────────────────────────────────────────────────────────

// ★ 이 시험은 **행이 존재하는 상태에서** 해야 한다.
// 표가 비어 있으면 BEFORE UPDATE 트리거가 아예 안 걸려 UPDATE 가 조용히 성공하고,
// 그러면 "고칠 수 없다"가 아니라 "고칠 것이 없었다"가 초록으로 나온다.
// 이 레포에서 실제로 난 실패다. 그래서 **행 개수를 먼저 단정한다.**
func TestJudgmentIsAppendOnly(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	a := mustSession(t, s, "p", "cc-A")

	original := "일부러 안 고친 자리: lifecycle.deletions — 문장이 같다고 함께 고치면 새 거짓이 생긴다"
	j, err := s.AddJudgment(ctx, model.Judgment{
		Project: "p", SessionID: a.ID, Kind: model.JudgmentHandoff,
		Title: "batch7 랜딩", Body: original,
		Links: []model.JudgmentLink{{TargetKind: "session", TargetID: a.ID}},
	})
	if err != nil {
		t.Fatalf("판단 저장 실패: %v", err)
	}

	// ── 대조 전제 확인: 트리거가 물 행이 실제로 있는가 ──
	var rows int
	if err := s.db.QueryRow(`SELECT count(*) FROM judgment`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("전제가 성립하지 않았다: judgment 행이 %d건이다 "+
			"— 0건이면 BEFORE UPDATE 트리거가 아예 안 걸려 아래 단정이 거짓 초록이 된다", rows)
	}

	// ── 본 판정 ──
	if _, err := s.db.Exec(`UPDATE judgment SET body = '덮어씀' WHERE id = ?`, j.ID); err == nil {
		t.Error("판단이 UPDATE 됐다 — 남의 절을 덮어써 원문이 영구 소실된 사고가 다시 가능해진다")
	} else if !strings.Contains(err.Error(), "추가 전용") {
		t.Errorf("거절 문구가 왜인지 말하지 않는다: %v", err)
	}
	if _, err := s.db.Exec(`DELETE FROM judgment WHERE id = ?`, j.ID); err == nil {
		t.Error("판단이 DELETE 됐다")
	}

	// 원문이 그대로여야 한다. "거절됐다"만 보면 부분 적용을 못 본다.
	got, err := s.GetJudgment(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != original {
		t.Errorf("본문이 바뀌었다: %q", got.Body)
	}
	if len(got.Links) != 1 || got.Links[0].TargetID != a.ID {
		t.Errorf("링크가 왕복하지 않았다: %+v", got.Links)
	}

	// 정정은 새 행 + supersedes 다.
	fix, err := s.AddJudgment(ctx, model.Judgment{
		Project: "p", SessionID: a.ID, Kind: model.JudgmentHandoff,
		Body: "정정: 앞 판단의 근거가 거짓이었다", Supersedes: j.ID,
	})
	if err != nil {
		t.Fatalf("정정 판단 저장 실패: %v", err)
	}
	if fix.Supersedes != j.ID {
		t.Errorf("supersedes 가 안 걸렸다: %q", fix.Supersedes)
	}
	// 두 판단이 시간순으로 나와야 한다.
	list, err := s.ListJudgmentsBySession(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != j.ID || list[1].ID != fix.ID {
		t.Errorf("세션 판단 목록이 틀렸다: %+v", list)
	}
}

// 위 시험이 요구하는 "행 개수 먼저 단정"이 왜 필요한지를 실행으로 못박는다.
// **빈 표에서는 같은 UPDATE 가 성공한다** — 이것이 거짓 초록의 정확한 모양이다.
func TestEmptyJudgmentTableWouldGiveAFalseGreen(t *testing.T) {
	s := newStore(t)
	var rows int
	if err := s.db.QueryRow(`SELECT count(*) FROM judgment`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("이 시험은 빈 표를 전제한다(행 %d건)", rows)
	}
	res, err := s.db.Exec(`UPDATE judgment SET body = '덮어씀'`)
	if err != nil {
		t.Fatalf("빈 표의 UPDATE 가 오류를 냈다 — 이 시험의 전제가 바뀌었다: %v", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("빈 표인데 %d행이 바뀌었다", n)
	}
	// 여기까지 오류 없이 왔다는 것이 곧 "행이 없으면 트리거가 안 걸린다"의 증거다.
	// 그래서 TestJudgmentIsAppendOnly 는 행 개수를 먼저 단정한다.
}

func TestJudgmentBodyMustNotBeBlank(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	// 스키마 CHECK(body <> '')는 공백만 든 본문을 통과시킨다. 1차 방어가 여기 있어야 한다.
	for _, body := range []string{"", "   ", "\n\t"} {
		if _, err := s.AddJudgment(ctx, model.Judgment{
			Project: "p", Kind: model.JudgmentNow, Body: body,
		}); err == nil {
			t.Errorf("빈 본문(%q)이 통과했다", body)
		}
	}
}

func TestSearchJudgments(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	a := mustSession(t, s, "p", "cc-A")

	add := func(title, body string) {
		t.Helper()
		if _, err := s.AddJudgment(ctx, model.Judgment{
			Project: "p", SessionID: a.ID, Kind: model.JudgmentHandoff, Title: title, Body: body,
		}); err != nil {
			t.Fatal(err)
		}
	}
	add("batch7 랜딩", "컨슈머 수렴 대기에서 DLQ 10건이 났다")
	add("계약 개정", "raw-envelope 에 sync_mode 를 넣었다")
	add("기각한 후보", "structlog 도입은 의존성 최소주의에 어긋나 기각")

	// ── 대조 전제: FTS 인덱스에 실제로 3건이 들어갔는가.
	//    external content FTS 는 트리거가 안 걸리면 조용히 0건이 되고,
	//    그러면 "검색이 안 된다"와 "결과가 없다"가 구분되지 않는다.
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM judgment_fts`).Scan(&n); err != nil {
		t.Fatalf("FTS 표 조회 실패: %v", err)
	}
	if n != 3 {
		t.Fatalf("전제가 성립하지 않았다: judgment_fts 에 %d건(기대 3건) — 삽입 트리거가 안 걸렸다", n)
	}

	hits, err := s.SearchJudgments(ctx, "p", "DLQ", 10)
	if err != nil {
		t.Fatalf("검색 실패: %v", err)
	}
	if len(hits) != 1 || !strings.Contains(hits[0].Body, "DLQ") {
		t.Fatalf("DLQ 검색 결과가 틀렸다: %+v", hits)
	}

	// 제목도 검색된다.
	if hits, err = s.SearchJudgments(ctx, "p", "batch7", 10); err != nil || len(hits) != 1 {
		t.Errorf("제목 검색 결과가 틀렸다: %d건, err=%v", len(hits), err)
	}

	// 표 밖: FTS5 문법 문자가 든 검색어도 **죽지 않아야** 한다.
	// 그대로 넘기면 구문 오류로 죽고, 그러면 결과 없음과 구분되지 않는다.
	for _, q := range []string{"sync_mode", `"unbalanced`, "a AND", "-not", "*", "(", "OR OR"} {
		if _, err := s.SearchJudgments(ctx, "p", q, 10); err != nil {
			t.Errorf("검색어 %q 가 오류를 냈다: %v", q, err)
		}
	}
	// 빈 검색어는 빈 결과가 아니라 오류다 — 둘을 뭉개면 "왜 안 나오나"에 답이 없다.
	if _, err := s.SearchJudgments(ctx, "p", "   ", 10); err == nil {
		t.Error("빈 검색어가 통과했다")
	}

	// 프로젝트 축으로 갈린다.
	if err := s.UpsertProject(ctx, model.Project{ID: "q", Path: "/repo/q"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddJudgment(ctx, model.Judgment{
		Project: "q", Kind: model.JudgmentNow, Body: "DLQ 는 여기도 있다",
	}); err != nil {
		t.Fatal(err)
	}
	hits, _ = s.SearchJudgments(ctx, "p", "DLQ", 10)
	if len(hits) != 1 {
		t.Errorf("프로젝트 필터가 안 먹었다: %d건", len(hits))
	}
	hits, _ = s.SearchJudgments(ctx, "", "DLQ", 10)
	if len(hits) != 2 {
		t.Errorf("전 프로젝트 검색이 %d건(기대 2건)", len(hits))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 스냅숏
// ─────────────────────────────────────────────────────────────────────────────

// ★ method=manual 인데 근거가 없으면 **호출 전에** 거절한다.
// 스키마 CHECK 는 최후 방어다 — 여기서 막아야 호출부가 사용자에게 옮길 말이 생긴다.
func TestSnapshotManualRequiresEvidence(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()

	err := s.PutSnapshot(ctx, model.Snapshot{
		Project: "p", Key: "part3.pct", Value: "62", Method: model.SnapshotManual,
	})
	if err == nil {
		t.Fatal("근거 없는 manual 스냅숏이 통과했다")
	}
	if !strings.Contains(err.Error(), "근거") {
		t.Errorf("거절 사유가 무엇이 빠졌는지 말하지 않는다: %v", err)
	}
	// 아무것도 안 들어갔어야 한다.
	if _, err := s.GetSnapshot(ctx, "p", "part3.pct"); !errors.Is(err, ErrNotFound) {
		t.Errorf("거절됐는데 행이 생겼다: %v", err)
	}

	// 표 밖: 공백만 든 근거. 스키마 CHECK(evidence <> '')는 이걸 통과시킨다.
	err = s.PutSnapshot(ctx, model.Snapshot{
		Project: "p", Key: "part3.pct", Value: "62", Method: model.SnapshotManual, Evidence: "  \n ",
	})
	if err == nil {
		t.Error("공백만 든 근거가 통과했다 — 스키마 CHECK 만 믿으면 여기가 샌다")
	}

	// 근거가 있으면 저장된다.
	if err := s.PutSnapshot(ctx, model.Snapshot{
		Project: "p", Key: "part3.pct", Value: "62", Method: model.SnapshotManual,
		Evidence: "12파트 전수 판정(2026-08-03, 병렬 조사 + 적대 감사)", InputDigest: "sha256:abc",
	}); err != nil {
		t.Fatalf("근거 있는 manual 스냅숏이 거절됐다: %v", err)
	}
	got, err := s.GetSnapshot(ctx, "p", "part3.pct")
	if err != nil {
		t.Fatal(err)
	}
	if got.Value != "62" || got.Evidence == "" || got.InputDigest != "sha256:abc" {
		t.Errorf("스냅숏이 왕복하지 않았다: %+v", got)
	}

	// command 는 근거 없이도 된다(재계산 가능하므로).
	if err := s.PutSnapshot(ctx, model.Snapshot{
		Project: "p", Key: "loc", Value: "12034", Method: model.SnapshotCommand,
	}); err != nil {
		t.Errorf("command 스냅숏이 거절됐다: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 이벤트 원장
// ─────────────────────────────────────────────────────────────────────────────

func TestEventLedgerIsAppendOnly(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	a := mustSession(t, s, "p", "cc-A")

	if err := s.TryLogEvent(ctx, "resource.acquire", "p", a.ID,
		map[string]any{"resource": "staging"}); err != nil {
		t.Fatalf("이벤트 기록 실패: %v", err)
	}
	var rows int
	if err := s.db.QueryRow(`SELECT count(*) FROM event`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("전제가 성립하지 않았다: event 행이 %d건 — 0건이면 트리거가 안 걸려 거짓 초록이 난다", rows)
	}
	if _, err := s.db.Exec(`UPDATE event SET kind = 'x'`); err == nil {
		t.Error("이벤트가 UPDATE 됐다")
	}
	if _, err := s.db.Exec(`DELETE FROM event`); err == nil {
		t.Error("이벤트가 DELETE 됐다")
	}

	evs, err := s.ListEvents(ctx, "resource.acquire", time.Now().Add(-time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || !strings.Contains(evs[0].Payload, "staging") {
		t.Errorf("이벤트 조회가 틀렸다: %+v", evs)
	}
	if n, _ := s.CountEvents(ctx, "", time.Now().Add(-time.Hour)); n != 1 {
		t.Errorf("CountEvents = %d, want 1", n)
	}
}

// 계측이 기능을 죽이면 안 된다. 다만 삼키지도 않는다 — WARN 이 나가야 한다.
func TestLogEventNeverPanicsAndWarnsOnFailure(t *testing.T) {
	var buf strings.Builder
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	dbp := filepath.Join(t.TempDir(), "fd.db")
	mustMigrate(t, dbp)
	mustMigrate(t, dbp)
	s, err := OpenWithLogger(dbp, log)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 직렬화 불가능한 payload → 기록 실패. 상위는 계속 돌아야 한다.
	s.LogEvent(context.Background(), "x", "", "", make(chan int))

	out := buf.String()
	if !strings.Contains(out, "계측 이벤트 기록 실패") {
		t.Errorf("실패를 삼켰다 — WARN 이 안 나갔다: %q", out)
	}
	// 소비자가 로그 한 줄만 보고 원인에 도달할 수 있어야 한다.
	if !strings.Contains(out, "error=") {
		t.Errorf("로그 줄에 원인 전문이 없다: %q", out)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 트랜잭션
// ─────────────────────────────────────────────────────────────────────────────

func TestTxRollsBackOnError(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()

	sentinel := errors.New("일부러 낸 오류")
	err := s.Tx(ctx, func(tx *Tx) error {
		if err := tx.AddItem(model.Item{Project: "p", ID: "rollback-me", Title: "t", Body: "b"}); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("fn 의 오류가 그대로 안 올라왔다: %v", err)
	}
	if _, err := s.GetItem(ctx, "p", "rollback-me"); !errors.Is(err, ErrNotFound) {
		t.Errorf("롤백됐어야 하는데 항목이 남았다: %v", err)
	}
}

// 여러 쓰기가 한 트랜잭션에서 원자적으로 묶인다 —
// finish 가 "판단 저장 + 후속 등록 + 종료"를 한 호출로 하는 근거가 이것이다.
func TestTxGroupsWritesAtomically(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	mustItem(t, s, "p", "t5-x")
	a := mustSession(t, s, "p", "cc-A")
	if _, err := s.ClaimItem(ctx, "p", "t5-x", a.ID, time.Time{}); err != nil {
		t.Fatal(err)
	}

	err := s.Tx(ctx, func(tx *Tx) error {
		if _, err := tx.AddJudgment(model.Judgment{
			Project: "p", SessionID: a.ID, Kind: model.JudgmentHandoff, Body: "랜딩했다",
		}); err != nil {
			return err
		}
		if err := tx.AddItem(model.Item{Project: "p", ID: "followup", Title: "후속", Body: "b"}); err != nil {
			return err
		}
		// 마지막 단계가 실패한다(사유 없는 폐기).
		return tx.FinishItem("p", "t5-x", a.ID, model.ItemDropped, "")
	})
	if err == nil {
		t.Fatal("사유 없는 폐기가 통과했다")
	}
	// 앞 두 쓰기도 함께 사라져야 한다.
	if _, err := s.GetItem(ctx, "p", "followup"); !errors.Is(err, ErrNotFound) {
		t.Errorf("후속 항목이 남았다: %v", err)
	}
	js, _ := s.ListJudgmentsBySession(ctx, a.ID)
	if len(js) != 0 {
		t.Errorf("판단이 남았다: %+v", js)
	}
	c, _ := s.GetClaim(ctx, "p", "t5-x")
	if c.ReleasedAt != nil {
		t.Error("선점이 풀렸다 — 원자성이 깨졌다")
	}
}

func TestRefStateAndChangeSetRoundTrip(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	now := time.Now()

	if err := s.UpsertRefState(ctx, model.RefState{
		Project: "p", Ref: "refs/heads/main", SHA: "c8206a9", Subject: "Prove the parts scan", At: now,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetRefState(ctx, "p", "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	if got.SHA != "c8206a9" || got.Subject == "" {
		t.Errorf("ref 상태가 왕복하지 않았다: %+v", got)
	}

	if err := s.UpsertChangeSet(ctx, model.ChangeSet{
		Project: "p", BaseSHA: "aaa", HeadSHA: "bbb",
		Paths: []string{"services/data-api/", "Makefile"}, ComputedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	cs, err := s.GetChangeSet(ctx, "p", "aaa", "bbb")
	if err != nil {
		t.Fatal(err)
	}
	if len(cs.Paths) != 2 || cs.Paths[1] != "Makefile" {
		t.Errorf("변경집합이 왕복하지 않았다: %+v", cs)
	}
	// 빈 경로 목록도 왕복해야 한다(nil 과 [] 를 뭉개면 JSON 이 null 이 되어 해석이 깨진다).
	if err := s.UpsertChangeSet(ctx, model.ChangeSet{
		Project: "p", BaseSHA: "ccc", HeadSHA: "ddd", ComputedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if cs, err = s.GetChangeSet(ctx, "p", "ccc", "ddd"); err != nil {
		t.Fatalf("빈 변경집합 조회 실패: %v", err)
	}
	if len(cs.Paths) != 0 {
		t.Errorf("빈 경로 목록이 %v 로 왔다", cs.Paths)
	}
}

func TestWorkspacesAndPickEval(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	a := mustSession(t, s, "p", "cc-A")

	if err := s.AddWorkspace(ctx, model.Workspace{
		SessionID: a.ID, Project: "p", Path: "/w/cc-A", IsPrimary: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddWorkspace(ctx, model.Workspace{
		SessionID: a.ID, Project: "p", Path: "/docs/wt", IsPrimary: false,
	}); err != nil {
		t.Fatal(err)
	}
	ws, err := s.ListWorkspaces(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 2 || !ws[0].IsPrimary {
		t.Errorf("워크스페이스 목록이 틀렸다: %+v", ws)
	}

	// 탈락 사유가 통째로 남아야 한다 — 사유 없는 큐는 블랙박스가 되고 무시된다.
	if err := s.RecordPickEval(ctx, model.PickEval{
		Project: "p", SessionID: a.ID, Picked: "t5-x",
		Rejected: []model.Rejection{
			{Item: "t5-y", Reason: "claimed", Detail: "세션 S2 가 선점 중"},
			{Item: "t5-z", Reason: "path_overlap", Detail: "services/ 가 겹친다"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := s.db.QueryRow(`SELECT rejected FROM pick_eval`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"path_overlap", "세션 S2 가 선점 중"} {
		if !strings.Contains(raw, want) {
			t.Errorf("탈락 사유에 %q 가 없다: %s", want, raw)
		}
	}
}

// ★ 읽고 나서 쓰는 트랜잭션이 경합해도 **하나도 죽으면 안 된다.**
//
// 선점·반납·종료가 전부 이 모양이다(읽어서 점유자를 보고, 그 뒤에 쓴다).
// WAL 에서 deferred 트랜잭션은 읽기 스냅숏을 잡은 뒤 남이 커밋하면 쓰기 승격이
// SQLITE_BUSY 로 **즉시** 실패한다 — busy_timeout 이 있어도 소용없다(스냅숏 충돌이라
// 기다린다고 풀리지 않는다). 그러면 사용자에게 "database is locked" 가 그대로 올라간다.
// DSN 의 _txlock=immediate 가 BEGIN 시점에 쓰기 잠금을 잡아 그 승격 자체를 없앤다.
//
// 이 시험은 그 축을 **동작으로** 본다. DSN 문자열을 단정하면 구현의 사본이 되고,
// 드라이버가 그 인자를 조용히 무시해도(모르는 pragma 를 무시하듯) 초록이 난다.
func TestTxSurvivesReadThenWriteContention(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	mustItem(t, s, "p", "t5-x")

	const n = 24
	sessions := make([]string, n)
	for i := range sessions {
		sessions[i] = mustSession(t, s, "p", fmt.Sprintf("cc-%02d", i)).ID
	}

	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = s.Tx(ctx, func(tx *Tx) error {
				// 읽기 — 여기서 스냅숏이 잡힌다
				if _, err := tx.GetItem("p", "t5-x"); err != nil {
					return err
				}
				// 그리고 쓰기 — deferred 면 이 승격이 터진다
				return tx.Touch(sessions[i], fmt.Sprintf("services/f%02d.go", i),
					model.OriginObserved, time.Now())
			})
		}(i)
	}
	close(start)
	wg.Wait()

	var failed int
	var first error
	for _, err := range errs {
		if err != nil {
			failed++
			if first == nil {
				first = err
			}
		}
	}
	if failed != 0 {
		t.Fatalf("읽고-쓰는 트랜잭션 %d개 중 %d개가 죽었다. 첫 오류: %v", n, failed, first)
	}
}
