# 화면에서 토큰을 넣고 들어간다

- 항목: `fd-screen-unreachable-from-browser`
- 날짜: 2026-08-10
- 경로: `internal/api/auth.go` · `internal/api/middleware.go` · `internal/api/login.go`(새) ·
  `internal/api/api.go` · `internal/web/login.gohtml`(새) · `internal/web/web.go` ·
  `cmd/fd/serve.go` · `DESIGN.md`

## 1. 관측 — 브라우저로는 도달 불가다

정본 서버(컨테이너 `flightdeck`, 29시간 가동, healthy)에서 브라우저로 대시보드를 열면 401 이 난다.
설정 실수가 아니다. 네 사실이 겹쳐 **구조적으로 닫혀 있다.**

| 사실 | 자리 |
|---|---|
| `JudgeAuth` 는 `Authorization` 헤더 하나만 읽는다 | `internal/api/auth.go:38` |
| `web/` 에는 토큰 개념이 아예 없다 — 입력할 자리가 없다 | `internal/web/` 전체에 `token` 문자열 0건 |
| 브라우저는 최초 요청에 임의 헤더를 실을 수 없다 | HTTP 의 성질 |
| 응답이 `WWW-Authenticate: Bearer` 라 로그인 팝업조차 안 뜬다 | `middleware.go:146`. 브라우저는 `Basic` 에만 팝업을 띄운다 |

루프백 면제는 처방이 못 된다. 컨테이너의 브리지 게이트웨이가 `172.20.0.1` 이라 호스트 요청이
루프백으로 안 보인다 — `/healthz` 의 `auth.notice` 가 이미 그 사실을 정확히 말하고 있다.

**화면은 이미 게이트 사슬 안에 있고, 그것은 옳다.** 앞선 판은 바깥 mux 에서 화면을 붙였고
그래서 대시보드의 쓰기 둘이 인증을 우회했다 — 토큰을 켠 배포에서 비루프백 무인증 POST 가
303 으로 통과해 **항목이 실제로 폐기됐다**(`api.go:75-88` 의 실물 재현). 그 사슬을 되돌리는
처방은 전부 기각이다. 열어야 하는 것은 게이트가 아니라 **브라우저가 자격증명을 내놓을 길**이다.

## 2. 결정 넷

| 축 | 결정 | 기각한 것과 이유 |
|---|---|---|
| 쿠키 인정 범위 | **화면 경로만** — `/` · `/actions/*` · `/events` | 전 표면: REST 쓰기는 `screenWrite` 의 출처 대조를 안 타므로(`screenwrite.go:116` 이 화면 경로만 `Screen=true`) 쿠키를 열면 CSRF 방어가 `SameSite` 하나에만 걸린다 |
| 로그인 진입 | **401 을 유지하고 본문만 HTML 폼** | `/login` 303: 인증 실패가 303→200 으로 보여 `/metrics` 의 unauthorized 카운터가 안 오른다. `?next=` 검증(오픈 리다이렉트)도 새로 져야 한다 |
| 쿠키 수명 | **10년 + 로그아웃** | 사용자 1명의 개인 조정 화면이다. 로그아웃은 내 머신이 아닌 곳에서 지우는 유일한 길이라 함께 둔다 |
| 구조 | **`api` 가 판정·쿠키, `web` 이 렌더** (콜백 주입) | `web` 이 전부: 토큰이 두 자리에 산다. 상수시간 비교·대소문자 규칙이 두 벌이 되고 `auth.go` 가 모아둔 판정이 다시 흩어진다. 새 `login/` 패키지: 인증 판정이 두 패키지로 갈리고 얻는 게 없다 |

## 3. 자리 배분 — `Fallback` 이 이미 낸 길

문제의 뼈대는 **토큰을 아는 자리와 HTML 을 아는 자리가 다르다**는 것이다. `api` 는 JSON 만 내고,
`web` 은 템플릿을 `embed` 로 갖고 있지만 토큰을 모른다.

`api.Options` 에 `LoginScreen` 을 하나 더 둔다. `api` 가 판정·쿠키·면제 경로를 쥐고, 폼을
그려야 할 때만 콜백을 부른다. `web` 이 그 콜백을 제공한다.

```go
// api.Options
// LoginScreen 은 401 을 HTML 폼으로 낼 렌더러다. nil 이면 JSON 401 로 접힌다.
//
// ★ Fallback 과 같은 이유의 같은 모양이다 — 화면을 게이트 사슬 안에 두면서도
//   api 가 템플릿을 알지 않게 하는 유일한 이음매다.
LoginScreen func(w http.ResponseWriter, r *http.Request, v LoginView)

// LoginView 는 폼을 그리는 데 필요한 것 전부다.
//
// ★ 토큰 값은 **안 담는다.** 되비추면 그 값이 HTML 에 실려 나가고,
//   web/notFound 가 요청 경로에 대해 세워둔 규율이 여기서 깨진다.
type LoginView struct {
    Error string // 비면 첫 방문이다. 채워지면 직전 시도의 사유
    Next  string // 로그인 후 돌아갈 자리. **호출부가 이미 JudgeNext 를 통과시킨 값이다**
                 // (401 갈래는 r.URL.RequestURI(), 로그인 실패 갈래는 폼의 next).
                 // web 이 그 검증을 다시 하지 않는다 — 두 자리에서 검증하면 한쪽만 고쳐진다
}
```

`web` 은 토큰을 여전히 모른다. 배선은 `serve.go` 의 `serveAPIOptions` 에 한 줄 는다.

## 4. 순수 판정 넷

미들웨어 본문에 조건을 흩지 않는다는 `auth.go` 의 규율을 그대로 따른다 — 흩어 놓으면 시험이
그 조건의 **사본**을 단정하게 되고 변이가 조용히 샌다.

```go
// AuthRequest 는 인증 판정에 필요한 요청 사실 전부다.
//
// ★ 구조체인 이유: 인자로 풀면 문자열 셋과 불리언 둘이 연속해 호출부에서 순서가 뒤집혀도
//   컴파일이 통과한다. ScreenPath 와 requireTokenOnLoopback 이 뒤바뀌면 REST 에 쿠키가
//   열리는데, 그 사고는 시험이 그 조합을 명시로 짚기 전에는 안 보인다.
type AuthRequest struct {
    RemoteAddr  string
    AuthHeader  string
    CookieToken string // 쿠키에서 읽은 값. 없으면 빈 문자열
    ScreenPath  bool   // 쿠키를 인정하는 유일한 조건
}

func JudgeAuth(req AuthRequest, token string, requireTokenOnLoopback bool) AuthDecision
func JudgeScreenPath(path string) bool  // path=="/" || path=="/events" || "/actions/" 접두
func JudgeLoginScreen(accept string) bool // Accept 에 text/html 이 있을 때만 폼
func JudgeNext(next string) string        // 외부로 나가는 값과 빈 값을 "/" 로 접는다
```

`JudgeLoginScreen` 이 **메서드를 안 보는** 이유. 걸러야 할 것은 "쓰기"가 아니라 "HTML 을 못
읽는 소비자"이고 그 축은 `Accept` 하나로 갈린다 — `fd` CLI 는 `application/json`, `EventSource`
는 `text/event-stream` 이다. 메서드를 조건에 넣으면 쿠키가 없는 상태에서 화면 폼을 제출한
사람(`POST /actions/drop`)이 **JSON 401 을 보게 된다.** 그 사람은 브라우저 앞에 있고 폼을
읽을 수 있다. 다시 로그인한 뒤 액션을 한 번 더 눌러야 하는 것은 맞지만, 그것이 JSON 을
들여다보는 것보다 낫다.

`AuthDecision` 은 지금 모양 그대로다(`OK`·`Anonymous`·`Reason`). 자격증명 출처는 `Reason`
문자열이 이미 말한다 — 별도 축을 더하지 않는다.

**`JudgeAuth` 안의 순서에 규율 둘이 있다.**

1. **헤더가 있으면 쿠키는 안 본다.** `screenWrite` 가 `Idempotency-Key` 에 대해 이미 세워둔
   규율과 같은 이유다 — 헤더를 실을 수 있는 클라이언트가 쿠키로 조용히 뒤집히면 무엇이
   인증했는지가 요청마다 달라진다.
2. **틀린 쿠키는 루프백이어도 거절한다.** 기존 `JudgeAuth` 가 틀린 헤더에 대해 이미 그렇게
   하고, 그 근거("클라이언트의 토큰 오설정이 영영 안 보인다")가 쿠키에 그대로 적용된다.
   토큰을 바꾼 뒤 옛 쿠키를 든 브라우저는 폼을 만나고, 새 값을 넣으면 덮어써진다.

`JudgeScreenPath` 가 `/api/v1/*` 에 **거짓**을 내는 것이 이 설계 전체의 안전을 지탱한다.
`/events` 는 api 라우트지만 화면이 무는 짧은 별칭이라 참이고(`api.go:311`), `/api/v1/events`
는 REST 라 거짓이다.

## 5. 흐름

`withAuth` 는 mux 보다 바깥이라 경로만 보고 갈라야 한다 — `/healthz` 가 이미 그렇게 서 있다.
갈래가 셋이 된다.

```
withAuth:
  ① /healthz              → 즉시 통과, loopbackSeen 관측도 안 한다
                             (컨테이너 헬스체크가 30초마다 쳐서 loopback_open 을
                              거짓으로 참으로 만든다 — middleware.go:130 의 그 근거)
  ② loopbackSeen 관측      ← /login 은 이 뒤다. 루프백 도달 관측에서 빠지면 안 된다
  ③ /login · /logout      → 즉시 통과. 핸들러가 스스로 판정한다
                             (경로 단위다 — 메서드를 안 본다. GET /login 도 인증 안 된
                              상태에서 들어오므로 같이 면제여야 한다)
  ④ JudgeAuth 판정
  ⑤ 실패 → JudgeLoginScreen 이 참이면 401 + LoginScreen, 아니면 지금 그대로 401 + JSON
```

경로별로 무슨 일이 나는가.

```
브라우저, 쿠키 없음
  GET /?project=kweiza
  → ④ 실패 · ⑤ 참 → 401 text/html · next = "/?project=kweiza"

  POST /login  token=…&next=/?project=kweiza
  → ③ 통과 → handleLogin
       JudgeScreenOrigin 으로 출처 대조 (screenWrite 가 auth 안쪽이라 직접 부른다)
       subtle.ConstantTimeCompare 로 토큰 대조
       맞음 → Set-Cookie → 303 → JudgeNext(next)
       틀림 → 401 + 폼 + "토큰이 일치하지 않는다"

  GET /?project=kweiza  (쿠키 있음)   → JudgeScreenPath 참 → 통과
  GET /events                         → 참 → 통과. EventSource 는 same-origin 이라
                                        쿠키가 자동으로 실린다 — 자동 갱신이 산다
  POST /actions/drop                  → 쿠키 + Origin 대조 + 멱등. 지금과 같다
  POST /logout                        → Max-Age=0 → 303 → "/" → 401 폼

fd CLI · curl · MCP
  Authorization: Bearer …             → 헤더가 있으면 쿠키를 안 본다 → 불변
  헤더 없음 + Accept: application/json → JSON 401 → 불변
```

**`GET /login` 도 둔다** — 사용자가 직접 칠 수 있는 URL 이다. `/` 로 303 하면 인증이 안 됐으면
거기서 폼을 만나고 됐으면 대시보드가 뜬다. 안 두면 `web` 의 404("대시보드는 / 한 장이다")가
나와 혼란스럽다.

**무차별 대입 방어는 새로 만들 게 없다.** `rateLimit` 이 `auth` 바깥이라 `/login` 도 그 한도를 탄다.

## 6. 쿠키

```
Set-Cookie: fd_token=<token>; Path=/; HttpOnly; SameSite=Strict; Max-Age=315360000
```

- `Path=/` — `/events` 와 `/actions/*` 둘 다 닿아야 한다.
- `HttpOnly` — JS 가 못 읽는다. 화면에 JS 로 토큰을 다루는 자리가 하나도 안 생긴다.
- `SameSite=Strict` — 크로스사이트 요청에 안 실린다. `screenWrite` 의 Origin 대조와 이중이다.
- `Max-Age=315360000`(10년) — 결정 §2.
- `Secure` — **TLS 뒤일 때만 켠다**(`r.TLS != nil` 또는 `X-Forwarded-Proto: https`).
  무조건 켜면 `http://` 에서 쿠키가 저장조차 안 돼 로그인이 원리적으로 불가능해진다.

쿠키에 토큰 원문을 담는다. 별도 세션 ID 를 발급하면 서버가 세션 표를 져야 하는데, 사용자
1명·토큰 1개·역할 없음(설계 §6)인 제품에서 그 표가 답하는 질문이 없다.

## 7. 오류

| 상황 | 응답 | 근거 |
|---|---|---|
| 토큰 불일치 | 401 + 폼 + "토큰이 일치하지 않는다" | 상태코드가 사실을 유지한다 |
| 출처 대조 실패 | 403 `cross_site_write_refused` | 기존 `screenWrite` 문구를 그대로 쓴다 |
| 토큰 빈 값 | 401 + 폼 + "토큰을 적어라" | |
| 서버에 토큰 미설정 | 쿠키를 **안 굽고** 303 → `/` | 인증이 꺼진 서버에 쿠키를 구우면, 나중에 토큰을 켰을 때 그 쿠키가 *틀린 자격증명*이 되어 폼이 아니라 거절을 만난다 |
| 템플릿 렌더 실패 | 500 | `web.render` 의 버퍼 규율 재사용 — 반쪽 HTML 이 200 으로 나가지 않는다 |
| `LoginScreen` 이 nil | JSON 401 로 접힌다 | `Fallback` nil 과 같은 모양 |

**로그는 안 남긴다.** `withAuth` 의 기존 규율("401 로그 줄 없음 — 초과 트래픽이 그대로 로그
증폭이 된다")을 로그인 실패에도 적용한다. 관측은 이미 있는 `/metrics` 의 unauthorized 카운터가 낸다.

**토큰 값을 응답에 되비추지 않는다.** 폼은 `type="password"` 로 비우고 다시 그린다.

## 8. 시험

순수 판정은 테이블로, 왕복은 `http_test.go` 로. ★ 표시가 이 설계를 지탱하는 축이다.

```
JudgeAuth (기존 15케이스에 축 추가 — 호출부가 한 자리라 시그니처 변경 부담이 작다)
  쿠키 일치 + 화면 경로            → 통과
  쿠키 일치 + REST 경로            → 거절        ★
  쿠키 틀림 (루프백이어도)          → 거절
  헤더 틀림 + 쿠키 맞음            → 거절        ★ 헤더가 이긴다
  헤더 없음 + 쿠키 없음 + 루프백    → 기존 면제 흐름 그대로

JudgeScreenPath   / · /actions/drop · /events → 참
                  /api/v1/items/next · /api/v1/events · /healthz · /metrics → 거짓  ★
JudgeLoginScreen  text/html → 참 │ application/json · text/event-stream · 빈 값 → 거짓
                  POST 여도 Accept 가 text/html 이면 참                      ★
JudgeNext         //evil.com · http://evil.com · 빈 값 → "/" │ /?project=x → 그대로

왕복
  쿠키 없이 GET /                      → 401 + text/html
  쿠키 없이 GET /api/v1/items/next     → 401 + JSON              (CLI 불변)
  Accept: application/json 로 GET /    → 401 + JSON              (CLI 불변)
  POST /login 맞는 토큰                 → 303 + Set-Cookie 속성 넷 단정
  POST /login 틀린 토큰                 → 401 + 폼, 본문에 토큰 값 없음
  그 쿠키로 GET /                       → 200
  그 쿠키로 GET /api/v1/items/next      → 401                     ★ 범위가 실제로 닫혔나
  POST /logout                          → Max-Age=0

배선
  serveAPIOptions 가 LoginScreen 을 채우는가                      ★
```

마지막 줄이 중요하다. `serve.go:85` 가 조립을 순수 함수로 뽑아둔 근거가 정확히 이것이다 —
축 하나가 빠져도 전 스위트가 초록이고 어긋남은 운영에서만 보이는 사고가 이미 한 번 났다
(`/healthz` 가 "루프백은 통과한다"고 광고하는데 배선상 아무도 통과 못 했던 건).

## 9. 안 하는 것

- **세션 표·만료·회전** — 사용자 1명, 토큰 1개, 역할 없음. 답할 질문이 없다.
- **화면에서 토큰을 바꾸는 자리** — 토큰은 `FD_TOKEN` 이 정한다. 화면에 두면 그 값이 두 자리에 산다.
- **`Basic` 인증으로 브라우저 팝업 띄우기** — 팝업은 로그아웃이 불가능하고 오류 문구를 못 낸다.
- **REST 에 쿠키 열기** — 결정 §2.
- **로컬 프록시·`network_mode: host`** — 우회이지 처방이 아니다. 이 항목이 그것을 없애려는 것이다.

## 10. `DESIGN.md`

§6 의 "인증은 `Authorization: Bearer <token>`" 이 쿠키 축이 들어가면 거짓이 된다. 그 문단에
화면 갈래를 적는다 — **쿠키는 화면 경로에서만 인정하고 `/api/v1/*` 는 헤더 전용이라는 사실**이
핵심이고, 그것이 안 적히면 다음 사람이 "인증은 하나"라고 읽고 REST 에 쿠키를 연다.

## 11. 알려진 한계

- **평문 HTTP 에서 쿠키가 오간다.** 토큰을 이미 헤더로 평문 전송 중이라 새 노출은 아니지만,
  쿠키는 이후 모든 요청에 자동으로 실려 노출 **빈도**가 는다. TLS 뒤에 놓는 것이 처방이고
  그때 `Secure` 가 자동으로 켜진다.
- **`SameSite=Strict` 라 외부 링크로 처음 들어오면 폼을 한 번 만난다.** 새로고침하면 들어가진다.
  이 화면에 외부 링크로 진입하는 경로가 실질적으로 없어 `Lax` 로 낮추지 않았다.
- **로그아웃은 그 브라우저의 쿠키만 지운다.** 토큰 자체는 유효하다. 토큰을 무효화하는 길은
  `FD_TOKEN` 을 바꾸고 서버를 다시 띄우는 것뿐이고, 이 제품에 그것 말고 다른 길을 만들지 않는다.
- **경로 접두 뒤에서 로그인 성공 후 리다이렉트가 접두 밖으로 떨어진다.** `login.go` 의
  `Location` 이 `/` 또는 `/?project=x` 라 **경로만 있는 값**이고, 접두를 벗겨 넘기는 프록시는
  그 응답 헤더를 고쳐 쓰지 않는다. 폼 `action` 과 SSE 는 상대경로라 접두 뒤에서도 맞는 자리를
  가리키는데(`JudgeLoginAction`), 리다이렉트만 그 규율 밖이다. **지금은 안 고친다** — 쿠키가
  `Path=/` 로 이미 구워져 남으므로 사람이 접두 주소를 다시 치면 그대로 들어가고, 즉 복구
  가능한 한 번의 헛걸음이다. 고친다면 `Next` 를 절대경로로 두는 대신 **폼이 자기 문서 URL 로
  상대화한 값을 싣게** 하는 쪽이다 — `Location` 에 `X-Forwarded-Prefix` 를 신뢰해 붙이는
  처방은 이 레포에 없는 신뢰를 새로 만드는 것이라 기각이다(`JudgeScreenOrigin` 의 그 근거).
  **2026-08-12 에 닫혔다** — `docs/superpowers/specs/2026-08-12-proxy-prefix-redirect-design.md`.
  이 문단의 처방("폼이 자기 문서 URL 로 상대화한 값을 싣게")은 **기각됐다**: 폼의 기준 문서
  URL(`/actions/reclaim`)과 리다이렉트의 기준(`/login`)이 달라서 깊이가 어긋난다. 대신 서버가
  응답하는 요청 경로를 기준으로 상대화한다(`judge.RelativeTo`). `X-Forwarded-Prefix` 기각은
  유효하고 근거가 둘 늘었다 — nginx 가 그 헤더를 기본으로 안 보내고, 안 고쳐졌다는 사실이
  증상으로도 안 드러난다.

  **결함은 넷이 아니라 여섯이었다.** 이 문단은 `actions.go` 의 `../?` 를 안전한 쪽으로
  분류했는데, `http.Redirect` 가 상대 URL 을 절대화해서 내보내므로 그 자리도 같은 결함이었다.
  그렇게 분류하게 만든 것은 `back()` 의 주석이다("하위 경로에 마운트돼도 그대로 성립한다").
- **무차별 대입 방어는 조건부다.** §5 는 "`rateLimit` 이 auth 바깥이라 새로 만들 게 없다"고
  적었으나, `cmd/fd/serve.go` 의 `-rate-per-minute` **기본값이 0(무제한)** 이고 컨테이너
  배포도 그 값을 안 켠다. 즉 그 문장은 **운영자가 값을 켰을 때만 참**이다. 회귀는 아니다 —
  헤더 추측도 똑같이 무제한이었고 이 변경이 낮춘 것은 없다. 다만 폼은 사람이 브라우저로
  칠 수 있는 표면이라 **시도 비용이 낮아진** 것은 사실이므로, 이 서버를 루프백 밖에 내놓는
  배포는 `-rate-per-minute` 를 켜야 한다. 본문 크기는 별개로 막았다(`handleLogin` 의
  `MaxBytesReader` — 그 경로가 `withIdempotency` 를 건너뛰어 상한이 안 걸려 있었다).
