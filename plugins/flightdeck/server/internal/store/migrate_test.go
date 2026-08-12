package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 증분 마이그레이션과 멱등 기록 표.
//
// ★ 이 파일이 지키는 것은 하나다: **기존 DB 를 여는 경로가 안 깨진다.**
// 새 표를 넣는 변경에서 가장 쉽게 깨지는 자리이고, 깨지면 그 사실이
// "이미 쓰던 사람의 서버가 안 뜬다"로만 드러난다.

// ─────────────────────────────────────────────────────────────────────────────
// UpgradeSteps — 순수 함수
// ─────────────────────────────────────────────────────────────────────────────

func TestUpgradeSteps(t *testing.T) {
	avail := []Migration{{To: 2, Name: "둘"}, {To: 3, Name: "셋"}}

	got, err := UpgradeSteps(1, 3, avail)
	if err != nil {
		t.Fatalf("1→3 경로가 있는데 거절했다: %v", err)
	}
	if len(got) != 2 || got[0].To != 2 || got[1].To != 3 {
		t.Fatalf("순서가 다르다: %+v", got)
	}
	if steps, err := UpgradeSteps(3, 3, avail); err != nil || len(steps) != 0 {
		t.Fatalf("올릴 것이 없는데 %d단을 냈다(err=%v)", len(steps), err)
	}

	// ★ 한 단이 빠지면 거절하고, **어느 단이 빠졌는지**를 말해야 한다.
	//   "경로가 없다"만 알면 2가 없는지 3이 없는지 운영자가 못 가린다.
	_, err = UpgradeSteps(1, 3, []Migration{{To: 3, Name: "셋"}})
	if err == nil {
		t.Fatal("2단이 빠졌는데 통과시켰다 — 모르는 스키마 위에서 돌게 된다")
	}
	if !strings.Contains(err.Error(), "2 로 올리는 증분이 없다") {
		t.Fatalf("사유가 어느 단인지 말하지 않는다: %v", err)
	}
	if _, err := UpgradeSteps(3, 1, avail); err == nil {
		t.Fatal("내려가는 마이그레이션을 받아들였다")
	}
}

// 이 바이너리가 실제로 가진 증분으로 SchemaVersion 까지 갈 수 있어야 한다.
// 버전 상수만 올리고 증분 파일을 안 넣는 실수를 여기서 잡는다.
func TestBundledMigrationsReachSchemaVersion(t *testing.T) {
	steps, err := UpgradeSteps(BaseSchemaVersion, SchemaVersion, migrations)
	if err != nil {
		t.Fatalf("기반 %d → 현재 %d 경로가 없다: %v", BaseSchemaVersion, SchemaVersion, err)
	}
	for _, m := range steps {
		if strings.TrimSpace(m.SQL) == "" {
			t.Fatalf("증분 %d(%s) 의 SQL 이 비었다 — //go:embed 가 안 걸렸을 수 있다", m.To, m.Name)
		}
		// 트랜잭션 안에서 도는 자리라 PRAGMA 가 들어가면 안 된다.
		if strings.Contains(strings.ToUpper(m.SQL), "PRAGMA ") {
			t.Errorf("증분 %d 에 PRAGMA 가 있다 — 트랜잭션 안에서 못 돈다", m.To)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 기존 DB 를 여는 경로
// ─────────────────────────────────────────────────────────────────────────────

// makeV1DB 는 **버전 1 시절의 DB** 를 만든다.
//
// 현행 스키마를 적용한 뒤 증분이 만든 것을 걷어내고 버전 기록을 1로 되돌린다.
// 호출부는 이 함수가 정말 v1 을 만들었는지를 **결과를 읽기 전에** 단정해야 한다 —
// 이 구성이 조용히 실패하면 "업그레이드가 됐다"가 아니라 "애초에 v2 였다"가 된다.
func makeV1DB(t *testing.T, path string) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s, err := OpenWithLogger(path, log)
	if err != nil {
		t.Fatalf("v1 DB 구성 실패(첫 Open): %v", err)
	}
	// v1 시절의 데이터를 남긴다 — 업그레이드가 그것을 지우지 않는지가 이 시험의 축이다.
	if err := s.UpsertProject(context.Background(),
		model.Project{ID: "v1p", Path: "/repo/v1p"}); err != nil {
		t.Fatalf("v1 DB 구성 실패(프로젝트): %v", err)
	}
	if err := s.AddItem(context.Background(),
		model.Item{Project: "v1p", ID: "v1-item", Title: "옛 항목", Body: "본문"}); err != nil {
		t.Fatalf("v1 DB 구성 실패(항목): %v", err)
	}
	// v1 로 되돌린다 — 증분이 만든 객체를 전부 걷어낸다.
	// ★ 새 증분을 더할 때마다 여기에 그 객체를 더해야 한다. 안 더하면 재열기에서
	//   "table already exists" 로 죽고, 그 실패는 마이그레이션 결함처럼 보이지만 이 목록의 누락이다.
	// 007 증분이 project 에 더한 컬럼 둘도 걷는다(dropNonIdempotentColumns 참고) — 이
	// 함수는 첫 Open 에서 이미 SchemaVersion(지금은 7)까지 물리적으로 올라간 뒤 버전
	// 기록만 1로 되돌리므로, 안 걷으면 재열기가 007 을 다시 돌려다 죽는다.
	dropNonIdempotentColumns(t, func(q string) (sql.Result, error) { return s.db.Exec(q) })
	for _, q := range []string{
		`DROP INDEX IF EXISTS idempotency_by_at`,
		`DROP TABLE IF EXISTS idempotency`,
		// 008 증분이 만든 자원 표. landing_queue 를 참조하므로(FK) landing_queue 보다
		// 먼저 걷는다 — 순서를 바꾸면 foreign_keys=1 아래서 DROP TABLE landing_queue 가 실패한다.
		`DROP INDEX IF EXISTS landing_queue_resource_by_name`,
		`DROP TABLE IF EXISTS landing_queue_resource`,
		`DROP INDEX IF EXISTS landing_queue_waiting`,
		`DROP INDEX IF EXISTS landing_queue_one_live_per_session`,
		`DROP TABLE IF EXISTS landing_queue`,
		// 004 증분이 pick_eval 에 더한 컬럼. v1 은 이 표를 갖고 있었지만(schema.sql)
		// 그 컬럼은 없었다 — 안 걷으면 재열기에서 같은 컬럼을 또 만들려다 죽는다.
		`ALTER TABLE pick_eval DROP COLUMN picked_with`,
		`DELETE FROM schema_version WHERE version > 1`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("v1 DB 구성 실패(%s): %v", q, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("v1 DB 닫기 실패: %v", err)
	}
}

func hasTableIn(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n); err != nil {
		t.Fatalf("표 존재 확인 실패(%s): %v", name, err)
	}
	return n > 0
}

// ★ v1 DB 를 v2 바이너리로 열면 **데이터를 유지한 채** 올라간다.
func TestOpenUpgradesVersion1Database(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fd.db")
	makeV1DB(t, path)

	// ── 대조 전제: 정말 v1 인가 ──
	raw, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("전제 확인용 열기 실패: %v", err)
	}
	var v int
	if err := raw.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&v); err != nil {
		t.Fatalf("전제 확인 실패: %v", err)
	}
	if v != 1 {
		t.Fatalf("전제가 성립하지 않았다: schema_version=%d — 이 상태로는 아래 단정이 무의미하다", v)
	}
	for _, table := range []string{"idempotency", "landing_queue"} {
		if hasTableIn(t, raw, table) {
			t.Fatalf("전제가 성립하지 않았다: v1 인데 %s 표가 이미 있다", table)
		}
	}
	raw.Close()

	// ── 본 판정 ──
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s, err := OpenWithLogger(path, log)
	if err != nil {
		t.Fatalf("v1 DB 를 열지 못했다 — 이미 쓰던 서버가 안 뜬다: %v", err)
	}
	defer s.Close()

	if err := s.db.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&v); err != nil {
		t.Fatalf("버전 읽기 실패: %v", err)
	}
	if v != SchemaVersion {
		t.Errorf("업그레이드 뒤 버전이 %d 다 — %d 를 기대했다", v, SchemaVersion)
	}
	for _, table := range []string{"idempotency", "landing_queue"} {
		if !hasTableIn(t, s.db, table) {
			t.Errorf("업그레이드했는데 %s 표가 없다", table)
		}
	}
	// 옛 데이터가 살아 있어야 한다. 여기가 이 시험의 진짜 축이다.
	if _, err := s.GetItem(context.Background(), "v1p", "v1-item"); err != nil {
		t.Errorf("업그레이드가 옛 데이터를 지웠다: %v", err)
	}
	// 백업이 떠 있어야 한다 — 나쁜 마이그레이션은 1인 운영에서 복구 불가 사건이다.
	entries, _ := os.ReadDir(dir)
	backup := false
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak-") {
			backup = true
		}
	}
	if !backup {
		t.Error("기존 데이터 위에 증분을 얹었는데 백업이 없다")
	}

	// 다시 열면 아무것도 안 한다(멱등).
	s2, err := OpenWithLogger(path, log)
	if err != nil {
		t.Fatalf("업그레이드된 DB 재열기 실패: %v", err)
	}
	defer s2.Close()
}

// 옛 DB(컬럼 없음)를 열면 판올림이 컬럼을 만들고, 옛 행은 그대로 읽혀야 한다.
//
// ★ 전제를 **결과를 읽기 전에** 단정한다. 옛 상태가 실제로 만들어지지 않았으면
// 아래 단정은 아무것도 안 지킨다 — makeV1DB 를 쓰는 시험이 같은 규율을 갖고 있다.
func TestUpgradeAddsPickedWithAndKeepsOldRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	s, err := OpenWithLogger(path, log)
	if err != nil {
		t.Fatalf("Open 실패: %v", err)
	}
	seed(t, s, "P")
	// ★ 리터럴 3 이다. SchemaVersion-1 이 아니다.
	//
	// 이 시험이 재현하려는 옛 상태는 "picked_with 가 생기기 직전", 즉 **004 직전인
	// 3** 이다. 그 사실은 004_pick_bundle.sql 에 고정돼 있고 뒤에 무슨 증분이 더
	// 붙든 변하지 않는다. SchemaVersion-1 로 적으면 005 를 쓰는 사람이 — pick_eval
	// 을 건드린 적도 없는데 — `판올림 뒤 옛 행을 못 읽는다` 로 터지는 시험을
	// 물려받는다(전제 DELETE 가 004 를 남겨 picked_with 를 도로 만들어 버리기
	// 때문이다). 고정점을 고정점으로 적는다.
	const prev = 3
	// 007 증분(project.pinned_at·archived_at)이 prev(3) 뒤에 온다(dropNonIdempotentColumns
	// 참고) — 위의 첫 Open 이 이미 SchemaVersion 까지 올려 그 컬럼을 물리적으로 만들어
	// 뒀으므로, 안 걷으면 재열기가 007 을 다시 돌려다 죽는다.
	dropNonIdempotentColumns(t, func(q string) (sql.Result, error) { return s.db.Exec(q) })
	for _, q := range []string{
		`INSERT INTO pick_eval(project, session_id, at, picked, rejected)
		   VALUES ('P','S1','2026-08-01T00:00:00.000000Z','old-lead','[]')`,
		`ALTER TABLE pick_eval DROP COLUMN picked_with`,
		// 008 증분(landing_queue_resource)이 prev(3) 뒤에 온다 — 위의 첫 Open 이 이미
		// SchemaVersion 까지 올려 그 표를 물리적으로 만들어 뒀으므로, 안 걷으면 재열기가
		// 008 을 다시 돌려다 "table already exists" 로 죽는다.
		`DROP INDEX IF EXISTS landing_queue_resource_by_name`,
		`DROP TABLE IF EXISTS landing_queue_resource`,
		fmt.Sprintf(`DELETE FROM schema_version WHERE version > %d`, prev),
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("옛 DB 구성 실패(%s): %v", q, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("닫기 실패: %v", err)
	}

	// ── 전제 확인 ──
	raw, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("raw 열기 실패: %v", err)
	}
	var v int
	if err := raw.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&v); err != nil {
		t.Fatalf("전제 확인 실패: %v", err)
	}
	if v != prev {
		t.Fatalf("전제가 성립하지 않았다: schema_version=%d — 이 상태로는 아래 단정이 무의미하다", v)
	}
	raw.Close()

	// ── 판올림 ──
	s2, err := OpenWithLogger(path, log)
	if err != nil {
		t.Fatalf("판올림 Open 실패: %v", err)
	}
	defer s2.Close()

	var picked string
	var isNull bool
	if err := s2.db.QueryRow(
		`SELECT picked, picked_with IS NULL FROM pick_eval WHERE session_id='S1'`,
	).Scan(&picked, &isNull); err != nil {
		t.Fatalf("판올림 뒤 옛 행을 못 읽는다: %v", err)
	}
	if picked != "old-lead" {
		t.Fatalf("옛 행의 picked 가 %q 로 바뀌었다", picked)
	}
	if !isNull {
		t.Fatal("옛 행의 picked_with 가 NULL 이 아니다 — 그 행은 묶음을 관측한 적이 없다")
	}
}

// ★ 신규 설치와 업그레이드가 **같은 모양의 DB** 를 만들어야 한다.
//
// 이 단정이 없으면 "신규용으로 schema.sql 에 표를 한 번 더 적는" 개조가 조용히 통과하고,
// 그때부터 두 경로의 DB 가 갈라진다. 그 차이는 문제가 터지기 전까지 아무 데도 안 보인다.
func TestFreshInstallAndUpgradeProduceTheSameSchema(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	freshPath := filepath.Join(t.TempDir(), "fresh.db")
	fresh, err := OpenWithLogger(freshPath, log)
	if err != nil {
		t.Fatalf("신규 설치 실패: %v", err)
	}
	defer fresh.Close()

	upPath := filepath.Join(t.TempDir(), "up.db")
	makeV1DB(t, upPath)
	upgraded, err := OpenWithLogger(upPath, log)
	if err != nil {
		t.Fatalf("업그레이드 실패: %v", err)
	}
	defer upgraded.Close()

	dump := func(s *Store) []string {
		rows, err := s.db.Query(
			`SELECT type || ' ' || name || ' :: ' || COALESCE(sql,'') FROM sqlite_master ORDER BY type, name`)
		if err != nil {
			t.Fatalf("sqlite_master 조회 실패: %v", err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatalf("sqlite_master 행 해석 실패: %v", err)
			}
			out = append(out, s)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("sqlite_master 순회 실패: %v", err)
		}
		sort.Strings(out)
		return out
	}
	a, b := dump(fresh), dump(upgraded)
	if len(a) == 0 {
		t.Fatal("전제가 깨졌다 — 신규 DB 에 스키마 객체가 하나도 없다")
	}
	if strings.Join(a, "\n") != strings.Join(b, "\n") {
		t.Errorf("신규 설치와 업그레이드의 스키마가 다르다:\n[신규]\n%s\n\n[업그레이드]\n%s",
			strings.Join(a, "\n"), strings.Join(b, "\n"))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 멱등 기록
// ─────────────────────────────────────────────────────────────────────────────

func TestValidateIdemRecord(t *testing.T) {
	base := IdemRecord{Key: "k", Fingerprint: "f", Status: http.StatusCreated}
	if err := ValidateIdemRecord(base); err != nil {
		t.Fatalf("정상 기록을 거절했다: %v", err)
	}
	cases := []struct {
		name         string
		mut          func(*IdemRecord)
		wantInReason string
	}{
		{"키 없음", func(r *IdemRecord) { r.Key = "" }, "키가 비었다"},
		{"지문 없음", func(r *IdemRecord) { r.Fingerprint = "" }, "지문이 비었다"},
		{"5xx", func(r *IdemRecord) { r.Status = 500 }, "저장하지 않는다"},
		{"503 도 5xx", func(r *IdemRecord) { r.Status = 503 }, "저장하지 않는다"},
		{"범위 밖", func(r *IdemRecord) { r.Status = 42 }, "범위 밖"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := base
			c.mut(&r)
			err := ValidateIdemRecord(r)
			if err == nil {
				t.Fatalf("%+v 를 받아들였다", r)
			}
			if !strings.Contains(err.Error(), c.wantInReason) {
				t.Fatalf("사유에 %q 가 없다: %v", c.wantInReason, err)
			}
		})
	}
}

func TestIdemRecordRoundTripAndPrune(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)

	rec := IdemRecord{
		Key: "cc-1:7", Fingerprint: "fp1", Status: http.StatusCreated,
		ContentType: "application/json; charset=utf-8", Body: []byte(`{"item":"x"}`), At: now,
	}
	if err := s.PutIdemRecord(ctx, rec, 24*time.Hour, 0); err != nil {
		t.Fatalf("저장 실패: %v", err)
	}
	got, err := s.GetIdemRecord(ctx, "cc-1:7")
	if err != nil {
		t.Fatalf("조회 실패: %v", err)
	}
	if got.Fingerprint != "fp1" || got.Status != http.StatusCreated ||
		string(got.Body) != `{"item":"x"}` || got.ContentType != rec.ContentType {
		t.Fatalf("왕복이 값을 바꿨다: %+v", got)
	}
	if _, err := s.GetIdemRecord(ctx, "없는키"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("없는 키가 ErrNotFound 가 아니다: %v", err)
	}

	// 5xx 는 저장 자체가 거절된다(1차 방어). 규율이 산문이 아니라 코드다.
	bad := rec
	bad.Key, bad.Status = "cc-1:8", http.StatusBadGateway
	if err := s.PutIdemRecord(ctx, bad, 24*time.Hour, 0); err == nil {
		t.Fatal("5xx 가 저장됐다 — 일시 장애가 영구 응답으로 굳는다")
	}

	// TTL: 새 기록을 하루 뒤 시각으로 넣으면 옛 것이 걷힌다.
	later := rec
	later.Key, later.At = "cc-1:9", now.Add(25*time.Hour)
	if err := s.PutIdemRecord(ctx, later, 24*time.Hour, 0); err != nil {
		t.Fatalf("두 번째 저장 실패: %v", err)
	}
	if _, err := s.GetIdemRecord(ctx, "cc-1:7"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("TTL 을 넘긴 기록이 안 걷혔다: %v", err)
	}
	n, err := s.CountIdemRecords(ctx)
	if err != nil {
		t.Fatalf("개수 조회 실패: %v", err)
	}
	if n != 1 {
		t.Fatalf("TTL 청소 뒤 %d건이다 — 1건을 기대했다", n)
	}

	// 개수 상한: max=2 로 셋을 넣으면 가장 오래된 것이 걷힌다.
	for i, k := range []string{"a", "b", "c"} {
		r := rec
		r.Key, r.At = k, now.Add(48*time.Hour+time.Duration(i)*time.Minute)
		if err := s.PutIdemRecord(ctx, r, 0, 2); err != nil {
			t.Fatalf("상한 시험 저장 실패(%s): %v", k, err)
		}
	}
	if n, err = s.CountIdemRecords(ctx); err != nil || n != 2 {
		t.Fatalf("상한 2 인데 %d건이 남았다(err=%v)", n, err)
	}
	if _, err := s.GetIdemRecord(ctx, "c"); err != nil {
		t.Fatalf("가장 최근 기록이 걷혔다: %v", err)
	}
}
