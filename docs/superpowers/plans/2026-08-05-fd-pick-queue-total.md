# fd-pick-queue-total 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `pick` 응답 네 모드(`recommended`·`claimed`·`resumed`·`none`) 전부가 `큐 열림 N건` 별도 줄을 내게 한다.

**Architecture:** `store.CountOpen` 이 `ListOpen` 과 같은 술어로 열린 항목을 센다. `service.PickResult.QueueOpen *int` 가 그 수를 나르고, `Pick()` 이 **모든 쓰기가 끝난 뒤** 반환 직전 한 자리에서 채운다. 추천 경로는 `candidates()` 가 이미 읽은 값을 재사용해 질의를 추가하지 않는다. `RenderPick` 이 `사유`/`범위` 다음에 줄 하나를 찍고, `nil` 이면 숫자 대신 부재 문장을 찍는다.

**Tech Stack:** Go 1.x · SQLite(`modernc.org/sqlite`) · 표준 `testing`. 새 의존성 0.

**설계 정본:** `docs/superpowers/specs/2026-08-05-fd-pick-queue-total-design.md`
**큐 항목:** `fd-pick-queue-total` · **판단:** `01KZ73V5AH9WME5QD002QTD5YT`(왜 처음 안 셋이 뒤집혔나) · `01KZ73XQMZMZ6YYK4ER2RAYP2H`(겹침 예고)

## Global Constraints

- 작업 디렉토리는 워크트리 `.flightdeck/worktrees/fd-pick-queue-total` 다. 브랜치 이름은 항목 id 와 같은 `fd-pick-queue-total`.
- 모든 `go` 명령은 `plugins/flightdeck/server` 에서 돈다. 모듈은 `github.com/kweiza/flightdeck`.
- **주석·오류 문구·시험 실패 메시지는 한국어다.** 이 레포는 "왜 이렇게 했나"를 코드에 적는다 — 새 결정마다 근거 한 줄을 남겨라.
- 문구는 **`큐 열림 N건`** 이다. `큐: 열린 항목 N건` 이 아니다 — `board` 가 이미 쓰는 이름이다(`internal/mcpsrv/render.go:450·456·466`).
- `QueueOpen` 은 **포인터**다. 값 타입이면 필드 부재가 `0` 으로 접혀 "큐 열림 0건"이 거짓 단정이 된다.
- `derive`(`d.note`/`d.fail`)를 **쓰지 않는다.** `FreshnessOf`(`internal/service/service.go:264-285`)가 `len(failures) > 0` 을 git 축 `Stale` 로 접는다.
- `internal/api` · `internal/web` 는 **건드리지 않는다.**
- 시험 하나 → 실패 확인 → 구현 → 통과 확인 → 커밋. 태스크마다 커밋 한 번.

---

### Task 0: 워크트리 준비

**Files:** 없음(작업 공간만 만든다)

- [ ] **Step 1: 항목을 선점하고 준비 명령을 받는다**

MCP 도구로:

```
pick(item_id: "fd-pick-queue-total")
```

응답의 `워크트리 준비:` 절에 나온 명령을 그대로 실행한다. 대략 이 형태다:

```bash
cd '/home/aaron/cdo-dev/kweiza-cc-plugins'
git worktree add '.flightdeck/worktrees/fd-pick-queue-total' -b fd-pick-queue-total 'main'
cd '/home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-pick-queue-total'
```

- [ ] **Step 2: 시험이 도는지 먼저 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/store/ ./internal/service/ ./internal/mcpsrv/`
Expected: PASS. 여기서 실패하면 **내 변경 탓이 아니다** — 무엇이 깨져 있는지 먼저 보고하고 멈춰라.

---

### Task 1: `store.CountOpen`

**Files:**
- Modify: `plugins/flightdeck/server/internal/store/item.go:380-408` (`ListOpen` 위에 주석 한 줄, 아래에 `CountOpen`)
- Create: `plugins/flightdeck/server/internal/store/item_count_open_test.go`

**Interfaces:**
- Consumes: 없음
- Produces: `func (s *Store) CountOpen(ctx context.Context, project string) (int, error)`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`plugins/flightdeck/server/internal/store/item_count_open_test.go`:

```go
package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// TestCountOpenIsTheSamePredicateAsListOpen 은 두 질의가 **같은 술어**임을 단정한다.
//
// 갈리면 pick 과 board 가 같은 이름(`큐 열림 N건`)으로 다른 수를 내고,
// 그 어긋남은 두 화면을 나란히 놓기 전에는 안 보인다.
func TestCountOpenIsTheSamePredicateAsListOpen(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "fd.db"))
	defer s.Close()
	ctx := context.Background()
	if err := s.UpsertProject(ctx, model.Project{ID: "p", Path: "/p", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertMachine(ctx, model.Machine{ID: "m", Hostname: "h"}); err != nil {
		t.Fatal(err)
	}
	sess, _, err := s.OpenSession(ctx, "p", "m", "/wt", "cc1", "라벨")
	if err != nil {
		t.Fatal(err)
	}

	// 빈 큐를 먼저 본다 — 0 은 "못 셌다"가 아니라 진짜 0이어야 한다.
	if n, err := s.CountOpen(ctx, "p"); err != nil || n != 0 {
		t.Fatalf("빈 큐에서 n=%d err=%v — 기대 0, nil", n, err)
	}

	for _, id := range []string{"a", "b", "c", "d", "e"} {
		if err := s.AddItem(ctx, model.Item{
			Project: "p", ID: id, Title: "t", Body: "b", CreatedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// c 는 선점(claimed), d 는 종료(done), e 는 폐기(dropped). 셋 다 열림이 아니다.
	// dropped 는 close_reason 이 비면 스키마 CHECK 가 막는다(schema.sql:155).
	if _, err := s.ClaimItem(ctx, "p", "c", sess.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetItemState(ctx, "p", "d", model.ItemDone, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetItemState(ctx, "p", "e", model.ItemDropped, "중복이라 접었다"); err != nil {
		t.Fatal(err)
	}

	open, err := s.ListOpen(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.CountOpen(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if n != len(open) {
		t.Fatalf("CountOpen=%d 인데 ListOpen=%d 다 — 술어가 갈렸다", n, len(open))
	}
	if n != 2 {
		t.Fatalf("열림은 a·b 둘이어야 한다: %d", n)
	}
}

// TestCountOpenIsScopedToItsProject 는 남의 프로젝트를 안 센다는 것이다.
// 이 서버는 한 DB 에 여러 프로젝트를 담는다 — 스코프가 새면 pick 이 남의 큐를 자기 것으로 센다.
func TestCountOpenIsScopedToItsProject(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "fd.db"))
	defer s.Close()
	ctx := context.Background()
	for _, id := range []string{"p", "q"} {
		if err := s.UpsertProject(ctx, model.Project{ID: id, Path: "/" + id, DefaultBranch: "main"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddItem(ctx, model.Item{Project: "p", ID: "a", Title: "t", Body: "b", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"x", "y", "z"} {
		if err := s.AddItem(ctx, model.Item{Project: "q", ID: id, Title: "t", Body: "b", CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.CountOpen(ctx, "p"); err != nil || n != 1 {
		t.Fatalf("프로젝트 p 에서 n=%d err=%v — 기대 1, nil (q 의 3건을 셌다면 스코프가 샌 것이다)", n, err)
	}
}
```

- [ ] **Step 2: 실패를 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/store/ -run TestCountOpen`
Expected: FAIL — `s.CountOpen undefined (type *Store has no field or method CountOpen)` 로 **컴파일이 안 된다**.

- [ ] **Step 3: 최소 구현을 쓴다**

`internal/store/item.go` 의 `ListOpen` 주석(380행)을 이렇게 바꾼다:

```go
// ListOpen 은 열린 항목을 오래된 순으로 낸다.
//
// ★ 큐의 정의(`state = 'open'`)는 여기와 CountOpen 두 곳에 있다. 한쪽만 고치지 마라 —
// board 는 이 함수의 길이를, pick 은 CountOpen 의 수를 `큐 열림 N건` 이라는 **같은 이름**으로
// 낸다. 술어가 갈리면 두 화면이 같은 이름으로 다른 수를 내고, 그 어긋남은
// 두 화면을 나란히 놓기 전에는 안 보인다.
func (s *Store) ListOpen(ctx context.Context, project string) ([]model.Item, error) {
```

그리고 `ListOpen` 함수가 끝난 바로 다음(408행 뒤)에 붙인다:

```go
// CountOpen 은 열린 항목 수다.
//
// ListOpen 으로 대신하지 않는 이유는 소비자가 다르기 때문이다 — pick 의 선점 경로는
// 수 하나만 필요한데 ListOpen 은 항목 본문·경로·선행 조건까지 읽는다.
// ★ 술어는 ListOpen 과 **같아야 한다**(그쪽 주석을 보라).
//
// Tx 짝을 만들지 않는다 — 호출자가 없다. 선점 트랜잭션 밖에서 세는 것이 설계이고
// (표시용 숫자 하나 때문에 선점의 실패면을 넓히지 않는다), 호출자 없는 Tx 표면은
// 이 패키지가 만들지 않는다.
func (s *Store) CountOpen(ctx context.Context, project string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM item WHERE project = ? AND state = 'open'`, project).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("열린 항목 수 조회 실패(project=%q): %w", clip(project, 64), err)
	}
	return n, nil
}
```

인덱스는 만들지 않는다 — `item_by_state`(`internal/store/schema.sql:158`, `item(project, state, created_at)`)가 이 질의를 그대로 덮는다.

- [ ] **Step 4: 통과를 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/store/ -run TestCountOpen -v`
Expected: PASS 2건 (`TestCountOpenIsTheSamePredicateAsListOpen`, `TestCountOpenIsScopedToItsProject`)

- [ ] **Step 5: 커밋**

```bash
git add plugins/flightdeck/server/internal/store/item.go plugins/flightdeck/server/internal/store/item_count_open_test.go
git commit -m "feat(flightdeck): store.CountOpen — ListOpen 과 같은 술어로 열린 항목을 센다"
```

---

### Task 2: `PickResult.QueueOpen` 과 세는 자리

**Files:**
- Modify: `plugins/flightdeck/server/internal/service/pick.go:41-54` (필드), `:138-159` (`Pick` 분기), `:250-267` (`pickRecommend`), `:320-374` (`candidates`)
- Modify: `plugins/flightdeck/server/internal/service/pick_test.go` (끝에 시험 3건 추가)

**Interfaces:**
- Consumes: `store.CountOpen(ctx, project) (int, error)` — Task 1
- Produces:
  - `service.PickResult.QueueOpen *int` (`json:"queue_open,omitempty"`)
  - `func (s *Service) candidates(...) ([]judge.Candidate, string, int, error)` — 셋째 반환값이 열린 항목 수다
  - `func (s *Service) fillQueueOpen(ctx context.Context, project string, res *PickResult)`

- [ ] **Step 1: 실패하는 시험 3건을 쓴다**

`internal/service/pick_test.go` 끝에 붙인다:

```go
// TestPickCountsTheQueueAfterTheClaimNotBefore 는 이 설계에서 가장 쉽게 틀리는 자리다.
//
// 진입부에서 세면 방금 집은 항목이 아직 state='open' 이라 카운트에 들어간다
// (ClaimItem 이 open→claimed 로 옮긴다). 그러면 pick 응답이 board 보다 정확히 1 크고,
// 같은 응답의 항목 블록은 [claimed] 라고 찍혀 있다 — 한 화면이 자기를 반박한다.
func TestPickCountsTheQueueAfterTheClaimNotBefore(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	for _, id := range []string{"a", "b", "c"} {
		addItem(t, s, "p", id, nil, nil)
	}

	got, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID, ItemID: "a"})
	if err != nil {
		t.Fatalf("선점 실패: %v", err)
	}
	if got.Mode != PickClaimed {
		t.Fatalf("mode = %s, 기대 %s", got.Mode, PickClaimed)
	}
	if got.QueueOpen == nil {
		t.Fatal("큐 열림 수가 안 실렸다 — nil 은 '이 응답에 없다'는 뜻이고 선점 경로에는 있어야 한다")
	}
	if *got.QueueOpen != 2 {
		t.Fatalf("큐 열림 %d건 (기대 2) — 방금 집은 a 를 아직 열림으로 세고 있다", *got.QueueOpen)
	}
}

// TestPickResumeReportsTheSameQueueSizeAsTheClaim 은 재개가 **재출력**임을 단정한다.
//
// pick.go 는 재개 경로가 "아무것도 쓰지 않는다"고 못박아 뒀다. 재출력이 원본과 다른 수를
// 내면 그건 재출력이 아니고, 이 경로에 오는 세션은 정의상 앞 응답의 기억이 없어서
// 그 차이를 "큐가 하나 줄었다"로 읽는다 — 아무도 아무것도 안 끝냈는데.
func TestPickResumeReportsTheSameQueueSizeAsTheClaim(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	for _, id := range []string{"a", "b", "c"} {
		addItem(t, s, "p", id, nil, nil)
	}

	first, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID, ItemID: "a"})
	if err != nil {
		t.Fatalf("선점 실패: %v", err)
	}
	again, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID, ItemID: "a"})
	if err != nil {
		t.Fatalf("재개 실패: %v", err)
	}
	if again.Mode != PickResumed {
		t.Fatalf("mode = %s, 기대 %s", again.Mode, PickResumed)
	}
	if first.QueueOpen == nil || again.QueueOpen == nil {
		t.Fatalf("큐 열림 수가 안 실렸다: 선점 %v, 재개 %v", first.QueueOpen, again.QueueOpen)
	}
	if *first.QueueOpen != *again.QueueOpen {
		t.Fatalf("선점 %d건 → 재개 %d건 — 그 사이에 add 도 finish 도 없었다",
			*first.QueueOpen, *again.QueueOpen)
	}
}

// TestPickRecommendationQueueSizeCannotDivergeFromItsOwnScopeLine 은
// 추천 응답의 두 줄이 **같은 관측**에서 나왔음을 단정한다.
//
// 진입부에서 따로 세면 그 사이에 sessionCards(이 서버에서 가장 비싼 함수)가 끼어들어
// 인접한 두 줄이 다른 수를 찍을 수 있다. candidates() 가 쥔 값을 그대로 쓰면 구조적으로 못 갈린다.
func TestPickRecommendationQueueSizeCannotDivergeFromItsOwnScopeLine(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	for _, id := range []string{"a", "b", "c"} {
		addItem(t, s, "p", id, nil, nil)
	}

	got, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("추천 실패: %v", err)
	}
	if got.Mode != PickRecommended {
		t.Fatalf("mode = %s, 기대 %s", got.Mode, PickRecommended)
	}
	if got.QueueOpen == nil {
		t.Fatal("큐 열림 수가 안 실렸다")
	}
	if *got.QueueOpen != 3 {
		t.Fatalf("큐 열림 %d건 (기대 3) — 추천은 선점하지 않으므로 셋 다 열림이다", *got.QueueOpen)
	}
	if !strings.Contains(got.Scope, "열린 항목 3건") {
		t.Fatalf("범위 문자열과 큐 수가 갈렸다: %q vs %d건", got.Scope, *got.QueueOpen)
	}
}
```

- [ ] **Step 2: 실패를 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/service/ -run 'TestPick(CountsTheQueue|ResumeReports|RecommendationQueueSize)'`
Expected: FAIL — `got.QueueOpen undefined (type PickResult has no field or method QueueOpen)` 로 컴파일이 안 된다.

- [ ] **Step 3: 필드를 더한다**

`internal/service/pick.go` 의 `PickResult` 에서 `Scope` 줄 다음에 붙인다:

```go
	Scope    string            `json:"scope"`              // 무엇을 후보로 봤나 — 안 본 것을 침묵하지 않는다
	// QueueOpen 은 **남은** 열린 항목 수다(이 호출이 집은 것을 뺀 값).
	//
	// ★ 포인터인 이유가 둘이다.
	//  1. 서버는 독립 컨테이너인데 플러그인은 자동 갱신된다. 구서버 + 신 클라이언트면
	//     이 키가 응답에 없고, 값 타입이면 0 으로 접혀 **신선한 온라인 응답이
	//     "큐 열림 0건" 을 단정한다**. SkewBanner 는 api_version 문자열만 보므로 안 뜬다.
	//  2. 오프라인 캐시에는 스키마 버전축이 없다 — 이 필드가 생기기 전에 굳은 next 응답이
	//     그대로 재생된다. nil 이면 "이 응답은 그 축을 안 낸다"로 정확히 읽힌다.
	QueueOpen *int `json:"queue_open,omitempty"`
	Derived
```

- [ ] **Step 4: `Pick()` 이 분기를 값으로 받게 바꾼다**

`internal/service/pick.go:155-158` 의 마지막 세 줄을 바꾼다. 지금:

```go
	if strings.TrimSpace(in.ItemID) != "" {
		return s.pickExplicit(ctx, proj, in, live, d, now)
	}
	return s.pickRecommend(ctx, proj, in, live, d, now)
}
```

이렇게:

```go
	var res PickResult
	if strings.TrimSpace(in.ItemID) != "" {
		res, err = s.pickExplicit(ctx, proj, in, live, d, now)
	} else {
		res, err = s.pickRecommend(ctx, proj, in, live, d, now)
	}
	if err != nil {
		return PickResult{}, err
	}
	// ★ 큐 규모는 **선점 쓰기가 끝난 뒤에** 센다.
	//
	// 먼저 세면 claimed 응답의 수에 방금 집은 항목이 들어간다(ClaimItem 이 open→claimed
	// 로 옮긴다). 그러면 같은 응답이 항목을 [claimed] 로 찍어 놓고 두 줄 밑에서 열림으로
	// 세고, 쓰기가 없는 재개 경로는 같은 세계에 대해 1 작은 수를 낸다 — 재출력이 원본과
	// 다른 수를 내면 그건 재출력이 아니다.
	//
	// 각주로는 못 덮는다: JudgeClaim 의 "상태가 claimed 인데 점유자가 없다" 갈래로 들어온
	// 선점은 항목이 애초에 open 이 아니라 오프셋이 0 이다. 고정 각주는 그 절반에서 거짓말이 된다.
	s.fillQueueOpen(ctx, proj.ID, &res)
	return res, nil
}
```

`err` 는 위쪽 `proj, err := s.st.GetProject(...)` 에서 이미 선언돼 있으므로 `:=` 가 아니라 `=` 다.

- [ ] **Step 5: `fillQueueOpen` 을 더한다**

`Pick` 함수가 끝난 바로 다음(`pickExplicit` 주석 앞)에 붙인다:

```go
// fillQueueOpen 은 응답에 남은 큐 열림 수를 싣는다.
//
// **실패해도 pick 을 실패시키지 않는다.** 표시용 숫자 하나 때문에 선점을 잃는 것이
// 더 나쁘고, nil 은 렌더가 "이 응답에 없다"로 정확히 말한다.
//
// derive(d.note/d.fail)에 넣지 않는다 — FreshnessOf 가 failures>0 을 **git 축** Stale 로
// 접기 때문에, DB 카운트 한 번이 실패했을 뿐인데 세션이 브랜치·HEAD·조상 판정이
// 낡았다고 읽게 된다.
func (s *Service) fillQueueOpen(ctx context.Context, project string, res *PickResult) {
	if res.QueueOpen != nil {
		return // 추천 경로가 candidates() 의 관측을 이미 실었다. 같은 사실을 두 번 세지 않는다
	}
	n, err := s.st.CountOpen(ctx, project)
	if err != nil {
		s.log.WarnContext(ctx, "큐 열림 수 조회 실패",
			"project", clip(project, 64), "error", err.Error())
		return
	}
	res.QueueOpen = &n
}
```

- [ ] **Step 6: `candidates()` 가 열린 항목 수를 함께 낸다**

`internal/service/pick.go:320` 의 시그니처를 바꾼다:

```go
func (s *Service) candidates(ctx context.Context, proj model.Project, live []judge.LiveSession) ([]judge.Candidate, string, int, error) {
```

함수 안의 `return` 넷을 전부 고친다 — 오류 경로 셋은 `return nil, "", 0, err`, 마지막 성공 경로는:

```go
	scope := fmt.Sprintf("후보 = 열린 항목 %d건 + 살아 있는 세션이 쥔 항목 %d건. "+
		"살아 있지 않은 세션이 쥔 항목은 후보에 없다", len(open), claimedCount)
	// ★ len(open) 을 scope 문자열과 **같은 자리에서** 낸다. 호출부가 따로 세면 그 사이에
	// sessionCards 가 끼어들어 인접한 두 줄이 다른 수를 찍을 수 있고, 나중에 이 함수의
	// 술어가 바뀌면 두 줄이 영구히 갈린다.
	return cands, scope, len(open), nil
}
```

`pickRecommend`(253행)의 호출부와 결과 조립을 고친다:

```go
	cands, scope, openCount, err := s.candidates(ctx, proj, live)
	if err != nil {
		return PickResult{}, err
	}
```

그리고 267행:

```go
	res := PickResult{Rejected: rejected, Scope: scope, QueueOpen: &openCount}
```

이렇게 두면 `none` 모드의 이른 반환(281-289행)도 같은 값을 그대로 나른다.

- [ ] **Step 7: 통과를 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/service/ -run 'TestPick' -v`
Expected: 새 시험 3건 PASS + 기존 `TestPick*` 6건 그대로 PASS

Run: `cd plugins/flightdeck/server && go build ./... && go test ./...`
Expected: PASS. `candidates` 시그니처를 바꿨으므로 다른 호출부가 있으면 여기서 잡힌다(지금은 `pick.go:253` 하나뿐이다).

- [ ] **Step 8: 커밋**

```bash
git add plugins/flightdeck/server/internal/service/pick.go plugins/flightdeck/server/internal/service/pick_test.go
git commit -m "feat(flightdeck): pick 이 남은 큐 열림 수를 낸다 — 선점 쓰기가 끝난 뒤에 센다"
```

---

### Task 3: `RenderPick` 의 줄 하나

**Files:**
- Modify: `plugins/flightdeck/server/internal/mcpsrv/render.go:538-541`
- Modify: `plugins/flightdeck/server/internal/mcpsrv/render_test.go` (`TestRenderPickCarriesBranchAndWorktree` 다음에 시험 2건 추가)

**Interfaces:**
- Consumes: `service.PickResult.QueueOpen *int` — Task 2
- Produces: 응답 문자열의 `큐 열림 N건` 줄

- [ ] **Step 1: 실패하는 시험 2건을 쓴다**

`internal/mcpsrv/render_test.go` 의 `TestRenderPickCarriesBranchAndWorktree` 가 끝난 다음에 붙인다:

```go
// TestRenderPickCarriesQueueSizeInEveryMode 는 네 모드 어느 쪽으로 들어와도
// 같은 이름의 같은 줄을 본다는 것이다 — 세션이 모드를 보고 어디를 읽을지 고르지 않아도 된다.
func TestRenderPickCarriesQueueSizeInEveryMode(t *testing.T) {
	n := 5
	for _, mode := range []service.PickMode{
		service.PickRecommended, service.PickClaimed, service.PickResumed, service.PickNone,
	} {
		got := RenderPick(service.PickResult{Mode: mode, Reason: "사유다", QueueOpen: &n}, t0)
		if !strings.Contains(got, "큐 열림 5건") {
			t.Fatalf("%s 모드 응답에 큐 열림 수가 없다:\n%s", mode, got)
		}
	}
}

// TestRenderPickNeverCallsAnAbsentQueueSizeZero 가 이 설계에서 가장 중요한 시험이다.
//
// nil 이 되는 경로는 구버전 서버(SkewBanner 가 안 잡는다) · 필드가 생기기 전의 캐시 ·
// 조회 실패 셋이다. 그것을 "큐 열림 0건" 으로 찍으면 신선한 온라인 응답이 거짓을 단정하고,
// none 모드에는 그 모순을 드러낼 항목조차 없어 에이전트가 "큐가 비었다" 로 읽고 세션을 접는다.
// 스큐 구간에서만 나타나는 실패라 사람이 재현하기 어렵다 — 이 시험이 유일한 방벽이다.
func TestRenderPickNeverCallsAnAbsentQueueSizeZero(t *testing.T) {
	got := RenderPick(service.PickResult{Mode: service.PickNone, Reason: "적격 0건이다"}, t0)
	if strings.Contains(got, "큐 열림 0건") {
		t.Fatalf("부재를 0건으로 단정했다:\n%s", got)
	}
	if !strings.Contains(got, "이 응답에 없다") {
		t.Fatalf("부재를 침묵으로 접었다 — 안 본 것을 침묵하지 않는다:\n%s", got)
	}
}
```

- [ ] **Step 2: 실패를 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/mcpsrv/ -run 'TestRenderPick(CarriesQueueSize|NeverCalls)'`
Expected: FAIL — 두 시험 모두 문자열이 없어서 실패한다(`큐 열림 5건 이 없다`, `이 응답에 없다`).

- [ ] **Step 3: 렌더에 줄을 더한다**

`internal/mcpsrv/render.go:538-541` 의 `범위` 블록 바로 다음에 붙인다:

```go
	fmt.Fprintf(&b, "사유: %s\n", r.Reason)
	if r.Scope != "" {
		fmt.Fprintf(&b, "범위: %s\n", r.Scope)
	}
	// 큐 규모. board 가 쓰는 이름을 **그대로** 쓴다(같은 술어에 두 번째 이름을 붙이면
	// 두 수가 갈려도 읽는 쪽이 "다른 지표겠지"로 넘어가 불일치가 조용히 정상으로 등록된다).
	//
	// nil 을 침묵으로 접지 않는다. 원인은 셋인데(구버전 서버 · 옛 캐시 · 조회 실패)
	// nil 하나로는 못 가르므로 **원인 중립 문장**을 쓴다 — 지어낸 원인보다 정확하고,
	// 이 문장은 SkewBanner 가 못 잡는 스큐 구간의 유일한 신호이기도 하다.
	if r.QueueOpen != nil {
		fmt.Fprintf(&b, "큐 열림 %d건\n", *r.QueueOpen)
	} else {
		b.WriteString("큐 열림 수가 이 응답에 없다 — 서버 판이 이 축을 안 내거나 세지 못했다\n")
	}
```

- [ ] **Step 4: 통과를 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/mcpsrv/ -v`
Expected: 새 시험 2건 PASS + 기존 전부 PASS. 특히 `board` 토큰 예산 시험(`render_test.go:187`)과 `instructions` 예산 시험이 그대로 통과해야 한다 — 이 줄은 `pick` 응답에만 붙는다.

- [ ] **Step 5: 커밋**

```bash
git add plugins/flightdeck/server/internal/mcpsrv/render.go plugins/flightdeck/server/internal/mcpsrv/render_test.go
git commit -m "feat(flightdeck): pick 응답이 '큐 열림 N건' 을 낸다 — 부재는 0건이 아니다"
```

---

### Task 4: 스킬 한 줄과 설계 문서 한 줄

**Files:**
- Modify: `plugins/flightdeck/skills/fd-pickup/SKILL.md` (§3 "집는다" 절)
- Modify: `plugins/flightdeck/DESIGN.md:297` (`pick` 행)

**Interfaces:**
- Consumes: Task 3 의 응답 문구
- Produces: 없음(문서)

- [ ] **Step 1: `SKILL.md` §3 에 한 줄 더한다**

`plugins/flightdeck/skills/fd-pickup/SKILL.md` 의 §3 마지막 줄

```
이미 자기 선점이면 거절이 아니라 **맥락 재출력**이다(컨텍스트가 날아가 돌아온 경로).
```

다음에 붙인다:

```markdown

무엇을 집었는지 사람에게 알릴 때 응답의 `큐 열림 N건` 줄을 **그대로 옮긴다.**
숫자를 외워서 말하지 마라 — 그 줄이 없는 응답이 있고(구버전 서버 · 회수 거절 · 오프라인 선점),
없을 때 지어낸 수는 "큐가 비었다"로 읽힌다.
```

**"숫자를 말한다"가 아니라 "그 줄을 그대로 옮긴다"** 여야 한다. 전자로 쓰면 줄이 없는 경로에서 에이전트가 값을 지어내거나 앞 응답의 수를 재사용한다.

- [ ] **Step 2: `DESIGN.md:297` 의 `pick` 행에 조각 하나 더한다**

지금:

```
| `pick` | `item_id?`, `steal_reason?` | 인자 없으면 추천 1건 + 왜 + **탈락 사유 전부**. 인자 있으면 선점 + 항목 본문 + 연결된 판단 전문 + 브랜치 이름 + 워크트리 준비 명령. **이미 자기 것이면 맥락 재출력**(재개 경로) |
```

바꿔서:

```
| `pick` | `item_id?`, `steal_reason?` | 인자 없으면 추천 1건 + 왜 + **탈락 사유 전부**. 인자 있으면 선점 + 항목 본문 + 연결된 판단 전문 + 브랜치 이름 + 워크트리 준비 명령. **이미 자기 것이면 맥락 재출력**(재개 경로). 어느 쪽이든 **남은 큐 열림 수**가 함께 온다 |
```

**297행 한 줄만 만진다.** 지금 `DESIGN.md` 를 동시에 만지는 세션이 셋이다 — §7 398행 · §6 REST 표 315~340 · §2 Tier B 108~151. hunk 를 넓히면 닿는다.

- [ ] **Step 3: 전체 시험을 돌린다**

Run: `cd plugins/flightdeck/server && go build ./... && go test ./...`
Expected: 전부 PASS

- [ ] **Step 4: `DESIGN.md` diff 가 한 줄인지 눈으로 본다**

Run: `git diff --stat plugins/flightdeck/DESIGN.md`
Expected: `1 file changed, 1 insertion(+), 1 deletion(-)`. 이보다 크면 되돌리고 297행만 다시 고쳐라.

- [ ] **Step 5: 커밋**

```bash
git add plugins/flightdeck/skills/fd-pickup/SKILL.md plugins/flightdeck/DESIGN.md
git commit -m "docs(flightdeck): pick 의 큐 열림 수를 계약과 스킬에 적는다"
```

---

### Task 5: 실물로 한 번 돌려 본다

**Files:** 없음(검증만)

- [ ] **Step 1: CLI 로 추천 경로를 본다**

Run: `cd plugins/flightdeck/server && go run ./cmd/fd next`
Expected: 응답에 `큐 열림 N건` 줄이 있고, 그 수가 바로 위 `범위:` 줄의 "열린 항목 N건" 과 같다.

- [ ] **Step 2: 보드와 수가 같은지 본다**

Run: `cd plugins/flightdeck/server && go run ./cmd/fd status`
(`board` 는 CLI 명령이 아니다 — `status` 가 `RenderBoard` 를 찍는다, `cmd/fd/cmds.go:106`)
Expected: 꼬리의 `큐 열림 N건` 이 Step 1 과 **같은 수**. 다르면 그 사이에 남이 큐를 바꿨거나 술어가 갈린 것이다. 후자면 Task 1 의 주석이 가리키는 두 자리를 보라.

- [ ] **Step 3: 결과를 판단으로 남긴다**

MCP 도구로. 본문에 최소한 이 넷을 적는다: (1) `next` 와 `status` 의 수가 같았는지(실제 숫자), (2) 선점→재개를 실제로 돌려 두 수가 같았는지, (3) **안 본 축** — 구버전 서버 스큐와 오프라인 캐시 재생은 시험으로만 덮었고 실물로 재현하지 않았다, (4) 남긴 결함이 있으면 무엇인지.

```
note(kind: "verified", item_id: "fd-pick-queue-total", body: "…")
```

- [ ] **Step 4: 항목을 끝낸다**

`outcome` 은 실제 결과를 쓴다. `body` 에는 왜 세는 자리를 쓰기 뒤로 골랐는지와 §9 의 "일부러 안 한 것"(Tx 안에서 안 셈 · `범위` 문구 안 고침 · `web` 대시보드 안 건드림)을 옮긴다 — 다음 세션이 그것을 결함으로 보고 고치러 가지 않도록.

```
finish(item_id: "fd-pick-queue-total", outcome: "…", body: "…")
```

`finish` 한 번이 판단 저장 + 후속 등록 + 종료 + 워크스페이스 해제를 원자적으로 한다.

---

## 합칠 때 조심할 것

`fd-item-path-project-mismatch-hint` 세션이 **같은 두 자리**를 만진다:

- `service.PickResult` 에 포인터 필드 추가 (그쪽 `PathCheck *judge.ItemPathVerdict`, 이쪽 `QueueOpen *int`)
- `RenderPick` 출력 블록

두 설계가 포인터로 둘 것 · `derive` 를 안 건드릴 것 · `RenderPick` 한 줄로 끝낼 것이라는 같은 결론에 독립적으로 도달했으므로 **의미 충돌은 없고 줄만 부딪힌다.** 충돌이 나면 양쪽 필드를 나란히 두고 양쪽 렌더 줄을 나란히 두면 끝난다.

추가로 이쪽만 건드리는 자리 하나: **`Pick()` 의 분기 구조**(`pick.go:155-158`)를 값으로 받는 형태로 바꾼다. 그쪽이 같은 부근을 만지면 이 hunk 가 겹친다.
