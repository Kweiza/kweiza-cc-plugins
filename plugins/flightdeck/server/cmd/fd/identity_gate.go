package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kweiza/flightdeck/internal/service"
)

// ── 신원 관문 축 ────────────────────────────────────────────────────────────
//
// ★ 이 축이 있는 이유는 2026-08-30 사건이다. `core.hooksPath` 가 **남의 머신의 절대경로**
// (`/home/aaron/cdo-dev/...`)를 가리켜 이 저장소의 신원 관문 넷이 통째로 안 돌았는데,
// **어느 화면에도 안 떴다.** git 은 hooksPath 가 없는 디렉토리를 가리켜도 아무 말 없이
// 커밋을 성공시킨다 — 몇 달 동안 조용했고, 그동안 신원이 안 걸린 커밋이 나갈 수 있었다.
//
// 이 저장소가 반복해서 만난 모양이다: gofmt 가 모듈 밖에서 빈 디렉토리를 검사하고 조용히
// 통과하는 그것, matcher 표류로 훅이 안 도는데 오류가 없는 그것. **관문의 무출력은
// 통과가 아니다** — 그래서 doctor 가 이 축을 이름으로 부른다.
//
// ★ **클라이언트에서 돈다.** 서버는 컨테이너라 이 저장소의 `.git/config` 도 `git var` 도
// 못 본다. 항목 본문의 `paths` 는 `internal/service/doctor.go` 를 지목했는데 **그 자리는
// 틀렸다** — codex 축이 같은 이유로 `cmd/fd` 에 사는 것과 같다(`codex.go` 머리말).
//
// ★ **머신마다 다르고 추적도 안 된다.** `core.hooksPath` 는 `.git/config` 의 로컬 설정이라
// 커밋으로 고칠 수 없다. 저장소에 고침을 실어 해결되는 문제가 아니라는 것이 이 축의
// 존재 이유다 — 각 머신이 자기 것을 봐야 하고, 그 화면이 여기다.

// identityHookFile 은 관문 훅 파일 하나의 관측이다.
type identityHookFile struct {
	Name       string
	Executable bool
	CallsIdent bool   // 신원을 실제로 무는가(check_ident 호출 또는 상수 자체 보유)
	Expect     string // 이 파일이 **자기 안에** 박은 기대 신원("" 면 안 박았다)
}

// IdentityGateState 는 관측 결과다. **판정은 안 한다** — 그 가름이 CodexState 와 같은 이유다.
type IdentityGateState struct {
	InRepo      bool
	HooksPath   string // core.hooksPath 원문. "" 면 미설정
	Resolved    string // 트리 기준으로 푼 자리
	DirExists   bool
	DirErr      string
	Hooks       []identityHookFile
	ExpectIdent string // _identity.sh 의 FD_EXPECT_IDENT
	ActualIdent string // git var GIT_AUTHOR_IDENT 에서 타임스탬프를 뗀 것
	ActualErr   string
}

// identExpectRe 는 `_identity.sh` 에서 기대 신원을 뽑는다.
//
// ★ 셸을 실행해서 읽지 않는다. doctor 가 저장소의 스크립트를 source 하면 진단이 임의
// 코드 실행이 된다 — 재려던 것보다 위험한 것을 들인다.
var identExpectRe = regexp.MustCompile(`(?m)^\s*FD_EXPECT_IDENT\s*=\s*"([^"]*)"`)

// identStampRe 는 `git var` 가 붙이는 " <초> <시간대>" 꼬리다(`_identity.sh` 와 같은 규칙).
var identStampRe = regexp.MustCompile(` [0-9]+ [-+][0-9]+$`)

// observeIdentityGate 는 사실만 모은다.
func (a *App) observeIdentityGate() IdentityGateState {
	var st IdentityGateState

	top, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return st // 저장소가 아니다 — 잴 것이 없다(없는 것이 정상이다)
	}
	st.InRepo = true
	root := strings.TrimSpace(string(top))

	if v, err := exec.Command("git", "config", "core.hooksPath").Output(); err == nil {
		st.HooksPath = strings.TrimSpace(string(v))
	}

	// ★ 상대경로는 **작업 트리 꼭대기** 기준으로 푼다 — git 이 그렇게 풀기 때문이고,
	// 그래서 `.githooks` 같은 상대값이 워크트리에서도 산다(2026-08-30 실측).
	switch {
	case st.HooksPath == "":
		st.Resolved = filepath.Join(root, ".git", "hooks")
	case filepath.IsAbs(st.HooksPath):
		st.Resolved = st.HooksPath
	default:
		st.Resolved = filepath.Join(root, st.HooksPath)
	}

	ents, derr := os.ReadDir(st.Resolved)
	if derr != nil {
		st.DirErr = derr.Error()
	} else {
		st.DirExists = true
		for _, e := range ents {
			if e.IsDir() || strings.HasSuffix(e.Name(), ".sample") || strings.HasPrefix(e.Name(), "_") {
				continue
			}
			h := identityHookFile{Name: e.Name()}
			if fi, err := e.Info(); err == nil {
				h.Executable = fi.Mode()&0o111 != 0
			}
			if b, err := os.ReadFile(filepath.Join(st.Resolved, e.Name())); err == nil {
				body := string(b)
				// ★ **두 모양을 다 센다.** `pre-commit`·`pre-merge-commit` 은 `_identity.sh` 를
				// source 해서 `check_ident` 를 부르고, `pre-push` 는 상수를 **자기 안에 다시
				// 박는다**. `check_ident` 만 보면 마지막 관문(rebase·cherry-pick 으로 만든
				// 커밋을 잡는 유일한 자리)이 화면에서 통째로 빠진다 — 실제로 처음에 빠졌다.
				h.CallsIdent = strings.Contains(body, "check_ident") ||
					strings.Contains(body, "FD_EXPECT_IDENT")
				if m := identExpectRe.FindStringSubmatch(body); m != nil {
					h.Expect = m[1]
				}
			}
			st.Hooks = append(st.Hooks, h)
		}
	}

	if b, err := os.ReadFile(filepath.Join(st.Resolved, "_identity.sh")); err == nil {
		if m := identExpectRe.FindSubmatch(b); m != nil {
			st.ExpectIdent = string(m[1])
		}
	}

	if v, err := exec.Command("git", "var", "GIT_AUTHOR_IDENT").Output(); err != nil {
		st.ActualErr = err.Error()
	} else {
		st.ActualIdent = identStampRe.ReplaceAllString(strings.TrimSpace(string(v)), "")
	}
	return st
}

// IdentityGateAxes 는 순수 판정이다.
//
// ★ 미관측 줄에 「관측 안 됨」을 안 붙인다(codex 절과 같은 규율): 여기서 ✗ 는 '못 쟀다'가
// 아니라 **'관문이 죽어 있다'** 이고, 그 둘을 같은 말로 덮으면 잡으려던 침묵이 화면에서
// 다시 침묵이 된다.
func IdentityGateAxes(st IdentityGateState) []service.DoctorAxis {
	if !st.InRepo {
		return nil // 저장소 밖이면 이 절 자체를 안 낸다
	}
	axes := make([]service.DoctorAxis, 0, 4)

	// ── ① 관문이 걸린 자리 ──────────────────────────────────────────────────
	where := service.DoctorAxis{Name: "신원 관문 자리"}
	switch {
	case !st.DirExists && st.HooksPath != "":
		where.Detail = "core.hooksPath 가 " + clip(st.HooksPath, 80) + " 인데 그 자리가 **없다**(" +
			clip(st.Resolved, 100) + ") — 관문 전부가 안 돈다. git 은 이때 아무 말도 안 하고 커밋을 " +
			"성공시킨다. 고치려면: git config core.hooksPath .githooks"
	case !st.DirExists:
		where.Detail = "core.hooksPath 가 미설정이고 " + clip(st.Resolved, 100) + " 도 없다 — 관문이 없다"
	case st.HooksPath == "":
		where.Observed, where.Value = true, st.Resolved+" (core.hooksPath 미설정 — .git/hooks 기본자리)"
	default:
		where.Observed, where.Value = true, st.HooksPath+" → "+st.Resolved
	}
	axes = append(axes, where)

	// ── ② 훅 파일과 **실행 권한** ───────────────────────────────────────────
	//
	// ★ 권한이 없으면 git 은 **조용히 건너뛴다.** 이번 사건에서는 있었지만, 같은 침묵의
	// 두 번째 경로라 함께 잰다.
	files := service.DoctorAxis{Name: "신원 관문 훅"}
	var gate, noexec []string
	for _, h := range st.Hooks {
		if !h.CallsIdent {
			continue
		}
		gate = append(gate, h.Name)
		if !h.Executable {
			noexec = append(noexec, h.Name)
		}
	}
	switch {
	case len(gate) == 0:
		files.Detail = "이 자리에 신원을 무는 훅이 **0건**이다(check_ident 를 부르는 파일이 없다) — " +
			"파일이 있어도 신원은 안 걸린다"
	case len(noexec) > 0:
		files.Detail = "실행 권한이 없는 관문 훅: " + strings.Join(noexec, ", ") +
			" — git 은 이때 **조용히 건너뛴다**. chmod +x 로 살려라"
	default:
		files.Observed, files.Value = true, strings.Join(gate, ", ")+" (전부 실행 가능)"
	}
	axes = append(axes, files)

	// ── ③·④ 기대 신원과 지금 신원 ─────────────────────────────────────────
	ident := service.DoctorAxis{Name: "신원 일치"}
	switch {
	case st.ExpectIdent == "":
		ident.Detail = "_identity.sh 에서 FD_EXPECT_IDENT 를 못 읽었다(" + clip(st.Resolved, 100) +
			") — 무엇을 기대하는지 모르므로 이 축은 판정 불가다"
	case st.ActualErr != "":
		ident.Detail = "git var GIT_AUTHOR_IDENT 를 못 읽었다: " + clip(st.ActualErr, 120)
	case st.ActualIdent != st.ExpectIdent:
		ident.Detail = "지금 신원이 다르다 — " + clip(st.ActualIdent, 80) + " · 기대 " +
			clip(st.ExpectIdent, 80) + " — 이 상태로 커밋하면 관문이 거부한다(관문이 살아 있다면)"
	default:
		ident.Observed, ident.Value = true, st.ActualIdent
	}
	axes = append(axes, ident)

	// ── ⑤ 기대 신원이 **두 자리에 산다** ────────────────────────────────────
	//
	// ★ `_identity.sh` 가 한 벌, `pre-push` 가 자기 안에 또 한 벌. 이 저장소의 규율은
	// "같은 판정이 두 자리에 살면 반드시 표류한다"이고, 표류하면 **앞 관문은 통과시키고
	// 마지막 관문만 거부하는** 상태가 된다 — 그때 사람은 커밋을 다 만든 뒤 push 에서
	// 막히고, 원인이 어디인지 화면에 없다. 그래서 갈렸는지를 여기서 본다.
	var drifted []string
	for _, h := range st.Hooks {
		if h.Expect != "" && st.ExpectIdent != "" && h.Expect != st.ExpectIdent {
			drifted = append(drifted, h.Name+"="+clip(h.Expect, 60))
		}
	}
	if len(drifted) > 0 || countExpectCopies(st) > 1 {
		drift := service.DoctorAxis{Name: "기대 신원 일관"}
		if len(drifted) > 0 {
			drift.Detail = "기대 신원이 자리마다 다르다 — _identity.sh=" + clip(st.ExpectIdent, 60) +
				" 인데 " + strings.Join(drifted, ", ") +
				" — 앞 관문은 통과시키고 마지막 관문만 거부하는 상태다"
		} else {
			drift.Observed = true
			drift.Value = "두 자리(_identity.sh · 훅 안 상수)가 같은 값이다 — " + clip(st.ExpectIdent, 80)
		}
		axes = append(axes, drift)
	}

	return axes
}

// countExpectCopies 는 기대 신원이 **몇 자리에** 적혀 있는지 센다.
//
// 둘 이상이면 표류가 가능한 상태이고, 그 사실 자체를 화면에 둔다 — 지금 값이 같아도
// 다음에 한쪽만 고치는 사람이 나온다.
func countExpectCopies(st IdentityGateState) int {
	n := 0
	if st.ExpectIdent != "" {
		n++ // _identity.sh
	}
	for _, h := range st.Hooks {
		if h.Expect != "" {
			n++
		}
	}
	return n
}
