package main

import (
	"context"
	"testing"
)

// `fd amend` 의 **CLI 이음매**를 지킨다.
//
// ★ 무엇이 위험한가: cmd/fd 의 amendReq 필드 이름이 internal/api 의 amendRequest 와
// 어긋나면 서버가 **조용히 0값을 받는다.** JSON 디코딩은 모르는 필드를 버리고 없는 필드를
// 0값으로 두므로, 오타 하나가 오류가 아니라 빈 값으로 나타난다. label_seam_test.go(이 파일이
// 그대로 베낀 원본)와 같은 이유로 실물 서버를 세워 **서버가 실제로 갖게 된 상태**를
// 단정한다 — 가짜 서버로는 그 어긋남을 원리적으로 못 본다.
func TestAmendCLISeamActuallyChangesStoredItem(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	const item = "t-amend-seam"

	if code, out := h.run("", "add", "--id", item, "--title", "옛 제목", "--body", "옛 본문", "--path", "old/path.go"); code != 0 {
		t.Fatalf("전제 구성 실패 — add 가 %d 로 끝났다:\n%s", code, out)
	}

	// ── 대조가 성립했는지 먼저 단정한다 ─────────────────────────────────────
	pre, err := h.st.GetItem(ctx, h.project, item)
	if err != nil {
		t.Fatalf("전제가 깨졌다 — 항목 조회 실패: %v", err)
	}
	if pre.Title != "옛 제목" || pre.Body != "옛 본문" || len(pre.Paths) != 1 || pre.Paths[0] != "old/path.go" {
		t.Fatalf("전제가 깨졌다 — 시작 상태가 %+v 다", pre)
	}

	// ★ title·body·path 를 **한 호출에 함께** 준다. wire.go 의 amendReq 에서 필드이
	// 서로 뒤바뀌면(예: Title 값이 Body 자리로, Paths 가 빠지는 등) 이 시험이 잡는다 —
	// 따로따로 부르면 각 축이 단독으로는 "그대로 반영된 것처럼" 보일 수 있다
	// (label_seam_test.go 의 Add/Rm 이 뒤바뀌는 결함과 같은 결).
	code, out := h.run("", "amend", item,
		"--title", "새 제목", "--body", "새 본문", "--path", "new/path.go", "--reason", "이음매 시험")
	if code != 0 {
		t.Fatalf("amend 가 %d 로 끝났다:\n%s", code, out)
	}

	// ★ 이 단정이 이 시험의 본체다: **서버가 실제로 갖게 된 항목의 상태.**
	got, err := h.st.GetItem(ctx, h.project, item)
	if err != nil {
		t.Fatalf("항목 조회 실패: %v", err)
	}
	if got.Title != "새 제목" {
		t.Errorf("제목이 %q다 — \"새 제목\"이어야 한다", got.Title)
	}
	if got.Body != "새 본문" {
		t.Errorf("본문이 %q다 — \"새 본문\"이어야 한다", got.Body)
	}
	if len(got.Paths) != 1 || got.Paths[0] != "new/path.go" {
		t.Errorf("경로가 %v다 — [\"new/path.go\"] 여야 한다", got.Paths)
	}

	// 화면도 mcpsrv.RenderAmend 를 통해 실제 변화분을 낸다는 규율을 함께 잠근다.
	mustContain(t, "amend 출력", out, "title·body·paths 를 고쳤다", "제목: 새 제목", "경로 1: new/path.go")
}

// TestAmendCLIOmittedFlagAxisStaysUntouched 는 fs.Visit 판정이 실제로 동작하는지 본다.
//
// ★ 기본값 비교로 가르면 `--title ""`(생략)와 "빈 문자열로 바꿔라"가 안 갈린다.
// 이 시험은 그 반대 방향을 잡는다: --title 을 아예 안 준 호출이 제목을 건드리지
// 않아야 한다 — 건드리면 amendReq.Title 이 기본값 비교(빈 문자열 == 생략)로
// 판정됐다는 뜻이고, 그러면 `fd amend <id> --body …`가 매번 제목을 지운다.
func TestAmendCLIOmittedFlagAxisStaysUntouched(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	const item = "t-amend-omit"

	if code, out := h.run("", "add", "--id", item, "--title", "지킬 제목", "--body", "옛 본문"); code != 0 {
		t.Fatalf("전제 구성 실패 — add 가 %d 로 끝났다:\n%s", code, out)
	}

	code, out := h.run("", "amend", item, "--body", "새 본문", "--reason", "본문만 고친다")
	if code != 0 {
		t.Fatalf("amend 가 %d 로 끝났다:\n%s", code, out)
	}

	got, err := h.st.GetItem(ctx, h.project, item)
	if err != nil {
		t.Fatalf("항목 조회 실패: %v", err)
	}
	if got.Title != "지킬 제목" {
		t.Errorf("제목이 %q다 — --title 을 안 줬으면 안 건드려야 한다", got.Title)
	}
	if got.Body != "새 본문" {
		t.Errorf("본문이 %q다 — \"새 본문\"이어야 한다", got.Body)
	}
	mustContain(t, "amend 출력", out, "amend · "+item+" 의 body 를 고쳤다")
}
