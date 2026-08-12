package mcpsrv

import (
	"fmt"
	"strings"

	"github.com/kweiza/flightdeck/internal/service"
)

// RenderLabel 은 꼬리표 고침 결과 한 조각이다.
//
// ★ **실제 변화분을 낸다** — 요청한 것이 아니라. 이미 있는 것을 더하거나 없는 것을
// 빼는 것은 집합 연산이라 거절하지 않지만, 그때 "더했다"고만 말하면 안 바뀐 것을
// 바뀐 줄 안다. 조용한 무변화를 안 만드는 것이 이 함수의 존재 이유다.
//
// 이 파일이 render.go 와 따로 있는 이유는 그 파일이 이미 크고, 새 렌더가 그쪽
// 대공사와 같은 자리를 다투기 때문이다. 렌더 하나가 자기 파일을 갖는 것은
// followups_arrival.go·drift.go 가 이미 하는 방식이다.
func RenderLabel(res service.LabelResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "label · %s 의 꼬리표를 고쳤다\n", res.Item.ID)
	fmt.Fprintf(&b, "실제로 더한 것: %s\n", labelsOrNone(res.Added))
	fmt.Fprintf(&b, "실제로 뺀 것: %s\n", labelsOrNone(res.Removed))
	fmt.Fprintf(&b, "지금 꼬리표: %s\n", labelsOrNone(res.After))
	if containsLabel(res.After, "tickler") {
		b.WriteString("tickler 가 붙었다 — 이 항목은 굶김 축(집계·★·기아 가중)에서 빠진다. " +
			"**배제가 아니다**: 추천·선점·겹침 어디에서도 이 꼬리표를 안 보고, 기한이 오면 집어야 한다.\n")
		b.WriteString("★ 묶음에서는 **선두에 달아야** 걸린다 — 구성원에 달면 그 묶음의 굶김은 안 바뀐다.\n")
	}
	return b.String()
}

// labelsOrNone 은 빈 목록을 "없음"으로 낸다.
// 빈 줄로 내면 "없다"와 "이 축을 안 읽었다"가 화면에서 같아진다.
func labelsOrNone(ls []string) string {
	if len(ls) == 0 {
		return "없음"
	}
	return strings.Join(ls, ", ")
}

func containsLabel(ls []string, want string) bool {
	for _, l := range ls {
		if l == want {
			return true
		}
	}
	return false
}
