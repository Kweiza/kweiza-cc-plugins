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
	"github.com/kweiza/flightdeck/internal/model"
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
		remote     string
		header     string
		token      string
		strictLoop bool
		wantOK     bool
		wantAnon   bool
		reasonHas  string
	}{
		{"토큰 일치", "203.0.113.9:1", "Bearer " + tok, tok, false, true, false, "일치한다"},
		{"토큰 불일치", "203.0.113.9:1", "Bearer nope", tok, false, false, false, "일치하지 않는다"},
		{"헤더 없음 원격", "203.0.113.9:1", "", tok, false, false, false, "헤더가 없다"},
		{"헤더 없음 루프백", "127.0.0.1:1", "", tok, false, true, true, "루프백"},
		{"헤더 없음 IPv6 루프백", "[::1]:1", "", tok, false, true, true, "루프백"},
		{"루프백 면제 끔", "127.0.0.1:1", "", tok, true, false, false, "루프백에도 토큰을 요구한다"},
		{"서버 토큰 미설정", "203.0.113.9:1", "", "", false, true, true, "설정되지 않았다"},
		{"형식 위반", "127.0.0.1:1", "Bearer a b", tok, false, false, false, "형식이 아니다"},
		{"방식이 Basic", "127.0.0.1:1", "Basic abcd", tok, false, false, false, "Bearer 가 아니다"},

		// ── 표 밖 ────────────────────────────────────────────────────────────
		// ① 틀린 토큰은 **루프백이어도** 거절한다. 면제로 덮으면 클라이언트의
		//    토큰 오설정이 영영 안 보인다.
		{"루프백인데 토큰이 틀림", "127.0.0.1:1", "Bearer wrong", tok, false, false, false, "일치하지 않는다"},
		// ② 서버에 토큰이 없는데 헤더가 온 경우 — 대조할 기준이 없으므로 통과하되 **무인증**이다.
		{"토큰 미설정 + 헤더 있음", "203.0.113.9:1", "Bearer whatever", "", false, true, true, "대조할 기준이 없다"},
		// ③ RemoteAddr 이 해석 불가면 루프백이 아니다(못 읽은 것을 면제로 접지 않는다).
		{"RemoteAddr 이 이상함", "@unix-socket", "", tok, false, false, false, "헤더가 없다"},
		// ④ 소문자 스킴도 받는다(HTTP 규약상 대소문자 무시).
		{"소문자 bearer", "203.0.113.9:1", "bearer " + tok, tok, false, true, false, "일치한다"},
		// ⑤ 대소문자가 다른 토큰 값은 다른 토큰이다.
		{"토큰 대소문자 다름", "203.0.113.9:1", "Bearer S3CRET", tok, false, false, false, "일치하지 않는다"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := JudgeAuth(c.remote, c.header, c.token, c.strictLoop)
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

func TestValidateOrigin(t *testing.T) {
	for _, c := range []struct {
		in     string
		want   model.FootprintOrigin
		wantOK bool
	}{
		{"", model.OriginObserved, true},
		{"observed", model.OriginObserved, true},
		{"declared", model.OriginDeclared, true},
		{"claimed", model.OriginClaimed, true},
		{" declared ", model.OriginDeclared, true},
		// 표 밖: 모르는 값을 기본값으로 접으면 셋의 구분이 조용히 사라진다.
		{"OBSERVED", "", false},
		{"guessed", "", false},
	} {
		got, err := ValidateOrigin(c.in)
		if (err == nil) != c.wantOK {
			t.Fatalf("ValidateOrigin(%q) 오류=%v, 통과 기대=%v", c.in, err, c.wantOK)
		}
		if c.wantOK && got != c.want {
			t.Fatalf("ValidateOrigin(%q)=%q, 기대 %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeFootprints(t *testing.T) {
	got := NormalizeFootprints("/repo", []string{
		"/repo/internal/api/api.go",
		"/repo/internal/api/api.go", // 중복은 접힌다
		"internal/api/sse.go",       // 이미 상대인 것은 그대로
		"",                          // 빈 것은 버린다
		"/other/x.go",               // 저장소 밖은 원본을 둔다
	})
	want := []string{"/other/x.go", "internal/api/api.go", "internal/api/sse.go"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("정규화 결과가 다르다: %v, 기대 %v", got, want)
	}
	// ★ 표 밖: 접두 문자열로 자르면 /repo-old/x.go 가 "-old/x.go" 로 둔갑한다.
	if got := NormalizeFootprints("/repo", []string{"/repo-old/x.go"}); got[0] != "/repo-old/x.go" {
		t.Fatalf("저장소 밖 경로가 잘렸다: %q", got[0])
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
	if !strings.Contains(AuthNotice(false, true), "무인증") {
		t.Fatal("토큰 미설정을 알리지 않는다 — 조용한 무인증은 안 된다")
	}
	if !strings.Contains(AuthNotice(true, true), "루프백") {
		t.Fatal("루프백 면제 사실이 없다")
	}
	if strings.Contains(AuthNotice(true, false), "루프백만") {
		t.Fatal("면제가 꺼졌는데 켜진 것처럼 말한다")
	}

	body := HealthzOf(service.Health{
		OK: false, APIVersion: "1", DBOK: false,
		DBPath:    "/home/user/.flightdeck/fd.db",
		DBError:   "unable to open database file /home/user/.flightdeck/fd.db",
		DiskError: "statfs /home/user/.flightdeck: no such file",
	}, true, true, buildinfo.Coord{})
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
