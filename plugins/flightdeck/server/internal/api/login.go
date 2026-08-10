package api

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// 화면 로그인 — 브라우저가 자격증명을 내놓는 유일한 길이다(설계 §6).
//
// ★ 이 파일이 존재하는 이유. JudgeAuth 는 Authorization 헤더를 읽는데 브라우저는 최초
// 요청에 임의 헤더를 못 싣는다. 응답이 WWW-Authenticate: Bearer 라 로그인 팝업도 안 뜬다
// (브라우저는 Basic 에만 띄운다). 컨테이너 배포에서는 루프백 면제도 구조적으로 안 걸린다 —
// 브리지 게이트웨이가 172.x 라 호스트 요청이 루프백으로 안 보인다. 즉 토큰을 켠 컨테이너
// 서버의 화면은 **브라우저로 도달 불가**였다.
//
// ★ 화면을 게이트 사슬 밖으로 빼는 처방은 기각이다. 그 사슬은 무인증 POST 로 항목이
// 실제 폐기된 사고의 처방이고 여전히 옳다(api.go 의 Fallback 주석).

// loginCookieMaxAge 는 로그인 쿠키의 수명(초)이다 — 10년.
//
// ★ 사실상 무기한인 이유: 사용자 1명·토큰 1개·역할 없음인 개인 조정 화면이다. 짧게 잡으면
// 보드를 볼 때마다 40자 토큰을 붙여 넣게 되고, 그러면 사람이 토큰을 어딘가 평문으로
// 적어 두게 된다 — 수명을 줄여 얻으려던 것을 그 습관이 되돌린다.
// 대신 로그아웃을 둔다. 내 머신이 아닌 곳에서 지우는 유일한 길이다.
const loginCookieMaxAge = 10 * 365 * 24 * 60 * 60

// JudgeCookieSecure 는 이 요청이 TLS 뒤인가다. 순수 함수다.
//
// ★ **무조건 켜면 안 된다.** Secure 쿠키는 http:// 에서 저장조차 안 되므로, 이 서버의
// 기본 배포(평문 루프백·컨테이너)에서 로그인이 원리적으로 불가능해진다 — 폼은 뜨는데
// 제출하면 다시 폼이 뜨고, 원인이 쿠키 속성이라는 것이 그 증상에서 안 보인다.
//
// ★ X-Forwarded-Proto 를 **여기서만** 신뢰한다. JudgeScreenOrigin 은 스킴을 일부러 안 보는데
// (없는 신뢰를 만들면 그것이 다음 우회로가 된다), 이 축의 실패 방향은 반대다: 헤더를 속여
// 얻는 것이 Secure 플래그 하나뿐이고 그것은 공격자에게 이득이 아니라 손해다.
func JudgeCookieSecure(tls bool, forwardedProto string) bool {
	if tls {
		return true
	}
	// 프록시 사슬은 "https, http" 처럼 쉼표로 온다. 첫째가 클라이언트에 가장 가깝다.
	first, _, _ := strings.Cut(forwardedProto, ",")
	return strings.EqualFold(strings.TrimSpace(first), "https")
}

// LoginCookie 는 쿠키 한 벌을 만든다. 순수 함수다.
//
// ★ 속성이 넷 다 필요하다. Path=/ 가 아니면 /events 와 /actions 중 한쪽이 안 닿고,
// HttpOnly 가 없으면 XSS 하나로 토큰이 새고, SameSite=Strict 가 없으면 크로스사이트
// 요청에 쿠키가 실린다. 시험이 넷을 개별로 단정하는 이유가 이것이다 — 하나가 빠져도
// 로그인은 멀쩡히 되므로 왕복 시험만으로는 안 잡힌다.
func LoginCookie(value string, maxAge int, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     LoginCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAge,
		Secure:   secure,
	}
}

// handleLogin 은 토큰 폼을 받아 쿠키를 굽는다.
//
// GET 은 "/" 로 보낸다 — 사용자가 주소창에 칠 수 있는 URL 이라, 없으면 화면의
// 404("대시보드는 / 한 장이다")가 나와 혼란스럽다. 인증이 안 됐으면 거기서 폼을 만난다.
func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	// ★ 출처 대조를 **직접** 부른다. withScreenWrite 는 게이트 사슬에서 withAuth 안쪽이라
	// 이 경로(게이트 앞에서 갈라진다)에는 안 걸린다.
	if v := JudgeScreenOrigin(r.Header.Get("Origin"), r.Header.Get("Sec-Fetch-Site"), r.Host); !v.OK {
		s.writeError(w, r, Classified{
			Status: http.StatusForbidden, Code: "cross_site_write_refused",
			Message:  "로그인의 출처가 이 화면이 아니다 — " + v.Reason,
			Guidance: "대시보드 화면의 폼에서 제출해라.",
		})
		return
	}
	if err := r.ParseForm(); err != nil {
		s.loginRefused(w, r, "폼을 읽을 수 없다", "/")
		return
	}
	next := JudgeNext(r.PostFormValue("next"))

	// ★ 인증이 꺼진 서버는 쿠키를 굽지 않는다. 구우면 나중에 토큰을 켰을 때 그 쿠키가
	// **틀린 자격증명**이 되어, 브라우저가 폼이 아니라 거절을 만난다.
	if s.opt.Token == "" {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	got := strings.TrimSpace(r.PostFormValue("token"))
	if got == "" {
		s.loginRefused(w, r, "토큰을 적어라", next)
		return
	}
	if subtle.ConstantTimeCompare([]byte(got), []byte(s.opt.Token)) != 1 {
		s.loginRefused(w, r, "토큰이 일치하지 않는다", next)
		return
	}
	http.SetCookie(w, LoginCookie(s.opt.Token, loginCookieMaxAge,
		JudgeCookieSecure(r.TLS != nil, r.Header.Get("X-Forwarded-Proto"))))
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// handleLogout 은 쿠키를 지운다.
//
// ★ 토큰 자체는 유효한 채로 남는다. 이것이 지우는 것은 **이 브라우저의 쿠키 하나**다.
// 토큰을 무효화하는 길은 FD_TOKEN 을 바꾸고 서버를 다시 띄우는 것뿐이고, 이 제품에
// 그것 말고 다른 길을 만들지 않는다(사용자 1명, 토큰 1개, 역할 없음 — 설계 §6).
func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if v := JudgeScreenOrigin(r.Header.Get("Origin"), r.Header.Get("Sec-Fetch-Site"), r.Host); !v.OK {
		s.writeError(w, r, Classified{
			Status: http.StatusForbidden, Code: "cross_site_write_refused",
			Message:  "로그아웃의 출처가 이 화면이 아니다 — " + v.Reason,
			Guidance: "대시보드 화면의 버튼을 눌러라.",
		})
		return
	}
	// MaxAge<0 이 삭제다. 값도 비운다 — 둘 중 하나만 하면 브라우저에 따라 남는다.
	http.SetCookie(w, LoginCookie("", -1,
		JudgeCookieSecure(r.TLS != nil, r.Header.Get("X-Forwarded-Proto"))))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// loginRefused 는 거절을 폼으로 되돌린다.
//
// ★ 상태코드는 401 이다. 200 으로 내면 "로그인 실패"가 성공과 같은 코드로 보이고,
// /metrics 의 unauthorized 축과 어긋난다.
//
// ★ **시도한 토큰 값을 안 싣는다.** LoginView 에 그 필드가 없는 것이 그 규율의 자리다.
func (s *server) loginRefused(w http.ResponseWriter, r *http.Request, why, next string) {
	s.met.incUnauthorized()
	if s.opt.LoginScreen != nil && JudgeLoginScreen(r.Header.Get("Accept")) {
		s.opt.LoginScreen(w, r, LoginView{Error: why, Next: next})
		return
	}
	w.Header().Set("WWW-Authenticate", `Bearer realm="flightdeck"`)
	s.writeError(w, r, Classified{
		Status: http.StatusUnauthorized, Code: "unauthorized",
		Message:  why,
		Guidance: UnauthorizedGuidance(s.loopbackReach()),
	})
}
