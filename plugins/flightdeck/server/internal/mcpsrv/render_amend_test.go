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
	// ★ 맨 숫자 "3" 만 보면 "경로 3:" 같은 다른 자리의 숫자로도 만족한다(리뷰 M-3) —
	// "개정 3" 으로 조여 되돌릴 좌표(rev) 그 자체를 잡는다.
	if !strings.Contains(got, "개정 3") {
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
//
// ★ 단정을 실문구로 조인다(리뷰 I-3) — "겹" 하나만 보면 0건 분기("겹침: 지금 이 경로를
// 만지는 다른 세션은 없다")도 못 셌다 분기도 전부 통과해, 세 분기 중 무엇이 실제로
// 나왔는지를 이 시험이 구분하지 못했다. 개수(%d)까지 박아 "겹친다" 문구와 "없다"
// 문구를 맞바꾸는 변이·`%d` 를 지우는 변이 둘 다 잡는다.
func TestRenderAmendWarnsOnPathsChange(t *testing.T) {
	got := RenderAmend(service.AmendResult{
		Item: model.Item{ID: "i1", Paths: []string{"a"}},
		Rev:  1, Changed: []string{"paths"},
		Overlaps: []judge.Overlap{{SessionID: "01OTHERSESSION"}},
	})
	if !strings.Contains(got, "세션 1개와 경로가 겹친다") {
		t.Errorf("경로를 고쳤는데 겹침 파급(개수 포함)을 실문구로 안 낸다:\n%s", got)
	}
}

// TestRenderAmendSaysZeroOverlapsExplicitly 는 **진짜 0건**을 0건으로 말하는지 본다.
//
// TestRenderAmendWarnsOnPathsChange(비-0)·TestRenderAmendSaysOverlapUnknown(못 셈)과
// 나란히 둬야 셋 중 하나만 항상 참인 변이(문구를 통째로 맞바꾸거나 지우는 것)가
// 드러난다 — 이 시험이 없으면 0건 분기의 문구를 통째로 지워도(또는 다른 분기 문구로
// 바꿔치기 해도) 위 두 시험은 그대로 초록이다(리뷰 I-3).
func TestRenderAmendSaysZeroOverlapsExplicitly(t *testing.T) {
	got := RenderAmend(service.AmendResult{
		Item: model.Item{ID: "i1", Paths: []string{"a"}},
		Rev:  1, Changed: []string{"paths"},
		// Overlaps 는 nil — 겹침 계산은 성공했고(파생 실패가 아니다) 결과가 0건이다.
	})
	if !strings.Contains(got, "지금 이 경로를 만지는 다른 세션은 없다") {
		t.Errorf("진짜 0건을 0건으로 안 말한다:\n%s", got)
	}
	if strings.Contains(got, "겹친다") || strings.Contains(got, "못 셌다") {
		t.Errorf("0건인데 겹치거나 못 센 것처럼 말한다:\n%s", got)
	}
}

// TestRenderAmendOmitsOverlapLineWhenPathsUnchanged 는 paths 를 안 고쳤으면 겹침 파급을
// 아예 안 내는지 본다 — Overlaps 가 우연히 차 있어도다.
//
// containsAxis(res.Changed, "paths") 판정을 지우거나 항상 참으로 바꾸는 변이를 잡는다
// (리뷰 I-3 의 실측 표 셋째 줄). res.Overlaps 를 일부러 채워, 판정이 축이 아니라 값의
// 유무로 갈리게 바뀌어도 이 시험이 빨개지게 했다.
func TestRenderAmendOmitsOverlapLineWhenPathsUnchanged(t *testing.T) {
	got := RenderAmend(service.AmendResult{
		Item:     model.Item{ID: "i1", Title: "새 제목", Paths: []string{"a"}},
		Rev:      1,
		Changed:  []string{"title"},
		Overlaps: []judge.Overlap{{SessionID: "01OTHERSESSION"}},
	})
	if strings.Contains(got, "겹") {
		t.Errorf("paths 를 안 고쳤는데 겹침 파급을 낸다:\n%s", got)
	}
}

// TestRenderAmendSaysOverlapUnknown 은 못 센 것과 0을 가르는지 본다.
func TestRenderAmendSaysOverlapUnknown(t *testing.T) {
	res := service.AmendResult{
		// ★ Paths 를 비우면 안 된다 — 리뷰 I-4 이후 경로가 0개면 "겹침 축에 안 잡힌다"
		// 분기가 파생 실패보다 먼저 이겨서(아래로 갈 것 자체가 없으므로) 이 시험이
		// 재려는 축(overlaps 파생 실패)에 안 닿는다. 실제 서비스 시험
		// (service/amend_test.go 의 TestAmendOverlapsDeriveFailureUsesTheOverlapsAxis)도
		// 새 경로를 준 채로 겹침 계산만 실패시킨다 — 그 재현 모양을 그대로 썼다.
		Item: model.Item{ID: "i1", Paths: []string{"new/"}}, Rev: 1, Changed: []string{"paths"},
	}
	// hasFailureAxis(res.Derived, "overlaps") 가 참이 되게 겹침 축을 실패로 적는다 —
	// render.go 의 render_partial_test.go·render_note_cross_project_test.go 와 같은 관용구다.
	res.Failures = append(res.Failures, service.DerivedFailure{Axis: "overlaps", Detail: "겹침을 못 셌다"})
	got := RenderAmend(res)
	if !strings.Contains(got, "못 셌다") {
		t.Errorf("못 센 것을 0으로 낸다 — 둘은 다른 사실이다:\n%s", got)
	}
}

// TestRenderAmendSaysPathsExcludedFromOverlapAxis 는 경로를 빈 목록으로 고쳤을 때
// (paths 축은 바뀌었지만 결과가 0개인 경우) 그 사실을 RenderAdd 와 같은 문구로
// 말하는지 본다(리뷰 I-4).
//
// "겹침: 지금 이 경로를 만지는 다른 세션은 없다"를 내면 겹침을 실제로 세어 0건이
// 나온 것과 구분되지 않는다 — 진짜 이유는 이 항목이 겹침 축 자체에서 빠졌다는
// 것이다. 매달린 콜론("경로 0: ")도 함께 잡는다.
func TestRenderAmendSaysPathsExcludedFromOverlapAxis(t *testing.T) {
	got := RenderAmend(service.AmendResult{
		Item: model.Item{ID: "i1", Paths: nil}, Rev: 1, Changed: []string{"paths"},
	})
	if !strings.Contains(got, "경로 0 — 경로가 없으면 이 항목은 겹침 축에 안 잡힌다") {
		t.Errorf("경로 0개를 RenderAdd 와 다른 문구로 말한다:\n%s", got)
	}
	if strings.Contains(got, "경로 0: ") {
		t.Errorf("매달린 콜론이 남아 있다:\n%s", got)
	}
	if strings.Contains(got, "겹침:") {
		t.Errorf("경로가 없는데 겹침 줄을 따로 낸다 — 위 한 줄로 충분해야 한다:\n%s", got)
	}
}
