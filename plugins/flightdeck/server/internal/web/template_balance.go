package web

import (
	"fmt"
	"regexp"
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
// 템플릿의 **행동 블록 균형** 검사 — 순수 함수
//
// ★ 이 검사가 있는 이유는 실제로 낸 결함 때문이다. 접기를 넣으면서 `{{range}}` 를
// `<tbody>` 로 감쌀 때 짝이 아닌 **첫 번째 `{{end}}`** 를 경계로 잡았고,
// 그래서 `</tbody>` 가 `<td>` 안에 들어가 표가 깨졌다. 브라우저는 그런 표를
// 다르게 재구성하므로 접기 스크립트가 행을 하나도 못 찾았다.
//
// 렌더 결과를 문자열로 단정하는 시험은 이 축을 **원리적으로 못 본다** —
// 필요한 문자열은 전부 들어 있기 때문이다. 그리고 HTML 파서를 손으로 쓰는 것은
// 검사기 자체가 검사 대상보다 틀리기 쉬웠다(시도했다가 접었다).
// 그래서 렌더 **결과**가 아니라 템플릿 **원문**에서, 내가 실제로 틀린 그 축만 본다.
//
// 이 검사가 보는 것: 여는 태그와 닫는 태그 사이에서 `{{range|if|with|block}}` 과
// `{{end}}` 의 수가 맞는가. 안 맞으면 그 구간이 다른 블록을 반쯤 가른 것이다.
// **이것이 HTML 이 성하다는 뜻은 아니다** — 이름과 이 주석이 그 범위를 못박는다.
// ─────────────────────────────────────────────────────────────────────────────

var actionRe = regexp.MustCompile(`\{\{-?\s*(range|if|with|block|define|end)\b`)

// UnbalancedWrappers 는 `<tag ...>` … `</tag>` 로 감싼 구간이 템플릿 블록을
// 반쯤 가르는 자리를 전부 찾는다. 사유 문자열로 돌려준다 —
// "어딘가 안 맞는다"만 알면 어느 구간인지 찾느라 다시 눈으로 훑게 된다.
//
// ★ **중첩되지 않는 태그에만 쓸 수 있다.** 여는 태그를 가장 가까운 닫는 태그와 짝짓기
// 때문이다 — `div` 처럼 중첩되는 태그에 걸면 안쪽 것이 바깥 것을 닫은 것으로 읽혀
// 오탐이 쏟아진다(실제로 그랬다). 호출자가 그 전제를 지키는지는 [AssertNotNested] 가 본다.
// 검사 범위를 주장 범위보다 넓게 잡지 않는 것이 이 주석의 목적이다.
func UnbalancedWrappers(tpl string, tag string) []string {
	open, closeT := "<"+tag, "</"+tag+">"
	var out []string
	i := 0
	for {
		s := strings.Index(tpl[i:], open)
		if s < 0 {
			return out
		}
		s += i
		gt := strings.Index(tpl[s:], ">")
		if gt < 0 {
			return append(out, fmt.Sprintf("<%s 가 닫히지 않았다(위치 %d)", tag, s))
		}
		body := s + gt + 1
		e := strings.Index(tpl[body:], closeT)
		if e < 0 {
			return append(out, fmt.Sprintf("<%s>(위치 %d)에 짝 </%s> 가 없다", tag, s, tag))
		}
		seg := tpl[body : body+e]

		depth := 0
		for _, m := range actionRe.FindAllStringSubmatch(seg, -1) {
			if m[1] == "end" {
				depth--
			} else {
				depth++
			}
		}
		if depth != 0 {
			what := "블록이 덜 닫혔다"
			if depth < 0 {
				what = "남의 블록의 {{end}} 를 삼켰다 — 짝이 아닌 {{end}} 를 경계로 잡았다"
			}
			out = append(out, fmt.Sprintf("<%s>…</%s>(위치 %d) 구간의 블록 균형이 %+d 다 — %s",
				tag, tag, s, depth, what))
		}
		i = body + e + len(closeT)
	}
}

// AssertNotNested 는 그 태그가 이 원문에서 **중첩되지 않는지** 본다.
// [UnbalancedWrappers] 의 전제이고, 전제가 깨지면 그 결과를 믿으면 안 된다 —
// 대조는 자기 전제를 먼저 증명해야 한다.
func AssertNotNested(tpl, tag string) error {
	open, closeT := "<"+tag, "</"+tag+">"
	depth, i := 0, 0
	for i < len(tpl) {
		o := strings.Index(tpl[i:], open)
		c := strings.Index(tpl[i:], closeT)
		switch {
		case o < 0 && c < 0:
			if depth != 0 {
				return fmt.Errorf("<%s> 가 %d개 안 닫혔다", tag, depth)
			}
			return nil
		case c < 0 || (o >= 0 && o < c):
			depth++
			if depth > 1 {
				return fmt.Errorf("<%s> 가 중첩된다(위치 %d) — 이 검사기는 그 경우를 못 본다", tag, i+o)
			}
			i += o + len(open)
		default:
			depth--
			i += c + len(closeT)
		}
	}
	if depth != 0 {
		return fmt.Errorf("<%s> 가 %d개 안 닫혔다", tag, depth)
	}
	return nil
}
