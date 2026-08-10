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
	// 임시 폴백은 **잃을 수 있다는 사실**을 말해야 한다 — 다만 상태 디렉토리가 담는 것은
	// 이제 캐시뿐이다. "판단"을 언급하면 아웃박스가 여기 있다는, 반증된 전제가 되살아난다.
	tmp := ResolveStateDir(envOf(map[string]string{}), "")
	if !strings.Contains(tmp.Source, "캐시") {
		t.Fatalf("임시 폴백이 무엇을 잃는지 말하지 않는다: %q", tmp.Source)
	}
	if strings.Contains(tmp.Source, "판단") {
		t.Fatalf("상태 디렉토리 폴백이 판단(아웃박스 몫)을 언급한다 — 갈린 축이 다시 섞였다: %q", tmp.Source)
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
// XDG_STATE_HOME|~/.local/state(사용자 셸)로 **일부러 갈리게** 만든 축이다. **응답 캐시**는
// 두 축(① 재생성 가능한가 ② 갈린 사본이 각자 옳은가)을 **둘 다 통과해서** 갈려도 되지만,
// 아웃박스는 설계 §7 이 "재생성 불가한 유일한 자산"이라 부른 것을 담아 ①에서 탈락한다.
// 축 목록의 전문은 env.go 의 OutboxPath 주석에 있다.
//
// ★ 여기서 "캐시"를 한 부류로 읽지 마라 — 런처의 **바이너리 캐시**는 ①을 통과하고도
// ②에서 탈락해 고정 자리로 갔다(같은 파일의 TestBinCacheDir 이 그것을 잠근다).
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

// 바이너리 캐시 자리도 **채널 환경과 무관해야 한다.** 그리고 HOME 이 없으면 **안 짓는다.**
//
// ★ 이 시험이 07:15 사고의 Go 쪽 회귀를 막는다. 같은 소스가 CLAUDE_PLUGIN_DATA 자리와
// XDG_STATE_HOME 자리에 두 번 지어져 빌드 시각이 55분 어긋났고(16:10:44 ↔ 17:05:29),
// 그 창에서 한 응답의 서버 축과 렌더 축이 서로 다른 판을 봤다. 셸 런처 쪽 절반은
// plugin_test.go 가 잠근다 — FD_PRINT_BIN 이음매로 런처의 답을 받아 **이 함수의 답과
// 디렉토리째 견주고**, 표 안의 FD_STATE_DIR 갈래(공백·끝 슬래시)까지 같은 env 로 통과시킨다.
// ★ 그 대조가 없으면 "두 구현이 같은 답을 낸다"는 **아무도 안 재는 문장**이다 — 실제로
// 한동안 그랬고, 그사이 둘은 공백 처리에서 갈려 있었다(런처는 `[ -n ]` 만 보고 값을 그대로
// 썼고 이 함수는 TrimSpace 로 갈랐다). 잰 척하지 않으려면 대조가 실물로 있어야 한다.
//
// ★ HOME 이 없을 때 임시 디렉토리로 떨어지지 **않는** 것이 형제 셋(MachineIDPath·
// OutboxPath·ConfigPath)과 갈리는 유일한 갈래다. world-writable 부모에 실행 파일을 놓으면
// 남이 심어 둔 것을 내가 exec 하게 된다(LegacyOutboxDirs 가 tmp 를 뺀 것과 같은 축이고,
// 실행 파일은 그쪽보다 무겁다). 그래서 값을 안 내는 쪽을 골랐고, 대신 사유는 **반드시** 낸다.
//
// ★ 계약의 넷째 조항(⑷ 절대경로가 아니면 자리를 안 낸다)은 **아래 형제 시험**이 잠근다 —
// 이 표는 값마다 자리를 견주는 표라 '자리를 안 낸다'를 넣으면 HOME 없음 갈래와 섞인다.
func TestBinCacheDir(t *testing.T) {
	home := "/h"
	fixed := filepath.Join(home, ".cache", "flightdeck", "bin")
	cases := []struct {
		name    string
		env     map[string]string
		home    string
		want    string
		wantSrc string
	}{
		{"명시 지정이 이긴다",
			map[string]string{"FD_STATE_DIR": "/explicit", "CLAUDE_PLUGIN_DATA": "/plugin/data"},
			home, filepath.Join("/explicit", "bin"), "FD_STATE_DIR"},
		{"환경이 없으면 홈 아래 고정 자리",
			map[string]string{}, home, fixed, "~/.cache"},
		{"훅·MCP 채널이 이기지 못한다",
			map[string]string{"CLAUDE_PLUGIN_DATA": "/plugin/data"}, home, fixed, "~/.cache"},
		{"사용자 셸 채널이 이기지 못한다",
			map[string]string{"XDG_STATE_HOME": "/xdg/state"}, home, fixed, "~/.cache"},
		{"둘 다 있어도 이기지 못한다",
			map[string]string{"CLAUDE_PLUGIN_DATA": "/plugin/data", "XDG_STATE_HOME": "/xdg/state"},
			home, fixed, "~/.cache"},
		// ★ 아래 넷은 **FD_STATE_DIR 를 읽는 법**의 갈래다 — 계약 전문(⑴트림 ⑵트림 후 비면
		//   미설정 ⑶끝 슬래시는 자리를 안 바꾼다)과 **갈렸을 때 무엇이 틀어지는지**는 env.go 의
		//   BinCacheDir 주석 한 자리에 있다. 여기서 잠그는 것은 그 계약의 **Go 쪽 절반**이고,
		//   나머지 절반(셸 런처)이 같은 답을 내는지는 plugin_test.go 가 런처를 실제로 돌려
		//   견준다. 공백 갈래를 표 안에 두는 이유: 실제로 갈려 있던 것이 공백류뿐이었다.
		{"앞뒤 공백은 값의 일부가 아니다",
			map[string]string{"FD_STATE_DIR": " /x "}, home, filepath.Join("/x", "bin"), "FD_STATE_DIR"},
		{"끝 슬래시는 자리를 안 바꾼다",
			map[string]string{"FD_STATE_DIR": "/x/"}, home, filepath.Join("/x", "bin"), "FD_STATE_DIR"},
		{"공백뿐인 값은 미설정과 같다",
			map[string]string{"FD_STATE_DIR": "  "}, home, fixed, "~/.cache"},
		{"빈 값도 미설정과 같다",
			map[string]string{"FD_STATE_DIR": ""}, home, fixed, "~/.cache"},
		// ★ 이 줄은 표 밖의 예외가 아니라 표 안의 판정이다 — 앞선 셋과 갈리는 자리라 더 그렇다.
		{"HOME 이 없으면 자리를 안 낸다", map[string]string{}, "", "", "HOME"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, src := BinCacheDir(envOf(c.env), c.home)
			if got != c.want {
				t.Fatalf("자리가 %q 다, %q 를 기대했다 — 바이너리 자리가 채널의 함수가 되면\n"+
					"같은 소스가 두 번 지어지고 둘 중 하나가 최신인 척하는 옛 코드가 된다", got, c.want)
			}
			if strings.TrimSpace(src) == "" {
				t.Fatal("출처가 비었다 — '왜 여기냐'에 답할 자리가 없다")
			}
			if !strings.Contains(src, c.wantSrc) {
				t.Fatalf("사유에 %q 가 없다: %q", c.wantSrc, src)
			}
		})
	}

	// (빈 값·공백뿐인 값은 **표 안으로 옮겼다** — 그 판정은 이제 런처와 맺은 계약의 한 갈래라
	//  형제 갈래들과 같은 자리에서 읽혀야 한다. 여기 사본을 남기면 한쪽만 고치는 날 갈린다.)

	// 표 밖 ①: HOME 이 없고 **채널 환경만** 있을 때도 자리를 안 낸다.
	// 채널 환경이 폴백 자격을 얻는 순간, 이 함수가 닫은 55분 창이 그대로 다시 열린다.
	for _, e := range []map[string]string{
		{"CLAUDE_PLUGIN_DATA": "/plugin/data"},
		{"XDG_STATE_HOME": "/xdg/state"},
		{"CLAUDE_PLUGIN_DATA": "/plugin/data", "XDG_STATE_HOME": "/xdg/state"},
	} {
		if got, src := BinCacheDir(envOf(e), ""); got != "" {
			t.Fatalf("HOME 이 없는데 채널 환경(%v)이 자리를 만들었다: %q (%s)", e, got, src)
		}
	}

	// 표 밖 ②: HOME 이 없을 때 **임시 디렉토리로 떨어지지 않는다.** 형제 셋과 갈리는 자리라
	// 못 박아 둔다 — 나중에 "일관성"을 이유로 tmp 폴백을 되살리면 여기서 걸린다.
	dir, src := BinCacheDir(envOf(map[string]string{}), "")
	if dir != "" {
		t.Fatalf("HOME 이 없는데 자리를 냈다(%q) — world-writable 부모에 실행 파일을 놓는 길이다", dir)
	}
	if strings.Contains(src, filepath.Clean(os.TempDir())) || strings.Contains(src, "임시 디렉토리 —") {
		t.Fatalf("사유가 임시 디렉토리를 자리로 약속한다: %q", src)
	}

	// 표 밖 ③: **파일 이름을 답하지 않는다.** 소스 트리를 접은 키의 주인은 셸 런처 하나뿐이고,
	// 여기 사본을 두면 한쪽만 고칠 때 조용히 어긋난다(client.go newClient 주석의 규율).
	got, _ := BinCacheDir(envOf(map[string]string{}), home)
	if filepath.Base(got) != "bin" {
		t.Fatalf("디렉토리가 아니라 %q 를 냈다 — 키 규칙은 런처가 유일하게 갖는다", got)
	}
	if strings.Contains(filepath.Base(got), "fd-") {
		t.Fatalf("키 규칙이 Go 로 새어 나왔다: %q", got)
	}
}

// 계약의 **넷째 조항**(⑷ 절대경로가 아니면 자리를 안 낸다)을 잠근다.
//
// ★ 위 표와 왜 갈라 두는가. 저 표는 "이 값에 이 자리를 낸다"를 재는데 여기서 재는 것은
// **자리를 안 내는 것**이라, 같은 표에 넣으면 `want: ""` 한 줄이 'HOME 이 없다' 갈래와
// 구분이 안 된다(사유가 갈리는 것이 이 갈래의 전부다).
//
// ★ 이 조항은 한동안 **런처에만** 있었다(bin/fd 의 case 문). 그동안 Go 는 `relpath/bin`·
// `~/x/bin` 을 자리로 답했고, 그 값이 그대로 화면까지 갔다 — `바이너리 캐시 relpath/bin
// (FD_STATE_DIR (명시 지정))`. 런처가 categorically 거절하는 자리를 사실처럼 찍은 것이다.
// 표에 절대경로만 있어서 갈래 자체가 한 번도 안 평가됐다.
//
// ★ 사유가 **축을 이름으로 말하는지**까지 잰다. "상대경로다"만으로는 FD_STATE_DIR 를
// 고쳐야 하는지 HOME 을 고쳐야 하는지 모르고, 이 함수의 사유는 값이 없을 때 사람에게
// 남는 유일한 것이다(doctor 가 값 자리에 사유를 찍는다 — cmds.go 의 「바이너리 캐시 없음」).
func TestBinCacheDirRefusesRelativePlaces(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		home    string
		wantSrc string // 사유가 반드시 이름으로 말해야 하는 축
	}{
		// 틸드·변수를 안 펴는 env 주입에서 실제로 오는 값들이다(런처는 셸이 펴 주지만,
		// hooks.json·systemd·컨테이너 env 는 안 편다).
		{"맨 상대경로", map[string]string{"FD_STATE_DIR": "relpath"}, "/h", "FD_STATE_DIR"},
		{"안 펴진 틸드", map[string]string{"FD_STATE_DIR": "~/x"}, "/h", "FD_STATE_DIR"},
		{"안 펴진 변수", map[string]string{"FD_STATE_DIR": "$HOME/x"}, "/h", "FD_STATE_DIR"},
		{"점 상대경로", map[string]string{"FD_STATE_DIR": "./x"}, "/h", "FD_STATE_DIR"},
		{"부모 상대경로", map[string]string{"FD_STATE_DIR": ".."}, "/h", "FD_STATE_DIR"},
		{"공백을 뗀 뒤에도 상대경로", map[string]string{"FD_STATE_DIR": "  rel  "}, "/h", "FD_STATE_DIR"},
		// HOME 갈래에도 같은 관문이 선다 — 런처의 case 문도 binroot 하나만 본다.
		{"HOME 이 상대경로", map[string]string{}, "relhome", "HOME"},
		{"HOME 이 안 펴진 틸드", map[string]string{}, "~", "HOME"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, src := BinCacheDir(envOf(c.env), c.home)
			if dir != "" {
				t.Fatalf("상대경로에 자리를 냈다: %q (%s)\n"+
					"런처는 이 자리에 안 짓는다 — doctor 가 영영 안 생길 자리를 찍고,\n"+
					"pruneBinCache 가 프로세스 cwd 기준으로 남의 fd-* 를 지운다", dir, src)
			}
			if !strings.Contains(src, c.wantSrc) {
				t.Fatalf("사유가 어느 축을 고쳐야 하는지 안 말한다(%q 가 없다): %q", c.wantSrc, src)
			}
			if !strings.Contains(src, "절대경로") {
				t.Fatalf("사유가 '왜 안 냈는지'를 안 말한다: %q", src)
			}
		})
	}

	// 대조 전제 — 절대경로에서는 **그대로 자리를 낸다.** 이 줄이 없으면 위 단정은
	// "이 함수가 늘 빈 문자열을 낸다"로도 통과한다.
	if dir, src := BinCacheDir(envOf(map[string]string{"FD_STATE_DIR": "/x"}), "/h"); dir != filepath.Join("/x", "bin") {
		t.Fatalf("대조가 깨졌다 — 절대경로인데 자리를 %q 로 냈다(%s)", dir, src)
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

// 옛 **바이너리** 자리 목록. doctor 가 이 자리들을 stat 해서 남은 것을 말한다.
//
// ★ 이 목록은 자리의 주인(BinCacheDir)과 **경쟁하지 않는다.** 답하는 질문이 다르다 —
// 저쪽은 "지금 어디에 짓나", 이쪽은 "예전에 어디에 지었나". 그래서 자리가 또 옮겨지는 날
// 사본이라면 따라가야 하지만 이 목록은 옛 항목을 **그대로 들고 있어야** 한다.
// LegacyOutboxDirs 가 OutboxPath 에 대해 갖는 관계와 같다.
//
// ★ **채택이 없다는 것이 이 표가 지키는 핵심**이다. MachineIDPath 가 "후보 목록에서
// 물려받으면 채택 순서가 채널마다 갈린다"로 거절한 그 결함은 목록에서 **하나를 골라
// 답으로 삼을 때** 선다. 여기는 셋을 다 낸다 — 그래서 채널마다 줄이 달라지는 것은 상태의
// 갈림이 아니라 시야의 차이이고, 그 시야는 doctor 가 화면에 적는다.
func TestLegacyBinDirsCoversTheOldLadderAndTheAbandonedFixedPlace(t *testing.T) {
	const home = "/h"
	fixed := filepath.Join(home, ".cache", "flightdeck", "bin")
	cases := []struct {
		name   string
		env    map[string]string
		home   string
		target string
		want   []string
	}{
		{
			// 훅·MCP 채널과 셸 채널이 각자 남긴 자리 + 옛 런처의 마지막 사다리 칸.
			name:   "옛 사다리 셋을 다 낸다",
			env:    map[string]string{"CLAUDE_PLUGIN_DATA": "/plugin/data", "XDG_STATE_HOME": "/xdg/state"},
			home:   home,
			target: fixed,
			want: []string{
				filepath.Join("/plugin/data", "flightdeck", "bin"),
				filepath.Join("/xdg/state", "flightdeck", "bin"),
				filepath.Join(home, ".local", "state", "flightdeck", "bin"),
			},
		},
		{
			// 이 머신의 실제 모양이다(2026-08-07 실측: 두 환경변수가 다 미설정인 셸).
			name:   "채널 환경이 없으면 홈 갈래 하나",
			env:    map[string]string{},
			home:   home,
			target: fixed,
			want:   []string{filepath.Join(home, ".local", "state", "flightdeck", "bin")},
		},
		{
			// ★ FD_STATE_DIR 를 **새로 켠** 사용자. 고정 자리가 통째로 버려지는데
			//   pruneBinCache 도 더는 그 자리를 안 훑어 keep(3)벌 ≈66MB 가 주인을 잃는다.
			name:   "FD_STATE_DIR 를 켜면 고정 자리도 옛 자리가 된다",
			env:    map[string]string{"FD_STATE_DIR": "/explicit"},
			home:   home,
			target: filepath.Join("/explicit", "bin"),
			want: []string{
				filepath.Join(home, ".local", "state", "flightdeck", "bin"),
				fixed,
			},
		},
		{
			// ★ HOME 이 없으면 홈 갈래도 고정 자리도 안 낸다. 옛 런처는 그 조건에서
			//   `${HOME:-/tmp}/.local/state/…` 로 실제로 지었지만 따라가지 않는다(표 밖 ①).
			name:   "HOME 이 없으면 채널 자리만",
			env:    map[string]string{"CLAUDE_PLUGIN_DATA": "/plugin/data"},
			home:   "",
			target: "",
			want:   []string{filepath.Join("/plugin/data", "flightdeck", "bin")},
		},
		{
			name:   "같은 자리를 두 축이 가리켜도 한 번만",
			env:    map[string]string{"CLAUDE_PLUGIN_DATA": "/same", "XDG_STATE_HOME": "/same"},
			home:   "",
			target: "",
			want:   []string{filepath.Join("/same", "flightdeck", "bin")},
		},
		{
			// 지금 쓰는 자리를 "옛 자리"로 찍으면 그 줄은 그 자리에서 곧바로 거짓이다.
			name:   "목표와 같은 자리는 뺀다",
			env:    map[string]string{"XDG_STATE_HOME": "/xdg/state"},
			home:   "",
			target: filepath.Join("/xdg/state", "flightdeck", "bin"),
			want:   nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := LegacyBinDirs(envOf(c.env), c.home, c.target)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("옛 바이너리 자리 목록이\n  %v\n여야 하는데\n  %v\n다", c.want, got)
			}
		})
	}
}

// ★ 고정 자리 갈래는 규칙을 **베끼지 않고 주인에게 묻는다.**
//
// 이 단정이 리터럴이 아니라 BinCacheDir 의 답인 것이 요점이다 — 손으로 조립한 사본은
// 오늘은 같은 값을 내지만 자리가 다시 옮겨지는 날 조용히 갈린다. 그때 이 시험이 걸린다.
// 옛 사다리 셋은 반대로 리터럴이 맞다: 그쪽은 **얼어붙은 과거**라 지금 사는 함수를 따라가면
// 안 된다. 두 갈래의 규칙이 다른 이유가 이것이고, 위 표가 그쪽을 리터럴로 잠근다.
func TestLegacyBinDirsAsksTheOwnerForTheAbandonedFixedPlace(t *testing.T) {
	const home = "/h"
	owner, src := BinCacheDir(envOf(map[string]string{}), home)
	if owner == "" {
		t.Fatalf("대조 전제가 깨졌다 — 주인이 자리를 안 냈다(%s)", src)
	}
	got := LegacyBinDirs(envOf(map[string]string{"FD_STATE_DIR": "/explicit"}), home,
		filepath.Join("/explicit", "bin"))
	for _, d := range got {
		if d == owner {
			return
		}
	}
	t.Fatalf("FD_STATE_DIR 를 켰는데 주인이 답한 고정 자리(%s)가 후보에 없다 — "+
		"그 자리에 남은 keep(3)벌 ≈66MB 를 아무도 말하지 않게 된다: %v", owner, got)
}

// 옛 바이너리 자리 목록은 임시 디렉토리를 **절대** 후보로 얹지 않는다.
//
// ★ 옛 런처는 HOME 이 없으면 `${HOME:-/tmp}/.local/state/flightdeck/bin` 에 **실제로 지었다**
// (개정 전 bin/fd). 그러니 이것은 "그 자리에 없다"가 아니라 **일부러 안 본다**는 판정이다.
// 이 줄이 읽기만 한다고 안전한 것이 아니다 — 나르는 처방이 사람의 `rm` 이고, `/tmp` 는
// 부모가 world-writable 이라 남이 그 경로를 먼저 만들어 둘 수 있다. 그러면 doctor 가 남의
// 자리를 가리키며 "지우려면 사람이 지운다"를 찍고, 개수·크기도 심은 쪽이 정한다.
// LegacyOutboxDirs 의 3번 기록과 같은 축이고, 얻는 쪽(HOME 없이 fd 가 돈 머신)은 이 제품의
// 채널 셋(훅·MCP·셸)이 전부 HOME 을 가져 가설이다.
func TestLegacyBinDirsNeverScansTempDir(t *testing.T) {
	tmp := filepath.Clean(os.TempDir())
	// HOME 이 없는 갈래 — 옛 런처가 tmp 로 떨어지던 바로 그 조건이다.
	if got := LegacyBinDirs(envOf(map[string]string{}), "", ""); len(got) != 0 {
		t.Fatalf("HOME 도 채널 환경도 없는데 후보를 냈다: %v", got)
	}
	// 채널 환경만 있는 갈래에서도 tmp 가 섞이지 않는다.
	got := LegacyBinDirs(envOf(map[string]string{
		"CLAUDE_PLUGIN_DATA": "/plugin/data",
		"XDG_STATE_HOME":     "/xdg/state",
	}), "", "")
	if len(got) == 0 {
		t.Fatal("대조가 깨졌다 — 채널 환경을 줬는데 후보가 0개다")
	}
	for _, d := range got {
		if filepath.Clean(d) == tmp || strings.HasPrefix(filepath.Clean(d), tmp+string(filepath.Separator)) {
			t.Fatalf("후보에 임시 디렉토리 아래 %q 가 들어 있다 — 남이 심어 둔 자리를 "+
				"doctor 가 '사람이 지워라'로 가리키게 된다: %v", d, got)
		}
	}
}

// 크기 문구는 **10^6 기준이고 단위를 이름으로 적는다.**
//
// ★ 설계 §7 이 이 축의 실측을 적어 둔 자가 10^6 이라(22.1MB · 22.0MB) 두 화면이 같은 파일에
// 같은 숫자를 말한다. `du -h` 는 2^20 이라 같은 파일을 `21M` 로 내는데, 단위 없이 숫자만
// 찍으면 사람이 두 자를 견주다 "파일이 두 개인가"로 읽는다.
//
// ★ 1MB 미만 갈래가 표에 있는 이유: 옛 자리에 22MB 가 아니라 몇 바이트가 남아 있으면
// 그것은 버려진 산출물이 아니라 **깨진 빌드의 잔해**(런처의 `mv` 가 실패한 자리)이고,
// 사람이 할 일이 다르다. 전부 "0.0MB" 로 접히면 그 구별이 사라진다.
func TestHumanBytesUsesDecimalMegabytesAndNamesTheUnit(t *testing.T) {
	cases := []struct {
		name string
		in   int64
		want string
	}{
		{"0", 0, "0바이트"},
		{"작은 잔해", 12, "12바이트"},
		{"KB 경계", 1_000, "1.0KB"},
		{"MB 경계", 1_000_000, "1.0MB"},
		// 2026-08-07 이 머신 실측 잔존 두 벌. 2^20 이면 21.1·21.0 이 된다.
		{"실측 잔존 ①", 22_144_360, "22.1MB"},
		{"실측 잔존 ②", 22_049_451, "22.0MB"},
		// 실물에서 안 오지만 0 과 뭉개지면 안 된다(-0.0MB 가 된다).
		{"음수", -1, "-1바이트(음수다)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := humanBytes(c.in); got != c.want {
				t.Fatalf("humanBytes(%d) = %q, %q 를 기대했다", c.in, got, c.want)
			}
		})
	}
}

// TestDisplayWidthCountsHangulAsTwo 는 runeDisplayWidth/displayWidth 의 계약이다.
//
// ★ 리뷰가 잡은 결함: `fmt` 의 `%-Ns` 는 룬 수로 채우는데 한글은 터미널에서 2칸을
// 먹는다. displayWidth 가 그 계산을 정확히 하는지가 padDisplay/padDisplayRight 전체의
// 기초다 — 여기가 틀리면 아래 두 시험도 뜻 없이 통과한다.
func TestDisplayWidthCountsHangulAsTwo(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"빈 문자열", "", 0},
		{"ASCII 만", "junk", 4},
		{"한글 넷", "프로젝트", 8},       // 4룬 × 2
		{"한글 둘", "보관", 4},         // 2룬 × 2
		{"한글 하나", "핀", 2},         // 1룬 × 2
		{"섞임", "junk-1", 6},       // ASCII 뿐이라 룬 수와 같다
		{"말줄임표 포함", "junk-1…", 7}, // clip 이 붙이는 "…" 는 기본 갈래(폭 1)를 탄다
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := displayWidth(c.in); got != c.want {
				t.Fatalf("displayWidth(%q) = %d, %d 를 기대했다", c.in, got, c.want)
			}
		})
	}
}

// TestPadDisplayAlignsHangulAndASCIIToTheSameVisualColumn 은 padDisplay/padDisplayRight
// 가 한글 헤더와 ASCII 데이터를 **같은 표시 폭**으로 맞추는지 본다.
//
// ★ 리뷰가 실측한 어긋남을 그대로 재현해 잠근다: `%-34s` 로 "프로젝트"(4룬)를 채우면
// 표시 폭 38칸, "junk"(4룬)를 채우면 34칸이 나왔다(4칸 밀림). padDisplay 는 **표시 폭**
// 34칸으로 정확히 채워야 한다 — 두 값이 반환한 문자열의 displayWidth 가 서로 같아야
// "칸이 안 어긋난다"는 것을 뜻한다.
func TestPadDisplayAlignsHangulAndASCIIToTheSameVisualColumn(t *testing.T) {
	header := padDisplay("프로젝트", 34)
	data := padDisplay("junk", 34)
	if w := displayWidth(header); w != 34 {
		t.Fatalf("padDisplay(%q, 34) 의 표시 폭이 %d다, 34 를 기대했다", "프로젝트", w)
	}
	if w := displayWidth(data); w != 34 {
		t.Fatalf("padDisplay(%q, 34) 의 표시 폭이 %d다, 34 를 기대했다", "junk", w)
	}
	if displayWidth(header) != displayWidth(data) {
		t.Fatalf("헤더 표시 폭 %d ≠ 데이터 표시 폭 %d — 표가 어긋난다",
			displayWidth(header), displayWidth(data))
	}

	// 오른쪽 정렬(숫자 칸)도 같은 계약이다: "상태" 헤더(2룬)와 "-"(1룬) 데이터가
	// 같은 표시 폭 6으로 맞아야 한다.
	stateHeader := padDisplay("상태", 6)
	stateData := padDisplay("-", 6)
	if displayWidth(stateHeader) != 6 || displayWidth(stateData) != 6 {
		t.Fatalf("상태 칸 표시 폭이 6 이 아니다: 헤더=%d 데이터=%d",
			displayWidth(stateHeader), displayWidth(stateData))
	}

	itemsHeader := padDisplayRight("항목", 6)
	itemsData := padDisplayRight("0", 6)
	if displayWidth(itemsHeader) != 6 || displayWidth(itemsData) != 6 {
		t.Fatalf("항목 칸 표시 폭이 6 이 아니다: 헤더=%d 데이터=%d",
			displayWidth(itemsHeader), displayWidth(itemsData))
	}
}

// TestRevParseCoordsKeepsTheArgumentOrder 는 두 값이 뒤바뀌지 않음을 못 박는다.
//
// ★ 이 순수 함수가 따로 있는 이유가 이 시험이다. 호출부에서 줄 번호로 집으면 인자를
// 하나 더하는 순간 두 값이 조용히 뒤바뀌고, 그러면 세션의 워크트리 자리에 `.git` 경로가
// 들어간다 — 3중키가 통째로 어긋나는데 어느 화면에도 안 뜬다.
func TestRevParseCoordsKeepsTheArgumentOrder(t *testing.T) {
	cases := []struct {
		name                string
		in                  string
		wantCommon, wantTop string
	}{
		{"주 워크트리", "/repo/.git\n/repo\n", "/repo/.git", "/repo"},
		{"링크된 워크트리", "/repo/.git\n/repo/.flightdeck/worktrees/wt-a\n",
			"/repo/.git", "/repo/.flightdeck/worktrees/wt-a"},
		// bare 저장소는 --show-toplevel 이 빈 줄을 낸다. 그때 워크트리는 접지 않는 것이 맞다.
		{"줄이 하나뿐", "/repo.git\n", "/repo.git", ""},
		{"빈 출력", "", "", ""},
		{"공백만", "  \n\n  \n", "", ""},
		{"앞뒤 공백", "  /repo/.git  \n  /repo  \n", "/repo/.git", "/repo"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			common, top := RevParseCoords(c.in)
			if common != c.wantCommon || top != c.wantTop {
				t.Fatalf("RevParseCoords(%q) = (%q, %q), 기대 (%q, %q)",
					c.in, common, top, c.wantCommon, c.wantTop)
			}
		})
	}
}
