package store

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testLogger 는 마이그레이션 로그를 버린다(시험 출력이 덮이지 않게).
func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// 되돌리기 절차 — 소비자 좌표계는 **오류 문자열**이다.
//
// ★ 설계 §7 은 나쁜 스키마 변경의 처방을 셋으로 적었고(one-shot 분리 · 기동 전 백업 · 롤백 명령)
// 지금 구현은 백업만 만족한다. 그 어긋남은 store.go 의 마이그레이션 절 주석에 적혀 있다.
// 이 시험이 지키는 것은 **남은 하나의 대체물**이다: 되돌리는 명령이 없다면
// 적어도 그 절차와 백업 파일 좌표가 실패한 그 자리에 있어야 한다.

func TestRollbackHint(t *testing.T) {
	got := RollbackHint("/data/fd.db", "/data/fd.db.bak-20260803T120000Z")
	for _, want := range []string{
		"/data/fd.db.bak-20260803T120000Z", // 어디서 되돌리나
		"/data/fd.db",                      // 어디로 되돌리나
		"/data/fd.db-wal",                  // 옛 WAL 을 안 지우면 반쯤 적용된 상태가 되살아난다
		"/data/fd.db-shm",
		"cp -f",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("절차에 %q 가 없다: %s", want, got)
		}
	}
	// ★ 2026-08-23 에 이 단정이 뒤집혔다. 그전까지 이 문구는 "설계 §7 대비 미구현"이라는
	// 사실을 말해야 했고 시험도 "§7" 을 요구했다. 이제 그 처방이 지어졌으므로 문구가
	// 말해야 할 것은 미구현이 아니라 **다음에 칠 명령**이다 — 절차가 실패한 그 자리에
	// 수단이 있어야 한다는 규율은 그대로이고, 가리키는 곳만 바뀌었다.
	if !strings.Contains(got, "fd migrate --rollback") {
		t.Errorf("되돌리는 명령을 문구가 안 낸다 — 손 복사 절차만 남으면 -wal 지우기를 빠뜨린다: %s", got)
	}

	// 백업이 없으면 **없다고 말한다.** 있는 척하면 그 순간 되돌릴 수 있다는 거짓이 생긴다.
	none := RollbackHint("/data/fd.db", "")
	if strings.Contains(none, "cp -f") {
		t.Errorf("백업이 없는데 복사 명령을 냈다: %s", none)
	}
	if !strings.Contains(none, "되돌릴 파일이 없다") || !strings.Contains(none, ".bak-*") {
		t.Errorf("백업 부재와 옛 백업 위치가 문구에 없다: %s", none)
	}
}

// 증분이 실제로 깨졌을 때, 오류가 **백업 경로와 되돌리는 절차**를 담는지 본다.
//
// ★ 앞선 판은 업그레이드 경로에서 백업 경로를 `:=` 로 선언해 버렸다. 그래서
// 정확히 §7 이 겨냥한 상황에서 유일한 탈출구의 좌표가 사라졌다.
func TestFailedUpgradeNamesTheBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fd.db")
	ctx := context.Background()

	// 정상 DB 를 한 번 만든다.
	mustMigrate(t, path)
	s, err := OpenWithLogger(path, testLogger())
	if err != nil {
		t.Fatalf("첫 열기 실패: %v", err)
	}
	// 버전을 되돌려 "증분을 얹어야 하는 DB" 로 만든다.
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM schema_version WHERE version > ?`, BaseSchemaVersion); err != nil {
		t.Fatalf("버전 되돌리기 실패: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("닫기 실패: %v", err)
	}

	// ── 대조가 성립했는지 먼저 단정한다 ─────────────────────────────────────
	// 증분을 망가뜨리기 **전에** 이 DB 가 정말 업그레이드 대상인지 확인한다.
	// 아니면 아래 실패는 증분이 아니라 다른 이유로 났을 수 있고, 그러면 이 시험은
	// 기대한 오류를 그대로 보면서 아무것도 검사하지 않는다.
	plan := PlanMigration(true, BaseSchemaVersion, 2, SchemaVersion)
	if plan.Action != MigrateUpgrade || !plan.Backup {
		t.Fatalf("전제가 깨졌다 — 이 상태의 판정이 %q(backup=%v)다: %s",
			plan.Action, plan.Backup, plan.Reason)
	}
	before := backupsOf(t, path)
	if len(before) != 0 {
		t.Fatalf("전제가 깨졌다 — 이미 백업이 %d개 있다", len(before))
	}

	// 커밋된 기준선 위의 변이다 — defer 로 원복한다.
	// ★ 한 단만 두면 UpgradeSteps 가 적용 전에 거절해 시험이 보려던 경로에 안 들어간다.
	//   BaseSchemaVersion+1 부터 SchemaVersion 까지 전 구간을 채우되 마지막 단을 깨뜨린다.
	saved := migrations
	defer func() { migrations = saved }()
	var stub []Migration
	for v := BaseSchemaVersion + 1; v <= SchemaVersion; v++ {
		m := Migration{To: v, Name: "일부러 깨뜨린 증분", SQL: `SELECT 1;`}
		if v == SchemaVersion {
			m.SQL = `THIS IS NOT SQL;`
		}
		stub = append(stub, m)
	}
	migrations = stub

	// ★ 적용이 기동에서 분리된 뒤로 이 실패는 Migrate 의 것이다(설계 §7 ①).
	//   여기에 mustMigrate 를 쓰면 안 된다 — 그것은 실패를 t.Fatal 로 삼켜서
	//   이 시험이 보려는 오류 문구가 영영 안 온다.
	err = Migrate(ctx, path, testLogger())
	if err == nil {
		t.Fatal("깨진 증분으로 적용이 성공했다 — 모르는 스키마 위에서 돌게 된다")
	}

	after := backupsOf(t, path)
	if len(after) != 1 {
		t.Fatalf("전제가 깨졌다 — 적용 전 백업이 %d개다(1개여야 한다)", len(after))
	}
	msg := err.Error()
	if !strings.Contains(msg, after[0]) {
		t.Errorf("오류가 백업 경로(%s)를 안 낸다 — 되돌릴 곳을 손으로 찾아야 한다:\n%s", after[0], msg)
	}
	if !strings.Contains(msg, "cp -f") {
		t.Errorf("오류에 되돌리는 절차가 없다:\n%s", msg)
	}
	if !strings.Contains(msg, "일부러 깨뜨린 증분") {
		t.Errorf("오류가 어느 증분에서 깨졌는지 안 낸다:\n%s", msg)
	}
}

// backupsOf 는 그 DB 옆의 백업 파일 전부다(경로 전체).
func backupsOf(t *testing.T, dbPath string) []string {
	t.Helper()
	matches, err := filepath.Glob(dbPath + ".bak-*")
	if err != nil {
		t.Fatalf("백업 탐색 실패: %v", err)
	}
	var out []string
	for _, m := range matches {
		if fi, err := os.Stat(m); err == nil && !fi.IsDir() {
			out = append(out, m)
		}
	}
	return out
}
