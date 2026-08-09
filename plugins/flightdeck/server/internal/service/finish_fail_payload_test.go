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
