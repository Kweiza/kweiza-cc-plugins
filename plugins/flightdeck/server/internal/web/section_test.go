package web

import (
	"strings"
	"testing"
)

// 이 파일은 **절 단위로 재는 도구**와 그 도구 자체를 지키는 시험을 함께 둔다.
//
// ★ 왜 도구에 시험이 붙나. 단정을 좁히는 헬퍼가 잘못 자르면 **좁힌 척만 하고 아무도
// 모른다** — 이 패키지가 지금 고치고 있는 병이 정확히 그것이다(페이지 전체에 건 단정이
// 다른 절 때문에 조용히 초록). 절을 못 찾는 것은 Fatal 이라 시끄럽지만, 나머지 둘은
// 조용하다:
//
//   ① 경계가 새는 것 — 다음 절까지 담으면 좁히기 전과 같아진다.
//   ② 빈 문자열을 돌려주는 것 — 빈 haystack 은 mustNotContain 을 **전부** 통과시킨다.
//      좁힐수록 시험이 약해지는 방향의 실패라 가장 늦게 발견된다.
//
// TestSectionOfCutsAtRealBoundaries 가 그 둘을 막는다.

// 여섯 절의 id 와 머리 문자열. 절 개수 자체는 render_test.go 가 잠근다 —
// 여기 목록은 그 여섯을 **각각 잘라낼 수 있는지**를 잰다.
var sectionHeads = map[string]string{
	"now":     "① 지금",
	"unacked": "② 미확인 결과",
	"queue":   "③ 큐",
	"landing": "④ 랜딩 이력",
	"blocked": "⑤ 막힘",
	"search":  "⑥ 판단 검색",
}

// sectionOf 는 렌더된 페이지에서 <section id="…"> 하나의 **안쪽만** 잘라낸다.
//
// 끝을 `</section>` 으로 잡는다. 다음 `<section` 으로 잡으면 마지막 절(⑥)에 끝 표지가
// 없어서 </main> 뒤의 <script>·</html> 이 통째로 딸려 온다 — 그러면 ⑥에 건 단정은
// 좁히기 전과 같은 haystack 을 본다.
func sectionOf(t *testing.T, html, id string) string {
	t.Helper()
	head := `<section id="` + id + `">`
	i := strings.Index(html, head)
	if i < 0 {
		t.Fatalf("절 %q 가 화면에 없다 — 이 헬퍼로 좁힌 단정은 전부 여기서 멈춘다", id)
	}
	sec := html[i+len(head):]
	j := strings.Index(sec, "</section>")
	if j < 0 {
		t.Fatalf("절 %q 가 안 닫혔다 — 여기서 안 끊으면 페이지 꼬리가 딸려 와 좁힌 척이 된다", id)
	}
	return sec[:j]
}

// TestSectionOfCutsAtRealBoundaries 는 **헬퍼가 실제로 좁히는지**를 잰다.
//
// 이 시험이 없으면 sectionOf 가 조각을 잘못 잘라도 그 위에 세운 단정 수십 개가 전부
// 조용히 뜻을 잃는다. 좁히기의 값어치는 경계가 참일 때만 생긴다.
func TestSectionOfCutsAtRealBoundaries(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	f.openSession("cc-1", "트랙2")
	_, html := f.get("")

	for id, head := range sectionHeads {
		sec := sectionOf(t, html, id)

		// ① 비지 않는다. 빈 조각은 mustNotContain 을 전부 통과시킨다.
		if strings.TrimSpace(sec) == "" {
			t.Fatalf("절 %q 가 빈 조각이다 — 빈 haystack 에 건 mustNotContain 은 무조건 초록이다", id)
		}
		// ② 자기 절의 머리를 담는다. 안 담으면 엉뚱한 자리를 잘랐다는 뜻이다.
		mustContain(t, sec, head, "잘라낸 조각에 절 "+id+" 의 머리가 없다")
		// ③ 남의 절은 안 담는다 — 경계가 새는지를 재는 자리다.
		for other, otherHead := range sectionHeads {
			if other == id {
				continue
			}
			mustNotContain(t, sec, otherHead, "절 "+id+" 가 절 "+other+" 까지 담았다")
		}
		// ④ 절 밖(스타일·스크립트)도 안 담는다. ⑥이 </main> 뒤를 삼키는 실패가 여기서 잡힌다.
		for _, outside := range []string{"prefers-color-scheme", "new EventSource(path)"} {
			mustNotContain(t, sec, outside, "절 "+id+" 가 페이지 꼬리를 담았다")
		}
	}
}
