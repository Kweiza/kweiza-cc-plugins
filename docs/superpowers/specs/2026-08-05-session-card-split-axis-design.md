# 세션 카드 갈림 축 — 설계

- 작성 2026-08-05
- 브랜치 `fd-session-worktree-is-cwd-not-repo-root`
- 묶은 큐 항목 넷
  - `fd-session-worktree-is-cwd-not-repo-root` — 갈림을 만드는 문 ①
  - `fd-session-lookup-without-upsert` — 갈림을 만드는 문 ②
  - `fd-board-counts-one-conversation-many-times` — 이미 갈린 것을 표시에서 접는다
  - `fd-ack-metric-measures-card-split` — 그 축을 재는 지표를 고친다

## 1. 왜 넷이 한 브랜치인가

세션 정체는 3중키다 — `(machine_id, worktree, cc_session_id)`. 그 둘째 성분이 흔들리면
한 대화가 카드 여러 장이 된다. 카드가 갈리면 발자국(훅이 쓴다)과 판단(MCP 가 쓴다)이
서로 다른 행에 쌓이고, 그때 보드의 세션 수 · 겹침 처방 · 확인율이 **동시에** 거짓이 된다.

넷은 그 하나의 축을 앞뒤로 나눈 것이다. 따로 가면 뒤의 둘이 앞의 둘의 전제를 만족하지
못한다 — 접기를 먼저 랜딩하면 "왜 갈리는지"가 화면에서 사라지고, 지표를 먼저 고치면
분모가 여전히 갈린 카드를 센다.

### 착수 전 실측 (2026-08-05 03:0x UTC)

세 항목이 본문에서 "먼저 재라"고 지시했다. 쟀다.

```
최근 6시간 · 카드 88장 / 대화 23개 · 형제 중복 65장 = 73%
```

항목 본문이 적은 69%(카드 32/대화 10)보다 **악화**됐다. `fd-board-counts-one-conversation-many-times`
의 반증 조건("훅이 형제를 합쳐서 인구가 곧 준다")은 성립하지 않는다.

갈림의 모양이 결정적이다. **형제 카드는 예외 없이 워크트리 하나당 한 장이다.**

```
카드 16장 · 워크트리 16개 · cc=e5edfbf0-86f
카드  9장 · 워크트리  9개 · cc=fd36d7f3-1d2
카드  8장 · 워크트리  8개 · cc=ca0914ed-525
```

cc 축이 아니라 전적으로 worktree 축에서 갈린다.

## 2. 조사가 축의 모양을 바꿨다 — 코드는 이미 옳다

항목 ①은 두 갈래를 갈라 달라고 했다: 도는 서버가 옛 바이너리라서인가, 정규화가 닿지
않는 경로가 따로 있어서인가. **둘 다 아니다. 정규화 코드가 이 머신 어디에서도 안 돈다.**

`worktree` 값은 서버가 아니라 클라이언트(훅 · MCP)가 계산한다(`cmd/fd/env.go`
`resolveProject`). 서버는 그 값을 검증만 하고 그대로 받는다(`service/session.go`
`JudgeOpenSession` — 절대경로인지 · 경로 탈출이 없는지만 본다). 설계 §13 이
"worktree 는 클라이언트의 cwd 다"로 못박은 그대로다.

그 클라이언트들이 무엇으로 도는지 실측했다.

```
바이너리 A  ~/.local/state/flightdeck/bin/fd              mtime 08-05 08:27 KST
            --show-toplevel: 2  → 4de4b21 있다
바이너리 B  ~/.claude/plugins/data/.../flightdeck/bin/fd  mtime 08-04 18:07 KST
            --show-toplevel: 0  → 4de4b21 없다

살아 있는 MCP 6건 중 최신(08-05 03:43 기동) → B
fd serve (08-04 18:20 기동)                  → A 의 지워진 옛 inode
```

B 가 왜 안 갱신되는가. 런처(`bin/fd` 셸 스크립트)는 **플러그인 캐시 소스**가 바이너리보다
새로울 때만 다시 빌드한다. 그 소스의 상태는 이렇다.

```
플러그인 캐시 소스  08-04 18:07 로 얼어붙음 · WithWorktree 없음 · --show-toplevel 없음
캐시의 출처         GitHub kweiza/kweiza-cc-plugins 마켓플레이스 클론 — 거기에도 없다
로컬 main           origin 보다 35 커밋 앞서 있다(미푸시)
```

즉 `main` 에서 도는 클라이언트까지 이어지는 배포 경로가 지금 끊겨 있다. 서버를
재기동해도, Claude Code 를 재기동해도 B 는 그대로다 — 런처가 `바이너리(18:07) ≥
소스(18:07)` 를 보고 빌드를 건너뛴다. 4de4b21 이 랜딩한 뒤에도 하위 디렉토리 카드가
9장 더 생긴 이유가 이것이고, 가장 최근이 이 조사 30분 전이다.

**따라서 이 브랜치는 `resolveProject` 를 다시 고치지 않는다.** 고칠 것이 없다. 같은
판정을 두 자리에 두면 한쪽만 고칠 때 조용히 어긋난다 — 이 저장소가 세 번 겪은 사고다.
대신 **원장이 스스로 이 사실을 말하게 한다.**

## 3. 구성요소

### 3.1 갈림 탐지 — 원장만으로, 클라이언트 판과 무관하게

`internal/judge` 에 순수 함수 하나를 둔다.

```go
// SplitCard 는 탐지에 필요한 카드 한 장의 좌표다.
//
// LiveSession 을 재사용하지 않는다 — 그 구조체에는 머신도 워크트리도 없고,
// 겹침·처방 두 축이 이미 그것을 쓰고 있어 성분을 더하면 두 축의 입력이 함께 바뀐다.
type SplitCard struct {
	SessionID   string
	MachineID   string
	Worktree    string
	CCSessionID string
}

// SplitReport 는 한 대화가 상하위 경로로 갈렸다는 보고다.
type SplitReport struct {
	CCSessionID string
	MachineID   string
	Ancestor    string   // 조상 쪽 워크트리
	Descendants []string // 그 아래로 잡힌 워크트리들
	SessionIDs  []string // 이 보고에 걸린 카드 전부
}

// DetectUnnormalizedSplit 은 정규화가 안 돈 흔적을 찾는다. 순수 함수다.
func DetectUnnormalizedSplit(cards []SplitCard) []SplitReport
```

판정 규칙:

1. `MachineID` 가 같고
2. `CCSessionID` 가 같고 **비어 있지 않고**
3. 두 `Worktree` 가 **경로 조상·자손 관계**일 때

셋을 다 만족하면 보고 한 줄. 형제 관계(`worktrees/A` ↔ `worktrees/B`)는 **보고하지 않는다.**

#### 접두 일치를 쓰는 것에 대한 울타리

이 함수는 이 설계에서 유일하게 **경로 접두 일치**를 쓴다. DESIGN §3 이 일부러 없앤
축이고, 이 저장소는 그 축에서 두 번 다쳤다. 그래서 다음을 불변식으로 못박는다.

- **이것은 정체 판정도 겹침 판정도 아니다. 보고다.** 어느 소비자도 이 결과로 두 카드를
  같은 세션이라고 보지 않는다. 카드는 여전히 3중키로만 같다.
- 접두 관계가 가르는 것은 두 가지다. **정당한 다중 워크트리**(형제 트리 — 진짜 다른
  파일이고 병합 때 실제로 충돌한다)와 **정규화 미실행 흔적**(조상·자손). 앞의 것은 이
  함수가 손대지 않는다.
- **`CCSessionID` 가 같을 때만 본다.** 앞선 세션이 상하위 17건을 가짜 겹침으로 셌다가
  "전부 다른 대화였다"로 정정한 사고가 있다. 그 17건은 cc 가 달라 이 함수에 애초에
  안 걸린다.
- 빈 `CCSessionID` 는 서로 같다고 보지 않는다. 못 읽음을 값으로 접으면 관측이 깨진
  순간 이 축이 통째로 거짓 초록을 낸다.

표면은 `RenderBoard` 의 `head` 한 줄이다 — 기존 `opt.Notice` 바로 다음, 보드 머리줄 위.
카드 절이 아니라 머리에 두는 이유는 이것이 특정 카드의 성질이 아니라 **이 관측 전체가
낡은 클라이언트에서 왔다**는 사실이기 때문이다.

```
⚠ 대화 N개의 카드가 상하위 경로로 갈렸다 — 그 클라이언트에서 워크트리 정규화(4de4b21)가 안 돈다
```

정규화가 도는 클라이언트는 이 모양을 **만들 수 없다.** 그래서 이 배너가 곧 "어느
클라이언트가 낡았는가"의 증거이고, 클라이언트의 빌드 좌표를 읽지 않고도 성립한다.

#### 여기서 하지 않는 것

`internal/buildinfo` 는 건드리지 않는다. 도는 클라이언트에 vcs 스탬프가 하나도 없는
것(실측: 플러그인 캐시가 git 저장소가 아니라 `go version -m` 에 `vcs.*` 가 0개다)은
미선점 항목 `fd-vcs-stamp-blind-to-worktree` 의 몫이다.

### 3.2 카드 접기 — service 가 계산, render 가 소비

```go
// Conversation 은 같은 대화의 카드 묶음이다.
type Conversation struct {
	CCSessionID string        `json:"cc_session_id,omitempty"`
	Cards       []SessionCard `json:"cards"`
	IsSelf      bool          `json:"is_self"`
	PathCount   int           `json:"path_count"` // 합집합 건수. 목록은 만들지 않는다
	Worktrees   int           `json:"worktrees"`
}

type BoardView struct {
	Sessions      []SessionCard  `json:"sessions"`      // 그대로 둔다
	Conversations []Conversation `json:"conversations"` // 신규
	// … 나머지 기존 필드
}
```

- `Sessions` 를 **안 바꾼다.** `dashboard.json` 계약이 깨지면 그것으로 실측하는 스크립트가
  전부 깨진다 — 이 항목 본문의 재측정 명령도 그중 하나다. 소비자는 자기 속도로 옮긴다.
- 접는 기준은 `judge.sameConversation`(cc 동등) **재사용**이다. 새 판정을 만들지 않는다.
  그 함수는 지금 package-private 이라 노출이 필요한데, **`judge/prescribe.go` 를 열지 않는다** —
  3.1 이 새로 만드는 파일(`judge/split.go`)에 한 줄짜리 공개 껍데기를 둔다.

  ```go
  // SameConversation 은 sameConversation 의 공개 이름이다. 로직은 그쪽 하나뿐이다.
  func SameConversation(a, b string) bool { return sameConversation(a, b) }
  ```

  껍데기를 쓰는 이유는 겹침 때문이 아니라 판정을 한 자리에 두기 위해서다. service 가
  cc 를 직접 비교하면 같은 판정이 두 자리에 살고, 그 어긋남은 어느 화면에도 안 뜬다.
- 빈 cc 는 접지 않는다. 카드 1장짜리 `Conversation` 이 된다.
- `PathCount` 는 **건수만** 낸다. 경로 목록을 합집합으로 내면 "이 대화가 만지는 자리"가
  실제보다 넓어 보인다 — 항목 본문이 직접 경고한 것이다.

렌더(`internal/mcpsrv/render.go`):

- 머리줄 `살아 있는 세션 32건` → `대화 10개(카드 32장)`.
- 기본 보드는 묶음당 요약 한 줄. `선점`은 어느 워크트리의 것인지 `@` 로 붙인다.
- `detail=true` 에서만 워크트리별로 전개한다.
- `rankCards` 는 묶음 단위로 돈다. 형제 하나에 `ask` 가 붙으면 묶음 전체가 그 등급이다.
- `IsSelf` 는 형제 중 하나라도 나면 묶음이 받는다.

```
【기본】
 01KZ71WF… · 대화 1개(카드 5장 · 워크트리 5개) · main · active
   경로 23개(워크트리 5개에 걸쳐) | 선점 fd-landing-order-queue @ .../fd-landing-order-queue
   prompt 38초 · tool 6시간 12분 · mcp 38초
   [ask 1시간 27분] render.go·DESIGN.md 잡은 세 세션에게 — 훅을 앵커…

【detail=true】
 01KZ71WF… · 대화 1개(카드 5장) · main · active
   ├ .../worktrees/fd-landing-order-queue  경로 12: plans/…, specs/… +10
   ├ (루트) kweiza-cc-plugins              경로  7: DESIGN.md, api/ +5
   └ .../plugins/flightdeck/server         경로  4: service/, store/ +2
```

**안 건드리는 render.go 자리:** `renderLane`(01KZ71WF) · `RenderTail` 의 겹침 switch 절과
`noteLines` 본문(d901c6e 가 이미 만졌다) · `RenderPick`.

### 3.3 빈 카드를 안 만드는 조회

`GET /api/v1/sessions?machine=&worktree=&cc=` — 읽기 전용. 없으면 404. **절대 만들지 않는다.**

| 파일 | 변경 |
|---|---|
| `internal/api/api.go` | 라우트 한 줄 순삽입. 앵커 `mux.HandleFunc("POST /api/v1/sessions", s.handleOpenSession)` 바로 아래 |
| `internal/api/handlers_session.go` | 신규 핸들러 하나. 기존 7개 함수 무수정 |
| `internal/service/session.go` | 신규 조회 하나. 기존 갈래 무수정 |
| `internal/store/session.go` | 신규 질의 하나 |
| `cmd/fd/app.go` | `App.FindSession` 하나. `App.OpenSession` 무수정 |
| `cmd/fd/hook.go` | 복구 갈래의 `a.OpenSession(ctx, beacon.CCSessionID, "")` **한 줄** 교체 + 그 위 네 갈래 표 주석 정정 |
| `cmd/fd/client.go` | **안 건드린다** — 범용 `Client.Read(ctx, path)` 를 탄다 |

기각된 안: `Created` 플래그로 돌려막기. 셋째 갈래가 1장에서 2장으로 나빠진다(항목 본문).

### 3.4 ack 분모

`internal/service/prescribe.go` 의 `ackPrescriptions` · `ackedKeys`. 분모를 "발화 전부"에서
**"note 를 부를 수 있었던 카드"**(판단을 하나라도 가진 카드)로 가른다.

**payload 를 바꾸지 않는다.** 처음에 `prescribe_ack` payload 에 적는 안을 적었으나 틀렸다 —
판단이 0인 카드는 애초에 ack 이벤트를 안 남기므로, 분모에서 빼야 할 바로 그 카드들이
payload 에 영영 안 나타난다. 지금 원장만으로 세는 것이 맞다.

```sql
SELECT (SELECT count(DISTINCT e.session_id) FROM event e
        WHERE e.kind='prescribe' AND e.project=?)                        AS emitted,
       (SELECT count(DISTINCT e.session_id) FROM event e
        WHERE e.kind='prescribe' AND e.project=?
          AND EXISTS (SELECT 1 FROM judgment j
                      WHERE j.session_id = e.session_id))                AS reachable,
       (SELECT count(DISTINCT e.session_id) FROM event e
        WHERE e.kind='prescribe_ack' AND e.project=?)                    AS acked;
```

이 질의를 실물 DB 에 돌린 결과(2026-08-05 04:0x UTC):

```
발화 카드 26 · 그중 판단 가진 카드 4 · ack 한 카드 4
지금 분모(26)로 본 확인율 15%  →  고친 분모(4)로는 100%
```

**ack 이 닿을 수 있었던 카드는 전부 ack 했다.** 항목의 논지가 그대로 확인됐다 — 15%는
규율이 아니라 카드 갈림을 재고 있었다.

표면은 `boardDetailFoot`(= `detail=true` 일 때만)이다. 기본 보드의 예산을 안 먹고,
그러면서 **생산 소비자가 있다** — 아무도 안 읽는 계측을 만들지 않는다는 이 저장소의
규율(`TestLandingQueueHasAProductionReader`)에 맞춘다. `/metrics` 는 프로세스 수명
카운터 모형이라 DB 질의를 얹는 자리가 아니다.

3.1 의 탐지는 여기에 직접 실리지 않는다(다른 표면이다). 둘의 관계는 문서로만 잇는다 —
배너가 갈림을 말하고, 이 수가 그 갈림이 확인율에 얼마나 섞였는지를 말한다.

DESIGN §10 에 이 갈라내기를 적는다. §3 헤더의 표 수는 건드리지 않는다
(`fd-design-table-count-confirm` 몫).

**확인율 재측정은 이 브랜치가 하지 않는다.** 배포가 되기 전에는 뜻이 없다(2절). 이
항목은 `done` 이 아니라 재측정 조건을 적은 판단과 함께 넘긴다.

## 4. 데이터 흐름

```
훅 / MCP (클라이언트)                     서버
─────────────────────                    ──────────────────────────────────
resolveProject → worktree                POST /sessions  → 3중키 upsert
   ※ 정규화는 여기서만 일어난다              ※ 값을 검증만 하고 그대로 받는다
   ※ 지금 도는 판에는 그 코드가 없다

hook 복구 갈래                            GET /sessions (신규)
   FindSession → 있으면 rekey                → 있으면 200, 없으면 404
                 없으면 건너뛴다               ※ 어떤 경우에도 만들지 않는다

board                                     Board()
                                            sessionCards()          기존
                                            → Conversations 계산     신규 3.2
                                            → DetectUnnormalizedSplit 신규 3.1
                                          RenderBoard()
                                            머리줄 = 대화 수
                                            배너 = 갈림 보고
```

## 5. 오류 처리

- 탐지가 실패하거나 재료가 없으면 **침묵하지 않는다.** 기존 `derive.fail` 축에 사유를
  남긴다 — 보드는 파생이 통째로 실패해도 응답을 내는 것이 존재 이유이므로 탐지 실패가
  보드를 죽여서는 안 된다.
- `GET /sessions` 의 404 는 오류가 아니라 **사실**이다. 훅의 복구 갈래는 404 를 받으면
  조용히 건너뛴다(지금 upsert 가 만들던 빈 카드가 여기서 사라진다).
- 서버가 안 닿으면 기존 열화 경로를 그대로 탄다. 새 갈래를 만들지 않는다.

## 6. 시험

순수 함수는 표 시험으로 잠근다.

- `DetectUnnormalizedSplit` — 조상·자손이면 보고 · 형제면 **보고 안 함** · cc 가 다르면
  보고 안 함 · 빈 cc 끼리는 보고 안 함 · 머신이 다르면 보고 안 함.
- 접기 — 같은 cc N장이 묶음 1개 · 빈 cc 는 각자 1장 · `IsSelf` 가 묶음으로 올라감 ·
  `PathCount` 가 합집합 건수 · `Sessions` 는 안 변함.
- ack 분모 — 판단 0인 카드가 분모에서 빠짐 · 빈 ack 은 여전히 안 남김.
- `GET /sessions` — 있으면 200 · 없으면 404이고 **행이 안 생김**(질의 뒤 카드 수 단정).

배선은 **운영 진입점을 그대로 타는** 시험으로 잠근다. 시험이 구조체를 직접 조립하면
"정말 배선됐나"를 원리적으로 못 본다 — `TestHookAndMCPAgreeOnWorktreeFromSubdir` 이
남긴 교훈이다.

그리고 **각 수정을 되돌려 빨강을 확인한 뒤에** 초록이라고 말한다. 대조 단정을 함께
건다 — 축을 통째로 꺼도 초록이 나오지 않게.

검증 명령: `gofmt` · `go build` · `go vet` · `go test ./... -race` · darwin/arm64 ·
windows/amd64 교차 빌드 · 살아 있는 브랜치와 `merge-tree` 무충돌.

## 7. 안 하는 것 (연결된 판단이 이미 못박았다)

- **소급 병합(원장 이관).** 3중키 UNIQUE 충돌 해소 경로가 설계에 없고(07e5df4 가 일부러
  안 만들었다) 원장 4종 이관이 필요해 200줄이 넘는다. 이 브랜치는 새로 갈리는 것을
  막고, 이미 갈린 것은 **표시로만** 접는다.
- **관문의 기준 트리를 프로젝트 루트로 바꾸기.** 4530e3c 가 사유 셋과 함께 기각했다.
- **접두 일치를 정체·겹침 판정에 쓰기.** 3.1 의 보고 전용 울타리 밖으로 나가지 않는다.
- **`resolveProject` 재수정.** 이미 옳다(2절).
- **`internal/buildinfo`** — `fd-vcs-stamp-blind-to-worktree` 몫.
- **DESIGN §3 표 수** — `fd-design-table-count-confirm` 몫.
- 남의 미랜딩 자리: `service/pick.go` · `store/judgment.go` · `judge/bundle.go`(01KZ7FR2) ·
  `store/store.go` · `store/probe.go` · `api/handlers_meta.go` · `cmd/fd/client.go`(01KZ71DM).

## 8. 겹침 실측과 랜딩 순서

이 브랜치를 열며 받은 화면 겹침 26줄을 전수로 갈랐다. **미랜딩 브랜치는 둘뿐이다.**

```
★미랜딩  fd-pick-bundle          judge/bundle.go(신규) · service/pick.go · store/judgment.go
★미랜딩  fd-server-self-restart  cmd/fd/{client,main,render,selfcheck,selfwatch*,serve}.go
                                 api/{api,handlers_meta}.go · store/{probe,store}.go
랜딩됨   fd-board-size-not-window-bound · fd-containment-gate-only-on-one-of-three-doors
         fd-finish-lets-followups-fall-on-the-floor · fd-itempath-cause-and-wire
         fd-live-window-baseline · fd-prescribe-false-positive · fd-session-identity-axis
```

진짜 교집합은 `internal/api/api.go` 한 파일이고 양쪽 다 순삽입이며 자리도 다르다.

`internal/judge` 는 양쪽 다 **새 파일만** 만든다 — 저쪽은 `bundle.go`, 이쪽은 `split.go`.
기존 `judge/prescribe.go` · `judge/eligible.go` 는 어느 쪽도 안 연다(3.2 의 공개 껍데기를
`split.go` 에 두는 이유가 이것이다).

가장 깨끗한 오탐: `fd-legacy-apply-bypasses-path-gate` 워크트리를 발자국으로 가진 세션
넷이 계속 겹침으로 뜨는데, `git rev-parse --verify` 가 `fatal: Needed a single revision`
을 낸다 — **브랜치가 존재하지 않는다.**

랜딩 순서는 `fd-server-self-restart` 가 먼저다. 그쪽이 크고 이쪽 `api.go` 는 한 줄이라
리베이스 비용이 이쪽에서 싸고, 그 축이 여는 서버 재기동이 3.1 탐지가 실제로 도는
조건이다.

## 9. 완료 판정

| 항목 | 완료의 모양 |
|---|---|
| `fd-session-worktree-is-cwd-not-repo-root` | 탐지가 랜딩하고, 배포 간극이 판단으로 기록된다. `resolveProject` 는 안 고친다 |
| `fd-session-lookup-without-upsert` | 조회가 서고, 복구 갈래가 그것을 타고, 그 갈래에 시험이 닿는다 |
| `fd-board-counts-one-conversation-many-times` | 머리줄이 대화 수를 내고 `detail` 이 워크트리별로 전개한다 |
| `fd-ack-metric-measures-card-split` | 분모가 갈라지고, 재측정 조건을 적은 판단과 함께 넘긴다(`done` 아님) |
