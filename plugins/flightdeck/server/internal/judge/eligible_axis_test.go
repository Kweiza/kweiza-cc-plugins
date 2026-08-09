package judge

import (
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 이 파일은 rejectionsFor 의 축 **사이**에 난 틈을 잠근다. 표 안이 아니라 표의 경계다.
//
// ★ 전부 추천 경로 전체(EligibleBundle)로 부른다 — 제품이 실제로 부르는 것이 이쪽이다.
// 앞선 판에는 껍데기 진입점(judge.Eligible)이 있었고 이 파일의 앞 두 시험이 그것을
// 불렀는데, 호출부 0으로 지워졌다(큐 항목 fd-eligible-dead-function-disposal).

// 리뷰가 찾은 틈: TestClaimedItemReportsClaimNotClosed 는 state=claimed 만 보고,
// TestSelfClaimIsItsOwnCode 는 state=open 만 본다. 그 사이가 이 조합이다.
func TestClosedItemWithLiveSelfClaimReportsClosed(t *testing.T) {
	for _, st := range []model.ItemState{model.ItemDone, model.ItemDropped} {
		picked, rej := EligibleBundle(EligibleInput{
			Self: "S1",
			Candidates: []Candidate{{
				Item:      model.Item{Project: "p", ID: "x", State: st},
				ClaimedBy: "S1",
			}},
		}, SiblingIndex{})
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
	_, rej := EligibleBundle(EligibleInput{
		Self: "S2",
		Candidates: []Candidate{{
			Item:      model.Item{Project: "p", ID: "x", State: model.ItemClaimed},
			ClaimedBy: "S1",
		}},
	}, SiblingIndex{})
	if len(rej) != 1 || rej[0].Reason != RejectClaimed {
		t.Errorf("claimed 항목의 사유가 %v 다 — %q 여야 한다", rej, RejectClaimed)
	}
}

// state=claimed 인데 **선점 행이 없다**. rejectionsFor 의 ③ 축이 맡는 자리다.
//
// ★ 무엇이 깨지나. ③ 을 통째로 지워도 전 스위트가 초록이었다(실측). 위 두 시험은
// ClaimedBy 가 **채워진** claimed 만 보고, 종료 축은 done·dropped 만 본다 — 그 사이가
// 정확히 이 조합이다. ③ 이 없으면 이 항목은 적격이 되어 **추천 선두로 나가고, 그대로
// 집힌다.** 그러면 보드에는 claimed 로 떠 있는 항목을 다른 세션이 새 일로 받아 들고,
// 원래 그것을 하던(그러나 선점 행이 사라진) 쪽과 같은 파일을 동시에 고친다.
// 이 판에는 선점 만료도 세션 종료 반납도 없어서 그 상태는 사람이 손대기 전까지 안 풀린다.
func TestClaimedStateWithoutAClaimRowIsNotRecommended(t *testing.T) {
	b, rej := EligibleBundle(EligibleInput{
		Self: "S1",
		Candidates: []Candidate{{
			Item: model.Item{Project: "p", ID: "x", State: model.ItemClaimed},
		}},
	}, SiblingIndex{})
	if b != nil {
		t.Fatalf("선점 행 없는 claimed 항목이 %q 로 추천됐다 — 보드가 claimed 라 말하는 것을 남이 새로 집는다", b.Lead.Item.ID)
	}
	if len(rej) != 1 || rej[0].Reason != RejectClosed {
		t.Fatalf("사유가 %v 다 — %q 한 줄이어야 한다", rej, RejectClosed)
	}
	if !strings.Contains(rej[0].Detail, string(model.ItemClaimed)) {
		t.Errorf("상세에 실제 상태가 없다: %q — 정합성 결함은 상태를 그대로 실어야 진단이 된다", rej[0].Detail)
	}
}

// 자원 색인에 **점유자가 빈 문자열**로 들어온 항목. "비어 있으면 아무도 안 쥠"이 계약이다
// (EligibleInput.HeldResources 주석).
//
// ★ 무엇이 깨지나. `holder == ""` 가드를 지워도 전 스위트가 초록이었다(실측).
// 지워진 채로 그 입력이 오면 항목은 resource-held 로 탈락하고, 사람이 보는 사유는
// "자원 db 를 세션  가 쥐고 있다" — **이름이 빈 자리**다. 누구에게 물어야 하는지가 없는
// 탈락이라 그 항목은 아무도 못 집고, 원장의 자원 축 분포도 있지도 않은 점유로 부푼다.
// 지금 유일한 호출부(service.heldResources)는 빈 값을 안 만들지만, 이 필드는 판정의
// **입력 계약**이고 계약은 호출부 하나가 지금 어떻게 짜여 있는지와 별개로 서 있어야 한다.
func TestEmptyResourceHolderDoesNotBlock(t *testing.T) {
	b, rej := EligibleBundle(EligibleInput{
		Self:          "S1",
		Candidates:    []Candidate{{Item: openItem("x", 0), Needs: []string{"db"}}},
		HeldResources: map[string]string{"db": ""},
	}, SiblingIndex{})
	if b == nil {
		t.Fatalf("점유자가 빈 자원에 막혔다 — 사유 %v", rej)
	}
	for _, r := range rej {
		if r.Reason == RejectResourceHeld {
			t.Fatalf("아무도 안 쥔 자원으로 탈락시켰다: %q", r.Detail)
		}
	}
}
