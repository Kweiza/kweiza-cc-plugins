package judge

import (
	"net/url"
	"testing"
)

// TestRelativeTo 는 깊이 셈을 표로 잠근다.
func TestRelativeTo(t *testing.T) {
	cases := []struct{ from, to, want string }{
		// 로그인 넷 — 기준이 언제나 /login · /logout 이라 깊이가 0 이다.
		{"/login", "/", "./"},
		{"/login", "/?project=kweiza", "./?project=kweiza"},
		{"/login", "/events", "./events"},
		{"/logout", "/", "./"},
		// 화면 쓰기 — /actions/* 는 깊이가 1 이다.
		{"/actions/reclaim", "/?notice=reclaim", "../?notice=reclaim"},
		{"/actions/reclaim", "/login", "../login"},
		{"/actions/", "/login", "../login"}, // 뒤 슬래시면 마디가 하나 더다
		{"/actions/reclaim/", "/", "../../"},
		{"/api/v1/items/next", "/login", "../../../login"},
		{"/a//b", "/login", "../../login"}, // 빈 마디도 해석이 한 마디로 센다
		// mux 가 정규화 요청(`/actions/../` 같은)에 내는 307 의 Location 이 여기로 들어온다 —
		// from 자체에 점 마디가 있다. 브라우저는 base 를 remove_dot_segments 로 정규화한
		// **뒤** 상대 참조를 푸므로, from 도 그렇게 셈해야 한다.
		{"/actions/../", "/", "./"}, // remove_dot_segments("/actions/../") == "/" 라 깊이 0
		// 뒤 슬래시 보존 케이스 — remove_dot_segments("/actions/reclaim/../") 는 "/actions/"
		// 다(path.Clean 이면 "/actions" 가 되어 깊이가 하나 어긋난다). 깊이 1이라
		// "/actions/" 와 같은 "../login" 이어야 한다({"/actions/", "/login", "../login"} 과 동일).
		{"/actions/reclaim/../", "/login", "../login"},
		// 못 읽은 from 은 뿌리로 접는다 — 과하게 올라가면 접두 밖으로 나간다.
		{"", "/login", "./login"},
		{"*", "/login", "./login"},
		// to 가 이 서버 안의 절대경로가 아니면 뿌리로 접는다.
		{"/login", "//evil.example/x", "./"},
		{"/login", "https://evil.example/x", "./"},
		{"/login", "", "./"},
		{"/login", "relative", "./"},
		{"/login", "/../../etc", "./"},        // 점 마디 — 호스트는 그대로지만 접두 밖으로 나간다
		{"/actions/reclaim", "/a/../b", "./"}, // 중간의 점 마디도 막는다
	}
	for _, c := range cases {
		if got := RelativeTo(c.from, c.to); got != c.want {
			t.Errorf("RelativeTo(%q, %q) = %q, 기대 %q", c.from, c.to, got, c.want)
		}
	}
}

// TestRelativeToLandsInsideProxyPrefix 는 그 값이 **접두 안에 착지하는지** 본다.
//
// ★ 기대값을 손으로 적는 것만으로는 부족하다 — 표가 틀리면 코드와 함께 틀린다.
// 그래서 각 줄을 접두가 붙은 문서 URL 에 실제로 해석한다(RFC 3986 그대로인
// url.ResolveReference). 브라우저가 하는 계산이 그것이다.
func TestRelativeToLandsInsideProxyPrefix(t *testing.T) {
	const prefix = "/dcp-dev-board"
	cases := []struct{ from, to, wantPath, wantQuery string }{
		{"/login", "/", "/", ""},
		{"/login", "/?project=kweiza", "/", "project=kweiza"},
		{"/logout", "/", "/", ""},
		{"/actions/reclaim", "/?notice=reclaim", "/", "notice=reclaim"},
		{"/actions/reclaim", "/login", "/login", ""},
		{"/api/v1/items/next", "/login", "/login", ""},
		// mux 의 307 이 실어 오는 점 마디 from — TestRelativeTo 의 같은 줄과 짝이다.
		{"/actions/../", "/", "/", ""},
		{"/actions/reclaim/../", "/login", "/login", ""},
	}
	for _, c := range cases {
		base, err := url.Parse("http://fd.example" + prefix + c.from)
		if err != nil {
			t.Fatalf("문서 URL 파싱 실패(%q): %v", c.from, err)
		}
		ref, err := url.Parse(RelativeTo(c.from, c.to))
		if err != nil {
			t.Fatalf("상대 참조 파싱 실패(%q→%q): %v", c.from, c.to, err)
		}
		got := base.ResolveReference(ref)
		if want := prefix + c.wantPath; got.Path != want {
			t.Errorf("%q 에서 %q 로 가면 %q 에 착지한다 — %q 여야 한다(접두 밖이면 배포가 깨진다)",
				c.from, c.to, got.Path, want)
		}
		if got.RawQuery != c.wantQuery {
			t.Errorf("%q 에서 %q 로 갈 때 질의가 %q 다 — %q 여야 한다",
				c.from, c.to, got.RawQuery, c.wantQuery)
		}
	}
}

// TestRelativeToWithoutPrefixIsUnchanged 는 접두가 **없는** 배포가 그대로인지 본다.
//
// ★ 이 축이 없으면 접두 대응이 기본 배포를 깨뜨려도 안 보인다. 이 서버의 기본
// 배포는 포트 직결(compose.yaml)이고 그쪽이 다수다.
func TestRelativeToWithoutPrefixIsUnchanged(t *testing.T) {
	cases := []struct{ from, to, wantPath string }{
		{"/login", "/", "/"},
		{"/login", "/?project=kweiza", "/"},
		{"/actions/reclaim", "/?notice=reclaim", "/"},
	}
	for _, c := range cases {
		base, err := url.Parse("http://fd.example" + c.from)
		if err != nil {
			t.Fatalf("문서 URL 파싱 실패(%q): %v", c.from, err)
		}
		ref, err := url.Parse(RelativeTo(c.from, c.to))
		if err != nil {
			t.Fatalf("상대 참조 파싱 실패(%q→%q): %v", c.from, c.to, err)
		}
		if got := base.ResolveReference(ref); got.Path != c.wantPath {
			t.Errorf("접두 없는 배포에서 %q→%q 가 %q 에 착지한다 — %q 여야 한다",
				c.from, c.to, got.Path, c.wantPath)
		}
	}
}
