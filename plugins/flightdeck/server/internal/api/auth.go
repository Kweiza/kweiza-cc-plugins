package api

import (
	"crypto/subtle"
	"net"
	"net/url"
	"strings"
)

// 인증 게이트 — 사용자 1명, 토큰 1개, 역할 없음(설계 §6).
//
// ★ 이 파일의 판정은 전부 순수 함수다. 미들웨어 본문에 조건을 흩어 놓으면
// 시험이 그 조건의 **사본**을 단정하게 되고, 그러면 변이가 조용히 샌다.

// AuthDecision 은 인증 게이트 한 번의 판정이다.
//
// 불리언이 아니라 **사유**를 돌려준다. 사유가 없으면 "토큰이 틀렸다"와
// "헤더가 아예 없다"와 "서버에 토큰이 설정되지 않아 검사 자체를 안 했다"가
// 전부 같은 false 로 접히고, 그 셋은 처방이 완전히 다르다.
//
// Anonymous 는 **통과했으나 신원을 확인하지 않았다**는 뜻이다.
// OK 하나로 접으면 "토큰으로 확인됨"과 "무인증 통과"가 구분되지 않고,
// 그러면 조용한 무인증이 성립한다 — /healthz 가 그 사실을 알리는 근거가 이 축이다.
type AuthDecision struct {
	OK        bool
	Anonymous bool
	Reason    string // 항상 채운다
}

// AuthRequest 는 인증 판정에 필요한 요청 사실 전부다.
//
// ★ 구조체인 이유. 인자로 풀면 문자열 셋과 불리언 둘이 연속해서, 호출부에서 순서가
// 뒤집혀도 컴파일이 통과한다. ScreenPath 와 requireTokenOnLoopback 이 뒤바뀌면
// **REST 에 쿠키가 열리는데** 그 사고는 시험이 그 조합을 명시로 짚기 전에는 안 보인다.
type AuthRequest struct {
	RemoteAddr  string // net/http 가 준 "host:port"
	AuthHeader  string // Authorization 헤더 원문
	CookieToken string // 로그인 쿠키에서 읽은 값. 없으면 빈 문자열
	// ScreenPath 는 이 경로가 화면인가다 — **쿠키를 인정하는 유일한 조건**이다.
	// 판정하는 것은 JudgeScreenPath 이고, 미들웨어가 그 값을 여기 실어 준다.
	ScreenPath bool
}

// JudgeAuth 는 요청 하나가 게이트를 통과하는지 판정한다. 순수 함수다.
//
//   - token: 서버에 설정된 토큰. 빈 문자열이면 **인증이 꺼져 있다**
//   - requireTokenOnLoopback: 루프백 면제를 끌 것인가
//
// 순서가 중요하다.
//
//  1. **헤더가 있으면 헤더만 본다.** 쿠키는 쳐다보지 않는다 — 헤더를 실을 수 있는
//     클라이언트가 쿠키로 조용히 뒤집히면 무엇이 인증했는지가 요청마다 달라진다
//     (withScreenWrite 가 Idempotency-Key 에 대해 세운 규율과 같다).
//  2. **틀린 자격증명은 루프백이어도 거절한다** — 헤더든 쿠키든. 명시적으로 잘못된
//     자격증명을 면제로 덮으면 클라이언트의 토큰 오설정이 영영 안 보인다.
//  3. **쿠키를 든 비화면 요청은 아래로 흘려보낸다.** 여기서 거절로 끝내면 루프백 면제가
//     죽는다 — 쿠키를 가진 브라우저가 있는 머신에서 REST 를 치던 클라이언트가 통째로
//     막히고, 그 회귀는 쿠키를 안 굽던 시절의 시험으로는 안 잡힌다.
func JudgeAuth(req AuthRequest, token string, requireTokenOnLoopback bool) AuthDecision {
	header := strings.TrimSpace(req.AuthHeader)
	cookie := strings.TrimSpace(req.CookieToken)
	loopback := IsLoopback(req.RemoteAddr)

	if header != "" {
		scheme, value, ok := splitBearer(header)
		if !ok {
			return AuthDecision{Reason: "Authorization 헤더가 'Bearer <token>' 형식이 아니다"}
		}
		if !strings.EqualFold(scheme, "bearer") {
			return AuthDecision{Reason: "Authorization 인증 방식이 Bearer 가 아니다"}
		}
		if token == "" {
			return AuthDecision{OK: true, Anonymous: true,
				Reason: "서버에 토큰이 설정되지 않아 대조할 기준이 없다 — 무인증으로 통과했다"}
		}
		if subtle.ConstantTimeCompare([]byte(value), []byte(token)) == 1 {
			return AuthDecision{OK: true, Reason: "토큰이 일치한다"}
		}
		return AuthDecision{Reason: "토큰이 일치하지 않는다"}
	}

	if token == "" {
		return AuthDecision{OK: true, Anonymous: true,
			Reason: "서버에 토큰이 설정되지 않았다 — 전 요청이 무인증으로 통과한다"}
	}

	// 화면 경로의 쿠키. 브라우저가 자격증명을 내놓는 유일한 길이다.
	if cookie != "" && req.ScreenPath {
		if subtle.ConstantTimeCompare([]byte(cookie), []byte(token)) == 1 {
			return AuthDecision{OK: true, Reason: "화면 쿠키의 토큰이 일치한다"}
		}
		return AuthDecision{Reason: "화면 쿠키의 토큰이 일치하지 않는다 — 토큰이 바뀌었으면 다시 로그인해라"}
	}

	if loopback && !requireTokenOnLoopback {
		return AuthDecision{OK: true, Anonymous: true,
			Reason: "루프백 요청이라 토큰 없이 통과했다"}
	}
	if loopback {
		return AuthDecision{Reason: "루프백이지만 이 서버는 루프백에도 토큰을 요구한다"}
	}
	// ★ 쿠키를 들고 REST 를 친 경우가 여기로 온다. 사유를 "헤더가 없다"로 접으면
	// 브라우저에서 REST 를 부르는 도구를 만드는 사람이 원인을 영영 못 찾는다.
	if cookie != "" {
		return AuthDecision{Reason: "쿠키가 있지만 이 경로는 화면이 아니다 — " +
			"/api/v1 은 Authorization 헤더만 받는다"}
	}
	return AuthDecision{Reason: "Authorization 헤더가 없다"}
}

// splitBearer 는 "Bearer xxx" 를 방식과 값으로 가른다.
// 값에 공백이 더 있으면 형식 위반이다 — 조용히 앞부분만 쓰면 틀린 토큰이 맞는 것처럼 보일 수 있다.
func splitBearer(h string) (scheme, value string, ok bool) {
	parts := strings.Fields(h)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// IsLoopback 은 요청이 이 머신 안에서 왔는지 본다. 순수 함수다.
//
// 포트가 없는 형태(시험 하네스)와 IPv6 대괄호 형태를 둘 다 받는다.
// 해석에 실패하면 **루프백이 아니라고 본다** — 못 읽은 것을 면제로 접으면
// 그 순간 인증이 사라진다.
func IsLoopback(remoteAddr string) bool {
	addr := strings.TrimSpace(remoteAddr)
	if addr == "" {
		return false
	}
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// JudgeScreenPath 는 이 경로가 화면인가다 — **쿠키를 인정하는 유일한 조건**이다. 순수 함수다.
//
// ★ 이 함수가 /api/v1 에 대해 거짓을 내는 것이 화면 로그인 설계 전체의 안전을 지탱한다.
// REST 쓰기는 withScreenWrite 의 출처 대조를 **안 탄다**(JudgeScreenWrite 가 화면 경로만
// Screen=true 로 본다). 그래서 여기서 REST 를 참으로 내는 순간 REST 쓰기 전체의 CSRF 방어가
// 쿠키의 SameSite 하나로 줄어든다 — 그 방어는 브라우저 구현에 전적으로 기대는 것이다.
//
// ★ /events 가 참인 이유. 화면이 무는 짧은 별칭이고(api.go 의 그 라우트), EventSource 는
// 임의 헤더를 못 싣는다. 이 경로가 쿠키를 안 받으면 화면은 멀쩡히 뜨는데 영원히 안 갱신된다.
// /api/v1/events 는 REST 쪽이라 거짓이다.
func JudgeScreenPath(path string) bool {
	switch path {
	case "/", "/events":
		return true
	}
	return strings.HasPrefix(path, "/actions/")
}

// JudgeLoginScreen 은 이 401 에 HTML 폼을 낼 것인가다. 순수 함수다.
//
// ★ **메서드를 안 본다.** 걸러야 하는 것은 "쓰기"가 아니라 **HTML 을 못 읽는 소비자**이고,
// 그 축은 Accept 하나로 갈린다 — fd CLI 는 application/json, EventSource 는
// text/event-stream 을 보낸다. 메서드를 조건에 넣으면 쿠키 없이 화면 폼을 제출한 사람이
// 브라우저 앞에 앉아 JSON 401 을 들여다보게 된다. 그 사람은 폼을 읽을 수 있다.
//
// ★ */* 를 거짓으로 두는 이유. curl 의 기본값이라 참으로 두면 CLI 소비자가 HTML 을 받는다.
// 브라우저는 언제나 text/html 을 명시한다.
func JudgeLoginScreen(accept string) bool {
	return strings.Contains(strings.ToLower(accept), "text/html")
}

// LoginCookieName 은 화면 로그인 쿠키의 이름이다.
//
// ★ 이름을 내보내는 이유: 굽는 자리(handleLogin)와 읽는 자리(withAuth)와 지우는
// 자리(handleLogout)가 셋인데, 리터럴로 두면 오타 하나가 "로그인은 되는데 다음 요청에서
// 다시 폼"이라는 모양으로 나타난다 — 원인이 이름이라는 것이 그 증상에서 안 보인다.
const LoginCookieName = "fd_token"

// JudgePreSessionPath 는 이 경로가 **세션이 생기기 전**인가다. 순수 함수다.
//
// ★ 두 게이트가 이것을 함께 본다 — 인증(withAuth)과 멱등(withIdempotency). 그 둘이 같은
// 경로 집합을 면제하는 것은 우연이 아니라 **같은 한 가지 이유**다: 세션이 없으니 대조할
// 자격증명도 없고 `<session>:<seq>` 형식의 멱등 키도 만들 수 없다.
//
// ★ 로그인 경로가 인증 게이트 뒤에 있으면 **로그인하려면 이미 로그인돼 있어야 한다.**
//
// ★ **메서드를 안 본다.** GET /login 도 세션 이전이다.
//
// ★ /healthz 는 여기 **없다.** 그것은 loopbackSeen 관측보다도 앞이고, 그 순서에는 별도의
// 근거가 있다(withAuth 의 주석 — 컨테이너 헬스체크가 30초마다 쳐서 loopback_open 을
// 거짓으로 참으로 만든다). 두 면제를 한 함수로 접으면 그 순서가 흐려진다.
func JudgePreSessionPath(path string) bool {
	return path == "/login" || path == "/logout"
}

// JudgeLoginAction 은 이 경로에서 뜬 폼의 action 이 가리켜야 할 **상대경로**다. 순수 함수다.
//
// ★ 절대경로(`/login`)로 두면 리버스 프록시의 경로 접두 뒤에서 브라우저가 원점의 /login 을
// 찾아가고 프록시는 그 경로를 모른다 — 이 화면의 폼·SSE 가 전부 상대경로인 이유다
// (web.go 의 defaultSSEPath 주석). 그런데 상대경로는 **문서 URL 의 깊이**에 붙으므로
// 뿌리가 아닌 자리에서 뜬 폼은 /actions/login 같은 없는 곳으로 간다. 그 자리도 세션 이전
// 경로가 아니라 다시 401 이 나고, 사람은 토큰을 정확히 쳐도 같은 폼을 무한히 다시 본다.
// 401 폼은 POST /actions/* 에서도 뜨므로(JudgeLoginScreen 이 메서드를 안 본다) 이것은
// 가설이 아니라 그 설계가 명시로 든 시나리오다.
//
// 그래서 깊이만큼 거슬러 올라간다. 프록시 접두 안전성은 그대로 유지된다.
//
// ★ 깊이는 **마지막 슬래시까지의 슬래시 수**로 센다. RFC 3986 의 상대 해석이 문서 URL 의
// 마지막 마디를 버리고 남은 자리에 붙이기 때문이다(`/a/b` 의 기준은 `/a/`).
// 빈 마디(`//`)도 한 마디로 센다 — 해석 알고리즘이 그것을 마디로 세므로, 정규화해서
// 걷어내면 그만큼 덜 올라가 다시 없는 자리를 가리킨다.
//
// ★ 슬래시가 아예 없는 경로(빈 문자열 · OPTIONS 의 `*`)는 뿌리로 본다. 못 읽은 것을
// 깊이 0 으로 접는 쪽이 안전하다 — 그 경우의 최악은 "이 이상한 자리에서만 폼이 안 통한다"
// 이고, 반대로 과하게 올라가면 접두 **밖**으로 나가 프록시 배포 전체가 깨진다.
func JudgeLoginAction(path string) string {
	// 라우트 등록(api.go 의 "POST /login")의 마지막 마디다.
	const name = "login"
	slash := strings.LastIndex(path, "/")
	if slash < 0 {
		return name
	}
	// 문서 URL 의 디렉토리 부분. 뿌리(`/`)는 슬래시가 하나뿐이라 깊이가 0 이다.
	depth := strings.Count(path[:slash+1], "/") - 1
	if depth <= 0 {
		return name
	}
	return strings.Repeat("../", depth) + name
}

// JudgeNextFrom 은 **401 을 맞은 요청 자체**를 로그인 뒤 돌아갈 자리로 쓸 수 있는지 본다.
// 순수 함수다.
//
// ★ 로그인 성공은 303 이고 303 은 **언제나 GET 으로 재생된다.** 그래서 GET 이 아닌 요청의
// URI 를 그대로 실으면 토큰이 맞아도 405/404 에 착지한다 — POST 전용인 /actions/* 가
// 정확히 그 자리다. 로그인은 됐는데 화면이 깨진 것처럼 보이고, 원인이 Next 라는 것이
// 그 증상에서 안 보인다. 그래서 GET 이 아니면 뿌리로 접는다.
//
// ★ HEAD 도 접는다. 303 이 재생하는 것은 GET 하나뿐이라 "같은 요청으로 되돌아간다"가
// 참인 메서드도 GET 하나다.
//
// ★ 값 자체의 안전(오픈 리다이렉트)은 여전히 JudgeNext 가 본다. 이 함수는 그 앞에
// **메서드 축 하나**를 더할 뿐이고, 두 축을 한 함수로 접지 않는 이유는 폼이 제출한 next
// 를 검증하는 자리(handleLogin)에는 메서드 축이 없기 때문이다 — 거기는 언제나 POST 다.
func JudgeNextFrom(method, requestURI string) string {
	if !strings.EqualFold(strings.TrimSpace(method), "GET") {
		return "/"
	}
	return JudgeNext(requestURI)
}

// JudgeNext 는 로그인 뒤 돌아갈 자리를 고른다. 순수 함수다.
//
// ★ **이 서버 안의 경로만 남긴다.** 스킴이 있거나 // 로 시작하면 브라우저가 다른 호스트로
// 나가고, 그 순간 이 로그인 폼이 오픈 리다이렉트가 된다 — 공격자가 만든 주소로 사람을
// 보내면서 출발지가 이 대시보드였다는 사실을 이용한다.
//
// ★ 못 읽은 것은 통과시키지 않는다. 파싱 실패도 "/" 로 접는다 — IsLoopback 이 해석 실패를
// 루프백이 아니라고 보는 것과 같은 규율이다.
func JudgeNext(next string) string {
	next = strings.TrimSpace(next)
	// 역슬래시는 일부 브라우저가 / 로 정규화한다. \\evil.com 이 //evil.com 이 되는 길을 막는다.
	if next == "" || !strings.HasPrefix(next, "/") ||
		strings.HasPrefix(next, "//") || strings.Contains(next, `\`) {
		return "/"
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return "/"
	}
	return u.RequestURI()
}
