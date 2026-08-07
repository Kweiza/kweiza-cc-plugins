package mcpsrv

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
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
	mine := LiveIdentity{SessionID: "s-me", MachineID: "m1", Worktree: "/w/repo", CCSessionID: "cc-new"}

	cases := []struct {
		name string
		live []LiveIdentity
		want []string // 기대하는 쌍둥이의 session id
	}{
		{
			// ★★ **내 카드 id 면 cc 가 무엇이든 나 자신이다.**
			// cc 축만으로 자기를 빼면 이 기능의 정상 상태에서 자기 카드가 잡힌다 —
			// ensureSession 은 비콘의 cc 로 카드를 여는데, 그 값과 이 프로세스가
			// 든 env cc 는 /clear 뒤 **정의상** 다르다. 그리고 열고 나서 board 를
			// 부르기 전에 /clear 가 또 오면 cc 축은 다시 갈린다 — id 축만이 안정적이다.
			name: "내 카드 id 면 cc 가 달라도 나 자신이다",
			live: []LiveIdentity{li("s-me", "m1", "/w/repo", "cc-changed-again")},
			want: nil,
		},
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
//
// ★ 훅이 비콘으로 표류를 고치게 된 뒤로 "재기동해라"는 틀린 조언이 됐다 — 지금은
// 그 수리가 이번엔 왜 안 됐는지를 말해야 한다. 그래서 여기서 단정하는 것은 옛 문구가
// 아니라 새 사실: why 인자가 화면에 그대로 실리는지, 그리고 재기동 권유가 사라졌는지다.
func TestRenderDriftNamesTheAxisAndWhyRepairDidNotHappen(t *testing.T) {
	twins := []CoordinateTwin{{SessionID: "s-old", CCSessionID: "cc-old"}}
	got := RenderDrift(twins, "s-mine", "cc-new", "조상 사슬 어디에도 이 머신의 비콘이 없다")

	for _, want := range []string{
		"cc-old", // 상대가 든 값
		"cc-new", // 내가 든 값
		"s-old",  // 어느 카드인지
		"조상 사슬",  // 수리가 이번엔 왜 안 됐는지(why 가 화면에 실렸는지)
	} {
		if !strings.Contains(got, want) {
			t.Errorf("문구에 %q 가 없다 — 무엇이 갈렸는지/왜 못 고쳤는지 알 수 없다:\n%s", want, got)
		}
	}

	// ★ 옛 조언("재기동해라")은 이제 틀렸다 — 훅이 다음 SessionStart 에 비콘으로 고친다.
	if strings.Contains(got, "재기동") {
		t.Errorf("고쳐진 뒤에도 재기동을 권한다:\n%s", got)
	}

	// ★★ **합쳐진다고 단정하지 않는다.** 자기 카드를 뺀 뒤에 남는 쌍둥이는 둘 중 하나다:
	// (a) 같은 워크트리에 열린 **다른 창** — 이것은 영영 안 합쳐지고, 안 합쳐지는 것이 옳다
	//     (이 머신에 그런 창이 다섯이다, 설계 개정 ③), 또는
	// (b) 진짜로 수리가 멈춘 표류.
	// 단정형 문구는 (a)에게 오지 않을 수리를 약속한다 — 그 약속을 믿고 기다리면
	// "왜 아직도 두 장이냐"에 아무도 답을 못 한다.
	if strings.Contains(got, "훅이 다음 SessionStart 에 이것을 합친다") {
		t.Errorf("합쳐진다고 단정한다 — 다른 창이면 영영 안 합쳐진다:\n%s", got)
	}
	if !strings.Contains(got, "다른 창") {
		t.Errorf("남은 쌍둥이가 '다른 창'일 수 있다는 갈래를 말하지 않는다:\n%s", got)
	}

	// 표류가 없으면 **아무 말도 안 한다.** 매 board 마다 빈 절이 붙으면 예산이 토큰인 화면이 상한다.
	if s := RenderDrift(nil, "s-mine", "cc-new", ""); s != "" {
		t.Errorf("표류가 없는데 문구를 냈다: %q", s)
	}
}

// TestRenderDriftCapsNamedTwins 는 배너가 이름을 적는 줄에 상한이 있는지 본다.
//
// 이 배너는 board 의 **고정분**이다 — 예산이 자르는 것은 카드뿐이라, 여기 한 줄이 늘면
// 카드가 한 장 밀려난다. 그런데 갈린 카드 수는 /clear·compact 때마다 자라고 상한이 없었다.
// 실측(2026-08-05): 쌍둥이 10건일 때 이 배너 혼자 예산 1200 의 44%.
func TestRenderDriftCapsNamedTwins(t *testing.T) {
	const n = 10
	var tw []CoordinateTwin
	for i := 0; i < n; i++ {
		tw = append(tw, CoordinateTwin{
			SessionID:   fmt.Sprintf("01KZ7CARD%013d", i),
			CCSessionID: fmt.Sprintf("%08d-6ca4-4321-9912-f713e791f3fe", i),
		})
	}
	got := RenderDrift(tw, "s-mine", "ce5c2e79-767f-4e85-8893-52a0219f6d9a", "")

	if named := strings.Count(got, "갈린 카드: "); named > driftTwinLimit {
		t.Fatalf("이름을 %d개 적었다 — 상한 %d\n%s", named, driftTwinLimit, got)
	}
	// 수는 첫 줄이 **참값**으로 낸다. 상한을 수에도 적용하면 배너가 거짓말을 한다.
	if !strings.Contains(got, fmt.Sprintf("갈린 세션이 %d건 더", n)) {
		t.Fatalf("첫 줄이 참 건수 %d 를 안 낸다:\n%s", n, got)
	}
	if !strings.Contains(got, fmt.Sprintf("%d건 더 —", n-driftTwinLimit)) {
		t.Fatalf("잘랐는데 몇 건을 잘랐는지 안 말한다:\n%s", got)
	}

	// 대조 — 상한 이하면 전부 이름이 나온다.
	few := RenderDrift(tw[:2], "s-mine", "ce5c2e79-767f-4e85-8893-52a0219f6d9a", "")
	if named := strings.Count(few, "갈린 카드: "); named != 2 {
		t.Fatalf("쌍둥이 2건인데 이름이 %d개다:\n%s", named, few)
	}
}

// why 가 비어도 문구가 잘 맺어지는지("사유:" 만 남고 뒤가 없는 꼴이 아닌지) 본다.
func TestRenderDriftIsWellFormedWithoutAReason(t *testing.T) {
	got := RenderDrift([]CoordinateTwin{{SessionID: "s-old", CCSessionID: "cc-old"}}, "s-mine", "cc-new", "")
	if got == "" {
		t.Fatalf("표류가 있는데 문구가 비었다")
	}
	if strings.Contains(got, "사유:") {
		t.Errorf("사유가 없는데 '사유:' 꼬리표만 남았다:\n%s", got)
	}
}

// TestBoardTailDoesNotAccuseSiblingCardOfOverlap 은 **세 번째 호출부**를 잠근다.
//
// 형제 판정은 judge 가 하고(eligible_test), board.go·pick.go 배선은 service 가 본다
// (pick_test). 여기 mcpsrv 의 liveOf 는 그 둘 어느 쪽도 안 지나는 별개 호출부다 —
// 그 한 줄이 빠지면 판정도 멀쩡하고 다른 시험도 전부 초록인데 **이 화면만** 거짓말한다.
// 같은 모양을 이 패키지가 머신 축에서 이미 겪었다.
func TestBoardTailDoesNotAccuseSiblingCardOfOverlap(t *testing.T) {
	repo := newRepo(t)
	svc, _ := newSvc(t)
	srv := newServer(t, svc, repo, fullEnv(repo))
	project := filepath.Base(repo)
	bg := context.Background()

	// 내 카드는 첫 도구 호출에서 생긴다(ensureSession 이 게으르다).
	serve(t, srv, call("board", map[string]any{}))
	view, err := svc.Board(bg, project, service.BoardOptions{})
	if err != nil {
		t.Fatalf("보드 조회 실패: %v", err)
	}
	var mineID string
	for _, c := range view.Sessions {
		if c.View.Session.CCSessionID == "cc-session-uuid-1" {
			mineID = c.View.Session.ID
		}
	}
	if mineID == "" {
		t.Fatal("전제 구성 실패 — 내 카드를 못 찾았다")
	}

	// 형제: **같은 대화**인데 3중키의 다른 성분이 갈려 카드가 따로 선 것.
	//
	// ★ 갈림 축으로 **머신**을 쓴다. 워크트리 축으로 가르면 카드의 경로가 그 워크트리
	// 기준으로 상대화되지 않아(하위 디렉토리는 git 이 못 읽는다) 애초에 겹치지 않고,
	// 그러면 "형제가 안 나왔다"가 판정 때문인지 경로가 애초에 안 겹쳐서인지 구분이 안 된다 —
	// 실제로 그렇게 썼다가 변이 시험에서 **판정을 죽여도 초록**인 것을 확인했다.
	// 머신 축은 이 저장소가 실제로 겪는 갈림이고(hostname 대 진입점의 안정 id,
	// 이 응답의 경고가 그것을 말한다) 경로 상대화가 나와 같아 대조가 성립한다.
	sib, err := svc.OpenSession(bg, service.OpenSessionInput{
		Project: project, ProjectPath: repo,
		MachineID: "othermachine", Hostname: "othermachine", Worktree: repo,
		CCSessionID: "cc-session-uuid-1", Label: "형제카드",
	})
	if err != nil {
		t.Fatalf("전제 구성 실패 — 형제 카드: %v", err)
	}
	// 진짜 남: 대화가 다르다.
	other, err := svc.OpenSession(bg, service.OpenSessionInput{
		Project: project, ProjectPath: repo,
		MachineID: "testhost", Hostname: "testhost", Worktree: repo,
		CCSessionID: "cc-other", Label: "진짜남",
	})
	if err != nil {
		t.Fatalf("전제 구성 실패 — 남의 카드: %v", err)
	}

	// 셋이 **같은 경로**를 만진다. 판정이 안 돌면 형제도 남도 둘 다 겹침으로 나온다.
	shared := filepath.Join(repo, "pipeline", "run.py")
	for _, id := range []string{mineID, sib.Session.ID, other.Session.ID} {
		if err := svc.Beat(bg, id, model.SignalTool, []string{shared}); err != nil {
			t.Fatalf("전제 구성 실패 — 비트(%s): %v", id, err)
		}
	}

	body, isErr := toolText(t, serve(t, srv, call("board", map[string]any{}))[0])
	if isErr {
		t.Fatalf("board 가 실패했다:\n%s", body)
	}

	// ★ 카드 id 로 단정하지 않는다. 세 카드가 같은 밀리초에 열려 ULID 앞 8자가 같고,
	// 꼬리는 ShortID(8자)로 찍으므로 **셋이 화면에서 구분되지 않는다** — 실제로 그렇게
	// 썼다가 이 시험이 자기 자신을 못 가르는 것을 확인했다. 꼬리표로 가른다.
	tail := body[strings.Index(body, "── 꼬리 ──"):]
	if strings.Contains(tail, "형제카드") {
		t.Fatalf("형제 카드가 겹침으로 나왔다 — 자기 자신과 조율하라는 화면이다:\n%s", tail)
	}
	// ★ 대조 — 진짜 남은 반드시 남는다. 없으면 이 시험은 겹침 축을 통째로 꺼 놓고 초록이다.
	if !strings.Contains(tail, "진짜남") {
		t.Fatalf("진짜 남과의 겹침이 사라졌다 — 형제를 빼면서 축을 껐다:\n%s", tail)
	}
	if !strings.Contains(tail, "겹침 1건") {
		t.Fatalf("겹침이 1건이 아니다 — 형제가 섞였거나 축이 꺼졌다:\n%s", tail)
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
	// ★ "재기동" 을 더 이상 요구하지 않는다 — 옛 조언이 틀렸으므로 문구에서 지웠다.
	// 렌더 문구가 바뀌어도 안 깨지도록, **갈린 cc 두 값이 본문에 실제로 뜨는지**로
	// 단정한다(정체 값). 프레이즈를 고정하면 문구가 바뀔 때마다 시험이 따라 깨진다.
	for _, want := range []string{"cc-session-uuid-1", "cc-from-before-clear"} {
		if !strings.Contains(body, want) {
			t.Errorf("응답에 갈린 cc 값 %q 가 없다 — 카드가 왜 여러 장인지 세션이 알 수 없다:\n%s", want, body)
		}
	}
	if strings.Contains(body, "재기동") {
		t.Errorf("고쳐진 뒤에도 재기동을 권한다:\n%s", body)
	}
}

// TestRenderDriftNamesTheStableAxis 는 배너가 **rekey 를 건너 보존되는 축**을 내는지 본다.
//
// ★ 왜 cc 만으로는 안 되는가. 배너가 인쇄하는 mineCC 는 `s.openedCC` 인데, 그 값은
// ensureSession 안 한 자리에서만 쓰이고 그 함수는 두 번째 호출부터 조기 반환한다.
// 지우는 코드도 없다 — 즉 "카드를 연 값"이 아니라 **"카드를 열었던 값"** 이다.
// 그 사이 훅이 SessionStart 에서 카드를 그 cc 에서 떼어 간다(Rekey 는 cc 컬럼만 UPDATE
// 하고 카드 id 는 보존한다). 그래서 인쇄된 cc 는 **어떤 카드도 갖지 않은 값**이 된다.
//
// ★ 실물 피해(2026-08-06 판단): 그 값을 유일한 소비처 `fd close -cc-session` 에 넣었더니
// 그 호출이 3중키 upsert 라 **새 카드를 만들어서 그것을 닫았다.** 진단하려는 사람이
// 카드를 하나씩 더 만들면서 진단했다.
//
// ★ 이 파일의 DriftedTwins 주석이 이미 답을 적어 뒀다 — "id 는 rekey 를 건너 보존되므로
// (설계 제약 ⑥) 그 축만이 안정적이다". 판정에는 그 사실을 쓰면서 화면에는 안 냈다.
func TestRenderDriftNamesTheStableAxis(t *testing.T) {
	twins := []CoordinateTwin{{SessionID: "s-old", CCSessionID: "cc-old"}}
	got := RenderDrift(twins, "01KZMINECARD", "cc-new", "")
	if !strings.Contains(got, "01KZMINECARD") {
		t.Fatalf("배너가 내 **카드 id** 를 안 냈다:\n%s\n"+
			"cc 는 rekey 를 못 견딘다 — 그 값으로 할 수 있는 일이 없고, "+
			"유일한 소비처에 넣으면 카드가 하나 더 생긴다", got)
	}
}
