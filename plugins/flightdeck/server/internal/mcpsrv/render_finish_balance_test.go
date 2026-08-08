package mcpsrv

import (
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
	"github.com/kweiza/flightdeck/internal/store"
)

func finishWithBalance(b *service.QueueBalance) service.FinishResult {
	return service.FinishResult{
		Item:         model.Item{ID: "i1", State: model.ItemDone},
		Judgment:     model.Judgment{ID: "J1", Kind: model.JudgmentHandoff, Body: "본문"},
		QueueBalance: b,
	}
}

// 마무리가 **자기가 큐에 한 일**을 그 자리에서 낸다.
//
// ★ 왜 필요한가. 실측 R=1.30 — 사이클 1회마다 큐가 +0.29 다. 그런데 지금 세션은 자기가
// 큐에 무엇을 했는지 볼 방법이 없다. 보드는 총량만 내고 그것도 다음 세션이 본다.
// 측정을 그 자리에 놓으면 판단은 사람이 한다(추천 강제를 기각하고 실측을 남긴 것과 같은 형태).
func TestRenderFinishShowsQueueBalance(t *testing.T) {
	got := RenderFinish(finishWithBalance(&service.QueueBalance{
		Closed: 1, Added: 2, Open: 27, Starved: 6,
		Oldest:      30 * time.Hour,
		Repro:       &store.Reproduction{Finishes: 20, Followups: 14, Adds: 12},
		ReproWindow: 20,
	}))

	// 이번 호출의 델타 — 닫은 1, 만든 2, 순증 +1.
	if !strings.Contains(got, "+1") {
		t.Fatalf("순증(+1)이 없다:\n%s", got)
	}
	if !strings.Contains(got, "27건") {
		t.Fatalf("열린 항목 수가 없다:\n%s", got)
	}
	// 굶은 건수와 최고령 — 큐 길이만으로는 화석이 안 보인다.
	if !strings.Contains(got, "6건") {
		t.Fatalf("굶은 항목 수가 없다:\n%s", got)
	}
	if !strings.Contains(got, FormatAge(30*time.Hour)) {
		t.Fatalf("최고령이 없다:\n%s", got)
	}
	// R = (14+12)/20 = 1.30
	if !strings.Contains(got, "1.30") {
		t.Fatalf("재생산율이 없다:\n%s", got)
	}
	// 표본 크기를 반드시 적는다 — 없으면 전 기간 누적과 구분되지 않는다.
	if !strings.Contains(got, "20회") {
		t.Fatalf("표본 크기(최근 20회)가 없다 — 전 기간 누적과 구분되지 않는다:\n%s", got)
	}
}

// R < 1 이면 **큐가 준다**고 말한다. 1.30 과 0.79 가 같은 문장을 받으면 계기가 아니다.
func TestRenderFinishDistinguishesShrinkingQueue(t *testing.T) {
	shrink := RenderFinish(finishWithBalance(&service.QueueBalance{
		Closed: 1, Added: 0, Open: 5,
		Repro:       &store.Reproduction{Finishes: 20, Followups: 8, Adds: 4},
		ReproWindow: 20,
	}))
	grow := RenderFinish(finishWithBalance(&service.QueueBalance{
		Closed: 1, Added: 3, Open: 30,
		Repro:       &store.Reproduction{Finishes: 20, Followups: 14, Adds: 12},
		ReproWindow: 20,
	}))
	if shrink == grow {
		t.Fatalf("R=0.60 과 R=1.30 이 같은 문장을 냈다 — 계기가 아무것도 안 가른다")
	}
	// 0.60 은 줄어드는 쪽이다.
	if !strings.Contains(shrink, "0.60") {
		t.Fatalf("줄어드는 R 이 안 찍혔다:\n%s", shrink)
	}
}

// nil 은 **0이 아니다** — 못 읽었다고 말한다.
//
// ★ 0으로 접으면 조회가 실패한 응답이 "큐가 안 늘었다"를 단정한다. StillHeld·QueueOpen·
// PathCheck 이 포인터인 것과 같은 계약이다.
func TestRenderFinishSaysWhenBalanceUnread(t *testing.T) {
	got := RenderFinish(finishWithBalance(nil))
	if strings.Contains(got, "큐 수지: ") {
		t.Fatalf("못 읽은 축을 값처럼 냈다:\n%s", got)
	}
	// ★ "못 읽었다"만 찾으면 StillHeld 의 같은 문구에 걸려 **거짓 초록**이 된다.
	// 이 축을 지목하는 문장을 단정한다.
	if !strings.Contains(got, "큐 수지는 이 응답이 못 읽었다") {
		t.Fatalf("nil 인데 그 사실을 안 말한다 — 침묵하면 '큐가 안 늘었다'로 읽힌다:\n%s", got)
	}
}

// 표본이 0이면 그 사실을 적는다 — 생략하거나 0으로 찍지 않는다.
//
// ★ 그리고 **집계 실패와 다른 문장이어야 한다.** 앞 판은 둘을 한 문장으로 내어,
// 집계가 실패했을 뿐인데 응답이 "최근 마무리 표본 0"이라고 원인을 단정했다.
func TestRenderFinishSaysWhenSampleIsZero(t *testing.T) {
	got := RenderFinish(finishWithBalance(&service.QueueBalance{
		Closed: 1, Added: 1, Open: 3,
		Repro:       &store.Reproduction{}, // 읽었고, Finishes 0 — 참일 수 있는 사실이다
		ReproWindow: 20,
	}))
	if strings.Contains(got, "R=0.00") {
		t.Fatalf("표본 0을 0으로 찍었다 — '큐가 안 는다'로 읽힌다:\n%s", got)
	}
	if !strings.Contains(got, "표본이 0회") {
		t.Fatalf("표본 0인데 그 사실을 안 말한다:\n%s", got)
	}
	if strings.Contains(got, "원자료가 없다") {
		t.Fatalf("표본이 0일 뿐인데 원자료가 없다고 말한다:\n%s", got)
	}
	// ★ **원인을 단정하면 안 된다.** 원자료가 실렸다는 것이 "집계가 성공했다"를 뜻하지
	// 않는다 — 이 축을 값 타입으로 내던 옛 서버 판은 집계가 실패해도 제로값을 실어 보낸다.
	if strings.Contains(got, "집계는 됐다") {
		t.Fatalf("관측하지 않은 사실(집계 성공)을 단정했다:\n%s", got)
	}
	// 큐 상태 자체는 읽었으므로 그 절은 살아 있어야 한다.
	if !strings.Contains(got, "3건") {
		t.Fatalf("R 만 못 쟀는데 큐 상태까지 통째로 뺐다:\n%s", got)
	}
}

// 집계가 **실패**하면 그렇게 말한다 — "표본 0"이라고 원인을 단정하지 않는다.
//
// ★ 이것이 이 항목의 핵심이다. 같은 파일이 세 자리에서 "0과 못 잼을 가른다"를 지키는데
// 이 축에서만 뭉개졌고, 그 값은 DESIGN §10 이 "큐가 줄고 있나"를 판정하는 유일한 축이다.
func TestRenderFinishSeparatesUnmeasuredRateFromZeroSample(t *testing.T) {
	got := RenderFinish(finishWithBalance(&service.QueueBalance{
		Closed: 1, Added: 1, Open: 3,
		Repro:       nil, // 집계가 실패했다 — 표본이 몇인지도 모른다
		ReproWindow: 20,
	}))
	if strings.Contains(got, "R=0.00") {
		t.Fatalf("못 잰 것을 0으로 찍었다:\n%s", got)
	}
	if !strings.Contains(got, "원자료가 없다") {
		t.Fatalf("원자료가 없는데 그 사실을 안 말한다:\n%s", got)
	}
	// ★ 원인을 단정하면 안 된다 — 마무리가 20회 쌓여 있어도 이 갈래가 돈다.
	if strings.Contains(got, "표본이 0회") {
		t.Fatalf("원자료가 없을 뿐인데 '표본이 0회'라고 원인을 단정했다:\n%s", got)
	}
	// ★ 부재의 원인도 단정하면 안 된다 — 집계 실패 · 프록시 탈락 · 구서버 판이 다 가능하다.
	if strings.Contains(got, "집계가 실패했다") {
		t.Fatalf("원자료 부재의 원인을 단정했다:\n%s", got)
	}
	if !strings.Contains(got, "3건") {
		t.Fatalf("R 만 못 쟀는데 큐 상태까지 통째로 뺐다:\n%s", got)
	}
}

// 추세 문장이 **경계에서** 뒤집힌다 — R<1 만 "줄고 있다"다.
//
// ★ 이 축이 안 잠겨 있었다(검토가 변이로 확인). 부등호를 뒤집으면 R=1.30 이 "큐가 줄고
// 있다"를 내는데, 그 문장은 DESIGN §10 의 R 반증 기한이 판정하는 바로 그 값이다 —
// 계기가 반대로 말하면 기한이 와도 아무도 그것을 모른다.
func TestRenderFinishTrendFlipsAtOne(t *testing.T) {
	// R = (Followups+Adds)/Finishes 다. 아래 셋은 각각 0.90 · 1.00 · 1.30 을 만든다.
	for _, c := range []struct {
		name          string
		repro         store.Reproduction
		wantShrinking bool
	}{
		{"R=0.90 — 준다", store.Reproduction{Finishes: 10, Followups: 5, Adds: 4}, true},
		{"R=1.00 — 안 준다(경계는 미포함)", store.Reproduction{Finishes: 10, Followups: 6, Adds: 4}, false},
		{"R=1.30 — 는다", store.Reproduction{Finishes: 10, Followups: 8, Adds: 5}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := c.repro
			got := RenderFinish(finishWithBalance(&service.QueueBalance{
				Closed: 1, Added: 1, Open: 3, Repro: &r, ReproWindow: 20,
			}))
			shrinking := strings.Contains(got, "큐가 줄고 있다")
			if shrinking != c.wantShrinking {
				t.Fatalf("추세 판정이 뒤집혔다(줄고 있다=%v, 원하는 것 %v):\n%s",
					shrinking, c.wantShrinking, got)
			}
			// 값 자체도 나와야 한다 — 판정만 있고 수가 없으면 재현이 안 된다.
			if !strings.Contains(got, "R=") {
				t.Fatalf("R 값이 화면에 없다:\n%s", got)
			}
		})
	}
}

// 굶은 것이 0건이면 그 절을 안 낸다 — 상시 점등된 경고는 판별력이 0이 된다.
func TestRenderFinishOmitsStarvedClauseWhenNone(t *testing.T) {
	got := RenderFinish(finishWithBalance(&service.QueueBalance{
		Closed: 1, Added: 0, Open: 4, Starved: 0,
		Oldest:      2 * time.Hour,
		Repro:       &store.Reproduction{Finishes: 5, Followups: 1, Adds: 1},
		ReproWindow: 20,
	}))
	if strings.Contains(got, "굶") {
		t.Fatalf("굶은 것이 0건인데 기아 절이 나왔다:\n%s", got)
	}
}
