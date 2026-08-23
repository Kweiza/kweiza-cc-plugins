package store

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// Open 은 적용하지 않는다 — 설계 §7 처방 ①(적용을 기동에서 분리)
// ─────────────────────────────────────────────────────────────────────────────
//
// ★ 이 파일이 뒤집는 것은 판단 하나다. store.go 마이그레이션 절과 DESIGN §7 각주는
// "적용을 기동에 남긴다" 를 두 번 유지했고, 그 근거는 **"적용을 떼면 모든 명령(fail-open 훅
// 4종 포함)이 스키마가 안 올라간 DB 를 만나는 새 경로가 생긴다"** 였다.
//
// 그 전제를 전수로 재니 거짓이었다(판단 01M0P2Q6D0YQPDC5ZZMJT8SY74). cmd/ 에서 DB 를 여는
// 문은 셋뿐이고(serve · openDB · OpenLedger) 훅 여섯은 **하나도 DB 를 열지 않는다** —
// beatFromHook 은 a.cli.Write 로 REST 를 치고 실패하면 아웃박스로 흐른다. failopen_test.go 의
// "fail-open" 은 큐 잠금 축이지 DB 스키마 축이 아니다.
//
// ★ 왜 업그레이드만 떼는 것으로는 부족한가. MigrateApply(빈 DB 신규 설치) 갈래도
// applyUpgrades 를 부른다("신규 DB 도 증분을 그대로 탄다" — store.go). 그래서 나쁜 증분은
// 신규 설치에서도 크래시루프를 낸다. 그리고 neverExempt 가 DROP TABLE 을 여는 근거는
// "적용이 기동 밖에 있다" 인데, 신규 설치에서 여전히 안이면 DROP TABLE 증분이 기동 경로에서
// 돌아 관문이 막으려던 상황이 그대로 남는다.

// 판올림이 필요한 DB 를 Open 이 만나면 **적용하지 않고 거절한다.**
//
// ★ 이 시험이 지키는 진짜 축은 "거절했다" 가 아니라 **"파일이 안 바뀌었다"** 다.
// 거절만 하고 백업이나 증분을 이미 얹었으면 옛 프로세스가 새 스키마 위에서 돌게 되고,
// 그것이 OpenLedger 머리말이 적어 둔 "조용히 망가지는 경로" 다.
func TestOpenRefusesADatabaseThatNeedsUpgrade(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fd.db")
	makeV1DB(t, path)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s, err := OpenWithLogger(path, log)
	if err == nil {
		s.Close()
		t.Fatal("판올림이 필요한 DB 를 열었다 — 적용이 아직 기동 경로에 있다")
	}

	// ── 사유가 다음 수를 말해야 한다 ──
	// 명령이 없던 시절의 RollbackHint 와 같은 규율: 절차가 실패한 그 자리에 다음 수가 있어야 한다.
	if !strings.Contains(err.Error(), "fd migrate") {
		t.Errorf("거절은 했는데 다음 수를 안 말한다 — 운영자가 무엇을 칠지 모른다: %v", err)
	}

	// ── 진짜 축: 파일이 한 바이트도 안 바뀌었나 ──
	raw, oerr := sql.Open("sqlite", ledgerDSN(path))
	if oerr != nil {
		t.Fatalf("확인용 열기 실패: %v", oerr)
	}
	defer raw.Close()
	var v int
	if err := raw.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&v); err != nil {
		t.Fatalf("버전 읽기 실패: %v", err)
	}
	if v != 1 {
		t.Errorf("거절했다면서 스키마를 %d 로 올렸다 — 거절이 아니라 절반 적용이다", v)
	}

	// 백업조차 뜨면 안 된다. 백업은 적용의 일부이고, 적용을 안 했으면 뜰 이유가 없다.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak-") {
			t.Errorf("적용을 안 했는데 백업 %s 이 떴다 — Open 이 아직 적용 경로를 탄다", e.Name())
		}
	}
}

// 빈 DB(신규 설치)도 Open 이 만들지 않는다.
//
// ★ 이 갈래를 따로 두는 이유: MigrateApply 도 applyUpgrades 를 타므로 여기를 남기면
// 나쁜 증분의 크래시루프 경로가 절반 남는다. 그리고 DROP TABLE 을 여는 근거가 안 선다.
func TestOpenRefusesToCreateANewDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fd.db")

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s, err := OpenWithLogger(path, log)
	if err == nil {
		s.Close()
		t.Fatal("빈 자리에 DB 를 만들었다 — 신규 설치 경로가 아직 기동 안에 있다")
	}
	if !strings.Contains(err.Error(), "fd migrate") {
		t.Errorf("거절은 했는데 다음 수를 안 말한다: %v", err)
	}

	// ★ 파일을 만들어 놓고 거절하면 안 된다 — 다음 호출이 "빈 DB 가 있다"를 보게 된다.
	//   ProbeMigration 머리말이 같은 함정을 적어 뒀다(sql.Open 은 파일을 만든다).
	if _, serr := os.Stat(path); serr == nil {
		t.Error("거절하면서 빈 DB 파일을 남겼다 — 다음 재기가 이것을 '있는 DB' 로 본다")
	}
}

// 스키마가 이미 맞는 DB 는 그대로 열린다. 이 축이 죽으면 서버가 아예 못 뜬다.
func TestOpenStillOpensAMatchingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fd.db")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// 적용은 이제 Migrate 의 일이다.
	if err := Migrate(context.Background(), path, log); err != nil {
		t.Fatalf("적용에 실패했다: %v", err)
	}
	s, err := OpenWithLogger(path, log)
	if err != nil {
		t.Fatalf("스키마가 맞는 DB 를 못 열었다: %v", err)
	}
	defer s.Close()

	var v int
	if err := s.db.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&v); err != nil {
		t.Fatalf("버전 읽기 실패: %v", err)
	}
	if v != SchemaVersion {
		t.Errorf("적용 뒤 버전이 %d 다 — %d 를 기대했다", v, SchemaVersion)
	}
}
