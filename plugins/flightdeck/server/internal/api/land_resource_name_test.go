package api

import (
	"net/http"
	"testing"
)

// TestLandRejectsInvalidResourceNameAsRefusal 은 최종 리뷰 Important #1 을 잠근다
// (2026-08-12, 실측 재현: 공백·한글이 든 자원 이름을 준 land 가 500 으로 나갔다).
//
// store.EnqueueLanding 은 store.ValidateResourceName 을 이미 돌리지만, 그 오류는
// 평범한 error 라 ClassifyError 의 화이트리스트(service.RefusedError·store.ClaimHeldError…)
// 를 못 타고 그대로 500(내부 오류)이 된다. service.Land 가 정규화 뒤 같은 검증을 돌려
// RefusedError 로 감싸야 한다 — 자원 이름 오타는 사용자 오류(400)이지 서버 결함이 아니다.
func TestLandRejectsInvalidResourceNameAsRefusal(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-1")

	w := e.write(http.MethodPost, "/api/v1/landing", map[string]any{
		"project": testProject, "session_id": sess, "mode": "acquire",
		"resources": []string{"나쁜 이름"},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("자원 이름이 나쁜데 %d 다(기대 400/refused) — 500 이면 사용자 오류가 서버 결함으로 보인다: %s",
			w.Code, w.Body.String())
	}
	if code, _ := errorOf(t, w)["code"].(string); code != "refused" {
		t.Fatalf("오류 코드가 %q 다(기대 refused): %s", code, w.Body.String())
	}
}
