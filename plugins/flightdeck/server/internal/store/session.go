package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// D 계층 — 세션·워크스페이스·신호·발자국·ref·변경집합.
//
// 이 계층에는 "죽었다"를 쓰는 함수가 없다. 신호의 나이를 숫자로 낼 뿐이다 —
// 불리언을 만드는 순간 그것이 회수·회피·탈락 셋의 상류가 되고, 그 판정은 실측에서 두 번 틀렸다.

// ─────────────────────────────────────────────────────────────────────────────
// session
// ─────────────────────────────────────────────────────────────────────────────

// OpenSession 은 3중키(machine_id, worktree, cc_session_id)로 세션을 upsert 한다.
//
// 같은 3중키면 **기존 행을 그대로 돌려준다**(재개 경로). 아니면 새 ULID 로 만든다.
// 워크트리 경로만 키로 쓰면 안 된다 — 경로는 규율상 재사용되고(지우고 다시 만든다),
// 그러면 옛 세션 행과 합쳐진다. 그리고 이 함수 어디에도 접두 일치가 없으므로
// 조상 트리의 등록을 물려받는 것이 원리적으로 불가능하다.
//
// 두 번째 반환값 created 는 "새로 만들었나"다. 재개와 신규를 호출부가 구분해야
// 배너 문구가 달라지는데, 세션 구조체만 보면 그 구분이 불가능하다.
//
// label 은 표시 전용이라 재개 때 최신 선언이 이긴다. 그 외 컬럼은 건드리지 않는다 —
// 재개는 관측이지 재선언이 아니다.
func (t *Tx) OpenSession(project, machineID, worktree, ccSessionID, label string) (model.Session, bool, error) {
	if project == "" || machineID == "" || worktree == "" || ccSessionID == "" {
		return model.Session{}, false, fmt.Errorf(
			"세션 3중키와 프로젝트는 전부 필요하다(project=%q machine=%q worktree=%q cc_session=%q)",
			clip(project, 64), clip(machineID, 64), clip(worktree, 200), clip(ccSessionID, 64))
	}

	existing, err := t.sessionByTriple(machineID, worktree, ccSessionID)
	switch {
	case err == nil:
		if label != "" && label != existing.Label {
			if _, err := t.tx.ExecContext(t.ctx,
				`UPDATE session SET label = ? WHERE id = ?`, label, existing.ID); err != nil {
				return model.Session{}, false, fmt.Errorf("세션 label 갱신 실패(id=%q): %w", existing.ID, err)
			}
			existing.Label = label
		}
		// ★ 닫힌 카드를 다시 열면 **살아난다.** 이 자리가 없으면 닫기를 넣는 순간
		// 살아서 일하는 세션이 보드에서 사라진다 — /clear 는 카드를 닫고 곧바로
		// 같은 카드를 rekey 로 이어받는데, 그때 state 가 done 이면 ListLive 가 그것을
		// 통째로 뺀다. 이 도구가 이미 두 번 겪은 오판이다(board.go 머리말).
		//
		// ★ **되살리기만 한다. 죽이지 않는다.** 그래서 done 일 때만 손댄다:
		// blocked 는 사람이 사유와 함께 남긴 판단이라 여는 것이 조용히 지우면 안 되고,
		// active 는 이미 맞다.
		if existing.State == model.SessionDone {
			if _, err := t.tx.ExecContext(t.ctx,
				`UPDATE session SET state = ? WHERE id = ?`,
				string(model.SessionActive), existing.ID); err != nil {
				return model.Session{}, false, fmt.Errorf("세션 되살리기 실패(id=%q): %w", existing.ID, err)
			}
			existing.State = model.SessionActive
		}
		return existing, false, nil
	case errors.Is(err, ErrNotFound):
		// 아래로
	default:
		return model.Session{}, false, err
	}

	s := model.Session{
		ID:          NewID(),
		Project:     project,
		MachineID:   machineID,
		Worktree:    worktree,
		CCSessionID: ccSessionID,
		Label:       label,
		State:       model.SessionActive,
		OpenedAt:    nowStamp(),
	}
	// UNIQUE 위반은 그대로 올린다. 여기 오는 유일한 경로는 위 조회 이후 남이 같은
	// 3중키를 넣은 것인데, 이 트랜잭션은 BEGIN IMMEDIATE 라 그 창이 없다.
	if _, err := t.tx.ExecContext(t.ctx, `
		INSERT INTO session(id, project, machine_id, worktree, cc_session_id, label, state, blocked_why, opened_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, NULL, ?)`,
		s.ID, s.Project, s.MachineID, s.Worktree, s.CCSessionID, nullStr(s.Label),
		string(s.State), fmtTime(s.OpenedAt)); err != nil {
		return model.Session{}, false, writeErr(err, writeTarget{
			Target: TargetSession, Project: project, ID: s.ID,
			RefHint: fmt.Sprintf("프로젝트 %s · 머신 %s", clip(project, 64), clip(machineID, 64)),
		}, "세션 등록 실패(project=%q machine=%q worktree=%q)",
			clip(project, 64), clip(machineID, 64), clip(worktree, 200))
	}
	return s, true, nil
}

// OpenSession 은 단발 트랜잭션으로 감싼 것이다.
func (s *Store) OpenSession(ctx context.Context, project, machineID, worktree, ccSessionID, label string) (model.Session, bool, error) {
	var out model.Session
	var created bool
	err := s.Tx(ctx, func(t *Tx) error {
		var e error
		out, created, e = t.OpenSession(project, machineID, worktree, ccSessionID, label)
		return e
	})
	return out, created, err
}

const sessionCols = `id, project, machine_id, worktree, cc_session_id, label, state, blocked_why, opened_at`

func scanSession(sc interface{ Scan(...any) error }) (model.Session, error) {
	var s model.Session
	var label, why sql.NullString
	var opened, state string
	if err := sc.Scan(&s.ID, &s.Project, &s.MachineID, &s.Worktree, &s.CCSessionID,
		&label, &state, &why, &opened); err != nil {
		return s, err
	}
	s.Label, s.BlockedWhy = str(label), str(why)
	s.State = model.SessionState(state)
	var err error
	if s.OpenedAt, err = parseTime(opened); err != nil {
		return s, err
	}
	return s, nil
}

func (t *Tx) sessionByTriple(machineID, worktree, ccSessionID string) (model.Session, error) {
	row := t.tx.QueryRowContext(t.ctx,
		`SELECT `+sessionCols+` FROM session
		 WHERE machine_id = ? AND worktree = ? AND cc_session_id = ?`,
		machineID, worktree, ccSessionID)
	s, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return s, notFoundNote(NFSession, "3중키(머신·워크트리·cc 세션)에 해당하는")
	}
	if err != nil {
		return s, fmt.Errorf("세션 3중키 조회 실패(machine=%q worktree=%q): %w",
			clip(machineID, 64), clip(worktree, 200), err)
	}
	return s, nil
}

// DivergentSessions 는 **같은 대화(cc_session_id)인데 project 나 machine 이 다른** 세션을 낸다.
//
// ★ 키를 바꾸지 않는다. 세션 정체는 (machine, worktree, cc) 3중키 그대로이고, 이 조회는
// 판정이 아니라 **관측**이다 — 갈렸다는 사실을 말할 자리를 만드는 것이 전부다.
// 갈렸을 때 조용히 합치면 워크트리 축의 보증(조상 트리 등록을 안 물려받는다)이 무너진다.
//
// ★ 이 조회를 받는 인덱스가 없다(session_by_project 는 (project,state), UNIQUE 는 machine 선두).
// 지금 이 표는 수십 행 규모라 전수 주사가 무해하고, 부르는 자리도 **세션이 새로 만들어질 때
// 한 번**뿐이다(재개 경로는 안 탄다). 표가 커지면 cc_session_id 인덱스를 먼저 봐라.
func (t *Tx) DivergentSessions(ccSessionID, project, machineID string) ([]model.Session, error) {
	rows, err := t.tx.QueryContext(t.ctx,
		`SELECT `+sessionCols+` FROM session
		 WHERE cc_session_id = ? AND (project <> ? OR machine_id <> ?)
		 ORDER BY opened_at`, ccSessionID, project, machineID)
	if err != nil {
		return nil, fmt.Errorf("정체 갈림 조회 실패(cc_session=%q): %w", clip(ccSessionID, 64), err)
	}
	defer rows.Close()
	var out []model.Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("정체 갈림 행 해석 실패: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("정체 갈림 조회 순회 실패: %w", err)
	}
	return out, nil
}

func getSession(ctx context.Context, q dbtx, id string) (model.Session, error) {
	row := q.QueryRowContext(ctx, `SELECT `+sessionCols+` FROM session WHERE id = ?`, id)
	s, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return s, notFound(NFSession, "", id)
	}
	if err != nil {
		return s, fmt.Errorf("세션 조회 실패(session_id=%q): %w", clip(id, 64), err)
	}
	return s, nil
}

// GetSession 은 세션 하나를 읽는다.
func (s *Store) GetSession(ctx context.Context, id string) (model.Session, error) {
	return getSession(ctx, s.db, id)
}

// GetSession 은 트랜잭션 안에서 읽는다.
func (t *Tx) GetSession(id string) (model.Session, error) { return getSession(t.ctx, t.tx, id) }

// ValidateSessionState 는 상태 전이 인자가 성립하는지 본다.
//
// 스키마의 CHECK 가 최후 방어이지 1차 방어가 아니다. 여기서 거절해야
// 호출부가 "왜 안 됐나"를 그 자리에서 안다 — DB 제약 위반 문구는 사유를 말하지 않는다.
// 사유 문자열을 돌려주는 이유는 공허한 단정(막혔다고만 쓰고 왜인지 안 남기는 것)을 막기 위해서다.
func ValidateSessionState(state model.SessionState, why string) error {
	switch state {
	case model.SessionActive, model.SessionPaused, model.SessionDone:
		return nil
	case model.SessionBlocked:
		if why == "" {
			return errors.New("state=blocked 에는 사유가 필수다 — 막혔다고만 쓰고 왜인지 안 남기면 다음 세션이 같은 조사를 처음부터 다시 한다")
		}
		return nil
	default:
		return fmt.Errorf("알 수 없는 세션 상태 %q (active|paused|blocked|done 중 하나여야 한다)", clip(string(state), 32))
	}
}

// SetState 는 세션 상태를 바꾼다. blocked 면 사유가 필수다.
func (t *Tx) SetSessionState(id string, state model.SessionState, why string) error {
	if err := ValidateSessionState(state, why); err != nil {
		return err
	}
	res, err := t.tx.ExecContext(t.ctx,
		`UPDATE session SET state = ?, blocked_why = ? WHERE id = ?`,
		string(state), nullStr(why), id)
	if err != nil {
		return fmt.Errorf("세션 상태 갱신 실패(session_id=%q state=%q): %w", clip(id, 64), state, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("세션 상태 갱신 결과 확인 실패(session_id=%q): %w", clip(id, 64), err)
	}
	if n == 0 {
		return notFound(NFSession, "", id)
	}
	return nil
}

// SetSessionState 는 단발 트랜잭션으로 감싼 것이다.
func (s *Store) SetSessionState(ctx context.Context, id string, state model.SessionState, why string) error {
	return s.Tx(ctx, func(t *Tx) error { return t.SetSessionState(id, state, why) })
}

// Rekey 는 카드의 cc_session_id 만 갈아끼운다.
//
// ★ 이것이 "카드 두 장을 하나로 합치기"의 전부다. 선점·판단·발자국·자원이 전부
// session(id) 를 참조하고 cc_session_id 는 UNIQUE (machine_id, worktree, cc_session_id) 에만
// 쓰이므로, 이 한 줄로 원장이 통째로 따라온다. 표를 옮기는 코드가 필요 없다.
//
// ★ 언제 이것이 필요한가. /clear·compact 로 대화의 cc 가 갈리면 이미 떠 있는 MCP 프로세스는
// 옛 값을 계속 쓴다(environ 은 exec 뒤 안 바뀐다). 훅만 새 값을 보므로, 훅이 이 갈래로
// 카드를 따라오게 한다.
//
// 빈 cc 를 거절한다 — 정체가 사라진 카드는 3중키에서 다른 빈 카드와 충돌하고,
// 그 카드가 쥔 선점은 아무도 회수할 수 없다.
func (t *Tx) Rekey(id, ccSessionID string) (model.Session, error) {
	cc := strings.TrimSpace(ccSessionID)
	if cc == "" {
		return model.Session{}, fmt.Errorf("cc_session_id 가 비었다 — 정체 없는 카드를 만들 수 없다")
	}
	res, err := t.tx.ExecContext(t.ctx,
		`UPDATE session SET cc_session_id = ? WHERE id = ?`, cc, id)
	if err != nil {
		return model.Session{}, writeErr(err, writeTarget{Target: TargetSession, ID: id},
			"세션 cc 갈아끼우기 실패(session=%s cc=%s)", clip(id, 64), clip(cc, 64))
	}
	if err := affectedOne(res, NFSession, "", id); err != nil {
		return model.Session{}, err
	}
	s, err := t.GetSession(id)
	if err != nil {
		return model.Session{}, err
	}
	// ★ 이벤트를 남긴다. 카드의 cc 가 조용히 바뀌면 나중에 아무도 원인에 도달 못 한다 —
	// 이 기능을 만들기 위해 /proc 을 뒤져야 했던 것이 정확히 그 이유였다.
	// Tx 안에서는 Tx.LogEvent 다. Store.LogEvent 는 별도 연결이라 여기서 부르면 교착한다.
	t.LogEvent("session.rekey", s.Project, s.ID, map[string]any{"cc_session_id": cc})
	return s, nil
}

// Rekey 는 Tx.Rekey 의 단독 실행 짝이다.
func (s *Store) Rekey(ctx context.Context, id, ccSessionID string) (model.Session, error) {
	var out model.Session
	err := s.Tx(ctx, func(t *Tx) error {
		var err error
		out, err = t.Rekey(id, ccSessionID)
		return err
	})
	return out, err
}

// ListLive 는 since 이후에 신호가 있거나 그 뒤에 열린 세션을 낸다.
//
// ★ 이 함수는 "죽었다"를 판정하지 않는다. 자르는 지점은 **호출자가 준 since** 이고,
// 결과에는 각 신호의 시각이 그대로 실린다. 나이를 숫자로만 내는 것이 §4 의 요구다.
//
// HasFootprint 를 함께 채우는 이유: 커밋도 편집도 안 하는 세션(조사·판정만)은
// 경로 축에서 아무도 안 막는데, **안 막는다는 사실이 화면에 있어야** 한다.
// 침묵하면 "겹침 없음"과 "이 축을 안 본다"가 구분되지 않는다.
func (s *Store) ListLive(ctx context.Context, project string, since time.Time) ([]model.SessionView, error) {
	cut := fmtTime(since)
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+sessionCols+` FROM session s
		WHERE s.project = ? AND s.state <> 'done'
		  AND (s.opened_at >= ?
		       OR EXISTS (SELECT 1 FROM signal g WHERE g.session_id = s.id AND g.at >= ?))
		ORDER BY s.opened_at DESC`, project, cut, cut)
	if err != nil {
		return nil, fmt.Errorf("살아 있는 세션 조회 실패(project=%q): %w", clip(project, 64), err)
	}
	defer rows.Close()

	var sessions []model.Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("세션 행 해석 실패: %w", err)
		}
		sessions = append(sessions, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("세션 목록 순회 실패: %w", err)
	}

	out := make([]model.SessionView, 0, len(sessions))
	for _, sess := range sessions {
		v := model.SessionView{Session: sess}
		if v.Signals, err = s.Signals(ctx, sess.ID); err != nil {
			return nil, err
		}
		if v.Paths, err = s.FootprintPaths(ctx, sess.ID); err != nil {
			return nil, err
		}
		v.HasFootprint = len(v.Paths) > 0
		if v.Claims, err = s.ClaimedItems(ctx, sess.ID); err != nil {
			return nil, err
		}
		// Branch·BranchSHA·AheadMain·LastNote 는 git 리더와 J 계층이 채우는 축이라
		// 여기서 비워 둔다. 0값과 "안 봤다"를 뭉개지 않으려면 채우는 쪽이 하나여야 한다.
		out = append(out, v)
	}
	return out, nil
}

// ListSessions 는 프로젝트의 **모든** 세션을 상태와 무관하게 낸다.
//
// ListLive 와 나란히 두는 이유: 되쓰기는 끝난 세션 카드까지 되돌려야 한다.
// ListLive 는 `state <> 'done'` 이고 신호 나이로도 거르므로, 되쓰기가 그것을 쓰면
// 끝난 카드가 통째로 사라진다 — 게시판과 달리 이관 산출물은 이력이 자산이다.
func (s *Store) ListSessions(ctx context.Context, project string) ([]model.Session, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+sessionCols+` FROM session WHERE project = ? ORDER BY opened_at, id`, project)
	if err != nil {
		return nil, fmt.Errorf("세션 전수 조회 실패(project=%q): %w", clip(project, 64), err)
	}
	defer rows.Close()

	var out []model.Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("세션 행 해석 실패: %w", err)
		}
		out = append(out, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("세션 목록 순회 실패: %w", err)
	}
	return out, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// session_workspace
// ─────────────────────────────────────────────────────────────────────────────

// AddWorkspace 는 세션이 만지는 작업 트리 하나를 붙인다.
// 한 세션이 코드 레포와 문서 레포를 함께 만지는 실무를 담는다 — 단수 project 필드로는 못 담는다.
//
// ─── 이 표는 지금 **쓰기만 있다.** 왜 그런지, 그리고 무엇을 근거로 쓰면 안 되는지 ───
//
// 실측(2026-08-04, 전수 grep):
//
//   - ListWorkspaces 의 비시험 호출자가 **0건**이다. 아무도 안 읽는다.
//   - 실제로 도는 유일한 writer 는 OpenSession 이 넣는 primary 하나이고,
//     그 Path 는 in.Worktree — 즉 **session.worktree 와 같은 값**이다.
//   - 부(副) 워크스페이스를 넣는 표면(POST /api/v1/sessions/{id}/workspaces)은 있지만
//     **그것을 치는 클라이언트가 하나도 없다**(cmd/fd 에도 mcpsrv 에도 없다).
//
// 그래서 지금 이 표에는 **세션 행이 이미 갖고 있지 않은 값이 한 건도 들어 있지 않다.**
//
// ★ **여기서 나오는 오판 하나를 미리 막는다.** 조회 키(3중키)에서 worktree 축을 빼자는
// 안을 기각할 때 "워크트리 축은 session_workspace 가 이미 갖고 있다"가 근거로 쓰였다.
// 그 문장은 **저장에만 참이고 표시에는 거짓이다** — 읽는 코드가 없으므로 카드·보드·겹침
// 어디에도 그 축이 나타나지 않는다. 게다가 위에서 보듯 담긴 값도 새롭지 않다.
// 이 표를 근거로 세션 키에서 worktree 를 빼면 그 축은 **아무 데서도 복구되지 않는다.**
//
// 이 표를 안 지운 이유: 표면(POST …/workspaces)과 스키마가 이미 있고, 한 세션이 여러 트리를
// 만지는 실무가 실재한다. 지우면 그 표면이 갈 곳을 잃는다. 다만 **읽는 쪽이 생기기 전까지는
// 근거로 쓰지 마라.** 지금 참인 상태는 시험이 지킨다 —
// internal/service 의 TestWorkspaceTableHoldsNothingNewYet.
func (t *Tx) AddWorkspace(w model.Workspace) error {
	primary := 0
	if w.IsPrimary {
		primary = 1
	}
	_, err := t.tx.ExecContext(t.ctx, `
		INSERT INTO session_workspace(session_id, project, path, is_primary)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(session_id, path) DO UPDATE SET
		  project = excluded.project, is_primary = excluded.is_primary`,
		w.SessionID, w.Project, w.Path, primary)
	if err != nil {
		return writeErr(err, writeTarget{
			Target: TargetSessionWorkspace, Project: w.Project, ID: w.SessionID,
			RefHint: fmt.Sprintf("세션 %s · 프로젝트 %s", clip(w.SessionID, 64), clip(w.Project, 64)),
		}, "워크스페이스 등록 실패(session_id=%q path=%q)",
			clip(w.SessionID, 64), clip(w.Path, 200))
	}
	return nil
}

// AddWorkspace 는 단발 트랜잭션으로 감싼 것이다.
func (s *Store) AddWorkspace(ctx context.Context, w model.Workspace) error {
	return s.Tx(ctx, func(t *Tx) error { return t.AddWorkspace(w) })
}

// ListWorkspaces 는 세션이 붙인 작업 트리를 낸다.
//
// ★ **비시험 호출자가 0건이다**(2026-08-04 실측). 지우지 않고 두는 이유와, 이 표를
// "워크트리 축은 이미 있다"의 근거로 쓰면 안 되는 이유는 AddWorkspace 주석에 있다.
func (s *Store) ListWorkspaces(ctx context.Context, sessionID string) ([]model.Workspace, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT session_id, project, path, is_primary FROM session_workspace
		 WHERE session_id = ? ORDER BY is_primary DESC, path`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("워크스페이스 조회 실패(session_id=%q): %w", clip(sessionID, 64), err)
	}
	defer rows.Close()

	var out []model.Workspace
	for rows.Next() {
		var w model.Workspace
		var primary int
		if err := rows.Scan(&w.SessionID, &w.Project, &w.Path, &primary); err != nil {
			return nil, fmt.Errorf("워크스페이스 행 해석 실패: %w", err)
		}
		w.IsPrimary = primary != 0
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("워크스페이스 목록 순회 실패: %w", err)
	}
	return out, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// signal
// ─────────────────────────────────────────────────────────────────────────────

// Beat 는 kind 별 최신 시각을 upsert 한다.
//
// ★ 시각을 **뒤로 되돌리지 않는다**. 훅은 비동기라 순서가 뒤집혀 도착할 수 있고,
// 뒤늦게 온 옛 비트가 최신 시각을 덮으면 살아 있는 세션이 남에게 낡은 것으로 보인다.
// 기존 도구에서 정확히 그 모양의 사고가 났다(419분 무갱신으로 표시된 세션이 실제로는 17초 전).
func (t *Tx) Beat(sessionID string, kind model.SignalKind, at time.Time) error {
	if at.IsZero() {
		at = nowStamp()
	}
	_, err := t.tx.ExecContext(t.ctx, `
		INSERT INTO signal(session_id, kind, at) VALUES (?, ?, ?)
		ON CONFLICT(session_id, kind) DO UPDATE SET at = excluded.at
		WHERE excluded.at > signal.at`,
		sessionID, string(kind), fmtTime(at))
	if err != nil {
		return writeErr(err, writeTarget{
			Target: TargetSignal, ID: sessionID,
			RefHint: "세션 " + clip(sessionID, 64),
		}, "신호 기록 실패(session_id=%q kind=%q)",
			clip(sessionID, 64), clip(string(kind), 32))
	}
	return nil
}

// Beat 는 단발 트랜잭션으로 감싼 것이다.
func (s *Store) Beat(ctx context.Context, sessionID string, kind model.SignalKind, at time.Time) error {
	return s.Tx(ctx, func(t *Tx) error { return t.Beat(sessionID, kind, at) })
}

// Signals 는 세션의 신호를 종류별로 낸다.
// **없는 종류는 키가 없다** — 0값과 부재를 가른다. 0값으로 채우면 "한 번도 안 온 신호"가
// "1970년에 온 신호"로 둔갑하고, 그 둘은 나이 표시에서 구분되지 않는다.
func (s *Store) Signals(ctx context.Context, sessionID string) (map[model.SignalKind]time.Time, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT kind, at FROM signal WHERE session_id = ?`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("신호 조회 실패(session_id=%q): %w", clip(sessionID, 64), err)
	}
	defer rows.Close()

	out := map[model.SignalKind]time.Time{}
	for rows.Next() {
		var kind, at string
		if err := rows.Scan(&kind, &at); err != nil {
			return nil, fmt.Errorf("신호 행 해석 실패: %w", err)
		}
		ts, err := parseTime(at)
		if err != nil {
			return nil, err
		}
		out[model.SignalKind(kind)] = ts
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("신호 목록 순회 실패: %w", err)
	}
	return out, nil
}

// LastSignal 은 세션의 가장 최근 신호 시각이다(종류 불문 MAX).
//
// ★ **생존 창으로 거르지 않는다.** 레인 점유자가 창 밖(무갱신)일 때가 정확히 그 나이를
// 알아야 하는 순간이다 — 여기서 창 밖 신호를 숨기면 "왜 안 비켜 주나"에 답할 수 없다.
// 신호가 하나도 없으면 두 번째 반환값이 false 다(0값과 부재를 가른다, Signals 와 같은 이유).
func (s *Store) LastSignal(ctx context.Context, sessionID string) (time.Time, bool, error) {
	var at sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT MAX(at) FROM signal WHERE session_id = ?`, sessionID).Scan(&at)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("최근 신호 조회 실패(session_id=%q): %w",
			clip(sessionID, 64), err)
	}
	if !at.Valid {
		return time.Time{}, false, nil
	}
	ts, err := parseTime(at.String)
	if err != nil {
		return time.Time{}, false, err
	}
	return ts, true, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// footprint
// ─────────────────────────────────────────────────────────────────────────────

// Touch 는 미커밋 발자국을 남긴다. first_at 은 보존하고 last_at 만 올린다.
//
// origin 을 뭉개지 않는다(스키마 PK 에 들어 있다) — 뭉개면 "선언했으나 안 건드림"과
// "선언 없이 건드림"이 구분되지 않는다.
// Beat 와 같은 이유로 last_at 도 뒤로 안 간다.
func (t *Tx) Touch(sessionID, path string, origin model.FootprintOrigin, at time.Time) error {
	if path == "" {
		return errors.New("발자국 경로가 비었다")
	}
	if at.IsZero() {
		at = nowStamp()
	}
	ts := fmtTime(at)
	_, err := t.tx.ExecContext(t.ctx, `
		INSERT INTO footprint(session_id, path, origin, first_at, last_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(session_id, path, origin) DO UPDATE SET last_at = excluded.last_at
		WHERE excluded.last_at > footprint.last_at`,
		sessionID, path, string(origin), ts, ts)
	if err != nil {
		return writeErr(err, writeTarget{
			Target: TargetFootprint, ID: sessionID,
			RefHint: "세션 " + clip(sessionID, 64),
		}, "발자국 기록 실패(session_id=%q path=%q origin=%q)",
			clip(sessionID, 64), clip(path, 200), clip(string(origin), 32))
	}
	return nil
}

// Touch 는 단발 트랜잭션으로 감싼 것이다.
func (s *Store) Touch(ctx context.Context, sessionID, path string, origin model.FootprintOrigin, at time.Time) error {
	return s.Tx(ctx, func(t *Tx) error { return t.Touch(sessionID, path, origin, at) })
}

// Footprints 는 세션의 발자국 전부를 origin 째로 낸다.
func (s *Store) Footprints(ctx context.Context, sessionID string) ([]model.Footprint, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT session_id, path, origin, first_at, last_at FROM footprint
		WHERE session_id = ? ORDER BY path, origin`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("발자국 조회 실패(session_id=%q): %w", clip(sessionID, 64), err)
	}
	defer rows.Close()

	var out []model.Footprint
	for rows.Next() {
		var f model.Footprint
		var origin, first, last string
		if err := rows.Scan(&f.SessionID, &f.Path, &origin, &first, &last); err != nil {
			return nil, fmt.Errorf("발자국 행 해석 실패: %w", err)
		}
		f.Origin = model.FootprintOrigin(origin)
		var err error
		if f.FirstAt, err = parseTime(first); err != nil {
			return nil, err
		}
		if f.LastAt, err = parseTime(last); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("발자국 목록 순회 실패: %w", err)
	}
	return out, nil
}

// FootprintPaths 는 세션이 만진 경로를 중복 없이 낸다(origin 은 접는다).
// 경로 겹침 판정의 입력이라 origin 축이 필요 없다 — 필요한 자리는 Footprints 를 쓴다.
func (s *Store) FootprintPaths(ctx context.Context, sessionID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT path FROM footprint WHERE session_id = ? ORDER BY path`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("발자국 경로 조회 실패(session_id=%q): %w", clip(sessionID, 64), err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("발자국 경로 행 해석 실패: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("발자국 경로 순회 실패: %w", err)
	}
	return out, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// ref_state · change_set
// ─────────────────────────────────────────────────────────────────────────────

// UpsertRefState 는 관측한 ref 하나를 기록한다. at 은 **관측 시각**이고
// UI 가 신선도를 그 값으로 표시한다 — 커밋 시각이 아니다.
func (t *Tx) UpsertRefState(r model.RefState) error {
	if r.At.IsZero() {
		r.At = nowStamp()
	}
	_, err := t.tx.ExecContext(t.ctx, `
		INSERT INTO ref_state(project, ref, sha, subject, at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(project, ref) DO UPDATE SET
		  sha = excluded.sha, subject = excluded.subject, at = excluded.at`,
		r.Project, r.Ref, r.SHA, nullStr(r.Subject), fmtTime(r.At))
	if err != nil {
		return writeErr(err, writeTarget{
			Target: TargetRefState, Project: r.Project, ID: r.Ref,
			RefHint: "프로젝트 " + clip(r.Project, 64),
		}, "ref 상태 기록 실패(project=%q ref=%q)", clip(r.Project, 64), clip(r.Ref, 200))
	}
	return nil
}

// UpsertRefState 는 단발 트랜잭션으로 감싼 것이다.
func (s *Store) UpsertRefState(ctx context.Context, r model.RefState) error {
	return s.Tx(ctx, func(t *Tx) error { return t.UpsertRefState(r) })
}

// GetRefState 는 ref 하나의 마지막 관측을 읽는다.
func (s *Store) GetRefState(ctx context.Context, project, ref string) (model.RefState, error) {
	var r model.RefState
	var subject sql.NullString
	var at string
	err := s.db.QueryRowContext(ctx,
		`SELECT project, ref, sha, subject, at FROM ref_state WHERE project = ? AND ref = ?`,
		project, ref).Scan(&r.Project, &r.Ref, &r.SHA, &subject, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return r, notFound(NFRefState, project, ref)
	}
	if err != nil {
		return r, fmt.Errorf("ref 상태 조회 실패(project=%q ref=%q): %w", clip(project, 64), clip(ref, 200), err)
	}
	r.Subject = str(subject)
	if r.At, err = parseTime(at); err != nil {
		return r, err
	}
	return r, nil
}

// UpsertChangeSet 은 두 커밋 사이의 변경 경로를 **불변으로** 보관한다.
// 브랜치가 지워져도 이 행은 남는다 — 파생 우선 설계의 유일한 약점(원본이 사라지면 계산 불가)에 대한 답이다.
func (t *Tx) UpsertChangeSet(c model.ChangeSet) error {
	paths := c.Paths
	if paths == nil {
		paths = []string{}
	}
	buf, err := json.Marshal(paths)
	if err != nil {
		return fmt.Errorf("변경집합 경로 직렬화 실패(project=%q %s..%s): %w",
			clip(c.Project, 64), clip(c.BaseSHA, 12), clip(c.HeadSHA, 12), err)
	}
	if c.ComputedAt.IsZero() {
		c.ComputedAt = nowStamp()
	}
	_, err = t.tx.ExecContext(t.ctx, `
		INSERT INTO change_set(project, base_sha, head_sha, paths, computed_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(project, base_sha, head_sha) DO UPDATE SET
		  paths = excluded.paths, computed_at = excluded.computed_at`,
		c.Project, c.BaseSHA, c.HeadSHA, string(buf), fmtTime(c.ComputedAt))
	if err != nil {
		return writeErr(err, writeTarget{
			Target: TargetChangeSet, Project: c.Project, ID: c.BaseSHA + ".." + c.HeadSHA,
			RefHint: "프로젝트 " + clip(c.Project, 64),
		}, "변경집합 기록 실패(project=%q %s..%s)",
			clip(c.Project, 64), clip(c.BaseSHA, 12), clip(c.HeadSHA, 12))
	}
	return nil
}

// UpsertChangeSet 은 단발 트랜잭션으로 감싼 것이다.
func (s *Store) UpsertChangeSet(ctx context.Context, c model.ChangeSet) error {
	return s.Tx(ctx, func(t *Tx) error { return t.UpsertChangeSet(c) })
}

// GetChangeSet 은 보관된 변경집합을 읽는다.
func (s *Store) GetChangeSet(ctx context.Context, project, baseSHA, headSHA string) (model.ChangeSet, error) {
	var c model.ChangeSet
	var raw, at string
	err := s.db.QueryRowContext(ctx, `
		SELECT project, base_sha, head_sha, paths, computed_at FROM change_set
		WHERE project = ? AND base_sha = ? AND head_sha = ?`, project, baseSHA, headSHA).
		Scan(&c.Project, &c.BaseSHA, &c.HeadSHA, &raw, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return c, notFound(NFChangeSet, project, clip(baseSHA, 12)+".."+clip(headSHA, 12))
	}
	if err != nil {
		return c, fmt.Errorf("변경집합 조회 실패(project=%q %s..%s): %w",
			clip(project, 64), clip(baseSHA, 12), clip(headSHA, 12), err)
	}
	if err := json.Unmarshal([]byte(raw), &c.Paths); err != nil {
		return c, fmt.Errorf("변경집합 경로 해석 실패(project=%q %s..%s): %w",
			clip(project, 64), clip(baseSHA, 12), clip(headSHA, 12), err)
	}
	if c.ComputedAt, err = parseTime(at); err != nil {
		return c, err
	}
	return c, nil
}
