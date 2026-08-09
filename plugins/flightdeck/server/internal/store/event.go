package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
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

// closeDeclarationScanLimit 는 CloseDeclarationsByItem 이 한 번에 훑는 item.finish 행의 상한이다.
//
// ★ 근거를 수로 적는다(실측 2026-08-09 · ~/.flightdeck/fd.db). item.finish 는 384건이고
// 프로젝트별 최대 속도는 context-platform 의 245건/5.26일 = 46.6건/일 이다. 5000건은 그
// 속도로 **107일**이다. 이 축이 겨냥하는 인구는 "롤백된 뒤 아직 열려 있는 항목"이고, 열린
// 항목 나이의 실측 최대는 9.6일 · 사고 사례는 42시간이었다 — 107일은 그 11배다.
//
// ★ **성능 손잡이가 아니다.** EXPLAIN QUERY PLAN 은 event_by_kind(kind=?) 를 타고
// kind='item.finish' 행 전부를 훑은 뒤 project 로 거른다 — 훑는 양은 LIMIT 이 아니라 원장의
// 크기가 정한다(실측 384행에 1.2ms, LIMIT 500~20000 사이에 차이가 없다). LIMIT 이 실제로 무는
// 것은 정렬 버퍼와 JSON 파싱 횟수뿐이다. 그래서 넉넉히 잡되, 이 수가 실제로 물리기 시작하는
// 때(원장이 지금의 13배)를 상한을 다시 잴 신호로 남긴다.
const closeDeclarationScanLimit = 5000

// CloseDeclarationsByItem 은 이 프로젝트에서 "이 항목을 닫는다"고 선언된 이력을 항목별로 접는다.
//
// ★ 무엇을 긁나. kind='item.finish' 하나다. 그 이벤트는 Finish 가 트랜잭션의 **첫 문장**에서
// 예약하고(service/finish.go) Tx.LogEvent 는 롤백 갈래에서도 흘러가므로(store.go 의 flushDeferred),
// 이 원장에는 **성공한 마무리와 롤백된 마무리가 같이** 들어 있다. 둘을 가르는 것은 항목의
// 상태이고 그 판정은 여기서 하지 않는다 — 이 함수는 원자료만 낸다.
//
// ★ **앵커도 항목 존재 판정도 여기서 하지 않는다.** 시간 앵커(그 항목 CreatedAt 이후의 선언만
// 센다)와 후보 목록에 없는 id 버리기는 service 가 한다. 그쪽은 이미 items 를 손에 쥐고 있어
// 추가 조회가 0이고, 여기서 하려면 json_extract 를 조인 조건에 넣어야 하는데 그 선례가 이
// 저장소에 0건이다. 그래서 이 반환값에는 **좌표가 어긋난 선언(실측 3건: 다른 프로젝트에서 친
// finish)과 지웠다 다시 만든 id 의 옛 선언이 그대로 들어 있다.**
//
// ★ **이 수는 정확한 수가 아니라 하한이다.** flushDeferred 는 트랜잭션이 물던 ctx 를 그대로 쓰고
// LogEvent 는 쓰기 실패를 WARN 으로만 삼키므로, 클라이언트가 끊겨 ctx 가 취소되면 행이 안 써진다.
// BeginTx 자체가 실패하면 클로저를 안 부르므로 이벤트가 아예 안 남는다. 소비자의 문구가
// "정확히 N건"이 아니라 "적어도 N건"으로 말해야 한다.
//
// ★ payload 를 못 읽은 행은 **안 센다**(eventItemID · QueueReproduction 과 같은 규율).
// payload 는 자유 JSON 이라 스키마가 없고, 못 읽은 것을 세면 어느 항목의 것인지 모르는 채로
// 수만 늘어 화면이 관측하지 않은 것을 단정하게 된다.
func (s *Store) CloseDeclarationsByItem(ctx context.Context, project string) (map[string]model.CloseDeclaration, error) {
	return s.closeDeclarationsByItem(ctx, project, closeDeclarationScanLimit)
}

// closeDeclarationsByItem 은 상한을 받는 속살이다. 상한을 시험이 못 밟으면 그 수는 근거가
// 아니라 장식이다 — 5000행을 심는 시험은 너무 느리므로 여기로 인자를 연다.
func (s *Store) closeDeclarationsByItem(ctx context.Context, project string, limit int) (map[string]model.CloseDeclaration, error) {
	if limit <= 0 {
		return map[string]model.CloseDeclaration{}, nil
	}
	// ★ 창을 id 로 자른다. event 인덱스는 (kind,at)·(session_id,at) 뿐이고, at 은 마이크로초
	// 해상도라 한 턴에 몰린 이벤트가 같은 값을 가질 수 있다. id 는 AUTOINCREMENT 라 단조이고
	// 유일하다 — QueueReproduction 이 같은 이유로 같은 선택을 했다.
	rows, err := s.db.QueryContext(ctx, `
		SELECT at, session_id, payload FROM event
		WHERE project = ? AND kind = 'item.finish'
		ORDER BY id DESC LIMIT ?`, project, limit)
	if err != nil {
		return nil, fmt.Errorf("종료 선언 조회 실패(project=%q): %w", clip(project, 64), err)
	}
	defer rows.Close()

	out := make(map[string]model.CloseDeclaration)
	for rows.Next() {
		var at string
		var session sql.NullString
		var payload string
		if err := rows.Scan(&at, &session, &payload); err != nil {
			return nil, fmt.Errorf("종료 선언 행 해석 실패: %w", err)
		}
		var p struct {
			Item string `json:"item"`
			Mode string `json:"mode"`
		}
		if json.Unmarshal([]byte(payload), &p) != nil {
			continue
		}
		item := strings.TrimSpace(p.Item)
		if item == "" {
			continue
		}
		d := out[item]
		switch p.Mode {
		case string(model.ItemDone):
			d.Done++
		case string(model.ItemDropped):
			d.Dropped++
		default:
			// mode 를 모르면 안 센다. 처방이 mode 로 갈리므로 모르는 값을 한쪽에 몰면
			// 화면이 관측하지 않은 원인을 단정한다.
			continue
		}
		// ORDER BY id DESC 라 이 항목을 **처음** 만나는 행이 가장 최근 선언이다.
		if d.LastMode == "" {
			t, err := parseTime(at)
			if err != nil {
				return nil, err
			}
			d.Last, d.LastSession, d.LastMode = t, str(session), p.Mode
		}
		out[item] = d
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("종료 선언 순회 실패: %w", err)
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
