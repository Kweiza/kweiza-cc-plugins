package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestMuxRedirectLocationIsRelative 는 net/http.ServeMux **자신**이 내는 307 의 Location 이
// 상대화되는지 본다.
//
// ★ 이 축은 저장소 코드가 아니라 표준 라이브러리 안에서 난다. mux 는 경로를 정규화해야
// 할 때(`//` · `/a//b` · `/a/../` 같은 요청) RedirectHandler 로 리다이렉트를 내는데, 그
// Location 이 경로만 있는 절대경로다 — `grep 'http.Redirect('` 로는 원리적으로 안 보인다
// (withRelativeLocation 의 독 코멘트가 실측 표를 남겼다).
//
// 표는 이 브랜치의 라우트를 실측한 값이다 — GET // · POST /actions//reclaim ·
// GET /actions/../ · GET //?project=kweiza 넷 다 리다이렉트이고 Location 은 경로만 있는 절대경로다.
//
// ★ **상태 코드는 stdlib 이 바꾼다.** 2026-08-12 에는 `307` 이었고 Go 1.25.6 은 `301` 을 낸다.
// 그 값을 못박았던 앞선 판은 하위 넷이 전부 상태 단정에서 죽어 **이 시험의 진짜 주장이
// 한 번도 검증되지 않았다.** 아래 단정이 "3xx 인가"만 무는 이유가 그것이다.
func TestMuxRedirectLocationIsRelative(t *testing.T) {
	const prefix = "/dcp-dev-board"
	cases := []struct {
		name      string
		method    string
		path      string
		wantPath  string // 접두를 포함한 착지 경로
		wantQuery string
	}{
		{"이중 슬래시 뿌리", "GET", "//", "/", ""},
		{"화면 액션 안의 이중 슬래시", "POST", "/actions//reclaim", "/actions/reclaim", ""},
		{"점 마디로 뿌리에 돌아온다", "GET", "/actions/../", "/", ""},
		{"이중 슬래시 뿌리 + 질의", "GET", "//?project=kweiza", "/", "project=kweiza"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := NewServer(nil, Options{})
			req := httptest.NewRequest(c.method, c.path, nil)
			req.RemoteAddr = "203.0.113.9:1"
			if c.method != http.MethodGet {
				// ★ /actions/ 는 화면 쓰기 경로라 withScreenWrite 의 출처 대조를 먼저
				// 통과해야 mux 까지 닿는다 — CSRF 방어는 이 축과 무관하니 헤더로 채운다.
				req.Header.Set("Sec-Fetch-Site", "same-origin")
				req.Header.Set("Idempotency-Key", "test:mux-redirect")
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			// ★ **어느 3xx 인지는 stdlib 이 정한다 — 이 저장소의 계약이 아니다.**
			// 앞선 판은 `307` 을 못박았는데(2026-08-12 실측), Go 1.25.6 의 ServeMux 는 정규화
			// 리다이렉트를 `301` 로 낸다(`net/http/server.go` 의 해당 세 자리가 전부
			// `StatusMovedPermanently` 이고 307 은 아예 없다). 그래서 이 시험은 하위 넷이 **전부
			// 이 줄에서 Fatal 로 죽었고, 그 뒤의 진짜 단정(Location 상대화)은 한 번도 안 돌았다.**
			// 정작 그 기능은 멀쩡했다 — 상태만 맞춰 주니 나머지가 그대로 통과했다.
			//
			// 이 시험의 주장은 "mux 가 정규화 리다이렉트를 낼 때 그 Location 이 접두 안에
			// 착지하는가"다. 코드가 301 이냐 307 이냐는 그 주장의 일부가 아니고, 못박으면
			// stdlib 이 바꾸는 날 **주장이 검증되지 않은 채로 빨개진다** — 방금 그랬듯이.
			// 그래서 "리다이렉트인가"만 문다. 200 이 오면 여기서 잡힌다.
			if rec.Code < 300 || rec.Code > 399 {
				t.Fatalf("상태가 %d 다 — mux 의 정규화 리다이렉트(3xx)여야 한다\n%s",
					rec.Code, rec.Body.String())
			}
			loc := rec.Header().Get("Location")
			if loc == "" {
				t.Fatal("Location 이 비었다")
			}
			if strings.HasPrefix(loc, "/") {
				t.Fatalf("Location %q 가 여전히 절대경로다 — withRelativeLocation 이 안 먹었다", loc)
			}

			base, err := url.Parse("http://fd.example" + prefix + c.path)
			if err != nil {
				t.Fatalf("문서 URL 파싱 실패: %v", err)
			}
			ref, err := url.Parse(loc)
			if err != nil {
				t.Fatalf("Location %q 파싱 실패: %v", loc, err)
			}
			got := base.ResolveReference(ref)
			if want := prefix + c.wantPath; got.Path != want {
				t.Errorf("Location %q 가 %q 에 착지한다 — %q 여야 한다(접두 밖이면 프록시 배포가 깨진다)",
					loc, got.Path, want)
			}
			if got.RawQuery != c.wantQuery {
				t.Errorf("질의가 %q 다 — %q 여야 한다", got.RawQuery, c.wantQuery)
			}
		})
	}
}

// TestOwnHandlerRelativeLocationIsUnchanged 는 이 서버의 핸들러(seeOther)가 낸 상대 Location 이
// withRelativeLocation 을 지나며 안 망가지는지 본다.
//
// ★ 이 축이 없으면 미들웨어가 조용히 **모든** 리다이렉트를 "./" 로 뭉갤 수 있다 — "/" 로
// 시작하지 않는 값을 다시 judge.RelativeTo 에 넣으면 그 to 방어(".." 를 품으면 "./") 에
// 걸려 "../" 나 "./?..." 같은 값이 전부 "./" 로 접힌다. 로그인 성공의 next 가 정확히 그
// 모양(`./?project=...`)이라 이 시험이 그 회귀를 가장 먼저 잡는다.
func TestOwnHandlerRelativeLocationIsUnchanged(t *testing.T) {
	h := NewServer(nil, Options{Token: "s3cret"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, loginPost(t, "/login", url.Values{
		"token": {"s3cret"}, "next": {"/?project=kweiza"},
	}))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("상태가 %d 다 — 303 이어야 한다\n%s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "./?project=kweiza" {
		t.Fatalf("Location 이 %q 다 — withRelativeLocation 이 이미 상대인 값을 건드렸다", loc)
	}
}
