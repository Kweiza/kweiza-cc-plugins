# cc 표류 — 훅이 현재 cc_session_id 를 MCP 에 실어 오는 통로 설계

작성 2026-08-05 · 상태 **승인됨**(브레인스토밍 2026-08-05) · 큐 항목 `fd-cc-drift-hook-channel`
선행 판단 `[handoff] 2026-08-04 02:49` (표류 실측 · 처방 ① 기각) · 선행 커밋 `ca248c2`

## 문제

`/clear`·compact·재개로 대화의 `cc_session_id` 가 갈리면 **같은 창이 보드에 카드 두 장으로 뜬다.**
MCP 프로세스는 기동 시 주입된 옛 cc 를 계속 쓰고, 훅은 매번 새 프로세스라 새 cc 를 본다.

직전 작업이 이것을 실측하고(`mcpsrv/drift.go`) 보드에서 **이름으로 말하는 데까지** 갔다.
남은 것은 따라가기이고, 그것이 이 설계다.

직전 판단이 처방 하나를 이미 지웠다. **"MCP 가 도구 호출마다 cc 를 다시 읽는다"는 no-op 이다** —
리눅스에서 프로세스의 `environ` 은 exec 뒤 바뀌지 않는다. 구현하면 초록인 채로 아무 일도 안 한다.
그 사실은 `drift.go` 머리에 근거와 함께 박혀 있고 이 설계도 그것을 되살리지 않는다.

## 조사에서 나온 제약 (설계를 실제로 바꾼 것들)

2026-08-05 이 머신에서 실측했다. 네 가지가 나왔고 그중 둘은 항목이 세운 전제를 바꿨다.

### ① 통로는 이미 있다 — 훅과 MCP 는 같은 상태 디렉토리를 본다

항목은 "상태 디렉토리가 전역이라 창끼리 덮는다"만 적었지만, 그보다 앞선 미확인은
"훅과 MCP 가 같은 자리를 보긴 하나"였다. 자연 실험으로 답이 나왔다 —
이 대화의 **훅이 쓴** `local-session-5fa98806…` 과 **MCP 가 쓴** `local-session-8210a1c6…` 이
같은 디렉토리에 있다:

```
~/.claude/plugins/data/flightdeck-kweiza-cc-plugins/flightdeck/cache/
```

`CLAUDE_PLUGIN_DATA` 가 훅·MCP 두 채널 모두에 온다. 파일 통로 자체는 새로 팔 필요가 없다.

### ② 그 자리는 머신 전체가 공유한다

```
fd mcp × 6, 프로젝트 5개 (stt-meetings · figma-agent · kweiza-cc-plugins×2 · context-platform)
전부 CLAUDE_PLUGIN_DATA=/home/aaron/.claude/plugins/data/flightdeck-kweiza-cc-plugins
```

플러그인 단위지 프로젝트 단위가 아니다. 키 없이 한 파일을 쓰면 창 여섯이 서로를 덮는다.

### ③ `(machine, worktree)` 로는 못 가른다 — 가설이 아니라 지금 깨져 있다

같은 `CLAUDE_PROJECT_DIR=/home/aaron/cdo-dev/kweiza-cc-plugins` 에 창이 둘이다
(pid 296061 cc=`0b206d90`, pid 643548 cc=`8210a1c6`).
항목이 *"그것도 워크트리가 같은 두 창을 못 가른다"* 고 의심한 자리가 **실물로 확인됐다.**

### ④ 공유 안정키가 하나 있다 — claude 프로세스

```
fd mcp   (643548) ──PPid──> claude --continue (3980399)
bash 도구 (762452) ──PPid──> claude --continue (3980399)   ← 훅과 같은 채널
```

MCP 와 훅이 **같은 claude 프로세스의 자손**이다. 그리고 이 claude pid 는 직전 판단이 측정한
값(3980399, 11:31:44 기동)과 **같다** — `/clear` 를 건너 살아남았다. 반면 MCP pid 는 그 사이
바뀌었다(3980449 → 643548). **claude pid 는 cc 가 갈리는 전환을 넘어 안정적이다.**

### ⑤ transcript 는 길이 아니다

새 세션의 transcript(`5fa98806.jsonl`)는 첫 줄부터 자기 `sessionId` 로 시작하고
옛 세션(`8210a1c6`)을 **전혀 참조하지 않는다.** `/proc` 없이 두 cc 를 잇는 이식성 좋은
대안이 거기엔 없다. 계보가 유일한 길이다.

### ⑥ 원장은 전부 `session.id` 에 매달려 있다 — "합치기"는 컬럼 하나다

```
schema.sql:67   UNIQUE (machine_id, worktree, cc_session_id)
schema.sql:188  claim.session_id      REFERENCES session(id)
schema.sql:218  judgment.session_id   REFERENCES session(id)
schema.sql:101  footprint.session_id  REFERENCES session(id)
schema.sql:305  resource.session_id   REFERENCES session(id)
```

`cc_session_id` 는 upsert 중복을 막는 UNIQUE 에만 쓰인다. 즉 카드를 합치는 일은
**그 컬럼 하나를 UPDATE 하는 것**이고, 선점·판단·발자국·자원은 자동으로 따라온다.

### ⑦ 그리고 그것이 선택지 하나를 죽인다

"새 cc 로 따라가고 옛 카드는 버린다"는 **못 쓴다.** 이 서버는 선점 회수를 거절하고
(`pick` 의 `steal_reason` 은 사유와 함께 거절된다), 후보 범위가
*"살아 있지 않은 세션이 쥔 항목은 후보에 없다"* 이다.
유령 카드가 선점을 쥐면 **그 항목이 큐에서 영영 사라진다.** 표류를 고치려다 항목을 잃는다.

### ⑧ `ensureSession` 은 한 번만 열고 캐시한다 — 그래서 MCP 는 재조회가 필요 없다

```
mcpsrv/mcpsrv.go:436  ensureSession — s.sessionID 가 있으면 그대로 반환
```

rekey 가 `session.id` 를 **보존**하므로 MCP 의 캐시는 `/clear` 뒤에도 유효하다.
MCP 가 해야 할 일은 **첫 한 번** 올바른 카드를 고르는 것뿐이다.

## 설계

### §1 비콘이 놓일 자리 — 상태 디렉토리가 **아니다**

이 코드베이스가 이미 한 번 피 흘려 배운 자리다. `env.go` 의 `MachineIDPath` 주석:

> 상태 디렉토리는 `CLAUDE_PLUGIN_DATA`·`XDG_STATE_HOME` 로 고르는데 **그 둘은 채널마다 있고 없다.**
> machine-id 를 거기 뒀다가 파일이 두 벌이 됐고 한 세션이 카드 세 장으로 떴다.

비콘의 요구는 machine-id 와 **정확히 같다** — "같은 창이면 어느 채널에서 봐도 같아야 한다".
제약 ①이 오늘은 훅·MCP 가 둘 다 `CLAUDE_PLUGIN_DATA` 를 받는다고 말하지만, **그 우연에 정체를
걸지 않는다.** 플랫폼이 한 채널에서 그 변수를 빼는 날 조용히 죽고, 그 침묵이 정확히
machine-id 사고의 형태다.

```
~/.flightdeck/windows/<machine>-<claudepid>-<starttime>.json
FD_STATE_DIR 이 있으면 <FD_STATE_DIR>/windows/ — 사람이 명시 지정하는 축이라 채널마다 안 갈린다
```

캐시·아웃박스(열화 상태 — 채널 의존이 **설계 의도**, DESIGN §7)와 정체(채널 의존이 **곧 결함**)를
다시 한 디렉토리에 뭉개지 않는다.

### §2 비콘의 내용

```json
{
  "claude_pid": 3980399,
  "claude_started": "39802719",
  "machine_id": "m-…",
  "worktree": "/home/aaron/cdo-dev/kweiza-cc-plugins",
  "cc_session_id": "5fa98806-…",
  "session_id": "01KZ64VY…",
  "updated_at": "2026-08-05T…Z"
}
```

- `claude_started` 는 pid 재사용 방어의 **첫 겹**이다. 파싱하지 않고 문자열로 대조만 한다 —
  양쪽이 같은 헬퍼로 얻으므로 **일관성만 있으면 되고 이식 가능한 의미는 필요 없다.**
  리눅스는 `/proc/<pid>/stat` 22번째 필드, 맥은 `ps -o lstart= -p <pid>`.
- `worktree` 는 **둘째 겹**이다. `claude_started` 를 못 읽는 플랫폼에서도 이건 된다.
  읽은 비콘의 워크트리가 내 것과 다르면 남의 창이다 — 거절한다.
- 쓰기는 임시파일 + `rename` 으로 원자적이다. 훅과 MCP 가 동시에 쓸 수 있다.

### §3 두 배우 — MCP 가 심고, 훅이 고친다

| | 아는 것 | 하는 일 |
|---|---|---|
| **MCP** | 자기 PPid = claude pid (`os.Getppid()`) | 기동 때 비콘을 **심는다**(pid · started · worktree · 자기 env cc). 첫 `ensureSession` 때 비콘의 `session_id`·`cc` 를 자기 env cc 보다 **우선**한다. 이후 캐시(제약 ⑧). **고치지 않는다** |
| **훅** | 새 cc (페이로드 `session_id`) | 조상 pid 마다 비콘이 있나 본다 → 내 창의 비콘 → cc 갈렸으면 **rekey 먼저, 그다음 OpenSession** → 비콘에 새 cc·`session_id` 를 적는다 |

**심는 쪽이 MCP 인 것이 이 설계에서 이름 대조를 없앤다.**
훅에게는 "어느 조상이 claude 인가"를 확정할 수단이 없다 — `cmdline` 에 `claude` 가 들어있는지로
맞추면 실행 경로(node·npx·래퍼·IDE 확장)마다 깨진다. 반면 MCP 의 부모는 **정의상** claude 다
(제약 ④, `os.Getppid()`, stdlib, 이식성 있음). 그래서 MCP 가 먼저 자리를 표시해 두면
훅은 **자기 조상 pid 로 파일이 있나 묻기만 하면 된다.** 이름을 아는 쪽이 아무도 없어도 된다.

기동 시점에는 훅과 MCP 의 cc 가 같으므로(표류는 전환에서만 생긴다) 누가 먼저 카드를 열든
3중키가 같은 카드로 떨어진다. 비콘이 아직 없으면 §5 폴백이다 — 오늘 거동이고 손해가 없다.

**rekey 를 OpenSession 앞에 두는 것이 이 설계의 핵심 순서다.**
뒤에 두면 훅의 upsert 가 새 cc 로 카드 B 를 먼저 만들고, 그러면 rekey 가 UNIQUE 에 걸려
두 카드를 진짜로 병합하는 경로가 또 필요해진다.
앞에 두면 카드 A 가 이미 새 cc 를 갖게 되어 **그 upsert 가 같은 카드로 떨어진다** — 새 코드 경로 없이.

`/proc`·`ps` 로 조상을 **거슬러 올라가야 하는 쪽은 훅뿐이고**, 그것도 이 워크트리의 비콘이
둘 이상일 때만 필요하다 — 하나뿐이면 걷지 않는다.

전환 시점의 흐름:

```
/clear
  훅 SessionStart(session_id=새cc)
    → 조상 사슬에서 claude pid 3980399
    → 비콘 읽기: { cc: 옛cc, session_id: 카드A }
    → 갈렸다 → POST /sessions/카드A/rekey { cc_session_id: 새cc }
    → OpenSession(새cc) → 3중키가 카드A 를 찾는다 (카드 B 는 안 생긴다)
    → 비콘 갱신 { cc: 새cc, session_id: 카드A }
  MCP 는 아무것도 안 한다 — s.sessionID 캐시가 그대로 카드A 다
```

### §4 서버 — 스키마 변경 없음

`POST /api/v1/sessions/{id}/rekey` · body `{ "cc_session_id": "…" }`

```sql
UPDATE session SET cc_session_id = ? WHERE id = ?
```

제약 ⑥에 따라 이 한 줄로 선점·판단·발자국·자원이 전부 따라온다.

- `UNIQUE (machine_id, worktree, cc_session_id)` 충돌은 `ConflictDuplicate` → 409 로 **사유와 함께**
  올린다. 클라이언트는 §5 폴백으로 간다.
- rekey 는 `event` 행을 남긴다. 카드의 cc 가 조용히 바뀌면 나중에 아무도 원인에 도달 못 한다 —
  이번에 `/proc` 을 뒤져야 했던 것이 정확히 그 이유였다.
- `judgment` 는 불변 트리거가 걸려 있지만(`judgment_no_update`) `session` 행은 아니다. 충돌 없다.

### §5 실패했을 때

비콘을 못 잡거나(비콘 없음 · 계보 못 걸음 · 워크트리 불일치) rekey 가 실패하면(UNIQUE 충돌)
**각자 env cc 로 진행하고 오늘 거동으로 폴백한다.** 도구는 전부 계속 된다.

훅이 fail-open 인 것이 이 코드베이스의 규율이고(`hook.go` 머리: *"이것이 깨지면 세션이 안 뜬다"*),
세션 귀속 도구를 막는 길은 `/clear` 한 번이 도구를 통째로 죽이므로 택하지 않았다.

`RenderDrift` 를 지우지 않는다. **사유 인자를 받게 바꾼다** — 위 네 가지 중 무엇이었는지.
오늘 그 경고는 유일한 대응책이지만, 이 작업 뒤에는 **수리가 왜 안 됐는지 말하는 자리**가 된다.
순수 함수인 것과 표류가 없으면 빈 문자열인 것은 그대로다.

## 시험

- **순수**: 비콘 인코딩/디코딩 · 키 조립과 파일명 · 워크트리 불일치 거절 · `claude_started` 불일치 거절
  · 사유별 `RenderDrift` 문구 · 조상 사슬 걷기(가짜 프로세스 표를 `ppidOf func(int) (int, error)` 로 주입).
- **저장소**: rekey 가 cc 를 옮긴다 · **선점과 판단이 같은 `session.id` 에 그대로 붙어 있다**
  · UNIQUE 충돌이 `ConflictDuplicate` 로 온다.
- **씰**: MCP 가 비콘을 심고 → 훅이 자기 조상 pid 로 그것을 찾아내고 → **카드가 한 장이다.**
  조상 사슬 중간에 셸이 끼어도(훅 → sh → claude) 찾아내는지 함께 단정한다.
  `/clear` 는 훅 페이로드의 `session_id` 를 바꿔 흉내내고, rekey 가 돌고 카드가 여전히 한 장이며
  선점이 보존됐음을 단정한다.

**시험을 쓸 때 조심할 것.** 직전 작업이 같은 자리에서 두 번 걸렸고 그 사유가 판단에 적혀 있다:

1. **순환 전제** — 판정기가 찍는 문자열로 전제를 세우면 판정기가 돌 때 전제도 함께 통과한다.
   카드 수·선점 보존은 **서비스를 직접 쳐서** 단정한다. 렌더된 문자열로 세지 않는다.
2. **순서** — `ensureSession` 이 게을러서 도구를 부르기 전에는 MCP 세션 행이 없다.
   카드 수를 세는 단정은 도구 호출 **뒤**에 둔다.

## 안 하는 것과 왜

- **3중키 스키마를 안 바꾼다.** cc 를 claude pid 로 교체하는 길은 서버 스키마·원장 귀속·캐시 키·
  기존 카드 마이그레이션을 전부 건드린다. rekey 로 같은 결과를 얻는데 그 값을 치를 이유가 없다.
- **`DivergentSessions` 를 안 건드린다.** 그것은 *같은 cc · 다른 프로젝트/머신* 이라는 **다른 축**이다
  (`store/session.go:132`). 항목이 "함께 보는 게 낫다"고 적은 자리지만, 이 설계는 같은 워크트리 안의
  cc 갈림만 다루므로 그 축과 겹치지 않는다. `fd-identity-divergence-detection` 에 그대로 남긴다.
- **기존 유령 카드를 소급 병합하지 않는다.** 1회성이라 코드로 넣을 값어치가 없다.
- **`environ` 재읽기를 되살리지 않는다.** 직전 판단이 실측으로 지운 처방이다.

## 건드리는 파일

| 파일 | 무엇 |
|---|---|
| `internal/window/` (신규) | 비콘 읽기·쓰기·정리 · 계보 걷기. 순수 핵심 + 얇은 OS 씰. **`cmd/fd` 아래 두면 안 된다** — `mcpsrv` 도 쓴다 |
| `cmd/fd/env.go` | 비콘 자리(`MachineIDPath` 와 같은 채널 독립 규칙) |
| `cmd/fd/hook.go` | `hookSessionStart` — 계보 → 비콘 → rekey → OpenSession → 비콘 갱신 |
| `internal/mcpsrv/mcpsrv.go` | 기동 때 비콘을 심는다 · `ensureSession` 이 비콘을 우선한다 |
| `internal/mcpsrv/drift.go` | `RenderDrift` 가 사유를 받는다 |
| `internal/store/session.go` | `Rekey` |
| `internal/service`, `internal/api` | rekey 배선 + 라우트 |
| `schema.sql` | **변경 없음** |

### 비콘 정리

머신 전체가 공유하는 디렉토리라 죽은 창의 비콘이 쌓인다.
훅이 쓸 때 함께 치운다(타임아웃 10초로 여유가 있는 쪽이다) — `kill(pid, 0)` 이 `ESRCH` 면 지운다.
MCP 는 정리하지 않는다.
