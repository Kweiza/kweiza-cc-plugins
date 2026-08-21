package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// 선점 회수 — 사람이 죽은 선점을 푸는 유일한 합법 경로.
//
// claim 에는 만료가 없다(schema.sql 의 claim 주석 — 생존 오판 실측 2회로 자동 회수를
// 의식적으로 기각했다). 그 트레이드오프의 비용이 2026-08-07 실측으로 드러났다:
// 무신호 9~24.5시간 세션 4곳이 claimed 12건(실질 잔량의 9~27%)을 쥔 채 침묵했고,
// 사람이 풀 표면은 대시보드 폼 하나뿐이었다. 이 파일은 그 로직을 service 로 올려
// 대시보드·REST·CLI 가 **같은 함수**를 부르게 한다 — web 과 CLI 가 ReleaseLaneRow
// 하나를 부르는 레인 회수와 같은 형태다.

// ClaimReclaimResult 는 사람이 한 선점 회수의 결과다.
type ClaimReclaimResult struct {
	Item       string `json:"item"`
	Holder     string `json:"holder"` // 회수된 선점의 점유 세션(트랜잭션 안에서 확정한 정체)
	JudgmentID string `json:"judgment_id"`
}

// judgeReclaimIdentity 는 "사람이 보고 판정한 그 선점이 지금도 그 선점인가"다. 순수 함수다.
//
// observed 는 회수 직전(트랜잭션 밖)에 관측한 점유자, live 는 트랜잭션 안에서 확정한
// 점유자다. 둘이 다르면 그 사이에 반납·재선점이 끼어든 것이고, 강행하면 **낡은 관측을
// 사유로 산 세션의 선점을 끊는다** — 항목 id 는 선점 인스턴스를 고정하지 못한다
// (claim 은 (project, item) 업서트 한 행이다). 레인 회수에서 줄 행 번호가 막는 창을
// 여기서는 이 대조가 막는다.
func judgeReclaimIdentity(observed, live string) *RefusedError {
	if observed == live {
		return nil
	}
	obs := observed
	if obs == "" {
		obs = "(관측 실패 또는 선점 없음)"
	}
	return &RefusedError{What: "claim release",
		Reason: fmt.Sprintf("점유자가 회수 직전에 바뀌었다 — 관측 %s, 지금 %s", obs, live),
		Guidance: "지금 점유자는 방금 선점한 산 세션일 수 있다. " +
			"보드를 다시 보고 판정해라 — 같은 판정이면 재실행이 통과한다."}
}

// signalObservation 은 판단 본문의 "마지막 신호" 문장이다. 순수 함수다.
//
// 셋을 **다른 문장으로** 적는다. 판단은 불변 기록이라, 못 읽은 것을 "없다"로 적으면
// 그 자리에 거짓 사실이 영구히 박힌다.
func signalObservation(at *time.Time, observed bool, now time.Time) string {
	switch {
	case !observed:
		return "마지막 신호: 읽지 못했다(신호 조회가 실패했다 — 원인은 서버 로그의 WARN 에 있다). " +
			"**이 회수는 신호 나이를 보지 않고 한 것이다.**"
	case at == nil:
		return "마지막 신호: 없음(이 세션은 신호를 한 번도 안 남겼다)"
	default:
		return fmt.Sprintf("마지막 신호: %s (나이 %s)",
			at.Format(time.RFC3339), now.Sub(*at).Round(time.Second))
	}
}

// ReclaimClaim 은 남의 선점을 회수한다. **사유가 필수고, 회수 행위 자체가
// judgment(kind='decision') 으로 남는다** — 사유가 원장에 안 남는 회수는
// 나중에 "왜 그 세션의 작업이 사라졌나"에 답할 수 없다.
//
// 세션을 요구하지 않는다(레인 회수와 같은 판정) — 회수하는 사람은 대개 그 세션이
// 아니고, 그 세션은 이미 죽었다. MCP 의 pick 이 steal_reason 을 거절하는 것과 한 쌍이다:
// 회수는 세션의 도구가 아니라 사람의 표면이다.
func (s *Service) ReclaimClaim(ctx context.Context, project, itemID, actor, reason string) (ClaimReclaimResult, error) {
	if strings.TrimSpace(project) == "" {
		return ClaimReclaimResult{}, &RefusedError{What: "claim release", Reason: "project 가 비었다"}
	}
	if strings.TrimSpace(itemID) == "" {
		return ClaimReclaimResult{}, &RefusedError{What: "claim release",
			Reason:   "회수할 항목 id 가 비었다",
			Guidance: "잡힌 항목은 보드의 세션 카드와 창 밖 선점 줄이 낸다."}
	}
	if strings.TrimSpace(reason) == "" {
		return ClaimReclaimResult{}, &RefusedError{What: "claim release",
			Reason:   "회수 사유가 비었다",
			Guidance: "사유가 원장에 안 남는 회수는 나중에 되짚을 수 없다 — 신호 나이·발자국 등 무엇을 보고 회수하는지를 적어라."}
	}
	now := s.now()

	// 관측(트랜잭션 밖): 점유자와 그 신호. **정체의 권위가 아니다** — 권위는 아래
	// 트랜잭션 안의 LiveClaim 이다. 이 관측은 (a) 판단 본문에 적을 신호 나이와
	// (b) 정체 대조(judgeReclaimIdentity)의 관측 축으로만 쓴다. 신호 표를 트랜잭션
	// 안에서 읽지 않는 이유는 레인 회수와 같다 — 쓰기 잠금을 쥔 채 커넥션 풀을
	// 기다리면 그 대기가 다른 쓰기 전부를 세운다.
	//
	// 반납된 행은 관측으로 안 친다(GetClaim 은 이력도 내므로 ReleasedAt 을 가른다) —
	// 옛 점유자를 관측이라고 적으면 "아무도 안 쥔 항목"의 기록이 옛 세션의 것으로 읽힌다.
	obsHolder := ""
	obsSignal := ""
	if c, err := s.st.GetClaim(ctx, project, itemID); err == nil && c.ReleasedAt == nil {
		obsHolder = c.SessionID
		at, observed := s.lastSignal(ctx, obsHolder)
		obsSignal = signalObservation(at, observed, now)
	}

	var out ClaimReclaimResult
	err := s.st.Tx(ctx, func(t *store.Tx) error {
		// 정체는 **트랜잭션 안에서** 확정한다. BEGIN IMMEDIATE 라 이 읽기와 아래
		// 회수 사이에 다른 쓰기가 끼어들 수 없다 — 밖에서 읽은 점유자는 커밋까지
		// busy_timeout 만큼 낡을 수 있어 정체의 근거가 못 된다.
		live, lerr := t.LiveClaim(project, itemID)
		holder := ""
		if lerr == nil {
			holder = live.SessionID
		}
		// 시도를 회수보다 먼저 예약한다 — 롤백돼도 남는다(성공만 세면 "회수가 안 되는데
		// 아무도 모른다"가 다음 조사에서 안 보인다). holder 는 트랜잭션 안 정체다:
		// 살아 있는 선점이 없으면 **빈 값** — 반납된 옛 점유자를 실으면 실패 시도가
		// 옛 세션의 것으로 읽힌다.
		t.LogEvent("claim.reclaim", project, "", map[string]any{
			"item": itemID, "holder": clip(holder, 64), "actor": clip(actor, 64), "bytes": len(reason),
		})
		if lerr != nil {
			return lerr
		}
		if refuse := judgeReclaimIdentity(obsHolder, holder); refuse != nil {
			return refuse
		}
		if err := t.ForceReleaseClaim(project, itemID, reason); err != nil {
			return err
		}

		// 행위자 문장은 레인 회수(landing.go 의 releaseBody)와 같은 두 갈래다 —
		// 빈 actor 는 대시보드 폼이고, 채워진 actor 는 CLI 가 셸 좌표로 채운 것이다.
		// 갈래를 안 가르면 셸에서 부른 회수가 "대시보드가 눌렀다"로 영구히 남는다.
		actorLine := "행위자: 대시보드(사람). 세션이 아니라 사람이 누른 것이므로 session_id 는 비어 있다."
		if strings.TrimSpace(actor) != "" {
			actorLine = fmt.Sprintf("행위자: %s. 세션 id 인지 사람 이름인지 서버는 구분하지 않으므로 "+
				"judgment.session_id 는 비운다.", actor)
		}
		// 정체(점유자·선점 시각)는 트랜잭션 안 값으로 적는다 — 위 대조를 지나
		// obsSignal 의 신호 관측도 같은 세션의 것임이 확정된 상태다.
		body := fmt.Sprintf("사람이 선점을 회수했다.\n항목: %s\n점유자: %s\n선점 시각: %s (나이 %s)\n%s\n사유: %s\n%s\n"+
			"★ 회수는 자동 만료가 아니다 — 사람이 위 관측을 보고 판정한 것이다.",
			itemID, holder, live.At.Format(time.RFC3339), now.Sub(live.At).Round(time.Second),
			obsSignal, reason, actorLine)

		j, err := t.AddJudgment(model.Judgment{
			Project: project, Kind: model.JudgmentDecision, At: now,
			Title: "선점 회수: " + itemID, Body: body,
			Links: []model.JudgmentLink{{TargetKind: "item", TargetID: itemID}},
		})
		if err != nil {
			return err
		}
		out = ClaimReclaimResult{Item: itemID, Holder: holder, JudgmentID: j.ID}
		return nil
	})
	if err != nil {
		// 실패도 원장에 남긴다(레인 회수와 같은 결선) — claim.reclaim 시도 이벤트만으로는
		// "왜 안 됐나"가 원장에 없다.
		s.logFail(ctx, "claim.reclaim", project, "", err, failAbout{Item: itemID})
		s.log.ErrorContext(ctx, "선점 회수 실패",
			"project", clip(project, 64), "item", clip(itemID, 64), "error", err.Error())
		return ClaimReclaimResult{}, err
	}
	s.log.InfoContext(ctx, "선점 회수",
		"project", clip(project, 64), "item", clip(itemID, 64),
		"holder", clip(out.Holder, 64), "actor", clip(actor, 64))
	return out, nil
}

// LeaveInput 은 자기 선점 반납의 입력이다. Backend 이음매가 이 꼴을 요구한다
// (in-process 서비스와 cmd/fd 프록시가 같은 서명을 만족해야 한다).
//
// ItemID 는 **선택**이다 — 비면 이 세션이 쥔 전부가 대상이다. Reason 은 필수다.
type LeaveInput struct {
	Project   string `json:"project"`
	SessionID string `json:"session_id"`
	ItemID    string `json:"item_id"`
	Reason    string `json:"reason"`
}

// ClaimLeaveResult 는 **산 세션이 자기 선점을 놓은** 결과다. 회수(ClaimReclaimResult)와
// 다른 타입인 이유는 축이 다르기 때문이다 — 회수는 항목 하나에 점유자 하나이고,
// 반납은 이 세션이 쥔 것 **전부**가 대상일 수 있다(묶음 선점은 함께 집히므로 함께 놓인다).
type ClaimLeaveResult struct {
	Items      []string `json:"items"`
	Session    string   `json:"session"`
	JudgmentID string   `json:"judgment_id"`
}

// LeaveClaim 은 살아 있는 세션이 **자기** 선점을 놓는다. 회수가 아니다.
//
// ★ **ReclaimClaim 과 섞지 않는 이유.** 겉보기에 둘 다 "선점을 푼다"이지만 판정이 반대다:
//
//	회수(ReclaimClaim)  3자가 **죽음을 판정하고** 한다. 그래서 세션을 요구하지 않고,
//	                    judgeReclaimIdentity 가 "관측한 점유자 == 지금 점유자"를 방벽으로 세우며,
//	                    판단 본문이 **무엇을 관측했나**(신호 나이)를 적는다.
//	반납(LeaveClaim)    본인이 한다. 판정할 것이 없다 — 본인이 안다. 방벽은 반대 방향이다:
//	                    **점유자 != 나면 거절**한다. 판단 본문이 적을 것은 **왜 안 했나**다.
//
// 두 뜻을 한 함수에 섞으면 "회수"라는 말이 무엇을 뜻하는지가 흐려지고, pick 이 steal_reason 을
// 거절해 좁게 잠가 둔 축(회수는 세션의 도구가 아니라 사람의 표면이다)이 함께 풀린다.
//
// itemID 가 비면 이 세션이 쥔 **전부**를 놓는다 — 묶음은 함께 집히므로 함께 놓이는 것이 대칭이다.
// 채우면 그 하나만 놓는다.
func (s *Service) LeaveClaim(ctx context.Context, in LeaveInput) (ClaimLeaveResult, error) {
	project, sessionID, itemID, reason := in.Project, in.SessionID, in.ItemID, in.Reason
	if strings.TrimSpace(project) == "" {
		return ClaimLeaveResult{}, &RefusedError{What: "claim leave", Reason: "project 가 비었다"}
	}
	if strings.TrimSpace(sessionID) == "" {
		// ★ 회수와 정반대다. 회수는 세션을 요구하면 탈출구가 막히지만, 반납은 **누구 것을
		//   놓는가**가 세션으로만 정해진다 — 세션이 없으면 이건 반납이 아니라 회수다.
		return ClaimLeaveResult{}, &RefusedError{What: "claim leave",
			Reason:   "세션이 비었다 — 반납은 자기 선점에만 쓴다",
			Guidance: "남의 선점을 푸는 것은 회수다: `fd claim release --item <id> --reason \"...\"`."}
	}
	if strings.TrimSpace(reason) == "" {
		// landing_queue 의 left_detail CHECK 와 같은 규율이다 — 사유 없는 이탈은 되짚을 수 없다.
		return ClaimLeaveResult{}, &RefusedError{What: "claim leave",
			Reason:   "반납 사유가 비었다",
			Guidance: "왜 안 했는지를 적어라 — 기한 미충족·막힘·판정 변경 중 무엇인가. 이 문장이 다음 사람의 유일한 단서다."}
	}

	// 대상 후보는 트랜잭션 **밖**에서 읽는다(회수가 신호를 밖에서 읽는 것과 같은 이유 —
	// 쓰기 잠금을 쥔 채 커넥션 풀을 기다리면 그 대기가 다른 쓰기 전부를 세운다).
	// 권위는 아래 트랜잭션 안의 LiveClaim 이다: 후보가 낡았으면 거기서 걸린다.
	targets := []string{strings.TrimSpace(itemID)}
	if targets[0] == "" {
		mine, err := s.st.ClaimedItems(ctx, sessionID)
		if err != nil {
			return ClaimLeaveResult{}, err
		}
		if len(mine) == 0 {
			return ClaimLeaveResult{}, &RefusedError{What: "claim leave",
				Reason:   "이 세션이 쥔 선점이 없다",
				Guidance: "이미 놓았거나 애초에 안 집은 것이다. 보드가 지금 무엇을 쥐고 있는지 낸다."}
		}
		targets = mine
	}

	now := s.now()
	var out ClaimLeaveResult
	err := s.st.Tx(ctx, func(t *store.Tx) error {
		// 시도를 반납보다 먼저 예약한다 — 롤백돼도 남는다(회수와 같은 결선).
		t.LogEvent("claim.leave", project, sessionID, map[string]any{
			"items": targets, "bytes": len(reason),
		})
		for _, id := range targets {
			live, lerr := t.LiveClaim(project, id)
			if lerr != nil {
				return lerr
			}
			// ★ 방벽이 회수와 **반대 방향**이다: 남의 것이면 거절하고 회수 표면으로 보낸다.
			//   이걸 빼면 반납이 조용한 회수가 되고, steal_reason 을 거절해 좁게 잠근 축이 함께 풀린다.
			if live.SessionID != sessionID {
				return &RefusedError{What: "claim leave",
					Reason: fmt.Sprintf("%s 는 내 선점이 아니다 — 세션 %s 가 쥐고 있다", id, clip(live.SessionID, 64)),
					Guidance: "남의 선점을 푸는 것은 회수다. 사람이 신호 나이를 보고 판정해라: " +
						"`fd claim release --item " + clip(id, 64) + " --reason \"...\"`."}
			}
			if err := t.ForceReleaseClaim(project, id, reason); err != nil {
				return err
			}
		}

		// ★ kind 가 not-done 이다(회수의 decision 이 아니다). 이 판단이 답하는 질문은
		//   "왜 그 세션의 작업이 사라졌나"가 아니라 **"왜 안 했나"**다.
		body := fmt.Sprintf("세션이 자기 선점을 놓았다.\n항목: %s\n세션: %s\n시각: %s\n사유: %s\n"+
			"★ 항목은 open 으로 돌아갔다 — id·이력·`after` 참조가 그대로 산다. 다음 pick 이 집을 수 있다.\n"+
			"★ 이것은 회수가 아니다. 본인이 놓은 것이라 죽음을 판정한 사람이 없고, 신호 나이 관측도 없다.",
			strings.Join(targets, ", "), sessionID, now.Format(time.RFC3339), reason)

		links := make([]model.JudgmentLink, 0, len(targets))
		for _, id := range targets {
			links = append(links, model.JudgmentLink{TargetKind: "item", TargetID: id})
		}
		j, err := t.AddJudgment(model.Judgment{
			Project: project, SessionID: sessionID, Kind: model.JudgmentNotDone, At: now,
			Title: leaveTitle(targets), Body: body,
			Links: links,
		})
		if err != nil {
			return err
		}
		out = ClaimLeaveResult{Items: targets, Session: sessionID, JudgmentID: j.ID}
		return nil
	})
	if err != nil {
		s.logFail(ctx, "claim.leave", project, sessionID, err, failAbout{Item: strings.Join(targets, ",")})
		s.log.ErrorContext(ctx, "선점 반납 실패",
			"project", clip(project, 64), "session", clip(sessionID, 64), "error", err.Error())
		return ClaimLeaveResult{}, err
	}
	s.log.InfoContext(ctx, "선점 반납",
		"project", clip(project, 64), "session", clip(sessionID, 64), "items", len(out.Items))
	return out, nil
}

// leaveTitle 은 반납 판단의 제목이다 — **개수를 앞에 둔다.**
//
// ★ 실물 관측이 만든 자리다(2026-08-21, 항목 `fd-leave-bundle-n2-observation-…`).
// 원장의 `claim.leave` 48건 중 n>=2 는 8건이고 그중 셋이 제목 128자에서 잘렸다
// (n=7 · n=7 · n=4). 잘린 제목은 앞 서넛만 보여서 **몇 개를 놓았는지가 사라진다** —
// 보드와 pick 의 「연결된 판단」은 이 한 줄만 보므로, 읽는 사람은 하나가 더 있는지 넷이
// 더 있는지를 못 가른다. 목록은 잘려도 되지만 수는 안 된다. 그래서 수가 clip 의
// **바깥**, 그것도 앞에 선다.
//
// ★ 단건은 모양을 안 바꾼다. 원장에 쌓인 41건과 갈리고, 하나뿐이라는 것은 목록이 이미 말한다.
func leaveTitle(targets []string) string {
	head := "선점 반납: "
	if len(targets) > 1 {
		head = fmt.Sprintf("선점 반납 %d건: ", len(targets))
	}
	return head + clip(strings.Join(targets, ", "), 120)
}
