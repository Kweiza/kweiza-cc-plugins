package main

import (
	"context"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// `fd show` 의 **CLI 이음매**를 지킨다 — 실물 서버를 세워 왕복시킨다.
//
// ★ 무엇이 위험한가: 이 경로는 읽기라 질의 인자(`?project=…`)로 좌표를 나른다. 인자
// 이름이 서버의 requireQuery 와 어긋나면 400 이고, 경로가 `/items/next` 와 부딪히면
// 엉뚱한 핸들러가 답한다 — 둘 다 가짜 서버로는 원리적으로 못 본다.
//
// ★ 그리고 이 파일의 첫 시험은 **이 동사의 존재 이유** 자체다: 닫힌 항목의 판단에
// 사람 경로(셸)로 닿는가. 실측 2026-09-17 로 그런 판단이 1,985건이었고 읽는 문이 없었다.

// TestShowCLIReachesJudgmentsOnAClosedItem 은 닫힌 항목의 판단이 화면까지 오는지 잰다.
func TestShowCLIReachesJudgmentsOnAClosedItem(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	const item = "t-show-closed"

	if code, out := h.run("", "add", "--id", item, "--title", "옛 제목", "--body", "옛 본문"); code != 0 {
		t.Fatalf("전제 구성 실패 — add 가 %d 로 끝났다:\n%s", code, out)
	}
	if code, out := h.run("", "note", "--kind", "decision", "--item", item,
		"--body", "닫히기 전에 남긴 판단이다"); code != 0 {
		t.Fatalf("전제 구성 실패 — note 가 %d 로 끝났다:\n%s", code, out)
	}
	// ★ 항목을 닫는다. 여기서부터 pick 은 이 항목을 영영 안 준다(state='open' 만 본다).
	if err := h.st.SetItemState(ctx, h.project, item, model.ItemDone, ""); err != nil {
		t.Fatalf("전제 구성 실패 — 항목 닫기: %v", err)
	}
	// 닫힌 **뒤에** 얹은 판단도 나와야 한다 — 실측에서 40건이 그 자리에 있었다.
	if code, out := h.run("", "note", "--kind", "ask", "--item", item,
		"--body", "닫힌 뒤에 물어 둔 것이다"); code != 0 {
		t.Fatalf("전제 구성 실패 — 닫힌 뒤 note 가 %d 로 끝났다:\n%s", code, out)
	}

	code, out := h.run("", "show", item)
	if code != 0 {
		t.Fatalf("show 가 %d 로 끝났다:\n%s", code, out)
	}
	mustContain(t, "show 출력", out,
		"done",           // 상태를 먼저 말한다
		"닫히기 전에 남긴 판단이다", // 전문이다
		"닫힌 뒤에 물어 둔 것이다",
		"걸린 판단 2건",
	)
}

// TestShowCLIListsRevisionHistoryWithReasons 는 amend 가 쌓은 개정을 되읽는다.
//
// ★ 이 왕복이 이 항목의 둘째 결함이었다 — `item_revision` 은 2026-09-16 에 생겼는데
// 읽는 경로가 레포 전체에 0건이었다. RenderAmend 가 「개정 N」이라는 좌표를 약속하면서
// 그 좌표를 열 문이 없었다.
func TestShowCLIListsRevisionHistoryWithReasons(t *testing.T) {
	h := newHarness(t)
	const item = "t-show-revisions"

	if code, out := h.run("", "add", "--id", item, "--title", "옛 제목", "--body", "옛 본문"); code != 0 {
		t.Fatalf("전제 구성 실패 — add 가 %d 로 끝났다:\n%s", code, out)
	}
	if code, out := h.run("", "amend", item, "--title", "고친 제목", "--reason", "제목 오타"); code != 0 {
		t.Fatalf("전제 구성 실패 — amend 가 %d 로 끝났다:\n%s", code, out)
	}
	if code, out := h.run("", "amend", item, "--body", "고친 본문", "--reason", "전제가 틀렸다"); code != 0 {
		t.Fatalf("전제 구성 실패 — 둘째 amend 가 %d 로 끝났다:\n%s", code, out)
	}

	code, out := h.run("", "show", item)
	if code != 0 {
		t.Fatalf("show 가 %d 로 끝났다:\n%s", code, out)
	}
	mustContain(t, "show 출력", out, "개정 2건", "rev 1", "rev 2", "제목 오타", "전제가 틀렸다")
	// 사슬 복원이 배선까지 살아 있는가 — 축이 비면 "무엇을 고쳤나"가 사라진다.
	if !strings.Contains(out, "title") || !strings.Contains(out, "body") {
		t.Errorf("바뀐 축이 화면에 없다 — 사슬 복원이 왕복 어딘가에서 끊겼다:\n%s", out)
	}
	// 지금 값도 함께 나야 한다(개정 이력만 내면 무엇이 현재인지 모른다).
	mustContain(t, "show 출력", out, "제목: 고친 제목", "고친 본문")
}

// TestShowCLIRefusesWithoutItemIDAndReportsMissingItem 은 두 거절 갈래다.
//
// 없는 항목은 **표준 not-found** 로 나와야 한다 — 서버가 404 에 좌표(항목 p/x)를 실어
// 보내고 CLI 가 그것을 그대로 옮긴다. 인자 없는 호출은 서버에 가기 전에 끊는다.
func TestShowCLIRefusesWithoutItemIDAndReportsMissingItem(t *testing.T) {
	h := newHarness(t)

	code, out := h.run("", "show")
	if code != 2 {
		t.Fatalf("항목 id 없는 show 가 %d 로 끝났다 — 2여야 한다:\n%s", code, out)
	}
	if !strings.Contains(out, "읽을 항목 id 를 줘라") {
		t.Errorf("무엇이 없는지를 안 말한다:\n%s", out)
	}

	code, out = h.run("", "show", "t-show-없는-항목")
	if code != 1 {
		t.Fatalf("없는 항목의 show 가 %d 로 끝났다 — 1이어야 한다:\n%s", code, out)
	}
	if !strings.Contains(out, "t-show-없는-항목") {
		t.Errorf("없음 응답이 무엇이 없었는지를 안 말한다 — 404 좌표가 어딘가에서 지워졌다:\n%s", out)
	}
}
