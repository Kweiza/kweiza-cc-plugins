package mcpsrv

import (
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// RenderBoard 는 **선점을 든 카드만** 낸다. 자기 카드도 예외가 없다.
//
// ★ 이 화면이 답하는 질문이 바뀌었다: "누가 살아 있나"가 아니라 "어느 작업이 잡혀 있나"다.
func TestRenderBoardShowsOnlyClaimedCards(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	v := service.BoardView{
		At: now, Window: 2 * time.Hour,
		Sessions: []service.SessionCard{
			{View: model.SessionView{
				Session: model.Session{ID: "01HELD"},
				Claims:  []string{"it-held"},
				Signals: map[model.SignalKind]time.Time{model.SignalPrompt: now.Add(-3 * time.Minute)},
			}},
			// 선점 없이 **실제로 일하는** 세션. 화면에서 사라지는 쪽이다.
			{View: model.SessionView{
				Session: model.Session{ID: "01FREE"},
				Signals: map[model.SignalKind]time.Time{model.SignalTool: now},
			}},
			// 선점 없는 **내** 카드 — 예외를 안 둔다.
			{View: model.SessionView{Session: model.Session{ID: "01SELF"}}, IsSelf: true},
		},
	}
	got := RenderBoard(v, BoardRenderOptions{Now: now, Self: "01SELF", Detail: true})

	mustHave(t, got, "잡혀 있는 작업 1건", "머리줄이 선점 기준으로 안 센다")
	mustHave(t, got, "선점 기준이다 — 세션의 생사가 아니다", "이 화면이 생존을 말하는 것처럼 읽힌다")
	mustHave(t, got, "it-held", "무엇을 쥐고 있는지가 카드 머리줄에 없다")
	mustHave(t, got, "01HELD", "선점을 든 카드가 없다")
	mustMiss(t, got, "01FREE", "선점 없는 세션이 나왔다")
	mustMiss(t, got, "01SELF", "선점 없는 자기 카드가 나왔다 — 규칙에 예외를 두지 않기로 했다")

	// 접은 것을 침묵하지 않는다. 그리고 조율에서 빠진 게 아니라는 사실을 함께 말한다.
	mustHave(t, got, "선점 없는 세션 2건은 안 낸다", "접은 수를 침묵하면 '없다'와 '안 보여 준다'가 같아진다")
	mustHave(t, got, "겹침 처방은 그 세션들도 그대로 본다", "조율에서 빠졌다고 잘못 읽힌다")
}

// 창 밖인데 항목을 쥔 세션이 **나온다.** 창을 이 화면에 안 거는 것의 유일한 가드다.
func TestRenderBoardKeepsClaimHoldersOutsideTheWindow(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	v := service.BoardView{
		At: now, Window: 2 * time.Hour,
		OutsideClaims: []model.SessionView{{
			Session: model.Session{ID: "01STUCK"},
			Claims:  []string{"it-stuck"},
			// mcp 뿐이다 — 조회 도구(board)를 부르기만 해도 찍히는 신호라 **활동이 아니다.**
			// 이것이 "쥐고만 있고 아무것도 안 한 세션"의 실제 모양이다.
			Signals: map[model.SignalKind]time.Time{model.SignalMCP: now.Add(-12 * time.Hour)},
		}},
	}
	got := RenderBoard(v, BoardRenderOptions{Now: now, Detail: true})

	mustHave(t, got, "잡혀 있는 작업 1건", "창 밖 선점자가 건수에 안 들어갔다")
	mustHave(t, got, "it-stuck", "창 밖 선점자가 사라졌다 — 회수가 가장 필요한 카드가 창 때문에 안 보인다")
	mustHave(t, got, "파생 안 읽음", "파생을 안 읽은 줄을 그렇다고 안 말하면 0값과 미관측이 뭉개진다")
	mustHave(t, got, "○ 활동 없음", "mcp 뿐인 카드를 활동 있음으로 냈다 — 배지의 판별력이 0이 된다")
	// ★ 회수 손잡이. 이 줄이 없으면 "창 밖 선점"은 진단만 있고 처방이 없는 표시다 —
	//   실측(2026-08-07): 무신호 9~24.5h 세션 4곳이 12건을 쥔 채, 아무도 회수 표면을 몰랐다.
	mustHave(t, got, "fd claim release", "잠긴 선점을 어떻게 푸는지가 화면에 없다 — 표시만으로는 재고가 안 돈다")
	mustHave(t, got, "자동으로 안 풀린다", "자동 만료가 없다는 사실을 안 말하면 사람이 기다리기만 한다")
	// 생존 낱말 가드는 이 픽스처(OutsideClaims 있음)에서도 유지된다 — 기존 가드 시험은
	// 창 밖 선점이 없는 픽스처라 회수 힌트 갈래를 안 지난다.
	mustMiss(t, got, "죽", "보드가 생존 낱말을 냈다 — 생사 판정은 화면이 아니라 사람의 몫이다(설계 §4)")
}

// 창 밖 선점이 **없으면** 회수 안내도 없다 — 상시 점등된 안내는 판별력이 0이 된다(설계 §4).
func TestRenderBoardHidesReclaimHintWithoutOutsideClaims(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	v := service.BoardView{At: now, Window: 2 * time.Hour}
	got := RenderBoard(v, BoardRenderOptions{Now: now, Detail: true})
	mustMiss(t, got, "fd claim release", "회수할 것이 없는데 회수 안내가 상시 점등이다")
}

// 배지는 **유무**를 말하고 나이가 낡음을 말한다. 오래된 활동을 "없음"으로 접으면
// 그 순간 이 화면이 생존 판정을 하게 된다 — 이 저장소가 두 번 틀린 그 판정이다.
func TestRenderBoardBadgeSaysPresenceNotFreshness(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	v := service.BoardView{
		At: now, Window: 2 * time.Hour,
		Sessions: []service.SessionCard{{View: model.SessionView{
			Session: model.Session{ID: "01OLD"},
			Claims:  []string{"it-old"},
			Signals: map[model.SignalKind]time.Time{model.SignalPrompt: now.Add(-12 * time.Hour)},
		}}},
	}
	got := RenderBoard(v, BoardRenderOptions{Now: now, Detail: true})
	mustHave(t, got, "● 활동 12시간", "12시간 전 활동을 '없음'으로 접었다 — 그것은 생존 판정이다")
	mustMiss(t, got, "○ 활동 없음", "활동 신호가 있는데 없다고 했다")
}

// 0건의 뜻이 바뀌었다 — **정상 상태**다.
func TestRenderBoardSaysZeroIsNormal(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	got := RenderBoard(service.BoardView{At: now, Window: 2 * time.Hour},
		BoardRenderOptions{Now: now})
	mustHave(t, got, "잡혀 있는 작업이 없다", "0건 문장이 없다")
	mustHave(t, got, "서버 장애가 아니다", "0건이 정상 상태라는 것을 화면이 말해야 한다")
}

func mustHave(t *testing.T, got, want, why string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("출력에 %q 가 없다 — %s\n%s", want, why, got)
	}
}

func mustMiss(t *testing.T, got, bad, why string) {
	t.Helper()
	if strings.Contains(got, bad) {
		t.Fatalf("출력에 %q 가 있다 — %s\n%s", bad, why, got)
	}
}
