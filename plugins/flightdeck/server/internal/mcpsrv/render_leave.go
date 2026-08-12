package mcpsrv

import (
	"fmt"
	"strings"

	"github.com/kweiza/flightdeck/internal/service"
)

// RenderLeave 는 자기 선점 반납의 결과 한 조각이다.
//
// ★ **"닫았다"고 말하지 않는다.** 이 표면이 생긴 이유가 정확히 그것이다 — 반납이 없어서
// finish(dropped) 로 때우면 원장에 "폐기됐다"로 남고, 후속으로 다시 세우면 id 가 바뀐다.
// 그래서 응답이 **항목이 살아 있다**는 사실을 그 자리에서 말한다. 안 그러면 다음 사람이
// (그리고 같은 세션이) 방금 무슨 일이 일어났는지를 finish 와 구분하지 못한다.
//
// 이 파일이 render.go 와 따로 있는 이유는 render_label.go 와 같다 — 그 파일은 크고
// 다른 세션이 자주 고친다. 렌더 하나가 자기 파일을 갖는 것은 이 패키지의 기존 방식이다.
func RenderLeave(res service.ClaimLeaveResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "pick · 선점을 놓았다 (%d건)\n", len(res.Items))
	for _, id := range res.Items {
		fmt.Fprintf(&b, "  · %s — **open 으로 돌아갔다**\n", id)
	}
	b.WriteString("항목은 안 닫혔다 — id·본문·이력·`after` 참조가 그대로다. 다음 pick 이 집을 수 있다.\n")
	if res.JudgmentID != "" {
		fmt.Fprintf(&b, "판단 %s 에 남겼다(not-done) — **왜 안 했나**가 거기 있다.\n", res.JudgmentID)
	}
	// ★ 반납과 회수를 응답에서 갈라 말한다. 둘을 같은 말로 내면 "내가 놓은 것"과
	//   "누가 뺏은 것"이 원장에서 같은 사건으로 읽히고, 그 구분이 이 축의 전부다.
	b.WriteString("남의 선점을 푸는 것은 이것이 아니라 회수다 — 사람이 여섯 축을 보고 판정한다.\n")
	return b.String()
}
