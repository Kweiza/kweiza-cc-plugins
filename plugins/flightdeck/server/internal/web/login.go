package web

import (
	"bytes"
	"net/http"
)

// 로그인 화면 — api 계층이 401 을 낼 때 부르는 렌더러다.
//
// ★ **이 패키지는 토큰을 모른다.** 대조도 쿠키도 api/login.go 의 일이고, 여기 있는 것은
// 폼 한 장뿐이다. 토큰이 두 자리에 살면 상수시간 비교와 대소문자 규칙이 두 벌이 되고,
// 그 둘은 반드시 표류한다.
//
// ★ **api 를 import 하지 않는다.** LoginView 를 여기서 다시 선언하는 이유가 그것이다 —
// 배선(cmd/fd/serve.go)이 두 타입을 잇는다. 화면 패키지가 REST 표면을 알기 시작하면
// 그 방향은 되돌리기 어렵다.

// LoginView 는 폼을 그리는 데 필요한 것 전부다. api.LoginView 와 필드가 같다.
type LoginView struct {
	Error string // 비면 첫 방문이다
	Next  string // 이미 검증된 값이다 — 여기서 다시 검증하지 않는다
	// Action 은 폼 action 에 그대로 찍을 **상대경로**다(`login` · `../login` · …).
	// ★ 이미 계산된 값이다 — 이 패키지는 깊이를 세지 않는다. 세면 그 셈이 api 와 두 벌이
	// 되고, 이 축의 표류는 "토큰을 정확히 쳐도 폼이 다시 뜬다"로 나타나 원인이 안 보인다.
	Action string
}

// LoginScreen 은 토큰 폼을 401 로 낸다.
//
// ★ 상태코드가 401 인 이유는 api/middleware.go 의 그 갈래에 적혀 있다 — 리다이렉트로
// 덮으면 인증 실패가 /metrics 의 unauthorized 축에서 사라진다.
//
// ★ 버퍼에 다 찍은 뒤에만 내보낸다. render() 와 같은 규율이다 — 스트림에 직접 찍으면
// 템플릿이 중간에 실패했을 때 반쪽 HTML 이 401 로 나가고, 폼 없는 로그인 화면이 된다.
func LoginScreen(w http.ResponseWriter, r *http.Request, v LoginView) {
	var buf bytes.Buffer
	if err := loginTpl.Execute(&buf, v); err != nil {
		http.Error(w, "로그인 화면을 렌더하지 못했다. 서버 로그의 원인 전문을 보라.",
			http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// ★ 캐시하면 로그아웃한 뒤 뒤로 가기로 폼이 되살아나고, 그 폼의 next 가 낡는다.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = buf.WriteTo(w)
}
