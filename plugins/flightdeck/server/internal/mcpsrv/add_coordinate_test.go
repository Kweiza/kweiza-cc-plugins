package mcpsrv

import (
	"path/filepath"
	"strings"
	"testing"
)

// add 응답은 **어느 프로젝트에 넣었는지**와 **되돌리는 길**을 말해야 한다.
//
// ─── 왜 도구를 더하지 않고 이 길로 갔나 ──────────────────────────────────────
//
// 문제는 결함이 나는 표면과 고칠 수 있는 표면이 다르다는 것이었다: 오등록은 대부분 MCP 로
// add 하다 나는데(프로젝트 좌표를 세션의 cwd 가 정한다) 되돌리는 `move` 는 CLI 에만 있다.
//
// 처음에는 MCP 에 move 도구를 더하는 쪽으로 갔다. **설계 §6 이 그 판정을 뒤집었다** —
// "도구 수를 예산 안에 묶는 이유는 컨텍스트다"가 근거와 함께 적힌 원칙이고,
// 시험 둘(TestToolTableIsEight · TestInitializeAndToolsListRoundTrip)이 그것을 강제한다.
// 그리고 같은 절이 이 부류의 처방을 이미 정해 뒀다:
// **"규율은 응답에 싣는다 — 필요할 때만, 그 자리에서."**
//
// ★ 위 두 이름을 정확히 적는 이유: 앞선 판의 그 줄은 `TestToolTableIsSix` 를 인용했는데
// 그 이름은 레포에 없다(land 를 더하며 TestToolTableIsSeven 으로 개명됐다) —
// grep 하는 사람이 빈손이 됐다. 판정 자체는 안 뒤집혔다: 도구가 여섯에서 일곱이 된 것은
// 예산을 안 건드렸기 때문이고(늘어난 고정비는 이름 하나, 설명 90자 상한 그대로 — DESIGN §6),
// "예산을 쓰는 도구는 안 더한다"는 이 항목의 판정은 그래서 지금도 산다.
//
// 그래서 예산을 안 쓰는 자리를 골랐다. MCP 세션은 Bash 로 CLI 를 부를 수 있으므로
// **능력은 이미 있고 없던 것은 앎**이다. 앎이 필요한 순간은 오등록이 **만들어지는 그때**이고,
// 그 순간 세션이 읽고 있는 화면이 add 응답이다. 앞선 판의 그 문구는 프로젝트를 한 글자도
// 안 냈다 — 틀린 순간에 신호가 아예 없었다.
//
// 버린 대안: 보드·pick 꼬리에서 "이 항목의 경로가 이 프로젝트에 없다"를 감지하는 안.
// 헛걸림이 크다 — 항목은 **아직 없는 경로**를 정당하게 가리킬 수 있다(앞으로 만들 파일).
// 상시 헛경보는 결국 읽히지 않는다. 후속으로 냈다.

func TestAddResponseNamesTheProjectAndTheWayBack(t *testing.T) {
	repo := newRepo(t)
	svc, _ := newSvc(t)
	srv := newServer(t, svc, repo, fullEnv(repo))
	project := filepath.Base(repo)

	frames := serve(t, srv, call("add", map[string]any{
		"id": "t-coord", "title": "제목", "body": "본문",
	}))
	if len(frames) != 1 {
		t.Fatalf("응답이 %d개다", len(frames))
	}
	body, isErr := toolText(t, frames[0])
	if isErr {
		t.Fatalf("add 가 실패했다:\n%s", body)
	}

	// ① 어디에 들어갔는가. 이것이 없으면 틀린 순간에 화면에 아무 신호가 없다.
	if !strings.Contains(body, project) {
		t.Errorf("응답이 프로젝트 %q 를 안 말한다 — 오등록을 만든 그 자리에서 알아챌 길이 없다:\n%s",
			project, body)
	}
	// ② 되돌리는 길. 능력은 이미 있고(Bash→CLI) 없던 것은 앎이다.
	for _, want := range []string{"fd move", "t-coord", "--project"} {
		if !strings.Contains(body, want) {
			t.Errorf("응답에 %q 가 없다 — 되돌리는 길을 모른 채 넘어간다:\n%s", want, body)
		}
	}
}

// ★ 이 항목은 move 를 도구로 더하지 않고 응답 문구로 푸는 쪽으로 판정했다 — 그 판정이
// 여기서 못박는 전부다. 도구 **개수**의 정본은 protocol_test.go 의 TestToolTableIsEight
// 하나여야 한다(정본이 둘이면 하나만 고쳐진 날 조용히 갈린다) — 그래서 개수는 여기서 안 잰다.
// ★ 다만 그 "하나"는 아직 의도지 사실이 아니다 — 지금 개수를 실제로 세는 시험은 셋이다:
// TestToolTableIsEight · TestInitializeAndToolsListRoundTrip · TestPickGainsItemIDsWithoutGrowingToolCount.
// 여기서 안 재는 판정은 그대로 옳지만, 넷째를 만들지 않는 것만으로 "정본 하나"가 되지는 않는다.
func TestFixingMisregistrationDidNotGrowTheToolTable(t *testing.T) {
	for _, n := range ToolNames() {
		if n == "move" {
			t.Error("move 가 도구 목록에 있다 — 이 항목은 응답 문구로 푸는 쪽으로 판정했다")
		}
	}
}
