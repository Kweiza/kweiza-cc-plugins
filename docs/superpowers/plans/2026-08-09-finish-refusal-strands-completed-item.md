# 닫히지 못한 항목이 큐의 머리에 서는 것을 막는다 — 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `finish` 가 롤백되어 큐에 남은 항목을 `pick` 이 나이순 1순위로 내지 않게 하고, 사람이 회수·폐기하는 자리에서 그 사실을 보게 한다.

**Architecture:** 원장의 `item.finish` 이벤트는 트랜잭션 첫 문장에서 예약되어 **롤백 뒤에도 흘러간다**. 그 이벤트가 있는데 항목이 아직 열려 있으면 그 시도는 죽은 것이다 — 새 이벤트를 만들지 않고 이미 있는 것을 읽는다(새로 만들면 이미 쌓인 과거 사례에 소급되지 않는다). `store` 가 원장을 한 번 긁어 항목별로 접고, `service` 가 후보의 좌표로 거르고, `judge` 가 순위를 강등하며, `mcpsrv`·`web` 이 같은 사실을 사람이 읽는 세 자리에 낸다.

**Tech Stack:** Go 1.x · SQLite(modernc) · `html/template` · 표준 `testing`

**설계 문서:** `docs/superpowers/specs/2026-08-09-finish-refusal-strands-completed-item-design.md`

## Global Constraints

모든 태스크의 요구사항에 이 절이 암묵적으로 포함된다.

- **작업 위치:** `/home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-finish-refusal-strands-completed-item` — `main` 에 직접 커밋하지 않는다. 서버 소스는 그 아래 `plugins/flightdeck/server` 이고 아래에서 `$SRV` 로 적는다.
- **시험:** `cd $SRV && go test ./internal/<pkg>/ -run <Name> -v -count=1`
- **교차 빌드 관문은 `go vet` 이다.** `go build` 는 `_test.go` 를 건너뛰어 시험 코드에 대해 관문이 열려 있다. `go vet ./...` 와 `GOOS=windows go vet ./internal/<pkg>/` 를 쓴다.
- **gofmt 관문(`service/gofmt_gate_test.go`)이 모듈 전수(`_test.go` 포함, 파일 323개)를 본다.** 선언 앞 **doc 주석**의 이어지는 줄을 `//   ` 로 들여쓰면 Go 1.19+ gofmt 가 탭 코드블록으로 재작성해 관문이 깨진다 — doc 주석은 **평평한 `// `**, 함수 **본문 안** 주석은 들여쓰기 가능. 확인은 `gofmt -l internal/<pkg>/` 로 좁혀서 하는 편이 빠르다(관문 시험 자체는 111초).
- **`git add` 는 경로를 하나씩 지정한다.** `git add -A` · `git commit -a` 금지 — 같은 워크트리에 다른 세션이 동시에 있을 수 있고, 남의 미완성 편집이 커밋에 들어간다.
- **금지 문자열** — pick 응답에 새 문구를 넣을 때 다음을 재사용하면 개수를 세거나 절을 나누는 시험이 깨진다: `경로 실재: ` · `브랜치: ` · `fd move ` · `겹침 판정 범위:` · `안 들어갔다` · `겹침을 관측하지 않았다` · `묶을 게 없어 단독이다`. 그리고 새 줄이 `"  " + markClaimed/markRejected/markProposed + " "` 로 시작하면 안 된다. **`renderPathCheck` 의 nil 문구(`"이 응답은 그 축을 읽지 않았다 — 낡은 캐시이거나 서버가 이 축을 모르는 판이다."`)도 복사하면 안 된다** — `render_test.go:1377` 이 그것을 `unreadSum` 상수로 잡아 구성원 절 격리를 재고 있어, 같은 문장을 쓰면 남의 시험이 붉어진다.
- **신호 그물(`store/signal_is_not_history_test.go`)이 산문을 훑는다.** `plugins/flightdeck` 아래 모든 `.md`·`.sql`·비시험 `.go` 를 줄 단위로 보고, 한 줄에 `signal|신호` 와 `간격|분포|횟수|이력|누적|추세` 가 **40자 이내**로 같이 있으면 실패한다(부정 표지가 같은 줄에 있으면 통과). 산문을 손보면 다시 확인한다.
- **새 `<form>`·`<section>` 을 만들지 않는다.** `web/render_test.go:399·405·410` 이 `<form>` ≤4 · `method="post"` ==3 · `name="reason" required` ==3 을, `:204·209` 가 `<section>` ==6 · `(파생: ` ≥6 을 잠근다.

### 확정 인터페이스 — 전 층이 이 이름·타입을 그대로 쓴다

```go
// internal/model/types.go
type CloseDeclaration struct {
    Done        int       // mode=done 인 선언 수
    Dropped     int       // mode=dropped 인 선언 수
    Last        time.Time // 마지막 선언 시각
    LastSession string    // 마지막 선언을 한 세션 id
    LastMode    string    // 마지막 선언의 mode ("done" | "dropped")
}
func (d CloseDeclaration) Count() int { return d.Done + d.Dropped }

// internal/store/event.go
func (s *Store) CloseDeclarationsByItem(ctx context.Context, project string) (map[string]model.CloseDeclaration, error)

// internal/judge/eligible.go — EligibleInput
CloseDeclarations     map[string]model.CloseDeclaration
CloseDeclarationsRead bool

// internal/judge/bundle.go — Bundle
CloseDeclared       bool
CloseDeclaredDetail string

// internal/service/pick.go
func (s *Service) closeDeclarations(ctx context.Context, project string, cands []judge.Candidate) (map[string]model.CloseDeclaration, bool)
// PickResult.CloseDeclared   *model.CloseDeclaration `json:"close_declared,omitempty"`
// BundleMember.CloseDeclared *model.CloseDeclaration `json:"close_declared,omitempty"`

// internal/mcpsrv/render.go
func renderCloseDeclared(d *model.CloseDeclaration, indent string) string

// internal/web/page.go — ItemRow
CloseDeclared string   // "" = 선언 없음 · "?" 센티널 = 축을 못 읽었다
```

### 세 층이 반드시 맞춰야 하는 계약 하나

포인터 `CloseDeclared` 의 **nil 은 "이 응답은 그 축을 안 읽었다"** 다. 그러면 "축은 읽었는데 이 항목엔 선언 0건"은 **non-nil 영값**이어야 한다. `closeDeclarations` 가 내는 맵에는 선언이 **있는** 항목만 키가 있으므로 `service` 가 무심코

```go
if d, has := m[id]; has { res.CloseDeclared = &d }     // ✗ 정상 pick 마다 "안 읽었다"가 뜬다
```

로 쓰면 관측한 것을 안 했다고 말하게 된다 — 이 물결이 닫으려는 바로 그 부류의 거짓말이다. 반드시:

```go
if ok { d := m[id]; res.CloseDeclared = &d }           // ○ 없으면 영값이 들어간다
```

그리고 `renderCloseDeclared` 는 `d == nil` 일 때와 `d.Count() == 0` 일 때 **둘 다 빈 문자열**을 낸다. `d != nil` 만 보고 찍으면 모든 pick 응답에 쓸모없는 줄이 붙는다. 이 어긋남은 렌더 시험으로는 원리적으로 못 잡는다(`pick_wiring_test.go:13-20`).

## 태스크 순서와 의존

```
T1 (model 타입)
 └─ T2 (store 집계)
     ├─ T3 → T4 → T5   (judge: 정렬 축 → 입력 → 원장)
     │        └─ T6 → T7 → T8   (service: 집계·필터 → 배선 → 표면)
     │                   └─ T9 → T10 → T11   (render)
     └─ T12 → T13   (web)

T14 → T15   (web-actions — 다른 층과 파일이 하나도 안 겹친다. 언제 실행해도 된다)
T16 … T19   (산문 — 마지막. T16 은 T4 의 문구가 확정된 뒤)
```

- **T6(S1)** 은 T1·T2 가 랜딩된 뒤에만 GREEN 이 된다(컴파일 의존).
- **T7(S2)의 마지막 단계**는 T3·T4 가 랜딩된 뒤에만 GREEN 이 된다 — `lessBundle` 의 축이 있어야 순위가 뒤집힌다. judge 가 아직이면 그 단계는 빨간불로 남으므로, 그때는 T7 을 judge 뒤로 미룬다.
- **T10·T11** 은 T8 뒤다. `service.PickResult.CloseDeclared` 가 없으면 시험이 컴파일조차 안 된다.
- **T13** 은 T12 뒤다. `page.go` 가 T12 의 순수 함수를 부른다. T13 안에서는 `page.go` 를 먼저 다 고치고 `go vet` 으로 끊은 뒤 템플릿을 고친다 — 두 편집을 한 덩이로 묶으면 `QueuePanel.Targets` 의 `[]string → []ItemRow` 전환이 템플릿의 `{{.}}` 와 함께 깨져 원인을 못 가른다.

## 담당이 겹치는 자리 셋 — 뒤 태스크는 확인만 한다

층별로 초안을 뽑아 엮은 계획이라, 같은 줄을 둘이 맡은 자리가 셋 있다. **뒤 태스크가 `old_string` 을 못 찾으면 그것은 실패가 아니라 앞 태스크가 이미 한 것이다.** 고쳐진 결과가 맞는지만 보고 넘어간다.

1. **`web/page.go:88` 의 "다섯 축" → "여섯 축"** — **T13 이 고친다.** T16 의 해당 단계는 확인만 한다.
2. **`dashboard.gohtml:158` 의 "다섯 축"** — **T13 이 고친다.** T16 범위 밖이다.
3. **`judge/bundle.go:178-181` 의 `StarvationAge` 근거 문장** — **T3 이 고치고, T18 은 날짜와 실측을 보강한다.** T18 착수 전에 현재 문장을 먼저 읽어라.

`model/types.go` 를 T4 의 파일 목록이 "없을 때만"으로 들고 있는데, T1 이 이미 만들므로 실제로는 안 만진다.

## 가장 미끄러운 자리 넷

1. **`lessBundle` 의 축 자리.** 굶김 전용 갈래(`if a.Starved { … }`, `bundle.go:413`)는 **무조건 return** 한다. 새 축을 그 뒤에 두면 굶은 묶음끼리 영영 안 읽히고, 지금 큐는 열린 30건 중 26건이 굶었다 — 축이 겨냥한 인구 전체에 무동작이 된다. 유효한 자리는 `Starved` 비교 **바로 아래**, 굶김 전용 갈래 **위** 하나뿐이다. 축을 뒤로 옮기면 `TestLessBundleCloseDeclaredSinksAmongStarvedToo` **하나만** 붉어진다 — 그 시험이 이 배치의 유일한 감시자다.
2. **정렬 축 시험은 `lessBundle` 을 직접 부른다.** `EligibleBundle` 을 통하면 `bundles` 가 `lessCandidate` 로 정렬된 `fit` 에서 만들어져 축을 `return false` 로 지워도 통과한다(`bundle_test.go:370-378` 이 그 함정을 기록해 뒀다).
3. **`d.Last.IsZero()` 를 "이 항목을 처음 봤나" 센티널로 쓰지 않는다.** `at` 이 빈 문자열이면 `parseTime` 이 zero 를 내고, 그러면 두 번째 행이 최신 값을 덮어쓴다. `d.LastMode == ""` 를 쓴다. 그리고 `ORDER BY id DESC` 와 "처음 만난 행이 최신"이 한 쌍이다 — `at` 으로 최신을 판정하면 안 된다(마이크로초 해상도라 한 턴에 몰린 이벤트가 같은 값을 갖는다).
4. **폐기 태스크(T14·T15)의 판별축은 `force_reason` 이다.** `released_at IS NOT NULL` 만 단정하면 고침이 통째로 없어도 초록이다 — `SetItemState`(`store/item.go:521-527`)가 이미 채운다. 순서가 틀려도 오늘은 침묵하므로(뒤에 두면 `NFLiveClaim` 으로 빠져 `state='open'` 에 닿지 못한다) 순서를 잠그는 것도 `force_reason` 단정이다.

## 실측 기준값 (2026-08-09)

계획의 주석·문구가 인용하는 수다. 재확인은 **읽기 전용 사본**으로 하고 원장(`~/.flightdeck/fd.db`)을 직접 열지 않는다.

- `item.finish` 384건 (`context-platform` 245 · `kweiza-cc-plugins` 139) · mode 별 `done` 308 · `dropped` 76(20%)
- 항목 상태 조인: `done` 305 · `dropped` 75 · 항목 행 없음 3 · **`open`/`claimed` 0건**(오탐 0)
- 살아 있는 선점 8건 중 `item.finish` 이력이 있는 것 **0건** — 이 축은 오늘의 큐를 안 고친다. 막는 것은 재발이다.
- 열린 항목 30건 중 24h 초과 **26건**(86.7%) — 기아는 예외가 아니라 현재 기본값이다.
- 사고 4건의 롤백된 판단 본문: 10300 · 5421 · 5060 · 3032 바이트

---

### Task 1: model.CloseDeclaration — store 와 judge 가 공유할 수 있는 유일한 자리
**Files:**
- Test: internal/model/close_declaration_test.go (새 파일 — 이 패키지의 첫 _test.go 다)
- Modify: internal/model/types.go:310-317 (Event struct 블록 바로 뒤에 삽입)

**Interfaces:**
- Consumes: 없음 — 이 회차의 첫 태스크다. 기존 코드에서 쓰는 것은 model 패키지가 이미 import 한 `time` 하나뿐이다(types.go:10).
- Produces: type model.CloseDeclaration struct { Done, Dropped int; Last time.Time; LastSession, LastMode string } 와 func (d CloseDeclaration) Count() int. 이 이름·필드·타입을 store·judge·service·mcpsrv·web 다섯 층이 그대로 쓴다 — 바꾸면 뒤 태스크 전부가 깨진다.

- [ ] **Step 1: 실패 시험을 먼저 쓴다**

새 파일 `internal/model/close_declaration_test.go` 를 통째로 만든다.

```go
package model

import "testing"

// 이 패키지의 첫 시험 파일이다. model 은 지금까지 순수 데이터뿐이라 시험할 행동이 없었고,
// CloseDeclaration 이 **메서드를 가진 첫 타입**이다.
//
// Count 가 사유 문구의 "종료 선언 N건"을 만든다. 한쪽 mode 를 빠뜨려도 컴파일은 되고
// 시험도 안 죽는데 화면만 조용히 작은 수를 말한다 — 그래서 여기 빨간불을 세운다.
func TestCloseDeclarationCountSumsBothModes(t *testing.T) {
	cases := []struct {
		name string
		in   CloseDeclaration
		want int
		why  string
	}{
		{
			name: "done 만", in: CloseDeclaration{Done: 2}, want: 2,
			why: "실측 384건 중 308건이 이쪽이다",
		},
		{
			name: "dropped 만", in: CloseDeclaration{Dropped: 3}, want: 3,
			why: "dropped 를 안 세면 실측 20%(384건 중 76건)가 통째로 침묵한다",
		},
		{
			name: "둘 다", in: CloseDeclaration{Done: 1, Dropped: 2}, want: 3,
			why: "둘은 처방이 갈려 따로 담지만 '몇 번 선언됐나'는 합이다",
		},
		{
			name: "빈 값은 0", in: CloseDeclaration{}, want: 0,
			why: "zero 값이 '선언 없음'이다 — '이 축을 안 읽었다'와 가르는 것은 호출부의 bool 이지 이 수가 아니다",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.in.Count(); got != c.want {
				t.Errorf("Count() = %d, 기대 %d\n%s", got, c.want, c.why)
			}
		})
	}
}
```

- [ ] **Step 2: 실패를 확인한다**

타입이 아직 없으므로 **컴파일이 안 된다**. Go 에서 이것이 이 단계의 빨간불이다 — 통과할 수 없는 시험이 먼저 있어야 구현이 시험을 따라간다.

Run: `go test ./internal/model/ -run TestCloseDeclarationCountSumsBothModes -v -count=1`

Expected: 빌드 실패. `internal/model/close_declaration_test.go:14:8: undefined: CloseDeclaration` 류. FAIL [build failed].

- [ ] **Step 3: 최소 구현 — types.go 의 Event 뒤에 붙인다**

`internal/model/types.go` 를 Edit 한다. Event 는 원장 행이고 CloseDeclaration 은 그 행에서 접은 것이라 붙여 둔다.

**old_string** (types.go:310-317, 공백까지 그대로):

```go
type Event struct {
	ID        int64
	At        time.Time
	Project   string
	SessionID string
	Kind      string
	Payload   string // JSON
}
```

**new_string**:

```go
type Event struct {
	ID        int64
	At        time.Time
	Project   string
	SessionID string
	Kind      string
	Payload   string // JSON
}

// CloseDeclaration 은 이 항목을 닫으려다 롤백된 선언이다.
//
// ★ 뜻은 "일이 끝났다"가 아니라 **"이 항목은 닫혀야 한다는 판단이 본문과 함께 내려졌다"**이다.
// 원장의 item.finish 는 트랜잭션 첫 문장에서 예약되고 롤백 갈래에서도 흘러가므로, 그 이벤트가
// 있는데 항목이 아직 열려 있다면 그 시도는 죽은 것이다 — 그리고 그때 쓰인 판단 본문도 함께
// 죽었다(실측 10300·5421·5060·3032 바이트).
//
// ★ 이 타입이 **model 에 있는 이유.** store 도 judge 도 이것을 손에 쥐는데 둘 사이에는 의존이
// 없다(store 는 judge 를 import 하지 않고, judge 의 import 는 model 하나뿐이다). 어느 한쪽에
// 두면 없던 방향의 의존이 새로 생긴다. Rejection 이 같은 모양의 선례다 — judge 가 만들고
// store 가 저장한다.
//
// ★ Done 과 Dropped 을 **합쳐 담지 않는다.** 처방이 갈린다 — done 은 "이미 랜딩됐을 수 있다",
// dropped 는 "이미 버리기로 판정됐을 수 있다"이고, 실측 384건 중 dropped 가 76건(20%)이다.
type CloseDeclaration struct {
	Done        int       // mode=done 인 선언 수
	Dropped     int       // mode=dropped 인 선언 수
	Last        time.Time // 마지막 선언 시각
	LastSession string    // 마지막 선언을 한 세션 id
	LastMode    string    // 마지막 선언의 mode ("done" | "dropped")
}

// Count 는 mode 를 가리지 않은 선언 수다. "몇 번 선언됐나"는 합이다.
func (d CloseDeclaration) Count() int { return d.Done + d.Dropped }
```

- [ ] **Step 4: 통과를 확인한다**

네 갈래 전부 초록이어야 한다.

Run: `go test ./internal/model/ -run TestCloseDeclarationCountSumsBothModes -v -count=1`

Expected: --- PASS: TestCloseDeclarationCountSumsBothModes 및 하위 네 갈래(done_만 · dropped_만 · 둘_다 · 빈_값은_0) 전부 PASS. ok github.com/kweiza/flightdeck/internal/model

- [ ] **Step 5: 관문 — gofmt 와 교차 빌드**

gofmt 관문(`service/gofmt_gate_test.go`)이 **_test.go 를 포함해** 모듈 전수를 본다. 새 파일 둘 다 걸린다.
교차 빌드는 `go build` 가 아니라 `go vet` 이다 — build 는 _test.go 를 건너뛴다.

Run: `gofmt -l ./internal/model && go vet ./...`

Expected: gofmt 출력 0줄, vet 출력 0줄. (`internal/web/actions_test.go` 가 gofmt -l 에 뜨면 그것은 이 워크트리의 다른 세션 것이다 — 노트 참조)

- [ ] **Step 6: 커밋**

```
feat(flightdeck): 롤백된 종료 선언에 이름을 준다 — store 와 judge 가 공유할 자리는 model 뿐이다

원장의 item.finish 는 트랜잭션 첫 문장에서 예약되고 롤백 갈래에서도 흘러간다.
그래서 그 이벤트가 있는데 항목이 아직 열려 있으면 그 시도는 롤백된 것이고,
그때 쓰인 판단 본문도 함께 죽었다(실측 10300·5421·5060·3032 바이트).

타입을 model 에 둔다. store 는 judge 를 import 하지 않고 judge 의 import 는
model 하나뿐이라, 어느 한쪽에 두면 없던 방향의 의존이 새로 생긴다.
Rejection 이 같은 모양의 선례다.

Done 과 Dropped 을 합치지 않는다 — 처방이 갈리고 실측 384건 중 dropped 가 76건이다.
Count() 는 사유 문구의 "종료 선언 N건"을 만드는 유일한 자리라 시험으로 잠근다
(이 패키지의 첫 _test.go 다).
```

Run: `git add plugins/flightdeck/server/internal/model/types.go plugins/flightdeck/server/internal/model/close_declaration_test.go && git commit -F -`

Expected: 커밋 1건. `git status --short` 에 model 관련 항목이 남지 않는다.

---

### Task 2: Store.CloseDeclarationsByItem — 원장을 한 번 긁어 항목별로 접는다
**Files:**
- Test: internal/store/event_close_declarations_test.go (새 파일)
- Modify: internal/store/event.go:3-11 (import 에 strings 추가)
- Modify: internal/store/event.go:227 (CountEvents 주석 바로 앞에 상수 + 함수 둘 삽입)

**Interfaces:**
- Consumes: 태스크 1의 `model.CloseDeclaration` (Done·Dropped·Last·LastSession·LastMode + Count()). 그리고 기존 store 헬퍼 넷을 그대로 쓴다: `clip(string,int) string`(store.go:799) · `parseTime(string) (time.Time, error)`(store.go:758) · `str(sql.NullString) string`(store.go:790) · `fmtTime`·`nowStamp`(store.go:731·739, 시험에서만).
- Produces: func (s *Store) CloseDeclarationsByItem(ctx context.Context, project string) (map[string]model.CloseDeclaration, error) — service.closeDeclarations 가 이것 하나만 부른다. 부재는 **빈 맵**이고 nil 이 아니다. 반환값에는 앵커 이전 선언·좌표 어긋난 선언·성공한 마무리가 **전부 들어 있다**(service 가 items 로 걸러야 한다). 곁딸린 것: 상수 closeDeclarationScanLimit = 5000, 상한을 받는 속살 (s *Store) closeDeclarationsByItem(ctx, project string, limit int).

- [ ] **Step 1: 실패 시험을 먼저 쓴다**

새 파일 `internal/store/event_close_declarations_test.go` 를 통째로 만든다.

**첫 시험이 이 회차의 핵심이다** — 이벤트를 손으로 심으면 "롤백돼도 흘러간다"는 전제 자체를 안 밟는다. store 시험 패키지는 service 를 import 할 수 없으므로(service → store 순환), `service.Finish` 의 tx 모양을 손으로 재현한다: 첫 문장에서 `tx.LogEvent("item.finish", …)`, 그 뒤 `tx.FinishItem` 이 선점 표류로 `*ClaimHeldError`(store/item.go:806)를 낸다.

```go
package store

import (
	"context"
	"errors"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 롤백된 finish 를 **실제로 일으켜** 그 이벤트가 원장에 남는 것을 본다.
//
// ★ 이벤트를 손으로 심으면 이 축의 전제 자체를 안 밟는다. 이 설계 전부가
// "Tx.LogEvent 는 롤백 뒤에도 흘러간다"(store.go 의 flushDeferred)에 얹혀 있는데,
// LogEvent 를 직접 부르면 그 문장을 시험이 한 번도 통과하지 않는다. 그래서 여기서는
// service.Finish 의 트랜잭션 모양을 그대로 재현한다 — 첫 문장에서 이벤트를 예약하고,
// 그 뒤 FinishItem 이 선점 표류로 거절해 tx 전체가 롤백된다.
func TestCloseDeclarationsByItemSeesRolledBackFinish(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	owner := mustSession(t, s, "p", "cc-owner")
	drifted := mustSession(t, s, "p", "cc-drifted")
	mustItem(t, s, "p", "it-1")

	if _, err := s.ClaimItem(ctx, "p", "it-1", owner.ID); err != nil {
		t.Fatalf("선점 실패: %v", err)
	}

	err := s.Tx(ctx, func(tx *Tx) error {
		tx.LogEvent("item.finish", "p", drifted.ID, map[string]any{
			"item": "it-1", "mode": string(model.ItemDone),
			"count": 0, "linked": 0, "bytes": 10300,
		})
		return tx.FinishItem("p", "it-1", drifted.ID, model.ItemDone, "")
	})
	var held *ClaimHeldError
	if !errors.As(err, &held) {
		t.Fatalf("선점 표류 거절이 *ClaimHeldError 가 아니다: %T %v", err, err)
	}

	// 전제 ① — 정말 롤백됐다. 항목은 남의 선점 그대로다.
	it, err := s.GetItem(ctx, "p", "it-1")
	if err != nil {
		t.Fatalf("항목 조회 실패: %v", err)
	}
	if it.State != model.ItemClaimed {
		t.Fatalf("롤백이 안 됐다 — 항목 상태가 %q 다(claimed 여야 한다)", it.State)
	}

	// 전제 ② — 그런데 선언은 원장에 남았다. 이 두 줄이 이 설계의 토대다.
	got, err := s.CloseDeclarationsByItem(ctx, "p")
	if err != nil {
		t.Fatalf("종료 선언 조회 실패: %v", err)
	}
	d, ok := got["it-1"]
	if !ok {
		t.Fatalf("롤백된 finish 가 원장에 안 남았거나 안 집혔다: %+v", got)
	}
	if d.Done != 1 || d.Dropped != 0 || d.Count() != 1 {
		t.Errorf("mode 별 수가 다르다: %+v (Done=1 Dropped=0 이어야 한다)", d)
	}
	if d.LastSession != drifted.ID {
		t.Errorf("마지막 선언 세션이 다르다: got %q, want %q — 사유 문구가 이 id 를 부른다",
			d.LastSession, drifted.ID)
	}
	if d.LastMode != string(model.ItemDone) {
		t.Errorf("마지막 선언 mode 가 다르다: got %q, want %q", d.LastMode, model.ItemDone)
	}
	if d.Last.IsZero() {
		t.Errorf("마지막 선언 시각이 안 찍혔다 — 사유 문구가 시각 없이 나간다")
	}
}

// 같은 항목에 대한 선언 여럿이 접히고, mode 는 갈려서 담긴다.
// 그리고 **성공한 마무리도 함께 센다** — 롤백 판정은 store 의 일이 아니다.
func TestCloseDeclarationsByItemFoldsRepeatsAndSeparatesModes(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	owner := mustSession(t, s, "p", "cc-owner")
	driftA := mustSession(t, s, "p", "cc-drift-A")
	driftB := mustSession(t, s, "p", "cc-drift-B")
	mustItem(t, s, "p", "it-1")
	mustItem(t, s, "p", "it-2")

	if _, err := s.ClaimItem(ctx, "p", "it-1", owner.ID); err != nil {
		t.Fatalf("선점 실패: %v", err)
	}

	// 표류한 세션이 남의 항목을 세 번 닫으려 한다. 셋 다 롤백된다.
	attempts := []struct {
		session string
		mode    model.ItemState
		reason  string
	}{
		{session: driftA.ID, mode: model.ItemDone},
		{session: driftA.ID, mode: model.ItemDropped, reason: "중복이라 버린다"},
		{session: driftB.ID, mode: model.ItemDropped, reason: "다시 봐도 중복이다"},
	}
	for i, a := range attempts {
		err := s.Tx(ctx, func(tx *Tx) error {
			tx.LogEvent("item.finish", "p", a.session, map[string]any{
				"item": "it-1", "mode": string(a.mode), "count": 0, "bytes": 3000,
			})
			return tx.FinishItem("p", "it-1", a.session, a.mode, a.reason)
		})
		var held *ClaimHeldError
		if !errors.As(err, &held) {
			t.Fatalf("%d번째 시도가 선점 표류로 안 죽었다: %T %v", i+1, err, err)
		}
	}

	// it-2 는 제 세션이 제대로 닫는다 — 성공한 마무리다.
	if _, err := s.ClaimItem(ctx, "p", "it-2", owner.ID); err != nil {
		t.Fatalf("it-2 선점 실패: %v", err)
	}
	if err := s.Tx(ctx, func(tx *Tx) error {
		tx.LogEvent("item.finish", "p", owner.ID, map[string]any{
			"item": "it-2", "mode": string(model.ItemDone), "count": 1, "bytes": 500,
		})
		return tx.FinishItem("p", "it-2", owner.ID, model.ItemDone, "")
	}); err != nil {
		t.Fatalf("정상 마무리가 실패했다: %v", err)
	}

	got, err := s.CloseDeclarationsByItem(ctx, "p")
	if err != nil {
		t.Fatalf("종료 선언 조회 실패: %v", err)
	}

	d := got["it-1"]
	if d.Done != 1 || d.Dropped != 2 || d.Count() != 3 {
		t.Errorf("it-1 의 mode 별 수가 다르다: %+v (Done=1 Dropped=2 Count=3 이어야 한다)", d)
	}
	if d.LastMode != string(model.ItemDropped) || d.LastSession != driftB.ID {
		t.Errorf("마지막 선언이 최신 것이 아니다: mode=%q session=%q, want mode=%q session=%q\n"+
			"ORDER BY id DESC 의 첫 행이 최신인데 나중 행이 덮어썼을 수 있다",
			d.LastMode, d.LastSession, model.ItemDropped, driftB.ID)
	}

	// ★ 성공한 마무리도 여기 있다. store 는 롤백을 판정하지 않는다 —
	// 그 판정에 필요한 항목 상태를 쥔 것은 service 다.
	if s2 := got["it-2"]; s2.Done != 1 {
		t.Errorf("성공한 마무리가 빠졌다: %+v — store 가 롤백 판정을 하고 있다", s2)
	}
}

// 프로젝트를 안 넘는다.
//
// ★ 이 축이 특히 중요하다. 실측 3건이 정확히 그 모양이다 — context-platform 에서 친 finish 인데
// 항목은 kweiza-cc-plugins 에 있다. 그것은 좌표 오류지 표류가 아니고, 프로젝트 스코프를 안 걸면
// 남의 프로젝트 선언이 이 프로젝트 항목의 강등 근거로 둔갑한다.
func TestCloseDeclarationsByItemIsPerProject(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	seed(t, s, "q")
	a := mustSession(t, s, "p", "cc-A")
	b := mustSession(t, s, "q", "cc-B")

	s.LogEvent(ctx, "item.finish", "p", a.ID, map[string]any{"item": "it-1", "mode": "done"})
	s.LogEvent(ctx, "item.finish", "q", b.ID, map[string]any{"item": "it-1", "mode": "dropped"})
	s.LogEvent(ctx, "item.finish", "q", b.ID, map[string]any{"item": "it-9", "mode": "done"})

	got, err := s.CloseDeclarationsByItem(ctx, "p")
	if err != nil {
		t.Fatalf("종료 선언 조회 실패: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("남의 프로젝트가 섞였다: %+v", got)
	}
	if d := got["it-1"]; d.Done != 1 || d.Dropped != 0 {
		t.Errorf("같은 id 의 남의 프로젝트 선언이 접혔다: %+v", d)
	}
}

// 못 읽는 행은 안 센다 — 그리고 **다른 종류의 이벤트도 안 센다**.
//
// ★ 여기서는 이벤트를 손으로 심는다. 앞의 시험이 "롤백돼도 흘러간다"는 전제를 이미 밟았고,
// 이쪽이 재는 것은 파서의 그물이라 실제 롤백으로는 이 입력들을 만들 수 없다(payload 를
// 쓰는 것은 service.Finish 하나뿐이고 그것은 언제나 온전한 JSON 을 쓴다).
func TestCloseDeclarationsByItemSkipsUnreadableRows(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	a := mustSession(t, s, "p", "cc-A")

	// 셀 것 하나. 이것이 없으면 "전부 0"이 그물 덕인지 조회가 죽은 건지 못 가른다.
	s.LogEvent(ctx, "item.finish", "p", a.ID, map[string]any{"item": "it-ok", "mode": "done"})

	rows := []struct {
		name    string
		kind    string
		payload any
		why     string
	}{
		{
			name: "payload 가 JSON 이 아니다", kind: "item.finish", payload: nil,
			why: "직렬화 자체가 안 되는 값은 애초에 안 써지므로 아래에서 raw 로 심는다",
		},
		{
			name: "item 이 없다", kind: "item.finish",
			payload: map[string]any{"mode": "done", "count": 3},
			why:     "어느 항목의 것인지 모르면 셀 자리가 없다 — 세면 수만 늘고 대상이 없다",
		},
		{
			name: "item 이 공백뿐이다", kind: "item.finish",
			payload: map[string]any{"item": "   ", "mode": "done"},
			why:     "eventItemID 와 같은 규율로 trim 한 뒤 빈 것은 버린다",
		},
		{
			name: "mode 를 모른다", kind: "item.finish",
			payload: map[string]any{"item": "it-unknown-mode", "mode": "abandoned"},
			why:     "처방이 mode 로 갈린다 — 모르는 값을 한쪽에 몰면 화면이 원인을 단정한다",
		},
		{
			name: "mode 가 아예 없다", kind: "item.finish",
			payload: map[string]any{"item": "it-no-mode", "count": 1},
			why:     "옛 판의 payload 가 이 모양일 수 있다. 조용히 done 으로 접지 않는다",
		},
		{
			name: "종류가 다르다", kind: "item.finish_followups_missing",
			payload: map[string]any{"item": "it-other-kind", "mode": "done"},
			why: "실측 24건 전수가 20~181초 안에 재호출돼 성공했고 24개 항목 전부 done 이다 — " +
				"관문이 제 일을 한 기록이지 사고가 아니다",
		},
	}
	for _, r := range rows {
		if r.payload == nil {
			// LogEvent 는 nil payload 를 "{}" 로 쓴다. 깨진 JSON 은 그 경로로 못 만들므로
			// 직접 넣는다 — 옛 판이 남긴 행이나 손으로 만진 원장이 이 모양이다.
			if _, err := s.db.ExecContext(ctx,
				`INSERT INTO event(at, project, session_id, kind, payload) VALUES (?, ?, ?, ?, ?)`,
				fmtTime(nowStamp()), "p", a.ID, r.kind, `{"item": `); err != nil {
				t.Fatalf("%s: 심기 실패: %v", r.name, err)
			}
			continue
		}
		s.LogEvent(ctx, r.kind, "p", a.ID, r.payload)
	}

	got, err := s.CloseDeclarationsByItem(ctx, "p")
	if err != nil {
		t.Fatalf("종료 선언 조회 실패: %v", err)
	}
	if len(got) != 1 || got["it-ok"].Done != 1 {
		t.Fatalf("못 읽는 행이 섞였다: %+v\n%s", got, "기대는 it-ok 하나(Done=1)뿐이다")
	}
	for _, r := range rows {
		if r.payload == nil {
			continue
		}
		id, _ := r.payload.(map[string]any)["item"].(string)
		if id == "" {
			continue
		}
		if _, ok := got[id]; ok {
			t.Errorf("%s: %q 가 집혔다\n%s", r.name, id, r.why)
		}
	}
}

// 상한이 실제로 문다 — 그리고 **오래된 쪽부터** 잘린다.
//
// ★ 상수 5000 을 시험이 못 밟으면 그 수는 근거가 아니라 장식이다. 5000행을 심는 시험은
// 너무 느리므로 속살(closeDeclarationsByItem)에 상한을 열어 두고 여기서 2로 민다.
func TestCloseDeclarationsByItemCutsOldestFirst(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	a := mustSession(t, s, "p", "cc-A")

	s.LogEvent(ctx, "item.finish", "p", a.ID, map[string]any{"item": "it-old", "mode": "done"})
	s.LogEvent(ctx, "item.finish", "p", a.ID, map[string]any{"item": "it-mid", "mode": "dropped"})
	s.LogEvent(ctx, "item.finish", "p", a.ID, map[string]any{"item": "it-new", "mode": "done"})

	got, err := s.closeDeclarationsByItem(ctx, "p", 2)
	if err != nil {
		t.Fatalf("종료 선언 조회 실패: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("상한이 안 물었다: %+v", got)
	}
	if _, ok := got["it-old"]; ok {
		t.Errorf("가장 오래된 선언이 남았다 — ORDER BY 방향이 뒤집혔다: %+v", got)
	}
	if got["it-new"].Done != 1 || got["it-mid"].Dropped != 1 {
		t.Errorf("최근 둘이 안 집혔다: %+v", got)
	}

	// 상한이 0 이하면 아무것도 안 낸다. QueueReproduction 이 같은 자리를 같은 모양으로 막는다.
	empty, err := s.closeDeclarationsByItem(ctx, "p", 0)
	if err != nil {
		t.Fatalf("상한 0 은 오류가 아니다: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("상한 0 인데 %d건이 나왔다", len(empty))
	}
}

// 선언이 하나도 없으면 **빈 맵**이다. nil 도 오류도 아니다.
//
// ★ nil 을 "안 읽었다"로 쓰지 않는다. Go 의 nil 맵 조회는 zero 를 내므로 nil 과 빈 맵이
// 소비자 쪽에서 바이트 단위로 같은 출력이 되고, 그러면 "선언 0건"과 "이 축을 못 읽었다"를
// 가를 관측점이 하나도 없다. 그 둘을 가르는 것은 호출부의 두 번째 반환값(bool)이다.
func TestCloseDeclarationsByItemEmptyIsEmptyMapNotError(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")

	got, err := s.CloseDeclarationsByItem(ctx, "p")
	if err != nil {
		t.Fatalf("선언 0건은 오류가 아니다: %v", err)
	}
	if got == nil {
		t.Fatalf("nil 맵이 나왔다 — 부재는 빈 맵으로 낸다")
	}
	if len(got) != 0 {
		t.Fatalf("빈 원장인데 %d건이 나왔다: %+v", len(got), got)
	}
}
```

- [ ] **Step 2: 실패를 확인한다**

메서드 둘이 아직 없으므로 패키지가 빌드되지 않는다.

Run: `go test ./internal/store/ -run TestCloseDeclarationsByItem -v -count=1`

Expected: 빌드 실패. `s.CloseDeclarationsByItem undefined (type *Store has no field or method CloseDeclarationsByItem)` 및 `s.closeDeclarationsByItem undefined`. FAIL [build failed].

- [ ] **Step 3: 구현 ① — event.go 의 import 에 strings 를 넣는다**

`internal/store/event.go` 를 Edit 한다. `strings.TrimSpace` 를 쓴다 — `eventItemID`(service/finish_followups.go:195)와 같은 규율이다.

**old_string** (event.go:3-11):

```go
import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)
```

**new_string**:

```go
import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)
```

- [ ] **Step 4: 구현 ② — QueueReproduction 뒤·CountEvents 앞에 상수와 함수 둘을 넣는다**

같은 파일을 다시 Edit 한다. 자리는 `QueueReproduction`(같은 kind 를 긁는 이웃) 바로 뒤다.

**old_string** (event.go:227, 이 한 줄이 파일에서 유일하다):

```go
// CountEvents 는 종류별 건수다. §10 의 지표(세션당 쓰기 호출 수 등)가 이걸로 나온다.
```

**new_string**:

```go
// closeDeclarationScanLimit 는 CloseDeclarationsByItem 이 한 번에 훑는 item.finish 행의 상한이다.
//
// ★ 근거를 수로 적는다(실측 2026-08-09 · ~/.flightdeck/fd.db). item.finish 는 384건이고
// 프로젝트별 최대 속도는 context-platform 의 245건/5.26일 = 46.6건/일 이다. 5000건은 그
// 속도로 **107일**이다. 이 축이 겨냥하는 인구는 "롤백된 뒤 아직 열려 있는 항목"이고, 열린
// 항목 나이의 실측 최대는 9.6일 · 사고 사례는 42시간이었다 — 107일은 그 11배다.
//
// ★ **성능 손잡이가 아니다.** EXPLAIN QUERY PLAN 은 event_by_kind(kind=?) 를 타고
// kind='item.finish' 행 전부를 훑은 뒤 project 로 거른다 — 훑는 양은 LIMIT 이 아니라 원장의
// 크기가 정한다(실측 384행에 1.2ms, LIMIT 500~20000 사이에 차이가 없다). LIMIT 이 실제로 무는
// 것은 정렬 버퍼와 JSON 파싱 횟수뿐이다. 그래서 넉넉히 잡되, 이 수가 실제로 물리기 시작하는
// 때(원장이 지금의 13배)를 상한을 다시 잴 신호로 남긴다.
const closeDeclarationScanLimit = 5000

// CloseDeclarationsByItem 은 이 프로젝트에서 "이 항목을 닫는다"고 선언된 이력을 항목별로 접는다.
//
// ★ 무엇을 긁나. kind='item.finish' 하나다. 그 이벤트는 Finish 가 트랜잭션의 **첫 문장**에서
// 예약하고(service/finish.go) Tx.LogEvent 는 롤백 갈래에서도 흘러가므로(store.go 의 flushDeferred),
// 이 원장에는 **성공한 마무리와 롤백된 마무리가 같이** 들어 있다. 둘을 가르는 것은 항목의
// 상태이고 그 판정은 여기서 하지 않는다 — 이 함수는 원자료만 낸다.
//
// ★ **앵커도 항목 존재 판정도 여기서 하지 않는다.** 시간 앵커(그 항목 CreatedAt 이후의 선언만
// 센다)와 후보 목록에 없는 id 버리기는 service 가 한다. 그쪽은 이미 items 를 손에 쥐고 있어
// 추가 조회가 0이고, 여기서 하려면 json_extract 를 조인 조건에 넣어야 하는데 그 선례가 이
// 저장소에 0건이다. 그래서 이 반환값에는 **좌표가 어긋난 선언(실측 3건: 다른 프로젝트에서 친
// finish)과 지웠다 다시 만든 id 의 옛 선언이 그대로 들어 있다.**
//
// ★ **이 수는 정확한 수가 아니라 하한이다.** flushDeferred 는 트랜잭션이 물던 ctx 를 그대로 쓰고
// LogEvent 는 쓰기 실패를 WARN 으로만 삼키므로, 클라이언트가 끊겨 ctx 가 취소되면 행이 안 써진다.
// BeginTx 자체가 실패하면 클로저를 안 부르므로 이벤트가 아예 안 남는다. 소비자의 문구가
// "정확히 N건"이 아니라 "적어도 N건"으로 말해야 한다.
//
// ★ payload 를 못 읽은 행은 **안 센다**(eventItemID · QueueReproduction 과 같은 규율).
// payload 는 자유 JSON 이라 스키마가 없고, 못 읽은 것을 세면 어느 항목의 것인지 모르는 채로
// 수만 늘어 화면이 관측하지 않은 것을 단정하게 된다.
func (s *Store) CloseDeclarationsByItem(ctx context.Context, project string) (map[string]model.CloseDeclaration, error) {
	return s.closeDeclarationsByItem(ctx, project, closeDeclarationScanLimit)
}

// closeDeclarationsByItem 은 상한을 받는 속살이다. 상한을 시험이 못 밟으면 그 수는 근거가
// 아니라 장식이다 — 5000행을 심는 시험은 너무 느리므로 여기로 인자를 연다.
func (s *Store) closeDeclarationsByItem(ctx context.Context, project string, limit int) (map[string]model.CloseDeclaration, error) {
	if limit <= 0 {
		return map[string]model.CloseDeclaration{}, nil
	}
	// ★ 창을 id 로 자른다. event 인덱스는 (kind,at)·(session_id,at) 뿐이고, at 은 마이크로초
	// 해상도라 한 턴에 몰린 이벤트가 같은 값을 가질 수 있다. id 는 AUTOINCREMENT 라 단조이고
	// 유일하다 — QueueReproduction 이 같은 이유로 같은 선택을 했다.
	rows, err := s.db.QueryContext(ctx, `
		SELECT at, session_id, payload FROM event
		WHERE project = ? AND kind = 'item.finish'
		ORDER BY id DESC LIMIT ?`, project, limit)
	if err != nil {
		return nil, fmt.Errorf("종료 선언 조회 실패(project=%q): %w", clip(project, 64), err)
	}
	defer rows.Close()

	out := make(map[string]model.CloseDeclaration)
	for rows.Next() {
		var at string
		var session sql.NullString
		var payload string
		if err := rows.Scan(&at, &session, &payload); err != nil {
			return nil, fmt.Errorf("종료 선언 행 해석 실패: %w", err)
		}
		var p struct {
			Item string `json:"item"`
			Mode string `json:"mode"`
		}
		if json.Unmarshal([]byte(payload), &p) != nil {
			continue
		}
		item := strings.TrimSpace(p.Item)
		if item == "" {
			continue
		}
		d := out[item]
		switch p.Mode {
		case string(model.ItemDone):
			d.Done++
		case string(model.ItemDropped):
			d.Dropped++
		default:
			// mode 를 모르면 안 센다. 처방이 mode 로 갈리므로 모르는 값을 한쪽에 몰면
			// 화면이 관측하지 않은 원인을 단정한다.
			continue
		}
		// ORDER BY id DESC 라 이 항목을 **처음** 만나는 행이 가장 최근 선언이다.
		if d.LastMode == "" {
			t, err := parseTime(at)
			if err != nil {
				return nil, err
			}
			d.Last, d.LastSession, d.LastMode = t, str(session), p.Mode
		}
		out[item] = d
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("종료 선언 순회 실패: %w", err)
	}
	return out, nil
}

// CountEvents 는 종류별 건수다. §10 의 지표(세션당 쓰기 호출 수 등)가 이걸로 나온다.
```

- [ ] **Step 5: 통과를 확인한다**

여섯 시험 전부 초록이어야 한다. 실물 SQLite 파일을 쓰므로 시험당 0.4초쯤 든다.

Run: `go test ./internal/store/ -run TestCloseDeclarationsByItem -v -count=1`

Expected: PASS 6건: SeesRolledBackFinish · FoldsRepeatsAndSeparatesModes · IsPerProject · SkipsUnreadableRows · CutsOldestFirst · EmptyIsEmptyMapNotError. ok github.com/kweiza/flightdeck/internal/store (약 2.5초)

- [ ] **Step 6: 변이로 그물을 확인한다 — 이 셋은 실제로 잡히는 것을 확인했다**

초록이 그물 덕인지 우연인지 가른다. **셋 다 넣었다 빼는 것이지 남기는 것이 아니다.**

변이 ① 정렬 방향 뒤집기 — `ORDER BY id DESC LIMIT ?` → `ORDER BY id ASC LIMIT ?`
→ FoldsRepeats(마지막 선언이 최신 것이 아니다) · CutsOldestFirst(ORDER BY 방향이 뒤집혔다) 둘이 빨개진다.

변이 ② 프로젝트 스코프 제거 — `WHERE project = ? AND kind = ...` → `WHERE (? = ? OR 1=1) AND kind = ...` (인자도 `project, project, limit` 로)
→ IsPerProject(남의 프로젝트가 섞였다)가 빨개진다.

변이 ③ mode 합치기 — `case string(model.ItemDropped): d.Dropped++` → `d.Done++`
→ FoldsRepeats(it-1 의 mode 별 수가 다르다)가 빨개진다.

확인 뒤 반드시 원상 복구한다.

Run: `go test ./internal/store/ -run TestCloseDeclarationsByItem -count=1`

Expected: 변이마다 위에 적은 시험이 FAIL. 원상 복구 뒤 다시 전부 PASS.

- [ ] **Step 7: 관문 — gofmt · 교차 빌드 · store 전수**

store 전수는 70초쯤 든다(실물 SQLite). 교차 빌드 관문은 `go vet` 이다 — `go build` 는 _test.go 를 건너뛴다.

Run: `gofmt -l ./internal/store ./internal/model && go vet ./... && go test ./internal/store/ ./internal/model/ -count=1`

Expected: gofmt 0줄 · vet 0줄 · `ok github.com/kweiza/flightdeck/internal/store` · `ok github.com/kweiza/flightdeck/internal/model`. (실측: store 71초)

- [ ] **Step 8: 커밋**

```
feat(flightdeck): 원장에 이미 있던 신호를 항목별로 접는다 — 롤백된 finish 는 하한으로 센다

새 이벤트 종류를 만들지 않는다. 신호가 이미 원장에 있고, 새로 만들면 지금 큐에
쌓인 과거 사례에 소급되지 않는다. kind='item.finish' 를 프로젝트로 걸러 한 번
긁고 payload 를 Go 로 파싱한다 — json_extract 선례가 이 저장소에 0건이고
eventItemID 가 이미 같은 일을 한다.

앵커와 항목 존재 판정은 여기서 안 한다. service 가 이미 items 를 쥐고 있어 추가
조회가 0이고, 여기서 하려면 json_extract 를 조인 조건에 넣어야 한다. 그래서 이
반환값에는 좌표 어긋난 선언(실측 3건)과 성공한 마무리가 그대로 들어 있다 —
doc 주석에 그 사실을 적었다.

이 수는 하한이다. flushDeferred 가 tx 의 ctx 를 그대로 쓰고 LogEvent 는 쓰기
실패를 WARN 으로만 삼키므로 원장에 안 써진 마무리가 있을 수 있다.

상한 5000 의 근거를 주석에 수로 적었다: 프로젝트별 최대 속도 46.6건/일(실측
context-platform 245건/5.26일)로 107일이고, 열린 항목 나이의 실측 최대는 9.6일이다.

시험은 롤백된 finish 를 실제로 일으킨다. 이벤트를 손으로 심으면 "롤백돼도
흘러간다"는 전제 자체를 안 밟는다. 정렬 방향·프로젝트 스코프·mode 분리 셋을
변이로 확인했다.
```

Run: `git add plugins/flightdeck/server/internal/store/event.go plugins/flightdeck/server/internal/store/event_close_declarations_test.go && git commit -F -`

Expected: 커밋 1건. `git status --short` 에 store 관련 항목이 남지 않는다.

---

### Task 3: judge 정렬 축 — Bundle 두 필드 + lessBundle 의 자리 (굶김 갈래보다 위)
**Files:**
- Create: internal/judge/bundle_close_declared_test.go
- Modify: internal/judge/bundle.go (12-16 · 166-169 · 178-184 · 378-426)

**Interfaces:**
- Consumes: 없다. 이 태스크는 model.CloseDeclaration 을 **안 쓴다** — Bundle 에 얹는 것이 bool 과 string 이라 저장층 태스크와 순서에 안 묶인다. 시험 헬퍼 t0(eligible_test.go:12) · cand(bundle_test.go:103) 는 같은 패키지에 이미 있다.
- Produces: judge.Bundle 에 `CloseDeclared bool` · `CloseDeclaredDetail string`. lessBundle 축 순서가 `Dependents → Starved → CloseDeclared → (굶김 전용 갈래) → 묶음 크기 → 최고령 → 선두 id`. 시험 파일 internal/judge/bundle_close_declared_test.go 가 생겼다(태스크 2·3 이 이어 붙인다).

- [ ] **Step 1: 실패 시험 넷을 먼저 쓴다 — lessBundle 을 직접 부른다**

새 파일 `internal/judge/bundle_close_declared_test.go` 를 이 내용 그대로 만든다.

**왜 lessBundle 을 직접 부르나.** bundle_test.go:370-378 이 그 함정을 이미 기록해 뒀다 — EligibleBundle 을 통해 확인하면 bundles 가 lessCandidate 로 정렬된 fit 에서 만들어져서, 축을 통째로 `return false` 로 지워도 답이 안 바뀐다.

```go
package judge

import (
	"testing"
	"time"
)

// 정렬 축(강등)을 lessBundle 을 직접 불러 확인한다 — **안 굶은 묶음끼리**.
//
// ★ EligibleBundle 을 통해서 확인하면 안 되는 이유는 bundle_test.go:370-378 이
// 이미 적어 뒀다: bundles 는 lessCandidate 로 정렬된 fit 에서 만들어져서, 축을
// 통째로 지워도 우연히 같은 답이 나온다.
//
// 축 격리: ②(묶음 크기)·③(최고령)·④(id)를 **전부 declared 편으로** 몰아 둔다.
// 강등 축 하나만 clean 편이다. 그래서 이 축을 지우면 반드시 붉어진다.
func TestLessBundleCloseDeclaredSinksAmongUnstarved(t *testing.T) {
	declared := Bundle{
		Lead:          cand("a-declared", 0, nil),
		Members:       []Candidate{cand("m1", 0, nil), cand("m2", 0, nil)},
		Oldest:        t0,
		CloseDeclared: true,
	}
	clean := Bundle{Lead: cand("z-clean", 0, nil), Oldest: t0.Add(72 * time.Hour)}
	if !lessBundle(clean, declared) {
		t.Fatalf("종료 선언이 붙은 3건 묶음이 안 붙은 단독을 이겼다 — 닫히지 못한 항목이 다시 큐의 머리에 선다")
	}
	if lessBundle(declared, clean) {
		t.Fatalf("역방향이 대칭이 아니다 — 강등된 쪽이 이겼다")
	}
}

// 같은 축을 **굶은 묶음끼리** 확인한다. 이 시험이 축의 자리를 정한다.
//
// ★ lessBundle 의 굶김 전용 갈래(`if a.Starved`)는 무조건 return 한다. 그래서
// 이 축을 그 갈래 **뒤**에 두면 굶은 묶음끼리는 영영 안 읽힌다. 지금 큐는 열린
// 30건 중 26건이 굶었고 사고 항목도 회수 시점에 42시간이었다 — 뒤에 두면 이 축이
// 겨냥한 인구 **전체**에 대해 무동작이 된다. 위 시험은 그 배치를 못 가른다.
//
// 축 격리: declared 를 **더 오래 굶은 쪽**으로 둔다(굶김 갈래는 최고령순이다).
// id 도 declared 편이다. 축이 갈래 뒤로 내려가면 declared 가 이겨서 반드시 붉어진다.
func TestLessBundleCloseDeclaredSinksAmongStarvedToo(t *testing.T) {
	declared := Bundle{Lead: cand("a-declared", 0, nil), Oldest: t0, Starved: true, CloseDeclared: true}
	clean := Bundle{Lead: cand("z-clean", 0, nil), Oldest: t0.Add(time.Hour), Starved: true}
	if !lessBundle(clean, declared) {
		t.Fatalf("둘 다 굶었을 때 강등이 안 읽혔다 — 축이 굶김 전용 갈래 뒤에 있으면 큐의 26/30 에 대해 무동작이다")
	}
	if lessBundle(declared, clean) {
		t.Fatalf("역방향이 대칭이 아니다 — 더 오래 굶은 강등이 그대로 이겼다")
	}
}

// ①(의존자 합)은 강등보다 **앞**이다. 이걸 풀어야 남이 움직이는 정도는
// "이 항목이 이미 닫혔을지 모른다"보다 먼저 답해야 할 질문이다.
//
// 축 격리: ③④를 clean 편에 두고, 강등도 clean 편이다. declared 편은 ① 하나뿐이다.
func TestLessBundleDependentsBeatCloseDeclared(t *testing.T) {
	declared := Bundle{Lead: cand("z-declared", 0, nil), Dependents: 5,
		Oldest: t0.Add(72 * time.Hour), CloseDeclared: true}
	clean := Bundle{Lead: cand("a-clean", 0, nil), Dependents: 1, Oldest: t0}
	if !lessBundle(declared, clean) {
		t.Fatalf("의존자 합 5가 강등에 밀렸다 — ①이 강등보다 앞이어야 한다")
	}
	if lessBundle(clean, declared) {
		t.Fatalf("역방향이 대칭이 아니다")
	}
}

// 기아는 강등보다 **앞**이다 — 그것이 이 설계의 탈출구다.
//
// ★ 강등에 유효기간을 안 걸었다(설계 §3: 항목을 위험하게 만든 조건은 시간이
// 지난다고 낫지 않는다). 그러면 강등된 항목이 영영 안 나오는 루프가 걱정인데,
// 축을 Starved **아래**에 둔 것이 그 루프를 구조적으로 끊는다 — 강등된 항목도
// 굶는 순간 안 굶은 묶음 전부를 이긴다. 조정 상수를 하나도 안 들이고 끊는다.
//
// 축 격리: ②③④와 강등까지 **전부** clean 편이다. declared 편은 기아 하나뿐이다.
func TestLessBundleStarvedBeatsCloseDeclared(t *testing.T) {
	declaredStarved := Bundle{Lead: cand("z-declared", 0, nil),
		Oldest: t0.Add(72 * time.Hour), Starved: true, CloseDeclared: true}
	cleanFresh := Bundle{Lead: cand("a-clean", 0, nil),
		Members: []Candidate{cand("m1", 0, nil), cand("m2", 0, nil)}, Oldest: t0}
	if !lessBundle(declaredStarved, cleanFresh) {
		t.Fatalf("굶은 강등 항목이 안 굶은 묶음에 밀렸다 — 강등에 탈출구가 없어 영구 유배가 된다")
	}
	if lessBundle(cleanFresh, declaredStarved) {
		t.Fatalf("역방향이 대칭이 아니다")
	}
}
```

Run: `go test ./internal/judge/ -run 'CloseDeclared' -v -count=1`

Expected: 컴파일 실패. `internal/judge/bundle_close_declared_test.go:32:3: unknown field CloseDeclared in struct literal of type Bundle` (네 시험 전부 같은 이유). 이것이 첫 RED 다.

- [ ] **Step 2: Bundle 에 필드 둘을 더한다**

`internal/judge/bundle.go:166-169`. before(정확히 이 두 줄):

```go
	Reason string
}
```

after:

```go
	Reason string
	// CloseDeclared 는 **이 묶음의 선두**를 닫으려다 롤백된 선언이 원장에 있다는 사실이다.
	//
	// zero(false)가 "강등 안 함"이다. 이 필드를 안 찍는 호출부(judge 를 직접 부르는
	// 시험·아직 안 배선된 경로)가 큐 순서를 뒤집지 않게 하는 것이 그 방향의 이유다.
	//
	// EligibleInput.CloseDeclarationsRead 가 false 면 **언제나 false** 다 —
	// "축을 안 읽었다"와 "선언이 없다"가 여기서 한 값으로 접히지만, 그 접힘은
	// 안전한 쪽이다(기존 순서가 그대로 산다). 둘을 갈라 보여줘야 하는 표면은
	// service 가 (값, bool) 두 반환값으로 따로 나른다.
	CloseDeclared bool
	// CloseDeclaredDetail 은 그 사실을 사람이 읽는 한 조각이다.
	// Reason 과 RejectNotTop 의 Detail 이 **같은 문자열**을 싣는다 — 화면이 말하는
	// 이유와 원장이 남기는 이유가 갈리면 어느 쪽이 참인지 되짚을 길이 없다.
	CloseDeclaredDetail string
}
```

`\tReason string` 은 이 파일에 한 번뿐이다(확인함).

Run: `go test ./internal/judge/ -run 'CloseDeclared' -v -count=1`

Expected: 컴파일은 통과하고 **단정 두 개가 붉다**:
--- FAIL: TestLessBundleCloseDeclaredSinksAmongUnstarved ("종료 선언이 붙은 3건 묶음이 안 붙은 단독을 이겼다")
--- FAIL: TestLessBundleCloseDeclaredSinksAmongStarvedToo ("둘 다 굶었을 때 강등이 안 읽혔다")
--- PASS: TestLessBundleDependentsBeatCloseDeclared
--- PASS: TestLessBundleStarvedBeatsCloseDeclared
뒤의 둘이 지금부터 초록인 것은 정상이다 — 그 둘은 새 축이 **기존 축을 밀어내지 않는지** 지키는 가드다.

- [ ] **Step 3: lessBundle 에 축 한 줄 — 굶김 전용 갈래 **앞****

`internal/judge/bundle.go:410-413`. before:

```go
	if a.Starved != b.Starved {
		return a.Starved
	}
	if a.Starved { // 둘 다 굶었다 — 묶음 크기를 건너뛰고 최고령순으로만 푼다
```

after:

```go
	if a.Starved != b.Starved {
		return a.Starved
	}
	if a.CloseDeclared != b.CloseDeclared {
		return !a.CloseDeclared
	}
	if a.Starved { // 둘 다 굶었다 — 묶음 크기를 건너뛰고 최고령순으로만 푼다
```

세 줄이다. `!a.CloseDeclared` 를 돌려주는 것이 강등이다 — a 에 선언이 없을 때 a 가 앞선다.

Run: `go test ./internal/judge/ -run 'CloseDeclared' -v -count=1`

Expected: 네 시험 전부 PASS. `ok  github.com/kweiza/flightdeck/internal/judge`

- [ ] **Step 4: 배치 변이를 손으로 한 번 확인한다 — 이 축의 자리가 진짜 물려 있나**

방금 넣은 세 줄을 **굶김 전용 갈래 뒤로** 임시로 옮긴다(`if a.Starved { … }` 블록의 닫는 중괄호 바로 다음). 시험을 돌리고, 확인한 뒤 원래 자리로 되돌린다.

이 확인을 왜 하나: "축을 넣었다"와 "축이 굶은 인구에도 돈다"는 다른 사실이고, 지금 큐의 26/30 이 그 인구다. 이 변이가 안 잡히면 아래 커밋은 아무것도 안 고친 것이 된다.

실측(사본에서 이미 밟았다): 옮기면 `TestLessBundleCloseDeclaredSinksAmongStarvedToo` **하나만** 붉어진다. 축을 통째로 지우면 두 개가, 방향을 `return a.CloseDeclared` 로 뒤집어도 두 개가 붉어진다.

Run: `go test ./internal/judge/ -run 'CloseDeclared' -v -count=1`

Expected: 옮긴 상태: `--- FAIL: TestLessBundleCloseDeclaredSinksAmongStarvedToo` 하나만 붉다(나머지 셋 PASS). 되돌린 뒤: 넷 다 PASS.

- [ ] **Step 5: 이 변경이 낡게 만든 주석 셋을 정정한다**

설계 §8 이 지목한 자리다. 시험이 문자열 존재만 보므로 빨간불 없이 표류한다.

**① 파일 머리 — `bundle.go:14-16`.** before:

```go
// 이 파일의 함수는 전부 순수 함수다. 그리고 **기존 판정을 하나도 안 고친다** —
// Eligible·PathsOverlap·lessCandidate 는 다른 질문에 답하고 있고,
// 같은 함수를 두 질문에 쓰면 한쪽을 고칠 때 다른 쪽이 조용히 바뀐다.
```

after:

```go
// 이 파일의 함수는 전부 순수 함수다. 원장·git·시계를 여기서 읽지 않는다 —
// 필요한 사실은 호출부가 EligibleInput 에 찍어 넣고(Now · CloseDeclarations)
// 판정은 그 값만 본다. 그래야 시험이 판정 규칙을 직접 부를 수 있다.
//
// 그리고 **Eligible·PathsOverlap·lessCandidate 는 하나도 안 고친다** — 셋은 다른
// 질문에 답하고 있고, 같은 함수를 두 질문에 쓰면 한쪽을 고칠 때 다른 쪽이 조용히
// 바뀐다. lessBundle 은 이 파일의 것이라 여기서 자란다(기아 축과 종료 선언 축이
// 그렇게 붙었다). "하나도 안 고친다"를 lessBundle 까지로 읽으면 그 문장이 거짓이 된다.
```

**② StarvationAge — `bundle.go:178-183`.** 지금 큐에서 거짓인 문장이다. before:

```go
// 24h 는 p90(16.3h) 바깥이라 **정상 작업이 안 걸린다.** 이 값을 p90 아래로 내리면
// 평시 항목이 줄줄이 기아로 판정돼 묶음 기능이 사실상 죽는다 — 그때는 기아 축이
// 예외가 아니라 새 기본값이 되고, 이 상수를 넣은 이유가 사라진다.
//
// "하루가 지나도 아무도 안 집었다"가 사람에게 설명 가능한 문장이라는 것도 값의
// 일부다. 원장이 낸 순위를 사람이 못 읽으면 두 번째 세션부터 무시된다.
```

after:

```go
// 24h 는 p90(16.3h) 바깥이라 **끝난 일의 리드타임 분포로는** 정상 작업이 안 걸린다.
//
// ★★ 그런데 지금 큐에서는 걸린다. 2026-08-09 실측: **열린 30건 중 26건이 24h 를
// 넘겼다.** 앞 문단이 재는 것은 이미 끝난 일의 리드타임이고 큐에 남은 것은 정의상
// 그 분포에서 빠진 꼬리다 — 두 분포를 같은 것으로 읽은 것이 "정상 작업이 안 걸린다"가
// 한동안 거짓인 채 남아 있던 이유다. **기아는 예외가 아니라 현재 기본값이다.**
//
// 그래서 이 상수를 얼마로 두느냐보다 중요한 것이 기아 영역 **안에서**의 순서다.
// lessBundle 의 굶김 전용 갈래는 무조건 return 하므로, 그 뒤에 놓인 축은 큐의
// 26/30 에 대해 무동작이 된다 — 축을 더할 때마다 그 자리를 먼저 정해야 한다.
//
// 이 값을 p90 아래로 내리면 남은 4건마저 기아로 접혀 축이 아무것도 안 가른다.
// "하루가 지나도 아무도 안 집었다"가 사람에게 설명 가능한 문장이라는 것도 값의
// 일부다. 원장이 낸 순위를 사람이 못 읽으면 두 번째 세션부터 무시된다.
```

**③ lessBundle 축 목록 — `bundle.go:380-383`.** before:

```go
//	① 의존자 수 합 ↓ — 이걸 풀어야 남이 움직이는 정도
//	② 묶음 크기   ↓ — 한 번에 더 많이 푸는 쪽이 이긴다
//	③ 최고령      ↑ — 오래 방치된 것을 먼저
//	④ 선두 id     사전순 — 동점 처리. 없으면 같은 입력에 다른 답이 나온다
```

after:

```go
//	①  의존자 수 합 ↓ — 이걸 풀어야 남이 움직이는 정도
//	①′ 기아          — 굶은 쪽이 먼저(임계는 StarvationAge 하나)
//	①″ 종료 선언     — 닫히려다 롤백된 항목은 **뒤로**. 거르지는 않는다
//	②  묶음 크기   ↓ — 한 번에 더 많이 푸는 쪽이 이긴다
//	③  최고령      ↑ — 오래 방치된 것을 먼저
//	④  선두 id     사전순 — 동점 처리. 없으면 같은 입력에 다른 답이 나온다
```

**④ 같은 주석 꼬리 — `bundle.go:403-405`.** before:

```go
// ★ 기아 영역 **안에서는 ②를 안 본다.** 다시 넣으면 굶은 단독이 굶은 묶음에 밀리는,
// 똑같은 함정이 그 안에서 재현된다. 예외 상태에서 방어 가능한 규칙은
// "가장 오래 굶은 것부터" 하나뿐이다.
```

after:

```go
// ★ 기아 영역 **안에서는 ②를 안 본다.** 다시 넣으면 굶은 단독이 굶은 묶음에 밀리는,
// 똑같은 함정이 그 안에서 재현된다. 예외 상태에서 방어 가능한 규칙은
// "가장 오래 굶은 것부터" 하나뿐이다.
//
// ★★★ ①″(종료 선언)의 자리는 **①′ 바로 아래, 굶김 전용 갈래보다 위**다. 둘 다
// 근거가 있다.
//
//	· 왜 ①′ 아래인가. 이 강등에는 유효기간이 없다(설계 §3 — 항목을 위험하게 만든
//	  조건은 시간이 지난다고 낫지 않는다. 기한 만료는 곧 사고 재현이다). 그러면
//	  강등된 항목이 영영 안 나오는 루프가 걱정인데, 기아를 위에 두는 것이 그것을
//	  구조적으로 끊는다 — 강등된 항목도 굶는 순간 안 굶은 묶음 전부를 이긴다.
//	  조정 상수를 하나도 안 들이고 끊는다.
//	· 왜 굶김 전용 갈래보다 위인가. 그 갈래는 **무조건 return** 하므로 뒤에 놓인
//	  축은 굶은 묶음끼리 영영 안 읽힌다. 지금 큐는 열린 30건 중 26건이 굶었고
//	  사고 항목도 회수 시점에 42시간이었다(StarvationAge 주석의 ★★ 참고) —
//	  뒤에 두면 이 축이 겨냥한 인구 **전체**에 대해 무동작이 된다.
//	  TestLessBundleCloseDeclaredSinksAmongStarvedToo 가 그 배치를 못박는다.
//
// lessCandidate 에는 **안 넣는다.** 제품이 부르는 것은 EligibleBundle 하나이고
// (judge.Eligible 은 저장소 전체에서 호출자가 0건이다), 거기 넣은 축은 묶음 구성원의
// 표시 순서만 바꾼다(설계 §4-②).
```

Run: `gofmt -l internal/judge && go vet ./...`

Expected: 둘 다 출력 없음(gofmt 가 파일 이름을 하나도 안 낸다).

- [ ] **Step 6: 패키지 전체와 교차 관문**

기존 시험이 하나도 안 깨졌는지 본다. Bundle 에 필드를 더한 것은 어느 호출부도 위치 지정 리터럴을 안 쓰므로(전수 확인: `judge.Bundle{` 생산 호출부 0건, `EligibleInput{` 은 pick.go:776 하나이고 전부 키 지정) 컴파일이 그대로 선다.

Run: `go test ./internal/judge/ -count=1 && go vet ./...`

Expected: `ok  github.com/kweiza/flightdeck/internal/judge` · vet 출력 없음

- [ ] **Step 7: 커밋**

```
feat(flightdeck): 닫히려다 롤백된 항목을 큐의 머리에서 내린다 — 자리는 굶김 갈래 앞이다

판정에 축 하나를 더한다. 거르지 않고 **강등한다** — 거르면 선점 표류가 아닌
이유로 롤백된 항목까지 큐에서 사라진다.

자리가 이 커밋의 전부다. lessBundle 의 굶김 전용 갈래는 무조건 return 하므로
그 뒤에 놓은 축은 굶은 묶음끼리 영영 안 읽힌다. 지금 큐는 열린 30건 중 26건이
굶었다 — 뒤에 뒀으면 이 축이 겨냥한 인구 전체에 대해 무동작이었다.
Starved 아래인 것도 근거가 있다: 강등에 유효기간을 안 걸었으므로(기한 만료는
곧 사고 재현이다) 탈출구가 필요한데, 굶는 순간 안 굶은 전부를 이기는 것이
조정 상수 없이 그 루프를 끊는다.

시험은 lessBundle 을 직접 부른다. EligibleBundle 을 통하면 축을 지워도
통과한다 — bundle_test.go:370-378 이 그 함정을 기록해 둔 자리다.

함께: "24h 는 p90 바깥이라 정상 작업이 안 걸린다"를 실측으로 정정한다.
그 문장이 재는 것은 끝난 일의 리드타임이고, 큐에 남은 것은 그 분포에서
빠진 꼬리다. 기아는 예외가 아니라 현재 기본값이다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
```

Run: `git add plugins/flightdeck/server/internal/judge && git commit -F -`

Expected: 커밋 1건. `git show --stat` 이 bundle.go · bundle_close_declared_test.go 둘만 낸다.

---

### Task 4: judge 입력 — EligibleInput 두 필드 + EligibleBundle 이 선두에 찍는다
**Files:**
- Modify: internal/judge/eligible.go (95-96)
- Modify: internal/judge/bundle.go (246-253 · 290)
- Modify: internal/judge/bundle_close_declared_test.go
- Test: internal/model/types.go (없을 때만)

**Interfaces:**
- Consumes: 태스크 1 의 `Bundle.CloseDeclared bool` · `Bundle.CloseDeclaredDetail string` · lessBundle 축. 그리고 저장층 태스크의 `model.CloseDeclaration{Done, Dropped int; Last time.Time; LastSession, LastMode string}` + `func (d CloseDeclaration) Count() int`. 그 타입이 아직 없으면 첫 단계가 만든다.
- Produces: `EligibleInput.CloseDeclarations map[string]model.CloseDeclaration` · `EligibleInput.CloseDeclarationsRead bool`. judge 내부 헬퍼 `closeDeclarationOf(in EligibleInput, id string) (model.CloseDeclaration, bool)` · `closeDeclaredDetail(d model.CloseDeclaration) string`. EligibleBundle 이 선두의 선언으로 Bundle 두 필드를 찍고 Reason 에 ` · ★종료 선언 N건 이상(…)` 을 덧붙인다. → service 배선 태스크가 이 두 필드를 채운다.

- [ ] **Step 1: model.CloseDeclaration 이 있는지 먼저 본다**

저장층 태스크가 이미 만들었으면 그대로 간다. **아무것도 안 나오면** 아래 블록을 `internal/model/types.go` 의 `Rejection` 구조체(237-239) 바로 뒤에 붙인다 — 그 자리를 고른 이유는 `model.Rejection` 이 정확히 같은 모양의 선례이기 때문이다(judge 가 만들고 store 가 저장한다. 이쪽은 store 가 만들고 judge 가 읽는다).

```go
// CloseDeclaration 은 이 항목을 닫으려다 롤백된 선언이다.
type CloseDeclaration struct {
	Done        int
	Dropped     int
	Last        time.Time
	LastSession string
	LastMode    string
}

func (d CloseDeclaration) Count() int { return d.Done + d.Dropped }
```

`model` 은 `time` 을 이미 import 한다(types.go:11).

Run: `grep -n 'type CloseDeclaration' internal/model/types.go`

Expected: `internal/model/types.go:NNN:type CloseDeclaration struct {` 한 줄. 안 나오면 위 블록을 붙이고 다시 돌려 한 줄이 나오게 만든다.

- [ ] **Step 2: 실패 시험 넷을 이어 붙인다 — 배선을 재는 자리**

`internal/judge/bundle_close_declared_test.go` 의 import 를 먼저 늘린다. before:

```go
import (
	"testing"
	"time"
)
```

after:

```go
import (
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)
```

헬퍼를 import 블록 바로 아래(첫 시험 앞)에 놓는다:

```go
// decl 은 시험용 종료 선언이다. mode 는 마지막 선언의 것이다.
func decl(done, dropped int, mode string) model.CloseDeclaration {
	return model.CloseDeclaration{
		Done: done, Dropped: dropped,
		Last: t0.Add(-time.Hour), LastSession: "01KZ785T-OLD", LastMode: mode,
	}
}
```

그리고 파일 끝에 시험 넷을 붙인다:

```go
// EligibleBundle 이 두 필드를 실제로 채우는지 — 배선이 이어져 있는지 본다.
//
// ★ lessBundle 단위 시험만 있으면 CloseDeclared 를 **아무도 안 채우는** 상태가
// 통과한다. 이 저장소가 여러 번 겪은 실패 모양이다(계산은 되는데 읽는 쪽이 0건) —
// TestEligibleBundleMarksStarvation 이 같은 이유로 서 있다.
//
// 그리고 **강등은 탈락이 아니다.** 단독 후보 하나로 부르면 그 항목은 여전히
// 추천된다 — 거르면 선점 표류 아닌 이유로 롤백된 항목까지 큐에서 사라진다(설계 §3).
func TestEligibleBundleMarksCloseDeclared(t *testing.T) {
	only := cand("a-declared", 0, nil)
	best, rej := EligibleBundle(EligibleInput{
		Self:                  "S1",
		Candidates:            []Candidate{only},
		CloseDeclarations:     map[string]model.CloseDeclaration{"a-declared": decl(1, 0, "done")},
		CloseDeclarationsRead: true,
	}, SiblingIndex{})
	if best == nil {
		t.Fatalf("강등이 탈락으로 샜다 — 추천이 nil 이다(사유 %v)", rej)
	}
	if best.Lead.Item.ID != "a-declared" {
		t.Fatalf("선두가 %q 다", best.Lead.Item.ID)
	}
	if !best.CloseDeclared {
		t.Fatalf("선언이 있는데 CloseDeclared 가 false 다 — 배선이 끊겼다")
	}
	// 사유 문구는 넷을 다 말해야 한다: 수가 하한이라는 것 · 마지막 세션 · mode ·
	// done 의 처방. 하나라도 없으면 사람이 무엇을 확인해야 하는지 모른다.
	for _, want := range []string{"종료 선언 1건 이상", "01KZ785T-OLD", "mode=done", "이미 랜딩됐을 수 있다"} {
		if !strings.Contains(best.CloseDeclaredDetail, want) {
			t.Fatalf("근거 조각에 %q 가 없다: %q", want, best.CloseDeclaredDetail)
		}
		if !strings.Contains(best.Reason, want) {
			t.Fatalf("Reason 이 %q 를 안 싣는다 — 왜 강등했는지 답 못 하는 추천이 된다: %q", want, best.Reason)
		}
	}
	// mode 를 안 합친다 — dropped 의 처방은 done 과 다르다.
	dropBest, _ := EligibleBundle(EligibleInput{
		Self:                  "S1",
		Candidates:            []Candidate{cand("a-declared", 0, nil)},
		CloseDeclarations:     map[string]model.CloseDeclaration{"a-declared": decl(0, 1, "dropped")},
		CloseDeclarationsRead: true,
	}, SiblingIndex{})
	if !strings.Contains(dropBest.CloseDeclaredDetail, "이미 버리기로 판정됐을 수 있다") {
		t.Fatalf("dropped 의 처방이 done 과 같은 문장으로 접혔다: %q", dropBest.CloseDeclaredDetail)
	}
}

// 강등된 항목은 **밀리되 사라지지 않는다.**
//
// ★ 거르면 선점 표류 아닌 이유로 롤백된 항목까지 큐에서 사라진다(설계 §3).
// Overlaps 가 "거르지 않고 알린다"로 선 것과 같은 자리다.
//
// 배치: 둘 다 단독·동나이·의존자 0이라 ①②③이 전부 동점이고, 선언이 없으면
// ④(id 사전순)로 a-declared 가 이긴다. 선언 하나만으로 승자가 뒤집혀야 한다.
func TestEligibleBundleCloseDeclaredDemotesButDoesNotDrop(t *testing.T) {
	best, rej := EligibleBundle(EligibleInput{
		Self:                  "S1",
		Candidates:            []Candidate{cand("a-declared", 0, nil), cand("z-clean", 0, nil)},
		CloseDeclarations:     map[string]model.CloseDeclaration{"a-declared": decl(1, 0, "done")},
		CloseDeclarationsRead: true,
	}, SiblingIndex{})
	if best == nil {
		t.Fatalf("추천이 nil 이다(사유 %v)", rej)
	}
	if best.Lead.Item.ID != "z-clean" {
		t.Fatalf("선두가 %q 다 — 선언이 붙은 a-declared 가 밀렸어야 한다", best.Lead.Item.ID)
	}
	if best.CloseDeclared {
		t.Fatalf("선언이 없는 z-clean 이 강등으로 찍혔다 — 선두가 아닌 항목의 선언을 읽었다")
	}
	// 사라지지 않는다: 원장에 not-top 으로 남는다.
	if !contains(codesFor(rej, "a-declared"), RejectNotTop) {
		t.Fatalf("강등된 항목의 사유가 %v 다 — not-top 으로 원장에 남아야 한다", codesFor(rej, "a-declared"))
	}
}

// CloseDeclarationsRead 가 false 면 이 축은 **아예 안 돈다** — 맵이 채워져 있어도.
//
// ★ 이것이 nil 맵을 "안 읽음"으로 안 쓴 이유다. 조회가 실패했는데 그 실패가
// "선언 0건"으로 접히면, 축을 못 읽은 pick 이 사고를 낸 그 항목을 다시 1순위로
// 낸다 — 그런데 응답은 아무 말도 안 한다. 관측을 못 하면 판정하지 않는다.
func TestEligibleBundleWithoutCloseDeclarationsReadDoesNotDemote(t *testing.T) {
	in := EligibleInput{
		Self:              "S1",
		Candidates:        []Candidate{cand("a-declared", 0, nil), cand("z-clean", 0, nil)},
		CloseDeclarations: map[string]model.CloseDeclaration{"a-declared": decl(1, 0, "done")},
		// CloseDeclarationsRead 를 일부러 안 켠다.
	}
	best, rej := EligibleBundle(in, SiblingIndex{})
	if best == nil {
		t.Fatalf("추천이 nil 이다(사유 %v)", rej)
	}
	if best.Lead.Item.ID != "a-declared" {
		t.Fatalf("축을 안 읽었는데 순서가 바뀌었다 — 선두가 %q 다", best.Lead.Item.ID)
	}
	if best.CloseDeclared {
		t.Fatalf("축을 안 읽었는데 강등으로 찍혔다")
	}
	if strings.Contains(best.Reason, "종료 선언") {
		t.Fatalf("축을 안 읽었는데 Reason 이 선언을 말한다: %q", best.Reason)
	}
	for _, r := range rej {
		if strings.Contains(r.Detail, "종료 선언") {
			t.Fatalf("축을 안 읽었는데 원장에 근거가 남았다: %q", r.Detail)
		}
	}
}

// 키는 있는데 수가 0인 선언은 강등하지 않는다.
//
// ★ 지금 store 가 그런 항목을 안 만들지만, 이 필드는 판정의 **입력 계약**이고
// 계약은 호출부 하나가 지금 어떻게 짜여 있는지와 별개로 서 있어야 한다
// (TestEmptyResourceHolderDoesNotBlock 이 같은 이유로 서 있다). 0건을 강등으로
// 읽으면 "선언이 있었다"가 아니라 "맵에 키가 있었다"가 큐 순서를 바꾼다.
func TestEligibleBundleZeroCountCloseDeclarationDoesNotDemote(t *testing.T) {
	best, rej := EligibleBundle(EligibleInput{
		Self:       "S1",
		Candidates: []Candidate{cand("a-declared", 0, nil), cand("z-clean", 0, nil)},
		CloseDeclarations: map[string]model.CloseDeclaration{
			"a-declared": {Last: t0, LastSession: "S-EMPTY"}, // Count()==0
		},
		CloseDeclarationsRead: true,
	}, SiblingIndex{})
	if best == nil {
		t.Fatalf("추천이 nil 이다(사유 %v)", rej)
	}
	if best.Lead.Item.ID != "a-declared" || best.CloseDeclared {
		t.Fatalf("0건 선언으로 강등했다 — 선두 %q, CloseDeclared=%v", best.Lead.Item.ID, best.CloseDeclared)
	}
}
```

Run: `go test ./internal/judge/ -run 'CloseDeclar' -v -count=1`

Expected: 컴파일 실패. `unknown field CloseDeclarations in struct literal of type EligibleInput` · `unknown field CloseDeclarationsRead in struct literal of type EligibleInput`.

- [ ] **Step 3: EligibleInput 에 필드 둘을 더한다**

`internal/judge/eligible.go:95-96`. before(정확히 이 두 줄):

```go
	Now time.Time
}
```

after:

```go
	Now time.Time
	// CloseDeclarations 는 항목 id → 그 항목을 닫으려다 롤백된 선언이다.
	//
	// ★ 여기 담긴 수는 **하한이다.** 원장에 안 써진 마무리가 있을 수 있다.
	// 없는 키와 Count()==0 은 둘 다 "강등하지 않는다"로 접힌다.
	CloseDeclarations map[string]model.CloseDeclaration
	// CloseDeclarationsRead 가 false 면 이 축이 **아예 안 돈다**.
	//
	// ★ nil 맵을 "안 읽음"으로 쓰지 않는다. 같은 구조체의 HeldResources 가
	// "비어 있으면 아무도 안 쥠"이라는 **정반대** 계약이라, 한 구조체에 nil 의 뜻이
	// 반대인 맵 둘이 나란히 서게 된다. 그리고 Go 의 nil 맵 조회는 zero 를 내므로
	// nil 과 빈 맵이 바이트 단위로 같은 출력이 되어, 순수 함수 시험이 두 상태를
	// 가를 관측점을 하나도 못 갖는다. service/pick.go 의 siblingIndex 가 같은 이유로
	// (값, bool) 두 반환값을 골랐다 — 그 모양을 그대로 쓴다.
	CloseDeclarationsRead bool
}
```

`eligible.go` 는 `model` 을 이미 import 한다(8행).

Run: `go test ./internal/judge/ -run 'CloseDeclar' -v -count=1`

Expected: 컴파일 통과. **정확히 두 개가 붉다**:
--- FAIL: TestEligibleBundleMarksCloseDeclared ("선언이 있는데 CloseDeclared 가 false 다 — 배선이 끊겼다")
--- FAIL: TestEligibleBundleCloseDeclaredDemotesButDoesNotDrop ("선두가 \"a-declared\" 다 — 선언이 붙은 a-declared 가 밀렸어야 한다")
나머지 여섯(태스크 1 의 넷 + WithoutRead + ZeroCount)은 PASS. 뒤의 둘이 지금 초록인 것은 정상이다 — 그 둘은 **축이 안 도는 것**을 지키는 가드다.

- [ ] **Step 4: 헬퍼 둘 + EligibleBundle 이 선두에 찍는 블록**

**① 헬퍼 둘.** `internal/judge/bundle.go:290`, `// bundleAround 는 선두 하나를 중심으로 직접 이웃만 모은다.` **바로 앞**에 넣는다(그 문자열은 파일에 한 번뿐이다). 앵커로 쓸 before 한 줄:

```go
// bundleAround 는 선두 하나를 중심으로 직접 이웃만 모은다.
```

after:

```go
// closeDeclarationOf 는 이 항목의 종료 선언을 낸다.
// 두 번째 반환값이 false 면 **강등하지 않는다** — 축을 안 읽었거나
// (CloseDeclarationsRead=false), 이 항목에 선언이 없거나, 키는 있는데 수가 0인
// 세 경우가 전부 여기로 접힌다. 세 경우의 처분이 같으므로 접는 것이 맞다.
func closeDeclarationOf(in EligibleInput, id string) (model.CloseDeclaration, bool) {
	if !in.CloseDeclarationsRead {
		return model.CloseDeclaration{}, false
	}
	d, ok := in.CloseDeclarations[id]
	if !ok || d.Count() == 0 {
		return model.CloseDeclaration{}, false
	}
	return d, true
}

// closeDeclaredDetail 은 강등 근거 한 조각이다. Bundle.Reason 과 RejectNotTop 의
// Detail 이 이 **한 문자열**을 함께 쓴다 — 두 자리에서 따로 조립하면 화면이 말하는
// 이유와 원장이 남기는 이유가 조용히 갈린다.
//
// ★ 수는 **하한이다.** flushDeferred 는 트랜잭션이 물고 있던 ctx 를 그대로 쓰고
// LogEvent 는 쓰기 실패를 WARN 으로만 삼키므로, 클라이언트가 끊긴 마무리는 원장에
// 아예 안 남는다. 문구가 "이상"이라고 말하는 이유가 그것이다 — 정확한 수로 읽히면
// "0건이니 안전하다"가 관측이 아니라 추측이 된다.
//
// ★ mode 를 안 합친다. done 은 "이미 랜딩됐을 수 있다"이고 dropped 는 "이미 버리기로
// 판정됐을 수 있다"라 **처방이 갈린다**(실측 383건 중 dropped 76건, 20%).
// 합치면 사람이 무엇을 확인해야 하는지가 문장에서 사라진다.
//
// Last·LastSession·LastMode 는 store 가 실제 행에서 읽은 값이다 — 못 읽은 행은
// 애초에 안 센다(CloseDeclarationsByItem 의 계약). 그래서 여기서 zero 를 따로
// 방어하지 않는다. 방어하면 "관측했는데 비었다"와 "안 셌다"가 다시 한 값으로 접힌다.
func closeDeclaredDetail(d model.CloseDeclaration) string {
	verdict := "이미 끝난 일일 수 있다"
	switch d.LastMode {
	case "done":
		verdict = "이미 랜딩됐을 수 있다"
	case "dropped":
		verdict = "이미 버리기로 판정됐을 수 있다"
	}
	return fmt.Sprintf("종료 선언 %d건 이상(done %d · dropped %d · 마지막 %s 세션 %s mode=%s) — %s. 연결된 판단부터 읽어라",
		d.Count(), d.Done, d.Dropped,
		d.Last.UTC().Format("2006-01-02 15:04:05"), d.LastSession, d.LastMode, verdict)
}

// bundleAround 는 선두 하나를 중심으로 직접 이웃만 모은다.
```

**② EligibleBundle 블록.** `internal/judge/bundle.go:249-253`, 기아 덧붙임 **바로 뒤**다. before:

```go
				b.Reason += fmt.Sprintf(" · ★기아 %s 경과(임계 %s) — 묶음 크기보다 먼저 본다",
					age.Round(time.Minute), StarvationAge)
			}
		}
		bundles = append(bundles, b)
```

after:

```go
				b.Reason += fmt.Sprintf(" · ★기아 %s 경과(임계 %s) — 묶음 크기보다 먼저 본다",
					age.Round(time.Minute), StarvationAge)
			}
		}
		// 종료 선언 판정도 여기서만 한다 — bundleAround 는 원장을 안 받는 순수 조립이다.
		// CloseDeclarationsRead 가 false 면 블록을 통째로 건너뛴다(Now.IsZero() 가 기아를
		// 건너뛰는 것과 같은 모양). 보는 것은 **선두 하나**다: 이 축은 "이 항목을 지금
		// 새로 집어도 되나"에 답하고, 그 질문의 주어는 브랜치를 받는 선두다.
		if d, ok := closeDeclarationOf(in, lead.Item.ID); ok {
			b.CloseDeclared = true
			b.CloseDeclaredDetail = closeDeclaredDetail(d)
			b.Reason += " · ★" + b.CloseDeclaredDetail
		}
		bundles = append(bundles, b)
```

`fmt` 은 이미 import 돼 있다.

Run: `go test ./internal/judge/ -run 'CloseDeclar' -v -count=1`

Expected: 여덟 시험 전부 PASS. Reason 실제 값 예: `의존자 합 0 · 묶음 1건 · 최고령 … · 선두 a-declared · ★종료 선언 1건 이상(done 1 · dropped 0 · 마지막 2026-08-01 08:00:00 세션 01KZ785T-OLD mode=done) — 이미 랜딩됐을 수 있다. 연결된 판단부터 읽어라`

- [ ] **Step 5: 변이 셋을 손으로 밟는다**

각각 넣었다 되돌린다. 사본에서 실측한 대응이다 — 어긋나면 축 격리가 안 된 것이다.

· `closeDeclarationOf` 의 `if !in.CloseDeclarationsRead { … }` 를 지운다 → `TestEligibleBundleWithoutCloseDeclarationsReadDoesNotDemote` **하나만** 붉다.
· `if !ok || d.Count() == 0 {` 를 `if !ok {` 로 바꾼다 → `TestEligibleBundleZeroCountCloseDeclarationDoesNotDemote` **하나만** 붉다.
· `b.Reason += " · ★" + b.CloseDeclaredDetail` 을 지운다 → `TestEligibleBundleMarksCloseDeclared` **하나만** 붉다("Reason 이 … 를 안 싣는다").

Run: `go test ./internal/judge/ -run 'CloseDeclar' -count=1`

Expected: 변이마다 지정된 시험 하나만 FAIL. 셋 다 되돌린 뒤 `ok`.

- [ ] **Step 6: 기존 소비자가 안 깨졌는지 — Reason 을 읽는 층까지 돈다**

Bundle.Reason 은 service 의 `BundleInfo.Reason` 으로, 거기서 mcpsrv 렌더로 그대로 흐른다. 이 축은 `CloseDeclarationsRead` 가 켜졌을 때만 문자열을 늘리고 지금 service 는 그 필드를 안 채우므로 제품 거동은 아직 안 바뀐다 — 그것을 시험으로 확인한다.

Run: `gofmt -l internal/judge internal/model && go vet ./... && go test ./internal/judge/ ./internal/service/ ./internal/mcpsrv/ ./internal/web/ -count=1`

Expected: gofmt·vet 출력 없음. `ok` 넷(judge · service · mcpsrv · web).

- [ ] **Step 7: 커밋**

```
feat(flightdeck): 판정이 원장의 종료 선언을 읽는다 — "못 읽었다"를 "0건"으로 안 접는다

EligibleBundle 이 선두의 종료 선언을 보고 Bundle 두 필드를 찍는다. 자리는
기아 판정과 같은 곳이다 — bundleAround 는 원장을 안 받는 순수 조립이라
시각도 원장도 이 한 자리에서만 들어온다.

nil 맵을 "안 읽음"으로 쓰지 않았다. 같은 구조체의 HeldResources 가 정반대
계약(비어 있으면 아무도 안 쥠)이라 nil 의 뜻이 반대인 맵 둘이 나란히 서게
되고, Go 의 nil 맵 조회는 zero 를 내므로 순수 함수 시험이 두 상태를 가를
관측점을 하나도 못 갖는다. 그래서 bool 을 따로 받는다.

문구는 수를 "N건 이상"으로 낸다. flushDeferred 가 트랜잭션 ctx 를 그대로
쓰므로 클라이언트가 끊긴 마무리는 원장에 안 남는다 — 이 수는 하한이고,
정확한 수로 읽히면 "0건이니 안전하다"가 관측이 아니라 추측이 된다.
done 과 dropped 는 처방이 갈려 합치지 않는다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
```

Run: `git add plugins/flightdeck/server/internal/judge plugins/flightdeck/server/internal/model && git commit -F -`

Expected: 커밋 1건. model/types.go 는 저장층 태스크가 이미 커밋했으면 diff 에 안 뜬다.

---

### Task 5: judge 원장 — RejectNotTop 이 이 축의 발화를 셀 수 있게 남긴다
**Files:**
- Modify: internal/judge/bundle.go (267-275)
- Modify: internal/judge/bundle_close_declared_test.go

**Interfaces:**
- Consumes: 태스크 2 의 `closeDeclarationOf(in EligibleInput, id string) (model.CloseDeclaration, bool)` 와 `closeDeclaredDetail(d model.CloseDeclaration) string`. 시험 파일의 `decl(done, dropped int, mode string)` 헬퍼와 strings·model import 도 태스크 2 가 이미 넣었다.
- Produces: model.Rejection{Reason: RejectNotTop} 의 Detail 이 **그 후보 자신**의 종료 선언 근거를 뒤에 단다. pick_eval 의 not-top 줄에서 `종료 선언 ` 을 세면 이 축이 실제로 몇 건을 밀어냈는지가 나온다. judge 층은 여기서 끝난다.

- [ ] **Step 1: 실패 시험 하나를 이어 붙인다**

`internal/judge/bundle_close_declared_test.go` 끝에 붙인다.

**왜 승자의 선언이 아니라 후보 자신의 것인가.** `fit` 의 모든 원소는 각자 묶음의 선두였다(EligibleBundle 이 후보 각각을 선두로 놓고 묶음을 만든다). 그러니 not-top 으로 남는 c 자신의 선언이 정확히 "이 축이 밀어낸 항목"이다.

```go
// 이 축이 **무엇을 몇 번 밀어냈는지**가 원장에 남는지 본다.
//
// ★ 안 남기면 pick_eval 의 not-top 줄이 "밀렸다"만 말하고 "왜"는 안 말한다.
// 그러면 이 축이 실제로 발화한 상태와 아예 안 도는 상태가 원장에서 같아 보이고,
// "조용히 버리는 것이 하나도 없다"가 형식만 지켜지고 목적은 안 지켜진다.
//
// ★ 대조를 함께 둔다 — 선언이 **없는** 항목의 not-top 줄에 이 조각이 붙으면,
// 원장에서 세는 수가 축의 발화 수가 아니라 그냥 not-top 수가 된다.
func TestEligibleBundleNotTopLedgersWhyCloseDeclared(t *testing.T) {
	in := EligibleInput{
		Self:                  "S1",
		Candidates:            []Candidate{cand("a-declared", 0, nil), cand("z-clean", 0, nil)},
		CloseDeclarations:     map[string]model.CloseDeclaration{"a-declared": decl(1, 0, "done")},
		CloseDeclarationsRead: true,
	}
	_, rej := EligibleBundle(in, SiblingIndex{})
	var detail string
	for _, r := range rej {
		if r.Item == "a-declared" && r.Reason == RejectNotTop {
			detail = r.Detail
		}
	}
	if detail == "" {
		t.Fatalf("전제가 깨졌다 — 강등된 항목의 not-top 줄이 없다: %v", rej)
	}
	// 기존 문장은 그대로 살아 있어야 한다. 덮어쓰면 "누구에게 밀렸나"가 사라진다.
	for _, want := range []string{"적격이지만 추천 묶음에 없다", "종료 선언 1건 이상", "mode=done"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("not-top 사유가 %q 를 안 싣는다: %q", want, detail)
		}
	}

	// 대조: 선언이 하나도 없으면 어느 not-top 줄에도 이 조각이 안 붙는다.
	clean := in
	clean.CloseDeclarations = map[string]model.CloseDeclaration{}
	_, rej2 := EligibleBundle(clean, SiblingIndex{})
	for _, r := range rej2 {
		if strings.Contains(r.Detail, "종료 선언") {
			t.Fatalf("선언이 없는데 근거가 붙었다(%s): %q", r.Item, r.Detail)
		}
	}
}
```

Run: `go test ./internal/judge/ -run TestEligibleBundleNotTopLedgersWhyCloseDeclared -v -count=1`

Expected: --- FAIL: TestEligibleBundleNotTopLedgersWhyCloseDeclared — `not-top 사유가 "종료 선언 1건 이상" 를 안 싣는다: "적격이지만 추천 묶음에 없다(추천 선두는 z-clean, 묶음 1건)"`

- [ ] **Step 2: RejectNotTop 의 Detail 에 같은 근거를 덧붙인다**

`internal/judge/bundle.go:271-274`. before:

```go
		rejByItem[c.Item.ID] = append(rejByItem[c.Item.ID], model.Rejection{
			Item: c.Item.ID, Reason: RejectNotTop,
			Detail: fmt.Sprintf("적격이지만 추천 묶음에 없다(추천 선두는 %s, 묶음 %d건)",
				best.Lead.Item.ID, len(best.Members)+1)})
```

after:

```go
		detail := fmt.Sprintf("적격이지만 추천 묶음에 없다(추천 선두는 %s, 묶음 %d건)",
			best.Lead.Item.ID, len(best.Members)+1)
		// ★ 이 축이 무엇을 몇 번 밀어냈는지는 여기에만 남는다. 안 남기면 pick_eval 의
		// not-top 줄이 "밀렸다"만 말하고 "왜"를 안 말해, 강등이 실제로 발화했는지를
		// 사후에 셀 방법이 하나도 없다 — 그러면 "조용히 버리는 것이 하나도 없다"가
		// 형식만 지켜지고 목적은 안 지켜진다.
		//
		// 싣는 것은 **이 후보 자신**의 선언이다(승자의 것이 아니다). fit 의 모든
		// 원소는 각자 묶음의 선두였으므로, 그것이 곧 이 축이 밀어낸 항목이다.
		if d, ok := closeDeclarationOf(in, c.Item.ID); ok {
			detail += " · " + closeDeclaredDetail(d)
		}
		rejByItem[c.Item.ID] = append(rejByItem[c.Item.ID],
			model.Rejection{Item: c.Item.ID, Reason: RejectNotTop, Detail: detail})
```

Run: `go test ./internal/judge/ -run 'CloseDeclar' -v -count=1`

Expected: 아홉 시험 전부 PASS(태스크 1 의 넷 + 태스크 2 의 넷 + 이번 하나).

- [ ] **Step 3: 변이 하나를 밟는다**

방금 넣은 `if d, ok := closeDeclarationOf(in, c.Item.ID); ok { … }` 세 줄을 지우고 돌린 뒤 되돌린다.

Run: `go test ./internal/judge/ -run 'CloseDeclar' -count=1`

Expected: `--- FAIL: TestEligibleBundleNotTopLedgersWhyCloseDeclared` 하나만 붉다. 되돌리면 `ok`.

- [ ] **Step 4: judge 층 전체 관문**

이 태스크로 judge 층이 끝난다. 패키지 전체 + 소비자 층 + 교차 빌드 관문을 한 번에 돈다. `go build` 는 _test.go 를 건너뛰므로 반드시 `go vet` 이다.

Run: `gofmt -l internal/judge && go vet ./... && go test ./internal/judge/ ./internal/service/ ./internal/mcpsrv/ ./internal/web/ ./internal/api/ -count=1`

Expected: gofmt·vet 출력 없음. `ok` 다섯.

- [ ] **Step 5: 커밋**

```
feat(flightdeck): 강등이 무엇을 밀어냈는지 원장에 남긴다 — 안 남기면 셀 방법이 없다

RejectNotTop 의 Detail 에 그 후보 자신의 종료 선언 근거를 덧붙인다.
안 남기면 pick_eval 의 not-top 줄이 "밀렸다"만 말하고 "왜"는 안 말해서,
이 축이 실제로 발화한 상태와 아예 안 도는 상태가 원장에서 같아 보인다 —
"조용히 버리는 것이 하나도 없다"가 형식만 지켜지고 목적은 안 지켜지는 자리다.

싣는 것은 승자의 선언이 아니라 후보 자신의 것이다. fit 의 모든 원소가
각자 묶음의 선두였으므로 그것이 곧 이 축이 밀어낸 항목이다.

문자열은 Reason 과 같은 조립부(closeDeclaredDetail)를 쓴다. 두 자리에서
따로 만들면 화면이 말하는 이유와 원장이 남기는 이유가 조용히 갈린다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
```

Run: `git add plugins/flightdeck/server/internal/judge && git commit -F -`

Expected: 커밋 1건. judge 층 완료 — 다음은 service 배선(closeDeclarations 가 이 두 필드를 채운다)이다.

---

### Task 6: S1. closeDeclarations / closeDeclaredOf — 원장의 수를 후보의 좌표로 거른다
**Files:**
- Modify: internal/service/pick.go (738-739 사이 — siblingIndex 의 닫는 괄호와 bundleScope 주석 사이에 함수 둘을 새로 판다)
- Test: internal/service/pick_wiring_test.go (3-11 import 블록 · 220 파일 끝에 헬퍼 둘 + 시험 하나)

**Interfaces:**
- Consumes: model 층: `type CloseDeclaration struct { Done, Dropped int; Last time.Time; LastSession, LastMode string }` 와 `func (d CloseDeclaration) Count() int`. store 층: `func (s *Store) CloseDeclarationsByItem(ctx context.Context, project string) (map[string]model.CloseDeclaration, error)` — 프로젝트 스코프로 event(kind='item.finish')를 긁어 항목별로 접은 **원자료**(앵커·존재 판정 없음).
- Produces: `func (s *Service) closeDeclarations(ctx context.Context, project string, cands []judge.Candidate) (map[string]model.CloseDeclaration, bool)` — 두 번째 값 false = 못 읽었다(맵은 nil). 성공하면 맵은 non-nil 이고 **후보에 있고 CreatedAt 이후인 선언만** 담는다.
`func closeDeclaredOf(m map[string]model.CloseDeclaration, id string, read bool) *model.CloseDeclaration` — read=false 면 nil, read=true 면 **언제나 non-nil**(없으면 zero 값 = 읽었고 0건).
시험 헬퍼: `seedCloseDeclaration(t, st, project, sessionID, itemID, mode string, at time.Time)` · `hideEvent(t, st)`.

- [ ] **Step 1: 시험 파일의 import 를 넓힌다**

pick_wiring_test.go:3-11 을 그대로 치환한다.

before:
```go
import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
)
```

after:
```go
import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)
```

Expected: `fmt`·`model` 은 기존 시험(182줄·35줄)이 계속 쓰므로 미사용 import 가 안 생긴다.

- [ ] **Step 2: 실패 시험 — 앵커와 좌표 관문을 표로 단정한다**

pick_wiring_test.go **맨 끝**(220줄 뒤)에 붙인다. 이 파일 13-20 의 규율 그대로다 — judge 시험도 render 시험도 service 가 무엇으로 그 구조체를 채우는지는 원리적으로 못 잰다.

```go
// seedCloseDeclaration 은 롤백된 종료 선언 하나를 원장에 **손으로** 심는다.
//
// ★ 왜 손으로 심나. 실물 원장에는 지금 `open`+`item.finish` 조합이 0건이다 — 두 번째
// finish 가 성공하면 항목이 done 이 되어 후보에서 아예 빠지기 때문이다. 실물 경로로는
// 이 상태를 못 밟는다. 그리고 앵커(항목 생성 **이전**의 이벤트)를 밟으려면 at 을 우리가
// 골라야 하는데 store.LogEvent 는 언제나 time.Now() 를 찍는다.
//
// 표기는 store 의 timeLayout 과 같아야 한다 — 폭 고정이라야 사전순 정렬이 시간순과
// 일치한다(그 상수는 store 안에 있어 여기서는 같은 문자열을 적는다).
func seedCloseDeclaration(t *testing.T, st *store.Store, project, sessionID, itemID, mode string, at time.Time) {
	t.Helper()
	payload := fmt.Sprintf(`{"item":%q,"mode":%q,"bytes":10300,"count":0}`, itemID, mode)
	if _, err := st.DB().ExecContext(ctx(),
		`INSERT INTO event(at, project, session_id, kind, payload) VALUES (?, ?, ?, 'item.finish', ?)`,
		at.UTC().Format("2006-01-02T15:04:05.000000Z"), project, sessionID, payload); err != nil {
		t.Fatalf("종료 선언 심기 실패(item=%s mode=%s): %v", itemID, mode, err)
	}
}

// hideEvent 는 원장 표를 **이름만 숨긴다**(hideJudgmentLink 과 같은 방식).
// 지우면 추가 전용 트리거까지 함께 흔들려 무엇이 실패했는지가 흐려진다.
func hideEvent(t *testing.T, st *store.Store) {
	t.Helper()
	if _, err := st.DB().ExecContext(ctx(),
		`ALTER TABLE event RENAME TO event_hidden`); err != nil {
		t.Fatalf("event 숨기기 실패: %v", err)
	}
}

// ⑦ 원장이 낸 수를 **그대로 안 믿는다** — 앵커와 존재 판정이 service 에서 걸린다.
//
// ★ 이 둘이 없으면 무엇이 깨지나. item 의 PK 가 (project, id) 라 지워졌다 다시 만들어진
// id 가 옛 화신의 선언을 물려받고, 실측 3건은 finish 를 친 프로젝트와 항목이 사는
// 프로젝트가 갈린 **좌표 오류**다 — 그것을 표류로 세면 이 축이 애먼 항목을 강등한다.
// 두 관문 다 store 가 아니라 여기 있다: candidates() 가 이미 items 를 손에 쥐고 있고,
// SQL 조인으로 하려면 json_extract 를 조인 조건에 넣어야 하는데 그 선례가 0건이다.
func TestCloseDeclarationsAnchorsOnCreationAndDropsNonCandidates(t *testing.T) {
	cases := []struct {
		name    string
		item    string // 빈 문자열이면 이 항목 자신
		offset  time.Duration
		wantHit bool
	}{
		{"생성 이후의 선언은 센다", "", time.Minute, true},
		{"생성 이전의 선언은 옛 화신의 것이라 안 센다", "", -time.Hour, false},
		{"생성과 같은 시각은 안 센다 — 애매한 쪽은 하한으로 접는다", "", 0, false},
		{"후보에 없는 id 의 선언은 좌표 어긋남이라 버린다", "ghost-item", time.Minute, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, st := newSvc(t)
			repo := newRepo(t)
			me := openSession(t, s, "p", repo, repo, "cc-1", "나")
			it := addItem(t, s, "p", "anchored", []string{"services/a.go"}, nil)

			id := c.item
			if id == "" {
				id = it.ID
			}
			seedCloseDeclaration(t, st, "p", me.Session.ID, id, "done", it.CreatedAt.Add(c.offset))

			got, read := s.closeDeclarations(ctx(), "p", []judge.Candidate{{Item: it}})
			if !read {
				t.Fatalf("원장을 읽을 수 있는데 못 읽었다고 한다: %+v", got)
			}
			d, hit := got[it.ID]
			if hit != c.wantHit {
				t.Fatalf("이 항목의 선언 유무가 %v 다(기대 %v) — 맵: %+v", hit, c.wantHit, got)
			}
			if c.wantHit && d.Count() != 1 {
				t.Fatalf("선언 수가 %d 다(기대 1): %+v", d.Count(), d)
			}
			if _, ghost := got["ghost-item"]; ghost {
				t.Fatalf("후보에 없는 id 가 맵에 남았다 — 좌표 오류를 표류로 셌다: %+v", got)
			}
		})
	}
}
```

- [ ] **Step 3: 실패 확인 (RED)**



Run: `go test ./internal/service/ -run TestCloseDeclarationsAnchorsOnCreationAndDropsNonCandidates -v -count=1`

Expected: 컴파일 실패: `s.closeDeclarations undefined (type *Service has no field or method closeDeclarations)`. 이것이 이 태스크의 RED 다 — 다른 오류(model.CloseDeclaration undefined 등)가 함께 나면 앞선 model/store 태스크가 아직 안 랜딩된 것이다.

- [ ] **Step 4: 최소 구현 — 함수 둘을 판다**

pick.go 에서 siblingIndex 의 닫는 괄호와 bundleScope 주석 사이를 치환한다.

before (pick.go:737-740, 공백 포함 정확히):
```go
	return judge.SiblingIndex(links), true
}

// bundleScope 는 Bundle.Scope 문장을 만든다. 순수 함수다 — 시험이 DB 없이 문구를 고정한다.
```

after:
```go
	return judge.SiblingIndex(links), true
}

// closeDeclarations 는 후보들에 걸린 **롤백된 종료 선언**을 항목별로 모은다.
//
// siblingIndex 와 같은 모양이다. 실패해도 pick 을 실패시키지 않는다 — 이 축 하나
// 때문에 추천을 통째로 잃는 것이 더 나쁘고, 축이 없어도 나머지 판정은 그대로 돈다.
// 못 읽은 사실은 **derive 에 안 넣는다**. 바로 위 siblingIndex 가 같은 함수에서 같은
// 판단을 이미 내렸다 — "못 읽은 사실은 derive 에 안 넣는다(derive 에 넣으면
// FreshnessOf 가 git 축을 낡음으로 접는다). 대신 로그에 남기고, **두 번째 반환값**으로
// 호출부에 그대로 넘긴다." pick 은 git 읽기가 실제로 도는 경로라 예외가 안 선다.
// 호출부는 그 bool 을 EligibleInput.CloseDeclarationsRead 와 Bundle.Scope 문장에
// 태워야 한다 — 안 그러면 "선언 0건"(진짜로 없다)과 "이 축을 아예 못 읽었다"가
// 같은 값(빈 맵)으로 접힌다.
//
// ★ 원장이 낸 수를 그대로 안 쓴다. 관문 둘을 **여기서** 건다(store 는 원장만 읽는다 —
// SQL 조인을 쓰면 json_extract 를 조인 조건에 넣어야 하는데 그 선례가 저장소에 0건이다):
//
//  1. **시간 앵커.** 항목의 CreatedAt **이후**의 선언만 남긴다. item 의 PK 가
//     (project, id) 라 지워졌다 다시 만들어진 id, 프로젝트를 옮겨 비워진 뒤 재사용된
//     id 가 옛 화신의 선언을 물려받는다. 두 값 다 이미 손에 있어 추가 조회가 없다.
//     ★ 앵커는 **접힌 값 단위**로 건다. store 가 항목별로 접어 주므로 여기 오는 시각은
//     마지막 선언(Last) 하나뿐이다 — 그것조차 생성 이전이면 그 접힘은 통째로 옛 화신의
//     것이라 버린다. 생성 시각을 걸치는 접힘은 남는데, 그 상태는 같은 id 가 지워졌다
//     다시 만들어진 **뒤에 또** 롤백된 finish 가 나야 성립한다(실측 0건). 그 갈래에서
//     수가 실제보다 커지는 것은 아는 한계다.
//     같은 시각은 **안 센다** — 항목이 있어야 닫을 수 있으니 동시각은 이 화신의 선언일
//     수 없고, 애매한 쪽은 하한으로 접는 것이 이 축의 규율이다.
//
//  2. **좌표 어긋남은 표류와 가른다.** 후보 목록에 없는 id 의 선언은 **버린다**.
//     실측 3건이 그 모양이다(context-platform 에서 친 finish 인데 항목은
//     kweiza-cc-plugins 에 있다 — fd-session-row-fanout·fd-ci-timing-baseline·
//     fd-prescribe-unclaimed-fires-after-finish). 그것은 좌표 오류지 표류가 아니다.
//
// ★ 이 수는 **하한이다.** flushDeferred 가 트랜잭션의 ctx 를 그대로 쓰고
// (store/store.go:366) LogEvent 는 쓰기 실패를 WARN 으로만 삼키므로(store/event.go:28-34),
// 원장에 안 써진 마무리가 있을 수 있다. 문구가 그렇게 말해야 한다.
func (s *Service) closeDeclarations(ctx context.Context, project string,
	cands []judge.Candidate) (map[string]model.CloseDeclaration, bool) {

	all, err := s.st.CloseDeclarationsByItem(ctx, project)
	if err != nil {
		s.log.WarnContext(ctx, "종료 선언 조회 실패 — 이 축 없이 판정한다",
			"project", clip(project, 64), "count", len(cands), "error", err.Error())
		return nil, false
	}
	out := make(map[string]model.CloseDeclaration, len(cands))
	for _, c := range cands {
		d, ok := all[c.Item.ID]
		if !ok || d.Count() == 0 {
			continue
		}
		if !d.Last.After(c.Item.CreatedAt) {
			continue
		}
		out[c.Item.ID] = d
	}
	return out, true
}

// closeDeclaredOf 는 항목 하나의 종료 선언을 응답에 실을 포인터로 바꾼다. 순수 함수다.
//
// ★ **포인터의 뜻은 PathCheck 의 규약 그대로다**(PickResult.PathCheck 의 주석):
// nil 은 "이 응답은 그 축을 안 읽었다"이고, 그 상태가 실제로 난다 — 구서버 + 신
// 클라이언트, 그리고 이 필드가 생기기 전에 굳은 오프라인 캐시가 그것을 만든다.
// 그래서 **읽었으면 선언이 0건이어도 non-nil 을 싣는다**(zero 값 = 읽었고 0건).
// 값 타입이나 "있을 때만 채움"으로 두면 그 두 상태가 한 값으로 접히고, 그러면
// 원장을 못 읽은 응답이 "이 항목은 깨끗하다"를 관측 없이 단정한다 —
// checkItemPaths 가 "절대 nil 을 돌려주지 않는다"로 선 것과 같은 자리다.
//
// read 가 false 면 맵을 아예 안 본다. 그때 못 읽었다는 사실은 Bundle.Scope 가 말한다
// (bundleScope 의 closeRead 인자) — 항목마다 같은 고백을 반복하지 않는다.
func closeDeclaredOf(m map[string]model.CloseDeclaration, id string, read bool) *model.CloseDeclaration {
	if !read {
		return nil
	}
	d := m[id] // 없으면 zero — "읽었고 0건"이다. 맵 원소의 주소는 못 잡으므로 복사본을 낸다
	return &d
}

// bundleScope 는 Bundle.Scope 문장을 만든다. 순수 함수다 — 시험이 DB 없이 문구를 고정한다.
```

★ `closeDeclaredOf` 는 이 태스크에서 아직 호출부가 없다 — Go 는 미사용 **함수**를 오류로 안 낸다(미사용 지역 변수만 오류다). S3 가 쓴다.

- [ ] **Step 5: 통과 확인 (GREEN) + 전 패키지 회귀**



Run: `go test ./internal/service/ -run TestCloseDeclarationsAnchorsOnCreationAndDropsNonCandidates -v -count=1 && go test ./internal/service/ -count=1 && go vet ./...`

Expected: 네 하위 시험 전부 PASS(--- PASS: …/생성_이후의_선언은_센다 등). 패키지 전체 ok. go vet 무출력.

- [ ] **Step 6: 커밋**

커밋 메시지:

```
feat(flightdeck): 롤백된 종료 선언을 후보의 좌표로 앵커한다 — 원장의 수를 그대로 안 믿는다

item.finish 가 있는데 항목이 done/dropped 이 아니면 그 시도는 롤백된 것이다.
그 사실은 08-04 부터 원장에 있었고 08-05 22:54 의 pick 이 그것을 1순위로 냈다 —
아무도 읽지 않았을 뿐이다.

store 는 원장만 읽는다. 앵커(항목 CreatedAt 이후)와 존재 판정(후보에 없는 id 는
버린다)은 여기서 건다 — candidates() 가 이미 items 를 손에 쥐고 있고, SQL 조인으로
하려면 json_extract 를 조인 조건에 넣어야 하는데 그 선례가 저장소에 0건이다.
좌표 어긋남 실측 3건은 표류가 아니라 좌표 오류라 강등의 근거가 못 된다.

못 읽은 사실은 derive 에 안 넣는다 — siblingIndex 가 같은 함수에서 같은 판단을
이미 내렸다(derive 에 넣으면 FreshnessOf 가 git 축을 낡음으로 접는다).

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
```

Run: `git add -A plugins/flightdeck/server/internal/service && git status --short`

Expected: pick.go · pick_wiring_test.go 둘만 스테이지된다.

---

### Task 7: S2. 배선 — EligibleInput 에 싣고, 못 읽으면 Bundle.Scope 가 말한다
**Files:**
- Modify: internal/service/pick.go (746-759 bundleScope · 775-782 EligibleBundle 호출 · 831-834 BundleInfo 조립)
- Modify: internal/service/pick_test.go (1131 · 1144 · 1148 · 2001 — bundleScope 호출부 넷 + 새 순수 시험 하나)
- Test: internal/service/pick_wiring_test.go (파일 끝에 시험 둘)

**Interfaces:**
- Consumes: S1 의 `s.closeDeclarations(ctx, project, cands) (map[string]model.CloseDeclaration, bool)`.
judge 층: `EligibleInput.CloseDeclarations map[string]model.CloseDeclaration` · `EligibleInput.CloseDeclarationsRead bool` · `Bundle.CloseDeclared bool` · `Bundle.CloseDeclaredDetail string` · `lessBundle` 의 `if a.CloseDeclared != b.CloseDeclared { return !a.CloseDeclared }` 축(Starved 바로 아래, 굶김 전용 갈래보다 위).
- Produces: `func bundleScope(total int, sibRead, closeRead bool) string` — 인자 셋. `pickRecommend` 안에 지역 변수 `closed map[string]model.CloseDeclaration` 과 `closeRead bool` 이 생긴다(S3 가 그대로 쓴다).

- [ ] **Step 1: 실패 시험 ⑧ — service 가 이 축을 judge 에 실제로 먹인다**

pick_wiring_test.go 맨 끝에 붙인다.

★ 기준선을 실측으로 잡아 뒀다: 선언이 없으면 `a-rolled-back`(먼저 만든 쪽)이 1순위다
(`의존자 합 0 · 묶음 1건 · 최고령 … · 선두 a-rolled-back`). 그래서 이 시험이 초록이 되려면
**나이 축을 이기는 새 축이 실제로 판정에 들어가야만** 한다.

```go
// ⑧ pickRecommend 가 EligibleInput 에 종료 선언 맵을 **실제로** 싣는다.
//
// ★ judge 시험은 EligibleInput 을 손으로 조립하므로 이 배선을 원리적으로 못 잰다.
// 그리고 이 축은 관측 가능한 출력이 순위 하나뿐이다 — 그래서 나이 축이 반대편을
// 가리키도록 깔았다: 선언된 쪽을 **먼저** 만들어(최고령) 축이 안 물리면 그쪽이 이긴다.
// 실측 기준선이 정확히 그 값이다(선두 a-rolled-back).
func TestPickRecommendDemotesTheItemWhoseCloseWasRolledBack(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	older := addItem(t, s, "p", "a-rolled-back", []string{"services/a.go"}, nil)
	addItem(t, s, "p", "b-clean", []string{"services/b.go"}, nil)
	seedCloseDeclaration(t, st, "p", me.Session.ID, older.ID, "done", older.CreatedAt.Add(time.Minute))

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("pick 실패: %v", err)
	}
	if res.Mode != PickRecommended || res.Item == nil {
		t.Fatalf("사전 조건이 깨졌다 — 추천 경로여야 한다: mode=%q item=%+v", res.Mode, res.Item)
	}
	if res.Item.ID != "b-clean" {
		t.Fatalf("닫히려다 롤백된 항목이 여전히 1순위다 — service 가 이 축을 judge 에 안 먹였다: 선두=%q", res.Item.ID)
	}

	// 상시 점등이면 판별력이 0이다. 반대 방향을 짝으로 못박는다.
	t.Run("선언이 없으면 나이순 그대로다", func(t *testing.T) {
		s, _ := newSvc(t)
		repo := newRepo(t)
		me := openSession(t, s, "p", repo, repo, "cc-1", "나")
		addItem(t, s, "p", "a-rolled-back", []string{"services/a.go"}, nil)
		addItem(t, s, "p", "b-clean", []string{"services/b.go"}, nil)

		res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
		if err != nil {
			t.Fatalf("pick 실패: %v", err)
		}
		if res.Item.ID != "a-rolled-back" {
			t.Fatalf("선언이 하나도 없는데 순서가 뒤집혔다 — 이 축이 상시 점등이면 판별력이 0이다: 선두=%q", res.Item.ID)
		}
	})
}
```

- [ ] **Step 2: 실패 시험 ⑨ — 못 읽으면 고백하되 derive 는 안 건드린다**

이어서 붙인다.

★ 실패 주입 이음매를 실측으로 확인해 뒀다: `event` 표를 이름만 숨겨도 pick 은 안 죽고
`res.Failures` 는 빈 채로 나온다(item.pick LogEvent 는 WARN 으로 삼켜진다).
그래서 `len(res.Failures) != 0` 이 이 갈래에서 정확한 단정이다.

```go
// ⑨ 원장을 못 읽으면 **그 사실이 Scope 에 남고**, derive 에는 안 들어간다.
//
// ★ derive 에 넣으면 무엇이 깨지나: FreshnessOf 가 failures>0 을 **git 축** Stale 로
// 접기 때문에, 원장 카운트 한 번이 실패했을 뿐인데 세션이 브랜치·HEAD·조상 판정이
// 낡았다고 읽는다. pick.go 의 siblingIndex 가 같은 판단을 이미 내려 뒀다.
//
// ★ 반대로 침묵도 안 된다. 안 남기면 이 순위가 "롤백된 항목이 진짜로 없다"인지
// "그 축을 아예 못 봤다"인지 응답만으로 못 가른다.
func TestPickRecommendConfessesUnreadCloseAxisWithoutFoldingItIntoDerive(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	addItem(t, s, "p", "solo", []string{"services/a.go"}, nil)

	hideEvent(t, st)

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("원장을 못 읽는다고 추천을 통째로 버렸다: %v", err)
	}
	if res.Mode != PickRecommended || res.Item == nil || res.Item.ID != "solo" {
		t.Fatalf("추천이 안 실렸다 — mode=%q item=%+v", res.Mode, res.Item)
	}
	if res.Bundle == nil {
		t.Fatal("묶음 축이 nil 이다")
	}
	if !strings.Contains(res.Bundle.Scope, "item.finish") {
		t.Fatalf("종료 선언 축을 못 읽었다는 고백이 Scope 에 없다: %q", res.Bundle.Scope)
	}
	if len(res.Failures) != 0 {
		t.Fatalf("종료 선언 축의 실패를 derive 에 실었다 — FreshnessOf 가 git 축을 낡음으로 접는다: %+v", res.Failures)
	}
}
```

- [ ] **Step 3: 실패 확인 (RED)**



Run: `go test ./internal/service/ -run 'TestPickRecommendDemotesTheItemWhoseCloseWasRolledBack|TestPickRecommendConfessesUnreadCloseAxisWithoutFoldingItIntoDerive' -v -count=1`

Expected: 컴파일은 된다(둘 다 기존 심볼만 쓴다). 단정 실패 둘:
· `닫히려다 롤백된 항목이 여전히 1순위다 … 선두="a-rolled-back"`
· `종료 선언 축을 못 읽었다는 고백이 Scope 에 없다: "관찰한 후보는 전체 1건이다…"`
하위 시험 `선언이 없으면 나이순 그대로다` 는 지금도 PASS 여야 한다 — 아니면 기준선이 밀린 것이다.

- [ ] **Step 4: 최소 구현 ⓐ — bundleScope 에 축 하나를 더한다**

pick.go:746-759 을 치환한다.

before:
```go
// sibRead 가 false 면 형제 축을 못 읽었다는 사실을 문장에 남긴다. 키 부재를 값으로
// 접지 않는다는 전역 규율이 이 한 줄에서도 지켜져야 한다 — 안 남기면 이 묶음이
// "형제가 진짜로 없다"인지 "형제 축을 아예 못 봤다"인지 응답만으로 못 가른다.
func bundleScope(total int, sibRead bool) string {
	sc := fmt.Sprintf("관찰한 후보는 전체 %d건이다(적격 여부와 무관하게 센 수다). "+
		"그 중 선두와 형제·선행 축으로 **직접** 이어진 것만 묶었다(전이 없음)", total)
	if !sibRead {
		sc += " · 형제 축(같은 판단에 함께 걸린 형제)은 이번에 못 읽었다 — " +
			"이 묶음은 선행·경로 축만 보고 나온 결과다"
	}
	return sc
}
```

after:
```go
// sibRead 가 false 면 형제 축을 못 읽었다는 사실을 문장에 남긴다. 키 부재를 값으로
// 접지 않는다는 전역 규율이 이 한 줄에서도 지켜져야 한다 — 안 남기면 이 묶음이
// "형제가 진짜로 없다"인지 "형제 축을 아예 못 봤다"인지 응답만으로 못 가른다.
//
// closeRead 도 같은 규율이고 **따로 적는다.** 하나로 뭉치면 "형제는 읽었고 종료 선언만
// 못 읽었다"가 화면에서 "둘 다 못 읽었다"와 같아진다. 이쪽은 항목마다 낼 수도 있지만
// 그러면 같은 고백이 후보 수만큼 반복되므로, 축의 상태는 축의 자리(범위 문장)에서
// 한 번만 말한다 — 항목별 값은 PickResult.CloseDeclared 가 나른다.
func bundleScope(total int, sibRead, closeRead bool) string {
	sc := fmt.Sprintf("관찰한 후보는 전체 %d건이다(적격 여부와 무관하게 센 수다). "+
		"그 중 선두와 형제·선행 축으로 **직접** 이어진 것만 묶었다(전이 없음)", total)
	if !sibRead {
		sc += " · 형제 축(같은 판단에 함께 걸린 형제)은 이번에 못 읽었다 — " +
			"이 묶음은 선행·경로 축만 보고 나온 결과다"
	}
	if !closeRead {
		sc += " · 이 후보들이 이미 닫히려다 롤백된 적이 있는지(원장의 item.finish)는 " +
			"이번에 못 읽었다 — 이 순위는 그 축 없이 나온 결과다"
	}
	return sc
}
```

- [ ] **Step 5: 최소 구현 ⓑ — pickRecommend 가 맵을 읽어 판정에 싣는다**

pick.go:775-782 을 치환한다.

before:
```go
	sib, sibRead := s.siblingIndex(ctx, proj.ID, cands)
	best, rejected := judge.EligibleBundle(judge.EligibleInput{
		Self: in.SessionID, SelfCC: selfCC, Candidates: cands, Live: live, Facts: facts, HeldResources: held,
		// Now 는 기아 축(judge.StarvationAge)에만 쓴다. 주입된 시계를 그대로 넘긴다 —
		// 여기서 time.Now() 를 부르면 시험이 가짜 시계를 밀어도 이 축만 실시계로
		// 판정한다(fd-lane-timestamps-ignore-injected-clock 이 고발한 그 모양이다).
		Now: now,
	}, sib)
```

after:
```go
	sib, sibRead := s.siblingIndex(ctx, proj.ID, cands)
	closed, closeRead := s.closeDeclarations(ctx, proj.ID, cands)
	best, rejected := judge.EligibleBundle(judge.EligibleInput{
		Self: in.SessionID, SelfCC: selfCC, Candidates: cands, Live: live, Facts: facts, HeldResources: held,
		// ★ 맵과 bool 을 **함께** 싣는다. 빈 맵 하나로 접으면 "선언 0건"과 "이 축을 아예
		// 못 읽었다"가 judge 안에서 같은 값이 되고, Go 의 nil 맵 조회는 zero 를 내므로
		// 순수 함수 시험이 두 상태를 가를 관측점을 하나도 못 갖는다. 같은 구조체의
		// HeldResources 가 "비어 있으면 아무도 안 쥠"이라는 정반대 계약이라 nil 을
		// "안 읽음"으로 재활용할 수도 없다.
		CloseDeclarations:     closed,
		CloseDeclarationsRead: closeRead,
		// Now 는 기아 축(judge.StarvationAge)에만 쓴다. 주입된 시계를 그대로 넘긴다 —
		// 여기서 time.Now() 를 부르면 시험이 가짜 시계를 밀어도 이 축만 실시계로
		// 판정한다(fd-lane-timestamps-ignore-injected-clock 이 고발한 그 모양이다).
		Now: now,
	}, sib)
```

그리고 pick.go:831-834 의 BundleInfo 조립에서 Scope 인자를 늘린다.

before:
```go
	res.Bundle = &BundleInfo{
		Reason: best.Reason,
		Scope:  bundleScope(len(cands), sibRead),
	}
```

after:
```go
	res.Bundle = &BundleInfo{
		Reason: best.Reason,
		Scope:  bundleScope(len(cands), sibRead, closeRead),
	}
```

- [ ] **Step 6: 최소 구현 ⓒ — 기존 순수 시험의 호출부 넷을 고치고 축 하나를 새로 잠근다**

pick_test.go:1131 · 1144 · 1148 · 2001 이 옛 시그니처를 부른다. 넷 다 안 고치면 패키지가 컴파일 안 된다.

① pick_test.go:1131
before: `	got := bundleScope(5, true)`
after:  `	got := bundleScope(5, true, true)`

② pick_test.go:1144-1152 를 통째로 치환하고 새 시험을 이어 붙인다.

before:
```go
	read := bundleScope(5, true)
	if strings.Contains(read, "못 읽") {
		t.Fatalf("다 읽었는데 못 읽었다고 말한다: %q", read)
	}
	unread := bundleScope(5, false)
	if !strings.Contains(unread, "못 읽") {
		t.Fatalf("형제 축을 못 읽었다는 사실이 문장에 없다: %q", unread)
	}
}
```

after:
```go
	read := bundleScope(5, true, true)
	if strings.Contains(read, "못 읽") {
		t.Fatalf("다 읽었는데 못 읽었다고 말한다: %q", read)
	}
	unread := bundleScope(5, false, true)
	if !strings.Contains(unread, "못 읽") {
		t.Fatalf("형제 축을 못 읽었다는 사실이 문장에 없다: %q", unread)
	}
}

// bundleScope 는 **종료 선언 축**을 못 읽었다는 사실도 따로 남긴다.
//
// ★ 축마다 따로 적는다. 하나로 뭉치면 "형제는 읽었고 종료 선언만 못 읽었다"가
// 화면에서 "둘 다 못 읽었다"와 같아지고, 그러면 이 순위를 얼마나 믿어도 되는지가
// 응답만으로 안 갈린다. 두 축은 서로 다른 표를 읽으므로 실제로 따로 죽는다
// (judgment_link vs event).
func TestBundleScopeNamesUnreadCloseDeclarationAxis(t *testing.T) {
	read := bundleScope(5, true, true)
	if strings.Contains(read, "item.finish") {
		t.Fatalf("다 읽었는데 종료 선언 축을 못 읽었다고 말한다: %q", read)
	}
	unread := bundleScope(5, true, false)
	if !strings.Contains(unread, "item.finish") {
		t.Fatalf("종료 선언 축을 못 읽었다는 사실이 문장에 없다: %q", unread)
	}
	if strings.Contains(unread, "형제 축") {
		t.Fatalf("종료 선언 축만 못 읽었는데 형제 축까지 고백한다: %q", unread)
	}
}
```

③ pick_test.go:2001
before: `	if want := bundleScope(2, true); res.Bundle.Scope != want {`
after:  `	if want := bundleScope(2, true, true); res.Bundle.Scope != want {`

★ ③ 은 문장 **전체**를 실제 응답과 맞추는 단정이라, 이 한 줄이 "pickRecommend 가
closeRead 자리에 상수를 박지 않았다"까지 함께 잠근다.

- [ ] **Step 7: 통과 확인 (GREEN) + 전 패키지 회귀**



Run: `go test ./internal/service/ -run 'TestPickRecommendDemotesTheItemWhoseCloseWasRolledBack|TestPickRecommendConfessesUnreadCloseAxisWithoutFoldingItIntoDerive|TestBundleScope|TestPickRecommendScopeDoesNotConfessAnAxisItRead|TestPickBundleScopeReflectsRealCandidateCount' -v -count=1 && go test ./internal/service/ -count=1 && go vet ./...`

Expected: 전부 PASS. 특히 `TestPickRecommendScopeDoesNotConfessAnAxisItRead` 가 초록이어야 한다 — 그것이 closeRead 에 상수를 박는 변이를 잡는 자리다. 패키지 전체 ok, go vet 무출력.

- [ ] **Step 8: 커밋**

커밋 메시지:

```
feat(flightdeck): pick 이 종료 선언 축을 판정에 먹인다 — 못 읽으면 범위 문장이 그렇게 말한다

맵과 bool 을 함께 싣는다. 빈 맵 하나로 접으면 "선언 0건"과 "이 축을 아예 못 읽었다"가
judge 안에서 같은 값이 되고, Go 의 nil 맵 조회는 zero 를 내므로 순수 함수 시험이 두
상태를 가를 관측점을 하나도 못 갖는다. 같은 구조체의 HeldResources 가 정반대 계약이라
nil 을 "안 읽음"으로 재활용할 수도 없다.

배선 시험은 나이 축을 반대편으로 깔아 잡는다 — 선언된 쪽을 먼저 만들면 축이 안 물릴 때
그쪽이 1순위다(실측 기준선). 짝으로 "선언이 없으면 나이순 그대로"를 함께 못박아 상시
점등을 막는다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
```

Run: `git add -A plugins/flightdeck/server/internal/service && git status --short`

Expected: pick.go · pick_test.go · pick_wiring_test.go 셋만 스테이지된다.

---

### Task 8: S3. 표면 — 선두와 구성원 양쪽이 자기 종료 선언을 싣는다
**Files:**
- Modify: internal/service/pick.go (81-84 PickResult · 126-127 BundleMember · 820-823 선두 조립 · 835-841 구성원 루프)
- Test: internal/service/pick_wiring_test.go (파일 끝에 시험 둘)

**Interfaces:**
- Consumes: S1 의 `closeDeclaredOf(m, id, read) *model.CloseDeclaration`. S2 가 pickRecommend 안에 만든 지역 변수 `closed` · `closeRead`. 시험 헬퍼 `seedCloseDeclaration` · `hideEvent` · 기존 `makeSiblings`(pick_test.go:658).
- Produces: `PickResult.CloseDeclared *model.CloseDeclaration \`json:"close_declared,omitempty"\`` 와 `BundleMember.CloseDeclared *model.CloseDeclaration \`json:"close_declared,omitempty"\``. 계약: nil = 이 응답은 그 축을 안 읽었다(추천 아닌 갈래·구서버·옛 캐시) · non-nil 이고 Count()==0 = 읽었고 선언이 없다. mcpsrv 의 `renderCloseDeclared` 는 **Count()==0 이면 빈 문자열**을 내야 한다.

- [ ] **Step 1: 실패 시험 ⑩ — 선두. 세 상태를 다 가른다**

pick_wiring_test.go 맨 끝에 붙인다.

```go
// ⑩ **선두**가 자기 종료 선언을 싣는다 — 이 사고의 항목이 정확히 선두였다.
//
// ★ renderBundle 은 BundleInfo 하나만 받고 Members 는 정의상 선두 제외라 선두를
// 모른다. 구성원 자리에만 심으면 사고를 낳은 그 항목에 대해 응답이 침묵한다.
//
// ★ 세 상태를 다 잰다. nil 의 뜻이 "안 읽었다" 하나로 서려면 "읽었고 0건"이 반드시
// non-nil 이어야 하고, 그 짝이 없으면 원장을 못 읽은 응답이 관측 없이 "이 항목은
// 깨끗하다"를 단정하게 된다(checkItemPaths 가 절대 nil 을 안 내는 것과 같은 자리).
func TestPickResultCarriesCloseDeclarationForTheLead(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	it := addItem(t, s, "p", "solo", []string{"services/a.go"}, nil)
	seedCloseDeclaration(t, st, "p", me.Session.ID, it.ID, "done", it.CreatedAt.Add(time.Minute))

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("pick 실패: %v", err)
	}
	if res.CloseDeclared == nil {
		t.Fatal("선두의 종료 선언이 안 실렸다 — 사고를 낳은 그 항목이 정확히 선두다")
	}
	if res.CloseDeclared.Count() != 1 || res.CloseDeclared.Done != 1 {
		t.Fatalf("선두의 선언 수가 틀렸다: %+v", res.CloseDeclared)
	}
	if res.CloseDeclared.LastMode != "done" || res.CloseDeclared.LastSession != me.Session.ID {
		t.Fatalf("마지막 선언의 좌표(세션·mode)가 안 실렸다: %+v", res.CloseDeclared)
	}

	t.Run("선언이 없어도 읽었으면 non-nil 이다", func(t *testing.T) {
		s, _ := newSvc(t)
		repo := newRepo(t)
		me := openSession(t, s, "p", repo, repo, "cc-1", "나")
		addItem(t, s, "p", "solo", []string{"services/a.go"}, nil)

		res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
		if err != nil {
			t.Fatalf("pick 실패: %v", err)
		}
		if res.CloseDeclared == nil {
			t.Fatal("읽었는데 nil 이다 — nil 은 '이 축을 안 읽었다'라서 0건과 접히면 안 된다")
		}
		if res.CloseDeclared.Count() != 0 {
			t.Fatalf("선언이 없는데 수가 %d 다: %+v", res.CloseDeclared.Count(), res.CloseDeclared)
		}
	})

	t.Run("축을 못 읽었으면 nil 이다", func(t *testing.T) {
		s, st := newSvc(t)
		repo := newRepo(t)
		me := openSession(t, s, "p", repo, repo, "cc-1", "나")
		addItem(t, s, "p", "solo", []string{"services/a.go"}, nil)
		hideEvent(t, st)

		res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
		if err != nil {
			t.Fatalf("pick 실패: %v", err)
		}
		if res.CloseDeclared != nil {
			t.Fatalf("못 읽었는데 값을 실었다 — 관측한 적 없는 사실을 단정한다: %+v", res.CloseDeclared)
		}
	})
}
```

- [ ] **Step 2: 실패 시험 ⑪ — 구성원. 선두 것의 복사가 아니어야 한다**

이어서 붙인다.

★ 강등 축이 선두를 뒤집는 것을 이용해 **선두는 깨끗하고 구성원이 선언을 가진** 배치를
결정론적으로 만든다: 형제 둘 중 선언된 쪽(`a1-declared`)이 먼저 만들어지고 id 도 앞서므로,
축이 안 물리면 그쪽이 선두다 — 축이 물려야만 `z9-clean` 이 선두가 된다.

```go
// ⑪ **구성원**이 자기 종료 선언을 싣는다. 선두 것을 빌려주면 안 된다.
//
// ★ 값을 서로 다르게 깐다(구성원=dropped 1건, 선두=0건). 같은 값으로 깔면 선두 것을
// 그대로 복사하는 변이가 초록으로 지나간다 — 구성원 PathCheck 이 같은 함정을 이미
// 밟았고(TestPickBundleMemberPathCheckIsPerItemNotLead) 같은 방식으로 막는다.
func TestPickBundleMemberCarriesItsOwnCloseDeclaration(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	declared := addItem(t, s, "p", "a1-declared", []string{"services/a.go"}, nil)
	addItem(t, s, "p", "z9-clean", []string{"services/z.go"}, nil)
	makeSiblings(t, st, "p", "a1-declared", "z9-clean")
	seedCloseDeclaration(t, st, "p", me.Session.ID, declared.ID, "dropped", declared.CreatedAt.Add(time.Minute))

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID})
	if err != nil {
		t.Fatalf("pick 실패: %v", err)
	}
	if res.Bundle == nil || len(res.Bundle.Members) != 1 {
		t.Fatalf("사전 조건이 깨졌다 — 형제 하나가 구성원이어야 한다: %+v", res.Bundle)
	}
	if res.Item.ID != "z9-clean" || res.Bundle.Members[0].Item.ID != "a1-declared" {
		t.Fatalf("선언된 쪽이 여전히 선두다 — 선두=%q 구성원=%q",
			res.Item.ID, res.Bundle.Members[0].Item.ID)
	}
	m := res.Bundle.Members[0]
	if m.CloseDeclared == nil {
		t.Fatal("구성원의 종료 선언이 안 실렸다 — 화면이 그 항목에 대해 침묵한다")
	}
	if m.CloseDeclared.Count() != 1 || m.CloseDeclared.Dropped != 1 || m.CloseDeclared.LastMode != "dropped" {
		t.Fatalf("구성원의 선언이 자기 것이 아니다: %+v", m.CloseDeclared)
	}
	if res.CloseDeclared == nil || res.CloseDeclared.Count() != 0 {
		t.Fatalf("선두가 구성원의 선언을 받아 갔다: %+v", res.CloseDeclared)
	}
}
```

- [ ] **Step 3: 실패 확인 (RED)**



Run: `go test ./internal/service/ -run 'TestPickResultCarriesCloseDeclarationForTheLead|TestPickBundleMemberCarriesItsOwnCloseDeclaration' -v -count=1`

Expected: 컴파일 실패: `res.CloseDeclared undefined (type PickResult has no field or method CloseDeclared)` · `m.CloseDeclared undefined (type BundleMember has no field or method CloseDeclared)`.

- [ ] **Step 4: 최소 구현 ⓐ — PickResult 에 필드를 판다**

pick.go:81-84 을 치환한다.

before:
```go
	// 적격 0건(PickNone)에도 nil 이다 — 항목이 없으면 관측할 대상이 없다.
	PathCheck *judge.ItemPathVerdict `json:"path_check,omitempty"`

	// Bundle 은 이 응답이 낸 묶음이다.
```

after:
```go
	// 적격 0건(PickNone)에도 nil 이다 — 항목이 없으면 관측할 대상이 없다.
	PathCheck *judge.ItemPathVerdict `json:"path_check,omitempty"`

	// CloseDeclared 는 이 항목을 닫으려다 **롤백된** 선언이다(원장의 item.finish 인데
	// 항목은 done/dropped 이 아니다).
	//
	// ★ **포인터다.** PathCheck 과 같은 이유이고, 그 상태가 실제로 난다: 구서버 + 신
	// 클라이언트, 그리고 이 필드가 생기기 전에 굳은 오프라인 캐시가 그대로 재생된다.
	// 값 타입이면 그 상황이 "선언 0건"으로 접혀 **관측한 적 없는 사실을 단정한다** —
	// 하필 그 단정이 "이 항목은 깨끗하다"라서, 이 축이 막으려는 사고를 그대로 통과시킨다.
	//
	//	nil            = 이 응답은 그 축을 안 읽었다
	//	non-nil, 0건   = 읽었고 선언이 없다
	//	non-nil, n건   = 읽었고 n번 닫히려다 롤백됐다
	//
	// 왜 읽었는데 못 읽는 갈래가 있나 — 원장 조회가 실패했을 때다. 그 사실은 항목마다
	// 반복하지 않고 Bundle.Scope 가 한 번 말한다(bundleScope 의 closeRead).
	//
	// **추천 경로에서만 채운다.** item_id 지정 선점·재개(pickExplicit)는 후보 집합을
	// 안 만들어 이 축을 안 돌린다 — 거기서 nil 은 "안 읽었다"로 정확히 읽힌다.
	//
	// ★ 이 수는 **하한이다.** 원장에 안 써진 마무리가 있을 수 있다(store/store.go:366 의
	// flushDeferred 가 트랜잭션의 ctx 를 그대로 쓴다). 문구가 그렇게 말해야 한다.
	CloseDeclared *model.CloseDeclaration `json:"close_declared,omitempty"`

	// Bundle 은 이 응답이 낸 묶음이다.
```

- [ ] **Step 5: 최소 구현 ⓑ — BundleMember 에 필드를 판다**

pick.go:126-127 을 치환한다. **gofmt 정렬이 바뀌는 자리다** — 주석 줄이 정렬 구획을 끊으므로 `Item/Link/PathCheck` 은 그대로 두고 `CloseDeclared/Notes` 가 새 구획이 된다(gofmt 로 확인함).

before:
```go
	PathCheck *judge.ItemPathVerdict `json:"path_check,omitempty"`
	Notes     []model.Judgment       `json:"notes,omitempty"` // 집었을 때만 전문
```

after:
```go
	PathCheck *judge.ItemPathVerdict `json:"path_check,omitempty"`
	// CloseDeclared 는 이 구성원을 닫으려다 롤백된 선언이다. 계약은 PickResult 쪽과
	// 글자 그대로 같다(nil = 그 축을 안 읽었다 · non-nil 0건 = 읽었고 없다).
	//
	// ★ 선두와 **양쪽 다** 있어야 한다. renderBundle 은 BundleInfo 하나만 받고 Members
	// 는 정의상 선두 제외라 선두를 모르는데, 이 사고의 항목은 정확히 선두였다.
	CloseDeclared *model.CloseDeclaration `json:"close_declared,omitempty"`
	Notes         []model.Judgment        `json:"notes,omitempty"` // 집었을 때만 전문
```

- [ ] **Step 6: 최소 구현 ⓒ — 선두와 구성원에 값을 찍는다**

① 선두. pick.go:820-823 을 치환한다(앞 줄 `res.Overlaps = best.Lead.Overlaps` 를 함께 잡아야 유일해진다 — pickExplicit:337-340 에 같은 두 줄이 있다).

before:
```go
	res.Overlaps = best.Lead.Overlaps
	res.Setup = SetupCommands(proj.Path, proj.DefaultBranch, item.ID)
	res.PathCheck = s.checkItemPaths(ctx, proj, item.Paths)
	if res.Setup == nil {
```

after:
```go
	res.Overlaps = best.Lead.Overlaps
	res.Setup = SetupCommands(proj.Path, proj.DefaultBranch, item.ID)
	res.PathCheck = s.checkItemPaths(ctx, proj, item.Paths)
	// ★ 선두에도 싣는다. renderBundle 은 Members(선두 제외)만 받아 선두를 모르므로,
	// 구성원 자리에만 심으면 이 사고를 낳은 그 항목에 대해 응답이 통째로 침묵한다.
	res.CloseDeclared = closeDeclaredOf(closed, item.ID, closeRead)
	if res.Setup == nil {
```

② 구성원. pick.go:835-842 의 루프를 치환한다.

before:
```go
	for i, m := range best.Members {
		res.Bundle.Members = append(res.Bundle.Members, BundleMember{
			Item: m.Item, Link: best.Links[i],
			PathCheck: s.checkItemPaths(ctx, proj, m.Item.Paths),
			// Notes 는 안 싣는다 — 추천은 아직 안 집은 것이라
			// 후보마다 전문을 실으면 컨텍스트를 태운다(설계 §6).
		})
	}
```

after:
```go
	for i, m := range best.Members {
		res.Bundle.Members = append(res.Bundle.Members, BundleMember{
			Item: m.Item, Link: best.Links[i],
			PathCheck: s.checkItemPaths(ctx, proj, m.Item.Paths),
			// ★ 종료 선언도 구성원마다 **자기 것**을 싣는다. 합치거나 선두 것을 빌려주면
			// 화면이 엉뚱한 항목을 "이미 닫히려 했다"고 지목한다 — PathCheck 을 항목
			// 단위로 가른 것과 같은 이유다(둘 다 항목 단위 사실이다).
			CloseDeclared: closeDeclaredOf(closed, m.Item.ID, closeRead),
			// Notes 는 안 싣는다 — 추천은 아직 안 집은 것이라
			// 후보마다 전문을 실으면 컨텍스트를 태운다(설계 §6).
		})
	}
```

- [ ] **Step 7: 통과 확인 (GREEN) + 모듈 전수 회귀**



Run: `go test ./internal/service/ -run 'TestPickResultCarriesCloseDeclarationForTheLead|TestPickBundleMemberCarriesItsOwnCloseDeclaration' -v -count=1 && go test ./internal/... ./cmd/fd/ -count=1 && go vet ./...`

Expected: 두 시험과 하위 시험 전부 PASS. `TestGofmtGateCoversTheWholeModuleIncludingTests` 도 초록이어야 한다(위 코드는 전부 gofmt 로 검증했다). 모듈 전수 ok, go vet 무출력.

- [ ] **Step 8: 커밋**

커밋 메시지:

```
feat(flightdeck): 선두와 구성원 양쪽이 자기 종료 선언을 싣는다

renderBundle 은 BundleInfo 하나만 받고 Members 는 정의상 선두 제외라 선두를 모른다.
그런데 이 사고의 항목은 정확히 선두였다 — 구성원 자리에만 심으면 사고를 낳은 그
항목에 대해 응답이 통째로 침묵한다.

포인터의 뜻은 PathCheck 규약 그대로다: nil = 이 응답은 그 축을 안 읽었다.
그래서 읽었으면 0건이어도 non-nil 을 싣는다 — 값으로 접으면 원장을 못 읽은 응답이
"이 항목은 깨끗하다"를 관측 없이 단정하고, 하필 그 단정이 이 축이 막으려는 사고를
그대로 통과시킨다.

추천 경로에서만 채운다. item_id 지정 선점·재개는 후보 집합을 안 만들어 이 축을
안 돌리고, 거기서 nil 은 "안 읽었다"로 정확히 읽힌다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
```

Run: `git add -A plugins/flightdeck/server/internal/service && git status --short`

Expected: pick.go · pick_wiring_test.go 둘만 스테이지된다.

---

### Task 9: R1 — renderCloseDeclared 순수 함수: nil·0건·done·dropped 넷 다 말한다
**Files:**
- Create: plugins/flightdeck/server/internal/mcpsrv/render_close_declared_test.go
- Modify: plugins/flightdeck/server/internal/mcpsrv/render.go (renderPathCheck 끝 1301줄 바로 뒤, 1303줄 구분선 앞)

**Interfaces:**
- Consumes: model.CloseDeclaration{Done,Dropped int; Last time.Time; LastSession, LastMode string} + (d CloseDeclaration) Count() int — internal/model/types.go:319-342 에 **이미 있다**(확인함). 이 태스크는 service 층에 아무것도 안 기댄다.
- Produces: func renderCloseDeclared(d *model.CloseDeclaration, indent string) string — mcpsrv 패키지 비공개 순수 함수. 네 갈래 문구가 여기서 확정되고 R2·R3 가 그 문자열을 그대로 단정한다:
· nil       → indent+"종료 선언: 이 응답은 이 축을 안 읽었다 — 낡은 캐시이거나 서버가 이 축을 모르는 판이다.\n"
· Count()==0 → indent+"종료 선언: 원장에서 하나도 못 봤다 — 이 항목을 닫으려다 롤백된 시도가 관측되지 않았다.\n" + indent+11칸+"이 수는 하한이다 — 원장에 안 써진 마무리는 여기서 영영 0으로 보인다.\n"
· Count()>0  → indent+"종료 선언: 롤백된 마무리 선언 적어도 N건(done D · dropped P) — 마지막 YYYY-MM-DD HH:MM · 세션 <ShortID|미상> · mode=<LastMode>\n" + (Done>0 이면) indent+11칸+"done D건: 이미 랜딩됐을 수 있다.\n" + (Dropped>0 이면) indent+11칸+"dropped P건: 이미 버리기로 판정됐을 수 있다.\n" + indent+11칸+"연결된 판단부터 읽어라. 이 수는 하한이다 — 원장에 안 써진 마무리는 여기 안 잡힌다.\n"
모든 갈래가 개행으로 끝나고, 모든 줄에 indent 가 붙는다.

- [ ] **Step 1: 전제 확인 — model 타입이 이미 있는지**

이 태스크는 service 층에 안 기댄다. model 타입만 있으면 바로 빨강까지 갈 수 있다.
없다면 model 층 태스크가 먼저다.

Run: `grep -n "type CloseDeclaration struct" -A 8 internal/model/types.go && grep -n "func (d CloseDeclaration) Count" internal/model/types.go`

Expected: types.go:333 의 구조체 다섯 필드와 342줄의 Count 가 둘 다 출력된다.

- [ ] **Step 2: 실패 시험 — 새 파일 render_close_declared_test.go 를 만든다**

```go
package mcpsrv

import (
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 종료 선언 축 — **닫히지 못한 항목이 큐의 머리에 서는 것**을 화면이 말하는 자리다.
//
// 사고의 모양: 08-04 에 finish 가 선점 표류로 거절·롤백됐고(판단 본문 10300바이트가
// 통째로 죽었다) 그 사실이 원장에 그대로 남아 있는데, 08-05 의 pick 이 같은 항목을
// **1순위 선두**로 추천하면서 그 신호를 한 글자도 말하지 않았다. 이 파일의 시험은
// 전부 "응답이 그 사실을 말하는가"를 문자열 좌표계에서 잰다.

// TestRenderCloseDeclaredNeverStaysSilent 는 네 갈래가 **전부 말하는지**를 본다.
//
// ★ renderPathCheck 이 이상이 없어도 한 줄을 찍는 그 이유 그대로다 — 침묵하면
// "선언이 없다"와 "이 축을 안 봤다"가 같은 화면이 되고, 그러면 원장 조회가 통째로
// 실패한 날에도 pick 은 평소와 똑같아 보인다.
//
// ★ 처방이 mode 로 갈린다. done 은 이미 랜딩됐을 수 있고 dropped 는 이미 버리기로
// 판정됐을 수 있다 — 다음 세션이 확인할 것이 서로 다르다(랜딩 이력을 볼 것인가,
// 버린 판단을 읽을 것인가). 그래서 갈래마다 **그 갈래에만 있는 문구**를 단정하고,
// 다른 갈래의 문구가 새어 들어오지 않는 것도 같이 본다 — 둘을 맞바꾸는 변이는
// want 만으로는 안 죽는다.
//
// ★ 수는 **하한**이다. store 의 doc 이 못박은 계약이다(event.go:255-258:
// "소비자의 문구가 '정확히 N건'이 아니라 '적어도 N건'으로 말해야 한다").
// 0건 갈래에서도 그 말을 한다 — 0 이야말로 안 써진 마무리에 가장 잘 속는 값이다.
func TestRenderCloseDeclaredNeverStaysSilent(t *testing.T) {
	at := time.Date(2026, 8, 4, 23, 54, 37, 0, time.UTC)
	cases := []struct {
		name string
		d    *model.CloseDeclaration
		want []string
		deny []string
	}{
		{
			name: "축을 못 읽었다",
			d:    nil,
			want: []string{"종료 선언: ", "이 축을 안 읽었다"},
			deny: []string{"이미 랜딩됐을 수 있다", "이미 버리기로 판정됐을 수 있다", "하한"},
		},
		{
			name: "읽었는데 0건",
			d:    &model.CloseDeclaration{},
			want: []string{"종료 선언: ", "관측되지 않았다", "이 수는 하한이다"},
			deny: []string{"이 축을 안 읽었다", "이미 랜딩됐을 수 있다", "이미 버리기로 판정됐을 수 있다"},
		},
		{
			name: "done 선언",
			d: &model.CloseDeclaration{
				Done: 2, Last: at, LastSession: "01LEADSESSION", LastMode: "done",
			},
			want: []string{
				"종료 선언: 롤백된 마무리 선언 적어도 2건(done 2 · dropped 0)",
				"마지막 2026-08-04 23:54", "세션 01LEADSE…", "mode=done",
				"done 2건: 이미 랜딩됐을 수 있다.",
				"연결된 판단부터 읽어라.", "이 수는 하한이다",
			},
			deny: []string{"이미 버리기로 판정됐을 수 있다", "이 축을 안 읽었다", "관측되지 않았다"},
		},
		{
			name: "dropped 선언",
			d: &model.CloseDeclaration{
				Dropped: 1, Last: at, LastSession: "01LEADSESSION", LastMode: "dropped",
			},
			want: []string{
				"종료 선언: 롤백된 마무리 선언 적어도 1건(done 0 · dropped 1)",
				"dropped 1건: 이미 버리기로 판정됐을 수 있다.",
				"연결된 판단부터 읽어라.", "이 수는 하한이다",
			},
			deny: []string{"이미 랜딩됐을 수 있다", "이 축을 안 읽었다", "관측되지 않았다"},
		},
		{
			// 둘 다 0이 아니면 **둘 다** 낸다. 하나로 뭉치면 처방이 갈리는 사실이 사라진다.
			// 세션 id 는 event.session_id 에서 오는데 그 열은 NULL 을 받는다(schema.sql:367,
			// store 가 str(session) 으로 빈 문자열을 낸다) — 빈 값을 그대로 찍으면
			// "세션  · mode=" 가 되어 잘린 줄로 읽힌다.
			name: "둘 다 있고 세션은 비었다",
			d: &model.CloseDeclaration{
				Done: 1, Dropped: 1, Last: at, LastSession: "", LastMode: "dropped",
			},
			want: []string{
				"종료 선언: 롤백된 마무리 선언 적어도 2건(done 1 · dropped 1)",
				"done 1건: 이미 랜딩됐을 수 있다.",
				"dropped 1건: 이미 버리기로 판정됐을 수 있다.",
				"세션 미상",
			},
			deny: []string{"이 축을 안 읽었다", "관측되지 않았다"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := renderCloseDeclared(c.d, "")
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Fatalf("%q 가 없다:\n%s", w, got)
				}
			}
			for _, d := range c.deny {
				if strings.Contains(got, d) {
					t.Fatalf("이 갈래에 없어야 할 %q 가 있다 — 갈래가 서로 새고 있다:\n%s", d, got)
				}
			}
			// 끝은 반드시 개행이다. 안 그러면 다음 줄이 이 줄에 붙어 두 사실이 한 줄이 된다.
			if !strings.HasSuffix(got, "\n") {
				t.Fatalf("마지막 개행이 없다: %q", got)
			}
			// 들여쓰기는 **모든 줄에** 붙는다. 첫 줄만 밀면 이어지는 줄이 구성원 절에서
			// 선두의 발화로 읽힌다 — 경로 축의 `fd move` 줄이 정확히 그 함정을 밟은 적이 있고
			// (render_test.go:1450-1455) 그래서 거기도 줄 단위로 잠갔다.
			indented := renderCloseDeclared(c.d, "    ")
			for _, line := range strings.Split(strings.TrimRight(indented, "\n"), "\n") {
				if !strings.HasPrefix(line, "    ") {
					t.Fatalf("들여쓰기가 안 붙은 줄이 있다: %q\n%s", line, indented)
				}
			}
		})
	}
}

// TestRenderCloseDeclaredStealsNoCountedString 은 새 줄이 **이미 세어지고 있는 문자열**을
// 하나도 밟지 않는 것을 잠근다.
//
// ★ 왜 이것이 시험이어야 하나. 이 저장소의 pick 렌더 시험 여럿이 개수와 절 분할을
// 문자열 하나에 걸어 두고 있다: `경로 실재: ` 4개(render_test.go:1411) ·
// `fd move ` 1개(:1442) · `브랜치: ` 1개(render_test.go:238) · 구성원 절을 자르는
// `"\n  "+표식+" "`(bundleMemberSegment, render_lines_test.go:241-243).
// 새 문장이 그중 하나를 우연히 품으면 엉뚱한 시험이 붉어지고, 더 나쁘게는 절 경계가
// 밀려 **격리 단정이 조용히 무의미해진다.**
//
// ★ nil 문구는 renderPathCheck·renderBundle 의 nil 문구와도 **글자가 달라야 한다.**
// 그 문장("이 응답은 그 축을 읽지 않았다 — …")을 그대로 복제하면
// render_test.go:1415-1435 가 그것을 구성원 절에 붙은 **남의 경로 판정**으로 읽어
// 붉어진다(그 시험은 unreadSum 을 남의 절에서 못 찾는 것으로 격리를 잰다).
// 그래서 여기서 그 문자열 자체를 금지 목록에 넣는다.
func TestRenderCloseDeclaredStealsNoCountedString(t *testing.T) {
	at := time.Date(2026, 8, 4, 23, 54, 37, 0, time.UTC)
	var all strings.Builder
	for _, d := range []*model.CloseDeclaration{
		nil,
		{},
		{Done: 3, Last: at, LastSession: "01LEADSESSION", LastMode: "done"},
		{Done: 1, Dropped: 2, Last: at, LastSession: "01LEADSESSION", LastMode: "dropped"},
	} {
		all.WriteString(renderCloseDeclared(d, ""))
		all.WriteString(renderCloseDeclared(d, "    "))
	}
	got := all.String()

	for _, banned := range []string{
		"경로 실재: ", "브랜치: ", "fd move ", "겹침 판정 범위:",
		"안 들어갔다", "겹침을 관측하지 않았다", "묶을 게 없어 단독이다",
		"이 응답은 그 축을 읽지 않았다 — 낡은 캐시이거나 서버가 이 축을 모르는 판이다.",
	} {
		if strings.Contains(got, banned) {
			t.Fatalf("종료 선언 줄이 이미 세어지는 문자열 %q 를 밟았다:\n%s", banned, got)
		}
	}
	// 구성원 절의 경계는 `"\n  " + 표식 + " "` 하나에 걸려 있다.
	for _, mark := range []string{markClaimed, markRejected, markProposed} {
		if strings.Contains("\n"+got, "\n  "+mark+" ") {
			t.Fatalf("종료 선언 줄이 구성원 머리줄 접두(%q)로 시작한다 — 절이 그 자리에서 잘린다:\n%s", mark, got)
		}
	}
}
```

Expected: 파일이 생긴다.

- [ ] **Step 3: 실패 확인**

renderCloseDeclared 가 아직 없으므로 컴파일이 안 된다. 이것이 이 태스크의 빨강이다.

Run: `go test ./internal/mcpsrv/ -run TestRenderCloseDeclared -v -count=1`

Expected: `undefined: renderCloseDeclared` 로 빌드 실패. 다른 이유(예: model.CloseDeclaration undefined)로 실패하면 전제가 안 선 것이다.

- [ ] **Step 4: 최소 구현 — render.go 의 renderPathCheck 바로 뒤에 쌍둥이를 붙인다**

현재 render.go:1296-1303 (탭 들여쓰기, 그대로 인용):

```go
	s := "경로 실재: " + v.Summary + "\n"
	if v.Kind == judge.KindMisregistered && v.Suggest != "" {
		s += fmt.Sprintf("           맞다면 지금 되돌려라: `fd move %s --project %s`\n", itemID, v.Suggest)
	}
	return s
}

// ─────────────────────────────────────────────────────────────────────────────
```

Edit 로 `	return s
}

// ────` 사이에 아래를 끼운다(치환 대상은 `	return s\n}\n\n// ───…` 의 앞부분
`	return s\n}\n` 하나뿐이라 유일하지 않다 — 그러므로 **`s += fmt.Sprintf("           맞다면…` 줄부터 `}` 까지**를 통째로 old_string 으로 잡아 치환한다):

old_string:
```go
		s += fmt.Sprintf("           맞다면 지금 되돌려라: `fd move %s --project %s`\n", itemID, v.Suggest)
	}
	return s
}
```

new_string:
```go
		s += fmt.Sprintf("           맞다면 지금 되돌려라: `fd move %s --project %s`\n", itemID, v.Suggest)
	}
	return s
}

// renderCloseDeclared 는 종료 선언 축이다. 순수 함수이고 renderPathCheck 의 **쌍둥이**다.
//
// ★ **어느 갈래에서도 침묵하지 않는다.** renderPathCheck 이 이상이 없어도 한 줄을 찍는
// 그 이유 그대로다 — 침묵하면 "선언이 없다"와 "이 축을 안 봤다"가 같은 화면이 되고,
// 그러면 원장 조회가 통째로 실패한 날에도 pick 은 평소와 똑같아 보인다. 이 사고가
// 정확히 그 모양이었다: 신호는 08-04 부터 원장에 있었고 08-05 의 추천이 그것을
// 한 글자도 말하지 않았다.
//
// ★ nil 갈래의 문장은 renderPathCheck·renderBundle 의 그것과 **글자가 다르다.**
// 같은 문장을 쓰면 구성원 절 안에서 남의 판정과 내 판정이 문자열로 구분되지 않고,
// render_test.go:1415-1435 의 "제 것인가" 단정이 그 순간 무의미해진다(그 시험은
// unreadSum 을 남의 절에서 못 찾는 것으로 격리를 잰다). 실제로 복제하면 붉어진다.
//
// ★ 접두를 `종료 선언:` 으로 새로 판 이유는 기존 접두들에 개수·절 분할 시험이 물려
// 있기 때문이다(`경로 실재: ` 4개 · `fd move ` 1개 · `브랜치: ` 1개 · 구성원 표식 3종).
//
// ★ 수는 **하한이다.** store 의 CloseDeclarationsByItem doc 이 못박은 계약이다
// (event.go:255-258 — flushDeferred 가 트랜잭션 ctx 를 그대로 쓰고 LogEvent 는 쓰기
// 실패를 WARN 으로 삼키므로 안 써진 마무리가 있을 수 있다). 그래서 0건 갈래에서도
// "0이다"로 단정하지 않는다 — 0 이야말로 안 써진 마무리에 가장 잘 속는 값이다.
//
// ★ 처방이 mode 로 갈린다 — done 은 이미 랜딩됐을 수 있고 dropped 는 이미 버리기로
// 판정됐을 수 있다. 둘을 "끝난 일" 하나로 뭉치면 다음 세션이 무엇을 확인해야 하는지가
// 사라진다(랜딩 이력인가, 버린 판단인가). 그래서 둘 다 0이 아니면 두 줄을 다 낸다.
//
// ★ 매개변수 이름 `indent` 가 같은 파일의 indent(s, pad) 헬퍼를 가린다. 계약이 정한
// 시그니처라 그대로 두고, 대신 이 함수 안에서는 줄마다 접두를 직접 붙인다 —
// 여기서 indent(...) 를 부르면 "cannot call non-function" 으로 컴파일이 죽는다.
func renderCloseDeclared(d *model.CloseDeclaration, indent string) string {
	// 이어지는 줄은 "종료 선언: " 만큼 민다 — renderPathCheck 의 되돌리기 줄과 같은 모양이다.
	const cont = "           "
	if d == nil {
		return indent + "종료 선언: 이 응답은 이 축을 안 읽었다 — 낡은 캐시이거나 서버가 이 축을 모르는 판이다.\n"
	}
	if d.Count() == 0 {
		return indent + "종료 선언: 원장에서 하나도 못 봤다 — 이 항목을 닫으려다 롤백된 시도가 관측되지 않았다.\n" +
			indent + cont + "이 수는 하한이다 — 원장에 안 써진 마무리는 여기서 영영 0으로 보인다.\n"
	}
	// 세션 id 는 event.session_id 에서 오는데 그 열은 NULL 을 받고(schema.sql:367)
	// store 는 그것을 빈 문자열로 낸다. 그대로 찍으면 "세션  · mode=done" 이 되어
	// 읽는 쪽이 잘린 줄로 오해한다.
	session := ShortID(d.LastSession)
	if session == "" {
		session = "미상"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s종료 선언: 롤백된 마무리 선언 적어도 %d건(done %d · dropped %d) — 마지막 %s · 세션 %s · mode=%s\n",
		indent, d.Count(), d.Done, d.Dropped,
		d.Last.UTC().Format("2006-01-02 15:04"), session, d.LastMode)
	if d.Done > 0 {
		fmt.Fprintf(&b, "%s%sdone %d건: 이미 랜딩됐을 수 있다.\n", indent, cont, d.Done)
	}
	if d.Dropped > 0 {
		fmt.Fprintf(&b, "%s%sdropped %d건: 이미 버리기로 판정됐을 수 있다.\n", indent, cont, d.Dropped)
	}
	fmt.Fprintf(&b, "%s%s연결된 판단부터 읽어라. 이 수는 하한이다 — 원장에 안 써진 마무리는 여기 안 잡힌다.\n", indent, cont)
	return b.String()
}
```

Expected: render.go 가 컴파일된다. model·strings·fmt 는 이미 import 되어 있다(render.go:4-11).

- [ ] **Step 5: 통과 확인 + 패키지 회귀**

두 번 돈다. 새 시험만 보면 기존 개수 단정을 밟았는지 못 잰다.

Run: `go test ./internal/mcpsrv/ -run TestRenderCloseDeclared -v -count=1 && go test ./internal/mcpsrv/ -count=1`

Expected: 새 시험 둘 다 PASS(하위 절 5개 포함). mcpsrv 패키지 전체도 ok — 이 시점에는 render.go 에 새 함수만 있고 호출부가 없으므로 기존 출력이 한 글자도 안 바뀐다.

- [ ] **Step 6: 커밋**



Run: `git add plugins/flightdeck/server/internal/mcpsrv/render.go plugins/flightdeck/server/internal/mcpsrv/render_close_declared_test.go && git commit -m "feat(flightdeck): 종료 선언 한 줄을 짓는다 — nil 도 0건도 침묵하지 않고, 수는 하한이라고 말한다" -m "renderPathCheck 의 쌍둥이다. nil 문구는 일부러 그쪽과 글자를 달리했다 — 같은 문장을 쓰면 구성원 절의 격리 단정이 남의 판정으로 오인한다."`

Expected: 커밋 1건.

---

### Task 10: R2 — 선두의 종료 선언을 pick 응답에 싣는다(이 사고의 주인공이 선두다)
**Files:**
- Modify: plugins/flightdeck/server/internal/mcpsrv/render.go:989 (renderPathCheck 호출 바로 뒤)
- Modify: plugins/flightdeck/server/internal/mcpsrv/render_close_declared_test.go (import 블록 + 시험 셋 추가)

**Interfaces:**
- Consumes: R1 의 renderCloseDeclared(d *model.CloseDeclaration, indent string) string. **그리고 service 층**: service.PickResult 에 `CloseDeclared *model.CloseDeclaration \`json:"close_declared,omitempty"\`` 가 있어야 한다(설계 §4-④). 없으면 시험이 컴파일조차 안 된다 — 이 태스크는 service 구조체 태스크 뒤다.
- Produces: RenderPick 이 항목 절 안, `경로 실재:` 바로 뒤에 0칸 들여쓰기로 종료 선언 줄을 낸다. r.Item == nil(적격 0건)이면 안 낸다. 구성원 갈래(R3)는 이 줄과 **다른 값**을 내야 하며 그 격리를 R3 가 잰다.

- [ ] **Step 1: 전제 확인 — service 필드가 있는지**



Run: `grep -n "CloseDeclared \*model.CloseDeclaration" internal/service/pick.go`

Expected: PickResult 와 BundleMember 두 자리가 나온다. 안 나오면 service 구조체 태스크를 먼저 끝내라.

- [ ] **Step 2: 실패 시험 — import 에 service 를 더하고 시험 셋을 붙인다**

먼저 import 블록을 고친다.

old_string:
```go
import (
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)
```

new_string:
```go
import (
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)
```

그리고 파일 끝에 아래 셋을 붙인다.

```go

// TestRenderPickCarriesTheLeadCloseDeclaration 은 **선두**의 종료 선언을 못박는다.
//
// ★ 이 사고의 주인공이 선두다. 08-04 에 롤백된 finish 가 원장에 남은 항목이
// 08-05 22:54 의 pick 에서 후보 26건 중 **1순위 선두**로 추천됐다. 그런데
// renderBundle 은 BundleInfo 하나만 받고 Members 는 정의상 선두 제외라 **선두를
// 모른다** — 구성원 자리에만 심으면 이 사고를 낳은 바로 그 항목에 대해 응답이
// 정확히 침묵한다. 그래서 선두 갈래는 별도 단정이 필요하다.
//
// 묶음이 없는 응답(Bundle=nil)으로 본다. 묶음 절이 있으면 어느 구성원 줄이
// 이 단정을 대신 통과시킬 수 있다.
func TestRenderPickCarriesTheLeadCloseDeclaration(t *testing.T) {
	got := RenderPick(service.PickResult{
		Mode: service.PickRecommended, Reason: "1순위다",
		Item: &model.Item{ID: "lead", Title: "선두", State: model.ItemOpen, CreatedAt: t0},
		CloseDeclared: &model.CloseDeclaration{
			Done: 1, Last: time.Date(2026, 8, 4, 23, 54, 37, 0, time.UTC),
			LastSession: "01LEADSESSION", LastMode: "done",
		},
	}, t0)

	const want = "\n종료 선언: 롤백된 마무리 선언 적어도 1건(done 1 · dropped 0) — " +
		"마지막 2026-08-04 23:54 · 세션 01LEADSE… · mode=done\n"
	if !strings.Contains(got, want) {
		t.Fatalf("선두의 종료 선언 줄이 0칸 들여쓰기로 제 값을 안 낸다:\n%s", got)
	}
	if !strings.Contains(got, "이미 랜딩됐을 수 있다") {
		t.Fatalf("done 처방(이미 랜딩됐을 수 있다)이 없다 — 다음 세션이 무엇을 확인할지 모른다:\n%s", got)
	}

	// 자리도 못박는다 — 항목 절 **안**, `경로 실재:` 바로 뒤다. 응답 꼬리로 밀리면
	// 본문 4000자와 묶음 절을 지나야 보이는 줄이 되고, 그것은 이 축이 겨냥한 독자
	// (집기 전에 읽는 세션)에게 사실상 안 보이는 것과 같다.
	head := strings.Index(got, "\n▸ lead")
	axis := strings.Index(got, "\n경로 실재: ")
	decl := strings.Index(got, "\n종료 선언: ")
	if head < 0 || axis < 0 || decl < 0 {
		t.Fatalf("항목 머리줄(%d)·경로 축(%d)·종료 선언(%d) 중 없는 줄이 있다:\n%s", head, axis, decl, got)
	}
	if head >= axis || axis >= decl {
		t.Fatalf("종료 선언 줄이 항목 절 안 `경로 실재:` 뒤가 아니다(머리줄 %d · 경로 축 %d · 종료 선언 %d):\n%s",
			head, axis, decl, got)
	}
}

// 선두의 축이 nil 이면 그 사실을 말한다 — 침묵이 "선언 없음"으로 읽히면 안 된다.
// (TestRenderPickSaysTheAxisWasNotReadWhenVerdictIsNil 과 같은 규율이다.)
func TestRenderPickSaysTheCloseAxisWasNotReadWhenNil(t *testing.T) {
	got := RenderPick(service.PickResult{
		Mode: service.PickRecommended, Reason: "1순위다",
		Item: &model.Item{ID: "lead", Title: "선두", State: model.ItemOpen, CreatedAt: t0},
	}, t0)

	if !strings.Contains(got, "\n종료 선언: 이 응답은 이 축을 안 읽었다") {
		t.Fatalf("종료 선언 축이 nil 인데 그 사실을 말하지 않는다 — 못 읽음이 0건으로 접힌다:\n%s", got)
	}
}

// 항목이 없으면 이 줄도 없다 — 관측할 대상이 없다.
// (TestRenderPickOmitsPathAxisWhenThereIsNoItem 과 같은 규율이다.)
func TestRenderPickOmitsCloseDeclarationWhenThereIsNoItem(t *testing.T) {
	got := RenderPick(service.PickResult{
		Mode: service.PickNone, Reason: "적격 항목이 0건이다", Scope: "후보 = 열린 항목 0건",
	}, t0)

	if strings.Contains(got, "종료 선언:") {
		t.Fatalf("항목이 없는데 종료 선언 줄이 나왔다:\n%s", got)
	}
}
```

Expected: 파일이 고쳐진다.

- [ ] **Step 3: 실패 확인**



Run: `go test ./internal/mcpsrv/ -run 'TestRenderPick(CarriesTheLeadCloseDeclaration|SaysTheCloseAxisWasNotReadWhenNil|OmitsCloseDeclarationWhenThereIsNoItem)' -v -count=1`

Expected: 앞의 둘이 FAIL("선두의 종료 선언 줄이 …" / "… 그 사실을 말하지 않는다"). 셋째는 이미 PASS 다 — 그것이 정상이다(빼는 것을 잠그는 시험이라 구현 전에도 참이다).

- [ ] **Step 4: 최소 구현 — RenderPick:989 뒤에 0칸으로 붙인다**

현재 render.go:989-990 (탭 두 개, 그대로 인용):

```go
		b.WriteString(renderPathCheck(r.PathCheck, it.ID))
		if len(it.After) > 0 {
```

Edit — old_string:
```go
		b.WriteString(renderPathCheck(r.PathCheck, it.ID))
		if len(it.After) > 0 {
```

new_string:
```go
		b.WriteString(renderPathCheck(r.PathCheck, it.ID))
		// ★ 종료 선언 축은 **선두에도** 찍는다. renderBundle 은 BundleInfo 하나만 받고
		// Members 는 정의상 선두 제외라 선두를 모른다 — 구성원 자리에만 심으면 이 사고를
		// 낳은 그 항목(선두였다)에 대해 응답이 정확히 침묵한다.
		//
		// 들여쓰기 0칸은 바로 위 `경로 실재:` 와 같은 깊이라는 뜻이다. 자리도 여기여야
		// 한다 — 본문 4000자와 묶음 절 뒤로 밀면 집기 전에 읽는 세션에게 사실상 안 보인다.
		b.WriteString(renderCloseDeclared(r.CloseDeclared, ""))
		if len(it.After) > 0 {
```

- [ ] **Step 5: 통과 확인 + 패키지 회귀**

패키지 전체를 반드시 같이 돈다. 이 줄이 처음으로 **기존 출력에 끼어드는** 변경이라, `경로 실재: ` 개수·`브랜치: ` 개수·구성원 절 경계를 밟았는지는 여기서만 드러난다.

Run: `go test ./internal/mcpsrv/ -run 'TestRenderPick(CarriesTheLeadCloseDeclaration|SaysTheCloseAxisWasNotReadWhenNil|OmitsCloseDeclarationWhenThereIsNoItem)' -v -count=1 && go test ./internal/mcpsrv/ -count=1`

Expected: 셋 다 PASS, mcpsrv 전체 ok. 특히 TestRenderPickGivesEachBundleMemberItsOwnPathVerdict(render_test.go:1372)가 초록이어야 한다 — 붉어지면 nil 문구가 renderPathCheck 의 그것과 같아진 것이다.

- [ ] **Step 6: 커밋**



Run: `git add plugins/flightdeck/server/internal/mcpsrv/render.go plugins/flightdeck/server/internal/mcpsrv/render_close_declared_test.go && git commit -m "feat(flightdeck): 선두의 종료 선언을 pick 응답에 싣는다 — 이 사고의 주인공이 선두였다" -m "renderBundle 은 Members 가 선두 제외라 선두를 모른다. 구성원 자리에만 심으면 08-05 의 1순위 추천이 하던 침묵을 그대로 재현한다."`

Expected: 커밋 1건.

---

### Task 11: R3 — 구성원의 종료 선언: continue 보다 **위에**, 각자 제 값으로
**Files:**
- Modify: plugins/flightdeck/server/internal/mcpsrv/render.go:1209-1210 (renderBundle 구성원 머리줄 바로 뒤 · 1215줄 continue 위)
- Modify: plugins/flightdeck/server/internal/mcpsrv/render_close_declared_test.go (import 에 judge 추가 + 시험 하나 추가)

**Interfaces:**
- Consumes: R1 의 renderCloseDeclared · R2 의 선두 배선. **그리고 service 층**: service.BundleMember 에 `CloseDeclared *model.CloseDeclaration \`json:"close_declared,omitempty"\`` 가 있어야 한다.
- Produces: renderBundle 이 구성원 머리줄 바로 아래에 4칸 들여쓰기로 종료 선언 줄을 낸다 — 못 집은 구성원(Rejection≠nil, continue 로 절을 끊는 갈래)에게도 나온다. 이로써 mcpsrv 층의 §4-④ 표면이 닫힌다.

- [ ] **Step 1: 실패 시험 — import 에 judge 를 더하고 격리 시험을 붙인다**

먼저 import 블록.

old_string:
```go
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)
```

new_string:
```go
	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)
```

그리고 파일 끝에 붙인다.

```go

// TestRenderPickGivesEachBundleMemberItsOwnCloseDeclaration 은 구성원마다 **제 값**을
// 받는지를 절 안에서 단정한다. render_test.go:1372 의 경로 축 시험과 같은 좌표계다 —
// 전체 문자열에 대한 strings.Contains 는 **출력을 넓히는 모든 변경을 통과시킨다.**
// 실측으로 확인된 것도 그것이다: renderPathCheck 의 인자를 Members[0] 것으로 바꿔도
// 전 스위트가 초록이었다. 그래서 다섯 값을 **서로 다르게** 깔고 bundleMemberSegment 로
// 잘라 그 안에서만 본다 — 선두 것을 구성원에 복사하는 변이가 여기서 죽는다.
//
// ★ 못 집은 구성원(Rejection≠nil)을 반드시 하나 넣는다. 그 갈래는 render.go:1215 의
// continue 로 절을 끊으므로, 줄을 continue 아래에 두는 구현은 **여기서만** 죽는다.
// 그리고 그 자리가 중요한 이유가 있다: 못 집은 구성원이야말로 다음 세션이 다시
// 집으러 오는 항목이다.
//
// ★ 오등록 시험이 Members[1] 에 값을 둔 것과 같은 이유로, 여기서도 값이 다른 구성원을
// Members[0] 아닌 자리에 섞는다 — Members[0] 만 쓰는 변이가 정답과 구별되게.
func TestRenderPickGivesEachBundleMemberItsOwnCloseDeclaration(t *testing.T) {
	var (
		leadAt    = time.Date(2026, 8, 4, 23, 54, 37, 0, time.UTC)
		doneAt    = time.Date(2026, 8, 5, 1, 0, 0, 0, time.UTC)
		droppedAt = time.Date(2026, 8, 6, 2, 0, 0, 0, time.UTC)
	)
	const (
		leadOwn    = "롤백된 마무리 선언 적어도 2건(done 2 · dropped 0) — 마지막 2026-08-04 23:54 · 세션 01LEADSE… · mode=done"
		doneOwn    = "롤백된 마무리 선언 적어도 1건(done 1 · dropped 0) — 마지막 2026-08-05 01:00 · 세션 01MEMDON… · mode=done"
		droppedOwn = "롤백된 마무리 선언 적어도 3건(done 0 · dropped 3) — 마지막 2026-08-06 02:00 · 세션 01MEMDRO… · mode=dropped"
		cleanOwn   = "원장에서 하나도 못 봤다"
		unreadOwn  = "이 응답은 이 축을 안 읽었다"
	)
	res := service.PickResult{
		Mode: service.PickClaimed, Reason: "선두를 선점했다", Branch: "lead",
		Item: &model.Item{ID: "lead", Title: "선두", State: model.ItemClaimed, CreatedAt: t0},
		CloseDeclared: &model.CloseDeclaration{
			Done: 2, Last: leadAt, LastSession: "01LEADSESSION", LastMode: "done",
		},
		Bundle: &service.BundleInfo{
			Reason: "의존자 합 0 · 묶음 5건 · 선두 lead",
			Members: []service.BundleMember{
				{
					Item:    model.Item{ID: "m-done", Title: "done 선언", State: model.ItemClaimed, CreatedAt: t0},
					Claimed: true,
					CloseDeclared: &model.CloseDeclaration{
						Done: 1, Last: doneAt, LastSession: "01MEMDONE01", LastMode: "done",
					},
				},
				{
					// 못 집은 구성원 — continue 갈래. 여기 줄이 없으면 이 시험만 붉어진다.
					Item:      model.Item{ID: "m-dropped", Title: "dropped 선언", State: model.ItemClaimed, CreatedAt: t0},
					Rejection: &model.Rejection{Item: "m-dropped", Reason: judge.RejectClaimed, Detail: "세션 S2 가 선점했다"},
					CloseDeclared: &model.CloseDeclaration{
						Dropped: 3, Last: droppedAt, LastSession: "01MEMDROP01", LastMode: "dropped",
					},
				},
				{
					// 축은 읽었고 이 항목엔 선언이 없다 — nil 과 **다른 사실**이다.
					Item:          model.Item{ID: "m-clean", Title: "선언 없음", State: model.ItemOpen, CreatedAt: t0},
					Link:          judge.Link{Item: "m-clean", Detail: "세션이 함께 지정했다"},
					CloseDeclared: &model.CloseDeclaration{},
				},
				{
					// 축 자체를 안 읽었다 — 구서버·옛 캐시.
					Item:    model.Item{ID: "m-unread", Title: "축 못 읽음", State: model.ItemClaimed, CreatedAt: t0},
					Claimed: true,
				},
			},
		},
	}
	got := RenderPick(res, t0)

	// ① 선두 1 + 구성원 4 = 다섯. ("nil 이면 건너뛴다" 변이와 줄 삭제 변이가 여기서 죽는다.)
	if n := strings.Count(got, "종료 선언: "); n != 5 {
		t.Fatalf("종료 선언 줄이 %d개다 — 선두 1 + 구성원 4 = 5여야 한다:\n%s", n, got)
	}
	// ② 선두는 0칸이다. 구성원 줄(4칸)이 이 단정을 대신 통과시키지 못한다.
	if !strings.Contains(got, "\n종료 선언: "+leadOwn+"\n") {
		t.Fatalf("선두의 종료 선언이 0칸 들여쓰기로 제 값을 안 낸다:\n%s", got)
	}

	// ③ 각자 **제 것**이다. 자기 절 안에 자기 값이 4칸으로 있고, 남의 값은 없다.
	segs := map[string]string{
		"m-done":    bundleMemberSegment(t, got, "m-done"),
		"m-dropped": bundleMemberSegment(t, got, "m-dropped"),
		"m-clean":   bundleMemberSegment(t, got, "m-clean"),
		"m-unread":  bundleMemberSegment(t, got, "m-unread"),
	}
	own := map[string]string{
		"m-done": doneOwn, "m-dropped": droppedOwn, "m-clean": cleanOwn, "m-unread": unreadOwn,
	}
	all := []string{leadOwn, doneOwn, droppedOwn, cleanOwn, unreadOwn}
	for id, seg := range segs {
		if !strings.Contains(seg, "\n    종료 선언: "+own[id]) {
			t.Fatalf("구성원 %s 의 절에 자기 종료 선언이 4칸 들여쓰기로 없다:\n%s\n전체:\n%s", id, seg, got)
		}
		for _, other := range all {
			if other == own[id] {
				continue
			}
			if strings.Contains(seg, other) {
				t.Fatalf("구성원 %s 의 절에 남의 종료 선언이 붙었다(%q):\n%s\n전체:\n%s", id, other, seg, got)
			}
		}
	}

	// ④ 처방은 mode 로 갈린다. 둘을 맞바꾸는 변이는 ③ 으로는 안 죽는다 —
	//    수와 시각만 봐도 ③ 은 통과하기 때문이다.
	if !strings.Contains(segs["m-done"], "done 1건: 이미 랜딩됐을 수 있다.") ||
		strings.Contains(segs["m-done"], "이미 버리기로 판정됐을 수 있다") {
		t.Fatalf("done 구성원의 처방이 틀렸다:\n%s\n전체:\n%s", segs["m-done"], got)
	}
	if !strings.Contains(segs["m-dropped"], "dropped 3건: 이미 버리기로 판정됐을 수 있다.") ||
		strings.Contains(segs["m-dropped"], "이미 랜딩됐을 수 있다") {
		t.Fatalf("dropped 구성원의 처방이 틀렸다:\n%s\n전체:\n%s", segs["m-dropped"], got)
	}

	// ⑤ 못 집은 구성원에게도 나온다 — render.go:1215 의 continue **위**여야 한다.
	if !strings.Contains(segs["m-dropped"], "못 집었다: ") {
		t.Fatalf("전제 실패 — m-dropped 가 못 집은 구성원이 아니다:\n%s", segs["m-dropped"])
	}

	// ⑥ 기존 개수 단정을 **같은 출력에서** 함께 본다. 새 줄이 절 경계나 개수를
	//    밟았는지는 순수 함수 시험으로는 못 잰다 — 밟히는 자리가 renderBundle 의
	//    조립 결과이기 때문이다.
	if n := strings.Count(got, "경로 실재: "); n != 5 {
		t.Fatalf("경로 실재 줄이 %d개다 — 선두 1 + 구성원 4 = 5여야 한다(새 줄이 그 축을 밟았다):\n%s", n, got)
	}
	heads := strings.Count(got, "\n  "+markClaimed+" ") +
		strings.Count(got, "\n  "+markRejected+" ") +
		strings.Count(got, "\n  "+markProposed+" ")
	if heads != 4 {
		t.Fatalf("구성원 머리줄이 %d개다 — 새 줄이 구성원 절 경계를 밟았다:\n%s", heads, got)
	}
}
```

Expected: 파일이 고쳐진다.

- [ ] **Step 2: 실패 확인**



Run: `go test ./internal/mcpsrv/ -run TestRenderPickGivesEachBundleMemberItsOwnCloseDeclaration -v -count=1`

Expected: FAIL — "종료 선언 줄이 1개다 — 선두 1 + 구성원 4 = 5여야 한다". (선두만 있고 구성원 넷이 비어 있다.)

- [ ] **Step 3: 최소 구현 — 구성원 머리줄 바로 뒤, continue 위에 4칸으로**

현재 render.go:1209-1210 (탭 두 개, 그대로 인용):

```go
		fmt.Fprintf(&b, "\n  %s %s — %s [%s]\n", mark, m.Item.ID, m.Item.Title, m.Item.State)
		if m.Rejection != nil {
```

Edit — old_string:
```go
		fmt.Fprintf(&b, "\n  %s %s — %s [%s]\n", mark, m.Item.ID, m.Item.Title, m.Item.State)
		if m.Rejection != nil {
```

new_string:
```go
		fmt.Fprintf(&b, "\n  %s %s — %s [%s]\n", mark, m.Item.ID, m.Item.Title, m.Item.State)
		// ★ 종료 선언은 **머리줄 바로 밑**에 찍는다. 아래 못 집은 갈래는 continue 로 절을
		// 끊으므로 이 줄을 그 뒤에 두면 못 집은 구성원에게 영영 안 나온다 — 그런데
		// "이미 닫으려던 항목"과 "지금 못 집었다"는 겹쳐서 나는 사실이고, 못 집은 구성원이야말로
		// 다음 세션이 다시 집으러 오는 자리다. 그래서 사유 줄보다 위다.
		//
		// 값은 **그 구성원의 것**이다. 선두의 r.CloseDeclared 를 여기 넘기면 다섯 항목이
		// 같은 사실을 말하게 되고, 그 변이는 전체 문자열 Contains 로는 안 죽는다
		// (경로 축이 실제로 그렇게 죽어 있었다 — render_test.go:1326-1333).
		b.WriteString(renderCloseDeclared(m.CloseDeclared, "    "))
		if m.Rejection != nil {
```

- [ ] **Step 4: 통과 확인 + 패키지 회귀 + 교차 빌드 관문**

go build 는 _test.go 를 건너뛰므로 관문은 go vet 이다.

Run: `go test ./internal/mcpsrv/ -run TestRenderPickGivesEachBundleMemberItsOwnCloseDeclaration -v -count=1 && go test ./internal/mcpsrv/ -count=1 && go vet ./... && GOOS=windows GOARCH=amd64 go vet ./... && GOOS=darwin GOARCH=arm64 go vet ./...`

Expected: 새 시험 PASS · mcpsrv 전체 ok · vet 셋 다 무출력. 특히 render_test.go:1372(경로 축 격리)·render_lines_test.go:232(구성원 수)·render_accounting_test.go 전부가 초록이어야 한다.

- [ ] **Step 5: 커밋**



Run: `git add plugins/flightdeck/server/internal/mcpsrv/render.go plugins/flightdeck/server/internal/mcpsrv/render_close_declared_test.go && git commit -m "feat(flightdeck): 못 집은 구성원에게도 종료 선언이 나온다 — continue 보다 위에 쓴다" -m "머리줄 바로 밑이다. 사유 줄 뒤에 두면 continue 가 절을 끊어 못 집은 구성원에게 영영 안 나오는데, 그 항목이야말로 다음 세션이 다시 집으러 오는 자리다."`

Expected: 커밋 1건.

---

### Task 12: 종료 선언 표기 — 순수 함수 하나(format.go)
**Files:**
- Modify: internal/web/format.go (파일 끝, 현재 423줄 뒤에 덧붙인다)
- Test: internal/web/format_test.go (파일 끝, 현재 382줄 뒤에 덧붙인다)

**Interfaces:**
- Consumes: model.CloseDeclaration{Done,Dropped int; Last time.Time; LastSession,LastMode string} + (d CloseDeclaration) Count() int — **이미 랜딩됐다**(internal/model/types.go:319-342). store.(*Store).CloseDeclarationsByItem 도 이미 있다(internal/store/event.go:263) — 그 doc 이 "앵커도 항목 존재 판정도 여기서 하지 않는다 … 지웠다 다시 만든 id 의 옛 선언이 그대로 들어 있다"고 못박았으므로 앵커는 이 함수가 건다.
- Produces: web.CloseDeclUnread = "?" (상수) · web.CloseDeclaredLabel(d model.CloseDeclaration, read bool, created time.Time) string. read=false → CloseDeclUnread · Count()==0 → "" · created 이후 선언 → "종료 선언 최소 1건(done 1 · dropped 0) — 마지막 08-04 23:54 · mode=done · 세션 01KZ785TQ8VW…"

- [ ] **Step 1: 실패 시험을 먼저 쓴다 — 표 주도, 이 파일의 결 그대로**

`internal/web/format_test.go` **맨 끝**(현재 마지막 함수 `TestSubSecondFutureIsNoiseNotSkew` 의 닫는 `}` 뒤)에 덧붙인다. 이 파일은 이미 `model`·`time`·`strings` 를 import 하고 있으므로 import 를 건드리지 않는다.

```go

// ─────────────────────────────────────────────────────────────────────────────

// TestCloseDeclaredLabel 은 **화면이 세 상태를 세 문장으로 가르는지** 본다:
// 안 읽음 · 선언 없음 · 선언 있음. 이 셋을 둘로 접는 순간 조회가 죽은 화면이
// "이 항목은 깨끗하다"고 말하게 되고, 그 거짓말이 정확히 이 축이 막으려는 사고다.
func TestCloseDeclaredLabel(t *testing.T) {
	created := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	last := time.Date(2026, 8, 4, 23, 54, 37, 0, time.UTC)
	const sess = "01KZ785TQ8VWXYZ0123456789"

	cases := []struct {
		name    string
		d       model.CloseDeclaration
		read    bool
		created time.Time
		want    string
	}{
		{
			name: "못 읽었다 — 0으로 접지 않는다",
			d:    model.CloseDeclaration{}, read: false, created: created,
			want: CloseDeclUnread,
		},
		{
			// 표 밖: 못 읽었는데 값이 딸려 온 경우. 센티널이 이긴다 —
			// 못 읽은 조회가 낸 수는 수가 아니다.
			name: "못 읽었으면 값이 있어도 센티널이 이긴다",
			d:    model.CloseDeclaration{Done: 3, Last: last}, read: false, created: created,
			want: CloseDeclUnread,
		},
		{
			name: "읽었고 선언이 없다 — 아무 말도 안 한다",
			d:    model.CloseDeclaration{}, read: true, created: created,
			want: "",
		},
		{
			name: "done 1건 — 사고 사례의 실제 값",
			d: model.CloseDeclaration{
				Done: 1, Last: last, LastSession: sess, LastMode: "done",
			}, read: true, created: created,
			want: "종료 선언 최소 1건(done 1 · dropped 0) — 마지막 08-04 23:54 · mode=done · 세션 01KZ785TQ8VW…",
		},
		{
			// dropped 를 done 에 합치지 않는다 — 처방이 갈린다(done 은 "이미 랜딩됐을 수 있다",
			// dropped 는 "이미 버리기로 판정됐을 수 있다"). 실측 384건 중 76건이 dropped 다.
			name: "dropped 도 센다",
			d: model.CloseDeclaration{
				Dropped: 1, Last: last, LastSession: sess, LastMode: "dropped",
			}, read: true, created: created,
			want: "종료 선언 최소 1건(done 0 · dropped 1) — 마지막 08-04 23:54 · mode=dropped · 세션 01KZ785TQ8VW…",
		},
		{
			name: "둘 다 — 합은 Count 가 낸다",
			d: model.CloseDeclaration{
				Done: 1, Dropped: 2, Last: last, LastSession: sess, LastMode: "dropped",
			}, read: true, created: created,
			want: "종료 선언 최소 3건(done 1 · dropped 2) — 마지막 08-04 23:54 · mode=dropped · 세션 01KZ785TQ8VW…",
		},
		{
			// item 의 PK 가 (project, id) 라 지웠다 다시 만든 id 가 옛 이벤트를 물려받는다.
			// store 가 그 앵커를 **일부러 안 걸고** 호출자에게 넘긴다고 doc 에 적어 뒀다.
			name: "항목보다 옛 선언은 버린다 — 되살아난 id 의 유산이다",
			d: model.CloseDeclaration{
				Done: 1, Last: created.Add(-time.Hour), LastSession: sess, LastMode: "done",
			}, read: true, created: created,
			want: "",
		},
		{
			name: "항목 생성 시각을 모르면 앵커를 안 건다 — 없는 근거로 버리지 않는다",
			d: model.CloseDeclaration{
				Done: 1, Last: last, LastSession: sess, LastMode: "done",
			}, read: true, created: time.Time{},
			want: "종료 선언 최소 1건(done 1 · dropped 0) — 마지막 08-04 23:54 · mode=done · 세션 01KZ785TQ8VW…",
		},
		{
			// 표 밖 — store 는 Count>0 이면 Last·LastMode 를 반드시 채운다(event.go 의
			// `if d.LastMode == ""` 갈래). 화면은 그 전제에 기대지 않는다: 기대면 store 가
			// 바뀌는 날 빈칸이 사실인 척한다.
			name: "메타를 못 읽어도 수는 낸다",
			d:    model.CloseDeclaration{Done: 1}, read: true, created: created,
			want: "종료 선언 최소 1건(done 1 · dropped 0) — 마지막 시각 미상 · mode 미상 · 세션 미상",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CloseDeclaredLabel(c.d, c.read, c.created); got != c.want {
				t.Fatalf("CloseDeclaredLabel = %q, 기대 %q", got, c.want)
			}
		})
	}
}

// 문구가 **하한이라고 말하는지**를 따로 잠근다.
//
// ★ flushDeferred 는 트랜잭션이 물던 ctx 를 그대로 쓰고 LogEvent 는 쓰기 실패를 WARN 으로만
// 삼키므로, 클라이언트가 끊기면 행이 안 써진다. "정확히 N건"으로 쓰면 화면이 관측하지 않은
// 것을 단정하는 셈이고, 그 문구는 위 표의 want 문자열 안에 묻혀 조용히 지워질 수 있다.
func TestCloseDeclaredLabelSaysTheCountIsALowerBound(t *testing.T) {
	got := CloseDeclaredLabel(model.CloseDeclaration{
		Done: 1, Last: time.Date(2026, 8, 4, 23, 54, 0, 0, time.UTC),
		LastSession: "01KZ785TQ8VWXYZ0123456789", LastMode: "done",
	}, true, time.Time{})
	if !strings.Contains(got, "최소") {
		t.Fatalf("%q — 이 수는 하한인데 문구가 정확한 수인 척한다", got)
	}
}
```

Expected: 파일이 저장된다. 아직 컴파일 안 된다(CloseDeclaredLabel·CloseDeclUnread 가 없다).

- [ ] **Step 2: 실패를 확인한다**

빨간불이 **올바른 이유로** 나는지 본다. 이 단계에서는 단정 실패가 아니라 미정의 심볼이 정상이다.

Run: `go test ./internal/web/ -run TestCloseDeclaredLabel -count=1 2>&1 | head -20`

Expected: `undefined: CloseDeclUnread` · `undefined: CloseDeclaredLabel` 로 빌드 실패. [build failed] 로 끝난다.

- [ ] **Step 3: 최소 구현 — format.go 끝에 덧붙인다**

`internal/web/format.go` 의 **마지막 함수 `Clip` 의 닫는 `}`** 뒤에 덧붙인다. 치환 앵커(파일 끝, 정확히 이 블록이다):

```go
	s = strings.TrimSpace(s)
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n]) + "…"
}
```

뒤에 붙일 것:

```go

// ─────────────────────────────────────────────────────────────────────────────
// 종료 선언 — 닫으려다 롤백된 시도
// ─────────────────────────────────────────────────────────────────────────────

// CloseDeclUnread 는 "이 축을 못 읽었다"는 센티널이다.
//
// ★ 빈 문자열과 **반드시 갈라야 한다.** 빈 문자열은 "원장을 읽었고 선언이 0건이다"이고
// 이것은 "원장을 아예 못 읽었다"다. 둘을 한 값으로 접으면 조회가 죽은 화면이 그 항목을
// "깨끗하다"고 말하게 되고, 그 거짓말이 정확히 이 축이 막으려는 사고다.
// 같은 층의 선례가 ItemRow.Dependents 의 -1 이다(page.go 의 역인덱스 조회 실패 갈래).
const CloseDeclUnread = "?"

// CloseDeclaredLabel 은 항목 한 줄의 종료 선언 표기다. 순수 함수다.
//
// 규약 셋:
//   - read=false → CloseDeclUnread. **0건으로 접지 않는다.**
//   - 선언 0건 → 빈 문자열. 화면에 아무것도 안 낸다(없는 것에 자리를 주지 않는다).
//   - 그 밖 → "최소 N건"으로 쓴다. store 가 낸 수는 정확한 수가 아니라 **하한**이다 —
//     flushDeferred 가 트랜잭션의 ctx 를 그대로 쓰고 LogEvent 는 쓰기 실패를 WARN 으로만
//     삼키므로, 클라이언트가 끊기면 행이 안 써진다. 문구가 그 사실을 말해야 한다.
//
// ★ created 이전의 선언은 **버린다.** item 의 PK 가 (project, id) 라 지웠다 다시 만든 id 가
// 옛 이벤트를 물려받는다. store.CloseDeclarationsByItem 이 그 앵커를 일부러 안 걸고
// 호출자에게 넘긴다고 doc 에 적어 뒀다("이 함수는 원자료만 낸다").
//
// ★ 앵커는 Last 하나로만 건다. 집계가 이미 mode 별 수로 접혀 있어 그보다 정밀하게 못 자른다 —
// **되살아난 id 에 옛 선언이 섞여 수만 부푼 경우는 이 함수가 못 가른다.** 정직하게 적는다:
// 안 적으면 다음 세션이 이 축을 완전한 것으로 믿는다.
//
// ★ 시각을 못 읽은 선언에는 앵커를 안 건다. 그것을 "항목보다 옛것"으로 몰면 관측하지 않은
// 사실을 단정하는 것이다 — 그때는 버리지 않고 시각만 미상으로 낸다.
func CloseDeclaredLabel(d model.CloseDeclaration, read bool, created time.Time) string {
	if !read {
		return CloseDeclUnread
	}
	if d.Count() == 0 {
		return ""
	}
	if !created.IsZero() && !d.Last.IsZero() && d.Last.Before(created) {
		return ""
	}
	last, mode, sess := "마지막 시각 미상", "mode 미상", "세션 미상"
	if !d.Last.IsZero() {
		last = "마지막 " + d.Last.Format("01-02 15:04")
	}
	if d.LastMode != "" {
		mode = "mode=" + Clip(d.LastMode, 16)
	}
	if d.LastSession != "" {
		sess = "세션 " + short(Clip(d.LastSession, 64))
	}
	return fmt.Sprintf("종료 선언 최소 %d건(done %d · dropped %d) — %s · %s · %s",
		d.Count(), d.Done, d.Dropped, last, mode, sess)
}
```

import 는 안 건드린다 — `fmt`·`time`·`model` 이 이미 다 들어 있다(format.go:27-35).

Expected: format.go 가 컴파일된다.

- [ ] **Step 4: 통과를 확인한다**

표의 아홉 갈래가 전부 초록이어야 한다. 하나라도 빨간불이면 문자열을 시험에 맞추지 말고 **어느 쪽이 옳은지 먼저 정하라** — 이 문자열은 세 표면이 공유한다.

Run: `go test ./internal/web/ -run 'TestCloseDeclaredLabel' -v -count=1 2>&1 | tail -30`

Expected: `--- PASS: TestCloseDeclaredLabel` 아래 9개 하위 갈래 PASS + `--- PASS: TestCloseDeclaredLabelSaysTheCountIsALowerBound` · `ok`

- [ ] **Step 5: 관문 — gofmt · vet · 패키지 전체**

교차 빌드 관문은 `go vet` 이다(`go build` 는 _test.go 를 건너뛴다). gofmt 는 **내 파일만** 본다 — actions_test.go 는 다른 담당이 지금 손대는 중이라 목록에 뜰 수 있다.

Run: `gofmt -l internal/web/format.go internal/web/format_test.go && go vet ./internal/web/ && go test ./internal/web/ -count=1 2>&1 | tail -3`

Expected: gofmt 무출력 · vet 무출력 · `ok github.com/kweiza/flightdeck/internal/web`

- [ ] **Step 6: 커밋**

이 커밋은 문자열 하나를 정한 것이다. 커밋 메시지가 **왜 세 상태인지**를 말한다.

Run: `git add plugins/flightdeck/server/internal/web/format.go plugins/flightdeck/server/internal/web/format_test.go && git commit -m "$(cat <<'EOF'
feat(flightdeck): 화면이 종료 선언을 문장으로 옮긴다 — 못 읽음을 0으로 접지 않는다

안 읽음·선언 없음·선언 있음 셋을 세 문장으로 가른다. 둘로 접으면 조회가 죽은
화면이 그 항목을 "깨끗하다"고 말한다. 같은 층의 선례가 Dependents 의 -1 이다.

수는 "최소 N건"으로 쓴다. flushDeferred 가 트랜잭션 ctx 를 그대로 쓰므로
클라이언트가 끊긴 마무리는 원장에 아예 없다 — 이 수는 하한이지 정확한 수가 아니다.

항목 CreatedAt 이전의 선언은 버린다. store 가 그 앵커를 일부러 안 걸고 넘긴다고
doc 에 적어 뒀다. 다만 앵커를 Last 하나로만 걸어 되살아난 id 에 옛 선언이 섞여
수가 부푸는 경우는 못 가른다 — 그것을 doc 에 적었다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"`

Expected: 커밋 하나. 파일 둘만 들어간다(다른 담당의 actions.go·actions_test.go 를 절대 담지 마라).

---

### Task 13: 두 시점을 잇는다 — 큐 표 · 회수 폼 · 폐기 폼(page.go + dashboard.gohtml)
**Files:**
- Create: internal/web/close_declared_test.go
- Modify: internal/web/page.go (88-94 ClaimTarget · 109-121 ItemRow · 131 Targets · 596-619 queuePanel · 642-665 itemRow · 667-689 claimTargets)
- Modify: internal/web/dashboard.gohtml (47 CSS · 149 회수 폼 option · 158 다섯→여섯 · 200 상태 칸 · 231 폐기 폼 option · 239 폐기 폼 꼬리 · 453 뒤 declText)

**Interfaces:**
- Consumes: 태스크 1 의 `CloseDeclUnread` · `CloseDeclaredLabel(d, read, created)`. 그리고 **이미 랜딩된** `(*store.Store).CloseDeclarationsByItem(ctx, project) (map[string]model.CloseDeclaration, error)`(internal/store/event.go:263).
- Produces: ItemRow.CloseDeclared string · ClaimTarget.Title/ClaimTarget.CloseDeclared · QueuePanel.Targets []ItemRow · (*handler).closeDeclarations(ctx, st, project) (map[string]model.CloseDeclaration, bool) · itemRow(ctx, st, it, holder, since, decls, declsRead) · 템플릿 {{define "declText"}}

- [ ] **Step 1: 실패 시험을 먼저 쓴다 — 두 시점 + 센티널**

새 파일 `internal/web/close_declared_test.go` 를 만든다. 절 단위로 좁히는 헬퍼 둘을 여기서 함께 판다(`nowSectionOf` 는 claim_filter_test.go 의 것을 그대로 쓴다 — 같은 패키지다).

```go
package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// 이 파일이 재는 것은 **두 시점**이다.
//
// 롤백된 finish 는 화면에서 두 번 읽혀야 한다. 한 번은 회수 폼에서(되돌릴 수 없는 행위를
// 저지르기 직전), 또 한 번은 큐 표에서(회수한 **뒤**). 회수 폼의 줄은 회수하는 순간
// 사라지는데 **사고는 그 다음에 난다** — open 이 된 그 항목을 pick 이 나이순 1순위로 냈다.
// 큐 표가 두 시점을 잇는 유일한 표면이다(설계 §4-⑤).
//
// ★ 이벤트를 **손으로 심는다.** 실물 원장에는 open/claimed + item.finish 조합이 지금 0건이라
// (실측: item.finish 384건 중 항목이 done 305 · dropped 75 · 행 없음 3, open/claimed 은 0)
// 실제 롤백으로는 이 자리를 못 밟는다. "롤백돼도 이벤트가 흘러간다"는 전제 자체는
// store 층이 실물 경로(선점 없는 finish)로 민다 — 여기서 겹쳐 밟지 않는다.

// declared 는 선점된 항목 하나에 **롤백된 종료 선언**을 심은 픽스처다.
func declared(t *testing.T, itemID string) (*fixture, string) {
	t.Helper()
	f := newFixture(t).withRepo("feat")
	sess := f.openSession("cc-1", "트랙2")
	f.claimOne(sess.ID, itemID) // 항목 등록 + 선점 (제목은 "<id> 제목")

	// payload 의 item·mode 키는 service/finish.go 가 실제로 싣는 것과 같다.
	// 여기서 어긋나면 store 가 그 행을 조용히 안 세고 시험이 초록으로 거짓말한다.
	if err := f.st.TryLogEvent(context.Background(), "item.finish", testProject, sess.ID,
		map[string]any{"item": itemID, "mode": "done", "count": 0, "bytes": 10300}); err != nil {
		t.Fatalf("종료 선언 이벤트 심기 실패: %v", err)
	}
	return f, sess.ID
}

// queueTableOf 는 ③ 안에서 **항목 표만** 잘라낸다 — 탈락 사유 분포 절과 폐기 폼 앞에서 끊는다.
//
// ★ 페이지 전체에 단정을 걸면 다른 절이 우연히 같은 문자열을 내는 순간 조용히 거짓 초록이
// 된다. 이 패키지가 실제로 그 값을 치렀다(claim_filter_test·lane_panel_test 의 머리말).
func queueTableOf(t *testing.T, html string) string {
	t.Helper()
	i := strings.Index(html, `<section id="queue">`)
	if i < 0 {
		t.Fatal("섹션 ③이 화면에 없다")
	}
	sec := html[i:]
	j := strings.Index(sec, "탈락 사유 분포")
	if j < 0 {
		t.Fatal("③의 항목 표 끝(탈락 사유 분포 절)을 못 찾았다 — 이 헬퍼의 전제가 깨졌다")
	}
	return sec[:j]
}

// dropFormOf 는 폐기 폼 하나만 잘라낸다. 큐 표와 **다른 표면**이라 따로 잰다 —
// 시점 B 의 올바른 처분 경로가 폐기다.
func dropFormOf(t *testing.T, html string) string {
	t.Helper()
	i := strings.Index(html, `action="actions/drop`)
	if i < 0 {
		t.Fatal("폐기 폼이 화면에 없다")
	}
	sec := html[i:]
	j := strings.Index(sec, "</form>")
	if j < 0 {
		t.Fatal("폐기 폼이 안 닫혔다")
	}
	return sec[:j]
}

// ─────────────────────────── 시점 A — 롤백 직후(claimed) ───────────────────────────

// 회수 폼은 **되돌릴 수 없는 행위를 저지르는 마지막 한 줄**이다. 그 줄이 침묵하면
// 사람은 "놀고 있는 선점"을 회수하고, 그 다음 pick 이 그것을 1순위로 낸다.
func TestReclaimFormNamesTheRolledBackFinish(t *testing.T) {
	f, sess := declared(t, "it-rolled")

	_, html := f.get("")
	now := nowSectionOf(t, html)

	// 전제 — 그 항목이 회수 대상으로 실제로 올라와 있다. 안 올라와 있으면 아래 단정들은
	// "표기가 붙었다"가 아니라 "줄이 애초에 없다"를 통과시킨다.
	mustContain(t, now, `<option value="it-rolled">it-rolled ←`,
		"전제 실패 — 선점이 회수 폼에 없다")

	mustContain(t, now, "종료 선언 최소 1건",
		"회수 폼이 롤백된 마무리를 침묵한다 — 이 줄이 그것을 말할 마지막 자리다")
	mustContain(t, now, "done 1 · dropped 0",
		"mode 별 수가 없다 — done 과 dropped 는 처방이 갈린다")
	mustContain(t, now, "· 세션 "+short(sess),
		"누가 선언했는지가 없다 — 그 세션의 판단이 죽은 본문을 되짚을 유일한 실마리다")
	mustContain(t, now, "it-rolled 제목",
		"회수 폼 줄에 제목이 없다 — id 만으로는 무엇을 회수하는지 사람이 모른다")

	// 그리고 이 수가 정확한 수인 척하지 않는다(문구는 ③의 폐기 폼 꼬리에 있다).
	mustContain(t, html, "그 수는 하한이다",
		"원장에 안 써진 마무리가 있을 수 있다는 사실이 화면 어디에도 없다")
}

// ─────────────────────────── 시점 B — 회수 후(open) ───────────────────────────

// 사고는 **여기서** 났다. 회수 폼의 줄은 사라졌고 항목은 open 이 됐다.
func TestQueueTableKeepsTheDeclarationAfterReclaim(t *testing.T) {
	f, _ := declared(t, "it-rolled")

	rec := f.post("/actions/reclaim", url.Values{
		"project": {testProject}, "item": {"it-rolled"},
		"reason":  {"세션이 사라졌고 발자국도 없다 — 근거 축을 보고 회수한다"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("회수 status = %d, 기대 303\n%s", rec.Code, rec.Body.String())
	}

	_, html := f.get("")

	// 전제 — 회수가 실제로 됐다(항목은 open·무점유). 회수 폼의 줄이 사라진 것으로 잰다.
	mustNotContain(t, nowSectionOf(t, html), `<option value="it-rolled">it-rolled ←`,
		"전제 실패 — 회수했는데 선점이 남아 있다")

	table := queueTableOf(t, html)
	mustContain(t, table, "it-rolled", "회수된 항목이 큐 표에서 사라졌다 — 회수는 폐기가 아니다")
	mustContain(t, table, "종료 선언 최소 1건",
		"회수 뒤 큐 표가 침묵한다 — 회수 폼의 줄은 사라졌고 사고는 바로 이 다음에 난다")
	mustContain(t, table, "mode=done",
		"무엇으로 닫으려 했는지가 없다 — done 과 dropped 는 처방이 갈린다")

	// 폐기 폼도 **같은 문장**을 얻는다. 이 시점의 올바른 처분 경로가 폐기인데
	// 그 select 가 id 만 내면 사람은 같은 사실을 두 번째 표면에서 다시 못 읽는다.
	drop := dropFormOf(t, html)
	mustContain(t, drop, "it-rolled", "폐기 폼에 그 항목이 없다")
	mustContain(t, drop, "it-rolled 제목", "폐기 폼 줄에 제목이 없다")
	mustContain(t, drop, "종료 선언 최소 1건", "폐기 폼이 id 만 낸다 — 두 번째 표면이 침묵한다")
}

// ─────────────────────────── 못 읽음은 0이 아니다 ───────────────────────────

func TestUnreadCloseDeclarationAxisIsNotFoldedIntoZero(t *testing.T) {
	f, _ := declared(t, "it-unread")

	// 원장을 통째로 감춘다 — 이 축의 조회를 실패시키는 유일한 길이고,
	// service 층 시험이 같은 관용구를 쓴다(finish_partial_test.go 의 RENAME TO … _hidden).
	if _, err := f.st.DB().Exec(`ALTER TABLE event RENAME TO event_hidden`); err != nil {
		t.Fatalf("원장 감추기 실패: %v", err)
	}

	_, html := f.get("")
	table := queueTableOf(t, html)

	mustContain(t, table, "종료 선언 축을 못 읽었다",
		"못 읽은 축을 '선언 없음'으로 접었다 — 0값과 미관측을 뭉갠 것이다")
	mustNotContain(t, table, "종료 선언 최소",
		"못 읽었는데 수를 지어냈다")
	// 그리고 큐 표는 통째로 안 죽는다 — 한 축의 실패가 화면을 지우면 사람이 추측으로 돌아간다.
	mustContain(t, table, "it-unread", "한 축을 못 읽었다고 항목 줄이 사라졌다")
}

// 이 시험이 "실패를 pan.Err 에 안 담는다"는 판단의 유일한 가드다.
//
// queuePanel 은 `len(pan.Items) == 0 && pan.Err == ""` 일 때만 "큐가 비었다"를 쓴다.
// 종료 선언 조회 실패를 pan.Err 에 담으면 **원장 한 축을 못 읽은 것이 큐가 비었다는
// 참인 문장까지 지운다.**
func TestUnreadAxisDoesNotEraseTheEmptyQueueSentence(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	f.openSession("cc-1", "트랙2")
	if _, err := f.st.DB().Exec(`ALTER TABLE event RENAME TO event_hidden`); err != nil {
		t.Fatalf("원장 감추기 실패: %v", err)
	}

	_, html := f.get("")
	mustContain(t, queueTableOf(t, html), "큐가 비었다 — 열린 항목도 선점된 항목도 없다",
		"종료 선언 축을 못 읽은 것이 '큐가 비었다'는 참인 문장을 지웠다 — 그래서 이 실패는 pan.Err 이 아니다")
}
```

Expected: 파일이 저장된다. 컴파일은 된다(새 심볼을 안 쓴다) — 단정에서 빨간불이 나야 정상이다.

- [ ] **Step 2: 실패를 확인한다 — **빨간불의 이유까지** 본다**

넷 중 셋이 단정 실패여야 한다. `TestUnreadAxisDoesNotEraseTheEmptyQueueSentence` 는 지금 코드에서도 통과한다(아직 pan.Err 을 안 쓰므로) — 그것이 정상이다. 이 시험은 앞으로 그 판단이 뒤집히는 것을 막는 가드다.

Run: `go test ./internal/web/ -run 'TestReclaimFormNamesTheRolledBackFinish|TestQueueTableKeepsTheDeclarationAfterReclaim|TestUnreadCloseDeclarationAxisIsNotFoldedIntoZero|TestUnreadAxisDoesNotEraseTheEmptyQueueSentence' -v -count=1 2>&1 | tail -30`

Expected: FAIL 셋: `HTML 에 "종료 선언 최소 1건" 이 없다` (회수 폼) · 같은 것 (큐 표) · `HTML 에 "종료 선언 축을 못 읽었다" 가 없다`. PASS 하나: TestUnreadAxisDoesNotEraseTheEmptyQueueSentence.

- [ ] **Step 3: page.go ① — ClaimTarget 에 제목과 표기를 싣는다**

page.go:88-94. before(정확히 이 블록):

```go
// ClaimTarget 은 회수 가능한 선점 하나다. **근거를 함께 낸다**(설계 §4 의 다섯 축 중 표시분).
type ClaimTarget struct {
	ItemID string
	Holder string
	Since  string
	Live   string // 그 세션이 창 안에 있나 — 판정이 아니라 사실 표기다
}
```

after:

```go
// ClaimTarget 은 회수 가능한 선점 하나다. **근거를 함께 낸다**(설계 §4 의 여섯 축 중 표시분).
//
// ★ 여섯째가 종료 선언이다 — "이 항목을 닫으려다 롤백된 적이 있다". 앞의 다섯(신호 나이·
// 발자국·선점 시각·브랜치·ahead)은 전부 **세션**에 대한 사실이고, 이것만 **항목**에 대한
// 사실이다. 그래서 카드가 아니라 이 줄에 실린다.
type ClaimTarget struct {
	ItemID string
	Title  string // 회수 폼 줄은 이것이 전부다 — 제목이 없으면 무엇을 회수하는지 id 로만 판단한다
	Holder string
	Since  string
	Live   string // 그 세션이 창 안에 있나 — 판정이 아니라 사실 표기다

	// CloseDeclared 는 ItemRow 와 **같은 규약**이다(빈 문자열=선언 없음, CloseDeclUnread=못 읽음).
	// 여기서 다시 계산하지 않고 ItemRow 에서 그대로 옮긴다 — 두 자리에서 따로 세면
	// 같은 이름이 표면마다 다른 사실을 내는 어긋남이 재현된다(설계 §4-⑤).
	CloseDeclared string
}
```

Expected: 컴파일은 아직 안 본다(다음 편집과 함께 본다).

- [ ] **Step 4: page.go ② — ItemRow 에 축을 하나 더 단다**

page.go:109-121. before:

```go
// ItemRow 는 섹션 ③ 의 항목 한 줄이다.
type ItemRow struct {
	ID         string
	Title      string
	Body       string
	State      string
	Paths      []string
	Labels     []string
	After      []string
	Dependents int
	Holder     string
	Since      string
}
```

after(**새 필드를 빈 줄로 떼어 놓는다** — 같은 정렬 그룹에 넣으면 gofmt 가 열 전체를 다시 맞춰 diff 가 무의미하게 커진다):

```go
// ItemRow 는 섹션 ③ 의 항목 한 줄이다.
type ItemRow struct {
	ID         string
	Title      string
	Body       string
	State      string
	Paths      []string
	Labels     []string
	After      []string
	Dependents int
	Holder     string
	Since      string

	// CloseDeclared 는 이 항목을 닫으려다 롤백된 선언의 표기다(format.go 의 CloseDeclaredLabel).
	//
	// **빈 문자열 = 선언 없음 · CloseDeclUnread("?") = 이 축을 못 읽었다.** 셋째 상태를
	// 0으로 접지 않는 것이 이 층의 규율이고, 같은 구조체의 Dependents = -1 이 선례다.
	//
	// ★ 사실을 **여기 하나에** 싣는다. buildPage 가 회수 폼의 원천을 큐 Items 로 삼으므로
	// (p.Live.Targets = p.Queue.claimTargets(board)) 큐 표·회수 폼·폐기 폼 셋이 배선 없이
	// 같은 문장을 얻는다. 표면마다 따로 계산하면 같은 이름이 다른 사실을 낸다.
	//
	// ★ ④ 랜딩 이력의 ItemRow 는 이 값을 안 채운다(빈 문자열). 그쪽은 이미 닫힌 항목이라
	// 이 축이 겨냥하는 인구가 아니다 — 롤백된 선언은 항목이 **아직 열려 있을 때만** 사고다.
	CloseDeclared string
}
```

- [ ] **Step 5: page.go ③ — 폐기 폼의 선택지가 줄 전체를 받는다**

page.go:131. before(한 줄):

```go
	Targets  []string // 항목 폐기 폼의 선택지
```

after:

```go

	// Targets 는 항목 폐기 폼의 선택지다. **줄 전체를 나른다** — id 만 나르면 폐기 폼이
	// 같은 사실을 다시 계산해야 하고, 두 자리에서 따로 계산하는 순간 같은 이름이
	// 표면마다 다른 사실을 내는 어긋남이 재현된다(설계 §4-⑤).
	Targets []ItemRow
```

남는 정렬 그룹(Items·Empty·Stats·StatsErr·Window)의 최장 이름은 여전히 StatsErr 이므로 **다른 줄은 안 건드려도 gofmt 가 조용하다.**

- [ ] **Step 6: page.go ④ — queuePanel 이 원장을 한 번 긁는다**

page.go:596-598. before:

```go
	for _, it := range board.OpenItems {
		pan.Items = append(pan.Items, h.itemRow(ctx, st, it, "", time.Time{}))
	}
```

after:

```go
	// 종료 선언 축은 **여기서 한 번만** 읽는다. 줄마다 읽으면 큐 길이만큼 원장을 긁는다.
	//
	// ★ 실패를 pan.Err 에 담지 않는다. 이유 둘 —
	//   ① pan.Err 은 ③의 머리말이라 **회수 폼(섹션 ①)에는 원리적으로 안 닿는다.** 이 축은
	//      그 폼의 <option> 에도 실리는데, 배너로만 두면 되돌릴 수 없는 그 한 줄이 침묵한 채
	//      "선언 없음"과 구분되지 않는다.
	//   ② pan.Err 이 차면 아래 `len(pan.Items) == 0 && pan.Err == ""` 가 막혀 "큐가 비었다"라는
	//      **참인 문장**이 사라진다. 원장 한 축을 못 읽은 것이 큐가 비었다는 사실까지 지우면
	//      안 된다.
	// 대신 줄마다 센티널(CloseDeclUnread)을 싣고 사유 전문은 서버 로그에 남긴다.
	decls, declsRead := h.closeDeclarations(ctx, st, project)

	for _, it := range board.OpenItems {
		pan.Items = append(pan.Items, h.itemRow(ctx, st, it, "", time.Time{}, decls, declsRead))
	}
```

그리고 page.go:614(선점 항목 갈래). before:

```go
		pan.Items = append(pan.Items, h.itemRow(ctx, st, it, hd.SessionID, hd.At))
```

after:

```go
		pan.Items = append(pan.Items, h.itemRow(ctx, st, it, hd.SessionID, hd.At, decls, declsRead))
```

그리고 page.go:617-619(폐기 폼 선택지). before:

```go
	for _, it := range pan.Items {
		pan.Targets = append(pan.Targets, it.ID)
	}
```

after:

```go
	for _, it := range pan.Items {
		pan.Targets = append(pan.Targets, it)
	}
```

- [ ] **Step 7: page.go ⑤ — 조회 헬퍼와 itemRow**

page.go:642 의 `itemRow` 를 통째로 갈고 그 **앞에** 헬퍼를 둔다. before(642-665, 함수 전체):

```go
func (h *handler) itemRow(ctx context.Context, st *store.Store, it model.Item,
	holder string, since time.Time) ItemRow {

	r := ItemRow{
		ID: it.ID, Title: it.Title, Body: Clip(it.Body, 300),
		State: string(it.State), Paths: it.Paths, Labels: it.Labels, Holder: holder,
	}
```

after(머리만 갈고 뒤쪽 본문은 그대로 둔다):

```go
// closeDeclarations 는 종료 선언 축을 한 번 읽는다.
//
// 두 번째 반환값이 false 면 **못 읽은 것**이고, 그때 화면은 0건이 아니라 센티널을 낸다.
// (값, bool) 두 반환값을 고른 이유는 nil 맵이 Go 에서 zero 를 내기 때문이다 — nil 을
// "안 읽음"으로 쓰면 빈 맵과 바이트 단위로 같은 출력이 되어 가를 관측점이 없어진다.
// service/pick.go 가 SiblingIndex 에서 같은 판단을 이미 내렸다.
func (h *handler) closeDeclarations(ctx context.Context, st *store.Store,
	project string) (map[string]model.CloseDeclaration, bool) {

	d, err := st.CloseDeclarationsByItem(ctx, project)
	if err != nil {
		// 삼키지 않는다. 다만 이 한 축 때문에 큐 표를 통째로 버리지도 않는다 —
		// 역인덱스 실패가 항목 줄을 안 버리는 것과 같은 자리다.
		h.log.WarnContext(ctx, "종료 선언 축 조회 실패",
			"project", Clip(project, 64), "error", err.Error())
		return nil, false
	}
	return d, true
}

func (h *handler) itemRow(ctx context.Context, st *store.Store, it model.Item,
	holder string, since time.Time,
	decls map[string]model.CloseDeclaration, declsRead bool) ItemRow {

	r := ItemRow{
		ID: it.ID, Title: it.Title, Body: Clip(it.Body, 300),
		State: string(it.State), Paths: it.Paths, Labels: it.Labels, Holder: holder,
		// ★ 앵커는 여기서 건다. store 는 원자료만 내고 "지웠다 다시 만든 id 의 옛 선언이
		// 그대로 들어 있다"고 doc 에 적어 뒀다 — 그 판정은 items 를 손에 쥔 이쪽 일이다.
		CloseDeclared: CloseDeclaredLabel(decls[it.ID], declsRead, it.CreatedAt),
	}
```

**주의:** `decls` 가 nil 이어도 `decls[it.ID]` 는 zero 를 낸다 — 그리고 그때 `declsRead` 가 false 라 `CloseDeclaredLabel` 이 첫 줄에서 센티널로 빠진다. 나머지 본문(`if !since.IsZero()` … `return r`)은 **한 글자도 안 고친다.**

- [ ] **Step 8: page.go ⑥ — claimTargets 가 제목과 표기를 옮긴다**

page.go:682. before:

```go
		t := ClaimTarget{ItemID: it.ID, Holder: it.Holder, Since: it.Since, Live: "창 밖 세션"}
```

after:

```go
		// ★ 여기서 다시 계산하지 않는다. ItemRow 가 정본이고 이 줄은 옮기기만 한다 —
		//   두 표면이 같은 이름으로 다른 사실을 내는 것이 이 항목이 고치는 병이다.
		t := ClaimTarget{ItemID: it.ID, Title: it.Title, Holder: it.Holder, Since: it.Since,
			Live: "창 밖 세션", CloseDeclared: it.CloseDeclared}
```

- [ ] **Step 9: page.go 컴파일 확인 — 템플릿 고치기 전에 한 번 끊는다**

여기서 vet 을 돌리면 템플릿이 아직 `[]string` 을 가정하므로 **Go 는 초록인데 화면은 깨진 상태**다. 그 상태를 한 번 눈으로 확인하고 다음 단계로 간다 — 두 편집을 한 덩이로 묶으면 어느 쪽이 깨졌는지 못 가른다.

Run: `gofmt -l internal/web/page.go && go vet ./internal/web/`

Expected: gofmt 무출력 · vet 무출력. (`go test` 는 아직 돌리지 마라 — 템플릿이 `{{.}}` 로 구조체를 찍어 회수/폐기 폼 시험이 엉뚱한 이유로 깨진다.)

- [ ] **Step 10: 템플릿 ① — CSS 한 줄과 declText 정의**

dashboard.gohtml:46-47. before:

```
.paths { font-size:12px; margin:6px 0 0; padding-left:16px; }
.none { color:var(--warn); font-size:12px; }
```

after:

```
.paths { font-size:12px; margin:6px 0 0; padding-left:16px; }
.none { color:var(--warn); font-size:12px; }
/* 종료 선언 — 닫으려다 롤백된 시도. 경고색보다 세게 낸다: 이 표기가 붙은 항목은
   "아직 안 한 일"이 아니라 "이미 끝났다고 선언됐던 일"이다. */
.decl { color:var(--crit); font-weight:600; font-size:12px; }
```

그리고 파일 **맨 끝**(`{{define "note"}}` … `{{end}}` 뒤)에 덧붙인다:

```
{{/* declText 는 CloseDeclared 한 값의 표기다. 값의 규약은 format.go 의 CloseDeclaredLabel 이
     정한다 — 빈 문자열은 "선언 없음"이라 호출부의 {{with}} 가 이미 걸러 여기 안 온다.
     "?" 는 CloseDeclUnread 다. **여기 한 자리에서만** 문장으로 옮긴다 — 세 표면(큐 표·
     회수 폼·폐기 폼)이 같은 사실을 다르게 말하면 그 자체가 이 항목이 고치는 병이다.
     한 줄로 쓴다: <option> 안에 들어가므로 줄바꿈이 그대로 새어 나간다. */}}
{{define "declText"}}{{if eq . "?"}}종료 선언 축을 못 읽었다 — 없다는 뜻이 아니다{{else}}{{.}}{{end}}{{end}}
```

- [ ] **Step 11: 템플릿 ② — 회수 폼 <option>(제목 + 표기)**

dashboard.gohtml:149. before(들여쓰기 8칸, 한 줄):

```
        {{range .Live.Targets}}<option value="{{.ItemID}}">{{.ItemID}} ← {{.Holder}} ({{.Live}}{{if .Since}}, {{.Since}}부터{{end}})</option>{{end}}
```

after:

```
        {{/* 제목과 종료 선언을 **뒤에** 붙인다. `{{.ItemID}} ← ` 접두는 actions_test.go 가
             선점의 생사를 재는 자리라(회수 전/후 두 번) 그 모양을 안 건드린다. */}}
        {{range .Live.Targets}}<option value="{{.ItemID}}">{{.ItemID}} ← {{.Holder}} ({{.Live}}{{if .Since}}, {{.Since}}부터{{end}}){{with .Title}} · {{.}}{{end}}{{with .CloseDeclared}} · {{template "declText" .}}{{end}}</option>{{end}}
```

그리고 dashboard.gohtml:158. before:

```
    <span class="k">근거 다섯 축은 위 카드에 있다. 회수는 사람만 하고, 사유는 원장에 남는다.</span>
```

after:

```
    <span class="k">근거 다섯 축은 위 카드에 있고, 여섯째(종료 선언)는 이 줄에 있다.
      회수는 사람만 하고, 사유는 원장에 남는다.</span>
```

- [ ] **Step 12: 템플릿 ③ — 큐 표 상태 칸 배지**

dashboard.gohtml:200. before(들여쓰기 6칸):

```
      <td>{{.State}}</td>
```

after:

```
      {{/* 상태 칸에 붙인다 — 이 배지는 "open 인데 이미 닫으려던 적이 있다"라는
           상태에 대한 단서지 별개의 축이 아니다. 열을 늘리면 표가 옆으로 터진다. */}}
      <td>{{.State}}{{with .CloseDeclared}}<div class="decl">{{template "declText" .}}</div>{{end}}</td>
```

**주의:** 이 `<td>` 는 `<tbody class="fold">` 안이다. `{{with}}`/`{{end}}` 가 같은 `<td>` 안에서 짝을 이루므로 template_balance 검사기(tbody 구간의 블록 균형)를 통과한다 — `{{template}}` 은 그 검사기의 정규식(range|if|with|block|define|end)에 안 걸린다.

- [ ] **Step 13: 템플릿 ④ — 폐기 폼 <option> 과 뜻을 정하는 한 줄**

dashboard.gohtml:231. before(들여쓰기 8칸):

```
        {{range .Queue.Targets}}<option value="{{.}}">{{.}}</option>{{end}}
```

after(**`←` 를 쓰지 마라** — actions_test.go 가 회수 폼의 줄을 `"…">t5-a ←"` 로 세고, 폐기 폼이 같은 모양을 내면 회수 뒤 mustNotContain 이 거짓 빨간불을 낸다):

```
        {{range .Queue.Targets}}<option value="{{.ID}}">{{.ID}}{{with .Title}} — {{.}}{{end}}{{with .CloseDeclared}} · {{template "declText" .}}{{end}}</option>{{end}}
```

그리고 dashboard.gohtml:239-240. before:

```
    <button type="submit" {{if not .Queue.Targets}}disabled{{end}}>항목 폐기</button>
  </form>
```

after(**새 폼을 만들지 않는다** — 기존 폼 안에 설명 한 줄만 넣는다):

```
    <button type="submit" {{if not .Queue.Targets}}disabled{{end}}>항목 폐기</button>
    {{/* 이 한 줄이 위 배지의 뜻을 정한다. 배지만 있고 뜻이 없으면 사람은 그것을 장식으로 읽는다. */}}
    <span class="k">★ <b>종료 선언</b>이 붙은 항목은 이미 한 번 닫으려다 롤백된 것이다 —
      연결된 판단부터 읽어라(그 본문은 롤백과 함께 죽었다). 그 수는 하한이다: 원장에
      안 써진 마무리가 있을 수 있다.</span>
  </form>
```

- [ ] **Step 14: 통과를 확인한다 — 새 시험 넷**



Run: `go test ./internal/web/ -run 'TestReclaimFormNamesTheRolledBackFinish|TestQueueTableKeepsTheDeclarationAfterReclaim|TestUnreadCloseDeclarationAxisIsNotFoldedIntoZero|TestUnreadAxisDoesNotEraseTheEmptyQueueSentence' -v -count=1 2>&1 | tail -20`

Expected: PASS 넷 · `ok`. 만약 `TestUnreadCloseDeclarationAxisIsNotFoldedIntoZero` 가 500 이나 빈 표로 깨지면 event 표를 감춘 것이 보드까지 죽인 것이다 — service/board.go 가 AckReach 실패를 파생 실패로만 남기므로 그럴 리 없지만, 그때는 `f.get` 대신 `queuePanel` 이 낸 것을 로그로 확인하라.

- [ ] **Step 15: 회귀 — 기존 폼 관문 셋이 안 깨졌는지 **명시적으로** 본다**

이 세 시험이 이번 편집의 진짜 위험 지점이다: 폼/POST/사유 개수 락(render_test.go:399-412)과 회수 폼 줄 모양(actions_test.go:35·57·87).

Run: `go test ./internal/web/ -run 'TestWriteFormsAreAtMostFourAndAllRequireReason|TestReclaim|TestDrop|TestPageHasSixSections|TestDashboardTemplateWrappersAreBalanced|TestItemTitleIsEscaped' -v -count=1 2>&1 | grep -E "^(---|    ---|ok|FAIL)" | head -30`

Expected: 전부 PASS. `폼 N개 — 넷을 넘었다` · `POST 폼 N개` · `<option value="t5-a">t5-a ←` 관련 실패가 0건이어야 한다.

- [ ] **Step 16: 관문 — gofmt · vet · 패키지 전체 · 전 저장소**

교차 빌드 관문은 `go vet` 이다(`go build` 는 _test.go 를 건너뛴다). gofmt 는 **내 파일만** 지목한다 — 다른 담당이 손대는 중인 파일이 목록에 뜨면 건드리지 말고 그대로 두라.

Run: `gofmt -l internal/web/page.go internal/web/close_declared_test.go && go vet ./... && go test ./internal/web/ -count=1 2>&1 | tail -3 && go test ./... 2>&1 | grep -E "^(FAIL|---)" | head -20`

Expected: gofmt 무출력 · vet 무출력 · `ok …/internal/web` · 전 저장소 FAIL 0건(다른 담당이 진행 중인 패키지의 빨간불은 내 것과 분리해 보고하라).

- [ ] **Step 17: 커밋**



Run: `git add plugins/flightdeck/server/internal/web/page.go plugins/flightdeck/server/internal/web/dashboard.gohtml plugins/flightdeck/server/internal/web/close_declared_test.go && git commit -m "$(cat <<'EOF'
feat(flightdeck): 롤백된 마무리를 화면이 두 시점에서 말한다

회수 폼의 줄은 회수하는 순간 사라지는데 사고는 그 다음에 났다 — open 이 된 항목을
pick 이 나이순 1순위로 냈다. 그래서 큐 표가 두 시점을 잇는다: 롤백 직후(claimed)는
회수 폼이, 회수 후(open)는 큐 표와 폐기 폼이 같은 문장을 낸다.

사실은 ItemRow 하나에 싣는다. buildPage 가 회수 폼의 원천을 큐 Items 로 삼으므로
세 표면이 배선 없이 같은 문장을 얻는다 — 표면마다 따로 계산하면 같은 이름이 다른
사실을 내고, 그 어긋남이 이 항목이 고치는 병이다. 폐기 폼의 Targets 를 id 목록에서
줄 목록으로 바꾼 것도 같은 이유다.

조회 실패는 pan.Err 이 아니라 줄 센티널 + 로그다. pan.Err 은 ③의 머리말이라
회수 폼(섹션 ①)에 원리적으로 안 닿고, 차는 순간 "큐가 비었다"라는 참인 문장까지
지운다. 그 판단을 시험 하나로 잠갔다.

새 폼을 안 만들었다(폼 4·POST 3 락은 그대로). 회수 폼의 `id ← 점유자` 접두도
안 건드렸다 — actions_test 가 선점의 생사를 그 모양으로 잰다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"`

Expected: 커밋 하나. 파일 셋만. actions.go·actions_test.go(폐기 액션 담당)를 절대 담지 마라.

---

### Task 14: 실패 시험 — 폐기가 claim 행을 '사유째로' 닫는지 잠근다
**Files:**
- Test: internal/web/actions_test.go (import 블록 3-13 수정 · 158줄 `func TestGetOnActionPathIs404` 앞에 시험 삽입)

**Interfaces:**
- Consumes: 없다. 이 태스크는 이 항목의 다른 층(store/judge/service/mcpsrv/web-page)과 파일이 하나도 안 겹친다 — actions.go · actions_test.go 둘뿐이라 순서 제약 없이 언제 실행해도 된다.
- Produces: `func TestDropClosesTheClaimWithTheDropReason(t *testing.T)` — internal/web/actions_test.go. 하위 시험 둘: `선점된_항목`(t5-e) · `선점_없는_항목`(t5-f). 단정 축 넷: HTTP 303 · item.State==dropped · claim.ForceReason == 폐기 사유 · 판단 본문 한 줄이 화면에 있다.

- [ ] **Step 1: 현재 코드를 먼저 읽어 전제를 확인한다**

이 시험이 무엇을 잡는지가 **스펙 §4-⑥ 과 다르다.** 실측으로 확인한 사실:

- `store/item.go:512-527` 의 `SetItemState` 는 종료 상태(done/dropped)에서 살아 있는 선점을 **스스로 반납한다**
  (`UPDATE claim SET released_at = ? WHERE ... AND released_at IS NULL`).
- 그래서 지금도 폐기 뒤 `released_at` 은 **찍힌다.** 스펙이 말한 "claim 행이 released_at=NULL 로 남는다"는 **거짓**이다.
- 실제로 비어 있는 것은 `claim.force_reason` 뿐이다 — 선점을 왜 끊었는지가 원장에 없다.

관측 근거(내가 임시 시험으로 실제로 재 본 값):
```
고침 전:  item state=dropped   claim released=2026-08-09 02:31:28 +0000 UTC  force=""
고침 후:  item state=dropped   claim released=…                              force="선점한 세션이 사라졌고 …"
```

그러므로 **단정을 `released_at` 에 걸면 이 시험은 고침이 통째로 없어도 초록이다.** 관측점은 `force_reason` 이다.

Run: `sed -n '505,532p' internal/store/item.go && sed -n '757,790p' internal/store/item.go && sed -n '210,254p' internal/web/actions.go`

Expected: item.go:512-527 에 "★ 종료하면 선점도 함께 반납한다" 주석과 claim UPDATE 가 보인다. item.go:779-781 의 `if n == 0 { return notFound(NFLiveClaim, ...) }` 가 782줄의 `UPDATE item SET state='open'` **앞**에 있다. actions.go:237 은 SetItemState 만 친다.

- [ ] **Step 2: import 에 errors · store 를 더한다**

actions_test.go 3-13줄. before(공백까지 정확):
```go
import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)
```
after:
```go
import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
	"github.com/kweiza/flightdeck/internal/store"
)
```

Expected: Go 는 import 를 파일마다 따로 본다 — render_test.go 가 store 를 import 해도 actions_test.go 는 자기 것이 필요하다.

- [ ] **Step 3: 시험을 삽입한다 (158줄 TestGetOnActionPathIs404 앞)**

아래를 `func TestGetOnActionPathIs404(t *testing.T) {` **바로 앞**에 통째로 넣는다.

★ **주석 들여쓰기 함정** — 이 저장소에는 gofmt 관문 시험이 있다(`internal/service/gofmt_gate_test.go`, 모듈 전체 + _test.go 를 본다). 선언 바로 앞의 **doc 주석**에서 이어지는 줄을 `//   ` 처럼 들여쓰면 gofmt 가 그것을 코드블록으로 재작성해 관문이 빨간불이 된다. doc 주석은 아래처럼 **평평한 `// `** 로만 쓴다(함수 **본문 안** 주석은 들여써도 된다 — 그 자리는 gofmt 가 안 건드린다). 이 규칙을 어겨서 실제로 한 번 빨간불을 받았다.

```go
// 폐기는 그 항목의 선점을 **사유째로** 닫는다.
//
// ★ 관측점이 released_at 이 아니라 force_reason 인 이유를 적어 둔다. 안 적으면 다음
// 사람이 "released_at 이 더 직관적인데"라며 단정을 옮기고, 그 순간 이 시험이 아무것도
// 안 잡게 된다.
//
// released_at 은 이 고침 **이전에도** 찍혔다 — SetItemState 가 종료 상태에서 살아 있는
// 선점을 스스로 반납하기 때문이다(store/item.go:512-527). 그래서 released_at 만 보면
// 세 구현이 전부 초록이다: ⓐ ForceReleaseClaim 을 아예 안 부르는 것, ⓑ SetItemState
// **뒤에** 부르는 것(그 자리에서는 UPDATE 가 0행이라 NFLiveClaim 으로 빠져 조용히
// 무동작이 된다), ⓒ **앞에** 부르는 올바른 것. 셋을 가르는 관측점은 force_reason 뿐이다.
//
// ★ 그리고 이 사실은 화면에도 있어야 한다. force_reason 은 지금 어느 표면도 안 읽으므로
// (page.go·dashboard.gohtml 전수 확인) 원장에만 두면 선점을 잃은 쪽에서는 그 사실이
// 영영 안 보인다. 그래서 판단 본문의 한 줄을 함께 단정한다.
func TestDropClosesTheClaimWithTheDropReason(t *testing.T) {
	const reason = "설계에서 빠진 축이라 이 항목은 성립하지 않는다 — 버린다"

	cases := []struct {
		name     string
		item     string
		claim    bool   // 폐기 전에 선점을 걸까
		wantRow  bool   // claim 행이 있어야 하나
		wantWhy  string // 기대하는 claim.force_reason
		wantLine string // 판단 본문이 화면에서 말해야 하는 한 줄
	}{
		{
			name: "선점된 항목", item: "t5-e", claim: true, wantRow: true, wantWhy: reason,
			wantLine: "선점: 살아 있던 선점을 이 폐기와 같은 트랜잭션에서 함께 닫았다",
		},
		{
			name: "선점 없는 항목", item: "t5-f", claim: false, wantRow: false, wantWhy: "",
			wantLine: "선점: 폐기 시점에 살아 있는 선점이 없었다",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t).withRepo("feat")
			sess := f.openSession("cc-1", "트랙2")
			if c.claim {
				f.claimOne(sess.ID, c.item)
			} else {
				f.addItem(c.item, c.item+" 제목", nil, nil)
			}

			rec := f.post("/actions/drop", url.Values{
				"project": {testProject}, "item": {c.item}, "reason": {reason},
			})
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("status = %d, 기대 303 — 선점 유무가 폐기의 성패를 가르면 안 된다\n%s",
					rec.Code, rec.Body.String())
			}

			ctx := context.Background()
			it, err := f.st.GetItem(ctx, testProject, c.item)
			if err != nil {
				t.Fatalf("항목 조회 실패: %v", err)
			}
			// ★ 순서 가드다. ForceReleaseClaim 은 `UPDATE item SET state='open' …
			//   AND state='claimed'` 를 함께 치므로(store/item.go:783), 폐기를 먼저 찍고
			//   그 뒤에 회수하면 방금 닫은 항목이 다시 열린다.
			if it.State != model.ItemDropped {
				t.Fatalf("항목 상태 = %q, 기대 dropped — 선점 회수가 폐기를 되돌렸다", it.State)
			}

			claim, cerr := f.st.GetClaim(ctx, testProject, c.item)
			switch {
			case !c.wantRow:
				if !errors.Is(cerr, store.ErrNotFound) {
					t.Fatalf("선점 없는 항목에 claim 행이 생겼다(err=%v) — 폐기가 없던 선점을 만들었다", cerr)
				}
			case cerr != nil:
				t.Fatalf("선점 조회 실패: %v", cerr)
			default:
				if claim.ReleasedAt == nil {
					t.Fatal("폐기했는데 claim 행이 released_at = NULL 로 남았다 — " +
						"claim 표에는 만료 컬럼이 없어 그 세션은 닫힌 항목의 선점을 영영 쥔다")
				}
				if claim.ForceReason != c.wantWhy {
					t.Fatalf("claim.force_reason = %q, 기대 %q — 선점을 왜 끊었는지가 원장에 없다",
						claim.ForceReason, c.wantWhy)
				}
			}

			// 그리고 그 사실을 화면이 말한다.
			_, html := f.get("")
			mustContain(t, html, c.wantLine,
				"선점을 어떻게 처리했는지가 판단에 안 남았다 — force_reason 은 어느 화면도 안 읽는다")
		})
	}
}
```

**설계 메모(왜 이 모양인가):**
- 항목 id 는 `t5-e`·`t5-f` 다 — 이 파일이 이미 `t5-a`~`t5-d` 를 쓴다.
- 헬퍼는 이 패키지 것을 그대로 쓴다: `newFixture(t).withRepo("feat")` · `openSession` · `claimOne`(render_test.go:565, addItem+Pick 을 함께 한다) · `addItem` · `post` · `get` · `mustContain`.
- 저장층 단정 순서를 화면 단정 **앞**에 뒀다. 반대로 두면 화면 단정이 먼저 죽어 `force_reason` 실패 메시지가 안 보인다(실제로 그렇게 나왔다).
- 화면 단정이 성립하는 근거: 판단 본문은 섹션 ⑥ 이 `Clip(j.Body, 600)` 으로 찍는다(page.go:919). 본문 전체가 약 200 룬이라 안 잘린다.
- `f.st.GetClaim` 은 반납된 행도 낸다(이력이 자산이라). 그래서 `ReleasedAt != nil` 로 닫힘을 잰다.

- [ ] **Step 4: 빨간불을 확인한다 (실패 확인)**



Run: `go test ./internal/web/ -run TestDropClosesTheClaimWithTheDropReason -v -count=1`

Expected: 두 하위 시험 모두 FAIL. 실제로 나온 문구:
```
=== RUN   TestDropClosesTheClaimWithTheDropReason/선점된_항목
    actions_test.go:241: claim.force_reason = "", 기대 "설계에서 빠진 축이라 이 항목은 성립하지 않는다 — 버린다" — 선점을 왜 끊었는지가 원장에 없다
=== RUN   TestDropClosesTheClaimWithTheDropReason/선점_없는_항목
    actions_test.go:248: HTML 에 "선점: 폐기 시점에 살아 있는 선점이 없었다" 가 없다 — …
--- FAIL: TestDropClosesTheClaimWithTheDropReason (0.36s)
```
★ 첫 하위 시험이 `force_reason = ""` 로 죽는 것이 이 태스크의 핵심이다. `released_at` 이나 `state=dropped` 로 죽으면 단정을 잘못 건 것이다(둘 다 이미 참이다).

- [ ] **Step 5: gofmt 를 확인한다**

관문 시험(`internal/service/gofmt_gate_test.go`)은 service 패키지에 있어 111초가 걸린다. 여기서는 gofmt 를 직접 부른다.

Run: `gofmt -l internal/web/`

Expected: 출력이 비어 있다. `internal/web/actions_test.go` 가 찍히면 doc 주석의 들여쓴 이어짐 줄이 남아 있다는 뜻이다 — `gofmt -d internal/web/actions_test.go` 로 자리를 보고 평평한 `// ` 로 고쳐라(gofmt -w 로 덮으면 주석이 탭 코드블록으로 재작성돼 읽기가 나빠진다).

---

### Task 15: 최소 구현 — drop 의 tx 안에서 ForceReleaseClaim 을 SetItemState 앞에 부른다
**Files:**
- Modify: internal/web/actions.go (229-246 치환)

**Interfaces:**
- Consumes: `TestDropClosesTheClaimWithTheDropReason` 이 두 하위 시험 모두 빨간불인 상태. 특히 `선점된_항목` 이 `claim.force_reason = ""` 로 죽고 있어야 한다.
- Produces: `(h *handler) drop` 이 tx 안에서 `LogEvent → ForceReleaseClaim(사유=in.Reason) → SetItemState(dropped) → AddJudgment(claimLine 포함)` 순으로 돈다. 판단 본문에 두 문장 중 하나가 실린다: `선점: 살아 있던 선점을 이 폐기와 같은 트랜잭션에서 함께 닫았다. 사유는 claim.force_reason 에도 그대로 남았다.` 또는 `선점: 폐기 시점에 살아 있는 선점이 없었다.`

- [ ] **Step 1: actions.go 229-246 을 치환한다**

import 는 손대지 않는다 — `errors`(4줄)와 `store`(12줄)가 이미 있다.

**before(공백까지 정확, actions.go:229-246):**
```go
	body := fmt.Sprintf("대시보드에서 항목을 폐기했다.\n항목: %s (%s)\n사유: %s\n"+
		"행위자: 대시보드(사람). 세션이 아니라 사람이 누른 것이므로 session_id 는 비어 있다.",
		in.Item, Clip(it.Title, 200), in.Reason)

	err = st.Tx(ctx, func(t *store.Tx) error {
		t.LogEvent("web.item.drop", in.Project, "", map[string]any{
			"item": in.Item, "state": string(it.State),
		})
		if err := t.SetItemState(in.Project, in.Item, model.ItemDropped, in.Reason); err != nil {
			return err
		}
		_, err := t.AddJudgment(model.Judgment{
			Project: in.Project, Kind: model.JudgmentDecision,
			Title: "항목 폐기: " + in.Item, Body: body,
			Links: []model.JudgmentLink{{TargetKind: "item", TargetID: in.Item}},
		})
		return err
	})
```

**after:**
```go
	err = st.Tx(ctx, func(t *store.Tx) error {
		// ★ 시도를 **먼저** 예약한다 — 예약 이벤트는 롤백 뒤에도 흘러서(store/store.go:346-352)
		//   아래가 통째로 죽어도 "누가 무엇을 폐기하려 했나"가 원장에 남는다.
		//   그래서 이 payload 에는 아래에서야 알게 되는 것(선점을 실제로 닫았는지)을 안 싣는다.
		t.LogEvent("web.item.drop", in.Project, "", map[string]any{
			"item": in.Item, "state": string(it.State),
		})

		// ★ 선점을 **SetItemState 앞에서** 닫는다. 순서에 두 가지가 걸려 있다.
		//
		//   ⓐ ForceReleaseClaim 은 `UPDATE item SET state='open' … AND state='claimed'` 를
		//     함께 친다(store/item.go:783). 폐기를 먼저 찍고 뒤에 회수하면 방금 닫은 항목이
		//     다시 열린다.
		//   ⓑ 그런데 지금 그 되돌림은 실제로는 안 일어난다 — SetItemState 가 종료 상태에서
		//     살아 있는 선점을 스스로 반납하므로(store/item.go:512-527) 뒤에 놓인
		//     ForceReleaseClaim 은 `released_at IS NULL` 가드에 0행으로 걸려 ⓐ 의 UPDATE 에
		//     닿기 전에 NFLiveClaim 으로 빠진다. 즉 **틀린 순서가 오류도 증상도 안 낸다.**
		//     그 갈래에서 유일하게 사라지는 것이 force_reason 이고, 그래서 시험의 관측점이
		//     released_at 이 아니라 force_reason 이다(actions_test.go 의 같은 이름 시험).
		//
		// ★ 사유는 폐기 사유를 **그대로** 나른다. ForceReleaseClaim 이 빈 사유를 거절하기
		//   때문만이 아니다(store/item.go:764) — 회수 경로가 이미 사람이 친 문장을 가공 없이
		//   넘긴다(service/reclaim.go:129). 같은 컬럼에 두 경로가 다른 모양을 쓰면 나중에
		//   force_reason 을 읽는 쪽이 접두를 벗겨 가며 읽어야 한다.
		claimLine := "선점: 폐기 시점에 살아 있는 선점이 없었다."
		switch err := t.ForceReleaseClaim(in.Project, in.Item, in.Reason); {
		case err == nil:
			claimLine = "선점: 살아 있던 선점을 이 폐기와 같은 트랜잭션에서 함께 닫았다. " +
				"사유는 claim.force_reason 에도 그대로 남았다."
		case errors.Is(err, store.ErrNotFound):
			// ★ NFLiveClaim 이다. "선점이 원래 없었다"는 **정상 갈래**이지 폐기의 실패가 아니다 —
			//   열린 항목의 폐기가 이 경로에서 제일 흔하다. 여기서 올리면 큐를 정리하는 유일한
			//   화면 경로가 정확히 정상 입력에 대해 500 을 낸다.
			//   같은 모양의 흡수가 service/finish.go:373 에 있고, 거기와 달리 여기는 갈래가
			//   이 하나뿐이라 errors.As 짝이 없다.
		default:
			return err
		}

		if err := t.SetItemState(in.Project, in.Item, model.ItemDropped, in.Reason); err != nil {
			return err
		}

		// ★ 선점을 어떻게 처리했는지는 **판단 본문**에 남긴다. 새 이벤트 종류를 만들지 않고
		//   위 payload 도 안 늘린다(그 자리는 롤백돼도 남는 시도 기록이라 결과를 못 싣는다).
		//   claim.force_reason 은 지금 어느 화면도 안 읽으므로(page.go·dashboard.gohtml 전수
		//   확인) 원장에만 두면 선점을 잃은 세션 쪽에서는 그 사실이 영영 안 보인다.
		//   판단은 이 폐기의 서사를 담는 자리이고 커밋된 사실만 담기므로 여기가 맞다.
		_, err := t.AddJudgment(model.Judgment{
			Project: in.Project, Kind: model.JudgmentDecision,
			Title: "항목 폐기: " + in.Item,
			Body: fmt.Sprintf("대시보드에서 항목을 폐기했다.\n항목: %s (%s)\n사유: %s\n%s\n"+
				"행위자: 대시보드(사람). 세션이 아니라 사람이 누른 것이므로 session_id 는 비어 있다.",
				in.Item, Clip(it.Title, 200), in.Reason, claimLine),
			Links: []model.JudgmentLink{{TargetKind: "item", TargetID: in.Item}},
		})
		return err
	})
```

**정한 것 넷(과제가 물은 그대로):**
1. **순서** — `ForceReleaseClaim` 을 `SetItemState` **앞**에. 위 ⓐⓑ 가 근거다.
2. **흡수** — `errors.Is(err, store.ErrNotFound)` 로 `NFLiveClaim` 만 삼킨다. `default: return err` 가 나머지를 전부 올린다. `switch 초기화문; {` 형태는 이 저장소의 선례가 있다(store/item.go:599 의 `switch it, err := t.GetItem(...); {`).
3. **사유 문자열** — `in.Reason` **가공 없이 그대로**. 접두를 붙이지 않는다: ⓐ 회수 경로(service/reclaim.go:129)가 이미 사람이 친 문장을 그대로 넘겨서 같은 컬럼에 두 모양이 생기면 읽는 쪽이 접두를 벗겨야 하고, ⓑ 폐기라는 사실은 같은 tx 의 `web.item.drop` 이벤트와 `항목 폐기: <id>` 판단이 이미 말한다. `JudgeAction` 의 `reasonMin=4` 가 빈 사유를 이미 막으므로 `ForceReleaseClaim` 의 빈 사유 거절에 걸릴 일이 없다.
4. **원장 자리** — 새 이벤트 종류를 안 만들고 `web.item.drop` payload 도 안 늘린다. 그 `LogEvent` 는 tx 첫 문장에 **예약**돼 롤백돼도 흘러가는 "시도" 기록이라, 아직 안 일어난 결과(선점을 닫았는지)를 못 싣는다 — 실으려면 호출을 뒤로 옮겨야 하고 그러면 롤백 갈래의 기록이 사라진다. 결과는 **판단 본문**과 **claim.force_reason** 둘에 남는다: 전자는 사람이 화면에서 읽는 자리, 후자는 그 행 자체가 근거인 자리다.

Expected: `body` 지역 변수가 사라지고 본문 조립이 `AddJudgment` 인자 안으로 들어간다. `fmt` 는 여전히 쓰이므로 import 변화 없다.

- [ ] **Step 2: 초록불을 확인한다**



Run: `go test ./internal/web/ -run TestDropClosesTheClaimWithTheDropReason -v -count=1`

Expected: ```
--- PASS: TestDropClosesTheClaimWithTheDropReason (0.41s)
    --- PASS: TestDropClosesTheClaimWithTheDropReason/선점된_항목 (0.23s)
    --- PASS: TestDropClosesTheClaimWithTheDropReason/선점_없는_항목 (0.18s)
```

- [ ] **Step 3: 순서 변이가 죽는지 확인한다 (시험이 실제로 무엇을 잡는지 잰다)**

`ForceReleaseClaim` switch 블록을 `SetItemState` **뒤로** 손으로 옮겨 시험을 한 번 돌리고, 확인한 뒤 **되돌린다.** 이 확인을 생략하면 "순서가 중요하다"는 주석이 시험으로 안 받쳐진다.

내가 실제로 재 본 결과:
```
--- FAIL: TestDropClosesTheClaimWithTheDropReason/선점된_항목
    actions_test.go:241: claim.force_reason = "", 기대 "설계에서 빠진 축이라 이 항목은 성립하지 않는다 — 버린다" — 선점을 왜 끊었는지가 원장에 없다
```
★ 주목: 항목 상태는 여전히 `dropped` 다 — 틀린 순서가 **오류도 증상도 안 낸다.** force_reason 만 조용히 빈다. 그것이 이 시험의 존재 이유다.

Expected: 변이를 넣으면 `선점된_항목` 이 force_reason 으로 FAIL. 되돌리면 다시 PASS.

- [ ] **Step 4: 관문 — gofmt · vet(교차 빌드 포함) · 패키지 전체**

`go build` 는 _test.go 를 건너뛰므로 교차 빌드 관문은 `go vet` 으로 돈다.

Run: `gofmt -l internal/web/ && go vet ./internal/web/ && GOOS=windows go vet ./internal/web/ && go test ./internal/web/ -count=1`

Expected: gofmt 출력 없음 · vet 둘 다 무출력 · `ok  github.com/kweiza/flightdeck/internal/web  약 11s`. (내가 실제로 이 넷을 다 통과시켰다.)

- [ ] **Step 5: 커밋한다**

두 파일만 담는다. 같은 워크트리에서 이 항목의 다른 층이 동시에 작업 중이므로 `git commit -a` 를 쓰지 마라.

```
git add plugins/flightdeck/server/internal/web/actions.go \
        plugins/flightdeck/server/internal/web/actions_test.go
```

커밋 메시지:
```
fix(flightdeck): 폐기가 선점을 끊고도 왜 끊었는지는 안 남기고 있었다

스펙 §4-⑥ 의 전제 절반이 실측에서 거짓이었다. "claim 행이 released_at=NULL 로
남는다"는 이미 참이 아니다 — SetItemState 가 종료 상태에서 살아 있는 선점을
스스로 반납한다(store/item.go:512-527). 실제로 비어 있던 것은 force_reason 이고,
그래서 원장에는 "선점이 언젠가 끊겼다"만 있고 "사람이 폐기하면서 끊었다"가 없었다.

drop 의 tx 안에서 ForceReleaseClaim 을 SetItemState **앞에** 부른다. 앞이어야 하는
이유가 둘인데 그중 하나는 지금 잠복 상태다 — 뒤에 두면 released_at IS NULL 가드에
0행으로 걸려 NFLiveClaim 으로 빠지므로 오류도 증상도 안 나고 force_reason 만
조용히 빈다. 그 침묵을 잡는 관측점이 시험의 force_reason 단정이다.

살아 있는 선점이 없으면 NFLiveClaim 이 나는데 그것은 정상 갈래다(열린 항목의
폐기가 이 경로에서 제일 흔하다). errors.Is 로 갈라 흡수한다 —
service/finish.go:373 의 같은 모양 흡수가 선례고, 거기와 달리 여기는 갈래가
하나뿐이라 errors.As 짝이 없다.

사유는 폐기 사유를 그대로 넘긴다. 회수 경로(service/reclaim.go:129)가 이미
사람이 친 문장을 가공 없이 넘겨서, 같은 컬럼에 두 모양이 생기면 읽는 쪽이
접두를 벗겨 가며 읽어야 한다.

새 이벤트 종류는 안 만들었고 web.item.drop payload 도 안 늘렸다 — 그 LogEvent 는
tx 첫 문장에 예약돼 롤백돼도 흘러가는 "시도" 기록이라 아직 안 일어난 결과를
못 싣는다. 선점을 어떻게 처리했는지는 판단 본문에 한 줄로 남는다. force_reason 은
지금 어느 화면도 안 읽으므로, 원장에만 두면 선점을 잃은 쪽에서는 안 보인다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
```

Expected: 두 파일만 담긴 커밋 하나.

---

### Task 16: 회수 근거 축이 여섯이 된다 — DESIGN §4 · pick 거절 문구 · page.go 주석을 한 수로 맞춘다
**Files:**
- Test: plugins/flightdeck/server/internal/mcpsrv/reclaim_axes_test.go (신규)
- Modify: plugins/flightdeck/DESIGN.md:369-371
- Modify: plugins/flightdeck/server/internal/mcpsrv/mcpsrv.go:735-744 (pick 거절)
- Modify: plugins/flightdeck/server/internal/mcpsrv/mcpsrv.go:884-885 (land 거절 주석)
- Modify: plugins/flightdeck/server/internal/mcpsrv/server_test.go:413 (★ 이 변경이 깨뜨리는 기존 시험)
- Modify: plugins/flightdeck/server/internal/web/page.go:88

**Interfaces:**
- Consumes: 없음. 다른 층의 산출물에 의존하지 않는다 — 이 태스크는 순수 산문·문자열이다.
- Produces: DESIGN.md §4 가 `근거를 여섯 축으로` 라고 말하고 여섯째를 `종료 선언` 으로 이름 부른다. pick 의 steal_reason 거절 문구가 `여섯 축을 나란히 본 뒤에야 한다` + `그 항목의 종료 선언` 을 담는다. land 의 release 거절은 **다섯 축 그대로**이고 그 갈림의 이유가 주석에 있다. `internal/mcpsrv` 에 시험 헬퍼 `reclaimDesignDoc(t *testing.T) string` 이 생긴다.

- [ ] **Step 1: 실패 시험을 쓴다 — 문서와 응답이 같은 수를 말하는지, 그리고 그 수가 여섯인지**

`plugins/flightdeck/server/internal/mcpsrv/reclaim_axes_test.go` 를 새로 만든다.

```go
package mcpsrv

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// 회수 근거의 축 수는 문서와 응답이 **같은 수**를 말해야 한다
// ─────────────────────────────────────────────────────────────────────────────
//
// 설계 §4 가 회수 근거의 축 수를 한 문장으로 못박고, pick 의 steal_reason 거절이
// 그 문장을 사람이 읽는 자리에 다시 낸다. 두 벌이라 한쪽만 고치면 다른 쪽이 조용히
// 거짓이 된다 — 그리고 그럴 뻔했다. 이 저장소에서 축 수를 단정하던 유일한 시험
// (TestPickRefusesSteal)은 "다섯 축"이라는 **문자열이 있는지**만 봤으므로, 문서가
// 여섯이 돼도 초록이었다. 존재 단정은 표류를 못 잡는다.
//
// 그래서 이 시험이 잠그는 것은 값이 아니라 **일치**다: DESIGN 이 말하는 수 낱말이 곧
// 응답이 말하는 수 낱말이어야 한다. 지금 사실(여섯 · 여섯째는 종료 선언)도 함께
// 잠근다 — 축을 늘리는 사람의 빨간불이 여기서 먼저 켜져야 그 사람이 §4 를 읽는다.
//
// ★ land 의 회수 거절은 **일부러 다섯이다.** 회수 대상이 줄 행이라 항목에 붙는
// 여섯째가 존재하지 않는다. 그 갈림도 함께 단정한다 — 안 그러면 다음 사람이
// "둘이 어긋났다"고 보고 맞추고, 그 순간 응답이 없는 축을 보라고 말하게 된다.

// reclaimDesignDoc 은 설계 정본을 읽는다.
// internal/mcpsrv → internal → server → plugins/flightdeck 세 단계 위다.
func reclaimDesignDoc(t *testing.T) string {
	t.Helper()
	p := filepath.Join("..", "..", "..", "DESIGN.md")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("DESIGN.md 를 못 읽었다(%s) — 이 시험의 좌표가 틀렸다: %v", p, err)
	}
	return string(b)
}

var reclaimAxisCountRe = regexp.MustCompile(`회수는 사람만, 사유 필수, 그리고 근거를 (\S+?) 축으로`)

func TestReclaimAxisCountAgreesBetweenDesignAndPickRefusal(t *testing.T) {
	design := reclaimDesignDoc(t)
	loc := reclaimAxisCountRe.FindStringSubmatchIndex(design)
	if loc == nil {
		t.Fatalf("DESIGN.md §4 의 회수 근거 머리줄을 못 찾았다 — 그 문장이 이 시험의 정본이다")
	}
	count := design[loc[2]:loc[3]]

	// 문단 안에서만 본다. 문서 전체를 보면 다른 절의 낱말이 이 단정을 통과시킨다.
	seg := design[loc[0]:]
	if len(seg) > 1600 {
		seg = seg[:1600]
	}

	if count != "여섯" {
		t.Errorf("DESIGN.md 의 회수 근거 축이 %q 다 — 항목 쪽 종료 선언이 여섯째로 붙었다", count)
	}
	if !strings.Contains(seg, "종료 선언") {
		t.Errorf("DESIGN.md §4 가 여섯째 축을 이름으로 안 부른다:\n%s", seg)
	}

	repo := newRepo(t)
	svc, st := newSvc(t)
	srv := newServer(t, svc, repo, fullEnv(repo))

	frames := serve(t, srv,
		call("add", map[string]any{"id": "t-axis", "title": "제목", "body": "본문"}),
		call("pick", map[string]any{"item_id": "t-axis", "steal_reason": "저쪽이 죽은 것 같다"}),
	)
	text, isErr := toolText(t, frames[1])
	if !isErr {
		t.Fatalf("steal_reason 이 조용히 무시됐다:\n%s", text)
	}
	if want := count + " 축을 나란히 본 뒤에야 한다"; !strings.Contains(text, want) {
		t.Errorf("pick 거절이 DESIGN 과 다른 수를 말한다(DESIGN 은 %q):\n%s", want, text)
	}
	if !strings.Contains(text, "종료 선언") {
		t.Errorf("pick 거절이 여섯째 축을 이름으로 안 부른다:\n%s", text)
	}
	// 거절은 아무것도 안 쓴다. 이 단정이 없으면 문구만 맞고 행이 생겨도 초록이다.
	if n := countRows(t, st, `SELECT count(*) FROM claim`); n != 0 {
		t.Fatalf("거절했는데 선점 %d행이 생겼다", n)
	}
}

// TestLaneReclaimStaysAtFiveAxes 는 land 의 회수 거절이 **일부러** 다섯인 것을 잠근다.
// 회수 대상이 줄 행이라 항목에 붙는 여섯째가 없다. 두 문구를 같은 수로 맞추면
// 이 응답이 존재하지 않는 축을 보라고 말하게 된다.
func TestLaneReclaimStaysAtFiveAxes(t *testing.T) {
	repo := newRepo(t)
	svc, _ := newSvc(t)
	srv := newServer(t, svc, repo, fullEnv(repo))

	frames := serve(t, srv, call("land", map[string]any{"release": "그만 좀 물려줘"}))
	text, isErr := toolText(t, frames[0])
	if !isErr {
		t.Fatalf("release 인자가 거절되지 않았다:\n%s", text)
	}
	if !strings.Contains(text, "다섯 축을 나란히 본 뒤에야 한다") {
		t.Errorf("레인 회수 거절의 축 수가 다섯이 아니다:\n%s", text)
	}
	if strings.Contains(text, "종료 선언") {
		t.Errorf("레인 회수 거절이 존재하지 않는 축을 보라고 말한다:\n%s", text)
	}
}
```

Run: `go test ./internal/mcpsrv/ -run 'TestReclaimAxisCountAgreesBetweenDesignAndPickRefusal|TestLaneReclaimStaysAtFiveAxes' -v -count=1`

Expected: TestReclaimAxisCount... 이 FAIL 한다. 실패 줄 둘: `DESIGN.md 의 회수 근거 축이 "다섯" 이다` · `pick 거절이 여섯째 축을 이름으로 안 부른다`. TestLaneReclaimStaysAtFiveAxes 는 PASS(지금 다섯이므로) — 이쪽은 회귀 관문이다.

- [ ] **Step 2: DESIGN.md §4 를 고친다**

`/home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-finish-refusal-strands-completed-item/plugins/flightdeck/DESIGN.md` 369-371.

BEFORE (그대로, 선행 공백 없음):
```
**회수는 사람만, 사유 필수, 그리고 근거를 다섯 축으로 나란히 보여준 뒤에.**
마지막 신호 종류·나이 · 발자국 경로 수 · 원격 마지막 커밋 시각 · 미푸시 커밋 수 · 마지막 판단.
**하나의 신호로 판정하지 않는다** — 두 번의 오판이 둘 다 근거가 하나뿐이라 났다.
```

AFTER:
```
**회수는 사람만, 사유 필수, 그리고 근거를 여섯 축으로 나란히 보여준 뒤에.**
세션 쪽 다섯 — 마지막 신호 종류·나이 · 발자국 경로 수 · 원격 마지막 커밋 시각 · 미푸시 커밋 수 · 마지막 판단.
항목 쪽 하나 — **종료 선언**. 이 항목을 닫으려다 롤백된 `finish` 다(`event(kind='item.finish')` 는
있는데 항목이 아직 `done`/`dropped` 이 아니다).
**하나의 신호로 판정하지 않는다** — 두 번의 오판이 둘 다 근거가 하나뿐이라 났다.

**여섯째는 앞의 다섯과 묻는 것이 다르다 — 그것이 이 축을 더한 이유다.** 앞의 다섯은 "저 세션이
살아 있나"에 답하고, 여섯째는 "이 항목을 애초에 남에게 넘길 것인가"에 답한다. 실측 하나가 그
빈틈이었다(2026-08-04~05, 항목 `fd-finish-refusal-strands-completed-item`): 세션이 완성된
핸드오프 10,300바이트와 함께 `finish` 를 쳤는데 선점 표류로 tx 가 통째로 롤백됐고, 사람이
다섯 축만 보고 선점을 회수했고, 그 다음 `pick` 이 그 항목을 후보 26건 중 **1순위**로 냈다.
**다섯 축은 전부 옳았다** — 그 세션은 정말 떠났다. 틀린 것은 항목의 처분이었다.
같은 모양이 넷이다(각각 10300·5421·5060·3032바이트의 판단 본문이 롤백과 함께 죽었다).

**여섯째는 선점 회수에만 붙는다.** 레인 회수(`fd lane release`)의 대상은 줄 행이지 항목이
아니라 그 축이 존재하지 않는다 — 그래서 `land` 의 회수 거절 문구는 다섯 축 그대로다.
두 거절이 같은 문장 틀을 쓰면서 목록만 갈리는 것은 표류가 아니라 **대상이 다르다는 사실**이고,
`mcpsrv` 의 두 시험이 그 갈림을 양쪽에서 잠근다.
```

Expected: DESIGN.md 만 바뀐다. 아직 시험은 빨갛다(pick 문구가 아직 다섯).

- [ ] **Step 3: pick 의 steal_reason 거절과 그 위 주석을 고친다**

`server/internal/mcpsrv/mcpsrv.go` 735-744. 들여쓰기는 탭이다.

BEFORE:
```go
	// ★ 회수는 이 서버가 하지 않는다. 설계 §4: "회수는 사람만, 사유 필수,
	//   근거를 다섯 축으로 나란히 보여준 뒤에." 인자를 조용히 무시하면
	//   에이전트가 회수됐다고 믿고 남의 작업 위에서 일한다.
	if strings.TrimSpace(a.StealReason) != "" {
		return textResult(s.withTail(ctx, RenderRefusal("pick",
			"steal_reason 이 왔지만 이 서버는 선점을 회수하지 않는다",
			"회수는 사람만 한다 — 마지막 신호 종류·나이, 발자국 경로 수, 원격 마지막 커밋 시각, "+
				"미푸시 커밋 수, 마지막 판단 다섯 축을 나란히 본 뒤에야 한다(설계 §4). "+
				"하나의 신호로 판정해 두 번 틀렸다. 지금 할 수 있는 것: note(kind=ask) 로 점유자에게 묻거나, "+
				"웹 대시보드의 '선점 회수' 버튼(사유 필수)을 쓴다."), tailOpts{}), true)
	}
```

AFTER:
```go
	// ★ 회수는 이 서버가 하지 않는다. 설계 §4: "회수는 사람만, 사유 필수,
	//   근거를 여섯 축으로 나란히 보여준 뒤에." 인자를 조용히 무시하면
	//   에이전트가 회수됐다고 믿고 남의 작업 위에서 일한다.
	//
	//   ★ 여섯째(종료 선언)는 세션이 아니라 **항목**을 묻는다 — 이 항목을 닫으려다
	//   롤백된 finish 가 원장에 있나. 다섯 축이 전부 "저 세션은 떠났다"로 옳았는데도
	//   회수가 사고가 된 실측이 그 축을 낳았다(2026-08-04~05, 같은 모양 넷).
	if strings.TrimSpace(a.StealReason) != "" {
		return textResult(s.withTail(ctx, RenderRefusal("pick",
			"steal_reason 이 왔지만 이 서버는 선점을 회수하지 않는다",
			"회수는 사람만 한다 — 마지막 신호 종류·나이, 발자국 경로 수, 원격 마지막 커밋 시각, "+
				"미푸시 커밋 수, 마지막 판단, 그리고 그 항목의 종료 선언 여섯 축을 나란히 본 뒤에야 한다(설계 §4). "+
				"하나의 신호로 판정해 두 번 틀렸다. 지금 할 수 있는 것: note(kind=ask) 로 점유자에게 묻거나, "+
				"웹 대시보드의 '선점 회수' 버튼(사유 필수)을 쓴다."), tailOpts{}), true)
	}
```

그리고 `mcpsrv.go` 884-885 의 land 쪽 주석에 갈림의 이유를 적는다. **문자열 본문은 안 고친다.**

BEFORE:
```go
	// ★ 회수는 이 서버가 하지 않는다 — pick 의 steal_reason 거절과 **같은 판정, 같은 문장 틀**이다.
	//   한 서버가 선점 회수는 거절하고 레인 회수는 허용하면 그 거절 문구가 화면에서 거짓이 된다.
```

AFTER:
```go
	// ★ 회수는 이 서버가 하지 않는다 — pick 의 steal_reason 거절과 **같은 판정, 같은 문장 틀**이다.
	//   한 서버가 선점 회수는 거절하고 레인 회수는 허용하면 그 거절 문구가 화면에서 거짓이 된다.
	//
	//   ★ **축 목록만 갈린다.** pick 은 여섯이고 여기는 다섯이다 — 여섯째(종료 선언)는
	//   항목에 붙는 사실인데 이쪽의 회수 대상은 줄 행이라 그 축이 존재하지 않는다(설계 §4).
	//   수를 맞추면 이 응답이 없는 축을 보라고 말하게 된다. 그 갈림은 표류가 아니라
	//   대상이 다르다는 사실이고, reclaim_axes_test.go 가 양쪽을 각각 잠근다.
```

Expected: 컴파일은 되지만 아직 기존 시험 TestPickRefusesSteal 이 빨개진다(그 시험이 "다섯 축"을 단정한다).

- [ ] **Step 4: ★ 이 변경이 깨뜨리는 기존 시험을 고친다**

`server/internal/mcpsrv/server_test.go:413`. 이것이 이 변경으로 **반드시 빨개지는 유일한 기존 시험**이다(전수 grep: `다섯 축` 을 제품 문구에 대해 단정하는 자리는 여기 하나뿐).

BEFORE:
```go
	if !strings.Contains(text, "회수하지 않는다") || !strings.Contains(text, "다섯 축") {
```

AFTER:
```go
	if !strings.Contains(text, "회수하지 않는다") || !strings.Contains(text, "여섯 축") {
```

같은 시험의 doc 주석도 한 줄 늘린다.

BEFORE:
```go
// TestPickRefusesSteal 은 설계 §4("회수는 사람만")를 응답 문자열로 단정한다.
```

AFTER:
```go
// TestPickRefusesSteal 은 설계 §4("회수는 사람만")를 응답 문자열로 단정한다.
//
// ★ 수 낱말의 **일치**는 여기가 아니라 reclaim_axes_test.go 가 본다. 이 시험은
// 존재만 보므로 문서가 여섯이 되고 문구가 다섯인 상태를 못 잡았다 — 실제로 그랬다.
```

Run: `go test ./internal/mcpsrv/ -run 'TestPickRefusesSteal|TestReclaimAxisCountAgreesBetweenDesignAndPickRefusal|TestLaneReclaimStaysAtFiveAxes|TestLandReleaseArgIsRefused' -v -count=1`

Expected: 넷 다 PASS.

- [ ] **Step 5: web/page.go:88 의 인용을 고친다**

`server/internal/web/page.go:88`.

BEFORE:
```go
// ClaimTarget 은 회수 가능한 선점 하나다. **근거를 함께 낸다**(설계 §4 의 다섯 축 중 표시분).
```

AFTER:
```go
// ClaimTarget 은 회수 가능한 선점 하나다. **근거를 함께 낸다**(설계 §4 의 여섯 축 중 표시분).
//
// ★ 여섯째(종료 선언)는 세션이 아니라 **항목**에 붙는다. 그래서 이 구조체가 그 값을
// 스스로 계산하지 않고 같은 id 의 ItemRow 에서 받는다 — claimTargets(page.go:668)가
// 이미 q.Items 를 원천으로 삼으므로 배선이 더 필요 없고, 두 자리에서 따로 계산하면
// 같은 이름으로 다른 사실을 내는 어긋남이 재현된다(설계 §4-⑤).
```

Run: `gofmt -l ./internal && go vet ./... && go test ./internal/mcpsrv/ ./internal/web/ -count=1`

Expected: gofmt 출력 없음, vet 조용, 두 패키지 ok.

- [ ] **Step 6: 커밋**

```
docs(flightdeck): 회수 근거에 여섯째가 붙는다 — 다섯 축은 전부 옳았는데도 회수가 사고가 됐다

앞의 다섯은 "저 세션이 살아 있나"에 답하고 여섯째(종료 선언)는 "이 항목을 애초에
남에게 넘길 것인가"에 답한다. 2026-08-04~05 실측에서 다섯 축은 전부 옳았고
(세션은 정말 떠났다) 틀린 것은 항목의 처분이었다 — 완성된 핸드오프 10,300바이트가
롤백과 함께 죽은 항목이 그 다음 pick 에서 26건 중 1순위로 나왔다. 같은 모양이 넷이다.

레인 회수는 다섯 그대로 둔다. 대상이 줄 행이라 항목 축이 없고, 수를 맞추면
응답이 존재하지 않는 축을 보라고 말하게 된다. 그 갈림을 주석과 시험에 적었다.

축 수를 단정하던 유일한 시험은 문자열 존재만 봐서, 문서가 여섯이 되고 문구가
다섯인 상태를 못 잡았다. 새 시험은 값이 아니라 **일치**를 본다 — DESIGN 이
말하는 수 낱말이 곧 응답이 말하는 수 낱말이어야 한다.
```

Run: `git add -A && git commit -F -`

Expected: 커밋 1개.

---

### Task 17: "조정할 상수가 0개다"는 두 번 낡았다 — 그 주장을 기계가 지킨다
**Files:**
- Test: plugins/flightdeck/server/internal/judge/sort_axis_doc_test.go (신규)
- Modify: plugins/flightdeck/DESIGN.md:288-293
- Modify: plugins/flightdeck/server/internal/judge/bundle.go:378 (lessBundle doc 첫 줄)

**Interfaces:**
- Consumes: 없음. Task 1 과 파일이 안 겹치므로 순서 무관하게 돌릴 수 있다.
- Produces: judge 시험 패키지에 헬퍼 셋이 생긴다 — `designDoc(t *testing.T) string` · `judgeSource(t *testing.T, name string) string` · `sortKeyParagraph(t *testing.T, design string) string`. Task 3 이 `judgeSource` 를 쓴다. 그리고 DESIGN §3 의 정렬 키 문단이 `기아` 와 `종료 선언` 을 이름으로 부른다.

- [ ] **Step 1: 실패 시험을 쓴다 — 코드에 축의 근거가 있으면 문서가 그 축을 이름으로 불러야 한다**

`plugins/flightdeck/server/internal/judge/sort_axis_doc_test.go` 를 새로 만든다.

```go
package judge

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// 정렬 축을 더하면 문서가 먼저 빨개진다
// ─────────────────────────────────────────────────────────────────────────────
//
// DESIGN §3 의 정렬 키 문단은 **두 번 낡았다.** 기아(StarvationAge)가 상수를 0에서
// 1로 만들었고, 종료 선언이 키를 하나 더 더했다. 두 번 다 시험이 안 빨개졌다 —
// 그 줄에 수를 세는 관문이 하나도 안 물려 있었다. 스킬 수(cmd/fd/plugin_test.go)는
// 같은 부류의 실패를 겪고 이미 정규식 관문을 얻었는데, 이쪽은 못 얻었다.
//
// 관문의 방향은 **코드 → 문서**다. 코드에 축의 근거(상수 선언·구조체 필드)가 있으면
// 문서의 그 문단이 그 축을 이름으로 불러야 한다. 반대 방향(문서에 있으면 코드에도
// 있어야 한다)은 **일부러 안 건다** — DESIGN 은 "여기 있는 것은 이대로 만든다"라서
// 구현보다 앞설 수 있는 문서이고(§0 머리말), 그 방향까지 걸면 설계 커밋이 못 선다.

// designDoc 은 설계 정본이다.
// internal/judge → internal → server → plugins/flightdeck 세 단계 위다.
func designDoc(t *testing.T) string {
	t.Helper()
	p := filepath.Join("..", "..", "..", "DESIGN.md")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("DESIGN.md 를 못 읽었다(%s) — 이 시험의 좌표가 틀렸다: %v", p, err)
	}
	return string(b)
}

// judgeSource 는 이 패키지의 소스 한 벌을 텍스트로 읽는다. 시험은 패키지
// 디렉토리에서 도므로 파일 이름만 준다.
func judgeSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("%s 를 못 읽었다: %v", name, err)
	}
	return string(b)
}

// sortKeyParagraph 는 DESIGN §3 의 "정렬 키는 …" 문단을 낸다(빈 줄 앞까지).
// 문서 전체를 보면 다른 절의 낱말이 이 단정을 통과시킨다.
func sortKeyParagraph(t *testing.T, design string) string {
	t.Helper()
	i := strings.Index(design, "정렬 키는")
	if i < 0 {
		t.Fatalf("DESIGN.md 에서 정렬 키 문단을 못 찾았다 — 이 시험의 정본이 사라졌다")
	}
	rest := design[i:]
	if j := strings.Index(rest, "\n\n"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// zeroTuningClaimRe 는 조정 상수가 **하나도** 없다고 무조건 주장하는 문장이다.
// 한정한 형태("이 함수 안에는" · "하나뿐이다")는 안 잡는다 — 그것이 지금의 사실이다.
var zeroTuningClaimRe = regexp.MustCompile(`조정할 상수가 0개|조정할 상수가 하나도 없다`)

func TestNoOneClaimsTheSortHasZeroTuningConstants(t *testing.T) {
	bundleSrc := judgeSource(t, "bundle.go")
	// 전제를 먼저 밟는다. 상수가 사라지면 이 관문은 판정할 것이 없다.
	if !strings.Contains(bundleSrc, "const StarvationAge") {
		t.Skip("StarvationAge 가 사라졌다 — 이 관문의 전제가 없어졌으므로 판정하지 않는다")
	}

	targets := []struct{ name, body string }{
		{"DESIGN.md", designDoc(t)},
		{"judge/bundle.go", bundleSrc},
		{"judge/eligible.go", judgeSource(t, "eligible.go")},
	}
	for _, tgt := range targets {
		for i, ln := range strings.Split(tgt.body, "\n") {
			if !zeroTuningClaimRe.MatchString(ln) {
				continue
			}
			t.Errorf("%s:%d 가 조정 상수가 하나도 없다고 주장한다 — StarvationAge(24h)는 "+
				"실측으로 선 조정 상수다:\n  %s", tgt.name, i+1, strings.TrimSpace(ln))
		}
	}
}

func TestDesignSortKeyParagraphNamesEveryLiveAxis(t *testing.T) {
	para := sortKeyParagraph(t, designDoc(t))
	bundleSrc := judgeSource(t, "bundle.go")

	axes := []struct{ evidence, name string }{
		{"const StarvationAge", "기아"},
		{"CloseDeclared ", "종료 선언"},
	}
	for _, a := range axes {
		if !strings.Contains(bundleSrc, a.evidence) {
			continue // 그 축이 아직 코드에 없다 — 문서를 강요하지 않는다
		}
		if strings.Contains(para, a.name) {
			continue
		}
		t.Errorf("bundle.go 에 %q 가 있는데 DESIGN 의 정렬 키 문단이 %q 를 안 부른다:\n%s",
			a.evidence, a.name, para)
	}
}
```

Run: `go test ./internal/judge/ -run 'TestNoOneClaimsTheSortHasZeroTuningConstants|TestDesignSortKeyParagraphNamesEveryLiveAxis' -v -count=1`

Expected: 둘 다 FAIL. 앞엣것은 `DESIGN.md:288` 과 `judge/bundle.go:378` 두 줄을 지목한다. 뒤엣것은 `bundle.go 에 "const StarvationAge" 가 있는데 DESIGN 의 정렬 키 문단이 "기아" 를 안 부른다` 로 실패한다(`CloseDeclared` 는 아직 코드에 없어 건너뛴다).

- [ ] **Step 2: DESIGN.md §3 의 정렬 키 문단을 고친다**

`plugins/flightdeck/DESIGN.md` 288-293. **선행 공백 2칸이다**(`pick_eval` 항목 안이라). 그대로 지켜라.

BEFORE:
```
  정렬 키는 넷이고 **조정할 상수가 0개다**: ① 의존자 수 합 ↓ · ② 묶음 크기 ↓ · ③ 최고령 구성원 ↑ ·
  ④ 선두 id 사전순.
  **★ ②가 없으면 이 기능이 발화하지 않는다** — 열린 16건 전부 의존자 0이라 ①이 상수이고, 그러면 ③이
  실질 1차 키가 되는데 그 최고령 항목은 교정된 규칙에서 **단독**이었다. ④는 동점 처리가 아니라 실제로
  브랜치 이름을 정한다(생성 시각이 마이크로초까지 같은 형제 셋은 ①②③이 전부 동점이다). 그래서 네 키의
  **실제 값**을 응답의 묶음 사유에 그대로 싣는다 — 답 못 하는 자동 선택은 두 번째 세션부터 무시된다.
```

AFTER:
```
  정렬 키는 여섯이고 **조정할 상수는 하나뿐이다**(`judge.StarvationAge`): ① 의존자 수 합 ↓ ·
  ★기아 · ★종료 선언(강등) · ② 묶음 크기 ↓ · ③ 최고령 구성원 ↑ · ④ 선두 id 사전순.
  **★ ②가 없으면 이 기능이 발화하지 않는다** — 열린 16건 전부 의존자 0이라 ①이 상수이고, 그러면 ③이
  실질 1차 키가 되는데 그 최고령 항목은 교정된 규칙에서 **단독**이었다. ④는 동점 처리가 아니라 실제로
  브랜치 이름을 정한다(생성 시각이 마이크로초까지 같은 형제 셋은 ①②③이 전부 동점이다). 그래서 여섯 키의
  **실제 값**을 응답의 묶음 사유에 그대로 싣는다 — 답 못 하는 자동 선택은 두 번째 세션부터 무시된다.

  **★ 이 줄은 두 번 낡았고 두 번 다 시험이 안 빨개졌다.** 앞선 판은 키를 "넷", 상수를 "0"으로
  적었다. 첫 번째로 기아 축이 그 상수를 1로 만들었고(`StarvationAge`, 리드타임 실측이 정한다),
  두 번째가 종료 선언이다. 이 줄에 수를 세는 관문이 하나도 안 물려 있어서 정정이 전적으로
  사람 손에 달려 있었다 — 지금은 `judge/sort_axis_doc_test.go` 가 **코드에 축의 근거가 있으면
  이 문단이 그 축을 이름으로 부른다**를 지킨다. 키를 더할 때 이 줄을 함께 고쳐라.

  **두 ★ 축은 자리가 곧 뜻이다.** 기아는 ① 아래에, 종료 선언은 **기아 아래**에 선다. 그래야
  강등된 항목도 굶는 순간 안 굶은 묶음 전부를 이기므로 탈출구가 구조적으로 보장된다 —
  그래서 강등에 유효기간을 안 건다. 그리고 종료 선언은 굶김 전용 갈래보다 **위**여야 읽힌다.
  그 갈래는 무조건 `return` 하므로 아래에 두면 굶은 묶음끼리는 이 축이 영영 안 읽히는데,
  지금 큐는 열린 30건 중 26건이 굶었다(2026-08-09 실측) — 강등이 겨냥한 인구 전체에 대해
  무동작이 된다.
```

**⚠ 함정.** 개정 블록에 옛 문장을 그대로 인용하면 안 된다 — `zeroTuningClaimRe` 가 `조정할 상수가 0개` 를 **파일 전체에서** 잡으므로 인용 한 줄이 이 관문을 스스로 빨갛게 만든다. 위 문안이 `키를 "넷", 상수를 "0"으로 적었다` 로 풀어 쓴 이유가 이것이다.

Expected: DESIGN.md 만 바뀐다. `TestDesignSortKeyParagraphNamesEveryLiveAxis` 는 초록이 되고, `TestNoOneClaims...` 는 아직 bundle.go:378 하나로 빨갛다.

- [ ] **Step 3: bundle.go:378 의 lessBundle doc 첫 줄을 고친다**

`server/internal/judge/bundle.go:378`.

BEFORE:
```go
// lessBundle 은 추천 순서다. 조정할 상수가 하나도 없다.
```

AFTER:
```go
// lessBundle 은 추천 순서다. **이 함수 안에는** 조정할 상수가 없다 — 비교자는 필드만 읽는다.
//
// ★ 그러나 순서 전체가 무상수인 것은 아니다. 축 하나(Starved)는 바깥의 실측 상수
// StarvationAge 가 정하고, 또 하나(CloseDeclared)는 원장 관측이 정한다. 이 문장을
// "상수가 0"으로 읽으면 안 된다 — 앞선 판이 그렇게 적혀 있었고, 그 사이 상수가
// 하나 생겼는데 아무도 못 봤다(sort_axis_doc_test.go 가 그래서 섰다).
```

**⚠ 함정.** AFTER 문안에 `조정할 상수가 하나도 없다` 나 `조정할 상수가 0개` 를 다시 쓰면 안 된다 — 같은 관문이 잡는다. 위 문안은 `"상수가 0"으로 읽으면 안 된다` 로 우회했다.

Run: `gofmt -l ./internal && go test ./internal/judge/ -count=1 && go vet ./...`

Expected: gofmt 출력 없음. judge 패키지 ok(새 시험 둘 포함 전부 초록). vet 조용.

- [ ] **Step 4: 커밋**

```
docs(flightdeck): 정렬 키가 여섯이고 상수는 하나다 — 두 번 낡은 줄에 처음으로 관문을 물린다

DESIGN §3 의 정렬 키 줄은 기아 축이 들어올 때 한 번, 종료 선언이 들어올 때 또 한 번
낡았다. 두 번 다 시험이 안 빨개졌다 — 그 줄을 세는 관문이 하나도 없었기 때문이다.
스킬 수는 같은 실패를 겪고 이미 정규식 관문을 얻었는데 이쪽은 못 얻었다.

관문의 방향은 코드 → 문서다. bundle.go 에 축의 근거(StarvationAge 선언 ·
Bundle.CloseDeclared 필드)가 있으면 그 문단이 그 축을 이름으로 불러야 한다.
반대 방향은 일부러 안 건다 — DESIGN 은 구현보다 앞설 수 있는 문서다.

lessBundle 의 머리줄도 함께 고쳤다. 그 함수 안에 상수가 없는 것은 지금도 참이지만,
축 하나는 바깥 상수가 정하고 또 하나는 원장 관측이 정한다.
```

Run: `git add -A && git commit -F -`

Expected: 커밋 1개.

---

### Task 18: StarvationAge 의 근거가 거짓이 됐다 — 2026-08-09 실측으로 정정하고 날짜를 박는다
**Files:**
- Test: plugins/flightdeck/server/internal/judge/starvation_rationale_test.go (신규)
- Modify: plugins/flightdeck/server/internal/judge/bundle.go:12-16 (파일 머리말)
- Modify: plugins/flightdeck/server/internal/judge/bundle.go:171-184 (StarvationAge doc)
- Modify: plugins/flightdeck/server/internal/judge/bundle.go:380-383, 402-405 (lessBundle 축 목록·기아 절)

**Interfaces:**
- Consumes: Task 2 의 `judgeSource(t *testing.T, name string) string` (같은 패키지 `sort_axis_doc_test.go` 에 있다). **Task 2 뒤에 와야 컴파일된다.**
- Produces: `bundle.go` 의 StarvationAge 주석이 날짜 있는 실측 둘(2026-08-05 · 2026-08-09)과 잔량(열린 큐) 실측을 함께 담는다. lessBundle 의 축 목록이 여섯이 된다. 관문 `TestStarvationRationaleCarriesADatedMeasurement` 가 선다.

- [ ] **Step 1: 실패 시험을 쓴다 — 근거 있는 상수는 날짜 있는 실측을 달고 있어야 한다**

`plugins/flightdeck/server/internal/judge/starvation_rationale_test.go` 를 새로 만든다.

```go
package judge

import (
	"regexp"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// 근거 있는 상수는 **날짜 있는** 실측을 달고 있어야 한다
// ─────────────────────────────────────────────────────────────────────────────
//
// StarvationAge 는 "근거 없는 상수를 없앤다"의 산물이다. 그런데 근거로 적힌 리드타임
// 분포에 **날짜가 없었다.** 그래서 나흘 만에 p90 이 16.3h → 33.6h 로 올라 결론
// ("24h 는 p90 바깥이라 정상 작업이 안 걸린다")이 거짓이 됐는데도 아무도 못 봤다 —
// 그 문장 옆에서 열린 30건 중 26건이 굶은 상태가 유지됐다(2026-08-09 실측).
//
// 이 관문이 지키는 것은 값이 아니라 **실측의 유효기간 표기**다. 값을 바꿀 때도,
// 안 바꿀 때도, 언제 잰 것인지가 주석에 있어야 다음 사람이 다시 잴지를 판단한다.
//
// ★ 잔량(열린 큐의 나이)을 함께 요구하는 이유. 리드타임만 보면 임계를 p90 에
// 자동 추종시키게 되는데, 그러면 큐가 나빠질수록 경고가 사라진다 — 설계 §4 가
// 고발한 상시 점등의 거울상(상시 소등)이다.

var isoDateRe = regexp.MustCompile(`20\d{2}-\d{2}-\d{2}`)

// starvationDoc 은 StarvationAge 선언 바로 위의 주석 블록이다.
func starvationDoc(t *testing.T) string {
	t.Helper()
	src := judgeSource(t, "bundle.go")
	i := strings.Index(src, "// StarvationAge 는")
	if i < 0 {
		t.Fatalf("StarvationAge 의 주석 블록을 못 찾았다 — 이 시험의 좌표가 틀렸다")
	}
	j := strings.Index(src[i:], "\nconst StarvationAge")
	if j < 0 {
		t.Fatalf("StarvationAge 선언을 못 찾았다 — 이 시험의 좌표가 틀렸다")
	}
	return src[i : i+j]
}

func TestStarvationRationaleCarriesADatedMeasurement(t *testing.T) {
	doc := starvationDoc(t)

	if dates := isoDateRe.FindAllString(doc, -1); len(dates) < 2 {
		t.Errorf("StarvationAge 의 근거에 날짜가 %d개다 — 최초 측정과 재측 **둘**을 나란히 적어라. "+
			"날짜 없는 실측은 언제 거짓이 됐는지 아무도 못 본다.\n%s", len(dates), doc)
	}
	if strings.Contains(doc, "정상 작업이 안 걸린다") {
		t.Errorf("StarvationAge 가 아직 '정상 작업이 안 걸린다'고 말한다 — 2026-08-09 실측으로 "+
			"리드타임 p90 이 33.6h 이고 열린 30건 중 26건이 24h 를 넘겼다.\n%s", doc)
	}
	if !strings.Contains(doc, "열린") {
		t.Errorf("StarvationAge 의 근거가 리드타임만 말하고 잔량(열린 큐의 나이)을 안 말한다 — "+
			"리드타임만으로 이 값을 다시 정하면 큐가 나빠질수록 경고가 사라진다.\n%s", doc)
	}
	if StarvationAge.Hours() != 24 {
		t.Errorf("StarvationAge 가 %v 다 — 값을 바꿨으면 위 실측 줄에 그 판정을 함께 적어라", StarvationAge)
	}
}
```

Run: `go test ./internal/judge/ -run TestStarvationRationaleCarriesADatedMeasurement -v -count=1`

Expected: FAIL. 실패 줄 셋: 날짜 0개 · `정상 작업이 안 걸린다` 가 아직 있다 · 잔량(`열린`)을 안 말한다.

- [ ] **Step 2: StarvationAge 의 주석을 실측으로 정정한다**

`server/internal/judge/bundle.go` 171-184. 표 줄의 들여쓰기는 원문 그대로 `//\t`(주석 뒤 탭)다.

BEFORE:
```go
// StarvationAge 는 묶음 크기를 이기기 시작하는 나이다.
//
// ★ 임의의 값이 아니다. 리드타임 실측(kweiza-cc-plugins · done 81건 · created→closed)이
// 정한다:
//
//	중앙값 3.4h · 평균 6.7h · p90 16.3h · 최대 42.2h
//
// 24h 는 p90(16.3h) 바깥이라 **정상 작업이 안 걸린다.** 이 값을 p90 아래로 내리면
// 평시 항목이 줄줄이 기아로 판정돼 묶음 기능이 사실상 죽는다 — 그때는 기아 축이
// 예외가 아니라 새 기본값이 되고, 이 상수를 넣은 이유가 사라진다.
//
// "하루가 지나도 아무도 안 집었다"가 사람에게 설명 가능한 문장이라는 것도 값의
// 일부다. 원장이 낸 순위를 사람이 못 읽으면 두 번째 세션부터 무시된다.
const StarvationAge = 24 * time.Hour
```

AFTER:
```go
// StarvationAge 는 묶음 크기를 이기기 시작하는 나이다.
//
// ★ 임의의 값이 아니다. 리드타임 실측(kweiza-cc-plugins · created→closed)이 정한다.
// **두 시점을 나란히 적는다 — 날짜 없는 실측은 언제 거짓이 됐는지 아무도 못 본다.**
//
//	2026-08-05  done  81건 — 중앙값 3.4h · 평균  6.7h · p90 16.3h · 최대 42.2h
//	2026-08-09  done 127건 — 중앙값 6.1h · 평균 12.0h · p90 33.6h · 최대 80.1h
//
// ★★ **앞선 판의 결론은 지금 거짓이다.** 그 판은 "24h 가 p90(16.3h) 바깥이라 평시
// 작업이 안 걸린다"였다. p90 이 33.6h 로 올라 24h 를 넘겼고, 잔량은 더 심하다 —
// 2026-08-09 실측으로 **열린 30건 중 26건**(티클러 1건을 빼면 25건)이 24h 를 넘겼고
// 열린 항목 나이의 중앙값이 62.0h · 최대 74.5h 다. 기아는 예외가 아니라 **현재
// 기본값**이다. 앞선 판이 "그때는 이 상수를 넣은 이유가 사라진다"고 적어 둔 상태가
// 실제로 왔다.
//
// **그런데도 값을 안 올린다.** 임계를 리드타임 p90 에 자동 추종시키면 큐가 나빠질수록
// 경고가 사라진다 — 설계 §4 가 고발한 상시 점등의 정확한 거울상(상시 소등)이다.
// p90 이 올라간 것은 임계를 따라 올릴 근거가 아니라 **소화가 안 된다는 관측 그 자체**다.
//
// 대신 이 상수가 지금 **무엇을 하고 있는지**를 정직하게 적는다: 거의 전부가 기아라
// lessBundle 의 ②(묶음 크기)가 사실상 죽고 ③(최고령)이 실질 1차 키가 됐다. 그것은
// 사고가 아니라 의도한 동작이다 — 굶은 것들 사이에서 방어 가능한 규칙은 "가장 오래
// 굶은 것부터" 하나뿐이라고 아래 lessBundle 주석이 이미 못박아 뒀다. 그리고 그 사실이
// 종료 선언 축의 자리를 정했다(기아 아래 · 굶김 전용 갈래 위).
//
// **이 값을 다시 정할 근거는 리드타임이 아니라 잔량이다** — 열린 항목 나이의 중앙값이
// 24h 아래로 내려오면 그때 이 상수가 다시 "예외를 고르는" 일을 하게 된다. 그 시점에
// 위 표에 셋째 줄을 더해 판정한다.
//
// 재측 방법: `python3` stdlib `sqlite3` 로 `file:/home/<사용자>/.flightdeck/fd.db?mode=ro`
// 를 열고(물결을 그대로 쓰면 안 된다) `item` 의 created_at/closed_at 을 읽는다.
// **`cp` 로 뜬 사본을 재면 안 된다** — WAL 몫이 빠져 조용히 낮은 수가 나온다.
//
// "하루가 지나도 아무도 안 집었다"가 사람에게 설명 가능한 문장이라는 것도 값의
// 일부다. 원장이 낸 순위를 사람이 못 읽으면 두 번째 세션부터 무시된다.
const StarvationAge = 24 * time.Hour
```

**⚠ 함정.** AFTER 에 `정상 작업이 안 걸린다` 라는 정확한 문자열을 다시 쓰면 관문이 잡는다. 위 문안이 인용을 `평시 작업이 안 걸린다` 로 바꿔 쓴 이유가 이것이다.

Run: `go test ./internal/judge/ -run TestStarvationRationaleCarriesADatedMeasurement -v -count=1`

Expected: PASS.

- [ ] **Step 3: lessBundle 의 축 목록과 기아 절을 여섯 축에 맞춘다**

`server/internal/judge/bundle.go` 380-383.

BEFORE:
```go
//	① 의존자 수 합 ↓ — 이걸 풀어야 남이 움직이는 정도
//	② 묶음 크기   ↓ — 한 번에 더 많이 푸는 쪽이 이긴다
//	③ 최고령      ↑ — 오래 방치된 것을 먼저
//	④ 선두 id     사전순 — 동점 처리. 없으면 같은 입력에 다른 답이 나온다
```

AFTER:
```go
//	① 의존자 수 합 ↓ — 이걸 풀어야 남이 움직이는 정도
//	★ 기아         — StarvationAge 를 넘긴 묶음이 먼저. 상수는 실측이 정한다
//	★ 종료 선언    — 닫으려다 롤백된 선언이 있는 묶음을 **뒤로**. 거르지는 않는다
//	② 묶음 크기   ↓ — 한 번에 더 많이 푸는 쪽이 이긴다
//	③ 최고령      ↑ — 오래 방치된 것을 먼저
//	④ 선두 id     사전순 — 동점 처리. 없으면 같은 입력에 다른 답이 나온다
```

이어서 402-405 의 기아 절 **뒤에** 종료 선언 절을 덧붙인다.

BEFORE:
```go
// ★ 기아 영역 **안에서는 ②를 안 본다.** 다시 넣으면 굶은 단독이 굶은 묶음에 밀리는,
// 똑같은 함정이 그 안에서 재현된다. 예외 상태에서 방어 가능한 규칙은
// "가장 오래 굶은 것부터" 하나뿐이다.
func lessBundle(a, b Bundle) bool {
```

AFTER:
```go
// ★ 기아 영역 **안에서는 ②를 안 본다.** 다시 넣으면 굶은 단독이 굶은 묶음에 밀리는,
// 똑같은 함정이 그 안에서 재현된다. 예외 상태에서 방어 가능한 규칙은
// "가장 오래 굶은 것부터" 하나뿐이다.
//
// ★★★ **종료 선언은 기아 아래, 굶김 전용 갈래 위다.** 두 경계 다 뜻이 있다.
//
// 기아 **아래**인 것은 탈출구 때문이다. 강등된 항목도 굶는 순간 안 굶은 묶음 전부를
// 이기므로 영구 침몰이 구조적으로 불가능하다 — 그래서 강등에 유효기간을 안 건다
// (항목을 위험하게 만든 조건은 시간이 지난다고 낫지 않고, 기한 만료는 곧 사고 재현이다).
//
// 굶김 전용 갈래(아래 `if a.Starved`)보다 **위**인 것은 그 갈래가 무조건 `return` 하기
// 때문이다. 아래에 두면 굶은 묶음끼리는 이 축이 영영 안 읽히는데, 지금 큐는 열린 30건 중
// 26건이 굶었다(2026-08-09) — 강등이 겨냥한 인구 **전체**에 대해 무동작이 된다.
// 이 저장소에서 가장 미끄러운 자리라 시험이 굶은 묶음끼리도 축이 읽히는 것을 따로 단정한다.
//
// 그리고 이 축은 **거르지 않는다.** 강등이고, 왜 강등했는지를 Reason 과 not-top 사유에
// 함께 낸다 — Overlaps 가 "거르지 않고 알린다"로 선 것과 같은 자리다(설계 §5).
func lessBundle(a, b Bundle) bool {
```

Run: `gofmt -l ./internal && go test ./internal/judge/ -count=1`

Expected: gofmt 출력 없음. judge 패키지 ok.

- [ ] **Step 4: bundle.go 파일 머리말에 "바깥에서 들어오는 축 둘"을 적는다**

`server/internal/judge/bundle.go` 12-16.

BEFORE:
```go
// 묶음 판정 — pick 이 함께 갈 항목을 고르는 자리.
//
// 이 파일의 함수는 전부 순수 함수다. 그리고 **기존 판정을 하나도 안 고친다** —
// Eligible·PathsOverlap·lessCandidate 는 다른 질문에 답하고 있고,
// 같은 함수를 두 질문에 쓰면 한쪽을 고칠 때 다른 쪽이 조용히 바뀐다.
```

AFTER:
```go
// 묶음 판정 — pick 이 함께 갈 항목을 고르는 자리.
//
// 이 파일의 함수는 전부 순수 함수다. 그리고 **기존 판정을 하나도 안 고친다** —
// Eligible·PathsOverlap·lessCandidate 는 다른 질문에 답하고 있고,
// 같은 함수를 두 질문에 쓰면 한쪽을 고칠 때 다른 쪽이 조용히 바뀐다.
//
// ★ 다만 **바깥에서 들어오는 축이 둘** 있다 — 기아(EligibleInput.Now)와
// 종료 선언(EligibleInput.CloseDeclarations·CloseDeclarationsRead). 둘 다 **안 주면
// 안 돈다**: Now 가 zero 면 기아 판정을 건너뛰고, CloseDeclarationsRead 가 false 면
// 강등 블록을 통째로 건너뛴다. 순수 함수라는 성질은 그대로지만, 이 파일만 읽고
// "후보 목록만 보면 순서가 결정된다"고 믿으면 틀린다 — 그 둘을 안 채운 호출은
// **옛 순서를 그대로 낸다.** zero 값이 안전한 쪽(강등 안 함)이라 배선이 빠져도
// 큐가 안 뒤집히지만, 그 대가로 **배선이 빠진 것을 이 패키지의 시험은 원리적으로
// 못 본다.** 그것을 잠그는 것은 service 의 배선 시험이다(service/pick_wiring_test.go).
```

Run: `gofmt -l ./internal && go test ./internal/judge/ -count=1 && go vet ./... && GOOS=windows GOARCH=amd64 go vet ./... && GOOS=darwin GOARCH=arm64 go vet ./...`

Expected: gofmt 출력 없음, judge ok, vet 세 벌 전부 조용.

- [ ] **Step 5: 커밋**

```
docs(flightdeck): StarvationAge 의 근거가 나흘 만에 거짓이 됐다 — 실측을 날짜와 함께 다시 박는다

"24h 는 p90(16.3h) 바깥이라 평시 작업이 안 걸린다"는 지금 거짓이다. 2026-08-09
재측: 리드타임 p90 이 33.6h(done 127건)이고, 열린 30건 중 26건이 24h 를 넘겼다
(나이 중앙값 62.0h). 기아는 예외가 아니라 현재 기본값이다.

값은 안 올린다. 임계를 리드타임 p90 에 자동 추종시키면 큐가 나빠질수록 경고가
사라진다 — §4 가 고발한 상시 점등의 거울상이다. 올라간 p90 은 임계를 따라 올릴
근거가 아니라 소화가 안 된다는 관측 그 자체다. 대신 이 상수가 지금 실제로 무엇을
하는지(②가 죽고 ③이 1차 키가 됐다)와, 다시 정할 근거가 리드타임이 아니라 잔량
이라는 것을 적었다.

뿌리는 값이 아니라 표기였다 — 앞선 실측에 날짜가 없어서 언제 거짓이 됐는지
아무도 못 봤다. 관문이 이제 날짜 둘과 잔량 축을 요구한다.

같은 커밋에서 lessBundle 의 축 목록을 여섯으로 늘리고, 종료 선언이 기아 아래·
굶김 전용 갈래 위인 두 경계의 이유를 적었다.
```

Run: `git add -A && git commit -F -`

Expected: 커밋 1개.

---

### Task 19: §5 파생 표 · §10 지표 · eligible.go — 원장에서 파생되는 축을 문서와 주석에 앉힌다
**Files:**
- Test: plugins/flightdeck/server/internal/store/close_declaration_doc_test.go (신규 · 교차층 관문)
- Modify: plugins/flightdeck/DESIGN.md:389-390 (§5 파생 표 + 표 뒤 문단)
- Modify: plugins/flightdeck/DESIGN.md:1103 (§10 지표 목록)
- Modify: plugins/flightdeck/DESIGN.md:1377-1380 (§10 4차 실측 · R 오염)
- Modify: plugins/flightdeck/server/internal/judge/eligible.go:105-106 (Eligible doc)
- Modify: plugins/flightdeck/server/internal/judge/eligible.go:196-199 (lessCandidate doc)

**Interfaces:**
- Consumes: 없음. Task 2·3 과 파일이 안 겹친다(judge/eligible.go 의 주석 자리는 Task 2 의 관문이 스캔만 하고 안 고친다).
- Produces: DESIGN §5 파생 표에 `event(kind='item.finish')` 행이 서고, §10 에 강등 계수 지표 줄과 R 오염 ⚠ 가 선다. `internal/store` 에 교차층 관문 `TestLedgerDerivedAxisIsNamedInDesign` 이 생긴다 — store 층이 `CloseDeclarationsByItem` 을 넣는 순간 이 문서를 요구한다.

- [ ] **Step 1: 교차층 관문을 쓴다 — 지금은 통과하고, store 함수가 들어오는 순간 문서를 요구한다**

`plugins/flightdeck/server/internal/store/close_declaration_doc_test.go` 를 새로 만든다.

**이 관문은 red 가 아니다.** 그 사실을 시험 주석에 적는다 — 없는 빨간불을 있는 척하지 않는다. 이 태스크의 문서 편집 자체에는 기계 관문이 없고(그것이 설계 §8 이 이 자리들을 지목한 이유다), 이 시험이 하는 일은 **다음 커밋이 문서를 두고 가지 못하게 막는 것**이다.

```go
package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// 원장에서 파생하는 축은 §5 파생 표와 §10 지표에 이름이 있어야 한다
// ─────────────────────────────────────────────────────────────────────────────
//
// ★ **이 관문은 지금 통과한다.** CloseDeclarationsByItem 이 아직 없기 때문이다.
// 그러니 이 시험은 이 커밋의 빨간불이 아니다 — 없는 빨간불을 있는 척 세우지 않는다.
// 하는 일은 하나다: **그 함수가 들어오는 순간 문서를 요구한다.**
//
// 왜 필요한가. 설계 §8 이 지목한 네 자리는 전부 "시험이 문자열 존재만 보므로 빨간불
// 없이 표류한 산문"이었다. 문서 커밋과 코드 커밋이 갈리면 그 사이가 정확히 같은
// 모양의 구멍이고, 이 저장소는 그 구멍을 이미 여러 번 겪었다.
//
// 방향은 코드 → 문서다. 반대는 안 건다 — DESIGN 은 구현보다 앞설 수 있다(§0 머리말).
//
// 아래 두 문자열은 **앵커**다. 문서의 표현을 바꾸려면 이 시험도 같이 고쳐라 —
// 그 한 번의 수고가 이 관문의 전부이고, 그것이 없으면 관문이 조용히 눈이 먼다.

func TestLedgerDerivedAxisIsNamedInDesign(t *testing.T) {
	src, err := os.ReadFile("event.go")
	if err != nil {
		t.Fatalf("store/event.go 를 못 읽었다 — 이 시험의 좌표가 틀렸다: %v", err)
	}
	if !strings.Contains(string(src), "func (s *Store) CloseDeclarationsByItem") {
		t.Skip("CloseDeclarationsByItem 이 아직 없다 — 이 관문의 전제가 안 섰다")
	}

	p := filepath.Join("..", "..", "..", "DESIGN.md")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("DESIGN.md 를 못 읽었다(%s) — 이 시험의 좌표가 틀렸다: %v", p, err)
	}
	design := string(b)

	for _, want := range []string{
		"`event(kind='item.finish')` + `item.state`", // §5 파생 표의 원천 칸
		"종료 선언 축이 무엇을 몇 번 뒤로 밀었나",           // §10 지표 줄
	} {
		if strings.Contains(design, want) {
			continue
		}
		t.Errorf("store 가 원장에서 종료 선언을 파생하는데 DESIGN 에 %q 가 없다 — "+
			"파생 표(§5)와 지표(§10) 둘 다 그 축을 이름으로 불러야 한다", want)
	}
}
```

Run: `go test ./internal/store/ -run TestLedgerDerivedAxisIsNamedInDesign -v -count=1`

Expected: SKIP (`CloseDeclarationsByItem 이 아직 없다`). 통과지 초록 거짓이 아니다 — 건너뛴 이유가 출력에 있다.

- [ ] **Step 2: DESIGN §5 파생 표에 원장 파생 행을 더한다**

`plugins/flightdeck/DESIGN.md` 389-390.

BEFORE:
```
| 핸드오프의 후속 항목 id | `judgment_link` 행 |
| 대시보드 전체 | 위 전부의 뷰 |
```

AFTER:
```
| 핸드오프의 후속 항목 id | `judgment_link` 행 |
| 큐 항목이 이미 "닫자"고 선언됐나 | `event(kind='item.finish')` + `item.state` — 선언이 있는데 안 닫혔으면 그 시도는 롤백됐다 |
| 대시보드 전체 | 위 전부의 뷰 |

**`item.finish` 줄만 출처가 다르다 — 손 기재가 아니라 원장이다.** 나머지는 "사람이 적던 것을
파생으로 바꿨다"라서 쓰기 API 에서 필드가 사라진 자리이고, 종료 선언은 애초에 적을 필드가
없었다. 그래서 이 줄이 표에 있는 이유도 다르다 — **파생을 저장하지 않기로 한 판정을 여기
박아 두는 것**이다. `item` 에 컬럼을 더하면 원장과 갈라지는 순간 되짚을 길이 없고, 위 여덟 줄이
전부 피한 실패가 바로 그것이다. 새 이벤트 종류를 만들지 않은 이유도 같다: 신호가 이미 원장에
있고, 새로 만들면 지금 큐에 쌓인 과거 사례에 소급되지 않는다.

**그리고 이 수는 하한이다.** `LogEvent` 는 쓰기 실패를 WARN 으로만 삼키고(`store/event.go`),
롤백 뒤에 흘리는 `flushDeferred` 는 트랜잭션이 물고 있던 `ctx` 를 그대로 쓴다 — 클라이언트가
끊겨 `ctx` 가 취소되면 행이 안 남는다. **"0건"은 "없었다"가 아니다.** 이 축을 쓰는 모든 표면의
문구가 그렇게 말해야 하고, 임계값을 세우는 데 쓰면 안 된다.
```

Expected: DESIGN.md 만 바뀐다.

- [ ] **Step 3: DESIGN §10 지표 목록에 강등 계수를 더한다**

`plugins/flightdeck/DESIGN.md` 1103.

BEFORE:
```
- 큐 판정 결과(탈락 사유 분포)
```

AFTER:
```
- 큐 판정 결과(탈락 사유 분포)
- **종료 선언 축이 무엇을 몇 번 뒤로 밀었나** — `pick_eval.rejected` 의 `not-top` detail 에 그 사실을
  함께 싣는다. 강등은 **탈락이 아니라 순서 변경**이라 위 줄의 사유 분포에는 원리적으로 안 나타난다.
  안 남기면 "조용히 버리는 것이 하나도 없다"가 형식만 지켜지고 목적은 안 지켜진다 —
  이 축이 실제로 무엇을 몇 번 밀어냈는지를 아무도 못 세게 된다.
  **이 수도 하한이다**(§5 의 마지막 파생 줄과 같은 이유)
```

**⚠ 함정.** 이 절에 문장을 더할 때 `신호`(또는 `signal`)와 `간격·분포·횟수·이력·누적·추세` 중 하나가 **같은 줄에 40자 이내로** 붙으면 `store/signal_is_not_history_test.go` 의 전수 그물이 잡는다. 위 문안은 `분포` 를 쓰지만 같은 줄에 `신호` 가 없어 안전하다. 표현을 바꿀 때 이 규칙을 지켜라 — 못 지키면 그 줄에 `값이 없다`·`쓸 수 없다` 같은 부정을 함께 적어야 통과한다.

Run: `go test ./internal/store/ -run 'TestSignalTableIsNotProposedAsHistory|TestSignalGuardActuallyCatches' -v -count=1`

Expected: 둘 다 PASS. `정본 문장을 한 줄도 못 봤다` 로 죽지 않는다.

- [ ] **Step 4: DESIGN §10 4차 실측에 R 오염을 적는다**

`plugins/flightdeck/DESIGN.md` 1377-1380. 그 문단 **뒤에** 덧붙인다.

BEFORE (그대로 두고, 이 뒤에 붙인다):
```
**그리고 화면 문장은 원인을 단정하지 않는다.** 이 축을 값 타입으로 내던 서버 판(0.10~0.12)은
집계가 실패해도 제로값을 실어 보내므로, wire 위의 존재/부재는 성공/실패의 대리값이 아니다 —
응답이 아는 것은 "원자료가 실렸나"뿐이고 문장도 딱 그만큼만 말한다. 기한 판정을 화면으로
할 때 그 한계를 함께 읽어라.
```

AFTER (같은 문단 + 새 문단):
```
**그리고 화면 문장은 원인을 단정하지 않는다.** 이 축을 값 타입으로 내던 서버 판(0.10~0.12)은
집계가 실패해도 제로값을 실어 보내므로, wire 위의 존재/부재는 성공/실패의 대리값이 아니다 —
응답이 아는 것은 "원자료가 실렸나"뿐이고 문장도 딱 그만큼만 말한다. 기한 판정을 화면으로
할 때 그 한계를 함께 읽어라.

**⚠ 그리고 분모·분자가 롤백된 시도를 함께 세고 있다(2026-08-09 발견).** `QueueReproduction` 은
`event` 의 `kind='item.finish'` 를 그대로 세고 항목 상태와 조인하지 않는다(`store/event.go`).
그런데 그 이벤트는 **트랜잭션이 롤백돼도 남는다** — `Store.Tx` 가 롤백 갈래에서도 `flushDeferred`
를 부르기 때문이고, 그것은 결함이 아니라 "무엇을 시도했다 실패했나"를 남기려는 설계다.
결과적으로 실패한 마무리가 분모에 1을 더하고 그 payload 의 `count` 가 분자에 그대로 들어가며,
같은 항목이 나중에 성공하면 **한 번 더** 들어간다. 실측: `item.finish` 385건 중 롤백 4건(1.0%,
`item.finish.fail` 과 같은 초에 짝지어 있다). 전 기간으로는 작지만 **롤링 창이 20이라 한 건이
5%다** — 위 기한을 롤링 R 로 판정할 것이면 이것부터 닫아야 한다. 고칠 자리는 `finish.go` 가
이미 같은 tx 에서 잇기형에 대해 세운 가드("만들지도 않은 항목을 R 의 분자로 더하면 §10 이
조용히 거짓이 된다")를 **롤백 갈래로 넓히는 것**이다.
```

Expected: DESIGN.md 만 바뀐다.

- [ ] **Step 5: eligible.go 에 "이 축은 여기 일부러 없다"를 적는다**

`server/internal/judge/eligible.go` 105-106 (Eligible doc).

BEFORE:
```go
// 정렬은 의존자 수 많은 것 → 오래된 것 → id 사전순이다. 마지막 축은 동점 처리이고,
// 없으면 같은 입력에 다른 답이 나올 수 있다(입력 순서에 의존하게 된다).
```

AFTER:
```go
// 정렬은 의존자 수 많은 것 → 오래된 것 → id 사전순이다. 마지막 축은 동점 처리이고,
// 없으면 같은 입력에 다른 답이 나올 수 있다(입력 순서에 의존하게 된다).
//
// ★ **기아도 종료 선언도 이 함수에는 없다 — 일부러다.** 둘 다 EligibleBundle 이
// Bundle 에 찍고 lessBundle 이 읽는다(bundle.go). 여기 사본을 만들지 않은 이유는
// 이 함수를 제품이 **안 부르기** 때문이다: 비시험 호출자가 저장소 전체에서 0건이고
// (2026-08-09 전수) 제품 경로는 EligibleBundle 하나뿐이다. 그러니 여기 축을 더해도
// 바뀌는 것은 judge 시험이 보는 값뿐이고, 대신 같은 규칙이 두 벌이 되어 조용히
// 표류한다 — 이 파일이 이미 그 이유로 SamePaths 를 PathsOverlap 에서 갈라 뒀다.
// 이 함수 자체의 처분은 큐 항목 fd-eligible-dead-function-disposal 이 정한다.
```

이어서 196-199 (lessCandidate doc).

BEFORE:
```go
// lessCandidate 는 추천 순서다. 의존자 수 많은 것 → 오래된 것 → id 사전순.
//
// 순수 함수로 빼 둔 이유는 시험이 정렬 규칙을 **직접** 부를 수 있게 하기 위해서다.
// 정렬이 Eligible 본문에 있으면 시험이 그 규칙의 사본을 단정하게 된다.
```

AFTER:
```go
// lessCandidate 는 추천 순서다. 의존자 수 많은 것 → 오래된 것 → id 사전순.
//
// 순수 함수로 빼 둔 이유는 시험이 정렬 규칙을 **직접** 부를 수 있게 하기 위해서다.
// 정렬이 Eligible 본문에 있으면 시험이 그 규칙의 사본을 단정하게 된다.
//
// ★ **강등 축(Bundle.CloseDeclared)은 여기 안 들어간다.** 이 비교자가 제품에서
// 살아 있는 자리는 EligibleBundle 의 fit 정렬 하나뿐이고(bundle.go 의
// sort.SliceStable), 그 순서가 흘러가는 곳은 묶음 **구성원의 표시 순서**다 —
// 무엇을 추천하느냐는 lessBundle 이 정한다(그쪽은 선두 id 로 끝나는 전순서라
// 안정 정렬의 입력 순서가 결과를 못 바꾼다). 여기 넣으면 강등이 두 곳에서 나고,
// 그러면 "강등을 한 번 했나 두 번 했나"를 화면에서 가를 관측점이 사라진다.
```

Run: `gofmt -l ./internal && go vet ./... && GOOS=windows GOARCH=amd64 go vet ./... && GOOS=darwin GOARCH=arm64 go vet ./... && go test ./internal/... -count=1`

Expected: gofmt 출력 없음. vet 세 벌 조용. `go test ./internal/...` 전부 ok — 특히 `internal/store`(신호 그물)와 `internal/judge`(Task 2·3 관문)가 초록.

- [ ] **Step 6: 커밋**

```
docs(flightdeck): 원장에서 파생하는 축을 §5 표와 §10 지표에 앉히고, R 이 롤백을 세고 있음을 적는다

§5 파생 표의 새 줄만 출처가 다르다 — 손 기재가 아니라 원장이다. 그래서 표에 있는
이유도 다르다: 파생을 저장하지 않기로 한 판정을 여기 박아 두는 것이다. item 에
컬럼을 더하면 원장과 갈라지는 순간 되짚을 길이 없다.

§10 에는 강등 계수를 더했다. 강등은 탈락이 아니라 순서 변경이라 사유 분포에는
원리적으로 안 나타난다 — not-top detail 에 안 실으면 이 축이 무엇을 몇 번
밀어냈는지 아무도 못 센다.

그리고 R 의 분모·분자가 롤백된 시도를 함께 세고 있다(item.finish 385건 중 4건).
전 기간으로는 1%지만 롤링 창이 20이라 한 건이 5%다. 2026-08-21 기한을 롤링 R 로
판정할 것이면 이것부터 닫아야 한다.

eligible.go 에는 "이 축은 여기 일부러 없다"를 적었다. 안 적으면 다음 세션이
비대칭을 결함으로 보고 사본을 하나 더 만든다.

store 쪽 관문은 지금 SKIP 한다 — CloseDeclarationsByItem 이 아직 없다.
그 함수가 들어오는 순간 이 문서를 요구한다.
```

Run: `git add -A && git commit -F -`

Expected: 커밋 1개.

---

