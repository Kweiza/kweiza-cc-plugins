package judge

import "testing"

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
