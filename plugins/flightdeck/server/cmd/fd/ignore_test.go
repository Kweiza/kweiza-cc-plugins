package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// 무시 경로 시험 — 좌표계는 **어느 트리가 답했는가**다.
//
// ★ 이 파일이 지키는 비대칭이 하나 있다. 이 필터가 틀리는 방향은 반드시
// "너무 많이 남긴다"여야 하고 "조용히 지운다"이면 안 된다. 남긴 것은 화면에서
// 사람이 걸러 읽을 수 있지만, 지워진 발자국은 아무 데도 안 나타난다 —
// 그 세션이 그 파일을 만진다는 사실이 겹침 축에서 통째로 사라진다.

func igGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(c.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func igWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("디렉토리 생성 실패: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("파일 쓰기 실패(%s): %v", path, err)
	}
}

// TestDropIgnoredPathsAsksTheTreeThatOwnsThePath 는 무시 판정을 **그 경로가 든 트리**가
// 내리는지 단정한다.
//
// ★ 왜 이 축이 전부인가. 주 저장소의 `.git/info/exclude` 에 `.flightdeck/` 이 있어서,
// 워크트리 안의 **진짜 소스**가 주 저장소 기준으로는 전부 "무시됨"으로 나온다. 실측:
// 원장의 무시 판정 경로 13종 중 진짜 스크래치는 3종뿐이고 나머지 10종은
// `plugins/flightdeck/DESIGN.md` · `internal/service/itempaths.go` 같은 추적 대상이었다.
// 주 저장소 한 자리에서 일괄로 걸렀다면 그 10종이 조용히 사라졌을 것이다.
func TestDropIgnoredPathsAsksTheTreeThatOwnsThePath(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("임시 경로 해석 실패: %v", err)
	}
	main := filepath.Join(root, "proj")
	igGit(t, root, "init", "-q", main)
	igWrite(t, filepath.Join(main, ".gitignore"), "scratch/\n")
	igGit(t, main, "add", "-A")
	igGit(t, main, "commit", "-q", "-m", "init")

	// 주 저장소는 wt/ 를 통째로 무시한다 — 실물의 `.flightdeck/` 과 같은 모양이다.
	igWrite(t, filepath.Join(main, ".git", "info", "exclude"), "wt/\n")
	wt := filepath.Join(main, "wt", "feat")
	igGit(t, main, "worktree", "add", "-q", wt, "-b", "feat")

	src := filepath.Join(wt, "internal", "real.go")
	igWrite(t, src, "package internal\n")
	scratch := filepath.Join(wt, "scratch", "progress.md")
	igWrite(t, scratch, "메모\n")
	mainSrc := filepath.Join(main, "cmd", "main.go")
	igWrite(t, mainSrc, "package main\n")
	mainScratch := filepath.Join(main, "scratch", "notes.md")
	igWrite(t, mainScratch, "메모\n")

	got := DropIgnoredPaths(nil, []string{src, scratch, mainSrc, mainScratch})
	has := func(p string) bool {
		for _, g := range got {
			if g == p {
				return true
			}
		}
		return false
	}

	// ① 워크트리 안의 진짜 소스는 남는다. 주 저장소 기준으로는 무시 대상인데도.
	if !has(src) {
		t.Fatalf("워크트리 안의 추적 대상이 사라졌다: %q\n"+
			"주 저장소가 wt/ 를 무시한다고 그 트리의 소스를 지우면 그 세션은 겹침 축에서 없어진다\n"+
			"남은 것: %v", src, got)
	}
	// ② 그 트리가 무시하는 스크래치는 빠진다. 이것이 이 항목의 본 증상이다.
	if has(scratch) {
		t.Fatalf("그 트리가 무시하는 스크래치가 남았다: %q\n남은 것: %v", scratch, got)
	}
	// ③ 주 트리 쪽도 같은 규칙이 적용된다.
	if !has(mainSrc) {
		t.Fatalf("주 트리의 추적 대상이 사라졌다: %v", got)
	}
	if has(mainScratch) {
		t.Fatalf("주 트리가 무시하는 스크래치가 남았다: %v", got)
	}
}

// TestDropIgnoredPathsKeepsWhatItCannotJudge 는 **판정 못 한 것을 지우지 않는다**를
// 단정한다. 이 필터의 fail-open 방향이다.
//
// git 이 없거나, 저장소 밖이거나, 디렉토리가 사라졌으면 그 경로는 그대로 남는다.
// 여기서 "모르면 버린다"로 기울면 훅이 조용히 발자국을 잃고, 잃었다는 사실이
// 어느 화면에도 안 뜬다 — 훅 전체가 fail-open 인 것과 같은 이유다.
func TestDropIgnoredPathsKeepsWhatItCannotJudge(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("임시 경로 해석 실패: %v", err)
	}
	outside := filepath.Join(root, "no-repo", "loose.txt")
	igWrite(t, outside, "x\n")
	missing := filepath.Join(root, "gone", "vanished.go")

	got := DropIgnoredPaths(nil, []string{outside, missing})
	if len(got) != 2 {
		t.Fatalf("판정할 수 없는 경로를 버렸다: %v (기대 2건)\n"+
			"지워진 발자국은 어느 화면에도 안 나타난다 — 모를 때는 남겨야 한다", got)
	}
}

// TestDropIgnoredPathsIsIdentityOnEmpty 는 빈 입력에 git 을 안 부른다를 겸한다.
func TestDropIgnoredPathsIsIdentityOnEmpty(t *testing.T) {
	if got := DropIgnoredPaths(nil, nil); got != nil {
		t.Fatalf("빈 입력에 %v 를 냈다", got)
	}
	if got := DropIgnoredPaths(nil, []string{}); len(got) != 0 {
		t.Fatalf("빈 입력에 %v 를 냈다", got)
	}
}

// TestGroupPathsByDirIsStable 은 순수 함수를 직접 단정한다.
func TestGroupPathsByDirIsStable(t *testing.T) {
	got := GroupPathsByDir([]string{"/a/x.go", "/a/y.go", "/b/z.go", "  ", ""})
	if len(got) != 2 {
		t.Fatalf("묶음이 %d개다: %v", len(got), got)
	}
	if len(got["/a"]) != 2 || len(got["/b"]) != 1 {
		t.Fatalf("묶음이 틀렸다: %v", got)
	}
	// 상대경로는 묶을 좌표가 없다 — 훅은 절대경로를 준다. 안 묶고 버리지도 않는다.
	if got := GroupPathsByDir([]string{"rel/x.go"}); len(got) != 0 {
		t.Fatalf("상대경로를 묶었다: %v", got)
	}
}

// TestKeepUnignoredDropsOnlyExactMatches 는 순수 함수를 직접 단정한다.
func TestKeepUnignoredDropsOnlyExactMatches(t *testing.T) {
	in := []string{"/a/x.go", "/a/y.go", "/b/z.go"}
	got := KeepUnignored(in, map[string]bool{"/a/y.go": true})
	if len(got) != 2 || got[0] != "/a/x.go" || got[1] != "/b/z.go" {
		t.Fatalf("순서나 내용이 틀렸다: %v", got)
	}
	// 접두 일치로 지우면 안 된다 — "/a" 가 무시돼도 "/a/x.go" 를 여기서 지우지 않는다.
	// 그 판정은 git 이 경로마다 내리고 이 함수는 그 답을 그대로 쓴다.
	if got := KeepUnignored(in, map[string]bool{"/a": true}); len(got) != 3 {
		t.Fatalf("접두 일치로 지웠다: %v", got)
	}
}
