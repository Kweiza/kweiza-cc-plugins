package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// event — 추가 전용 감사·계측 원장.
//
// 기존 구조는 반납이 `rm -rf` 라 흔적이 없었고, 그래서 "기다렸으나 안 적은 세션"이
// 원리적으로 관측 불가였다. 이 표가 그 축을 연다.
//
// ★ UPDATE·DELETE 함수가 없다. 스키마 트리거가 막지만 애초에 호출부가 없어야 한다.

// LogEvent 는 계측 이벤트를 남긴다. **실패해도 상위 동작을 막지 않는다.**
//
// 계측이 기능을 죽이면 안 되므로 오류를 올리지 않는다. 다만 **삼키지도 않는다** —
// WARN 으로 원인 전문을 남긴다. 조용히 버리면 "이벤트가 안 쌓인다"는 사실 자체를
// 아무도 모르게 되고, 그러면 §10 의 지표가 전부 거짓 0이 된다.
//
// ★ 호출자의 트랜잭션에 얹지 않고 별도 커넥션으로 쓴다. 상위 작업이 롤백돼도
// "무엇을 시도했다 실패했나"는 남아야 하기 때문이다 — 그것이 감사 원장의 존재 이유다.
func (s *Store) LogEvent(ctx context.Context, kind, project, sessionID string, payload any) {
	if err := s.TryLogEvent(ctx, kind, project, sessionID, payload); err != nil {
		s.log.Warn("계측 이벤트 기록 실패(상위 동작은 계속한다)",
			"kind", clip(kind, 64), "project", clip(project, 64),
			"session_id", clip(sessionID, 64), "error", err)
	}
}

// TryLogEvent 는 LogEvent 와 같은 일을 하되 오류를 돌려준다.
// 시험과, 기록 실패 자체가 판정 대상인 자리에서만 쓴다.
func (s *Store) TryLogEvent(ctx context.Context, kind, project, sessionID string, payload any) error {
	if kind == "" {
		return fmt.Errorf("이벤트 kind 가 비었다")
	}
	body := "{}"
	if payload != nil {
		buf, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("이벤트 payload 직렬화 실패(kind=%q): %w", clip(kind, 64), err)
		}
		body = string(buf)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO event(at, project, session_id, kind, payload) VALUES (?, ?, ?, ?, ?)`,
		fmtTime(time.Now()), nullStr(project), nullStr(sessionID), kind, body); err != nil {
		return fmt.Errorf("이벤트 기록 실패(kind=%q project=%q session=%q): %w",
			clip(kind, 64), clip(project, 64), clip(sessionID, 64), err)
	}
	return nil
}

// ListEvents 는 종류로 걸러 최신순으로 낸다. kind 가 비면 전 종류다.
func (s *Store) ListEvents(ctx context.Context, kind string, since time.Time, limit int) ([]model.Event, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, at, project, session_id, kind, payload FROM event
		WHERE (? = '' OR kind = ?) AND at >= ?
		ORDER BY at DESC, id DESC LIMIT ?`,
		kind, kind, fmtTime(since), limit)
	if err != nil {
		return nil, fmt.Errorf("이벤트 조회 실패(kind=%q): %w", clip(kind, 64), err)
	}
	defer rows.Close()

	var out []model.Event
	for rows.Next() {
		var e model.Event
		var project, session sql.NullString
		var at string
		if err := rows.Scan(&e.ID, &at, &project, &session, &e.Kind, &e.Payload); err != nil {
			return nil, fmt.Errorf("이벤트 행 해석 실패: %w", err)
		}
		e.Project, e.SessionID = str(project), str(session)
		if e.At, err = parseTime(at); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("이벤트 목록 순회 실패: %w", err)
	}
	return out, nil
}

// CountEvents 는 종류별 건수다. §10 의 지표(세션당 쓰기 호출 수 등)가 이걸로 나온다.
func (s *Store) CountEvents(ctx context.Context, kind string, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM event WHERE (? = '' OR kind = ?) AND at >= ?`,
		kind, kind, fmtTime(since)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("이벤트 건수 조회 실패(kind=%q): %w", clip(kind, 64), err)
	}
	return n, nil
}
