package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// service 계층의 **입력 거절이 400 refused 로 나가는지**를 HTTP 레벨에서 잠근다.
//
// ★ 왜 서비스 계층 시험만으로 부족한가. `ClassifyError`(errors.go)는 **화이트리스트**다 —
// `*service.RefusedError` 갈래가 지워지거나 순서가 밀리면 같은 오류가 조용히 500 으로
// 돌아가는데, service 쪽에서 타입만 단정하는 시험은 **그 배선이 끊겨도 안 빨개진다.**
// 재는 것은 "service 가 무엇을 반환하나"가 아니라 "사용자가 무엇을 받나"다.
//
// 선례가 바로 옆에 있다: `land_resource_name_test.go` 의
// `TestLandRejectsInvalidResourceNameAsRefusal` 이 2026-08-12 에 정확히 같은 버그 부류
// (평범한 error 가 화이트리스트를 못 타고 500 이 되던 것)를 고치고 이 방식으로 잠갔다.
// `claim_bundle_test.go` 의 `TestClaimBundleLeadMismatchRefuses` 도 같은 패턴이다.
//
// 이 파일이 덮는 여섯 자리는 `service/{move,cut_after,label}.go` 의 입력 거절이고,
// 그중 label 의 빈-요청 하나는 **MCP 에서 정상 도달 가능**하다(도구 스키마의 required 가
// `item_id` 하나뿐이라 add·rm 을 둘 다 안 준 호출이 그대로 service 까지 온다).

// assertRefused 는 응답이 400 refused 이고 처방을 실었는지 본다.
//
// guidance 까지 보는 이유: 이 저장소의 거절은 **처방을 함께 낸다**. 400 만 맞고 처방이
// 비면 사용자는 무엇을 고쳐야 하는지 모른 채 거절만 받는다 — 그것은 절반만 고친 것이다.
func assertRefused(t *testing.T, w *httptest.ResponseRecorder, what string) {
	t.Helper()
	if w.Code != http.StatusBadRequest {
		t.Fatalf("%s: 응답이 %d 다(기대 400/refused) — 500 이면 사용자 오류가 서버 결함으로 보이고, "+
			"정성껏 쓴 거절 문구 대신 \"서버 내부 오류다\"가 나간다: %s", what, w.Code, w.Body.String())
	}
	e := errorOf(t, w)
	if code, _ := e["code"].(string); code != "refused" {
		t.Fatalf("%s: 오류 코드가 %q 다(기대 refused): %s", what, code, w.Body.String())
	}
	if g, _ := e["guidance"].(string); g == "" {
		t.Errorf("%s: 거절에 guidance 가 비었다 — 이 저장소의 거절은 처방을 함께 낸다: %s",
			what, w.Body.String())
	}
}

// TestLabelEmptyRequestIsRefusedNotInternal 은 **최종 전수 리뷰가 park 한 그 자리**다.
//
// 빈 요청 거절은 항목을 조회하기 **전에** 선다. 그래서 있지도 않은 항목 id 로 불러도
// 404 가 아니라 400 이어야 한다 — 순서가 뒤집히면 이 시험이 404 로 빨개진다.
func TestLabelEmptyRequestIsRefusedNotInternal(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-1")

	w := e.write(http.MethodPost, "/api/v1/items/nonexistent-item/label", map[string]any{
		"project": testProject, "session_id": sess,
		"add": []string{}, "rm": []string{},
	})
	assertRefused(t, w, "label 빈 요청")
}

// 공백만 든 꼬리표도 빈 요청이다 — judge.ApplyLabels 가 공백을 버리므로 여기서 안 막으면
// "무엇도 안 바뀐 성공"이 원장에 한 줄 남는다.
func TestLabelWhitespaceOnlyRequestIsRefused(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-1")

	w := e.write(http.MethodPost, "/api/v1/items/nonexistent-item/label", map[string]any{
		"project": testProject, "session_id": sess,
		"add": []string{"  ", ""}, "rm": []string{"   "},
	})
	assertRefused(t, w, "label 공백-only 요청")
}

// 아래 셋은 같은 부류(빈 project)를 세 동사에서 각각 잠근다.
//
// 한 동사만 덮으면 나머지 둘의 배선이 끊겨도 초록이다 — 실제로 이 셋은 2026-08-12 까지
// **셋 다** 평범한 error 라 500 으로 나갔고, label 하나만 먼저 고쳐졌을 때
// "move·cut_after 도 같은 선례"라는 이유로 나머지가 남았다.
func TestMoveWithoutProjectIsRefused(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-1")

	w := e.write(http.MethodPost, "/api/v1/items/some-item/move", map[string]any{
		"session_id": sess, "to": "다른-프로젝트",
	})
	assertRefused(t, w, "move 빈 project")
}

func TestCutAfterWithoutProjectIsRefused(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-1")

	w := e.write(http.MethodPost, "/api/v1/items/some-item/after/cut", map[string]any{
		"session_id": sess,
		"dep":        map[string]any{"item": "선행-항목"},
	})
	assertRefused(t, w, "after cut 빈 project")
}

func TestLabelWithoutProjectIsRefused(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-1")

	w := e.write(http.MethodPost, "/api/v1/items/some-item/label", map[string]any{
		"session_id": sess, "add": []string{"tickler"},
	})
	assertRefused(t, w, "label 빈 project")
}
