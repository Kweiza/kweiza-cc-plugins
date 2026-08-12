package service

import (
	"fmt"
	"strings"

	"github.com/kweiza/flightdeck/internal/store"
)

// LifecycleGate 는 한 대화(machine + cc_session_id)가 지금 어느 단계에 걸려 있는지다.
// nil 이면 걸린 것이 없다 — Stop 훅(Task 12)이 이 값의 유무로 decision:block 을 낸다.
type LifecycleGate struct {
	Stage  string `json:"stage"` // lane-wait | finish | land
	Reason string `json:"reason"`
}

// judgeLifecycleGate 는 대화 하나의 라이프사이클 단계를 본다. **순수 함수다** — DB 를 안 본다.
// 판정에 쓰는 값은 전부 store.ConversationLifecycle 이 이미 모아 온 관측치다.
//
// ★ 위에서부터 먼저 맞는 것 하나만 낸다 — 셋이 겹치면(줄에 서 있으면서 선점도 있다)
// 가장 급한 것은 줄이다: 차례를 흘리면 뒤 전원이 선다(다른 세션의 랜딩이 그만큼 밀린다).
// 선점을 안 끝낸 것은 그 세션 하나의 사정이지만, 줄은 **공유 자원**이라 한 세션의 실수가
// 여럿에게 번진다 — 그래서 순서가 lane-wait → finish → land 다.
//
// ★ 쥔 자원이 하나라도 있으면 lane-wait 를 안 낸다. 전부 쥔 것은 정당한 대화 중일 수
// 있고(스펙 §4 — 랜딩 중 사람과 상의하며 턴이 여러 번 오가는 것은 정상이다), 일부만 쥔 것은
// all-or-nothing 이 깨진 어긋남이라 block 이 아니라 사람의 회수가 푸는 자리다 — 이 판정이
// "회수하라"를 대신 말하면 그 문구가 실제 회수 절차(ForceReleaseResource, 사유 필수)를
// 우회하는 지름길처럼 읽힌다. 그래서 그 갈래는 조용히 통과시킨다(nil).
//
// ★ **쥠의 판정은 LaneRow 의 자원 집합과 대조하지 않는다 — HeldRes 가 하나라도 있으면 그것으로
// 족하다.** laneTurnRow(prescribe.go)의 같은 판정("남이든 나든 쥔 자원이 하나라도 있으면
// 0")과 일관되게 뒀다. 대조하려면 "그 줄 행이 요구하는 자원 집합 중 몇 개를 쥐었나"를 다시
// 비교해야 하는데, 그 비교가 필요한 값(LaneRow.Resources)과 HeldRes 는 이미 같은 관측에
// 실려 있으므로 원한다면 나중에 더 좁힐 수 있다 — 지금은 "쥔 것이 있다=지금 뭔가 하는 중이다"
// 라는 거친 신호만으로 lane-wait 오탐(사람이 자원을 들고 상의 중인데 "차례를 놓친다"고
// 겁주는 것)을 막는 것이 목적이다.
//
// ★ finish 단계는 LiveClaims 만 본다 — 그 항목이 랜딩 줄과 무관해도 뜬다. 선점을 쥔 채
// 대화를 끝내는 것 자체가 다음 세션의 관측을 막기 때문이다(claim 에 만료가 없다).
//
// ★ land 단계는 outcome='done' 에만 반응한다(store 의 DoneItems 가 이미 done 만 담는다 —
// dropped 로 닫은 항목은 랜딩할 것이 없으므로 자연히 이 게이트를 안 켠다). 관측 구간은
// EarliestOpen 이후이고, 여기서 "머지됐는가"는 안 묻는다 — 그것은 러너의 실제 fast-forward
// 결과(item.landed_ref)로만 알 수 있는 값이라 서버가 이 판정 시점에 잴 수 없다. 이 게이트가
// 묻는 것은 정확히 "줄에 섰나" 하나다: done 을 선언하고 랜딩 줄에 한 번도 안 선 대화는
// (성공이든 실패든) 그 절차 자체를 건너뛴 것이므로 그 사실만으로 충분히 block 할 근거가 된다.
func judgeLifecycleGate(c store.ConvLifecycle) *LifecycleGate {
	if c.LaneRow != nil && len(c.HeldRes) == 0 {
		res := strings.Join(c.LaneRow.Resources, " ")
		return &LifecycleGate{Stage: "lane-wait", Reason: fmt.Sprintf(
			"자원 %s 줄에 서 있다(행 %d). 지금 턴을 끝내면 차례가 와도 못 받는다 — "+
				"`fd lane wait` 로 이어라. 줄에서 빠지려면 land(leave:\"사유\") 다.",
			res, c.LaneRow.ID)}
	}
	if len(c.LiveClaims) > 0 {
		return &LifecycleGate{Stage: "finish", Reason: fmt.Sprintf(
			"선점 중인 항목 %s 가 아직 열려 있다. 끝났으면 finish 로 닫아라(판단·후속·반납이 한 호출이다). "+
				"끝나지 않았으면 이어서 하라 — 이 알림은 턴 끝마다 온다.",
			strings.Join(c.LiveClaims, " "))}
	}
	if len(c.DoneItems) > 0 && !c.EverEnqueued {
		return &LifecycleGate{Stage: "land", Reason: fmt.Sprintf(
			"%s 를 done 으로 닫았는데 이 대화는 랜딩 줄에 선 기록이 없다. "+
				"land 로 줄을 서고 차례에 랜딩해라 — 기다림은 `fd lane wait` 가 잇는다.",
			strings.Join(c.DoneItems, " "))}
	}
	return nil
}
