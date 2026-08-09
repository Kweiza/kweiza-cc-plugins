package mcpsrv

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// 회수 근거의 축 수는 문서와 응답이 **같은 수**를 말해야 한다
// ─────────────────────────────────────────────────────────────────────────────
//
// 설계 §4 가 회수 근거의 축 수를 한 문장으로 못박고, pick 의 steal_reason 거절이
// 그 문장을 사람이 읽는 자리에 다시 낸다. 두 벌이라 한쪽만 고치면 다른 쪽이 조용히
// 거짓이 된다 — 그리고 그럴 뻔했다. 이 저장소에서 축 수를 단정하던 유일한 시험
// (TestPickRefusesSteal)은 "다섯 축"이라는 **문자열이 있는지**만 봤으므로, 문서가
// 여섯이 돼도 초록이었다. 존재 단정은 표류를 못 잡는다.
//
// 그래서 이 시험이 잠그는 것은 값이 아니라 **일치**다: DESIGN 이 말하는 수 낱말이 곧
// 응답이 말하는 수 낱말이어야 한다. 지금 사실(여섯 · 여섯째는 종료 선언)도 함께
// 잠근다 — 축을 늘리는 사람의 빨간불이 여기서 먼저 켜져야 그 사람이 §4 를 읽는다.
//
// ★ land 의 회수 거절은 **일부러 다섯이다.** 회수 대상이 줄 행이라 항목에 붙는
// 여섯째가 존재하지 않는다. 그 갈림도 함께 단정한다 — 안 그러면 다음 사람이
// "둘이 어긋났다"고 보고 맞추고, 그 순간 응답이 없는 축을 보라고 말하게 된다.

// reclaimDesignDoc 은 설계 정본을 읽는다.
// internal/mcpsrv → internal → server → plugins/flightdeck 세 단계 위다.
func reclaimDesignDoc(t *testing.T) string {
	t.Helper()
	p := filepath.Join("..", "..", "..", "DESIGN.md")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("DESIGN.md 를 못 읽었다(%s) — 이 시험의 좌표가 틀렸다: %v", p, err)
	}
	return string(b)
}

var reclaimAxisCountRe = regexp.MustCompile(`회수는 사람만, 사유 필수, 그리고 근거를 (\S+?) 축으로`)

func TestReclaimAxisCountAgreesBetweenDesignAndPickRefusal(t *testing.T) {
	design := reclaimDesignDoc(t)
	loc := reclaimAxisCountRe.FindStringSubmatchIndex(design)
	if loc == nil {
		t.Fatalf("DESIGN.md §4 의 회수 근거 머리줄을 못 찾았다 — 그 문장이 이 시험의 정본이다")
	}
	count := design[loc[2]:loc[3]]

	// 문단 안에서만 본다. 문서 전체를 보면 다른 절의 낱말이 이 단정을 통과시킨다.
	seg := design[loc[0]:]
	if len(seg) > 1600 {
		seg = seg[:1600]
	}

	if count != "여섯" {
		t.Errorf("DESIGN.md 의 회수 근거 축이 %q 다 — 항목 쪽 종료 선언이 여섯째로 붙었다", count)
	}
	if !strings.Contains(seg, "종료 선언") {
		t.Errorf("DESIGN.md §4 가 여섯째 축을 이름으로 안 부른다:\n%s", seg)
	}

	repo := newRepo(t)
	svc, st := newSvc(t)
	srv := newServer(t, svc, repo, fullEnv(repo))

	frames := serve(t, srv,
		call("add", map[string]any{"id": "t-axis", "title": "제목", "body": "본문"}),
		call("pick", map[string]any{"item_id": "t-axis", "steal_reason": "저쪽이 죽은 것 같다"}),
	)
	text, isErr := toolText(t, frames[1])
	if !isErr {
		t.Fatalf("steal_reason 이 조용히 무시됐다:\n%s", text)
	}
	if want := count + " 축을 나란히 본 뒤에야 한다"; !strings.Contains(text, want) {
		t.Errorf("pick 거절이 DESIGN 과 다른 수를 말한다(DESIGN 은 %q):\n%s", want, text)
	}
	if !strings.Contains(text, "종료 선언") {
		t.Errorf("pick 거절이 여섯째 축을 이름으로 안 부른다:\n%s", text)
	}
	// 거절은 아무것도 안 쓴다. 이 단정이 없으면 문구만 맞고 행이 생겨도 초록이다.
	if n := countRows(t, st, `SELECT count(*) FROM claim`); n != 0 {
		t.Fatalf("거절했는데 선점 %d행이 생겼다", n)
	}
}

// TestLaneReclaimStaysAtFiveAxes 는 land 의 회수 거절이 **일부러** 다섯인 것을 잠근다.
// 회수 대상이 줄 행이라 항목에 붙는 여섯째가 없다. 두 문구를 같은 수로 맞추면
// 이 응답이 존재하지 않는 축을 보라고 말하게 된다.
func TestLaneReclaimStaysAtFiveAxes(t *testing.T) {
	repo := newRepo(t)
	svc, _ := newSvc(t)
	srv := newServer(t, svc, repo, fullEnv(repo))

	frames := serve(t, srv, call("land", map[string]any{"release": "그만 좀 물려줘"}))
	text, isErr := toolText(t, frames[0])
	if !isErr {
		t.Fatalf("release 인자가 거절되지 않았다:\n%s", text)
	}
	if !strings.Contains(text, "다섯 축을 나란히 본 뒤에야 한다") {
		t.Errorf("레인 회수 거절의 축 수가 다섯이 아니다:\n%s", text)
	}
	if strings.Contains(text, "종료 선언") {
		t.Errorf("레인 회수 거절이 존재하지 않는 축을 보라고 말한다:\n%s", text)
	}
}
