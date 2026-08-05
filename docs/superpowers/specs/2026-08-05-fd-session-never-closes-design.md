# 세션을 닫는 경로를 만든다 — 설계

항목: `fd-session-never-closes`
날짜: 2026-08-05

## 1. 무엇이 틀렸나 (실측)

2026-08-05 05:55 UTC, context-platform 보드가 "① 지금 — 살아 있는 세션" 아래 카드 26장을 냈다.
`/proc` 으로 잰 결과 그 프로젝트에서 실제로 살아 있는 `claude` 프로세스는 **5개**였다
(cwd=`/home/aaron/cdo-dev/context-platform`, 전부 tmux `axh` 의 별개 pane:
pid 2367712·2381501·2648360·2649352·2650987). 그 5대화가 카드 6장이고 **나머지 20장은 이미 죽은
프로세스의 시체**다. 창 밖은 41건, done 이 아닌 세션 행은 총 67건이었다.

원인은 셋이고 전부 코드로 확인했다.

**① 세션을 닫는 프로덕션 경로가 하나도 없다.**
`model.SessionDone` 을 쓰는 자리가 `internal/legacy/session.go`(옛 도구 임포터) 하나뿐이다.
`PATCH /api/v1/sessions/{id}` 라우트는 서버에 있는데 **부르는 클라이언트가 없다** —
`cmd/fd`·`internal/mcpsrv` 전수에서 `signals`·`rekey`·`prescriptions` 만 부른다.

**② 그래서 "살아 있다"의 유일한 판정이 창(2시간)이다.**
`internal/store/session.go` 의 `ListLive` 는 `state <> 'done' AND (opened_at >= cut OR 신호 >= cut)` 인데,
아무도 done 으로 안 내리므로 앞 절은 한 행도 못 거른다. 죽은 세션이 나가는 길은 2시간 뒤에
스스로 조용해지는 것뿐이다.

**③ 세션을 여는 것 자체가 신호를 찍는다.**
`internal/service/session.go` 의 `OpenSession` 안에 `t.Beat(sess.ID, model.SignalMCP, now)` 가 있다.
그래서 claude 를 켜기만 하고 아무것도 안 해도 그 카드는 2시간 동안 살아 있는 세션에 뜬다.
실측상 카드 26장 중 **16장이 신호가 `mcp` 하나뿐이고, 그 시각이 `opened_at` 과 소수점까지 같고,
발자국 0**이었다.

이 설계는 ①만 고친다. ②·③은 화면·판정 축이라 별도 항목이다(§7).

## 2. 기각한 것 — SessionEnd 로 프로세스 종료를 잡는 길

항목 본문의 첫 제안은 "SessionEnd 훅으로 세션을 닫는다"였다. **지을 수 없다.**

설치본 **2.1.221·2.1.222 바이너리를 직접 뜯어** 확인했다. `executeSessionEndHooks` 를 부르는 자리는
번들 전체에 정확히 둘뿐이다:

```js
o3t("clear",  …)   // clearConversation 안 — /clear
o3t("resume", …)   // 돌고 있는 REPL 의 콜백 안 — /resume·/fork 로 대화를 갈아탈 때
```

zod 스키마의 `reason` 열거값은 여섯이지만
(`clear`·`resume`·`logout`·`prompt_input_exit`·`other`·`bypass_permissions_disabled`)
**뒤 넷은 아무도 안 쏜다.** `prompt_input_exit` 문자열 바로 옆에는
`"Session keeps running. Use /stop to end it."` 이 박혀 있다.
훅 이벤트 31종 전수에도 프로세스 종료를 알리는 이벤트가 없다.

**결론: 플랫폼은 "이 프로세스가 사라진다"를 알려 주지 않는다.**
그래서 창을 닫고 나간 세션·`tmux kill`·SIGKILL 은 이 설계로도 안 잡힌다. 그 한계를 화면이 말한다(§5).

### `resume` 을 matcher 에 안 넣는 이유

`reason` 은 **지금 세션이 끝나는 이유**를 말한다. `"resume"` 은 "재개 때문에 끝난다"이지
"재개가 시작된다"가 아니다 — 그래서 논리적으로는 닫는 것이 맞다. 그래도 뺀다:

1. 실효가 거의 없다. `/resume` 뒤에 `SessionStart(resume)` 가 따라오고 비콘 rekey 가 같은 카드를
   새 cc 로 옮긴 뒤 조각 1이 되살린다. 순 효과는 rekey 가 거절된 갈래뿐인데 그건 `clear` 가 이미 덮는다.
2. **`/fork` 도 같은 `reason: "resume"` 으로 온다**(위 콜백의 `$r === "fork"` 갈래가 같은 호출을 지난다).
   fork 에서 원본 카드를 닫는 것이 옳은지는 별도 판단이고 지금 그 근거가 없다.
3. matcher 한 단어라 나중에 넣으면 된다.

## 3. 조각 1 — 열면 살아난다 (안전핀, 먼저 짓는다)

`internal/store/session.go` 의 `Tx.OpenSession` 은 3중키로 기존 행을 찾으면 label 말고는
아무것도 안 건드리고 그대로 돌려준다. **그래서 done 카드는 그 세션이 계속 일해도 영원히 done 이다.**

이 구멍을 안 막고 닫기부터 넣으면, rekey 로 이어진 카드가 done 인 채 남아 **살아 있는 세션이 보드에서
사라진다** — 이 저장소가 이미 겪은 사고(`internal/legacy/export.go`: "pid 死로 살아 있는 세션을
죽었다고 판정")의 재현이다. 그래서 이것이 조각 2·3의 전제다.

**규칙: `OpenSession` 이 기존 행을 찾았을 때 `state == done` 이면 `active` 로 되돌린다.**

- 죽음은 판정하지 않는다. **삶만 복구한다** — `internal/service/board.go` 머리말의 원칙과 같은 방향이다.
- `blocked` 는 안 건드린다. blocked 는 사람이 사유와 함께 남긴 것이고, 여는 것이 그 사유를 지우면 안 된다.
- 안전망의 폭이 넓다: `cmd/fd/hook.go` 의 `beatFromHook` 이 신호를 남기기 **전에** 반드시 `a.OpenSession`
  을 부르므로, UserPromptSubmit·PostToolUse·MCP 도구 호출·세션 시작이 전부 이 자리를 지난다.
  즉 **사람이 말을 걸거나 도구를 쓰는 순간 카드가 되살아난다.**

스키마는 안 바꾼다. 마이그레이션 없음.

## 4. 조각 2 — 사람이 닫는다

### `fd close [--why …]`

`PATCH /api/v1/sessions/{id}` 를 부르는 **첫 클라이언트**다. 서버·서비스·API 계층은 안 고친다.

- `cmd/fd/app.go` 에 `CloseSession` 메서드 하나. `Rekey` 와 같은 이유로 `a.cli.do` 를 쓴다(`a.cli.Write` 가
  아니다) — `offline.go` 의 정책표가 모르는 명령을 거절하고, 무엇보다 **닫기는 지금의 사실이지 나중에
  재생할 사실이 아니다.** 낡은 닫기를 나중에 재생하면 그 사이 되살아난 세션을 다시 죽인다.
- 서버가 안 닿으면 그 사실을 그대로 내고 종료코드 1이다. 조용히 성공한 척하지 않는다.

### `fd finish <item> --close`

`finish` 는 **기본으로 세션을 안 닫는다.** 항목 하나를 끝내도 세션은 다음 항목으로 갈 수 있고,
거기서 자동으로 닫으면 살아 있는 세션이 보드에서 사라진다 — 조각 1이 다음 훅에서 되살리지만
그 사이 창의 겹침 판정이 이 세션을 못 본다.

`--close` 를 준 경우에만 닫는다. 구현은 **호출 둘**이다(항목 finish → 세션 close). 한 트랜잭션이
아니므로, finish 는 됐는데 close 가 실패하면 **그 사실을 그대로 낸다** — 둘 다 성공한 척하지 않는다.

### 선점이 남아 있으면 거절한다

done 카드는 `ListLive` 에서 빠지고, 그러면 그 세션이 든 선점이 **아무에게도 안 보인다** —
항목을 아무도 못 집는데 누가 잡았는지도 안 보이는 상태가 된다.

그래서 `fd close` 는 이 세션이 선점한 항목이 남아 있으면 **거절하고**, 무엇이 남았는지와
`fd finish <item>` 을 먼저 하라는 처방을 낸다. 우회 플래그는 두지 않는다 — 우회할 필드가 있으면
우회된다.

### `skills/fd-handoff`

마지막 단계로 세션 닫기를 넣는다. 이 스킬의 "안 하는 것" 목록에 있는
**"세션 해제 — 해제라는 개념이 없다(신호의 나이만 있다)"** 한 줄을 지운다. 이 항목이 그 전제를 뒤집었다.

## 5. 조각 3 — `/clear` 가 닫는다

`plugins/flightdeck/hooks/hooks.json` 에 `SessionEnd` 블록을 더한다. **matcher 는 `clear` 만.**
`cmd/fd/hook.go` 에 `case "session-end"` 갈래와 `hookSessionEnd` 함수를 더하고,
`HookPayload` 에 `reason` 을 받는 필드 한 줄을 더한다.

순서는 플랫폼이 보장한다 — `clearConversation` 이 `await o3t("clear", …)` 를 **먼저 기다리고** 나서
clear 를 진행하며, 그 뒤에 `SessionStart(clear)` 가 온다.

| rekey | 지금 | 이 변경 뒤 |
|---|---|---|
| 성공 | 카드 1장 (옛 cc→새 cc) | 카드 1장 — 닫혔다가 `SessionStart` 의 `OpenSession` 이 되살린다 |
| 거절 | **카드 2장** — 죽은 cc 의 고아가 남는다 | 카드 1장 — 고아가 닫힌 채 사라진다 |

여기서도 **선점을 든 카드는 안 닫는다.** rekey 가 거절되면 그 선점이 통째로 안 보이게 되기 때문이다.

훅은 §4의 거절 규칙을 그대로 쓴다. 그리고 **훅은 절대 세션을 막지 않는다** — `runHook` 의 계약대로
어떤 실패도 종료코드 0이고 사유는 stderr 로그로만 간다.

## 6. 화면이 한계를 말한다

`plugins/flightdeck/DESIGN.md` 에 세션 수명 문단을 더한다. 반드시 적을 것:

- `SessionEnd` 는 `clear` 와 `resume` 에서만 온다(2.1.221·2.1.222 실측). **프로세스가 죽을 때는 안 온다.**
- 그러므로 창을 닫고 나간 세션·`tmux kill`·SIGKILL 은 **여전히 창 2시간으로만 사라진다.**
- 닫기는 관측이지 판정이 아니다. 이 도구는 무응답에서 죽음을 추론하지 않는다.

이 문단이 없으면 다음 사람은 "닫히니까 카드는 항상 정확하다"고 믿는다. 그 믿음이 회수·회피 판단의
상류가 되면 이 도구가 이미 두 번 겪은 오판이 다시 난다.

## 7. 일부러 범위 밖에 둔 것

- **화면 접기** — "활동 신호가 하나도 없는 카드"를 보드에서 접는 축. 실측상 26장 중 16장이 그것이다.
  `internal/service/board.go`·`internal/web/page.go`·`internal/mcpsrv/render.go` 를 만져야 하고
  지금 여러 세션이 그 파일들을 잡고 있다.
- **`signal.kind` 에 `open` 추가** — `OpenSession` 이 찍는 열림 비트가 `mcp` 로 위장하는 문제.
  `schema.sql` 의 CHECK 를 바꿔야 해서 마이그레이션이 필요하고, 증분 004 는 다른 세션이 쓰는 중이다.
- **`resume`·`fork` matcher** — §2 참조.
- **자동 회수** — 죽은 세션의 선점을 자동으로 반납하는 것. 그것은 판정이라 이 설계가 안 한다.

## 8. 시험

1. `OpenSession` 이 `done` 카드를 만나면 `active` 로 되돌린다. 같은 3중키로 다시 열어 확인한다.
2. `OpenSession` 은 `blocked` 카드의 상태와 사유를 **안 건드린다.**
3. `fd close` 는 선점이 남아 있으면 거절하고, 무엇이 남았는지를 낸다.
4. `fd close` 는 선점이 없으면 `PATCH /sessions/{id}` 를 `state=done` 으로 부른다.
5. `fd finish` 는 `--close` 없이는 세션을 **안 닫는다.**
6. `fd finish --close` 는 항목을 끝내고 세션도 닫는다. close 만 실패하면 그 사실이 출력에 남는다.
7. `session-end` 훅이 `reason=clear` 면 카드를 닫는다.
8. `session-end` 훅은 `reason` 이 `clear` 가 아니면 **아무것도 안 한다**(matcher 를 못 믿는 경우의 이중 방어).
9. `session-end` 훅은 선점이 남아 있으면 안 닫는다.
10. `session-end` 훅은 어떤 실패에서도 종료코드 0이다.
11. `/clear` 왕복: 닫기 → `SessionStart` → rekey 성공 시 카드가 1장이고 `active` 다.
