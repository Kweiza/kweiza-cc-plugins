package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestJudgeCookieSecure(t *testing.T) {
	cases := []struct {
		name  string
		tls   bool
		proto string
		want  bool
	}{
		{"평문", false, "", false},
		{"직접 TLS", true, "", true},
		{"프록시가 https 라 함", false, "https", true},
		{"프록시가 http 라 함", false, "http", false},
		{"프록시 목록의 첫째를 본다", false, "https, http", true},
		{"대문자", false, "HTTPS", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := JudgeCookieSecure(c.tls, c.proto); got != c.want {
				t.Fatalf("JudgeCookieSecure(%v, %q) = %v, 기대 %v", c.tls, c.proto, got, c.want)
			}
		})
	}
}

func TestLoginCookieAttributes(t *testing.T) {
	c := LoginCookie("s3cret", loginCookieMaxAge, false)
	if c.Name != LoginCookieName || c.Value != "s3cret" {
		t.Fatalf("이름/값이 다르다: %q=%q", c.Name, c.Value)
	}
	if c.Path != "/" {
		t.Fatalf("Path 가 %q 다 — /events 와 /actions 둘 다 닿으려면 / 여야 한다", c.Path)
	}
	if !c.HttpOnly {
		t.Fatal("HttpOnly 가 꺼졌다 — JS 가 토큰을 읽을 수 있게 된다")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Fatal("SameSite 가 Strict 가 아니다 — 크로스사이트 요청에 쿠키가 실린다")
	}
	if c.MaxAge != loginCookieMaxAge {
		t.Fatalf("MaxAge 가 %d 다", c.MaxAge)
	}
	if c.Secure {
		t.Fatal("평문인데 Secure 가 켜졌다 — http:// 에서 쿠키가 저장조차 안 된다")
	}
}

// loginPost 는 폼 POST 하나를 만든다. 출처 헤더를 채워 JudgeScreenOrigin 을 통과시킨다.
func loginPost(t *testing.T, path string, form url.Values) *http.Request {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	req.RemoteAddr = "203.0.113.9:1"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	return req
}

func TestLoginSetsCookieAndRedirects(t *testing.T) {
	h := NewServer(nil, Options{Token: "s3cret"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, loginPost(t, "/login", url.Values{
		"token": {"s3cret"}, "next": {"/?project=kweiza"},
	}))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("상태가 %d 다 — 303 이어야 한다", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/?project=kweiza" {
		t.Fatalf("Location 이 %q 다", loc)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != LoginCookieName || cookies[0].Value != "s3cret" {
		t.Fatalf("쿠키를 안 구웠다: %+v", cookies)
	}
}

func TestLoginRejectsWrongTokenWithoutEcho(t *testing.T) {
	var gotView LoginView
	h := NewServer(nil, Options{
		Token: "s3cret",
		LoginScreen: func(w http.ResponseWriter, r *http.Request, v LoginView) {
			gotView = v
			w.WriteHeader(http.StatusUnauthorized)
		},
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, loginPost(t, "/login", url.Values{"token": {"wrong-guess"}}))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("상태가 %d 다 — 401 이어야 한다", rec.Code)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("틀린 토큰에 쿠키를 구웠다")
	}
	if !strings.Contains(gotView.Error, "일치하지 않는다") {
		t.Fatalf("사유가 %q 다", gotView.Error)
	}
	// ★ 시도한 값이 응답에 돌아오면 안 된다.
	if strings.Contains(gotView.Error, "wrong-guess") || strings.Contains(gotView.Next, "wrong-guess") {
		t.Fatal("시도한 토큰 값이 응답에 되비쳤다")
	}
}

func TestLoginRefusesCrossSite(t *testing.T) {
	h := NewServer(nil, Options{Token: "s3cret"})
	req := httptest.NewRequest("POST", "/login", strings.NewReader("token=s3cret"))
	req.RemoteAddr = "203.0.113.9:1"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("상태가 %d 다 — 403 이어야 한다", rec.Code)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("크로스사이트 로그인에 쿠키를 구웠다")
	}
}

// TestLoginWithoutServerTokenBakesNothing 은 인증이 꺼진 서버의 갈래다.
// ★ 여기서 쿠키를 구우면 나중에 토큰을 켰을 때 그 쿠키가 **틀린 자격증명**이 되어
// 폼이 아니라 거절을 만난다.
func TestLoginWithoutServerTokenBakesNothing(t *testing.T) {
	h := NewServer(nil, Options{Token: ""})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, loginPost(t, "/login", url.Values{"token": {"whatever"}}))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("상태가 %d 다 — 303 이어야 한다", rec.Code)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("인증이 꺼진 서버가 쿠키를 구웠다")
	}
}

func TestLogoutClearsCookie(t *testing.T) {
	h := NewServer(nil, Options{Token: "s3cret"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, loginPost(t, "/logout", nil))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("상태가 %d 다", rec.Code)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != LoginCookieName {
		t.Fatalf("쿠키를 안 지웠다: %+v", cookies)
	}
	if cookies[0].MaxAge >= 0 || cookies[0].Value != "" {
		t.Fatalf("쿠키가 안 지워졌다: MaxAge=%d Value=%q", cookies[0].MaxAge, cookies[0].Value)
	}
}

func TestLoginGetRedirectsHome(t *testing.T) {
	h := NewServer(nil, Options{Token: "s3cret"})
	req := httptest.NewRequest("GET", "/login", nil)
	req.RemoteAddr = "203.0.113.9:1"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("상태가 %d 다 — 303 이어야 한다", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Fatalf("Location 이 %q 다", loc)
	}
}

func TestLogoutRefusesCrossSite(t *testing.T) {
	h := NewServer(nil, Options{Token: "s3cret"})
	req := httptest.NewRequest("POST", "/logout", nil)
	req.RemoteAddr = "203.0.113.9:1"
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("상태가 %d 다 — 403 이어야 한다", rec.Code)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("크로스사이트 로그아웃에 쿠키를 구웠다")
	}
}

// TestLoginRetryAfterWrongTokenIsNotConflated 는 틀린 토큰 뒤의 재시도가 통하는지 본다.
//
// ★ 이 시험이 없어서 결함이 샜다. 로그인 시험이 전부 서버를 새로 세우고 쓰기를 한 번만
// 해서, 빈 키가 멱등 표의 한 슬롯을 공유한다는 사실이 드러날 자리가 없었다.
func TestLoginRetryAfterWrongTokenIsNotConflated(t *testing.T) {
	h := NewServer(nil, Options{
		Token: "s3cret",
		LoginScreen: func(w http.ResponseWriter, r *http.Request, v LoginView) {
			w.WriteHeader(http.StatusUnauthorized)
		},
	})

	// ① 틀린 토큰 — 401
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, loginPost(t, "/login", url.Values{"token": {"wrong"}}))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("첫 시도가 %d 다 — 401 이어야 한다", rec.Code)
	}

	// ② 같은 서버에 맞는 토큰 — 303 이어야 한다. 409 면 멱등 표가 둘을 접은 것이다.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, loginPost(t, "/login", url.Values{"token": {"s3cret"}}))
	if rec.Code == http.StatusConflict {
		t.Fatal("맞는 토큰이 409 로 거절됐다 — 빈 키가 멱등 표의 한 슬롯을 공유한다")
	}
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("둘째 시도가 %d 다 — 303 이어야 한다", rec.Code)
	}
	if len(rec.Result().Cookies()) != 1 {
		t.Fatal("둘째 시도가 쿠키를 안 구웠다")
	}
}

// TestLogoutAfterLoginIsNotConflated 는 경로가 다른 두 쓰기가 안 접히는지 본다.
func TestLogoutAfterLoginIsNotConflated(t *testing.T) {
	h := NewServer(nil, Options{Token: "s3cret"})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, loginPost(t, "/login", url.Values{"token": {"s3cret"}}))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("로그인이 %d 다", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, loginPost(t, "/logout", nil))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("로그아웃이 %d 다 — 로그인과 같은 슬롯에서 충돌했을 수 있다", rec.Code)
	}
}
