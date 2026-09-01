package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// MCP 에는 있고 CLI 에만 없던 손잡이 셋.
//
//	pick(leave:)      → fd pick --leave "사유"
//	finish(followups:) → fd finish --followups '<JSON>'
//	land(resources:)   → fd land --resource <이름> (반복 가능)
//
// ★ 왜 이것이 「있으면 좋은 것」이 아닌가. codex 는 오늘 **훅 전용**이라 MCP 표면이
// 없다 — 그 창에서 이 셋은 CLI 에 없으면 **아예 없는 기능**이다. 그리고 없는 손잡이는
// 대체 경로로 흘러가는데, 그 대체가 원장을 망가뜨린다: 선점 반납이 없으면 사람이
// finish(dropped) 로 때우고, 그러면 항목 id 가 바뀌어 이력이 끊기고 큐 수지에
// 「일 없이 닫힌 항목」이 하나 쌓인다.

// myClaim 은 **이 세션이** 항목 하나를 쥔 상태를 만든다.
//
// claim_release_seam_test.go 의 deadClaim 과 다르다 — 저쪽은 남의(죽은) 선점을
// 사람이 회수하는 축이고, 이쪽은 자기 선점을 스스로 놓는 축이다.
func myClaim(t *testing.T, h *harness, item string) {
	t.Helper()
	ctx := context.Background()
	if code, out := h.run("", "open"); code != 0 {
		t.Fatalf("세션 열기 실패(%d):\n%s", code, out)
	}
	if err := h.st.AddItem(ctx, model.Item{
		Project: h.project, ID: item, Title: "내가 쥔 것", Body: "b", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("항목 등록 실패: %v", err)
	}
	if code, out := h.run("", "pick", item); code != 0 {
		t.Fatalf("선점 실패(%d):\n%s", code, out)
	}
}

func itemState(t *testing.T, h *harness, id string) model.ItemState {
	t.Helper()
	it, err := h.st.GetItem(context.Background(), h.project, id)
	if err != nil {
		t.Fatalf("항목 조회 실패(%s): %v", id, err)
	}
	return it.State
}

// ① fd pick --leave — 자기 선점을 놓는다. 항목은 **open 으로 살아 돌아온다.**
func TestPickLeaveReleasesMyOwnClaim(t *testing.T) {
	h := newHarness(t)
	myClaim(t, h, "h-leave")

	code, out := h.run("", "pick", "--leave", "이 창에서는 못 한다")
	if code != 0 {
		t.Fatalf("pick --leave 가 %d 로 끝났다:\n%s", code, out)
	}
	if got := itemState(t, h, "h-leave"); got != model.ItemOpen {
		t.Fatalf("반납 뒤 항목 상태가 %q 다 — open 이어야 한다(id·이력이 살아야 한다)", got)
	}
	// ★ dropped 로 닫히면 안 된다 — 그것이 이 손잡이가 막으려는 대체 경로다.
	if got := itemState(t, h, "h-leave"); got == model.ItemDropped {
		t.Fatal("항목이 dropped 로 닫혔다 — 반납은 닫기가 아니다")
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("반납했는데 화면이 아무 말도 안 한다")
	}
}

// 사유 없는 반납은 거절한다 — 사유 없는 반납은 나중에 되짚을 수 없다.
func TestPickLeaveRequiresAReason(t *testing.T) {
	h := newHarness(t)
	myClaim(t, h, "h-leave-noreason")

	code, out := h.run("", "pick", "--leave", "")
	if code == 0 {
		t.Fatalf("빈 사유로 반납이 성공했다:\n%s", out)
	}
	if got := itemState(t, h, "h-leave-noreason"); got != model.ItemClaimed {
		t.Fatalf("거절됐는데 항목 상태가 %q 로 바뀌었다 — claimed 로 남아야 한다", got)
	}
}

// ② fd finish --followups — 후속이 **같은 호출에서** 판단에 이어진다.
//
// ★ 미리 add 하면 판단과의 연결을 영영 못 산다(그래서 이 손잡이가 필요하다).
func TestFinishCarriesFollowups(t *testing.T) {
	h := newHarness(t)
	myClaim(t, h, "h-finish")

	code, out := h.run("", "finish", "h-finish",
		"--outcome", "done",
		"--title", "끝냈다",
		"--body", "본문",
		"--followups", `[{"id":"h-followup-1","title":"뒤에 할 것","body":"이유가 있다","paths":["a/b.go"]}]`)
	if code != 0 {
		t.Fatalf("finish --followups 가 %d 로 끝났다:\n%s", code, out)
	}

	it, err := h.st.GetItem(context.Background(), h.project, "h-followup-1")
	if err != nil {
		t.Fatalf("후속 항목이 원장에 없다: %v", err)
	}
	if it.Title != "뒤에 할 것" {
		t.Fatalf("후속의 제목이 %q 다", it.Title)
	}
	if len(it.Paths) != 1 || it.Paths[0] != "a/b.go" {
		t.Fatalf("후속의 경로가 %v 다 — 인자가 다 안 실렸다", it.Paths)
	}
}

// 깨진 JSON 은 **조용히 버리지 않고** 거절한다.
//
// ★ 조용히 버리면 사람은 후속을 등록했다고 믿고 세션을 닫는다 — 그리고 그 후속은
// 어디에도 없다. 이 도구에서 가장 비싼 종류의 침묵이다.
func TestFinishRefusesBrokenFollowupsJSON(t *testing.T) {
	h := newHarness(t)
	myClaim(t, h, "h-finish-bad")

	code, out := h.run("", "finish", "h-finish-bad",
		"--outcome", "done", "--title", "t", "--body", "b",
		"--followups", `[{"id":`)
	if code == 0 {
		t.Fatalf("깨진 JSON 인데 성공했다:\n%s", out)
	}
	if got := itemState(t, h, "h-finish-bad"); got == model.ItemDone {
		t.Fatal("후속이 깨졌는데 항목이 닫혔다 — 후속을 잃은 채 마무리된다")
	}
	if !strings.Contains(out, "followups") && !strings.Contains(out, "후속") {
		t.Fatalf("거절 사유가 무엇이 틀렸는지 안 말한다:\n%s", out)
	}
}

// ③ fd land --resource — 자원을 걸고 줄을 선다.
func TestLandTakesResources(t *testing.T) {
	h := newHarness(t)
	myClaim(t, h, "h-land")

	code, out := h.run("", "land", "--resource", "env:dell", "--resource", "db:staging")
	if code != 0 {
		t.Fatalf("land --resource 가 %d 로 끝났다:\n%s", code, out)
	}
	for _, want := range []string{"env:dell", "db:staging"} {
		if !strings.Contains(out, want) {
			t.Fatalf("줄을 섰는데 화면이 자원 %q 를 안 낸다 — 무엇을 쥐었는지가 안 보이면 조율이 안 된다:\n%s", want, out)
		}
	}
}
