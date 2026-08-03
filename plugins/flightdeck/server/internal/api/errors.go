package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/kweiza/flightdeck/internal/service"
	"github.com/kweiza/flightdeck/internal/store"
)

// 오류 표면 — **원인 전문은 로그로, 고칠 거리는 응답으로.**
//
// ★ 이 파일의 규율 하나: 응답 문구를 err.Error() 에서 만들지 않는다.
// 오류 문자열에는 SQL·파일 경로·드라이버 이름·Go 타입명이 섞여 들어오고,
// 그것을 그대로 내보내면 무인증으로 열려 있는 표면에서 서버 내부가 새어 나간다.
// 대신 **타입이 이미 들고 있는 도메인 필드**로 문구를 조립한다 — 그러면 새는 것이
// 검사로 막히는 것이 아니라 구조적으로 불가능해진다.
//
// 도메인 식별자(프로젝트·항목·세션 id)는 뺄 수 없다. 점유자를 못 내면
// "누구에게 물어야 하나"에 답이 없어지고, 그것이 이 제품이 없애려는 바로 그 추측이다.

// ErrorBody 는 오류 응답 본문이다.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail 은 소비자가 읽는 오류 하나다.
//
// RequestID 가 여기 있는 이유: 응답과 서버 로그를 잇는 유일한 열쇠다.
// 원인 전문을 응답에서 뺀 대가로 반드시 있어야 하는 축이다.
type ErrorDetail struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Guidance  string `json:"guidance,omitempty"`
	RequestID string `json:"request_id"`
}

// Classified 는 오류 하나의 분류 결과다.
//
// Internal=true 면 원인 전문을 응답에 싣지 않고 로그로만 낸다.
// 이 축을 불리언 하나로 두는 이유는, "설명해도 되는 실패"와 "설명하면 새는 실패"가
// 상태코드와 1:1 이 아니기 때문이다(400 인데 내부 문자열이 섞인 오류가 존재한다).
type Classified struct {
	Status   int
	Code     string
	Message  string
	Guidance string
	Internal bool
}

// ClassifyError 는 하류 오류를 소비자가 읽을 응답으로 옮긴다. 순수 함수다.
//
// 아는 타입만 문구를 만들고 **나머지는 전부 500 + 고정 문구**다.
// 화이트리스트가 아니라 블랙리스트로 두면(= 아는 것만 가리고 나머지는 통과)
// 새 오류 타입이 생기는 날 조용히 새기 시작한다.
func ClassifyError(err error) Classified {
	if err == nil {
		return Classified{Status: http.StatusOK, Code: "ok", Message: "오류가 없다"}
	}

	var refused *service.RefusedError
	if errors.As(err, &refused) {
		return Classified{
			Status:   http.StatusBadRequest,
			Code:     "refused",
			Message:  fmt.Sprintf("%s 거절: %s", refused.What, refused.Reason),
			Guidance: refused.Guidance,
		}
	}

	var held *store.ClaimHeldError
	if errors.As(err, &held) {
		return Classified{
			Status: http.StatusConflict,
			Code:   "claim_held",
			Message: fmt.Sprintf("항목 %s 는 세션 %s 가 쥐고 있다(%s부터). %s",
				held.ItemID, held.Holder, held.At.UTC().Format("2006-01-02 15:04Z"), held.Reason),
			Guidance: "그 세션에 물어라 — 회수는 사람만 할 수 있고 사유가 필수다.",
		}
	}

	var claimRefused *store.ClaimRefusedError
	if errors.As(err, &claimRefused) {
		return Classified{
			Status:  http.StatusConflict,
			Code:    "claim_refused",
			Message: fmt.Sprintf("항목 %s 를 선점할 수 없다: %s", claimRefused.ItemID, claimRefused.Reason),
		}
	}

	var resHeld *store.ResourceHeldError
	if errors.As(err, &resHeld) {
		return Classified{
			Status: http.StatusConflict,
			Code:   "resource_held",
			Message: fmt.Sprintf("자원 %s 는 %s 가 쥐고 있다(%s부터)",
				resHeld.Resource, resHeld.Holder.String(), resHeld.AcquiredAt.UTC().Format("2006-01-02 15:04Z")),
			Guidance: "점유자에게 물어라 — 이 서버는 자동 회수를 하지 않는다.",
		}
	}

	var conflict *store.ConflictError
	if errors.As(err, &conflict) {
		return ConflictAdvice(conflict)
	}

	if errors.Is(err, store.ErrNotFound) {
		return Classified{
			Status:  http.StatusNotFound,
			Code:    "not_found",
			Message: "요청한 대상이 없다 — 좌표(프로젝트·항목·세션 id)를 확인해라",
		}
	}

	return Classified{
		Status:   http.StatusInternalServerError,
		Code:     "internal",
		Message:  "서버 내부 오류다. 원인 전문은 서버 로그에 있다",
		Guidance: "이 응답의 request_id 로 로그를 찾아라 — 응답에는 내부 좌표를 싣지 않는다.",
		Internal: true,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 제약 위반 → 소비자가 읽는 문구
// ─────────────────────────────────────────────────────────────────────────────

// conflictWords 는 대상 하나의 사람용 이름과 처방이다.
//
// 저장 계층은 기계가 읽는 좌표(ConflictTarget)만 올린다. 한국어 문구를 그쪽에 두면
// 같은 문구가 표면마다 두 벌이 되고, 두 벌은 반드시 표류한다.
type conflictWords struct {
	Name string // "항목"·"판단"…
	Dup  string // 중복일 때의 처방
	Ref  string // 가리킨 것이 없을 때의 처방
}

// conflictWordTable 은 대상 전부의 문구다.
//
// ★ 시험이 store.ConflictTargets() 로 **전수** 확인한다. 대상을 하나 늘리고 여기를
// 안 채우면 그 하나만 기본 문구로 새어 나가는데, 기본 문구는 무엇을 고칠지 말하지 못한다.
var conflictWordTable = map[store.ConflictTarget]conflictWords{
	store.TargetItem: {
		Name: "항목",
		Dup:  "다른 id 를 쓰거나, 이미 있는 그것을 집어라(pick <id>). 항목 id 는 브랜치 이름이라 전역 유일해야 한다.",
		Ref:  "프로젝트를 먼저 등록해라 — 세션을 한 번 열면 등록된다(fd open).",
	},
	store.TargetItemAfter: {
		Name: "선행 조건",
		Dup:  "같은 선행 조건이 이미 걸려 있다 — 더 걸 것이 없다.",
		Ref:  "선행 조건을 걸 항목이 먼저 있어야 한다.",
	},
	store.TargetClaim: {
		Name: "선점",
		Dup:  "이미 선점 행이 있다 — 반납했다면 다시 집을 수 있다.",
		Ref:  "세션과 항목이 먼저 등록돼 있어야 한다 — fd open 으로 세션을 열어라.",
	},
	store.TargetJudgment: {
		Name: "판단",
		Dup:  "판단 id 가 이미 있다 — 판단은 추가 전용이라 덮어쓰지 않는다. 정정은 새 판단 + supersedes 다.",
		Ref:  "판단이 가리키는 프로젝트·세션·supersedes 중 등록되지 않은 것이 있다 — 세션을 먼저 열어라(fd open).",
	},
	store.TargetJudgmentLink: {
		Name: "판단 링크",
		Dup:  "같은 링크가 이미 걸려 있다.",
		Ref:  "링크를 걸 판단이 먼저 있어야 한다.",
	},
	store.TargetSnapshot: {
		Name: "스냅숏",
		Dup:  "같은 키의 스냅숏이 이미 있다.",
		Ref:  "프로젝트를 먼저 등록해라 — 세션을 한 번 열면 등록된다(fd open).",
	},
	store.TargetSession: {
		Name: "세션",
		Dup:  "같은 3중키(머신·워크트리·cc 세션)의 세션이 이미 있다 — 그것이 재개 경로다.",
		Ref:  "프로젝트와 머신이 먼저 등록돼 있어야 한다.",
	},
	store.TargetSessionWorkspace: {
		Name: "세션 워크스페이스",
		Dup:  "같은 경로가 이미 붙어 있다.",
		Ref:  "세션과 프로젝트가 먼저 등록돼 있어야 한다.",
	},
	store.TargetSignal: {
		Name: "신호",
		Dup:  "같은 종류의 신호가 이미 있다.",
		Ref:  "세션이 먼저 등록돼 있어야 한다 — fd open 으로 열어라.",
	},
	store.TargetFootprint: {
		Name: "발자국",
		Dup:  "같은 경로·출처의 발자국이 이미 있다.",
		Ref:  "세션이 먼저 등록돼 있어야 한다 — fd open 으로 열어라.",
	},
	store.TargetResourceHold: {
		Name: "자원 점유",
		Dup:  "그 자원은 이미 누가 쥐고 있다 — 점유자에게 물어라.",
		Ref:  "프로젝트와 점유자(세션)가 먼저 등록돼 있어야 한다.",
	},
	store.TargetCounter: {
		Name: "카운터",
		Dup:  "같은 이름의 카운터가 이미 있다.",
		Ref:  "프로젝트를 먼저 등록해라 — 세션을 한 번 열면 등록된다(fd open).",
	},
	store.TargetRefState: {
		Name: "ref 관측",
		Dup:  "같은 ref 의 관측이 이미 있다.",
		Ref:  "프로젝트를 먼저 등록해라.",
	},
	store.TargetChangeSet: {
		Name: "변경집합",
		Dup:  "같은 두 커밋 사이의 변경집합이 이미 있다.",
		Ref:  "프로젝트를 먼저 등록해라.",
	},
	store.TargetIdempotency: {
		Name: "멱등 기록",
		Dup:  "같은 키의 기록이 이미 있다.",
		Ref:  "가리키는 좌표가 없다.",
	},
}

// ConflictAdvice 는 제약 위반 하나를 소비자가 읽을 응답으로 옮긴다. 순수 함수다.
//
// ★ 상태코드는 둘 다 409 다. 요청 자체는 잘 만들어졌고 라우트도 있다 — 충돌하는 것은
// **DB 의 현재 상태**이고 그것이 409 의 정의다. 404 로 나누지 않는 이유: 그러면
// "그런 표면이 없다"와 "가리킨 프로젝트가 없다"가 같은 코드로 접혀, 소비자가
// 경로를 의심하러 간다. 대신 code 와 처방으로 두 갈래를 가른다.
//
// ★ 500 이 아닌 것이 이 함수의 존재 이유다. 500 은 ① 조사를 서버 쪽으로 돌리고
// ② 멱등 표에 저장되지 않아 재시도가 계속 하류로 들어간다.
func ConflictAdvice(e *store.ConflictError) Classified {
	if e == nil {
		return Classified{Status: http.StatusOK, Code: "ok", Message: "오류가 없다"}
	}
	w, ok := conflictWordTable[e.Target]
	if !ok {
		// 대상이 늘었는데 문구가 안 붙은 것이다. 조용히 500 으로 접지 않는다 —
		// 그러면 이 결함이 "서버 고장"으로 분류돼 안 고쳐진다.
		w = conflictWords{
			Name: string(e.Target),
			Dup:  "같은 좌표가 이미 있다 — 다른 id 를 쓰거나 이미 있는 것을 써라.",
			Ref:  "가리킨 좌표 중 등록되지 않은 것이 있다 — 먼저 등록해라.",
		}
	}
	// 문구에 조사(을/를·이/가)를 붙이지 않는다 — 대상 이름과 id 가 둘 다 가변이라
	// 어느 조사가 맞는지 조립 시점에 알 수 없고, 틀린 조사는 매 응답에 남는다.
	switch e.Kind {
	case store.ConflictDuplicate:
		return Classified{
			Status:   http.StatusConflict,
			Code:     "duplicate",
			Message:  fmt.Sprintf("%s 중복: %s (이미 등록돼 있다)", w.Name, clip(e.ID, 100)),
			Guidance: w.Dup,
		}
	case store.ConflictMissingRef:
		return Classified{
			Status:  http.StatusConflict,
			Code:    "missing_ref",
			Message: fmt.Sprintf("%s 등록 실패 — 가리키는 좌표(%s) 중 등록되지 않은 것이 있다", w.Name, clip(e.RefHint, 200)),
			// 무엇을 가리켰는지는 RefHint 가 이미 담고 있다. 어느 FK 였는지는 드라이버가
			// 코드로 말해 주지 않으므로 **지어내지 않는다** — 지어내면 그것이 새 거짓이 된다.
			Guidance: w.Ref,
		}
	default:
		// 접기로 판정한 종류가 늘었는데 여기가 안 늘었다. 사실대로 500 이다.
		return Classified{
			Status:   http.StatusInternalServerError,
			Code:     "internal",
			Message:  "서버 내부 오류다. 원인 전문은 서버 로그에 있다",
			Guidance: "이 응답의 request_id 로 로그를 찾아라.",
			Internal: true,
		}
	}
}

// badRequest 는 표면 계층이 스스로 만든 거절이다(디코딩·필수 인자 누락).
// 문구는 전부 이 계층이 쓴 것이라 하류 문자열이 섞이지 않는다.
func badRequest(code, msg, guidance string) Classified {
	return Classified{Status: http.StatusBadRequest, Code: code, Message: msg, Guidance: guidance}
}
