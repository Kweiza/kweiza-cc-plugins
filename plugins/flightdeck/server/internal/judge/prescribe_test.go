package judge

import (
	"strings"
	"testing"
	"time"
)

var pt0 = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

// keys 는 처방 키만 뽑는다. 순서도 단정 대상이다.
func keys(ps []Prescription) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Key)
	}
	return out
}

func TestPrescribe(t *testing.T) {
	other := LiveSession{ID: "01SESSIONOTHER", Label: "", Paths: []string{"cmd/fd/hook.go"}}

	cases := []struct {
		name     string
		in       PrescribeInput
		wantKeys []string
	}{
		{
			name: "조사만 하는 세션 — 넷 다 안 뜬다",
			in: PrescribeInput{
				Now: pt0, SessionID: "me", TurnPaths: nil, Others: []LiveSession{other},
				LastJudgment: pt0.Add(-5 * time.Hour), NewPaths: 0,
			},
			wantKeys: nil,
		},
		{
			name: "남과 겹치기 시작했다",
			in: PrescribeInput{
				Now: pt0, SessionID: "me", TurnPaths: []string{"cmd/fd/hook.go"},
				Others: []LiveSession{other}, LastJudgment: pt0, NewPaths: 1,
			},
			wantKeys: []string{"overlap:01SESSIONOTHER", "unclaimed"},
		},
		{
			name: "같은 상대와 다시 겹쳐도 안 뜬다",
			in: PrescribeInput{
				Now: pt0, SessionID: "me", TurnPaths: []string{"cmd/fd/hook.go"},
				Others: []LiveSession{other}, LastJudgment: pt0, NewPaths: 1,
				Emitted: map[string]time.Time{
					"overlap:01SESSIONOTHER": pt0.Add(-time.Minute), "unclaimed": pt0.Add(-time.Minute),
				},
			},
			wantKeys: nil,
		},
		{
			name: "자기 자신과는 안 겹친다",
			in: PrescribeInput{
				Now: pt0, SessionID: "01SESSIONOTHER", TurnPaths: []string{"cmd/fd/hook.go"},
				Others: []LiveSession{other}, LastJudgment: pt0, NewPaths: 1,
			},
			wantKeys: []string{"unclaimed"},
		},
		{
			name: "선언 경로 밖 — 경로마다 하나",
			in: PrescribeInput{
				Now: pt0, SessionID: "me",
				Claims:       []ClaimView{{ItemID: "fd-x", Paths: []string{"internal/judge"}}},
				TurnPaths:    []string{"internal/judge/prescribe.go", "cmd/fd/hook.go"},
				LastJudgment: pt0, NewPaths: 2,
			},
			wantKeys: []string{"outside:cmd/fd/hook.go"},
		},
		{
			name: "선언 경로가 하나도 없으면 outside 축이 안 돈다",
			in: PrescribeInput{
				Now: pt0, SessionID: "me",
				Claims:       []ClaimView{{ItemID: "fd-x", Paths: nil}},
				TurnPaths:    []string{"cmd/fd/hook.go"},
				LastJudgment: pt0, NewPaths: 1,
			},
			wantKeys: nil,
		},
		{
			name: "선점이 있으면 unclaimed 는 안 뜬다",
			in: PrescribeInput{
				Now: pt0, SessionID: "me",
				Claims:    []ClaimView{{ItemID: "fd-x", Paths: []string{"cmd/fd"}}},
				TurnPaths: []string{"cmd/fd/hook.go"}, LastJudgment: pt0, NewPaths: 1,
			},
			wantKeys: nil,
		},
		{
			// ★ finish 로 항목을 제대로 닫으면 선점이 반납된다. 그 순간 Claims 가 비고,
			//   방금 끝낸 그 일의 경로를 근거로 "선점하지 않고 고치고 있다"가 뜬다 —
			//   **가장 성실하게 마무리한 세션이 가장 확실하게 잔소리를 듣는다.**
			//   "한 번도 안 집었다"와 "방금 제대로 끝냈다"는 다른 상태다.
			name: "방금 끝낸 항목의 경로에는 unclaimed 가 안 뜬다",
			in: PrescribeInput{
				Now: pt0, SessionID: "me",
				Closed:       []ClaimView{{ItemID: "fd-x", Paths: []string{"cmd/fd"}}},
				TurnPaths:    []string{"cmd/fd/hook.go"},
				LastJudgment: pt0, NewPaths: 1,
			},
			wantKeys: nil,
		},
		{
			// 그러나 끝낸 뒤 **다른** 일을 시작하면 뜬다. 그게 이 처방의 존재 이유다.
			name: "끝낸 항목의 경로 밖으로 새 일을 시작하면 unclaimed 가 뜬다",
			in: PrescribeInput{
				Now: pt0, SessionID: "me",
				Closed:       []ClaimView{{ItemID: "fd-x", Paths: []string{"cmd/fd"}}},
				TurnPaths:    []string{"internal/store/item.go"},
				LastJudgment: pt0, NewPaths: 1,
			},
			wantKeys: []string{"unclaimed"},
		},
		{
			// 경로를 선언 안 한 항목을 닫았으면 접을 근거가 없다 — 옛 동작 그대로 뜬다.
			// 빈 선언을 "전부 덮음"으로 접으면 paths 없는 항목 하나가 이 축을 통째로 끈다.
			name: "선언 경로가 없는 항목을 닫았으면 unclaimed 는 그대로 뜬다",
			in: PrescribeInput{
				Now: pt0, SessionID: "me",
				Closed:       []ClaimView{{ItemID: "fd-x", Paths: nil}},
				TurnPaths:    []string{"cmd/fd/hook.go"},
				LastJudgment: pt0, NewPaths: 1,
			},
			wantKeys: []string{"unclaimed"},
		},
		{
			// 끝낸 항목의 경로 **일부만** 덮으면 뜬다. 부분 일치로 접으면
			// 큰 항목 하나를 닫은 세션이 그 뒤 아무 일이나 해도 안 걸린다.
			name: "일부만 덮이면 unclaimed 가 뜬다",
			in: PrescribeInput{
				Now: pt0, SessionID: "me",
				Closed:       []ClaimView{{ItemID: "fd-x", Paths: []string{"cmd/fd"}}},
				TurnPaths:    []string{"cmd/fd/hook.go", "internal/store/item.go"},
				LastJudgment: pt0, NewPaths: 2,
			},
			wantKeys: []string{"unclaimed"},
		},
		{
			name: "silent — 경로 임계",
			in: PrescribeInput{
				Now: pt0, SessionID: "me",
				Claims:    []ClaimView{{ItemID: "fd-x", Paths: []string{"cmd/fd"}}},
				TurnPaths: []string{"cmd/fd/hook.go"}, LastJudgment: pt0, NewPaths: SilentNewPaths,
			},
			wantKeys: []string{"silent"},
		},
		{
			name: "silent — 시간 임계는 새 경로가 있어야 걸린다",
			in: PrescribeInput{
				Now: pt0, SessionID: "me",
				Claims:       []ClaimView{{ItemID: "fd-x", Paths: []string{"cmd/fd"}}},
				TurnPaths:    []string{"cmd/fd/hook.go"},
				LastJudgment: pt0.Add(-SilentGap), NewPaths: 1,
			},
			wantKeys: []string{"silent"},
		},
		{
			name: "silent — 시간이 지나도 새 경로가 0이면 안 뜬다",
			in: PrescribeInput{
				Now: pt0, SessionID: "me",
				Claims:       []ClaimView{{ItemID: "fd-x", Paths: []string{"cmd/fd"}}},
				LastJudgment: pt0.Add(-10 * SilentGap), NewPaths: 0,
			},
			wantKeys: nil,
		},
		{
			name: "silent 은 판단 뒤에 다시 뜬다",
			in: PrescribeInput{
				Now: pt0, SessionID: "me",
				Claims:    []ClaimView{{ItemID: "fd-x", Paths: []string{"cmd/fd"}}},
				TurnPaths: []string{"cmd/fd/hook.go"}, NewPaths: SilentNewPaths,
				LastJudgment: pt0.Add(-time.Minute),
				Emitted:      map[string]time.Time{"silent": pt0.Add(-2 * time.Minute)},
			},
			wantKeys: []string{"silent"},
		},
		{
			name: "silent 은 무시하면 안 다시 뜬다",
			in: PrescribeInput{
				Now: pt0, SessionID: "me",
				Claims:    []ClaimView{{ItemID: "fd-x", Paths: []string{"cmd/fd"}}},
				TurnPaths: []string{"cmd/fd/hook.go"}, NewPaths: SilentNewPaths,
				LastJudgment: pt0.Add(-3 * time.Minute),
				Emitted:      map[string]time.Time{"silent": pt0.Add(-2 * time.Minute)},
			},
			wantKeys: nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := keys(Prescribe(c.in))
			if len(got) != len(c.wantKeys) {
				t.Fatalf("키 수가 다르다: got %v, want %v", got, c.wantKeys)
			}
			for i := range got {
				if got[i] != c.wantKeys[i] {
					t.Fatalf("키가 다르다(%d번째): got %v, want %v", i, got, c.wantKeys)
				}
			}
			// **사유가 비면 실패다.** 결과만 찍는 단정을 통과시키지 않는다(설계 §12).
			for _, p := range Prescribe(c.in) {
				if strings.TrimSpace(p.Reason) == "" {
					t.Fatalf("사유가 비었다: key=%s", p.Key)
				}
				if strings.TrimSpace(p.Text) == "" {
					t.Fatalf("문구가 비었다: key=%s", p.Key)
				}
			}
		})
	}
}

// 문구가 무엇을 부를지를 실제로 말하는지 본다. 소비자가 읽는 것은 이 문자열이다.
func TestPrescribeTextNamesTheCall(t *testing.T) {
	ps := Prescribe(PrescribeInput{
		Now: pt0, SessionID: "me", TurnPaths: []string{"cmd/fd/hook.go"},
		Others:       []LiveSession{{ID: "01OTHER", Paths: []string{"cmd/fd/hook.go"}}},
		LastJudgment: pt0, NewPaths: 1,
	})
	if len(ps) == 0 {
		t.Fatal("처방이 하나도 안 나왔다")
	}
	if !strings.Contains(ps[0].Text, "note(kind='ask'") {
		t.Fatalf("겹침 처방이 부를 도구를 안 말한다: %q", ps[0].Text)
	}
	if !strings.Contains(ps[0].Text, "cmd/fd/hook.go") {
		t.Fatalf("겹침 처방이 경로를 안 말한다: %q", ps[0].Text)
	}
	if !strings.Contains(ps[0].Text, "01OTHER") {
		t.Fatalf("겹침 처방이 상대를 안 말한다: %q", ps[0].Text)
	}
}

func TestFoldPrescriptions(t *testing.T) {
	mk := func(n int) []Prescription {
		out := make([]Prescription, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, Prescription{Key: "k", Reason: "r", Text: "t"})
		}
		return out
	}
	shown, folded := FoldPrescriptions(mk(2))
	if len(shown) != 2 || folded != 0 {
		t.Fatalf("상한 아래인데 접었다: shown=%d folded=%d", len(shown), folded)
	}
	shown, folded = FoldPrescriptions(mk(10))
	if len(shown) != PrescribeMax || folded != 10-PrescribeMax {
		t.Fatalf("상한을 안 지켰다: shown=%d folded=%d", len(shown), folded)
	}
}
