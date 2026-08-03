package service

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 순수 함수 시험. 이 함수들이 판정을 담고 있고, 본문에 흩어져 있었다면
// 아래 단정들은 전부 "로직의 사본"을 단정하는 것이 됐을 것이다.

func TestRelPathUsesComponentsNotStringPrefix(t *testing.T) {
	sep := string(filepath.Separator)
	cases := []struct {
		name, root, in, want string
	}{
		{"저장소 안의 파일", "/a/b", "/a/b/tools/x.sh", filepath.Join("tools", "x.sh")},
		{"저장소 루트 자신", "/a/b", "/a/b", "."},
		{"이미 상대경로면 그대로", "/a/b", "tools/x.sh", filepath.Join("tools", "x.sh")},
		{"빈 값은 빈 값", "/a/b", "", ""},
		{"공백만이면 빈 값", "/a/b", "   ", ""},
		{"루트를 모르면 원본", "", "/a/b/x", filepath.Join(sep, "a", "b", "x")},

		// ★ 표 밖 케이스 — 문자열 접두로 자르면 여기서 "c/d" 가 나온다.
		//   다른 저장소의 파일이 이 저장소의 경로인 척하게 되고, 그러면 겹침이 거짓 양성이 된다.
		{"접두는 같지만 형제 디렉토리", "/a/b", "/a/bc/d", filepath.Join(sep, "a", "bc", "d")},
		{"저장소 밖 상위", "/a/b", "/a/x", filepath.Join(sep, "a", "x")},
		{"루트 끝에 슬래시", "/a/b/", "/a/b/x", "x"},
		{"중복 슬래시·점", "/a/b", "/a/b/./tools//x.sh", filepath.Join("tools", "x.sh")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RelPath(c.root, c.in); got != c.want {
				t.Fatalf("RelPath(%q, %q) = %q, 기대 %q", c.root, c.in, got, c.want)
			}
		})
	}
}

func TestUnionPathsDedupesSortsAndDropsEmpty(t *testing.T) {
	got := UnionPaths([]string{"b", "a", ""}, []string{"a", "  ", "c"}, nil)
	want := []string{"a", "b", "c"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("UnionPaths = %v, 기대 %v", got, want)
	}
	if UnionPaths() != nil {
		t.Fatalf("빈 입력은 nil 이어야 한다(빈 슬라이스와 nil 을 가르지 않으면 '경로 없음'이 두 모양이 된다)")
	}
}

func TestFreshnessOfSeparatesUnreadFromPartial(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name            string
		reads, failures int
		wantSource      string
		wantStale       bool
	}{
		{"전부 읽었다", 3, 0, "git", false},
		{"일부 실패", 3, 1, "git", true},
		{"한 번도 못 읽었다", 0, 2, "db", true},
		// 표 밖 케이스: 읽지도 실패하지도 않았다(파생을 아예 시도 안 함).
		// 값이 있는 척하면 안 되므로 db·낡음이어야 한다.
		{"시도 자체가 없었다", 0, 0, "db", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := FreshnessOf(now, c.reads, c.failures)
			if f.Source != c.wantSource || f.Stale != c.wantStale {
				t.Fatalf("FreshnessOf(%d,%d) = {%s stale=%v}, 기대 {%s stale=%v}",
					c.reads, c.failures, f.Source, f.Stale, c.wantSource, c.wantStale)
			}
			if !f.ObservedAt.Equal(now) {
				t.Fatalf("ObservedAt = %v, 기대 %v", f.ObservedAt, now)
			}
		})
	}
}

func TestPickDefaultBranch(t *testing.T) {
	refs := func(names ...string) []model.RefState {
		var out []model.RefState
		for _, n := range names {
			out = append(out, model.RefState{Ref: n})
		}
		return out
	}
	cases := []struct {
		name, declared string
		refs           []model.RefState
		head, want     string
	}{
		{"선언이 이긴다", "trunk", refs("main", "master"), "feat", "trunk"},
		{"main 이 있으면 main", "", refs("feat", "main"), "feat", "main"},
		{"main 이 없으면 master", "", refs("master", "feat"), "feat", "master"},
		{"둘 다 없으면 HEAD 브랜치", "", refs("feat"), "feat", "feat"},
		{"아무것도 없으면 main", "", nil, "", "main"},
		// 표 밖 케이스: HEAD 라는 이름의 ref 가 섞여 들어와도 브랜치로 세지 않는다.
		{"HEAD 는 브랜치가 아니다", "", refs("HEAD"), "", "main"},
		{"선언에 공백만", "   ", refs("master"), "", "master"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := PickDefaultBranch(c.declared, c.refs, c.head); got != c.want {
				t.Fatalf("PickDefaultBranch(%q, %v, %q) = %q, 기대 %q", c.declared, c.refs, c.head, got, c.want)
			}
		})
	}
}

func TestValidateItemIDBlocksShellAndRefMetacharacters(t *testing.T) {
	ok := []string{"t5-iam-hardening", "batch7", "docs/status.html", "a_b.c"}
	for _, id := range ok {
		if err := ValidateItemID(id); err != nil {
			t.Fatalf("%q 는 통과해야 한다: %v", id, err)
		}
	}
	// 소비자는 **셸과 git ref 둘**이다. 그 문법의 메타문자로 시험한다.
	bad := []string{
		"", "   ",
		"-rf",           // git·셸이 옵션으로 읽는다
		"a..b",          // ref 규칙 위반이자 경로 탈출
		"../etc/passwd", // 경로 탈출
		".hidden",       // ref 규칙 위반
		"trailing.",     //
		"a b",           // 셸 인자 분리
		"a;rm -rf /",    // 셸 명령 분리
		"a$(id)",        // 명령 치환
		"a`id`",         // 명령 치환
		"a|b", "a&b", "a>b", "a'b", "a\"b",
		"a\nb", // 제어문자
		strings.Repeat("x", 101),
	}
	for _, id := range bad {
		if err := ValidateItemID(id); err == nil {
			t.Fatalf("%q 는 거절돼야 한다 — 이 값은 브랜치 이름·디렉토리·셸 인자로 그대로 나간다", id)
		}
	}
}

func TestSetupCommandsRefusesUnsafeID(t *testing.T) {
	cmds := SetupCommands("/repo", "main", "batch7")
	if len(cmds) != 3 {
		t.Fatalf("명령 3줄이어야 한다: %v", cmds)
	}
	joined := strings.Join(cmds, "\n")
	for _, want := range []string{"cd '/repo'", "git worktree add '.flightdeck/worktrees/batch7' -b batch7 'main'"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("명령에 %q 가 없다:\n%s", want, joined)
		}
	}
	if got := SetupCommands("/repo", "main", "a;rm -rf /"); got != nil {
		t.Fatalf("안전하지 않은 id 에는 명령을 만들면 안 된다: %v", got)
	}
	if got := SetupCommands("", "main", "batch7"); got != nil {
		t.Fatalf("프로젝트 경로를 모르면 명령을 만들면 안 된다: %v", got)
	}
	// 표 밖 케이스: 기본 브랜치가 비면 main 으로 메운다(빈 인자를 git 에 넘기지 않는다).
	if got := SetupCommands("/repo", "", "batch7"); !strings.HasSuffix(got[1], " 'main'") {
		t.Fatalf("기본 브랜치가 비면 main 이어야 한다: %v", got)
	}
}

func TestJudgeFinishReturnsReasonAndGuidance(t *testing.T) {
	cases := []struct {
		name        string
		outcome     model.ItemState
		item        string
		body        string
		closeReason string
		wantOK      bool
		wantIn      string
	}{
		{"정상 done", model.ItemDone, "x", "본문", "", true, ""},
		{"정상 dropped", model.ItemDropped, "x", "본문", "중복이라 버린다", true, ""},
		{"항목 없음", model.ItemDone, "", "본문", "", false, "item_id"},
		{"본문 없음", model.ItemDone, "x", "", "", false, "일부러 안 한 것"},
		{"본문이 공백만", model.ItemDone, "x", "  \n\t ", "", false, "일부러 안 한 것"},
		{"폐기인데 사유 없음", model.ItemDropped, "x", "본문", "", false, "close_reason"},
		// 표 밖 케이스: 열거에 없는 종료 상태(open 으로 끝내려는 시도)
		{"open 으로 끝내려 함", model.ItemOpen, "x", "본문", "", false, "done 또는 dropped"},
		{"빈 상태", model.ItemState(""), "x", "본문", "", false, "done 또는 dropped"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := JudgeFinish(c.outcome, c.item, c.body, c.closeReason)
			if v.OK != c.wantOK {
				t.Fatalf("OK = %v, 기대 %v (사유: %s)", v.OK, c.wantOK, v.Reason)
			}
			if v.Reason == "" {
				t.Fatalf("사유는 성공일 때도 항상 채워야 한다 — 없으면 통과가 검증 불가가 된다")
			}
			if c.wantIn != "" && !strings.Contains(v.Reason+v.Guidance, c.wantIn) {
				t.Fatalf("사유·처방에 %q 가 없다:\n%s\n%s", c.wantIn, v.Reason, v.Guidance)
			}
		})
	}
}

func TestRecipientsExcludesSelf(t *testing.T) {
	card := func(id string) SessionCard {
		var c SessionCard
		c.View.Session.ID = id
		return c
	}
	got := Recipients([]SessionCard{card("A"), card("B"), card("C")}, "B")
	if strings.Join(got, ",") != "A,C" {
		t.Fatalf("Recipients = %v, 기대 [A C]", got)
	}
	// 표 밖 케이스: 혼자뿐이면 0명이어야 한다. 자기를 세면 "아무도 안 보고 있다"가 화면에서 사라진다.
	if n := len(Recipients([]SessionCard{card("A")}, "A")); n != 0 {
		t.Fatalf("혼자면 0명이어야 한다(받은 값 %d)", n)
	}
}

func TestProbePlatformNamesWhatIsMissing(t *testing.T) {
	env := map[string]string{
		"CLAUDE_CODE_SESSION_ID": "11111111-2222-3333-4444-555555555555",
		"CLAUDE_PROJECT_DIR":     "/proj",
		"CLAUDE_ENV_FILE":        "", // 빈 값은 관측 안 됨으로 센다
	}
	get := func(k string) (string, bool) { v, ok := env[k]; return v, ok }

	axes := ProbePlatform(get, "/proj/wt", nil)
	byName := map[string]DoctorAxis{}
	for _, a := range axes {
		byName[a.Name] = a
		if a.Detail == "" {
			t.Fatalf("축 %q 에 설명이 없다 — 부재를 이름만으로 내면 왜 필요한지가 사라진다", a.Name)
		}
	}
	if !byName["CLAUDE_CODE_SESSION_ID"].Observed {
		t.Fatalf("세션 id 축이 관측으로 안 잡혔다: %+v", byName["CLAUDE_CODE_SESSION_ID"])
	}
	if byName["CLAUDE_PLUGIN_ROOT"].Observed {
		t.Fatalf("없는 축이 관측으로 잡혔다")
	}
	// ★ 없는 축도 **이름과 함께** 나와야 한다. 목록에서 빠지면 그 탐지가 깨진 사실이 영영 안 보인다.
	if _, ok := byName["CLAUDE_ENV_FILE"]; !ok {
		t.Fatalf("CLAUDE_ENV_FILE 축이 목록에 없다 — 없는 것이 정상인 축도 이름으로 내야 한다")
	}
	if byName["CLAUDE_ENV_FILE"].Observed {
		t.Fatalf("빈 문자열은 관측으로 세면 안 된다")
	}
	if !byName["cwd"].Observed || byName["cwd"].Value != "/proj/wt" {
		t.Fatalf("cwd 축이 잘못됐다: %+v", byName["cwd"])
	}
}
