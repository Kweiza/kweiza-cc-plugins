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

// 창작 후속이 **하나라도** 있으면 거르는 기준이 뜬다 (2026-08-21 개정).
//
// 앞선 판은 버스트(창작 ≥3)에만 냈고, 그 판정의 근거는 "2건 이하에 띄우면 후속 실은
// finish 절반에 떠서 상시 점등이 된다(§4)"였다. 원장이 그 판단을 뒤집었다 —
// 창 2026-08-11T10:12~08-21(item.followup_created 가 그때 생겨 그 이전은 원리적으로
// 못 센다)에서 거르는 기준의 실제 도달률이 **1/338** 이었다. TriageGuidance 는
// judgeMissingFollowups 거절에만 실리는데 그 관문이 창 안에서 1회 발화했고,
// 같은 창이 창작 후속 338건을 낳았다.
//
// 구멍은 배치에 있었다: 0건은 아래 0건 줄이, ≥3건은 버스트 줄이 잡는데 **1~2건 구간이
// 통째로 비었고**, 그 구간 세션이 본 것은 "후속 N건 등록 (판단에 이어졌다)"라는 칭찬
// 한 줄뿐이다. committed finish 510회의 분포가 그 구멍의 크기다: 0개 280 · 1개 97 ·
// 2개 59 · ≥3개 74.
//
// 겨누는 실물: 같은 창의 done 후속 141건 중 81건(57%)을 **만든 세션이 중앙값 20.7분 뒤
// 다시 집어서** 닫았다. 그 세션들은 그 자리에서 할 수 있었음을 행동으로 이미 증명했고,
// 이 줄이 주는 것이 정확히 그 실행 경로(pick(item_ids=[…]))다.
//
// 상시 점등이 아니다 — 후속 0건이 committed finish 의 54.9%라 이 줄은 절반 이하에서만 뜬다.
func TestRenderFinishTriageReachesEveryCreatedFollowup(t *testing.T) {
	for _, n := range []int{1, 2, 3} {
		out := RenderFinish(finishWithCreated(n))
		for _, want := range []string{"본문이 곧 패치", "pick(item_ids"} {
			if !strings.Contains(out, want) {
				t.Fatalf("창작 %d건 응답에 %q 가 없다 — 거르는 기준이 이 구간에 안 닿으면\n"+
					"세션이 보는 것은 등록 칭찬 한 줄뿐이다:\n%s", n, want, out)
			}
		}
		// 지시 낱말 금지 — 판단은 세션이 한다("판단은 사람이 한다" 계승).
		if strings.Contains(out, "만들지 마라") || strings.Contains(out, "등록하지 마라") {
			t.Fatalf("창작 %d건 줄이 blanket 지시를 한다:\n%s", n, out)
		}
	}
	// 0건 갈래에는 이 줄이 없다 — 그쪽은 0건 줄이 따로 진다(유실 복구 경로를 먼저 말한다).
	if zero := RenderFinish(finishWithCreated(0)); strings.Contains(zero, "pick(item_ids") {
		t.Fatalf("창작 0건에 이어받기 문구가 떴다 — 만들지도 않은 것을 이어받으라고 한다:\n%s", zero)
	}
}

// "버스트"라는 낱말과 그 실측 주장은 ≥3 에만 남는다.
//
// ★ 이 갈림이 위 개정의 절반이다 — 발화 조건은 넓히되 **실측 문장은 안 넓힌다.**
// "이런 버스트가 후속 유입의 절반을 낳는다"는 창작 ≥3 에서만 참인 수다(finish 의 9.5%가
// 유입의 51%). 1~2건에 그대로 실으면 응답이 거짓을 말하고, 이 저장소에는 관문 문구가
// 거짓이어서 세션 행동을 실제로 오염시킨 실물이 있다(d878bab — 그 문구가 권한 note 우회를
// 쓴 세션이 있었고, 그렇게 만들어진 것은 중복 판단이었다).
func TestRenderFinishBurstClaimStaysAtThree(t *testing.T) {
	for _, n := range []int{1, 2} {
		if out := RenderFinish(finishWithCreated(n)); strings.Contains(out, "버스트") {
			t.Fatalf("창작 %d건에 버스트 낱말이 떴다 — 그 수는 ≥3 에서만 참이다:\n%s", n, out)
		}
	}
	if three := RenderFinish(finishWithCreated(3)); !strings.Contains(three, "버스트") {
		t.Fatalf("창작 3건에서 버스트 실측이 사라졌다:\n%s", three)
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
