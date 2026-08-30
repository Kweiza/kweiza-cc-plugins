package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 훅 시험 — 좌표계는 **훅 stdout 과 종료코드**다.
//
// 이것이 깨지면 세션이 안 뜬다. 그래서 단정은 "함수가 오류를 안 냈다"가 아니라
// "종료코드가 0 이고 stdout 이 훅 계약을 만족한다"이다.

// sessionStartPayload 는 SessionStart 훅 stdin 한 벌이다.
// /clear 는 **같은 창에 새 session_id** 로 온다 — `source:"clear"` 가 흉내내는 것이 그것이다.
//
// ★ 이 헬퍼가 **무태그 파일에** 사는 이유. 원래 자리는 hook_beacon_test.go 였는데 그 파일은
// `//go:build linux` 다(비콘이 window.StartedOf 를 쓰고 그 함수가 리눅스 밖에서
// ErrUnsupported 를 낸다). 소비자가 비콘뿐일 때는 그것으로 족했지만, 이번에 무태그인
// bincache_test.go 가 이 헬퍼를 부르면서 **리눅스 시험은 전부 초록인데
// `GOOS=darwin GOARCH=arm64 go vet` 만 `undefined: sessionStartPayload` 로 터졌다.**
// 훅 stdin 한 벌은 플랫폼 축과 무관하므로 판정 자리를 여기로 옮긴다 —
// 리눅스 전용은 비콘 **단정**이지 훅을 **부르는 법**이 아니다.
//
// ★ 왜 사본을 안 만들었나. 이 파일 안에도 같은 모양의 JSON 리터럴이 몇 개 있어서
// "무태그 쪽은 인라인이 관례"라고 볼 여지가 있었지만, 그 리터럴들은 각자 자기 시험이
// 겨누는 필드만 담은 **다른 입력**이다. 반면 bincache 쪽이 필요한 것은 비콘 시험이 쓰는
// 것과 **같은 한 벌**이라, 거기에 사본을 두면 훅 계약이 바뀔 때 갈릴 두 화면이 생긴다.
func sessionStartPayload(cc, cwd string) string {
	raw, err := json.Marshal(map[string]string{
		"session_id":      cc,
		"cwd":             cwd,
		"hook_event_name": "SessionStart",
		"source":          "clear",
	})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// ★ 어떤 입력에도 종료코드 0. 이 표가 이 파일의 존재 이유다.
func TestHooksAreFailOpenForEveryInput(t *testing.T) {
	h := newHarness(t)
	h.down() // 서버까지 죽여 둔다 — 가장 나쁜 조건에서 단정한다

	valid := `{"session_id":"cc-1","cwd":"/tmp","hook_event_name":"SessionStart","source":"startup"}`
	inputs := []struct{ name, stdin string }{
		{"빈 stdin", ""},
		{"공백만", "   \n  "},
		{"깨진 JSON", "{이건 JSON 이 아니다"},
		{"JSON 이지만 배열", `["세션"]`},
		{"필드가 하나도 없는 객체", `{}`},
		{"session_id 가 숫자", `{"session_id":123}`},
		{"정상", valid},
		{"거대한 tool_input", `{"session_id":"cc-1","tool_input":{"file_path":"` + strings.Repeat("a", 5000) + `"}}`},
	}
	events := []string{"session-start", "user-prompt", "post-tool", "pre-compact", "stop",
		"없는-훅-이름"} // ★ 표 밖: 우리가 모르는 훅 이름도 세션을 막으면 안 된다

	for _, ev := range events {
		for _, in := range inputs {
			t.Run(ev+"/"+in.name, func(t *testing.T) {
				code, _ := h.run(in.stdin, "hook", ev)
				if code != 0 {
					t.Fatalf("훅 %s(%s) 가 종료코드 %d 를 냈다 — 세션이 안 뜬다", ev, in.name, code)
				}
			})
		}
	}
	// 훅 이름 자체가 없는 경우도.
	if code, _ := h.run("", "hook"); code != 0 {
		t.Fatalf("훅 이름 없이 불렀을 때 종료코드 %d — fail-open 이 깨졌다", code)
	}
}

// 서버 미도달일 때 SessionStart 는 **배너를 실제로 낸다.**
// 조용히 두면 에이전트가 조정 기구가 있는 줄 알고 움직인다(설계 §7).
func TestSessionStartEmitsBannerWhenServerIsDown(t *testing.T) {
	h := newHarness(t)
	h.down()

	code, out := h.run(`{"session_id":"cc-1","cwd":"/tmp","hook_event_name":"SessionStart"}`,
		"hook", "session-start")
	if code != 0 {
		t.Fatalf("종료코드 %d", code)
	}
	var payload struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
		t.Fatalf("훅 stdout 이 JSON 이 아니다: %v\n%s", err, out)
	}
	if payload.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Fatalf("hookEventName 이 %q 다", payload.HookSpecificOutput.HookEventName)
	}
	ctx := payload.HookSpecificOutput.AdditionalContext
	mustContain(t, "additionalContext", ctx,
		"⚠ 조정 서버 미도달",
		"되는 것: 코드 작성·커밋·조사 전부",
		"안 되는 것: 새 항목 선점",
		"내 선점:", // 선점 축을 침묵하지 않는다
	)
	// 배너는 **맨 앞**이어야 한다. 뒤에 있으면 긴 보드에 묻힌다.
	if !strings.HasPrefix(strings.TrimSpace(ctx), "⚠") {
		t.Fatalf("배너가 맨 앞이 아니다:\n%s", ctx)
	}
}

// 서버가 살아 있으면 세션을 실제로 등록하고, 그 사실이 additionalContext 에 나온다.
func TestSessionStartRegistersSessionWhenServerIsUp(t *testing.T) {
	h := newHarness(t)
	code, out := h.run(`{"session_id":"cc-live-1","cwd":"/tmp","hook_event_name":"SessionStart"}`,
		"hook", "session-start")
	if code != 0 {
		t.Fatalf("종료코드 %d: %s", code, out)
	}
	if !strings.Contains(out, "flightdeck 세션 신규") {
		t.Fatalf("세션 등록이 화면에 안 나온다:\n%s", out)
	}
	if strings.Contains(out, "조정 서버 미도달") {
		t.Fatalf("서버가 살아 있는데 미도달 배너를 냈다:\n%s", out)
	}
	// 좌표계: 서버가 실제로 세션을 갖게 됐나.
	view, err := h.svc.Board(t.Context(), h.project, service0BoardOptions())
	if err != nil {
		t.Fatalf("보드 조회 실패: %v", err)
	}
	if len(view.Sessions) != 1 {
		t.Fatalf("살아 있는 세션이 %d개다 — 훅이 등록하지 않았다", len(view.Sessions))
	}
}

// PostToolUse 는 **미커밋 발자국의 유일한 원천**이다. 경로가 실제로 저장돼야 한다.
func TestPostToolHookRecordsFootprintPath(t *testing.T) {
	h := newHarness(t)
	if code, out := h.run(`{"session_id":"cc-1","cwd":"/tmp","hook_event_name":"SessionStart"}`,
		"hook", "session-start"); code != 0 {
		t.Fatalf("세션 열기 실패(%d): %s", code, out)
	}
	payload := `{"session_id":"cc-1","cwd":"/tmp","tool_name":"Edit",` +
		`"tool_input":{"file_path":"/tmp/pipeline/x.py"}}`
	if code, _ := h.run(payload, "hook", "post-tool"); code != 0 {
		t.Fatalf("post-tool 훅이 실패했다")
	}
	view, err := h.svc.Board(t.Context(), h.project, service0BoardOptions())
	if err != nil {
		t.Fatalf("보드 조회 실패: %v", err)
	}
	if len(view.Sessions) == 0 {
		t.Fatal("세션이 없다")
	}
	found := false
	for _, p := range view.Sessions[0].View.Paths {
		if strings.Contains(p, "pipeline/x.py") {
			found = true
		}
	}
	if !found {
		t.Fatalf("편집 경로가 발자국에 없다: %v", view.Sessions[0].View.Paths)
	}
}

// PreCompact 는 초안 판단을 남긴다. 무엇을 못 하는지도 본문이 말해야 한다.
// TestPreCompactResolvesTheProjectFromThePayloadCwd 는 **여섯 훅의 비대칭**을 잠근다.
//
// ★ session-start · user-prompt · post-tool · stop · session-end 다섯은 전부
// `a.proj = resolveProject(a.env, p.CWD)` 로 페이로드 cwd 에서 좌표를 다시 푼다.
// pre-compact 만 그것이 없어서 **훅 프로세스의 cwd** 기준 카드로 간다. 그 둘이 갈리는
// 것이 이 제품의 정상 흐름이다 — 규율이 `git worktree add` 를 지시하므로 대화는 트리를 옮긴다.
//
// ★ 기존 시험(아래 TestPreCompactLeavesDraftJudgment)은 이 비대칭을 못 잡는다.
// `cwd:"/tmp"` 를 보내면서 초안 **개수만** 세므로, 초안이 엉뚱한 카드에 붙어도 초록이다.
// 압축 직전 초안은 그 대화가 컨텍스트를 잃기 직전에 남기는 마지막 기록이라 가장 나쁜
// 자리에서 어긋난다 — 복귀한 세션이 자기 카드에서 그것을 못 찾는다.
func TestPreCompactResolvesTheProjectFromThePayloadCwd(t *testing.T) {
	h := newHarness(t)
	elsewhere := t.TempDir()

	payload := `{"session_id":"cc-1","cwd":"` + elsewhere + `"}`
	if code, out := h.run(payload, "hook", "session-start"); code != 0 {
		t.Fatalf("세션 열기 실패(%d): %s", code, out)
	}
	live := h.liveSessions()
	if len(live) != 1 {
		t.Fatalf("전제가 깨졌다 — 카드가 %d건이다, want 1", len(live))
	}
	card := live[0].Session.ID

	if code, out := h.run(payload[:len(payload)-1]+`,"trigger":"auto"}`,
		"hook", "pre-compact"); code != 0 {
		t.Fatalf("pre-compact 훅 실패(%d): %s", code, out)
	}

	js := h.judgments(model.JudgmentDraft)
	if len(js) != 1 {
		t.Fatalf("초안이 %d건이다, want 1", len(js))
	}
	if js[0].SessionID != card {
		t.Fatalf("초안이 카드 %s 에 붙었다, want %s\n"+
			"pre-compact 만 페이로드 cwd 로 좌표를 다시 안 풀어서 훅 프로세스 cwd 기준 "+
			"카드로 갔다 — 복귀한 세션이 자기 카드에서 이 초안을 못 찾는다", js[0].SessionID, card)
	}
	if !strings.Contains(js[0].Body, elsewhere) {
		t.Errorf("초안 본문의 워크트리가 페이로드 cwd 가 아니다:\n%s", js[0].Body)
	}
}

func TestPreCompactLeavesDraftJudgment(t *testing.T) {
	h := newHarness(t)
	if code, _ := h.run(`{"session_id":"cc-1","cwd":"/tmp"}`, "hook", "session-start"); code != 0 {
		t.Fatal("세션 열기 실패")
	}
	if code, _ := h.run(`{"session_id":"cc-1","cwd":"/tmp","trigger":"auto"}`,
		"hook", "pre-compact"); code != 0 {
		t.Fatal("pre-compact 훅 실패")
	}
	js := h.judgments(model.JudgmentDraft)
	if len(js) != 1 {
		t.Fatalf("초안이 %d건이다", len(js))
	}
	mustContain(t, "초안 본문", js[0].Body,
		"압축 직전 자동 초안", "trigger=auto", "이 세션이 쥔 항목",
		"판단 본문은 이 초안에 없다")
}

// envAtProject 는 h.env 를 베끼고 FD_PROJECT 만 바꾼다(wire_test.go 의 envAt 과 같은
// 관용구 — 하네스는 프로젝트 하나로 도는데, 이 축을 갈아 끼우면 같은 3중키(machine·
// worktree·cc)로 다른 프로젝트 이름을 보내는 상황을 만들 수 있다).
func envAtProject(h *harness, project string) map[string]string {
	e := map[string]string{}
	for k, v := range h.env {
		e[k] = v
	}
	e["FD_PROJECT"] = project
	return e
}

// TestSessionStartAdoptsTheServerResolvedProjectAndNoticesIt 는 I-1(최종 리뷰)을 잰다.
//
// e81831b(고아 방지) 뒤로, 같은 (machine, worktree, cc) 3중키가 이미 프로젝트 "real" 로
// 열려 있는데 다른(미등록) 프로젝트 이름으로 다시 열면 서버는 세션을 "real" 로 **정상
// 재개**시키고 그 이름은 등록하지 않는다(internal/service/session_test.go 의
// TestOpenSessionDoesNotOrphanAProjectWhenResumingAnotherOnesTriple 이 서비스 층에서 같은
// 시나리오를 잰다 — 이 시험은 그것을 클라이언트 표면에서 재현한다: "클라이언트가 git 을
// 못 읽어 워크트리 디렉토리 이름을 프로젝트로 지어낸 상황"을 FD_PROJECT 로 강제한다).
//
// 옛 코드는 그 답(res.Project)을 한 번도 안 보고 a.proj.ID 를 요청한 이름 그대로 뒀다 —
// 그러면 이 프로세스의 후속 쓰기가 전부 미등록 이름으로 나가 FK 위반으로 죽었다
// (TestPreCompactWriteLandsInTheProjectTheServerActuallyOpened 가 그 사고를 원장에서 잰다).
//
// ★ 무엇을 재나: ⑴ 요청한 이름으로 프로젝트가 안 생긴다(e81831b 의 성과가 안 깨졌는지도
// 같이 본다) ⑵ SessionStart 머리줄이 **채택된 실제 프로젝트**를 말한다(a.proj.ID 가
// 실제로 갈렸다는 간접 증거) ⑶ 그 사실이 notice 로 화면에 남는다 — 지금까지는 서버 로그
// (session.project.mismatch 이벤트)로만 나가 에이전트가 못 봤다.
func TestSessionStartAdoptsTheServerResolvedProjectAndNoticesIt(t *testing.T) {
	h := newHarness(t)
	cwd := t.TempDir()
	cc := "cc-mismatch"

	// 정상 세션 하나 — 프로젝트 "real" 에 3중키를 만든다.
	if code, out := h.runEnv(envAtProject(h, "real"), sessionStartPayload(cc, cwd),
		"hook", "session-start"); code != 0 {
		t.Fatalf("첫 session-start 실패(%d): %s", code, out)
	}

	// 같은 3중키(같은 cc·cwd)로 **다른(미등록) 프로젝트 이름**을 보낸다.
	code, out := h.runEnv(envAtProject(h, "지어낸이름"), sessionStartPayload(cc, cwd),
		"hook", "session-start")
	if code != 0 {
		t.Fatalf("둘째 session-start 실패(%d): %s", code, out)
	}

	if _, err := h.st.GetProject(context.Background(), "지어낸이름"); err == nil {
		t.Fatal("미등록 이름으로 프로젝트가 생겼다 — 자동 등록이 3중키 조회보다 앞으로 되돌아갔다")
	}

	if !strings.Contains(out, "프로젝트 real") {
		t.Fatalf("SessionStart 머리줄이 채택된 실제 프로젝트(real)를 안 말한다:\n%s", out)
	}
	mustContain(t, "SessionStart 출력", out, "지어낸이름", "등록돼 있지 않다")
}

// TestPreCompactWriteLandsInTheProjectTheServerActuallyOpened 는 I-1 이 고친 실제 사고를
// **원장 좌표계**에서 잰다(harness_test.go 머리말의 규율: "서버가 실제로 갖게 된 것").
// hookPreCompact 는 noteReq{Project: a.proj.ID, …} 로 프로젝트 좌표를 실어 쓰는 유일한
// 훅이다 — a.proj.ID 가 채택되지 않으면 이 쓰기가 FK 위반으로 죽어 초안이 **한 건도**
// 안 남는다. 그것이 최종 리뷰가 실측으로 든 사고 그대로다.
func TestPreCompactWriteLandsInTheProjectTheServerActuallyOpened(t *testing.T) {
	h := newHarness(t)
	cwd := t.TempDir()
	cc := "cc-mismatch-precompact"

	if code, out := h.runEnv(envAtProject(h, "real"), sessionStartPayload(cc, cwd),
		"hook", "session-start"); code != 0 {
		t.Fatalf("session-start 실패(%d): %s", code, out)
	}

	payload, err := json.Marshal(map[string]string{
		"session_id": cc, "cwd": cwd, "trigger": "auto",
	})
	if err != nil {
		t.Fatal(err)
	}
	if code, out := h.runEnv(envAtProject(h, "지어낸이름"), string(payload),
		"hook", "pre-compact"); code != 0 {
		t.Fatalf("pre-compact 실패(%d): %s", code, out)
	}

	if _, err := h.st.GetProject(context.Background(), "지어낸이름"); err == nil {
		t.Fatal("미등록 이름으로 프로젝트가 생겼다")
	}

	js, err := h.st.ListJudgmentsByKind(context.Background(), "real", model.JudgmentDraft, 10)
	if err != nil {
		t.Fatalf("판단 조회 실패: %v", err)
	}
	if len(js) != 1 {
		t.Fatalf("압축 직전 초안이 %d건이다 — 1건이어야 한다. a.proj.ID 를 서버 응답으로 "+
			"채택하지 않으면 이 쓰기가 FK 위반으로 죽어 0건이 된다", len(js))
	}
}

func TestEditedPathsReadsEveryToolShape(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want []string
	}{
		{"Edit", map[string]any{"file_path": "/a/b.go"}, []string{"/a/b.go"}},
		{"Write", map[string]any{"file_path": "/a/c.go", "content": "x"}, []string{"/a/c.go"}},
		{"NotebookEdit", map[string]any{"notebook_path": "/a/n.ipynb"}, []string{"/a/n.ipynb"}},
		{"nil", nil, nil},
		{"경로 없는 도구", map[string]any{"command": "ls"}, nil},
		// ★ 표 밖: 값이 문자열이 아닌 경우(플랫폼이 형식을 바꾼 날). 죽지 않고 0건이어야 한다.
		{"file_path 가 숫자", map[string]any{"file_path": 42}, nil},
		{"file_path 가 빈 문자열", map[string]any{"file_path": "  "}, nil},
		// ★ 배열형 키. 2.1.240 에 이 키를 쓰는 도구는 0건이라 지금은 예방인데, 시험이
		//   없으면 그 가지는 다음 리팩터링에 조용히 사라진다 — 그리고 플랫폼이 배열형을
		//   내기 시작한 날 아무도 그것을 모른다.
		{"배열형 키", map[string]any{"file_paths": []any{"/a/x.go", "/a/y.go"}},
			[]string{"/a/x.go", "/a/y.go"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := EditedPaths(c.in, "/repo")
			if len(got) != len(c.want) {
				t.Fatalf("%v → %v, %v 를 기대했다", c.in, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("%v → %v, %v 를 기대했다", c.in, got, c.want)
				}
			}
		})
	}
}

// TestEditedPathsReadsCodexApplyPatch 는 codex 하네스의 발자국을 문다.
//
// ★ 이 시험이 없으면 codex 세션의 경로 겹침이 **0건인 채로 초록**이다. 위 시험표는
// Claude 의 도구 모양(file_path)만 보므로 codex 가 들어와도 안 깨진다 — 조용한 부재는
// 시험이 따로 물지 않으면 영영 안 뜬다.
//
// 입력 문자열은 2026-08-30 실측을 **그대로** 옮긴 것이다(codex-cli 0.151.0).
func TestEditedPathsReadsCodexApplyPatch(t *testing.T) {
	const cwd = "/repo/work"
	cases := []struct {
		name string
		cmd  string
		want []string
	}{
		{"Add File", "*** Begin Patch\n*** Add File: sub/extra.txt\n+x\n*** End Patch",
			[]string{"/repo/work/sub/extra.txt"}},
		{"Delete File", "*** Begin Patch\n*** Delete File: sub/extra.txt\n*** End Patch",
			[]string{"/repo/work/sub/extra.txt"}},
		// ★ 이름 바꾸기는 경로가 **둘**이다. 하나만 잡으면 겹침의 한쪽이 사라진다.
		{"Update + Move to", "*** Begin Patch\n*** Update File: sub/old.txt\n*** Move to: sub/new.txt\n@@\n-old content\n+new content\n*** End Patch",
			[]string{"/repo/work/sub/old.txt", "/repo/work/sub/new.txt"}},
		// 패치가 아닌 셸 명령은 아무것도 안 낸다 — `command` 키를 본다고 다 뒤지면
		// `rm -rf x` 같은 문자열에서 유령 경로가 나온다.
		{"패치가 아닌 셸 명령", "pwd && rg --files -g 'probe.txt'", nil},
		{"빈 문자열", "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := EditedPaths(map[string]any{"command": c.cmd}, cwd)
			if len(got) != len(c.want) {
				t.Fatalf("%q → %v, %v 를 기대했다", c.cmd, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("%q → %v, %v 를 기대했다", c.cmd, got, c.want)
				}
			}
		})
	}
}

// TestEditedPathsAbsolutizesAgainstCwd 는 **좌표계**를 문다.
//
// 서버의 service.RelPathWithin 은 상대경로를 그대로 통과시켜 저장소 상대로 취급한다.
// 그래서 cwd 가 저장소 하위일 때 절대화를 빼먹으면 오류 없이 틀린 발자국이 남는다.
// 위 시험은 값이 맞는지만 보므로 이 축을 따로 문다 — 절대화를 지워도 위 시험이
// 안 깨지는 판(cwd 가 뿌리인 경우)이 존재하기 때문이다.
func TestEditedPathsAbsolutizesAgainstCwd(t *testing.T) {
	got := EditedPaths(map[string]any{"command": "*** Begin Patch\n*** Add File: a/b.go\n*** End Patch"}, "/repo/sub")
	if len(got) != 1 || got[0] != "/repo/sub/a/b.go" {
		t.Fatalf("하위 디렉토리 cwd 에서 %v — [/repo/sub/a/b.go] 여야 한다. "+
			"상대로 두면 서버가 저장소 뿌리 기준으로 읽어 없는 경로의 발자국이 된다", got)
	}
	// 이미 절대인 것(Claude 의 file_path)은 안 건드린다.
	if got := EditedPaths(map[string]any{"file_path": "/a/b.go"}, "/repo/sub"); got[0] != "/a/b.go" {
		t.Fatalf("절대경로를 건드렸다: %v", got)
	}
	// cwd 를 모르면 지어내지 않는다 — 변경 전 동작 그대로 둔다.
	if got := EditedPaths(map[string]any{"command": "*** Begin Patch\n*** Add File: a/b.go\n*** End Patch"}, ""); got[0] != "a/b.go" {
		t.Fatalf("cwd 가 없는데 지어냈다: %v", got)
	}
}

func TestParseHookPayloadSeparatesEmptyFromBroken(t *testing.T) {
	if _, err := ParseHookPayload(nil); err == nil ||
		!strings.Contains(err.Error(), "비었다") {
		t.Fatalf("빈 입력의 사유가 '비었다'가 아니다: %v", err)
	}
	if _, err := ParseHookPayload([]byte("{깨짐")); err == nil ||
		!strings.Contains(err.Error(), "JSON 이 아니다") {
		t.Fatalf("깨진 JSON 의 사유가 다르다: %v", err)
	}
	p, err := ParseHookPayload([]byte(`{"session_id":"x","cwd":"/y"}`))
	if err != nil || p.SessionID != "x" || p.CWD != "/y" {
		t.Fatalf("정상 입력을 못 읽었다: %+v %v", p, err)
	}
	// 모르는 필드는 무시한다 — 플랫폼이 필드를 늘리는 날 훅이 죽으면 세션이 안 뜬다.
	if _, err := ParseHookPayload([]byte(`{"session_id":"x","새필드":1}`)); err != nil {
		t.Fatalf("모르는 필드에 죽었다: %v", err)
	}
}
