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

// TestFindSessionNeedsAllThreeKeyParts 는 3중키의 세 축이 **각각** 조회를 가르는지 본다.
//
// "이웃을 심고 못 찾는지 본다" — 기준 카드 하나(machine=m1 · worktree=e.repo · cc=cc-x)를
// 세운 뒤, 축 하나씩만 다른 좌표로 조회한다. 세 경우 전부 **404 여야 한다.** 축 하나가
// WHERE 절에서 빠지면 그 이웃(기준 카드)이 걸려 200 이 되므로 확정적으로 잡힌다 —
// 행 순서에 안 기댄다(올바른 답이 404 라서 한 건이라도 200 이면 실패다).
//
// e.openSession 이 고정하는 machine="m1"·worktree=e.repo 를 그대로 기준으로 쓴다 —
// TestFindSessionNeverCreatesACard·TestFindSessionReturnsExistingCard 는 이 둘을 고정하고
// cc 만 바꾸므로 machine_id·worktree 가 WHERE 절에서 빠져도 안 잡힌다(검토가 뮤테이션으로
// 확인). 이 시험이 그 사각지대를 메운다.
func TestFindSessionNeedsAllThreeKeyParts(t *testing.T) {
	e := newEnv(t, nil)
	e.openSession("cc-x") // 기준 카드: machine=m1 · worktree=e.repo · cc=cc-x

	cases := []struct {
		name            string
		machine, wt, cc string
		why             string
	}{
		{
			name: "머신이 다르면 못 찾는다", machine: "m2", wt: e.repo, cc: "cc-x",
			why: "WHERE 에서 machine_id 가 빠지면 기준 카드가 걸려 200 이 된다",
		},
		{
			name: "워크트리가 다르면 못 찾는다", machine: "m1", wt: e.repo + "/elsewhere", cc: "cc-x",
			why: "WHERE 에서 worktree 가 빠지면 기준 카드가 걸려 200 이 된다",
		},
		{
			name: "cc 가 다르면 못 찾는다", machine: "m1", wt: e.repo, cc: "cc-y",
			why: "WHERE 에서 cc_session_id 가 빠지면 기준 카드가 걸려 200 이 된다",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := url.Values{"machine": {c.machine}, "worktree": {c.wt}, "cc": {c.cc}}
			w := e.do(http.MethodGet, "/api/v1/sessions?"+q.Encode(), nil, loopback())
			if w.Code != http.StatusNotFound {
				t.Fatalf("상태 %d, 원하는 것 404 — %s\n본문: %s", w.Code, c.why, w.Body.String())
			}
		})
	}
}

// TestFindSessionTrimsQueryValues 는 질의값에 공백이 붙어도 같은 카드를 찾는지 본다.
func TestFindSessionTrimsQueryValues(t *testing.T) {
	e := newEnv(t, nil)
	e.openSession("cc-x") // machine=m1 · worktree=e.repo · cc=cc-x

	q := url.Values{"machine": {" m1 "}, "worktree": {" " + e.repo + " "}, "cc": {" cc-x "}}
	w := e.do(http.MethodGet, "/api/v1/sessions?"+q.Encode(), nil, loopback())
	if w.Code != http.StatusOK {
		t.Fatalf("상태 %d, 원하는 것 200 — 공백이 붙으면 못 찾는다면 TrimSpace 가 빠진 것이다\n본문: %s",
			w.Code, w.Body.String())
	}
}
