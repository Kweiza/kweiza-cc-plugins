package mcpsrv

import (
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

func TestRenderLabelShowsWhatActuallyChanged(t *testing.T) {
	got := RenderLabel(service.LabelResult{
		Item:  model.Item{ID: "it"},
		After: []string{"a"}, Added: nil, Removed: nil,
	})
	if !strings.Contains(got, "실제로 더한 것: 없음") {
		t.Errorf("변화가 없는데 화면이 그것을 안 말한다:\n%s", got)
	}
}

// 티클러가 붙으면 그 뜻과 **선두 규칙**을 그 자리에서 말한다.
// 선두 규칙이 아무 데도 안 적혀 있던 것이 이 표면을 만든 사고의 절반이었다.
func TestRenderLabelExplainsTicklerAndLeadRule(t *testing.T) {
	got := RenderLabel(service.LabelResult{
		Item:  model.Item{ID: "it"},
		After: []string{"tickler"}, Added: []string{"tickler"},
	})
	if !strings.Contains(got, "굶김 축") {
		t.Errorf("tickler 를 달았는데 그 뜻을 안 말한다:\n%s", got)
	}
	if !strings.Contains(got, "배제가 아니다") {
		t.Errorf("배제가 아니라는 것을 안 말한다 — 그러면 다음 사람이 이 항목이 안 잡히는 줄 안다:\n%s", got)
	}
	if !strings.Contains(got, "선두") {
		t.Errorf("묶음에서 선두에 달아야 한다는 것을 안 말한다 — 그 침묵이 이 표면을 만든 사고였다:\n%s", got)
	}
}
