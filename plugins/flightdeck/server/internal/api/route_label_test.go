package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 게이트가 mux 에 닿기 전에 되돌아 나가는 응답도 **제 라우트 라벨**을 가져야 한다.
//
// r.Pattern 은 mux 가 매칭한 뒤에야 채워지므로, 멱등 게이트의 조기 반환 셋
// (재생 201 · 충돌 409 · 키 없음 400)이 전부 `<METHOD> unmatched` 로 새고 있었다.
// 이 레포의 로그 규율은 "route 는 메트릭 라벨과 **같은 문자열**"이고, 그 결선이 끊기면
// 아웃박스 재생 구간처럼 정의상 재시도가 몰리는 트래픽이 통째로 한 칸에 뭉친다.
func TestGateShortCircuitsKeepTheirRouteLabel(t *testing.T) {
	s := newServer(nil, Options{})
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("POST /api/v1/things", func(w http.ResponseWriter, r *http.Request) {})
	s.mux.HandleFunc("GET /api/v1/things/{id}", func(w http.ResponseWriter, r *http.Request) {})

	cases := []struct {
		name, method, path, want string
	}{
		{"매칭되는 POST", "POST", "/api/v1/things", "POST /api/v1/things"},
		{"경로 변수", "GET", "/api/v1/things/abc", "GET /api/v1/things/{id}"},
		// 표 밖: 정말로 없는 라우트는 여전히 unmatched 여야 한다.
		// 전부 무언가로 풀어 버리면 "없는 표면"이라는 사실이 지표에서 사라진다.
		{"진짜 없는 경로", "POST", "/api/v1/없는것", "POST unmatched"},
		{"메서드가 다르다", "DELETE", "/api/v1/things", "DELETE unmatched"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(c.method, c.path, strings.NewReader(""))
			// ── 대조 전제: mux 를 안 거쳤으므로 Pattern 이 비어 있다.
			//    차 있으면 이 시험은 resolveRoute 의 폴백 경로를 아예 안 탄다.
			if r.Pattern != "" {
				t.Fatalf("전제가 깨졌다 — Pattern 이 이미 %q 다", r.Pattern)
			}
			if got := s.resolveRoute(r); got != c.want {
				t.Errorf("resolveRoute = %q, 기대 %q", got, c.want)
			}
		})
	}
}

// 매칭된 뒤에는 r.Pattern 을 그대로 쓴다 — mux 를 두 번 돌리지 않는다.
func TestResolveRoutePrefersTheMatchedPattern(t *testing.T) {
	s := newServer(nil, Options{})
	s.mux = http.NewServeMux() // 비어 있다: 폴백이 돌면 unmatched 가 나온다
	r := httptest.NewRequest("POST", "/api/v1/things", nil)
	r.Pattern = "POST /api/v1/things"
	if got := s.resolveRoute(r); got != "POST /api/v1/things" {
		t.Errorf("매칭된 패턴을 안 쓴다: %q", got)
	}
}
