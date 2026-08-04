# 항목 경로 실재 힌트(fd-item-path-project-mismatch-hint) 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `pick` 이 항목을 낼 때 "이 항목이 선언한 경로가 이 프로젝트에 실재하는가"를 항상 한 줄로 내고, 오등록이면 되돌리는 명령을 그 자리에 낸다.

**Architecture:** 판정은 `internal/judge/itempaths.go` 의 순수 함수 `ClassifyItemPaths` 하나에 모은다(I/O 0). 관측(`os.Stat`)은 `internal/service/itempaths.go` 가 2단으로 한다 — 자기 프로젝트를 먼저 보고, **전부 없을 때만** 다른 등록 프로젝트를 본다. 출력은 `internal/mcpsrv/render.go` 의 `RenderPick` 안 블록 하나이고, 그 한 자리가 MCP `pick` 도구·`fd next`·`fd pick` 세 소비자를 동시에 덮는다.

**Tech Stack:** Go 1.25 · 표준 `testing` · `os.Stat`/`filepath` 만 쓴다(git 도 새 의존도 없다)

**Spec:** `docs/superpowers/specs/2026-08-05-fd-item-path-project-mismatch-hint-design.md` (커밋 `8e804c4`, 개정 `f0f9ff2`)

## Global Constraints

- **작업 디렉토리는 `plugins/flightdeck/server` 다.** 이 문서의 모든 `go` 명령은 거기서 돈다.
- **MCP 도구는 6개, 스킬은 3개, 테이블은 12개로 고정이다.** 이 계획은 셋 다 안 늘린다. `TestToolTableIsSix` 와 `TestFixingMisregistrationDidNotGrowTheToolTable` 이 그것을 지킨다(DESIGN §1 원칙 ②, §6).
- **판정 로직은 `internal/judge` 의 순수 함수에 있어야 한다.** service·render 본문에 판정을 쓰면 시험이 로직의 사본을 단정한다(DESIGN §12).
- **다중 조건 판정은 불리언이 아니라 사유를 반환한다.** `ItemPathVerdict.Summary` 는 **항상** 채운다(DESIGN §12).
- **단정은 소비자의 좌표계로 쓴다.** 대상은 **MCP 응답 문자열**과 순수 함수의 반환값이다(DESIGN §12).
- **새 검사는 망가진 것을 넣어 빨간불을 먼저 확인한다.** 초록만 보고 통과로 단정하지 않는다(DESIGN §12).
- **"못 읽었다"를 "없다"로 접지 않는다.** `PathUnknown` 이 0값인 것이 그 규율의 구현이다. `diskFreePct`(`internal/service/disk_unix.go`)가 같은 논증의 선례다 — "못 재면 0이 아니라 오류를 낸다".
- **`derive`(`d.ok`/`d.fail`/`d.note`)를 건드리지 않는다.** `FreshnessOf` 가 `reads==0 → Source:"db"`, `failures>0 → Stale` 로 정의돼 있어 stat 을 거기 세면 git 축의 뜻이 오염된다(스펙 §6).
- **주석과 사용자 문구는 한국어다. 커밋 메시지는 영어다**(레포 관례: `git log`).
- 전체 시험: `go test ./...` · 경합: `go test ./... -race` · 서식: `gofmt -l .`(빈 출력이어야 한다) · `go vet ./...`

---

## File Structure

| 파일 | 책임 | 상태 |
|---|---|---|
| `internal/judge/itempaths.go` | `PathPresence`·`Kind`·`ItemPathInput`·`ItemPathVerdict`·`ClassifyItemPaths` — 판정 전부 | 신규 |
| `internal/judge/itempaths_test.go` | 여섯 갈래 × `Unreadable` 섞임 표 시험 | 신규 |
| `internal/service/itempaths.go` | `os.Stat` 2단 관측 + `judge` 호출 | 신규 |
| `internal/service/itempaths_test.go` | 실물 임시 저장소 둘로 관측을 단정 | 신규 |
| `internal/service/pick.go` | `PickResult.PathCheck` 필드 + 세 갈래에서 채우기 | 수정 |
| `internal/mcpsrv/render.go` | `RenderPick` 안 `경로 실재:` 블록 | 수정 |
| `internal/mcpsrv/render_test.go` | 렌더 문자열 단정 | 수정 |
| `cmd/fd/wire_test.go` | REST 왕복 끝에서 끝까지 | 수정 |

**안 건드리는 파일(중요):** `internal/mcpsrv/backend.go`(`Pick` 서명 불변) · `internal/mcpsrv/tools.go`(도구 표) · `internal/api/*`(핸들러가 `PickResult` 를 통째로 직렬화한다) · `cmd/fd/mcpbackend.go`·`cmd/fd/cmds.go`(통째로 역직렬화한다) · `internal/web/*`(`PickResult` 를 안 만진다) · `DESIGN.md`.

---

## 사전 준비: 워크트리

- [ ] **워크트리를 만든다**

```bash
cd '/home/aaron/cdo-dev/kweiza-cc-plugins'
git worktree add '.flightdeck/worktrees/fd-item-path-project-mismatch-hint' -b fd-item-path-project-mismatch-hint 'main'
cd '/home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-item-path-project-mismatch-hint/plugins/flightdeck/server'
```

이후 모든 명령은 마지막 디렉토리에서 돈다.

---

### Task 1: 판정 순수 함수 — 타입과 여섯 갈래

**Files:**
- Create: `internal/judge/itempaths.go`
- Test: `internal/judge/itempaths_test.go`

**Interfaces:**
- Consumes: 없음(이 패키지는 I/O 도 다른 패키지도 안 쓴다)
- Produces:
  - `judge.PathPresence` — `int`, 상수 `PathUnknown`(0) · `PathAbsent` · `PathPresent`
  - `judge.Kind` — `string`, 상수 `KindNoPaths`·`KindOK`·`KindMisregistered`·`KindAmbiguous`·`KindNowhere`·`KindUnknown`
  - `judge.ItemPathInput{Project string; Paths []string; Here map[string]PathPresence; Elsewhere map[string]map[string]PathPresence; Unreadable []string}`
  - `judge.ItemPathVerdict{Kind Kind; Suggest string; Candidates []string; Unreadable []string; Summary string}` (전부 소문자 json 태그)
  - `func judge.ClassifyItemPaths(in ItemPathInput) ItemPathVerdict`

- [ ] **Step 1: 실패하는 표 시험을 쓴다**

`internal/judge/itempaths_test.go` 를 만든다:

```go
package judge

import (
	"strings"
	"testing"
)

// 판정 우선순위는 no-paths → ok → unknown → 나머지 셋이다.
// 그 순서가 곧 이 표의 뼈대다 — 순서가 틀리면 "못 읽었다"가 "오등록이다"로 접힌다.
func TestClassifyItemPaths(t *testing.T) {
	tests := []struct {
		name      string
		in        ItemPathInput
		wantKind  Kind
		wantSug   string
		wantCands []string
		wantInSum []string // Summary 에 반드시 들어갈 조각
		// noSuggest 는 "이 갈래에서는 어느 프로젝트도 지목하면 안 된다"다.
		// ★ Suggest 가 곧 되돌리는 명령의 방아쇠이므로(렌더가 그것만 보고 fd move 를 낸다)
		//   이 단정이 오등록 단정을 막는 **유일한** 자리다.
		noSuggest bool
	}{
		{
			name:      "경로가 없으면 판정할 재료가 없다",
			in:        ItemPathInput{Project: "p", Paths: nil},
			wantKind:  KindNoPaths,
			wantInSum: []string{"경로 0"},
		},
		{
			name: "하나라도 여기 있으면 이 항목은 여기 앵커돼 있다",
			in: ItemPathInput{
				Project: "p", Paths: []string{"a.go", "b.go"},
				Here: map[string]PathPresence{"a.go": PathPresent, "b.go": PathAbsent},
			},
			wantKind:  KindOK,
			wantInSum: []string{"p"},
		},
		{
			name: "Present 가 하나 있으면 Unknown 이 섞여도 ok 다",
			in: ItemPathInput{
				Project: "p", Paths: []string{"a.go", "b.go"},
				Here: map[string]PathPresence{"a.go": PathPresent, "b.go": PathUnknown},
			},
			wantKind: KindOK,
		},
		{
			name: "Absent 둘에 Unknown 하나면 오등록이라 말하지 않는다",
			in: ItemPathInput{
				Project: "p", Paths: []string{"a.go", "b.go", "c.go"},
				Here: map[string]PathPresence{"a.go": PathAbsent, "b.go": PathAbsent, "c.go": PathUnknown},
				Elsewhere: map[string]map[string]PathPresence{
					"q": {"a.go": PathPresent},
				},
			},
			wantKind:  KindUnknown,
			wantInSum: []string{"못 읽었다"},
			noSuggest: true,
		},
		{
			name: "여기 전부 없고 한 프로젝트만 지목하면 오등록이다",
			in: ItemPathInput{
				Project: "context-platform", Paths: []string{"x/y.go"},
				Here: map[string]PathPresence{"x/y.go": PathAbsent},
				Elsewhere: map[string]map[string]PathPresence{
					"kweiza-cc-plugins": {"x/y.go": PathPresent},
				},
			},
			wantKind:  KindMisregistered,
			wantSug:   "kweiza-cc-plugins",
			wantInSum: []string{"kweiza-cc-plugins"},
		},
		{
			name: "둘 이상이 지목되면 지목이 아니다",
			in: ItemPathInput{
				Project: "context-platform", Paths: []string{"docs/"},
				Here: map[string]PathPresence{"docs/": PathAbsent},
				Elsewhere: map[string]map[string]PathPresence{
					"a": {"docs/": PathPresent},
					"b": {"docs/": PathPresent},
				},
			},
			wantKind:  KindAmbiguous,
			wantCands: []string{"a", "b"},
			noSuggest: true,
		},
		{
			name: "어디에도 없으면 경로가 틀렸거나 레포가 미등록이다",
			in: ItemPathInput{
				Project: "kweiza-cc-plugins", Paths: []string{"internal/service/service.go"},
				Here: map[string]PathPresence{"internal/service/service.go": PathAbsent},
			},
			wantKind:  KindNowhere,
			wantInSum: []string{"미등록"},
			noSuggest: true,
		},
		{
			name: "못 읽은 프로젝트가 있으면 그 사실이 문장에 있다",
			in: ItemPathInput{
				Project: "p", Paths: []string{"x/y.go"},
				Here: map[string]PathPresence{"x/y.go": PathAbsent},
				Elsewhere: map[string]map[string]PathPresence{
					"q": {"x/y.go": PathPresent},
				},
				Unreadable: []string{"r"},
			},
			wantKind:  KindMisregistered,
			wantSug:   "q",
			wantInSum: []string{"못 읽었다", "r"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyItemPaths(tt.in)
			if got.Kind != tt.wantKind {
				t.Fatalf("Kind 가 %q 다 — %q 여야 한다 (Summary=%q)", got.Kind, tt.wantKind, got.Summary)
			}
			if tt.wantSug != "" && got.Suggest != tt.wantSug {
				t.Fatalf("Suggest 가 %q 다 — %q 여야 한다", got.Suggest, tt.wantSug)
			}
			if len(tt.wantCands) > 0 {
				if len(got.Candidates) != len(tt.wantCands) {
					t.Fatalf("Candidates 가 %v 다 — %v 여야 한다", got.Candidates, tt.wantCands)
				}
				for i, c := range tt.wantCands {
					if got.Candidates[i] != c {
						t.Fatalf("Candidates[%d] 가 %q 다 — %q 여야 한다(정렬돼야 한다)", i, got.Candidates[i], c)
					}
				}
			}
			// ★ Summary 는 어느 갈래에서도 비면 안 된다. 사유 없는 판정은 이 레포의 규율 위반이다.
			if strings.TrimSpace(got.Summary) == "" {
				t.Fatal("Summary 가 비었다 — 사유 없는 판정은 통과시키지 않는다")
			}
			for _, w := range tt.wantInSum {
				if !strings.Contains(got.Summary, w) {
					t.Fatalf("Summary 에 %q 가 없다: %s", w, got.Summary)
				}
			}
			// ★ 지목이 없어야 하는 갈래에서 Suggest 가 찍히면 렌더가 되돌리는 명령을 낸다 —
			//   그것이 곧 오등록 단정이고, 유일 지목 조건이 없던 규칙이 실물 큐에서
			//   5건 헛발화하던 자리가 정확히 여기다.
			if tt.noSuggest && got.Suggest != "" {
				t.Fatalf("이 갈래(%s)는 지목하면 안 되는데 Suggest 가 %q 다: %s",
					got.Kind, got.Suggest, got.Summary)
			}
		})
	}
}

// 0값이 "못 봤다"여야 한다. 이 단정이 깨지면 관측하지 않은 경로가 "없다"로 접힌다.
func TestPathPresenceZeroValueIsUnknown(t *testing.T) {
	var p PathPresence
	if p != PathUnknown {
		t.Fatalf("PathPresence 의 0값이 %v 다 — PathUnknown 이어야 한다", p)
	}
}
```

- [ ] **Step 2: 빨간불을 확인한다**

Run: `go test ./internal/judge/ -run 'TestClassifyItemPaths|TestPathPresenceZeroValueIsUnknown' -v`
Expected: FAIL — `undefined: ItemPathInput`, `undefined: ClassifyItemPaths` 등 컴파일 실패.

- [ ] **Step 3: 판정을 구현한다**

`internal/judge/itempaths.go` 를 만든다:

```go
package judge

import (
	"fmt"
	"sort"
	"strings"
)

// 항목이 선언한 경로가 그 프로젝트에 실재하는가 — 그 판정이다.
//
// 이 판정이 있는 이유는 실물 사고다: 항목 10건이 남의 프로젝트에 등록돼 그 프로젝트에는
// 존재하지도 않는 경로를 가리켰고, 그중 하나는 id 가 전역 유일이라 회수되지 않아
// 그 이름이 영구히 죽었다. add 응답이 등록 시점을 막았고(b315980), 이것은 **두 번째 그물**이다.
//
// ★ 규칙의 핵심은 "없다"가 아니라 "저기 있다"이고, 그 '저기'가 **하나로 지목될 때만**이다.
// 정답이 있는 데이터(오등록 10건)로 채점한 결과 두 규칙 다 9건을 잡는데,
// 유일 지목 조건이 없으면 지금 큐에서 5건을 헛발화한다 — 전부 `docs/` 처럼
// 어디에나 있는 이름이라 세 프로젝트를 동시에 가리킨 경우였다.
// 세 곳을 동시에 가리키는 것은 지목이 아니라 잡음이다.

// PathPresence 는 경로 하나를 한 저장소에서 찾아본 결과다. 셋을 가른다.
//
// ★ 0값이 PathUnknown 이다. "못 봤다"가 "없다"로 접히면 이 기능 전체가 거짓말이 된다 —
// 관측하지 않은 경로를 근거로 남의 항목을 오등록이라 고발하게 된다.
// 같은 논증이 diskFreePct 에도 있다(못 재면 0이 아니라 오류를 낸다).
type PathPresence int

const (
	PathUnknown PathPresence = iota // 못 봤다 — 판정 근거로 쓸 수 없다
	PathAbsent                      // 봤고, 없다
	PathPresent                     // 봤고, 있다
)

// Kind 는 진단 여섯 갈래다.
type Kind string

const (
	KindNoPaths       Kind = "no-paths"       // 항목에 경로가 0개다 — 판정할 재료가 없다
	KindOK            Kind = "ok"             // 한 경로라도 여기 있다
	KindMisregistered Kind = "misregistered"  // 여기 전부 없고, 다른 프로젝트 하나가 유일하게 지목된다
	KindAmbiguous     Kind = "ambiguous"      // 여기 전부 없는데 여럿이 지목된다 — 지목이 아니다
	KindNowhere       Kind = "nowhere"        // 어디에도 없다 — 경로가 틀렸거나 레포가 미등록이다
	KindUnknown       Kind = "unknown"        // 못 읽었다 — "없다"가 아니다
)

// ItemPathInput 은 판정에 필요한 **관측 결과**다. 이 구조체는 파일시스템을 모른다.
type ItemPathInput struct {
	Project    string
	Paths      []string
	Here       map[string]PathPresence            // 경로 → 이 프로젝트에서 본 결과
	Elsewhere  map[string]map[string]PathPresence // 프로젝트 → 경로 → 결과
	Unreadable []string                           // 아예 못 연 프로젝트
}

// ItemPathVerdict 는 판정 하나다.
//
// ★ json 태그를 반드시 단다. 이 값은 REST 를 왕복한다(PickResult 에 실려 나갔다 돌아온다).
// 이 패키지 안에 판례가 갈려 있어서(prescribe.go 는 달고 eligible.go 는 안 단다)
// 안 달면 "Kind"·"Summary" 같은 대문자 키가 나가고, 그 모양이 굳으면 되돌릴 수 없다.
type ItemPathVerdict struct {
	Kind       Kind     `json:"kind"`
	Suggest    string   `json:"suggest,omitempty"`    // 유일 지목일 때 그 프로젝트 id
	Candidates []string `json:"candidates,omitempty"` // 여럿이 지목될 때 전부(정렬)
	Unreadable []string `json:"unreadable,omitempty"` // 판정 근거가 그만큼 약하다
	Summary    string   `json:"summary"`              // 한 줄. ★ 항상 채운다
}

// ClassifyItemPaths 는 관측 결과를 진단으로 옮긴다. 순수 함수다.
//
// **판정 순서가 곧 우선순위다**: no-paths → ok → unknown → 나머지 셋.
//
//   - ok 가 unknown 보다 앞인 이유: 한 경로라도 여기 **실재하는 것을 봤으면**
//     다른 경로를 못 읽었어도 "이 항목은 여기 앵커돼 있다"는 결론이 안 흔들린다.
//   - unknown 이 남은 셋보다 앞인 이유: 그 셋은 전부 "여기 없다"를 **전제**하는데,
//     PathUnknown 이 섞여 있으면 그 전제 자체가 관측되지 않은 것이다.
//     못 읽은 경로 하나를 Absent 로 접으면 정확히 이 기능이 없애려는 종류의 거짓말이 된다.
func ClassifyItemPaths(in ItemPathInput) ItemPathVerdict {
	v := ItemPathVerdict{Unreadable: in.Unreadable}

	if len(in.Paths) == 0 {
		v.Kind = KindNoPaths
		v.Summary = "경로 0 — 이 항목은 겹침 축에 안 잡힌다. 아무도 안 막고, 아무도 이 항목을 못 피한다."
		return v
	}

	var present, absent, unknown int
	for _, p := range in.Paths {
		switch in.Here[p] {
		case PathPresent:
			present++
		case PathAbsent:
			absent++
		default:
			unknown++
		}
	}

	switch {
	case present > 0:
		v.Kind = KindOK
		v.Summary = fmt.Sprintf("%d개 중 %d개가 이 프로젝트(%s)에 있다.", len(in.Paths), present, in.Project)
		if unknown > 0 {
			v.Summary += fmt.Sprintf(" %d개는 못 읽었다.", unknown)
		}
	case unknown > 0:
		// ★ 여기가 이 함수에서 가장 중요한 분기다. 남은 것이 전부 Absent 여도
		//   못 읽은 것이 하나라도 있으면 오등록이라 말하지 않는다.
		v.Kind = KindUnknown
		v.Summary = fmt.Sprintf("%d개 중 %d개를 못 읽었다 — '없다'가 아니다. 이 축은 판정하지 않았다.",
			len(in.Paths), unknown)
	default:
		v = classifyAllAbsent(in, v, absent)
	}

	v.Summary += unreadableSuffix(in.Unreadable)
	return v
}

// classifyAllAbsent 는 "이 프로젝트에서 전부 없다"가 관측된 뒤의 세 갈래다.
func classifyAllAbsent(in ItemPathInput, v ItemPathVerdict, absent int) ItemPathVerdict {
	hits := make([]string, 0, len(in.Elsewhere))
	for proj, m := range in.Elsewhere {
		for _, pres := range m {
			if pres == PathPresent {
				hits = append(hits, proj)
				break
			}
		}
	}
	sort.Strings(hits) // 지목이 여럿일 때 순서가 흔들리면 같은 사실이 다른 문장이 된다

	switch len(hits) {
	case 0:
		v.Kind = KindNowhere
		v.Summary = fmt.Sprintf(
			"%d개 전부 이 프로젝트(%s)에 없고 등록된 어느 프로젝트에도 없다. "+
				"경로가 틀렸거나(뿌리가 잘렸을 수 있다) 그 레포가 아직 미등록이다. "+
				"지금 이 항목은 겹침 축에서 아무도 안 막는다.", absent, in.Project)
	case 1:
		v.Kind, v.Suggest = KindMisregistered, hits[0]
		v.Summary = fmt.Sprintf(
			"%d개 전부 이 프로젝트(%s)에 없다 — %s 에는 있다. 오등록일 수 있다.",
			absent, in.Project, hits[0])
	default:
		v.Kind, v.Candidates = KindAmbiguous, hits
		v.Summary = fmt.Sprintf(
			"%d개 전부 이 프로젝트(%s)에 없다. 등록된 다른 %d개 프로젝트(%s)에도 같은 이름이 있어 "+
				"어느 하나를 지목하지 못한다 — 근거로 쓰지 않는다.",
			absent, in.Project, len(hits), strings.Join(hits, ", "))
	}
	return v
}

// unreadableSuffix 는 못 연 프로젝트를 문장 끝에 붙인다.
// 숨기면 "지목이 유일하다"가 실제보다 강해 보인다 — 못 본 프로젝트가 같은 경로를
// 갖고 있었을 수 있고, 그러면 유일 지목이 아니라 모호였다.
func unreadableSuffix(un []string) string {
	if len(un) == 0 {
		return ""
	}
	s := append([]string(nil), un...)
	sort.Strings(s)
	return fmt.Sprintf(" (등록 프로젝트 %d개를 못 읽었다: %s — 지목이 그만큼 약하다)",
		len(s), strings.Join(s, ", "))
}
```

- [ ] **Step 4: 초록불을 확인한다**

Run: `go test ./internal/judge/ -run 'TestClassifyItemPaths|TestPathPresenceZeroValueIsUnknown' -v`
Expected: PASS — 하위 시험 8건 전부.

- [ ] **Step 5: 망가뜨려 시험이 실제로 잡는지 본다**

`ClassifyItemPaths` 의 `case unknown > 0:` 분기를 **잠시** 지우고(그 아래 `default` 로 떨어지게) 시험을 돌린다.

Run: `go test ./internal/judge/ -run TestClassifyItemPaths -v`
Expected: FAIL — `"Absent 둘에 Unknown 하나면 오등록이라 말하지 않는다"` 가 `Kind 가 "misregistered" 다 — "unknown" 여야 한다` 로 죽는다.

**확인했으면 되돌린다.** 이 단계를 건너뛰면 그 분기가 없어도 초록인 시험을 갖게 된다.

- [ ] **Step 6: 서식·정적검사·커밋**

```bash
gofmt -l . && go vet ./internal/judge/
git add internal/judge/itempaths.go internal/judge/itempaths_test.go
git commit -m "feat(judge): classify whether an item's declared paths exist in its project

Six verdicts: no-paths / ok / misregistered / ambiguous / nowhere / unknown.
The discriminator that matters is uniqueness -- 'it is over there' is only
evidence when 'there' names exactly one project. Scored against the real
misregistration incident (10 items): both the naive rule and this one catch 9,
but without the uniqueness condition the rule misfires 5 times on today's queue,
every one of them a generic directory name present in three repos.

Priority is no-paths -> ok -> unknown -> the rest. A single unread path never
collapses into 'absent' -- that collapse is the exact lie this feature exists
to prevent.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 2: 관측 — `os.Stat` 2단, 루트 먼저, `..` 는 거절

**Files:**
- Create: `internal/service/itempaths.go`
- Test: `internal/service/itempaths_test.go`

**Interfaces:**
- Consumes: Task 1 의 `judge.ClassifyItemPaths`·`judge.ItemPathInput`·`judge.PathPresence` 상수
- Produces: `func (s *Service) checkItemPaths(ctx context.Context, proj model.Project, paths []string) *judge.ItemPathVerdict`
  - 반환은 **포인터**다. 이 함수는 절대 `nil` 을 돌려주지 않는다 — `nil` 은 "안 읽었다"를 뜻하고, 이 함수가 돌았다는 것은 읽었다는 뜻이다.

**참고 — 이미 존재하는 것들(새로 만들지 마라):**
- `s.st` 는 **구체 타입 `*store.Store`** 다(인터페이스가 아니다). `s.st.ListProjects(ctx)` 가 바로 도달한다 — `internal/service/doctor.go` 가 이미 그렇게 부른다.
- `store.ListProjects(ctx context.Context) ([]model.Project, error)` — `internal/store/project.go:89`
- `model.Project{ID, Path, RemoteURL, DefaultBranch, Config, ConfigFromSHA, CreatedAt}`
- `clip(s string, n int) string` — 이 패키지의 헬퍼

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/service/itempaths_test.go` 를 만든다:

```go
package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
)

// twoProjects 는 등록된 프로젝트 둘을 만든다.
//
// ★ 세션을 여는 것이 곧 프로젝트 등록이다 — OpenSession 이 GetProject 가 ErrNotFound 일 때
// UpsertProject 한다. 그래서 프로젝트를 따로 등록하는 헬퍼가 이 패키지에 없다.
func twoProjects(t *testing.T, s *Service) (a, b model.Project) {
	t.Helper()
	repoA, repoB := newRepo(t), newRepo(t)
	openSession(t, s, "proj-a", repoA, repoA, "cc-a", "")
	openSession(t, s, "proj-b", repoB, repoB, "cc-b", "")
	pa, err := s.st.GetProject(ctx(), "proj-a")
	if err != nil {
		t.Fatalf("proj-a 조회 실패: %v", err)
	}
	pb, err := s.st.GetProject(ctx(), "proj-b")
	if err != nil {
		t.Fatalf("proj-b 조회 실패: %v", err)
	}
	return pa, pb
}

func TestCheckItemPathsSeesPresentPaths(t *testing.T) {
	s, _ := newSvc(t)
	pa, _ := twoProjects(t, s)
	writeFile(t, pa.Path, "internal/x/y.go", "package x\n")

	v := s.checkItemPaths(ctx(), pa, []string{"internal/x/y.go"})
	if v == nil {
		t.Fatal("판정이 nil 이다 — 이 함수는 nil 을 안 낸다")
	}
	if v.Kind != judge.KindOK {
		t.Fatalf("Kind 가 %q 다 — ok 여야 한다: %s", v.Kind, v.Summary)
	}
}

func TestCheckItemPathsNamesTheOneProjectThatHasThem(t *testing.T) {
	s, _ := newSvc(t)
	pa, pb := twoProjects(t, s)
	writeFile(t, pb.Path, "plugins/flightdeck/server/cmd/fd/migrate.go", "package main\n")

	// pa 에는 없고 pb 에만 있다 → 유일 지목 → 오등록.
	v := s.checkItemPaths(ctx(), pa, []string{"plugins/flightdeck/server/cmd/fd/migrate.go"})
	if v.Kind != judge.KindMisregistered {
		t.Fatalf("Kind 가 %q 다 — misregistered 여야 한다: %s", v.Kind, v.Summary)
	}
	if v.Suggest != pb.ID {
		t.Fatalf("Suggest 가 %q 다 — %q 여야 한다", v.Suggest, pb.ID)
	}
}

func TestCheckItemPathsDoesNotAccuseWhenBothProjectsHaveTheName(t *testing.T) {
	s, _ := newSvc(t)
	pa, pb := twoProjects(t, s)
	// `docs/` 모양 — 흔한 이름이라 여러 레포에 있다. pa 에만 없다.
	writeFile(t, pb.Path, "docs/keep.md", "x\n")
	third := newRepo(t)
	openSession(t, s, "proj-c", third, third, "cc-c", "")
	writeFile(t, third, "docs/keep.md", "x\n")

	v := s.checkItemPaths(ctx(), pa, []string{"docs/"})
	if v.Kind != judge.KindAmbiguous {
		t.Fatalf("Kind 가 %q 다 — ambiguous 여야 한다: %s", v.Kind, v.Summary)
	}
	if v.Suggest != "" {
		t.Fatalf("여럿이 지목됐는데 Suggest 가 %q 로 찍혔다", v.Suggest)
	}
}

func TestCheckItemPathsSaysNowhereWhenNoProjectHasThem(t *testing.T) {
	s, _ := newSvc(t)
	pa, _ := twoProjects(t, s)

	// 실물 결함 모양: 뿌리가 잘린 경로.
	v := s.checkItemPaths(ctx(), pa, []string{"internal/service/service.go"})
	if v.Kind != judge.KindNowhere {
		t.Fatalf("Kind 가 %q 다 — nowhere 여야 한다: %s", v.Kind, v.Summary)
	}
}

// ★ 이 시험이 이 태스크의 핵심이다.
// 루트를 따로 재지 않으면 죽은 프로젝트의 경로가 전부 ErrNotExist 로 와서
// "없다"로 접히고, 그 항목이 nowhere 나 misregistered 로 **고발당한다**.
func TestCheckItemPathsDistinguishesUnreadableRootFromAbsentPath(t *testing.T) {
	s, _ := newSvc(t)
	pa, _ := twoProjects(t, s)
	if err := os.RemoveAll(pa.Path); err != nil {
		t.Fatalf("저장소 제거 실패: %v", err)
	}

	v := s.checkItemPaths(ctx(), pa, []string{"internal/x/y.go"})
	if v.Kind != judge.KindUnknown {
		t.Fatalf("Kind 가 %q 다 — unknown 이어야 한다(루트가 없다): %s", v.Kind, v.Summary)
	}
	if strings.Contains(v.Summary, "없다 —") || strings.Contains(v.Summary, "미등록") {
		t.Fatalf("루트를 못 읽었는데 '없다'로 단정했다: %s", v.Summary)
	}
}

// 다른 프로젝트의 루트가 죽었으면 그 이름이 Unreadable 에 남아야 한다.
// 안 남기면 "유일 지목"이 실제보다 강해 보인다.
func TestCheckItemPathsReportsUnreadableOtherProjects(t *testing.T) {
	s, _ := newSvc(t)
	pa, pb := twoProjects(t, s)
	if err := os.RemoveAll(pb.Path); err != nil {
		t.Fatalf("저장소 제거 실패: %v", err)
	}

	v := s.checkItemPaths(ctx(), pa, []string{"internal/x/y.go"})
	found := false
	for _, u := range v.Unreadable {
		if u == pb.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("못 읽은 프로젝트 %q 가 Unreadable 에 없다: %+v", pb.ID, v.Unreadable)
	}
	if !strings.Contains(v.Summary, pb.ID) {
		t.Fatalf("못 읽었다는 사실이 문장에 없다: %s", v.Summary)
	}
}

// `..` 는 정규화하지 않고 거절한다. filepath.Join 에 그대로 stat 하면 프로젝트 밖을 관측한다.
func TestCheckItemPathsRefusesToLookOutsideTheRoot(t *testing.T) {
	s, _ := newSvc(t)
	pa, _ := twoProjects(t, s)
	outside := filepath.Join(filepath.Dir(pa.Path), "outside.txt")
	if err := os.WriteFile(outside, []byte("x\n"), 0o644); err != nil {
		t.Fatalf("바깥 파일 생성 실패: %v", err)
	}

	// 이 토큰은 실제로 존재하는 바깥 파일을 가리킨다. 그래도 "있다"가 되면 안 된다.
	v := s.checkItemPaths(ctx(), pa, []string{"../outside.txt"})
	if v.Kind == judge.KindOK {
		t.Fatalf("루트 밖 파일을 '있다'로 셌다: %s", v.Summary)
	}
	if v.Kind != judge.KindUnknown {
		t.Fatalf("Kind 가 %q 다 — unknown 이어야 한다(밖은 관측하지 않는다): %s", v.Kind, v.Summary)
	}
}

func TestCheckItemPathsHandlesZeroPaths(t *testing.T) {
	s, _ := newSvc(t)
	pa, _ := twoProjects(t, s)

	v := s.checkItemPaths(ctx(), pa, nil)
	if v.Kind != judge.KindNoPaths {
		t.Fatalf("Kind 가 %q 다 — no-paths 여야 한다: %s", v.Kind, v.Summary)
	}
}
```

- [ ] **Step 2: 빨간불을 확인한다**

Run: `go test ./internal/service/ -run TestCheckItemPaths -v`
Expected: FAIL — `s.checkItemPaths undefined`.

- [ ] **Step 3: 관측을 구현한다**

`internal/service/itempaths.go` 를 만든다:

```go
package service

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
)

// 항목이 선언한 경로가 실제로 그 프로젝트에 있는지 관측한다.
//
// **git 을 쓰지 않는다.** 답하려는 질문은 "이 경로가 이 레포에 실재하는가"이고
// 그 질문의 정본은 파일시스템이다. `git ls-files` 는 미추적 파일을 못 보고(방금 만든 파일을
// 가리키는 항목이 오탐이 된다), `git cat-file -e HEAD:<path>` 는 워크트리 상태를 못 본다.
// 그리고 프로세스 스폰이 stat 보다 서너 자릿수 비싸다.
//
// **2단이다.** 자기 프로젝트를 먼저 보고, 전부 없을 때만 남을 본다.
// 실측: 항목 하나당 stat 27회 · 0.048ms(보드 한 장의 git 파생이 이미 8~60ms 다).
//
// **derive(d.ok/d.fail)를 안 쓴다.** FreshnessOf 가 reads==0 → Source:"db",
// failures>0 → Stale 로 정의돼 있어서, stat 을 거기 세면 git 을 한 번도 안 읽은 응답이
// Source:"git" 이 되거나 git 이 멀쩡한 응답이 Stale 이 된다. 이 축은 자기 상태를
// 자기 안에서 말한다 — Unreadable 과 KindUnknown 이 그 자리다.

// checkItemPaths 는 항목 하나의 경로가 어느 프로젝트에 실재하는지 관측하고 판정한다.
//
// **절대 nil 을 돌려주지 않는다.** nil 은 "이 응답은 그 축을 안 읽었다"를 뜻하는데,
// 이 함수가 돌았다는 것 자체가 읽었다는 뜻이다. 못 읽은 것은 KindUnknown 으로 말한다.
func (s *Service) checkItemPaths(ctx context.Context, proj model.Project, paths []string) *judge.ItemPathVerdict {
	in := judge.ItemPathInput{Project: proj.ID, Paths: paths}
	if len(paths) == 0 {
		v := judge.ClassifyItemPaths(in)
		return &v
	}

	in.Here = observeIn(proj.Path, paths)

	// 1단에서 하나라도 봤으면(있다) 남을 볼 필요가 없다. 못 읽은 것이 섞여 있어도
	// 판정은 unknown 으로 갈 것이므로 역시 남을 볼 필요가 없다.
	if !allAbsent(paths, in.Here) {
		v := judge.ClassifyItemPaths(in)
		return &v
	}

	others, err := s.st.ListProjects(ctx)
	if err != nil {
		// 목록을 못 읽으면 "다른 데 있다/없다"를 말할 수 없다. 그 사실을 숨기지 않는다.
		s.log.WarnContext(ctx, "프로젝트 목록 조회 실패 — 경로 실재 축의 지목을 못 한다",
			"project", proj.ID, "error", err.Error())
		in.Unreadable = append(in.Unreadable, "(프로젝트 목록을 못 읽었다)")
		v := judge.ClassifyItemPaths(in)
		return &v
	}

	in.Elsewhere = map[string]map[string]judge.PathPresence{}
	for _, o := range others {
		if o.ID == proj.ID {
			continue
		}
		if !rootUsable(o.Path) {
			in.Unreadable = append(in.Unreadable, o.ID)
			continue
		}
		in.Elsewhere[o.ID] = observeIn(o.Path, paths)
	}

	v := judge.ClassifyItemPaths(in)
	return &v
}

// allAbsent 는 관측된 것이 **전부 Absent** 인지 본다.
// Present 가 하나라도 있으면 false, Unknown 이 하나라도 있어도 false 다 —
// 둘 다 "남의 프로젝트를 뒤질 근거가 없다"는 뜻이기 때문이다.
func allAbsent(paths []string, here map[string]judge.PathPresence) bool {
	for _, p := range paths {
		if here[p] != judge.PathAbsent {
			return false
		}
	}
	return true
}

// observeIn 은 저장소 하나에서 경로들을 stat 한다.
//
// ★ **루트를 먼저 잰다.** 루트가 통째로 없으면 그 아래 모든 경로의 stat 도 ErrNotExist 를
// 내는데, 그것을 Absent 로 접으면 죽은 프로젝트의 항목이 nowhere 나 misregistered 로
// **고발당한다.** "프로젝트를 못 열었으면 Unknown"과 "ErrNotExist 면 Absent"라는 두 규칙이
// 정확히 이 지점에서 충돌하고, 루트 stat 이 그 충돌을 없애는 유일한 단계다.
func observeIn(root string, paths []string) map[string]judge.PathPresence {
	out := make(map[string]judge.PathPresence, len(paths))
	if !rootUsable(root) {
		for _, p := range paths {
			out[p] = judge.PathUnknown // 0값이지만 명시한다 — 키 부재와 값을 가른다
		}
		return out
	}
	for _, p := range paths {
		out[p] = observeOne(root, p)
	}
	return out
}

// rootUsable 은 저장소 루트가 실제로 열리는 디렉토리인지 본다.
func rootUsable(root string) bool {
	if strings.TrimSpace(root) == "" {
		return false
	}
	st, err := os.Stat(root)
	return err == nil && st.IsDir()
}

// observeOne 은 경로 하나를 관측한다.
//
// ★ 루트 **밖으로 나가는 토큰은 stat 하지 않는다.** judge.components 가 ".." 를 일부러
// 안 걷어내는 것과 같은 규율이다 — 조용히 정규화하면 그 입력 오류가 안 보인다.
// 그리고 filepath.Join(root, "../../etc") 에 그대로 stat 하면 프로젝트 밖을 관측하게 되어
// 판정이 남의 디렉토리에 기댄다.
//
// 밖인지는 문자열 접두가 아니라 filepath.Rel 로 성분 단위 계산한다 — 접두로 하면
// root="/a/b" 일 때 "/a/bc/d" 가 안이라고 나온다(같은 모양의 결함이 이 레포에 실재했다).
func observeOne(root, p string) judge.PathPresence {
	if strings.TrimSpace(p) == "" {
		return judge.PathUnknown
	}
	joined := filepath.Join(root, p)
	rel, err := filepath.Rel(filepath.Clean(root), joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return judge.PathUnknown // 루트 밖이다. 관측하지 않는다
	}
	if _, err := os.Stat(joined); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return judge.PathAbsent
		}
		return judge.PathUnknown // 권한·I/O — **절대 Absent 가 아니다**
	}
	return judge.PathPresent
}
```

- [ ] **Step 4: 초록불을 확인한다**

Run: `go test ./internal/service/ -run TestCheckItemPaths -v`
Expected: PASS — 시험 8건 전부.

- [ ] **Step 5: 망가뜨려 시험이 실제로 잡는지 본다**

`observeIn` 의 루트 검사(`if !rootUsable(root) { … }` 블록)를 **잠시** 지운다.

Run: `go test ./internal/service/ -run TestCheckItemPathsDistinguishesUnreadableRootFromAbsentPath -v`
Expected: FAIL — `Kind 가 "nowhere" 다 — unknown 이어야 한다(루트가 없다)`.

그리고 `observeOne` 의 `filepath.Rel` 검사를 **잠시** 지운다.

Run: `go test ./internal/service/ -run TestCheckItemPathsRefusesToLookOutsideTheRoot -v`
Expected: FAIL — `루트 밖 파일을 '있다'로 셌다`.

**둘 다 확인했으면 되돌린다.**

- [ ] **Step 6: 서식·정적검사·커밋**

```bash
gofmt -l . && go vet ./internal/service/ && go test ./internal/service/ ./internal/judge/
git add internal/service/itempaths.go internal/service/itempaths_test.go
git commit -m "feat(service): observe whether an item's paths exist, two-stage and root-first

os.Stat, not git: the question is whether the path exists in the repo, and the
filesystem is the authority for that. git ls-files misses untracked files and
cat-file misses worktree state; a process spawn also costs orders of magnitude
more than a stat.

Two stages -- own project first, others only when everything is absent.
Measured at 27 stats / 0.048ms per item.

Root is stat'd before any path under it. Without that step 'project unreadable
-> unknown' and 'ErrNotExist -> absent' collide on a dead repo, and every item
in it gets accused of being misregistered. Paths escaping the root via '..' are
refused rather than normalised -- same discipline as judge.components.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 3: `PickResult` 에 필드를 붙이고 세 갈래에서 채운다

**Files:**
- Modify: `internal/service/pick.go` (구조체 `PickResult` · `pickExplicit` 의 재개/선점 반환 · `pickRecommend` 의 추천 반환)
- Test: `internal/service/pick_test.go` (추가)

**Interfaces:**
- Consumes: Task 2 의 `s.checkItemPaths(ctx, proj, paths) *judge.ItemPathVerdict`
- Produces: `service.PickResult.PathCheck *judge.ItemPathVerdict` (json `path_check,omitempty`)

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/service/pick_test.go` 끝에 붙인다:

```go
// pick 은 세 모드 전부에서 경로 실재 판정을 낸다.
// none(적격 0건)에는 항목이 없으므로 안 낸다 — 관측할 대상이 없다.
func TestPickCarriesPathCheckInEveryModeThatHasAnItem(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	sess := openSession(t, s, "proj", repo, repo, "cc-1", "")
	writeFile(t, repo, "internal/x/y.go", "package x\n")
	addItem(t, s, "proj", "t-here", []string{"internal/x/y.go"}, nil)

	// ① 추천
	rec, err := s.Pick(ctx(), PickInput{Project: "proj", SessionID: sess.Session.ID})
	if err != nil {
		t.Fatalf("추천 실패: %v", err)
	}
	if rec.PathCheck == nil {
		t.Fatal("추천에 경로 실재 판정이 없다")
	}
	if rec.PathCheck.Kind != judge.KindOK {
		t.Fatalf("Kind 가 %q 다 — ok 여야 한다: %s", rec.PathCheck.Kind, rec.PathCheck.Summary)
	}

	// ② 선점
	cl, err := s.Pick(ctx(), PickInput{Project: "proj", SessionID: sess.Session.ID, ItemID: "t-here"})
	if err != nil {
		t.Fatalf("선점 실패: %v", err)
	}
	if cl.Mode != PickClaimed || cl.PathCheck == nil {
		t.Fatalf("선점에 경로 실재 판정이 없다(mode=%s)", cl.Mode)
	}

	// ③ 재개 — 같은 세션이 다시 부른다
	re, err := s.Pick(ctx(), PickInput{Project: "proj", SessionID: sess.Session.ID, ItemID: "t-here"})
	if err != nil {
		t.Fatalf("재개 실패: %v", err)
	}
	if re.Mode != PickResumed || re.PathCheck == nil {
		t.Fatalf("재개에 경로 실재 판정이 없다(mode=%s)", re.Mode)
	}
}

// 적격 0건에는 항목이 없다 → 판정도 없다. nil 이어야 한다.
func TestPickNoneHasNoPathCheck(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	sess := openSession(t, s, "proj", repo, repo, "cc-1", "")

	res, err := s.Pick(ctx(), PickInput{Project: "proj", SessionID: sess.Session.ID})
	if err != nil {
		t.Fatalf("추천 실패: %v", err)
	}
	if res.Mode != PickNone {
		t.Fatalf("mode 가 %q 다 — none 이어야 한다(큐가 비었다)", res.Mode)
	}
	if res.PathCheck != nil {
		t.Fatalf("항목이 없는데 경로 실재 판정이 실렸다: %+v", res.PathCheck)
	}
}
```

`internal/service/pick_test.go` 의 import 에 `"github.com/kweiza/flightdeck/internal/judge"` 가 없으면 더한다.

- [ ] **Step 2: 빨간불을 확인한다**

Run: `go test ./internal/service/ -run 'TestPickCarriesPathCheck|TestPickNoneHasNoPathCheck' -v`
Expected: FAIL — `rec.PathCheck undefined (type PickResult has no field or method PathCheck)`.

- [ ] **Step 3: 필드를 더한다**

`internal/service/pick.go` 의 `PickResult` 에서 `Scope` 줄 **뒤**, `Derived` **앞**에 넣는다:

```go
	Scope    string            `json:"scope"`              // 무엇을 후보로 봤나 — 안 본 것을 침묵하지 않는다

	// PathCheck 는 이 항목이 선언한 경로가 이 프로젝트에 실재하는가다.
	//
	// ★ **포인터다.** nil 은 "이 응답은 그 축을 안 읽었다"를 뜻하고, 그 상태가 실제로 난다:
	// 오프라인 `fd next` 는 디스크 캐시의 옛 바이트를 그대로 다시 내는데, 이 필드가
	// 생기기 전에 저장된 캐시에는 키가 없어 역직렬화 후 nil 이 온다.
	// 값 타입이면 그 상황이 Kind:"" 라는 여섯 갈래 어디에도 없는 유령 상태가 되고,
	// 낡은 캐시가 관측한 적 없는 사실을 단정하게 된다.
	//
	// 적격 0건(PickNone)에도 nil 이다 — 항목이 없으면 관측할 대상이 없다.
	PathCheck *judge.ItemPathVerdict `json:"path_check,omitempty"`

	Derived
```

- [ ] **Step 4: 세 갈래에서 채운다**

`pickExplicit` 에서 `res` 를 만든 직후(`res.Setup = SetupCommands(...)` 바로 아래)에 한 줄:

```go
	res.PathCheck = s.checkItemPaths(ctx, proj, item.Paths)
```

이 한 줄이 **재개와 선점 두 반환 경로를 동시에** 덮는다(둘 다 같은 `res` 를 쓴다).

`pickRecommend` 에서는 `res.Setup = SetupCommands(...)` 바로 아래에 같은 한 줄을 넣는다:

```go
	res.PathCheck = s.checkItemPaths(ctx, proj, item.Paths)
```

**`picked == nil` 갈래(적격 0건)에는 넣지 않는다.** 그 자리에는 항목이 없다.

- [ ] **Step 5: 초록불을 확인한다**

Run: `go test ./internal/service/ -run 'TestPickCarriesPathCheck|TestPickNoneHasNoPathCheck' -v`
Expected: PASS.

Run: `go test ./internal/service/`
Expected: PASS — 기존 시험 전부 그대로.

- [ ] **Step 6: 커밋**

```bash
gofmt -l . && go vet ./internal/service/
git add internal/service/pick.go internal/service/pick_test.go
git commit -m "feat(service): carry the path-existence verdict on PickResult

Pointer, not value. nil means 'this response did not read that axis', and that
state genuinely occurs: an offline 'fd next' replays the bytes it cached, and a
cache written before this field existed unmarshals to nil. A value type would
turn that into Kind:\"\" -- a ghost state in none of the six verdicts -- and a
stale cache would assert a fact nobody ever observed.

Filled in recommended/claimed/resumed. Not in none: no item, nothing to observe.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 4: 렌더 — `경로 실재:` 한 줄과 되돌리는 명령

**Files:**
- Modify: `internal/mcpsrv/render.go` (`RenderPick`, 현재 `542-555` 의 `if r.Item != nil` 블록)
- Test: `internal/mcpsrv/render_test.go` (추가)

**Interfaces:**
- Consumes: Task 3 의 `service.PickResult.PathCheck`, Task 1 의 `judge.Kind` 상수
- Produces: 없음(문자열만)

**왜 `if r.Item != nil` 블록 안인가:** 그 위치가 곧 "`none` 에는 안 낸다"의 구현이다. 그리고 항목이 선언한 경로 줄(`경로: …`) 바로 아래라 **선언과 관측이 나란히 읽힌다.**

**왜 접두가 `경로 실재:` 인가:** `RenderPick` 은 이미 `경로: <목록>` 을 찍는다. 같은 접두를 쓰면 한 응답에 같은 모양의 줄이 둘 생겨 어느 쪽이 선언이고 어느 쪽이 관측인지 안 갈린다.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/mcpsrv/render_test.go` 끝에 붙인다:

```go
// pickWith 는 경로 실재 판정 하나를 실은 pick 결과를 만든다.
func pickWith(v *judge.ItemPathVerdict, paths []string) service.PickResult {
	item := model.Item{
		Project: "proj", ID: "t-path", Title: "제목", Body: "본문",
		Paths: paths, State: model.ItemOpen, CreatedAt: t0,
	}
	return service.PickResult{
		Mode: service.PickClaimed, Reason: "선점했다", Scope: "지정된 항목 1건",
		Item: &item, Branch: item.ID, PathCheck: v,
	}
}

func TestRenderPickNamesMisregistrationAndTheWayBack(t *testing.T) {
	got := RenderPick(pickWith(&judge.ItemPathVerdict{
		Kind: judge.KindMisregistered, Suggest: "kweiza-cc-plugins",
		Summary: "1개 전부 이 프로젝트(context-platform)에 없다 — kweiza-cc-plugins 에는 있다. 오등록일 수 있다.",
	}, []string{"plugins/x.go"}), t0)

	for _, want := range []string{
		"경로 실재:",
		"오등록일 수 있다",
		"fd move t-path --project kweiza-cc-plugins",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q 가 없다:\n%s", want, got)
		}
	}
}

// ★ V1(유일 지목 조건이 없는 규칙)의 헛발화 5건이 여기서 죽는다.
// 여럿이 지목될 때 되돌리는 명령을 내면 그것이 곧 오등록 단정이다.
func TestRenderPickDoesNotPrescribeMoveWhenAmbiguous(t *testing.T) {
	got := RenderPick(pickWith(&judge.ItemPathVerdict{
		Kind: judge.KindAmbiguous, Candidates: []string{"a", "b", "c"},
		Summary: "1개 전부 이 프로젝트에 없다. 등록된 다른 3개 프로젝트(a, b, c)에도 같은 이름이 있어 어느 하나를 지목하지 못한다 — 근거로 쓰지 않는다.",
	}, []string{"docs/"}), t0)

	if !strings.Contains(got, "지목하지 못한다") {
		t.Fatalf("모호하다는 사실이 없다:\n%s", got)
	}
	if strings.Contains(got, "fd move") {
		t.Fatalf("여럿이 지목됐는데 되돌리는 명령을 냈다 — 그것이 곧 오등록 단정이다:\n%s", got)
	}
}

func TestRenderPickStatesThePathAxisEvenWhenClean(t *testing.T) {
	got := RenderPick(pickWith(&judge.ItemPathVerdict{
		Kind: judge.KindOK, Summary: "2개 중 2개가 이 프로젝트(proj)에 있다.",
	}, []string{"a.go", "b.go"}), t0)

	if !strings.Contains(got, "경로 실재:") {
		t.Fatalf("이상이 없어도 경로 축 줄은 있어야 한다 — 침묵하면 '이상 없다'와 '안 봤다'가 같은 화면이 된다:\n%s", got)
	}
}

// nil 은 침묵이 아니다. 낡은 캐시가 "이상 없다"처럼 보이면 안 된다.
func TestRenderPickSaysTheAxisWasNotReadWhenVerdictIsNil(t *testing.T) {
	got := RenderPick(pickWith(nil, []string{"a.go"}), t0)

	if !strings.Contains(got, "읽지 않았다") {
		t.Fatalf("판정이 nil 인데 그 사실을 말하지 않는다:\n%s", got)
	}
}

// 적격 0건에는 항목이 없으므로 이 줄도 없다.
func TestRenderPickOmitsPathAxisWhenThereIsNoItem(t *testing.T) {
	got := RenderPick(service.PickResult{
		Mode: service.PickNone, Reason: "적격 항목이 0건이다", Scope: "후보 = 열린 항목 0건",
	}, t0)

	if strings.Contains(got, "경로 실재:") {
		t.Fatalf("항목이 없는데 경로 축 줄이 나왔다:\n%s", got)
	}
}
```

import 에 `"github.com/kweiza/flightdeck/internal/judge"` 가 없으면 더한다(이미 `judge.RejectClaimed` 를 쓰는 시험이 있어 대개 있다).

- [ ] **Step 2: 빨간불을 확인한다**

Run: `go test ./internal/mcpsrv/ -run TestRenderPick -v`
Expected: FAIL — `경로 실재:` 를 못 찾는다(그리고 `PathCheck` 필드가 이미 있으므로 컴파일은 된다).

- [ ] **Step 3: 렌더 블록을 넣는다**

`internal/mcpsrv/render.go` 의 `if r.Item != nil` 블록에서 `선행:` 줄 **앞**에 넣는다:

```go
	if r.Item != nil {
		it := *r.Item
		fmt.Fprintf(&b, "\n▸ %s — %s [%s]\n", it.ID, it.Title, it.State)
		if len(it.Paths) > 0 {
			fmt.Fprintf(&b, "경로: %s\n", strings.Join(it.Paths, ", "))
		}
		b.WriteString(renderPathCheck(r.PathCheck, it.ID))
		if len(it.After) > 0 {
```

그리고 파일 아래쪽(`formatAfter` 옆)에 순수 함수를 더한다:

```go
// renderPathCheck 는 경로 실재 축 한 줄이다. 순수 함수다.
//
// ★ **어느 갈래에서도 침묵하지 않는다.** 이상이 없어도 한 줄을 찍는 이유는
// RenderTail 이 겹침 0건일 때도 "겹침: 없음"을 찍는 것과 같다 — 침묵하면
// "이상 없다"와 "이 축을 안 봤다"가 같은 화면이 되고, 그러면 stat 이 전부 실패한 날에도
// pick 은 평소와 똑같아 보인다.
//
// ★ 접두가 "경로 실재:" 인 이유는 바로 위 줄이 이미 "경로: <목록>" 이기 때문이다.
// 같은 접두를 쓰면 선언과 관측이 안 갈린다.
//
// ★ 되돌리는 명령은 **유일 지목일 때만** 낸다. 여럿이 지목된 상태에서 그 명령을 내면
// 그것이 곧 오등록 단정이고, 그 단정이 실물 큐에서 5건 헛발화하던 규칙이다.
func renderPathCheck(v *judge.ItemPathVerdict, itemID string) string {
	if v == nil {
		return "경로 실재: 이 응답은 그 축을 읽지 않았다(낡은 캐시일 수 있다).\n"
	}
	s := "경로 실재: " + v.Summary + "\n"
	if v.Kind == judge.KindMisregistered && v.Suggest != "" {
		s += fmt.Sprintf("           맞다면 지금 되돌려라: `fd move %s --project %s`\n", itemID, v.Suggest)
	}
	return s
}
```

`internal/mcpsrv/render.go` 의 import 에 `"github.com/kweiza/flightdeck/internal/judge"` 가 없으면 더한다.

- [ ] **Step 4: 초록불을 확인한다**

Run: `go test ./internal/mcpsrv/ -v -run TestRenderPick`
Expected: PASS — 새 시험 5건 + 기존 `TestRenderPickCarriesBranchAndWorktree`.

- [ ] **Step 5: 도구 표가 안 늘었는지 확인한다**

Run: `go test ./internal/mcpsrv/ -run 'TestToolTableIsSix|TestFixingMisregistrationDidNotGrowTheToolTable|TestInitializeAndToolsListRoundTrip' -v`
Expected: PASS — 셋 다. 이 계획은 응답 내용만 바꾸고 도구 표는 안 건드린다(DESIGN §6).

- [ ] **Step 6: 망가뜨려 시험이 실제로 잡는지 본다**

`renderPathCheck` 의 `v.Kind == judge.KindMisregistered` 조건을 **잠시** 지워 모든 갈래에서 `fd move` 를 내게 한다.

Run: `go test ./internal/mcpsrv/ -run TestRenderPickDoesNotPrescribeMoveWhenAmbiguous -v`
Expected: FAIL — `여럿이 지목됐는데 되돌리는 명령을 냈다`.

**확인했으면 되돌린다.**

- [ ] **Step 7: 커밋**

```bash
gofmt -l . && go vet ./internal/mcpsrv/ && go test ./internal/mcpsrv/
git add internal/mcpsrv/render.go internal/mcpsrv/render_test.go
git commit -m "feat(mcpsrv): print the path-existence axis on every pick that has an item

One line, always -- same discipline as RenderTail printing '겹침: 없음' when
there is no overlap. Silence would make 'nothing wrong' and 'never looked'
render identically, so a day when every stat fails would look ordinary.

The prefix is '경로 실재:' because the line above it is already '경로: <list>';
sharing a prefix would blur what the item declared with what we observed.

The move prescription fires only on a unique match. Emitting it under ambiguity
is itself the misregistration claim -- the one that misfired 5 times.

One render site covers three consumers: the MCP pick tool, 'fd next', 'fd pick'.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 5: REST 왕복을 끝에서 끝까지 단정한다

**Files:**
- Modify: `cmd/fd/wire_test.go` (추가)

**Interfaces:**
- Consumes: Task 1~4 전부
- Produces: 없음(시험만)

**왜 필요한가:** 이 필드는 서버 → JSON → `mcpbackend` → `RenderPick` 을 지난다. 손으로 옮겨 적는 자리는 없지만(양쪽이 같은 Go 구조체를 쓴다), **그 사실을 지키는 시험이 레포에 하나도 없다.** `reflect` 로 필드 왕복을 강제하는 그물도 없어서, 어느 계층에서 떨어뜨려도 자동으로 빨간불이 안 켜진다.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`cmd/fd/wire_test.go` 끝에 붙인다:

```go
// 경로 실재 판정이 서버 → JSON → 클라이언트 → 렌더까지 살아 오는지 본다.
//
// ★ 이 축을 지키는 시험이 없으면, 어느 계층이 이 필드를 떨어뜨려도 조용하다.
// 레포에 구조체 필드 왕복을 reflect 로 강제하는 그물이 없다.
func TestPathCheckSurvivesTheRestRoundTrip(t *testing.T) {
	h := newHarness(t)

	// ★ open 이 먼저다 — 프로젝트 행을 만드는 것이 이 명령이고, 없으면 add 가 FK 로 죽는다.
	code, out := h.run("", "open", "--label", "경로축")
	if code != 0 {
		t.Fatalf("open 실패(%d): %s", code, out)
	}

	// 이 프로젝트에 없는 경로를 선언한 항목. 등록된 프로젝트가 이것 하나뿐이라 nowhere 다.
	// 플래그는 `--path` 다(단수·반복) — `--paths` 가 아니다.
	code, out = h.run("", "add",
		"--id", "t-path-rt",
		"--title", "경로 실재 왕복",
		"--body", "본문이다",
		"--path", "internal/nope/gone.go")
	if code != 0 {
		t.Fatalf("add 실패(%d): %s", code, out)
	}

	code, out = h.run("", "next")
	if code != 0 {
		t.Fatalf("next 실패(%d): %s", code, out)
	}
	mustContain(t, "fd next 출력", out, "경로 실재:", "어느 프로젝트에도 없다")

	code, out = h.run("", "pick", "t-path-rt")
	if code != 0 {
		t.Fatalf("pick 실패(%d): %s", code, out)
	}
	mustContain(t, "fd pick 출력", out, "경로 실재:")
}
```

이 시험은 새 import 를 안 쓴다 — `newHarness`·`h.run`·`mustContain` 이 전부 `cmd/fd/harness_test.go` 에 있다.

- [ ] **Step 2: 빨간불을 먼저 본다**

이 시험은 Task 1~4 가 끝났으면 바로 초록일 수 있다. **그러면 시험이 무엇을 지키는지 알 수 없다.** 그래서 먼저 망가뜨린다 — `internal/service/pick.go` 의 `pickRecommend` 에 넣은 `res.PathCheck = …` 한 줄을 **잠시** 지운다.

Run: `go test ./cmd/fd/ -run TestPathCheckSurvivesTheRestRoundTrip -v`
Expected: FAIL — `fd next 출력 에 "어느 프로젝트에도 없다" 가 없다`(대신 "읽지 않았다"가 나온다).

**확인했으면 되돌린다.**

- [ ] **Step 3: 초록불을 확인한다**

Run: `go test ./cmd/fd/ -run TestPathCheckSurvivesTheRestRoundTrip -v`
Expected: PASS.

- [ ] **Step 4: 커밋**

```bash
gofmt -l . && go vet ./cmd/fd/
git add cmd/fd/wire_test.go
git commit -m "test(fd): pin the path-existence verdict across the REST round trip

Server -> JSON -> mcpbackend -> RenderPick. No layer hand-maps PickResult, but
nothing in the repo enforced that: there is no reflect-based field round-trip
net, so any layer could drop the field silently.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 6: 전체 검증과 실물 확인

**Files:** 없음(검증만)

- [ ] **Step 1: 전 패키지 시험**

Run: `go build ./... && go vet ./... && gofmt -l . && go test ./... -race`
Expected: 빌드·vet 조용 · `gofmt -l .` **빈 출력** · 전 패키지 `ok`.

`gofmt -l .` 이 파일을 뱉으면 `gofmt -w <파일>` 로 고치고 다시 돌린다.

- [ ] **Step 2: 실물 큐에 대고 확인한다 — 오늘 잡히는 결함 2건**

스펙 §2 가 실물 결함 2건을 지목했다: `fd-live-window-baseline`(`internal/service/service.go`)과 `fd-prescribe-threshold-baseline`(`internal/judge/prescribe.go`). 둘 다 뿌리가 잘린 경로다.

**서버를 새로 빌드해 띄우지 말고**(도는 서버를 죽이면 다른 세션의 조정이 끊긴다) 시험 하네스로 같은 모양을 재현한 것이 Task 2·5 다. 실물 확인은 이 항목이 랜딩된 **뒤** 도는 서버가 갱신되고 나서 `pick` 을 불러 본다.

이 단계에서는 **그 두 항목이 여전히 그 모양인지만** 확인한다:

```bash
python3 - <<'PY'
import sqlite3, os, json
con = sqlite3.connect("file:/home/aaron/.flightdeck/fd.db?mode=ro", uri=True)
projs = {r[0]: r[1] for r in con.execute("select id, path from project")}
for iid in ("fd-live-window-baseline", "fd-prescribe-threshold-baseline"):
    row = con.execute("select project, paths from item where id=?", (iid,)).fetchone()
    if not row:
        print(f"{iid}: 큐에 없다(이미 처리됐다)"); continue
    proj, paths = row[0], json.loads(row[1])
    here = [p for p in paths if os.path.exists(os.path.join(projs[proj], p))]
    print(f"{iid}: 경로={paths} · 이 프로젝트에 있는 것={here}")
PY
```

Expected: 둘 다 `이 프로젝트에 있는 것=[]`. 그렇지 않으면 누군가 이미 고친 것이고, 그 사실을 `finish` 판단에 적는다.

- [ ] **Step 3: 커밋(있으면) 후 다음 단계**

시험만 돌렸다면 커밋할 것이 없다. 있으면 커밋하고, `superpowers:finishing-a-development-branch` 로 랜딩을 판단한 뒤 `fd finish` 로 항목을 닫는다.

`fd finish` 본문에 반드시 적을 것(스펙이 이미 답을 갖고 있다):

- **일부러 안 한 것**: `board` 에 안 실었다 · 부분 미스를 경보로 안 만들었다 · 미등록 레포를 디스크에서 찾아 나서지 않았다 · stat 에 상한을 안 뒀다 · `derive` 를 안 건드렸다.
- **못 잡는 것**: 경로가 **틀린 프로젝트에 진짜로 존재하는** 오등록(`fd-ci-timing-baseline` 모양). 존재 기반 규칙의 원리적 상한이고, 실측 10건 중 9건이 이 축의 최대치다.
- **뒤집히는 조건**: 등록 프로젝트가 늘고 경로가 짧아져 `ambiguous` 가 흔해지면 판별자를 V4(유일 지목 ∧ 매치 경로 2성분 이상)로 좁힌다. 원격 마운트를 등록하면 stat 상한을 `diskFreePct` 와 함께 한 축으로 다룬다.
- **후속 후보**: `fd import` 요약에 같은 축을 싣기(대량 수입은 add 응답을 안 보는 유일한 경로라 값이 가장 크게 난다).

---

## 태스크 의존 관계

```
Task 1 (judge)  ──▶  Task 2 (service 관측)  ──▶  Task 3 (PickResult)  ──▶  Task 4 (render)  ──▶  Task 5 (왕복 시험)  ──▶  Task 6 (검증)
```

전부 직렬이다. Task 4 의 시험이 Task 3 의 필드를 쓰고, Task 5 는 Task 4 의 문구를 단정한다.
