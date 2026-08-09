package service

import (
	"encoding/json"
	"testing"
	"time"
)

// 접힘이 **원장에서 다시 측정 가능한가**를 잠근다.
//
// 2026-08-06 개정(`182664a`)이 표시분만 발화 기록으로 남기면서, 한 턴의 `prescribe` 이벤트
// 수가 구조적으로 `PrescribeMax` 를 못 넘게 됐다. 그런데 그 개정의 근거로 못박은 재측 방법이
// "턴 = 같은 세션·같은 초의 prescribe 이벤트 묶음, len>3 이면 접힌 턴"이라, **그 커밋 뒤로
// 이 레시피는 영구히 0을 낸다.** 랜딩 직전 분포에는 4 이상이 실재했고(4:8 · 5:4 · 6:2 · 7:2 ·
// 8:1) 그 값이 다시는 안 생긴다.
//
// 그래서 접힌 턴은 **자기 이름의 이벤트 하나**를 남긴다. `emittedKeys` 가 `kind='prescribe'`
// 로 거르므로 억제 축은 안 건드리고, `store/prescribe_reach.go` 도 `kind IN
// ('prescribe','prescribe_ack')` 라 확인율에도 안 섞인다 — 그 격리가 이 설계의 조건이다.

// foldedPayloadOf 는 접힘 이벤트 하나를 읽는다. **재측 레시피 그 자체다** —
// 사람이 원장에서 접힘을 다시 재려면 정확히 이 모양을 읽게 된다.
//
// ★ `since` 는 안 읽는다. 그 필드는 다음 턴이 창을 물려받는 **배선**이고, 여기서 값을
// 단정하면 시험이 구현을 과잉 지정한다 — 그 축은 행동으로 잠근다
// (TestFoldedTurnKeepsTheWindowUntilTheBacklogDrains).
func foldedPayloadOf(t *testing.T, payload string) struct {
	Keys  []string `json:"keys"`
	Shown int      `json:"shown"`
} {
	t.Helper()
	var p struct {
		Keys  []string `json:"keys"`
		Shown int      `json:"shown"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		t.Fatalf("접힘 payload 를 못 읽었다(%s): %v", payload, err)
	}
	return p
}

// 접힌 턴은 원장에 흔적을 남기고, 그 흔적만으로 접힌 수와 접힌 축을 되찾을 수 있다.
func TestFoldedTurnLeavesALedgerTrace(t *testing.T) {
	svc, st := newSvc(t)

	paths := []string{"a/1.go", "b/2.go", "c/3.go", "d/4.go", "e/5.go"}
	sess := openSessionForPrescribeTest(t, svc)
	claimItemForPrescribeTest(t, svc, st, sess, "fd-x", []string{"internal/judge"})
	for _, p := range paths {
		touchPathForPrescribeTest(t, st, sess, p)
	}

	res, err := svc.Prescriptions(ctx(), sess)
	if err != nil {
		t.Fatalf("호출 실패: %v", err)
	}
	if res.Folded == 0 {
		t.Fatalf("다섯이 선언 밖인데 안 접혔다 — 이 시험의 전제가 깨졌다: shown=%d", len(res.Shown))
	}

	evs, err := st.ListSessionEvents(ctx(), sess, "prescribe_folded", time.Time{})
	if err != nil {
		t.Fatalf("접힘 이벤트 조회 실패: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("접힌 턴 하나에 접힘 이벤트가 %d건이다 — 원장에서 접힘을 다시 셀 수 없다\n"+
			"(이 턴: all=%d shown=%d folded=%d)", len(evs), len(res.All), len(res.Shown), res.Folded)
	}

	p := foldedPayloadOf(t, evs[0].Payload)
	if len(p.Keys) != res.Folded {
		t.Fatalf("접힌 키가 %d개인데 접힌 수는 %d다: %v", len(p.Keys), res.Folded, p.Keys)
	}
	if p.Shown != len(res.Shown) {
		t.Fatalf("표시분이 %d인데 payload 는 %d라고 적었다 — 한 턴의 총 처방 수(%d)를 "+
			"이 둘의 합으로 되찾는 것이 이 필드의 유일한 쓸모다", len(res.Shown), p.Shown, len(res.All))
	}

	// 접힌 **축**까지 되찾을 수 있어야 한다. 2026-08-06 재측이 실제로 낸 값이
	// "overlap 11 · unclaimed 11 · silent 4 · outside 2" 라는 축 분포였다.
	folded := map[string]bool{}
	for _, x := range res.All[len(res.Shown):] {
		folded[x.Key] = true
	}
	for _, k := range p.Keys {
		if !folded[k] {
			t.Errorf("원장이 %q 를 접혔다고 적었는데 이 턴에 접힌 것이 아니다: 접힘 %v", k, folded)
		}
		delete(folded, k)
	}
	if len(folded) != 0 {
		t.Errorf("접혔는데 원장에 안 남은 키가 있다: %v — 축 분포를 다시 못 잰다", folded)
	}
}

// TestFoldedTurnKeepsTheWindowUntilTheBacklogDrains — **한 턴에 몰아친 다발이 돌아온다.**
//
// 2026-08-06 개정은 접힌 것을 발화로 안 세게 만들어 소실을 순환으로 바꿨지만, 그 순환에는
// 조건이 하나 붙어 있었다: `TurnPaths` 가 `f.LastAt.After(since)` 로 뽑히고 `since` 가
// **마지막 발화 시각**이라, 세션이 **그 경로를 다시 만져야만** 축이 돈다. 그래서 한 턴에
// 몰아친 다발은 그 뒤 다른 일을 하러 가는 순간 그대로 사라졌다.
//
// ★ 기존 시험(`TestFoldedPrescriptionsAreNotRecordedAndComeBack`)은 둘째 턴에 **같은 다섯을
// 전부 다시 만진다.** 그래서 이 결함이 원리적으로 안 보인다 — 그 시험이 재현하는 것은
// "일하는 세션의 정상 흐름"이고, 여기서 재현하는 것은 **다발이 끝난 뒤**다.
//
// ★ 걸리는 축은 `outside` 만이 아니다. `overlap` 은 `OverlapPairs(in.TurnPaths, …)` 라 빈
// TurnPaths 에 0쌍이고 `unclaimed` 는 `len(in.TurnPaths)==0` 이면 첫 가드에서 반환한다.
// 2026-08-06 재측의 접힘 분포로 보면 사라지던 28건 중 24건이 이 셋이다(overlap 11 ·
// unclaimed 11 · outside 2).
//
// 그래서 접힌 턴은 자기가 본 창을 원장에 남기고 **다음 턴이 그것을 물려받는다.** 이 시험은
// 세 턴을 다 본다 — 물려받고, 밀린 것이 나오고, **마른다.** 마지막이 없으면 "창을 영영
// 안 민다"로도 초록이 나고 그것은 설계 §4 의 상시 점등이다.
func TestFoldedTurnKeepsTheWindowUntilTheBacklogDrains(t *testing.T) {
	svc, st := newSvc(t)

	paths := []string{"a/1.go", "b/2.go", "c/3.go", "d/4.go", "e/5.go"}
	sess := openSessionForPrescribeTest(t, svc)
	claimItemForPrescribeTest(t, svc, st, sess, "fd-x", []string{"internal/judge"})
	for _, p := range paths {
		touchPathForPrescribeTest(t, st, sess, p)
	}

	first, err := svc.Prescriptions(ctx(), sess)
	if err != nil {
		t.Fatalf("첫 턴 실패: %v", err)
	}
	if first.Folded == 0 {
		t.Fatalf("다섯이 선언 밖인데 안 접혔다 — 이 시험의 전제가 깨졌다: shown=%d", len(first.Shown))
	}
	foldedKeys := map[string]bool{}
	for _, p := range first.All[len(first.Shown):] {
		foldedKeys[p.Key] = true
	}

	// 둘째 턴 — **아무것도 다시 안 만진다.** 다발이 끝나고 세션이 다른 일로 간 모양이다.
	second, err := svc.Prescriptions(ctx(), sess)
	if err != nil {
		t.Fatalf("둘째 턴 실패: %v", err)
	}
	if len(second.Shown) == 0 {
		t.Fatalf("접혔던 %d건이 안 올라왔다 — 경로를 다시 안 만지면 그대로 소실이다.\n"+
			"이것이 PrescribeMax 가 애초에 겨냥한 시나리오(한 턴에 몰아친 다발)다", first.Folded)
	}
	for _, p := range second.Shown {
		if !foldedKeys[p.Key] {
			t.Fatalf("첫 턴에 이미 표시된 %q 가 다시 떴다 — 창을 물려받는 것과 "+
				"억제를 푸는 것은 다른 일이다(설계 §4 의 상시 점등)", p.Key)
		}
		delete(foldedKeys, p.Key)
	}
	if len(foldedKeys) != 0 {
		t.Fatalf("접혔던 것 중 %v 가 아직 안 나왔다 — 상한 안에 들어가는 수인데 밀렸다", foldedKeys)
	}

	// 셋째 턴 — 밀린 것이 다 나갔으니 **마른다.** 여기가 초록이어야 "창을 안 민다"와 갈린다.
	third, err := svc.Prescriptions(ctx(), sess)
	if err != nil {
		t.Fatalf("셋째 턴 실패: %v", err)
	}
	if len(third.All) != 0 {
		t.Fatalf("밀린 것이 다 나갔는데 처방이 %d건 더 떴다: %v\n"+
			"접힘이 끝난 턴에는 창이 정상적으로 밀려야 한다 — 안 그러면 매 턴 같은 것이 반복된다",
			len(third.All), keysOf(third.All))
	}
}

// 안 접힌 턴에는 안 남는다. 매 턴 남기면 이 이벤트를 세는 것이 곧 턴 수를 세는 것이 되어
// **접힌 턴의 비율**이라는 값이 다시 사라진다.
func TestUnfoldedTurnLeavesNoFoldedTrace(t *testing.T) {
	svc, st := newSvc(t)

	sess := openSessionForPrescribeTest(t, svc)
	touchPathForPrescribeTest(t, st, sess, "cmd/fd/hook.go")

	res, err := svc.Prescriptions(ctx(), sess)
	if err != nil {
		t.Fatalf("호출 실패: %v", err)
	}
	if res.Folded != 0 {
		t.Fatalf("경로 하나인데 접혔다 — 이 시험의 전제가 깨졌다: all=%d", len(res.All))
	}
	evs, err := st.ListSessionEvents(ctx(), sess, "prescribe_folded", time.Time{})
	if err != nil {
		t.Fatalf("접힘 이벤트 조회 실패: %v", err)
	}
	if len(evs) != 0 {
		t.Fatalf("안 접힌 턴에 접힘 이벤트가 %d건 남았다 — 접힌 턴의 비율을 다시 못 잰다", len(evs))
	}
}
