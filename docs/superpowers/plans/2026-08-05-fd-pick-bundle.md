# fd-pick-bundle 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `pick` 이 큐의 1순위 하나가 아니라 **함께 갈 항목들을 묶어** 추천·선점한다. 묶을 것이 없으면 지금과 똑같이 단독으로 낸다.

**Architecture:** 판정은 전부 `internal/judge` 의 순수 함수에 둔다(설계 §12). `Eligible` 위에 `EligibleBundle` 을 얹고 기존 함수는 한 줄도 안 고친다. 묶음은 **저장되지 않는다** — 테이블도 id 도 없고, DB 에 남는 것은 지금과 똑같은 `claim` 행 N개다(다중 선점은 `claim` 의 기본키가 `(project, item_id)` 라 이미 허용된다). 브랜치는 **묶음 선두 항목의 id** 이고 나머지는 그 워크트리에서 함께 간다.

**Tech Stack:** Go 1.25 · SQLite(modernc.org/sqlite, CGO 없음) · 표준 `testing` 만 사용(단정 라이브러리 없음)

**설계 문서:** `docs/superpowers/specs/2026-08-05-fd-pick-bundle-design.md` — 절 번호는 그 문서를 가리킨다.

**작업 디렉토리:** 이 워크트리(`.flightdeck/worktrees/fd-pick-bundle`, 브랜치 `fd-pick-bundle`). Go 명령은 전부 `plugins/flightdeck/server` 에서 돈다.

## Global Constraints

- **판정 로직은 `internal/judge` 의 순수 함수에 둔다.** 상태도 I/O 도 없다. 시험은 그 함수를 **직접** 부른다 — 로직의 사본을 단정하면 변이가 새어 나간다(설계 §12).
- **다중 조건 판정은 불리언이 아니라 사유를 돌려준다.** 사유가 없으면 "조건 A 때문"과 "이 축을 아예 안 본다"가 구분되지 않는다.
- **키 부재를 값으로 접지 않는다.** 못 읽은 축은 `nil` 로 두고, 렌더가 "이 응답은 그 축을 안 읽었다"를 말한다.
- **어느 갈래에서도 침묵하지 않는다.** 묶을 게 없어도 한 줄 찍는다.
- **`Eligible`·`PathsOverlap`·`OverlapPairs`·`lessCandidate` 는 수정하지 않는다.** 소비자의 질문이 다르다.
- **MCP 도구 수는 6개 그대로.** 새 도구를 만들지 않는다(컨텍스트 예산 — 설계 §6).
- **조정 가능한 상수를 새로 만들지 않는다.** 묶음 크기 상한도, 가중치도 없다.
- **새 검사는 망가진 것을 넣어 빨간불을 먼저 확인한다.** 초록만 보고 통과로 단정하지 않는다.
- 커밋 메시지는 `<type>(<scope>): <한 줄>` 형식. 본문은 한국어.
- 모든 커밋 끝에: `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`

---

## File Structure

| 파일 | 책임 | 태스크 |
|---|---|---|
| `internal/judge/bundle.go` **(신규)** | 묶음 판정 전부 — 축 술어 · 링크 · 묶음 조립 · 정렬 | 1–4 |
| `internal/judge/bundle_test.go` **(신규)** | 위의 순수 함수 시험 | 1–4 |
| `internal/store/judgment.go` | `JudgmentLinksForItems` 접근자 추가 | 5 |
| `internal/store/migrations/00N_pick_bundle.sql` **(신규)** | `pick_eval.picked_with` 컬럼 | 6 |
| `internal/store/schema.sql` · `store.go` · `item.go` | 신규 DB 의 스키마 · 버전 · 쓰기 | 6 |
| `internal/model/types.go` | `PickEval.PickedWith` 한 줄 | 6 |
| `internal/service/pick.go` | 형제 색인 조립 · 추천 경로 · 묶음 선점 | 5, 7, 8 |
| `internal/mcpsrv/tools.go` | `pick` 의 `item_ids` 인자 | 9 |
| `internal/mcpsrv/render.go` | 묶음 절 렌더 | 10 |
| `cmd/fd/wire_test.go` | 서버→JSON→클라이언트→렌더 왕복 | 11 |
| `skills/fd-pickup/SKILL.md` · `DESIGN.md` | 표면 문서 | 12 |

**태스크 1–4 가 이 기능의 전부다.** 나머지는 그 판정을 배선하고 화면에 내는 일이다.

---

### Task 1: `judge.SamePaths` — 정확히 같은 경로만

경로 축이 **조상 디렉토리를 세면 안 되는** 이유가 이 태스크의 전부다. 실측에서 `fd-judgment-backup-missing` 이 `plugins/flightdeck/server/cmd/fd` 를 디렉토리 통째로 선언했고, 그것이 `PathsOverlap` 의 조상 규칙과 만나 열린 16건 중 10건을 한 묶음으로 만들었다(설계 §0.1).

**Files:**
- Create: `plugins/flightdeck/server/internal/judge/bundle.go`
- Create: `plugins/flightdeck/server/internal/judge/bundle_test.go`

**Interfaces:**
- Consumes: `components(p string) []string` (같은 패키지 `paths.go:95` — 앞뒤·중복 `/` 와 `.` 성분을 걷어낸다. `..` 는 **안** 걷어낸다)
- Produces: `func SamePaths(a, b []string) []string` — 두 집합에 **정확히 같은** 경로가 있으면 `a` 쪽 표기 그대로 돌려준다. 없으면 `nil`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`plugins/flightdeck/server/internal/judge/bundle_test.go`:

```go
package judge

import (
	"reflect"
	"testing"
)

// SamePaths 는 PathsOverlap 과 **일부러 다르다**.
// PathsOverlap 의 소비자는 "남의 세션과 부딪히나"라 넓게 잡는 것이 옳고,
// 이 함수의 소비자는 "함께 할 일인가"라 넓게 잡으면 큐가 통째로 한 묶음이 된다.
// 아래 표의 조상 관계 줄들이 그 차이를 못박는 자리다.
func TestSamePathsCountsOnlyExactTokens(t *testing.T) {
	cases := []struct {
		name string
		a, b []string
		want []string
	}{
		{"같은 파일", []string{"a/b.go"}, []string{"a/b.go"}, []string{"a/b.go"}},
		{"표기 흔들림은 흡수한다", []string{"a/b/"}, []string{"./a//b"}, []string{"a/b/"}},

		// ── 실측 결함 그대로. 이 셋이 nil 이어야 한다 ──
		{"조상 디렉토리는 안 센다",
			[]string{"plugins/flightdeck/server/cmd/fd"},
			[]string{"plugins/flightdeck/server/cmd/fd/hook.go"}, nil},
		{"자손 파일도 안 센다",
			[]string{"plugins/flightdeck/server/cmd/fd/client.go"},
			[]string{"plugins/flightdeck/server/cmd/fd"}, nil},
		{"디렉토리끼리 조상 관계도 안 센다",
			[]string{"internal/store"}, []string{"internal/store/session.go"}, nil},

		{"무관", []string{"a/b.go"}, []string{"c/d.go"}, nil},
		{"여럿 중 하나만 일치",
			[]string{"x.go", "shared.go", "y.go"},
			[]string{"shared.go", "z.go"}, []string{"shared.go"}},
		{"중복은 한 번만",
			[]string{"s.go", "./s.go"}, []string{"s.go"}, []string{"s.go"}},
		{"빈 토큰은 무시한다", []string{"", "  ", "a.go"}, []string{"a.go", ""}, []string{"a.go"}},
		{"양쪽 다 비면", nil, nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SamePaths(c.a, c.b)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("SamePaths(%v, %v) = %v, 원하는 값 %v", c.a, c.b, got, c.want)
			}
		})
	}
}

// 순수 함수는 인자를 안 고친다. 고치면 시험이 보는 것과 호출자가 보는 것이 갈라진다.
func TestSamePathsDoesNotMutateInput(t *testing.T) {
	a := []string{"a.go", "b.go"}
	b := []string{"b.go"}
	SamePaths(a, b)
	if !reflect.DeepEqual(a, []string{"a.go", "b.go"}) {
		t.Fatalf("입력 a 가 바뀌었다: %v", a)
	}
}
```

- [ ] **Step 2: 시험이 **실패**하는지 확인한다**

```
cd plugins/flightdeck/server
go test ./internal/judge/ -run TestSamePaths -v
```
기대: 컴파일 실패 — `undefined: SamePaths`

- [ ] **Step 3: 최소 구현을 쓴다**

`plugins/flightdeck/server/internal/judge/bundle.go`:

```go
package judge

import "strings"

// 묶음 판정 — pick 이 함께 갈 항목을 고르는 자리.
//
// 이 파일의 함수는 전부 순수 함수다. 그리고 **기존 판정을 하나도 안 고친다** —
// Eligible·PathsOverlap·lessCandidate 는 다른 질문에 답하고 있고,
// 같은 함수를 두 질문에 쓰면 한쪽을 고칠 때 다른 쪽이 조용히 바뀐다.

// SamePaths 는 두 경로 집합에서 **정확히 같은** 토큰을 낸다. 순수 함수다.
//
// ★ PathsOverlap 을 안 쓰는 것이 이 함수의 존재 이유 전부다.
// PathsOverlap 은 조상 디렉토리도 겹침으로 센다(paths.go:27). 그 규칙은 그 함수의
// 소비자("남의 세션과 부딪히나")에게는 옳지만 여기서는 무너진다 —
// 실측에서 `plugins/flightdeck/server/cmd/fd` 를 디렉토리 통째로 선언한 항목 하나가
// 열린 16건 중 10건을 한 묶음으로 끌어왔다(설계 §0.1).
//
// 돌려주는 표기는 **a 쪽 원문**이다. 정규화된 문자열을 돌려주면 화면에 뜨는 경로가
// 항목이 선언한 것과 달라져, 사람이 "내가 적은 그 줄"을 못 찾는다.
func SamePaths(a, b []string) []string {
	norm := make(map[string]bool, len(b))
	for _, y := range b {
		if n := normPath(y); n != "" {
			norm[n] = true
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, x := range a {
		n := normPath(x)
		if n == "" || seen[n] || !norm[n] {
			continue
		}
		seen[n] = true
		out = append(out, x)
	}
	return out
}

// normPath 는 경로를 비교용 정규형으로 만든다.
// components 를 그대로 쓴다 — 성분 규칙이 두 벌이 되면 두 축이 조용히 표류한다.
func normPath(p string) string { return strings.Join(components(p), "/") }
```

- [ ] **Step 4: 시험이 통과하는지 확인한다**

```
go test ./internal/judge/ -run TestSamePaths -v
```
기대: PASS (전 하위 시험)

- [ ] **Step 5: 빨간불을 확인한다 — 규칙을 되돌려 본다**

`SamePaths` 본문의 `norm[n]` 판정을 잠시 `PathsOverlap([]string{x}, b)` 로 바꿔 돌린다.

```
go test ./internal/judge/ -run TestSamePaths -v
```
기대: `조상 디렉토리는 안 센다`·`자손 파일도 안 센다`·`디렉토리끼리 조상 관계도 안 센다` 셋이 **FAIL**.
확인했으면 원래 코드로 되돌리고 다시 돌려 PASS 를 본다.

- [ ] **Step 6: 커밋**

```bash
git add plugins/flightdeck/server/internal/judge/bundle.go plugins/flightdeck/server/internal/judge/bundle_test.go
git commit -m "$(cat <<'EOF'
feat(judge): SamePaths — 묶음 판정의 경로 축은 정확 일치만 센다

PathsOverlap 은 조상 디렉토리도 겹침으로 세고, 그 규칙은 "남의 세션과
부딪히나"에는 옳다. "함께 할 일인가"에 그대로 쓰면 무너진다 — 실측에서
cmd/fd 를 디렉토리 통째로 선언한 항목 하나가 열린 16건 중 10건을 끌어왔다.

PathsOverlap 은 한 줄도 안 고친다. 소비자의 질문이 다르므로 술어를 따로 둔다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: `judge.LinkOf` — 두 항목이 왜 함께 갈 만한가

축 셋을 판정하고 **뭉개지 않고 전부** 돌려준다. 그리고 결합 규칙 하나를 여기서 강제한다: **경로만으로는 링크가 안 된다.**

**Files:**
- Modify: `plugins/flightdeck/server/internal/judge/bundle.go`
- Modify: `plugins/flightdeck/server/internal/judge/bundle_test.go`

**Interfaces:**
- Consumes: `SamePaths`(태스크 1) · `Candidate`(`eligible.go:30`) · `model.After`(`types.go:191`, 셋 중 정확히 하나만 채워진다)
- Produces:
  - `type BundleAxis string` — 상수 `AxisSibling`/`AxisAfter`/`AxisPaths`
  - `type Link struct { Item string; Axes []BundleAxis; Detail string }`
  - `type SiblingIndex map[string][]string` — 항목 id → 그 항목에 걸린 판단 id **사전순** 목록
  - `func LinkOf(lead, other Candidate, sib SiblingIndex) *Link` — 무관하면 `nil`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`bundle_test.go` 에 덧붙인다:

```go
// afterItem·afterSHA 는 시험용 선행이다.
func afterItem(id string) model.After { return model.After{Item: id} }
func afterSHA(s string) model.After   { return model.After{SHA: s} }

func cand(id string, minutes int, paths []string, after ...model.After) Candidate {
	it := openItem(id, minutes, paths...)
	it.After = after
	return Candidate{Item: it}
}

func axesOf(l *Link) []BundleAxis {
	if l == nil {
		return nil
	}
	return l.Axes
}

func TestLinkOfAxes(t *testing.T) {
	sib := SiblingIndex{
		"a": {"J1", "J2"},
		"b": {"J2"},
		"c": {"J9"},
	}
	cases := []struct {
		name       string
		lead, othr Candidate
		want       []BundleAxis
	}{
		{"형제 단독으로 성립한다",
			cand("a", 0, nil), cand("b", 1, nil),
			[]BundleAxis{AxisSibling}},

		{"같은 선행 단독으로 성립한다",
			cand("c", 0, nil, afterSHA("47421b4")), cand("d", 1, nil, afterSHA("47421b4")),
			[]BundleAxis{AxisAfter}},

		// ★ 이 줄이 결합 규칙의 전부다.
		{"경로만 같으면 성립하지 않는다",
			cand("c", 0, []string{"x.go"}), cand("d", 1, []string{"x.go"}),
			nil},

		{"경로는 이미 선 링크를 보강한다",
			cand("a", 0, []string{"x.go"}), cand("b", 1, []string{"x.go"}),
			[]BundleAxis{AxisSibling, AxisPaths}},

		{"축 셋이 전부 맞으면 셋 다 나온다",
			cand("a", 0, []string{"x.go"}, afterSHA("47421b4")),
			cand("b", 1, []string{"x.go"}, afterSHA("47421b4")),
			[]BundleAxis{AxisSibling, AxisAfter, AxisPaths}},

		{"선행이 하나라도 다르면 그 축은 안 선다",
			cand("c", 0, nil, afterSHA("47421b4")), cand("d", 1, nil, afterSHA("f7ff0a7")),
			nil},

		{"선행 순서가 달라도 같은 집합이면 성립한다",
			cand("c", 0, nil, afterSHA("47421b4"), afterItem("z")),
			cand("d", 1, nil, afterItem("z"), afterSHA("47421b4")),
			[]BundleAxis{AxisAfter}},

		{"선행이 양쪽 다 없으면 그 축은 안 선다 — 빈 집합끼리 같다고 세지 않는다",
			cand("c", 0, nil), cand("d", 1, nil),
			nil},

		{"무관",
			cand("c", 0, []string{"x.go"}), cand("e", 1, []string{"y.go"}),
			nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := axesOf(LinkOf(tc.lead, tc.othr, sib))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("축이 %v 인데 %v 를 원한다", got, tc.want)
			}
		})
	}
}

// 사유가 없으면 "왜 이게 들어왔나"에 답할 수 없고, 답 못 하는 자동 선택은
// 두 번째 세션부터 무시된다.
func TestLinkOfCarriesWhy(t *testing.T) {
	sib := SiblingIndex{"a": {"J2"}, "b": {"J2"}}
	l := LinkOf(cand("a", 0, []string{"x.go"}, afterSHA("47421b4")),
		cand("b", 1, []string{"x.go"}, afterSHA("47421b4")), sib)
	if l == nil {
		t.Fatal("링크가 nil 이다")
	}
	for _, want := range []string{"J2", "47421b4", "x.go"} {
		if !strings.Contains(l.Detail, want) {
			t.Fatalf("근거에 %q 가 없다: %q", want, l.Detail)
		}
	}
	if l.Item != "b" {
		t.Fatalf("Link.Item 이 %q 다 — 이웃 id 여야 한다", l.Item)
	}
}

// 공유 판단이 여럿이면 어느 것을 적을지가 흔들려선 안 된다.
// 흔들리면 같은 입력에 다른 응답이 나오고, 그러면 재개가 재출력이 아니게 된다.
func TestLinkOfPicksSharedJudgmentDeterministically(t *testing.T) {
	sib := SiblingIndex{"a": {"J1", "J2", "J3"}, "b": {"J3", "J2", "J1"}}
	first := LinkOf(cand("a", 0, nil), cand("b", 1, nil), sib).Detail
	for i := 0; i < 50; i++ {
		if got := LinkOf(cand("a", 0, nil), cand("b", 1, nil), sib).Detail; got != first {
			t.Fatalf("같은 입력에 다른 근거가 나왔다: %q vs %q", first, got)
		}
	}
	if !strings.Contains(first, "J1") {
		t.Fatalf("공유 판단 중 사전순 첫째(J1)를 안 골랐다: %q", first)
	}
}
```

`bundle_test.go` 의 import 를 `"reflect"`, `"strings"`, `"testing"`, `"github.com/kweiza/flightdeck/internal/model"` 로 맞춘다.

- [ ] **Step 2: 시험이 실패하는지 확인한다**

```
go test ./internal/judge/ -run 'TestLinkOf' -v
```
기대: 컴파일 실패 — `undefined: SiblingIndex`, `undefined: LinkOf`

- [ ] **Step 3: 구현을 쓴다**

`bundle.go` 에 덧붙인다(import 에 `"sort"` 추가):

```go
// BundleAxis 는 두 항목이 왜 함께 갈 만한가다.
type BundleAxis string

const (
	AxisSibling BundleAxis = "sibling" // 같은 판단에 함께 매달렸다
	AxisAfter   BundleAxis = "after"   // 같은 선행을 기다렸다 / 선행이 선두다
	AxisPaths   BundleAxis = "paths"   // 선언 경로가 정확히 같다 — 보강 전용
)

// Link 는 선두와 이웃 하나 사이의 관계 **전부**다.
//
// ★ 축을 뭉개지 않는다. 뭉개면 "셋 다 맞는 쌍"과 "형제이기만 한 쌍"이 화면에서 같아지고,
// 그러면 사람이 추천을 신뢰할지 판단할 근거를 잃는다.
type Link struct {
	Item   string       // 이웃 항목 id
	Axes   []BundleAxis // 고정 순서: sibling → after → paths
	Detail string       // 무엇이 근거인가 — 판단 id · 선행 좌표 · 겹친 경로
}

// SiblingIndex 는 항목 id → 그 항목에 걸린 판단 id 목록이다.
//
// ★ 슬라이스이고 **사전순으로 정렬돼 있어야 한다**(조립은 service 가 한다).
// 맵으로 두면 공유 판단이 여럿일 때 어느 것이 근거로 찍힐지가 순회 순서에 달리고,
// 그러면 같은 입력에 다른 응답이 나온다 — 재개가 재출력이 아니게 되는 그 결함이다.
type SiblingIndex map[string][]string

// shared 는 두 항목이 함께 매달린 판단 중 **사전순 첫째**를 낸다.
func (x SiblingIndex) shared(a, b string) (string, bool) {
	bs := make(map[string]bool, len(x[b]))
	for _, j := range x[b] {
		bs[j] = true
	}
	for _, j := range x[a] { // a 의 목록이 사전순이므로 결과가 고정된다
		if bs[j] {
			return j, true
		}
	}
	return "", false
}

// afterKey 는 선행 집합의 정규형이다. 순서에 안 흔들린다.
// 빈 문자열은 "선행이 없다"이고, 그것끼리는 같다고 세지 않는다 —
// 선행 없는 항목이 큐의 다수라 그걸 축으로 세면 전부가 서로 묶인다.
func afterKey(as []model.After) string {
	parts := make([]string, 0, len(as))
	for _, a := range as {
		switch {
		case a.Item != "":
			parts = append(parts, "item:"+a.Item)
		case a.SHA != "":
			parts = append(parts, "sha:"+a.SHA)
		case a.Job != "":
			parts = append(parts, "job:"+a.Job)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// LinkOf 는 선두와 이웃 하나의 관계를 낸다. 무관하면 nil 이다.
//
// ★ 결합 규칙: **링크는 (형제 ∨ 같은 선행) 일 때만 선다.**
// 경로 일치는 이미 선 링크의 근거에 덧붙을 뿐 링크를 만들지 못한다.
// 경로 단독을 허용하면 DESIGN.md 처럼 모두가 만지는 파일 하나가 큐를 통째로 묶는다
// (실측: 그 파일 하나로 4건이 서로 묶였다 — 설계 §0.1).
func LinkOf(lead, other Candidate, sib SiblingIndex) *Link {
	l := Link{Item: other.Item.ID}
	var why []string

	if j, ok := sib.shared(lead.Item.ID, other.Item.ID); ok {
		l.Axes = append(l.Axes, AxisSibling)
		why = append(why, "판단 "+j+" 가 둘을 함께 가리킨다")
	}
	if k := afterKey(lead.Item.After); k != "" && k == afterKey(other.Item.After) {
		l.Axes = append(l.Axes, AxisAfter)
		why = append(why, "선행이 같다("+k+")")
	}
	if len(l.Axes) == 0 {
		return nil // 경로는 보강 전용이다 — 여기서 끝낸다
	}
	if same := SamePaths(lead.Item.Paths, other.Item.Paths); len(same) > 0 {
		l.Axes = append(l.Axes, AxisPaths)
		why = append(why, "같은 경로 "+strings.Join(same, ", "))
	}
	l.Detail = strings.Join(why, " · ")
	return &l
}
```

- [ ] **Step 4: 시험이 통과하는지 확인한다**

```
go test ./internal/judge/ -v
```
기대: 이 패키지 전부 PASS(기존 시험 포함 — `Eligible`·`PathsOverlap` 을 안 건드렸으므로 회귀가 없어야 한다)

- [ ] **Step 5: 빨간불을 확인한다**

`LinkOf` 의 `if len(l.Axes) == 0 { return nil }` 를 잠시 지운다(= 경로 단독 허용).

```
go test ./internal/judge/ -run TestLinkOfAxes -v
```
기대: `경로만 같으면 성립하지 않는다` 가 **FAIL**. 확인 후 되돌린다.

- [ ] **Step 6: 커밋**

```bash
git add plugins/flightdeck/server/internal/judge/
git commit -m "$(cat <<'EOF'
feat(judge): LinkOf — 두 항목이 왜 함께 갈 만한가를 축을 뭉개지 않고 낸다

링크는 (형제 ∨ 같은 선행) 일 때만 선다. 경로 일치는 이미 선 링크를
보강할 뿐 링크를 만들지 못한다 — 허용하면 DESIGN.md 처럼 모두가 만지는
파일 하나가 큐를 통째로 묶는다(실측: 그 파일로 4건이 서로 묶였다).

SiblingIndex 를 사전순 슬라이스로 둔다. 맵이면 공유 판단이 여럿일 때
근거 문구가 순회 순서에 흔들리고, 그러면 재개가 재출력이 아니게 된다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: `judge.EligibleBundle` — 방사형 조립과 정렬

후보 각각을 선두로 놓고 **직접 이웃만** 붙인다(전이 없음). 정렬은 키 넷이고 조정 상수가 없다. **흡수는 다음 태스크다** — 이 태스크에서는 `Eligible` 이 적격이라 판정한 것만 묶음에 들어간다.

**Files:**
- Modify: `plugins/flightdeck/server/internal/judge/bundle.go`
- Modify: `plugins/flightdeck/server/internal/judge/bundle_test.go`

**Interfaces:**
- Consumes: `LinkOf`(태스크 2) · `rejectionsFor(c Candidate, in EligibleInput) []model.Rejection`(`eligible.go:119`, 같은 패키지의 비공개 함수) · `lessCandidate`(`eligible.go:178`) · `OverlapsWithLive`(`eligible.go:193`) · `RejectNotTop`(`eligible.go:26`)
- Produces:
  - `type Bundle struct { Lead Candidate; Members []Candidate; Links []Link; Dependents int; Oldest time.Time; Reason string }`
  - `func EligibleBundle(in EligibleInput, sib SiblingIndex) (picked *Bundle, rejected []model.Rejection)`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`bundle_test.go` 에 덧붙인다:

```go
func memberIDs(b *Bundle) []string {
	if b == nil {
		return nil
	}
	out := make([]string, 0, len(b.Members))
	for _, m := range b.Members {
		out = append(out, m.Item.ID)
	}
	return out
}

// 이웃이 없으면 원소 1개짜리 묶음이다 — 단독은 특수 경우가 아니라 상위집합의 밑변이다.
func TestEligibleBundleSoloWhenNoNeighbor(t *testing.T) {
	in := EligibleInput{Self: "S1", Candidates: []Candidate{
		cand("solo", 0, []string{"a.go"}),
	}}
	b, rej := EligibleBundle(in, SiblingIndex{})
	if b == nil {
		t.Fatalf("적격이 있는데 묶음이 nil 이다 (탈락 %v)", rej)
	}
	if b.Lead.Item.ID != "solo" || len(b.Members) != 0 {
		t.Fatalf("단독이어야 하는데 선두 %q 구성원 %v", b.Lead.Item.ID, memberIDs(b))
	}
}

// 전이하지 않는다. A–B, B–C 인데 A–C 가 무관하면 A 선두 묶음에 C 가 없어야 한다.
// 전이를 허용하면 넓은 토큰 하나가 큐의 3분의 2를 한 묶음으로 만든다(설계 §0.1).
func TestEligibleBundleDoesNotTransit(t *testing.T) {
	sib := SiblingIndex{"A": {"J1"}, "B": {"J1", "J2"}, "C": {"J2"}}
	in := EligibleInput{Self: "S1", Candidates: []Candidate{
		cand("A", 0, nil), cand("B", 1, nil), cand("C", 2, nil),
	}}
	b, _ := EligibleBundle(in, sib)
	// 어느 것이 선두든 구성원은 정확히 1건이어야 한다(자기와 직접 이어진 것 하나).
	if len(b.Members) != 1 {
		t.Fatalf("전이가 일어났다 — 선두 %q 구성원 %v", b.Lead.Item.ID, memberIDs(b))
	}
}

// 정렬 키 ②. 이게 없으면 최고령 단독이 항상 이겨 묶음이 영원히 발화하지 않는다.
func TestEligibleBundlePrefersBiggerBundleOverOlderSolo(t *testing.T) {
	sib := SiblingIndex{"y1": {"J1"}, "y2": {"J1"}}
	in := EligibleInput{Self: "S1", Candidates: []Candidate{
		cand("x-oldest-solo", 0, nil), // 가장 오래됨. 이웃 없음
		cand("y1", 10, nil),
		cand("y2", 11, nil),
	}}
	b, _ := EligibleBundle(in, sib)
	if len(b.Members) != 1 {
		t.Fatalf("묶음(2건)이 최고령 단독을 못 이겼다 — 선두 %q 구성원 %v",
			b.Lead.Item.ID, memberIDs(b))
	}
}

// 정렬 키 ①은 여전히 ②보다 앞이다.
func TestEligibleBundleDependentsBeatSize(t *testing.T) {
	sib := SiblingIndex{"y1": {"J1"}, "y2": {"J1"}}
	heavy := cand("heavy-solo", 0, nil)
	heavy.Dependents = 5
	in := EligibleInput{Self: "S1", Candidates: []Candidate{
		heavy, cand("y1", 10, nil), cand("y2", 11, nil),
	}}
	b, _ := EligibleBundle(in, sib)
	if b.Lead.Item.ID != "heavy-solo" {
		t.Fatalf("의존자 합이 크기보다 앞이어야 한다 — 선두가 %q 다", b.Lead.Item.ID)
	}
}

// 키 ①②③이 전부 동점이면 ④(선두 id 사전순)가 브랜치 이름을 정한다.
// 실측의 형제 3건이 정확히 이 경우다 — 생성 시각이 마이크로초까지 같다.
func TestEligibleBundleTieBreaksByLeadID(t *testing.T) {
	sib := SiblingIndex{"m-mid": {"J1"}, "a-first": {"J1"}, "z-last": {"J1"}}
	in := EligibleInput{Self: "S1", Candidates: []Candidate{
		cand("m-mid", 0, nil), cand("z-last", 0, nil), cand("a-first", 0, nil),
	}}
	b, _ := EligibleBundle(in, sib)
	if b.Lead.Item.ID != "a-first" {
		t.Fatalf("동점에서 선두 id 사전순이어야 한다 — 선두가 %q 다", b.Lead.Item.ID)
	}
	if len(b.Members) != 2 {
		t.Fatalf("형제 셋이 한 묶음이어야 한다 — 구성원 %v", memberIDs(b))
	}
}

// 불변식: 모든 후보는 picked 이거나 rejected 에 최소 한 줄.
// 조용히 사라지는 것이 하나도 없어야 큐가 블랙박스가 안 된다.
func TestEligibleBundleLedgersEveryCandidate(t *testing.T) {
	sib := SiblingIndex{"y1": {"J1"}, "y2": {"J1"}}
	in := EligibleInput{Self: "S1", Candidates: []Candidate{
		cand("y1", 0, nil), cand("y2", 1, nil),
		cand("lonely", 5, nil),
		{Item: openItem("taken", 2), ClaimedBy: "S2"},
	}}
	b, rej := EligibleBundle(in, sib)
	inBundle := map[string]bool{b.Lead.Item.ID: true}
	for _, id := range memberIDs(b) {
		inBundle[id] = true
	}
	ledger := itemIDs(rej)
	for _, id := range []string{"y1", "y2", "lonely", "taken"} {
		if !inBundle[id] && !ledger[id] {
			t.Fatalf("후보 %q 가 묶음에도 원장에도 없다", id)
		}
		if inBundle[id] && ledger[id] {
			t.Fatalf("후보 %q 가 묶음에도 원장에도 있다 — 두 번 셌다", id)
		}
	}
	if !contains(codesFor(rej, "lonely"), RejectNotTop) {
		t.Fatalf("적격이지만 안 뽑힌 항목의 사유가 %v 다", codesFor(rej, "lonely"))
	}
	if !contains(codesFor(rej, "taken"), RejectClaimed) {
		t.Fatalf("남이 선점한 항목의 사유가 %v 다", codesFor(rej, "taken"))
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// 적격 0건이면 nil 을 내되 사유는 전부 낸다.
func TestEligibleBundleNoneKeepsEveryReason(t *testing.T) {
	in := EligibleInput{Self: "S1", Candidates: []Candidate{
		{Item: openItem("taken", 0), ClaimedBy: "S2"},
		{Item: model.Item{ID: "done", State: model.ItemDone, CreatedAt: t0}},
	}}
	b, rej := EligibleBundle(in, SiblingIndex{})
	if b != nil {
		t.Fatalf("적격이 없는데 묶음이 나왔다: %q", b.Lead.Item.ID)
	}
	if len(itemIDs(rej)) != 2 {
		t.Fatalf("탈락 원장에 2건이 있어야 한다: %v", rej)
	}
}

// 겹침은 묶음 **전체 경로**로 본다 — 남과 부딪히는지는 묶음 단위 질문이다.
func TestEligibleBundleOverlapsUseWholeBundlePaths(t *testing.T) {
	sib := SiblingIndex{"lead": {"J1"}, "mem": {"J1"}}
	in := EligibleInput{
		Self: "S1",
		Candidates: []Candidate{
			cand("lead", 0, []string{"lead-only.go"}),
			cand("mem", 1, []string{"member-only.go"}),
		},
		Live: []LiveSession{{ID: "S2", Paths: []string{"member-only.go"}}},
	}
	b, _ := EligibleBundle(in, sib)
	if len(b.Lead.Overlaps) == 0 {
		t.Fatal("구성원의 경로로 난 겹침이 안 잡혔다 — 겹침을 선두 경로로만 봤다")
	}
}

// 사유가 없으면 "왜 저것이 아니라 이것인가"에 답할 수 없다.
func TestEligibleBundleCarriesReason(t *testing.T) {
	sib := SiblingIndex{"y1": {"J1"}, "y2": {"J1"}}
	in := EligibleInput{Self: "S1", Candidates: []Candidate{cand("y1", 0, nil), cand("y2", 1, nil)}}
	b, _ := EligibleBundle(in, sib)
	for _, want := range []string{"의존자", "묶음", "최고령"} {
		if !strings.Contains(b.Reason, want) {
			t.Fatalf("사유에 %q 가 없다: %q", want, b.Reason)
		}
	}
}
```

- [ ] **Step 2: 시험이 실패하는지 확인한다**

```
go test ./internal/judge/ -run TestEligibleBundle -v
```
기대: 컴파일 실패 — `undefined: EligibleBundle`

- [ ] **Step 3: 구현을 쓴다**

`bundle.go` 에 덧붙인다(import 에 `"fmt"`, `"time"` 추가):

```go
// Bundle 은 pick 한 번이 제안하는 집합이다. **저장되지 않는다** —
// 테이블도 id 도 상태도 없다. 저장하면 개념이 하나 늘고,
// 그 순간 "묶음이 깨졌다"·"묶음을 해체한다" 같은 상태 전이가 따라온다.
type Bundle struct {
	Lead    Candidate
	Members []Candidate // 선두 제외. lessCandidate 로 정렬
	Links   []Link      // Members 와 같은 순서·같은 길이
	// Dependents 는 구성원 전부의 합이다. 이걸 풀어야 남이 움직이는 정도.
	Dependents int
	// Oldest 는 가장 오래된 구성원의 생성 시각이다.
	Oldest time.Time
	// Reason 은 네 키의 **실제 값**이다. 감추면 "왜 하필 이 브랜치 이름인가"에
	// 답할 수 없고, 답 못 하는 자동 선택은 두 번째 세션부터 무시된다.
	Reason string
}

// EligibleBundle 은 Eligible 위에 얹는다.
//
// 적격 후보 **각각을 선두로** 놓고 방사형으로 이웃을 붙인 뒤 §2.4 의 키 넷으로 정렬해
// 1순위를 낸다. **전이하지 않는다** — 이웃의 이웃은 안 들어온다.
//
// Eligible 을 안 고치고 그 위에 얹는 이유는, 시험이 단일 추천 규칙을 독립으로
// 계속 부를 수 있어야 하기 때문이다. 묶음 판정이 그 규칙의 사본을 만들면
// 두 규칙이 조용히 표류한다.
func EligibleBundle(in EligibleInput, sib SiblingIndex) (*Bundle, []model.Rejection) {
	var fit []Candidate
	var rejected []model.Rejection
	for _, c := range in.Candidates {
		rs := rejectionsFor(c, in)
		if len(rs) == 0 {
			fit = append(fit, c)
			continue
		}
		rejected = append(rejected, rs...)
	}
	if len(fit) == 0 {
		return nil, rejected
	}
	sort.SliceStable(fit, func(i, j int) bool { return lessCandidate(fit[i], fit[j]) })

	bundles := make([]Bundle, 0, len(fit))
	for _, lead := range fit {
		bundles = append(bundles, bundleAround(lead, fit, sib))
	}
	sort.SliceStable(bundles, func(i, j int) bool { return lessBundle(bundles[i], bundles[j]) })

	best := bundles[0]
	best.Lead.Overlaps = OverlapsWithLive(bundlePaths(best), in.Live, in.Self)

	// 적격이었으나 이 묶음에 못 든 것도 원장에 남긴다. 안 남기면
	// pick_eval 어디에도 없어 "왜 저것이 아니라 이것인가"에 답할 수 없다.
	picked := map[string]bool{best.Lead.Item.ID: true}
	for _, m := range best.Members {
		picked[m.Item.ID] = true
	}
	for _, c := range fit {
		if picked[c.Item.ID] {
			continue
		}
		rejected = append(rejected, model.Rejection{Item: c.Item.ID, Reason: RejectNotTop,
			Detail: fmt.Sprintf("적격이지만 추천 묶음에 없다(추천 선두는 %s, 묶음 %d건)",
				best.Lead.Item.ID, len(best.Members)+1)})
	}
	return &best, rejected
}

// bundleAround 는 선두 하나를 중심으로 직접 이웃만 모은다.
func bundleAround(lead Candidate, fit []Candidate, sib SiblingIndex) Bundle {
	b := Bundle{Lead: lead, Dependents: lead.Dependents, Oldest: lead.Item.CreatedAt}
	for _, c := range fit {
		if c.Item.ID == lead.Item.ID {
			continue
		}
		l := LinkOf(lead, c, sib)
		if l == nil {
			continue
		}
		b.Members = append(b.Members, c)
		b.Links = append(b.Links, *l)
		b.Dependents += c.Dependents
		if c.Item.CreatedAt.Before(b.Oldest) {
			b.Oldest = c.Item.CreatedAt
		}
	}
	// fit 이 이미 lessCandidate 로 정렬돼 있어 Members·Links 도 그 순서를 물려받는다.
	b.Reason = fmt.Sprintf("의존자 합 %d · 묶음 %d건 · 최고령 %s · 선두 %s",
		b.Dependents, len(b.Members)+1, b.Oldest.UTC().Format("2006-01-02 15:04"), lead.Item.ID)
	return b
}

// lessBundle 은 추천 순서다. 조정할 상수가 하나도 없다.
//
//	① 의존자 수 합 ↓ — 이걸 풀어야 남이 움직이는 정도
//	② 묶음 크기   ↓ — 한 번에 더 많이 푸는 쪽이 이긴다
//	③ 최고령      ↑ — 오래 방치된 것을 먼저
//	④ 선두 id     사전순 — 동점 처리. 없으면 같은 입력에 다른 답이 나온다
//
// ★ ②가 없으면 이 기능이 **발화하지 않는다.** 실측에서 열린 16건 전부 의존자 0이라
// ①이 상수이고, 그 상태에서 ③이 실질 1차 키가 되는데 최고령이 단독이었다(설계 §0.2).
func lessBundle(a, b Bundle) bool {
	if a.Dependents != b.Dependents {
		return a.Dependents > b.Dependents
	}
	if len(a.Members) != len(b.Members) {
		return len(a.Members) > len(b.Members)
	}
	if !a.Oldest.Equal(b.Oldest) {
		return a.Oldest.Before(b.Oldest)
	}
	return a.Lead.Item.ID < b.Lead.Item.ID
}

// bundlePaths 는 묶음 전체가 만지는 경로다.
// 겹침("남과 부딪히나")은 묶음 단위 질문이므로 합집합으로 본다.
func bundlePaths(b Bundle) []string {
	seen := map[string]bool{}
	var out []string
	add := func(ps []string) {
		for _, p := range ps {
			if n := normPath(p); n != "" && !seen[n] {
				seen[n] = true
				out = append(out, p)
			}
		}
	}
	add(b.Lead.Item.Paths)
	for _, m := range b.Members {
		add(m.Item.Paths)
	}
	return out
}
```

- [ ] **Step 4: 시험이 통과하는지 확인한다**

```
go test ./internal/judge/ -v
```
기대: 전부 PASS

- [ ] **Step 5: 빨간불을 확인한다 — 정렬 키 ②를 뺀다**

`lessBundle` 의 `if len(a.Members) != len(b.Members)` 블록을 잠시 지운다.

```
go test ./internal/judge/ -run TestEligibleBundlePrefersBiggerBundleOverOlderSolo -v
```
기대: **FAIL**. 이 빨간불이 안 나오면 그 시험은 설계 §0.2 를 안 지키고 있는 것이다. 확인 후 되돌린다.

- [ ] **Step 6: 커밋**

```bash
git add plugins/flightdeck/server/internal/judge/
git commit -m "$(cat <<'EOF'
feat(judge): EligibleBundle — 후보 각각을 선두로 놓고 직접 이웃만 묶는다

전이하지 않는다. 전이를 허용하면 넓은 디렉토리 토큰 하나가 큐의 3분의 2를
한 묶음으로 만든다.

정렬 키는 넷이고 조정 상수가 없다: 의존자 합 → 묶음 크기 → 최고령 → 선두 id.
묶음 크기가 없으면 이 기능이 발화하지 않는다 — 실측에서 열린 16건 전부
의존자 0이라 1차 키가 상수이고, 그때 최고령이 실질 1차 키가 되는데
최고령이 단독이었다.

불변식 유지: 모든 후보는 picked 이거나 rejected 에 최소 한 줄.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: 흡수 — 선행이 선두일 때, 그리고 `after-unmet-item` 일 때만

`Eligible` 이 탈락시킨 항목을 묶음에 되살리는 **유일한** 경우다. 아홉 개 선행 사유 코드 중 흡수 가능한 것은 하나뿐이라, 이 태스크의 시험은 나머지 여덟이 **안** 들어오는 것을 코드별로 못박는다.

**Files:**
- Modify: `plugins/flightdeck/server/internal/judge/bundle.go`
- Modify: `plugins/flightdeck/server/internal/judge/bundle_test.go`

**Interfaces:**
- Consumes: `AfterUnmetItem`·`AfterDroppedDep`·`AfterUnknown`·`AfterBadRef`(`after.go:55-63`) · `rejectionsFor`
- Produces: (공개 시그니처 변경 없음 — `EligibleBundle` 의 동작만 넓어진다)

- [ ] **Step 1: 실패하는 시험을 쓴다**

`bundle_test.go` 에 덧붙인다:

```go
// 흡수의 유일한 경우. A 의 선행이 B 이고 B 가 선두면 한 브랜치에서 B→A 로 하면
// 랜딩을 안 기다린다.
func TestEligibleBundleAbsorbsBlockedItemWhenBlockerIsLead(t *testing.T) {
	blocker := cand("B-blocker", 0, nil)
	blocked := cand("A-blocked", 1, nil, afterItem("B-blocker"))
	in := EligibleInput{
		Self:       "S1",
		Candidates: []Candidate{blocker, blocked},
		Facts:      AfterFacts{ItemStates: map[string]model.ItemState{"B-blocker": model.ItemOpen}},
	}
	b, rej := EligibleBundle(in, SiblingIndex{})
	if b == nil {
		t.Fatalf("묶음이 nil 이다 (탈락 %v)", rej)
	}
	if b.Lead.Item.ID != "B-blocker" {
		t.Fatalf("선행이 선두여야 한다 — 선두가 %q 다", b.Lead.Item.ID)
	}
	if !contains(memberIDs(b), "A-blocked") {
		t.Fatalf("막힌 항목이 흡수되지 않았다 — 구성원 %v", memberIDs(b))
	}
	// 흡수된 항목은 picked 이므로 원장에서 빠져야 한다. 두 번 세면 불변식이 깨진다.
	if itemIDs(rej)["A-blocked"] {
		t.Fatalf("흡수된 항목이 탈락 원장에도 있다: %v", rej)
	}
	// 그런데 왜 들어왔는지는 남아야 한다.
	for i, m := range b.Members {
		if m.Item.ID == "A-blocked" && !strings.Contains(b.Links[i].Detail, "B-blocker") {
			t.Fatalf("흡수 근거에 선행 좌표가 없다: %q", b.Links[i].Detail)
		}
	}
}

// 선행이 묶음 밖이면 흡수하지 않는다. 밖의 것을 기다려야 하는 사실이 안 바뀐다.
func TestEligibleBundleDoesNotAbsorbWhenBlockerIsNotInBundle(t *testing.T) {
	in := EligibleInput{
		Self: "S1",
		Candidates: []Candidate{
			cand("A-blocked", 0, nil, afterItem("outside")),
			cand("Z-unrelated", 1, nil),
		},
		Facts: AfterFacts{ItemStates: map[string]model.ItemState{"outside": model.ItemOpen}},
	}
	b, rej := EligibleBundle(in, SiblingIndex{})
	if contains(memberIDs(b), "A-blocked") {
		t.Fatalf("묶음 밖 선행인데 흡수했다 — 구성원 %v", memberIDs(b))
	}
	if !contains(codesFor(rej, "A-blocked"), AfterUnmetItem) {
		t.Fatalf("막힌 항목의 사유가 원장에 없다: %v", codesFor(rej, "A-blocked"))
	}
}

// 선행이 둘인데 하나만 묶음 안이면 흡수하지 않는다.
func TestEligibleBundleNeedsEveryBlockerInBundle(t *testing.T) {
	in := EligibleInput{
		Self: "S1",
		Candidates: []Candidate{
			cand("B-blocker", 0, nil),
			cand("A-blocked", 1, nil, afterItem("B-blocker"), afterItem("outside")),
		},
		Facts: AfterFacts{ItemStates: map[string]model.ItemState{
			"B-blocker": model.ItemOpen, "outside": model.ItemOpen,
		}},
	}
	b, _ := EligibleBundle(in, SiblingIndex{})
	if contains(memberIDs(b), "A-blocked") {
		t.Fatalf("선행 하나가 묶음 밖인데 흡수했다 — 구성원 %v", memberIDs(b))
	}
}

// ★ 이 시험이 이 태스크의 핵심이다.
// 아홉 코드 중 흡수 가능한 것은 after-unmet-item 하나뿐이다.
// 나머지를 흡수하면 "모르는 것"이나 "영영 안 풀리는 것"이 충족으로 접힌다.
func TestEligibleBundleAbsorbsOnlyUnmetItem(t *testing.T) {
	cases := []struct {
		name  string
		facts AfterFacts
		code  string
	}{
		{"폐기된 선행은 흡수 불가 — 영영 안 풀린다",
			AfterFacts{ItemStates: map[string]model.ItemState{"B-blocker": model.ItemDropped}},
			AfterDroppedDep},
		{"조회 못 한 선행은 흡수 불가 — 판정 자체를 안 했다",
			AfterFacts{ItemStates: map[string]model.ItemState{}}, // 키 부재 = after-unknown
			AfterUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := EligibleInput{
				Self: "S1",
				Candidates: []Candidate{
					cand("B-blocker", 0, nil),
					cand("A-blocked", 1, nil, afterItem("B-blocker")),
				},
				Facts: tc.facts,
			}
			b, rej := EligibleBundle(in, SiblingIndex{})
			if contains(memberIDs(b), "A-blocked") {
				t.Fatalf("%s 인데 흡수했다 — 구성원 %v", tc.code, memberIDs(b))
			}
			if !contains(codesFor(rej, "A-blocked"), tc.code) {
				t.Fatalf("사유 코드 %q 가 원장에 없다: %v", tc.code, codesFor(rej, "A-blocked"))
			}
		})
	}
}

// sha 선행은 흡수 대상이 아니다 — 이 세션이 만들 수 없는 사실을 기다린다.
func TestEligibleBundleDoesNotAbsorbSHABlocked(t *testing.T) {
	in := EligibleInput{
		Self: "S1",
		Candidates: []Candidate{
			cand("B-lead", 0, nil),
			cand("A-blocked", 1, nil, afterSHA("deadbee")),
		},
		Facts: AfterFacts{SHAAncestry: map[string]AncestryResult{}},
	}
	b, _ := EligibleBundle(in, SiblingIndex{})
	if contains(memberIDs(b), "A-blocked") {
		t.Fatalf("sha 선행을 흡수했다 — 구성원 %v", memberIDs(b))
	}
}
```

> `AncestryResult` 의 정확한 형태는 `internal/judge/after.go` 에서 확인하고, 위 시험에서 빈 맵으로 두면 키 부재(=`after-unknown`)가 되는지 그 파일의 술어로 다시 읽어라. 코드가 다르면 시험의 기대 코드를 실물에 맞춰라 — **기대를 실물에 맞추되, 흡수하지 않는다는 단정은 바꾸지 마라.**

- [ ] **Step 2: 시험이 실패하는지 확인한다**

```
go test ./internal/judge/ -run 'TestEligibleBundleAbsorbs|TestEligibleBundleDoesNotAbsorb|TestEligibleBundleNeedsEvery' -v
```
기대: `TestEligibleBundleAbsorbsBlockedItemWhenBlockerIsLead` 가 FAIL(막힌 항목이 흡수되지 않았다). 나머지는 이미 PASS 여야 한다 — **지금은 아무것도 흡수하지 않으니까.**

- [ ] **Step 3: 구현을 쓴다**

`bundle.go` 의 `EligibleBundle` 을 고친다. 탈락 사유를 항목별로 들고 있어야 흡수 뒤 원장에서 뺄 수 있다:

```go
func EligibleBundle(in EligibleInput, sib SiblingIndex) (*Bundle, []model.Rejection) {
	var fit []Candidate
	byID := make(map[string]Candidate, len(in.Candidates))
	rejByItem := make(map[string][]model.Rejection, len(in.Candidates))
	order := make([]string, 0, len(in.Candidates))

	for _, c := range in.Candidates {
		byID[c.Item.ID] = c
		order = append(order, c.Item.ID)
		rs := rejectionsFor(c, in)
		if len(rs) == 0 {
			fit = append(fit, c)
			continue
		}
		rejByItem[c.Item.ID] = rs
	}
	if len(fit) == 0 {
		return nil, flatten(order, rejByItem)
	}
	sort.SliceStable(fit, func(i, j int) bool { return lessCandidate(fit[i], fit[j]) })

	// 흡수 후보: 사유가 **전부** after-unmet-item 인 항목만.
	absorbable := map[string]Candidate{}
	for id, rs := range rejByItem {
		all := true
		for _, r := range rs {
			if r.Reason != AfterUnmetItem {
				all = false
				break
			}
		}
		if all && len(rs) > 0 {
			absorbable[id] = byID[id]
		}
	}

	bundles := make([]Bundle, 0, len(fit))
	for _, lead := range fit {
		bundles = append(bundles, bundleAround(lead, fit, absorbable, sib))
	}
	sort.SliceStable(bundles, func(i, j int) bool { return lessBundle(bundles[i], bundles[j]) })

	best := bundles[0]
	best.Lead.Overlaps = OverlapsWithLive(bundlePaths(best), in.Live, in.Self)

	picked := map[string]bool{best.Lead.Item.ID: true}
	for _, m := range best.Members {
		picked[m.Item.ID] = true
		delete(rejByItem, m.Item.ID) // 흡수됐으면 원장에서 뺀다 — picked 이므로
	}
	for _, c := range fit {
		if picked[c.Item.ID] {
			continue
		}
		rejByItem[c.Item.ID] = append(rejByItem[c.Item.ID], model.Rejection{
			Item: c.Item.ID, Reason: RejectNotTop,
			Detail: fmt.Sprintf("적격이지만 추천 묶음에 없다(추천 선두는 %s, 묶음 %d건)",
				best.Lead.Item.ID, len(best.Members)+1)})
	}
	return &best, flatten(order, rejByItem)
}

// flatten 은 항목별 사유를 **입력 순서대로** 편다.
// 맵을 그대로 순회하면 같은 입력에 사유 순서가 흔들리고,
// 그러면 pick_eval 로 쌓인 분포를 시점 간에 비교할 수 없다.
func flatten(order []string, byItem map[string][]model.Rejection) []model.Rejection {
	var out []model.Rejection
	for _, id := range order {
		out = append(out, byItem[id]...)
	}
	return out
}
```

`bundleAround` 를 고쳐 흡수 후보를 받게 한다:

```go
func bundleAround(lead Candidate, fit []Candidate, absorbable map[string]Candidate, sib SiblingIndex) Bundle {
	b := Bundle{Lead: lead, Dependents: lead.Dependents, Oldest: lead.Item.CreatedAt}
	add := func(c Candidate, l Link) {
		b.Members = append(b.Members, c)
		b.Links = append(b.Links, l)
		b.Dependents += c.Dependents
		if c.Item.CreatedAt.Before(b.Oldest) {
			b.Oldest = c.Item.CreatedAt
		}
	}
	for _, c := range fit {
		if c.Item.ID == lead.Item.ID {
			continue
		}
		if l := LinkOf(lead, c, sib); l != nil {
			add(c, *l)
		}
	}
	// 흡수 — 선두가 그 항목의 **미충족 선행 전부**여야 한다.
	// 하나라도 밖에 있으면 밖의 것을 기다려야 하는 사실이 안 바뀐다.
	for _, c := range sortedCands(absorbable) {
		if blockedOnlyBy(c, lead.Item.ID) {
			add(c, Link{Item: c.Item.ID, Axes: []BundleAxis{AxisAfter},
				Detail: fmt.Sprintf("선행 %s 를 같은 묶음이 함께 한다 — 랜딩을 안 기다린다", lead.Item.ID)})
		}
	}
	b.Reason = fmt.Sprintf("의존자 합 %d · 묶음 %d건 · 최고령 %s · 선두 %s",
		b.Dependents, len(b.Members)+1, b.Oldest.UTC().Format("2006-01-02 15:04"), lead.Item.ID)
	return b
}

// blockedOnlyBy 는 이 항목의 선행이 **정확히 그 항목 하나**뿐인지 본다.
// 항목 선행이 여럿이면 전부 묶음 안이어야 하는데, 방사형 묶음의 구성원은
// 선두와만 직접 이어지므로 "전부"가 성립하는 경우가 곧 "하나뿐"이다.
func blockedOnlyBy(c Candidate, leadID string) bool {
	n := 0
	for _, a := range c.Item.After {
		if a.Item == "" {
			return false // sha·job 선행이 섞여 있으면 흡수하지 않는다
		}
		if a.Item != leadID {
			return false
		}
		n++
	}
	return n > 0
}

// sortedCands 는 맵을 id 사전순으로 편다. 맵 순회는 순서가 흔들린다.
func sortedCands(m map[string]Candidate) []Candidate {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Candidate, 0, len(ids))
	for _, id := range ids {
		out = append(out, m[id])
	}
	return out
}
```

- [ ] **Step 4: 시험이 통과하는지 확인한다**

```
go test ./internal/judge/ -v
```
기대: 전부 PASS

- [ ] **Step 5: 빨간불을 확인한다 — 코드 가드를 푼다**

`absorbable` 조립에서 `r.Reason != AfterUnmetItem` 조건을 잠시 `false` 로 바꾼다(= 모든 탈락 항목을 흡수 후보로).

```
go test ./internal/judge/ -run TestEligibleBundleAbsorbsOnlyUnmetItem -v
```
기대: 두 하위 시험 다 **FAIL**. 확인 후 되돌린다.

- [ ] **Step 6: 커밋**

```bash
git add plugins/flightdeck/server/internal/judge/
git commit -m "$(cat <<'EOF'
feat(judge): 흡수 — 선행이 선두이고 사유가 after-unmet-item 일 때만

Eligible 이 탈락시킨 항목을 묶음에 되살리는 유일한 경로다. 아홉 사유 코드 중
흡수 가능한 것은 after-unmet-item("기다리면 풀린다") 하나뿐이다.
after-dropped-dep 는 영영 안 풀리고 after-unknown 은 조회 자체를 못 한 것이라,
흡수하면 모르는 것이 충족으로 접힌다.

흡수된 항목은 picked 이므로 탈락 원장에서 뺀다. 왜 들어왔는지는 링크 근거에
남는다. 불변식은 유지된다 — 모든 후보는 picked 이거나 rejected 에 최소 한 줄.

사유는 입력 순서대로 편다. 맵 순회로 내면 같은 입력에 사유 순서가 흔들려
pick_eval 분포를 시점 간에 비교할 수 없다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: `store.JudgmentLinksForItems` — 인덱스는 있고 접근자만 없었다

`judgment_link_by_target` 인덱스는 `schema.sql:261` 에 이미 있다. `service/pick.go:520-522` 가 접근자가 없다는 사실을 주석으로 적어 두고 종류 9개를 훑는 방식으로 우회하고 있다. 묶음 N건이면 그것이 **N×9 질의**가 된다 — 이 기능이 만든 비용이므로 여기서 함께 고친다.

**Files:**
- Modify: `plugins/flightdeck/server/internal/store/judgment.go`
- Modify: `plugins/flightdeck/server/internal/service/pick.go` (`linkedJudgments`)
- Create: `plugins/flightdeck/server/internal/store/judgment_links_test.go`

**Interfaces:**
- Consumes: `s.db`(`dbtx`) · `judgmentCols`·`collectJudgments`·`fillLinks`(`judgment.go` 안)
- Produces:
  - `func (s *Store) JudgmentLinksForItems(ctx context.Context, project string, itemIDs []string) (map[string][]string, error)` — 항목 id → **사전순** 판단 id 목록. 링크가 없는 항목은 키가 없다
  - `func (s *Store) JudgmentsForItem(ctx context.Context, project, itemID string) ([]model.Judgment, error)` — 그 항목에 걸린 판단 전문. 최신 먼저, 동점이면 id 역순

- [ ] **Step 1: 실패하는 시험을 쓴다**

`plugins/flightdeck/server/internal/store/judgment_links_test.go`:

```go
package store

import (
	"context"
	"reflect"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// linkJudgment 는 판단 하나를 항목들에 매단다. finish 가 만드는 모양 그대로다
// (finish.go:148-152 가 끝낸 항목과 후속 전부를 한 handoff 판단에 매단다).
func linkJudgment(t *testing.T, s *Store, project string, kind model.JudgmentKind, items ...string) string {
	t.Helper()
	links := make([]model.JudgmentLink, 0, len(items))
	for _, it := range items {
		links = append(links, model.JudgmentLink{TargetKind: "item", TargetID: it})
	}
	j, err := s.AddJudgment(context.Background(), model.Judgment{
		Project: project, Kind: kind, Body: "본문", Links: links,
	})
	if err != nil {
		t.Fatalf("판단 저장 실패(%v): %v", items, err)
	}
	return j.ID
}

func TestJudgmentLinksForItemsGroupsByItem(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "P")

	mustItem(t, s, "P", "a")
	mustItem(t, s, "P", "b")
	mustItem(t, s, "P", "c")

	// J1 이 a·b 를 함께 가리킨다 = a 와 b 는 형제다.
	linkJudgment(t, s, "P", model.JudgmentHandoff, "a", "b")
	// J2 는 a 만.
	linkJudgment(t, s, "P", model.JudgmentAsk, "a")

	got, err := s.JudgmentLinksForItems(ctx, "P", []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("조회 실패: %v", err)
	}
	if len(got["a"]) != 2 {
		t.Fatalf("a 의 판단이 2건이어야 한다: %v", got["a"])
	}
	if len(got["b"]) != 1 {
		t.Fatalf("b 의 판단이 1건이어야 한다: %v", got["b"])
	}
	// 링크 없는 항목은 **키가 없다** — 빈 슬라이스를 넣으면 "없다"와 "안 봤다"가 접힌다.
	if _, ok := got["c"]; ok {
		t.Fatalf("링크 없는 항목에 키가 생겼다: %v", got["c"])
	}
	// a 와 b 가 같은 판단을 공유하는지가 형제 축의 전부다.
	shared := false
	for _, ja := range got["a"] {
		for _, jb := range got["b"] {
			if ja == jb {
				shared = true
			}
		}
	}
	if !shared {
		t.Fatalf("a·b 가 공유하는 판단이 없다: a=%v b=%v", got["a"], got["b"])
	}
}

// 사전순이어야 SiblingIndex 의 근거 문구가 흔들리지 않는다.
func TestJudgmentLinksForItemsSortsIDs(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "P")
	mustItem(t, s, "P", "x")
	for i := 0; i < 5; i++ {
		linkJudgment(t, s, "P", model.JudgmentAsk, "x")
	}
	got, err := s.JudgmentLinksForItems(ctx, "P", []string{"x"})
	if err != nil {
		t.Fatalf("조회 실패: %v", err)
	}
	sorted := append([]string(nil), got["x"]...)
	for i := 1; i < len(sorted); i++ {
		if sorted[i-1] > sorted[i] {
			t.Fatalf("사전순이 아니다: %v", got["x"])
		}
	}
	if !reflect.DeepEqual(got["x"], sorted) {
		t.Fatalf("정렬이 안 됐다: %v", got["x"])
	}
}

// 다른 프로젝트의 판단이 새면 안 된다.
func TestJudgmentLinksForItemsIsProjectScoped(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "P")
	seed(t, s, "Q")
	mustItem(t, s, "P", "same-id")
	mustItem(t, s, "Q", "same-id")
	linkJudgment(t, s, "Q", model.JudgmentAsk, "same-id")

	got, err := s.JudgmentLinksForItems(ctx, "P", []string{"same-id"})
	if err != nil {
		t.Fatalf("조회 실패: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("다른 프로젝트의 판단이 샜다: %v", got)
	}
}

// 빈 입력에 질의를 쏘지 않는다. IN () 는 SQLite 구문 오류다.
func TestJudgmentLinksForItemsEmptyInput(t *testing.T) {
	s := newStore(t)
	got, err := s.JudgmentLinksForItems(context.Background(), "P", nil)
	if err != nil {
		t.Fatalf("빈 입력에서 오류가 났다: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("빈 입력에 결과가 있다: %v", got)
	}
}
```

> 쓰는 헬퍼는 이 패키지에 이미 있다: `newStore`(`store_test.go:24`) · `seed`(project·machine 등록) · `mustItem`. `linkJudgment` 만 새로 만든다.

- [ ] **Step 2: 시험이 실패하는지 확인한다**

```
go test ./internal/store/ -run TestJudgmentLinksForItems -v
```
기대: 컴파일 실패 — `st.JudgmentLinksForItems undefined`

- [ ] **Step 3: 구현을 쓴다**

`internal/store/judgment.go` 끝에 덧붙인다:

```go
// JudgmentLinksForItems 는 항목 id 들에 걸린 판단 id 를 한 번에 읽는다.
//
// ★ judgment_link_by_target 인덱스(schema.sql:261)는 처음부터 있었고 접근자만 없었다.
// 그래서 service/pick.go 가 종류 9개를 훑어 링크로 거르는 방식으로 우회했는데,
// 묶음 N건이면 그것이 N×9 질의가 된다.
//
// 링크가 없는 항목은 **키를 안 만든다.** 빈 슬라이스를 넣으면
// "이 항목에 판단이 없다"와 "이 항목을 안 봤다"가 같은 값이 된다.
func (s *Store) JudgmentLinksForItems(ctx context.Context, project string, itemIDs []string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(itemIDs) == 0 {
		return out, nil // 질의를 쏘지 않는다. IN () 는 SQLite 구문 오류다
	}
	ph := make([]string, len(itemIDs))
	args := make([]any, 0, len(itemIDs)+1)
	args = append(args, project)
	for i, id := range itemIDs {
		ph[i] = "?"
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT jl.target_id, jl.judgment_id
		   FROM judgment_link jl JOIN judgment j ON j.id = jl.judgment_id
		  WHERE j.project = ? AND jl.target_kind = 'item'
		    AND jl.target_id IN (`+strings.Join(ph, ",")+`)
		  ORDER BY jl.target_id, jl.judgment_id`,
		args...)
	if err != nil {
		return nil, fmt.Errorf("항목별 판단 링크 조회 실패(project=%q, 항목 %d건): %w",
			clip(project, 64), len(itemIDs), err)
	}
	defer rows.Close()
	for rows.Next() {
		var item, jid string
		if err := rows.Scan(&item, &jid); err != nil {
			return nil, fmt.Errorf("판단 링크 행 해석 실패: %w", err)
		}
		out[item] = append(out[item], jid) // ORDER BY 가 사전순을 보장한다
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("판단 링크 순회 실패: %w", err)
	}
	return out, nil
}

// JudgmentsForItem 은 항목 하나에 걸린 판단 **전문**이다. 최신 먼저, 동점이면 id 역순.
//
// service/pick.go 의 linkedJudgments 가 종류 9개를 훑던 자리를 대신한다.
func (s *Store) JudgmentsForItem(ctx context.Context, project, itemID string) ([]model.Judgment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+judgmentCols+`
		   FROM judgment j JOIN judgment_link jl ON jl.judgment_id = j.id
		  WHERE j.project = ? AND jl.target_kind = 'item' AND jl.target_id = ?
		  ORDER BY j.at DESC, j.id DESC`,
		project, itemID)
	if err != nil {
		return nil, fmt.Errorf("항목의 판단 조회 실패(project=%q item=%q): %w",
			clip(project, 64), clip(itemID, 64), err)
	}
	js, err := collectJudgments(rows)
	if err != nil {
		return nil, err
	}
	return s.fillLinks(ctx, js)
}
```

> `judgmentCols` 가 `j.` 접두 없이 컬럼을 나열한다면 위 질의에서 모호성 오류가 난다. 그 경우 `judgmentCols` 를 그대로 쓰되 `FROM judgment j` 를 `FROM judgment` 로 바꾸고 조인을 `jl.judgment_id = judgment.id` 로 써라. 빌드가 알려 준다.

`internal/service/pick.go` 의 `linkedJudgments` 를 통째로 교체한다(시그니처는 그대로):

```go
// linkedJudgments 는 항목 하나에 연결된 판단 전문이다.
//
// 앞 판은 저장 계층에 "링크 대상으로 찾기" 조회가 없어 **종류 9개를 훑어 링크로 걸렀다**.
// 항목 하나에 질의 9회였고, 묶음이 들어오면서 N×9 가 됐다.
// 접근자(store.JudgmentsForItem)를 만들어 항목당 1회로 줄인다.
func (s *Service) linkedJudgments(ctx context.Context, project, itemID string) ([]model.Judgment, error) {
	return s.st.JudgmentsForItem(ctx, project, itemID)
}
```

`pick.go` 에서 이제 안 쓰는 import(`sort` 를 다른 데서 안 쓰면)가 생기면 지운다. `candidates` 가 `sort.Slice` 를 쓰므로 대개 남는다.

- [ ] **Step 4: 시험이 통과하는지 확인한다**

```
go test ./internal/store/ ./internal/service/ -v 2>&1 | tail -40
```
기대: 두 패키지 전부 PASS. 특히 `internal/service` 의 기존 pick 시험 중 연결된 판단을 단정하는 것이 **그대로 초록**이어야 한다 — 그것이 이 교체가 동작을 안 바꿨다는 증거다.

- [ ] **Step 5: 빨간불을 확인한다**

`JudgmentLinksForItems` 의 `AND j.project = ?` 를 잠시 지운다.

```
go test ./internal/store/ -run TestJudgmentLinksForItemsIsProjectScoped -v
```
기대: **FAIL**. 확인 후 되돌린다.

- [ ] **Step 6: 커밋**

```bash
git add plugins/flightdeck/server/internal/store/ plugins/flightdeck/server/internal/service/pick.go
git commit -m "$(cat <<'EOF'
store: 항목별 판단 링크 접근자 — 인덱스는 있었고 접근자만 없었다

judgment_link_by_target 인덱스는 처음부터 있었는데 접근자가 없어서
service/pick.go 가 종류 9개를 훑어 링크로 거르고 있었다. 항목 하나에
질의 9회이고 묶음이 들어오면 N×9 가 된다.

JudgmentLinksForItems 는 형제 축이 쓰고, JudgmentsForItem 은
linkedJudgments 를 대신한다. 링크 없는 항목은 키를 안 만든다 —
빈 슬라이스면 "판단이 없다"와 "안 봤다"가 같은 값이 된다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: 원장 — `pick_eval.picked_with`

`picked` 는 **선두**를 계속 담는다. 기존 행도, 탈락 사유 분포를 세는 기존 질의도 안 깨진다. 추가만 하는 마이그레이션이다.

**Files:**
- Create: `plugins/flightdeck/server/internal/store/migrations/00N_pick_bundle.sql`
- Modify: `plugins/flightdeck/server/internal/store/schema.sql` (`pick_eval` 정의)
- Modify: `plugins/flightdeck/server/internal/store/store.go` (`SchemaVersion`, `migrations`, `//go:embed`)
- Modify: `plugins/flightdeck/server/internal/store/item.go` (`RecordPickEval`)
- Modify: `plugins/flightdeck/server/internal/model/types.go` (`PickEval`)
- Modify: `plugins/flightdeck/server/internal/store/migrate_test.go`

**Interfaces:**
- Consumes: `Migration{To, Name, SQL}`(`store.go:50`) · `SchemaVersion`(`store.go:40`) · `marshalStrings`(`item.go:900`)
- Produces: `model.PickEval.PickedWith []string` — 선두를 **뺀** 나머지. `RecordPickEval` 이 JSON 배열로 쓴다

- [ ] **Step 0: 마이그레이션 번호를 정한다 — 착수 시점에**

```
git fetch origin
git log origin/main --oneline -5
ls plugins/flightdeck/server/internal/store/migrations/
grep -n 'To: ' plugins/flightdeck/server/internal/store/migrations/../store.go
```

`003_landing_queue.sql` 이 **이미 main 에 있으면** 이 태스크는 `004` 이고 `SchemaVersion` 은 `3 → 4` 다. **아직 없으면** `003` 이고 `2 → 3` 이다. 아래 코드의 `00N`·`To:` 를 그 값으로 바꿔 쓴다.

정했으면 그 사실을 알린다(다른 세션이 같은 번호를 쓰고 있을 수 있다):

```
fd note --kind ask --item-id fd-pick-bundle \
  --body '마이그레이션 번호를 <정한 값> 으로 잡았다. schema.sql 을 잡은 세션은 충돌 여부를 봐 달라.'
```

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/store/judgment_links_test.go` 와 같은 자리에 `pick_eval` 시험을 둔다(파일은 `internal/store/pick_eval_bundle_test.go` 신규):

```go
package store

import (
	"context"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

func TestRecordPickEvalKeepsLeadInPickedAndRestInPickedWith(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "P")

	err := s.RecordPickEval(ctx, model.PickEval{
		Project: "P", SessionID: "S1",
		Picked:     "lead-item",
		PickedWith: []string{"m1", "m2"},
	})
	if err != nil {
		t.Fatalf("기록 실패: %v", err)
	}

	var picked, with string
	row := s.db.QueryRowContext(ctx,
		`SELECT picked, COALESCE(picked_with,'') FROM pick_eval WHERE project='P'`)
	if err := row.Scan(&picked, &with); err != nil {
		t.Fatalf("읽기 실패: %v", err)
	}
	// ★ picked 는 선두를 계속 담는다. 기존 분포 질의가 안 깨지는 근거가 이 줄이다.
	if picked != "lead-item" {
		t.Fatalf("picked 가 %q 다 — 선두여야 한다", picked)
	}
	if with != `["m1","m2"]` {
		t.Fatalf("picked_with 가 %q 다", with)
	}
}

// 단독이면 picked_with 가 NULL 이다 — 빈 배열과 다르다.
// "묶을 게 없었다"와 "이 판이 그 축을 안 썼다"를 가르는 자리다.
func TestRecordPickEvalSoloLeavesPickedWithNull(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "P")
	if err := s.RecordPickEval(ctx, model.PickEval{
		Project: "P", SessionID: "S1", Picked: "solo",
	}); err != nil {
		t.Fatalf("기록 실패: %v", err)
	}
	var isNull bool
	if err := s.db.QueryRowContext(ctx,
		`SELECT picked_with IS NULL FROM pick_eval WHERE project='P'`).Scan(&isNull); err != nil {
		t.Fatalf("읽기 실패: %v", err)
	}
	if !isNull {
		t.Fatal("단독인데 picked_with 가 NULL 이 아니다")
	}
}
```

`internal/store/migrate_test.go` 에 판올림 시험을 덧붙인다. `makeV1DB`(`migrate_test.go:85`)가 쓰는 방식 — **컬럼·표를 지우고 `schema_version` 을 되돌린 뒤 다시 연다** — 를 그대로 따른다:

```go
// 옛 DB(컬럼 없음)를 열면 판올림이 컬럼을 만들고, 옛 행은 그대로 읽혀야 한다.
//
// ★ 전제를 **결과를 읽기 전에** 단정한다. 옛 상태가 실제로 만들어지지 않았으면
// 아래 단정은 아무것도 안 지킨다 — makeV1DB 를 쓰는 시험이 같은 규율을 갖고 있다.
func TestUpgradeAddsPickedWithAndKeepsOldRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	s, err := OpenWithLogger(path, log)
	if err != nil {
		t.Fatalf("Open 실패: %v", err)
	}
	seed(t, s, "P")
	prev := SchemaVersion - 1
	for _, q := range []string{
		`INSERT INTO pick_eval(project, session_id, at, picked, rejected)
		   VALUES ('P','S1','2026-08-01T00:00:00.000000Z','old-lead','[]')`,
		`ALTER TABLE pick_eval DROP COLUMN picked_with`,
		fmt.Sprintf(`DELETE FROM schema_version WHERE version > %d`, prev),
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("옛 DB 구성 실패(%s): %v", q, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("닫기 실패: %v", err)
	}

	// ── 전제 확인 ──
	raw, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("raw 열기 실패: %v", err)
	}
	var v int
	if err := raw.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&v); err != nil {
		t.Fatalf("전제 확인 실패: %v", err)
	}
	if v != prev {
		t.Fatalf("전제가 성립하지 않았다: schema_version=%d — 이 상태로는 아래 단정이 무의미하다", v)
	}
	raw.Close()

	// ── 판올림 ──
	s2, err := OpenWithLogger(path, log)
	if err != nil {
		t.Fatalf("판올림 Open 실패: %v", err)
	}
	defer s2.Close()

	var picked string
	var isNull bool
	if err := s2.db.QueryRow(
		`SELECT picked, picked_with IS NULL FROM pick_eval WHERE session_id='S1'`,
	).Scan(&picked, &isNull); err != nil {
		t.Fatalf("판올림 뒤 옛 행을 못 읽는다: %v", err)
	}
	if picked != "old-lead" {
		t.Fatalf("옛 행의 picked 가 %q 로 바뀌었다", picked)
	}
	if !isNull {
		t.Fatal("옛 행의 picked_with 가 NULL 이 아니다 — 그 행은 묶음을 관측한 적이 없다")
	}
}
```

> `ALTER TABLE … DROP COLUMN` 은 SQLite 3.35+ 기능이다. 이 모듈의 드라이버(modernc.org/sqlite v1.55)가 받는다. 안 받으면 `pick_eval` 을 `DROP TABLE` 하고 옛 정의로 `CREATE TABLE` 한 뒤 행을 다시 넣어라 — `makeV1DB` 가 표 단위로 하는 것과 같은 방식이다.
>
> **이미 있는 `TestFreshInstallAndUpgradeProduceTheSameSchema`(`migrate_test.go:193`)가 이 태스크의 진짜 안전망이다.** `schema.sql` 에만 컬럼을 넣고 마이그레이션에 안 넣으면(또는 그 반대면) 그 시험이 죽는다. 두 자리를 다 고쳤는지는 그 시험이 말해 준다.

- [ ] **Step 2: 시험이 실패하는지 확인한다**

```
go test ./internal/store/ -run 'TestRecordPickEval|TestMigrationAddsPickedWith' -v
```
기대: `PickedWith undefined` 컴파일 실패, 또는 `no such column: picked_with`

- [ ] **Step 3: 구현을 쓴다**

`internal/store/migrations/00N_pick_bundle.sql` (신규):

```sql
-- 00N · pick_eval 이 묶음을 담는다 (schema_version N-1 → N)
--
-- ★ picked 를 안 바꾼다. 그 칸은 **선두**를 계속 담는다 —
--   선두 id 가 곧 브랜치 이름이고, 기존 행과 기존 분포 질의가 그 칸을 읽는다.
--   배열로 승격하면 옛 행(평문 id)과 새 행(JSON)이 같은 칸에서 갈리고,
--   그 순간 이 표를 읽는 모든 질의가 두 형식을 알아야 한다.
--
-- ★ NULL 을 빈 배열로 접지 않는다. NULL 은 "단독이었다"이고,
--   이 컬럼이 없던 시절의 행도 NULL 이라 둘이 같은 뜻이다 — 정확하다.
ALTER TABLE pick_eval ADD COLUMN picked_with TEXT;
```

`internal/store/schema.sql` 의 `pick_eval` 정의(202-209행)에 컬럼을 더한다:

```sql
CREATE TABLE pick_eval (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  project     TEXT NOT NULL,
  session_id  TEXT NOT NULL,
  at          TEXT NOT NULL,
  picked      TEXT,                     -- 묶음 **선두**의 항목 id 또는 NULL(적격 0건)
  picked_with TEXT,                     -- JSON 배열: 선두를 뺀 나머지. NULL 이면 단독
  rejected    TEXT NOT NULL             -- JSON: [{item, reason_code, detail}]
);
```

`internal/store/store.go`:

```go
const SchemaVersion = N          // 기존 값 + 1

//go:embed migrations/00N_pick_bundle.sql
var migrationPickBundle string

var migrations = []Migration{
	{To: 2, Name: "멱등 기록을 DB 로", SQL: migration002},
	// … 003 이 이미 있으면 그 줄 다음에
	{To: N, Name: "pick_eval 이 묶음을 담는다", SQL: migrationPickBundle},
}
```

`internal/model/types.go` 의 `PickEval`:

```go
type PickEval struct {
	Project   string
	SessionID string
	At        time.Time
	Picked    string // 묶음 **선두**의 항목 id. 빈 문자열이면 적격 0건
	// PickedWith 는 선두를 뺀 나머지다. 비면 단독이었다는 뜻이고,
	// 저장에서 NULL 이 된다 — 빈 배열로 쓰지 않는다.
	PickedWith []string
	Rejected   []Rejection
}
```

`internal/store/item.go` 의 `RecordPickEval`:

```go
func (t *Tx) RecordPickEval(e model.PickEval) error {
	rejected := e.Rejected
	if rejected == nil {
		rejected = []model.Rejection{}
	}
	buf, err := json.Marshal(rejected)
	if err != nil {
		return fmt.Errorf("탈락 사유 직렬화 실패(project=%q session=%q): %w",
			clip(e.Project, 64), clip(e.SessionID, 64), err)
	}
	// 빈 목록은 NULL 로 간다. 빈 배열로 쓰면 "단독이었다"와
	// "묶음을 냈는데 구성원이 0이었다"가 저장에서 같아진다 — 후자는 상태가 아니다.
	var with any
	if len(e.PickedWith) > 0 {
		s, err := marshalStrings(e.PickedWith)
		if err != nil {
			return fmt.Errorf("묶음 구성원 직렬화 실패(project=%q): %w", clip(e.Project, 64), err)
		}
		with = s
	}
	if e.At.IsZero() {
		e.At = nowStamp()
	}
	if _, err := t.tx.ExecContext(t.ctx,
		`INSERT INTO pick_eval(project, session_id, at, picked, picked_with, rejected)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		e.Project, e.SessionID, fmtTime(e.At), nullStr(e.Picked), with, string(buf)); err != nil {
		return fmt.Errorf("추천 판정 기록 실패(project=%q session=%q): %w",
			clip(e.Project, 64), clip(e.SessionID, 64), err)
	}
	return nil
}
```

- [ ] **Step 4: 시험이 통과하는지 확인한다**

```
go test ./internal/store/ -v 2>&1 | tail -30
```
기대: 전부 PASS. 특히 기존 판올림 시험(`migrate_test.go`)이 초록이어야 한다.

- [ ] **Step 5: 실물 DB 로 판올림을 한 번 돌린다**

```
cp ~/.flightdeck/fd.db /tmp/fd-migrate-check.db
go run ./cmd/fd doctor 2>&1 | head -20   # FD_STATE_DIR 를 임시로 돌려 실물을 안 건드린다
```
> 실물 `~/.flightdeck/fd.db` 에 직접 돌리지 마라. 다른 세션 29건이 그 파일을 쓰고 있다.
> 사본으로만 확인하고, 판올림이 `picked_with` 를 만들고 기존 행 수가 안 줄었는지 본다.

- [ ] **Step 6: 커밋**

```bash
git add plugins/flightdeck/server/internal/store/ plugins/flightdeck/server/internal/model/types.go
git commit -m "$(cat <<'EOF'
store: pick_eval 이 묶음을 담는다 — picked 는 선두를 계속 담는다

컬럼 하나를 더하는 마이그레이션이다. picked 를 배열로 승격하지 않는다 —
옛 행(평문 id)과 새 행(JSON)이 같은 칸에서 갈리면 이 표를 읽는 모든 질의가
두 형식을 알아야 하고, picked 는 선두 id 이자 브랜치 이름이라 그 뜻이 안 바뀐다.

picked_with 의 NULL 은 "단독이었다"이고, 이 컬럼이 없던 시절의 행도 NULL 이다.
둘이 같은 뜻이라 접어도 정확하다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 7: 서비스 — 형제 색인 조립과 추천 경로

**Files:**
- Modify: `plugins/flightdeck/server/internal/service/pick.go`
- Modify: `plugins/flightdeck/server/internal/service/pick_test.go`

**Interfaces:**
- Consumes: `judge.EligibleBundle`·`judge.SiblingIndex`·`judge.Link`(태스크 2–4) · `s.st.JudgmentLinksForItems`(태스크 5) · `model.PickEval.PickedWith`(태스크 6) · `s.candidates`·`s.afterFacts`·`s.heldResources`·`s.checkItemPaths`(`pick.go` 안)
- Produces:
  - `type BundleInfo struct { Members []BundleMember; Reason string; Scope string }`
  - `type BundleMember struct { Item model.Item; Link judge.Link; PathCheck *judge.ItemPathVerdict; Notes []model.Judgment; Claimed bool; Rejection *model.Rejection }`
  - `PickResult.Bundle *BundleInfo`
  - `func (s *Service) siblingIndex(ctx context.Context, project string, cands []judge.Candidate) judge.SiblingIndex` — **오류를 안 돌려준다.** 조회가 실패하면 빈 색인을 내고 로그에만 남긴다(축 하나 때문에 추천을 잃지 않는다)

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/service/pick_test.go` 에 덧붙인다. 헬퍼는 이 패키지에 이미 있다:
`newSvc`·`newRepo`·`openSession`·`addItem`·`countRows`·`ctx`(`helper_test.go`).

```go
// makeSiblings 는 항목들을 형제로 만든다.
// finish 가 만드는 모양 그대로다 — 끝낸 항목과 후속 전부가 한 handoff 판단에 매달린다
// (finish.go:148-152). 이 관계의 생산자는 실질적으로 finish 하나뿐이다.
func makeSiblings(t *testing.T, st *store.Store, project string, items ...string) {
	t.Helper()
	links := make([]model.JudgmentLink, 0, len(items))
	for _, id := range items {
		links = append(links, model.JudgmentLink{TargetKind: "item", TargetID: id})
	}
	if _, err := st.AddJudgment(ctx(), model.Judgment{
		Project: project, Kind: model.JudgmentHandoff,
		Title: "쪼갰다", Body: "이건 따로 빼자", Links: links,
	}); err != nil {
		t.Fatalf("형제 준비 실패(%v): %v", items, err)
	}
}

// 형제 둘이 열려 있으면 추천이 묶음으로 온다.
func TestPickRecommendsBundleOfSiblings(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "묶음시험")

	addItem(t, s, "p", "b1-sib", []string{"services/a.go"}, nil)
	addItem(t, s, "p", "b2-sib", []string{"services/b.go"}, nil)
	makeSiblings(t, st, "p", "b1-sib", "b2-sib")

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("pick 실패: %v", err)
	}
	if res.Bundle == nil {
		t.Fatal("묶음 축이 nil 이다 — 서버가 그 축을 읽었으면 non-nil 이어야 한다")
	}
	if len(res.Bundle.Members) != 1 {
		t.Fatalf("형제 하나가 구성원이어야 한다: %+v", res.Bundle.Members)
	}
	m := res.Bundle.Members[0]
	if m.Link.Detail == "" {
		t.Fatal("왜 묶였는지가 비었다")
	}
	if len(m.Link.Axes) == 0 || m.Link.Axes[0] != judge.AxisSibling {
		t.Fatalf("형제 축이 안 붙었다: %v", m.Link.Axes)
	}
	// 브랜치는 선두 하나다.
	if res.Branch != res.Item.ID {
		t.Fatalf("브랜치가 %q 인데 선두는 %q 다", res.Branch, res.Item.ID)
	}
}

// ★ 이 시험이 부재 규율을 지킨다.
// 묶을 게 없어도 Bundle 은 non-nil 이고 구성원이 0건이다.
// nil 은 "이 응답은 그 축을 안 읽었다" 하나만 뜻해야 한다.
func TestPickSoloStillCarriesBundleAxis(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "단독시험")
	addItem(t, s, "p", "alone", []string{"services/x.go"}, nil)

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("pick 실패: %v", err)
	}
	if res.Bundle == nil {
		t.Fatal("단독인데 묶음 축이 nil 이다 — '묶을 게 없다'와 '안 읽었다'가 접혔다")
	}
	if len(res.Bundle.Members) != 0 {
		t.Fatalf("단독인데 구성원이 있다: %+v", res.Bundle.Members)
	}
}

// 추천은 아직 안 집은 것이므로 구성원의 판단 전문을 안 싣는다(컨텍스트 예산 — 설계 §6).
func TestPickRecommendDoesNotLoadMemberNotes(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "판단시험")
	addItem(t, s, "p", "n1", []string{"services/a.go"}, nil)
	addItem(t, s, "p", "n2", []string{"services/b.go"}, nil)
	makeSiblings(t, st, "p", "n1", "n2")

	// 구성원 쪽에만 걸리는 판단을 하나 더 둔다.
	if _, err := s.Note(ctx(), NoteInput{
		Project: "p", SessionID: me.Session.ID, Kind: model.JudgmentNotDone,
		Title: "일부러 안 한 것", Body: "여기는 손대지 않았다", ItemID: "n2",
	}); err != nil {
		t.Fatalf("판단 저장 실패: %v", err)
	}

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("pick 실패: %v", err)
	}
	for _, m := range res.Bundle.Members {
		if len(m.Notes) != 0 {
			t.Fatalf("추천인데 구성원 %q 의 판단 전문을 실었다(%d건)", m.Item.ID, len(m.Notes))
		}
	}
}

// 원장에 선두와 나머지가 갈려 남는다. pick_eval 의 소비자는 SQL 질의다.
func TestPickRecordsBundleInPickEval(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "원장시험")
	addItem(t, s, "p", "e1", []string{"services/a.go"}, nil)
	addItem(t, s, "p", "e2", []string{"services/b.go"}, nil)
	makeSiblings(t, st, "p", "e1", "e2")

	// 대조가 성립하는지 결과를 읽기 전에 단정한다.
	if n := countRows(t, st, `SELECT count(*) FROM pick_eval`); n != 0 {
		t.Fatalf("원장이 비어 있어야 한다: %d행", n)
	}
	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("pick 실패: %v", err)
	}
	lead := res.Item.ID
	if n := countRows(t, st,
		`SELECT count(*) FROM pick_eval WHERE picked = ?`, lead); n != 1 {
		t.Fatalf("picked 에 선두 %q 가 안 남았다", lead)
	}
	if n := countRows(t, st,
		`SELECT count(*) FROM pick_eval WHERE picked_with IS NOT NULL AND picked_with <> '[]'`); n != 1 {
		t.Fatalf("picked_with 에 나머지가 안 남았다")
	}
}
```

> import 에 `"github.com/kweiza/flightdeck/internal/judge"` 와 `"github.com/kweiza/flightdeck/internal/store"` 가 필요하다. `pick_test.go` 는 이미 `model` 을 쓴다.

- [ ] **Step 2: 시험이 실패하는지 확인한다**

```
go test ./internal/service/ -run 'TestPickRecommendsBundle|TestPickSoloStillCarriesBundleAxis' -v
```
기대: `res.Bundle undefined` 컴파일 실패

- [ ] **Step 3: 구현을 쓴다**

`pick.go` 의 `PickResult` 에 필드를 더한다:

```go
	// Bundle 은 이 응답이 낸 묶음이다.
	//
	// ★ **포인터다.** QueueOpen·PathCheck 과 같은 이유이고, 그 상태가 실제로 난다:
	// 서버는 독립 컨테이너인데 플러그인은 자동 갱신되고(구서버 + 신 클라이언트),
	// 오프라인 `fd next` 는 이 필드가 생기기 전에 굳은 디스크 캐시를 그대로 재생한다.
	// 슬라이스만 두면 그 상태가 **"묶을 게 하나도 없다"를 단정한다** — 관측한 적 없는 사실을.
	// SkewBanner 는 api_version 문자열만 보므로 필드 추가로는 안 뜬다.
	//
	// nil = 이 응답은 묶음 축을 안 읽었다 · 구성원 0건 = 묶을 게 없어 단독이다.
	Bundle *BundleInfo `json:"bundle,omitempty"`
```

같은 파일에 타입을 둔다:

```go
// BundleInfo 는 pick 한 번이 낸 묶음이다. **저장되지 않는다.**
type BundleInfo struct {
	Members []BundleMember `json:"members"` // 선두 제외
	Reason  string         `json:"reason"`  // 정렬 네 키의 실제 값
	Scope   string         `json:"scope"`   // 무엇을 이웃 후보로 봤나
}

// BundleMember 는 묶음 구성원 하나다.
type BundleMember struct {
	Item      model.Item             `json:"item"`
	Link      judge.Link             `json:"link"`                // 왜 선두와 묶였나
	PathCheck *judge.ItemPathVerdict `json:"path_check,omitempty"`
	Notes     []model.Judgment       `json:"notes,omitempty"` // 집었을 때만 전문
	Claimed   bool                   `json:"claimed"`
	Rejection *model.Rejection       `json:"rejection,omitempty"` // 못 집었으면 사유
}
```

형제 색인 조립:

```go
// siblingIndex 는 후보들에 걸린 판단 링크를 모아 judge 가 쓸 색인으로 만든다.
//
// 실패해도 pick 을 실패시키지 않는다 — 형제 축 하나 때문에 추천을 잃는 것이 더 나쁘고,
// 빈 색인이면 나머지 두 축이 그대로 돈다. 못 읽은 사실은 derive 가 아니라
// 로그와 Scope 문장에 남긴다(derive 에 넣으면 FreshnessOf 가 git 축을 낡음으로 접는다).
func (s *Service) siblingIndex(ctx context.Context, project string, cands []judge.Candidate) judge.SiblingIndex {
	ids := make([]string, 0, len(cands))
	for _, c := range cands {
		ids = append(ids, c.Item.ID)
	}
	links, err := s.st.JudgmentLinksForItems(ctx, project, ids)
	if err != nil {
		s.log.WarnContext(ctx, "형제 색인 조회 실패 — 형제 축 없이 판정한다",
			"project", clip(project, 64), "count", len(ids), "error", err.Error())
		return judge.SiblingIndex{}
	}
	return judge.SiblingIndex(links)
}
```

`pickRecommend` 를 고친다. `judge.Eligible` 호출을 `judge.EligibleBundle` 로 바꾸고 결과를 싣는다:

```go
	sib := s.siblingIndex(ctx, proj.ID, cands)
	best, rejected := judge.EligibleBundle(judge.EligibleInput{
		Self: in.SessionID, Candidates: cands, Live: live, Facts: facts, HeldResources: held,
	}, sib)

	res := PickResult{Rejected: rejected, Scope: scope, QueueOpen: &openCount}
	eval := model.PickEval{Project: proj.ID, SessionID: in.SessionID, Rejected: rejected}
	if best != nil {
		eval.Picked = best.Lead.Item.ID
		for _, m := range best.Members {
			eval.PickedWith = append(eval.PickedWith, m.Item.ID)
		}
	}
	if err := s.st.RecordPickEval(ctx, eval); err != nil {
		return PickResult{}, err
	}
	s.st.LogEvent(ctx, "item.pick", proj.ID, in.SessionID, map[string]any{
		"picked": eval.Picked, "picked_count": len(eval.PickedWith) + 1,
		"count": len(cands), "skipped": len(rejected),
	})

	if best == nil {
		// … 기존 PickNone 경로 그대로
	}

	item := best.Lead.Item
	res.Mode, res.Item, res.Branch = PickRecommended, &item, item.ID
	res.Overlaps = best.Lead.Overlaps
	res.Setup = SetupCommands(proj.Path, proj.DefaultBranch, item.ID)
	res.PathCheck = s.checkItemPaths(ctx, proj, item.Paths)
	res.Bundle = &BundleInfo{
		Reason: best.Reason,
		Scope: fmt.Sprintf("이웃 후보는 적격 항목 %d건이다. 선두와 **직접** 이어진 것만 붙였다(전이 없음)",
			len(cands)),
	}
	for i, m := range best.Members {
		res.Bundle.Members = append(res.Bundle.Members, BundleMember{
			Item: m.Item, Link: best.Links[i],
			PathCheck: s.checkItemPaths(ctx, proj, m.Item.Paths),
			// Notes 는 안 싣는다 — 추천은 아직 안 집은 것이라
			// 후보마다 전문을 실으면 컨텍스트를 태운다(설계 §6).
		})
	}
	res.Reason = fmt.Sprintf("%s · 후보 %d건 중 1순위다. "+
		"아직 선점하지 않았다 — 집으려면 item_ids 에 선두부터 순서대로 주고 다시 불러라",
		best.Reason, len(cands))
```

- [ ] **Step 4: 시험이 통과하는지 확인한다**

```
go test ./internal/service/ -v 2>&1 | tail -40
```
기대: 전부 PASS. **기존 pick 시험이 전부 초록이어야 한다** — 단독일 때의 동작이 안 바뀌었다는 증거다.

- [ ] **Step 5: 빨간불을 확인한다**

`res.Bundle = &BundleInfo{...}` 를 잠시 `res.Bundle = nil` 로 바꾼다.

```
go test ./internal/service/ -run TestPickSoloStillCarriesBundleAxis -v
```
기대: **FAIL**. 확인 후 되돌린다.

- [ ] **Step 6: 커밋**

```bash
git add plugins/flightdeck/server/internal/service/
git commit -m "$(cat <<'EOF'
feat(service): 추천이 묶음으로 온다 — 형제 색인을 조립해 judge 에 넘긴다

PickResult.Bundle 은 포인터다. 슬라이스만 두면 구서버·옛 캐시가 낸 응답이
"묶을 게 하나도 없다"를 단정하게 되고, SkewBanner 는 필드 추가로는 안 뜬다.
nil 은 "이 축을 안 읽었다", 구성원 0건은 "묶을 게 없어 단독이다"다.

형제 색인 조회가 실패해도 pick 을 실패시키지 않는다 — 축 하나 때문에 추천을
잃는 것이 더 나쁘고, 빈 색인이면 나머지 두 축이 그대로 돈다. 그 실패를
derive 에 넣지 않는다(FreshnessOf 가 git 축을 낡음으로 접는다).

추천은 구성원의 판단 전문을 안 싣는다. 아직 안 집은 것이라 컨텍스트만 태운다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 8: 서비스 — 묶음 선점(선두 원자, 나머지는 되는 대로)

**Files:**
- Modify: `plugins/flightdeck/server/internal/service/pick.go`
- Modify: `plugins/flightdeck/server/internal/service/pick_test.go`

**Interfaces:**
- Consumes: `s.pickExplicit`(기존, 그대로 둔다) · `s.st.Tx`·`t.ClaimItem`·`t.Touch`(`pick.go:263-281` 의 방식)
- Produces: `PickInput.ItemIDs []string` · `func (s *Service) pickBundle(...) (PickResult, error)`

- [ ] **Step 1: 실패하는 시험을 쓴다**

```go
// 선두를 못 집으면 아무것도 안 쓴다 — 브랜치가 정의되지 않으므로
// "묶음을 집었다"고 말할 수 없다.
func TestPickBundleLeadIsAtomic(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	other := openSession(t, s, "p", repo, repo, "cc-2", "남")

	addItem(t, s, "p", "lead", []string{"services/a.go"}, nil)
	addItem(t, s, "p", "mem", []string{"services/b.go"}, nil)
	if _, err := s.Pick(ctx(), PickInput{
		Project: "p", SessionID: other.Session.ID, ItemID: "lead"}); err != nil {
		t.Fatalf("남의 선점 준비 실패: %v", err)
	}

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID,
		ItemIDs: []string{"lead", "mem"}})
	if err == nil {
		t.Fatalf("선두가 남의 것인데 성공했다: %+v", res)
	}
	// mem 에 선점 행이 생기면 안 된다.
	if _, cerr := st.GetClaim(ctx(), "p", "mem"); !errors.Is(cerr, store.ErrNotFound) {
		t.Fatalf("선두가 막혔는데 구성원을 집었다 (GetClaim err=%v)", cerr)
	}
}

// 선두를 집었으면 구성원 하나가 막혀도 나머지는 살아야 한다.
func TestPickBundleKeepsLeadWhenMemberBlocked(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	other := openSession(t, s, "p", repo, repo, "cc-2", "남")

	addItem(t, s, "p", "lead", []string{"services/a.go"}, nil)
	addItem(t, s, "p", "m1-taken", []string{"services/b.go"}, nil)
	addItem(t, s, "p", "m2-free", []string{"services/c.go"}, nil)
	if _, err := s.Pick(ctx(), PickInput{
		Project: "p", SessionID: other.Session.ID, ItemID: "m1-taken"}); err != nil {
		t.Fatalf("남의 선점 준비 실패: %v", err)
	}

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID,
		ItemIDs: []string{"lead", "m1-taken", "m2-free"}})
	if err != nil {
		t.Fatalf("구성원 하나가 막혔다고 pick 이 실패했다: %v", err)
	}
	if res.Mode != PickClaimed {
		t.Fatalf("모드가 %q 다", res.Mode)
	}
	if res.Branch != "lead" {
		t.Fatalf("브랜치가 %q 다 — 선두 id 여야 한다", res.Branch)
	}
	var claimed, blocked int
	for _, m := range res.Bundle.Members {
		if m.Claimed {
			claimed++
			continue
		}
		blocked++
		if m.Rejection == nil || m.Rejection.Reason == "" {
			t.Fatalf("못 집은 구성원 %q 에 사유가 없다", m.Item.ID)
		}
		if m.Rejection.Detail == "" {
			t.Fatalf("못 집은 구성원 %q 에 상세가 없다 — 사유 코드만으로는 왜인지 모른다", m.Item.ID)
		}
	}
	if claimed != 1 || blocked != 1 {
		t.Fatalf("집은 것 %d · 막힌 것 %d", claimed, blocked)
	}
}

// 집었으면 구성원의 판단 전문이 온다 — 추천과 다른 점이 이것이다.
func TestPickBundleLoadsMemberNotesWhenClaimed(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	addItem(t, s, "p", "lead", []string{"services/a.go"}, nil)
	addItem(t, s, "p", "mem", []string{"services/b.go"}, nil)
	if _, err := s.Note(ctx(), NoteInput{
		Project: "p", SessionID: me.Session.ID, Kind: model.JudgmentNotDone,
		Title: "일부러 안 한 것", Body: "DLQ 재처리는 계약 대기라 손대지 않았다", ItemID: "mem",
	}); err != nil {
		t.Fatalf("판단 저장 실패: %v", err)
	}

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID,
		ItemIDs: []string{"lead", "mem"}})
	if err != nil {
		t.Fatalf("pick 실패: %v", err)
	}
	if len(res.Bundle.Members) != 1 || len(res.Bundle.Members[0].Notes) == 0 {
		t.Fatalf("집은 구성원의 판단 전문이 없다: %+v", res.Bundle.Members)
	}
}

// 원소 1개짜리 item_ids 는 기존 item_id 와 같은 결과여야 한다.
// 다르면 CLI 가 인자 하나를 넘겼을 때 조용히 다른 경로를 탄다.
func TestPickBundleOfOneEqualsSinglePick(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	a := openSession(t, s, "p", repo, repo, "cc-1", "A")
	b := openSession(t, s, "p", repo, repo, "cc-2", "B")
	addItem(t, s, "p", "one", []string{"services/a.go"}, nil)
	addItem(t, s, "p", "two", []string{"services/b.go"}, nil)

	viaID, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: a.Session.ID, ItemID: "one"})
	if err != nil {
		t.Fatalf("단독 선점 실패: %v", err)
	}
	viaIDs, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: b.Session.ID, ItemIDs: []string{"two"}})
	if err != nil {
		t.Fatalf("원소 1개 묶음 선점 실패: %v", err)
	}
	if viaID.Mode != viaIDs.Mode {
		t.Fatalf("모드가 갈렸다: %q vs %q", viaID.Mode, viaIDs.Mode)
	}
	if viaIDs.Branch != "two" {
		t.Fatalf("브랜치가 %q 다", viaIDs.Branch)
	}
	if len(viaIDs.Setup) != len(viaID.Setup) {
		t.Fatalf("워크트리 명령 수가 갈렸다: %d vs %d", len(viaID.Setup), len(viaIDs.Setup))
	}
	if viaIDs.Bundle == nil || len(viaIDs.Bundle.Members) != 0 {
		t.Fatalf("원소 1개인데 구성원이 있다: %+v", viaIDs.Bundle)
	}
}

// 큐 열림 수는 **모든 쓰기 뒤에** 센다. 묶음 3건을 집으면 3이 빠진다.
func TestPickBundleCountsQueueAfterWrites(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	for _, id := range []string{"q1", "q2", "q3", "q4", "q5"} {
		addItem(t, s, "p", id, []string{"services/" + id + ".go"}, nil)
	}
	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID,
		ItemIDs: []string{"q1", "q2", "q3"}})
	if err != nil {
		t.Fatalf("pick 실패: %v", err)
	}
	if res.QueueOpen == nil {
		t.Fatal("큐 열림 수가 없다")
	}
	if *res.QueueOpen != 2 {
		t.Fatalf("큐 열림이 %d 다 — 5건 중 3건을 집었으니 2여야 한다", *res.QueueOpen)
	}
}
```

> import 에 `"errors"` 와 `"github.com/kweiza/flightdeck/internal/store"` 가 필요하다.

- [ ] **Step 2: 시험이 실패하는지 확인한다**

```
go test ./internal/service/ -run TestPickBundle -v
```
기대: `PickInput.ItemIDs undefined` 컴파일 실패

- [ ] **Step 3: 구현을 쓴다**

`PickInput` 에 필드를 더한다:

```go
type PickInput struct {
	Project   string
	SessionID string
	ItemID    string   // 비면 추천, 있으면 단독 선점
	ItemIDs   []string // 묶음 선점. **첫째가 선두**이고 그 id 가 브랜치가 된다
}
```

`Pick` 의 분기를 셋으로 넓힌다:

```go
	var res PickResult
	switch {
	case len(in.ItemIDs) > 0:
		res, err = s.pickBundle(ctx, proj, in, live, d, now)
	case strings.TrimSpace(in.ItemID) != "":
		res, err = s.pickExplicit(ctx, proj, in, live, d, now)
	default:
		res, err = s.pickRecommend(ctx, proj, in, live, d, now)
	}
```

`pickBundle` 을 새로 쓴다(`pickExplicit` 은 **안 고친다**):

```go
// pickBundle 은 묶음을 선점한다.
//
// 지키는 것 둘:
//
//  1. **선두는 원자다.** 못 집으면 아무것도 안 쓰고 거절한다 —
//     브랜치가 정의되지 않으므로 "묶음을 집었다"고 말할 수 없다.
//  2. **구성원은 각각 별도 트랜잭션이다.** 하나를 남이 채 갔다는 이유로 이미 성립한
//     선두 선점을 되돌리면 세션이 아무것도 못 얻고, 동시 세션이 스물 넘는 환경에서
//     그 재시도는 잦다. 대신 **침묵하지 않는다** — 못 집은 사유를 그대로 싣는다.
func (s *Service) pickBundle(ctx context.Context, proj model.Project, in PickInput,
	live []judge.LiveSession, d *derive, now time.Time) (PickResult, error) {

	ids := dedupeIDs(in.ItemIDs)
	lead, rest := ids[0], ids[1:]

	// ① 선두 — 기존 단독 경로를 그대로 탄다. 흉내 내지 않는다.
	res, err := s.pickExplicit(ctx, proj,
		PickInput{Project: in.Project, SessionID: in.SessionID, ItemID: lead}, live, d, now)
	if err != nil {
		return PickResult{}, err
	}

	res.Scope = fmt.Sprintf("지정된 묶음 %d건(선두 %s)", len(ids), lead)
	res.Bundle = &BundleInfo{
		Reason: fmt.Sprintf("세션이 지정한 묶음이다 — 선두 %s 가 브랜치가 된다", lead),
		Scope:  "판정 없이 지정된 그대로 집었다",
	}

	// ② 구성원 — 되는 대로 집는다.
	allPaths := append([]string(nil), res.Item.Paths...)
	for _, id := range rest {
		m := BundleMember{Link: judge.Link{Item: id, Detail: "세션이 함께 지정했다"}}
		sub, serr := s.pickExplicit(ctx, proj,
			PickInput{Project: in.Project, SessionID: in.SessionID, ItemID: id}, live, d, now)
		if serr != nil {
			m.Rejection = rejectionOf(id, serr)
			s.log.WarnContext(ctx, "묶음 구성원 선점 실패 — 나머지를 진행한다",
				"project", proj.ID, "session_id", in.SessionID, "item", clip(id, 64),
				"error", serr.Error())
			res.Bundle.Members = append(res.Bundle.Members, m)
			continue
		}
		m.Item, m.Claimed = *sub.Item, true
		m.Notes, m.PathCheck = sub.Notes, sub.PathCheck
		allPaths = append(allPaths, sub.Item.Paths...)
		res.Bundle.Members = append(res.Bundle.Members, m)
	}

	// ③ 겹침은 묶음 전체 경로로 다시 본다 — 남과 부딪히는지는 묶음 단위 질문이다.
	res.Overlaps = judge.OverlapsWithLive(allPaths, live, in.SessionID)

	claimed := 1
	for _, m := range res.Bundle.Members {
		if m.Claimed {
			claimed++
		}
	}
	res.Reason = fmt.Sprintf("선두 %s 를 선점했다. 묶음 %d건 중 %d건을 집었다", lead, len(ids), claimed)
	return res, nil
}

// dedupeIDs 는 순서를 지키며 중복을 걷어낸다. 같은 id 를 두 번 집으려 하면
// 둘째가 "이미 내 선점"으로 재개 경로를 타 사유가 흐려진다.
func dedupeIDs(ids []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// rejectionOf 는 선점 실패를 탈락 사유 한 줄로 바꾼다.
// 사유 코드를 사람 말로 풀지 않는다 — 기계가 세는 값은 그대로 보인다.
func rejectionOf(id string, err error) *model.Rejection {
	code := "claim-failed"
	if errors.Is(err, store.ErrNotFound) {
		code = "not-found"
	}
	var ce *store.ConflictError // 실물 타입 이름은 store 패키지에서 확인해 맞춘다
	if errors.As(err, &ce) {
		code = judge.RejectClaimed
	}
	return &model.Rejection{Item: id, Reason: code, Detail: clip(err.Error(), 200)}
}
```

> `store` 의 선점 충돌 오류 타입 이름은 `internal/store/item.go` 의 `JudgeClaim` 근처에서 확인하고 `rejectionOf` 를 실물에 맞춰라. 맞는 타입이 없으면 `errors.Is(err, store.ErrConflict)` 같은 센티넬을 찾아 쓰고, 그것도 없으면 `code = "claim-failed"` 하나로 두되 **Detail 에 원문을 반드시 남겨라.**

`ItemIDs` 가 비었을 때(전부 공백) `dedupeIDs` 가 빈 슬라이스를 내므로 `Pick` 진입부에서 막는다:

```go
	if len(in.ItemIDs) > 0 && len(dedupeIDs(in.ItemIDs)) == 0 {
		return PickResult{}, &RefusedError{What: "pick",
			Reason: "item_ids 에 쓸 수 있는 항목 id 가 없다"}
	}
```

- [ ] **Step 4: 시험이 통과하는지 확인한다**

```
go test ./internal/service/ -v 2>&1 | tail -40
```
기대: 전부 PASS

- [ ] **Step 5: 빨간불을 확인한다**

`pickBundle` 의 선두 오류 반환(`return PickResult{}, err`)을 잠시 `_ = err` 로 바꿔 계속 진행하게 만든다.

```
go test ./internal/service/ -run TestPickBundleLeadIsAtomic -v
```
기대: **FAIL**. 확인 후 되돌린다.

- [ ] **Step 6: 커밋**

```bash
git add plugins/flightdeck/server/internal/service/
git commit -m "$(cat <<'EOF'
feat(service): 묶음 선점 — 선두는 원자, 나머지는 되는 대로

선두를 못 집으면 아무것도 안 쓰고 거절한다. 브랜치가 정의되지 않으므로
"묶음을 집었다"고 말할 수 없다.

구성원은 각각 별도 트랜잭션이다. 하나를 남이 채 갔다고 이미 성립한 선두
선점을 되돌리면 세션이 아무것도 못 얻고, 동시 세션이 스물 넘는 환경에서
그 재시도는 잦다. 대신 못 집은 사유를 코드 그대로 응답에 싣는다.

선두는 기존 pickExplicit 을 그대로 탄다 — 흉내 내면 조회와 삽입 사이에
남이 잡는 창이 생긴다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 9: MCP 인자 — `item_ids`

**Files:**
- Modify: `plugins/flightdeck/server/internal/mcpsrv/tools.go`
- Modify: `plugins/flightdeck/server/internal/mcpsrv/mcpsrv.go` (`pick` 핸들러 — 실제 파일은 `grep -rn 'pickArgs' internal/mcpsrv/` 로 찾는다)
- Modify: `plugins/flightdeck/server/internal/mcpsrv/tools_test.go`

**Interfaces:**
- Consumes: `strArr`·`str`·`obj`(`tools.go` 안) · `service.PickInput.ItemIDs`(태스크 8) · `RenderRefusal`(`render.go:849`)
- Produces: `pickArgs.ItemIDs []string`

- [ ] **Step 1: 실패하는 시험을 쓴다**

```go
// 도구 수는 6개 그대로다. 세션 시작 컨텍스트 예산이 그 이유다(설계 §6).
func TestPickGainsItemIDsWithoutGrowingToolCount(t *testing.T) {
	if got := len(Tools()); got != 6 {
		t.Fatalf("도구가 %d개다 — 6개여야 한다", got)
	}
	var pick *Tool
	for i := range tools {
		if tools[i].Name == "pick" {
			pick = &tools[i]
		}
	}
	if pick == nil {
		t.Fatal("pick 도구가 없다")
	}
	props := pick.InputSchema["properties"].(map[string]any)
	if _, ok := props["item_ids"]; !ok {
		t.Fatalf("pick 에 item_ids 가 없다: %v", props)
	}
	if _, ok := props["item_id"]; !ok {
		t.Fatal("item_id 가 사라졌다 — 단독 선점·재개 경로가 깨진다")
	}
}
```

핸들러 시험. `server_test.go` 의 `call`(120행)·`serve`(124행) 를 쓰고, 서버 조립은 그 파일의 기존 시험이 하는 방식을 그대로 쓴다:

```go
// 둘을 동시에 주면 거절한다. 합치거나 한쪽을 우선하면
// 무엇을 집었는지가 흐려지고, 그것이 이 도구가 지키려는 것 자체다.
func TestPickRefusesBothItemIDAndItemIDs(t *testing.T) {
	srv := newTestServer(t) // server_test.go 의 기존 조립 방식 그대로
	frames := serve(t, srv,
		call("pick", map[string]any{"item_id": "a", "item_ids": []string{"b"}}))

	if len(frames) == 0 {
		t.Fatal("응답이 없다")
	}
	got := frames[len(frames)-1].textContent() // 이 파일의 기존 본문 추출 방식을 쓴다
	for _, want := range []string{"item_id", "item_ids", "둘 중 하나만"} {
		if !strings.Contains(got, want) {
			t.Fatalf("거절 문구에 %q 가 없다:\n%s", want, got)
		}
	}
	// 거절인데 선점이 일어나면 안 된다 — 응답이 "선점했다"로 시작하면 실패다.
	if strings.Contains(got, "선점했다") {
		t.Fatalf("거절해야 하는데 선점했다:\n%s", got)
	}
}
```

> `newTestServer`·`textContent` 는 이 파일에 이미 있는 것을 쓴다. 이름이 다르면 `server_test.go` 의 다른 도구 호출 시험 하나를 그대로 본떠라 — **새 조립기를 만들지 마라.** 조립이 두 벌이 되면 한쪽만 배선이 바뀌어도 안 보인다.

- [ ] **Step 2: 시험이 실패하는지 확인한다**

```
go test ./internal/mcpsrv/ -run 'TestPickGainsItemIDs|TestPickRefusesBoth' -v
```
기대: FAIL — `item_ids` 없음

- [ ] **Step 3: 구현을 쓴다**

`tools.go` 의 `pick` 항목:

```go
	{
		Name:        "pick",
		Description: "인자 없으면 함께 갈 항목까지 묶어 추천하고 탈락 사유 전부. item_ids 를 주면 선점한다.",
		InputSchema: obj(map[string]any{
			"item_id":  str("집을 항목 id. 없으면 추천만 하고 선점하지 않는다"),
			"item_ids": strArr("함께 집을 항목 id 들. **첫째가 선두**이고 그 id 가 브랜치가 된다"),
			"steal_reason": str("남의 선점을 회수하는 사유. **이 서버는 회수하지 않는다** — " +
				"주면 사유와 함께 거절한다"),
		}),
	},
```

`pickArgs`:

```go
type pickArgs struct {
	ItemID      string   `json:"item_id"`
	ItemIDs     []string `json:"item_ids"`
	StealReason string   `json:"steal_reason"`
}
```

핸들러에서 동시 지정을 막고 `ItemIDs` 를 넘긴다:

```go
	if strings.TrimSpace(a.ItemID) != "" && len(a.ItemIDs) > 0 {
		return RenderRefusal("pick",
			"item_id 와 item_ids 를 함께 줬다",
			"둘 중 하나만 써라 — 하나면 item_id, 묶음이면 item_ids 에 선두부터 순서대로."), nil
	}
	// 기존 핸들러가 채우던 Project·SessionID 는 **그대로 두고** ItemIDs 한 줄만 더한다.
	in.ItemIDs = a.ItemIDs
```

- [ ] **Step 4: 시험이 통과하는지 확인한다**

```
go test ./internal/mcpsrv/ -v 2>&1 | tail -30
```
기대: 전부 PASS. 특히 `tools/list` 의 고정 순서를 단정하는 기존 시험이 초록이어야 한다.

- [ ] **Step 5: 커밋**

```bash
git add plugins/flightdeck/server/internal/mcpsrv/
git commit -m "$(cat <<'EOF'
feat(mcpsrv): pick 이 item_ids 를 받는다 — 도구 수는 6개 그대로

새 도구를 만들지 않는다. 세션 시작에는 도구 이름과 서버 instructions 만
실리고 그 예산이 이 표면의 상한이다.

item_id 와 item_ids 를 함께 주면 거절한다. 합치거나 한쪽을 우선하면
무엇을 집었는지가 흐려지고, 그것이 이 도구가 지키려는 것 자체다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 10: 렌더 — 묶음 절, 그리고 침묵 금지

**Files:**
- Modify: `plugins/flightdeck/server/internal/mcpsrv/render.go`
- Modify: `plugins/flightdeck/server/internal/mcpsrv/render_test.go`

**Interfaces:**
- Consumes: `service.PickResult.Bundle`·`service.BundleMember`(태스크 7) · `renderPathCheck`·`indent`·`clip`(`render.go` 안)
- Produces: (공개 시그니처 변경 없음 — `RenderPick` 의 출력만 넓어진다)

- [ ] **Step 1: 실패하는 시험을 쓴다**

```go
// ★ 이 시험이 이 태스크에서 가장 중요하다.
// 묶음 축이 없는 응답(구서버·옛 캐시)이 "묶을 게 없다"로 읽히면 안 된다.
func TestRenderPickNeverCallsAnAbsentBundleSolo(t *testing.T) {
	got := RenderPick(service.PickResult{
		Mode: service.PickRecommended, Reason: "1순위다", Bundle: nil,
	}, t0)
	if !strings.Contains(got, "이 응답은 그 축을 읽지 않았다") {
		t.Fatalf("묶음 축 부재를 안 말한다:\n%s", got)
	}
	if strings.Contains(got, "묶을 게 없어 단독이다") {
		t.Fatalf("안 읽은 축을 '단독'으로 단정했다:\n%s", got)
	}
}

// 구성원 0건이면 단독이라고 **말한다**. 침묵하면 부재와 같은 화면이 된다.
func TestRenderPickSaysSoloWhenBundleIsEmpty(t *testing.T) {
	got := RenderPick(service.PickResult{
		Mode: service.PickRecommended, Reason: "1순위다",
		Bundle: &service.BundleInfo{Reason: "의존자 합 0 · 묶음 1건"},
	}, t0)
	if !strings.Contains(got, "단독") {
		t.Fatalf("단독임을 안 말한다:\n%s", got)
	}
}

func TestRenderPickShowsWhyEachMemberIsBundled(t *testing.T) {
	res := service.PickResult{
		Mode: service.PickRecommended, Reason: "1순위다",
		Item:   &model.Item{ID: "lead", Title: "선두", State: model.ItemOpen, CreatedAt: t0},
		Branch: "lead",
		Bundle: &service.BundleInfo{
			Reason: "의존자 합 0 · 묶음 2건 · 최고령 2026-08-04 23:50 · 선두 lead",
			Members: []service.BundleMember{{
				Item: model.Item{ID: "mem", Title: "구성원", State: model.ItemOpen,
					Paths: []string{"x.go"}, CreatedAt: t0},
				Link: judge.Link{Item: "mem",
					Axes:   []judge.BundleAxis{judge.AxisSibling, judge.AxisAfter},
					Detail: "판단 J1 가 둘을 함께 가리킨다 · 선행이 같다(sha:47421b4)"},
			}},
		},
	}
	got := RenderPick(res, t0)
	for _, want := range []string{"mem", "묶은 근거", "sibling", "after", "J1", "47421b4"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q 가 응답에 없다:\n%s", want, got)
		}
	}
	// 브랜치는 선두 하나다.
	if strings.Count(got, "브랜치: ") != 1 {
		t.Fatalf("브랜치 줄이 하나가 아니다:\n%s", got)
	}
}

// 못 집은 구성원은 사유 코드 그대로 보인다.
func TestRenderPickShowsUnclaimedMemberReason(t *testing.T) {
	res := service.PickResult{
		Mode: service.PickClaimed, Reason: "선두를 선점했다",
		Item: &model.Item{ID: "lead", Title: "선두", State: model.ItemClaimed, CreatedAt: t0},
		Bundle: &service.BundleInfo{Members: []service.BundleMember{{
			Item:      model.Item{ID: "blocked", Title: "막힘", CreatedAt: t0},
			Claimed:   false,
			Rejection: &model.Rejection{Item: "blocked", Reason: judge.RejectClaimed, Detail: "세션 S2 가 선점했다"},
		}}},
	}
	got := RenderPick(res, t0)
	for _, want := range []string{"못 집었다", judge.RejectClaimed, "S2"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q 가 응답에 없다:\n%s", want, got)
		}
	}
}
```

- [ ] **Step 2: 시험이 실패하는지 확인한다**

```
go test ./internal/mcpsrv/ -run TestRenderPick -v
```
기대: 새 시험 넷 FAIL(부재 문장 없음 등)

- [ ] **Step 3: 구현을 쓴다**

`RenderPick` 의 헤더 줄을 묶음 수에 맞춘다:

```go
	n := 1
	if r.Bundle != nil {
		n = len(r.Bundle.Members) + 1
	}
	switch r.Mode {
	case service.PickRecommended:
		if n > 1 {
			fmt.Fprintf(&b, "pick · 추천 묶음 %d건 — **아직 선점하지 않았다**\n", n)
		} else {
			b.WriteString("pick · 추천 1건 — **아직 선점하지 않았다**\n")
		}
	case service.PickClaimed:
		if n > 1 {
			claimed := 1
			for _, m := range r.Bundle.Members {
				if m.Claimed {
					claimed++
				}
			}
			fmt.Fprintf(&b, "pick · 선점했다 — 묶음 %d건 중 %d건\n", n, claimed)
		} else {
			b.WriteString("pick · 선점했다\n")
		}
	// … resumed·none 은 그대로
	}
```

선두 항목 블록 **뒤**, 브랜치 줄 **앞**에 묶음 절을 넣는다:

```go
	b.WriteString(renderBundle(r.Bundle))
```

그리고 헬퍼를 쓴다:

```go
// renderBundle 은 묶음 절이다. 순수 함수다.
//
// ★ **어느 갈래에서도 침묵하지 않는다.** 셋을 다 말한다:
// 축을 안 읽었다 · 묶을 게 없어 단독이다 · 이런 것들과 묶였다.
// 침묵하면 "묶을 게 없다"와 "이 축을 안 봤다"가 같은 화면이 되고,
// 그러면 판정이 통째로 실패한 날에도 pick 은 평소와 똑같아 보인다.
// renderPathCheck 이 같은 이유로 이상이 없어도 한 줄을 찍는다.
func renderBundle(bi *service.BundleInfo) string {
	if bi == nil {
		return "\n묶음: 이 응답은 그 축을 읽지 않았다 — 낡은 캐시이거나 서버가 이 축을 모르는 판이다.\n"
	}
	var b strings.Builder
	if len(bi.Members) == 0 {
		b.WriteString("\n묶음: 함께 갈 항목이 없다 — 단독이다.\n")
		if bi.Reason != "" {
			fmt.Fprintf(&b, "  %s\n", bi.Reason)
		}
		return b.String()
	}
	fmt.Fprintf(&b, "\n묶음 구성원 %d건 (선두는 위의 항목이다):\n", len(bi.Members))
	for _, m := range bi.Members {
		mark := "+"
		if m.Rejection != nil || !m.Claimed {
			mark = "✗"
		}
		fmt.Fprintf(&b, "\n  %s %s — %s [%s]\n", mark, m.Item.ID, m.Item.Title, m.Item.State)
		if m.Rejection != nil {
			fmt.Fprintf(&b, "    못 집었다: %-16s %s\n",
				m.Rejection.Reason, clip(m.Rejection.Detail, 160))
			b.WriteString("    이 항목 없이 나머지를 진행한다. " +
				"필요하면 그 세션에게 note(kind:\"ask\") 로 알려라\n")
			continue
		}
		if len(m.Link.Axes) > 0 {
			axes := make([]string, 0, len(m.Link.Axes))
			for _, a := range m.Link.Axes {
				axes = append(axes, string(a))
			}
			fmt.Fprintf(&b, "    묶은 근거: [%s] %s\n", strings.Join(axes, " + "), m.Link.Detail)
		}
		if len(m.Item.Paths) > 0 {
			fmt.Fprintf(&b, "    경로: %s\n", strings.Join(m.Item.Paths, ", "))
		}
		b.WriteString(indent(strings.TrimRight(renderPathCheck(m.PathCheck, m.Item.ID), "\n"), "    ") + "\n")
		if len(m.Notes) > 0 {
			fmt.Fprintf(&b, "    연결된 판단 %d건 (전문):\n", len(m.Notes))
			for _, j := range m.Notes {
				fmt.Fprintf(&b, "      [%s] %s · %s\n", j.Kind,
					j.At.UTC().Format("2006-01-02 15:04"), clip(firstLine(j.Title, j.Body), 100))
				if strings.TrimSpace(j.Body) != "" {
					b.WriteString(indent(clip(j.Body, 4000), "        ") + "\n")
				}
			}
		}
	}
	if bi.Reason != "" {
		fmt.Fprintf(&b, "\n왜 이 묶음인가: %s\n", bi.Reason)
	}
	if bi.Scope != "" {
		fmt.Fprintf(&b, "묶음 범위: %s\n", bi.Scope)
	}
	return b.String()
}
```

브랜치 줄에 묶음임을 덧붙인다:

```go
	if r.Branch != "" {
		fmt.Fprintf(&b, "\n브랜치: %s\n", r.Branch)
		if r.Bundle != nil && len(r.Bundle.Members) > 0 {
			fmt.Fprintf(&b, "  묶음 선두의 id 다. %d건을 이 워크트리에서 함께 한다.\n",
				len(r.Bundle.Members)+1)
		}
		// … 기존 Setup 출력 그대로
	}
```

겹침이 **묶음 전체 경로**로 계산됐다는 사실을 `RenderPick` 이 말한다. `RenderTail` 은 안 고친다 — 그 함수는 모든 도구 응답이 쓰고 묶음을 모른다. `renderBundle` 호출 바로 뒤에 한 줄 넣는다:

```go
	if r.Bundle != nil && len(r.Bundle.Members) > 0 {
		fmt.Fprintf(&b, "겹침 판정 범위: 묶음 %d건의 경로를 전부 합쳐서 봤다 — "+
			"남과 부딪히는지는 묶음 단위 질문이다.\n", len(r.Bundle.Members)+1)
	}
```

이 줄이 없으면 꼬리의 `겹침:` 줄이 **선두 경로만 본 결과**로 읽힌다. 침묵하면 범위를 좁게 본 것과 넓게 본 것이 같은 화면이 된다.

- [ ] **Step 4: 시험이 통과하는지 확인한다**

```
go test ./internal/mcpsrv/ -v 2>&1 | tail -30
```
기대: 전부 PASS

- [ ] **Step 5: 빨간불을 확인한다**

`renderBundle` 의 `if bi == nil` 가지를 잠시 `return ""` 로 바꾼다.

```
go test ./internal/mcpsrv/ -run TestRenderPickNeverCallsAnAbsentBundleSolo -v
```
기대: **FAIL**. 확인 후 되돌린다.

- [ ] **Step 6: 커밋**

```bash
git add plugins/flightdeck/server/internal/mcpsrv/
git commit -m "$(cat <<'EOF'
feat(mcpsrv): pick 응답이 묶음 절을 낸다 — 어느 갈래에서도 침묵하지 않는다

셋을 다 말한다: 축을 안 읽었다 · 묶을 게 없어 단독이다 · 이런 것들과 묶였다.
침묵하면 "묶을 게 없다"와 "이 축을 안 봤다"가 같은 화면이 되고, 그러면
판정이 통째로 실패한 날에도 pick 은 평소와 똑같아 보인다. renderPathCheck 이
같은 이유로 이상이 없어도 한 줄을 찍는다.

구성원마다 왜 묶였는지를 축과 근거로 낸다. 축을 뭉개면 셋 다 맞는 쌍과
형제이기만 한 쌍이 화면에서 같아진다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 11: 왕복 — 서버 → JSON → 클라이언트 → 렌더

렌더 시험은 `PickResult` 를 손으로 구성한다. 그러면 서버가 실제로 그 값을 채우는 경로와 렌더가 그 값을 읽는 경로가 **각각** 고정될 뿐, 이음매에서 값이 뒤바뀌어도 아무 시험도 안 죽는다. 큐에 그 부류의 결함이 이미 열려 있다(`fd-itempath-move-not-verified-end-to-end`).

**Files:**
- Modify: `plugins/flightdeck/server/cmd/fd/wire_test.go`
- Modify: `plugins/flightdeck/server/cmd/fd/cmds.go` (`fd pick` 이 인자 여럿을 받는다 — Step 3)

**Interfaces:**
- Consumes: `newHarness(t) *harness` · `h.run(stdin string, args ...string) (int, string)` · `h.st *store.Store` · `h.project` · `mustContain(t, what, got string, wants ...string)` (전부 `cmd/fd/harness_test.go`)
- Produces: `fd pick <id> [<id>…]` — 첫째가 선두

- [ ] **Step 1: 실패하는 시험을 쓴다**

```go
// 묶음이 서버에서 만들어져 JSON 을 건너 렌더까지 그대로 오는지 본다.
// 손으로 구성한 PickResult 로는 이 이음매를 못 본다.
//
// ★ 형제 관계는 하네스의 **실물 store** 로 만든다. fd CLI 의 finish 에는
// 후속 인자가 없어서(cmds.go 에 --followup 이 0건이다) 그 경로로는 형제를 못 만든다.
// 이 시험이 지키려는 것은 **읽기 경로**(서버 → JSON → 클라이언트 → 렌더)이므로
// 쓰기를 store 로 놓는 것이 범위를 정확히 맞춘다.
func TestPickBundleSurvivesTheWire(t *testing.T) {
	h := newHarness(t)

	if code, out := h.run("", "open", "--label", "묶음왕복"); code != 0 {
		t.Fatalf("세션 열기 실패(%d):\n%s", code, out)
	}
	for _, id := range []string{"w1-sib", "w2-sib"} {
		if code, out := h.run("", "add", "--id", id, "--title", id+" 제목",
			"--body", id+" 본문", "--paths", "services/"+id+".go"); code != 0 {
			t.Fatalf("항목 등록 실패(%s, %d):\n%s", id, code, out)
		}
	}
	// 형제로 만든다 — finish 가 만드는 모양 그대로(한 handoff 판단이 둘을 가리킨다).
	if _, err := h.st.AddJudgment(context.Background(), model.Judgment{
		Project: h.project, Kind: model.JudgmentHandoff,
		Title: "쪼갰다", Body: "이건 따로 빼자",
		Links: []model.JudgmentLink{
			{TargetKind: "item", TargetID: "w1-sib"},
			{TargetKind: "item", TargetID: "w2-sib"},
		},
	}); err != nil {
		t.Fatalf("형제 준비 실패: %v", err)
	}

	// ① 추천이 묶음으로 온다.
	code, out := h.run("", "next")
	if code != 0 {
		t.Fatalf("next 실패(%d):\n%s", code, out)
	}
	mustContain(t, "묶음 추천", out, "묶음 구성원", "묶은 근거", "sibling", "w1-sib", "w2-sib")

	// ② 묶음을 집는다. 브랜치는 선두 하나다.
	code, claimed := h.run("", "pick", "w1-sib", "w2-sib")
	if code != 0 {
		t.Fatalf("묶음 선점 실패(%d):\n%s", code, claimed)
	}
	if !strings.Contains(claimed, "브랜치: w1-sib") {
		t.Fatalf("선두 브랜치가 안 나온다:\n%s", claimed)
	}
	if n := strings.Count(claimed, "브랜치: "); n != 1 {
		t.Fatalf("브랜치 줄이 %d개다 — 묶음의 브랜치는 선두 하나뿐이다:\n%s", n, claimed)
	}
	if !strings.Contains(claimed, "w2-sib") {
		t.Fatalf("구성원이 응답에 없다:\n%s", claimed)
	}
}
```

> - `h.run(stdin string, args ...string) (int, string)` · `h.st`(실물 store) · `h.project`("testproj") · `mustContain(t, what, got, wants...)` 는 `harness_test.go` 에 이미 있다.
> - `fd add` 의 경로 인자 이름(`--paths`)은 `cmd/fd/cmds.go` 에서 확인해 맞춰라. 없으면 그 인자를 빼라 — 이 시험은 경로 축을 안 본다.
> - `fd pick` 은 지금 인자 하나만 받는다. 아래 Step 3 에서 여러 인자를 받게 고친다.

- [ ] **Step 2: 시험이 실패하는지 확인한다**

```
go test ./cmd/fd/ -run TestPickBundleSurvivesTheWire -v
```
기대: FAIL

- [ ] **Step 3: `fd pick` 이 여러 인자를 받게 한다**

`cmd/fd/cmds.go` 의 `pick` 갈래에서 남은 인자 전부를 `ItemIDs` 로 넘긴다. 인자가 하나면 지금과 같은 경로(단독 선점·재개)를 타야 한다 — 태스크 8 의 `pickBundle` 이 원소 1개에서 `pickExplicit` 과 같은 결과를 내므로 그대로 통과한다.

- [ ] **Step 4: 시험이 통과하는지 확인한다**

```
go test ./... 2>&1 | tail -30
```
기대: 전 패키지 PASS

- [ ] **Step 5: 커밋**

```bash
git add plugins/flightdeck/server/cmd/fd/
git commit -m "$(cat <<'EOF'
test(fd): 묶음이 서버에서 렌더까지 왕복하는 경로를 못박는다

손으로 구성한 PickResult 로는 서버가 값을 채우는 경로와 렌더가 그 값을 읽는
경로가 각각 고정될 뿐, 이음매에서 값이 뒤바뀌어도 아무 시험도 안 죽는다.
큐에 그 부류의 결함이 이미 열려 있다.

형제 관계를 finish 의 followups 라는 실물 쓰기로 만든다 — 그것이 이 관계의
유일한 생산자다.

fd pick 이 인자 여럿을 받는다. 하나면 지금과 같은 경로다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 12: 표면 문서 — 스킬과 설계

**⚠ 착수 전에 `DESIGN.md` §6 의 `pick` 행(303행 근처)을 누가 잡고 있는지 확인한다.** 세션 01KZ71DM 이 "303행 pick 행 한 줄만 만진다"고 알렸다. `git fetch && git log origin/main --oneline -3` 로 그 변경이 랜딩됐는지 보고, **랜딩된 판 위에** 얹어라. 안 됐으면 `note(kind:"ask")` 로 다시 물어라.

**Files:**
- Modify: `plugins/flightdeck/skills/fd-pickup/SKILL.md`
- Modify: `plugins/flightdeck/DESIGN.md`

- [ ] **Step 1: 스킬을 고친다**

`skills/fd-pickup/SKILL.md` 의 §2·§3 을 바꾼다. **줄 수를 늘리지 않는다** — 스킬 목록은 항목당 1,536자에서 잘린다.

```markdown
## 2. 무엇을 집을지 고른다

```
pick
```

인자가 없으면 **추천만** 한다(아직 선점하지 않았다). 함께 갈 항목이 있으면 **묶어서** 온다 —
1순위와 **왜 그것인지**, 구성원마다 **왜 묶였는지**, **탈락 사유 전부**가 함께 온다.
묶을 것이 없으면 "단독이다"라고 말한다. 그 줄이 없으면 서버가 그 축을 안 낸 것이다.

## 3. 집는다

```
pick(item_ids: ["<선두>", "<나머지>", …])
```

**첫째가 선두**이고 그 id 가 브랜치·워크트리 이름이 된다. 나머지는 같은 워크트리에서 함께 간다.
선두를 못 집으면 전부 거절이다. 선두를 집었으면 나머지는 되는 대로 집고 **못 집은 사유가 온다.**
하나만 집을 때는 `pick(item_id: "<id>")` 그대로다.
```

`§6 안 하는 것` 에 한 줄 덧붙인다:

```markdown
묶음 만들기(서버가 판정한다 — `item_ids` 는 그 판정을 **덮어쓸 때만** 손으로 쓴다).
```

- [ ] **Step 2: 설계 문서를 고친다**

`DESIGN.md` §6 의 MCP 도구 표에서 `pick` 행 하나만 바꾼다:

```markdown
| `pick` | `item_id?`, `item_ids?`, `steal_reason?` | 인자 없으면 **함께 갈 항목까지 묶어** 추천 + 왜 + **탈락 사유 전부**. `item_ids` 를 주면 선점(**첫째가 선두**이고 그 id 가 브랜치다) + 항목 본문 + 연결된 판단 전문 + 워크트리 준비 명령. **이미 자기 것이면 맥락 재출력**(재개 경로). 어느 쪽이든 **남은 큐 열림 수**가 함께 온다 |
```

§3 의 `pick_eval` 설명이 있으면 `picked_with` 를 한 줄 더한다. §11 "안 만드는 것" 표에 한 줄 더한다:

```markdown
| 묶음 저장(테이블·id·상태) | 새 개념이 하나 늘고, 그 순간 "묶음이 깨졌다"·"묶음을 해체한다" 같은 상태 전이가 따라온다. 파생으로 충분하다 |
```

- [ ] **Step 3: 스킬 예산을 확인한다**

```
wc -c plugins/flightdeck/skills/fd-pickup/SKILL.md
```
기대: 고치기 전보다 크지 않다. 커졌으면 다른 절에서 줄여라.

- [ ] **Step 4: 전 시험을 돌린다**

```
cd plugins/flightdeck/server && go test ./... 2>&1 | tail -20
```
기대: 전부 PASS

- [ ] **Step 5: 커밋**

```bash
git add plugins/flightdeck/skills/fd-pickup/SKILL.md plugins/flightdeck/DESIGN.md
git commit -m "$(cat <<'EOF'
docs(flightdeck): 묶음 pick 을 스킬과 설계 표면에 적는다

스킬은 줄 수를 안 늘린다 — 스킬 목록은 항목당 1,536자에서 잘리고
덜 쓰는 것부터 버려진다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

## 마무리 — 이 계획이 끝난 뒤

1. **전 시험 + 빌드**
   ```
   cd plugins/flightdeck/server && go build ./... && go test ./... 2>&1 | tail -20
   ```
2. **실물로 한 번 돌린다** — 이 워크트리의 `fd` 를 빌드해 `fd next` 를 쳐서, 설계 §2.2 가 예측한 묶음 4개 중 하나가 실제로 나오는지 본다. 안 나오면 그 차이가 이 설계의 반증이다.
3. **`finish` 로 마무리한다** — 항목마다 한 번씩. 무엇을 기각했고 무엇을 일부러 안 했는지를 본문에 남긴다. 특히 설계 §0.4 의 "못 잡는 부류"는 **의도적으로 안 한 것**이므로 반드시 적어라 — 안 적으면 다음 세션이 그것을 결함으로 보고 고치러 간다.
