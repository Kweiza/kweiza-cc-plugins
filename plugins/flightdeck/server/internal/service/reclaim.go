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
	Holder     string `json:"holder"` // 회수 당시 점유 세션. 조회 실패면 그 사실이 그대로 적힌다
	JudgmentID string `json:"judgment_id"`
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

	// 점유자·선점 시각·마지막 신호는 트랜잭션 **밖에서** 먼저 읽는다(ReleaseLaneRow 와
	// 같은 이유 — 판단 본문에 적을 관측이지 권위 판정이 아니다. "지금도 살아 있는
	// 선점인가"의 권위는 아래 ForceReleaseClaim 의 RowsAffected 가 쥔다).
	//
	// 셋을 **다른 문장으로** 적는다. 판단은 불변 기록이라, 못 읽은 것을 "없다"로
	// 적으면 그 자리에 거짓 사실이 영구히 박힌다.
	holder := ""
	holderLine := "점유자: 조회 실패 — 아래 회수가 성립했다면 선점 자체는 있었던 것이다"
	claimedLine := "선점 시각: 관측하지 못했다"
	signalLine := "마지막 신호: 관측하지 못했다(점유자를 못 읽어 신호도 못 찾았다)"
	if c, err := s.st.GetClaim(ctx, project, itemID); err == nil {
		holder = c.SessionID
		holderLine = "점유자: " + holder
		claimedLine = fmt.Sprintf("선점 시각: %s (나이 %s)",
			c.At.Format(time.RFC3339), now.Sub(c.At).Round(time.Second))
		at, observed := s.lastSignal(ctx, holder)
		switch {
		case !observed:
			signalLine = "마지막 신호: 읽지 못했다(신호 조회가 실패했다 — 원인은 서버 로그의 WARN 에 있다). " +
				"**이 회수는 신호 나이를 보지 않고 한 것이다.**"
		case at == nil:
			signalLine = "마지막 신호: 없음(이 세션은 신호를 한 번도 안 남겼다)"
		default:
			signalLine = fmt.Sprintf("마지막 신호: %s (나이 %s)",
				at.Format(time.RFC3339), now.Sub(*at).Round(time.Second))
		}
	} else {
		holderLine = "점유자: 조회 실패: " + clip(err.Error(), 200)
	}

	// 행위자 문장은 레인 회수(landing.go 의 releaseBody)와 같은 두 갈래다 —
	// 빈 actor 는 대시보드 폼이고, 채워진 actor 는 CLI 가 셸 좌표로 채운 것이다.
	// 갈래를 안 가르면 셸에서 부른 회수가 "대시보드가 눌렀다"로 영구히 남는다.
	actorLine := "행위자: 대시보드(사람). 세션이 아니라 사람이 누른 것이므로 session_id 는 비어 있다."
	if strings.TrimSpace(actor) != "" {
		actorLine = fmt.Sprintf("행위자: %s. 세션 id 인지 사람 이름인지 서버는 구분하지 않으므로 "+
			"judgment.session_id 는 비운다.", actor)
	}
	body := fmt.Sprintf("사람이 선점을 회수했다.\n항목: %s\n%s\n%s\n%s\n사유: %s\n%s\n"+
		"★ 회수는 자동 만료가 아니다 — 사람이 위 관측을 보고 판정한 것이다.",
		itemID, holderLine, claimedLine, signalLine, reason, actorLine)

	var out ClaimReclaimResult
	err := s.st.Tx(ctx, func(t *store.Tx) error {
		// 시도를 **먼저** 예약한다 — 롤백돼도 남는다. 끝에 두면 성공한 것만 세게 되고,
		// "회수가 안 되는데 아무도 모른다"가 다음 조사에서 안 보인다.
		t.LogEvent("claim.reclaim", project, "", map[string]any{
			"item": itemID, "holder": clip(holder, 64), "actor": clip(actor, 64), "bytes": len(reason),
		})
		if err := t.ForceReleaseClaim(project, itemID, reason); err != nil {
			return err
		}
		j, err := t.AddJudgment(model.Judgment{
			Project: project, Kind: model.JudgmentDecision,
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
		return ClaimReclaimResult{}, err
	}
	s.log.InfoContext(ctx, "선점 회수",
		"project", clip(project, 64), "item", clip(itemID, 64),
		"holder", clip(holder, 64), "actor", clip(actor, 64))
	return out, nil
}
