package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/kweiza/flightdeck/internal/model"
)

// project · machine — 설정 계층.
//
// 둘 다 다른 표의 FK 대상이라 **먼저 있어야 한다**. 없으면 세션 등록이 FK 위반으로 죽고,
// 그 오류가 곧 "프로젝트를 먼저 등록하라"는 안내가 된다(앱에서 미리 조회해 판정하지 않는다).

// ─────────────────────────────────────────────────────────────────────────────
// project
// ─────────────────────────────────────────────────────────────────────────────

// UpsertProject 는 프로젝트를 등록하거나 갱신한다.
//
// created_at 은 첫 등록 시각을 보존한다 — 재등록이 나이를 0으로 되돌리면
// "언제부터 있던 프로젝트인가"가 사라진다.
func (t *Tx) UpsertProject(p model.Project) error {
	if p.ID == "" {
		return errors.New("프로젝트 id 가 비었다")
	}
	if p.DefaultBranch == "" {
		p.DefaultBranch = "main"
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = nowStamp()
	}
	_, err := t.tx.ExecContext(t.ctx, `
		INSERT INTO project(id, path, remote_url, default_branch, config, config_from_sha, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		  path            = excluded.path,
		  remote_url      = excluded.remote_url,
		  default_branch  = excluded.default_branch,
		  config          = excluded.config,
		  config_from_sha = excluded.config_from_sha`,
		p.ID, p.Path, nullStr(p.RemoteURL), p.DefaultBranch,
		nullStr(p.Config), nullStr(p.ConfigFromSHA), fmtTime(p.CreatedAt))
	if err != nil {
		return fmt.Errorf("프로젝트 upsert 실패(id=%q): %w", clip(p.ID, 64), err)
	}
	return nil
}

// UpsertProject 는 단발 트랜잭션으로 감싼 것이다.
func (s *Store) UpsertProject(ctx context.Context, p model.Project) error {
	return s.Tx(ctx, func(t *Tx) error { return t.UpsertProject(p) })
}

func getProject(ctx context.Context, q dbtx, id string) (model.Project, error) {
	var p model.Project
	var remote, config, fromSHA sql.NullString
	var created string
	err := q.QueryRowContext(ctx, `
		SELECT id, path, remote_url, default_branch, config, config_from_sha, created_at
		FROM project WHERE id = ?`, id).
		Scan(&p.ID, &p.Path, &remote, &p.DefaultBranch, &config, &fromSHA, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return p, notFound(NFProject, "", id)
	}
	if err != nil {
		return p, fmt.Errorf("프로젝트 조회 실패(id=%q): %w", clip(id, 64), err)
	}
	p.RemoteURL, p.Config, p.ConfigFromSHA = str(remote), str(config), str(fromSHA)
	if p.CreatedAt, err = parseTime(created); err != nil {
		return p, err
	}
	return p, nil
}

// GetProject 는 프로젝트 하나를 읽는다. 없으면 ErrNotFound 를 감싼 오류다.
func (s *Store) GetProject(ctx context.Context, id string) (model.Project, error) {
	return getProject(ctx, s.db, id)
}

// GetProject 는 트랜잭션 안에서 읽는다.
func (t *Tx) GetProject(id string) (model.Project, error) {
	return getProject(t.ctx, t.tx, id)
}

// ListProjects 는 전부를 id 순으로 낸다. 프로젝트 수는 사람이 등록한 만큼이라 페이징이 없다.
func (s *Store) ListProjects(ctx context.Context) ([]model.Project, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, path, remote_url, default_branch, config, config_from_sha, created_at
		FROM project ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("프로젝트 목록 조회 실패: %w", err)
	}
	defer rows.Close()

	var out []model.Project
	for rows.Next() {
		var p model.Project
		var remote, config, fromSHA sql.NullString
		var created string
		if err := rows.Scan(&p.ID, &p.Path, &remote, &p.DefaultBranch, &config, &fromSHA, &created); err != nil {
			return nil, fmt.Errorf("프로젝트 행 해석 실패: %w", err)
		}
		p.RemoteURL, p.Config, p.ConfigFromSHA = str(remote), str(config), str(fromSHA)
		if p.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("프로젝트 목록 순회 실패: %w", err)
	}
	return out, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// machine
// ─────────────────────────────────────────────────────────────────────────────

// UpsertMachine 은 머신을 등록하고 last_seen 을 갱신한다.
//
// first_seen 은 보존한다 — 그 값이 "이 머신을 언제부터 봤나"이고,
// 갱신해 버리면 last_seen 과 같은 값이 되어 컬럼 하나가 통째로 의미를 잃는다.
func (t *Tx) UpsertMachine(m model.Machine) error {
	if m.ID == "" {
		return errors.New("머신 id 가 비었다")
	}
	now := m.LastSeen
	if now.IsZero() {
		now = nowStamp()
	}
	first := m.FirstSeen
	if first.IsZero() {
		first = now
	}
	_, err := t.tx.ExecContext(t.ctx, `
		INSERT INTO machine(id, hostname, first_seen, last_seen)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		  hostname  = excluded.hostname,
		  last_seen = excluded.last_seen`,
		m.ID, m.Hostname, fmtTime(first), fmtTime(now))
	if err != nil {
		return fmt.Errorf("머신 upsert 실패(id=%q): %w", clip(m.ID, 64), err)
	}
	return nil
}

// UpsertMachine 은 단발 트랜잭션으로 감싼 것이다.
func (s *Store) UpsertMachine(ctx context.Context, m model.Machine) error {
	return s.Tx(ctx, func(t *Tx) error { return t.UpsertMachine(m) })
}

// GetMachine 은 머신 하나를 읽는다.
func (s *Store) GetMachine(ctx context.Context, id string) (model.Machine, error) {
	var m model.Machine
	var first, last string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, hostname, first_seen, last_seen FROM machine WHERE id = ?`, id).
		Scan(&m.ID, &m.Hostname, &first, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return m, notFound(NFMachine, "", id)
	}
	if err != nil {
		return m, fmt.Errorf("머신 조회 실패(id=%q): %w", clip(id, 64), err)
	}
	if m.FirstSeen, err = parseTime(first); err != nil {
		return m, err
	}
	if m.LastSeen, err = parseTime(last); err != nil {
		return m, err
	}
	return m, nil
}
