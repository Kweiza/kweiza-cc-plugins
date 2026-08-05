package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 후속 관문 — `finish` 가 후속을 바닥에 떨어뜨리는 것을 한 번은 붙잡는다.
//
// ★ 이 파일이 finish.go 와 따로 있는 이유는 **import 하나** 때문이다. 여기는
// encoding/json 이 필요한데(이벤트 payload), finish.go 의 import 블록은 지금
// fd-landing-order-queue 가 미랜딩으로 쥐고 있다. 한 줄 때문에 남의 훅과 부딪히느니
// 파일을 가른다 — finish.go 에는 호출 세 줄만 남는다.

// followupsMissingEvent 는 이 관문이 **한 번 발화했다**는 사실이다.
//
// 이 이벤트가 관문을 "벽"이 아니라 "관문"으로 만든다 — 아래 judgeMissingFollowups 를 보라.
const followupsMissingEvent = "item.finish_followups_missing"

// FollowupsGuidance 는 후속을 안 실은 세션에게 그 자리에서 내는 문구다.
//
// HandoffGuidance 와 같은 자리에서 같은 이유로 나간다: 규율 산문을 도구 설명이나
// 스킬에 넣지 않고 **필요할 때 그 자리에서** 응답에 싣는다(설계 §6, 컨텍스트 예산).
const FollowupsGuidance = `후속은 followups 로 **같은 호출에** 넣어라 — 그래야 판단과 후속이 FK 로 이어지고,
다음 세션의 pick 이 그 항목과 함께 "왜 이것이 생겼나"를 낸다.

★ 위에 이름이 나온 항목들은 **이미 add 로 만들어져 있어서 지금 followups 로 옮길 수 없다**
(같은 id 를 다시 만들게 된다). 그 연결은 이번 마무리에서는 못 산다 —
대신 note(kind='handoff', item_id=<그 항목>) 로 판단을 그 항목에 직접 걸어 두면
pick 이 연결된 판단으로 낸다. 다음부터는 add 를 미루고 followups 에 실어라.

이 관문은 **한 번만** 막는다. 그 항목들이 이 작업의 후속이 아니라면 그대로 다시 불러라.`

// judgeMissingFollowups 는 후속을 바닥에 떨어뜨리는 finish 를 **한 번** 막는다.
//
// ★★ **한 번만 막는 것이 이 설계의 전부다.**
//
// 영영 막으면 관문이 아니라 벽이다 — 세션이 작업 중에 딴 축을 발견해 항목을 올리는 것은
// 정상이고, 그것이 이번 작업의 후속이 **아닐** 수 있다. 그때 finish 가 안 되면 세션은
// 항목을 지우거나 거짓 후속을 만들어 빠져나간다. 둘 다 원장을 더럽힌다.
//
// 반대로 안 막으면 지금과 같다. 지금도 응답 꼬리에 "후속 0건" 이 나가는데, 그 줄은
// **성공 응답에 섞여** 나가서 안 읽힌다. 이 항목 자체가 그 실패의 산물이다 —
// 그 줄을 보고도 넘어간 세션이 이 관문을 만들고 있다.
//
// 그래서 한 번은 멈춰 세워 목록을 보게 하고, 두 번째는 통과시킨다. 사람이 "아니다"라고
// 판단할 자유를 남기되, 그 판단을 **의식적으로** 하게 만든다.
//
// 관측을 못 하면 막지 않는다(fail-open). 선점 시각을 못 읽거나 이벤트 조회가 실패하면
// 그냥 통과다 — 계측 하나가 실패했다고 마무리를 잃는 것이 훨씬 나쁘고, 그 판정은
// 이 파일이 아니라 원장이 내려야 한다.
func (s *Service) judgeMissingFollowups(ctx context.Context, in FinishInput) *RefusedError {
	open := s.followupCandidates(ctx, in)
	if len(open) == 0 {
		return nil
	}
	if s.alreadyWarnedFollowups(ctx, in) {
		return nil // 이미 한 번 말했다. 두 번째는 사람의 판단이다
	}
	s.st.LogEvent(ctx, followupsMissingEvent, in.Project, in.SessionID,
		map[string]any{"item": clip(in.ItemID, 100), "pending": len(open)})

	return &RefusedError{
		What: "finish",
		Reason: fmt.Sprintf(
			"후속 없이 마무리하려는데, 이 세션이 %s 를 선점한 뒤 만든 항목 %d건이 아직 열려 있다: %s",
			clip(in.ItemID, 64), len(open), strings.Join(open, ", ")),
		Guidance: FollowupsGuidance,
	}
}

// followupCandidates 는 "이 세션이 이 선점 뒤에 만들었고, 아직 열려 있고,
// 이번 followups 에 없는" 항목 id 들이다.
//
// ★ **닫힌 것은 세지 않는다.** 실측 사례가 있다: 한 세션이 만든
// `fd-footprint-has-no-containment-gate` 를 남이 집어 랜딩까지 해서, 그 세션이 마무리할
// 때는 이미 닫혀 있었다. 그것까지 세면 거짓 거절이 된다 — 남이 끝내 준 일을 두고
// "후속으로 넣어라"라고 하는 꼴이다.
//
// ★ **선점 뒤로 자른다.** 오래 사는 세션은 앞선 작업에서 만든 항목을 갖고 있다.
// 그것까지 세면 항목 하나를 끝낼 때마다 과거 전부가 딸려 온다.
func (s *Service) followupCandidates(ctx context.Context, in FinishInput) []string {
	claim, err := s.st.GetClaim(ctx, in.Project, in.ItemID)
	if err != nil || claim.At.IsZero() {
		return nil // 언제부터 쥐었는지 모르면 자를 지점이 없다 — 안 막는다
	}
	evs, err := s.st.ListSessionEvents(ctx, in.SessionID, "item.add", claim.At)
	if err != nil {
		return nil
	}

	given := make(map[string]bool, len(in.Followups))
	for _, f := range in.Followups {
		given[f.ID] = true
	}

	seen := map[string]bool{}
	var out []string
	for _, e := range evs {
		id := eventItemID(e)
		if id == "" || id == in.ItemID || given[id] || seen[id] {
			continue
		}
		seen[id] = true
		it, err := s.st.GetItem(ctx, in.Project, id)
		if err != nil || it.State != model.ItemOpen {
			continue // 못 읽었거나 이미 닫혔다
		}
		out = append(out, id)
	}
	return out
}

// alreadyWarnedFollowups 는 이 (세션·항목) 조합에 이 관문이 이미 발화했는지다.
//
// 항목 id 까지 보는 이유: 한 세션이 항목 여럿을 마무리한다(실측: 한 세션이 두 건을
// 연달아 닫았다). 세션만 보면 첫 항목에서 발화한 뒤 둘째 항목은 조용히 지나간다.
func (s *Service) alreadyWarnedFollowups(ctx context.Context, in FinishInput) bool {
	evs, err := s.st.ListSessionEvents(ctx, in.SessionID, followupsMissingEvent, time.Time{})
	if err != nil {
		return true // 못 읽었으면 막지 않는다 — 관문의 실패가 마무리를 막으면 안 된다
	}
	for _, e := range evs {
		if eventItemID(e) == in.ItemID {
			return true
		}
	}
	return false
}

// eventItemID 는 이벤트 payload 의 "item" 값이다. 못 읽으면 빈 문자열이다.
//
// payload 는 자유 JSON 이라 스키마가 없다 — 실패를 오류로 올리지 않고 빈 값으로 접는다.
// 이 축의 소비자는 전부 "비면 안 센다" 로 동작한다.
func eventItemID(e model.Event) string {
	var p struct {
		Item string `json:"item"`
	}
	if json.Unmarshal([]byte(e.Payload), &p) != nil {
		return ""
	}
	return strings.TrimSpace(p.Item)
}
