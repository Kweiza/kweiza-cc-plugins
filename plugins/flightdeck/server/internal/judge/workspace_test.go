package judge

import (
	"strings"
	"testing"
)

// 블록이 없으면 **아무 일도 안 일어난다** — 단일 레포 프로젝트 전건이 지금과 같이 돈다.
//
// Declared 가 false 인 것과 멤버가 0건인 것을 가르는 이유가 이 시험의 전부다:
// 그 둘이 접히면 «명부를 비웠다»가 «명부가 없다»와 같아져 캐시에 남은 옛 멤버를
// 영영 못 걷는다.
func TestParseWorkspaceUndeclaredIsNotAnError(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"빈 파일", ""},
		{"다른 키만", "schema: 1\nname: solo\ndefault_branch: main\n"},
		{"주석만", "# workspace: 이건 주석이다\n"},
		{"들여쓴 workspace 는 최상위가 아니다", "verify:\n  workspace:\n    members:\n      - path: x\n"},
	}
	for _, c := range cases {
		ws, err := ParseWorkspace(c.src)
		if err != nil {
			t.Errorf("%s: 오류가 나면 안 된다: %v", c.name, err)
		}
		if ws.Declared {
			t.Errorf("%s: Declared=true 였다 — 블록이 없으면 선언도 없다", c.name)
		}
		if len(ws.Members) != 0 {
			t.Errorf("%s: 멤버 %d건이 나왔다", c.name, len(ws.Members))
		}
	}
}

// 정상 명부를 읽는다. project 가 비면 path 의 마지막 마디가 id 다.
func TestParseWorkspaceReadsBlockMembers(t *testing.T) {
	src := `schema: 1
name: cp-root
workspace:
  members:
    - project: search-api
      path: context-platform-search-api
    - path: context-platform-docs        # project 생략 → basename
    - path: "nested/inner"
labels:
  values: [a, b]
`
	ws, err := ParseWorkspace(src)
	if err != nil {
		t.Fatalf("파싱 실패: %v", err)
	}
	if !ws.Declared {
		t.Fatal("Declared=false 다")
	}
	if len(ws.Members) != 3 {
		t.Fatalf("멤버 %d건 — 3건이어야 한다: %+v", len(ws.Members), ws.Members)
	}
	want := []struct{ id, path string }{
		{"search-api", "context-platform-search-api"},
		{"context-platform-docs", "context-platform-docs"},
		{"inner", "nested/inner"},
	}
	for i, w := range want {
		if got := MemberProjectID(ws.Members[i]); got != w.id {
			t.Errorf("멤버 %d: id=%q, 기대 %q", i, got, w.id)
		}
		if ws.Members[i].Path != w.path {
			t.Errorf("멤버 %d: path=%q, 기대 %q", i, ws.Members[i].Path, w.path)
		}
	}
	// ★ 그 뒤의 최상위 키(labels)가 명부에 안 새어 든다 — 블록 경계를 들여쓰기로 읽는다.
}

// `workspace:` 는 있는데 `members:` 가 없으면 **선언은 있었다**고 본다.
func TestParseWorkspaceDeclaredWithoutMembers(t *testing.T) {
	ws, err := ParseWorkspace("workspace:\n  # 아직 아무도 안 넣었다\n")
	if err != nil {
		t.Fatalf("오류: %v", err)
	}
	if !ws.Declared || len(ws.Members) != 0 {
		t.Fatalf("Declared=%v 멤버=%d — 선언 있음·멤버 0건이어야 한다", ws.Declared, len(ws.Members))
	}
}

// 못 읽는 꼴은 **거절한다**. 조용히 0건으로 접으면 사람은 "등록했는데 안 붙는다"만 본다.
func TestParseWorkspaceRefusesWhatItCannotRead(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantSub string
	}{
		{
			"플로우 스타일(최상위)",
			"workspace: {members: [{path: a}]}\n",
			"블록 스타일만 읽는다",
		},
		{
			"플로우 스타일(members)",
			"workspace:\n  members: [{path: a}]\n",
			"블록 시퀀스만 읽는다",
		},
		{
			"값이 플로우",
			"workspace:\n  members:\n    - path: [a, b]\n",
			"플로우 스타일",
		},
		{
			"탭 들여쓰기",
			"workspace:\n  members:\n    - path:\tx\n",
			"탭",
		},
		{
			"절대경로",
			"workspace:\n  members:\n    - path: /Users/kweiza/cp\n",
			"절대경로",
		},
		{
			"루트 밖",
			"workspace:\n  members:\n    - path: ../sibling\n",
			"루트 밖",
		},
		{
			"루트 자신",
			"workspace:\n  members:\n    - path: .\n",
			"루트 밖이거나 루트 자신",
		},
		{
			"path 없음",
			"workspace:\n  members:\n    - project: only-a-name\n",
			"path 가 없다",
		},
		{
			"시퀀스가 아닌 줄",
			"workspace:\n  members:\n    path: a\n",
			"시퀀스가 아닌 줄",
		},
	}
	for _, c := range cases {
		_, err := ParseWorkspace(c.src)
		if err == nil {
			t.Errorf("%s: 거절해야 하는데 통과했다", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.wantSub) {
			t.Errorf("%s: 사유에 %q 가 없다 — 실제: %v", c.name, c.wantSub, err)
		}
	}
}

// 값 안의 `#` 은 주석이 아니다. 잘라내면 경로가 조용히 바뀐다.
//
// ★ **인용 없는 꼴이 이 시험의 본체다.** 처음엔 `"a#b"` 만 재고 통과라고 적었는데,
// 변이(주석 판정에서 공백 조건을 뗀 것)를 넣어도 초록이었다 — 큰따옴표가 먼저 막아서
// 정작 재려던 줄에 닿지 않았다. YAML 규격에서 `#` 이 주석이 되는 것은 **공백 뒤**일
// 때뿐이고, 인용 없는 `a#b` 가 그 규칙이 걸리는 유일한 자리다.
func TestParseWorkspaceHashInsideValueIsNotAComment(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"인용 없음", "workspace:\n  members:\n    - path: a#b\n", "a#b"},
		{"큰따옴표", "workspace:\n  members:\n    - path: \"a#b\"\n", "a#b"},
		{"공백 뒤 #는 주석", "workspace:\n  members:\n    - path: ab # 여긴 주석\n", "ab"},
	}
	for _, c := range cases {
		ws, err := ParseWorkspace(c.src)
		if err != nil {
			t.Errorf("%s: 오류: %v", c.name, err)
			continue
		}
		if len(ws.Members) != 1 || ws.Members[0].Path != c.want {
			t.Errorf("%s: path=%+v — %q 여야 한다", c.name, ws.Members, c.want)
		}
	}
}

// 문서 구분자 뒤는 **둘째 문서**라 안 읽는다. 진짜 YAML 도구가 첫 문서만 보는 것과 맞춘다.
//
// ★ 첫 문서에 블록이 **없는** 꼴로 잰다. 양쪽 문서에 다 두면 «첫 키에서 멈춘다»는
// 다른 규칙이 먼저 걸려서, 구분자를 아예 무시하도록 변이를 넣어도 초록이 나온다
// (실측 — 그래서 이 시험이 이 꼴로 다시 쓰였다).
func TestParseWorkspaceStopsAtDocumentSeparator(t *testing.T) {
	src := "schema: 1\n---\nworkspace:\n  members:\n    - path: second\n"
	ws, err := ParseWorkspace(src)
	if err != nil {
		t.Fatalf("오류: %v", err)
	}
	if ws.Declared || len(ws.Members) != 0 {
		t.Fatalf("Declared=%v 멤버=%+v — 둘째 문서는 안 읽어야 한다", ws.Declared, ws.Members)
	}
}

// 모르는 키는 건너뛴다 — 명부가 나중에 칸을 얻어도 옛 서버가 그 파일을 읽어야 한다.
func TestParseWorkspaceSkipsUnknownMemberKeys(t *testing.T) {
	ws, err := ParseWorkspace("workspace:\n  members:\n    - path: a\n      branch: main\n      note: 나중에 생긴 칸\n")
	if err != nil {
		t.Fatalf("오류: %v", err)
	}
	if len(ws.Members) != 1 || ws.Members[0].Path != "a" {
		t.Fatalf("멤버=%+v", ws.Members)
	}
}

// 프로젝트 id 규칙은 **클라이언트와 같은 문자열**을 내야 한다 — 다르면 같은 레포가
// 프로젝트 둘로 갈린다. cmd/fd 의 동명 함수가 이것을 부른다(그 위임이 이 대칭의 자리다).
func TestProjectIDFromPathStripsBoundaryCharacters(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/a/b/context-platform-search-api", "context-platform-search-api"},
		{"nested/inner", "inner"},
		{"a b", "a-b"},
		{"-lead-", "lead"},
		{"", ""},
		{"/", ""},
		{"...", ""},
		// 슬래시 구분으로 읽는다 — 서버가 윈도우에서 돌아도 커밋된 값의 뜻은 안 바뀐다.
		{"a\\b", "b"},
	}
	for _, c := range cases {
		if got := ProjectIDFromPath(c.in); got != c.want {
			t.Errorf("ProjectIDFromPath(%q) = %q, 기대 %q", c.in, got, c.want)
		}
	}
}
