package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// 처방 — 발화 지점(설계 §6). 판정은 judge.Prescribe 가 하고 이 파일은 입력을 모으고 결과를 남긴다.
//
// ★ **세션 카드 파생을 안 돈다.** 이 경로는 턴마다 돌므로, git worktree list +
// 세션별 ChangedPaths·UncommittedPaths·UncommittedDelta 를 얹으면 **모든 턴 종료에 저장소 전수 훑기가 붙는다**.
// 필요한 입력(footprint·claim·judgment·session·레인)은 전부 DB 표라 git 을 안 탄다.
// 설계 §6 이 /notices 를 /dashboard.json 에서 가른 것과 같은 판정이다.
//
// ★ 레인도 같은 이유로 **줄 전체가 아니라 맨 앞 하나와 점유 유무**만 읽는다.
// 무엇을 기각했는지는 laneTurnRow 주석에 있다.
//
// ★ **그래서 겹침 처방은 변경 규모를 안 낸다 — 그것은 결함이 아니라 이 판정의 결과다**
// (2026-08-11). 규모(`+47/-1`)를 내려면 numstat 을 읽어야 하고, 그것은 위 문단이 금지한
// 바로 그 저장소 훑기다. 규모는 **git 파생을 이미 도는 표면**에만 실었다 —
// `board`·`pick` 의 꼬리 겹침(judge.OverlapsWithLive)이다. 거기가 사람이 "ask 를 써야 하나"를
// 실제로 판단하는 자리이기도 하다.
//
// 그래서 아래 `in.Others` 조립부는 judge.LiveSession.Delta 를 **비운 채로** 넘긴다.
// 그것이 맞다 — 채우려 드는 순간 모든 턴 종료에 저장소 전수 훑기가 붙는다.
// 다음 사람이 이 빈 필드를 결함으로 보고 "고치지" 않도록 여기 남긴다.

const (
	eventPrescribe    = "prescribe"
	eventPrescribeAck = "prescribe_ack"
	// eventPrescribeFolded 는 **접힌 턴에만** 남는다. 발화가 아니라 계측이라 이름이 갈린다.
	//
	// ★ 이 kind 가 따로인 것이 설계의 조건이다. `emittedKeys` 는 `kind='prescribe'` 로
	// 거르고 `store/prescribe_reach.go` 는 `kind IN ('prescribe','prescribe_ack')` 로 거른다 —
	// 둘 다 명시 목록이라 이 이벤트는 **억제 축에도 확인율에도 안 섞인다.** 같은 값을
	// `prescribe` payload 에 얹었다면 그 격리가 payload 해석에 걸리게 되고, 접힘을 세려는
	// 질의가 억제를 읽는 질의와 같은 행을 공유하게 된다.
	eventPrescribeFolded = "prescribe_folded"
)

// PrescribeResult 는 한 턴의 처방이다.
//
// ★ **발화로 남는 것은 Shown 뿐이다**(2026-08-06 개정. 아래 Prescriptions 의 기록 루프).
// 세 필드가 서로 다른 것을 세므로 셋을 같은 뜻으로 읽으면 안 된다 — 특히 `All` 은
// `POST /api/v1/sessions/{id}/prescriptions` 의 `all` 로 서버 밖에 나가는데 비시험
// 소비자가 0건이라, 이 주석이 그 필드의 유일한 계약이다.
//
// ★ **"원장에 안 남는다"와 "발화로 안 센다"는 다르다**(2026-08-09 개정). 접힌 것은 여전히
// `prescribe` 로 안 남지만 — 그래서 `suppressed` 가 안 누르고 다음 턴에 올라온다 —
// 접혔다는 **사실 자체**는 `prescribe_folded` 로 남는다. 앞선 판이 이 자리에 "원장에
// 안 남는다"고 적었고, 그 한 문장이 접힘 빈도를 측정 불가로 만든 결함의 요약이었다.
type PrescribeResult struct {
	Shown  []judge.Prescription `json:"shown"`  // 문구로 낼 것 (최대 judge.PrescribeMax) — **발화로 세는 것은 이것뿐이다**
	Folded int                  `json:"folded"` // 요약으로 접힌 수 — 발화로는 안 세고 prescribe_folded 로 남는다
	All    []judge.Prescription `json:"all"`    // 이번 턴에 판정된 전부(표시분 + 접힌 것). 발화로 세는 것은 Shown 뿐이다

	// Lifecycle 은 이 세션이 속한 **대화**(machine + cc_session_id)의 라이프사이클 단계다.
	// nil 이면 걸린 것이 없다. Task 12 의 hookStop 이 이 필드로 decision:block 을 낸다 —
	// 훅이 이미 이 POST 를 부르므로 추가 왕복이 0이다.
	Lifecycle *LifecycleGate `json:"lifecycle,omitempty"`
}

// prescribePayload 는 event.payload 의 모양이다.
//
// ★ **판정을 가른 축을 함께 싣는다(2026-08-11 개정).** 앞선 판은 `key`·`reason` 뿐이었고,
// 그래서 "이 발화가 왜 떴나"에 원장이 답을 못 했다. 실물 대가: unclaimed 118건의 성격을
// 가르려고 `item.claim`·`item.finish`·`claim.reclaim` 을 시간순으로 재생하는 일회용
// 스크립트가 필요했고, 그것이 그 조사의 대부분이었다.
//
// ★ **항목 id 만 싣는다 — 경로는 안 싣는다.** judge 쪽 `SiblingClaims`·`WorkspaceClaims`
// 주석의 논거가 그대로 걸린다. 덤으로 payload 가 발자국 수에 비례해 붓지 않는다.
//
// ★ **이것만으로는 거짓 양성을 못 센다.** 여기 남는 것은 판정이 **본** 것이고, 거짓 양성은
// 판정이 **못 본** 것 때문에 난다. 세상의 진실은 `store.ClaimHolderAt` 이 낸다 —
// 둘을 나란히 놓아야 "판정은 비었는데 세상엔 점유자가 있었다"가 세어진다.
// 그래서 이 구조체가 답하는 질문은 **"판정이 무엇을 보고 그렇게 말했나"** 하나다.
//
// ★ 빈 축은 `[]` 로 나간다(`omitempty` 를 안 쓴다). 없는 것과 빈 것을 원장에서 가르려면
// 키가 있어야 한다 — 이 형태 이전의 옛 행은 키 자체가 없어서 그 둘이 저절로 갈린다.
type prescribePayload struct {
	Key    string `json:"key"`
	Reason string `json:"reason"`
	// 아래 넷이 judge.PrescribeInput 의 같은 이름 축 그대로다.
	Claims          []string `json:"claims"`
	SiblingClaims   []string `json:"sibling_claims"`
	WorkspaceClaims []string `json:"workspace_claims"`
	Closed          []string `json:"closed"`
}

// claimViewIDs 는 ClaimView 목록에서 항목 id 만 뽑는다. **nil 대신 빈 슬라이스**를 낸다 —
// 위 구조체가 빈 축을 `[]` 로 내보내기로 한 결정이 여기 걸려 있다.
func claimViewIDs(vs []judge.ClaimView) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.ItemID)
	}
	return out
}

// idsOrEmpty 는 nil 을 빈 슬라이스로 바꾼다. 같은 이유다.
func idsOrEmpty(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}

// prescribeFoldedPayload 는 접힌 턴 하나의 흔적이다. **재측이 읽는 것이 이 모양 전부다.**
//
// 접힌 수와 총 처방 수는 **안 싣는다** — 각각 `len(keys)` 와 `shown + len(keys)` 로 파생된다
// (설계 §1 ①의 정신. 두 벌로 두면 어긋난 행이 원장에 영구히 남는데 event 는 추가 전용이다).
// 그래서 `shown` 은 파생 불가한 유일한 축으로 남는다: `PrescribeMax` 를 나중에 바꿔도
// **옛 행의 총 처방 수를 그 시점 상한으로 되짚을 수 없기 때문에** 그때의 값이 필요하다.
type prescribeFoldedPayload struct {
	Keys  []string `json:"keys"`  // 접힌 키 — 축 분포를 다시 재는 재료다
	Shown int      `json:"shown"` // 그 턴에 표시된 수
	// Since 는 그 턴이 본 창의 시작이다. **계측이 아니라 배선이다** — 다음 턴이 이 값을
	// 물려받아 밀린 처방이 돌아올 자리를 만든다(Prescriptions 의 창 물려받기 문단).
	// 계측용으로도 읽을 수 있다: 접힘이 몇 턴 이어졌나는 같은 Since 를 공유한 행의 수다.
	Since time.Time `json:"since"`
}

// Prescriptions 는 이 세션이 지금 받아야 할 처방을 내고, 낸 것을 event 에 기록한다.
func (s *Service) Prescriptions(ctx context.Context, sessionID string) (PrescribeResult, error) {
	sess, err := s.st.GetSession(ctx, sessionID)
	if err != nil {
		return PrescribeResult{}, err
	}

	in := judge.PrescribeInput{Now: s.now(), SessionID: sessionID, SelfCC: sess.CCSessionID}

	// 억제 상태 — 이 세션이 이미 낸 키와 그 시각.
	emitted, since, err := s.emittedKeys(ctx, sessionID, sess.OpenedAt)
	if err != nil {
		return PrescribeResult{}, err
	}
	in.Emitted = emitted

	// ★ **접힌 턴이 마지막이면 그 턴의 창을 물려받는다(2026-08-09 개정).**
	//
	// 2026-08-06 이 접힌 것을 발화로 안 세게 만들어 영구 소실을 순환으로 바꿨는데, 그 순환에
	// 조건이 하나 남아 있었다: 아래 TurnPaths 가 `f.LastAt.After(since)` 로 뽑히고 since 가
	// **마지막 발화 시각**이라, 밀린 축은 세션이 **그 경로를 다시 만져야만** 돈다.
	// 그래서 한 턴에 몰아친 다발은 세션이 다른 일로 가는 순간 그대로 사라졌다 —
	// 하필 PrescribeMax 가 애초에 겨냥한 시나리오다.
	//
	// 걸리는 축은 outside 만이 아니다. overlap 은 OverlapPairs 가 빈 TurnPaths 에 0쌍을 내고
	// unclaimed 는 `len(in.TurnPaths)==0` 이면 첫 가드에서 반환한다. 2026-08-06 재측의 접힘
	// 분포로 보면 사라지던 28건 중 **24건**이 이 셋이다(overlap 11 · unclaimed 11 · outside 2).
	// silent 만 무관하다 — NewPaths 는 LastJudgment 기준이라 이 창을 안 탄다.
	//
	// ★ **억제는 안 건드린다.** 창만 되돌리므로 이미 표시된 키는 여전히 suppressed 가 누른다.
	// 그래서 되돌아오는 것은 밀린 것뿐이고, 접힘이 끝난 턴에는 창이 정상적으로 밀린다 —
	// 그 두 경계를 TestFoldedTurnKeepsTheWindowUntilTheBacklogDrains 가 세 턴으로 잠근다.
	// 창을 안 미는 쪽으로 잘못 만들면 그것이 설계 §4 의 상시 점등이다.
	foldedAt, foldedSince, hasFolded, err := s.foldedWindow(ctx, sessionID, sess.OpenedAt)
	if err != nil {
		return PrescribeResult{}, err
	}
	// **동시각이면 물려받는다.** 접힘 이벤트는 그 턴의 발화 기록 **뒤에** 쓰이므로 정상 경로에서
	// foldedAt >= since 이고, 같은 초로 접히는 것도 같은 턴이다. Before 로 가르면 그 경계에서
	// 창이 밀려 밀린 처방이 다시 사라진다.
	if hasFolded && !foldedAt.Before(since) && !foldedSince.IsZero() {
		since = foldedSince
	}

	// 선점 항목과 각자의 선언 경로.
	claimed, err := s.st.ClaimedItems(ctx, sessionID)
	if err != nil {
		return PrescribeResult{}, err
	}
	for _, id := range claimed {
		it, err := s.st.GetItem(ctx, sess.Project, id)
		if err != nil {
			// 항목을 못 읽는 것은 처방을 못 낼 이유가 아니다. 조용히 접지 않고 남긴다.
			s.log.WarnContext(ctx, "처방: 선점 항목을 못 읽었다",
				"session_id", sessionID, "item", id, "error", err.Error())
			continue
		}
		in.Claims = append(in.Claims, judge.ClaimView{ItemID: it.ID, Paths: it.Paths})
	}

	// 같은 대화의 **다른 카드**가 쥔 선점. 규율이 지시하는 `git worktree add` 가 카드를
	// 가르고 선점은 갈리기 전 카드에 남으므로, 이 축이 없으면 워크트리에서 일하는 세션이
	// 매 턴 "선점 0건"으로 보인다(2026-08-06 실측: 대화 단위로는 10건이 그 상태였다).
	//
	// ★ GetItem 을 **안 부른다.** 이 축은 항목 id 만 나르고 선언 경로를 안 싣는다 —
	// 실으면 outside 가 형제의 경로를 기준으로 돌기 시작한다(judge 쪽 SiblingClaims 주석).
	// 덤으로 턴당 질의가 형제 항목 수만큼 늘지 않는다.
	if in.SiblingClaims, err = s.st.SiblingClaimedItems(
		ctx, sess.Project, sess.MachineID, sess.CCSessionID, sessionID); err != nil {
		return PrescribeResult{}, err
	}

	// 이 카드가 **서 있는 워크트리의 항목**이 지금 선점돼 있는가.
	//
	// ★ 형제 축이 못 잡는 나머지다. 형제 조인은 cc 로 걸리는데, 사람이 주 저장소에서
	// pick 하고 워크트리 안에서 **새 대화**를 열면 cc 가 갈린다. 08-07 이후 남은 거짓 양성
	// 16건 전수가 그 모양이다(judge 쪽 WorkspaceClaims 주석에 실측이 있다).
	//
	// ★ 여기서도 GetItem 을 **안 부른다** — 이 축은 항목 id 만 나르고 선언 경로를 안 싣는다
	// (형제 축과 같은 논거: 실으면 outside 가 그 경로를 기준으로 돌기 시작한다).
	// 질의는 워크트리가 관례 자리일 때만, 그때도 한 건이다.
	//
	// ★ 선점 **없음**은 처방을 못 낼 이유가 아니다 — 그때는 축이 비고 판정이 옛날대로 돈다.
	// 다른 오류만 올린다.
	if id := judge.WorkspaceItemID(sess.Worktree); id != "" {
		switch c, err := s.st.GetClaim(ctx, sess.Project, id); {
		case err == nil && c.ReleasedAt == nil:
			in.WorkspaceClaims = []string{id}
		case err != nil && !errors.Is(err, store.ErrNotFound):
			return PrescribeResult{}, err
		}
	}

	// 이 구간에 반납한 항목 — "한 번도 안 집었다"와 "방금 제대로 끝냈다"를 가르는 축이다.
	// **TurnPaths 와 같은 since 를 쓴다.** 두 창이 갈리면 "이번 턴에 만진 경로"와
	// "이번 턴에 끝낸 항목"이 서로 다른 구간을 가리키게 되고, 그 어긋남은 화면에 안 뜬다.
	//
	// ★ **그래서 창을 물려받으면 이쪽도 함께 넓어진다 — 그것이 맞다.** 접힌 턴의 창을 되돌린
	// 뒤 여기만 좁게 두면 방금 위에서 막은 어긋남이 정확히 그 턴에 생긴다: 밀린 outside 는
	// 다시 판정되는데 그 경로를 덮던 Closed 항목은 창 밖으로 나가 있어, **제대로 마무리한
	// 세션이 접힘 때문에 잔소리를 듣는다**(uncoveredByClosed 가 막으라고 있는 바로 그 결과).
	released, err := s.st.ReleasedItems(ctx, sessionID, since)
	if err != nil {
		return PrescribeResult{}, err
	}
	// **형제 카드의 반납도 같은 축이다.** finish 는 MCP 로 가고(카드 A) 처방은 훅에서
	// 뜬다(카드 B). 이것이 없으면 규율대로 마무리한 그 순간 카드 B 가 "한 번도 안 집었다"와
	// 똑같이 보인다 — 카드 단위에서 Closed 축이 이미 고친 결함이 대화 단위에 그대로 남는다.
	// 같은 since 를 쓰는 이유도 위와 같다.
	siblingReleased, err := s.st.SiblingReleasedItems(
		ctx, sess.Project, sess.MachineID, sess.CCSessionID, sessionID, since)
	if err != nil {
		return PrescribeResult{}, err
	}
	for _, id := range append(append([]string{}, released...), siblingReleased...) {
		it, err := s.st.GetItem(ctx, sess.Project, id)
		if err != nil {
			s.log.WarnContext(ctx, "처방: 반납 항목을 못 읽었다",
				"session_id", sessionID, "item", id, "error", err.Error())
			continue
		}
		in.Closed = append(in.Closed, judge.ClaimView{ItemID: it.ID, Paths: it.Paths})
	}

	// 이번 구간에 새로 만진 경로 · 마지막 판단 이후 새로 만진 경로.
	prints, err := s.st.Footprints(ctx, sessionID)
	if err != nil {
		return PrescribeResult{}, err
	}
	last, err := s.lastJudgmentAt(ctx, sessionID, sess.OpenedAt)
	if err != nil {
		return PrescribeResult{}, err
	}
	in.LastJudgment = last
	for _, f := range prints {
		if f.Origin != model.OriginObserved {
			continue // 선언·항목 경로는 "만졌다"가 아니다. 뭉개면 §3 이 가른 축이 사라진다
		}
		if f.LastAt.After(since) {
			in.TurnPaths = append(in.TurnPaths, f.Path)
		}
		if f.LastAt.After(last) {
			in.NewPaths++
		}
	}

	// 살아 있는 남의 세션. **창은 보드와 같은 것을 쓴다** — 두 자리에 두면 조용히 어긋난다.
	live, err := s.st.ListLive(ctx, sess.Project, s.cut(in.Now, s.window))
	if err != nil {
		return PrescribeResult{}, err
	}
	for _, v := range live {
		if v.Session.ID == sessionID {
			continue
		}
		in.Others = append(in.Others, judge.LiveSession{
			ID: v.Session.ID, Label: v.Session.Label, Paths: v.Paths,
			// ★ 대화 id 를 함께 넘긴다. 카드 id 만으로는 형제 카드(같은 대화, 다른 카드)를
			// 남으로 보고 **자기 자신과 조율하라**는 처방을 낸다.
			CCSessionID: v.Session.CCSessionID,
		})
	}

	// 랜딩 줄의 차례. 0 이면 차례가 아니고, 그 판정을 하는 자리는 아래 하나다.
	in.LaneTurnRow = s.laneTurnRow(ctx, sess.Project, sessionID)

	all := judge.Prescribe(in)
	shown, folded := judge.FoldPrescriptions(all)

	// **표시된 것만 기록한다(2026-08-06 개정).** 앞선 판은 접힌 것까지 기록하고 그 근거를
	// "안 기록하면 다음 턴에 그대로 다시 떠서 상한이 무의미해진다"라고 적었는데, 그 조합이
	// **접힌 처방을 영구히 지웠다**: 기록되면 suppressed 가 그 키를 누르고(해제 규칙은
	// silent 에만 있다), 세션은 그 문구를 **한 번도 못 본 채** 원장에는 "정상적으로 접혔다"로만
	// 남는다. 사라지는 것이 `outside`(남이 보는 겹침 입력이 낡았다) 나 `unclaimed` 면
	// 그 사실을 아무도 못 듣는다.
	//
	// ★ 상한은 무의미해지지 않는다 — **순환한다.** 표시된 셋만 눌리므로 넷째가 다음 턴에
	// 첫 칸으로 올라온다. 설계 §4 가 고발한 "상시 점등"(같은 것이 매 턴 반복)과는
	// 다르다 — 눌리는 것은 표시된 것뿐이다.
	//
	// ⚠ **그 "다음 턴"이 안 오면 순환도 없다** — 실측 36%가 그랬다(아래 재측 문단).
	// 순환은 기구가 아니라 **다음 Stop 훅**에 달려 있고, 그것은 여기서 못 만든다.
	//
	// ★ **그 순환에 붙어 있던 조건을 2026-08-09 에 없앴다.** 앞선 판은 여기에 "세션이 그
	// 경로를 다시 안 만지면 안 올라온다 — 그래서 한 턴에 몰아친 outside 다발은 여전히
	// 소실이고, 그것은 상한 자체의 한계다"를 적고 후속 항목 이름을 달아 뒀다. 그 항목이
	// 이것이고, 한계가 아니라 **창의 문제**였다: 접힌 턴이 자기가 본 창을 물려주면
	// 조건 없이 순환한다(위 창 물려받기 문단). 걸려 있던 축은 outside 만이 아니었다.
	//
	// ★ 재측(2026-08-06): 처방이 뜬 턴 129개 중 접힌 턴 **15개**(11.6%)이고 한 턴 최대는
	// **7건**이다. 접혀서 사라지던 축은 overlap 11 · unclaimed 11 · silent 4 · outside 2 —
	// 앞선 판의 "35턴 중 2개"는 **표본이 4배가 되기 전** 값이다(lane-turn 은 원장에 전 기간
	// 0건이라 그 축의 효과가 아니다. PrescribeMax 주석의 재측 문단).
	//
	// ★ **접힘은 자기 이벤트로 잰다(2026-08-09 개정).** 앞선 판은 여기에 "이 커밋 뒤로
	// 접힘 빈도는 원장에서 못 잰다 — 다시 재려면 folded 를 원장에 실어야 한다"를 적고
	// 후속 항목 이름을 달아 뒀다. 그 항목이 이것이다.
	//
	// 옛 재측 레시피("턴 = 같은 세션·같은 초의 prescribe 이벤트 묶음, len>3 이면 접힌 턴")는
	// **위 루프가 표시분만 도는 한 영구히 0을 낸다** — 상한이 구조적으로 3을 넘기지 못하게
	// 막기 때문이다. 그래서 접힌 턴이 자기 이름의 이벤트 하나를 남기고, 그때부터 재측은
	// `kind='prescribe_folded'` 를 세는 일이 된다(위 129/15 를 그 축으로 다시 만든다).
	//
	// ★ **재측(2026-08-12) — 그 축으로 다시 쟀고, 수렴은 한 턴이다.** 배포 뒤 이틀 ·
	// prescribe_folded 14건 기준으로 **턴 70개 중 접힌 턴 14개(20.0%)**, 접힌 축은
	// overlap 18 · silent 6 · unclaimed 1(키 25개 · 한 턴 최대 6). 위 129/15 는 기준선으로
	// 남는다 — 비율이 오른 원인은 축이 아니라 overlap 밀도다(이 창의 처방 129건 중 99건).
	//
	// **같은 Since 를 공유한 연쇄는 14개가 전부 길이 1이다.** 그것만으로는 "한 턴에 마른다"와
	// "물려받기가 안 걸린다"가 구분이 안 되므로 접힌 키의 복귀를 따로 쟀다: 25개 중 16개 복귀,
	// **미복귀 9개는 전부 그 세션에 다음 처방 턴이 없던 경우**이고, 기회가 있었던 11턴은
	// 11/11 이 **바로 다음 턴**에 전부 복귀했다. 위 "순환한다"는 이제 추론이 아니다.
	//
	// ⚠ 다만 **다턴 배수는 여전히 미실측**이다 — 상한 3을 넘겨 접힌 턴이 그 창에 1개(6개
	// 접힘)뿐이었고 그 세션은 뒤에 턴이 없었다. 잠긴 것은 "3개 이하는 다음 한 턴에 전부"까지고,
	// PrescribeMax 를 다시 볼 근거는 안 생겼다(미루는 것이 한 턴이면 상한이 손해를 안 만든다).
	// ★ 축은 루프 **밖**에서 한 번 뽑는다. 한 턴의 발화들은 같은 판정 입력에서 나왔으므로
	// 행마다 다시 계산할 것이 없고, 다시 계산하면 행마다 다른 값이 실릴 자리가 생긴다.
	axes := prescribePayload{
		Claims:          claimViewIDs(in.Claims),
		SiblingClaims:   idsOrEmpty(in.SiblingClaims),
		WorkspaceClaims: idsOrEmpty(in.WorkspaceClaims),
		Closed:          claimViewIDs(in.Closed),
	}
	for _, p := range shown {
		row := axes
		row.Key, row.Reason = p.Key, p.Reason
		s.st.LogEvent(ctx, eventPrescribe, sess.Project, sessionID, row)
	}
	if folded > 0 {
		keys := make([]string, 0, folded)
		for _, p := range all[len(shown):] {
			keys = append(keys, p.Key)
		}
		// since 는 위에서 물려받기를 거친 값이다 — 접힘이 이어지는 동안 같은 창이 계속
		// 실려 나가고, 그래서 밀린 것이 다 나갈 때까지 창이 안 밀린다.
		s.st.LogEvent(ctx, eventPrescribeFolded, sess.Project, sessionID,
			prescribeFoldedPayload{Keys: keys, Shown: len(shown), Since: since})
	}
	if len(all) > 0 {
		s.log.InfoContext(ctx, "처방 발화",
			"session_id", sessionID, "count", len(all), "shown", len(shown), "folded", folded)
	}

	result := PrescribeResult{Shown: shown, Folded: folded, All: all}
	// 대화 단위 라이프사이클 판정 — Task 11. 카드 갈림을 넘어 (machine, cc_session_id) 로
	// 접은 관측을 순수 함수(judgeLifecycleGate)에 넘긴다.
	//
	// ★ 실패해도 처방 전체를 죽이지 않는다 — laneTurnRow 와 같은 관용(WARN 뒤 계속)이다.
	// 이 축이 하나 못 잡는다고 겹침·미선점·outside 같은 나머지 처방까지 함께 잃으면
	// 대가가 이 축의 가치보다 크다.
	if conv, cerr := s.st.ConversationLifecycle(ctx, sess.Project, sessionID); cerr != nil {
		s.log.WarnContext(ctx, "라이프사이클 판정 실패", "session_id", sessionID, "error", cerr.Error())
	} else {
		result.Lifecycle = judgeLifecycleGate(conv)
	}
	return result, nil
}

// laneTurnRow 는 이 세션 차례가 된 줄 행의 번호다. 0 이면 차례가 아니다.
//
// ★ **발화 0건은 기구 결함이 아니라 표본이다**(실측 2026-08-12, 항목
// fd-lane-turn-machinery-is-dead-remove-it 의 판정 "안 지운다" — finish 세션 38건 중
// 15건(39%)이 랜딩 줄에 안 섰고 done 196건에 landed_ref 0건. ρ=1.14% 가 실측 대기율
// 1.06% 와 맞아, 대기 부재는 구조가 아니라 낮은 랜딩률의 결과다). 랜딩의 문(instructions·
// pick 꼬리)과 라이프사이클 block 이 서면 이 축이 처음으로 표본을 갖는다.
// wait 중인 세션에게는 이 처방이 안 뜬다(턴이 안 끝나 Stop 이 없다) — 이 축이 잡는 것은
// wait 를 아직 안 부른(또는 타임아웃으로 나온) 세션의 턴 끝이고, block(stage=lane-wait)의
// "줄에 서 있는데 안 쥠"보다 구체적인 신호("네 차례다")를 낸다.
//
// 차례의 정의가 자원 집합의 곱으로 넓어졌다(2026-08-12): 내 살아 있는 줄 행이 있고,
// 그 행의 **모든** 자원에서 (맨 앞이 그 행) 그리고 (레인이 비었다).
// 남이든 나든 쥔 자원이 하나라도 있으면 0 이다 — 남이 쥔 것은 어긋남(사람의 회수가 푼다),
// 내가 쥔 것은 land 가 이미 turn 으로 답한 것이라 같은 말을 두 번 하는 것이다.
// (오류를 안 올리는 규율·LandingLane 기각 근거는 개편 전 주석 그대로다.)
func (s *Service) laneTurnRow(ctx context.Context, project, sessionID string) int64 {
	row, err := s.st.LiveLandingRow(ctx, project, sessionID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.log.WarnContext(ctx, "처방: 내 줄 행을 못 읽었다",
				"session_id", sessionID, "project", project, "error", err.Error())
		}
		return 0 // 줄에 안 섰다(정상) 또는 못 읽었다
	}
	for _, r := range row.Resources {
		front, ferr := s.st.FrontLandingRowFor(ctx, project, r)
		if ferr != nil || front.ID != row.ID {
			return 0 // 그 자원 줄에 앞사람이 있거나 못 읽었다
		}
		if _, herr := s.st.HeldBy(ctx, project, r); herr == nil {
			return 0 // 누가 쥐고 있다(나든 남이든)
		} else if !errors.Is(herr, store.ErrNotFound) {
			s.log.WarnContext(ctx, "처방: 레인 점유를 못 읽었다",
				"session_id", sessionID, "project", project, "error", herr.Error())
			return 0
		}
	}
	return row.ID
}

// emittedKeys 는 이미 낸 키와 그 시각, 그리고 마지막 발화 시각을 낸다.
// 마지막 발화 시각이 "이번 구간"의 시작이다 — 없으면 세션 시작이다.
func (s *Service) emittedKeys(ctx context.Context, sessionID string, openedAt time.Time) (map[string]time.Time, time.Time, error) {
	evs, err := s.st.ListSessionEvents(ctx, sessionID, eventPrescribe, openedAt)
	if err != nil {
		return nil, time.Time{}, err
	}
	out := map[string]time.Time{}
	since := openedAt
	for _, e := range evs {
		var p prescribePayload
		if err := json.Unmarshal([]byte(e.Payload), &p); err != nil || p.Key == "" {
			// 해석 실패를 조용히 버리면 그 키가 안 눌린 것으로 보여 처방이 다시 뜬다.
			s.log.WarnContext(ctx, "처방 이벤트 payload 해석 실패",
				"session_id", sessionID, "payload", e.Payload)
			continue
		}
		out[p.Key] = e.At
		if e.At.After(since) {
			since = e.At
		}
	}
	return out, since, nil
}

// ackPrescriptions 는 **판단을 남기는 경로**(note·finish)가 닫는 것을 닫는다.
//
// ★ note 한 번이 (거의) 전부를 닫는 이유: 처방 문구가 무엇을 쓸지 지정하므로 보통 판단 하나가
// 그것을 덮는다. 처방마다 대응 판단을 요구하면 세션이 형식적 note 를 양산하고,
// 그러면 건수는 오르는데 판단 바이트는 안 오른다 — 설계 §10 이 그 둘을 함께 보라고 한 이유다.
//
// ★ **"전부"에서 하나가 빠진다(2026-08-09 개정).** 행동이 판단이 아닌 처방은 판단으로 안
// 닫힌다 — `judge.AckedByLand` 가 가리는 `lane-turn` 이다. 그전까지 이 함수는 키를 안 가려서
// 확인이 그 축에 대해 **정확히 반대 신호**를 쟀다: 처방대로 랜딩한 세션은 미확인으로 남고,
// 레인과 아무 상관 없는 note 한 줄을 남긴 세션이 확인으로 잡혔다. 그 사실을 잠그고 있던
// 시험(`…AckMeasuresJudgmentsNotTheLandItPrescribed`)이 스스로 "통로를 뚫으면 여기가 먼저
// 빨개진다, 그때 고칠 것은 시험이 아니라 여기 적힌 사실"이라고 적어 뒀고, 이것이 그 개정이다.
// 지금 그 자리를 잠그는 것은 `TestLaneTurnIsClosedByLandAndUnrelatedJudgmentsLeaveItOpen` 이다.
//
// ★ **실패해도 판단 저장을 되돌리지 않는다.** 판단이 재생성 불가한 자산이고 ack 은 계측이다.
// 다만 삼키지 않는다 — WARN 으로 남긴다.
//
// ★ 이 축과 `AckReach`(board.go)를 섞지 마라. 저쪽은 키를 안 보고 **대화 단위**로 센다
// (machine + cc_session_id. 카드가 갈려도 한 번 센다) — 반면 이 함수는 **카드 하나**의
// 열린 처방만 닫는다. 그래서 형제 카드에 뜬 처방은 여기서 안 닫히고, 그 대화는 저쪽의
// 분모에는 들어가면서 분자에는 안 들어간다. 그 차이가 지금 확인율이 100%가 아닌 이유다.
// 키별 확인율을 내는 코드는 없다 — 설계 §10 의 "overlap 0/31" 은 사람이 따로 잰 값이다.
func (s *Service) ackPrescriptions(ctx context.Context, project, sessionID string) {
	s.ackPrescriptionsMatching(ctx, project, sessionID, func(k string) bool {
		return !judge.AckedByLand(k)
	})
}

// ackLaneTurn 은 land 가 **자기가 방금 응답한 줄 행**의 차례 처방을 닫는다.
//
// ★ 접두가 아니라 그 행 하나다. 차례를 흘리고 다시 선 세션은 서로 다른 행의 lane-turn 을
// 여럿 열어 둘 수 있고(억제 키에 행 번호가 실린 이유다), 접두로 닫으면 **아직 응답하지
// 않은 차례**까지 확인 처리된다 — 그러면 확인율은 다시 행동이 아니라 접두 일치를 잰다.
// TestLandAcksOnlyTheRowItAnswered 가 그 자리를 잠근다.
func (s *Service) ackLaneTurn(ctx context.Context, project, sessionID string, rowID int64) {
	key := fmt.Sprintf("%s:%d", judge.PrescribeLaneTurn, rowID)
	s.ackPrescriptionsMatching(ctx, project, sessionID, func(k string) bool { return k == key })
}

// ackPrescriptionsMatching 은 열려 있는 처방 중 want 가 고른 것을 닫는다.
//
// ★ 술어를 밖에서 받는다 — 어느 경로가 어느 키를 닫는가는 **부르는 쪽의 성질**이지
// 이 함수의 성질이 아니다. 두 호출자(판단 경로 · land 경로)가 각자 본문을 복사했다면
// 그때부터 "빈 ack 을 안 남긴다" 같은 규율이 두 벌이 된다.
func (s *Service) ackPrescriptionsMatching(
	ctx context.Context, project, sessionID string, want func(string) bool) {
	sess, err := s.st.GetSession(ctx, sessionID)
	if err != nil {
		s.log.WarnContext(ctx, "ack: 세션을 못 읽었다", "session_id", sessionID, "error", err.Error())
		return
	}
	open, _, err := s.emittedKeys(ctx, sessionID, sess.OpenedAt)
	if err != nil {
		s.log.WarnContext(ctx, "ack: 발화 이력을 못 읽었다", "session_id", sessionID, "error", err.Error())
		return
	}
	acked, err := s.ackedKeys(ctx, sessionID, sess.OpenedAt)
	if err != nil {
		s.log.WarnContext(ctx, "ack: 확인 이력을 못 읽었다", "session_id", sessionID, "error", err.Error())
		return
	}
	var keys []string
	for k := range open {
		if !acked[k] && want(k) {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return // **빈 ack 은 안 남긴다** — 확인율의 분자를 부풀린다
	}
	sort.Strings(keys) // 같은 입력에 같은 payload
	s.st.LogEvent(ctx, eventPrescribeAck, project, sessionID, map[string]any{"keys": keys})
}

// foldedWindow 는 **마지막 접힌 턴**의 시각과 그 턴이 본 창을 낸다. ok 는 접힌 턴이 있었나다.
//
// ★ 해석 실패를 조용히 버리지 않는다. 버리면 그 턴이 물려줄 창이 사라져 밀린 처방이
// 그대로 소실되는데(이 개정이 막으려는 바로 그 결과), 화면에는 아무것도 안 뜬다 —
// emittedKeys 가 같은 이유로 WARN 을 남기는 것과 같은 규율이다.
//
// ★ 세션 카드 하나만 본다. 형제 카드에서 접힌 턴은 여기 안 잡히는데, 그것이 맞다 —
// 창의 단위인 footprint 도 카드 단위다(store.Footprints 가 session_id 로 뽑는다).
func (s *Service) foldedWindow(ctx context.Context, sessionID string, openedAt time.Time) (at, window time.Time, ok bool, err error) {
	evs, err := s.st.ListSessionEvents(ctx, sessionID, eventPrescribeFolded, openedAt)
	if err != nil {
		return time.Time{}, time.Time{}, false, err
	}
	for _, e := range evs {
		var p prescribeFoldedPayload
		if uerr := json.Unmarshal([]byte(e.Payload), &p); uerr != nil {
			s.log.WarnContext(ctx, "접힘 이벤트 payload 해석 실패(그 턴의 창을 못 물려받는다)",
				"session_id", sessionID, "payload", e.Payload)
			continue
		}
		if !ok || e.At.After(at) {
			at, window, ok = e.At, p.Since, true
		}
	}
	return at, window, ok, nil
}

// ackedKeys 는 이미 확인된 키다.
func (s *Service) ackedKeys(ctx context.Context, sessionID string, openedAt time.Time) (map[string]bool, error) {
	evs, err := s.st.ListSessionEvents(ctx, sessionID, eventPrescribeAck, openedAt)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, e := range evs {
		var p struct {
			Keys []string `json:"keys"`
		}
		if err := json.Unmarshal([]byte(e.Payload), &p); err != nil {
			s.log.WarnContext(ctx, "ack payload 해석 실패", "payload", e.Payload)
			continue
		}
		for _, k := range p.Keys {
			out[k] = true
		}
	}
	return out, nil
}

// lastJudgmentAt 은 이 세션의 마지막 판단 시각이다.
// **판단이 하나도 없으면 세션 시작 시각을 낸다** — judge 쪽 제로값 규약(기준 없음)에 맞춘다.
func (s *Service) lastJudgmentAt(ctx context.Context, sessionID string, openedAt time.Time) (time.Time, error) {
	js, err := s.st.ListJudgmentsBySession(ctx, sessionID)
	if err != nil {
		return time.Time{}, err
	}
	out := openedAt
	for _, j := range js {
		if j.At.After(out) {
			out = j.At
		}
	}
	return out, nil
}
