package api

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// 세션 표면 — D 계층(파생).
//
// ★ 이 파일의 요청 타입 어디에도 branch·head·sha·updated 필드가 **없다**.
// 설계 §5 의 "쓰기 API 에서 삭제된 필드" 목록이 곧 이 부재다 —
// 검사가 아니라 부재로 강제한다. 우회할 필드가 없으면 검사할 것도 우회할 것도 없다.

type openSessionRequest struct {
	Project       string `json:"project"`
	ProjectPath   string `json:"project_path"`
	DefaultBranch string `json:"default_branch"`
	MachineID     string `json:"machine_id"`
	Hostname      string `json:"hostname"`
	Worktree      string `json:"worktree"`
	CCSessionID   string `json:"cc_session_id"`
	Label         string `json:"label"`
}

// handleOpenSession 은 세션을 열거나(신규) 그대로 돌려준다(재개).
func (s *server) handleOpenSession(w http.ResponseWriter, r *http.Request) {
	var req openSessionRequest
	if !s.decode(w, r, &req) {
		return
	}
	res, err := s.svc.OpenSession(r.Context(), service.OpenSessionInput{
		Project:       req.Project,
		ProjectPath:   req.ProjectPath,
		DefaultBranch: req.DefaultBranch,
		MachineID:     req.MachineID,
		Hostname:      req.Hostname,
		Worktree:      req.Worktree,
		CCSessionID:   req.CCSessionID,
		Label:         req.Label,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	infoFrom(r.Context()).setSession(res.Session.ID)
	s.publish(r, "session.open", res.Project.ID, res.Session.ID, map[string]any{
		"created": res.Created, "label": clip(res.Session.Label, 120),
	})
	status := http.StatusOK
	if res.Created {
		status = http.StatusCreated
	}
	s.writeJSON(w, r, status, res)
}

type patchSessionRequest struct {
	State string `json:"state"`
	Why   string `json:"why"`
}

// handlePatchSession 은 세션 상태를 바꾼다.
//
// active 는 파생이라 여기서 못 쓴다 — 그 판정은 store.ValidateSessionState 가 하고
// 이 계층은 흉내 내지 않는다(흉내 내면 두 벌이 되고 두 벌은 표류한다).
func (s *server) handlePatchSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	infoFrom(r.Context()).setSession(id)
	var req patchSessionRequest
	if !s.decode(w, r, &req) {
		return
	}
	if err := s.svc.SetState(r.Context(), id, model.SessionState(req.State), req.Why); err != nil {
		s.fail(w, r, err)
		return
	}
	sess, err := s.st.GetSession(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.publish(r, "session.state", sess.Project, id, map[string]any{"state": req.State})
	s.writeJSON(w, r, http.StatusOK, map[string]any{"session": sess})
}

type signalRequest struct {
	Kind  string   `json:"kind"`
	Paths []string `json:"paths"`
}

// handleSignal 은 생존 신호 하나와(있으면) 미커밋 발자국을 기록한다.
//
// 신호는 **사실**이지 판정이 아니다. 넷을 나란히 쌓고 합치지 않는다.
func (s *server) handleSignal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	infoFrom(r.Context()).setSession(id)
	var req signalRequest
	if !s.decode(w, r, &req) {
		return
	}
	if err := s.svc.Beat(r.Context(), id, model.SignalKind(req.Kind), req.Paths); err != nil {
		s.fail(w, r, err)
		return
	}
	project := ""
	if sess, err := s.st.GetSession(r.Context(), id); err == nil {
		project = sess.Project
	} else {
		// 신호는 이미 기록됐다. 프로젝트를 못 읽은 것은 알림의 라우팅에만 영향을 준다.
		s.log.WarnContext(r.Context(), "신호 기록 뒤 세션 조회 실패",
			"session_id", clip(id, 64), "error", err.Error())
	}
	s.publish(r, "session.signal", project, id, map[string]any{
		"kind": clip(req.Kind, 32), "count": len(req.Paths),
	})
	s.writeJSON(w, r, http.StatusAccepted, map[string]any{
		"session_id": id, "kind": req.Kind, "count": len(req.Paths),
	})
}

type workspaceRequest struct {
	Project   string `json:"project"`
	Path      string `json:"path"`
	IsPrimary bool   `json:"is_primary"`
}

// ValidateWorkspacePath 는 작업 트리 경로가 성립하는지 본다. 순수 함수다.
//
// 절대경로가 아니면 거절한다 — 서버와 세션이 서로 다른 곳을 가리키게 되고,
// 그러면 경로 겹침 축이 조용히 죽는다(상대경로는 아무것과도 안 겹친다).
func ValidateWorkspacePath(p string) error {
	q := strings.TrimSpace(p)
	switch {
	case q == "":
		return fmt.Errorf("작업 트리 경로가 비었다")
	case !filepath.IsAbs(q):
		return fmt.Errorf("작업 트리 경로 %q 가 절대경로가 아니다", clip(q, 200))
	}
	return nil
}

// handleWorkspace 는 세션에 작업 트리를 하나 더 붙인다.
//
// 한 세션이 코드 레포와 문서 레포를 함께 만지는 실무가 있다 — 단수 project 필드로는
// 담기지 않아서 별도 표면이 있는 것이다(설계 §3 의 session_workspace).
// handleWorkspace 는 세션에 작업 트리 하나를 붙인다.
//
// ★ **이 표면을 치는 클라이언트가 하나도 없다**(2026-08-04 실측 — cmd/fd 에도 mcpsrv 에도
// "workspaces" 를 치는 코드가 0건). 그래서 session_workspace 에 실제로 들어가는 것은
// OpenSession 이 넣는 primary 하나뿐이고, 그 값은 session.worktree 와 같다.
// 이 사실이 왜 중요한지(그리고 이 표를 무엇의 근거로 쓰면 안 되는지)는
// store/session.go 의 AddWorkspace 주석에 있다.
func (s *server) handleWorkspace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	infoFrom(r.Context()).setSession(id)
	var req workspaceRequest
	if !s.decode(w, r, &req) {
		return
	}
	if err := ValidateWorkspacePath(req.Path); err != nil {
		s.writeError(w, r, badRequest("bad_workspace_path", err.Error(),
			"MCP 서버의 cwd 나 워크트리의 절대경로를 보내라."))
		return
	}
	if strings.TrimSpace(req.Project) == "" {
		s.writeError(w, r, badRequest("missing_project", "project 가 비었다",
			"어느 프로젝트의 작업 트리인지 없이는 좌표가 없다."))
		return
	}
	ws := model.Workspace{SessionID: id, Project: req.Project, Path: req.Path, IsPrimary: req.IsPrimary}
	if err := s.st.AddWorkspace(r.Context(), ws); err != nil {
		s.fail(w, r, err)
		return
	}
	s.publish(r, "session.workspace", req.Project, id, map[string]any{"is_primary": req.IsPrimary})
	s.writeJSON(w, r, http.StatusCreated, map[string]any{"workspace": ws})
}

type footprintRequest struct {
	SessionID string   `json:"session_id"`
	Paths     []string `json:"paths"`
	Origin    string   `json:"origin"`
}

// ValidateOrigin 은 발자국의 출처를 판정한다. 순수 함수다.
//
// 빈 값은 observed 로 본다(훅이 보내는 기본 경로다). 모르는 값은 **거절한다** —
// 셋을 뭉개면 "선언했으나 안 건드림"과 "선언 없이 건드림"이 구분되지 않고,
// 그 구분이 없으면 겹침 축이 무엇을 근거로 삼았는지 아무도 모른다.
func ValidateOrigin(o string) (model.FootprintOrigin, error) {
	switch model.FootprintOrigin(strings.TrimSpace(o)) {
	case "":
		return model.OriginObserved, nil
	case model.OriginObserved:
		return model.OriginObserved, nil
	case model.OriginDeclared:
		return model.OriginDeclared, nil
	case model.OriginClaimed:
		return model.OriginClaimed, nil
	default:
		return "", fmt.Errorf("발자국 출처 %q 가 열거에 없다 — observed|declared|claimed 중 하나여야 한다",
			clip(o, 32))
	}
}

// NormalizeFootprints 는 훅이 준 절대경로를 저장소 좌표계로 옮긴다. 순수 함수다.
//
// ★ 좌표계를 안 맞추면 겹침 축이 **조용히** 죽는다. 훅은 절대경로를 주고
// git 은 저장소 상대를 주므로, 둘을 그대로 두면 같은 파일이 서로 다른 문자열이 되어
// 아무와도 안 겹친다 — 그리고 그 결과는 "겹침 없음"이라는 정상 응답과 구분되지 않는다.
func NormalizeFootprints(worktree string, paths []string) []string {
	rels := make([]string, 0, len(paths))
	for _, p := range paths {
		rels = append(rels, service.RelPath(worktree, p))
	}
	return service.UnionPaths(rels)
}

// handleFootprints 는 발자국을 기록한다.
//
// 신호 없이 발자국만 남기는 경로다(선언·항목 경로). 생존 신호와 함께 오는 것은
// POST /sessions/{id}/signals 쪽이다 — 둘을 한 표면으로 합치면
// "선언했다"가 "살아 있다"로 둔갑한다.
func (s *server) handleFootprints(w http.ResponseWriter, r *http.Request) {
	var req footprintRequest
	if !s.decode(w, r, &req) {
		return
	}
	infoFrom(r.Context()).setSession(req.SessionID)
	if strings.TrimSpace(req.SessionID) == "" {
		s.writeError(w, r, badRequest("missing_session_id", "session_id 가 비었다",
			"발자국은 세션에 귀속된다 — 주인 없는 경로는 겹침 판정에 쓸 수 없다."))
		return
	}
	origin, err := ValidateOrigin(req.Origin)
	if err != nil {
		s.writeError(w, r, badRequest("bad_origin", err.Error(),
			"훅이 관측한 것은 observed, 세션이 선언한 것은 declared, 항목이 선언한 것은 claimed 다."))
		return
	}
	sess, err := s.st.GetSession(r.Context(), req.SessionID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rels := NormalizeFootprints(sess.Worktree, req.Paths)
	now := s.now()
	for _, p := range rels {
		if err := s.st.Touch(r.Context(), req.SessionID, p, origin, now); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	s.publish(r, "session.footprint", sess.Project, req.SessionID, map[string]any{
		"origin": string(origin), "count": len(rels),
	})
	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"session_id": req.SessionID, "origin": string(origin),
		"count": len(rels), "paths": rels,
	})
}
