package mcpsrv

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/service"
)

// cc_session_id 표류 — **실측으로 확정된 사건**이다(2026-08-04).
//
// 측정 방법과 결과(이 레포에서 재현 가능):
//
//	pgrep -af 'fd mcp'                         → MCP 프로세스 pid
//	tr '\0' '\n' < /proc/<pid>/environ         → 그 프로세스가 든 CLAUDE_CODE_SESSION_ID
//	그 pid 의 조상 사슬을 /proc/<pid>/status 의 PPid 로 따라가면 claude 프로세스가 나온다
//
// 실측값:
//
//	claude   (3980399)  CLAUDE_CODE_SESSION_ID 없음 — **claude 가 내부에서 만든다**
//	fd mcp   (3980449)  61a918f7-…  ← claude 가 기동 시 **주입**했다(11:31:45)
//	훅·Bash             e5edfbf0-…  ← 지금 이 대화의 값
//
// 즉 같은 대화의 MCP 와 훅이 **서로 다른 cc 를 들고 쓰고 있었다.** 그 결과가 보드의 카드 두 장이다.
//
// ★ **이 측정이 항목의 처방 하나를 지웠다.** 앞선 판은 "MCP 가 도구 호출마다 cc 를 다시 읽는다"를
// 후보로 적었는데, 리눅스에서 프로세스의 environ 은 **기동 뒤 바뀌지 않는다.** 몇 번을 다시
// 읽어도 영원히 61a918f7 이다 — 그것은 고치기 어려운 처방이 아니라 **아무것도 안 하는 처방**이다.
// 구현했다면 초록인 채로 아무 일도 안 일어났을 것이고, 그 침묵이 이 항목을 두 번 죽였을 것이다.
//
// 그래서 지금 하는 것은 **따라가기가 아니라 알아채기**다. 이 계층의 규율이 그렇다
// (mcpsrv.go 머리: "못 읽으면 조용히 익명으로 진행하지 않는다").
// 따라갈 수 있는 값이 아예 없는데 따라가는 시늉을 하면 그것이 다음 거짓 초록이다.

func li(session, machine, worktree, cc string) LiveIdentity {
	return LiveIdentity{SessionID: session, MachineID: machine, Worktree: worktree, CCSessionID: cc}
}

func TestDriftedTwinsFindsTheCCAxisOnly(t *testing.T) {
	mine := Identity{MachineID: "m1", Worktree: "/w/repo", CCSessionID: "cc-new"}

	cases := []struct {
		name string
		live []LiveIdentity
		want []string // 기대하는 쌍둥이의 session id
	}{
		{
			name: "같은 좌표에 cc 만 다른 세션 — 이것이 표류다",
			live: []LiveIdentity{li("s-old", "m1", "/w/repo", "cc-old")},
			want: []string{"s-old"},
		},
		{
			name: "cc 까지 같으면 나 자신이다 — 표류가 아니다",
			live: []LiveIdentity{li("s-me", "m1", "/w/repo", "cc-new")},
			want: nil,
		},
		{
			// ★ 워크트리가 다르면 카드가 둘인 것이 **옳다.** 그것을 표류로 부르면
			//   워크트리로 일하는 정상 흐름이 매번 경고를 낸다.
			name: "워크트리가 다르면 표류가 아니다",
			live: []LiveIdentity{li("s-wt", "m1", "/w/repo/.wt/a", "cc-old")},
			want: nil,
		},
		{
			name: "머신이 다르면 표류가 아니다",
			live: []LiveIdentity{li("s-other", "m2", "/w/repo", "cc-old")},
			want: nil,
		},
		{
			name: "여럿이면 전부 낸다",
			live: []LiveIdentity{
				li("s-a", "m1", "/w/repo", "cc-old1"),
				li("s-me", "m1", "/w/repo", "cc-new"),
				li("s-b", "m1", "/w/repo", "cc-old2"),
				li("s-wt", "m1", "/w/other", "cc-old3"),
			},
			want: []string{"s-a", "s-b"},
		},
		{
			// ★ 내 cc 를 모르면 판정 자체가 성립하지 않는다. 빈 값을 "다르다"로 세면
			//   정체가 반쪽인 세션이 살아 있는 세션 전부를 표류로 고발한다.
			name: "내 cc 가 비면 아무것도 안 낸다",
			live: []LiveIdentity{li("s-a", "m1", "/w/repo", "cc-old")},
			want: nil,
		},
		{
			name: "상대 cc 가 비면 그 상대는 세지 않는다",
			live: []LiveIdentity{li("s-a", "m1", "/w/repo", "")},
			want: nil,
		},
	}

	for _, c := range cases {
		m := mine
		if c.name == "내 cc 가 비면 아무것도 안 낸다" {
			m.CCSessionID = ""
		}
		got := DriftedTwins(m, c.live)
		var ids []string
		for _, d := range got {
			ids = append(ids, d.SessionID)
		}
		if strings.Join(ids, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: 쌍둥이가 %v 여야 하는데 %v 다", c.name, c.want, ids)
		}
	}
}

// 문구는 소비자 좌표계다 — 세션이 읽는 것은 이것뿐이다.
func TestRenderDriftNamesTheAxisAndWhatToDo(t *testing.T) {
	got := RenderDrift([]CoordinateTwin{{SessionID: "s-old", CCSessionID: "cc-old"}}, "cc-new")

	for _, want := range []string{
		"cc-old", // 상대가 든 값
		"cc-new", // 내가 든 값
		"s-old",  // 어느 카드인지
		"기동",     // 왜 이렇게 됐는지(기동 시 주입)
		"재기동",    // 무엇을 하면 되는지
	} {
		if !strings.Contains(got, want) {
			t.Errorf("문구에 %q 가 없다 — 무엇이 갈렸는지/무엇을 하면 되는지 알 수 없다:\n%s", want, got)
		}
	}

	// 표류가 없으면 **아무 말도 안 한다.** 매 board 마다 빈 절이 붙으면 예산이 토큰인 화면이 상한다.
	if s := RenderDrift(nil, "cc-new"); s != "" {
		t.Errorf("표류가 없는데 문구를 냈다: %q", s)
	}
}

// 표류가 **응답 문자열에 실제로 뜨는지**를 본다 — 세션이 읽는 것은 이것뿐이다.
func TestBoardShowsCCDriftInTheResponse(t *testing.T) {
	repo := newRepo(t)
	svc, _ := newSvc(t)
	srv := newServer(t, svc, repo, fullEnv(repo))
	project := filepath.Base(repo)

	// 같은 (machine, worktree) 인데 cc 만 다른 세션을 하나 만든다.
	// newServer 는 WithMachine 을 안 주므로 머신 id 는 hostname("testhost")이고,
	// 워크트리는 WithCwd 로 준 repo 다.
	if _, err := svc.OpenSession(context.Background(), service.OpenSessionInput{
		Project: project, ProjectPath: repo,
		MachineID: "testhost", Hostname: "testhost", Worktree: repo,
		CCSessionID: "cc-from-before-clear",
	}); err != nil {
		t.Fatalf("전제 구성 실패 — 갈린 세션을 못 만들었다: %v", err)
	}

	frames := serve(t, srv, call("board", map[string]any{}))
	if len(frames) != 1 {
		t.Fatalf("board 응답이 %d개다", len(frames))
	}
	body, isErr := toolText(t, frames[0])
	if isErr {
		t.Fatalf("board 가 실패했다:\n%s", body)
	}

	// ── 대조가 성립했는지 **독립된 경로로** 단정한다 ─────────────────────────
	// ★ 응답 본문에서 "cc-from-before-clear" 를 찾는 것으로 전제를 세우면 안 된다 —
	//   그 문자열을 찍는 것이 바로 지금 시험하는 알림이라, 판정기가 돌면 전제도 함께
	//   통과한다(순환). 실제로 그렇게 썼다가 고쳤다. 그래서 서비스를 직접 친다.
	//
	// ★ 순서가 board 호출 **뒤**인 이유: ensureSession 이 게을러서(도구를 한 번도 안 부르면
	//   세션 행도 안 생긴다) 호출 전에는 cc 가 한 종뿐이다. 앞에 두면 이 전제가 항상 깨진다 —
	//   실제로 그렇게 썼다가 이 검사에 걸렸다.
	view, err := svc.Board(context.Background(), project, service.BoardOptions{})
	if err != nil {
		t.Fatalf("전제 확인 실패 — 보드를 못 읽었다: %v", err)
	}
	ccs := map[string]bool{}
	for _, c := range view.Sessions {
		if c.View.Session.Worktree == repo {
			ccs[c.View.Session.CCSessionID] = true
		}
	}
	if len(ccs) < 2 {
		t.Fatalf("전제가 깨졌다 — 워크트리 %s 에 cc 가 %d종뿐이다(%v). 갈림이 없으면 볼 것도 없다",
			repo, len(ccs), ccs)
	}
	for _, want := range []string{"cc_session_id 가 갈린", "재기동"} {
		if !strings.Contains(body, want) {
			t.Errorf("응답에 %q 가 없다 — 카드가 왜 여러 장인지 세션이 알 수 없다:\n%s", want, body)
		}
	}
}
