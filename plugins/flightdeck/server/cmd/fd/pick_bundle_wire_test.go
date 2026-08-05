package main

import (
	"context"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/service"
)

// 배선 시험 — mcpBackend.Pick 이 실물 서버를 상대로 item_ids 를 실제로 쓰기까지 옮기는가.
//
// 태스크 9 까지는 이 도달이 없었다. mcpBackend.Pick 은 `in.ItemID == ""` 이면
// **추천**(GET /items/next, 읽기)으로 분기했는데, item_ids 만 채운 요청은 ItemID 가
// 비어 있으니 그 분기를 그대로 타 아무것도 안 집고 추천을 돌려줬다 — 태스크 7 의
// Critical 이 배포 경로에서 되살아나는 자리였다.
//
// mcpsrv 의 시험(newServer)은 service 를 **직접 주입**해서 이 결함을 못 봤다. 여기서는
// `fd mcp` 와 같은 순서(newApp → newMCPBackend)로 조립하고, 실물 서버(newHarness)에
// 실제로 HTTP 를 보내 **store 에 무엇이 남았는지**를 본다.
func TestMCPBackendPickBundleDoesNotFallToRecommend(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if code, out := h.run("", "open", "--label", "배선묶음"); code != 0 {
		t.Fatalf("세션 열기 실패(%d):\n%s", code, out)
	}
	for _, id := range []string{"wire-lead", "wire-m1"} {
		if code, out := h.run("", "add", "--id", id, "--title", id+" 제목", "--body", id+" 본문"); code != 0 {
			t.Fatalf("항목 등록 실패(%s, %d):\n%s", id, code, out)
		}
	}

	// mcpBackend 를 운영과 같은 순서로 조립한다(가짜 백엔드를 끼우지 않는다) —
	// 이 시험이 막으려는 결함이 바로 "배선이 딴 데를 본다" 이다.
	dir := t.TempDir()
	app := newApp(envOf(h.env), quietLogger(), dir, strings.NewReader(""))
	openRes, _, err := app.OpenSession(ctx, "cc-session-uuid-1", "")
	if err != nil {
		t.Fatalf("세션 좌표 확보 실패: %v", err)
	}
	sess := openRes.Session.ID

	backend := newMCPBackend(app)
	res, err := backend.Pick(ctx, service.PickInput{
		Project: h.project, SessionID: sess,
		ItemID:  "", // ★ 일부러 비운다 — 결함이 나던 갈래가 정확히 이것이다.
		ItemIDs: []string{"wire-lead", "wire-m1"},
	})
	if err != nil {
		t.Fatalf("묶음 선점 실패: %v", err)
	}
	if res.Mode == service.PickRecommended {
		t.Fatalf("item_ids 만 줬는데 추천 경로로 빠졌다(mode=%s) — Pick 의 분기 순서를 의심해라", res.Mode)
	}
	if res.Mode != service.PickClaimed {
		t.Fatalf("mode 가 %q 다(claimed 를 기대)", res.Mode)
	}
	if res.Bundle == nil {
		t.Fatal("묶음으로 집었는데 응답에 Bundle 절이 없다")
	}

	// 좌표계는 "서버가 실제로 무엇을 갖게 됐나"다 — 응답을 셌다는 것만으로는
	// "보냈다"만 말하고 "저장됐다"는 말하지 못한다.
	for _, id := range []string{"wire-lead", "wire-m1"} {
		cl, err := h.st.GetClaim(ctx, h.project, id)
		if err != nil || cl.ReleasedAt != nil {
			t.Fatalf("항목 %s 의 선점 행이 서버에 없다: %+v %v", id, cl, err)
		}
	}
}
