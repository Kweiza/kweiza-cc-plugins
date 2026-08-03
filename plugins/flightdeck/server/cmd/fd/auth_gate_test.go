package main

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// 리뷰가 실물 서버로 재현한 인가 우회를 닫는다.
//
// 앞선 판은 화면을 바깥 mux 에 붙여 게이트 사슬 밖에 두었고, 토큰을 켠 배포에서
// REST 는 401 인데 `POST /actions/drop` 은 303 으로 통과하며 항목이 실제로 폐기됐다.
// 이 제품이 막으려는 것이 "남의 작업을 통째로 집는 사고"인데 그것을 아무나 원격에서 낼 수 있었다.
func TestWebWritesAreBehindTheSameGateAsREST(t *testing.T) {
	h := newHarnessAuth(t, "supersecret")

	// ── 대조 전제 ① 인증이 정말 켜져 있는가. 안 켜져 있으면 아래는 아무것도 안 지킨다.
	if h.token == "" {
		t.Fatal("전제가 깨졌다 — 토큰이 비었다")
	}
	// ── 대조 전제 ② 토큰을 주면 통과하는가. 전부 401 이면 "잠겼다"가 공허하다.
	req, _ := http.NewRequest("GET", h.srv.URL+"/api/v1/dashboard.json?project=p", nil)
	req.Header.Set("Authorization", "Bearer supersecret")
	res, err := h.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		t.Fatal("전제가 깨졌다 — 올바른 토큰도 401 이다")
	}

	// httptest 는 127.0.0.1 이라 루프백 면제가 걸린다. 면제를 끄고 재조립해야
	// "원격에서 들어온 요청"과 같은 좌표계가 된다 — 그러지 않으면 이 시험이
	// 루프백이라 통과한 것인지 게이트가 막은 것인지 구분되지 않는다.
	h2 := newHarnessAuth(t, "supersecret")
	h2.requireTokenEverywhere()

	for _, c := range []struct {
		name, method, path string
		form               url.Values
	}{
		{"REST 읽기", "GET", "/api/v1/dashboard.json?project=p", nil},
		{"화면", "GET", "/", nil},
		{"화면 쓰기 — 항목 폐기", "POST", "/actions/drop",
			url.Values{"project": {"p"}, "item": {"victim"}, "reason": {"무인증 시험"}}},
		{"화면 쓰기 — 선점 회수", "POST", "/actions/reclaim",
			url.Values{"project": {"p"}, "item": {"victim"}, "reason": {"무인증 시험"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var body io.Reader
			if c.form != nil {
				body = strings.NewReader(c.form.Encode())
			}
			req, _ := http.NewRequest(c.method, h2.srv.URL+c.path, body)
			if c.form != nil {
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			res, err := h2.srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			if res.StatusCode != http.StatusUnauthorized {
				t.Errorf("무인증 %s %s 가 %d 다 — 401 이어야 한다", c.method, c.path, res.StatusCode)
			}
		})
	}
}
