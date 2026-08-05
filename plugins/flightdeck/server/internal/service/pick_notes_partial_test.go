package service

import (
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// judgment_link 을 못 읽는 상태를 만든다.
//
// 이 저장소가 저장 실패를 유도하는 기존 방식 그대로다(landing_test 의 signal 축).
// 테이블을 지우지 않고 **이름만 숨긴다** — 지우면 외래키를 건 다른 테이블까지
// 흔들려 무엇이 실패했는지가 흐려진다.
func hideJudgmentLink(t *testing.T, st *store.Store) {
	t.Helper()
	if _, err := st.DB().ExecContext(ctx(),
		`ALTER TABLE judgment_link RENAME TO judgment_link_hidden`); err != nil {
		t.Fatalf("judgment_link 숨기기 실패: %v", err)
	}
}

// TestPickRecommendSurvivesUnreadableJudgmentLink 는 **이미 만든 결과를 버리지
// 않는다**를 못박는다.
//
// ★ 무엇이 걸려 있나. pickRecommend 는 마지막에 연결된 판단 전문을 싣는데, 그 조회는
// 후보 수집·판정·묶음 조립·**pick_eval 기록이 전부 끝난 뒤**에 일어난다. 예전에는 거기서
// 오류를 그대로 올렸다. 그러면 원장에는 "이 세션에 X 를 추천했다"가 남고 세션은 오류만
// 받는다 — **원장과 응답이 갈라진다.** 다음 사람이 pick_eval 을 읽고 "추천이 나갔는데
// 왜 아무도 안 집었나"를 묻게 되는데, 그 질문에 답할 근거가 어디에도 없다.
//
// 같은 함수 안에서 **같은 테이블을 읽는 다른 조회**(siblingIndex)는 이미 부드럽게
// 실패하며 그 사실을 Scope 문장에 적는다. 한 함수 안에 정반대의 실패 정책 둘이
// 있었던 것이고, 이 시험이 그 비대칭을 못박아 없앤다.
func TestPickRecommendSurvivesUnreadableJudgmentLink(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	addItem(t, s, "p", "solo", []string{"services/a.go"}, nil)

	hideJudgmentLink(t, st)

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("판단 링크를 못 읽는다고 추천을 통째로 버렸다: %v", err)
	}
	if res.Mode != PickRecommended || res.Item == nil || res.Item.ID != "solo" {
		t.Fatalf("추천이 안 실렸다 — mode=%q item=%+v", res.Mode, res.Item)
	}

	// 침묵으로 접지 않는다. "연결된 판단 0건"이 **없다는 뜻인지 못 읽었다는 뜻인지**를
	// 응답만으로 가를 수 있어야 한다 — 못 가르면 세션이 앞선 판단이 없다고 믿고
	// 이미 기각된 길을 다시 간다. 이 도구가 존재하는 이유가 정확히 그것을 막는 것이다.
	var confessed bool
	for _, f := range res.Failures {
		if strings.HasPrefix(f.Axis, "notes:") && strings.Contains(f.Axis, "solo") {
			confessed = true
		}
	}
	if !confessed {
		t.Fatalf("판단을 못 읽은 사실이 어디에도 없다 — 침묵으로 접혔다: %+v", res.Failures)
	}
}

// 고백이 **상시 점등**이면 판별력이 0이다. 정상 경로에서는 그 축이 없어야 하고,
// 판단 전문은 실제로 실려야 한다.
//
// ★ 이 짝이 없으면 "notes 를 아예 안 싣고 항상 고백한다"는 고침이 위 시험만 보고
// 초록으로 지나간다. 그건 결함을 다른 결함으로 바꾼 것이다.
func TestPickRecommendStillCarriesJudgmentsWhenReadable(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	addItem(t, s, "p", "solo", []string{"services/a.go"}, nil)
	if _, err := s.Note(ctx(), NoteInput{
		Project: "p", SessionID: me.Session.ID, Kind: model.JudgmentDecision,
		Title: "왜 그렇게 했나", Body: "이 줄이 추천에 실려야 한다", ItemID: "solo",
	}); err != nil {
		t.Fatalf("판단 준비 실패: %v", err)
	}

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("pick 실패: %v", err)
	}
	if len(res.Notes) != 1 || res.Notes[0].Title != "왜 그렇게 했나" {
		t.Fatalf("연결된 판단이 안 실렸다: %+v", res.Notes)
	}
	for _, f := range res.Failures {
		if strings.HasPrefix(f.Axis, "notes:") {
			t.Fatalf("다 읽었는데 못 읽었다고 말한다: %+v", res.Failures)
		}
	}
}

// 같은 실패에서 **형제 축의 고백은 그대로 살아 있어야** 한다.
//
// judgment_link 하나가 죽으면 두 조회가 함께 죽는다: 형제 색인(siblingIndex)과
// 판단 전문(linkedJudgments). 전자는 Bundle.Scope 문장으로, 후자는 파생 실패 축으로
// 각자의 자리에서 고백한다. 한쪽을 고치면서 다른 쪽을 조용히 끄는 것을 막는다.
func TestPickRecommendConfessesBothAxesOnSameFailure(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	addItem(t, s, "p", "solo", []string{"services/a.go"}, nil)

	hideJudgmentLink(t, st)

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("pick 실패: %v", err)
	}
	if res.Bundle == nil {
		t.Fatal("묶음 축이 nil 이다")
	}
	if !strings.Contains(res.Bundle.Scope, "못 읽") {
		t.Fatalf("형제 축을 못 읽었다는 고백이 Scope 에서 사라졌다: %q", res.Bundle.Scope)
	}
}
