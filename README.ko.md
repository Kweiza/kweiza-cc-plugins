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

병렬 에이전트 세션(Claude Code · codex)의 조정 계층. 자체 호스팅 서버 하나(Docker)에 여러 머신·여러
저장소의 세션이 등록되고, 누가 살아 있나 · 어느 경로를 만지나 · 무엇을 집었나 · 무엇이 랜딩됐나를
**git 과 데이터베이스에서 파생해** 낸다. 락 대신 선점이 있는 큐, 머지 전에 보이는 경로 겹침, 랜딩 레인,
그리고 항목을 따라다니는 판단이 그 표면이다.

**2026-09-24 부터 단독 저장소 [Kweiza/flightdeck](https://github.com/Kweiza/flightdeck) 에서 개발한다.**
이 마켓플레이스의 `flightdeck` 항목은 그 저장소를 가리킨다. 그래서 위의
`/plugin install flightdeck@kweiza-cc-plugins` 는 그대로 동작하고 `/plugin update` 도 그쪽 판을 받아 온다.
안내서·설계 정본은 그 저장소에 있고, 0.38.3 까지의 이력은 이 저장소에 남아 있다.
