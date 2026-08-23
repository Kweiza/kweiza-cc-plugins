package legacy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// `import --apply` → `export --to-legacy` 왕복.
//
// **완전 왕복이 아니다.** 무엇이 안 돌아오는지가 이 파일의 주제이고,
// [RoundTripLosses] 가 그 목록이며 아래 시험이 그 목록대로만 잃는지를 실물로 단정한다.
// 산문에만 적어 두면 코드가 더 잃기 시작해도 아무도 모른다.

func applyFixture(t *testing.T) (*store.Store, ImportPlan) {
	t.Helper()
	dbp1 := filepath.Join(t.TempDir(), "fd.db")
	// ★ 적용은 기동에서 분리돼 있다(설계 §7 ①) — 열기 전에 올린다.
	if err := store.Migrate(context.Background(), dbp1, nil); err != nil {
		t.Fatalf("DB 적용 실패: %v", err)
	}
	st, err := store.Open(dbp1)
	if err != nil {
		t.Fatalf("DB 를 열지 못했다: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	p := planFixture(t)
	if _, err := Apply(context.Background(), st, p, fxCode); err != nil {
		t.Fatalf("이관 적용 실패: %v", err)
	}
	return st, p
}

func exportFixture(t *testing.T) (outDir string, res ExportResult, p ImportPlan) {
	t.Helper()
	st, p := applyFixture(t)
	outDir = t.TempDir()
	res, err := ExportLegacy(context.Background(), st, "cp", outDir)
	if err != nil {
		t.Fatalf("되쓰기 실패: %v", err)
	}
	return outDir, res, p
}

func readOut(t *testing.T, dir, rel string) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		if os.IsNotExist(err) {
			return "", false
		}
		t.Fatalf("되쓴 파일을 읽지 못했다(%s): %v", rel, err)
	}
	return string(b), true
}

// ── 돌아오는 것 ────────────────────────────────────────────────────────────

// 큐 항목은 **바이트 단위로** 돌아온다. 그것이 이 되쓰기의 존재 이유다 —
// 원본과 diff 로 대조할 수 없으면 "되쓸 수 있다"는 주장을 확인할 방법이 없다.
func TestRoundTripQueueItemsAreByteIdentical(t *testing.T) {
	outDir, _, _ := exportFixture(t)
	for _, rel := range []string{
		".claude/queue/items/alert-channel.md",
		".claude/queue/items/contracts-batch8.md",
		".claude/queue/done/batch7-design-doc.md",
	} {
		want := string(readFixture(t, "code/"+rel))
		got, ok := readOut(t, outDir, rel)
		if !ok {
			t.Errorf("%s 가 되쓰이지 않았다", rel)
			continue
		}
		if got != want {
			t.Errorf("%s 가 원본과 다르다\n--- 원본 앞 300자 ---\n%s\n--- 되쓴 것 앞 300자 ---\n%s",
				rel, clip(want, 300), clip(got, 300))
		}
	}
}

// 핸드오프는 파싱하지 않으므로 본문이 통째로 그대로 돌아온다.
func TestRoundTripHandoffsAreByteIdentical(t *testing.T) {
	outDir, res, _ := exportFixture(t)
	if res.Handoffs != 2 {
		t.Fatalf("핸드오프가 %d건 되쓰였다(기대 2)", res.Handoffs)
	}
	for _, name := range []string{
		"2026-07-30-0923-contracts-batch7-landed.md",
		"2026-07-31-CONTRACT-HANDOFF-raw-replay.md", // 규약 밖 파일명도 그대로 돌아온다
	} {
		rel := ".claude/handoffs/" + name
		want := string(readFixture(t, "code/"+rel))
		got, ok := readOut(t, outDir, rel)
		if !ok {
			t.Errorf("%s 가 되쓰이지 않았다", rel)
			continue
		}
		if got != want {
			t.Errorf("%s 의 본문이 달라졌다(원본 %d바이트 · 되쓴 것 %d바이트)", rel, len(want), len(got))
		}
	}
}

// 비규약 절이 **이름 그대로** 돌아오는가 — 이 이관이 지키려는 것의 핵심이다.
func TestRoundTripKeepsNonCanonicalSectionNames(t *testing.T) {
	outDir, _, _ := exportFixture(t)
	got, ok := readOut(t, outDir, ".claude/sessions/7.md")
	if !ok {
		t.Fatal("7.md 가 되쓰이지 않았다")
	}
	orig := string(readFixture(t, "code/.claude/sessions/7.md"))

	origCard, _ := ParseSessionCard("7.md", []byte(orig))
	gotCard, rs := ParseSessionCard("7.md", []byte(got))
	for _, r := range rs {
		if r.Fatal {
			t.Fatalf("되쓴 카드를 다시 못 읽는다 — [%s] %s", r.Code, r.Detail)
		}
	}
	if len(gotCard.Sections) != len(origCard.Sections) {
		t.Fatalf("절 수가 %d 다(원본 %d)", len(gotCard.Sections), len(origCard.Sections))
	}
	for i, s := range origCard.Sections {
		g := gotCard.Sections[i]
		if g.Name != s.Name {
			t.Errorf("%d번째 절 이름이 %q 다(원본 %q) — 순서나 이름이 바뀌면 인용이 끊긴다", i, g.Name, s.Name)
		}
		if strings.TrimSpace(g.Body) != strings.TrimSpace(s.Body) {
			t.Errorf("절 %q 의 본문이 달라졌다", s.Name)
		}
	}
	if gotCard.Desc != origCard.Desc || gotCard.State != origCard.State ||
		gotCard.Worktree != origCard.Worktree || !gotCard.Updated.Equal(origCard.Updated) {
		t.Errorf("머리 필드가 달라졌다: %+v", gotCard)
	}
}

// ── 안 돌아오는 것 — 목록대로만 잃는지 단정한다 ──────────────────────────────

func TestRoundTripLosesExactlyTheDocumentedFields(t *testing.T) {
	outDir, res, _ := exportFixture(t)
	orig := string(readFixture(t, "code/.claude/sessions/7.md"))
	got, ok := readOut(t, outDir, ".claude/sessions/7.md")
	if !ok {
		t.Fatal("7.md 가 되쓰이지 않았다")
	}

	origCard, _ := ParseSessionCard("7.md", []byte(orig))
	// 전제: 원본에는 이 셋이 실제로 채워져 있어야 한다. 비어 있으면 "잃었다"를 못 본다.
	if origCard.Branch == "" || origCard.Head == "" || origCard.PID == "" {
		t.Fatalf("전제가 깨졌다 — 원본의 branch/head/pid 가 이미 비었다(%q/%q/%q)",
			origCard.Branch, origCard.Head, origCard.PID)
	}
	gotCard, _ := ParseSessionCard("7.md", []byte(got))
	for _, f := range []struct{ name, v string }{
		{"branch", gotCard.Branch}, {"head", gotCard.Head}, {"pid", gotCard.PID},
	} {
		if f.v != "" {
			t.Errorf("%s 가 %q 로 채워져 나갔다 — 파생이거나 칸이 없는 값을 지어내면 옛 도구가 그것을 사실로 읽는다",
				f.name, f.v)
		}
	}

	// 잃는 목록이 실제 산출물과 같은 축을 말하는가.
	joined := strings.Join(res.Losses, "\n")
	for _, want := range []string{"branch", "head", "pid", "status.html", "gone"} {
		if !strings.Contains(joined, want) {
			t.Errorf("잃는 목록에 %q 축이 없다: %v", want, res.Losses)
		}
	}

	// status.html 은 되쓰지 않는다 — 부분 DATA 블록을 끼우면 페이지가 통째로 깨진다.
	for _, f := range res.Files {
		if strings.Contains(f, "status.html") || strings.Contains(f, "slides/") {
			t.Errorf("대시보드를 되썼다(%s) — 부분 재생성 블록은 인라인 스크립트를 깨뜨린다", f)
		}
	}
}

// 거절된 항목은 DB 에도 없고 되쓴 트리에도 없다. 원본이 그대로 정본이다.
func TestRoundTripDropsRejectedItems(t *testing.T) {
	outDir, _, _ := exportFixture(t)
	for _, rel := range []string{
		".claude/queue/items/dash-real-data-render.md",
		".claude/queue/items/t5-issuer-aud-literal-drift.md",
	} {
		if _, ok := readOut(t, outDir, rel); ok {
			t.Errorf("%s 가 되쓰였다 — 거절한 것을 되쓰면 원본과 다른 값이 옛 트리로 나간다", rel)
		}
	}
}

// 끊긴 `handoff:` 포인터는 되쓰기에서 빈 값으로 나간다 — 그것도 손실이고 목록에 있다.
func TestRoundTripDropsGoneHandoffPointer(t *testing.T) {
	outDir, _, _ := exportFixture(t)
	rel := ".claude/queue/dropped/t1-cover-fallback-removal.md"
	orig := string(readFixture(t, "code/"+rel))
	if !strings.Contains(orig, "handoff: .claude/handoffs/2026-07-31-1620") {
		t.Fatal("전제가 깨졌다 — 원본에 그 handoff 포인터가 없다")
	}
	got, ok := readOut(t, outDir, rel)
	if !ok {
		t.Fatal("폐기 항목이 되쓰이지 않았다")
	}
	if strings.Contains(got, "2026-07-31-1620") {
		t.Error("없는 핸드오프를 가리키는 포인터가 되살아났다")
	}
	if !strings.Contains(got, "handoff: \n") {
		t.Error("handoff 줄 자체가 사라졌다 — 옛 도구가 읽는 형식이 깨진다")
	}
	// 나머지는 그대로여야 한다.
	if !strings.Contains(got, "dropped_reason: after 를 잘못 걸었다") {
		t.Error("폐기 사유가 사라졌다")
	}
}

// ── DB 쪽 단정 — FK 로 옮겨졌는가 ──────────────────────────────────────────

func TestApplyMakesJudgmentLinkFK(t *testing.T) {
	st, _ := applyFixture(t)
	ctx := context.Background()

	hs, err := st.ListJudgmentsByKind(ctx, "cp", model.JudgmentHandoff, 1000)
	if err != nil {
		t.Fatal(err)
	}
	// 핸드오프 2 + 랜딩 서사 5 = 7
	if len(hs) != 7 {
		t.Fatalf("handoff 판단이 %d건이다(핸드오프 2 + 랜딩 서사 5 = 7)", len(hs))
	}
	links := map[string][]string{}
	for _, h := range hs {
		for _, l := range h.Links {
			links[l.TargetKind] = append(links[l.TargetKind], l.TargetID)
		}
	}
	if len(links["item"]) != 2 {
		t.Errorf("항목으로 걸린 FK 가 %d건이다(기대 2): %v", len(links["item"]), links["item"])
	}
	if len(links["commit"]) != 5 {
		t.Errorf("커밋으로 걸린 FK 가 %d건이다(기대 5 — 랜딩 서사): %v", len(links["commit"]), links["commit"])
	}
	// `note` 만 있던 랜딩도 본문을 갖고 들어와야 한다(스키마 CHECK: body <> '').
	for _, h := range hs {
		if strings.TrimSpace(h.Body) == "" {
			t.Errorf("본문이 빈 판단이 들어갔다(title=%q)", clip(h.Title, 60))
		}
	}

	// 진척은 근거를 달고 들어간다.
	sn, err := st.GetSnapshot(ctx, "cp", "part:계약 — 스키마 · API 스펙")
	if err != nil {
		t.Fatalf("진척 스냅숏이 없다: %v", err)
	}
	if sn.Method != model.SnapshotManual || !strings.Contains(sn.Evidence, "5e83926") {
		t.Errorf("스냅숏 근거가 전수 판정 좌표를 안 담았다: method=%q evidence=%q", sn.Method, clip(sn.Evidence, 120))
	}

	// 세션 절은 판단이 된다. 절 이름이 title 이다.
	ss, err := st.ListSessions(ctx, "cp")
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 3 {
		t.Fatalf("세션이 %d건이다(픽스처 카드 3장)", len(ss))
	}
	var titles []string
	for _, s := range ss {
		js, err := st.ListJudgmentsBySession(ctx, s.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, j := range js {
			titles = append(titles, j.Title)
		}
	}
	joined := strings.Join(titles, "|")
	for _, want := range []string{"실측 기록", "범위에서 뺀 것", "지금 하는 것", "다른 트랙에 요청"} {
		if !strings.Contains(joined, want) {
			t.Errorf("절 %q 가 판단으로 안 들어갔다: %v", want, titles)
		}
	}
}

// 판단 표는 추가 전용이다. 이관이 그 규율을 우회하지 않는지 실물로 본다.
func TestApplyRespectsAppendOnlyJudgment(t *testing.T) {
	st, _ := applyFixture(t)
	_, err := st.DB().Exec(`UPDATE judgment SET body = 'x'`)
	if err == nil {
		t.Fatal("판단을 UPDATE 할 수 있었다 — 남의 절이 덮여 원문이 영구 소실된 사고가 물리적으로 가능해진다")
	}
	if !strings.Contains(err.Error(), "추가 전용") {
		t.Errorf("거절 사유가 규율을 말하지 않는다: %v", err)
	}
}
