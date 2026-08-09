package service

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
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

// ⑤ 묶음 사유의 **쥔 건수**는 실제로 쥔 수다 — 요청한 수가 아니다.
//
// ★ `held := 1 + heldMembers` 를 `1 + len(rest)` 로 바꿔도 전 스위트가 초록이었다.
// 그러면 구성원이 하나라도 실패했을 때 응답이 **쥐지 않은 건수를 쥐었다고 말한다** —
// 3건 묶음에서 둘 다 실패해도 "묶음 3건 중 3건을 이 세션이 쥐고 있고"가 나온다.
// 이 파일이 held 와 newly 를 애초에 가른 이유가 그 문장 하나 때문이었는데(★ "응답이
// 일어나지 않은 쓰기를 보고한다"), 정작 held 쪽이 안 물려 있었다. 동시 세션이 서른인
// 판에서 "쥐었다고 믿는데 안 쥔" 항목은 두 세션이 같은 파일을 동시에 고치는 사고가 된다.
func TestPickBundleReasonCountsWhatItActuallyHolds(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	other := openSession(t, s, "p", repo, repo, "cc-2", "남")

	addItem(t, s, "p", "lead", []string{"services/lead.go"}, nil)
	addItem(t, s, "p", "held-by-other", []string{"services/b.go"}, nil)
	addItem(t, s, "p", "gone", []string{"services/c.go"}, nil)
	if _, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: other.Session.ID,
		ItemID: "held-by-other"}); err != nil {
		t.Fatalf("남의 선점 준비 실패: %v", err)
	}
	if err := st.SetItemState(ctx(), "p", "gone", model.ItemDropped, "버린다"); err != nil {
		t.Fatalf("항목 폐기 실패: %v", err)
	}

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID,
		ItemIDs: []string{"lead", "held-by-other", "gone"}})
	if err != nil {
		t.Fatalf("선두가 성립해야 한다: %v", err)
	}

	// 응답이 스스로 말하는 것에서 기대값을 뽑는다 — 상수를 적으면 시험이 응답과
	// 따로 놀고, 그러면 둘이 갈라져도 초록이다.
	held := 1 // 선두
	for _, m := range res.Bundle.Members {
		if m.Claimed {
			held++
		}
	}
	if held != 1 {
		t.Fatalf("사전 조건이 깨졌다 — 구성원 둘 다 실패해야 한다: %+v", res.Bundle.Members)
	}
	want := fmt.Sprintf("묶음 %d건 중 %d건", 3, held)
	if !strings.Contains(res.Reason, want) {
		t.Fatalf("사유가 실제 쥔 건수를 안 말한다 — %q 가 없다: %q", want, res.Reason)
	}
}

// ⑥ 묶음 **구성원의 경로 실재 판정**이 응답에 실린다(item_ids 로 집는 경로).
//
// ★ `m.Notes, m.PathCheck = sub.Notes, sub.PathCheck` 에서 PathCheck 를 빼도 전 스위트가
// 초록이었다. 그러면 화면이 구성원마다 "이 응답은 그 축을 읽지 않았다"를 내고 —
// 서버는 읽을 수 있는데 못 읽었다고 고백한다 — 오등록 구성원의 `fd move <id> --project X`
// 줄이 통째로 사라진다. 그 줄은 경로가 남의 프로젝트에 있는 항목을 사람이 알 **유일한**
// 통로다. 추천 경로(pickRecommend)는 자기 자리에서 따로 채우므로 물려 있었고,
// 선점 경로만 비어 있었다 — 계층이 아니라 **갈래** 사이의 빈 칸이다.
func TestPickBundleMemberCarriesItsPathVerdictOnTheClaimPath(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	// 선두는 실재하는 경로(newRepo 가 README.md 를 만든다), 구성원은 어디에도 없는 경로.
	addItem(t, s, "p", "lead", []string{"README.md"}, nil)
	addItem(t, s, "p", "mem", []string{"nowhere/at/all.go"}, nil)

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID,
		ItemIDs: []string{"lead", "mem"}})
	if err != nil {
		t.Fatalf("pick 실패: %v", err)
	}
	if len(res.Bundle.Members) != 1 || !res.Bundle.Members[0].Claimed {
		t.Fatalf("사전 조건이 깨졌다 — 구성원이 집혔어야 한다: %+v", res.Bundle.Members)
	}
	if res.Bundle.Members[0].PathCheck == nil {
		t.Fatal("구성원의 경로 실재 판정이 안 실렸다 — 화면이 '축을 안 읽었다'로 고백하게 된다")
	}
	// 선두 것을 복사한 게 아니라 자기 것이어야 한다.
	if res.PathCheck != nil && res.Bundle.Members[0].PathCheck.Summary == res.PathCheck.Summary {
		t.Fatalf("구성원이 선두의 판정을 받았다: 선두=%q 구성원=%q",
			res.PathCheck.Summary, res.Bundle.Members[0].PathCheck.Summary)
	}
}

// seedCloseDeclaration 은 롤백된 종료 선언 하나를 원장에 **손으로** 심는다.
//
// ★ 왜 손으로 심나. 실물 원장에는 지금 `open`+`item.finish` 조합이 0건이다 — 두 번째
// finish 가 성공하면 항목이 done 이 되어 후보에서 아예 빠지기 때문이다. 실물 경로로는
// 이 상태를 못 밟는다. 그리고 앵커(항목 생성 **이전**의 이벤트)를 밟으려면 at 을 우리가
// 골라야 하는데 store.LogEvent 는 언제나 time.Now() 를 찍는다.
//
// 표기는 store 의 timeLayout 과 같아야 한다 — 폭 고정이라야 사전순 정렬이 시간순과
// 일치한다(그 상수는 store 안에 있어 여기서는 같은 문자열을 적는다).
func seedCloseDeclaration(t *testing.T, st *store.Store, project, sessionID, itemID, mode string, at time.Time) {
	t.Helper()
	payload := fmt.Sprintf(`{"item":%q,"mode":%q,"bytes":10300,"count":0}`, itemID, mode)
	if _, err := st.DB().ExecContext(ctx(),
		`INSERT INTO event(at, project, session_id, kind, payload) VALUES (?, ?, ?, 'item.finish', ?)`,
		at.UTC().Format("2006-01-02T15:04:05.000000Z"), project, sessionID, payload); err != nil {
		t.Fatalf("종료 선언 심기 실패(item=%s mode=%s): %v", itemID, mode, err)
	}
}

// hideEvent 는 원장 표를 **이름만 숨긴다**(hideJudgmentLink 과 같은 방식).
// 지우면 추가 전용 트리거까지 함께 흔들려 무엇이 실패했는지가 흐려진다.
func hideEvent(t *testing.T, st *store.Store) {
	t.Helper()
	if _, err := st.DB().ExecContext(ctx(),
		`ALTER TABLE event RENAME TO event_hidden`); err != nil {
		t.Fatalf("event 숨기기 실패: %v", err)
	}
}

// ⑦ 원장이 낸 수를 **그대로 안 믿는다** — 앵커와 존재 판정이 service 에서 걸린다.
//
// ★ 이 둘이 없으면 무엇이 깨지나. item 의 PK 가 (project, id) 라 지워졌다 다시 만들어진
// id 가 옛 화신의 선언을 물려받고, 실측 3건은 finish 를 친 프로젝트와 항목이 사는
// 프로젝트가 갈린 **좌표 오류**다 — 그것을 표류로 세면 이 축이 애먼 항목을 강등한다.
// 두 관문 다 store 가 아니라 여기 있다: candidates() 가 이미 items 를 손에 쥐고 있고,
// SQL 조인으로 하려면 json_extract 를 조인 조건에 넣어야 하는데 그 선례가 0건이다.
func TestCloseDeclarationsAnchorsOnCreationAndDropsNonCandidates(t *testing.T) {
	cases := []struct {
		name    string
		item    string // 빈 문자열이면 이 항목 자신
		offset  time.Duration
		wantHit bool
	}{
		{"생성 이후의 선언은 센다", "", time.Minute, true},
		{"생성 이전의 선언은 옛 화신의 것이라 안 센다", "", -time.Hour, false},
		{"생성과 같은 시각은 안 센다 — 애매한 쪽은 하한으로 접는다", "", 0, false},
		{"후보에 없는 id 의 선언은 좌표 어긋남이라 버린다", "ghost-item", time.Minute, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, st := newSvc(t)
			repo := newRepo(t)
			me := openSession(t, s, "p", repo, repo, "cc-1", "나")
			it := addItem(t, s, "p", "anchored", []string{"services/a.go"}, nil)

			id := c.item
			if id == "" {
				id = it.ID
			}
			seedCloseDeclaration(t, st, "p", me.Session.ID, id, "done", it.CreatedAt.Add(c.offset))

			got, read := s.closeDeclarations(ctx(), "p", []judge.Candidate{{Item: it}})
			if !read {
				t.Fatalf("원장을 읽을 수 있는데 못 읽었다고 한다: %+v", got)
			}
			d, hit := got[it.ID]
			if hit != c.wantHit {
				t.Fatalf("이 항목의 선언 유무가 %v 다(기대 %v) — 맵: %+v", hit, c.wantHit, got)
			}
			if c.wantHit && d.Count() != 1 {
				t.Fatalf("선언 수가 %d 다(기대 1): %+v", d.Count(), d)
			}
			if _, ghost := got["ghost-item"]; ghost {
				t.Fatalf("후보에 없는 id 가 맵에 남았다 — 좌표 오류를 표류로 셌다: %+v", got)
			}
		})
	}
}

// ⑧ pickRecommend 가 EligibleInput 에 종료 선언 맵을 **실제로** 싣는다.
//
// ★ judge 시험은 EligibleInput 을 손으로 조립하므로 이 배선을 원리적으로 못 잰다.
// 그리고 이 축은 관측 가능한 출력이 순위 하나뿐이다 — 그래서 나이 축이 반대편을
// 가리키도록 깔았다: 선언된 쪽을 **먼저** 만들어(최고령) 축이 안 물리면 그쪽이 이긴다.
// 실측 기준선이 정확히 그 값이다(선두 a-rolled-back).
func TestPickRecommendDemotesTheItemWhoseCloseWasRolledBack(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	older := addItem(t, s, "p", "a-rolled-back", []string{"services/a.go"}, nil)
	addItem(t, s, "p", "b-clean", []string{"services/b.go"}, nil)
	seedCloseDeclaration(t, st, "p", me.Session.ID, older.ID, "done", older.CreatedAt.Add(time.Minute))

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("pick 실패: %v", err)
	}
	if res.Mode != PickRecommended || res.Item == nil {
		t.Fatalf("사전 조건이 깨졌다 — 추천 경로여야 한다: mode=%q item=%+v", res.Mode, res.Item)
	}
	if res.Item.ID != "b-clean" {
		t.Fatalf("닫히려다 롤백된 항목이 여전히 1순위다 — service 가 이 축을 judge 에 안 먹였다: 선두=%q", res.Item.ID)
	}

	// 상시 점등이면 판별력이 0이다. 반대 방향을 짝으로 못박는다.
	t.Run("선언이 없으면 나이순 그대로다", func(t *testing.T) {
		s, _ := newSvc(t)
		repo := newRepo(t)
		me := openSession(t, s, "p", repo, repo, "cc-1", "나")
		addItem(t, s, "p", "a-rolled-back", []string{"services/a.go"}, nil)
		addItem(t, s, "p", "b-clean", []string{"services/b.go"}, nil)

		res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
		if err != nil {
			t.Fatalf("pick 실패: %v", err)
		}
		if res.Item.ID != "a-rolled-back" {
			t.Fatalf("선언이 하나도 없는데 순서가 뒤집혔다 — 이 축이 상시 점등이면 판별력이 0이다: 선두=%q", res.Item.ID)
		}
	})
}

// ⑨ 원장을 못 읽으면 **그 사실이 Scope 에 남고**, derive 에는 안 들어간다.
//
// ★ derive 에 넣으면 무엇이 깨지나: FreshnessOf 가 failures>0 을 **git 축** Stale 로
// 접기 때문에, 원장 카운트 한 번이 실패했을 뿐인데 세션이 브랜치·HEAD·조상 판정이
// 낡았다고 읽는다. pick.go 의 siblingIndex 가 같은 판단을 이미 내려 뒀다.
//
// ★ 반대로 침묵도 안 된다. 안 남기면 이 순위가 "롤백된 항목이 진짜로 없다"인지
// "그 축을 아예 못 봤다"인지 응답만으로 못 가른다.
func TestPickRecommendConfessesUnreadCloseAxisWithoutFoldingItIntoDerive(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	addItem(t, s, "p", "solo", []string{"services/a.go"}, nil)

	hideEvent(t, st)

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("원장을 못 읽는다고 추천을 통째로 버렸다: %v", err)
	}
	if res.Mode != PickRecommended || res.Item == nil || res.Item.ID != "solo" {
		t.Fatalf("추천이 안 실렸다 — mode=%q item=%+v", res.Mode, res.Item)
	}
	if res.Bundle == nil {
		t.Fatal("묶음 축이 nil 이다")
	}
	if !strings.Contains(res.Bundle.Scope, "item.finish") {
		t.Fatalf("종료 선언 축을 못 읽었다는 고백이 Scope 에 없다: %q", res.Bundle.Scope)
	}
	if len(res.Failures) != 0 {
		t.Fatalf("종료 선언 축의 실패를 derive 에 실었다 — FreshnessOf 가 git 축을 낡음으로 접는다: %+v", res.Failures)
	}
}

// ⑩ **선두**가 자기 종료 선언을 싣는다 — 이 사고의 항목이 정확히 선두였다.
//
// ★ renderBundle 은 BundleInfo 하나만 받고 Members 는 정의상 선두 제외라 선두를
// 모른다. 구성원 자리에만 심으면 사고를 낳은 그 항목에 대해 응답이 침묵한다.
//
// ★ 세 상태를 다 잰다. nil 의 뜻이 "안 읽었다" 하나로 서려면 "읽었고 0건"이 반드시
// non-nil 이어야 하고, 그 짝이 없으면 원장을 못 읽은 응답이 관측 없이 "이 항목은
// 깨끗하다"를 단정하게 된다(checkItemPaths 가 절대 nil 을 안 내는 것과 같은 자리).
func TestPickResultCarriesCloseDeclarationForTheLead(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	it := addItem(t, s, "p", "solo", []string{"services/a.go"}, nil)
	seedCloseDeclaration(t, st, "p", me.Session.ID, it.ID, "done", it.CreatedAt.Add(time.Minute))

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("pick 실패: %v", err)
	}
	if res.CloseDeclared == nil {
		t.Fatal("선두의 종료 선언이 안 실렸다 — 사고를 낳은 그 항목이 정확히 선두다")
	}
	if res.CloseDeclared.Count() != 1 || res.CloseDeclared.Done != 1 {
		t.Fatalf("선두의 선언 수가 틀렸다: %+v", res.CloseDeclared)
	}
	if res.CloseDeclared.LastMode != "done" || res.CloseDeclared.LastSession != me.Session.ID {
		t.Fatalf("마지막 선언의 좌표(세션·mode)가 안 실렸다: %+v", res.CloseDeclared)
	}

	t.Run("선언이 없어도 읽었으면 non-nil 이다", func(t *testing.T) {
		s, _ := newSvc(t)
		repo := newRepo(t)
		me := openSession(t, s, "p", repo, repo, "cc-1", "나")
		addItem(t, s, "p", "solo", []string{"services/a.go"}, nil)

		res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
		if err != nil {
			t.Fatalf("pick 실패: %v", err)
		}
		if res.CloseDeclared == nil {
			t.Fatal("읽었는데 nil 이다 — nil 은 '이 축을 안 읽었다'라서 0건과 접히면 안 된다")
		}
		if res.CloseDeclared.Count() != 0 {
			t.Fatalf("선언이 없는데 수가 %d 다: %+v", res.CloseDeclared.Count(), res.CloseDeclared)
		}
	})

	t.Run("축을 못 읽었으면 nil 이다", func(t *testing.T) {
		s, st := newSvc(t)
		repo := newRepo(t)
		me := openSession(t, s, "p", repo, repo, "cc-1", "나")
		addItem(t, s, "p", "solo", []string{"services/a.go"}, nil)
		hideEvent(t, st)

		res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
		if err != nil {
			t.Fatalf("pick 실패: %v", err)
		}
		if res.CloseDeclared != nil {
			t.Fatalf("못 읽었는데 값을 실었다 — 관측한 적 없는 사실을 단정한다: %+v", res.CloseDeclared)
		}
	})
}

// ⑪ **구성원**이 자기 종료 선언을 싣는다. 선두 것을 빌려주면 안 된다.
//
// ★ 값을 서로 다르게 깐다(구성원=dropped 1건, 선두=0건). 같은 값으로 깔면 선두 것을
// 그대로 복사하는 변이가 초록으로 지나간다 — 구성원 PathCheck 이 같은 함정을 이미
// 밟았고(TestPickBundleMemberPathCheckIsPerItemNotLead) 같은 방식으로 막는다.
func TestPickBundleMemberCarriesItsOwnCloseDeclaration(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	declared := addItem(t, s, "p", "a1-declared", []string{"services/a.go"}, nil)
	addItem(t, s, "p", "z9-clean", []string{"services/z.go"}, nil)
	makeSiblings(t, st, "p", "a1-declared", "z9-clean")
	seedCloseDeclaration(t, st, "p", me.Session.ID, declared.ID, "dropped", declared.CreatedAt.Add(time.Minute))

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("pick 실패: %v", err)
	}
	if res.Bundle == nil || len(res.Bundle.Members) != 1 {
		t.Fatalf("사전 조건이 깨졌다 — 형제 하나가 구성원이어야 한다: %+v", res.Bundle)
	}
	if res.Item.ID != "z9-clean" || res.Bundle.Members[0].Item.ID != "a1-declared" {
		t.Fatalf("선언된 쪽이 여전히 선두다 — 선두=%q 구성원=%q",
			res.Item.ID, res.Bundle.Members[0].Item.ID)
	}
	m := res.Bundle.Members[0]
	if m.CloseDeclared == nil {
		t.Fatal("구성원의 종료 선언이 안 실렸다 — 화면이 그 항목에 대해 침묵한다")
	}
	if m.CloseDeclared.Count() != 1 || m.CloseDeclared.Dropped != 1 || m.CloseDeclared.LastMode != "dropped" {
		t.Fatalf("구성원의 선언이 자기 것이 아니다: %+v", m.CloseDeclared)
	}
	if res.CloseDeclared == nil || res.CloseDeclared.Count() != 0 {
		t.Fatalf("선두가 구성원의 선언을 받아 갔다: %+v", res.CloseDeclared)
	}
}
