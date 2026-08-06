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
