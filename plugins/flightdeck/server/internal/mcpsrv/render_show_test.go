package mcpsrv

import (
	"fmt"
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
		Body:  fmt.Sprintf("판단 %d 의 본문 시작.\n", n) + strings.Repeat("긴 본문 줄이다. ", 140),
	}
}

// TestRenderShowSeparatesZeroFromUnreadable 은 이 화면의 **핵심 규율**을 잰다:
// 0건과 «못 읽었다»가 다른 문장이어야 한다.
//
// 축이 둘(revisions·judgments)이라 넷을 전부 잰다 — 하나만 재면 나머지 축의 침묵이
// 통과로 굳는다.
func TestRenderShowSeparatesZeroFromUnreadable(t *testing.T) {
	zero := RenderShow(showRes(), t0)
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
	got := RenderShow(unread, t0)
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
	got := RenderShow(res, t0)

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
	got := RenderShow(res, t0)

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
	got := RenderShow(res, t0)

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
	got := RenderShow(res, t0)

	first := strings.SplitN(got, "\n", 2)[0]
	if !strings.Contains(first, "dropped") {
		t.Fatalf("첫 줄이 상태를 안 말한다(%q) — 닫힌 항목을 진행 중으로 읽는다:\n%s", first, got)
	}
	if !strings.Contains(got, "전제가 사라졌다") {
		t.Errorf("닫힘 사유가 없다:\n%s", got)
	}
}
