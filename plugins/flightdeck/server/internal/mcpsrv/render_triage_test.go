package mcpsrv

import (
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// 후속 트리아지 문구 — 안 잠근 문구가 개정 세 번을 살아남은 전례가 있어 시험으로 핀한다.

func finishWithCreated(n int) service.FinishResult {
	r := service.FinishResult{
		Item:     model.Item{ID: "batch", State: model.ItemDone},
		Judgment: model.Judgment{ID: "j1", Kind: model.JudgmentHandoff, Body: "본문"},
	}
	for i := 0; i < n; i++ {
		r.Followups = append(r.Followups, model.Item{ID: "f" + string(rune('a'+i))})
	}
	return r
}

// 버스트(창작 ≥3)에만 트리아지 줄이 뜬다 — 실측: finish 의 9.5%가 후속 유입의 51%를
// 낳았다. 2건 이하에 띄우면 후속 실은 finish 절반에 떠서 상시 점등이 된다(§4).
func TestRenderFinishBurstLineFiresAtThreeNotTwo(t *testing.T) {
	two := RenderFinish(finishWithCreated(2))
	if strings.Contains(two, "버스트") {
		t.Fatalf("창작 2건에 버스트 줄이 떴다 — 발화율이 §4 를 재현한다:\n%s", two)
	}
	three := RenderFinish(finishWithCreated(3))
	for _, want := range []string{"버스트", "본문이 곧 패치", "pick(item_ids"} {
		if !strings.Contains(three, want) {
			t.Fatalf("창작 3건 응답에 %q 가 없다 — 기준·실행 동사 없는 경고는 지시가 된다:\n%s", want, three)
		}
	}
	// 지시 낱말 금지 — 판단은 세션이 한다("판단은 사람이 한다" 계승).
	if strings.Contains(three, "만들지 마라") || strings.Contains(three, "등록하지 마라") {
		t.Fatalf("버스트 줄이 blanket 지시를 한다:\n%s", three)
	}
}

// 후속 0건 줄은 add 를 **밀지 않는다** — 옛 문구("있다면 지금 add 로 넣어라")는
// 항목화를 미는 문장 10 : 거르는 기준 0 불균형의 한 자리였고, 그 부류의 관문·문구가
// add→followups 전환만 만들고 총유입을 못 줄인 실측(2026-08-07)이 있다.
func TestRenderFinishZeroLineDoesNotPushAdd(t *testing.T) {
	out := RenderFinish(finishWithCreated(0))
	if !strings.Contains(out, "후속 0건") {
		t.Fatalf("0건 줄이 사라졌다:\n%s", out)
	}
	if strings.Contains(out, "지금 add 로 넣어라") {
		t.Fatalf("0건 줄이 여전히 등록을 민다:\n%s", out)
	}
	if !strings.Contains(out, "본문이 곧 패치") {
		t.Fatalf("0건 줄에 거르는 기준이 없다:\n%s", out)
	}
}
