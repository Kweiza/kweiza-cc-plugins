# fd-pick-queue-total — 설계

`pick` 응답이 **남은 큐 열림 수**를 함께 낸다. 세션이 "전체 N건 중 이걸 골랐다"를
사람에게 말할 수 있게 하는 것이 목적이다.

---

## 0. 이 문서가 뒤집은 것 — 처음 안 셋이 전부 틀렸다

처음 설계는 이랬다: `PickResult.QueueOpen int` 를 더하고, `Pick()` **진입부에서
어떤 쓰기보다 먼저** 한 번 세고, 네 모드 전부에 `큐: 열린 항목 N건` 을 찍는다.

세 조각이 각각 틀렸다. 아래 §3·§4·§5 가 그 셋이고, 나머지는 그 결론의 배선이다.

### 뒤집기 1 — "쓰기보다 먼저 센다"가 재개를 재출력이 아니게 만든다

`t.ClaimItem` 이 항목을 `open → claimed` 로 옮긴다(`store/item.go:621`).
진입부에서 세면 `claimed` 응답의 N 에 **방금 집은 그 항목이 들어 있다**.
재개(`resumed`)는 쓰기가 하나도 없고 그 항목이 이미 `claimed` 라 빠진다.

```
열린 항목 5건
pick(item_id: fd-x)        → "큐 열림 5건"   ← fd-x 를 포함한 수
board                      → "큐 열림 4건"
(컨텍스트가 날아가 돌아옴)
pick(item_id: fd-x)        → "큐 열림 4건"   ← 재개. 아무도 아무것도 안 끝냈다
```

`pick.go:178-180` 이 재개 경로를 **"아무것도 쓰지 않는다"**로 못박아 뒀다.
재출력이 원본과 다른 수를 내면 그건 재출력이 아니다. 그리고 이 경로에 오는 세션은
정의상 앞 응답의 기억이 없어서 그 4를 현재 사실로 읽는다.

더 나쁜 것은 각주로 못 덮는다는 점이다. `JudgeClaim`(`store/item.go:69-73`)에
**"항목 상태가 `claimed` 인데 점유자가 없다"** 갈래가 살아 있고, 그 갈래로 들어온 선점은
항목이 애초에 `open` 이 아니라 진입부 카운트에 안 들어간다. 같은 `claimed` 모드에서
오프셋이 어떤 때는 +1, 어떤 때는 0 이다. `(선점 전 기준)` 같은 고정 각주는
그 절반에서 거짓말이 된다.

→ **모든 쓰기가 끝난 뒤에 센다.** §3.

### 뒤집기 2 — `int` 는 "부재"와 "진짜 0"을 뭉갠다

서버는 독립 컨테이너이고 플러그인은 자동 갱신된다. `offline.go:149-158` 이
그 어긋남을 **"운영자가 아무것도 안 해도 생긴다"**고 스스로 적어 뒀다.
그런데 이 레포의 유일한 스큐 감지기 `SkewBanner` 는 `/healthz` 의 `api_version`
**문자열 동일성**만 본다 — 필드 추가는 계약 `v1` 안의 하위호환 변경이라
양쪽 다 `"1"` 을 계속 알리고 **배너가 안 뜬다.**

렌더는 클라이언트에서 돈다(`cmd/fd/cmds.go:245·282`, `mcpsrv` 도 `fd` 프로세스 안이다).
구서버 + 신 `fd` 조합이면 응답에 `queue_open` 키가 없고, `json.Unmarshal`
(`mcpbackend.go:249`)이 `0` 을 채우고, 화면에 **"큐 열림 0건"**이 찍힌다.
`recommended` 모드면 바로 아래 추천 항목이 있어 사람이 모순을 눈치챌 수 있지만
**`none` 모드에는 그 모순을 드러낼 항목이 없다.** 에이전트는 "큐가 비었다"를
사실로 읽고 세션을 접는다.

이건 `pick.go:378-380` 과 `service.go:273-275` 가 이 레포에서 반복해 금지한
**"키 부재를 값으로 접기"** 그 자체다.

→ **`*int` 로 둔다.** §4. 형제 설계(`2026-08-05-fd-item-path-project-mismatch-hint`,
§"`PickResult` 에 붙는 필드는 **포인터**다")가 같은 결론에 독립적으로 도달했다.

### 뒤집기 3 — 추천 경로에서 새로 세면 한 화면이 자기와 모순된다

`candidates()` 가 이미 `ListOpen` 을 부른다(`pick.go:321`). 진입부에서 또 세면
두 관측 사이에 `sessionCards` 가 통째로 끼어드는데, `board.go:232-234` 가
**"이 함수가 이 서버에서 가장 비싼 일이다"**라고 스스로 적어 둔 자리다 —
살아 있는 세션마다 `ChangedPaths`·`AheadBehind`·`UncommittedPaths`.
지금 이 저장소 기준 세션 10건이면 `git` 프로세스 수십 개다.

그 창 안에서 남이 `add` 나 `finish` 를 하나 하면, 렌더가

```
범위: 후보 = 열린 항목 7건 + 살아 있는 세션이 쥔 항목 3건 ...
큐 열림 8건
```

를 **인접한 두 줄**에 찍는다. 같은 술어(`WHERE project=? AND state='open'`)의
두 관측이다. 그리고 시간축보다 큰 문제는 정의축이다 — 나중에 `ListOpen` 의 `WHERE` 가
한 칸 좁아지면 두 줄이 영구히 갈린다. `board.go:105-107` 이
**"같은 판정을 두 자리에 두면 한쪽만 고치는 순간 조용히 어긋난다는 것을
이 파일이 이미 한 번 겪었다"**고 적어 둔 그 패턴이다.

→ **추천·적격0건은 `candidates()` 가 손에 쥔 수를 그대로 쓴다. 새 질의는
`claimed`·`resumed` 두 모드에서만.** §5.

---

## 1. 지금 무엇이 없나

| 모드 | 지금 응답에 큐 규모가 있나 |
|---|---|
| `recommended` | 있다 — `사유`에 "후보 N건 중 1순위", `범위`에 "열린 항목 N건 + …"(`pick.go:299·371`) |
| `none` | 있다 — `사유`가 `scope` 를 통째로 품는다(`pick.go:283-284`) |
| `claimed` | **없다** — `범위`가 `지정된 항목 1건` 뿐이다(`pick.go:170`) |
| `resumed` | **없다** — 같은 자리 |

사람에게 "뭘 골랐다"고 말하는 순간은 `pick(item_id:)` 직후다. 정확히 그 두 모드에
숫자가 없다. 그래서 이 설계가 실제로 결손을 메우는 곳은 `claimed`·`resumed` 다.
`recommended`·`none` 에는 **같은 수를 같은 이름으로 다시 낸다** — 어느 경로로 들어와도
한 이름의 한 줄을 보게 하는 것이 이 변경의 절반이다(§6).

---

## 2. 무엇을 만드나

1. `store.CountOpen(ctx, project) (int, error)`
2. `service.PickResult.QueueOpen *int` — 모드별 채우는 자리는 §5
3. `mcpsrv.RenderPick` 이 네 모드 전부에 **별도 줄** 하나
4. `skills/fd-pickup/SKILL.md` 에 "그 줄을 그대로 옮긴다" 한 줄
5. `DESIGN.md:297` 의 `pick` 행에 조각 하나

새 개념은 0이다. `큐 열림` 은 `board` 가 이미 쓰는 이름이다(`render.go:450·456·466`).

---

## 3. 세는 시점 — 모든 쓰기 뒤, 한 자리

`Pick()` 이 분기를 **값으로 받아** 반환 직전에 채운다.

```go
var res PickResult
var err error
if strings.TrimSpace(in.ItemID) != "" {
    res, err = s.pickExplicit(ctx, proj, in, live, d, now)
} else {
    res, err = s.pickRecommend(ctx, proj, in, live, d, now)
}
if err != nil {
    return PickResult{}, err
}
// 선점 쓰기가 끝난 뒤에 센다. 응답이 [claimed] 라고 찍은 항목을 열림으로 세지 않기
// 위해서다 — pick.go:231 이 항목을 쓰기 뒤에 재조회하기로 이미 정해 뒀고,
// 이 수도 같은 편에 서야 한 응답 안에서 두 줄이 서로를 반박하지 않는다.
s.fillQueueOpen(ctx, proj.ID, &res)
return res, nil
```

`recommended`·`none`·`resumed` 세 모드는 항목 상태를 바꾸는 쓰기가 없으므로
(`RecordPickEval`·`LogEvent` 는 `item.state` 를 안 건드린다) 진입부든 반환 직전이든
값이 같다. 구속력이 있는 곳은 `claimed` 하나이고, 거기서 진입부 안이 정확히 틀린다.
**"먼저 센다"는 아무것도 사지 않고 유일하게 사는 자리에서 오답을 만든다.**

`ObservedAt`(`now := s.now()`, 진입 시각)보다 늦은 값이라는 것은 알고 감수한다 —
항목 블록이 이미 쓰기 뒤 재조회 값이라, 둘을 맞추는 쪽이 응답 내부 정합에 이긴다.

### Tx 안에서 세지 않는 이유

`ClaimItem` 트랜잭션 안에서 세면 남의 `add`/`finish` 와도 못 어긋난다(`BEGIN IMMEDIATE`).
그래도 안 한다: 표시용 숫자 하나 때문에 **선점 트랜잭션의 실패면을 넓히지 않는다.**
지금 그 Tx 가 실패하면 잃는 것은 선점이고, 카운트를 넣으면 카운트 실패가 선점을 죽인다.
바깥에서 세면 최악이 "수를 못 냈다"이고 선점은 남는다.

---

## 4. 타입 — `*int`, 그리고 `nil` 은 침묵이 아니다

```go
QueueOpen *int `json:"queue_open,omitempty"`
```

`nil` 이 되는 경로는 셋이다.

1. **구서버 + 신 클라이언트** — 서버가 키를 안 보낸다(뒤집기 2).
2. **옛 캐시 재생** — 캐시에 스키마 버전도 TTL 도 무효화도 없다(`cache.go:31-49, 65-97`).
   키가 요청 경로의 해시뿐이라, 이 필드가 생기기 전에 굳은 `next` 응답이 그대로 살아남는다.
3. **`CountOpen` 실패** — 사실상 도달 불가다(같은 호출에서 DB 가 이미 여러 번 응답했고
   `item_by_state`(`schema.sql:158`)가 덮는 질의다). 그래도 표현은 필요하다.

렌더는 `nil` 을 **원인 중립 문장**으로 낸다:

```
큐 열림 수가 이 응답에 없다 — 서버 판이 이 축을 안 내거나 세지 못했다
```

원인별로 다른 문장을 쓰지 않는다. `nil` 하나로는 셋을 못 가르고, 가르려고 필드를
하나 더 두면 표시용 숫자 하나가 개념 둘이 된다(`DESIGN.md:53` 원칙 ②).
**이 문장은 스큐 구간의 유일한 신호이기도 하다** — `SkewBanner` 는 이 변경으로 안 뜬다.

### `derive`(`Freshness`·`Failures`)에 넣지 않는다

`derive.note`/`fail` 은 같은 `failures` 슬라이스에 쌓이고
`FreshnessOf(now, d.reads, len(d.failures))` 가 `failures > 0 → Source:"git", Stale:true`
로 접는다(`service.go:255-285`). DB 카운트 한 번이 실패했을 뿐인데 응답 꼬리가
`파생 git@… 낡음` 으로 바뀌고, 세션은 브랜치·HEAD·조상 판정 같은 **진짜 git 축이
낡았다**고 읽는다. 형제 설계가 같은 이유로 같은 결정을 했다(§"`derive` 를 건드리지 않는다").

이 축은 자기 상태를 자기 안에서 말한다 — `nil` 과 위 문장이 그 자리다.

---

## 5. 어디서 세나 — 모드별

| 모드 | 값의 출처 | 질의 추가 |
|---|---|---|
| `recommended` | `candidates()` 가 이미 읽은 `len(open)` | 없다 |
| `none` | 같음 | 없다 |
| `claimed` | `CountOpen` — 선점 Tx 뒤 | 1 |
| `resumed` | `CountOpen` | 1 |

`candidates()` 시그니처가 열린 항목 수를 함께 낸다:

```go
func (s *Service) candidates(...) (cands []judge.Candidate, scope string, openCount int, err error)
```

`pickRecommend` 가 `res.QueueOpen = &openCount` 로 채운다. 그러면 `범위:` 줄의 수와
새 줄의 수가 **같은 변수에서 나와** 구조적으로 못 갈린다(뒤집기 3).

`fillQueueOpen` 은 이미 채워져 있으면 아무것도 안 한다:

```go
func (s *Service) fillQueueOpen(ctx context.Context, project string, res *PickResult) {
    if res.QueueOpen != nil {
        return // 추천 경로가 candidates() 의 관측을 이미 실었다. 두 번 세지 않는다
    }
    n, err := s.st.CountOpen(ctx, project)
    if err != nil {
        s.log.WarnContext(ctx, "큐 열림 수 조회 실패", "project", project, "error", err.Error())
        return // nil 로 둔다. 표시용 숫자 하나 때문에 pick 을 실패시키지 않는다
    }
    res.QueueOpen = &n
}
```

### `store.CountOpen` 은 `ListOpen` 바로 옆에 둔다

```go
// CountOpen 은 열린 항목 수다.
//
// ★ 술어가 ListOpen 과 **같아야 한다**. 다르면 pick 과 board 가 같은 이름으로
// 다른 수를 내고, 그 어긋남은 두 화면을 나란히 놓기 전에는 안 보인다.
// 큐의 정의를 바꿀 일이 오면 이 둘을 함께 고쳐라.
func (s *Store) CountOpen(ctx context.Context, project string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM item WHERE project = ? AND state = 'open'`, project).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("열린 항목 수 조회 실패(project=%q): %w", clip(project, 64), err)
	}
	return n, nil
}
```

`ListOpen` 쪽에도 마주 보는 한 줄을 단다. `Tx` 짝은 만들지 않는다 — 호출자가 없다
(§3 이 Tx 밖에서 세기로 정했다). 인덱스는 `item_by_state`(`schema.sql:158`,
`item(project, state, created_at)`)가 그대로 덮으므로 스키마 변경이 없다.

---

## 6. 문구 — `board` 와 같은 이름

```
큐 열림 5건
```

`큐: 열린 항목 5건` 이 아니다. `board` 렌더가 이미 `큐 열림 N건` 을 쓴다
(`render.go:450·456·466`). **같은 술어에 두 번째 이름을 붙이면, 두 수가 갈리는 순간
읽는 쪽이 "다른 지표라 그런가 보다"로 넘어가 불일치가 조용히 정상으로 등록된다.**
이름이 같으면 최소한 눈에 걸린다. 그리고 §3·§5 를 지키면 두 수는 실제로 같다.

자리는 `사유`/`범위` 다음, 항목 블록 앞이다 — 헤더 세 줄이 "무엇을 했나 · 왜 · 큐가
얼마나 남았나"로 끝나고 그 아래가 본문이다.

### `recommended`·`none` 에서 수가 두 번 나오는 것

`범위: 후보 = 열린 항목 5건 + …` 과 `큐 열림 5건` 이 함께 찍힌다. 감수한다.
`범위` 는 **후보 집합의 구성**을 말하고(왜 후보가 8건인가), 새 줄은 **큐 규모**를 말한다.
그리고 §5 대로 둘이 같은 변수에서 나오므로 갈릴 수 없다. 네 모드에서 같은 자리에
같은 이름의 줄이 있다는 성질이 중복 한 줄보다 값어치가 크다 — 세션이 모드를 보고
어디를 읽을지 고르지 않아도 된다.

---

## 7. 손대는 파일

| 파일 | 무엇 |
|---|---|
| `store/item.go` | `CountOpen` 추가 + `ListOpen` 에 마주 보는 주석 |
| `service/pick.go` | `QueueOpen *int` 필드 · `Pick()` 분기를 값으로 받기 · `fillQueueOpen` · `candidates()` 가 `openCount` 반환 |
| `mcpsrv/render.go` | `RenderPick` 에 줄 하나(+`nil` 문장) |
| `skills/fd-pickup/SKILL.md` | 한 줄 |
| `DESIGN.md:297` | `pick` 행에 조각 하나 |

`internal/api` 는 손대지 않는다 — `handlers_items.go:71·130` 이 `PickResult` 를 전용 DTO
없이 그대로 `json.Marshal` 하므로(`respond.go:24-25`) 새 필드가 `GET /items/next` 와
`POST /items/{id}/claim` 양쪽에 자동으로 실린다.
`internal/web` 도 손대지 않는다 — `page.go` 는 `PickResult` 를 안 쓰고
`BoardView.OpenItems` 로 자체 집계한다(`page.go:480`).

### `SKILL.md` 한 줄

`DESIGN.md:305` 가 "규율 산문을 도구 설명이나 스킬에 넣지 않는다"를 못박고 있다.
그 규칙의 근거는 컨텍스트 예산이고(스킬 목록은 항목당 1,536자에서 절단된다),
`fd-pickup` 은 지금 58줄이다. 한 줄은 그 예산 안이고, 이 스킬은 이미 §4·§5 로
같은 결의 산문을 담고 있다.

문장은 **"숫자를 말한다"가 아니라 "그 줄을 그대로 옮긴다"**여야 한다.
숫자를 말하라고 쓰면, 줄이 없는 경로(§4 의 `nil`, 회수 거절 `mcpsrv.go:565-574`,
오프라인 선점 거절 `offline.go:65-68`, 구 `fd` 바이너리)에서 에이전트가 값을
지어내거나 앞 응답의 수를 재사용한다.

### `DESIGN.md` 조각

297행 `pick` 행 끝에 `**응답에 남은 큐 열림 수가 함께 온다**` 를 붙인다.
297행 **한 줄로 좁게** 유지한다 — 지금 `DESIGN.md` 를 동시에 만지는 세션이 셋이고
각각 §7 398행 · §6 REST 표(315~340) · §2 Tier B(108~151)다. hunk 를 넓히면 닿는다.

---

## 8. 시험

| 무엇 | 어디 |
|---|---|
| `CountOpen` 이 `ListOpen` 과 같은 수를 낸다(빈 큐 · `done`/`dropped` 섞임 포함) | `store/item_test.go` |
| `claimed` 응답의 수가 **방금 집은 항목을 뺀** 수다 — 진입부 카운트였다면 실패한다 | `service/pick_test.go` |
| 같은 항목을 다시 `pick` 한 `resumed` 응답이 `claimed` 응답과 **같은 수**를 낸다 | `service/pick_test.go` |
| `recommended` 의 `QueueOpen` 이 `범위` 문자열의 수와 일치한다 | `service/pick_test.go` |
| `RenderPick` 이 네 모드 전부에 `큐 열림 N건` 을 찍는다 | `mcpsrv/render_test.go` |
| `QueueOpen == nil` 이면 숫자 대신 부재 문장을 찍는다(0건이라고 안 쓴다) | `mcpsrv/render_test.go` |

마지막 줄이 이 설계에서 가장 중요한 시험이다 — 뒤집기 2 가 막으려는 실패가 정확히
"부재가 0으로 찍힌다"이고, 그 실패는 스큐 구간에서만 나타나 사람이 재현하기 어렵다.

이 레포에 골든 파일·고정 JSON·응답 스키마 검증·필드 수 단언은 0건이므로
(`DisallowUnknownFields` 는 도구 **인자**에만 걸린다, `mcpsrv.go:700-712`)
필드 추가만으로 깨지는 시험은 없다. 새 줄을 지키는 시험은 자동으로 안 생긴다 —
위 표를 직접 써야 한다.

---

## 9. 일부러 안 하는 것

- **`open + claimed` 를 세지 않는다.** `board` 가 쓰는 정의와 갈린다.
- **선점 전/후 두 수를 함께 내지 않는다.** 차가 자기 항목 1 뿐이라 정보량이 0이고,
  `JudgeClaim` 고아 갈래에서는 두 수가 같아져 "왜 안 줄었지"라는 새 질문을 만든다.
- **`Tx` 안에서 세지 않는다.** §3 의 마지막 절.
- **`candidates()` 의 `범위` **문구**를 안 고친다.** 시그니처는 §5 대로 `openCount` 를
  더 내지만 문자열은 그대로 둔다 — `후보 = 큐 열림 %d건 + …` 로 이름을 통일하는 안은
  값어치가 작고, `service/pick.go` 를 동시에 만지는 세션이 있어 hunk 를 넓힌다.
- **`web` 대시보드에 같은 줄을 안 넣는다.** 이 요청의 범위 밖이다.
- **`steal_reason` 경로는 그대로 둔다.** `Pick` 을 부르지도 않고 회수 거절 텍스트로
  끝난다(`mcpsrv.go:565-574`).

---

## 10. 동시 작업과의 충돌면

`fd-item-path-project-mismatch-hint` 세션이 **같은 두 자리**를 만진다 —
`PickResult` 에 포인터 필드 추가 + `RenderPick` 출력 블록. 브랜치도 워크트리도 없이
`main` 워킹트리에서 작업 중이다.

충돌은 구조가 아니라 줄이다. 두 설계가 같은 결론(포인터 · `derive` 안 건드림 ·
`RenderPick` 한 줄)에 독립적으로 도달했으므로 합칠 때 의미 충돌은 없다.
**착수 전에 `note(kind: "ask")` 로 알린다.**

`DESIGN.md` 는 297행 한 줄이라 다른 셋(398 · 315~340 · 108~151)과 안 겹친다.

---

## 11. 이 판정이 뒤집히는 조건

- **큐의 정의가 `open` 이 아니게 되면** — 예컨대 `paused` 상태가 생기거나
  "큐 = `open` ∧ 선행 충족"으로 바뀌면 `CountOpen`·`ListOpen`·`board` 셋을 함께 고쳐야 한다.
  §5 의 마주 보는 주석이 그 자리를 가리킨다.
- **`PickResult` 에 기계 소비자가 생기면** — 지금은 0이다(`web` 은 안 쓴다).
  생기면 `nil` 을 문장이 아니라 값으로 다루는 층이 필요해진다.
- **캐시에 스키마 버전축이 생기면** — §4 의 `nil` 경로 2가 사라진다. 그래도 경로 1(스큐)은 남는다.
