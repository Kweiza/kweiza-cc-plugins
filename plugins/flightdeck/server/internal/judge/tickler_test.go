package judge

import (
	"testing"
	"time"
)

// 정확 일치만 문다 — 근사를 허용하면 표시용 자유 문자열이 판정에 걸린다.
func TestIsTicklerMatchesExactLabelOnly(t *testing.T) {
	cases := []struct {
		labels []string
		want   bool
	}{
		{nil, false},
		{[]string{}, false},
		{[]string{"tickler"}, true},
		{[]string{"release", "tickler"}, true},
		{[]string{"Tickler"}, false},  // 대소문자 근사 금지
		{[]string{"ticklers"}, false}, // 접두 근사 금지
		{[]string{"tick"}, false},
	}
	for _, c := range cases {
		if got := IsTickler(c.labels); got != c.want {
			t.Errorf("IsTickler(%v) = %v, 기대 %v", c.labels, got, c.want)
		}
	}
}

// FiresOn 은 `fires:YYYY-MM-DD` 꼬리표에서 **언제 열리나**를 읽는다.
//
// 이 축이 없으면 보드가 낼 수 있는 것은 나이뿐이고, 기한 없는 나이는 뜻이 없다 —
// 2026-08-12 에 네 세션이 같은 재측을 돌린 이유가 그것이다.
func TestFiresOnReadsTheDateLabel(t *testing.T) {
	day := func(y int, m time.Month, d int) time.Time {
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	}
	cases := []struct {
		name   string
		labels []string
		want   time.Time
		ok     bool
	}{
		{"없다", []string{"tickler"}, time.Time{}, false},
		{"빈 것", nil, time.Time{}, false},
		{"정상", []string{"tickler", "fires:2026-08-19"}, day(2026, time.August, 19), true},
		{"순서 무관", []string{"fires:2026-09-03", "tickler"}, day(2026, time.September, 3), true},
		// 티클러가 아니어도 읽는다 — 판정이 아니라 표시라, 부르는 쪽이 조합을 정한다.
		{"꼬리표 tickler 없이도", []string{"fires:2026-08-19"}, day(2026, time.August, 19), true},

		// 아래는 전부 **조용히 무시**한다. 표시 축이 잘못된 값 때문에 죽으면 안 되고,
		// 근사를 허용하면 자유 문자열이 우연히 걸린다(IsTickler 와 같은 규율).
		{"형식 틀림", []string{"fires:08-19"}, time.Time{}, false},
		{"날짜 아님", []string{"fires:내일"}, time.Time{}, false},
		{"없는 날", []string{"fires:2026-02-30"}, time.Time{}, false},
		{"접두만", []string{"fires:"}, time.Time{}, false},
		{"대문자 근사 금지", []string{"FIRES:2026-08-19"}, time.Time{}, false},
		{"시각까지 주면 안 받는다", []string{"fires:2026-08-19T00:00:00Z"}, time.Time{}, false},
	}
	for _, c := range cases {
		got, ok := FiresOn(c.labels)
		if ok != c.ok || !got.Equal(c.want) {
			t.Errorf("%s: FiresOn(%v) = (%v, %v), 기대 (%v, %v)",
				c.name, c.labels, got, ok, c.want, c.ok)
		}
	}
}

// 값이 여럿이면 **가장 이른 것**이 이긴다 — 기한이 둘이면 먼저 오는 것이 기한이다.
func TestFiresOnTakesTheEarliestWhenThereAreSeveral(t *testing.T) {
	got, ok := FiresOn([]string{"fires:2026-09-03", "tickler", "fires:2026-08-19"})
	if !ok {
		t.Fatalf("여럿일 때 아무것도 안 읽었다")
	}
	want := time.Date(2026, time.August, 19, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("FiresOn = %v, 기대 %v — 둘이면 먼저 오는 것이 기한이다", got, want)
	}
}
