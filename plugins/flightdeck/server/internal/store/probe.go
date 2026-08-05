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
	if _, err := os.Stat(path); err != nil {
		return MigrationPlan{}, fmt.Errorf("DB 파일이 없다(path=%q): %w", path, err)
	}
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return MigrationPlan{}, fmt.Errorf("sqlite 열기 실패(path=%q): %w", path, err)
	}
	defer db.Close()

	var hasTable int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schema_version'`,
	).Scan(&hasTable); err != nil {
		return MigrationPlan{}, fmt.Errorf("schema_version 존재 확인 실패: %w", err)
	}

	var dbVersion int
	if hasTable > 0 {
		var v sql.NullInt64
		if err := db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_version`).Scan(&v); err != nil {
			return MigrationPlan{}, fmt.Errorf("schema_version 읽기 실패: %w", err)
		}
		if v.Valid {
			dbVersion = int(v.Int64)
		}
	}

	var objects int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type IN ('table','index','trigger','view')`,
	).Scan(&objects); err != nil {
		return MigrationPlan{}, fmt.Errorf("sqlite_master 읽기 실패: %w", err)
	}

	// 판정은 migrate 와 **같은 순수 함수**를 쓴다. 여기서 다시 판정하면 두 벌이 된다.
	return PlanMigration(hasTable > 0, dbVersion, objects, SchemaVersion), nil
}
