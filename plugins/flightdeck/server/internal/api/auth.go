package api

import (
	"crypto/subtle"
	"net"
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
