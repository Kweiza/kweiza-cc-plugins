# 접두 뒤 리다이렉트 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 이 서버가 내는 `Location` 을 상대 참조로 바꿔, 경로 접두를 벗기는 리버스 프록시 뒤에서 리다이렉트가 접두 **안**에 착지하게 한다.

**Architecture:** 깊이 셈을 `internal/judge` 의 순수 함수 `RelativeTo` 한 벌로 두고, `api` 와 `web` 이 각각 두 줄짜리 `seeOther` 헬퍼로 그 값을 `Location` 에 직접 쓴다. `http.Redirect` 는 상대 URL 을 절대화하므로 쓰지 않는다. 호출자는 절대 목표만 말하고 상대화는 헬퍼가 한 번만 한다.

**Tech Stack:** Go 1.x 표준 라이브러리만. `net/url` 의 `ResolveReference`(RFC 3986)로 브라우저 해석을 시험에서 재현하고, `net/http/httputil.ReverseProxy` 로 접두 프록시를 시험 안에 세운다.

**설계 근거:** `docs/superpowers/specs/2026-08-12-proxy-prefix-redirect-design.md`

## Global Constraints

- **작업 트리는 워크트리다.** 모든 명령의 기준은 `/home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-login-redirect-escapes-proxy-prefix` 이고, `main` 에 직접 커밋하지 않는다.
- **Go 모듈 루트는 `plugins/flightdeck/server`** 다. 모든 `go` 명령은 그 디렉토리에서 돈다.
- **관문의 무출력은 통과가 아니다.** `gofmt -l` 과 `go vet` 은 아무것도 안 봤을 때와 통과했을 때가 화면에서 같다. 그래서 모든 검증 단계는 `cd <절대경로> && pwd` 로 **어디서 돌았는지를 출력에 남긴다.**
- **교차 빌드 검증은 `go vet` 으로 한다.** `go build` 는 `_test.go` 를 컴파일 대상에 안 넣어 시험 코드에 대해 열려 있다.
- 주석·커밋 메시지는 **한글**이다. 기존 파일의 `★` 근거 표기를 따른다.
- 커밋 메시지 끝에 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>` 를 넣는다.
- 새로 쓰는 주석은 **무엇을 하는지가 아니라 왜 그런지**를 적는다. 이 저장소의 기존 주석이 그 형태이고, 이 항목 자체가 거짓 주석 하나에서 나왔다.

**공용 상수 — 계획 전체가 같은 값을 쓴다:**

- 시험용 접두: `/dcp-dev-board`
- 시험용 호스트: `http://fd.example`
- 시험용 토큰: `s3cret` (기존 `api/login_test.go` 와 같은 값)

---

### Task 1: `judge.RelativeTo` — 깊이 셈 하나

**Files:**
- Create: `plugins/flightdeck/server/internal/judge/relative.go`
- Create: `plugins/flightdeck/server/internal/judge/relative_test.go`

**Interfaces:**
- Consumes: 없음(표준 라이브러리 `strings` 만)
- Produces: `func RelativeTo(from, to string) string` — `from` 은 응답을 내는 요청의 경로, `to` 는 목표 **절대경로**, 반환은 상대 참조. Task 2·3·4 가 이 함수를 부른다.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`plugins/flightdeck/server/internal/judge/relative_test.go` 를 새로 만든다:

```go
package judge

import (
	"net/url"
	"testing"
)

// TestRelativeTo 는 깊이 셈을 표로 잠근다.
func TestRelativeTo(t *testing.T) {
	cases := []struct{ from, to, want string }{
		// 로그인 넷 — 기준이 언제나 /login · /logout 이라 깊이가 0 이다.
		{"/login", "/", "./"},
		{"/login", "/?project=kweiza", "./?project=kweiza"},
		{"/login", "/events", "./events"},
		{"/logout", "/", "./"},
		// 화면 쓰기 — /actions/* 는 깊이가 1 이다.
		{"/actions/reclaim", "/?notice=reclaim", "../?notice=reclaim"},
		{"/actions/reclaim", "/login", "../login"},
		{"/actions/", "/login", "../login"}, // 뒤 슬래시면 마디가 하나 더다
		{"/actions/reclaim/", "/", "../../"},
		{"/api/v1/items/next", "/login", "../../../login"},
		{"/a//b", "/login", "../../login"}, // 빈 마디도 해석이 한 마디로 센다
		// 못 읽은 from 은 뿌리로 접는다 — 과하게 올라가면 접두 밖으로 나간다.
		{"", "/login", "./login"},
		{"*", "/login", "./login"},
		// to 가 이 서버 안의 절대경로가 아니면 뿌리로 접는다.
		{"/login", "//evil.example/x", "./"},
		{"/login", "https://evil.example/x", "./"},
		{"/login", "", "./"},
		{"/login", "relative", "./"},
	}
	for _, c := range cases {
		if got := RelativeTo(c.from, c.to); got != c.want {
			t.Errorf("RelativeTo(%q, %q) = %q, 기대 %q", c.from, c.to, got, c.want)
		}
	}
}

// TestRelativeToLandsInsideProxyPrefix 는 그 값이 **접두 안에 착지하는지** 본다.
//
// ★ 기대값을 손으로 적는 것만으로는 부족하다 — 표가 틀리면 코드와 함께 틀린다.
// 그래서 각 줄을 접두가 붙은 문서 URL 에 실제로 해석한다(RFC 3986 그대로인
// url.ResolveReference). 브라우저가 하는 계산이 그것이다.
func TestRelativeToLandsInsideProxyPrefix(t *testing.T) {
	const prefix = "/dcp-dev-board"
	cases := []struct{ from, to, wantPath, wantQuery string }{
		{"/login", "/", "/", ""},
		{"/login", "/?project=kweiza", "/", "project=kweiza"},
		{"/logout", "/", "/", ""},
		{"/actions/reclaim", "/?notice=reclaim", "/", "notice=reclaim"},
		{"/actions/reclaim", "/login", "/login", ""},
		{"/api/v1/items/next", "/login", "/login", ""},
	}
	for _, c := range cases {
		base, err := url.Parse("http://fd.example" + prefix + c.from)
		if err != nil {
			t.Fatalf("문서 URL 파싱 실패(%q): %v", c.from, err)
		}
		ref, err := url.Parse(RelativeTo(c.from, c.to))
		if err != nil {
			t.Fatalf("상대 참조 파싱 실패(%q→%q): %v", c.from, c.to, err)
		}
		got := base.ResolveReference(ref)
		if want := prefix + c.wantPath; got.Path != want {
			t.Errorf("%q 에서 %q 로 가면 %q 에 착지한다 — %q 여야 한다(접두 밖이면 배포가 깨진다)",
				c.from, c.to, got.Path, want)
		}
		if got.RawQuery != c.wantQuery {
			t.Errorf("%q 에서 %q 로 갈 때 질의가 %q 다 — %q 여야 한다",
				c.from, c.to, got.RawQuery, c.wantQuery)
		}
	}
}

// TestRelativeToWithoutPrefixIsUnchanged 는 접두가 **없는** 배포가 그대로인지 본다.
//
// ★ 이 축이 없으면 접두 대응이 기본 배포를 깨뜨려도 안 보인다. 이 서버의 기본
// 배포는 포트 직결(compose.yaml)이고 그쪽이 다수다.
func TestRelativeToWithoutPrefixIsUnchanged(t *testing.T) {
	cases := []struct{ from, to, wantPath string }{
		{"/login", "/", "/"},
		{"/login", "/?project=kweiza", "/"},
		{"/actions/reclaim", "/?notice=reclaim", "/"},
	}
	for _, c := range cases {
		base, err := url.Parse("http://fd.example" + c.from)
		if err != nil {
			t.Fatalf("문서 URL 파싱 실패(%q): %v", c.from, err)
		}
		ref, err := url.Parse(RelativeTo(c.from, c.to))
		if err != nil {
			t.Fatalf("상대 참조 파싱 실패(%q→%q): %v", c.from, c.to, err)
		}
		if got := base.ResolveReference(ref); got.Path != c.wantPath {
			t.Errorf("접두 없는 배포에서 %q→%q 가 %q 에 착지한다 — %q 여야 한다",
				c.from, c.to, got.Path, c.wantPath)
		}
	}
}
```

- [ ] **Step 2: 시험이 실패하는지 확인한다**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-login-redirect-escapes-proxy-prefix/plugins/flightdeck/server && pwd
go test ./internal/judge/ -run TestRelativeTo -count=1
```

기대: 컴파일 실패 — `undefined: RelativeTo`

- [ ] **Step 3: 최소 구현을 쓴다**

`plugins/flightdeck/server/internal/judge/relative.go` 를 새로 만든다:

```go
package judge

import "strings"

// RelativeTo 는 from 에서 응답할 때 to 로 가는 **상대 참조**다. 순수 함수다.
//
// ★ 이 함수가 있는 이유. 경로 접두를 벗겨 넘기는 리버스 프록시(`/dcp-dev-board/` 같은 것)
// 뒤에서는 서버가 접두를 모른다. 경로만 있는 절대경로(`/`)를 Location 이나 폼 action 에
// 실으면 브라우저가 접두 **밖**으로 나가고, 프록시는 응답 헤더를 고쳐 쓰지 않는다.
// 상대 참조는 접두를 **몰라도** 맞는 자리를 가리킨다 — 접두를 서버 설정으로 받는 처방을
// 기각한 이유가 이것이다(아는 순간 그 값이 배포와 어긋날 자리가 생긴다).
//
// ★ 깊이는 **마지막 슬래시까지의 슬래시 수**로 센다. RFC 3986 의 상대 해석이 문서 URL 의
// 마지막 마디를 버리고 남은 자리에 붙이기 때문이다(`/a/b` 의 기준은 `/a/`).
// 빈 마디(`//`)도 한 마디로 센다 — 해석 알고리즘이 그것을 마디로 세므로, 정규화해서
// 걷어내면 그만큼 덜 올라가 다시 없는 자리를 가리킨다.
//
// ★ **`./` 를 언제나 붙인다.** 생략하면 쿼리만 있는 목표에서 조용히 틀린다 —
// `?project=x` 는 상대 참조로서 base 의 **경로를 유지**하므로 `/dcp-dev-board/login?project=x`
// 에 착지한다. 로그인 화면으로 되돌아가는 것이고, 증상은 "토큰이 맞는데 로그인이 안 된다"
// 로 보인다. 규칙을 하나로 두어 그 함정을 구조에서 없앤다.
func RelativeTo(from, to string) string {
	// ★ to 는 이 서버 안의 절대경로여야 한다. `//` 로 시작하면 스킴 상대 URL 이라
	// 브라우저가 다른 호스트로 나간다 — 호출부의 JudgeNext 가 이미 막지만, 순수 함수는
	// 자기 방어를 진다. 못 읽은 것은 통과시키지 않고 뿌리로 접는다.
	if !strings.HasPrefix(to, "/") || strings.HasPrefix(to, "//") {
		return "./"
	}
	rest := to[1:]

	// ★ 슬래시가 아예 없는 from(빈 문자열 · OPTIONS 의 `*`)은 뿌리로 본다. 못 읽은 것을
	// 깊이 0 으로 접는 쪽이 안전하다 — 그 경우의 최악은 "이 이상한 자리에서만 안 통한다"
	// 이고, 반대로 과하게 올라가면 접두 **밖**으로 나가 프록시 배포 전체가 깨진다.
	depth := 0
	if slash := strings.LastIndex(from, "/"); slash >= 0 {
		depth = strings.Count(from[:slash+1], "/") - 1
	}
	if depth <= 0 {
		return "./" + rest
	}
	return strings.Repeat("../", depth) + rest
}
```

- [ ] **Step 4: 시험이 통과하는지 확인한다**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-login-redirect-escapes-proxy-prefix/plugins/flightdeck/server && pwd
gofmt -l .
go test ./internal/judge/ -run TestRelativeTo -count=1
```

기대: `gofmt -l .` 무출력, 시험 `ok`

- [ ] **Step 5: 커밋한다**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-login-redirect-escapes-proxy-prefix
git add plugins/flightdeck/server/internal/judge/relative.go plugins/flightdeck/server/internal/judge/relative_test.go
git commit -F - <<'EOF'
feat(flightdeck): 깊이 셈을 judge 에 한 벌 둔다 — 상대 참조는 접두를 몰라도 맞는다

접두를 벗기는 프록시 뒤에서 경로만 있는 절대경로는 접두 밖으로 나간다.
상대 참조는 접두를 모르고도 맞는 자리를 가리키므로, 접두를 설정으로 받는
처방(FD_BASE_PATH 류)을 기각하고 이 셈 하나만 둔다.

./ 를 언제나 붙인다. 생략하면 쿼리만 있는 목표에서 base 의 경로가 유지되어
/dcp-dev-board/login?project=x 에 착지한다 — 로그인 화면으로 되돌아가는 것이고
증상은 "토큰이 맞는데 로그인이 안 된다" 로 보인다.

시험은 기대값 표만 잠그지 않는다. 각 줄을 접두가 붙은 문서 URL 에 실제로
해석해서(url.ResolveReference, RFC 3986) 착지 지점을 본다. 접두가 없는 배포가
그대로인지도 함께 본다 — 이 서버의 기본 배포는 포트 직결이고 그쪽이 다수다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
```

---

### Task 2: `JudgeLoginAction` 을 위임으로 바꾼다

**Files:**
- Modify: `plugins/flightdeck/server/internal/api/auth.go:208-241`
- Modify: `plugins/flightdeck/server/internal/api/pure_test.go:734-760` (기대값 표)
- Modify: `plugins/flightdeck/server/internal/api/login_test.go:114` (재시도 폼의 `Action` 단정 — **실제로 빨개진다**)
- Modify: `plugins/flightdeck/server/internal/web/login_test.go:13,27,72` (시험 데이터를 현실과 맞춘다 — 안 깨진다)

**Interfaces:**
- Consumes: Task 1 의 `judge.RelativeTo(from, to string) string`
- Produces: `JudgeLoginAction(path string) string` — 시그니처 그대로, 반환값만 `login` → `./login` 으로 바뀐다. 호출자 두 자리(`middleware.go:183`·`login.go:166`)는 안 고친다.

- [ ] **Step 1: 기대값 표를 새 규칙으로 고친다 (실패하는 시험)**

`plugins/flightdeck/server/internal/api/pure_test.go` 의 `TestJudgeLoginAction` 안 `cases` 맵을 통째로 아래로 바꾼다. **`./` 접두가 붙는 것이 유일한 변화다** — `../` 가 붙는 줄은 그대로다:

```go
	cases := map[string]string{
		"/":                     "./login",
		"/login":                "./login", // 재시도 폼 — 제출이 실패한 자리도 뿌리 깊이다
		"/events":               "./login",
		"/actions/reclaim":      "../login",
		"/actions/lane-release": "../login",
		"/actions/":             "../login", // 뒤 슬래시면 마디가 하나 더다
		"/api/v1/items/next":    "../../../login",
		"/a//b":                 "../../login", // 빈 마디도 해석이 한 마디로 센다
		"":                      "./login",     // 못 읽은 것은 뿌리로 접는다
		"*":                     "./login",     // OPTIONS * — 슬래시가 없다
	}
```

- [ ] **Step 2: 시험이 실패하는지 확인한다**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-login-redirect-escapes-proxy-prefix/plugins/flightdeck/server && pwd
go test ./internal/api/ -run TestJudgeLoginAction -count=1
```

기대: FAIL — `JudgeLoginAction("/") = "login", 기대 "./login"` 같은 줄이 여러 개

- [ ] **Step 3: 몸통을 위임으로 바꾼다**

`plugins/flightdeck/server/internal/api/auth.go` 에서 `JudgeLoginAction` 의 **주석과 몸통 전체**(208행 `// JudgeLoginAction 은…` 부터 241행 `}` 까지)를 아래로 바꾼다:

```go
// JudgeLoginAction 은 이 경로에서 뜬 폼의 action 이 가리켜야 할 **상대경로**다. 순수 함수다.
//
// ★ 절대경로(`/login`)로 두면 리버스 프록시의 경로 접두 뒤에서 브라우저가 원점의 /login 을
// 찾아가고 프록시는 그 경로를 모른다. 그런데 상대경로는 **문서 URL 의 깊이**에 붙으므로
// 뿌리가 아닌 자리에서 뜬 폼은 /actions/login 같은 없는 곳으로 간다. 그 자리도 세션 이전
// 경로가 아니라 다시 401 이 나고, 사람은 토큰을 정확히 쳐도 같은 폼을 무한히 다시 본다.
// 401 폼은 POST /actions/* 에서도 뜨므로(JudgeLoginScreen 이 메서드를 안 본다) 이것은
// 가설이 아니라 그 설계가 명시로 든 시나리오다.
//
// ★ **깊이 셈은 여기 없다.** judge.RelativeTo 한 벌이 그것을 진다 — 같은 셈이 폼 action 과
// 리다이렉트 Location 양쪽에 필요한데, 두 벌이 되면 반드시 표류하고 그 표류의 증상이
// 바로 위 문단의 무한 폼이다. 이 함수가 남아 있는 이유는 이름이 의도를 말하고
// `"/login"` 이 한 자리에 있기 때문이다 — 없애면 그 리터럴이 호출자 둘로 흩어진다.
func JudgeLoginAction(path string) string {
	// 라우트 등록(api.go 의 "POST /login")의 경로다.
	return judge.RelativeTo(path, "/login")
}
```

같은 파일의 import 블록에 `judge` 를 더한다. **`api` → `judge` 는 이 저장소에 없던 방향이다**(순환은 없다 — `judge` 는 `model` 만 문다):

```go
	"github.com/kweiza/flightdeck/internal/judge"
```

- [ ] **Step 4: 시험이 통과하는지 확인한다**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-login-redirect-escapes-proxy-prefix/plugins/flightdeck/server && pwd
gofmt -l .
go test ./internal/api/ -run TestJudgeLoginAction -count=1
go test ./internal/api/ -run TestLoginFormActionReachesLoginRoute -count=1
```

기대: 둘 다 `ok`. 두 번째 시험은 값이 `login` 에서 `./login` 로 바뀌어도 `ResolveReference` 로 풀어서 보므로 **안 고쳐도 통과해야 한다.** 여기서 빨개지면 `RelativeTo` 의 깊이 셈이 기존 셈과 어긋난 것이니 Task 1 로 돌아간다.

- [ ] **Step 5: 남은 `Action` 단정을 맞춘다 — `api` 하나는 진짜 깨진다**

먼저 `plugins/flightdeck/server/internal/api/login_test.go:114`, `TestLoginRejectsWrongTokenWithoutEcho` 안이다. **이것은 실제로 빨개진다** — 재시도 폼을 채우는 자리가 `withAuth` 와 `loginRefused` 둘인데 이 시험이 뒤엣것을 잰다:

```go
	if gotView.Action != "./login" {
		t.Fatalf("재시도 폼의 action 이 %q 다 — /login 에서 뜬 폼이라 \"./login\" 이어야 한다", gotView.Action)
	}
```

★ 이 시험이 재는 축은 값이 아니라 **`Action` 이 채워졌는가**다. 위 주석이 그 이유를 적어 뒀다 — 안 채우면 `action=""` 이 되어 폼이 문서 URL 자신으로 제출되고, 틀린 토큰을 한 번 친 사람만 무한 폼에 갇힌다. 그 축은 그대로 살아 있고 기대 문자열만 바뀐다.

아래 셋은 **안 고쳐도 통과한다** — `Action` 값을 시험이 직접 주입해서 `api` 의 셈을 안 타기 때문이다. 그래도 고친다. 그 문자열이 다음 사람에게 실물의 예시로 읽히기 때문이다.

`plugins/flightdeck/server/internal/web/login_test.go:13` 에서:

```go
		LoginView{Error: "토큰이 일치하지 않는다", Next: "/?project=kweiza", Action: "./login"})
```

같은 파일 27행의 단정도 맞춘다:

```go
		`action="./login"`, // 상대경로다 — 프록시 접두 뒤에서도 맞는 자리를 가리킨다
```

같은 파일 72행의 표에서 첫 값만 바꾼다:

```go
	for _, action := range []string{"./login", "../login", "../../../login"} {
```

- [ ] **Step 6: 두 패키지 시험을 다 돌린다**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-login-redirect-escapes-proxy-prefix/plugins/flightdeck/server && pwd
gofmt -l .
go vet ./internal/api/ ./internal/web/ ./internal/judge/
go test ./internal/api/ ./internal/web/ ./internal/judge/ -count=1
```

기대: `gofmt -l .` 과 `go vet` 무출력, 시험 셋 다 `ok`

- [ ] **Step 7: 커밋한다**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-login-redirect-escapes-proxy-prefix
git add plugins/flightdeck/server/internal/api/auth.go plugins/flightdeck/server/internal/api/pure_test.go plugins/flightdeck/server/internal/api/login_test.go plugins/flightdeck/server/internal/web/login_test.go
git commit -F - <<'EOF'
refactor(flightdeck): 폼 action 의 깊이 셈을 judge 에 위임한다

같은 셈이 폼 action 과 리다이렉트 Location 양쪽에 필요해졌다. 두 벌이 되면
반드시 표류하고, 그 표류의 증상은 "토큰을 정확히 쳐도 폼이 무한히 다시 뜬다"
라서 원인이 안 보인다 — web/login.go 가 이미 명시로 경계한 그 축이다.

함수를 없애지 않고 몸통만 위임한 이유는 "/login" 리터럴이다. 없애면 그 값이
호출자 둘(middleware.go·login.go)로 흩어진다. 이름이 의도를 말하는 쪽을 고른다.

api → judge 는 이 저장소에 없던 의존 방향이다. judge 는 model 만 물고 model 은
아무것도 안 물어 순환이 없다(go list -deps 로 확인).

반환값이 login → ./login 으로 바뀐다. 브라우저 해석은 동일하다.
TestLoginFormActionReachesLoginRoute 는 ResolveReference 로 풀어서 보므로
안 고쳤다 — 그 시험이 값이 아니라 착지를 재고 있었다는 뜻이다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
```

---

### Task 3: `api` 의 리다이렉트 넷

**Files:**
- Modify: `plugins/flightdeck/server/internal/api/login.go:68-106,142-149` (헬퍼 추가 + 네 자리)
- Modify: `plugins/flightdeck/server/internal/api/login_test.go:77,179` (기존 단정 둘)
- Modify: `plugins/flightdeck/server/internal/api/login_test.go` (2층 시험 추가)

**Interfaces:**
- Consumes: Task 1 의 `judge.RelativeTo`. Task 2 가 이미 `api` 에 `judge` import 를 넣었다.
- Produces: `func (s *server) seeOther(w http.ResponseWriter, r *http.Request, to string)` — `api` 패키지 안에서만 쓴다.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`plugins/flightdeck/server/internal/api/login_test.go` 끝에 아래를 더한다:

```go
// TestLoginRedirectsLandInsideProxyPrefix 는 로그인·로그아웃의 Location 이
// **접두 안에** 착지하는지 본다.
//
// ★ 회귀를 붙드는 것은 이 시험이다. 누가 http.Redirect 로 되돌리면 Location 이
// 경로만 있는 절대경로가 되어 여기서 "/" 에 착지하고 빨개진다. 그 함수는 상대 URL 을
// 받아도 요청 경로 기준으로 절대화하므로(net/http server.go), "./" 를 줘도 마찬가지다.
func TestLoginRedirectsLandInsideProxyPrefix(t *testing.T) {
	const prefix = "/dcp-dev-board"
	cases := []struct {
		name      string
		req       func(t *testing.T) *http.Request
		docPath   string // 브라우저가 보는 문서 경로(접두 뒤)
		wantPath  string // 접두를 포함한 착지 경로
		wantQuery string
	}{
		{
			name: "로그인 성공은 next 로 간다",
			req: func(t *testing.T) *http.Request {
				return loginPost(t, "/login", url.Values{
					"token": {"s3cret"}, "next": {"/?project=kweiza"},
				})
			},
			docPath: "/login", wantPath: prefix + "/", wantQuery: "project=kweiza",
		},
		{
			name: "next 가 없으면 뿌리로 간다",
			req: func(t *testing.T) *http.Request {
				return loginPost(t, "/login", url.Values{"token": {"s3cret"}})
			},
			docPath: "/login", wantPath: prefix + "/",
		},
		{
			name: "GET /login 은 뿌리로 보낸다",
			req: func(t *testing.T) *http.Request {
				req := httptest.NewRequest("GET", "/login", nil)
				req.RemoteAddr = "203.0.113.9:1"
				return req
			},
			docPath: "/login", wantPath: prefix + "/",
		},
		{
			name: "로그아웃은 뿌리로 간다",
			req: func(t *testing.T) *http.Request {
				return loginPost(t, "/logout", url.Values{})
			},
			docPath: "/logout", wantPath: prefix + "/",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := NewServer(nil, Options{Token: "s3cret"})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, c.req(t))
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("상태가 %d 다 — 303 이어야 한다\n%s", rec.Code, rec.Body.String())
			}
			loc := rec.Header().Get("Location")
			if loc == "" {
				t.Fatal("Location 이 비었다")
			}
			base, err := url.Parse("http://fd.example" + prefix + c.docPath)
			if err != nil {
				t.Fatalf("문서 URL 파싱 실패: %v", err)
			}
			ref, err := url.Parse(loc)
			if err != nil {
				t.Fatalf("Location %q 파싱 실패: %v", loc, err)
			}
			got := base.ResolveReference(ref)
			if got.Path != c.wantPath {
				t.Errorf("Location %q 가 %q 에 착지한다 — %q 여야 한다. "+
					"접두 밖이면 로그인은 됐는데 화면을 못 본다", loc, got.Path, c.wantPath)
			}
			if got.RawQuery != c.wantQuery {
				t.Errorf("질의가 %q 다 — %q 여야 한다", got.RawQuery, c.wantQuery)
			}
		})
	}
}
```

- [ ] **Step 2: 시험이 실패하는지 확인한다**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-login-redirect-escapes-proxy-prefix/plugins/flightdeck/server && pwd
go test ./internal/api/ -run TestLoginRedirectsLandInsideProxyPrefix -count=1
```

기대: FAIL — 네 갈래 전부 `Location "/" 가 "/" 에 착지한다 — "/dcp-dev-board/" 여야 한다`

- [ ] **Step 3: `seeOther` 를 넣고 네 자리를 옮긴다**

`plugins/flightdeck/server/internal/api/login.go` 의 `handleLogin` **바로 위**(64행 `// handleLogin 은…` 주석 앞)에 헬퍼를 더한다:

```go
// seeOther 는 303 과 함께 **상대 참조**를 Location 에 싣는다.
//
// ★ **http.Redirect 를 안 쓴다.** 그 함수는 상대 URL 을 받아도 요청 경로 기준으로
// 절대화해서 내보낸다(net/http server.go 의 olddir+url → path.Clean). 그래서 `./` 를
// 줘도 `/` 가 나가고, 접두를 벗기는 리버스 프록시 뒤에서 그 값은 접두 **밖**으로
// 떨어진다 — 로그인은 성공했는데 화면을 못 보는 그 결함이다.
//
// ★ **to 는 절대경로로 준다.** 상대화는 여기서 한 번만 한다. 호출자가 `../` 를 손으로
// 박으면 새 라우트가 깊이를 바꿀 때 조용히 틀리고, 그 값이 http.Redirect 를 지나며
// 절대화되면 틀린 사실조차 안 보인다(web/actions.go 가 정확히 그 모양이었다).
//
// ★ 본문을 안 쓴다. http.Redirect 는 GET 에 <a href> 한 줄을 붙이는데, 303 을 못 따르는
// 옛 클라이언트를 위한 것이고 이 화면의 대상이 아니다.
func (s *server) seeOther(w http.ResponseWriter, r *http.Request, to string) {
	w.Header().Set("Location", judge.RelativeTo(r.URL.Path, to))
	w.WriteHeader(http.StatusSeeOther)
}
```

그리고 네 자리를 바꾼다. **값은 그대로 두고 함수만 바꾼다** — 상대화는 헬퍼가 한다:

| 위치 | 지금 | 바꿀 것 |
|---|---|---|
| `handleLogin` GET 갈래 | `http.Redirect(w, r, "/", http.StatusSeeOther)` | `s.seeOther(w, r, "/")` |
| `handleLogin` 토큰 미설정 갈래 | `http.Redirect(w, r, next, http.StatusSeeOther)` | `s.seeOther(w, r, next)` |
| `handleLogin` 성공 갈래 | `http.Redirect(w, r, next, http.StatusSeeOther)` | `s.seeOther(w, r, next)` |
| `handleLogout` | `http.Redirect(w, r, "/", http.StatusSeeOther)` | `s.seeOther(w, r, "/")` |

- [ ] **Step 4: 기존 단정 둘을 새 값으로 고친다**

`plugins/flightdeck/server/internal/api/login_test.go:77` (`TestLoginSetsCookieAndRedirects` 안):

```go
	if loc := rec.Header().Get("Location"); loc != "./?project=kweiza" {
```

같은 파일 179행 (`TestLoginGetRedirectsHome` 안):

```go
	if loc := rec.Header().Get("Location"); loc != "./" {
```

- [ ] **Step 5: 시험이 통과하는지 확인한다**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-login-redirect-escapes-proxy-prefix/plugins/flightdeck/server && pwd
gofmt -l .
go vet ./internal/api/
go test ./internal/api/ -count=1
```

기대: 무출력 둘, 시험 `ok`. `http.Redirect` 를 다 걷어냈다면 `login.go` 에 `net/http` import 는 남는다(`http.ResponseWriter`·`http.StatusSeeOther` 등을 계속 쓴다).

- [ ] **Step 6: 커밋한다**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-login-redirect-escapes-proxy-prefix
git add plugins/flightdeck/server/internal/api/login.go plugins/flightdeck/server/internal/api/login_test.go
git commit -F - <<'EOF'
fix(flightdeck): 로그인·로그아웃 리다이렉트가 접두 안에 착지한다

handleLogin·handleLogout 의 Location 이 "/" 또는 "/?project=x" 라 경로만 있는
절대경로였다. 접두를 벗기는 프록시는 그 헤더를 고쳐 쓰지 않으므로 브라우저가
접두 밖으로 나갔다 — 로그인은 성공했는데 화면을 못 본다.

http.Redirect 를 버린다. 그 함수는 상대 URL 을 받아도 요청 경로 기준으로
절대화해서 내보내므로(net/http server.go), "./" 를 줘도 "/" 가 나간다.
seeOther 가 judge.RelativeTo 의 값을 Location 에 직접 쓴다.

시험은 값만 보지 않는다. 접두가 붙은 문서 URL 에 Location 을 해석해서
착지 지점을 본다 — 누가 http.Redirect 로 되돌리면 여기서 빨개진다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
```

---

### Task 4: `web` 의 리다이렉트 둘 — 그리고 거짓 주석

**Files:**
- Modify: `plugins/flightdeck/server/internal/web/actions.go:355-365` (`back()` — 주석과 몸통)
- Modify: `plugins/flightdeck/server/internal/web/actions.go:515-528` (project-view 갈래)
- Modify: `plugins/flightdeck/server/internal/web/actions_test.go:71,124`

**Interfaces:**
- Consumes: Task 1 의 `judge.RelativeTo`. `web` 은 이미 `judge` 를 import 한다(`format.go`) — 이 파일에는 새로 넣어야 한다.
- Produces: `func (h *handler) seeOther(w http.ResponseWriter, r *http.Request, to string)` — `web` 패키지 안에서만 쓴다.

- [ ] **Step 1: 기존 시험 둘을 브라우저 해석으로 고친다 (실패하는 시험)**

`plugins/flightdeck/server/internal/web/actions_test.go` 에 헬퍼를 더한다(파일 안 아무 곳, 첫 `func Test` 앞이 좋다):

```go
// resolveFrom 은 Location 을 **브라우저처럼** 푼다.
//
// ★ Location 을 요청 URL 로 그대로 쓰면 안 된다. 이 서버의 리다이렉트는 상대 참조라
// (`../?notice=…`) 요청 경로로 성립하지 않는다. 브라우저는 문서 URL 을 기준으로 그것을
// 풀고, url.ResolveReference 가 그 규칙(RFC 3986)의 구현이다.
//
// ★ 접두를 일부러 씌운다. 접두 뒤 배포에서 착지가 접두 **안**인지가 이 축의 전부이고,
// 접두 없이 풀면 그 사실을 영영 안 재게 된다.
func resolveFrom(t *testing.T, docPath, loc string) *url.URL {
	t.Helper()
	const prefix = "/dcp-dev-board"
	if loc == "" {
		t.Fatal("Location 이 비었다")
	}
	base, err := url.Parse("http://fd.example" + prefix + docPath)
	if err != nil {
		t.Fatalf("문서 URL 파싱 실패(%q): %v", docPath, err)
	}
	ref, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("Location %q 파싱 실패: %v", loc, err)
	}
	got := base.ResolveReference(ref)
	if !strings.HasPrefix(got.Path, prefix+"/") {
		t.Fatalf("Location %q 가 %q 에 착지한다 — 접두 %q 밖이다. "+
			"쓰기는 됐는데 화면으로 못 돌아온다", loc, got.Path, prefix)
	}
	return got
}
```

`actions_test.go:71` 의 단정(`TestReclaimReleasesClaimAndLeavesJudgment` 안)을 바꾼다:

```go
	loc := rec.Header().Get("Location")
	landed := resolveFrom(t, "/actions/reclaim", loc)
	if landed.Path != "/dcp-dev-board/" || !strings.Contains(landed.RawQuery, "notice=reclaim") {
		t.Fatalf("Location = %q → %q — 화면으로 되돌리지 않았다", loc, landed)
	}

	// 리다이렉트를 따라간 화면이 소비자 좌표계다. 접두를 벗긴 자리가 서버가 보는 경로다.
	req := httptest.NewRequest(http.MethodGet, "/"+strings.TrimPrefix(landed.RequestURI(), "/dcp-dev-board/"), nil)
```

같은 파일 124행, `TestDropMarksItemDroppedWithReason` 안의 자리도 바꾼다:

```go
	landed := resolveFrom(t, "/actions/drop", rec.Header().Get("Location"))
	req := httptest.NewRequest(http.MethodGet, "/"+strings.TrimPrefix(landed.RequestURI(), "/dcp-dev-board/"), nil)
```

import 에 `net/url` 이 없으면 더한다.

- [ ] **Step 2: 시험이 실패하는지 확인한다**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-login-redirect-escapes-proxy-prefix/plugins/flightdeck/server && pwd
go test ./internal/web/ -run 'TestReclaim|TestDrop' -count=1
```

기대: FAIL — `Location "/?project=…" 가 "/" 에 착지한다 — 접두 "/dcp-dev-board" 밖이다`

★ 이 실패가 §1 의 발견을 재현한 것이다. 소스에는 `"../?"` 라고 쓰여 있는데 나가는 값이 `"/?"` 다.

- [ ] **Step 3: `seeOther` 를 넣고 두 자리를 옮긴다 — 거짓 주석부터 고친다**

`plugins/flightdeck/server/internal/web/actions.go` 의 `back()` 을 통째로 아래로 바꾼다:

```go
// seeOther 는 303 과 함께 **상대 참조**를 Location 에 싣는다.
//
// ★ **http.Redirect 를 안 쓴다.** 그 함수는 상대 URL 을 받아도 요청 경로 기준으로
// 절대화해서 내보낸다(net/http server.go 의 olddir+url → path.Clean). 이 자리에는
// `"../?"` 라고 쓰여 있었는데 실제로 나간 값은 `"/?"` 였고, 아래 back() 의 옛 주석이
// "하위 경로에 마운트돼도 성립한다"고 적어 그 사실을 가렸다. 소스의 상대경로는
// 그 함수를 지나며 사라진다.
func (h *handler) seeOther(w http.ResponseWriter, r *http.Request, to string) {
	w.Header().Set("Location", judge.RelativeTo(r.URL.Path, to))
	w.WriteHeader(http.StatusSeeOther)
}

// back 은 화면으로 되돌린다(POST-리다이렉트-GET).
//
// ★ **뿌리를 절대경로로 말한다.** 깊이를 세는 것은 seeOther 안의 judge.RelativeTo 이고
// 이 자리는 목적지만 말한다. 앞선 판은 여기에 `"../"` 를 손으로 박았는데, 그러면
// /actions/x/y 처럼 깊이가 다른 라우트가 생길 때 조용히 틀린다.
//
// 알림은 **코드**로 넘기고 문장은 서버가 만든다(NoticeText).
func (h *handler) back(w http.ResponseWriter, r *http.Request, in ActionInput, code ActionKind) {
	q := url.Values{}
	q.Set("project", in.Project)
	q.Set("notice", string(code))
	q.Set("item", in.Item)
	h.seeOther(w, r, "/?"+q.Encode())
}
```

같은 파일 527행 근처(project-view 갈래)의 마지막 줄을 바꾼다:

```go
	h.seeOther(w, r, "/?"+q.Encode())
```

import 블록에 `judge` 를 더한다:

```go
	"github.com/kweiza/flightdeck/internal/judge"
```

- [ ] **Step 4: 시험이 통과하는지 확인한다**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-login-redirect-escapes-proxy-prefix/plugins/flightdeck/server && pwd
gofmt -l .
go vet ./internal/web/
go test ./internal/web/ -count=1
```

기대: 무출력 둘, 시험 `ok`

- [ ] **Step 5: `http.Redirect` 가 하나도 안 남았는지 확인한다**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-login-redirect-escapes-proxy-prefix/plugins/flightdeck/server && pwd
grep -rn 'http\.Redirect(' --include=*.go internal/ cmd/
```

기대: **무출력.** 한 줄이라도 나오면 그 자리가 접두 밖으로 떨어지는 일곱 번째 자리다 — 고치고 이 단계를 다시 돈다.

★ **여는 괄호까지 넣어야 한다.** `grep "http.Redirect"` 로는 "`http.Redirect` 를 안 쓴다"고 적은 **주석 다섯 줄**이 함께 잡힌다(`api/login.go`·`api/login_test.go`·`web/actions.go`). 그 주석들은 이 작업이 일부러 남긴 것이라 앞으로도 계속 잡힌다 — 호출부를 세는 관문이 주석을 세면 영영 빨간불이고, 그러면 사람이 그 관문을 무시하게 된다.

- [ ] **Step 6: 커밋한다**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-login-redirect-escapes-proxy-prefix
git add plugins/flightdeck/server/internal/web/actions.go plugins/flightdeck/server/internal/web/actions_test.go
git commit -F - <<'EOF'
fix(flightdeck): 화면 쓰기의 되돌림도 접두 안에 착지한다 — 주석이 그것을 가렸다

back() 의 주석은 "상대 경로로 보낸다 — 이 핸들러가 하위 경로에 마운트돼도 그대로
성립한다" 였다. 참이 아니다. 소스에는 "../?" 라고 쓰여 있는데 http.Redirect 가
그것을 "/?" 로 절대화해서 내보낸다. 큐 항목이 이 자리를 안전한 쪽으로 분류한
근거가 이 주석이었다 — 결함은 넷이 아니라 여섯이었다.

호출자는 이제 목적지만 말한다("/?"+q). 깊이는 seeOther 안의 judge.RelativeTo 가
센다. /actions/x/y 처럼 깊이가 다른 라우트가 생겨도 자동으로 맞는다.

시험도 고쳤다. Location 을 요청 URL 로 그대로 쓰던 자리가 있었는데, 상대 참조는
요청 경로로 성립하지 않는다. 접두를 씌운 문서 URL 에 ResolveReference 로 푼다 —
깨진 것이 옳다. 상대 Location 을 절대경로처럼 다루던 가정이 시험에도 박혀 있었다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
```

---

### Task 5: 접두 프록시 뒤에서 왕복한다

**Files:**
- Create: `plugins/flightdeck/server/cmd/fd/proxy_prefix_login_test.go`

**Interfaces:**
- Consumes: `serveAPIOptions(token string, ratePerMinute int, log *slog.Logger, inContainer bool, watcher *selfWatcher, ledgerJob *ledgerBackupJob) api.Options` 와 `buildHandler(svc *service.Service, webH http.Handler, opt api.Options) api.Handler` — 둘 다 `cmd/fd` 패키지의 기존 함수다. `watcher` 와 `ledgerJob` 은 `nil` 을 준다(둘 다 nil 검사가 있다).
- Produces: 없음(시험 전용)

★ **`serveAPIOptions` 를 반드시 거친다.** `api.Options` 를 손으로 만들면 `LoginScreen` 콜백이 `nil` 이라 폼이 아예 안 뜨고 JSON 401 이 온다. `cmd/fd/harness_test.go` 의 `newHarnessAuth` 가 그렇게 만들고 있어서 그 하네스로는 이 왕복을 못 잰다.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`plugins/flightdeck/server/cmd/fd/proxy_prefix_login_test.go` 를 새로 만든다:

```go
package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/api"
	"github.com/kweiza/flightdeck/internal/service"
	"github.com/kweiza/flightdeck/internal/store"
	"github.com/kweiza/flightdeck/internal/web"
)

// prefixStripper 는 경로 접두를 벗겨 뒤 서버로 넘긴다. nginx 의 그 배포를 본뜬다.
//
// ★ **Location 을 고쳐 쓰지 않는다.** nginx 가 경로만 있는 Location 을 안 고치는 것이
// 이 결함의 전제다 — 시험의 프록시가 그 일을 대신하면 재려는 것이 사라진다.
func prefixStripper(t *testing.T, prefix, upstreamURL string) http.Handler {
	t.Helper()
	target, err := url.Parse(upstreamURL)
	if err != nil {
		t.Fatalf("업스트림 URL 파싱 실패(%q): %v", upstreamURL, err)
	}
	rp := httputil.NewSingleHostReverseProxy(target)
	inner := rp.Director
	rp.Director = func(req *http.Request) {
		inner(req)
		req.URL.Path = strings.TrimPrefix(req.URL.Path, prefix)
		if req.URL.Path == "" {
			req.URL.Path = "/"
		}
	}
	return rp
}

// formAction 은 로그인 폼의 action 값을 꺼낸다. 템플릿의 그 한 줄(login.gohtml)이다.
func formAction(t *testing.T, html string) string {
	t.Helper()
	const marker = `<form method="post" action="`
	i := strings.Index(html, marker)
	if i < 0 {
		t.Fatalf("로그인 폼이 없다 — 본문 앞머리:\n%s", clipForTest(html))
	}
	rest := html[i+len(marker):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		t.Fatalf("action 이 안 닫혔다 — 본문 앞머리:\n%s", clipForTest(html))
	}
	return rest[:j]
}

func clipForTest(s string) string {
	if len(s) > 400 {
		return s[:400] + "…"
	}
	return s
}

// TestLoginRoundTripBehindPathPrefix 는 접두를 벗기는 프록시 뒤에서 로그인 왕복이
// **접두 안에** 착지하는지 본다.
//
// ★ 이 층이 재는 것은 값이 아니라 **왕복**이다. 폼 action 과 리다이렉트 Location 이
// 각각 맞아도 둘을 이어 붙였을 때 깨질 수 있다 — 앞선 판이 정확히 그렇게 깨졌다
// (폼은 떴는데 제출이 없는 자리로 가서 토큰을 정확히 쳐도 같은 폼이 무한히 떴다).
//
// ★ 배선을 재현하지 않고 serveAPIOptions 를 그대로 부른다. api.Options 를 손으로
// 만들면 LoginScreen 이 nil 이라 폼 대신 JSON 401 이 오고, 이 시험은 그 차이를
// "폼이 없다"로만 보게 된다.
func TestLoginRoundTripBehindPathPrefix(t *testing.T) {
	const prefix = "/dcp-dev-board"
	const token = "s3cret"

	quiet := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	st, err := store.Open(filepath.Join(t.TempDir(), "fd.db"))
	if err != nil {
		t.Fatalf("DB 를 못 열었다: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := service.New(st, quiet)

	opt := serveAPIOptions(token, 0, quiet, false, nil, nil)
	// ★ **루프백 면제를 끈다.** httptest 서버는 언제나 127.0.0.1 이고 프록시도 같은
	// 머신이라, 끄지 않으면 업스트림이 보는 RemoteAddr 이 루프백이라 인증 게이트가
	// 통째로 건너뛰어진다 — 첫 방문이 401 이 아니라 **200** 이 되고, 그러면 이 시험은
	// 로그인을 한 번도 안 거치고 초록이 된다(실측). 재려는 것이 로그인 왕복인데
	// 로그인이 아예 안 일어나는 것이 가장 나쁜 초록이다.
	//
	// ★ 이 한 줄이 가리키는 더 큰 축이 있다. `serveAPIOptions` 는 이 필드를 한 번도
	// 세팅하지 않아서 **운영 배포의 면제는 항상 켜져 있고 끌 길이 없다.** 판정은
	// RemoteAddr 이므로 같은 호스트의 리버스 프록시 뒤에서는 실제 배포도 루프백으로
	// 보인다. 그 축은 이 항목의 범위가 아니라 별도로 다룬다 — 여기서는 이 시험이
	// **인증이 실제로 켜진 서버**를 재게 만드는 것이 전부다.
	opt.RequireTokenOnLoopback = true
	upstream := httptest.NewServer(buildHandler(svc, web.New(svc, web.WithLogger(quiet)), opt))
	t.Cleanup(upstream.Close)

	proxy := httptest.NewServer(prefixStripper(t, prefix, upstream.URL))
	t.Cleanup(proxy.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("쿠키 자를 못 만들었다: %v", err)
	}
	client := &http.Client{Jar: jar}

	// ① 접두 뒤 첫 방문 — 폼이 떠야 한다.
	//
	// ★ Accept 를 손으로 싣는다. 브라우저는 언제나 보내고, 이 서버는 그 헤더로
	// HTML 폼과 JSON 401 을 가른다(JudgeLoginScreen). 안 실으면 401 은 오는데
	// 폼이 아니라 JSON 이 와서 이 시험이 폼을 못 찾는다 — Go 의 http.Client 는
	// Accept 를 자동으로 안 붙이므로 브라우저를 흉내내려면 이 줄이 필요하다.
	docURL := proxy.URL + prefix + "/"
	req0, err := http.NewRequest(http.MethodGet, docURL, nil)
	if err != nil {
		t.Fatalf("첫 방문 요청을 못 만들었다: %v", err)
	}
	req0.Header.Set("Accept", "text/html")
	resp, err := client.Do(req0)
	if err != nil {
		t.Fatalf("첫 방문 실패: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("첫 방문이 %d 다 — 401 이어야 한다(토큰을 켠 서버다)", resp.StatusCode)
	}
	action := formAction(t, string(body))

	// ② 폼 action 을 문서 URL 기준으로 푼다. 브라우저가 하는 계산이다.
	base, err := url.Parse(docURL)
	if err != nil {
		t.Fatalf("문서 URL 파싱 실패: %v", err)
	}
	ref, err := url.Parse(action)
	if err != nil {
		t.Fatalf("action %q 파싱 실패: %v", action, err)
	}
	submitURL := base.ResolveReference(ref)
	if !strings.HasPrefix(submitURL.Path, prefix+"/") {
		t.Fatalf("폼 action %q 가 %q 를 가리킨다 — 접두 %q 밖이다", action, submitURL.Path, prefix)
	}

	// ③ 거기에 토큰을 제출한다. 브라우저는 same-origin POST 에 Origin 을 싣는다.
	form := url.Values{"token": {token}, "next": {"/"}}
	req, err := http.NewRequest(http.MethodPost, submitURL.String(), strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("제출 요청을 못 만들었다: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html")
	req.Header.Set("Origin", proxy.URL)
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	resp2, err := client.Do(req)
	if err != nil {
		t.Fatalf("제출 실패: %v", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()

	// ④ 클라이언트가 303 을 따라간 **최종 자리**가 접두 안이어야 한다.
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("최종 상태가 %d 다 — 200 이어야 한다\n%s", resp2.StatusCode, clipForTest(string(body2)))
	}
	final := resp2.Request.URL
	if final.Path != prefix+"/" {
		t.Fatalf("최종 착지가 %q 다 — %q 여야 한다. 접두 밖이면 로그인은 됐는데 화면을 못 본다",
			final.Path, prefix+"/")
	}
	if !strings.Contains(string(body2), "<form") {
		t.Fatalf("착지한 화면에 폼이 하나도 없다 — 대시보드가 아니다\n%s", clipForTest(string(body2)))
	}
	if strings.Contains(string(body2), `name="token"`) {
		t.Fatalf("착지한 화면이 로그인 폼이다 — 쿠키가 안 실렸거나 되돌아왔다\n%s", clipForTest(string(body2)))
	}

	// ⑤ 쿠키가 프록시 오리진에 남았는가.
	proxyURL, _ := url.Parse(proxy.URL)
	var found bool
	for _, c := range jar.Cookies(proxyURL) {
		if c.Name == api.LoginCookieName {
			found = true
		}
	}
	if !found {
		t.Fatal("로그인 쿠키가 자에 없다 — 다음 요청이 다시 401 이 된다")
	}
}
```

- [ ] **Step 2: 시험을 돌린다 — 무엇이 깨지는지 본다**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-login-redirect-escapes-proxy-prefix/plugins/flightdeck/server && pwd
go test ./cmd/fd/ -run TestLoginRoundTripBehindPathPrefix -count=1 -v
```

기대: **Task 3·4 가 끝난 뒤라면 통과해야 한다.**

★ **여기서 ③이 403 으로 깨지면 그것은 새 발견이다.** 스펙 §11 이 열어 둔 위험 — 프록시 뒤에서 `JudgeScreenOrigin` 의 출처 대조가 성립하는지 아무도 잰 적이 없다. 그 경우 **고치지 말고 멈춘다.** 실패 출력을 그대로 들고 사람에게 판단을 받는다(별개 항목으로 낼지, 여기서 고칠지). 접두 배포의 두 번째 결함이고 이 항목의 범위가 아니다.

- [ ] **Step 3: 실패로 만들어 본다 — 시험이 정말 재는지 확인한다**

`internal/api/login.go` 의 성공 갈래를 잠깐 `http.Redirect(w, r, next, http.StatusSeeOther)` 로 되돌리고 다시 돌린다:

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-login-redirect-escapes-proxy-prefix/plugins/flightdeck/server && pwd
go test ./cmd/fd/ -run TestLoginRoundTripBehindPathPrefix -count=1
```

기대: FAIL — `최종 착지가 "/" 다 — "/dcp-dev-board/" 여야 한다`

★ 이 단계를 건너뛰지 마라. 아무것도 안 재는 시험이 초록인 것은 이 저장소가 이미 한 번 밟은 함정이다(`TestDashboardHasLogout` 이 로그아웃을 GET 으로 바꿔도 초록이었다). 확인했으면 **되돌린 것을 다시 되돌린다**(`git checkout -- internal/api/login.go` 가 아니라 손으로 `s.seeOther(w, r, next)` 로 고친다 — 그 파일에 이 태스크의 다른 변경은 없다).

- [ ] **Step 4: 전체 시험을 돌린다**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-login-redirect-escapes-proxy-prefix/plugins/flightdeck/server && pwd
gofmt -l .
go vet ./...
go test ./internal/... ./cmd/fd/ -count=1
```

기대: `gofmt -l .` 과 `go vet ./...` 무출력, 시험 전부 `ok`

- [ ] **Step 5: 커밋한다**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-login-redirect-escapes-proxy-prefix
git add plugins/flightdeck/server/cmd/fd/proxy_prefix_login_test.go
git commit -F - <<'EOF'
test(flightdeck): 접두 프록시 뒤에서 로그인 왕복을 잰다 — 이 축의 첫 시험이다

접두 뒤 배포를 재는 시험이 이 저장소에 하나도 없었다. 그래서 이 축의 회귀는
원리적으로 안 잡혔다 — 큐 항목이 "고칠 때 함께 볼 것"으로 짚은 그 공백이다.

프록시는 접두만 벗기고 Location 은 고쳐 쓰지 않는다. nginx 가 안 하는 그 일을
시험이 대신하면 재려는 것이 사라진다.

배선을 재현하지 않고 serveAPIOptions + buildHandler 를 그대로 부른다.
api.Options 를 손으로 만들면 LoginScreen 이 nil 이라 폼 대신 JSON 401 이 오고,
harness_test.go 의 newHarnessAuth 가 그렇게 만들고 있어 그 하네스로는 못 잰다.

왕복 전체를 본다 — 폼이 뜨고, 그 action 이 가리킨 자리에 제출이 통하고,
303 을 따라간 최종 URL 이 접두 안이고, 거기가 로그인 폼이 아니라 대시보드다.
값 넷이 각각 맞아도 이어 붙였을 때 깨질 수 있다(앞선 판이 그렇게 깨졌다).

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
```

---

### Task 6: 옛 스펙의 한계 항목을 닫는다

**Files:**
- Modify: `docs/superpowers/specs/2026-08-10-web-token-login-design.md:255-262`

**Interfaces:**
- Consumes: 없음
- Produces: 없음(문서)

- [ ] **Step 1: 한계 항목을 지우지 말고 닫는다**

`docs/superpowers/specs/2026-08-10-web-token-login-design.md` 에서 "경로 접두 뒤에서 로그인 성공 후 리다이렉트가 접두 밖으로 떨어진다" 로 시작하는 항목(255~262행)의 **끝에** 아래를 덧붙인다. 기존 문장은 한 글자도 지우지 않는다:

```markdown
  **2026-08-12 에 닫혔다** — `docs/superpowers/specs/2026-08-12-proxy-prefix-redirect-design.md`.
  이 문단의 처방("폼이 자기 문서 URL 로 상대화한 값을 싣게")은 **기각됐다**: 폼의 기준 문서
  URL(`/actions/reclaim`)과 리다이렉트의 기준(`/login`)이 달라서 깊이가 어긋난다. 대신 서버가
  응답하는 요청 경로를 기준으로 상대화한다(`judge.RelativeTo`). `X-Forwarded-Prefix` 기각은
  유효하고 근거가 둘 늘었다 — nginx 가 그 헤더를 기본으로 안 보내고, 안 고쳐졌다는 사실이
  증상으로도 안 드러난다.

  **결함은 넷이 아니라 여섯이었다.** 이 문단은 `actions.go` 의 `../?` 를 안전한 쪽으로
  분류했는데, `http.Redirect` 가 상대 URL 을 절대화해서 내보내므로 그 자리도 같은 결함이었다.
  그렇게 분류하게 만든 것은 `back()` 의 주석이다("하위 경로에 마운트돼도 그대로 성립한다").
```

- [ ] **Step 2: 커밋한다**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-login-redirect-escapes-proxy-prefix
git add docs/superpowers/specs/2026-08-10-web-token-login-design.md
git commit -F - <<'EOF'
docs(flightdeck): 옛 한계 항목을 지우지 않고 닫는다

그때 "안 고친다"고 판단한 근거(복구 가능한 한 번의 헛걸음)는 그 시점에 옳았다.
지운 기록은 왜 미뤘는지를 함께 지운다.

그 문단이 적어 둔 처방은 기각됐다는 사실도 함께 남긴다 — 폼의 기준 문서 URL 과
리다이렉트의 기준이 달라 깊이가 어긋난다. 그리고 그 문단이 안전한 쪽으로 분류한
actions.go 의 두 자리가 실제로는 같은 결함이었다는 것도.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
```

---

### Task 7: 랜딩 관문 다섯 줄

**Files:** 없음(검증만)

**Interfaces:**
- Consumes: Task 1~6 의 모든 변경
- Produces: 없음

- [ ] **Step 1: 다섯 줄을 순서대로 돌린다**

★ **`pwd` 를 지우지 마라.** 이 관문은 전부 "무출력이면 통과" 형태라, 검사가 **아무것도 안 봤을 때**와 통과했을 때가 화면에서 같다. `cwd` 가 모듈 밖이면 `gofmt -l .` 이 빈 디렉토리를 검사하고 조용히 통과한 실측이 있다.

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-login-redirect-escapes-proxy-prefix/plugins/flightdeck/server && pwd
gofmt -l .
go vet ./...
GOOS=windows GOARCH=amd64 go vet ./...
GOOS=darwin GOARCH=arm64 go vet ./...
go test ./internal/... ./cmd/fd/ -count=1
```

기대: 앞 넷 무출력, 마지막이 전부 `ok`

★ 교차 `vet` 둘을 잊기 쉽다. `go build` 로 대신하면 안 된다 — 그것은 `_test.go` 를 컴파일 대상에 안 넣어 시험 코드에 대해 열려 있다. 이 태스크가 만든 시험 파일 셋이 정확히 그 사각지대에 있다.

- [ ] **Step 2: `http.Redirect` 가 안 남았는지 마지막으로 확인한다**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-login-redirect-escapes-proxy-prefix/plugins/flightdeck/server && pwd
grep -rn 'http\.Redirect(' --include=*.go . || echo "없다 — 여섯 자리를 다 옮겼다"
```

기대: `없다 — 여섯 자리를 다 옮겼다`

★ 여는 괄호가 필수다. 이유는 Task 4 Step 5 에 있다 — 괄호가 없으면 이 작업이 일부러 남긴 주석 다섯 줄이 잡혀 관문이 영영 빨갛다.

---

### Task 8: 실물 브라우저 검증

**Files:** 없음(관측 + 판단 기록)

**Interfaces:**
- Consumes: Task 5 의 `prefixStripper` 와 같은 구성. 다만 시험이 아니라 **손으로 띄운 서버**에 붙는다.
- Produces: flightdeck 원장의 판단 하나

★ **이 태스크의 산출은 초록 불이 아니다.** playwright 는 시험 스위트에 없어 CI 가 안 돌린다. 회귀를 붙드는 것은 Task 1·3·4·5 의 시험이고, 이 태스크가 남기는 것은 "실물에서 한 번 봤다"는 사실이다. 그것을 판단으로 안 남기면 이 항목은 아무 흔적도 없이 끝난다.

- [ ] **Step 1: 접두 프록시 뒤에 서버를 띄운다**

playwright MCP 가 이 세션에 붙어 있어야 한다. 안 붙어 있으면 사람에게 `/mcp` 재연결을 요청하고 기다린다.

토큰을 켠 서버를 띄우고, 그 앞에 접두를 벗기는 프록시를 세운다. Task 5 의 `prefixStripper` 와 같은 구성이면 되고, 손으로 띄우는 자리라 `go run` 대신 **이미 설치된 `fd` 실행 파일**을 쓴다(임시 `go run` 은 공유 배포 원장을 오염시킬 수 있다).

- [ ] **Step 2: 브라우저로 넷을 잰다**

| 축 | 어떻게 | 통과 기준 |
|---|---|---|
| 접두 뒤 왕복 | `<접두>/` 방문 → 폼에 토큰 → 제출 | 주소창이 `<접두>/` 이고 대시보드가 보인다 |
| 상대 `Location` 해석 | 위 왕복의 최종 URL | 접두가 살아 있다 |
| `HttpOnly` | 콘솔에서 `document.cookie` | `fd_token` 이 **안** 보인다 |
| `Path=/` | 대시보드에서 SSE 가 붙는지 · 쓰기 폼 하나 제출 | 둘 다 401 이 안 난다 |

`SameSite=Strict` 는 두 번째 오리진이 필요하다. **재면 재고, 못 재면 왜 못 쟀는지를 다음 단계의 판단에 적는다** — 무리해서 넣지 않는다.

- [ ] **Step 3: 판단을 남긴다**

`note(kind: "verified", item_id: "fd-screen-login-unverified-in-real-browser", …)` 로 남긴다. 본문에 **반드시** 들어갈 것:

- 무엇을 실제로 봤는가(위 표의 축별 결과)
- 무엇을 **못** 쟀는가와 이유(`SameSite` 를 건너뛰었다면 그 사실)
- ★ 이 검증이 **CI 에 안 남는다**는 사실. 회귀를 붙드는 것은 Task 5 의 왕복 시험이라는 것

- [ ] **Step 4: 스펙 §11 의 열린 위험을 닫는다**

Task 5 에서 출처 대조(`JudgeScreenOrigin`)가 프록시 뒤에서 어떻게 됐는지가 그때 드러났을 것이다. 안 깨졌으면 **그 사실 자체를 판단에 적는다** — 다음 사람이 같은 의심을 다시 하지 않도록. 깨졌으면 이미 사람의 판단을 받았을 것이고, 그 결과를 적는다.

---

### Task 9: 루프백 면제를 끌 스위치

★ **실행 순서: 이 태스크를 Task 6 보다 먼저 돈다.** 코드 변경이라 관문(Task 7)과 브라우저(Task 8) 앞에 와야 한다. 번호가 뒤인 것은 계획이 실행 중에 자란 흔적일 뿐이다.

★ **이것은 리다이렉트 축이 아니다.** Task 5 를 만들다 드러난 인접 결함이고, 사람이 "이 브랜치에서 함께 고친다"고 판단했다. 스펙 §12 를 읽어라.

**Files:**
- Modify: `plugins/flightdeck/server/cmd/fd/serve.go` (플래그 추가 · `serveAPIOptions` 시그니처 · 전달)
- Modify: `plugins/flightdeck/server/internal/api/handlers_meta.go` (`AuthNotice` 의 `Observed` 갈래)
- Modify: `plugins/flightdeck/server/internal/api/pure_test.go` (문구 시험 추가)
- Modify: `plugins/flightdeck/server/cmd/fd/auth_reach_test.go` (배선 시험 추가 + 호출부)
- Modify: 호출부만 고칠 파일 넷 — `cmd/fd/ledgerbackup_test.go:242,246` · `cmd/fd/selfwatch_wiring_test.go:115,132` · `cmd/fd/serve_test.go:359` · `cmd/fd/proxy_prefix_login_test.go:88`

**Interfaces:**
- Consumes: `api.Options.RequireTokenOnLoopback bool` (이미 존재 — `internal/api/api.go:38`)
- Produces: `serveAPIOptions(token string, ratePerMinute int, log *slog.Logger, inContainer bool, watcher *selfWatcher, ledgerJob *ledgerBackupJob, requireTokenOnLoopback bool) api.Options` — **인자가 하나 늘어난다. 끝에 붙인다.**

- [ ] **Step 1: 배선 시험을 쓴다 (실패한다 — 인자가 아직 없다)**

`plugins/flightdeck/server/cmd/fd/auth_reach_test.go` 끝에 더한다:

```go
// TestServeAPIOptionsCarriesLoopbackSwitch 는 스위치가 실제로 옵션에 실리는지 본다.
//
// ★ 이 축이 안 잠기면 스위치가 **조용히 죽는다.** 운영자가 -require-token-on-loopback 을
// 켰는데 아무 일도 안 일어나고, 그 사실이 증상으로 안 드러난다 — 면제는 원래 눈에 안
// 보이고, 안 걸리는 것과 안 열린 것이 화면에서 같기 때문이다.
func TestServeAPIOptionsCarriesLoopbackSwitch(t *testing.T) {
	if serveAPIOptions("tok", 60, quietLogger(), false, nil, nil, false).RequireTokenOnLoopback {
		t.Error("기본값이 참이다 — 로컬 루프백으로 토큰 없이 붙던 세션이 전부 깨진다")
	}
	if !serveAPIOptions("tok", 60, quietLogger(), false, nil, nil, true).RequireTokenOnLoopback {
		t.Error("스위치를 켰는데 옵션에 안 실렸다 — 플래그가 조용히 죽는다")
	}
}
```

- [ ] **Step 2: 시험이 컴파일 실패하는지 확인한다**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-login-redirect-escapes-proxy-prefix/plugins/flightdeck/server && pwd
go vet ./cmd/fd/
```

기대: `too many arguments in call to serveAPIOptions`

- [ ] **Step 3: 시그니처와 플래그를 넣는다**

`plugins/flightdeck/server/cmd/fd/serve.go` 의 `serveAPIOptions` 선언에 인자를 **끝에** 더한다:

```go
func serveAPIOptions(token string, ratePerMinute int, log *slog.Logger, inContainer bool,
	watcher *selfWatcher, ledgerJob *ledgerBackupJob, requireTokenOnLoopback bool) api.Options {
	opt := api.Options{
		Token:         token,
		RatePerMinute: ratePerMinute,
		Log:           log,
		InContainer:   inContainer,
		// ★ 기본값(false)이 설계의 기본 동작이다 — 그것을 여기서 바꾸지 않는다.
		// 이 필드가 배선을 타야 하는 이유는 아래 플래그 주석에 있다.
		RequireTokenOnLoopback: requireTokenOnLoopback,
```

(나머지 필드는 그대로 둔다. `RequireTokenOnLoopback` 을 `InContainer` 다음 줄에 넣는다.)

같은 파일 `runServe` 의 플래그 블록, `rate` 줄 **다음에** 더한다:

```go
	// ★ 루프백 면제를 끄는 스위치다. **기본값은 면제 열림이고 그것을 안 바꾼다** —
	// 로컬 루프백으로 토큰 없이 붙는 세션이 이 제품의 정상 사용이라, 기본값을 뒤집으면
	// 그것들이 한꺼번에 전부 깨진다.
	//
	// ★ 이 플래그가 필요한 자리는 **리버스 프록시가 같은 호스트에 있는 배포**다.
	// 면제 판정은 RemoteAddr 이므로(auth.go 의 IsLoopback) 그 프록시를 거친 요청이
	// 전부 127.0.0.1 로 도착한다 — 토큰을 켜 뒀는데 바깥에서 오는 요청 전부가
	// 무인증으로 통과한다. 컨테이너 배포는 해당 없다: 브리지 게이트웨이가 172.x 라
	// 루프백으로 안 보인다(login.go 가 그 사실을 이미 적어 뒀다).
	//
	// ★ 환경변수를 안 만든다. 이 저장소의 불리언 설정은 전부 플래그이고
	// (migrate.go·project.go), 불리언 환경변수는 선례가 없다. 한 축을 위해 새 관례를
	// 만들면 다음 사람이 어느 쪽이 규칙인지 모른다.
	requireTokenOnLoopback := fs.Bool("require-token-on-loopback", false,
		"루프백 요청에도 토큰을 요구한다(리버스 프록시가 같은 호스트에 있으면 켜라)")
```

그리고 같은 파일의 `handler := buildHandler(...)` 줄에서 새 인자를 넘긴다:

```go
	handler := buildHandler(svc, webH, serveAPIOptions(token, *rate, log, inContainer, watcher, ledgerJob, *requireTokenOnLoopback))
```

- [ ] **Step 4: 호출부 여섯을 고친다**

컴파일러가 전부 잡아 준다. 각 자리에 `, false` 를 **마지막 인자로** 더한다:

- `cmd/fd/ledgerbackup_test.go:242` · `:246`
- `cmd/fd/selfwatch_wiring_test.go:115` · `:132`
- `cmd/fd/auth_reach_test.go:71` · `:75`
- `cmd/fd/serve_test.go:359`
- `cmd/fd/proxy_prefix_login_test.go:88`

★ `proxy_prefix_login_test.go:88` 은 **바로 다음 줄에서 `opt.RequireTokenOnLoopback = true` 로 덮어쓴다.** 그 줄과 위 주석을 지우지 마라 — 인자로 `true` 를 넘기도록 바꾸지도 마라. 그 시험이 면제를 끄는 이유는 배선 축이 아니라 "이 시험은 인증이 켜진 서버를 잰다"이고, 그 근거가 거기 주석에 있다.

- [ ] **Step 5: 시험이 통과하는지 확인한다**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-login-redirect-escapes-proxy-prefix/plugins/flightdeck/server && pwd
gofmt -l .
go vet ./...
go test ./cmd/fd/ -count=1
```

기대: 무출력 둘, 시험 `ok`

- [ ] **Step 6: `/healthz` 문구를 보강한다 (실패하는 시험 먼저)**

`plugins/flightdeck/server/internal/api/pure_test.go` 끝에 더한다:

```go
// TestAuthNoticeWarnsAboutSameHostProxy 는 면제가 **실제로 열린** 갈래가 리버스 프록시를
// 함께 경고하는지 본다.
//
// ★ 관측은 이미 있었다 — loopback_open 이 그 상태를 정확히 낸다. 없던 것은 **연결**이다.
// 같은 호스트 프록시 뒤에서 그 값이 참이 되는데, 운영자가 그 사실을 프록시와 잇지
// 못하면 "내 서버는 루프백으로 아무도 안 치는데 왜 열렸다고 하지"로 읽고 넘긴다.
func TestAuthNoticeWarnsAboutSameHostProxy(t *testing.T) {
	n := AuthNotice(true, LoopbackReach{Configured: true, Observed: true})
	for _, want := range []string{"리버스 프록시", "require-token-on-loopback"} {
		if !strings.Contains(n, want) {
			t.Errorf("문구에 %q 가 없다 — 운영자가 loopback_open 을 프록시와 못 잇는다: %s", want, n)
		}
	}
}
```

돌려서 빨간 것을 본다:

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-login-redirect-escapes-proxy-prefix/plugins/flightdeck/server && pwd
go test ./internal/api/ -run TestAuthNoticeWarnsAboutSameHostProxy -count=1
```

기대: FAIL — 문구에 그 낱말들이 없다

- [ ] **Step 7: 문구를 고친다**

`plugins/flightdeck/server/internal/api/handlers_meta.go` 의 `AuthNotice` 에서 `case reach.Observed:` 갈래를 바꾼다:

```go
	case reach.Observed:
		// ★ **리버스 프록시를 함께 경고한다.** 면제 판정은 RemoteAddr 이라, 프록시가 같은
		// 호스트에 있으면 그것을 거친 요청 전부가 127.0.0.1 로 도착해 이 면제를 받는다 —
		// 토큰을 켜 뒀는데 바깥에서 오는 요청이 전부 무인증으로 통과하는 상태다.
		// 관측은 원래 있었고(loopback_open) 없던 것은 이 연결이다: 그 값을 프록시와 못 이으면
		// "루프백으로 아무도 안 치는데 왜 열렸다지"로 읽고 넘긴다.
		return "토큰이 설정돼 있다. 루프백 요청만 토큰 없이 통과한다 — " +
			"리버스 프록시가 같은 호스트에 있으면 그것을 거친 요청 전부가 여기에 해당한다. " +
			"그 배포라면 -require-token-on-loopback 으로 면제를 꺼라"
```

- [ ] **Step 8: 관문을 돌린다**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-login-redirect-escapes-proxy-prefix/plugins/flightdeck/server && pwd
gofmt -l .
go vet ./...
go test ./internal/... ./cmd/fd/ -count=1
```

기대: 무출력 둘, 시험 전부 `ok`

★ 기존 `AuthNotice` 시험 넷(`pure_test.go:338,377,392,404,419`)이 함께 초록이어야 한다. 그중 하나라도 빨개지면 문구를 고치면서 다른 갈래를 건드린 것이다 — 그 갈래들은 각각 실물 사고에서 나온 문장이라 지우면 안 된다.

- [ ] **Step 9: 커밋한다**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-login-redirect-escapes-proxy-prefix
git add plugins/flightdeck/server/cmd/fd/ plugins/flightdeck/server/internal/api/
git status --short
git commit -F - <<'EOF'
feat(flightdeck): 루프백 면제를 끌 스위치를 단다 — 같은 호스트 프록시가 그것을 받는다

접두 프록시 뒤 왕복 시험을 만들다 드러났다. 면제 판정은 RemoteAddr 이라
리버스 프록시가 같은 호스트에 있으면 그것을 거친 요청이 전부 127.0.0.1 로
도착한다 — 토큰을 켜 뒀는데 바깥에서 오는 요청 전부가 무인증으로 통과한다.

serveAPIOptions 는 RequireTokenOnLoopback 을 한 번도 세팅하지 않았다. 그 필드를
쓰는 자리가 저장소 전체에서 harness_test.go 하나, 즉 시험뿐이었다. 운영자가
끌 길이 없었다는 뜻이다.

**기본값은 안 바꾼다.** 로컬 루프백으로 토큰 없이 붙는 세션이 이 제품의 정상
사용이라, 기본값을 뒤집으면 그것들이 한꺼번에 전부 깨진다. 명시적 스위치만 단다.

관측은 원래 있었다 — /healthz 의 loopback_open 이 그 상태를 정확히 냈다.
없던 것은 연결이라, AuthNotice 의 그 갈래가 이제 프록시를 함께 경고한다.
그 값을 프록시와 못 이으면 "루프백으로 아무도 안 치는데 왜 열렸다지"로 읽는다.

환경변수는 안 만들었다. 이 저장소의 불리언 설정은 전부 플래그이고 불리언
환경변수는 선례가 없다 — 한 축을 위해 새 관례를 만들지 않는다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
```

---

## Self-Review

**스펙 각 절 → 태스크 대응:**

| 스펙 | 태스크 |
|---|---|
| §1 관측(여섯 자리·거짓 주석) | Task 3(넷) · Task 4(둘 + 주석) |
| §2 기계적 원인 | Task 3 Step 3 의 `seeOther` 주석 · Task 4 Step 5 의 `grep` |
| §3 결정 셋 | Task 1(judge 자리) · Task 3·4(절대 목표를 말한다) |
| §4 `RelativeTo` | Task 1 · Task 2(`JudgeLoginAction` 위임) |
| §5 `seeOther` | Task 3 Step 3 · Task 4 Step 3 |
| §6 여섯 자리 | Task 3 Step 3 표 · Task 4 Step 3 |
| §7 1층 | Task 1 Step 1 |
| §7 2층 | Task 3 Step 1 · Task 4 Step 1 |
| §7 3층 | Task 5 |
| §7 기존 시험 넷이 깨진다 | Task 3 Step 4(둘) · Task 4 Step 1(둘) |
| §8 브라우저 | Task 8 |
| §9 문서 | Task 6 |
| §10 안 하는 것 | 어느 태스크도 `DESIGN.md`·`api.go`·`ActionInput` 을 안 만진다 |
| §11 열린 위험 | Task 5 Step 2 의 ★(멈추고 판단을 받는다) · Task 8 Step 4 |

**타입 일관성:** `RelativeTo(from, to string) string` 가 Task 1 에서 정의되고 Task 2·3·4 에서 같은 이름·같은 인자 순서로 쓰인다. `seeOther` 는 두 패키지에서 리시버만 다르다(`s *server` · `h *handler`) — 각 패키지의 기존 리시버 이름을 따랐다.

**계획을 쓰며 실물로 확인한 것 (추측으로 남기지 않았다):**

- `web/actions_test.go` 의 `Location` 자리 둘은 `TestReclaimReleasesClaimAndLeavesJudgment`(71행)와 `TestDropMarksItemDroppedWithReason`(124행) 안이다.
- `api.LoginCookieName` 은 exported 다(`auth.go:189`, 값 `"fd_token"`) — Task 5 가 패키지 밖에서 부를 수 있다.
- `cmd/fd` 에 `prefixStripper`·`formAction`·`clipForTest` 라는 이름이 없다 — Task 5 의 새 헬퍼 셋이 안 부딪힌다.
- `judge` 는 `model` 만 물고 `model` 은 아무것도 안 문다(`go list -deps`) — Task 2 가 여는 `api` → `judge` 방향에 순환이 없다.

**계획이 놓쳤고 실행 중에 드러난 것 하나 (2026-08-12, Task 2 구현자가 발견):**

`api/login_test.go:114` 의 `Action` 단정이 빠져 있었다. 계획을 쓰며 `Location` 을 단정하는 자리는 전수로 찾았으나 **`Action` 을 단정하는 자리는 안 찾았다.** `LoginView.Action` 은 `api` 안에서 두 자리가 채우는데(`withAuth` 와 `loginRefused`) 그 시험이 뒤엣것을 잰다. Task 2 Step 5 에 넣었다.

같은 grep 으로 함께 확인한 결과 `cmd/fd/serve_test.go:392` 의 `action="../login"` 은 **안 깨진다** — 깊이 1 이라 새 규칙에서도 같은 값이다.

**한 가지는 실행 시점에만 알 수 있다:** Task 5 의 ③에서 출처 대조가 프록시 뒤에서 통과하는지. 통과를 가정하고 썼으나(`NewSingleHostReverseProxy` 가 `req.Host` 를 안 바꾸므로 `Origin` 과 `r.Host` 가 둘 다 프록시의 것이 된다), 403 이 나오면 **고치지 말고 멈추라고** 그 태스크에 적어 뒀다. 스펙 §11 이 열어 둔 위험이다.
