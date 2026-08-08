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

// ListSessionEvents 는 세션 하나의 이벤트를 **오래된 순**으로 낸다. kind 가 비면 전 종류다.
//
// ★ ListEvents 와 정렬이 반대다. 그쪽은 "무슨 일이 있었나"(최신순)에 답하고, 이쪽은
// "이 키를 언제 냈나"(억제 판정)에 답한다. 최신순으로 주면 호출자가 뒤집어야 하고,
// 그 뒤집기를 잊으면 억제가 조용히 틀린다.
//
// ★ 상한이 없다. 상한을 걸면 오래된 키가 잘려 **이미 낸 처방이 다시 뜬다** —
// 이 함수가 막으려는 바로 그 사고다. 그것을 감당하는 근거가 "세션 하나의 처방 이벤트는
// 원리적으로 작다"인데, **조건마다 늘어나는 축이 달라 하나의 곱으로 적을 수 없다**
// (judge/prescribe.go 의 조건 다섯. 넷에서 다섯이 된 것은 lane-turn 이 들어오면서다):
//
//	unclaimed : 접미 없는 키 하나 — 세션당 1건이 상한이다(suppressed 가 무조건 누른다)
//	silent    : 접미 없는 키 하나이되 **판단 뒤에만 억제가 풀린다**(judge 의 suppressed 가
//	            silent 하나만 예외로 둔다) — 다시 뜨려면 그 사이에 판단이 하나 이상 남아야 한다
//	overlap   : 살아 있는 남의 세션마다 하나(overlap:<세션 id>)
//	outside   : 선언 경로 밖에서 만진 경로마다 하나(outside:<경로>)
//	lane-turn : **그 세션이 받은 줄 행마다** 하나(lane-turn:<줄 행 id>) —
//	            유일하게 "대상 수"가 아닌 축이다
//
// ★ 그래도 lane-turn 축이 작은 이유를 적어 둔다. 세션은 살아 있는 줄 행을 한 번에 하나만
// 가지므로(landing_queue_one_live_per_session 부분 유니크 인덱스 · EnqueueLanding 은 재진입에서
// 기존 행을 그대로 낸다) **새 줄 행은 앞 행이 닫힌 뒤에만 나온다.** 즉 이 축의 건수는 그 세션이
// 실제로 돈 랜딩 왕복 횟수이고, 한 왕복마다 세션의 쓰기 호출(land → report·leave·finish)이나
// 사람의 회수가 든다 — 저절로 늘어나는 축이 아니다.
func (s *Store) ListSessionEvents(ctx context.Context, sessionID, kind string, since time.Time) ([]model.Event, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, at, project, session_id, kind, payload FROM event
		WHERE session_id = ? AND (? = '' OR kind = ?) AND at >= ?
		ORDER BY at ASC, id ASC`,
		sessionID, kind, kind, fmtTime(since))
	if err != nil {
		return nil, fmt.Errorf("세션 이벤트 조회 실패(session_id=%q kind=%q): %w",
			clip(sessionID, 64), clip(kind, 64), err)
	}
	defer rows.Close()

	var out []model.Event
	for rows.Next() {
		var e model.Event
		var project, session sql.NullString
		var at string
		if err := rows.Scan(&e.ID, &at, &project, &session, &e.Kind, &e.Payload); err != nil {
			return nil, fmt.Errorf("세션 이벤트 행 해석 실패: %w", err)
		}
		e.Project, e.SessionID = str(project), str(session)
		if e.At, err = parseTime(at); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("세션 이벤트 순회 실패: %w", err)
	}
	return out, nil
}

// Reproduction 은 재생산율의 **원자료**다. 비율은 여기서 안 만든다.
//
// 0으로 나누는 갈래를 저장 계층에 두면 "마무리 0건"과 "R=0"이 같은 값으로 접힌다.
//
// ★ Finishes==0 은 **"이 창에 마무리가 없었다"**이지 "못 쟀다"가 아니다. 그 둘을 가르는
// 것은 호출자이고, 방식은 이 구조체가 아니라 **포인터의 유무**다(service.QueueBalance.Repro
// 가 nil 이면 못 쟀다). 앞 판은 여기에 "호출자가 Finishes==0 을 보고 못 쟀다를 낸다"고
// 적혀 있었는데, 그 뭉갬이 화면에서 집계 실패를 "표본 0"으로 원인 단정하게 만들었다.
type Reproduction struct {
	Finishes  int // 표본이 된 마무리 수(최근 N회)
	Followups int // 그 마무리들이 실은 후속 합(item.finish payload 의 count)
	Adds      int // 같은 구간의 독립 add 수
}

// QueueReproduction 은 최근 n회 마무리 기준 재생산율의 원자료다.
//
// ★ 왜 이 축이 있나. 실측(kweiza-cc-plugins · 이 원장): finish 88건이 followups 61건과
// 독립 add 53건을 낳아 R=1.30 이다 — 사이클 1회(pickup→작업→finish)마다 큐가 +0.29 이고,
// 그래서 **pickup 을 더 돌려서는 큐가 안 준다.** 큐를 줄이는 것은 pickup 이 아니라 finish 인데
// 그 finish 가 평균 1.29건을 다시 넣는다. 세션이 그 사실을 마무리하는 자리에서 봐야 한다.
//
// ★ **창을 id 로 자른다.** at 은 마이크로초 해상도라(timeLayout) 한 턴에 몰린 이벤트가 같은
// 값을 가질 수 있고, 그러면 경계에 걸친 add 가 창 안팎을 오간다. id 는 AUTOINCREMENT 라
// 단조이고 유일하다.
//
// ★ **add 구간도 함께 자른다.** 마무리는 최근 n회만 세면서 add 는 전 기간을 세면 R 이 실제보다
// 크게 나온다. AckReach 가 시각 절단 없이 전 기간을 누적해 겪은 것과 같은 부류다.
func (s *Store) QueueReproduction(ctx context.Context, project string, n int) (Reproduction, error) {
	var out Reproduction
	if n <= 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, payload FROM event
		WHERE project = ? AND kind = 'item.finish'
		ORDER BY id DESC LIMIT ?`, project, n)
	if err != nil {
		return out, fmt.Errorf("마무리 이벤트 조회 실패(project=%q): %w", clip(project, 64), err)
	}
	defer rows.Close()

	sinceID := int64(-1)
	for rows.Next() {
		var id int64
		var payload string
		if err := rows.Scan(&id, &payload); err != nil {
			return Reproduction{}, fmt.Errorf("마무리 이벤트 행 해석 실패: %w", err)
		}
		out.Finishes++
		if sinceID < 0 || id < sinceID {
			sinceID = id
		}
		// payload 는 자유 JSON 이라 스키마가 없다. 못 읽으면 **0으로 접는다** —
		// 이 축의 소비자는 "비면 안 센다"로 동작한다(eventItemID 와 같은 규율).
		var p struct {
			Count int `json:"count"`
		}
		if json.Unmarshal([]byte(payload), &p) == nil && p.Count > 0 {
			out.Followups += p.Count
		}
	}
	if err := rows.Err(); err != nil {
		return Reproduction{}, fmt.Errorf("마무리 이벤트 순회 실패: %w", err)
	}
	if out.Finishes == 0 {
		return out, nil // 표본이 없다. 0값 그대로 — **오류가 아니다**(호출자가 표본 0으로 읽는다)
	}

	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM event
		WHERE project = ? AND kind = 'item.add' AND id >= ?`,
		project, sinceID).Scan(&out.Adds); err != nil {
		return Reproduction{}, fmt.Errorf("추가 이벤트 조회 실패(project=%q): %w", clip(project, 64), err)
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
