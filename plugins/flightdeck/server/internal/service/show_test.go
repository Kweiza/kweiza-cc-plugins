package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// 이 파일이 재는 것 — **닫힌 항목의 판단에 닿는가.**
//
// 실측(운영 원장 2026-09-17): 닫힌 항목에 걸린 판단 1,985건 · 열린 항목은 125건.
// 그 1,985건을 읽는 경로가 `pick` 하나였고 pick 은 `state='open'` 만 준다.
// 그래서 이 표면이 열렸고, 아래 첫 시험이 그 이유 자체다.

// showFixture 는 세션 하나·항목 하나가 있는 서비스를 연다.
func showFixture(t *testing.T) (*Service, *store.Store, string) {
	t.Helper()
	s, st := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-show", "나")
	addItem(t, s, "p", "it1", []string{"internal/"}, nil)
	return s, st, me.Session.ID
}

// noteOn 은 항목에 판단 하나를 건다.
func noteOn(t *testing.T, s *Service, sess, item, body string) model.Judgment {
	t.Helper()
	res, err := s.Note(ctx(), NoteInput{
		Project: "p", SessionID: sess, Kind: model.JudgmentDecision,
		Title: "제목 " + body, Body: body, ItemID: item,
	})
	if err != nil {
		t.Fatalf("판단 저장 실패: %v", err)
	}
	return res.Judgment
}

// TestShowReachesJudgmentsOnAClosedItem 은 **이 표면의 존재 이유**다.
//
// 항목을 닫은 뒤에도 걸린 판단 전문이 나와야 한다. 안 나오면 pick 과 같은 것이 하나
// 더 생긴 것뿐이고, 도달 불가하던 1,985건은 그대로 도달 불가다.
func TestShowReachesJudgmentsOnAClosedItem(t *testing.T) {
	s, st, sess := showFixture(t)
	noteOn(t, s, sess, "it1", "닫히기 전에 남긴 판단")

	// ★ 닫는다. 그리고 **닫힌 뒤에도** 판단을 하나 더 얹는다 — 실측에서 40건이
	//   그 자리에 있었고 그중 ask 가 5건이다(누군가 답을 기다리는 것이다).
	if err := st.SetItemState(ctx(), "p", "it1", model.ItemDone, ""); err != nil {
		t.Fatalf("항목 닫기 실패: %v", err)
	}
	noteOn(t, s, sess, "it1", "닫힌 뒤에 얹은 판단")

	res, err := s.ShowItem(ctx(), ShowInput{Project: "p", SessionID: sess, ItemID: "it1"})
	if err != nil {
		t.Fatalf("닫힌 항목을 못 읽었다 — 이 동사가 존재하는 이유가 그것이다: %v", err)
	}
	if res.Item.State != model.ItemDone {
		t.Fatalf("항목 상태가 %q다 — done 이어야 한다", res.Item.State)
	}
	if len(res.Judgments) != 2 {
		t.Fatalf("판단이 %d건이다 — 2건(닫히기 전·후)이어야 한다: %+v", len(res.Judgments), res.Judgments)
	}
	// 전문이어야 한다. 제목만 내면 되짚는 사람이 다시 원장을 뒤진다.
	var joined string
	for _, j := range res.Judgments {
		joined += j.Body + "\n"
	}
	for _, want := range []string{"닫히기 전에 남긴 판단", "닫힌 뒤에 얹은 판단"} {
		if !strings.Contains(joined, want) {
			t.Errorf("판단 본문에 %q 가 없다 — 전문이 아니다:\n%s", want, joined)
		}
	}
}

// TestShowSeparatesZeroJudgmentsFromUnreadable 은 **0 과 못 잼을 가른다.**
//
// 판단이 0건인 것과 판단을 못 읽은 것은 다른 사실이다. 못 읽었는데 빈 목록만 내면
// 되짚는 사람은 "앞선 판단이 없다"고 믿고 이미 기각된 길을 다시 간다.
func TestShowSeparatesZeroJudgmentsFromUnreadable(t *testing.T) {
	t.Run("0건은 고백하지 않는다", func(t *testing.T) {
		s, _, sess := showFixture(t)
		res, err := s.ShowItem(ctx(), ShowInput{Project: "p", SessionID: sess, ItemID: "it1"})
		if err != nil {
			t.Fatalf("읽기 실패: %v", err)
		}
		if len(res.Judgments) != 0 {
			t.Fatalf("판단이 %d건이다 — 안 걸었으니 0건이어야 한다", len(res.Judgments))
		}
		if hasAxis(res.Failures, "judgments") {
			t.Fatalf("0건인데 judgments 축을 실패로 고백했다 — 상시 점등이면 판별력이 0이다: %+v",
				res.Failures)
		}
	})

	t.Run("못 읽으면 고백한다", func(t *testing.T) {
		s, st, sess := showFixture(t)
		noteOn(t, s, sess, "it1", "이 판단은 링크 표가 숨으면 안 보인다")
		hideJudgmentLink(t, st)

		res, err := s.ShowItem(ctx(), ShowInput{Project: "p", SessionID: sess, ItemID: "it1"})
		if err != nil {
			t.Fatalf("판단을 못 읽는다고 결과를 통째로 버렸다 — 항목 본문은 이미 참이다: %v", err)
		}
		if res.Item.ID != "it1" {
			t.Fatalf("항목이 안 실렸다: %+v", res.Item)
		}
		if len(res.Judgments) != 0 {
			t.Fatalf("링크 표를 숨겼는데 판단이 %d건 나왔다 — 이 시험의 격리가 안 먹었다", len(res.Judgments))
		}
		if !hasAxis(res.Failures, "judgments") {
			t.Fatalf("못 읽은 사실이 어디에도 없다 — 침묵으로 접혔다: %+v", res.Failures)
		}
	})
}

// TestShowSeparatesZeroRevisionsFromUnreadable 은 개정 축에 같은 판정을 건다.
//
// 축을 따로 재는 이유: 둘을 한 시험에 묶으면 한 축만 고쳐도 초록이 되고, 그러면
// 나머지 축의 침묵이 통과로 굳는다.
func TestShowSeparatesZeroRevisionsFromUnreadable(t *testing.T) {
	t.Run("0건은 고백하지 않는다", func(t *testing.T) {
		s, _, sess := showFixture(t)
		res, err := s.ShowItem(ctx(), ShowInput{Project: "p", SessionID: sess, ItemID: "it1"})
		if err != nil {
			t.Fatalf("읽기 실패: %v", err)
		}
		if len(res.Revisions) != 0 {
			t.Fatalf("개정이 %d건이다 — 안 고쳤으니 0건이어야 한다", len(res.Revisions))
		}
		if hasAxis(res.Failures, "revisions") {
			t.Fatalf("0건인데 revisions 축을 실패로 고백했다: %+v", res.Failures)
		}
	})

	t.Run("못 읽으면 고백한다", func(t *testing.T) {
		s, st, sess := showFixture(t)
		if _, err := st.DB().ExecContext(ctx(),
			`ALTER TABLE item_revision RENAME TO item_revision_hidden`); err != nil {
			t.Fatalf("item_revision 숨기기 실패: %v", err)
		}
		res, err := s.ShowItem(ctx(), ShowInput{Project: "p", SessionID: sess, ItemID: "it1"})
		if err != nil {
			t.Fatalf("개정 이력을 못 읽는다고 결과를 통째로 버렸다: %v", err)
		}
		if !hasAxis(res.Failures, "revisions") {
			t.Fatalf("못 읽은 사실이 어디에도 없다 — 침묵으로 접혔다: %+v", res.Failures)
		}
	})
}

// TestShowRevisionsCarryOrderReasonAndChangedAxis 는 개정 이력이 **rev 순**으로 나오고
// 각 행이 사유와 바뀐 축을 나르는지 잰다.
//
// ★ store 층의 같은 축을 이미 재는 시험이 있다(TestItemRevisionsAreOrderedByRev…).
// 여기서 다시 재는 것은 **배선**이다 — service 가 MarkItemRevisionChanges 를 안 부르면
// 축이 통째로 nil 인데, store 시험은 그것을 원리적으로 못 본다.
func TestShowRevisionsCarryOrderReasonAndChangedAxis(t *testing.T) {
	s, _, sess := showFixture(t)
	if _, err := s.AmendItem(ctx(), AmendInput{
		Project: "p", SessionID: sess, ItemID: "it1",
		Title: strPtr("고친 제목"), Reason: "제목 오타",
	}); err != nil {
		t.Fatalf("첫 개정 실패: %v", err)
	}
	if _, err := s.AmendItem(ctx(), AmendInput{
		Project: "p", SessionID: sess, ItemID: "it1",
		Body: strPtr("고친 본문"), Reason: "전제가 틀렸다",
	}); err != nil {
		t.Fatalf("둘째 개정 실패: %v", err)
	}

	res, err := s.ShowItem(ctx(), ShowInput{Project: "p", SessionID: sess, ItemID: "it1"})
	if err != nil {
		t.Fatalf("읽기 실패: %v", err)
	}
	if len(res.Revisions) != 2 {
		t.Fatalf("개정이 %d건이다 — 2건이어야 한다: %+v", len(res.Revisions), res.Revisions)
	}
	if res.Revisions[0].Rev != 1 || res.Revisions[1].Rev != 2 {
		t.Fatalf("rev 순서가 %d·%d 다 — 1·2 여야 한다", res.Revisions[0].Rev, res.Revisions[1].Rev)
	}
	if res.Revisions[0].Reason != "제목 오타" || res.Revisions[1].Reason != "전제가 틀렸다" {
		t.Fatalf("사유가 %q·%q 다", res.Revisions[0].Reason, res.Revisions[1].Reason)
	}
	// ★ 배선 단정 — 이 축이 nil 이면 service 가 사슬 복원을 안 부른 것이다.
	if strings.Join(res.Revisions[0].Changed, "·") != "title" {
		t.Errorf("rev 1 이 바꾼 축이 %v다 — title 하나여야 한다(service 가 사슬 복원을 안 불렀나)",
			res.Revisions[0].Changed)
	}
	if strings.Join(res.Revisions[1].Changed, "·") != "body" {
		t.Errorf("rev 2 가 바꾼 축이 %v다 — body 하나여야 한다", res.Revisions[1].Changed)
	}
}

// TestShowOnMissingItemIsStandardNotFound 는 없는 항목이 **표준 not-found** 인지 잰다.
//
// errors.Is(err, store.ErrNotFound) 가 성립해야 api 가 404 + 종류별 처방을 낸다.
// 이 계층이 문구를 새로 지으면 좌표(항목 p/없는-id)가 사라지고 처방표가 무엇이
// 없었는지 말하지 못한다.
func TestShowOnMissingItemIsStandardNotFound(t *testing.T) {
	s, _, sess := showFixture(t)
	_, err := s.ShowItem(ctx(), ShowInput{Project: "p", SessionID: sess, ItemID: "없는-id"})
	if err == nil {
		t.Fatal("없는 항목을 읽었는데 성공했다")
	}
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("없음 표식이 안 달렸다(%T: %v) — api 가 404 로 못 옮긴다", err, err)
	}
	var nf *store.NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("좌표가 없는 없음이다(%T) — 처방표가 무엇이 없었는지 못 말한다", err)
	}
	if nf.Kind != store.NFItem || nf.ID != "없는-id" {
		t.Fatalf("좌표가 %s/%s 다 — 항목/없는-id 여야 한다", nf.Kind, nf.ID)
	}
}

func strPtr(s string) *string { return &s }
