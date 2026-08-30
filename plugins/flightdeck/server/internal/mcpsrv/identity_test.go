package mcpsrv

import (
	"errors"
	"strings"
	"testing"
)

// 소비자 좌표계 = **도구 응답에 실리는 배너 문구와 거절 사유**다.
// 구현의 개념(Identity 필드)이 아니라 그 문자열로 단정한다.

func env(pairs map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := pairs[k]
		return v, ok
	}
}

func TestResolveIdentity(t *testing.T) {
	cases := []struct {
		name        string
		env         map[string]string
		cwd         string
		cwdErr      error
		host        string
		hostErr     error
		wantMissing []string
		wantProject string
		wantWork    string
	}{
		{
			name:        "실측된 정상 환경",
			env:         map[string]string{EnvSessionID: "uuid-1", EnvProjectDir: "/home/a/proj"},
			cwd:         "/home/a/proj",
			host:        "box",
			wantMissing: nil,
			wantProject: "proj",
			wantWork:    "/home/a/proj",
		},
		{
			name:        "세션 id 만 없다",
			env:         map[string]string{EnvProjectDir: "/home/a/proj"},
			cwd:         "/home/a/proj",
			host:        "box",
			wantMissing: []string{EnvSessionID},
			wantProject: "proj",
			wantWork:    "/home/a/proj",
		},
		{
			name:        "PROJECT_DIR 이 없어 cwd 로 좌표를 만든다",
			env:         map[string]string{EnvSessionID: "uuid-1"},
			cwd:         "/home/a/other",
			host:        "box",
			wantMissing: []string{EnvProjectDir},
			wantProject: "other",
			wantWork:    "/home/a/other",
		},
		{
			name:        "워크트리에서 띄워 cwd 와 PROJECT_DIR 이 다르다 — cwd 가 이긴다",
			env:         map[string]string{EnvSessionID: "u", EnvProjectDir: "/home/a/proj"},
			cwd:         "/home/a/proj/.flightdeck/worktrees/x",
			host:        "box",
			wantProject: "proj",
			wantWork:    "/home/a/proj/.flightdeck/worktrees/x",
		},

		// ── 표 밖 케이스 ──
		{
			name:        "빈 문자열 환경변수는 '있다'가 아니다",
			env:         map[string]string{EnvSessionID: "   ", EnvProjectDir: ""},
			cwd:         "/home/a/proj",
			host:        "box",
			wantMissing: []string{EnvSessionID, EnvProjectDir},
			wantProject: "proj",
			wantWork:    "/home/a/proj",
		},
		{
			name:        "cwd 도 PROJECT_DIR 도 없다 — 좌표가 통째로 없다",
			env:         map[string]string{EnvSessionID: "u"},
			cwdErr:      errors.New("getwd: no such file or directory"),
			host:        "box",
			wantMissing: []string{"cwd", EnvProjectDir, "project"},
			wantProject: "",
			wantWork:    "",
		},
		{
			name:        "cwd 가 루트라 좌표가 못 된다",
			env:         map[string]string{EnvSessionID: "u"},
			cwd:         "/",
			host:        "box",
			wantMissing: []string{EnvProjectDir, "project"},
			wantProject: "",
			wantWork:    "/",
		},
		{
			name:        "cwd 가 상대경로다 — 워크트리로 못 쓴다",
			env:         map[string]string{EnvSessionID: "u"},
			cwd:         "relative/dir",
			host:        "box",
			wantMissing: []string{"cwd", EnvProjectDir},
			wantProject: "dir",
			wantWork:    "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id := ResolveIdentity(env(c.env), c.cwd, c.cwdErr, c.host, c.hostErr)
			if id.ProjectID != c.wantProject {
				t.Fatalf("ProjectID = %q, 기대 %q", id.ProjectID, c.wantProject)
			}
			if id.Worktree != c.wantWork {
				t.Fatalf("Worktree = %q, 기대 %q", id.Worktree, c.wantWork)
			}
			if len(id.Missing) != len(c.wantMissing) {
				t.Fatalf("결손 축 = %v, 기대 %v", id.Missing, c.wantMissing)
			}
			for _, want := range c.wantMissing {
				found := false
				for _, got := range id.Missing {
					if got == want {
						found = true
					}
				}
				if !found {
					t.Fatalf("결손 축에 %q 가 없다: %v", want, id.Missing)
				}
			}
		})
	}
}

func TestBannerNamesEveryMissingAxis(t *testing.T) {
	id := ResolveIdentity(env(map[string]string{}), "", errors.New("getwd 실패"), "", nil)
	b := id.Banner()
	// 소비자 좌표계: 이 문자열이 도구 응답 꼬리에 그대로 실린다.
	for _, want := range []string{EnvSessionID, EnvProjectDir, "cwd", "project", "안 되는 것", "지어내지 않는다"} {
		if !strings.Contains(b, want) {
			t.Fatalf("배너에 %q 가 없다:\n%s", want, b)
		}
	}
	// hostname 결손은 경고이지 거절이 아니다 — 그 사실도 화면에 있어야 한다.
	if !strings.Contains(b, "unknown-machine") {
		t.Fatalf("hostname 대체값을 배너가 안 알린다:\n%s", b)
	}

	full := ResolveIdentity(env(map[string]string{
		EnvSessionID: "u", EnvProjectDir: "/home/a/proj",
	}), "/home/a/proj", nil, "box", nil)
	if got := full.Banner(); got != "" {
		t.Fatalf("정체가 온전한데 배너가 났다:\n%s", got)
	}
}

func TestGateTool(t *testing.T) {
	full := ResolveIdentity(env(map[string]string{
		EnvSessionID: "u", EnvProjectDir: "/home/a/proj",
	}), "/home/a/proj", nil, "box", nil)
	noSession := ResolveIdentity(env(map[string]string{
		EnvProjectDir: "/home/a/proj",
	}), "/home/a/proj", nil, "box", nil)
	noProject := ResolveIdentity(env(map[string]string{
		EnvSessionID: "u",
	}), "", errors.New("없다"), "box", nil)

	cases := []struct {
		name, tool string
		id         Identity
		wantOK     bool
		wantIn     string
	}{
		{"정상 · pick", "pick", full, true, "3중키가 전부 있다"},
		{"정상 · board", "board", full, true, "세션 귀속이 없어도"},
		{"정상 · land", "land", full, true, "3중키가 전부 있다"},
		{"세션 없음 · note 거절", "note", noSession, false, EnvSessionID},
		{"세션 없음 · add 거절", "add", noSession, false, EnvSessionID},
		{"세션 없음 · finish 거절", "finish", noSession, false, EnvSessionID},
		{"세션 없음 · land 거절", "land", noSession, false, EnvSessionID},
		{"세션 없음 · label 거절", "label", noSession, false, EnvSessionID},
		{"세션 없음 · board 는 통과", "board", noSession, true, "세션 귀속이 없어도"},
		{"세션 없음 · alloc 은 통과", "alloc", noSession, true, "세션 귀속이 없어도"},
		{"프로젝트 없음 · board 도 거절", "board", noProject, false, "프로젝트 좌표가 없다"},

		// ── 표 밖 케이스 ──
		{"표에 없는 도구", "status", full, false, "이 서버에 없다"},
		{"빈 이름", "", full, false, "이 서버에 없다"},
		{"전체 이름을 그대로 준 경우", "mcp__plugin_flightdeck_fd__pick", full, false, "이 서버에 없다"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, reason := GateTool(c.tool, c.id)
			if ok != c.wantOK {
				t.Fatalf("GateTool(%q) ok=%v, 기대 %v (사유: %s)", c.tool, ok, c.wantOK, reason)
			}
			if reason == "" {
				t.Fatal("사유가 비었다 — 다중 조건 판정은 불리언이 아니라 사유를 낸다")
			}
			if !strings.Contains(reason, c.wantIn) {
				t.Fatalf("사유에 %q 가 없다: %s", c.wantIn, reason)
			}
		})
	}
}

func TestProjectIDFor(t *testing.T) {
	cases := []struct{ name, dir, cwd, want string }{
		{"PROJECT_DIR 우선", "/a/b/proj", "/a/b/proj/wt", "proj"},
		{"PROJECT_DIR 없으면 cwd", "", "/a/b/other", "other"},
		{"끝 슬래시", "/a/b/proj/", "", "proj"},

		// ── 표 밖 케이스 ──
		{"둘 다 없다", "", "", ""},
		{"루트", "/", "", ""},
		{"점", ".", "", ""},
		{"루트지만 cwd 는 쓸 수 있다", "/", "/a/b/c", "c"},
		{"공백만", "   ", "  ", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ProjectIDFor(c.dir, c.cwd); got != c.want {
				t.Fatalf("ProjectIDFor(%q,%q) = %q, 기대 %q", c.dir, c.cwd, got, c.want)
			}
		})
	}
}

// TestResolveIdentityAsNamesTheHarnessEnv 는 이 항목의 **이유**를 문다.
//
// 배너와 거절 사유가 부르는 환경변수 이름이 그 하네스의 것이어야 한다. codex 세션에서
// CLAUDE_CODE_SESSION_ID 를 가리키면, "지어내지 않는다"를 지키려고 만든 문장이 그 자리에서
// 존재하지 않는 변수를 가리키는 안내가 된다.
func TestResolveIdentityAsNamesTheHarnessEnv(t *testing.T) {
	codexEnv := env(map[string]string{EnvCodexSessionID: "codex-uuid", EnvProjectDir: "/p"})
	id := ResolveIdentityAs(HarnessCodex, codexEnv, "/p", nil, "h", nil)
	if id.CCSessionID != "codex-uuid" {
		t.Fatalf("codex 세션 id 를 못 읽었다: %q", id.CCSessionID)
	}
	if id.Harness != HarnessCodex || id.HarnessLabel() != "codex" {
		t.Fatalf("하네스가 %q · 라벨 %q — codex 여야 한다", id.Harness, id.HarnessLabel())
	}
	if id.SessionEnvName() != EnvCodexSessionID {
		t.Fatalf("세션 축 이름이 %q — %q 여야 한다", id.SessionEnvName(), EnvCodexSessionID)
	}

	// ★ codex 인데 값이 없으면 **codex 의 이름**이 결손으로 나와야 한다.
	empty := ResolveIdentityAs(HarnessCodex, env(map[string]string{EnvProjectDir: "/p"}), "/p", nil, "h", nil)
	if !containsAxis(empty.Missing, EnvCodexSessionID) {
		t.Fatalf("결손 축이 %v — %s 를 불러야 한다", empty.Missing, EnvCodexSessionID)
	}
	if containsAxis(empty.Missing, EnvSessionID) {
		t.Fatalf("codex 세션인데 결손 축이 %s 를 불렀다 — 없는 변수를 가리키는 안내다", EnvSessionID)
	}
	ok, reason := GateTool("note", empty)
	if ok {
		t.Fatal("세션 id 가 없는데 note 가 통과했다")
	}
	if !strings.Contains(reason, EnvCodexSessionID) {
		t.Fatalf("거절 사유가 codex 축을 안 불렀다: %s", reason)
	}
}

// TestResolveIdentityDeclarationBeatsProbing 은 **선언이 관측을 이긴다**를 문다.
//
// 실측된 오염이 근거다: Claude 세션 안에서 띄운 codex 는 CLAUDE_CODE_SESSION_ID 를
// 물려받는다. 선언이 그 값을 이기지 못하면 codex 카드가 남의 카드에 붙는다.
func TestResolveIdentityDeclarationBeatsProbing(t *testing.T) {
	both := env(map[string]string{
		EnvSessionID:      "claude-uuid(물려받은 것)",
		EnvCodexSessionID: "codex-uuid",
		EnvProjectDir:     "/p",
	})
	id := ResolveIdentityAs(HarnessCodex, both, "/p", nil, "h", nil)
	if id.CCSessionID != "codex-uuid" {
		t.Fatalf("세션 id 가 %q — 선언한 하네스의 값(codex-uuid)이어야 한다. "+
			"상속된 CLAUDE_CODE_SESSION_ID 를 집으면 codex 세션이 남의 카드에 붙는다", id.CCSessionID)
	}
}

// TestResolveIdentityWithoutHarnessStaysUnknown 은 **미선언을 claude 로 접지 않는다**를 문다.
func TestResolveIdentityWithoutHarnessStaysUnknown(t *testing.T) {
	id := ResolveIdentityAs("", env(map[string]string{EnvCodexSessionID: "codex-uuid", EnvProjectDir: "/p"}), "/p", nil, "h", nil)
	if id.Harness != "" || id.HarnessLabel() != "미상" {
		t.Fatalf("선언이 없는데 하네스가 %q 로 정해졌다 — 「미상」이어야 한다", id.Harness)
	}
	// 값은 찾는다. 찾는 것과 이름 붙이는 것은 다른 질문이다.
	if id.CCSessionID != "codex-uuid" || id.SessionEnvName() != EnvCodexSessionID {
		t.Fatalf("훑기로 값을 못 찾았다: id=%q env=%q", id.CCSessionID, id.SessionEnvName())
	}
	if len(id.Warnings) == 0 {
		t.Fatal("미선언인데 경고가 없다 — 조용히 넘어가면 그 사실이 어느 화면에도 안 뜬다")
	}
}

// TestResolveIdentityRejectsUnknownHarnessLoudly 는 오타가 조용히 「미상」으로 접히지 않게 한다.
func TestResolveIdentityRejectsUnknownHarnessLoudly(t *testing.T) {
	id := ResolveIdentityAs("codx", env(map[string]string{EnvSessionID: "u", EnvProjectDir: "/p"}), "/p", nil, "h", nil)
	if id.Harness != "" {
		t.Fatalf("모르는 이름이 하네스로 앉았다: %q", id.Harness)
	}
	joined := strings.Join(id.Warnings, " ")
	if !strings.Contains(joined, "codx") {
		t.Fatalf("경고가 오타를 안 불렀다: %v", id.Warnings)
	}
}
