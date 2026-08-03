package legacy

import (
	"os"
	"path/filepath"
	"testing"
)

// 되쓰기 대상 판정. 이 도구 전체가 "이관 원본은 읽기 전용"이라는 규율 위에 서 있는데,
// 그 규율을 깨는 가장 짧은 경로가 도구 자신 안에 열려 있었다 —
// `--out` 을 필수로 만든 앞선 가드는 "기본값이 원본이 되는 것"만 막았다.
func TestJudgeOutTarget(t *testing.T) {
	cases := []struct {
		name                     string
		exists, inGit, hasLegacy bool
		entries                  int
		wantOK                   bool
		wantCode                 string
		wantForceCanOverride     bool
	}{
		{"없는 자리", false, false, false, 0, true, "empty", false},
		{"빈 자리", true, false, false, 0, true, "empty", false},
		{"항목이 있다", true, false, false, 3, false, "not-empty", true},
		{"레거시가 이미 있다", true, false, true, 9, false, "has-legacy", true},
		{"git 작업 트리다", true, true, false, 0, false, "git-worktree", false},
		// 표 밖: 여러 축이 겹치면 **가장 되돌리기 어려운 축**이 이겨야 한다.
		{"git + 레거시", true, true, true, 400, false, "git-worktree", false},
		{"git + 비어있음", true, true, false, 0, false, "git-worktree", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := JudgeOutTarget(c.exists, c.inGit, c.hasLegacy, c.entries)
			if v.OK != c.wantOK || v.Code != c.wantCode {
				t.Errorf("OK=%v code=%q — 기대 OK=%v code=%q", v.OK, v.Code, c.wantOK, c.wantCode)
			}
			if v.Reason == "" {
				t.Error("사유가 비었다 — 사유가 없으면 사람이 --force 를 무턱대고 붙인다")
			}
			if got := ForceAllows(v.Code); got != c.wantForceCanOverride {
				t.Errorf("ForceAllows(%q)=%v — 기대 %v", v.Code, got, c.wantForceCanOverride)
			}
		})
	}
}

// ★ git 작업 트리는 --force 로도 못 뒤집는다.
// 탈출구를 만들면 그것이 곧 사고 경로가 되고, 이 축은 되돌릴 방법이 아예 없다
// (.claude/ 는 gitignore 라 git checkout 이 원복하지 못한다).
func TestForceCannotOverrideGitWorktree(t *testing.T) {
	if ForceAllows("git-worktree") {
		t.Fatal("git 작업 트리를 --force 로 덮을 수 있다 — 되돌릴 방법이 없는 축에 탈출구를 만들면 안 된다")
	}
}

func TestInspectOutTargetSeesAncestorGit(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	// 대조 전제: 대상 자체에는 .git 이 없다. 있으면 이 시험이 조상 탐색을 안 본다.
	if _, err := os.Stat(filepath.Join(deep, ".git")); err == nil {
		t.Fatal("전제가 깨졌다 — 대상에 .git 이 있다")
	}
	_, inGit, _, _, err := InspectOutTarget(deep)
	if err != nil {
		t.Fatal(err)
	}
	if !inGit {
		t.Error("조상의 .git 을 못 봤다 — 레포 안 하위 디렉토리를 조준하면 그대로 덮인다")
	}
}

func TestInspectOutTargetCountsAndFindsLegacy(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude", "handoffs"), 0o755); err != nil {
		t.Fatal(err)
	}
	exists, _, hasLegacy, n, err := InspectOutTarget(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !exists || !hasLegacy || n != 1 {
		t.Errorf("exists=%v hasLegacy=%v entries=%d — 기대 true/true/1", exists, hasLegacy, n)
	}
}
