package legacy

import (
	"bytes"
	"strings"
	"testing"
)

func scanFixture(t *testing.T) Scan {
	t.Helper()
	sc, err := ScanSource(Source{CodeRoot: fxCode, DocsRoot: fxDocs})
	if err != nil {
		t.Fatalf("원본을 훑지 못했다: %v", err)
	}
	return sc
}

func planFixture(t *testing.T) ImportPlan {
	t.Helper()
	return PlanImport(scanFixture(t), PlanOptions{Project: "cp"})
}

// 대조표는 **발견 · 넣음 · 거절**이 나란히 있어야 한다.
// "넣음 N건"만 내면 그것이 몇 건 중 N건인지 알 수 없고, 그 구분이 이 표의 존재 이유다.
func TestPlanImportCountsAreReconcilable(t *testing.T) {
	sc := scanFixture(t)
	p := PlanImport(sc, PlanOptions{Project: "cp"})

	if got := sc.Found["queue"].Files; got != 6 {
		t.Fatalf("전제가 깨졌다 — 픽스처의 큐 파일이 %d개다(기대 6)", got)
	}
	for _, c := range p.Counts {
		if c.Found < c.Accept+c.Reject {
			t.Errorf("%s: 발견(%d) < 넣음(%d)+거절(%d) — 대조표가 안 맞는다",
				c.Source, c.Found, c.Accept, c.Reject)
		}
		if c.Found > 0 && c.Bytes == 0 {
			t.Errorf("%s: 파일은 %d개인데 바이트가 0이다", c.Source, c.Found)
		}
	}
	// 쉼표 paths 2건이 거절되므로 큐는 6 발견 · 4 넣음 · 2 거절이다.
	var queue CountRow
	for _, c := range p.Counts {
		if c.Source == "큐 항목" {
			queue = c
		}
	}
	if queue.Accept != 4 || queue.Reject != 2 {
		t.Errorf("큐 대조표가 %+v — 기대는 넣음 4 · 거절 2(쉼표 paths 2건)", queue)
	}
}

// 쉼표 paths 항목이 **목록에 나타나는가** — 소비자 좌표계(dry-run stdout)로 단정한다.
func TestDryRunListsCommaPathsRejects(t *testing.T) {
	p := planFixture(t)
	var buf bytes.Buffer
	RenderPlan(&buf, p, false)
	out := buf.String()

	for _, want := range []string{
		"queue/items/dash-real-data-render.md",
		"queue/items/t5-issuer-aud-literal-drift.md",
		"paths_comma",
		"쪼개 주지 않는다",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run 출력에 %q 가 없다 — 조용히 버리는 것이 하나도 없어야 한다", want)
		}
	}
	if !strings.Contains(out, "DB 를 한 바이트도 건드리지 않았다") {
		t.Error("예행이라는 사실이 출력에 없다")
	}
	if !strings.Contains(out, "--apply") {
		t.Error("실제로 넣는 방법이 출력에 없다")
	}
}

// 끊긴 포인터는 **전량** 나열돼야 한다. 하나라도 접으면 그것이 곧 조용한 소실이다.
func TestPlanImportListsGonePointers(t *testing.T) {
	p := planFixture(t)

	kinds := map[string]int{}
	byTarget := map[string]Gone{}
	for _, g := range p.Gone {
		kinds[g.Kind]++
		byTarget[g.Target] = g
	}

	// 전제: 픽스처는 원본의 부분집합이라 이 셋이 실제로 끊겨 있어야 한다.
	want := []struct{ kind, target string }{
		// dropped 항목이 가리키는 핸드오프를 픽스처에 안 넣었다(= 다른 머신에서 온 항목과 같은 모양)
		{"item.handoff", ".claude/handoffs/2026-07-31-1620-track2-cover-gate-replacement-landed.md"},
		// 그 항목의 after 대상도 픽스처에 없다
		{"item.after", "t2-quarantine-stale-rows"},
		// 대시보드 막힘이 가리키는 큐 항목도 없다
		{"blocker.qid", "image-axis-ruling"},
	}
	for _, w := range want {
		g, ok := byTarget[w.target]
		if !ok {
			t.Errorf("끊긴 포인터 %s(%s)가 목록에 없다 — 침묵하면 FK 로 못 옮긴 사실이 사라진다", w.target, w.kind)
			continue
		}
		if g.Kind != w.kind {
			t.Errorf("%s 의 kind 가 %q 다(기대 %q)", w.target, g.Kind, w.kind)
		}
		if strings.TrimSpace(g.Detail) == "" {
			t.Errorf("%s 에 사유가 없다 — 사유 없는 목록은 두 번째 세션부터 무시된다", w.target)
		}
	}
	if kinds["blocker.qid"] < 5 {
		t.Errorf("blocker.qid 끊김이 %d건뿐이다 — 픽스처의 막힘 3건이 가리키는 큐 항목은 하나도 없다", kinds["blocker.qid"])
	}

	var buf bytes.Buffer
	RenderPlan(&buf, p, false)
	out := buf.String()
	for _, w := range want {
		if !strings.Contains(out, w.target) {
			t.Errorf("dry-run 출력에 끊긴 포인터 %q 가 없다", w.target)
		}
	}
	if !strings.Contains(out, "끊긴 포인터") {
		t.Error("dry-run 에 끊긴 포인터 절이 없다")
	}
}

// 연결되는 포인터는 실제로 FK 로 간다.
func TestPlanImportLinksResolvableHandoff(t *testing.T) {
	p := planFixture(t)
	linked := 0
	for _, it := range p.Items {
		if it.HandoffRel != "" {
			linked++
		}
	}
	if linked != 2 {
		t.Errorf("핸드오프가 걸린 항목이 %d건이다 — contracts-batch8 · batch7-design-doc 둘이어야 한다", linked)
	}
}

func TestPlanImportListsUnclassifiedSections(t *testing.T) {
	p := planFixture(t)
	got := map[string]bool{}
	for _, s := range p.Unclassified {
		got[s.File+"|"+s.Name] = true
	}
	for _, want := range []string{"7.md|실측 기록", "7.md|범위에서 뺀 것"} {
		if !got[want] {
			t.Errorf("비규약 절 %q 가 목록에 없다 — 보존은 하되 분류 못 했다는 사실이 보여야 한다", want)
		}
	}
	var buf bytes.Buffer
	RenderPlan(&buf, p, false)
	if !strings.Contains(buf.String(), "## 실측 기록") {
		t.Error("dry-run 출력에 비규약 절 이름이 없다")
	}
}

// landed_sha 는 landed_ref 로 안 간다. 그 판단이 출력에 있어야 한다.
func TestPlanImportDoesNotCarryLegacyLandedSHA(t *testing.T) {
	p := planFixture(t)
	for _, it := range p.Items {
		if it.Item.LandedRef != "" {
			t.Errorf("%s: landed_ref 에 레거시 값이 들어갔다(%q) — "+
				"그 칸에는 러너가 실제로 ff 한 sha 만 들어간다", it.Item.ID, it.Item.LandedRef)
		}
	}
	var buf bytes.Buffer
	RenderPlan(&buf, p, false)
	if !strings.Contains(buf.String(), "landed_ref 에 넣지 않는다") {
		t.Error("landed_sha 를 안 옮긴다는 사실이 출력에 없다")
	}
}

// track 은 라벨로만 간다 — 어떤 배제 판정에도 안 쓴다(설계 §5).
func TestPlanImportPutsTrackInLabelsOnly(t *testing.T) {
	p := planFixture(t)
	var found bool
	for _, it := range p.Items {
		if it.Item.ID != "contracts-batch8" {
			continue
		}
		found = true
		if LabelValue(it.Item.Labels, "track") != "contracts" {
			t.Errorf("track 이 라벨에 없다: %v", it.Item.Labels)
		}
		for _, pth := range it.Item.Paths {
			if strings.Contains(pth, "contracts") && pth != "contracts/" {
				t.Errorf("track 이 경로 축으로 샜다: %v", it.Item.Paths)
			}
		}
	}
	if !found {
		t.Fatal("전제가 깨졌다 — contracts-batch8 이 계획에 없다")
	}
}

// parts 는 근거 없이 못 들어간다. judged 가 없으면 **통째로** 거절하고 그 사유를 낸다.
func TestPlanImportRefusesPartsWithoutEvidence(t *testing.T) {
	sc := scanFixture(t)
	if len(sc.Dash.Parts) == 0 {
		t.Fatal("전제가 깨졌다 — 픽스처에 parts 가 없다")
	}
	sc.Dash.Judged = Judged{} // 근거를 걷어낸다
	p := PlanImport(sc, PlanOptions{Project: "cp"})
	if len(p.Parts) != 0 {
		t.Fatalf("근거가 없는데 진척 %d건이 들어갔다 — 근거 없는 숫자가 되는 순간 이 표를 아무도 못 믿는다", len(p.Parts))
	}
	var buf bytes.Buffer
	RenderPlan(&buf, p, false)
	if !strings.Contains(buf.String(), "no_evidence") {
		t.Error("거절 사유가 출력에 없다")
	}
}

// 서사가 빈 랜딩 — 거절하면 제목과 커밋 sha 까지 함께 사라진다.
// 지어내지 않고 제목을 본문 자리에 옮기고, 그 사실을 출력에 낸다.
func TestPlanImportKeepsLandingsWithEmptyNarrative(t *testing.T) {
	sc := scanFixture(t)

	// 전제: 픽스처에 서사가 통째로 빈 랜딩이 실제로 있어야 한다.
	empty := 0
	for _, l := range sc.Dash.Landings {
		if strings.TrimSpace(l.Body) == "" && strings.TrimSpace(l.Note) == "" {
			empty++
		}
	}
	if empty == 0 {
		t.Fatal("전제가 깨졌다 — 픽스처에 서사가 빈 문자열인 랜딩이 없다. " +
			"그 모양이 없으면 이 시험은 아무것도 못 본다(실물에는 5건 있다)")
	}

	p := PlanImport(sc, PlanOptions{Project: "cp"})
	if len(p.Landings) != len(sc.Dash.Landings) {
		t.Fatalf("랜딩 %d건이 계획에 들어갔다(원본 %d) — 서사가 비었다고 커밋 sha 까지 버리면 안 된다",
			len(p.Landings), len(sc.Dash.Landings))
	}
	for _, l := range p.Landings {
		if strings.TrimSpace(l.Body) == "" && strings.TrimSpace(l.Note) == "" {
			t.Errorf("본문이 빈 랜딩이 남았다(%q) — 판단 표가 받지 않는다", clip(l.Title, 60))
		}
	}
	var buf bytes.Buffer
	RenderPlan(&buf, p, false)
	if !strings.Contains(buf.String(), "empty_narrative") {
		t.Error("제목을 본문 자리에 옮겼다는 사실이 출력에 없다")
	}
}
