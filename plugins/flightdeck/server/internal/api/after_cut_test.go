package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// ─────────────────────────────────────────────────────────────────────────────
// 선행 절단의 표면 — `after-dropped-dep` 의 처방을 실제로 집행할 수 있는 유일한 라우트
// ─────────────────────────────────────────────────────────────────────────────

func seedAfterCut(t *testing.T, e *env) {
	t.Helper()
	ctx := context.Background()
	if err := e.st.UpsertProject(ctx, model.Project{ID: testProject, Path: e.repo}); err != nil {
		t.Fatalf("프로젝트 등록 실패: %v", err)
	}
	for _, id := range []string{"dep", "waiter"} {
		if err := e.st.AddItem(ctx, model.Item{Project: testProject, ID: id, Title: id, Body: "본문"}); err != nil {
			t.Fatalf("항목 등록 실패(%s): %v", id, err)
		}
	}
	if err := e.st.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.AddAfter(testProject, "waiter", model.After{Item: "dep"}); err != nil {
			return err
		}
		return tx.AddAfter(testProject, "waiter", model.After{SHA: "0f19bf3"})
	}); err != nil {
		t.Fatalf("선행 등록 실패: %v", err)
	}
}

// 끊으면 200 이고, 응답이 **남은 선행**을 낸다.
func TestCutAfterRouteAnswersWithWhatIsStillAttached(t *testing.T) {
	e := newEnv(t, nil)
	seedAfterCut(t, e)

	w := e.write(http.MethodPost, "/api/v1/items/waiter/after/cut", map[string]any{
		"project": testProject, "session_id": "", "dep": map[string]any{"item": "dep"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("상태 %d — 본문: %s", w.Code, w.Body.String())
	}

	body := decodeBody(t, w)
	item, ok := body["item"].(map[string]any)
	if !ok {
		t.Fatalf("응답에 item 절이 없다: %s", w.Body.String())
	}
	// model.Item 은 json 태그가 없어 필드명 그대로 나간다(기존 move 응답과 같은 모양이다).
	after, _ := item["After"].([]any)
	if len(after) != 1 {
		t.Fatalf("남은 선행이 %d건이다 — sha 하나만 남아야 한다: %s", len(after), w.Body.String())
	}
	if got, _ := after[0].(map[string]any)["SHA"].(string); got != "0f19bf3" {
		t.Errorf("남은 선행이 sha 축이 아니다: %v", after[0])
	}
	// 무엇을 끊었는지도 응답에 있어야 한다 — 그것만 보고 원장에 판단을 쓴다.
	if cut, _ := body["cut"].(map[string]any); cut == nil || cut["Item"] != "dep" {
		t.Errorf("응답에 끊은 것이 없다: %s", w.Body.String())
	}
}

// 안 걸린 선행을 끊으라고 하면 404 이고, **처방이 축을 짚는다.**
//
// 이 종류(NFItemAfter)의 가장 흔한 실수는 dep 이름이 아니라 축(item·job·sha)을 틀리는 것이다.
// 처방이 일반 문구로 새면 사람은 이름만 계속 고쳐 본다.
func TestCutAfterRouteRefusesAnUnattachedDepWithAnAxisAwarePrescription(t *testing.T) {
	e := newEnv(t, nil)
	seedAfterCut(t, e)

	w := e.write(http.MethodPost, "/api/v1/items/waiter/after/cut", map[string]any{
		"project": testProject, "dep": map[string]any{"job": "dep"}, // 축을 틀렸다
	})
	if w.Code != http.StatusNotFound {
		t.Fatalf("상태 %d — 404 여야 한다. 본문: %s", w.Code, w.Body.String())
	}
	errObj := errorOf(t, w)
	guidance, _ := errObj["guidance"].(string)
	if !strings.Contains(guidance, "축") {
		t.Errorf("처방이 축을 안 짚는다 — 사람은 dep 이름만 고쳐 본다: %v", errObj)
	}

	// ── 거절이 부작용을 남기면 더 나쁘다. 걸려 있던 둘은 그대로여야 한다.
	it, err := e.st.GetItem(context.Background(), testProject, "waiter")
	if err != nil {
		t.Fatalf("항목 조회 실패: %v", err)
	}
	if len(it.After) != 2 {
		t.Errorf("거절했는데 선행이 %d건이다 — 엉뚱한 행을 건드렸다", len(it.After))
	}
}
