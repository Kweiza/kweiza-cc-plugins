package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestIdentityGateAxesNameTheDeadGate 는 **죽은 관문이 이름으로 불리는지** 문다.
//
// ★ 이 시험이 이 축의 존재 이유 그 자체다. 2026-08-30 에 `core.hooksPath` 가 남의 머신
// 절대경로를 가리켜 관문 넷이 통째로 안 돌았는데 **어느 화면에도 안 떴다.** 그러므로
// "축이 있다"로는 부족하다 — 죽었을 때 **✗ 와 처방이 나오는지**를 물어야 한다.
func TestIdentityGateAxesNameTheDeadGate(t *testing.T) {
	// 남의 머신 절대경로를 가리키는 상태 — 2026-08-30 의 실제 모양이다.
	st := IdentityGateState{
		InRepo:    true,
		HooksPath: "/home/aaron/cdo-dev/kweiza-cc-plugins/.githooks",
		Resolved:  "/home/aaron/cdo-dev/kweiza-cc-plugins/.githooks",
		DirExists: false,
	}
	axes := IdentityGateAxes(st)
	if len(axes) == 0 {
		t.Fatal("축이 0건이다 — 관문이 죽었는데 화면이 조용하다. 이 항목이 고치려던 그 상태다")
	}
	where := axes[0]
	if where.Observed {
		t.Fatalf("hooksPath 가 없는 자리를 가리키는데 ✓ 로 났다(%q) — git 은 이때 아무 말 없이 "+
			"커밋을 성공시킨다. 무출력을 통과로 읽는 그 병을 doctor 가 그대로 옮긴 것이다", where.Value)
	}
	// ★ **처방이 본문에 있어야 한다.** "✗" 만으로는 사람이 무엇을 할지 모른다.
	//
	// ★★ **"core.hooksPath 가 들어 있나"로 물으면 안 된다.** 그 문자열은 바로 옆 분기
	// (미설정 + 자리 없음)의 문장에도 있어서, 이 분기를 통째로 죽여도 옆이 대신 잡고
	// 시험은 초록이 된다 — 변이로 실제 확인했다. 그러면 재려던 주장이 미검증인 채
	// 통과한다. 그래서 **이 분기에만 있는 것 둘**을 문다: 고칠 **명령**과, 사람이
	// 자기 상태를 알아볼 **그 경로 자체**.
	if !strings.Contains(where.Detail, "git config core.hooksPath") {
		t.Errorf("죽은 관문 줄에 **고칠 명령**이 없다 — ✗ 만 보고는 무엇을 할지 모른다: %q",
			where.Detail)
	}
	if !strings.Contains(where.Detail, st.HooksPath) {
		t.Errorf("어느 자리를 가리키고 있는지 안 말한다 — 남의 머신 경로가 박힌 것이 "+
			"2026-08-30 의 실제 원인이었고, 그 문자열을 보는 순간 사람이 안다: %q", where.Detail)
	}
}

// TestIdentityGateAxesCatchNonExecutableHooks 는 **실행 권한이 없는 훅**을 잡는지 문다.
// git 은 그때 조용히 건너뛴다 — 같은 침묵의 두 번째 경로다.
func TestIdentityGateAxesCatchNonExecutableHooks(t *testing.T) {
	st := IdentityGateState{
		InRepo: true, HooksPath: ".githooks", Resolved: "/x/.githooks", DirExists: true,
		Hooks: []identityHookFile{
			{Name: "pre-commit", Executable: false, CallsIdent: true},
			{Name: "pre-push", Executable: true, CallsIdent: true},
		},
		ExpectIdent: "A <a@b>", ActualIdent: "A <a@b>",
	}
	axes := IdentityGateAxes(st)
	found := false
	for _, ax := range axes {
		if ax.Name == "신원 관문 훅" {
			found = true
			if ax.Observed {
				t.Errorf("실행 권한 없는 관문 훅이 있는데 ✓ 로 났다(%q) — git 은 조용히 건너뛴다", ax.Value)
			}
			if !strings.Contains(ax.Detail, "pre-commit") {
				t.Errorf("어느 파일인지 안 말한다: %q", ax.Detail)
			}
		}
	}
	if !found {
		t.Fatal("훅 축 자체가 없다")
	}
}

// TestIdentityGateAxesCatchDriftedExpectation 은 기대 신원이 **자리마다 갈린** 상태를 잡는지 문다.
func TestIdentityGateAxesCatchDriftedExpectation(t *testing.T) {
	st := IdentityGateState{
		InRepo: true, HooksPath: ".githooks", Resolved: "/x/.githooks", DirExists: true,
		Hooks: []identityHookFile{
			{Name: "pre-push", Executable: true, CallsIdent: true, Expect: "옛 이름 <old@x>"},
		},
		ExpectIdent: "새 이름 <new@x>", ActualIdent: "새 이름 <new@x>",
	}
	for _, ax := range IdentityGateAxes(st) {
		if ax.Name == "기대 신원 일관" {
			if ax.Observed {
				t.Error("두 자리의 기대 신원이 다른데 ✓ 로 났다 — 앞 관문은 통과시키고 push 에서만 막힌다")
			}
			return
		}
	}
	t.Fatal("표류 축이 안 나왔다")
}

// TestIdentityHooksActuallyRejectWrongIdentity 는 **양방향을 실물로** 증명한다.
//
// ★ 존재만 재고 끝내지 않는다. 2026-08-30 의 교훈이 정확히 그것이다 — 설정을 고친 것으로
// 끝내지 않고 틀린 신원이 **거부되는지**와 옳은 신원이 **통과하는지**를 둘 다 봤다.
//
// ★ **진짜 저장소에서 커밋하지 않는다.** t.TempDir() 에 저장소를 새로 만들고 이 레포의
// `.githooks` 를 복사해서 거기서 증명한다.
func TestIdentityHooksActuallyRejectWrongIdentity(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 이 없다")
	}
	root := repoRootFromCmdFd(t)
	src := filepath.Join(root, ".githooks")
	if _, err := os.Stat(src); err != nil {
		t.Skipf(".githooks 가 없다: %v", err)
	}

	dir := t.TempDir()
	run := func(t *testing.T, args ...string) (string, error) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+dir)
		b, err := cmd.CombinedOutput()
		return string(b), err
	}
	if out, err := run(t, "init", "-q"); err != nil {
		t.Fatalf("init 실패: %v\n%s", err, out)
	}

	// 훅을 복사하고 관문을 건다.
	dst := filepath.Join(dir, ".githooks")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if fi, err := e.Info(); err == nil && fi.Mode()&0o111 != 0 {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), b, mode); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := run(t, "config", "core.hooksPath", ".githooks"); err != nil {
		t.Fatalf("hooksPath 설정 실패: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, "add", "a.txt"); err != nil {
		t.Fatalf("add 실패: %v\n%s", err, out)
	}

	head := func() string {
		out, _ := run(t, "rev-parse", "HEAD")
		return strings.TrimSpace(out)
	}
	before := head()

	// ── ① 틀린 신원 → 거부되고 **HEAD 가 안 움직인다** ──────────────────────
	out, err := run(t, "-c", "user.name=Nobody", "-c", "user.email=x@x.com",
		"commit", "-q", "-m", "틀린 신원")
	if err == nil {
		t.Fatalf("틀린 신원으로 커밋이 **성공했다** — 관문이 죽어 있다.\n%s", out)
	}
	if !strings.Contains(out, "거부") {
		t.Errorf("거부 메시지가 없다 — 다른 이유로 실패했을 수 있다:\n%s", out)
	}
	// ★ 거부 메시지만 보고 "막혔다"로 읽지 않는다. HEAD 를 본다.
	if head() != before {
		t.Fatalf("거부됐다는데 HEAD 가 움직였다(%s → %s)", before, head())
	}

	// ── ② 옳은 신원 → 통과한다 ─────────────────────────────────────────────
	//
	// 기대 신원은 `_identity.sh` 에서 읽는다 — 시험에 상수를 또 박으면 그것이 세 번째
	// 자리가 되고, 이 파일이 잡으려는 표류를 이 파일이 만든다.
	b, err := os.ReadFile(filepath.Join(dst, "_identity.sh"))
	if err != nil {
		t.Fatal(err)
	}
	m := identExpectRe.FindSubmatch(b)
	if m == nil {
		t.Fatal("_identity.sh 에서 FD_EXPECT_IDENT 를 못 읽었다")
	}
	want := string(m[1])
	name, mail, ok := strings.Cut(want, " <")
	if !ok {
		t.Fatalf("기대 신원 형식이 예상과 다르다: %q", want)
	}
	mail = strings.TrimSuffix(mail, ">")

	out, err = run(t, "-c", "user.name="+name, "-c", "user.email="+mail,
		"commit", "-q", "-m", "옳은 신원")
	if err != nil {
		t.Fatalf("옳은 신원인데 거부됐다 — 관문이 과하게 문다:\n%s", out)
	}
	if head() == before {
		t.Fatal("통과했다는데 HEAD 가 안 움직였다")
	}
}
