# flightdeck

병렬 Claude Code 세션의 조정 계층. 서버 하나에 여러 프로젝트·여러 세션이 붙는다.

세션끼리 대화할 수단이 없어 "저쪽이 뭘 집었나"를 추측하게 되고, 그 추측이 틀리면
남의 작업을 통째로 집는 사고가 난다. flightdeck 은 그 추측을 없앤다 —
누가 살아 있나 · 어느 경로를 만지나 · 무엇을 집었나를 **git 과 DB 에서 파생해서** 낸다.

설계 정본은 [`DESIGN.md`](DESIGN.md) 다. 여기 없는 것은 만들지 않는다.

---

## 5분 설치

### 1. 서버를 띄운다 (한 머신에서 한 번)

```bash
cd plugins/flightdeck
docker compose up -d
curl -s localhost:7420/healthz
```

`{"ok":true,"api_version":"1","db_ok":true,…}` 가 나오면 됐다.
화면은 <http://localhost:7420> — 읽기 전용 한 장이다.

도커 없이 돌리려면:

```bash
cd server && go run ./cmd/fd serve --addr :7420 --db ~/.flightdeck/fd.db
```

### 2. 플러그인을 켠다 (세션을 돌리는 머신마다)

이 레포가 마켓플레이스다. `/plugin` 에서 `flightdeck` 을 켜거나, 설정에 직접 넣는다.

켜면 다음이 자동으로 붙는다:

| 무엇 | 언제 |
|---|---|
| `SessionStart` 훅 | 세션을 등록하고 보드 요약·내 선점·미확인·**서버 상태 배너**를 주입한다 |
| `UserPromptSubmit` 훅 | `prompt` 신호 + 미확인 알림 |
| `PostToolUse`(Edit\|Write) 훅 | `tool` 신호 + **미커밋 발자국** — 경로 겹침 축의 유일한 원천 |
| `PreCompact` 훅 | 압축 직전 좌표를 초안 판단으로 남긴다 |
| MCP 도구 6개 | `board` `pick` `note` `add` `finish` `alloc` |
| 스킬 2개 | `fd-pickup` · `fd-handoff` |

`bin/fd` 는 셸 런처다. 첫 훅이 `server/` 를 빌드해 `${CLAUDE_PLUGIN_DATA}` 에 캐시한다.
**Go 가 없으면 안내만 내고 세션은 그대로 진행된다**(훅은 세션을 막지 않는다).

### 3. 서버가 다른 머신이면 주소를 알려준다

```bash
export FD_URL=http://<서버머신>:7420
export FD_TOKEN=<서버와 같은 토큰>   # 서버에 FD_TOKEN 을 줬을 때만
```

토큰을 안 주면 인증이 꺼진다. **그 사실은 `/healthz` 가 알린다** — 조용히 열어 두지 않는다.

---

## 쓰는 법

세션 안에서는 MCP 도구를 부르면 된다.

```
board                      지금 누가 무엇을 만지고 있나
pick                       추천 1건 + 왜 + 탈락 사유 전부. **선점하지 않는다**
pick(item_id: "t2-abc")    선점 + 항목 본문 + 연결된 판단 + 브랜치·워크트리 명령
note(kind: "ask", body: …) 남이 건드리면 곤란한 것
add(id, title, body)       큐 항목
finish(item_id, outcome, body, followups)   판단+후속+종료+반납을 한 번에
alloc(counter_name)        원자 발번(개정 차수 같은 논리 카운터)
```

터미널에서는 같은 것을 `fd` 로 한다.

```bash
fd status                 # 서버 상태 배너 + 보드
fd next                   # 추천만
fd pick <item-id>         # 선점
fd note --kind decision --body "왜 그렇게 했나"
fd finish <item-id> --outcome done --body "① 왜 ② 기각 ③ 안 한 것 ④ 확인만 한 것"
fd doctor                 # 이 머신의 플랫폼 축과 서버 상태를 실제로 잰다
```

---

## 서버가 죽으면 (L1)

`SessionStart` 가 배너를 **명시 주입**한다. 조용히 두면 에이전트가 조정 기구가 있는 줄 알고 움직인다.

```
⚠ 조정 서버 미도달(http://localhost:7420, 마지막 접속 14:02 · 37분 전).
  되는 것: 코드 작성·커밋·조사 전부. 이미 선점한 항목의 작업.
  안 되는 것: 새 항목 선점 · 다른 세션의 현재 상태.
  아래는 14:02 시점의 스냅숏이다. 그 뒤 남이 무엇을 집었는지는 알 수 없다.
```

- **읽기** — 마지막 성공 응답을 낡음 배너와 함께 낸다. 침묵하지 않는다.
- **판단·노트** — 아웃박스에 쌓이고 재연결 시 **멱등 재생**된다. 종료코드 0.
- **선점** — 거절된다. 배타는 서버만 보장할 수 있고, 오프라인 획득을 허용하면 배타가 거짓이 된다.
- **발번** — 거절된다. 오프라인에서 발급하면 두 세션이 같은 번호를 쓴다.

상태는 `${CLAUDE_PLUGIN_DATA}` 에 둔다 — `${CLAUDE_PLUGIN_ROOT}` 는 갱신마다 경로가 바뀐다.

---

## 뭔가 안 될 때

```bash
fd doctor
```

플랫폼 축을 **하나씩 이름으로** 낸다. `CLAUDE_CODE_SESSION_ID` 가 `✗` 면 세션 정체의 원천이 끊긴 것이고,
그때 이 도구는 세션을 지어내지 않고 거절한다. 부재를 기본값으로 접으면 그 사실이 영영 안 보인다.

| 증상 | 볼 곳 |
|---|---|
| 보드가 비어 있다 | `fd doctor` 의 `FD_URL` · 서버 로그의 `기동` 줄 · 다른 세션이 같은 서버를 보나 |
| 훅이 아무것도 안 한다 | `bin/fd` 를 직접 돌려 봐라. Go 가 없으면 안내가 나온다 |
| 포트가 안 열린다 | 서버 로그의 `서버를 띄우지 못했다` 줄에 처방이 함께 있다 |
| 도구가 안 보인다 | `.mcp.json` 의 `type` 이 `stdio` 인가 — 없으면 서버가 통째로 스킵된다 |

---

## 안 만든 것

머지 큐·러너(Tier B) · 이벤트 소싱 · 표류 자동 탐지기 · RBAC · 선점의 오프라인 재생.
각각의 이유는 `DESIGN.md` §11 에 있다.
