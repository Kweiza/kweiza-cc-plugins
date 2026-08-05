package service

import (
	"path/filepath"
	"testing"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
)

// 이 파일은 **판정을 먹이는 배선**을 본다. 판정 자체는 judge 가, 화면은 mcpsrv 가 본다.
//
// ★ 왜 따로 두나. judge 시험은 EligibleInput 을 손으로 조립하고, render 시험은
// PickResult 를 손으로 조립한다. 둘 다 자기 계층에서는 정확한데, **service 가 그
// 구조체를 실제로 무엇으로 채우는지는 어느 쪽도 원리적으로 못 잰다.** 이 저장소가
// 이미 한 번 그 대가를 치렀다 — 기능이 열 태스크 동안 배포 경로에서 아무것도 안 하고
// 있었는데 모든 시험이 초록이었다. 아래 넷은 전부 그 부류이고, 전 스위트를 통과하는
// 변이로 하나씩 실증한 뒤 못박았다.

// ① pickRecommend 가 EligibleInput.SelfCC 를 실제로 채운다.
//
// ★ 이것이 셋 중 가장 흔한 경로다 — 인자 없는 `fd pick` 이 여기로 온다. 선점 경로 둘은
// 이미 물려 있었는데 이 배선만 비어 있었다(그 자리를 "" 로 박아도 전 스위트가 초록이다).
// 비면 같은 대화의 형제 카드가 남으로 보고되고, 세션은 자기 자신과 조율하라는 화면을 받는다.
func TestPickRecommendDoesNotReportSiblingCardAsOverlap(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "내 카드")
	sibling := openSession(t, s, "p", repo, repo, "cc-1", "같은 대화의 다른 카드")
	other := openSession(t, s, "p", repo, repo, "cc-2", "진짜 남")

	for _, id := range []string{sibling.Session.ID, other.Session.ID} {
		if err := s.Beat(ctx(), id, model.SignalTool,
			[]string{filepath.Join(repo, "services", "solo.go")}); err != nil {
			t.Fatalf("비트 실패: %v", err)
		}
	}
	addItem(t, s, "p", "solo", []string{"services/solo.go"}, nil)

	// 인자 없는 추천이다 — 선점 경로가 아니다.
	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("pick 실패: %v", err)
	}
	if res.Mode != PickRecommended {
		t.Fatalf("사전 조건이 깨졌다 — 추천 경로여야 한다: mode=%q", res.Mode)
	}
	for _, ov := range res.Overlaps {
		if ov.SessionID == sibling.Session.ID {
			t.Fatalf("형제 카드가 겹침으로 나왔다 — 자기 자신과 조율하라는 화면이다: %+v", res.Overlaps)
		}
	}
	if len(res.Overlaps) != 1 || res.Overlaps[0].SessionID != other.Session.ID {
		t.Fatalf("진짜 남과의 겹침이 사라졌다 — 형제를 빼면서 축을 통째로 껐다: %+v", res.Overlaps)
	}
}

// ② 자기 카드를 **id 로** 건너뛰는 규칙은 여기 없다 — judge 계층이 못박는다
// (TestEligibleBundleOverlapsSkipOwnCardEvenWithoutCC).
//
// ★ 여기 안 쓴 이유를 남긴다. 그 인자(in.SessionID)를 "" 로 바꿔도 전 스위트가 초록이라
// 공백처럼 보이지만, service 계층에서는 **도달할 수 없는 상태**다. selfCC 와 live 가
// 같은 cards 슬라이스에서 나오기 때문이다(selfCCOf · liveFor, board.go): 내 카드가
// 살아 있으면 selfCC 는 반드시 내 cc 이고, 그러면 형제 규칙 하나로 이미 걸러진다.
// 내 카드가 살아 있지 않으면 애초에 live 에 없어 거를 것이 없다.
//
// cc 가 빈 카드를 만들어 그 상태를 재현할 수는 있지만, 그 카드는 세 계층이 거부한다
// (judge 판정 · OpenSession · store). 저장 계층으로 우회해 넣으면 **시스템이 만들기를
// 거부하는 상태**를 시험이 사실인 양 고정하게 된다. 규칙 자체는 judge 좌표계에 있고
// 거기서 물려 있으니, service 의 그 인자는 도달 불가능한 이중 안전망으로 남긴다.

// ③ 묶음 겹침의 경로 합집합에 **선두 경로가 들어간다.**
//
// ★ 기존 시험 둘(…OverlapsCoverWholeBundle · …DoesNotReportSibling…)은 남이 **구성원**
// 경로를 만지는 모양이라, 합집합의 초기값에서 선두 경로를 빼도 둘 다 초록이다.
// 시험이 둘인데 잡는 것은 한 방향뿐이었다. 여기서는 남이 **선두 경로만** 만진다.
func TestPickBundleOverlapsCoverLeadPathToo(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	other := openSession(t, s, "p", repo, repo, "cc-2", "남")

	if err := s.Beat(ctx(), other.Session.ID, model.SignalTool,
		[]string{filepath.Join(repo, "services", "lead.go")}); err != nil {
		t.Fatalf("비트 실패: %v", err)
	}
	addItem(t, s, "p", "lead", []string{"services/lead.go"}, nil)
	addItem(t, s, "p", "mem", []string{"services/mem.go"}, nil)

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID,
		ItemIDs: []string{"lead", "mem"}})
	if err != nil {
		t.Fatalf("pick 실패: %v", err)
	}
	if len(res.Overlaps) != 1 || res.Overlaps[0].SessionID != other.Session.ID {
		t.Fatalf("선두 경로의 겹침이 묶음 응답에 안 실렸다 — 합집합이 구성원만 봤다: %+v", res.Overlaps)
	}
}

// ④ rejectionOf 의 **closed 가지**가 자기 사유 코드를 낸다.
//
// ★ 이 함수의 세 가지 중 not-found·claimed 는 물려 있었고 closed 만 비어 있었다
// (그 자리를 안전망 claim-failed 로 바꿔도 전 스위트가 초록이다). 사유 코드는
// pick_eval 분포로 질의되는 값이라, 접히면 "끝난 항목을 구성원으로 지정했다"가
// "알 수 없는 이유로 못 집었다"에 섞여 영영 안 보인다.
func TestPickBundleClosedMemberIsRejectedWithTheClosedCode(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	addItem(t, s, "p", "lead", []string{"services/lead.go"}, nil)
	addItem(t, s, "p", "gone", []string{"services/gone.go"}, nil)
	if err := st.SetItemState(ctx(), "p", "gone", model.ItemDropped, "중복이라 버린다"); err != nil {
		t.Fatalf("항목 폐기 실패: %v", err)
	}

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID,
		ItemIDs: []string{"lead", "gone"}})
	if err != nil {
		t.Fatalf("선두가 성립해야 한다: %v", err)
	}
	if len(res.Bundle.Members) != 1 {
		t.Fatalf("구성원이 한 줄로 남아야 한다: %+v", res.Bundle.Members)
	}
	m := res.Bundle.Members[0]
	if m.Claimed {
		t.Fatal("폐기된 항목이 집혔다")
	}
	if m.Rejection == nil {
		t.Fatal("못 집은 구성원에 사유가 없다 — 원장에서 사라진다")
	}
	// 안전망 코드로 접히면 안 된다. 실제 값으로 단정한다.
	if m.Rejection.Reason != judge.RejectClosed {
		t.Fatalf("사유 코드가 %q 다 — %q 여야 한다(안전망 %q 로 접혔다)",
			m.Rejection.Reason, judge.RejectClosed, RejectClaimFailed)
	}
}
