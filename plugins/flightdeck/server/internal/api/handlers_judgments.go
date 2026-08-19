package api

import (
	"net/http"
	"strings"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
	"github.com/kweiza/flightdeck/internal/store"
)

// 판단·발번·스냅숏 표면 — J 계층과 그 이웃.
//
// ★ J 계층에는 PUT·DELETE 가 **없다**. 판단은 추가 전용이고 정정은 새 행 + supersedes 다.
// 표면에 수정 경로를 두면 스키마의 트리거가 막는 사고(남의 절을 덮어써 원문이 영구 소실)를
// 표면이 다시 열어 주는 꼴이 된다.

type noteRequest struct {
	Project   string `json:"project"`
	SessionID string `json:"session_id"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	ItemID    string `json:"item_id"`
	// ★ links 에는 대상 프로젝트를 안 연다. 그 경로는 resolveItemProject 의 검증을
	//   안 타므로, 거기에 프로젝트를 실을 수 있게 하면 **거절 층을 우회하는 문**이 된다.
	//   교차 링크는 item_id + item_project 로만 만든다.
	ItemProject string      `json:"item_project"`
	Supersedes  string      `json:"supersedes"`
	Links       []linkInput `json:"links"`
}

// handleAddJudgment 은 판단 하나를 남긴다.
func (s *server) handleAddJudgment(w http.ResponseWriter, r *http.Request) {
	var req noteRequest
	if !s.decode(w, r, &req) {
		return
	}
	infoFrom(r.Context()).setSession(req.SessionID)
	res, err := s.svc.Note(r.Context(), service.NoteInput{
		Project: req.Project, SessionID: req.SessionID, Kind: model.JudgmentKind(req.Kind),
		Title: req.Title, Body: req.Body, ItemID: req.ItemID,
		ItemProject: req.ItemProject,
		Supersedes:  req.Supersedes, Links: toLinks(req.Links),
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.publish(r, "judgment.note", req.Project, req.SessionID, map[string]any{
		"mode": clip(req.Kind, 32), "count": len(res.Recipients), "bytes": len(req.Body),
	})
	s.writeJSON(w, r, http.StatusCreated, res)
}

// handleSearchJudgments 는 판단을 전문 검색한다.
//
// q 가 비면 **거절한다**. 빈 질의를 "전부"로 접으면 판단 표 전체가 한 번에 나가고,
// 그 응답은 어느 소비자에게도 쓸모가 없는데 컨텍스트만 통째로 태운다.
func (s *server) handleSearchJudgments(w http.ResponseWriter, r *http.Request) {
	q, ok := s.requireQuery(w, r, "q", "검색어 없이는 무엇을 찾는지 알 수 없다 — 목록이 필요하면 dashboard.json 을 써라.")
	if !ok {
		return
	}
	limit, err := queryInt(r, "limit", 20)
	if err != nil {
		s.writeError(w, r, badRequest("bad_limit", "limit 이 정수가 아니다", "예: limit=20"))
		return
	}
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	project := strings.TrimSpace(r.URL.Query().Get("project")) // 비면 전 프로젝트
	js, err := s.st.SearchJudgments(r.Context(), project, q, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"query": q, "project": project, "count": len(js), "judgments": js,
	})
}

type counterRequest struct {
	Project string `json:"project"`
}

// handleCounter 는 논리 카운터의 다음 번호를 발급한다.
//
// ★ 값을 읽어 +1 하는 경로를 표면에 두지 않는다. 그것이 락으로 원리적으로 못 막는
// 사고(두 세션이 같은 개정 차수를 쓴다)의 모양이고, 그래서 여기는 발번만 있다.
func (s *server) handleCounter(w http.ResponseWriter, r *http.Request) {
	var req counterRequest
	if !s.decode(w, r, &req) {
		return
	}
	name := r.PathValue("name")
	n, err := s.svc.Alloc(r.Context(), req.Project, name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.publish(r, "counter.alloc", req.Project, "", map[string]any{
		"mode": clip(name, 64), "count": n,
	})
	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"project": req.Project, "counter": name, "value": n,
	})
}

// handleGetSnapshot 은 보관된 수치 하나를 읽는다.
//
// "낡음" 판정은 여기서 하지 않는다 — input_digest 를 현재 트리와 대조하는 것은
// git 을 읽는 계층의 몫이고, 표면이 그 판정을 흉내 내면 두 벌이 된다.
func (s *server) handleGetSnapshot(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireQuery(w, r, "project", "스냅숏 키는 프로젝트 안에서만 유일하다.")
	if !ok {
		return
	}
	sn, err := s.st.GetSnapshot(r.Context(), project, r.PathValue("key"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{"snapshot": sn})
}

type snapshotRequest struct {
	Project     string `json:"project"`
	Value       string `json:"value"`
	Method      string `json:"method"`
	Evidence    string `json:"evidence"`
	InputDigest string `json:"input_digest"`
}

// handlePutSnapshot 은 수치 하나를 보관한다.
//
// method='manual' 인데 근거가 없으면 거절한다 — 판정은 store.ValidateSnapshot(순수 함수)이
// 하고 이 계층은 흉내 내지 않는다. "손으로 올리지 마라"는 규율이 제약이 되는 자리다.
func (s *server) handlePutSnapshot(w http.ResponseWriter, r *http.Request) {
	var req snapshotRequest
	if !s.decode(w, r, &req) {
		return
	}
	sn := model.Snapshot{
		Project: req.Project, Key: r.PathValue("key"), Value: req.Value,
		Method: model.SnapshotMethod(req.Method), Evidence: req.Evidence,
		InputDigest: req.InputDigest, ComputedAt: s.now(),
	}
	if err := store.ValidateSnapshot(sn); err != nil {
		s.writeError(w, r, badRequest("bad_snapshot", err.Error(),
			"근거 없는 숫자를 넣으면 그 순간 이 표를 아무도 못 믿는다 — 어떻게 잰 값인지 evidence 에 적어라."))
		return
	}
	if err := s.st.PutSnapshot(r.Context(), sn); err != nil {
		s.fail(w, r, err)
		return
	}
	s.publish(r, "snapshot.put", req.Project, "", map[string]any{
		"mode": clip(req.Method, 32), "item": clip(sn.Key, 100),
	})
	s.writeJSON(w, r, http.StatusOK, map[string]any{"snapshot": sn})
}
