package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func envOf(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

// 상태 디렉토리는 **왜 그 자리인지**를 함께 낸다.
// 사유가 없으면 "여기가 맞나"를 물을 자리가 없고, 아웃박스가 조용히 다른 곳에 쌓인다.
func TestResolveStateDirPrefersPluginDataAndAlwaysGivesReason(t *testing.T) {
	cases := []struct {
		name     string
		env      map[string]string
		home     string
		wantPath string
		wantSrc  string
	}{
		{"명시 지정이 이긴다",
			map[string]string{"FD_STATE_DIR": "/x/y", "CLAUDE_PLUGIN_DATA": "/p"}, "/home/a",
			"/x/y", "FD_STATE_DIR"},
		{"PLUGIN_DATA 아래에 둔다",
			map[string]string{"CLAUDE_PLUGIN_DATA": "/p", "XDG_STATE_HOME": "/x"}, "/home/a",
			"/p/flightdeck", "CLAUDE_PLUGIN_DATA"},
		{"없으면 XDG",
			map[string]string{"XDG_STATE_HOME": "/x"}, "/home/a",
			"/x/flightdeck", "XDG_STATE_HOME"},
		{"그것도 없으면 홈",
			map[string]string{}, "/home/a",
			"/home/a/.local/state/flightdeck", "~/.local/state"},
		// ★ 표 밖: 홈도 없다. 값은 내되 **재부팅하면 사라진다는 사실**을 사유에 담아야 한다.
		{"홈도 없다", map[string]string{}, "", "", "임시 디렉토리"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ResolveStateDir(envOf(c.env), c.home)
			if c.wantPath != "" && got.Path != c.wantPath {
				t.Fatalf("경로가 %q 다, %q 를 기대했다", got.Path, c.wantPath)
			}
			if !strings.Contains(got.Source, c.wantSrc) {
				t.Fatalf("사유에 %q 가 없다: %q", c.wantSrc, got.Source)
			}
		})
	}
	// 빈 값은 "설정 안 됨"과 같다 — 빈 문자열을 경로로 받으면 루트에 쓰게 된다.
	got := ResolveStateDir(envOf(map[string]string{"CLAUDE_PLUGIN_DATA": "  "}), "/home/a")
	if got.Path != filepath.Join("/home/a", ".local", "state", "flightdeck") {
		t.Fatalf("빈 CLAUDE_PLUGIN_DATA 를 값으로 받았다: %q", got.Path)
	}
	// 임시 폴백은 **잃을 수 있다는 사실**을 말해야 한다.
	tmp := ResolveStateDir(envOf(map[string]string{}), "")
	if !strings.Contains(tmp.Source, "아직 못 보낸 판단") {
		t.Fatalf("임시 폴백이 무엇을 잃는지 말하지 않는다: %q", tmp.Source)
	}
}

// 워크트리에서 부르면 **주 저장소**를 골라야 한다.
// 링크된 워크트리 경로를 주면 서버의 worktree list 가 그 하나만 보고,
// 그러면 다른 세션들이 통째로 안 보인다.
func TestMainRepoRootPicksTheMainRepositoryNotTheWorktree(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/repo/.git", "/repo"},
		{"/repo/.git/", "/repo"},
		{"/srv/bare.git", "/srv/bare.git"}, // bare 는 그 자체가 루트다
		{"  /repo/.git  ", "/repo"},
		{"", ""},
		{".git", "."}, // 상대경로도 성분으로 다룬다
	}
	for _, c := range cases {
		if got := MainRepoRoot(c.in); got != c.want {
			t.Fatalf("%q → %q, %q 를 기대했다", c.in, got, c.want)
		}
	}
}

func TestProjectIDFromPathStripsShellHostileCharacters(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/home/a/sample-platform", "sample-platform"},
		{"/home/a/my repo", "my-repo"},
		{"/home/a/repo;rm -rf", "repo-rm--rf"},
		{"/", ""},
		{"", ""},
		{"/home/a/.hidden", "hidden"}, // 앞뒤 '.' 은 떨어진다(파일 이름·질의 값으로 나간다)
	}
	for _, c := range cases {
		if got := ProjectIDFromPath(c.in); got != c.want {
			t.Fatalf("%q → %q, %q 를 기대했다", c.in, got, c.want)
		}
	}
}

// 멱등 키는 **시각을 안 넣는다.** 넣으면 재시도마다 키가 달라져 멱등이 이름뿐이 된다.
func TestIdempotencyKeyIsStableForSameBody(t *testing.T) {
	a := IdempotencyKey("sess-1", []byte(`{"body":"x"}`))
	b := IdempotencyKey("sess-1", []byte(`{"body":"x"}`))
	if a != b {
		t.Fatalf("같은 세션·같은 본문인데 키가 다르다: %s vs %s", a, b)
	}
	if c := IdempotencyKey("sess-1", []byte(`{"body":"y"}`)); c == a {
		t.Fatalf("다른 본문인데 키가 같다: %s", c)
	}
	if d := IdempotencyKey("sess-2", []byte(`{"body":"x"}`)); d == a {
		t.Fatalf("다른 세션인데 키가 같다: %s", d)
	}
	// 표 밖: 세션이 없는 경로(훅이 세션을 열기 전)도 키를 낸다.
	if e := IdempotencyKey("", []byte("x")); !strings.HasPrefix(e, "nosession:") {
		t.Fatalf("세션 없는 키가 그 사실을 말하지 않는다: %s", e)
	}
}

// 캐시 키는 경로마다 달라야 한다. 접두만 쓰면 project 가 다른 두 보드가 서로를 덮는다.
func TestCacheKeySeparatesDifferentQueries(t *testing.T) {
	a := CacheKey("/api/v1/dashboard.json?project=alpha&self=")
	b := CacheKey("/api/v1/dashboard.json?project=beta&self=")
	if a == b {
		t.Fatalf("project 가 다른데 캐시 키가 같다: %s", a)
	}
	if strings.ContainsAny(a, "/?&=") {
		t.Fatalf("캐시 키에 경로 문자가 남았다: %s", a)
	}
	if CacheKey("") == "" {
		t.Fatal("빈 경로에 빈 키를 냈다 — 파일 이름이 없어진다")
	}
}

// 아웃박스 자리는 **채널 환경과 무관해야 한다.**
//
// ★ 이 시험이 이 항목의 회귀를 원리적으로 막는다. TestConfigPathIsChannelIndependent ·
// TestAllChannelsAgreeOnMachineID 와 같은 모양이다 — 산문이 아니라 이것이 규칙을 지킨다.
//
// 왜 상태 디렉토리가 아닌가: ResolveStateDir 은 CLAUDE_PLUGIN_DATA(훅·MCP 에만 있다)와
// XDG_STATE_HOME|~/.local/state(사용자 셸)로 **일부러 갈리게** 만든 축이다. 캐시는
// 재생성 가능하니 갈려도 되지만 아웃박스는 설계 §7 이 "재생성 불가한 유일한 자산"이라
// 부른 것을 담는다.
func TestOutboxPathIsChannelIndependent(t *testing.T) {
	home := "/h"
	envs := []map[string]string{
		{},
		{"CLAUDE_PLUGIN_DATA": "/plugin/data"}, // 훅·MCP 채널
		{"XDG_STATE_HOME": "/xdg/state"},       // 사용자 셸 채널
		{"CLAUDE_PLUGIN_DATA": "/plugin/data", "XDG_STATE_HOME": "/xdg/state"},
	}
	want := filepath.Join(home, ".flightdeck", "outbox")
	for i, e := range envs {
		got, src := OutboxPath(envOf(e), home)
		if got != want {
			t.Errorf("%d번 환경에서 아웃박스 자리가 %q 다 — %q 여야 한다.\n"+
				"채널마다 갈리면 셸에서 쌓인 판단을 훅·MCP 가 영영 못 보낸다", i, got, want)
		}
		if strings.TrimSpace(src) == "" {
			t.Errorf("%d번 환경에서 출처가 비었다 — '왜 여기냐'에 답할 자리가 없다", i)
		}
	}
}

// FD_STATE_DIR 는 채널이 아니라 **사람이** 지정하는 축이라 이것만은 존중한다
// (MachineIDPath·ConfigPath 가 같은 예외를 같은 이유로 둔다).
func TestOutboxPathHonoursExplicitStateDir(t *testing.T) {
	got, src := OutboxPath(envOf(map[string]string{"FD_STATE_DIR": "/explicit"}), "/h")
	if want := filepath.Join("/explicit", "outbox"); got != want {
		t.Errorf("FD_STATE_DIR 를 줬는데 %q 다 — %q 여야 한다", got, want)
	}
	if !strings.Contains(src, "FD_STATE_DIR") {
		t.Errorf("출처가 FD_STATE_DIR 를 안 말한다: %q", src)
	}
}

// HOME 도 FD_STATE_DIR 도 없으면 임시 디렉토리로 떨어지되 **그 사실을 사유에 적는다.**
func TestOutboxPathSaysWhenItWillNotSurviveReboot(t *testing.T) {
	_, src := OutboxPath(envOf(map[string]string{}), "")
	if !strings.Contains(src, "임시") {
		t.Errorf("임시 디렉토리 폴백인데 사유가 그것을 안 말한다: %q", src)
	}
}

// 옛 자리 목록은 **이 채널이 계산할 수 있는 것만** 담는다.
//
// ★ ~/.claude/plugins/data/*/flightdeck 를 glob 하지 않는다. 그 경로에는 플러그인
// 버전과 마켓 이름이 들어가고, 설계 §13 이 "그 경로를 어디에도 저장하지 않는다"고
// 판정했다. 대신 각 채널이 제 자리를 **전송으로** 비우고, 그래서 어느 채널이 한 번도
// 안 돌면 그 자리는 안 비워진다 — 그 구멍은 fd doctor 가 말로 찍는다.
func TestLegacyOutboxDirsCoversOnlyWhatThisChannelCanCompute(t *testing.T) {
	home := "/h"
	target := filepath.Join(home, ".flightdeck", "outbox")

	got := LegacyOutboxDirs(envOf(map[string]string{
		"CLAUDE_PLUGIN_DATA": "/plugin/data",
		"XDG_STATE_HOME":     "/xdg/state",
	}), home, target)

	want := []string{
		filepath.Join("/plugin/data", "flightdeck", "outbox"),
		filepath.Join("/xdg/state", "flightdeck", "outbox"),
		filepath.Join(home, ".local", "state", "flightdeck", "outbox"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("옛 자리 목록이\n  %v\n여야 하는데\n  %v\n다", want, got)
	}
	for _, d := range got {
		if strings.Contains(d, "*") {
			t.Errorf("glob 패턴이 목록에 들어갔다(%q) — §13 은 그 경로를 저장하지 말라고 했다", d)
		}
	}
}

// 옛 자리 목록은 임시 디렉토리를 **절대** 후보로 얹지 않는다.
//
// ★ 이 축은 "채널이 계산할 수 있는가"가 아니라 "누가 그 파일을 쓸 수 있는가"다.
// 과제 5부터 이 목록의 각 자리는 fd 가 **읽어서 사용자의 토큰으로 서버에 POST 하는**
// 자리가 된다. `/tmp` 는 부모가 world-writable 이라, 이 머신의 다른 로컬 사용자가
// `<tmp>/flightdeck/outbox/pending.jsonl` 을 0644 로 심어 두면 fd 가 그 줄을 읽어
// 사용자 이름으로 그대로 전송한다. 판단은 추가 전용이라(트리거가 UPDATE·DELETE 를
// 막는다) 그렇게 들어간 줄은 **회수할 방법이 없다**. 나머지 네 후보(CLAUDE_PLUGIN_DATA·
// XDG_STATE_HOME·~/.local/state·~/.flightdeck)는 $HOME 아래거나 사용자 자신의
// 프로세스만 세팅하는 환경변수에서 오므로 이 주입이 안 통한다 — tmp 만 다르다.
func TestLegacyOutboxDirsNeverScansTempDir(t *testing.T) {
	got := LegacyOutboxDirs(envOf(map[string]string{
		"CLAUDE_PLUGIN_DATA": "/plugin/data",
		"XDG_STATE_HOME":     "/xdg/state",
	}), "/h", filepath.Join("/h", ".flightdeck", "outbox"))

	tmp := filepath.Clean(os.TempDir())
	for _, d := range got {
		if filepath.Clean(d) == filepath.Join(tmp, "flightdeck", "outbox") ||
			strings.HasPrefix(filepath.Clean(d), tmp+string(filepath.Separator)) {
			t.Fatalf("옛 자리 목록에 임시 디렉토리 아래 %q 가 들어 있다 — "+
				"world-writable 부모 아래라 남이 심은 줄을 그대로 전송할 위험이 있다: %v", d, got)
		}
	}
}

// ★ FD_STATE_DIR 를 **새로 켠** 사용자를 위한 자리.
//
// 그러면 목표가 FD_STATE_DIR/outbox 로 바뀌는데, ~/.flightdeck/outbox 가 후보에 없으면
// 그때까지 고정 자리에 쌓인 판단 전량이 조용히 안 보이게 된다(doctor 가 '대기 0건'을 찍는다).
// home 은 이미 인자로 들어오므로 계산 불가한 것이 아니라 그냥 빠뜨리기 쉬운 자리다.
func TestLegacyOutboxDirsIncludesFixedPlaceWhenStateDirIsExplicit(t *testing.T) {
	home := "/h"
	got := LegacyOutboxDirs(envOf(map[string]string{"FD_STATE_DIR": "/explicit"}),
		home, filepath.Join("/explicit", "outbox"))
	fixed := filepath.Join(home, ".flightdeck", "outbox")
	for _, d := range got {
		if d == fixed {
			return
		}
	}
	t.Fatalf("FD_STATE_DIR 를 켰는데 고정 자리(%s)가 후보에 없다 — "+
		"그때까지 쌓인 판단이 조용히 안 보이게 된다: %v", fixed, got)
}

// 목표와 같은 자리는 돌 것이 없다. 넣으면 같은 큐를 두 번 재생한다.
func TestLegacyOutboxDirsExcludesTheTarget(t *testing.T) {
	target := filepath.Join("/h", ".flightdeck", "outbox")
	got := LegacyOutboxDirs(envOf(map[string]string{}), "/h", target)
	for _, d := range got {
		if filepath.Clean(d) == filepath.Clean(target) {
			t.Fatalf("목표 자리(%s)가 옛 자리 목록에 들어 있다 — 같은 큐를 두 번 돈다", target)
		}
	}
}

// 같은 자리를 두 축이 가리켜도 한 번만 돈다.
func TestLegacyOutboxDirsDeduplicates(t *testing.T) {
	got := LegacyOutboxDirs(envOf(map[string]string{
		"CLAUDE_PLUGIN_DATA": "/same",
		"XDG_STATE_HOME":     "/same",
	}), "", "/target/outbox")
	n := 0
	for _, d := range got {
		if d == filepath.Join("/same", "flightdeck", "outbox") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("같은 자리를 %d번 돈다: %v", n, got)
	}
}
