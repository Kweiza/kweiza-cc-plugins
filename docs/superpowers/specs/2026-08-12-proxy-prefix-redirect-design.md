# 리다이렉트가 접두 안에 착지한다

- 항목: `fd-login-redirect-escapes-proxy-prefix` · `fd-screen-login-unverified-in-real-browser`
- 날짜: 2026-08-12
- 선행: `sha:1ba049b` (화면 토큰 로그인)
- 경로: `internal/judge/relative.go`(새) · `internal/api/login.go` · `internal/api/auth.go` ·
  `internal/web/actions.go` · `cmd/fd/`(새 왕복 시험) ·
  `docs/superpowers/specs/2026-08-10-web-token-login-design.md`

## 1. 관측 — 여섯 자리이고, 소스는 거짓말을 한다

경로 접두를 벗겨 넘기는 리버스 프록시(`/dcp-dev-board/` 같은 것) 뒤에서, 이 서버가 내는
`Location` 이 전부 **경로만 있는 절대경로**다. 프록시는 그 헤더를 고쳐 쓰지 않으므로 브라우저는
접두 **밖**으로 나간다.

`net/http` 의 실측이다(`httptest` 로 여섯 자리를 그대로 재현):

| 자리 | 소스에 쓰인 값 | 실제 `Location` |
|---|---|---|
| `login.go:70` GET `/login` | `"/"` | `/` |
| `login.go:92` 토큰 꺼진 서버 | `next` | `/?project=x` |
| `login.go:105` 로그인 성공 | `next` | `/?project=x` |
| `login.go:148` 로그아웃 | `"/"` | `/` |
| `actions.go:364` `back()` | `"../?"+q` | **`/?project=x`** |
| `actions.go:527` project-view | `"../?"+q` | **`/?project=x`** |

★ **아래 둘이 이 항목의 값이다.** 큐 항목 본문은 `actions.go` 의 `"../?"` 를 "상대경로 규율을
따르는 자리"로 분류했고, `back()` 의 주석은 이렇게 적혀 있다:

```go
// 상대 경로로 보낸다 — 이 핸들러가 하위 경로에 마운트돼도 그대로 성립한다.
http.Redirect(w, r, "../?"+q.Encode(), http.StatusSeeOther)
```

**이 문장은 참이 아니다.** 소스는 상대경로인데 나가는 값은 절대경로다. 결함이 넷이 아니라
여섯인 것을 가린 것이 이 주석이고, 그래서 이 스펙은 코드보다 **주석을 먼저** 고친다.

## 2. 기계적 원인 — `http.Redirect` 가 절대화한다

```go
// net/http/server.go — Redirect()
if url == "" || url[0] != '/' {
    olddir, _ := path.Split(oldpath)   // 요청 경로의 디렉토리를 앞에 붙인다
    url = olddir + url
}
… url = path.Clean(url)
```

상대 URL 을 받아도 요청 경로 기준으로 **절대화한 뒤** `Location` 에 싣는다. RFC 7231 §7.1.2 가
상대 참조를 허용하는데도 그렇게 한다("The client would probably do this for us, but doing it
ourselves is more reliable").

즉 **`http.Redirect` 를 쓰는 한 상대 `Location` 은 원리적으로 불가능하다.** 폼 `action` 과 SSE
경로는 템플릿이 직접 찍어서 상대경로가 살아남았고, 리다이렉트만 이 함수를 지나며 규율을 잃었다.
`./` 를 줘도 `/` 가 나간다(실측).

## 3. 결정 셋

| 축 | 결정 | 기각한 것과 이유 |
|---|---|---|
| `Location` 형태 | **상대 참조를 직접 쓴다** — `http.Redirect` 를 안 쓴다 | `X-Forwarded-Prefix` 를 신뢰해 접두를 붙이는 처방: 이 레포에 없는 신뢰를 새로 만든다(`JudgeScreenOrigin` 이 스킴을 일부러 안 보는 그 근거). 게다가 nginx 는 이 헤더를 **기본으로 안 보내므로** 기본 배포에서 안 고쳐지고, 안 고쳐졌다는 사실이 증상으로도 안 드러난다. `Referer` 로 접두를 역산: 같은 신뢰 문제 + 정책에 따라 헤더가 아예 안 온다 |
| 깊이 셈의 자리 | **`internal/judge` 에 한 벌** | 각 패키지에 사본: `web/login.go` 가 이미 명시로 경계한 그 두 벌이 된다("세면 그 셈이 api 와 두 벌이 되고, 이 축의 표류는 원인이 안 보인다"). 그 표류의 증상이 바로 지금 고치는 결함이라, 사본은 재발을 구조에 심는 것이다 |

★ **`api` → `judge` 는 새 의존 방향이다.** `web` 은 이미 `judge` 를 import 하지만(`format.go`)
`api` 는 한 번도 안 했다. `judge` 는 표준 라이브러리와 `model` 만 쓰므로 순환은 없다. 이 방향을
새로 여는 근거는 위 표의 그것 하나다 — 대안이 전부 셈을 두 벌로 만든다. `api/auth.go` 의 다른
`Judge*` 판정들은 그대로 둔다. 옮기는 것은 **깊이 셈 하나**이고, 그것이 두 패키지에서 필요한
유일한 판정이기 때문이다.
| 상대화의 주체 | **호출자는 절대 목표를 말하고 헬퍼가 상대화한다** | 호출자가 `../` 를 손으로 박는 지금 방식: 새 라우트가 깊이를 바꾸면 조용히 틀리고, `actions.go` 가 정확히 그 모양이었다 |

## 4. `RelativeTo` — 깊이 셈 하나

```go
// RelativeTo 는 from 에서 응답할 때 to 로 가는 상대 참조다. 순수 함수다.
func RelativeTo(from, to string) string
```

`from` 은 이 응답을 내는 요청의 경로, `to` 는 목표 절대경로다.

| `from` | `to` | 반환 | 접두 `/dcp-dev-board` 뒤 착지 |
|---|---|---|---|
| `/login` | `/` | `./` | `/dcp-dev-board/` |
| `/login` | `/?project=x` | `./?project=x` | `/dcp-dev-board/?project=x` |
| `/logout` | `/` | `./` | `/dcp-dev-board/` |
| `/actions/reclaim` | `/?notice=…` | `../?notice=…` | `/dcp-dev-board/?notice=…` |
| `/actions/reclaim` | `/login` | `../login` | `/dcp-dev-board/login` |
| `/actions/reclaim/` | `/` | `../../` | `/dcp-dev-board/` |

셈은 `JudgeLoginAction` 이 폼 `action` 에 하던 것과 같다 — **마지막 슬래시까지의 슬래시 수**로
깊이를 센다. RFC 3986 의 상대 해석이 문서 URL 의 마지막 마디를 버리고 남은 자리에 붙이기
때문이다(`/a/b` 의 기준은 `/a/`). 빈 마디(`//`)도 한 마디로 센다.

★ **`./` 를 언제나 붙인다.** 생략하면 쿼리만 있는 목표에서 조용히 틀린다 — `?project=x` 는
상대 참조로서 base 의 **경로를 유지**하므로 `/dcp-dev-board/login?project=x` 에 착지한다.
로그인 화면으로 되돌아가는 것이고, 증상은 "토큰이 맞는데 로그인이 안 된다"로 보인다. 규칙을
하나로 두어 그 함정을 구조에서 없앤다.

★ **못 읽은 것은 깊이 0 으로 접는다.** `from` 에 슬래시가 없으면(빈 문자열 · OPTIONS 의 `*`)
뿌리로 본다. 그 경우의 최악은 "이 이상한 자리에서만 안 통한다"이고, 반대로 과하게 올라가면
접두 **밖**으로 나가 배포 전체가 깨진다. `to` 가 `/` 로 시작하지 않거나 `//` 로 시작하면 `./`
로 접는다 — `JudgeNext` 가 이미 막지만 순수 함수는 자기 방어를 진다.

**`JudgeLoginAction` 은 이름만 남고 몸통이 위임된다** — `return RelativeTo(path, "/login")`.
셈을 두 벌로 두는 것은 §3 이 기각했고, 반대로 함수를 통째로 없애면 `"/login"` 리터럴이 호출자
두 자리(`middleware.go:183` · `login.go:166`)로 흩어진다. 이름이 의도를 말하고 경로가 한 자리에
남는 쪽을 고른다.

대가는 폼 `action` 이 `login` → `./login` 으로 바뀌는 것이다. 브라우저 해석은 동일하다.
`api/pure_test.go` 의 `TestJudgeLoginAction` 표를 갱신한다. `web/login_test.go` 의
`action="login"` 은 **안 깨진다** — 그 시험은 `Action` 값을 직접 주입해서 `api` 의 셈을 안 탄다.
그래도 시험 데이터를 현실과 맞춘다. 그 문자열은 다음 사람에게 실물의 예시로도 읽히기 때문이다.

## 5. `seeOther` — 쓰기는 얇게

```go
// ★ http.Redirect 를 안 쓴다 — 그것은 상대 URL 을 요청 경로 기준으로 절대화해서
//   접두 뒤 배포에서 접두 밖으로 떨어뜨린다. 이 파일이 고친 결함이 그것이다.
func seeOther(w http.ResponseWriter, r *http.Request, to string) {
	w.Header().Set("Location", judge.RelativeTo(r.URL.Path, to))
	w.WriteHeader(http.StatusSeeOther)
}
```

**계산은 한 벌, 쓰기는 `api` 와 `web` 에 각각 얇게 둔다.** 두 패키지는 서로 import 하지 않고
(그 방향은 `web/login.go` 가 명시로 막았다), 이 헬퍼는 `http.ResponseWriter` 를 만져서 순수
패키지인 `judge` 에 못 들어간다. 사본이 둘 생기지만 각각 두 줄이고 **판단이 없다** — 값은 이미
정해져서 온다. 표류할 수 있는 것(깊이 셈)은 한 자리에 있다.

본문은 안 쓴다. `http.Redirect` 는 GET 에 `<a href>` 한 줄을 붙이는데, 303 을 못 따르는 옛
클라이언트용이고 이 화면의 대상이 아니다.

## 6. 여섯 자리

| 자리 | 바뀔 것 |
|---|---|
| `login.go:70` | `s.seeOther(w, r, "/")` |
| `login.go:92` | `s.seeOther(w, r, next)` |
| `login.go:105` | `s.seeOther(w, r, next)` |
| `login.go:148` | `s.seeOther(w, r, "/")` |
| `actions.go:364` | `h.seeOther(w, r, "/?"+q.Encode())` |
| `actions.go:527` | `h.seeOther(w, r, "/?"+q.Encode())` |

`actions.go` 의 `"../"` 가 `"/"` 로 **바뀌는 것**이 §3 의 셋째 결정이다. 깊이 셈이 호출자에서
사라지므로 `/actions/x/y` 같은 새 라우트가 생겨도 자동으로 맞는다.

`back()` 의 거짓 주석(§1)은 교체한다 — 무엇이 상대성을 **보장**하는지(`seeOther` 가
`RelativeTo` 를 거친다)를 말하게 한다.

## 7. 시험 세 층

**1층 · 셈 (`judge`, 순수).** §4 의 표를 전수로 단정한다 + 방어 경계(슬래시 없는 `from`,
뒤 슬래시 `from`, `//` 로 시작하는 `to`).

**2층 · 해석 (`api`).** 기존 `TestLoginFormActionReachesLoginRoute` 의 패턴을 리다이렉트에
적용한다. 접두를 base 에 넣고 브라우저 규칙대로 푼다:

```go
base, _ := url.Parse("http://fd.example/dcp-dev-board/login")
target := base.ResolveReference(mustParse(rec.Header().Get("Location")))
// target.Path 가 "/dcp-dev-board/" 여야 한다 — "/" 면 접두 밖이다
```

`url.ResolveReference` 는 RFC 3986 구현이라 브라우저와 같은 규칙이다. **회귀를 붙드는 것은 이
층이다** — 누가 `http.Redirect` 로 되돌리면 여기서 `/` 가 나와 깨진다.

★ **기존 시험 넷이 이 변경으로 빨개진다.** `api/login_test.go:77,179` 의 `Location` 값 단정 둘,
`web/actions_test.go:71` 의 `HasPrefix(loc, "/?")`, 그리고 `web/actions_test.go:124` 가
`Location` 을 **요청 URL 로 그대로** 쓰는 자리다. 마지막 것은 `../?…` 가 요청 경로로 성립하지
않아서 깨지는데, 처방은 값을 바꾸는 것이 아니라 `ResolveReference` 로 푸는 것이다 — 고치고 나면
그 시험도 2층이 된다. **깨지는 것이 옳다**: 상대 `Location` 을 절대경로처럼 다루던 가정이
시험에도 박혀 있었다는 뜻이다.

**3층 · 왕복 (`cmd/fd`).** 접두를 벗기는 프록시를 세우고 실제 배선으로 로그인 전 과정을 돈다.

```
http.Client{Jar: cookiejar}
   → httptest(프록시, /dcp-dev-board 를 벗긴다)
       → httptest(buildHandler 로 세운 실제 서버)
```

`serveAPIOptions` + `buildHandler` 를 그대로 부르므로 **배선 사본이 없다.** 프록시는 `Location`
을 고쳐 쓰지 않는다 — nginx 가 안 하는 그 일을 시험도 안 해야 재는 것이 성립한다.

단정: 접두 뒤 첫 방문에 폼이 뜨고 → 폼 `action` 이 가리킨 자리에 토큰을 제출하면 303 이고 →
그 `Location` 을 따라간 최종 URL 이 **접두 안**이고 → 거기에 대시보드가 있다.

## 8. 실물 브라우저 검증 — 회귀를 못 붙든다

`fd-screen-login-unverified-in-real-browser` 를 여기서 함께 닫는다. playwright 로 접두 프록시
뒤에서 실제 로그인을 왕복한다.

★ **이 층의 산출은 초록 불이 아니라 원장에 남는 판단 하나다.** playwright 는 시험 스위트에 없어
CI 가 안 돌린다. 이것을 명시하지 않으면 다음 사람이 브라우저 검증을 회귀 방어로 착각하고,
7절의 시험을 지워도 된다고 읽는다.

| 축 | 어떻게 | 왜 브라우저여야 하나 |
|---|---|---|
| 접두 뒤 왕복 | 폼에 토큰 → 착지 URL | Go 클라이언트도 재지만, 실물이 같은 답을 내는지가 이 항목의 질문이다 |
| 상대 `Location` 해석 | 착지 URL | `ResolveReference` 는 표준 구현이지 브라우저 자체가 아니다 |
| `HttpOnly` | `document.cookie` 에 `fd_token` 이 없다 | JS 실행이 필요하다 — Go 시험이 원리적으로 못 잰다 |
| `Path=/` | `/` 와 `/events` 양쪽에 쿠키가 실린다 | 브라우저의 쿠키 저장소 동작 |

`SameSite=Strict` 는 두 번째 오리진이 필요하다. **재면 재고, 못 재면 왜 못 쟀는지를 판단에
남긴다.** `Secure` 는 순수 함수 `JudgeCookieSecure` 가 단위 시험으로 이미 닫았고, 브라우저가
더할 것은 "Secure 쿠키는 http 에 저장 안 된다"는 **브라우저 명세**지 이 코드가 아니다.

## 9. 문서

`2026-08-10-web-token-login-design.md:255-262` 의 알려진 한계 항목을 **지우지 않고 닫는다.**
그때 "안 고친다"고 판단한 근거(복구 가능한 한 번의 헛걸음)는 그 시점에 옳았고, 지운 기록은 왜
미뤘는지를 함께 지운다. 항목 끝에 닫힌 날짜와 이 스펙을 가리키는 줄을 덧붙인다.

그 문단이 기각한 `X-Forwarded-Prefix` 처방은 **여전히 기각**이고, §3 이 그 근거를 이어받아 둘을
더 붙였다(nginx 기본값 · 침묵하는 실패).

## 10. 안 하는 것

- **`DESIGN.md`** — 이 규율은 코드 주석 셋(`web.go` 의 SSE · `auth.go` 의 폼 action · 새
  `RelativeTo`)에 이미 살아 있어 DESIGN 급이 아니다. 다른 세션이 그 파일을 잡고 있어 겹침을
  새로 만들 이유도 없다.
- **`actions.go:527` 을 `back()` 으로 합치기** — 그 둘은 필드 이름만 다른 같은 블록이지만,
  합치려면 `ActionInput` 의 필드 대응을 정리해야 하고 그건 이 축과 무관한 리팩터다. 후속으로
  남긴다.
- **접두를 서버가 아는 것** — `FD_BASE_PATH` 같은 설정을 새로 두지 않는다. 상대 참조는 접두를
  **몰라도** 맞고, 아는 순간 그 값이 배포와 어긋날 자리가 생긴다.
- **`JudgeNext` 완화** — 상대 참조가 원점을 못 벗어나므로 오픈 리다이렉트 방어가 한 겹 늘었지만,
  기존 문자열 검사를 걷어내지 않는다. 방어가 겹치는 것은 이 레포의 규율이다.

## 11. 열어 두는 위험

**프록시 뒤에서 출처 대조가 성립하는지 아직 모른다.** `JudgeScreenOrigin` 은 `Origin` 헤더와
`r.Host` 를 대조하는데, 접두 프록시 뒤에서 `Origin` 은 프록시의 호스트이고 `r.Host` 는 프록시가
`Host` 를 넘겨주느냐에 달렸다. 7절 3층이 이 축을 처음으로 재게 된다.

깨지면 그것은 **접두 배포의 두 번째 결함**이고 이 스펙이 발견한 것이다. 여기서 고칠지 별개
항목으로 낼지는 드러난 뒤에 판단한다 — 지금 범위에 미리 넣지 않는다. 안 깨지면 그 사실 자체를
판단에 적는다(다음 사람이 같은 의심을 다시 하지 않도록).

**2026-08-12 실측 — 통과했다.** 7절 3층이 실제로 왕복하면서 403 이 한 번도 안 났다.
`httputil.ReverseProxy` 가 `req.Host` 를 안 바꾸므로 `Origin` 과 `r.Host` 가 둘 다 프록시의
것이 되고, `JudgeScreenOrigin` 이 그 둘을 맞춰 통과시킨다. 이 축은 이제 추측이 아니다.

★ **다만 남은 것이 하나 있다.** 실제 nginx 가 `Host` 를 그대로 넘기는지는 설정에 달렸고
(`proxy_set_header Host $host` 를 안 쓰면 업스트림 주소가 간다), 이 시험은 Go 의 프록시
구현을 잴 뿐이다. 8절의 실물 브라우저 검증도 같은 Go 프록시를 쓰므로 그 축을 못 메운다.
접두 뒤 배포를 실제로 쓰기 시작하면 그 설정 한 줄이 조건이라는 것을 알고 있어야 한다.

★ **또 하나 — `net/http.ServeMux` 자체가 절대경로 `Location` 을 낸다.** 이 스펙 1절의
"여섯 자리"는 저장소 코드가 낸 자리의 전수였지, `net/http` 표준 라이브러리 안의 자리까지
포함한 전수는 아니었다. `ServeMux.findHandler` 가 요청 경로를 `cleanPath` 로 정규화한 뒤
원래 경로와 다르면 `RedirectHandler` 로 307 을 내는 갈래가 있고, 그 `Location` 은 접두를
모르는 절대경로다. 이 저장소 코드가 아니라 표준 라이브러리 안이라 `grep 'http\.Redirect('`
가 원리적으로 못 본다. 실측:

| 요청 | 응답 | `Location` |
|---|---|---|
| `GET //` | 307 | `/` |
| `POST /actions//reclaim` | 307 | `/actions/reclaim` |
| `GET /actions/../` | 307 | `/` |
| `GET //?project=kweiza` | 307 | `/?project=kweiza` |

**회귀가 아니다** — 이 브랜치 이전에도 같았다. `RelativeTo`·`seeOther` 는 이 저장소가 만드는
`Location` 만 다루므로 이 갈래는 손이 안 닿는다.

도달 경로는 nginx 의 흔한 설정 하나로 짐작한다 — `location /dcp-dev-board { proxy_pass
http://…/; }` 처럼 `location` 에 뒤 슬래시가 없으면 `/dcp-dev-board/` 요청이 업스트림에 `//`
로 도착한다. **이것은 추측이다 — 실물 nginx 로 확인한 것이 아니다**(위 문단의 `Host` 조건과
달리 이 축은 아직 아무 검증도 없다). 확인되면 접두 뒤 배포가 성립하는 조건이 이제 둘이다 —
`Host` 를 그대로 넘기는 nginx 설정 한 줄, 그리고 `location` 에 뒤 슬래시를 붙여 `//` 가
업스트림에 안 닿게 하는 설정 한 줄.

## 12. 실행 중 드러난 인접 결함 — 루프백 면제가 프록시를 받는다

7절 3층을 만들다 나왔다. **이 스펙의 축(리다이렉트가 어디에 착지하는가)이 아니라 인접 축
(인증이 언제 면제되는가)이지만, 같은 배포 형태에서 만난다.** 사람이 이 브랜치에서 함께
고치기로 판단했다.

**관측.** 시험이 세운 `httptest` 서버는 언제나 `127.0.0.1` 이라 루프백 면제가 발동해 인증
게이트가 통째로 건너뛰어졌다 — 첫 방문이 401 이 아니라 **200** 이었다. 그 시험은 로그인을
한 번도 안 거치고 초록이 될 뻔했다(리뷰가 반증 실험으로 재현했다).

시험 쪽은 `opt.RequireTokenOnLoopback = true` 로 닫힌다. 그런데 그 필드를 확인하다 더 큰
것이 나왔다:

| 사실 | 자리 |
|---|---|
| 면제 판정은 **TCP 피어 주소**다 | `auth.go:64` — `IsLoopback(req.RemoteAddr)` |
| `serveAPIOptions` 는 `RequireTokenOnLoopback` 을 **한 번도 세팅하지 않는다** | `cmd/fd/serve.go` |
| 그 필드를 쓰는 자리는 저장소 전체에서 **시험 하나뿐**이다 | `harness_test.go:279` |

즉 **운영 배포의 면제는 항상 켜져 있고 끌 길이 없다.** 리버스 프록시가 flightdeck 과 같은
호스트에 있으면 그것을 거친 요청이 전부 `127.0.0.1` 로 도착하므로, 토큰을 켜 뒀는데 바깥에서
오는 요청 전부가 무인증으로 통과한다. **접두 배포의 가장 흔한 형태가 그것이다.**

컨테이너 배포는 해당 없다 — 브리지 게이트웨이가 `172.x` 라 루프백으로 안 보이고, `login.go`
가 그 사실을 이미 적어 뒀다.

**처방: 명시적 스위치 하나. 기본값은 안 바꾼다.**

`-require-token-on-loopback` 플래그를 달고 `serveAPIOptions` 가 그것을 옮긴다. 기본값을
뒤집는 처방은 기각이다 — 로컬 루프백으로 토큰 없이 붙는 세션이 이 제품의 정상 사용이라
(`compose.yaml` 이 "다른 머신의 세션이 붙는 것이 이 제품의 전부"라고 적었고 로컬도 그중
하나다), 뒤집으면 그것들이 한꺼번에 전부 깨진다.

환경변수는 안 만든다. 이 저장소의 불리언 설정은 전부 플래그이고(`migrate.go`·`project.go`)
불리언 환경변수는 선례가 없다. 한 축을 위해 새 관례를 만들면 다음 사람이 어느 쪽이 규칙인지
모른다.

★ **관측은 이미 있었다.** `/healthz` 의 `loopback_open`(`Configured && Observed`)이 이 상태를
정확히 낸다. 없던 것은 **연결**이다 — 같은 호스트 프록시 뒤에서 그 값이 참이 되는데, 운영자가
그것을 프록시와 못 이으면 "루프백으로 아무도 안 치는데 왜 열렸다지"로 읽고 넘긴다. 그래서
`AuthNotice` 의 그 갈래가 프록시를 함께 경고하게 한다. 고치는 것은 관측이 아니라 **문장**이다.
