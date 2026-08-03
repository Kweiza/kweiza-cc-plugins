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

// badRequest 는 표면 계층이 스스로 만든 거절이다(디코딩·필수 인자 누락).
// 문구는 전부 이 계층이 쓴 것이라 하류 문자열이 섞이지 않는다.
func badRequest(code, msg, guidance string) Classified {
	return Classified{Status: http.StatusBadRequest, Code: code, Message: msg, Guidance: guidance}
}
