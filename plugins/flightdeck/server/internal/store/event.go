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

// 트랜잭션 결말 표시 — 예약 이벤트가 "그래서 그 트랜잭션이 어떻게 끝났나"를 스스로 말한다.
//
// ★ 왜 필요한가. Tx.LogEvent 로 예약된 이벤트는 롤백 갈래에서도 흘러간다(store.go 의
// flushDeferred). 그것은 결함이 아니라 "무엇을 시도했다 실패했나"를 남기려는 설계이고,
// 그 대가로 원장의 item.finish 에는 **성공한 마무리와 롤백된 마무리가 같이** 들어 있다.
// 결말을 여기서 안 찍으면 소비자는 항목 상태로 되추론할 수밖에 없는데, 그 되추론은
// 실측으로 죽었다(QueueReproduction 의 ★ 와 DESIGN §10 의 표).
//
// ★ **양쪽 다 찍는다.** 롤백만 찍으면 "커밋됐다"와 "이 표시 이전에 쓰인 옛 행"이 같은
// 값(키 없음)으로 접힌다 — 이 저장소가 반복해서 닫아 온 0과 못 잼의 혼동 그대로다.
const (
	// TxOutcomeKey 는 결말이 실리는 payload 키다. **store 가 소유한다** — 호출자가 같은
	// 키를 실어도 덮인다.
	//
	// 키가 **없는** 행은 두 인구다: 이 표시 이전에 쓰인 옛 행과, 트랜잭션 밖에서
	// Store.LogEvent 로 직접 쓴 행(service 의 logFail 이 남기는 *.fail 이 그렇다).
	// 둘을 가르는 방법은 없고 필요도 없다 — 뒤쪽은 애초에 예약 이벤트가 아니라
	// 롤백될 트랜잭션 자체가 없다. 소비자가 알아야 할 것은 "커밋됐다가 아니다" 하나다.
	TxOutcomeKey = "tx"
	// TxCommitted 는 그 트랜잭션이 커밋된 것이다.
	TxCommitted = "committed"
	// TxRolledBack 은 그 트랜잭션이 롤백된 것이다 — 커밋 자체가 실패한 갈래를 포함한다.
	TxRolledBack = "rolled_back"
)

// markTxOutcome 은 예약 이벤트 payload 에 결말을 얹은 **사본**을 낸다.
//
// 사본을 뜨는 이유: 호출자가 만든 맵을 여기서 고치면, 같은 맵을 두 번 쓰는 호출자가
// 생기는 날 값이 조용히 달라진다. 지금 호출부가 전부 인라인 리터럴이라는 것은 호출부의
// 성질이지 이 함수의 성질이 아니다.
//
// map[string]any 가 아닌 payload 는 **그대로 낸다** — 결말을 못 찍고, 그 행은 키가 없어
// "관측 못 함"으로 읽힌다. 실호출부는 전부 map[string]any 다(store·service·web·legacy 전수).
func markTxOutcome(payload any, committed bool) any {
	outcome := TxRolledBack
	if committed {
		outcome = TxCommitted
	}
	switch p := payload.(type) {
	case nil:
		return map[string]any{TxOutcomeKey: outcome}
	case map[string]any:
		out := make(map[string]any, len(p)+1)
		for k, v := range p {
			out[k] = v
		}
		out[TxOutcomeKey] = outcome
		return out
	default:
		return payload
	}
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

// reproPayload 는 QueueReproduction 이 원장 payload 에서 읽는 전부다.
//
// item.finish 와 item.add 가 같은 타입을 쓴다 — 결말 표시를 읽는 규칙이 둘로 갈리면
// 한쪽만 고쳐져 분자와 분모가 다른 규칙으로 세어진다. 그 갈림이 그 함수가 닫는 결함의
// 원래 모양이다.
type reproPayload struct {
	Count int    `json:"count"`
	Tx    string `json:"tx"`
}

// readReproPayload 는 payload 를 읽고 **읽혔는지**를 함께 낸다.
//
// bool 을 따로 내는 이유: 못 읽은 행과 "count 가 0인 행"은 다르다. 못 읽은 행은 결말도
// 모르므로 롤백으로 뺄 수 없고(관측이 없다), 후속만 0으로 접어 센다.
func readReproPayload(payload string) (reproPayload, bool) {
	var p reproPayload
	if json.Unmarshal([]byte(payload), &p) != nil {
		return reproPayload{}, false
	}
	return p, true
}

// QueueReproduction 은 최근 n회 마무리 기준 재생산율의 원자료다.
//
// ★ 왜 이 축이 있나. 실측(kweiza-cc-plugins · 이 원장): finish 88건이 followups 61건과
// 독립 add 53건을 낳아 R=1.30 이다 — 사이클 1회(pickup→작업→finish)마다 큐가 +0.29 이고,
// 그래서 **pickup 을 더 돌려서는 큐가 안 준다.** 큐를 줄이는 것은 pickup 이 아니라 finish 인데
// 그 finish 가 평균 1.29건을 다시 넣는다. 세션이 그 사실을 마무리하는 자리에서 봐야 한다.
//
// ★ 그 1.30 은 **날짜가 빠져 있었다 — 2026-08-06 무렵의 전 기간 값이다.** 이 원장의 88번째
// kweiza 마무리가 2026-08-06T00:40 이고, 그 시점까지의 followups 61이 그대로 재현된다
// (2026-08-09 22:19 KST 읽기 전용 사본). 즉 "창 88"이 아니라 "원장이 88건이던 날의 전 기간"이다.
// 같은 프로젝트를 지금 다시 재면 최근 88 창은 1.15, 전 기간(140건)은 1.21, 최근 20 창은
// 0.80 이다. 이 수는 축의 **동기**를 적는 자리이지 기한 판정의 정본이 아니다 —
// 정본은 DESIGN §10 의 4차 실측(2026-08-07)이다.
//
// ★ **창을 id 로 자른다.** at 은 마이크로초 해상도라(timeLayout) 한 턴에 몰린 이벤트가 같은
// 값을 가질 수 있고, 그러면 경계에 걸친 add 가 창 안팎을 오간다. id 는 AUTOINCREMENT 라
// 단조이고 유일하다.
//
// ★ **add 구간도 함께 자른다.** 마무리는 최근 n회만 세면서 add 는 전 기간을 세면 R 이 실제보다
// 크게 나온다. AckReach 가 시각 절단 없이 전 기간을 누적해 겪은 것과 같은 부류다.
//
// ★ **롤백된 시도는 분모에도 분자에도 안 넣는다.** Tx.LogEvent 가 예약한 item.finish 는
// 롤백 갈래에서도 흘러가므로(store.go 의 flushDeferred) 이 원장에는 성공한 마무리와 롤백된
// 마무리가 같이 있다. 가르는 것은 payload 의 결말 표시(TxOutcomeKey)다 — 그것을 찍는 자리가
// 결말을 아는 유일한 자리이기 때문이다.
//
// ★ **n 은 마무리 이벤트 수이지 성공한 마무리 수가 아니다.** 그래서 롤백이 섞인 창에서는
// 표본이 n 보다 작아진다. 성공이 n개가 될 때까지 더 긁지 않는 이유는 창을 id 로 자르는
// 이유와 같다 — 더 긁으면 창의 아래 끝이 내려가 add 구간이 함께 넓어지고, 그러면 분자와
// 분모가 다른 구간을 본다. 표본이 줄면 분산이 커질 뿐 방향이 안 틀린다.
//
// ★ **그래서 R 의 뜻이 "시도당"에서 "완료된 사이클당"으로 바뀌었다.** 분모에서 롤백을 빼도
// 분자의 add 구간은 id 기준 그대로라, 창 **한가운데** 롤백이 있으면 그 시도가 걸친 구간의
// add 는 분자에 남는데 그 시도는 분모에서 빠진다. 창의 아래 끝을 옮기는 것으로는 이 갈래를
// 못 막는다 — 하한은 창 **끝**만 고친다. 그리고 그것이 옳다: 롤백은 큐를 안 줄였는데 그
// 사이 add 는 실제로 들어왔으므로, 이 값의 뜻은 "완료된 사이클 하나가 큐에 몇 개를 남기나"다.
// 그 뜻을 이미 무는 시험이 있다 — TestQueueReproductionExcludesRolledBackFinishes 가 롤백된
// 마무리 **뒤**에 커밋된 add 를 두고 Finishes=1 · Adds=1 을 못박는다(그 창의 R 은 2.0 이다).
// 다만 롤백을 실제로 심는 시험 셋은 전부 그 행을 창의 **끝**(바닥 또는 꼭대기)에 둔다 —
// 한가운데에 두는 시험은 없고, 그 갈래는 이 문장이 지킨다.
//
// ★ **항목 상태로 조인하는 길은 실측으로 기각했다**(2026-08-09 21:01 KST · ~/.flightdeck/fd.db
// 읽기 전용 사본 · item.finish 391건). 롤백 4건 중 3건은 나중에 재시도로 닫혀 항목이 done 이라
// 상태 조인이 한 건도 못 잡는다. 반대로 상태 조인이 실제로 잡는 3건(그 프로젝트에 항목이 없는
// finish) 중 2건은 **성공한** 마무리다 — 항목이 프로젝트를 옮겨 event.project 와 갈렸을 뿐이고,
// 같은 순간 handoff 판단이 커밋돼 있다. 즉 그 방법은 유령 count 2를 하나도 못 빼면서 진짜
// count 8을 뺀다. 전문은 DESIGN §10 의 표에 있다.
//
// ★ **결말 표시가 없는 행은 센다.** 표시 이전에 쓰인 행이고, 그것은 "커밋됐다"가 아니라
// "관측 못 했다"이다. 안 세면 표시 이전 구간의 R 이 통째로 0이 되고 0은 "큐가 안 는다"로
// 읽힌다 — 접는 쪽을 고른 근거와 그 대가를 DESIGN §10 이 적는다. 창이 최근 n회라 실측
// 마무리 속도(프로젝트 최대 46.3건/일)에서 몇 시간이면 창 전체가 표시된 행으로 바뀐다.
//
// ★ **그 접기가 R 을 틀리게 하는 방향은 하나가 아니다 — 세 항이 함께 정한다.** 표시 없는
// 롤백 행은 (1) count 를 분자에 더하고, (2) 아래 out.Finishes++ 로 분모에도 1을 더하며,
// (3) 그 행이 창에서 **가장 오래되면** 아래 sinceID 갱신에도 참여해 창의 아래 끝을 그 행까지
// 끌어내린다 — 그러면 그 행과 다음 센 마무리 사이 구간의 add 가 분자에 추가로 들어온다.
// 그 수를 k 라 하자(하한이 내려가서 딸려 들어오는 add 수. 바닥이 아닌 롤백은 하한을 안
// 건드리므로 k=0 이다). 그 행이 표시돼 빠졌을 때의 값과 견주면, 관측은 (F+c+A+k)/D 이고
// 참값은 (F+A)/(D-1) 이라(D 는 창의 마무리 이벤트 수 n) **관측 < 참값 ⟺ c+k < 참값** 이다.
//
// ★ 그래서 조건은 c 혼자가 아니라 **c+k** 다. 창 한가운데나 위 끝의 롤백은 k=0 이라 c < R 로
// 줄고 그때 count=0 인 롤백은 **항상** R 을 내리지만, 창 **바닥**의 롤백은 count=0 이어도
// k≥1 이면 R 을 올린다 — 참값 16/19=0.84 인 창의 바닥이 count=0 인 표시 없는 롤백이고 그
// 뒤 gap 에 add 가 하나만 있어도 관측은 17/20=0.85 다. 이 갈래는 가설이 아니다: 지금 원장은
// tx 키가 **0건**이라(2026-08-09 22:19 KST 실측) 모든 창의 바닥이 표시 없는 행이다.
// 실측 롤백 4건 중 2건이 count=0 이라 반대쪽(과소) 갈래도 함께 실재한다 — kweiza 의 지금 창은
// 관측 16/20=0.80 이고, 그 20 중 하나가 표시 없는 count=0 롤백이면서 k=0 이면 참값은
// 16/19=0.84 다.
//
// ★ **그래서 남는 오차는 §10 기한에 유리한 쪽으로도 기운다.** 그 기한은 "R 이 한 번도 1
// 아래로 안 내려갔으면 가설을 죽인다"라, 아래로 미는 오차는 가설에 **면죄부**를 준다.
// 그럼에도 세는 쪽을 고른 이유는 안 세는 쪽의 대가가 더 크기 때문이지(표시 이전 구간의 R 이
// 통째로 0이 되고 0은 "큐가 안 는다"로 읽힌다) 오차가 안전한 쪽으로만 틀려서가 아니다.
// 2026-08-21 에 이 수를 읽는 사람은 오차를 **한 방향으로 보정하지 마라** — 방향은 그 창에
// 섞인 표시 없는 롤백의 count 가 정하고, 그 창을 직접 열어 세는 것 말고 아는 방법이 없다.
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
		// payload 는 자유 JSON 이라 스키마가 없다. 못 읽으면 **0으로 접는다** —
		// 이 축의 소비자는 "비면 안 센다"로 동작한다(eventItemID 와 같은 규율).
		p, read := readReproPayload(payload)
		// ★ 롤백된 시도는 마무리가 아니다. 분모에서 빼고 그 count 도 분자에 안 넣는다 —
		//   그 후속들은 트랜잭션과 함께 사라져 큐에 한 건도 안 들어갔다.
		if read && p.Tx == TxRolledBack {
			continue
		}
		out.Finishes++
		// ★ 창의 아래 끝은 **센 것 중** 가장 오래된 id 다. 롤백된 시도의 id 로 잡으면
		//   분모는 그대로인데 add 구간만 넓어져 분자가 분모 없이 커진다.
		if sinceID < 0 || id < sinceID {
			sinceID = id
		}
		if read && p.Count > 0 {
			out.Followups += p.Count
		}
	}
	if err := rows.Err(); err != nil {
		return Reproduction{}, fmt.Errorf("마무리 이벤트 순회 실패: %w", err)
	}
	if out.Finishes == 0 {
		return out, nil // 표본이 없다. 0값 그대로 — **오류가 아니다**(호출자가 표본 0으로 읽는다)
	}

	// ★ add 도 같은 규칙으로 거른다. Service.AddItem 역시 Tx.LogEvent 로 예약하므로
	// 롤백된 add 는 항목이 안 만들어진 채 이벤트만 남고, 그것이 분자에 그대로 든다 —
	// 분자의 나머지 절반에 같은 오염이 있었다.
	//
	// ★ count(*) 를 못 쓰는 이유가 결말이 payload 안에 있기 때문이다. 조인 조건에
	// json_extract 를 넣는 선례가 이 저장소에 0건이라 Go 로 읽는다 — 창이 최근 n회라
	// 훑는 행 수가 원래 작다.
	addRows, err := s.db.QueryContext(ctx, `
		SELECT payload FROM event
		WHERE project = ? AND kind = 'item.add' AND id >= ?`, project, sinceID)
	if err != nil {
		return Reproduction{}, fmt.Errorf("추가 이벤트 조회 실패(project=%q): %w", clip(project, 64), err)
	}
	defer addRows.Close()
	for addRows.Next() {
		var payload string
		if err := addRows.Scan(&payload); err != nil {
			return Reproduction{}, fmt.Errorf("추가 이벤트 행 해석 실패: %w", err)
		}
		if p, read := readReproPayload(payload); read && p.Tx == TxRolledBack {
			continue
		}
		out.Adds++
	}
	if err := addRows.Err(); err != nil {
		return Reproduction{}, fmt.Errorf("추가 이벤트 순회 실패: %w", err)
	}
	return out, nil
}

// closeDeclarationScanLimit 는 CloseDeclarationsByItem 이 한 번에 훑는 item.finish 행의 상한이다.
//
// ★ 근거를 수로 적는다(실측 2026-08-09 17:21 KST(08:21 UTC) · ~/.flightdeck/fd.db 의 읽기
// 전용 사본). item.finish 는 388건이고 프로젝트별 최대 속도는 context-platform 의
// 249건/5.38일 = 46.3건/일 이다. 5000건은 그 속도로 **108일**이다. 이 축이 겨냥하는 인구는
// "롤백된 뒤 아직 열려 있는 항목"이고, 열린 항목 나이의 실측 최대는 9.6일 · 사고 사례는
// 42시간이었다 — 108일은 그 11배다.
//
// ★ 이 총량은 시각 고정 스냅샷이다 — 원장은 다른 세션이 `finish` 할 때마다 자라므로,
// 다른 시각에 다시 재면 다른 수가 나온다(이 물결 안에서만 383→384→386→388 로
// 관측됐다. DESIGN.md §10 의 R 오염 문단이 같은 이유를 적는다). 여기 적는 목적은
// 정확한 현재값이 아니라 **자릿수**다 — 5000 이 384 든 388 이든 여유가 크다는 논지는
// 안 바뀐다.
//
// ★ **성능 손잡이가 아니다.** EXPLAIN QUERY PLAN 은 event_by_kind(kind=?) 를 타고
// kind='item.finish' 행 전부를 훑은 뒤 project 로 거른다 — 훑는 양은 LIMIT 이 아니라 원장의
// 크기가 정한다(위와 같은 시각·같은 사본에서 실측 388행에 0.9ms, LIMIT 500~20000 사이에
// 차이가 없다). LIMIT 이 실제로 무는 것은 정렬 버퍼와 JSON 파싱 횟수뿐이다. 그래서 넉넉히
// 잡되, 이 수가 실제로 물리기 시작하는 때(원장이 지금의 13배)를 상한을 다시 잴 신호로 남긴다.
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
// ★ **이 수는 정확한 수가 아니라 하한이다.** 남는 사유는 셋이다: BeginTx 가 실패하면
// 클로저를 안 불러 예약 자체가 없고, INSERT 가 실패하면 LogEvent 가 WARN 으로 삼키고,
// 프로세스가 흘리기 전에 죽으면 버퍼가 메모리째 사라진다. 소비자의 문구가
// "정확히 N건"이 아니라 "적어도 N건"으로 말해야 한다.
//
// ★ **사유 하나는 2026-08-09 에 닫혔다.** 그전에는 flushDeferred 가 트랜잭션의 ctx 를
// 그대로 타서 "요청이 끊기면 행이 안 써진다"가 맞는 말이었다(실측: 롤백 갈래 기록 0건).
// 지금은 취소를 떼고 예산을 다시 건다(store.go 의 flushCtx). 가장 흔한 갈래가 빠진 것이지
// 하한이라는 성질이 바뀐 것은 아니다 — 위 셋이 남는다.
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
