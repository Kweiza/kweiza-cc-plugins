package judge

import (
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

func TestAfterSatisfied(t *testing.T) {
	facts := AfterFacts{
		ItemStates: map[string]model.ItemState{
			"t5-done":    model.ItemDone,
			"t5-open":    model.ItemOpen,
			"t5-claimed": model.ItemClaimed,
			"t5-dropped": model.ItemDropped,
			"t5-weird":   model.ItemState("half-done"), // 열거 밖 (스키마와 코드가 어긋난 경우)
		},
		JobStates: map[string]string{
			"j-ok":       "ok",
			"j-bypassed": "bypassed",
			"j-queued":   "queued",
			"j-running":  "running",
			"j-fail":     "fail",
			"j-stalled":  "stalled",
			"j-weird":    "훗날",
		},
		SHAAncestry: map[string]AncestryResult{
			"aaaaaaa": AncestryYes,
			"bbbbbbb": AncestryNo,
			"ccccccc": AncestryBadRef,
			"ddddddd": AncestryUnknown,
			"eeeeeee": AncestryResult(99), // 열거 밖
		},
	}

	cases := []struct {
		name      string
		after     []model.After
		wantOK    bool
		wantCodes []string
	}{
		// ── 충족 ──
		{"선행이 없으면 충족", nil, true, nil},
		{"끝난 항목", []model.After{{Item: "t5-done"}}, true, nil},
		{"성공한 잡", []model.After{{Job: "j-ok"}}, true, nil},
		{"사람이 우회 기록한 잡", []model.After{{Job: "j-bypassed"}}, true, nil},
		{"조상인 sha", []model.After{{SHA: "aaaaaaa"}}, true, nil},

		// ── 기다리면 풀리는 것 ──
		{"열린 항목", []model.After{{Item: "t5-open"}}, false, []string{AfterUnmetItem}},
		{"남이 집은 항목", []model.After{{Item: "t5-claimed"}}, false, []string{AfterUnmetItem}},
		{"대기 중인 잡", []model.After{{Job: "j-queued"}}, false, []string{AfterUnmetJob}},
		{"도는 중인 잡", []model.After{{Job: "j-running"}}, false, []string{AfterUnmetJob}},
		{"아직 조상이 아닌 sha", []model.After{{SHA: "bbbbbbb"}}, false, []string{AfterUnmetSHA}},

		// ── 기다려도 안 풀리는 것 (이 갈래가 이 함수의 존재 이유다) ──
		{"폐기된 선행 항목", []model.After{{Item: "t5-dropped"}}, false, []string{AfterDroppedDep}},
		{"없는 ref (git rc=128)", []model.After{{SHA: "ccccccc"}}, false, []string{AfterBadRef}},
		{"실패한 잡", []model.After{{Job: "j-fail"}}, false, []string{AfterFailedJob}},
		{"정지한 잡", []model.After{{Job: "j-stalled"}}, false, []string{AfterFailedJob}},

		// ── 판정 자체를 못 한 것 ──
		{"조회하지 않은 sha", []model.After{{SHA: "ddddddd"}}, false, []string{AfterUnknown}},
		{"맵에 아예 없는 sha", []model.After{{SHA: "fffffff"}}, false, []string{AfterUnknown}},
		{"맵에 없는 항목", []model.After{{Item: "t5-없음"}}, false, []string{AfterUnknown}},
		{"맵에 없는 잡", []model.After{{Job: "j-없음"}}, false, []string{AfterUnknown}},

		// ── 표 밖: 스키마 CHECK 를 우회해 들어온 입력 ──
		{"셋 다 비었다", []model.After{{}}, false, []string{AfterMalformed}},
		{"둘이 찼다", []model.After{{Item: "t5-done", SHA: "aaaaaaa"}}, false, []string{AfterMalformed}},
		{"셋 다 찼다", []model.After{{Item: "t5-done", Job: "j-ok", SHA: "aaaaaaa"}}, false, []string{AfterMalformed}},

		// ── 표 밖: 열거에 없는 상태 문자열 ──
		{"항목 상태가 열거 밖", []model.After{{Item: "t5-weird"}}, false, []string{AfterBadState}},
		{"잡 상태가 열거 밖", []model.After{{Job: "j-weird"}}, false, []string{AfterBadState}},
		{"조상 판정값이 열거 밖", []model.After{{SHA: "eeeeeee"}}, false, []string{AfterBadState}},

		// ── 표 밖: 여러 선행 ──
		{
			"충족과 미충족이 섞이면 미충족만 사유가 된다",
			[]model.After{{Item: "t5-done"}, {Item: "t5-open"}, {SHA: "aaaaaaa"}},
			false, []string{AfterUnmetItem},
		},
		{
			"미충족이 여럿이면 전부 낸다 — 첫 사유에서 끊지 않는다",
			[]model.After{{Item: "t5-open"}, {SHA: "ccccccc"}, {Job: "j-fail"}},
			false, []string{AfterUnmetItem, AfterBadRef, AfterFailedJob},
		},
		{
			"전부 충족",
			[]model.After{{Item: "t5-done"}, {Job: "j-ok"}, {SHA: "aaaaaaa"}},
			true, nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, reasons := AfterSatisfied(c.after, facts)
			if ok != c.wantOK {
				t.Errorf("충족 여부 = %v, 기대 = %v (사유 %v)", ok, c.wantOK, reasons)
			}
			if len(reasons) != len(c.wantCodes) {
				t.Fatalf("사유 %d건, 기대 %d건: %v", len(reasons), len(c.wantCodes), reasons)
			}
			for i, r := range reasons {
				code, detail := SplitReason(r)
				if code != c.wantCodes[i] {
					t.Errorf("사유[%d] 코드 = %q, 기대 = %q (전문 %q)", i, code, c.wantCodes[i], r)
				}
				if detail == "" {
					t.Errorf("사유[%d] 에 상세가 없다: %q — 어느 선행인지 말하지 못한다", i, r)
				}
			}
			if ok && len(reasons) != 0 {
				t.Errorf("충족인데 사유가 남았다: %v", reasons)
			}
		})
	}
}

// 이 시험이 이 파일의 핵심이다.
//
// "아직 안 됐다"(rc=1)와 "그런 ref 가 없다"(rc=128)와 "폐기됐다"와 "조회 안 했다"가
// **각각 다른 문자열**이어야 한다. 하나라도 같으면 항목 하나가 영구히 굶는데
// 출력에는 "기다리면 된다"로만 보인다 — 기존 도구가 정확히 그 상태였다.
func TestAfterReasonsAreFiveDistinctStrings(t *testing.T) {
	facts := AfterFacts{
		ItemStates: map[string]model.ItemState{
			"dep-open":    model.ItemOpen,
			"dep-dropped": model.ItemDropped,
		},
		SHAAncestry: map[string]AncestryResult{
			"bbbbbbb": AncestryNo,
			"ccccccc": AncestryBadRef,
			"ddddddd": AncestryUnknown,
		},
	}
	after := []model.After{
		{Item: "dep-open"},    // after-unmet-item
		{Item: "dep-dropped"}, // after-dropped-dep
		{SHA: "bbbbbbb"},      // after-unmet-sha
		{SHA: "ccccccc"},      // after-bad-ref
		{SHA: "ddddddd"},      // after-unknown
	}

	ok, reasons := AfterSatisfied(after, facts)
	if ok {
		t.Fatalf("대조가 성립하지 않는다 — 이 입력은 전부 미충족이어야 한다")
	}
	if len(reasons) != 5 {
		t.Fatalf("사유 5건을 기대했는데 %d건: %v", len(reasons), reasons)
	}

	codes := map[string][]string{}
	for _, r := range reasons {
		code, _ := SplitReason(r)
		codes[code] = append(codes[code], r)
	}
	if len(codes) != 5 {
		t.Errorf("사유 코드가 %d종으로 뭉개졌다(5종이어야 한다): %v", len(codes), codes)
	}

	// 사유 전문도 서로 달라야 한다. 코드만 다르고 문장이 같으면 사람이 읽는 자리에서 다시 뭉개진다.
	seen := map[string]bool{}
	for _, r := range reasons {
		if seen[r] {
			t.Errorf("사유 전문이 겹친다: %q", r)
		}
		seen[r] = true
	}
}

// rc=128 은 "아직"이 아니라 "영영"이다. 사유 문장이 그것을 말해야 한다 —
// 코드만 다르고 문장이 "아직 안 됐다"면 읽는 사람은 여전히 기다린다.
func TestBadRefReasonSaysItWillNeverResolve(t *testing.T) {
	_, reasons := AfterSatisfied(
		[]model.After{{SHA: "ccccccc"}},
		AfterFacts{SHAAncestry: map[string]AncestryResult{"ccccccc": AncestryBadRef}},
	)
	if len(reasons) != 1 {
		t.Fatalf("사유 1건을 기대했는데 %v", reasons)
	}
	r := reasons[0]
	if !strings.Contains(r, "ccccccc") {
		t.Errorf("어느 sha 인지 안 적혀 있다: %q", r)
	}
	if !strings.Contains(r, "128") {
		t.Errorf("git 종료코드가 안 적혀 있다 — 조사할 좌표가 없다: %q", r)
	}
	if !strings.Contains(r, "기다려도") {
		t.Errorf("영영 안 풀린다는 사실이 문장에 없다: %q", r)
	}
}

func TestAncestryUnknownIsNotNo(t *testing.T) {
	// 조회를 안 했다는 사실이 "아니다"로 보이면, 조회를 빠뜨린 버그가 정상적인 대기로 보인다.
	_, unknown := AfterSatisfied([]model.After{{SHA: "x"}}, AfterFacts{})
	_, no := AfterSatisfied([]model.After{{SHA: "x"}},
		AfterFacts{SHAAncestry: map[string]AncestryResult{"x": AncestryNo}})

	uc, _ := SplitReason(unknown[0])
	nc, _ := SplitReason(no[0])
	if uc == nc {
		t.Errorf("미조회와 미충족이 같은 코드다: %q", uc)
	}
}

func TestAncestryResultStringsAreDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range []AncestryResult{AncestryUnknown, AncestryYes, AncestryNo, AncestryBadRef} {
		s := a.String()
		if seen[s] {
			t.Errorf("AncestryResult 표기가 겹친다: %q", s)
		}
		seen[s] = true
	}
	// 열거 밖 값도 침묵하지 않는다.
	if got := AncestryResult(42).String(); !strings.Contains(got, "42") {
		t.Errorf("열거 밖 값이 값을 안 실었다: %q", got)
	}
}
