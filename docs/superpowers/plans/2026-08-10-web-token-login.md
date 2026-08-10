# 화면 토큰 로그인 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 토큰을 켠 배포에서 브라우저가 대시보드에 들어갈 수 있게, 401 본문에 토큰 폼을 내고 맞으면 화면 경로에서만 통하는 쿠키를 굽는다.

**Architecture:** `api` 가 판정·쿠키를 쥐고(토큰을 아는 유일한 자리) `web` 이 폼을 그린다(템플릿을 가진 유일한 자리). 둘을 잇는 것은 `api.Options.LoginScreen` 콜백이고, 이는 `Fallback` 이 이미 낸 길과 같은 모양이다. 인증 판정은 전부 순수 함수로 `internal/api/auth.go` 에 모은다.

**Tech Stack:** Go 1.x · 표준 라이브러리만(`net/http` · `crypto/subtle` · `html/template`) · 새 의존성 0

## Global Constraints

- **모듈 루트는 `plugins/flightdeck/server`** 다. 모든 `go` 명령은 거기서 돈다.
- **작업 트리는 워크트리 `.flightdeck/worktrees/fd-screen-unreachable-from-browser`** 다. main 에 직접 커밋하지 않는다.
- **새 의존성 0.** 표준 라이브러리만 쓴다.
- **주석은 한글**이고, 판정에는 **왜 그렇게 갈랐는지**를 적는다. 이 저장소의 기존 주석 밀도를 따른다.
- **`template.HTML` 을 반환하는 템플릿 함수를 절대 들이지 않는다**(`web/web.go:21` 의 규율).
- **판정은 순수 함수로 뽑는다.** 미들웨어 본문에 조건을 흩으면 시험이 그 조건의 사본을 단정하게 되고 변이가 조용히 샌다(`auth.go:11` 의 규율).
- **401 에 로그를 남기지 않는다**(`middleware.go:145`). 관측은 `/metrics` 의 unauthorized 카운터가 낸다.
- **응답에 토큰 값을 절대 되비추지 않는다.**
- 겹침 때문에 **`internal/web/page.go` · `actions.go` · `cmd/fd/{migrate,selfcheck}.go` 는 안 만진다**(다른 세션이 쥐고 있다. 판단 `01KZMY760E…` 에 적어 알렸다).
- 랜딩 전 관문: `gofmt -l .` · `go vet ./...` · `go test ./... -count=1` 셋 다 모듈 루트에서 돈다.

---

### Task 1: 순수 판정 셋 — 경로·응답형태·돌아갈 자리

**Files:**
- Modify: `plugins/flightdeck/server/internal/api/auth.go` (파일 끝에 추가, import 에 `net/url` 추가)
- Test: `plugins/flightdeck/server/internal/api/pure_test.go` (파일 끝에 추가)

**Interfaces:**
- Consumes: 없음. 이 태스크는 독립이다.
- Produces: `JudgeScreenPath(path string) bool` · `JudgeLoginScreen(accept string) bool` · `JudgeNext(next string) string`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/api/pure_test.go` 끝에 붙인다:

```go
func TestJudgeScreenPath(t *testing.T) {
	// ★ 이 표가 이 설계 전체의 안전을 지탱한다. /api/v1 이 참이 되는 순간
	// REST 쓰기의 CSRF 방어가 쿠키의 SameSite 하나로 줄어든다.
	cases := map[string]bool{
		"/":                    true,
		"/events":              true,
		"/actions/reclaim":     true,
		"/actions/drop":        true,
		"/actions/lane-release": true,
		"/api/v1/items/next":   false,
		"/api/v1/events":       false, // REST 쪽 별칭이다 — 화면이 무는 것은 /events 다
		"/healthz":             false,
		"/metrics":             false,
		"/login":               false,
		"/actions":             false, // 접두는 슬래시까지다
		"":                     false,
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
		"text/html":        true,
		"TEXT/HTML":        true, // 대소문자 무시
		"application/json": false,
		"text/event-stream": false, // EventSource 는 폼을 못 읽는다
		"*/*":              false, // curl 기본값 — 사람이 아니다
		"":                 false,
	}
	for accept, want := range cases {
		if got := JudgeLoginScreen(accept); got != want {
			t.Errorf("JudgeLoginScreen(%q) = %v, 기대 %v", accept, got, want)
		}
	}
}

func TestJudgeNext(t *testing.T) {
	cases := map[string]string{
		"/":                  "/",
		"/?project=kweiza":   "/?project=kweiza",
		"/?q=a%20b":          "/?q=a%20b",
		"":                   "/",
		"   ":                "/",
		"//evil.com":         "/", // 프로토콜 상대 URL — 다른 호스트로 나간다
		"///evil.com":        "/",
		"http://evil.com":    "/",
		"https://evil.com/x": "/",
		"javascript:alert(1)": "/",
		"relative":           "/", // 슬래시로 안 시작하면 거절한다
		"/\\evil.com":        "/", // 일부 브라우저가 \ 를 / 로 정규화한다
	}
	for next, want := range cases {
		if got := JudgeNext(next); got != want {
			t.Errorf("JudgeNext(%q) = %q, 기대 %q", next, got, want)
		}
	}
}
```

- [ ] **Step 2: 실패를 확인한다**

```bash
cd plugins/flightdeck/server
go test ./internal/api/ -run 'TestJudgeScreenPath|TestJudgeLoginScreen|TestJudgeNext' -count=1
```
기대: 컴파일 실패 — `undefined: JudgeScreenPath` 등 셋.

- [ ] **Step 3: 구현한다**

`internal/api/auth.go` 의 import 에 `"net/url"` 을 더하고, 파일 끝에 붙인다:

```go
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
```

- [ ] **Step 4: 통과를 확인한다**

```bash
cd plugins/flightdeck/server
go test ./internal/api/ -run 'TestJudgeScreenPath|TestJudgeLoginScreen|TestJudgeNext' -count=1 -v
```
기대: 셋 다 PASS.

- [ ] **Step 5: 커밋한다**

```bash
git add plugins/flightdeck/server/internal/api/auth.go plugins/flightdeck/server/internal/api/pure_test.go
git commit -m "feat(flightdeck): 화면 로그인의 순수 판정 셋 — 경로·응답형태·돌아갈 자리

JudgeScreenPath 가 /api/v1 에 거짓을 내는 것이 이 설계의 안전을 지탱한다.
JudgeLoginScreen 은 메서드를 안 본다 — 걸러야 할 축은 HTML 을 읽을 수 있느냐다.
JudgeNext 는 오픈 리다이렉트를 막는다. 아직 아무도 이 셋을 안 부른다."
```

---

### Task 2: `JudgeAuth` 에 쿠키 축

**Files:**
- Modify: `plugins/flightdeck/server/internal/api/auth.go:38-72` (`JudgeAuth` 시그니처와 본문)
- Modify: `plugins/flightdeck/server/internal/api/middleware.go:142` (호출부 하나)
- Test: `plugins/flightdeck/server/internal/api/pure_test.go:21-71` (`TestJudgeAuth` 테이블)

**Interfaces:**
- Consumes: Task 1 의 `JudgeScreenPath`(시험이 쓴다)
- Produces: `type AuthRequest struct{ RemoteAddr, AuthHeader, CookieToken string; ScreenPath bool }` · `JudgeAuth(req AuthRequest, token string, requireTokenOnLoopback bool) AuthDecision`

- [ ] **Step 1: 시험 테이블을 새 시그니처로 바꾸고 축을 더한다**

`pure_test.go` 의 `TestJudgeAuth` 를 통째로 갈아끼운다. 기존 14케이스를 **하나도 안 지우고** 필드만 옮기고, 쿠키 축 7개를 더한다(합 21):

```go
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
```

- [ ] **Step 2: 실패를 확인한다**

```bash
cd plugins/flightdeck/server
go test ./internal/api/ -run TestJudgeAuth -count=1
```
기대: 컴파일 실패 — `undefined: AuthRequest`, `too many arguments in call to JudgeAuth`.

- [ ] **Step 3: `JudgeAuth` 를 바꾼다**

`internal/api/auth.go` 의 `JudgeAuth` 를 통째로 갈아끼운다:

```go
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
```

- [ ] **Step 4: 호출부 하나를 고친다**

`internal/api/middleware.go:142` 를 바꾼다:

```go
		d := JudgeAuth(AuthRequest{
			RemoteAddr: r.RemoteAddr,
			AuthHeader: r.Header.Get("Authorization"),
			ScreenPath: JudgeScreenPath(r.URL.Path),
		}, s.opt.Token, s.opt.RequireTokenOnLoopback)
```

쿠키는 아직 안 읽는다 — Task 3 이 채운다.

- [ ] **Step 5: 통과를 확인한다**

```bash
cd plugins/flightdeck/server
go test ./internal/api/ -count=1
```
기대: 전부 PASS. 특히 `TestJudgeAuth/쿠키_일치_+_REST` 와 `비화면_쿠키_+_루프백_면제`.

- [ ] **Step 6: 커밋한다**

```bash
git add plugins/flightdeck/server/internal/api/auth.go plugins/flightdeck/server/internal/api/middleware.go plugins/flightdeck/server/internal/api/pure_test.go
git commit -m "feat(flightdeck): JudgeAuth 가 화면 경로의 쿠키를 받는다 — REST 는 헤더 전용 그대로

인자를 AuthRequest 로 묶었다. 문자열 셋과 불리언 둘을 늘어놓으면 ScreenPath 와
requireTokenOnLoopback 이 뒤바뀌어도 컴파일이 통과하고, 그 사고는 REST 에 쿠키를 연다.

쿠키를 든 비화면 요청은 거절로 끝내지 않고 아래로 흘려보낸다 — 거기서 끊으면
루프백 면제가 죽는다. 시험 ⑥ 이 그 회귀를 붙든다."
```

---

### Task 3: `withAuth` — 쿠키를 읽고, 면제 경로를 열고, 401 을 폼으로

**Files:**
- Modify: `plugins/flightdeck/server/internal/api/api.go` (`Options` 에 `LoginScreen` 추가, `LoginView` 타입 정의)
- Modify: `plugins/flightdeck/server/internal/api/auth.go` (`JudgeAuthExempt` 추가)
- Modify: `plugins/flightdeck/server/internal/api/middleware.go:113-156` (`withAuth`)
- Test: `plugins/flightdeck/server/internal/api/pure_test.go` · `http_test.go`

**Interfaces:**
- Consumes: Task 1 의 `JudgeLoginScreen`·`JudgeNext`, Task 2 의 `AuthRequest`
- Produces: `LoginCookieName` 상수 · `type LoginView struct{ Error, Next string }` · `Options.LoginScreen func(http.ResponseWriter, *http.Request, LoginView)` · `JudgeAuthExempt(path string) bool`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`pure_test.go` 에 붙인다:

```go
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
```

`http_test.go` 에 붙인다. 기존 파일의 조립 관용구(`newTestServer` 등)가 있으면 그것을 쓰고, 없으면 아래처럼 직접 조립한다:

```go
// TestUnauthorizedBrowserGetsForm 은 브라우저가 401 본문으로 폼을 받는지 본다.
// ★ 상태코드는 401 을 **유지해야 한다**. 리다이렉트로 덮으면 /metrics 의 unauthorized
// 카운터가 안 오르고, 인증 실패가 지표에서 사라진다.
func TestUnauthorizedBrowserGetsForm(t *testing.T) {
	var gotView LoginView
	h := NewServer(nil, Options{
		Token: "s3cret",
		LoginScreen: func(w http.ResponseWriter, r *http.Request, v LoginView) {
			gotView = v
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("<form>토큰</form>"))
		},
	})

	req := httptest.NewRequest("GET", "/?project=kweiza", nil)
	req.RemoteAddr = "203.0.113.9:1"
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("상태가 %d 다 — 401 이어야 한다", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("Content-Type 이 %q 다 — HTML 이어야 한다", ct)
	}
	if gotView.Next != "/?project=kweiza" {
		t.Fatalf("돌아갈 자리가 %q 다 — 원래 URL 이어야 한다", gotView.Next)
	}
	if gotView.Error == "" {
		t.Fatal("사유가 비었다 — 폼이 왜 떴는지 말해야 한다")
	}
}

// TestUnauthorizedCLIStaysJSON 은 CLI 소비자의 401 이 안 바뀌었는지 본다.
func TestUnauthorizedCLIStaysJSON(t *testing.T) {
	h := NewServer(nil, Options{
		Token: "s3cret",
		LoginScreen: func(w http.ResponseWriter, r *http.Request, v LoginView) {
			t.Fatal("CLI 요청에 폼을 냈다 — Accept 를 안 본 것이다")
		},
	})
	for _, path := range []string{"/", "/api/v1/items/next"} {
		req := httptest.NewRequest("GET", path, nil)
		req.RemoteAddr = "203.0.113.9:1"
		req.Header.Set("Accept", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: 상태가 %d 다", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Fatalf("%s: Content-Type 이 %q 다 — JSON 이어야 한다", path, ct)
		}
		if rec.Header().Get("WWW-Authenticate") == "" {
			t.Fatalf("%s: WWW-Authenticate 가 없다", path)
		}
	}
}

// TestScreenCookiePassesScreenOnly 는 쿠키의 범위가 실제로 닫혔는지 본다.
// ★ 이 시험이 이 변경의 안전을 재는 자리다.
func TestScreenCookiePassesScreenOnly(t *testing.T) {
	h := NewServer(nil, Options{Token: "s3cret"})
	cookie := &http.Cookie{Name: LoginCookieName, Value: "s3cret"}

	// 화면 경로 — 통과해야 한다. svc 가 nil 이라 500 이 날 수 있으나 **401 이면 안 된다**.
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "203.0.113.9:1"
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized {
		t.Fatal("화면 경로에서 쿠키가 안 통했다")
	}

	// REST — 401 이어야 한다.
	req = httptest.NewRequest("GET", "/api/v1/items/next", nil)
	req.RemoteAddr = "203.0.113.9:1"
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("REST 에서 쿠키가 통했다 (상태 %d) — 범위가 안 닫혔다", rec.Code)
	}
}
```

- [ ] **Step 2: 실패를 확인한다**

```bash
cd plugins/flightdeck/server
go test ./internal/api/ -run 'TestJudgeAuthExempt|TestUnauthorized|TestScreenCookie' -count=1
```
기대: 컴파일 실패 — `undefined: JudgeAuthExempt`, `unknown field LoginScreen`, `undefined: LoginCookieName`.

- [ ] **Step 3: `Options` 에 표면을 더한다**

`internal/api/api.go` 의 `Options` 안, `Fallback` 바로 아래에 붙인다:

```go
	// LoginScreen 은 401 을 HTML 토큰 폼으로 낼 렌더러다. nil 이면 JSON 401 로 접힌다.
	//
	// ★ Fallback 과 **같은 이유의 같은 모양**이다. 토큰을 아는 자리(여기)와 템플릿을 가진
	// 자리(internal/web)가 다른데, 화면은 게이트 사슬 안에 있어야 하므로 401 을 내는 것도
	// 이 계층이다. 콜백으로 받으면 api 가 HTML 을 알지 않고도 폼을 낼 수 있고, web 은
	// 토큰을 모르는 채로 남는다 — 토큰이 두 자리에 살면 상수시간 비교와 대소문자 규칙이
	// 두 벌이 되고 그 둘은 반드시 표류한다.
	//
	// ★ 이 콜백은 **상태코드를 스스로 쓴다**(401). 여기서 미리 쓰면 렌더러가 500 을 내야
	// 하는 경우에 헤더가 이미 나가 있다.
	LoginScreen func(w http.ResponseWriter, r *http.Request, v LoginView)
```

같은 파일에 타입을 더한다:

```go
// LoginView 는 토큰 폼을 그리는 데 필요한 것 전부다.
//
// ★ **토큰 값을 안 담는다.** 되비추면 그 값이 HTML 에 실려 나가고, web/notFound 가
// 요청 경로에 대해 세워둔 규율("소비자가 이미 아는 것을 되비추지 않는다")이 여기서 깨진다.
type LoginView struct {
	// Error 는 직전 시도의 사유다. 비면 첫 방문이다.
	Error string
	// Next 는 로그인 뒤 돌아갈 자리다. **호출부가 이미 JudgeNext 를 통과시킨 값이다** —
	// 렌더러가 그 검증을 다시 하지 않는다. 두 자리에서 검증하면 한쪽만 고쳐진다.
	Next string
}
```

- [ ] **Step 4: 면제 판정과 쿠키 이름을 더한다**

`internal/api/auth.go` 에 붙인다:

```go
// LoginCookieName 은 화면 로그인 쿠키의 이름이다.
//
// ★ 이름을 내보내는 이유: 굽는 자리(handleLogin)와 읽는 자리(withAuth)와 지우는
// 자리(handleLogout)가 셋인데, 리터럴로 두면 오타 하나가 "로그인은 되는데 다음 요청에서
// 다시 폼"이라는 모양으로 나타난다 — 원인이 이름이라는 것이 그 증상에서 안 보인다.
const LoginCookieName = "fd_token"

// JudgeAuthExempt 는 이 경로가 인증 게이트 앞인가다. 순수 함수다.
//
// ★ 로그인 경로가 게이트 뒤에 있으면 **로그인하려면 이미 로그인돼 있어야 한다.**
//
// ★ **메서드를 안 본다.** GET /login 도 인증 안 된 상태에서 들어온다.
//
// ★ /healthz 는 여기 **없다.** 그것은 loopbackSeen 관측보다도 앞이고, 그 순서에는
// 별도의 근거가 있다(withAuth 의 주석 — 컨테이너 헬스체크가 30초마다 쳐서 loopback_open 을
// 거짓으로 참으로 만든다). 두 면제를 한 함수로 접으면 그 순서가 흐려진다.
func JudgeAuthExempt(path string) bool {
	return path == "/login" || path == "/logout"
}
```

- [ ] **Step 5: `withAuth` 를 고친다**

`internal/api/middleware.go` 의 `/healthz` 갈래와 `loopbackSeen` 관측은 **그대로 두고**, 그 아래를 바꾼다:

```go
		// 로그인·로그아웃은 게이트 앞이다. 판정은 핸들러가 스스로 한다(login.go).
		//
		// ★ loopbackSeen 관측 **뒤**다. 앞에 두면 루프백에서 온 로그인 요청이 도달
		// 관측에서 빠진다 — /healthz 를 앞에 둔 것은 그 요청이 컨테이너 안에서 30초마다
		// 자동으로 나기 때문이고, 로그인은 사람이 치는 것이라 사정이 다르다.
		if JudgeAuthExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		// 쿠키는 화면 경로에서만 인정한다 — 그 판정은 JudgeAuth 안에 있다.
		// 여기서는 값만 읽는다. 쿠키가 없으면 ErrNoCookie 라 빈 문자열로 남는다.
		var cookieTok string
		if c, err := r.Cookie(LoginCookieName); err == nil {
			cookieTok = c.Value
		}
		d := JudgeAuth(AuthRequest{
			RemoteAddr:  r.RemoteAddr,
			AuthHeader:  r.Header.Get("Authorization"),
			CookieToken: cookieTok,
			ScreenPath:  JudgeScreenPath(r.URL.Path),
		}, s.opt.Token, s.opt.RequireTokenOnLoopback)
		if !d.OK {
			s.met.incUnauthorized()
			// 로그 줄 없음(위 규율). 사유는 응답에만 싣는다.
			//
			// ★ HTML 을 읽는 소비자에게는 폼을 낸다. **상태코드는 401 그대로다** —
			// 리다이렉트로 덮으면 인증 실패가 지표에서 사라지고, /metrics 의
			// unauthorized 카운터가 "아무도 막히지 않았다"고 거짓말한다.
			if s.opt.LoginScreen != nil && JudgeLoginScreen(r.Header.Get("Accept")) {
				s.opt.LoginScreen(w, r, LoginView{
					Error: d.Reason,
					Next:  JudgeNext(r.URL.RequestURI()),
				})
				return
			}
			w.Header().Set("WWW-Authenticate", `Bearer realm="flightdeck"`)
			s.writeError(w, r, Classified{
				Status: http.StatusUnauthorized, Code: "unauthorized",
				Message:  d.Reason,
				Guidance: UnauthorizedGuidance(s.loopbackReach()),
			})
			return
		}
		next.ServeHTTP(w, r)
```

- [ ] **Step 6: 통과를 확인한다**

```bash
cd plugins/flightdeck/server
go test ./internal/api/ -count=1
```
기대: 전부 PASS.

- [ ] **Step 7: 커밋한다**

```bash
git add plugins/flightdeck/server/internal/api/
git commit -m "feat(flightdeck): 401 이 HTML 소비자에게 토큰 폼을 낸다 — 상태코드는 401 그대로

Options.LoginScreen 은 Fallback 과 같은 모양의 이음매다. api 가 HTML 을 모르고,
web 이 토큰을 모른 채로 남는 유일한 배선이다.

/login·/logout 면제는 loopbackSeen 관측 뒤다 — 앞에 두면 루프백 로그인이 도달
관측에서 빠진다. /healthz 를 앞에 둔 근거와 사정이 다르다.

아직 렌더러가 없다(다음 태스크). 지금은 nil 이라 JSON 401 로 접힌다."
```

---

### Task 4: `login.go` — 토큰 대조와 쿠키

**Files:**
- Create: `plugins/flightdeck/server/internal/api/login.go`
- Modify: `plugins/flightdeck/server/internal/api/api.go` (`routes()` 에 라우트 셋)
- Test: `plugins/flightdeck/server/internal/api/login_test.go` (새 파일)

**Interfaces:**
- Consumes: Task 1 의 `JudgeNext`, Task 3 의 `LoginCookieName`·`LoginView`, 기존 `JudgeScreenOrigin(origin, secFetchSite, host string) OriginVerdict`
- Produces: `JudgeCookieSecure(tls bool, forwardedProto string) bool` · `LoginCookie(value string, maxAge int, secure bool) *http.Cookie` · 핸들러 `handleLogin`·`handleLogout`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/api/login_test.go` 를 만든다:

```go
package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestJudgeCookieSecure(t *testing.T) {
	cases := []struct {
		name   string
		tls    bool
		proto  string
		want   bool
	}{
		{"평문", false, "", false},
		{"직접 TLS", true, "", true},
		{"프록시가 https 라 함", false, "https", true},
		{"프록시가 http 라 함", false, "http", false},
		{"프록시 목록의 첫째를 본다", false, "https, http", true},
		{"대문자", false, "HTTPS", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := JudgeCookieSecure(c.tls, c.proto); got != c.want {
				t.Fatalf("JudgeCookieSecure(%v, %q) = %v, 기대 %v", c.tls, c.proto, got, c.want)
			}
		})
	}
}

func TestLoginCookieAttributes(t *testing.T) {
	c := LoginCookie("s3cret", loginCookieMaxAge, false)
	if c.Name != LoginCookieName || c.Value != "s3cret" {
		t.Fatalf("이름/값이 다르다: %q=%q", c.Name, c.Value)
	}
	if c.Path != "/" {
		t.Fatalf("Path 가 %q 다 — /events 와 /actions 둘 다 닿으려면 / 여야 한다", c.Path)
	}
	if !c.HttpOnly {
		t.Fatal("HttpOnly 가 꺼졌다 — JS 가 토큰을 읽을 수 있게 된다")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Fatal("SameSite 가 Strict 가 아니다 — 크로스사이트 요청에 쿠키가 실린다")
	}
	if c.MaxAge != loginCookieMaxAge {
		t.Fatalf("MaxAge 가 %d 다", c.MaxAge)
	}
	if c.Secure {
		t.Fatal("평문인데 Secure 가 켜졌다 — http:// 에서 쿠키가 저장조차 안 된다")
	}
}

// loginPost 는 폼 POST 하나를 만든다. 출처 헤더를 채워 JudgeScreenOrigin 을 통과시킨다.
func loginPost(t *testing.T, path string, form url.Values) *http.Request {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	req.RemoteAddr = "203.0.113.9:1"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	return req
}

func TestLoginSetsCookieAndRedirects(t *testing.T) {
	h := NewServer(nil, Options{Token: "s3cret"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, loginPost(t, "/login", url.Values{
		"token": {"s3cret"}, "next": {"/?project=kweiza"},
	}))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("상태가 %d 다 — 303 이어야 한다", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/?project=kweiza" {
		t.Fatalf("Location 이 %q 다", loc)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != LoginCookieName || cookies[0].Value != "s3cret" {
		t.Fatalf("쿠키를 안 구웠다: %+v", cookies)
	}
}

func TestLoginRejectsWrongTokenWithoutEcho(t *testing.T) {
	var gotView LoginView
	h := NewServer(nil, Options{
		Token: "s3cret",
		LoginScreen: func(w http.ResponseWriter, r *http.Request, v LoginView) {
			gotView = v
			w.WriteHeader(http.StatusUnauthorized)
		},
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, loginPost(t, "/login", url.Values{"token": {"wrong-guess"}}))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("상태가 %d 다 — 401 이어야 한다", rec.Code)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("틀린 토큰에 쿠키를 구웠다")
	}
	if !strings.Contains(gotView.Error, "일치하지 않는다") {
		t.Fatalf("사유가 %q 다", gotView.Error)
	}
	// ★ 시도한 값이 응답에 돌아오면 안 된다.
	if strings.Contains(gotView.Error, "wrong-guess") || strings.Contains(gotView.Next, "wrong-guess") {
		t.Fatal("시도한 토큰 값이 응답에 되비쳤다")
	}
}

func TestLoginRefusesCrossSite(t *testing.T) {
	h := NewServer(nil, Options{Token: "s3cret"})
	req := httptest.NewRequest("POST", "/login", strings.NewReader("token=s3cret"))
	req.RemoteAddr = "203.0.113.9:1"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("상태가 %d 다 — 403 이어야 한다", rec.Code)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("크로스사이트 로그인에 쿠키를 구웠다")
	}
}

// TestLoginWithoutServerTokenBakesNothing 은 인증이 꺼진 서버의 갈래다.
// ★ 여기서 쿠키를 구우면 나중에 토큰을 켰을 때 그 쿠키가 **틀린 자격증명**이 되어
// 폼이 아니라 거절을 만난다.
func TestLoginWithoutServerTokenBakesNothing(t *testing.T) {
	h := NewServer(nil, Options{Token: ""})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, loginPost(t, "/login", url.Values{"token": {"whatever"}}))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("상태가 %d 다 — 303 이어야 한다", rec.Code)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("인증이 꺼진 서버가 쿠키를 구웠다")
	}
}

func TestLogoutClearsCookie(t *testing.T) {
	h := NewServer(nil, Options{Token: "s3cret"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, loginPost(t, "/logout", nil))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("상태가 %d 다", rec.Code)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != LoginCookieName {
		t.Fatalf("쿠키를 안 지웠다: %+v", cookies)
	}
	if cookies[0].MaxAge >= 0 || cookies[0].Value != "" {
		t.Fatalf("쿠키가 안 지워졌다: MaxAge=%d Value=%q", cookies[0].MaxAge, cookies[0].Value)
	}
}

func TestLoginGetRedirectsHome(t *testing.T) {
	h := NewServer(nil, Options{Token: "s3cret"})
	req := httptest.NewRequest("GET", "/login", nil)
	req.RemoteAddr = "203.0.113.9:1"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("상태가 %d 다 — 303 이어야 한다", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Fatalf("Location 이 %q 다", loc)
	}
}
```

- [ ] **Step 2: 실패를 확인한다**

```bash
cd plugins/flightdeck/server
go test ./internal/api/ -run 'TestLogin|TestLogout|TestJudgeCookieSecure' -count=1
```
기대: 컴파일 실패 — `undefined: JudgeCookieSecure`, `undefined: LoginCookie`, `undefined: loginCookieMaxAge`.

- [ ] **Step 3: `login.go` 를 만든다**

```go
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
```

- [ ] **Step 4: 라우트를 단다**

`internal/api/api.go` 의 `routes()` 안, `GET /metrics` 줄 아래에 붙인다:

```go
	// 화면 로그인. **게이트 앞에서 갈라진다**(withAuth 의 JudgeAuthExempt) — 로그인이
	// 게이트 뒤에 있으면 로그인하려면 이미 로그인돼 있어야 한다.
	mux.HandleFunc("GET /login", s.handleLogin)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("POST /logout", s.handleLogout)
```

- [ ] **Step 5: 통과를 확인한다**

```bash
cd plugins/flightdeck/server
go test ./internal/api/ -count=1
```
기대: 전부 PASS.

- [ ] **Step 6: 커밋한다**

```bash
git add plugins/flightdeck/server/internal/api/login.go plugins/flightdeck/server/internal/api/login_test.go plugins/flightdeck/server/internal/api/api.go
git commit -m "feat(flightdeck): /login 이 토큰을 대조하고 화면 쿠키를 굽는다

출처 대조를 직접 부른다 — withScreenWrite 는 withAuth 안쪽이라 게이트 앞에서
갈라지는 이 경로에 안 걸린다.

Secure 는 TLS 뒤일 때만 켠다. 무조건 켜면 평문 배포에서 쿠키가 저장조차 안 돼
'폼을 제출하면 다시 폼'이 되고, 원인이 쿠키 속성이라는 것이 그 증상에서 안 보인다.

인증이 꺼진 서버는 쿠키를 안 굽는다 — 구우면 나중에 토큰을 켰을 때 그 쿠키가
틀린 자격증명이 되어 폼이 아니라 거절을 만난다."
```

---

### Task 5: `web` 의 로그인 화면

**Files:**
- Create: `plugins/flightdeck/server/internal/web/login.gohtml`
- Create: `plugins/flightdeck/server/internal/web/login.go`
- Test: `plugins/flightdeck/server/internal/web/login_test.go` (새 파일)

**Interfaces:**
- Consumes: 없음 — **`web` 은 `api` 를 import 하지 않는다.** 자기 타입을 쓰고 배선(Task 6)이 잇는다.
- Produces: `web.LoginView{Error, Next string}` · `web.LoginScreen(w http.ResponseWriter, r *http.Request, v LoginView)`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/web/login_test.go`:

```go
package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoginScreenRendersForm(t *testing.T) {
	rec := httptest.NewRecorder()
	LoginScreen(rec, httptest.NewRequest("GET", "/", nil),
		LoginView{Error: "토큰이 일치하지 않는다", Next: "/?project=kweiza"})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("상태가 %d 다 — 401 이어야 한다", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("Content-Type 이 %q 다", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control 이 %q 다 — 로그인 화면이 캐시되면 안 된다", cc)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`method="post"`,
		`action="login"`,      // 상대경로다 — 프록시 접두 뒤에서도 맞는 자리를 가리킨다
		`type="password"`,     // 어깨너머로 안 보인다
		`name="token"`,
		`name="next"`,
		"토큰이 일치하지 않는다",
		`value="/?project=kweiza"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("본문에 %q 가 없다", want)
		}
	}
}

// TestLoginScreenEscapes 는 사유와 next 가 HTML 로 새지 않는지 본다.
func TestLoginScreenEscapes(t *testing.T) {
	rec := httptest.NewRecorder()
	LoginScreen(rec, httptest.NewRequest("GET", "/", nil),
		LoginView{Error: `<script>alert(1)</script>`, Next: `/"><script>x</script>`})

	body := rec.Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatal("사유가 이스케이프 없이 나갔다")
	}
	if strings.Contains(body, `"><script>x</script>`) {
		t.Fatal("next 가 속성 밖으로 샜다")
	}
}

// TestLoginScreenFirstVisitHasNoError 는 첫 방문에 빈 오류 자리가 안 뜨는지 본다.
func TestLoginScreenFirstVisitHasNoError(t *testing.T) {
	rec := httptest.NewRecorder()
	LoginScreen(rec, httptest.NewRequest("GET", "/", nil), LoginView{Next: "/"})
	if strings.Contains(rec.Body.String(), `class="err"`) {
		t.Fatal("사유가 없는데 오류 자리가 떴다")
	}
}
```

- [ ] **Step 2: 실패를 확인한다**

```bash
cd plugins/flightdeck/server
go test ./internal/web/ -run TestLoginScreen -count=1
```
기대: 컴파일 실패 — `undefined: LoginScreen`, `undefined: LoginView`.

- [ ] **Step 3: 템플릿을 만든다**

`internal/web/login.gohtml`. 색·글꼴은 `dashboard.gohtml` 의 토큰을 그대로 베낀다 — 두 화면이 다른 제품처럼 보이면 안 된다:

```html
<!doctype html>
<html lang="ko">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>flightdeck — 토큰</title>
<style>
:root { color-scheme: light dark; --bg:#fbfbfa; --fg:#1c1b19; --dim:#5f5c56; --line:#e0ddd6;
        --card:#ffffff; --crit:#8f1d1d; --accent:#2f5fb3; }
@media (prefers-color-scheme: dark) {
  :root { --bg:#16181c; --fg:#e6e4e0; --dim:#9a978f; --line:#2c3037; --card:#1c1f24;
          --crit:#f08a8a; --accent:#8fb2f0; }
}
* { box-sizing: border-box; }
body { margin:0; background:var(--bg); color:var(--fg); font:14px/1.55 ui-sans-serif,system-ui,
       "Noto Sans KR","Apple SD Gothic Neo",sans-serif;
       min-height:100vh; display:flex; align-items:center; justify-content:center; padding:20px; }
main { width:100%; max-width:380px; background:var(--card); border:1px solid var(--line);
       border-radius:6px; padding:20px 22px; }
h1 { font-size:16px; margin:0 0 2px; letter-spacing:.02em; }
p.sub { color:var(--dim); font-size:12px; margin:0 0 16px; }
label { display:block; font-size:13px; margin:0 0 6px; }
input, button { font:inherit; padding:6px 8px; background:var(--bg); color:var(--fg);
                border:1px solid var(--line); border-radius:4px; }
input { width:100%; margin-top:4px; }
button { cursor:pointer; margin-top:12px; width:100%; border-color:var(--accent); color:var(--accent); }
p.err { color:var(--crit); font-size:13px; margin:0 0 12px; }
</style>
</head>
<body>
<main>
  <h1>flightdeck</h1>
  <p class="sub">이 서버는 토큰이 켜져 있다.</p>
  {{with .Error}}<p class="err">{{.}}</p>{{end}}
  {{/* ★ action 이 상대경로다. dashboard.gohtml 의 폼·SSE 와 같은 규율 —
       절대경로로 두면 리버스 프록시의 경로 접두 뒤에서 원점의 /login 을 찾아가고,
       프록시는 그 경로를 모르므로 로그인이 조용히 실패한다. */}}
  <form method="post" action="login">
    <input type="hidden" name="next" value="{{.Next}}">
    <label>토큰
      <input type="password" name="token" autocomplete="current-password"
             autofocus required spellcheck="false">
    </label>
    <button type="submit">들어간다</button>
  </form>
</main>
</body>
</html>
```

- [ ] **Step 4: 렌더러를 만든다**

`internal/web/login.go`:

```go
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
```

`internal/web/web.go` 의 `//go:embed` 줄과 템플릿 선언을 고친다:

```go
//go:embed dashboard.gohtml login.gohtml
var files embed.FS
```

그리고 `tpl` 선언 아래에 붙인다:

```go
// loginTpl 도 기동 시 한 번 파싱한다. tpl 과 따로 두는 이유는 이 화면이 대시보드의
// 템플릿 함수(join)도 데이터 모양도 쓰지 않기 때문이다 — 한 벌로 묶으면 로그인 화면이
// 대시보드 Page 의 필드를 실수로 참조해도 파싱이 통과한다.
var loginTpl = template.Must(template.New("login.gohtml").ParseFS(files, "login.gohtml"))
```

- [ ] **Step 5: 통과를 확인한다**

```bash
cd plugins/flightdeck/server
go test ./internal/web/ -count=1
```
기대: 전부 PASS. 기존 `TestTemplateBalance` 계열이 새 템플릿에도 도는지 확인한다 — 붉으면 그 시험이 요구하는 규칙(태그 균형)을 `login.gohtml` 이 지키도록 고친다.

- [ ] **Step 6: 커밋한다**

```bash
git add plugins/flightdeck/server/internal/web/login.gohtml plugins/flightdeck/server/internal/web/login.go plugins/flightdeck/server/internal/web/login_test.go plugins/flightdeck/server/internal/web/web.go
git commit -m "feat(flightdeck): 화면이 토큰 폼을 그린다 — 이 패키지는 여전히 토큰을 모른다

api 를 import 하지 않는다. LoginView 를 여기서 다시 선언하고 배선이 둘을 잇는다 —
화면 패키지가 REST 표면을 알기 시작하면 그 방향은 되돌리기 어렵다.

폼 action 이 상대경로다. dashboard.gohtml 의 폼·SSE 와 같은 규율 — 절대경로면
리버스 프록시의 경로 접두 뒤에서 로그인이 조용히 실패한다."
```

---

### Task 6: 배선 — `serve.go`

**Files:**
- Modify: `plugins/flightdeck/server/cmd/fd/serve.go:102-116` (`serveAPIOptions`)
- Test: `plugins/flightdeck/server/cmd/fd/serve_test.go` (없으면 새로 만든다)

**Interfaces:**
- Consumes: Task 3 의 `api.Options.LoginScreen`·`api.LoginView`, Task 5 의 `web.LoginScreen`·`web.LoginView`
- Produces: 배선된 `api.Options`

> **겹침 주의:** `01KZDPEE` 세션이 `cmd/fd/{migrate,selfcheck,serve}` 를 쥐고 있다. **`serveAPIOptions` 한 함수와 그 독 코멘트만** 만진다. `migrate.go`·`selfcheck.go` 는 열지 않는다.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`cmd/fd/serve_test.go` (기존 파일이 있으면 끝에 붙인다):

```go
// TestServeAPIOptionsWiresLoginScreen 은 배선 누락을 잡는다.
//
// ★ 이 시험이 없으면 배선 빠짐이 **조용하다.** LoginScreen 이 nil 이면 api 는 JSON 401 로
// 접히므로 서버는 멀쩡히 뜨고 REST 도 다 돌고, 오직 브라우저에서만 폼 대신 JSON 이 뜬다.
// serve.go 가 조립을 순수 함수로 뽑아둔 근거가 정확히 이것이다.
func TestServeAPIOptionsWiresLoginScreen(t *testing.T) {
	opt := serveAPIOptions("tok", 60, slog.Default(), false, nil)
	if opt.LoginScreen == nil {
		t.Fatal("LoginScreen 이 nil 이다 — 브라우저가 폼 대신 JSON 401 을 본다")
	}

	// 실제로 폼을 그리는지 본다. nil 아님만 재면 func(...){} 빈 몸통도 통과한다.
	rec := httptest.NewRecorder()
	opt.LoginScreen(rec, httptest.NewRequest("GET", "/", nil),
		api.LoginView{Error: "토큰이 일치하지 않는다", Next: "/?project=x"})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("상태가 %d 다 — 401 이어야 한다", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `name="token"`) {
		t.Fatal("토큰 입력이 없다")
	}
	// ★ 두 LoginView 사이에서 필드가 뒤바뀌지 않았는지 본다. 같은 타입의 문자열 둘이라
	// Error 와 Next 를 맞바꿔도 컴파일이 통과한다.
	if !strings.Contains(body, "토큰이 일치하지 않는다") {
		t.Fatal("사유가 안 실렸다 — 어댑터가 Error 를 잘못 옮겼다")
	}
	if !strings.Contains(body, `value="/?project=x"`) {
		t.Fatal("돌아갈 자리가 안 실렸다 — 어댑터가 Next 를 잘못 옮겼다")
	}
}
```

필요한 import: `log/slog` · `net/http` · `net/http/httptest` · `strings` · `testing` · `github.com/kweiza/flightdeck/internal/api`.

- [ ] **Step 2: 실패를 확인한다**

```bash
cd plugins/flightdeck/server
go test ./cmd/fd/ -run TestServeAPIOptionsWiresLoginScreen -count=1
```
기대: FAIL — "LoginScreen 이 nil 이다".

- [ ] **Step 3: 배선한다**

`cmd/fd/serve.go` 의 `serveAPIOptions` 안, `return opt` 바로 위에 붙인다:

```go
	// ★ 화면 로그인 렌더러. **여기서 두 LoginView 를 잇는다** — web 이 api 를 import 하지
	// 않으므로 타입이 둘이고, 그 둘을 아는 자리는 이 조립 함수뿐이다.
	//
	// ★ nil 로 두면 실패가 **조용하다**: 서버는 뜨고 REST 도 다 도는데 브라우저에서만
	// 폼 대신 JSON 401 이 뜬다. 그 모양은 운영에서 사람이 봐야 발견되고, 정확히 그런
	// 침묵이 이 함수를 순수 함수로 뽑게 만든 사고였다(위 ★ 참고).
	opt.LoginScreen = func(w http.ResponseWriter, r *http.Request, v api.LoginView) {
		web.LoginScreen(w, r, web.LoginView{Error: v.Error, Next: v.Next})
	}
```

import 은 더할 것이 없다 — `net/http`·`internal/api`·`internal/web` 셋 다 `serve.go` 가 이미 쓰고 있다(`main@7e98e7e` 기준 확인).

- [ ] **Step 4: 통과를 확인한다**

```bash
cd plugins/flightdeck/server
go test ./cmd/fd/ -count=1
```
기대: 전부 PASS.

- [ ] **Step 5: 커밋한다**

```bash
git add plugins/flightdeck/server/cmd/fd/serve.go plugins/flightdeck/server/cmd/fd/serve_test.go
git commit -m "wire(flightdeck): serveAPIOptions 가 로그인 렌더러를 잇는다

시험이 nil 아님만 재지 않고 실제로 폼을 그리게 한 뒤 Error·Next 가 제 자리에
실렸는지 본다 — 같은 타입의 문자열 둘이라 어댑터에서 맞바꿔도 컴파일이 통과한다.

배선을 빠뜨리면 실패가 조용하다: 서버는 뜨고 REST 도 도는데 브라우저에서만
폼 대신 JSON 401 이 뜬다."
```

---

### Task 7: 로그아웃 버튼

**Files:**
- Modify: `plugins/flightdeck/server/internal/web/dashboard.gohtml:83-91` (`<header>`)
- Test: `plugins/flightdeck/server/internal/web/login_test.go` (Task 5 의 파일에 추가)

**Interfaces:**
- Consumes: Task 4 의 `POST /logout`
- Produces: 없음

> **`page.go` 를 안 만진다.** "지금 쿠키가 있나"를 화면에 실으려면 `Page` 에 필드가 늘고 그 파일은 겹침 대상이다. 버튼을 **항상** 보이게 두면 그 필드가 필요 없다 — 인증이 꺼진 서버에서 눌러도 쿠키가 없으니 303 으로 `/` 에 돌아올 뿐이고, 그 동작은 틀리지 않다.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/web/login_test.go` 에 붙인다:

```go
// TestDashboardHasLogout 은 대시보드에 쿠키를 지울 길이 있는지 본다.
//
// ★ 로그아웃이 없으면 쿠키를 버릴 수단이 브라우저 설정뿐이다. 수명이 10년이라 그 길이
// 없으면 남의 머신에서 한 번 본 것이 사실상 영구히 남는다.
func TestDashboardHasLogout(t *testing.T) {
	src, err := files.ReadFile("dashboard.gohtml")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	if !strings.Contains(body, `action="logout"`) {
		t.Error(`로그아웃 폼이 없다 (action="logout" — 상대경로여야 한다)`)
	}
	if !strings.Contains(body, `method="post"`) {
		t.Error("로그아웃이 POST 가 아니다 — GET 이면 링크 프리페치로 눌린다")
	}
}
```

- [ ] **Step 2: 실패를 확인한다**

```bash
cd plugins/flightdeck/server
go test ./internal/web/ -run TestDashboardHasLogout -count=1
```
기대: FAIL — "로그아웃 폼이 없다".

- [ ] **Step 3: 헤더에 버튼을 넣는다**

`internal/web/dashboard.gohtml` 의 `<header>` 를 바꾼다:

```html
<header>
  <h1>{{.Title}}</h1>
  <div class="derived">{{.Now}} · {{.HealthLine}} · 디스크: <span class="lv-{{.Disk.Level}}">{{.Disk.Text}}</span></div>
  {{if .Projects}}
  <nav>프로젝트:
    {{range .Projects}}<a class="{{if eq .ID $.Project.ID}}on{{end}}" href="?project={{.ID}}">{{.ID}}</a>{{end}}
  </nav>
  {{end}}
  {{/* ★ POST 다. GET 링크로 두면 브라우저·확장의 링크 프리페치가 눌러서 로그아웃이
       사람 의사 없이 일어난다. 상대경로인 이유는 이 페이지의 다른 폼과 같다.
       ★ 인증이 꺼진 서버에서도 보인다 — 그 사실을 화면에 실으려면 Page 에 축이 하나
       늘고, 눌러도 303 으로 여기 돌아올 뿐이라 틀린 동작이 아니다. */}}
  <form class="logout" method="post" action="logout"><button type="submit">로그아웃</button></form>
</header>
```

`<style>` 에 한 줄 더한다(기존 `header` 규칙 아래):

```css
header { position:relative; }
form.logout { position:absolute; top:12px; right:18px; margin:0; }
form.logout button { font-size:12px; padding:2px 8px; color:var(--dim); }
```

- [ ] **Step 4: 통과를 확인한다**

```bash
cd plugins/flightdeck/server
go test ./internal/web/ -count=1
```
기대: 전부 PASS. 기존 렌더 시험이 헤더 모양을 단정하고 있으면 그 단정과 충돌하지 않는지 확인한다.

- [ ] **Step 5: 커밋한다**

```bash
git add plugins/flightdeck/server/internal/web/dashboard.gohtml plugins/flightdeck/server/internal/web/login_test.go
git commit -m "feat(flightdeck): 대시보드 헤더에 로그아웃 — POST 다

GET 링크로 두면 링크 프리페치가 눌러서 사람 의사 없이 로그아웃된다.

수명이 10년이라 이 버튼이 없으면 쿠키를 버릴 수단이 브라우저 설정뿐이고,
남의 머신에서 한 번 본 것이 사실상 영구히 남는다."
```

---

### Task 8: `DESIGN.md` §6 과 관문

**Files:**
- Modify: `plugins/flightdeck/DESIGN.md` (§6 의 인증 문단 하나)

**Interfaces:**
- Consumes: 앞 태스크 전부
- Produces: 없음

> **겹침 주의:** `01KZKSJM` 이 `DESIGN.md` 를 쥐고 있으나 §7·§10·§11 만 만진다고 알려 왔다. **§6 의 인증 문단 한 자리**만 고친다. 착수 전에 `git log -1 --format=%s -- plugins/flightdeck/DESIGN.md` 로 그 사이 바뀐 게 있는지 본다.

- [ ] **Step 1: 문단을 고친다**

`DESIGN.md` 의 이 문단을 찾는다(§6, "인증은 `Authorization: Bearer <token>`" 으로 시작):

```
인증은 `Authorization: Bearer <token>`. 사용자 1명이므로 토큰 1개, 역할 없음.
```

바로 아래(같은 문단 끝, "모든 쓰기에 `Idempotency-Key`" 줄 **앞**)에 붙인다:

```markdown
**브라우저는 헤더를 못 싣는다 — 그래서 화면 경로에만 쿠키 갈래가 있다.**
`POST /login` 이 토큰을 대조해 `fd_token` 쿠키(`HttpOnly`·`SameSite=Strict`·`Path=/`·10년,
TLS 뒤에서만 `Secure`)를 굽고, `JudgeAuth` 는 **`/` · `/actions/*` · `/events` 에서만** 그
쿠키를 인정한다. `/api/v1/*` 는 헤더 전용 그대로다 — REST 쓰기는 `withScreenWrite` 의 출처
대조를 안 타므로(`JudgeScreenWrite` 가 화면 경로만 `Screen=true`), 거기에 쿠키를 열면 REST
쓰기 전체의 CSRF 방어가 `SameSite` 하나로 줄어든다. **헤더가 있으면 쿠키는 안 본다**
(멱등 키와 같은 규율 — 무엇이 인증했는지가 요청마다 달라지면 안 된다).
인증 안 된 요청 중 `Accept: text/html` 인 것만 **401 본문에 폼**을 받는다. 상태코드는
401 그대로다 — 리다이렉트로 덮으면 인증 실패가 `/metrics` 의 unauthorized 축에서 사라진다.
`/login`·`/logout` 은 게이트 앞이다(`JudgeAuthExempt`). 로그아웃은 그 브라우저의 쿠키만
지운다 — 토큰 무효화는 `FD_TOKEN` 교체뿐이고 이 제품에 다른 길을 만들지 않는다.
```

- [ ] **Step 2: 랜딩 관문 다섯 줄을 돌린다**

```bash
cd plugins/flightdeck/server
gofmt -l .
go vet ./...
go test ./... -count=1
GOOS=darwin GOARCH=arm64 go vet ./...
GOOS=windows GOARCH=amd64 go vet ./...
```
기대: `gofmt -l .` 이 **아무것도 안 찍는다**(빈 출력이 통과다 — 단 cwd 가 모듈 안인지 먼저 확인해라). 나머지 넷은 오류 없이 끝난다.

- [ ] **Step 3: 실물로 한 번 본다**

```bash
cd plugins/flightdeck/server
go build -o /tmp/fd-login-check ./cmd/fd
FD_TOKEN=testtoken123 FD_ADDR=127.0.0.1:7999 FD_DB=/tmp/fd-login-check.db /tmp/fd-login-check serve &
sleep 2

# ① 브라우저 흉내 — 폼이 401 로 온다
curl -s -i -H 'Accept: text/html' http://127.0.0.1:7999/ | head -3
curl -s -H 'Accept: text/html' http://127.0.0.1:7999/ | grep -c 'name="token"'

# ② CLI 흉내 — JSON 401 그대로
curl -s -i -H 'Accept: application/json' http://127.0.0.1:7999/api/v1/items/next | head -3

# ③ 로그인 → 쿠키
curl -s -i -X POST -H 'Sec-Fetch-Site: same-origin' \
  -d 'token=testtoken123&next=/' http://127.0.0.1:7999/login | grep -i 'set-cookie\|^HTTP'

# ④ 그 쿠키로 화면은 되고 REST 는 안 된다
curl -s -o /dev/null -w 'screen=%{http_code}\n' -b 'fd_token=testtoken123' http://127.0.0.1:7999/
curl -s -o /dev/null -w 'rest=%{http_code}\n' -b 'fd_token=testtoken123' http://127.0.0.1:7999/api/v1/items/next

kill %1
```
기대: ① `HTTP/1.1 401` 과 `1` · ② `HTTP/1.1 401` + `application/json` · ③ `303` 과 `Set-Cookie: fd_token=...; Path=/; Max-Age=315360000; HttpOnly; SameSite=Strict` · ④ `screen=200` 과 `rest=401`.

- [ ] **Step 4: 커밋한다**

```bash
git add plugins/flightdeck/DESIGN.md
git commit -m "docs(flightdeck): DESIGN §6 에 화면 쿠키 갈래 — 범위가 화면뿐이라는 것이 핵심이다

'인증은 Authorization: Bearer' 만 적혀 있으면 다음 사람이 '인증은 하나'라고 읽고
REST 에 쿠키를 연다. 그 순간 REST 쓰기의 CSRF 방어가 SameSite 하나로 줄어든다."
```

---

## 자체 점검

**명세 대조.** §3 자리 배분 → Task 3·5·6. §4 순수 판정 넷 → Task 1(셋)·Task 2(`JudgeAuth`). §5 흐름의 갈래 다섯 → Task 3(면제·401 갈래)·Task 4(로그인·로그아웃·`GET /login`). §6 쿠키 속성 다섯 → Task 4 의 `LoginCookie`·`JudgeCookieSecure` 와 그 시험. §7 오류 여섯 갈래 → Task 4 의 시험 다섯 + Task 5 의 렌더 실패. §8 시험 목록 전부 → 각 태스크의 Step 1. §10 `DESIGN.md` → Task 8. **§11 의 한계 셋은 코드 변경이 없다** — 문서에만 남는다.

**빠진 것 하나를 채웠다.** 명세에 없던 로그아웃 **버튼**(Task 7)이다. §2 가 "로그아웃을 함께 둔다"고 결정했는데 §5 는 `POST /logout` 만 적고 그것을 누를 자리를 안 적었다. 엔드포인트만 있고 버튼이 없으면 사람은 `curl` 로만 로그아웃할 수 있다.

**타입 일관성.** `LoginView{Error, Next}` 는 `api`·`web` 두 곳에 같은 필드로 선언되고 Task 6 의 어댑터가 잇는다 — 그 어댑터가 필드를 맞바꿔도 컴파일이 통과하므로 Task 6 의 시험이 둘을 개별로 단정한다. `LoginCookieName` 은 Task 3 에서 선언되어 Task 4(굽기·지우기)·Task 3(읽기)·Task 4 시험이 함께 쓴다. `loginCookieMaxAge` 는 Task 4 에서 선언되고 같은 태스크의 시험이 쓴다.
