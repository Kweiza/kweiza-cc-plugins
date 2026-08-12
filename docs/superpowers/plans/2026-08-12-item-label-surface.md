# 항목 꼬리표 표면 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 이미 있는 항목에 꼬리표를 더하고 빼는 표면(REST·CLI·MCP)을 만들고, 선두에 단 `tickler` 가 묶음 전체의 굶김 축에 실제로 걸리게 고친다.

**Architecture:** `fd move`·`fd after cut` 이 깔아 둔 3층(REST 전용 동사 → CLI → MCP 도구)을 그대로 복제한다. 계산은 `judge` 의 순수 함수로 가르고, 쓰기와 원장은 한 트랜잭션 안 `store` 에 둔다. 묶음 전파는 새 기구를 안 만들고 `bundleAround` 에서 **네 줄을 지워** 굶김 축을 `CloseDeclared` 와 같은 "선두만 본다"로 맞춘다.

**Tech Stack:** Go(표준 라이브러리) · modernc SQLite · `net/http` `mux.HandleFunc` 라우팅 · 자체 MCP 서버(`internal/mcpsrv`)

**설계 정본:** `docs/superpowers/specs/2026-08-12-item-label-surface-design.md`

## Global Constraints

- 작업 디렉토리는 워크트리 `.flightdeck/worktrees/fd-no-surface-to-set-a-label-on-an-existing-item` 다. Go 명령은 전부 `plugins/flightdeck/server` 안에서 돈다 — **cwd 가 모듈 밖이면 `gofmt` 가 빈 디렉토리를 검사하고 조용히 통과한다.**
- 주석·오류 문구·화면 문구는 **전부 한글**이다. 이 저장소의 기존 문체를 따른다.
- MCP 도구 설명은 **90 룬 상한**(`protocol_test.go` 가 잰다).
- 커밋 메시지는 한글이고 마지막 줄에 `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>` 를 붙인다.
- 랜딩 전 관문 다섯: `gofmt -l .` (빈 출력) · `go vet ./...` · `go build ./...` · `go test ./...` · `git status`. **무출력은 통과가 아니다** — 검사한 파일 수가 0이 아닌지 확인한다.
- `internal/mcpsrv/render.go` 는 **안 만진다** — 다른 세션이 +118/-59 로 뜯는 중이다. 새 렌더는 `render_label.go` 에 둔다.
- `internal/judge/bundle.go` 는 `bundleAround` 한 함수만 만진다.
- `cmd/fd/cmds.go` 는 파일 끝에 추가만 한다 — 기존 함수(특히 `runClose` 517~602)는 무수정.

## 순서 제약 (다른 세션과의 조율)

**Task 7(MCP 도구)은 세션 01KZSV35 의 랜딩 뒤에 한다.** 그쪽이 `protocol_test.go` 를 쥐고 있고 "도구 8개 판은 내 랜딩 뒤가 깨끗하다"고 답해 왔다. Task 1~6·8 은 그 제약과 무관하니 먼저 간다. Task 7 에 도달했는데 아직 안 랜딩됐으면 `board` 로 확인하고 기다린다.

DESIGN 의 REST ` ```routes ` 표는 그쪽이 "다른 자리라 둘 다 선다"고 확인해 줬다 — Task 5 에서 그대로 진행한다.

## 파일 구조

**새로 만든다**

| 파일 | 책임 |
|---|---|
| `internal/judge/labels.go` | `ApplyLabels` 순수 함수 하나 |
| `internal/judge/labels_test.go` | 그 함수의 시험 |
| `internal/judge/bundle_starve_lead_only_test.go` | 굶김 축이 선두만 본다는 판정을 못박는다 |
| `internal/service/label.go` | `SetLabels` 서비스 — 입력 검증·되읽기 |
| `internal/service/label_test.go` | 서비스 시험 |
| `internal/store/labels_test.go` | store 쓰기·원장 시험 |
| `internal/mcpsrv/render_label.go` | `RenderLabel` — `render.go` 를 피한다 |
| `internal/mcpsrv/render_label_test.go` | 렌더 시험 |
| `internal/api/label_seam_test.go` | CLI 요청 타입과 API 요청 타입의 필드 이름 대조 |

**고친다**

| 파일 | 무엇을 |
|---|---|
| `internal/judge/bundle.go` | `bundleAround` 447~450 **삭제** |
| `internal/store/item.go` | `SetLabels` 추가(파일 끝, `SetLandedRef` 뒤) |
| `internal/api/api.go` | 라우트 한 줄 |
| `internal/api/handlers_items.go` | `labelRequest` 타입 + `handleLabelItem`(파일 끝) |
| `internal/mcpsrv/tools.go` | `var tools` 끝에 원소 하나 + `labelArgs` 타입 |
| `internal/mcpsrv/mcpsrv.go` | 디스패치 `case "label"` + `toolLabel`(파일 끝) |
| `internal/mcpsrv/backend.go` | `Backend` 인터페이스에 메서드 하나 + 머리 주석 |
| `internal/mcpsrv/protocol_test.go` | `TestToolTableIsSeven` → `TestToolTableIsEight` |
| `cmd/fd/wire.go` | `labelPath` · `labelReq` |
| `cmd/fd/cmds.go` | `labelHelp` 상수 + `runLabel`(파일 끝) |
| `cmd/fd/main.go` | `case "label"` + `usage` 문자열 |
| `cmd/fd/offline.go` | `CmdLabel` 상수 + `JudgeOffline` 갈래 |
| `cmd/fd/outbox.go` | `IdempotencyStable` 갈래 |
| `cmd/fd/mcpbackend.go` | `SetLabels` 구현(파일 끝) |
| `internal/store/item_body_immutable_test.go` | **머리 주석만** |
| `plugins/flightdeck/DESIGN.md` | §5 티클러 절 · §6 도구 표·제목 · §6 routes 표 · §11 표 |

---

### Task 1: 굶김 축을 선두만 보게 한다

두 번째 결함의 수정이다. 표면과 **완전히 독립**이라 먼저 간다 — `fd label` 이 없어도 `add(labels:["tickler"])` 로 검증된다.

**Files:**
- Modify: `plugins/flightdeck/server/internal/judge/bundle.go:435-451`
- Test: `plugins/flightdeck/server/internal/judge/bundle_starve_lead_only_test.go` (create)

**Interfaces:**
- Consumes: 없음 (기존 `bundleAround`·`Candidate`·`IsTickler`)
- Produces: `judge.Bundle.StarveOldest` 의 의미가 "선두가 티클러가 아니면 선두의 생성 시각, 티클러면 zero" 로 확정된다. Task 8 의 DESIGN §5 문장이 이 의미를 적는다.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`plugins/flightdeck/server/internal/judge/bundle_starve_lead_only_test.go`:

```go
package judge

import (
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 굶김 축은 **선두만** 본다.
//
// 이 시험이 있는 이유: 앞선 판은 선두가 티클러면 StarveOldest 를 비웠다가,
// 구성원이 티클러가 아니면 거기서 **다시 채웠다.** 실측 사고에서 두 항목의
// created_at 이 글자까지 같아(일괄 반입분) 선두에만 티클러를 달았을 때 기아 값이
// 한 자리도 안 줄었고, 사용자 판정이 조용히 무효가 됐다.
//
// 판정의 근거는 같은 파일의 CloseDeclared 다 — "이 축은 '지금 새로 집어도 되나'에
// 답하고 그 질문의 주어는 브랜치를 받는 선두다". 굶김 축도 같은 질문에 답하는데
// 이것만 구성원을 봤다. 그 비대칭이 결함의 자리였다.

// starveCand 는 선행 하나로 이어지는 후보를 만든다.
//
// LinkOf 는 형제(판단) 또는 선행 축이 이어져야 묶는다 — 경로는 보강 전용이다.
// 빈 SiblingIndex 로도 묶이게 하려고 **같은 선행**을 준다(afterKey 일치).
func starveCand(id string, created time.Time, labels []string) Candidate {
	return Candidate{Item: model.Item{
		ID:        id,
		CreatedAt: created,
		Labels:    labels,
		After:     []model.After{{SHA: "cafe1234"}},
	}}
}

func TestStarveOldestIsEmptyWhenLeadIsTicklerEvenIfMemberIsNot(t *testing.T) {
	old := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	lead := starveCand("lead-tickler", old, []string{"tickler"})
	member := starveCand("member-plain", old, nil)

	b := bundleAround(lead, []Candidate{lead, member}, map[string]Candidate{}, SiblingIndex{})

	if len(b.Members) != 1 {
		t.Fatalf("구성원이 %d명이다 — 이 시험은 구성원 1명인 묶음을 봐야 한다(선행 축으로 이어진다)", len(b.Members))
	}
	if !b.StarveOldest.IsZero() {
		t.Errorf("선두가 티클러인데 StarveOldest 가 %s 다 — 구성원이 굶김 값을 다시 채웠다. "+
			"이러면 선두에 단 티클러가 무효가 되고, 그것이 이 시험을 만든 사고다", b.StarveOldest)
	}
}

func TestStarveOldestIsLeadAgeAndIgnoresOlderMember(t *testing.T) {
	older := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	lead := starveCand("lead-plain", newer, nil)
	member := starveCand("member-older", older, nil)

	b := bundleAround(lead, []Candidate{lead, member}, map[string]Candidate{}, SiblingIndex{})

	if !b.StarveOldest.Equal(newer) {
		t.Errorf("StarveOldest 가 %s 다 — 선두 나이(%s)여야 한다. "+
			"이것은 회귀가 아니라 판정이다: 오래된 구성원은 **자기가 선두인 묶음**에서 "+
			"제 나이로 기아 판정을 받는다(EligibleBundle 이 fit 전원을 각각 선두로 세운다)", b.StarveOldest, newer)
	}
	// Oldest 는 그대로 전체를 본다 — 굶김 축과 갈라 둔 값이라 함께 움직이면 안 된다.
	if !b.Oldest.Equal(older) {
		t.Errorf("Oldest 가 %s 다 — 전체 최고령(%s)이어야 한다. 이 축은 이번 변경 대상이 아니다", b.Oldest, older)
	}
}
```

- [ ] **Step 2: 시험이 실패하는 것을 본다**

```bash
cd plugins/flightdeck/server
go test ./internal/judge/ -run 'TestStarveOldestIs' -v
```

기대: `TestStarveOldestIsEmptyWhenLeadIsTicklerEvenIfMemberIsNot` 가 **FAIL** — "선두가 티클러인데 StarveOldest 가 2026-08-01 … 다".
`TestStarveOldestIsLeadAgeAndIgnoresOlderMember` 도 **FAIL** — 지금은 구성원 나이로 내려간다.

**둘 다 FAIL 이어야 한다.** 하나라도 PASS 면 시험이 결함에 안 닿은 것이니 `starveCand` 의 `After` 가 실제로 묶음을 만드는지(`len(b.Members) != 1` 에서 Fatal 이 나는지) 먼저 확인한다.

- [ ] **Step 3: 네 줄을 지운다**

`internal/judge/bundle.go` 의 `add` 클로저에서 아래 블록을 **삭제**한다(447~450):

```go
		if !IsTickler(c.Item.Labels) &&
			(b.StarveOldest.IsZero() || c.Item.CreatedAt.Before(b.StarveOldest)) {
			b.StarveOldest = c.Item.CreatedAt
		}
```

삭제한 자리에 판정의 근거를 남긴다 — `add` 클로저 바로 위, `b.StarveOldest = lead.Item.CreatedAt` 를 감싸는 `if` 에 주석을 붙인다:

```go
	// ★ 굶김 축은 **선두만** 본다. 구성원은 안 본다.
	//
	// 아래 CloseDeclared 판정과 같은 논법이다("보는 것은 선두 하나다 — 이 축은
	// '지금 새로 집어도 되나'에 답하고 그 질문의 주어는 브랜치를 받는 선두다").
	// 앞선 판은 이 축만 구성원까지 봤고, 그 비대칭이 결함이었다: 선두에 티클러를
	// 달아도 구성원이 티클러가 아니면 기아 값이 거기서 다시 채워져 **사용자 판정이
	// 조용히 무효가 됐다**(실측 2026-08-12 — created_at 이 글자까지 같은 두 항목).
	//
	// 오래된 구성원이 감춰지지 않는 이유: EligibleBundle 은 fit 전원을 **각각 선두로
	// 세워** 묶음을 만드므로, 굶은 항목은 자기가 선두인 묶음에서 제 나이로 판정된다.
	// 자기 묶음이 없는 것은 흡수분뿐인데 그들은 선행이 선두 하나뿐이라(blockedOnlyBy)
	// 선두 없이 못 간다 — 굶김이 선두에 종속되는 것은 감춤이 아니라 사실이다.
	if !IsTickler(lead.Item.Labels) {
		b.StarveOldest = lead.Item.CreatedAt
	}
```

- [ ] **Step 4: 시험이 통과하는 것을 본다**

```bash
cd plugins/flightdeck/server
go test ./internal/judge/ -run 'TestStarveOldestIs' -v
go test ./internal/judge/
```

기대: 새 시험 둘 PASS, `judge` 패키지 전체 PASS.

`judge` 의 다른 시험이 깨지면 **고치기 전에 읽어라** — 그 시험이 옛 의미론(구성원까지 본다)을 못박고 있었다면 이번 판정으로 갱신 대상이고, 그 사실을 시험 주석에 적어야 한다.

- [ ] **Step 5: 전체 시험과 관문**

```bash
cd plugins/flightdeck/server
gofmt -l . && go vet ./... && go test ./...
```

기대: `gofmt -l .` 빈 출력, vet 무경고, 전체 PASS.

- [ ] **Step 6: 커밋**

```bash
git add plugins/flightdeck/server/internal/judge/
git commit -F - <<'EOF'
fix(judge): 굶김 축이 선두만 본다 — 선두에 단 티클러가 구성원 때문에 무효가 되던 자리

bundleAround 는 선두가 티클러면 StarveOldest 를 비웠다가, 구성원이 티클러가
아니면 add 클로저에서 다시 채웠다. 실측(2026-08-12)에서 두 항목의 created_at 이
글자까지 같아 선두에만 달았을 때 기아 값이 한 자리도 안 줄었고, 사용자 판정이
조용히 무효가 됐다. 세션이 fd next 를 두 번 돌려서야 알았다.

근거는 같은 함수 20줄 아래에 이미 있었다 — CloseDeclared 가 선두만 보며
"이 축은 '지금 새로 집어도 되나'에 답하고 그 주어는 브랜치를 받는 선두다"라고
적는다. 굶김 축도 같은 질문에 답하는데 이것만 구성원을 봤다.

오래된 구성원은 감춰지지 않는다: EligibleBundle 이 fit 전원을 각각 선두로 세워
묶음을 만들므로 굶은 항목은 자기 묶음에서 제 나이로 판정된다. 자기 묶음이 없는
흡수분은 선행이 선두 하나뿐이라 선두 없이 못 가므로, 그 종속은 감춤이 아니라 사실이다.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 2: `judge.ApplyLabels` 순수 함수

**Files:**
- Create: `plugins/flightdeck/server/internal/judge/labels.go`
- Test: `plugins/flightdeck/server/internal/judge/labels_test.go`

**Interfaces:**
- Consumes: 없음
- Produces: `func judge.ApplyLabels(cur, add, rm []string) []string` — Task 4 의 서비스가 부른다.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`plugins/flightdeck/server/internal/judge/labels_test.go`:

```go
package judge

import (
	"reflect"
	"testing"
)

func TestApplyLabels(t *testing.T) {
	cases := []struct {
		name         string
		cur, add, rm []string
		want         []string
	}{
		{"빈 항목에 하나 더한다", nil, []string{"tickler"}, nil, []string{"tickler"}},
		{"기존 순서를 지키고 새것은 뒤에", []string{"b", "a"}, []string{"c"}, nil, []string{"b", "a", "c"}},
		{"이미 있는 것을 더해도 안 늘어난다", []string{"tickler"}, []string{"tickler"}, nil, []string{"tickler"}},
		{"없는 것을 빼도 오류가 아니다", []string{"a"}, nil, []string{"zzz"}, []string{"a"}},
		{"빼면 사라진다", []string{"a", "tickler", "b"}, nil, []string{"tickler"}, []string{"a", "b"}},
		{"더하기와 빼기가 함께 온다", []string{"a"}, []string{"b"}, []string{"a"}, []string{"b"}},
		{"같은 값을 더하고 빼면 빼기가 이긴다", []string{}, []string{"x"}, []string{"x"}, []string{}},
		{"add 안의 중복은 한 번만", nil, []string{"x", "x"}, nil, []string{"x"}},
		{"cur 의 중복은 정리된다", []string{"a", "a"}, nil, nil, []string{"a"}},
		{"공백은 다듬고 빈 값은 버린다", nil, []string{"  tickler  ", "", "   "}, nil, []string{"tickler"}},
		{"전부 비면 빈 슬라이스", []string{"a"}, nil, []string{"a"}, []string{}},
	}
	for _, c := range cases {
		got := ApplyLabels(c.cur, c.add, c.rm)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: ApplyLabels(%v, %v, %v) = %v, 기대 %v", c.name, c.cur, c.add, c.rm, got, c.want)
		}
	}
}

// nil 이 아니라 빈 슬라이스를 내는 것이 계약이다 — store 가 JSON 으로 직렬화하는데
// nil 은 `null` 이 되고 빈 슬라이스는 `[]` 가 된다. 되읽기(scanItem)는 둘 다
// 받지만, 원장 diff 와 되쓰기 산출물에서 두 값이 다른 글자가 된다.
func TestApplyLabelsNeverReturnsNil(t *testing.T) {
	if got := ApplyLabels(nil, nil, nil); got == nil {
		t.Error("ApplyLabels 가 nil 을 냈다 — 빈 슬라이스여야 한다")
	}
}
```

- [ ] **Step 2: 시험이 실패하는 것을 본다**

```bash
cd plugins/flightdeck/server
go test ./internal/judge/ -run TestApplyLabels
```

기대: 컴파일 실패 — `undefined: ApplyLabels`.

- [ ] **Step 3: 최소 구현**

`plugins/flightdeck/server/internal/judge/labels.go`:

```go
package judge

import "strings"

// ApplyLabels 는 꼬리표 집합에 더하기와 빼기를 적용한다. 순수 함수다.
//
// ★ 계약 셋이 있고 셋 다 이유가 있다.
//
//  1. **기존 순서를 지킨다.** 정렬하지 않는다 — labels 는 JSON 배열로 저장되고
//     되쓰기 산출물(legacy/export.go)이 원본과 diff 로 대조된다. 정렬을 걸면 판이
//     바뀔 때 무관한 항목들의 줄이 통째로 흔들리고, 그 산출물의 존재 이유가 무너진다.
//     새로 더한 것은 **뒤에** 붙는다.
//
//  2. **빼기가 더하기를 이긴다.** 같은 값이 add·rm 에 함께 오면 결과에서 빠진다.
//     반대로 정하면 "지워라"가 조용히 무시되는데, 이 함수의 소비자는 사람이 방금
//     친 명령이라 무시된 의도가 화면에 안 나타난다.
//
//  3. **nil 을 안 낸다.** 빈 슬라이스다 — nil 은 JSON 에서 `null` 이 되고 빈
//     슬라이스는 `[]` 가 된다. 두 값은 되읽기에서 같아 보이지만 원장에 남는
//     글자가 다르다.
//
// 공백만 있는 값과 빈 문자열은 버린다. 판정의 정본은 IsTickler 이고 그것은 정확
// 일치만 보므로(tickler.go), 앞뒤 공백이 붙은 채 저장되면 사람 눈에는 같은
// 꼬리표인데 판정에서 조용히 빠진다.
func ApplyLabels(cur, add, rm []string) []string {
	drop := make(map[string]bool, len(rm))
	for _, l := range rm {
		if l = strings.TrimSpace(l); l != "" {
			drop[l] = true
		}
	}

	out := make([]string, 0, len(cur)+len(add))
	seen := make(map[string]bool, len(cur)+len(add))
	keep := func(l string) {
		l = strings.TrimSpace(l)
		if l == "" || drop[l] || seen[l] {
			return
		}
		seen[l] = true
		out = append(out, l)
	}

	for _, l := range cur {
		keep(l)
	}
	for _, l := range add {
		keep(l)
	}
	return out
}
```

- [ ] **Step 4: 시험이 통과하는 것을 본다**

```bash
cd plugins/flightdeck/server
go test ./internal/judge/ -run TestApplyLabels -v
```

기대: 두 시험 PASS.

- [ ] **Step 5: 커밋**

```bash
cd plugins/flightdeck/server && gofmt -l . && go vet ./... && go test ./internal/judge/
cd - && git add plugins/flightdeck/server/internal/judge/labels.go plugins/flightdeck/server/internal/judge/labels_test.go
git commit -F - <<'EOF'
feat(judge): 꼬리표 더하기·빼기를 순수 함수로 가른다

계약 셋: 기존 순서를 지키고(정렬하면 되쓰기 산출물의 diff 가 흔들린다),
빼기가 더하기를 이기며(반대면 "지워라"가 조용히 무시된다), nil 대신 빈
슬라이스를 낸다(JSON 에서 null 과 [] 는 다른 글자로 원장에 남는다).

공백만 있는 값은 버린다 — 판정의 정본 IsTickler 는 정확 일치만 보므로
앞뒤 공백이 붙어 저장되면 사람 눈에는 같은 꼬리표인데 판정에서 조용히 빠진다.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 3: `store.SetLabels` — 쓰기와 원장

**Files:**
- Modify: `plugins/flightdeck/server/internal/store/item.go` (파일 끝, `SetLandedRef` 래퍼 뒤)
- Test: `plugins/flightdeck/server/internal/store/labels_test.go` (create)

**Interfaces:**
- Consumes: 없음
- Produces:
  - `func (t *Tx) SetLabels(project, itemID string, labels []string, sessionID string) error`
  - `func (s *Store) SetLabels(ctx context.Context, project, itemID string, labels []string, sessionID string) error`
  - 원장 이벤트 이름 `item.label`. Task 4 가 이 둘을 쓴다.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`plugins/flightdeck/server/internal/store/labels_test.go`:

```go
package store

import (
	"context"
	"errors"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// setLabelsFixture 는 항목 하나가 있는 저장소를 연다.
// (기존 시험이 쓰는 열기 헬퍼 이름이 다르면 그것을 쓴다 — 이 파일만의 헬퍼를 새로 만들지 마라.)
func setLabelsFixture(t *testing.T) (*Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	s := openTestStore(t)
	if err := s.AddItem(ctx, model.Item{
		Project: "p", ID: "it", Title: "제목", Body: "본문", Labels: []string{"a"},
	}); err != nil {
		t.Fatalf("항목 준비 실패: %v", err)
	}
	return s, ctx
}

func TestSetLabelsReplacesAndIsReadBack(t *testing.T) {
	s, ctx := setLabelsFixture(t)

	if err := s.SetLabels(ctx, "p", "it", []string{"a", "tickler"}, "sess"); err != nil {
		t.Fatalf("SetLabels 실패: %v", err)
	}
	it, err := s.GetItem(ctx, "p", "it")
	if err != nil {
		t.Fatalf("되읽기 실패: %v", err)
	}
	if len(it.Labels) != 2 || it.Labels[0] != "a" || it.Labels[1] != "tickler" {
		t.Errorf("labels 가 %v 다 — [a tickler] 여야 한다(순서 포함)", it.Labels)
	}
}

// 없는 항목에 조용히 성공하면 항목 id 오타 하나에 도구가 성공을 보고하고
// 원장에는 아무것도 안 남는다 — affectedOne 주석이 적은 그 결함이다.
func TestSetLabelsRefusesMissingItem(t *testing.T) {
	s, ctx := setLabelsFixture(t)

	err := s.SetLabels(ctx, "p", "없는-항목", []string{"x"}, "sess")
	if err == nil {
		t.Fatal("없는 항목에 SetLabels 가 성공했다 — 오타 하나에 성공이 보고된다")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("오류가 %v 다 — ErrNotFound 여야 한다", err)
	}
}

// 이 쓰기는 되돌리는 코드가 없다. 무엇이 붙어 있었는지가 바꾸는 순간 사라지므로
// before 를 원장에 남긴다 — RemoveAfter 가 item.after.cut 을 남기는 것과 같은 규율이다.
func TestSetLabelsWritesLedgerWithBeforeAndAfter(t *testing.T) {
	s, ctx := setLabelsFixture(t)

	if err := s.SetLabels(ctx, "p", "it", []string{"tickler"}, "sess"); err != nil {
		t.Fatalf("SetLabels 실패: %v", err)
	}

	evs, err := s.RecentEvents(ctx, "p", 20)
	if err != nil {
		t.Fatalf("원장 조회 실패: %v", err)
	}
	var found bool
	for _, e := range evs {
		if e.Kind != "item.label" {
			continue
		}
		found = true
		if e.SessionID != "sess" {
			t.Errorf("이벤트의 세션이 %q 다 — sess 여야 한다", e.SessionID)
		}
	}
	if !found {
		t.Errorf("원장에 item.label 이 없다 — 이 표면이 메우려던 공백이 그대로 남는다(사고 당시 흔적은 판단 하나뿐이었다)")
	}
}
```

**주의:** `openTestStore` 와 `RecentEvents` 는 이 패키지의 기존 헬퍼·메서드 이름을 그대로 써야 한다. 다르면 아래로 확인하고 **기존 이름에 맞춰라** — 새 헬퍼를 만들지 마라:

```bash
cd plugins/flightdeck/server
grep -rn "func openTestStore\|func newTestStore\|func testStore" internal/store/*_test.go | head -3
grep -rn "func (s \*Store) RecentEvents\|func.*Events(ctx" internal/store/*.go | grep -v _test | head -5
```

- [ ] **Step 2: 시험이 실패하는 것을 본다**

```bash
cd plugins/flightdeck/server
go test ./internal/store/ -run TestSetLabels
```

기대: 컴파일 실패 — `s.SetLabels undefined`.

- [ ] **Step 3: 최소 구현**

`internal/store/item.go` 의 `SetLandedRef` 단발 래퍼 **뒤**에 붙인다:

```go
// SetLabels 는 항목의 꼬리표를 통째로 바꾼다.
//
// ★ **더하기·빼기 계산은 여기 없다.** 이 함수는 최종 집합을 받아 쓰기만 한다 —
// 계산은 judge.ApplyLabels 가 하고, 지금 값을 읽어 그 함수에 먹이는 것은 service 가
// 같은 트랜잭션 안에서 한다. 읽고-고쳐-쓰기를 호출자에게 맡기면 두 세션이 서로의
// 꼬리표를 지운다.
//
// ★ 원장을 **여기서** 남긴다. move 는 API 계층에서 남기지만(handlers_items.go 의
// s.publish) 이 쓰기는 그럴 수 없다 — before 를 아는 것은 같은 트랜잭션 안에서 읽은
// 쪽뿐이고, API 로 올려 보내면 원장의 정확성이 응답 왕복에 의존하게 된다.
// RemoveAfter 가 item.after.cut 을 store 에 둔 이유와 같다: 이 쓰기는 되돌리는 코드가
// 없고 **무엇이 붙어 있었는지가 바꾸는 순간 사라진다.**
//
// labels 는 표시 전용이고 배제 판정에 안 쓴다(설계 §5). 유일한 예외가 tickler 이고
// 그것도 배제가 아니라 굶김 축에서의 승격 부재다(judge/tickler.go).
func (t *Tx) SetLabels(project, itemID string, labels []string, sessionID string) error {
	before, err := t.GetItem(project, itemID)
	if err != nil {
		return err
	}
	// 종료된 항목은 안 고친다. tickler 의 유일한 판정 소비자는 굶김 축이고 그 축은
	// 열린 항목만 본다 — 끝난 항목의 꼬리표를 바꾸는 것은 아무 데도 안 닿으면서
	// 원장만 늘린다. SetItemState 가 종료를 안 되돌리는 규율과 같은 방향이다.
	switch before.State {
	case model.ItemDone, model.ItemDropped:
		return &ItemClosedError{
			Project: clip(project, 64), ItemID: clip(itemID, 200),
			State: before.State, Want: before.State,
		}
	}

	labelsJSON, err := marshalStrings(labels)
	if err != nil {
		return fmt.Errorf("항목 labels 직렬화 실패(id=%q): %w", clip(itemID, 64), err)
	}
	res, err := t.tx.ExecContext(t.ctx,
		`UPDATE item SET labels = ? WHERE project = ? AND id = ?`, labelsJSON, project, itemID)
	if err != nil {
		return fmt.Errorf("항목 labels 갱신 실패(project=%q id=%q): %w",
			clip(project, 64), clip(itemID, 64), err)
	}
	if err := affectedOne(res, NFItem, project, itemID); err != nil {
		return err
	}

	t.LogEvent("item.label", project, sessionID, map[string]any{
		"item":   clip(itemID, 100),
		"before": before.Labels,
		"after":  labels,
	})
	return nil
}

// SetLabels 는 단발 트랜잭션으로 감싼 것이다.
func (s *Store) SetLabels(ctx context.Context, project, itemID string, labels []string, sessionID string) error {
	return s.Tx(ctx, func(t *Tx) error { return t.SetLabels(project, itemID, labels, sessionID) })
}
```

- [ ] **Step 4: 시험이 통과하는 것을 본다**

```bash
cd plugins/flightdeck/server
go test ./internal/store/ -run TestSetLabels -v
go test ./internal/store/
```

기대: 새 시험 셋 PASS, `store` 패키지 전체 PASS.

**`item_body_immutable_test.go` 가 여기서 깨지면 안 된다** — 새 UPDATE 의 컬럼은 `labels` 이고 그 시험이 무는 것은 `title`·`body` 뿐이다. 만약 깨지면 정규식이 SET 절을 못 읽은 것이니(WHERE 로 안 끝나는 UPDATE) 문장 모양을 확인한다.

- [ ] **Step 5: 커밋**

```bash
cd plugins/flightdeck/server && gofmt -l . && go vet ./... && go test ./internal/store/
cd - && git add plugins/flightdeck/server/internal/store/
git commit -F - <<'EOF'
feat(store): 항목 꼬리표를 고치는 쓰기 하나 — 원장에 before·after 를 함께 남긴다

계산은 안 한다. 최종 집합을 받아 쓰기만 하고, 지금 값을 읽어 judge.ApplyLabels 에
먹이는 것은 service 가 같은 트랜잭션 안에서 한다 — 읽고-고쳐-쓰기를 호출자에게
맡기면 두 세션이 서로의 꼬리표를 지운다.

원장을 store 에서 남기는 이유: before 를 아는 것은 같은 트랜잭션 안에서 읽은
쪽뿐이다. API 로 올려 보내면 원장의 정확성이 응답 왕복에 의존한다. RemoveAfter 가
item.after.cut 을 store 에 둔 이유와 같다.

종료된 항목은 ItemClosedError 로 거절한다. tickler 의 유일한 판정 소비자는 굶김
축이고 그 축은 열린 항목만 본다.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 4: `service.SetLabels` — 계산과 되읽기

**Files:**
- Create: `plugins/flightdeck/server/internal/service/label.go`
- Test: `plugins/flightdeck/server/internal/service/label_test.go`

**Interfaces:**
- Consumes: `judge.ApplyLabels` (Task 2) · `store.(*Store).SetLabels` (Task 3)
- Produces:

```go
type LabelInput struct {
	Project   string
	SessionID string
	ItemID    string
	Add       []string
	Rm        []string
}

type LabelResult struct {
	Item    model.Item `json:"item"`
	Before  []string   `json:"before"`
	After   []string   `json:"after"`
	Added   []string   `json:"added"`   // 실제로 늘어난 것
	Removed []string   `json:"removed"` // 실제로 빠진 것
}

func (s *Service) SetLabels(ctx context.Context, in LabelInput) (LabelResult, error)
```

Task 5(REST)·Task 6(CLI)·Task 7(MCP)이 이 타입들을 그대로 쓴다.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`plugins/flightdeck/server/internal/service/label_test.go`:

```go
package service

import (
	"context"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 실제로 무엇이 바뀌었는지를 낸다 — 요청한 것이 아니라.
//
// 이미 있는 것을 --add 하거나 없는 것을 --rm 해도 거절하지 않는다(집합 연산의
// 멱등). 대신 Added·Removed 가 비어서 화면이 "실제로 더한 것: 없음"을 말할 수
// 있게 한다. 조용한 무변화는 안 만든다.
func TestSetLabelsReportsWhatActuallyChanged(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t)
	mustAddItem(t, s, model.Item{Project: "p", ID: "it", Title: "제목", Body: "본문", Labels: []string{"a"}})

	res, err := s.SetLabels(ctx, LabelInput{
		Project: "p", SessionID: "sess", ItemID: "it",
		Add: []string{"a", "tickler"}, // "a" 는 이미 있다
		Rm:  []string{"zzz"},          // "zzz" 는 없다
	})
	if err != nil {
		t.Fatalf("SetLabels 실패: %v", err)
	}
	if got := strings.Join(res.Added, ","); got != "tickler" {
		t.Errorf("Added 가 %q 다 — tickler 만이어야 한다(a 는 이미 있었다)", got)
	}
	if len(res.Removed) != 0 {
		t.Errorf("Removed 가 %v 다 — 비어야 한다(zzz 는 없었다)", res.Removed)
	}
	if got := strings.Join(res.Before, ","); got != "a" {
		t.Errorf("Before 가 %q 다 — a 여야 한다", got)
	}
	if got := strings.Join(res.After, ","); got != "a,tickler" {
		t.Errorf("After 가 %q 다 — a,tickler 여야 한다", got)
	}
	if got := strings.Join(res.Item.Labels, ","); got != "a,tickler" {
		t.Errorf("되읽은 항목의 labels 가 %q 다 — 저장된 값이어야 한다", got)
	}
}

// 조용한 무작업을 안 만든다. 둘 다 비면 쓰기를 시작하기 전에 거절한다 —
// 오프라인이면 그 왕복이 아웃박스에 쌓이는 쓰기가 되기 때문이다(runAfterCut 과 같은 규율).
func TestSetLabelsRefusesEmptyRequestBeforeTouchingTheStore(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t)
	mustAddItem(t, s, model.Item{Project: "p", ID: "it", Title: "제목", Body: "본문"})

	if _, err := s.SetLabels(ctx, LabelInput{Project: "p", SessionID: "sess", ItemID: "it"}); err == nil {
		t.Fatal("add·rm 이 둘 다 비었는데 통과했다 — 조용한 무작업이다")
	}
}

func TestSetLabelsRefusesEmptyItemID(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t)

	if _, err := s.SetLabels(ctx, LabelInput{Project: "p", SessionID: "sess", Add: []string{"x"}}); err == nil {
		t.Fatal("항목 id 가 비었는데 통과했다")
	}
}
```

**주의:** `newTestService` 와 `mustAddItem` 은 이 패키지의 기존 헬퍼 이름이어야 한다. 확인:

```bash
cd plugins/flightdeck/server
grep -rn "func newTestService\|func testService\|func mustAddItem" internal/service/*_test.go | head -5
```

없으면 `internal/service/cut_after_test.go` 가 서비스를 어떻게 세우는지 읽고 **그 방식을 그대로** 쓴다.

- [ ] **Step 2: 시험이 실패하는 것을 본다**

```bash
cd plugins/flightdeck/server
go test ./internal/service/ -run TestSetLabels
```

기대: 컴파일 실패 — `s.SetLabels undefined`, `undefined: LabelInput`.

- [ ] **Step 3: 최소 구현**

`plugins/flightdeck/server/internal/service/label.go`:

```go
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
)

// LabelInput 은 이미 있는 항목의 꼬리표를 고치는 요청이다.
//
// ★ 범위를 **꼬리표 한 축으로 못박는다.** title·body·paths·after 를 함께 고치는 일반
// amend 로 번지면 "무엇을 고칠 수 있나"가 표면마다 달라지고 그 차이를 아무도 못
// 따라간다 — move 가 프로젝트 한 축으로 못박은 이유와 같고, cutAfterRequest 가
// "전용 동사"라고 적은 이유와도 같다. 본문·선행의 사후 수정은 DESIGN §11 이
// "안 만든다"로 이미 판정했고 이 표면은 그 판정을 안 건드린다.
type LabelInput struct {
	Project   string
	SessionID string // 누가 고쳤는지. 원장에 남는다
	ItemID    string
	Add       []string
	Rm        []string
}

// LabelResult 는 고친 결과다.
//
// ★ Before·After 와 **Added·Removed 를 따로** 담는다. 요청한 것과 실제로 바뀐 것이
// 다르기 때문이다: 이미 있는 것을 더하거나 없는 것을 빼는 것은 집합 연산이라
// 거절하지 않지만, 그때 화면이 "더했다"고만 말하면 사람은 안 바뀐 것을 바뀐 줄 안다.
// 조용한 무변화를 안 만드는 것이 이 두 필드의 존재 이유다.
type LabelResult struct {
	Item    model.Item `json:"item"`
	Before  []string   `json:"before"`
	After   []string   `json:"after"`
	Added   []string   `json:"added"`
	Removed []string   `json:"removed"`
}

// SetLabels 는 항목의 꼬리표를 고친다.
//
// 계산(judge.ApplyLabels)과 쓰기(store.SetLabels)를 가르되, **지금 값을 읽는 것부터
// 쓰기까지가 한 트랜잭션 안**이어야 한다 — 읽기와 쓰기 사이가 벌어지면 두 세션이
// 서로의 꼬리표를 지운다. 그래서 store 의 Tx 를 직접 연다.
func (s *Service) SetLabels(ctx context.Context, in LabelInput) (LabelResult, error) {
	var res LabelResult
	in.Project = strings.TrimSpace(in.Project)
	in.ItemID = strings.TrimSpace(in.ItemID)

	if in.Project == "" {
		return res, errors.New("프로젝트가 비었다")
	}
	if in.ItemID == "" {
		return res, errors.New("꼬리표를 고칠 항목 id 가 비었다")
	}
	// ★ 빈 요청을 **쓰기 전에** 거절한다. 서버까지 갔다 와도 같은 결론이지만, 그
	// 왕복은 오프라인에서 아웃박스에 쌓이는 쓰기가 된다(runAfterCut 이 축 수를
	// 클라이언트에서 세는 것과 같은 규율).
	if len(nonBlank(in.Add))+len(nonBlank(in.Rm)) == 0 {
		return res, errors.New("더하거나 뺄 꼬리표를 하나는 줘라 — 빈 요청은 원장만 늘린다")
	}

	var before, after []string
	err := s.st.Tx(ctx, func(t *store.Tx) error {
		cur, gerr := t.GetItem(in.Project, in.ItemID)
		if gerr != nil {
			return gerr
		}
		before = cur.Labels
		after = judge.ApplyLabels(cur.Labels, in.Add, in.Rm)
		return t.SetLabels(in.Project, in.ItemID, after, in.SessionID)
	})
	if err != nil {
		return res, err
	}

	res = LabelResult{
		Before: before, After: after,
		Added: onlyIn(after, before), Removed: onlyIn(before, after),
	}
	// 저장된 값을 다시 읽는다 — 요청 값을 그대로 돌려주면 무엇이 저장됐는지가
	// 아니라 무엇을 보냈는지를 화면에 내게 된다(MoveItem 과 같은 규율).
	it, gerr := s.st.GetItem(ctx, in.Project, in.ItemID)
	if gerr != nil {
		// 쓰기는 이미 커밋됐다. 되읽기 실패로 결과를 버리면 Added·Removed 까지
		// 함께 죽는데, 그 둘이 이 응답의 값이다(DESIGN §5).
		s.log.WarnContext(ctx, "꼬리표 고친 뒤 되읽기 실패 — 쓰기는 커밋됐다",
			"project", clip(in.Project, 64), "item", clip(in.ItemID, 64), "error", gerr.Error())
		it = model.Item{Project: in.Project, ID: in.ItemID, Labels: after}
	}
	res.Item = it
	return res, nil
}

// nonBlank 는 공백 아닌 값만 남긴다.
func nonBlank(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// onlyIn 은 a 에는 있고 b 에는 없는 값이다. 순서는 a 를 따른다.
func onlyIn(a, b []string) []string {
	has := make(map[string]bool, len(b))
	for _, v := range b {
		has[v] = true
	}
	out := make([]string, 0)
	for _, v := range a {
		if !has[v] {
			out = append(out, v)
		}
	}
	return out
}
```

**주의 셋:**
1. `nonBlank` 이름이 `cmds.go` 의 `nonBlankPositionals` 와 다른 패키지라 충돌하지 않지만, `internal/service` 안에 같은 이름이 이미 있으면 `nonBlankLabels` 로 바꾼다. 확인: `grep -rn "func nonBlank" internal/service/`
2. `s.st.Tx` 와 `store` 임포트가 이 패키지의 기존 방식과 맞는지 `cut_after.go` 를 보고 맞춘다. `Service` 가 store 를 어떤 필드 이름으로 갖는지(`s.st`), 로거가 `s.log` 인지 확인한다.
3. **임포트 목록은 위 코드를 그대로 베끼지 말고 실제로 쓰는 것만 남겨라.** 위 본문은 `fmt` 를 안 쓴다 — Go 는 안 쓰는 임포트를 컴파일 오류로 잡으니 빌드가 알려 준다. `store` 임포트는 `s.st.Tx` 의 콜백 인자 타입(`*store.Tx`) 때문에 필요하다.

- [ ] **Step 4: 시험이 통과하는 것을 본다**

```bash
cd plugins/flightdeck/server
go test ./internal/service/ -run TestSetLabels -v
go test ./internal/service/
```

기대: 새 시험 셋 PASS, `service` 패키지 전체 PASS.

- [ ] **Step 5: 커밋**

```bash
cd plugins/flightdeck/server && gofmt -l . && go vet ./... && go test ./internal/service/
cd - && git add plugins/flightdeck/server/internal/service/label.go plugins/flightdeck/server/internal/service/label_test.go
git commit -F - <<'EOF'
feat(service): 꼬리표 고치기 — 읽기부터 쓰기까지 한 트랜잭션, 실제 변화분을 낸다

읽고-고쳐-쓰기 사이가 벌어지면 두 세션이 서로의 꼬리표를 지운다. 그래서 지금
값을 읽는 것부터 쓰기까지를 한 Tx 안에 둔다.

Before·After 와 별개로 Added·Removed 를 담는다. 이미 있는 것을 더하거나 없는
것을 빼는 것은 집합 연산이라 거절하지 않지만, 그때 화면이 "더했다"고만 말하면
사람은 안 바뀐 것을 바뀐 줄 안다.

빈 요청은 쓰기 전에 거절한다 — 오프라인이면 그 왕복이 아웃박스에 쌓이는 쓰기가
된다(runAfterCut 이 축 수를 클라이언트에서 세는 것과 같은 규율).

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 5: REST 라우트 + DESIGN routes 표

두 파일을 **같은 커밋**에 둔다 — `api/design_route_table_test.go` 가 mux 등록과 DESIGN 표를 양방향 대조하므로 하나만 고치면 빨간불이다.

**Files:**
- Modify: `plugins/flightdeck/server/internal/api/api.go:327` (move 라우트 다음 줄)
- Modify: `plugins/flightdeck/server/internal/api/handlers_items.go` (파일 끝)
- Modify: `plugins/flightdeck/DESIGN.md` (` ```routes ` 블록, `POST /items/{id}/move` 다음 줄)

**Interfaces:**
- Consumes: `service.LabelInput` · `service.LabelResult` (Task 4)
- Produces: `POST /api/v1/items/{id}/label` — 본문 `{project, session_id, add[], rm[]}`. Task 6·7 이 이 경로와 필드 이름을 쓴다.

- [ ] **Step 1: 라우트 표 시험이 실패하는 것을 본다**

먼저 DESIGN 표에만 한 줄 넣어서 **관문이 살아 있는지** 확인한다. `plugins/flightdeck/DESIGN.md` 의 ` ```routes ` 블록에서 `POST   /items/{id}/move` 줄 **다음**에:

```
POST   /items/{id}/label            (고칠 수 있는 축은 꼬리표 하나뿐 — 본문·제목·선행은 못 바꾼다)
```

그리고:

```bash
cd plugins/flightdeck/server
go test ./internal/api/ -run TestDesign -v
```

기대: **FAIL** — DESIGN 표에는 있는데 mux 에 없다는 취지의 실패. 이것이 관문이 살아 있다는 증거다. 실패 문구에서 이 시험의 정확한 이름을 확인해 둔다.

- [ ] **Step 2: 라우트를 등록한다**

`internal/api/api.go` 의 `POST /api/v1/items/{id}/move` 등록 **다음 줄**에:

```go
	// 꼬리표. 고칠 수 있는 축은 이 하나뿐이다 — move·after/cut 과 같은 전용 동사다.
	mux.HandleFunc("POST /api/v1/items/{id}/label", s.handleLabelItem)
```

- [ ] **Step 3: 핸들러를 쓴다**

`internal/api/handlers_items.go` 파일 **끝**에:

```go
// labelRequest 는 항목의 꼬리표를 고치는 요청이다.
//
// move·after/cut 과 같은 규율으로 **전용 동사**다 — 일반 PATCH 를 열면 "무엇까지
// 고칠 수 있나"가 다시 열린 질문이 되고, 그 질문은 항목 본문까지 번진다.
// 본문이 만들어진 시점의 사진이라는 규율은 DESIGN §11 이 적고 store 의 관문이 지킨다.
//
// 필드 이름이 cmd/fd 의 labelReq 와 어긋나면 서버가 조용히 0값을 받는다 —
// add·rm 이 둘 다 빈 채 닿으면 "하나는 줘라"로 거절되는데, 사람은 자기가 방금 친
// `--add` 를 다시 들여다본다. 이음매 시험이 잠근다.
type labelRequest struct {
	Project   string   `json:"project"`
	SessionID string   `json:"session_id"`
	Add       []string `json:"add"`
	Rm        []string `json:"rm"`
}

func (s *server) handleLabelItem(w http.ResponseWriter, r *http.Request) {
	var req labelRequest
	if !s.decode(w, r, &req) {
		return
	}
	infoFrom(r.Context()).setSession(req.SessionID)
	res, err := s.svc.SetLabels(r.Context(), service.LabelInput{
		Project: req.Project, SessionID: req.SessionID,
		ItemID: r.PathValue("id"), Add: req.Add, Rm: req.Rm,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// ★ SSE 알림용이다. 원장 행 자체는 store 가 트랜잭션 안에서 남긴다(item.label) —
	// before 를 아는 것이 거기뿐이기 때문이다. 여기서 다시 publish 하면 같은 사실이
	// 원장에 두 줄이 되므로, 이 호출은 **알림 축만** 태운다.
	s.publish(r, "item.label", req.Project, req.SessionID, map[string]any{
		"item": clip(res.Item.ID, 100), "added": res.Added, "removed": res.Removed,
	})
	s.writeJSON(w, r, http.StatusOK, res)
}
```

**주의:** `s.publish` 가 실제로 원장에 쓰는지 SSE 만 태우는지 확인한다:

```bash
cd plugins/flightdeck/server
grep -rn "func (s \*server) publish" -A 20 internal/api/*.go | head -25
```

**원장에도 쓴다면** 위 `s.publish` 호출을 **지운다** — 같은 사실이 원장에 두 줄이 되고, `store` 쪽이 정본이다. SSE 전용 함수가 따로 있으면 그것으로 바꾼다.

- [ ] **Step 4: 시험이 통과하는 것을 본다**

```bash
cd plugins/flightdeck/server
go test ./internal/api/ -run TestDesign -v
go test ./internal/api/
```

기대: 라우트 표 시험 PASS(양쪽에 다 있다), `api` 패키지 전체 PASS.

- [ ] **Step 5: 커밋**

```bash
cd plugins/flightdeck/server && gofmt -l . && go vet ./... && go test ./internal/api/
cd - && git add plugins/flightdeck/server/internal/api/ plugins/flightdeck/DESIGN.md
git commit -F - <<'EOF'
feat(api): 꼬리표 전용 동사 하나 — POST /items/{id}/label

move·after/cut 과 같은 규율이다. 일반 PATCH 를 열면 "무엇까지 고칠 수 있나"가
다시 열린 질문이 되고 그 질문은 항목 본문까지 번진다.

라우트 등록과 DESIGN 의 routes 표를 한 커밋에 둔다 — design_route_table_test.go 가
둘을 양방향으로 대조하므로 하나만 고치면 빨간불이다.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 6: CLI `fd label`

**Files:**
- Modify: `plugins/flightdeck/server/cmd/fd/wire.go` (파일 끝 — `afterCutReq` 뒤)
- Modify: `plugins/flightdeck/server/cmd/fd/offline.go` (`CmdAfterCut` 뒤 + `JudgeOffline` 갈래)
- Modify: `plugins/flightdeck/server/cmd/fd/outbox.go` (`IdempotencyStable` 갈래)
- Modify: `plugins/flightdeck/server/cmd/fd/cmds.go` (파일 끝 — `runMove` 뒤)
- Modify: `plugins/flightdeck/server/cmd/fd/main.go` (`case "move"` 뒤 + `usage`)
- Test: `plugins/flightdeck/server/internal/api/label_seam_test.go` (create)

**Interfaces:**
- Consumes: `POST /api/v1/items/{id}/label` (Task 5)
- Produces: `CmdLabel = "label"` 상수 — Task 7 의 `mcpBackend` 가 쓴다. `labelPath(itemID)` · `labelReq` 도 같이 쓴다.

- [ ] **Step 1: 이음매 시험을 쓴다**

`plugins/flightdeck/server/internal/api/label_seam_test.go`:

```go
package api

import (
	"encoding/json"
	"testing"
)

// CLI 가 보내는 본문의 필드 이름이 서버가 읽는 이름과 같아야 한다.
//
// 어긋나면 서버가 조용히 0값을 받는다 — add·rm 이 둘 다 빈 채 닿으면 "하나는
// 줘라"로 거절되고, 사람은 자기가 방금 친 --add 를 다시 들여다본다.
// move·after cut 이 같은 이유로 같은 시험을 갖는다.
func TestLabelRequestFieldNamesMatchTheWire(t *testing.T) {
	// cmd/fd 의 labelReq 와 **글자 그대로** 같은 JSON 이어야 한다.
	const wire = `{"project":"p","session_id":"s","add":["tickler"],"rm":["old"]}`

	var got labelRequest
	if err := json.Unmarshal([]byte(wire), &got); err != nil {
		t.Fatalf("본문을 못 읽었다: %v", err)
	}
	if got.Project != "p" {
		t.Errorf("project 가 %q 다 — 필드 이름이 어긋났다", got.Project)
	}
	if got.SessionID != "s" {
		t.Errorf("session_id 가 %q 다 — 필드 이름이 어긋났다", got.SessionID)
	}
	if len(got.Add) != 1 || got.Add[0] != "tickler" {
		t.Errorf("add 가 %v 다 — 필드 이름이 어긋났다", got.Add)
	}
	if len(got.Rm) != 1 || got.Rm[0] != "old" {
		t.Errorf("rm 이 %v 다 — 필드 이름이 어긋났다", got.Rm)
	}
}
```

- [ ] **Step 2: 시험을 돌려 통과를 확인한다**

```bash
cd plugins/flightdeck/server
go test ./internal/api/ -run TestLabelRequestFieldNames -v
```

기대: PASS (Task 5 에서 `labelRequest` 를 이미 만들었다). 여기서 실패하면 태그가 틀린 것이니 Task 5 의 타입을 고친다.

- [ ] **Step 3: 배선 넷을 쓴다**

**(a)** `cmd/fd/wire.go` 파일 끝:

```go
// labelPath 는 항목의 꼬리표를 고치는 표면이다.
func labelPath(itemID string) string {
	return "/api/v1/items/" + urlPath(itemID) + "/label"
}

// labelReq 는 POST /api/v1/items/{id}/label 의 본문이다.
// 필드 이름이 internal/api 의 labelRequest 와 어긋나면 서버가 조용히 0값을 받는다
// (label_seam_test.go 가 잠근다).
type labelReq struct {
	Project   string   `json:"project"`
	SessionID string   `json:"session_id"`
	Add       []string `json:"add"`
	Rm        []string `json:"rm"`
}
```

**(b)** `cmd/fd/offline.go` 의 `CmdAfterCut` 상수 **뒤**:

```go
	// CmdLabel 은 이미 있는 항목의 꼬리표를 고치는 것이다(`fd label`).
	//
	// ★ 리터럴 대신 상수를 쓰는 이유는 CmdMove 와 같다 — offline.go·outbox.go 가
	// 이 이름으로 명시 갈래를 잡고, write_cmd_table_coverage_test.go 가 그것을
	// 기계로 지킨다.
	CmdLabel = "label"
```

`JudgeOffline` 의 `case CmdAfterCut:` **뒤**:

```go
	case CmdLabel:
		// ★ CmdAfterCut 과 같은 결이다 — 표 밖으로 떨어지면 사유가 "정의돼 있지
		//   않다"가 되어 설계 결함처럼 읽힌다. 동작(거절)은 default 와 같지만 사유가 다르다.
		return OfflineVerdict{OfflineRefuse,
			"꼬리표 더하기·빼기는 지금 그 항목에 무엇이 붙어 있는지를 원장에서 실시간으로 " +
				"읽어 계산한다 — 아웃박스에 쌓아 재생하면 그 사이 다른 경로로 꼬리표가 " +
				"바뀌었어도 낡은 요청이 그대로 덮는다. 서버가 돌아오면 지금 상태를 보고 다시 실행하라"}
```

**(c)** `cmd/fd/outbox.go` 의 `IdempotencyStable` 에서 `case CmdAfterCut:` **뒤**:

```go
	case CmdLabel:
		// ★ CmdMove·CmdAfterCut 과 같은 위험이다. 꼬리표를 달았다가 다른 경로로 도로
		//   뗀 뒤 **같은 본문**으로 다시 부르면, 고정 키가 그때와 같은 값을 내 서버는
		//   실제로 쓰지 않고 옛 성공 응답을 재생한다. 화면은 "더했다"고 말하는데
		//   항목에는 안 붙어 있다.
		return false, "응답이 지금 상태다(그 순간의 before·after·실제 변화분) — 고정하면 " +
			"꼬리표가 그 사이 도로 바뀐 뒤 같은 본문으로 다시 불러도 실제 쓰기 없이 " +
			"옛 성공 응답이 재생된다"
```

**(d)** `cmd/fd/cmds.go` 파일 **끝**(`runMove` 뒤):

```go
const labelHelp = "fd label <item-id> --add <꼬리표> --rm <꼬리표>  — 이미 있는 항목의 꼬리표를 고친다(둘 다 반복 지정 가능)"

// runLabel 은 이미 있는 항목의 꼬리표를 고친다.
//
// ★ 고칠 수 있는 축은 **꼬리표 하나뿐**이다 — move 가 프로젝트 한 축으로 못박은 것과
// 같은 좁기다. 본문·제목·선행의 사후 수정은 DESIGN §11 이 "안 만든다"로 판정했다.
func (a *App) runLabel(ctx context.Context, args []string, out io.Writer) int {
	fs := newFlagSet("label")
	var add, rm stringList
	fs.Var(&add, "add", "더할 꼬리표(반복 지정 가능). 'tickler' 만 굶김 축에서 빠진다")
	fs.Var(&rm, "rm", "뺄 꼬리표(반복 지정 가능)")
	session := fs.String("cc-session", "", "Claude Code 세션 id")
	itemID, rest := TakeFirstPositional(args)
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if itemID == "" {
		itemID = fs.Arg(0)
	}
	if strings.TrimSpace(itemID) == "" {
		fmt.Fprintln(out, "꼬리표를 고칠 항목 id 를 줘라:")
		fmt.Fprintln(out, "  "+labelHelp)
		return 2
	}
	// ★ 빈 요청을 **여기서** 막는다. 서버도 거절하지만 그 왕복은 오프라인에서
	// 아웃박스에 쌓이는 쓰기가 된다 — runAfterCut 이 축 수를 여기서 세는 것과 같은 규율이다.
	if len(nonBlankPositionals(add))+len(nonBlankPositionals(rm)) == 0 {
		fmt.Fprintln(out, "더하거나 뺄 꼬리표를 하나는 줘라:")
		fmt.Fprintln(out, "  "+labelHelp)
		return 2
	}

	sess, _ := a.sessionID(ctx, *session)
	a.cli.Session = sess
	res, err := a.cli.Write(ctx, CmdLabel, labelPath(itemID), labelReq{
		Project: a.proj.ID, SessionID: sess, Add: add, Rm: rm,
	})
	if err != nil {
		fmt.Fprintf(out, "꼬리표를 못 고쳤다: %v\n", err)
		return 1
	}
	var got struct {
		Before  []string `json:"before"`
		After   []string `json:"after"`
		Added   []string `json:"added"`
		Removed []string `json:"removed"`
		Item    struct {
			ID    string `json:"ID"`
			Title string `json:"Title"`
		} `json:"item"`
	}
	if uerr := json.Unmarshal(res.Body, &got); uerr != nil {
		fmt.Fprintf(out, "고쳤으나 응답을 못 읽었다: %v\n", uerr)
		return 1
	}

	fmt.Fprintf(out, "label · %s 의 꼬리표를 고쳤다\n", got.Item.ID)
	// ★ **실제 변화분을 낸다.** 요청한 것이 아니라. 이미 있는 것을 더하거나 없는 것을
	// 빼는 것은 거절하지 않지만, 그때 "더했다"고만 말하면 사람은 안 바뀐 것을 바뀐 줄 안다.
	fmt.Fprintf(out, "실제로 더한 것: %s\n", labelListOrNone(got.Added))
	fmt.Fprintf(out, "실제로 뺀 것: %s\n", labelListOrNone(got.Removed))
	fmt.Fprintf(out, "지금 꼬리표: %s\n", labelListOrNone(got.After))
	return 0
}

// labelListOrNone 은 꼬리표 목록 한 줄이다. 빈 것을 빈 줄로 내면 "없다"와
// "이 축을 안 읽었다"가 화면에서 같아진다.
func labelListOrNone(ls []string) string {
	if len(ls) == 0 {
		return "없음"
	}
	return strings.Join(ls, ", ")
}
```

**(e)** `cmd/fd/main.go` 의 `case "move":` **뒤**:

```go
	case "label":
		return app.runLabel(ctx, args[1:], stdout)
```

그리고 같은 파일의 `usage` 문자열에서 `move` 줄 옆에 한 줄 더한다. 정확한 모양은 아래로 확인하고 **그 형식에 맞춰** 넣는다:

```bash
cd plugins/flightdeck/server
grep -n "usage = \|move " cmd/fd/main.go | head -10
```

- [ ] **Step 4: 시험과 관문**

```bash
cd plugins/flightdeck/server
go test ./cmd/fd/ -run TestWriteCmdTable -v
go test ./...
```

기대: `write_cmd_table_coverage_test.go` PASS — `CmdLabel` 이 두 표에 다 있다. 전체 PASS.

이 시험이 **누락**으로 실패하면 (b)나 (c)를 빠뜨린 것이고, **유령**으로 실패하면 상수 값과 `a.cli.Write` 의 인자가 다른 것이다.

- [ ] **Step 5: 실제로 돌려 본다**

```bash
cd plugins/flightdeck/server
go build -o /tmp/claude-1000/-home-aaron-cdo-dev-kweiza-cc-plugins/0e3a21f6-927c-4c39-b61e-25d76ab5aabd/scratchpad/fd ./cmd/fd
/tmp/claude-1000/-home-aaron-cdo-dev-kweiza-cc-plugins/0e3a21f6-927c-4c39-b61e-25d76ab5aabd/scratchpad/fd label 2>&1 | head -5
```

기대: "꼬리표를 고칠 항목 id 를 줘라:" 와 도움말 한 줄. 인자 검증이 서버 없이도 도는지 보는 것이다.

- [ ] **Step 6: 커밋**

```bash
cd plugins/flightdeck/server && gofmt -l . && go vet ./... && go test ./...
cd - && git add plugins/flightdeck/server/cmd/fd/ plugins/flightdeck/server/internal/api/label_seam_test.go
git commit -F - <<'EOF'
feat(fd): fd label — 이미 있는 항목의 꼬리표를 고친다

고칠 수 있는 축은 꼬리표 하나뿐이다. 본문·제목·선행의 사후 수정은 DESIGN §11 이
"안 만든다"로 판정했고 이 명령은 그 판정을 안 건드린다.

두 표에 명시 갈래를 둔다(JudgeOffline 거절 · IdempotencyStable false). 동작은
default 와 같지만 사유가 다르다 — 표 밖으로 떨어지면 "정의돼 있지 않다"가 되어
설계 결함처럼 읽힌다. write_cmd_table_coverage_test.go 가 이것을 기계로 지킨다.

화면은 요청한 것이 아니라 실제 변화분을 낸다. 이미 있는 것을 더하거나 없는 것을
빼는 것은 거절하지 않지만, 그때 "더했다"고만 말하면 사람은 안 바뀐 것을 바뀐 줄 안다.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 7: MCP 도구 (8번째)

**⚠ 착수 전에 확인한다.** 세션 01KZSV35 가 `protocol_test.go` 를 쥐고 있고 "도구 8개 판은 내 랜딩 뒤가 깨끗하다"고 답했다. `board` 로 그 세션의 랜딩 여부를 확인하고, 아직이면 기다리거나 `note(kind:"ask")` 로 순서를 묻는다.

**Files:**
- Modify: `plugins/flightdeck/server/internal/mcpsrv/tools.go` (`var tools` 끝 + `labelArgs`)
- Modify: `plugins/flightdeck/server/internal/mcpsrv/backend.go` (인터페이스 + 머리 주석)
- Modify: `plugins/flightdeck/server/internal/mcpsrv/mcpsrv.go` (디스패치 `case` + `toolLabel`)
- Create: `plugins/flightdeck/server/internal/mcpsrv/render_label.go`
- Modify: `plugins/flightdeck/server/internal/mcpsrv/protocol_test.go` (`TestToolTableIsSeven` → `IsEight`)
- Modify: `plugins/flightdeck/server/cmd/fd/mcpbackend.go` (파일 끝)
- Test: `plugins/flightdeck/server/internal/mcpsrv/render_label_test.go` (create)

**Interfaces:**
- Consumes: `service.LabelInput`/`LabelResult` (Task 4) · `CmdLabel`·`labelPath`·`labelReq` (Task 6)
- Produces: MCP 도구 `label`. `Backend` 인터페이스에 `SetLabels(ctx, in service.LabelInput) (service.LabelResult, error)` 가 는다.

- [ ] **Step 1: 도구 표 시험을 8개로 고쳐 실패를 본다**

`internal/mcpsrv/protocol_test.go` 에서 함수 이름과 기대값을 바꾼다:

```go
func TestToolTableIsEight(t *testing.T) {
	got := ToolNames()
	want := []string{"board", "pick", "note", "add", "finish", "alloc", "land", "label"}
	if len(got) != len(want) {
		t.Fatalf("도구가 %d개다(%v) — 항목 꼬리표 표면이 label 을 더해 8개다", len(got), got)
	}
```

나머지 본문(순서 대조·`KnownTool("status")`·90자 상한·`inputSchema` 검사)은 **그대로 둔다**.

같은 파일 39행 근처의 주석 "그 일곱을 잠그는 것은 바로 아래 TestToolTableIsSeven 이다" 도 여덟으로 고친다.

```bash
cd plugins/flightdeck/server
go test ./internal/mcpsrv/ -run TestToolTableIsEight -v
```

기대: **FAIL** — "도구가 7개다 … — 항목 꼬리표 표면이 label 을 더해 8개다".

- [ ] **Step 2: 도구를 표 끝에 더한다**

`internal/mcpsrv/tools.go` 의 `var tools` 슬라이스에서 `land` 원소 **뒤**, 닫는 `}` 앞에:

```go
	{
		Name:        "label",
		Description: "항목의 꼬리표를 더하거나 뺀다. 'tickler' 만 굶김 축에서 빠진다.",
		InputSchema: obj(map[string]any{
			"item_id": str("꼬리표를 고칠 항목 id"),
			"add":     strArr("더할 꼬리표. 'tickler' 는 기한까지 늙는 항목을 굶김 축에서 뺀다"),
			"rm":      strArr("뺄 꼬리표"),
		}, "item_id"),
	},
```

같은 파일 인자 타입 절(파일 끝, `landArgs` 뒤)에:

```go
type labelArgs struct {
	ItemID string   `json:"item_id"`
	Add    []string `json:"add"`
	Rm     []string `json:"rm"`
}
```

파일 **머리 주석**도 고친다 — "도구 7개 … 더는 늘리지 않는다" 가 거짓이 됐다:

```go
// 도구 8개 — 설계 §6 표에 랜딩 순서 큐 설계(2026-08-05-landing-order-queue-design.md)가
// land 를, 항목 꼬리표 표면(2026-08-12-item-label-surface-design.md)이 label 을 더했다.
// 더는 늘리지 않는다.
```

- [ ] **Step 3: 렌더를 새 파일에 쓴다**

`render.go` 는 **안 만진다**(다른 세션이 뜯는 중). `internal/mcpsrv/render_label.go`:

```go
package mcpsrv

import (
	"fmt"
	"strings"

	"github.com/kweiza/flightdeck/internal/service"
)

// RenderLabel 은 꼬리표 고침 결과 한 조각이다.
//
// ★ **실제 변화분을 낸다** — 요청한 것이 아니라. 이미 있는 것을 더하거나 없는 것을
// 빼는 것은 집합 연산이라 거절하지 않지만, 그때 "더했다"고만 말하면 안 바뀐 것을
// 바뀐 줄 안다. 조용한 무변화를 안 만드는 것이 이 함수의 존재 이유다.
//
// 이 파일이 render.go 와 따로 있는 이유는 그 파일이 이미 크고, 새 렌더가 그쪽
// 대공사와 같은 자리를 다투기 때문이다. 렌더 하나가 자기 파일을 갖는 것은
// followups_arrival.go·drift.go 가 이미 하는 방식이다.
func RenderLabel(res service.LabelResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "label · %s 의 꼬리표를 고쳤다\n", res.Item.ID)
	fmt.Fprintf(&b, "실제로 더한 것: %s\n", labelsOrNone(res.Added))
	fmt.Fprintf(&b, "실제로 뺀 것: %s\n", labelsOrNone(res.Removed))
	fmt.Fprintf(&b, "지금 꼬리표: %s\n", labelsOrNone(res.After))
	if containsLabel(res.After, "tickler") {
		b.WriteString("tickler 가 붙었다 — 이 항목은 굶김 축(집계·★·기아 가중)에서 빠진다. " +
			"**배제가 아니다**: 추천·선점·겹침 어디에서도 이 꼬리표를 안 보고, 기한이 오면 집어야 한다.\n")
		b.WriteString("★ 묶음에서는 **선두에 달아야** 걸린다 — 구성원에 달면 그 묶음의 굶김은 안 바뀐다.\n")
	}
	return b.String()
}

// labelsOrNone 은 빈 목록을 "없음"으로 낸다.
// 빈 줄로 내면 "없다"와 "이 축을 안 읽었다"가 화면에서 같아진다.
func labelsOrNone(ls []string) string {
	if len(ls) == 0 {
		return "없음"
	}
	return strings.Join(ls, ", ")
}

func containsLabel(ls []string, want string) bool {
	for _, l := range ls {
		if l == want {
			return true
		}
	}
	return false
}
```

**주의:** `labelsOrNone`·`containsLabel` 이 `mcpsrv` 패키지에 이미 있으면 이름이 충돌한다. 확인:

```bash
cd plugins/flightdeck/server
grep -rn "func labelsOrNone\|func containsLabel" internal/mcpsrv/
```

있으면 기존 것을 쓰고 여기 정의를 지운다.

- [ ] **Step 4: 렌더 시험을 쓰고 돌린다**

`internal/mcpsrv/render_label_test.go`:

```go
package mcpsrv

import (
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

func TestRenderLabelShowsWhatActuallyChanged(t *testing.T) {
	got := RenderLabel(service.LabelResult{
		Item:  model.Item{ID: "it"},
		After: []string{"a"}, Added: nil, Removed: nil,
	})
	if !strings.Contains(got, "실제로 더한 것: 없음") {
		t.Errorf("변화가 없는데 화면이 그것을 안 말한다:\n%s", got)
	}
}

// 티클러가 붙으면 그 뜻과 **선두 규칙**을 그 자리에서 말한다.
// 선두 규칙이 아무 데도 안 적혀 있던 것이 이 표면을 만든 사고의 절반이었다.
func TestRenderLabelExplainsTicklerAndLeadRule(t *testing.T) {
	got := RenderLabel(service.LabelResult{
		Item:  model.Item{ID: "it"},
		After: []string{"tickler"}, Added: []string{"tickler"},
	})
	if !strings.Contains(got, "굶김 축") {
		t.Errorf("tickler 를 달았는데 그 뜻을 안 말한다:\n%s", got)
	}
	if !strings.Contains(got, "배제가 아니다") {
		t.Errorf("배제가 아니라는 것을 안 말한다 — 그러면 다음 사람이 이 항목이 안 잡히는 줄 안다:\n%s", got)
	}
	if !strings.Contains(got, "선두") {
		t.Errorf("묶음에서 선두에 달아야 한다는 것을 안 말한다 — 그 침묵이 이 표면을 만든 사고였다:\n%s", got)
	}
}
```

```bash
cd plugins/flightdeck/server
go test ./internal/mcpsrv/ -run TestRenderLabel -v
```

기대: 둘 다 PASS.

- [ ] **Step 5: 백엔드 이음매와 디스패치를 잇는다**

**(a)** `internal/mcpsrv/backend.go` 의 `Backend` 인터페이스에서 `Alloc` **뒤**:

```go
	// SetLabels 는 이미 있는 항목의 꼬리표를 고친다. 고칠 수 있는 축은 그 하나뿐이다.
	SetLabels(ctx context.Context, in service.LabelInput) (service.LabelResult, error)
```

같은 파일 머리 주석의 "여기 있는 것은 도구 7개와 세션 귀속이 실제로 부르는 메서드뿐이다" 를 **8개**로 고친다.

**(b)** `internal/mcpsrv/mcpsrv.go` 디스패치의 `case "land":` **뒤**:

```go
	case "label":
		res = s.toolLabel(ctx, sessionID, args)
```

**(c)** 같은 파일 파일 끝(`toolLand` 뒤)에:

```go
// toolLabel 은 이미 있는 항목의 꼬리표를 고친다.
//
// ★ 고칠 수 있는 축은 **꼬리표 하나뿐**이다 — 일반 amend 가 아니다(설계 §11).
func (s *Server) toolLabel(ctx context.Context, sessionID string, raw json.RawMessage) toolResult {
	var a labelArgs
	if err := decodeArgs(raw, &a); err != nil {
		return textResult(s.withTail(ctx, s.errText("label", err), tailOpts{}), true)
	}
	res, err := s.be.SetLabels(ctx, service.LabelInput{
		Project: s.id.ProjectID, SessionID: sessionID,
		ItemID: strings.TrimSpace(a.ItemID), Add: a.Add, Rm: a.Rm,
	})
	if err != nil {
		if r, ok := s.degradedResult(ctx, "label", err); ok {
			return r
		}
		return textResult(s.withTail(ctx, s.errText("label", err), tailOpts{}), true)
	}
	return textResult(s.withTail(ctx, RenderLabel(res), tailOpts{}), false)
}
```

**(d)** `cmd/fd/mcpbackend.go` 파일 끝:

```go
// SetLabels 는 꼬리표 고침을 REST 로 보낸다.
func (b *mcpBackend) SetLabels(ctx context.Context, in service.LabelInput) (service.LabelResult, error) {
	var res service.LabelResult
	body, err := b.write(ctx, CmdLabel, labelPath(in.ItemID), labelReq{
		Project: in.Project, SessionID: in.SessionID, Add: in.Add, Rm: in.Rm,
	})
	if err != nil {
		return res, b.apiError("꼬리표 고치기", err)
	}
	if uerr := json.Unmarshal(body, &res); uerr != nil {
		return res, fmt.Errorf("꼬리표 고침 응답을 못 읽었다: %w", uerr)
	}
	return res, nil
}
```

**주의:** `b.write` 의 시그니처(`(ctx, cmd, path string, body any) ([]byte, error)`)와 `b.apiError` 사용법을 `AddItem`(313행)에서 확인하고 **그 모양에 맞춘다**.

- [ ] **Step 6: 전체 시험**

```bash
cd plugins/flightdeck/server
go test ./internal/mcpsrv/ -v -run 'TestToolTable|TestRenderLabel'
go test ./...
```

기대: `TestToolTableIsEight` PASS(설명 90자 상한 포함), 전체 PASS.

`serial_test.go` 의 `serialProbe` 처럼 `Backend` 를 구현하는 **시험용 가짜**가 컴파일 실패한다 — 인터페이스가 늘었기 때문이다. 그 가짜들에 `SetLabels` 를 더한다:

```bash
cd plugins/flightdeck/server
go build ./... && go vet ./... 2>&1 | grep -i "does not implement\|missing method" | head
```

각 가짜에 최소 구현을 더한다(예):

```go
func (p *serialProbe) SetLabels(ctx context.Context, in service.LabelInput) (service.LabelResult, error) {
	return service.LabelResult{}, nil
}
```

- [ ] **Step 7: 커밋**

```bash
cd plugins/flightdeck/server && gofmt -l . && go vet ./... && go test ./...
cd - && git add plugins/flightdeck/server/internal/mcpsrv/ plugins/flightdeck/server/cmd/fd/mcpbackend.go
git commit -F - <<'EOF'
feat(mcpsrv): 도구 여덟 번째 label — 항목 꼬리표를 고친다

도구 예산은 land 가 6→7 로 간 길과 같은 방식으로 치른다. 늘어나는 고정비는
이름 하나이고 설명은 90자 상한 안에 든다. 표 끝에 붙여 기존 일곱의 인덱스가
안 밀리게 한다.

렌더를 render_label.go 에 따로 둔다 — render.go 는 다른 세션이 크게 고치는
중이고, 렌더 하나가 자기 파일을 갖는 것은 followups_arrival.go 가 이미 하는 방식이다.

응답은 tickler 가 붙었을 때 그 뜻과 **선두 규칙**을 그 자리에서 말한다. 선두에
달아야 묶음의 굶김이 바뀐다는 사실이 아무 데도 안 적혀 있던 것이 이 표면을 만든
사고의 절반이었다.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 8: 낡은 문장들을 고친다

**시험이 안 깨지는 쪽**이라 가장 위험하다. 주석과 설계가 조용히 거짓이 된 채 초록으로 남는다.

**Files:**
- Modify: `plugins/flightdeck/DESIGN.md` (§5 티클러 절 · §6 도구 표·제목 · §11 표)
- Modify: `plugins/flightdeck/server/internal/store/item_body_immutable_test.go` (머리 주석만)
- Modify: `plugins/flightdeck/server/internal/judge/bundle.go:192-197` (`Bundle.StarveOldest` 필드 doc 주석만 — Task 1 리뷰 이월)

**Interfaces:**
- Consumes: Task 1~7 의 결과 전부
- Produces: 없음 (문서·주석)

- [ ] **Step 0: `Bundle.StarveOldest` 필드 주석을 고친다** (Task 1 리뷰가 이월한 Minor)

Task 1 이 `bundleAround` 의 의미론을 바꿨는데 그 필드의 doc 주석은 "한 함수만" 제약 때문에
안 고쳐졌다. 지금 주석은 이렇게 읽힌다 — **"구성원 전체에서 티클러를 걸러낸 값"**:

```go
	// StarveOldest 는 **굶김 판정에 쓰는** 최고령이다 — 티클러(TicklerLabel)를 뺀 값.
	// 티클러는 기한까지 늙는 것이 정상이라, 그 나이로 묶음을 기아 승격시키면
	// 상시 점등이 된다(§4). 전원이 티클러면 zero 고, 그 묶음은 굶지 않는다.
	// Oldest 와 갈라 둔 이유: Oldest 는 순서 동률 해소와 표시에도 쓰여서
	// 티클러를 빼면 "가장 오래된 구성원"이라는 이름이 거짓이 된다.
	StarveOldest time.Time
```

→ 아래로 바꾼다:

```go
	// StarveOldest 는 **굶김 판정에 쓰는** 나이다 — **선두의 생성 시각**이고,
	// 선두가 티클러(TicklerLabel)면 zero 다. **구성원은 안 본다.**
	//
	// 티클러는 기한까지 늙는 것이 정상이라, 그 나이로 묶음을 기아 승격시키면
	// 상시 점등이 된다(§4).
	//
	// ★ 앞선 판은 이 값을 "구성원 전체에서 티클러를 걸러낸 최고령"으로 계산했고,
	// 그래서 선두에 단 티클러가 구성원 하나 때문에 **무효가 됐다**(실측 2026-08-12).
	// 그 판정은 bundleAround 로 옮겨 갔다 — 아래 CloseDeclared 와 같은 논법이다
	// ("주어는 브랜치를 받는 선두다"). 이 주석이 그때 안 따라와서 Task 1 리뷰가
	// 잡았다: 옛 문장은 **"전원이 티클러가 아니면 non-zero"** 라는 역방향 오독을
	// 부르는데, 그 오독이 정확히 고쳐진 그 결함이다.
	//
	// Oldest 와 갈라 둔 이유: Oldest 는 순서 동률 해소와 표시에 쓰이고 **전체를 본다** —
	// 여기서 티클러나 구성원을 빼면 "가장 오래된 구성원"이라는 이름이 거짓이 된다.
	StarveOldest time.Time
```

돌린다:

```bash
cd plugins/flightdeck/server
go test ./internal/judge/
```

기대: PASS (주석만 바뀌었다).

- [ ] **Step 1: DESIGN §6 도구 절**

`### MCP 도구 7개` → `### MCP 도구 8개`.

표에서 `land` 행 **뒤**에:

```
| `label` | `item_id`, `add?`, `rm?` | 이미 있는 항목의 꼬리표를 고친다 — **고칠 수 있는 축은 그 하나뿐**이다(본문·제목·선행은 못 바꾼다, §11). 응답은 요청한 것이 아니라 **실제 변화분**을 낸다(이미 있는 것을 더해도 거절하지 않지만 "더했다"고만 말하면 안 바뀐 것을 바뀐 줄 안다). `tickler` 가 붙으면 그 뜻과 **선두 규칙**을 그 자리에서 낸다 |
```

같은 절의 "6개에서 7개(`land`)로 늘어난 것은 이 예산을 안 건드린다" 문단에 한 문장을 잇는다:

```
7개에서 8개(`label`)도 같다 — 늘어난 것은 이름 하나이고, 그 설명도 90자 상한
(`mcpsrv/protocol_test.go` 의 `TestToolTableIsEight`)을 그대로 지킨다. 이 도구가
나르는 규율(티클러의 뜻·선두 규칙)은 도구 설명이 아니라 **응답**(`RenderLabel`)에만 있다.
```

- [ ] **Step 2: DESIGN §5 티클러 절 — 선두 규칙을 적는다**

"판정의 정본은 `judge.IsTickler`(…)다." 문장 **뒤**에 문단 하나를 더한다:

```
**묶음에서는 선두에만 걸린다 (2026-08-12).** 추천의 기아 가중(`judge.Bundle.StarveOldest`)은
**선두의 꼬리표만** 본다 — 구성원이 티클러인지는 안 본다. 같은 함수의 종료 선언 축과 같은
논법이다("이 축은 '지금 새로 집어도 되나'에 답하고 그 질문의 주어는 브랜치를 받는 선두다").
앞선 판은 이 축만 구성원까지 봤고, 그래서 **선두에 단 티클러가 구성원 때문에 무효가 됐다**
(실측: `created_at` 이 글자까지 같은 두 항목에서 기아 값이 한 자리도 안 줄었다).
오래된 구성원이 감춰지지는 않는다 — `EligibleBundle` 이 적격 항목 전원을 각각 선두로 세워
묶음을 만들므로 굶은 항목은 **자기가 선두인 묶음**에서 제 나이로 판정된다.
이미 있는 항목에 이 꼬리표를 다는 표면은 `label` 이다(§6·§11).
```

- [ ] **Step 3: DESIGN §11 — labels 는 판정된 적이 없었다는 것을 적는다**

§11 표의 `항목 본문(title·body) 수정` 행 **뒤**, `열린 항목에 선행…` 행 **앞**에 새 행:

```
| ~~열린 항목의 꼬리표(`labels`) 수정~~ — **열었다 (2026-08-12)** | **이 축은 원래 "안 만든다"로 판정된 적이 없었다** — 위 두 행(본문·선행)과 달리 §11 에 이름조차 없었고, 그 부재는 결정이 아니라 빈자리였다. 그 빈자리의 비용: `tickler` 는 fd 가 스스로 정의한 운용 수단인데(§5) 이미 있는 항목에 그것을 다는 경로가 REST·MCP·CLI 어디에도 없어, 사용자 판정을 집행하려던 세션이 **sqlite `UPDATE` 를 직접 쳤다**(원장에 흔적은 판단 하나뿐). 그래서 `label` 을 열되 **축 하나로 못박는다** — `move` 가 프로젝트 한 축인 것과 같은 좁기이고, 위 두 행의 판정은 **안 건드린다**. 설계 정본은 `docs/superpowers/specs/2026-08-12-item-label-surface-design.md` |
```

- [ ] **Step 4: `item_body_immutable_test.go` 머리 주석**

파일 머리의 이 문장을 고친다:

```
// `item` 표에 title·body 를 쓰는 자리는 AddItem 의 INSERT 하나뿐이다. UPDATE 는
// state·close_reason·closed_at·landed_ref·project 계열이고, REST(`/items` 6라우트)·
// MCP 7도구·CLI 어디에도 본문을 고치는 경로가 없다.
```

→

```
// `item` 표에 title·body 를 쓰는 자리는 AddItem 의 INSERT 하나뿐이다. UPDATE 는
// state·close_reason·closed_at·landed_ref·project·labels 계열이고, REST(`/items` 7라우트)·
// MCP 8도구·CLI 어디에도 본문을 고치는 경로가 없다.
//
// ★ labels 가 그 목록에 든 것은 2026-08-12 다(`fd label` · POST /items/{id}/label ·
// MCP label). **이 시험은 그때 안 깨졌다** — 무는 컬럼이 title·body 뿐이라서다.
// 그것이 이 주석을 손으로 고쳐야 했던 이유이고, 여기 적어 두는 이유이기도 하다:
// 이 파일이 지키는 것은 "부재를 주장하는 문장"인데, 정작 **이 주석 자체가**
// 코드가 바뀌어도 안 깨지는 문장이다.
```

- [ ] **Step 5: 관문을 돌린다**

```bash
cd plugins/flightdeck/server
go test ./internal/store/ -run TestItemBody -v
go test ./...
```

기대: `TestItemBodyImmutabilityIsNamedInDesign` PASS — §11 의 앵커 두 문자열("항목 본문(`title`·`body`) 수정" · "영구 결정으로 못박지 않는다")을 **안 건드렸으므로** 그대로 통과해야 한다. 깨졌으면 Step 3 에서 기존 행을 잘못 고친 것이다.

전체 PASS.

- [ ] **Step 6: 커밋**

```bash
cd plugins/flightdeck/server && gofmt -l . && go vet ./... && go test ./...
cd - && git add plugins/flightdeck/DESIGN.md plugins/flightdeck/server/internal/store/item_body_immutable_test.go
git commit -F - <<'EOF'
docs(fd): 표면이 생기며 낡은 문장들을 고친다 — 시험이 안 깨지는 쪽이라 더 위험하다

§6 도구 8개, §5 에 선두 규칙, §11 에 labels 행을 더한다.

§11 에서 중요한 것은 labels 가 원래 그 표에 **없었다**는 사실이다. 본문·선행과
달리 "안 만든다"로 판정된 적이 없었고, 그 부재는 결정이 아니라 빈자리였다.
그 빈자리의 비용이 sqlite 직접 UPDATE 였다.

§5 의 선두 규칙은 지금까지 아무 데도 안 적혀 있었다. 선두에 달아야 묶음의 굶김이
바뀐다는 그 사실의 침묵이 사고의 절반이었다.

item_body_immutable_test.go 의 머리 주석은 손으로 고쳐야 했다 — 그 시험은 labels
UPDATE 가 생겨도 안 깨진다(무는 컬럼이 title·body 뿐이다). 부재를 주장하는 문장을
지키는 파일의 주석 자체가 안 깨지는 문장이라는 것을 거기 적어 뒀다.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
```

---

## 랜딩 전 최종 검증

여덟 작업이 끝나면 설계 §8 의 **재현 좌표**를 실제로 밟는다. 단위 시험이 초록인 것과 이 표면이 사고를 막는 것은 다른 사실이다.

- [ ] **1. 관문 다섯**

```bash
cd plugins/flightdeck/server
gofmt -l . ; echo "gofmt 종료=$?"
go vet ./... ; echo "vet 종료=$?"
go build ./... ; echo "build 종료=$?"
go test ./... ; echo "test 종료=$?"
cd - && git status --short
```

`gofmt -l .` 은 **빈 출력**이어야 한다. 출력이 없다고 통과로 읽지 마라 — `find . -name '*.go' | wc -l` 로 검사 대상이 0이 아닌지 함께 확인한다.

- [ ] **2. 실제 큐에서 두 축을 잰다**

★기아를 내는 묶음을 찾고, 그 **선두**에 꼬리표를 달고, 다시 추천을 받는다.

```bash
fd next            # ★기아 묶음의 선두 id 를 적어 둔다
fd label <선두-id> --add tickler
fd next            # 다시 본다
```

두 축을 **둘 다** 단정한다:
1. **추천에서 빠졌나** — `★기아 … 경과` 문구가 사라졌다
2. **여전히 적격인가** — 탈락 사유가 `not-top` 이다. `배제`나 새 사유 코드면 **실패다** — 티클러는 「배제가 아니라 승격의 부재」다

- [ ] **3. 되돌린다**

```bash
fd label <선두-id> --rm tickler
fd next            # ★기아가 돌아왔는지 본다
```

돌아오지 않으면 빼기가 안 먹은 것이다.

- [ ] **4. 원장을 본다**

`item.label` 이 `before`·`after` 와 함께 두 줄(달기·빼기) 남았는지 확인한다. 이 표면이 메우려던 공백이 그것이다.

---

## 자체 검토 결과

**스펙 커버리지** — 스펙 9절을 작업에 대응시켰다.

| 스펙 절 | 작업 |
|---|---|
| §3 표면 셋 | Task 5(REST) · 6(CLI) · 7(MCP) |
| §4 저장·순수 함수·원장·응답·거절 | Task 2(`ApplyLabels`) · 3(`SetLabels`+원장+거절) · 4(응답의 실제 변화분) |
| §5 묶음 전파 | Task 1 |
| §6 표 등록 | Task 6 Step 3 (b)(c) |
| §7 낡는 문장 6줄 | Task 5(routes 표) · 7(protocol_test·tools.go 주석) · 8(나머지 넷) |
| §8 검증 좌표 | Task 1·2·3·4·7 의 단위 시험 + 「랜딩 전 최종 검증」 2~4 |
| §9 범위 밖 | 어느 작업에도 안 들어갔다 (의도) |

**스펙에 없던 것 둘을 계획에서 발견해 넣었다:**
- `mcpsrv/backend.go` 머리 주석의 "도구 7개" — Task 7 Step 5(a). 스펙 §7 표에 없던 줄이다
- `cmd/fd/main.go` 의 `usage` 문자열 — Task 6 Step 3(e)

**타입 일관성** — `LabelInput`/`LabelResult` 의 필드 이름이 Task 4 정의와 Task 5·6·7 사용처에서 같다(`Add`/`Rm`/`Before`/`After`/`Added`/`Removed`). JSON 태그는 `add`/`rm`/`before`/`after`/`added`/`removed` 로 세 계층(api·wire·mcpbackend)에서 같고 `label_seam_test.go` 가 그중 둘을 잠근다. 명령 이름은 `CmdLabel = "label"` 하나이고 CLI(`a.cli.Write`)·mcpbackend(`b.write`)가 같은 상수를 쓴다.

**확인이 필요한 헬퍼 이름** — 계획 안에 확인 명령을 함께 넣었다: `openTestStore`·`RecentEvents`(Task 3) · `newTestService`·`mustAddItem`·`s.st`·`s.log`(Task 4) · `s.publish` 의 원장 여부(Task 5) · `usage` 형식(Task 6) · `labelsOrNone` 충돌(Task 7) · `Backend` 가짜 구현체(Task 7 Step 6).
