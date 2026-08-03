package api

import (
	"net/http"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// 큐 표면 — Q 계층.
//
// 사람이 주는 것은 title·body·paths·after 뿐이다. 상태·랜딩 sha·역인덱스·
// 탈락 사유는 전부 서버가 채운다(설계 §3 의 쓰기 권한).

// afterInput 은 선행 조건 하나다. **브랜치 이름을 담을 필드가 없다** —
// 랜딩이 끝나면 브랜치가 지워져 조건이 충족되는 바로 그 순간 해석 불가가 되기 때문이다.
type afterInput struct {
	Item string `json:"item"`
	Job  string `json:"job"`
	SHA  string `json:"sha"`
}

func toAfter(in []afterInput) []model.After {
	if len(in) == 0 {
		return nil
	}
	out := make([]model.After, 0, len(in))
	for _, a := range in {
		out = append(out, model.After{Item: a.Item, Job: a.Job, SHA: a.SHA})
	}
	return out
}

type linkInput struct {
	TargetKind string `json:"target_kind"`
	TargetID   string `json:"target_id"`
}

func toLinks(in []linkInput) []model.JudgmentLink {
	if len(in) == 0 {
		return nil
	}
	out := make([]model.JudgmentLink, 0, len(in))
	for _, l := range in {
		out = append(out, model.JudgmentLink{TargetKind: l.TargetKind, TargetID: l.TargetID})
	}
	return out
}

// handleNextItem 은 추천 1건과 **탈락 사유 전부**를 낸다.
//
// ★ 이 경로는 **선점하지 않는다**(Mode=recommended). 설계 §6 의 표가
// "인자 없으면 추천, 인자 있으면 선점"이고, 응답의 Reason 이 그 사실을 문장으로 말한다.
// 집으려면 POST /items/{id}/claim 이다.
func (s *server) handleNextItem(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireQuery(w, r, "project", "어느 프로젝트의 큐인지 없이는 후보 집합이 없다.")
	if !ok {
		return
	}
	sessionID, ok := s.requireQuery(w, r, "session_id",
		"겹침·선점 판정이 '나'를 알아야 한다 — 자기 발자국을 남의 것으로 세면 자기가 자기를 막는다.")
	if !ok {
		return
	}
	infoFrom(r.Context()).setSession(sessionID)
	res, err := s.svc.Pick(r.Context(), service.PickInput{Project: project, SessionID: sessionID})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, res)
}

type addItemRequest struct {
	Project   string       `json:"project"`
	SessionID string       `json:"session_id"`
	ID        string       `json:"id"`
	Title     string       `json:"title"`
	Body      string       `json:"body"`
	Paths     []string     `json:"paths"`
	Labels    []string     `json:"labels"`
	After     []afterInput `json:"after"`
}

// handleAddItem 은 큐 항목을 만든다.
func (s *server) handleAddItem(w http.ResponseWriter, r *http.Request) {
	var req addItemRequest
	if !s.decode(w, r, &req) {
		return
	}
	infoFrom(r.Context()).setSession(req.SessionID)
	it, err := s.svc.AddItem(r.Context(), service.AddItemInput{
		Project: req.Project, SessionID: req.SessionID, ID: req.ID,
		Title: req.Title, Body: req.Body, Paths: req.Paths, Labels: req.Labels,
		After: toAfter(req.After),
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.publish(r, "item.add", req.Project, req.SessionID, map[string]any{"item": clip(it.ID, 100)})
	s.writeJSON(w, r, http.StatusCreated, map[string]any{"item": it})
}

type claimRequest struct {
	Project   string `json:"project"`
	SessionID string `json:"session_id"`
}

// handleClaimItem 은 지정한 항목을 선점하거나 **맥락을 다시 낸다**(재개 경로).
//
// 이미 자기 선점이면 거절이 아니다 — 컨텍스트가 날아가 같은 워크트리로 돌아온 세션이
// 가장 절실하게 맥락을 부르는 순간이라, 거기서 거절하면 되찾을 길이 없다.
func (s *server) handleClaimItem(w http.ResponseWriter, r *http.Request) {
	var req claimRequest
	if !s.decode(w, r, &req) {
		return
	}
	infoFrom(r.Context()).setSession(req.SessionID)
	res, err := s.svc.Pick(r.Context(), service.PickInput{
		Project: req.Project, SessionID: req.SessionID, ItemID: r.PathValue("id"),
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.publish(r, "item."+string(res.Mode), req.Project, req.SessionID, map[string]any{
		"item": clip(r.PathValue("id"), 100), "overlaps": len(res.Overlaps),
	})
	s.writeJSON(w, r, http.StatusOK, res)
}

type followupRequest struct {
	ID     string       `json:"id"`
	Title  string       `json:"title"`
	Body   string       `json:"body"`
	Paths  []string     `json:"paths"`
	Labels []string     `json:"labels"`
	After  []afterInput `json:"after"`
}

type finishRequest struct {
	Project     string            `json:"project"`
	SessionID   string            `json:"session_id"`
	Outcome     string            `json:"outcome"`
	Title       string            `json:"title"`
	Body        string            `json:"body"`
	CloseReason string            `json:"close_reason"`
	Followups   []followupRequest `json:"followups"`
	Links       []linkInput       `json:"links"`
}

// handleFinishItem 은 판단 저장 + 후속 등록 + 항목 종료 + 자원 반납을 **한 호출**로 한다.
//
// 넷 중 하나라도 실패하면 전부 롤백된다 — 그래서 검산할 순서 자체가 없다.
// body 없이 부르면 무엇을 적어야 하는지가 거절 사유의 처방으로 그 자리에서 나온다.
func (s *server) handleFinishItem(w http.ResponseWriter, r *http.Request) {
	var req finishRequest
	if !s.decode(w, r, &req) {
		return
	}
	infoFrom(r.Context()).setSession(req.SessionID)
	in := service.FinishInput{
		Project: req.Project, SessionID: req.SessionID, ItemID: r.PathValue("id"),
		Outcome: model.ItemState(req.Outcome), Title: req.Title, Body: req.Body,
		CloseReason: req.CloseReason, Links: toLinks(req.Links),
	}
	for _, f := range req.Followups {
		in.Followups = append(in.Followups, service.FollowupInput{
			ID: f.ID, Title: f.Title, Body: f.Body,
			Paths: f.Paths, Labels: f.Labels, After: toAfter(f.After),
		})
	}
	res, err := s.svc.Finish(r.Context(), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.publish(r, "item.finish", req.Project, req.SessionID, map[string]any{
		"item": clip(in.ItemID, 100), "mode": req.Outcome,
		"count": len(res.Followups), "released": len(res.Released),
	})
	s.writeJSON(w, r, http.StatusOK, res)
}
