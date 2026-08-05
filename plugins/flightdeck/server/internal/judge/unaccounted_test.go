package judge

import (
	"strings"
	"testing"
)

// UnaccountedIDs 는 "보낸 것과 돌아온 것"의 차집합이다. 순수 함수라 여기서 못박는다 —
// 이 판정이 틀리면 두 소비자(cmd/fd 의 종료코드 · mcpsrv 의 isError)가 동시에 틀린다.
func TestUnaccountedIDs(t *testing.T) {
	cases := []struct {
		name      string
		requested []string
		accounted []string
		want      string
	}{
		{"전부 설명됐다", []string{"a", "b"}, []string{"a", "b"}, ""},
		{"구서버가 선두만 집었다", []string{"a", "b", "c"}, []string{"a"}, "b,c"},
		// 순서는 **요청 순서**다. 사람이 명령줄에 적은 순서 그대로 불려야
		// 어느 인자가 빠졌는지를 눈으로 짚을 수 있다.
		{"요청 순서를 지킨다", []string{"z", "y", "x"}, []string{"y"}, "z,x"},
		// 못 집은 것도 **설명은 된 것**이다 — 탈락 사유가 실려 오면 이름이 불린 것이다.
		{"설명됐으면 집혔는지는 안 본다", []string{"a", "b"}, []string{"b", "a"}, ""},
		{"공백은 양쪽에서 다듬는다", []string{" a ", "b"}, []string{"a"}, "b"},
		// ★ 위 줄은 이름이 "양쪽"인데 **요청 쪽만** 다듬는다. 응답 쪽 TrimSpace 를 지워도
		// 전 스위트가 초록이었다(실측). 그 상태로 배포되면, 응답이 id 를 한 칸 띄워 실어 온
		// 순간 `fd pick a b` 가 **집힌 항목을 "안 집혔다"로 신고하고 종료코드를 세운다** —
		// 세션은 자기가 쥔 것을 안 쥔 줄 알고 다시 집으러 가거나 손을 뗀다. 이 함수가
		// 존재하는 이유(쥔 줄 알았는데 안 쥔 상태를 없앤다)의 정확한 반대다.
		{"응답 쪽 공백도 다듬는다", []string{"a", "b"}, []string{" a "}, "b"},
		{"요청의 중복은 한 번만 센다", []string{"a", "a", "b"}, []string{}, "a,b"},
		// 응답이 아예 없는 갈래(설명 0건)도 조용히 지나가면 안 된다.
		{"설명이 하나도 없다", []string{"a"}, nil, "a"},
		{"요청이 없으면 빠진 것도 없다", nil, []string{"a"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := strings.Join(UnaccountedIDs(c.requested, c.accounted), ",")
			if got != c.want {
				t.Fatalf("빠진 id 가 %q 다 — %q 를 기대했다", got, c.want)
			}
		})
	}
}
