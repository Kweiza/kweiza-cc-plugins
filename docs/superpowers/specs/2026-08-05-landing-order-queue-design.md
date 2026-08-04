# 랜딩 순서 큐 — Tier B 조각 ① 설계

작성 2026-08-05 · 상태 **승인됨**(브레인스토밍 2026-08-05) · 항목 `fd-landing-order-queue`

선행 판단: `01KZ5CEWGDCGME5557THWS74QC`(가짜 장벽 제거) · `01KZ5CA0TFD2K82ZQGNNSVF2Y0`(session_workspace 전례) ·
`01KZ74K61HYAF1V3SYW5SB4XGM`(이 설계 중 나온 판정)

---

## 문제

서버가 "지금 랜딩해도 된다"를 **순서대로** 내준다. 검증은 세션이 자기 자리에서 돌리고 결과를 보고한다.
`landing`·`docs-land` 락의 경합이 여기서 사라진다.

DESIGN §2 Tier B 표의 ① 이고, 선행 넷(러너·cosign·도커 소켓·GHE push)을 **하나도 안 쓴다.**

---

## 조사에서 나온 제약 — 설계를 실제로 바꾼 것들

### ① 항목의 전제가 코드와 달랐다 — `resource_hold` 에는 진입점이 없다

항목 본문은 "이미 있는 배타에 순서를 붙인다 / 지금은 못 잡으면 그냥 거절이고 세션이 알아서
다시 시도한다"고 적었다. **거절되는 재시도가 없다.** 전수 확인:

| 함수 | 프로덕션(비시험) 호출자 |
|---|---|
| `store.AcquireResource` | **0건** |
| `store.ForceReleaseResource` | **0건** — 대시보드 "선점 회수" 버튼이 부르는 것은 `ForceReleaseClaim`(항목 선점)이다 |
| `store.ReleaseResource` | `service/finish.go:184` 하나. 쥔 게 없으니 항상 무동작 |
| `store.ListHeld` | `service/board.go:155` · `service/pick.go:440`. 항상 빈 목록 |
| `judge.RejectResourceHeld` | `Candidate.Needs` 가 `.flightdeck.yaml` 에서 오는데 **그 파일이 이 레포에 없다** |

MCP 도구는 여섯(`board·pick·note·add·finish·alloc`)이고 CLI 에도 자원 명령이 없다.
`landing`·`docs-land` 경합은 fd 안이 아니라 fd 가 대체하려는 **레거시 셸 락**에 있었다.

**따라서 ①은 "순서를 붙인다"가 아니라 배타를 처음 켜면서 순서까지 붙이는 것이다.**
DESIGN §2 의 "①은 선행 넷을 하나도 안 쓴다"는 여전히 참이고, 크기 판정만 틀렸다.

### ② 대시보드의 쓰기 버튼 둘이 지금 400 이다 (이 항목과 무관한 기존 결함)

`api.go:214-221` 이 화면을 `Fallback` 으로 mux **안에** 넣어 게이트 사슬을 전부 타게 한다.
그건 의도된 수정이다 — 앞선 판이 바깥 mux 에 붙였다가 토큰을 켠 배포에서 **무인증 폐기가 실제로 성공**했다
(`cmd/fd/serve.go:80-83`).

그 사슬의 `withIdempotency`(`api/middleware.go:219-231`)는 모든 쓰기에 `Idempotency-Key` 헤더를 요구한다.
그런데 `web/dashboard.gohtml:138`(선점 회수) · `:220`(항목 폐기)은 평범한 `<form method="post">` 이고,
그 파일의 `<script>` 둘은 SSE 갱신과 접기다 — 헤더를 실을 자리가 없다.

**두 버튼은 누르면 400 `idempotency_key_required` 다.**
`web/actions_test.go` 는 사슬 **밖**의 `web.New` 핸들러를 눌러 초록이라 이 축을 원리적으로 못 본다.

랜딩 레인 회수가 같은 벽에 부딪히므로 이 항목이 함께 고친다. 안 고치면 "물린 레인을 사람이 푸는 길"이
만들자마자 죽는다.

### ③ 검증을 락 밖에 두면 검증이 아무것도 증명하지 못한다

DESIGN §0 병목 2번은 레거시 랜딩 락이 너무 넓다고 지적했다("검증 11단계 중 이미지 태그를 쓰는 것은
4단계뿐인데 전 구간이 잠긴다"). 그러나 머지 큐에서 검증을 락 밖으로 빼면 두 세션이 각자 **자기 브랜치만**
검증하고 둘 다 병합해 main 이 깨진다.

그래서 레인 보유 구간은 `rebase → 검증 → 병합 → push` 전 구간이다. 실측 ρ≈0.05 도 **바로 그 구간**을 잰
값이므로 이 선택이 발산을 만들지 않는다.

### ④ `session_workspace` 전례

쓰기만 있고 읽는 코드가 0건인 표는 나중에 "그 축은 이미 있다"의 **거짓 근거**가 된다
(`store/session.go:316-337`, 판단 `01KZ5CA0TFD2K82ZQGNNSVF2Y0`). 이 설계는 그 함정을 두 방향으로 피한다 —
읽는 쪽을 같은 커밋에 넣고, 읽는 쪽이 사라지면 빨개지는 시험을 둔다.

---

## 확정한 결정

| 축 | 결정 | 사유 |
|---|---|---|
| 범위 | 취득 + 순서 + 결과 보고 + 반납을 한 덩어리로 | 취득 없는 순서는 아무도 안 서는 줄이다(제약 ①) |
| 레인 단위 | 프로젝트당 하나, 이름은 서버가 `landing` 으로 고정 | `.flightdeck.yaml` 로더가 선행이 되면 이 항목보다 커진다. 문서 레포는 이미 별도 프로젝트라 프로젝트 축이 `docs-land` 를 이미 가른다 |
| 보유 구간 | `rebase → 검증 → 병합 → push` 전 구간 | 제약 ③ |
| 굶주림 | 검증 실패 = 즉시 반납 + 다시 서면 **맨 뒤** | 사람이 버그를 고치는 무제한 시간 동안 남이 막히지 않는다. ρ=0.05 라 다시 서는 비용이 사실상 0 |
| 순서 표현 | `landing_queue.id`(AUTOINCREMENT) | 발번기는 삽입이 거절되면 번호만 소각되는데 회수 함수가 없다. `id` 는 같은 INSERT 안에서 원자적이다 |
| 배타 | 기존 `resource_hold` 그대로 | 배타의 정본은 부분 유니크 인덱스 `resource_one_holder` 다. 건드리지 않는다 |
| 회수 대상 | 레인이 아니라 **줄 행** | 대기 중 좀비도 같은 문법으로 빠져야 한다. 안 그러면 유일한 탈출구가 만료 기구가 된다 |
| 회수 표면 | **CLI + 웹**. MCP 는 거절 | `mcpsrv.go:565-575` 가 `pick` 의 `steal_reason` 을 이미 거절한다. 한 서버가 선점 회수는 거절하고 레인 회수는 허용하면 그 문구가 화면에서 거짓이 된다 |
| 차례 통지 | 처방(`Prescribe`)에 `lane-turn` 키 추가 | 새 통로를 안 만든다. Stop 훅이 매 턴 끌어가는 그 통로가 이 레포의 유일한 세션 단위 push 다 |
| 자동 회수·만료 | **없다** | `store/resource.go:14-19` 의 규율. 실측으로 두 번 틀렸다 |

---

## 설계

### 1. 데이터 모델

★ **`schema.sql` 을 고치지 않는다.** 그 파일은 기반 한 판이고 그 위의 변경은
`internal/store/migrations/NNN_*.sql` 에 증분으로 쌓는다(`schema.sql:1-6` 이 명시적으로 금지한다 —
"신규용으로 여기에 새 표를 또 적으면 정의가 두 벌이 되고, 그때 신규 설치와 업그레이드가
다른 모양의 DB 를 갖는다"). 표를 더한 유일한 선례 커밋 `523b21d` 의 diff 가 그것을 증명한다 —
`002_idempotency.sql` 을 새로 만들었고 `schema.sql` 은 머리말 주석만 늘었다.

그래서 새 표는 **`internal/store/migrations/003_landing_queue.sql`** 이고,
`store.go` 세 자리를 함께 고친다(`//go:embed` 변수 · `SchemaVersion 2→3` · `migrations` 슬라이스).
`TestFreshInstallAndUpgradeProduceTheSameSchema` 가 이 규율을 지킨다.

```sql
CREATE TABLE landing_queue (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,   -- 이것이 순번이다
  project     TEXT NOT NULL REFERENCES project(id),
  session_id  TEXT NOT NULL REFERENCES session(id),
  enqueued_at TEXT NOT NULL,
  left_at     TEXT,
  left_kind   TEXT,        -- ok | fail | leave | finish | force
  left_detail TEXT,
  CHECK ((left_at IS NULL) = (left_kind IS NULL)),
  CHECK (left_kind IS NULL
         OR left_kind IN ('ok','finish')
         OR (left_detail IS NOT NULL AND left_detail <> ''))
);

CREATE UNIQUE INDEX landing_queue_one_live_per_session
  ON landing_queue(project, session_id) WHERE left_at IS NULL;

CREATE INDEX landing_queue_waiting
  ON landing_queue(project, id) WHERE left_at IS NULL;
```

**두지 않는 컬럼과 그 이유** — 이 절이 이 설계에서 가장 중요하다.

- **`granted_at` 없음.** "내가 레인을 쥐었나"는 `HeldBy(project,"landing").SessionID == me` 로만 파생한다.
  배타의 정본이 이미 DB 제약인데 두 번째 사본을 만들면 갈릴 자리가 생기고, 갈렸을 때 어느 쪽이 참인지
  정하는 문장이 없다. 사본이 없으면 표류가 원리적으로 불가능하다.
- **`seq` 없음.** `id` 가 순번이다. 별도 발번은 회수 불가능한 번호를 태운다.
- **`item_id` 없음.** 세션의 선점에서 파생 가능하고(보드가 이미 잇는다), 읽는 쪽 없는 컬럼은
  제약 ④ 의 함정이다. 랜딩 이력은 `judgment_link` 로 잇는다.
- **`left_reason` 을 한 컬럼에 안 뭉갠다.** `force:<사유>` 접두 파싱은 `api/idempotency.go:111-119` 가
  이미 기각한 방식이다. 종류는 CHECK 로, 사유는 별도 컬럼으로 둔다.

`left_kind` 가 `ok`·`finish` 가 아닐 때 `left_detail` 을 필수로 만드는 CHECK 가 "사유 없는 회수는
되짚을 수 없다"를 산문이 아니라 제약으로 만든다(`item.state='dropped'` → `close_reason` 이 그 본이다).

### 2. 상태 전이 — `BEGIN IMMEDIATE` 한 트랜잭션 안에서

DSN 이 `_txlock=immediate` 라 `land` 트랜잭션끼리 직렬화된다(`store/store.go:100-113`).

**`land()` — 인자 없음**

1. 내 살아 있는 줄 행이 없으면 INSERT
2. `SELECT MIN(id) FROM landing_queue WHERE project=? AND left_at IS NULL` 이 내 id 이고 레인이 비었으면
   `AcquireResource(project,"landing",me)` → **"네 차례다"**
3. 아니면 → **"너는 N번째"** + 점유자 · 획득 경과 · **점유자의 마지막 신호 나이**

★ **`ResourceHeldError` 는 오류가 아니라 정상 결과다.** 삼켜서 "N번째"로 바꾸고 트랜잭션은 커밋한다.
여기서 롤백하면 줄 행과 순서 자체가 함께 사라져 큐에 영원히 한 명만 남는다.

★ **순서 집행 지점은 2번의 `MIN(id)` 비교 하나다.** 이 조건이 없으면 순번은 표시용이 되고
"순서 큐"라는 이름이 거짓이 된다.

**차례를 미는 주체는 다음 호출이다(지연 부여).** 서버가 먼저 남의 이름으로 자원을 잡지 않는다.
그래서 깨우는 통로가 따로 필요하고, 그것이 처방이다(아래 4).

**`land(result:"ok"|"fail", detail:"…")`**

- **먼저 내가 아직 점유자인지 본다.** 아니면 "네 레인은 〈사유〉로 회수됐다"로 답한다 —
  줄 행은 이미 `force` 로 닫혀 있고, 회수를 `ok` 로 덮어쓰지 않는다.
- 점유자면 `ReleaseResource` + 줄 행 닫기를 같은 트랜잭션에서.
- `fail` 은 `detail` 이 필수다(CHECK). "다시 서면 통과할 종류인가"에 다음 사람이 답할 수 있어야 한다.

★ **`ok` 는 "랜딩됐다"가 아니다.** `item.landed_ref` 도 랜딩 이력도 이 값으로 채우지 않는다.
DESIGN §5 는 랜딩 sha 의 출처를 "러너가 실제로 ff 한 sha"로 못박았고, 클라이언트 자기 보고를 그 자리에
넣으면 "메인 트리의 지금 HEAD 를 적어 남의 커밋이 이 항목의 랜딩 sha 로 박힌" 결함(3회 관측)이
이름만 바꿔 부활한다. 화면 문구도 "랜딩 완료"가 아니라 **"세션이 ok 로 보고하고 레인을 놓았다"** 다.

**`land(leave:"사유")`** — 내 살아 있는 줄 행을 닫는다. **레인 미보유여도 성립한다.**
줄 서 놓고 포기한 세션이 스스로 빠지는 유일한 길이다.

**`land(release:…)`** — **거절.** `pick` 의 `steal_reason` 과 같은 문구 모양 +
"회수는 사람이 화면이나 `fd lane release` 로" 처방.

### 3. 회수 — 대상은 줄 행이다

서비스 함수 하나:

```go
func (s *Service) ReleaseLaneRow(ctx, project string, rowID int64, reason string) (LaneReleaseResult, error)
```

`ForceReleaseResource`(점유가 있을 때만) 와 줄 행 닫기(`left_kind='force'`, `left_detail=reason`)를
**같은 트랜잭션**에서 한다. 웹과 CLI 가 **둘 다 이 함수를 부른다** — 규칙이 두 벌이 되는 자리를 안 만든다.

대상은 **줄 행 id 하나**로 받는다. 세션 id 로 받지 않는다 — 한 세션은 줄 행 이력을 여럿 갖고,
그러면 "어느 것을 뺄까"가 다시 판정이 된다. 그 번호는 보드와 대시보드가 이미 내고 있다.

대상이 줄 행이라 점유 중이든 대기 중이든 같은 문법으로 빠진다. 이것이 "죽은 선두가 큐를 영구히 막고
유일한 탈출구가 만료 기구가 된다"를 막는 자리다.

**자동 만료는 만들지 않는다.** 화면과 응답은 나이를 **숫자로만** 낸다. 판정은 사람이 한다.

★ **`fd lane release` 는 1단계에 들어간다.** 원래 회수 표면 전체를 2단계로 미루려 했는데,
그러면 1단계만 랜딩한 상태에서 **물린 레인을 푸는 길이 하나도 없다.** 오늘 자원 점유를 푸는
프로덕션 표면이 0건이고(`AcquireResource`·`ForceReleaseResource` 둘 다 호출자 0건),
자동 만료도 없고, 세션 정체가 `(machine, worktree, cc_session_id)` 라 **죽은 세션 명의로
`land(leave)` 를 부를 방법이 없다.** 레인은 프로젝트당 하나뿐이라 한 번 물리면 그 프로젝트의
랜딩이 전원 정지하고, 복구 수단이 `sqlite3` 직접 UPDATE 뿐이 된다 — 판단 한 줄 안 남는 경로다.

CLI 한 줄기만 앞당기면 탈출구가 성립한다(REST 라우트 하나 + `cmd/fd` 서브명령 하나 + `wire.go`
요청 타입 하나). 웹 폼·미들웨어·출처 대조·화면 레인 절은 2단계에 그대로 남는다.

★ **두 표가 어긋난 상태를 잡는 시험을 둔다.** `resource_hold(resource='landing')` 의 살아 있는 점유와
`landing_queue` 의 살아 있는 선두 행은 같은 사실을 표현한다. 어긋나면(행은 닫혔는데 hold 가 남는다)
`ListLandingQueue` 는 아무도 안 보여 주는데 레인은 영영 잡혀 있다.
"살아 있는 landing hold 가 있으면 반드시 대응하는 살아 있는 줄 행이 있다"를 시험으로 잠근다.

회수는 `judgment`(kind=`decision`)도 함께 남긴다(`web/actions.go:149-154` 가 선점 회수에서 이미 그렇게 한다).
**그 판단은 `left_detail` 의 사본이 아니다** — `left_detail` 은 사람이 친 한 줄이고, 판단은 거기에
서버가 관측한 것(점유 경과·마지막 신호 나이·그때 줄에 있던 사람)을 더한 넓은 기록이다.
담는 것이 다르므로 "어느 쪽이 정본인가"가 생기지 않는다.

### 4. 차례 통지

`judge/prescribe.go` 의 키에 `PrescribeLaneTurn = "lane-turn"` 을 더한다.
처방은 상태가 아니라 **전이**에서만, `(세션 × Key)` 당 1회 뜬다.

**억제 키에 줄 행 id 를 넣는다**(`lane-turn:<row id>`). 안 넣으면 한 번 차례를 받고 실패해 다시 선
세션에게 두 번째 차례가 영영 안 뜬다.

이것이 없으면 폴링이 기본값이 되고, `land` 는 POST 라 DESIGN §10 의 "세션당 쓰기 호출 수"를
대기 시간에 비례해 태운다.

### 5. `finish` 와의 접합 — 이 항목이 살리는 죽은 코드

`land` 가 `AcquireResource` 의 첫 프로덕션 호출자가 되는 순간 `service/finish.go` 의 ④ 가 살아난다.
셋을 함께 고친다:

1. **줄 행 인지로 만든다** — `landing` 을 반납할 때 같은 트랜잭션에서 그 세션의 살아 있는 줄 행을
   `left_kind='finish'` 로 닫는다. 안 고치면 랜딩 후 마무리한 세션이 유령 행을 남기고,
   유니크 인덱스 때문에 **영영 다시 줄을 못 선다.** 뒤 전원은 그 유령 선두에 막힌다.
2. **멱등하게 만든다** — 이미 남이 회수해 점유가 없으면 오류를 올리지 말고 `Released` 에서 빼고
   원장에만 남긴다. 지금은 `ErrNotFound` 가 그대로 올라가 **핸드오프 판단이 통째로 롤백된다** —
   이 레포가 "원리적으로 파생 불가한 유일한 자산"이라 부르는 값이다.
   (`store/item.go:684-686` 의 `ReleaseClaim` 이 이미 같은 규율이다.)
3. **`holds` 읽기를 트랜잭션 안으로 옮긴다**(`finish.go:131`).

### 6. 열화·오프라인

**`land` 는 전 가지가 POST 이고 어떤 응답도 캐시에 안 들어간다.**

`Client.Read`(`cmd/fd/client.go:194-222`)는 `JudgeOffline` 을 한 번도 안 보고, 성공한 GET 을 조건 없이
캐시하며 미도달이면 조건 없이 꺼낸다. "내 자리 재출력"을 GET 으로 만들면 서버가 죽은 뒤
**30분 전의 "네 차례다"가 그대로 나오고 세션은 레인을 안 쥔 채 랜딩을 시작한다** —
배타가 깨지는 게 아니라 우회된다.

선례가 있다: `Healthz`(`client.go:312-315`)는 `c.do` 로 직행하고 캐시를 안 쓴다 —
*"'서버가 살아 있나'에 캐시로 답하면 그 질문 자체가 무의미해진다."*

`JudgeOffline`(`cmd/fd/offline.go:54-88`)에 **각각 다른 사유로** 넣는다:

| 가지 | 처방 | 사유 |
|---|---|---|
| 취득 | 거절 | 배타의 정본이 서버의 DB 제약이라 오프라인에 성립할 수 없다 |
| 반납·이탈 | 거절 | 재생 시점에 이미 남이 잡았을 수 있다 — 남의 점유를 반납하게 된다 |

사유를 뭉개면 다음 사람이 반납만 아웃박스로 연다.

**아웃박스 방어를 명시적으로 만든다.** 지금 `offline.go:56-58` 의 아웃박스 가지에 `"land"` 한 낱말을
끼워 넣으면 그대로 아웃박스로 가고, 막는 코드가 없다. 순수 함수
`OutboxEligible(cmd, path string) (bool, reason string)` 을 두고 `client.go:262` 진입 직전에 통과 못 하면
거절로 접는다. 그리고 "아웃박스 적격 집합 == {note}, 적격 경로 == `/api/v1/judgments`" 를 단정하는
시험을 붙인다.

멱등 키는 `FreshKey` 다(`IdempotencyStable` 에서 false) — 응답에 "지금 상태"가 실리므로 `pick` 과 같다.

### 7. 정체 게이트

`sessionBoundTools`(`mcpsrv/identity.go:174-176`)에 `"land": true` 를 넣는다.
**표에 없는 도구는 거절이 아니라 통과다**(`GateTool` 의 `if !sessionBoundTools[tool] { return true, … }`).
`land` 는 `resource_hold.session_id` 와 `landing_queue.session_id` 로 원장에 행을 남기므로 반드시 넣는다.
빼먹으면 세션 좌표 없이 레인을 잡고, 실패는 "정체가 없다"가 아니라 FK/CHECK 위반으로 엉뚱하게 나온다.
**어떤 시험도 안 깨지므로 조용하다.**

함께 움직여야 거짓말이 안 되는 자리: `identity.go:144` 배너의 "안 되는 것" 목록,
`mcpsrv.go` 패키지 주석의 같은 목록.

### 8. 웹 400 을 고치는 방법과 그 대가

`<form>` 이 헤더를 못 실으니 **폼 action 의 쿼리에 키를 싣고 얇은 미들웨어가 헤더로 올린다.**
본문을 안 건드리므로 `withIdempotency` 의 본문 읽기와 안 부딪힌다.
키는 렌더 시각에 서버가 만든다(`web:<대상>:<렌더 unix>`) — 더블클릭은 같은 키라 접히고,
새로고침하면 새 키라 다시 눌린다. 멱등이 원래 원하는 의미 그대로다.

**대가를 명시한다.** 이 레포엔 CSRF 토큰·SameSite·Origin 검사가 **0건**이고, 지금은
`Idempotency-Key` 헤더 요구가 **우연히** 그 역할을 하고 있다 — 외부 사이트의 폼은 헤더를 못 싣는다.
쿼리로 우회하는 순간 그 우연한 방어가 사라진다.
그래서 **화면 액션 경로 한정으로 `Sec-Fetch-Site`/`Origin` 대조를 같은 커밋에 넣는다.**
없애는 것을 대체물 없이 없애지 않는다.

새 `ActionKind` 는 `lane` 계열 이름을 피한다 — `JudgeAction`(`web/actions.go:59-60`)이
`"lane"·"lane-stop"·"lane-resume"` 를 **이름으로 알고 거절**하고 그 거절은 여전히 참이다
(레인 정지/재개는 Tier B). `lane-release` 를 통과 목록에 더하고, 거절 문구를
"정지/재개는 여전히 Tier B, 회수는 열렸다"로 가른다.

### 9. 읽는 쪽 — 같은 커밋에 들어간다

제약 ④ 를 피하는 자리다. `landing_queue` 를 읽는 표면 셋:

1. **보드**(MCP) — 랜딩 레인 절: 점유자 · 획득 경과 · 대기 줄 전체(순번 · 세션 · 대기 경과 · 마지막 신호 나이)
2. **`land` 응답** — 내 순번과 앞사람
3. **웹 대시보드** — 같은 줄과 회수 버튼

**`pick` 꼬리는 이번 범위에서 뺀다.** 지금 두 세션(`01KZ71DM`·`01KZ7214`)이 `pick.go`·`render.go` 를
정면으로 만지고 있어 세 번째가 끼면 리베이스 충돌이 확정적이다. 읽는 쪽 요건은 위 셋으로 이미 충족된다 —
후속 항목으로 뺀다.

웹 대시보드는 ④ 랜딩 이력 안쪽 `<h3>` 로 넣는다(새 `<section>` 은 `render_test.go:176` 의 절 개수 6을 깬다).
줄은 **전부** 낸다 — 생존 창으로 거르지 않는다(`web/page.go:315-316` 이 같은 자리에서 이미 그렇게 정했다).
0건 문장은 자기 절이 따로 가진다("랜딩 레인을 쥔 세션이 0건이다(질의는 돌았다)") —
`blockedPanel` 이 자원 0건을 패널 전체 Empty 한 줄에 뭉개는 모양을 베끼지 않는다.

점유자의 마지막 신호 나이를 내려면 **창 밖 세션도 답하는 접근자**가 필요하다 —
`ListLive` 는 창 밖 점유자를 통째로 빠뜨린다. `store.Signals` 를 감싼 함수 하나를 추가한다.

### 10. 문서와 시험 락 — 숫자가 코드가 아니라 판정인 자리

| 자리 | 지금 | 이 항목 뒤 | 잠근 것 |
|---|---|---|---|
| MCP 도구 수 | 6 | **7** | `mcpsrv/protocol_test.go:62`(이름·순서 하드코딩, 설명 90자 상한), `mcpsrv/add_coordinate_test.go:65` |
| 웹 버튼 수 | 4 | **5** | `web/render_test.go:365` POST==2, `:369` reason required==2, `:373` Tier B 비활성 문자열 |
| DESIGN 테이블 수 | 12(실제 **23**) | **24** | 없음 — 그래서 표류했다 |

★ **테이블 수는 세는 축을 함께 적지 않으면 반드시 다시 표류한다.** 실측(판단 `01KZ7DKQ3QHKH75X4XY0YDPFMC`):

- 사람이 선언한 것 = **23** — `schema.sql` 의 `CREATE TABLE` 21 + `CREATE VIRTUAL TABLE` 1(`judgment_fts`, `schema.sql:264`) + 증분 `002` 의 `idempotency` 1
- 살아 있는 DB 의 `sqlite_master` = **28** — 위 23 에 FTS5 그림자 넷(`judgment_fts_config`·`_data`·`_docsize`·`_idx`)과 `sqlite_sequence` 가 더해진 값

**그 다섯은 엔진이 만든 것이라 데이터 모델이 아니다.** §3 은 데이터 모델을 말하는 절이므로 선언 수를 적는다.
`landing_queue` 를 더하면 선언 **24** · `sqlite_master` **29** 다. 두 수가 왜 다른지를 §3 에 함께 적는다 —
안 적으면 다음 사람이 `sqlite_master` 를 세고 또 어긋났다고 판단한다(실제로 다른 세션이 "28개"로 관측해 넘겨 왔다).

DESIGN 줄: `:58`, `:156`, `:290`, `:303`, `:358`, `:367`.

두 시험 모두 실패 문구로 "DESIGN.md §6 을 함께 고쳐라"를 요구한다. **문서를 먼저 고친다** —
안 그러면 첫 커밋이 "시험이 시키는 대로 문서를 몰래 고친 커밋"이 된다.
도구를 7개로 올리는 근거(§6 의 컨텍스트 예산 재계산)와 버튼을 다섯으로 여는 근거를 문장으로 적는다.

`web/format.go:1-16` · `web/actions.go:14-18` · `web/web.go:95-97` 의 독스트링 셋이
"쓰기는 넷 / 라우트는 셋"이라 적고 있어 버튼을 더하는 순간 셋 다 거짓이 된다. 같이 움직인다.

§3 테이블 수는 열린 항목 `fd-design-table-count-drift` 와 겹친다 — 판단 `01KZ74PF3RPG9ZFHHJ9YGHM9BK`
로 알렸다. 이 커밋이 실제 개수로 고치고 그 항목은 랜딩 뒤 확인하고 닫는다.

---

## 시험 — 무엇을 잠그나

**상태 전이 (store·service)**

- 두 세션이 동시에 `land()` → 하나는 점유, 하나는 "2번째". **둘 다 줄 행이 남는다**
  (`ResourceHeldError` 를 롤백으로 접지 않았다는 단정)
- 맨 앞이 `ok` 로 빠진 뒤 2번째가 `land()` → 그 자리에서 부여된다
- 맨 앞이 `fail` 로 빠지고 다시 서면 **맨 뒤**다
- 회수된 세션이 `land(result:"ok")` → "회수됐다"로 답하고 줄 행은 `force` 로 남는다(`ok` 로 안 덮인다)
- **대기 중** 줄 행을 회수할 수 있다(점유가 없어도 성립)
- `leave` 는 레인 미보유 상태에서 성립한다

**접합부 — 이 항목이 살리는 죽은 코드**

- 레인을 쥔 채 `finish` → 자원이 반납되고 **줄 행도 닫힌다.**
  단정은 "자원이 반납됐나"가 아니라 **"그 세션이 다시 줄을 설 수 있나"** 다
- `ListHeld` 와 트랜잭션 사이에 강제 회수를 끼우고 `finish` → **판단 행이 남아 있다**
  (오늘 이 시험을 쓰면 빨갛다)

**표면**

- 오프라인 `land` 전 가지가 거절이고 **아웃박스에 안 샌다**
  (`cmd/fd/degrade_path_test.go` 방식으로 경로 단정)
- 조립된 서버(`api.NewServer` + Fallback)를 눌러 웹 회수가 **200**
  (`cmd/fd/auth_gate_test.go` 방식). **이 시험을 기존 버튼에 붙이면 지금 빨갛다** — 그것이 400 결함의 증거다
- `GateTool("land", 정체 없음)` 이 거절한다
- 화면 액션에 `Origin` 대조가 걸린다(외부 Origin POST 는 거절)

**만료 통지형**

- `landing_queue` 를 읽는 프로덕션 호출자가 0이 되면 빨개지는 시험.
  `session_workspace` 는 이 시험이 없어서 거짓 근거가 됐다

---

## 범위 밖 — 일부러 안 하는 것

- **`.flightdeck.yaml` 의 `resources:` 를 읽지 않는다.** 파일 자체가 이 레포에 없어 로더가 선행이 되는데
  그건 이 항목보다 크다. 레인 이름은 서버가 고정한다.
- **`land result:"ok"` 를 랜딩 사실로 승격시키지 않는다.** 랜딩 sha 는 여전히 러너(Tier B ②③)만 쓴다.
- **세션 死 판정을 만들지 않는다.** 나이는 숫자로만 낸다.
- **Tier B 의 나머지 셋(②③④)은 이 항목이 아니다.** ①②③④ 를 한 덩어리로 다시 제시하지 마라 —
  그 제시 방식이 착수를 막은 원인이었다(DESIGN §2).
- **`job` 표를 안 건드린다.** 레인 점유자는 세션만이다. `Holder` 의 `JobID` 축은 Tier B ② 이후다.
