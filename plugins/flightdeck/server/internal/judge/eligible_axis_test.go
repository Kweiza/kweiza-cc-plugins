package judge

import (
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 리뷰가 찾은 틈: TestClaimedItemReportsClaimNotClosed 는 state=claimed 만 보고,
// TestSelfClaimIsItsOwnCode 는 state=open 만 본다. 그 사이가 이 조합이다.
func TestClosedItemWithLiveSelfClaimReportsClosed(t *testing.T) {
	for _, st := range []model.ItemState{model.ItemDone, model.ItemDropped} {
		picked, rej := Eligible(EligibleInput{
			Self: "S1",
			Candidates: []Candidate{{
				Item:      model.Item{Project: "p", ID: "x", State: st},
				ClaimedBy: "S1",
			}},
		})
		if picked != nil {
			t.Fatalf("state=%s 인데 추천됐다", st)
		}
		if len(rej) != 1 {
			t.Fatalf("state=%s: 사유 %d건 — 1건이어야 한다: %v", st, len(rej), rej)
		}
		if rej[0].Reason != RejectClosed {
			t.Errorf("state=%s: 사유가 %q 다 — %q 여야 한다. "+
				"'끝났다'가 '재개해라'로 뒤바뀌면 탈락 사유 분포가 거짓이 된다",
				st, rej[0].Reason, RejectClosed)
		}
	}
}

// 회귀 방지: claimed 상태는 여전히 선점 축이 맡아야 한다(종료 축이 가로채면 안 된다).
func TestClaimedStateStillReportsClaim(t *testing.T) {
	_, rej := Eligible(EligibleInput{
		Self: "S2",
		Candidates: []Candidate{{
			Item:      model.Item{Project: "p", ID: "x", State: model.ItemClaimed},
			ClaimedBy: "S1",
		}},
	})
	if len(rej) != 1 || rej[0].Reason != RejectClaimed {
		t.Errorf("claimed 항목의 사유가 %v 다 — %q 여야 한다", rej, RejectClaimed)
	}
}
