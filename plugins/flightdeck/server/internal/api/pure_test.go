package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/buildinfo"
	"github.com/kweiza/flightdeck/internal/service"
	"github.com/kweiza/flightdeck/internal/store"
)

// 순수 함수 시험 — 표와 **표 밖 케이스**를 함께 둔다.
// 표만 두면 시험이 구현자가 상상한 입력만 보게 되고, 그 밖의 입력이 정확히
// 결함이 사는 자리다.

func TestJudgeAuth(t *testing.T) {
	const tok = "s3cret"
	cases := []struct {
		name       string
		req        AuthRequest
		token      string
		strictLoop bool
		wantOK     bool
		wantAnon   bool
		reasonHas  string
	}{
		{"토큰 일치", AuthRequest{RemoteAddr: "203.0.113.9:1", AuthHeader: "Bearer " + tok}, tok, false, true, false, "일치한다"},
		{"토큰 불일치", AuthRequest{RemoteAddr: "203.0.113.9:1", AuthHeader: "Bearer nope"}, tok, false, false, false, "일치하지 않는다"},
		{"헤더 없음 원격", AuthRequest{RemoteAddr: "203.0.113.9:1"}, tok, false, false, false, "헤더가 없다"},
		{"헤더 없음 루프백", AuthRequest{RemoteAddr: "127.0.0.1:1"}, tok, false, true, true, "루프백"},
		{"헤더 없음 IPv6 루프백", AuthRequest{RemoteAddr: "[::1]:1"}, tok, false, true, true, "루프백"},
		{"루프백 면제 끔", AuthRequest{RemoteAddr: "127.0.0.1:1"}, tok, true, false, false, "루프백에도 토큰을 요구한다"},
		{"서버 토큰 미설정", AuthRequest{RemoteAddr: "203.0.113.9:1"}, "", false, true, true, "설정되지 않았다"},
		{"형식 위반", AuthRequest{RemoteAddr: "127.0.0.1:1", AuthHeader: "Bearer a b"}, tok, false, false, false, "형식이 아니다"},
		{"방식이 Basic", AuthRequest{RemoteAddr: "127.0.0.1:1", AuthHeader: "Basic abcd"}, tok, false, false, false, "Bearer 가 아니다"},
		{"루프백인데 토큰이 틀림", AuthRequest{RemoteAddr: "127.0.0.1:1", AuthHeader: "Bearer wrong"}, tok, false, false, false, "일치하지 않는다"},
		{"토큰 미설정 + 헤더 있음", AuthRequest{RemoteAddr: "203.0.113.9:1", AuthHeader: "Bearer whatever"}, "", false, true, true, "대조할 기준이 없다"},
		{"RemoteAddr 이 이상함", AuthRequest{RemoteAddr: "@unix-socket"}, tok, false, false, false, "헤더가 없다"},
		{"소문자 bearer", AuthRequest{RemoteAddr: "203.0.113.9:1", AuthHeader: "bearer " + tok}, tok, false, true, false, "일치한다"},
		{"토큰 대소문자 다름", AuthRequest{RemoteAddr: "203.0.113.9:1", AuthHeader: "Bearer S3CRET"}, tok, false, false, false, "일치하지 않는다"},

		// ── 쿠키 축 ──────────────────────────────────────────────────────────
		// ① 화면 경로의 맞는 쿠키는 통과한다. 브라우저가 들어오는 유일한 길이다.
		{"쿠키 일치 + 화면", AuthRequest{RemoteAddr: "203.0.113.9:1", CookieToken: tok, ScreenPath: true},
			tok, false, true, false, "쿠키"},
		// ② ★ 같은 쿠키가 REST 에서는 통과하지 못한다. 이 줄이 설계 전체를 붙든다 —
		//    빠지면 REST 쓰기의 CSRF 방어가 SameSite 하나로 줄어든다.
		{"쿠키 일치 + REST", AuthRequest{RemoteAddr: "203.0.113.9:1", CookieToken: tok, ScreenPath: false},
			tok, false, false, false, "화면이 아니다"},
		// ③ 틀린 쿠키는 거절한다. 사유가 "헤더가 없다"로 접히면 옛 쿠키를 든 브라우저의
		//    처방("다시 로그인해라")이 안 나온다.
		{"쿠키 불일치 + 화면", AuthRequest{RemoteAddr: "203.0.113.9:1", CookieToken: "nope", ScreenPath: true},
			tok, false, false, false, "쿠키의 토큰이 일치하지 않는다"},
		// ④ 틀린 쿠키는 **루프백이어도** 거절한다. 틀린 헤더와 같은 규율이다.
		{"쿠키 불일치 + 루프백", AuthRequest{RemoteAddr: "127.0.0.1:1", CookieToken: "nope", ScreenPath: true},
			tok, false, false, false, "일치하지 않는다"},
		// ⑤ ★ 헤더가 이긴다. 헤더를 실을 수 있는 클라이언트가 쿠키로 조용히 뒤집히면
		//    무엇이 인증했는지가 요청마다 달라진다.
		{"헤더 틀림 + 쿠키 맞음", AuthRequest{RemoteAddr: "203.0.113.9:1", AuthHeader: "Bearer wrong", CookieToken: tok, ScreenPath: true},
			tok, false, false, false, "일치하지 않는다"},
		// ⑥ ★ 쿠키가 있어도 루프백 면제는 살아 있어야 한다. 쿠키 갈래를 면제보다 위에서
		//    거절로 끝내면 이 줄이 깨진다 — 즉 기존 배포의 루프백 클라이언트가 죽는다.
		{"비화면 쿠키 + 루프백 면제", AuthRequest{RemoteAddr: "127.0.0.1:1", CookieToken: tok, ScreenPath: false},
			tok, false, true, true, "루프백"},
		// ⑦ 서버에 토큰이 없으면 쿠키도 대조할 기준이 없다.
		{"토큰 미설정 + 쿠키", AuthRequest{RemoteAddr: "203.0.113.9:1", CookieToken: "whatever", ScreenPath: true},
			"", false, true, true, "설정되지 않았다"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := JudgeAuth(c.req, c.token, c.strictLoop)
			if got.OK != c.wantOK || got.Anonymous != c.wantAnon {
				t.Fatalf("판정이 다르다: OK=%v Anonymous=%v (기대 OK=%v Anonymous=%v) 사유=%q",
					got.OK, got.Anonymous, c.wantOK, c.wantAnon, got.Reason)
			}
			if got.Reason == "" {
				t.Fatal("사유가 비었다 — 사유 없는 판정은 '왜'에 답하지 못한다")
			}
			if !strings.Contains(got.Reason, c.reasonHas) {
				t.Fatalf("사유에 %q 가 없다: %q", c.reasonHas, got.Reason)
			}
		})
	}
}

func TestIsLoopback(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:8080": true,
		"127.5.5.5:1":    true,
		"[::1]:7420":     true,
		"::1":            true,
		"127.0.0.1":      true,
		"192.0.2.1:1234": false,
		"":               false,
		"localhost:7420": false, // 이름 해석을 하지 않는다 — 해석하면 DNS 가 인증의 일부가 된다
		"  ":             false,
		"999.1.1.1:1":    false,
	}
	for addr, want := range cases {
		if got := IsLoopback(addr); got != want {
			t.Errorf("IsLoopback(%q)=%v, 기대 %v", addr, got, want)
		}
	}
}

func TestRoutePattern(t *testing.T) {
	cases := []struct{ pattern, method, want string }{
		{"POST /api/v1/sessions", "POST", "POST /api/v1/sessions"},
		{"GET /api/v1/items/next", "GET", "GET /api/v1/items/next"},
		{"", "GET", "GET unmatched"},
		{"/", "DELETE", "DELETE unmatched"},
		{"", "", "unmatched"},
		// 표 밖: 못 맞춘 요청의 경로가 라벨에 새면 카디널리티가 요청 수만큼 는다.
		{"  ", "get", "GET unmatched"},
	}
	for _, c := range cases {
		if got := RoutePattern(c.pattern, c.method); got != c.want {
			t.Errorf("RoutePattern(%q,%q)=%q, 기대 %q", c.pattern, c.method, got, c.want)
		}
	}
}

func TestJudgeIdempotencyKey(t *testing.T) {
	cases := []struct {
		name, method, key string
		wantOK            bool
	}{
		{"GET 은 불요", http.MethodGet, "", true},
		{"HEAD 도 불요", http.MethodHead, "", true},
		{"POST 에 키 있음", http.MethodPost, "s1:1", true},
		{"POST 에 키 없음", http.MethodPost, "", false},
		{"PUT 에 키 없음", http.MethodPut, "  ", false},
		{"PATCH 에 키 없음", http.MethodPatch, "", false},
		{"DELETE 에 키 없음", http.MethodDelete, "", false},
		// 표 밖
		{"키에 공백", http.MethodPost, "s1 1", false},
		{"키에 제어문자", http.MethodPost, "s1\n1", false},
		{"키가 너무 김", http.MethodPost, strings.Repeat("k", 201), false},
		{"소문자 메서드", "post", "", false},
		{"키 앞뒤 공백은 허용", http.MethodPost, "  s1:1  ", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := JudgeIdempotencyKey(c.method, c.key)
			if got.OK != c.wantOK {
				t.Fatalf("OK=%v, 기대 %v (사유 %q)", got.OK, c.wantOK, got.Reason)
			}
			if got.Reason == "" {
				t.Fatal("사유가 비었다")
			}
		})
	}
}

func TestJudgeIdemMatch(t *testing.T) {
	m := JudgeIdemMatch("", "abc")
	if m.Replay || m.Conflict || m.Reason == "" {
		t.Fatalf("처음 보는 키는 새 처리여야 한다: %+v", m)
	}
	m = JudgeIdemMatch("abc", "abc")
	if !m.Replay || m.Conflict {
		t.Fatalf("같은 지문은 재생이어야 한다: %+v", m)
	}
	m = JudgeIdemMatch("abc", "xyz")
	if m.Replay || !m.Conflict {
		t.Fatalf("다른 지문은 충돌이어야 한다: %+v", m)
	}
}

func TestFingerprintSeparatesFields(t *testing.T) {
	// 표 밖: 구분자 없이 이어 붙이면 (method+path) 의 경계가 옮겨진 조합이 같은 지문이 된다.
	a := Fingerprint("POST", "/a/b", []byte("x"))
	b := Fingerprint("POST", "/a", []byte("/bx"))
	if a == b {
		t.Fatal("경로와 본문의 경계가 옮겨진 두 요청이 같은 지문이다")
	}
	if Fingerprint("post", "/a", nil) != Fingerprint("POST", "/a", nil) {
		t.Fatal("메서드 대소문자는 같은 요청이어야 한다")
	}
	if Fingerprint("POST", "/a", []byte("1")) == Fingerprint("POST", "/a", []byte("2")) {
		t.Fatal("본문이 다르면 지문이 달라야 한다")
	}
}

func TestClassifyError(t *testing.T) {
	at := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		msgHas     string
		internal   bool
	}{
		{"거절", &service.RefusedError{What: "finish", Reason: "본문이 비었다", Guidance: "넷을 적어라"},
			http.StatusBadRequest, "refused", "본문이 비었다", false},
		{"남이 쥔 항목", &store.ClaimHeldError{ItemID: "t5-x", Holder: "S1", At: at, Reason: "이미 남이 잡았다"},
			http.StatusConflict, "claim_held", "S1", false},
		{"선점 거절", &store.ClaimRefusedError{ItemID: "t5-x", Reason: "이미 끝난 항목이다"},
			http.StatusConflict, "claim_refused", "이미 끝난 항목이다", false},
		{"종료된 항목", &store.ItemClosedError{ItemID: "t5-x", State: "dropped", Want: "open"},
			http.StatusConflict, "item_closed", "dropped", false},
		{"자원 점유", &store.ResourceHeldError{Resource: "staging", Holder: store.Holder{SessionID: "S2"}, AcquiredAt: at},
			http.StatusConflict, "resource_held", "staging", false},
		{"없다", fmt.Errorf("항목 조회: %w", store.ErrNotFound),
			http.StatusNotFound, "not_found", "없다", false},
		{"모르는 오류", errors.New("SQL logic error near \"SELECT\": /data/fd.db"),
			http.StatusInternalServerError, "internal", "서버 내부 오류", true},
		// 표 밖: nil 을 넣어도 죽지 않는다(호출부가 실수해도 500 을 흘리지 않는다).
		{"nil", nil, http.StatusOK, "ok", "오류가 없다", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ClassifyError(c.err)
			if got.Status != c.wantStatus || got.Code != c.wantCode {
				t.Fatalf("status=%d code=%q, 기대 %d %q", got.Status, got.Code, c.wantStatus, c.wantCode)
			}
			if !strings.Contains(got.Message, c.msgHas) {
				t.Fatalf("문구에 %q 가 없다: %q", c.msgHas, got.Message)
			}
			if got.Internal != c.internal {
				t.Fatalf("Internal=%v, 기대 %v", got.Internal, c.internal)
			}
		})
	}
}

// ★ 이 시험이 이 파일의 핵심이다: **모르는 오류의 문구는 입력과 무관해야 한다.**
// err.Error() 를 한 글자라도 옮기면 여기서 걸린다.
func TestClassifyErrorNeverEchoesUnknownCause(t *testing.T) {
	secret := "/home/user/.flightdeck/fd.db"
	c := ClassifyError(fmt.Errorf("unable to open database file: %s (sqlite3.Error)", secret))
	if strings.Contains(c.Message+c.Guidance, secret) {
		t.Fatalf("응답 문구에 하류 문자열이 새어 나왔다: %q", c.Message+c.Guidance)
	}
	if strings.Contains(strings.ToLower(c.Message+c.Guidance), "sqlite") {
		t.Fatalf("응답 문구에 드라이버 이름이 새어 나왔다: %q", c.Message)
	}
}

func TestRenderMetrics(t *testing.T) {
	out := RenderMetrics(MetricsSnapshot{
		Requests: map[RequestKey]uint64{
			{Route: "POST /api/v1/items", Status: 201}:     2,
			{Route: "GET /api/v1/items/next", Status: 200}: 1,
		},
		DurationSum:   map[string]float64{"POST /api/v1/items": 0.5},
		DurationCount: map[string]uint64{"POST /api/v1/items": 2},
		Unauthorized:  3,
		RateLimited:   4,
		SSESubs:       2,
	})
	for _, want := range []string{
		`flightdeck_requests_total{route="POST /api/v1/items",status="201"} 2`,
		`flightdeck_requests_total{route="GET /api/v1/items/next",status="200"} 1`,
		`flightdeck_request_duration_seconds_count{route="POST /api/v1/items"} 2`,
		"flightdeck_unauthorized_total 3",
		"flightdeck_rate_limited_total 4",
		"flightdeck_sse_subscribers 2",
		"# TYPE flightdeck_requests_total counter",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("지표 문서에 %q 가 없다:\n%s", want, out)
		}
	}
	// 표 밖: 같은 스냅숏은 항상 같은 문서여야 한다(맵 순회 순서가 새면 안 된다).
	for i := 0; i < 20; i++ {
		if again := RenderMetrics(MetricsSnapshot{
			Requests: map[RequestKey]uint64{
				{Route: "b", Status: 200}: 1, {Route: "a", Status: 500}: 1, {Route: "a", Status: 200}: 1,
			},
			DurationSum: map[string]float64{}, DurationCount: map[string]uint64{},
		}); again != RenderMetrics(MetricsSnapshot{
			Requests: map[RequestKey]uint64{
				{Route: "a", Status: 200}: 1, {Route: "a", Status: 500}: 1, {Route: "b", Status: 200}: 1,
			},
			DurationSum: map[string]float64{}, DurationCount: map[string]uint64{},
		}) {
			t.Fatal("같은 상태에서 서로 다른 지표 문서가 나왔다 — 순서가 고정되지 않았다")
		}
	}
}

func TestEscapeLabel(t *testing.T) {
	if got := escapeLabel(`a"b\c` + "\n"); got != `a\"b\\c\n` {
		t.Fatalf("이스케이프가 다르다: %q", got)
	}
}

func TestEncodeSSE(t *testing.T) {
	frame, err := EncodeSSE("01ABC", Event{
		Kind: "judgment.note", Project: "cp", SessionID: "S1",
		At:     time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC),
		Detail: map[string]any{"bytes": 12},
	})
	if err != nil {
		t.Fatalf("직렬화 실패: %v", err)
	}
	s := string(frame)
	if !strings.HasPrefix(s, "id: 01ABC\ndata: {") {
		t.Fatalf("프레임 머리가 다르다:\n%s", s)
	}
	if !strings.HasSuffix(s, "\n\n") {
		t.Fatalf("프레임이 빈 줄로 안 끝난다:\n%q", s)
	}
	// ★ 표 밖: 본문에 개행이 있어도 프레임이 쪼개지면 안 된다.
	frame, err = EncodeSSE("id\n주입", Event{Kind: "x\ny", At: time.Now(),
		Detail: map[string]any{"body": "첫 줄\n\ndata: 가짜\n\n"}})
	if err != nil {
		t.Fatalf("직렬화 실패: %v", err)
	}
	if n := strings.Count(strings.TrimSuffix(string(frame), "\n\n"), "\n\n"); n != 0 {
		t.Fatalf("프레임 안에 빈 줄이 생겼다(주입 통로다):\n%q", string(frame))
	}
	if lines := strings.Split(strings.TrimSuffix(string(frame), "\n\n"), "\n"); len(lines) != 2 {
		// id·data 둘뿐이다 — event 줄을 안 찍는다(브라우저 onmessage 가 발화해야 하므로)
		t.Fatalf("프레임 줄 수가 2가 아니다: %q", string(frame))
	}
}

func TestValidateWorkspacePath(t *testing.T) {
	if err := ValidateWorkspacePath("/abs/path"); err != nil {
		t.Fatalf("절대경로가 거절됐다: %v", err)
	}
	for _, bad := range []string{"", "  ", "rel/path", "./x"} {
		if err := ValidateWorkspacePath(bad); err == nil {
			t.Fatalf("%q 가 통과했다 — 상대경로는 서버와 세션이 서로 다른 곳을 가리킨다", bad)
		}
	}
}

func TestAuthNoticeAndHealthzScrub(t *testing.T) {
	if !strings.Contains(AuthNotice(false, LoopbackReach{Configured: true, Observed: true}), "무인증") {
		t.Fatal("토큰 미설정을 알리지 않는다 — 조용한 무인증은 안 된다")
	}
	if !strings.Contains(AuthNotice(true, LoopbackReach{Configured: true, Observed: true}), "루프백") {
		t.Fatal("루프백 면제 사실이 없다")
	}
	if strings.Contains(AuthNotice(true, LoopbackReach{Configured: false}), "루프백만") {
		t.Fatal("면제가 꺼졌는데 켜진 것처럼 말한다")
	}

	body := HealthzOf(service.Health{
		OK: false, APIVersion: "1", DBOK: false,
		DBPath:    "/home/user/.flightdeck/fd.db",
		DBError:   "unable to open database file /home/user/.flightdeck/fd.db",
		DiskError: "statfs /home/user/.flightdeck: no such file",
	}, true, LoopbackReach{Configured: true, Observed: true}, buildinfo.Coord{}, SelfUpdateStatus{}, LedgerBackupStatus{})
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("직렬화 실패: %v", err)
	}
	if strings.Contains(string(raw), "/home/user") {
		t.Fatalf("/healthz 본문에 서버 파일 경로가 새어 나왔다: %s", raw)
	}
	if !strings.Contains(string(raw), "db_ok") {
		t.Fatalf("db_ok 축이 없다: %s", raw)
	}
	if body.DBError == "" {
		t.Fatal("DB 가 죽었는데 그 사실이 응답에 없다 — 침묵하면 배너가 정상으로 읽힌다")
	}
}

// 루프백 문장은 **설정이 아니라 도달 가능성**을 말해야 한다.
//
// ★ 실물 사고가 이 시험의 근거다(2026-08-05). 서버를 컨테이너로 띄우면 호스트에서 온
// 요청의 원격 주소가 브리지 게이트웨이(172.x)라 IsLoopback 이 false 다 — **면제를 실제로
// 받는 클라이언트가 0인데** /healthz 는 "루프백 요청만 토큰 없이 통과한다"를 그대로 냈다.
// 운영자는 그것을 믿고 클라이언트에 토큰을 안 준 채 전환했고 첫 쓰기에서 401 을 만났다.
// 그 대응이 **인증을 통째로 끄는 것**이었다(token → token.off-2026-08-05). 그것이 이 문장의 비용이다.
func TestAuthNoticeSaysLoopbackIsConfiguredButUnreached(t *testing.T) {
	n := AuthNotice(true, LoopbackReach{Configured: true, Observed: false})
	if !strings.Contains(n, "도달") {
		t.Fatalf("면제가 설정만 열려 있고 아무도 그것을 못 받는다는 사실이 없다: %q", n)
	}
	if strings.Contains(n, "루프백 요청만 토큰 없이 통과한다") {
		t.Fatalf("면제를 받는 클라이언트가 0인데 통과한다고 단정한다: %q", n)
	}
}

// 안 닿는 **사유를 아는데 말하지 않는 것**은 절반만 고친 것이다.
//
// 설정과 관측이 어긋난다는 사실만 내면 운영자는 "왜?"를 스스로 파야 하고, 그 답
// (도커 브리지가 소스 주소를 갈아 끼운다)은 서버가 이미 알고 있다 — self_update 축이
// 같은 신호(/.dockerenv)를 이미 본다.
func TestAuthNoticeNamesContainerWhenLoopbackUnreached(t *testing.T) {
	n := AuthNotice(true, LoopbackReach{Configured: true, Observed: false, InContainer: true})
	if !strings.Contains(n, "컨테이너") {
		t.Fatalf("안 닿는 사유를 아는데 안 말한다: %q", n)
	}
	if !strings.Contains(n, "토큰") {
		t.Fatalf("그래서 무엇을 해야 하는지가 없다 — 클라이언트도 토큰이 필요하다: %q", n)
	}
}

// 면제가 **꺼져 있을 때**는 관측을 물을 이유가 없다. 그 갈래를 관측으로 오염시키면
// "면제를 껐다"와 "면제는 켰는데 아무도 못 받는다"가 화면에서 같아진다 — 처방이 정반대다.
func TestAuthNoticeKeepsTheDisabledCaseDistinct(t *testing.T) {
	off := AuthNotice(true, LoopbackReach{Configured: false, Observed: false, InContainer: true})
	if strings.Contains(off, "도달") || strings.Contains(off, "컨테이너") {
		t.Fatalf("면제를 끈 서버가 '안 닿는다'고 말한다 — 끈 것은 결함이 아니다: %q", off)
	}
	if !strings.Contains(off, "루프백에도 토큰이 필요하다") {
		t.Fatalf("면제가 꺼졌다는 사실이 사라졌다: %q", off)
	}
}

// 도달-0 갈래는 **재는 법까지** 줘야 한다.
//
// ★ 이 문장을 읽은 사람이 할 가장 자연스러운 확인이 루프백에서 /healthz 를 쳐 보는
// 것인데, 그 요청은 인증 게이트 앞에서 되돌아 나가 관측을 안 남긴다. 절차를 안 주면
// **확인하려는 시도가 언제나 "아직 없다"를 확증한다** — 멀쩡한 서버를 결함으로 읽는다.
func TestAuthNoticeTellsHowToMeasureReach(t *testing.T) {
	n := AuthNotice(true, LoopbackReach{Configured: true, Observed: false})
	if !strings.Contains(n, "/healthz") {
		t.Fatalf("재는 법이 없다 — 이 값이 /healthz 로는 안 움직인다는 사실이 어디에도 안 나온다: %q", n)
	}
	if !strings.Contains(n, "API 요청") {
		t.Fatalf("무엇을 하면 재지는지가 없다: %q", n)
	}
	// 사유를 아는 갈래(컨테이너)는 제 사유를 말한다 — 절차로 덮으면 진단이 흐려진다.
	c := AuthNotice(true, LoopbackReach{Configured: true, Observed: false, InContainer: true})
	if !strings.Contains(c, "컨테이너") {
		t.Fatalf("사유를 아는 갈래가 사유를 잃었다: %q", c)
	}
}

// 면제가 **닿는** 서버가 내는 401 처방도 거짓을 말하면 안 된다.
//
// ★ 이 갈래만 오래 "그 **설정**을 알린다"로 남아 있었다 — 인접 갈래 둘은 관측 어법인데.
// 그리고 이 문구를 읽는 사람은 **방금 401 을 맞았다.** 루프백에서 왔다면 원인은 면제가
// 아니라 토큰 값이다(JudgeAuth 는 헤더가 있으면 먼저 대조하고 불일치는 루프백이어도
// 거절한다 — auth.go 의 순서 계약). 면제의 범위를 안 적으면 그 사람이 자기가 방금 받은
// 401 을 설명하지 못하고, 그것이 2026-08-05 사고에서 세션이 배선을 안 의심한 이유다.
func TestUnauthorizedGuidanceScopesTheExemptionWhenLoopbackReaches(t *testing.T) {
	g := UnauthorizedGuidance(LoopbackReach{Configured: true, Observed: true})
	if strings.Contains(g, "설정을 알린다") {
		t.Fatalf("관측 축인데 설정을 알린다고 말한다 — 인접 갈래 둘과 어법이 갈린다: %q", g)
	}
	if !strings.Contains(g, "틀린 토큰은 루프백이어도 거절한다") {
		t.Fatalf("면제의 범위가 없다 — 401 을 맞은 루프백 클라이언트가 자기 401 을 설명할 수 없다: %q", g)
	}
	if !strings.Contains(g, "Bearer") {
		t.Fatalf("거짓을 지우면서 처방까지 지웠다: %q", g)
	}
}

// loopback_open 은 **관측**이고, 설정값은 따로 남는다.
//
// 둘을 한 필드에 접으면 "왜 안 닿는가"를 물을 수 없다 — 설정이 꺼진 것인지
// 켰는데 도달이 없는 것인지가 같은 false 로 보인다.
func TestHealthzLoopbackOpenIsObservedNotConfigured(t *testing.T) {
	body := HealthzOf(service.Health{OK: true, APIVersion: "1", DBOK: true},
		true, LoopbackReach{Configured: true, Observed: false}, buildinfo.Coord{}, SelfUpdateStatus{}, LedgerBackupStatus{})
	if body.Auth.LoopbackOpen {
		t.Fatal("도달한 루프백 요청이 없는데 loopback_open 이 참이다 — 설정을 옮기기만 하면 그것이 거짓 광고다")
	}
	if !body.Auth.LoopbackConfigured {
		t.Fatal("설정값이 사라졌다 — 관측과 설정이 둘 다 있어야 '왜 안 닿는가'를 물을 수 있다")
	}

	seen := HealthzOf(service.Health{OK: true, APIVersion: "1", DBOK: true},
		true, LoopbackReach{Configured: true, Observed: true}, buildinfo.Coord{}, SelfUpdateStatus{}, LedgerBackupStatus{})
	if !seen.Auth.LoopbackOpen {
		t.Fatal("루프백으로 도달한 요청이 있는데 loopback_open 이 거짓이다 — 관측을 안 읽는다")
	}
}

// ★ 문자열 포함만 보면 태그 오타(예: outcome → outcomeX)가 안 잡힌다 — 그 오타가 나도
// 값은 여전히 응답 어딘가에 있으므로 strings.Contains(raw, "refused") 는 그대로 통과한다.
// 그래서 **키:값 쌍**으로 단언한다. `"outcome":"refused"` 는 태그가 정확히 outcome 일 때만 나온다.
func TestHealthzCarriesSelfUpdateRefusal(t *testing.T) {
	at := time.Date(2026, 8, 5, 0, 31, 2, 0, time.UTC)
	body := HealthzOf(service.Health{OK: true, APIVersion: "1", DBOK: true},
		false, LoopbackReach{Configured: true, Observed: true}, buildinfo.Coord{}, SelfUpdateStatus{
			Watching: true, LastAt: &at,
			From: "07e5df4", To: "1d044b2",
			Outcome: "refused", Detail: "selfcheck exit 1 — 증분 계획이 거절된다",
		}, LedgerBackupStatus{})
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("직렬화 실패: %v", err)
	}
	for _, want := range []string{
		`"self_update":`,
		`"watching":true`,
		`"outcome":"refused"`,
		`"from":"07e5df4"`,
		`"to":"1d044b2"`,
		`"detail":`,
		`"last_at":`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("%s 가 응답에 없다: %s", want, raw)
		}
	}
}

// ★ **보고는 있는데 못 재는** 상태가 선을 넘어가야 한다. 이것이 안 실리면 클라이언트는
// watching=true 만 보고 "따라가는 중"이라 찍는다 — 지워진 바이너리를 감시하는 서버가
// 화면에서는 정상으로 보인다. 여기서도 키:값 쌍으로 단언한다(태그 오타를 잡으려면 그래야 한다).
func TestHealthzCarriesTheStalledWatcher(t *testing.T) {
	body := HealthzOf(service.Health{OK: true, APIVersion: "1", DBOK: true},
		false, LoopbackReach{Configured: true, Observed: true}, buildinfo.Coord{}, SelfUpdateStatus{
			Watching: true,
			Stalled:  "실행 파일을 못 쟀다: no such file or directory",
		}, LedgerBackupStatus{})
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("직렬화 실패: %v", err)
	}
	if !strings.Contains(string(raw), `"stalled":"실행 파일을 못 쟀다`) {
		t.Fatalf("막힌 사실이 선을 안 넘었다: %s", raw)
	}
	// 아무 일도 없을 때는 안 나가야 한다 — 빈 축이 매번 실리면 읽는 쪽이 그 키를 무시하게 된다.
	quiet, err := json.Marshal(HealthzOf(service.Health{OK: true, APIVersion: "1", DBOK: true},
		false, LoopbackReach{Configured: true, Observed: true}, buildinfo.Coord{}, SelfUpdateStatus{Watching: true}, LedgerBackupStatus{}))
	if err != nil {
		t.Fatalf("직렬화 실패: %v", err)
	}
	if strings.Contains(string(quiet), `"stalled"`) {
		t.Fatalf("막히지 않았는데 stalled 가 실렸다: %s", quiet)
	}
}

// ★ **못 덮는 갈래도 선을 넘어야 한다.** 이것이 안 실리면 클라이언트는 watching=true 만
// 보고 "따라가는 중"이라 찍는데, 그 서버는 플러그인 버전이 올라도 영영 안 바뀐다 —
// 침묵이 아니라 **틀린 안심**이다. Stalled 와 **다른 키**인 것이 계약이다: 접으면
// "지금 못 잰다"(회복된다)와 "영영 안 바뀔 자리를 잰다"(회복이 없다)가 같은 값이 된다.
func TestHealthzCarriesTheUncoveredBranch(t *testing.T) {
	body := HealthzOf(service.Health{OK: true, APIVersion: "1", DBOK: true},
		false, LoopbackReach{Configured: true, Observed: true}, buildinfo.Coord{}, SelfUpdateStatus{
			Watching:  true,
			Uncovered: "이 실행 파일 이름에는 소스 트리가 박혀 있다(런처 bin/fd)",
		}, LedgerBackupStatus{})
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("직렬화 실패: %v", err)
	}
	if !strings.Contains(string(raw), `"uncovered":"이 실행 파일 이름에는`) {
		t.Fatalf("못 덮는 갈래가 선을 안 넘었다: %s", raw)
	}
	// 두 축이 한 키로 접히지 않았는가 — stalled 는 안 찼으니 안 나가야 한다.
	if strings.Contains(string(raw), `"stalled"`) {
		t.Fatalf("못 덮는 갈래를 stalled 로 접었다: %s", raw)
	}
	// 덮는 배치에서는 안 나가야 한다. 빈 축이 매번 실리면 읽는 쪽이 그 키를 무시하게 된다.
	quiet, err := json.Marshal(HealthzOf(service.Health{OK: true, APIVersion: "1", DBOK: true},
		false, LoopbackReach{Configured: true, Observed: true}, buildinfo.Coord{}, SelfUpdateStatus{Watching: true}, LedgerBackupStatus{}))
	if err != nil {
		t.Fatalf("직렬화 실패: %v", err)
	}
	if strings.Contains(string(quiet), `"uncovered"`) {
		t.Fatalf("다 덮는데 uncovered 가 실렸다: %s", quiet)
	}
}

// ★ 안 보고 있다는 사실이 '아직 갱신이 없었다'로 접히면 안 된다.
// json.Marshal 을 거쳐 **실제 바이트**로 확인한다 — 구조체 필드만 보면 태그 오타를
// 원리적으로 못 잡는다(Go 필드 값은 태그와 무관하게 그대로 있으므로).
func TestHealthzSaysWhenItIsNotWatching(t *testing.T) {
	body := HealthzOf(service.Health{OK: true, APIVersion: "1", DBOK: true},
		false, LoopbackReach{Configured: true, Observed: true}, buildinfo.Coord{}, SelfUpdateStatus{
			Watching: false, Reason: "이 플랫폼은 자기 재기동을 지원하지 않는다",
		}, LedgerBackupStatus{})
	if body.SelfUpdate.Watching {
		t.Fatal("watching 이 참이다")
	}
	if strings.TrimSpace(body.SelfUpdate.Reason) == "" {
		t.Fatal("왜 안 보는지가 비었다")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("직렬화 실패: %v", err)
	}
	for _, want := range []string{`"watching":false`, `"reason":`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("%s 가 응답에 없다: %s", want, raw)
		}
	}
}

// ★ 시도가 없었으면 last_at 은 응답에서 아예 **빠져야** 한다(omitempty). 제로 시각을
// null 로 찍으면 "시도가 있었는데 시각을 모른다"로 읽힐 수 있다 — 부재와 null 은 다른 말이다.
//
// ★ 이 시험이 재는 것은 **omitempty 하나**다. 앞선 판의 주석은 "serve.go 의 변환이
// 깨지면 이 시험이 잡는다"고 적었는데 거짓이었다 — 여기서는 nil 을 직접 넣으므로 상류가
// 항상 &at 를 채우게 바뀌어도 이 시험은 초록이다(2026-08-07 실측: 그 자리는 아무 시험에도
// 안 걸렸다). 그 변환을 재는 것은 cmd/fd 의 selfUpdateStatusOf 갈래이고, 이 선의 두
// 조각은 **각자 자기 것만** 잰다.
func TestHealthzOmitsLastAtWhenNoAttemptEver(t *testing.T) {
	body := HealthzOf(service.Health{OK: true, APIVersion: "1", DBOK: true},
		false, LoopbackReach{Configured: true, Observed: true}, buildinfo.Coord{}, SelfUpdateStatus{
			Watching: true, // 감시 중이지만 아직 교체를 한 번도 안 봤다 — LastAt 이 nil
		}, LedgerBackupStatus{})
	if body.SelfUpdate.LastAt != nil {
		t.Fatalf("시도가 없었는데 LastAt 이 채워졌다: %v", body.SelfUpdate.LastAt)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("직렬화 실패: %v", err)
	}
	if strings.Contains(string(raw), `"last_at"`) {
		t.Fatalf("last_at 이 omitempty 로 안 빠졌다: %s", raw)
	}
}

func TestRefill(t *testing.T) {
	base := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	if got := Refill(0, base, base.Add(10*time.Second), 1, 60); got != 10 {
		t.Fatalf("10초에 10토큰이어야 한다: %v", got)
	}
	if got := Refill(0, base, base.Add(time.Hour), 1, 60); got != 60 {
		t.Fatalf("상한을 넘었다: %v", got)
	}
	// 표 밖: 시계가 뒤로 갔을 때 토큰이 줄면 안 된다.
	if got := Refill(5, base, base.Add(-time.Hour), 1, 60); got != 5 {
		t.Fatalf("시계 역행에서 토큰이 변했다: %v", got)
	}
}

func TestClip(t *testing.T) {
	if got := clip("가나다라", 2); got != "가나…" {
		t.Fatalf("룬 단위로 안 잘린다: %q", got)
	}
	if got := clip("a\nb\x00c", 10); got != "a b c" {
		t.Fatalf("제어문자가 안 걷혔다: %q", got)
	}
}

// 판단 원장 백업 축이 본문까지 실려 나간다.
//
// ★ 순수 함수(HealthzOf)를 통과시키는 것이 요점이다. 본문 뒤에서 필드를 채우면 그 변환이
// 어느 시험에도 안 걸리고, 앞선 물결이 자동 갱신 축에서 정확히 그렇게 값을 잃었다.
func TestHealthzCarriesLedgerBackup(t *testing.T) {
	at := time.Date(2026, 8, 10, 4, 0, 0, 0, time.UTC)
	body := HealthzOf(service.Health{OK: true, APIVersion: "1", DBOK: true},
		true, LoopbackReach{}, buildinfo.Coord{}, SelfUpdateStatus{},
		LedgerBackupStatus{Running: true, LastAt: &at, Outcome: "failed",
			Detail: "디스크가 찼다", Route: "/ledger"})
	if !body.LedgerBackup.Running || body.LedgerBackup.Outcome != "failed" {
		t.Fatalf("축이 본문에 안 실렸다: %+v", body.LedgerBackup)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("직렬화 실패: %v", err)
	}
	for _, want := range []string{`"ledger_backup"`, `"outcome":"failed"`, `"route":"/ledger"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("%s 가 wire 에 없다:\n%s", want, raw)
		}
	}
	// 회차가 없으면 시각 키가 통째로 빠진다 — 제로값이 1970년으로 나가면 안 된다.
	quiet, err := json.Marshal(HealthzOf(service.Health{OK: true, APIVersion: "1", DBOK: true},
		true, LoopbackReach{}, buildinfo.Coord{}, SelfUpdateStatus{},
		LedgerBackupStatus{Running: true}))
	if err != nil {
		t.Fatalf("직렬화 실패: %v", err)
	}
	if strings.Contains(string(quiet), `"last_at"`) {
		t.Errorf("회차가 없는데 last_at 이 실렸다:\n%s", quiet)
	}
}

func TestJudgeScreenPath(t *testing.T) {
	// ★ 이 표가 이 설계 전체의 안전을 지탱한다. /api/v1 이 참이 되는 순간
	// REST 쓰기의 CSRF 방어가 쿠키의 SameSite 하나로 줄어든다.
	cases := map[string]bool{
		"/":                     true,
		"/events":               true,
		"/actions/reclaim":      true,
		"/actions/drop":         true,
		"/actions/lane-release": true,
		"/api/v1/items/next":    false,
		"/api/v1/events":        false, // REST 쪽 별칭이다 — 화면이 무는 것은 /events 다
		"/healthz":              false,
		"/metrics":              false,
		"/login":                false,
		"/actions":              false, // 접두는 슬래시까지다
		"":                      false,
	}
	for path, want := range cases {
		if got := JudgeScreenPath(path); got != want {
			t.Errorf("JudgeScreenPath(%q) = %v, 기대 %v", path, got, want)
		}
	}
}

func TestJudgeLoginScreen(t *testing.T) {
	cases := map[string]bool{
		"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8": true,
		"text/html":         true,
		"TEXT/HTML":         true, // 대소문자 무시
		"application/json":  false,
		"text/event-stream": false, // EventSource 는 폼을 못 읽는다
		"*/*":               false, // curl 기본값 — 사람이 아니다
		"":                  false,
	}
	for accept, want := range cases {
		if got := JudgeLoginScreen(accept); got != want {
			t.Errorf("JudgeLoginScreen(%q) = %v, 기대 %v", accept, got, want)
		}
	}
}

func TestJudgeNext(t *testing.T) {
	cases := map[string]string{
		"/":                   "/",
		"/?project=kweiza":    "/?project=kweiza",
		"/?q=a%20b":           "/?q=a%20b",
		"":                    "/",
		"   ":                 "/",
		"//evil.com":          "/", // 프로토콜 상대 URL — 다른 호스트로 나간다
		"///evil.com":         "/",
		"http://evil.com":     "/",
		"https://evil.com/x":  "/",
		"javascript:alert(1)": "/",
		"relative":            "/", // 슬래시로 안 시작하면 거절한다
		"/\\evil.com":         "/", // 일부 브라우저가 \ 를 / 로 정규화한다
	}
	for next, want := range cases {
		if got := JudgeNext(next); got != want {
			t.Errorf("JudgeNext(%q) = %q, 기대 %q", next, got, want)
		}
	}
}

func TestJudgeAuthExempt(t *testing.T) {
	cases := map[string]bool{
		"/login":  true,
		"/logout": true,
		// /healthz 는 여기 없다 — loopbackSeen 관측보다도 앞이라 미들웨어가 따로 본다.
		// 여기 넣으면 그 순서가 흐려지고, 컨테이너 헬스체크가 loopback_open 을 거짓으로
		// 참으로 만드는 회귀가 돌아온다(middleware.go 의 그 근거).
		"/healthz":           false,
		"/":                  false,
		"/api/v1/items/next": false,
		"/login/x":           false,
	}
	for path, want := range cases {
		if got := JudgeAuthExempt(path); got != want {
			t.Errorf("JudgeAuthExempt(%q) = %v, 기대 %v", path, got, want)
		}
	}
}
