package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 화면의 404 는 **요청 경로를 되비추지 않는다.**
//
// ★ 왜 고쳤나: 같은 서버의 api 쪽 404(api.handleUnmatched)는 경로를 **일부러** 안 비춘다.
// 한 서버의 두 404 가 정반대 정책인 것 자체가 결함의 신호다 — 비대칭은
// "여기는 왜 다른가"에 답이 없는 상태이고, 그 상태는 한쪽이 틀렸다는 뜻이다.
// 지금은 text/plain + nosniff 라 XSS 가 아니지만, 이 응답이 HTML 로 바뀌거나
// 누가 이 문자열을 다른 표면으로 옮기는 날 그 자리가 반사형 노출 통로가 된다.
func TestDashboard404DoesNotReflectTheRequestPath(t *testing.T) {
	f := newFixture(t)

	cases := []string{
		"/없는경로",
		"/%3Cscript%3Ealert(1)%3C/script%3E",
		"/actions/reclaimXYZ",
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, p, nil)
			rec := httptest.NewRecorder()
			f.h.ServeHTTP(rec, req)

			// ── 대조 전제: 정말 404 경로를 탔는가 ──
			if rec.Code != http.StatusNotFound {
				t.Fatalf("전제가 깨졌다 — status = %d 다(404 를 기대했다): %s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			// ── 본 판정 ──
			if strings.Contains(body, req.URL.Path) {
				t.Errorf("요청 경로가 본문에 되비쳤다: %q\n%s", req.URL.Path, body)
			}
			// 왜 없는지는 여전히 말해야 한다 — 경로를 빼는 것과 침묵하는 것은 다르다.
			if !strings.Contains(body, "대시보드는 / 한 장이다") {
				t.Errorf("왜 없는지를 안 말했다: %s", body)
			}
			// 형식 방어는 그대로 살아 있어야 한다.
			if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q — nosniff 를 기대했다", got)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
				t.Errorf("Content-Type = %q — text/plain 을 기대했다", ct)
			}
		})
	}
}
