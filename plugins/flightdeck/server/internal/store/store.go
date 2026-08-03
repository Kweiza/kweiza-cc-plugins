// Package store 는 flightdeck 의 SQLite 저장 계층이다.
//
// 정본은 schema.sql 이고 이 패키지는 그 위의 얇은 접근층이다. 스키마의 제약이
// 실제로 났던 사고를 막으므로, 여기서 **제약을 미리 흉내 내 판정하지 않는다** —
// 예를 들어 자원 배타는 부분 유니크 인덱스가 지키고, 이 코드는 그 위반을 받아
// 점유자를 담은 오류로 옮길 뿐이다. 앱에서 먼저 조회해 판정하면 그 사이에 남이 잡는다.
//
// 판정이 필요한 자리는 전부 **순수 함수**로 빼고 시험이 그 함수를 직접 부른다
// (PlanMigration·UpgradeSteps·JudgeClaim·JudgeConstraintCode·ValidateSnapshot·
// ValidateHolder·ValidateFinish·ValidateIdemRecord).
// 판정이 함수 본문에 흩어지면 시험이 그 로직의 사본을 단정하게 되고, 그러면 변이가 조용히 샌다.
// 그리고 다중 조건은 불리언이 아니라 **사유**를 돌려준다 — 사유가 없으면
// "조건 A 때문에 탈락"과 "이 축을 아예 안 본다"가 구분되지 않는다.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // 순수 Go 드라이버. CGO_ENABLED=0 에서 돌아야 한다
)

//go:embed schema.sql
var schemaSQL string

//go:embed migrations/002_idempotency.sql
var migration002 string

// SchemaVersion 은 **이 바이너리가 아는** 스키마 버전이다.
// DB 가 이보다 높으면 연다는 것 자체가 조용히 망가지는 경로이므로 거절한다.
const SchemaVersion = 2

// BaseSchemaVersion 은 schema.sql 하나가 만드는 버전이다.
//
// ★ 빈 DB 도 "schema.sql → 증분 전부"를 거쳐 올라간다. 신규 DB 를 위해 schema.sql 에
// 같은 표를 한 번 더 적으면 그 표의 정의가 두 벌이 되고, 두 벌은 반드시 표류한다 —
// 그때 신규 설치와 업그레이드가 **다른 모양의 DB** 를 갖게 되는데 그 차이는
// 문제가 터지기 전까지 아무 데도 안 보인다. 정의는 한 자리에만 둔다.
const BaseSchemaVersion = 1

// Migration 은 증분 하나다.
type Migration struct {
	To   int    // 적용 뒤의 버전
	Name string // 로그에 남길 이름
	SQL  string
}

// migrations 는 BaseSchemaVersion 위에 순서대로 얹는 증분 전부다.
//
// ★ 트랜잭션 안에서 돌 수 있어야 한다 — PRAGMA journal_mode 같은 문을 넣으면 안 된다.
// (schema.sql 은 그것 때문에 트랜잭션 밖에서 돈다.)
var migrations = []Migration{
	{To: 2, Name: "멱등 기록을 DB 로", SQL: migration002},
}

// timeLayout 은 저장용 시각 표기다.
//
// time.RFC3339Nano 를 쓰면 안 된다 — 소수부의 뒤 0을 잘라 내므로 **길이가 흔들리고**,
// 그러면 사전순 정렬이 시간순과 어긋난다("…:05Z" 의 'Z'(0x5A) 가 "…:05.5Z" 의 '.'(0x2E)보다 크다).
// 스키마 주석이 "정렬이 사전순과 일치한다"를 전제하므로 폭 고정이 필수다.
const timeLayout = "2006-01-02T15:04:05.000000Z"

// ─────────────────────────────────────────────────────────────────────────────
// 오류
// ─────────────────────────────────────────────────────────────────────────────

// ErrNotFound 는 조회 대상이 없을 때의 표식이다. errors.Is 로 판별한다.
var ErrNotFound = errors.New("없다")

// ─────────────────────────────────────────────────────────────────────────────
// Store · Tx
// ─────────────────────────────────────────────────────────────────────────────

// dbtx 는 *sql.DB 와 *sql.Tx 가 함께 만족하는 최소 면이다.
// 질의 구현을 한 벌로 두기 위한 것이다 — 두 벌이 되면 트랜잭션 경로와 단발 경로가 표류한다.
type dbtx interface {
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
}

// Store 는 열린 DB 하나다.
type Store struct {
	db   *sql.DB
	path string
	log  *slog.Logger
}

// Tx 는 진행 중인 쓰기 트랜잭션이다.
//
// 이 DB 핸들은 DSN 에 `_txlock=immediate` 가 걸려 있어 **BEGIN IMMEDIATE** 로 열린다.
//
// 무엇을 사는가를 정확히 적는다. **배타 자체는 이것과 무관하다** — 두 트랜잭션이 같은 것을
// 쓰려 하면 SQLite 가 어느 모드에서든 하나를 거절하므로, 이중 선점은 deferred 에서도 안 난다.
// immediate 가 사는 것은 **신뢰성**이다: deferred 는 읽기 스냅숏을 잡은 뒤 남이 커밋하면
// 쓰기 승격이 SQLITE_BUSY 로 즉시 실패하고(스냅숏 충돌이라 busy_timeout 이 안 듣는다),
// 그러면 정상 요청이 "database is locked" 로 사용자에게 올라간다.
// BEGIN 시점에 쓰기 잠금을 잡으면 그 승격 자체가 사라지고 대기가 busy_timeout 안에서 줄을 선다.
//
// 실측: 읽고 나서 쓰는 트랜잭션 24개를 동시에 돌리면 deferred 는 19~21개가 죽고 immediate 는
// 0개가 죽는다(TestTxSurvivesReadThenWriteContention 이 그 축을 동작으로 본다).
//
// 대가: 읽기 전용 작업도 이 함수를 거치면 쓰기 잠금을 잡아 서로 직렬화된다.
// 그래서 **읽기는 Tx 를 안 거치고** Store 의 조회 메서드가 s.db 로 바로 질의한다(WAL 이라 안 막힌다).
//
// 중첩 트랜잭션은 지원하지 않는다(SQLite 의 SAVEPOINT 를 쓰지 않는다).
type Tx struct {
	tx  *sql.Tx
	ctx context.Context
	s   *Store

	// deferred 는 이 트랜잭션 안에서 남기려 한 계측 이벤트다.
	//
	// 트랜잭션 안에서 별도 커넥션으로 곧장 쓰면 **교착한다** — DSN 에 `_txlock=immediate` 가
	// 걸려 있어 BEGIN 시점부터 쓰기 잠금을 쥐므로, 같은 파일에 다른 커넥션으로 INSERT 하면
	// busy_timeout 을 통째로 기다린 뒤 SQLITE_BUSY 로 실패한다(실측 4.5초, 기록 0건).
	// 그러면 이벤트를 남길 가장 자연스러운 자리(원자화된 finish 같은 트랜잭션 안)에서
	// 계측이 **구조적으로 항상 0**이 되고, "0에 수렴하면 위험 신호"라는 §10 의 지표가
	// 거짓 양성이 된다.
	//
	// 그렇다고 같은 트랜잭션에 얹으면 롤백 때 함께 사라진다 — "무엇을 시도했다 실패했나"가
	// 감사 원장의 존재 이유이므로 그것도 안 된다.
	//
	// 그래서 버퍼에 모아 **커밋·롤백이 끝난 뒤** 별도 커넥션으로 흘린다. 두 성질을 다 지킨다.
	deferred []pendingEvent
}

type pendingEvent struct {
	kind      string
	project   string
	sessionID string
	payload   any
}

// LogEvent 는 트랜잭션이 끝난 뒤에 남길 계측 이벤트를 예약한다.
//
// **트랜잭션 안에서는 반드시 이것을 쓴다.** `Store.LogEvent` 를 그대로 부르면 교착한다.
// 롤백돼도 남는다 — 실패한 시도가 원장에서 사라지면 그 실패를 나중에 셀 수 없다.
func (t *Tx) LogEvent(kind, project, sessionID string, payload any) {
	t.deferred = append(t.deferred, pendingEvent{kind, project, sessionID, payload})
}

// Open 은 DB 를 열고 필요하면 마이그레이션을 적용한다.
func Open(path string) (*Store, error) {
	return OpenWithLogger(path, slog.Default())
}

// OpenWithLogger 는 Open 에 로거를 주입한다.
func OpenWithLogger(path string, log *slog.Logger) (*Store, error) {
	if log == nil {
		log = slog.Default()
	}
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("sqlite 열기 실패(path=%q): %w", path, err)
	}
	// 동시 커넥션 상한. WAL 이라 읽기는 서로 막지 않고, 쓰기는 immediate 트랜잭션이
	// busy_timeout 안에서 줄을 선다. 무제한으로 두면 파일 핸들만 늘고 이득이 없다.
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)

	if err := db.PingContext(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite 접속 실패(path=%q): %w", path, err)
	}

	s := &Store{db: db, path: path, log: log}

	// ★ DSN pragma 가 실제로 먹었는지 되읽어 확인한다.
	//   드라이버는 **모르는 pragma 이름을 조용히 무시한다**(실물로 확인: `_pragma=nonsense(1)` 이
	//   오류 없이 열린다). 그래서 "DSN 에 적었다"는 것은 "걸렸다"의 근거가 못 된다.
	//   foreign_keys 가 안 걸린 채로 도는 것은 FK 위반이 조용히 통과하는 최악의 경로다.
	if err := s.verifyPragmas(context.Background()); err != nil {
		db.Close()
		return nil, err
	}

	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// dsn 은 접속 문자열을 만든다.
//
// busy_timeout 은 스키마 주석(§2 의 `busy_timeout=5000`)과 같은 값이고,
// _txlock=immediate 는 Tx 주석의 실측 근거로 건다.
func dsn(path string) string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Set("_txlock", "immediate")
	return "file:" + path + "?" + q.Encode()
}

// wantPragmas 는 되읽기로 확인할 pragma 와 기대값이다.
var wantPragmas = map[string]string{
	"busy_timeout": "5000",
	"foreign_keys": "1",
	"journal_mode": "wal",
}

// CheckPragmas 는 되읽은 pragma 값들이 기대와 맞는지 판정한다.
//
// 불리언이 아니라 **어느 pragma 가 무슨 값이었는지**를 담은 오류를 돌려준다 —
// "안 걸렸다"만 알면 DSN 문법이 틀린 것인지 드라이버가 그 이름을 모르는 것인지 구분이 안 된다.
func CheckPragmas(got map[string]string) error {
	var bad []string
	for name, want := range wantPragmas {
		g, ok := got[name]
		if !ok {
			bad = append(bad, fmt.Sprintf("%s=<읽지 못함>(기대 %s)", name, want))
			continue
		}
		if !strings.EqualFold(g, want) {
			bad = append(bad, fmt.Sprintf("%s=%s(기대 %s)", name, g, want))
		}
	}
	if len(bad) == 0 {
		return nil
	}
	// 정렬 없이 map 순회라 순서가 흔들리지만, 사유 전문이 목적이라 순서는 무의미하다.
	return fmt.Errorf("DSN pragma 가 실제로 걸리지 않았다: %s "+
		"— 드라이버가 모르는 pragma 이름을 조용히 무시하므로 DSN 문법을 확인하라", strings.Join(bad, ", "))
}

func (s *Store) verifyPragmas(ctx context.Context) error {
	got := map[string]string{}
	for name := range wantPragmas {
		var v string
		// pragma 이름은 상수 map 의 키라 주입 경로가 없다(사용자 입력이 닿지 않는다).
		if err := s.db.QueryRowContext(ctx, "PRAGMA "+name).Scan(&v); err != nil {
			return fmt.Errorf("PRAGMA %s 되읽기 실패: %w", name, err)
		}
		got[name] = v
	}
	return CheckPragmas(got)
}

// Close 는 DB 를 닫는다.
func (s *Store) Close() error { return s.db.Close() }

// Path 는 DB 파일 경로다.
func (s *Store) Path() string { return s.path }

// DB 는 저수준 핸들이다. **시험과 진단 전용** — 일반 경로는 이걸 쓰지 않는다.
func (s *Store) DB() *sql.DB { return s.db }

// Tx 는 fn 을 한 트랜잭션 안에서 돌린다. fn 이 오류를 내면 롤백하고 그 오류를 그대로 올린다.
//
// 롤백 실패는 삼키지 않고 원래 오류에 합쳐 올린다 — 롤백이 실패하면 커넥션이 이상한 상태로
// 풀에 돌아가므로, 그 사실이 안 남으면 뒤따르는 무관한 실패의 원인을 영영 못 찾는다.
func (s *Store) Tx(ctx context.Context, fn func(*Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("트랜잭션 시작 실패: %w", err)
	}
	t := &Tx{tx: tx, ctx: ctx, s: s}
	if err := fn(t); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			// 예약 이벤트는 롤백 뒤에도 흘린다 — 실패한 시도가 원장에 남는 것이 목적이다.
			t.flushDeferred()
			return fmt.Errorf("%w (롤백도 실패: %v)", err, rbErr)
		}
		t.flushDeferred()
		return err
	}
	if err := tx.Commit(); err != nil {
		t.flushDeferred()
		return fmt.Errorf("커밋 실패: %w", err)
	}
	t.flushDeferred()
	return nil
}

// flushDeferred 는 예약된 계측 이벤트를 별도 커넥션으로 흘린다.
// 트랜잭션이 끝난 뒤에만 불린다 — 그 전에 부르면 쓰기 잠금 때문에 교착한다.
func (t *Tx) flushDeferred() {
	for _, e := range t.deferred {
		t.s.LogEvent(t.ctx, e.kind, e.project, e.sessionID, e.payload)
	}
	t.deferred = nil
}

// Ctx 는 이 트랜잭션이 물고 있는 컨텍스트다.
func (t *Tx) Ctx() context.Context { return t.ctx }

// ─────────────────────────────────────────────────────────────────────────────
// 마이그레이션 — 판정은 순수 함수, 실행만 여기
// ─────────────────────────────────────────────────────────────────────────────
//
// ⚠ **설계 §7 과 어긋난 자리다. 다음 세션이 "설계대로 돼 있다"고 믿지 않게 여기 적는다.**
//
// §7 의 "나쁜 스키마 변경으로 크래시루프" 행은 처방을 셋으로 적었다:
//
//	① 마이그레이션을 **별도 one-shot 컨테이너로 분리**
//	② 기동 전 DB 백업
//	③ 롤백 명령
//
// 지금 구현이 만족하는 것은 **②뿐이다.**
//
//   - ①이 없다: 적용이 Open() 안에 있고 Open() 은 `fd serve` 기동 경로다. 그래서 나쁜 증분은
//     "서버가 안 뜬다"로 나타나고, 그때 고칠 수단도 같은 바이너리를 다시 띄우는 것뿐이다.
//   - ③이 없다: 되돌리는 서브명령이 없다. 되돌릴 길은 백업 파일 손 복사뿐이다.
//
// **지금 이 구조를 유지하기로 한 판단과 근거**(2026-08-03):
// 증분이 한 단(002)이고 순수 가산이라 실질 위험이 낮다. 반면 적용을 기동에서 떼면
// **모든 명령**(fail-open 훅 4종 포함)이 "스키마가 아직 안 올라간 DB" 를 만나는 새 경로가 생기고,
// 훅은 정의상 조용히 죽으므로 그 경로의 실패가 침묵한다. 제거하는 위험보다 새로 만드는 위험이 크다.
//
// **이 판단이 뒤집히는 조건**: 증분이 파괴적(컬럼 삭제·타입 변경·데이터 이행)이 되는 순간.
// 그때는 `fd migrate [--to N]` / `fd migrate --rollback` 으로 적용을 기동에서 분리한다.
// 그 전까지 ③의 자리는 RollbackHint 가 **문구로** 메운다 — 명령이 없다면 적어도
// 절차가 실패한 그 자리에 있어야 한다.

// RollbackHint 는 마이그레이션이 깨졌을 때 되돌리는 절차를 낸다. 순수 함수다.
//
// ★ 이 문구가 오류와 로그에 실린다. 앞선 판은 업그레이드 경로에서 백업 경로를
// **지역 변수로 버려서**, 정확히 §7 이 겨냥한 상황(다음 증분이 깨지는 날)에
// 오류가 "무엇을 얹다 실패했다"만 말하고 **어디로 되돌리는지는 말하지 못했다.**
// 적용 경로(MigrateApply)는 backup=%q 를 실었는데 업그레이드 경로는 안 실었다 —
// 같은 상황을 다루는 두 경로가 다르게 생겼으면 한쪽이 틀린 것이다.
//
// -wal·-shm 을 함께 지우라고 적는 이유: 백업은 VACUUM INTO 로 뜬 **독립된 일관 사본**이라
// WAL 이 딸려 있지 않다. 옛 -wal 을 남긴 채 .db 만 갈아 끼우면 SQLite 가 그 WAL 을
// 되살려 얹고, 그러면 되돌렸다고 믿는 순간 반쯤 적용된 상태가 부활한다.
func RollbackHint(dbPath, backupPath string) string {
	if strings.TrimSpace(backupPath) == "" {
		return "이번 기동은 백업을 뜨지 않았다(빈 DB 이거나 메모리 DB 다) — 되돌릴 파일이 없다. " +
			"옛 백업은 " + clip(dbPath, 200) + ".bak-* 에 있다."
	}
	db := clip(dbPath, 200)
	return fmt.Sprintf("되돌리려면 서버를 멈추고: cp -f %q %q && rm -f %q %q "+
		"(백업은 VACUUM INTO 로 뜬 일관 사본이라 WAL 이 없다 — 옛 -wal 을 남기면 "+
		"반쯤 적용된 상태가 되살아난다). "+
		"적용을 기동에서 떼는 별도 one-shot 단계와 되돌리기 서브명령은 아직 없다(설계 §7 대비 미구현).",
		clip(backupPath, 200), db, db+"-wal", db+"-shm")
}

// MigrationAction 은 여는 시점에 무엇을 할지다.
type MigrationAction string

const (
	MigrateNone    MigrationAction = "none"    // 이미 맞다
	MigrateApply   MigrationAction = "apply"   // 스키마를 새로 적용한다(그 뒤 증분이 따라온다)
	MigrateUpgrade MigrationAction = "upgrade" // 기존 DB 에 증분을 얹는다
	MigrateReject  MigrationAction = "reject"  // 열지 않는다
)

// MigrationPlan 은 판정 결과다. Reason 은 **항상** 채운다 —
// 사유가 없으면 "조건 때문에 거절"과 "이 축을 안 본다"가 구분되지 않는다.
type MigrationPlan struct {
	Action MigrationAction
	Backup bool
	Reason string
}

// PlanMigration 은 DB 의 현재 상태만 보고 무엇을 할지 정한다. 순수 함수다.
//
//   - hasVersionTable: schema_version 테이블이 있는가
//   - dbVersion:       그 표의 MAX(version). 표가 없거나 행이 없으면 0
//   - objectCount:     sqlite_master 의 객체 수. 0 이면 완전히 빈 파일이다
//   - codeVersion:     이 바이너리가 아는 버전(SchemaVersion)
//
// 백업 판정을 여기에 둔 이유: "언제 백업하는가"가 조건 넷의 조합이라
// 본문에 흩어지면 시험이 그 조합의 사본을 단정하게 된다.
func PlanMigration(hasVersionTable bool, dbVersion, objectCount, codeVersion int) MigrationPlan {
	switch {
	case !hasVersionTable && objectCount == 0:
		return MigrationPlan{
			Action: MigrateApply, Backup: false,
			Reason: "빈 DB 다 — 스키마를 새로 적용한다(잃을 것이 없으므로 백업하지 않는다)",
		}

	case !hasVersionTable && objectCount > 0:
		// 여기서 스키마를 덮어 적용하면 남의 DB 를 망가뜨리거나, 중간에 끊긴
		// 마이그레이션 위에 또 얹게 된다. 둘 다 조용히 망가지는 경로다.
		return MigrationPlan{
			Action: MigrateReject, Backup: false,
			Reason: fmt.Sprintf("schema_version 표가 없는데 객체가 %d개 있다 "+
				"— flightdeck 의 DB 가 아니거나 마이그레이션이 중간에 끊긴 것이다. 손으로 확인하라", objectCount),
		}

	case dbVersion == 0 && objectCount <= 1:
		// 표는 있는데 행이 없고, 그 표 말고는 아무것도 없다 =
		// 앞선 적용이 schema_version 만 만들고 곧바로 끊겼다. 안전하게 다시 적용할 수 있다.
		// 파일이 이미 있으므로 백업은 뜬다(비용이 거의 없고, 여기서 안 뜨면 백업 경로가 통째로 죽는다).
		return MigrationPlan{
			Action: MigrateApply, Backup: true,
			Reason: "schema_version 표만 있고 기록된 버전이 없다 — 앞선 적용이 곧바로 끊긴 것으로 보고 다시 적용한다",
		}

	case dbVersion == 0:
		// ★ 객체는 있는데 버전 기록이 없다. **다시 적용하면 안 된다** —
		//   schema.sql 은 멱등이 아니다(IF NOT EXISTS 는 schema_version 하나뿐이라
		//   재적용이 "table already exists" 로 죽고, 그 시점의 DB 는 반쯤 적용된 채 남는다).
		//   조용히 시도해 실패하는 것보다 사유를 말하고 멈추는 쪽이 복구 가능하다.
		return MigrationPlan{
			Action: MigrateReject, Backup: false,
			Reason: fmt.Sprintf("스키마 객체가 %d개 있는데 schema_version 에 기록된 버전이 없다 "+
				"— schema.sql 은 멱등이 아니라 재적용이 실패한다. 앞선 마이그레이션이 중간에 끊긴 것이므로 "+
				"백업(<db>.bak-*)에서 되돌리거나 손으로 버전을 기록하라", objectCount),
		}

	case dbVersion > codeVersion:
		// ★ 구 바이너리가 신 DB 를 여는 것. 조용히 열면 모르는 컬럼·제약 위에서 돌게 된다.
		return MigrationPlan{
			Action: MigrateReject, Backup: false,
			Reason: fmt.Sprintf("DB 스키마 버전이 %d 인데 이 바이너리는 %d 까지만 안다 "+
				"— 구 바이너리로 신 DB 를 여는 것은 조용히 망가지는 경로라 거절한다. 바이너리를 올려라",
				dbVersion, codeVersion),
		}

	case dbVersion < codeVersion:
		// ★ 여기서 경로 유무를 판정하지 않는다 — 그것은 증분 목록을 봐야 알 수 있어
		//   이 함수가 순수하지 않게 된다. 실제 경로 해석은 UpgradeSteps 가 하고,
		//   빠진 단이 있으면 **거기서** 사유를 담아 거절한다.
		//   백업은 무조건 뜬다: 나쁜 마이그레이션은 1인 운영에서 복구 불가 사건이다.
		return MigrationPlan{
			Action: MigrateUpgrade, Backup: true,
			Reason: fmt.Sprintf("DB 스키마 버전이 %d 이고 이 바이너리는 %d 를 기대한다 "+
				"— %d→%d 증분을 얹는다(적용 전에 백업한다)", dbVersion, codeVersion, dbVersion, codeVersion),
		}

	default:
		return MigrationPlan{
			Action: MigrateNone, Backup: false,
			Reason: fmt.Sprintf("스키마 버전 %d 로 이미 맞다", dbVersion),
		}
	}
}

// UpgradeSteps 는 from 에서 to 까지 얹을 증분을 순서대로 고른다. 순수 함수다.
//
// ★ 한 단이라도 빠지면 **사유를 담아 거절한다.** 조용히 건너뛰면 "모르는 구 스키마 위에서
// 도는" 상태가 침묵으로 열리고, 그 침묵은 그 표를 처음 읽는 질의가 죽을 때까지 안 보인다.
// 사유에 무엇이 있는지까지 적는 이유: "1→3 경로가 없다"만으로는 2가 없는 것인지
// 3이 없는 것인지 운영자가 못 가린다.
func UpgradeSteps(from, to int, avail []Migration) ([]Migration, error) {
	if from > to {
		return nil, fmt.Errorf("내려가는 마이그레이션은 없다(%d→%d)", from, to)
	}
	have := map[int]Migration{}
	var haveList []int
	for _, m := range avail {
		have[m.To] = m
		haveList = append(haveList, m.To)
	}
	var out []Migration
	for v := from + 1; v <= to; v++ {
		m, ok := have[v]
		if !ok {
			return nil, fmt.Errorf("%d→%d 업그레이드 경로가 없다 — %d 로 올리는 증분이 없다(가진 증분: %v)",
				from, to, v, haveList)
		}
		out = append(out, m)
	}
	return out, nil
}

// applyUpgrades 는 증분을 순서대로 얹는다. **한 단이 한 트랜잭션이다** —
// 중간에 끊겨도 얹힌 단까지는 버전 기록과 실제 스키마가 일치하고,
// 그러면 다음 기동이 남은 단부터 이어서 얹는다.
func (s *Store) applyUpgrades(ctx context.Context, from int) error {
	steps, err := UpgradeSteps(from, SchemaVersion, migrations)
	if err != nil {
		s.log.Error("업그레이드 경로가 없어 DB 를 열지 않는다",
			"path", s.path, "db_version", from, "code_version", SchemaVersion, "error", err.Error())
		return fmt.Errorf("스키마 거절: %w", err)
	}
	for _, m := range steps {
		start := time.Now()
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("증분 %d(%s) 트랜잭션 시작 실패(path=%q): %w", m.To, m.Name, s.path, err)
		}
		if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
			if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
				return fmt.Errorf("증분 %d(%s) 적용 실패(path=%q): %w (롤백도 실패: %v)",
					m.To, m.Name, s.path, err, rbErr)
			}
			return fmt.Errorf("증분 %d(%s) 적용 실패(path=%q): %w", m.To, m.Name, s.path, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_version(version, applied_at) VALUES (?, ?)`,
			m.To, fmtTime(time.Now())); err != nil {
			if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
				return fmt.Errorf("증분 %d(%s) 버전 기록 실패(path=%q): %w (롤백도 실패: %v)",
					m.To, m.Name, s.path, err, rbErr)
			}
			return fmt.Errorf("증분 %d(%s) 버전 기록 실패(path=%q): %w", m.To, m.Name, s.path, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("증분 %d(%s) 커밋 실패(path=%q): %w", m.To, m.Name, s.path, err)
		}
		s.log.Info("증분 적용 완료",
			"path", s.path, "version", m.To, "reason", m.Name,
			"duration", time.Since(start).Seconds())
	}
	return nil
}

func (s *Store) migrate(ctx context.Context) error {
	hasTable, err := s.hasTable(ctx, "schema_version")
	if err != nil {
		return err
	}
	var dbVersion int
	if hasTable {
		var v sql.NullInt64
		if err := s.db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_version`).Scan(&v); err != nil {
			return fmt.Errorf("schema_version 읽기 실패: %w", err)
		}
		if v.Valid {
			dbVersion = int(v.Int64)
		}
	}
	var objects int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type IN ('table','index','trigger','view')`,
	).Scan(&objects); err != nil {
		return fmt.Errorf("sqlite_master 읽기 실패: %w", err)
	}

	plan := PlanMigration(hasTable, dbVersion, objects, SchemaVersion)

	switch plan.Action {
	case MigrateNone:
		s.log.Info("스키마 확인", "path", s.path, "version", dbVersion, "reason", plan.Reason)
		return nil

	case MigrateReject:
		// 열지 못한 사유는 원인 전문으로 남긴다. 이 오류가 곧 운영자가 볼 유일한 단서다.
		s.log.Error("스키마 버전 불일치로 DB 를 열지 않는다",
			"path", s.path, "db_version", dbVersion, "code_version", SchemaVersion, "reason", plan.Reason)
		return fmt.Errorf("스키마 거절: %s", plan.Reason)

	case MigrateApply:
		start := time.Now()
		s.log.Info("마이그레이션 시작",
			"path", s.path, "from", dbVersion, "to", SchemaVersion, "backup", plan.Backup, "reason", plan.Reason)

		var backupPath string
		if plan.Backup {
			// ★ 나쁜 마이그레이션은 1인 운영에서 복구 불가 사건이다. 적용 **전에** 뜬다.
			backupPath, err = s.backup(ctx)
			if err != nil {
				return err
			}
			s.log.Info("마이그레이션 전 백업 완료", "path", s.path, "backup", backupPath)
		}

		// ★ 트랜잭션 밖에서 돌린다. schema.sql 안에 PRAGMA journal_mode 가 있고
		//   그것은 트랜잭션 안에서 못 돈다. 중간에 끊기면 남는 것은 위 백업뿐이고,
		//   그 상태는 PlanMigration 이 다음 기동에서 dbVersion==0 으로 잡아 다시 적용한다.
		if _, err := s.db.ExecContext(ctx, schemaSQL); err != nil {
			s.log.Error("스키마 적용 실패 — 되돌리는 절차를 함께 낸다",
				"path", s.path, "reason", RollbackHint(s.path, backupPath), "error", err.Error())
			return fmt.Errorf("스키마 적용 실패(path=%q): %w — %s", s.path, err, RollbackHint(s.path, backupPath))
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO schema_version(version, applied_at) VALUES (?, ?)`,
			BaseSchemaVersion, fmtTime(time.Now()),
		); err != nil {
			return fmt.Errorf("schema_version 기록 실패(스키마는 적용됨, path=%q): %w", s.path, err)
		}
		// ★ 신규 DB 도 증분을 그대로 탄다. 신규용으로 schema.sql 에 같은 표를 또 적으면
		//   정의가 두 벌이 되고, 신규 설치와 업그레이드가 다른 모양의 DB 를 갖게 된다.
		if err := s.applyUpgrades(ctx, BaseSchemaVersion); err != nil {
			return s.rollbackable(err, backupPath)
		}
		s.log.Info("마이그레이션 완료",
			"path", s.path, "version", SchemaVersion, "duration", time.Since(start).Seconds())
		return nil

	case MigrateUpgrade:
		start := time.Now()
		s.log.Info("마이그레이션 시작",
			"path", s.path, "from", dbVersion, "to", SchemaVersion, "backup", plan.Backup, "reason", plan.Reason)
		// ★ backupPath 를 블록 밖에 둔다. 앞선 판은 이 자리에서 `:=` 로 선언해 **버렸고**,
		//   그래서 아래 실패가 어디로 되돌리는지 말하지 못했다 —
		//   정확히 설계 §7 이 겨냥한 상황에서 유일한 탈출구의 좌표가 사라진 것이다.
		var backupPath string
		if plan.Backup {
			backupPath, err = s.backup(ctx)
			if err != nil {
				return err
			}
			s.log.Info("마이그레이션 전 백업 완료", "path", s.path, "backup", backupPath)
		}
		if err := s.applyUpgrades(ctx, dbVersion); err != nil {
			return s.rollbackable(err, backupPath)
		}
		s.log.Info("마이그레이션 완료",
			"path", s.path, "version", SchemaVersion, "duration", time.Since(start).Seconds())
		return nil

	default:
		return fmt.Errorf("알 수 없는 마이그레이션 판정: %q (%s)", plan.Action, plan.Reason)
	}
}

// rollbackable 은 마이그레이션 실패에 **되돌리는 절차**를 붙인다.
//
// 두 경로(신규 적용·증분 업그레이드)가 같은 문구를 쓰게 하려고 헬퍼로 뺐다 —
// 두 벌로 두면 한쪽만 고쳐지고, 그 비대칭이 다시 "어디로 되돌리나"를 지운다.
func (s *Store) rollbackable(err error, backupPath string) error {
	hint := RollbackHint(s.path, backupPath)
	s.log.Error("마이그레이션 실패 — 되돌리는 절차를 함께 낸다",
		"path", s.path, "reason", hint, "error", err.Error())
	return fmt.Errorf("%w — %s", err, hint)
}

func (s *Store) hasTable(ctx context.Context, name string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("테이블 존재 확인 실패(name=%q): %w", name, err)
	}
	return n > 0, nil
}

// BackupSuffix 는 백업 파일 이름의 꼬리를 만든다. 사전순 = 시간순이다.
func BackupSuffix(at time.Time) string {
	return ".bak-" + at.UTC().Format("20060102T150405Z")
}

// backup 은 마이그레이션 직전 스냅숏을 뜬다.
//
// 파일을 그냥 복사하지 않고 VACUUM INTO 를 쓴다 — WAL 모드에서는 .db 파일만 베끼면
// -wal 에 남은 커밋이 빠져 **조용히 낡은 사본**이 된다. VACUUM INTO 는 그 시점의
// 일관된 DB 하나를 만든다.
func (s *Store) backup(ctx context.Context) (string, error) {
	if s.path == "" || strings.Contains(s.path, ":memory:") {
		return "", nil // 메모리 DB 는 뜰 것이 없다. 무해한 무시라 여기 적는다
	}
	dst := s.path + BackupSuffix(time.Now())
	if _, err := os.Stat(dst); err == nil {
		// VACUUM INTO 는 대상이 있으면 실패한다. 같은 초에 두 번 열린 것이므로 이름을 벌린다.
		dst = fmt.Sprintf("%s-%d", dst, os.Getpid())
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", fmt.Errorf("백업 디렉토리 생성 실패(dir=%q): %w", filepath.Dir(dst), err)
	}
	// VACUUM INTO 는 파라미터 바인딩을 안 받는다. 경로는 사용자 입력이 아니라
	// 서버 설정에서 온 값이고, 작은따옴표만 이스케이프하면 리터럴로 안전하다.
	q := "VACUUM INTO '" + strings.ReplaceAll(dst, "'", "''") + "'"
	if _, err := s.db.ExecContext(ctx, q); err != nil {
		return "", fmt.Errorf("마이그레이션 전 백업 실패(dst=%q): %w", dst, err)
	}
	return dst, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// 잡동사니
// ─────────────────────────────────────────────────────────────────────────────

func fmtTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// nowStamp 는 **저장 표기와 정확히 같은 해상도**의 지금 시각이다.
//
// 함수가 시각을 스스로 만들고 그것을 구조체에 담아 돌려줄 때 반드시 이걸 쓴다.
// time.Now() 를 그대로 담으면 반환값(로컬 시간대·나노초)과 다시 읽은 값
// (UTC·마이크로초)이 달라서, "방금 만든 것"과 "다시 읽은 것"의 비교가 조용히 틀어진다.
// 재개 판정이 정확히 그 비교다.
func nowStamp() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("시각 해석 실패(%q): %w", clip(s, 64), err)
	}
	return t.UTC(), nil
}

func parseNullTime(ns sql.NullString) (*time.Time, error) {
	if !ns.Valid || ns.String == "" {
		return nil, nil
	}
	t, err := parseTime(ns.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// nullStr 은 빈 문자열을 NULL 로 바꾼다.
// 빈 문자열을 그대로 넣으면 FK 가 "" 라는 없는 키를 찾다 실패하고,
// 그 실패는 "값을 안 준 것"과 구분되지 않는다.
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func str(ns sql.NullString) string {
	if !ns.Valid {
		return ""
	}
	return ns.String
}

// clip 은 오류·로그에 실을 외부 문자열을 자르고 제어문자를 걷어낸다.
// 로그 주입과 무한장 오류 메시지를 막는다.
func clip(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n]) + "…"
}
