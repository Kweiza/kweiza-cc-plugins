package service

import (
	"context"
	"strings"
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

// logFail 은 실패한 시도의 **사유**를 원장에 덧붙인다.
//
// 시도 자체는 트랜잭션 안에서 Tx.LogEvent 로 먼저 예약되므로 롤백돼도 남는다.
// 다만 그 시점에는 결과를 모르므로 "왜 실패했나"를 여기서 따로 남긴다 —
// 원장에 시도만 있고 사유가 없으면 실패율은 세지되 무엇을 고쳐야 하는지는 답하지 못한다.
func (s *Service) logFail(ctx context.Context, kind, project, sessionID string, err error, about failAbout) {
	if err == nil {
		return
	}
	s.st.LogEvent(ctx, kind+".fail", project, sessionID,
		aboutPayload(map[string]any{"error": clip(err.Error(), 400)}, about))
}
