package service

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
)

// 처방 — 발화 지점(설계 §6). 판정은 judge.Prescribe 가 하고 이 파일은 입력을 모으고 결과를 남긴다.
//
// ★ **세션 카드 파생을 안 돈다.** 이 경로는 턴마다 돌므로, git worktree list +
// 세션별 ChangedPaths·UncommittedPaths 를 얹으면 **모든 턴 종료에 저장소 전수 훑기가 붙는다**.
// 필요한 입력(footprint·claim·judgment·session)은 전부 DB 표라 git 을 안 탄다.
// 설계 §6 이 /notices 를 /dashboard.json 에서 가른 것과 같은 판정이다.

const (
	eventPrescribe    = "prescribe"
	eventPrescribeAck = "prescribe_ack"
)

// PrescribeResult 는 한 턴의 처방이다.
type PrescribeResult struct {
	Shown  []judge.Prescription `json:"shown"`  // 문구로 낼 것 (최대 judge.PrescribeMax)
	Folded int                  `json:"folded"` // 요약으로 접힌 수
	All    []judge.Prescription `json:"all"`    // 발화 기록된 전부
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

	in := judge.PrescribeInput{Now: s.now(), SessionID: sessionID}

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
		})
	}

	all := judge.Prescribe(in)
	shown, folded := judge.FoldPrescriptions(all)

	// **접힌 것도 기록한다.** 요약된 것은 "안 낸 것"이 아니다 —
	// 안 기록하면 다음 턴에 그대로 다시 떠서 상한이 무의미해진다.
	for _, p := range all {
		s.st.LogEvent(ctx, eventPrescribe, sess.Project, sessionID,
			prescribePayload{Key: p.Key, Reason: p.Reason})
	}
	if len(all) > 0 {
		s.log.InfoContext(ctx, "처방 발화",
			"session_id", sessionID, "count", len(all), "shown", len(shown), "folded", folded)
	}
	return PrescribeResult{Shown: shown, Folded: folded, All: all}, nil
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
