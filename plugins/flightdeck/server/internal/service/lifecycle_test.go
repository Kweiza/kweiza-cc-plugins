package service

import (
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// judgeLifecycleGate 는 순수 함수라 표 시험 하나로 판정 전체를 잠글 수 있다 — SQLite 픽스처가
// 필요 없다(store.ConversationLifecycle 이 이미 관측을 다 모아 온 뒤의 자리이기 때문이다).
func TestJudgeLifecycleGate(t *testing.T) {
	cases := []struct {
		name      string
		in        store.ConvLifecycle
		wantStage string // "" 면 nil 을 기대한다
	}{
		{
			name: "줄에 섰는데 하나도 안 쥠 → lane-wait",
			in: store.ConvLifecycle{
				LaneRow: &model.LandingRow{ID: 5, Resources: []string{"landing"}},
			},
			wantStage: "lane-wait",
		},
		{
			name: "줄 행의 자원 일부만 쥠 → lane-wait 아님(부분 점유는 어긋남 — block 이 판정할 자리가 아니다)",
			in: store.ConvLifecycle{
				LaneRow: &model.LandingRow{ID: 5, Resources: []string{"landing", "path:x.go"}},
				HeldRes: []string{"landing"},
			},
			wantStage: "",
		},
		{
			name: "전부 쥠 → 통과(쥔 채 끝내는 것은 block 안 한다)",
			in: store.ConvLifecycle{
				LaneRow: &model.LandingRow{ID: 5, Resources: []string{"landing"}},
				HeldRes: []string{"landing"},
			},
			wantStage: "",
		},
		{
			name: "선점 있고 항목 열림 → finish",
			in: store.ConvLifecycle{
				LiveClaims: []string{"it-1"},
			},
			wantStage: "finish",
		},
		{
			name: "done 닫았고 줄 선 적 없음 → land",
			in: store.ConvLifecycle{
				DoneItems:    []string{"it-2"},
				EverEnqueued: false,
			},
			wantStage: "land",
		},
		{
			name: "done 닫았고 줄 선 적 있음 → 통과(land 아님)",
			in: store.ConvLifecycle{
				DoneItems:    []string{"it-2"},
				EverEnqueued: true,
			},
			wantStage: "",
		},
		// "dropped 만 닫음 → 통과"는 이 표에서 안 잠근다 — DoneItems 는 store 층에서 이미
		// done 만 담게 걸러지므로(dropped 는 안 들어온다), 이 순수 함수 입장에서는
		// `DoneItems: nil` 인 "아무것도 없음" 행과 입력이 완전히 같아 아무것도 못 가른다
		// (그렇게 적어 뒀다가 리뷰에서 지적됨 — mutation 으로 store 의 dropped 배제 조건을
		// 지워도 이 표는 초록이었다). dropped 배제는
		// store.TestConversationLifecycleExcludesDroppedAndRolledBackFinishes 가 잠근다.
		{
			name:      "아무것도 없음 → nil",
			in:        store.ConvLifecycle{},
			wantStage: "",
		},
		{
			// 리뷰어 재현(Critical): 형제 카드가 이 줄 행과 **서로소인** 자원을 쥐면
			// HeldRes 는 비지 않지만 그 행 자체는 하나도 안 쥔 것이다 — lane-wait 가 떠야
			// 한다. 앞 판은 `len(HeldRes)==0` 전칭이라 이 사례에서 억제가 잘못 걸렸다.
			name: "형제가 줄 행과 서로소인 자원만 쥠 → lane-wait 뜸(그 행 자체는 하나도 안 쥐었다)",
			in: store.ConvLifecycle{
				LaneRow: &model.LandingRow{ID: 7, Resources: []string{"landing"}},
				HeldRes: []string{"path:a.go"},
			},
			wantStage: "lane-wait",
		},
		{
			name: "줄에 섰고 선점도 있고 done 도 있음 → lane-wait 가 이긴다(가장 급한 것이 줄이다)",
			in: store.ConvLifecycle{
				LaneRow:      &model.LandingRow{ID: 9, Resources: []string{"landing"}},
				LiveClaims:   []string{"it-1"},
				DoneItems:    []string{"it-2"},
				EverEnqueued: false,
			},
			wantStage: "lane-wait",
		},
		{
			name: "선점도 있고 done 도 있는데 줄은 안 섬 → finish 가 land 보다 먼저다",
			in: store.ConvLifecycle{
				LiveClaims:   []string{"it-1"},
				DoneItems:    []string{"it-2"},
				EverEnqueued: false,
			},
			wantStage: "finish",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := judgeLifecycleGate(c.in)
			if c.wantStage == "" {
				if got != nil {
					t.Fatalf("nil 을 기대했는데 %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("stage=%q 를 기대했는데 nil 이 나왔다", c.wantStage)
			}
			if got.Stage != c.wantStage {
				t.Fatalf("stage=%q, want %q (reason=%q)", got.Stage, c.wantStage, got.Reason)
			}
			if got.Reason == "" {
				t.Fatalf("stage=%q 인데 이유가 비었다", got.Stage)
			}
		})
	}
}
