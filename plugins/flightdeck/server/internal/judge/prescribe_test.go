package judge

import (
	"fmt"
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
			name: "조사만 하는 세션 — 다섯 다 안 뜬다",
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
// **한 턴 지연**이다(2026-08-06 개정 전에는 영구 소실이었다 — PrescribeMax 주석 참고.
// 지금은 표시분만 발화 기록하므로 접힌 것이 다음 턴에 올라온다). 그 한 턴 동안 그 세션은
// 레인을 안 쥔 채 남고 **뒤에 선 전원의 랜딩이 그만큼 선다** — 소실은 아니지만 **그 한 턴은
// 남의 시간**이라 맨 앞이 여전히 값을 한다. 그리고 그 지연은 화면에 안 뜬다 —
// 원장에는 "정상적으로 접혔다"로만 남는다.
//
// overlap 을 상한만큼 까는 것이 억지 상황이 아니다: 발화 55건 중 31건이 overlap 이고
// 세션 7개에 몰렸다(PrescribeMax 주석의 2026-08-04 기준선). 그 기준선 당시 한 턴 최대
// 발화는 6건이었고, 2026-08-06 재측에서는 7건이다.
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
		t.Fatalf("overlap %d건에 밀려 lane-turn 이 접혔다 — 그 줄 행의 차례 통지가 한 턴 늦고 그동안 뒤 줄 전원이 선다(shown=%v, 접힘 %d)",
			len(others), keys(shown), folded)
	}
	if shown[0].Key != "lane-turn:7" {
		t.Fatalf("lane-turn 이 맨 앞이 아니다 — overlap 이 하나만 더 늘어도 접힌다: %v", keys(shown))
	}
}

// ★ **축 순서 전체를 잠근다.**
//
// 위 표는 어느 줄도 축을 **둘 넘게** 내지 않는다. 그래서 순서 교환 변이가 표를 그대로
// 통과한다 — 변이 스윕이 찾았고 이 시험을 넣기 전에 재현했다(2026-08-06): Prescribe 에서
// `unclaimed ↔ silent` 를 바꾸면 이 패키지 전체가 초록이었다. (`overlap ↔ outside` 는
// TestLaneTurnSurvivesFolding 이 잡는다. 다만 그 시험은 lane-turn 의 자리를 겨냥한 것이라
// 나머지 쌍은 안 본다.)
//
// 순서는 표시 취향이 아니다. FoldPrescriptions 가 `ps[:PrescribeMax]` 로 **뒤를 자르므로**
// 순서가 곧 **무엇을 버리느냐**이고, lane-turn 을 맨 앞에 둔 결정 전체가 그것이다.
//
// ★ **다섯이 동시에 뜨는 입력은 존재하지 않는다.** outside 는 선언 경로가 있는 선점을
// 요구하고(outsidePrescriptions 는 declared 가 비면 nil), unclaimed 는 선점 0건을 요구한다
// (unclaimedPrescription 의 첫 가드) — 배타다. 그래서 한 번에 뜨는 최대는 넷이고, 최대
// 모양 **둘**로 나눠 잠근다. 둘을 합치면 outside↔unclaimed 를 뺀 모든 쌍이 잠기고, 그 한 쌍은
// 애초에 출력에 함께 나타날 수 없으므로 잠글 대상 자체가 없다.
func TestAxisOrderIsLockedWhereverTwoAxesCanCoexist(t *testing.T) {
	other := LiveSession{ID: "01SESSIONA", Paths: []string{"cmd/fd/hook.go"}}

	cases := []struct {
		name string
		in   PrescribeInput
		want []string
	}{
		{
			name: "선점 있음 — lane-turn → overlap → outside → silent",
			in: PrescribeInput{
				Now: pt0, SessionID: "me",
				Claims:       []ClaimView{{ItemID: "fd-x", Paths: []string{"internal/judge"}}},
				TurnPaths:    []string{"cmd/fd/hook.go"},
				Others:       []LiveSession{other},
				LaneTurnRow:  7,
				LastJudgment: pt0, NewPaths: SilentNewPaths,
			},
			want: []string{"lane-turn:7", "overlap:01SESSIONA", "outside:cmd/fd/hook.go", "silent"},
		},
		{
			name: "선점 없음 — lane-turn → overlap → unclaimed → silent",
			in: PrescribeInput{
				Now: pt0, SessionID: "me",
				TurnPaths:    []string{"cmd/fd/hook.go"},
				Others:       []LiveSession{other},
				LaneTurnRow:  7,
				LastJudgment: pt0, NewPaths: SilentNewPaths,
			},
			want: []string{"lane-turn:7", "overlap:01SESSIONA", "unclaimed", "silent"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := keys(Prescribe(c.in))
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Fatalf("축 순서가 다르다:\n got %v\nwant %v", got, c.want)
			}

			// 순서가 정하는 것은 표시가 아니라 **뒤로 미룰 것**이다. 상한을 넘는 만큼이 맨
			// 뒤에서 떨어져야 하고, 여기서 떨어지는 것은 silent 다 — 판단 뒤 해제 규칙까지
			// 가진 유일한 축이라(suppressed) 입력이 다시 안 생겨도 돌아오는 자리는 그것뿐이다.
			if PrescribeMax >= len(c.want) {
				return // 상한이 이 목록을 못 넘으면 접힘에 대해 말할 것이 없다
			}
			shown, folded := FoldPrescriptions(Prescribe(c.in))
			if folded != len(c.want)-PrescribeMax {
				t.Fatalf("접힌 수가 다르다: folded=%d, 기대 %d", folded, len(c.want)-PrescribeMax)
			}
			if strings.Join(keys(shown), ",") != strings.Join(c.want[:PrescribeMax], ",") {
				t.Fatalf("맨 뒤가 아니라 다른 것이 접혔다: shown=%v, 기대 %v", keys(shown), c.want[:PrescribeMax])
			}
		})
	}
}

// ★ **세션이 실제로 읽는 것은 Text 하나다** — Reason 은 원장에, Key 는 억제에 쓰인다.
// 그런데 위 표가 Text 에 대해 잠그는 것은 "비어 있지 않다" 하나뿐이라, 없는 도구를 부르라고
// 하거나 줄 행 번호를 빠뜨려도 전부 초록이다(overlap 문구는 TestPrescribeTextNamesTheCall 이
// 잡는다. lane-turn 문구에는 그런 자리가 없었다).
//
// 잠그는 것은 **행동 가능한 정보 둘뿐**이다. 표현을 바꾸면 깨지는 시험은 나쁘다:
//
//	① 부를 도구와 인자 — land() · land(result='ok') · land(leave=…).
//	   셋 다 실재를 확인했다(internal/mcpsrv/tools.go: 도구 "land", result 는 enum ok|fail,
//	   leave 는 문자열). judge 가 mcpsrv 를 임포트하면 순환이라 그 대조는 **손으로** 한 것이고,
//	   여기서 잠기는 것은 "문구가 그 이름을 계속 말한다"까지다.
//	② 줄 행 번호 — 세션이 자기 차례가 **어느 행**인지 모르면 land 응답과 대조할 수 없고,
//	   억제 키(lane-turn:<행>)가 가리키는 것이 무엇인지도 문구만 보고는 알 수 없다.
func TestLaneTurnTextNamesTheCallAndTheRow(t *testing.T) {
	const row = 4242 // 문구의 다른 어떤 숫자와도 안 겹치는 값 — 부분 일치가 우연히 통과하지 않는다
	ps := Prescribe(PrescribeInput{Now: pt0, SessionID: "me", LaneTurnRow: row, LastJudgment: pt0})
	if len(ps) != 1 {
		t.Fatalf("lane-turn 하나만 나와야 한다: %v", keys(ps))
	}
	for _, want := range []string{"land()", "land(result='ok')", "land(leave=", fmt.Sprintf("%d", row)} {
		if !strings.Contains(ps[0].Text, want) {
			t.Errorf("차례 처방 문구가 %q 를 안 말한다 — 세션이 읽는 것은 이 문자열 하나다:\n%s", want, ps[0].Text)
		}
	}
}

// hasKey 는 처방 키 하나가 나왔는지만 본다. 겹침 축은 상대마다 키가 갈리므로
// keys() 전체를 단정하면 상대 id 까지 시험이 외워야 한다.
func hasKey(ps []Prescription, key string) bool {
	for _, p := range ps {
		if p.Key == key {
			return true
		}
	}
	return false
}

// sameConversation 의 `strings.TrimSpace` 한 줄이 **정반대 두 결함**을 동시에 쥔다.
// 그 한 줄을 지워도 전 패키지가 초록이었다(실측: main a0978cb 에서 변이 주입 후
// `go test ./internal/... ./cmd/fd/` 전건 통과). 그래서 두 갈래를 함께 잠근다:
//
//	① 공백만 있는 cc 끼리 — `a != "" && a == b` 가 **참**이 되어 cc 를 못 읽은 두 카드가
//	   한 대화로 접힌다. 서로 다른 두 대화의 겹침이 통째로 사라진다. 그 함수 주석이
//	   명시적으로 금지한 실패 모양("못 읽었다"를 "같다"로 접기)이 빈 문자열 대신
//	   **공백**으로 들어왔을 때만 되살아난다 — 주석은 빈 문자열만 막고 있었다.
//	② 앞뒤 공백만 다른 같은 cc — 남남이 되어 형제 카드에 겹침이 뜬다. 세션이
//	   **자기 자신과 조율하라는** 발화를 받는다(01KZ8XVK 의 실측 32건 중 5건).
//
// 두 갈래는 서로 반대 방향이라 한쪽만 잠그면 다른 쪽이 그대로 열린다.
func TestWhitespaceOnlyCCIsNeverOneConversationAndPaddedCCAlwaysIs(t *testing.T) {
	const otherID = "01SESSIONOTHER"
	// Claims 가 턴 경로를 덮으므로 unclaimed·outside 축은 안 돈다 — 남는 것은 겹침뿐이다.
	in := func(selfCC, otherCC string) PrescribeInput {
		return PrescribeInput{
			Now: pt0, SessionID: "me", SelfCC: selfCC,
			Claims:       []ClaimView{{ItemID: "fd-x", Paths: []string{"cmd/fd"}}},
			TurnPaths:    []string{"cmd/fd/hook.go"},
			Others:       []LiveSession{{ID: otherID, CCSessionID: otherCC, Paths: []string{"cmd/fd/hook.go"}}},
			LastJudgment: pt0, NewPaths: 1,
		}
	}
	cases := []struct {
		name            string
		selfCC, otherCC string
		wantOverlap     bool
		why             string
	}{
		{
			name: "공백만 있는 cc 끼리는 같은 대화가 아니다", selfCC: "   ", otherCC: "   ", wantOverlap: true,
			why: "관측이 깨진 두 카드를 한 대화로 접으면 겹침 축이 조용히 꺼진다",
		},
		{
			name: "빈 cc 끼리도 같은 대화가 아니다", selfCC: "", otherCC: "", wantOverlap: true,
			why: "이미 잠긴 계약이다 — 공백 갈래를 고치다 이쪽을 깨뜨리면 여기서 걸린다",
		},
		{
			name: "앞뒤 공백만 다르면 같은 대화다", selfCC: " cc-1", otherCC: "cc-1\t", wantOverlap: false,
			why: "형제 카드에 겹침이 뜨면 세션이 자기 자신과 조율하라는 발화를 받는다",
		},
		{
			name: "다른 cc 는 다듬어도 남남이다", selfCC: "cc-1", otherCC: "cc-2", wantOverlap: true,
			why: "부정 대조 — 다듬기가 서로 다른 대화까지 접으면 여기서 걸린다",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := hasKey(Prescribe(in(c.selfCC, c.otherCC)), PrescribeOverlap+":"+otherID)
			if got != c.wantOverlap {
				t.Fatalf("self=%q other=%q 에서 겹침 처방 %v 를 기대했는데 %v — %s",
					c.selfCC, c.otherCC, c.wantOverlap, got, c.why)
			}
		})
	}
}

// 겹침 처방의 **좌우**와 **쌍의 수**를 잠근다. 셋 다 변이가 살아남던 자리다:
// `mine, theirs` 교환 · `pairs[0]` → `pairs[len-1]` · `len(pairs)` → `len(pairs)+1`.
//
// 기존 시험이 못 잡은 이유는 양쪽에 **같은 경로**를 주고 `strings.Contains` 로만 봤기 때문이다 —
// 두 문자열이 같으면 뒤바뀜이 원리적으로 안 보인다. 그래서 여기서는 겹침을 **조상 관계**로 만든다
// (내가 파일, 상대가 그 디렉토리). 좌우가 바뀌면 세션은 **자기가 만지지도 않은 경로**를 근거로
// ask 를 남기게 되고, 그것이 이 축이 나르는 유일한 행동이다.
//
// 잠그는 것은 행동 가능한 정보 셋뿐이다: 내가 만진 것이 어느 쪽인가 · 상대가 잡은 것이 어느 쪽인가 ·
// 겹친 쌍이 몇인가(`Prescription` 주석이 "사유는 시험이 단정하는 축"이라 못박은 자리인데 수만 빠져 있었다).
func TestOverlapReasonPinsMineTheirsAndPairCount(t *testing.T) {
	const (
		mine     = "internal/judge/prescribe.go" // 첫 쌍이 된다 — TurnPaths 순회가 바깥이다
		alsoMine = "internal/judge/paths.go"     // 둘째 쌍. pairs[len-1] 변이가 이걸 mine 으로 만든다
		theirs   = "internal/judge"              // 조상이라 둘 다와 겹친다 → 쌍이 정확히 둘
		otherID  = "01SESSIONOTHER"
	)
	ps := Prescribe(PrescribeInput{
		Now: pt0, SessionID: "me",
		Claims:       []ClaimView{{ItemID: "fd-x", Paths: []string{theirs}}},
		TurnPaths:    []string{mine, alsoMine},
		Others:       []LiveSession{{ID: otherID, Paths: []string{theirs}}},
		LastJudgment: pt0, NewPaths: 2,
	})
	if len(ps) != 1 || ps[0].Key != PrescribeOverlap+":"+otherID {
		t.Fatalf("겹침 처방 하나만 나와야 한다: %v", keys(ps))
	}
	p := ps[0]

	for _, w := range []struct{ where, in, why string }{
		{"사유", "이번에 만진 " + mine + " 가", "내가 만진 것이 첫 쌍의 왼쪽이다"},
		{"사유", "의 발자국 " + theirs + " 와", "상대가 잡은 것이 첫 쌍의 오른쪽이다"},
		{"사유", "(겹친 쌍 2)", "쌍의 수는 세션이 조율 범위를 가늠하는 값이다"},
		{"문구", mine + " 를 만졌는데", "세션이 실제로 읽는 문자열에서도 좌우가 같아야 한다"},
		{"문구", "도 " + theirs + " 를 잡고 있다", "상대가 잡은 것이 상대 자리에 있어야 한다"},
	} {
		got := p.Reason
		if w.where == "문구" {
			got = p.Text
		}
		if !strings.Contains(got, w.in) {
			t.Errorf("%s가 %q 를 안 말한다 — %s:\n%s", w.where, w.in, w.why, got)
		}
	}
	// 부정 대조 — 좌우가 바뀌거나 마지막 쌍을 집으면 이 조각들이 나타난다.
	for _, never := range []string{
		"이번에 만진 " + theirs + " 가",   // 좌우 교환
		"이번에 만진 " + alsoMine + " 가", // pairs[len-1]
		"의 발자국 " + mine + " 와",      // 좌우 교환
	} {
		if strings.Contains(p.Reason, never) {
			t.Errorf("사유가 %q 라고 말한다 — 세션이 자기가 만지지도 않은 경로를 근거로 ask 를 남기게 된다:\n%s", never, p.Reason)
		}
	}
}
