package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// 랜딩 레인 표면 — 순서(landing_queue)와 배타(resource_hold).
//
// 표면이 **셋이다.**
//
//	POST /api/v1/landing                      줄 서기 · 보고+반납 · 이탈 (mode 가 고른다)
//	POST /api/v1/landing/rows/{id}/release     사람의 회수
//	GET  /api/v1/landing/queue?project=…       줄 전체의 읽기 전용 조회 (아래 ★ 좁힘)
//
// ★ 셋(서기·보고·이탈)을 한 라우트에 모은 이유: 셋은 같은 좌표(project, session)에 대한
// **같은 줄 행의 전이**이고, 응답 타입도 하나(service.LandResult)다. 라우트를 셋으로 쪼개면
// 멱등 표(JudgePersistIdempotency)·지표 라벨·클라이언트 열화 표가 전부 세 벌이 되는데,
// 그 세 벌이 갈라졌을 때 어느 쪽이 참인지 말해 주는 자리가 없다.
//
// ★ **"지금 내 차례인가"에는 여전히 GET 이 없다.** 그 질문은 mode=acquire 만 답한다
// (재진입 안전이라 다시 물어도 새 자리가 안 생긴다 — service.Land 의 계약). 그 질문에
// GET 을 열면 클라이언트가 그 응답을 캐시하고, 서버가 죽은 뒤 30분 전의 "네 차례다"가
// 그대로 나온다. 배타가 깨지는 게 아니라 **우회된다**(cmd/fd 의 Client.Read 주석에 같은
// 판정이 있다). 이 판정은 그대로다 — 아래 GET /landing/queue 가 여는 것은 **다른 질문**이다.
//
// ★ 이 문단은 2026-08-12 에 좁혀졌다(handleLandingQueue 주석 참조): **취득 판정**은 여전히
// POST(mode=acquire) 하나뿐이고, 새로 연 GET 은 "차례로 보이는가"의 힌트일 뿐이다.

// 랜딩 모드 — POST /api/v1/landing 의 `mode` 어휘다.
//
// ★ **이 상수가 이 어휘의 정본이다.** 클라이언트(cmd/fd)가 값을 다시 적지 않고 이것을
// import 해 쓴다. 값을 두 벌로 두면 한쪽만 고쳐진 날 서버가 "모르는 mode" 로 거절하는데,
// 그 거절은 배포 스큐 구간에 **랜딩 전체가 멈추는** 모양으로 나타난다.
// 이름(json 태그)까지 공유할 수는 없다 — 요청 구조체는 이 패키지의 비공개 타입이고,
// 그 축은 cmd/fd 의 land_seam_test.go 가 실물 왕복으로 잠근다.
const (
	// LandModeAcquire 는 줄에 서거나 **이미 서 있으면 내 자리를 다시 낸다.**
	LandModeAcquire = "acquire"
	// LandModeReport 는 레인을 쓰고 난 결과를 보고하고 반납한다(kind=ok|fail).
	LandModeReport = "report"
	// LandModeLeave 는 줄에서 스스로 빠진다. 레인 미보유여도 성립한다.
	LandModeLeave = "leave"
)

// landRequest 는 POST /api/v1/landing 의 본문이다.
//
// ★ mode 를 **필수**로 둔다. 빈 값을 acquire 로 접으면, 클라이언트의 필드 이름이
// 하나 어긋나 mode 가 0값으로 도착했을 때 서버가 그것을 "줄에 서겠다"로 읽는다 —
// 세션은 반납했다고 믿는데 원장에는 다시 줄에 선 행이 생기고, 레인은 안 풀린다.
// 그 어긋남은 어느 화면에도 안 뜬다. 필수로 두면 같은 사고가 400 한 줄로 바뀐다.
type landRequest struct {
	Project   string `json:"project"`
	SessionID string `json:"session_id"`
	Mode      string `json:"mode"`
	Kind      string `json:"kind"`   // mode=report 일 때 ok|fail
	Detail    string `json:"detail"` // fail·leave 는 필수(store.ValidateLandingLeave 가 본다)
	// Resources 는 mode=acquire 일 때만 쓴다 — 비면 service.LandInput 이 기존 단일
	// 레인({service.LaneResource})으로 정규화한다(Task 3). report·leave 는 이미 선
	// 자원 집합을 줄 행에서 읽으므로 이 필드를 안 본다.
	Resources []string `json:"resources"`
}

// handleLand 는 줄 서기·보고·이탈 셋을 mode 로 가른다.
//
// 종류 검사(ok|fail)와 사유 필수 판정은 **여기서 흉내 내지 않는다** — service 가
// store.ValidateLandingLeave 로 이미 하고, 그 거절에는 처방까지 붙어 있다.
// 이 계층이 한 벌 더 두면 두 벌이 되고, 두 벌은 반드시 표류한다.
func (s *server) handleLand(w http.ResponseWriter, r *http.Request) {
	var req landRequest
	if !s.decode(w, r, &req) {
		return
	}
	infoFrom(r.Context()).setSession(req.SessionID)

	mode := strings.TrimSpace(req.Mode)
	var res service.LandResult
	var err error
	switch mode {
	case LandModeAcquire:
		res, err = s.svc.Land(r.Context(), service.LandInput{
			Project: req.Project, SessionID: req.SessionID, Resources: req.Resources,
		})
	case LandModeReport:
		res, err = s.svc.LandReport(r.Context(), service.LandReportInput{
			Project: req.Project, SessionID: req.SessionID,
			Kind: model.LandingLeftKind(strings.TrimSpace(req.Kind)), Detail: req.Detail,
		})
	case LandModeLeave:
		res, err = s.svc.LandLeave(r.Context(), service.LandLeaveInput{
			Project: req.Project, SessionID: req.SessionID, Detail: req.Detail,
		})
	default:
		// 문구를 err.Error() 에서 만들지 않는다(errors.go 의 규율) — 받은 값은 외부에서
		// 온 것이라 자르고 제어문자를 걷어낸 뒤에만 싣는다.
		s.writeError(w, r, badRequest("bad_mode",
			"랜딩 mode 가 "+strconv.Quote(clip(mode, 40))+" 다 — acquire|report|leave 중 하나여야 한다",
			"줄에 서거나 내 자리를 다시 물으려면 acquire, 다 쓰고 반납하려면 report(kind=ok|fail), "+
				"줄에서 빠지려면 leave 다. 비어 있으면 클라이언트가 mode 를 안 실은 것이다."))
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// 알림에 낸다. 레인은 프로젝트당 하나라 **누가 언제 쥐고 놓았는지**가 다른 세션에게
	// 곧바로 쓸모 있는 사실이다 — 조용하면 그 사실을 폴링으로만 알 수 있다.
	s.publish(r, "lane."+mode, req.Project, req.SessionID, map[string]any{
		"row": res.RowID, "state": res.State, "count": res.Position,
		"resources": len(res.Resources),
	})
	s.writeJSON(w, r, http.StatusOK, res)
}

// handleLandingQueue 는 줄 전체의 읽기 전용 조회다 — fd lane wait 의 폴링이 이것을 친다.
//
// ★ 머리 주석의 "읽기(GET)가 없다"는 2026-08-12 에 좁혀졌다: **취득 판정**은 여전히
// POST(mode=acquire)만 한다. 이 GET 은 "차례로 보이는가"의 힌트일 뿐이고, 클라이언트는
// 캐시를 안 타는 직행(client.ReadFresh — Healthz 선례)으로만 읽는다. 캐시된 줄 상태로
// 취득을 판정하면 배타가 우회된다는 원판정은 그대로다.
func (s *server) handleLandingQueue(w http.ResponseWriter, r *http.Request) {
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	view, err := s.svc.LandingLane(r.Context(), project)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, view)
}

// laneReleaseRequest 는 POST /api/v1/landing/rows/{id}/release 의 본문이다.
//
// session_id 가 **없다.** 대상은 줄 행 번호이고, 그것이 이 표면의 존재 이유다 —
// 세션 정체가 (machine, worktree, cc_session_id) 라 죽은 세션 명의로는 아무 호출도 못 한다.
// 물린 레인을 그 세션 이름으로 풀게 만들면 탈출구가 없다.
type laneReleaseRequest struct {
	Project string `json:"project"`
	Actor   string `json:"actor"`  // 누가 회수했나. 판단 본문에 그대로 남는다
	Reason  string `json:"reason"` // 왜. **비면 service 가 거절한다**
}

// handleReleaseLaneRow 는 사람이 줄 행 하나를 회수한다.
//
// ★ 자동 만료가 아니다. 이 표면은 **사람이 다섯 축을 본 뒤에** 부르는 자리이고
// (설계 §4), 그래서 사유가 필수이며 판단 하나를 원장에 남긴다.
func (s *server) handleReleaseLaneRow(w http.ResponseWriter, r *http.Request) {
	var req laneReleaseRequest
	if !s.decode(w, r, &req) {
		return
	}
	raw := r.PathValue("id")
	rowID, perr := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if perr != nil {
		// 파서의 오류 문구에는 Go 타입명이 섞인다. 응답에는 받은 값만 싣는다.
		s.writeError(w, r, badRequest("bad_row",
			"줄 행 번호 "+strconv.Quote(clip(raw, 40))+" 를 정수로 읽을 수 없다",
			"회수 대상은 줄 행 하나다 — 번호는 보드의 레인 절이 낸다."))
		return
	}
	res, err := s.svc.ReleaseLaneRow(r.Context(), req.Project, rowID, req.Actor, req.Reason)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// 회수당한 세션을 액세스 로그의 좌표로 삼는다 — 요청 본문에는 그 세션이 없고,
	// "누구의 레인이 끊겼나"가 이 한 줄에서 답해져야 한다.
	infoFrom(r.Context()).setSession(res.SessionID)
	// ★ 키 둘을 형제 이벤트와 같은 어휘로 맞춘다.
	//
	//	"state" 는 형제(lane.land)에서 **문자열**(줄 상태)이라 거기에 불리언을 실으면
	//	같은 키가 두 가지 타입을 뜻하고, str("state") 로 읽는 이 레포의 관용적 소비자는
	//	조용히 빈 문자열을 받는다. → "held_release"
	//	"mode" 는 형제에서 acquire|report|leave 다. 사람 이름의 자리가 아니다. → "actor"
	s.publish(r, "lane.release", req.Project, res.SessionID, map[string]any{
		"row": res.RowID, "held_release": res.HeldRelease, "actor": clip(req.Actor, 64),
	})
	s.writeJSON(w, r, http.StatusOK, res)
}
