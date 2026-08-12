package judge

import (
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 굶김 축은 **선두만** 본다.
//
// 이 시험이 있는 이유: 앞선 판은 선두가 티클러면 StarveOldest 를 비웠다가,
// 구성원이 티클러가 아니면 거기서 **다시 채웠다.** 실측 사고에서 두 항목의
// created_at 이 글자까지 같아(일괄 반입분) 선두에만 티클러를 달았을 때 기아 값이
// 한 자리도 안 줄었고, 사용자 판정이 조용히 무효가 됐다.
//
// 판정의 근거는 같은 파일의 CloseDeclared 다 — "이 축은 '지금 새로 집어도 되나'에
// 답하고 그 질문의 주어는 브랜치를 받는 선두다". 굶김 축도 같은 질문에 답하는데
// 이것만 구성원을 봤다. 그 비대칭이 결함의 자리였다.

// starveCand 는 선행 하나로 이어지는 후보를 만든다.
//
// LinkOf 는 형제(판단) 또는 선행 축이 이어져야 묶는다 — 경로는 보강 전용이다.
// 빈 SiblingIndex 로도 묶이게 하려고 **같은 선행**을 준다(afterKey 일치).
func starveCand(id string, created time.Time, labels []string) Candidate {
	return Candidate{Item: model.Item{
		ID:        id,
		CreatedAt: created,
		Labels:    labels,
		After:     []model.After{{SHA: "cafe1234"}},
	}}
}

func TestStarveOldestIsEmptyWhenLeadIsTicklerEvenIfMemberIsNot(t *testing.T) {
	old := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	lead := starveCand("lead-tickler", old, []string{"tickler"})
	member := starveCand("member-plain", old, nil)

	b := bundleAround(lead, []Candidate{lead, member}, map[string]Candidate{}, SiblingIndex{})

	if len(b.Members) != 1 {
		t.Fatalf("구성원이 %d명이다 — 이 시험은 구성원 1명인 묶음을 봐야 한다(선행 축으로 이어진다)", len(b.Members))
	}
	if !b.StarveOldest.IsZero() {
		t.Errorf("선두가 티클러인데 StarveOldest 가 %s 다 — 구성원이 굶김 값을 다시 채웠다. "+
			"이러면 선두에 단 티클러가 무효가 되고, 그것이 이 시험을 만든 사고다", b.StarveOldest)
	}
}

func TestStarveOldestIsLeadAgeAndIgnoresOlderMember(t *testing.T) {
	older := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	lead := starveCand("lead-plain", newer, nil)
	member := starveCand("member-older", older, nil)

	b := bundleAround(lead, []Candidate{lead, member}, map[string]Candidate{}, SiblingIndex{})

	if !b.StarveOldest.Equal(newer) {
		t.Errorf("StarveOldest 가 %s 다 — 선두 나이(%s)여야 한다. "+
			"이것은 회귀가 아니라 판정이다: 오래된 구성원은 **자기가 선두인 묶음**에서 "+
			"제 나이로 기아 판정을 받는다(EligibleBundle 이 fit 전원을 각각 선두로 세운다)", b.StarveOldest, newer)
	}
	// Oldest 는 그대로 전체를 본다 — 굶김 축과 갈라 둔 값이라 함께 움직이면 안 된다.
	if !b.Oldest.Equal(older) {
		t.Errorf("Oldest 가 %s 다 — 전체 최고령(%s)이어야 한다. 이 축은 이번 변경 대상이 아니다", b.Oldest, older)
	}
}
