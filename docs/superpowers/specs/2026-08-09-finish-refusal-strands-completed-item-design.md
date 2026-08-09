# 닫히지 못한 항목이 큐의 머리에 서는 것을 막는다

- 항목: `fd-finish-refusal-strands-completed-item`
- 날짜: 2026-08-09
- 경로: `internal/store/event.go` · `internal/judge/eligible.go` · `internal/judge/bundle.go` ·
  `internal/service/pick.go` · `internal/mcpsrv/render.go` · `internal/web/page.go` ·
  `internal/web/dashboard.gohtml` · `internal/web/actions.go` · `DESIGN.md`

## 1. 사고의 궤적 — 원장이 통째로 갖고 있다

항목 본문이 서술한 사고를 원장에서 다시 쟀다(`~/.flightdeck/fd.db` 사본, 읽기 전용).

| 시각 (UTC) | 사건 | 원장 |
|---|---|---|
| 08-04 23:54:37 | 세션 …P26E68 이 `finish` 호출 → 선점 표류로 거절·**롤백** | `item.finish {mode:done, bytes:10300, count:0}` + `item.finish.fail` |
| 08-05 12:08:11 | 사람이 대시보드에서 선점 회수 → 항목이 `open` 이 된다 | `web.claim.reclaim` |
| 08-05 22:54 | `pick` 이 후보 26건 중 **1순위**로 추천 | `pick_eval` |
| 08-05 23:00:55 | 두 번째 `finish` 성공 | `item.finish {bytes:6178, count:1}` |

**추천 시점에 신호는 이미 원장에 있었다.** 08-04 의 `item.finish` 이벤트가 08-05 22:54 에도
그대로 있었고 항목은 `open` 이었다. 아무도 그것을 읽지 않았을 뿐이다.

같은 모양이 넷이다 — `fd-windows-path-overlap-silent` · `fd-session-worktree-is-cwd-not-repo-root` ·
`t6-console-browser-gate` · `fd-prescribe-unclaimed-fires-after-finish`. 넷 다 `item.finish` 가
두 번 났고, 첫 번째가 `item.finish.fail` 과 **같은 초**에 짝지어 있다. 롤백된 시도가 쓴 판단
본문은 각각 10300 · 5421 · 5060 · 3032 바이트다 — **완성된 핸드오프가 통째로 죽었다.**

## 2. 무엇을 사실로 삼나

**`item.finish` 이벤트가 있는데 그 항목이 `done`/`dropped` 이 아니면 — 그 시도는 롤백됐다.**

근거 셋을 코드로 확인했다.

- `Tx.LogEvent` 는 `deferred` 에 예약되고 `Store.Tx` 가 **롤백 갈래에서도** `flushDeferred` 를
  부른다(`store/store.go:346-352`, 주석: "예약 이벤트는 롤백 뒤에도 흘린다"). `Finish` 는 tx 의
  **첫 문장**에서 그것을 예약하고(`service/finish.go:233`), 선점 표류 거절은 그보다 뒤인
  tx 안 `FinishItem` 에서 난다(`store/item.go:806`). 사고 경로에서 이벤트는 반드시 남는다.
- `done`/`dropped` 은 종점이다. `item.state` 를 쓰는 자리를 전수 조사했다 —
  `AddItem` INSERT · `SetItemState` 두 갈래 · `ReleaseClaim` · `ForceReleaseClaim` ·
  `DeleteItem`(생산 호출부 0건) · `MoveItem`. 되돌리는 둘은 `AND state='claimed'` 가드가 있고,
  종료 재실행은 `JudgeDropTarget` 이, 재선점은 `JudgeClaim` 이 막는다. 마이그레이션 다섯은
  `item` 표를 아예 안 건드린다.
- 오탐이 없다. 실측 `item.finish` 383건을 항목 상태와 조인하면 `done` 305 · `dropped` 75 ·
  항목 행 없음 3 — **`open`/`claimed` 은 0건이다.**

### 축의 뜻을 정확히 적는다

이 축은 "일이 끝났다"가 아니라 **"이 항목은 닫혀야 한다는 판단이 본문과 함께 내려졌다"** 이다.

`JudgeFinish` 의 body 관문(`service/finish.go:131`)을 지나야만 tx 에 들어가므로, tx 안에서
죽은 여섯 자리(`AddJudgment` · `AddItem` · `FinishItem` · `ListHeld` · `ReleaseResource` ·
`CloseLandingRow`)는 **예외 없이** 그 함의를 갖는다. 갈릴 것이 없다.

그래서 `mode` 는 `done` 과 `dropped` 를 **둘 다 센다**(실측 383건 중 dropped 76건, 20%).
둘은 처방이 갈리므로 수를 따로 담고 문구도 따로 낸다 — `done` 은 "이미 랜딩됐을 수 있다",
`dropped` 는 "이미 버리기로 판정됐을 수 있다".

## 3. 정한 것

- **탈락시키지 않는다. 강등하고, 왜 강등했는지 말한다.** 거르면 선점 표류 아닌 이유로 롤백된
  항목까지 큐에서 사라진다. `Overlaps` 가 "거르지 않고 알린다"로 선 것과 같은 자리다.
- **새 이벤트 종류를 만들지 않는다.** 신호가 이미 원장에 있고, 새로 만들면 지금 큐에 쌓인
  과거 사례에 소급되지 않는다.
- **파생을 저장하지 않는다.** `item` 에 컬럼을 더하지 않는다 — 원장과 갈라지면 되짚을 길이 없다.
- **강등에 유효기간을 걸지 않는다.** 항목을 위험하게 만든 조건은 시간이 지난다고 낫지 않는다.
  기한 만료는 곧 사고 재현이다. 루프 차단은 §4-② 의 축 순서가 이미 제공한다.
- **화면은 두 시점 모두를 덮는다.** 회수 폼의 줄은 회수하는 순간 사라지는데 사고는 그 다음에
  난다. 큐 표가 두 시점을 잇는 유일한 표면이다.

## 4. 구조

### ① 저장층 — 원장을 한 번 긁어 항목별로 접는다

```go
// CloseDeclaration 은 이 항목을 닫으려다 롤백된 선언이다.
type CloseDeclaration struct {
    Done, Dropped int       // mode 별 수. 처방이 갈리므로 합치지 않는다
    Last          time.Time
    LastSession   string
    LastMode      string
}

func (s *Store) CloseDeclarationsByItem(ctx context.Context, project string) (map[string]CloseDeclaration, error)
```

- `WHERE project = ? AND kind = 'item.finish'` 한 번. `QueueReproduction`(`store/event.go:182-185`)이
  세운 프로젝트 스코프 읽기 선례 그대로다. `event` 인덱스는 `(kind,at)`·`(session_id,at)` 뿐이라
  `ORDER BY id DESC LIMIT` 로 자른다.
- payload 는 Go 에서 파싱한다 — `json_extract` 선례가 저장소에 0건이고,
  `eventItemID`(`service/finish_followups.go:188`)가 이미 같은 일을 한다. 못 읽은 행은 **안 센다**.
**앵커와 항목 존재 판정은 `store` 가 아니라 `service` 가 한다.** `store` 는 원장만 읽어 항목별
원자료를 내고, `candidates()`(`pick.go:893`)가 이미 손에 쥔 `items` 로 두 가지를 건다. SQL 조인을
쓰면 `json_extract` 를 조인 조건에 넣어야 하는데 그 선례가 저장소에 0건이다.

- **시간 앵커.** 그 항목의 `item.CreatedAt` **이후**의 선언만 센다. `item` 의 PK 가
  `(project, id)` 라 지워졌다 다시 만들어진 id, 프로젝트를 옮겨 비워진 뒤 재사용된 id 가
  옛 이벤트를 물려받는 것을 막는다. 두 값 다 이미 손에 있어 추가 조회가 없다.
- **좌표 어긋남은 표류와 가른다.** 실측 3건이 그 모양이다(`context-platform` 에서 친 `finish` 인데
  항목은 `kweiza-cc-plugins` 에 있다 — `fd-session-row-fanout`·`fd-ci-timing-baseline`·
  `fd-prescribe-unclaimed-fires-after-finish`). 후보 목록에 없는 id 의 선언은 **버리고**, 버린다는
  사실을 doc 주석에 적는다. 그것은 좌표 오류지 표류가 아니다.

타입은 **`judge` 에 선언한다**. `judge.AfterFacts` 선례 그대로 — `judge` 의 import 는 전부
`model` 하나뿐이고(`bundle.go:3-9`), `store` 에 두고 `judge` 가 받으면 `judge → store` 의존이
새로 생긴다. `store` 는 `judge` 타입을 채워 돌려준다.

### ② 판정 — 비교자는 필드만 본다

`lessCandidate`(`eligible.go:200`)와 `lessBundle`(`bundle.go:406`)은 `EligibleInput` 을 **못 본다**.
그래서 값을 구조체에 찍고 비교자는 그 필드만 읽는다.

```go
// EligibleInput
CloseDeclarations     map[string]CloseDeclaration
CloseDeclarationsRead bool   // false 면 이 축이 아예 안 돈다

// Bundle
CloseDeclared       bool     // zero(false) = 강등 안 함 — 배선 안 된 호출이 큐를 뒤집지 않는다
CloseDeclaredDetail string
```

**누가 어디에 찍나.** `EligibleBundle` 이 `bundleAround` 뒤, 기아 판정과 **같은 자리**에서
`in.CloseDeclarations[lead.Item.ID]` 를 보고 `Bundle` 의 두 필드를 찍는다 —
`CloseDeclarationsRead == false` 면 그 블록을 통째로 건너뛴다(`Now.IsZero()` 가 기아 판정을
건너뛰는 것과 같은 모양, `eligible.go:91-93`). 화면용 값(`PickResult`·`BundleMember`)은
`service` 가 같은 맵에서 id 로 조회해 찍는다 — `Candidate` 에는 필드를 더하지 않는다
(`lessCandidate` 가 이 축을 안 보므로 `judge` 안에서 그 값이 필요한 자리가 없다).

**nil 맵을 "안 읽음"으로 쓰지 않는다.** 같은 구조체의 `HeldResources`(`eligible.go:88`)가
"비어 있으면 아무도 안 쥠"이라는 정반대 계약이고, 한 구조체에 nil 뜻이 반대인 맵 둘이
나란히 서게 된다. 그리고 Go 의 nil 맵 조회는 zero 를 내므로 비교자 안에서 읽으면 nil 과 빈 맵이
바이트 단위로 같은 출력이 되어 **순수 함수 시험이 두 상태를 가를 관측점이 하나도 없다.**
가장 가까운 선례가 `SiblingIndex` 이고, `service/pick.go:167-177` 이 `(값, bool)` 두 반환값을
골랐다 — 같은 모양을 쓴다.

**정렬 축의 자리는 하나뿐이다.**

```go
if a.Dependents != b.Dependents { return a.Dependents > b.Dependents }
if a.Starved != b.Starved       { return a.Starved }
if a.CloseDeclared != b.CloseDeclared { return !a.CloseDeclared }   // ← 여기
if a.Starved { ... Oldest ... }   // 굶김 전용 갈래는 무조건 return 한다
```

`413-418` 의 굶김 전용 갈래는 무조건 `return` 하므로 **그 뒤에 놓인 축은 굶은 묶음끼리
영영 안 읽힌다.** 지금 큐는 열린 30건 중 26건이 24h 를 넘겼고 사고 항목도 회수 시점에
42시간이었다 — 뒤에 두면 강등이 겨냥한 인구 전체에 대해 무동작이 된다.

`Starved` **아래**에 두는 것이 루프를 끊는다. 강등된 항목도 굶는 순간 안 굶은 묶음 전부를
이기므로 탈출구가 구조적으로 보장된다. 조정 상수를 하나도 안 들인다(`bundle.go:378` 이
"조정할 상수가 하나도 없다"를 표방하는 함수다).

`lessCandidate` 에는 **넣지 않는다.** `judge.Eligible` 은 저장소 전체에서 호출자가 0건이고
제품이 부르는 것은 `EligibleBundle` 하나뿐이라, 거기 넣은 축은 묶음 구성원의 표시 순서만
바꾼다. 그 함수의 처분은 큐의 `fd-eligible-dead-function-disposal` 이 정할 일이다.

**사유를 같은 커밋에서 늘린다.** `Bundle.Reason` 은 `bundle.go:341` 의 `Sprintf` 와 `249` 의
기아 덧붙임이 유일한 조립부다. 축을 더하고 문장을 안 늘리면 "왜 하필 이게 1순위인가"에
답 못 하는 추천이 된다. 기아 문구와 같은 형식으로 붙인다:

```
· ★종료 선언 1건(2026-08-04 23:54, 세션 01KZ785T…, mode=done) — 이미 끝난 일일 수 있다. 연결된 판단부터 읽어라
```

사고 시점(회수 후·두 번째 `finish` 전)의 실제 값이 그것이다. 두 번째 `finish` 가 성공하면
항목이 `done` 이 되어 후보에서 빠지므로, 이 축이 2 이상을 내는 것은 같은 항목이 두 번 이상
롤백된 때뿐이다.

**원장에도 근거를 남긴다.** `RejectNotTop` 의 `Detail`(`bundle.go:271-274`)에 같은 사실을
덧붙인다. 안 남기면 이 축이 실제로 무엇을 몇 번 밀어냈는지 `pick_eval` 로 셀 수 없어,
"조용히 버리는 것이 하나도 없다"가 형식만 지켜지고 목적은 안 지켜진다.

### ③ 배선 — `derive` 에 안 넣는다

```go
func (s *Service) closeDeclarations(ctx context.Context, project string) (map[string]judge.CloseDeclaration, bool)
```

조회 실패는 ⓐ `s.log.WarnContext` ⓑ 두 번째 반환값 `false` ⓒ 그 bool 을
`EligibleInput.CloseDeclarationsRead` 와 `Bundle.Scope` 문장에 그대로 태운다.

**`derive.Failures` 에 넣지 않는다.** `pick.go:720-725` 가 같은 함수에서 같은 판단을 이미
반대로 내렸다 — "못 읽은 사실은 derive 에 안 넣는다(derive 에 넣으면 `FreshnessOf` 가 git 축을
낡음으로 접는다). 대신 로그에 남기고 두 번째 반환값으로 호출부에 그대로 넘긴다."
`pick` 은 git 읽기가 실제로 도는 경로라 예외가 안 선다.

### ④ 표면 — pick 응답

**선두와 구성원 양쪽에 같은 모양으로 싣는다.** `renderBundle`(`render.go:1159`)은 `BundleInfo`
하나만 받고 `Members` 는 정의상 선두 제외라 **선두를 모른다**. 그런데 이 사고의 항목은
정확히 **선두**다 — 구성원 자리에만 심으면 사고를 낳은 그 항목에 대해 응답이 침묵한다.

`PathCheck` 의 규약을 그대로 복제한다(`service/pick.go:73-82`):

- `PickResult.CloseDeclared *judge.CloseDeclaration` — 선두용. **포인터다**: nil 은
  "이 응답은 그 축을 안 읽었다". 구서버+신 클라이언트, 오프라인 캐시가 그 상태를 실제로 만든다.
- `BundleMember.CloseDeclared *judge.CloseDeclaration` — 구성원용.
- `Link.Detail`·`Rejection.Detail`·`BundleInfo.Reason` 은 **건드리지 않는다** — 각자 이미 다른
  질문에 답한다.

렌더는 `renderPathCheck` 과 쌍둥이로 짓는다. 선두는 `render.go:989` 의 `renderPathCheck` 뒤에
0칸, 구성원은 `render.go:1234` 옆에 4칸. **구성원 줄은 `render.go:1215` 의 `continue` 보다
위에 쓴다** — 안 그러면 못 집은 구성원에게 영영 안 나온다.

접두는 새로 판다(`종료 선언:`). 다음 문자열은 재사용 금지 — 개수를 세거나 절을 나누는 시험이
물려 있다: `경로 실재: ` · `브랜치: ` · `fd move ` · `겹침 판정 범위:` · `안 들어갔다` ·
`겹침을 관측하지 않았다` · `묶을 게 없어 단독이다`. 그리고 새 줄이
`"  " + markClaimed/markRejected/markProposed + " "` 로 시작하면 안 된다(구성원 수 카운트와
절 분할이 그 접두 하나에 걸려 있다).

### ⑤ 표면 — web

**사실은 `ItemRow` 에 하나로 싣는다.** `page.go:375` 가 이미 `p.Queue.claimTargets(board)` 로
회수 폼의 원천을 큐 `Items` 로 삼으므로, `ItemRow` 에 담으면 두 표면이 배선 없이 같이 얻는다.
두 자리에서 따로 계산하면 같은 이름으로 다른 사실을 내는 어긋남이 재현된다.

두 시점을 표로 적는다.

| | 시점 A (롤백 직후, `claimed`·점유자 있음) | 시점 B (회수 후, `open`·무점유) |
|---|---|---|
| 회수 폼 `<option>` | **○** (`claimHolders` 가 `released_at IS NULL` 로 잡는다) | ✕ (`claimTargets` 가 `Holder==""` 를 건너뛴다) |
| 큐 표 | ○ | ○ |
| 폐기 폼 | ○ | ○ |

- **회수 폼** = 되돌릴 수 없는 행위를 저지르는 마지막 한 줄. `dashboard.gohtml:149` 에 붙인다.
  지금 그 줄엔 항목 제목조차 없으므로 제목도 함께 싣는다.
- **큐 표** = 사고 시점까지 살아남는 기록. 회수 폼의 줄은 회수하는 순간 사라지는데 사고는
  그 다음에 난다(`open` 이 된 항목을 `pick` 이 나이순 1순위로 낸다). `dashboard.gohtml:194` 의
  상태 칸에 배지로 붙인다.
- **폐기 폼**도 같은 문장을 얻는다. `Queue.Targets` 를 `[]string` 에서 `ItemRow` 참조로 바꾼다 —
  시점 B 의 올바른 처분 경로가 폐기인데 지금 그 `<select>` 는 id 만 낸다(`dashboard.gohtml:231`).

**새 폼을 만들지 않는다.** `render_test.go:398-410` 이 `<form>` 4개·`method="post"` 3개를
잠근다. 기존 `<option>` 텍스트와 표 열만 늘린다.

**못 읽음을 0으로 접지 않는다.** 원장 조회가 실패하면 "종료 선언 없음"이 아니라 "이 축을
못 읽었다"를 화면이 말한다. 같은 층의 선례가 `-1` 센티널이다(`page.go:657-662` 의 `r.Dependents`).

### ⑥ 별개 결함 — 폐기가 `claim` 행을 안 닫는다

`web/actions.go:237` 은 선점 검사 없이 `SetItemState(..., ItemDropped, ...)` 만 친다.
`JudgeDropTarget`(`actions.go:125-134`)이 거절하는 것은 `done`/`dropped` 뿐이라 **`claimed` 항목이
통과하고, `claim` 행은 `released_at = NULL` 로 남는다.** 그러면 그 세션은 닫힌 항목의 선점을
영영 쥐고 있고, `schema.sql` 에 만료가 없으므로 사람이 강제로 풀 때까지 안 풀린다.

같은 tx 안에서 `Tx.ForceReleaseClaim` 을 **먼저** 부른다. 그 함수는 `UPDATE item SET state='open'
... AND state='claimed'` 를 치므로(`store/item.go:783`) 반드시 `SetItemState(dropped)` **앞**이어야
한다. 살아 있는 선점이 없으면 `NFLiveClaim` 을 올리는데, 그것은 "선점이 원래 없었다"는
정상 갈래이므로 `errors.Is(err, store.ErrNotFound)` 로 흡수한다.

사유는 폐기 사유를 그대로 나른다 — `ForceReleaseClaim` 이 빈 사유를 거절하기 때문이고,
그 사유가 `claim.force_reason` 에 남아 나중에 되짚을 수 있다.

## 5. 안 하는 것 · 기각한 것

- **선점 없이 닫는 경로를 열지 않는다(항목 본문의 ①).** 열면 남의 일을 닫는 문이 생기고,
  본문이 "그 대가를 재야 한다"고 남겨 둔 자리다. 본문 스스로 "둘 중 하나만 한다면 ②"라고
  못박았다.
- **판단 본문의 문자열 매칭으로 "끝났다"를 읽지 않는다.** 항목 본문의 경고 그대로다 —
  판단은 자유 서술이고 오탐이 침묵보다 나쁘다.
- **`item.finish_followups_missing` 을 같은 축에 합치지 않는다.** 실측 24건 전수가 20~181초 안에
  같은 세션·같은 항목으로 재호출돼 성공했고 24개 항목 전부 `done` 이다. 표류 0건.
  그것은 관문이 제 일을 한 기록이지 사고가 아니다 — 합치면 관문의 성공률 100%가
  사고 발생률로 뒤집혀 보고된다.
- **`item.finish.fail` 의 `error` 문자열을 파싱하지 않는다.** 실측 4건 전부 항목 id 가 문구
  안에 있지만(`"항목 kweiza-cc-plugins/fd-windows-path-overlap-silent 선점 거절: …"`),
  정규식으로 긁는 것은 문구가 바뀌는 순간 조용히 죽는다. payload 에 `item` 을 싣는 것이
  정공법이고 그것은 후속으로 세운다(§9).
- **`bytes` 로 임계값을 세우지 않는다.** 판단 본문이 크면 "완성된 핸드오프"라는 신호이긴 하나
  (실측 10300·5421·5060·3032), 임계값은 조정 상수를 들이는 일이다. 대신 사유 문구에 수를
  그대로 내고 사람이 판단한다.

## 6. 이 축이 못 보는 것

정직하게 적는다. 안 적으면 다음 세션이 이 축을 완전한 것으로 믿는다.

- **오늘의 큐를 하나도 안 고친다.** 실측: `item.finish` 383건 중 항목이 `open`/`claimed` 인 것은
  **0건**이다. 살아 있는 선점 8건(2026-08-09 00:04 기준) 중 `item.finish` 이력이 있는 것도
  **0건**이다 — 그 선점들은 `finish` 를 부른 적조차 없이 세션이 사라진 것이다.
  이 설계가 막는 것은 **재발**이다. 세션 소실로 인한 표류는 회수 지점(§4-⑤)과 선점 생존
  판정이 답이고, 후자는 이 항목 밖이다.
- **`BeginTx` 실패는 못 본다.** `store/store.go:340-343` 은 클로저를 아예 안 부르므로
  `item.finish` 가 안 남는다. 커밋 실패는 남는다.
- **원장에 안 써진 마무리는 영구히 "0건"으로 보인다.** `flushDeferred` 는 트랜잭션이 물고 있던
  `ctx` 를 그대로 쓰고(`store/store.go:366`), `LogEvent` 는 쓰기 실패를 WARN 으로만 삼킨다
  (`event.go:28-34`). 클라이언트가 끊겨 `ctx` 가 취소되면 행이 안 써진다. **이 수는 정확한
  수가 아니라 하한이다** — doc 주석과 사유 문구가 그렇게 말해야 한다.
- **tx 진입 전 거절은 안 센다.** 그것은 결함이 아니라 정확성이다 — tx 전에는 아무것도 안 썼고
  항목이 표류하지 않는다. 다만 그 건수를 지금 아무도 모른다(§9).

## 7. 시험

- **정렬 축은 `lessBundle` 을 직접 부른다.** `EligibleBundle` 을 통하면 `bundles` 가 이미
  `lessCandidate` 로 정렬된 `fit` 에서 만들어져서 축을 `return false` 로 지워도 통과한다 —
  `bundle_test.go:370-378` 이 그 함정을 명시적으로 기록해 뒀다. `407-408`·`440-445`·`463-469` 의
  방식(손으로 만든 `Bundle` 둘 + 나머지 축을 전부 반대편으로 몰아 축 격리)을 복제한다.
  **굶은 묶음끼리도 축이 읽히는 것**을 별도로 단정한다 — 그것이 이 설계에서 가장 미끄러운 자리다.
- **배선 시험을 따로 둔다.** `pick_wiring_test.go:13-20` 이 이유를 적어 뒀다 — "judge 시험은
  `EligibleInput` 을 손으로 조립하고 render 시험은 `PickResult` 를 손으로 조립한다. service 가
  그 구조체를 실제로 무엇으로 채우는지는 어느 쪽도 원리적으로 못 잰다. 이 저장소가 이미
  대가를 치렀다." `EligibleBundle` 이 필드를 채우는지, `pick.go:831` 의 `BundleInfo` 조립이
  그것을 wire 까지 나르는지 각각 잠근다.
- **저장층은 선점 없는 `finish` 를 실제로 실패시킨다.** 이벤트를 손으로 심으면
  "롤백돼도 흘러간다"는 전제 자체를 안 밟는다.
- **회귀는 손으로 심은 이벤트로 밟는다.** 실물 원장에는 지금 `open`+`item.finish` 조합이
  0건이라 그것으로는 못 밟는다. 이 사실을 시험 주석에 적는다.
- **렌더는 절 단위로 단정한다.** `render_test.go:1372` 의 `bundleMemberSegment` 를 쓰고,
  선두 것이 구성원에 복사되는 변이가 죽도록 값을 서로 다르게 깐다. **선두 갈래는 별도 단정이
  필요하다** — 이 사고의 주인공이 선두다.
- **web 은 두 시점 모두**를 단정한다: 롤백 직후(`claimed`) 회수 폼에 표기가 뜨는 것,
  회수 후(`open`) 큐 표에 그대로 남는 것.
- **폐기 뒤 `claim` 행이 닫힌 것**을 단정한다(`released_at IS NOT NULL`).

## 8. 함께 고칠 산문

시험이 문자열 존재만 보므로 빨간불 없이 표류하는 자리들이다.

- `DESIGN.md:288-293` — "정렬 키는 넷이고 **조정할 상수가 0개다**". 기아 축(+`StarvationAge`)으로
  이미 한 단계 낡았고 이 축으로 두 단계가 된다.
- `DESIGN.md:369-371` §4 "근거를 다섯 축으로" · `web/page.go:88` 이 그 문구를 인용한다 —
  회수 근거에 여섯째가 붙는다.
- `judge/bundle.go:383-405` `lessBundle` 축 주석 · `bundle.go:12-16` ("기존 판정을 하나도
  안 고친다").
- `judge/bundle.go:178-181` — "24h 는 p90 바깥이라 정상 작업이 안 걸린다". **지금 큐에서 거짓이다**
  (열린 30건 중 26건이 굶었다). 기아는 예외가 아니라 현재 기본값이다. 이 문장을 실측으로
  정정한다.

## 9. 후속 항목

- `service/service.go:241` `logFail` 의 payload 에 `item`·`mode`·`cause` 를 싣는다.
  그러면 롤백을 **추론이 아니라 관측**으로 읽고, 선점 표류로 죽은 시도와 다른 이유로 죽은
  시도를 가를 수 있다.
- `store/store.go:366` `flushDeferred` 의 `ctx` 를 트랜잭션 `ctx` 에서 뗀다
  (`context.WithoutCancel` 선례가 `api/api.go:406`·`453` 에 있다).
- `store/item.go:490` 의 `open`/`claimed` UPDATE 에 `AND state IN ('open','claimed')` 가드.
  이 설계 전체가 "부활 경로가 없다"에 기대는데 그것을 지탱하는 것이 지금 `JudgeClaim` 하나뿐이다
  — 같은 파일이 스스로 "방어는 두 겹"이라고 적어 놓고 이 자리는 한 겹이다.
- `service/finish.go` 의 tx 진입 전 거절 일곱 자리에 `item.finish.refused` 를 남긴다.
  표류 탐지에 **먹이지 않는다** — 목적은 재현율의 분모를 처음으로 갖는 것이다.
- `QueueReproduction`(`store/event.go:178-190`)이 롤백된 시도의 `count` 를 R 의 분자에 넣는다.
  `finish.go:235-237` 이 "만들지도 않은 항목을 R 의 분자로 더하면 §10 이 조용히 거짓이 된다"고
  이미 못박은 자리인데 롤백 갈래는 아직 안 막혔다.
