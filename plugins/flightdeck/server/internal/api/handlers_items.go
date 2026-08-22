package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// 큐 표면 — Q 계층.
//
// 사람이 주는 것은 title·body·paths·after 뿐이다. 상태·랜딩 sha·종속 수·
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
	//
	// ★ 비교는 **트림한 값끼리** 한다 — service 가 이미 그렇게 보기 때문이다.
	//
	// 아래 s.svc.Pick 이 받는 item_ids 는 service 의 dedupeIDs 를 지나며 TrimSpace
	// 된다(service/pick.go). 그래서 여기서 바이트로 비교하면 선두에 공백 하나 붙은
	// 같은 요청이 **문에 따라 답이 갈린다**: REST 는 400 으로 걷어내고, service 를
	// 직접 부르는 쪽(mcpsrv 하네스 등)은 통과시켜 트림된 값을 브랜치로 삼는다.
	// 두 파일 중 어느 쪽만 봐도 그 자리는 옳아 보여, 이 빈 칸은 어느 diff 에도
	// 안 나타난다. 정규화 지점을 한 군데로 맞추는 것이 유일한 봉합이다.
	//
	// 사유에도 **트림한 값**을 찍는다. 원문 바이트를 그대로 내면 화면에서 두 값이
	// 똑같아 보이는데 거절된 꼴이 되고, 그러면 고칠 곳을 못 찾는다.
	pathID := r.PathValue("id")
	if len(req.ItemIDs) > 0 {
		lead, want := strings.TrimSpace(req.ItemIDs[0]), strings.TrimSpace(pathID)
		if lead != want {
			s.fail(w, r, &service.RefusedError{What: "claim",
				Reason: fmt.Sprintf("경로의 항목(%s)과 item_ids 의 선두(%s)가 다르다",
					clip(want, 64), clip(lead, 64)),
				Guidance: "선두가 브랜치 이름이 된다 — 경로와 item_ids[0] 을 같게 맞춰라."})
			return
		}
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

// claimReleaseRequest 는 POST /api/v1/items/{id}/claim/release 의 본문이다.
//
// session_id 가 **없다**(레인 회수와 같은 판정) — 세션 정체가 (machine, worktree,
// cc_session_id) 라 죽은 세션 명의로는 아무 호출도 못 하고, 회수하는 사람은 대개
// 그 세션이 아니다. 세션을 요구하는 순간 탈출구가 다시 막힌다.
type claimReleaseRequest struct {
	Project string `json:"project"`
	Actor   string `json:"actor"`  // 누가 회수했나. 판단 본문에 그대로 남는다
	Reason  string `json:"reason"` // 왜. **비면 service 가 거절한다**
}

// handleReclaimClaim 은 사람이 선점 하나를 회수한다.
//
// 로직의 정본은 service.ReclaimClaim — web 대시보드 폼·CLI `fd claim release` 와
// **같은 함수**다. 자동 만료가 아니다: 사람이 신호 나이·발자국을 본 뒤 부르는 자리라
// 사유가 필수이고, 회수 행위가 judgment(decision) 하나로 원장에 남는다.
func (s *server) handleReclaimClaim(w http.ResponseWriter, r *http.Request) {
	var req claimReleaseRequest
	if !s.decode(w, r, &req) {
		return
	}
	res, err := s.svc.ReclaimClaim(r.Context(), req.Project,
		strings.TrimSpace(r.PathValue("id")), req.Actor, req.Reason)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// 회수당한 세션을 액세스 로그의 좌표로 삼는다 — 요청 본문에는 그 세션이 없고,
	// "누구의 선점이 끊겼나"가 이 한 줄에서 답해져야 한다.
	infoFrom(r.Context()).setSession(res.Holder)
	s.publish(r, "claim.reclaim", req.Project, res.Holder, map[string]any{
		"item": clip(res.Item, 100), "actor": clip(req.Actor, 64),
	})
	s.writeJSON(w, r, http.StatusOK, res)
}

// claimLeaveRequest 는 POST /api/v1/claims/leave 의 본문이다.
//
// ★ 회수와 정반대로 **session_id 가 필수다.** 회수는 세션을 요구하면 탈출구가 막히지만
// (죽은 세션 명의로는 아무 호출도 못 한다), 반납은 "누구 것을 놓는가"가 세션으로만
// 정해진다 — 세션 없는 반납은 반납이 아니라 회수다. item_id 는 선택이고, 비면 이
// 세션이 쥔 전부가 대상이다(묶음은 함께 집히므로 함께 놓인다).
type claimLeaveRequest struct {
	Project   string `json:"project"`
	SessionID string `json:"session_id"`
	ItemID    string `json:"item_id"`
	Reason    string `json:"reason"` // 왜 안 했나. **비면 service 가 거절한다**
}

// handleLeaveClaim 은 살아 있는 세션이 자기 선점을 놓는다.
//
// 로직의 정본은 service.LeaveClaim — MCP 의 `pick(leave:…)` 가 이 경로로 온다.
// 회수(handleReclaimClaim)와 **다른 함수**인 이유는 판정이 반대이기 때문이다:
// 회수는 "관측한 점유자 == 지금 점유자"를 방벽으로 세우고, 반납은 "점유자 == 나"를 세운다.
func (s *server) handleLeaveClaim(w http.ResponseWriter, r *http.Request) {
	var req claimLeaveRequest
	if !s.decode(w, r, &req) {
		return
	}
	res, err := s.svc.LeaveClaim(r.Context(), service.LeaveInput{
		Project: req.Project, SessionID: strings.TrimSpace(req.SessionID),
		ItemID: strings.TrimSpace(req.ItemID), Reason: req.Reason,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.publish(r, "claim.leave", req.Project, res.Session, map[string]any{
		"items": res.Items, "count": len(res.Items),
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

// cutAfterRequest 는 항목에 걸린 선행 하나를 끊는 요청이다.
//
// move 와 같은 규율으로 **전용 동사**다 — item_after 에 일반 PATCH/DELETE 를 열면
// "무엇까지 고칠 수 있나"가 다시 열린 질문이 되고, 그 질문은 항목 본문까지 번진다.
// 본문이 만들어진 시점의 사진이라는 규율은 DESIGN §11 이 적고 store 의 관문이 지킨다.
type cutAfterRequest struct {
	Project   string     `json:"project"`
	SessionID string     `json:"session_id"`
	Dep       afterInput `json:"dep"` // item·job·sha 중 정확히 하나
}

func (s *server) handleCutAfter(w http.ResponseWriter, r *http.Request) {
	var req cutAfterRequest
	if !s.decode(w, r, &req) {
		return
	}
	infoFrom(r.Context()).setSession(req.SessionID)
	res, err := s.svc.CutAfter(r.Context(), service.CutAfterInput{
		Project: req.Project, SessionID: req.SessionID,
		ItemID: r.PathValue("id"),
		Dep:    model.After{Item: req.Dep.Item, Job: req.Dep.Job, SHA: req.Dep.SHA},
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// ★ 화면에도 낸다. 이 쓰기는 **무엇이 걸려 있었는지를 지우는** 유일한 동사라,
	// 보고 있는 세션이 자기 항목의 전제가 방금 바뀐 것을 즉시 알아야 한다.
	// (원장은 store 가 item.after.cut 으로 따로 남긴다 — SSE 는 지금 보는 사람에게만 가고 사라진다.)
	s.publish(r, "item.after.cut", req.Project, req.SessionID, map[string]any{
		"item": clip(res.Item.ID, 100), "count": len(res.Item.After),
	})
	s.writeJSON(w, r, http.StatusOK, res)
}

// labelRequest 는 항목의 꼬리표를 고치는 요청이다.
//
// move·after/cut 과 같은 규율으로 **전용 동사**다 — 일반 PATCH 를 열면 "무엇까지
// 고칠 수 있나"가 다시 열린 질문이 되고, 그 질문은 항목 본문까지 번진다.
// 본문이 만들어진 시점의 사진이라는 규율은 DESIGN §11 이 적고 store 의 관문이 지킨다.
//
// 필드 이름이 cmd/fd 의 labelReq 와 어긋나면 서버가 조용히 0값을 받는다 —
// add·rm 이 둘 다 빈 채 닿으면 "하나는 줘라"로 거절되는데, 사람은 자기가 방금 친
// `--add` 를 다시 들여다본다. 이음매 시험이 잠근다.
type labelRequest struct {
	Project   string   `json:"project"`
	SessionID string   `json:"session_id"`
	Add       []string `json:"add"`
	Rm        []string `json:"rm"`
}

func (s *server) handleLabelItem(w http.ResponseWriter, r *http.Request) {
	var req labelRequest
	if !s.decode(w, r, &req) {
		return
	}
	infoFrom(r.Context()).setSession(req.SessionID)
	res, err := s.svc.SetLabels(r.Context(), service.LabelInput{
		Project: req.Project, SessionID: req.SessionID,
		ItemID: r.PathValue("id"), Add: req.Add, Rm: req.Rm,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// ★ SSE 알림용이다. 원장 행 자체는 store 가 트랜잭션 안에서 남긴다(item.label) —
	// before 를 아는 것이 거기뿐이기 때문이다. 여기서 다시 publish 하면 같은 사실이
	// 원장에 두 줄이 되므로, 이 호출은 **알림 축만** 태운다.
	s.publish(r, "item.label", req.Project, req.SessionID, map[string]any{
		"item": clip(res.Item.ID, 100), "added": res.Added, "removed": res.Removed,
	})
	s.writeJSON(w, r, http.StatusOK, res)
}
