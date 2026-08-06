package store

import (
	"context"
	"database/sql"
	"fmt"
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

// LedgerDump 는 한 순간의 세 표 전량이다.
type LedgerDump struct {
	Judgments []LedgerJudgment
	Links     []LedgerLink
	Snapshots []LedgerSnapshot
}

// ReadLedger 는 세 표를 **한 트랜잭션 안에서** 전량 읽는다.
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
		SELECT judgment_id, target_kind, target_id FROM judgment_link
		ORDER BY judgment_id, target_kind, target_id`)
	if err != nil {
		return nil, fmt.Errorf("원장 링크 조회 실패: %w", err)
	}
	defer rows.Close()

	var out []LedgerLink
	for rows.Next() {
		var l LedgerLink
		if err := rows.Scan(&l.JudgmentID, &l.TargetKind, &l.TargetID); err != nil {
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

// ptrOf 는 NULL 을 nil 로, 값을 포인터로 낸다.
// str() 과 다르다 — str 은 NULL 을 "" 로 접어 둘을 구분 불가능하게 만든다.
func ptrOf(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	v := ns.String
	return &v
}
