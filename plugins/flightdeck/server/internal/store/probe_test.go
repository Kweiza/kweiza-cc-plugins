package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// probeDB 는 시험용 DB 를 만든다. sqls 를 순서대로 실행한다(비면 파일만 만든다).
func probeDB(t *testing.T, sqls ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fd.db")
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("열기 실패: %v", err)
	}
	defer db.Close()
	for _, q := range sqls {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("준비 SQL 실패(%q): %v", q, err)
		}
	}
	if len(sqls) == 0 {
		// 파일만 만들고 객체는 0으로 둔다
		if _, err := db.Exec(`SELECT 1`); err != nil {
			t.Fatalf("빈 DB 준비 실패: %v", err)
		}
	}
	return path
}

func TestProbeMigrationEmptyDBPlansApply(t *testing.T) {
	plan, err := ProbeMigration(context.Background(), probeDB(t))
	if err != nil {
		t.Fatalf("probe 실패: %v", err)
	}
	if plan.Action != MigrateApply {
		t.Fatalf("빈 DB 인데 %q 다 — 사유 %q", plan.Action, plan.Reason)
	}
}

// ★ 이 갈래가 selfcheck 의 존재 이유다 — 강등하면 여기서 걸린다.
func TestProbeMigrationRejectsWhenDBIsAheadOfCode(t *testing.T) {
	path := probeDB(t,
		`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL, applied_at TEXT NOT NULL)`,
		`INSERT INTO schema_version VALUES (9999, '2026-01-01T00:00:00Z')`,
		`CREATE TABLE filler (a TEXT)`,
	)
	plan, err := ProbeMigration(context.Background(), path)
	if err != nil {
		t.Fatalf("probe 실패: %v", err)
	}
	if plan.Action != MigrateReject {
		t.Fatalf("DB 가 코드보다 앞선데 %q 다 — 사유 %q", plan.Action, plan.Reason)
	}
}

func TestProbeMigrationRejectsForeignDB(t *testing.T) {
	path := probeDB(t, `CREATE TABLE somebody_elses (a TEXT)`)
	plan, err := ProbeMigration(context.Background(), path)
	if err != nil {
		t.Fatalf("probe 실패: %v", err)
	}
	if plan.Action != MigrateReject {
		t.Fatalf("남의 DB 인데 %q 다", plan.Action)
	}
}

// ★ 없는 파일에 probe 하면 **만들면 안 된다.** sql.Open 은 파일을 만든다.
func TestProbeMigrationDoesNotCreateMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.db")
	if _, err := ProbeMigration(context.Background(), path); err == nil {
		t.Fatal("없는 파일인데 오류가 없다")
	} else if !strings.Contains(err.Error(), "없다") {
		t.Fatalf("사유가 부재를 안 말한다: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("probe 가 DB 파일을 만들었다 — 읽기 전용이어야 한다")
	}
}
