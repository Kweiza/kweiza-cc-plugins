package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
)

// ProbeMigration 은 DB 를 **읽기만 해서** 이 바이너리가 그것을 어떻게 다룰지를 낸다.
//
// ★ store.Open 과 다른 점이 전부다: **적용하지 않는다.** 검증 도중 증분이 붙어 버리면,
// 그 뒤 재기동이 실패했을 때 옛 프로세스가 새 스키마 위에서 돌게 된다 —
// 조용히 망가지는 경로이고, 그때는 되돌릴 자리도 없다.
//
// 없는 파일에는 오류를 낸다. sql.Open 은 파일을 **만들기** 때문에, 부재를 확인 안 하고
// 열면 검증이 빈 DB 를 하나 만들어 놓고 "빈 DB 다"라고 답한다.
func ProbeMigration(ctx context.Context, path string) (MigrationPlan, error) {
	return probeMigrationVia(ctx, path, dsn(path))
}

// probeMigrationLedger 는 **대상 파일을 한 바이트도 안 바꾸는** 재기다.
//
// ★ 왜 갈래가 둘인가. ProbeMigration 은 기본 dsn() 을 쓰고 그 안에 `journal_mode(WAL)` 이
// 있다 — 그것이 이 DSN 에서 파일을 바꿀 수 있는 유일한 pragma 이고, 롤백저널 DB 를 만나면
// 헤더 18·19바이트를 1/1 에서 2/2 로 **영구히** 고친다. OpenLedger 가 그 함수를 첫 줄에서
// 부르는 바람에 "이행이 필요하니 거절한다"고 인쇄하는 실행조차 아카이브를 변조했다.
//
// ★ 왜 ProbeMigration 쪽을 안 고치는가. 그 함수는 fd selfcheck 가 쓰고, 그 명령이 0 을 내면
// 감시기가 syscall.Exec 로 넘어간다 — 서버가 곧바로 그 DB 를 기본 DSN 으로 열 자리다.
// 재기동 판정 경로의 열기 조건은 실제 열기와 같아야 하므로 건드리지 않는다.
//
// 판정 자체(readMigrationState → PlanMigration)는 한 벌 그대로다. 다른 것은 DSN 뿐이다.
func probeMigrationLedger(ctx context.Context, path string) (MigrationPlan, error) {
	return probeMigrationVia(ctx, path, ledgerDSN(path))
}

// probeMigrationVia 는 두 갈래의 공통 본체다. 접속 문자열만 달라진다.
func probeMigrationVia(ctx context.Context, path, connStr string) (MigrationPlan, error) {
	if _, err := os.Stat(path); err != nil {
		return MigrationPlan{}, fmt.Errorf("DB 파일이 없다(path=%q): %w", path, err)
	}
	db, err := sql.Open("sqlite", connStr)
	if err != nil {
		return MigrationPlan{}, fmt.Errorf("sqlite 열기 실패(path=%q): %w", path, err)
	}
	defer db.Close()

	// 판정 입력을 readMigrationState 로 읽는다. Open 경로(migrate)도 같은 함수를 쓴다 —
	// 두 벌로 두면 한쪽만 고쳐져 탐지가 갈린다.
	hasTable, dbVersion, objects, err := readMigrationState(ctx, db)
	if err != nil {
		return MigrationPlan{}, err
	}

	// 판정은 migrate 와 **같은 순수 함수**를 쓴다. 여기서 다시 판정하면 두 벌이 된다.
	return PlanMigration(hasTable, dbVersion, objects, SchemaVersion), nil
}
