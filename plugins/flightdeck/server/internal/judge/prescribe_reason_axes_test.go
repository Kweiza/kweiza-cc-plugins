package judge

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// 겹침 축에만 잠갔던 **변이 부류**를 나머지 축으로 넓힌다.
//
// `TestOverlapReasonPinsMineTheirsAndPairCount` 가 overlap 에 대해 셋을 잠갔다 —
// 좌우 교환 · 첫 쌍 대신 마지막 쌍 · 개수 ±1. 그 부류는 overlap 고유가 아니다:
// 어느 축이든 사유·문구가 **두 값을 나란히 놓는 자리**가 있으면 교환이 살아남고,
// 그때 세션은 자기가 만지지도 않은 것을 근거로 행동한다.
//
// ★ **실측이다**(2026-08-09, main c127421 에 변이를 주입하고 judge·service 를 돌렸다).
// 아홉 중 여섯이 **초록으로 살아남았다**:
//
//	살아남음  outside 사유 좌우 교환 · outside 문구의 add paths 가 남의 경로 ·
//	         unclaimed 두 수 교환 · silent 경로 임계 인자 교환 · silent 간격 인자 교환 ·
//	         clipList 경계 `<=` → `<`
//	죽음     unclaimed 증거를 TurnPaths 전부로 · unclaimed ids 를 Claims 로 ·
//	         clipList 잔여 수를 전체 수로
//
// 죽은 셋을 잡은 것은 `prescribe_evidence_test.go` 다(2026-08-06 개정의 산물). 그 파일은
// unclaimed 의 **어느 경로를 대나**를 잠갔고, 여기서 잠그는 것은 **그 옆에 붙는 수**와
// **다른 두 축**이다 — 겹치지 않는다.
//
// ★ 잠그는 것은 표현이 아니라 **행동 가능한 정보**뿐이다. 각 시험이 부정 대조를 함께
// 두는 이유는 그것이 변이가 만들어 내는 실제 문자열이기 때문이다.

// outside 는 **내가 만진 경로**와 **선점이 선언한 범위**를 나란히 놓는다. 그 둘이 바뀌면
// 사유는 "선언한 경로가 선언 경로 밖이다"라는 자기모순이 되고, 문구의 `add(... paths=[…])`
// 는 **이미 선언된 경로로 새 항목을 만들라**고 시킨다 — 그대로 따르면 겹침이 생긴다.
//
// 겹침 시험이 조상 관계를 쓴 것과 같은 이유로 두 값을 실제로 다르게 둔다.
func TestOutsideReasonPinsTheTouchedPathAndTheDeclaredScope(t *testing.T) {
	const (
		touched  = "cmd/fd/hook.go" // 선언 밖이다 — 사유의 왼쪽
		declared = "internal/judge" // 선점이 선언했다 — 오른쪽
		itemID   = "fd-x"
	)
	ps := outsidePrescriptions(PrescribeInput{
		Now: pt0, SessionID: "me",
		Claims:    []ClaimView{{ItemID: itemID, Paths: []string{declared}}},
		TurnPaths: []string{touched},
	})
	if len(ps) != 1 {
		t.Fatalf("outside 처방 하나만 나와야 한다: %v", keys(ps))
	}
	p := ps[0]

	for _, w := range []struct{ where, want, why string }{
		{"사유", touched + " 는 선점 항목", "내가 만진 경로가 왼쪽이다"},
		{"사유", "선언 경로(" + declared + ")", "선점이 선언한 범위가 오른쪽이다"},
		{"문구", touched + " 는 선점한 ", "세션이 읽는 문자열에서도 좌우가 같아야 한다"},
		{"문구", "paths=['" + touched + "']", "새 항목이 선언할 것은 **안 덮인** 경로다"},
	} {
		got := p.Reason
		if w.where == "문구" {
			got = p.Text
		}
		if !strings.Contains(got, w.want) {
			t.Errorf("%s가 %q 를 안 말한다 — %s:\n%s", w.where, w.want, w.why, got)
		}
	}

	// 부정 대조 — 좌우가 바뀌면 정확히 이 조각들이 나타난다.
	if strings.Contains(p.Reason, declared+" 는 선점 항목") {
		t.Errorf("사유가 선언 경로를 '밖'이라고 말한다 — 자기모순이다:\n%s", p.Reason)
	}
	if strings.Contains(p.Reason, "선언 경로("+touched+")") {
		t.Errorf("사유가 방금 만진 경로를 선언 범위라고 말한다:\n%s", p.Reason)
	}
	if strings.Contains(p.Text, "paths=['"+declared+"']") {
		t.Errorf("문구가 **이미 선언된** 경로로 새 항목을 만들라고 시킨다 — 그대로 따르면 겹침이 생긴다:\n%s", p.Text)
	}
}

// 선언 경로가 여럿이면 **전부** 사유에 실린다. 하나만 실으면 읽는 쪽은 나머지 선언을
// 못 보고 "왜 저기가 밖이지"에 답할 수 없다.
func TestOutsideReasonNamesEveryDeclaredPath(t *testing.T) {
	const (
		first  = "internal/judge"
		second = "internal/store"
	)
	ps := outsidePrescriptions(PrescribeInput{
		Now: pt0, SessionID: "me",
		Claims:    []ClaimView{{ItemID: "fd-x", Paths: []string{first, second}}},
		TurnPaths: []string{"cmd/fd/hook.go"},
	})
	if len(ps) != 1 {
		t.Fatalf("outside 처방 하나만 나와야 한다: %v", keys(ps))
	}
	for _, want := range []string{first, second} {
		if !strings.Contains(ps[0].Reason, want) {
			t.Errorf("사유가 선언 경로 %q 를 빠뜨렸다 — 읽는 쪽이 판정 근거를 재구성할 수 없다:\n%s",
				want, ps[0].Reason)
		}
	}
}

// unclaimed 사유는 **안 덮인 수**와 **이번 구간 전체 수**를 나란히 놓는다.
// 그 둘이 바뀌면 "5개 중 2개"가 "2개 중 5개"가 되어 부분이 전체보다 커진다 —
// 읽는 쪽은 그 수로 "얼마나 새어 나갔나"를 가늠하므로 뒤집히면 판단이 정반대가 된다.
//
// ★ `prescribe_evidence_test.go` 는 **어느 경로를 대나**를 잠갔지 이 두 수는 안 본다.
// 그 파일의 `!strings.Contains(p.Reason, "1")` 은 교환된 사유에도 여전히 참이다.
func TestUnclaimedReasonPinsUncoveredCountAgainstTurnCount(t *testing.T) {
	p, ok := unclaimedPrescription(PrescribeInput{
		Now: pt0, SessionID: "me",
		// 다섯 중 셋은 덮이고 둘이 안 덮인다 — 두 수가 서로 달라야 교환이 보인다.
		TurnPaths: []string{"a/1.go", "b/2.go", "cmd/fd/x.go", "cmd/fd/y.go", "cmd/fd/z.go"},
		Closed:    []ClaimView{{ItemID: "fd-x", Paths: []string{"cmd/fd"}}},
	})
	if !ok {
		t.Fatal("안 덮인 경로가 둘인데 처방이 안 떴다 — 이 시험의 전제가 깨졌다")
	}
	const (
		want  = "경로 2개를 편집했다(이번 구간 5개 중)"
		never = "경로 5개를 편집했다(이번 구간 2개 중)"
	)
	if !strings.Contains(p.Reason, want) {
		t.Errorf("사유가 %q 를 안 말한다 — 안 덮인 수와 구간 전체 수가 이 축의 값이다:\n%s", want, p.Reason)
	}
	if strings.Contains(p.Reason, never) {
		t.Errorf("사유가 %q 라고 말한다 — 안 덮인 수가 구간 전체보다 크다는 뜻이 되어 "+
			"읽는 쪽이 새어 나간 양을 정반대로 읽는다:\n%s", never, p.Reason)
	}
}

// silent 사유는 **잰 값**과 **임계**를 나란히 놓는다. 두 팔 다 그렇다.
// 바뀌면 임계를 넘긴 정도가 뒤집혀 보이고, 설계 §10 이 요구하는 "떨어지면 조건을 줄인다"는
// 교정이 엉뚱한 방향으로 간다 — 원장에 남는 것이 이 문자열이다.
//
// ★ 상수를 시험에 박지 않는다. `SilentNewPaths`·`SilentGap` 을 기준으로 값을 만들어야
// 임계를 조정한 날 이 시험이 **거짓으로** 깨지지 않는다.
func TestSilentReasonPinsTheMeasuredValueAgainstItsThreshold(t *testing.T) {
	t.Run("경로 팔", func(t *testing.T) {
		const over = 5
		p, ok := silentPrescription(PrescribeInput{
			Now: pt0, SessionID: "me",
			NewPaths: SilentNewPaths + over, LastJudgment: pt0,
		})
		if !ok {
			t.Fatal("임계를 넘겼는데 silent 가 안 떴다 — 이 시험의 전제가 깨졌다")
		}
		want := fmt.Sprintf("새 경로 %d개(임계 %d)", SilentNewPaths+over, SilentNewPaths)
		never := fmt.Sprintf("새 경로 %d개(임계 %d)", SilentNewPaths, SilentNewPaths+over)
		if !strings.Contains(p.Reason, want) {
			t.Errorf("사유가 %q 를 안 말한다:\n%s", want, p.Reason)
		}
		if strings.Contains(p.Reason, never) {
			t.Errorf("사유가 %q 라고 말한다 — 잰 값과 임계가 바뀌어 임계 미달로 읽힌다:\n%s", never, p.Reason)
		}
	})

	t.Run("시간 팔", func(t *testing.T) {
		const over = 35 * time.Minute
		gap := SilentGap + over
		p, ok := silentPrescription(PrescribeInput{
			Now: pt0, SessionID: "me",
			// 경로 팔을 안 타도록 임계 아래로 두되 0 은 아니게 둔다(시간 팔의 조건이다).
			NewPaths: 3, LastJudgment: pt0.Add(-gap),
		})
		if !ok {
			t.Fatal("간격이 임계를 넘겼는데 silent 가 안 떴다 — 이 시험의 전제가 깨졌다")
		}
		want := fmt.Sprintf("%d분(임계 %d분)", int(gap.Minutes()), int(SilentGap.Minutes()))
		never := fmt.Sprintf("%d분(임계 %d분)", int(SilentGap.Minutes()), int(gap.Minutes()))
		if !strings.Contains(p.Reason, want) {
			t.Errorf("사유가 %q 를 안 말한다:\n%s", want, p.Reason)
		}
		if strings.Contains(p.Reason, never) {
			t.Errorf("사유가 %q 라고 말한다 — 잰 간격과 임계가 바뀌었다:\n%s", never, p.Reason)
		}
	})
}

// 증거 목록은 상한에서 **정확히** 잘린다. 안 덮인 것이 상한과 같으면 전부 실리고
// "외 N개" 는 안 붙는다.
//
// ★ 기존 시험(`TestUnclaimedClipsManyUncoveredPathsAndSaysHowMany`)은 상한을 **넘긴**
// 쪽만 본다. 경계 자체(`len(xs) <= n`)를 `<` 로 바꾸는 변이는 그 시험을 그대로 통과하고,
// 남기는 것은 "외 0개" 라는 **아무것도 안 말하는 꼬리**다.
func TestUncoveredEvidenceStopsClippingExactlyAtTheLimit(t *testing.T) {
	// 안 덮인 것이 정확히 셋 — 상한과 같다.
	p, ok := unclaimedPrescription(PrescribeInput{
		Now: pt0, SessionID: "me",
		TurnPaths: []string{"a/1.go", "b/2.go", "c/3.go", "cmd/fd/x.go"},
		Closed:    []ClaimView{{ItemID: "fd-x", Paths: []string{"cmd/fd"}}},
	})
	if !ok {
		t.Fatal("안 덮인 경로가 셋인데 처방이 안 떴다 — 이 시험의 전제가 깨졌다")
	}
	for _, want := range []string{"a/1.go", "b/2.go", "c/3.go"} {
		if !strings.Contains(p.Text, want) {
			t.Errorf("상한과 같은 수인데 %q 가 빠졌다: %q", want, p.Text)
		}
	}
	if strings.Contains(p.Text, "외 ") {
		t.Errorf("잘린 것이 없는데 잔여 꼬리가 붙었다 — 경계가 하나 어긋났다: %q", p.Text)
	}
}
