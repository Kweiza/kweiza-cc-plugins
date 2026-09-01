package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/service"
)

// codex 창에 `fd` 그 자체가 없다.
//
// ★ 실측(선행 판단 01M1D9W33…): `InstallCodex` 가 까는 것은 래퍼(`fd-hook`)와
// `hooks.json` 둘뿐이고, `env -i HOME=$HOME zsh -lc 'command -v fd'` → `NOFD` 다.
// 그래서 codex 창이 잃는 것은 「MCP 표면」이 아니라 **`fd` 그 자체**다 — 보드도 큐도
// 판단도 부를 방법이 없다.
//
// ★ 그리고 이 항목에는 선행이 있다(identity-harness-nesting-gate). 맨손 `fd` 를 까는 것은
// **`--harness` 없는 호출 경로를 여는 일**이라, 중첩 기동의 봉인이 먼저 서 있어야 한다.
// 그 봉인은 같은 묶음에서 이미 섰다.

func TestCodexCLIPathSitsBesideTheWrapper(t *testing.T) {
	home := "/home/u"
	cli, wrapper := CodexCLIPath(home), CodexWrapperPath(home)
	if filepath.Dir(cli) != filepath.Dir(wrapper) {
		t.Fatalf("CLI(%s)와 래퍼(%s)가 다른 디렉토리다 — PATH 안내가 두 벌이 된다", cli, wrapper)
	}
	if filepath.Base(cli) != "fd" {
		t.Fatalf("설치되는 이름이 %q 다 — 사람이 치는 것은 `fd` 다", filepath.Base(cli))
	}
}

// doctor 가 **셋을 각각** 잰다: 깔렸나 · PATH 에 있나 · 다른 fd 가 가리나.
//
// ★ 셋을 하나로 접으면 안 된다. "fd 가 안 된다"의 원인이 셋이고 처방이 전부 다르다 —
// 안 깔림(설치해라) · PATH 밖(PATH 를 고쳐라) · 이름 충돌(fd-find 가 먼저 잡힌다).
func TestCodexAxesReportTheCLIAxis(t *testing.T) {
	base := CodexState{Present: true, Home: "/h/.codex", HooksPath: "/h/.codex/hooks.json"}

	t.Run("안 깔렸다", func(t *testing.T) {
		st := base
		st.CLIPath, st.CLIOK = "/h/.local/bin/fd", false
		if !axesMention(CodexAxes(st), "fd") {
			t.Fatal("doctor 가 fd 축을 아예 안 낸다")
		}
		if !axesMentionDetail(CodexAxes(st), "없다") {
			t.Fatalf("안 깔린 사실을 안 말한다:\n%s", axesDump(CodexAxes(st)))
		}
	})

	t.Run("깔렸지만 PATH 밖", func(t *testing.T) {
		st := base
		st.CLIPath, st.CLIOK, st.CLIOnPath = "/h/.local/bin/fd", true, false
		if !axesMentionDetail(CodexAxes(st), "PATH") {
			t.Fatalf("PATH 밖이라는 사실을 안 말한다 — 깔아 놓고 안 되는 그 화면이다:\n%s",
				axesDump(CodexAxes(st)))
		}
	})

	t.Run("남의 fd 가 가린다", func(t *testing.T) {
		st := base
		st.CLIPath, st.CLIOK, st.CLIOnPath = "/h/.local/bin/fd", true, true
		st.CLIShadowedBy = "/usr/bin/fd"
		if !axesMentionDetail(CodexAxes(st), "/usr/bin/fd") {
			t.Fatalf("다른 fd 가 먼저 잡히는 사실을 안 말한다(fd-find 충돌):\n%s",
				axesDump(CodexAxes(st)))
		}
	})
}

// 설치가 실제로 파일을 놓는다 — 그리고 **래퍼와 같은 스크립트**여야 한다.
//
// ★ 같은 스크립트인 것이 핵심이다: 그 스크립트가 설치본 중 **가장 높은 판**을 골라
// exec 하므로, 판올림이 자동으로 따라온다. 버전 박힌 경로를 심으면 판올림마다 낡는다.
func TestInstallCodexAlsoInstallsTheCLI(t *testing.T) {
	home := t.TempDir()
	// codex 가 있는 것으로 보이게 한다 — 없으면 설치가 통째로 건너뛴다.
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatalf("가짜 ~/.codex 생성 실패: %v", err)
	}

	h := newHarness(t)
	env := map[string]string{}
	for k, v := range h.env {
		env[k] = v
	}
	env["HOME"] = home
	if code, out := h.runEnv(env, "", "setup", "--install-codex"); code != 0 {
		t.Fatalf("setup --install-codex 가 %d 로 끝났다:\n%s", code, out)
	}

	cli := CodexCLIPath(home)
	got, err := os.ReadFile(cli)
	if err != nil {
		t.Fatalf("fd 가 안 깔렸다(%s): %v", cli, err)
	}
	if string(got) != string(codexWrapperScript) {
		t.Fatal("깔린 fd 가 래퍼 스크립트와 다르다 — 판올림 추종이 깨진다")
	}
	fi, err := os.Stat(cli)
	if err != nil || fi.Mode()&0o111 == 0 {
		t.Fatalf("깔린 fd 에 실행 권한이 없다: %v (mode=%v)", err, fi.Mode())
	}
}

// ── 시험 헬퍼 ────────────────────────────────────────────────────────────────

// axesMention 은 축 **이름**에 그 말이 있나다 — 축이 아예 있는지를 본다.
func axesMention(axes []service.DoctorAxis, want string) bool {
	for _, a := range axes {
		if strings.Contains(a.Name, want) {
			return true
		}
	}
	return false
}

// axesMentionDetail 은 축의 **사람이 읽는 부분**에 그 말이 있나다.
// Detail·Value 를 함께 보는 이유: 어느 쪽에 싣는지는 구현의 선택이고,
// 이 시험이 재려는 것은 "그 사실이 화면에 있나" 하나다.
func axesMentionDetail(axes []service.DoctorAxis, want string) bool {
	for _, a := range axes {
		if strings.Contains(a.Detail, want) || strings.Contains(a.Value, want) {
			return true
		}
	}
	return false
}

func axesDump(axes []service.DoctorAxis) string {
	var b strings.Builder
	for _, a := range axes {
		b.WriteString("  · " + a.Name + " | " + a.Value + " | " + a.Detail + "\n")
	}
	return b.String()
}
