package api

import (
	"net/http"
	"testing"
)

// TestAmendRouteAppliesOnlyRequestedAxis 는 라우트가 실제로 붙었고, title 만 준
// 요청이 title 만 고치는지 본다 — 경로의 {id} 가 service 까지 제대로 넘어가지
// 않으면 항목을 못 찾아 이 시험이 애초에 200 을 못 받는다.
func TestAmendRouteAppliesOnlyRequestedAxis(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-1")
	e.addItemIn(t, testProject, sess, "amd-1", nil)

	body := e.okBody(t, e.write(http.MethodPost, "/api/v1/items/amd-1/amend", map[string]any{
		"project": testProject, "session_id": sess,
		"title": "고친 제목", "reason": "오타",
	}), "본문 고침")

	for _, k := range []string{"item", "rev", "changed", "before"} {
		if _, ok := body[k]; !ok {
			t.Errorf("응답에 %q 가 없다 — 되돌릴 좌표와 실제 변화분이 화면에 나야 한다", k)
		}
	}

	item, ok := body["item"].(map[string]any)
	if !ok {
		t.Fatalf("응답의 item 절을 못 읽었다: %v", body)
	}
	if item["Title"] != "고친 제목" {
		t.Errorf("title 이 %v 다 — 요청한 값이 안 실렸다", item["Title"])
	}
	if item["Body"] != "amd-1 본문" {
		t.Errorf("body 가 %v 다 — title 만 준 요청이 본문까지 건드렸다", item["Body"])
	}

	before, ok := body["before"].(map[string]any)
	if !ok {
		t.Fatalf("응답의 before 절을 못 읽었다: %v", body)
	}
	if before["Title"] != "amd-1 제목" {
		t.Errorf("before.title 이 %v 다 — 고치기 전 값이어야 한다", before["Title"])
	}

	changed, _ := body["changed"].([]any)
	if len(changed) != 1 || changed[0] != "title" {
		t.Errorf("changed 가 %v 다 — title 하나만 바뀌어야 한다", changed)
	}
}

// TestAmendDistinguishesOmittedFromEmptyBody 는 생략과 빈 문자열이 갈리는지 본다.
//
// ★ 이 축이 무너지면 두 방향으로 다친다 — 값 타입으로 받으면 "본문을 비워라"가
// 원리적으로 불가능해지거나(빈 문자열과 미지정이 같은 0값이라), title 만 고치려던
// 요청이 body 를 통째로 지운다(안 준 축까지 "빈 값으로 바꿔라"로 읽혀서).
func TestAmendDistinguishesOmittedFromEmptyBody(t *testing.T) {
	e := newEnv(t, nil)
	sess := e.openSession("cc-1")
	e.addItemIn(t, testProject, sess, "amd-2", nil)

	// body 만 명시적으로 빈 문자열로 준다 — title 은 요청에 아예 없다(맵에 키가 없으므로
	// JSON 에도 안 실린다).
	body := e.okBody(t, e.write(http.MethodPost, "/api/v1/items/amd-2/amend", map[string]any{
		"project": testProject, "session_id": sess,
		"body": "", "reason": "본문을 비운다",
	}), "본문 비움")

	item, ok := body["item"].(map[string]any)
	if !ok {
		t.Fatalf("응답의 item 절을 못 읽었다: %v", body)
	}
	if item["Body"] != "" {
		t.Errorf("body 가 %q 다 — 빈 문자열로 바뀌었어야 한다", item["Body"])
	}
	if item["Title"] != "amd-2 제목" {
		t.Errorf("title 이 %v 다 — 요청에 없던 title 이 바뀌었다(생략과 빈 값이 안 갈렸다)", item["Title"])
	}

	changed, _ := body["changed"].([]any)
	if len(changed) != 1 || changed[0] != "body" {
		t.Errorf("changed 가 %v 다 — body 하나만 바뀌어야 한다", changed)
	}
}
