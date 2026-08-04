# 아웃박스를 채널 무관한 고정 자리로 — 설계

- 항목: `fd-outbox-per-channel`
- 선행: `sha:3c4c7a4` (영구 거절 격리 — `fd-outbox-stuck-record`)
- 날짜: 2026-08-05

## 1. 무엇이 잘못됐나

아웃박스는 상태 디렉토리 아래에 있고(`newOutbox(sd)` → `sd.sub("outbox")`),
상태 디렉토리는 **채널마다 갈린다**(`ResolveStateDir`):

| 채널 | 자리 |
|---|---|
| 훅·MCP | `CLAUDE_PLUGIN_DATA/flightdeck` — Claude Code 가 그 프로세스에만 넣어 준다 |
| 사용자 셸 | `XDG_STATE_HOME/flightdeck` 또는 `~/.local/state/flightdeck` |

즉 **한 머신에 아웃박스가 여럿이다.** 셸에서 쌓인 판단은 셸 명령만 재생할 수 있고,
MCP 가 아무리 돌아도 그 줄은 안 나간다.

### 이 머신의 실측 (2026-08-05)

| 자리 | 내용 |
|---|---|
| `~/.local/state/flightdeck/outbox/` | `rejected.jsonl` 1건 (8/3 판단, 409 로 영구 격리). `pending` 없음 |
| `~/.claude/plugins/data/flightdeck-kweiza-cc-plugins/flightdeck/` | outbox 디렉토리 자체가 없다 |
| `~/.flightdeck/` | machine-id · token · db — **고정 자리가 이미 여기다** |

지금 막힌 판단은 없다(선행 작업이 격리로 풀었다). **이 작업은 구조 결함을 없애는 것이지
막힌 줄을 구조하는 것이 아니다.** 다만 격리 파일도 채널마다 갈리므로, 영구 보존해야 할
판단 1건이 셸 채널에만 있고 MCP 채널에서 `fd doctor` 를 부르면 "격리 0건"이 나온다.

## 2. 설계 §7 이 실제로 방어하는 것 — 그리고 하지 않는 것

항목은 "설계 §7 이 상태 디렉토리의 환경 의존을 **의도**로 적어 둔다"고 경고했다.
읽어 보면 그 방어는 좁다.

- **§7 이 말하는 것**: `상태는 ${CLAUDE_PLUGIN_DATA} 에 둔다(${CLAUDE_PLUGIN_ROOT} 는
  업데이트마다 경로가 바뀐다)`. 이것은 *`CLAUDE_PLUGIN_ROOT` 를 쓰지 말라*는 논거이지
  *채널마다 갈려도 된다*는 논거가 아니다.
- **더 강한 주장은 설계가 아니라 주석에 있다**: `config.go:15` 의
  "열화 상태(캐시·아웃박스)는 채널마다 따로여도 되기 때문이다".

**그 주장이 반증됐다.** 캐시는 재생성 가능하니 갈려도 되지만, 아웃박스는 §7 스스로
"재생성 불가한 유일한 자산"이라 부른 것을 담는다. 두 축이 한 디렉토리에 뭉개져 있었고,
그것은 `MachineIDPath` 주석이 "이 사고의 전부"라고 지목한 것과 **같은 모양**이다.

**따라서 가르는 축은 "열화 상태인가"가 아니라 "재생성 가능한가"다.**

## 3. 자리

`env.go` 에 `OutboxPath(get, home) (dir, source string)` 를 `MachineIDPath`·`ConfigPath` 와
같은 모양으로 둔다:

```
FD_STATE_DIR/outbox        명시 지정 — 사람이 정하는 축이라 프로세스마다 안 갈린다
~/.flightdeck/outbox       채널 환경과 무관한 고정 자리
<tmp>/flightdeck/outbox    HOME 이 없다 — 재부팅하면 아직 못 보낸 판단이 사라진다
```

새 규칙이 아니라 **같은 규칙의 셋째 적용**이다. `FD_STATE_DIR` 예외를 남기는 이유도 같다 —
채널이 아니라 사람이 지정하는 축이고, 시험이 진짜 홈을 안 건드리게 막는 유일한 자리다.

`StateDir` 은 **캐시만** 남긴다. `${CLAUDE_PLUGIN_ROOT}` 를 피하라는 §7 의 원래 논거는
캐시에 그대로 유효하다.

`newOutbox(sd StateDir)` → `newOutbox(get, home)`. `Outbox` 가 `source` 를 들고 있어
`fd doctor` 가 "왜 저 자리냐"에 답한다(`machineSrc` 가 선례다).

## 4. 이전 — 청구 후 흡수

`adopt()` 를 `Replay` 맨 앞에 둔다. 옛 자리의 파일마다:

1. **청구** — `os.Rename(src, src+".claimed-<유일값>")`. 실패(ENOENT)면 남이 이겼거나
   없는 것이니 넘어간다. rename 이 원자라 **둘이 동시에 이전해도 한쪽만 이긴다.**
   유일값은 `crypto/rand` 12바이트 hex 다. 시각을 쓰지 않는다 — 같은 초에 둘이 들어오면
   이름이 겹쳐 청구가 청구가 아니게 된다(`FreshKey` 가 같은 이유로 같은 선택을 한다).
   난수를 못 읽으면 나노초로 대신한다.
2. **흡수** — 청구한 파일을 읽어 고정 자리에 `Append`(키 중복 검사를 탄다).
   격리 파일은 고정 격리 파일로 추가한다.
3. **보존** — 성공하면 `.migrated-<시각>` 으로 개명한다. **지우지 않는다**(§9).
4. **중간 실패** — 원본으로 되돌리지 **않는다.** 그 사이 새 오프라인 쓰기가 원본을
   만들었으면 `rename` 이 그것을 덮어써 판단을 잃는다. 대신 `.claimed-*` 를 그대로 두고
   `notice` 로 올린다. 다음 실행의 `adopt` 는 **고아 청구 파일도 청구 대상에 포함**해
   새 유일 이름으로 다시 집는다. `Append` 가 멱등이라 재시도가 안전하다.

### 왜 청구가 필요한가 — 24시간 TTL

판단 POST 는 DB 에 남는 멱등이다(`api.JudgePersistIdempotency`: "판단은 추가 전용이라
중복이 들어가면 되돌릴 방법이 없다"). 그런데 그 표의 TTL 이 기본 24시간이고
(`api.Options.IdempotencyTTL`), 판단은 트리거가 `UPDATE`·`DELETE` 를 막는다.

즉 **같은 키가 24시간을 넘겨 두 번 재생되면 되돌릴 수 없는 판단 한 줄이 생긴다.**
잠금 없이 흡수하면 두 프로세스가 같은 줄을 각자 `Append` 해 중복 줄이 남고,
`Replay` 가 중간에 멈추는 경우 둘째 사본이 24시간 뒤에 나갈 수 있다.
**청구가 그 구멍을 원리적으로 닫는다** — 원본 파일 하나는 한 프로세스만 읽는다.

### 순서는 뒤에 붙인다 — 그리고 그것으로 충분하다

옛 자리의 줄을 고정 큐 **끝에** 붙인다. 그러면 옛 채널의 오래된 판단이 이미 쌓여 있던
새 판단보다 뒤에 나갈 수 있다.

`Replay` 는 "순서가 의미이고"를 근거로 첫 실패에서 멈춘다. 그 보증이 여기서 깨지는가 —
**아니다.** 그 보증의 범위는 **한 큐 안에서 보내는 순서**이고, 그 이유는 두 가지였다:
뒤엣것을 계속 시도하면 실패 폭풍이 나는 것, 그리고 앞엣것이 만들 좌표를 뒤엣것이 참조할
수 있는 것. 둘 다 "보내는 순서"의 문제이지 "시간순으로 정렬되어야 한다"가 아니다.

애초에 **채널이 갈려 있는 동안에도 전역 시간순은 없었다** — 두 큐가 각자 순서대로
나갔을 뿐이다. 합치면 나빠지는 것이 아니라 지금과 같다. 그리고 각 줄은 `At` 을 들고
있고 판단의 시각은 본문이 말한다. 시각으로 병합 정렬하는 기구를 만들지 않는 이유가
그것이다 — 얻는 것 없이 복잡도만 는다.

## 5. 후보 목록 — 이 채널이 계산할 수 있는 것만

훑는 **디렉토리**:

- `CLAUDE_PLUGIN_DATA/flightdeck/outbox` (있으면)
- `XDG_STATE_HOME/flightdeck/outbox` (있으면)
- `~/.local/state/flightdeck/outbox`
- `<tmp>/flightdeck/outbox`

목표 자리와 같으면 건너뛴다(`FD_STATE_DIR` 이 고정 자리를 가리키는 경우).

각 디렉토리에서 청구하는 **파일**:

- `pending.jsonl` · `rejected.jsonl` — 정규 이름
- `pending.jsonl.claimed-*` · `rejected.jsonl.claimed-*` — **앞선 실행이 중간에 죽여 놓고 간
  고아.** §4 의 4번이 이것에 기댄다. 고아를 다시 집는 것도 청구다 — 새 유일 이름으로
  rename 하므로 둘이 동시에 집어도 한쪽만 이긴다.
- `.migrated-*` 는 **건드리지 않는다.** 이미 흡수가 끝난 보존본이라, 다시 집으면
  흡수가 무한히 반복된다.

**`~/.claude/plugins/data/*/flightdeck` 를 glob 하지 않는다.** 그 배치는 §13 이 실측으로
적어 둔 플랫폼 사실이고 마켓 이름이 변수다. 추측해 박으면 "버전이 경로에 들어가므로
그 경로를 어디에도 저장하지 않는다"는 §13 의 판정을 어긴다.

**수렴 경로**: 훅·MCP 채널은 `CLAUDE_PLUGIN_DATA` 가 있으니 SessionStart 마다 제 자리를
비운다. 셸 채널은 제 자리를 비운다. 서로의 배치를 추측하지 않고 양쪽이 고정 자리로 모인다.

**정직한 구멍**: 어떤 채널이 이 변경 뒤 `fd` 를 한 번도 안 돌리면 그 자리는 영영 안
비워진다. `fd doctor` 가 *이 채널이 볼 수 있는* 옛 자리와 잔량을 찍고, **볼 수 있는 범위가
채널에 한정된다는 사실 자체를 함께 찍는다.** 안 잰 축을 잰 척하지 않는다(§13 의 규율).

## 6. 반증된 주장이 적힌 자리를 전부 고친다

이 레포는 주석이 판단의 저장소라, 틀린 주석을 남기면 다음 사람이 그 위에서 돈다.

| 자리 | 지금 | 정정 |
|---|---|---|
| `config.go:15` | "열화 상태(캐시·아웃박스)는 채널마다 따로여도 되기 때문이다" | 축은 열화 여부가 아니라 재생성 가능성이다 — 캐시는 갈려도 되고 아웃박스는 안 된다 |
| `env.go:20` | `StateDir` = "열화 상태(캐시·아웃박스)를 두는 자리" | 캐시만. 아웃박스가 왜 떠났는지 `OutboxPath` 를 가리킨다 |
| `env.go:74` | "상태 디렉토리는 … 환경 의존이 **설계 의도**다" | 캐시에 한해 유효하다고 좁힌다 |
| `outbox.go:169` | "상태 디렉토리 아래의 대기열 하나다" | 고정 자리 |
| `cmds.go:447` | "아웃박스는 상태 디렉토리마다 따로 쌓인다 … 이 채널의 대기다" | 이 머신의 대기다 + 옛 자리 잔량 + 이 채널이 못 보는 범위 |
| `DESIGN.md:398` | "상태는 `${CLAUDE_PLUGIN_DATA}` 에 둔다" | 캐시는 거기, 아웃박스·격리는 고정 자리 — 사유와 함께 |

## 7. 시험

**축을 박는 것 하나가 핵심이다.**

- **`TestOutboxPathIsChannelIndependent`** — 채널 환경 셋(`CLAUDE_PLUGIN_DATA` 있음 /
  `XDG_STATE_HOME` 만 / 둘 다 없음)에서 **같은 경로**가 나온다.
  `TestConfigPathIsChannelIndependent`·`TestAllChannelsAgreeOnMachineID` 와 같은 모양이다.
  산문이 아니라 이것이 규칙을 지킨다.

나머지:

- `TestAdoptDrainsLegacyOutboxes` — 옛 자리의 pending·rejected 가 고정 자리로 오고,
  원본은 `.migrated-*` 로 **남아 있다**(안 지웠다).
- `TestAdoptIsIdempotent` — 두 번 돌려도 중복 키가 안 생긴다.
- `TestConcurrentAdoptClaimsOnce` — `-race` 로 동시 `adopt`. `.migrated-*` 가 정확히 하나,
  대상에 중복 키 0. **§4 의 TTL 구멍이 닫혔다는 단정이 여기다.**
- `TestAdoptFailureKeepsClaimAndSelfHeals` — 대상을 못 쓰게 만들면 `.claimed-*` 가 남고
  `notice` 가 뜬다. 복구 후 다음 `adopt` 가 고아를 집는다.
- `TestDoctorReportsLegacyLeftoversAndItsOwnBlindness` — doctor 가 옛 자리 잔량과
  "이 채널이 볼 수 있는 범위에 한정된다"를 함께 찍는다.

### 기존 시험 하나를 고친다

`TestOfflineStateLandsUnderPluginDataNotPluginRoot`(`degrade_path_test.go:339`).

이 시험의 **본 판정은 384행의 "`${CLAUDE_PLUGIN_ROOT}` 아래에 아무것도 안 생긴다"이고
그것은 그대로 유효하다.** 379행의 "아웃박스가 plugin-data 아래에 있다"가 부수 단정인데
그것이 지금 반증된 것이다.

캐시 단정(368-372행)과 PLUGIN_ROOT 순회(384-400행)는 **손대지 않고** 아웃박스 단정만
고정 자리로 옮기며, 이름도 지키는 것에 맞게 바꾼다. 보호를 약화하는 것이 아니라
갈라진 두 축을 시험에서도 가르는 것이다.

### 확인만 하고 지나갈 것

`TestUnpinnedEnvReachesEveryStateDirBranch`·`TestUnpinnedRunActuallyUsesTheResolvedStateDir`·
`TestEnvAxisInventoryIsCurrent` 는 `StateDir`(캐시) 축이라 그대로 유효할 것으로 본다.
새 환경 축을 안 만들기 때문이다(`OutboxPath` 는 `FD_STATE_DIR`·`HOME` 만 읽는다).
**다만 doctor 출력이 한 줄 늘어나므로 문자열 단정에 걸리는지는 실제로 돌려 확인한다.**

## 8. 안 하는 것

- **캐시는 안 옮긴다** — 재생성 가능하고, `${CLAUDE_PLUGIN_ROOT}` 회피 논거가 그대로 유효하다.
- **옛 machine-id 두 벌은 안 건드린다** — 별개 축이고 이미 판정이 끝났다
  (`MachineIDPath`: 물려받지 않는다). 이 머신에 실제로 두 벌이 남아 있지만 그것은
  더 이상 읽히지 않는 잔해다.
- **`Append` 의 잠금 없는 동시성 구멍은 안 고친다** — `client.go:69-78` 이 전제로 명시했고
  `TestServeNeverOverlapsBackend` 가 지킨다. 청구는 **채널 간** 중복만 닫는다.
  같은 디렉토리 안의 경합은 여전히 그 전제 위에 있다.
- **남의 채널 자리를 glob 으로 추측하지 않는다**(§13).
- **`fd doctor` 에 이전 실행 부작용을 넣지 않는다** — 흡수는 `Replay` 경로에서만 돈다.
  doctor 는 읽고 찍기만 한다.
