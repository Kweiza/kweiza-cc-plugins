package main

import (
	"fmt"

	"github.com/kweiza/flightdeck/internal/service"
)

// REST 본문 타입 — **internal/api 의 요청 구조체와 필드 이름이 1:1 이어야 한다.**
//
// 그쪽 타입은 비공개라 여기서 다시 선언한다. 그래서 위험이 하나 생긴다:
// 이름이 갈라지면 서버가 조용히 0값을 받는다(빠진 필드는 오류가 아니다).
// 그 위험을 시험이 막는다 — wire_test.go 가 실제 서버에 붙여 왕복시킨다.
// 구조체를 눈으로 대조하는 시험은 쓰지 않는다: 사본을 단정하는 시험은 아무것도 지키지 못한다.

// afterWire 는 선행 조건이다. 셋 중 **정확히 하나**만 채운다.
// model.After 를 그대로 쓰지 않는 이유: 그 타입에는 json 태그가 없어
// 필드 이름이 "Item"·"SHA" 로 나가는데 서버는 "item"·"sha" 를 읽는다.
type afterWire struct {
	Item string `json:"item,omitempty"`
	Job  string `json:"job,omitempty"`
	SHA  string `json:"sha,omitempty"`
}

// linkWire 는 판단 링크다. 위와 같은 이유로 따로 있다.
type linkWire struct {
	TargetKind string `json:"target_kind"`
	TargetID   string `json:"target_id"`
}

type openReq struct {
	Project       string `json:"project"`
	ProjectPath   string `json:"project_path"`
	DefaultBranch string `json:"default_branch"`
	MachineID     string `json:"machine_id"`
	Hostname      string `json:"hostname"`
	Worktree      string `json:"worktree"`
	CCSessionID   string `json:"cc_session_id"`
	Label         string `json:"label"`
}

type beatReq struct {
	Kind  string   `json:"kind"`
	Paths []string `json:"paths,omitempty"`
}

type addReq struct {
	Project   string      `json:"project"`
	SessionID string      `json:"session_id"`
	ID        string      `json:"id"`
	Title     string      `json:"title"`
	Body      string      `json:"body"`
	Paths     []string    `json:"paths,omitempty"`
	Labels    []string    `json:"labels,omitempty"`
	After     []afterWire `json:"after,omitempty"`
}

type claimReq struct {
	Project   string `json:"project"`
	SessionID string `json:"session_id"`
	// ItemIDs 는 묶음 선점이다. internal/api 의 claimRequest.ItemIDs 와 이름이 같아야 한다 —
	// 어긋나면 서버가 조용히 0값을 받아 단독 선점으로 접힌다.
	ItemIDs []string `json:"item_ids,omitempty"`
}

type followupReq struct {
	ID     string      `json:"id"`
	Title  string      `json:"title"`
	Body   string      `json:"body"`
	Paths  []string    `json:"paths,omitempty"`
	Labels []string    `json:"labels,omitempty"`
	After  []afterWire `json:"after,omitempty"`
}

type finishReq struct {
	Project     string        `json:"project"`
	SessionID   string        `json:"session_id"`
	Outcome     string        `json:"outcome"`
	Title       string        `json:"title,omitempty"`
	Body        string        `json:"body"`
	CloseReason string        `json:"close_reason,omitempty"`
	Followups   []followupReq `json:"followups,omitempty"`
	Links       []linkWire    `json:"links,omitempty"`
}

type noteReq struct {
	Project    string     `json:"project"`
	SessionID  string     `json:"session_id"`
	Kind       string     `json:"kind"`
	Title      string     `json:"title,omitempty"`
	Body       string     `json:"body"`
	ItemID     string     `json:"item_id,omitempty"`
	Supersedes string     `json:"supersedes,omitempty"`
	Links      []linkWire `json:"links,omitempty"`
}

type counterReq struct {
	Project string `json:"project"`
}

type allocResp struct {
	Project string `json:"project"`
	Counter string `json:"counter"`
	Value   int64  `json:"value"`
}

// projectsResp 는 GET /api/v1/projects 의 응답 껍데기다.
//
// allocResp 와 같은 이유로 있다 — handleListProjects 가 내는 것은 service.ProjectSummary
// 를 감싼 map[string]any 라, 그 껍데기(키 "projects")를 벗길 타입이 필요하다. 안의 항목은
// service.ProjectSummary 를 그대로 다시 쓴다 — 그 타입에 이미 json 태그가 있고, 이 파일
// 머리의 규율(요청 본문은 internal/api 의 비공개 타입과 이름을 맞추려 사본을 둔다)은
// **쓰기** 요청에 대한 것이다. 이건 읽기 응답이고 원본이 이미 공개(exported)라 사본을
// 새로 정의할 이유가 없다.
type projectsResp struct {
	Projects []service.ProjectSummary `json:"projects"`
}

// projectRemoveReq 는 프로젝트 삭제 요청이다. 필드 이름이 internal/api 의 handleRemoveProject
// 요청 구조체와 어긋나면 서버가 조용히 0값을 받는다 — confirm 이 빈 채 닿으면 되돌릴 수 없는
// 삭제가 "확인 없음"으로 항상 접혀서(안전한 방향이라 시끄럽지 않다), --yes 를 줘도 아무 일도
// 안 일어나는 조용한 실패가 된다.
type projectRemoveReq struct {
	Actor   string `json:"actor"`
	Reason  string `json:"reason"`
	Confirm bool   `json:"confirm"`
}

// errBody 는 서버의 오류 응답이다(internal/api.ErrorBody 와 같은 모양).
// request_id 를 그대로 사람에게 보여준다 — 응답과 서버 로그를 잇는 유일한 열쇠다.
type errBody struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Guidance  string `json:"guidance"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}

// ─────────────────────────────────────────────────────────────────────────────
// 랜딩 레인
// ─────────────────────────────────────────────────────────────────────────────

// landingPath 는 land 세 갈래가 **공유하는 한 경로**다.
//
// ★ 상수로 두는 이유: 이 경로를 부르는 자리가 둘이다(cmds.go 의 runLand · mcpbackend.go 의
// Land·LandReport·LandLeave). 리터럴로 두 벌 적으면 한쪽만 고쳐진 날 MCP 는 404 를 받고
// CLI 는 멀쩡한데, 그 비대칭은 "MCP 가 고장났다"로만 보여 원인에 못 닿는다.
const landingPath = "/api/v1/landing"

// laneReleasePath 는 줄 행 하나의 회수 표면이다. **대상이 세션이 아니라 줄 행 번호다** —
// 죽은 세션 명의로는 아무 호출도 못 하므로(세션 정체가 3중키다) 번호가 유일한 손잡이다.
func laneReleasePath(rowID int64) string {
	return fmt.Sprintf("/api/v1/landing/rows/%d/release", rowID)
}

// landReq 는 POST /api/v1/landing 의 본문이다.
//
// ★ Mode 값은 지어내지 않고 **정본 표면의 상수**(api.LandModeAcquire 계열)를 쓴다.
// 필드 이름은 그렇게 못 한다(그쪽 구조체가 비공개다) — 그 축은 land_seam_test.go 가
// 실물 서버 왕복으로 잠근다. 이름이 어긋나면 서버가 오류 없이 0값을 받는데,
// mode 는 서버가 필수로 두어 그 0값이 400 으로 드러난다.
type landReq struct {
	Project   string `json:"project"`
	SessionID string `json:"session_id"`
	Mode      string `json:"mode"`
	Kind      string `json:"kind,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// laneReleaseReq 는 회수 본문이다. session_id 가 없는 이유는 위 laneReleasePath 주석에 있다.
type laneReleaseReq struct {
	Project string `json:"project"`
	Actor   string `json:"actor,omitempty"`
	Reason  string `json:"reason"`
}

// claimReleasePath 는 선점 하나의 회수 표면이다. 레인 회수와 같은 판정으로
// **세션이 아니라 항목 id 가 손잡이다** — 죽은 세션 명의로는 아무 호출도 못 한다.
func claimReleasePath(itemID string) string {
	return "/api/v1/items/" + urlPath(itemID) + "/claim/release"
}

// claimReleaseReq 는 선점 회수 본문이다. 필드 이름이 internal/api 의
// claimReleaseRequest 와 어긋나면 서버가 조용히 0값을 받는다 — actor 가 빈 채 닿으면
// 판단에 "행위자: 대시보드(사람)"가 영구히 박힌다(그 축은 이음매 시험이 잠근다).
type claimReleaseReq struct {
	Project string `json:"project"`
	Actor   string `json:"actor,omitempty"`
	Reason  string `json:"reason"`
}

// moveReq 는 POST /api/v1/items/{id}/move 의 본문이다.
// 필드 이름이 internal/api 의 moveRequest 와 어긋나면 서버가 조용히 0값을 받는다.
type moveReq struct {
	Project   string `json:"project"`
	SessionID string `json:"session_id"`
	To        string `json:"to"`
}

// afterCutPath 는 선행 하나를 끊는 표면이다.
func afterCutPath(itemID string) string {
	return "/api/v1/items/" + urlPath(itemID) + "/after/cut"
}

// afterCutReq 는 POST /api/v1/items/{id}/after/cut 의 본문이다.
//
// 필드 이름이 internal/api 의 cutAfterRequest 와 어긋나면 서버가 조용히 0값을 받는다 —
// 그리고 이 명령에서 그 실패는 특히 나쁘다. dep 이 빈 채 닿으면 서버는 "정확히 하나여야
// 한다"로 거절하는데, 사람은 자기가 방금 준 `--item` 을 다시 들여다본다. 이음매 시험이 잠근다.
//
// dep 은 **하나**다(add·finish 의 after 는 배열이다) — 이 동사는 한 번에 하나씩 끊는다.
// 여럿을 한 호출로 받으면 "셋 중 둘만 끊겼다"가 표현 불가능한 결과가 되고,
// 그때 사람이 무엇을 다시 시도해야 하는지 응답이 말할 수 없다.
type afterCutReq struct {
	Project   string    `json:"project"`
	SessionID string    `json:"session_id"`
	Dep       afterWire `json:"dep"`
}

// rekeyReq 는 POST /api/v1/sessions/{id}/rekey 의 본문이다.
// 필드 이름이 internal/api 의 rekeyRequest 와 어긋나면 서버가 조용히 0값을 받는다.
type rekeyReq struct {
	CCSessionID string `json:"cc_session_id"`
}

// patchStateReq 는 PATCH /api/v1/sessions/{id} 의 본문이다.
// 필드 이름이 internal/api 의 patchSessionRequest 와 어긋나면 서버가 조용히 0값을 받고,
// 상태는 안 바뀐 채 200 이 돌아온다 — 그 실패는 어느 화면에도 안 뜬다.
type patchStateReq struct {
	State string `json:"state"`
	Why   string `json:"why"`
}
