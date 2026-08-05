package legacy

import (
	"bytes"
	"strings"
	"testing"
)

// firstPlannedItemID 는 계획에 실제로 들어가는 큐 항목 하나를 고른다.
//
// 픽스처에는 Fatal 거절이 붙어 통째로 빠지는 파일도 있다. 그런 항목을 골라
// 경로를 갈아 끼우면 이 시험은 관문이 아니라 Fatal 규율을 보게 된다.
func firstPlannedItemID(t *testing.T) string {
	t.Helper()
	p := PlanImport(scanFixture(t), PlanOptions{Project: "cp"})
	if len(p.Items) == 0 {
		t.Fatal("전제가 깨졌다 — 픽스처에서 계획에 들어가는 큐 항목이 0건이다")
	}
	return p.Items[0].Item.ID
}

// 이관은 item.paths 로 가는 **세 번째 문**이다(add · finish followup 이 앞의 둘).
//
// 앞의 둘과 달리 거절하지 않고 **그 경로만 버리고 남긴다.** 고칠 수 있는 사람이
// 그 자리에 없기 때문이다 — 그 판단이 plan.go 주석에 있고, 이 시험이 그것을 못박는다.
func TestPlanImportDropsBadCoordinatePathsButKeepsTheItem(t *testing.T) {
	id := firstPlannedItemID(t)

	sc := scanFixture(t)
	idx := -1
	for i := range sc.Items {
		if sc.Items[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatalf("전제가 깨졌다 — 계획에 있던 %q 를 원본에서 못 찾았다", id)
	}
	sc.Items[idx].Paths = []string{
		"internal/api/x.go", // 통과
		`internal\api\y.go`, // 백슬래시
		"C:/repo/z.go",      // Windows 드라이브 절대경로
	}

	p := PlanImport(sc, PlanOptions{Project: "cp"})

	// ── ① 항목은 살아 있어야 한다. 좌표계가 틀린 경로는 그 항목의 **겹침 축만** 죽인다.
	var got *PlannedItem
	for i := range p.Items {
		if p.Items[i].Item.ID == id {
			got = &p.Items[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("경로 하나가 틀렸다고 항목 %q 가 통째로 빠졌다 — "+
			"제목·본문·상태·선행은 멀쩡한데 이관이 그것까지 잃는다", id)
	}

	// ── ② 통과한 경로만 남는다.
	if len(got.Item.Paths) != 1 || got.Item.Paths[0] != "internal/api/x.go" {
		t.Errorf("남은 경로가 %v 다 — 통과분 하나(internal/api/x.go)만 남아야 한다.\n"+
			"틀린 좌표를 그냥 넣으면 그 항목의 겹침 축이 **조용히** 죽는다("+
			"오류가 아니라 '겹침 없음'이라 정상과 구분되지 않는다)", got.Item.Paths)
	}

	// ── ③ 버린 것은 사유와 함께 남는다. 조용히 지우면 이관이 무엇을 잃었는지 아무도 모른다.
	var coord []Reject
	for _, r := range p.Rejects {
		if r.Code == "bad_path_coordinate" {
			coord = append(coord, r)
		}
	}
	if len(coord) != 2 {
		t.Fatalf("bad_path_coordinate 거절이 %d건이다 — 버린 경로가 둘이므로 2건이어야 한다: %+v",
			len(coord), p.Rejects)
	}
	for _, r := range coord {
		// ── ④ Fatal 이 아니어야 한다. Fatal 이면 규율 ①이 그 파일을 통째로 빼고,
		//    그러면 카드 하나가 이관 전체를 멈춘다.
		if r.Fatal {
			t.Errorf("경로 좌표계 거절에 Fatal 이 붙었다(%q) — "+
				"그러면 그 카드가 통째로 안 들어가고, 고칠 사람은 이미 그 자리에 없다", r.Detail)
		}
		if r.Field != "paths" {
			t.Errorf("거절의 field 가 %q 다 — 어느 칸이 걸렸는지 말해야 사람이 고친다", r.Field)
		}
		if strings.TrimSpace(r.Detail) == "" {
			t.Error("거절에 사유가 없다 — 사유 없는 목록은 두 번째 세션부터 무시된다")
		}
	}

	// ── ⑤ 출력에 나와야 한다. 계획에만 있고 화면에 안 나오면 아무도 안 본다.
	var buf bytes.Buffer
	RenderPlan(&buf, p, false)
	if !strings.Contains(buf.String(), "bad_path_coordinate") {
		t.Error("거절 사유가 출력에 없다 — dry-run 이 무엇을 버렸는지 말하지 않는다")
	}
}

// 경로가 **전부** 틀려도 항목은 들어간다.
//
// 이 갈래를 따로 세우는 이유: "하나라도 틀리면 통째로"가 규율 ①의 모양이라,
// 다음 사람이 그것을 이 축에도 적용하기 쉽다. 여기서는 다르다는 것을 못박는다.
func TestPlanImportKeepsItemWhenEveryPathIsBad(t *testing.T) {
	id := firstPlannedItemID(t)

	sc := scanFixture(t)
	for i := range sc.Items {
		if sc.Items[i].ID == id {
			sc.Items[i].Paths = []string{`a\b.go`, `C:\repo\c.go`}
			break
		}
	}

	p := PlanImport(sc, PlanOptions{Project: "cp"})
	for _, pi := range p.Items {
		if pi.Item.ID != id {
			continue
		}
		if len(pi.Item.Paths) != 0 {
			t.Errorf("남은 경로가 %v 다 — 전부 틀렸으므로 0건이어야 한다", pi.Item.Paths)
		}
		return
	}
	t.Fatalf("경로가 전부 틀렸다고 항목 %q 가 빠졌다 — 겹침 축만 죽을 뿐 항목은 유효하다", id)
}

// 이관은 포함 축을 **판정할 수 없다** — 그래서 판정하는 대신 말한다.
//
// 좌표계 축과 갈리는 자리다. 좌표계는 문자열 형태만 보므로 이관에서도 판정된다(위 두
// 시험). 포함 축("이 경로가 어느 트리 안인가")은 기준 트리를 알아야 하는데
// **PlanOptions 에 그것이 없다** — 레거시 카드의 경로는 다른 머신·다른 디렉토리에서 온
// 것일 수 있고 이 순수 함수는 그것을 알 방법이 없다.
//
// 그래서 버리지 않는다. 버리면 이관이 경로를 통째로 잃고, 고칠 수 있는 사람은 이미 그
// 자리에 없다(add·finish 와 규율이 갈리는 기준 그대로). 대신 Notes 로 말한다 —
// 관문이 어느 표면에 없는지가 코드 어디에도 안 적혀 있으면 다음 사람이 네 표면을 다시
// 전수해야 그 표를 만든다.
func TestPlanImportSaysItCannotJudgeContainmentInsteadOfDroppingPaths(t *testing.T) {
	id := firstPlannedItemID(t)

	sc := scanFixture(t)
	for i := range sc.Items {
		if sc.Items[i].ID == id {
			sc.Items[i].Paths = []string{
				"internal/api/x.go", // 상대경로 — 이 축과 무관하다
				// 흠 없는 POSIX 절대경로. 좌표계 관문을 **통과한다** — 그래서 포함 축이
				// 따로 필요했고, 여기서는 그 축을 판정할 기준이 없다.
				"/home/other/repo/internal/z.go",
			}
			break
		}
	}

	p := PlanImport(sc, PlanOptions{Project: "cp"})

	var got *PlannedItem
	for i := range p.Items {
		if p.Items[i].Item.ID == id {
			got = &p.Items[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("항목 %q 가 통째로 빠졌다", id)
	}

	// ── ① 절대경로도 **살아남는다.** 판정할 수 없는 것을 버리면 못 읽음이 값이 된다.
	if len(got.Item.Paths) != 2 {
		t.Fatalf("남은 경로가 %v 다 — 둘 다 남아야 한다.\n"+
			"포함 축을 판정할 기준이 없는데 버리면 이관이 경로를 통째로 잃는다 — "+
			"그 경로가 정말 밖인지 안인지 이 함수는 모른다", got.Item.Paths)
	}

	// ── ② 그러나 침묵하지 않는다. 못 본 축이 있다는 사실이 Notes 에 남는다.
	var note string
	for _, n := range p.Notes {
		if strings.Contains(n, "포함") {
			note = n
			break
		}
	}
	if note == "" {
		t.Fatalf("포함 축을 못 봤다는 말이 Notes 에 없다: %v.\n"+
			"침묵하면 다음 사람은 '관문이 있다'만 배우고 이 표면에 없다는 것을 못 본다 — "+
			"그것이 '반쪽 발화는 균일한 부재보다 나쁘다'의 모양이다", p.Notes)
	}
	// ── ③ 어느 카드인지 말해야 사람이 고친다.
	if !strings.Contains(note, id) {
		t.Errorf("Note 에 항목 id 가 없다: %q", note)
	}
	// ── ④ 출력에 나와야 한다. 계획에만 있고 화면에 안 나오면 아무도 안 본다.
	var buf bytes.Buffer
	RenderPlan(&buf, p, false)
	if !strings.Contains(buf.String(), "포함") {
		t.Error("포함 축 Note 가 dry-run 출력에 없다")
	}
}
