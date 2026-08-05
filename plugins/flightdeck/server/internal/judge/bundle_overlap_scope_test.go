package judge

import "testing"

// TestEligibleBundleOverlapsCoverEveryMemberPath 는 **추천 묶음의 겹침이 묶음 전체
// 경로의 합집합**이라는 사실을 못박는다.
//
// ★ 왜 이 시험이 따로 서 있나. 이 사실은 judge 안에 있는데(bundle.go 의
// `best.Lead.Overlaps = OverlapsWithLive(bundlePaths(best), …)`) 그 값을 **문장으로
// 번역하는 곳은 mcpsrv/render.go** 다. 두 자리가 갈리면 응답이 자기가 본 범위를
// 틀리게 말하는데, 그 불일치는 어느 한쪽의 시험으로도 안 잡힌다 — 실제로 한 번
// 갈렸다(리뷰 라운드 2 뒤: 렌더가 추천 모드에서 "선두 경로만 봤다"고 말했다).
//
// 그래서 렌더가 기대는 전제를 여기서 **직접** 고정한다. 이 시험이 빨개지면
// render.go 의 겹침 범위 문장도 함께 고쳐야 한다는 뜻이다.
//
// 대조를 세게 잡는다: 남의 세션이 **구성원의 경로만** 만진다. 선두 경로와는
// 아예 안 겹치므로, 합집합을 안 보면 결과가 반드시 빈다.
func TestEligibleBundleOverlapsCoverEveryMemberPath(t *testing.T) {
	lead := cand("q-lead", 0, []string{"lead-only.go"})
	mem := cand("q-next", 1, []string{"member-only.go"})

	in := EligibleInput{
		Self:       "S1",
		Candidates: []Candidate{lead, mem},
		// 남의 세션은 **구성원 경로만** 만진다.
		Live: []LiveSession{{ID: "S2", Label: "남", Paths: []string{"member-only.go"}}},
	}
	// 형제 축으로 둘을 묶는다 — 그래야 구성원이 실제로 묶음에 든다.
	b, rej := EligibleBundle(in, SiblingIndex{"q-lead": {"J1"}, "q-next": {"J1"}})
	if b == nil {
		t.Fatalf("적격이 있는데 묶음이 nil 이다(탈락 %v)", rej)
	}

	// ── 전제 확인: 결과를 읽기 전에 묶음이 정말 만들어졌는가 ──
	if b.Lead.Item.ID != "q-lead" || len(b.Members) != 1 || b.Members[0].Item.ID != "q-next" {
		t.Fatalf("전제가 깨졌다 — 선두 %q 구성원 %v(선두 q-lead + 구성원 q-next 를 기대)",
			b.Lead.Item.ID, memberIDs(b))
	}
	// ── 전제 확인: 선두 경로 단독으로는 정말 안 겹치는가 ──
	// 이게 성립 안 하면 아래 단정이 합집합을 증명하지 못한다(선두만 봐도 통과한다).
	if solo := OverlapsWithLive(b.Lead.Item.Paths, in.Live, in.Self, ""); len(solo) != 0 {
		t.Fatalf("전제가 깨졌다 — 선두 경로만으로 이미 겹친다(%+v). "+
			"이 상태로는 합집합을 봤는지 안 봤는지 못 가른다", solo)
	}

	if len(b.Lead.Overlaps) == 0 {
		t.Fatal("구성원 경로를 만지는 세션이 있는데 추천 묶음의 겹침이 0건이다 — " +
			"겹침이 묶음 전체 합집합이 아니라 선두만 보고 있다. " +
			"mcpsrv 의 '겹침 판정 범위' 문장이 추천 모드에서 거짓이 된다")
	}
	if b.Lead.Overlaps[0].SessionID != "S2" {
		t.Fatalf("겹친 세션이 %q 다 — S2 를 기대했다", b.Lead.Overlaps[0].SessionID)
	}
}
