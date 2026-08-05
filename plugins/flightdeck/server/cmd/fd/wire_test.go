package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 배선 시험 — wire.go 의 필드 이름이 internal/api 의 요청 구조체와 **실제로** 맞는가.
//
// 구조체를 눈으로 대조하는 시험은 쓰지 않는다: 사본을 단정하는 시험은 아무것도 못 지킨다.
// 여기서는 실물 서버에 보내고 **서버가 무엇을 갖게 됐는지**를 본다.
// 이름이 하나라도 어긋나면 서버가 조용히 0값을 받고, 그 순간 아래 단정이 깨진다.

func TestFullRoundTripThroughRealServer(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// ① 세션.
	code, out := h.run("", "open", "--label", "트랙2")
	if code != 0 {
		t.Fatalf("open 실패(%d): %s", code, out)
	}
	mustContain(t, "open stdout", out, "세션 신규", "프로젝트 "+h.project)

	// ② 항목 등록 — paths·labels·after 가 전부 서버에 닿아야 한다.
	code, out = h.run("", "add", "--id", "t2-pipeline", "--title", "파이프라인 완성",
		"--body", "무엇을 왜 해야 하나", "--path", "pipeline/", "--label", "표시용")
	if code != 0 {
		t.Fatalf("add 실패(%d): %s", code, out)
	}
	mustContain(t, "add stdout", out, "t2-pipeline", "경로 1")

	it, err := h.st.GetItem(ctx, h.project, "t2-pipeline")
	if err != nil {
		t.Fatalf("항목을 서버에서 못 읽었다: %v", err)
	}
	if it.Title != "파이프라인 완성" || it.Body != "무엇을 왜 해야 하나" {
		t.Fatalf("제목·본문이 서버에 안 닿았다: %+v", it)
	}
	if len(it.Paths) != 1 || it.Paths[0] != "pipeline/" {
		t.Fatalf("paths 가 서버에 안 닿았다: %v — wire 의 필드 이름을 의심해라", it.Paths)
	}
	if len(it.Labels) != 1 || it.Labels[0] != "표시용" {
		t.Fatalf("labels 가 서버에 안 닿았다: %v", it.Labels)
	}

	// ③ 선행 조건 — after 는 소문자 json 이름으로 나가야 한다(model.After 에는 태그가 없다).
	code, out = h.run("", "add", "--id", "t2-followup", "--title", "후속", "--body", "본문",
		"--after-item", "t2-pipeline")
	if code != 0 {
		t.Fatalf("선행 있는 add 실패(%d): %s", code, out)
	}
	dep, err := h.st.GetItem(ctx, h.project, "t2-followup")
	if err != nil {
		t.Fatalf("후속 항목을 못 읽었다: %v", err)
	}
	if len(dep.After) != 1 || dep.After[0].Item != "t2-pipeline" {
		t.Fatalf("after 가 서버에 안 닿았다: %+v — afterWire 의 json 태그를 의심해라", dep.After)
	}

	// ④ 추천 — **선점하지 않는다**는 사실이 문장으로 나와야 한다.
	code, out = h.run("", "next")
	if code != 0 {
		t.Fatalf("next 실패(%d): %s", code, out)
	}
	if !strings.Contains(out, "아직 선점하지 않았다") {
		t.Fatalf("추천이 '선점하지 않았다'를 말하지 않는다:\n%s", out)
	}
	if _, err := h.st.GetClaim(ctx, h.project, "t2-pipeline"); err == nil {
		t.Fatal("추천만 했는데 선점 행이 생겼다")
	}

	// ⑤ 선점.
	code, out = h.run("", "pick", "t2-pipeline")
	if code != 0 {
		t.Fatalf("pick 실패(%d): %s", code, out)
	}
	cl, err := h.st.GetClaim(ctx, h.project, "t2-pipeline")
	if err != nil || cl.ReleasedAt != nil {
		t.Fatalf("선점 행이 없다: %+v %v", cl, err)
	}

	// ⑥ 마무리 — 판단 저장 + 종료가 한 번에.
	code, out = h.run("", "finish", "t2-pipeline", "--outcome", "done",
		"--title", "핸드오프", "--body", "① 왜 그렇게 했나 ② 기각한 것")
	if code != 0 {
		t.Fatalf("finish 실패(%d): %s", code, out)
	}
	fin, err := h.st.GetItem(ctx, h.project, "t2-pipeline")
	if err != nil {
		t.Fatalf("끝난 항목을 못 읽었다: %v", err)
	}
	if fin.State != model.ItemDone {
		t.Fatalf("항목 상태가 %q 다", fin.State)
	}
	js := h.judgments(model.JudgmentHandoff)
	if len(js) != 1 {
		t.Fatalf("핸드오프 판단이 %d건이다", len(js))
	}
	if !strings.Contains(js[0].Body, "기각한 것") {
		t.Fatalf("판단 본문이 서버에 안 닿았다: %q", js[0].Body)
	}
}

// body 없이 finish 를 부르면 **그 자리에서** 무엇을 적어야 하는지 낸다(설계 §6).
func TestFinishWithoutBodyGivesTheGuidanceRightThere(t *testing.T) {
	h := newHarness(t)
	if code, _ := h.run("", "open"); code != 0 {
		t.Fatal("세션 열기 실패")
	}
	code, out := h.run("", "finish", "없는항목", "--outcome", "done", "--body", "")
	if code == 0 {
		t.Fatalf("본문 없이 끝났다:\n%s", out)
	}
	mustContain(t, "finish stdout", out,
		"① 왜 그렇게 했나", "② 무엇을 기각했나", "③ 일부러 안 한 것", "④ 확인했으나 못 한 것")
}

// note 는 **받을 세션 수**를 낸다. 0 이면 지금 아무도 안 보고 있다는 사실이 드러나야 한다.
func TestNoteReportsRecipients(t *testing.T) {
	h := newHarness(t)
	if code, _ := h.run("", "open"); code != 0 {
		t.Fatal("세션 열기 실패")
	}
	code, out := h.run("", "note", "--kind", "ask", "--title", "요청", "--body", "이 파일 건드리지 마라")
	if code != 0 {
		t.Fatalf("note 실패(%d): %s", code, out)
	}
	if !strings.Contains(out, "0") {
		t.Fatalf("받을 세션 수가 안 나온다:\n%s", out)
	}
	js := h.judgments(model.JudgmentAsk)
	if len(js) != 1 || js[0].Title != "요청" {
		t.Fatalf("ask 판단이 서버에 안 닿았다: %+v", js)
	}
}

// /events 별칭이 살아 있어야 한다 — 화면(internal/web)이 그 경로를 구독한다.
func TestEventsAliasIsReachable(t *testing.T) {
	h := newHarness(t)
	cli := newClient(ResolveStateDir(envOf(h.env), ""), envOf(h.env), h.home, quietLogger())
	ctx, cancel := context.WithTimeout(context.Background(), 2*timeUnit)
	defer cancel()
	// SSE 는 끝나지 않는 응답이라 타임아웃으로 끊는다. 여기서 보는 것은
	// "그 경로가 404 가 아니다"뿐이다 — 별칭이 죽으면 화면이 조용히 폴백으로 내려간다.
	_, _, err := cli.do(ctx, "GET", "/events?project="+h.project, nil, "")
	if err != nil && !strings.Contains(err.Error(), "context deadline") &&
		!strings.Contains(err.Error(), "Client.Timeout") {
		t.Fatalf("/events 별칭이 살아 있지 않다: %v", err)
	}
	if ae, ok := err.(*APIError); ok {
		t.Fatalf("/events 가 %d 를 냈다: %s", ae.Status, ae.Message)
	}
}

// 경로 실재 판정이 서버 → JSON → 클라이언트 → 렌더까지 살아 오는지 본다.
//
// ★ 이 축을 지키는 시험이 없으면, 어느 계층이 이 필드를 떨어뜨려도 조용하다.
// 레포에 구조체 필드 왕복을 reflect 로 강제하는 그물이 없다.
func TestPathCheckSurvivesTheRestRoundTrip(t *testing.T) {
	h := newHarness(t)

	// ★ open 이 먼저다 — 프로젝트 행을 만드는 것이 이 명령이고, 없으면 add 가 FK 로 죽는다.
	code, out := h.run("", "open", "--label", "경로축")
	if code != 0 {
		t.Fatalf("open 실패(%d): %s", code, out)
	}

	// 이 프로젝트에 없는 경로를 선언한 항목. 등록된 프로젝트가 이것 하나뿐이라 nowhere 다.
	// 플래그는 `--path` 다(단수·반복) — `--paths` 가 아니다.
	code, out = h.run("", "add",
		"--id", "t-path-rt",
		"--title", "경로 실재 왕복",
		"--body", "본문이다",
		"--path", "internal/nope/gone.go")
	if code != 0 {
		t.Fatalf("add 실패(%d): %s", code, out)
	}

	code, out = h.run("", "next")
	if code != 0 {
		t.Fatalf("next 실패(%d): %s", code, out)
	}
	mustContain(t, "fd next 출력", out, "경로 실재:", "어느 프로젝트에도 없다")

	code, out = h.run("", "pick", "t-path-rt")
	if code != 0 {
		t.Fatalf("pick 실패(%d): %s", code, out)
	}
	mustContain(t, "fd pick 출력", out, "경로 실재:")
}

// `fd move <id> --project <그것>` 이 **파일시스템에서 유도된** misregistered 판정에서
// 끝까지 나오는지 본다.
//
// ★ 왜 이것이 따로 필요한가. 이 축의 시험이 두 갈래로 나뉘어 있었다 —
// internal/mcpsrv/render_test.go 는 `ItemPathVerdict` 를 **손으로 구성**해 렌더만 보고,
// 위 TestPathCheckSurvivesTheRestRoundTrip 은 실물 왕복을 치지만 등록 프로젝트가 하나뿐이라
// `nowhere` 만 닿는다. 그래서 **관측이 만든 Suggest 가 그대로 화면의 --project 값이 되는가**를
// 어느 시험도 통째로 안 봤다. 그 이음매에서 값이 뒤바뀌어도(프로젝트 id 대신 경로가 들어가도)
// 아무것도 안 죽는다.
//
// `fd move` 줄은 이 기능이 내는 **유일한 행동 지시**다. 그 값이 틀리면 사용자가 항목을
// 엉뚱한 프로젝트로 옮긴다 — 이 기능이 고치려던 사고를 그대로 재현한다.
func TestMoveSuggestionSurvivesFromFilesystemToScreen(t *testing.T) {
	h := newHarness(t)

	// 프로젝트 둘을 **서로 다른 경로**로 등록한다. 하네스는 FD_PROJECT 하나로 도는데,
	// FD_WORKTREE 로 좌표를 갈아 끼우면 같은 서버에 두 번째 프로젝트를 열 수 있다.
	here := t.TempDir()  // 등록된 프로젝트. 선언 경로가 **없다**
	there := t.TempDir() // 다른 프로젝트. 선언 경로가 **있다**

	const declared = "svc/api/handler.go"
	if err := os.MkdirAll(filepath.Join(there, "svc", "api"), 0o755); err != nil {
		t.Fatalf("두 번째 프로젝트의 경로 생성 실패: %v", err)
	}
	if err := os.WriteFile(filepath.Join(there, declared), []byte("package api\n"), 0o644); err != nil {
		t.Fatalf("두 번째 프로젝트의 파일 생성 실패: %v", err)
	}

	// 좌표를 갈아 끼운 환경. 하네스의 env 를 베끼고 두 축만 바꾼다 —
	// FD_URL·FD_STATE_DIR 를 그대로 물려받아야 두 호출이 **같은 서버**를 친다.
	envAt := func(worktree, project string) map[string]string {
		e := map[string]string{}
		for k, v := range h.env {
			e[k] = v
		}
		e["FD_WORKTREE"] = worktree
		e["FD_PROJECT"] = project
		return e
	}
	envHere := envAt(here, h.project)
	envThere := envAt(there, "otherproj")

	for _, e := range []map[string]string{envHere, envThere} {
		if code, out := h.runEnv(e, "", "open"); code != 0 {
			t.Fatalf("open 실패(%d): %s", code, out)
		}
	}

	// ── 대조 전제: 두 프로젝트가 **정말 둘로** 등록됐는가.
	// 하나로 접히면 지목이 생길 수 없고, 이 시험은 아무것도 안 보면서 초록이 된다.
	projs, err := h.st.ListProjects(t.Context())
	if err != nil {
		t.Fatalf("프로젝트 목록 조회 실패: %v", err)
	}
	if len(projs) != 2 {
		ids := make([]string, 0, len(projs))
		for _, p := range projs {
			ids = append(ids, p.ID+"@"+p.Path)
		}
		t.Fatalf("대조 전제가 깨졌다 — 프로젝트가 %d개다(2개여야 한다): %s",
			len(projs), strings.Join(ids, " · "))
	}

	code, out := h.runEnv(envHere, "", "add",
		"--id", "t-move-suggest",
		"--title", "오등록 지목 왕복",
		"--body", "본문이다",
		"--path", declared)
	if code != 0 {
		t.Fatalf("add 실패(%d): %s", code, out)
	}

	code, out = h.runEnv(envHere, "", "pick", "t-move-suggest")
	if code != 0 {
		t.Fatalf("pick 실패(%d): %s", code, out)
	}

	// ── 본 단정: 지목이 **otherproj 라는 id 그대로** 행동 지시에 실려야 한다.
	mustContain(t, "fd pick 출력", out,
		"경로 실재:",
		"오등록일 수 있다",
		"`fd move t-move-suggest --project otherproj`")

	// ★ 경로가 id 자리에 새는 것을 따로 막는다. 위 단정은 부분 문자열이라
	// "--project otherproj" 를 포함하는 더 긴 잘못된 문자열도 통과시킬 수 있다.
	if strings.Contains(out, "--project "+there) {
		t.Errorf("지목에 프로젝트 id 가 아니라 경로가 실렸다:\n%s", out)
	}
}
