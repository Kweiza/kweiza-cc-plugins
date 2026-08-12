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
	// ★ **재시도 폼도 제출 가능해야 한다.** 폼을 채우는 자리가 둘인데(withAuth 와 여기)
	// 왕복 시험이 닿는 것은 앞엣것뿐이라, 이 자리가 Action 을 안 채우면 action="" 이 되고
	// 그러면 폼이 문서 URL 자신으로 제출된다 — 틀린 토큰을 한 번 친 사람만 무한 폼에 갇힌다.
	if gotView.Action != "./login" {
		t.Fatalf("재시도 폼의 action 이 %q 다 — /login 에서 뜬 폼이라 \"./login\" 이어야 한다", gotView.Action)
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

// TestLoginBodyIsCapped 는 무인증 표면이 본문 상한을 타는지 본다.
//
// ★ /login 은 세션 이전 경로라 withIdempotency 를 통째로 건너뛴다 — MaxBodyBytes 를
// 거는 자리가 거기뿐이라, 인증을 통과한 모든 REST 쓰기가 1MiB 인데 **아무나 칠 수 있는
// 이 경로 하나만 ParseForm 의 기본값 10MiB** 를 받고 있었다. 상한이 큰 쪽이 무인증인
// 것은 방향이 거꾸로다.
//
// 맞는 토큰을 실은 채로 상한을 넘긴다. 상한이 안 걸려 있으면 폼이 그대로 파싱돼
// **로그인이 성공한다** — 그래서 이 시험은 "거절됐나"가 아니라 "통과했나"로 갈린다.
func TestLoginBodyIsCapped(t *testing.T) {
	h := NewServer(nil, Options{Token: "s3cret", MaxBodyBytes: 1024})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, loginPost(t, "/login", url.Values{
		"token": {"s3cret"},
		"pad":   {strings.Repeat("x", 4096)},
	}))

	if rec.Code == http.StatusSeeOther {
		t.Fatal("상한(1KiB)의 네 배짜리 본문이 통과해 로그인이 성공했다")
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("상한을 넘은 본문에 쿠키를 구웠다")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("상태가 %d 다 — 폼을 못 읽었으니 401 이어야 한다", rec.Code)
	}
}

// TestLoginFormActionReachesLoginRoute 는 **렌더된 폼과 로그인 라우트를 잇는다.**
//
// ★ 정확히 이 시험이 없어서 결함이 여덟 태스크를 통과했다. 폼 시험(web)은 action
// 문자열이 있는지만 봤고, 로그인 시험(여기)은 언제나 `/login` 에 **직접** POST 했다 —
// 그 둘 사이의 **상대경로 해석**을 재는 자리가 아무 데도 없었다. 그래서 뿌리가 아닌
// 자리에서 뜬 폼이 `/actions/login` 같은 없는 곳을 가리켜도 전건이 초록이었다.
//
// 브라우저가 하는 일을 그대로 한다: 문서 URL 에 폼 action 을 붙여 해석하고(ResolveReference
// 가 RFC 3986 그대로다), **그 자리에 실제로 제출한다.** 제출이 303 이 아니면 사람은
// 토큰을 정확히 쳐도 같은 폼을 다시 본다.
func TestLoginFormActionReachesLoginRoute(t *testing.T) {
	cases := []struct{ method, doc string }{
		{"GET", "/"},
		{"GET", "/?project=kweiza"},
		{"POST", "/actions/reclaim"},  // 낡은 쿠키로 화면 폼을 제출한 자리
		{"GET", "/api/v1/items/next"}, // 주소창에 친 REST 경로
		{"GET", "/events"},            // 화면이 무는 SSE 별칭
		{"POST", "/actions/reclaim/"}, // 뒤 슬래시 — 깊이가 한 칸 더다
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.doc, func(t *testing.T) {
			var action string
			h := NewServer(nil, Options{
				Token: "s3cret",
				LoginScreen: func(w http.ResponseWriter, r *http.Request, v LoginView) {
					action = v.Action // login.gohtml 의 action="{{.Action}}" 이 찍는 값
					w.WriteHeader(http.StatusUnauthorized)
				},
			})
			req := httptest.NewRequest(c.method, c.doc, nil)
			req.RemoteAddr = "203.0.113.9:1"
			req.Header.Set("Accept", "text/html")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("폼이 안 떴다 — 상태가 %d 다", rec.Code)
			}
			if action == "" {
				t.Fatal("폼의 action 이 비었다")
			}

			base, err := url.Parse("http://fd.example" + c.doc)
			if err != nil {
				t.Fatalf("문서 URL 파싱 실패: %v", err)
			}
			ref, err := url.Parse(action)
			if err != nil {
				t.Fatalf("action 파싱 실패: %v", err)
			}
			target := base.ResolveReference(ref)
			if target.Path != "/login" {
				t.Fatalf("폼 action %q 가 %q 로 풀린다 — /login 이어야 한다", action, target.Path)
			}

			// 그리고 그 자리에 실제로 제출한다. 여기서 401 이 나면 무한 폼이다.
			rec = httptest.NewRecorder()
			h.ServeHTTP(rec, loginPost(t, target.Path, url.Values{"token": {"s3cret"}}))
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("폼이 가리킨 %q 에 제출했더니 %d 다 — 303 이어야 한다", target.Path, rec.Code)
			}
			if len(rec.Result().Cookies()) != 1 {
				t.Fatal("제출이 통했는데 쿠키를 안 구웠다")
			}
		})
	}
}

// TestLoginNextFoldsNonGET 은 쓰기 자리에서 뜬 폼의 돌아갈 자리를 본다.
//
// ★ 로그인 성공은 303 이고 303 은 **언제나 GET 으로 재생된다.** 그래서 POST 전용
// 경로를 Next 로 실으면 토큰이 맞아도 405/404 로 착지한다 — 로그인은 됐는데 화면이
// 깨진 것처럼 보이고, 원인이 Next 라는 것이 그 증상에서 안 보인다.
func TestLoginNextFoldsNonGET(t *testing.T) {
	var got LoginView
	h := NewServer(nil, Options{
		Token: "s3cret",
		LoginScreen: func(w http.ResponseWriter, r *http.Request, v LoginView) {
			got = v
			w.WriteHeader(http.StatusUnauthorized)
		},
	})
	req := httptest.NewRequest("POST", "/actions/reclaim?key=abc", nil)
	req.RemoteAddr = "203.0.113.9:1"
	req.Header.Set("Accept", "text/html")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got.Next != "/" {
		t.Fatalf("돌아갈 자리가 %q 다 — POST 전용 경로라 / 로 접혀야 한다", got.Next)
	}
}
