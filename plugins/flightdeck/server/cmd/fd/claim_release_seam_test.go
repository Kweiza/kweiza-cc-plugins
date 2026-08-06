package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// `fd claim release` — 죽은 세션의 선점을 사람이 회수하는 CLI 표면.
//
// 레인 회수(land_seam_test.go)와 같은 결로 검증한다: 실물 서버 왕복으로,
// 회수하는 사람은 그 세션이 아니라는 전제(세션 좌표 없는 환경)를 그대로 두고.

// deadClaim 은 "무신호 세션이 항목을 쥔 채 침묵하는" 상태를 만든다 —
// 2026-08-07 실측(무신호 9~24.5h 세션 4곳이 12건 동결)의 재현이다.
func deadClaim(t *testing.T, h *harness, item string) model.Session {
	t.Helper()
	ctx := context.Background()
	if err := h.st.UpsertProject(ctx, model.Project{ID: h.project, Path: "/p", DefaultBranch: "main"}); err != nil {
		t.Fatalf("프로젝트 등록 실패: %v", err)
	}
	if err := h.st.UpsertMachine(ctx, model.Machine{ID: "m-dead", Hostname: "dead-host"}); err != nil {
		t.Fatalf("머신 등록 실패: %v", err)
	}
	sess, _, err := h.st.OpenSession(ctx, h.project, "m-dead", "/wt-dead", "cc-dead-"+item, "죽은 세션")
	if err != nil {
		t.Fatalf("세션 열기 실패: %v", err)
	}
	if err := h.st.AddItem(ctx, model.Item{
		Project: h.project, ID: item, Title: "회수 대상", Body: "b", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("항목 등록 실패: %v", err)
	}
	if _, err := h.st.ClaimItem(ctx, h.project, item, sess.ID); err != nil {
		t.Fatalf("선점 실패: %v", err)
	}
	return sess
}

func TestClaimReleaseFreesADeadSessionsClaim(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	sess := deadClaim(t, h, "cr-dead")
	before := len(h.judgments(model.JudgmentDecision))

	// ★ 회수하는 사람은 **그 세션이 아니다.** 세션 좌표를 지운 환경으로 부른다 —
	//   여기서 세션을 요구하면 탈출구가 다시 막힌다(레인 회수와 같은 판정).
	e := map[string]string{}
	for k, v := range h.env {
		e[k] = v
	}
	delete(e, "CLAUDE_CODE_SESSION_ID")
	e["USER"] = "당번유저"

	code, out := h.runEnv(e, "", "claim", "release",
		"--item", "cr-dead", "--reason", "무신호 20시간 — 보드 근거로 회수")
	if code != 0 {
		t.Fatalf("회수가 %d 로 끝났다:\n%s", code, out)
	}
	mustContain(t, "회수 stdout", out, "회수", "cr-dead")

	// 선점이 풀리고 항목은 open 으로 돌아온다 — 다음 pick 이 집을 수 있어야 회수다.
	it, err := h.st.GetItem(ctx, h.project, "cr-dead")
	if err != nil {
		t.Fatal(err)
	}
	if it.State != model.ItemOpen {
		t.Fatalf("항목이 open 으로 안 돌아왔다: %s", it.State)
	}
	if got, _ := h.st.ClaimedItems(ctx, sess.ID); len(got) != 0 {
		t.Fatalf("회수했는데 선점이 남아 있다: %v", got)
	}

	// ★ 판단이 남아야 한다. 이것이 sqlite3 직접 UPDATE 와 이 명령의 **유일한 차이**다.
	js := h.judgments(model.JudgmentDecision)
	if len(js) != before+1 {
		t.Fatalf("회수했는데 판단이 %d건 늘었다 — 1건이어야 한다", len(js)-before)
	}
	body := js[len(js)-1].Body
	mustContain(t, "회수 판단", body,
		"무신호 20시간 — 보드 근거로 회수", // 사유
		sess.ID,  // 점유자
		"마지막 신호") // 신호 관측 — 이 회수가 무엇을 보고 한 판정인지
	// ★ actor 폴백이 이 이음매에서 가장 조용한 축이다 — 빈 값으로 도착하면 서버는
	//   "행위자: 대시보드(사람)"라고 적고, 셸에서 부른 회수가 원장에 거짓으로 남는다.
	if !strings.Contains(body, "행위자: 당번유저@") {
		t.Fatalf("셸 좌표가 판단에 안 남았다 — wire 의 actor 필드 이름을 의심해라:\n%s", body)
	}
	if strings.Contains(body, "대시보드(사람)") {
		t.Fatalf("셸에서 부른 회수가 원장에 '대시보드가 눌렀다'로 남았다:\n%s", body)
	}

	// 재회수는 실패한다 — 살아 있는 선점이 없다.
	if code, out := h.runEnv(e, "", "claim", "release",
		"--item", "cr-dead", "--reason", "둘째 회수"); code == 0 {
		t.Fatalf("죽은 선점의 재회수가 통과했다:\n%s", out)
	}
}

// 사유·대상 없는 회수는 거절된다. 거절이 선점을 건드리면 안 된다.
func TestClaimReleaseRefusesWithoutReasonOrItem(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	sess := deadClaim(t, h, "cr-guard")

	if code, out := h.run("", "claim", "release", "--item", "cr-guard"); code == 0 {
		t.Fatalf("사유 없는 회수가 통과했다:\n%s", out)
	}
	if code, out := h.run("", "claim", "release", "--reason", "사유는 있다"); code == 0 {
		t.Fatalf("대상 없는 회수가 통과했다:\n%s", out)
	}
	if got, _ := h.st.ClaimedItems(ctx, sess.ID); len(got) != 1 {
		t.Fatalf("거절당한 회수가 선점을 건드렸다: %v", got)
	}
	// 모르는 하위 명령은 조용히 무시하지 않는다.
	if code, out := h.run("", "claim", "grab"); code == 0 {
		t.Fatalf("모르는 claim 하위 명령이 0 으로 끝났다:\n%s", out)
	}
}
