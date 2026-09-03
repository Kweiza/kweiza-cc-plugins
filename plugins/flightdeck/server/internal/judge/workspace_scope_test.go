package judge

import (
	"reflect"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

func roster() Roster {
	return Roster{Root: "cp-root", Members: map[string]string{
		"search-api": "context-platform-search-api",
		"search":     "context-platform-search", // ★ 위 이름의 진접두다 — 마디 경계 시험의 축
		"inner":      "nested/inner",
		"outer":      "nested",
	}}
}

// 멤버가 0건이면 아무것도 안 바꾼다 — 단일 레포와 구별할 실익이 없다.
func TestRosterActiveNeedsBothRootAndMembers(t *testing.T) {
	cases := []struct {
		name string
		r    Roster
		want bool
	}{
		{"빈 것", Roster{}, false},
		{"루트만", Roster{Root: "cp-root"}, false},
		{"루트 + 빈 맵", Roster{Root: "cp-root", Members: map[string]string{}}, false},
		{"멤버만(루트 없음)", Roster{Members: map[string]string{"a": "a"}}, false},
		{"둘 다", roster(), true},
	}
	for _, c := range cases {
		if got := c.r.Active(); got != c.want {
			t.Errorf("%s: Active()=%v, 기대 %v", c.name, got, c.want)
		}
	}
}

// 명부 밖 이름은 **거절**의 근거다 — 오타가 조용히 새 프로젝트를 만드는 것을 막는다.
func TestRosterKnowsRootAndMembersOnly(t *testing.T) {
	r := roster()
	for _, ok := range []string{"cp-root", "search-api", "inner", "outer"} {
		if !r.Knows(ok) {
			t.Errorf("Knows(%q)=false — 명부 안이다", ok)
		}
	}
	for _, no := range []string{"", "cp-roo", "search-apis", "kweiza-cc-plugins", "nested/inner"} {
		if r.Knows(no) {
			t.Errorf("Knows(%q)=true — 명부 밖이다", no)
		}
	}
}

// 순서가 고정이어야 화면과 시험이 같은 입력에 같은 답을 본다.
func TestRosterMemberIDsAreSortedAndExcludeRoot(t *testing.T) {
	got := roster().MemberIDs()
	want := []string{"inner", "outer", "search", "search-api"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MemberIDs()=%v, 기대 %v", got, want)
	}
}

// 경로 귀속 — **마디 경계로만** 자르고, 겹치면 가장 긴 멤버가 이긴다.
func TestRosterAttributePath(t *testing.T) {
	r := roster()
	cases := []struct {
		name    string
		rel     string
		project string
		member  string
		ok      bool
	}{
		{"멤버 안", "context-platform-search-api/server/foo.go", "search-api", "server/foo.go", true},
		{
			// ★ **이 줄이 마디 경계 규칙을 재는 자리다.** `context-platform-search-api` 는
			//   이 경로의 진접두라, 접두 문자열 비교면 명부에 없는 레포의 파일이 그 멤버로
			//   귀속된다. 앞선 판의 시험은 `…-search-api/x.go` 만 재서 이 변이를 못 잡았다
			//   — 그 경로는 «가장 긴 멤버가 이긴다» 규칙이 우연히 같은 답을 내 준다.
			"이름이 더 긴 남의 레포를 안 삼킨다", "context-platform-search-api-v2/x.go", "", "", false,
		},
		{"진접두 멤버가 있어도 제 것은 제 것", "context-platform-search-api/x.go", "search-api", "x.go", true},
		{"짧은 멤버 자기 것", "context-platform-search/y.go", "search", "y.go", true},
		{"중첩은 안쪽이 이긴다", "nested/inner/a/b.go", "inner", "a/b.go", true},
		{"중첩 바깥", "nested/other.go", "outer", "other.go", true},
		{"멤버 밖은 루트에 남는다", "README.md", "", "", false},
		{"멤버 루트 자신은 안 접는다", "nested/inner", "", "", false},
		{"빈 값", "", "", "", false},
		{"절대경로", "/etc/passwd", "", "", false},
		{"루트 밖", "../elsewhere/a.go", "", "", false},
		{"./ 접두는 벗긴다", "./context-platform-search-api/z.go", "search-api", "z.go", true},
	}
	for _, c := range cases {
		p, m, ok := r.AttributePath(c.rel)
		if ok != c.ok || p != c.project || m != c.member {
			t.Errorf("%s: AttributePath(%q) = (%q,%q,%v), 기대 (%q,%q,%v)",
				c.name, c.rel, p, m, ok, c.project, c.member, c.ok)
		}
	}
}

// 워크스페이스가 아니면 정규화는 항등이다 — 이 플러그인 레포 자신이 그 경우다.
func TestScopeResourceIsIdentityOutsideWorkspace(t *testing.T) {
	for _, res := range []string{"env:dell", "landing", "path:a/b.go", ""} {
		if got := ScopeResource(res, "kweiza-cc-plugins", ""); got != "kweiza-cc-plugins" {
			t.Errorf("ScopeResource(%q, 루트 없음) = %q — 안 바꿔야 한다", res, got)
		}
	}
}

// 배타 자원은 루트 스코프로 접히고, `landing` 과 `path:` 는 레포별로 남는다.
func TestScopeResourceFoldsExclusiveNamesOnly(t *testing.T) {
	cases := []struct {
		res  string
		want string
		why  string
	}{
		{"env:dell", "cp-root", "스테이징은 워크스페이스에 하나다"},
		{"device:pixel8", "cp-root", "전용기도 같다"},
		{"landing", "search-api", "랜딩 명제는 레포마다 별개다 — 17개가 병렬로 랜딩하는 것이 정상이다"},
		{"path:server/foo.go", "search-api", "두 멤버의 같은 상대경로는 다른 파일이다"},
	}
	for _, c := range cases {
		if got := ScopeResource(c.res, "search-api", "cp-root"); got != c.want {
			t.Errorf("ScopeResource(%q) = %q, 기대 %q — %s", c.res, got, c.want, c.why)
		}
	}
	// 루트 자신이 잡는 것도 같은 줄이다 — 아니면 루트와 멤버가 서로를 안 막는다.
	if got := ScopeResource("env:dell", "cp-root", "cp-root"); got != "cp-root" {
		t.Errorf("루트가 잡은 env:dell 의 스코프 = %q", got)
	}
}

// 자원 집합 하나가 두 스코프에 걸치면 **접지 않고 이름을 댄다.**
//
// 접는 방법이 둘 다 틀리기 때문이다: 루트로 접으면 멤버 17개의 랜딩이 한 줄로
// 직렬화되고, 멤버로 접으면 스테이징 배타가 워크스페이스 밖으로 샌다.
func TestScopeSetRefusesToFoldMixedScopes(t *testing.T) {
	cases := []struct {
		name      string
		res       []string
		wantScope string
		wantMixed []string
	}{
		{"빈 집합", nil, "search-api", nil},
		{"랜딩만", []string{"landing"}, "search-api", nil},
		{"자원만", []string{"env:dell"}, "cp-root", nil},
		{"자원 둘", []string{"env:dell", "device:pixel8"}, "cp-root", nil},
		{"랜딩 + 자원", []string{"landing", "env:dell"}, "search-api", []string{"landing", "env:dell"}},
		{"자원 + 랜딩(순서 반대)", []string{"env:dell", "landing"}, "cp-root", []string{"env:dell", "landing"}},
		{"path: + 자원", []string{"path:a/b.go", "env:dell"}, "search-api", []string{"path:a/b.go", "env:dell"}},
	}
	for _, c := range cases {
		scope, mixed := ScopeSet(c.res, "search-api", "cp-root")
		if scope != c.wantScope {
			t.Errorf("%s: scope=%q, 기대 %q", c.name, scope, c.wantScope)
		}
		if len(mixed) != len(c.wantMixed) {
			t.Errorf("%s: mixed=%v, 기대 %v", c.name, mixed, c.wantMixed)
			continue
		}
		for i := range mixed {
			if mixed[i] != c.wantMixed[i] {
				t.Errorf("%s: mixed=%v, 기대 %v", c.name, mixed, c.wantMixed)
				break
			}
		}
	}
	// ★ **갈릴 때 첫 자원도 함께 낸다.** 이름 하나만 대면 읽는 쪽이 "그 하나만 빼면
	//   되나"로 읽는데, 실제로는 두 부류가 한 줄에 있는 것이 문제다.
	_, mixed := ScopeSet([]string{"landing", "env:dell"}, "search-api", "cp-root")
	if len(mixed) < 2 {
		t.Fatalf("갈린 목록에 첫 자원이 안 들어갔다: %v", mixed)
	}
}

// 겹침 좌표 변환 — 이것이 없으면 같은 파일의 겹침이 **원리적으로** 안 보인다.
func TestRosterPathAsSeenFrom(t *testing.T) {
	r := roster()
	cases := []struct {
		name          string
		src, dst, rel string
		want          string
		ok            bool
	}{
		{"같은 프로젝트", "search-api", "search-api", "server/foo.go", "server/foo.go", true},
		{"멤버 → 루트", "search-api", "cp-root", "server/foo.go", "context-platform-search-api/server/foo.go", true},
		{"루트 → 멤버", "cp-root", "search-api", "context-platform-search-api/server/foo.go", "server/foo.go", true},
		{
			// 루트가 만진 자기 파일은 그 멤버 좌표로 옮길 수 없다 — 다른 파일이다.
			"루트 → 멤버(접두가 아니다)", "cp-root", "search-api", "README.md", "", false,
		},
		{
			// ★ 이 갈래를 억지로 맞추면 서로 무관한 두 레포의 같은 상대경로가 겹침으로 뜬다.
			"멤버 → 다른 멤버", "search-api", "search", "server/foo.go", "", false,
		},
		{"명부 밖", "남의-레포", "cp-root", "a.go", "", false},
		{"빈 경로", "search-api", "cp-root", "", "", false},
		{"./ 접두는 벗긴다", "search-api", "cp-root", "./x.go", "context-platform-search-api/x.go", true},
	}
	for _, c := range cases {
		got, ok := r.PathAsSeenFrom(c.src, c.dst, c.rel)
		if ok != c.ok || got != c.want {
			t.Errorf("%s: PathAsSeenFrom(%q,%q,%q) = (%q,%v), 기대 (%q,%v)",
				c.name, c.src, c.dst, c.rel, got, ok, c.want, c.ok)
		}
	}
	// 워크스페이스가 아니면 같은 프로젝트만 통과한다.
	var none Roster
	if _, ok := none.PathAsSeenFrom("a", "b", "x.go"); ok {
		t.Error("워크스페이스가 아닌데 좌표를 옮겼다")
	}
	if got, ok := none.PathAsSeenFrom("a", "a", "x.go"); !ok || got != "x.go" {
		t.Errorf("같은 프로젝트인데 안 통과했다: %q %v", got, ok)
	}
}

// 선행 키는 **같은 프로젝트면 예전과 같은 문자열**이어야 한다.
// 바뀌면 이미 저장된 판정이 통째로 「조회하지 않았다」가 된다.
func TestAfterItemKeyKeepsLegacyShape(t *testing.T) {
	if got := AfterItemKey(model.After{Item: "dep-1"}); got != "dep-1" {
		t.Fatalf("빈 프로젝트의 키가 %q — 예전과 같은 `dep-1` 이어야 한다", got)
	}
	if got := AfterItemKey(model.After{Item: "dep-1", Project: "member-a"}); got != "member-a/dep-1" {
		t.Fatalf("교차 프로젝트 키가 %q", got)
	}
}
