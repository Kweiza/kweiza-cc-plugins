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
	// ★ 옛 클라이언트는 이 필드를 안 싣는다 — 그때 빈 문자열이고 「미상」이다.
	// 없는 것을 claude 로 접지 않는 규율이 여기서도 그대로다.
	Harness string `json:"harness"`
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
		Harness:       req.Harness,
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

// handleFindSession 은 3중키로 세션을 찾는다. **만들지 않는다.**
//
// 이 자리가 없어서 복구 갈래가 upsert 를 조회로 쓰고 있었고, 그것이 빈 카드를 낳았다.
// ★ **id 가 오면 좌표 해석을 아예 끈다.** 이 갈래를 새 라우트로 가르지 않은 이유는
// 대체재다(설계 §6 이 `POST /footprints` 를 지우고 `/workspaces` 를 남긴 그 기준) —
// 여기가 이미 "세션 하나를 찾는다"는 표면이고, 갈리는 것은 **무엇으로 지목하느냐**뿐이다.
// cc 축은 rekey 를 못 견디므로(service.SessionByID 주석) id 축이 그 대체가 아니라 짝이다.
func (s *server) handleFindSession(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if id := strings.TrimSpace(q.Get("id")); id != "" {
		sess, claims, err := s.svc.SessionByID(r.Context(), id)
		if err != nil {
			s.fail(w, r, err) // 없으면 notFound 가 404 로 나간다
			return
		}
		infoFrom(r.Context()).setSession(sess.ID)
		// ★ claims 에 omitempty 를 **안 붙인다.** 붙이면 "선점 0건"과 "이 서버는 선점을
		// 안 센다"가 같은 응답이 되고, 그 둘을 구분 못 하는 클라이언트는 낡은 서버를 만난 날
		// 선점을 든 카드를 조용히 닫는다. 빈 배열은 **센 결과**다.
		if claims == nil {
			claims = []string{}
		}
		s.writeJSON(w, r, http.StatusOK, map[string]any{"session": sess, "claims": claims})
		return
	}
	sess, err := s.svc.FindSession(r.Context(),
		strings.TrimSpace(q.Get("machine")),
		strings.TrimSpace(q.Get("worktree")),
		strings.TrimSpace(q.Get("cc")))
	if err != nil {
		s.fail(w, r, err) // 없으면 notFound 가 404 로 나간다
		return
	}
	infoFrom(r.Context()).setSession(sess.ID)
	s.writeJSON(w, r, http.StatusOK, map[string]any{"session": sess})
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
	// ★ **못 읽어도 5xx 를 내지 않는다**(DESIGN §5「쓰기 뒤 조회가 실패하면」). 상태 변경은
	// 위 SetState 에서 이미 원장에 남았다 — 여기서 실패로 응답하면 호출자는 안 바뀐 줄 알고
	// 다시 부르고, SSE 알림도 안 나가 다른 세션이 그 변경을 영영 못 본다.
	// 바로 아래 handleSignal 이 **같은 조회를 같은 자리에서** 무르게 하고 그 근거까지 적어
	// 뒀다 — 한 파일 안에 정반대의 실패 정책 둘이 있던 자리다.
	//
	// ★ 응답은 **아는 사실만** 낸다. 세션 행 전체를 못 읽었으므로 id·state 만 채우고,
	// 그것이 부분 응답이라는 사실을 partial 로 말한다 — 빈 세션 객체를 내면 "필드가 전부
	// 빈 세션"이라는 없는 사실을 만든다.
	sess, err := s.st.GetSession(r.Context(), id)
	if err != nil {
		s.log.WarnContext(r.Context(), "세션 상태 변경 뒤 조회 실패 — 변경은 원장에 남았다",
			"session_id", clip(id, 64), "state", clip(req.State, 32), "error", err.Error())
		s.publish(r, "session.state", "", id, map[string]any{"state": req.State})
		s.writeJSON(w, r, http.StatusOK, map[string]any{
			"session": map[string]any{"id": id, "state": req.State},
			"partial": "세션 행을 못 읽었다 — 상태 변경은 원장에 남았다. 이 응답의 session 은 " +
				"요청으로 아는 두 값뿐이다",
		})
		return
	}
	s.publish(r, "session.state", sess.Project, id, map[string]any{"state": req.State})
	s.writeJSON(w, r, http.StatusOK, map[string]any{"session": sess})
}

type rekeyRequest struct {
	CCSessionID string `json:"cc_session_id"`
}

// handleRekey 는 /clear·compact 로 갈린 대화의 새 cc 를 카드에 반영한다.
//
// ★ 훅만 이걸 부른다. MCP 는 자기 environ 이 exec 뒤 안 바뀌므로 새 cc 를 알 길이 없다
// (store.Tx.Rekey 주석 참조).
func (s *server) handleRekey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	infoFrom(r.Context()).setSession(id)
	var req rekeyRequest
	if !s.decode(w, r, &req) {
		return
	}
	out, err := s.svc.Rekey(r.Context(), id, req.CCSessionID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.publish(r, "session.rekey", out.Project, out.ID, map[string]any{"cc_session_id": out.CCSessionID})
	s.writeJSON(w, r, http.StatusOK, out)
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

// ─────────────────────────────────────────────────────────────────────────────
// POST /api/v1/footprints 는 **지웠다**(2026-08-05) — 그 자리의 기록
// ─────────────────────────────────────────────────────────────────────────────
//
// 여기 있던 것: footprintRequest · ValidateOrigin · NormalizeFootprints ·
// handleFootprints. 신호 없이 발자국만 남기는 표면이었고 origin=declared|claimed 를
// 받을 수 있어 Beat(observed 고정)와 표현력이 달랐다.
//
// 지운 근거는 그 표현력 차이가 **쓰이는 자리가 없다**는 실측 셋이다:
//
//	코드 전수    이 표면을 치는 클라이언트 0건(cmd/fd·hooks·mcpsrv·웹 자산 전부)
//	DB origin    observed 592 · claimed 140 · declared **0** (2026-08-05 실측)
//	Touch 호출부 이 핸들러 · service.Pick(claimed) · service.Beat(observed)
//
// claimed 는 Pick 이 선점 트랜잭션에서 직접 넣는다. declared 는 **생산자가 이 표면뿐**
// 이었고 그 표면을 아무도 안 쳤다 — 그래서 그 값의 행이 한 건도 없다. 즉 차이는
// 실재했으나 그 차이가 만든 데이터가 없었다.
//
// ★ 같은 "호출자 0건"인데 바로 위 handleWorkspace 는 **남겼다.** 기준은 대체재다 —
// 발자국은 이 표면이 없어도 두 문(Beat·Pick)으로 들어오지만, 부 워크스페이스를 넣을
// 문은 그것 하나뿐이다. 자세한 것은 store/session.go 의 AddWorkspace 주석에 있다.
//
// ★ 되살리려면 관문부터 붙여라. 이 핸들러의 NormalizeFootprints 는 좌표계 축만 태우고
// **포함 축을 안 봤다**(항목 fd-containment-gate-only-on-one-of-three-doors). 지금
// 살아 있는 두 문은 둘 다 RelPathWithin 을 태우며, 그 개수를 service 의
// TestFootprintDoorsAreExactlyTwo 가 전수로 잠근다.

// handlePrescriptions 는 이 세션이 지금 받아야 할 처방을 내고 발화를 기록한다.
//
// ★ POST 인 이유는 **부작용이 있어서**다 — 낸 것이 event 에 남는다.
// GET 으로 두면 프록시·재시도가 조용히 처방을 소모한다.
//
// ★ 본문 인자가 없다. 필요한 것은 전부 세션 id 로부터 파생된다 —
// 파생 가능한 사실에는 쓰기 파라미터를 만들지 않는다(설계 §1 원칙 ①).
func (s *server) handlePrescriptions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	infoFrom(r.Context()).setSession(id)
	res, err := s.svc.Prescriptions(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, res)
}
