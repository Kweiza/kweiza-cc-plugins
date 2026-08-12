# 이미 있는 항목에 꼬리표를 다는 표면 — 설계

- 항목: `fd-no-surface-to-set-a-label-on-an-existing-item`
- 날짜: 2026-08-12
- 상태: 승인됨 (구현 계획 대기)

## 1. 무엇이 없었나

`tickler` 는 fd 가 스스로 정의한 운용 수단이다(`judge/tickler.go`). 큐가 만료 조건의
보관소를 겸하므로(설계 §7·§10) 그 용법의 항목은 기한까지 늙는 것이 정상이고, 그 나이를
굶김 지표에 넣으면 경고가 상시 점등돼 판별력이 0이 된다 — 그래서 이 꼬리표 하나만
굶김 축에서 빠진다.

그런데 **이미 있는 항목에 그 꼬리표를 다는 표면이 전수로 없었다.**

- `add`·`finish.followups` 는 **만들 때** `labels` 를 받는다. 사후 경로만 없다.
- `fd move` 는 스스로 「고칠 수 있는 축은 프로젝트 하나뿐」이라고 적는다.
- `add` 재호출은 PK 중복으로 거절된다(`store/constraint.go` → `ConflictDuplicate`).
- 재생성(drop → add)은 **판단 링크를 끊는다** — 이 사고를 만든 항목은 연결된 판단이 17건이었다.

### 실측된 비용

2026-08-12, 사용자 판정을 집행하려던 세션이 **sqlite `UPDATE` 를 직접 쳤다**
(`context-platform` 의 `contracts-search-image-api`·`mcp-search-by-image-tool`).
원장에 그 조작의 흔적은 판단 하나뿐이고, fd 는 자기 상태가 밖에서 바뀐 것을 모른다.

### 두 번째 결함 — 선두에만 달면 무효였다

`judge/bundle.go` 의 `bundleAround` 는 선두가 tickler 면 `StarveOldest` 를 비우지만,
**구성원이 tickler 가 아니면 거기서 다시 채운다**(447~450). 위 두 항목은 `created_at` 이
글자까지 같아(일괄 반입분) 선두만 달았을 때 기아 값이 **한 자리도 안 줄었다** —
사용자 판정이 조용히 무효가 됐고, 세션이 `fd next` 를 두 번 돌려서야 알았다.

## 2. 착수 전 조사에서 뒤집힌 전제

항목 본문이 적은 처방 후보는 두 전제 위에 있었고, 둘 다 실측으로 정정됐다.

| 항목 본문의 주장 | 실측 |
|---|---|
| 표면을 만들면 `store/item_body_immutable_test.go` 가 조용히 거짓이 된다 | **안 깨진다.** 그 시험이 잠그는 컬럼은 `itemBodyColumns = {"title","body"}` 뿐이고, 통과 목록에 `UPDATE item SET project = ?` 가 이미 선례로 들어 있다. 낡는 것은 시험이 아니라 그 파일 **머리 주석**과 DESIGN 서술이다 |
| ⓐ·ⓑ 둘 다 "MCP 도구 하나"를 전제 | `mcpsrv/protocol_test.go` 의 `TestToolTableIsSeven` 이 도구 **이름과 순서까지** 인덱스로 잠근다. 8번째는 DESIGN §6 표 + 그 시험 + 90자 설명 상한을 함께 치러야 하는 값이다 |
| — (본문에 없던 사실) | **`fd move` 와 `fd after cut` 은 둘 다 MCP 도구가 아니다.** 사후 정정 조작 둘이 이미 REST+CLI 전용으로 서 있다 |

세 번째 줄은 "CLI 만으로 충분하지 않은가"라는 반론의 근거였다. 사용자 판정은
**셋 다 깐다**로 났다 — 사람도 세션도 같은 조작을 부르므로 경로마다 다른 답이 나오지
않게 한다. 도구 예산 규율(`tools.go` 머리: "더는 늘리지 않는다")은 6→7(`land`)이 간
길과 같은 방식으로 치른다: 늘어나는 고정비는 **이름 하나**이고 설명은 90자 안에 든다.

## 3. 표면 — 전용 동사 하나

축은 `labels` **하나**다. 일반 PATCH/amend 는 안 연다.
근거는 `api/handlers_items.go` 의 `cutAfterRequest` 주석이 이미 적었다:

> move 와 같은 규율으로 **전용 동사**다 — 일반 PATCH/DELETE 를 열면 "무엇까지 고칠 수
> 있나"가 다시 열린 질문이 되고, 그 질문은 항목 본문까지 번진다.

```
REST  POST /api/v1/items/{id}/label     {project, session_id, add[], rm[]}
CLI   fd label <item-id> --add tickler --rm 어쩌구      (--add/--rm 반복 지정)
MCP   label(item_id, add[], rm[])       8번째 도구
```

- CLI 는 `cmds.go` 의 기존 `stringList` 를 그대로 쓴다(`--add` 반복 지정).
- MCP 도구는 표의 **맨 끝**에 붙인다 — `TestToolTableIsSeven` 이 순서를 인덱스로
  잠그므로 끝에 붙여야 기존 7개의 자리가 안 밀린다(`land` 가 간 길).
- MCP 설명(90자 상한): **"항목의 꼬리표를 더하거나 뺀다. 'tickler' 만 굶김 축에서 빠진다."**

## 4. 저장 — 계산을 순수 함수로 가른다

읽고-고쳐-쓰기를 클라이언트에 두면 두 세션이 서로의 꼬리표를 지운다.
**한 트랜잭션 안**에서 읽고 계산한다.

```go
// judge — 순수. 중복 제거, 기존 순서 유지, 새것은 뒤에 붙인다.
func ApplyLabels(cur, add, rm []string) []string

// store — 전체 교체. affectedOne 으로 없는 항목의 조용한 성공을 막는다.
func (t *Tx) SetLabels(project, itemID string, labels []string, sessionID string) error
```

순서를 정렬하지 않고 **유지**하는 이유: `labels` 는 JSON 배열로 저장되고 되쓰기
산출물(`legacy/export.go`)이 원본과 diff 로 대조된다 — 정렬을 걸면 판이 바뀔 때
무관한 항목들의 줄이 통째로 흔들린다.

### 원장 — store 계층에서 남긴다

```
item.label  {item, add, rm, before, after}
```

**선례가 둘로 갈린다**: `move` 는 API 계층에서 `s.publish(r, "item.move", …)` 로 남기고,
`after cut` 은 store 계층에서 `t.LogEvent("item.after.cut", …)` 로 남긴다.
여기는 **store 쪽**이다 — `before` 를 아는 것은 같은 트랜잭션 안에서 읽은 쪽뿐이고,
API 계층에서 남기려면 그 값을 응답에 실어 되돌려 받아야 하는데 그러면 원장의
정확성이 응답 왕복에 의존하게 된다. `RemoveAfter` 가 store 에 있는 이유와 같다:

> 이 쓰기는 되돌리는 코드가 없고 (…) **무엇이 걸려 있었는지가 끊는 순간 사라진다.**

그래서 `SetLabels` 가 `sessionID` 를 받는다(`RemoveAfter` 와 같은 시그니처 모양).

이 항목을 만든 사고가 정확히 "원장에 흔적이 판단 하나뿐"이었으므로, 이 표면이 그
공백을 메우지 못하면 표면을 만든 의미가 없다.

### 응답

**전·후·실제 변화분**을 셋 다 낸다. 이미 있는 것을 `--add` 하거나 없는 것을 `--rm` 해도
거절하지 않되 `실제로 더한 것: 없음` 이 화면에 뜬다 — 집합 연산의 멱등은 지키면서
조용한 무변화는 안 만든다(`RemoveAfter` 의 "조용한 0건 성공은 최악이다"와 같은 축이되,
그쪽은 관계 절단이라 오류이고 이쪽은 집합이라 화면이다).

### 거절

| 조건 | 결과 |
|---|---|
| 없는 항목 | `notFound(NFItem, …)` |
| 종료된 항목(`done`·`dropped`) | `ItemClosedError` |
| `add`·`rm` 둘 다 빔 | 거절 — 조용한 무작업을 안 만든다 |

**종료된 항목을 거절하는 이유**: `labels` 의 유일한 판정 소비자는 굶김 축이고 그 축은
열린 항목만 본다. 끝난 항목의 꼬리표를 바꾸는 것은 아무 데도 안 닿으면서 원장만 늘린다.
`SetItemState` 가 종료를 안 되돌리는 규율과 결이 같다.

**`config` 의 `labels.values`·`strict` 검증은 안 붙인다.** 코드 전수에 그 검증 구현이
지금 없고, `strict: false` 가 기본인 이유가 "기존 항목을 거절하면 큐가 통째로 막힌다"다.
없는 관문을 이 표면에서 처음 만들지 않는다.

## 5. 묶음 전파 — 선두만 본다 (코드가 줄어든다)

`bundleAround` 의 **구성원 갱신 블록(`bundle.go:447~450`)을 지운다.** 남는 것은 선두 판정 하나:

```go
b := Bundle{Lead: lead, Dependents: lead.Dependents, Oldest: lead.Item.CreatedAt}
if !IsTickler(lead.Item.Labels) {
    b.StarveOldest = lead.Item.CreatedAt
}
```

근거는 **같은 함수 20줄 아래에 이미 있다**. `CloseDeclared` 판정이 선두만 보며 이렇게 적는다:

> 보는 것은 **선두 하나**다: 이 축은 "이 항목을 지금 새로 집어도 되나"에 답하고,
> 그 질문의 주어는 브랜치를 받는 선두다.

굶김 축도 같은 질문에 답하는데 이것만 구성원을 봤다. 이 저장소는 "비대칭이 결함의
자리를 가리킨다"를 규율로 쓴다(`affectedOne`·`SetItemState` 주석) — 그 모양 그대로다.

### 감춤이 안 생기는 이유

`EligibleBundle` 은 `for _, lead := range fit` 로 **모든 적격 항목을 각각 선두로 세워**
묶음을 만든다. 오래된 구성원은 자기가 선두인 묶음에서 제 나이로 기아 판정을 받는다.

자기 묶음이 없는 것은 흡수분(`absorbable`)뿐인데, 그들은 선행이 선두 하나뿐이라
(`blockedOnlyBy`) 선두 없이 못 간다 — 그들의 굶김이 선두에 종속되는 것은 감춤이 아니라
사실이다.

`Oldest`(순위 ③축)는 그대로 전체를 본다. 바뀌는 것은 굶김 축 하나다.

### 사람이 생각하는 단위를 어디에 적나

항목 본문은 "사람이 「당분간 추천하지 마라」로 생각하는 단위는 묶음인데 꼬리표는 항목
단위이고, 어느 쪽인지 아무 데도 안 적혀 있다"고 했다. **묶음은 영속 개체가 아니다** —
추천 시점에 동적으로 계산되고 선두가 바뀌면 묶음도 바뀐다. 그래서 "묶음에 꼬리표를 단다"는
성립하지 않는다. 성립하는 문장은 **"선두에 달면 그 묶음이 굶김 축에서 빠진다"** 이고,
그 문장을 DESIGN §5 에 적는 것이 이 설계의 답이다(§7 참조).

## 6. 표 등록 — 시험이 강제한다

`cmd/fd/write_cmd_table_coverage_test.go` 가 AST 로 `a.cli.Write` 의 명령 인자를 전수
수집해 두 표와 양방향 대조한다. 빠뜨리면 `default` 로 떨어지는데 그 기본값이 하필
**안전한 방향**(거절·새 키)이라 아무도 안 아프고 그래서 아무도 못 본다.

- `CmdLabel = "label"` (`cmd/fd/offline.go`)
- **`JudgeOffline` → 거절.** 사유: 지금 그 항목에 무엇이 붙어 있는지를 원장에서 봐야
  add/rm 이 성립한다. 아웃박스에 쌓아 재생하면 그 사이 다른 경로로 꼬리표가 바뀌었어도
  낡은 요청이 그대로 덮는다. (`CmdAfterCut` 과 같은 결)
- **`IdempotencyStable` → false.** 응답이 지금 상태다(전·후). 고정하면 꼬리표가 도로
  바뀐 뒤 같은 본문으로 다시 불러도 실제 변경 없이 옛 성공이 재생된다. (`CmdMove` 와 같은 위험)

두 갈래 모두 **표 안에 명시**한다 — 동작은 `default` 와 같아도 사유가 다르다.
`CmdProjectRemove` 주석이 이미 그 함정을 이름으로 적어 뒀다.

## 7. 낡는 문장들 — 같은 커밋에서 고친다

표면이 생기는 순간 거짓이 되는 서술이다. **시험이 깨지는 것은 둘뿐이고 나머지는
조용히 낡는 쪽이라 더 위험하다.**

| 자리 | 무엇이 바뀌나 | 깨지나 |
|---|---|---|
| `mcpsrv/protocol_test.go` `TestToolTableIsSeven` | 이름·개수·순서 → 8개 | **빨간불** |
| DESIGN §6 REST ` ```routes ` 표 | `POST /items/{id}/label` 행 추가 | **빨간불** (`api/design_route_table_test.go` 가 mux 와 양방향 대조) |
| DESIGN §6 "MCP 도구 7개" 제목·표 | `label` 행 추가, 제목 8개로 | 조용히 낡음 |
| DESIGN §5 티클러 절 | "굶김 축 셋에서 빠진다"는 유지. **"선두에만 걸린다"를 새로 적는다** — 지금 아무 데도 안 적힌 그 사실이 사고의 절반이었다 | 조용히 낡음 |
| DESIGN §11 "안 만드는 것" | `labels` 는 §11 표에 **없다**. `title`·`body` 와 `item_after` 만 "안 만든다"로 판정돼 있다 — labels 의 부재는 **결정이 아니라 빈자리**였다는 것을 적는다 | 조용히 낡음 |
| `store/item_body_immutable_test.go` 머리 주석 | "REST(`/items` 6라우트)·MCP 7도구" → 7라우트·8도구 | 조용히 낡음 |

마지막 줄이 이 설계에서 가장 조심할 자리다. 그 시험은 **안 깨지므로** 주석만 낡은 채
초록으로 남는다 — 부재를 주장하는 문장이 조용히 거짓이 되는, 그 시험이 애초에 막으려던
바로 그 형태다.

## 8. 검증 좌표

항목 본문이 준 재현 절차를 그대로 관문으로 만든다. ★기아를 내는 묶음의 선두에
`tickler` 를 달고 다시 `pick`:

1. **추천에서 빠졌나** — `★기아` 문구가 사라진다
2. **여전히 적격인가** — 탈락 사유가 `not-top` 이어야 하고 배제면 안 된다.
   티클러는 「배제가 아니라 승격의 부재」다

### 단위 시험

- `ApplyLabels` — 중복 add · 없는 rm · 순서 유지 · add·rm 동시 · 빈 입력
- `bundleAround` — 선두 tickler + 구성원 non-tickler → `StarveOldest` 가 zero.
  **지금 판은 여기서 실패해야 한다** = 결함이 시험에 닿았다는 증거
- `bundleAround` — 선두 non-tickler + 더 오래된 구성원 → `StarveOldest` 가 **선두 나이**.
  의도된 변화를 못박는다(회귀가 아니라 판정임을 다음 사람이 알게)
- 종료된 항목에 label → `ItemClosedError`
- 없는 항목에 label → `notFound`
- `add`·`rm` 둘 다 비면 거절
- 원장에 `item.label` 이 남는가 (전·후 포함)

### 관문 (기존 시험이 자동으로 잡는다)

- `write_cmd_table_coverage_test.go` — `CmdLabel` 이 두 표에 다 있는가
- `design_route_table_test.go` — 새 라우트가 DESIGN 표에 있는가
- `protocol_test.go` — 도구 8개·순서·90자 설명

## 9. 범위 밖

- **`--until` 같은 만료 시각 인자.** `labels` 는 문자열 배열이라 시각을 못 담고,
  담으려면 컬럼이 는다. tickler 항목의 기한은 지금 본문(때로는 id)에 적혀 있고
  그것으로 충분하다는 실측이 아직 안 뒤집혔다.
- **`config.labels.values` 검증.** §4 참조.
- **항목 본문·선행의 사후 수정.** DESIGN §11 이 "안 만든다"로 이미 판정했다.
  이 설계는 그 판정을 안 건드린다 — `labels` 는 그 표에 **없던** 축이다.
