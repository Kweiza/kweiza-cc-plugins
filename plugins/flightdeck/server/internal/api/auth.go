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

// JudgeAuth 는 요청 하나가 게이트를 통과하는지 판정한다. 순수 함수다.
//
//   - remoteAddr: net/http 가 준 "host:port"
//   - authHeader: Authorization 헤더 원문
//   - token: 서버에 설정된 토큰. 빈 문자열이면 **인증이 꺼져 있다**
//   - requireTokenOnLoopback: 루프백 면제를 끌 것인가
//
// 순서가 중요하다. **틀린 토큰은 루프백이어도 거절한다** — 명시적으로 잘못된
// 자격증명을 면제로 덮으면 클라이언트의 토큰 오설정이 영영 안 보인다.
func JudgeAuth(remoteAddr, authHeader, token string, requireTokenOnLoopback bool) AuthDecision {
	header := strings.TrimSpace(authHeader)
	loopback := IsLoopback(remoteAddr)

	if header != "" {
		scheme, value, ok := splitBearer(header)
		if !ok {
			return AuthDecision{Reason: "Authorization 헤더가 'Bearer <token>' 형식이 아니다"}
		}
		if !strings.EqualFold(scheme, "bearer") {
			return AuthDecision{Reason: "Authorization 인증 방식이 Bearer 가 아니다"}
		}
		if token == "" {
			// 검사할 기준이 없다. 헤더가 왔다는 이유로 통과시키되 확인은 안 됐다고 말한다.
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
	if loopback && !requireTokenOnLoopback {
		return AuthDecision{OK: true, Anonymous: true,
			Reason: "루프백 요청이라 토큰 없이 통과했다"}
	}
	if loopback {
		return AuthDecision{Reason: "루프백이지만 이 서버는 루프백에도 토큰을 요구한다"}
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
