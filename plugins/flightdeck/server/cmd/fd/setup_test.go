package main

import (
	"strings"
	"testing"
)

// 셋업 판정 — **전부 순수 함수다.** 관측(OS·go 버전·docker·healthz)은 부수효과 있는 자리에서
// 하고, 판정은 그 값만 본다. 그래야 조합 전체를 표로 돌 수 있다.

// ── 역할은 저장하지 않고 파생한다 ───────────────────────────────────────────
//
// ★ URL 이 루프백이면 이 머신이 서버다 — 루프백은 그것 말고 다른 뜻이 될 수 없다.
// 역할을 파일에 저장하면 **의도와 현실이 갈린다**(저장해 둔 "서버"인데 주소는 원격인 상태).
// 이 레포의 규율이 "파생 가능한 값에 파라미터를 두면 틀린 값이 들어온다"이다.

func TestDetectRoleReadsTheURLNotAStoredFlag(t *testing.T) {
	cases := []struct {
		url  string
		want Role
	}{
		{"http://127.0.0.1:7420", RoleServer},
		{"http://localhost:7420", RoleServer},
		{"http://[::1]:7420", RoleServer},
		{"http://127.0.0.1", RoleServer},
		// 0.0.0.0 은 "전 인터페이스로 듣는다"라 클라이언트 목적지로는 성립하지 않는다.
		// 이 머신이 서버라는 뜻으로 읽는 것이 유일하게 말이 된다.
		{"http://0.0.0.0:7420", RoleServer},
		{"http://10.0.0.5:7420", RoleClient},
		{"http://fd.internal:7420", RoleClient},
		{"https://fd.example.com", RoleClient},
		// 못 읽는 주소를 서버로 접으면 "이 머신에 서버를 세워라"라는 틀린 처방이 나간다.
		{"", RoleUnknown},
		{"::::", RoleUnknown},
	}
	for _, c := range cases {
		if got := DetectRole(c.url); got != c.want {
			t.Errorf("DetectRole(%q) = %v — %v 여야 한다", c.url, got, c.want)
		}
	}
}

// ── Go 버전 — 이번 조사에서 찾은 함정 ───────────────────────────────────────
//
// ★ **존재 검사로는 부족하다.** go.mod 가 1.25 를 요구하는데 Ubuntu apt 의 후보는 1.22 다
// (실측: `apt-cache policy golang-go` → 2:1.22~2build1). 그래서 "go 가 있다"만 보고 진행하면
// 설치는 성공하고 **빌드가 깨진다** — 사용자는 원인을 모른 채 실패를 본다.
func TestGoVersionOKRejectsWhatCannotBuild(t *testing.T) {
	cases := []struct {
		out  string // `go version` 의 출력 그대로
		want bool
	}{
		{"go version go1.26.5 linux/amd64", true},
		{"go version go1.25.0 darwin/arm64", true},
		{"go version go1.25 linux/amd64", true},
		// ↓ Ubuntu apt 가 주는 것. 이것을 통과시키면 빌드가 깨진다.
		{"go version go1.22.2 linux/amd64", false},
		{"go version go1.24.9 linux/amd64", false},
		{"go version go1.9 linux/amd64", false},
		{"", false},
		{"command not found", false},
	}
	for _, c := range cases {
		ok, why := GoVersionOK(c.out)
		if ok != c.want {
			t.Errorf("GoVersionOK(%q) = %v — %v 여야 한다 (사유: %s)", c.out, ok, c.want, why)
		}
		if strings.TrimSpace(why) == "" {
			t.Errorf("GoVersionOK(%q) 가 사유를 안 냈다 — 무엇이 부족한지 말해야 고칠 수 있다", c.out)
		}
	}
	// 거절 사유는 **필요한 버전을 말해야** 한다. "안 된다"만으로는 무엇을 설치할지 모른다.
	if _, why := GoVersionOK("go version go1.22.2 linux/amd64"); !strings.Contains(why, MinGoVersion) {
		t.Errorf("거절 사유가 필요한 버전(%s)을 안 말한다: %q", MinGoVersion, why)
	}
}

// ── 계획 ────────────────────────────────────────────────────────────────────

func state(mut func(*SetupState)) SetupState {
	s := SetupState{
		OS: "linux", Distro: "ubuntu",
		GoOutput:  "go version go1.26.5 linux/amd64",
		Docker:    true,
		Endpoint:  Endpoint{URL: DefaultURL, URLSource: "기본값"},
		Reachable: true,
	}
	if mut != nil {
		mut(&s)
	}
	return s
}

// Windows 는 **정직하게 거절한다.** 셋업 성공을 흉내내면 사용자는 훅·MCP 가 왜 안 뜨는지
// 영영 모른다 — 진입점 bin/fd 가 bash 라 Git Bash 없는 Windows 에서는 PowerShell 이 받는다.
func TestPlanRefusesWindowsInsteadOfPretending(t *testing.T) {
	p := PlanSetup(state(func(s *SetupState) { s.OS = "windows"; s.Distro = "" }))
	if !p.Refused {
		t.Fatal("Windows 에서 계획이 거절하지 않았다 — 셋업 성공을 흉내내면 안 된다")
	}
	txt := RenderSetupPlan(p)
	for _, want := range []string{"Windows", "WSL"} {
		if !strings.Contains(txt, want) {
			t.Errorf("거절 문구에 %q 가 없다 — 무엇을 하면 되는지 알 수 없다:\n%s", want, txt)
		}
	}
	if len(p.Steps) != 0 {
		t.Errorf("거절했는데 할 일을 %d개 냈다 — 따라 하면 안 되는 절차다", len(p.Steps))
	}
}

// Go 가 낮으면 **apt 가 아니라 snap** 을 낸다. 이것이 이 시험의 존재 이유다.
func TestPlanOnUbuntuAvoidsTheTooOldAptPackage(t *testing.T) {
	p := PlanSetup(state(func(s *SetupState) {
		s.GoOutput = "go version go1.22.2 linux/amd64"
	}))
	txt := RenderSetupPlan(p)
	if !strings.Contains(txt, "snap install go") {
		t.Errorf("Ubuntu 에서 Go 설치 명령이 snap 이 아니다:\n%s", txt)
	}
	if strings.Contains(txt, "apt-get install -y golang") || strings.Contains(txt, "apt install golang") {
		t.Errorf("apt 의 golang 을 제안했다 — 그 후보는 %s 미만이라 빌드가 깨진다:\n%s", MinGoVersion, txt)
	}
	// 지금 버전이 무엇인지도 말해야 한다 — "낮다"만으로는 사용자가 확인할 수 없다.
	if !strings.Contains(txt, "1.22.2") {
		t.Errorf("지금 설치된 버전을 안 말한다:\n%s", txt)
	}
}

func TestPlanOnMacUsesHomebrew(t *testing.T) {
	p := PlanSetup(state(func(s *SetupState) {
		s.OS, s.Distro = "darwin", ""
		s.GoOutput = ""
	}))
	if txt := RenderSetupPlan(p); !strings.Contains(txt, "brew install go") {
		t.Errorf("macOS 에서 brew 를 안 쓴다:\n%s", txt)
	}
}

// 이미 다 갖춰졌으면 **할 일이 없다고 말한다.** 없는 일을 만들어 내면 신뢰가 깨진다.
func TestPlanSaysNothingToDoWhenAlreadySet(t *testing.T) {
	p := PlanSetup(state(nil))
	if !p.Ready {
		t.Fatalf("전부 갖춰졌는데 Ready 가 아니다: %+v", p)
	}
	if len(p.Steps) != 0 {
		t.Errorf("할 일이 없어야 하는데 %d개다: %v", len(p.Steps), p.Steps)
	}
}

// 역할과 도달성은 **따로** 낸다 — "서버로 설정됐다"와 "서버가 떠 있다"는 다른 사실이고,
// 뭉개면 설정 문제와 기동 문제를 구분할 수 없다.
func TestPlanSeparatesRoleFromReachability(t *testing.T) {
	p := PlanSetup(state(func(s *SetupState) { s.Reachable = false }))
	if p.Role != RoleServer {
		t.Errorf("역할이 %v 다 — 루프백이면 서버여야 한다(도달 못 해도)", p.Role)
	}
	if p.Ready {
		t.Error("서버에 못 닿는데 Ready 다")
	}
	txt := RenderSetupPlan(p)
	if !strings.Contains(txt, "서버") {
		t.Errorf("역할을 안 말한다:\n%s", txt)
	}
	// 서버를 띄우는 길 둘을 **구분해서** 내야 한다 — 레포에 상시 실행의 정식 방법이 없다.
	if !strings.Contains(txt, "docker compose") {
		t.Errorf("지원되는 상시 실행 경로(docker compose)를 안 낸다:\n%s", txt)
	}
}

// 원격을 가리키는데 못 닿으면 **서버를 세우라고 하면 안 된다** — 주소가 틀렸거나 저쪽이 죽은 것이다.
func TestPlanDoesNotTellClientsToStartAServer(t *testing.T) {
	p := PlanSetup(state(func(s *SetupState) {
		s.Endpoint = Endpoint{URL: "http://10.0.0.5:7420", URLSource: "config.json"}
		s.Reachable = false
	}))
	if p.Role != RoleClient {
		t.Fatalf("역할이 %v 다 — 원격 주소면 클라이언트여야 한다", p.Role)
	}
	txt := RenderSetupPlan(p)
	if strings.Contains(txt, "docker compose up") {
		t.Errorf("클라이언트에게 서버를 띄우라고 한다 — 주소가 틀렸거나 저쪽이 죽은 것이다:\n%s", txt)
	}
	if !strings.Contains(txt, "10.0.0.5") {
		t.Errorf("어느 주소에 못 닿았는지 안 말한다:\n%s", txt)
	}
}

// 출처를 항상 낸다 — "왜 저 주소인가"에 답할 자리다.
func TestRenderAlwaysNamesWhereTheAddressCameFrom(t *testing.T) {
	txt := RenderSetupPlan(PlanSetup(state(func(s *SetupState) {
		s.Endpoint = Endpoint{URL: "http://10.0.0.5:7420", URLSource: "FD_URL 환경변수"}
	})))
	if !strings.Contains(txt, "FD_URL 환경변수") {
		t.Errorf("주소의 출처를 안 찍는다:\n%s", txt)
	}
}

// 설정 경고(깨진 파일·넓은 권한)는 계획에도 실려야 한다 — 조용히 사라지면 안 된다.
func TestPlanCarriesTheConfigWarning(t *testing.T) {
	txt := RenderSetupPlan(PlanSetup(state(func(s *SetupState) {
		s.Endpoint.Warn = "설정 파일 권한이 0644 다"
	})))
	if !strings.Contains(txt, "0644") {
		t.Errorf("설정 경고가 계획에서 사라졌다:\n%s", txt)
	}
}
