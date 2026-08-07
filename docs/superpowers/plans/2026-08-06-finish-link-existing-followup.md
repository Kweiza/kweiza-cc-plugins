# finish 가 이미 있는 항목을 후속으로 잇는다 — 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `finish` 의 `followups` 에 **이미 있는 항목 id 를 넣으면 새로 만들지 않고 판단에 잇고**, 이을 자격이 없는 id 는 트랜잭션 진입 전에 거절하며, 응답·안내 문구가 그 사실을 정직하게 말하게 한다.

**Architecture:** 분류를 트랜잭션 **밖**에서 하고(거절은 여기서만), 링크는 트랜잭션 **안**에서 확정한다. `sessionSpawnedOpen`(이 선점 뒤 이 세션이 만든 열린 항목 + **관측 여부**)이 관문과 잇기 자격의 **한 정의**가 되고, `classifyFollowups` 가 후속마다 `만들기 | 잇기 | 거절` 을 고른다. 판단 링크는 `dedupeLinks` 를 지나 조립되어 오늘 있는 판단 소실 결함을 닫는다.

**Tech Stack:** Go 1.25(툴체인 1.26.5) · SQLite(`modernc.org/sqlite`, WAL · `_txlock=immediate`) · 표준 `testing`. 새 의존성 0. 스키마 변경 0.

**설계 정본:** `docs/superpowers/specs/2026-08-06-finish-link-existing-followup-design.md`(개정 1 포함, 커밋 `561887b`·`8d88500`)
**큐 항목:** `fd-finish-cannot-link-an-existing-item-as-followup` · **판단:** `01KZBFETZSTASRFHGNQNN2PBNQ`(경로 앵커) · `01KZBFJBKK7Z11CJ6HJ1CA5PK0`(범위 정정 — 본문의 전제가 실측으로 깨진 사실)

## Global Constraints

- 작업 디렉토리는 워크트리 `.flightdeck/worktrees/fd-finish-cannot-link-an-existing-item-as-followup` 다. 브랜치는 항목 id 와 같다. 모든 `go` 명령은 그 안의 `plugins/flightdeck/server` 에서 돈다. 모듈은 `github.com/kweiza/flightdeck`.
- **주석·오류 문구·시험 실패 메시지는 한국어다.** 이 저장소는 "왜 이렇게 했나"를 코드에 적는다 — 새 결정마다 근거를 남겨라.
- **`finish.go` 에 새 함수를 넣지 마라.** 이 파일은 지금 열린 항목 **넷**이 동시에 쥐고 있다(`finish_balance.go:48-52` 주석이 이름을 적는다). 새 코드는 `finish_followups.go` 로 간다 — `finish.go` 에는 호출 줄만 남긴다. 이것이 이 저장소의 명시적 관례다(`finish_followups.go:15-18`).
- **오류 문구의 순번은 `i+1`(1-based) 이다.** `internal/service/indexnotation_test.go` 가 소스 전수로 0-based 표기를 잡는다.
- **`RefusedError` 는 항상 포인터**로 만든다: `&RefusedError{What: "finish", Reason: …, Guidance: …}`. `Error()` 는 `"finish 거절: <Reason>\n<Guidance>"` 로 조립된다.
- **트랜잭션 안에서는 거절하지 않는다.** `Store.Tx` 는 `fn` 이 오류를 내면 ①②③④ 를 통째로 롤백한다 — 넷 중 판단만이 원리적으로 파생 불가하다. tx 안 판정은 **분기에만** 쓴다.
- **`store.ErrNotFound` 는 `errors.Is` 로 잡는다**(`*store.NotFoundError` 가 `Unwrap` 한다). **`*store.ConflictError` 는 `errors.As` 로 잡는다**(`Unwrap` 이 드라이버 원문을 내므로 `errors.Is(…, ErrNotFound)` 로는 절대 안 잡힌다).
- 시험 헬퍼는 이미 있는 것을 쓴다: `newSvc`(`helper_test.go:31`) · `newRepoWithWorktree`(**`board_test.go:21`**) · `openSession`(`helper_test.go:95`, 세션 id 는 `me.Session.ID`) · `addItem`(`helper_test.go:108`, **세션을 안 싣는다**) · `addItemAs`(`finish_followups_test.go:14`, **세션을 싣는다**) · `claimed`(`finish_test.go:14`) · `countRows`(`helper_test.go:121`) · `ctx()`(`helper_test.go:130`). 패키지가 하나라 **중복 정의하면 컴파일이 깨진다.**
- 태스크마다: 시험 하나 → **빨강 확인** → 구현 → 초록 확인 → 커밋. 커밋 제목은 `<type>(flightdeck): <한국어 한 줄>` 이고 두 절은 em dash( — )로 잇는다. 마침표를 안 찍는다. 본문 끝에 빈 줄 하나 뒤 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`. `🤖` 마커는 **안 쓴다**.
- **빨강은 "실패했다"가 아니라 "의도한 문구로 실패했다"여야 한다.** `finish` 는 관문이 여럿이라(`JudgeFinish` → `ValidateItemID` → 중복 id → 자격 → `title`·`body` → `paths`) 인자를 덜 채우면 **앞 관문이 먼저 거절해** 뒤 관문을 겨눈 시험이 구현 전에도 초록이 된다. 실측으로 그렇게 됐다 — Task 3 의 거절 시험 셋이 손대지 않은 트리에서 전부 PASS 했다. 새 거절 시험마다 **그 관문의 고유 문구**를 단정해라.
- 매 태스크 끝: `gofmt -l .` 무출력 · `go vet ./...` 무출력 · `go test ./...` 초록.

---

### Task 0: 워크트리 정리와 기준선 — **이미 실행돼 끝났다**

> **★ 이 태스크는 다시 돌리지 마라.** 탐침은 이미 없고(2026-08-06 실측: 워크트리 전수 탐색 0건), 기준선도 이미 초록이었다(`gofmt -l .` 무출력). 아래는 **무엇을 했는지의 기록**이고, 명령은 재실행해도 무해하도록만 고쳐 뒀다.

**Files:**
- Delete(있으면): `plugins/flightdeck/server/internal/service/zz_probe_test.go` — 설계 단계의 임시 탐침. **지금 워크트리에는 이미 없다.**

**Interfaces:** 없음.

- [x] **Step 1: 설계 단계의 임시 탐침이 남아 있으면 지운다**

```bash
cd plugins/flightdeck/server && rm -f internal/service/zz_probe_test.go
```

이 파일은 git 미추적이고 **단정이 하나도 없었다**(`t.Logf` 만 했다). 랜딩 관문은 단정 없는 시험을 조용히 통과시키므로 남기지 않는다. 그것이 확인한 사실은 이미 설계 문서 §1 과 판단 `01KZBFJBKK7Z11CJ6HJ1CA5PK0` 에 옮겨져 있다.

`-f` 인 이유: 실측에서 이 파일은 **이미 없었다.** `rm` 은 없는 경로에 exit 1 이라, `&&` 체인 전체가 실패하고 계획을 집행하는 세션이 "Task 0 Step 1 실패"로 읽어 없는 조사를 시작한다. 이 단계는 관문이 아니다 — 관문은 Step 2 하나다.

- [x] **Step 2: 기준선을 잰다 — 여기서 빨간 것은 내 탓이 아니다**

Run: `cd plugins/flightdeck/server && gofmt -l . && go vet ./... && go test ./...`
Expected: `gofmt` 무출력 · `vet` 무출력 · 시험 전부 PASS. 여기서 실패하면 **멈추고 무엇이 깨져 있는지 먼저 보고해라.**

**실행 결과(2026-08-06):** 초록. Task 1 부터 시작해라.

---

### Task 1: 판단이 사라지는 자리를 먼저 닫는다 — 링크 중복 제거

오늘 있는 결함이다. `judgment_link` 의 PK 는 `(judgment_id, target_kind, target_id)`(`store/schema.sql:271`)이고 `AddJudgment` 는 평범한 INSERT 다(`store/judgment.go:54-67`). `finish.go:199-203` 이 `in.ItemID` + `in.Links` + 후속 id 전부를 **중복 제거 없이** 이어 붙이므로, 겹치면 ①에서 `ConflictDuplicate` 가 나고 tx 전체가 롤백된다 — **판단이 사라진다.** 잇기는 정확히 그 겹침을 만들기 쉬우므로 이것을 **먼저** 닫는다.

**Files:**
- Create: `plugins/flightdeck/server/internal/service/finish_links_test.go`
- Modify: `plugins/flightdeck/server/internal/service/finish_followups.go` (파일 끝에 `dedupeLinks` 추가)
- Modify: `plugins/flightdeck/server/internal/service/finish.go:160-184`(전단 검증 루프에 중복 id 거절) · `finish.go:199-203`(링크 조립을 `dedupeLinks` 로 감싼다)

**Interfaces:**
- Produces: `func dedupeLinks(links []model.JudgmentLink) []model.JudgmentLink` — 같은 `(TargetKind, TargetID)` 를 처음 것만 남긴다. `model.JudgmentLink` 는 문자열 둘짜리 비교 가능한 구조체라 맵 키로 그대로 쓴다(`internal/model/model.go:253`).

- [ ] **Step 1: 빨강 시험 둘을 쓴다**

`plugins/flightdeck/server/internal/service/finish_links_test.go` 를 새로 만든다:

```go
package service

import (
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// TestFinishSurvivesALinkThatRepeatsTheItem 은 **오늘 판단이 사라지는 자리**를 잠근다.
//
// judgment_link 의 PK 는 (judgment_id, target_kind, target_id) 이고 AddJudgment 는
// 평범한 INSERT 다. finish 는 in.ItemID · in.Links · 후속 id 를 중복 제거 없이 이어 붙이므로,
// 링크 하나가 항목을 한 번 더 가리키면 ① 에서 ConflictDuplicate 가 나고 Store.Tx 가
// ①②③④ 를 통째로 롤백한다 — 넷 중 **판단만이 원리적으로 파생 불가**한데 그것이 사라진다.
//
// 잠금은 이 창을 못 닫는다. _txlock=immediate 가 배제하는 것은 **다른 커넥션**이고,
// 이 겹침은 한 호출 안에서 자기와 부딪힌다.
func TestFinishSurvivesALinkThatRepeatsTheItem(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")

	res, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone, Title: "끝냈다", Body: "본문",
		Links: []model.JudgmentLink{{TargetKind: "item", TargetID: "batch7"}},
	})
	if err != nil {
		t.Fatalf("링크가 항목을 두 번 가리켰다고 마무리가 통째로 실패했다 — 판단이 사라진다: %v", err)
	}
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 1 {
		t.Fatalf("판단이 %d건이다 — 1건이어야 한다", n)
	}
	if n := countRows(t, st,
		`SELECT count(*) FROM judgment_link WHERE judgment_id = ? AND target_id = 'batch7'`,
		res.Judgment.ID); n != 1 {
		t.Fatalf("판단 링크가 %d건이다 — 중복이 제거돼 1건이어야 한다", n)
	}
}

// TestFinishRefusesTheSameFollowupIDTwiceInOneCall 은 같은 호출의 자기 충돌을 그 자리에서 잡는다.
//
// ★ **오늘 이것은 흡수가 아니라 판단 소실이다.** 실측: 같은 id 를 두 번 실으면 ① 의 AddJudgment 가
// 링크 twin 을 두 번 INSERT 해 PK(schema.sql:271)에 부딪히고 tx 전체가 롤백된다 —
// 판단 0건 · 항목 0건 · 원래 항목은 claimed 인 채로 남는다. 오류도 RefusedError 가 아니라
// raw *store.ConflictError(code=1555)이고 그 문구에는 어느 id 인지가 안 나온다
// (writeErr 가 target=item/twin 을 담은 포맷 문자열을 버린다 — store/constraint.go:201).
// 즉 중복 후속 id 하나가 오늘 이미 판단을 통째로 없앤다.
//
// ★ Step 3 의 dedupeLinks 가 들어가면 링크는 살지만 두 번째 t.AddItem 이 자기 트랜잭션의
// 첫 INSERT 때문에 ConflictDuplicate 를 받아 흡수 갈래로 빠진다 — 세션은 "후속 2건"을 실었는데
// 응답은 1건 등록 + 1건 건너뜀이 되고, **그 건너뜀의 사유가 거짓**이다(남이 만든 것이 아니라
// 자기가 만들었다). 이 시험은 그 두 상태를 **둘 다** 잠근다.
func TestFinishRefusesTheSameFollowupIDTwiceInOneCall(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")

	_, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone, Title: "끝냈다", Body: "본문",
		Followups: []FollowupInput{
			{ID: "twin", Title: "제목", Body: "본문"},
			{ID: "twin", Title: "제목", Body: "본문"},
		},
	})
	if err == nil {
		t.Fatalf("같은 후속 id 를 두 번 실었는데 통과했다")
	}
	if !strings.Contains(err.Error(), "twin") {
		t.Fatalf("거절 사유가 어느 id 인지 안 낸다:\n%s", err.Error())
	}
	// ★ 이 관문의 **고유 문구**를 못 박는다. 다른 관문(제목·본문 · 경로 · 자격)이 먼저
	//   거절해도 위 단정 셋은 전부 참이 되므로, 이 줄이 없으면 이 시험이 무엇을 잠그는지
	//   모르게 된다. Global Constraints 의 "빨강은 의도한 문구로 실패해야 한다"가 이것이다.
	if !strings.Contains(err.Error(), "같은 호출에 두 번 실렸다") {
		t.Fatalf("중복 관문이 아닌 다른 것이 거절했다 — 이 시험은 무엇도 안 잠근다:\n%s", err.Error())
	}
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 0 {
		t.Fatalf("거절했는데 판단이 %d건 남았다 — 트랜잭션 진입 전이라 아무것도 안 써야 한다", n)
	}
	if n := countRows(t, st, `SELECT count(*) FROM item WHERE id = 'twin'`); n != 0 {
		t.Fatalf("거절했는데 항목이 %d건 만들어졌다", n)
	}
}
```

- [ ] **Step 2: 빨강을 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/service/ -run 'TestFinishSurvivesALinkThatRepeatsTheItem|TestFinishRefusesTheSameFollowupIDTwiceInOneCall' -v`
Expected: **둘 다 FAIL. 단 사유가 서로 다르다 — 아래는 실측값이다.**

- 첫째(`…SurvivesALinkThatRepeatsTheItem`)는 `링크가 항목을 두 번 가리켰다고 마무리가 통째로 실패했다` 로 죽는다. 원문은 `*store.ConflictError` — `judgment_link 제약 위반(duplicate, …, code=1555): … UNIQUE constraint failed: judgment_link.judgment_id, judgment_link.target_kind, judgment_link.target_id`.
- 둘째(`…RefusesTheSameFollowupIDTwiceInOneCall`)는 **"통과했다"가 아니라 `거절 사유가 어느 id 인지 안 낸다` 로 죽는다.** 오늘은 흡수 갈래에 **못 간다** — `finish.go:199-202` 가 링크에 `twin` 을 두 번 실어 ① 의 `AddJudgment` 가 첫째와 **같은** PK(`schema.sql:271`)에 부딪히고 tx 가 통째로 롤백되므로, ② 의 `AddItem` 은 한 번도 안 돌아본다. 오류는 `*RefusedError` 가 아니라 raw `*store.ConflictError` 이고 그 문구에 `twin` 이 **없다**: `writeErr`(`store/constraint.go:201-208`)가 제약 위반이면 `target=item/twin` 을 담은 포맷 문자열을 버리고 `ConflictError` 만 올리며, `Error()`(`:108-111`)는 값이 아니라 컬럼 이름만 찍는다. 실측 상태: **판단=0 · 항목 twin=0 · batch7=claimed**.
- **"통과했다" 모드는 Step 3(`dedupeLinks`)이 들어간 뒤에 나타난다.** 그러니 Step 3 을 넣고 **Step 4 를 넣기 전에** 둘째만 한 번 더 돌려 그 전환을 눈으로 봐라:
  `go test ./internal/service/ -run TestFinishRefusesTheSameFollowupIDTwiceInOneCall -v`
  → 이때는 `err == nil`(판단 1건 · 항목 twin 1건 · batch7=done)이라 `같은 후속 id 를 두 번 실었는데 통과했다` 로 죽는다. 이 전환이 안 보이면 `dedupeLinks` 배선이 안 된 것이다.
- 어느 시험이든 **위에 적힌 사유가 아닌 이유**로 죽으면 멈추고 원문을 읽어라 — 이 계획의 전제가 틀린 것이다.

- [ ] **Step 3: `dedupeLinks` 를 `finish_followups.go` 파일 끝에 넣는다**

```go
// dedupeLinks 는 판단 링크에서 같은 (종류·대상)을 처음 것만 남긴다.
//
// ★ 이것이 없으면 **판단이 사라진다.** judgment_link 의 PK 는
// (judgment_id, target_kind, target_id)(schema.sql:271) 이고 AddJudgment 는 평범한
// INSERT 다(store/judgment.go:54). finish 는 in.ItemID · in.Links · 후속 id 를 이어 붙이므로
// 셋 중 무엇이든 겹치면 ① 이 ConflictDuplicate 를 내고 Store.Tx 가 ①②③④ 를 통째로
// 롤백한다 — 넷 중 판단만이 원리적으로 파생 불가하다.
//
// ★ **잠금으로는 못 닫는 창이다.** _txlock=immediate(store/store.go:211)가 배제하는 것은
// 다른 커넥션이고, 이 겹침은 한 호출이 자기와 부딪히는 것이다.
//
// 저장층에 OR IGNORE 를 넣지 않는 이유: 그러면 "링크가 겹쳤다"가 어느 호출에서도 안 보이게
// 되고, 판단 링크를 조립하는 책임이 있는 이 계층이 자기 실수를 못 배운다.
func dedupeLinks(links []model.JudgmentLink) []model.JudgmentLink {
	seen := make(map[model.JudgmentLink]bool, len(links))
	out := make([]model.JudgmentLink, 0, len(links))
	for _, l := range links {
		if seen[l] {
			continue
		}
		seen[l] = true
		out = append(out, l)
	}
	return out
}
```

- [ ] **Step 4: `finish.go` 두 자리를 고친다**

`finish.go:199-203` 의 링크 조립을 감싼다:

```go
		// ① 판단 — 가장 먼저 저장한다. 이것이 원리적으로 파생 불가한 유일한 자산이다.
		//
		// ★ dedupeLinks 를 지난다. judgment_link 의 PK 가 (판단·종류·대상)이라 겹치면
		//   이 INSERT 가 실패하고 **그 판단이 통째로 사라진다** — 사유는 그 함수 주석에 있다.
		links := append([]model.JudgmentLink{{TargetKind: "item", TargetID: in.ItemID}}, in.Links...)
		for _, f := range in.Followups {
			links = append(links, model.JudgmentLink{TargetKind: "item", TargetID: f.ID})
		}
		j, err := t.AddJudgment(model.Judgment{
			Project: in.Project, SessionID: in.SessionID, At: now,
			Kind: model.JudgmentHandoff, Title: in.Title, Body: in.Body, Links: dedupeLinks(links),
		})
```

`finish.go:160` 의 전단 검증 루프 머리에 중복 id 거절을 넣는다(루프 앞에 `seen` 선언, `ValidateItemID` 바로 뒤에 검사):

```go
	// ★ 같은 호출에 같은 후속 id 가 두 번 실리면 여기서 끊는다. 링크는 dedupeLinks 가
	//   살리지만, 두 번째 AddItem 은 **자기 트랜잭션의 첫 INSERT** 때문에 중복 흡수로 빠져
	//   "남이 만든 것이라 건너뛰었다"는 거짓 사유가 응답에 나간다.
	seen := make(map[string]bool, len(in.Followups))
	for i, f := range in.Followups {
		if err := ValidateItemID(f.ID); err != nil {
			…기존…
		}
		if seen[f.ID] {
			return FinishResult{}, &RefusedError{What: "finish",
				Reason:   fmt.Sprintf("%d번째 후속(%s)이 같은 호출에 두 번 실렸다", i+1, clip(f.ID, 64)),
				Guidance: "같은 항목을 두 번 만들 수도, 두 번 이을 수도 없다 — 한 번만 실어라."}
		}
		seen[f.ID] = true
		…기존 title/body·paths 검사…
	}
```

- [ ] **Step 5: 초록을 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/service/ && gofmt -l . && go vet ./...`
Expected: 전부 PASS · 무출력.

- [ ] **Step 6: 커밋**

```bash
git add plugins/flightdeck/server/internal/service/finish.go \
        plugins/flightdeck/server/internal/service/finish_followups.go \
        plugins/flightdeck/server/internal/service/finish_links_test.go
git commit -m "$(cat <<'EOF'
fix(flightdeck): 판단 링크가 겹치면 판단이 통째로 사라지고 있었다

judgment_link 의 PK 는 (judgment_id, target_kind, target_id) 인데 finish 는
in.ItemID · in.Links · 후속 id 를 중복 제거 없이 이어 붙였다. 겹치면 ① 의
AddJudgment 가 ConflictDuplicate 를 내고 Store.Tx 가 ①②③④ 를 통째로
롤백한다 — 넷 중 판단만이 원리적으로 파생 불가한데 그것이 사라진다.

중복 후속 id 는 오늘 흡수 갈래로 가지도 못한다 — 링크가 먼저 겹쳐 ① 에서 죽으므로
판단 0건 · 후속 0건 · 항목은 선점된 채로 남고, 오류마저 어느 id 인지 말하지 않는다.
흡수 갈래는 dedupeLinks 를 넣은 뒤에야 나타나고, 그때의 거짓 사유를 2번이 막는다.

1. dedupeLinks 로 (종류·대상)을 한 번만 남긴다.
2. 같은 호출에 같은 후속 id 가 두 번 실리면 트랜잭션 전에 거절한다.

잠금은 이 창을 못 닫는다 — _txlock=immediate 가 배제하는 것은 다른
커넥션이고, 이 겹침은 한 호출이 자기와 부딪히는 것이다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: 자격의 원천을 하나로 만든다 — `sessionSpawnedOpen`

**Files:**
- Modify: `plugins/flightdeck/server/internal/service/finish_followups.go:87-117` (`followupCandidates` 를 둘로 가른다)

**Interfaces:**
- Produces: `func (s *Service) sessionSpawnedOpen(ctx context.Context, in FinishInput) ([]string, bool)` — 이 선점 뒤 이 세션이 만든 **열린** 항목 id 들과, 그 판정을 **실제로 관측했는지**. Task 3 의 `classifyFollowups` 가 이것을 쓴다.
- Consumes: `s.st.GetClaim(ctx, project, itemID) (model.Claim, error)` · `s.st.ListSessionEvents(ctx, sessionID, kind string, since time.Time) ([]model.Event, error)` · `s.st.GetItem(ctx, project, id) (model.Item, error)` · `eventItemID(model.Event) string`(같은 파일 :140).

- [ ] **Step 1: 순수 리팩터다 — 새 시험을 쓰지 않는다**

동작이 하나도 안 바뀌므로 기존 관문 시험 셋(`TestFinishStopsOnceWhenFollowupsFellOnTheFloor` · `TestFinishFollowupGateIgnoresClosedAndPreClaimItems` · `TestFinishFollowupGateIsPerItem`)이 그대로 초록인 것이 이 태스크의 관문이다. 자격 축의 새 단정은 Task 3 에서 **거동과 함께** 들어온다.

★ **단 `observed` bool 은 이 태스크에 시험이 없다.** 그 축을 잠그는 단정은 Task 3 Step 1 의 `TestRefuseIneligibleFollowupSaysWhichOfTheThreeReasons` **하나뿐**이다. Task 3 을 그 시험 없이 끝내면 이 리팩터는 무시험 축이 되고, 다음 개정이 `sessionSpawnedOpen` 을 `[]string` 하나로 되접어도 `go test ./... -race` 가 전부 초록이다 — 이 리팩터가 막으려던 그 실패다. 선례가 이미 있다: `internal/mcpsrv/render_accounting_test.go:245-253` 이 `StillHeld: nil` 갈래를 그렇게 직접 단정한다.

- [ ] **Step 2: `followupCandidates` 를 가른다**

`finish_followups.go:87-117` 을 통째로 이 둘로 바꾼다(기존 ★주석 둘은 `sessionSpawnedOpen` 으로 옮긴다):

```go
// sessionSpawnedOpen 은 "이 세션이 이 선점 뒤에 만들었고 아직 열려 있는" 항목 id 들과,
// **그 판정을 실제로 관측했는지**다.
//
// ★ **닫힌 것은 세지 않는다.** 실측 사례가 있다: 한 세션이 만든
// `fd-footprint-has-no-containment-gate` 를 남이 집어 랜딩까지 해서, 그 세션이 마무리할
// 때는 이미 닫혀 있었다. 그것까지 세면 거짓 거절이 된다.
//
// ★ **선점 뒤로 자른다.** 오래 사는 세션은 앞선 작업에서 만든 항목을 갖고 있다.
// 그것까지 세면 항목 하나를 끝낼 때마다 과거 전부가 딸려 온다.
//
// ★ **관측 여부를 값으로 나르는 이유.** 소비자 둘이 같은 빈 목록에 정반대로 반응해야 한다 —
// 관문(judgeMissingFollowups)은 못 읽었으면 **안 막고**(마무리를 잃지 않는 쪽), 잇기
// (classifyFollowups)는 못 읽었으면 **안 잇는다**(거짓 링크를 안 만드는 쪽). 빈 슬라이스
// 하나로 접으면 그 둘이 같은 값이 되어, 다음 개정이 한쪽을 고치며 다른 쪽을 조용히 뒤집는다.
// FinishResult 의 StillHeld·QueueBalance 를 포인터로 둔 것과 같은 규율이다(finish.go:80·89).
//
// ★ **item.add 이벤트로만 판정한다.** 그 이벤트를 남기는 자리는 Service.AddItem(pick.go:1164)
// 하나뿐이라, **finish 의 후속으로 만들어진 항목은 여기 안 걸린다.** 그것까지 세려면 finish 도
// item.add 를 남겨야 하는데, 그러면 관문의 사정거리가 함께 넓어져 거짓 거절이 는다 —
// 별개 축이라 후속 항목으로 낸다. 거절 문구가 이 부류를 갈라 말한다.
func (s *Service) sessionSpawnedOpen(ctx context.Context, in FinishInput) ([]string, bool) {
	claim, err := s.st.GetClaim(ctx, in.Project, in.ItemID)
	if err != nil || claim.At.IsZero() {
		return nil, false // 언제부터 쥐었는지 모르면 자를 지점이 없다
	}
	evs, err := s.st.ListSessionEvents(ctx, in.SessionID, "item.add", claim.At)
	if err != nil {
		return nil, false
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range evs {
		id := eventItemID(e)
		if id == "" || id == in.ItemID || seen[id] {
			continue
		}
		seen[id] = true
		it, err := s.st.GetItem(ctx, in.Project, id)
		if err != nil || it.State != model.ItemOpen {
			continue // 못 읽었거나 이미 닫혔다
		}
		out = append(out, id)
	}
	return out, true
}

// followupCandidates 는 위에서 **이번 followups 에 실린 것을 뺀** 목록이다.
// 관문이 "바닥에 떨어뜨린 후속"이라고 부르는 것이 이것이다.
//
// 관측을 못 하면 빈 목록이다 — 관문은 그때 **안 막는다**(fail-open). 계측 하나가
// 실패했다고 마무리를 잃는 것이 훨씬 나쁘고, 그 판정은 이 파일이 아니라 원장이 내려야 한다.
func (s *Service) followupCandidates(ctx context.Context, in FinishInput) []string {
	ids, observed := s.sessionSpawnedOpen(ctx, in)
	if !observed {
		return nil
	}
	given := make(map[string]bool, len(in.Followups))
	for _, f := range in.Followups {
		given[f.ID] = true
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if given[id] {
			continue
		}
		out = append(out, id)
	}
	return out
}
```

- [ ] **Step 3: 기존 시험이 그대로 초록인지 본다**

Run: `cd plugins/flightdeck/server && go test ./internal/service/ -run TestFinishFollowup -v && go test ./internal/service/ -run TestFinishStopsOnce -v`
Expected: 전부 PASS. 하나라도 빨가면 리팩터가 동작을 바꾼 것이다 — 되돌리고 diff 를 다시 봐라.

- [ ] **Step 4: 커밋**

```bash
git add plugins/flightdeck/server/internal/service/finish_followups.go
git commit -m "$(cat <<'EOF'
refactor(flightdeck): 후속 자격의 원천을 sessionSpawnedOpen 하나로 모은다

관문이 이름을 내는 목록과 앞으로 "이을 수 있는 것"의 정의가 같아야 한다.
둘로 갈리면 다음 개정에서 어긋난다.

관측 여부를 bool 로 함께 낸다 — 소비자 둘이 같은 빈 목록에 정반대로
반응해야 하기 때문이다. 관문은 못 읽었으면 안 막고(마무리를 잃지 않는 쪽),
잇기는 못 읽었으면 안 잇는다(거짓 링크를 안 만드는 쪽). 빈 슬라이스로
접으면 다음 개정이 한쪽을 고치며 다른 쪽을 조용히 뒤집는다.

거동 변화 0. 기존 관문 시험 셋이 그대로 초록이다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: 부적격 id 를 트랜잭션 전에 거절한다

**Files:**
- Modify: `plugins/flightdeck/server/internal/service/finish_followups.go` (파일 끝에 `followupCreate`·`followupPlan`·`classifyFollowups`·`itemExists`·`refuseIneligibleFollowup`·`linkableHint`)
- Modify: `plugins/flightdeck/server/internal/service/finish.go:152-184` (분류 호출 + 검증 루프를 만들 것에만. **`172-177` 의 ★주석은 옮겨 보존한다**)
- Modify: `plugins/flightdeck/server/internal/service/finish_test.go:633`·`:675`·`:715` (뒤집는 계약 셋 다시 쓰기)
- Modify: `plugins/flightdeck/server/internal/service/finish_links_test.go` (시험 여섯 추가 — 부적격 셋 + `title`·`body` 필수 + 오타 사유 + 거절 사유 세 갈래)

**Interfaces:**
- Produces:
  - `type followupCreate struct { Index int; Item FollowupInput }` — `Index` 는 **요청에서 몇 번째였나(1-based)**. 오류 문구가 요청 좌표를 잃지 않게 나른다.
  - `type followupPlan struct { Create []followupCreate; Link []string; Eligible []string }`
  - `func (s *Service) classifyFollowups(ctx context.Context, in FinishInput) (followupPlan, *RefusedError)`
  - `func (s *Service) itemExists(ctx context.Context, project, id string) bool`
  - `func refuseIneligibleFollowup(nth int, id, itemID string, eligible []string, observed bool) *RefusedError`
  - `func linkableHint(eligible []string) string` — id 만 실은 후속이 **만들기**로 떨어졌을 때(= 그 id 의 항목이 아예 없다 = 오타) 그 사실을 말하는 한 줄.
- Consumes: Task 2 의 `sessionSpawnedOpen`.

- [ ] **Step 1: 빨강 시험 셋 + 딸린 셋을 쓴다 — 부적격 세 부류와 그 이웃**

`finish_links_test.go` 에 이어 붙인다.

★ 아래 여섯 중 **마지막 하나(`TestRefuseIneligibleFollowupSaysWhichOfTheThreeReasons`)는 순수 함수 시험**이라 `refuseIneligibleFollowup` 이 없으면 패키지가 아예 안 컴파일된다. 앞 다섯을 먼저 붙여 Step 2 의 첫 명령으로 빨강 이유를 확인한 **뒤에** 그것을 붙인다.

```go
// TestFinishRefusesAFollowupThatBelongsToSomeoneElse 는 이 기능의 **안전 축**이다.
//
// ★ 회귀이기도 하다. 오늘은 남의 항목 id 를 후속으로 넣으면 항목만 안 만들어지고
// **판단 링크는 그대로 걸린다**(finish.go:199 가 AddItem 보다 먼저 링크를 짜고
// judgment_link.target_id 에 REFERENCES 가 없다). 즉 오타 하나로 남의 항목이 내 판단에
// 조용히 이어진다. 아래 링크 0건 단정이 그 문을 닫는다.
//
// ★ **title·body 를 일부러 채운다.** 안 채우면 지금 판이 finish.go:166 의
// "제목이나 본문이 없다" 로 **먼저** 거절하고, 그 사유 문자열에 id 가 박혀 있어
// 아래 Contains 까지 참이 된다 — err != nil · id 포함 · 판단 0건 · 링크 0건이
// 전부 만족돼 시험이 **구현 전에도 초록**이 된다(실측으로 셋 다 PASS 했다).
// 그러면 이 시험은 자격 축을 하나도 안 잠근다 — classifyFollowups 를 통째로 지워도 초록이다.
// 채우면 지금 판이 실제로 성공하고(err == nil) judgment_link 에 남의 항목이 걸려,
// 링크 0건 단정이 진짜로 회귀를 잠근다. 구현 뒤에는 classifyFollowups 거절이
// 제목·본문 관문보다 먼저 오므로(Step 4 의 ②→③ 순서) 이 값들은 경로를 안 바꾼다.
// id 만 싣는 잇기 경로는 Task 4 의 성공 시험이 잠근다.
func TestFinishRefusesAFollowupThatBelongsToSomeoneElse(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	other := openSession(t, s, "p", repo, repo, "cc-2", "트랙7")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")
	addItemAs(t, s, "p", other.Session.ID, "someone-elses")

	_, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone, Title: "끝냈다", Body: "본문",
		Followups: []FollowupInput{{ID: "someone-elses", Title: "제목", Body: "본문"}},
	})
	if err == nil {
		t.Fatalf("남이 만든 항목을 후속으로 이었는데 통과했다")
	}
	if !strings.Contains(err.Error(), "someone-elses") {
		t.Fatalf("거절 사유가 어느 id 인지 안 낸다:\n%s", err.Error())
	}
	// ★ **title/body 거절로 되돌아가는 회귀를 이 줄이 막는다.** "이을 자격이 없다" 는
	//   refuseIneligibleFollowup 의 Reason 에만 있고 다른 어떤 거절에도 없다.
	//   ("이을 수 있는 것은" 은 그 Guidance 첫 줄에도 있어 사유 갈래와 무관하게 늘 맞으므로
	//    쓰면 안 된다 — 이 셋 중 둘은 len(eligible)==0 갈래라 Reason 쪽 그 문구가 안 나온다.)
	if !strings.Contains(err.Error(), "이을 자격이 없다") {
		t.Fatalf("자격 축이 아닌 다른 관문이 먼저 거절했다 — 이 시험은 무엇도 안 잠근다:\n%s", err.Error())
	}
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 0 {
		t.Fatalf("거절인데 판단이 %d건 남았다 — 트랜잭션 진입 전이라 아무것도 안 써야 한다", n)
	}
	if n := countRows(t, st,
		`SELECT count(*) FROM judgment_link WHERE target_id = 'someone-elses'`); n != 0 {
		t.Fatalf("남의 항목에 판단 링크가 %d건 걸렸다 — 오타 하나로 남의 항목이 내 판단에 붙는다", n)
	}
}

// TestFinishRefusesAFollowupThatIsAlreadyClosed 는 닫힌 항목을 못 잇게 한다.
//
// 닫힌 것을 이으면 판단이 "이 작업이 낳은 후속"이라고 말하는 대상이 이미 끝난 일이 된다.
// 관문(sessionSpawnedOpen)도 같은 이유로 닫힌 것을 안 센다 — 두 목록이 한 정의에서 나온다.
func TestFinishRefusesAFollowupThatIsAlreadyClosed(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")
	addItemAs(t, s, "p", me.Session.ID, "already-landed")
	if err := st.SetItemState(ctx(), "p", "already-landed", model.ItemDone, "남이 끝냈다"); err != nil {
		t.Fatalf("전제 구성 실패: %v", err)
	}

	_, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone, Title: "끝냈다", Body: "본문",
		Followups: []FollowupInput{{ID: "already-landed", Title: "제목", Body: "본문"}},
	})
	if err == nil {
		t.Fatalf("이미 닫힌 항목을 후속으로 이었는데 통과했다")
	}
	// ★ 위 시험과 같은 이유로 자격 축 문구를 못 박는다 — title·body 를 빼면 다른 관문이
	//   먼저 거절해 이 시험이 구현 전에도 초록이 된다.
	if !strings.Contains(err.Error(), "이을 자격이 없다") {
		t.Fatalf("자격 축이 아닌 다른 관문이 먼저 거절했다 — 이 시험은 무엇도 안 잠근다:\n%s", err.Error())
	}
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 0 {
		t.Fatalf("거절인데 판단이 %d건 남았다", n)
	}
}

// TestFinishRefusesAFollowupMadeBeforeTheClaim 은 **선점 전**에 만든 자기 항목도 못 잇게 한다.
//
// 오래 사는 세션은 앞선 작업의 항목을 갖고 있다. 그것을 이으면 이번 판단이 낳지 않은 일까지
// "이 작업의 후속"이 된다 — 관문이 선점 시각으로 자르는 것과 같은 이유다.
func TestFinishRefusesAFollowupMadeBeforeTheClaim(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItemAs(t, s, "p", me.Session.ID, "from-earlier-work")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")

	_, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone, Title: "끝냈다", Body: "본문",
		Followups: []FollowupInput{{ID: "from-earlier-work", Title: "제목", Body: "본문"}},
	})
	if err == nil {
		t.Fatalf("선점 전에 만든 항목을 후속으로 이었는데 통과했다")
	}
	// ★ 위 둘과 같은 이유로 자격 축 문구를 못 박는다.
	if !strings.Contains(err.Error(), "이을 자격이 없다") {
		t.Fatalf("자격 축이 아닌 다른 관문이 먼저 거절했다 — 이 시험은 무엇도 안 잠근다:\n%s", err.Error())
	}
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 0 {
		t.Fatalf("거절인데 판단이 %d건 남았다", n)
	}
}

// TestFinishStillRequiresTitleAndBodyForANewFollowup 은 이 태스크가 **안 뒤집는** 계약이다
// (설계 §6-4). 새로 만드는 후속은 여전히 title·body 가 필수다.
//
// ★ 이 관문의 문구 "제목이나 본문"을 단정하는 시험이 저장소에 **하나도 없다** —
// FollowupInput 을 쓰는 시험 13곳이 전부 Title·Body 를 채운다. 그런데 이 태스크는
// 그 검사를 in.Followups 루프에서 plan.Create 루프로 **옮기고**, Task 6 은
// followupSchema 의 required 를 id 하나로 낮춘다(tools.go:67). 그러면 title 없이 보내는 것이
// **처음으로 정상 경로**가 되고 이 서비스 계층 검사가 남는 유일한 관문이 되는데,
// 그 관문을 보는 시험이 없으면 조건을 흘려도(Create 가 비어 루프를 안 돌거나, c.Item 대신
// 다른 것을 보거나) 전 스위트가 초록이다. 스키마 required 를 단정하는 시험도 저장소에 없다.
//
// ★ **적격 잇기를 첫째에, 새 후속을 둘째에 놓는다.** 이것이 이 시험을 빨강으로도 만든다:
//   지금 판은 in.Followups 를 전수로 도므로 **첫째**(spun-off-axis)에서 먼저 죽어
//   "1번째 후속(spun-off-axis)에 제목이나 본문이 없다" 를 낸다 — 아래 "2번째 후속(brand-new)"
//   단정이 그것을 잡는다. 구현 뒤에는 첫째가 잇기로 빠지고 둘째만 관문을 지나며,
//   요청 좌표(followupCreate.Index)가 살아 있어야만 "2번째" 가 나온다.
//   그 필드를 이 태스크가 일부러 만들었는데 그것을 보는 단정이 여기 말고 없다.
func TestFinishStillRequiresTitleAndBodyForANewFollowup(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")
	addItemAs(t, s, "p", me.Session.ID, "spun-off-axis") // 선점 뒤 · 이 세션 · 열림 → 이을 수 있다

	_, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone, Title: "끝냈다", Body: "판단 본문",
		Followups: []FollowupInput{
			{ID: "spun-off-axis"}, // id 만 — 이것은 이어진다(제목·본문을 다시 안 적는다)
			{ID: "brand-new"},     // id 만 — 이것은 **새로 만들 것**이라 거절돼야 한다
		},
	})
	if err == nil {
		t.Fatalf("제목·본문 없는 새 후속이 통과했다 — 빈 항목이 큐에 들어간다")
	}
	msg := err.Error()
	for _, want := range []string{"2번째 후속(brand-new)", "제목이나 본문이 없다"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("거절 사유에 %q 가 없다:\n%s", want, msg)
		}
	}
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 0 {
		t.Fatalf("거절인데 판단이 %d건 남았다 — 트랜잭션 진입 전이라 아무것도 안 써야 한다", n)
	}
	if n := countRows(t, st, `SELECT count(*) FROM item WHERE id = 'brand-new'`); n != 0 {
		t.Fatalf("거절인데 항목이 %d건 만들어졌다", n)
	}
}

// TestFinishNamesTheLinkTargetWhenTheFollowupIDDoesNotExist 는 **오타의 사유가 갈리는 자리**다.
//
// id 만 실은 후속은 이제 "잇겠다"는 뜻이다(도구 스키마가 그렇게 가르친다 — Task 6).
// 그 id 가 오타면 그런 항목이 없어 분류가 '만들기'로 떨어지고, 거절은 "제목이나 본문이 없다"가
// 된다 — 세션은 진짜 사유를 못 받고 제목·본문을 지어내 **쌍둥이**를 만든다. 이으려던 항목은
// 큐에 그대로 남는다. 이 도구가 없애려는 부류의 조용한 거짓과 같은 모양이라 여기서 닫는다.
func TestFinishNamesTheLinkTargetWhenTheFollowupIDDoesNotExist(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")
	addItemAs(t, s, "p", me.Session.ID, "spun-off-axis") // 이을 수 있었던 것

	_, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone, Title: "끝냈다", Body: "판단 본문",
		Followups: []FollowupInput{{ID: "spun-of-axis"}}, // 오타 — 이런 항목은 없다
	})
	if err == nil {
		t.Fatalf("없는 id 를 id 만 실었는데 통과했다")
	}
	for _, want := range []string{"이을 셈이었다면", "spun-off-axis"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("거절이 %q 를 안 낸다 — 세션이 제목·본문을 지어내 쌍둥이를 만든다:\n%s", want, err.Error())
		}
	}
	if n := countRows(t, st, `SELECT count(*) FROM item WHERE id = 'spun-of-axis'`); n != 0 {
		t.Fatalf("오타 id 로 항목이 %d건 만들어졌다", n)
	}
}

// TestRefuseIneligibleFollowupSaysWhichOfTheThreeReasons 는
// **`observed` bool 을 지키는 유일한 자리다.**
//
// 위 통합 시험 셋은 전부 `observed=true · eligible 빈 목록` 한 갈래만 밟는다(셋 다 claimed 를
// 먼저 하고, 자격자가 있으면 거절이 아니라 잇기가 되기 때문이다). 관측 실패 갈래와 이름을 내는
// 갈래는 통합 경로로 안 걸린다. 그래서 세 갈래를 여기서 **순수 함수로 직접** 부른다 —
// 이 단정이 없으면 다음 개정이 sessionSpawnedOpen 을 []string 하나로 되접어도 전부 초록이다.
// 같은 빈 값이 두 뜻을 갖는 모양은 이 저장소가 반복해서 닫아 온 실패고,
// render_accounting_test.go:245 가 StillHeld 의 nil 갈래를 같은 방식으로 직접 단정한다.
func TestRefuseIneligibleFollowupSaysWhichOfTheThreeReasons(t *testing.T) {
	unobserved := refuseIneligibleFollowup(1, "x", "batch7", nil, false)
	none := refuseIneligibleFollowup(1, "x", "batch7", nil, true)
	named := refuseIneligibleFollowup(2, "x", "batch7", []string{"spun-off-axis"}, true)

	if !strings.Contains(unobserved.Reason, "못 읽어") {
		t.Fatalf("관측 실패를 사유로 안 낸다:\n%s", unobserved.Reason)
	}
	if unobserved.Reason == none.Reason {
		t.Fatalf("관측 실패와 '만든 것이 없다'가 같은 문구다 — 세션이 없는 사고를 쫓는다:\n%s",
			unobserved.Reason)
	}
	if !strings.Contains(none.Reason, "하나도 없다") {
		t.Fatalf("관측은 했는데 자격자가 없다는 사실을 안 낸다:\n%s", none.Reason)
	}
	if !strings.Contains(named.Reason, "spun-off-axis") {
		t.Fatalf("이을 수 있는 것의 이름을 안 낸다 — 수만 말하면 다시 조사해야 한다:\n%s",
			named.Reason)
	}
	if !strings.Contains(named.Reason, "2번째") {
		t.Fatalf("요청 좌표(몇 번째 후속인지)를 잃었다:\n%s", named.Reason)
	}
}
```

- [ ] **Step 2: 빨강을 확인한다 — 두 번 나눠 본다**

Run(① 앞 다섯을 붙인 상태에서):
`cd plugins/flightdeck/server && go test ./internal/service/ -run 'TestFinishRefusesAFollowup|TestFinishStillRequiresTitleAndBody|TestFinishNamesTheLinkTarget' -v`

Expected — **다섯 다 FAIL. 단 사유가 셋으로 갈린다. 갈라 적는다.**

- `TestFinishRefusesAFollowup*` **셋 다 FAIL 이고, 실패 문구가 반드시 "…통과했다" 여야 한다.** 실측으로 확인한 오늘 판의 거동이다 — 셋 다 `err == nil` 로 돌아오고, 남의 항목에는 `judgment_link` 까지 걸린다.
- `TestFinishNamesTheLinkTargetWhenTheFollowupIDDoesNotExist` 는 `거절이 "이을 셈이었다면" 를 안 낸다` 로 죽는다 — 지금 판은 `finish.go:166` 의 문구만 낸다.
- `TestFinishStillRequiresTitleAndBodyForANewFollowup` 은 `거절 사유에 "2번째 후속(brand-new)" 가 없다` 로 죽는다. **이 관문은 오늘도 있지만 좌표가 다르다** — 지금 판은 `in.Followups` 를 전수로 돌아 첫째(`spun-off-axis`)에서 먼저 거절해 `1번째 후속(spun-off-axis)에 제목이나 본문이 없다` 를 낸다. 잇기가 생겨야 첫째가 관문을 안 지나고, `followupCreate.Index` 가 살아 있어야 `2번째` 가 나온다. 이 시험이 빨강인 것은 **"관문이 없다"가 아니라 "분류와 좌표가 아직 없다"**는 뜻이다 — 그 사실을 Step 6 에서 초록으로 되잡는다.

★ **거절 시험 셋의 `followups` 에서 `Title`·`Body` 를 빼지 마라.** 빼면 `finish.go:166` 의 "제목이나 본문이 없다" 거절이 먼저 나고, 그 거절이 `err != nil` · id 포함 · 판단 0건 · 링크 0건을 **전부** 만족시켜 구현 전에도 셋 다 PASS 한다(실측했다). 그 상태로는 이 태스크가 잠그려는 자격 축을 하나도 안 잠근다 — `classifyFollowups` 를 통째로 지워도 초록이다. `"이을 자격이 없다"` 단정이 그 되돌아감을 잡는 자리다.

곁들여 확인할 것(이 태스크가 닫는 문이 실재한다는 증거): 구현 전 `judgment_link` 에 `target_id='someone-elses'` 행이 **1건** 생긴다 — 오타 하나로 남의 항목이 내 판단에 이어지는 그 문이다.

Run(② `TestRefuseIneligibleFollowupSaysWhichOfTheThreeReasons` 를 붙인 뒤):
`cd plugins/flightdeck/server && go test ./internal/service/ -run TestRefuseIneligibleFollowup -v`
Expected: `undefined: refuseIneligibleFollowup` 로 **빌드 실패.** 그것이 이 단계의 정상적인 빨강이다 — 함수가 아직 없다. Step 3 뒤에 여섯이 함께 초록이 된다.

- [ ] **Step 3: 분류기를 `finish_followups.go` 파일 끝에 넣는다**

```go
// followupCreate 는 "새로 만들 후속"이다. Index 는 **요청에서 몇 번째였나**(1-based) —
// 분류로 순서가 갈려도 오류 문구가 요청 좌표를 잃지 않게 나른다.
type followupCreate struct {
	Index int
	Item  FollowupInput
}

// followupPlan 은 이번 마무리의 후속을 만들 것과 이을 것으로 가른 결과다.
//
// ★ Eligible 은 **이 호출에서 이을 수 있었던 것 전부**다. 거절 문구가 이름을 내는 데 쓴다 —
// id 만 실었는데 그 id 의 항목이 아예 없으면(오타) 분류는 '만들기'로 떨어지고, 그때
// "이으려던 것이 없다"는 진짜 사유를 낼 자리가 finish.go 의 ③ 밖에 없다.
type followupPlan struct {
	Create   []followupCreate
	Link     []string
	Eligible []string
}

// classifyFollowups 는 후속마다 만들기·잇기·거절 중 하나를 고른다.
//
// ★ **여기가 거절할 수 있는 마지막 자리다.** 트랜잭션 안에서 거절하면 Store.Tx 가 ①②③④ 를
// 통째로 롤백해 판단이 함께 죽는다. 그래서 자격 판정은 tx **밖**에 있고, tx 안의 같은 판정은
// 분기에만 쓴다(finish.go 의 tx 절을 보라).
//
// ★ 거절이 안전한 이유. 이 자리는 트랜잭션 전이라 **아무것도 안 쓴다.** 판단 본문은 아직
// 세션 손에 있으므로 그 후속만 빼고 다시 부르면 된다 — title·body 누락 거절(finish.go:166)과
// 같은 자리·같은 성격이다.
func (s *Service) classifyFollowups(ctx context.Context, in FinishInput) (followupPlan, *RefusedError) {
	var plan followupPlan
	if len(in.Followups) == 0 {
		return plan, nil
	}
	eligible, observed := s.sessionSpawnedOpen(ctx, in)
	plan.Eligible = eligible // 거절 문구가 이름을 내는 데 쓴다 — 위 followupPlan 주석
	canLink := make(map[string]bool, len(eligible))
	for _, id := range eligible {
		canLink[id] = true
	}
	for i, f := range in.Followups {
		switch {
		case canLink[f.ID]:
			plan.Link = append(plan.Link, f.ID)
		case s.itemExists(ctx, in.Project, f.ID):
			return followupPlan{}, refuseIneligibleFollowup(i+1, f.ID, in.ItemID, eligible, observed)
		default:
			plan.Create = append(plan.Create, followupCreate{Index: i + 1, Item: f})
		}
	}
	return plan, nil
}

// itemExists 는 그 id 의 항목이 지금 있는지다.
//
// ★ **조회가 실패하면 "없다"로 접는다.** 이 판정은 갈래를 고르는 참고값이고, 정본 판정은
// 트랜잭션 안의 INSERT 가 내는 *store.ConflictError 다 — store 패키지 머리가 못박은 규율
// ("제약을 미리 흉내 내 판정하지 않는다")을 이 계층도 따른다. 없다고 접어 만들러 가면
// 최악의 경우 tx 가 중복을 잡아 그 후속만 건너뛴다(판단은 산다).
func (s *Service) itemExists(ctx context.Context, project, id string) bool {
	_, err := s.st.GetItem(ctx, project, id)
	return err == nil
}

// refuseIneligibleFollowup 은 "이미 있는데 이을 자격이 없는" 후속을 거절한다.
//
// ★ **사유를 셋으로 가른다.** 관측을 못 한 것과 남의 항목인 것을 같은 문구로 접으면 세션이
// 없는 사고를 쫓는다. 그리고 이을 수 있는 것의 **이름을 전부** 낸다 — 수만 말하면 무엇을
// 실을지 다시 조사해야 한다(관문 judgeMissingFollowups 가 같은 규율을 쓴다).
func refuseIneligibleFollowup(nth int, id, itemID string, eligible []string, observed bool) *RefusedError {
	var why string
	switch {
	case !observed:
		why = fmt.Sprintf("%s 를 언제 선점했는지 원장에서 못 읽어 자격을 판정할 수 없다", clip(itemID, 64))
	case len(eligible) == 0:
		why = "이 세션이 이 선점 뒤에 add 로 만든 열린 항목이 하나도 없다"
	default:
		why = fmt.Sprintf("이을 수 있는 것은 %s 뿐이다", strings.Join(eligible, ", "))
	}
	return &RefusedError{
		What: "finish",
		Reason: fmt.Sprintf("%d번째 후속(%s)은 이미 있는 항목인데 이을 자격이 없다 — %s",
			nth, clip(id, 64), why),
		Guidance: `이을 수 있는 것은 **이 세션이 이 선점 뒤에 add 로 만든, 아직 열린 항목**뿐이다.
남의 항목 · 이미 닫힌 항목 · 선점 전에 만든 항목은 못 잇는다 — 오타 하나로 남의 항목이
내 판단에 이어지는 것을 막는 유일한 자리다. finish 의 후속으로 만들어진 항목도 못 잇는다
(원장에 "이 세션이 만들었다"가 안 남는다).

내용이 다르면 다른 id 로 add 해서 이번 followups 에 실어라.
그 항목에 판단만 걸고 싶으면 note(kind='handoff', item_id=<그 항목>) 를 쓴다.`,
	}
}

// linkableHint 는 "이을 수 있었던 것"의 이름을 낸다.
//
// ★ id 만 실은 후속이 **만들기**로 떨어지는 길은 하나뿐이다 — 그 id 의 항목이 없다(오타).
// 그때 "제목이나 본문이 없다"만 내면 세션은 제목·본문을 지어내 옆에 새 항목을 만든다.
// 이으려던 항목은 큐에 그대로 남고 쌍둥이가 하나 는다 — 이 도구가 없애려는 부류의
// 조용한 거짓이다. 이름을 내는 이유는 refuseIneligibleFollowup 과 같다(수만 말하면
// 무엇을 실을지 다시 조사해야 한다).
func linkableHint(eligible []string) string {
	if len(eligible) == 0 {
		return "이을 셈이었다면 — 이 선점 뒤 이 세션이 add 로 만든 열린 항목이 하나도 없어 이을 것이 없다."
	}
	return "이을 셈이었다면 그 id 의 항목이 없다는 뜻이다 — 지금 이을 수 있는 것은 " +
		strings.Join(eligible, ", ") + " 뿐이다."
}
```

- [ ] **Step 4: `finish.go` 를 배선한다**

`finish.go:152-184` 를 이 순서로 바꾼다. **함수 본문은 안 늘리고 호출만 넣는다**(새 함수는 전부 `finish_followups.go` 에 있다).

**★ 기존 ★주석(`finish.go:172-177`, 경로 관문이 우회 문이 되는 것을 막는 근거)은 지우지 말고 ③ 의 `judgeItemPathsCoordinate` 위로 그대로 옮긴다** — `pick.go:1142-1148` 의 쌍둥이 ★주석이 이 자리를 가리키고 있어, 한쪽만 남으면 다음 세션이 이 호출을 "중복 검사"로 보고 지우러 온다.

```go
	// 후속을 안 실었으면 **한 번** 붙잡는다 — 판정과 사유는 finish_followups.go 에 있다.
	if len(in.Followups) == 0 {
		if refused := s.judgeMissingFollowups(ctx, in); refused != nil {
			…기존…
		}
	}
	// ① id 자체는 전부 본다 — 잇기든 만들기든 브랜치 이름 규칙을 지나야 한다.
	seen := make(map[string]bool, len(in.Followups))
	for i, f := range in.Followups {
		if err := ValidateItemID(f.ID); err != nil { …기존… }
		if seen[f.ID] { …Task 1 의 중복 거절… }
		seen[f.ID] = true
	}
	// ② 만들 것과 이을 것을 가른다. **부적격이면 여기서 거절한다** — 트랜잭션 진입 전이라
	//    아무것도 안 쓴다. 자격 정의와 사유는 finish_followups.go 에 있다.
	plan, refused := s.classifyFollowups(ctx, in)
	if refused != nil {
		s.log.WarnContext(ctx, "마무리 거절 — 이을 자격이 없는 후속",
			"project", clip(in.Project, 64), "session_id", clip(in.SessionID, 64),
			"item", clip(in.ItemID, 64))
		return FinishResult{}, refused
	}
	// ③ 제목·본문·경로 좌표는 **새로 만드는 것에만** 건다. 잇기는 기존 항목의 본문을
	//    안 덮으므로(store 에 그럴 메서드가 아예 없다) 다시 적게 할 이유가 없다 —
	//    적게 하고 버리는 것이 조용한 거짓이다.
	for _, c := range plan.Create {
		f := c.Item
		if strings.TrimSpace(f.Title) == "" || strings.TrimSpace(f.Body) == "" {
			return FinishResult{}, &RefusedError{What: "finish",
				Reason: fmt.Sprintf("%d번째 후속(%s)에 제목이나 본문이 없다", c.Index, clip(f.ID, 64)),
				Guidance: "후속은 다음 세션이 집을 항목이다 — 제목만 있으면 " +
					"그 세션이 무엇을 해야 하는지 다시 조사해야 한다.\n" +
					linkableHint(plan.Eligible)}
		}
		// ★ followup.paths 도 add(item.paths)와 같은 관문(judgeItemPathsCoordinate,
		// pick.go)을 거친다. Finish 는 tx 안에서 t.AddItem 을 직접 불러 Service.AddItem 의
		// 검증 루프를 거치지 않으므로, 여기서 따로 부르지 않으면 같은 사람이 같은
		// 세션에서 add 는 거절당하고 finish followup 은 조용히 통과하는 우회 문이
		// 된다 — 반쪽 발화는 균일한 부재보다 나쁘다(관문이 발화한다는 것만 가르치고
		// 다른 문에서 배신한다).
		//
		// ★ 이제 **새로 만드는 것에만** 건다. 잇기는 기존 항목의 경로를 안 건드리므로
		// 통과시킬 우회 문 자체가 없다(store 에 그 항목의 paths 를 덮을 메서드가 없다).
		if err := judgeItemPathsCoordinate(f.Paths); err != nil {
			return FinishResult{}, &RefusedError{What: "finish",
				Reason: fmt.Sprintf("%d번째 후속(%s)의 %s", c.Index, clip(f.ID, 64), err),
				Guidance: "경로는 저장소 상대(internal/api/x.go) 또는 POSIX 절대경로여야 한다 — " +
					"좌표계가 다르면 이 후속 항목의 겹침 축이 조용히 죽는다."}
		}
	}
```

`linkableHint` 를 `Guidance` 에만 붙이는 이유: 사유 갈래가 둘(정말 제목·본문을 빠뜨렸다 · 이으려던 id 가 오타다)이므로 `Reason` 은 관측 사실만 두고, 두 번째 가설은 처방에 붙인다. `RefusedError.Error()` 가 `Guidance` 까지 이어 내므로(`service.go:314-319`) 시험도 화면도 이 절을 본다.

트랜잭션 안 ②의 루프는 아직 `in.Followups` 를 돈다 — **Task 4 에서 `plan` 으로 바꾼다.**

★ **이 태스크의 시험이 전부 거절 경로인 것은 아니다.** 아래 Step 5 의 `TestFinishNeverLosesAJudgmentWhenTwoSessionsRaceTheSameFollowupID` 는 tx 에 **들어간다**. 그런데도 옛 루프로 초록인 이유는, 그 시험이 싣는 후속이 존재하지 않는 id + `title`·`body` 를 갖춘 `plan.Create` 갈래뿐이어서 `in.Followups` 를 도나 `plan.Create` 를 도나 같은 답이 나오기 때문이다. 이 시험을 "범위 밖"으로 읽고 빼지 마라 — 흡수 갈래가 살아 있어야 하는 이유를 잠그는 유일한 시험이다.

★ **이 커밋 하나만 있는 중간 상태에는 알고 지나가는 거짓이 하나 있다.** 자격 있는 id-only 후속(= 이 세션이 이 선점 뒤에 `add` 로 만든 열린 항목)을 실으면 분류가 `plan.Link` 로 보내 바로 위 `title`·`body` 관문을 건너뛰는데, tx 루프는 여전히 `t.AddItem(Title:"", Body:"")` 를 친다. `item` 표에 `title`·`body` 는 NOT NULL 이지만 **빈 문자열 CHECK 가 없어**(`store/schema.sql:145-164` — CHECK 는 `state` enum 과 `dropped→close_reason` 둘뿐) INSERT 는 PK 중복에서만 죽고, `store.ConflictDuplicate` 흡수 갈래가 `SkippedFollowups` 에 담아 "같은 id 의 항목이 이미 있다" 라는 **사유가 틀린** 응답을 낸다(판단 링크는 그대로 걸린다). 이 상태를 밟는 시험은 Task 3 에 없다 — 잇기 성공 시험은 Task 4 Step 1 에서 생긴다. 그러므로 **Task 3 을 커밋한 세션은 Task 4 까지 이어서 끝낸다.** 여기서 멈추지 말고, 중간 상태로 실물 잇기를 돌려 보고 그 거짓 사유를 새 결함으로 오진하지도 마라. 부득이 Task 3 에서 멈춰야 한다면 그때만 Task 4 Step 4 의 `creates` 조립·② 루프까지를 함께 가져와 한 커밋으로 합친다(`LinkedFollowups` 필드·`item.followup_linked` 원장·`count`/`linked` 분리는 그래도 Task 4 에 남긴다).

- [ ] **Step 5: 뒤집는 계약 셋을 다시 쓴다**

`finish_test.go:633` `TestFinishKeepsJudgmentWhenFollowupIDAlreadyExists` 를 통째로 이것으로 바꾼다(이름도 바꾼다):

```go
// TestFinishWritesNothingWhenAFollowupIsIneligible 은 **옛 계약을 뒤집은 자리**다.
//
// 옛 계약: 후속 id 가 이미 있으면 그 후속만 건너뛰고 판단은 무조건 지킨다.
// 그 규율은 흡수가 **트랜잭션 안**에 있었기 때문이다 — 거기서 거절하면 판단이 함께 죽는다.
//
// 새 계약: 자격 판정이 트랜잭션 **밖**으로 나왔으므로 거절해도 아무것도 안 쓴다.
// 판단 본문은 아직 세션 손에 있고 그 후속만 빼면 그대로 다시 부를 수 있다 —
// title·body 누락 거절과 같은 자리·같은 성격이다. 그 대가로 오타 하나가 남의 항목을
// 내 판단에 잇는 문이 닫힌다(그 문은 지금 열려 있다 — 링크는 이미 걸리고 있었다).
//
// (단정 주의: claimed() 는 Pick→ClaimItem 을 거치므로 항목 상태는 이 시점에 이미 'claimed' 다.
//  "안 썼다" 를 볼 때 'open' 을 기대하면 구현이 맞아도 빨간불이 뜬다 — 실측으로 확인했다.)
func TestFinishWritesNothingWhenAFollowupIsIneligible(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	author := openSession(t, s, "p", repo, repo, "cc-a", "트랙A")
	me := openSession(t, s, "p", repo, wt, "cc-b", "트랙B")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")
	addItemAs(t, s, "p", author.Session.ID, "shared-followup")

	_, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone, Title: "끝냈다", Body: "판단 본문",
		Followups: []FollowupInput{{ID: "shared-followup", Title: "제목", Body: "본문"}},
	})
	if err == nil {
		t.Fatalf("남이 만든 항목을 후속으로 실었는데 통과했다")
	}
	// 자격 관문의 고유 문구 — 다른 관문이 먼저 거절하면 아래 단정들이 헛돈다.
	if !strings.Contains(err.Error(), "이을 자격이 없다") {
		t.Fatalf("자격 축이 아닌 다른 관문이 먼저 거절했다:\n%s", err.Error())
	}
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 0 {
		t.Fatalf("거절인데 판단이 %d건 남았다 — 트랜잭션 진입 전이라 반쪽 상태가 없어야 한다", n)
	}
	// 'open' 이 아니라 'claimed' 다 — 위 claimed() 가 Pick 을 거쳐 ClaimItem 을 부르고,
	// ClaimItem(store/item.go:645)이 항목 상태를 open→claimed 로 이미 옮겨 놨다.
	// 여기서 볼 것은 "거절이 그 상태를 건드리지 않았다" 이지 "아직 open 이다" 가 아니다.
	if n := countRows(t, st, `SELECT count(*) FROM item WHERE id = 'batch7' AND state = 'claimed'`); n != 1 {
		t.Fatalf("거절인데 항목이 여전히 이 세션 손에 있지 않다 — 그 후속만 빼고 그대로 다시 부를 수 있어야 한다")
	}
}
```

`finish_test.go:675` `TestFinishReportsSkippedFollowupInsteadOfSwallowingIt` 를 이것으로 바꾼다:

```go
// TestFinishRefusesTheWholeCallWhenOneFollowupIsIneligible 은 **부분 성공을 기각한 자리**다.
//
// 옛 계약은 섞인 호출에서 정상 후속만 살렸다. 그러면 오타 하나가 "후속 1건 등록" 안에
// 조용히 섞여 나가고, 세션은 자기가 실은 둘 중 하나가 다른 뜻이 된 것을 못 본다.
// 지금은 전체를 세운다 — 되부르는 비용은 한 번이고, 그 한 번이 무엇이 틀렸는지 이름을 낸다.
func TestFinishRefusesTheWholeCallWhenOneFollowupIsIneligible(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	author := openSession(t, s, "p", repo, repo, "cc-a", "트랙A")
	me := openSession(t, s, "p", repo, wt, "cc-b", "트랙B")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")
	addItemAs(t, s, "p", author.Session.ID, "taken-id")

	_, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone, Title: "끝냈다", Body: "판단 본문",
		Followups: []FollowupInput{
			{ID: "taken-id", Title: "제목", Body: "본문"},
			{ID: "fresh-id", Title: "제목", Body: "본문"},
		},
	})
	if err == nil {
		t.Fatalf("부적격 후속이 섞였는데 통과했다")
	}
	if !strings.Contains(err.Error(), "taken-id") {
		t.Fatalf("거절 사유가 어느 후속인지 안 낸다:\n%s", err.Error())
	}
	// 자격 관문의 고유 문구 — 아래 "정상 후속도 안 만들어졌다" 단정은 어떤 거절에서도 참이라,
	// 이 줄이 없으면 **왜** 전체가 섰는지를 이 시험이 안 잠근다.
	if !strings.Contains(err.Error(), "이을 자격이 없다") {
		t.Fatalf("자격 축이 아닌 다른 관문이 먼저 거절했다:\n%s", err.Error())
	}
	if n := countRows(t, st, `SELECT count(*) FROM item WHERE id = 'fresh-id'`); n != 0 {
		t.Fatalf("정상 후속이 %d건 만들어졌다 — 전체가 거절돼야 한다", n)
	}
}
```

`finish_test.go:715` `TestFinishLogsSkippedFollowupToLedger` 를 이것으로 바꾼다(경합의 **끝 두 갈래**를 함께 잠근다 — 어느 쪽이 나올지는 시험이 못 고른다). 이 시험이 `item.followup_skipped` 축을 보는 **유일한** 자리가 되므로(설계 개정 1-④ 3행), 흡수의 산출 둘 — 응답 `SkippedFollowups` 와 원장 — 을 **조건부로** 함께 단정한다.

```go
// TestFinishNeverLosesAJudgmentWhenTwoSessionsRaceTheSameFollowupID 는 경합의 **끝**을 잠근다.
//
// 자격 판정은 트랜잭션 **밖**이고 링크는 **안**이다. 그래서 두 세션이 같은 새 id 를 동시에
// 후속으로 실으면 끝이 둘 중 하나이고, **어느 쪽이 나오는지는 스케줄러가 정한다** —
// 시험이 고를 수 없다:
//
//	ⓐ 둘 다 classifyFollowups 를 지난 뒤에 커밋이 붙는다 → 진 쪽은 tx 안에서
//	  ConflictDuplicate 를 만나 **그 후속만 건너뛰고**(SkippedFollowups) 판단은 산다.
//	  흡수 갈래(finish.go 의 ②)가 실제로 밟히는 것은 이 경우다.
//	ⓑ 이긴 쪽이 커밋까지 마친 뒤에 진 쪽이 분류를 돈다 → 진 쪽에게 그 id 는 이미
//	  "있는 항목"이고 이을 자격이 없으므로 **트랜잭션 진입 전에 거절**된다.
//
// ★ **ⓑ 를 "판단 소실"이라고 부르면 안 된다.** 거절은 tx 진입 전이라 아무것도 안 쓰고,
//   판단 본문은 아직 세션 손에 있어 그 후속만 빼면 그대로 되부를 수 있다 —
//   title·body 누락 거절과 같은 자리·같은 성격이다. 이 시험이 잠그는 것은 "둘 다 성공한다"가
//   아니라 **반쪽 상태가 없다**는 것이다.
//
// ★ **갈래를 단정하지 않는 이유는 실측이다.** 이 시험만 -race 로 돌리면 사실상 항상 ⓑ 다
//   (측정: -run … -race -count=30 이 다섯 배치 100/100 ⓑ). 전체 suite 부하에서는 ⓐ 가 나온다.
//   "둘 다 성공한다"로 단정하면 랜딩 관문이 스케줄러에 따라 빨개지고, 원인이 시험이 아니라
//   계약이라 다음 세션이 시험만 고치다 이 설계의 진짜 모양을 덮는다.
//
// ★ **그래도 흡수의 산출은 잠근다 — 조건부로.** 이 시험이 지우는
//   TestFinishLogsSkippedFollowupToLedger 가 원장 축(item.followup_skipped 의 건수와
//   **어느** 후속인지)을 보던 유일한 자리였다. 갈래가 tx 안 전용이 됐다고 축까지 사라지면
//   그 발신이 통째로 죽거나 payload 에서 좌표가 빠져도 전 스위트가 초록이다. 그래서
//   "응답으로 건너뛰었다고 말한 세션 수" 를 세고, 원장 기록이 **정확히 그 수만큼** 있는지를
//   본다 — ⓑ 만 나온 회차에서는 둘 다 0이 되어 유령 기록도 함께 잡힌다.
func TestFinishNeverLosesAJudgmentWhenTwoSessionsRaceTheSameFollowupID(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	a := openSession(t, s, "p", repo, wt, "cc-a", "트랙A")
	b := openSession(t, s, "p", repo, repo, "cc-b", "트랙B")
	addItem(t, s, "p", "item-a", nil, nil)
	addItem(t, s, "p", "item-b", nil, nil)
	claimed(t, s, "p", a.Session.ID, "item-a")
	claimed(t, s, "p", b.Session.ID, "item-b")

	fin := func(sessionID, itemID string) (FinishResult, error) {
		return s.Finish(ctx(), FinishInput{
			Project: "p", SessionID: sessionID, ItemID: itemID,
			Outcome: model.ItemDone, Title: "끝냈다", Body: "판단 본문",
			Followups: []FollowupInput{{ID: "twin-race", Title: "제목", Body: "본문"}},
		})
	}
	type outcome struct {
		res FinishResult
		err error
	}
	outs := make(chan outcome, 2)
	go func() { r, e := fin(a.Session.ID, "item-a"); outs <- outcome{r, e} }()
	go func() { r, e := fin(b.Session.ID, "item-b"); outs <- outcome{r, e} }()

	ok, skipped := 0, 0
	for i := 0; i < 2; i++ {
		o := <-outs
		if o.err == nil {
			ok++
			// 흡수 갈래(ⓐ)를 밟았으면 응답이 **반드시** 그 사실을 말한다. 조용히 넘기면
			// 세션은 후속이 들어간 줄 알고 떠나는데 그 id 의 항목은 남이 만든 다른 것이다.
			if len(o.res.SkippedFollowups) == 1 && o.res.SkippedFollowups[0] == "twin-race" {
				skipped++
			} else if len(o.res.SkippedFollowups) != 0 {
				t.Fatalf("건너뛴 후속 목록이 %v 다 — twin-race 하나이거나 비어 있어야 한다",
					o.res.SkippedFollowups)
			}
			continue
		}
		// 거절이 아닌 오류는 tx 안에서 올라온 것이고, 그 갈래는 ① 의 판단을 함께 롤백한다.
		var refused *RefusedError
		if !errors.As(o.err, &refused) {
			t.Fatalf("경합이 거절 아닌 오류로 죽었다 — 이 모양이 판단을 잃는다: %v", o.err)
		}
		if !strings.Contains(o.err.Error(), "twin-race") {
			t.Fatalf("거절이 어느 후속인지 안 낸다:\n%s", o.err.Error())
		}
	}
	if ok == 0 {
		t.Fatalf("둘 다 거절됐다 — 먼저 커밋한 쪽은 반드시 성공해야 한다")
	}
	// ★ 판단 수 == 성공 수. 이것이 이 시험의 심장이다 — 성공했는데 판단이 없으면
	//   롤백이 파생 불가한 자산을 지운 것이고, 거절했는데 판단이 있으면 반쪽 상태가 남은 것이다.
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != ok {
		t.Fatalf("판단이 %d건인데 성공한 마무리는 %d건이다 — 성공마다 하나, 거절은 0건이어야 한다", n, ok)
	}
	if n := countRows(t, st, `SELECT count(*) FROM item WHERE id = 'twin-race'`); n != 1 {
		t.Fatalf("같은 id 의 항목이 %d건이다 — 정확히 하나여야 한다", n)
	}
	// 거절당한 세션의 항목은 **안 닫힌 채로** 남아야 되부를 수 있다(선점은 유지되므로 claimed 다).
	if n := countRows(t, st,
		`SELECT count(*) FROM item WHERE id IN ('item-a','item-b') AND state = 'done'`); n != ok {
		t.Fatalf("닫힌 항목이 %d건인데 성공은 %d건이다 — 거절당한 항목은 안 닫혀 있어야 한다", n, ok)
	}
	// ★ **건너뜀은 원장에도 남는다.** 응답 SkippedFollowups 는 그 세션과 함께 사라지므로,
	//   나중에 "왜 이 후속이 안 들어갔나"를 되짚을 수 있는 자리는 원장뿐이다.
	//   갈래를 단정하지 않으려고 기대값을 위에서 센 skipped 로 잡는다 — ⓐ 면 1, ⓑ 면 0이다.
	if n := countRows(t, st,
		`SELECT count(*) FROM event WHERE kind = 'item.followup_skipped'`); n != skipped {
		t.Fatalf("응답으로 건너뛰었다고 말한 세션은 %d개인데 원장 기록은 %d건이다 — 응답과 원장이 갈렸다",
			skipped, n)
	}
	if n := countRows(t, st,
		`SELECT count(*) FROM event WHERE kind = 'item.followup_skipped' AND payload LIKE '%twin-race%'`); n != skipped {
		t.Fatalf("원장 기록에 **어느** 후속을 건너뛰었는지가 없다 — 좌표 없는 기록은 나중에 못 되짚는다(기대 %d건, 실제 %d건)",
			skipped, n)
	}
}
```

`finish_test.go` 는 `errors` · `strings` 를 이미 import 하고 있어(파일 머리 4·5행) import 를 안 늘린다.

- [ ] **Step 6: 초록을 확인한다**

Run(①): `cd plugins/flightdeck/server && go test ./internal/service/ -run 'TestFinishRefusesAFollowup|TestRefuseIneligibleFollowup|TestFinishStillRequiresTitleAndBody|TestFinishNamesTheLinkTarget' -v`
Expected: **여섯 다** PASS. `TestRefuseIneligibleFollowupSaysWhichOfTheThreeReasons` 가 빠지면 `observed` 축이 무시험이므로 이 태스크는 안 끝난 것이다.

Run(②): `cd plugins/flightdeck/server && go test ./internal/service/ -race`
그리고 **경합 시험은 단독으로도 반복해서** 돌린다:
`go test ./internal/service/ -run TestFinishNeverLosesAJudgmentWhenTwoSessionsRace -race -count=30`

Expected: 둘 다 전부 PASS. ★ 단독 반복이 관문에 들어간 이유가 있다 — 이 시험이 밟는 갈래는 전체 suite 부하에서는 흡수(ⓐ), 단독 `-race` 에서는 거절(ⓑ)로 **거의 항상 갈린다**(실측 100/100). 한쪽만 돌리면 다른 갈래가 영영 안 밟혀, 갈래를 단정하는 단정이 들어와도 관문이 안 잡는다.

- [ ] **Step 7: 커밋**

```bash
git add plugins/flightdeck/server/internal/service/
git commit -m "$(cat <<'EOF'
feat(flightdeck): 이을 자격이 없는 후속을 트랜잭션 전에 거절한다

오늘은 남의 항목 id 를 followups 에 넣으면 항목만 안 만들어지고 판단 링크는
그대로 걸린다 — finish.go 가 AddItem 보다 먼저 링크를 짜고 judgment_link 의
target_id 에 FK 가 없기 때문이다. 즉 오타 하나로 남의 항목이 내 판단에
조용히 이어진다.

1. classifyFollowups 가 후속마다 만들기·잇기·거절을 고른다. 자격은
   sessionSpawnedOpen 하나에서 나온다 — 관문이 이름을 내는 목록 그대로다.
2. 거절은 트랜잭션 진입 전이라 아무것도 안 쓴다. 사유는 셋으로 갈라 적고
   이을 수 있는 것의 이름을 전부 낸다.
3. 제목·본문·경로 관문은 새로 만드는 것에만 건다. id 만 실었는데 그 항목이
   아예 없으면(오타) 처방이 "이을 셈이었다면 …" 을 함께 낸다 — 안 그러면
   세션이 제목·본문을 지어내 이으려던 항목 옆에 쌍둥이를 만든다.

뒤집은 옛 계약 셋과 그 근거는 각 시험 주석에 적었다.

흡수 갈래는 남는다. 다만 **경합의 끝은 하나가 아니다** — 자격 판정이 tx 밖이라,
두 세션이 같은 새 id 를 경합하면 둘 다 분류를 지난 뒤 커밋이 붙을 때만 흡수가
밟히고, 이긴 쪽이 커밋을 마친 뒤 진 쪽이 분류를 돌면 **tx 진입 전 거절**로 끝난다.
어느 쪽이 나올지는 스케줄러가 정한다(실측: 단독 -race 는 사실상 항상 거절,
전체 suite 부하에서는 흡수). 그래서 시험은 갈래를 안 고르고 **끝의 성질**을
단정한다 — 성공마다 판단 하나, 거절은 아무것도 안 씀, 그 id 의 항목은 하나,
그리고 응답이 건너뛰었다고 말한 수만큼 원장에 좌표가 남는다.
거절로 끝난 세션은 판단을 잃지 않는다. 본문이 아직 그 세션 손에 있다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: 잇기를 실제로 낸다 — `LinkedFollowups` · 원장 · tx 안 확정

**Files:**
- Modify: `plugins/flightdeck/server/internal/service/finish.go:56-97`(`FinishResult` 에 필드) · `:189-250`(tx 절) · `:373`(주석만)
- Modify: `plugins/flightdeck/server/internal/service/finish_links_test.go` (잇기 성공 시험 둘)

**Interfaces:**
- Produces: `FinishResult.LinkedFollowups []string` (`json:"linked_followups,omitempty"`) — Task 6 의 `RenderFinish` 가 읽는다.
- 원장: `item.followup_linked`(payload `item`·`why`) 신설. `item.finish` payload 의 `count` 는 **만들 것의 수**, `linked` 는 **이을 것의 수**.

- [ ] **Step 1: 빨강 시험 둘을 쓴다**

`finish_links_test.go` 에 이어 붙인다(파일 머리 import 에 `encoding/json` 과 `time` 을 더한다):

```go
// TestFinishLinksAnExistingItemInsteadOfCreatingIt 은 이 기능의 본체다.
//
// id 만 실은 기존 항목은 **새로 만들지 않고** 판단에 잇는다. 항목의 제목·본문은 그대로다 —
// store 에 항목 본문을 고치는 메서드가 아예 없고, 있어도 안 고칠 것이다(다른 세션이 그 항목의
// 본문을 근거로 계획을 세운다).
func TestFinishLinksAnExistingItemInsteadOfCreatingIt(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")
	addItemAs(t, s, "p", me.Session.ID, "spun-off-axis") // 선점 뒤 · 이 세션 · 열림

	res, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone, Title: "끝냈다", Body: "판단 본문",
		Followups: []FollowupInput{
			{ID: "spun-off-axis"},                               // id 만 — 잇는다
			{ID: "brand-new", Title: "새 제목", Body: "새 본문"}, // 만든다
		},
	})
	if err != nil {
		t.Fatalf("잇기가 실패했다: %v", err)
	}
	if len(res.LinkedFollowups) != 1 || res.LinkedFollowups[0] != "spun-off-axis" {
		t.Fatalf("이은 항목을 응답이 안 낸다: %v", res.LinkedFollowups)
	}
	if len(res.Followups) != 1 || res.Followups[0].ID != "brand-new" {
		t.Fatalf("만든 후속이 %v 다 — brand-new 하나여야 한다", res.Followups)
	}
	if len(res.SkippedFollowups) != 0 {
		t.Fatalf("건너뛴 후속이 %v 다 — 잇기는 건너뛴 것이 아니다", res.SkippedFollowups)
	}

	it, err := st.GetItem(ctx(), "p", "spun-off-axis")
	if err != nil {
		t.Fatalf("이은 항목을 못 읽는다: %v", err)
	}
	if it.Title != "spun-off-axis 제목" || it.Body != "spun-off-axis 본문" {
		t.Fatalf("잇기가 항목 본문을 덮었다: title=%q body=%q", it.Title, it.Body)
	}
	if it.State != model.ItemOpen {
		t.Fatalf("이은 항목이 %s 다 — 열린 채로 남아야 한다", it.State)
	}

	js, err := st.JudgmentsForItem(ctx(), "p", "spun-off-axis")
	if err != nil {
		t.Fatalf("판단 조회 실패: %v", err)
	}
	if len(js) != 1 || js[0].ID != res.Judgment.ID {
		t.Fatalf("이 마무리의 판단이 그 항목에 안 걸렸다: %v", js)
	}

	// 큐 수지는 **만든 것만** 센다 — 이은 항목은 이미 큐에 있었다.
	if res.QueueBalance == nil {
		t.Fatalf("큐 수지를 못 읽었다")
	}
	if res.QueueBalance.Added != 1 {
		t.Fatalf("큐 수지 added 가 %d 다 — 만든 것 1건만 세야 한다", res.QueueBalance.Added)
	}
}

// TestFinishSeparatesLinkedFromCreatedInTheLedger 는 **재생산율 R 이 오염되는 자리**를 잠근다.
//
// store.QueueReproduction(store/event.go:203)이 item.finish 의 count 를 R 의 분자로 그대로
// 더한다. 잇기를 거기 세면 만들지도 않은 항목이 R 을 부풀리고, DESIGN §10 이 이 설계를
// 판정하겠다고 세운 축이 조용히 거짓이 된다.
func TestFinishSeparatesLinkedFromCreatedInTheLedger(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")
	addItemAs(t, s, "p", me.Session.ID, "spun-off-axis")

	if _, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone, Title: "끝냈다", Body: "판단 본문",
		Followups: []FollowupInput{
			{ID: "spun-off-axis"},
			{ID: "brand-new", Title: "새 제목", Body: "새 본문"},
		},
	}); err != nil {
		t.Fatalf("마무리 실패: %v", err)
	}

	evs, err := st.ListSessionEvents(ctx(), me.Session.ID, "item.finish", time.Time{})
	if err != nil || len(evs) != 1 {
		t.Fatalf("item.finish 이벤트가 %d건이다(err=%v)", len(evs), err)
	}
	var p struct {
		Count  int `json:"count"`
		Linked int `json:"linked"`
	}
	if err := json.Unmarshal([]byte(evs[0].Payload), &p); err != nil {
		t.Fatalf("payload 를 못 읽는다: %v", err)
	}
	if p.Count != 1 {
		t.Fatalf("count 가 %d 다 — 만든 것 1건만 세야 재생산율이 안 부푼다", p.Count)
	}
	if p.Linked != 1 {
		t.Fatalf("linked 가 %d 다 — 이은 것 1건이 원장에 있어야 한다", p.Linked)
	}
	if n := countRows(t, st, `SELECT count(*) FROM event WHERE kind = 'item.followup_linked'`); n != 1 {
		t.Fatalf("item.followup_linked 가 %d건이다 — 1건이어야 한다", n)
	}
}
```

- [ ] **Step 2: 빨강을 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/service/ -run 'TestFinishLinksAnExistingItem|TestFinishSeparatesLinked' -v`
Expected: 컴파일 실패(`res.LinkedFollowups` 가 없다) 또는 FAIL. **컴파일 실패도 빨강으로 센다** — 단, 단정이 도는 것을 확인하려면 Step 3 의 필드만 먼저 넣고 다시 돌려라.

- [ ] **Step 3: `FinishResult` 에 필드를 더한다**

`finish.go:66`(`SkippedFollowups` 바로 뒤)에 넣는다:

```go
	// LinkedFollowups 는 **새로 만들지 않고 판단에 이은** 기존 항목이다.
	//
	// ★ Followups 와 갈라 두는 이유가 둘이다.
	//   ① 계측: 이것들은 큐를 안 늘린다. len(out.Followups) 가 그대로 QueueBalance 의
	//     added 이고(아래), 같은 축이 원장 item.finish 의 count 를 거쳐 재생산율 R 의
	//     분자가 된다(store/event.go:203). 여기 섞으면 만들지 않은 항목이 R 을 부풀린다.
	//   ② 화면: 세션은 "만들었다"와 "이었다"를 구분해서 봐야 한다. 같은 칸에 담으면
	//     응답이 "후속 2건 등록"이라고 말하는데 큐에는 1건만 는다.
	LinkedFollowups []string `json:"linked_followups,omitempty"`
```

- [ ] **Step 4: tx 절을 고친다**

`finish.go` 의 tx 안, 이벤트 예약부터 ② 루프까지를 이렇게 바꾼다:

```go
		t.LogEvent("item.finish", in.Project, in.SessionID, map[string]any{
			"item": in.ItemID, "mode": string(in.Outcome),
			// ★ count 는 **만들 것의 수**다. 잇기를 여기 세면 store.QueueReproduction
			//   (store/event.go:203)이 만들지도 않은 항목을 재생산율 R 의 분자로 더한다 —
			//   DESIGN §10 이 R 을 이 설계의 판정 축으로 세운 자리라 조용히 거짓이 된다.
			"count": len(plan.Create), "linked": len(plan.Link),
			"bytes": len(in.Body), // §10 "세션당 판단 바이트"
		})

		// ★ 무엇을 만들고 무엇을 이을지 **이 안에서** 확정한다. 트랜잭션 밖 분류는 참고값이고,
		//   BEGIN IMMEDIATE(store/store.go:211)가 다른 커넥션을 막는 이 안쪽이 정본이다.
		//
		// ★ **여기서는 거절하지 않는다.** 오류를 올리면 ① 의 판단이 함께 롤백된다.
		//   어긋난 것은 건너뛰고 원장에 남긴다 — 자격 거절은 tx 진입 전에 이미 끝났다.
		creates := make([]model.Item, 0, len(plan.Create))
		for _, c := range plan.Create {
			if _, err := t.GetItem(in.Project, c.Item.ID); err == nil {
				out.SkippedFollowups = append(out.SkippedFollowups, c.Item.ID)
				t.LogEvent("item.followup_skipped", in.Project, in.SessionID, map[string]any{
					"item": c.Item.ID,
					"why":  "분류 뒤 이 트랜잭션 사이에 같은 id 의 항목이 생겼다 — 판단을 지키려고 이 후속만 건너뛴다",
				})
				continue
			}
			creates = append(creates, model.Item{
				Project: in.Project, ID: c.Item.ID, Title: c.Item.Title, Body: c.Item.Body,
				Paths: c.Item.Paths, Labels: c.Item.Labels, State: model.ItemOpen,
				After: c.Item.After, CreatedAt: now,
			})
		}
		linked := make([]string, 0, len(plan.Link))
		for _, id := range plan.Link {
			it, err := t.GetItem(in.Project, id)
			// ★ 이 갈래는 **이 브랜치의 시험이 못 밟는다**(분류 뒤 남이 그 항목을 닫아야 도달한다).
			//   원장 축은 위 경합 갈래에서 잠갔고, 여기는 같은 payload 모양을 손으로 맞춘 것이다 —
			//   "item" 키를 빼면 나중에 어느 후속이 사라졌는지 못 되짚는다.
			if err != nil || it.State != model.ItemOpen {
				out.SkippedFollowups = append(out.SkippedFollowups, id)
				t.LogEvent("item.followup_skipped", in.Project, in.SessionID, map[string]any{
					"item": id,
					"why":  "이을 대상이 분류 뒤 사라졌거나 닫혔다 — 판단을 지키려고 이 후속만 건너뛴다",
				})
				continue
			}
			linked = append(linked, id)
		}

		// ① 판단 — 가장 먼저 저장한다. 이것이 원리적으로 파생 불가한 유일한 자산이다.
		//
		// 링크 = 이 항목 + 호출자가 준 링크 + **실제로 만들 것** + **실제로 이을 것**.
		// dedupeLinks 를 지나는 이유는 그 함수 주석에 있다(겹치면 판단이 통째로 사라진다).
		links := append([]model.JudgmentLink{{TargetKind: "item", TargetID: in.ItemID}}, in.Links...)
		for _, it := range creates {
			links = append(links, model.JudgmentLink{TargetKind: "item", TargetID: it.ID})
		}
		for _, id := range linked {
			links = append(links, model.JudgmentLink{TargetKind: "item", TargetID: id})
		}
		j, err := t.AddJudgment(model.Judgment{
			Project: in.Project, SessionID: in.SessionID, At: now,
			Kind: model.JudgmentHandoff, Title: in.Title, Body: in.Body, Links: dedupeLinks(links),
		})
		if err != nil {
			return err
		}
		out.Judgment = j

		// ② 후속 등록 — **새로 만들 것만** 돈다.
		//
		// ★ 중복 흡수 갈래는 남는다. 바로 위 t.GetItem 이 "없다"고 본 뒤에도 같은
		//   트랜잭션 안에서 부딪히는 길이 남아 있고(같은 호출의 중복은 tx 전에 거절하지만
		//   그 거절이 미래에 느슨해질 수 있다), 무엇보다 여기서 오류를 올리면 ① 의 판단이
		//   함께 죽는다. lane.release_skipped 갈래와 같은 성격의 최후 방어다.
		//
		// ★ 이 갈래도 **시험이 결정론적으로는 못 밟는다.** 바로 위 t.GetItem 선검사와
		//   BEGIN IMMEDIATE(store/store.go:211)의 쓰기 직렬화 때문이다. 경합 시험이
		//   전체 suite 부하에서 여기 닿을 때만 산출이 관측되고, 그 시험은 그 사실을
		//   조건부로 단정한다(갈래를 못 고르기 때문이다).
		for _, it := range creates {
			if err := t.AddItem(it); err != nil {
				var conflict *store.ConflictError
				if errors.As(err, &conflict) && conflict.Kind == store.ConflictDuplicate {
					out.SkippedFollowups = append(out.SkippedFollowups, it.ID)
					t.LogEvent("item.followup_skipped", in.Project, in.SessionID, map[string]any{
						"item": it.ID,
						"why":  "같은 id 의 항목이 이미 있다 — 판단을 지키려고 이 후속만 건너뛴다",
					})
					continue
				}
				return fmt.Errorf("후속 항목 %s 등록 실패: %w", clip(it.ID, 64), err)
			}
			out.Followups = append(out.Followups, it)
		}

		// ②' 잇기는 원장에만 남는다 — 항목을 안 만들었으므로 out.Followups 에 안 담는다.
		for _, id := range linked {
			t.LogEvent("item.followup_linked", in.Project, in.SessionID, map[string]any{
				"item": id,
				"why":  "이 세션이 이 선점 뒤에 만든 열린 항목이라 새로 만들지 않고 판단에 이었다",
			})
		}
		out.LinkedFollowups = linked
```

`finish.go:373` 은 **값을 안 바꾸고 주석만** 더한다:

```go
	// ★ added 는 len(out.Followups) 다 — **이은 항목은 안 센다.** 이미 큐에 있던 것이라
	//   순증이 0이기 때문이다. 이 줄을 out.LinkedFollowups 까지 세도록 고치면 "이 마무리가
	//   큐를 늘렸나"가 거짓이 된다.
	out.QueueBalance = s.queueBalance(ctx, in.Project, len(out.Followups), now)
```

- [ ] **Step 5: 초록을 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/service/ -race && gofmt -l . && go vet ./...`
그리고 경합 시험 단독 반복(Task 3 Step 6 과 같은 이유 — 갈래가 부하에 따라 갈린다):
`go test ./internal/service/ -run TestFinishNeverLosesAJudgmentWhenTwoSessionsRace -race -count=30`
Expected: 전부 PASS · 무출력.

- [ ] **Step 6: 커밋**

```bash
git add plugins/flightdeck/server/internal/service/
git commit -m "$(cat <<'EOF'
feat(flightdeck): finish 가 이미 있는 항목을 새로 만들지 않고 잇는다

이 선점 뒤 이 세션이 만든 열린 항목은 followups 에 id 만 실으면 이어진다.
항목의 제목·본문은 안 덮는다 — store 에 그럴 메서드가 없고, 있어도 안 덮을
것이다(다른 세션이 그 본문을 근거로 계획을 세운다).

1. 무엇을 만들고 무엇을 이을지 트랜잭션 안에서 확정한 뒤 그 결과로만 판단
   링크를 짠다. 어긋난 것은 건너뛴다 — 그 자리에서 거절하면 판단이 죽는다.
2. LinkedFollowups 로 "만들었다"와 "이었다"를 가른다.
3. 원장 item.finish 의 count 는 만든 것, linked 는 이은 것이다. 이 값이
   재생산율 R 의 분자라 섞으면 만들지 않은 항목이 R 을 부풀린다.
4. 큐 수지 added 는 그대로 만든 것만 센다 — 근거를 그 줄에 적었다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: 거짓말하는 안내 문구를 사실로 바꾼다

**Files:**
- Modify: `plugins/flightdeck/server/internal/service/finish_followups.go:29-37` (`FollowupsGuidance`)
- Modify: `plugins/flightdeck/server/internal/service/finish_followups_test.go:38` (새 문장을 잠그는 단정 추가)

**Interfaces:** 없음(상수 본문만 바뀐다).

- [ ] **Step 1: 빨강 시험을 먼저 만든다**

`TestFinishStopsOnceWhenFollowupsFellOnTheFloor`(`finish_followups_test.go:55`)의 문구 단정 목록을 바꾼다:

```go
	msg := err.Error()
	// ★ 문구를 **문장 단위로** 잠근다. 지금까지 "spun-off-axis"·"followups" 두 조각만 봤는데,
	//   그 사이 안내 본문이 거짓이 되어도(실제로 그랬다 — "지금 followups 로 옮길 수 없다")
	//   시험은 조용히 초록이었다.
	for _, want := range []string{"spun-off-axis", "followups", "id 만"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("거절 사유에 %q 가 없다:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "옮길 수 없다") {
		t.Fatalf("안내가 아직 '옮길 수 없다'고 말한다 — 이제 이어진다:\n%s", msg)
	}
	// ★ 이 브랜치의 존재 이유가 "judgment_link.target_id 에 REFERENCES 가 없다"(schema.sql:265)
	//   인데, 같은 응답이 "FK 로 이어진다"고 말하면 우리가 없앤 거짓말 하나를 우리가 되살린다.
	if strings.Contains(msg, "FK") {
		t.Fatalf("안내가 아직 'FK 로 이어진다'고 말한다 — judgment_link.target_id 에는 REFERENCES 가 없다(schema.sql:265):\n%s", msg)
	}
```

- [ ] **Step 2: 빨강을 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/service/ -run TestFinishStopsOnce -v`
Expected: FAIL — `"id 만"` 이 없고 `"옮길 수 없다"` 와 `"FK"` 가 있다.

- [ ] **Step 3: `FollowupsGuidance` 의 ★문단을 사실로 바꾼다**

```go
const FollowupsGuidance = `후속은 followups 로 **같은 호출에** 넣어라 — 그래야 판단과 후속이 판단 링크로 이어지고,
다음 세션의 pick 이 그 항목과 함께 "왜 이것이 생겼나"를 낸다.

★ 위에 이름이 나온 항목들은 **이미 만들어져 있으니 followups 에 id 만 넣어라** — 새로 만들지
않고 이 판단에 잇는다. 제목·본문은 다시 안 적는다(그 항목의 것을 안 덮는다).
이을 수 있는 것은 **이 선점 뒤에 이 세션이 add 로 만든, 아직 열린 항목**뿐이다 —
그 밖의 id 를 실으면 거절한다(오타로 남의 항목이 이 판단에 붙는 것을 막는 유일한 자리다).
이번 작업의 후속이 아닌 항목은 그대로 두고, 판단만 걸려면 note(kind='handoff', item_id=…) 를 쓴다.

이 관문은 **한 번만** 막는다. 그 항목들이 이 작업의 후속이 아니라면 그대로 다시 불러라.`
```

**첫 줄의 "FK 로" 를 "판단 링크로" 로 함께 낮춘다.** `judgment_link.target_id` 에는 `REFERENCES` 가 없고(`schema.sql:265` 가 "끊긴 포인터가 원리적으로 사라진다"는 이유로 일부러 안 걸었다), **그 부재가 이 항목 결함 ②의 물리적 원인**이다. 이 문단을 어차피 다시 쓰므로 여기서 함께 고친다 — 줄 수 불변이고, `grep -rn "FK 로 이어" plugins/flightdeck/server` 로 확인한 결과 **이 문자열을 단정하는 시험은 하나도 없다**(`finish_test.go:207`·`judge/bundle_test.go:432` 는 주석이다). 그래서 Step 1 의 새 단정이 그 자리를 처음으로 잠근다.

- [ ] **Step 4: 초록을 확인하고, MCP 이음매 시험도 함께 본다**

Run: `cd plugins/flightdeck/server && go test ./internal/service/ ./cmd/fd/ -run 'TestFinish|TestMCPRefusal' -v`
Expected: PASS. `cmd/fd/mcp_seam_test.go:393` 은 `HandoffGuidance`(body 누락) 축이라 이 변경과 무관해야 한다 — 빨가면 문구가 그쪽까지 샌 것이니 원문을 읽어라.

- [ ] **Step 5: 커밋**

```bash
git add plugins/flightdeck/server/internal/service/
git commit -m "$(cat <<'EOF'
fix(flightdeck): 후속 안내가 "지금은 못 옮긴다"고 거짓말하고 있었다

이 문단이 세션에게 note 우회를 권했고, 실제로 그 우회를 쓴 세션이 있다.
그런데 링크는 이미 걸리고 있었다 — 우회로 만든 것은 중복 판단이었다.

문구를 사실로 바꾸고, 그 문장을 잠그는 단정을 시험에 넣었다. 지금까지는
조각 둘만 봐서 본문이 거짓이 되어도 조용히 초록이었다.

같은 문단의 "FK 로 이어지고" 도 "판단 링크로 이어지고" 로 낮췄다.
judgment_link.target_id 에는 REFERENCES 가 없고, 그 부재가 이 항목이 고치는
결함의 물리적 원인이다 — 표면이 반대로 말하면 없앤 거짓말을 되살린다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: 표면 — 도구 스키마와 응답 화면

**Files:**
- Modify: `plugins/flightdeck/server/internal/mcpsrv/tools.go:56-69` (`followupSchema` 만)
- Modify: `plugins/flightdeck/server/internal/mcpsrv/render.go:1338-1353` (`RenderFinish` 안)
- Modify: `plugins/flightdeck/server/internal/mcpsrv/render_test.go:1219-1234` (`TestRenderFinishSaysWhichFollowupsWereSkipped` — 머리 주석과 단정을 함께) · 새 시험 둘 추가

**Interfaces:**
- Consumes: `service.FinishResult.LinkedFollowups`(Task 4).

**★ 겹침 주의:** `render.go` 는 다른 세션이 `RenderSessionStart` 부근을 쥐고 있다(그 세션의 확정 앵커: "순삽입 14줄·삭제 0, 기존 함수 본문 무변경"). 내 헝크는 `RenderFinish`(1330~1387) **안에만** 있어야 한다.

- [ ] **Step 1: 빨강 렌더 시험 둘을 더하고, 낡은 시험 하나를 고친다**

`render_test.go` 에 더한다:

```go
// TestRenderFinishSaysWhatItLinkedInsteadOfCreated 는 "만들었다"와 "이었다"를 화면에서 가른다.
//
// 같은 줄에 담으면 응답이 "후속 2건 등록"이라고 말하는데 큐에는 1건만 는다 — 세션이
// 자기가 큐에 무엇을 했는지 못 본다(그 축이 finishBalanceLines 의 존재 이유다).
//
// ★ 단정은 **문장을 통째로** 한다. id 만 찾으면(`"spun-off-axis"` 가 어딘가 있는가) 두 줄을
//   합쳐 "후속 2건 등록: brand-new, spun-off-axis" 를 찍어도 초록이다 — 이 주석이 막겠다고
//   적은 바로 그 거짓말이 통과한다(사보타주 실측으로 확인했다). 그리고 맨 `"이었다"` 는 꼬리줄
//   "…한 트랜잭션이었다"(render.go:1383)에 걸려 잇기 줄이 아예 없어도 참이니 쓰지 않는다.
//
// ★ `"안 썼다"` 가 이 화면의 **정직성 관문**이다. 오늘까지 followupSchema 가 id·title·body 를
//   셋 다 필수로 받았으므로(tools.go:67) 돌고 있는 세션은 예외 없이 셋을 다 싣는다.
//   잇기 갈래는 그 title·body 를 읽지도 저장하지도 않는다 — followupPlan.Link 가 []string 이고,
//   store 에 항목 본문을 고치는 메서드가 아예 없다(store/item.go 전수). 게다가 이 변경
//   **전에는** 같은 입력이 "후속 N건은 안 넣었다"로 시끄럽게 나왔다(render.go:1349).
//   화면이 여기서 침묵하면 그 신호가 조용해지는 쪽으로 퇴행하고, 세션은 자기가 적어 보낸
//   본문이 어딘가 반영됐다고 믿고 떠난다 — 설계 §3 이 이름 붙인 "조용한 거짓"이다.
func TestRenderFinishSaysWhatItLinkedInsteadOfCreated(t *testing.T) {
	out := RenderFinish(service.FinishResult{
		Item:            model.Item{ID: "batch7", State: model.ItemDone},
		Judgment:        model.Judgment{ID: "j1", Kind: model.JudgmentHandoff, Body: "본문"},
		Followups:       []model.Item{{ID: "brand-new"}},
		LinkedFollowups: []string{"spun-off-axis"},
	})
	for _, want := range []string{
		"후속 1건 등록: brand-new",
		"기존 항목 1건을 후속으로 **이었다**: spun-off-axis",
		"안 썼다",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("화면에 %q 가 없다:\n%s", want, out)
		}
	}
	// 합쳐진 줄은 개수가 거짓이 된다 — 그것이 이 시험이 잠그는 회귀다.
	for _, banned := range []string{"후속 2건 등록", "후속 0건"} {
		if strings.Contains(out, banned) {
			t.Fatalf("화면에 %q 가 떴다:\n%s", banned, out)
		}
	}
	// ★ 이 브랜치의 존재 이유가 "judgment_link.target_id 에 FK 가 없다"이다(schema.sql:265).
	//   같은 화면이 "FK 로 이어졌다"고 말하면 우리가 없앤 거짓말 하나를 우리가 되살린다.
	if strings.Contains(out, "FK") {
		t.Fatalf("화면이 아직 'FK 로 이어졌다'고 말한다 — judgment_link.target_id 에는 FK 가 없다(schema.sql:265):\n%s", out)
	}
}

// TestRenderFinishSaysZeroOnlyWhenNothingWasCreatedOrLinked 는 0건 문구의 조건을 잠근다.
//
// len(Followups)==0 만 보면 잇기만 한 마무리에서 "지금 add 로 넣어라"가 떠서 방금 이은 것을
// 부정한다.
func TestRenderFinishSaysZeroOnlyWhenNothingWasCreatedOrLinked(t *testing.T) {
	linkedOnly := RenderFinish(service.FinishResult{
		Item:            model.Item{ID: "batch7", State: model.ItemDone},
		Judgment:        model.Judgment{ID: "j1", Kind: model.JudgmentHandoff, Body: "본문"},
		LinkedFollowups: []string{"spun-off-axis"},
	})
	if strings.Contains(linkedOnly, "후속 0건") {
		t.Fatalf("잇기만 한 마무리에 '후속 0건' 이 떴다:\n%s", linkedOnly)
	}
	// 0건 줄이 사라진 것만으로는 부족하다 — 잇기를 "등록"으로 찍어도 그 단정은 참이다.
	if strings.Contains(linkedOnly, "건 등록") {
		t.Fatalf("잇기만 했는데 '등록' 이 떴다(만든 적 없다):\n%s", linkedOnly)
	}
	if !strings.Contains(linkedOnly, "기존 항목 1건을 후속으로") {
		t.Fatalf("잇기 줄이 없다:\n%s", linkedOnly)
	}
	none := RenderFinish(service.FinishResult{
		Item:     model.Item{ID: "batch7", State: model.ItemDone},
		Judgment: model.Judgment{ID: "j1", Kind: model.JudgmentHandoff, Body: "본문"},
	})
	if !strings.Contains(none, "후속 0건") {
		t.Fatalf("정말 0건인데 그 줄이 없다:\n%s", none)
	}
}
```

그리고 `TestRenderFinishSaysWhichFollowupsWereSkipped`(`render_test.go:1219-1234`)를 고친다 — 건너뜀 사유가 이제 둘(만들 대상이 그 사이 생겼다 · 이을 대상이 사라졌다)이라 `"이미 있"` 으로 단정할 수 없다. **머리 주석과 단정을 함께** 고쳐야 한다: 주석은 한쪽 사유를 단정문으로 서술하고 있고, 단정은 그 사유 문자열에 매달려 있다.

**★ 단정을 바꾸되 잃지 않는다.** 사유는 첫 줄에서 **둘째 줄(처방)로 옮겨 간다.** 이름과 `"안 넣었다"` 만 단정하면 둘 다 첫 줄에 있어, **처방 줄을 통째로 지워도 초록**이다. 그러면 이 축에 남는 것이 이름 하나뿐이고, `item.followup_skipped` 의 `why` 를 읽는 시험은 이 계획에 없다(경합 시험은 건수와 좌표만 조건부로 센다). 그래서 처방 줄의 고정 조각 `"사유는 원장에 있다"` 를 단정에 **더한다** — 이 조각은 사유가 몇 개로 갈리든 안 늙는다.

```go
// 건너뛴 후속은 **화면에 나온다.** 응답 구조체에만 있으면 세션은 못 본다.
//
// ★ 이 줄이 없으면 finish 의 흡수가 조용한 거짓이 된다 — "후속 1건 등록"만 보고
// 세션이 떠나는데, 실제로 그 id 로는 아무것도 안 들어갔다.
//
// ★ 사유를 화면 첫 줄에 박지 않는 이유: 건너뜀 갈래가 둘이다(만들 대상이 tx 사이에 생겼다 ·
// 이을 대상이 tx 사이에 사라졌다). 한쪽을 첫 줄에 박으면 다른 쪽에서 거짓이 된다.
func TestRenderFinishSaysWhichFollowupsWereSkipped(t *testing.T) {
	out := RenderFinish(service.FinishResult{
		Item:             model.Item{ID: "batch7", State: model.ItemDone},
		Judgment:         model.Judgment{ID: "j1", Kind: model.JudgmentHandoff, Body: "본문"},
		Followups:        []model.Item{{ID: "batch8"}},
		SkippedFollowups: []string{"taken-id"},
	})
	// ★ 셋을 다 단정한다. 이름과 "안 넣었다"는 **첫 줄 하나에** 있어서, 둘만 보면
	//   사유를 실은 둘째 줄이 통째로 사라져도 초록이다 — 그 사유를 잠그는 다른 시험이
	//   화면에 없으므로, 그 순간 이 축에 남는 것은 이름 하나뿐이 된다.
	//   "사유는 원장에 있다"는 갈래가 몇 개로 늘든 안 늙는 조각이라 여기 고정한다.
	for _, want := range []string{"taken-id", "안 넣었다", "사유는 원장에 있다"} {
		if !strings.Contains(out, want) {
			t.Fatalf("건너뛴 후속 화면에 %q 가 없다 — 세션은 안 들어간 것을 들어간 줄 알거나, "+
				"왜 안 들어갔는지 물을 자리를 못 찾는다:\n%s", want, out)
		}
	}
}
```

- [ ] **Step 2: 빨강을 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/mcpsrv/ -run TestRenderFinish -v`
Expected: **셋 다 FAIL.**
- 새 시험 둘은 잇기 줄이 없어 죽는다(`"기존 항목 1건을 후속으로 …"` 부재).
- `…WereSkipped` **도 FAIL 이어야 한다** — `"안 넣었다"` 는 현재 문구(`render.go:1350`)에 이미 있지만 `"사유는 원장에 있다"` 는 Step 3 이 넣는 줄이다. 여기서 그것이 초록이면 단정을 잘못 옮긴 것이다.

- [ ] **Step 3: `RenderFinish` 를 고친다**

`render.go:1338-1353` 을 이렇게 바꾼다:

```go
	// ★ "FK 로" 라고 쓰지 않는다. judgment_link.target_id 에는 REFERENCES 가 없고
	//   (schema.sql:265), **그 부재가 이 변경의 존재 이유**다 — 표면이 반대로 말하면
	//   이 브랜치가 없앤 거짓말 하나를 같은 응답에서 되살린다.
	if len(r.Followups) > 0 {
		ids := make([]string, 0, len(r.Followups))
		for _, f := range r.Followups {
			ids = append(ids, f.ID)
		}
		fmt.Fprintf(&b, "후속 %d건 등록: %s (판단에 이어졌다)\n", len(ids), strings.Join(ids, ", "))
	}
	// ★ 이은 것은 **따로** 낸다. 등록과 한 줄에 담으면 "후속 2건 등록"이라고 말하는데
	//   큐에는 1건만 늘어, 세션이 자기가 큐에 무엇을 했는지 못 본다.
	if len(r.LinkedFollowups) > 0 {
		fmt.Fprintf(&b, "기존 항목 %d건을 후속으로 **이었다**: %s (새로 만들지 않았다)\n",
			len(r.LinkedFollowups), strings.Join(r.LinkedFollowups, ", "))
		// ★ 실어 보낸 제목·본문을 **버렸다고 말한다.** 여기서 침묵하면 "적게 하고 서버가
		//   버린다"가 된다 — 설계 §3 이 이 변경으로 없애기로 한 바로 그 모양이다.
		//   오늘까지 스키마가 title·body 를 필수로 받아 왔으므로(tools.go) 세션은
		//   관성으로 싣고, 잇기는 그것을 안 읽는다(store 에 항목 본문 갱신이 없다).
		//   그리고 이 변경 전에는 같은 입력이 "안 넣었다"로 시끄럽게 나왔다 —
		//   여기서 침묵하면 신호가 조용해지는 쪽으로 퇴행한다.
		b.WriteString("  함께 실어 보낸 제목·본문은 **안 썼다** — 그 항목의 것을 안 덮는다" +
			"(다른 세션이 그 본문을 근거로 계획을 세운다). 내용이 다르면 다른 id 로 add 해라.\n")
	}
	// ★ 0건 문구는 **등록과 잇기가 둘 다 0**일 때만 뜬다. 등록만 보면 잇기만 한 마무리에서
	//   "지금 add 로 넣어라"가 떠서 방금 이은 것을 부정한다.
	if len(r.Followups) == 0 && len(r.LinkedFollowups) == 0 {
		b.WriteString("후속 0건 — 이번에 나온 후속이 정말 없다면 그대로 두고, 있다면 지금 add 로 넣어라.\n")
	}
	// ★ 건너뛴 후속은 **반드시 낸다.** 사유는 둘로 갈렸다(만들 대상이 그 사이 생겼다 ·
	//   이을 대상이 사라졌다) — 화면은 무엇이 안 들어갔는지를 이름으로 말하고,
	//   왜인지는 원장(item.followup_skipped)이 갖는다. 한쪽 사유를 화면에 박으면
	//   다른 쪽에서 거짓이 된다.
	if len(r.SkippedFollowups) > 0 {
		fmt.Fprintf(&b, "후속 %d건은 **안 넣었다**: %s\n",
			len(r.SkippedFollowups), strings.Join(r.SkippedFollowups, ", "))
		b.WriteString("  그 사이 그 id 의 항목이 생겼거나, 이을 대상이 사라졌다 — 사유는 원장에 있다.\n")
	}
```

- [ ] **Step 4: `followupSchema` 의 필수를 낮춘다**

`tools.go:56-69`:

```go
func followupSchema() map[string]any {
	return map[string]any{
		"type":        "array",
		"description": "이번에 나온 후속. 같은 호출에 넣으면 판단에 이어진다",
		"items": obj(map[string]any{
			"id":     str("항목 id — 브랜치 이름이 된다. **이미 있는 id 면 만들지 않고 잇는다**(없는 id 면 새로 만드니 제목·본문이 필요하다)"),
			"title":  str("한 줄 제목. 새로 만들 때만 쓴다 — 이미 있는 id 면 안 읽는다"),
			"body":   str("무엇을 해야 하는가. 새로 만들 때만 쓴다 — 이미 있는 id 면 안 읽는다"),
			"paths":  strArr("이 항목이 건드릴 경로"),
			"labels": strArr("표시 전용 꼬리표"),
			"after":  afterSchema(),
		}, "id"),
	}
}
```

배열 `description` 의 "FK 로 이어진다" 도 함께 걷는다 — Task 5 의 `FollowupsGuidance` 와 같은 이유다(`judgment_link.target_id` 에 `REFERENCES` 가 없고, 그 부재가 이 변경의 존재 이유다). `title`·`body` 는 **"필수가 아니다"가 아니라 "안 읽는다"** 로 적는다. 낱말 둘 차이지만 "필수가 아니다"를 세션은 "실으면 뭔가 되겠지"로 읽는다.

**자격 산문을 여기 넣지 않는다.** DESIGN §6 이 "규율 산문을 도구 설명에 넣지 않는다"고 못박았고, 자격은 거절 응답과 `FollowupsGuidance` 가 그 자리에서 낸다. 중첩 스키마 설명은 어떤 시험도 길이를 안 재므로 여기서 예산을 스스로 지켜야 한다.

**required 를 낮춰도 관문은 안 죽는다.** 이 완화 뒤 "새 후속에 제목·본문 필수"를 지키는 것은 `finish.go` 의 `plan.Create` 루프 하나뿐이다. 그 자리를 `TestFinishStillRequiresTitleAndBodyForANewFollowup`(Task 3 Step 1)이 잡는다 — 스키마 `required` 를 단정하는 시험은 저장소에 없으므로, 이 완화의 안전망은 그 시험 하나다. Step 5 에서 그 시험이 초록인지 반드시 눈으로 확인해라.

- [ ] **Step 5: 초록을 확인한다**

Run: `cd plugins/flightdeck/server && go test ./... && gofmt -l . && go vet ./...`
Expected: 전부 PASS · 무출력. 여기서 `cmd/fd` 시험이 깨지면 스키마 필수 완화가 어딘가 단정에 걸린 것이다 — 그 자리를 읽고 근거를 적어 고쳐라.

- [ ] **Step 6: 커밋**

```bash
git add plugins/flightdeck/server/internal/mcpsrv/
git commit -m "$(cat <<'EOF'
feat(flightdeck): 마무리 응답이 "만들었다"와 "이었다"를 가른다

1. 잇기 줄을 따로 낸다 — 등록과 한 줄에 담으면 "후속 2건 등록" 인데 큐는
   1건만 늘어 세션이 자기가 큐에 무엇을 했는지 못 본다.
2. "후속 0건" 은 등록과 잇기가 둘 다 0일 때만 뜬다.
3. 건너뜀 문구를 중립으로 바꿨다. 사유가 둘로 갈렸고(그 사이 생겼다 ·
   이을 대상이 사라졌다) 한쪽을 화면에 박으면 다른 쪽에서 거짓이 된다.
   §4.3 이 시킨 것은 아니지만 §5 표의 "잇기 대상이 tx 사이에 사라짐" 행이
   만든 갈래라 따라간 정정이다. 사유는 첫 줄에서 처방 줄로 내려갔으므로
   시험 단정도 그 줄의 고정 조각까지 함께 잡는다 — 안 그러면 처방이
   통째로 사라져도 초록이다.
4. followups 의 필수를 id 하나로 낮추고, 잇기에 실려 온 title·body 는 안
   읽는다는 사실을 응답과 스키마가 함께 말한다. 오늘까지 셋이 다 필수였으니
   세션은 관성으로 싣는데, 그것을 조용히 버리면 이 설계가 없애기로 한 "적게
   하고 서버가 버린다"를 우리가 다시 만드는 것이다. 거절은 기각했다 — 캐시된
   스킬을 쥔 기존 세션 전부를 마무리 시점에 하드 실패로 만드는 값이 크다.
5. "FK 로 이어졌다/이어진다" 를 화면과 스키마에서 걷었다. judgment_link 의
   target_id 에는 REFERENCES 가 없고, 그 부재가 이 변경의 존재 이유다.

자격 산문은 스키마에 안 넣었다 — DESIGN §6 이고, 그 설명은 어떤 시험도
길이를 안 재기 때문에 여기서 스스로 지켜야 한다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 7: 같은 계약을 적는 문서 둘

**Files:**
- Modify: `plugins/flightdeck/skills/fd-handoff/SKILL.md:19` (**줄 대체 — 순증 0**)
- Modify: `plugins/flightdeck/skills/fd-handoff/SKILL.md:37` (**줄 대체 — 순증 0**)
- Modify: `plugins/flightdeck/DESIGN.md:431` (**한 줄만**)

**★ 줄 수 상한:** `fd-handoff/SKILL.md` 는 **60줄 미만**이 관문이다(`cmd/fd/plugin_test.go` 의 `skillLineCaps`, 검사는 `lines >= limit` 이면 실패). 실측 **58줄**이라 여유가 1줄뿐이다 — 아래 둘 다 **대체**라 순증 0이다.
**★ 겹침 주의:** `DESIGN.md` 를 지금 여러 세션이 만진다(§2 108~151 · §6 315~340 · §6 493 · §7·§9). **431행 한 줄만** 만진다.

- [ ] **Step 1: 지금 줄 수를 확인한다**

Run: `wc -l plugins/flightdeck/skills/fd-handoff/SKILL.md`
Expected: 58(±1). 60 이상이면 이미 관문 밖이니 멈추고 보고해라.

- [ ] **Step 2: `SKILL.md:19` 를 대체한다(줄 수 불변)**

바꾸기 전:
```
  followups: [ { id, title, body, paths } ]   // 이번에 나온 후속
```
바꾼 뒤:
```
  followups: [ { id, title, body, paths } ]   // 새 후속. **이미 있는 항목은 id 만** — 잇는다
```

- [ ] **Step 2': `SKILL.md:37` 을 대체한다(줄 수 불변)**

바꾸기 전:
```
`followups` 로 넣으면 판단과 후속이 FK 로 이어진다. 나중에 따로 넣으면 그 연결이 없다.
```
바꾼 뒤:
```
`followups` 로 넣으면 판단과 후속이 판단 링크로 이어진다. 나중에 따로 넣으면 그 연결이 없다.
```

`judgment_link.target_id` 에는 `REFERENCES` 가 없고(`schema.sql:265`) **그 부재가 이 항목이 고치는 결함의 물리적 원인**이다. Task 5(`FollowupsGuidance`) · Task 6(`RenderFinish`·`followupSchema`)과 같은 정정이라 같은 브랜치에서 함께 걷는다. 이 문자열을 단정하는 시험은 없다(`grep -rn "FK 로 이어" plugins/flightdeck/server` 로 확인 — 걸리는 것은 프로덕션 셋뿐이다).

- [ ] **Step 3: `DESIGN.md:431` 을 대체한다(줄 수 불변)**

바꾸기 전:
```
| `finish` | `item_id`, `outcome`, `body`, `followups?[]` | **한 호출이 판단 저장 + 후속 등록 + 종료 + 워크스페이스 해제를 원자적으로** |
```
바꾼 뒤:
```
| `finish` | `item_id`, `outcome`, `body`, `followups?[]` | **한 호출이 판단 저장 + 후속 등록(이미 있는 id 면 잇기) + 종료 + 워크스페이스 해제를 원자적으로** |
```

`README.md:96` 은 `followups` 를 창설로 규정하지 않으므로 **안 고친다**. §7(650~651행)의 같은 문구도 다른 세션이 쥐고 있어 **안 고친다** — 그 사실을 마무리 판단문의 "일부러 안 한 것"에 적는다.

- [ ] **Step 4: 관문 둘을 확인한다**

Run: `cd plugins/flightdeck/server && go test ./cmd/fd/ -run TestSkills -v && git -C ../../.. diff --stat plugins/flightdeck/DESIGN.md`
Expected: 시험 PASS · `DESIGN.md` 가 `1 file changed, 1 insertion(+), 1 deletion(-)`. 더 크면 되돌리고 431행만 다시 고쳐라.

- [ ] **Step 5: 커밋**

```bash
git add plugins/flightdeck/skills/fd-handoff/SKILL.md plugins/flightdeck/DESIGN.md
git commit -m "$(cat <<'EOF'
docs(flightdeck): followups 가 잇기도 한다는 사실을 계약과 스킬에 적는다

전부 줄 대체다. SKILL.md 는 58줄이고 상한이 60줄 미만이라 순증 0으로 쓴다.
DESIGN.md 는 지금 여러 세션이 만지고 있어 431행 한 줄만 건드린다.

SKILL.md:37 의 "FK 로 이어진다" 도 "판단 링크로 이어진다" 로 낮췄다 —
judgment_link.target_id 에는 REFERENCES 가 없고 그 부재가 이 항목이 고치는
결함의 원인이다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 8: 관문 전수와 브랜치 충돌 확인

**Files:** 없음(검증만). 단 Step 3' 은 조건이 맞으면 `finish.go:30` 한 줄을 고친다.

- [ ] **Step 1: 관문 합본**

Run: `cd plugins/flightdeck/server && go build ./... && go vet ./... && gofmt -l . && go test ./... -race -count=1`
그리고 경합 시험 단독 반복: `go test ./internal/service/ -run TestFinishNeverLosesAJudgmentWhenTwoSessionsRace -race -count=30`
Expected: `gofmt` **무출력** · 나머지 전부 초록.

**스펙 §6 시험 5건의 대응 자리(여기서 눈으로 확인한다):**

| 스펙 §6 | 시험 | 자리 |
|---|---|---|
| 1 회귀(부적격 id 에 링크 안 걸림) | `TestFinishRefusesAFollowupThatBelongsToSomeoneElse` | Task 3 Step 1 |
| 2 잇기 성공 | `TestFinishLinksAnExistingItemInsteadOfCreatingIt` | Task 4 Step 1 |
| 3 부적격 셋 | `…ThatBelongsToSomeoneElse` · `…ThatIsAlreadyClosed` · `…MadeBeforeTheClaim` | Task 3 Step 1 |
| 4 새 항목은 여전히 `title`·`body` 필수 | `TestFinishStillRequiresTitleAndBodyForANewFollowup` | Task 3 Step 1 |
| 5 렌더 | `TestRenderFinishSaysWhatItLinkedInsteadOfCreated` · `…SaysZeroOnlyWhenNothingWasCreatedOrLinked` | Task 6 Step 1 |

하나라도 이름이 안 맞으면 그 축이 무시험이다 — 멈추고 채워라.

- [ ] **Step 2: 교차 관문 — `go build` 가 아니라 `go vet` 이다**

Run:
```bash
cd plugins/flightdeck/server
GOOS=darwin  GOARCH=arm64 go vet ./...
GOOS=windows GOARCH=amd64 go vet ./...
```
Expected: 둘 다 무출력. **`go build` 는 `_test.go` 를 건너뛰므로 관문이 아니다.**

- [ ] **Step 3: 살아 있는 브랜치와 충돌 확인**

Run(저장소 루트 `/home/aaron/cdo-dev/kweiza-cc-plugins` 에서):
```bash
for b in $(git branch --format='%(refname:short)' | grep -v '^main$'); do
  git merge-tree $(git merge-base main "$b") main "$b" >/dev/null 2>&1 \
    && echo "무충돌 $b" || echo "★충돌 $b"
done
```
Expected: 이 브랜치와 다른 브랜치 사이에 `★충돌` 이 없다. 나오면 **그 세션에 `note(kind='ask')` 로 정확한 자리를 알린 뒤** 해소한다. `finish.go`·`render.go`·`DESIGN.md` 가 특히 위험하다.

- [ ] **Step 3': "FK 로 이어진다"의 마지막 자리 — 조건이 맞으면 걷는다**

`finish.go:30` `HandoffGuidance` 마지막 줄도 이렇게 말한다:

```
후속이 있으면 followups 로 같은 호출에 넣어라 — 그러면 판단과 후속이 FK 로 이어진다.
```

이 파일은 넷이 쥐고 있으나 그 자리는 이 계획의 세 헝크(152-184 · 189-250 · 373)와 **줄이 안 겹치는 상수 블록**이고, 이 문자열을 단정하는 시험도 없다(`cmd/fd/mcp_seam_test.go:393` 은 `HandoffGuidance` 의 **body 누락** 축이라 이 문장을 안 본다).

- Step 3 의 `merge-tree` 가 `finish.go:20-40` 을 만지는 브랜치를 **안 내면**: `FK 로 이어진다` → `판단 링크로 이어진다` 로 대체하고(줄 수 불변), `go test ./... && gofmt -l .` 을 다시 돌린 뒤 아래 한 줄짜리 커밋을 낸다.

```bash
git add plugins/flightdeck/server/internal/service/finish.go
git commit -m "$(cat <<'EOF'
fix(flightdeck): 핸드오프 안내의 "FK 로 이어진다" 를 사실로 낮춘다

judgment_link.target_id 에는 REFERENCES 가 없다(schema.sql:265 이 "끊긴
포인터가 원리적으로 사라진다"는 이유로 일부러 안 걸었다). 그 부재가 이 항목이
고치는 결함의 물리적 원인인데, 같은 응답이 반대로 말하고 있었다.

render.go · FollowupsGuidance · followupSchema · SKILL.md 와 같은 정정의
마지막 자리다. 남은 것은 DESIGN.md 두 줄뿐이고 후속으로 낸다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

- 충돌이 나오면 **안 고치고** 그 사실을 마무리 판단문의 "일부러 안 한 것"에 적는다.

- [ ] **Step 4: 결과를 판단으로 남긴다(실패해도 남긴다)**

관문 출력이 하나라도 빨가면 `note(kind: "blocked", item_id: "fd-finish-cannot-link-an-existing-item-as-followup")` 로 무엇이 왜 막혔는지 남기고 멈춰라.

---

### Task 9: 실물로 돌려 보고 마무리한다

**Files:** 없음(검증과 마무리).

- [ ] **Step 1: 잇기를 실물로 한 번 돌린다**

이 워크트리의 서버를 띄우지 말고(돌고 있는 MCP 서버는 **정식 설치본**이라 이 변경이 없다) 시험으로 재현한 것을 그대로 인정한다. 다만 CLI 로 렌더가 깨지지 않는지는 본다:

Run: `cd plugins/flightdeck/server && go run ./cmd/fd status`
Expected: 평소 보드가 나온다(렌더 변경이 다른 화면을 안 깼다).

- [ ] **Step 2: 무엇을 못 봤는지 적는다**

`RenderFinish` 의 잇기 줄은 **클라이언트(`fd`)가 갱신돼야** 실제 세션 화면에 뜬다 — 지금 돌고 있는 정식 설치본은 옛 판이다. 이것은 결함이 아니라 이 변경의 도달 조건이다.

- [ ] **Step 3: 검증 판단을 남긴다**

```
note(kind: "verified", item_id: "fd-finish-cannot-link-an-existing-item-as-followup", body: "…")
```
본문에 최소한 넷:

① 관문 전수 결과(명령과 실제 출력)
② 시험이 실제로 빨갛다가 초록이 된 자리 목록 — **각각 어떤 문구로 빨갰는지까지** 적는다(같은 시험이 "관문이 없다"로 빨간 것과 "좌표가 아직 없다"로 빨간 것은 다른 사실이다). 스펙 §6 대응표(Task 8 Step 1)를 함께 옮겨 적는다.
③ **안 본 축** — 실물 MCP 왕복은 안 돌렸다(설치본이 옛 판이다). 그리고 tx 안에서 `item.followup_skipped` 를 내는 자리는 **셋**인데(분류 뒤 같은 id 가 생겼다 · 이을 대상이 사라졌다 · `AddItem` 중복 흡수) 시험이 밟는 것은 부하에 따라 그중 하나뿐이고, 그것도 **결정론적이 아니다**. 원장 단정(건수 + payload 의 `item` 좌표)은 그 하나에 **조건부로** 걸었고 나머지 둘은 시험 없이 남는다.
④ merge-tree 결과와 Step 3' 의 판정(`HandoffGuidance` 를 고쳤나 아닌가).

- [ ] **Step 4: 항목을 끝낸다**

```
finish(item_id: "fd-finish-cannot-link-an-existing-item-as-followup", outcome: "done", body: "…", followups: […])
```

`body` 에 반드시 넣을 것:
- **왜 그렇게 했나** — 항목 본문의 전제가 실측으로 깨졌다는 사실(링크는 이미 걸리고 있었다)과, 그래서 범위가 "잇는 길 만들기"에서 "거짓 문구 걷어내기 + 자격 검사 넣기"로 바뀐 것.
- **무엇을 기각했나** — `link_followups` 별도 인자 · ②를 ① 앞으로 옮기기 · 부분 성공 유지 · 스키마 설명에 자격 산문 넣기 · 아래 둘.
  - **적격 id 에 `title`·`body` 를 실었을 때 거절하기** — 오늘까지 스키마가 셋을 다 필수로 받아 왔으므로(`tools.go:67` · `finish.go:166`) 캐시된 스킬을 쥔 기존 세션 전부가 그 모양으로 부른다. 마무리 시점의 하드 실패로 갚기엔 비싸다. 대신 응답과 스키마가 "그 둘은 안 읽었다"를 말하고, 렌더 시험이 그 문장을 잠근다.
  - **경합 시험이 흡수 갈래를 결정론적으로 밟게 만들기**(`st.Tx` 핸드셰이크 또는 `afterClassify` 시험 후크) — 그 구성은 이 브랜치가 안 만든 새 이음매를 프로덕션에 넣거나, 시험이 갈래를 골라 계약을 실제보다 좁게 말하게 한다. 대신 갈래를 안 고르고 **끝의 성질**을 단정했고, 흡수의 산출은 **조건부로** 잠갔다.
- **일부러 안 한 것** — `DESIGN.md` §7(650~651)의 같은 문구(다른 세션이 쥐고 있다) · `README.md:96` · `finish` 후속에 `item.add` 를 안 남긴 것 · 그리고 FK 문구의 남은 자리.
  - **코드 표면의 "FK 로 이어졌다/이어진다" 문구는 이번에 걷었다**(`render.go` · `FollowupsGuidance` · `followupSchema` · `SKILL.md:37`, 그리고 충돌이 없으면 `HandoffGuidance`). 남은 자리는 `DESIGN.md:295`·`:383` **둘뿐**이고, 그것은 처음부터 부정확했으며 지금 여러 세션이 그 파일을 쥐고 있어 후속 `fd-design-says-judgment-link-is-an-fk` 로 넘긴다.
- **확인했으나 못 한 것** — 실물 MCP 왕복 · tx 안 건너뜀 세 자리 중 **둘**(이을 대상 소실 · `AddItem` 중복 흡수)의 직접 재현. 원장 축(`item.followup_skipped` 건수 + 좌표)은 밟히는 나머지 하나에만, 그것도 조건부로 걸려 있다.
- **DESIGN §1② 와의 긴장** — 개념 하나(`이을 자격`)가 늘었다. 그 대가로 거짓 문구 하나가 사라진다는 판정과 근거.

`followups` 에 실을 것(**이 선점 뒤에 `add` 로 만든 것만 이을 수 있다** — 이번 작업에서 새로 만드는 것이므로 `followups` 에 직접 싣는다):
- `fd-finish-followups-leave-no-item-add-event` — `finish` 의 후속으로 만든 항목은 `item.add` 가 안 남아 다음 마무리에서 못 잇는다. 남기면 관문의 사정거리가 함께 넓어지는 것이 이 축의 어려움이다.
- `fd-design-says-judgment-link-is-an-fk` — `DESIGN.md:295`·`:383` 이 `judgment_link` 를 FK 라고 적는데 `target_id` 에는 `REFERENCES` 가 없다. 처음부터 부정확했고, 이번 변경이 그 간극을 앱 계층 검사로 메웠다는 사실을 함께 적어야 한다. **본문에 한 문장 넣어라:** 코드 표면(`render.go` · `FollowupsGuidance` · `followupSchema` · `SKILL.md` · 조건부로 `HandoffGuidance`)의 같은 문구는 `fd-finish-cannot-link-an-existing-item-as-followup` 에서 이미 "판단 링크"로 낮췄다 — 남은 것은 DESIGN 두 줄이다.

---

## 합칠 때 조심할 것

`internal/service/finish.go` 를 **넷**이 동시에 쥐고 있다(`finish_balance.go:48-52` 가 이름을 적는다): `fd-finish-discards-committed-work-on-aux-read` · `fd-note-beat-masquerades-as-mcp` · 이 항목 · `fd-item-body-immutable-is-undocumented`.

- 이 계획의 `finish.go` 헝크는 셋 + 조건부 하나다: **전단 검증 루프**(152~184) · **tx 절**(189~250) · **373행 주석** · 그리고 Task 8 Step 3' 이 충돌 없을 때만 만지는 **30행 `HandoffGuidance` 한 줄**. 새 함수는 전부 `finish_followups.go` 에 있어 그쪽 세션들과 안 부딪힌다.
- `fd-item-body-immutable-is-undocumented` 와는 **주제가 닿는다** — 잇기는 기존 항목의 본문을 **안 덮는다**(store 에 그럴 메서드가 없다). 그쪽이 본문 갱신 메서드를 만들면 "잇기가 본문을 덮어야 하나"가 새 질문이 된다. 이 계획의 답은 **덮지 않는다**이고 근거는 `TestFinishLinksAnExistingItemInsteadOfCreatingIt` 이 잠근다.
- `internal/mcpsrv/render.go` 는 다른 세션이 `RenderSessionStart`(순삽입 14줄) 를 쥐고 있다. 내 헝크는 `RenderFinish` 안뿐이라 줄이 안 겹친다.
- `plugins/flightdeck/DESIGN.md` 는 여러 세션이 만진다. **431행 한 줄만** 만지고, `git diff --stat` 로 확인한다.
