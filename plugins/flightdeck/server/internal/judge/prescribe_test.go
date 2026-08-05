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
		{
			// 발자국도 선점도 없는 세션이다 — 나머지 넷이 전부 안 도는 자리라
			// lane-turn 축만 남는다.
			name: "레인 차례가 오면 lane-turn 이 뜬다",
			in: PrescribeInput{
				Now: pt0, SessionID: "me", LaneTurnRow: 7, LastJudgment: pt0,
			},
			wantKeys: []string{"lane-turn:7"},
		},
		{
			name: "같은 줄 행에는 다시 안 뜬다",
			in: PrescribeInput{
				Now: pt0, SessionID: "me", LaneTurnRow: 7, LastJudgment: pt0,
				Emitted: map[string]time.Time{"lane-turn:7": pt0.Add(-time.Minute)},
			},
			wantKeys: nil,
		},
		{
			// ★ **억제 키에 줄 행 번호를 넣은 이유가 이 케이스다.** 차례를 받고 랜딩에
			//   실패한 세션은 굶주림 정책상 맨 뒤로 가서 새 줄 행을 받는다. 접미 없이
			//   `lane-turn` 하나만 쓰면 suppressed 가 그 키를 영구히 누르므로
			//   그 세션에게 두 번째 차례가 영영 안 뜨고, 그 뒤 줄 전원이 그만큼 선다.
			name: "새 줄 행에는 다시 뜬다",
			in: PrescribeInput{
				Now: pt0, SessionID: "me", LaneTurnRow: 9, LastJudgment: pt0,
				Emitted: map[string]time.Time{"lane-turn:7": pt0.Add(-time.Minute)},
			},
			wantKeys: []string{"lane-turn:9"},
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

// ★ **접힘 시험** — 상한을 넘겨 놓고 lane-turn 이 살아남는지 본다.
//
// 순서만 단정하는 표 케이스로는 이 축이 원리적으로 안 보인다. 표는 전부 상한 아래에서
// 돌아 lane-turn 이 목록 어디에 있든 초록이 나기 때문이다. 그런데 lane-turn 에게 접힘은
// **영구 소실**이다: 접힌 처방도 호출자가 전부 발화 기록하고(PrescribeMax 주석),
// suppressed 는 silent 외 모든 키를 무조건 누른다. 한 번 접히면 그 줄 행의 차례 통지는
// 다시 안 뜨고, 그 세션은 레인을 안 쥔 채 남아 **뒤에 선 전원의 랜딩이 선다.**
// 그리고 그 실패는 화면에 안 뜬다 — 원장에는 "정상적으로 접혔다"로만 남는다.
//
// overlap 을 상한만큼 까는 것이 억지 상황이 아니다: 발화 55건 중 31건이 overlap 이고
// 세션 7개에 몰렸다(PrescribeMax 주석의 실측). 한 턴 최대 발화는 6건이었다.
func TestLaneTurnSurvivesFolding(t *testing.T) {
	others := []LiveSession{
		{ID: "01SESSIONA", Paths: []string{"cmd/fd/hook.go"}},
		{ID: "01SESSIONB", Paths: []string{"cmd/fd/hook.go"}},
		{ID: "01SESSIONC", Paths: []string{"cmd/fd/hook.go"}},
	}
	in := PrescribeInput{
		Now: pt0, SessionID: "me",
		Claims:       []ClaimView{{ItemID: "fd-x", Paths: []string{"internal/judge"}}},
		TurnPaths:    []string{"cmd/fd/hook.go"},
		Others:       others,
		LaneTurnRow:  7,
		LastJudgment: pt0, NewPaths: 1,
	}

	all := Prescribe(in)
	// 순서 자체도 여기서 잠근다: lane-turn → overlap → outside.
	want := []string{"lane-turn:7", "overlap:01SESSIONA", "overlap:01SESSIONB", "overlap:01SESSIONC", "outside:cmd/fd/hook.go"}
	got := keys(all)
	if len(got) != len(want) {
		t.Fatalf("처방 구성이 다르다: got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("순서가 다르다(%d번째): got %v, want %v", i, got, want)
		}
	}

	shown, folded := FoldPrescriptions(all)
	if folded == 0 {
		t.Fatalf("접히지 않았다 — 이 시험이 겨냥한 상황 자체가 안 만들어졌다(처방 %d건, 상한 %d)", len(all), PrescribeMax)
	}
	found := false
	for _, p := range shown {
		if p.Key == "lane-turn:7" {
			found = true
		}
	}
	if !found {
		t.Fatalf("overlap %d건에 밀려 lane-turn 이 접혔다 — 그 줄 행의 차례 통지는 영구히 사라지고 뒤 줄 전원이 선다(shown=%v, 접힘 %d)",
			len(others), keys(shown), folded)
	}
	if shown[0].Key != "lane-turn:7" {
		t.Fatalf("lane-turn 이 맨 앞이 아니다 — overlap 이 하나만 더 늘어도 접힌다: %v", keys(shown))
	}
}
