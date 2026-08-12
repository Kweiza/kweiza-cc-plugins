package main

import (
	"context"
	"testing"
)

// `fd label` 의 **CLI 이음매**를 지킨다.
//
// ★ 무엇이 위험한가: cmd/fd 의 labelReq 필드 이름이 internal/api 의 labelRequest 와
// 어긋나면 서버가 **조용히 0값을 받는다.** JSON 디코딩은 모르는 필드를 버리고 없는 필드를
// 0값으로 두므로, 오타 하나가 오류가 아니라 빈 값으로 나타난다. label_seam_test.go
// (internal/api, package api)는 이 축을 **손으로 쓴 JSON 문자열**로만 본다 — `labelPath()` 를
// 부르지도 `labelReq{}` 를 만들지도 않으므로, URL 오타나 `Add`/`Rm` 필드가 서로 뒤바뀌는
// 결함은 못 잡는다. move_seam_test.go·after_cut_seam_test.go 가 같은 이유로 실물 서버를
// 세워 **서버가 실제로 갖게 된 상태**를 단정하는 것과 같은 결로, 이 시험도 그렇게 한다.
func TestLabelCLISeamActuallyChangesStoredLabels(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	const item = "t-label-seam"

	if code, out := h.run("", "add", "--id", item, "--title", "제목", "--body", "본문", "--label", "old"); code != 0 {
		t.Fatalf("전제 구성 실패 — add 가 %d 로 끝났다:\n%s", code, out)
	}

	// ── 대조가 성립했는지 먼저 단정한다 ─────────────────────────────────────
	// 시작 꼬리표가 정확히 ["old"] 가 아니면 아래 add/rm 단정이 무엇이 바뀌었는지 말하지 못한다.
	pre, err := h.st.GetItem(ctx, h.project, item)
	if err != nil {
		t.Fatalf("전제가 깨졌다 — 항목 조회 실패: %v", err)
	}
	if len(pre.Labels) != 1 || pre.Labels[0] != "old" {
		t.Fatalf("전제가 깨졌다 — 시작 꼬리표가 %v 다(\"old\" 하나여야 한다)", pre.Labels)
	}

	// ★ add 와 rm 을 **한 호출에 함께** 준다. wire.go 의 labelReq 에서 Add/Rm 필드가
	// 서로 뒤바뀌면 — "old" 를 더하려다 이미 있어 무변화, "tickler" 를 빼려다 없어
	// 무변화가 되어 최종 꼬리표가 여전히 ["old"] 로 남는다. 그 결함을 잡는 것이
	// 이 시험 하나의 존재 이유다(따로따로 add 만, rm 만 부르면 이 결함을 못 본다 —
	// 두 축이 뒤바뀌어도 각각 단독으로는 "요청한 축 하나가 그대로 반영된 것처럼" 보인다).
	code, out := h.run("", "label", item, "--add", "tickler", "--rm", "old")
	if code != 0 {
		t.Fatalf("label 이 %d 로 끝났다:\n%s", code, out)
	}

	// ★ 이 단정이 이 시험의 본체다: **서버가 실제로 갖게 된 항목의 꼬리표.**
	got, err := h.st.GetItem(ctx, h.project, item)
	if err != nil {
		t.Fatalf("항목 조회 실패: %v", err)
	}
	if len(got.Labels) != 1 || got.Labels[0] != "tickler" {
		t.Fatalf("항목의 꼬리표가 %v 다 — [\"tickler\"] 여야 한다(add·rm 이 실제로 반영 안 됐거나 "+
			"뒤바뀌었을 수 있다)", got.Labels)
	}

	// 화면도 실제 변화분(요청한 것이 아니라)을 낸다는 규율을 함께 잠근다.
	mustContain(t, "label 출력", out, "실제로 더한 것: tickler", "실제로 뺀 것: old")
}
