package api

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// 화면 쓰기가 게이트 사슬을 통과하게 하는 자리다.
//
// ## 왜 이 파일이 있나
//
// 화면은 mux **안**에 있어 chain() 의 게이트를 전부 탄다(api.go 의 ★ — 바깥에 붙였다가
// 토큰을 켠 배포에서 무인증 폐기가 실제로 성공한 사고가 있었다). 그 사슬의
// withIdempotency 는 모든 쓰기에 Idempotency-Key **헤더**를 요구하는데,
// 대시보드의 폼은 평범한 <form method="post"> 라 헤더를 실을 자리가 없다.
// 그래서 버튼 둘이 조립된 서버에서 400 이었다.
//
// 폼 action 의 **쿼리**에 키를 싣고 여기서 헤더로 올린다. 본문을 안 건드리므로
// withIdempotency 의 본문 읽기와 안 부딪힌다.
//
// ## ★ 대가와 그 상환
//
// 이 레포에는 CSRF 토큰·SameSite·Origin 검사가 **하나도 없었다.** 지금까지 그 역할을
// 우연히 대신한 것이 다름 아닌 "쓰기에 헤더가 필요하다"였다 — 외부 사이트의 폼은
// 헤더를 못 싣기 때문이다. 쿼리로 우회하는 순간 그 우연한 방어가 사라진다.
// 그래서 같은 자리에서 화면 액션 경로 한정 출처 대조를 함께 세운다.
// **없애는 것을 대체물 없이 없애지 않는다.**

// ScreenWriteVerdict 는 이 요청이 화면 쓰기 경로인지의 판정이다.
// **불리언이 아니라 사유를 담는다** — 사유가 없으면 "화면 쓰기가 아니다"와
// "읽기라 안 본다"가 같은 값으로 접히고, 라우트가 하나 늘 때 조용히 뒤엣것이 된다.
type ScreenWriteVerdict struct {
	Screen bool
	Reason string // 항상 채운다
}

// JudgeScreenWrite 는 이 요청이 대시보드 폼이 낸 쓰기인지 본다. 순수 함수다.
func JudgeScreenWrite(method, path string) ScreenWriteVerdict {
	if !isWrite(method) {
		return ScreenWriteVerdict{false, "읽기 요청이라 화면 쓰기 축을 안 본다"}
	}
	if !strings.HasPrefix(path, screenActionPrefix) {
		return ScreenWriteVerdict{false, "화면 액션 경로(" + screenActionPrefix + ")가 아니다 — REST 쓰기다"}
	}
	return ScreenWriteVerdict{true, "화면 액션 경로의 쓰기다"}
}

// screenWriteKeyParam 은 폼 action 이 키를 싣는 쿼리 이름이다.
const screenWriteKeyParam = "key"

// OriginVerdict 는 화면 쓰기의 출처 판정이다. 사유를 항상 채운다 —
// 거절 사유가 없으면 "다른 사이트가 냈다"와 "브라우저가 축을 안 실었다"가
// 같은 403 으로 접히고, 그러면 진짜 공격과 오래된 클라이언트가 구분되지 않는다.
type OriginVerdict struct {
	OK     bool
	Reason string
}

// JudgeScreenOrigin 은 화면 쓰기가 **이 화면 자신**에서 왔는지 본다. 순수 함수다.
//
// 규칙은 셋이다:
//   - Origin 이 있으면 그 호스트가 요청 호스트와 같아야 한다
//   - Sec-Fetch-Site 가 있으면 same-origin 이거나 none 이어야 한다
//   - 둘 다 없으면 거절한다 — 출처를 못 확인한 쓰기를 통과시키면 이 검사가 있으나 마나다
//
// 둘 다 있으면 **둘 다** 봐야 한다. 브라우저는 어긋나게 안 보내지만, 손으로 만든
// 요청은 한쪽만 맞춰 통과를 노릴 수 있다.
//
// ★ **스킴은 대조하지 않고 호스트만 본다.** 이 서버는 루프백 평문 HTTP 로 뜨고,
// 이 레포에는 X-Forwarded-Proto 를 신뢰하는 자리가 하나도 없다 — 스킴을 비교하려면
// 없는 신뢰를 새로 만들어야 하고, 그 신뢰가 곧 다음 우회로가 된다.
// CSRF 가 막으려는 것은 **다른 호스트**의 페이지이므로 호스트 대조가 그 축을 덮는다.
func JudgeScreenOrigin(origin, secFetchSite, host string) OriginVerdict {
	origin, secFetchSite = strings.TrimSpace(origin), strings.TrimSpace(secFetchSite)
	if origin == "" && secFetchSite == "" {
		return OriginVerdict{false, "Origin 도 Sec-Fetch-Site 도 없다 — 출처를 확인할 수 없는 화면 쓰기다"}
	}
	if origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" {
			return OriginVerdict{false, "Origin 을 읽을 수 없다: " + strconv.Quote(clip(origin, 64))}
		}
		if !strings.EqualFold(u.Host, host) {
			return OriginVerdict{false, "Origin 의 호스트가 " + strconv.Quote(clip(u.Host, 64)) +
				" 라 이 서버(" + strconv.Quote(clip(host, 64)) + ")가 아니다"}
		}
	}
	if secFetchSite != "" {
		// none = 주소창·북마크처럼 문서에서 시작하지 않은 요청이다. 다른 사이트가 만들 수 없다.
		if secFetchSite != "same-origin" && secFetchSite != "none" {
			return OriginVerdict{false, "Sec-Fetch-Site 가 " + strconv.Quote(clip(secFetchSite, 64)) +
				" 다 — 다른 사이트에서 시작한 요청이다"}
		}
	}
	return OriginVerdict{true, "출처가 이 화면 자신이다"}
}

// withScreenWrite 는 화면 폼의 출처를 대조하고, 통과한 것의 쿼리 키를 헤더로 올린다.
//
// chain 에서 **withIdempotency 앞**에 있어야 한다 — 뒤에 두면 이미 400 이 나간 뒤다.
//
// 순서가 계약이다: **출처를 먼저 본다.** 키를 먼저 올리면 거절된 요청도
// 멱등 표에 자리를 잡아, 외부가 키를 선점해 사람의 진짜 클릭을 접을 수 있다.
//
// 이미 헤더가 있으면 안 덮는다. 헤더를 실을 수 있는 클라이언트가 쿼리로 조용히
// 뒤집히면, 무엇이 키를 정했는지가 요청마다 달라진다.
func (s *server) withScreenWrite(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !JudgeScreenWrite(r.Method, r.URL.Path).Screen {
			next.ServeHTTP(w, r)
			return
		}
		if v := JudgeScreenOrigin(r.Header.Get("Origin"), r.Header.Get("Sec-Fetch-Site"), r.Host); !v.OK {
			s.writeError(w, r, Classified{
				Status: http.StatusForbidden, Code: "cross_site_write_refused",
				Message: "화면 쓰기의 출처가 이 화면이 아니다 — " + v.Reason,
				Guidance: "대시보드 화면에서 버튼을 눌러라. 자동화가 필요하면 " +
					"화면 액션이 아니라 REST 쓰기(/api/v1/...)를 토큰과 함께 써라.",
			})
			return
		}
		if strings.TrimSpace(r.Header.Get("Idempotency-Key")) == "" {
			if k := strings.TrimSpace(r.URL.Query().Get(screenWriteKeyParam)); k != "" {
				r.Header.Set("Idempotency-Key", k)
			}
		}
		next.ServeHTTP(w, r)
	})
}
