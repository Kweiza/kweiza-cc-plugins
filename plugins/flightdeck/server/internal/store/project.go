package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// project · machine — 설정 계층.
//
// 둘 다 다른 표의 FK 대상이라 **먼저 있어야 한다**. 없으면 세션 등록이 FK 위반으로 죽고,
// 그 오류가 곧 "프로젝트를 먼저 등록하라"는 안내가 된다(앱에서 미리 조회해 판정하지 않는다).

// ─────────────────────────────────────────────────────────────────────────────
// project
// ─────────────────────────────────────────────────────────────────────────────

// projectCols 는 프로젝트 조회의 컬럼 목록이다.
// judgmentCols·sessionCols 와 같은 이유로 상수다 — 목록을 손으로 다시 적으면
// 순서가 어긋나는 순간 Scan 이 조용히 엉뚱한 값을 채운다(전부 문자열이라 타입 오류도 안 난다).
const projectCols = `id, path, remote_url, default_branch, config, config_from_sha, created_at, pinned_at, archived_at`

// machineCols 는 머신 조회의 컬럼 목록이다.
const machineCols = `id, hostname, first_seen, last_seen`

// UpsertProject 는 프로젝트를 등록하거나 갱신한다.
//
// created_at 은 첫 등록 시각을 보존한다 — 재등록이 나이를 0으로 되돌리면
// "언제부터 있던 프로젝트인가"가 사라진다.
//
// ★ pinned_at·archived_at 도 같은 이유로 **갱신 목록 밖**이다. 이 함수는 세션이 열릴 때마다
// 돌고(service/session.go 의 자동 등록), 목록에 넣으면 훅이 세션을 열 때마다 사람이 고른
// 표시 축이 날아간다. 그 손실은 어느 화면에도 안 뜬다 — 다음에 볼 때 그냥 안 켜져 있을 뿐이다.
// 그 축을 쓰는 문은 SetProjectView 하나뿐이다.
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

// scanProject 는 projectCols 순서의 한 행을 읽는다.
//
// ★ ListProjects 와 getProject 가 이것을 공유한다. Scan 목록이 두 벌이면 컬럼을 더할 때
// 한쪽만 고쳐지고, 전부 문자열이라 타입 오류도 안 난다 — projectCols 주석이 경고하는 실패다.
func scanProject(sc interface{ Scan(...any) error }) (model.Project, error) {
	var p model.Project
	var remote, config, fromSHA, pinned, archived sql.NullString
	var created string
	if err := sc.Scan(&p.ID, &p.Path, &remote, &p.DefaultBranch, &config, &fromSHA,
		&created, &pinned, &archived); err != nil {
		return model.Project{}, err
	}
	p.RemoteURL, p.Config, p.ConfigFromSHA = str(remote), str(config), str(fromSHA)
	var err error
	if p.CreatedAt, err = parseTime(created); err != nil {
		return model.Project{}, err
	}
	// ★ store.go 의 parseNullTime 은 *time.Time 을 낸다(landing.go·item.go 의 포인터
	//   필드가 그것을 그대로 받는다). PinnedAt·ArchivedAt 은 포인터가 아니라 값 필드라
	//   nil 을 제로값으로 편다 — 그 자체가 이미 "NULL == 아님"이다.
	pinnedAt, err := parseNullTime(pinned)
	if err != nil {
		return model.Project{}, err
	}
	if pinnedAt != nil {
		p.PinnedAt = *pinnedAt
	}
	archivedAt, err := parseNullTime(archived)
	if err != nil {
		return model.Project{}, err
	}
	if archivedAt != nil {
		p.ArchivedAt = *archivedAt
	}
	return p, nil
}

func getProject(ctx context.Context, q dbtx, id string) (model.Project, error) {
	row := q.QueryRowContext(ctx, `
		SELECT `+projectCols+`
		FROM project WHERE id = ?`, id)
	p, err := scanProject(row)
	if errors.Is(err, sql.ErrNoRows) {
		return p, notFound(NFProject, "", id)
	}
	if err != nil {
		return p, fmt.Errorf("프로젝트 조회 실패(id=%q): %w", clip(id, 64), err)
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
		SELECT `+projectCols+`
		FROM project ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("프로젝트 목록 조회 실패: %w", err)
	}
	defer rows.Close()

	var out []model.Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, fmt.Errorf("프로젝트 행 해석 실패: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("프로젝트 목록 순회 실패: %w", err)
	}
	return out, nil
}

// SetProjectView 는 프로젝트의 표시 축(핀·보관)을 정한다. 제로값은 NULL 이다.
//
// ★ 이 축은 표시 계층이라 사유를 안 받는다. 이 화면에서 사유가 필수인 셋(선점 회수 ·
// 항목 폐기 · 줄 회수)은 전부 남의 일을 뺏거나 되돌릴 수 없는 것인데, 핀과 보관은 둘 다
// 아니다 — 내 판이고 클릭 하나로 돌아온다. 되짚을 거리는 시각과 event 가 남긴다.
//
// ★ 이 UPDATE 는 두 축을 **통째로** 덮어쓴다 — "핀만 바꾼다"는 계약이 아니다. 호출자가
// archived 자리에 제로값을 넘기면 기존 보관이 있어도 조용히 풀린다(TestSetProjectViewOverwritesBothAxesTogether
// 가 그 동작을 의도로 못박아 둔다). 한 축만 바꾸고 싶으면 이 함수를 부르기 전에 같은 Tx
// 안에서 GetProject 로 다른 축의 현재 값을 읽어 그대로 함께 실어야 한다 — 그러지 않으면
// UpsertProject 에 대해 이미 막은 것과 같은 모양의 손실이 난다(핀 토글 처리기가
// `SetProjectView(ctx, id, time.Now(), time.Time{})` 라고만 쓰면 보관이 날아간다).
func (t *Tx) SetProjectView(id string, pinned, archived time.Time) error {
	res, err := t.tx.ExecContext(t.ctx, `
		UPDATE project SET pinned_at = ?, archived_at = ? WHERE id = ?`,
		nullTime(pinned), nullTime(archived), id)
	if err != nil {
		return fmt.Errorf("프로젝트 표시 축 갱신 실패(id=%q): %w", clip(id, 64), err)
	}
	// ★ UPDATE 는 0행이어도 성공한다. 확인하지 않으면 프로젝트 id 오타가 조용히 성공하고,
	//   화면은 "눌렀는데 아무 일도 안 일어난다"가 된다 — 그 증상에서 원인이 안 보인다.
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("프로젝트 표시 축 갱신 결과 확인 실패(id=%q): %w", clip(id, 64), err)
	}
	// ★ ErrNotFound 를 fmt.Errorf 로 직접 감싸지 않는다. getProject(project.go 위쪽)가
	//   쓰는 notFound(NFProject, ...) 와 같은 길로 보내야 internal/api 의 errors.As(*NotFoundError)
	//   가 좌표·처방을 붙일 수 있다 — sentinel 로 새면 일반 404 문구로 접혀 "무엇이
	//   없었는지"가 응답에서 사라진다(notfound.go 의 그 타입 주석이 이 실패를 이미 적어 뒀다).
	if n == 0 {
		return notFound(NFProject, "", id)
	}
	return nil
}

// SetProjectView 는 단발 트랜잭션으로 감싼 것이다.
func (s *Store) SetProjectView(ctx context.Context, id string, pinned, archived time.Time) error {
	return s.Tx(ctx, func(t *Tx) error { return t.SetProjectView(id, pinned, archived) })
}

// projectRefTables 는 project(id) 를 참조하거나 project 컬럼으로 프로젝트에 묶이는 표 중
// **삭제할 때 사람이 직접 다뤄야 하는 것**이다. 삭제 순서이기도 하다 — 자식부터 부모 순이다.
//
// item_after · claim 은 여기 없다. 둘 다 (project, item_id) 로 item(project, id) 를
// ON DELETE CASCADE 로 참조해서, item 을 지우면 자동으로 함께 사라진다 — 이 목록이
// 답해야 하는 "지우기 전에 손으로 볼 것"에 안 든다.
//
// ★ 뒤의 둘(item_dependents · pick_eval)은 FK 가 아니라 컬럼으로만 묶인다. FK 가 안 우니
// 안 지워도 삭제는 성공하고, 그래서 더 위험하다 — 조용히 고아 행이 남는다.
//
// ★ judgment 는 여기 있지만 **지우지 않는다**. judgment_no_delete 트리거가 원리적으로
// 막는다(schema.sql). 그래서 RemoveProject 는 판단이 하나라도 있으면 거절한다.
//
// ★ event 는 여기 없다. event.project 는 FK 가 아니라 그냥 컬럼이고(schema.sql 의 그 자리),
// 프로젝트가 사라져도 남는다 — 그것이 옳다. "이런 프로젝트가 있었고 언제 지워졌다"가
// 원장에 남는 유일한 길이다.
//
// ★ landing_queue(증분 003)는 project(id) 와 session(id) 를 **둘 다** FK 로 참조하고
// 어느 쪽도 CASCADE 가 아니다(schema_table_count_test.go 의 TestDeclaredTablesMatchDesign 이
// 이 표가 실재함을 못박는다). session 보다 앞에 둔 이유: session 을 먼저 지우면 그 세션을
// 가리키는 landing_queue 행이 FK 위반으로 삭제 전체를 막는다 — 자식(landing_queue)이 부모
// (session) 보다 먼저다.
var projectRefTables = []string{
	"session_workspace",
	"landing_queue",
	"session",
	"ref_state",
	"change_set",
	"item",
	"judgment",
	"snapshot",
	"counter",
	"resource_hold",
	"job",
	"item_dependents",
	"pick_eval",
}

// ProjectRefCounts 는 이 프로젝트에 묶인 행 수를 표별로 센다.
// 지우기 전에 무엇이 함께 갈지 보여주는 자리다.
func (s *Store) ProjectRefCounts(ctx context.Context, id string) (map[string]int, error) {
	out := make(map[string]int, len(projectRefTables)+1)
	for _, tbl := range projectRefTables {
		var n int
		// ★ 표 이름은 위 projectRefTables 상수에서만 온다(외부 입력이 아니다) — 그래서
		// 문자열 결합으로 SQL 을 짓는 것이 안전하다. tbl 이 사용자 입력이었다면 이 자리가
		// SQL 인젝션 통로였을 것이다.
		if err := s.db.QueryRowContext(ctx,
			`SELECT count(*) FROM `+tbl+` WHERE project = ?`, id).Scan(&n); err != nil {
			return nil, fmt.Errorf("행 수 조회 실패(table=%s, project=%q): %w", tbl, clip(id, 64), err)
		}
		out[tbl] = n
	}
	var ev int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM event WHERE project = ?`, id).Scan(&ev); err != nil {
		return nil, fmt.Errorf("이벤트 수 조회 실패(project=%q): %w", clip(id, 64), err)
	}
	out["event"] = ev // 세기만 한다 — 안 지운다
	return out, nil
}

// nullTime 은 제로값을 NULL 로 낸다.
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return fmtTime(t)
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
		`SELECT `+machineCols+` FROM machine WHERE id = ?`, id).
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
