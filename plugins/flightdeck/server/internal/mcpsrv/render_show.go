package mcpsrv

import (
	"fmt"
	"net/url"
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

// ShowTokenBudget 은 show 출력의 기본 상한이다(board 의 BoardTokenBudget 과 같은 자리).
//
// ★ 근거는 실측이다(운영 원장, 2026-09-17 읽기 전용 측정):
//
//	항목당 판단   69%가 2건 이하 · 94%가 5건 이하 · 최대 17건
//	판단 본문     중앙값 1,420자 · 평균 1,677자 · 최대 12,257자
//	최악 항목     판단 17건 · 본문 합 44,757자
//
// EstimateTokens 는 한글을 1.5토큰/자로 넉넉히 잡으므로 중앙값 본문 하나가 약
// 2,130토큰이고 최악 항목은 약 67,000토큰이다 — **무제한이 불가능한 이유가 그 수다.**
// 6,000 이면 중앙값 본문 **둘**이 통째로 들어간다(항목 절 약 100 + 2,130×2 = 4,360).
// 셋째는 안 들어간다(6,490 > 6,000) — 그래도 이 값을 고른 근거는 분포다: 항목당 판단이
// 2건 이하인 것이 **69%** 라 대부분의 호출은 안 잘린다.
//
// ★ board 의 1,200 보다 다섯 배인 이유는 **고정비가 아니어서**다. board 는 세션이
// 상황을 물을 때마다 실리는 값이고 show 는 한 항목을 되짚으려 일부러 부르는 호출이다.
// 예산의 목적은 같다 — 출력이 입력과 함께 무한히 자라지 않는다는 보장.
//
// ★★ **꼬리(withTail)는 이 예산 밖이다.** board 는 꼬리를 예산 안에 넣는데(그쪽은
// `BoardRenderOptions.Tail` 로 받는다) 여기서는 안 넣는다. 이유 셋:
// ⓐ 꼬리는 **도구 전부**에 붙는 공통분이고 그 크기는 이 항목의 내용이 아니라 남의 세션
// 수가 정한다 — 예산에 넣으면 옆 세션이 늘었다는 이유로 이 항목의 판단이 잘린다.
// ⓑ 꼬리에는 이미 자기 상한이 있다(tailOverlapLimit · TailNoteLimit).
// ⓒ `fd show` 는 꼬리를 아예 안 낸다 — 넣으면 같은 예산이 두 표면에서 다른 것을 재게 된다.
// board 가 반대로 하는 이유도 명확하다: 그쪽 예산의 목적은 **매 턴 실리는 고정비 전체**를
// 1,200 안에 묶는 것이라 꼬리가 그 안에 있어야 한다.
const ShowTokenBudget = 6000

// showJudgmentFloor 는 예산이 다 차도 **전문으로 내는** 판단 수다.
//
// boardCardFloor 와 같은 자리, 같은 이유다. 되짚는 사람이 이 항목을 여는 순간 제일 먼저
// 읽을 것이 가장 최근 판단인데, 고정분(항목 본문·개정 이력·판단 머리줄)이 예산을 먹었다고
// 그것까지 제목으로 접으면 이 표면을 부를 이유가 사라진다. 그때는 예산을 넘고,
// **넘겼다는 사실과 넘긴 주체를 함께 찍는다**(renderShowBudgetNotes).
const showJudgmentFloor = 1

// showRevisionLimit 은 개정 절이 **줄을 내는** 개정 수다. 건수는 머리줄이 전부 센다.
//
// ★ tailOverlapLimit 과 같은 자리, 같은 이유다(리뷰 Minor, 2026-09-17). 판단 축에는
// 바닥과 절단이 있는데 이 축에는 상한이 없었다 — 행 하나가 사유 200 runes(약 300토큰)에
// 좌표 약 60토큰이라 개정 수 K 에 대해 O(K) 로 자란다. `amend` 가 2026-09-16 에 열렸으니
// 이 축은 **이제부터** 자라고, 상한이 없으면 개정 이력이 예산을 먹어 판단 절이 통째로
// 제목만 남는다(고정분이 예산을 이기는 갈래다).
//
// 12 인 근거: 최악 12×360 = 약 4,320 토큰이라 6,000 안에 머물고, 그 위에 바닥 1건이
// 얹혀 넘치면 그 초과가 화면에 고백된다. 자르는 방향은 판단 절과 같다 — **오래된 것부터**.
const showRevisionLimit = 12

// ShowRenderOptions 는 show 화면의 조절값이다.
//
// ★ Budget 이 인자인 이유는 **시험이 그 갈래를 태울 손잡이가 필요해서**다
// (BoardRenderOptions.Budget 과 같은 모양, 같은 이유). 상수만 두면 바닥·초과 고백 갈래를
// 실제로 지나는 입력을 만들 수 없고, 그러면 그 두 갈래가 죽은 코드인 채로 초록이 된다 —
// 실제로 그 상태였다(리뷰 I-2).
type ShowRenderOptions struct {
	Now time.Time
	// Budget 은 토큰 상한이다. 0 이면 ShowTokenBudget.
	Budget int
}

// showBudgetTally 는 이 화면이 예산을 어떻게 썼는가다.
//
// ★ 넷을 **따로** 센다. 뭉치면 "무엇이 예산을 넘겼나"에 답할 수 없고, 그러면 화면이
// 늘 바닥을 범인으로 지목한다(리뷰 I-3 — RenderBoard 가 "원인이 다르므로 뭉치면 읽는
// 사람이 손댈 자리를 못 찾는다"로 이미 금지한 방식이다).
type showBudgetTally struct {
	// fixed 는 **항상 찍히는 몫**이다 — 항목 절 · 개정 절 · 판단 절 머리줄 전부.
	// 떨어뜨릴 수 있는 단위가 아니라 board 의 `fixed`(머리·발·꼬리)와 같은 자리다.
	fixed int
	// spent 는 실제 출력 어림 전체다(fixed + 실린 전문).
	spent int
	// forced 는 **바닥이 예산을 이기고** 남긴 전문 수다.
	forced int
	// cut 은 제목만 낸 판단 수다.
	cut int
}

// RenderShow 는 ShowResult 하나를 사람이 읽는 텍스트로 만든다.
func RenderShow(res service.ShowResult, opt ShowRenderOptions) string {
	now := opt.Now
	budget := opt.Budget
	if budget <= 0 {
		budget = ShowTokenBudget
	}
	it := res.Item
	rest := ShowRESTPath(it.Project, it.ID)

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
	head.WriteString("\n" + renderRevisions(res, now, rest))

	// ── ③ 걸린 판단 ─────────────────────────────────────────────────────────
	fixed := head.String() + "\n"
	section, tally := renderShowJudgments(res, now, EstimateTokens(fixed), budget)
	return fixed + section + renderShowBudgetNotes(tally, budget, rest)
}

// ShowRESTPath 는 **자르지 않는 정본**의 경로다.
//
// ★ 화면이 주는 경로는 사람이 그대로 쳐서 돌아야 한다. 손으로 조립하면 안 된다 —
// 항목 id 는 `[A-Za-z0-9._/-]` 라 슬래시가 들어오고 프로젝트 이름에는 그 제약조차 없다
// (리뷰 Minor: `?project=%s` 에 원문을 그대로 박고 있었다).
//
// ★★ **cmd/fd 의 showPath 가 이 함수를 쓴다.** 화면에 나가는 문자열과 클라이언트가
// 실제로 치는 문자열이 두 벌이면 반드시 갈리고, 갈린 날 화면은 못 쓰는 경로를 준다.
// 이스케이프 규칙도 그쪽 urlPath 와 같다(QueryEscape 한 뒤 `+` 를 `%20` 으로 — 경로
// 성분에서 `+` 는 공백이 아니라 리터럴 `+` 로 읽힌다).
func ShowRESTPath(project, itemID string) string {
	return "/api/v1/items/" + strings.ReplaceAll(url.QueryEscape(itemID), "+", "%20") +
		"?project=" + url.QueryEscape(project)
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
//
// ★ 줄 수에는 상한이 있다(showRevisionLimit). 넘으면 **오래된 것부터** 줄을 접고 그
// 사실을 찍는다 — 조용히 접으면 "개정이 12건뿐"과 "12건만 보여준다"가 구분되지 않는다.
func renderRevisions(res service.ShowResult, now time.Time, rest string) string {
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

	shown := res.Revisions
	if skipped := len(shown) - showRevisionLimit; skipped > 0 {
		shown = shown[skipped:]
		fmt.Fprintf(&b, "  … 오래된 %d건은 줄 상한(%d) 때문에 안 냈다 — "+
			"전문은 `GET %s` 가 자르지 않고 낸다\n", skipped, showRevisionLimit, rest)
	}
	for _, r := range shown {
		axes := strings.Join(r.Changed, "·")
		if axes == "" {
			// ★ **모르는 것을 «없다»로 단정하지 않는다**(리뷰 I-4). 여기 오는 갈래가 둘이다:
			//
			//   ⓐ 같은 값 재지정 — store/amend.go 가 그것을 거절하지 않으므로 정말 아무
			//      축도 안 바뀐 개정이 원장에 존재할 수 있다.
			//   ⓑ **읽는 사이에 들어온 개정** — service.ShowItem 은 항목을 먼저 읽고 개정
			//      이력을 그 다음에 읽는다(없음 관문이 항목에 걸려 있어 순서를 못 바꾼다).
			//      그 사이에 amend 가 하나 커밋되면 마지막 행의 「다음 상태」로 쓰는 값이
			//      **그 행이 담은 옛 값과 같아져** 축이 빈다. 그때 이 화면은 그 개정이
			//      무엇을 바꿨는지 **모르는** 것이지 «없는» 것이 아니다.
			//
			// 앞선 판은 "여기 오는 것은 전자뿐이다"라고 단정했는데 같은 파일이 문서화한
			// 한 칸 오차가 정확히 ⓑ 를 만든다 — 0 과 못 잼을 가르려고 만든 표면이 그
			// 규율이 가장 센 자리에서 그것을 어기고 있었다.
			axes = "바뀐 축 없음 — 같은 값 재지정이거나, 읽는 사이에 들어온 개정이다"
		}
		fmt.Fprintf(&b, "  rev %d · %s (%s 전) · %s — %s\n",
			r.Rev, r.At.UTC().Format("2006-01-02 15:04"), FormatAge(now.Sub(r.At)),
			axes, clip(firstLine("", r.Reason), 200))
	}
	return b.String()
}

// renderShowJudgments 는 걸린 판단 절과 그 절의 예산 셈을 낸다.
//
// used 는 고정분(항목 절 · 개정 절)이 이미 쓴 토큰이다.
//
// ★★ **머리줄은 고정분이다**(리뷰 I-1). 전문이 실리든 제목만 실리든 `[kind] 시각 · 제목`
// 줄은 **반드시 찍히므로** 떨어뜨릴 수 있는 단위가 아니다. 앞선 판은 이 줄의 비용을
// 한 푼도 안 세서, 판단 17건이면 미계상분이 약 2,600토큰(한글 줄당 약 155)이나 됐고
// 실제 출력이 7,000토큰인데 셈은 4,050 이라 **초과 고백이 아예 안 떴다.** 미계상분은
// 판단 수 K 에 대해 O(K·155) 로 선형으로 벌어진다 — "출력이 입력과 함께 무한히 자라지
// 않는다"는 보장이 그 축에서 거짓이었다.
//
// ★ 그래서 RenderBoard 와 **정말로** 같은 계산이 된다: board 의 `blocks` 는 카드 **통째**라
// 떨어뜨리면 출력에서 통째로 사라진다. 여기서 그 단위는 머리줄이 아니라 **본문 블록**이고,
// 머리줄은 board 의 `fixed`(머리·발·꼬리) 쪽이다.
func renderShowJudgments(res service.ShowResult, now time.Time, used, budget int) (string, showBudgetTally) {
	if hasFailureAxis(res.Derived, "judgments") {
		s := "걸린 판단: **못 읽었다** — 0건이라는 뜻이 아니다. " +
			failureDetail(res.Derived, "judgments") + "\n"
		return s, showBudgetTally{fixed: used + EstimateTokens(s), spent: used + EstimateTokens(s)}
	}
	if len(res.Judgments) == 0 {
		s := "걸린 판단: 없다 — 이 항목에 걸린 판단이 원장에 한 건도 없다.\n"
		return s, showBudgetTally{fixed: used + EstimateTokens(s), spent: used + EstimateTokens(s)}
	}

	// 항상 찍히는 것과 떨어뜨릴 수 있는 것을 **먼저 가른다.**
	heads := make([]string, len(res.Judgments))
	bodies := make([]string, len(res.Judgments))
	for i, j := range res.Judgments {
		heads[i] = fmt.Sprintf("  [%s] %s · %s\n", j.Kind,
			j.At.UTC().Format("2006-01-02 15:04"), clip(firstLine(j.Title, j.Body), 100))
		if strings.TrimSpace(j.Body) != "" {
			bodies[i] = indent(clipBody(j.Body), "      ") + "\n"
		}
	}
	// ★ 절 머리줄은 `full` 을 담는데 그 값은 아래 루프가 정한다. 셈에는 전체 건수를 박아
	//   재는데, 두 수는 자릿수만 다르고 ASCII 는 0.3토큰/자라 차이가 1토큰을 안 넘는다.
	sectionHead := fmt.Sprintf("걸린 판단 %d건 (최신 먼저 · 전문 %d건):\n",
		len(res.Judgments), len(res.Judgments))
	fixed := used + EstimateTokens(sectionHead+strings.Join(heads, ""))

	// 잘랐다는 줄의 몫을 미리 뗀다 — 그 줄이 예산을 넘겨 버리면 "잘랐다"는 사실 자체가
	// 잘려 나간다(RenderBoard 의 reserve 와 같은 자리, 값만 다르다: 이쪽 줄은 REST 경로를
	// 함께 실어 길다).
	const cutReserve = 120
	tally := showBudgetTally{fixed: fixed, spent: fixed}
	full := 0
	for i, body := range bodies {
		cost := EstimateTokens(body) + 1
		reserve := 0
		if i < len(bodies)-1 {
			reserve = cutReserve
		}
		over := tally.spent+cost+reserve > budget
		if over && full >= showJudgmentFloor {
			break
		}
		if over {
			// 바닥이 아니었으면 안 실었을 전문이다 — 초과의 **원인**을 여기서 센다.
			tally.forced++
		}
		tally.spent += cost
		full++
	}
	tally.cut = len(res.Judgments) - full

	var b strings.Builder
	fmt.Fprintf(&b, "걸린 판단 %d건 (최신 먼저 · 전문 %d건):\n", len(res.Judgments), full)
	for i := range res.Judgments {
		b.WriteString(heads[i])
		if i < full {
			b.WriteString(bodies[i])
		}
	}
	return b.String(), tally
}

// renderShowBudgetNotes 는 예산에 대한 사실 **둘**을 낸다. 순수 함수다.
//
// ★ 둘은 **다른 사실**이다(리뷰 I-3). "잘랐다"는 판단 전문이 넘쳤다는 것이고, "넘었다"는
// 출력이 상한을 실제로 넘었다는 것이며 그 원인은 **고정분**일 수도 **바닥**일 수도 있다.
// 뭉쳐서 늘 바닥을 범인으로 지목하면 읽는 사람이 손댈 자리를 못 찾는다 — RenderBoard 가
// 같은 자리에서 그것을 이미 금지했다("원인이 다르므로 뭉치면…").
//
// 고정분이 혼자 예산을 넘기는 갈래는 실재한다: 항목 본문이 clipBody 상한(4,000 runes ≈
// 6,000토큰)까지 갈 수 있고 개정 절도 상한 안에서 수천 토큰이 된다.
func renderShowBudgetNotes(t showBudgetTally, budget int, rest string) string {
	var b strings.Builder
	if t.cut > 0 {
		fmt.Fprintf(&b, "★ 오래된 %d건은 **제목만 냈다** — 출력 예산 %d토큰을 넘겼다. "+
			"전문은 `GET %s` 가 자르지 않고 낸다.\n", t.cut, budget, rest)
	}
	if t.spent > budget {
		var causes []string
		if t.fixed > budget {
			causes = append(causes, fmt.Sprintf(
				"**고정분**(항목 본문·개정 이력·판단 머리줄)만으로 이미 %d토큰이다", t.fixed))
		}
		if t.forced > 0 {
			causes = append(causes, fmt.Sprintf(
				"가장 최근 판단 %d건은 **바닥**(%d건)이 예산을 이기고 남긴 것이다"+
					"(그것까지 접으면 이 표면을 부를 이유가 없다)",
				t.forced, showJudgmentFloor))
		}
		if len(causes) == 0 {
			// 도달 불가여야 한다(고정분도 바닥도 아닌데 넘는 길이 없다). 그래도 침묵하지
			// 않는다 — 원인을 못 가른 초과를 조용히 내면 셈이 틀렸다는 사실이 사라진다.
			causes = append(causes, "원인을 이 계층이 못 가른다 — 셈이 틀렸다")
		}
		fmt.Fprintf(&b, "⚠ 이 출력은 예산 %d토큰을 %d토큰 넘는다 — %s.\n",
			budget, t.spent-budget, strings.Join(causes, " · "))
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
