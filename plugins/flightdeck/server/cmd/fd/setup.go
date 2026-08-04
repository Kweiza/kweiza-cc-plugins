package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// 셋업 판정 — **관측과 판정을 가른다.**
//
// 관측(OS·go 버전·docker 유무·설정 출처·healthz)은 부수효과 있는 자리에서 하고,
// 여기 있는 것은 그 값만 보는 순수 함수다. 그래야 조합 전체를 시험이 표로 돌 수 있다.
// 이 레포의 다른 판정기(JudgeOffline·PlanMigration·GateTool)가 전부 같은 모양이다.

// MinGoVersion 은 이 소스를 빌드할 수 있는 최소 Go 다. go.mod 의 값과 같아야 한다.
//
// ★ 상수로 두는 이유가 실측에 있다: Ubuntu apt 의 golang-go 후보가 **1.22** 라
// (2026-08-04 `apt-cache policy golang-go` → `2:1.22~2build1`) 존재 검사만 하면
// 설치는 성공하고 빌드가 깨진다. 그 함정을 이 상수와 GoVersionOK 가 막는다.
const MinGoVersion = "1.25"

// Role 은 이 머신이 서버인가 클라이언트인가다.
type Role string

const (
	RoleServer  Role = "server"
	RoleClient  Role = "client"
	RoleUnknown Role = "unknown"
)

// DetectRole 은 주소만 보고 역할을 정한다. 순수 함수다.
//
// ★ **역할을 저장하지 않는다.** 루프백 주소는 "이 머신이 서버다" 말고 다른 뜻이 될 수 없으므로
// 파생이 성립한다. 파일에 저장하면 의도와 현실이 갈린다 — 저장된 "서버"인데 주소는 원격인
// 상태를 아무도 못 막는다. 파생 가능한 값에 파라미터를 두지 않는 것이 이 레포의 규율이다.
func DetectRole(raw string) Role {
	s := strings.TrimSpace(raw)
	if s == "" {
		return RoleUnknown
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return RoleUnknown
	}
	host := u.Hostname()
	if host == "" {
		return RoleUnknown
	}
	if strings.EqualFold(host, "localhost") {
		return RoleServer
	}
	if ip := net.ParseIP(host); ip != nil {
		// 0.0.0.0 은 "전 인터페이스로 듣는다"라 클라이언트 목적지로는 성립하지 않는다 —
		// 이 머신이 서버라는 뜻으로 읽는 것이 유일하게 말이 된다.
		if ip.IsLoopback() || ip.IsUnspecified() {
			return RoleServer
		}
		return RoleClient
	}
	return RoleClient
}

var goVersionRe = regexp.MustCompile(`go(\d+)\.(\d+)(?:\.(\d+))?`)

// GoVersionOK 는 `go version` 출력이 이 소스를 빌드할 수 있는지 본다. 순수 함수다.
//
// 사유를 **항상** 낸다. "안 된다"만 알면 무엇을 설치해야 하는지 모른다.
func GoVersionOK(goVersionOutput string) (ok bool, why string) {
	s := strings.TrimSpace(goVersionOutput)
	if s == "" {
		return false, "Go 툴체인이 없다 — 런처가 fd 를 빌드하지 못한다(필요: " + MinGoVersion + " 이상)"
	}
	m := goVersionRe.FindStringSubmatch(s)
	if m == nil {
		return false, fmt.Sprintf("Go 버전을 못 읽었다(%q) — 필요: %s 이상", clip(s, 120), MinGoVersion)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	wantMajor, wantMinor := 1, 25
	got := fmt.Sprintf("%d.%d", major, minor)
	if m[3] != "" {
		got += "." + m[3]
	}
	if major > wantMajor || (major == wantMajor && minor >= wantMinor) {
		return true, "Go " + got + " — 빌드할 수 있다(필요: " + MinGoVersion + " 이상)"
	}
	return false, fmt.Sprintf("Go %s 가 설치돼 있는데 이 소스는 %s 이상이 필요하다 — "+
		"이대로면 빌드가 깨진다", got, MinGoVersion)
}

// SetupState 는 **관측된 값 전부**다. 여기 있는 것은 부수효과 있는 자리에서 잰 것이고,
// 판정은 이 값만 본다.
type SetupState struct {
	OS        string // runtime.GOOS
	Distro    string // linux 일 때만. "ubuntu"·"debian"·"fedora"·"arch"·"" (모름)
	GoOutput  string // `go version` 출력 그대로. 없으면 빈 문자열
	Docker    bool   // docker 가 PATH 에 있나
	Endpoint  Endpoint
	Reachable bool // healthz 가 답했나
}

// SetupStep 은 사람에게 보여 주고 승인을 받을 명령 하나다.
//
// ★ 명령과 **왜 그것인지**를 함께 나른다. 승인을 구하는 화면에 이유가 없으면
// 사용자는 자기 머신에 무엇이 왜 들어가는지 모른 채 예를 누른다.
type SetupStep struct {
	What    string // 무엇을 하는가
	Command string // 실행할 명령 그대로
	Why     string // 왜 필요한가
	Sudo    bool   // 관리자 권한을 요구하는가
}

// SetupPlan 은 판정 결과다.
type SetupPlan struct {
	Role      Role
	Ready     bool // 지금 그대로 쓸 수 있나
	Refused   bool // 이 환경은 지원하지 않는다(Windows)
	Reason    string
	Steps     []SetupStep
	Verify    string // 다 하고 나서 무엇으로 확인하나
	Endpoint  Endpoint
	Reachable bool
}

// PlanSetup 은 관측값만 보고 할 일을 정한다. 순수 함수다.
func PlanSetup(s SetupState) SetupPlan {
	p := SetupPlan{
		Role: DetectRole(s.Endpoint.URL), Endpoint: s.Endpoint, Reachable: s.Reachable,
		Verify: "fd doctor  — 좌표·서버·아웃박스를 한 화면에 낸다",
	}

	// ★ Windows 는 **거절한다.** 셋업 성공을 흉내내면 훅·MCP 가 왜 안 뜨는지 영영 모른다.
	// 진입점 bin/fd 가 bash 스크립트인데, Claude Code 는 훅 shell-form 을 Git Bash 없는
	// Windows 에서 PowerShell 로 돌리고, .mcp.json 스키마에는 OS 분기 필드가 없다.
	// 즉 스킬이 아무리 잘 돌아도 플러그인 자체가 안 뜬다.
	if s.OS == "windows" {
		p.Refused = true
		p.Reason = "이 플러그인은 아직 Windows 에서 동작하지 않는다. 진입점(bin/fd)이 bash 스크립트라 " +
			"훅과 MCP 서버가 뜨지 않는다 — Claude Code 는 Git Bash 가 없는 Windows 에서 훅 명령을 " +
			"PowerShell 로 돌리고, .mcp.json 에는 OS 분기 수단이 없다.\n" +
			"쓰려면 **WSL(Ubuntu) 안에서 Claude Code 를 실행해라** — 그 안에서는 이 셋업이 그대로 돈다."
		p.Verify = ""
		return p
	}

	if ok, why := GoVersionOK(s.GoOutput); !ok {
		p.Steps = append(p.Steps, goInstallStep(s, why))
	}

	switch p.Role {
	case RoleServer:
		if !s.Reachable {
			p.Steps = append(p.Steps, serverStartSteps(s)...)
		}
	case RoleClient:
		if !s.Reachable {
			p.Reason = fmt.Sprintf("클라이언트로 설정돼 있는데 %s 에 못 닿았다(출처: %s). "+
				"주소가 맞는지, 저쪽 서버가 떠 있는지 확인해라 — **이 머신에 서버를 세우는 것은 답이 아니다.**",
				clip(s.Endpoint.URL, 200), clip(s.Endpoint.URLSource, 120))
		}
	case RoleUnknown:
		p.Reason = fmt.Sprintf("서버 주소를 못 읽었다(%q, 출처: %s) — "+
			"`fd setup --url <주소>` 로 정해라", clip(s.Endpoint.URL, 200), clip(s.Endpoint.URLSource, 120))
	}

	p.Ready = len(p.Steps) == 0 && s.Reachable && p.Role != RoleUnknown
	if p.Ready && p.Reason == "" {
		p.Reason = "이미 셋업돼 있다 — 바꿀 것이 없다"
	}
	return p
}

// goInstallStep 은 OS 별 Go 설치 명령이다.
//
// ★ **Ubuntu·Debian 에서 apt 를 쓰지 않는다.** apt 의 golang-go 후보는 1.22 라
// (2026-08-04 실측) 설치는 성공하고 빌드가 깨진다. snap 이 현행 Go 를 준다.
func goInstallStep(s SetupState, why string) SetupStep {
	switch {
	case s.OS == "darwin":
		return SetupStep{
			What: "Go 툴체인 설치", Command: "brew install go", Why: why,
		}
	case s.Distro == "ubuntu" || s.Distro == "debian":
		return SetupStep{
			What:    "Go 툴체인 설치",
			Command: "sudo snap install go --classic",
			Why:     why + " · apt 의 golang-go 는 1.22 라 못 쓴다(빌드가 깨진다)",
			Sudo:    true,
		}
	case s.Distro == "fedora" || s.Distro == "rhel":
		return SetupStep{
			What: "Go 툴체인 설치", Command: "sudo dnf install -y golang", Why: why, Sudo: true,
		}
	case s.Distro == "arch":
		return SetupStep{
			What: "Go 툴체인 설치", Command: "sudo pacman -S --noconfirm go", Why: why, Sudo: true,
		}
	default:
		return SetupStep{
			What:    "Go 툴체인 설치",
			Command: "https://go.dev/dl/ 에서 " + MinGoVersion + " 이상을 받아 설치해라",
			Why:     why + " · 이 배포판의 패키지 관리자를 모른다",
		}
	}
}

// serverStartSteps 는 서버를 띄우는 길이다.
//
// ★ **둘을 구분해서 낸다.** 이 레포에는 상시 실행의 정식 방법이 없다(systemd·launchd 정의가
// 하나도 없다). docker compose 만 재시작 정책을 갖고 있으므로 그것을 지원 경로로,
// 포그라운드 실행은 "지금만"으로 이름 붙인다. 없는 것을 있는 척하지 않는다.
func serverStartSteps(s SetupState) []SetupStep {
	if s.Docker {
		return []SetupStep{{
			What:    "서버 기동(지원되는 상시 실행 경로)",
			Command: "cd plugins/flightdeck && docker compose up -d",
			Why:     "restart 정책이 있어 재부팅·크래시를 넘긴다. DB 는 ~/.flightdeck 에 붙는다",
		}}
	}
	return []SetupStep{{
		What:    "서버 기동(임시 — 이 터미널을 닫으면 죽는다)",
		Command: "fd serve --addr 127.0.0.1:7420",
		Why: "docker 가 없다. 이 레포에는 systemd·launchd 정의가 없으므로 " +
			"상시 실행을 원하면 docker 를 설치하거나 서비스 정의를 직접 만들어야 한다",
	}}
}

// RenderSetupPlan 은 계획을 사람이 읽는 문구로 만든다. 순수 함수다.
//
// 이 문자열이 소비자 좌표계다 — 스킬이 읽는 것도, 사람이 읽는 것도 이것뿐이다.
func RenderSetupPlan(p SetupPlan) string {
	var b strings.Builder

	if p.Refused {
		b.WriteString("■ 이 환경은 지원하지 않는다\n")
		b.WriteString("  " + strings.ReplaceAll(p.Reason, "\n", "\n  ") + "\n")
		return b.String()
	}

	b.WriteString("■ 지금 상태\n")
	fmt.Fprintf(&b, "  역할      %s\n", roleLabel(p.Role))
	fmt.Fprintf(&b, "  서버      %s (%s)\n", orDash(p.Endpoint.URL), clip(p.Endpoint.URLSource, 120))
	fmt.Fprintf(&b, "  토큰      %s\n", clip(p.Endpoint.TokenSource, 120))
	// 역할과 도달성은 **따로** 낸다 — 설정 문제와 기동 문제는 다른 사실이다.
	if p.Reachable {
		b.WriteString("  도달      ✓ healthz 가 답한다\n")
	} else {
		b.WriteString("  도달      ✗ 응답이 없다\n")
	}
	if strings.TrimSpace(p.Endpoint.Warn) != "" {
		fmt.Fprintf(&b, "  ! %s\n", clip(p.Endpoint.Warn, 400))
	}
	if strings.TrimSpace(p.Reason) != "" {
		fmt.Fprintf(&b, "  %s\n", strings.ReplaceAll(p.Reason, "\n", "\n  "))
	}

	if len(p.Steps) == 0 {
		if p.Ready {
			b.WriteString("\n■ 할 일 없음\n")
		}
	} else {
		b.WriteString("\n■ 할 일 — **실행 전에 사람의 승인을 받아라**\n")
		for i, s := range p.Steps {
			mark := ""
			if s.Sudo {
				mark = " [관리자 권한]"
			}
			fmt.Fprintf(&b, "  %d. %s%s\n     $ %s\n     왜: %s\n", i+1, s.What, mark, s.Command, s.Why)
		}
	}
	if strings.TrimSpace(p.Verify) != "" {
		fmt.Fprintf(&b, "\n■ 확인\n  %s\n", p.Verify)
	}
	return b.String()
}

func roleLabel(r Role) string {
	switch r {
	case RoleServer:
		return "서버 — 이 머신이 조정 서버를 띄운다(주소가 루프백이다)"
	case RoleClient:
		return "클라이언트 — 다른 머신의 서버에 붙는다"
	default:
		return "모름 — 주소를 못 읽었다"
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// `fd setup` — 관측하고 판정을 낸다. **아무것도 설치하지 않고 아무것도 띄우지 않는다.**
//
// ★ 왜 이 명령이 실행을 안 하나: 설치는 sudo/관리자를 요구하고 되돌리기 어렵다.
// 그래서 이 자리는 "무엇이 없고 다음에 무엇을 해야 하나"까지만 내고, **승인을 받아 실행하는
// 일은 사람(또는 fd-setup 스킬)이** 한다. 도구가 조용히 시스템을 바꾸지 않는다.
// ─────────────────────────────────────────────────────────────────────────────

// runSetup 은 `fd setup` 이다.
func (a *App) runSetup(ctx context.Context, args []string, out io.Writer) int {
	fs := newFlagSet("setup")
	setURL := fs.String("url", "", "서버 주소를 정한다(예: http://10.0.0.5:7420). 이 머신이 서버면 "+DefaultURL)
	setToken := fs.String("token", "", "서버 토큰. --url 과 함께 쓴다")
	clearToken := fs.Bool("clear-token", false, "저장된 토큰을 지운다")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	home := homeDir(a.env)
	path, pathSrc := ConfigPath(a.env, home)

	// ── 저장 갈래 ───────────────────────────────────────────────────────────
	if strings.TrimSpace(*setURL) != "" || strings.TrimSpace(*setToken) != "" || *clearToken {
		cfg, _, _ := LoadConfig(path)
		if v := strings.TrimSpace(*setURL); v != "" {
			cfg.URL = strings.TrimRight(v, "/")
		}
		if *clearToken {
			cfg.Token = ""
		} else if v := strings.TrimSpace(*setToken); v != "" {
			cfg.Token = v
		}
		if err := SaveConfig(path, cfg); err != nil {
			fmt.Fprintf(out, "설정을 저장하지 못했다: %v\n", err)
			return 1
		}
		fmt.Fprintf(out, "설정 저장 · %s (%s)\n", path, pathSrc)
		fmt.Fprintf(out, "  서버 %s · 역할 %s\n", orDash(cfg.URL), roleLabel(DetectRole(cfg.URL)))
		fmt.Fprintf(out, "  토큰 %s\n", map[bool]string{true: "설정됨", false: "없음"}[cfg.Token != ""])
		// ★ 이 줄을 반드시 낸다. MCP 서버는 **기동 시 환경을 한 번 읽고 끝**이고
		//   훅도 부모(claude)의 환경을 물려받는다. 안 말하면 사용자는
		//   "설정했는데 안 된다"를 겪고 원인을 못 찾는다.
		fmt.Fprintln(out, "\n★ 지금 도는 세션에는 안 붙는다 — Claude Code 를 다시 시작해야 훅·MCP 가 이 값을 본다.")
		return 0
	}

	// ── 보고 갈래 ───────────────────────────────────────────────────────────
	st := a.observeSetup(ctx)
	fmt.Fprint(out, RenderSetupPlan(PlanSetup(st)))
	if st.OS != "windows" {
		fmt.Fprintf(out, "\n설정 파일  %s (%s)\n", path, pathSrc)
	}
	return 0
}

// observeSetup 은 판정에 필요한 값을 **실제로 잰다.** 판정은 안 한다(PlanSetup 이 한다).
func (a *App) observeSetup(ctx context.Context) SetupState {
	st := SetupState{
		OS:       runtime.GOOS,
		Distro:   detectDistro(),
		GoOutput: commandOutput(ctx, "go", "version"),
		Docker:   lookPath("docker"),
		Endpoint: a.cli.Endpoint,
	}
	// 도달성은 healthz 로 잰다 — 인증 게이트 앞이라 토큰 없이도 답한다.
	if _, err := a.cli.Healthz(ctx); err == nil {
		st.Reachable = true
	}
	return st
}

// detectDistro 는 /etc/os-release 의 ID 를 읽는다. 못 읽으면 빈 문자열이다.
//
// 지어내지 않는다 — 모르면 모른다고 두고, 그때 계획은 공식 다운로드 페이지를 낸다.
func detectDistro() string {
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "ID="); ok {
			return strings.ToLower(strings.Trim(strings.TrimSpace(v), `"`))
		}
	}
	return ""
}

func lookPath(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

// commandOutput 은 명령 하나를 돌려 stdout 을 낸다. 실패하면 빈 문자열이다.
func commandOutput(ctx context.Context, bin string, args ...string) string {
	if !lookPath(bin) {
		return ""
	}
	out, err := exec.CommandContext(ctx, bin, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
