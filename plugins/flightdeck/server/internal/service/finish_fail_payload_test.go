package service

import (
	"encoding/json"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// readEventPayload 는 kind 하나의 **가장 최근** payload 를 map 으로 낸다.
//
// map[string]any 로 받는 이유: 구조체로 받으면 없는 키와 빈 문자열이 같은 값이 되고,
// 이 항목이 세우려는 규율이 정확히 그 둘을 가르는 것이다(축이 없으면 키 자체가 없다).
func readEventPayload(t *testing.T, st *store.Store, kind string) map[string]any {
	t.Helper()
	var raw string
	if err := st.DB().QueryRowContext(ctx(),
		`SELECT payload FROM event WHERE kind = ? ORDER BY id DESC LIMIT 1`, kind).Scan(&raw); err != nil {
		t.Fatalf("%s 이벤트가 없다: %v", kind, err)
	}
	var p map[string]any
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("%s payload 해석 실패(%s): %v", kind, raw, err)
	}
	return p
}

// TestFinishFailNamesItsItemAndMode 는 롤백된 마무리가 **자기가 어느 항목의 것인지**
// 말하는지 본다.
//
// ★ 왜 이 축인가. 앞 판의 item.finish.fail payload 는 error 하나뿐이었다. 그래서 롤백된
// 종료 선언과 그 실패 사유를 잇는 유일한 방법이 "같은 초·같은 세션"이라는 추론이었다 —
// DESIGN §10 의 실측 문단(1435행)이 그 방법으로 잰 값을 싣고 있다. 초 단위 짝짓기는 한
// 세션이 한 초에 두 항목을 건드리는 순간 조용히 틀리고, 틀렸다는 사실조차 안 남는다.
//
// ★ 실패시키는 수단은 finish_test.go 의 TestFinishRollsBackEverythingWhenFollowupFails 와
// 같다(선행 조건이 빈 후속). 그쪽은 **롤백 계약**을 재고 이쪽은 **원장의 좌표**를 잰다 —
// 같은 수단, 다른 축이다.
func TestFinishFailNamesItsItemAndMode(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")

	_, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone, Title: "batch7 랜딩", Body: "①…②…③…④…",
		Followups: []FollowupInput{
			{ID: "bad-after", Title: "후속", Body: "본문", After: []model.After{{}}},
		},
	})
	if err == nil {
		t.Fatalf("불변식을 깨는 후속은 실패해야 한다 — 이 시험의 전제가 깨졌다")
	}

	p := readEventPayload(t, st, "item.finish.fail")
	if p["item"] != "batch7" {
		t.Fatalf("실패 이벤트가 어느 항목의 것인지 안 말한다: %v", p)
	}
	if p["mode"] != string(model.ItemDone) {
		t.Fatalf("실패 이벤트가 무엇을 하려 했는지 안 말한다: %v", p)
	}

	// ★ **표류 탐지에 먹이면 안 된다.** .fail 은 종료 선언이 아니다.
	//   CloseDeclarationsByItem 은 kind='item.finish' 로 정확히 거르므로(store/event.go)
	//   이 항목의 선언은 1건이어야 한다 — 2건이면 .fail 이 같은 축에 섞인 것이다.
	decls, derr := st.CloseDeclarationsByItem(ctx(), "p")
	if derr != nil {
		t.Fatalf("종료 선언 조회 실패: %v", derr)
	}
	if got := decls["batch7"].Done; got != 1 {
		t.Fatalf("종료 선언이 %d건이다(기대 1) — .fail 이 표류 탐지에 섞였다", got)
	}
}

// TestFinishFailCauseSeparatesTheStageFromTheLeaf 는 **후속 등록 단계**의 실패가
// leaf 오류의 종류에 먹히지 않는지 본다.
//
// 고칠 자리가 정반대다: 단계가 후속이면 고칠 것은 followups 인자이고, 끝내려는 항목이
// 없는 것이면 고칠 것은 item_id 다. 그 둘이 같은 값으로 쌓이면 원장은 "몇 번 실패했나"만
// 답하고 "무엇을 고쳐야 하나"에는 앞 판과 똑같이 침묵한다.
func TestFinishFailCauseSeparatesTheStageFromTheLeaf(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")

	if _, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone, Title: "batch7 랜딩", Body: "①…②…③…④…",
		Followups: []FollowupInput{
			{ID: "bad-after", Title: "후속", Body: "본문", After: []model.After{{}}},
		},
	}); err == nil {
		t.Fatalf("불변식을 깨는 후속은 실패해야 한다 — 전제가 깨졌다")
	}
	p := readEventPayload(t, st, "item.finish.fail")
	if p["cause"] != "followup-write" {
		t.Fatalf("사유 갈래가 %v 다(기대 followup-write): %v", p["cause"], p)
	}
}

// TestFinishFailCauseSeparatesClaimDriftFromItemMissing 은 처방이 갈리는 두 갈래를 본다.
//
// claim-drift 는 "그 세션에 물어라"이고 item-missing 은 "id 를 확인해라"다. 한 값으로
// 뭉치면 원장은 두 처방 중 어느 쪽도 못 낸다.
func TestFinishFailCauseSeparatesClaimDriftFromItemMissing(t *testing.T) {
	t.Run("남이 쥔 항목", func(t *testing.T) {
		s, st := newSvc(t)
		repo, wt := newRepoWithWorktree(t, "feat")
		me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
		other := openSession(t, s, "p", repo, repo, "cc-2", "트랙7")
		addItem(t, s, "p", "batch7", nil, nil)
		claimed(t, s, "p", other.Session.ID, "batch7")

		if _, err := s.Finish(ctx(), FinishInput{
			Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
			Outcome: model.ItemDone, Body: "①…②…③…④…",
		}); err == nil {
			t.Fatalf("남이 쥔 항목은 끝낼 수 없어야 한다")
		}
		p := readEventPayload(t, st, "item.finish.fail")
		if p["cause"] != "claim-drift" || p["item"] != "batch7" {
			t.Fatalf("선점 표류 갈래가 아니다: %v", p)
		}
	})

	t.Run("없는 항목", func(t *testing.T) {
		s, st := newSvc(t)
		repo, wt := newRepoWithWorktree(t, "feat")
		me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")

		if _, err := s.Finish(ctx(), FinishInput{
			Project: "p", SessionID: me.Session.ID, ItemID: "ghost",
			Outcome: model.ItemDropped, Body: "①…②…③…④…", CloseReason: "없는 항목이다",
		}); err == nil {
			t.Fatalf("없는 항목은 끝낼 수 없어야 한다")
		}
		p := readEventPayload(t, st, "item.finish.fail")
		if p["cause"] != "item-missing" {
			t.Fatalf("항목 없음 갈래가 아니다: %v", p)
		}
		if p["mode"] != string(model.ItemDropped) {
			t.Fatalf("무엇을 하려 했는지가 없다: %v", p)
		}
	})
}

// TestLogFailKeepsANonItemAbsenceFromMasqueradingAsAMissingItem 은 **항목이 아닌 없음**이
// item-missing 으로 굳지 않는지 본다.
//
// store 의 없음은 좌표를 들고 온다(NotFoundError.Kind — 항목·세션·자원·줄 행 …).
// 그것을 안 보고 ErrNotFound 하나로 접으면, 세션이 없어서 난 실패가 원장에 "항목이 없다"로
// 남는다 — 화면이 관측하지 않은 원인을 단정하는 이 저장소의 상습 실패 모양이다.
func TestLogFailKeepsANonItemAbsenceFromMasqueradingAsAMissingItem(t *testing.T) {
	s, st := newSvc(t)
	if err := s.SetState(ctx(), "sess-없음", model.SessionPaused, ""); err == nil {
		t.Fatalf("없는 세션의 상태는 못 바꾼다 — 전제가 깨졌다")
	}
	p := readEventPayload(t, st, "session.state.fail")
	if p["cause"] != "not-found" {
		t.Fatalf("없음 갈래가 %v 다(기대 not-found — 항목이 아닌 없음이다): %v", p["cause"], p)
	}
	if _, ok := p["item"]; ok {
		t.Fatalf("겨눈 항목이 없는데 item 축이 실렸다: %v", p)
	}
	if p["mode"] != string(model.SessionPaused) {
		t.Fatalf("무엇을 하려 했는지가 없다: %v", p)
	}
}

// TestLogFailMarksAnUnclassifiedCauseAsOther 는 갈래에 안 걸린 것이 **분류 안 됨**으로
// 남는지 본다. "원인 없음"이 아니다 — 이 값이 늘면 갈래를 늘릴 자리가 있다는 뜻이고,
// 그 판정은 이 파일이 아니라 원장이 낸다.
//
// 수단: judgment.supersedes 는 judgment(id) FK 다(schema.sql:245). 없는 id 를 주면
// 제약 위반이고, 그것은 없음도 선점 표류도 후속도 아니다.
func TestLogFailMarksAnUnclassifiedCauseAsOther(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")

	if _, err := s.Note(ctx(), NoteInput{
		Project: "p", SessionID: me.Session.ID, Kind: model.JudgmentDecision,
		Body: "정정한다", Supersedes: "j-없음",
	}); err == nil {
		t.Fatalf("없는 판단을 정정할 수 없다 — 전제가 깨졌다")
	}
	p := readEventPayload(t, st, "judgment.note.fail")
	if p["cause"] != "other" {
		t.Fatalf("분류 안 됨 갈래가 아니다: %v", p)
	}
	if p["mode"] != string(model.JudgmentDecision) {
		t.Fatalf("무엇을 하려 했는지가 없다: %v", p)
	}
}
