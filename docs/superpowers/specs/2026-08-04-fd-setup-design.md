# fd setup — 설치 자동화 설계

작성 2026-08-04 · 항목 `fd-setup-skill` · 상태 **승인됨**

## 문제

플러그인을 켜도 바로 못 쓴다. 바이너리는 런처가 알아서 빌드하지만(Go 가 있어야 한다),
**서버·DB·주소·토큰은 사람이 README 를 읽고 손으로 맞춰야 한다.** 새 머신마다 그 절차가 반복된다.

원하는 것: 설치 후 한 번의 호출로 셋업이 끝난다. 이미 셋업돼 있으면 그 사실을 알고,
구성을 바꿀지 묻는다.

## 조사에서 나온 제약 (설계를 실제로 바꾼 것들)

### ① 설계가 스킬 수를 2로 못박았다

```
DESIGN.md:58   MCP 도구는 6개, 스킬은 2개, 에이전트·커맨드는 0개다.  (§1 원칙 ②)
README.md:9    설계 정본은 DESIGN.md 다. 여기 없는 것은 만들지 않는다.
plugin_test.go:215   SKILL.md 는 60줄 미만이어야 한다.
```

근거는 컨텍스트 예산이다 — 스킬 목록은 컨텍스트의 1%에서 절단되고 **덜 쓰는 것부터 버려진다**.
`fd-setup` 은 정의상 가장 덜 쓰는 스킬(머신당 1회)이라 절단 1순위다.

**그래서 판정을 Go 로 뺀다.** 스킬이 얇아져 60줄에 들어가고, 잘려도 `fd setup` 이 남는다.

### ② fd 는 설정 파일을 한 줄도 안 읽는다

`FD_URL`·`FD_TOKEN` 은 환경변수뿐이고 CLI 플래그조차 없다(`client.go:108,112`).
그리고 **상태 디렉토리에 저장하는 안은 원리적으로 실패한다** — 그 축은 훅·MCP
(`CLAUDE_PLUGIN_DATA`)와 사용자 셸(`~/.local/state`)로 **일부러** 갈리게 만든 것이다
(`env.go:20-62`). 이 머신에 실제로 두 벌이 존재한다.

`settings.json` 의 `env` 가 훅·MCP 에 전달되는지는 **미검증**이다(공식 문서에 명시 없음).
미검증 기구 위에 설계를 얹지 않는다.

### ③ Ubuntu 의 Go 패키지가 너무 낮다 — 실측

```
go.mod 요구        go 1.25.0
apt 후보           2:1.22~2build1     ← 빌드 실패한다
snap latest/stable 1.26.5             ← 맞다
```

**계획은 존재가 아니라 버전을 검사해야 한다.** 이것이 없으면 "자동 설치"가 성공한 뒤
빌드가 깨지고, 사용자는 원인을 모른다.

### ④ Windows 는 지금 구조로 안 된다

- Claude Code 는 훅 shell-form 을 **Git Bash 없는 Windows 에서 PowerShell 로** 돌린다
  (CLI 바이너리의 스키마 문구 실측). 지금 hooks.json 의 따옴표 시작 명령은 PowerShell 에서 파싱 오류다.
- `.mcp.json` 은 `shell:false` 로 직접 spawn 하고 **스키마에 OS 분기 필드가 없다**.
- 즉 진입점 `bin/fd`(bash)를 안 고치면 훅도 MCP 도 안 뜬다.

**이 설계는 Windows 를 고치지 않는다. 정직하게 거절한다.**

## 설계

### 계층

```
fd setup                        순수 판정 + 사람이 읽는 보고
fd setup --url <U> [--token <T>]  config.json 을 원자적으로 0600 으로 쓴다
skills/fd-setup/SKILL.md        묻고 · 승인받고 · 실행하고 · 검증한다 (60줄 미만)
```

파일 형식을 **코드가 소유한다** — 스킬이 JSON 을 손으로 쓰지 않는다.

### 설정 — `~/.flightdeck/config.json`

자리 근거는 새로 만드는 규칙이 아니라 **있는 규칙**이다. `MachineIDPath`(env.go:66-93)가
이미 같은 판정을 내렸다: 채널 무관해야 하는 값은 상태 디렉토리가 아니라 `~/.flightdeck` 에 둔다.

```jsonc
{ "url": "http://10.0.0.5:7420", "token": "…" }
```

| 축 | 규칙 |
|---|---|
| 우선순위 | `FD_URL` 환경변수 > `config.json` > `http://127.0.0.1:7420` |
| 왜 env 가 이기나 | 기존 규율과 같은 결 — 사람이 명시 지정한 축이 이긴다(`FD_STATE_DIR`·`FD_PROJECT`) |
| 반환 | `(Config, source, warn)` — `MachineID` 가 이미 이 모양이다 |
| 깨진 파일 | **죽이지 않는다.** warn 을 올리고 env/기본값으로 간다. 다만 조용하지 않다 |
| 토큰 권한 | 0600 으로 쓴다. 더 넓으면 경고한다(거절은 안 한다) |

**역할(서버/클라이언트)은 저장하지 않고 파생한다** — URL 이 루프백이면 서버, 아니면 클라이언트다.
저장하면 의도와 현실이 갈리고, 이 레포는 "파생 가능한 값에 파라미터를 두면 틀린 값이 들어온다"를
원칙으로 삼는다. **도달성(healthz)은 별개 축**이라 따로 낸다 — "서버로 설정됐다"와 "서버가 떠 있다"는 다르다.

### 판정 — 순수 함수

```go
DetectRole(url string) Role                 // 루프백이면 RoleServer
GoVersionOK(v string) (ok bool, why string) // 1.22 거절 · 1.25 통과
PlanSetup(SetupState) SetupPlan             // 관측 → 순서 있는 할 일
RenderSetupPlan(SetupPlan) string           // 사람이 읽는 문구
```

`SetupState` 는 전부 **관측된 값**이다(OS·arch·go 버전·docker 유무·config 출처·healthz 결과).
관측은 부수효과 있는 자리에서 하고, 판정은 그 값만 본다 — 시험이 표로 전 조합을 돈다.

### OS별 명령

| OS | Go (필수) | Docker (선택) |
|---|---|---|
| macOS | `brew install go` | `brew install --cask docker` |
| Ubuntu/Debian | `sudo snap install go --classic` — **apt 는 1.22 라 못 쓴다** | `sudo apt-get install -y docker.io docker-compose-v2` |
| Windows | 거절 + WSL 안내 | — |

**서버 상시 실행은 레포에 정식 방법이 없다**(systemd·launchd 파일 0건). 그래서 계획이 둘을
**구분해서** 낸다: `docker compose up -d`(restart 정책이 있는 지원 경로)와 포그라운드 실행
(지금만 쓰는 임시). 없는 것을 있는 척하지 않는다.

### 스킬 — 얇은 껍데기

```
1. fd setup 을 부른다
2. 이미 구성돼 있으면 → 바꿀지 묻는다
3. 아니면 → 서버냐 클라이언트냐 묻고, 클라이언트면 주소를 묻는다
4. 계획의 명령을 **보여주고 승인받아** 실행한다
5. fd setup --url 로 저장하고, fd doctor 로 검증한다
6. 안 하는 것
```

자동 설치를 승인 없이 하지 않는다 — sudo/관리자를 요구하고 되돌리기 어렵다.

### 반드시 말해야 하는 것

저장 직후 **"다음 세션부터 적용된다"**. MCP 서버는 기동 시 환경을 한 번 읽고 끝이라
(이 세션에서 `/proc` 으로 실측했다) 지금 도는 프로세스는 새 설정을 못 본다.
이걸 안 말하면 사용자는 "설정했는데 안 된다"를 겪는다.

## 오류 처리

| 상황 | 답 |
|---|---|
| Windows | 무엇이 왜 안 되는지 이름으로 대고 WSL 경로 안내. 성공을 흉내내지 않는다 |
| 포트 점유 | 감지해서 알린다. `restart: unless-stopped` 때문에 재시작 루프가 된다 |
| config 깨짐 | warn + env/기본값. 죽지 않는다 |
| Go 가 낮음 | 버전과 필요한 버전을 함께 대고 맞는 설치 명령을 낸다 |
| 서버 미도달 | 역할과 도달성을 **따로** 낸다 — 설정 문제와 기동 문제를 안 뭉갠다 |

## 시험

- `DetectRole` — 루프백 표기 여러 형태(`127.0.0.1`·`localhost`·`::1`·`0.0.0.0`) vs 원격
- **`GoVersionOK`** — `1.22` 거절 · `1.25.0` 통과 · `1.26.5` 통과 (이번에 찾은 함정)
- `PlanSetup` — OS × go(없음/낮음/맞음) × docker × 역할 × 도달성 표
- config — 왕복 · **우선순위(env 가 이긴다)** · 깨진 파일이 warn 만 내는가 · 0600
- 이음매 — 하네스로 `fd setup --url` → `fd doctor` 가 **출처**를 찍는가

## 함께 고치는 것

| 파일 | 왜 |
|---|---|
| `DESIGN.md` | 스킬 2→3 + **왜 셋이어도 되는가**(숫자만 바꾸면 근거를 지우는 것이다) · `fd setup` 등재 |
| `plugin_test.go:217` | 이름 슬라이스 — 안 넣으면 새 스킬만 60줄 검사를 통째로 안 받는다 |
| `README.md` | 5분 설치에 스킬 경로 한 줄. **절차를 복제하지 않고 가리킨다** |
| `cmds.go`(doctor) | 서버 주소·토큰의 **출처**를 찍는다(machineSrc 가 선례다) |

## 범위 밖

**Windows 에서 경로 겹침이 조용히 죽는 결함** — `RelPath` 는 OS 구분자를, `judge.components` 는
`/` 를 쓴다. 오류가 아니라 "겹침 없음"으로 나와 정상과 구분되지 않는다. 별도 항목으로 낸다.

## 구현 순서

1. `config.go` + 시험 (TDD)
2. `setup.go` 순수 판정기 + 표 시험
3. `app.go`·`client.go` 배선 + `doctor` 출처 줄
4. `fd setup` 서브명령 + 하네스 이음매 시험
5. `skills/fd-setup/SKILL.md` + `plugin_test.go` 슬라이스
6. `DESIGN.md`·`README.md`
7. 전체 검증 → 커밋 → main ff-merge → push
