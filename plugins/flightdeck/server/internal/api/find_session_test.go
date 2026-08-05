package api

import (
	"net/http"
	"net/url"
	"testing"
)

// 이 시험이 이 항목의 전부다 — 조회가 **행을 만들지 않는 것**.
//
// 발판(before)을 0 이 아니게 두려고 먼저 진짜 세션 하나를 연다 — "0 에서 0 으로"는
// "안 늘었다"와 "아무것도 못 쟀다"를 가르지 못한다. 조회 대상 3중키(cc-none)는
// 그 세션과 겹치지 않는 값을 일부러 고른다.
func TestFindSessionNeverCreatesACard(t *testing.T) {
	e := newEnv(t, nil)
	e.openSession("cc-real")

	before := len(decodeBody(t, e.do(http.MethodGet,
		"/api/v1/dashboard.json?project="+testProject, nil, loopback()))["sessions"].([]any))

	q := url.Values{"machine": {"m1"}, "worktree": {e.repo}, "cc": {"cc-none"}}
	w := e.do(http.MethodGet, "/api/v1/sessions?"+q.Encode(), nil, loopback())
	if w.Code != http.StatusNotFound {
		t.Fatalf("상태 %d, 원하는 것 404 — 없는 것은 없다고 말해야 한다\n본문: %s", w.Code, w.Body.String())
	}

	after := len(decodeBody(t, e.do(http.MethodGet,
		"/api/v1/dashboard.json?project="+testProject, nil, loopback()))["sessions"].([]any))
	if after != before {
		t.Fatalf("세션이 %d장에서 %d장으로 늘었다 — 조회가 카드를 만들었다. "+
			"이 항목이 고치려는 바로 그 결함이다", before, after)
	}
}

// TestFindSessionReturnsExistingCard 는 있는 카드를 정확히 그 카드로 찾는지 본다.
// model.Session 은 json 태그가 없어 필드명 그대로("ID") 실린다 — helper_test.go 의
// openSession 이 같은 규약을 쓴다.
func TestFindSessionReturnsExistingCard(t *testing.T) {
	e := newEnv(t, nil)
	want := e.openSession("cc-a")

	q := url.Values{"machine": {"m1"}, "worktree": {e.repo}, "cc": {"cc-a"}}
	w := e.do(http.MethodGet, "/api/v1/sessions?"+q.Encode(), nil, loopback())
	if w.Code != http.StatusOK {
		t.Fatalf("상태 %d, 원하는 것 200\n본문: %s", w.Code, w.Body.String())
	}
	got := decodeBody(t, w)["session"].(map[string]any)["ID"].(string)
	if got != want {
		t.Fatalf("세션 %q, 원하는 것 %q", got, want)
	}
}
