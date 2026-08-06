package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 자원 배타와 논리 발번기.
//
// 이 파일에는 **자동 회수도 만료도 없다.** 그런 함수가 없는 것이 설계다 —
// "만료 = 죽음"이 성립하려면 생존 판정의 정본이 있어야 하는데 그게 없고,
// 실측으로 두 번 틀렸다(죽었다 판정한 세션이 그 뒤 6커밋 랜딩,
// 419분 무갱신 세션이 실제로는 17초 전에 살아 있었음).
// 강제 반납은 사유를 필수 인자로 받는 별도 함수(ForceReleaseResource)뿐이다.

// ─────────────────────────────────────────────────────────────────────────────
// 점유자
// ─────────────────────────────────────────────────────────────────────────────

// Holder 는 자원을 쥐는 주체다. 세션이거나 잡이고, **둘 다이거나 둘 다 아닐 수 없다**.
type Holder struct {
	SessionID string
	JobID     string
}

func (h Holder) String() string {
	switch {
	case h.SessionID != "":
		return "session=" + h.SessionID
	case h.JobID != "":
		return "job=" + h.JobID
	default:
		return "<비어 있음>"
	}
}

// ValidateHolder 는 점유자가 정확히 한 축만 채웠는지 본다. 순수 함수다.
// 스키마의 CHECK 와 같은 규율이지만 여기가 1차 방어다 — CHECK 위반 문구는
// "둘 다 비었다"와 "둘 다 찼다"를 구분해 주지 않는다.
func ValidateHolder(h Holder) error {
	switch {
	case h.SessionID != "" && h.JobID != "":
		return fmt.Errorf("점유자는 세션이거나 잡이어야 하는데 둘 다 왔다(session=%q job=%q)",
			clip(h.SessionID, 64), clip(h.JobID, 64))
	case h.SessionID == "" && h.JobID == "":
		return errors.New("점유자가 비었다 — session_id 나 job_id 중 하나가 필요하다")
	default:
		return nil
	}
}

// ResourceHeldError 는 이미 남이 쥐고 있는 자원을 잡으려 했을 때의 오류다.
// **점유자와 획득 시각을 담는다** — 점유자·경과를 못 내면 "누구에게 물어야 하나"에 답이 없다.
type ResourceHeldError struct {
	Project    string
	Resource   string
	Holder     Holder
	AcquiredAt time.Time
}

func (e *ResourceHeldError) Error() string {
	return fmt.Sprintf("자원 %s/%s 는 이미 %s 가 쥐고 있다(획득 %s, 경과 %s) "+
		"— 자동 회수는 없다. 강제 반납은 사유를 받는 ForceReleaseResource 다",
		e.Project, e.Resource, e.Holder, e.AcquiredAt.Format(time.RFC3339),
		time.Since(e.AcquiredAt).Round(time.Second))
}

// ─────────────────────────────────────────────────────────────────────────────
// resource_hold
// ─────────────────────────────────────────────────────────────────────────────

// AcquireResource 는 자원을 배타 점유한다.
//
// ★ 먼저 조회해 판정하지 않는다. 조회와 삽입 사이에 남이 잡을 수 있기 때문이다.
// 그냥 넣고 **부분 유니크 인덱스(resource_one_holder)의 위반을 받아** 점유자를 담은 오류로 바꾼다.
// 배타를 애플리케이션 판정이 아니라 DB 제약으로 두면 우회할 코드 자체가 없다.
//
// 위반 판별을 드라이버의 오류 코드로 하지 않는 이유: 삽입이 실패한 뒤 **살아 있는 점유자를
// 실제로 조회해** 있으면 ResourceHeldError, 없으면 원래 오류를 그대로 올린다.
// 어느 쪽이든 점유자를 조회해야 오류에 실을 수 있으므로 드라이버 내부에 기대는 대가가 순이익이 아니다.
//
// ★ 획득 시각을 **받는다**(Beat·Touch·EnqueueLanding 과 같은 문법 — 영값이면 지금. atStamp).
// 이 값이 곧 레인 절의 **획득 경과**이고, 스스로 실시계를 찍으면 주입된 시계로 그리는
// 화면과 갈려 그 칸이 통째로 거짓이 된다. 대기 경과와 **다른 표에서 오는 축이라**
// landing.go 만 고쳐서는 절반만 참이 된다(fd-lane-timestamps-ignore-injected-clock).
func (t *Tx) AcquireResource(project, resource string, h Holder, at time.Time) (model.ResourceHold, error) {
	if project == "" || resource == "" {
		return model.ResourceHold{}, fmt.Errorf("자원 좌표가 비었다(project=%q resource=%q)",
			clip(project, 64), clip(resource, 64))
	}
	if err := ValidateHolder(h); err != nil {
		return model.ResourceHold{}, err
	}
	at = atStamp(at)
	res, err := t.tx.ExecContext(t.ctx, `
		INSERT INTO resource_hold(project, resource, session_id, job_id, acquired_at, released_at, force_reason)
		VALUES (?, ?, ?, ?, ?, NULL, NULL)`,
		project, resource, nullStr(h.SessionID), nullStr(h.JobID), fmtTime(at))
	if err != nil {
		held, qErr := heldBy(t.ctx, t.tx, project, resource)
		if qErr == nil {
			return model.ResourceHold{}, &ResourceHeldError{
				Project:    project,
				Resource:   resource,
				Holder:     Holder{SessionID: held.SessionID, JobID: held.JobID},
				AcquiredAt: held.AcquiredAt,
			}
		}
		if !errors.Is(qErr, ErrNotFound) {
			// 점유자 조회까지 실패했다. 둘 다 남긴다 — 원인이 인덱스 위반이 아닐 수도 있다.
			return model.ResourceHold{}, fmt.Errorf(
				"자원 획득 실패(project=%q resource=%q holder=%s): %w (점유자 조회도 실패: %v)",
				clip(project, 64), clip(resource, 64), h, err, qErr)
		}
		// 살아 있는 점유자가 없는데 삽입이 실패했다 = 배타 위반이 아닌 다른 오류(FK·CHECK 등).
		// 등록 안 된 프로젝트·세션으로 잡으면 여기로 오고, 그것은 호출자가 고칠 거리다.
		return model.ResourceHold{}, writeErr(err, writeTarget{
			Target: TargetResourceHold, Project: project, ID: resource,
			RefHint: fmt.Sprintf("프로젝트 %s · %s", clip(project, 64), h),
		}, "자원 획득 실패(project=%q resource=%q holder=%s)",
			clip(project, 64), clip(resource, 64), h)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return model.ResourceHold{}, fmt.Errorf("자원 점유 id 확인 실패(project=%q resource=%q): %w",
			clip(project, 64), clip(resource, 64), err)
	}
	return model.ResourceHold{
		ID: id, Project: project, Resource: resource,
		SessionID: h.SessionID, JobID: h.JobID, AcquiredAt: at,
	}, nil
}

// AcquireResource 는 단발 트랜잭션으로 감싼 것이다.
//
// ★ 짝도 at 을 **통과시킨다**(Beat·Touch 가 선례다). 여기서만 삼키면 "Tx 로 잡으면 주입한
// 시계, Store 로 잡으면 실시계"가 되고, 그 비대칭은 시각이 틀어질 때까지 안 보인다.
func (s *Store) AcquireResource(ctx context.Context, project, resource string, h Holder, at time.Time) (model.ResourceHold, error) {
	var out model.ResourceHold
	err := s.Tx(ctx, func(t *Tx) error {
		var e error
		out, e = t.AcquireResource(project, resource, h, at)
		return e
	})
	return out, err
}

// heldBy 는 살아 있는 점유 행을 읽는다. 없으면 ErrNotFound.
func heldBy(ctx context.Context, q dbtx, project, resource string) (model.ResourceHold, error) {
	var r model.ResourceHold
	var session, job, force sql.NullString
	var at string
	err := q.QueryRowContext(ctx, `
		SELECT id, project, resource, session_id, job_id, acquired_at, force_reason
		FROM resource_hold
		WHERE project = ? AND resource = ? AND released_at IS NULL`, project, resource).
		Scan(&r.ID, &r.Project, &r.Resource, &session, &job, &at, &force)
	if errors.Is(err, sql.ErrNoRows) {
		return r, notFound(NFResourceHold, project, resource)
	}
	if err != nil {
		return r, fmt.Errorf("자원 점유 조회 실패(project=%q resource=%q): %w",
			clip(project, 64), clip(resource, 64), err)
	}
	r.SessionID, r.JobID, r.ForceReason = str(session), str(job), str(force)
	if r.AcquiredAt, err = parseTime(at); err != nil {
		return r, err
	}
	return r, nil
}

// HeldBy 는 자원의 현재 점유자를 낸다. 없으면 ErrNotFound.
func (s *Store) HeldBy(ctx context.Context, project, resource string) (model.ResourceHold, error) {
	return heldBy(ctx, s.db, project, resource)
}

// ReleaseResource 는 **자기 점유만** 반납한다.
// 남의 점유를 반납하려 하면 점유자를 담은 오류가 온다 — 조용히 성공시키면
// 배타가 거짓이 되고, 그 거짓은 남의 실행이 깨진 뒤에야 드러난다.
func (t *Tx) ReleaseResource(project, resource string, h Holder) error {
	if err := ValidateHolder(h); err != nil {
		return err
	}
	held, err := heldBy(t.ctx, t.tx, project, resource)
	if err != nil {
		return err
	}
	if held.SessionID != h.SessionID || held.JobID != h.JobID {
		return &ResourceHeldError{
			Project: project, Resource: resource,
			Holder:     Holder{SessionID: held.SessionID, JobID: held.JobID},
			AcquiredAt: held.AcquiredAt,
		}
	}
	if _, err := t.tx.ExecContext(t.ctx,
		`UPDATE resource_hold SET released_at = ? WHERE id = ?`, fmtTime(time.Now()), held.ID); err != nil {
		return fmt.Errorf("자원 반납 실패(project=%q resource=%q holder=%s): %w",
			clip(project, 64), clip(resource, 64), h, err)
	}
	return nil
}

// ReleaseResource 는 단발 트랜잭션으로 감싼 것이다.
func (s *Store) ReleaseResource(ctx context.Context, project, resource string, h Holder) error {
	return s.Tx(ctx, func(t *Tx) error { return t.ReleaseResource(project, resource, h) })
}

// ForceReleaseResource 는 남의 점유를 회수한다. **사유가 필수다.**
//
// 이 함수가 자동 회수의 대체재가 아니라는 점이 중요하다 — 사람이 부르고, 사유가 남는다.
// 사유가 원장에 남지 않는 회수는 나중에 "왜 남의 실행이 깨졌나"에 답할 수 없다.
func (t *Tx) ForceReleaseResource(project, resource, reason string) error {
	if reason == "" {
		return errors.New("강제 반납에는 사유가 필수다 — 자동 회수가 없는 이유가 여기 담긴다")
	}
	res, err := t.tx.ExecContext(t.ctx, `
		UPDATE resource_hold SET released_at = ?, force_reason = ?
		WHERE project = ? AND resource = ? AND released_at IS NULL`,
		fmtTime(time.Now()), reason, project, resource)
	if err != nil {
		return fmt.Errorf("자원 강제 반납 실패(project=%q resource=%q): %w",
			clip(project, 64), clip(resource, 64), err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("자원 강제 반납 결과 확인 실패(project=%q resource=%q): %w",
			clip(project, 64), clip(resource, 64), err)
	}
	if n == 0 {
		return notFound(NFResourceHold, project, resource)
	}
	return nil
}

// ForceReleaseResource 는 단발 트랜잭션으로 감싼 것이다.
func (s *Store) ForceReleaseResource(ctx context.Context, project, resource, reason string) error {
	return s.Tx(ctx, func(t *Tx) error { return t.ForceReleaseResource(project, resource, reason) })
}

// listHeld 는 지금 쥐어져 있는 자원 전부를 낸다.
// 획득이 오래된 순이고, 같은 시각이면 자원 이름순이다 — ORDER BY 가 여기 있으므로
// 그 사실의 출처는 이 자리다(공개 짝 둘은 이 문장을 소비자에게 보이게 옮겨 적은 것이다).
//
// Tx 안팎에서 같은 질의가 필요해서(finish 의 holds 읽기를 트랜잭션 안으로 옮기는 자리,
// ReleaseLaneRow 의 "점유가 있을 때만 회수" 판정) 자유 함수로 뺐다 — heldBy 와 같은 자리다.
func listHeld(ctx context.Context, q dbtx, project string) ([]model.ResourceHold, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT id, project, resource, session_id, job_id, acquired_at, force_reason
		FROM resource_hold
		WHERE project = ? AND released_at IS NULL
		ORDER BY acquired_at, resource`, project)
	if err != nil {
		return nil, fmt.Errorf("자원 점유 목록 조회 실패(project=%q): %w", clip(project, 64), err)
	}
	defer rows.Close()

	var out []model.ResourceHold
	for rows.Next() {
		var r model.ResourceHold
		var session, job, force sql.NullString
		var at string
		if err := rows.Scan(&r.ID, &r.Project, &r.Resource, &session, &job, &at, &force); err != nil {
			return nil, fmt.Errorf("자원 점유 행 해석 실패: %w", err)
		}
		r.SessionID, r.JobID, r.ForceReason = str(session), str(job), str(force)
		if r.AcquiredAt, err = parseTime(at); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("자원 점유 목록 순회 실패: %w", err)
	}
	return out, nil
}

// ListHeld 는 지금 쥐어져 있는 자원 전부를 트랜잭션 밖에서 낸다.
// 획득이 오래된 순이고, 같은 시각이면 자원 이름순이다.
//
// ★ 이 순서에 **분기하는** 호출자는 지금 없다 — pick 은 자원→점유자 맵으로 접고
// (heldResources), finish 는 자기 세션 것만 걸러 반납한다. 정렬을 뒤집어도 그 둘은
// 안 깨진다. 그런데도 공개 짝이 순서를 말해야 하는 이유는 이 슬라이스가 **재정렬 없이
// 그대로 사람에게 나가기 때문이다**: 보드의 막힘 절(web/page.go)과 MCP 꼬리의 자원 점유
// 줄(mcpsrv/render.go)이 둘 다 받은 순서대로 이어 붙인다. 즉 순서가 바뀔 때 상하는 것은
// 호출자의 판정이 아니라 **사람이 보는 화면**이다.
//
// 그래서 이 두 줄은 "계약"이 아니라 **지금 사실**이고, 사실인 채로 두기 위해
// store_test.go 의 TestListHeldOrdersByAcquisitionThenResource 가 이것을 잠근다 —
// 그 시험이 서기 전까지는 ORDER BY 를 DESC 로 뒤집어도 다섯 패키지가 전부 초록이었다.
func (s *Store) ListHeld(ctx context.Context, project string) ([]model.ResourceHold, error) {
	return listHeld(ctx, s.db, project)
}

// ListHeld 는 트랜잭션 안에서 읽는다.
// 정렬은 Store.ListHeld 와 같다(획득이 오래된 순, 같은 시각이면 자원 이름순) — 질의를
// 자유 함수 하나로 공유하므로 둘이 갈릴 자리가 없다.
//
// finish 처럼 "점유 목록을 보고 그것을 근거로 같은 트랜잭션에서 반납까지 하는" 호출자를 위한
// 것이다 — 밖에서 읽고 트랜잭션 안에서 반납하면 그 사이에 남이 잡을 수 있고, 그러면
// **남의 점유를 반납한다.**
func (t *Tx) ListHeld(project string) ([]model.ResourceHold, error) {
	return listHeld(t.ctx, t.tx, project)
}

// HeldBy 는 트랜잭션 안에서 자원의 현재 점유자를 낸다. 없으면 ErrNotFound.
//
// ReleaseLaneRow 의 "점유가 있을 때만 회수" 판정을 트랜잭션 안에 두기 위한 것이다 —
// 밖에서 판정하면 그 사이에 남이 잡은 점유를 반납하게 된다.
func (t *Tx) HeldBy(project, resource string) (model.ResourceHold, error) {
	return heldBy(t.ctx, t.tx, project, resource)
}

// ─────────────────────────────────────────────────────────────────────────────
// counter — 원자적 발번
// ─────────────────────────────────────────────────────────────────────────────

// NextCounter 는 다음 번호를 발급한다. **원자적이다.**
//
// ★ 이 함수가 락이 원리적으로 못 지키는 논리 카운터를 닫는 자리다.
// 파일 접근을 직렬화해도 발번은 안 지켜진다 — 두 세션이 각자 "지금 값"을 읽고
// 각자 +1 하면 둘 다 같은 번호를 쓴다. 실제로 같은 날 두 세션이 같은 개정 차수를 써서 뒤가 물렀다.
//
// 읽고-더하고-쓰기를 **한 SQL 문**으로 둔다.
//
// ★ 정직하게 적는다: 이 트랜잭션은 BEGIN IMMEDIATE 라 두 문으로 쪼개도 **지금은** 값이 겹치지 않고,
// 실제로 두 문으로 바꿔 보아도 동시 발번 시험이 초록으로 남는다(변이 주입으로 확인). 즉 이 시험은
// 이 축을 못 잡는다. 그래도 한 문으로 두는 이유는 정합성의 근거를 **여기**에 두기 위해서다 —
// 쪼개면 근거가 "DSN 의 잠금 모드"라는 먼 곳으로 옮겨가고, 누가 그 모드를 건드리는 순간
// 발번이 조용히 깨지는데 그 연결을 아무도 안 본다.
//
// 첫 발급은 1이다(0은 "아직 안 씀"과 구분돼야 한다).
func (t *Tx) NextCounter(project, name string) (int64, error) {
	if project == "" || name == "" {
		return 0, fmt.Errorf("카운터 좌표가 비었다(project=%q name=%q)",
			clip(project, 64), clip(name, 64))
	}
	var v int64
	err := t.tx.QueryRowContext(t.ctx, `
		INSERT INTO counter(project, name, value) VALUES (?, ?, 1)
		ON CONFLICT(project, name) DO UPDATE SET value = value + 1
		RETURNING value`, project, name).Scan(&v)
	if err != nil {
		return 0, writeErr(err, writeTarget{
			Target: TargetCounter, Project: project, ID: name,
			RefHint: "프로젝트 " + clip(project, 64),
		}, "발번 실패(project=%q name=%q)", clip(project, 64), clip(name, 64))
	}
	return v, nil
}

// NextCounter 는 단발 트랜잭션으로 감싼 것이다.
// 이 경로가 실제 발번 경로다 — 트랜잭션이 BEGIN IMMEDIATE 라 동시 호출이 직렬화된다.
func (s *Store) NextCounter(ctx context.Context, project, name string) (int64, error) {
	var v int64
	err := s.Tx(ctx, func(t *Tx) error {
		var e error
		v, e = t.NextCounter(project, name)
		return e
	})
	return v, err
}

// PeekCounter 는 현재 값을 읽기만 한다. 없으면 0.
// **발번이 아니다** — 이 값을 읽어 +1 해서 쓰면 위 주석의 사고가 그대로 재현된다.
func (s *Store) PeekCounter(ctx context.Context, project, name string) (int64, error) {
	var v int64
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM counter WHERE project = ? AND name = ?`, project, name).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("카운터 조회 실패(project=%q name=%q): %w",
			clip(project, 64), clip(name, 64), err)
	}
	return v, nil
}
