package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 랜딩 레인의 순서 큐(landing_queue).
//
// ★ 배타는 이 표가 아니라 resource_hold 가 지킨다. landing_queue 에는 점유 여부도
// granted_at 도 없다 — "내가 레인을 쥐었나"는 HeldBy(project,"landing") 하나로만 파생한다.
// 사본을 두면 갈릴 자리가 생기고, 갈렸을 때 어느 쪽이 참인지 정하는 문장이 없다.
//
// ★ CloseLandingRow·CloseLandingRowBySession 은 자원을 안 건드린다. 레인 점유의
// 반납(ReleaseResource·ForceReleaseResource)과 줄 행 닫기를 같은 트랜잭션에 묶는 것은
// 상위 계층(service)의 몫이다. 여기서 함께 건드리면 "줄 행만 닫고 싶은" 대기 중 회수가
// 점유가 없다는 이유로 실패한다.
//
// ★ 만료도 자동 회수도 없다 — resource_hold·claim 과 같은 규율이다.

// ValidateLandingLeave 는 줄에서 빠지는 인자가 성립하는지 본다. 순수 함수다.
//
// 스키마의 CHECK 가 최종 방어이지 1차 방어가 아니다 — DB 제약 위반 문구는 무엇이
// 왜 빠졌는지 말하지 않는다. 여기서 먼저 거절해야 호출부가 그 자리에서 이유를 안다
// (ValidateFinish·ValidateSessionState 와 같은 규율).
func ValidateLandingLeave(kind model.LandingLeftKind, detail string) error {
	switch kind {
	case model.LandingLeftOK, model.LandingLeftFinish:
		// 정상 종료다 — "왜"가 종류 자체에 들어 있으므로 사유가 면제된다.
		return nil
	case model.LandingLeftFail, model.LandingLeftLeave, model.LandingLeftForce:
		if detail == "" {
			return fmt.Errorf("종류 %q 는 사유가 필수다 — 사유 없는 이탈은 나중에 되짚을 수 없다",
				kind)
		}
		return nil
	default:
		return fmt.Errorf("알 수 없는 랜딩 줄 이탈 종류 %q (ok|fail|leave|finish|force 중 하나여야 한다)",
			clip(string(kind), 32))
	}
}

// landingCols 는 세 질의(살아 있는 행 조회·맨 앞 조회·전체 나열)가 공유하는 컬럼 목록이다.
// 두 벌로 두면 컬럼을 늘린 날 한쪽만 고쳐지고, 그 비대칭은 스캔이 죽을 때까지 안 보인다.
const landingCols = `id, project, session_id, enqueued_at, left_at, left_kind, left_detail`

func scanLandingRow(sc interface{ Scan(...any) error }) (model.LandingRow, error) {
	var r model.LandingRow
	var enqueued string
	var left, kind, detail sql.NullString
	if err := sc.Scan(&r.ID, &r.Project, &r.SessionID, &enqueued, &left, &kind, &detail); err != nil {
		return r, err
	}
	var err error
	if r.EnqueuedAt, err = parseTime(enqueued); err != nil {
		return r, err
	}
	if r.LeftAt, err = parseNullTime(left); err != nil {
		return r, err
	}
	r.LeftKind = model.LandingLeftKind(str(kind))
	r.LeftDetail = str(detail)
	return r, nil
}

// EnqueueLanding 은 랜딩 줄에 선다. **재진입 안전하다** — 이미 살아 있는 줄 행이 있으면
// 새로 넣지 않고 그것을 그대로 돌려준다.
//
// ★ 먼저 조회해 판정하지 않는다(AcquireResource 와 같은 이유). 그냥 넣고 **부분 유니크
// 인덱스의 위반을 받아** 이미 있는 행을 조회한다. 재진입을 앱 판정으로 두면 조회와 삽입
// 사이에 같은 세션의 다른 호출이 끼어들 수 있고, 그때 한 세션이 줄을 두 자리 차지해
// 순번 자체가 거짓이 된다.
//
// Store 짝을 두지 않는다 — 이 호출은 항상 service 가 다른 쓰기와 묶어 부르는 것이
// 설계 의도다(레인이 비어 있으면 곧바로 AcquireResource 를 같은 트랜잭션에서 잇는다).
func (t *Tx) EnqueueLanding(project, sessionID string) (model.LandingRow, error) {
	if project == "" || sessionID == "" {
		return model.LandingRow{}, fmt.Errorf("랜딩 줄 좌표가 비었다(project=%q session=%q)",
			clip(project, 64), clip(sessionID, 64))
	}
	now := nowStamp()
	res, err := t.tx.ExecContext(t.ctx, `
		INSERT INTO landing_queue(project, session_id, enqueued_at, left_at, left_kind, left_detail)
		VALUES (?, ?, ?, NULL, NULL, NULL)`,
		project, sessionID, fmtTime(now))
	if err != nil {
		row, qErr := liveLandingRow(t.ctx, t.tx, project, sessionID)
		if qErr == nil {
			// 이미 서 있다. 순번(id)과 대기 시작 시각을 그대로 낸다 —
			// 여기서 새 행을 만들면 다시 부를 때마다 맨 뒤로 밀린다.
			return row, nil
		}
		if !errors.Is(qErr, ErrNotFound) {
			return model.LandingRow{}, fmt.Errorf(
				"랜딩 줄 서기 실패(project=%q session=%q): %w (살아 있는 줄 행 조회도 실패: %v)",
				clip(project, 64), clip(sessionID, 64), err, qErr)
		}
		// 살아 있는 행이 없는데 삽입이 실패했다 = 재진입이 아닌 다른 오류(FK·CHECK 등).
		return model.LandingRow{}, writeErr(err, writeTarget{
			Target: TargetLandingQueue, Project: project, ID: sessionID,
			RefHint: fmt.Sprintf("프로젝트 %s · 세션 %s", clip(project, 64), clip(sessionID, 64)),
		}, "랜딩 줄 서기 실패(project=%q session=%q)", clip(project, 64), clip(sessionID, 64))
	}
	id, err := res.LastInsertId()
	if err != nil {
		return model.LandingRow{}, fmt.Errorf("랜딩 줄 순번 확인 실패(project=%q session=%q): %w",
			clip(project, 64), clip(sessionID, 64), err)
	}
	return model.LandingRow{ID: id, Project: project, SessionID: sessionID, EnqueuedAt: now}, nil
}

// liveLandingRow 는 세션의 살아 있는(left_at IS NULL) 줄 행을 읽는다. 없으면 ErrNotFound.
func liveLandingRow(ctx context.Context, q dbtx, project, sessionID string) (model.LandingRow, error) {
	row := q.QueryRowContext(ctx, `
		SELECT `+landingCols+` FROM landing_queue
		WHERE project = ? AND session_id = ? AND left_at IS NULL`, project, sessionID)
	r, err := scanLandingRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return r, notFound(NFLiveLandingRow, project, sessionID)
	}
	if err != nil {
		return r, fmt.Errorf("살아 있는 랜딩 줄 행 조회 실패(project=%q session=%q): %w",
			clip(project, 64), clip(sessionID, 64), err)
	}
	return r, nil
}

// LiveLandingRow 는 세션이 지금 줄에 서 있는지를 낸다. 없으면 ErrNotFound.
func (t *Tx) LiveLandingRow(project, sessionID string) (model.LandingRow, error) {
	return liveLandingRow(t.ctx, t.tx, project, sessionID)
}

// LiveLandingRow 는 트랜잭션 밖에서 읽는다.
func (s *Store) LiveLandingRow(ctx context.Context, project, sessionID string) (model.LandingRow, error) {
	return liveLandingRow(ctx, s.db, project, sessionID)
}

// lastLandingRow 는 세션의 **가장 최근** 줄 행을 읽는다(살아 있든 닫혔든). 없으면 ErrNotFound.
//
// 살아 있는 행은 세션당 하나뿐이고(부분 유니크 인덱스), 다시 서려면 먼저 닫혀야 하므로
// id 가 가장 큰 행은 "살아 있으면 그 행, 아니면 마지막으로 닫힌 행"이다.
//
// ★ LiveLandingRow 로 대신할 수 없는 자리가 있다: 회수된 세션에게 **왜** 레인을 잃었는지
// 답하려면 이미 닫힌 행의 left_detail 을 읽어야 한다. 그 사유는 landing_queue 에만 있다
// (resource_hold.force_reason 은 released_at 이 찍힌 행이라 heldBy 로 안 읽히고,
// 판단은 사람이 읽는 넓은 기록이지 응답이 파싱할 자리가 아니다).
func lastLandingRow(ctx context.Context, q dbtx, project, sessionID string) (model.LandingRow, error) {
	row := q.QueryRowContext(ctx, `
		SELECT `+landingCols+` FROM landing_queue
		WHERE project = ? AND session_id = ?
		ORDER BY id DESC LIMIT 1`, project, sessionID)
	r, err := scanLandingRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return r, notFoundNote(NFLiveLandingRow, fmt.Sprintf("프로젝트 %s · 세션 %s 의 줄 행(닫힌 것 포함)에 해당하는",
			clip(project, 64), clip(sessionID, 64)))
	}
	if err != nil {
		return r, fmt.Errorf("마지막 랜딩 줄 행 조회 실패(project=%q session=%q): %w",
			clip(project, 64), clip(sessionID, 64), err)
	}
	return r, nil
}

// LastLandingRow 는 트랜잭션 안에서 세션의 마지막 줄 행을 낸다.
//
// 트랜잭션 안에 두는 이유는 HeldBy 와 같다 — "내가 점유자인가"와 "내 행이 어떻게 닫혔나"를
// 밖에서 따로 읽으면 그 사이에 회수가 끼어들어 두 답이 서로 다른 순간을 가리킨다.
func (t *Tx) LastLandingRow(project, sessionID string) (model.LandingRow, error) {
	return lastLandingRow(t.ctx, t.tx, project, sessionID)
}

// frontLandingRow 는 줄의 맨 앞(살아 있는 행 중 가장 작은 id)을 읽는다. 없으면 ErrNotFound.
func frontLandingRow(ctx context.Context, q dbtx, project string) (model.LandingRow, error) {
	row := q.QueryRowContext(ctx, `
		SELECT `+landingCols+` FROM landing_queue
		WHERE project = ? AND left_at IS NULL
		ORDER BY id LIMIT 1`, project)
	r, err := scanLandingRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return r, notFoundNote(NFLiveLandingRow, fmt.Sprintf("프로젝트 %s 줄의 맨 앞에 해당하는", clip(project, 64)))
	}
	if err != nil {
		return r, fmt.Errorf("랜딩 줄 맨 앞 조회 실패(project=%q): %w", clip(project, 64), err)
	}
	return r, nil
}

// FrontLandingRow 는 줄의 맨 앞을 낸다. 순서 집행(누가 다음 차례인가)이 걸리는 유일한 자리다.
func (t *Tx) FrontLandingRow(project string) (model.LandingRow, error) {
	return frontLandingRow(t.ctx, t.tx, project)
}

// FrontLandingRow 는 트랜잭션 밖에서 읽는다.
func (s *Store) FrontLandingRow(ctx context.Context, project string) (model.LandingRow, error) {
	return frontLandingRow(ctx, s.db, project)
}

// CloseLandingRow 는 줄 행 하나를 id 로 닫는다. **자원을 안 건드린다** —
// 레인 점유의 반납은 상위 계층이 같은 트랜잭션에서 별도로 한다(파일 위쪽 주석 참조).
func (t *Tx) CloseLandingRow(project string, id int64, kind model.LandingLeftKind, detail string) error {
	if err := ValidateLandingLeave(kind, detail); err != nil {
		return err
	}
	res, err := t.tx.ExecContext(t.ctx, `
		UPDATE landing_queue SET left_at = ?, left_kind = ?, left_detail = ?
		WHERE project = ? AND id = ? AND left_at IS NULL`,
		fmtTime(time.Now()), string(kind), nullStr(detail), project, id)
	if err != nil {
		return fmt.Errorf("랜딩 줄 행 닫기 실패(project=%q id=%d kind=%q): %w",
			clip(project, 64), id, kind, err)
	}
	return affectedOne(res, NFLiveLandingRow, project, strconv.FormatInt(id, 10))
}

// CloseLandingRow 는 단발 트랜잭션으로 감싼 것이다.
func (s *Store) CloseLandingRow(ctx context.Context, project string, id int64, kind model.LandingLeftKind, detail string) error {
	return s.Tx(ctx, func(t *Tx) error { return t.CloseLandingRow(project, id, kind, detail) })
}

// CloseLandingRowBySession 은 세션의 살아 있는 줄 행을 닫는다.
//
// ★ **살아 있는 행이 없으면 무동작으로 통과한다.** affectedOne 을 안 쓰는 이유가 이것이다 —
// 줄을 한 번도 안 선 세션이 마무리(finish)하는 것은 정상이고, 여기서 ErrNotFound 를 올리면
// finish 트랜잭션이 통째로 롤백돼 핸드오프 판단(이 레포가 "원리적으로 파생 불가한 유일한
// 자산"이라 부르는 값)이 함께 사라진다. item.go 의 ReleaseClaim(이미 반납된 선점을 멱등하게
// 통과시키는 자리)이 같은 규율의 선례다.
func (t *Tx) CloseLandingRowBySession(project, sessionID string, kind model.LandingLeftKind, detail string) error {
	if err := ValidateLandingLeave(kind, detail); err != nil {
		return err
	}
	if _, err := t.tx.ExecContext(t.ctx, `
		UPDATE landing_queue SET left_at = ?, left_kind = ?, left_detail = ?
		WHERE project = ? AND session_id = ? AND left_at IS NULL`,
		fmtTime(time.Now()), string(kind), nullStr(detail), project, sessionID); err != nil {
		return fmt.Errorf("랜딩 줄 행 닫기 실패(project=%q session=%q kind=%q): %w",
			clip(project, 64), clip(sessionID, 64), kind, err)
	}
	return nil
}

// CloseLandingRowBySession 은 단발 트랜잭션으로 감싼 것이다.
func (s *Store) CloseLandingRowBySession(ctx context.Context, project, sessionID string, kind model.LandingLeftKind, detail string) error {
	return s.Tx(ctx, func(t *Tx) error { return t.CloseLandingRowBySession(project, sessionID, kind, detail) })
}

// listLandingQueue 는 지금 줄에 서 있는 행 전부를 순번(오래된 순)으로 읽는다.
//
// ★ 생존 창으로 거르지 않는다 — 맨 앞 세션이 창 밖(무갱신)일 때가 정확히 사람이 그
// 사실을 봐야 하는 순간이다. 거르면 "줄이 비었는데 아무도 못 잡는다"가 된다.
//
// Tx 안팎에서 같은 질의가 필요해서 자유 함수로 뒀다(listHeld·heldBy 와 같은 자리).
func listLandingQueue(ctx context.Context, q dbtx, project string) ([]model.LandingRow, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT `+landingCols+` FROM landing_queue
		WHERE project = ? AND left_at IS NULL
		ORDER BY id`, project)
	if err != nil {
		return nil, fmt.Errorf("랜딩 줄 조회 실패(project=%q): %w", clip(project, 64), err)
	}
	defer rows.Close()

	var out []model.LandingRow
	for rows.Next() {
		r, err := scanLandingRow(rows)
		if err != nil {
			return nil, fmt.Errorf("랜딩 줄 행 해석 실패: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("랜딩 줄 목록 순회 실패: %w", err)
	}
	return out, nil
}

// ListLandingQueue 는 트랜잭션 밖에서 줄을 읽는다(보드·화면).
func (s *Store) ListLandingQueue(ctx context.Context, project string) ([]model.LandingRow, error) {
	return listLandingQueue(ctx, s.db, project)
}

// ListLandingQueue 는 트랜잭션 안에서 줄을 읽는다.
//
// ★ 순번을 이 트랜잭션에서 세려면 반드시 이쪽이어야 한다. 밖에서 읽으면 **방금 넣은
// 내 행이 아직 커밋 전이라 안 보이고**, 그러면 자기 자신이 빠진 줄에서 순번을 세게 된다.
func (t *Tx) ListLandingQueue(project string) ([]model.LandingRow, error) {
	return listLandingQueue(t.ctx, t.tx, project)
}
