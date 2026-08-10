# 겹침 줄이 변경 규모를 함께 낸다 — 한 줄 순삽입과 47줄 개정을 가른다

- 항목: `fd-overlap-prescription-ignores-change-size`
- 날짜: 2026-08-11
- 경로: `internal/gitreader/gitreader.go` · `internal/model/types.go` · `internal/service/board.go` ·
  `internal/service/service.go` · `internal/judge/eligible.go` · `internal/mcpsrv/render.go` ·
  `internal/service/prescribe.go`(주석만) · `DESIGN.md`(§5 순삽입만)

## 1. 항목 본문의 전제 둘이 코드와 안 맞는다

항목은 축 하나를 세우고 관문 하나를 걸었다. **둘 다 코드를 읽어 보니 다르게 서 있다.**

### 1.1 "첫 번째 관문"에는 이미 답이 적혀 있다 — 처방 경로에 대해서는 *아니오*다

항목 본문: "발자국은 경로만 있으므로 뭐가 되든 git 을 한 번 더 읽는 비용이 든다 — 그 비용이
허용 범위인지가 첫 번째 관문이다."

그 관문은 이미 판정돼 있고 판정문이 우리가 고치려던 파일에 있다.

> `service/prescribe.go:18` — ★ **세션 카드 파생을 안 돈다.** 이 경로는 턴마다 돌므로,
> git worktree list + 세션별 `ChangedPaths`·`UncommittedPaths` 를 얹으면 **모든 턴 종료에
> 저장소 전수 훑기가 붙는다**. 필요한 입력(footprint·claim·judgment·session·레인)은 전부 DB 표라
> git 을 안 탄다. 설계 §6 이 `/notices` 를 `/dashboard.json` 에서 가른 것과 같은 판정이다.

항목 본문 자신이 그 선례(`/notices`)를 인용했는데, 인용한 그 판정이 **이 축에 대해 이미
적용돼 있다는 것**을 못 봤다.

### 1.2 겹침을 내는 표면은 둘이고 항목은 하나만 봤다

| | 자리 | git | 지금 문구 |
|---|---|---|---|
| **처방** | 턴 종료마다 · `judge.overlapPrescriptions`(`judge/prescribe.go:319`) | **금지**(위 판정) | `DESIGN.md 를 만졌는데 세션 X 도 DESIGN.md 를 잡고 있다` |
| **꼬리 겹침** | `board`·`pick` · `judge.OverlapsWithLive`(`judge/eligible.go:228`) → `mcpsrv/render.go:1731` | **이미 돈다** | `· 01KZPBB3…: DESIGN.md↔DESIGN.md` |

이 항목을 집은 세션이 받은 `pick` 응답의 꼬리가 정확히 그 무차별 4줄이었다 — 네 세션이 전부
`plugins/flightdeck/DESIGN.md↔plugins/flightdeck/DESIGN.md` 하나로 접혀 있었다.

**꼬리 겹침이 조율을 실제로 판단하는 자리다.** 작업을 집을 때(`pick`)와 보드를 볼 때(`board`)
사람이 "저기에 ask 를 써야 하나"를 정하는 순간이 거기다.

### 1.3 "발자국은 경로만 있다"도 절반만 참이다

훅은 `PostToolUse` 페이로드 전체를 쥐고 있다 — `old_string`·`new_string`·`content` 가 그
안에 있다. `EditedPaths`(`cmd/fd/hook.go:71`)가 **경로 키 셋만 뽑고 나머지를 버린다.**
원천에는 규모가 있다.

그래서 안이 셋이었다(훅 실측 · 보드 numstat · 처방 git). **보드 numstat 을 골랐다** — 근거는
§3 에 있다.

## 2. 비용을 실측으로 갈랐다

`board.go:373` 의 `sessionCardsAndRoots` 가 스스로 "이 서버에서 가장 비싼 일"이라 적고
계측(`s.derives` · `s.deriveMicros` · `s.deriveCards`)까지 달아 뒀다. 지금 살아 있는 세션마다
git 호출이 넷이다.

| 호출 | 자리 | numstat 을 넣으면 |
|---|---|---|
| `merge-base` | `board.go:437` | 안 바뀐다 |
| `diff --name-only -z --no-renames` | `board.go:440` → `gitreader.go:265` | **`--numstat` 으로 바꾼다. 같은 프로세스 — 공짜** |
| `rev-list`(ahead/behind) | `board.go:449` | 안 바뀐다 |
| `status --porcelain -z` | `board.go:458` → `gitreader.go:402` | 안 바뀐다(미추적·이름변경 원본을 나르는 유일한 축) |
| — | — | **`diff --numstat -z HEAD` 하나가 새로 붙는다** |

**새로 드는 것은 세션당 git 호출 하나뿐이다**(4→5). 그 하나를 내는 이유: 이 항목을 낳은 손
앵커들("DESIGN.md 헝크 1개, §6 493행 1줄 치환 + 3줄 순삽입")은 **대부분 랜딩 전, 즉 미커밋
구간**이다. 안 재면 조율이 가장 필요한 창에서 정확히 "규모 못 읽었다"만 뜬다.

`pick` 도 같은 카드를 쓴다(`pick.go:291` 의 `live := liveFor(cards)`) — **두 표면이 같은
원천이라 문구가 문자 그대로 같아지고 파생도 한 번만 돈다.**

### 2.1 numstat 의 출력 형태 — 관측으로 적는다

기억이 아니라 실물로 확인했다(2026-08-11, 이 저장소 + 임시 저장소).

```
$ git diff --numstat -z --no-renames HEAD~3 HEAD --
8\t0\t.gitignore\0 1\t1\t…/plugin.json\0 12\t4\t…/cmds.go\0 …

$ git diff --numstat -z --no-renames HEAD --      # 이진 파일 + 미추적 파일이 있는 트리
-\t-\tb.bin\0 1\t0\tt.txt\0

$ git status --porcelain -z
 M b.bin\0 M t.txt\0?? untracked.txt\0
```

셋이 확인됐다.

1. 레코드는 `증가 TAB 감소 TAB 경로 NUL` 이다. 선행 NUL 도 헤더도 없다.
2. **이진 파일은 `-`/`-` 다** — 0 이 아니다.
3. **미추적 파일은 numstat 에 아예 안 나온다.** `status` 에만 `?? ` 로 있다.

`--no-renames` 가 이미 붙어 있어 numstat 의 rename 3필드 형태(`a TAB d TAB NUL from NUL to NUL`)는
나오지 않는다. **그 플래그가 지금 `--name-only` 에 대해 지고 있는 하중과 같은 종류의 하중을
파서에 대해서도 지게 된다** — 떼면 파싱이 조용히 어긋난다. 시험이 그것을 못박는다(§7).

## 3. 정한 것 — 넷

1. **꼬리 겹침에만 싣는다. 처방은 안 건드린다.** 처방 경로가 git 을 안 도는 것은 §6 판정이고
   그대로 산다. `judge/prescribe.go` 와 `service/prescribe.go` 의 코드는 한 줄도 안 바뀐다
   (후자에 "이 축이 왜 여기로 안 왔나" 주석 한 자리만 는다).
2. **미커밋 구간도 잰다.** 세션당 git 호출 4→5. 근거는 §2.
3. **경로마다 · 상대 쪽에만 붙인다.** 두 표면이 같은 모양이 된다 — `board` 의 왼쪽은 내
   발자국이고 `pick` 의 왼쪽은 항목의 **선언 경로**(아직 diff 가 아니다)라, 왼쪽에 규모를 실으면
   `pick` 에서 `(+0/-0)` 이라는 거짓말을 하거나 두 표면의 문구가 갈린다. 그리고 내 규모는
   내가 이미 안다.
4. **보이기 + 큰 것부터 정렬. 접지 않는다.** 항목 본문이 "수를 보이는 것과 접는 것을 갈라
   판단해라"라고 했고, 꼬리는 처방과 달리 억제 축이 없어 접을 동기가 없다(§5 "거르지 않고
   알린다"). 대신 그 수가 **이미 있는 절단**(`tailOverlapLimit`)의 순서를 정한다.

## 4. 자료 흐름과 타입

**새 값 하나.** 나르는 그릇은 전부 `map[string]LineDelta` 이고 **없는 키는 0 이 아니라
"못 읽었다"** 다 — `model.SessionView.Signals`(`model/types.go:355`)의 관용 그대로:
"없는 종류는 키가 없다. 0값과 부재를 가른다".

```go
// model/types.go
//
// LineDelta 는 경로 하나의 증감이다.
//
// ★ 이것을 나르는 맵에서 **키가 없는 것은 0 이 아니라 "못 읽었다"** 다. 둘을 뭉개면
// "안 만졌다"와 "못 쟀다"가 같은 화면이 되고, 그것이 이 항목이 없애려는 오탐의 거울상이다.
type LineDelta struct{ Added, Removed int }
```

```
gitreader                        service/board.go            judge/eligible.go        mcpsrv/render.go
─────────                        ────────────────            ─────────────────        ────────────────
ChangedPaths(base, head)         card.View.PathDelta         LiveSession.Delta        DESIGN.md↔DESIGN.md(+47/-1)
  --name-only → --numstat  ─┐      = changed ⊎ uncommitted ─→  ↓                      cmds.go↔cmds.go(규모?)
  (같은 프로세스)            ├─→    (model.SessionView 에      Overlap.TheirDelta
UncommittedDelta(worktree) ─┘        필드 하나 추가)            map[상대경로]LineDelta
  diff --numstat -z HEAD
  (새로 드는 유일한 호출)
```

### 4.1 `ChangedPaths` 의 반환을 늘린다

```go
func (r *Reader) ChangedPaths(ctx context.Context, base, head string) ([]string, map[string]model.LineDelta, error)
```

**새 메서드로 빼지 않는다.** 빼면 `--name-only` 와 `--numstat` 이 두 프로세스가 되어
"공짜"라는 §2 의 전제가 무너진다. 경로 목록은 numstat 출력에서 그대로 파생된다.

파급은 작다 — 이 인터페이스(`service/service.go:96`)의 구현체는 **둘**뿐이다:
`gitreader.Reader` 와 `degrade_test.go:35` 의 `flakyReader`.

### 4.2 `UncommittedPaths` 는 그대로 두고 `UncommittedDelta` 를 더한다

```go
func (r *Reader) UncommittedDelta(ctx context.Context, worktree string) (map[string]model.LineDelta, error)
// git diff --numstat -z --no-renames HEAD --
```

**대체가 아니라 추가다.** 이유 둘:

- `status --porcelain -z` 는 **미추적 파일과 이름 변경 원본 경로를 나르는 유일한 축**이고
  numstat 은 둘 다 못 낸다(§2.1 의 관측 2·3). 그 독스트링(`gitreader.go:399`)이 이미
  "커밋 전 의도를 나르는 유일한 축이라 조용히 짧아지면 안 된다"고 못박았다.
- 별개 호출이라 **새 호출이 실패해도 경로 축이 산다**(§6).

### 4.3 합산이 옳다 — 이길 규칙을 만들 필요가 없다

`card.View.PathDelta` 는 `changed` 와 `uncommitted` 의 **합**이다. 두 구간
(`forkSHA..branch` 와 `HEAD..worktree`)은 **서로소**라 더하면 "갈래 지점 이후 전부"가 정확히
나온다. 어느 쪽이 이기는 규칙도, 중복 제거도 필요 없다.

한쪽만 알려진 경로는 아는 쪽의 값이 그대로 남는다. **둘 다 없으면 키가 없다.**

### 4.4 `Overlap.Pairs [][2]string` 을 안 바꾼다

```go
// judge/eligible.go
type Overlap struct {
	SessionID string
	Label     string
	Pairs     [][2]string
	// TheirDelta 는 Pairs[i][1](상대 세션의 경로)의 증감이다.
	//
	// ★ **키가 없으면 0 이 아니라 "못 읽었다"** 다. 그리고 Pairs 와 합치지 않는다 —
	// judge.OverlapPairs 는 처방(overlapPrescriptions)도 쓰는데 그 경로는 git 을 안 돌아
	// 이 값을 원리적으로 못 채운다. 합쳤다면 처방 쪽에 영원히 빈 필드가 생긴다.
	TheirDelta map[string]model.LineDelta
}
```

`judge.OverlapPairs` 의 서명을 안 건드리는 것이 요점이다. 처방 경로의 코드가 한 줄도 안 바뀐다.

## 5. 화면

지금(`render.go:1731-1746` · 세션 5건 `tailOverlapLimit` · 세션당 쌍 4개에서 자른다):

```
겹침 6건 (거르지 않고 알린다):
  · 01KZPBB3… (꼬리표 없음): plugins/flightdeck/DESIGN.md↔plugins/flightdeck/DESIGN.md
  · … 1건 더 — 수는 위 머리줄이 전부 센 값이다. 이름까지는 board 가 낸다
```

바뀐 뒤:

```
겹침 6건 (거르지 않고 알린다 · 상대 규모 큰 순):
  · 01KZP9EF… (꼬리표 없음): plugins/flightdeck/DESIGN.md↔plugins/flightdeck/DESIGN.md(규모?)
  · 01KZPBB3… (꼬리표 없음): plugins/flightdeck/DESIGN.md↔plugins/flightdeck/DESIGN.md(+47/-1)
  · 01KZP8FB… (꼬리표 없음): plugins/flightdeck/DESIGN.md↔plugins/flightdeck/DESIGN.md(+3/-0)
  · … 1건 더 — 잘린 것은 **제일 작은 쪽**이다. 수는 위 머리줄이 전부 센 값이다
```

### 5.1 못 읽은 것은 `+∞` 로 친다 — 맨 위다

정렬 키는 그 세션의 쌍 중 **최대** `Added+Removed` 다. **못 읽은 쌍이 하나라도 있으면 그
세션은 `+∞`** — 즉 아는 값을 가진 모든 세션보다 위다. 동점은 세션 id 오름차순이라 결정적이다.

**아래로 밀지 않는 이유.** 밀면 절단될 때 **제일 먼저 사라지는 것이 못 잰 것**이 된다.
이 저장소가 반복해서 고발한 침묵이 정확히 그 모양이고, 같은 규율이 세 자리에 이미 적혀 있다 —
`judge/prescribe.go` 의 `comparablePath`("못 읽었다는 없다가 아니다"),
`sameConversation`("빈 값끼리는 같지 않다. 못 읽었다를 같다로 접으면 겹침 축이 조용히 꺼진다"),
`board.go:469` 의 `HasFootprint`("발자국이 없다는 사실을 침묵하지 않는다"). 모르는 것은 클 수
있으니 크게 친다. 규칙이 **하나**여야 하므로 계층을 두지 않는다.

**반례를 하나 확인하고 뺐다.** `laneTurnPrescription`(`judge/prescribe.go:294`)은 정반대로
적혀 있다 — "0 은 차례 아님이고 호출자가 레인을 못 읽었을 때도 0 이다. **둘을 안 가르는 것이
맞다**". 거기서는 접는 쪽이 안전하기 때문이다(못 읽은 것을 "차례다"로 펴면 세션을 반드시
실패할 자리로 보낸다). 이 항목은 방향이 반대다 — 접으면 **정보가 사라지고**, 펴면 조율 한 번이
더 일어날 뿐이다. 그래서 그 선례를 근거로 쓰지 않는다.

### 5.2 표기는 `(규모?)` — 수와 모양이 달라야 한다

`(+0/-0)` 으로 찍으면 "안 만졌다"로 읽힌다. 그것이 이 항목이 없애려는 오탐의 거울상이다.

### 5.3 머리줄과 꼬리줄이 정렬을 소리 내어 말한다

순서에 뜻이 생겼으면 그 사실이 화면에 있어야 한다. `· … N건 더` 가 지금은 어느 N건인지 안
말하는데, 정렬을 넣고도 그대로 두면 화면이 **말 안 한 주장**을 하게 된다.

## 6. 열화 — 새 호출이 규모 축만 죽인다

| 사건 | 결과 | 자리 |
|---|---|---|
| `UncommittedDelta` 실패 | 규모 키 부재 · **경로는 산다** | `d.fail("uncommitted-delta:…")` + 카드 `DeriveError` |
| `ChangedPaths` 실패 | 지금과 같다(경로도 규모도 없다) | `board.go:441` 기존 분기 |
| `MergeBase` 실패 | 지금과 같다 | `board.go:438` 기존 분기 |
| 커밋 0개 저장소 | `diff HEAD` 가 실패 → 위 첫 줄과 같음 | **특례를 안 만든다** |
| 이진 파일(`-`/`-`) | 키 부재 | 파서 |
| numstat 수 파싱 실패 | **그 경로만** 키 부재 + WARN | 파서 |
| 미추적 · footprint 전용 경로 | 키 부재 | 자연히 |
| 프로젝트 경로 없음(`g == nil`) | 규모 전부 키 부재 | `board.go:466` 기존 분기 |

**커밋 0개 저장소에 특례를 안 만드는 이유:** 바로 이웃 줄인 `Ref(proj.DefaultBranch)`
(`board.go:406`)가 이미 같은 저장소에서 `d.fail` 을 낸다. 특례를 만들면 **붙어 있는 두 줄의
관용이 갈린다.**

**파서는 전체를 안 버린다.** 레코드 하나가 깨져도 그 경로만 빼고 나머지를 낸다 —
`emittedKeys`(`service/prescribe.go:336`)가 payload 해석 실패에 대해 쓰는 규율과 같다:
조용히 버리지 않고 WARN 을 남기되 그 하나 때문에 축 전체를 끄지 않는다.

## 7. 시험

- **`parseNumstatZ` 순수 시험** — 정상 · 이진 `-\t-` · 빈 출력 · 수 아닌 값 · 후행 NUL.
  **`--no-renames` 가 붙어 있어 rename 3필드 형태가 안 나온다는 사실을 시험이 못박는다.**
- **judge** — `OverlapsWithLive` 가 `TheirDelta` 를 나른다 · 정렬 결정성 ·
  **못 읽은 것이 맨 위** · 동점은 세션 id.
- **render** — 아는 값 · `(규모?)` · 쌍 4개 클립 · "제일 작은 쪽" 꼬리줄 · 머리줄의 "상대 규모 큰 순".
- **board** — `changed ⊎ uncommitted` 합산 · footprint 전용 경로는 키 없음 ·
  **`UncommittedDelta` 만 실패시켰을 때 경로 축이 사는지**(`flakyReader` 확장).
- **변이 시험 하나** — 못 읽음을 0 으로 접으면 빨개지는 시험. **이 항목의 전체 논지가 그 한 줄에
  달려 있다.** DESIGN §12 가 "새 검사는 망가진 것을 넣어 빨간불을 먼저 확인한다"고 요구하므로
  새 시험 전부에 대해 그 확인을 하되, 이 하나는 **변이를 시험으로 고정**한다.

랜딩 관문: `gofmt -l` · `go vet ./...`(교차 빌드 — `go build` 는 `_test.go` 를 건너뛴다) ·
`go test -count=1 ./...`.

## 8. 안 하는 것과 그 이유

- **`ask` 를 없애지 않는다.** 항목 본문이 이미 금지했다. 사람이 쓰는 앵커에는 수가 못 나르는
  것이 있다 — "같은 함수를 만진다" · "기존 본문은 무변경이다" · "이 줄은 일부러 남겼다".
- **처방에 규모를 안 싣는다.** §1.1 · §3-1.
- **훅에서 규모를 안 잰다.** `ToolInput` 에 규모가 있는 것은 사실이지만(§1.3), 그것을 쓰면
  같은 수에 원천이 둘이 되고 서로 어긋난다. 그리고 Claude 도구 밖 편집(bash · 수동 ·
  서브에이전트)을 못 본다. **git 이 이미 보는 것을 두 번 재지 않는다.**
- **임계값 아래를 접지 않는다.** §3-4.
- **`tailOverlapLimit`(5)·쌍 상한(4)·머리줄 건수를 안 바꾼다.** 이번 판은 순서만 정한다.
- **겹침 줄의 양쪽 경로가 같을 때 한쪽을 접지 않는다.** 화면이 짧아지긴 하지만 이 항목의
  축이 아니고, 접기 규칙은 그 자체로 판단이 필요하다.
- **새 계측 카운터를 안 만든다.** `deriveMicros` 가 이미 이 함수 전체를 감싼다
  (`board.go:378-382`) — 새 호출의 비용은 그 수에 자동으로 들어간다.

## 9. DESIGN.md 편집 범위 — 최소로 잡았다

**§5 의 소절 「경로 겹침 축」(`DESIGN.md:469`)에서 `DESIGN.md:477` 뒤에 문단 하나를
순삽입한다.** 헝크 1개 · 순삽입 4줄(빈 줄 포함) · **삭제 0** · 기존 문장 치환 0.

```markdown
**그리고 규모를 함께 낸다.** 겹침 줄은 상대 세션이 그 경로를 얼마나 만졌는지(`+증가/-감소`)를
함께 내고 큰 것부터 세운다 — 경로가 겹치는가만으로는 한 줄 순삽입과 47줄 개정이 같은 무게로 뜬다.
**못 잰 것은 0 이 아니라 `(규모?)` 다.**
```

**번호 목록(471-474행)에 항목 4 로 넣는 것을 기각했다.** 두 이유다.

- 제목이 "**세 겹**을 다 고친다"라 항목을 더하면 제목의 한 낱말을 **치환**해야 한다.
  선언한 "순삽입 · 치환 0" 이 깨지고, 제목 줄은 다른 세션과 부딪히기 가장 쉬운 자리다.
- **글로도 안 맞는다.** 그 셋은 전부 *결함의 수정*(정규화 결함 · 레포 축 오탐 · 착수 구간
  공백)이고 이번 것은 결함 수정이 아니라 축에 더하는 성질이다. 성질을 말하는 자리는 바로 아래
  비번호 문단(`476행`, "**그리고 거르지 않고 알린다.**")이고, 새 문단은 그것과 **문장 모양이
  같다** — 같은 종류의 것은 같은 모양으로 적는다.

`476-477행`("거르지 않고 알린다")은 안 건드린다 — 이 항목은 거르는 것이 아니라 순서만 정하므로
그 문단이 그대로 참이다.

**§6 은 안 건드린다.** "처방은 왜 이 수를 못 받나"는 **구현 계층**이라
`service/prescribe.go` 주석이 지는 게 맞고, DESIGN 은 독자가 밟을 함정만 진다.
그리고 지금 이 문서를 잡은 세션이 넷이라 부딪힐 면을 줄인다 — 범위는 착수 직후
`note(kind='ask')`(판단 `01KZPF3YZ55E8Q300P03CKB427`)로 그 넷에게 선언했다.

인용하는 절 번호는 `cmd/fd/design_cites_test.go` 의 관문을 탄다. 이 스펙이 인용한
§5 · §6 · §12 는 전부 실재한다(그 관문은 **존재만** 재므로 내용 일치는 사람이 확인한 것이다).
