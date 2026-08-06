package judge

import (
	"strings"
	"testing"
)

// 처방이 **자기가 든 근거를 지목**하는지 보는 축이다.
//
// 판정이 맞아도 문구가 엉뚱한 경로를 대면 읽는 쪽이 오진한다. 실측(2026-08-05, context-platform
// 세션 01KZ85KS): 항목 다섯을 finish 한 직후 unclaimed 가 떴는데 문구가 댄
// `tools/gitleaks-config.test.sh` 는 **닫은 항목 둘이 선언한 경로**였고, 정작 안 덮인 것은
// `tools/gitleaks-allowlist.test.sh` 였다. 판정은 옳았고 증거만 틀렸다. 그 세션은 그것을
// 구조적 결함으로 오진해 원장에 판단 하나와 큐 항목 하나(철회됨)를 남겼다 —
// 안 덮인 경로 하나만 댔으면 `paths` 를 고치는 5초짜리 일이었다.

// 덮인 경로와 안 덮인 경로가 함께 있으면 문구는 **안 덮인 쪽**을 댄다.
func TestUnclaimedNamesTheUncoveredPathNotTheCoveredOne(t *testing.T) {
	const (
		covered   = "tools/gitleaks-config.test.sh"    // 닫은 항목이 선언했다
		uncovered = "tools/gitleaks-allowlist.test.sh" // 아무 항목도 선언 안 했다
	)
	p, ok := unclaimedPrescription(PrescribeInput{
		Now: pt0, SessionID: "me",
		// 덮인 쪽이 앞이다 — 문구가 앞에서부터 자르면 이것만 실린다(실측이 그 모양이었다).
		TurnPaths: []string{covered, uncovered},
		Closed:    []ClaimView{{ItemID: "t1-gitleaks-config", Paths: []string{covered}}},
	})
	if !ok {
		t.Fatal("안 덮인 경로가 있는데 처방이 안 떴다 — 판정 자체가 꺼졌다")
	}
	if !strings.Contains(p.Text, uncovered) {
		t.Errorf("문구가 안 덮인 경로를 안 댄다 — 고칠 자리를 못 가리킨다: %q", p.Text)
	}
	if strings.Contains(p.Text, covered) {
		t.Errorf("문구가 **덮인** 경로를 댄다 — 읽는 쪽이 거짓 양성으로 오진한다: %q", p.Text)
	}
	// 사유도 같은 것을 가리켜야 한다. 문구만 고치면 원장에 남는 근거가 여전히 틀리다.
	if strings.Contains(p.Reason, covered) || !strings.Contains(p.Reason, "1") {
		t.Errorf("사유가 안 덮인 경로 수를 안 센다: %q", p.Reason)
	}
}

// 안 덮인 것이 많으면 몇 개만 대고 **수를 붙인다.** 화면을 덮는 목록은 사유가 없는 것과 같다.
func TestUnclaimedClipsManyUncoveredPathsAndSaysHowMany(t *testing.T) {
	p, ok := unclaimedPrescription(PrescribeInput{
		Now: pt0, SessionID: "me",
		TurnPaths: []string{"a/1.go", "b/2.go", "c/3.go", "d/4.go", "cmd/fd/hook.go"},
		Closed:    []ClaimView{{ItemID: "fd-x", Paths: []string{"cmd/fd"}}},
	})
	if !ok {
		t.Fatal("처방이 안 떴다")
	}
	if strings.Contains(p.Text, "cmd/fd/hook.go") {
		t.Errorf("덮인 경로가 문구에 실렸다: %q", p.Text)
	}
	if !strings.Contains(p.Text, "외 1개") {
		t.Errorf("안 덮인 4개 중 3개만 대면서 남은 수를 안 말한다: %q", p.Text)
	}
}

// 끝낸 항목이 근거면 문구는 **그 상태**를 말하고, **실행 가능한** 행동을 대야 한다.
//
// ★ "paths 를 갱신해라"는 안 된다 — fd 에 항목의 등록 경로를 나중에 고치는 수단이 없다
// (`fd move` 는 프로젝트 축뿐이고 재등록은 409 다). 실행 불가능한 지시를 처방에 싣는 것은
// 이 결함(문구가 사람을 헛되이 움직인다)의 재발이다. 남는 것은 add 와 note 둘이다.
func TestUnclaimedAfterFinishNamesTheClosedItemAndAnActionThatExists(t *testing.T) {
	p, ok := unclaimedPrescription(PrescribeInput{
		Now: pt0, SessionID: "me",
		TurnPaths: []string{"cmd/fd/hook.go", "internal/store/item.go"},
		Closed:    []ClaimView{{ItemID: "fd-x", Paths: []string{"cmd/fd"}}},
	})
	if !ok {
		t.Fatal("처방이 안 떴다")
	}
	if !strings.Contains(p.Text, "fd-x") {
		t.Errorf("문구가 근거로 삼은 끝낸 항목을 안 댄다: %q", p.Text)
	}
	if strings.Contains(p.Text, "선점하지 않고") {
		t.Errorf("방금 제대로 끝낸 세션에게 '한 번도 안 집었다'로 말한다: %q", p.Text)
	}
	if !strings.Contains(p.Text, "add(") || !strings.Contains(p.Text, "note(") {
		t.Errorf("실행 가능한 행동을 안 댄다: %q", p.Text)
	}
	if strings.Contains(p.Text, "paths 를 갱신") || strings.Contains(p.Text, "paths를 갱신") {
		t.Errorf("존재하지 않는 수단을 처방했다 — 항목의 등록 경로는 나중에 못 고친다: %q", p.Text)
	}
}

// 근거가 **없으면** 옛 문구 그대로다. 안 덮인 집합이라는 말 자체가 성립하지 않는다.
//
// 두 갈래가 여기 온다: 닫은 항목이 없다(한 번도 안 집었다) · 비교 가능한 경로가 하나도 없다.
func TestUnclaimedWithoutGroundsKeepsNamingTheTurnPaths(t *testing.T) {
	t.Run("닫은 항목이 없다", func(t *testing.T) {
		p, ok := unclaimedPrescription(PrescribeInput{
			Now: pt0, SessionID: "me",
			TurnPaths: []string{"cmd/fd/hook.go"},
		})
		if !ok {
			t.Fatal("처방이 안 떴다")
		}
		if !strings.Contains(p.Text, "cmd/fd/hook.go") {
			t.Errorf("만진 경로를 안 댄다: %q", p.Text)
		}
		if !strings.Contains(p.Text, "선점하지 않고") {
			t.Errorf("한 번도 안 집은 세션에게 낼 문구가 아니다: %q", p.Text)
		}
	})

	// 비교 가능한 경로가 0이면 덮였다고도 안 덮였다고도 말할 수 없다. 처방은 뜨고(근거 0을
	// 통과로 접으면 축이 통째로 꺼진다) 문구는 만진 것을 그대로 댄다.
	t.Run("비교 가능한 경로가 하나도 없다", func(t *testing.T) {
		const abs = "/home/aaron/other-repo/x.go"
		p, ok := unclaimedPrescription(PrescribeInput{
			Now: pt0, SessionID: "me",
			TurnPaths: []string{abs},
			Closed:    []ClaimView{{ItemID: "fd-x", Paths: []string{"cmd/fd"}}},
		})
		if !ok {
			t.Fatal("근거가 0인데 덮였다고 판정했다 — 처방이 통째로 꺼진다")
		}
		if !strings.Contains(p.Text, abs) {
			t.Errorf("댈 수 있는 유일한 것(만진 경로)을 안 댄다: %q", p.Text)
		}
	})
}

// 비교 불가능한 경로는 **증거로도 안 쓴다.** 덮였는지 아닌지 판정할 수 없는 것을
// "안 덮였다"고 지목하면 그것이 바로 이 항목이 없애려는 거짓 증거다.
func TestUnclaimedDoesNotCiteUncomparablePathsAsUncovered(t *testing.T) {
	const abs = "/home/aaron/other-repo/x.go"
	p, ok := unclaimedPrescription(PrescribeInput{
		Now: pt0, SessionID: "me",
		TurnPaths: []string{"internal/store/item.go", abs},
		Closed:    []ClaimView{{ItemID: "fd-x", Paths: []string{"cmd/fd"}}},
	})
	if !ok {
		t.Fatal("비교 가능한 경로가 선언 밖인데 처방이 안 떴다")
	}
	if strings.Contains(p.Text, abs) {
		t.Errorf("판정 불가능한 좌표의 경로를 증거로 댔다: %q", p.Text)
	}
	if !strings.Contains(p.Text, "internal/store/item.go") {
		t.Errorf("실제로 안 덮인 경로를 안 댄다: %q", p.Text)
	}
}

// 이 축이 문구에서 끝나지 않게 잠근다 — Prescribe 전체를 지나도 같은 증거가 나와야 한다.
func TestUncoveredEvidenceSurvivesTheFullPrescribe(t *testing.T) {
	ps := Prescribe(PrescribeInput{
		Now: pt0, SessionID: "me",
		TurnPaths:    []string{"cmd/fd/hook.go", "internal/store/item.go"},
		Closed:       []ClaimView{{ItemID: "fd-x", Paths: []string{"cmd/fd"}}},
		LastJudgment: pt0, NewPaths: 2,
	})
	var found bool
	for _, p := range ps {
		if p.Key != PrescribeUnclaimed {
			continue
		}
		found = true
		if strings.Contains(p.Text, "cmd/fd/hook.go") {
			t.Errorf("덮인 경로가 문구에 실렸다: %q", p.Text)
		}
		if !strings.Contains(p.Text, "internal/store/item.go") {
			t.Errorf("안 덮인 경로를 안 댄다: %q", p.Text)
		}
	}
	if !found {
		t.Fatalf("unclaimed 처방이 안 나왔다: %v", keys(ps))
	}
}
