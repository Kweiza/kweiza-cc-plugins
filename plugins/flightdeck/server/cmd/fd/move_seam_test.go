package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/service"
	"github.com/kweiza/flightdeck/internal/store"
)

// `fd move` 의 **CLI 이음매**를 지킨다.
//
// ★ 무엇이 위험한가: cmd/fd 의 moveReq 필드 이름이 internal/api 의 moveRequest 와 어긋나면
// 서버가 **조용히 0값을 받는다.** JSON 디코딩은 모르는 필드를 그냥 버리고 없는 필드를
// 0값으로 두므로, 오타 하나가 오류가 아니라 **빈 값**으로 나타난다.
//
// 지금은 project 가 비면 서비스가 거절하므로 시끄럽게 죽는다. 그러나 **to 가 비면**
// 판정이 "대상이 비었다"로 답하고, 그러면 화면에 뜨는 것은 "인자를 안 줬다"라서
// **원인이 한 겹 가려진다** — 사용자는 자기가 준 `--project` 를 다시 들여다본다.
//
// ★ 앞선 판은 이 축을 **실물 이동 10건으로만** 확인했다. 그건 일회성이라 다음 어긋남을
// 못 잡는다. 그래서 하네스로 실제 명령을 태우고 **서버가 실제로 갖게 된 item.project** 를
// 단정한다 — 요청을 보냈다는 단정은 "보냈다"만 말하고 "무엇이 도착했나"는 말하지 못한다.
//
// ★ 가짜 서버를 끼우지 않는다. mcp_seam_test.go 의 newMCPRig 가 같은 이유로 같은 규율을
// 걸어 뒀다: 막으려는 결함이 "배선이 딴 데를 본다"인데 배선을 시험이 대신 만들면
// 그 축을 원리적으로 못 본다.

// registerProject 는 대상 프로젝트를 **서비스로** 등록한다(시험 준비물이지 피시험 코드가 아니다).
//
// fd move 는 없는 프로젝트로는 옮기지 않는다 — 자동 생성이 유령 프로젝트를 만든 전례가 있어
// 일부러 거절한다. 그래서 대상이 먼저 있어야 이 시험이 이음매를 볼 수 있다.
func registerProject(t *testing.T, h *harness, id string) {
	t.Helper()
	dir := t.TempDir()
	if _, err := h.svc.OpenSession(context.Background(), service.OpenSessionInput{
		Project: id, ProjectPath: dir, MachineID: "m-setup", Hostname: "m-setup",
		Worktree: dir, CCSessionID: "cc-setup-" + id,
	}); err != nil {
		t.Fatalf("대상 프로젝트 %q 등록 실패: %v", id, err)
	}
}

func TestMoveCLISeamLandsInTheTargetProject(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	const item = "t-move-seam"
	const target = "otherproj"

	registerProject(t, h, target)
	if code, out := h.run("", "add", "--id", item, "--title", "제목", "--body", "본문"); code != 0 {
		t.Fatalf("전제 구성 실패 — add 가 %d 로 끝났다:\n%s", code, out)
	}

	// ── 대조가 성립했는지 먼저 단정한다 ─────────────────────────────────────
	// 항목이 원래 자리에 없으면 아래 이동 단정은 무엇이 옮겨졌는지 말하지 못한다.
	if _, err := h.st.GetItem(ctx, h.project, item); err != nil {
		t.Fatalf("전제가 깨졌다 — 항목이 원래 프로젝트 %q 에 없다: %v", h.project, err)
	}
	if _, err := h.st.GetItem(ctx, target, item); err == nil {
		t.Fatalf("전제가 깨졌다 — 옮기기 전인데 항목이 이미 %q 에 있다", target)
	}

	code, out := h.run("", "move", item, "--project", target)
	if code != 0 {
		t.Fatalf("move 가 %d 로 끝났다:\n%s\n"+
			"이음매가 어긋나면 여기서 '대상이 비었다' 같은 **한 겹 가려진** 사유가 뜬다 — "+
			"인자를 안 준 것이 아니라 필드 이름이 안 맞은 것이다.", code, out)
	}

	// ★ 이 단정이 이 시험의 본체다: **서버가 실제로 갖게 된 item.project.**
	got, err := h.st.GetItem(ctx, target, item)
	if err != nil {
		t.Fatalf("항목이 대상 프로젝트 %q 에 없다: %v\n"+
			"move 가 0 으로 끝났는데 서버에는 안 도착했다 — wire.go 의 moveReq 필드 이름이 "+
			"internal/api 의 moveRequest 와 어긋났는지부터 봐라.", target, err)
	}
	if got.Project != target {
		t.Errorf("항목의 프로젝트가 %q 다 — %q 여야 한다", got.Project, target)
	}

	// 옛 자리에는 없어야 한다 — 사본이 생기면 같은 id 가 두 프로젝트에 산다.
	if _, err := h.st.GetItem(ctx, h.project, item); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("옛 프로젝트 %q 에 항목이 남아 있다(err=%v) — 이동이 아니라 복사가 됐다", h.project, err)
	}
}

// 이음매가 어긋났을 때 **무엇이 보이는지**를 박아 둔다.
//
// to 를 안 주면 CLI 가 먼저 막는다(서버까지 안 간다). 그 문구가 "대상 프로젝트를 줘라"인 것이
// 중요하다 — 필드명이 어긋나 서버가 빈 to 를 받는 경우와 **같은 화면**이 되면 둘을 구분할
// 길이 없어진다. 여기서는 CLI 가 먼저 막는다는 사실 자체를 고정한다.
func TestMoveWithoutTargetIsRefusedBeforeTheServer(t *testing.T) {
	h := newHarness(t)
	if code, out := h.run("", "add", "--id", "t-move-2", "--title", "제목", "--body", "본문"); code != 0 {
		t.Fatalf("전제 구성 실패: %s", out)
	}
	code, out := h.run("", "move", "t-move-2")
	if code == 0 {
		t.Fatalf("대상 없이 move 가 성공했다:\n%s", out)
	}
	if !strings.Contains(out, "대상 프로젝트를 줘라") {
		t.Errorf("거절 문구가 무엇을 줘야 하는지 안 말한다:\n%s", out)
	}
}

// 없는 프로젝트로는 안 옮긴다 — 자동 생성이 유령 프로젝트를 만든 전례가 있다.
func TestMoveToUnknownProjectIsRefused(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	const item = "t-move-3"
	if code, out := h.run("", "add", "--id", item, "--title", "제목", "--body", "본문"); code != 0 {
		t.Fatalf("전제 구성 실패: %s", out)
	}
	code, out := h.run("", "move", item, "--project", "nonexistent-project")
	if code == 0 {
		t.Fatalf("없는 프로젝트로 move 가 성공했다:\n%s", out)
	}
	// 거절당했으면 항목은 **원래 자리에 그대로** 있어야 한다.
	if _, err := h.st.GetItem(ctx, h.project, item); err != nil {
		t.Errorf("거절당했는데 항목이 원래 프로젝트에서 사라졌다: %v", err)
	}
}
