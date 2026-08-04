# 경로 좌표계 관문 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 잘못된 좌표계의 경로가 "겹침 없음"이라는 정상 응답으로 조용히 죽는 것을, 입구에서 사유를 말하는 거절로 바꾼다.

**Architecture:** `internal/judge` 에 좌표계 판정 순수 함수를 하나 두고, 경로가 DB 로 들어가는 입구 셋에서 그것을 부른다. 세션 등록과 `add` 는 거절하고, 발자국은 버리되 관측 가능하게 남긴다. `judge.components` 는 **고치지 않고** 그 결정을 회귀 시험으로 지킨다.

**Tech Stack:** Go 1.25.0, 표준 라이브러리만. 시험은 `testing` 표 시험.

## Global Constraints

- 설계 스펙: `docs/superpowers/specs/2026-08-05-fd-windows-path-overlap-silent-design.md` (커밋 `a4b2672`). 이 계획과 어긋나면 **스펙이 정본이다.**
- 작업 디렉토리는 전부 `plugins/flightdeck/server` 다. 이 문서의 `go` 명령은 거기서 돈다.
- 브랜치 `fd-windows-path-overlap-silent`, 워크트리 `.flightdeck/worktrees/fd-windows-path-overlap-silent`.
- **`internal/judge` 는 순수 함수만 담는다** — 상태도 I/O 도 없다(패키지 주석). 새 함수도 그 규율을 지킨다.
- **판정은 불리언이 아니라 사유를 돌려준다**(`judge` 패키지 주석). 사유가 비면 이 항목의 목적이 사라진다.
- 주석과 사유 문자열은 **한국어**다. 이 저장소 전체가 그렇다.
- **`store/schema.sql` 을 만지지 않는다.** 이관 대상이 실측 0건이라 마이그레이션이 없다(스펙 §2.1).
- **`judge.components` 를 고치지 않는다**(스펙 §3.2). Task 1 의 회귀 시험이 이 결정을 지킨다.
- 크로스컴파일 검사·CI 신설은 범위 밖이다(스펙 §3.3).
- 여러 세션이 같은 파일을 동시에 만진다. `service/session.go` · `service/pick.go` · `service/service.go` 는 **삽입만** 한다 — 기존 줄을 지우거나 옮기지 않는다.

---

## File Structure

| 파일 | 책임 | 변경 |
|---|---|---|
| `internal/judge/paths.go` | 경로 성분 비교 + **좌표계 판정(신규)** | 파일 끝에 추가. 기존 함수 불변 |
| `internal/judge/paths_test.go` | 위의 시험 | 파일 끝에 추가 |
| `internal/service/service.go` | 서비스 공용 순수 함수(`RelPath`·`UnionPaths`) | Task 2: `RelPath` 반환 한 줄 · Task 5: 파일 끝에 별칭 + 위임 |
| `internal/service/pure_test.go` | service 의 **순수 함수 시험이 모이는 자리** | 파일 끝에 추가 |
| `internal/service/session.go` | 세션 판정·신호·발자국 | `JudgeOpenSession` 에 절 하나, `Beat` 에 필터 |
| `internal/service/session_test.go` | 위의 시험 | 파일 끝에 추가 |
| `internal/service/pick.go` | 큐 항목 등록·선점 | `AddItem` 에 검증 루프 |
| `internal/service/pick_test.go` | 위의 시험 | 파일 끝에 추가 |
| `internal/api/handlers_session.go` | 세션 REST 표면 | `NormalizeFootprints` 시그니처 + `handleFootprints` 응답 |
| `internal/api/pure_test.go` | api 의 순수 함수 시험 — **기존 `TestNormalizeFootprints`(331행)를 고쳐야 한다** | 수정 + 추가 |

Task 1 이 나머지 넷의 의존이다. Task 2~5 는 서로 독립이며 순서를 바꿔도 된다.

## 시험 하네스 (실측 — 지어내지 마라)

이 저장소의 시험은 **임시 DB + 실물 git 저장소**로 돈다. 가짜 리더만 쓰면 "내가 만든 값을
내가 읽는다"가 되기 때문이다(`internal/service/helper_test.go` 주석).

| 헬퍼 | 시그니처 | 자리 |
|---|---|---|
| `newSvc` | `newSvc(t *testing.T) (*Service, *store.Store)` | `internal/service/helper_test.go:31` |
| `ctx` | `ctx() context.Context` | `internal/service/helper_test.go:130` |
| `newRepo` | `newRepo(t *testing.T) string` — 실물 git 저장소를 만들고 경로를 낸다 | `internal/service/helper_test.go` |
| `openSession` | `openSession(t, s *Service, project, projectPath, worktree, ccID, label string) SessionResult` | `internal/service/helper_test.go:96` |
| `addItem` | `addItem(t, s *Service, project, id string, paths []string, after []model.After) model.Item` — **실패하면 `t.Fatalf` 한다.** 거절을 기대하는 시험은 `s.AddItem` 을 직접 불러라 | `internal/service/helper_test.go:110` |

`internal/service/pure_test.go` 는 `strings`·`filepath`·`testing`·`time`·`model` 을 이미
임포트하고 있다. `internal/service/pick_test.go` 는 `strings`·`judge`·`model`·`store` 를 이미
임포트하고 있다.

---

### Task 1: 좌표계 판정 순수 함수 + `components` 결정 방어

**Files:**
- Modify: `internal/judge/paths.go` (파일 끝에 추가 — 1~106행은 건드리지 않는다)
- Test: `internal/judge/paths_test.go` (파일 끝에 추가)

**Interfaces:**
- Consumes: 없음 (이 패키지의 기존 `components`·`PathsOverlap` 만 시험에서 참조)
- Produces:
  - `judge.CoordinateVerdict{OK bool, Reason string}` — 두 필드 모두 json 태그(`ok`·`reason`)
  - `judge.JudgePathCoordinate(p string) CoordinateVerdict`
  - `judge.RejectedPath{Path string, Reason string}` — json 태그(`path`·`reason`)
  - `judge.FilterPathCoordinate(paths []string) (kept []string, rejected []RejectedPath)`

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/judge/paths_test.go` **파일 끝**에 붙인다:

```go
// 좌표계 관문 — 잘못된 좌표계의 경로는 '겹침 없음'이 아니라 **사유**를 받아야 한다.
// 사유가 비면 이 판정의 존재 이유가 사라지므로 사유 조각도 함께 단정한다.
func TestJudgePathCoordinate(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
		want string // 사유에 반드시 들어 있어야 하는 조각
	}{
		{"드라이브 절대경로(백슬래시)", `C:\repo\x.go`, false, "드라이브 절대경로"},
		{"드라이브 절대경로(슬래시)", `C:/repo/x.go`, false, "드라이브 절대경로"},
		{"소문자 드라이브", `d:\a`, false, "드라이브 절대경로"},
		{"UNC", `\\server\share\x.go`, false, "UNC"},
		{"상대 백슬래시", `internal\api\x.go`, false, "백슬래시"},
		{"백슬래시 하나만", `a\b`, false, "백슬래시"},

		{"정상 상대경로", "internal/api/x.go", true, "슬래시 좌표계"},
		{"정상 절대경로", "/home/a/repo/x.go", true, "슬래시 좌표계"},
		{"디렉토리 토큰", "tools/", true, "슬래시 좌표계"},
		{"파일형 토큰", "Makefile", true, "슬래시 좌표계"},
		{"빈 문자열", "", true, "빈 경로"},
		{"공백만", "   ", true, "빈 경로"},

		// ── 이 축이 아닌 것 (다른 판정의 몫이다) ──
		{".. 는 좌표계 축이 아니다", "../a/b.go", true, "슬래시 좌표계"},
		{"콜론이 있어도 드라이브가 아니면", "a:b", true, "슬래시 좌표계"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := JudgePathCoordinate(c.in)
			if got.OK != c.ok {
				t.Fatalf("JudgePathCoordinate(%q).OK = %v, want %v (사유: %s)",
					c.in, got.OK, c.ok, got.Reason)
			}
			if got.Reason == "" {
				t.Fatal("사유가 비었다 — 사유 없는 판정은 이 패키지에서 금지다")
			}
			if !strings.Contains(got.Reason, c.want) {
				t.Fatalf("사유 %q 에 %q 가 없다", got.Reason, c.want)
			}
		})
	}
}

// 거절 사유는 무엇을 해야 하는지 말해야 한다 — "안 된다"만으로는 사람이 못 고친다.
func TestJudgePathCoordinateReasonCarriesGuidance(t *testing.T) {
	v := JudgePathCoordinate(`C:\repo\x.go`)
	for _, want := range []string{"POSIX", "WSL"} {
		if !strings.Contains(v.Reason, want) {
			t.Errorf("사유에 %q 가 없다: %s", want, v.Reason)
		}
	}
}

// 백슬래시 거절은 POSIX 합법 파일명 하나를 실제로 막는다. 그 사실을 사유가 숨기지 않는지 본다.
func TestJudgePathCoordinateAdmitsItRejectsALegalPOSIXName(t *testing.T) {
	v := JudgePathCoordinate(`a\b`)
	if !strings.Contains(v.Reason, "지원하지 않는다") {
		t.Errorf("사유가 '지원하지 않는다'를 말하지 않는다 — 침묵의 반대는 명시다: %s", v.Reason)
	}
}

func TestFilterPathCoordinate(t *testing.T) {
	kept, rejected := FilterPathCoordinate([]string{
		"internal/api/x.go",
		`C:\repo\y.go`,
		"Makefile",
		`z\w.go`,
	})
	wantKept := []string{"internal/api/x.go", "Makefile"}
	if len(kept) != len(wantKept) {
		t.Fatalf("kept = %q, want %q", kept, wantKept)
	}
	for i := range wantKept {
		if kept[i] != wantKept[i] {
			t.Fatalf("kept = %q, want %q", kept, wantKept)
		}
	}
	if len(rejected) != 2 {
		t.Fatalf("rejected %d건, want 2건: %+v", len(rejected), rejected)
	}
	if rejected[0].Path != `C:\repo\y.go` || rejected[0].Reason == "" {
		t.Errorf("첫 거절이 원본 경로와 사유를 함께 날라야 한다: %+v", rejected[0])
	}
}

func TestFilterPathCoordinateEmptyInput(t *testing.T) {
	kept, rejected := FilterPathCoordinate(nil)
	if len(kept) != 0 || len(rejected) != 0 {
		t.Fatalf("빈 입력에 kept=%q rejected=%+v", kept, rejected)
	}
}

// ★ 이 시험은 결함을 고발하지 않는다. **결정을 지킨다.**
//
// components 는 백슬래시를 성분 구분자로 보지 않는다. 그것이 옳은 이유:
// POSIX 에서 백슬래시는 파일명에 쓸 수 있는 합법 문자라 `a\b` 는 성분 하나짜리 정상
// 파일명이다. 구분자에 넣으면 그 파일이 `a/b` 와 겹친다고 **오탐**한다 —
// 침묵 하나를 오탐 하나와 바꾸는 거래이고, 이 도구에서 오탐은 침묵만큼 나쁘다.
//
// Windows 경로가 여기 도달하지 않게 막는 자리는 이 함수가 아니라 입구의
// JudgePathCoordinate 다(스펙 §3.2 · §4.2).
//
// **이 시험이 깨졌다면 그 결정을 되돌리는 변경을 하고 있는 것이다. 스펙을 먼저 읽어라.**
func TestComponentsDeliberatelyDoesNotSplitBackslash(t *testing.T) {
	got := components(`a\b`)
	if len(got) != 1 || got[0] != `a\b` {
		t.Fatalf(`components("a\b") = %q — 성분 1개 [a\b] 여야 한다. 위 주석을 읽어라`, got)
	}
	if PathsOverlap([]string{`a\b`}, []string{"a/b"}) {
		t.Fatal(`a\b 와 a/b 가 겹친다고 나왔다 — POSIX 합법 파일명을 오탐하고 있다. 위 주석을 읽어라`)
	}
}
```

같은 파일 상단 import 를 `import "testing"` 에서 다음으로 바꾼다:

```go
import (
	"strings"
	"testing"
)
```

- [ ] **Step 2: 시험이 실패하는 것을 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/judge/ -run 'Coordinate|Components' 2>&1 | head -20`
Expected: 컴파일 실패 — `undefined: JudgePathCoordinate`, `undefined: FilterPathCoordinate`

- [ ] **Step 3: 최소 구현을 쓴다**

`internal/judge/paths.go` **파일 끝**에 붙인다:

```go
// ── 좌표계 관문 ──────────────────────────────────────────────────────────
//
// 이 판정이 있는 이유는 위 components 가 슬래시만 성분 구분자로 보기 때문이다.
// 백슬래시 경로가 저장까지 도달하면 성분 1개가 되어 git 이 주는 경로와 **절대 안 겹치고**,
// 그 결과는 오류가 아니라 '겹침 없음'이라 정상 응답과 구분되지 않는다.
//
// ★ 고치는 자리를 components 가 아니라 **입구**로 잡은 것이 이 설계의 핵심 판단이다.
// POSIX 에서 백슬래시는 파일명에 쓸 수 있는 합법 문자라, components 가 그것을 구분자로
// 보면 `a\b` 라는 정상 파일이 `a/b` 와 겹친다고 오탐한다. 침묵을 오탐과 바꾸는 거래다.
// 근거 전문은 스펙 §3.2 에 있다.

// CoordinateVerdict 는 경로 좌표계 판정 결과다. 사유는 항상 채운다.
//
// ★ 같은 패키지의 ItemPathVerdict 와 **다른 축**이다 — 저쪽은 "경로가 그 프로젝트에
// 실재하는가"(파일시스템 관측)이고, 이쪽은 "경로가 이 서버의 좌표계인가"(문자열 형태)다.
// 둘을 합치지 마라. 실재 판정은 I/O 결과를 받아야 하고 이것은 순수 문자열 판정이다.
type CoordinateVerdict struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason"`
}

// RejectedPath 는 관문이 버린 경로 하나와 그 사유다.
//
// ★ 원본 경로를 그대로 나른다. 정규화한 값을 실으면 사람이 자기가 넣은 것을 못 알아본다.
type RejectedPath struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// coordinateGuidance 는 거절마다 붙는 처방이다.
// "안 된다"만 말하는 거절은 사람이 못 고치고, 못 고치는 거절은 무시된다.
const coordinateGuidance = "이 서버는 POSIX 좌표계(슬래시)만 받는다 — " +
	"Windows 에서는 WSL 로 띄우고 /mnt/c/… 경로를 써라"

// JudgePathCoordinate 는 경로가 이 서버의 좌표계에 맞는지 판정한다. 순수 함수다.
//
// 빈 경로는 **통과시킨다** — 빈 값의 처리는 호출부마다 다르고(worktree 는 거절,
// 발자국은 무시) 그 판정을 여기서 가로채면 호출부의 사유가 이 함수의 사유로 덮인다.
//
// ".." 성분도 통과시킨다. 그것은 실재하는 문제이지만 좌표계 축이 아니다 —
// 상대경로 정책 전반을 함께 정해야 하므로 분리했다(스펙 §6).
func JudgePathCoordinate(p string) CoordinateVerdict {
	q := strings.TrimSpace(p)
	switch {
	case q == "":
		return CoordinateVerdict{OK: true,
			Reason: "빈 경로다 — 좌표계 축은 통과시킨다(호출부가 따로 다룬다)"}
	case isWindowsDriveAbs(q):
		return CoordinateVerdict{Reason: fmt.Sprintf(
			"%q 는 Windows 드라이브 절대경로다. %s", clipPath(q), coordinateGuidance)}
	case strings.HasPrefix(q, `\\`):
		return CoordinateVerdict{Reason: fmt.Sprintf(
			"%q 는 Windows UNC 경로다. %s", clipPath(q), coordinateGuidance)}
	case strings.ContainsRune(q, '\\'):
		return CoordinateVerdict{Reason: fmt.Sprintf(
			"%q 에 백슬래시가 들어 있다. %s. "+
				"정말 파일명에 백슬래시가 있는 것이라면 이 도구는 그 경로를 지원하지 않는다 — "+
				"POSIX 에서는 합법 문자이지만 겹침 판정이 성분을 가르지 못한다",
			clipPath(q), coordinateGuidance)}
	}
	return CoordinateVerdict{OK: true, Reason: "슬래시 좌표계다"}
}

// isWindowsDriveAbs 는 "C:\…" · "C:/…" 형태인지 본다.
//
// 구분자까지 함께 보는 이유는 "a:b" 같은 정상 POSIX 파일명을 드라이브로 오인하지 않기
// 위해서다 — 콜론은 POSIX 파일명에 합법이다.
func isWindowsDriveAbs(p string) bool {
	if len(p) < 3 || p[1] != ':' {
		return false
	}
	c := p[0]
	if !(('A' <= c && c <= 'Z') || ('a' <= c && c <= 'z')) {
		return false
	}
	return p[2] == '\\' || p[2] == '/'
}

// FilterPathCoordinate 는 목록을 관문에 태워 통과분과 버린 것을 가른다. 순수 함수다.
//
// 거르는 쪽과 버린 쪽을 **둘 다** 돌려주는 것이 요점이다. 버린 것을 안 돌려주면
// 호출부가 "몇 개가 사라졌는지"를 말할 수 없고, 그 침묵이 이 항목이 없애려는 것이다.
func FilterPathCoordinate(paths []string) (kept []string, rejected []RejectedPath) {
	for _, p := range paths {
		if v := JudgePathCoordinate(p); !v.OK {
			rejected = append(rejected, RejectedPath{Path: p, Reason: v.Reason})
			continue
		}
		kept = append(kept, p)
	}
	return kept, rejected
}

// clipPath 는 사유에 싣는 경로를 자른다.
// 사유가 화면을 덮으면 사유가 없는 것과 같아진다(verify.go 의 clipPat 과 같은 이유다).
func clipPath(p string) string {
	const n = 200
	rs := []rune(p)
	if len(rs) <= n {
		return p
	}
	return string(rs[:n]) + "…"
}
```

같은 파일 상단 import 를 `import "strings"` 에서 다음으로 바꾼다:

```go
import (
	"fmt"
	"strings"
)
```

- [ ] **Step 4: 시험이 통과하는 것을 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/judge/ -v -run 'Coordinate|Components' 2>&1 | tail -30`
Expected: 모든 시험 PASS

- [ ] **Step 5: 패키지 전체 시험으로 회귀가 없는지 본다**

Run: `cd plugins/flightdeck/server && go test ./internal/judge/ && gofmt -l internal/judge/`
Expected: `ok`, `gofmt -l` 출력 없음

- [ ] **Step 6: 커밋**

```bash
cd plugins/flightdeck/server
git add internal/judge/paths.go internal/judge/paths_test.go
git commit -m "feat(flightdeck): 경로 좌표계 관문을 순수 함수로 세운다 — components 는 일부러 안 고친다

백슬래시 경로가 저장까지 도달하면 components 가 성분 1개로 만들어 git 경로와 절대
안 겹치고, 그 결과가 오류가 아니라 '겹침 없음'이라 정상 응답과 구분되지 않는다.

고치는 자리를 components 가 아니라 입구로 잡았다. POSIX 에서 백슬래시는 합법
파일명 문자라 구분자에 넣으면 a\b 가 a/b 와 겹친다고 오탐한다 — 침묵을 오탐과
바꾸는 거래다. 그 결정을 회귀 시험이 지킨다.

거절 사유에 처방(WSL 안내)을 함께 싣고, 백슬래시 거절이 합법 POSIX 파일명 하나를
실제로 막는다는 사실도 사유에 명시한다. 침묵의 반대는 명시다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 2: `RelPath` 의 출력 계약을 코드로 못박는다

**Files:**
- Modify: `internal/service/service.go:378` (`return rel` 한 줄)
- Test: `internal/service/pure_test.go` (파일 끝에 추가 — **`service_test.go` 는 없다.** 이 패키지의 순수 함수 시험은 `pure_test.go` 에 모인다. `TestRelPathUsesComponentsNotStringPrefix` 가 15행에 이미 있으니 그것은 **건드리지 말고** 새 시험만 더한다)

**Interfaces:**
- Consumes: 없음
- Produces: 없음 (`service.RelPath(root, p string) string` 시그니처 불변)

> **이 시험은 Linux 에서 약하다** — `filepath.ToSlash` 가 무연산이므로 변경 전에도 통과한다.
> 그래서 Step 2 의 기대는 "실패"가 아니라 "이미 통과"다. 목적은 결함을 잡는 것이 아니라
> **계약을 주석이 아니라 코드와 시험에 두는 것**이다(스펙 §4.3). 이 항목의 실제 안전망은
> Task 1·3·4·5 다. 이 사실을 알고 진행해라 — 시험이 처음부터 초록인 것은 실수가 아니다.

- [ ] **Step 1: 계약 시험을 쓴다**

`internal/service/pure_test.go` **파일 끝**에 붙인다:

```go
// RelPath 의 출력은 슬래시 좌표계다 — 그 계약을 코드 밖에 못박는다.
//
// ★ Linux 에서 이 시험은 약하다(filepath.ToSlash 가 무연산이라 변경 전에도 통과한다).
// 그래도 두는 이유는 계약이 주석에만 있으면 다음 사람이 깨기 때문이다.
// 겹침 축 전체가 "모든 경로는 슬래시"라는 이 계약 위에 서 있다.
func TestRelPathOutputIsSlashCoordinate(t *testing.T) {
	cases := []struct {
		name       string
		root, in   string
		want       string
	}{
		{"저장소 안", "/repo", "/repo/internal/api/x.go", "internal/api/x.go"},
		{"저장소 밖은 원본", "/repo", "/other/x.go", "/other/x.go"},
		{"상대경로는 정리만", "/repo", "internal/./api/x.go", "internal/api/x.go"},
		{"빈 경로", "/repo", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RelPath(c.root, c.in)
			if got != c.want {
				t.Fatalf("RelPath(%q, %q) = %q, want %q", c.root, c.in, got, c.want)
			}
			if strings.ContainsRune(got, '\\') {
				t.Fatalf("출력 %q 에 백슬래시가 있다 — 슬래시 좌표계 계약을 깼다", got)
			}
		})
	}
}
```

import 은 손댈 필요가 없다 — `pure_test.go` 는 `strings` 를 이미 임포트하고 있다.

- [ ] **Step 2: 시험을 돌린다 (이미 통과한다 — 위 설명을 읽어라)**

Run: `cd plugins/flightdeck/server && go test ./internal/service/ -run TestRelPathOutputIsSlashCoordinate -v 2>&1 | tail -15`
Expected: PASS. 실패한다면 `RelPath` 의 기존 동작이 이 계획의 가정과 다른 것이므로 **멈추고 보고해라.**

- [ ] **Step 3: 계약을 코드에 넣는다**

`internal/service/service.go` 의 `RelPath` 마지막 줄을 바꾼다. **이 한 줄만 바꾼다:**

```go
	return rel
```

를

```go
	// ★ 출력은 슬래시 좌표계다. Linux 에서 이 호출은 무연산이지만, 계약을 주석이 아니라
	// 코드에 둔다 — 겹침 축 전체가 "모든 경로는 슬래시"라는 이 계약 위에 서 있고,
	// 계약이 주석에만 있으면 다음 사람이 깬다.
	return filepath.ToSlash(rel)
```

로 바꾼다. `filepath` 는 이 파일이 이미 임포트하고 있다.

- [ ] **Step 4: 시험이 여전히 통과하는지 본다**

Run: `cd plugins/flightdeck/server && go test ./internal/service/ && gofmt -l internal/service/`
Expected: `ok`, `gofmt -l` 출력 없음

- [ ] **Step 5: 커밋**

```bash
cd plugins/flightdeck/server
git add internal/service/service.go internal/service/pure_test.go
git commit -m "refactor(flightdeck): RelPath 의 슬래시 계약을 주석에서 코드로 옮긴다

Linux 에서 filepath.ToSlash 는 무연산이라 값이 바뀌지 않는다. 목적은 값이 아니라
계약이다 — 겹침 축 전체가 '모든 경로는 슬래시'라는 전제 위에 서 있는데 그 전제가
주석에만 있으면 다음 사람이 깬다.

시험도 같은 이유로 약하다는 것을 시험 주석에 적었다. 처음부터 초록인 것이 실수가
아니라는 사실이 코드에 남아야 한다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 3: 세션 등록 관문 — 사유를 정확하게

**Files:**
- Modify: `internal/service/session.go` (`JudgeOpenSession`, 57~75행 부근에 절 하나 삽입)
- Test: `internal/service/session_test.go` (파일 끝에 추가)

**Interfaces:**
- Consumes: `judge.JudgePathCoordinate` (Task 1)
- Produces: 없음 (`JudgeOpenSession(in OpenSessionInput) SessionVerdict` 시그니처 불변)

**왜 필요한가:** 지금 `C:\repo` 를 주면 Linux 서버의 `filepath.IsAbs` 가 `false` 를 내서
"절대경로가 아니다"라고 답한다. 사실이지만 **원인이 아니다** — 사용자는 자기가 준 것이
절대경로라고 알고 있으므로 이 사유로는 못 고친다.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/service/session_test.go` **파일 끝**에 붙인다:

```go
// Windows 경로를 주면 "절대경로가 아니다"가 아니라 **원인**을 말해야 한다.
// 사용자는 자기가 준 것이 절대경로라고 알고 있어서, 그 사유로는 고칠 수 없다.
func TestJudgeOpenSessionNamesWindowsPathAsTheCause(t *testing.T) {
	base := OpenSessionInput{
		Project: "p", MachineID: "m", CCSessionID: "cc",
	}
	cases := []struct {
		name     string
		worktree string
		want     string
	}{
		{"드라이브 절대경로", `C:\Users\a\repo`, "드라이브 절대경로"},
		{"UNC", `\\host\share\repo`, "UNC"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := base
			in.Worktree = c.worktree
			v := JudgeOpenSession(in)
			if v.OK {
				t.Fatal("Windows 경로를 통과시켰다")
			}
			if !strings.Contains(v.Reason, c.want) {
				t.Fatalf("사유 %q 가 원인(%q)을 안 짚는다", v.Reason, c.want)
			}
			if !strings.Contains(v.Reason, "WSL") {
				t.Errorf("사유에 처방(WSL)이 없다: %s", v.Reason)
			}
		})
	}
}

// 좌표계 판정이 기존 축을 가리면 안 된다 — 상대경로는 여전히 상대경로 사유를 받는다.
func TestJudgeOpenSessionKeepsExistingAxes(t *testing.T) {
	base := OpenSessionInput{Project: "p", MachineID: "m", CCSessionID: "cc"}

	in := base
	in.Worktree = "relative/path"
	if v := JudgeOpenSession(in); v.OK || !strings.Contains(v.Reason, "절대경로") {
		t.Errorf("상대경로 사유가 바뀌었다: ok=%v reason=%s", v.OK, v.Reason)
	}

	in = base
	in.Worktree = ""
	if v := JudgeOpenSession(in); v.OK || !strings.Contains(v.Reason, "worktree 가 비었다") {
		t.Errorf("빈 worktree 사유가 바뀌었다: ok=%v reason=%s", v.OK, v.Reason)
	}

	in = base
	in.Worktree = "/home/a/repo"
	if v := JudgeOpenSession(in); !v.OK {
		t.Errorf("정상 POSIX 경로를 거절했다: %s", v.Reason)
	}
}
```

`internal/service/session_test.go` 의 import 에 `"strings"` 가 없으면 더한다.

- [ ] **Step 2: 시험이 실패하는 것을 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/service/ -run TestJudgeOpenSession -v 2>&1 | tail -20`
Expected: `TestJudgeOpenSessionNamesWindowsPathAsTheCause` 가 FAIL — 사유가 "절대경로가 아니다"라서 "드라이브 절대경로"를 안 담는다

- [ ] **Step 3: 관문을 삽입한다**

`internal/service/session.go` 의 `JudgeOpenSession` 을 고친다. **`switch` 앞에 한 줄, `switch` 안에 절 하나를 삽입한다. 기존 절은 지우지도 옮기지도 않는다:**

```go
func JudgeOpenSession(in OpenSessionInput) SessionVerdict {
	wt := judge.JudgePathCoordinate(in.Worktree)
	switch {
	case strings.TrimSpace(in.Project) == "":
		return SessionVerdict{Reason: "project 가 비었다 — 어느 프로젝트의 세션인지 없이는 큐도 보드도 좌표가 없다"}
	case strings.TrimSpace(in.MachineID) == "":
		return SessionVerdict{Reason: "machine_id 가 비었다 — 세션 정체는 (machine, worktree, cc_session) 3중키다"}
	case strings.TrimSpace(in.Worktree) == "":
		return SessionVerdict{Reason: "worktree 가 비었다 — MCP 서버의 cwd 가 그 값이다(설계 §13)"}
	// ★ 이 절이 IsAbs 보다 **앞**이어야 한다. Linux 서버의 filepath.IsAbs 는 "C:\repo" 를
	// 절대경로로 안 보므로, 순서가 뒤바뀌면 Windows 사용자가 "절대경로가 아니다"라는
	// 사실이되 원인이 아닌 사유를 받는다 — 그 사유로는 고칠 수 없다.
	case !wt.OK:
		return SessionVerdict{Reason: wt.Reason}
	case !filepath.IsAbs(in.Worktree):
		return SessionVerdict{Reason: fmt.Sprintf(
			"worktree %q 가 절대경로가 아니다 — 상대경로는 서버와 세션이 서로 다른 곳을 가리킨다",
			clip(in.Worktree, 200))}
	case strings.TrimSpace(in.CCSessionID) == "":
		return SessionVerdict{Reason: "cc_session_id 가 비었다 — CLAUDE_CODE_SESSION_ID 를 못 읽었다면 " +
			"그 탐지가 깨진 것이다(fd doctor 가 그 축을 잰다). 지어내지 않는다"}
	default:
		return SessionVerdict{OK: true, Reason: "3중키와 프로젝트가 전부 있다"}
	}
}
```

`internal/service/session.go` 의 import 블록에 `"github.com/kweiza/flightdeck/internal/judge"` 를
**더해야 한다** — 실측으로 지금 없다. `model` 위에 알파벳 순으로 넣는다:

```go
	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)
```

- [ ] **Step 4: 시험이 통과하는 것을 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/service/ -run TestJudgeOpenSession -v 2>&1 | tail -20`
Expected: 두 시험 모두 PASS

- [ ] **Step 5: 패키지 전체 시험**

Run: `cd plugins/flightdeck/server && go test ./internal/service/ && gofmt -l internal/service/`
Expected: `ok`, 출력 없음

- [ ] **Step 6: 커밋**

```bash
cd plugins/flightdeck/server
git add internal/service/session.go internal/service/session_test.go
git commit -m "fix(flightdeck): 세션 등록이 Windows 경로의 원인을 짚는다 — '절대경로가 아니다'는 사실이되 원인이 아니었다

Linux 서버의 filepath.IsAbs 는 C:\\repo 를 절대경로로 안 본다. 그래서 Windows 에서
띄운 세션은 '절대경로가 아니다'라는 사유를 받았는데, 사용자는 자기가 준 것이
절대경로라고 알고 있으므로 그 사유로는 고칠 수 없었다.

좌표계 절을 IsAbs 앞에 세웠다. 순서가 이 수정의 전부다 — 뒤에 두면 IsAbs 가 먼저
잡아서 원인이 다시 가려진다. 그 이유를 코드 주석에 적었다.

기존 축(빈 값·상대경로)의 사유는 그대로다. 회귀 시험이 그것을 지킨다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 4: `add` 관문 — 가장 큰 입구를 막는다

**Files:**
- Modify: `internal/service/pick.go` (`AddItem`, 573~599행 부근 — `After` 검증 뒤 `it := model.Item{` 앞에 삽입)
- Test: `internal/service/pick_test.go` (파일 끝에 추가)

**Interfaces:**
- Consumes: `judge.JudgePathCoordinate` (Task 1)
- Produces: 없음 (`AddItem` 시그니처 불변)

**왜 이 자리가 중요한가:** `item.paths` 는 321행으로 가장 큰 경로 컬럼인데 **검증이 하나도
없다**. 세션 worktree 와 달리 클라이언트 OS 라는 관문조차 없어서, 사람이 무엇을 붙여넣든
들어간다. 통과시키면 그 항목의 겹침 축이 영영 죽는다.

> **주의:** `internal/service/pick.go` 는 여러 세션이 동시에 만지는 파일이다. **삽입만 한다.**
> 기존 검증 절(`ValidateItemID`·제목·본문·`After`)을 지우거나 순서를 바꾸지 않는다.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/service/pick_test.go` **파일 끝**에 붙인다. 거절을 기대하는 시험이므로 헬퍼
`addItem` 을 쓰지 않는다 — 그 헬퍼는 실패하면 `t.Fatalf` 한다. `s.AddItem` 을 직접 부른다:

```go
// item.paths 는 가장 큰 경로 컬럼인데 검증이 하나도 없었다.
// 여기를 통과시키면 그 항목의 겹침 축이 영영 죽는다 — 조용히.
func TestAddItemRejectsNonSlashCoordinatePaths(t *testing.T) {
	s, _ := newSvc(t)
	cases := []struct {
		name  string
		paths []string
		want  string
	}{
		{"드라이브 절대경로", []string{`C:\repo\x.go`}, "드라이브 절대경로"},
		{"UNC", []string{`\\host\share\x.go`}, "UNC"},
		{"상대 백슬래시", []string{`internal\api\x.go`}, "백슬래시"},
		{"정상 경로 뒤에 섞여 있어도", []string{"internal/api/x.go", `b\c.go`}, "백슬래시"},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := s.AddItem(ctx(), AddItemInput{
				Project: "p", ID: fmt.Sprintf("fd-x%d", i), Title: "t", Body: "b",
				Paths: c.paths,
			})
			if err == nil {
				t.Fatal("잘못된 좌표계의 경로를 통과시켰다")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("오류 %q 가 원인(%q)을 안 짚는다", err.Error(), c.want)
			}
		})
	}
}

// 몇 번째 경로가 문제인지 말해야 한다 — 목록이 길면 "어딘가 틀렸다"로는 못 고친다.
func TestAddItemSaysWhichPathIsWrong(t *testing.T) {
	s, _ := newSvc(t)
	_, err := s.AddItem(ctx(), AddItemInput{
		Project: "p", ID: "fd-which", Title: "t", Body: "b",
		Paths: []string{"a/b.go", "c/d.go", `e\f.go`},
	})
	if err == nil {
		t.Fatal("통과시켰다")
	}
	if !strings.Contains(err.Error(), "2번째") {
		t.Errorf("몇 번째 경로인지 안 말한다: %s", err.Error())
	}
}

// 정상 경로는 그대로 들어간다.
func TestAddItemAcceptsSlashCoordinatePaths(t *testing.T) {
	s, _ := newSvc(t)
	it, err := s.AddItem(ctx(), AddItemInput{
		Project: "p", ID: "fd-ok", Title: "t", Body: "b",
		Paths: []string{"internal/api/x.go", "Makefile", "tools/"},
	})
	if err != nil {
		t.Fatalf("정상 경로를 거절했다: %v", err)
	}
	if len(it.Paths) != 3 {
		t.Fatalf("경로 %d개, want 3개", len(it.Paths))
	}
}
```

`internal/service/pick_test.go` 는 `fmt`·`strings` 를 이미 임포트하고 있다 — 손댈 필요 없다.

- [ ] **Step 2: 시험이 실패하는 것을 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/service/ -run TestAddItem -v 2>&1 | tail -25`
Expected: 거절 시험 둘이 FAIL — `err == nil` ("잘못된 좌표계의 경로를 통과시켰다")

- [ ] **Step 3: 관문을 삽입한다**

`internal/service/pick.go` 의 `AddItem` 에서 `After` 검증 루프가 끝난 **직후**, `it := model.Item{`
**직전**에 삽입한다:

```go
	// ★ item.paths 는 가장 큰 경로 컬럼인데 여기 오기 전까지 검증이 하나도 없었다.
	// 세션 worktree 와 달리 클라이언트 OS 라는 관문조차 없어서 사람이 무엇을 붙여넣든
	// 들어온다. 통과시키면 그 항목의 겹침 축이 **조용히** 죽는다 — 오류가 아니라
	// '겹침 없음'이라 정상 응답과 구분되지 않는다.
	for i, p := range in.Paths {
		if v := judge.JudgePathCoordinate(p); !v.OK {
			return model.Item{}, &RefusedError{What: "add",
				Reason: fmt.Sprintf("%d번째 경로: %s", i, v.Reason),
				Guidance: "경로는 저장소 상대(internal/api/x.go) 또는 POSIX 절대경로여야 한다 — " +
					"좌표계가 다르면 이 항목의 겹침 축이 조용히 죽는다."}
		}
	}
```

import 은 손댈 필요가 없다 — `internal/service/pick.go` 는 `judge` 와 `fmt` 를 이미
임포트하고 있다(실측).

- [ ] **Step 4: 시험이 통과하는 것을 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/service/ -run TestAddItem -v 2>&1 | tail -25`
Expected: 세 시험 모두 PASS

- [ ] **Step 5: 패키지 전체 시험**

Run: `cd plugins/flightdeck/server && go test ./internal/service/ && gofmt -l internal/service/`
Expected: `ok`, 출력 없음

- [ ] **Step 6: 커밋**

```bash
cd plugins/flightdeck/server
git add internal/service/pick.go internal/service/pick_test.go
git commit -m "fix(flightdeck): add 가 경로 좌표계를 검사한다 — 가장 큰 입구가 가장 무방비였다

item.paths 는 경로 컬럼 중 가장 크다(실측 321행). 그런데 검증이 하나도 없어서 임의
문자열이 그대로 들어갔다. 세션 worktree 와 달리 클라이언트 OS 라는 관문조차 없으므로,
Windows 클라이언트를 띄우지 않아도 도달한다 — 사람이 Windows 쪽 문서나 IDE 에서
경로를 복사해 오는 것으로 충분하다.

통과시키면 그 항목의 겹침 축이 조용히 죽는다. 오류가 아니라 '겹침 없음'이라
정상 응답과 구분되지 않는다.

몇 번째 경로가 문제인지 함께 낸다 — 목록이 길면 '어딘가 틀렸다'로는 못 고친다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 5: 발자국 — 버리되 관측 가능하게

**Files:**
- Modify: `internal/service/service.go` (파일 끝에 타입 별칭 + 위임 — 아래 **계층** 절을 읽어라)
- Modify: `internal/api/handlers_session.go` (`NormalizeFootprints` 210~221행 부근, `handleFootprints` 250~264행 부근)
- Modify: `internal/service/session.go` (`Beat` 307~330행 부근)
- Test: `internal/api/pure_test.go` — **331행의 기존 `TestNormalizeFootprints` 를 고쳐야 한다.** 시그니처를 바꾸므로 그 시험이 컴파일부터 깨진다. 새 시험은 그 뒤에 더한다
- Test: `internal/service/session_test.go` (파일 끝에 추가)

**Interfaces:**
- Consumes: `judge.FilterPathCoordinate`, `judge.RejectedPath` (Task 1)
- Produces:
  - `service.RejectedPath` — `judge.RejectedPath` 의 **타입 별칭**(`=`). 별칭이므로 같은 타입이다
  - `service.FilterFootprintPaths(paths []string) (kept []string, rejected []RejectedPath)`
  - `api.NormalizeFootprints(worktree string, paths []string) ([]string, []service.RejectedPath)` — **시그니처가 바뀐다.** 호출처는 `handleFootprints` 하나다(실측)

**계층 — 이 태스크에서 반드시 지킬 것:**

`internal/api` 는 `internal/judge` 를 **한 번도 임포트하지 않는다**(실측 0건). 그것이 이 저장소의
규율이다. `api` 패키지 주석은 자기 계층 판정(`JudgeAuth`·`JudgeIdempotencyKey` 등)을 자기
패키지에 두고, 도메인 순수 함수는 `service` 를 거쳐 부른다 — `NormalizeFootprints` 가 이미
`service.RelPath`·`service.UnionPaths` 를 그렇게 쓰고 있다.

그래서 `api` 에서 `judge` 를 직접 부르지 않는다. `service` 에 별칭과 위임을 두고 그것을 쓴다.
**`api/handlers_session.go` 에 `judge` 임포트를 추가하지 마라.**

**왜 여기만 통과시키나:** 훅이 자동으로 보내는 경로다. 400 으로 죽이면 세션 생존 신호가
끊기고, 세션이 보드에서 사라지는 것으로 나타나 그 인과를 아무도 못 짚는다 — 침묵보다 나쁘다.
그래서 **버리되 버린 사실을 남긴다.**

두 표면의 남기는 자리가 다르다(스펙 §4.2):
- `handleFootprints` → 응답에 싣는다 (명시적 호출, 사람이 읽는다)
- `Beat` → event 원장에 남긴다 (`Beat` 시그니처를 바꾸면 호출처 5곳을 건드려야 하고 그중
  `mcpsrv.go:395` 는 다른 세션이 만지는 중이다. 그리고 이 표면의 응답은 훅이 삼킨다)

- [ ] **Step 1: 실패하는 시험을 쓴다 — api 쪽**

**먼저 기존 시험을 고친다.** `internal/api/pure_test.go:331` 의 `TestNormalizeFootprints` 는
반환값 하나를 받으므로 시그니처를 바꾸면 컴파일이 깨진다. 두 자리를 두 값 받게 바꾼다 —
**단정 내용은 그대로 둔다**(그 시험이 지키는 것은 좌표계가 아니라 접두 절단 결함이다):

```go
func TestNormalizeFootprints(t *testing.T) {
	got, _ := NormalizeFootprints("/repo", []string{
		"/repo/internal/api/api.go",
		"/repo/internal/api/api.go", // 중복은 접힌다
		"internal/api/sse.go",       // 이미 상대인 것은 그대로
		"",                          // 빈 것은 버린다
		"/other/x.go",               // 저장소 밖은 원본을 둔다
	})
	want := []string{"/other/x.go", "internal/api/api.go", "internal/api/sse.go"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("정규화 결과가 다르다: %v, 기대 %v", got, want)
	}
	// ★ 표 밖: 접두 문자열로 자르면 /repo-old/x.go 가 "-old/x.go" 로 둔갑한다.
	if got, _ := NormalizeFootprints("/repo", []string{"/repo-old/x.go"}); got[0] != "/repo-old/x.go" {
		t.Fatalf("저장소 밖 경로가 잘렸다: %q", got[0])
	}
}
```

**그 뒤에 새 시험을 붙인다:**

```go
// 발자국은 거절하지 않고 버린다 — 훅을 400 으로 죽이면 세션 생존 신호가 끊긴다.
// 대신 버린 것을 돌려줘야 한다. 안 그러면 경로가 조용히 사라진 것과 같다.
func TestNormalizeFootprintsKeepsGoodDropsBad(t *testing.T) {
	kept, rejected := NormalizeFootprints("/repo", []string{
		"/repo/internal/api/x.go",
		`C:\other\y.go`,
		"/repo/Makefile",
		`z\w.go`,
	})

	want := []string{"Makefile", "internal/api/x.go"} // UnionPaths 가 정렬한다
	if fmt.Sprint(kept) != fmt.Sprint(want) {
		t.Fatalf("kept = %v, want %v", kept, want)
	}

	if len(rejected) != 2 {
		t.Fatalf("rejected %d건, want 2건: %+v", len(rejected), rejected)
	}
	if rejected[0].Path != `C:\other\y.go` {
		t.Errorf("거절이 원본 경로를 안 나른다: %+v", rejected[0])
	}
	if rejected[0].Reason == "" {
		t.Error("거절에 사유가 없다")
	}
}

func TestNormalizeFootprintsAllGoodHasNoRejected(t *testing.T) {
	kept, rejected := NormalizeFootprints("/repo", []string{"/repo/a.go", "/repo/b.go"})
	if len(kept) != 2 {
		t.Fatalf("kept = %v, want 2건", kept)
	}
	if len(rejected) != 0 {
		t.Fatalf("정상 입력에 거절이 생겼다: %+v", rejected)
	}
}
```

`internal/api/pure_test.go` 는 `fmt` 를 이미 임포트하고 있다.

- [ ] **Step 2: 시험이 실패하는 것을 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/api/ -run TestNormalizeFootprints 2>&1 | head -15`
Expected: 컴파일 실패 — `NormalizeFootprints` 가 값 하나만 돌려주므로 `kept, rejected :=` 가 안 맞는다

- [ ] **Step 3: `service` 에 별칭과 위임을 두고, 그다음 api 를 고친다**

**3-a.** `internal/service/service.go` **파일 끝**에 붙인다:

```go
// RejectedPath 는 좌표계 관문이 버린 경로다. judge 타입의 **별칭**이다(복제가 아니다).
//
// ★ 별칭인 이유는 계층이다. internal/api 는 internal/judge 를 임포트하지 않는다 —
// HTTP 계층은 자기 판정만 자기 패키지에 두고 도메인 판정은 이 패키지를 거쳐 부른다
// (RelPath·UnionPaths 가 같은 자리에 있는 이유이기도 하다).
// 새 타입을 만들면 같은 값이 두 이름을 갖게 되므로 별칭으로 둔다.
type RejectedPath = judge.RejectedPath

// FilterFootprintPaths 는 좌표계 관문을 발자국 목록에 적용한다. 순수 함수다.
//
// 판정 자체는 judge 에 있다 — 여기서 흉내 내지 않는다. 이 함수의 존재 이유는
// 계층뿐이다(위 RejectedPath 주석).
func FilterFootprintPaths(paths []string) (kept []string, rejected []RejectedPath) {
	return judge.FilterPathCoordinate(paths)
}
```

import 은 손댈 필요가 없다 — `internal/service/service.go` 는 `judge` 를 이미 임포트하고
있다(실측).

**3-b.** `internal/api/handlers_session.go` 의 `NormalizeFootprints` 를 통째로 바꾼다:

```go
// NormalizeFootprints 는 훅이 준 절대경로를 저장소 좌표계로 옮기고, 좌표계가 다른 것을 가른다.
// 순수 함수다.
//
// ★ 좌표계를 안 맞추면 겹침 축이 **조용히** 죽는다. 훅은 절대경로를 주고
// git 은 저장소 상대를 주므로, 둘을 그대로 두면 같은 파일이 서로 다른 문자열이 되어
// 아무와도 안 겹친다 — 그리고 그 결과는 "겹침 없음"이라는 정상 응답과 구분되지 않는다.
//
// ★ 버린 것을 **함께 돌려준다.** 이 표면은 거절하지 않는다(훅을 400 으로 죽이면 세션
// 생존 신호가 끊긴다). 그러면 버린 사실을 호출부가 말할 수 있어야 하고, 못 말하면
// 경로가 조용히 사라진 것과 같아진다 — 이 함수가 없애려는 바로 그 침묵이다.
func NormalizeFootprints(worktree string, paths []string) ([]string, []service.RejectedPath) {
	kept, rejected := service.FilterFootprintPaths(paths)
	rels := make([]string, 0, len(kept))
	for _, p := range kept {
		rels = append(rels, service.RelPath(worktree, p))
	}
	return service.UnionPaths(rels), rejected
}
```

`handleFootprints` 의 250행 부근을 바꾼다:

```go
	rels, rejected := NormalizeFootprints(sess.Worktree, req.Paths)
```

그리고 같은 함수의 응답(261~264행 부근)을 바꾼다:

```go
	res := map[string]any{
		"session_id": req.SessionID, "origin": string(origin),
		"count": len(rels), "paths": rels,
	}
	// 버린 것이 있을 때만 싣는다 — 빈 배열을 늘 실으면 정상 응답에 잡음이 낀다.
	if len(rejected) > 0 {
		res["rejected"] = rejected
	}
	s.writeJSON(w, r, http.StatusOK, res)
```

`internal/api/handlers_session.go` 의 import 는 **손대지 않는다.** `service` 를 이미 임포트하고
있고, `judge` 는 추가하지 않는다(위 **계층** 절).

- [ ] **Step 4: api 시험이 통과하는 것을 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/api/ -run TestNormalizeFootprints -v 2>&1 | tail -15`
Expected: 두 시험 PASS

- [ ] **Step 5: `Beat` 쪽 실패하는 시험을 쓴다**

`internal/service/session_test.go` **파일 끝**에 붙인다:

기존 `TestBeatNormalizesAbsolutePathsToRepoRelative`(118행)와 같은 관례를 쓴다:

```go
// Beat 는 훅이 부른다. 좌표계가 틀린 경로가 섞여 와도 신호 자체는 살아야 한다 —
// 여기서 오류를 내면 세션이 보드에서 사라지고 그 인과를 아무도 못 짚는다.
// 좋은 경로는 그대로 들어가고, 나쁜 것만 조용히가 아니라 **원장에 건수를 남기고** 빠진다.
func TestBeatDropsBadCoordinatePathsButKeepsSignal(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	sess := openSession(t, s, "p", repo, repo, "cc-beat-coord", "좌표계")

	good := filepath.Join(repo, "tools", "x.sh")
	if err := s.Beat(ctx(), sess.Session.ID, model.SignalTool, []string{
		good,
		`C:\other\y.go`,
		`z\w.go`,
	}); err != nil {
		t.Fatalf("좌표계가 틀린 경로 때문에 신호가 죽었다: %v", err)
	}

	// 신호는 살아 있다.
	sig, err := st.Signals(ctx(), sess.Session.ID)
	if err != nil {
		t.Fatalf("신호 조회 실패: %v", err)
	}
	if _, ok := sig[model.SignalTool]; !ok {
		t.Fatalf("tool 신호가 안 남았다 — 나쁜 경로 하나가 신호 전체를 죽였다: %v", sig)
	}
}
```

`internal/service/session_test.go` 는 `filepath`·`model` 을 이미 임포트하고 있다.

- [ ] **Step 6: 시험이 실패하는지 확인한다**

Run: `cd plugins/flightdeck/server && go test ./internal/service/ -run TestBeatDrops -v 2>&1 | tail -15`
Expected: 이 시험은 **변경 전에도 통과할 수 있다** — 지금 `Beat` 는 `RelPath` 가 낸 값을
그대로 `Touch` 하므로 오류를 안 낸다. 그렇다면 이 시험은 회귀 방지용이고, Step 7 이 바꾸는
것은 "버린 사실이 원장에 남는가"다. 통과하면 그대로 두고 Step 7 로 가라.

- [ ] **Step 7: `Beat` 에 필터를 넣고 버린 건수를 원장에 남긴다**

`internal/service/session.go` 의 `Beat` 에서 `s.st.Tx(...)` 클로저 안을 고친다.
**`t.LogEvent` 의 payload 에 한 항목을 더하고, 루프의 대상을 `kept` 로 바꾼다:**

```go
	now := s.now()
	// ★ 좌표계가 다른 경로는 버린다 — 여기서 거절하면 훅이 죽고 세션 생존 신호가 끊긴다.
	// 그것이 침묵보다 나쁘다. 대신 버린 건수를 event 원장에 남긴다(응답은 훅이 삼킨다).
	kept, rejected := judge.FilterPathCoordinate(paths)
	err := s.st.Tx(ctx, func(t *store.Tx) error {
		sess, err := t.GetSession(sessionID)
		if err != nil {
			return err
		}
		t.LogEvent("session.beat", sess.Project, sessionID, map[string]any{
			"kind": string(kind), "count": len(kept), "rejected": len(rejected),
		})
		if err := t.Beat(sessionID, kind, now); err != nil {
			return err
		}
		for _, p := range kept {
			rel := RelPath(sess.Worktree, p)
			if rel == "" {
				continue
			}
			// origin=observed. "선언했으나 안 건드림"과 "선언 없이 건드림"을 뭉개지 않는다.
			if err := t.Touch(sessionID, rel, model.OriginObserved, now); err != nil {
				return err
			}
		}
		return nil
	})
	// 사유는 로그로 낸다 — 원장에는 건수만 남고, 무엇이 왜 버려졌는지는 여기서만 볼 수 있다.
	if len(rejected) > 0 {
		s.log.WarnContext(ctx, "발자국 경로를 좌표계 관문이 버렸다",
			"session_id", clip(sessionID, 64), "dropped", len(rejected),
			"first_reason", rejected[0].Reason)
	}
```

`internal/service/session.go` 는 Task 3 에서 이미 `judge` 를 임포트했다.

- [ ] **Step 8: 시험 전체를 돌린다**

Run: `cd plugins/flightdeck/server && go test ./... 2>&1 | grep -v "^ok" | head -20`
Expected: 출력 없음 (모든 패키지 통과)

- [ ] **Step 9: 포맷과 vet**

Run: `cd plugins/flightdeck/server && gofmt -l . && go vet ./...`
Expected: 둘 다 출력 없음

- [ ] **Step 10: 커밋**

```bash
cd plugins/flightdeck/server
git add internal/api/handlers_session.go internal/api/pure_test.go \
        internal/service/service.go internal/service/session.go internal/service/session_test.go
git commit -m "fix(flightdeck): 발자국은 좌표계가 틀린 경로를 버리되 버린 사실을 남긴다

입구 셋 중 여기만 통과시킨다. 훅이 자동으로 보내는 경로라 400 으로 죽이면 세션
생존 신호가 끊기고, 세션이 보드에서 사라지는 것으로 나타나 그 인과를 아무도 못
짚는다 — 침묵보다 나쁘다.

버린 사실을 남기는 자리가 두 표면에서 갈린다. footprints 는 명시적 호출이라 응답에
싣고, signals(Beat)는 훅이 응답을 삼키므로 event 원장에 건수를 남기고 사유는 로그로
낸다. Beat 의 시그니처를 안 바꾼 것은 호출처가 5곳이고 그중 mcpsrv.go 는 다른
세션이 만지는 중이기 때문이다.

버린 것을 안 돌려주면 경로가 조용히 사라진 것과 같아진다 — 이 항목이 없애려는
바로 그 침묵이다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## 완료 확인

전부 끝나면 다음이 참이어야 한다:

```bash
cd plugins/flightdeck/server
go test ./... && gofmt -l . && go vet ./...
git log --oneline main..HEAD   # 커밋 5개 + 스펙 커밋 1개
```

- `store/schema.sql` 이 diff 에 **없다**(`git diff main --stat` 로 확인)
- `internal/judge/paths.go` 의 `components`·`PathsOverlap`·`pathRelated` 가 **안 바뀌었다**
- `TestComponentsDeliberatelyDoesNotSplitBackslash` 가 초록이다
