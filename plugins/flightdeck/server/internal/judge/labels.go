package judge

import "strings"

// ApplyLabels 는 꼬리표 집합에 더하기와 빼기를 적용한다. 순수 함수다.
//
// ★ 계약 셋이 있고 셋 다 이유가 있다.
//
//  1. **기존 순서를 지킨다.** 정렬하지 않는다 — labels 는 JSON 배열로 저장되고
//     되쓰기 산출물(legacy/export.go)이 원본과 diff 로 대조된다. 정렬을 걸면 판이
//     바뀔 때 무관한 항목들의 줄이 통째로 흔들리고, 그 산출물의 존재 이유가 무너진다.
//     새로 더한 것은 **뒤에** 붙는다.
//
//  2. **빼기가 더하기를 이긴다.** 같은 값이 add·rm 에 함께 오면 결과에서 빠진다.
//     반대로 정하면 "지워라"가 조용히 무시되는데, 이 함수의 소비자는 사람이 방금
//     친 명령이라 무시된 의도가 화면에 안 나타난다.
//
//  3. **nil 을 안 낸다.** 빈 슬라이스다 — nil 은 JSON 에서 `null` 이 되고 빈
//     슬라이스는 `[]` 가 된다. 두 값은 되읽기에서 같아 보이지만 원장에 남는
//     글자가 다르다.
//
// 공백만 있는 값과 빈 문자열은 버린다. 판정의 정본은 IsTickler 이고 그것은 정확
// 일치만 보므로(tickler.go), 앞뒤 공백이 붙은 채 저장되면 사람 눈에는 같은
// 꼬리표인데 판정에서 조용히 빠진다.
func ApplyLabels(cur, add, rm []string) []string {
	drop := make(map[string]bool, len(rm))
	for _, l := range rm {
		if l = strings.TrimSpace(l); l != "" {
			drop[l] = true
		}
	}

	out := make([]string, 0, len(cur)+len(add))
	seen := make(map[string]bool, len(cur)+len(add))
	keep := func(l string) {
		l = strings.TrimSpace(l)
		if l == "" || drop[l] || seen[l] {
			return
		}
		seen[l] = true
		out = append(out, l)
	}

	for _, l := range cur {
		keep(l)
	}
	for _, l := range add {
		keep(l)
	}
	return out
}
