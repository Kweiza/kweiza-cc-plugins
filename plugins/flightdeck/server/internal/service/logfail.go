package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kweiza/flightdeck/internal/store"
)

// 실패한 시도의 원장 — **시도만 남기면 실패율은 세지되 무엇을 고칠지는 답하지 못한다.**
//
// ★ 이 파일이 service.go 에서 갈라진 이유는 나중에 늘 import 하나다(finish_followups.go 가
// 갈린 것과 같은 자리·같은 이유). 갈래 판정이 errors 를 물고 들어오는데 service.go 의
// import 블록은 이 물결의 다른 항목들이 함께 만지고 있다 — 한 줄 때문에 남의 훅과
// 부딪히느니 파일을 가른다.

// failAbout 은 실패한 시도가 **무엇을 대상으로** 했나다.
//
// ★ 왜 인자로 받나. 이 좌표는 호출부에만 있다 — 오류 객체는 "무엇이 없었나"는 알아도
// "이 시도가 무엇을 겨눴나"는 모른다. 앞 판은 이 자리를 아예 안 받았고, 그래서 원장의
// item.finish.fail 은 자기가 어느 항목의 것인지 못 말했다. 바로 아래 줄의
// s.log.ErrorContext 는 그 값을 이미 찍고 있었다 — 로그에는 있고 원장에는 없었다.
//
// ★ **가변 인자로 안 둔 이유.** 그러면 "실을 좌표가 없다"와 "좌표를 안 실었다"가 호출부에서
// 같은 글자가 된다 — 이 저장소가 반복해서 갈라 온 접힘(0과 못 잼)이다. 필수로 두면
// 호출부를 하나 늘릴 때 컴파일러가 그 질문을 대신 던진다. 교차 빌드 관문이 go vet 이라
// 시험 코드까지 컴파일되므로 누락은 관문에서 즉시 죽는다.
type failAbout struct {
	Item string // 이 시도가 겨눈 항목 id
	Mode string // 이 시도가 하려던 것(done|dropped|handoff|paused …)
}

// aboutPayload 는 좌표를 payload 에 올린다. **빈 축은 키 자체를 안 만든다.**
//
// 빈 문자열을 실으면 "좌표가 없다"와 "좌표가 빈 문자열이다"가 같은 값이 되고, 이 축의
// 소비자 규율은 eventItemID 와 같다 — 비면 안 센다. 값이 있는데 못 세는 것과 값이 없는데
// 세는 것 중 후자가 훨씬 나쁘다(원장은 추가 전용이라 되돌릴 수 없다).
func aboutPayload(base map[string]any, about failAbout) map[string]any {
	if item := strings.TrimSpace(about.Item); item != "" {
		base["item"] = clip(item, 100)
	}
	if mode := strings.TrimSpace(about.Mode); mode != "" {
		base["mode"] = clip(mode, 32)
	}
	return base
}

// finishAbout 은 마무리 한 번의 좌표다.
//
// 실패와 거절 두 자리가 **같은 값**을 써야 원장에서 그 둘을 같은 항목으로 이을 수 있다.
// 두 자리가 각자 조립하면 한쪽만 고치는 개정이 조용히 좌표계를 가른다.
func finishAbout(in FinishInput) failAbout {
	return failAbout{Item: in.ItemID, Mode: string(in.Outcome)}
}

// FailCause 는 실패 사유의 갈래다. **열거다.**
//
// 자유 문자열로 두면 같은 사유가 자리마다 다른 값으로 쌓이고, event.payload 는 추가
// 전용에 스키마가 없어 나중에 고칠 수 없다 — 그러면 이 축은 세지도 못하는 산문이 된다.
// 갈래를 고르는 것도 호출부가 아니라 여기다. 13개 호출부가 각자 고르면 정의가 흩어지고,
// 흩어진 정의는 반드시 표류한다.
type FailCause string

const (
	// CauseFollowupWrite 는 **후속 등록 단계**에서 끊긴 것이다. 고칠 자리는 followups 인자다.
	CauseFollowupWrite FailCause = "followup-write"
	// CauseClaimDrift 는 선점이 이 세션의 것이 아니라 끊긴 것이다. 고칠 자리는 그 세션에
	// 물어보는 것이고, 회수는 사람만 한다.
	CauseClaimDrift FailCause = "claim-drift"
	// CauseItemMissing 은 **항목**이 없어 끊긴 것이다. 고칠 자리는 item_id 다.
	CauseItemMissing FailCause = "item-missing"
	// CauseNotFound 는 항목 아닌 무엇이 없어 끊긴 것이다(세션·자원·줄 행 …).
	// item-missing 과 가르는 이유는 처방이 다르기 때문이다 — 그리고 안 가르면 세션이
	// 없어서 난 실패가 원장에 "항목이 없다"로 영구히 남는다.
	CauseNotFound FailCause = "not-found"
	// CauseOther 는 위 어디에도 안 걸린 것이다. **"원인 없음"이 아니라 "분류 안 됨"이다** —
	// 이 값이 늘면 갈래를 늘릴 자리가 있다는 뜻이고, 그 판정은 여기가 아니라 원장이 낸다.
	CauseOther FailCause = "other"
)

// followupWriteError 는 **후속 등록 단계에서** 난 실패다. 나르는 것은 오류가 아니라 단계다.
//
// ★ 왜 타입이 필요한가. 앞 판은 이 자리를 fmt.Errorf 로만 감쌌고, 그래서 갈래 판정이
// leaf 오류의 타입밖에 못 봤다 — 같은 없음이 "끝내려는 항목이 없다"인지 "후속 인자가
// 틀렸다"인지 구분되지 않는다. 고칠 자리가 정반대인 둘이다. store 의 NotFoundError 가
// "타입이 도메인 필드를 들고, 문구는 소비 계층이 조립한다"로 연 길과 같은 길이다.
//
// ★ 문구는 앞 판과 **글자 그대로 같다**(%w 는 출력에서 %v 다). 그 문구를 단정하는 시험이
// 있고(finish_test.go 의 "후속 항목 bad-after 등록 실패"), 이 개정은 문구가 아니라 타입을 바꾼다.
//
// ★ Unwrap 이 있으므로 표면의 ClassifyError(internal/api/errors.go)는 그대로 산다 —
// 그쪽은 errors.As 로 store 타입을 집고, As 는 이 껍데기를 지나간다. 후속 등록에서 난
// 중복·FK 위반이 계속 409 로 나가야 하고, 500 으로 접히면 멱등 표에 안 남아 재시도가
// 계속 하류로 들어간다.
type followupWriteError struct {
	ID  string
	Err error
}

func (e *followupWriteError) Error() string {
	return fmt.Sprintf("후속 항목 %s 등록 실패: %v", clip(e.ID, 64), e.Err)
}

func (e *followupWriteError) Unwrap() error { return e.Err }

// failCause 는 오류 하나를 갈래로 옮긴다. 순수 함수다.
//
// ★ **순서가 판정이다.**
// ① 후속 등록 단계를 가장 먼저 본다. 그 안에 어떤 leaf 오류가 들어 있든 고칠 자리는
// 후속 인자이고, 뒤로 미루면 후속 id 의 없음이 "끝내려는 항목이 없다"로 굳는다.
// ② item-missing 은 NotFoundError 의 Kind 가 NFItem 일 때만이다. 세션·자원·줄 행의
// 없음까지 item-missing 으로 적으면 이 이벤트가 관측하지 않은 원인을 단정한다 —
// store/event.go 가 mode 를 모르는 종료 선언을 한쪽에 안 모는 것과 같은 규율이다.
// ③ 그 밖의 없음은 not-found 로 남긴다. other 로 접으면 "무엇이 없었다"는 관측이
// "분류 안 됨"으로 버려진다.
func failCause(err error) FailCause {
	var write *followupWriteError
	var held *store.ClaimHeldError
	var missing *store.NotFoundError
	switch {
	case err == nil:
		return ""
	case errors.As(err, &write):
		return CauseFollowupWrite
	case errors.As(err, &held):
		return CauseClaimDrift
	case errors.As(err, &missing) && missing.Kind == store.NFItem:
		return CauseItemMissing
	case errors.Is(err, store.ErrNotFound):
		return CauseNotFound
	default:
		return CauseOther
	}
}

// logFail 은 실패한 시도의 **사유**를 원장에 덧붙인다.
//
// 시도 자체는 트랜잭션 안에서 Tx.LogEvent 로 먼저 예약되므로 롤백돼도 남는다.
// 다만 그 시점에는 결과를 모르므로 "왜 실패했나"를 여기서 따로 남긴다 —
// 원장에 시도만 있고 사유가 없으면 실패율은 세지되 무엇을 고쳐야 하는지는 답하지 못한다.
func (s *Service) logFail(ctx context.Context, kind, project, sessionID string, err error, about failAbout) {
	if err == nil {
		return
	}
	s.st.LogEvent(ctx, kind+".fail", project, sessionID, aboutPayload(map[string]any{
		"error": clip(err.Error(), 400),
		"cause": string(failCause(err)),
	}, about))
}

// FinishGate 는 **트랜잭션 진입 전에** 마무리를 끊은 관문의 이름이다. 열거다.
//
// 값 하나가 return 자리 하나에 1:1 로 붙는다. 이름을 안 실으면 거절이 한 덩어리가 되어
// "어느 문이 실제로 무나"에 답하지 못하고, 그 질문에 못 답하면 관문을 늘릴지 풀지를
// 사람의 인상으로 정하게 된다.
type FinishGate string

const (
	GateJudge              FinishGate = "judge"               // item_id·outcome·body·close_reason
	GateFollowupsPending   FinishGate = "followups-pending"   // 바닥에 떨어뜨린 후속이 있다
	GateFollowupID         FinishGate = "followup-id"         // 후속 id 가 브랜치 이름 규칙 밖이다
	GateFollowupDuplicate  FinishGate = "followup-duplicate"  // 같은 후속 id 가 한 호출에 두 번
	GateFollowupIneligible FinishGate = "followup-ineligible" // 이을 자격이 없거나 존재를 못 읽었다
	GateFollowupBody       FinishGate = "followup-body"       // 새로 만들 후속에 제목·본문이 없다
	GateFollowupPaths      FinishGate = "followup-paths"      // 후속 경로가 좌표계 밖이다
	GateDroppedDeps        FinishGate = "dropped-deps"        // 이 항목을 기다리는 살아 있는 항목이 있다
	// GateFollowupAfter·GateJudgmentLinkKind 는 **tx 안에서 죽던 마지막 둘**을 전단으로
	// 옮긴 자리다(2026-08-11). 저 안에서 오류가 되면 판단이 함께 롤백돼 파생 불가한
	// 자산이 사라진다 — finish_preflight_after_and_links_test.go 가 그 둘을 잠근다.
	GateFollowupAfter    FinishGate = "followup-after"     // 후속의 선행 형식이 틀렸다(축이 0개 또는 둘 이상)
	GateJudgmentLinkKind FinishGate = "judgment-link-kind" // 판단 링크의 target_kind 가 열거 밖이다
)

// logFinishRefused 는 **트랜잭션에 들어가기도 전에** 끊긴 시도를 원장에 남긴다.
//
// ★ 왜 필요한가. 이 거절들은 지금 WARN 로그로만 나가고 원장에는 자국이 0이다. 그래서
// "몇 번 시도해서 몇 번 끊겼나"의 **분모가 원리적으로 없다** — 관문의 효과를 사후에 재는
// 방법이 사람의 신고뿐이 된다. 이 저장소가 후속 관문을 세우며 실측으로 기댄 것이 정확히
// 그 종류의 수치였다.
//
// ★ kind 를 `item.finish` 와 **가른다. 이 분리가 안전 축의 전부다.** 그 kind 는 표류
// 탐지(store.CloseDeclarationsByItem)와 재생산율(store.QueueReproduction)의 재료이고,
// 둘 다 `kind = 'item.finish'` 로 정확히 거른다(store/event.go). 거절을 그 kind 로 남기면
// 쓰지도 않은 종료 선언이 두 축에 들어가 멀쩡한 항목이 "롤백된 종료 선언"으로 강등된다.
// 접미가 붙은 kind 는 그 두 질의에 원리적으로 안 걸린다 — 시험이 그 사실을 단정한다.
//
// ★ 좌표는 finishAbout 로 조립한다. 실패(.fail)와 거절(.refused)이 같은 값을 써야 원장에서
// 그 둘을 같은 항목으로 이을 수 있다. outcome 이 열거 밖 값이어도 그대로 싣는다 —
// **호출자가 보낸 것**이고, 지어낸 값보다 받은 값이 조사에 쓸모 있다(clip 이 길이를 문다).
func (s *Service) logFinishRefused(ctx context.Context, in FinishInput, gate FinishGate) {
	s.st.LogEvent(ctx, "item.finish.refused", in.Project, in.SessionID,
		aboutPayload(map[string]any{"gate": string(gate)}, finishAbout(in)))
}
