package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// 마무리 — 판단 저장 + 후속 등록 + 항목 종료 + 자원 반납을 **한 호출, 한 트랜잭션**으로.
//
// 기존 규율은 이 순서를 산문으로 강제했고(문서 → done → add → unregister),
// 순서를 어긴 세션이 실제로 있었다. 원자화하면 **검산할 순서 자체가 사라진다**.

// HandoffGuidance 는 body 없이 finish 를 부른 세션에게 그 자리에서 내는 문구다.
//
// 규율 산문을 도구 설명이나 스킬에 넣지 않는다(컨텍스트 예산 — 설계 §6).
// **필요할 때, 그 자리에서** 응답에 싣는다. 넷의 문구는 설계 §5 "남는 손 기재 넷"의
// judgment(kind=handoff) 줄 그대로다.
const HandoffGuidance = `무엇을 적어야 하는가 — 넷이다:
  ① 왜 그렇게 했나
  ② 무엇을 기각했나
  ③ 일부러 안 한 것
  ④ 확인했으나 못 한 것
안 남기면 다음 세션이 같은 조사를 처음부터 다시 하거나,
더 나쁘게는 **의도적으로 남긴 자리를 결함으로 보고 고치러 간다.**
후속이 있으면 followups 로 같은 호출에 넣어라 — 그러면 판단과 후속이 FK 로 이어진다.`

// FollowupInput 은 마무리와 같은 호출에 넣는 후속 항목이다.
type FollowupInput struct {
	ID     string
	Title  string
	Body   string
	Paths  []string
	Labels []string
	After  []model.After
}

// FinishInput 은 마무리 한 번의 인자다.
type FinishInput struct {
	Project     string
	SessionID   string
	ItemID      string
	Outcome     model.ItemState // done | dropped
	Title       string
	Body        string // 핸드오프 본문. **비면 거절한다**
	CloseReason string // dropped 면 필수
	Followups   []FollowupInput
	Links       []model.JudgmentLink // 커밋 등 추가 링크
}

// FinishResult 는 마무리 결과다.
type FinishResult struct {
	Item      model.Item     `json:"item"`
	Judgment  model.Judgment `json:"judgment"`
	Followups []model.Item   `json:"followups,omitempty"`
	Released  []string       `json:"released,omitempty"` // 함께 반납한 자원
	// SkippedFollowups 는 **id 가 이미 있어 안 넣은** 후속이다.
	//
	// 이 칸이 없으면 흡수가 거짓말이 된다 — 세션은 후속이 들어간 줄 알고 떠나고,
	// 그 id 의 항목은 남이 만든 다른 것이다. Released 와 같은 성격의 칸이다:
	// "요청한 것과 실제로 된 것이 다르다"를 그 자리에서 말한다.
	SkippedFollowups []string `json:"skipped_followups,omitempty"`

	// StillHeld 는 이 finish 뒤에도 **이 세션이 여전히 쥐고 있는** 항목 id 다
	// (방금 닫은 항목은 뺀다).
	//
	// ★ 왜 이 칸이 필요한가. finish 는 항목을 **하나만** 닫는다 — 항목마다 자기
	// 판단이 필요하기 때문이고 그 설계는 안 바꾼다. 그런데 pick 은 이제 묶음을
	// 집는다. 그래서 묶음 3건을 집은 세션이 finish 를 한 번 부르면 2건이 선점된
	// 채로 남는데, 지금까지 **그 사실을 말하는 표면이 하나도 없었다**:
	// Tx.FinishItem 은 닫은 항목의 선점만 반납하고, RenderFinish 에는 나머지를
	// 말하는 갈래가 없고, 세션이 생존 창을 벗어나면 보드에서도 사라진다.
	// schema.sql 에는 만료가 없고 세션 종료가 선점을 풀지도 않으므로, 그 2건은
	// **사람이 강제로 풀 때까지 다른 어떤 세션도 못 집는다.**
	//
	// ★ **포인터다.** QueueOpen·PathCheck·Bundle 과 같은 계약이다:
	//   nil        = 이 응답은 그 축을 못 읽었다(조회 실패 · 이 필드 이전의 구서버·캐시)
	//   빈 목록     = 읽었고, 남은 선점이 정말 0건이다
	// 슬라이스만 두면 그 둘이 같은 값으로 접혀, 조회가 실패한 응답이 "남은 선점
	// 없음"을 단정한다 — 이 프로젝트가 반복해서 닫아 온 실패 모양 그대로다.
	StillHeld *[]string `json:"still_held,omitempty"`

	// QueueBalance 는 이 마무리가 큐에 한 일과, 그 직후 큐가 어떤 상태인가다.
	//
	// ★ **포인터다** — StillHeld 와 같은 계약이다. nil 은 "수지 0"이 아니라
	// "이 응답이 그 축을 못 읽었다"이고, 0으로 접으면 조회가 실패한 응답이
	// "큐가 안 늘었다"를 단정한다.
	// 타입·상수는 finish_balance.go 에 있다 — 이 파일의 import 블록을 안 늘리려는 것이다
	// (finish_followups.go 가 갈라진 것과 같은 이유).
	QueueBalance *QueueBalance `json:"queue_balance,omitempty"`

	Derived
}

// FinishVerdict 는 마무리 요청의 판정이다. Reason 은 항상 채운다.
type FinishVerdict struct {
	OK       bool
	Reason   string
	Guidance string // 거절일 때 무엇을 하면 되는지. 없을 수 있다
}

// JudgeFinish 는 마무리 인자가 성립하는지 판정한다. 순수 함수다.
//
// 불리언이 아니라 **사유와 처방**을 돌려준다. body 누락은 이 도구가 지키려는 것의 핵심이라
// 사유만으로 끝내지 않고 "무엇을 적어야 하는지"까지 낸다 —
// 판단은 원리적으로 파생 불가한 유일한 자산이고, 안 남으면 다음 세션이
// **의도적으로 남긴 자리를 결함으로 보고 고치러 간다.**
func JudgeFinish(outcome model.ItemState, itemID, body, closeReason string) FinishVerdict {
	switch {
	case strings.TrimSpace(itemID) == "":
		return FinishVerdict{Reason: "item_id 가 비었다 — 무엇을 끝내는지 없이는 종료도 판단 링크도 좌표가 없다"}

	case outcome != model.ItemDone && outcome != model.ItemDropped:
		return FinishVerdict{Reason: fmt.Sprintf(
			"outcome 은 done 또는 dropped 여야 한다(받은 값 %q)", clip(string(outcome), 32))}

	case strings.TrimSpace(body) == "":
		return FinishVerdict{
			Reason:   "판단 본문(body)이 비어 있어 끝낼 수 없다",
			Guidance: HandoffGuidance,
		}

	case outcome == model.ItemDropped && strings.TrimSpace(closeReason) == "":
		return FinishVerdict{
			Reason:   "outcome=dropped 에는 폐기 사유(close_reason)가 필수다",
			Guidance: "사유 없는 폐기는 나중에 왜 버렸는지 되짚을 수 없다 — 한 줄이면 된다.",
		}

	default:
		return FinishVerdict{OK: true, Reason: "항목 좌표·종료 상태·판단 본문이 전부 있다"}
	}
}

// Finish 는 판단 저장 + 후속 등록 + 항목 종료 + 자원 반납을 한 트랜잭션에서 한다.
//
// ★ 넷 중 하나라도 실패하면 **전부 롤백된다.** 판단만 남고 항목이 안 닫히거나,
// 항목은 닫혔는데 후속이 안 들어간 상태는 만들어지지 않는다 —
// 그 반쪽 상태가 기존 도구에서 "핸드오프는 했는데 후속이 유입되지 않은" 결함이었다.
func (s *Service) Finish(ctx context.Context, in FinishInput) (FinishResult, error) {
	if v := JudgeFinish(in.Outcome, in.ItemID, in.Body, in.CloseReason); !v.OK {
		s.log.WarnContext(ctx, "마무리 거절",
			"project", clip(in.Project, 64), "session_id", clip(in.SessionID, 64),
			"item", clip(in.ItemID, 64), "reason", v.Reason)
		return FinishResult{}, &RefusedError{What: "finish", Reason: v.Reason, Guidance: v.Guidance}
	}
	// 후속을 안 실었으면 **한 번** 붙잡는다 — 판정과 사유는 finish_followups.go 에 있다.
	// body 관문(위 JudgeFinish)과 같은 자리·같은 모양이다: 빠진 것을 그 자리에서 말한다.
	if len(in.Followups) == 0 {
		if refused := s.judgeMissingFollowups(ctx, in); refused != nil {
			s.log.WarnContext(ctx, "마무리 거절 — 후속이 안 실렸다",
				"project", clip(in.Project, 64), "session_id", clip(in.SessionID, 64),
				"item", clip(in.ItemID, 64))
			return FinishResult{}, refused
		}
	}
	// ★ 같은 호출에 같은 후속 id 가 두 번 실리면 여기서 끊는다. 링크는 dedupeLinks 가
	//   살리지만, 두 번째 AddItem 은 **자기 트랜잭션의 첫 INSERT** 때문에 중복 흡수로 빠져
	//   "남이 만든 것이라 건너뛰었다"는 거짓 사유가 응답에 나간다.
	seen := make(map[string]bool, len(in.Followups))
	for i, f := range in.Followups {
		if err := ValidateItemID(f.ID); err != nil {
			return FinishResult{}, &RefusedError{What: "finish",
				Reason:   fmt.Sprintf("%d번째 후속: %v", i+1, err),
				Guidance: "후속 항목 id 도 브랜치 이름으로 그대로 쓰인다."}
		}
		if seen[f.ID] {
			return FinishResult{}, &RefusedError{What: "finish",
				Reason:   fmt.Sprintf("%d번째 후속(%s)이 같은 호출에 두 번 실렸다", i+1, clip(f.ID, 64)),
				Guidance: "같은 항목을 두 번 만들 수도, 두 번 이을 수도 없다 — 한 번만 실어라."}
		}
		seen[f.ID] = true
		if strings.TrimSpace(f.Title) == "" || strings.TrimSpace(f.Body) == "" {
			return FinishResult{}, &RefusedError{What: "finish",
				Reason: fmt.Sprintf("%d번째 후속(%s)에 제목이나 본문이 없다", i+1, clip(f.ID, 64)),
				Guidance: "후속은 다음 세션이 집을 항목이다 — 제목만 있으면 " +
					"그 세션이 무엇을 해야 하는지 다시 조사해야 한다."}
		}
		// ★ followup.paths 도 add(item.paths)와 같은 관문(judgeItemPathsCoordinate,
		// pick.go)을 거친다. Finish 는 아래 ②에서 t.AddItem 을 직접 불러 AddItem 의
		// 검증 루프를 거치지 않으므로, 여기서 따로 부르지 않으면 같은 사람이 같은
		// 세션에서 add 는 거절당하고 finish followup 은 조용히 통과하는 우회 문이
		// 된다 — 반쪽 발화는 균일한 부재보다 나쁘다(관문이 발화한다는 것만 가르치고
		// 다른 문에서 배신한다).
		if err := judgeItemPathsCoordinate(f.Paths); err != nil {
			return FinishResult{}, &RefusedError{What: "finish",
				Reason: fmt.Sprintf("%d번째 후속(%s)의 %s", i+1, clip(f.ID, 64), err),
				Guidance: "경로는 저장소 상대(internal/api/x.go) 또는 POSIX 절대경로여야 한다 — " +
					"좌표계가 다르면 이 후속 항목의 겹침 축이 조용히 죽는다."}
		}
	}

	now := s.now()

	var out FinishResult
	err := s.st.Tx(ctx, func(t *store.Tx) error {
		// ★ 시도를 **먼저** 예약한다. Tx.LogEvent 는 롤백된 뒤에도 흘러가므로
		//   "무엇을 시도했다 실패했나"가 원장에 남는다 — 끝에 두면 성공한 것만 세게 되고,
		//   그러면 §10 의 "세션당 쓰기 호출 수"가 실패를 못 본다.
		t.LogEvent("item.finish", in.Project, in.SessionID, map[string]any{
			"item": in.ItemID, "mode": string(in.Outcome), "count": len(in.Followups),
			"bytes": len(in.Body), // §10 "세션당 판단 바이트" — 0 에 수렴하면 위험 신호다
		})

		// ① 판단 — 가장 먼저 저장한다. 이것이 원리적으로 파생 불가한 유일한 자산이다.
		//
		// ★ dedupeLinks 를 지난다. judgment_link 의 PK 가 (판단·종류·대상)이라 겹치면
		//   이 INSERT 가 실패하고 **그 판단이 통째로 사라진다** — 사유는 그 함수 주석에 있다.
		links := append([]model.JudgmentLink{{TargetKind: "item", TargetID: in.ItemID}}, in.Links...)
		for _, f := range in.Followups {
			links = append(links, model.JudgmentLink{TargetKind: "item", TargetID: f.ID})
		}
		j, err := t.AddJudgment(model.Judgment{
			Project: in.Project, SessionID: in.SessionID, At: now,
			Kind: model.JudgmentHandoff, Title: in.Title, Body: in.Body, Links: dedupeLinks(links),
		})
		if err != nil {
			return err
		}
		out.Judgment = j

		// ② 후속 등록. 여기서 실패하면 ①③④ 가 전부 롤백된다 —
		//    그것이 이 함수를 한 트랜잭션에 둔 이유다.
		//
		// ★ 단 **중복 id 하나만 예외다.** 그 갈래는 흡수하고 계속 간다.
		//
		//   원자성이 지키려는 실패 모드는 "핸드오프는 했는데 후속이 유입되지 않은" 상태인데,
		//   중복 id 에서는 그 모드가 성립하지 않는다 — **같은 id 의 항목이 이미 존재한다.**
		//   반면 롤백하면 ① 의 판단이 함께 사라지고, 그것은 원리적으로 파생 불가하다.
		//   나머지 셋(종료·반납·후속)은 전부 다시 만들 수 있지만 본문은 그 세션에만 있다.
		//
		//   ④ 가 이미 같은 판단을 내렸다(ErrNotFound·ResourceHeldError 를 흡수). 이 자리만
		//   그 규율 밖에 있었다. 두 세션이 같은 축을 동시에 마무리하며 자연스럽게 같은
		//   후속 이름을 고르는 것이 이 결함의 실제 진입로다.
		//
		//   중복 **이외의** 실패(FK 위반·CHECK·직렬화)는 그대로 롤백한다. 그 분류는
		//   store.JudgeConstraintCode 가 이미 하고 있어 여기서 문구를 파싱하지 않는다.
		for _, f := range in.Followups {
			it := model.Item{
				Project: in.Project, ID: f.ID, Title: f.Title, Body: f.Body,
				Paths: f.Paths, Labels: f.Labels, State: model.ItemOpen, After: f.After,
				CreatedAt: now,
			}
			if err := t.AddItem(it); err != nil {
				var conflict *store.ConflictError
				if errors.As(err, &conflict) && conflict.Kind == store.ConflictDuplicate {
					// 흡수했으면 **반드시 말한다.** 조용히 넘기면 세션은 후속이 들어간 줄 알고
					// 떠나고, 그 id 의 항목은 남이 만든 다른 것이다 — 판단을 지키려다
					// 더 나쁜 거짓을 만들게 된다. 응답(SkippedFollowups)과 원장 양쪽에 남긴다.
					out.SkippedFollowups = append(out.SkippedFollowups, f.ID)
					t.LogEvent("item.followup_skipped", in.Project, in.SessionID, map[string]any{
						"item": f.ID,
						"why":  "같은 id 의 항목이 이미 있다 — 판단을 지키려고 이 후속만 건너뛴다",
					})
					continue
				}
				return fmt.Errorf("후속 항목 %s 등록 실패: %w", clip(f.ID, 64), err)
			}
			out.Followups = append(out.Followups, it)
		}

		// ③ 항목 종료(선점 반납을 포함한다).
		if err := t.FinishItem(in.Project, in.ItemID, in.SessionID, in.Outcome, in.CloseReason); err != nil {
			return err
		}

		// ④ 이 세션이 쥔 자원 반납. 규율 산문이 강제하던 마지막 단계다.
		//
		// ★ holds 를 **트랜잭션 안에서** 읽는다. 밖에서 읽으면 그 사이에 사람이 레인을
		//   강제 회수했을 때 아래 반납이 ErrNotFound 를 올리고, 그 오류가 ①②③ 을 통째로
		//   롤백시켜 **원리적으로 파생 불가한 유일한 자산인 판단이 사라진다.**
		holds, err := t.ListHeld(in.Project)
		if err != nil {
			return err
		}
		for _, h := range holds {
			if h.SessionID != in.SessionID {
				continue
			}
			if err := t.ReleaseResource(in.Project, h.Resource, store.Holder{SessionID: in.SessionID}); err != nil {
				var held *store.ResourceHeldError
				if errors.Is(err, store.ErrNotFound) || errors.As(err, &held) {
					// ★ 남이 이미 반납했거나 강제로 회수했다. 그것은 finish 를 실패시킬
					//   이유가 아니다 — 원장에만 남기고 Released 에서 뺀다.
					//
					// ★ **개정 — 인용이 두 갈래 중 하나에서만 맞았다.** 원래 이 자리는 위 두 줄
					//   뒤에 "(item.go 의 ReleaseClaim 과 같은 규율)" 한 마디로 두 갈래를 함께
					//   인용했다. 갈래마다 선례가 갈린다:
					//
					//   ⓐ ErrNotFound — 선례가 **맞다.** 여기서 뜨는 ErrNotFound 는 바로 위
					//     ListHeld 가 살아 있는 행으로 내준 뒤 사라진 것이므로 "이미 반납됐다"
					//     하나뿐이고, ReleaseClaim 도 그 상황을 `return nil` 로 삼킨다(item.go 의
					//     "이미 반납됐다. 멱등하게 통과시킨다"). 계층만 다르다 — 그쪽은 저장층
					//     안에서 삼키고, 이쪽은 heldBy 가 `released_at IS NULL` 만 보므로 같은
					//     상황이 ErrNotFound 로 올라와 호출자인 여기가 삼킨다.
					//     같은 문장이 service/landing.go 의 이탈 갈래에도 있는데 **그쪽은 옳다** —
					//     거기 갈래는 이 ⓐ 하나뿐이라 통째로 인용해도 어긋날 데가 없다.
					//
					//   ⓑ ResourceHeldError — 선례가 **정반대다.** ReleaseClaim 은 바로 그 상황
					//     (남이 쥐고 있다)에서 ClaimHeldError 를 **올린다.** 이 흡수는 선례를 못
					//     빌리고, 빌리지 않고 서는 근거가 셋이다:
					//     · 배타를 하나도 안 약화시킨다 — ReleaseResource 는 점유자가 다르면
					//       UPDATE 를 아예 안 친다(store/resource.go). 남의 점유는 그대로 남는다.
					//     · 여기는 **지목 반납이 아니라 쓸어담기다.** ReleaseClaim 은 호출자가
					//       항목을 지목해 명령한 자리라 "그건 남의 것이다"를 반드시 알려야 하지만,
					//       이 루프는 ListHeld 를 훑어 자기 것만 걷는 청소라 걷을 것이 이미
					//       사라졌다는 사실이 명령 실패가 아니다. 같은 함수의 ③(FinishItem)이
					//       ClaimHeldError 를 그대로 올리는 것(TestFinishRefusesSomeoneElsesItem)과
					//       여기가 갈리는 이유가 그 구분이다.
					//     · 롤백 대가가 비대칭이다 — 오류를 올리면 ① 의 판단이 함께 사라지고,
					//       넷 중 그것만이 원리적으로 파생 불가하다.
					//
					// ★ **두 갈래 다 지금은 안 밟힌다. 단 그것은 바로 위 전제에 달려 있다** —
					//   holds 를 트랜잭션 **안에서** 읽는 한 ListHeld 와 ReleaseResource 사이에
					//   남이 못 끼어들어 어느 쪽도 안 뜬다. 그 읽기를 트랜잭션 밖으로 되돌리면
					//   둘 다 살아나므로 이 분기는 지우지 않는다. 전제를 잠근 것은
					//   TestFinishSurvivesAForcedReleaseRacingIt 이고, 그 시험이 단정하는 값이
					//   바로 아래 lane.release_skipped 가 **0건**이라는 것이다.
					//
					// ★ 아래 사유 문자열은 두 갈래를 한 문장으로 적는다. **일부러 안 갈랐다** —
					//   가르려면 코드가 늘고, 늘어난 그 코드를 밟는 시험은 위 전제 때문에 쓸 수 없다.
					t.LogEvent("lane.release_skipped", in.Project, in.SessionID,
						map[string]any{"resource": h.Resource, "why": "이미 반납되었거나 남이 회수했다"})
					continue
				}
				return fmt.Errorf("자원 %s 반납 실패: %w", clip(h.Resource, 64), err)
			}
			out.Released = append(out.Released, h.Resource)
		}

		// ★ 줄 행 닫기는 **반납 루프 밖에서 조건 없이** 한다. 루프 안에 두면 레인을 안 쥔 채
		//   줄만 서 있던 세션(대기 중 마무리)의 유령 행이 안 닫힌다. 살아 있는 행이 없으면
		//   무동작으로 통과하므로 줄을 한 번도 안 선 세션에도 안전하다.
		if err := t.CloseLandingRowBySession(
			in.Project, in.SessionID, model.LandingLeftFinish, ""); err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		s.logFail(ctx, "item.finish", in.Project, in.SessionID, err)
		s.log.ErrorContext(ctx, "마무리 실패",
			"project", clip(in.Project, 64), "session_id", clip(in.SessionID, 64),
			"item", clip(in.ItemID, 64), "error", err.Error())
		return FinishResult{}, err
	}

	// ★ 트랜잭션이 커밋된 **바로 다음**에 부른다. 이 자리 뒤의 GetItem 이 실패해도
	// handoff 판단은 이미 남았으므로 열린 처방은 닫혀야 한다 — Note(finish.go 아래)와
	// 같은 규율이다: 커밋됐으면 그 뒤 무엇이 실패하든 ack 은 반드시 시도한다.
	s.ackPrescriptions(ctx, in.Project, in.SessionID)

	// ★ 남은 선점을 **커밋 뒤에** 읽는다. 앞에서 읽으면 방금 닫은 항목이 아직 반납
	// 전이라 목록에 들어오고, 그러면 응답이 "아직 쥐고 있다"고 말하는 항목 중 하나가
	// 바로 이 호출이 닫은 것이 된다.
	//
	// **실패해도 finish 를 실패시키지 않는다** — 트랜잭션은 이미 커밋됐고, 표시용
	// 목록 하나 때문에 "판단은 저장됐는데 오류가 났다"는 응답을 내면 세션이 같은
	// finish 를 다시 부른다. 대신 nil 로 두어 렌더가 "못 읽었다"고 정확히 말한다 —
	// 빈 목록으로 접으면 관측한 적 없는 "남은 선점 0건"을 단정하게 된다.
	//
	// derive 에 안 넣는 이유는 fillQueueOpen 과 같다: FreshnessOf 가 failures>0 을
	// **git 축** Stale 로 접어서, DB 조회 한 번이 실패했을 뿐인데 브랜치·조상 판정이
	// 낡았다고 읽히게 된다.
	if held, herr := s.st.ClaimedItems(ctx, in.SessionID); herr != nil {
		s.log.WarnContext(ctx, "마무리 뒤 남은 선점 조회 실패 — 응답은 그 축을 안 낸다",
			"project", clip(in.Project, 64), "session_id", clip(in.SessionID, 64),
			"error", herr.Error())
	} else {
		rest := make([]string, 0, len(held))
		for _, id := range held {
			if id == in.ItemID {
				continue // 방금 닫은 것. FinishItem 이 반납했으면 애초에 안 오지만, 두 번 세지 않는다
			}
			rest = append(rest, id)
		}
		out.StillHeld = &rest
	}

	// ★ 큐 수지도 **커밋 뒤에** 읽는다 — 방금 만든 후속이 열린 목록에 들어와야
	// "이 마무리 직후의 큐"가 된다. 실패하면 nil 이고, 렌더가 "못 읽었다"를 말한다.
	out.QueueBalance = s.queueBalance(ctx, in.Project, len(out.Followups), now)

	item, err := s.st.GetItem(ctx, in.Project, in.ItemID)
	if err != nil {
		return FinishResult{}, err
	}
	out.Item = item
	out.Derived = (&derive{}).result(now) // 마무리에는 git 파생이 없다. 그 사실도 사유로 남는다
	s.log.InfoContext(ctx, "마무리",
		"project", in.Project, "session_id", in.SessionID, "item", in.ItemID,
		"mode", string(in.Outcome), "count", len(out.Followups), "released", len(out.Released))
	return out, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// 판단 · 발번
// ─────────────────────────────────────────────────────────────────────────────

// NoteInput 은 판단 하나를 남기는 인자다.
type NoteInput struct {
	Project    string
	SessionID  string
	Kind       model.JudgmentKind
	Title      string
	Body       string
	ItemID     string // 있으면 항목에 링크한다
	Supersedes string // 정정은 새 행 + supersedes. **덮어쓰기는 없다**
	Links      []model.JudgmentLink
}

// NoteResult 는 저장 확인과 이 노트를 받을 세션이다.
type NoteResult struct {
	Judgment   model.Judgment `json:"judgment"`
	Recipients []string       `json:"recipients"` // 지금 살아 있는 다른 세션들
}

// ValidateNoteKind 는 판단 종류가 열거에 있는지 본다. 순수 함수다.
func ValidateNoteKind(k model.JudgmentKind) error {
	switch k {
	case model.JudgmentHandoff, model.JudgmentDecision, model.JudgmentBlocked,
		model.JudgmentAsk, model.JudgmentNow, model.JudgmentRejected,
		model.JudgmentNotDone, model.JudgmentVerified, model.JudgmentDraft:
		return nil
	case "":
		return fmt.Errorf("판단 종류가 비었다 — handoff|decision|blocked|ask|now|rejected|not-done|verified|draft 중 하나여야 한다")
	default:
		return fmt.Errorf("판단 종류 %q 가 열거에 없다 — "+
			"handoff|decision|blocked|ask|now|rejected|not-done|verified|draft 중 하나여야 한다",
			clip(string(k), 32))
	}
}

// Recipients 는 이 노트를 받을 세션들이다. 순수 함수다.
//
// 자기 자신은 뺀다 — 자기가 쓴 것을 자기가 받는다고 세면 그 숫자가 항상 1 이상이 되어
// "아무도 안 보고 있다"는 사실이 화면에서 사라진다.
func Recipients(cards []SessionCard, self string) []string {
	var out []string
	for _, c := range cards {
		if c.View.Session.ID == self {
			continue
		}
		out = append(out, c.View.Session.ID)
	}
	return out
}

// Note 는 판단 하나를 남긴다. **추가 전용이다** — 정정은 새 행 + Supersedes 다.
func (s *Service) Note(ctx context.Context, in NoteInput) (NoteResult, error) {
	if err := ValidateNoteKind(in.Kind); err != nil {
		return NoteResult{}, &RefusedError{What: "note", Reason: err.Error()}
	}
	if strings.TrimSpace(in.Body) == "" {
		return NoteResult{}, &RefusedError{What: "note",
			Reason:   "판단 본문이 비었다",
			Guidance: "무엇을 왜 그렇게 했는지가 이 표의 존재 이유다 — 한 줄이라도 남겨라."}
	}
	now := s.now()

	links := in.Links
	if strings.TrimSpace(in.ItemID) != "" {
		links = append(links, model.JudgmentLink{TargetKind: "item", TargetID: in.ItemID})
	}
	if strings.TrimSpace(in.SessionID) != "" {
		links = append(links, model.JudgmentLink{TargetKind: "session", TargetID: in.SessionID})
	}

	var j model.Judgment
	err := s.st.Tx(ctx, func(t *store.Tx) error {
		t.LogEvent("judgment.note", in.Project, in.SessionID, map[string]any{
			"mode": string(in.Kind), "bytes": len(in.Body), "item": clip(in.ItemID, 64),
		})
		var err error
		j, err = t.AddJudgment(model.Judgment{
			Project: in.Project, SessionID: in.SessionID, At: now,
			Kind: in.Kind, Title: in.Title, Body: in.Body,
			Supersedes: in.Supersedes, Links: links,
		})
		if err != nil {
			return err
		}
		if in.SessionID != "" {
			if err := t.Beat(in.SessionID, model.SignalMCP, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.logFail(ctx, "judgment.note", in.Project, in.SessionID, err)
		s.log.ErrorContext(ctx, "판단 저장 실패",
			"project", clip(in.Project, 64), "session_id", clip(in.SessionID, 64),
			"mode", string(in.Kind), "error", err.Error())
		return NoteResult{}, err
	}

	// 처방을 받고 판단을 남겼다 — 열린 처방을 닫는다. 실패해도 판단은 이미 저장됐다.
	//
	// ★ **수신자 파생보다 먼저 부른다.** 아래 sessionCards 가 실패하면 이 함수는
	// 그 자리에서 바로 반환한다 — 그 반환 뒤에 ack 을 두면, 판단은 트랜잭션에서
	// 이미 커밋됐는데 파생 실패 하나 때문에 처방이 영영 안 닫히는 반쪽 상태가 생긴다.
	// 커밋 여부만이 ack 을 가르는 조건이어야 하고, 그 뒤의 다른 축 실패는 아니다.
	s.ackPrescriptions(ctx, in.Project, in.SessionID)

	// 받을 세션은 조정 정보다. 파생 실패로 접지 않고 그대로 올린다.
	d := &derive{}
	proj, perr := s.st.GetProject(ctx, in.Project)
	var recipients []string
	if perr == nil {
		cards, err := s.sessionCards(ctx, proj, s.cut(now, 0), in.SessionID, d)
		if err != nil {
			return NoteResult{}, err
		}
		recipients = Recipients(cards, in.SessionID)
	}
	s.log.InfoContext(ctx, "판단 저장",
		"project", in.Project, "session_id", in.SessionID, "mode", string(in.Kind),
		"count", len(recipients), "bytes", len(in.Body))

	return NoteResult{Judgment: j, Recipients: recipients}, nil
}

// Alloc 은 논리 카운터의 다음 번호를 발급한다.
//
// ★ 락이 원리적으로 못 지키는 자리다 — 파일 접근을 직렬화해도
// 두 세션이 각자 "지금 값"을 읽고 각자 +1 하면 둘 다 같은 번호를 쓴다.
// 그래서 이 계층은 값을 읽어 더하지 않고 store 의 원자 발번을 그대로 부른다.
func (s *Service) Alloc(ctx context.Context, project, counter string) (int64, error) {
	if strings.TrimSpace(project) == "" {
		return 0, &RefusedError{What: "alloc", Reason: "project 가 비었다"}
	}
	if strings.TrimSpace(counter) == "" {
		return 0, &RefusedError{What: "alloc", Reason: "카운터 이름이 비었다"}
	}
	n, err := s.st.NextCounter(ctx, project, counter)
	if err != nil {
		s.log.ErrorContext(ctx, "발번 실패",
			"project", clip(project, 64), "mode", clip(counter, 64), "error", err.Error())
		return 0, err
	}
	s.st.LogEvent(ctx, "counter.alloc", project, "", map[string]any{
		"mode": clip(counter, 64), "count": n,
	})
	s.log.InfoContext(ctx, "발번", "project", project, "mode", clip(counter, 64), "count", n)
	return n, nil
}
