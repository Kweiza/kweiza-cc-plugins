package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// 처방 이벤트가 **판정을 가른 축**을 남기는가.
//
// ★ 왜 필요한가. payload 가 `key`·`reason` 만 실으면 "이 발화가 왜 떴나"에 원장이 답을 못 한다.
// 2026-08-11 에 실물로 대가를 치렀다: unclaimed 118건의 성격을 가르려고
// `item.claim`·`item.finish`·`claim.reclaim` 을 시간순으로 재생하는 일회용 스크립트를 짰고,
// 그것이 그 조사의 대부분이었다.
//
// ★ **이것이 혼자서는 거짓 양성을 못 센다.** 축은 "판정이 본 것"이고 거짓 양성은 판정이
// **못 본 것** 때문에 난다. 세계의 진실은 store.ClaimHolderAt 이 낸다 — 둘을 나란히 놓아야
// "판정은 비었는데 세상엔 점유자가 있었다"가 세어진다. 그 짝을 갈라 두는 것이 맞다:
// 이쪽은 발화 시점에 **쓰는** 축이고 저쪽은 나중에 **읽는** 축이다.

// prescribeAxes 는 시험이 읽는 payload 모양이다. **생산 코드의 타입을 안 쓴다** —
// 쓰면 필드 이름을 바꿔도 이 시험이 같이 따라가서 원장 계약이 안 잠긴다.
type prescribeAxes struct {
	Key             string   `json:"key"`
	Reason          string   `json:"reason"`
	Claims          []string `json:"claims"`
	SiblingClaims   []string `json:"sibling_claims"`
	WorkspaceClaims []string `json:"workspace_claims"`
	Closed          []string `json:"closed"`
}

func readPrescribeAxes(t *testing.T, st *store.Store, sessionID, key string) prescribeAxes {
	t.Helper()
	evs, err := st.ListSessionEvents(ctx(), sessionID, "prescribe", time.Time{})
	if err != nil {
		t.Fatalf("이벤트 조회 실패: %v", err)
	}
	var keys []string
	for _, e := range evs {
		var a prescribeAxes
		if err := json.Unmarshal([]byte(e.Payload), &a); err != nil {
			t.Fatalf("payload 해석 실패(%s): %v", e.Payload, err)
		}
		keys = append(keys, a.Key)
		if a.Key == key {
			return a
		}
	}
	t.Fatalf("키 %q 인 처방 이벤트가 없다 — 기록된 키: %v", key, keys)
	return prescribeAxes{}
}

func sameIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestPrescribeEventRecordsTheClaimsItSaw — 선점을 쥔 채 뜬 발화는 그 선점을 남긴다.
func TestPrescribeEventRecordsTheClaimsItSaw(t *testing.T) {
	svc, st := newSvc(t)
	sess := openSessionForPrescribeTest(t, svc)
	claimItemForPrescribeTest(t, svc, st, sess, "batch7", []string{"a.go"})
	touchPathForPrescribeTest(t, st, sess, "b.go") // 선언 밖 — outside:b.go 가 뜬다

	if _, err := svc.Prescriptions(ctx(), sess); err != nil {
		t.Fatalf("처방 실패: %v", err)
	}

	a := readPrescribeAxes(t, st, sess, "outside:b.go")
	if !sameIDs(a.Claims, []string{"batch7"}) {
		t.Fatalf("선점 축이 원장에 안 남았다: claims=%v (기대 [batch7])\n"+
			"이 값이 없으면 \"무엇을 쥔 채 뜬 발화인가\"에 원장이 답을 못 한다", a.Claims)
	}
}

// TestPrescribeEventRecordsTheClosedItemsThatGroundedTheVerdict —
// **근거 있는** unclaimed 은 그 근거(끝낸 항목)를 남긴다.
//
// grounded 갈래와 근거 없는 갈래는 화면 문구로만 갈렸다. 원장에서는 둘 다 `key=unclaimed` 라
// 사후에 못 가른다 — 그 둘의 성격이 완전히 다른데도.
func TestPrescribeEventRecordsTheClosedItemsThatGroundedTheVerdict(t *testing.T) {
	svc, st := newSvc(t)
	sess := openSessionForPrescribeTest(t, svc)
	claimItemForPrescribeTest(t, svc, st, sess, "fd-x", []string{"cmd/fd"})
	if _, err := svc.Finish(ctx(), FinishInput{
		Project: "p", SessionID: sess, ItemID: "fd-x", Outcome: model.ItemDone,
		Title: "끝냈다", Body: "무엇을 정했고 무엇을 기각했나",
	}); err != nil {
		t.Fatalf("finish 실패: %v", err)
	}
	touchPathForPrescribeTest(t, st, sess, "internal/store/item.go") // 선언 밖 — 안 덮인다

	if _, err := svc.Prescriptions(ctx(), sess); err != nil {
		t.Fatalf("처방 실패: %v", err)
	}

	a := readPrescribeAxes(t, st, sess, "unclaimed")
	if !sameIDs(a.Closed, []string{"fd-x"}) {
		t.Fatalf("근거 축이 원장에 안 남았다: closed=%v (기대 [fd-x])\n"+
			"이 값이 없으면 \"방금 끝낸 사람에게 뜬 발화\"와 \"한 번도 안 집은 사람에게 뜬 발화\"가 "+
			"원장에서 글자 그대로 같아진다", a.Closed)
	}
	if len(a.Claims) != 0 {
		t.Fatalf("반납했는데 선점 축이 남아 있다: claims=%v", a.Claims)
	}
}

// TestPrescribeEventRecordsSiblingAndWorkspaceAxesApart —
// 형제 축과 워크트리 축은 **서로 다른 자리**에 남아야 한다.
//
// ★ 값을 일부러 다르게 준다(형제는 fd-y, 워크트리는 fd-x). 같은 값이면 두 필드를 뒤바꾼
// 수정이 초록불로 지나간다 — 그리고 그 둘은 원인이 다른 축이라 뒤바뀌면 진단이 뒤집힌다.
func TestPrescribeEventRecordsSiblingAndWorkspaceAxesApart(t *testing.T) {
	svc, st := newSvc(t)
	repo, card := openConventionWorktreeCard(t, svc, "fd-x", "cc-워크트리")

	// 워크트리 축: **다른 대화**가 이 워크트리의 항목을 쥔다.
	holder := openSession(t, svc, "p", repo, repo, "cc-주트리", "주 트리 대화").Session.ID
	claimItemForPrescribeTest(t, svc, st, holder, "fd-x", []string{"cmd/fd"})
	// 형제 축: **같은 대화**의 다른 카드가 다른 항목을 쥔다.
	sibling := openSession(t, svc, "p", repo, repo, "cc-워크트리", "형제 카드").Session.ID
	claimItemForPrescribeTest(t, svc, st, sibling, "fd-y", []string{"internal"})

	// 발화를 하나 만든다 — 남의 대화와 경로가 겹치게 한다.
	stranger := openSession(t, svc, "p", repo, repo, "cc-남", "남의 대화").Session.ID
	touchPathForPrescribeTest(t, st, stranger, "cmd/fd/hook.go")
	touchPathForPrescribeTest(t, st, card, "cmd/fd/hook.go")

	if _, err := svc.Prescriptions(ctx(), card); err != nil {
		t.Fatalf("처방 실패: %v", err)
	}

	a := readPrescribeAxes(t, st, card, "overlap:"+stranger)
	if !sameIDs(a.WorkspaceClaims, []string{"fd-x"}) {
		t.Fatalf("워크트리 축이 원장에 안 남았다: workspace_claims=%v (기대 [fd-x])", a.WorkspaceClaims)
	}
	if !sameIDs(a.SiblingClaims, []string{"fd-y"}) {
		t.Fatalf("형제 축이 원장에 안 남았다: sibling_claims=%v (기대 [fd-y])", a.SiblingClaims)
	}
}
