package judge

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 이 파일은 **적격 판정**을 잠근다 — rejectionsFor(탈락 축과 그 순서) · lessCandidate
// (추천 순서) · OverlapsWithLive(겹침은 거르지 않고 알린다). 셋 다 eligible.go 의 것이다.
//
// ★ 부르는 것은 EligibleBundle 이다. 제품이 실제로 부르는 진입점이 그것 하나뿐이라
// 그렇다(eligible_axis_test.go 가 같은 이유로 먼저 그렇게 옮겼다). 여기 시험이
// 제품이 안 부르는 진입점을 통과하면, 같은 규칙이 제품 경로에서 깨져도 전부 초록이 된다.
//
// 단독 후보(SiblingIndex{} · Now zero)에서는 묶음 축이 전부 무동작이라 이 파일이 보는
// 것은 위 셋뿐이다: 묶음 크기가 전부 1이라 lessBundle 의 ②가 동점이 되고, 그러면
// 남는 축(의존자 합 → 최고령 → 선두 id)이 lessCandidate 의 세 축과 정확히 같다.
// 겹침도 bundlePaths 가 구성원 없이 선두 경로만 낸다.
// 묶음 축 자체는 bundle_test.go 가 맡는다 — 두 파일이 같은 것을 두 번 잠그지 않는다.
//
// 시험 이름의 "Eligible" 은 **판정의 이름**이지 함수 이름이 아니다.
// judge.Eligible(단일 추천 껍데기)은 호출부 0으로 제거됐다 —
// 큐 항목 fd-eligible-dead-function-disposal 이 그 처분을 정했다.

var t0 = time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)

// openItem 은 시험용 열린 항목이다. 나이는 t0 + minutes 로 준다(작을수록 오래된 것).
func openItem(id string, minutes int, paths ...string) model.Item {
	return model.Item{
		ID:        id,
		Title:     id,
		State:     model.ItemOpen,
		Paths:     paths,
		CreatedAt: t0.Add(time.Duration(minutes) * time.Minute),
	}
}

// solo 는 이웃이 하나도 안 서는 색인이다. 이 파일의 시험은 전부 단독 후보를 본다.
func solo() SiblingIndex { return SiblingIndex{} }

// itemIDs 는 rejected 에 나타난 항목 id 집합이다.
// "조용히 버리는 것이 없다"를 소비자 좌표계(어느 항목이 원장에 남았나)로 단정하기 위한 것이다.
func itemIDs(rs []model.Rejection) map[string]bool {
	out := map[string]bool{}
	for _, r := range rs {
		out[r.Item] = true
	}
	return out
}

func codesFor(rs []model.Rejection, item string) []string {
	var out []string
	for _, r := range rs {
		if r.Item == item {
			out = append(out, r.Reason)
		}
	}
	return out
}

func TestEligiblePicksTopAndLedgersEveryoneElse(t *testing.T) {
	in := EligibleInput{
		Self: "S1",
		Candidates: []Candidate{
			{Item: openItem("a-claimed", 0), ClaimedBy: "S2"},
			{Item: openItem("b-fit", 10), Dependents: 3},
			{Item: openItem("c-fit-lonely", 5), Dependents: 0},
			{Item: model.Item{ID: "d-done", State: model.ItemDone, CreatedAt: t0}},
		},
	}
	best, rejected := EligibleBundle(in, solo())
	if best == nil {
		t.Fatalf("적격이 있는데 추천이 없다 (탈락 %v)", rejected)
	}
	// 의존자 수가 첫 축이다 — 더 오래된 c 가 아니라 b 가 나와야 한다.
	if best.Lead.Item.ID != "b-fit" {
		t.Errorf("추천 = %s, 기대 = b-fit", best.Lead.Item.ID)
	}

	// 불변식: 모든 후보는 추천이거나 원장에 있다.
	got := itemIDs(rejected)
	for _, c := range in.Candidates {
		if c.Item.ID == best.Lead.Item.ID {
			continue
		}
		if !got[c.Item.ID] {
			t.Errorf("후보 %s 가 조용히 사라졌다 — 추천도 아니고 사유도 없다", c.Item.ID)
		}
	}

	if codes := codesFor(rejected, "a-claimed"); !reflect.DeepEqual(codes, []string{RejectClaimed}) {
		t.Errorf("a-claimed 사유 = %v, 기대 = [%s]", codes, RejectClaimed)
	}
	if codes := codesFor(rejected, "d-done"); !reflect.DeepEqual(codes, []string{RejectClosed}) {
		t.Errorf("d-done 사유 = %v, 기대 = [%s]", codes, RejectClosed)
	}
	if codes := codesFor(rejected, "c-fit-lonely"); !reflect.DeepEqual(codes, []string{RejectNotTop}) {
		t.Errorf("c-fit-lonely 사유 = %v, 기대 = [%s]", codes, RejectNotTop)
	}
}

func TestEligibleWithNoneFitLedgersAll(t *testing.T) {
	dropped := openItem("x-after-dropped", 0)
	dropped.After = []model.After{{Item: "dep-dropped"}}
	badref := openItem("y-after-badref", 1)
	badref.After = []model.After{{SHA: "ccccccc"}}

	in := EligibleInput{
		Self: "S1",
		Candidates: []Candidate{
			{Item: dropped},
			{Item: badref},
			{Item: openItem("z-needs-staging", 2), Needs: []string{"staging"}},
			{Item: openItem("w-claimed", 3), ClaimedBy: "S9"},
			{Item: model.Item{ID: "v-dropped-item", State: model.ItemDropped, CreatedAt: t0, CloseReason: "중복"}},
		},
		Facts: AfterFacts{
			ItemStates:  map[string]model.ItemState{"dep-dropped": model.ItemDropped},
			SHAAncestry: map[string]AncestryResult{"ccccccc": AncestryBadRef},
		},
		HeldResources: map[string]string{"staging": "S7"},
	}

	best, rejected := EligibleBundle(in, solo())
	if best != nil {
		t.Fatalf("적격이 없어야 하는데 %s 를 추천했다", best.Lead.Item.ID)
	}
	got := itemIDs(rejected)
	if len(got) != len(in.Candidates) {
		t.Errorf("원장에 %d건, 후보는 %d건 — 조용히 버려진 것이 있다: %v", len(got), len(in.Candidates), rejected)
	}

	want := map[string][]string{
		"x-after-dropped": {AfterDroppedDep},
		"y-after-badref":  {AfterBadRef},
		"z-needs-staging": {RejectResourceHeld},
		"w-claimed":       {RejectClaimed},
		"v-dropped-item":  {RejectClosed},
	}
	for item, wantCodes := range want {
		if codes := codesFor(rejected, item); !reflect.DeepEqual(codes, wantCodes) {
			t.Errorf("%s 사유 = %v, 기대 = %v", item, codes, wantCodes)
		}
	}

	// 선행 사유가 상위 코드로 접히지 않았는지 — "폐기됐다"와 "그런 ref 가 없다"는
	// 둘 다 영영 안 풀리지만 고치는 자리가 다르다.
	for _, r := range rejected {
		if strings.HasPrefix(r.Reason, "after-") && r.Detail == "" {
			t.Errorf("선행 사유에 상세가 없다: %+v", r)
		}
	}
}

// 경로 겹침은 **거르는 축이 아니다.** 겹쳐도 추천되고, 겹쳤다는 사실이 함께 온다.
func TestOverlapDoesNotFilterButIsReported(t *testing.T) {
	item := openItem("t2-pipeline", 0, "pipeline/")
	live := []LiveSession{{ID: "S2", Label: "트랙 2", Paths: []string{"pipeline/ingest.py", "docs/"}}}

	// 대조 조건: 이 입력이 정말 겹치는가. 안 겹치면 이 시험은 아무것도 지키지 않는다.
	if !PathsOverlap(item.Paths, live[0].Paths) {
		t.Fatalf("대조가 성립하지 않는다 — 이 입력은 겹쳐야 한다: %v vs %v", item.Paths, live[0].Paths)
	}

	best, rejected := EligibleBundle(EligibleInput{
		Self:       "S1",
		Candidates: []Candidate{{Item: item}},
		Live:       live,
	}, solo())
	if best == nil {
		t.Fatalf("겹침이 탈락으로 샜다: %v", rejected)
	}
	if len(rejected) != 0 {
		t.Errorf("겹침이 사유로 새어 나왔다: %v", rejected)
	}
	if len(best.Lead.Overlaps) != 1 {
		t.Fatalf("겹침이 알려지지 않았다 — 침묵하면 '겹침 없음'과 구분되지 않는다: %+v", best.Lead.Overlaps)
	}
	ov := best.Lead.Overlaps[0]
	if ov.SessionID != "S2" || ov.Label != "트랙 2" {
		t.Errorf("상대 세션이 틀렸다: %+v", ov)
	}
	if len(ov.Pairs) != 1 || ov.Pairs[0] != [2]string{"pipeline/", "pipeline/ingest.py"} {
		t.Errorf("겹친 경로 쌍이 틀렸다: %v", ov.Pairs)
	}
}

func TestOverlapWithSelfIsNotReported(t *testing.T) {
	item := openItem("t2-pipeline", 0, "pipeline/")
	best, _ := EligibleBundle(EligibleInput{
		Self:       "S1",
		Candidates: []Candidate{{Item: item}},
		Live: []LiveSession{
			{ID: "S1", Label: "나", Paths: []string{"pipeline/"}}, // 자기 발자국
			{ID: "S3", Label: "무관", Paths: []string{"console/"}},
		},
	}, solo())
	if best == nil {
		t.Fatal("추천이 없다")
	}
	if len(best.Lead.Overlaps) != 0 {
		t.Errorf("자기 자신과의 겹침이 알림으로 나왔다: %+v", best.Lead.Overlaps)
	}
}

// TestOverlapWithSiblingCardIsNotReported 는 **같은 대화의 다른 카드**를 겹침에서 뺀다.
//
// 정체가 3중키(머신·워크트리·cc)라 한 대화가 카드 여러 장이 될 수 있다 —
// /clear·compact·재개로 cc 가 갈리거나, 하위 디렉토리에서 MCP 가 뜨거나. 그때 카드 id 는
// 다르지만 대화는 같고, id 만 비교하면 화면이 **자기 자신과 조율하라**고 말한다.
// 실측(2026-08-05): 이 머신의 살아 있는 카드 34장이 대화 11개였다.
//
// 처방 축은 이 판정을 이미 갖고 있었다(sameConversation). 이 시험은 그 판정이
// **겹침 축에도** 적용됐는지를 잠근다 — 한쪽만 고치면 같은 화면이 두 말을 한다.
func TestOverlapWithSiblingCardIsNotReported(t *testing.T) {
	const mine = "cc-aaaa"
	best, _ := EligibleBundle(EligibleInput{
		Self: "S1", SelfCC: mine,
		Candidates: []Candidate{{Item: openItem("t2-pipeline", 0, "pipeline/")}},
		Live: []LiveSession{
			// 카드 id 는 다르지만 **같은 대화**다. 겹침이 아니다.
			{ID: "S9", Label: "내 형제 카드", Paths: []string{"pipeline/"}, CCSessionID: mine},
			// 진짜 남 — 이건 반드시 나와야 한다. 안 나오면 이 시험은 겹침 축을
			// 통째로 꺼 놓고 초록을 내는 것이 된다.
			{ID: "S3", Label: "남", Paths: []string{"pipeline/"}, CCSessionID: "cc-zzzz"},
		},
	}, solo())
	if best == nil {
		t.Fatal("추천이 없다")
	}
	if len(best.Lead.Overlaps) != 1 {
		t.Fatalf("겹침이 %d건이다 — 형제는 빠지고 남은 남아야 한다: %+v",
			len(best.Lead.Overlaps), best.Lead.Overlaps)
	}
	if best.Lead.Overlaps[0].SessionID != "S3" {
		t.Fatalf("남은 겹침이 %q 다 — S3(진짜 남)이어야 한다", best.Lead.Overlaps[0].SessionID)
	}
}

// TestEmptyCCIsNotASibling 은 반대편을 못박는다.
//
// **못 읽은 cc 둘을 같은 대화로 접으면 안 된다.** 접으면 관측이 깨진 순간
// 겹침 축이 조용히 전부 꺼지고, 꺼졌다는 사실조차 화면에 안 나온다 —
// 이 저장소가 반복해서 겪은 실패 모양이다. 위 시험만 있으면
// `a == b` 로 '정리'하는 변경이 초록으로 통과한다.
func TestEmptyCCIsNotASibling(t *testing.T) {
	best, _ := EligibleBundle(EligibleInput{
		Self: "S1", SelfCC: "", // 내 cc 를 못 읽었다
		Candidates: []Candidate{{Item: openItem("t2-pipeline", 0, "pipeline/")}},
		Live: []LiveSession{
			{ID: "S3", Label: "남", Paths: []string{"pipeline/"}, CCSessionID: ""}, // 저쪽도 못 읽었다
		},
	}, solo())
	if best == nil {
		t.Fatal("추천이 없다")
	}
	if len(best.Lead.Overlaps) != 1 {
		t.Fatalf("빈 cc 둘을 형제로 접었다 — 관측 실패가 겹침 축을 끈다: %+v", best.Lead.Overlaps)
	}
}

// 겹침이 없다는 것과 이 축을 안 본다는 것이 구분돼야 한다.
// 살아 있는 세션이 있는데도 겹침이 0건이면 그것은 "안 겹친다"라는 판정이다.
func TestNoOverlapWhenPathsAreUnrelated(t *testing.T) {
	best, _ := EligibleBundle(EligibleInput{
		Self:       "S1",
		Candidates: []Candidate{{Item: openItem("a", 0, "tool/")}},
		Live:       []LiveSession{{ID: "S2", Paths: []string{"tools/"}}}, // 성분이 다르다
	}, solo())
	if best == nil {
		t.Fatal("추천이 없다")
	}
	if len(best.Lead.Overlaps) != 0 {
		t.Errorf("tool/ 과 tools/ 가 겹친 것으로 나왔다: %+v", best.Lead.Overlaps)
	}
}

func TestOverlapsCarryTheirDelta(t *testing.T) {
	live := []LiveSession{{
		ID: "s-1", CCSessionID: "cc-other", Paths: []string{"a.go", "b.bin"},
		Delta: map[string]model.LineDelta{"a.go": {Added: 47, Removed: 1}},
	}}
	got := OverlapsWithLive([]string{"a.go", "b.bin"}, live, "me", "cc-me")
	if len(got) != 1 {
		t.Fatalf("겹침 1건을 기대했는데 %d건이다", len(got))
	}
	if d, want := got[0].TheirDelta["a.go"], (model.LineDelta{Added: 47, Removed: 1}); d != want {
		t.Errorf("a.go: %+v 를 기대했는데 %+v 다", want, d)
	}
	// ★ 상대가 규모를 안 준 경로는 **키가 없어야** 한다. 0 이 아니다.
	if d, ok := got[0].TheirDelta["b.bin"]; ok {
		t.Errorf("b.bin 의 규모 키가 생겼다(%+v) — 상대가 못 잰 것은 키가 없어야 한다", d)
	}
}

func TestOverlapsSortBiggestFirstAndUnknownOnTop(t *testing.T) {
	mk := func(id string, d map[string]model.LineDelta) LiveSession {
		return LiveSession{ID: id, CCSessionID: "cc-" + id, Paths: []string{"a.go"}, Delta: d}
	}
	live := []LiveSession{
		mk("s-small", map[string]model.LineDelta{"a.go": {Added: 3}}),
		mk("s-unknown", nil), // 규모를 못 읽었다
		mk("s-big", map[string]model.LineDelta{"a.go": {Added: 47, Removed: 1}}),
		mk("s-zero", map[string]model.LineDelta{"a.go": {Added: 0, Removed: 0}}),
	}
	got := OverlapsWithLive([]string{"a.go"}, live, "me", "cc-me")

	var order []string
	for _, o := range got {
		order = append(order, o.SessionID)
	}
	want := []string{"s-unknown", "s-big", "s-small", "s-zero"}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("순서 %q 를 기대했는데 %q 다", want, order)
	}
}

// ★ 이 시험이 이 태스크의 논지를 지킨다. 못 읽은 것을 0 으로 접으면
// s-unknown 이 s-zero 옆으로 내려가고 절단될 때 제일 먼저 사라진다.
func TestUnknownSizeOutranksAMeasuredZero(t *testing.T) {
	live := []LiveSession{
		{ID: "s-zero", CCSessionID: "cc-a", Paths: []string{"a.go"},
			Delta: map[string]model.LineDelta{"a.go": {}}},
		{ID: "s-unknown", CCSessionID: "cc-b", Paths: []string{"a.go"}},
	}
	got := OverlapsWithLive([]string{"a.go"}, live, "me", "cc-me")
	if got[0].SessionID != "s-unknown" {
		t.Fatalf("못 읽은 것이 맨 위여야 한다 — %q 가 먼저다. 아래로 밀면 절단될 때 "+
			"제일 먼저 사라지는 것이 못 잰 것이 된다", got[0].SessionID)
	}
}

func TestOverlapSortIsDeterministicOnTies(t *testing.T) {
	mk := func(id string) LiveSession {
		return LiveSession{ID: id, CCSessionID: "cc-" + id, Paths: []string{"a.go"},
			Delta: map[string]model.LineDelta{"a.go": {Added: 5}}}
	}
	got := OverlapsWithLive([]string{"a.go"}, []LiveSession{mk("s-b"), mk("s-a")}, "me", "cc-me")
	if got[0].SessionID != "s-a" || got[1].SessionID != "s-b" {
		t.Errorf("동점은 세션 id 오름차순이어야 한다: %q, %q", got[0].SessionID, got[1].SessionID)
	}
}

// TestPartiallyUnknownSessionOutranksAFullyKnownBiggerOne 은 겹친 경로가 여럿일 때
// **하나라도 못 읽으면 그 세션 전체가 `+∞`** 임을 잠근다.
//
// 위의 네 시험은 전부 세션당 겹침 경로가 하나뿐이라 "하나라도 못 읽으면"과
// "전부 못 읽었을 때만"을 가르지 못한다. 이 시험은 s-partial 이 경로 둘(하나는
// 작게 알고 하나는 모름)을 겹치고, s-big 은 경로 하나(크게 앎)만 겹치게 해서
// 그 둘을 가른다 — s-partial 이 s-big 보다 위여야 한다.
func TestPartiallyUnknownSessionOutranksAFullyKnownBiggerOne(t *testing.T) {
	live := []LiveSession{
		{ID: "s-big", CCSessionID: "cc-big", Paths: []string{"a.go"},
			Delta: map[string]model.LineDelta{"a.go": {Added: 99}}}, // 크지만 전부 앎
		{ID: "s-partial", CCSessionID: "cc-partial", Paths: []string{"a.go", "b.go"},
			Delta: map[string]model.LineDelta{"a.go": {Added: 1}}}, // b.go 는 못 읽었다
	}
	got := OverlapsWithLive([]string{"a.go", "b.go"}, live, "me", "cc-me")
	if got[0].SessionID != "s-partial" {
		t.Fatalf("일부만 못 읽어도 그 세션 전체가 +∞ 여야 한다 — %q 가 먼저다. "+
			"s-partial(일부 못 읽음)이 s-big(전부 알고 규모가 큼)보다 위여야 한다",
			got[0].SessionID)
	}
}

func TestSelfClaimIsItsOwnCode(t *testing.T) {
	_, rejected := EligibleBundle(EligibleInput{
		Self:       "S1",
		Candidates: []Candidate{{Item: openItem("mine", 0), ClaimedBy: "S1"}},
	}, solo())
	if len(rejected) != 1 {
		t.Fatalf("사유 1건을 기대했는데 %v", rejected)
	}
	if rejected[0].Reason != RejectClaimedBySelf {
		t.Errorf("사유 = %q, 기대 = %q — 남이 집은 것과 처방이 다르다(회피가 아니라 재개다)",
			rejected[0].Reason, RejectClaimedBySelf)
	}
}

// 선점된 항목은 state 가 claimed 다. 축 순서가 뒤집히면 "closed" 라는 엉뚱한 사유가 나간다.
func TestClaimedItemReportsClaimNotClosed(t *testing.T) {
	it := openItem("mine", 0)
	it.State = model.ItemClaimed
	_, rejected := EligibleBundle(EligibleInput{
		Self:       "S1",
		Candidates: []Candidate{{Item: it, ClaimedBy: "S2"}},
	}, solo())
	if len(rejected) != 1 || rejected[0].Reason != RejectClaimed {
		t.Fatalf("사유 = %v, 기대 = [%s]", rejected, RejectClaimed)
	}
	if !strings.Contains(rejected[0].Detail, "S2") {
		t.Errorf("누가 쥐었는지 안 적혀 있다: %q", rejected[0].Detail)
	}
}

func TestSelfHeldResourceDoesNotBlock(t *testing.T) {
	in := EligibleInput{
		Self:          "S1",
		Candidates:    []Candidate{{Item: openItem("needs-staging", 0), Needs: []string{"staging"}}},
		HeldResources: map[string]string{"staging": "S1"},
	}
	best, rejected := EligibleBundle(in, solo())
	if best == nil {
		t.Errorf("내가 쥔 자원이 나를 막았다: %v", rejected)
	}

	// 대조: 남이 쥐면 막혀야 한다. 이 대조가 없으면 위 단정은 "자원 축을 아예 안 본다"와 구분되지 않는다.
	in.HeldResources = map[string]string{"staging": "S9"}
	best, rejected = EligibleBundle(in, solo())
	if best != nil {
		t.Fatalf("남이 쥔 자원인데 추천됐다")
	}
	if len(rejected) != 1 || rejected[0].Reason != RejectResourceHeld {
		t.Fatalf("사유 = %v, 기대 = [%s]", rejected, RejectResourceHeld)
	}
	if !strings.Contains(rejected[0].Detail, "staging") || !strings.Contains(rejected[0].Detail, "S9") {
		t.Errorf("어느 자원을 누가 쥐었는지 안 적혀 있다: %q", rejected[0].Detail)
	}
}

// 한 후보가 두 축에서 걸리면 사유도 두 줄이다. 첫 축에서 끊으면
// 자원을 반납받고 다시 왔더니 이번엔 선행이 안 됐더라가 반복된다.
func TestMultipleAxesGiveMultipleReasons(t *testing.T) {
	it := openItem("both", 0)
	it.After = []model.After{{Item: "dep"}}
	_, rejected := EligibleBundle(EligibleInput{
		Self:          "S1",
		Candidates:    []Candidate{{Item: it, Needs: []string{"staging"}}},
		Facts:         AfterFacts{ItemStates: map[string]model.ItemState{"dep": model.ItemOpen}},
		HeldResources: map[string]string{"staging": "S9"},
	}, solo())
	want := []string{AfterUnmetItem, RejectResourceHeld}
	if got := codesFor(rejected, "both"); !reflect.DeepEqual(got, want) {
		t.Errorf("사유 = %v, 기대 = %v", got, want)
	}
}

func TestPickIsDeterministicRegardlessOfInputOrder(t *testing.T) {
	base := []Candidate{
		{Item: openItem("c", 5), Dependents: 2},
		{Item: openItem("a", 5), Dependents: 2}, // c 와 나이·의존자 동점 → id 로 갈린다
		{Item: openItem("b", 1), Dependents: 2}, // 더 오래됐다
		{Item: openItem("d", 0), Dependents: 9}, // 의존자가 가장 많다
	}
	mk := func(cs []Candidate) EligibleInput {
		return EligibleInput{Self: "S1", Candidates: cs}
	}

	first, _ := EligibleBundle(mk(base), solo())
	if first == nil || first.Lead.Item.ID != "d" {
		t.Fatalf("추천 = %v, 기대 = d(의존자 9)", first)
	}

	// 입력 순서를 뒤집어도 같은 답이어야 한다.
	rev := make([]Candidate, 0, len(base))
	for i := len(base) - 1; i >= 0; i-- {
		rev = append(rev, base[i])
	}
	second, _ := EligibleBundle(mk(rev), solo())
	if second.Lead.Item.ID != first.Lead.Item.ID {
		t.Errorf("입력 순서에 답이 흔들린다: %s vs %s", first.Lead.Item.ID, second.Lead.Item.ID)
	}

	// 의존자 동점이면 오래된 것, 그마저 동점이면 id 사전순.
	noD := base[:3]
	got, _ := EligibleBundle(mk(noD), solo())
	if got.Lead.Item.ID != "b" {
		t.Errorf("동점일 때 오래된 것이 아니다: %s", got.Lead.Item.ID)
	}
	tie := []Candidate{base[0], base[1]} // c, a — 나이·의존자 동점
	got, _ = EligibleBundle(mk(tie), solo())
	if got.Lead.Item.ID != "a" {
		t.Errorf("완전 동점의 동점 처리가 id 사전순이 아니다: %s", got.Lead.Item.ID)
	}
	got, _ = EligibleBundle(mk([]Candidate{base[1], base[0]}), solo())
	if got.Lead.Item.ID != "a" {
		t.Errorf("완전 동점에서 입력 순서에 답이 흔들린다: %s", got.Lead.Item.ID)
	}
}

// TestLessCandidateOrdersByDependentsThenAgeThenID 는 추천 순서 세 축을
// lessCandidate 를 **직접** 불러 확인한다.
//
// ★ EligibleBundle 을 통해서 확인하면 이 축들이 안 잡힌다 — 돌연변이로 실측했다.
// 의존자 축을 뒤집어도(`>` → `<`) 나이 축을 통째로 지워도(`if false`) 전 스위트가 초록이다.
// bundles 를 다시 정렬하는 lessBundle 이 Bundle.Dependents(합)·Bundle.Oldest 로 같은
// 판정을 하고, 그쪽은 선두 id 로 끝나는 전순서라 SliceStable 의 입력 순서가 결과를
// 못 바꾼다. 즉 위 시험들이 잠그는 것은 lessBundle 이지 이 함수가 아니다.
// bundle_test.go 의 TestLessBundleTieBreaksByLeadID·TestLessBundlePrefersOlder 가
// 반대 방향에서 같은 함정을 피한 것과 같은 처방이다.
//
// 이 순서가 제품에서 사는 자리는 **묶음 구성원의 표시 순서** 하나다(bundle.go 의
// fit 정렬을 Members 가 물려받는다 — bundle.go:438). 그 연결 자체는
// TestEligibleBundleTieBreaksByLeadID 가 ③ 축으로 잠근다.
func TestLessCandidateOrdersByDependentsThenAgeThenID(t *testing.T) {
	mk := func(id string, minutes, dependents int) Candidate {
		return Candidate{Item: openItem(id, minutes), Dependents: dependents}
	}

	// ① 의존자 수 ↓. 나머지 두 축을 **기대와 반대**로 둔다 — 이 축이 사라져 ②③으로
	// 새면 승자가 뒤집혀서 잡힌다(더 최근이고 id 도 뒤인 쪽이 이겨야 한다).
	many, few := mk("z-many", 10, 3), mk("a-few", 0, 1)
	if !lessCandidate(many, few) {
		t.Errorf("의존자가 많은 쪽(z-many, 3)이 안 이겼다 — ①이 첫 축이 아니다")
	}
	if lessCandidate(few, many) {
		t.Errorf("①의 역방향이 대칭이 아니다 — a-few(1)가 z-many(3)를 이겼다")
	}

	// ② 나이 ↑. ①은 동점, ③은 기대와 반대다.
	old, recent := mk("z-old", 0, 2), mk("a-new", 10, 2)
	if !lessCandidate(old, recent) {
		t.Errorf("① 동점인데 오래된 쪽(z-old)이 안 이겼다 — ②가 사라지고 ③(id)으로 샜다")
	}
	if lessCandidate(recent, old) {
		t.Errorf("②의 역방향이 대칭이 아니다 — 더 최근인 a-new 가 이겼다")
	}

	// ③ id 사전순. ①② 동점 — 없으면 같은 입력에 다른 답이 나온다.
	a, z := mk("a", 5, 2), mk("z", 5, 2)
	if !lessCandidate(a, z) {
		t.Errorf("①② 동점인데 id 사전순으로 안 갈렸다")
	}
	if lessCandidate(z, a) {
		t.Errorf("③의 역방향이 대칭이 아니다 — z 가 a 를 이겼다")
	}
}

func TestRejectedIsDeterministic(t *testing.T) {
	it := openItem("multi", 0)
	it.After = []model.After{{Item: "d1"}, {SHA: "s1"}, {Item: "d2"}}
	in := EligibleInput{
		Self: "S1",
		Candidates: []Candidate{
			{Item: it, Needs: []string{"staging", "docs-land"}},
			{Item: openItem("other", 1), ClaimedBy: "S2"},
		},
		Facts: AfterFacts{
			ItemStates:  map[string]model.ItemState{"d1": model.ItemOpen, "d2": model.ItemDropped},
			SHAAncestry: map[string]AncestryResult{"s1": AncestryNo},
		},
		HeldResources: map[string]string{"staging": "S7", "docs-land": "S8"},
	}
	_, a := EligibleBundle(in, solo())
	_, b := EligibleBundle(in, solo())
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("같은 입력에 다른 원장이 나왔다:\n%v\n%v", a, b)
	}
	// 선행 사유는 선언 순서대로, 자원은 Needs 순서대로.
	want := []string{AfterUnmetItem, AfterUnmetSHA, AfterDroppedDep, RejectResourceHeld, RejectResourceHeld}
	if got := codesFor(a, "multi"); !reflect.DeepEqual(got, want) {
		t.Errorf("사유 순서 = %v, 기대 = %v", got, want)
	}
}

// 표 밖: 후보가 없으면 추천도 원장도 비어 있다(패닉하지 않는다).
func TestEligibleWithNoCandidates(t *testing.T) {
	best, rejected := EligibleBundle(EligibleInput{Self: "S1"}, solo())
	if best != nil {
		t.Errorf("후보가 없는데 추천이 나왔다: %+v", best)
	}
	if len(rejected) != 0 {
		t.Errorf("후보가 없는데 사유가 나왔다: %v", rejected)
	}
}

// 표 밖: 입력으로 준 Overlaps 는 무시하고 다시 계산한다.
// 호출자가 넣어 둔 옛 값이 그대로 나가면 화면이 낡은 겹침을 현재 사실로 보여준다.
func TestPickedOverlapsAreRecomputedNotInherited(t *testing.T) {
	stale := []Overlap{{SessionID: "S-옛날", Pairs: [][2]string{{"a", "b"}}}}
	best, _ := EligibleBundle(EligibleInput{
		Self:       "S1",
		Candidates: []Candidate{{Item: openItem("a", 0, "pipeline/"), Overlaps: stale}},
		Live:       nil,
	}, solo())
	if best == nil {
		t.Fatal("추천이 없다")
	}
	if len(best.Lead.Overlaps) != 0 {
		t.Errorf("입력의 낡은 겹침이 그대로 나왔다: %+v", best.Lead.Overlaps)
	}
}

// 표 밖: 순수 함수여야 한다 — 입력 슬라이스를 고치면 호출자가 보는 것과 갈라진다.
func TestEligibleDoesNotMutateInput(t *testing.T) {
	cands := []Candidate{{Item: openItem("a", 0, "pipeline/")}}
	EligibleBundle(EligibleInput{
		Self:       "S1",
		Candidates: cands,
		Live:       []LiveSession{{ID: "S2", Paths: []string{"pipeline/x.py"}}},
	}, solo())
	if len(cands[0].Overlaps) != 0 {
		t.Errorf("입력 후보가 수정됐다: %+v", cands[0].Overlaps)
	}
}
