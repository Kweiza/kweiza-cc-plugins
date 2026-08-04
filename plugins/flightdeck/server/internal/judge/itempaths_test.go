package judge

import (
	"strings"
	"testing"
)

// 판정 우선순위는 no-paths → ok → unknown → 나머지 셋이다.
// 그 순서가 곧 이 표의 뼈대다 — 순서가 틀리면 "못 읽었다"가 "오등록이다"로 접힌다.
func TestClassifyItemPaths(t *testing.T) {
	tests := []struct {
		name         string
		in           ItemPathInput
		wantKind     Kind
		wantSug      string
		wantCands    []string
		wantInSum    []string // Summary 에 반드시 들어갈 조각
		wantNotInSum []string // Summary 에 있으면 안 되는 조각
		// wantSug 가 빈 문자열인 행은 전부 "이 갈래에서는 어느 프로젝트도 지목하면 안 된다"다.
		// ★ Suggest 가 곧 되돌리는 명령의 방아쇠다(렌더는 Kind 와 Suggest 를 둘 다 보지만,
		//   Suggest 는 REST 로 나가 다른 소비자가 그것만 읽을 수 있다) — 그래서 기본값을
		//   "지목 없음"으로 두고 예외(misregistered)만 wantSug 를 채운다.
	}{
		{
			name:      "경로가 없으면 판정할 재료가 없다",
			in:        ItemPathInput{Project: "p", Paths: nil},
			wantKind:  KindNoPaths,
			wantInSum: []string{"경로 0"},
		},
		{
			name: "하나라도 여기 있으면 이 항목은 여기 앵커돼 있다",
			in: ItemPathInput{
				Project: "p", Paths: []string{"a.go", "b.go"},
				Here: map[string]PathPresence{"a.go": PathPresent, "b.go": PathAbsent},
			},
			wantKind:  KindOK,
			wantInSum: []string{"p"},
		},
		{
			name: "Present 가 하나 있으면 Unknown 이 섞여도 ok 다",
			in: ItemPathInput{
				Project: "p", Paths: []string{"a.go", "b.go"},
				Here: map[string]PathPresence{"a.go": PathPresent, "b.go": PathUnknown},
			},
			wantKind: KindOK,
		},
		{
			name: "Absent 둘에 Unknown 하나면 오등록이라 말하지 않는다",
			in: ItemPathInput{
				Project: "p", Paths: []string{"a.go", "b.go", "c.go"},
				Here: map[string]PathPresence{"a.go": PathAbsent, "b.go": PathAbsent, "c.go": PathUnknown},
				Elsewhere: map[string]map[string]PathPresence{
					"q": {"a.go": PathPresent},
				},
			},
			wantKind:  KindUnknown,
			wantInSum: []string{"못 읽었다"},
		},
		{
			// D2: 원인 셋(루트 없음 · ".." · 권한 거부)이 전부 같은 문장을 내던 결함.
			// 못 읽은 경로 이름이 문장에 있어야 어느 경로가 원인인지 가려낼 수 있다.
			name: "못 읽은 경로 이름이 문장에 있어야 원인을 구분할 수 있다",
			in: ItemPathInput{
				Project: "p", Paths: []string{"a.go", "b.go", "plugins/x/y.go"},
				Here: map[string]PathPresence{
					"a.go": PathAbsent, "b.go": PathAbsent, "plugins/x/y.go": PathUnknown,
				},
			},
			wantKind:  KindUnknown,
			wantInSum: []string{"plugins/x/y.go"},
		},
		{
			name: "여기 전부 없고 한 프로젝트만 지목하면 오등록이다",
			in: ItemPathInput{
				Project: "context-platform", Paths: []string{"x/y.go"},
				Here: map[string]PathPresence{"x/y.go": PathAbsent},
				Elsewhere: map[string]map[string]PathPresence{
					"kweiza-cc-plugins": {"x/y.go": PathPresent},
				},
			},
			wantKind:  KindMisregistered,
			wantSug:   "kweiza-cc-plugins",
			wantInSum: []string{"kweiza-cc-plugins"},
		},
		{
			name: "둘 이상이 지목되면 지목이 아니다",
			in: ItemPathInput{
				Project: "context-platform", Paths: []string{"docs/"},
				Here: map[string]PathPresence{"docs/": PathAbsent},
				Elsewhere: map[string]map[string]PathPresence{
					"a": {"docs/": PathPresent},
					"b": {"docs/": PathPresent},
				},
			},
			wantKind:  KindAmbiguous,
			wantCands: []string{"a", "b"},
		},
		{
			// Elsewhere 는 성공한 목록 조회의 결과다(빈 map{} — 다른 프로젝트를 실제로 봤고
			// 아무 데도 없었다). service 쪽 계약: 실패하면 nil, 성공하면 map{}(§D1).
			name: "어디에도 없으면 경로가 틀렸거나 레포가 미등록이다",
			in: ItemPathInput{
				Project: "kweiza-cc-plugins", Paths: []string{"internal/service/service.go"},
				Here:      map[string]PathPresence{"internal/service/service.go": PathAbsent},
				Elsewhere: map[string]map[string]PathPresence{},
			},
			wantKind:  KindNowhere,
			wantInSum: []string{"미등록"},
		},
		{
			// D1: ListProjects 실패가 nowhere 확정으로 접히던 결함.
			// Elsewhere 가 nil 이면 목록 조회 자체가 실패한 것 — 남의 프로젝트를 하나도
			// 못 본 것이지 "찾아봤는데 없다"가 아니다. nowhere 로 접으면 안 된다.
			name: "Elsewhere 가 nil 이면 목록 조회가 실패한 것이다 — nowhere 로 접지 않는다",
			in: ItemPathInput{
				Project: "kweiza-cc-plugins", Paths: []string{"internal/service/service.go"},
				Here:      map[string]PathPresence{"internal/service/service.go": PathAbsent},
				Elsewhere: nil,
			},
			wantKind:     KindUnknown,
			wantNotInSum: []string{"어느 프로젝트에도 없다"},
		},
		{
			name: "못 읽은 프로젝트가 있으면 그 사실이 문장에 있다",
			in: ItemPathInput{
				Project: "p", Paths: []string{"x/y.go"},
				Here: map[string]PathPresence{"x/y.go": PathAbsent},
				Elsewhere: map[string]map[string]PathPresence{
					"q": {"x/y.go": PathPresent},
				},
				Unreadable: []string{"r"},
			},
			wantKind:  KindMisregistered,
			wantSug:   "q",
			wantInSum: []string{"못 읽었다", "r"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyItemPaths(tt.in)
			if got.Kind != tt.wantKind {
				t.Fatalf("Kind 가 %q 다 — %q 여야 한다 (Summary=%q)", got.Kind, tt.wantKind, got.Summary)
			}
			if tt.wantSug != "" && got.Suggest != tt.wantSug {
				t.Fatalf("Suggest 가 %q 다 — %q 여야 한다", got.Suggest, tt.wantSug)
			}
			// ★ wantSug 가 빈 문자열인 모든 행에서 Suggest 도 비어야 한다. 이 단정 하나로
			//   여섯 갈래 전부가 닫힌다 — 행을 더할 때 "지목 금지" 플래그를 잊는 재발을 없앤다.
			if tt.wantSug == "" && got.Suggest != "" {
				t.Fatalf("이 갈래(%s)는 지목하면 안 되는데 Suggest 가 %q 다: %s",
					got.Kind, got.Suggest, got.Summary)
			}
			if len(tt.wantCands) > 0 {
				if len(got.Candidates) != len(tt.wantCands) {
					t.Fatalf("Candidates 가 %v 다 — %v 여야 한다", got.Candidates, tt.wantCands)
				}
				for i, c := range tt.wantCands {
					if got.Candidates[i] != c {
						t.Fatalf("Candidates[%d] 가 %q 다 — %q 여야 한다(정렬돼야 한다)", i, got.Candidates[i], c)
					}
				}
			}
			// ★ Summary 는 어느 갈래에서도 비면 안 된다. 사유 없는 판정은 이 레포의 규율 위반이다.
			if strings.TrimSpace(got.Summary) == "" {
				t.Fatal("Summary 가 비었다 — 사유 없는 판정은 통과시키지 않는다")
			}
			for _, w := range tt.wantInSum {
				if !strings.Contains(got.Summary, w) {
					t.Fatalf("Summary 에 %q 가 없다: %s", w, got.Summary)
				}
			}
			for _, w := range tt.wantNotInSum {
				if strings.Contains(got.Summary, w) {
					t.Fatalf("Summary 에 %q 가 있으면 안 된다: %s", w, got.Summary)
				}
			}
		})
	}
}

// 0값이 "못 봤다"여야 한다. 이 단정이 깨지면 관측하지 않은 경로가 "없다"로 접힌다.
func TestPathPresenceZeroValueIsUnknown(t *testing.T) {
	var p PathPresence
	if p != PathUnknown {
		t.Fatalf("PathPresence 의 0값이 %v 다 — PathUnknown 이어야 한다", p)
	}
}
