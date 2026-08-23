package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/api"
	"github.com/kweiza/flightdeck/internal/service"
	"github.com/kweiza/flightdeck/internal/store"
	"github.com/kweiza/flightdeck/internal/web"
)

// prefixStripper 는 경로 접두를 벗겨 뒤 서버로 넘긴다. nginx 의 그 배포를 본뜬다.
//
// ★ **Location 을 고쳐 쓰지 않는다.** nginx 가 경로만 있는 Location 을 안 고치는 것이
// 이 결함의 전제다 — 시험의 프록시가 그 일을 대신하면 재려는 것이 사라진다.
func prefixStripper(t *testing.T, prefix, upstreamURL string) http.Handler {
	t.Helper()
	target, err := url.Parse(upstreamURL)
	if err != nil {
		t.Fatalf("업스트림 URL 파싱 실패(%q): %v", upstreamURL, err)
	}
	rp := httputil.NewSingleHostReverseProxy(target)
	inner := rp.Director
	rp.Director = func(req *http.Request) {
		inner(req)
		req.URL.Path = strings.TrimPrefix(req.URL.Path, prefix)
		if req.URL.Path == "" {
			req.URL.Path = "/"
		}
	}
	return rp
}

// formAction 은 로그인 폼의 action 값을 꺼낸다. 템플릿의 그 한 줄(login.gohtml)이다.
func formAction(t *testing.T, html string) string {
	t.Helper()
	const marker = `<form method="post" action="`
	i := strings.Index(html, marker)
	if i < 0 {
		t.Fatalf("로그인 폼이 없다 — 본문 앞머리:\n%s", clipForTest(html))
	}
	rest := html[i+len(marker):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		t.Fatalf("action 이 안 닫혔다 — 본문 앞머리:\n%s", clipForTest(html))
	}
	return rest[:j]
}

func clipForTest(s string) string {
	if len(s) > 400 {
		return s[:400] + "…"
	}
	return s
}

// TestLoginRoundTripBehindPathPrefix 는 접두를 벗기는 프록시 뒤에서 로그인 왕복이
// **접두 안에** 착지하는지 본다.
//
// ★ 이 층이 재는 것은 값이 아니라 **왕복**이다. 폼 action 과 리다이렉트 Location 이
// 각각 맞아도 둘을 이어 붙였을 때 깨질 수 있다 — 앞선 판이 정확히 그렇게 깨졌다
// (폼은 떴는데 제출이 없는 자리로 가서 토큰을 정확히 쳐도 같은 폼이 무한히 떴다).
//
// ★ 배선을 재현하지 않고 serveAPIOptions 를 그대로 부른다. api.Options 를 손으로
// 만들면 LoginScreen 이 nil 이라 폼 대신 JSON 401 이 오고, 이 시험은 그 차이를
// "폼이 없다"로만 보게 된다.
func TestLoginRoundTripBehindPathPrefix(t *testing.T) {
	const prefix = "/dcp-dev-board"
	const token = "s3cret"

	quiet := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	dbp1 := filepath.Join(t.TempDir(), "fd.db")
	// ★ 적용은 기동에서 분리돼 있다(설계 §7 ①) — 열기 전에 올린다.
	if err := store.Migrate(context.Background(), dbp1, nil); err != nil {
		t.Fatalf("DB 적용 실패: %v", err)
	}
	st, err := store.Open(dbp1)
	if err != nil {
		t.Fatalf("DB 를 못 열었다: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := service.New(st, quiet)

	opt := serveAPIOptions(token, 0, quiet, false, nil, nil, false)
	// ★ **루프백 면제를 끈다.** httptest 서버는 언제나 127.0.0.1 이고 프록시도 같은
	// 머신이라, 끄지 않으면 업스트림이 보는 RemoteAddr 이 루프백이라 인증 게이트가
	// 통째로 건너뛰어진다 — 첫 방문이 401 이 아니라 **200** 이 되고, 그러면 이 시험은
	// 로그인을 한 번도 안 거치고 초록이 된다(실측). 재려는 것이 로그인 왕복인데
	// 로그인이 아예 안 일어나는 것이 가장 나쁜 초록이다.
	//
	// ★ 이 한 줄이 가리키는 더 큰 축이 있다. `serveAPIOptions` 는 이 필드를 한 번도
	// 세팅하지 않아서 **운영 배포의 면제는 항상 켜져 있고 끌 길이 없다.** 판정은
	// RemoteAddr 이므로 같은 호스트의 리버스 프록시 뒤에서는 실제 배포도 루프백으로
	// 보인다. 그 축은 이 항목의 범위가 아니라 별도로 다룬다 — 여기서는 이 시험이
	// **인증이 실제로 켜진 서버**를 재게 만드는 것이 전부다.
	opt.RequireTokenOnLoopback = true
	upstream := httptest.NewServer(buildHandler(svc, web.New(svc, web.WithLogger(quiet)), opt))
	t.Cleanup(upstream.Close)

	proxy := httptest.NewServer(prefixStripper(t, prefix, upstream.URL))
	t.Cleanup(proxy.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("쿠키 자를 못 만들었다: %v", err)
	}
	client := &http.Client{Jar: jar}

	// ① 접두 뒤 첫 방문 — 폼이 떠야 한다.
	//
	// ★ Accept 를 손으로 싣는다. 브라우저는 언제나 보내고, 이 서버는 그 헤더로
	// HTML 폼과 JSON 401 을 가른다(JudgeLoginScreen). 안 실으면 401 은 오는데
	// 폼이 아니라 JSON 이 와서 이 시험이 폼을 못 찾는다 — Go 의 http.Client 는
	// Accept 를 자동으로 안 붙이므로 브라우저를 흉내내려면 이 줄이 필요하다.
	docURL := proxy.URL + prefix + "/"
	req0, err := http.NewRequest(http.MethodGet, docURL, nil)
	if err != nil {
		t.Fatalf("첫 방문 요청을 못 만들었다: %v", err)
	}
	req0.Header.Set("Accept", "text/html")
	resp, err := client.Do(req0)
	if err != nil {
		t.Fatalf("첫 방문 실패: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("첫 방문이 %d 다 — 401 이어야 한다(토큰을 켠 서버다)", resp.StatusCode)
	}
	action := formAction(t, string(body))

	// ② 폼 action 을 문서 URL 기준으로 푼다. 브라우저가 하는 계산이다.
	base, err := url.Parse(docURL)
	if err != nil {
		t.Fatalf("문서 URL 파싱 실패: %v", err)
	}
	ref, err := url.Parse(action)
	if err != nil {
		t.Fatalf("action %q 파싱 실패: %v", action, err)
	}
	submitURL := base.ResolveReference(ref)
	if !strings.HasPrefix(submitURL.Path, prefix+"/") {
		t.Fatalf("폼 action %q 가 %q 를 가리킨다 — 접두 %q 밖이다", action, submitURL.Path, prefix)
	}

	// ③ 거기에 토큰을 제출한다. 브라우저는 same-origin POST 에 Origin 을 싣는다.
	form := url.Values{"token": {token}, "next": {"/"}}
	req, err := http.NewRequest(http.MethodPost, submitURL.String(), strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("제출 요청을 못 만들었다: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html")
	req.Header.Set("Origin", proxy.URL)
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	resp2, err := client.Do(req)
	if err != nil {
		t.Fatalf("제출 실패: %v", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()

	// ④ 클라이언트가 303 을 따라간 **최종 자리**가 접두 안이어야 한다.
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("최종 상태가 %d 다 — 200 이어야 한다\n%s", resp2.StatusCode, clipForTest(string(body2)))
	}
	final := resp2.Request.URL
	if final.Path != prefix+"/" {
		t.Fatalf("최종 착지가 %q 다 — %q 여야 한다. 접두 밖이면 로그인은 됐는데 화면을 못 본다",
			final.Path, prefix+"/")
	}
	if !strings.Contains(string(body2), "<form") {
		t.Fatalf("착지한 화면에 폼이 하나도 없다 — 대시보드가 아니다\n%s", clipForTest(string(body2)))
	}
	if strings.Contains(string(body2), `name="token"`) {
		t.Fatalf("착지한 화면이 로그인 폼이다 — 쿠키가 안 실렸거나 되돌아왔다\n%s", clipForTest(string(body2)))
	}

	// ⑤ 쿠키가 프록시 오리진에 남았는가.
	proxyURL, _ := url.Parse(proxy.URL)
	var found bool
	for _, c := range jar.Cookies(proxyURL) {
		if c.Name == api.LoginCookieName {
			found = true
		}
	}
	if !found {
		t.Fatal("로그인 쿠키가 자에 없다 — 다음 요청이 다시 401 이 된다")
	}
}
