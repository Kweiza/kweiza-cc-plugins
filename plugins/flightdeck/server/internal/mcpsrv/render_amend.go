package mcpsrv

import (
	"fmt"
	"strings"

	"github.com/kweiza/flightdeck/internal/service"
)

// RenderAmend 는 본문 수정의 응답이다.
//
// ★ **이 동사의 규율은 전부 여기 있다.** 도구 설명(90자)에도 스킬에도 안 넣는다 —
// 세션 시작 컨텍스트는 이름 하나만 받고, 규율은 필요할 때 그 자리에서만 실린다.
// 여기 실리는 것 셋: ⓐ 실제 변화분(요청이 아니라) ⓑ 되돌릴 좌표(rev) ⓒ 경로를
// 고쳤을 때의 겹침 파급.
//
// 이 파일이 render.go 와 따로 있는 이유는 RenderLabel 과 같다 — 렌더 하나가 자기
// 파일을 갖는 것은 followups_arrival.go·drift.go 가 이미 하는 방식이다.
func RenderAmend(res service.AmendResult) string {
	var b strings.Builder

	if len(res.Changed) == 0 {
		fmt.Fprintf(&b, "amend · %s — **아무것도 안 바뀌었다**(준 값이 지금 값과 같다)\n",
			res.Item.ID)
	} else {
		fmt.Fprintf(&b, "amend · %s 의 %s 를 고쳤다\n",
			res.Item.ID, strings.Join(res.Changed, "·"))
	}
	fmt.Fprintf(&b, "개정 %d — 옛 값은 그대로 남는다(item_revision 은 추가 전용이다)\n", res.Rev)

	if containsAxis(res.Changed, "title") {
		fmt.Fprintf(&b, "제목: %s\n", res.Item.Title)
	}
	if containsAxis(res.Changed, "paths") {
		fmt.Fprintf(&b, "경로 %d: %s\n", len(res.Item.Paths), strings.Join(res.Item.Paths, ", "))
		// ★ 이 축은 **남의 화면을 움직인다.** 고친 사람이 그 사실을 알 다른 경로가 없다.
		switch {
		case hasFailureAxis(res.Derived, "overlaps"):
			b.WriteString("겹침: 이 수정으로 겹치게 된 세션을 **못 셌다** — 0이라는 뜻이 아니다. " +
				"`board` 가 그 축을 다시 읽는다\n")
		case len(res.Overlaps) == 0:
			b.WriteString("겹침: 지금 이 경로를 만지는 다른 세션은 없다\n")
		default:
			fmt.Fprintf(&b, "겹침: 이 수정으로 **세션 %d개와 경로가 겹친다** — "+
				"경로는 겹침 판정의 입력이라 남의 화면도 함께 움직였다\n", len(res.Overlaps))
		}
	}

	return b.String()
}
