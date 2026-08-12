package mcpsrv

import (
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 큐 줄은 티클러의 **기한**을 낸다 — 나이만으로는 뜻이 없다.
//
// 2026-08-12 에 한 티클러를 두고 세 시간 반에 네 세션이 같은 원장 재측을 돌렸다.
// 앞 세션들이 "아직 아니다"를 판단으로 남겼는데도 그랬다 — 보드가 `3시간 38분·티클러`
// 만 내서 **언제 열리는지가 화면에 없었기** 때문이다. 기한을 옆에 두면 눈으로 거른다.
func TestQueueItemAgeShowsTicklerFiringDate(t *testing.T) {
	now := time.Date(2026, time.August, 12, 15, 0, 0, 0, time.UTC)
	item := model.Item{
		CreatedAt: now.Add(-3 * time.Hour),
		Labels:    []string{"tickler", "fires:2026-08-19"},
	}

	got := queueItemAge(now, item)

	if !strings.Contains(got, "티클러") {
		t.Fatalf("queueItemAge = %q — 티클러라는 사실이 사라졌다", got)
	}
	if !strings.Contains(got, "08-19") {
		t.Fatalf("queueItemAge = %q — 기한이 화면에 없다. 나이만 있는 티클러가 이 결함이다", got)
	}
}

// 기한이 지났으면 그 사실도 낸다 — 지난 기한이 안 지난 것과 같아 보이면 안 연다.
func TestQueueItemAgeSaysWhenTheTicklerIsDue(t *testing.T) {
	now := time.Date(2026, time.August, 20, 9, 0, 0, 0, time.UTC)
	item := model.Item{
		CreatedAt: now.Add(-8 * 24 * time.Hour),
		Labels:    []string{"tickler", "fires:2026-08-19"},
	}

	got := queueItemAge(now, item)

	if !strings.Contains(got, "지났다") {
		t.Fatalf("queueItemAge = %q — 기한이 지났는데 화면이 안 말한다", got)
	}
}

// 기한 없는 티클러는 지금까지와 똑같다 — 없는 값을 지어내지 않는다.
func TestQueueItemAgeLeavesTicklerWithoutDateAlone(t *testing.T) {
	now := time.Date(2026, time.August, 12, 15, 0, 0, 0, time.UTC)
	item := model.Item{CreatedAt: now.Add(-3 * time.Hour), Labels: []string{"tickler"}}

	got := queueItemAge(now, item)

	if !strings.HasSuffix(got, "·티클러") {
		t.Fatalf("queueItemAge = %q — 기한 없는 티클러의 표기가 바뀌었다", got)
	}
}

// 티클러가 아닌 항목은 fires 를 달아도 안 본다 — 이 축은 티클러 표기 안에서만 산다.
func TestQueueItemAgeIgnoresFiresOnNonTickler(t *testing.T) {
	now := time.Date(2026, time.August, 12, 15, 0, 0, 0, time.UTC)
	item := model.Item{CreatedAt: now.Add(-3 * time.Hour), Labels: []string{"fires:2026-08-19"}}

	got := queueItemAge(now, item)

	if strings.Contains(got, "08-19") || strings.Contains(got, "티클러") {
		t.Fatalf("queueItemAge = %q — 티클러가 아닌데 발화일이 붙었다", got)
	}
}
