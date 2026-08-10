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
// ★ **대상 파일을 한 바이트도 안 바꾼다.** 기본 dsn() 의 journal_mode(WAL) 이 이 DSN 에서
// 파일을 바꿀 수 있는 유일한 pragma 이고, 롤백저널 DB 를 만나면 헤더 18·19바이트를
// 1/1 에서 2/2 로 **영구히** 고친다. 그래서 여기서는 그 pragma 가 없는 접속 문자열을 쓴다.
//
// ★ 예전에는 갈래가 둘이었다 — 원장 내보내기만 안 바꾸는 DSN 을 쓰고, 이 함수는 기본
// dsn() 을 유지했다. 근거는 "fd selfcheck 가 이 함수를 쓰고, 그 명령이 0 을 내면 감시기가
// syscall.Exec 로 넘어가므로 **재기동 판정의 열기 조건은 실제 열기와 같아야 한다**"였다.
//
// 그 근거를 뒤집는다. 근거가 사는 유일한 자리는 감시기가 재기동을 걸기 직전인데, 그
// 자리에서는 journal_mode 적용 가능성이 **이미 증명돼 있다** — 지금 도는 서버가 바로 그
// 파일을 WAL 로 열고 있기 때문이다(serve.go 가 자기 dbPath 를 newSelfWatcher 에 그대로
// 넘기고, 감시기는 그 경로로만 자식을 부른다). 즉 그 pragma 는 재기동 경로에서 자기가
// 이미 아는 것을 다시 재는 축이고, 그 대가로 `fd selfcheck --db <백업>` 이 아카이브를
// 영구 변환했다. 사람이 백업을 점검하는 것은 자연스러운 사용인데 그 순간 마지막 남은
// 백업이 바뀐다.
//
// ★ **안 재게 된 것을 정직하게 적는다.** 이 재기는 "이 파일에 WAL 을 걸 수 있는가"를
// 더 이상 안 본다(파일시스템이 WAL 을 못 받는 경우 등). 서버가 안 돌고 있는 상태에서
// 사람이 이 명령을 부르면 그 축은 관측되지 않은 채로 남고, 실패는 실제 `fd serve` 기동
// 에서 드러난다. 자동 재기동을 막는 것이 이 명령의 목적이므로 그 갈래는 목적 밖이다.
//
// 없는 파일에는 오류를 낸다. sql.Open 은 파일을 **만들기** 때문에, 부재를 확인 안 하고
// 열면 검증이 빈 DB 를 하나 만들어 놓고 "빈 DB 다"라고 답한다.
func ProbeMigration(ctx context.Context, path string) (MigrationPlan, error) {
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
