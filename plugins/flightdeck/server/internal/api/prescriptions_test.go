package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// 처방 REST 표면 — 세션 좌표 하나로 이 턴의 처방을 낸다.
//
// ★ 본문 인자가 없다(설계 §1 원칙 ①). 필요한 것은 전부 세션 id 로부터 파생된다 —
// 그래서 아래 시험도 요청 본문을 안 싣는다.

// TestPrescriptionsRouteReturnsShownAndFolded 는 선점 없이 편집한 세션이
// unclaimed 처방을 받는지를 HTTP 좌표계로 확인한다.
func TestPrescriptionsRouteReturnsShownAndFolded(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-presc-1")

	// 선점 없이 경로 하나를 편집한다 — unclaimedPrescription 이 뜨는 조건이다.
	//
	// ★ 이 전제는 POST /api/v1/footprints 로 넣고 있었다. 그 표면을 지우면서
	// signals 로 옮겼다(2026-08-05) — 발자국이 실제로 들어오는 문이 그쪽이고,
	// 시험이 죽은 표면을 쓰고 있으면 그 표면이 살아 있는 것처럼 보인다.
	if w := e.write(http.MethodPost, "/api/v1/sessions/"+sess+"/signals", map[string]any{
		"kind": "tool", "paths": []string{"a/b.go"},
	}); w.Code != http.StatusAccepted {
		t.Fatalf("전제가 깨졌다 — 신호 기록이 %d 다: %s", w.Code, w.Body.String())
	}

	res := e.write(http.MethodPost, "/api/v1/sessions/"+sess+"/prescriptions", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("상태가 다르다: got %d, want 200 — body=%s", res.Code, res.Body.String())
	}

	var got struct {
		Shown  []map[string]any `json:"shown"`
		Folded int              `json:"folded"`
		All    []map[string]any `json:"all"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatalf("응답 해석 실패: %v — %s", err, res.Body.String())
	}
	if len(got.All) == 0 {
		t.Fatalf("처방이 0건이다: %s", res.Body.String())
	}
	if got.Shown[0]["key"] == nil || got.Shown[0]["text"] == nil {
		t.Fatalf("key·text 가 응답에 없다: %s", res.Body.String())
	}
}

// TestPrescriptionsRouteRejectsUnknownSession 은 모르는 세션 id 가
// 좌표 있는 404(not_found)로 응답되는지를 본다(store.NotFoundError → NFSession).
func TestPrescriptionsRouteRejectsUnknownSession(t *testing.T) {
	e := newEnv(t, nil)
	res := e.write(http.MethodPost, "/api/v1/sessions/nope/prescriptions", nil)
	if res.Code != http.StatusNotFound {
		t.Fatalf("모르는 세션에 404 가 아니다: got %d — %s", res.Code, res.Body.String())
	}
	errBody := errorOf(t, res)
	if errBody["code"] != "not_found" {
		t.Errorf("오류 코드가 not_found 가 아니다: %v", errBody["code"])
	}
}
