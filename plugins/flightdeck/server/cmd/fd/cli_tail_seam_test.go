package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// CLI 응답 꼬리 — **남이 남긴 ask·blocked 가 이 화면에 닿는가.**
//
// ★ 이것이 이 항목의 가장 큰 값이다. `Tail` 을 채우는 자리가 mcpsrv 하나뿐이라
// codex 창(훅 전용이라 MCP 표면이 없다)에서는 **남의 판단이 영영 안 보인다.**
// 조율은 그 알림을 읽는 것에서 시작하므로, 꼬리가 없다는 것은 그 창이 조율 밖에
// 있다는 뜻이다 — 보드에는 멀쩡히 떠 있으면서.

// otherSessionAsks 는 **다른** 세션이 ask 하나를 남긴 상태를 만든다.
func otherSessionAsks(t *testing.T, h *harness, title string) {
	t.Helper()
	ctx := context.Background()
	if err := h.st.UpsertProject(ctx, model.Project{ID: h.project, Path: "/p", DefaultBranch: "main"}); err != nil {
		t.Fatalf("프로젝트 등록 실패: %v", err)
	}
	if err := h.st.UpsertMachine(ctx, model.Machine{ID: "m-other", Hostname: "other-host"}); err != nil {
		t.Fatalf("머신 등록 실패: %v", err)
	}
	other, _, err := h.st.OpenSession(ctx, h.project, "m-other", "/wt-other", "cc-other", "옆 세션", time.Time{})
	if err != nil {
		t.Fatalf("옆 세션 열기 실패: %v", err)
	}
	if _, err := h.st.AddJudgment(ctx, model.Judgment{
		Project: h.project, SessionID: other.ID, Kind: model.JudgmentAsk,
		Title: title, Body: "이 경로를 만진다", At: time.Now(),
	}); err != nil {
		t.Fatalf("옆 세션 판단 저장 실패: %v", err)
	}
}

// ① 쓰기 명령의 화면에 남의 알림이 붙는다.
func TestCLIWriteCommandCarriesTheTail(t *testing.T) {
	h := newHarness(t)
	otherSessionAsks(t, h, "내가 render.go 를 만진다")

	if code, out := h.run("", "open"); code != 0 {
		t.Fatalf("세션 열기 실패(%d):\n%s", code, out)
	}
	code, out := h.run("", "note", "--kind", "decision", "--title", "t", "--body", "b")
	if code != 0 {
		t.Fatalf("note 가 %d 로 끝났다:\n%s", code, out)
	}
	if !strings.Contains(out, "내가 render.go 를 만진다") {
		t.Fatalf("CLI 화면에 남의 ask 가 안 붙는다 — 이 창은 조율 밖이다:\n%s", out)
	}
}

// ② 알림이 0건이면 **없다고 말한다.** 침묵하지 않는다.
//
// ★ 침묵하면 "남긴 사람이 없다"와 "이 화면이 그 축을 안 본다"가 구분되지 않는다.
// MCP 꼬리가 같은 이유로 「알림: 다른 세션이 남긴 ask·blocked 가 없다」를 찍는다.
func TestCLITailSaysWhenThereIsNothing(t *testing.T) {
	h := newHarness(t)
	if code, out := h.run("", "open"); code != 0 {
		t.Fatalf("세션 열기 실패(%d):\n%s", code, out)
	}
	code, out := h.run("", "note", "--kind", "decision", "--title", "t", "--body", "b")
	if code != 0 {
		t.Fatalf("note 가 %d 로 끝났다:\n%s", code, out)
	}
	if !strings.Contains(out, "알림") {
		t.Fatalf("알림이 0건인데 화면이 그 사실을 안 말한다:\n%s", out)
	}
}

// ③ **자기가 남긴 것은 자기 꼬리에 안 뜬다** — 방금 쓴 것이 알림으로 되돌아오면 잡음이다.
func TestCLITailExcludesMyOwnNotes(t *testing.T) {
	h := newHarness(t)
	if code, out := h.run("", "open"); code != 0 {
		t.Fatalf("세션 열기 실패(%d):\n%s", code, out)
	}
	if code, out := h.run("", "note", "--kind", "ask", "--title", "내 자신의 ask 다", "--body", "b"); code != 0 {
		t.Fatalf("첫 note 가 %d 로 끝났다:\n%s", code, out)
	}
	code, out := h.run("", "note", "--kind", "decision", "--title", "t2", "--body", "b2")
	if code != 0 {
		t.Fatalf("둘째 note 가 %d 로 끝났다:\n%s", code, out)
	}
	if strings.Contains(out, "내 자신의 ask 다") {
		t.Fatalf("자기가 남긴 판단이 자기 꼬리에 되돌아온다:\n%s", out)
	}
}

// ④ 꼬리는 **쓰기 명령 전부**에 붙는다 — note 하나만이면 반쪽이다.
//
// ★ MCP 는 모든 도구 응답에 꼬리를 단다. 그 규율이 CLI 에서만 명령별로 갈리면,
// 사람은 "어떤 명령은 알림을 낸다"를 배우게 되고 그 규칙을 못 외운 자리에서 알림을 놓친다.
func TestCLITailIsOnEveryWriteCommand(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"open", []string{"open"}},
		{"pick", []string{"pick", "t-tail"}},
		{"note", []string{"note", "--kind", "decision", "--title", "t", "--body", "b"}},
		{"land", []string{"land"}},
		{"finish", []string{"finish", "t-tail", "--outcome", "done", "--title", "t", "--body", "b"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			otherSessionAsks(t, h, "옆에서 만지는 중이다")
			if code, out := h.run("", "open"); code != 0 {
				t.Fatalf("사전 세션 열기 실패(%d):\n%s", code, out)
			}
			if err := h.st.AddItem(context.Background(), model.Item{
				Project: h.project, ID: "t-tail", Title: "꼬리 시험", Body: "b", CreatedAt: time.Now(),
			}); err != nil {
				t.Fatalf("항목 등록 실패: %v", err)
			}
			// finish·land 는 선점이 있어야 성립한다 — pick 자신을 재는 갈래만 빼고 먼저 집는다.
			if c.name != "pick" {
				if code, out := h.run("", "pick", "t-tail"); code != 0 {
					t.Fatalf("사전 선점 실패(%d):\n%s", code, out)
				}
			}
			code, out := h.run("", c.args...)
			if code != 0 {
				t.Fatalf("%s 가 %d 로 끝났다:\n%s", c.name, code, out)
			}
			if !strings.Contains(out, "옆에서 만지는 중이다") {
				t.Fatalf("%s 화면에 남의 알림이 안 붙는다:\n%s", c.name, out)
			}
		})
	}
}
