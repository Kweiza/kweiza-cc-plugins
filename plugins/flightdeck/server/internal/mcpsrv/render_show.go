package mcpsrv

import (
	"fmt"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// RenderShow 는 항목 하나의 이력이다. 순수 함수다.
//
// ★ **이 동사의 규율은 전부 여기 있다.** 도구 설명(90자)에도 스킬에도 안 넣는다 —
// RenderAmend·RenderLabel 과 같은 자리, 같은 이유다(설계 §6: 규율은 응답에 싣는다).
// 여기 실리는 것 셋: ⓐ 지금 본문 ⓑ 개정 이력(rev 순 · 사유 · 바뀐 축) ⓒ 걸린 판단 전문.
//
// ★ **세 절 어디에서도 침묵하지 않는다.** 0건과 "못 읽었다"는 다른 문장이어야 한다 —
// 못 읽었는데 빈 목록만 내면 되짚는 사람이 "앞선 판단이 없다"고 믿고 이미 기각된 길을
// 다시 간다. 이 표면이 존재하는 이유가 정확히 그것을 막는 것이다.

// ShowTokenBudget 은 show 출력의 상한이다(board 의 BoardTokenBudget 과 같은 자리).
//
// ★ 근거는 실측이다(운영 원장, 2026-09-17 읽기 전용 측정):
//
//	항목당 판단   69%가 2건 이하 · 94%가 5건 이하 · 최대 17건
//	판단 본문     중앙값 1,420자 · 평균 1,677자 · 최대 12,257자
//	최악 항목     판단 17건 · 본문 합 44,757자
//
// EstimateTokens 는 한글을 1.5토큰/자로 넉넉히 잡으므로 중앙값 본문 하나가 약
// 2,130토큰이고 최악 항목은 약 67,000토큰이다 — **무제한이 불가능한 이유가 그 수다.**
// 6,000 이면 중앙값 본문 둘이 통째로 들어가고 셋째도 대개 들어간다: 항목당 판단이
// 2건 이하인 것이 69%라 **대부분은 안 잘린다.**
//
// ★ board 의 1,200 보다 다섯 배인 이유는 **고정비가 아니어서**다. board 는 세션이
// 상황을 물을 때마다 실리는 값이고 show 는 한 항목을 되짚으려 일부러 부르는 호출이다.
// 예산의 목적은 같다 — 출력이 입력과 함께 무한히 자라지 않는다는 보장.
const ShowTokenBudget = 6000

// showJudgmentFloor 는 예산이 다 차도 **전문으로 내는** 판단 수다.
//
// boardCardFloor 와 같은 자리, 같은 이유다. 되짚는 사람이 이 항목을 여는 순간 제일 먼저
// 읽을 것이 가장 최근 판단인데, 고정분(항목 본문·개정 이력)이 예산을 먹었다고 그것까지
// 제목으로 접으면 이 표면을 부를 이유가 사라진다. 그때는 예산을 넘고, **넘겼다는 사실을
// 함께 찍는다.**
const showJudgmentFloor = 1

// RenderShow 는 ShowResult 하나를 사람이 읽는 텍스트로 만든다.
func RenderShow(res service.ShowResult, now time.Time) string {
	it := res.Item

	// ── ① 지금 본문 ──────────────────────────────────────────────────────────
	var head strings.Builder
	fmt.Fprintf(&head, "show · %s — %s\n", it.ID, showStateLine(it, now))
	if strings.TrimSpace(it.Title) != "" {
		fmt.Fprintf(&head, "제목: %s\n", it.Title)
	}
	if len(it.Paths) > 0 {
		fmt.Fprintf(&head, "경로 %d: %s\n", len(it.Paths), strings.Join(it.Paths, ", "))
	} else {
		// RenderAdd·RenderAmend 와 같은 문구, 같은 이유 — 같은 상태는 같은 진실을 말해야 한다.
		head.WriteString("경로 0 — 경로가 없으면 이 항목은 겹침 축에 안 잡힌다.\n")
	}
	if len(it.Labels) > 0 {
		fmt.Fprintf(&head, "꼬리표: %s\n", strings.Join(it.Labels, ", "))
	}
	if strings.TrimSpace(it.Body) != "" {
		head.WriteString("본문:\n" + indent(clipBody(it.Body), "  ") + "\n")
	} else {
		head.WriteString("본문: 비었다.\n")
	}

	// ── ② 개정 이력 ─────────────────────────────────────────────────────────
	head.WriteString("\n" + renderRevisions(res, now))

	// ── ③ 걸린 판단 ─────────────────────────────────────────────────────────
	//
	// 예산은 여기서만 건다. 고정분(위 둘)을 먼저 세고 남은 만큼 전문을 싣는다 —
	// RenderBoard 가 세션 카드에 하는 것과 같은 계산이다.
	fixed := head.String()
	return fixed + "\n" + renderShowJudgments(res, now, EstimateTokens(fixed))
}

// showStateLine 은 상태 한 줄이다 — 닫힌 항목이면 언제·왜 닫혔는지까지.
//
// ★ 상태를 **첫 줄에** 두는 이유: 이 동사는 닫힌 항목을 주는 것이 존재 이유라, 읽는
// 사람이 "이건 아직 살아 있는 일인가"를 먼저 알아야 아래 판단들을 옳게 읽는다.
func showStateLine(it model.Item, now time.Time) string {
	var b strings.Builder
	b.WriteString(string(it.State))
	if it.ClosedAt != nil {
		fmt.Fprintf(&b, " (%s · %s 전에 닫혔다)",
			it.ClosedAt.UTC().Format("2006-01-02 15:04"), FormatAge(now.Sub(*it.ClosedAt)))
	} else if !it.CreatedAt.IsZero() {
		fmt.Fprintf(&b, " (%s 에 열렸다 · %s 전)",
			it.CreatedAt.UTC().Format("2006-01-02 15:04"), FormatAge(now.Sub(it.CreatedAt)))
	}
	if strings.TrimSpace(it.CloseReason) != "" {
		fmt.Fprintf(&b, "\n닫힘 사유: %s", clip(it.CloseReason, 400))
	}
	if strings.TrimSpace(it.LandedRef) != "" {
		fmt.Fprintf(&b, "\n랜딩: %s", clip(it.LandedRef, 64))
	}
	return b.String()
}

// renderRevisions 는 개정 이력 절이다.
//
// ★ 옛 값 **전문은 안 낸다.** 사유와 바뀐 축은 짧아서 전부 내지만, 개정마다 옛 본문을
// 통째로 실으면 출력이 개정 수에 비례해 자란다 — 그리고 되짚는 사람이 이 절에서 찾는
// 것은 "무엇을 왜 고쳤나"이지 옛 본문 자체가 아니다. 옛 본문이 필요하면 좌표(rev)가
// 여기 있고, 그 값은 추가 전용 표에 그대로 살아 있다.
func renderRevisions(res service.ShowResult, now time.Time) string {
	if hasFailureAxis(res.Derived, "revisions") {
		return "개정 이력: **못 읽었다** — 0건이라는 뜻이 아니다. " +
			failureDetail(res.Derived, "revisions") + "\n"
	}
	if len(res.Revisions) == 0 {
		return "개정 이력: 없다 — 이 항목은 한 번도 안 고쳐졌다.\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "개정 %d건 (rev 순 · 옛 값은 item_revision 에 추가 전용으로 남는다):\n",
		len(res.Revisions))
	for _, r := range res.Revisions {
		axes := strings.Join(r.Changed, "·")
		if axes == "" {
			// ★ 침묵하지 않는다. 같은 값 재지정은 거절되지 않으므로(store/amend.go) 정말
			//   아무 축도 안 바뀐 개정이 존재할 수 있고, 그것과 "축을 못 셌다"는 다르다 —
			//   여기 오는 것은 전자뿐이다(사슬 복원은 실패할 수 없다).
			axes = "바뀐 축 없음(같은 값 재지정)"
		}
		fmt.Fprintf(&b, "  rev %d · %s (%s 전) · %s — %s\n",
			r.Rev, r.At.UTC().Format("2006-01-02 15:04"), FormatAge(now.Sub(r.At)),
			axes, clip(firstLine("", r.Reason), 200))
	}
	return b.String()
}

// renderShowJudgments 는 걸린 판단 절이다. 예산을 여기서 건다.
//
// used 는 고정분이 이미 쓴 토큰이다. 남은 예산만큼 최신부터 전문을 싣고, 모자라면
// **오래된 것부터 제목만** 낸다 — 그리고 그 사실과 건수를 반드시 찍는다.
// 조용히 자르면 "판단이 둘뿐"과 "둘만 보여준다"가 구분되지 않는다(RenderBoard 와 같은 규율).
func renderShowJudgments(res service.ShowResult, now time.Time, used int) string {
	if hasFailureAxis(res.Derived, "judgments") {
		return "걸린 판단: **못 읽었다** — 0건이라는 뜻이 아니다. " +
			failureDetail(res.Derived, "judgments") + "\n"
	}
	if len(res.Judgments) == 0 {
		return "걸린 판단: 없다 — 이 항목에 걸린 판단이 원장에 한 건도 없다.\n"
	}

	// 어디까지 전문인가를 먼저 센다. 잘랐다는 줄의 몫을 미리 떼는 것도 RenderBoard 와 같다 —
	// 그 줄이 예산을 넘겨 버리면 "잘랐다"는 사실 자체가 잘려 나간다.
	const cutReserve = 120
	full := 0
	spent := used
	for i, j := range res.Judgments {
		cost := EstimateTokens(clipBody(j.Body)) + 1
		reserve := 0
		if i < len(res.Judgments)-1 {
			reserve = cutReserve
		}
		if spent+cost+reserve > ShowTokenBudget && full >= showJudgmentFloor {
			break
		}
		spent += cost
		full++
	}

	var b strings.Builder
	fmt.Fprintf(&b, "걸린 판단 %d건 (최신 먼저 · 전문 %d건):\n", len(res.Judgments), full)
	for i, j := range res.Judgments {
		fmt.Fprintf(&b, "  [%s] %s · %s\n", j.Kind,
			j.At.UTC().Format("2006-01-02 15:04"), clip(firstLine(j.Title, j.Body), 100))
		if i < full && strings.TrimSpace(j.Body) != "" {
			b.WriteString(indent(clipBody(j.Body), "      ") + "\n")
		}
	}
	if full < len(res.Judgments) {
		fmt.Fprintf(&b, "★ 오래된 %d건은 **제목만 냈다** — 출력 예산 %d토큰을 넘겼다. "+
			"전문은 `GET /api/v1/items/%s?project=%s` 가 자르지 않고 낸다.\n",
			len(res.Judgments)-full, ShowTokenBudget, res.Item.ID, res.Item.Project)
	}
	if spent > ShowTokenBudget {
		fmt.Fprintf(&b, "★ 그래도 예산을 넘었다(%d토큰 / 상한 %d) — "+
			"가장 최근 판단 %d건은 예산보다 세게 낸다. 그것까지 접으면 이 표면을 부를 이유가 없다.\n",
			spent, ShowTokenBudget, showJudgmentFloor)
	}
	return b.String()
}

// clipBody 는 본문 하나의 상한이다.
//
// ★ 값이 RenderPick 과 같은 것은 우연이 아니다 — 같은 판단 본문을 같은 소비자에게
// 내는 자리라 두 화면이 다른 길이로 자르면 같은 판단이 표면마다 다르게 보인다.
func clipBody(s string) string { return clip(s, judgmentBodyClip) }

// failureDetail 은 그 축의 원인 전문이다. 없으면 빈 문자열이 아니라 그 사실을 말한다 —
// "못 읽었다"만 주고 원인을 안 주면 읽는 쪽이 할 수 있는 것이 없다.
func failureDetail(d service.Derived, axis string) string {
	for _, f := range d.Failures {
		if f.Axis == axis {
			return clip(f.Detail, 300)
		}
	}
	return "원인이 응답에 안 실렸다"
}
