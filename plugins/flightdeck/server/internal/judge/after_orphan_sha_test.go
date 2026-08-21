package judge

import (
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 재생된 커밋을 "기다리면 풀린다"로 내면 항목이 영구히 굶는다 (2026-08-21).
//
// ★ 실측이 이 축의 근거다. 이 저장소의 랜딩은 커밋을 **재생한다** — 브랜치가 머지되면서
// 새 sha 가 서고 원래 sha 는 어느 브랜치에도 안 남는다. 그러면 `merge-base --is-ancestor`
// 는 영원히 rc=1 이다. 내용은 들어갔는데 판정은 영원히 거짓이다.
//
// 원장·git 으로 재현했다(2026-08-21). after-unmet-sha 로 막힌 열린 항목 셋이 전부 그 경로였고,
// 세 dep_sha 의 내용이 **다른 sha 로 main 에 들어가 있었다**:
//
//	fd-lane-turn-remeasure-on-2026-08-26-or-50-landings        6ef65db → a419130 (0.19.0 릴리스)
//	fd-leave-bundle-n2-observation-on-2026-08-19-or-10-leaves  e776805 → 7a18807
//	fd-leave-judgment-body-is-mostly-boilerplate               a6c68b4 → 6903562
//
// 세 커밋 다 `git cat-file -e` 는 통과하고 `git branch -a --contains` 는 **비어 있다.**
// 그리고 셋 다 **티클러**다 — 굶김 축에서 이미 빠져 있는데 적격에서도 빠지니, 발화 조건이
// 차도(하나는 fires:2026-08-19 가 지났다) 추천에 영영 안 오른다. 이중 차단이다.
func TestOrphanSHAIsNotWaitable(t *testing.T) {
	after := []model.After{{SHA: "6ef65db"}}
	facts := AfterFacts{SHAAncestry: map[string]AncestryResult{"6ef65db": AncestryOrphan}}

	ok, reasons := AfterSatisfied(after, facts)
	if ok || len(reasons) != 1 {
		t.Fatalf("고아 sha 가 충족으로 통과했다(ok=%v reasons=%v)", ok, reasons)
	}
	r := reasons[0]
	if !strings.HasPrefix(r, AfterOrphanSHA+":") {
		t.Fatalf("사유 코드가 %q 가 아니다 — '아직'과 같은 코드로 내면 영구히 굶는다:\n%s", AfterOrphanSHA, r)
	}
	if !strings.Contains(r, "기다려도 안 풀린다") {
		t.Fatalf("끝이 없다는 사실을 안 말한다 — after-dropped-dep 와 같은 부류다:\n%s", r)
	}
	// 처방에 **실재하는 동사**를 단다. 없는 통로를 가리키는 문구는 이 레포가 결함으로 분류한다.
	if !strings.Contains(r, "fd after cut") {
		t.Fatalf("집행 동사가 없다 — 처방만 있고 수단이 없으면 항목은 계속 굶는다:\n%s", r)
	}
}

// 아직 안 랜딩된 브랜치 위의 커밋은 **여전히 기다리면 풀린다** — 축을 안 덮는다.
func TestUnmetSHAStillMeansWaitable(t *testing.T) {
	_, reasons := AfterSatisfied([]model.After{{SHA: "deadbee"}},
		AfterFacts{SHAAncestry: map[string]AncestryResult{"deadbee": AncestryNo}})
	if len(reasons) != 1 || !strings.HasPrefix(reasons[0], AfterUnmetSHA+":") {
		t.Fatalf("아직 조상이 아닌 sha 의 사유가 바뀌었다 — 그쪽은 기다리면 풀린다: %v", reasons)
	}
}
