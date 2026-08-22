package mcpsrv

import (
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/service"
)

// renderAfterCheck 은 **어느 갈래에서도 침묵하지 않는다** — renderPathCheck 과 같은 규율이다.
//
// 침묵하면 "충족됐다"와 "이 축을 안 봤다"가 같은 화면이 되고, 그러면 이 줄이 막으려는
// 바로 그 사고(막힌 항목을 조용히 집는 것)를 화면이 다시 만든다.
func TestRenderAfterCheckNeverGoesSilent(t *testing.T) {
	cases := []struct {
		name string
		v    *service.AfterVerdict
		want []string // 전부 들어 있어야 한다
		deny []string // 하나도 있으면 안 된다
	}{
		{
			name: "nil — 안 읽었다",
			v:    nil,
			want: []string{"이 응답에 없다"},
			// ★ nil 을 "충족됐다"로 접으면 낡은 캐시가 관측한 적 없는 통과를 단정한다.
			deny: []string{"전부 충족됐다", "미충족"},
		},
		{
			name: "충족",
			v:    &service.AfterVerdict{Satisfied: true},
			want: []string{"전부 충족됐다"},
			deny: []string{"미충족", "막지 않는다"},
		},
		{
			name: "미충족 · 단독",
			v: &service.AfterVerdict{Reasons: []string{
				"after-unmet-item: dep_item=base state=open"}},
			want: []string{"미충족", "after-unmet-item", "base", "막지 않는다", "fd after cut"},
			// 단독 호출에는 함께 집는 선행이 없다 — 그 문장이 뜨면 거짓말이다.
			deny: []string{"함께 집는다"},
		},
		{
			name: "미충족 · 전부 이 호출이 함께 집는다",
			v: &service.AfterVerdict{
				Reasons:    []string{"after-unmet-item: dep_item=lead state=claimed"},
				WithInCall: []string{"lead"},
			},
			want: []string{"함께 집는다", "lead", "정상 경로다"},
			// ★ 정상 경로에 거절조로 말하면 안 된다. 실측 53건 중 27건(51%)이 이 모양이라,
			// 여기서 "막지 않는다"가 함께 뜨면 그 줄은 곧 잡음이 되어 아무도 안 읽는다.
			deny: []string{"막지 않는다"},
		},
		{
			name: "미충족 · 일부만 함께 집는다",
			v: &service.AfterVerdict{
				Reasons: []string{
					"after-unmet-item: dep_item=lead state=claimed",
					"after-dropped-dep: dep_item=gone 이(가) 폐기됐다",
				},
				WithInCall: []string{"lead"},
			},
			// 둘 다 말해야 한다 — 하나는 정상 경로이고 하나는 아니다.
			want: []string{"함께 집는다", "lead", "막지 않는다", "fd after cut"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := renderAfterCheck(c.v, "")
			if strings.TrimSpace(got) == "" {
				t.Fatal("빈 문자열이다 — 이 줄은 어느 갈래에서도 침묵하지 않는다")
			}
			if !strings.HasSuffix(got, "\n") {
				t.Errorf("줄바꿈으로 안 끝난다 — 다음 줄과 붙는다: %q", got)
			}
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("%q 가 없다:\n%s", w, got)
				}
			}
			for _, dn := range c.deny {
				if strings.Contains(got, dn) {
					t.Errorf("%q 가 있으면 안 된다:\n%s", dn, got)
				}
			}
		})
	}
}

// pad 는 구성원 절의 들여쓰기다 — 모든 줄에 붙어야 절이 안 깨진다.
func TestRenderAfterCheckIndentsEveryLine(t *testing.T) {
	v := &service.AfterVerdict{
		Reasons: []string{"after-unmet-item: dep_item=lead state=claimed",
			"after-dropped-dep: dep_item=gone"},
		WithInCall: []string{"lead"},
	}
	got := renderAfterCheck(v, "    ")
	for i, ln := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if !strings.HasPrefix(ln, "    ") {
			t.Errorf("%d번 줄에 들여쓰기가 없다 — 구성원 절이 깨진다: %q", i, ln)
		}
	}
}
