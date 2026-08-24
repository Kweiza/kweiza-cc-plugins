[English](README.md) | **한국어**

# Claude Code Plugins

개인용 Claude Code 플러그인 마켓플레이스.

> **이 저장소는 한글이 정본이다.** 문서를 고칠 일이 생기면 `.ko.md` 를 먼저 고치고 영문을
> 맞춘다. 커밋·판단·설계 문서가 전부 한글이라 그 방향이 현실과 맞는다.
>
> GitHub 은 언어별 README 를 **자동으로 안 고른다** — `README.md` 만 렌더링한다(GitLab 은
> `README.<lang>.md` 를 지원한다. 여기가 갈린다). 그래서 맨 윗줄의 링크가 그 역할을 대신하고,
> 파일명은 ISO 639-1 을 쓰는 사실상의 표준 관례(`README.ko.md`)를 따랐다.

## 설치

```
/plugin marketplace add kweiza/kweiza-cc-plugins
/plugin install grafik-bar@kweiza-cc-plugins
/plugin install session-handoff@kweiza-cc-plugins
/plugin install flightdeck@kweiza-cc-plugins
```

## 플러그인

### grafik-bar

그래픽 상태줄: 로그인·작업 폴더·git 브랜치·모델·추론 강도·컨텍스트 창·5시간/7일 사용량 한도와
초기화까지 남은 시간·세션 통계(비용, 변경 줄 수, 경과 시간)를 낸다. 배치는 조각을 조립한 뒤
그 폭을 실제로 재서 터미널 폭에 맞게 줄을 가른다 — 한글·이모지가 두 칸을 먹는 것까지 센다.

**셋업 명령이 없다.** 설치만 하면 `SessionStart` 훅이 `~/.claude/settings.json` 의 `statusLine` 을
플러그인 자신의 스크립트로 가리키고 그 상태를 유지한다. 설치된 플러그인을 직접 참조하므로
플러그인을 갱신하면 자동으로 반영된다. 훅은 멱등이고 `statusLine` 키만 건드린다(나머지 설정은
그대로 둔다). `jq` 가 필요하다.

> 새 버전으로 올리는 것은 Claude Code 의 마켓플레이스 플러그인 갱신이 처리한다.
> 훅은 언제나 설치된 판을 따라간다.

### session-handoff

세션 핸드오프 — 진행 상황을 `.claude/handoffs/` 아래 파일로 남기고, 다음 세션을 계획하고,
시작 프롬프트를 써 둔다. 대화가 아니라 파일에 사는 덕에 `/clear` 와 컨텍스트 초기화를 넘어
살아남는다. 다음 세션에서 `/session-resume` 으로 이어 받는다.

| 스킬 | 설명 |
|-------|-------------|
| `/session-handoff` | 세션을 마무리하고 맥락을 핸드오프 파일 + 메모리에 저장, 다음 세션 프롬프트 작성 |
| `/session-resume` | 저장한 핸드오프를 다시 읽어 하던 일을 잇는다 — `list`, 또는 날짜·키워드로 고른다(기본값: 가장 최근) |

### flightdeck

병렬 Claude Code 세션의 조정 계층. 자체 호스팅 서버 하나(Docker)에 여러 머신·여러 저장소의
세션이 등록된다.

한 제품에 세션 열 개를 돌리면 그들끼리 대화할 수단이 없어 서로가 무엇을 집었는지를 *추측*하게
된다. 그 추측이 틀리면 남이 이미 하고 있는 일을 통째로 집는다. flightdeck 은 그 추측을 없앤다 —
누가 살아 있나, 어느 경로를 만지나, 무엇을 집었나, 무엇이 랜딩됐나가 전부 **git 과 데이터베이스에서
파생되고** 손으로 베끼지 않는다.

- **락이 아니라 선점이 있는 큐.** `pick` 이 항목을 집으면 그 항목 id 가 그대로 브랜치 이름이자
  워크트리 경로가 된다. 배타로 쥐는 것은 반드시 그래야 하는 하나뿐이다.
- **머지 전에 보이는 경로 겹침.** `PostToolUse` 훅이 미커밋 발자국을 보고하므로 "우리 둘 다 그
  파일을 고치고 있다"가 아직 쌀 때 드러난다.
- **랜딩 레인.** 머지 앞에 선 직렬 큐 — `fd land` 는 자기 차례가 아니면 0 이 아닌 값으로 끝나므로
  `fd land && <머지 명령>` 한 줄이 그대로 옳다.
- **판단.** 파생할 수 없는 유일한 자산이다: 왜 그렇게 했나, 무엇을 기각했나, 무엇을 일부러 안 했나.
  항목을 따라다니며 다음에 그것을 집는 사람에게 간다.
- **읽기 전용 보드** — `localhost:7420`. 그리고 `SessionStart` 훅이 같은 보드를 새 세션마다 주입한다.
  서버가 안 닿을 때는 그 배너까지 함께 넣는다 — 없는 조정 기구가 있다고 믿고 움직이는 에이전트가
  없도록.

| 스킬 | 설명 |
|-------|-------------|
| `/fd-setup` | 이 머신을 셋업한다 — 상태를 재고, 서버인지 클라이언트인지 정하고, 없는 것만 설치·기동 |
| `/fd-pickup` | 세션을 시작한다: 보드 → 추천 → 선점 → 그 항목에 연결된 판단 읽기 |
| `/fd-handoff` | 마무리한다: 판단 + 후속 + 항목 종료 + 자원 반납을 한 호출·한 트랜잭션으로 |
| `/fd-update` | 서버·플러그인·DB 를 최신으로 올린다 |

MCP 도구 여덟(`board` `pick` `note` `add` `finish` `alloc` `land` `label`)과 `fd` CLI 가 같은 동작을
에이전트에게도 사람에게도 낸다. 서버에는 Docker(또는 Go)가 필요하다.

안내서: [`plugins/flightdeck/README.ko.md`](plugins/flightdeck/README.ko.md) ·
설계 정본: [`plugins/flightdeck/DESIGN.md`](plugins/flightdeck/DESIGN.md)
