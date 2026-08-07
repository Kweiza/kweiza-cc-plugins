package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
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
const FollowupsGuidance = `후속은 followups 로 **같은 호출에** 넣어라 — 그래야 판단과 후속이 판단 링크로 이어지고,
다음 세션의 pick 이 그 항목과 함께 "왜 이것이 생겼나"를 낸다.

★ 위에 이름이 나온 항목들은 **이미 만들어져 있으니 followups 에 id 만 넣어라** — 새로 만들지
않고 이 판단에 잇는다. 제목·본문은 다시 안 적는다(그 항목의 것을 안 덮는다).
이을 수 있는 것은 **이 선점 뒤에 이 세션이 add 로 만든, 아직 열린 항목**뿐이다 —
그 밖의 id 를 실으면 거절한다(오타로 남의 항목이 이 판단에 붙는 것을 막는 유일한 자리다).
이번 작업의 후속이 아닌 항목은 그대로 두고, 판단만 걸려면 note(kind='handoff', item_id=…) 를 쓴다.

이 관문은 **한 번만** 막는다. 그 항목들이 이 작업의 후속이 아니라면 그대로 다시 불러라.`

// TriageGuidance 는 후속 규율의 **유일한 거르는 기준**이다.
//
// 실측(2026-08-07 조사): 이 규율에는 항목화를 미는 문장 10곳 + 강제 기구 2개가 있는데
// 거르는 기준이 0곳이었다 — 세션이 규율에 순응할수록 R>1 이 나오는 미시 기전이다.
// 기준을 "선언 경로 안인가"로 잡지 않는다: 백테스트(버스트 후속 13건, 눈가림)에서 세션들은
// 경로 밖도 즉석에서 소화했고, do-now 를 맞힌 예측자는 경로 소속이 아니라 **본문 완성도**였다.
// 이것은 관문이 아니라 기준이다 — 거르는 판단은 세션이 한다(finish_balance.go 의 원칙 그대로).
const TriageGuidance = `싣기 전에 하나만 걸러라 — **본문이 곧 패치인 항목은 후속이 아니라 지금 할 일이다.**
고칠 문장·해법이 본문에 이미 완성돼 있으면 등록하지 말고 지금 하고, finish 를 그만큼 미뤄라.
선언 경로 밖이면 note(kind='decision') 로 범위가 왜 늘었는지 남기면 된다.
후속은 "이 세션이 지금 못 하는 이유"가 있는 것만이다 — 별도 검증 축 · 미래 기한 · 전제 미확정.
(기존 항목을 **잇는** 것은 거를 것이 없다 — 큐를 안 늘린다.)`

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
		// 트리아지를 같은 표면에 싣는다 — 거절-시점 안내는 이 저장소에서 준수가 실측된
		// 유일한 표면 부류다(관측 전 사례 준수 vs 성공 응답 산문 8%). 문구 둘이 한
		// 방향("실어라")과 기준("이건 싣지 마라")을 함께 내야 관문이 증식 엔진이 안 된다 —
		// 이 관문의 전신이 add 를 followups 로 전환만 시키고 총유입을 못 줄인 실측이 있다.
		Guidance: FollowupsGuidance + "\n\n" + TriageGuidance,
	}
}

// sessionSpawnedOpen 은 "이 세션이 이 선점 뒤에 만들었고 아직 열려 있는" 항목 id 들과,
// **그 판정을 실제로 관측했는지**다.
//
// ★ **닫힌 것은 세지 않는다.** 실측 사례가 있다: 한 세션이 만든
// `fd-footprint-has-no-containment-gate` 를 남이 집어 랜딩까지 해서, 그 세션이 마무리할
// 때는 이미 닫혀 있었다. 그것까지 세면 거짓 거절이 된다.
//
// ★ **선점 뒤로 자른다.** 오래 사는 세션은 앞선 작업에서 만든 항목을 갖고 있다.
// 그것까지 세면 항목 하나를 끝낼 때마다 과거 전부가 딸려 온다.
//
// ★ **관측 여부를 값으로 나르는 이유.** 소비자 둘이 같은 빈 목록에 정반대로 반응해야 한다 —
// 관문(judgeMissingFollowups)은 못 읽었으면 **안 막고**(마무리를 잃지 않는 쪽), 잇기
// (classifyFollowups)는 못 읽었으면 **안 잇는다**(거짓 링크를 안 만드는 쪽). 빈 슬라이스
// 하나로 접으면 그 둘이 같은 값이 되어, 다음 개정이 한쪽을 고치며 다른 쪽을 조용히 뒤집는다.
// FinishResult 의 StillHeld·QueueBalance 를 포인터로 둔 것과 같은 규율이다(finish.go:78·97).
//
// ★ **item.add 이벤트로만 판정한다.** 그 이벤트를 남기는 자리는 Service.AddItem(pick.go:1164)
// 하나뿐이라, **finish 의 후속으로 만들어진 항목은 여기 안 걸린다.** 그것까지 세려면 finish 도
// item.add 를 남겨야 하는데, 그러면 관문의 사정거리가 함께 넓어져 거짓 거절이 는다 —
// 별개 축이라 후속 항목으로 낸다. 거절 문구가 이 부류를 갈라 말한다.
func (s *Service) sessionSpawnedOpen(ctx context.Context, in FinishInput) ([]string, bool) {
	claim, err := s.st.GetClaim(ctx, in.Project, in.ItemID)
	if err != nil || claim.At.IsZero() {
		return nil, false // 언제부터 쥐었는지 모르면 자를 지점이 없다
	}
	evs, err := s.st.ListSessionEvents(ctx, in.SessionID, "item.add", claim.At)
	if err != nil {
		return nil, false
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range evs {
		id := eventItemID(e)
		if id == "" || id == in.ItemID || seen[id] {
			continue
		}
		seen[id] = true
		it, err := s.st.GetItem(ctx, in.Project, id)
		if err != nil || it.State != model.ItemOpen {
			continue // 못 읽었거나 이미 닫혔다
		}
		out = append(out, id)
	}
	return out, true
}

// followupCandidates 는 위에서 **이번 followups 에 실린 것을 뺀** 목록이다.
// 관문이 "바닥에 떨어뜨린 후속"이라고 부르는 것이 이것이다.
//
// 관측을 못 하면 빈 목록이다 — 관문은 그때 **안 막는다**(fail-open). 계측 하나가
// 실패했다고 마무리를 잃는 것이 훨씬 나쁘고, 그 판정은 이 파일이 아니라 원장이 내려야 한다.
func (s *Service) followupCandidates(ctx context.Context, in FinishInput) []string {
	ids, observed := s.sessionSpawnedOpen(ctx, in)
	if !observed {
		return nil
	}
	given := make(map[string]bool, len(in.Followups))
	for _, f := range in.Followups {
		given[f.ID] = true
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if given[id] {
			continue
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

// dedupeLinks 는 판단 링크에서 같은 (종류·대상)을 처음 것만 남긴다.
//
// ★ 이것이 없으면 **판단이 사라진다.** judgment_link 의 PK 는
// (judgment_id, target_kind, target_id)(schema.sql:271) 이고 AddJudgment 는 평범한
// INSERT 다(store/judgment.go:59). finish 는 in.ItemID · in.Links · 후속 id 를 이어 붙이므로
// 셋 중 무엇이든 겹치면 ① 이 ConflictDuplicate 를 내고 Store.Tx 가 ①②③④ 를 통째로
// 롤백한다 — 넷 중 판단만이 원리적으로 파생 불가하다.
//
// ★ **잠금으로는 못 닫는 창이다.** _txlock=immediate(store/store.go:211)가 배제하는 것은
// 다른 커넥션이고, 이 겹침은 한 호출이 자기와 부딪히는 것이다.
//
// 저장층에 OR IGNORE 를 넣지 않는 이유: 그러면 "링크가 겹쳤다"가 어느 호출에서도 안 보이게
// 되고, 판단 링크를 조립하는 책임이 있는 이 계층이 자기 실수를 못 배운다.
func dedupeLinks(links []model.JudgmentLink) []model.JudgmentLink {
	seen := make(map[model.JudgmentLink]bool, len(links))
	out := make([]model.JudgmentLink, 0, len(links))
	for _, l := range links {
		if seen[l] {
			continue
		}
		seen[l] = true
		out = append(out, l)
	}
	return out
}

// followupCreate 는 "새로 만들 후속"이다. Index 는 **요청에서 몇 번째였나**(1-based) —
// 분류로 순서가 갈려도 오류 문구가 요청 좌표를 잃지 않게 나른다.
type followupCreate struct {
	Index int
	Item  FollowupInput
}

// followupPlan 은 이번 마무리의 후속을 만들 것과 이을 것으로 가른 결과다.
//
// ★ Eligible 은 **이 호출에서 이을 수 있었던 것 전부**다. 거절 문구가 이름을 내는 데 쓴다 —
// id 만 실었는데 그 id 의 항목이 아예 없으면(오타) 분류는 '만들기'로 떨어지고, 그때
// "이으려던 것이 없다"는 진짜 사유를 낼 자리가 finish.go 의 ③ 밖에 없다.
type followupPlan struct {
	Create   []followupCreate
	Link     []string
	Eligible []string
}

// classifyFollowups 는 후속마다 만들기·잇기·거절 중 하나를 고른다.
//
// ★ **여기가 거절할 수 있는 마지막 자리다.** 트랜잭션 안에서 거절하면 Store.Tx 가 ①②③④ 를
// 통째로 롤백해 판단이 함께 죽는다. 그래서 자격 판정은 tx **밖**에 있고, tx 안의 같은 판정은
// 분기에만 쓴다(finish.go 의 tx 절을 보라).
//
// ★ 거절이 안전한 이유. 이 자리는 트랜잭션 전이라 **아무것도 안 쓴다.** 판단 본문은 아직
// 세션 손에 있으므로 그 후속만 빼고 다시 부르면 된다 — title·body 누락 거절(finish.go:204)과
// 같은 자리·같은 성격이다.
func (s *Service) classifyFollowups(ctx context.Context, in FinishInput) (followupPlan, *RefusedError) {
	var plan followupPlan
	if len(in.Followups) == 0 {
		return plan, nil
	}
	eligible, observed := s.sessionSpawnedOpen(ctx, in)
	plan.Eligible = eligible // 거절 문구가 이름을 내는 데 쓴다 — 위 followupPlan 주석
	canLink := make(map[string]bool, len(eligible))
	for _, id := range eligible {
		canLink[id] = true
	}
	for i, f := range in.Followups {
		if canLink[f.ID] {
			plan.Link = append(plan.Link, f.ID)
			continue
		}
		exists, itemObserved := s.itemExists(ctx, in.Project, f.ID)
		switch {
		case !itemObserved:
			// ★ **fail-closed 다 — 아래 있음/없음 갈래와 반대 방향.** sessionSpawnedOpen 의
			// observed 와 같은 이유다: 여기서 "없다"로 접으면 그 후속은 '만들기'로 간다.
			// 판단 링크는 이제 **트랜잭션 안에서 확정한 것으로만** 짜이므로(finish.go 의 tx 절)
			// 남의 항목에 링크가 걸리지는 않는다 — 그 자리의 선검사가 잡아 건너뛴다. 그래도
			// 접으면 안 되는 이유는, 그 건너뜀이 **경합을 위한 최후 방어**라 원장에 "분류 뒤
			// 같은 id 가 생겼다"고 적기 때문이다. 못 읽었을 뿐인 것이 경합으로 굳고, event 는
			// 추가 전용이라 그 거짓이 영구히 남는다. 반대로 여기서 거절하면 트랜잭션 전이라
			// 아무것도 안 쓴다 — title·body 누락 거절과 같은 자리·같은 성격이고, 그 후속만
			// 빼면 그대로 되부를 수 있다. 비대칭이 fail-closed 를 요구한다.
			return followupPlan{}, refuseUnreadableFollowupExistence(i+1, f.ID)
		case exists:
			return followupPlan{}, refuseIneligibleFollowup(i+1, f.ID, in.ItemID, eligible, observed)
		default:
			plan.Create = append(plan.Create, followupCreate{Index: i + 1, Item: f})
		}
	}
	return plan, nil
}

// itemExists 는 그 id 의 항목이 지금 있는지와, 그 판정을 실제로 관측했는지다.
//
// ★ **개정 — 원래 이 함수는 모든 조회 실패를 "없다"로 접었다.** 그러면 그 id 는 '만들기'로
// 분류되는데, 그 판(판단 링크가 in.Followups 전수로 짜이던 판)에서는 조회가 DB 오류 등으로
// 실패했을 뿐인 **남의 항목에 링크가 그대로 걸렸다**(리뷰 실측: 링크 1건). 같은 파일의
// sessionSpawnedOpen 은 관측 실패를 observed=false 로 나르는데 이 함수만 반대 방향으로
// fail-open 이었던 것이 그 안전 축을 되열었다.
//
// ★ **링크 조립이 바뀐 지금도 fail-closed 다 — 근거가 옮겨갔을 뿐이다.** 링크는 이제
// 트랜잭션 안에서 확정한 것으로만 짜여(finish.go 의 tx 절) 잘못된 링크는 안 걸린다. 대신
// 남는 것이 원장이다: 못 읽은 것을 '만들기'로 보내면 tx 안 선검사가 그 항목을 건너뛰며
// **"분류 뒤 같은 id 가 생겼다"**고 적는다. 원인이 경합이 아니라 조회 실패인데 event 는
// 추가 전용이라 그 거짓을 나중에 되짚을 수 없다. 거절은 트랜잭션 전이라 아무것도 안 쓴다.
//
// ★ **store.ErrNotFound 만 "없다"로 접는다.** 그 밖의 오류(DB 접속 실패 등)는 "있는지 모른다"
// 로 나르고, classifyFollowups 가 그것을 거절로 접는다 — 정본 판정이 트랜잭션 안의 INSERT 가
// 내는 *store.ConflictError 라는 것(store 패키지 머리의 "제약을 미리 흉내 내 판정하지 않는다"
// 규율)은 안 바뀐다. 여기서 가르는 것은 그 판정 이전에, **거절할지 만들러 보낼지를 고르는
// 참고값 자체를 못 읽었을 때** 어느 쪽으로 접느냐다.
func (s *Service) itemExists(ctx context.Context, project, id string) (exists, observed bool) {
	_, err := s.st.GetItem(ctx, project, id)
	switch {
	case err == nil:
		return true, true
	case errors.Is(err, store.ErrNotFound):
		return false, true
	default:
		return false, false
	}
}

// refuseIneligibleFollowup 은 "이미 있는데 이을 자격이 없는" 후속을 거절한다.
//
// ★ **사유를 셋으로 가른다.** 관측을 못 한 것과 남의 항목인 것을 같은 문구로 접으면 세션이
// 없는 사고를 쫓는다. 그리고 이을 수 있는 것의 **이름을 전부** 낸다 — 수만 말하면 무엇을
// 실을지 다시 조사해야 한다(관문 judgeMissingFollowups 가 같은 규율을 쓴다).
func refuseIneligibleFollowup(nth int, id, itemID string, eligible []string, observed bool) *RefusedError {
	var why string
	switch {
	case !observed:
		why = fmt.Sprintf("%s 를 언제 선점했는지 원장에서 못 읽어 자격을 판정할 수 없다", clip(itemID, 64))
	case len(eligible) == 0:
		why = "이 세션이 이 선점 뒤에 add 로 만든 열린 항목이 하나도 없다"
	default:
		why = fmt.Sprintf("이을 수 있는 것은 %s 뿐이다", strings.Join(eligible, ", "))
	}
	return &RefusedError{
		What: "finish",
		Reason: fmt.Sprintf("%d번째 후속(%s)은 이미 있는 항목인데 이을 자격이 없다 — %s",
			nth, clip(id, 64), why),
		Guidance: `이을 수 있는 것은 **이 세션이 이 선점 뒤에 add 로 만든, 아직 열린 항목**뿐이다.
남의 항목 · 이미 닫힌 항목 · 선점 전에 만든 항목은 못 잇는다 — 오타 하나로 남의 항목이
내 판단에 이어지는 것을 막는 유일한 자리다. finish 의 후속으로 만들어진 항목도 못 잇는다
(원장에 "이 세션이 만들었다"가 안 남는다).

내용이 다르면 다른 id 로 add 해서 이번 followups 에 실어라.
그 항목에 판단만 걸고 싶으면 note(kind='handoff', item_id=<그 항목>) 를 쓴다.`,
	}
}

// refuseUnreadableFollowupExistence 는 "그 id 의 항목이 있는지조차 못 읽은" 후속을 거절한다.
//
// ★ itemExists 의 fail-closed 갈래가 여기로 온다 — 왜 fail-closed 인지는 그 함수 주석에
// 있다. 요약: 여기서 "없다"로 접으면 남의 항목에 거짓 링크가 tx 안에서 커밋돼 되돌릴 수
// 없고, 거절은 트랜잭션 전이라 그 후속만 빼고 그대로 되부를 수 있다 — 비대칭이 명확하다.
func refuseUnreadableFollowupExistence(nth int, id string) *RefusedError {
	return &RefusedError{
		What: "finish",
		Reason: fmt.Sprintf("%d번째 후속(%s)은 그 id 의 항목이 있는지 못 읽어 자격을 판정할 수 없다",
			nth, clip(id, 64)),
		Guidance: "원장 조회가 실패했다 — 잠시 뒤 같은 followups 로 다시 불러라. " +
			"계속 이 사유로 거절되면 원장 조회 자체가 막혔다는 뜻이니 그 자리에서 알려라.",
	}
}

// linkableHint 는 "이을 수 있었던 것"의 이름을 낸다.
//
// ★ id 만 실은 후속이 **만들기**로 떨어지는 길은 하나뿐이다 — 그 id 의 항목이 없다(오타).
// 그때 "제목이나 본문이 없다"만 내면 세션은 제목·본문을 지어내 옆에 새 항목을 만든다.
// 이으려던 항목은 큐에 그대로 남고 쌍둥이가 하나 는다 — 이 도구가 없애려는 부류의
// 조용한 거짓이다. 이름을 내는 이유는 refuseIneligibleFollowup 과 같다(수만 말하면
// 무엇을 실을지 다시 조사해야 한다).
func linkableHint(eligible []string) string {
	if len(eligible) == 0 {
		return "이을 셈이었다면 — 이 선점 뒤 이 세션이 add 로 만든 열린 항목이 하나도 없어 이을 것이 없다."
	}
	return "이을 셈이었다면 그 id 의 항목이 없다는 뜻이다 — 지금 이을 수 있는 것은 " +
		strings.Join(eligible, ", ") + " 뿐이다."
}
