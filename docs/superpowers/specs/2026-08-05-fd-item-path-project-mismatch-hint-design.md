# fd-item-path-project-mismatch-hint — 설계

`pick` 이 항목을 낼 때, **그 항목의 경로가 이 프로젝트에 없다**는 사실을 진단과 함께 낸다.
오등록을 add 응답에서 놓친 뒤에도 알아채게 하는 **두 번째 그물**이다.

- 항목: `fd-item-path-project-mismatch-hint`
- 선행: `sha:b315980` (add 응답이 프로젝트와 되돌리는 명령을 낸다)
- 날짜: 2026-08-05

---

## 0. 이 문서가 뒤집은 것 — 항목이 제안한 규칙은 그대로 쓰면 안 된다

항목은 규칙을 이렇게 제안했다:

> 경로가 **전부** 없고 그중 하나라도 **다른 등록된 프로젝트에는 있을 때**만 말한다
> — '없다'가 아니라 '저기 있다'가 근거가 된다.

그리고 "비용과 실패 처리를 먼저 재라"고 못박았다. **쟀다.** 그 측정이 규칙을 한 줄 바꿨다.

### 정답이 있는 데이터가 실물로 있었다

`~/.flightdeck/fd.db.before-move` 가 오등록 10건을 **옮기기 전 상태로** 보존하고 있다.
그래서 제안된 규칙을 재현 가능한 정답 위에서 채점할 수 있었다.

| 규칙 | 실제 오등록 10건 중 잡음 | 지금 큐 240건에서 헛발화 |
|---|---|---|
| **V1 — 항목이 제안한 규칙** (여기 전부 없다 ∧ 어딘가엔 있다) | 9 | **5** |
| **V2 — V1 ∧ 매치가 프로젝트 하나를 유일하게 지목** | 9 | **0** |
| V3 — V1 ∧ 매치 경로가 2성분 이상 | 9 | 2 |
| V5 — V2 ∧ 그 프로젝트가 경로 전부를 가짐 | 9 | 0 |

**잡는 건수는 넷 다 같고, 헛발화만 갈린다.** 그러니 가장 싼 판별자를 고르면 된다.

### 헛발화 5건의 정체가 판별자를 지목했다

전부 `context-platform` 항목이고 경로가 `docs/` · `docs/superpowers/specs/` 다.
그 이름은 등록된 다른 세 프로젝트에 **전부** 있다.

즉 **"저기 있다"가 세 곳을 동시에 가리키면 그것은 지목이 아니라 잡음이다.**
`docs/` 가 어디에나 있다는 사실은 이 항목이 어디 소속인지에 대해 아무것도 말하지 않는다.

그래서 규칙에 한 줄을 더한다: **유일하게 지목할 때만 오등록이라고 말한다.**

그리고 이 항목들은 계속 열려 있으므로 V1 의 경보는 **영구히** 뜬다.
항목이 두려워한 "상시 경고는 결국 안 읽힌다"가 정확히 이 모양으로 재현된다 —
다만 그 두려움의 근거는 항목이 지목한 "아직 없는 파일"이 아니라 **흔한 디렉토리 이름**이었다.

### 못 잡는 것 하나를 알고 간다

`fd-ci-timing-baseline` 의 경로는 `Makefile` · `deploy/bootstrap/` 였고
**틀린 프로젝트(context-platform)에 진짜로 존재했다.** 존재 기반 규칙으로는 원리적으로 못 잡는다.
**10건 중 9건이 이 축의 상한이다.** 이 문장을 숨기지 않는다.

### 비용은 장벽이 아니었다

항목은 "서버가 다른 프로젝트의 저장소를 읽어야 한다 — 비용과 실패 처리를 먼저 재라"고 했다.
**git 을 읽을 필요가 없다.** `os.Stat` 이면 된다.

| 범위 | stat 횟수 | 시간 |
|---|---|---|
| 큐 240건 전수 × 프로젝트 5개 | 1,340 | 2.30ms |
| 2단(자기 먼저, 전부 없을 때만 확장) 전수 | 259 | 0.46ms |
| **pick 이 실제로 보는 범위(항목 1건)** | 27 | **0.048ms** |

지금 보드 한 장의 git 파생이 이미 8~60ms 다. **이 축은 그 옆에서 반올림 오차다.**

### 헛걸림의 실제 크기도 쟀다

항목은 "새 기능 항목은 거의 전부 아직 없는 경로를 가리킨다"고 했다. **실측은 그렇지 않다.**

열린/선점 항목 240건 중:

| | 건수 |
|---|---|
| 경로가 전부 이 프로젝트에 있다 | 202 |
| 경로가 0개다 | 26 |
| **경로가 전부 없다** | **11** |
| 일부만 있다 | 1 |

그리고 "전부 없다" 11건 중 **아직 없는 파일은 0건이다.**
사람들이 `docs/decisions/` 처럼 **디렉토리**로 적기 때문에 그 축이 자연히 흡수된다.

---

## 1. 무엇을 만드나

`pick` 응답에 **경로 축 한 줄**을 항상 싣고, 이상하면 진단과 처방을 함께 낸다.

표면은 `pick` 하나뿐이다. 그 결정의 근거:

- `pick` 은 `cd '<프로젝트 경로>'` + `git worktree add` 를 **그대로 실행하라고** 낸다.
  오등록이면 그 명령이 **틀린 레포에 브랜치를 만든다** — 해가 실제로 나는 자리가 정확히 여기다.
- `pick` 응답에 실리는 항목은 **언제나 한 건**이다. 그래서 경보가 상시 점등될 자리가 구조적으로 없다.
  그 항목을 집는 그 순간에 한 줄 뜨고 끝이다.
- `board` 는 자주 부르는 도구다. 안 집을 항목의 경고까지 매번 실으면 그것이 마모의 시작이다.

---

## 2. 판정 — 여섯 갈래

진입점은 **"경로가 전부 이 프로젝트에 없다"** 이고, 거기서 진단이 갈린다.

| Kind | 조건 | 뜻 | 처방 |
|---|---|---|---|
| `no-paths` | 항목에 경로가 0개다 | 판정할 재료가 없다 | 겹침 축에 안 잡힌다고 말한다 |
| `ok` | 한 경로라도 여기 있다 | 이 항목은 여기 앵커돼 있다 | 없음(한 줄만) |
| `misregistered` | 여기 전부 `Absent` ∧ 다른 프로젝트 **정확히 하나**가 지목됨 | 오등록이다 | `fd move <id> --project <그것>` |
| `ambiguous` | 여기 전부 `Absent` ∧ 둘 이상 지목 | **지목이 아니다** | 없음 — 근거가 약하다고 말한다 |
| `nowhere` | 여기 전부 `Absent` ∧ 어디에도 0 | 경로가 틀렸거나(뿌리 잘림) 레포가 미등록이다 | 겹침 축이 죽어 있다고 말한다 |
| `unknown` | 여기에 `PathUnknown` 이 **하나라도** 있다 | **"없다"가 아니다** | 무엇을 못 읽었는지 낸다 |

**판정 순서가 곧 우선순위다**: `no-paths` → `ok` → `unknown` → 나머지 셋.

`ok` 가 `unknown` 보다 앞에 있는 이유: 한 경로라도 여기 **실재하는 것을 봤으면**
다른 경로를 못 읽었어도 그 항목이 여기 앵커돼 있다는 결론은 흔들리지 않는다.
반대로 `unknown` 이 남은 셋보다 앞에 있는 이유: 그 셋은 전부 **"여기 없다"를 전제**하는데,
`PathUnknown` 이 섞여 있으면 그 전제 자체가 관측되지 않은 것이다.
못 읽은 경로 하나를 `Absent` 로 접으면 정확히 이 기능이 없애려는 종류의 거짓말이 된다.

### `no-paths` 를 갈래로 두는 이유

열린/선점 항목 240건 중 **26건이 경로 0개**다(11%). "항상 한 줄"을 지키려면
이 11%에 할 말이 있어야 하고, 그 말은 `ok` 와 다르다 —
경로가 없는 항목은 이상이 없는 것이 아니라 **겹침 축에 아예 안 잡힌다.**
`RenderAdd` 가 등록 시점에 이미 같은 문장을 낸다. 여기서는 집는 시점에 한 번 더 낸다.

### `ambiguous` 를 별도 갈래로 두는 이유

`ok` 로 접으면 "봤는데 깨끗하다"가 되고, `misregistered` 로 접으면 헛발화 5건이 부활한다.
**둘 다 거짓이다.** 실제로 일어난 일은 "봤고, 이상하고, 그런데 어디라고는 못 말한다"이며
그 셋째 상태에 이름이 없으면 반드시 두 거짓 중 하나로 접힌다.

### `nowhere` 가 실제로 잡는 것 — 오늘 큐의 진짜 결함 2건

`nowhere` 는 오늘 6건 발화하고 그중 **2건이 지금 바로 고칠 수 있는 결함**이다:

- `fd-live-window-baseline` — `internal/service/service.go`
- `fd-prescribe-threshold-baseline` — `internal/judge/prescribe.go`

실물은 `plugins/flightdeck/server/internal/…` 다. **뿌리가 잘린 경로**이고,
그래서 이 둘은 **지금 겹침 축에서 아무도 안 막는다.** 화면에는 그 사실이 어디에도 없다.

나머지 4건은 `/home/aaron/cdo-dev/design-context-platform`(context-platform 의 문서 레포)을 가리키는데
**그 레포가 flightdeck 에 프로젝트로 등록돼 있지 않다.** 오등록이 아니라 미등록이다 —
그래서 문구가 두 가능성을 함께 낸다.

---

## 3. 구조 — 새 개념 0, 기존 경계 그대로

### `internal/judge/itempaths.go` — 순수 판정

§12: 판정은 시험이 부르는 순수 함수에 있어야 한다. I/O 는 이 파일에 없다.

```go
// PathPresence 는 경로 하나를 한 저장소에서 찾아본 결과다. 셋을 가른다.
// ★ 0값이 PathUnknown 이다 — "못 봤다"가 "없다"로 접히면 이 기능 전체가 거짓말이 된다.
type PathPresence int

const (
	PathUnknown PathPresence = iota
	PathAbsent
	PathPresent
)

type Kind string

const (
	KindNoPaths       Kind = "no-paths"
	KindOK            Kind = "ok"
	KindMisregistered Kind = "misregistered"
	KindAmbiguous     Kind = "ambiguous"
	KindNowhere       Kind = "nowhere"
	KindUnknown       Kind = "unknown"
)

// ItemPathInput 은 판정에 필요한 **관측 결과**다. 이 구조체는 파일시스템을 모른다.
type ItemPathInput struct {
	Project    string
	Paths      []string
	Here       map[string]PathPresence            // 경로 → 이 프로젝트에서 본 결과
	Elsewhere  map[string]map[string]PathPresence // 프로젝트 → 경로 → 결과
	Unreadable []string                           // 아예 못 연 프로젝트
}

type ItemPathVerdict struct {
	Kind       Kind     `json:"kind"`
	Suggest    string   `json:"suggest,omitempty"`    // 유일 지목일 때 그 프로젝트 id
	Candidates []string `json:"candidates,omitempty"` // 여럿이 지목될 때 전부(정렬)
	Unreadable []string `json:"unreadable,omitempty"` // 판정 근거가 그만큼 약하다
	Summary    string   `json:"summary"`              // 한 줄. ★ 항상 채운다
}

func ClassifyItemPaths(in ItemPathInput) ItemPathVerdict
```

**json 태그를 반드시 단다.** 이 값은 REST 를 왕복한다(§4). `internal/judge` 안에 판례가
갈려 있어서 — `prescribe.go` 는 소문자 태그를 달고 `eligible.go`·`after.go` 는 안 단다 —
안 달면 `"Kind"`·`"Summary"` 처럼 대문자 키가 나가고, 그 모양이 굳으면 되돌릴 수 없다.

### `PickResult` 에 붙는 필드는 **포인터**다

```go
PathCheck *judge.ItemPathVerdict `json:"path_check,omitempty"`
```

값 타입이면 안 되는 이유가 둘이다.

1. **§4 의 "`none` 에는 내지 않는다"가 값 타입으로는 표현이 안 된다.** 제로값 구조체가
   실려 나가고, 그 제로값의 `Kind` 는 `""` 라 여섯 갈래 어디에도 없는 유령 상태다.
2. **오프라인 `fd next` 는 디스크 캐시의 옛 바이트를 그대로 다시 낸다.** 이 필드가 생기기
   전에 저장된 캐시에는 그 키가 없으므로 역직렬화 후 제로값이 온다. 포인터면 `nil` 이고,
   `nil` 은 **"이 응답은 그 축을 안 읽었다"** 로 정확히 읽힌다. 값 타입이면 그 상황이
   `Kind: ""` 가 되어, 낡은 캐시가 관측한 적 없는 사실을 단정하게 된다.

`RenderPick` 은 `nil` 을 침묵으로 접지 않는다 — §5 의 마지막 문구가 그 자리다.

### `internal/service` — I/O 한 겹

`os.Stat` 을 2단으로 부른다: **자기 프로젝트를 먼저 보고, 전부 없을 때만 남을 본다.**
pick 이 보는 항목은 언제나 1건이므로 최악이 `경로수 × 프로젝트수` 다.

`git` 을 쓰지 않는다:

- 프로세스 스폰이 stat 보다 3~4자릿수 비싸다.
- `git ls-files` 는 **미추적 파일을 못 본다** — 방금 만든 파일을 가리키는 항목이 오탐이 된다.
- `git cat-file -e HEAD:<path>` 는 **워크트리 상태를 못 본다** — 지금 체크아웃된 브랜치와 어긋난다.

우리가 답하려는 질문은 "이 경로가 이 레포에 실재하는가"이고, 그 질문의 정본은 파일시스템이다.

### `internal/mcpsrv/render.go`

`RenderPick` 에 한 줄(처방이 필요하면 한 줄 더). **도구 표는 손대지 않는다.**

---

## 4. 데이터 흐름

```
Pick
 └─ sessionCards(…)                     ← 기존. 안 건드린다
 └─ checkItemPaths(item, ListProjects()) ← 새것. os.Stat 2단
     └─ judge.ClassifyItemPaths(…)       ← 순수
 └─ PickResult.PathCheck                 ← 새 필드
     └─ RenderPick                       ← 한 줄
```

`PickResult` 의 필드이므로 REST 응답(`/items/next` · `/items/{id}/claim`)에도 **자동으로** 실린다.
§1 ③ 그대로다 — 둘 다 같은 순수 함수를 부르고, 두 벌이 아니다.

`recommended` · `claimed` · `resumed` 세 모드 전부에서 낸다.
셋 다 워크트리 준비 명령을 내므로 셋 다 같은 해를 낼 수 있다.
`none`(적격 0건)에는 항목이 없으므로 내지 않는다 — 관측할 대상이 없다.
렌더에서는 이것이 **`if r.Item != nil` 블록 안**이라는 사실로 자연히 표현된다.

**손으로 옮겨 적는 자리가 없다.** `internal/api/handlers_items.go` 가 `PickResult` 를
통째로 `writeJSON` 하고, `cmd/fd/mcpbackend.go` 와 `cmd/fd/cmds.go` 가 통째로
`json.Unmarshal` 한다. 그래서 필드 하나를 `pick.go` 에 더하면 REST 를 자동으로 왕복한다.
`mcpsrv.Backend` 의 `Pick` 서명도 안 바뀌므로 구현 셋(`*service.Service` ·
`cmd/fd` 의 `*mcpBackend` · 시험용 `serialProbe`)이 그대로 컴파일된다.

**렌더러도 하나뿐이다.** `fd next` 와 `fd pick` 은 자기 렌더러가 없고 둘 다
`mcpsrv.RenderPick` 을 부른다. 그래서 `render.go` 한 자리를 고치면
MCP `pick` 도구 · `fd next` · `fd pick` 셋이 동시에 새 줄을 낸다.

**자동으로 안 흐르는 곳 둘 — 이번 범위 밖이다.**
① 웹 대시보드(`internal/web`)는 `PickResult` 를 한 번도 안 만지고 자기 SQL 로 `pick_eval` 을 읽는다.
② SSE 알림(`handlers_items.go` 의 `publish`)은 손으로 고른 두 축만 싣는다.

---

## 5. 문구 — 실물 그대로

**접두는 `경로 실재:` 다.** `RenderPick` 은 이미 `경로: <목록>` 으로 항목이 선언한 경로를
찍고 있다(`render.go:546`). 같은 접두를 쓰면 한 응답에 `경로:` 로 시작하는 줄이 둘 생겨
어느 쪽이 선언이고 어느 쪽이 관측인지 읽는 쪽이 못 가른다.
그리고 이 블록은 **그 선언 줄 바로 아래**에 붙는다 — 선언과 관측이 나란히 읽혀야 한다.

```
경로: plugins/flightdeck/server/internal/mcpsrv/, DESIGN.md
경로 실재: 2개 전부 이 프로젝트(kweiza-cc-plugins)에 있다.

경로 실재: 3개 전부 이 프로젝트(context-platform)에 없다 — kweiza-cc-plugins 에 3개 다 있다.
           오등록일 수 있다. 맞다면: fd move fd-migrate-oneshot --project kweiza-cc-plugins

경로 실재: 1개 전부 이 프로젝트(context-platform)에 없다. 등록된 다른 3개 프로젝트에도
           같은 이름이 있어 어느 하나를 지목하지 못한다(docs/) — 근거로 쓰지 않는다.

경로 실재: 1개 전부 이 프로젝트(kweiza-cc-plugins)에 없고 등록된 어느 프로젝트에도 없다.
           경로가 틀렸거나(뿌리가 잘렸을 수 있다) 그 레포가 아직 등록 안 됐다.
           지금 이 항목은 겹침 축에서 아무도 안 막는다.

경로 실재: 3개 중 1개를 못 읽었다 — '없다'가 아니다(plugins/…: permission denied).
           나머지 2개는 이 프로젝트에 없다. 이 축은 판정하지 않았다.

경로 실재: 경로 0 — 이 항목은 겹침 축에 안 잡힌다. 아무도 안 막고, 아무도 이 항목을 못 피한다.

경로 실재: 이 응답은 그 축을 읽지 않았다(낡은 캐시일 수 있다).
```

마지막 줄은 `PathCheck == nil` 일 때다(§3 의 포인터 근거를 보라).
**침묵으로 접지 않는다** — 그러면 낡은 캐시가 "이상 없다"처럼 보인다.

`Unreadable` 이 비어 있지 않으면 어느 갈래든 뒤에 붙인다(이름은 실제 못 읽은 프로젝트다):

```
      (등록 프로젝트 2개를 못 읽었다: <id>, <id> — 지목이 그만큼 약하다)
```

### 왜 이상이 없어도 한 줄을 찍나

`RenderTail` 이 겹침 0건일 때도 "겹침: 없음"을 반드시 찍는 것과 같은 규율이다.
침묵하면 **"이상 없다"와 "이 축을 안 봤다"가 같은 화면**이 되고,
그러면 stat 이 전부 실패한 날에도 pick 은 평소와 똑같아 보인다.
설계가 겨냥하는 뿌리 원인 중 하나가 "도구가 자기가 무엇을 안 보는지 모르는 채 초록불을 낸다"이다.

---

## 6. 오류 처리

| 상황 | 처리 |
|---|---|
| **루트를 먼저 stat 한다** | 저장소 루트가 디렉토리가 아니면 그 프로젝트의 경로를 **하나도 안 본다** |
| `proj.Path` 가 비었다 | 전부 `PathUnknown` → `KindUnknown`. **`nowhere` 로 접지 않는다** |
| 이 프로젝트를 못 열었다 | 같음. 사유(errno 문자열)를 문장에 싣는다 |
| 다른 프로젝트를 못 열었다 | 이름을 `Unreadable` 에 담고 **문장에 낸다** — 유일 지목이 그만큼 약해진다 |
| 경로 토큰이 루트 **밖으로** 나간다(`..`) | `PathUnknown` + 그 사실을 문장에 낸다. **stat 하지 않는다** |
| `Stat` 이 `ErrNotExist` | `PathAbsent` |
| `Stat` 이 그 밖의 오류(권한·I/O) | `PathUnknown`. 절대 `Absent` 가 아니다 |
| 경로 일부만 `PathUnknown` | `KindUnknown`. 남은 것이 전부 `Absent` 여도 오등록이라 말하지 않는다 |
| 심볼릭 링크 | `os.Stat` 이 따라간다. 그대로 둔다 |
| 프로젝트가 이것 하나뿐 | `Elsewhere` 가 비어 `nowhere` 로 간다 — 정확하다 |

### 루트를 먼저 재는 이유 — 이게 없으면 표의 두 행이 서로 모순이다

"프로젝트를 못 열었으면 `Unknown`" 과 "`Stat` 이 `ErrNotExist` 면 `Absent`" 는
루트가 통째로 없을 때 **같은 errno 로 충돌한다**. `os.Stat(filepath.Join(없는루트, p))` 도
`ErrNotExist` 이므로, 루트를 따로 재지 않으면 죽은 프로젝트가 전부 `Absent` 로 접히고
그 항목이 `nowhere` 나 `misregistered` 로 고발당한다.

**루트 stat 이 그 충돌을 없애는 유일한 단계다.** 시험 5번이 정확히 이 경로를 친다.

### `..` 를 정규화하지 않고 거절하는 이유

`judge.components`(`paths.go:93`)가 `..` 를 **일부러 안 걷어낸다** — "등록 목록에 `..` 가
들어오는 것은 입력 오류이고, 조용히 정규화하면 그 오류가 안 보인다"가 그 자리의 주석이다.
같은 규율을 여기서도 지킨다. 그리고 `filepath.Join(root, "../../etc")` 에 그대로 stat 하면
**프로젝트 밖을 관측한다** — 판정이 남의 디렉토리에 기대게 된다.

밖으로 나가는지는 `RelPath`(`service.go:363`)와 같은 방식으로 본다:
`filepath.Rel` 로 성분 단위 계산 후 결과가 `..` 로 시작하면 밖이다.
**문자열 접두로 자르지 않는다** — 그 결함이 이 레포에 실재했다.

### `derive`(`Freshness`·`Failures`)를 건드리지 않는다

stat 성공을 `d.ok()` 로 세면 **git 을 한 번도 안 읽은 응답이 `Source: "git"`** 이 되고,
stat 실패를 `d.fail()` 로 세면 **git 이 멀쩡한 응답이 `Stale: true`** 가 된다.
`FreshnessOf`(`service.go:265`)가 `reads == 0 → "db"`, `failures > 0 → Stale` 로
정의돼 있어서 어느 쪽이든 다른 축의 뜻을 오염시킨다.

그래서 이 축은 **자기 상태를 자기 안에서 말한다** — `Unreadable` 과 `KindUnknown` 이 그 자리다.
`Derived` 는 git 파생 전용으로 남긴다.

### stat 에 상한을 두지 않는다

죽은 NFS 마운트에서 `os.Stat` 은 멈출 수 있다. 그래도 시임을 안 만든다:

- 같은 위험을 이미 `diskFreePct`(`disk_unix.go`, `syscall.Statfs`)가 상한 없이 안고 있다.
  여기만 다르게 하면 두 자리의 규율이 갈린다.
- 호출 수가 **항목 하나의 경로수 × 프로젝트수**로 구조적으로 묶여 있다(실측 27회).
- 상한을 두려면 `Service` 에 Option 과 필드를 새로 만들어야 하는데,
  그것은 이 기능이 아니라 **파일 관측 전반**의 결정이다.

**이 판단이 뒤집히는 조건**: 원격 마운트를 프로젝트로 등록하는 날. 그때는 `diskFreePct` 와
함께 한 축으로 다룬다 — 이 기능만 따로 고치지 않는다.

**막지 않는다.** 어느 갈래에서도 `pick` 은 그대로 선점한다.
§5 의 규율 그대로다 — 거르지 않고 알린다. 거르면 이 축의 오판이 곧 큐의 막힘이 된다.

---

## 7. 일부러 안 하는 것

| 안 한다 | 왜 |
|---|---|
| **부분 미스를 경보로 만들기** | 하나라도 여기 있으면 `ok` 다. 실물 `dash-real-data-render`(`slides/` 없음 + `tools/` 있음)가 **정당한** 교차 레포 항목이고, 부분을 경보로 만들면 그것이 상시 경보가 된다 |
| **미등록 레포를 찾아 나서기** | 디스크를 뒤져 `design-context-platform` 을 지목하지 않는다. 등록된 것만 본다 — 남의 디렉토리를 훑는 것은 이 서버가 하는 일이 아니고, 그 비용은 여기서 잰 0.05ms 와 자릿수가 다르다 |
| **`board` 에 싣기** | 안 집을 항목의 경고를 자주 쓰는 도구에 매번 실으면 그것이 마모의 시작이다 |
| **도구 늘리기** | 여섯 그대로. `TestFixingMisregistrationDidNotGrowTheToolTable` 이 계속 지킨다 |
| **`git` 으로 읽기** | §3 에 근거를 적었다 |
| **선점 막기** | §6 마지막 줄 |

---

## 8. 시험 — 소비자 좌표계(§12)

단정 대상은 **MCP 응답 문자열**이다. 구현의 개념을 빌리지 않는다.

1. `TestPickNamesMisregistrationWithTheOneProjectThatHasThePaths`
   실물 재현(`fd-migrate-oneshot` 모양) → `fd move … --project <그 프로젝트>` 가 응답에 있다.
2. `TestPickDoesNotAccuseWhenManyProjectsShareThePath`
   `docs/` 모양(세 프로젝트가 공유) → **move 처방이 없다.** V1 의 헛발화 5건이 여기서 죽는다.
3. `TestPickSaysNowhereWhenNoRegisteredProjectHasThePath`
   뿌리 잘린 경로 모양 → "등록된 어느 프로젝트에도 없다" + 겹침 축이 죽었다는 문장.
4. `TestPickStatesThePathAxisEvenWhenClean`
   이상이 없어도 경로 줄이 **있다**.
5. `TestPickDistinguishesUnreadableFromAbsent`
   프로젝트 경로를 지우고 → "못 읽었다"가 나오고 **"없다"는 안 나온다**.
6. `TestClassifyItemPaths` — 순수 함수 표 시험. 여섯 Kind × `Unreadable` 섞임.
   **우선순위를 직접 단정한다**: `Absent` 둘 + `Unknown` 하나 → `unknown`(오등록이 아니다),
   `Present` 하나 + `Unknown` 하나 → `ok`.
7. 회귀: `TestToolTableIsSix` · `TestFixingMisregistrationDidNotGrowTheToolTable` 그대로 통과.

각 시험은 §12 대로 **망가진 것을 먼저 넣어 빨간불을 확인한 뒤** 통과로 친다.

---

## 9. 이 판정이 뒤집히는 조건

- **등록 프로젝트가 많아지고 경로가 짧아지면** `ambiguous` 가 늘어 이 축의 값이 떨어진다.
  그때는 판별자를 "유일 지목"에서 "유일 지목 ∧ 매치 경로가 2성분 이상"(측정한 V4)으로 좁힌다.
- **문서 레포를 프로젝트로 등록하면** 오늘의 `nowhere` 4건이 `misregistered` 나 `ok` 로 바뀐다.
  그것은 이 기능의 실패가 아니라 정상 동작이다.
- **`fd import` 로 대량 유입이 다시 일어나면** 이 축의 값이 가장 크게 나타난다 —
  대량 수입은 add 응답을 안 보는 유일한 경로다. 그때 `import` 요약에도 싣는 것을 재검토한다
  (이번 범위에서는 뺐다).
