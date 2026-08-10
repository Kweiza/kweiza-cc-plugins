package main

import (
	"context"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// `fd after cut` 의 **CLI 이음매**를 지킨다.
//
// ★ 무엇이 위험한가: cmd/fd 의 요청 구조체 필드 이름이 internal/api 의 cutAfterRequest 와
// 어긋나면 서버가 **조용히 0값을 받는다.** JSON 디코딩은 모르는 필드를 버리고 없는 필드를
// 0값으로 두므로, 오타 하나가 오류가 아니라 빈 값으로 나타난다.
//
// 그리고 이 명령에서 그 실패는 특히 나쁘다. dep 이 빈 값으로 도착하면 서버는
// "정확히 하나여야 한다"로 거절하는데, 사람은 자기가 방금 준 `--item` 을 다시 들여다본다 —
// 원인이 한 겹 가려진다. move_seam_test.go 가 같은 이유로 같은 규율을 건다.

func TestAfterCutCLISeamActuallyDetachesTheDep(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	const dep, waiter = "t-after-dep", "t-after-waiter"

	for _, id := range []string{dep, waiter} {
		if code, out := h.run("", "add", "--id", id, "--title", "제목", "--body", "본문"); code != 0 {
			t.Fatalf("전제 구성 실패 — add(%s) 가 %d 로 끝났다:\n%s", id, code, out)
		}
	}
	if err := h.st.Tx(ctx, func(tx *store.Tx) error {
		return tx.AddAfter(h.project, waiter, model.After{Item: dep})
	}); err != nil {
		t.Fatalf("선행 등록 실패: %v", err)
	}
	// ── 대조 전제: 끊기 전에 정말 걸려 있는가.
	if it, err := h.st.GetItem(ctx, h.project, waiter); err != nil || len(it.After) != 1 {
		t.Fatalf("전제가 깨졌다 — 선행이 %d건이다(err=%v)", len(it.After), err)
	}

	code, out := h.run("", "after", "cut", waiter, "--item", dep)
	if code != 0 {
		t.Fatalf("after cut 이 %d 로 끝났다:\n%s", code, out)
	}

	// ★ **서버가 실제로 갖게 된 상태**를 단정한다. "요청을 보냈다"는 무엇이 도착했는지 말하지 못한다.
	it, err := h.st.GetItem(ctx, h.project, waiter)
	if err != nil {
		t.Fatalf("항목 조회 실패: %v", err)
	}
	if len(it.After) != 0 {
		t.Errorf("선행이 %d건 남았다 — 이음매가 빈 값을 보냈다: %+v", len(it.After), it.After)
	}
	// 역인덱스도 같이 줄어야 한다. 이 축이 어긋나면 pick 추천이 조용히 틀린다.
	if n, _ := h.st.Dependents(ctx, h.project, dep); n != 0 {
		t.Errorf("역인덱스가 %d 다 — 행은 끊겼는데 순위 축이 안 따라왔다", n)
	}
	if !strings.Contains(out, dep) {
		t.Errorf("무엇을 끊었는지 출력에 없다:\n%s", out)
	}
}

// 축을 안 주거나 둘 이상 주면 **서버에 가기 전에** 거절한다.
//
// 빈 요청을 보내면 서버가 "정확히 하나"로 거절하지만, 그 왕복은 오프라인에서 아웃박스에
// 쌓이는 쓰기가 된다 — 되돌릴 수 없는 명령을 알 수 있는 오류로 미리 세우는 편이 낫다.
func TestAfterCutRefusesUnlessExactlyOneAxisIsGiven(t *testing.T) {
	h := newHarness(t)
	for _, c := range []struct {
		name string
		args []string
	}{
		{"축이 없다", []string{"after", "cut", "x"}},
		{"축이 둘이다", []string{"after", "cut", "x", "--item", "a", "--sha", "0f19bf3"}},
		{"항목 id 가 없다", []string{"after", "cut", "--item", "a"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, out := h.run("", c.args...)
			if code == 0 {
				t.Fatalf("통과했다:\n%s", out)
			}
			if !strings.Contains(out, "after cut") {
				t.Errorf("쓰는 법이 출력에 없다:\n%s", out)
			}
		})
	}
}
