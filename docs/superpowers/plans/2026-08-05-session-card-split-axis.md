# 세션 카드 갈림 축 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 한 대화가 카드 여러 장이 되는 축을 닫는다 — 갈림을 만드는 문 둘을 막고, 이미 갈린 것을 표시에서 접고, 그 갈림이 지표를 오염시키는 것을 드러낸다.

**Architecture:** 원인 코드(`resolveProject` 의 `--show-toplevel` 정규화)는 이미 옳고 이미 `main` 에 있다 — 안 도는 것뿐이다(스펙 §2). 그래서 이 브랜치는 원인을 다시 고치지 않고 **원장이 스스로 말하게 한다**: `judge` 의 순수 함수가 "정규화가 안 돈 흔적"을 원장만으로 탐지하고, `service` 가 카드를 대화 단위로 접고, `render` 가 그 둘을 낸다. 여기에 빈 카드를 만들지 않는 읽기 전용 세션 조회를 더한다.

**Tech Stack:** Go 1.x (표준 라이브러리만) · SQLite(`database/sql`, 드라이버는 기존 것) · 시험은 `testing` 표 시험 + 운영 진입점 배선 시험.

## Global Constraints

스펙 `docs/superpowers/specs/2026-08-05-session-card-split-axis-design.md` 의 §7 이 전 과제에 걸린다. 값 그대로 옮긴다.

- **`cmd/fd/env.go` 의 `resolveProject` 를 고치지 않는다.** 이미 옳다.
- **`internal/buildinfo` 를 건드리지 않는다** — `fd-vcs-stamp-blind-to-worktree` 몫이다.
- **`DESIGN.md` §3 헤더의 표 수를 건드리지 않는다** — `fd-design-table-count-confirm` 몫이다.
- **소급 병합(원장 이관)을 하지 않는다.** 3중키 UNIQUE 충돌 해소 경로가 설계에 없다. 표시 계층만이다.
- **경로 접두 일치는 `judge/split.go` 의 보고 전용 함수 밖으로 나가지 않는다.** 정체 판정에도 겹침 판정에도 안 쓴다.
- **빈 `cc_session_id` 끼리는 절대 같다고 보지 않는다.** 못 읽음을 값으로 접으면 관측이 깨진 순간 축이 통째로 거짓 초록을 낸다.
- **남의 미랜딩 자리를 안 연다:** `internal/service/pick.go` · `internal/store/judgment.go` · `internal/judge/bundle.go` · `internal/judge/prescribe.go` · `internal/judge/eligible.go` · `internal/store/store.go` · `internal/store/probe.go` · `internal/api/handlers_meta.go` · `cmd/fd/client.go` · `internal/mcpsrv/render.go` 의 `renderLane`·`RenderTail`·`noteLines`·`RenderPick`.
- **모든 과제는 "수정을 되돌려 빨강을 확인"한 뒤에만 초록이라고 말한다.** 대조 단정을 함께 건다.
- 검증 명령은 `plugins/flightdeck/server` 에서 돈다.

---

## File Structure

| 파일 | 책임 | 상태 |
|---|---|---|
| `internal/judge/split.go` | 정규화 미실행 흔적 탐지 + `SameConversation` 공개 껍데기 | 신규 |
| `internal/judge/split_test.go` | 위의 표 시험 | 신규 |
| `internal/service/board.go` | 대화 접기 · 탐지 배선 · ack 도달성 배선 | 수정 |
| `internal/service/board_conversation_test.go` | 접기·탐지·도달성 시험 | 신규 |
| `internal/store/session.go` | 3중키 읽기 전용 조회(Store 수준) | 수정 |
| `internal/store/prescribe_reach.go` | ack 도달성 질의 | 신규 |
| `internal/store/prescribe_reach_test.go` | 위 질의 시험 | 신규 |
| `internal/service/session.go` | `FindSession` 조회 | 수정 |
| `internal/api/api.go` | `GET /api/v1/sessions` 라우트 한 줄 | 수정 |
| `internal/api/handlers_session.go` | `handleFindSession` | 수정 |
| `internal/api/find_session_test.go` | 조회가 행을 안 만드는 것 단정 | 신규 |
| `internal/mcpsrv/render.go` | 머리줄 · 묶음 카드 · detail 전개 · 배너 · 꼬리 | 수정 |
| `internal/mcpsrv/render_conversation_test.go` | 렌더 시험 | 신규 |
| `cmd/fd/app.go` | `App.FindSession` | 수정 |
| `cmd/fd/hook.go` | 복구 갈래 한 줄 교체 + 주석 정정 | 수정 |
| `cmd/fd/hook_recovery_test.go` | 복구 갈래가 빈 카드를 안 만드는 것 단정 | 신규 |
| `plugins/flightdeck/DESIGN.md` | §10 확인율 분모 정정 | 수정 |

---
### Task 1: `judge/split.go` — 정규화 미실행 흔적 탐지

**Files:**
- Create: `plugins/flightdeck/server/internal/judge/split.go`
- Test: `plugins/flightdeck/server/internal/judge/split_test.go`

**Interfaces:**
- Consumes: 없음(이 과제가 시작점이다).
- Produces: `judge.SplitCard{SessionID, MachineID, Worktree, CCSessionID string}` · `judge.SplitReport{CCSessionID, MachineID, Root string; Recorded, SessionIDs []string}` · `func judge.DetectUnnormalizedSplit(cards []SplitCard, worktreeRoots []string) (reports []SplitReport, unattributed int)` · `func judge.SameConversation(a, b string) bool`

**이 과제가 판정하는 것 — 조상 관계가 아니라 "같은 트리에 값이 여럿"이다**

정규화가 도는 클라이언트는 `worktree` 를 언제나 **그 트리의 git 루트**로 적는다
(`cmd/fd/env.go` `resolveProject` 의 `--show-toplevel`). 따라서 한 대화가 **같은 트리**에
대해 서로 다른 값을 둘 이상 기록했다면, 그 카드 중 최소 하나는 정규화 없이 열린 것이다.

**앞선 두 판이 실측에서 무너졌다. 그 실측이 이 설계의 근거다.**

| 판정 규칙 | 실측 결과 |
|---|---|
| ① 조상-자손 경로 쌍 | 조상-자손 쌍 100건 중 **56건(56%)이 거짓 양성** |
| ② `git worktree list` 의 **살아 있는** 루트로 소유 트리 판정 | 보고 31건 중 **26건(84%)이 거짓 양성** |
| ③ 살아 있는 루트 **∪ 관례로 복원한 루트** | 보고 36건 중 **거짓 양성 0건** |

①이 무너진 이유: 이 저장소의 링크 워크트리는 `<repo>/.flightdeck/worktrees/X` 즉
저장소 루트의 **자손 경로**에 살고, 그것은 정규화가 완벽히 도는 클라이언트도 만드는
정당한 모양이다. 경로 모양만으로는 못 가른다.

②가 무너진 이유: 원장의 링크-워크트리 경로 93개 중 **81개가 이미 지워진 워크트리**다
(랜딩 뒤 정리한다). `git worktree list` 는 살아 있는 것만 아므로 지워진 트리의 카드가
저장소 루트로 흡수되고, ①의 거짓 양성이 그대로 재생산된다.

③이 쓰는 관례 복원은 **추측이 아니다.** flightdeck 자신이 그 자리에 워크트리를 만든다 —
`pick` 응답의 "워크트리 준비" 절이 `git worktree add '.flightdeck/worktrees/<항목id>'` 를
출력한다. `.claude/worktrees/<이름>` 도 같은 부류(하네스가 만드는 자리)다. 즉 이것은
**이 제품이 스스로 지키는 불변식**이고, 그래서 경로에서 되읽을 수 있다.

---

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/judge/split_test.go` 를 만든다.

```go
package judge

import (
	"reflect"
	"testing"
)

// 이 저장소의 실제 배치를 그대로 쓴다 — 링크 워크트리가 저장소 루트 **아래** 산다.
var testRoots = []string{
	"/repo",
	"/repo/.flightdeck/worktrees/A",
	"/repo/.flightdeck/worktrees/B",
}

func TestDetectUnnormalizedSplit(t *testing.T) {
	const (
		m  = "machine-1"
		cc = "cc-aaa"
	)
	cases := []struct {
		name  string
		cards []SplitCard
		roots []string
		want  int
		why   string
	}{
		{
			name: "같은 트리에 값이 둘이면 보고한다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/repo", CCSessionID: cc},
				{SessionID: "s2", MachineID: m, Worktree: "/repo/plugins/flightdeck/server", CCSessionID: cc},
			},
			roots: testRoots,
			want:  1,
			why:   "정규화가 돌았다면 둘 다 /repo 로 적혔을 것이다",
		},
		{
			name: "저장소 루트와 링크 워크트리 루트는 보고하지 않는다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/repo", CCSessionID: cc},
				{SessionID: "s2", MachineID: m, Worktree: "/repo/.flightdeck/worktrees/A", CCSessionID: cc},
			},
			roots: testRoots,
			want:  0,
			why:   "둘 다 자기 트리의 루트다 — 정규화가 도는 클라이언트가 만드는 정당한 모양이고, 실측 거짓 양성 56%의 원인이었다",
		},
		{
			name: "지워진 워크트리도 관례로 복원해 보고하지 않는다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/repo", CCSessionID: cc},
				{SessionID: "s2", MachineID: m, Worktree: "/repo/.flightdeck/worktrees/GONE", CCSessionID: cc},
			},
			roots: []string{"/repo"}, // GONE 은 git 이 모른다 — 이미 지워졌다
			want:  0,
			why:   "원장의 링크-워크트리 경로 93개 중 81개가 이미 지워진 것이다. 살아 있는 루트만 보면 거짓 양성 84%가 난다",
		},
		{
			name: "지워진 워크트리 안의 하위 디렉토리는 그 트리로 보고한다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/repo/.flightdeck/worktrees/GONE", CCSessionID: cc},
				{SessionID: "s2", MachineID: m, Worktree: "/repo/.flightdeck/worktrees/GONE/plugins/flightdeck/server", CCSessionID: cc},
			},
			roots: []string{"/repo"},
			want:  1,
			why:   "복원된 트리 안에서 값이 둘이면 그것은 진짜 갈림이다 — 복원이 거짓 음성을 만들면 안 된다",
		},
		{
			name: "링크 워크트리 안의 하위 디렉토리는 보고한다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/repo/.flightdeck/worktrees/A", CCSessionID: cc},
				{SessionID: "s2", MachineID: m, Worktree: "/repo/.flightdeck/worktrees/A/plugins/flightdeck/server", CCSessionID: cc},
			},
			roots: testRoots,
			want:  1,
			why:   "소유 트리가 A 로 같은데 값이 둘이다 — 소유 루트를 가장 긴 것으로 골라야 여기가 /repo 로 흡수되지 않는다",
		},
		{
			name: "형제 워크트리 둘은 보고하지 않는다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/repo/.flightdeck/worktrees/A", CCSessionID: cc},
				{SessionID: "s2", MachineID: m, Worktree: "/repo/.flightdeck/worktrees/B", CCSessionID: cc},
			},
			roots: testRoots,
			want:  0,
			why:   "서로 다른 git 워크트리다 — 같은 repo-상대 경로를 만지면 병합 때 실제로 충돌한다",
		},
		{
			name: "한 대화가 트리 둘에서 각각 안 접혔으면 보고 둘",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/repo", CCSessionID: cc},
				{SessionID: "s2", MachineID: m, Worktree: "/repo/plugins", CCSessionID: cc},
				{SessionID: "s3", MachineID: m, Worktree: "/repo/.flightdeck/worktrees/A", CCSessionID: cc},
				{SessionID: "s4", MachineID: m, Worktree: "/repo/.flightdeck/worktrees/A/plugins", CCSessionID: cc},
			},
			roots: testRoots,
			want:  2,
			why:   "트리마다 따로 보고한다 — 대표 하나만 내면 나머지가 조용히 사라진다",
		},
		{
			name: "cc 가 다르면 보고하지 않는다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/repo", CCSessionID: "cc-aaa"},
				{SessionID: "s2", MachineID: m, Worktree: "/repo/plugins", CCSessionID: "cc-bbb"},
			},
			roots: testRoots,
			want:  0,
			why:   "다른 대화가 한 트리 안에서 일하는 것은 이 제품의 정상 흐름이다",
		},
		{
			name: "빈 cc 끼리는 보고하지 않는다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/repo", CCSessionID: ""},
				{SessionID: "s2", MachineID: m, Worktree: "/repo/plugins", CCSessionID: ""},
			},
			roots: testRoots,
			want:  0,
			why:   "못 읽음을 값으로 접으면 관측이 깨진 순간 이 축이 거짓 초록을 낸다",
		},
		{
			name: "머신이 다르면 보고하지 않는다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: "machine-1", Worktree: "/repo", CCSessionID: cc},
				{SessionID: "s2", MachineID: "machine-2", Worktree: "/repo/plugins", CCSessionID: cc},
			},
			roots: testRoots,
			want:  0,
			why:   "다른 머신의 같은 경로는 같은 트리가 아니다",
		},
		{
			name: "루트를 모르면 아무것도 보고하지 않는다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/repo", CCSessionID: cc},
				{SessionID: "s2", MachineID: m, Worktree: "/repo/plugins", CCSessionID: cc},
			},
			roots: nil,
			want:  0,
			why:   "git 을 못 읽었을 때 추측으로 보고하면 실측 기준 거짓 양성이 56%다",
		},
		{
			name: "소유 트리를 못 찾은 경로는 건너뛴다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/elsewhere", CCSessionID: cc},
				{SessionID: "s2", MachineID: m, Worktree: "/elsewhere/sub", CCSessionID: cc},
			},
			roots: testRoots,
			want:  0,
			why:   "git 이 모르는 트리에 대고 '접혔어야 한다'를 판정할 근거가 없다",
		},
		{
			// ★ 이 케이스의 roots 를 줄이지 마라. testRoots 를 쓰면 /repo-backup 이 자기
			//   자신에 매칭돼 그룹이 갈리고, 그러면 isDescendant 를 HasPrefix 로 바꿔도
			//   초록이 나온다(실제로 그렇게 죽어 있었다).
			name: "경로 성분 경계를 지킨다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/repo", CCSessionID: cc},
				{SessionID: "s2", MachineID: m, Worktree: "/repo-backup/sub", CCSessionID: cc},
			},
			roots: []string{"/repo"},
			want:  0,
			why:   "/repo 는 /repo-backup/sub 의 조상이 아니다 — 문자열 접두로 보면 둘이 한 트리로 묶인다",
		},
		{
			name: "같은 트리를 여러 표기로 적어도 한 값으로 본다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/repo", CCSessionID: cc},
				{SessionID: "s2", MachineID: m, Worktree: "/repo/", CCSessionID: cc},
				{SessionID: "s3", MachineID: m, Worktree: "/repo/sub/..", CCSessionID: cc},
			},
			roots: []string{" /repo "},
			want:  0,
			why:   "Clean·TrimSpace 가 없으면 표기 차이가 갈림으로 보고된다 — 없는 문제에 배너가 켜진다",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := DetectUnnormalizedSplit(c.cards, c.roots)
			if len(got) != c.want {
				t.Fatalf("보고 %d건, 원하는 것 %d건 — %s\n입력: %+v\n결과: %+v",
					len(got), c.want, c.why, c.cards, got)
			}
		})
	}
}

// 대조 단정 — 함수가 항상 빈 결과를 내도 위 표의 다수가 통과한다(want 0 이 많다).
// 이 시험이 보고의 **내용**까지 잠근다.
func TestDetectUnnormalizedSplitReportsDetail(t *testing.T) {
	got, _ := DetectUnnormalizedSplit([]SplitCard{
		{SessionID: "s1", MachineID: "m", Worktree: "/repo", CCSessionID: "cc"},
		{SessionID: "s2", MachineID: "m", Worktree: "/repo/a", CCSessionID: "cc"},
		{SessionID: "s3", MachineID: "m", Worktree: "/repo/a/b", CCSessionID: "cc"},
	}, testRoots)
	if len(got) != 1 {
		t.Fatalf("보고 %d건, 원하는 것 1건: %+v", len(got), got)
	}
	r := got[0]
	if r.Root != "/repo" {
		t.Errorf("트리 %q, 원하는 것 %q", r.Root, "/repo")
	}
	if want := []string{"/repo", "/repo/a", "/repo/a/b"}; !reflect.DeepEqual(r.Recorded, want) {
		t.Errorf("기록된 값 %v, 원하는 것 %v", r.Recorded, want)
	}
	if want := []string{"s1", "s2", "s3"}; !reflect.DeepEqual(r.SessionIDs, want) {
		t.Errorf("카드 %v, 원하는 것 %v", r.SessionIDs, want)
	}
	if r.CCSessionID != "cc" {
		t.Errorf("cc %q, 원하는 것 %q", r.CCSessionID, "cc")
	}
}

// 소유 루트는 **가장 긴** 것이어야 한다. 가장 짧은 것을 고르면 링크 워크트리가
// 통째로 저장소 루트에 흡수되고, 그 순간 거짓 양성 56%가 돌아온다.
func TestOwningRootPicksTheLongestMatch(t *testing.T) {
	got, _ := DetectUnnormalizedSplit([]SplitCard{
		{SessionID: "s1", MachineID: "m", Worktree: "/repo", CCSessionID: "cc"},
		{SessionID: "s2", MachineID: "m", Worktree: "/repo/.flightdeck/worktrees/A", CCSessionID: "cc"},
		{SessionID: "s3", MachineID: "m", Worktree: "/repo/.flightdeck/worktrees/B", CCSessionID: "cc"},
	}, testRoots)
	if len(got) != 0 {
		t.Fatalf("보고 %d건, 원하는 것 0건 — 셋 다 자기 트리의 루트다: %+v", len(got), got)
	}
}

// 버려진 카드는 **세어서 낸다.** 침묵하면 "갈림 없음"과 "그 트리를 못 알아봤다"가
// 화면에서 같아진다 — 이 저장소가 반복해서 겪은 실패 모양이다.
func TestDetectUnnormalizedSplitCountsUnattributed(t *testing.T) {
	_, n := DetectUnnormalizedSplit([]SplitCard{
		{SessionID: "s1", MachineID: "m", Worktree: "/repo", CCSessionID: "cc"},
		{SessionID: "s2", MachineID: "m", Worktree: "/elsewhere", CCSessionID: "cc"},
		{SessionID: "s3", MachineID: "m", Worktree: "/nowhere/deep", CCSessionID: "cc"},
	}, []string{"/repo"})
	if n != 2 {
		t.Fatalf("버린 카드 %d장, 원하는 것 2장 — 어느 트리에도 안 붙은 카드를 세지 않으면 침묵이 된다", n)
	}
}

// 정렬이 결정적인가 — 같은 cc 가 머신 둘에서 보고를 만들 때가 유일한 흔들림 자리다.
// 맵 순회 순서가 결과로 새면 같은 입력이 매번 다른 문서를 낸다.
func TestDetectUnnormalizedSplitIsDeterministic(t *testing.T) {
	in := []SplitCard{
		{SessionID: "s1", MachineID: "machine-1", Worktree: "/repo", CCSessionID: "cc"},
		{SessionID: "s2", MachineID: "machine-1", Worktree: "/repo/sub", CCSessionID: "cc"},
		{SessionID: "s3", MachineID: "machine-2", Worktree: "/repo", CCSessionID: "cc"},
		{SessionID: "s4", MachineID: "machine-2", Worktree: "/repo/sub", CCSessionID: "cc"},
	}
	want, _ := DetectUnnormalizedSplit(in, testRoots)
	if len(want) != 2 {
		t.Fatalf("보고 %d건, 원하는 것 2건: %+v", len(want), want)
	}
	for i := 0; i < 200; i++ {
		got, _ := DetectUnnormalizedSplit(in, testRoots)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%d회차에서 결과가 흔들렸다:\n%+v\nvs\n%+v", i, got, want)
		}
	}
}

func TestSameConversation(t *testing.T) {
	if !SameConversation("cc-a", "cc-a") {
		t.Error("같은 cc 는 같은 대화다")
	}
	if SameConversation("cc-a", "cc-b") {
		t.Error("다른 cc 는 다른 대화다")
	}
	if SameConversation("", "") {
		t.Error("빈 cc 끼리는 같지 않다 — 못 읽음을 값으로 접으면 안 된다")
	}
}
```

- [ ] **Step 2: 실패를 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/judge/ -run 'TestDetectUnnormalizedSplit|TestOwningRoot|TestSameConversation' -v
```

기대: 컴파일 실패 — `undefined: SplitCard` · `undefined: DetectUnnormalizedSplit` · `undefined: SameConversation`.

- [ ] **Step 3: 최소 구현을 쓴다**

`internal/judge/split.go` 를 만든다.

```go
package judge

import (
	"path/filepath"
	"sort"
	"strings"
)

// SameConversation 은 sameConversation 의 공개 이름이다.
//
// 로직은 prescribe.go 의 그것 하나뿐이다 — 여기는 껍데기다. service 가 cc 를 직접
// 비교하면 같은 판정이 두 자리에 살고, 그 어긋남은 어느 화면에도 안 뜬다.
// 껍데기를 이 파일에 두는 이유는 prescribe.go 를 안 열기 위해서다(남의 자리에 가깝다).
func SameConversation(a, b string) bool { return sameConversation(a, b) }

// SplitCard 는 갈림 탐지에 필요한 카드 한 장의 좌표다.
//
// LiveSession 을 재사용하지 않는다 — 그 구조체에는 머신도 워크트리도 없고,
// 겹침·처방 두 축이 이미 그것을 쓰고 있어 성분을 더하면 두 축의 입력이 함께 바뀐다.
type SplitCard struct {
	SessionID   string
	MachineID   string
	Worktree    string
	CCSessionID string
}

// SplitReport 는 한 대화가 **같은 트리**에 대해 서로 다른 worktree 값을 기록했다는 보고다.
type SplitReport struct {
	CCSessionID string
	MachineID   string
	Root        string   // 이 보고가 걸린 워크트리 루트
	Recorded    []string // 그 트리에 대해 기록된 서로 다른 값들. 정렬된다. 언제나 2개 이상
	SessionIDs  []string // 이 보고에 걸린 카드 전부. 정렬된다
}

// DetectUnnormalizedSplit 은 워크트리 정규화가 안 돈 흔적을 찾는다. 순수 함수다.
//
// 판정은 하나다 — **같은 (머신, cc, 소유 트리)인데 기록된 worktree 값이 둘 이상.**
// 정규화가 도는 클라이언트는 언제나 그 트리의 git 루트를 적으므로(cmd/fd/env.go
// resolveProject 의 --show-toplevel) 값이 여럿이면 최소 하나는 정규화 없이 열린 것이다.
//
// 둘째 반환값은 **어느 트리에도 못 붙인 카드 수**다. 침묵하면 "갈림 없음"과
// "그 트리를 못 알아봤다"가 화면에서 같아진다 — 호출부가 이 수를 반드시 낸다.
//
// ★★ 판정 규칙이 두 번 무너졌고, 그 실측이 이 설계의 근거다.
//
//	① 조상-자손 경로 쌍            → 조상-자손 쌍 100건 중 56건(56%)이 거짓 양성
//	② 살아 있는 git 워크트리 루트   → 보고 31건 중 26건(84%)이 거짓 양성
//	③ 살아 있는 루트 ∪ 관례 복원    → 보고 36건 중 거짓 양성 0건
//
// ①: 링크 워크트리가 `<repo>/.flightdeck/worktrees/X` 즉 저장소 루트의 자손 경로에
// 살아서, 정규화가 완벽히 도는 클라이언트도 조상-자손 쌍을 만든다.
//
// ②: 원장의 링크-워크트리 경로 93개 중 81개가 **이미 지워진** 워크트리다(랜딩 뒤
// 정리한다). git 은 살아 있는 것만 아므로 지워진 트리의 카드가 저장소 루트로 흡수돼
// ①의 거짓 양성이 그대로 재생산된다.
//
// ★ 울타리:
//   - 이것은 정체 판정도 겹침 판정도 **아니다. 보고다.** 어느 소비자도 이 결과로 두
//     카드를 같은 세션이라고 보지 않는다. 카드는 여전히 3중키로만 같다.
//   - **CCSessionID 가 같을 때만 본다.** 앞선 세션이 상하위 17건을 가짜 겹침으로 셌다가
//     "전부 다른 대화였다"로 정정한 사고가 있다. 그 17건은 cc 가 달라 여기 안 걸린다.
//   - **형제 트리는 안 건드린다.** 소유 루트가 다르면 아예 다른 묶음이 된다.
//   - **빈 cc 끼리는 같다고 보지 않는다.**
//   - **알려진 루트가 하나도 없으면 아무것도 보고하지 않는다**(카드 전부가 둘째
//     반환값으로 나간다).
func DetectUnnormalizedSplit(cards []SplitCard, worktreeRoots []string) ([]SplitReport, int) {
	seen := map[string]bool{}
	var roots []string
	add := func(p string) {
		if p = strings.TrimSpace(p); p == "" {
			return
		}
		p = filepath.Clean(p)
		if !seen[p] {
			seen[p] = true
			roots = append(roots, p)
		}
	}
	for _, r := range worktreeRoots {
		add(r)
	}
	// 관례로 알려진 루트를 카드 경로에서 되읽는다 — 지워진 워크트리를 덮는다.
	for _, c := range cards {
		if r := conventionRoot(c.Worktree); r != "" {
			add(r)
		}
	}

	type key struct{ machine, cc, root string }
	groups := map[key]map[string][]string{} // (머신,cc,트리) → worktree 값 → 세션 id 들
	unattributed := 0
	for _, c := range cards {
		cc := strings.TrimSpace(c.CCSessionID)
		m := strings.TrimSpace(c.MachineID)
		wt := strings.TrimSpace(c.Worktree)
		if cc == "" || m == "" || wt == "" {
			continue // 3중키가 안 서는 카드다 — 판정 대상이 아니고 '버렸다'고 셀 것도 아니다
		}
		wt = filepath.Clean(wt)
		if wt == "." {
			continue
		}
		root := owningRoot(wt, roots)
		if root == "" {
			unattributed++ // git 도 관례도 모르는 트리다. **세어서 낸다**
			continue
		}
		k := key{m, cc, root}
		if groups[k] == nil {
			groups[k] = map[string][]string{}
		}
		groups[k][wt] = append(groups[k][wt], c.SessionID)
	}

	var out []SplitReport
	for k, byWT := range groups {
		if len(byWT) < 2 {
			continue // 한 트리에 값 하나 — 정규화가 돈 모양이다
		}
		rec := make([]string, 0, len(byWT))
		var ids []string
		for wt, sids := range byWT {
			rec = append(rec, wt)
			ids = append(ids, sids...)
		}
		sort.Strings(rec)
		sort.Strings(ids)
		out = append(out, SplitReport{
			CCSessionID: k.cc, MachineID: k.machine,
			Root: k.root, Recorded: rec, SessionIDs: ids,
		})
	}
	// ★ 정렬 키가 셋이어야 결정적이다. cc 하나만 쓰면 같은 cc 가 서로 다른 머신
	//   둘에서 보고를 만들 때 두 보고의 상대 순서가 맵 순회에 따라 흔들린다 —
	//   그리고 "같은 cc 가 여러 머신에 걸친다"는 이 축이 감지하려는 모양 중 하나다.
	sort.Slice(out, func(i, j int) bool {
		if out[i].CCSessionID != out[j].CCSessionID {
			return out[i].CCSessionID < out[j].CCSessionID
		}
		if out[i].MachineID != out[j].MachineID {
			return out[i].MachineID < out[j].MachineID
		}
		return out[i].Root < out[j].Root
	})
	return out, unattributed
}

// conventionRoot 는 경로 안에서 **관례로 알려진** 워크트리 루트를 되읽는다. 순수 함수다.
//
// ★ 추측이 아니다. flightdeck 자신이 그 자리에 워크트리를 만든다 — `pick` 응답의
// "워크트리 준비" 절이 `git worktree add '.flightdeck/worktrees/<항목id>'` 를 출력한다.
// `.claude/worktrees/<이름>` 도 같은 부류(하네스가 만드는 자리)다. 이 제품이 스스로
// 지키는 불변식이라 경로에서 되읽을 수 있다.
//
// ★ 이것이 없으면 **지워진 워크트리**의 카드가 저장소 루트로 흡수된다. 실측에서
// 원장의 링크-워크트리 경로 93개 중 81개가 이미 지워진 것이었고, 그 상태로는
// 보고의 84%가 거짓 양성이었다.
//
// 못 찾으면 빈 문자열이다.
func conventionRoot(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	segs := strings.Split(filepath.ToSlash(filepath.Clean(p)), "/")
	for i := 0; i+2 < len(segs); i++ {
		if segs[i] != ".flightdeck" && segs[i] != ".claude" {
			continue
		}
		if segs[i+1] != "worktrees" || segs[i+2] == "" {
			continue
		}
		return filepath.FromSlash(strings.Join(segs[:i+3], "/"))
	}
	return ""
}

// owningRoot 는 이 경로를 소유한 워크트리 루트다 — 조상-또는-자기인 루트 중 **가장 긴** 것.
//
// ★ 가장 긴 것을 골라야 한다. 가장 짧은 것을 고르면 `<repo>/.flightdeck/worktrees/X` 가
// 통째로 저장소 루트에 흡수되고, 그 순간 거짓 양성 56%가 돌아온다.
func owningRoot(p string, roots []string) string {
	best := ""
	for _, r := range roots {
		if p == r || isDescendant(r, p) {
			if len(r) > len(best) {
				best = r
			}
		}
	}
	return best
}

// isDescendant 는 child 가 parent **아래**인지다. 순수 함수다.
//
// ★ 문자열 접두가 아니라 **경로 성분 경계**로 본다. 접두로 보면 `/repo` 가
// `/repo-backup` 의 조상이 되고, 그것은 서로 무관한 두 저장소다.
func isDescendant(parent, child string) bool {
	if parent == child {
		return false
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
```

- [ ] **Step 4: 초록을 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/judge/ -v
```

기대: 새 시험 전부 PASS, **기존 judge 시험도 전부 PASS**.

- [ ] **Step 5: 수정을 되돌려 빨강을 확인한다 — 다섯**

이 과제의 품질은 이 단계가 정한다. 다섯 다 **실제 실패 메시지를 눈으로 보고** 보고서에 적어라.
빨강이 안 나면 그 시험이 아무것도 안 잡는 것이다 — 시험을 고쳐라(구현을 고치지 마라).

| # | 되돌릴 것 | 빨강이어야 하는 시험 |
|---|---|---|
| 1 | `owningRoot` 가 가장 **짧은** 루트를 고르게(`len(r) > len(best)` → `best == ""`) | `저장소_루트와_링크_워크트리_루트는_보고하지_않는다` · `TestOwningRootPicksTheLongestMatch` |
| 2 | `conventionRoot` 가 언제나 `""` 를 내게 | `지워진_워크트리도_관례로_복원해_보고하지_않는다` |
| 3 | `isDescendant` 를 `strings.HasPrefix(child, parent)` 로 | `경로_성분_경계를_지킨다` |
| 4 | `unattributed++` 를 지우고 0을 내게 | `TestDetectUnnormalizedSplitCountsUnattributed` |
| 5 | 정렬 키를 `CCSessionID` 하나로 | `TestDetectUnnormalizedSplitIsDeterministic` |

**`if len(roots) == 0 { return nil }` 류의 조기 반환은 이 판에 없다.** 앞선 판에서 그것이
행동상 죽은 코드였고(어떤 입력을 넣어도 결과가 같았다) 존재할 수 없는 빨강을 약속했다.
지금은 `루트를_모르면_아무것도_보고하지_않는다` 가 그 계약을 행동으로 잠근다.

다섯 다 확인했으면 되돌린다.

- [ ] **Step 6: 커밋**

```bash
cd plugins/flightdeck/server && gofmt -l . && go vet ./internal/judge/
git add internal/judge/split.go internal/judge/split_test.go
git commit -m "feat(flightdeck): 정규화가 안 돈 흔적을 원장만으로 탐지한다 — 지워진 워크트리를 관례로 복원한다"
```

---

### Task 2: `service` — 카드를 대화 단위로 접는다

**Files:**
- Modify: `plugins/flightdeck/server/internal/service/board.go` (`BoardView` 구조체 · `Board` 함수의 `view := BoardView{...}` 직후)
- Test: `plugins/flightdeck/server/internal/service/board_conversation_test.go`

**Interfaces:**
- Consumes: `judge.SameConversation(a, b string) bool` (Task 1)
- Produces: `service.Conversation{CCSessionID string; Cards []SessionCard; IsSelf bool; PathCount, Worktrees int}` · `service.BoardView.Conversations []Conversation` · `func service.FoldConversations(cards []SessionCard) []Conversation`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/service/board_conversation_test.go` 를 만든다.

```go
package service

import (
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

func card(id, cc, wt string, self bool, paths ...string) SessionCard {
	return SessionCard{
		View: model.SessionView{
			Session: model.Session{ID: id, CCSessionID: cc, Worktree: wt},
			Paths:   paths,
		},
		IsSelf: self,
	}
}

func TestFoldConversations(t *testing.T) {
	got := FoldConversations([]SessionCard{
		card("s1", "cc-a", "/repo", false, "a.go", "b.go"),
		card("s2", "cc-a", "/repo/sub", false, "b.go", "c.go"),
		card("s3", "cc-b", "/other", false, "d.go"),
	})
	if len(got) != 2 {
		t.Fatalf("묶음 %d개, 원하는 것 2개: %+v", len(got), got)
	}
	var a *Conversation
	for i := range got {
		if got[i].CCSessionID == "cc-a" {
			a = &got[i]
		}
	}
	if a == nil {
		t.Fatal("cc-a 묶음이 없다")
	}
	if len(a.Cards) != 2 {
		t.Errorf("cc-a 카드 %d장, 원하는 것 2장", len(a.Cards))
	}
	if a.Worktrees != 2 {
		t.Errorf("cc-a 워크트리 %d개, 원하는 것 2개", a.Worktrees)
	}
	// 합집합 건수다 — b.go 가 둘 다에 있으므로 4가 아니라 3이다.
	if a.PathCount != 3 {
		t.Errorf("cc-a 경로 %d개, 원하는 것 3개(합집합)", a.PathCount)
	}
}

func TestFoldConversationsNeverFoldsEmptyCC(t *testing.T) {
	got := FoldConversations([]SessionCard{
		card("s1", "", "/repo", false),
		card("s2", "", "/other", false),
	})
	if len(got) != 2 {
		t.Fatalf("묶음 %d개, 원하는 것 2개 — 빈 cc 는 절대 안 접는다: %+v", len(got), got)
	}
}

func TestFoldConversationsLiftsIsSelf(t *testing.T) {
	got := FoldConversations([]SessionCard{
		card("s1", "cc-a", "/repo", false),
		card("s2", "cc-a", "/repo/sub", true),
	})
	if len(got) != 1 {
		t.Fatalf("묶음 %d개, 원하는 것 1개", len(got))
	}
	if !got[0].IsSelf {
		t.Error("형제 중 하나라도 나면 묶음이 * 를 받아야 한다")
	}
}

// 대조 단정 — Sessions 를 안 건드리는 것이 이 설계의 계약이다.
// dashboard.json 소비자(이 항목의 재측정 명령을 포함)가 그것에 기대고 있다.
func TestFoldDoesNotMutateCards(t *testing.T) {
	in := []SessionCard{
		card("s1", "cc-a", "/repo", false, "a.go"),
		card("s2", "cc-a", "/repo/sub", false, "b.go"),
	}
	before := len(in)
	_ = FoldConversations(in)
	if len(in) != before {
		t.Fatalf("입력이 %d장에서 %d장으로 바뀌었다", before, len(in))
	}
	if in[0].View.Session.ID != "s1" || in[1].View.Session.ID != "s2" {
		t.Fatal("입력 카드의 순서·내용이 바뀌었다")
	}
}
```

- [ ] **Step 2: 실패를 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/service/ -run TestFold -v
```

기대: 컴파일 실패 — `undefined: FoldConversations` · `undefined: Conversation`.

- [ ] **Step 3: 최소 구현을 쓴다**

`internal/service/board.go` 의 `SessionCard` 정의 **바로 아래**에 더한다.

```go
// Conversation 은 같은 대화(cc)의 카드 묶음이다.
//
// ★ 원장을 안 건드린다 — **표시 계층만**이다. 카드는 여전히 3중키로만 같고, 이 묶음은
// 보드가 "지금 몇 개가 동시에 돌고 있나"에 참말을 하기 위한 파생일 뿐이다.
type Conversation struct {
	CCSessionID string        `json:"cc_session_id,omitempty"`
	Cards       []SessionCard `json:"cards"`
	IsSelf      bool          `json:"is_self"`
	// PathCount 는 합집합 **건수**다. 목록은 만들지 않는다 — 합쳐서 내면
	// "이 대화가 만지는 자리"가 실제보다 넓어 보이고, 그러면 겹침 축을 읽는
	// 사람이 없는 다툼을 본다.
	PathCount int `json:"path_count"`
	Worktrees int `json:"worktrees"`
}

// FoldConversations 는 카드를 대화 단위로 접는다. 순수 함수다.
//
// ★ 접는 기준은 judge.SameConversation(cc 동등)이다 — 겹침·처방이 이미 쓰는 그 판정의
// 세 번째 소비자가 되는 것이지 새 판정이 아니다. 빈 cc 는 접지 않는다(카드 1장짜리
// 묶음이 된다) — 그 규칙이 judge.LiveSession.CCSessionID 주석에 못박혀 있다.
//
// ★ 입력을 변형하지 않는다. BoardView.Sessions 는 그대로 나가야 한다 —
// dashboard.json 계약이 깨지면 그것으로 실측하는 스크립트가 전부 깨진다.
func FoldConversations(cards []SessionCard) []Conversation {
	out := make([]Conversation, 0, len(cards))
	idx := map[string]int{} // cc → out 의 자리. 빈 cc 는 안 담는다

	for _, c := range cards {
		cc := c.View.Session.CCSessionID
		if i, ok := idx[cc]; ok {
			out[i].Cards = append(out[i].Cards, c)
			out[i].IsSelf = out[i].IsSelf || c.IsSelf
			continue
		}
		out = append(out, Conversation{CCSessionID: cc, Cards: []SessionCard{c}, IsSelf: c.IsSelf})
		// ★ 색인에 담을지를 judge 가 정한다. SameConversation(cc, cc) 는 cc 가 비면
		//   false 라서, 빈 cc 는 색인에 안 들어가고 따라서 **다음 빈 cc 카드와 절대
		//   안 묶인다.** "빈 값끼리는 같지 않다"는 판정을 여기서 다시 쓰지 않고
		//   그 함수 하나에 남겨 두기 위한 형태다 — cc != "" 로 적으면 같은 판정이
		//   두 자리에 살고, 한쪽만 고치는 순간 조용히 어긋난다.
		if judge.SameConversation(cc, cc) {
			idx[cc] = len(out) - 1
		}
	}

	for i := range out {
		seenWT := map[string]bool{}
		seenPath := map[string]bool{}
		for _, c := range out[i].Cards {
			seenWT[c.View.Session.Worktree] = true
			for _, p := range c.View.Paths {
				seenPath[p] = true
			}
		}
		out[i].Worktrees = len(seenWT)
		out[i].PathCount = len(seenPath)
	}
	return out
}
```

`BoardView` 구조체에 필드를 더한다 — `Sessions` **바로 아래**.

```go
	// Conversations 는 Sessions 를 대화 단위로 접은 것이다. Sessions 는 그대로 둔다 —
	// 소비자가 셋(MCP 보드·dashboard.json·웹)이고 각자 속도로 옮긴다.
	Conversations []Conversation `json:"conversations,omitempty"`
```

`Board` 함수의 `view := BoardView{Project: proj, At: now, Window: window, Sessions: cards}` **바로 다음 줄**에 더한다.

```go
	view.Conversations = FoldConversations(cards)
```

- [ ] **Step 4: 초록을 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/service/ -run TestFold -v
```

기대: PASS 넷 전부.

- [ ] **Step 5: 되돌려 빨강을 확인한다**

`FoldConversations` 가 카드마다 묶음 하나를 내도록(접기 없이) 바꾸고 돌린다.
기대: `TestFoldConversations` 와 `TestFoldConversationsLiftsIsSelf` 가 FAIL. 되돌린다.

빈 cc 도 접도록(`idx[cc] = …` 를 무조건 실행) 바꾸고 돌린다.
기대: `TestFoldConversationsNeverFoldsEmptyCC` 가 FAIL. 되돌린다.

- [ ] **Step 6: 커밋**

```bash
cd plugins/flightdeck/server && gofmt -l . && go build ./... && go test ./internal/service/
git add internal/service/board.go internal/service/board_conversation_test.go
git commit -m "feat(flightdeck): 보드가 카드를 대화 단위로 접는다 — Sessions 계약은 안 깬다"
```

---

### Task 3: `service` — 갈림 탐지를 보드에 배선한다

**Files:**
- Modify: `plugins/flightdeck/server/internal/service/board.go` (`BoardView` · `Board`)
- Test: `plugins/flightdeck/server/internal/service/board_conversation_test.go` (Task 2 의 파일에 더한다)

**Interfaces:**
- Consumes: `judge.DetectUnnormalizedSplit(cards []judge.SplitCard, worktreeRoots []string) []judge.SplitReport` (Task 1) · `service.SessionCard` (기존) · `Service.worktreeIndex` (기존, `sessionCards` 안에서 이미 돈다)
- Produces: `service.BoardView.Splits []judge.SplitReport` · `func service.splitCardsOf(cards []SessionCard) []judge.SplitCard` · `sessionCards` 가 워크트리 루트 목록을 함께 돌려준다

**이 과제의 핵심은 루트 목록을 어디서 얻느냐다**

`DetectUnnormalizedSplit` 은 git 이 아는 워크트리 루트 목록이 있어야 돈다(없으면 `nil` 을 낸다).
그 목록은 `sessionCards` 안에서 이미 계산된다 — `wts, heads = s.worktreeIndex(ctx, g, d)` 의
`wts` 가 `워크트리 경로 → 브랜치` 맵이고, 그 **키가 곧 루트 목록**이다.

**`worktreeIndex` 를 다시 부르지 마라.** 그것은 `git worktree list` 한 번이고 이 서버에서
가장 비싼 일의 일부다(`sessionCards` 머리말이 그 비용을 세는 이유를 적어 뒀다). 대신
`sessionCards` 의 반환에 루트 목록을 더한다.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`board_conversation_test.go` 끝에 더한다.

```go
func TestSplitCardsOfCarriesTriple(t *testing.T) {
	in := []SessionCard{card("s1", "cc-a", "/repo", false)}
	in[0].View.Session.MachineID = "m1"
	got := splitCardsOf(in)
	if len(got) != 1 {
		t.Fatalf("%d건, 원하는 것 1건", len(got))
	}
	if got[0].SessionID != "s1" || got[0].MachineID != "m1" ||
		got[0].Worktree != "/repo" || got[0].CCSessionID != "cc-a" {
		t.Fatalf("3중키가 안 실렸다: %+v", got[0])
	}
}
```

- [ ] **Step 2: 실패를 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/service/ -run TestSplitCardsOf -v
```

기대: 컴파일 실패 — `undefined: splitCardsOf`.

- [ ] **Step 3: 최소 구현을 쓴다**

`internal/service/board.go` 의 `FoldConversations` 아래에 더한다.

```go
// splitCardsOf 는 카드에서 갈림 탐지 입력을 뽑는다.
//
// judge 가 SessionCard 를 직접 받지 않는 이유: 그러면 판정 계층이 표시 계층의
// 구조체에 묶이고, SessionCard 에 필드를 더할 때마다 judge 가 다시 컴파일된다.
func splitCardsOf(cards []SessionCard) []judge.SplitCard {
	out := make([]judge.SplitCard, 0, len(cards))
	for _, c := range cards {
		out = append(out, judge.SplitCard{
			SessionID:   c.View.Session.ID,
			MachineID:   c.View.Session.MachineID,
			Worktree:    c.View.Session.Worktree,
			CCSessionID: c.View.Session.CCSessionID,
		})
	}
	return out
}
```

`BoardView` 에 필드를 더한다 — `Conversations` 바로 아래.

```go
	// Splits 는 워크트리 정규화가 안 돈 흔적이다. **비어 있는 것이 정상**이고,
	// 하나라도 있으면 그 카드를 연 클라이언트가 4de4b21 이전 판이라는 뜻이다.
	//
	// ★ git 을 못 읽으면 이 축은 **판정 자체를 안 한다**(빈 슬라이스). 그 사실은
	//   Failures 에 남는다 — 침묵과 "갈림 없음"을 구분해야 한다.
	Splits []judge.SplitReport `json:"splits,omitempty"`
```

`sessionCards` 의 시그니처에 루트 목록을 더한다. 지금은

```go
func (s *Service) sessionCards(ctx context.Context, proj model.Project, cut time.Time, self string, d *derive) ([]SessionCard, error) {
```

이고, 이것을 다음으로 바꾼다.

```go
// 반환의 둘째 값은 **git 이 아는 워크트리 루트 목록**이다.
// 갈림 탐지가 그것 없이는 못 돌고(judge.DetectUnnormalizedSplit), 여기서 이미
// `git worktree list` 를 한 번 돌렸으므로 호출부가 다시 부르면 그 비용이 두 배가 된다.
// git 을 못 읽었으면 nil 이다 — 빈 것과 못 읽은 것을 호출부가 가를 수 있어야 한다.
func (s *Service) sessionCards(ctx context.Context, proj model.Project, cut time.Time, self string, d *derive) ([]SessionCard, []string, error) {
```

함수 안에서 `wts` 가 채워진 직후(앵커: `wts, heads = s.worktreeIndex(ctx, g, d)`) 루트를 모은다.

```go
		wts, heads = s.worktreeIndex(ctx, g, d)
		roots = make([]string, 0, len(wts))
		for wt := range wts {
			roots = append(roots, wt)
		}
		sort.Strings(roots) // 파생 결과가 맵 순회 순서에 안 새게
```

`roots` 는 함수 머리에서 `var roots []string` 으로 선언한다. 모든 `return` 을 세 값으로
고친다(오류 갈래는 `return nil, nil, err`).

`Board` 의 호출부를 고친다. 앵커: `cards, err := s.sessionCards(...)`.

```go
	cards, roots, err := s.sessionCards(ctx, proj, s.cut(now, window), opt.Self, d)
```

그리고 `view.Conversations = …` 바로 다음 줄에 더한다.

```go
	// ★ 침묵하지 않는다. 루트를 못 읽었거나 어느 트리에도 못 붙인 카드가 있으면
	//   그 사실을 파생 기록에 남긴다 — 안 남기면 "갈림 없음"과 "판정을 못 했다"가
	//   화면에서 같아진다.
	if len(roots) == 0 {
		d.note("split-detect", "워크트리 루트를 못 읽었다 — 갈림 탐지의 근거가 관례 복원뿐이다")
	}
	var unattributed int
	view.Splits, unattributed = judge.DetectUnnormalizedSplit(splitCardsOf(cards), roots)
	if unattributed > 0 {
		d.note("split-detect", fmt.Sprintf(
			"카드 %d장은 어느 워크트리에도 못 붙여 갈림 판정에서 빠졌다", unattributed))
	}
```

`d.note` 의 정확한 시그니처와 `fmt` import 유무는 같은 파일의 기존 호출부
(`d.note("project-path", …)`)를 보고 맞춘다.

`d.note` 의 정확한 시그니처는 같은 파일의 기존 호출부(`d.note("project-path", …)`)를 따른다.

- [ ] **Step 4: 초록을 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/service/ -run 'TestSplitCardsOf|TestFold' -v
```

기대: PASS.

- [ ] **Step 5: 되돌려 빨강을 확인한다**

`splitCardsOf` 에서 `MachineID` 를 안 싣도록 바꾸고 돌린다. 기대: FAIL. 되돌린다.

- [ ] **Step 6: 커밋**

```bash
cd plugins/flightdeck/server && gofmt -l . && go build ./... && go test ./internal/service/
git add internal/service/board.go internal/service/board_conversation_test.go
git commit -m "feat(flightdeck): 보드가 갈림 흔적을 함께 낸다 — 비어 있는 것이 정상이다"
```

---

### Task 4: `render` — 대화 수 머리줄 · 묶음 카드 · 갈림 배너

**Files:**
- Modify: `plugins/flightdeck/server/internal/mcpsrv/render.go` (`RenderBoard` 의 `head`·카드 루프 · `rankCards`)
- Test: `plugins/flightdeck/server/internal/mcpsrv/render_conversation_test.go`

**Interfaces:**
- Consumes: `service.BoardView.Conversations` · `service.BoardView.Splits` (Task 2·3)
- Produces: `func rankConversations(v service.BoardView, self string, now time.Time) []service.Conversation` · `func conversationCard(c service.Conversation, now time.Time, pathLimit int, detail bool, asks, blocked []model.Judgment) string` · `func splitBanner(reports []judge.SplitReport) string`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/mcpsrv/render_conversation_test.go` 를 만든다.

```go
package mcpsrv

import (
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

func convCard(id, cc, wt string, paths ...string) service.SessionCard {
	return service.SessionCard{
		View: model.SessionView{
			Session: model.Session{ID: id, CCSessionID: cc, Worktree: wt, State: "active"},
			Paths:   paths,
		},
	}
}

func TestRenderBoardHeadCountsConversations(t *testing.T) {
	now := time.Now()
	cards := []service.SessionCard{
		convCard("s1", "cc-a", "/repo", "a.go"),
		convCard("s2", "cc-a", "/repo/sub", "b.go"),
		convCard("s3", "cc-b", "/other", "c.go"),
	}
	v := service.BoardView{
		Project: model.Project{ID: "p"}, At: now, Window: 2 * time.Hour,
		Sessions: cards, Conversations: service.FoldConversations(cards),
	}
	out := RenderBoard(v, BoardRenderOptions{Now: now})
	if !strings.Contains(out, "대화 2개(카드 3장)") {
		t.Fatalf("머리줄이 대화 수를 안 낸다:\n%s", out)
	}
	if strings.Contains(out, "살아 있는 세션 3건") {
		t.Fatalf("옛 머리줄이 남아 있다 — 3.2배로 부풀린 그 수다:\n%s", out)
	}
}

func TestRenderBoardShowsSplitBanner(t *testing.T) {
	now := time.Now()
	v := service.BoardView{
		Project: model.Project{ID: "p"}, At: now, Window: 2 * time.Hour,
		Splits: []judge.SplitReport{{
			CCSessionID: "cc-a", MachineID: "m", Root: "/repo",
			Recorded: []string{"/repo", "/repo/sub"}, SessionIDs: []string{"s1", "s2"},
		}},
	}
	out := RenderBoard(v, BoardRenderOptions{Now: now})
	if !strings.Contains(out, "상하위 경로로 갈렸다") {
		t.Fatalf("갈림 배너가 없다:\n%s", out)
	}
	if !strings.Contains(out, "정규화") {
		t.Fatalf("배너가 원인을 안 말한다 — 증상만 내면 다음 사람이 또 조사한다:\n%s", out)
	}
}

// 대조 단정 — 갈림이 없으면 배너가 **없어야** 한다.
// 항상 찍으면 배너가 배경이 되고, 배경은 아무도 안 읽는다.
func TestRenderBoardSilentWhenNoSplit(t *testing.T) {
	now := time.Now()
	v := service.BoardView{Project: model.Project{ID: "p"}, At: now, Window: 2 * time.Hour}
	out := RenderBoard(v, BoardRenderOptions{Now: now})
	if strings.Contains(out, "상하위 경로로 갈렸다") {
		t.Fatalf("갈림이 없는데 배너가 떴다:\n%s", out)
	}
}

func TestConversationCardFoldsByDefaultAndExpandsInDetail(t *testing.T) {
	now := time.Now()
	cards := []service.SessionCard{
		convCard("s1", "cc-a", "/repo", "a.go"),
		convCard("s2", "cc-a", "/repo/sub", "b.go"),
	}
	v := service.BoardView{
		Project: model.Project{ID: "p"}, At: now, Window: 2 * time.Hour,
		Sessions: cards, Conversations: service.FoldConversations(cards),
	}
	brief := RenderBoard(v, BoardRenderOptions{Now: now})
	if strings.Contains(brief, "/repo/sub") {
		t.Fatalf("기본 보드가 워크트리를 전개했다 — 예산이 그만큼 준다:\n%s", brief)
	}
	if !strings.Contains(brief, "카드 2장") {
		t.Fatalf("기본 보드가 카드 수를 안 낸다:\n%s", brief)
	}
	detail := RenderBoard(v, BoardRenderOptions{Now: now, Detail: true})
	if !strings.Contains(detail, "/repo/sub") {
		t.Fatalf("detail 이 워크트리별로 안 전개했다:\n%s", detail)
	}
}
```

- [ ] **Step 2: 실패를 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/mcpsrv/ -run 'TestRenderBoardHead|TestRenderBoardShowsSplit|TestRenderBoardSilent|TestConversationCard' -v
```

기대: FAIL — 머리줄이 `살아 있는 세션 3건` 이고 배너가 없다.

- [ ] **Step 3: 구현한다**

`render.go` 의 `RenderBoard` 에서 머리줄을 바꾼다. 앵커는 `살아 있는 세션 %d건` 이다.

```go
	head = append(head,
		fmt.Sprintf("보드 · %s · %s · %s",
			v.Project.ID, v.At.UTC().Format("2006-01-02 15:04 UTC"), FormatFreshness(v.Derived)),
		// ★ 대화 수가 먼저다. 카드 수는 괄호 안이다 — 사람이 이 줄로 하는 판단은
		//   "지금 몇 개가 동시에 돌고 있나"이고, 그 답은 카드가 아니라 대화다.
		//   실측(2026-08-05): 카드 88장이 대화 23개였다 — 3.8배로 부풀린 수였다.
		fmt.Sprintf("대화 %d개(카드 %d장) (최근 %s 안에 신호가 있었다 — 생존 판정이 아니다)",
			len(v.Conversations), len(v.Sessions), FormatAge(v.Window)),
	)
	if b := splitBanner(v.Splits); b != "" {
		head = append(head, b)
	}
```

카드 루프를 묶음 단위로 바꾼다. 앵커는 `ranked := rankCards(v, opt.Self, now)` 다.

```go
	ranked := rankConversations(v, opt.Self, now)
	blocks := make([]string, 0, len(ranked))
	for _, c := range ranked {
		blocks = append(blocks, conversationCard(c, now, pathLimit, opt.Detail, v.Asks, v.Blocked))
	}
```

`rankCards` **아래**에 새 함수 셋을 더한다. `rankCards` 자체는 안 지운다 — 다른 호출부가 없더라도 이 변경으로 죽는 것을 이 과제에서 판단하지 않는다(Task 8 에서 `go vet` 과 함께 정리한다).

```go
// splitBanner 는 갈림 보고를 머리 한 줄로 낸다.
//
// ★ 없으면 **빈 문자열**이다. 항상 찍으면 배너가 배경이 되고 배경은 아무도 안 읽는다.
// ★ 카드 절이 아니라 머리에 두는 이유: 이것은 특정 카드의 성질이 아니라 이 관측
//   전체가 낡은 클라이언트에서 왔다는 사실이다.
func splitBanner(reports []judge.SplitReport) string {
	if len(reports) == 0 {
		return ""
	}
	// ★ len(reports) 를 세지 않는다. 보고는 **갈림 그룹** 단위이고 한 대화가 무관한
	//   그룹을 둘 이상 가질 수 있다 — 그대로 세면 대화 하나가 여러 개로 부풀어
	//   보이고, 그러면 이 배너가 고치려던 바로 그 부풀림을 스스로 저지른다.
	ccs := map[string]bool{}
	for _, r := range reports {
		ccs[r.CCSessionID] = true
	}
	return fmt.Sprintf(
		"⚠ 대화 %d개의 카드가 상하위 경로로 갈렸다 — 그 카드를 연 클라이언트에서 "+
			"워크트리 정규화(4de4b21)가 안 돈다. 정규화가 도는 판은 이 모양을 만들 수 없다.",
		len(ccs))
}

// rankConversations 는 묶음을 정렬한다.
//
// ★ 사건(ask·blocked)이 붙은 형제가 **하나라도** 있으면 묶음 전체가 그 등급을 받는다.
// 카드 단위로 보면 판단이 붙은 카드와 발자국이 있는 카드가 갈려 있을 때
// 묶음이 맨 아래로 떨어진다 — 그것이 이 항목이 고치려는 갈림 그 자체다.
func rankConversations(v service.BoardView, self string, now time.Time) []service.Conversation {
	hasNote := map[string]bool{}
	for _, j := range v.Asks {
		hasNote[j.SessionID] = true
	}
	for _, j := range v.Blocked {
		hasNote[j.SessionID] = true
	}

	var selfPaths []string
	for _, c := range v.Conversations {
		if c.IsSelf {
			for _, k := range c.Cards {
				selfPaths = append(selfPaths, k.View.Paths...)
			}
		}
	}

	rank := func(c service.Conversation) int {
		noted := false
		for _, k := range c.Cards {
			if k.View.Session.ID == self {
				return 0
			}
			if hasNote[k.View.Session.ID] {
				noted = true
			}
		}
		switch {
		case c.IsSelf:
			return 0
		case noted:
			return 1
		}
		if len(selfPaths) > 0 {
			for _, k := range c.Cards {
				if judge.PathsOverlap(selfPaths, k.View.Paths) {
					return 2
				}
			}
		}
		return 3
	}

	out := append([]service.Conversation{}, v.Conversations...)
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := rank(out[i]), rank(out[j])
		if ri != rj {
			return ri < rj
		}
		return lastSignalOfConversation(out[i], now).After(lastSignalOfConversation(out[j], now))
	})
	return out
}

// lastSignalOfConversation 은 묶음 안 카드들의 마지막 신호 중 가장 최근이다.
func lastSignalOfConversation(c service.Conversation, now time.Time) time.Time {
	var last time.Time
	for _, k := range c.Cards {
		if t := lastSignal(k, now); t.After(last) {
			last = t
		}
	}
	return last
}

// conversationCard 는 묶음 하나를 그린다.
//
// 기본은 요약 한 줄 묶음이고, detail 일 때만 워크트리별로 전개한다.
// 합집합 경로 **목록**은 어느 경우에도 안 낸다 — 대화가 만지는 자리가 실제보다
// 넓어 보이고, 그러면 겹침 축을 읽는 사람이 없는 다툼을 본다.
func conversationCard(c service.Conversation, now time.Time, pathLimit int, detail bool,
	asks, blocked []model.Judgment) string {
	if len(c.Cards) == 0 {
		return ""
	}
	// 카드가 한 장이면 접을 것이 없다 — 기존 카드 모양 그대로 낸다.
	if len(c.Cards) == 1 {
		return boardCard(c.Cards[0], now, pathLimit, detail, asks, blocked)
	}

	lead := c.Cards[0]
	var b strings.Builder
	mark := " "
	if c.IsSelf {
		mark = "*"
	}
	fmt.Fprintf(&b, "%s%s… · 대화 1개(카드 %d장 · 워크트리 %d개) · %s\n",
		mark, ShortID(lead.View.Session.ID), len(c.Cards), c.Worktrees, lead.View.Session.State)
	fmt.Fprintf(&b, "   경로 %d개(워크트리 %d개에 걸쳐)", c.PathCount, c.Worktrees)
	for _, k := range c.Cards {
		for _, cl := range k.View.Claims {
			fmt.Fprintf(&b, " | 선점 %s @ %s", cl, k.View.Session.Worktree)
		}
	}
	b.WriteString("\n")
	if detail {
		for _, k := range c.Cards {
			fmt.Fprintf(&b, "   ├ %s  경로 %d\n", k.View.Session.Worktree, len(k.View.Paths))
		}
	}
	for _, k := range c.Cards {
		for _, l := range noteLines(k.View.Session.ID, asks, blocked, now) {
			b.WriteString(l + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
```

import 에 `sort` 와 `judge` 가 이미 있는지 확인하고 없으면 더한다.

- [ ] **Step 4: 초록을 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/mcpsrv/ -v
```

기대: 새 시험 넷 PASS, **기존 시험도 전부 PASS**. 기존 시험이 머리줄 문구에 기대고 있으면 그 시험도 함께 고친다 — 문구가 바뀐 것이 의도이므로 시험을 맞추는 것이 옳다. 다만 **무엇을 왜 고쳤는지 커밋 메시지에 적는다.**

- [ ] **Step 5: 되돌려 빨강을 확인한다**

`splitBanner` 가 항상 빈 문자열을 내게 바꾸고 돌린다. 기대: `TestRenderBoardShowsSplitBanner` FAIL. 되돌린다.

`conversationCard` 의 `if detail` 을 `if true` 로 바꾸고 돌린다. 기대: `TestConversationCardFoldsByDefaultAndExpandsInDetail` FAIL. 되돌린다.

- [ ] **Step 6: 커밋**

```bash
cd plugins/flightdeck/server && gofmt -l . && go vet ./... && go test ./internal/mcpsrv/
git add internal/mcpsrv/render.go internal/mcpsrv/render_conversation_test.go
git commit -m "feat(flightdeck): 보드 머리줄이 대화 수를 낸다 + 갈림 배너 — 3.8배로 부풀던 수를 고친다"
```

---

### Task 5: ack 도달성 — 확인율의 분모를 가른다

**Files:**
- Create: `plugins/flightdeck/server/internal/store/prescribe_reach.go`
- Create: `plugins/flightdeck/server/internal/store/prescribe_reach_test.go`
- Modify: `plugins/flightdeck/server/internal/service/board.go` (`BoardView` · `Board`)
- Modify: `plugins/flightdeck/server/internal/mcpsrv/render.go` (`boardDetailFoot`)
- Modify: `plugins/flightdeck/DESIGN.md` (§10 만)

**Interfaces:**
- Consumes: 없음(store 는 DB 만 본다)
- Produces: `model.AckReach{Emitted, Reachable, Acked int}` 대신 **service 에 둔다**: `service.AckReach{Emitted, Reachable, Acked int}` · `func (s *Store) AckReach(ctx context.Context, project string) (emitted, reachable, acked int, err error)` · `service.BoardView.AckReach *AckReach`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/store/prescribe_reach_test.go` 를 만든다. 헬퍼는 `store_test.go:24` 의
`newStore(t *testing.T) *Store` 다(임시 디렉토리에 DB 를 열고 `t.Cleanup` 으로 닫는다).

```go
package store

import (
	"context"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

func TestAckReachSplitsDenominatorByJudgmentPresence(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	// 카드 셋: id1 은 판단이 있고 ack 도 했다 · id2 는 발화만 받고 판단이 0이다 ·
	// id3 은 발화 자체가 없다.
	s1, _, err := s.OpenSession(ctx, "p", "m", "/wt1", "cc-1", "")
	if err != nil {
		t.Fatalf("OpenSession wt1: %v", err)
	}
	s2, _, err := s.OpenSession(ctx, "p", "m", "/wt2", "cc-2", "")
	if err != nil {
		t.Fatalf("OpenSession wt2: %v", err)
	}
	if _, _, err := s.OpenSession(ctx, "p", "m", "/wt3", "cc-3", ""); err != nil {
		t.Fatalf("OpenSession wt3: %v", err)
	}

	s.LogEvent(ctx, "prescribe", "p", s1.ID, map[string]any{"key": "k"})
	s.LogEvent(ctx, "prescribe", "p", s2.ID, map[string]any{"key": "k"})
	s.LogEvent(ctx, "prescribe_ack", "p", s1.ID, map[string]any{"keys": []string{"k"}})

	// id1 만 판단을 가진다 — 이것이 분모를 가르는 축이다.
	if _, err := s.AddJudgment(ctx, model.Judgment{
		Project: "p", SessionID: s1.ID, Kind: model.JudgmentDecision,
		Title: "t", Body: "b",
	}); err != nil {
		t.Fatalf("AddJudgment: %v", err)
	}

	emitted, reachable, acked, err := s.AckReach(ctx, "p")
	if err != nil {
		t.Fatalf("AckReach: %v", err)
	}
	if emitted != 2 {
		t.Errorf("발화 카드 %d, 원하는 것 2", emitted)
	}
	if reachable != 1 {
		t.Errorf("판단 가진 카드 %d, 원하는 것 1 — 분모는 이쪽이다", reachable)
	}
	if acked != 1 {
		t.Errorf("ack 한 카드 %d, 원하는 것 1", acked)
	}
	// 이 시험의 존재 이유 — 두 분모가 **다른 답**을 낸다.
	if emitted == reachable {
		t.Fatal("두 분모가 같으면 이 지표는 아무것도 안 가른다")
	}
}
```

`model.JudgmentDecision` 의 정확한 상수명은 `internal/model/types.go` 에서 확인한다
(`model.JudgmentBlocked`·`model.JudgmentAsk` 가 `service/board.go` 에서 쓰이는 것과 같은 꼴이다).

- [ ] **Step 2: 실패를 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/store/ -run TestAckReach -v
```

기대: 컴파일 실패 — `s.AckReach undefined`.

- [ ] **Step 3: 구현한다**

`internal/store/prescribe_reach.go` 를 만든다.

```go
package store

import (
	"context"
	"fmt"
)

// AckReach 는 처방 확인율의 **분모를 가른** 세 수다.
//
//	emitted   처방이 발화된 카드 수
//	reachable 그중 판단을 하나라도 가진 카드 수 = ack 이 원리적으로 닿을 수 있는 카드
//	acked     실제로 ack 이 꽂힌 카드 수
//
// ★ 왜 분모가 둘인가. 발자국은 훅이 쓰고 판단은 MCP 가 쓴다. 한 대화의 카드가 갈리면
// 처방은 발자국 카드에서 뜨고 ack 은 판단 카드에 꽂힌다 — 그 발자국 카드는 판단이 0이라
// **영영 ack 할 수 없다.** 그것을 분모에 두면 확인율이 규율이 아니라 갈림을 잰다.
//
// 실측(2026-08-05): emitted 26 · reachable 4 · acked 4.
// 옛 분모로 15%, 고친 분모로 100%였다 — 닿을 수 있었던 카드는 전부 ack 했다.
//
// ★ payload 를 안 본다. 판단이 0인 카드는 애초에 prescribe_ack 이벤트를 안 남기므로,
// 분모에서 빼야 할 바로 그 카드들이 payload 에는 영영 안 나타난다.
func (s *Store) AckReach(ctx context.Context, project string) (emitted, reachable, acked int, err error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT (SELECT count(DISTINCT e.session_id) FROM event e
		        WHERE e.kind='prescribe' AND e.project=?),
		       (SELECT count(DISTINCT e.session_id) FROM event e
		        WHERE e.kind='prescribe' AND e.project=?
		          AND EXISTS (SELECT 1 FROM judgment j WHERE j.session_id = e.session_id)),
		       (SELECT count(DISTINCT e.session_id) FROM event e
		        WHERE e.kind='prescribe_ack' AND e.project=?)`,
		project, project, project)
	if err := row.Scan(&emitted, &reachable, &acked); err != nil {
		return 0, 0, 0, fmt.Errorf("확인율 도달성 조회 실패(project=%q): %w", clip(project, 64), err)
	}
	return emitted, reachable, acked, nil
}
```

`internal/service/board.go` 에 타입과 배선을 더한다.

```go
// AckReach 는 처방 확인율이 지금 무엇을 재고 있는지다.
// Emitted 와 Reachable 이 크게 다르면 그 차이가 곧 카드 갈림이다.
type AckReach struct {
	Emitted   int `json:"emitted"`
	Reachable int `json:"reachable"`
	Acked     int `json:"acked"`
}
```

`BoardView` 에 필드를 더한다 — `Splits` 아래.

```go
	// AckReach 는 detail 꼬리 전용이다. nil 이면 이 조회가 안 돌았다는 뜻이다.
	AckReach *AckReach `json:"ack_reach,omitempty"`
```

`Board` 의 `view.Splits = …` 다음에 더한다.

```go
	// ★ 실패해도 보드를 죽이지 않는다 — 파생이 통째로 실패해도 응답을 내는 것이
	//   이 도구의 존재 이유다. 다만 침묵하지 않고 파생 실패로 남긴다.
	if em, re, ak, aerr := s.st.AckReach(ctx, project); aerr != nil {
		d.fail("ack-reach", aerr)
	} else {
		view.AckReach = &AckReach{Emitted: em, Reachable: re, Acked: ak}
	}
```

`internal/mcpsrv/render.go` 의 `boardDetailFoot` 끝에 한 줄을 더한다.

```go
	if r := v.AckReach; r != nil && r.Emitted > 0 {
		out = append(out, fmt.Sprintf(
			"확인율 — 발화 카드 %d · 그중 ack 이 닿을 수 있는 카드 %d · 실제 ack %d "+
				"(두 수가 크게 다르면 그 차이가 카드 갈림이다)",
			r.Emitted, r.Reachable, r.Acked))
	}
```

(`boardDetailFoot` 의 반환 변수 이름이 `out` 이 아니면 그 이름을 쓴다.)

`plugins/flightdeck/DESIGN.md` §10 에서 확인율을 설명하는 자리에 다음을 더한다. **§3 헤더의 표 수는 건드리지 않는다.**

```markdown
> ⚠ 확인율의 분모는 **발화 카드 수가 아니다.** 발자국은 훅이 쓰고 판단은 MCP 가 쓰므로,
> 한 대화의 카드가 갈리면 처방은 발자국 카드에서 뜨고 ack 은 판단 카드에 꽂힌다 —
> 그 발자국 카드는 판단이 0이라 영영 ack 할 수 없다. 분모는 **판단을 하나라도 가진
> 카드**여야 한다. 실측(2026-08-05): 발화 26 · 도달 가능 4 · ack 4 — 옛 분모로 15%,
> 고친 분모로 100%였다. 보드의 `detail=true` 꼬리가 세 수를 함께 낸다.
```

- [ ] **Step 4: 초록을 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/store/ -run TestAckReach -v && go test ./internal/service/ ./internal/mcpsrv/
```

기대: PASS.

- [ ] **Step 5: 되돌려 빨강을 확인한다**

`AckReach` 의 두 번째 부질의에서 `EXISTS (…)` 절을 지우고 돌린다.
기대: `reachable != 1` 과 `emitted == reachable` 둘 다 FAIL. 되돌린다.

- [ ] **Step 6: 커밋**

```bash
cd plugins/flightdeck/server && gofmt -l . && go vet ./... && go test ./...
git add internal/store/prescribe_reach.go internal/store/prescribe_reach_test.go \
        internal/service/board.go internal/mcpsrv/render.go ../DESIGN.md
git commit -m "fix(flightdeck): 확인율의 분모가 갈린 카드를 세고 있었다 — 15%가 아니라 100%였다"
```

---

### Task 6: `GET /api/v1/sessions` — 카드를 안 만드는 조회

**Files:**
- Modify: `plugins/flightdeck/server/internal/store/session.go` (`sessionByTriple` 아래에 Store 수준 함수 추가)
- Modify: `plugins/flightdeck/server/internal/service/session.go` (파일 끝)
- Modify: `plugins/flightdeck/server/internal/api/api.go` (라우트 한 줄)
- Modify: `plugins/flightdeck/server/internal/api/handlers_session.go` (핸들러 추가)
- Test: `plugins/flightdeck/server/internal/api/find_session_test.go`

**Interfaces:**
- Consumes: `store.notFoundNote(NFSession, …)` (기존) · `store.sessionCols` · `store.scanSession` (기존)
- Produces: `func (s *Store) FindSession(ctx context.Context, machineID, worktree, ccSessionID string) (model.Session, error)` · `func (s *Service) FindSession(ctx context.Context, machineID, worktree, ccSessionID string) (model.Session, error)` · `GET /api/v1/sessions?machine=&worktree=&cc=`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/api/find_session_test.go` 를 만든다. 헬퍼는 `helper_test.go` 의
`newEnv(t, nil) *env` · `e.do(method, path, body, opts...) *httptest.ResponseRecorder` ·
`e.write(...)` · `decodeBody(t, w) map[string]any` · `loopback()` 이다.

```go
package api

import (
	"net/http"
	"net/url"
	"testing"
)

// 이 시험이 이 항목의 전부다 — 조회가 **행을 만들지 않는 것**.
func TestFindSessionNeverCreatesACard(t *testing.T) {
	e := newEnv(t, nil)

	q := url.Values{"machine": {"m1"}, "worktree": {"/repo"}, "cc": {"cc-none"}}
	before := len(decodeBody(t, e.do(http.MethodGet, "/api/v1/dashboard.json?project=p", nil, loopback()))["sessions"].([]any))

	w := e.do(http.MethodGet, "/api/v1/sessions?"+q.Encode(), nil, loopback())
	if w.Code != http.StatusNotFound {
		t.Fatalf("상태 %d, 원하는 것 404 — 없는 것은 없다고 말해야 한다\n본문: %s", w.Code, w.Body.String())
	}

	after := len(decodeBody(t, e.do(http.MethodGet, "/api/v1/dashboard.json?project=p", nil, loopback()))["sessions"].([]any))
	if after != before {
		t.Fatalf("세션이 %d장에서 %d장으로 늘었다 — 조회가 카드를 만들었다. "+
			"이 항목이 고치려는 바로 그 결함이다", before, after)
	}
}

func TestFindSessionReturnsExistingCard(t *testing.T) {
	e := newEnv(t, nil)

	opened := decodeBody(t, e.write(http.MethodPost, "/api/v1/sessions", map[string]any{
		"project": "p", "project_path": t.TempDir(), "machine_id": "m1",
		"worktree": "/repo", "cc_session_id": "cc-a",
	}, loopback()))
	want := opened["session"].(map[string]any)["id"].(string)

	q := url.Values{"machine": {"m1"}, "worktree": {"/repo"}, "cc": {"cc-a"}}
	w := e.do(http.MethodGet, "/api/v1/sessions?"+q.Encode(), nil, loopback())
	if w.Code != http.StatusOK {
		t.Fatalf("상태 %d, 원하는 것 200\n본문: %s", w.Code, w.Body.String())
	}
	got := decodeBody(t, w)["session"].(map[string]any)["id"].(string)
	if got != want {
		t.Fatalf("세션 %q, 원하는 것 %q", got, want)
	}
}
```

`dashboard.json` 의 세션 배열 키가 `sessions` 가 아니면 `newEnv` 로 세운 서버의 실제 응답을
한 번 찍어 확인한다. 카드 수를 세는 다른 길이 있으면 그것을 써도 된다 — 이 시험이 잠그는
것은 **개수가 안 변하는 것**이지 특정 엔드포인트가 아니다.

- [ ] **Step 2: 실패를 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/api/ -run TestFindSession -v
```

기대: 404 가 아니라 405 나 404(라우트 없음) — 어느 쪽이든 FAIL.

- [ ] **Step 3: 구현한다**

`internal/store/session.go` 의 `sessionByTriple` **아래**에 더한다.

```go
// FindSession 은 3중키로 세션을 찾는다. **없으면 만들지 않는다.**
//
// OpenSession 과 이 함수의 차이가 이 항목의 전부다 — 저쪽은 upsert 라 행이 없으면
// 만든다. 훅의 복구 갈래가 "옛 cc 의 카드를 찾는" 용도로 그것을 부르고 있었고,
// 카드가 없고 rekey 가 거절되는 갈래에서 **빈 카드 한 장을 남겼다.**
func (s *Store) FindSession(ctx context.Context, machineID, worktree, ccSessionID string) (model.Session, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+sessionCols+` FROM session
		 WHERE machine_id = ? AND worktree = ? AND cc_session_id = ?`,
		machineID, worktree, ccSessionID)
	sess, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return sess, notFoundNote(NFSession, "3중키(머신·워크트리·cc 세션)에 해당하는")
	}
	if err != nil {
		return sess, fmt.Errorf("세션 3중키 조회 실패(machine=%q worktree=%q): %w",
			clip(machineID, 64), clip(worktree, 200), err)
	}
	return sess, nil
}
```

`internal/service/session.go` 끝에 더한다.

```go
// FindSession 은 세션을 **찾기만** 한다. 없으면 만들지 않는다.
//
// OpenSession 과 달리 파생(branch·head·ahead)을 안 붙인다 — 이 조회를 부르는 자리는
// 복구 갈래이고 거기서 필요한 것은 "그 카드가 있느냐"와 그 id 뿐이다. 파생을 붙이면
// 가장 잦은 훅 경로에 git 호출이 는다.
func (s *Service) FindSession(ctx context.Context, machineID, worktree, ccSessionID string) (model.Session, error) {
	return s.st.FindSession(ctx, machineID, worktree, ccSessionID)
}
```

`internal/api/api.go` 에 라우트 한 줄. 앵커는 `mux.HandleFunc("POST /api/v1/sessions", s.handleOpenSession)` 이고 **그 바로 아래**다.

```go
	mux.HandleFunc("GET /api/v1/sessions", s.handleFindSession)
```

`internal/api/handlers_session.go` 의 `handleOpenSession` **아래**에 더한다.

```go
// handleFindSession 은 3중키로 세션을 찾는다. **만들지 않는다.**
//
// 이 자리가 없어서 복구 갈래가 upsert 를 조회로 쓰고 있었고, 그것이 빈 카드를 낳았다.
func (s *server) handleFindSession(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sess, err := s.svc.FindSession(r.Context(),
		strings.TrimSpace(q.Get("machine")),
		strings.TrimSpace(q.Get("worktree")),
		strings.TrimSpace(q.Get("cc")))
	if err != nil {
		s.fail(w, r, err) // 없으면 notFound 가 404 로 나간다
		return
	}
	infoFrom(r.Context()).setSession(sess.ID)
	s.writeJSON(w, r, http.StatusOK, map[string]any{"session": sess})
}
```

- [ ] **Step 4: 초록을 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/api/ -run TestFindSession -v && go test ./internal/store/ ./internal/service/
```

기대: PASS.

- [ ] **Step 5: 되돌려 빨강을 확인한다**

`Service.FindSession` 이 `s.st.OpenSession(...)` 을 부르도록 바꾸고 돌린다.
기대: `TestFindSessionNeverCreatesACard` 가 "세션이 N장에서 N+1장으로 늘었다" 로 FAIL.
**이것이 이 과제의 핵심 단정이다** — 확인했으면 되돌린다.

- [ ] **Step 6: 커밋**

```bash
cd plugins/flightdeck/server && gofmt -l . && go vet ./... && go test ./...
git add internal/store/session.go internal/service/session.go \
        internal/api/api.go internal/api/handlers_session.go internal/api/find_session_test.go
git commit -m "feat(flightdeck): 카드를 안 만들고 찾는 조회 — 복구 갈래가 빈 카드를 낳던 문을 닫는다"
```

---

### Task 7: `cmd/fd` — 복구 갈래를 조회로 바꾼다

**Files:**
- Modify: `plugins/flightdeck/server/cmd/fd/app.go` (`App.OpenSession` 아래)
- Modify: `plugins/flightdeck/server/cmd/fd/hook.go` (복구 갈래 · 그 위 주석)
- Test: `plugins/flightdeck/server/cmd/fd/hook_recovery_test.go`

**Interfaces:**
- Consumes: `GET /api/v1/sessions` (Task 6) · `Client.Read(ctx, path)` (기존)
- Produces: `func (a *App) FindSession(ctx context.Context, ccSession string) (model.Session, error)`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`cmd/fd/hook_recovery_test.go` 를 만든다. 헬퍼는 `harness_test.go:44` 의
`newHarness(t) *harness` 이고, 실행은 `h.run(stdin string, args ...string) (int, string)` 이다.
`h.openStore()` 로 같은 DB 를 직접 열어 셀 수 있다.

**비콘을 심는 방법은 이 패키지의 `hook_beacon_test.go` 가 이미 보여 준다** — 그 파일에서
비콘 파일 경로와 형식을 그대로 가져온다. 새로 만들지 마라(같은 판단이 두 자리에 산다).

```go
package main

import (
	"testing"
)

// 항목 본문: "도달 조건이 3중이라(늦은 심기 + 이 워크트리에 카드가 없던 비콘 cc +
// 서버가 닿는 상태에서의 거절) 시험이 안 닿는다. 새 조회를 만들면 그 갈래를 시험으로
// 닿게 하는 것까지 함께 해라." — 이 시험이 그 갈래다.
func TestRecoveryBranchDoesNotCreateAStrayCard(t *testing.T) {
	h := newHarness(t)

	// 비콘에 옛 cc 를 심는다(SessionID 는 비운다 = 늦은 심기).
	// 그 cc 로는 이 워크트리에 카드가 **없다** — 그것이 넷째 갈래의 조건이다.
	writeBeaconForTest(t, h, "cc-old") // hook_beacon_test.go 의 방식을 그대로 쓴다

	before := countSessionRows(t, h)
	code, out := h.run("", "hook", "session-start")
	after := countSessionRows(t, h)

	if code != 0 {
		t.Fatalf("훅이 종료코드 %d — 훅은 세션을 막으면 안 된다\n%s", code, out)
	}
	if after != before+1 {
		t.Fatalf("세션이 %d → %d 장. 원하는 것 %d 장 — 새 cc 의 카드 하나만 생겨야 한다. "+
			"둘 늘었으면 복구 갈래가 빈 카드를 만든 것이다", before, after, before+1)
	}
}

// countSessionRows 는 하네스의 DB 에서 세션 행을 센다.
func countSessionRows(t *testing.T, h *harness) int {
	t.Helper()
	h.openStore()
	ss, err := h.store.ListSessions(t.Context(), "kweiza-cc-plugins")
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	return len(ss)
}
```

`h.store` 의 실제 필드명과 하네스가 쓰는 프로젝트 id 는 `harness_test.go` 에서 확인해 맞춘다.
`t.Context()` 가 이 Go 판에 없으면 `context.Background()` 를 쓴다.

- [ ] **Step 2: 실패를 확인한다**

```bash
cd plugins/flightdeck/server && go test ./cmd/fd/ -run TestRecoveryBranch -v
```

기대: `세션이 N → N+2 장` 으로 FAIL — 지금 `OpenSession` 이 빈 카드를 만든다.
**이 빨강을 반드시 눈으로 확인한다.** 안 나면 시험이 그 갈래에 안 닿은 것이므로,
비콘 심기와 서버 도달 상태를 다시 본다.

- [ ] **Step 3: 구현한다**

`cmd/fd/app.go` 의 `App.OpenSession` **아래**에 더한다.

```go
// FindSession 은 이 좌표의 세션을 **찾기만** 한다. 없으면 오류다(만들지 않는다).
//
// ★ Client 에 새 메서드를 안 만든다 — 범용 Read 가 캐시·열화까지 이미 갖고 있고,
// 같은 갈래를 둘로 만들면 한쪽만 고칠 때 조용히 어긋난다.
func (a *App) FindSession(ctx context.Context, ccSession string) (model.Session, error) {
	q := url.Values{
		"machine":  {a.machine},
		"worktree": {a.proj.Worktree},
		"cc":       {ccSession},
	}
	res, err := a.cli.Read(ctx, "/api/v1/sessions?"+q.Encode())
	if err != nil {
		return model.Session{}, err
	}
	var body struct {
		Session model.Session `json:"session"`
	}
	if uerr := json.Unmarshal(res.Body, &body); uerr != nil {
		return model.Session{}, fmt.Errorf("세션 조회 응답 해석 실패: %w", uerr)
	}
	return body.Session, nil
}
```

import 에 `net/url` 과 `model` 이 없으면 더한다.

`cmd/fd/hook.go` 의 복구 갈래를 바꾼다. 앵커는
`if haveBeacon && beacon.SessionID == "" && beacon.CCSessionID != "" && beacon.CCSessionID != cc {` 이다.

```go
	if haveBeacon && beacon.SessionID == "" && beacon.CCSessionID != "" && beacon.CCSessionID != cc {
		old, oerr := a.FindSession(ctx, beacon.CCSessionID)
		switch {
		case oerr != nil:
			// 못 찾아도 오류가 아니다 — 비콘을 못 찾은 것과 같은 급이라 폴백한다.
			// ★ 그리고 이제 **아무것도 안 만든다.** 이것이 옛 코드와의 차이 전부다.
			a.log.Warn("비콘의 옛 cc 로 카드를 못 찾았다 — 이번 전환은 합치지 못한다",
				"error", oerr.Error(), "cc", clip(beacon.CCSessionID, 40))
		case old.ID != "":
			beacon.SessionID = old.ID
		}
	}
```

그 **위의 네 갈래 표 주석**을 새 사실로 고친다. 옛 주석의 `옛 cc 의 카드가 없다 + rekey 거절 → **카드 2장. 그중 한 장은 이 줄이 만든 빈 카드다**` 줄을 다음으로 바꾼다.

```go
	//	옛 cc 의 카드가 있다  + rekey 성공 → 카드 1장. 이 갈래가 노린 것이다
	//	옛 cc 의 카드가 있다  + rekey 거절 → 카드 2장. 다만 둘 다 원래 있던 것이다
	//	옛 cc 의 카드가 없다  + rekey 성공 → 해당 없음(찾은 것이 없으면 rekey 를 안 탄다)
	//	옛 cc 의 카드가 없다  + rekey 거절 → 카드 1장. **조회가 아무것도 안 만든다**
	//
	// ★ 넷째 갈래가 2장에서 1장이 된 것이 fd-session-lookup-without-upsert 의 성과다.
	//   옛 코드는 여기서 OpenSession(3중키 upsert)을 조회로 썼고, 그것이 행이 없을 때
	//   **만들었다.** 지금은 GET /api/v1/sessions 라 만들 수 없다.
```

- [ ] **Step 4: 초록을 확인한다**

```bash
cd plugins/flightdeck/server && go test ./cmd/fd/ -v
```

기대: 새 시험 PASS + 기존 시험 전부 PASS.

- [ ] **Step 5: 되돌려 빨강을 확인한다**

`a.FindSession(...)` 을 `a.OpenSession(ctx, beacon.CCSessionID, "")` 로 되돌리고 돌린다.
기대: `TestRecoveryBranchDoesNotCreateAStrayCard` FAIL. 되돌린다.

- [ ] **Step 6: 커밋**

```bash
cd plugins/flightdeck/server && gofmt -l . && go vet ./... && go test ./...
git add cmd/fd/app.go cmd/fd/hook.go cmd/fd/hook_recovery_test.go
git commit -m "fix(flightdeck): 복구 갈래가 조회로 찾는다 — 넷째 갈래의 빈 카드가 사라진다"
```

---

### Task 8: 통합 검증과 판단 기록

**Files:**
- Modify: 없음(코드 변경 없이 검증만). 필요하면 앞선 과제의 파일을 고친다.

**Interfaces:**
- Consumes: Task 1~7 전부
- Produces: 없음(원장에 판단 하나)

- [ ] **Step 1: 죽은 코드를 정리한다**

Task 4 에서 `rankCards`·`boardCard` 의 호출부가 줄었다. 확인한다.

```bash
cd plugins/flightdeck/server && grep -rn "rankCards\|boardCard(" --include=*.go . | grep -v _test
```

`conversationCard` 가 카드 1장일 때 `boardCard` 를 부르므로 `boardCard` 는 살아 있다.
`rankCards` 가 비시험 호출부 0건이면 **지운다** — 아무도 안 부르는 함수를 남기면
다음 사람이 그것이 살아 있는 축이라고 믿는다. 시험만 부르고 있으면 그 시험도 함께 본다.

- [ ] **Step 2: 전 패키지 검증**

```bash
cd plugins/flightdeck/server
gofmt -l .                       # 출력이 비어야 한다
go build ./...
go vet ./...
go test ./... -race
```

기대: 전부 초록. `-race` 까지 돈다.

- [ ] **Step 3: 교차 빌드**

```bash
cd plugins/flightdeck/server
GOOS=darwin GOARCH=arm64 go build ./...
GOOS=windows GOARCH=amd64 go build ./...
```

기대: 둘 다 성공. `judge/split.go` 가 `filepath` 를 쓰므로 Windows 에서 경로 구분자가
갈린다 — 시험이 `/` 경로로만 쓰여 있어 Windows 에서 다르게 돌 수 있다. 교차 **빌드**만
확인하고, 경로 판정의 플랫폼 차이는 판단에 적는다.

- [ ] **Step 4: 살아 있는 브랜치와 충돌을 확인한다**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins
for b in $(git branch --format='%(refname:short)' | grep -v '^main$'); do
  git merge-tree $(git merge-base main "$b") main "$b" >/dev/null 2>&1 \
    && echo "무충돌 $b" || echo "★충돌 $b"
done
```

기대: `fd-pick-bundle` · `fd-server-self-restart` 를 포함해 무충돌.
충돌이 나면 **그 세션에 `note(kind='ask')` 로 정확한 자리를 알린 뒤** 해소한다.

- [ ] **Step 5: 실물로 한 번 돌려 본다**

새 바이너리를 세워 보드를 낸다. 배너가 뜨는지, 머리줄이 대화 수를 내는지 눈으로 본다.

```bash
cd plugins/flightdeck/server && go build -o /tmp/fd-verify ./cmd/fd
/tmp/fd-verify doctor
```

기대: 크래시 없음. `doctor` 가 도는 것으로 배선이 안 깨졌음을 확인한다.
(보드는 서버가 새 판이어야 하므로 여기서 안 본다 — 그 사실 자체가 스펙 §2 다.)

- [ ] **Step 6: 판단을 남기고 항목을 닫는다**

`finish` 로 네 항목을 각각 닫는다. 스펙 §9 의 완료 판정을 그대로 따른다.

- `fd-session-worktree-is-cwd-not-repo-root` → `done`. 본문: 탐지가 무엇을 보고하는지 ·
  `resolveProject` 를 왜 안 고쳤는지 · 배포 간극 실측 전문.
- `fd-session-lookup-without-upsert` → `done`. 본문: 넷째 갈래가 2장에서 1장이 된 것 ·
  그 갈래에 시험이 닿게 된 방법.
- `fd-board-counts-one-conversation-many-times` → `done`. 본문: 접기 모양 · 안 접은 것
  (빈 cc) · `Sessions` 계약을 왜 안 깼는지.
- `fd-ack-metric-measures-card-split` → **`done` 이 아니다.** 분모는 고쳤지만 재측정은
  배포 뒤라야 뜻이 있다. `note(kind='handoff')` 로 재측정 조건과 실측(26/4/4, 15%→100%)을
  적고 `finish(outcome='done')` 하되 본문에 **"재측정은 fd-main-never-reaches-running-clients
  뒤"** 를 명시한다.

`finish` 의 `followups` 에 이번에 나온 후속을 싣는다. **`add` 로 따로 만들지 마라** —
그러면 FK 가 안 이어져 다음 세션의 `pick` 이 판단을 함께 못 낸다(이 저장소가 겪은 사고다).

---

## Self-Review

**1. 스펙 커버리지**

| 스펙 절 | 과제 |
|---|---|
| §3.1 갈림 탐지 + 울타리 | Task 1 · 3 · 4 |
| §3.2 카드 접기 | Task 2 · 4 |
| §3.3 빈 카드 안 만드는 조회 | Task 6 · 7 |
| §3.4 ack 분모 | Task 5 |
| §5 오류 처리(파생 실패에 침묵 안 함) | Task 5 Step 3 의 `d.fail("ack-reach", …)` |
| §6 시험(되돌려 빨강 · 운영 진입점) | 각 과제 Step 5 · Task 7 Step 2 |
| §7 안 하는 것 | Global Constraints |
| §8 랜딩 순서 | Task 8 Step 4 |
| §9 완료 판정 | Task 8 Step 6 |

빠진 절 없음.

**2. 빈칸 스캔**

`TBD` · `TODO` · "적절히 처리한다" · "위 것들의 시험을 쓴다" — 없음. 모든 코드 단계에
실제 코드 블록이 있다.

기존 헬퍼 이름은 전부 **실물로 확인해 박았다**:

| 패키지 | 헬퍼 | 자리 |
|---|---|---|
| `internal/store` | `newStore(t) *Store` | `store_test.go:24` |
| `internal/api` | `newEnv(t, nil) *env` · `e.do(...)` · `e.write(...)` · `decodeBody(t, w)` · `loopback()` | `helper_test.go:94,126,135,166,188` |
| `cmd/fd` | `newHarness(t) *harness` · `h.run(stdin, args...) (int, string)` · `h.openStore()` | `harness_test.go:44,94,178` |

남은 세 자리는 확정 대신 **어디서 확인할지**를 적었다 — `model.JudgmentDecision` 상수명 ·
`dashboard.json` 의 세션 배열 키 · `harness` 의 store 필드명과 프로젝트 id. 셋 다 해당
파일을 한 번 열면 끝나고, 추측해서 박으면 오히려 틀린 이름이 계획에 박힌다.

**3. 타입 일관성**

- `judge.SplitCard` / `judge.SplitReport` / `judge.DetectUnnormalizedSplit` / `judge.SameConversation` — Task 1 정의, Task 3·4 소비. 이름 일치.
- `service.Conversation` / `service.FoldConversations` — Task 2 정의, Task 4 소비. 일치.
- `service.BoardView.Conversations` / `.Splits` / `.AckReach` — Task 2·3·5 정의, Task 4·5 소비. 일치.
- `Store.AckReach(ctx, project) (emitted, reachable, acked int, err error)` — Task 5 정의·소비. 일치.
- `Store.FindSession` / `Service.FindSession` / `App.FindSession` — Task 6·7. 셋 다 이름이 같지만 시그니처가 다르다(store·service 는 3중키 전부, app 은 cc 만 받고 나머지는 자기 좌표에서 채운다). 의도한 것이고 Task 7 코드에 그 이유가 주석으로 있다.
- `lastSignal(c SessionCard, now)` — 기존 함수. Task 4 의 `lastSignalOfConversation` 이 부른다. 시그니처 확인함.
