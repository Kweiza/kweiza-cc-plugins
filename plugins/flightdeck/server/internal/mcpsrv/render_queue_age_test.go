package mcpsrv

import (
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// 큐 줄이 **나이를 낸다**.
//
// ★ 이 축이 없으면 화석이 안 보인다. 보드는 이미 최고령 3건을 매번 앞에 냈다
// (store/item.go 의 ORDER BY created_at 이 그 순서를 준다). 그런데 나이를 안 찍어서
// "26시간째 아무도 안 집었다"는 사실이 화면 어디에도 없었다 — 세션은 그 셋을
// 그냥 "큐에 있는 항목 셋"으로 읽고 지나갔다.
//
// 실측(kweiza-cc-plugins · 판단 01KZAW342JAC6EAW8C31RCXXK0): 열린 26건 중
// 7.5h 이상 묵은 17건이 전부 단독이었고, 그 사실을 보드가 한 번도 말한 적이 없다.
func TestBoardQueueLinesCarryAge(t *testing.T) {
	v := service.BoardView{
		Project: model.Project{ID: "sample-platform", DefaultBranch: "main"},
		At:      t0, Window: 8 * time.Hour,
		OpenItems: []model.Item{
			{ID: "fossil-one", Title: "굶은 것", CreatedAt: t0.Add(-30 * time.Hour)},
			{ID: "fossil-two", Title: "굶은 것 둘", CreatedAt: t0.Add(-25 * time.Hour)},
			{ID: "fresh-one", Title: "최근 것", CreatedAt: t0.Add(-1 * time.Hour)},
		},
	}

	brief := RenderBoard(v, BoardRenderOptions{Now: t0})
	if !strings.Contains(brief, "큐 열림 3건") {
		t.Fatalf("brief 에 큐 열림 수가 없다:\n%s", brief)
	}
	// 최고령이 화면에 있어야 한다. 30h 를 FormatAge 가 어떻게 쓰든, 그 문자열이 나와야 한다.
	oldest := FormatAge(30 * time.Hour)
	if !strings.Contains(brief, oldest) {
		t.Fatalf("brief 큐 줄에 최고령(%s)이 없다 — 화석이 안 보인다:\n%s", oldest, brief)
	}
	// 임계를 넘긴 건수도 나와야 한다. 하나만 보이면 "그 하나만 오래됐다"로 읽힌다.
	if !strings.Contains(brief, "2건") {
		t.Fatalf("brief 에 굶은 항목 건수(2건)가 없다:\n%s", brief)
	}

	detail := RenderBoard(v, BoardRenderOptions{Now: t0, Detail: true})
	for _, want := range []string{"fossil-one", "fossil-two", "fresh-one"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail 에 항목 %s 가 없다:\n%s", want, detail)
		}
	}
	if !strings.Contains(detail, FormatAge(25*time.Hour)) {
		t.Fatalf("detail 줄에 항목별 나이가 없다:\n%s", detail)
	}
}

// 나이 판정은 v.At 을 쓴다 — **보드를 만든 시각**이다.
//
// ★ 여기서 time.Now() 를 부르면 시험이 가짜 시계를 밀어도 이 축만 실시계로 찍힌다.
// fd-lane-timestamps-ignore-injected-clock 이 고발한 그 모양이고, 그 항목은
// "운영 영향은 없다 — 문제는 시험이다" 로 끝난다. 같은 실패를 새로 만들지 않는다.
func TestBoardQueueAgeUsesBoardClockNotWallClock(t *testing.T) {
	// t0 는 과거 고정 시각이다. 실시계를 쓰면 나이가 수년으로 찍힌다.
	v := service.BoardView{
		Project:   model.Project{ID: "sample-platform", DefaultBranch: "main"},
		At:        t0,
		OpenItems: []model.Item{{ID: "x", Title: "t", CreatedAt: t0.Add(-2 * time.Hour)}},
	}
	got := RenderBoard(v, BoardRenderOptions{Now: t0, Detail: true})
	if !strings.Contains(got, FormatAge(2*time.Hour)) {
		t.Fatalf("주입된 보드 시각(v.At)으로 나이를 안 쟀다:\n%s", got)
	}
}

// 티클러는 굶김 축에서 빠진다 — 기한까지 늙는 것이 정상인 항목이 굶김 절을 상시
// 점등시키면 판별력이 0이 된다(§4). 대신 나이 옆에 이름을 얻는다: 표식 없는 긴
// 나이가 "잊힌 항목"으로 읽히면 빠진 것이 침묵이 된다.
func TestBoardQueueExcludesTicklerFromStarvation(t *testing.T) {
	v := service.BoardView{
		Project: model.Project{ID: "sample-platform", DefaultBranch: "main"},
		At:      t0, Window: 8 * time.Hour,
		OpenItems: []model.Item{
			{ID: "tick-due", Title: "기한 대기", CreatedAt: t0.Add(-40 * time.Hour),
				Labels: []string{"tickler"}},
			{ID: "fresh-one", Title: "최근 것", CreatedAt: t0.Add(-1 * time.Hour)},
		},
	}
	brief := RenderBoard(v, BoardRenderOptions{Now: t0})
	if strings.Contains(brief, FormatAge(judge.StarvationAge)+"+") {
		t.Fatalf("티클러 하나뿐인데 굶김 절이 켜졌다 — 상시 점등이 된다:\n%s", brief)
	}
	if strings.Contains(brief, FormatAge(40*time.Hour)) {
		t.Fatalf("티클러의 나이가 최고령으로 나왔다 — 기한 대기가 방치로 읽힌다:\n%s", brief)
	}

	detail := RenderBoard(v, BoardRenderOptions{Now: t0, Detail: true})
	if strings.Contains(detail, "★"+FormatAge(40*time.Hour)) {
		t.Fatalf("티클러 항목에 ★ 가 붙었다:\n%s", detail)
	}
	if !strings.Contains(detail, "티클러") {
		t.Fatalf("티클러가 이름을 안 얻었다 — 빠진 것이 침묵이 된다:\n%s", detail)
	}

	// 대조: 비티클러가 임계를 넘기면 굶김 절과 ★ 는 그대로 산다.
	v.OpenItems[0].Labels = nil
	brief = RenderBoard(v, BoardRenderOptions{Now: t0})
	if !strings.Contains(brief, FormatAge(judge.StarvationAge)+"+ 1건") {
		t.Fatalf("대조가 깨졌다 — 비티클러 굶김 절이 사라졌다:\n%s", brief)
	}
}

// 굶은 것이 0건이면 그 절을 **안 낸다**.
//
// 상시 점등된 경고는 판별력이 0이 된다 — 이 저장소가 겹침 알림에서 이미 내린
// 판단이다(judge/eligible.go 의 self·형제 건너뛰기 주석).
func TestBoardQueueOmitsStarvedClauseWhenNone(t *testing.T) {
	v := service.BoardView{
		Project:   model.Project{ID: "sample-platform", DefaultBranch: "main"},
		At:        t0,
		OpenItems: []model.Item{{ID: "x", Title: "t", CreatedAt: t0.Add(-1 * time.Hour)}},
	}
	got := RenderBoard(v, BoardRenderOptions{Now: t0})
	if strings.Contains(got, FormatAge(judge.StarvationAge)+"+") {
		t.Fatalf("굶은 것이 0건인데 기아 절이 나왔다:\n%s", got)
	}
}
