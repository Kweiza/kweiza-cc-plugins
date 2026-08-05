# fd-pick-bundle — 설계

`pick` 이 큐의 1순위 **하나**가 아니라 **함께 갈 항목들**을 묶어 낸다.
묶을 것이 없으면 지금과 똑같이 단독으로 낸다.

목적은 하나다: 한 워크트리에서 한 번에 끝낼 수 있는 일을 세 번에 나눠 하지 않는 것.

---

## 0. 이 문서가 뒤집은 것 — 실측이 초안의 두 축을 죽였다

초안은 이랬다: 묶는 축 셋(형제·경로 겹침·같은 선행)을 대등하게 두고,
묶음 점수를 **의존자 수 합**으로 매겨 가장 높은 것을 추천한다.

**실물 큐에 걸어 보니 둘 다 틀렸다.** 아래 §0.1·§0.2 가 그 둘이고,
§2 의 결합 규칙과 정렬 키는 전부 이 두 관측의 결론이다.

측정 시점 · 대상: 2026-08-05 00:2x UTC, `kweiza-cc-plugins` 의 **열린 항목 16건**.
(이 문서를 쓰는 동안 7 → 15 → 16 으로 늘었다. 동시 세션 19건이 계속 항목을 등록하고 있다.)

### 0.1 뒤집기 1 — 경로 겹침은 **단독으로는 신호가 아니다**

`judge.PathsOverlap` 은 **조상 디렉토리도 겹침으로 센다**(`paths.go:27` — "두 경로가 같거나,
한쪽이 다른 쪽의 조상 디렉토리이면 겹친다"). 그 규칙 자체는 옳다 — 남의 세션과 부딪히는지를
알리는 용도로는 넓게 잡는 쪽이 맞다. 그런데 **"함께 할 일인가"에 그대로 쓰면 무너진다.**

초안 규칙을 열린 16건에 건 결과:

```
● fd-judgment-backup-missing  (묶음 10건)          ← 16건 중 10건을 혼자 끌어온다
    + fd-itempath-move-not-verified-end-to-end  [paths: cmd/fd ~ cmd/fd/wire_test.go]
    + fd-replay-concurrency-premise             [paths: cmd/fd ~ cmd/fd/client.go]
    + fd-banner-legacy-guard                    [paths: cmd/fd ~ cmd/fd/hook.go]
    + fd-vcs-stamp-blind-to-worktree            [paths: cmd/fd ~ cmd/fd/setup.go]
    + fd-session-lookup-without-upsert          [paths: cmd/fd ~ cmd/fd/hook.go]
    + fd-worktree-split-cards-backfill          [paths: internal/store ~ internal/store/session.go]
    + fd-design-table-count-confirm             [paths: DESIGN.md ~ DESIGN.md]
    + fd-item-premise-signal-table-has-no-history [paths: DESIGN.md ~ DESIGN.md]
    + fd-footprints-endpoint-has-no-client      [paths: DESIGN.md ~ DESIGN.md]

단독(이웃 0) 선두 1건 / 전체 15건       ← 큐의 거의 전부가 서로 묶인다
```

> 이 판은 열린 항목이 **15건**이던 시점이고, §2.2 의 교정 판은 **16건**이다.
> 두 수를 맞추지 않고 그대로 둔다 — 측정 사이에 다른 세션이 항목을 하나 더 등록했고,
> 그것이 이 큐의 정상 속도다. 수를 사후에 맞추면 관측이 아니라 서술이 된다.

원인이 둘이고 성질이 다르다:

1. **넓은 토큰.** `fd-judgment-backup-missing` 은 경로를 `plugins/flightdeck/server/cmd/fd`
   와 `plugins/flightdeck/server/internal/store` 로, 즉 **디렉토리 통째**로 선언했다.
   그 항목이 실제로 그 디렉토리 전부를 만질 예정이라 선언 자체는 정직하다.
   조상 규칙과 만나면 그 디렉토리 아래 파일을 하나라도 건드리는 모든 항목이 이웃이 된다.
2. **모두가 만지는 파일.** `plugins/flightdeck/DESIGN.md` 는 이 레포에서 사실상 모든 항목이
   한 줄씩 고치는 파일이다. 실측 순간에도 살아 있는 세션 다섯이 동시에 잡고 있었고,
   그 다섯이 서로에게 "나는 297행만 / 320행만 / 398행만 만진다"고 알리는 `ask` 판단을
   주고받는 중이었다. **그 파일을 공유한다는 사실은 함께 할 이유가 못 된다.**

여기서 나오는 결론은 "겹침 축을 버린다"가 아니다. 실측으로 가장 좋은 묶음
(`fd-banner-legacy-guard` ↔ `fd-replay-concurrency-premise`)은 경로 축에서도 걸리고,
그 걸림은 **정확히 같은 파일**(`cmd/fd/outbox.go`)이며, 그 쌍은 형제이기도 하고 선행도 같다.

→ **경로는 정확히 같을 때만 세고, 그것만으로는 절대 안 묶는다.** 이미 다른 축으로 이어진
쌍을 **보강**할 뿐이다. §2.2.

### 0.2 뒤집기 2 — `의존자 수 합`은 이 큐에서 **죽어 있다**

초안의 정렬 1차 키는 묶음 구성원의 `Dependents` 합이었다. 기존 `lessCandidate` 의 1차 키를
그대로 합산한 것이다(`eligible.go:178`).

**열린 16건 전부 의존자 0이다.** `item_after` 에 실제로 행이 있는 항목은 여럿이지만
그 대부분이 `dep_sha`(랜딩된 커밋)를 가리키고, 항목이 항목을 가리키는 `dep_item` 은 드물다.
즉 `Dependents`(= `dep_item` 역인덱스)는 이 큐에서 상수 0이다.

1차 키가 전부 동점이면 2차 키가 실질 1차 키가 된다. 초안의 2차 키는 **최고령**이었고,
열린 항목 중 가장 오래된 것은 `fd-judgment-backup-missing`(2026-08-04T09:15:01Z)인데
교정된 규칙에서 그것은 **단독**이다.

> 그대로 두면 추천은 항상 그 단독 항목이고, **묶음 기능이 영원히 발화하지 않는다.**

→ **묶음 크기를 의존자 합 바로 다음 키로 둔다.** §2.4.
"한 번에 더 많이 푸는 쪽이 이긴다"는 의존자 합과 같은 뜻이므로 새 상수가 아니다.

### 0.3 확인된 것 — 손 묶음은 **이미 일어나고 있다**

이 기능이 없는 상태에서 세션들이 이미 손으로 묶고 있다. 실측 순간의 `ask` 판단 실물:

```
[ask] 01KZ7DMG·01KZ7F6Y·DESIGN.md 잡은 세션들에게 — 상수 근거 2건을 묶었다. 값은 안 바꾼다, 주석만 바꾼다
[ask] cmd/fd 를 잡은 세션들에게 — doctor 두 축을 묶었다. config.go·service/doctor.go 는 안 만진다
[ask] hook.go·mcpsrv.go 잡은 세션들에게 — 세션 동일성 축 2건을 묶었다. hook.go 는 주석 3줄뿐
```

세 세션이 각각 2건씩 묶어 한 브랜치에서 처리하고 있었다. **행동은 이미 있고 도구가 그것을
모른다.** 그래서 그 묶음이 큐에 안 남고, 겹침 판정도 그것을 모르며, 다음 세션은 같은 판단을
처음부터 다시 한다.

다중 선점은 **데이터 모델이 이미 허용한다** — `claim` 의 기본키가 `(project, item_id)` 라
한 세션이 여러 항목을 쥐는 데 제약이 없고, 실측 순간 세션 `01KZ71DM` 이 3건을 쥐고 있었다.
없는 것은 저장 구조가 아니라 **"무엇을 함께 집을지"에 대한 판정과 묶음이 쓸 브랜치 하나**다.

### 0.4 그리고 못 잡는 것 하나를 알고 간다

위 손 묶음 중 첫째(`fd-live-window-baseline` + `fd-prescribe-threshold-baseline`,
"상수 근거 2건")를 **이 설계의 세 축은 잡지 못한다.** 실측으로 확인했다:

| 축 | 관측 |
|---|---|
| 형제 | 각각 `01KZ7JYEQ0…` 와 `01KZ7JZQN9…` 에 매달렸다 — **공유 판단 없음** |
| 경로 | `internal/service/service.go` ↔ `internal/judge/prescribe.go` — 겹침 없음 |
| 선행 | 둘 다 `item_after` 행 0건 — 공유 선행 없음 |

둘을 잇는 것은 **"근거 없는 상수를 실측으로 대체한다"는 의도의 동일성**뿐이고, 그것은
사람이 본문을 읽어야 나오는 사실이다. 파생 가능한 축에 없다.

**이것을 메우지 않는다.** 메우려면 본문 유사도나 꼬리표 기반 판정이 필요한데,
꼬리표는 설계 §5 가 **어떤 배제 판정에도 안 쓴다**고 못박아 둔 축이고, 본문 유사도는
정밀도 미지수의 연구 과제다(설계 §11 의 "표류 자동 탐지기"와 같은 부류).
대신 **추천이 놓친 묶음을 사람이 만들 수 있게** `item_ids` 를 열어 둔다 — §3.4.

---

## 1. 무엇을 만드나 — 새 개념 0개

**묶음은 저장되지 않는다.** 테이블도, id 도, 상태도 없다.
`pick` 한 번의 응답 안에서만 사는 파생값이고, DB 에 남는 것은 지금과 똑같은 `claim` 행 N개다.

세션이 새로 외울 것은 **하나**다: *`pick` 이 여러 개를 줄 수 있다.*
설계 §1② ("세션이 외워야 할 개념 수를 늘리는 기능은 만들지 않는다")를 지키는 자리다.

만드는 것:

| 자리 | 무엇 |
|---|---|
| `internal/judge/bundle.go` | 순수 판정 — 두 항목이 왜 함께 갈 만한가, 어느 묶음이 1순위인가 |
| `internal/store` | `JudgmentLinksForItems` 접근자 — 인덱스는 이미 있고 접근자만 없다 |
| `internal/service/pick.go` | 형제 색인 조립 · 묶음 선점(선두 원자) |
| `internal/mcpsrv` | `pick` 에 `item_ids` 인자 · 묶음 절 렌더 |
| `store/schema.sql` + 마이그레이션 | `pick_eval.picked_with` 한 칸 |
| `skills/fd-pickup/SKILL.md` | §2·§3 을 묶음으로 |

---

## 2. 판정 — 축 셋, 결합 규칙 하나, 정렬 키 넷

### 2.1 축 셋

```go
type BundleAxis string

const (
    AxisSibling BundleAxis = "sibling" // 같은 판단에 함께 매달렸다
    AxisAfter   BundleAxis = "after"   // 같은 선행을 기다렸다 / 선행이 선두다
    AxisPaths   BundleAxis = "paths"   // 선언 경로가 정확히 같다 — 보강 전용
)
```

**형제(`sibling`)** — 판단 하나가 두 항목을 함께 가리키면 형제다. 판단 종류를 안 가린다.
실질적으로 이 관계를 만드는 것은 `finish` 뿐이다(`finish.go:148-152` 가 끝낸 항목과
`followups` 전부를 한 handoff 판단의 링크로 묶는다). `note` 는 `item_id` 를 하나만 받으므로
다중 링크를 만들 수 없다. 즉 **형제 = 한 세션이 한 자리에서 "이건 따로 빼자"고 쪼갠 집합**이다.

> 끝낸 항목도 같은 판단에 매달리지만 그것은 `done` 이라 후보에 안 들어온다.
> 후보 집합이 열린 항목과 살아 있는 선점뿐이라(`pick.go:380`) 이 축이 종료된 항목을 끌어올 수 없다.

실측 형제 묶음 크기(이 프로젝트, 열린 항목 기준): **3 · 2 · 2**.

**같은 선행(`after`)** — 갈래가 둘이고 성질이 다르다.

- *같은 문이 함께 열렸다* — 두 항목의 선행 집합이 같다. 같은 랜딩을 기다렸다가 함께 열린 일이다.
  실측: `sha:47421b4` 를 기다린 쌍 1개, `sha:f7ff0a7` 를 기다린 3건 1개.
- *선행이 선두다* — A 의 선행이 B 이고 B 가 이 묶음의 선두다. 한 브랜치에서 B→A 순서로 하면
  **랜딩을 안 기다린다.** 이 갈래는 `Eligible` 이 이미 탈락시킨 항목을 되살리므로 조건이 엄격하다 — §2.3.

**경로(`paths`)** — 두 항목이 **정확히 같은 경로 토큰**을 선언했을 때만 성립한다.
조상 디렉토리 관계는 이 축에서 세지 않는다(§0.1). 그리고 **단독으로는 묶지 않는다** — §2.2.

> `judge.PathsOverlap`·`OverlapPairs` 는 **안 건드린다.** 그 함수들의 소비자는
> "남의 세션과 부딪히는가"이고 거기서는 넓게 잡는 것이 옳다. 묶음은 다른 질문이므로
> 별도 술어(`SamePaths`)를 둔다. 같은 함수를 두 질문에 쓰면 한쪽을 고칠 때 다른 쪽이 조용히 바뀐다.

### 2.2 결합 규칙 — 경로는 보강 전용

```
링크가 성립한다 ⟺ (형제 ∨ 같은 선행)  이 참이다.
경로 일치는 링크의 근거에 덧붙지만, 그것만으로는 링크를 만들지 못한다.
```

교정된 규칙을 열린 16건에 건 결과 — **잡음이 전부 사라지고 진짜 묶음이 전부 남는다:**

```
● fd-replay-concurrency-premise (2건)
    + fd-banner-legacy-guard   [sibling + after:sha 47421b4 + paths:cmd/fd/outbox.go]   ← 3축 전부
● fd-prescribe-overlap-ack-zero (3건)
    + fd-board-size-not-window-bound            [sibling + after:item fd-live-window-baseline]
    + fd-item-premise-signal-table-has-no-history [sibling]
● fd-legacy-apply-bypasses-path-gate (3건)
    + fd-index-notation-off-by-one    [after:sha f7ff0a7 + paths:internal/service/pick.go]
    + fd-footprints-endpoint-has-no-client [after:sha f7ff0a7]
● fd-session-lookup-without-upsert (2건)
    + fd-worktree-split-cards-backfill [sibling + after:item fd-worktree-axis-asymmetry]

단독 6건: fd-judgment-backup-missing, fd-itempath-unknown-carries-no-cause,
          fd-itempath-move-not-verified-end-to-end, fd-design-table-count-confirm,
          fd-vcs-stamp-blind-to-worktree, fd-covered-by-closed-crosses-coordinates
```

`fd-judgment-backup-missing` 이 10건에서 **단독**으로 돌아왔고,
`DESIGN.md` 로 엮이던 4건이 전부 풀렸다. 조정할 상수는 하나도 없다.

### 2.3 흡수 — 선행이 선두일 때만, 그리고 `after-unmet-item` 일 때만

A 가 `Eligible` 에서 탈락했는데 묶음에 들어오는 경우는 **정확히 하나**다:

```
A 의 탈락 사유가 전부 `after-unmet-item` 이고,
A 의 **선행 전체**(충족 여부와 무관하게)가 이 선두 하나만 가리킨다.
```

★ 이 규칙은 "미충족 선행만 전부 묶음 안이면 된다"보다 **좁다.** 선행 하나가 이미
충족된 `sha:cafe` 이고 다른 하나가 미충족 `item:B-lead` 이면, 미충족분만 보면 흡수
대상이지만 이 구현은 흡수하지 **않는다** — 판정(`blockedOnlyBy`)이 그 항목의 `After`
전체를 보고, 선두가 아닌 것이 하나라도 섞이면(충족됐어도) 거른다. 넓히려면(=충족된
선행은 무시하고 미충족분만 본다) 판정 함수가 `AfterFacts` 를 받아 선행마다 충족
여부를 다시 매겨야 하는데, 그 순간 `AfterSatisfied` 의 사본이 여기 하나 더 생기고
순수 판정 표면이 넓어진다. 실측(2026-08-05, 열린 큐)에서 sha 선행과 item 선행을
동시에 가진 open 항목이 **0건**이라 이 좁힘의 비용은 지금 0이다 — **실패 안전**
(fail-safe) 쪽으로 잡아 둔다. 넓히는 것은 그 실측이 바뀌었을 때 다시 잴 결정이지,
지금 조용히 넓힐 일이 아니다(`bundle.go` 의 `bundleAround` 흡수 주석이 같은 근거를
코드 옆에 싣는다 — 코드가 출처이고 이 문서는 그걸 따른다).

`after.go:55-63` 의 아홉 코드 중 흡수 가능한 것은 `AfterUnmetItem`("선행 항목이 아직
안 끝났다 — 기다리면 풀린다") **하나뿐**이다. 나머지는 전부 흡수하면 안 된다:

| 코드 | 왜 흡수 불가 |
|---|---|
| `after-dropped-dep` | 선행이 폐기됐다 — **영영 안 풀린다.** 함께 해도 안 풀린다 |
| `after-bad-ref` | 그런 ref 가 없다 — 오타다. 함께 하는 것으로 안 고쳐진다 |
| `after-unmet-sha` / `after-failed-job` / `after-unmet-job` | 이 세션이 만들 수 없는 사실을 기다린다 |
| `after-unknown` | **조회 자체를 못 했다.** 모르는 것을 충족으로 접으면 §5 의 "키 부재를 값으로 접기"다 |
| `after-malformed` / `after-bad-state` | 스키마와 코드가 어긋난 정합성 결함이다 |

흡수된 항목은 `rejected` 에서 **빠지고**(picked 이므로), 그 사실이 링크 근거에 남는다:
`after: 선행 fd-x 를 같은 묶음이 함께 한다 — 랜딩을 안 기다린다`.
불변식은 유지된다: **모든 후보는 picked 이거나 rejected 에 최소 한 줄.**

### 2.4 정렬 — 키 넷, 상수 0개

```
① 의존자 수 합   ↓   — 이걸 풀어야 남이 움직이는 정도. 지금 큐에서는 상수 0이다(§0.2)
② 묶음 크기      ↓   — 한 번에 더 많이 푸는 쪽이 이긴다
③ 최고령 구성원  ↑   — 오래 방치된 것을 먼저
④ 선두 id        사전순 — 동점 처리. 없으면 같은 입력에 다른 답이 나온다
```

②가 없으면 묶음 기능이 발화하지 않는다(§0.2). ④가 없으면 입력 순서에 답이 의존한다 —
`lessCandidate` 가 같은 이유로 id 축을 갖고 있다(`eligible.go:174-186`).

★ **④가 실제로 브랜치 이름을 정하는 경우가 있다.** 후보 각각을 선두로 놓으므로
한 집합이 여러 번 나올 수 있다 — 실측의 `{board-size, item-premise, prescribe-overlap}` 은
셋 다 선두가 될 수 있고 크기도 최고령도 같다(생성 시각이 마이크로초까지 동일한 형제다).
그 셋을 가르는 것은 ④뿐이고, 그래서 브랜치는 `fd-board-size-not-window-bound` 가 된다.

**이것을 감추지 않는다.** `Bundle.Reason` 에 네 키의 실제 값을 그대로 실어
"왜 이것이 선두인가"가 화면에서 읽히게 한다. 감추면 "왜 하필 이 브랜치 이름인가"에
답할 수 없고, 답 못 하는 자동 선택은 두 번째 세션부터 무시된다.

구성원의 내부 순서는 기존 `lessCandidate` 로 정렬한다. 재현 가능해야 응답이 재출력이 된다.

---

## 3. 구조 — 기존 경계 그대로

### 3.1 `internal/judge/bundle.go` — 순수 판정

```go
// SiblingIndex 는 항목 → 그 항목에 걸린 판단 id 집합이다.
// 조립은 service 가 하고 판정은 여기서 한다 — 이 패키지에 I/O 는 없다.
type SiblingIndex map[string]map[string]bool

// Link 는 선두와 이웃 하나 사이의 관계 **전부**다.
// 축을 뭉개지 않는다 — 여러 축이 맞으면 여러 개가 들어간다.
// 뭉개면 "3축 전부 맞는 쌍"과 "형제이기만 한 쌍"이 화면에서 같아진다.
type Link struct {
    Item   string       // 이웃 항목 id
    Axes   []BundleAxis // 고정 순서: sibling → after → paths
    Detail string       // 무엇이 근거인가 — 판단 id · 선행 좌표 · 겹친 경로
}

// Bundle 은 pick 한 번이 제안하는 집합이다. **저장되지 않는다.**
type Bundle struct {
    Lead       Candidate
    Members    []Candidate // 선두 제외. lessCandidate 로 정렬
    Links      []Link      // Members 와 같은 순서
    Dependents int         // 합산
    Oldest     time.Time   // 가장 오래된 구성원의 CreatedAt
}

// SamePaths 는 두 경로 집합에서 **정확히 같은** 토큰을 낸다. 순수 함수다.
// PathsOverlap 과 일부러 다른 함수다 — 소비자의 질문이 다르다(§2.1).
func SamePaths(a, b []string) []string

// LinkOf 는 선두와 이웃 하나의 관계를 낸다. 무관하면 nil.
func LinkOf(lead, other Candidate, sib SiblingIndex) *Link

// EligibleBundle 은 Eligible 위에 얹는다.
// 적격 후보 **각각을 선두로** 놓고 방사형으로 이웃을 붙인 뒤 §2.4 로 정렬해 1순위를 낸다.
// 전이하지 않는다 — 이웃의 이웃은 안 들어온다.
// Eligible 이 낸 탈락 사유를 그대로 나르되, §2.3 으로 흡수된 항목의 줄은 뺀다.
func EligibleBundle(in EligibleInput, sib SiblingIndex) (picked *Bundle, rejected []model.Rejection)
```

`Eligible` 은 **안 바꾼다.** 그대로 두고 그 위에 얹는다 — 시험이 단일 추천 규칙을
독립으로 계속 부를 수 있어야 하고, 묶음 판정이 그 규칙의 사본을 만들면 안 된다.

비용: 후보 n 에 대해 O(n²) 링크 계산. n=16 에서 240회, 각 회가 경로 토큰 곱(실측 최대 5×4).
큐가 200건이 돼도 4만 회로 밀리초 단위다. 상한을 두지 않는다 — 상한은 근거 없는 상수이고,
자르는 순간 "무엇이 잘렸나"를 따로 내야 한다.

### 3.2 `internal/store` — 접근자 하나

```go
// JudgmentLinksForItems 는 항목 id 들에 걸린 판단 링크를 한 번에 읽는다.
// 항목 id → 판단 id 목록.
func (s *Store) JudgmentLinksForItems(ctx context.Context, project string, itemIDs []string) (map[string][]string, error)
```

`judgment_link_by_target` 인덱스는 이미 있고 **접근자만 없었다**(`pick.go:520-522` 가
그 사실을 주석으로 적어 두고 종류 9개를 훑는 방식으로 우회하고 있다).

**`linkedJudgments` 도 이 접근자로 옮긴다.** 지금 방식은 항목 하나에 질의 9회이고,
묶음 N건이면 N×9 회가 된다 — 이 기능이 만든 비용이므로 같이 고친다.
옮긴 뒤에도 결과의 정렬(최신 먼저, 동점이면 id 역순)은 그대로 둔다.

### 3.3 `internal/service/pick.go`

- **추천**: 후보 목록으로 형제 색인을 만들고 → `judge.EligibleBundle` → `PickResult.Bundle` 에 싣는다.
- **선점**: `pickBundle(itemIDs []string)`. `itemIDs[0]` 이 선두다.
- `Branch`·`Setup` 은 **선두 id 기준으로 지금과 동일**. 나머지는 그 워크트리에서 함께 간다.
  불변식은 "항목 id 가 곧 브랜치다"에서 **"묶음 선두 id 가 브랜치다"**로 완화된다.
  단독은 원소 1개짜리 묶음이므로 옛 문장의 상위집합이다.
- `Overlaps`(남의 세션과의 겹침)는 **묶음 전체 경로의 합집합**으로 한 번 계산한다.
  겹침은 "남과 부딪히나"라 묶음 단위가 옳다. 렌더가 그 사실을 명시한다(§4).
- `PathCheck`(경로 실재)는 **항목마다** 낸다. 합치면 `fd move <id> --project X` 줄이
  엉뚱한 id 를 가리킨다 — 그 줄은 이 기능이 내는 유일한 행동 지시다.
- `QueueOpen` 은 지금처럼 **모든 쓰기가 끝난 뒤에** 센다(`pick.go:185-195`).
  묶음이면 뺄 것이 N개일 뿐 규율은 같다.

#### `PickResult` 에 붙는 필드는 **포인터**다

```go
type PickResult struct {
    // ... 기존 그대로. Item 은 여전히 **선두**다(구 클라이언트가 계속 읽는다)
    Bundle *BundleInfo `json:"bundle,omitempty"`
}

type BundleInfo struct {
    Members []BundleMember `json:"members"` // 선두 제외. 0건이면 "묶을 게 없어 단독이다"
    Reason  string         `json:"reason"`  // 왜 이 묶음이 1순위인가 — 네 키의 실제 값
    Scope   string         `json:"scope"`   // 무엇을 이웃 후보로 봤나
}

type BundleMember struct {
    Item      model.Item              `json:"item"`
    Link      judge.Link              `json:"link"`                 // 왜 선두와 묶였나
    PathCheck *judge.ItemPathVerdict  `json:"path_check,omitempty"`
    Notes     []model.Judgment        `json:"notes,omitempty"`      // 집었을 때만 전문
    Claimed   bool                    `json:"claimed"`              // 이 호출이 실제로 집었나
    Rejection *model.Rejection        `json:"rejection,omitempty"`  // 못 집었으면 사유
}
```

★ **`Bundle` 이 포인터인 이유는 `QueueOpen`·`PathCheck` 과 같다.**
슬라이스만 두면 `nil` 이 두 가지를 뜻하게 된다 — "묶을 게 없다"와 "이 응답은 그 축을 안 읽었다".
후자는 실제로 난다: 서버는 독립 컨테이너인데 플러그인은 자동 갱신되고(구서버 + 신 클라이언트),
오프라인 `fd next` 는 이 필드가 생기기 전에 굳은 디스크 캐시를 그대로 재생한다.
그 상태에서 값 타입은 **"묶을 게 하나도 없다"를 단정한다** — 관측한 적 없는 사실을.
`SkewBanner` 는 `api_version` 문자열만 보므로 필드 추가로는 안 뜬다.

`nil` = 이 응답은 묶음 축을 안 읽었다 · 구성원 0건 = 묶을 게 없어 단독이다.

### 3.4 `internal/mcpsrv` — 인자 하나, 절 하나

```go
{
    Name: "pick",
    Description: "인자 없으면 함께 갈 항목까지 묶어 추천하고 탈락 사유 전부. item_ids 를 주면 선점한다.",
    InputSchema: obj(map[string]any{
        "item_id":  str("집을 항목 id. 없으면 추천만 하고 선점하지 않는다"),
        "item_ids": strArr("함께 집을 항목 id 들. 첫째가 선두이고 그 id 가 브랜치가 된다"),
        "steal_reason": str(...),  // 그대로
    }),
}
```

**`item_id` 와 `item_ids` 가 동시에 오면 거절한다.** 합치거나 한쪽을 우선하면
무엇을 집었는지가 흐려지고, 그것이 이 도구가 지키려는 것 자체다.
`RenderRefusal` 로 사유와 처방(둘 중 하나만 써라)을 그 자리에서 낸다.

**도구 수는 6개 그대로다.** 새 도구를 만들지 않는다 — 세션 시작 컨텍스트 예산(설계 §6).

---

## 4. 문구 — 실물 그대로

추천(선점 전):

```
pick · 추천 묶음 3건 — **아직 선점하지 않았다**
사유: 의존자 합 0 · 묶음 3건 · 최고령 2026-08-04 23:50 · 선두 id 사전순으로 갈렸다(동점 3).
      후보 16건 중 1순위다. 집으려면 item_ids 에 아래 순서 그대로 주고 다시 불러라
범위: 후보 = 열린 항목 16건 + 살아 있는 세션이 쥔 항목 0건. 살아 있지 않은 세션이 쥔 항목은 후보에 없다
큐 열림 16건

▸ fd-board-size-not-window-bound — 보드 크기가 창에 안 묶인다 [open]     ← 선두 · 브랜치가 된다
경로: plugins/flightdeck/server/internal/service/board.go
경로 실재: 선언 경로 1개가 전부 이 프로젝트에 있다
본문:
  …

  + fd-prescribe-overlap-ack-zero — 겹침 처방의 확인율이 0이다 [open]
    묶은 근거: sibling(판단 01KZ7JYEQ0…) + after(선행 item:fd-live-window-baseline 이 같다)
    경로: plugins/flightdeck/server/internal/judge/prescribe.go
    경로 실재: 선언 경로 1개가 전부 이 프로젝트에 있다

  + fd-item-premise-signal-table-has-no-history — 항목의 전제가 signal 표에 없다 [open]
    묶은 근거: sibling(판단 01KZ7JYEQ0…)
    경로: plugins/flightdeck/DESIGN.md
    경로 실재: 선언 경로 1개가 전부 이 프로젝트에 있다

브랜치: fd-board-size-not-window-bound   ← 묶음 선두의 id 다. 셋을 이 워크트리에서 함께 한다
워크트리 준비:
  cd '/home/aaron/cdo-dev/kweiza-cc-plugins'
  git worktree add '.flightdeck/worktrees/fd-board-size-not-window-bound' -b fd-board-size-not-window-bound 'main'
  cd '/home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-board-size-not-window-bound'

겹침: 묶음 3건의 경로 전부를 합쳐서 봤다 — 살아 있는 세션 어느 것과도 안 겹친다
```

선점 뒤 일부가 막혔을 때:

```
pick · 선점했다 — 묶음 3건 중 2건
사유: 선두 fd-board-size-not-window-bound 를 선점했다. 구성원 1건은 못 집었다(사유 아래)
큐 열림 14건

  + fd-prescribe-overlap-ack-zero … [claimed]
  ✗ fd-item-premise-signal-table-has-no-history — 못 집었다
    claimed   세션 01KZ71DM… 가 선점했다
    이 항목 없이 나머지를 진행한다. 필요하면 그 세션에게 note(kind:"ask") 로 알려라
```

묶음 축이 없는 응답(구서버·옛 캐시):

```
묶음: 이 응답은 그 축을 읽지 않았다 — 낡은 캐시이거나 서버가 이 축을 모르는 판이다.
```

**어느 갈래에서도 침묵하지 않는다.** 묶을 게 없으면 없다고 한 줄 찍는다 —
`renderPathCheck`(`render.go:640-651`)이 같은 규율을 이미 갖고 있다.
침묵하면 "묶을 게 없다"와 "이 축을 안 봤다"가 같은 화면이 되고,
그러면 판정이 통째로 실패한 날에도 `pick` 은 평소와 똑같아 보인다.

---

## 5. 원장 — `pick_eval` 에 한 칸

```sql
ALTER TABLE pick_eval ADD COLUMN picked_with TEXT;  -- JSON 배열, 선두 제외. NULL 이면 단독
```

`picked` 는 **선두**를 계속 담는다. 기존 행도, 탈락 사유 분포를 세는 기존 질의도 안 깨진다.
추가만 하는 마이그레이션이다.

> 마이그레이션 번호는 **착수 시점에 정한다.** `002_idempotency.sql` 이 main 의 마지막이고
> `003_landing_queue.sql` 이 다른 세션에 미랜딩으로 있다 — §9 참조.

`item.pick` 이벤트에도 `picked_count` 를 싣는다. §10 의 "세션당 쓰기 호출 수"가
묶음 도입 전후로 비교 가능해야 이 기능이 실제로 호출을 줄였는지 판정할 수 있다.

---

## 6. 오류 처리 — 선두만 원자

```
선두를 못 집었다  → 전부 거절한다. 아무것도 안 쓴다.
                    브랜치가 정의되지 않으므로 "묶음을 집었다"고 말할 수 없다.
선두를 집었다     → 나머지는 집힐 만큼 집는다. 못 집은 것은 탈락 사유 그대로 응답에 싣는다.
```

선두 선점은 지금과 같은 트랜잭션이다(`t.ClaimItem` + `t.Touch`). 구성원은 **각각 별도
트랜잭션**으로 시도한다 — 하나를 남이 채 갔다는 이유로 이미 성립한 선두 선점을 되돌리면
세션이 아무것도 못 얻고, 동시 세션 19건 환경에서는 그 재시도가 잦다.

구성원 선점 실패는 `pick` 을 실패시키지 않는다. `fillQueueOpen` 이 같은 규율을 갖고 있다
(`pick.go:198-205` — "표시용 숫자 하나 때문에 선점을 잃는 것이 더 나쁘다").
다만 실패를 **침묵하지 않는다**: 사유 코드 그대로 `BundleMember.Rejection` 에 싣는다.

`item.claim` 이벤트는 구성원마다 남긴다. 거절당한 선점도 원장의 자산이다.

---

## 7. 일부러 안 하는 것

| 안 한다 | 왜 |
|---|---|
| 묶음을 저장한다(테이블·id·상태) | 새 개념이 하나 늘고, 그 순간 "묶음이 깨졌다"·"묶음을 해체한다" 같은 상태 전이가 따라온다. 파생으로 충분하다 |
| 전이적 연결 성분 | 넓은 디렉토리 토큰 하나가 큐의 3분의 2를 한 묶음으로 만든다 — 실측(§0.1) |
| 묶음 크기 상한 | 근거 없는 상수다. 자르면 "무엇이 잘렸나"를 따로 내야 한다. §2.2 규칙이 실측에서 최대 3건으로 자연히 갇힌다 |
| 결합 이득 가중치 | 같은 이유. 지금 큐에 "이 상수에 근거가 없다"를 고치는 항목이 실제로 열려 있다 |
| 꼬리표·본문 유사도로 묶기 | 꼬리표는 설계 §5 가 어떤 배제 판정에도 안 쓴다고 못박았다. 본문 유사도는 정밀도 미지수의 연구 과제다(§0.4) |
| 항목 id 접두로 묶기 | 이름 규칙에 의존하는 신호다. 규칙을 안 지킨 항목에서 조용히 죽고, 그 죽음이 화면에 안 보인다 |
| `finish` 를 묶음 단위로 | 항목마다 "왜 그렇게 했나"가 따로 있어야 한다. 묶음 3건이면 `finish` 3회, handoff 판단 3건이 맞다 |
| `Eligible`·`PathsOverlap` 수정 | 소비자가 다른 질문을 한다. 같은 함수를 두 질문에 쓰면 한쪽을 고칠 때 다른 쪽이 조용히 바뀐다 |
| 자동 워크트리 생성 | 지금도 명령만 낸다. 실행은 세션이 한다 |

---

## 8. 시험 — 소비자 좌표계(설계 §12)

**`internal/judge` (순수, 시험이 직접 부른다)**

- `SamePaths` — 정확 일치만. `cmd/fd` ↔ `cmd/fd/hook.go` 가 **안** 걸리는지(§0.1 의 실물 쌍).
- `LinkOf` — 축 셋 각각 단독 · 셋 조합 · 경로 단독이면 `nil` 인지.
- `EligibleBundle` — 이웃 0건(단독) · 정렬 네 키 각각이 실제로 갈리는지 · 동점에서 id 로 갈리는지.
- 흡수 — `after-unmet-item` 이고 선행이 묶음 안일 때만 들어오는지. `after-dropped-dep`·
  `after-unknown` 은 **안** 들어오는지(코드별로 한 줄씩).
- 전이 안 함 — A–B, B–C 인데 A–C 가 무관하면 A 선두 묶음에 C 가 없는지.

**`internal/service`**

- 선두 원자 — 선두가 막히면 아무 `claim` 행도 안 생기는지.
- 부분 실패 — 구성원 하나가 막혀도 선두 선점이 남고 사유가 실리는지.
- 불변식 — 모든 후보가 `picked`(선두 또는 `picked_with`)이거나 `rejected` 에 최소 한 줄인지.
- `QueueOpen` 이 묶음 N건을 뺀 수인지, 재개가 같은 수를 내는지.

**`internal/mcpsrv`** — 단정 대상은 **응답 문자열**이다.

- 묶음 절이 §4 문구대로 나오는지 · `Bundle` 이 `nil` 일 때 부재 문장이 나오는지.
- `item_id` + `item_ids` 동시 거절.
- 구성원 0건일 때 "묶을 게 없어 단독이다"가 찍히는지(침묵하지 않는지).

**`cmd/fd` 왕복** — 서버 → JSON → 클라이언트 → 렌더. 묶음 3건이 그대로 나오는지.

**빨간불을 먼저 본다.** `LinkOf` 에서 축 하나를 지우고, `SamePaths` 대신 `PathsOverlap` 을
쓰도록 되돌리고, 정렬에서 ②를 빼 보고 — 각각에서 시험이 죽는지 확인한 뒤에 초록을 믿는다.
§0.1·§0.2 는 실측으로 잡은 결함이므로 **그 둘을 되돌렸을 때 죽는 시험이 반드시 있어야 한다.**

---

## 9. 조율 — 지금 살아 있는 세션과 정면으로 겹친다

이 설계가 만지는 자리 전부가 실측 순간 다른 세션이 잡고 있던 자리다:

| 자리 | 겹치는 세션(실측 시점) |
|---|---|
| `internal/service/pick.go` | 01KZ73Z0 · 01KZ7DBM · 01KZ74M7 · 01KZ785T |
| `internal/mcpsrv/render.go`·`tools.go` | 01KZ71DM · 01KZ5J2H · 01KZ71G8 (한 세션은 **도구를 6→7 로 늘리는 중**) |
| `internal/judge/` | 01KZ7DBM · 01KZ7F6Y |
| `internal/store/schema.sql` | 01KZ71WF (미랜딩 `003_landing_queue.sql`) |
| `plugins/flightdeck/DESIGN.md` | 다섯 세션이 §3·§7 각 한 줄씩 |

착수 전에 `note(kind:"ask")` 로 **만질 자리를 줄 단위로** 낸다(스킬 §5).
특히 둘:

- **마이그레이션 번호** — `003` 이 미랜딩이라 이 항목은 `004` 가 되거나, 랜딩 순서가 뒤집히면 `003` 이다.
  번호를 착수 시점에 정하고 그 사실을 `ask` 로 알린다.
- **`DESIGN.md` §6 의 `pick` 행** — 지금 01KZ71DM 이 303행을 만지고 있다.
  `item_ids` 를 그 행에 더해야 하므로 정면 겹침이다.

---

## 10. 이 판정이 뒤집히는 조건

- **의존자 축이 살아나면** §2.4 의 ①이 실질 1차 키가 되고 ②의 영향이 줄어든다.
  그때 ②가 여전히 필요한지 재라 — 실측 없이 두 키를 다 유지하지 마라.
- **형제 묶음이 커지면**(실측 최대 3). 다른 프로젝트 데이터에서는 한 handoff 에 열린 항목
  8건이 매달린 사례가 있었다. 이 프로젝트에서 그 크기가 나오기 시작하면 상한 논의를 다시 연다 —
  다만 그때도 상한이 아니라 **`finish` 가 후속을 8개 내는 것 자체**를 먼저 의심하라.
- **경로 선언이 정밀해지면** 조상 규칙을 다시 켜는 것을 검토할 수 있다.
  지금 막는 것은 디렉토리 통째 선언이지 조상 규칙 자체가 아니다.
- **§0.4 의 못 잡는 부류가 잦아지면** — 사람이 `item_ids` 로 만든 묶음과 추천 묶음의 차이를
  `pick_eval` 로 셀 수 있다. 그 차이가 크면 축이 부족한 것이고, 그때 무엇을 더할지는
  그 실측이 지목한다. 지금 지어내지 않는다.
