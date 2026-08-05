# 처방(prescription) 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 기계가 판정할 수 있는 순간에 세션이 무엇을 남겨야 하는지가 그 자리에서 나오고, 안 남겼다는 사실이 관측 가능해진다.

**Architecture:** 판정은 `internal/judge` 의 순수 함수 하나(`Prescribe`)에 모은다. 상태는 기존 `event` 표(추가 전용)에 `prescribe`·`prescribe_ack` 두 종류로 쌓아 새 테이블을 만들지 않는다. 배달은 새 `Stop` 훅이 `POST /api/v1/sessions/{id}/prescriptions` 를 쳐서 stdout 으로 낸다. 읽는 쪽(보드)은 사건을 카드에 붙이고 접기를 관련성 순으로 바꾼다.

**Tech Stack:** Go 1.25 · SQLite(WAL) · `net/http` `ServeMux` 패턴 라우팅 · 표준 `testing`

**Spec:** `docs/superpowers/specs/2026-08-04-prescription-design.md` (커밋 `40ed7b8`, 개정 `07310fc`)

## Global Constraints

- **작업 디렉토리는 `plugins/flightdeck/server` 다.** 모든 `go` 명령은 여기서 돈다.
- **MCP 도구는 6개, 스킬은 3개, 테이블은 12개로 고정이다.** 이 계획은 셋 다 안 늘린다. 늘리는 변경은 설계 위반이다(DESIGN §1 원칙 ②, §3).
- **판정 로직은 `internal/judge` 의 순수 함수에 있어야 한다.** 핸들러·서비스 본문에 판정을 쓰면 시험이 로직의 사본을 단정하게 된다(DESIGN §12).
- **다중 조건 판정은 불리언이 아니라 사유 문자열을 반환한다.** 결과만 찍는 단정은 통과시키지 않는다(DESIGN §12).
- **단정은 소비자의 좌표계로 쓴다.** 대상은 훅 stdout · MCP 응답 문자열 · 렌더 출력이다. 내부 구조체를 단정하면 자기가 막는 축을 원리적으로 못 본다(DESIGN §12).
- **새 검사는 망가진 것을 넣어 빨간불을 먼저 확인한다.** 초록만 보고 통과로 단정하지 않는다.
- **훅은 전부 fail-open 이다.** `runHook` 의 반환값은 항상 0 이고, 서버가 죽어도 세션을 막지 않는다.
- **근거 없는 상수에는 근거가 없다고 주석에 적는다.** `SilentNewPaths`·`SilentGap`·`DefaultLiveWindow` 셋이 해당한다(DESIGN §10 이 고발한 "ttl 근거 주석이 하나뿐"을 반복하지 않는다).
- **주석과 사용자 문구는 한국어다.** 커밋 메시지는 영어다(레포 관례: `git log` 참조).
- 전체 시험: `go test ./...`

---

## File Structure

| 파일 | 책임 | 상태 |
|---|---|---|
| `internal/judge/prescribe.go` | 처방 판정 순수 함수 전부 | 신규 |
| `internal/judge/prescribe_test.go` | 조건 넷 × 억제 조합 전수 | 신규 |
| `internal/store/event.go` | `ListSessionEvents` 추가 | 수정 |
| `internal/service/prescribe.go` | 입력 조립 + `judge` 호출 + 발화 기록 | 신규 |
| `internal/service/judgment.go` (또는 `Note` 가 있는 파일) | ack 경로 | 수정 |
| `internal/api/handlers_session.go` | `POST /sessions/{id}/prescriptions` | 수정 |
| `internal/api/api.go` | 라우트 등록 | 수정 |
| `cmd/fd/hook.go` | `stop` 훅 분기 + 렌더 | 수정 |
| `cmd/fd/render.go` | 훅 stdout 문구 | 수정 |
| `hooks/hooks.json` | `Stop` 훅 등록 | 수정 |
| `internal/mcpsrv/render.go` | 카드에 사건 · 관련성 접기 · 창 밖 줄 | 수정 |
| `internal/mcpsrv/tools.go` | `note.kind` enum 에서 `now` 제거 | 수정 |
| `internal/service/service.go` | `DefaultLiveWindow` 2시간 | 수정 |
| `DESIGN.md` | §6 훅·REST · §10 지표 · §13 실측 | 수정 |

---

### Task 1: `Stop` 훅 실측 — 이 계획의 유일한 미검증 의존

DESIGN §13 은 "추측을 사실로 적지 않는다"고 못박았다. `Stop` 훅은 이 레포가 한 번도 안 쓴 훅이고, **stdout 이 다음 턴 컨텍스트로 주입되는지 이 머신에서 잰 적이 없다.** 재기 전에는 Task 7 을 쓸 수 없다.

**★ 이 태스크는 플러그인을 안 통한다.** 설치된 flightdeck 는 GitHub 마켓플레이스
(`kweiza/kweiza-cc-plugins`)에서 오므로 레포의 `hooks/hooks.json` 을 고쳐도 도는 세션에는
안 붙는다 — 릴리스를 태워야 붙는다. 그런데 **재려는 것은 플러그인이 아니라 플랫폼 동작**
(Stop 훅 stdout 이 주입되나)이라 그 릴리스는 순수한 낭비다.
그래서 프로젝트 `.claude/settings.json` 에 임시 훅을 걸어 잰다.

**★ 이 태스크만 주 작업트리(`/home/aaron/cdo-dev/kweiza-cc-plugins`)에서 돈다.**
프로젝트 설정은 **도는 세션의 프로젝트 디렉토리**에서 읽히기 때문이다. Task 2 부터는 워크트리다.

**Files:**
- Create: `.claude/settings.json` (임시 — Step 7 에서 지운다)
- Create: `.claude/stop-probe.sh` (임시 — Step 7 에서 지운다)
- Modify: `plugins/flightdeck/DESIGN.md` §13

**Interfaces:**
- Consumes: 없음
- Produces: DESIGN §13 에 기록된 사실 셋. Task 7 이 어느 채널로 배달할지가 여기서 정해진다.

- [ ] **Step 1: 탐침 스크립트를 쓴다**

`.claude/stop-probe.sh`:

```sh
#!/bin/sh
# Stop 훅 실측 탐침 — **임시다.** 계획 Task 1 Step 7 이 이 파일을 지운다.
#
# 재는 것 셋:
#   ① stdin 페이로드에 session_id 가 오나 · stop_hook_active 가 오나
#   ② stdout 이 다음 턴 컨텍스트에 주입되나
#   ③ 주입된다면 JSON hookSpecificOutput 이 해석되나, 아니면 평문으로 실리나
#
# ③ 을 한 판에 가르려고 JSON 을 낸다. 다음 턴에 보이는 것으로 갈린다:
#   FD-PROBE-CTX 만 보인다      → JSON 이 해석됐다. additionalContext 가 채널이다
#   중괄호째 JSON 이 보인다      → 평문 stdout 이 주입됐다. JSON 은 해석 안 된다
#   아무것도 안 보인다          → 주입 자체가 없다. Task 7 은 UserPromptSubmit 으로 간다
set -u
dir="$(dirname "$0")"
cat > "$dir/stop-probe-stdin.json"
printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"Stop","additionalContext":"FD-PROBE-CTX"}}'
```

실행 권한을 준다: `chmod +x .claude/stop-probe.sh`

- [ ] **Step 2: 훅을 건다**

`.claude/settings.json`:

```json
{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "\"$CLAUDE_PROJECT_DIR/.claude/stop-probe.sh\"",
            "timeout": 5
          }
        ]
      }
    ]
  }
}
```

- [ ] **Step 3: 스크립트가 혼자서도 도는지 본다**

Run: `echo '{"session_id":"x"}' | .claude/stop-probe.sh && cat .claude/stop-probe-stdin.json`
Expected: `{"hookSpecificOutput":…"FD-PROBE-CTX"}` 가 출력되고 stdin 파일에 `{"session_id":"x"}` 가 있다

- [ ] **Step 4: 여기서 멈추고 컨트롤러에게 넘긴다**

**구현자는 여기까지다.** 남은 것은 사람이 세션을 다시 띄워야 하는 일이라 서브에이전트가 못 한다.
보고서에 이렇게 적고 DONE 으로 반환한다: "탐침 준비 완료. 사람이 세션을 재시작한 뒤
`FD-PROBE-CTX` 가 보이는지 판정해야 한다."

- [ ] **Step 5: (컨트롤러) 사람에게 실측을 요청한다**

> 프로젝트 `settings.json` 에 훅을 새로 걸었습니다. 이 세션은 시작할 때 읽은 설정으로
> 도니까 **Claude Code 를 한 번 재시작**해 주십시오. 그 뒤 아무 프롬프트나 하나 보내고,
> 그 다음 턴에 `FD-PROBE-CTX` 또는 `hookSpecificOutput` 이라는 글자가 컨텍스트에
> 보이는지 알려 주십시오. 안 보이면 "안 보인다"가 그대로 답입니다.

- [ ] **Step 6: (컨트롤러) 세 축을 읽고 판정한다**

```bash
cat .claude/stop-probe-stdin.json
```

읽는 것: `session_id` 키가 있나 · `stop_hook_active` 키가 있나 · `cwd` 가 있나.

| 다음 턴에 보인 것 | 판정 | Task 7 의 배달 채널 |
|---|---|---|
| `FD-PROBE-CTX` 만 | JSON 이 해석된다 | `Stop` 훅 stdout, `hookSpecificOutput.additionalContext` 형식 |
| JSON 문자열째 | 평문이 주입된다 | `Stop` 훅 stdout, 평문 |
| 아무것도 | 주입이 없다 | `UserPromptSubmit`. `Stop` 은 발화 기록만 하고 문구는 다음 프롬프트에 나간다 |
| (stdin 파일이 아예 없다) | 훅이 안 불렸다 | 위와 같다. 그리고 그 사실도 §13 에 적는다 |

- [ ] **Step 7: DESIGN §13 에 사실로 적고 탐침을 지운다**

`plugins/flightdeck/DESIGN.md` 의 `### 확인됨` 또는 `### 아직 아님` 에 축 셋을 결과대로 적는다.
**추측을 적지 않는다** — 안 재진 축은 "아직 아님"에 남긴다. 확인됨에 적을 때는 이 레포의
관례대로 실측 날짜와 Claude Code 판을 함께 적는다.

그리고 탐침을 지운다:

```bash
rm -rf .claude
```

**탐침을 남기지 않는 이유**: `.claude/settings.json` 이 남으면 이 레포를 여는 모든 세션에
탐침 훅이 걸리고, 그러면 다음 사람이 "이게 뭔가"를 조사하는 비용을 문다.
실측의 산출물은 스크립트가 아니라 **DESIGN §13 의 한 줄**이다.

- [ ] **Step 8: 커밋**

```bash
git add plugins/flightdeck/DESIGN.md
git commit -m "measure(flightdeck): find out what the Stop hook carries and whether it injects"
```


### Task 2: `judge.Prescribe` — 판정 순수 함수

**Files:**
- Create: `internal/judge/prescribe.go`
- Test: `internal/judge/prescribe_test.go`

**Interfaces:**
- Consumes: `judge.LiveSession{ID, Label, Paths}`(기존, `eligible.go`) · `judge.PathsOverlap`·`judge.OverlapPairs`(기존, `paths.go`)
- Produces:
  - `judge.ClaimView{ItemID string; Paths []string}`
  - `judge.PrescribeInput{Now time.Time; SessionID string; Claims []ClaimView; TurnPaths []string; Others []LiveSession; LastJudgment time.Time; NewPaths int; Emitted map[string]time.Time}`
  - `judge.Prescription{Key, Reason, Text string}`
  - `judge.Prescribe(PrescribeInput) []Prescription`
  - `judge.FoldPrescriptions([]Prescription) (shown []Prescription, folded int)`
  - 상수 `judge.PrescribeOverlap`·`PrescribeOutside`·`PrescribeSilent`·`PrescribeUnclaimed`·`PrescribeMax`·`SilentNewPaths`·`SilentGap`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/judge/prescribe_test.go`:

```go
package judge

import (
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

// keys 는 처방 키만 뽑는다. 순서도 단정 대상이다.
func keys(ps []Prescription) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Key)
	}
	return out
}

func TestPrescribe(t *testing.T) {
	other := LiveSession{ID: "01SESSIONOTHER", Label: "", Paths: []string{"cmd/fd/hook.go"}}

	cases := []struct {
		name     string
		in       PrescribeInput
		wantKeys []string
	}{
		{
			name: "조사만 하는 세션 — 넷 다 안 뜬다",
			in: PrescribeInput{
				Now: t0, SessionID: "me", TurnPaths: nil, Others: []LiveSession{other},
				LastJudgment: t0.Add(-5 * time.Hour), NewPaths: 0,
			},
			wantKeys: nil,
		},
		{
			name: "남과 겹치기 시작했다",
			in: PrescribeInput{
				Now: t0, SessionID: "me", TurnPaths: []string{"cmd/fd/hook.go"},
				Others: []LiveSession{other}, LastJudgment: t0, NewPaths: 1,
			},
			wantKeys: []string{"overlap:01SESSIONOTHER", "unclaimed"},
		},
		{
			name: "같은 상대와 다시 겹쳐도 안 뜬다",
			in: PrescribeInput{
				Now: t0, SessionID: "me", TurnPaths: []string{"cmd/fd/hook.go"},
				Others: []LiveSession{other}, LastJudgment: t0, NewPaths: 1,
				Emitted: map[string]time.Time{
					"overlap:01SESSIONOTHER": t0.Add(-time.Minute), "unclaimed": t0.Add(-time.Minute),
				},
			},
			wantKeys: nil,
		},
		{
			name: "자기 자신과는 안 겹친다",
			in: PrescribeInput{
				Now: t0, SessionID: "01SESSIONOTHER", TurnPaths: []string{"cmd/fd/hook.go"},
				Others: []LiveSession{other}, LastJudgment: t0, NewPaths: 1,
			},
			wantKeys: []string{"unclaimed"},
		},
		{
			name: "선언 경로 밖 — 경로마다 하나",
			in: PrescribeInput{
				Now: t0, SessionID: "me",
				Claims:   []ClaimView{{ItemID: "fd-x", Paths: []string{"internal/judge"}}},
				TurnPaths: []string{"internal/judge/prescribe.go", "cmd/fd/hook.go"},
				LastJudgment: t0, NewPaths: 2,
			},
			wantKeys: []string{"outside:cmd/fd/hook.go"},
		},
		{
			name: "선언 경로가 하나도 없으면 outside 축이 안 돈다",
			in: PrescribeInput{
				Now: t0, SessionID: "me",
				Claims:   []ClaimView{{ItemID: "fd-x", Paths: nil}},
				TurnPaths: []string{"cmd/fd/hook.go"},
				LastJudgment: t0, NewPaths: 1,
			},
			wantKeys: nil,
		},
		{
			name: "선점이 있으면 unclaimed 는 안 뜬다",
			in: PrescribeInput{
				Now: t0, SessionID: "me",
				Claims:   []ClaimView{{ItemID: "fd-x", Paths: []string{"cmd/fd"}}},
				TurnPaths: []string{"cmd/fd/hook.go"}, LastJudgment: t0, NewPaths: 1,
			},
			wantKeys: nil,
		},
		{
			name: "silent — 경로 임계",
			in: PrescribeInput{
				Now: t0, SessionID: "me",
				Claims:   []ClaimView{{ItemID: "fd-x", Paths: []string{"cmd/fd"}}},
				TurnPaths: []string{"cmd/fd/hook.go"}, LastJudgment: t0, NewPaths: SilentNewPaths,
			},
			wantKeys: []string{"silent"},
		},
		{
			name: "silent — 시간 임계는 새 경로가 있어야 걸린다",
			in: PrescribeInput{
				Now: t0, SessionID: "me",
				Claims:   []ClaimView{{ItemID: "fd-x", Paths: []string{"cmd/fd"}}},
				TurnPaths: []string{"cmd/fd/hook.go"},
				LastJudgment: t0.Add(-SilentGap), NewPaths: 1,
			},
			wantKeys: []string{"silent"},
		},
		{
			name: "silent — 시간이 지나도 새 경로가 0이면 안 뜬다",
			in: PrescribeInput{
				Now: t0, SessionID: "me",
				Claims:   []ClaimView{{ItemID: "fd-x", Paths: []string{"cmd/fd"}}},
				LastJudgment: t0.Add(-10 * SilentGap), NewPaths: 0,
			},
			wantKeys: nil,
		},
		{
			name: "silent 은 판단 뒤에 다시 뜬다",
			in: PrescribeInput{
				Now: t0, SessionID: "me",
				Claims:   []ClaimView{{ItemID: "fd-x", Paths: []string{"cmd/fd"}}},
				TurnPaths: []string{"cmd/fd/hook.go"}, NewPaths: SilentNewPaths,
				LastJudgment: t0.Add(-time.Minute),
				Emitted:      map[string]time.Time{"silent": t0.Add(-2 * time.Minute)},
			},
			wantKeys: []string{"silent"},
		},
		{
			name: "silent 은 무시하면 안 다시 뜬다",
			in: PrescribeInput{
				Now: t0, SessionID: "me",
				Claims:   []ClaimView{{ItemID: "fd-x", Paths: []string{"cmd/fd"}}},
				TurnPaths: []string{"cmd/fd/hook.go"}, NewPaths: SilentNewPaths,
				LastJudgment: t0.Add(-3 * time.Minute),
				Emitted:      map[string]time.Time{"silent": t0.Add(-2 * time.Minute)},
			},
			wantKeys: nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := keys(Prescribe(c.in))
			if len(got) != len(c.wantKeys) {
				t.Fatalf("키 수가 다르다: got %v, want %v", got, c.wantKeys)
			}
			for i := range got {
				if got[i] != c.wantKeys[i] {
					t.Fatalf("키가 다르다(%d번째): got %v, want %v", i, got, c.wantKeys)
				}
			}
			// **사유가 비면 실패다.** 결과만 찍는 단정을 통과시키지 않는다(설계 §12).
			for _, p := range Prescribe(c.in) {
				if strings.TrimSpace(p.Reason) == "" {
					t.Fatalf("사유가 비었다: key=%s", p.Key)
				}
				if strings.TrimSpace(p.Text) == "" {
					t.Fatalf("문구가 비었다: key=%s", p.Key)
				}
			}
		})
	}
}

// 문구가 무엇을 부를지를 실제로 말하는지 본다. 소비자가 읽는 것은 이 문자열이다.
func TestPrescribeTextNamesTheCall(t *testing.T) {
	ps := Prescribe(PrescribeInput{
		Now: t0, SessionID: "me", TurnPaths: []string{"cmd/fd/hook.go"},
		Others:       []LiveSession{{ID: "01OTHER", Paths: []string{"cmd/fd/hook.go"}}},
		LastJudgment: t0, NewPaths: 1,
	})
	if len(ps) == 0 {
		t.Fatal("처방이 하나도 안 나왔다")
	}
	if !strings.Contains(ps[0].Text, "note(kind='ask'") {
		t.Fatalf("겹침 처방이 부를 도구를 안 말한다: %q", ps[0].Text)
	}
	if !strings.Contains(ps[0].Text, "cmd/fd/hook.go") {
		t.Fatalf("겹침 처방이 경로를 안 말한다: %q", ps[0].Text)
	}
	if !strings.Contains(ps[0].Text, "01OTHER") {
		t.Fatalf("겹침 처방이 상대를 안 말한다: %q", ps[0].Text)
	}
}

func TestFoldPrescriptions(t *testing.T) {
	mk := func(n int) []Prescription {
		out := make([]Prescription, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, Prescription{Key: "k", Reason: "r", Text: "t"})
		}
		return out
	}
	shown, folded := FoldPrescriptions(mk(2))
	if len(shown) != 2 || folded != 0 {
		t.Fatalf("상한 아래인데 접었다: shown=%d folded=%d", len(shown), folded)
	}
	shown, folded = FoldPrescriptions(mk(10))
	if len(shown) != PrescribeMax || folded != 10-PrescribeMax {
		t.Fatalf("상한을 안 지켰다: shown=%d folded=%d", len(shown), folded)
	}
}
```

- [ ] **Step 2: 빨간불을 먼저 본다**

Run: `go test ./internal/judge/ -run 'Prescribe|Fold' -v`
Expected: FAIL — `undefined: PrescribeInput` 등 컴파일 오류

- [ ] **Step 3: 구현한다**

`internal/judge/prescribe.go`:

```go
package judge

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// 처방 — 사건이 남게 만드는 강제 지점(설계 §6 "규율은 응답에 싣는다 — 필요할 때만, 그 자리에서").
//
// ★ 상태가 아니라 **전이**에서만 뜬다. (세션 × Key) 당 1회다.
// 조건이 지속되는 동안 매번 뜨면 설계 §4 가 고발한 실패를 재현한다 —
// 살아 있는 5건 중 3건에 경고가 붙어 판별력이 0이 된 그 화면이다.

const (
	PrescribeOverlap   = "overlap"   // overlap:<상대 세션 id>
	PrescribeOutside   = "outside"   // outside:<경로>
	PrescribeSilent    = "silent"    // 대상 없음
	PrescribeUnclaimed = "unclaimed" // 대상 없음
)

// PrescribeMax 는 한 번에 **문구로** 내는 처방 수다. 넘는 것은 요약 한 줄이 되지만
// **키는 호출자가 전부 발화 기록한다** — 요약된 것도 이미 낸 것이다.
// 대규모 리팩터 한 턴이 outside 처방 수십 건을 쏟는 경로를 이 상수가 막는다.
const PrescribeMax = 3

// silent 임계.
//
// ★ **이 두 값에는 근거가 없다.** 창 2시간과 같은 성질의 잠정값이고, 발화율이 실측되면
// 조정한다. 근거 없는 상수를 근거 있는 척 두지 않는 것이 설계 §10 이 요구한 것이다 —
// 그 절이 고발한 것은 "락 다섯 중 ttl 근거 주석이 있는 것이 하나뿐"이었다.
//
// ★ 그리고 **`tool` 신호 횟수는 쓸 수 없다.** signal 표의 PK 가 (session_id, kind) 라
// 종류별 한 행이고 갱신된다 — 횟수라는 값이 존재하지 않는다(schema.sql:91-96).
const (
	SilentNewPaths = 12
	SilentGap      = 60 * time.Minute
)

// ClaimView 는 이 세션이 쥔 항목 하나와 그 항목이 선언한 경로다.
type ClaimView struct {
	ItemID string
	Paths  []string
}

// PrescribeInput 은 처방 판정에 필요한 전부다. I/O 도 상태도 없다.
type PrescribeInput struct {
	Now       time.Time
	SessionID string
	Claims    []ClaimView
	// TurnPaths 는 마지막 처방 이후 새로 만진 경로다(처방이 없었으면 세션 시작 이후).
	TurnPaths []string
	// Others 는 살아 있는 세션 목록이다. 자기 자신이 섞여 있어도 이 함수가 뺀다 —
	// 호출자가 빼는 것에 의존하면 그 축이 시험 밖에 있게 된다.
	Others []LiveSession
	// LastJudgment 는 이 세션의 마지막 판단 시각이다.
	// **판단이 하나도 없으면 호출자가 세션 시작 시각을 넣는다** — 제로값이면 기준이 없어 안 뜬다.
	LastJudgment time.Time
	// NewPaths 는 마지막 판단 이후 새로 만진 경로 수다.
	NewPaths int
	// Emitted 는 이미 낸 키 → 낸 시각이다.
	//
	// ★ 불리언이 아니라 시각인 이유: 억제 해제 규칙(silent 은 판단 뒤 다시 뜬다)이
	// Emitted[key] 와 LastJudgment 의 대소 비교로 표현된다. 불리언으로 받으면 그 규칙이
	// 서비스 계층으로 새어 나가고, 그러면 §12 가 금지한 "시험이 사본을 단정하는" 모양이 된다.
	Emitted map[string]time.Time
}

// Prescription 은 처방 하나다.
type Prescription struct {
	Key    string // 억제 단위이자 전이 식별자
	Reason string // 왜 떴는가. 시험이 단정하는 축이다
	Text   string // 세션에게 낼 문구
}

// Prescribe 는 지금 내야 할 처방 전부를 낸다. 표시 상한은 FoldPrescriptions 가 건다.
//
// 순서는 고정이다: overlap → outside → unclaimed → silent.
// overlap 이 맨 앞인 이유는 **그것만이 남이 알아야 하는 사건**이기 때문이다.
// 나머지 셋은 이 세션의 규율 축이라 접혀도 남의 화면이 틀리지 않는다.
func Prescribe(in PrescribeInput) []Prescription {
	var out []Prescription
	out = append(out, overlapPrescriptions(in)...)
	out = append(out, outsidePrescriptions(in)...)
	if p, ok := unclaimedPrescription(in); ok {
		out = append(out, p)
	}
	if p, ok := silentPrescription(in); ok {
		out = append(out, p)
	}
	return out
}

// FoldPrescriptions 는 표시분과 접힌 수를 가른다. 순서는 Prescribe 가 정한 그대로다.
func FoldPrescriptions(ps []Prescription) (shown []Prescription, folded int) {
	if len(ps) <= PrescribeMax {
		return ps, 0
	}
	return ps[:PrescribeMax], len(ps) - PrescribeMax
}

// ① 남과 경로가 겹치기 시작했다 — 상대마다 1회.
func overlapPrescriptions(in PrescribeInput) []Prescription {
	others := append([]LiveSession(nil), in.Others...)
	// 순서를 고정한다 — 같은 입력에 같은 출력이어야 시험이 순서를 단정할 수 있다.
	sort.Slice(others, func(i, j int) bool { return others[i].ID < others[j].ID })

	var out []Prescription
	for _, o := range others {
		if o.ID == in.SessionID {
			continue
		}
		pairs := OverlapPairs(in.TurnPaths, o.Paths)
		if len(pairs) == 0 {
			continue
		}
		key := PrescribeOverlap + ":" + o.ID
		if suppressed(in, key) {
			continue
		}
		mine, theirs := pairs[0][0], pairs[0][1]
		out = append(out, Prescription{
			Key: key,
			Reason: fmt.Sprintf("이번에 만진 %s 가 세션 %s 의 발자국 %s 와 겹친다(겹친 쌍 %d)",
				mine, o.ID, theirs, len(pairs)),
			Text: fmt.Sprintf(
				"%s 를 만졌는데 세션 %s%s 도 %s 를 잡고 있다.\n"+
					"  → note(kind='ask', body='무엇을 왜 잡는가') 로 의도를 남겨라. "+
					"그 세션의 다음 프롬프트에 배달된다.",
				mine, o.ID, labelSuffix(o.Label), theirs),
		})
	}
	return out
}

// ② 선점한 항목의 선언 경로 밖 — 경로마다 1회.
//
// ★ 선언 경로가 하나도 없으면 이 축은 **안 돈다.** 빈 선언에 대고 "밖"을 판정할 수 없고,
// 빈 선언을 "전부 밖"으로 접으면 paths 를 안 적은 항목 하나가 첫 턴에 처방을 쏟는다.
func outsidePrescriptions(in PrescribeInput) []Prescription {
	declared := declaredPaths(in.Claims)
	if len(declared) == 0 {
		return nil
	}
	ids := claimIDs(in.Claims)
	var out []Prescription
	for _, p := range in.TurnPaths {
		if PathsOverlap([]string{p}, declared) {
			continue
		}
		key := PrescribeOutside + ":" + p
		if suppressed(in, key) {
			continue
		}
		out = append(out, Prescription{
			Key:    key,
			Reason: fmt.Sprintf("%s 는 선점 항목 %s 의 선언 경로(%s) 밖이다", p, ids, strings.Join(declared, " ")),
			Text: fmt.Sprintf(
				"%s 는 선점한 %s 가 선언한 경로 밖이다 — 남이 보는 겹침 판정의 입력이 낡았다.\n"+
					"  → 같은 작업이면 note(kind='decision') 으로 범위가 왜 늘었는지 남겨라.\n"+
					"  → 별개 작업이면 add(id=…, title=…, body=…, paths=['%s']) 로 항목을 만들어라.",
				p, ids, p),
		})
	}
	return out
}

// ③ 선점 없이 편집 — 세션당 1회.
//
// ★ 이 조건은 흔하다. 세션당 1회로 눌러 잡지 않으면 편집마다 떠서 §4 의 실패가 된다.
func unclaimedPrescription(in PrescribeInput) (Prescription, bool) {
	if len(in.Claims) > 0 || len(in.TurnPaths) == 0 || suppressed(in, PrescribeUnclaimed) {
		return Prescription{}, false
	}
	return Prescription{
		Key:    PrescribeUnclaimed,
		Reason: fmt.Sprintf("선점 0건인데 경로 %d개를 편집했다", len(in.TurnPaths)),
		Text: fmt.Sprintf(
			"항목을 선점하지 않고 %s 를 고치고 있다 — 큐에도 카드에도 무엇을 하는지가 없다.\n"+
				"  → pick(item_id=…) 로 집거나, 큐 밖 작업이면 note(kind='decision') 으로 "+
				"무엇을 왜 하는지 남겨라.",
			strings.Join(clipList(in.TurnPaths, 3), ", ")),
	}, true
}

// ④ 오래 일했는데 판단이 0건.
func silentPrescription(in PrescribeInput) (Prescription, bool) {
	reason, ok := silentReason(in)
	if !ok || suppressed(in, PrescribeSilent) {
		return Prescription{}, false
	}
	return Prescription{
		Key:    PrescribeSilent,
		Reason: reason,
		Text: "일한 뒤로 판단이 하나도 안 남았다 — 판단은 원리적으로 파생 불가한 유일한 자산이다(설계 §5).\n" +
			"  → note(kind='decision', body='무엇을 정했고 무엇을 기각했나') 로 남겨라.",
	}, true
}

// silentReason 은 판단 공백 조건과 그 사유다.
//
// ★ 시간 팔에 "새 경로 ≥ 1" 이 붙는다. 순수 OR 로 두면 조사만 하는 세션이 60분마다 걸리는데,
// 그 세션은 경로 축에서 아무도 안 막으므로 찌를 이유가 없다(설계 §5).
func silentReason(in PrescribeInput) (string, bool) {
	if in.NewPaths >= SilentNewPaths {
		return fmt.Sprintf("마지막 판단 이후 새 경로 %d개(임계 %d)", in.NewPaths, SilentNewPaths), true
	}
	if in.NewPaths == 0 || in.LastJudgment.IsZero() {
		return "", false
	}
	if gap := in.Now.Sub(in.LastJudgment); gap >= SilentGap {
		return fmt.Sprintf("마지막 판단 후 %d분(임계 %d분) · 그 사이 새 경로 %d개",
			int(gap.Minutes()), int(SilentGap.Minutes()), in.NewPaths), true
	}
	return "", false
}

// suppressed 는 이 키가 지금 눌려 있는지 본다.
//
// ★ silent 만 판단 뒤에 풀린다. 판단을 남기는 세션은 다시 조용해졌을 때 한 번 더 찔러야 하고,
// 한 번 무시한 세션을 계속 찌르는 것은 §4 가 고발한 상시 점등이다. **재촉은 이 설계에 없다.**
func suppressed(in PrescribeInput, key string) bool {
	at, ok := in.Emitted[key]
	if !ok {
		return false
	}
	if key != PrescribeSilent {
		return true
	}
	return !in.LastJudgment.After(at)
}

func declaredPaths(claims []ClaimView) []string {
	var out []string
	for _, c := range claims {
		out = append(out, c.Paths...)
	}
	return out
}

func claimIDs(claims []ClaimView) string {
	ids := make([]string, 0, len(claims))
	for _, c := range claims {
		ids = append(ids, c.ItemID)
	}
	return strings.Join(ids, ", ")
}

func labelSuffix(label string) string {
	if strings.TrimSpace(label) == "" {
		return ""
	}
	return "(" + label + ")"
}

func clipList(xs []string, n int) []string {
	if len(xs) <= n {
		return xs
	}
	return append(append([]string(nil), xs[:n]...), fmt.Sprintf("외 %d개", len(xs)-n))
}
```

- [ ] **Step 4: 초록을 본다**

Run: `go test ./internal/judge/ -run 'Prescribe|Fold' -v`
Expected: PASS 전부

- [ ] **Step 5: 망가뜨려 빨간불을 확인한다**

`suppressed` 의 `if key != PrescribeSilent { return true }` 를 `return false` 로 잠깐 바꾸고 시험을 돌린다.
Expected: `같은 상대와 다시 겹쳐도 안 뜬다` 가 FAIL. 확인 후 되돌린다.

- [ ] **Step 6: 커밋**

```bash
git add plugins/flightdeck/server/internal/judge/prescribe.go plugins/flightdeck/server/internal/judge/prescribe_test.go
git commit -m "feat(flightdeck): judge which moments earn a prescription, and which repeat"
```

---

### Task 3: `store.ListSessionEvents` — 억제 상태를 읽는 질의

**Files:**
- Modify: `internal/store/event.go`
- Test: `internal/store/event_session_test.go` (신규)

**Interfaces:**
- Consumes: 기존 `store.Store` · `model.Event{ID, At, Project, SessionID, Kind, Payload}`
- Produces: `func (s *Store) ListSessionEvents(ctx context.Context, sessionID, kind string, since time.Time) ([]model.Event, error)` — **오래된 순**

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/store/event_session_test.go`. 기존 `event_tx_test.go` 의 스토어 생성 헬퍼를 그대로 쓴다 (그 파일을 먼저 읽고 같은 헬퍼 이름을 쓴다).

```go
package store

import (
	"context"
	"testing"
	"time"
)

func TestListSessionEventsFiltersBySessionAndKind(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t) // ← event_tx_test.go 의 헬퍼와 같은 이름을 쓴다

	seedProjectAndSessions(t, s) // ← 세션 "sess-a", "sess-b" 를 만든다. 기존 헬퍼가 없으면 이 시험 안에 쓴다

	s.LogEvent(ctx, "prescribe", "proj", "sess-a", map[string]any{"key": "unclaimed"})
	s.LogEvent(ctx, "prescribe", "proj", "sess-a", map[string]any{"key": "overlap:x"})
	s.LogEvent(ctx, "prescribe", "proj", "sess-b", map[string]any{"key": "unclaimed"})
	s.LogEvent(ctx, "prescribe_ack", "proj", "sess-a", map[string]any{"keys": []string{"unclaimed"}})

	got, err := s.ListSessionEvents(ctx, "sess-a", "prescribe", time.Time{})
	if err != nil {
		t.Fatalf("조회 실패: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("건수가 다르다: got %d, want 2 (%+v)", len(got), got)
	}
	// **오래된 순이어야 한다** — 억제 판정이 "언제 냈나"를 보므로 최신순이면 호출자가 뒤집어야 하고,
	// 그 뒤집기를 잊으면 조용히 틀린다.
	if got[0].At.After(got[1].At) {
		t.Fatalf("오래된 순이 아니다: %v then %v", got[0].At, got[1].At)
	}
}

func TestListSessionEventsEmptyKindMeansAll(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProjectAndSessions(t, s)

	s.LogEvent(ctx, "prescribe", "proj", "sess-a", nil)
	s.LogEvent(ctx, "prescribe_ack", "proj", "sess-a", nil)

	got, err := s.ListSessionEvents(ctx, "sess-a", "", time.Time{})
	if err != nil {
		t.Fatalf("조회 실패: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("kind 가 비면 전 종류여야 한다: got %d", len(got))
	}
}
```

- [ ] **Step 2: 빨간불을 본다**

Run: `go test ./internal/store/ -run ListSessionEvents -v`
Expected: FAIL — `s.ListSessionEvents undefined`

- [ ] **Step 3: 구현한다**

`internal/store/event.go` 의 `ListEvents` 아래에 넣는다.

```go
// ListSessionEvents 는 세션 하나의 이벤트를 **오래된 순**으로 낸다. kind 가 비면 전 종류다.
//
// ★ ListEvents 와 정렬이 반대다. 그쪽은 "무슨 일이 있었나"(최신순)에 답하고, 이쪽은
// "이 키를 언제 냈나"(억제 판정)에 답한다. 최신순으로 주면 호출자가 뒤집어야 하고,
// 그 뒤집기를 잊으면 억제가 조용히 틀린다.
//
// ★ 상한이 없다. 세션 하나의 처방 이벤트는 조건 넷 × 대상 수라 원리적으로 작고,
// 상한을 걸면 오래된 키가 잘려 **이미 낸 처방이 다시 뜬다** — 이 함수가 막으려는 바로 그 사고다.
func (s *Store) ListSessionEvents(ctx context.Context, sessionID, kind string, since time.Time) ([]model.Event, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, at, project, session_id, kind, payload FROM event
		WHERE session_id = ? AND (? = '' OR kind = ?) AND at >= ?
		ORDER BY at ASC, id ASC`,
		sessionID, kind, kind, fmtTime(since))
	if err != nil {
		return nil, fmt.Errorf("세션 이벤트 조회 실패(session_id=%q kind=%q): %w",
			clip(sessionID, 64), clip(kind, 64), err)
	}
	defer rows.Close()

	var out []model.Event
	for rows.Next() {
		var e model.Event
		var project, session sql.NullString
		var at string
		if err := rows.Scan(&e.ID, &at, &project, &session, &e.Kind, &e.Payload); err != nil {
			return nil, fmt.Errorf("세션 이벤트 행 해석 실패: %w", err)
		}
		e.Project, e.SessionID = str(project), str(session)
		if e.At, err = parseTime(at); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("세션 이벤트 순회 실패: %w", err)
	}
	return out, nil
}
```

- [ ] **Step 4: 초록을 본다**

Run: `go test ./internal/store/ -run ListSessionEvents -v`
Expected: PASS

- [ ] **Step 5: 커밋**

```bash
git add plugins/flightdeck/server/internal/store/event.go plugins/flightdeck/server/internal/store/event_session_test.go
git commit -m "feat(flightdeck): read one session's ledger oldest-first, so suppression can ask when"
```

---

### Task 4: `service.Prescriptions` — 입력 조립과 발화 기록

**Files:**
- Create: `internal/service/prescribe.go`
- Test: `internal/service/prescribe_test.go`

**Interfaces:**
- Consumes: `judge.Prescribe`·`judge.FoldPrescriptions`·`judge.PrescribeInput`·`judge.ClaimView`(Task 2) · `store.ListSessionEvents`(Task 3) · 기존 `store.GetSession`·`ClaimedItems`·`GetItem`·`Footprints`·`ListJudgmentsBySession`·`ListLive`·`LogEvent`
- Produces:
  - `service.PrescribeResult{Shown []judge.Prescription; Folded int; All []judge.Prescription}`
  - `func (s *Service) Prescriptions(ctx context.Context, sessionID string) (PrescribeResult, error)`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/service/prescribe_test.go`. 기존 시험의 서비스 생성 헬퍼를 그대로 쓴다 (`internal/service/*_test.go` 를 먼저 읽고 같은 헬퍼를 쓴다).

```go
package service

import (
	"context"
	"strings"
	"testing"
)

// 처방이 발화되면 event 에 남고, 두 번째 호출에는 안 뜬다.
// **이것이 이 서비스의 유일한 불변식이다** — 억제가 DB 를 통해 돌아야 세션이 재시작해도 유효하다.
func TestPrescriptionsAreEmittedOnceAcrossCalls(t *testing.T) {
	ctx := context.Background()
	svc, st := newTestService(t) // ← 기존 헬퍼 이름에 맞춘다

	sess := openSessionForTest(t, svc, st) // ← 세션 하나를 연다
	touchPathForTest(t, st, sess, "cmd/fd/hook.go")

	first, err := svc.Prescriptions(ctx, sess)
	if err != nil {
		t.Fatalf("첫 호출 실패: %v", err)
	}
	if len(first.All) == 0 {
		t.Fatal("선점 없이 편집했는데 처방이 0건이다")
	}

	second, err := svc.Prescriptions(ctx, sess)
	if err != nil {
		t.Fatalf("둘째 호출 실패: %v", err)
	}
	if len(second.All) != 0 {
		t.Fatalf("같은 키가 다시 떴다: %+v", second.All)
	}

	evs, err := st.ListSessionEvents(ctx, sess, "prescribe", timeZero())
	if err != nil {
		t.Fatalf("이벤트 조회 실패: %v", err)
	}
	if len(evs) != len(first.All) {
		t.Fatalf("발화 기록 수가 다르다: events=%d, prescriptions=%d", len(evs), len(first.All))
	}
	if !strings.Contains(evs[0].Payload, `"key"`) {
		t.Fatalf("payload 에 key 가 없다: %s", evs[0].Payload)
	}
}

// 접힌 것도 발화 기록된다. 요약된 것은 "안 낸 것"이 아니다.
func TestFoldedPrescriptionsAreStillRecorded(t *testing.T) {
	ctx := context.Background()
	svc, st := newTestService(t)

	sess := openSessionForTest(t, svc, st)
	claimItemForTest(t, svc, st, sess, "fd-x", []string{"internal/judge"})
	for _, p := range []string{"a/1.go", "b/2.go", "c/3.go", "d/4.go", "e/5.go"} {
		touchPathForTest(t, st, sess, p)
	}

	res, err := svc.Prescriptions(ctx, sess)
	if err != nil {
		t.Fatalf("호출 실패: %v", err)
	}
	if res.Folded == 0 {
		t.Fatalf("5개 경로가 선언 밖인데 안 접혔다: shown=%d", len(res.Shown))
	}
	evs, _ := st.ListSessionEvents(ctx, sess, "prescribe", timeZero())
	if len(evs) != len(res.All) {
		t.Fatalf("접힌 것이 발화 기록에서 빠졌다: events=%d, all=%d", len(evs), len(res.All))
	}
}
```

- [ ] **Step 2: 빨간불을 본다**

Run: `go test ./internal/service/ -run Prescription -v`
Expected: FAIL — `svc.Prescriptions undefined`

- [ ] **Step 3: 구현한다**

`internal/service/prescribe.go`:

```go
package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
)

// 처방 — 발화 지점(설계 §6). 판정은 judge.Prescribe 가 하고 이 파일은 입력을 모으고 결과를 남긴다.
//
// ★ **세션 카드 파생을 안 돈다.** 이 경로는 턴마다 돌므로, git worktree list +
// 세션별 ChangedPaths·UncommittedPaths 를 얹으면 **모든 턴 종료에 저장소 전수 훑기가 붙는다**.
// 필요한 입력(footprint·claim·judgment·session)은 전부 DB 표라 git 을 안 탄다.
// 설계 §6 이 /notices 를 /dashboard.json 에서 가른 것과 같은 판정이다.

const (
	eventPrescribe    = "prescribe"
	eventPrescribeAck = "prescribe_ack"
)

// PrescribeResult 는 한 턴의 처방이다.
type PrescribeResult struct {
	Shown  []judge.Prescription `json:"shown"`  // 문구로 낼 것 (최대 judge.PrescribeMax)
	Folded int                  `json:"folded"` // 요약으로 접힌 수
	All    []judge.Prescription `json:"all"`    // 발화 기록된 전부
}

// prescribePayload 는 event.payload 의 모양이다.
type prescribePayload struct {
	Key    string `json:"key"`
	Reason string `json:"reason"`
}

// Prescriptions 는 이 세션이 지금 받아야 할 처방을 내고, 낸 것을 event 에 기록한다.
func (s *Service) Prescriptions(ctx context.Context, sessionID string) (PrescribeResult, error) {
	sess, err := s.st.GetSession(ctx, sessionID)
	if err != nil {
		return PrescribeResult{}, err
	}

	in := judge.PrescribeInput{Now: s.now(), SessionID: sessionID}

	// 억제 상태 — 이 세션이 이미 낸 키와 그 시각.
	emitted, since, err := s.emittedKeys(ctx, sessionID, sess.OpenedAt)
	if err != nil {
		return PrescribeResult{}, err
	}
	in.Emitted = emitted

	// 선점 항목과 각자의 선언 경로.
	claimed, err := s.st.ClaimedItems(ctx, sessionID)
	if err != nil {
		return PrescribeResult{}, err
	}
	for _, id := range claimed {
		it, err := s.st.GetItem(ctx, sess.Project, id)
		if err != nil {
			// 항목을 못 읽는 것은 처방을 못 낼 이유가 아니다. 조용히 접지 않고 남긴다.
			s.log.WarnContext(ctx, "처방: 선점 항목을 못 읽었다",
				"session_id", sessionID, "item", id, "error", err.Error())
			continue
		}
		in.Claims = append(in.Claims, judge.ClaimView{ItemID: it.ID, Paths: it.Paths})
	}

	// 이번 구간에 새로 만진 경로 · 마지막 판단 이후 새로 만진 경로.
	prints, err := s.st.Footprints(ctx, sessionID)
	if err != nil {
		return PrescribeResult{}, err
	}
	last, err := s.lastJudgmentAt(ctx, sessionID, sess.OpenedAt)
	if err != nil {
		return PrescribeResult{}, err
	}
	in.LastJudgment = last
	for _, f := range prints {
		if f.Origin != model.OriginObserved {
			continue // 선언·항목 경로는 "만졌다"가 아니다. 뭉개면 §3 이 가른 축이 사라진다
		}
		if f.LastAt.After(since) {
			in.TurnPaths = append(in.TurnPaths, f.Path)
		}
		if f.LastAt.After(last) {
			in.NewPaths++
		}
	}

	// 살아 있는 남의 세션. **창은 보드와 같은 것을 쓴다** — 두 자리에 두면 조용히 어긋난다.
	live, err := s.st.ListLive(ctx, sess.Project, s.cut(in.Now, s.window))
	if err != nil {
		return PrescribeResult{}, err
	}
	for _, v := range live {
		if v.Session.ID == sessionID {
			continue
		}
		in.Others = append(in.Others, judge.LiveSession{
			ID: v.Session.ID, Label: v.Session.Label, Paths: v.Paths,
		})
	}

	all := judge.Prescribe(in)
	shown, folded := judge.FoldPrescriptions(all)

	// **접힌 것도 기록한다.** 요약된 것은 "안 낸 것"이 아니다 —
	// 안 기록하면 다음 턴에 그대로 다시 떠서 상한이 무의미해진다.
	for _, p := range all {
		s.st.LogEvent(ctx, eventPrescribe, sess.Project, sessionID,
			prescribePayload{Key: p.Key, Reason: p.Reason})
	}
	if len(all) > 0 {
		s.log.InfoContext(ctx, "처방 발화",
			"session_id", sessionID, "count", len(all), "shown", len(shown), "folded", folded)
	}
	return PrescribeResult{Shown: shown, Folded: folded, All: all}, nil
}

// emittedKeys 는 이미 낸 키와 그 시각, 그리고 마지막 발화 시각을 낸다.
// 마지막 발화 시각이 "이번 구간"의 시작이다 — 없으면 세션 시작이다.
func (s *Service) emittedKeys(ctx context.Context, sessionID string, openedAt time.Time) (map[string]time.Time, time.Time, error) {
	evs, err := s.st.ListSessionEvents(ctx, sessionID, eventPrescribe, openedAt)
	if err != nil {
		return nil, time.Time{}, err
	}
	out := map[string]time.Time{}
	since := openedAt
	for _, e := range evs {
		var p prescribePayload
		if err := json.Unmarshal([]byte(e.Payload), &p); err != nil || p.Key == "" {
			// 해석 실패를 조용히 버리면 그 키가 안 눌린 것으로 보여 처방이 다시 뜬다.
			s.log.WarnContext(ctx, "처방 이벤트 payload 해석 실패",
				"session_id", sessionID, "payload", e.Payload)
			continue
		}
		out[p.Key] = e.At
		if e.At.After(since) {
			since = e.At
		}
	}
	return out, since, nil
}

// lastJudgmentAt 은 이 세션의 마지막 판단 시각이다.
// **판단이 하나도 없으면 세션 시작 시각을 낸다** — judge 쪽 제로값 규약(기준 없음)에 맞춘다.
func (s *Service) lastJudgmentAt(ctx context.Context, sessionID string, openedAt time.Time) (time.Time, error) {
	js, err := s.st.ListJudgmentsBySession(ctx, sessionID)
	if err != nil {
		return time.Time{}, err
	}
	out := openedAt
	for _, j := range js {
		if j.At.After(out) {
			out = j.At
		}
	}
	return out, nil
}
```

- [ ] **Step 4: 초록을 본다**

Run: `go test ./internal/service/ -run Prescription -v`
Expected: PASS

- [ ] **Step 5: 파생 비용이 안 늘었는지 확인한다**

Run: `go test ./internal/service/ -run Derive -v`
Expected: PASS — `Prescriptions` 는 `sessionCards` 를 안 부르므로 파생 누산기가 안 움직여야 한다.

- [ ] **Step 6: 커밋**

```bash
git add plugins/flightdeck/server/internal/service/prescribe.go plugins/flightdeck/server/internal/service/prescribe_test.go
git commit -m "feat(flightdeck): assemble prescriptions from tables only, never from git"
```

---

### Task 5: REST `POST /api/v1/sessions/{id}/prescriptions`

**Files:**
- Modify: `internal/api/handlers_session.go`
- Modify: `internal/api/api.go:184` 부근 (세션 라우트 묶음)
- Test: `internal/api/prescriptions_test.go` (신규)

**Interfaces:**
- Consumes: `service.Prescriptions`·`service.PrescribeResult`(Task 4)
- Produces: `POST /api/v1/sessions/{id}/prescriptions` → `200` + `{"shown":[…],"folded":N,"all":[…]}`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/api/prescriptions_test.go`. 기존 API 시험의 서버 기동 헬퍼를 그대로 쓴다.

```go
package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestPrescriptionsRouteReturnsShownAndFolded(t *testing.T) {
	srv, ctx := newTestServer(t) // ← 기존 헬퍼 이름에 맞춘다
	sess := seedSessionWithUnclaimedEdit(t, srv, ctx)

	res := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/"+sess+"/prescriptions", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("상태가 다르다: got %d, want 200 — body=%s", res.Code, res.Body.String())
	}
	var got struct {
		Shown  []map[string]any `json:"shown"`
		Folded int              `json:"folded"`
		All    []map[string]any `json:"all"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatalf("응답 해석 실패: %v — %s", err, res.Body.String())
	}
	if len(got.All) == 0 {
		t.Fatalf("처방이 0건이다: %s", res.Body.String())
	}
	if got.Shown[0]["key"] == nil || got.Shown[0]["text"] == nil {
		t.Fatalf("key·text 가 응답에 없다: %s", res.Body.String())
	}
}

func TestPrescriptionsRouteRejectsUnknownSession(t *testing.T) {
	srv, _ := newTestServer(t)
	res := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/nope/prescriptions", nil)
	if res.Code != http.StatusNotFound {
		t.Fatalf("모르는 세션에 404 가 아니다: got %d — %s", res.Code, res.Body.String())
	}
}
```

- [ ] **Step 2: 빨간불을 본다**

Run: `go test ./internal/api/ -run Prescriptions -v`
Expected: FAIL — 404 (라우트 없음) 또는 컴파일 오류

- [ ] **Step 3: 라우트를 등록한다**

`internal/api/api.go` 의 세션 라우트 묶음(184행 `handleWorkspace` 다음)에 넣는다:

```go
	// 처방은 턴마다 돈다. **세션 카드 파생을 안 도는 표면이다** — /notices 와 같은 이유(설계 §6).
	mux.HandleFunc("POST /api/v1/sessions/{id}/prescriptions", s.handlePrescriptions)
```

- [ ] **Step 4: 핸들러를 쓴다**

`internal/api/handlers_session.go` 끝에:

```go
// handlePrescriptions 는 이 세션이 지금 받아야 할 처방을 내고 발화를 기록한다.
//
// ★ POST 인 이유는 **부작용이 있어서**다 — 낸 것이 event 에 남는다.
// GET 으로 두면 프록시·재시도가 조용히 처방을 소모한다.
//
// ★ 본문 인자가 없다. 필요한 것은 전부 세션 id 로부터 파생된다 —
// 파생 가능한 사실에는 쓰기 파라미터를 만들지 않는다(설계 §1 원칙 ①).
func (s *server) handlePrescriptions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	infoFrom(r.Context()).setSession(id)
	res, err := s.svc.Prescriptions(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, res)
}
```

- [ ] **Step 5: 초록을 본다**

Run: `go test ./internal/api/ -run Prescriptions -v`
Expected: PASS 둘 다

- [ ] **Step 6: 커밋**

```bash
git add plugins/flightdeck/server/internal/api/
git commit -m "feat(flightdeck): expose prescriptions as a POST, because handing one out spends it"
```

---

### Task 6: ack — `Note` 가 열린 처방을 닫는다

**Files:**
- Modify: `internal/service/prescribe.go` (ack 함수)
- Modify: `service.Note` 가 있는 파일 (`grep -rn "func (s \*Service) Note" internal/service/`)
- Test: `internal/service/prescribe_test.go` (추가)

**Interfaces:**
- Consumes: Task 4 의 `eventPrescribe`·`eventPrescribeAck`·`prescribePayload`
- Produces: `func (s *Service) ackPrescriptions(ctx context.Context, project, sessionID string)` — `Note` 성공 후에 불린다

- [ ] **Step 1: 실패하는 시험을 추가한다**

`internal/service/prescribe_test.go` 에 더한다:

```go
// note 하나가 그 시점 열린 처방 전부를 닫고, 무엇이 열려 있었는지가 ack 에 남는다.
func TestNoteAcksOpenPrescriptions(t *testing.T) {
	ctx := context.Background()
	svc, st := newTestService(t)

	sess := openSessionForTest(t, svc, st)
	touchPathForTest(t, st, sess, "cmd/fd/hook.go")

	first, err := svc.Prescriptions(ctx, sess)
	if err != nil || len(first.All) == 0 {
		t.Fatalf("처방이 안 나왔다: %v / %+v", err, first)
	}

	if _, err := svc.Note(ctx, NoteInput{
		Project: testProject, SessionID: sess, Kind: model.JudgmentDecision,
		Title: "무엇을 하는지", Body: "훅에서 처방을 낸다",
	}); err != nil {
		t.Fatalf("note 실패: %v", err)
	}

	acks, err := st.ListSessionEvents(ctx, sess, "prescribe_ack", timeZero())
	if err != nil {
		t.Fatalf("ack 조회 실패: %v", err)
	}
	if len(acks) != 1 {
		t.Fatalf("ack 이 1건이 아니다: %d", len(acks))
	}
	if !strings.Contains(acks[0].Payload, first.All[0].Key) {
		t.Fatalf("ack payload 에 열려 있던 키가 없다: %s", acks[0].Payload)
	}
}

// 열린 처방이 없으면 ack 도 안 남는다. 빈 ack 는 확인율 분모를 오염시킨다.
func TestNoteWithoutOpenPrescriptionsLeavesNoAck(t *testing.T) {
	ctx := context.Background()
	svc, st := newTestService(t)
	sess := openSessionForTest(t, svc, st)

	if _, err := svc.Note(ctx, NoteInput{
		Project: testProject, SessionID: sess, Kind: model.JudgmentDecision,
		Title: "t", Body: "b",
	}); err != nil {
		t.Fatalf("note 실패: %v", err)
	}
	acks, _ := st.ListSessionEvents(ctx, sess, "prescribe_ack", timeZero())
	if len(acks) != 0 {
		t.Fatalf("열린 처방이 없는데 ack 이 남았다: %d", len(acks))
	}
}
```

- [ ] **Step 2: 빨간불을 본다**

Run: `go test ./internal/service/ -run 'Acks|LeavesNoAck' -v`
Expected: FAIL — ack 이 0건

- [ ] **Step 3: ack 함수를 쓴다**

`internal/service/prescribe.go` 에 더한다:

```go
// ackPrescriptions 는 지금 열려 있는 처방 전부를 닫는다.
//
// ★ note 한 번이 전부를 닫는 이유: 처방 문구가 무엇을 쓸지 지정하므로 보통 판단 하나가
// 그것을 덮는다. 처방마다 대응 판단을 요구하면 세션이 형식적 note 를 양산하고,
// 그러면 건수는 오르는데 판단 바이트는 안 오른다 — 설계 §10 이 그 둘을 함께 보라고 한 이유다.
//
// ★ **실패해도 판단 저장을 되돌리지 않는다.** 판단이 재생성 불가한 자산이고 ack 은 계측이다.
// 다만 삼키지 않는다 — WARN 으로 남긴다.
func (s *Service) ackPrescriptions(ctx context.Context, project, sessionID string) {
	sess, err := s.st.GetSession(ctx, sessionID)
	if err != nil {
		s.log.WarnContext(ctx, "ack: 세션을 못 읽었다", "session_id", sessionID, "error", err.Error())
		return
	}
	open, _, err := s.emittedKeys(ctx, sessionID, sess.OpenedAt)
	if err != nil {
		s.log.WarnContext(ctx, "ack: 발화 이력을 못 읽었다", "session_id", sessionID, "error", err.Error())
		return
	}
	acked, err := s.ackedKeys(ctx, sessionID, sess.OpenedAt)
	if err != nil {
		s.log.WarnContext(ctx, "ack: 확인 이력을 못 읽었다", "session_id", sessionID, "error", err.Error())
		return
	}
	var keys []string
	for k := range open {
		if !acked[k] {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return // **빈 ack 은 안 남긴다** — 확인율의 분자를 부풀린다
	}
	sort.Strings(keys) // 같은 입력에 같은 payload
	s.st.LogEvent(ctx, eventPrescribeAck, project, sessionID, map[string]any{"keys": keys})
}

// ackedKeys 는 이미 확인된 키다.
func (s *Service) ackedKeys(ctx context.Context, sessionID string, openedAt time.Time) (map[string]bool, error) {
	evs, err := s.st.ListSessionEvents(ctx, sessionID, eventPrescribeAck, openedAt)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, e := range evs {
		var p struct {
			Keys []string `json:"keys"`
		}
		if err := json.Unmarshal([]byte(e.Payload), &p); err != nil {
			s.log.WarnContext(ctx, "ack payload 해석 실패", "payload", e.Payload)
			continue
		}
		for _, k := range p.Keys {
			out[k] = true
		}
	}
	return out, nil
}
```

`import` 에 `"sort"` 를 더한다.

- [ ] **Step 4: `Note` 에서 부른다**

`service.Note` 의 **성공 반환 직전**에 한 줄 넣는다. 정확한 위치는 `grep -n "func (s \*Service) Note" -A 60 internal/service/*.go` 로 찾는다.

```go
	// 처방을 받고 판단을 남겼다 — 열린 처방을 닫는다. 실패해도 판단은 이미 저장됐다.
	s.ackPrescriptions(ctx, in.Project, in.SessionID)
```

**`kind` 로 거르지 않는다.** `draft` 든 `decision` 이든 판단은 판단이고, 종류로 거르면
"어떤 종류가 확인으로 쳐지나"를 세션이 외워야 한다(설계 §1 원칙 ②).

- [ ] **Step 5: 초록을 본다**

Run: `go test ./internal/service/ -run 'Acks|LeavesNoAck|Prescription' -v`
Expected: PASS 전부

- [ ] **Step 6: 커밋**

```bash
git add plugins/flightdeck/server/internal/service/
git commit -m "feat(flightdeck): let one judgment close every open prescription, and record which"
```

---

### Task 7: `fd hook stop` — 실제 배달

**전제:** Task 1 의 결정 게이트. 주입이 안 되면 Step 4 의 대체 경로를 쓴다.

**Files:**
- Modify: `cmd/fd/hook.go` (`hookStopProbe` → `hookStop`)
- Modify: `cmd/fd/render.go` (문구)
- Test: `cmd/fd/hook_stop_test.go` (신규)

**Interfaces:**
- Consumes: `POST /api/v1/sessions/{id}/prescriptions`(Task 5) · 기존 `a.cli.Write`·`a.OpenSession`·`a.ccSessionID`·`emitContext`
- Produces: `func RenderPrescriptions(shown []PrescriptionLine, folded int) string` · `type PrescriptionLine struct{ Key, Text string }`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`cmd/fd/hook_stop_test.go`. **단정 대상은 stdout 실물 문자열이다**(설계 §12).

```go
package main

import (
	"strings"
	"testing"
)

func TestRenderPrescriptionsNamesTheCall(t *testing.T) {
	got := RenderPrescriptions([]PrescriptionLine{
		{Key: "overlap:01OTHER", Text: "cmd/fd/hook.go 를 만졌는데 세션 01OTHER 도 잡고 있다.\n  → note(kind='ask', …)"},
	}, 0)

	if !strings.Contains(got, "flightdeck 처방") {
		t.Fatalf("머리글이 없다: %q", got)
	}
	if !strings.Contains(got, "note(kind='ask'") {
		t.Fatalf("부를 도구가 없다: %q", got)
	}
	if strings.Contains(got, "접었다") {
		t.Fatalf("접힌 게 없는데 접었다고 말한다: %q", got)
	}
}

func TestRenderPrescriptionsSaysWhatItFolded(t *testing.T) {
	got := RenderPrescriptions([]PrescriptionLine{{Key: "outside:a", Text: "a"}}, 7)
	if !strings.Contains(got, "7") {
		t.Fatalf("접힌 수를 안 말한다: %q", got)
	}
}

// 처방이 0건이면 **아무것도 안 낸다.** 빈 머리글을 내면 매 턴 컨텍스트를 먹고,
// 그러면 세션이 이 채널 자체를 읽지 않게 된다.
func TestRenderPrescriptionsEmptyIsSilent(t *testing.T) {
	if got := RenderPrescriptions(nil, 0); got != "" {
		t.Fatalf("0건인데 뭔가 냈다: %q", got)
	}
}
```

- [ ] **Step 2: 빨간불을 본다**

Run: `go test ./cmd/fd/ -run RenderPrescriptions -v`
Expected: FAIL — `undefined: RenderPrescriptions`

- [ ] **Step 3: 렌더를 쓴다**

`cmd/fd/render.go` 에:

```go
// PrescriptionLine 은 낼 처방 하나다.
type PrescriptionLine struct {
	Key  string
	Text string
}

// RenderPrescriptions 는 훅 stdout 에 실을 문구다.
//
// ★ 0건이면 **빈 문자열이다.** 빈 머리글을 매 턴 내면 컨텍스트를 먹고,
// 그러면 세션이 이 채널 자체를 읽지 않게 된다 — 설계 §4 가 고발한 상시 점등의 다른 얼굴이다.
func RenderPrescriptions(shown []PrescriptionLine, folded int) string {
	if len(shown) == 0 && folded == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "flightdeck 처방 %d건 — 지금 남기지 않으면 남의 화면에 안 뜬다\n", len(shown)+folded)
	for _, p := range shown {
		fmt.Fprintf(&b, "  %s\n", strings.ReplaceAll(p.Text, "\n", "\n  "))
	}
	if folded > 0 {
		fmt.Fprintf(&b, "  … %d건을 접었다. 접힌 것도 이미 발화된 것이라 다시 안 뜬다\n", folded)
	}
	return strings.TrimRight(b.String(), "\n")
}
```

- [ ] **Step 4: 훅을 쓴다**

`cmd/fd/hook.go` 의 `hookStopProbe` 를 지우고 아래로 바꾼다. `runHook` 의 `case "stop":` 은 `a.hookStop(ctx, p, out)` 로 바꾼다.

```go
// hookStop 은 턴이 끝날 때 처방을 받아 낸다.
//
// ★ 턴 끝에 모으는 이유: 한 턴에 파일 20개를 고쳐도 처방은 1회로 묶인다.
// 그리고 에이전트가 다음 턴을 시작하기 전이라 사람을 안 기다린다.
//
// ★ fail-open 이다. 서버가 죽어도 조용히 반환한다 — 훅이 세션을 막으면 안 된다.
//
// ★★ **stop_hook_active 면 아무것도 안 낸다. 이 가드가 없으면 무한 루프다.**
// 2026-08-04 실측(Claude Code 2.1.221): Stop 훅의 additionalContext 는 붙기만 하는 것이
// 아니라 **모델을 다시 부른다**. 그 턴이 끝나면 Stop 이 또 불리고, 또 주입한다.
// 무가드 판을 실제로 돌려 루프를 냈고 사람이 인터럽트로 끊었다.
// 그리고 이 가드는 임시방편이 아니라 옳은 의미론이다 — 처방은 **사람이 몰던 턴의 끝**에
// 한 번 뜨는 것이지, 자기가 만든 턴의 끝에 다시 뜨는 것이 아니다.
func (a *App) hookStop(ctx context.Context, p HookPayload, out io.Writer) {
	if p.StopHookActive {
		// 내가 만든 턴이다. 여기서 또 내면 그 턴이 또 턴을 만든다.
		return
	}
	if strings.TrimSpace(p.CWD) != "" {
		a.proj = resolveProject(a.env, p.CWD)
	}
	cc := a.ccSessionID(p.SessionID)
	if cc == "" {
		a.log.Warn("stop: 세션 id 를 못 읽어 처방을 못 냈다")
		return
	}
	res, _, err := a.OpenSession(ctx, cc, "")
	if err != nil {
		a.log.Warn("stop: 세션 좌표를 못 얻었다", "error", err.Error())
		return
	}
	a.cli.Session = res.Session.ID

	body, err := a.cli.Write(ctx, "prescriptions",
		"/api/v1/sessions/"+res.Session.ID+"/prescriptions", struct{}{})
	if err != nil {
		a.log.Warn("stop: 처방 조회 실패", "error", err.Error())
		return
	}
	var got struct {
		Shown  []PrescriptionLine `json:"shown"`
		Folded int                `json:"folded"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		a.log.Warn("stop: 처방 응답 해석 실패", "error", err.Error())
		return
	}
	if text := RenderPrescriptions(got.Shown, got.Folded); text != "" {
		emitContext(out, "Stop", text)
	}
}
```

`PrescriptionLine` 의 JSON 태그가 서버 응답과 맞아야 한다. `judge.Prescription` 에 태그가 없으므로 서버가 `{"Key":…,"Text":…}` 를 낸다 — **`judge.Prescription` 에 `json:"key"`·`json:"reason"`·`json:"text"` 태그를 더하고**, `PrescriptionLine` 에도 같은 태그를 단다. 이 불일치를 시험이 잡도록 Step 5 의 통합 시험을 둔다.

**`HookPayload` 에 필드를 더한다.** 실측한 Stop 페이로드에 있는 것 중 이 훅이 쓰는 것은 하나다:

```go
	StopHookActive bool `json:"stop_hook_active"`
```

**~~Task 1 이 "주입 안 된다"로 판정됐으면~~ — 그 갈래는 닫혔다.** 실측 결과 주입은 **된다**
(2026-08-04, Claude Code 2.1.221). 채널은 `Stop` 훅 stdout 의
`hookSpecificOutput.additionalContext` 이고, 받는 쪽에는 `<system-reminder>` 로 들어온다.
그러니 `UserPromptSubmit` 폴백은 안 만든다 — 안 쓰는 경로를 만들면 썩는다.

**대신 실측이 새 제약을 하나 만들었다**: 주입이 모델을 다시 부르므로
`stop_hook_active` 가드가 **필수**다. Step 5 의 시험이 그 가드를 지킨다.

- [ ] **Step 5: 통합 시험 — 응답 모양이 맞는지**

`cmd/fd/hook_stop_test.go` 에 더한다. 기존 `cmd/fd` 시험의 가짜 서버 헬퍼를 쓴다 (`grep -rn "httptest.NewServer" cmd/fd/*_test.go`).

```go
// 서버 응답의 필드 이름이 클라이언트 구조체와 맞는지 본다.
// 태그가 어긋나면 처방이 조용히 빈 목록이 된다 — 이 시험이 없으면 아무도 모른다.
func TestHookStopReadsServerFieldNames(t *testing.T) {
	srv := fakeServer(t, map[string]string{
		"/api/v1/sessions/S1/prescriptions": `{"shown":[{"key":"unclaimed","text":"XYZ-MARK"}],"folded":2}`,
	})
	defer srv.Close()

	out := runHookForTest(t, srv.URL, "stop", `{"session_id":"cc-1","cwd":"."}`)
	if !strings.Contains(out, "XYZ-MARK") {
		t.Fatalf("서버가 준 문구가 stdout 에 없다: %q", out)
	}
	if !strings.Contains(out, "2") {
		t.Fatalf("접힌 수가 stdout 에 없다: %q", out)
	}
}

// 서버가 죽어도 훅은 조용히 성공한다. 훅이 세션을 막으면 안 된다.
func TestHookStopFailsOpen(t *testing.T) {
	out := runHookForTest(t, "http://127.0.0.1:1", "stop", `{"session_id":"cc-1","cwd":"."}`)
	if strings.TrimSpace(out) != "" {
		t.Fatalf("서버가 없는데 뭔가 냈다: %q", out)
	}
}

// ★ 이 시험이 이 파일에서 가장 중요하다.
//
// stop_hook_active 면 아무것도 안 낸다. 안 그러면 주입이 모델을 다시 부르고,
// 그 턴이 끝나면 Stop 이 또 불리고, 또 주입한다 — 무한 루프다.
// 2026-08-04 에 무가드 판으로 실제로 재현했고 사람이 인터럽트로 끊었다.
func TestHookStopIsSilentOnReentry(t *testing.T) {
	srv := fakeServer(t, map[string]string{
		"/api/v1/sessions/S1/prescriptions": `{"shown":[{"key":"unclaimed","text":"XYZ-MARK"}],"folded":0}`,
	})
	defer srv.Close()

	out := runHookForTest(t, srv.URL, "stop",
		`{"session_id":"cc-1","cwd":".","stop_hook_active":true}`)
	if strings.TrimSpace(out) != "" {
		t.Fatalf("재진입인데 뭔가 냈다 — 이게 무한 루프의 씨앗이다: %q", out)
	}
}
```

**Step 5 의 빨간불 확인은 이 시험으로 한다**: `hookStop` 의 `if p.StopHookActive { return }` 를
잠깐 지우고 `TestHookStopIsSilentOnReentry` 가 빨간불인지 본다. 확인 후 되돌린다.

- [ ] **Step 6: 초록을 본다**

Run: `go test ./cmd/fd/ -run 'RenderPrescriptions|HookStop' -v`
Expected: PASS 전부

- [ ] **Step 7: `fd doctor` 에 채널을 낸다**

`cmd/fd` 의 doctor 명령에 한 줄 더한다 (`grep -n "doctor" cmd/fd/cmds.go`). 설계 §13: 부재를 기본값으로 접지 않는다.

```
처방 채널   Stop 훅 stdout      (Task 1 실측: 주입됨)
```
또는
```
처방 채널   UserPromptSubmit    (Task 1 실측: Stop stdout 이 주입 안 됨 — 다음 프롬프트까지 지연된다)
```

- [ ] **Step 8: 커밋**

```bash
git add plugins/flightdeck/server/cmd/fd/ plugins/flightdeck/server/internal/judge/prescribe.go
git commit -m "feat(flightdeck): deliver prescriptions at turn end, and say which channel carried them"
```

---

### Task 8: 보드 카드에 사건을 붙인다

**Files:**
- Modify: `internal/mcpsrv/render.go` (`boardCard`, 265행 부근)
- Test: `internal/mcpsrv/render_test.go` (추가)

**Interfaces:**
- Consumes: `service.BoardView.Asks`·`.Blocked`(기존, `model.Judgment` 에 `SessionID` 가 있다) · `service.SessionCard`
- Produces: 카드 블록에 `[ask 12분] …` 줄

- [ ] **Step 1: 실패하는 시험을 쓴다**

```go
// 사건이 그것을 남긴 세션의 카드에 붙는다. 전역 꼬리만으로는 누가 남겼는지가 안 이어진다.
func TestBoardCardCarriesItsOwnAsk(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	v := service.BoardView{
		Sessions: []service.SessionCard{
			{View: model.SessionView{Session: model.Session{ID: "01AAA"}}},
			{View: model.SessionView{Session: model.Session{ID: "01BBB"}}},
		},
		Asks: []model.Judgment{
			{ID: "j1", SessionID: "01AAA", At: now.Add(-12 * time.Minute),
				Title: "mcpbackend.go 를 잡는다"},
		},
	}
	got := RenderBoard(v, BoardRenderOptions{Now: now, Detail: true})

	lines := strings.Split(got, "\n")
	var aaaIdx, askIdx, bbbIdx int = -1, -1, -1
	for i, l := range lines {
		switch {
		case strings.Contains(l, "01AAA"):
			aaaIdx = i
		case strings.Contains(l, "mcpbackend.go 를 잡는다"):
			askIdx = i
		case strings.Contains(l, "01BBB"):
			bbbIdx = i
		}
	}
	if askIdx < 0 {
		t.Fatalf("사건이 어디에도 없다:\n%s", got)
	}
	if !(aaaIdx < askIdx && askIdx < bbbIdx) {
		t.Fatalf("사건이 01AAA 카드 안에 없다 (aaa=%d ask=%d bbb=%d):\n%s", aaaIdx, askIdx, bbbIdx, got)
	}
	if !strings.Contains(lines[askIdx], "12분") {
		t.Fatalf("사건의 나이가 없다: %q", lines[askIdx])
	}
}
```

- [ ] **Step 2: 빨간불을 본다**

Run: `go test ./internal/mcpsrv/ -run BoardCardCarriesItsOwnAsk -v`
Expected: FAIL — 사건이 카드 안에 없다

- [ ] **Step 3: 구현한다**

`boardCard` 의 시그니처에 사건 목록을 더한다. 호출부(`RenderBoard` 의 `for _, c := range v.Sessions`)에서 세션별로 갈라 넘긴다.

```go
// noteLines 는 이 카드가 실을 사건 줄이다.
//
// ★ 전역 꼬리를 없애지 않는다. 카드가 접히면 사건도 접히므로 꼬리가 그 안전망이다.
func noteLines(sessionID string, asks, blocked []model.Judgment, now time.Time) []string {
	var out []string
	add := func(kind string, js []model.Judgment) {
		for _, j := range js {
			if j.SessionID != sessionID {
				continue
			}
			out = append(out, fmt.Sprintf("   [%s %s] %s",
				kind, humanAge(now.Sub(j.At)), clip(firstLine(j.Title, j.Body), 100)))
		}
	}
	add("ask", asks)
	add("blocked", blocked)
	return out
}
```

`humanAge`·`firstLine`·`clip` 은 이 패키지에 이미 있다 — `grep -n "func humanAge\|func firstLine\|func clip" internal/mcpsrv/*.go` 로 정확한 이름을 확인하고 맞춘다. 없으면 카드가 신호 나이를 찍는 함수를 그대로 쓴다.

- [ ] **Step 4: 초록을 본다**

Run: `go test ./internal/mcpsrv/ -v`
Expected: PASS 전부 (기존 렌더 시험 포함)

- [ ] **Step 5: 커밋**

```bash
git add plugins/flightdeck/server/internal/mcpsrv/
git commit -m "feat(flightdeck): attach each event to the card of the session that left it"
```

---

### Task 9: 접기를 관련성 순으로

**Files:**
- Modify: `internal/mcpsrv/render.go:195-247`
- Test: `internal/mcpsrv/render_test.go` (추가)

**Interfaces:**
- Consumes: Task 8 의 카드 사건 · `service.SessionCard.IsSelf` · `BoardView.Asks`·`.Blocked`
- Produces: `func rankCards(v service.BoardView, self string) []service.SessionCard` — 표시 순서

- [ ] **Step 1: 실패하는 시험을 쓴다**

```go
// 예산이 자를 때 **사건이 붙은 카드가 조용한 카드보다 먼저 남는다.**
// 이것이 없으면 사건을 카드에 붙여도 예산이 그걸 먼저 버린다.
func TestFoldKeepsEventCardsOverSilentOnes(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	var sessions []service.SessionCard
	for i := 0; i < 20; i++ {
		sessions = append(sessions, service.SessionCard{
			View: model.SessionView{
				Session: model.Session{ID: fmt.Sprintf("01S%02d", i)},
				Paths:   []string{"some/long/path/that/costs/tokens.go"},
			},
		})
	}
	v := service.BoardView{
		Sessions: sessions,
		Asks: []model.Judgment{
			{ID: "j1", SessionID: "01S19", At: now, Title: "마지막 세션이 남긴 요청"},
		},
	}
	got := RenderBoard(v, BoardRenderOptions{Now: now, Budget: 300})

	if !strings.Contains(got, "01S19") {
		t.Fatalf("사건이 붙은 카드가 접혔다:\n%s", got)
	}
	if !strings.Contains(got, "접었다") {
		t.Fatalf("예산 300 인데 아무것도 안 접혔다:\n%s", got)
	}
}

// 나는 언제나 첫 카드다.
func TestFoldAlwaysKeepsSelfFirst(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	var sessions []service.SessionCard
	for i := 0; i < 20; i++ {
		sessions = append(sessions, service.SessionCard{
			View:   model.SessionView{Session: model.Session{ID: fmt.Sprintf("01S%02d", i)}},
			IsSelf: i == 19,
		})
	}
	got := RenderBoard(service.BoardView{Sessions: sessions},
		BoardRenderOptions{Now: now, Self: "01S19", Budget: 300})
	if !strings.Contains(got, "01S19") {
		t.Fatalf("내 카드가 접혔다:\n%s", got)
	}
}
```

- [ ] **Step 2: 빨간불을 본다**

Run: `go test ./internal/mcpsrv/ -run 'FoldKeeps|FoldAlways' -v`
Expected: FAIL — 사건 카드가 접힘

- [ ] **Step 3: 구현한다**

`RenderBoard` 에서 `blocks` 를 만들기 **전에** 카드를 정렬한다.

```go
// rankCards 는 예산이 자를 순서를 정한다. 자르는 것은 이 순서의 **뒤부터**다.
//
//	① 나 ② 사건(ask·blocked)이 붙은 카드 ③ 나와 경로가 겹치는 카드 ④ 나머지 — 신호 최신순
//
// ★ 앞선 판은 목록 위치 순으로 잘랐다. 그래서 열린 ask 가 붙은 카드가 조용한 카드보다
// 먼저 접힐 수 있었고, 사건을 카드에 붙여도 예산이 그것을 먼저 버렸다.
func rankCards(v service.BoardView, self string, now time.Time) []service.SessionCard {
	hasNote := map[string]bool{}
	for _, j := range v.Asks {
		hasNote[j.SessionID] = true
	}
	for _, j := range v.Blocked {
		hasNote[j.SessionID] = true
	}

	var selfPaths []string
	for _, c := range v.Sessions {
		if c.View.Session.ID == self || c.IsSelf {
			selfPaths = c.View.Paths
		}
	}

	rank := func(c service.SessionCard) int {
		switch {
		case c.IsSelf || c.View.Session.ID == self:
			return 0
		case hasNote[c.View.Session.ID]:
			return 1
		case len(selfPaths) > 0 && judge.PathsOverlap(selfPaths, c.View.Paths):
			return 2
		default:
			return 3
		}
	}

	out := append([]service.SessionCard(nil), v.Sessions...)
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := rank(out[i]), rank(out[j])
		if ri != rj {
			return ri < rj
		}
		// 같은 등급이면 최근 신호가 앞이다. 신호가 아예 없으면 뒤로.
		return lastSignal(out[i], now).After(lastSignal(out[j], now))
	})
	return out
}

// lastSignal 은 신호 넷 중 가장 최근 시각이다. 없으면 제로값이다.
// **합치지 않는다** — 여기서 최댓값을 쓰는 것은 정렬 키일 뿐이고,
// 카드 본문은 종류별로 따로 낸다(설계 §4).
func lastSignal(c service.SessionCard, now time.Time) time.Time {
	var out time.Time
	for _, at := range c.View.Signals {
		if at.After(out) {
			out = at
		}
	}
	return out
}
```

`RenderBoard` 안에서 `for _, c := range v.Sessions` 를 `for _, c := range rankCards(v, opt.Self, now)` 로 바꾼다. `import` 에 `"sort"` 와 `judge` 패키지를 더한다.

- [ ] **Step 4: 초록을 본다**

Run: `go test ./internal/mcpsrv/ -v`
Expected: PASS 전부

- [ ] **Step 5: 커밋**

```bash
git add plugins/flightdeck/server/internal/mcpsrv/
git commit -m "fix(flightdeck): let the budget cut by relevance, not by list position"
```

---

### Task 10: 창 2시간 + "창 밖 N건"

**Files:**
- Modify: `internal/service/service.go:44`
- Modify: `internal/service/board.go` (창 밖 건수 파생)
- Modify: `internal/mcpsrv/render.go` (문구)
- Test: `internal/service/board_test.go`·`internal/mcpsrv/render_test.go` (추가)

**Interfaces:**
- Consumes: `service.BoardView`
- Produces: `service.BoardView.OutOfWindow int` · `service.BoardView.OldestOutside time.Time`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/mcpsrv/render_test.go` 에:

```go
// 창 밖으로 잘린 것을 **침묵시키지 않는다.** 창은 표시 구간이지 생존 판정이 아니다(설계 §4).
func TestBoardSaysWhatTheWindowCutOff(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	got := RenderBoard(service.BoardView{
		Sessions:      []service.SessionCard{{View: model.SessionView{Session: model.Session{ID: "01AAA"}}}},
		Window:        2 * time.Hour,
		OutOfWindow:   9,
		OldestOutside: now.Add(-7 * time.Hour),
	}, BoardRenderOptions{Now: now})

	if !strings.Contains(got, "창 밖 9건") {
		t.Fatalf("창 밖 건수를 안 말한다:\n%s", got)
	}
	if !strings.Contains(got, "window=") {
		t.Fatalf("어떻게 보는지를 안 말한다:\n%s", got)
	}
	if strings.Contains(got, "죽") {
		t.Fatalf("생존 판정 낱말이 들어갔다 — 설계 §4 위반:\n%s", got)
	}
}
```

`internal/service/board_test.go` 에:

```go
func TestDefaultLiveWindowIsTwoHours(t *testing.T) {
	if DefaultLiveWindow != 2*time.Hour {
		t.Fatalf("기본 창이 2시간이 아니다: %v", DefaultLiveWindow)
	}
}
```

- [ ] **Step 2: 빨간불을 본다**

Run: `go test ./internal/service/ ./internal/mcpsrv/ -run 'TwoHours|WindowCutOff' -v`
Expected: FAIL 둘 다

- [ ] **Step 3: 창을 바꾼다**

`internal/service/service.go:44`:

> **개정 (2026-08-06 · 항목 `fd-item-premise-signal-table-has-no-history`).** 아래 주석 문안의
> 셋째 ★ 절("signal 표로 … 간격 분포를 재면")은 **재료를 잘못 지목했다.** `signal` 표는 PK 가
> `(session_id, kind)` 라 종류별 한 행이고 갱신되므로 간격이라는 값이 존재하지 않는다. 재료는
> `event(kind='session.beat')` 뿐이다.
>
> **실제로 랜딩된 것은 이 문안이 아니다.** `internal/service/service.go` 의 현재 주석은 `event` 로
> 실측한 결과판이고(침묵 W 뒤 복귀율과, 원래 근거였던 "화면이 줄어든다"가 **반증됐다**는 기록을
> 함께 싣는다), 방법은 `DESIGN.md` §10 「1차 실측」에 있다. 아래는 그때의 계획 문안으로 남긴다.

```go
	// DefaultLiveWindow 는 Board 가 "이 안에 신호가 있었나"로 자르는 기본 구간이다.
	//
	// ★ **이 값에는 근거가 없다.** 8시간이었을 때 이 머신의 보드에 세션 19건이 떠서
	// 그중 9건이 예산에 접혔고, 그 화면은 조정에 쓸 수 없었다. 2시간은 그것을
	// 줄이려고 고른 잠정값이지 실측이 아니다.
	//
	// ★ 근거는 만들 수 있고 재료가 이미 DB 에 있다 — signal 표로 "마지막 신호 후 다음
	// 신호까지의 간격 분포"를 재면 "이 구간 뒤엔 사실상 안 돌아온다"가 나온다.
	// 그 실측은 큐 항목 fd-live-window-baseline 이다.
	//
	// ★ **생존 판정이 아니다.** 창 밖으로 잘린 건수는 BoardView.OutOfWindow 로 나가고
	// 화면이 그것을 반드시 말한다(설계 §4: "죽었다"고 쓰지 않는다).
	DefaultLiveWindow = 2 * time.Hour
```

- [ ] **Step 4: 창 밖 건수를 파생한다**

`internal/service/board.go` 의 `BoardView` 에 두 필드를 더한다:

```go
	// OutOfWindow 는 창 밖이라 카드가 안 나간 세션 수다. **화면이 반드시 말한다** —
	// 침묵하면 "그런 세션이 없다"와 "안 보여 준다"가 구분되지 않는다.
	OutOfWindow int `json:"out_of_window,omitempty"`
	// OldestOutside 는 창 밖 세션 중 가장 오래된 마지막 신호 시각이다.
	OldestOutside time.Time `json:"oldest_outside,omitempty"`
```

`Board` 에서 `cards` 를 얻은 뒤 채운다. 창 없이 한 번 더 세는 질의가 필요하다 —
`s.st.ListLive(ctx, project, time.Time{})` 로 전부 세고 창 안 건수를 뺀다.

```go
	// 창 밖 건수 — 카드를 안 만든다. 세는 것만 한다(파생 비용을 안 늘린다).
	if all, aerr := s.st.ListLive(ctx, project, time.Time{}); aerr != nil {
		d.fail("out-of-window", aerr) // 못 세면 침묵하지 않고 파생 실패로 남긴다
	} else {
		view.OutOfWindow = len(all) - len(cards)
		cut := s.cut(now, window)
		for _, v := range all {
			for _, at := range v.Signals {
				if at.Before(cut) && (view.OldestOutside.IsZero() || at.Before(view.OldestOutside)) {
					view.OldestOutside = at
				}
			}
		}
	}
```

`d.fail` 의 정확한 이름은 `internal/service/board.go` 의 `derive` 타입에서 확인해 맞춘다.

- [ ] **Step 5: 문구를 낸다**

`internal/mcpsrv/render.go` 의 `foot` 조립부에 더한다:

```go
	if v.OutOfWindow > 0 {
		age := ""
		if !v.OldestOutside.IsZero() {
			age = fmt.Sprintf("(가장 오래된 신호 %s 전) ", humanAge(now.Sub(v.OldestOutside)))
		}
		foot = append(foot, fmt.Sprintf(
			"창 밖 %d건 %s— 창은 표시 구간이지 생존 판정이 아니다. window=8h 로 본다",
			v.OutOfWindow, age))
	}
```

- [ ] **Step 6: 초록을 본다**

Run: `go test ./... -v 2>&1 | tail -40`
Expected: PASS 전부. 기존 시험이 8시간을 단정하면 그 시험의 기대값을 고친다 — **단, 그 시험이 무엇을 지키려던 것인지 먼저 읽고 고친다.**

- [ ] **Step 7: 커밋**

```bash
git add plugins/flightdeck/server/internal/service/ plugins/flightdeck/server/internal/mcpsrv/
git commit -m "fix(flightdeck): narrow the board window, and say out loud what it cut"
```

---

### Task 11: `now` 를 도구 enum 에서 뺀다

**Files:**
- Modify: `internal/mcpsrv/tools.go:93-94`
- Test: `internal/mcpsrv/tools_test.go` (추가)

**Interfaces:**
- Consumes: 없음
- Produces: 없음 (`model.JudgmentNow` 상수와 검증 목록은 **남는다**)

- [ ] **Step 1: 실패하는 시험을 쓴다**

```go
// 쓸 수 있는데 아무 데도 안 보이는 종류는 거짓 초록이다.
// note(kind='now') 는 저장은 되고 보드 어디에도 안 나온다 — 도구가 그것을 제안하면 안 된다.
func TestNoteKindEnumHasNoInvisibleKinds(t *testing.T) {
	var noteTool Tool
	for _, tl := range Tools() {
		if tl.Name == "note" {
			noteTool = tl
		}
	}
	props := noteTool.InputSchema["properties"].(map[string]any)
	kind := props["kind"].(map[string]any)
	for _, v := range kind["enum"].([]any) {
		if v == "now" {
			t.Fatal("note.kind 에 'now' 가 있다 — 보드가 안 읽는 종류다")
		}
	}
}

// 반대로 model 상수는 남아 있어야 한다. 레거시 임포터가 생산하고 DB 에 이미 행이 있다.
func TestJudgmentNowConstantSurvives(t *testing.T) {
	if model.JudgmentNow != "now" {
		t.Fatal("레거시 임포터가 쓰는 상수를 지웠다 — DB 에 있는 행을 못 읽게 된다")
	}
}
```

- [ ] **Step 2: 빨간불을 본다**

Run: `go test ./internal/mcpsrv/ -run 'InvisibleKinds|NowConstant' -v`
Expected: 첫 시험 FAIL, 둘째 PASS

- [ ] **Step 3: enum 에서 뺀다**

`internal/mcpsrv/tools.go` 의 `note` 도구:

```go
			"kind": enumStr("판단 종류",
				// ★ 'now' 가 여기 없다. 저장은 되지만 보드가 안 읽어서(service/board.go 는
				// ask·blocked 만 읽는다) 쓴 세션에게만 보이고 남에게는 안 보인다 —
				// 쓸 수 있는데 안 보이는 것은 거짓 초록이다(설계 §11).
				// model.JudgmentNow 상수와 검증 목록은 남는다: 레거시 임포터가 생산하고
				// DB 에 이미 행이 있어, 지우면 그 행을 못 읽게 된다.
				"handoff", "decision", "blocked", "ask", "rejected", "not-done", "verified", "draft"),
```

- [ ] **Step 4: 초록을 본다**

Run: `go test ./... 2>&1 | tail -20`
Expected: PASS 전부

- [ ] **Step 5: 커밋**

```bash
git add plugins/flightdeck/server/internal/mcpsrv/
git commit -m "fix(flightdeck): stop offering a note kind the board never reads"
```

---

### Task 12: DESIGN.md 반영 + 후속 항목 등록

**Files:**
- Modify: `plugins/flightdeck/DESIGN.md` §6 · §10 · §13
- Modify: `plugins/flightdeck/README.md` (훅이 5종이라고 적혀 있으면)

**Interfaces:**
- Consumes: Task 1 의 실측 결과 · Task 5 의 REST 경로 · Task 7 의 채널
- Produces: 없음 (문서)

- [ ] **Step 1: `hooks.json` 에 `Stop` 훅을 등록한다 — 이게 없으면 기능 전체가 죽은 코드다**

**★ 이 단계가 원래 Task 1 에 있었는데, Task 1 을 settings.json 탐침으로 고쳐 쓰면서 같이
사라졌다.** Task 7 리뷰가 그 구멍을 잡았다. `fd hook stop` 이 구현돼 있어도 훅이 등록 안 되면
아무 데서도 안 불린다.

`plugins/flightdeck/hooks/hooks.json` 의 `hooks` 객체에 더한다:

```json
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "\"${CLAUDE_PLUGIN_ROOT}/bin/fd\" hook stop",
            "timeout": 3
          }
        ]
      }
    ]
```

**`async` 를 쓰지 않는다.** 이 훅의 출력이 곧 배달이고, async 는 그 출력의 운명을 안 정해 준다.
**타임아웃 3초**: 사람이 기다리는 유일한 훅이라 짧게 잡는다.

그리고 `hooks.json` 을 단정하는 시험이 있으면(`plugin_test.go` 계열) 훅 수 기대값을 맞춘다.

- [ ] **Step 2: §6 훅 표에 `Stop` 행을 더한다**

```
| `Stop` | — | `fd hook stop` → 처방(발화 4조건, 전이 1회) 주입. **fail-open, 3초** |
```

- [ ] **Step 2: §6 REST 목록에 경로를 더한다**

`POST /sessions/{id}/prescriptions` — `/notices` 와 같은 줄에 두고 **세션 카드 파생을 안 돈다**고 적는다.

- [ ] **Step 3: §10 에 지표 둘을 더한다**

```
- 처방 발화·확인율 — `prescribe` 대비 `prescribe_ack`. **떨어지면 조건을 줄인다, 문구를 키우지 않는다**
- 사건이 있는 세션 비율 — 착수 시점 19건 중 1건
```

- [ ] **Step 4: §13 을 Task 1 결과로 마감한다**

Task 1 Step 6 에서 적은 것을 정리한다. 안 잰 축은 "아직 아님"에 남긴다.

- [ ] **Step 5: 후속 항목을 큐에 등록한다**

MCP `add` 로 두 건을 만든다. **이 계획이 남긴 근거 없는 상수 둘이 여기서 닫힌다.**

> **개정 (2026-08-06 · 항목 `fd-item-premise-signal-table-has-no-history`).** 아래 `add` 호출은
> 그대로 실행됐고, **틀린 재료 지목이 큐 항목 본문이 되어 다음 사람에게 전달됐다.** 이 자리가 이
> 사고의 발원지다 — `fd-live-window-baseline` 을 집은 세션이 지시대로 `signal` 표를 재려다
> "못 잰다"에 부딪혔고, 그 사고 보고서가 항목 `fd-item-premise-signal-table-has-no-history` 다.
>
> 옳은 문안은 이렇다: title 은 "보드 생존 창의 근거를 `event`(session.beat) 원장으로 만든다",
> body 의 "`signal` 표로" 는 "`event` 표(kind='session.beat', 추가 전용·스로틀 없음)로" 다.
> `signal` 은 PK 가 `(session_id, kind)` 라 종류별 한 행이고 갱신되므로 간격이라는 값이 없다.
>
> **항목 본문 자체는 고칠 수 없다** — 이 저장소에 항목 본문을 갱신하는 표면이 존재하지 않는다
> (`store/item.go` 의 쓰기는 Add·Delete·SetState·Claim·Finish·Move 뿐이고, REST·MCP·CLI 어디에도
> 본문 수정 경로가 없다). 그래서 정정은 여기와 `DESIGN.md` §3·§4, 그리고 관문
> `store/signal_is_not_history_test.go` 로 나갔다. 아래는 기록으로 남긴다.

```
add(id="fd-live-window-baseline",
    title="보드 생존 창의 근거를 signal 간격 분포로 만든다",
    body="DefaultLiveWindow=2h 에 근거가 없다. signal 표로 '마지막 신호 후 다음 신호까지의 간격 분포'를 재서 '이 구간 뒤엔 사실상 안 돌아온다'를 낸다. 결과를 상수 주석과 DESIGN §10 에 적는다.",
    paths=["internal/service/service.go"])

add(id="fd-prescribe-threshold-baseline",
    title="silent 임계 12경로/60분의 근거를 발화율로 만든다",
    body="SilentNewPaths=12, SilentGap=60m 에 근거가 없다. event 의 prescribe/prescribe_ack 분포로 발화율과 확인율을 재서 조정한다. 확인율이 떨어지면 조건을 줄인다 — 문구를 키우지 않는다.",
    paths=["internal/judge/prescribe.go"])
```

- [ ] **Step 6: 전체 시험**

Run: `go test ./...`
Expected: PASS 전부

- [ ] **Step 7: 커밋**

```bash
git add plugins/flightdeck/DESIGN.md plugins/flightdeck/README.md
git commit -m "docs(flightdeck): fold prescriptions into the contract, and name what still lacks evidence"
```

---

## 자체 검토

**스펙 커버리지** — 스펙의 각 절이 어느 태스크에 있나:

| 스펙 절 | 태스크 |
|---|---|
| §1 처방은 새 개념이 아니다 (도구 6·스킬 3·표 12 유지) | 전 태스크의 Global Constraints |
| §2 판정 순수 함수 | Task 2 |
| §3 발화 조건 넷 + 전이 규칙 + 3건 상한 | Task 2 |
| §4 event 재사용 · ack | Task 3, 4, 6 |
| §5 Stop 훅 · 실측 · 폴백 · 파생 안 돌기 | Task 1, 4, 5, 7 |
| §6 표면(REST 1, MCP 0) | Task 5 |
| §7 사건을 카드에 | Task 8 |
| §8(a) 관련성 접기 | Task 9 |
| §8(b) 창 2시간 + 창 밖 줄 | Task 10 |
| §9 `now` enum 제거 | Task 11 |
| 안 만드는 것 | 해당 태스크 없음 — 만들지 않는 것이 이행이다 |
| 시험 규율 | 각 태스크의 Step 1·2·5 |
| DESIGN 반영 | Task 12 |

**빈 자리 없음.** "후속 항목 자동 등록"은 스펙이 안 만들기로 한 것이고, 그 자리를 Task 2 의 `outside` 처방 문구(`add(...)` 를 처방한다)가 메운다.

**타입 일관성** — 태스크 간에 이름이 어긋나지 않는지:

| 이름 | 정의 | 소비 |
|---|---|---|
| `judge.PrescribeInput.NewPaths` | Task 2 | Task 4 |
| `judge.PrescribeInput.Emitted map[string]time.Time` | Task 2 | Task 4 (`emittedKeys`) |
| `judge.Prescription{Key,Reason,Text}` + json 태그 | Task 2 (태그는 Task 7 Step 4) | Task 4, 5, 7 |
| `judge.FoldPrescriptions` | Task 2 | Task 4 |
| `store.ListSessionEvents(ctx, sessionID, kind, since)` | Task 3 | Task 4, 6 |
| `service.PrescribeResult{Shown,Folded,All}` | Task 4 | Task 5, 7 |
| `service.ackPrescriptions` | Task 6 | Task 6 (`Note`) |
| `PrescriptionLine{Key,Text}` + json 태그 | Task 7 | Task 7 |
| `service.BoardView.OutOfWindow`·`OldestOutside` | Task 10 | Task 10 |

**주의점 하나** — `judge.Prescription` 의 JSON 태그는 Task 2 에서 정의되지만 필요는 Task 7 에서 생긴다. Task 2 를 구현할 때 태그를 미리 붙여 두면 Task 7 의 시험이 한 번에 통과한다:

```go
type Prescription struct {
	Key    string `json:"key"`
	Reason string `json:"reason"`
	Text   string `json:"text"`
}
```

**시험 헬퍼 이름** — Task 3·4·5·7 의 시험은 기존 헬퍼(`newTestStore`·`newTestService`·`newTestServer`·`fakeServer` 등)를 쓴다고 적었다. **각 태스크의 Step 1 을 시작하기 전에 그 패키지의 기존 `*_test.go` 를 먼저 읽고 실제 헬퍼 이름에 맞춘다.** 이름이 다르면 그 이름을 쓴다 — 새 헬퍼를 만들지 않는다.
