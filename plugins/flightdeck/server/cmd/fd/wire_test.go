package main

import (
	"context"
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
