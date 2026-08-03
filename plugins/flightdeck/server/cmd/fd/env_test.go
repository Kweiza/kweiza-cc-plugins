package main

import (
	"path/filepath"
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
