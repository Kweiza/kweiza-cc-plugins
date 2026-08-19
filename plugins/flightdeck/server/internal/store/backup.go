package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
)

// 판단 원장 내보내기 — `fd export --judgments` 의 저장 계층.
//
// ★ 이름을 "백업"이라고 하지 않는다. 이 패키지에서 backup·BackupSuffix·<db>.bak-* 는
// 이미 마이그레이션 직전 VACUUM INTO 로 뜨는 **DB 파일 사본**을 뜻한다. 두 개념이
// 같은 낱말을 쓰면 오류 문구와 로그에서 섞인다.
//
// ★ model 을 거치지 않는다. nullStr/str 이 NULL 과 빈 문자열을 접기 때문이다.
// 그 접힘이 왕복으로 닫히는지는 "빈 문자열이 저장된 행이 원리적으로 없다"는 별도 논증에
// 의존하는데, 원문 DTO 로 가면 그 논증 자체가 필요 없다. 포인터가 nil 이면 NULL 이다.

// LedgerJudgment 는 judgment 표 한 행의 원문이다.
type LedgerJudgment struct {
	ID         string  `json:"id"`
	Project    *string `json:"project"`
	SessionID  *string `json:"session_id"`
	At         string  `json:"at"` // DB 원문 문자열(폭 고정 마이크로초). time.Time 으로 접지 않는다
	Kind       string  `json:"kind"`
	Title      *string `json:"title"`
	Body       string  `json:"body"`
	Supersedes *string `json:"supersedes"`
}

// LedgerLink 는 judgment_link 표 한 행이다.
type LedgerLink struct {
	JudgmentID string `json:"judgment_id"`
	TargetKind string `json:"target_kind"`
	TargetID   string `json:"target_id"`
	// ★ NULL 은 "판단 자신의 프로젝트"다 — 증분 009 이전의 링크가 전부 그 모양이라
	//   *string 이어야 한다. string 으로 받으면 NULL 과 명시된 빈 값이 같아지고,
	//   왕복 복원이 옛 행에 빈 문자열을 써 넣어 **어느 프로젝트와도 안 맞는 링크**로
	//   바꿔 놓는다(교차 링크가 복구 경로에서 다시 죽는 자리).
	TargetProject *string `json:"target_project"`
}

// LedgerSnapshot 은 snapshot 표 한 행의 원문이다.
type LedgerSnapshot struct {
	Project     string  `json:"project"`
	Key         string  `json:"key"`
	Value       string  `json:"value"`
	Method      string  `json:"method"`
	Evidence    *string `json:"evidence"`
	InputDigest *string `json:"input_digest"`
	ComputedAt  string  `json:"computed_at"`
}

// LedgerMachine 은 machine 표 한 행의 원문이다. NULL 가능 컬럼이 없다.
type LedgerMachine struct {
	ID        string `json:"id"`
	Hostname  string `json:"hostname"`
	FirstSeen string `json:"first_seen"`
	LastSeen  string `json:"last_seen"`
}

// LedgerProject 는 project 표 한 행의 원문이다.
//
// ★ PinnedAt·ArchivedAt 도 *string 이다(원장은 값 원문을 그대로 나르지, 파싱해서 time.Time
// 으로 되살리지 않는다 — RemoteURL·Config·ConfigFromSHA 와 같은 이유). 증분 007 이 더한
// 두 컬럼이 nullable TEXT 라 이 표의 NULL 가능 필드가 셋에서 다섯으로 늘었다.
type LedgerProject struct {
	ID            string  `json:"id"`
	Path          string  `json:"path"`
	RemoteURL     *string `json:"remote_url"`
	DefaultBranch string  `json:"default_branch"`
	Config        *string `json:"config"`
	ConfigFromSHA *string `json:"config_from_sha"`
	CreatedAt     string  `json:"created_at"`
	PinnedAt      *string `json:"pinned_at"`
	ArchivedAt    *string `json:"archived_at"`
}

// LedgerSession 은 session 표 한 행의 원문이다.
//
// ★ 이 표가 원장에 있는 이유. session.id 는 서버 발급 ULID 라 같은 3중키로 다시 열어도
// 새 값이 나온다 — project·machine 처럼 "이름을 다시 부르면 같은 것"이 아니다.
// 판단의 85%(실측 973/1141)가 session_id 를 갖고, 복원이 한 트랜잭션이라 그 FK 가 하나만
// 깨져도 판단 전체가 롤백된다.
type LedgerSession struct {
	ID          string  `json:"id"`
	Project     string  `json:"project"`
	MachineID   string  `json:"machine_id"`
	Worktree    string  `json:"worktree"`
	CCSessionID string  `json:"cc_session_id"`
	Label       *string `json:"label"`
	State       string  `json:"state"`
	BlockedWhy  *string `json:"blocked_why"`
	OpenedAt    string  `json:"opened_at"`
}

// LedgerDump 는 한 순간의 FK 폐포 전량이다.
//
// 여섯 표다. 앞 셋(machine·project·session)이 뒤 셋의 FK 대상이고, machine·project 는
// 아무것도 참조하지 않는 leaf 라 폐포가 여기서 닫힌다.
type LedgerDump struct {
	Machines  []LedgerMachine
	Projects  []LedgerProject
	Sessions  []LedgerSession
	Judgments []LedgerJudgment
	Links     []LedgerLink
	Snapshots []LedgerSnapshot
}

// linkCols 는 judgment_link 표의 컬럼이다. 다른 다섯과 달리 이 표에는 조회 접근자가 없어
// 상수가 없었고, 읽기와 쓰기가 각자 리터럴을 들고 있었다.
const linkCols = `judgment_id, target_kind, target_id, target_project`

// ledgerTables 는 판단 원장이 담는 FK 폐포 여섯 표다 — **표마다 컬럼 목록이 하나다.**
//
// 순서는 되쓰기 순서다: machine·project·session 이 판단보다 먼저 들어가야 FK 가 닫힌다
// (WriteLedger 의 그 주석 참고).
//
// ★ 왜 한 자리에 모으나. 예전에는 읽기가 상수를, 쓰기가 인라인 리터럴을 써서 같은 표에
// 목록이 **둘**이었다. 스키마에 컬럼이 하나 들어오면 백업이 그것을 버리고(읽기 목록에
// 없다) 복원도 버리는데(쓰기 목록에 없다), 왕복 시험은 want·final 이 둘 다 ReadLedger
// 산출물이라 원리적으로 못 본다. 목록이 둘이면 관문도 한쪽만 재게 되고, **재지 않은
// 쪽이 정확히 그 조용한 손실 자리**가 된다.
var ledgerTables = []struct {
	name string
	cols string
}{
	{"machine", machineCols},
	{"project", projectCols},
	{"session", sessionCols},
	{"judgment", judgmentCols},
	{"judgment_link", linkCols},
	{"snapshot", snapshotCols},
}

// LedgerTableNames 는 판단 원장이 담는 폐포 표 이름들이다(선언 순서 = 되쓰기 순서).
//
// ★ 왜 내보내나. **복원 결과를 설명하는 쪽**(ledger.Losses)이 "무엇이 복원되는가"를 손으로
// 적으면 폐포가 바뀔 때 그 설명이 조용히 거짓이 된다. 실제로 그랬다 — session 이 폐포에
// 들어온 뒤에도 손실 목록은 세션을 가리키는 링크를 손실로 부르고 있었고, 그 링크가
// 실측 34%였다.
func LedgerTableNames() []string {
	out := make([]string, 0, len(ledgerTables))
	for _, e := range ledgerTables {
		out = append(out, e.name)
	}
	return out
}

// ledgerColNames 는 컬럼 목록 문자열을 이름 슬라이스로 가른다. 순수 함수다.
func ledgerColNames(cols string) []string {
	var out []string
	for _, c := range strings.Split(cols, ",") {
		if c = strings.TrimSpace(c); c != "" {
			out = append(out, c)
		}
	}
	return out
}

// ledgerInsert 는 표 하나의 되쓰기 문장이다. 순수 함수다.
//
// ★ `?` 를 손으로 안 적는다. 컬럼을 더한 사람이 목록·자리표·인자 셋을 다 고쳐야 하는데
// 자리표를 빠뜨리면 런타임에서야 죽는다 — 상수에서 세면 그 자리가 원리적으로 사라진다.
func ledgerInsert(name, cols string) string {
	n := len(ledgerColNames(cols))
	return `INSERT INTO ` + name + `(` + cols + `) VALUES (` +
		strings.TrimSuffix(strings.Repeat("?, ", n), ", ") + `)`
}

// ReadLedger 는 여섯 표(FK 폐포 전량)를 **한 트랜잭션 안에서** 읽는다.
//
// ★ 왜 트랜잭션인가. 표를 따로 읽으면 그 사이 서버가 커밋한 판단의 **링크만** 산출물에
// 들어간다(judgment 를 읽은 뒤 link 를 읽으므로). 그 링크가 가리키는 판단이 없으니
// 되읽기가 FK 로 죽는다. 트랜잭션 원자성은 DB 상태를 보장하지 서로 다른 시점의 두 읽기를
// 보장하지 않는다 — 동시 세션이 스물이 넘는 이 저장소에서 실제로 열리는 창이다.
//
// ★ 왜 Store.Tx 를 안 쓰는가. 그 함수는 DSN 의 _txlock=immediate 때문에 BEGIN IMMEDIATE 라
// 읽기만 해도 쓰기 잠금을 잡는다(Tx 주석이 그 대가를 적어 뒀다). 전량 읽기가 도는 동안
// 서버의 모든 쓰기가 busy_timeout 안에서 줄을 선다. 여기서는 BeginTx 를 직접 부르고,
// 백업 전용 열기(OpenLedger)가 DSN 을 deferred 로 준다. 기본 DSN 으로 열린 Store 에서
// 불러도 정확성은 같고(스냅숏 일관성은 유지된다) 잠금만 세진다.
//
// ★ 상한이 없다. ListJudgmentsByKind 는 limit<=0 을 50 으로 바꾸고, legacy 되쓰기는
// 100000 이라는 수를 손으로 넣는다 — 상한에 걸려 조용히 잘리면 산출물이 원본보다
// 적어지고 그 차이는 세어 보기 전에는 안 보인다. 원장은 전량이 목적이라 WHERE 절도 없다.
func (s *Store) ReadLedger(ctx context.Context) (LedgerDump, error) {
	var d LedgerDump
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return d, fmt.Errorf("원장 읽기 트랜잭션 시작 실패: %w", err)
	}
	// 읽기만 하므로 언제나 롤백으로 끝낸다. 커밋할 것이 없다.
	defer func() { _ = tx.Rollback() }()

	if d.Machines, err = readLedgerMachines(ctx, tx); err != nil {
		return d, err
	}
	if d.Projects, err = readLedgerProjects(ctx, tx); err != nil {
		return d, err
	}
	if d.Sessions, err = readLedgerSessions(ctx, tx); err != nil {
		return d, err
	}
	if d.Judgments, err = readLedgerJudgments(ctx, tx); err != nil {
		return d, err
	}
	if d.Links, err = readLedgerLinks(ctx, tx); err != nil {
		return d, err
	}
	if d.Snapshots, err = readLedgerSnapshots(ctx, tx); err != nil {
		return d, err
	}
	return d, nil
}

// readLedgerJudgments 는 판단 전량을 id 순으로 읽는다.
// id 는 ULID 라 정렬이 곧 생성순이고, 같은 DB 면 같은 바이트가 나오는 근거가 이것이다.
func readLedgerJudgments(ctx context.Context, q dbtx) ([]LedgerJudgment, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+judgmentCols+` FROM judgment ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("원장 판단 조회 실패: %w", err)
	}
	defer rows.Close()

	var out []LedgerJudgment
	for rows.Next() {
		var j LedgerJudgment
		var project, session, title, supersedes sql.NullString
		if err := rows.Scan(&j.ID, &project, &session, &j.At, &j.Kind, &title, &j.Body, &supersedes); err != nil {
			return nil, fmt.Errorf("원장 판단 행 해석 실패: %w", err)
		}
		j.Project, j.SessionID = ptrOf(project), ptrOf(session)
		j.Title, j.Supersedes = ptrOf(title), ptrOf(supersedes)
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("원장 판단 순회 실패: %w", err)
	}
	return out, nil
}

// readLedgerLinks 는 링크 전량을 읽는다.
// 정렬 셋은 PK 와 같은 순서다 — 재현성을 위해 완전 순서여야 한다.
func readLedgerLinks(ctx context.Context, q dbtx) ([]LedgerLink, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT `+linkCols+` FROM judgment_link
		ORDER BY judgment_id, target_kind, target_id`)
	if err != nil {
		return nil, fmt.Errorf("원장 링크 조회 실패: %w", err)
	}
	defer rows.Close()

	var out []LedgerLink
	for rows.Next() {
		var l LedgerLink
		if err := rows.Scan(&l.JudgmentID, &l.TargetKind, &l.TargetID, &l.TargetProject); err != nil {
			return nil, fmt.Errorf("원장 링크 행 해석 실패: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("원장 링크 순회 실패: %w", err)
	}
	return out, nil
}

// readLedgerSnapshots 는 스냅숏 전량을 읽는다. PK 와 같은 (project, key) 순이다.
func readLedgerSnapshots(ctx context.Context, q dbtx) ([]LedgerSnapshot, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT `+snapshotCols+` FROM snapshot ORDER BY project, key`)
	if err != nil {
		return nil, fmt.Errorf("원장 스냅숏 조회 실패: %w", err)
	}
	defer rows.Close()

	var out []LedgerSnapshot
	for rows.Next() {
		var sn LedgerSnapshot
		var evidence, digest sql.NullString
		if err := rows.Scan(&sn.Project, &sn.Key, &sn.Value, &sn.Method,
			&evidence, &digest, &sn.ComputedAt); err != nil {
			return nil, fmt.Errorf("원장 스냅숏 행 해석 실패: %w", err)
		}
		sn.Evidence, sn.InputDigest = ptrOf(evidence), ptrOf(digest)
		out = append(out, sn)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("원장 스냅숏 순회 실패: %w", err)
	}
	return out, nil
}

// readLedgerMachines 는 머신 전량을 id 순으로 읽는다.
func readLedgerMachines(ctx context.Context, q dbtx) ([]LedgerMachine, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+machineCols+` FROM machine ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("원장 머신 조회 실패: %w", err)
	}
	defer rows.Close()

	var out []LedgerMachine
	for rows.Next() {
		var m LedgerMachine
		if err := rows.Scan(&m.ID, &m.Hostname, &m.FirstSeen, &m.LastSeen); err != nil {
			return nil, fmt.Errorf("원장 머신 행 해석 실패: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("원장 머신 순회 실패: %w", err)
	}
	return out, nil
}

// readLedgerProjects 는 프로젝트 전량을 id 순으로 읽는다.
func readLedgerProjects(ctx context.Context, q dbtx) ([]LedgerProject, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+projectCols+` FROM project ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("원장 프로젝트 조회 실패: %w", err)
	}
	defer rows.Close()

	var out []LedgerProject
	for rows.Next() {
		var p LedgerProject
		var remote, config, fromSHA, pinned, archived sql.NullString
		if err := rows.Scan(&p.ID, &p.Path, &remote, &p.DefaultBranch,
			&config, &fromSHA, &p.CreatedAt, &pinned, &archived); err != nil {
			return nil, fmt.Errorf("원장 프로젝트 행 해석 실패: %w", err)
		}
		p.RemoteURL, p.Config, p.ConfigFromSHA = ptrOf(remote), ptrOf(config), ptrOf(fromSHA)
		p.PinnedAt, p.ArchivedAt = ptrOf(pinned), ptrOf(archived)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("원장 프로젝트 순회 실패: %w", err)
	}
	return out, nil
}

// readLedgerSessions 는 세션 전량을 id 순으로 읽는다.
func readLedgerSessions(ctx context.Context, q dbtx) ([]LedgerSession, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+sessionCols+` FROM session ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("원장 세션 조회 실패: %w", err)
	}
	defer rows.Close()

	var out []LedgerSession
	for rows.Next() {
		var x LedgerSession
		var label, blockedWhy sql.NullString
		if err := rows.Scan(&x.ID, &x.Project, &x.MachineID, &x.Worktree, &x.CCSessionID,
			&label, &x.State, &blockedWhy, &x.OpenedAt); err != nil {
			return nil, fmt.Errorf("원장 세션 행 해석 실패: %w", err)
		}
		x.Label, x.BlockedWhy = ptrOf(label), ptrOf(blockedWhy)
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("원장 세션 순회 실패: %w", err)
	}
	return out, nil
}

// ptrOf 는 NULL 을 nil 로, 값을 포인터로 낸다.
// str() 과 다르다 — str 은 NULL 을 "" 로 접어 둘을 구분 불가능하게 만든다.
func ptrOf(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	v := ns.String
	return &v
}

// ledgerDSN 은 원장 읽기 전용 접속 문자열이다. 기본 dsn() 과 두 곳이 다르다.
//
//	_txlock=deferred      기본은 immediate 다. 읽기 스냅숏만 잡고 서버 쓰기를 안 막는다.
//	                      deferred 의 알려진 위험(읽기 뒤 쓰기 승격이 SQLITE_BUSY 로 즉시
//	                      실패하고 busy_timeout 이 안 듣는다)은 원장이 쓰기를 하지 않으므로
//	                      발생할 자리가 없다.
//	journal_mode 를 안 건다  그것이 이 DSN 에서 파일을 바꿀 수 있는 유일한 pragma 다.
//	                      대상은 롤백저널일 수 있다 — VACUUM INTO 로 뜬 <db>.bak-* 이 그 모드이고,
//	                      그것이 바로 이 명령이 건져야 할 물건이다. 걸면 그 헤더를 영구히 고친다.
//	                      되읽기 확인은 ledgerWantPragmas 로 한다(journal_mode 가 빠진 판) —
//	                      요청하지도 않은 pragma 를 요구하면 롤백저널 DB 마다 거짓 진단이 난다.
func ledgerDSN(path string) string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Set("_txlock", "deferred")
	return "file:" + path + "?" + q.Encode()
}

// WriteLedger 는 원장을 DB 에 되쓴다. **CLI 표면이 없다** — 지금은 무손실을 시험이 증명하는
// 데 쓰이고, 후속에서 `fd import --judgments` 를 배선만 하면 된다.
//
// ★ AddJudgment 를 쓰지 않는다. 그것은 빈 ID 에 새 ULID 를, 빈 At 에 지금 시각을 채운다 —
// 복원은 원문을 그대로 되살려야 하므로 raw INSERT 로 간다. 트리거와 CHECK 는 그대로 걸린다
// (그것이 안전핀이다).
//
// ★ 정책은 "빈 표 전제"다. judgment 는 judgment_no_update·judgment_no_delete 트리거로
// UPDATE·DELETE 가 물리적으로 금지돼 있어, 잘못 넣은 행을 고치거나 지울 수 없다.
// 그래서 중복 id 를 건너뛰지 않고 거절한다 — 조용히 넘어가면 무엇이 반쯤 들어갔는지 모른다.
//
// ★ 폐포를 통째로 되쓴다. machine·project·session 이 판단보다 먼저 들어가고, 그 셋은
// 아무것도 참조하지 않거나 서로만 참조하므로 여기서 닫힌다. 빈 DB 에 되쓰면 미리 심어 둘
// 것이 하나도 없다 — 그것이 이 함수가 증명하는 "무손실"의 실제 의미다.
func (s *Store) WriteLedger(ctx context.Context, d LedgerDump) error {
	return s.Tx(ctx, func(t *Tx) error {
		// ★ supersedes 는 judgment 를 자기참조하고, 원장의 id 순서가 그 참조 순서와 같다는
		//   보장이 없다. FK 검사를 커밋 시점으로 미뤄 순서 제약을 없앤다(move.go 와 같은 수단).
		//   이 pragma 는 **이 트랜잭션에만** 걸리고 커밋 때 전부 검사된다.
		if _, err := t.tx.ExecContext(t.ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
			return fmt.Errorf("FK 검사 미루기 실패: %w", err)
		}
		machineStmt := ledgerInsert("machine", machineCols)
		for _, m := range d.Machines {
			if _, err := t.tx.ExecContext(t.ctx, machineStmt,
				m.ID, m.Hostname, m.FirstSeen, m.LastSeen); err != nil {
				return fmt.Errorf("원장 머신 되쓰기 실패(id=%q): %w", clip(m.ID, 64), err)
			}
		}
		projectStmt := ledgerInsert("project", projectCols)
		for _, p := range d.Projects {
			if _, err := t.tx.ExecContext(t.ctx, projectStmt,
				p.ID, p.Path, p.RemoteURL, p.DefaultBranch,
				p.Config, p.ConfigFromSHA, p.CreatedAt, p.PinnedAt, p.ArchivedAt); err != nil {
				return fmt.Errorf("원장 프로젝트 되쓰기 실패(id=%q): %w", clip(p.ID, 64), err)
			}
		}
		sessionStmt := ledgerInsert("session", sessionCols)
		for _, x := range d.Sessions {
			if _, err := t.tx.ExecContext(t.ctx, sessionStmt,
				x.ID, x.Project, x.MachineID, x.Worktree, x.CCSessionID,
				x.Label, x.State, x.BlockedWhy, x.OpenedAt); err != nil {
				return fmt.Errorf("원장 세션 되쓰기 실패(id=%q project=%q): %w",
					clip(x.ID, 64), clip(x.Project, 64), err)
			}
		}
		judgmentStmt := ledgerInsert("judgment", judgmentCols)
		for _, j := range d.Judgments {
			if _, err := t.tx.ExecContext(t.ctx, judgmentStmt,
				j.ID, j.Project, j.SessionID, j.At, j.Kind, j.Title, j.Body, j.Supersedes); err != nil {
				return fmt.Errorf("원장 판단 되쓰기 실패(id=%q kind=%q): %w",
					clip(j.ID, 64), clip(j.Kind, 32), err)
			}
		}
		linkStmt := ledgerInsert("judgment_link", linkCols)
		for _, l := range d.Links {
			if _, err := t.tx.ExecContext(t.ctx, linkStmt,
				l.JudgmentID, l.TargetKind, l.TargetID, l.TargetProject); err != nil {
				return fmt.Errorf("원장 링크 되쓰기 실패(judgment=%q target=%s/%s): %w",
					clip(l.JudgmentID, 64), clip(l.TargetKind, 32), clip(l.TargetID, 64), err)
			}
		}
		snapshotStmt := ledgerInsert("snapshot", snapshotCols)
		for _, sn := range d.Snapshots {
			if _, err := t.tx.ExecContext(t.ctx, snapshotStmt,
				sn.Project, sn.Key, sn.Value, sn.Method,
				sn.Evidence, sn.InputDigest, sn.ComputedAt); err != nil {
				return fmt.Errorf("원장 스냅숏 되쓰기 실패(project=%q key=%q): %w",
					clip(sn.Project, 64), clip(sn.Key, 64), err)
			}
		}
		return nil
	})
}

// ledgerOpenRefusal 은 원장 열기를 거절하는 문구다. 순수 함수다.
//
// ★ 왜 갈래를 가르나. 예전에는 non-None 넷을 한 문장으로 뭉쳤다:
//
//	"이 바이너리로 열면 DB 가 바뀐다(%s) — … (먼저 fd serve 를 이 바이너리로 올려 스키마를 맞춰라)"
//
// 그런데 non-None 중 **셋이 MigrateReject** 이고, 그것은 정의상 "열어도 DB 를 안 바꾼다 —
// 아예 안 연다"다. 앞 절이 거짓이고, 뒤 절은 어느 갈래도 fd serve 재기동으로 안 풀린다
// (남의 DB거나 · 끊긴 마이그레이션이라 백업에서 되돌려야 하거나 · 바이너리가 낡았거나).
//
// 특히 dbVersion > codeVersion 에서는 한 문장이 **정반대를 말했다**. Reason 은 "바이너리를
// 올려라"인데 꼬리는 "이 바이너리로 serve 를 올려라"이고, 그 처방을 따르면 selfcheck 가
// 같은 판정으로 기동도 거절한다. 이 저장소에서 실제로 났던 상황이다(수동 빌드 서버가
// 19시간·115커밋만큼 낡은 채로 돌았다) — 하필 판단 원장을 뜨는 것이 가장 절실한
// 상황에서만 보이는 문구였다.
//
// ★ MigrateReject 에는 처방을 **일부러 안 붙인다.** Reason 이 갈래마다 다른 처방을 이미
// 담고 있어서(손으로 확인하라 · 백업에서 되돌려라 · 바이너리를 올려라), 고정 꼬리를 붙이면
// 그중 하나와 반드시 어긋난다. 갈래를 가르는 선례는 selfcheck.go 가 이미 쓴다.
func ledgerOpenRefusal(plan MigrationPlan) error {
	// Reason 은 PlanMigration 이 항상 채운다 — 어느 갈래든 새 사유를 지어내지 않는다.
	if plan.Action == MigrateReject {
		return fmt.Errorf("이 바이너리는 이 DB 를 아예 열지 않는다(스키마도 안 바꾼다) "+
			"— 원장 내보내기를 거절한다: %s", plan.Reason)
	}
	return fmt.Errorf("이 바이너리로 열면 DB 가 바뀐다(%s) — 원장 내보내기를 거절한다: %s "+
		"(먼저 fd serve 를 이 바이너리로 올려 스키마를 맞춰라)", plan.Action, plan.Reason)
}

// OpenLedger 는 원장을 읽기 위해 DB 를 연다. **스키마를 바꾸지 않는다.**
//
// ★ store.Open 을 쓰지 않는 이유. 그것은 verifyPragmas 다음에 반드시 s.migrate 를 돌고,
// 판정에 따라 증분을 적용하며 그 앞에서 VACUUM INTO 백업을 뜬다. 즉 낡은 DB 를 만나면
// **백업하기 전에 스키마를 바꾼다.** 백업이 그 계기가 되면 안 된다.
//
// ★ mode=ro 를 쓰지 않는 이유. 서버가 죽은 채 -wal 이 남아 있으면 읽기 전용 연결은
// WAL 복구를 못 해 **열기 자체가 실패한다.** 원장 내보내기는 정확히 그 상황에서 돌아야 한다.
// "이행이 필요하면 거절"이 mode=ro 보다 강하다 — 애초에 바꿀 수 있는 상태로 안 들어간다.
//
// 없는 파일에는 오류를 낸다. sql.Open 은 파일을 **만들기** 때문에, 부재를 확인 안 하고 열면
// 내보내기가 빈 DB 를 하나 만들어 놓고 "0건 내보냈다"고 말한다(ProbeMigration 과 같은 판단).
func OpenLedger(ctx context.Context, path string, log *slog.Logger) (*Store, error) {
	if log == nil {
		log = slog.Default()
	}
	// ★ 이 재기는 대상 파일을 한 바이트도 안 바꾼다 — "거절한다"고 인쇄하는 실행조차
	//   아카이브를 변조하던 것을 probe.go 가 DSN 에서 막는다(그 함수의 주석 참고).
	plan, err := ProbeMigration(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("원장을 읽기 전에 DB 상태를 재지 못했다(path=%q): %w", clip(path, 200), err)
	}
	if plan.Action != MigrateNone {
		return nil, ledgerOpenRefusal(plan)
	}

	db, err := sql.Open("sqlite", ledgerDSN(path))
	if err != nil {
		return nil, fmt.Errorf("원장용 sqlite 열기 실패(path=%q): %w", clip(path, 200), err)
	}
	// 읽기 한 갈래뿐이라 커넥션을 늘릴 이유가 없다. 트랜잭션이 커넥션 하나에 묶인다.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("원장용 sqlite 접속 실패(path=%q): %w", clip(path, 200), err)
	}

	s := &Store{db: db, path: path, log: log}
	// DSN pragma 가 실제로 걸렸는지 되읽어 확인한다 — 드라이버는 모르는 pragma 를 조용히 무시한다.
	// ledgerDSN 이 요청한 것만 본다. journal_mode 는 요청하지 않았으므로 요구하지도 않는다.
	if err := s.verifyPragmas(ctx, ledgerWantPragmas); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
