package api

import (
	"fmt"
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
	// ItemIDs 는 묶음 선점이다. **선두를 포함한 순서대로**이고, 비면 경로 id 단독 선점이다.
	//
	// ★ 라우트를 안 늘린 이유: 새 라우트는 "어느 것을 써야 하나"를 새 개념으로 만든다(설계 §1②).
	// 경로에 선두가 남아 있으므로 멱등 키·오프라인 거절·이벤트 라벨이 그대로 산다.
	ItemIDs []string `json:"item_ids"`
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

	// 경로의 항목과 item_ids 의 선두가 어긋나면 거절한다. 합치거나 한쪽을 우선하면
	// "무엇을 집었는가"가 모호해지고, 선두 id 가 곧 브랜치 이름이 된다 — pick 응답이
	// 나르는 것 중 가장 중요한 사실이라 짐작으로 메우지 않는다. 아직 s.svc.Pick 을
	// 안 불렀으니 쓰기는 하나도 안 열렸다.
	pathID := r.PathValue("id")
	if len(req.ItemIDs) > 0 && req.ItemIDs[0] != pathID {
		s.fail(w, r, &service.RefusedError{What: "claim",
			Reason: fmt.Sprintf("경로의 항목(%s)과 item_ids 의 선두(%s)가 다르다",
				clip(pathID, 64), clip(req.ItemIDs[0], 64)),
			Guidance: "선두가 브랜치 이름이 된다 — 경로와 item_ids[0] 을 같게 맞춰라."})
		return
	}

	in := service.PickInput{Project: req.Project, SessionID: req.SessionID, ItemID: pathID}
	if len(req.ItemIDs) > 0 {
		in = service.PickInput{Project: req.Project, SessionID: req.SessionID, ItemIDs: req.ItemIDs}
	}
	res, err := s.svc.Pick(r.Context(), in)
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

// moveRequest 는 항목을 다른 프로젝트로 옮기는 요청이다.
//
// PATCH 를 열지 않고 전용 동사를 쓴다 — 항목 표면에 PATCH 가 생기면 "무엇까지
// 고칠 수 있나"가 열린 질문이 되고, 그 질문은 이 자리에서 답할 것이 아니다.
type moveRequest struct {
	Project   string `json:"project"`
	SessionID string `json:"session_id"`
	To        string `json:"to"`
}

func (s *server) handleMoveItem(w http.ResponseWriter, r *http.Request) {
	var req moveRequest
	if !s.decode(w, r, &req) {
		return
	}
	infoFrom(r.Context()).setSession(req.SessionID)
	res, err := s.svc.MoveItem(r.Context(), service.MoveInput{
		Project: req.Project, SessionID: req.SessionID,
		ItemID: r.PathValue("id"), To: req.To,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// ★ 원장에 남긴다. 조용히 옮기면 "왜 이 항목이 여기 있나"에 답할 자리가 없어진다 —
	// 그리고 이 명령이 존재하는 이유 자체가 그 질문에 답하지 못해서다.
	s.publish(r, "item.move", req.Project, req.SessionID, map[string]any{
		"item": clip(res.Item.ID, 100), "from": clip(res.From, 64), "to": clip(res.To, 64),
		"count": res.CrossRefs,
	})
	s.writeJSON(w, r, http.StatusOK, res)
}
