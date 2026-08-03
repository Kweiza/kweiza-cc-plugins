// Package judge 는 flightdeck 의 판정 로직을 담는다.
//
// 이 패키지에 있는 것은 전부 순수 함수다 — 상태도 I/O 도 없다.
// 판정이 핸들러 본문에 흩어지면 시험이 그 로직의 **사본**을 단정하게 되고,
// 그러면 변이가 조용히 새어 나간다. 시험은 반드시 여기 있는 함수를 직접 부른다.
//
// 그리고 다중 조건 판정은 불리언이 아니라 **사유**를 돌려준다.
// 사유가 없으면 "조건 A 때문에 탈락"과 "이 축을 아예 안 본다"가 구분되지 않고,
// 그 구분이 안 되는 도구는 두 번째 세션부터 무시된다.
package judge

import "strings"

// PathsOverlap 은 두 경로 집합이 겹치는지 판정한다.
//
// 기존 도구의 이 자리에 결함이 있었다. 모든 토큰 끝에 "/" 를 붙여 디렉토리로 정규화했기 때문에
// 파일형 토큰이 **자기 자신과도 안 겹쳤다**:
//
//	PathsOverlap(["Makefile"], ["Makefile"])          → 겹침 없음   ← 결함
//	PathsOverlap([".gitleaks.toml"], [".gitleaks.toml tools/x.sh"]) → 겹침 없음  ← 결함
//
// 실측 시점 큐 226건 중 파일형 토큰이 33건이었고 그 전부의 경로 축이 죽어 있었다.
// 그래서 여기서는 **경로 성분(component) 단위**로 비교한다 — 문자열 접두가 아니라.
// 문자열 접두로 하면 "tool/" 이 "tools/" 를 덮는 반대 방향 오탐이 생긴다.
//
// 판정 규칙: 두 경로가 같거나, 한쪽이 다른 쪽의 **조상 디렉토리**이면 겹친다.
func PathsOverlap(a, b []string) bool {
	for _, x := range a {
		cx := components(x)
		if len(cx) == 0 {
			continue
		}
		for _, y := range b {
			cy := components(y)
			if len(cy) == 0 {
				continue
			}
			if pathRelated(cx, cy) {
				return true
			}
		}
	}
	return false
}

// OverlapPairs 는 겹치는 (a, b) 쌍을 전부 돌려준다.
// 사용자에게 "무엇이 겹치는지"를 보여야 하므로 불리언만으로는 부족하다 —
// 거르지 않고 알리는 것이 이 도구의 규율이고, 알리려면 무엇이 겹쳤는지 말할 수 있어야 한다.
func OverlapPairs(a, b []string) [][2]string {
	var out [][2]string
	seen := map[[2]string]bool{}
	for _, x := range a {
		cx := components(x)
		if len(cx) == 0 {
			continue
		}
		for _, y := range b {
			cy := components(y)
			if len(cy) == 0 {
				continue
			}
			if pathRelated(cx, cy) {
				k := [2]string{x, y}
				if !seen[k] {
					seen[k] = true
					out = append(out, k)
				}
			}
		}
	}
	return out
}

// pathRelated 은 한쪽이 다른 쪽과 같거나 조상인지 본다.
// 성분 단위이므로 "tool" 과 "tools" 는 절대 관련되지 않는다.
func pathRelated(x, y []string) bool {
	n := len(x)
	if len(y) < n {
		n = len(y)
	}
	for i := 0; i < n; i++ {
		if x[i] != y[i] {
			return false
		}
	}
	// 여기까지 왔으면 짧은 쪽이 긴 쪽의 접두 성분열이다 = 같거나 조상이다.
	return true
}

// components 는 경로를 성분으로 쪼갠다.
// 앞뒤 "/", 중복 "/", "." 성분을 걷어낸다. ".." 는 걷어내지 않는다 —
// 등록 목록에 ".." 가 들어오는 것은 입력 오류이고, 조용히 정규화하면 그 오류가 안 보인다.
func components(p string) []string {
	p = strings.TrimSpace(p)
	if p == "" {
		return nil
	}
	var out []string
	for _, c := range strings.Split(p, "/") {
		if c == "" || c == "." {
			continue
		}
		out = append(out, c)
	}
	return out
}
