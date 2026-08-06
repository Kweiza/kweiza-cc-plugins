package service

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// 처방 — 발화 지점(설계 §6). 판정은 judge.Prescribe 가 하고 이 파일은 입력을 모으고 결과를 남긴다.
//
// ★ **세션 카드 파생을 안 돈다.** 이 경로는 턴마다 돌므로, git worktree list +
// 세션별 ChangedPaths·UncommittedPaths 를 얹으면 **모든 턴 종료에 저장소 전수 훑기가 붙는다**.
// 필요한 입력(footprint·claim·judgment·session·레인)은 전부 DB 표라 git 을 안 탄다.
// 설계 §6 이 /notices 를 /dashboard.json 에서 가른 것과 같은 판정이다.
//
// ★ 레인도 같은 이유로 **줄 전체가 아니라 맨 앞 하나와 점유 유무**만 읽는다.
// 무엇을 기각했는지는 laneTurnRow 주석에 있다.

const (
	eventPrescribe    = "prescribe"
	eventPrescribeAck = "prescribe_ack"
)

// PrescribeResult 는 한 턴의 처방이다.
//
// ★ **원장에 남는 것은 Shown 뿐이다**(2026-08-06 개정. 아래 Prescriptions 의 기록 루프).
// 세 필드가 서로 다른 것을 세므로 셋을 같은 뜻으로 읽으면 안 된다 — 특히 `All` 은
// `POST /api/v1/sessions/{id}/prescriptions` 의 `all` 로 서버 밖에 나가는데 비시험
// 소비자가 0건이라, 이 주석이 그 필드의 유일한 계약이다.
type PrescribeResult struct {
	Shown  []judge.Prescription `json:"shown"`  // 문구로 낼 것 (최대 judge.PrescribeMax) — **이것만 event 에 남는다**
	Folded int                  `json:"folded"` // 요약으로 접힌 수 — 원장에 안 남는다(아래 ★)
	All    []judge.Prescription `json:"all"`    // 이번 턴에 판정된 전부(표시분 + 접힌 것). 기록되는 것은 Shown 뿐이다
}

// prescribePayload 는 event.payload 의 모양이다.
type prescribePayload struct {
	Key    string `json:"key"`
	Reason string `json:"reason"`
}

// Prescriptions 는 이 세션이 지금 받아야 할 처방을 내고, 낸 것을 event 에 기록한다.
func (s *Service) Prescriptions(ctx context.Context, sessionID string) (PrescribeResult, error) {
	sess, err := s.st.GetSession(ctx, sessionID)
	if err != nil {
		return PrescribeResult{}, err
	}

	in := judge.PrescribeInput{Now: s.now(), SessionID: sessionID, SelfCC: sess.CCSessionID}

	// 억제 상태 — 이 세션이 이미 낸 키와 그 시각.
	emitted, since, err := s.emittedKeys(ctx, sessionID, sess.OpenedAt)
	if err != nil {
		return PrescribeResult{}, err
	}
	in.Emitted = emitted

	// 선점 항목과 각자의 선언 경로.
	claimed, err := s.st.ClaimedItems(ctx, sessionID)
	if err != nil {
		return PrescribeResult{}, err
	}
	for _, id := range claimed {
		it, err := s.st.GetItem(ctx, sess.Project, id)
		if err != nil {
			// 항목을 못 읽는 것은 처방을 못 낼 이유가 아니다. 조용히 접지 않고 남긴다.
			s.log.WarnContext(ctx, "처방: 선점 항목을 못 읽었다",
				"session_id", sessionID, "item", id, "error", err.Error())
			continue
		}
		in.Claims = append(in.Claims, judge.ClaimView{ItemID: it.ID, Paths: it.Paths})
	}

	// 이 구간에 반납한 항목 — "한 번도 안 집었다"와 "방금 제대로 끝냈다"를 가르는 축이다.
	// **TurnPaths 와 같은 since 를 쓴다.** 두 창이 갈리면 "이번 턴에 만진 경로"와
	// "이번 턴에 끝낸 항목"이 서로 다른 구간을 가리키게 되고, 그 어긋남은 화면에 안 뜬다.
	released, err := s.st.ReleasedItems(ctx, sessionID, since)
	if err != nil {
		return PrescribeResult{}, err
	}
	for _, id := range released {
		it, err := s.st.GetItem(ctx, sess.Project, id)
		if err != nil {
			s.log.WarnContext(ctx, "처방: 반납 항목을 못 읽었다",
				"session_id", sessionID, "item", id, "error", err.Error())
			continue
		}
		in.Closed = append(in.Closed, judge.ClaimView{ItemID: it.ID, Paths: it.Paths})
	}

	// 이번 구간에 새로 만진 경로 · 마지막 판단 이후 새로 만진 경로.
	prints, err := s.st.Footprints(ctx, sessionID)
	if err != nil {
		return PrescribeResult{}, err
	}
	last, err := s.lastJudgmentAt(ctx, sessionID, sess.OpenedAt)
	if err != nil {
		return PrescribeResult{}, err
	}
	in.LastJudgment = last
	for _, f := range prints {
		if f.Origin != model.OriginObserved {
			continue // 선언·항목 경로는 "만졌다"가 아니다. 뭉개면 §3 이 가른 축이 사라진다
		}
		if f.LastAt.After(since) {
			in.TurnPaths = append(in.TurnPaths, f.Path)
		}
		if f.LastAt.After(last) {
			in.NewPaths++
		}
	}

	// 살아 있는 남의 세션. **창은 보드와 같은 것을 쓴다** — 두 자리에 두면 조용히 어긋난다.
	live, err := s.st.ListLive(ctx, sess.Project, s.cut(in.Now, s.window))
	if err != nil {
		return PrescribeResult{}, err
	}
	for _, v := range live {
		if v.Session.ID == sessionID {
			continue
		}
		in.Others = append(in.Others, judge.LiveSession{
			ID: v.Session.ID, Label: v.Session.Label, Paths: v.Paths,
			// ★ 대화 id 를 함께 넘긴다. 카드 id 만으로는 형제 카드(같은 대화, 다른 카드)를
			// 남으로 보고 **자기 자신과 조율하라**는 처방을 낸다.
			CCSessionID: v.Session.CCSessionID,
		})
	}

	// 랜딩 줄의 차례. 0 이면 차례가 아니고, 그 판정을 하는 자리는 아래 하나다.
	in.LaneTurnRow = s.laneTurnRow(ctx, sess.Project, sessionID)

	all := judge.Prescribe(in)
	shown, folded := judge.FoldPrescriptions(all)

	// **표시된 것만 기록한다(2026-08-06 개정).** 앞선 판은 접힌 것까지 기록하고 그 근거를
	// "안 기록하면 다음 턴에 그대로 다시 떠서 상한이 무의미해진다"라고 적었는데, 그 조합이
	// **접힌 처방을 영구히 지웠다**: 기록되면 suppressed 가 그 키를 누르고(해제 규칙은
	// silent 에만 있다), 세션은 그 문구를 **한 번도 못 본 채** 원장에는 "정상적으로 접혔다"로만
	// 남는다. 사라지는 것이 `outside`(남이 보는 겹침 입력이 낡았다) 나 `unclaimed` 면
	// 그 사실을 아무도 못 듣는다.
	//
	// ★ 상한은 무의미해지지 않는다 — **순환한다.** 표시된 셋만 눌리므로, **그 축의 입력이
	// 다시 생기는 턴**에 넷째가 첫 칸으로 올라온다. 다만 그 조건이 전부는 아니다:
	// 세션이 그 경로를 다시 안 만지면 안 올라온다(`TurnPaths` 는 `f.LastAt.After(since)` 로
	// 뽑고 `since` 는 **마지막 발화 시각**이다). 그래서 **한 턴에 몰아친 outside 다발은
	// 여전히 소실**이고, 그것은 이 개정이 아니라 상한 자체의 한계다(후속:
	// fd-folded-outside-burst-still-lost). 설계 §4 가 고발한 "상시 점등"(같은 것이 매 턴
	// 반복)과는 다르다 — 눌리는 것은 표시된 것뿐이다.
	//
	// ★ 재측(2026-08-06): 처방이 뜬 턴 129개 중 접힌 턴 **15개**(11.6%)이고 한 턴 최대는
	// **7건**이다. 접혀서 사라지던 축은 overlap 11 · unclaimed 11 · silent 4 · outside 2 —
	// 앞선 판의 "35턴 중 2개"는 **표본이 4배가 되기 전** 값이다(lane-turn 은 원장에 전 기간
	// 0건이라 그 축의 효과가 아니다. PrescribeMax 주석의 재측 문단).
	//
	// ★ **이 커밋 뒤로 접힘 빈도는 원장에서 못 잰다.** 표시분만 기록하므로 한 턴의 prescribe
	// 이벤트 수가 구조적으로 PrescribeMax 를 못 넘고, `folded` 는 slog 와 화면 문구에만 남는다.
	// 위 129/15 는 **마지막으로 잴 수 있었던 값**이고, 다시 재려면 folded 를 원장에 실어야
	// 한다(후속: fd-folded-count-left-no-ledger-trace).
	for _, p := range shown {
		s.st.LogEvent(ctx, eventPrescribe, sess.Project, sessionID,
			prescribePayload{Key: p.Key, Reason: p.Reason})
	}
	if len(all) > 0 {
		s.log.InfoContext(ctx, "처방 발화",
			"session_id", sessionID, "count", len(all), "shown", len(shown), "folded", folded)
	}
	return PrescribeResult{Shown: shown, Folded: folded, All: all}, nil
}

// laneTurnRow 는 랜딩 줄에서 **지금 이 세션 차례가 된 줄 행**의 번호다. 0 이면 차례가 아니다.
//
// 차례의 정의는 곱이다: 줄 맨 앞이 이 세션이고 **그리고** 레인을 쥔 사람이 없다.
//
// ★ **쥔 사람이 있으면 안 낸다 — 남이든 나든.** 남이 쥔 채 내가 맨 앞인 것은 두 표가
// 어긋난 상태이고(Land 가 그 상태를 오류가 아니라 waiting 으로 인정한다. landing.go 의
// "맨 앞인데 남이 쥐고 있다" 분기), 거기서 "네 차례다"를 내면 세션을 AcquireResource 가
// 반드시 실패할 자리로 보낸다. 그 상태를 푸는 것은 사람의 회수다. 내가 쥐었으면 이미
// land 응답이 turn 으로 답했으니 같은 말을 두 번 하는 것이다. 둘 다 0 이다.
//
// ★ **LandingLane 을 기각했다.** 새 SQL 을 안 만드는 것은 어느 쪽이든 같지만, LandingLane 은
// 줄 전체(ListLandingQueue) + 점유 + (어긋나 보이면 줄 재조회) + **행마다 lastSignal 한 번씩**
// 을 돈다(그 함수가 스스로 "줄 길이만큼 LastSignal 을 부른다"고 적어 뒀다). 처방은 모든
// 세션의 모든 턴 종료에 도는데
// 여기서 필요한 것은 맨 앞 하나와 점유 유무뿐이라 그 신호 나이들은 전부 버려질 값이다.
// 그리고 LaneView.Entries[0] 으로 "맨 앞"을 다시 표현하면 순서 집행이 두 자리가 된다 —
// FrontLandingRow 의 독스트링이 자기가 그 유일한 자리라고 선언한 것을 깨는 모양이다.
// (앞사람의 신호 나이 같은 것을 처방 문구에 싣기로 하면 그때 갈아탈 자리다.)
//
// ★ **오류를 안 올린다.** ErrNotFound 둘은 정상 상태다 — 줄이 비었거나(줄에 한 번도 안 선
// 세션이 다수다) 아무도 안 쥐었거나. 그 밖의 오류도 처방 전부를 죽일 이유가 아니다:
// 여기서 return err 를 하면 레인 조회 하나가 unclaimed·outside·silent 까지 통째로 막는다.
// 위 선점·반납 항목 조회와 같은 관용(WARN 을 남기고 계속)이고, 0 은 judge 쪽에서
// "차례 아님"과 같은 값으로 접힌다(laneTurnPrescription 이 그 합침을 의도로 적어 뒀다).
func (s *Service) laneTurnRow(ctx context.Context, project, sessionID string) int64 {
	front, err := s.st.FrontLandingRow(ctx, project)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.log.WarnContext(ctx, "처방: 랜딩 줄 맨 앞을 못 읽었다",
				"session_id", sessionID, "project", project, "error", err.Error())
		}
		return 0 // 줄이 비었거나(정상) 못 읽었다
	}
	if front.SessionID != sessionID {
		return 0 // 앞에 사람이 있다. 점유까지 물을 이유가 없다
	}

	// 맨 앞이 나다. 남은 질문은 레인이 비었나 하나뿐이다.
	if _, herr := s.st.HeldBy(ctx, project, LaneResource); herr != nil {
		if !errors.Is(herr, store.ErrNotFound) {
			s.log.WarnContext(ctx, "처방: 랜딩 레인 점유를 못 읽었다",
				"session_id", sessionID, "project", project, "error", herr.Error())
			return 0
		}
		return front.ID // 아무도 안 쥐었고 맨 앞이 나다 = 차례다
	}
	return 0
}

// emittedKeys 는 이미 낸 키와 그 시각, 그리고 마지막 발화 시각을 낸다.
// 마지막 발화 시각이 "이번 구간"의 시작이다 — 없으면 세션 시작이다.
func (s *Service) emittedKeys(ctx context.Context, sessionID string, openedAt time.Time) (map[string]time.Time, time.Time, error) {
	evs, err := s.st.ListSessionEvents(ctx, sessionID, eventPrescribe, openedAt)
	if err != nil {
		return nil, time.Time{}, err
	}
	out := map[string]time.Time{}
	since := openedAt
	for _, e := range evs {
		var p prescribePayload
		if err := json.Unmarshal([]byte(e.Payload), &p); err != nil || p.Key == "" {
			// 해석 실패를 조용히 버리면 그 키가 안 눌린 것으로 보여 처방이 다시 뜬다.
			s.log.WarnContext(ctx, "처방 이벤트 payload 해석 실패",
				"session_id", sessionID, "payload", e.Payload)
			continue
		}
		out[p.Key] = e.At
		if e.At.After(since) {
			since = e.At
		}
	}
	return out, since, nil
}

// ackPrescriptions 는 지금 열려 있는 처방 전부를 닫는다.
//
// ★ note 한 번이 전부를 닫는 이유: 처방 문구가 무엇을 쓸지 지정하므로 보통 판단 하나가
// 그것을 덮는다. 처방마다 대응 판단을 요구하면 세션이 형식적 note 를 양산하고,
// 그러면 건수는 오르는데 판단 바이트는 안 오른다 — 설계 §10 이 그 둘을 함께 보라고 한 이유다.
//
// ★ **실패해도 판단 저장을 되돌리지 않는다.** 판단이 재생성 불가한 자산이고 ack 은 계측이다.
// 다만 삼키지 않는다 — WARN 으로 남긴다.
//
// ★ **이 통로를 지나는 것은 판단을 남기는 경로뿐이다(note·finish). `land` 는 안 지난다.**
// 그래서 행동이 `land()` 인 처방(`lane-turn`)에 대해 확인은 **정확히 반대 신호**를 잰다 —
// 처방대로 랜딩한 세션은 미확인으로 남고, 처방을 무시하고 상관없는 판단만 남긴 세션이
// 확인으로 잡힌다. 위 "note 한 번이 전부를 닫는다"가 그 뒤집힘을 완성한다: 키를 안 가리므로
// 레인과 아무 상관 없는 note 한 줄이 `lane-turn:<행>` 까지 닫는다.
//
// 이것은 **계약이 아니라 현재 사실**이고 `TestLaneTurnAckMeasuresJudgmentsNotTheLandItPrescribed`
// 가 그대로 잠갔다 — 통로를 뚫으면(land 가 자기가 응답한 키만 골라 ack) 그 시험이 먼저
// 빨개진다. 그때 고칠 것은 시험이 아니라 여기 적힌 사실이다.
//
// ★ 이 축과 `AckReach`(board.go)를 섞지 마라. 저쪽은 키를 안 보고 **세션 단위**로 센다.
// 키별 확인율을 내는 코드는 없다 — 설계 §10 의 "overlap 0/31" 은 사람이 따로 잰 값이다.
func (s *Service) ackPrescriptions(ctx context.Context, project, sessionID string) {
	sess, err := s.st.GetSession(ctx, sessionID)
	if err != nil {
		s.log.WarnContext(ctx, "ack: 세션을 못 읽었다", "session_id", sessionID, "error", err.Error())
		return
	}
	open, _, err := s.emittedKeys(ctx, sessionID, sess.OpenedAt)
	if err != nil {
		s.log.WarnContext(ctx, "ack: 발화 이력을 못 읽었다", "session_id", sessionID, "error", err.Error())
		return
	}
	acked, err := s.ackedKeys(ctx, sessionID, sess.OpenedAt)
	if err != nil {
		s.log.WarnContext(ctx, "ack: 확인 이력을 못 읽었다", "session_id", sessionID, "error", err.Error())
		return
	}
	var keys []string
	for k := range open {
		if !acked[k] {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return // **빈 ack 은 안 남긴다** — 확인율의 분자를 부풀린다
	}
	sort.Strings(keys) // 같은 입력에 같은 payload
	s.st.LogEvent(ctx, eventPrescribeAck, project, sessionID, map[string]any{"keys": keys})
}

// ackedKeys 는 이미 확인된 키다.
func (s *Service) ackedKeys(ctx context.Context, sessionID string, openedAt time.Time) (map[string]bool, error) {
	evs, err := s.st.ListSessionEvents(ctx, sessionID, eventPrescribeAck, openedAt)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, e := range evs {
		var p struct {
			Keys []string `json:"keys"`
		}
		if err := json.Unmarshal([]byte(e.Payload), &p); err != nil {
			s.log.WarnContext(ctx, "ack payload 해석 실패", "payload", e.Payload)
			continue
		}
		for _, k := range p.Keys {
			out[k] = true
		}
	}
	return out, nil
}

// lastJudgmentAt 은 이 세션의 마지막 판단 시각이다.
// **판단이 하나도 없으면 세션 시작 시각을 낸다** — judge 쪽 제로값 규약(기준 없음)에 맞춘다.
func (s *Service) lastJudgmentAt(ctx context.Context, sessionID string, openedAt time.Time) (time.Time, error) {
	js, err := s.st.ListJudgmentsBySession(ctx, sessionID)
	if err != nil {
		return time.Time{}, err
	}
	out := openedAt
	for _, j := range js {
		if j.At.After(out) {
			out = j.At
		}
	}
	return out, nil
}
