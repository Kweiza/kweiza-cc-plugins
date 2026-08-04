# cc 표류 — 훅이 현재 cc_session_id 를 MCP 에 실어 오는 통로 설계

작성 2026-08-05 · 상태 **승인됨**(브레인스토밍 2026-08-05) · **개정 2026-08-05**(구현 조사 뒤)
큐 항목 `fd-cc-drift-hook-channel` · 선행 판단 `[handoff] 2026-08-04 02:49` · 선행 커밋 `ca248c2`

**이 문서의 file:line 은 전부 브랜치 `fd-cc-drift-hook-channel` 의 워크트리 기준이다**
(`.flightdeck/worktrees/fd-cc-drift-hook-channel/`). 오늘은 main 과 줄 번호가 같지만 그건 이 브랜치가
문서 하나만 더했기 때문이지 의존해도 되는 사실이 아니다.

## 개정 — 구현 조사가 승인된 설계의 전제 다섯 개를 깼다

계획을 쓰기 전 코드 5계층을 훑고 완결성 비평가를 붙였다. 결정 셋(계보 키 · 카드 한 장 · 경고 후 폴백)은
살아남았지만 **그것을 실현하는 방법 다섯 군데가 실측으로 틀린 것이 드러났다.** 아래 §들은 개정본이다.

| # | 승인본이 말한 것 | 실측 | 개정 |
|---|---|---|---|
| ① | "MCP 의 부모는 **정의상** claude 다" | **거짓.** `fd mcp` pid 201051 의 부모는 Cursor 의 node 이고(node→node→sh→bash→systemd) 사슬 어디에도 claude 가 없다. `CLAUDE_CODE_SESSION_ID`·`CLAUDE_PLUGIN_DATA` 도 없다 | 심기에 `canAttribute` 급 가드를 건다(§3) |
| ② | "기동 때 심는다"(1회) | **MCP 는 대화 중간에 재기동된다.** pid 643548 의 시작이 부모 claude 3980399 보다 2,374,680 틱(≈6.6시간) 늦다 | 심기는 **병합**이다. 같은 claude pid 의 비콘이 있으면 cc·session_id 를 **절대 안 덮는다**(§3) |
| ③ | "비콘이 하나뿐이면 계보를 안 걷는다" | 같은 워크트리에 **창이 다섯**이다(cc 다섯, claude 부모 다섯) | 빠른 길을 **삭제**한다. 계보 대조는 무조건이다(§3) |
| ④ | "비콘 자리는 `cmd/fd/env.go`" | `cmd/fd` 는 `package main` 이다 — `mcpsrv` 가 임포트 못 한다 | 사다리를 `internal/window` 로 옮기고 `cmd/fd/env.go` 가 **위임**한다(§1) |
| ⑤ | "MCP 가 비콘의 `session_id`·`cc` 를 우선한다" | `Backend` 인터페이스가 닫혀 있고(`backend.go:26`) 넓히면 컴파일로 고정된 `serialProbe` 포함 4곳이 따라 바뀐다 | **cc 만** 우선한다. 3중키가 알아서 같은 카드로 떨어진다(§3) |

③이 가장 위험했다. 가지치기 뒤 비콘이 하나 남았을 때 빠른 길이 **남의 창 비콘을 집어** 그 창의 선점과
판단을 이 대화의 cc 로 옮겼을 것이다 — 표류를 고치려다 원장을 섞는다.

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
schema.sql:305  resource_hold.session_id  REFERENCES session(id)  (nullable)
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

**개정 ④ — 이 사다리는 `internal/window` 에 산다, `cmd/fd/env.go` 가 아니다.**
`cmd/fd/env.go:1` 이 `package main` 이라 `internal/mcpsrv` 가 `MachineIDPath` 를 임포트할 수 없다.
승인본의 파일 표는 그대로는 구현 불가다. 그래서 `window.BeaconDir(get, home)` 이 사다리의 **유일한 주인**이고
`cmd/fd/env.go` 는 거기에 위임한다 — `client.go:110-114` 가 "같은 판단이 두 자리에 살아 세 번 데였다"고
적어 둔 그 실수를 반복하지 않기 위해서다. `MachineIDPath` 자체는 안 옮긴다(이 작업의 범위가 아니다).

**시험이 진짜 홈을 안 건드리게 하는 축이 `mcpsrv` 에는 없다.** `cmd/fd` 는 `unpinnedEnv` +
`TestUnpinnedEnvNeverReachesTheRealHome`(`cmd/fd/harness_env_test.go:43-76`)로 이 사고를 막지만,
`mcpsrv` 의 `newServer`(`server_test.go:74`)는 `WithEnv`·`WithCwd`·`WithHostname` 만 주입하고
HOME 개념이 없으며 `New` 의 기본 `getenv` 는 `os.LookupEnv` 다(`mcpsrv.go:135`).
그래서 **`WithBeaconDir` 옵션을 새로 만들고, 그 옵션이 없으면 심지 않는다.**
옵션 없이 기본 경로로 떨어지면 `go test ./internal/mcpsrv/` 가 개발자의 실제 `~/.flightdeck/windows/` 에
파일을 쓴다.

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
  리눅스는 `/proc/<pid>/stat` 22번째 필드(실측: 643548→`546818553`, 3980399→`544443873`).
  "같은 헬퍼"는 그 헬퍼가 `internal/window` 에 하나만 있을 때만 참이다 — 쓰는 쪽은 `mcpsrv`,
  읽는 쪽은 `cmd/fd` 라 개정 ④와 같은 이유로 여기 산다.
- `worktree` 는 **둘째 겹**이다. 다만 **이것만으로는 아무것도 못 가른다** — 같은 워크트리에 창이 다섯이다
  (개정 ③). 계보 대조를 대신하지 못하고 보조일 뿐이다.
- 쓰기는 임시파일 + `rename` 으로 원자적이다.

**플랫폼 분기는 `disk_unix.go`/`disk_other.go` 선례를 따른다**(`internal/service/disk_unix.go:1`
`//go:build unix` / `disk_other.go:1` `//go:build !unix`). 그 선례의 규칙이 핵심이다 —
**지원 안 되는 플랫폼은 빈 값이 아니라 오류를 낸다**(`disk_other.go:11-12`).
`claude_started` 를 빈 문자열로 돌려주면 모든 비콘이 "시작 시각 일치"로 통과해 방어가 조용히 사라진다.

### §3 두 배우 — MCP 가 심고, 훅이 고친다

| | 아는 것 | 하는 일 |
|---|---|---|
| **MCP** | 자기 PPid (`os.Getppid()`) | 기동 때 비콘을 **심는다** — 단 **병합**이고 **가드가 걸린다**(아래). 첫 `ensureSession` 때 비콘의 **`cc` 만** 우선한다. 이후 캐시(제약 ⑧). **고치지 않는다** |
| **훅** | 새 cc (페이로드 `session_id`) | 조상 pid 를 **끝까지** 훑어 비콘을 찾는다 → cc 갈렸으면 **rekey 먼저, 그다음 OpenSession** → 비콘에 새 cc·`session_id` 를 적는다 |

**심는 쪽이 MCP 인 것이 이 설계에서 이름 대조를 없앤다.**
훅에게는 "어느 조상이 claude 인가"를 확정할 수단이 없다 — `cmdline` 에 `claude` 가 들어있는지로
맞추면 실행 경로(node·npx·래퍼·IDE 확장)마다 깨진다. MCP 가 먼저 자리를 표시해 두면
훅은 **자기 조상 pid 로 파일이 있나 묻기만 하면 된다.** 이름을 아는 쪽이 아무도 없어도 된다.

### 개정 ① — 심기에 가드를 건다. 부모가 claude 라는 보장이 없다

승인본은 "MCP 의 부모는 정의상 claude 다"라고 적었다. **이 머신에서 이미 거짓이다:**

```
fd mcp (201051) ──PPid──> node (200182, ~/.cursor-server/…) → node → sh → bash → systemd
  환경: HOME 뿐. CLAUDE_CODE_SESSION_ID·CLAUDE_PROJECT_DIR·CLAUDE_PLUGIN_DATA 전부 없음
  바이너리도 딴 자리: ~/.local/state/flightdeck/bin/fd
```

Cursor 가 띄운 MCP 다. 여기서 무심코 심으면 **어떤 훅도 영영 못 맞추는 node pid 로 키가 잡힌다.**
그래서 심기는 `canAttribute`(`mcpsrv.go:430-432`)와 같은 급의 가드를 통과해야 한다 —
`CCSessionID`·`ProjectID`·절대경로 `Worktree` 가 다 있을 때만 심는다.
저 채널에는 Claude Code 훅 자체가 없으므로 안 심는 것이 옳다(§5 폴백이고 손해가 없다).

### 개정 ② — 심기는 병합이다. MCP 는 대화 중간에 재기동된다

승인본은 심기가 훅보다 먼저 딱 한 번 돈다고 가정했다. **틀렸다:**

```
claude  3980399  시작 틱 544443873
fd mcp   643548  시작 틱 546818553   ← 부모보다 2,374,680 틱(≈6.6시간) 늦게 떴다
```

플러그인 갱신 등으로 MCP 만 다시 뜬다. 그때 통째로 덮어쓰면 훅이 방금 적은 `{새 cc, 카드 A}` 를
**그 프로세스의 낡은 env cc 로 되돌리고**, 이어서 `ensureSession` 이 "비콘을 우선"해 그 낡은 값을 집는다 —
**이 기능이 고치려는 바로 그 버그를 재현한다.**

그래서 심기의 규칙은 이렇다: 같은 claude pid 의 비콘이 이미 있으면
**`cc_session_id` 와 `session_id` 를 건드리지 않는다.** 없을 때만 자기 env cc 로 채운다.
`worktree`·`claude_started` 같은 좌표는 갱신해도 되지만 **정체 두 필드는 훅만 쓴다.**

### 개정 ③ — 계보 대조는 무조건이다. 빠른 길을 지운다

승인본은 "이 워크트리의 비콘이 하나뿐이면 안 걷는다"고 적었다. **같은 워크트리에 창이 다섯이다:**

```
cwd=/home/aaron/cdo-dev/kweiza-cc-plugins 인 fd mcp 5개
  296061 cc=0b206d90 ppid 295977      1486144 cc=6d689979 ppid 1486079
  643548 cc=8210a1c6 ppid 3980399     1495102 cc=0e52ffdb ppid 1495035
 1449466 cc=88cadf4d ppid 1449423
```

워크트리 축이 아무것도 못 가른다. 그리고 가지치기 뒤 우연히 하나만 남은 순간 빠른 길이 돌면
**남의 창 비콘을 집어 그 창의 카드를 이 대화의 cc 로 rekey 한다** — 그 창의 선점과 판단이
통째로 딴 대화에 붙는다. 표류를 고치려다 원장을 섞는 것이라 이 설계에서 가장 나쁜 결과다.

**훅은 언제나 조상 사슬을 걷고, 조상 pid 와 비콘의 `claude_pid` 가 맞을 때만 그 비콘을 쓴다.**
맞는 게 없으면 §5 폴백이다.

### 개정 ⑤ — `ensureSession` 은 `cc` 만 우선한다

`Backend` 인터페이스는 닫혀 있다(`backend.go:26` "넓히지 않는다"). 비콘의 `session_id` 로 카드를
직접 잡으려면 `Backend.OpenSession` 을 우회하는 길이 필요하고, 그러면 컴파일로 고정된
`serialProbe`(`serial_test.go:142`) 포함 네 곳이 따라 바뀐다.

필요 없다. `ensureSession`(`mcpsrv.go:436-449`)이 만드는 `service.OpenSessionInput` 의 cc 자리에
비콘 값을 끼워 넣기만 하면 **3중키가 알아서 같은 카드로 떨어진다.** 인터페이스 변경 0.
비콘의 `session_id` 는 훅이 rekey 대상을 지목할 때만 쓴다.

기동 시점에는 훅과 MCP 의 cc 가 같으므로(표류는 전환에서만 생긴다) 누가 먼저 카드를 열든
3중키가 같은 카드로 떨어진다. 비콘이 아직 없으면 §5 폴백이다 — 오늘 거동이고 손해가 없다.
`session_id` 는 **첫 SessionStart 훅이 `OpenSession` 을 마친 뒤** 적힌다(그 전에는 카드가 없다 —
`ensureSession` 이 게을러서 도구 호출 전에는 세션 행이 안 생긴다). 즉 `/clear` 가 올 때쯤이면 이미 있다.
없으면 rekey 를 건너뛰고 그냥 `OpenSession` 한다 — 카드 두 장이 되지만 그게 오늘 거동이다.

**rekey 를 OpenSession 앞에 두는 것이 이 설계의 핵심 순서다.**
뒤에 두면 훅의 upsert 가 새 cc 로 카드 B 를 먼저 만들고, 그러면 rekey 가 UNIQUE 에 걸려
두 카드를 진짜로 병합하는 경로가 또 필요해진다.
앞에 두면 카드 A 가 이미 새 cc 를 갖게 되어 **그 upsert 가 같은 카드로 떨어진다** — 새 코드 경로 없이.

`/proc` 으로 조상을 거슬러 올라가야 하는 쪽은 **훅뿐이다** — MCP 는 `os.Getppid()` 로 끝난다.
그 걷기는 **언제나** 돈다(개정 ③이 빠른 길을 지웠다).

전환 시점의 흐름:

```
/clear
  훅 SessionStart(session_id=새cc)
    → 조상 사슬을 끝까지 걷는다: self → sh → claude(3980399) → …
    → 그 pid 들로 비콘을 찾고 claude_pid 가 맞는 것만 쓴다
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

- `UNIQUE (machine_id, worktree, cc_session_id)` 충돌은 `ConflictDuplicate`(`constraint.go:87`
  `ConflictError`, `TargetSession`) → 409 로 **사유와 함께** 올린다.
- 없는 id 는 `notFound`/`NFSession` 이다. `RowsAffected == 0` 판정은 이미 있는 `affectedOne`
  (`item.go:550`)을 쓴다 — UPDATE 꼴 메서드가 전부 그걸 쓴다.
- rekey 는 `event` 행을 남긴다. 카드의 cc 가 조용히 바뀌면 나중에 아무도 원인에 도달 못 한다 —
  이번에 `/proc` 을 뒤져야 했던 것이 정확히 그 이유였다.
  **트랜잭션 안에서는 `Tx.LogEvent`(`store.go:148`)를 쓴다.** `Store.LogEvent` 는 별도 연결이라
  Tx 안에서 부르면 교착한다.
- `judgment` 는 불변 트리거가 걸려 있지만(`judgment_no_update`) `session` 행은 아니다. 충돌 없다.
- **마이그레이션이 필요 없다.** 컬럼도 표도 안 늘어난다. `SchemaVersion` 은 2 그대로다.
- **`DESIGN.md` §6 의 REST 표에 한 줄 넣는다.** `api.go:175` 가 "routes() 는 설계 §6 의 REST 표와
  1:1"이라고 선언하는데 그걸 강제하는 시험이 없다 — 안 적으면 조용히 썩는다(`DESIGN.md:318-325`).

### §5 실패했을 때

비콘을 못 잡거나(비콘 없음 · 계보에 맞는 것 없음 · `claude_started` 불일치) rekey 가 실패하면
(UNIQUE 충돌 · 404 · 서버 불통) **각자 env cc 로 진행하고 오늘 거동으로 폴백한다.**
도구는 전부 계속 된다.

훅이 fail-open 인 것이 이 코드베이스의 규율이고(`hook.go` 머리: *"이것이 깨지면 세션이 안 뜬다"*),
세션 귀속 도구를 막는 길은 `/clear` 한 번이 도구를 통째로 죽이므로 택하지 않았다.

**폴백은 공짜가 아니다 — 손으로 써야 한다.** 조사가 이 자리에서 셋을 짚었다:

1. **rekey 는 `a.cli.do(ctx, "POST", path, body, FreshKey(...))` 로 보낸다**
   (`openSession` 이 쓰는 꼴, `app.go:111`). `a.cli.Write`(`client.go:238-277`)로 보내면
   `JudgeOffline`(`offline.go:83-87`)과 `IdempotencyStable`(`outbox.go:132-135`)의 default 가
   "정책이 정의되어 있지 않다"로 **거절**하므로 서버가 안 닿을 때마다 실패한다.
   rekey 는 아웃박스에 쌓을 것도 아니다 — 다음 훅이 어차피 다시 시도한다.
2. **409 는 `ErrUnreachable` 이 아니라 `*APIError` 로 온다**(`client.go:176-180`,
   `offline.go:93-97` 이 4xx 와 불통을 굳게 가른다). 즉 기존 열화 경로가 이걸 안 잡아 준다.
   `hook.go:169-175` 가 `OpenSession` 실패를 삼켜 `in.Notice` 로 바꾸는 그 꼴을 그대로 쓴다.
3. **rekey 성공 뒤 세션 캐시 키를 옮긴다.** `openSession` 은 응답을 `"/local/session/"+cc` 에
   캐시하는데(`app.go:88,117,126`) cc 가 바뀌면 오프라인 읽기가 빗나가 "이 세션의 캐시도 없다"가 된다.

`RenderDrift` 를 지우지 않는다. **사유 인자를 받게 바꾼다** — 위 사유들 중 무엇이었는지.
오늘 그 경고는 유일한 대응책이지만, 이 작업 뒤에는 **수리가 왜 안 됐는지 말하는 자리**가 된다.
순수 함수인 것과 표류가 없으면 빈 문자열인 것은 그대로다.

⚠ **이 변경은 지금 초록인 단정 둘을 깬다. 그게 정상이고, 계획에 명시된 태스크가 있다.**
`TestRenderDriftNamesTheAxisAndWhatToDo`(`drift_test.go:112-131`)가 `기동`·`재기동` 을,
`TestBoardShowsCCDriftInTheResponse`(`drift_test.go:182`)가 `재기동` 을 요구한다.
그 문구는 **"고칠 길이 재기동뿐"이라는 옛 사실**을 굳혀 둔 것이고 이 작업이 그 사실을 바꾼다.
자연스러운 반사("시험을 초록으로 되돌리자")를 따르면 **틀린 조언이 되살아난다.**

## 시험

- **순수**: 비콘 인코딩/디코딩 · 키 조립과 파일명 · 워크트리 불일치 거절 · `claude_started` 불일치 거절
  · 사유별 `RenderDrift` 문구 · 조상 사슬 걷기(가짜 프로세스 표를 `ppidOf func(int) (int, error)` 로 주입).
- **저장소**: rekey 가 cc 를 옮긴다 · **선점과 판단이 같은 `session.id` 에 그대로 붙어 있다**
  · UNIQUE 충돌이 `ConflictDuplicate` 로 온다.
- **씰**: MCP 가 비콘을 심고 → 훅이 자기 조상 pid 로 그것을 찾아내고 → **카드가 한 장이다.**
  조상 사슬 중간에 셸이 끼어도(훅 → sh → claude) 찾아내는지 함께 단정한다.
  `/clear` 는 훅 페이로드의 `session_id` 를 바꿔 흉내내고, rekey 가 돌고 카드가 여전히 한 장이며
  선점이 보존됐음을 단정한다.
- **개정이 만든 회귀 케이스 넷** — 이것들이 없으면 개정은 문서에만 남는다:
  1. 심기 가드: 정체가 반쪽(cc 없음)이면 **파일을 안 만든다**.
  2. 심기 병합: 훅이 `{새 cc, 카드A}` 를 적은 뒤 MCP 가 다시 심어도 그 두 필드가 **안 바뀐다**.
  3. 계보 필수: 같은 워크트리·같은 머신인데 **조상이 아닌** 창의 비콘은 안 쓴다(rekey 가 안 돈다).
  4. 시험 격리: `WithBeaconDir` 없이 만든 서버는 **아무 파일도 안 쓴다**.
- **시험이 진짜 홈을 안 건드리는지**를 `cmd/fd` 쪽 `TestUnpinnedEnvNeverReachesTheRealHome`
  (`harness_env_test.go:43-76`)과 같은 급으로 `internal/window` 에도 둔다.

**믿으면 안 되는 가드가 하나 있다.** `TestEnvAxisInventoryIsCurrent`(`harness_env_test.go:139-152`)는
소스를 훑지 않고 **하니스가 고정한 키만** 훑는다. 새 환경 축을 읽어도 이 시험은 안 빨개진다 —
새 축을 쓰면 목록에 손으로 넣어야 한다.

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
| `cmd/fd/env.go` | 비콘 자리를 `window.BeaconDir` 에 **위임**한다(사다리 주인은 하나다) |
| `cmd/fd/hook.go` | `hookSessionStart` — 계보 → 비콘 → rekey → OpenSession → 비콘 갱신 · 캐시 키 이전 |
| `cmd/fd/app.go` | rekey 를 `a.cli.do` 로 보내는 갈래 |
| `internal/mcpsrv/mcpsrv.go` | `WithBeaconDir` 옵션 · 가드 걸린 **병합** 심기 · `ensureSession` 이 비콘의 **cc 만** 우선 |
| `internal/mcpsrv/drift.go` | `RenderDrift` 가 사유를 받는다 |
| `internal/mcpsrv/drift_test.go` | 위 둘의 단정을 새 조언으로 다시 쓴다(빨강 먼저) |
| `internal/store/session.go` | `Rekey` (+`Tx.LogEvent`) |
| `internal/service`, `internal/api` | rekey 배선 + 라우트 |
| `plugins/flightdeck/DESIGN.md` | §6 REST 표에 rekey 한 줄(`:318-325`) |
| `schema.sql` · 마이그레이션 | **변경 없음** |

### 비콘 정리와 동시성

머신 전체가 공유하는 디렉토리라 죽은 창의 비콘이 쌓인다.
훅이 쓸 때 함께 치운다(`SessionStart` 타임아웃 10초로 여유가 있는 쪽이다) —
`kill(pid, 0)` 이 `ESRCH` 면 지운다. MCP 는 정리하지 않는다.

**동시성 규칙은 세 줄이다.** 지금 이 머신에서 창 다섯이 같은 디렉토리를 쓰고 있고,
`cmd/fd` 의 기존 파일 기록자들은 스스로 "동시성 안전하지 않다"고 적어 뒀다(`client.go:67-83`).
그러니 여기서는 그 실수를 처음부터 안 한다:

1. **쓰기는 자기 파일에만.** 창마다 파일이 다르므로(키가 claude pid) 두 창이 같은 파일을 안 쓴다.
   같은 창의 훅과 MCP 만 한 파일을 공유하고, 그 둘은 임시파일+`rename` 으로 원자적으로 쓴다.
   임시파일 이름에 **자기 pid 를 넣는다** — 키마다 고정 임시경로를 쓰면 `Cache.Put` 이 가진
   바로 그 결함이 된다.
2. **가지치기는 지우기만 한다.** 남의 파일을 고쳐 쓰지 않으므로 남의 `rename` 과 안 싸운다.
   지우려는 순간 그 창이 살아나면 다음 심기가 다시 만든다 — 손해가 없다.
3. **읽기 실패는 폴백이지 오류가 아니다.** 남이 `rename` 하는 중에 읽으면 옛 내용이거나 없음이다.
   둘 다 §5 로 간다.
