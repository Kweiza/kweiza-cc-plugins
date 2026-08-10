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

// ─────────────────────────────────────────────────────────────────────────────
// 재기동 판정은 **대상을 한 바이트도 안 바꾼다**
// ─────────────────────────────────────────────────────────────────────────────
//
// ProbeMigration 은 기본 dsn() 을 썼고 그 안의 journal_mode(WAL) 이 롤백저널 DB 를
// 만나면 헤더 18·19바이트를 1/1 → 2/2 로 **영구히** 고친다. 그래서
// `fd selfcheck --db <백업>` 이 그 백업을 변조했다 — 사람이 아카이브를 점검하는 것은
// 자연스러운 사용인데, 그 순간 마지막 남은 백업이 바뀐다.
//
// ★ 예전 근거와 그것을 왜 뒤집는가. probe.go 는 "재기동 판정 경로의 열기 조건은 실제
// 열기와 같아야 한다"로 이 축을 일부러 안 고쳤다. 그 근거가 사는 유일한 자리는
// **감시기가 재기동을 걸기 직전**인데, 그 자리에서는 journal_mode 적용 가능성이
// 이미 증명돼 있다 — 지금 도는 서버가 바로 그 파일을 WAL 로 열고 있기 때문이다
// (serve.go 가 자기 dbPath 를 newSelfWatcher 에 그대로 넘긴다). 즉 그 pragma 는
// 재기동 경로에서 **자기가 이미 아는 것을 다시 재는** 축이고, 그 대가가 아카이브 변조다.
func TestProbeMigrationDoesNotRewriteJournalMode(t *testing.T) {
	for _, c := range []struct {
		name string
		seed int
	}{
		{"거절 경로(이행이 필요하다)", BaseSchemaVersion},
		{"통과 경로(이미 맞다)", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fd.db")
			makeRollbackJournalDB(t, path, c.seed)
			before := journalHeaderOf(t, path)

			if _, err := ProbeMigration(context.Background(), path); err != nil {
				t.Fatalf("재기 실패: %v", err)
			}
			if after := journalHeaderOf(t, path); after != before {
				t.Errorf("재기만 했는데 대상을 바꿨다: 헤더 %d/%d → %d/%d\n"+
					"`fd selfcheck --db <백업>` 이 그 백업을 WAL 로 영구 변환한다 — "+
					"이 명령이 존재하는 이유가 '망가진 판에서 재기동해도 되는가'인데, "+
					"재는 행위가 대상을 바꾸면 마지막 남은 아카이브가 오타 한 번에 고쳐진다.",
					before[0], before[1], after[0], after[1])
			}
		})
	}
}

// 판정 자체는 안 달라진다 — 바뀐 것은 접속 문자열의 pragma 하나뿐이다.
func TestProbeMigrationVerdictUnchangedByDSN(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fd.db")
	makeRollbackJournalDB(t, path, BaseSchemaVersion)

	got, err := ProbeMigration(context.Background(), path)
	if err != nil {
		t.Fatalf("재기 실패: %v", err)
	}
	if got.Action != MigrateUpgrade {
		t.Errorf("판정이 %q 다 — 옛 판 DB 는 증분 대상이어야 한다: %s", got.Action, got.Reason)
	}
}
