# 겹침 줄에 변경 규모를 싣는다 — 구현 계획

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `board`·`pick` 응답 꼬리의 겹침 줄이 상대 세션의 경로별 증감(`+47/-1`)을 함께 내고 규모가 큰 순으로 서게 한다.

**Architecture:** git 파생을 이미 도는 경로(`service.sessionCardsAndRoots`)에서만 잰다. `ChangedPaths` 의 `--name-only` 를 `--numstat` 으로 바꾸면 커밋 구간은 같은 프로세스라 공짜고, 미커밋 구간에만 세션당 `git diff --numstat -z HEAD` 하나가 는다. 두 구간은 서로소라 합산한다. 규모는 `map[string]model.LineDelta` 로 흐르고 **키가 없는 것은 0 이 아니라 "못 읽었다"** 다. 턴마다 도는 처방 경로(`service.Prescriptions`)는 git 을 안 도는 §6 판정이 그대로 살아서 **손대지 않는다**.

**Tech Stack:** Go 1.x · `os/exec` 로 부르는 실제 git · SQLite · 표준 `testing`

**Spec:** `docs/superpowers/specs/2026-08-11-overlap-change-size-design.md`

## Global Constraints

- **작업 디렉토리는 워크트리다.** 모든 명령은 `/home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-overlap-prescription-ignores-change-size` 안에서 돈다. 원본 저장소로 `cd` 하지 마라.
- **Go 모듈 루트는 `plugins/flightdeck/server` 다.** `go` 명령은 전부 그 안에서 돈다 — **모듈 밖에서 돌린 `gofmt` 는 빈 디렉토리를 검사하고 조용히 통과한다.**
- **랜딩 관문 셋** — `gofmt -l .`(무출력) · `go vet ./...` · `go test -count=1 ./...`. `go build` 로 대신하지 마라. **`go build` 는 `_test.go` 를 건너뛰어 시험 코드에 대해 관문이 열려 있다.**
- **주석과 시험 이름과 커밋 메시지는 한국어다.** 이 저장소의 모든 판단이 한국어로 적혀 있다.
- **`internal/judge` 는 순수하다** — I/O 도 상태도 시계도 없다. 판정은 시험이 **직접 부르는** 함수에 둔다(DESIGN §12).
- **"못 읽었다"를 0 으로 접지 마라.** 이 계획 전체의 논지가 그 한 줄이다. 맵에서 키 부재가 "못 읽었다"이고, 화면에서는 `(규모?)` 다.
- **`internal/judge/prescribe.go` 는 한 글자도 안 바꾼다.** `internal/service/prescribe.go` 는 **주석 한 자리만** 는다(Task 6).
- **`DESIGN.md` 에서 만지는 자리는 `477행 뒤 순삽입` 하나다.** 469행(소절 제목) · 471-474행(번호 목록) · 476-477행은 안 건드린다. 이 범위는 살아 있는 세션들에 `note(kind='ask')` 로 선언돼 있다(판단 `01KZPFZ58NEPD6DX4A7JH2ZB6E`).
- 커밋 메시지 끝에 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>` 를 붙인다.

## File Structure

| 파일 | 책임 | 태스크 |
|---|---|---|
| `internal/model/types.go` | `LineDelta` 값 · `SessionView.PathDelta` 필드 | 1, 3 |
| `internal/gitreader/parse.go` | `parseNumstatZ` — numstat 출력을 가르는 **순수 함수** | 1 |
| `internal/gitreader/gitreader.go` | `ChangedPaths`(numstat 으로 교체) · `UncommittedDelta`(신설) | 2 |
| `internal/service/service.go` | `GitReader` 인터페이스 · `MergeDelta` 순수 함수 | 2, 3 |
| `internal/service/board.go` | 두 구간의 규모를 카드에 합산 · 열화 사유 | 3 |
| `internal/judge/eligible.go` | `Overlap.TheirDelta` · `OverlapsWithLive` · `SortOverlapsBySize` | 4 |
| `internal/mcpsrv/render.go` | 꼬리 문구 · `renderDelta` | 5 |
| `internal/service/prescribe.go` | 주석 한 자리 — 이 축이 왜 처방으로 안 왔나 | 6 |
| `plugins/flightdeck/DESIGN.md` | §5 에 문단 하나 순삽입 | 6 |

**태스크 사이 계약.** Task 2 는 `ChangedPaths` 의 서명을 바꾸면서 호출부가 새 반환값을 `_` 로 버린다 — 그것을 실제로 쓰는 것은 Task 3 이다. 두 태스크 사이에서도 관문 셋은 전부 초록이어야 한다.

---

### Task 1: `model.LineDelta` 와 numstat 파서

**Files:**
- Modify: `plugins/flightdeck/server/internal/model/types.go` (`SessionView` 정의 앞, 353행 부근)
- Modify: `plugins/flightdeck/server/internal/gitreader/parse.go` (`parseNameOnlyZ` 뒤, 303행 부근)
- Test: `plugins/flightdeck/server/internal/gitreader/parse_test.go`

**Interfaces:**
- Produces: `model.LineDelta{Added, Removed int}` · `gitreader.parseNumstatZ(out []byte) (paths []string, delta map[string]model.LineDelta, skipped []string)`
- Consumes: 없다. 이 태스크는 순수 함수 둘만 만든다.

**배경 — 이 형태는 실물로 관측한 것이다(2026-08-11).**

```
$ git diff --numstat -z --no-renames HEAD~3 HEAD --
8\t0\t.gitignore\0 1\t1\t…/plugin.json\0 12\t4\t…/cmds.go\0

$ git diff --numstat -z --no-renames HEAD --     # 이진 파일 + 미추적 파일이 있는 트리
-\t-\tb.bin\0 1\t0\tt.txt\0

$ git status --porcelain -z
 M b.bin\0 M t.txt\0?? untracked.txt\0
```

레코드는 `증가 TAB 감소 TAB 경로 NUL` 이다. 선행 NUL 도 헤더도 없다. **이진 파일은 `-`/`-` 이고 미추적 파일은 numstat 에 아예 안 나온다.**

- [ ] **Step 1: `model.LineDelta` 를 넣는다**

`internal/model/types.go` 의 `type SessionView struct {`(353행) **바로 앞**에 넣는다.

```go
// LineDelta 는 경로 하나의 증감이다.
//
// ★ **이것을 나르는 맵에서 키가 없는 것은 0 이 아니라 "못 읽었다"** 다. 둘을 뭉개면
// "안 만졌다"와 "못 쟀다"가 같은 화면이 되고, 그것이 이 축이 없애려는 오탐의 거울상이다.
// 바로 아래 SessionView.Signals 가 같은 관용을 쓴다("없는 종류는 키가 없다").
//
// 규모를 못 재는 자리가 넷 있고 넷 다 키 부재로 접힌다 — 이진 파일(numstat 이 `-`/`-` 를 낸다) ·
// 미추적 파일(numstat 에 아예 안 나온다) · footprint 에만 있는 경로 · git 파생 실패.
type LineDelta struct {
	Added   int
	Removed int
}
```

- [ ] **Step 2: 실패하는 시험을 쓴다**

`internal/gitreader/parse_test.go` 에 붙인다(파일이 없으면 만든다). 임포트는
`package gitreader` · `reflect` · `testing` · `github.com/kweiza/flightdeck/internal/model` 이다.

```go
func TestParseNumstatZ(t *testing.T) {
	cases := []struct {
		name      string
		out       string
		wantPaths []string
		wantDelta map[string]model.LineDelta
		wantSkip  int
	}{
		{
			name:      "정상 레코드 셋",
			out:       "8\t0\t.gitignore\x001\t1\tplugin.json\x0012\t4\tcmds.go\x00",
			wantPaths: []string{".gitignore", "plugin.json", "cmds.go"},
			wantDelta: map[string]model.LineDelta{
				".gitignore":  {Added: 8, Removed: 0},
				"plugin.json": {Added: 1, Removed: 1},
				"cmds.go":     {Added: 12, Removed: 4},
			},
		},
		{
			// ★ 이진 파일은 **경로로는 남고 규모로는 안 남는다.** 그 파일이 바뀐 것은 사실이라
			// 겹침 축에서 빠지면 안 되고, 규모는 못 잰 것이라 0 으로 세면 안 된다.
			name:      "이진 파일은 경로만 남고 규모는 키가 없다",
			out:       "-\t-\tb.bin\x001\t0\tt.txt\x00",
			wantPaths: []string{"b.bin", "t.txt"},
			wantDelta: map[string]model.LineDelta{"t.txt": {Added: 1, Removed: 0}},
		},
		{
			name:      "빈 출력",
			out:       "",
			wantPaths: nil,
			wantDelta: map[string]model.LineDelta{},
		},
		{
			name:      "필드가 모자라면 그 레코드만 버린다",
			out:       "5\tx.go\x003\t1\ty.go\x00",
			wantPaths: []string{"y.go"},
			wantDelta: map[string]model.LineDelta{"y.go": {Added: 3, Removed: 1}},
			wantSkip:  1,
		},
		{
			name:      "수가 아닌 값이면 그 레코드만 버린다",
			out:       "a\t0\tx.go\x003\t1\ty.go\x00",
			wantPaths: []string{"y.go"},
			wantDelta: map[string]model.LineDelta{"y.go": {Added: 3, Removed: 1}},
			wantSkip:  1,
		},
		{
			// 경로에 공백·유니코드가 있어도 -z 라 구분자가 안 흔들린다.
			name:      "공백과 유니코드 경로",
			out:       "2\t0\tdocs/설계 노트.md\x00",
			wantPaths: []string{"docs/설계 노트.md"},
			wantDelta: map[string]model.LineDelta{"docs/설계 노트.md": {Added: 2, Removed: 0}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			paths, delta, skipped := parseNumstatZ([]byte(tc.out))
			if !reflect.DeepEqual(paths, tc.wantPaths) {
				t.Errorf("경로: %q 를 기대했는데 %q 다", tc.wantPaths, paths)
			}
			if len(delta) != len(tc.wantDelta) {
				t.Errorf("규모 %d건을 기대했는데 %d건이다: %v", len(tc.wantDelta), len(delta), delta)
			}
			for p, want := range tc.wantDelta {
				got, ok := delta[p]
				if !ok {
					t.Errorf("%q 의 규모 키가 없다", p)
					continue
				}
				if got != want {
					t.Errorf("%q: %+v 를 기대했는데 %+v 다", p, want, got)
				}
			}
			if len(skipped) != tc.wantSkip {
				t.Errorf("버린 레코드 %d건을 기대했는데 %d건이다: %v", tc.wantSkip, len(skipped), skipped)
			}
		})
	}
}

// ★ 이 시험이 이 계획의 논지를 지킨다. 이진 파일의 규모를 0 으로 접으면 여기가 빨개진다.
func TestParseNumstatZNeverReportsUnknownAsZero(t *testing.T) {
	_, delta, _ := parseNumstatZ([]byte("-\t-\tb.bin\x00"))
	if d, ok := delta["b.bin"]; ok {
		t.Fatalf("이진 파일의 규모 키가 생겼다(%+v) — 못 읽은 것은 키가 없어야 한다. "+
			"0 으로 접으면 화면이 '안 만졌다'라고 말하게 된다", d)
	}
}
```

- [ ] **Step 3: 빨간불을 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/gitreader/ -run 'TestParseNumstatZ' -v
```
Expected: FAIL — `undefined: parseNumstatZ`

- [ ] **Step 4: 파서를 쓴다**

`internal/gitreader/parse.go` 의 `parseNameOnlyZ`(294-303행) **바로 뒤**에 넣는다.

```go
// parseNumstatZ 는 `git diff --numstat -z --no-renames …` 출력을 가른다. 순수 함수다.
//
// 레코드 하나는 `증가 TAB 감소 TAB 경로 NUL` 이다 — 선행 NUL 도 헤더도 없다
// (2026-08-11 실물 관측. 스펙 §2.1 에 출력 그대로 있다).
//
// ★ **경로와 규모를 따로 낸다.** 이진 파일은 `-\t-\t경로` 로 오는데, 그 파일이 **바뀐 것은
// 사실**이라 경로 목록에는 남아야 하고(빠지면 겹침 축에서 통째로 사라진다) 규모는 못 잰
// 것이라 맵에 키가 없어야 한다. 하나의 반환값으로는 그 둘을 못 가른다.
//
// ★ **깨진 레코드 하나가 축 전체를 끄지 않는다.** 그 레코드만 버리고 사유를 skipped 로
// 올린다 — 호출자가 WARN 을 남긴다. 조용히 버리면 규모가 왜 비었는지 아무 데도 안 남고,
// 통째로 오류를 내면 멀쩡한 나머지 경로까지 잃는다. service.emittedKeys 가 payload 해석
// 실패에 대해 쓰는 규율과 같다.
//
// ★ **`--no-renames` 가 붙어 있다는 전제 위에 서 있다.** 그것이 없으면 numstat 이 이름
// 변경을 `증가 TAB 감소 TAB NUL 원본 NUL 목적지 NUL` 3필드로 내고, 이 파서는 그것을
// 조용히 어긋나게 읽는다. 호출부 둘 다 그 플래그를 준다 —
// TestParseNumstatZAssumesNoRenames 가 그 전제를 시험으로 잠근다.
func parseNumstatZ(out []byte) (paths []string, delta map[string]model.LineDelta, skipped []string) {
	delta = map[string]model.LineDelta{}
	for _, rec := range strings.Split(string(out), "\x00") {
		if rec == "" {
			continue // 마지막 NUL 뒤의 빈 토큰
		}
		f := strings.SplitN(rec, "\t", 3)
		if len(f) != 3 || f[2] == "" {
			skipped = append(skipped, fmt.Sprintf("필드가 3개가 아니다: %q", sanitizeExcerpt([]byte(rec), 80)))
			continue
		}
		path := f[2]
		// 이진 파일은 `-`/`-` 다. 경로는 남기고 규모만 비운다.
		if f[0] == "-" && f[1] == "-" {
			paths = append(paths, path)
			continue
		}
		added, aerr := strconv.Atoi(f[0])
		removed, rerr := strconv.Atoi(f[1])
		if aerr != nil || rerr != nil {
			skipped = append(skipped, fmt.Sprintf("증감이 수가 아니다: %q", sanitizeExcerpt([]byte(rec), 80)))
			continue
		}
		paths = append(paths, path)
		delta[path] = model.LineDelta{Added: added, Removed: removed}
	}
	return paths, delta, skipped
}
```

`parse.go` 는 `fmt`·`strconv`·`strings`·`model` 을 이미 임포트하고 있다 — 새 임포트가 없다.

- [ ] **Step 5: `--no-renames` 전제를 시험으로 잠근다**

`parse_test.go` 에 붙인다.

```go
// ★ 이 시험은 **파서가 무엇을 전제하는지**를 잠근다. 호출부에서 --no-renames 를 떼면
// numstat 이 rename 을 3필드(NUL 로 나뉜 원본·목적지)로 내는데, 그 형태가 이 파서에
// 들어오면 조용히 어긋난다. 그래서 그 형태가 오면 **버려진다는 것**을 여기 못박는다 —
// 버려지면 skipped 에 남아 WARN 이 뜨고, 조용한 손실이 아니게 된다.
func TestParseNumstatZAssumesNoRenames(t *testing.T) {
	// --no-renames 없이 나오는 형태: "3\t1\t\x00old.go\x00new.go\x00"
	paths, delta, skipped := parseNumstatZ([]byte("3\t1\t\x00old.go\x00new.go\x00"))
	if len(delta) != 0 {
		t.Errorf("rename 형태에서 규모가 나왔다: %v — 파서는 --no-renames 를 전제한다", delta)
	}
	if len(skipped) == 0 {
		t.Error("rename 형태가 조용히 버려졌다 — skipped 에 사유가 남아야 WARN 이 뜬다")
	}
	for _, p := range paths {
		if p == "" {
			t.Error("빈 경로가 경로 목록에 들어갔다")
		}
	}
}
```

- [ ] **Step 6: 초록불을 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/gitreader/ -run 'TestParseNumstatZ' -v && gofmt -l . && go vet ./...
```
Expected: 모든 하위 시험 PASS · `gofmt -l` 무출력 · `vet` 무출력

- [ ] **Step 7: 커밋**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-overlap-prescription-ignores-change-size
git add plugins/flightdeck/server/internal/model/types.go plugins/flightdeck/server/internal/gitreader/parse.go plugins/flightdeck/server/internal/gitreader/parse_test.go
git commit -m "$(cat <<'EOF'
feat(flightdeck): numstat 을 가르는 순수 함수와 LineDelta — 못 잰 것은 키가 없다

경로와 규모를 따로 낸다. 이진 파일은 `-`/`-` 로 오는데 그 파일이 바뀐 것은
사실이라 경로로는 남고, 규모는 못 잰 것이라 맵에 키가 없다. 둘을 한 반환값으로
접으면 "안 만졌다"와 "못 쟀다"가 같아진다.

깨진 레코드 하나가 축 전체를 끄지 않는다 — 그것만 버리고 사유를 올린다.

--no-renames 전제를 시험으로 잠갔다. 떼면 rename 이 3필드로 와서 조용히
어긋나는데, 지금은 버려지고 사유가 남는다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: gitreader 가 규모를 낸다

**Files:**
- Modify: `plugins/flightdeck/server/internal/gitreader/gitreader.go:248-271` (`ChangedPaths`) · 396-413행 뒤(`UncommittedDelta` 신설)
- Modify: `plugins/flightdeck/server/internal/service/service.go:92-102` (`GitReader` 인터페이스)
- Modify: `plugins/flightdeck/server/internal/service/board.go:440` (호출부 — 이 태스크에서는 새 값을 `_` 로 버린다)
- Modify: `plugins/flightdeck/server/internal/service/degrade_test.go:35` (`flakyReader`)
- Test: `plugins/flightdeck/server/internal/gitreader/gitreader_test.go`

**Interfaces:**
- Consumes: `parseNumstatZ`, `model.LineDelta` (Task 1)
- Produces:
  - `(*gitreader.Reader).ChangedPaths(ctx, base, head string) ([]string, map[string]model.LineDelta, error)`
  - `(*gitreader.Reader).UncommittedDelta(ctx, worktree string) (map[string]model.LineDelta, error)`
  - `service.GitReader` 인터페이스가 그 둘을 포함한다

**왜 새 메서드로 안 빼나.** `ChangedPaths` 를 그대로 두고 `ChangedDelta` 를 따로 만들면 `--name-only` 와 `--numstat` 이 **두 프로세스**가 된다. 커밋 구간이 공짜라는 이 계획의 전제가 거기서 무너진다.

**왜 `UncommittedPaths` 는 안 지우나.** `status --porcelain -z` 만이 **미추적 파일과 이름 변경 원본 경로**를 나른다(Task 1 의 관측). `diff --numstat HEAD` 는 둘 다 못 낸다. 그리고 별개 호출이라 **새 호출이 실패해도 경로 축이 산다**.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/gitreader/gitreader_test.go` 에 붙인다. **도우미는 이 파일에 이미 있다**(44-110행):
`newRepo(t) string` · `write(t, repo, rel, content)` · `commit(t, repo, msg) string`(전부 스테이징 후 커밋하고 **sha 를 돌려준다**) · `runGit(t, dir, args...)` · `ctxT(t) context.Context`.

```go
func TestChangedPathsCarriesLineDelta(t *testing.T) {
	repo := newRepo(t)
	write(t, repo, "t.txt", "a\nb\nc\n")
	base := commit(t, repo, "기준")
	write(t, repo, "t.txt", "a\nB\nc\nd\n") // b→B 가 +1/-1, d 가 +1 = +2/-1
	head := commit(t, repo, "고침")

	paths, delta, err := New(repo).ChangedPaths(ctxT(t), base, head)
	if err != nil {
		t.Fatalf("ChangedPaths 실패: %v", err)
	}
	if len(paths) != 1 || paths[0] != "t.txt" {
		t.Fatalf("경로 [t.txt] 를 기대했는데 %q 다", paths)
	}
	want := model.LineDelta{Added: 2, Removed: 1}
	if got := delta["t.txt"]; got != want {
		t.Errorf("규모 %+v 를 기대했는데 %+v 다", want, got)
	}
}

func TestUncommittedDeltaCoversStagedAndUnstaged(t *testing.T) {
	repo := newRepo(t)
	write(t, repo, "s.txt", "a\n")
	write(t, repo, "u.txt", "a\n")
	commit(t, repo, "기준")

	write(t, repo, "s.txt", "a\nb\n") // 스테이징할 것
	runGit(t, repo, "add", "s.txt")
	write(t, repo, "u.txt", "a\nb\nc\n") // 미스테이징
	write(t, repo, "new.txt", "x\n")     // 미추적 — commit 뒤에 쓰므로 추적 안 된다

	delta, err := New(repo).UncommittedDelta(ctxT(t), repo)
	if err != nil {
		t.Fatalf("UncommittedDelta 실패: %v", err)
	}
	if got, want := delta["s.txt"], (model.LineDelta{Added: 1, Removed: 0}); got != want {
		t.Errorf("스테이징된 것: %+v 를 기대했는데 %+v 다", want, got)
	}
	if got, want := delta["u.txt"], (model.LineDelta{Added: 2, Removed: 0}); got != want {
		t.Errorf("미스테이징된 것: %+v 를 기대했는데 %+v 다", want, got)
	}
	// ★ 미추적 파일은 numstat 에 아예 안 나온다(Task 1 의 실물 관측). 0 이 아니라 **키가 없다**.
	if d, ok := delta["new.txt"]; ok {
		t.Errorf("미추적 파일의 규모 키가 생겼다(%+v) — numstat 은 그것을 못 본다. "+
			"키가 있으면 화면이 못 잰 것을 잰 척한다", d)
	}
}
```

- [ ] **Step 2: 빨간불을 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/gitreader/ -run 'TestChangedPathsCarriesLineDelta|TestUncommittedDeltaCoversStagedAndUnstaged' -v
```
Expected: FAIL — 컴파일 오류(`ChangedPaths` 의 반환이 2개다 / `UncommittedDelta` 가 없다)

- [ ] **Step 3: `ChangedPaths` 를 numstat 으로 바꾼다**

`internal/gitreader/gitreader.go:258-271` 을 통째로 바꾼다. **248-257행의 기존 독스트링은 그대로 두고 아래 두 문단만 그 끝에 더한다.**

```go
// ★ **`--numstat` 이다(2026-08-11 개정. `--name-only` 였다).** 겹침 줄이 상대 세션의 변경
// 규모를 함께 내야 하는데, numstat 은 경로를 **덤으로** 준다 — 즉 같은 프로세스 하나로
// 경로와 규모를 둘 다 얻는다. 따로 메서드를 두면 git 프로세스가 하나 더 늘고, 이 축을
// 꼬리 겹침에만 실은 근거(비용이 안 는다)가 거기서 무너진다.
//
// ★ **반환이 셋인 것이 요점이다.** 이진 파일은 경로로는 남고 규모로는 안 남는다 —
// 하나로 접으면 "바뀌었는데 못 쟀다"를 표현할 수 없다(parseNumstatZ 주석).
func (r *Reader) ChangedPaths(ctx context.Context, base, head string) ([]string, map[string]model.LineDelta, error) {
	if err := validateRev("base", base); err != nil {
		return nil, nil, err
	}
	if err := validateRev("head", head); err != nil {
		return nil, nil, err
	}
	out, err := r.run(ctx, "", "diff", "--numstat", "-z", "--no-renames", base, head, "--")
	if err != nil {
		r.log.ErrorContext(ctx, "변경 경로 관측 실패", "base", base, "head", head, "error", err.Error())
		return nil, nil, fmt.Errorf("변경 경로 관측 실패(%s..%s): %w", base, head, err)
	}
	paths, delta, skipped := parseNumstatZ(out)
	if len(skipped) > 0 {
		// 조용히 버리지 않는다 — 규모가 왜 비었는지 남는 유일한 자리다.
		r.log.WarnContext(ctx, "numstat 레코드를 버렸다(그 경로만 규모가 빈다)",
			"base", base, "head", head, "dropped", len(skipped), "first_reason", skipped[0])
	}
	return paths, delta, nil
}
```

- [ ] **Step 4: `UncommittedDelta` 를 넣는다**

`internal/gitreader/gitreader.go` 의 `UncommittedPaths`(396-413행) **바로 뒤**에 넣는다.

```go
// UncommittedDelta 는 워크트리의 미커밋 증감이다 — 스테이징 + 미스테이징(추적 파일).
//
// ★ **UncommittedPaths 를 대체하지 않는다. 그 옆에 선다.** 이유 둘이고 둘 다 실물 관측이다:
//
//	① `status --porcelain` 만이 **미추적 파일과 이름 변경 원본 경로**를 낸다. numstat 은
//	   미추적 파일을 아예 안 본다 — 그것들은 경로로는 남고 규모로는 키가 없어야 맞다.
//	② **별개 호출이라 이것이 실패해도 경로 축이 산다.** UncommittedPaths 의 독스트링이
//	   "커밋 전 의도를 나르는 유일한 축이라 조용히 짧아지면 안 된다"고 못박은 그 축이다.
//	   합쳤다면 규모를 못 읽은 순간 경로도 같이 사라진다.
//
// ★ **이것이 이 축에서 유일하게 새로 드는 git 호출이다**(세션당 4→5). 커밋 구간은
// ChangedPaths 가 numstat 으로 바뀌면서 공짜가 됐다. 이 하나를 내는 이유는 이 축을 낳은
// 손 앵커들이 대부분 **랜딩 전 구간**이기 때문이다 — 안 재면 조율이 가장 필요한 창이 빈다.
//
// 커밋이 하나도 없는 저장소에서는 `HEAD` 가 없어 실패한다. **특례를 안 만든다** —
// 호출부(service.sessionCardsAndRoots)의 바로 이웃 줄인 Ref(기본 브랜치)가 같은 저장소에서
// 이미 같은 모양으로 실패를 낸다. 특례를 만들면 붙어 있는 두 줄의 관용이 갈린다.
func (r *Reader) UncommittedDelta(ctx context.Context, worktree string) (map[string]model.LineDelta, error) {
	out, err := r.run(ctx, worktree, "diff", "--numstat", "-z", "--no-renames", "HEAD", "--")
	if err != nil {
		r.log.ErrorContext(ctx, "미커밋 규모 관측 실패", "worktree", worktree, "error", err.Error())
		return nil, fmt.Errorf("미커밋 규모 관측 실패(%s): %w", worktree, err)
	}
	_, delta, skipped := parseNumstatZ(out)
	if len(skipped) > 0 {
		r.log.WarnContext(ctx, "numstat 레코드를 버렸다(그 경로만 규모가 빈다)",
			"worktree", worktree, "dropped", len(skipped), "first_reason", skipped[0])
	}
	return delta, nil
}
```

**경로를 버리는 것이 의도다** — 미커밋 경로는 `UncommittedPaths` 가 이미 낸다. 여기서 또 내면 같은 축에 원천이 둘이 된다.

- [ ] **Step 5: `GitReader` 인터페이스를 맞춘다**

`internal/service/service.go:92-102` 의 두 줄을 바꾸고 한 줄을 더한다.

```go
type GitReader interface {
	Refs(ctx context.Context) ([]model.RefState, error)
	Ref(ctx context.Context, ref string) (model.RefState, error)
	Worktrees(ctx context.Context) ([]gitreader.Worktree, error)
	ChangedPaths(ctx context.Context, base, head string) ([]string, map[string]model.LineDelta, error)
	MergeBase(ctx context.Context, a, b string) (string, error)
	UncommittedPaths(ctx context.Context, worktree string) ([]string, error)
	// UncommittedDelta 는 UncommittedPaths **옆에** 선다 — 위쪽이 미추적 파일과 이름 변경
	// 원본 경로를 나르고, 이쪽이 추적 파일의 증감을 나른다. 둘 다 필요하고, 갈라져 있어야
	// 이쪽이 실패해도 경로 축이 산다.
	UncommittedDelta(ctx context.Context, worktree string) (map[string]model.LineDelta, error)
	AheadBehind(ctx context.Context, ref, base string) (ahead, behind int, err error)
	Ancestry(ctx context.Context, sha, tip string) (judge.AncestryResult, error)
}
```

- [ ] **Step 6: 호출부와 fake 를 컴파일되게 맞춘다**

`internal/service/board.go:440` — **이 태스크에서는 규모를 아직 안 쓴다.** Task 3 이 쓴다.

```go
				} else if paths, _, err := g.ChangedPaths(ctx, forkSHA, card.View.Branch); err != nil {
```

`internal/service/degrade_test.go` 의 `flakyReader` — `ChangedPaths` 를 새 서명으로 바꾸고, `failUncommittedDelta` 갈래를 더한다(Task 3 이 그 시험을 쓴다).

```go
type flakyReader struct {
	GitReader
	failChanged          bool
	failAhead            bool
	failMergeBase        bool
	failUncommittedDelta bool
}

func (f flakyReader) ChangedPaths(ctx context.Context, base, head string) ([]string, map[string]model.LineDelta, error) {
	if f.failChanged {
		return nil, nil, errInjected
	}
	return f.GitReader.ChangedPaths(ctx, base, head)
}

// ★ **UncommittedPaths 는 안 감싼다.** 이 fake 의 요점이 "규모 축만 죽여도 경로 축이 사는가"라서,
// 둘을 같이 죽이면 그 단정이 성립하지 않는다.
func (f flakyReader) UncommittedDelta(ctx context.Context, worktree string) (map[string]model.LineDelta, error) {
	if f.failUncommittedDelta {
		return nil, errInjected
	}
	return f.GitReader.UncommittedDelta(ctx, worktree)
}
```

`degrade_test.go` 가 `model` 을 아직 임포트 안 했으면 더한다.

- [ ] **Step 7: 초록불과 관문을 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/gitreader/ -run 'TestChangedPaths|TestUncommittedDelta' -v && go test -count=1 ./... && gofmt -l . && go vet ./...
```
Expected: 전부 PASS · `gofmt -l` 무출력 · `vet` 무출력.
**기존 `TestChangedPathsIsTwoDotDiff`·`TestChangedPathsKeepsBothSidesOfARename`·`TestChangedPathsHandlesSpacesAndUnicodeAndNewlines` 도 반환값이 셋이 되도록 고쳐야 한다** — 그 셋은 경로만 단정하므로 `paths, _, err :=` 로 받으면 된다.

- [ ] **Step 8: 커밋**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-overlap-prescription-ignores-change-size
git add plugins/flightdeck/server/internal/gitreader/ plugins/flightdeck/server/internal/service/service.go plugins/flightdeck/server/internal/service/board.go plugins/flightdeck/server/internal/service/degrade_test.go
git commit -m "$(cat <<'EOF'
feat(flightdeck): ChangedPaths 를 numstat 으로 바꾸고 UncommittedDelta 를 옆에 세운다

커밋 구간은 공짜다 — --name-only 를 --numstat 으로 바꾸면 같은 프로세스가
경로와 규모를 둘 다 낸다. 따로 메서드를 두면 git 프로세스가 하나 더 늘고,
이 축을 꼬리 겹침에만 실은 근거가 거기서 무너진다.

미커밋 구간에만 호출이 하나 는다(세션당 4→5). UncommittedPaths 를 대체하지
않고 옆에 세운 이유 둘: status 만이 미추적 파일과 이름 변경 원본을 내고,
갈라져 있어야 규모를 못 읽어도 경로 축이 산다.

커밋 0개 저장소에 특례를 안 만든다 — 이웃 줄인 Ref(기본 브랜치)가 같은
저장소에서 이미 같은 모양으로 실패를 낸다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: 카드가 두 구간의 규모를 합산해서 든다

**Files:**
- Modify: `plugins/flightdeck/server/internal/model/types.go:353-363` (`SessionView` 에 필드 하나)
- Modify: `plugins/flightdeck/server/internal/service/service.go` (`UnionPaths` 뒤, 371행 부근 — `MergeDelta` 신설)
- Modify: `plugins/flightdeck/server/internal/service/board.go:440-464`
- Test: `plugins/flightdeck/server/internal/service/board_test.go` · `degrade_test.go`

**Interfaces:**
- Consumes: `model.LineDelta`(Task 1) · `GitReader.ChangedPaths`/`UncommittedDelta`(Task 2)
- Produces: `model.SessionView.PathDelta map[string]model.LineDelta` · `service.MergeDelta(sets ...map[string]model.LineDelta) map[string]model.LineDelta`

**합산이 옳은 이유.** `forkSHA..branch`(커밋된 것)와 `HEAD..worktree`(미커밋)는 **서로소 구간**이다. 더하면 "갈래 지점 이후 전부"가 정확히 나온다 — 어느 쪽이 이기는 규칙도 중복 제거도 필요 없다.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/service/board_test.go` 에 붙인다. **도우미는 전부 이미 있다**:
`newSvc(t) (*Service, *store.Store)` · `newRepoWithWorktree(t, branch) (repo, wt string)` ·
`writeFile(t, root, rel, content)` · `runGit(t, dir, args...)` ·
`openSession(t, s, project, projectPath, worktree, ccID, label) SessionResult` · `ctx() context.Context`.

**보드 서명은 `s.Board(ctx(), project string, opt BoardOptions) (BoardView, error)` 다** —
세션 id 는 `BoardOptions{Self: …}` 로 간다. 세션이 하나뿐이므로 카드는 `view.Sessions[0]` 로 집는다
(이 파일의 기존 관용이다. 도우미를 새로 만들지 마라).

```go
func TestSessionCardSumsCommittedAndUncommittedDelta(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")

	// ① 커밋된 구간 — 브랜치에 2줄짜리 파일을 새로 넣는다(+2/-0).
	writeFile(t, wt, "pipeline/run.py", "print(1)\nprint(2)\n")
	runGit(t, wt, "add", "-A")
	runGit(t, wt, "commit", "-q", "-m", "add pipeline")

	// ② 미커밋 구간 — 같은 파일에 1줄 더한다(+1/-0).
	writeFile(t, wt, "pipeline/run.py", "print(1)\nprint(2)\nprint(3)\n")

	sess := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")

	// ③ footprint 에만 있는 경로 — git 은 이 파일을 아예 모른다.
	if err := s.Beat(ctx(), sess.Session.ID, model.SignalTool,
		[]string{filepath.Join(wt, "docs/only-a-footprint.md")}); err != nil {
		t.Fatalf("Beat 실패: %v", err)
	}

	view, err := s.Board(ctx(), "p", BoardOptions{Self: sess.Session.ID})
	if err != nil {
		t.Fatalf("Board 실패: %v", err)
	}
	card := view.Sessions[0]

	// 두 구간은 서로소다 — 합이 갈래 지점 이후 전부다.
	want := model.LineDelta{Added: 3, Removed: 0}
	got, ok := card.View.PathDelta["pipeline/run.py"]
	if !ok {
		t.Fatalf("규모 키가 없다: %v", card.View.PathDelta)
	}
	if got != want {
		t.Errorf("%+v 를 기대했는데 %+v 다 — 커밋 구간(+2)과 미커밋 구간(+1)의 합이어야 한다", want, got)
	}

	// ★ footprint 전용 경로는 **경로로는 있고 규모로는 키가 없다.**
	var sawPath bool
	for _, p := range card.View.Paths {
		if p == "docs/only-a-footprint.md" {
			sawPath = true
		}
	}
	if !sawPath {
		t.Fatalf("발자국 경로가 카드에서 사라졌다: %q", card.View.Paths)
	}
	if d, ok := card.View.PathDelta["docs/only-a-footprint.md"]; ok {
		t.Errorf("git 이 모르는 경로에 규모 키가 생겼다(%+v) — 못 잰 것은 키가 없어야 한다", d)
	}
}

// ★ 이 시험이 Task 2 에서 UncommittedPaths 와 UncommittedDelta 를 가른 이유를 잠근다.
func TestUncommittedDeltaFailureKeepsThePathAxisAlive(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	writeFile(t, wt, "pipeline/run.py", "print(1)\n")
	runGit(t, wt, "add", "-A")
	runGit(t, wt, "commit", "-q", "-m", "add pipeline")
	writeFile(t, wt, "pipeline/run.py", "print(1)\nprint(2)\n") // 미커밋

	sess := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")

	// 같은 DB·같은 저장소에, 규모 축만 죽인 서비스를 다시 만든다
	// (degrade_test.go 의 TestBoardKeepsCoordinatingWhenOneDerivedAxisFails 와 같은 관용).
	broken := New(st, nil, WithGitFactory(func(repoPath string) GitReader {
		return flakyReader{GitReader: gitreader.New(repoPath), failUncommittedDelta: true}
	}))
	view, err := broken.Board(ctx(), "p", BoardOptions{Self: sess.Session.ID})
	if err != nil {
		t.Fatalf("Board 실패: %v", err)
	}
	card := view.Sessions[0]

	// 경로 축은 산다 — UncommittedPaths 는 따로 돌기 때문이다.
	var found bool
	for _, p := range card.View.Paths {
		if p == "pipeline/run.py" {
			found = true
		}
	}
	if !found {
		t.Fatalf("규모를 못 읽었다고 경로까지 사라졌다: %q — 두 호출을 가른 이유가 이것이다", card.View.Paths)
	}
	// 그리고 침묵하지 않는다.
	if !strings.Contains(card.DeriveError, "미커밋 규모를 못 읽었다") {
		t.Errorf("열화 사유가 안 남았다: %q", card.DeriveError)
	}
}
```

`board_test.go` 의 임포트에 `path/filepath` 와 `github.com/kweiza/flightdeck/internal/model` 이
없으면 더한다. `gitreader` 임포트는 `degrade_test.go` 에 이미 있다 — 두 번째 시험을 그 파일에
두면 임포트가 안 는다(어느 파일에 둘지는 구현자가 정하되 **한 곳에만** 둔다).

- [ ] **Step 2: 빨간불을 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/service/ -run 'TestSessionCardSumsCommittedAndUncommittedDelta|TestUncommittedDeltaFailureKeepsThePathAxisAlive' -v
```
Expected: FAIL — `card.View.PathDelta` 가 없다(컴파일 오류)

- [ ] **Step 3: `SessionView` 에 필드를 더한다**

`internal/model/types.go:356`(`Paths` 줄) **바로 뒤**에 넣는다.

```go
	// PathDelta 는 Paths 중 규모를 잰 것의 증감이다(커밋 구간 + 미커밋 구간의 **합**).
	//
	// ★ **없는 키는 0 이 아니라 "못 읽었다"** 다 — 바로 아래 Signals 와 같은 관용이다.
	// 이진 파일 · 미추적 파일 · footprint 에만 있는 경로 · git 파생 실패가 그 자리다.
	// Paths 와 합치지 않는 이유가 이것이다: 합치면 "바뀌었는데 못 쟀다"를 표현할 수 없다.
	//
	// 두 구간(`forkSHA..branch` 와 `HEAD..worktree`)은 **서로소**라 더하면 갈래 지점
	// 이후 전부가 정확히 나온다. 어느 쪽이 이기는 규칙이 필요 없다.
	PathDelta map[string]LineDelta
```

- [ ] **Step 4: `MergeDelta` 를 넣는다**

`internal/service/service.go` 의 `UnionPaths` **바로 뒤**(371행 부근)에 넣는다.

```go
// MergeDelta 는 규모 맵 여럿을 **더해서** 하나로 만든다. 순수 함수다.
//
// ★ 덮어쓰기가 아니라 합이다. 부르는 자리에서 들어오는 두 구간(커밋된 `forkSHA..branch` 와
// 미커밋 `HEAD..worktree`)이 **서로소**라, 합이 곧 "갈래 지점 이후 전부"다. 덮어쓰면 나중에
// 온 구간만 남아 규모가 조용히 작아진다.
//
// ★ **없는 키를 만들지 않는다.** 어느 맵에도 없던 경로는 결과에도 없다 — 그것이
// "못 읽었다"를 나르는 유일한 방법이고, 0 으로 채우면 이 축 전체가 뜻을 잃는다.
func MergeDelta(sets ...map[string]model.LineDelta) map[string]model.LineDelta {
	var out map[string]model.LineDelta
	for _, set := range sets {
		for p, d := range set {
			if out == nil {
				out = map[string]model.LineDelta{}
			}
			cur := out[p]
			out[p] = model.LineDelta{Added: cur.Added + d.Added, Removed: cur.Removed + d.Removed}
		}
	}
	return out
}
```

- [ ] **Step 5: board 가 두 구간을 채우게 한다**

`internal/service/board.go:440`(Task 2 에서 `_` 로 둔 자리)를 되살린다.

```go
				} else if paths, delta, err := g.ChangedPaths(ctx, forkSHA, card.View.Branch); err != nil {
					d.fail("changed-paths:"+clip(card.View.Branch, 120), err)
					fails = append(fails, "변경 경로를 못 읽었다")
				} else {
					d.ok()
					card.View.Paths = UnionPaths(card.View.Paths, paths)
					card.View.PathDelta = MergeDelta(card.View.PathDelta, delta)
					// 보관되는 뜻이 정확해진다 — 갈래 기준 diff 는 forkSHA 로부터의 두 점 diff 와 같다.
					s.rememberChangeSet(ctx, proj.ID, forkSHA, card.View.BranchSHA, paths)
				}
```

그리고 `UncommittedPaths` 블록(458-464행) **바로 뒤**에 넣는다.

```go
			// 미커밋 규모 — **위 경로 축과 갈라 둔다.** 이것이 실패해도 위가 살아야 하고
			// (커밋 전 의도를 나르는 유일한 축이다), 그것이 두 git 호출을 안 합친 이유다.
			// 이 호출 하나가 이 축에서 새로 드는 비용의 전부다(세션당 4→5).
			if ud, err := g.UncommittedDelta(ctx, v.Session.Worktree); err != nil {
				d.fail("uncommitted-delta:"+clip(v.Session.ID, 64), err)
				fails = append(fails, "미커밋 규모를 못 읽었다")
			} else {
				d.ok()
				card.View.PathDelta = MergeDelta(card.View.PathDelta, ud)
			}
```

- [ ] **Step 6: 초록불과 관문을 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/service/ -run 'TestSessionCardSums|TestUncommittedDeltaFailure' -v && go test -count=1 ./... && gofmt -l . && go vet ./...
```
Expected: 전부 PASS · 무출력 둘

- [ ] **Step 7: 커밋**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-overlap-prescription-ignores-change-size
git add plugins/flightdeck/server/internal/model/types.go plugins/flightdeck/server/internal/service/
git commit -m "$(cat <<'EOF'
feat(flightdeck): 세션 카드가 커밋·미커밋 두 구간의 규모를 합쳐 든다

두 구간(forkSHA..branch 와 HEAD..worktree)은 서로소라 합이 곧 갈래 지점
이후 전부다. 어느 쪽이 이기는 규칙도 중복 제거도 필요 없다.

PathDelta 를 Paths 와 합치지 않는다 — 합치면 "바뀌었는데 못 쟀다"를 표현할
수 없다. 없는 키가 0 이 아니라 "못 읽었다"이고, 그 관용은 바로 위 Signals 가
이미 쓰던 것이다.

미커밋 규모만 죽였을 때 경로 축이 사는지를 시험으로 잠갔다 — 두 git 호출을
안 합친 이유가 그것이다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: 겹침이 상대 규모를 나르고 큰 순으로 선다

**Files:**
- Modify: `plugins/flightdeck/server/internal/judge/eligible.go:53-59`(`Overlap`) · `61-78`(`LiveSession`) · `228-241`(`OverlapsWithLive`)
- Modify: `plugins/flightdeck/server/internal/service/board.go:552-564`(`liveFor`)
- Modify: `plugins/flightdeck/server/internal/mcpsrv/mcpsrv.go` (`liveOf` — 이름은 파일에서 확인)
- Test: `plugins/flightdeck/server/internal/judge/eligible_test.go`

**Interfaces:**
- Consumes: `model.LineDelta`(Task 1) · `model.SessionView.PathDelta`(Task 3)
- Produces:
  - `judge.LiveSession.Delta map[string]model.LineDelta`
  - `judge.Overlap.TheirDelta map[string]model.LineDelta` (키는 **상대 세션의 경로** = `Pairs[i][1]`)
  - `judge.SortOverlapsBySize(os []Overlap)` — 제자리 정렬

**`Pairs [][2]string` 을 안 바꾸는 것이 요점이다.** `judge.OverlapPairs` 는 처방(`overlapPrescriptions`)도 쓰는데 그 경로는 git 을 안 돌아 규모를 원리적으로 못 채운다. 서명을 바꿨다면 처방 쪽에 영원히 빈 필드가 생긴다.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/judge/eligible_test.go` 에 붙인다.

```go
func TestOverlapsCarryTheirDelta(t *testing.T) {
	live := []LiveSession{{
		ID: "s-1", CCSessionID: "cc-other", Paths: []string{"a.go", "b.bin"},
		Delta: map[string]model.LineDelta{"a.go": {Added: 47, Removed: 1}},
	}}
	got := OverlapsWithLive([]string{"a.go", "b.bin"}, live, "me", "cc-me")
	if len(got) != 1 {
		t.Fatalf("겹침 1건을 기대했는데 %d건이다", len(got))
	}
	if d, want := got[0].TheirDelta["a.go"], (model.LineDelta{Added: 47, Removed: 1}); d != want {
		t.Errorf("a.go: %+v 를 기대했는데 %+v 다", want, d)
	}
	// ★ 상대가 규모를 안 준 경로는 **키가 없어야** 한다. 0 이 아니다.
	if d, ok := got[0].TheirDelta["b.bin"]; ok {
		t.Errorf("b.bin 의 규모 키가 생겼다(%+v) — 상대가 못 잰 것은 키가 없어야 한다", d)
	}
}

func TestOverlapsSortBiggestFirstAndUnknownOnTop(t *testing.T) {
	mk := func(id string, d map[string]model.LineDelta) LiveSession {
		return LiveSession{ID: id, CCSessionID: "cc-" + id, Paths: []string{"a.go"}, Delta: d}
	}
	live := []LiveSession{
		mk("s-small", map[string]model.LineDelta{"a.go": {Added: 3}}),
		mk("s-unknown", nil), // 규모를 못 읽었다
		mk("s-big", map[string]model.LineDelta{"a.go": {Added: 47, Removed: 1}}),
		mk("s-zero", map[string]model.LineDelta{"a.go": {Added: 0, Removed: 0}}),
	}
	got := OverlapsWithLive([]string{"a.go"}, live, "me", "cc-me")

	var order []string
	for _, o := range got {
		order = append(order, o.SessionID)
	}
	want := []string{"s-unknown", "s-big", "s-small", "s-zero"}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("순서 %q 를 기대했는데 %q 다", want, order)
	}
}

// ★ 이 시험이 이 태스크의 논지를 지킨다. 못 읽은 것을 0 으로 접으면
// s-unknown 이 s-zero 옆으로 내려가고 절단될 때 제일 먼저 사라진다.
func TestUnknownSizeOutranksAMeasuredZero(t *testing.T) {
	live := []LiveSession{
		{ID: "s-zero", CCSessionID: "cc-a", Paths: []string{"a.go"},
			Delta: map[string]model.LineDelta{"a.go": {}}},
		{ID: "s-unknown", CCSessionID: "cc-b", Paths: []string{"a.go"}},
	}
	got := OverlapsWithLive([]string{"a.go"}, live, "me", "cc-me")
	if got[0].SessionID != "s-unknown" {
		t.Fatalf("못 읽은 것이 맨 위여야 한다 — %q 가 먼저다. 아래로 밀면 절단될 때 "+
			"제일 먼저 사라지는 것이 못 잰 것이 된다", got[0].SessionID)
	}
}

func TestOverlapSortIsDeterministicOnTies(t *testing.T) {
	mk := func(id string) LiveSession {
		return LiveSession{ID: id, CCSessionID: "cc-" + id, Paths: []string{"a.go"},
			Delta: map[string]model.LineDelta{"a.go": {Added: 5}}}
	}
	got := OverlapsWithLive([]string{"a.go"}, []LiveSession{mk("s-b"), mk("s-a")}, "me", "cc-me")
	if got[0].SessionID != "s-a" || got[1].SessionID != "s-b" {
		t.Errorf("동점은 세션 id 오름차순이어야 한다: %q, %q", got[0].SessionID, got[1].SessionID)
	}
}
```

- [ ] **Step 2: 빨간불을 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/judge/ -run 'TestOverlaps|TestUnknownSizeOutranks|TestOverlapSort' -v
```
Expected: FAIL — `LiveSession.Delta` / `Overlap.TheirDelta` 가 없다

- [ ] **Step 3: 두 타입에 필드를 더한다**

`internal/judge/eligible.go` 의 `Overlap`(53-59행) 에 필드 하나:

```go
type Overlap struct {
	SessionID string      // 상대 세션 id
	Label     string      // 표시 전용. 판정에 안 쓴다
	Pairs     [][2]string // [0]=이 항목의 경로, [1]=상대 세션의 경로

	// TheirDelta 는 Pairs[i][1](**상대 세션의 경로**)의 증감이다.
	//
	// ★ **키가 없으면 0 이 아니라 "못 읽었다"** 다.
	//
	// ★ **왼쪽에는 규모를 안 싣는다.** 이 타입을 채우는 자리가 둘인데 왼쪽에 넣는 것이
	// 서로 다르다 — board 는 내 발자국, pick 은 항목의 **선언 경로**(아직 diff 가 아니다).
	// 왼쪽에 규모를 실으면 pick 에서 `(+0/-0)` 이라는 거짓말을 하거나 두 표면의 문구가
	// 갈린다. 그리고 내 규모는 내가 이미 안다 — 알아야 하는 것은 상대 쪽이다.
	//
	// ★ **Pairs 와 합치지 않는다.** Pairs 를 만드는 OverlapPairs 는 처방
	// (overlapPrescriptions)도 쓰는데 그 경로는 턴마다 돌아 git 을 안 탄다(설계 §6) —
	// 즉 이 값을 **원리적으로** 못 채운다. 합쳤다면 그쪽에 영원히 빈 필드가 생긴다.
	TheirDelta map[string]model.LineDelta
}
```

`LiveSession`(61-78행) 에 필드 하나 — `Paths` 줄 바로 뒤:

```go
	// Delta 는 Paths 중 규모를 잰 것의 증감이다. **없는 키는 0 이 아니라 "못 읽었다"** 다.
	//
	// 채우는 자리는 git 파생을 이미 도는 경로 하나뿐이다(service.sessionCardsAndRoots).
	// 처방 경로(service.Prescriptions)는 이것을 **비운 채로** 넘긴다 — 그 경로가 git 을
	// 안 도는 것이 설계 §6 판정이라, 여기서 채우려 하면 모든 턴 종료에 저장소 전수 훑기가 붙는다.
	Delta map[string]model.LineDelta
```

- [ ] **Step 4: `OverlapsWithLive` 와 정렬을 쓴다**

`internal/judge/eligible.go:228-241` 을 바꾼다. 기존 독스트링이 있으면 그대로 두고 본문만 바꾼다.

```go
func OverlapsWithLive(paths []string, live []LiveSession, self, selfCC string) []Overlap {
	var out []Overlap
	for _, s := range live {
		if s.ID == self || sameConversation(selfCC, s.CCSessionID) {
			continue
		}
		pairs := OverlapPairs(paths, s.Paths)
		if len(pairs) == 0 {
			continue
		}
		o := Overlap{SessionID: s.ID, Label: s.Label, Pairs: pairs}
		// 상대 경로에 대해 **아는 것만** 싣는다. 모르는 것은 키를 안 만든다.
		for _, p := range pairs {
			d, ok := s.Delta[p[1]]
			if !ok {
				continue
			}
			if o.TheirDelta == nil {
				o.TheirDelta = map[string]model.LineDelta{}
			}
			o.TheirDelta[p[1]] = d
		}
		out = append(out, o)
	}
	SortOverlapsBySize(out)
	return out
}

// SortOverlapsBySize 는 상대 규모가 큰 순으로 세운다(제자리). 순수 함수다.
//
// ★ **못 읽은 것은 `+∞` 다 — 맨 위다.** 아래로 밀면 화면이 절단될 때
// (mcpsrv.tailOverlapLimit) **제일 먼저 사라지는 것이 못 잰 것**이 된다. 이 저장소가
// 반복해서 고발한 침묵이 정확히 그 모양이고, 같은 규율이 세 자리에 이미 적혀 있다 —
// judge/prescribe.go 의 uncoveredByClosed("못 읽었다는 없다가 아니다") · sameConversation
// ("빈 값끼리는 같지 않다") · service/board.go 의 HasFootprint("발자국이 없다는 사실을
// 침묵하지 않는다"). 모르는 것은 클 수 있으니 크게 친다.
//
// ★ **계층을 두지 않는다.** "못 읽은 것은 안 잘린다"를 따로 보장하려면 규칙이 둘이 되고,
// 그 둘이 어긋나는 경계(못 읽은 것이 상한보다 많을 때)에서 답이 없다. 규칙 하나로 접는다.
//
// ★ **laneTurnPrescription 의 선례를 일부러 안 따랐다.** 거기서는 "0 은 차례 아님이고
// 못 읽었을 때도 0 이다 — 둘을 안 가르는 것이 맞다"라고 적혀 있다. 방향이 반대이기
// 때문이다: 거기서는 못 읽은 것을 펴면 세션을 반드시 실패할 자리로 보내고, 여기서는
// 접으면 정보가 사라지고 펴면 조율이 한 번 더 일어날 뿐이다.
//
// 동점은 세션 id 오름차순이다 — 같은 입력에 같은 출력이어야 시험이 순서를 단정한다.
func SortOverlapsBySize(os []Overlap) {
	sort.SliceStable(os, func(i, j int) bool {
		ui, si := overlapWeight(os[i])
		uj, sj := overlapWeight(os[j])
		if ui != uj {
			return ui // 못 읽은 쪽이 위
		}
		if si != sj {
			return si > sj
		}
		return os[i].SessionID < os[j].SessionID
	})
}

// overlapWeight 는 정렬 키다 — (못 읽은 쌍이 하나라도 있나, 아는 쌍 중 최대 증감합).
//
// ★ 합이 아니라 **최대**다. 겹침이 여럿일 때 알아야 하는 것은 "제일 큰 것이 얼마나 큰가"이지
// 총량이 아니다 — 작은 파일 열 개가 47줄 개정 하나보다 위로 오면 안 된다.
func overlapWeight(o Overlap) (unknown bool, max int) {
	for _, p := range o.Pairs {
		d, ok := o.TheirDelta[p[1]]
		if !ok {
			unknown = true
			continue
		}
		if s := d.Added + d.Removed; s > max {
			max = s
		}
	}
	return unknown, max
}
```

`eligible.go` 의 임포트에 `"sort"` 를 더한다(`fmt`·`time`·`model` 이 이미 있다).

- [ ] **Step 5: 두 조립부가 규모를 넘기게 한다**

`internal/service/board.go:552-564` 의 `liveFor`:

```go
func liveFor(cards []SessionCard) []judge.LiveSession {
	out := make([]judge.LiveSession, 0, len(cards))
	for _, c := range cards {
		out = append(out, judge.LiveSession{
			ID: c.View.Session.ID, Label: c.View.Session.Label, Paths: c.View.Paths,
			// 규모도 함께 넘긴다 — 이 자리가 git 파생을 이미 돈 유일한 자리다.
			// 안 넘기면 꼬리 겹침이 규모를 원리적으로 못 낸다.
			Delta: c.View.PathDelta,
			// ★ 대화 id 를 함께 넘긴다. 카드 id 만으로는 형제 카드(같은 대화, 다른 카드)를
			// 남으로 보고 **자기 자신과 겹친다**고 알린다. prescribe 쪽이 먼저 같은 사고를
			// 겪고 같은 한 줄로 고쳤다(service/prescribe.go 의 Others 조립부).
			CCSessionID: c.View.Session.CCSessionID,
		})
	}
	return out
}
```

`internal/mcpsrv/mcpsrv.go:717-728` 의 `liveOf(cards []service.SessionCard) []judge.LiveSession` — 같은 변환을 하는 **둘째** 자리다. 같은 줄을 더한다.

```go
			ID: c.View.Session.ID, Label: c.View.Session.Label, Paths: c.View.Paths,
			// 규모도 함께 넘긴다. 이 변환이 두 자리(service.liveFor · 여기)에 있는데,
			// 한쪽만 고치면 board 와 pick 중 한쪽에서만 규모가 뜬다.
			Delta: c.View.PathDelta,
```

**두 자리 다 고쳐야 한다.** `service.liveFor` 는 `pick` 이 쓰고(`pick.go:291`), `mcpsrv.liveOf` 는 `board` 가 쓴다(`mcpsrv.go:664`).

`internal/service/prescribe.go:211-216` 의 `in.Others` 조립부는 **안 건드린다.** 거기는 `Delta` 를 비운 채로 남는 것이 맞다 — 그 자리가 git 을 안 도는 것이 §6 판정이다.

- [ ] **Step 6: 초록불과 관문을 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/judge/ -v -run 'Overlap|Unknown' && go test -count=1 ./... && gofmt -l . && go vet ./...
```
Expected: 전부 PASS · 무출력 둘

- [ ] **Step 7: 커밋**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-overlap-prescription-ignores-change-size
git add plugins/flightdeck/server/internal/judge/ plugins/flightdeck/server/internal/service/board.go plugins/flightdeck/server/internal/mcpsrv/mcpsrv.go
git commit -m "$(cat <<'EOF'
feat(flightdeck): 겹침이 상대 규모를 나르고 큰 순으로 선다 — 못 읽은 것이 맨 위다

Pairs 의 서명을 안 바꾼다. OverlapPairs 는 처방도 쓰는데 그 경로는 git 을
안 돌아 규모를 원리적으로 못 채운다 — 합쳤다면 거기 영영 빈 필드가 생긴다.

왼쪽에는 규모를 안 싣는다. board 의 왼쪽은 내 발자국이고 pick 의 왼쪽은
항목의 선언 경로라, 왼쪽에 실으면 pick 에서 +0/-0 이라는 거짓말을 하거나
두 표면의 문구가 갈린다.

못 읽은 것을 +∞ 로 친다. 아래로 밀면 절단될 때 제일 먼저 사라지는 것이
못 잰 것이 된다. laneTurnPrescription 이 반대로 접는 이유(펴면 반드시
실패할 자리로 보낸다)는 여기 안 맞아서 그 선례를 안 따랐다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: 꼬리가 규모를 낸다

**Files:**
- Modify: `plugins/flightdeck/server/internal/mcpsrv/render.go:1730-1753`
- Test: `plugins/flightdeck/server/internal/mcpsrv/render_test.go`

**Interfaces:**
- Consumes: `judge.Overlap.TheirDelta`(Task 4)
- Produces: 없다 — 이 태스크의 산출은 화면 문자열이다.

**단정은 소비자의 좌표계로 쓴다**(DESIGN §12) — 즉 `RenderTail` 이 낸 **문자열**을 단정한다.

- [ ] **Step 1: 실패하는 시험을 쓴다**

`internal/mcpsrv/render_test.go` 에 붙인다. 기존 관용은 `RenderTail(TailInput{Now: t0, NotesObserved: true, OverlapsObserved: true, Overlaps: …})` 다(77-85행).

**기존 시험 하나를 미리 확인해라.** `render_test.go:82-84` 가 `strings.Contains(withOverlap, "pipeline/↔pipeline/x.py")` 로 단정하는데, 그 겹침은 `TheirDelta` 가 없으므로 `pipeline/↔pipeline/x.py(규모?)` 가 된다 — **`Contains` 라 그대로 통과한다.** 통과하는 것을 눈으로 확인만 하고 고치지 마라(고치면 그 시험이 재던 축이 바뀐다).

```go
func TestTailShowsTheirChangeSize(t *testing.T) {
	out := RenderTail(TailInput{
		NotesObserved:    true,
		OverlapsObserved: true,
		Overlaps: []judge.Overlap{{
			SessionID: "01KZP9EFAAAAAAAAAAAAAAAAAA", Label: "",
			Pairs:      [][2]string{{"DESIGN.md", "DESIGN.md"}, {"cmds.go", "cmds.go"}},
			TheirDelta: map[string]model.LineDelta{"DESIGN.md": {Added: 47, Removed: 1}},
		}},
	})

	if !strings.Contains(out, "DESIGN.md↔DESIGN.md(+47/-1)") {
		t.Errorf("아는 규모가 안 붙었다:\n%s", out)
	}
	// ★ 상대가 못 잰 경로는 `(규모?)` 다.
	if !strings.Contains(out, "cmds.go↔cmds.go(규모?)") {
		t.Errorf("못 읽은 규모 표기가 없다:\n%s", out)
	}
	if !strings.Contains(out, "상대 규모 큰 순") {
		t.Errorf("머리줄이 정렬을 안 말한다:\n%s", out)
	}
}

// ★ 이 시험이 이 계획 전체의 논지를 지킨다. 못 읽음을 0 으로 접으면 여기가 빨개진다.
func TestTailNeverRendersUnknownSizeAsZero(t *testing.T) {
	out := RenderTail(TailInput{
		NotesObserved:    true,
		OverlapsObserved: true,
		Overlaps: []judge.Overlap{{
			SessionID: "01KZPBB3AAAAAAAAAAAAAAAAAA",
			Pairs:     [][2]string{{"DESIGN.md", "DESIGN.md"}},
			// TheirDelta 가 nil 이다 — 상대의 규모를 못 읽었다.
		}},
	})
	if strings.Contains(out, "+0/-0") {
		t.Fatalf("못 읽은 규모를 +0/-0 으로 찍었다 — 읽는 쪽은 그것을 '안 만졌다'로 읽는다:\n%s", out)
	}
	if !strings.Contains(out, "(규모?)") {
		t.Fatalf("못 읽었다는 표기가 없다:\n%s", out)
	}
}

func TestTailSaysWhatWasCutWhenOverlapsAreTruncated(t *testing.T) {
	var os []judge.Overlap
	for i := 0; i < tailOverlapLimit+2; i++ {
		os = append(os, judge.Overlap{
			SessionID: fmt.Sprintf("01KZP%021d", i),
			Pairs:     [][2]string{{"a.go", "a.go"}},
			TheirDelta: map[string]model.LineDelta{
				"a.go": {Added: tailOverlapLimit + 2 - i},
			},
		})
	}
	out := RenderTail(TailInput{NotesObserved: true, OverlapsObserved: true, Overlaps: os})
	if !strings.Contains(out, "제일 작은 쪽") {
		t.Errorf("잘린 것이 무엇인지 안 말한다 — 정렬에 뜻이 생겼으면 화면이 그것을 말해야 한다:\n%s", out)
	}
}
```

- [ ] **Step 2: 빨간불을 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/mcpsrv/ -run 'TestTailShowsTheirChangeSize|TestTailNeverRendersUnknownSizeAsZero|TestTailSaysWhatWasCut' -v
```
Expected: FAIL — 규모가 안 붙는다 / `(규모?)` 가 없다

- [ ] **Step 3: `renderDelta` 를 넣는다**

`internal/mcpsrv/render.go` 의 `RenderTail` **바로 앞**(1698행 부근)에 넣는다.

```go
// renderDelta 는 상대 경로 하나의 증감 표기다. 순수 함수다.
//
// ★ **키가 없으면 `(규모?)` 다 — `(+0/-0)` 이 아니다.** 수와 **모양이 달라야** 한다:
// 0 으로 찍으면 읽는 쪽이 "안 만졌다"로 읽고, 그것이 이 축이 없애려는 오탐의 거울상이다.
// 못 재는 자리가 넷 있다 — 이진 파일 · 미추적 파일 · footprint 에만 있는 경로 · git 파생 실패.
func renderDelta(m map[string]model.LineDelta, path string) string {
	d, ok := m[path]
	if !ok {
		return "(규모?)"
	}
	return fmt.Sprintf("(+%d/-%d)", d.Added, d.Removed)
}
```

- [ ] **Step 4: 꼬리 문구 세 줄을 바꾼다**

`internal/mcpsrv/render.go:1731`(머리줄):

```go
		lines = append(lines, fmt.Sprintf(
			"겹침 %d건 (거르지 않고 알린다 · 상대 규모 큰 순):", len(in.Overlaps)))
```

`:1734-1736`(절단 줄) — **순서에 뜻이 생겼으므로 무엇이 잘렸는지 말한다.** 화면이 말 안 한 주장을 하면 안 된다.

```go
			if i >= tailOverlapLimit {
				lines = append(lines, fmt.Sprintf(
					"  · … %d건 더(제일 작은 쪽이다) — 수는 위 머리줄이 전부 센 값이다. 이름까지는 board 가 낸다",
					len(in.Overlaps)-tailOverlapLimit))
				break
			}
```

`:1745`(쌍 한 줄):

```go
				pairs = append(pairs, fmt.Sprintf("%s↔%s%s", p[0], p[1], renderDelta(o.TheirDelta, p[1])))
```

- [ ] **Step 5: 초록불과 관문을 확인한다**

```bash
cd plugins/flightdeck/server && go test ./internal/mcpsrv/ -v -run 'TestTail' && go test -count=1 ./... && gofmt -l . && go vet ./...
```
Expected: 전부 PASS · 무출력 둘.
**기존 꼬리 시험 중 겹침 줄 문자열을 통째로 단정하는 것이 있으면 새 형태로 고친다** — `(규모?)` 가 붙는다.

- [ ] **Step 6: 커밋**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-overlap-prescription-ignores-change-size
git add plugins/flightdeck/server/internal/mcpsrv/
git commit -m "$(cat <<'EOF'
feat(flightdeck): 꼬리 겹침 줄이 상대 규모를 낸다 — 못 잰 것은 (규모?) 다

경로쌍마다 상대 쪽 증감을 붙인다. 키가 없으면 (규모?) — (+0/-0) 이 아니다.
수와 모양이 달라야 한다: 0 으로 찍으면 읽는 쪽이 "안 만졌다"로 읽고, 그것이
이 축이 없애려는 오탐의 거울상이다.

머리줄과 절단 줄이 정렬을 소리 내어 말한다. 순서에 뜻이 생겼는데 화면이
그것을 안 말하면 화면이 말 안 한 주장을 하게 된다.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: DESIGN 한 문단과 처방 쪽 주석, 그리고 전체 관문

**Files:**
- Modify: `plugins/flightdeck/DESIGN.md` — **477행 뒤 순삽입만**
- Modify: `plugins/flightdeck/server/internal/service/prescribe.go:16-24`(파일 머리 주석)
- Test: 없다(문서와 주석) — 대신 **관문 셋을 전부 돌린다**

**Interfaces:** 없다.

- [ ] **Step 1: DESIGN 에 문단 하나를 순삽입한다**

`plugins/flightdeck/DESIGN.md` 의 **477행 뒤**(빈 줄 478행 앞)에 넣는다. 넣기 전에 `sed -n '469,480p' plugins/flightdeck/DESIGN.md` 로 자리가 그대로인지 확인해라 — 다른 세션이 이 문서를 만지고 있다.

```markdown

**그리고 규모를 함께 낸다.** 겹침 줄은 상대 세션이 그 경로를 얼마나 만졌는지(`+증가/-감소`)를
함께 내고 큰 것부터 세운다 — 경로가 겹치는가만으로는 한 줄 순삽입과 47줄 개정이 같은 무게로 뜬다.
**못 잰 것은 0 이 아니라 `(규모?)` 다.**
```

**안 건드리는 자리:** 469행(소절 제목) · 471-474행(번호 목록) · 476-477행. 이 범위는 판단 `01KZPFZ58NEPD6DX4A7JH2ZB6E` 로 선언돼 있다.

- [ ] **Step 2: 처방 쪽에 왜 이 축이 안 왔는지 남긴다**

`internal/service/prescribe.go` 의 파일 머리 주석(16-24행) 끝에 더한다. **코드는 한 줄도 안 바꾼다.**

```go
// ★ **그래서 겹침 처방은 변경 규모를 안 낸다 — 그것은 결함이 아니라 이 판정의 결과다**
// (2026-08-11). 규모(`+47/-1`)를 내려면 numstat 을 읽어야 하고, 그것은 위 문단이 금지한
// 바로 그 저장소 훑기다. 규모는 **git 파생을 이미 도는 표면**에만 실었다 —
// `board`·`pick` 의 꼬리 겹침(judge.OverlapsWithLive)이다. 거기가 사람이 "ask 를 써야 하나"를
// 실제로 판단하는 자리이기도 하다.
//
// 그래서 아래 `in.Others` 조립부는 judge.LiveSession.Delta 를 **비운 채로** 넘긴다.
// 그것이 맞다 — 채우려 드는 순간 모든 턴 종료에 저장소 전수 훑기가 붙는다.
// 다음 사람이 이 빈 필드를 결함으로 보고 "고치지" 않도록 여기 남긴다.
```

- [ ] **Step 3: 관문 셋을 전부 돌린다**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-overlap-prescription-ignores-change-size/plugins/flightdeck/server
gofmt -l .
go vet ./...
go test -count=1 ./...
```
Expected: `gofmt -l` **무출력** · `vet` **무출력** · 모든 패키지 `ok`.

**무출력이 통과인지 확인해라** — `pwd` 가 `plugins/flightdeck/server` 인지 먼저 봐라. 모듈 밖에서 돌린 `gofmt` 는 빈 디렉토리를 검사하고 조용히 통과한다.

- [ ] **Step 4: DESIGN 인용 관문이 도는지 따로 확인한다**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-overlap-prescription-ignores-change-size/plugins/flightdeck/server
go test ./cmd/fd/ -run TestDesignSectionCitationsResolve -v
```
Expected: PASS. 이 태스크들이 새로 쓴 주석이 `설계 §6`·`DESIGN §12` 를 인용한다 — 그 절들은 실재한다.

- [ ] **Step 5: DESIGN 편집이 선언한 범위 그대로인지 확인한다**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-overlap-prescription-ignores-change-size
git diff --numstat HEAD -- plugins/flightdeck/DESIGN.md
```
Expected: `4\t0\tplugins/flightdeck/DESIGN.md` — **삭제가 0 이어야 한다.** 0 이 아니면 선언한 범위를 넘긴 것이니 되돌리고 `note(kind='ask')` 로 정정부터 내라.

- [ ] **Step 6: 커밋**

```bash
cd /home/aaron/cdo-dev/kweiza-cc-plugins/.flightdeck/worktrees/fd-overlap-prescription-ignores-change-size
git add plugins/flightdeck/DESIGN.md plugins/flightdeck/server/internal/service/prescribe.go
git commit -m "$(cat <<'EOF'
docs(flightdeck): §5 에 겹 하나를 더하고, 처방이 왜 이 수를 안 내는지 그 자리에 적는다

DESIGN 은 477행 뒤 문단 하나만 순삽입했다(삭제 0). 번호 목록에 안 넣은
이유는 셋이 전부 결함의 수정이고 이번 것은 축에 더하는 성질이라서다 —
그리고 제목이 "세 겹"이라 항목을 더하면 제목을 치환해야 한다.

처방 쪽에는 코드 대신 사실을 남겼다. 겹침 처방이 규모를 안 내는 것은 결함이
아니라 "세션 카드 파생을 안 돈다"는 §6 판정의 결과다. LiveSession.Delta 가
그 경로에서 비어 있는 것도 의도다 — 다음 사람이 그것을 고치러 가지 않게.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

## 끝난 뒤

`fd-handoff` 로 마무리한다. 판단에 반드시 남길 것:

- **항목 본문의 전제 둘이 틀렸다는 것**(관문은 이미 답이 있었고, 표면은 둘이었다). 안 남기면 다음 사람이 같은 조사를 다시 한다.
- **훅 실측(`ToolInput` 에 규모가 있다)을 알면서 기각했다는 것** — 같은 수에 원천이 둘이 되고, git 이 이미 보는 것을 두 번 재게 된다.
- **처방에 규모를 일부러 안 실었다는 것** — 이것을 안 남기면 다음 사람이 `LiveSession.Delta` 가 처방 경로에서 비어 있는 것을 결함으로 보고 고치러 간다.
- 미커밋 numstat 이 세션당 git 호출을 4→5 로 늘렸다는 것과, 그 비용이 `fd status` 의 파생 계측에 이미 들어간다는 것.
