package mcpsrv

import (
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// TestRenderAmendNamesActualChange 는 실제로 바뀐 축만 말하는지 본다.
func TestRenderAmendNamesActualChange(t *testing.T) {
	got := RenderAmend(service.AmendResult{
		Item:    model.Item{ID: "i1", Title: "새 제목"},
		Rev:     3,
		Changed: []string{"title"},
	})
	if !strings.Contains(got, "title") {
		t.Errorf("바뀐 축을 안 말한다:\n%s", got)
	}
	if strings.Contains(got, "body") {
		t.Errorf("안 바뀐 축을 말한다:\n%s", got)
	}
	if !strings.Contains(got, "3") {
		t.Errorf("rev 가 없다 — 되돌릴 좌표가 그 자리에서 나와야 한다:\n%s", got)
	}
}

// TestRenderAmendSaysNothingChanged 는 무변화를 **무변화로** 말하는지 본다.
//
// "고쳤다"고만 말하면 사람은 안 바뀐 것을 바뀐 줄 안다 — RenderLabel 이 같은 이유로
// 같은 것을 한다.
func TestRenderAmendSaysNothingChanged(t *testing.T) {
	got := RenderAmend(service.AmendResult{
		Item: model.Item{ID: "i1"}, Rev: 1, Changed: nil,
	})
	if !strings.Contains(got, "안 바뀌었다") {
		t.Errorf("무변화를 무변화로 안 말한다:\n%s", got)
	}
}

// TestRenderAmendWarnsOnPathsChange 는 경로를 고쳤을 때 겹침 파급을 그 자리에서 내는지 본다.
func TestRenderAmendWarnsOnPathsChange(t *testing.T) {
	got := RenderAmend(service.AmendResult{
		Item: model.Item{ID: "i1", Paths: []string{"a"}},
		Rev:  1, Changed: []string{"paths"},
		Overlaps: []judge.Overlap{{SessionID: "01OTHERSESSION"}},
	})
	if !strings.Contains(got, "겹") {
		t.Errorf("경로를 고쳤는데 겹침 파급을 안 낸다:\n%s", got)
	}
}

// TestRenderAmendSaysOverlapUnknown 은 못 센 것과 0을 가르는지 본다.
func TestRenderAmendSaysOverlapUnknown(t *testing.T) {
	res := service.AmendResult{
		Item: model.Item{ID: "i1"}, Rev: 1, Changed: []string{"paths"},
	}
	// hasFailureAxis(res.Derived, "overlaps") 가 참이 되게 겹침 축을 실패로 적는다 —
	// render.go 의 render_partial_test.go·render_note_cross_project_test.go 와 같은 관용구다.
	res.Failures = append(res.Failures, service.DerivedFailure{Axis: "overlaps", Detail: "겹침을 못 셌다"})
	got := RenderAmend(res)
	if !strings.Contains(got, "못 셌다") {
		t.Errorf("못 센 것을 0으로 낸다 — 둘은 다른 사실이다:\n%s", got)
	}
}
