package mcpsrv

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// showRes 는 최소 골격이다 — 시험마다 필요한 축만 덧붙인다.
func showRes() service.ShowResult {
	return service.ShowResult{
		Item: model.Item{
			Project: "p", ID: "it1", Title: "항목 제목", Body: "항목 본문",
			Paths: []string{"internal/"}, State: model.ItemOpen,
			CreatedAt: t0.Add(-48 * time.Hour),
		},
	}
}

// bigJudgment 은 예산을 실제로 넘길 만큼 긴 판단 하나다.
//
// ★ 길이의 근거는 실측이다 — 판단 본문 중앙값 1,420자이고 EstimateTokens 가 한글을
// 1.5토큰/자로 잡으므로 하나가 약 2,130토큰이다. 여기서는 그 중앙값 정도의 본문을
// 여러 건 쌓아 6,000토큰 상한을 넘긴다(실측 최악 항목은 17건 · 44,757자였다).
func bigJudgment(n int, at time.Time) model.Judgment {
	return model.Judgment{
		Kind:  model.JudgmentDecision,
		Title: fmt.Sprintf("판단 %d", n),
		At:    at,
		Body:  fmt.Sprintf("전문시작 판단 %d 의 본문 시작.\n", n) + strings.Repeat("긴 본문 줄이다. ", 140),
	}
}

// TestRenderShowSeparatesZeroFromUnreadable 은 이 화면의 **핵심 규율**을 잰다:
// 0건과 «못 읽었다»가 다른 문장이어야 한다.
//
// 축이 둘(revisions·judgments)이라 넷을 전부 잰다 — 하나만 재면 나머지 축의 침묵이
// 통과로 굳는다.
func TestRenderShowSeparatesZeroFromUnreadable(t *testing.T) {
	zero := RenderShow(showRes(), ShowRenderOptions{Now: t0})
	if !strings.Contains(zero, "개정 이력: 없다") {
		t.Errorf("개정 0건을 «없다»로 안 말한다:\n%s", zero)
	}
	if !strings.Contains(zero, "걸린 판단: 없다") {
		t.Errorf("판단 0건을 «없다»로 안 말한다:\n%s", zero)
	}
	if strings.Contains(zero, "못 읽었다") {
		t.Errorf("0건인데 못 읽었다고 말한다 — 상시 점등이면 판별력이 0이다:\n%s", zero)
	}

	unread := showRes()
	unread.Failures = append(unread.Failures,
		service.DerivedFailure{Axis: "revisions", Detail: "no such table: item_revision"},
		service.DerivedFailure{Axis: "judgments", Detail: "no such table: judgment_link"})
	got := RenderShow(unread, ShowRenderOptions{Now: t0})
	if !strings.Contains(got, "개정 이력: **못 읽었다**") {
		t.Errorf("개정 축의 실패를 «없다»로 접었다 — 되짚는 사람이 0으로 읽는다:\n%s", got)
	}
	if !strings.Contains(got, "걸린 판단: **못 읽었다**") {
		t.Errorf("판단 축의 실패를 «없다»로 접었다:\n%s", got)
	}
	if strings.Count(got, "0건이라는 뜻이 아니다") != 2 {
		t.Errorf("두 축 다 «0이 아니다»를 말해야 한다:\n%s", got)
	}
	// 원인 전문이 없으면 읽는 쪽이 할 수 있는 것이 없다.
	if !strings.Contains(got, "no such table: judgment_link") {
		t.Errorf("원인 전문이 화면에 없다:\n%s", got)
	}
}

// TestRenderShowSaysWhenItTruncatedToBudget 은 **잘렸다는 사실이 화면에 나는지**를 잰다.
//
// 조용히 자르면 "판단이 둘뿐"과 "둘만 보여준다"가 구분되지 않는다(RenderBoard 와 같은
// 규율). 실측 최악은 판단 17건 · 본문 합 44,757자 — 무제한이면 그것이 그대로 나간다.
func TestRenderShowSaysWhenItTruncatedToBudget(t *testing.T) {
	res := showRes()
	for i := 1; i <= 8; i++ {
		res.Judgments = append(res.Judgments, bigJudgment(i, t0.Add(-time.Duration(i)*time.Hour)))
	}
	got := RenderShow(res, ShowRenderOptions{Now: t0})

	if !strings.Contains(got, "걸린 판단 8건") {
		t.Fatalf("전체 건수가 사라졌다 — 자른 뒤에도 몇 건인지는 말해야 한다:\n%s", got)
	}
	if !strings.Contains(got, "제목만 냈다") {
		t.Fatalf("예산을 넘겼는데 잘랐다는 사실이 화면에 없다 — 8건이 전부 실린 줄 안다:\n%s",
			got)
	}
	if !strings.Contains(got, fmt.Sprintf("예산 %d토큰", ShowTokenBudget)) {
		t.Errorf("무엇에 걸려 잘렸는지가 없다:\n%s", got)
	}
	// 전문이 온 것과 안 온 것이 실제로 갈려야 한다 — 문구만 찍고 전부 싣는 고침을 막는다.
	if n := strings.Count(got, "판단 8 의 본문 시작"); n != 0 {
		t.Errorf("가장 오래된 판단의 전문이 그대로 실렸다(%d회) — 자른 것이 아니다:\n%s", n, got)
	}
	if !strings.Contains(got, "판단 1 의 본문 시작") {
		t.Errorf("가장 최근 판단의 전문이 없다 — 자르는 방향이 뒤집혔다:\n%s", got)
	}
	// 되찾을 길을 준다. "잘렸다"만 말하고 전문 경로를 안 주면 읽는 쪽이 할 일이 없다.
	if !strings.Contains(got, "/api/v1/items/it1?project=p") {
		t.Errorf("자르지 않은 전문을 어디서 보는지가 없다:\n%s", got)
	}
}

// TestRenderShowKeepsEveryBodyWhenTheyFitTheBudget 은 위 시험의 **짝**이다.
//
// 이 짝이 없으면 "항상 제목만 낸다"는 고침이 위 시험만 보고 초록으로 지나간다 —
// 그것은 결함을 다른 결함으로 바꾼 것이다. 실측상 항목당 판단이 2건 이하인 것이
// 69%라, 이 갈래가 **대부분의 호출**이다.
func TestRenderShowKeepsEveryBodyWhenTheyFitTheBudget(t *testing.T) {
	res := showRes()
	res.Judgments = []model.Judgment{
		bigJudgment(1, t0.Add(-time.Hour)),
		bigJudgment(2, t0.Add(-2*time.Hour)),
	}
	got := RenderShow(res, ShowRenderOptions{Now: t0})

	if strings.Contains(got, "제목만 냈다") {
		t.Fatalf("중앙값 크기 2건이 예산에 안 들어갔다 — 대부분의 호출이 잘린다는 뜻이다:\n%s", got)
	}
	if !strings.Contains(got, "판단 1 의 본문 시작") || !strings.Contains(got, "판단 2 의 본문 시작") {
		t.Fatalf("전문이 둘 다 안 실렸다:\n%s", got)
	}
	if !strings.Contains(got, "걸린 판단 2건 (최신 먼저 · 전문 2건)") {
		t.Errorf("전문 건수를 안 말한다 — 몇 건이 통째로 왔는지가 이 줄의 값이다:\n%s", got)
	}
}

// TestRenderShowListsRevisionsInRevOrderWithReasonAndAxis 는 개정 절을 잰다.
func TestRenderShowListsRevisionsInRevOrderWithReasonAndAxis(t *testing.T) {
	res := showRes()
	res.Revisions = []model.ItemRevision{
		{Rev: 1, At: t0.Add(-3 * time.Hour), Reason: "제목 오타", Changed: []string{"title"}},
		{Rev: 2, At: t0.Add(-2 * time.Hour), Reason: "리네임 추종", Changed: []string{"body", "paths"}},
	}
	got := RenderShow(res, ShowRenderOptions{Now: t0})

	if !strings.Contains(got, "개정 2건") {
		t.Fatalf("개정 건수가 없다:\n%s", got)
	}
	i1, i2 := strings.Index(got, "rev 1"), strings.Index(got, "rev 2")
	if i1 < 0 || i2 < 0 {
		t.Fatalf("개정 좌표(rev)가 없다 — 옛 값을 되찾을 손잡이가 그것뿐이다:\n%s", got)
	}
	if i1 > i2 {
		t.Errorf("개정이 rev 역순으로 나온다 — 사슬을 거꾸로 읽게 된다:\n%s", got)
	}
	for _, want := range []string{"제목 오타", "리네임 추종", "title", "body·paths"} {
		if !strings.Contains(got, want) {
			t.Errorf("개정 줄에 %q 가 없다 — 사유와 바뀐 축이 이 절의 전부다:\n%s", want, got)
		}
	}
}

// TestRenderShowNamesTheClosedStateFirst 는 닫힌 항목의 화면을 잰다.
//
// ★ 이 동사는 닫힌 항목을 주는 것이 존재 이유라, 읽는 사람이 "이건 아직 살아 있는
// 일인가"를 먼저 알아야 아래 판단들을 옳게 읽는다. 상태가 안 보이면 닫힌 항목의
// 판단을 진행 중인 일의 지시로 읽는다.
func TestRenderShowNamesTheClosedStateFirst(t *testing.T) {
	res := showRes()
	closed := t0.Add(-24 * time.Hour)
	res.Item.State, res.Item.ClosedAt = model.ItemDropped, &closed
	res.Item.CloseReason = "전제가 사라졌다"
	got := RenderShow(res, ShowRenderOptions{Now: t0})

	first := strings.SplitN(got, "\n", 2)[0]
	if !strings.Contains(first, "dropped") {
		t.Fatalf("첫 줄이 상태를 안 말한다(%q) — 닫힌 항목을 진행 중으로 읽는다:\n%s", first, got)
	}
	if !strings.Contains(got, "전제가 사라졌다") {
		t.Errorf("닫힘 사유가 없다:\n%s", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 예산이 **실제 출력**을 묶는가 (리뷰 I-1·I-2·I-3)
// ─────────────────────────────────────────────────────────────────────────────

// worstJudgment 은 실측 최악 항목(haujae-clean-restart)의 판단 하나 크기다 —
// 본문 합 44,757자 / 17건 = 건당 약 2,633자.
//
// ★ **제목도 실물 크기로 준다.** 이 저장소의 판단 제목은 한 문장짜리 한글이라 화면의
// 머리줄이 clip 상한(100 runes)까지 찬다 — 머리줄이 줄당 약 160토큰이 되는 것이 I-1 이
// 가리킨 미계상분의 정체이고, 짧은 제목으로 재면 그 결함이 재현되지 않는다.
func worstJudgment(n int, at time.Time) model.Judgment {
	// ★ 한글로 채운다. 실측의 44,757자가 약 67,000토큰이라는 것은 그 글자가 거의 전부
	//   한글(1.5토큰/자)이라는 뜻이다 — 공백 섞인 문장으로 재면 같은 글자 수가 훨씬 싸져
	//   재현하려던 최악이 재현되지 않는다.
	return model.Judgment{
		Kind: model.JudgmentDecision, At: at,
		Title: fmt.Sprintf("%d", n) + strings.Repeat("최악판단제목이다", 13), // clip 상한(100 runes)까지 찬다
		Body:  "전문시작\n" + strings.Repeat("최악항목의본문줄이다", 263),          // 약 2,633자
	}
}

// reportedOverflow 는 화면이 스스로 밝힌 초과분이다(없으면 -1).
func reportedOverflow(t *testing.T, out string) int {
	t.Helper()
	m := regexp.MustCompile(`예산 (\d+)토큰을 (\d+)토큰 넘는다`).FindStringSubmatch(out)
	if m == nil {
		return -1
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		t.Fatalf("초과분을 못 읽었다(%q): %v", m[0], err)
	}
	return n
}

// TestRenderShowOverflowIsReportedAtTheWorstMeasuredItem 은 **I-1 의 본체**다.
//
// ★ 무엇이 결함이었나. 제목만 내는 판단도 머리줄(`  [kind] 시각 · 제목`)은 찍히는데 그
// 비용이 셈에 한 푼도 안 실렸다. 실측 최악(17건)에서 미계상분이 약 2,600토큰이라
// **실제 출력은 약 7,000토큰인데 셈은 4,050** 이었고, 그래서 초과 고백이 아예 안 떴다 —
// 상한을 15% 넘긴 화면이 "예산 안"이라고 말하고 있었다.
//
// 여기서 재는 것은 문구가 아니라 **셈과 실제의 거리**다. 화면이 스스로 밝힌 초과분이
// 실제 초과분과 상수 오차 안이어야 한다(아래 gap 시험이 그 상수성을 따로 잠근다).
func TestRenderShowOverflowIsReportedAtTheWorstMeasuredItem(t *testing.T) {
	res := showRes()
	for i := 1; i <= 17; i++ {
		res.Judgments = append(res.Judgments, worstJudgment(i, t0.Add(-time.Duration(i)*time.Hour)))
	}
	got := RenderShow(res, ShowRenderOptions{Now: t0})

	actual := EstimateTokens(got)
	if actual <= ShowTokenBudget {
		t.Fatalf("대조가 성립하지 않았다 — 실측 최악 입력인데 출력이 %d토큰으로 예산 %d 안이다",
			actual, ShowTokenBudget)
	}
	reported := reportedOverflow(t, got)
	if reported < 0 {
		t.Fatalf("실제 출력이 %d토큰(상한 %d)인데 화면이 **넘었다는 말을 안 한다** — "+
			"예산이 실제 출력을 안 묶고 있다:\n%s", actual, ShowTokenBudget, clip(got, 800))
	}
	// 셈이 실제보다 작을 수는 있다(초과 줄 자체는 셈에 없다). 다만 그 차는 **상수**여야 한다.
	if gap := (actual - ShowTokenBudget) - reported; gap > 400 {
		t.Errorf("화면이 밝힌 초과분(%d)이 실제 초과분(%d)보다 %d토큰 적다 — "+
			"머리줄 같은 항상 찍히는 몫이 셈에서 빠졌다", reported, actual-ShowTokenBudget, gap)
	}
}

// TestRenderShowBudgetGapDoesNotGrowWithJudgmentCount 은 위 시험의 **일반화**다.
//
// ★ I-1 의 진짜 내용은 "17건에서 2,600토큰이 빠졌다"가 아니라 **미계상분이 판단 수 K 에
// 대해 O(K) 로 벌어진다**는 것이다. 한 점만 재면 상수 보정으로도 초록이 되므로 두 점을
// 재서 기울기를 잠근다 — ShowTokenBudget 주석의 "출력이 입력과 함께 무한히 자라지
// 않는다는 보장"이 이 시험이 지키는 문장 그 자체다.
func TestRenderShowBudgetGapDoesNotGrowWithJudgmentCount(t *testing.T) {
	// 두 점 다 예산을 넘겨야 기울기를 잴 수 있다 — 기본 예산에서는 4건이 안 넘으므로
	// 예산을 좁혀 두 점을 같은 갈래 위에 세운다(바닥이 예산 값과 무관한 성질인 것과 같다).
	const tight = 1500
	gapAt := func(k int) int {
		res := showRes()
		for i := 1; i <= k; i++ {
			res.Judgments = append(res.Judgments, worstJudgment(i, t0.Add(-time.Duration(i)*time.Hour)))
		}
		got := RenderShow(res, ShowRenderOptions{Now: t0, Budget: tight})
		reported := reportedOverflow(t, got)
		if reported < 0 {
			t.Fatalf("판단 %d건에서 초과 고백이 안 떴다 — 이 시험은 두 점 다 넘는 입력을 전제한다:\n%s",
				k, clip(got, 600))
		}
		return (EstimateTokens(got) - tight) - reported
	}
	small, large := gapAt(4), gapAt(17)
	// 13건이 늘었는데 미계상분이 그만큼 자라면(줄당 약 155토큰 × 13 ≈ 2,000) 여기서 걸린다.
	if large > small+200 {
		t.Fatalf("판단 4건일 때 미계상분 %d, 17건일 때 %d — 판단 수에 따라 벌어진다. "+
			"항상 찍히는 몫(머리줄)이 셈에 없다는 뜻이고, 그러면 예산은 보장이 아니다",
			small, large)
	}
}

// TestRenderShowFloorKeepsTheNewestBodyAndNamesWhoBlewTheBudget 은 **셋째 상태**다 —
// 고정분이 예산을 먹어 바닥이 발동하는 갈래(리뷰 I-2).
//
// ★ 앞선 판에서는 이 갈래가 **죽은 코드**였다: 시험의 최대 입력으로도 셈이 예산의 1/4 라
// `showJudgmentFloor` 를 0 으로 바꿔도, 초과 고백 블록을 통째로 지워도 초록이었다.
// RenderBoard 가 같은 자리에서 쓰는 3단 구성(대조 · 갈래 · 상시 발동 아님)을 그대로 옮긴다.
func TestRenderShowFloorKeepsTheNewestBodyAndNamesWhoBlewTheBudget(t *testing.T) {
	const tight = 400
	heavy := func(judgments int) service.ShowResult {
		res := showRes()
		// 고정분을 무겁게 만든다 — 항목 본문이 clipBody 상한까지 갈 수 있는 것이 이 갈래의 근거다.
		res.Item.Body = strings.Repeat("항목 본문이 길다. ", 60)
		for i := 1; i <= judgments; i++ {
			res.Judgments = append(res.Judgments, bigJudgment(i, t0.Add(-time.Duration(i)*time.Hour)))
		}
		return res
	}

	// ── ① 대조 ── 고정분만으로 이 예산을 넘겨야 바닥 갈래를 실제로 지난다.
	empty := RenderShow(heavy(0), ShowRenderOptions{Now: t0, Budget: tight})
	if got := EstimateTokens(empty); got <= tight {
		t.Fatalf("대조가 성립하지 않았다: 판단 0건인 고정분이 %d토큰이라 예산 %d 를 안 넘는다 — "+
			"이 입력으로는 바닥이 발동하는 갈래가 안 돈다", got, tight)
	}
	// 판단이 0건이어도 초과는 말해야 한다. 그리고 그때 범인은 **고정분 하나**다.
	if !strings.Contains(empty, "고정분") {
		t.Errorf("판단 0건인데 고정분이 예산을 넘겼다 — 그 사실이 화면에 없다:\n%s", empty)
	}
	if strings.Contains(empty, "바닥") {
		t.Errorf("판단이 0건인데 바닥을 범인으로 댄다 — 원인 둘이 뭉쳤다:\n%s", empty)
	}

	// ── ② 바닥 ── 전문은 남고, 초과와 **원인 둘**이 갈려 화면에 난다.
	got := RenderShow(heavy(5), ShowRenderOptions{Now: t0, Budget: tight})
	bodies := strings.Count(got, "전문시작")
	// ★ **깨질 수 없는 계약을 리터럴로 단정한다.** `bodies < showJudgmentFloor` 로 쓰면
	//   바닥을 0 으로 만드는 변이가 `0 < 0`(거짓)으로 초록을 낸다 — RenderBoard 의 카드
	//   바닥 시험이 그 함정을 주석으로 적어 뒀고, 여기서도 같다.
	if bodies < 1 {
		t.Fatalf("전문이 0건이다 — 전문이 한 건도 없는 show 는 이 표면을 부를 이유가 없다:\n%s", got)
	}
	if bodies < showJudgmentFloor {
		t.Fatalf("전문이 %d건이다 — 바닥 %d건을 못 지켰다:\n%s", bodies, showJudgmentFloor, got)
	}
	for _, want := range []string{"제목만 냈다", "고정분", "바닥"} {
		if !strings.Contains(got, want) {
			t.Errorf("예산을 넘겼는데 %q 를 안 말한다 — 접은 사실과 넘긴 주체 둘을 따로 말해야 한다:\n%s",
				want, got)
		}
	}

	// ── ③ 예산이 넉넉하면 **아무것도 안 바뀐다.** 상시 발동하면 판별력이 0이다.
	light := RenderShow(heavy(1), ShowRenderOptions{Now: t0})
	if strings.Contains(light, "고정분") || strings.Contains(light, "바닥") {
		t.Fatalf("예산이 넉넉한데 초과를 말한다 — 초과 고백이 상시 점등이다:\n%s", light)
	}
	if got := EstimateTokens(light); got > ShowTokenBudget {
		t.Fatalf("가벼운 입력인데 출력이 %d토큰이다 — 상한 %d", got, ShowTokenBudget)
	}
}

// TestRenderShowNamesWhichOfTheTwoCausesBlewTheBudget 은 **원인 둘이 갈리는지**만 잰다
// (리뷰 I-3). 위 시험은 둘이 함께 뜨는 갈래를 보고, 여기서는 각각 **혼자** 뜨는 갈래를 본다 —
// 둘을 한 문장으로 뭉치면 화면이 늘 바닥을 범인으로 지목한다.
func TestRenderShowNamesWhichOfTheTwoCausesBlewTheBudget(t *testing.T) {
	t.Run("고정분만", func(t *testing.T) {
		res := showRes()
		res.Item.Body = strings.Repeat("항목 본문이 길다. ", 60)
		got := RenderShow(res, ShowRenderOptions{Now: t0, Budget: 300})
		if !strings.Contains(got, "고정분") {
			t.Fatalf("고정분이 넘겼는데 그 사실이 없다:\n%s", got)
		}
		if strings.Contains(got, "바닥") {
			t.Errorf("판단이 0건인데 바닥을 범인으로 댄다:\n%s", got)
		}
	})
	t.Run("바닥만", func(t *testing.T) {
		res := showRes()
		res.Judgments = []model.Judgment{bigJudgment(1, t0.Add(-time.Hour))}
		// 고정분은 이 예산 안이고(항목 절이 약 100토큰), 전문 하나가 그것을 넘긴다.
		got := RenderShow(res, ShowRenderOptions{Now: t0, Budget: 1000})
		if !strings.Contains(got, "바닥") {
			t.Fatalf("바닥이 예산을 이겼는데 그 사실이 없다:\n%s", got)
		}
		if strings.Contains(got, "고정분") {
			t.Errorf("고정분은 예산 안인데 그것을 범인으로 댄다 — 원인 둘이 뭉쳤다:\n%s", got)
		}
	})
}

// TestRenderShowCapsTheRevisionSection 은 개정 절의 줄 상한이다(리뷰 Minor).
//
// 판단 축에는 바닥과 절단이 있는데 이 축은 무제한이었다 — `amend` 가 방금 열렸으니
// 이 축은 이제부터 자란다. 조용히 접으면 "개정이 12건뿐"과 "12건만 보여준다"가 구분되지 않는다.
//
// ★ **입력 건수도 상한 판정도 리터럴이다.** 입력을 `showRevisionLimit+5` 로 잡고 줄 수를
// `showRevisionLimit` 로 재면 시험이 자기가 지켜야 할 상수를 자기 기준으로 재게 되어,
// 상한을 10만으로 키우는 변이가 **초록을 낸다**(실제로 그렇게 썼다가 변이에서 잡혔다 —
// RenderBoard 의 카드 바닥 시험이 같은 함정을 주석으로 적어 뒀다). 계약은 "30건을 주면
// 30줄을 내면 안 된다"이고 12는 조율값이다. 둘을 따로 단정한다.
func TestRenderShowCapsTheRevisionSection(t *testing.T) {
	const given = 30
	res := showRes()
	for i := 1; i <= given; i++ {
		res.Revisions = append(res.Revisions, model.ItemRevision{
			Rev: i, At: t0.Add(-time.Duration(given-i) * time.Hour),
			Reason: fmt.Sprintf("개정 사유 %d", i), Changed: []string{"body"},
		})
	}
	got := RenderShow(res, ShowRenderOptions{Now: t0})

	lines := 0
	for _, ln := range strings.Split(got, "\n") {
		if strings.HasPrefix(ln, "  rev ") {
			lines++
		}
	}
	if lines >= given {
		t.Fatalf("개정 %d건에 줄이 %d개다 — 이 절에 상한이 없다. 개정 수에 O(K) 로 자라면 "+
			"판단 절이 개정 이력에 밀려 통째로 제목만 남는다:\n%s", given, lines, clip(got, 600))
	}
	if lines > showRevisionLimit {
		t.Fatalf("개정 줄이 %d개다 — 상한 %d 을 넘었다", lines, showRevisionLimit)
	}
	if !strings.Contains(got, fmt.Sprintf("개정 %d건", given)) {
		t.Fatalf("전체 건수가 사라졌다 — 접은 뒤에도 몇 건인지는 말해야 한다:\n%s", clip(got, 600))
	}
	if !strings.Contains(got, "줄 상한") {
		t.Fatalf("줄을 접었는데 접었다는 사실이 화면에 없다:\n%s", clip(got, 600))
	}
	// 자르는 방향은 판단 절과 같다 — **오래된 것부터**. 최신 개정이 사라지면 뒤집힌 것이다.
	if strings.Contains(got, "  rev 1 ·") {
		t.Errorf("가장 오래된 개정이 그대로 실렸다 — 접는 방향이 뒤집혔다:\n%s", clip(got, 600))
	}
	if !strings.Contains(got, fmt.Sprintf("  rev %d ·", given)) {
		t.Errorf("가장 최근 개정이 없다 — 접는 방향이 뒤집혔다:\n%s", clip(got, 600))
	}
	// 상한이 넉넉할 때는 아무것도 안 접는다 — 상시 발동하면 판별력이 0이다.
	few := showRes()
	few.Revisions = []model.ItemRevision{{Rev: 1, At: t0, Reason: "하나뿐", Changed: []string{"title"}}}
	if light := RenderShow(few, ShowRenderOptions{Now: t0}); strings.Contains(light, "줄 상한") {
		t.Errorf("개정 1건인데 줄 상한을 말한다 — 접기가 상시 발동한다:\n%s", light)
	}
}

// TestRenderShowRESTPathIsEscaped 는 화면이 주는 경로가 **그대로 쳐서 도는지** 잰다.
//
// 항목 id 는 `[A-Za-z0-9._/-]` 라 슬래시가 들어오고 프로젝트 이름에는 그 제약조차 없다.
// 원문을 그대로 박으면 화면이 사람이 못 쓰는 경로를 준다(리뷰 Minor).
func TestRenderShowRESTPathIsEscaped(t *testing.T) {
	got := ShowRESTPath("proj ect/&x", "fd/item id")
	for _, bad := range []string{" ", "&x=", "?project=proj ect"} {
		if strings.Contains(strings.TrimPrefix(got, "/api/v1/items/"), bad) {
			t.Fatalf("경로에 이스케이프 안 된 %q 가 있다: %s", bad, got)
		}
	}
	if !strings.HasPrefix(got, "/api/v1/items/fd%2Fitem%20id?project=") {
		t.Fatalf("경로가 %q다 — id 는 경로 성분으로, project 는 질의값으로 이스케이프돼야 한다", got)
	}
}

// TestRenderShowDoesNotClaimNothingChangedWhenItCannotKnow 는 **I-4** 다.
//
// 사슬 복원이 빈 축을 내는 갈래가 둘인데(같은 값 재지정 · 읽는 사이에 들어온 개정)
// 화면이 앞의 것으로 **단정**하고 있었다. 이 표면의 존재 이유가 「0 과 못 잼을 가른다」라
// 그 규율이 가장 센 파일에서 그것을 어기면 안 된다.
func TestRenderShowDoesNotClaimNothingChangedWhenItCannotKnow(t *testing.T) {
	res := showRes()
	res.Revisions = []model.ItemRevision{
		{Rev: 1, At: t0.Add(-time.Hour), Reason: "같은 값을 다시 줬다"}, // Changed 가 비어 있다
	}
	got := RenderShow(res, ShowRenderOptions{Now: t0})
	if !strings.Contains(got, "읽는 사이에 들어온 개정") {
		t.Fatalf("빈 축을 «같은 값 재지정»으로 단정한다 — 읽는 사이에 개정이 들어와도 축이 비는데, "+
			"그때는 무엇이 바뀌었는지 **모르는** 것이다:\n%s", got)
	}
}
