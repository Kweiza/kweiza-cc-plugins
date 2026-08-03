package mcpsrv

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/store"
)

// 없음(404) 의 좌표 — 소비자 좌표계는 **MCP 응답 문자열**이다.
//
// ★ 세션이 읽는 것은 이 문자열뿐이다. 여기서 좌표가 빠지면
// 오타 난 항목 id 와 프로젝트 미등록이 같은 화면이 되고, 그러면 다음 행동이
// "무엇을 확인해야 하나"가 아니라 추측이 된다.

// pick 이 없는 항목 id 를 받으면 **무엇이 없었는지**가 응답 본문에 그대로 있어야 한다.
func TestPickMissingItemNamesTheCoordinate(t *testing.T) {
	repo := newRepo(t)
	svc, st := newSvc(t)
	srv := newServer(t, svc, repo, fullEnv(repo))
	project := filepath.Base(repo)

	// ── 대조가 성립했는지 먼저 단정한다 ─────────────────────────────────────
	// board 가 프로젝트를 등록시킨다. 프로젝트가 없으면 아래 pick 은 "항목이 없다"가 아니라
	// "프로젝트가 없다"로 404 가 나고, 그러면 이 시험은 통과하면서 항목 축을 전혀 안 본다.
	frames := serve(t, srv, call("board", map[string]any{}))
	if len(frames) != 1 {
		t.Fatalf("board 응답이 %d개다", len(frames))
	}
	if _, isErr := toolText(t, frames[0]); isErr {
		t.Fatalf("전제가 깨졌다 — board 가 실패했다")
	}
	if _, err := st.GetProject(context.Background(), project); err != nil {
		t.Fatalf("전제가 깨졌다 — 프로젝트 %q 가 등록돼 있어야 한다: %v", project, err)
	}
	if _, err := st.GetItem(context.Background(), project, "t9-nonexistent"); err == nil {
		t.Fatal("전제가 깨졌다 — 항목 t9-nonexistent 는 없어야 한다")
	}

	frames = serve(t, srv, call("pick", map[string]any{"item_id": "t9-nonexistent"}))
	if len(frames) != 1 {
		t.Fatalf("pick 응답이 %d개다", len(frames))
	}
	body, _ := toolText(t, frames[0])

	want := "항목 " + project + "/t9-nonexistent 가 없다"
	if !strings.Contains(body, want) {
		t.Errorf("응답에 %q 가 없다 — 무엇이 없었는지 알 수 없다:\n%s", want, body)
	}
	// ★ 표식 문구가 문장 끝에 한 번 더 붙는 회귀(…가 없다: 없다)를 막는다.
	if strings.Contains(body, "없다: 없다") {
		t.Errorf("표식 문구가 두 번 붙었다:\n%s", body)
	}
}

// NotFoundGuidance — 하류가 실어 보낸 처방이 이기고, 없으면 고정 문구로 접는다. 순수 함수다.
func TestNotFoundGuidancePrefersCarried(t *testing.T) {
	if got := NotFoundGuidance(&carrier{g: "항목 id 오타다 — fd next 로 확인해라."}, "cp"); got != "항목 id 오타다 — fd next 로 확인해라." {
		t.Fatalf("실어 온 처방을 안 썼다: %q", got)
	}
	// 빈 처방을 실어 오면 그것은 "처방이 없다"와 같다 — 고정 문구로 접는다.
	if got := NotFoundGuidance(&carrier{g: "  "}, "cp"); !strings.Contains(got, "cp") {
		t.Fatalf("빈 처방에서 고정 문구로 안 접었다: %q", got)
	}
	// 좌표 자체가 없는 오류(로컬 배선)도 처방이 있어야 한다.
	if got := NotFoundGuidance(&store.NotFoundError{Kind: store.NFItem, ID: "x"}, "cp"); strings.TrimSpace(got) == "" {
		t.Fatal("처방이 비었다")
	}
	// 실어 온 처방도 외부에서 온 문자열이라 잘라 싣는다.
	if got := NotFoundGuidance(&carrier{g: strings.Repeat("가", 900)}, "cp"); len([]rune(got)) > 610 {
		t.Fatalf("처방이 안 잘렸다(%d룬)", len([]rune(got)))
	}
}

// carrier 는 REST 클라이언트가 서버 처방을 실어 올리는 모양을 흉내 낸다.
// (운영 배선의 실물은 cmd/fd 의 notFoundRelay 다.)
type carrier struct{ g string }

func (c *carrier) Error() string            { return "항목 cp/x 가 없다" }
func (c *carrier) Unwrap() error            { return store.ErrNotFound }
func (c *carrier) NotFoundGuidance() string { return c.g }
