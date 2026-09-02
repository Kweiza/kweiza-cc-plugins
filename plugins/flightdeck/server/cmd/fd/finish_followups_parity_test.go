package main

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// ── 후속이 판단에 **실제로 이어졌는가**, 그리고 두 창이 같은 것을 만드는가 ──────────
//
// ★ 이 파일이 있는 이유. `TestFinishCarriesFollowups` 는 후속 **항목이 원장에 생겼는지**
// 만 본다 — 그런데 이 손잡이가 존재하는 이유는 항목이 생기는 것이 아니라 **판단과
// 한 트랜잭션으로 묶이는 것**이다(`fd add` 로도 항목은 생긴다). 연결이 끊긴 채
// 항목만 생겨도 그 시험은 초록이고, 그러면 이 손잡이는 `fd add` 의 비싼 별명이 된다.
//
// 그리고 표면이 둘이다(MCP 도구 · CLI 플래그). 둘이 갈리면 같은 병이 자리만 옮긴다 —
// codex 창만 연결을 잃고, Claude 창에서 보면 멀쩡하다. 그 비대칭은 **아무 화면에도
// 안 뜬다.** 그래서 둘이 만든 원장을 정규화해 맞대 본다.

// judgmentsLinkedTo 는 항목에 걸린 판단 id 들이다.
//
// judgment_link 에는 조회 접근자가 없어(store/backup.go 의 linkCols 주석) SQL 을 직접 친다.
func judgmentsLinkedTo(t *testing.T, h *harness, itemID string) []string {
	t.Helper()
	rows, err := h.st.DB().QueryContext(context.Background(),
		`SELECT judgment_id FROM judgment_link WHERE target_kind='item' AND target_id=?`, itemID)
	if err != nil {
		t.Fatalf("판단 링크 조회 실패(%s): %v", itemID, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("판단 링크 읽기 실패: %v", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("판단 링크 순회 실패: %v", err)
	}
	sort.Strings(out)
	return out
}

// followupShape 는 후속 하나가 원장에 남긴 **관측 가능한 모양**이다.
// 표면 둘을 맞댈 때 문자열 하나로 비교하려고 접는다.
func followupShape(t *testing.T, h *harness, id string) string {
	t.Helper()
	it, err := h.st.GetItem(context.Background(), h.project, id)
	if err != nil {
		t.Fatalf("후속 항목이 원장에 없다(%s): %v", id, err)
	}
	return fmt.Sprintf("title=%q body=%q paths=%v labels=%v state=%s links=%d",
		it.Title, it.Body, it.Paths, it.Labels, it.State, len(judgmentsLinkedTo(t, h, id)))
}

// TestFinishLinksFollowupsToTheSameJudgment 는 이 손잡이의 **존재 이유**를 문다.
//
// 선두와 후속이 **같은 판단 id** 에 걸려야 한다. 그것이 "한 트랜잭션에 묶였다"의
// 유일한 관측이고, 다음 세션의 `pick` 이 「연결된 판단」을 그 항목과 함께 내주는 근거다.
func TestFinishLinksFollowupsToTheSameJudgment(t *testing.T) {
	h := newHarness(t)
	myClaim(t, h, "h-link-lead")

	code, out := h.run("", "finish", "h-link-lead",
		"--outcome", "done", "--title", "끝냈다", "--body", "왜 그렇게 했나",
		"--followups", `[{"id":"h-link-followup","title":"뒤에 할 것","body":"이유가 있다"}]`)
	if code != 0 {
		t.Fatalf("finish 가 %d 로 끝났다:\n%s", code, out)
	}

	lead := judgmentsLinkedTo(t, h, "h-link-lead")
	fu := judgmentsLinkedTo(t, h, "h-link-followup")
	if len(lead) == 0 {
		t.Fatal("선두 항목에 걸린 판단이 없다 — finish 가 판단을 안 남겼다")
	}
	if len(fu) == 0 {
		t.Fatal("후속에 걸린 판단이 0건이다 — 항목만 생기고 **연결이 안 붙었다**. " +
			"그러면 다음 세션의 pick 이 「왜 이게 생겼나」를 못 준다 — 이 손잡이가 fd add 의 비싼 별명이 된다")
	}
	same := false
	for _, a := range lead {
		for _, b := range fu {
			if a == b {
				same = true
			}
		}
	}
	if !same {
		t.Fatalf("선두(%v)와 후속(%v)이 **다른** 판단에 걸렸다 — 한 트랜잭션이 아니다", lead, fu)
	}
}

// TestMCPAndCLIFinishLeaveTheSameLedger 는 표면 둘이 **같은 것**을 만드는지 맞댄다.
//
// ★ 항목이 요구한 관문이 이것이다: "MCP 와 CLI 가 같은 서버 표면을 부르는지 시험으로
// 물어라 — 두 벌이 갈리면 같은 병이 자리만 옮긴다."
func TestMCPAndCLIFinishLeaveTheSameLedger(t *testing.T) {
	h := newHarness(t)

	// ── CLI 쪽 ──
	myClaim(t, h, "p-cli-lead")
	if code, out := h.run("", "finish", "p-cli-lead",
		"--outcome", "done", "--title", "끝냈다", "--body", "판단 본문",
		"--followups", `[{"id":"p-cli-fu","title":"뒤에 할 것","body":"이유","paths":["a/b.go"],"labels":["tickler"]}]`,
	); code != 0 {
		t.Fatalf("CLI finish 가 %d 로 끝났다:\n%s", code, out)
	}

	// ── MCP 쪽 ── 같은 값을 도구 인자로 준다
	rig := newMCPRig(t, h, "cc-mcp-parity")
	frames := mcpServe(t, rig,
		mcpCall("add", map[string]any{"id": "p-mcp-lead", "title": "내가 쥔 것", "body": "b"}),
		mcpCall("pick", map[string]any{"item_id": "p-mcp-lead"}),
		mcpCall("finish", map[string]any{
			"item_id": "p-mcp-lead", "outcome": "done", "title": "끝냈다", "body": "판단 본문",
			"followups": []any{map[string]any{
				"id": "p-mcp-fu", "title": "뒤에 할 것", "body": "이유",
				"paths": []any{"a/b.go"}, "labels": []any{"tickler"},
			}},
		}),
	)
	for i, name := range []string{"add", "pick", "finish"} {
		if txt, isErr := mcpText(t, frames[i]); isErr {
			t.Fatalf("MCP %s 가 실패했다:\n%s", name, txt)
		}
	}

	// ── 맞댄다 ──
	cli := followupShape(t, h, "p-cli-fu")
	mcp := followupShape(t, h, "p-mcp-fu")
	if cli != mcp {
		t.Fatalf("같은 값을 줬는데 두 표면이 **다른 원장**을 남겼다 — 한쪽만 고쳐진 것이다\n"+
			"  CLI: %s\n  MCP: %s", cli, mcp)
	}
	// 눈멂 방지: 둘 다 "연결 0건"이면 위 비교는 통과한다. 그 상태가 이 항목의 결함 자체다.
	if n := len(judgmentsLinkedTo(t, h, "p-cli-fu")); n == 0 {
		t.Fatalf("양쪽이 같긴 한데 **둘 다 연결이 0건**이다 — 같이 깨진 것을 같다고 통과시킬 뻔했다")
	}
	if got := itemState(t, h, "p-cli-fu"); got != model.ItemOpen {
		t.Fatalf("후속이 %q 로 생겼다 — open 이어야 다음 세션이 집는다", got)
	}
}
