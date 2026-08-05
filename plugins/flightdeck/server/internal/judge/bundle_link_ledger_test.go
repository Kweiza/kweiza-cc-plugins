package judge

import (
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// Links[i].Item 은 **그 구성원 자신**의 id 다. Members 와 같은 순서·같은 길이라는
// Link 주석의 계약이 여기서 끝난다.
//
// ★ 무엇이 깨지나. 두 자리가 안 물려 있었다(실측):
//
//	① bundleAround 의 LinkOf(lead, c, sib) 를 LinkOf(c, lead, sib) 로 뒤집어도 초록.
//	② 흡수 링크의 Link{Item: c.Item.ID} 를 lead.Item.ID 로 바꿔도 초록.
//
// 둘 다 결과가 같다 — 모든 구성원의 link.item 이 **선두 자신**을 가리킨다.
// 이 필드는 그대로 응답에 실려 나가고(service.BundleMember.Link, wire 의 `link.item`),
// PickResult.AccountedIDs 가 id 원천 셋 중 하나로 읽는다. 그러면 응답은 구성원 이름을
// 세 자리 중 두 자리에서만 부르게 되고, 구성원 조회가 실패해 Item.ID 만 남는 갈래에서는
// 그 id 가 "설명 안 됨"으로 오분류돼 있지도 않은 스큐를 신고한다.
//
// ①은 덤으로 하나 더 깬다: LinkOf 는 경로 근거를 **선두 쪽 표기**로 적는데(SamePaths 의
// a 는 lead 다), 인자를 뒤집으면 화면의 경로가 구성원이 적은 표기가 된다 —
// TestLinkOfCarriesLeadSpellingAndOrder 가 함수 좌표계에서 못박은 계약이 호출부에서 샌다.
func TestBundleMemberLinksNameTheNeighborNotTheLead(t *testing.T) {
	// 형제 축으로 붙는 구성원 하나 + 선행 축으로 흡수되는 구성원 하나.
	lead := cand("a-lead", 0, []string{"plugins/flightdeck/server/"})
	sibling := cand("b-sib", 1, []string{"plugins/flightdeck/server"})
	blocked := cand("c-blocked", 2, nil, afterItem("a-lead"))

	b, rej := EligibleBundle(EligibleInput{
		Self:       "S1",
		Candidates: []Candidate{lead, sibling, blocked},
		Facts:      AfterFacts{ItemStates: map[string]model.ItemState{"a-lead": model.ItemOpen}},
	}, SiblingIndex{"a-lead": {"J1"}, "b-sib": {"J1"}})
	if b == nil {
		t.Fatalf("묶음이 nil 이다(탈락 %v)", rej)
	}
	if b.Lead.Item.ID != "a-lead" {
		t.Fatalf("전제가 깨졌다 — 선두가 %q 다", b.Lead.Item.ID)
	}
	if len(b.Members) != 2 || len(b.Links) != len(b.Members) {
		t.Fatalf("전제가 깨졌다 — 구성원 %v, 링크 %d개", memberIDs(b), len(b.Links))
	}
	for i, m := range b.Members {
		if b.Links[i].Item != m.Item.ID {
			t.Errorf("구성원 %q 의 링크가 %q 를 가리킨다 — 이웃 자리에 선두가 앉으면 응답이 구성원 이름을 잃는다",
				m.Item.ID, b.Links[i].Item)
		}
		if b.Links[i].Item == b.Lead.Item.ID {
			t.Errorf("구성원 %q 의 링크가 선두 자신을 가리킨다", m.Item.ID)
		}
	}
	// 경로 근거는 **선두 표기**다. 선두는 뒤에 슬래시가 붙은 쪽으로 적었다.
	for i, m := range b.Members {
		if m.Item.ID != "b-sib" {
			continue
		}
		if !strings.Contains(b.Links[i].Detail, "plugins/flightdeck/server/") {
			t.Errorf("경로 근거가 선두 표기가 아니다: %q — 사람이 자기가 적은 줄을 못 찾는다", b.Links[i].Detail)
		}
	}
}

// not-top 사유의 "묶음 N건"은 **선두를 포함한** 수다.
//
// ★ 무엇이 깨지나. len(best.Members)+1 을 len(best.Members) 로 바꿔도 전 스위트가
// 초록이었다(실측). Bundle.Reason 쪽의 같은 값은 TestEligibleBundleCarriesReason 이
// 잡지만, 원장에 남는 이 줄은 아무도 안 봤다. 이 사유는 "왜 저것이 아니라 이것인가"에
// 답하라고 있는 자리인데, 구성원 하나짜리 추천이 "묶음 1건"이 아니라 "묶음 0건"으로
// 남으면 원장을 세는 사람이 **원소 없는 묶음이 이겼다**고 읽는다. 크기는 정렬 키 ②의
// 실제 값이라 그 오독은 곧 "정렬 규칙이 틀렸다"는 결론으로 간다.
func TestNotTopDetailCountsTheLeadInTheBundleSize(t *testing.T) {
	// y1·y2 는 판단 J1 로 묶여 2건, lonely 는 단독이라 not-top 이 된다.
	b, rej := EligibleBundle(EligibleInput{Self: "S1", Candidates: []Candidate{
		cand("y1", 0, nil), cand("y2", 1, nil), cand("lonely", 5, nil),
	}}, SiblingIndex{"y1": {"J1"}, "y2": {"J1"}})
	if b == nil || len(b.Members) != 1 {
		t.Fatalf("전제가 깨졌다 — 구성원 %v", memberIDs(b))
	}
	var detail string
	for _, r := range rej {
		if r.Item == "lonely" && r.Reason == RejectNotTop {
			detail = r.Detail
		}
	}
	if detail == "" {
		t.Fatalf("not-top 사유가 없다: %v", rej)
	}
	if !strings.Contains(detail, "묶음 2건") {
		t.Errorf("사유가 %q 다 — 선두를 포함한 '묶음 2건'이어야 한다", detail)
	}
}
