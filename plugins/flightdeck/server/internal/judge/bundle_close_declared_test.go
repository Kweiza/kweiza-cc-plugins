package judge

import (
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// decl 은 시험용 종료 선언이다. mode 는 마지막 선언의 것이다.
func decl(done, dropped int, mode string) model.CloseDeclaration {
	return model.CloseDeclaration{
		Done: done, Dropped: dropped,
		Last: t0.Add(-time.Hour), LastSession: "01KZ785T-OLD", LastMode: mode,
	}
}

// 정렬 축(강등)을 lessBundle 을 직접 불러 확인한다 — **안 굶은 묶음끼리**.
//
// ★ EligibleBundle 을 통해서 확인하면 안 되는 이유는 bundle_test.go:370-378 이
// 이미 적어 뒀다: bundles 는 lessCandidate 로 정렬된 fit 에서 만들어져서, 축을
// 통째로 지워도 우연히 같은 답이 나온다.
//
// 축 격리: ②(묶음 크기)·③(최고령)·④(id)를 **전부 declared 편으로** 몰아 둔다.
// 강등 축 하나만 clean 편이다. 그래서 이 축을 지우면 반드시 붉어진다.
func TestLessBundleCloseDeclaredSinksAmongUnstarved(t *testing.T) {
	declared := Bundle{
		Lead:          cand("a-declared", 0, nil),
		Members:       []Candidate{cand("m1", 0, nil), cand("m2", 0, nil)},
		Oldest:        t0,
		CloseDeclared: true,
	}
	clean := Bundle{Lead: cand("z-clean", 0, nil), Oldest: t0.Add(72 * time.Hour)}
	if !lessBundle(clean, declared) {
		t.Fatalf("종료 선언이 붙은 3건 묶음이 안 붙은 단독을 이겼다 — 닫히지 못한 항목이 다시 큐의 머리에 선다")
	}
	if lessBundle(declared, clean) {
		t.Fatalf("역방향이 대칭이 아니다 — 강등된 쪽이 이겼다")
	}
}

// 같은 축을 **굶은 묶음끼리** 확인한다. 이 시험이 축의 자리를 정한다.
//
// ★ lessBundle 의 굶김 전용 갈래(`if a.Starved`)는 무조건 return 한다. 그래서
// 이 축을 그 갈래 **뒤**에 두면 굶은 묶음끼리는 영영 안 읽힌다. 지금 큐는 열린
// 30건 중 26건이 굶었고 사고 항목도 회수 시점에 42시간이었다 — 뒤에 두면 이 축이
// 겨냥한 인구 **전체**에 대해 무동작이 된다. 위 시험은 그 배치를 못 가른다.
//
// 축 격리: declared 를 **더 오래 굶은 쪽**으로 둔다(굶김 갈래는 최고령순이다).
// id 도 declared 편이다. 축이 갈래 뒤로 내려가면 declared 가 이겨서 반드시 붉어진다.
func TestLessBundleCloseDeclaredSinksAmongStarvedToo(t *testing.T) {
	declared := Bundle{Lead: cand("a-declared", 0, nil), Oldest: t0, Starved: true, CloseDeclared: true}
	clean := Bundle{Lead: cand("z-clean", 0, nil), Oldest: t0.Add(time.Hour), Starved: true}
	if !lessBundle(clean, declared) {
		t.Fatalf("둘 다 굶었을 때 강등이 안 읽혔다 — 축이 굶김 전용 갈래 뒤에 있으면 큐의 26/30 에 대해 무동작이다")
	}
	if lessBundle(declared, clean) {
		t.Fatalf("역방향이 대칭이 아니다 — 더 오래 굶은 강등이 그대로 이겼다")
	}
}

// ①(의존자 합)은 강등보다 **앞**이다. 이걸 풀어야 남이 움직이는 정도는
// "이 항목이 이미 닫혔을지 모른다"보다 먼저 답해야 할 질문이다.
//
// 축 격리: ③④를 clean 편에 두고, 강등도 clean 편이다. declared 편은 ① 하나뿐이다.
func TestLessBundleDependentsBeatCloseDeclared(t *testing.T) {
	declared := Bundle{Lead: cand("z-declared", 0, nil), Dependents: 5,
		Oldest: t0.Add(72 * time.Hour), CloseDeclared: true}
	clean := Bundle{Lead: cand("a-clean", 0, nil), Dependents: 1, Oldest: t0}
	if !lessBundle(declared, clean) {
		t.Fatalf("의존자 합 5가 강등에 밀렸다 — ①이 강등보다 앞이어야 한다")
	}
	if lessBundle(clean, declared) {
		t.Fatalf("역방향이 대칭이 아니다")
	}
}

// 기아는 강등보다 **앞**이다 — 그것이 이 설계의 탈출구다.
//
// ★ 강등에 유효기간을 안 걸었다(설계 §3: 항목을 위험하게 만든 조건은 시간이
// 지난다고 낫지 않는다). 그러면 강등된 항목이 영영 안 나오는 루프가 걱정인데,
// 축을 Starved **아래**에 둔 것이 그 루프를 구조적으로 끊는다 — 강등된 항목도
// 굶는 순간 안 굶은 묶음 전부를 이긴다. 조정 상수를 하나도 안 들이고 끊는다.
//
// 축 격리: ②③④와 강등까지 **전부** clean 편이다. declared 편은 기아 하나뿐이다.
func TestLessBundleStarvedBeatsCloseDeclared(t *testing.T) {
	declaredStarved := Bundle{Lead: cand("z-declared", 0, nil),
		Oldest: t0.Add(72 * time.Hour), Starved: true, CloseDeclared: true}
	cleanFresh := Bundle{Lead: cand("a-clean", 0, nil),
		Members: []Candidate{cand("m1", 0, nil), cand("m2", 0, nil)}, Oldest: t0}
	if !lessBundle(declaredStarved, cleanFresh) {
		t.Fatalf("굶은 강등 항목이 안 굶은 묶음에 밀렸다 — 강등에 탈출구가 없어 영구 유배가 된다")
	}
	if lessBundle(cleanFresh, declaredStarved) {
		t.Fatalf("역방향이 대칭이 아니다")
	}
}

// EligibleBundle 이 두 필드를 실제로 채우는지 — 배선이 이어져 있는지 본다.
//
// ★ lessBundle 단위 시험만 있으면 CloseDeclared 를 **아무도 안 채우는** 상태가
// 통과한다. 이 저장소가 여러 번 겪은 실패 모양이다(계산은 되는데 읽는 쪽이 0건) —
// TestEligibleBundleMarksStarvation 이 같은 이유로 서 있다.
//
// 그리고 **강등은 탈락이 아니다.** 단독 후보 하나로 부르면 그 항목은 여전히
// 추천된다 — 거르면 선점 표류 아닌 이유로 롤백된 항목까지 큐에서 사라진다(설계 §3).
func TestEligibleBundleMarksCloseDeclared(t *testing.T) {
	only := cand("a-declared", 0, nil)
	best, rej := EligibleBundle(EligibleInput{
		Self:                  "S1",
		Candidates:            []Candidate{only},
		CloseDeclarations:     map[string]model.CloseDeclaration{"a-declared": decl(1, 0, "done")},
		CloseDeclarationsRead: true,
	}, SiblingIndex{})
	if best == nil {
		t.Fatalf("강등이 탈락으로 샜다 — 추천이 nil 이다(사유 %v)", rej)
	}
	if best.Lead.Item.ID != "a-declared" {
		t.Fatalf("선두가 %q 다", best.Lead.Item.ID)
	}
	if !best.CloseDeclared {
		t.Fatalf("선언이 있는데 CloseDeclared 가 false 다 — 배선이 끊겼다")
	}
	// 사유 문구는 넷을 다 말해야 한다: 수가 하한이라는 것 · 마지막 세션 · mode ·
	// done 의 처방. 하나라도 없으면 사람이 무엇을 확인해야 하는지 모른다.
	for _, want := range []string{"종료 선언 1건 이상", "01KZ785T-OLD", "mode=done", "이미 랜딩됐을 수 있다"} {
		if !strings.Contains(best.CloseDeclaredDetail, want) {
			t.Fatalf("근거 조각에 %q 가 없다: %q", want, best.CloseDeclaredDetail)
		}
		if !strings.Contains(best.Reason, want) {
			t.Fatalf("Reason 이 %q 를 안 싣는다 — 왜 강등했는지 답 못 하는 추천이 된다: %q", want, best.Reason)
		}
	}
	// mode 를 안 합친다 — dropped 의 처방은 done 과 다르다.
	dropBest, _ := EligibleBundle(EligibleInput{
		Self:                  "S1",
		Candidates:            []Candidate{cand("a-declared", 0, nil)},
		CloseDeclarations:     map[string]model.CloseDeclaration{"a-declared": decl(0, 1, "dropped")},
		CloseDeclarationsRead: true,
	}, SiblingIndex{})
	if !strings.Contains(dropBest.CloseDeclaredDetail, "이미 버리기로 판정됐을 수 있다") {
		t.Fatalf("dropped 의 처방이 done 과 같은 문장으로 접혔다: %q", dropBest.CloseDeclaredDetail)
	}
}

// 강등된 항목은 **밀리되 사라지지 않는다.**
//
// ★ 거르면 선점 표류 아닌 이유로 롤백된 항목까지 큐에서 사라진다(설계 §3).
// Overlaps 가 "거르지 않고 알린다"로 선 것과 같은 자리다.
//
// 배치: 둘 다 단독·동나이·의존자 0이라 ①②③이 전부 동점이고, 선언이 없으면
// ④(id 사전순)로 a-declared 가 이긴다. 선언 하나만으로 승자가 뒤집혀야 한다.
func TestEligibleBundleCloseDeclaredDemotesButDoesNotDrop(t *testing.T) {
	best, rej := EligibleBundle(EligibleInput{
		Self:                  "S1",
		Candidates:            []Candidate{cand("a-declared", 0, nil), cand("z-clean", 0, nil)},
		CloseDeclarations:     map[string]model.CloseDeclaration{"a-declared": decl(1, 0, "done")},
		CloseDeclarationsRead: true,
	}, SiblingIndex{})
	if best == nil {
		t.Fatalf("추천이 nil 이다(사유 %v)", rej)
	}
	if best.Lead.Item.ID != "z-clean" {
		t.Fatalf("선두가 %q 다 — 선언이 붙은 a-declared 가 밀렸어야 한다", best.Lead.Item.ID)
	}
	if best.CloseDeclared {
		t.Fatalf("선언이 없는 z-clean 이 강등으로 찍혔다 — 선두가 아닌 항목의 선언을 읽었다")
	}
	// 사라지지 않는다: 원장에 not-top 으로 남는다.
	if !contains(codesFor(rej, "a-declared"), RejectNotTop) {
		t.Fatalf("강등된 항목의 사유가 %v 다 — not-top 으로 원장에 남아야 한다", codesFor(rej, "a-declared"))
	}
}

// CloseDeclarationsRead 가 false 면 이 축은 **아예 안 돈다** — 맵이 채워져 있어도.
//
// ★ 이것이 nil 맵을 "안 읽음"으로 안 쓴 이유다. 조회가 실패했는데 그 실패가
// "선언 0건"으로 접히면, 축을 못 읽은 pick 이 사고를 낸 그 항목을 다시 1순위로
// 낸다 — 그런데 응답은 아무 말도 안 한다. 관측을 못 하면 판정하지 않는다.
func TestEligibleBundleWithoutCloseDeclarationsReadDoesNotDemote(t *testing.T) {
	in := EligibleInput{
		Self:              "S1",
		Candidates:        []Candidate{cand("a-declared", 0, nil), cand("z-clean", 0, nil)},
		CloseDeclarations: map[string]model.CloseDeclaration{"a-declared": decl(1, 0, "done")},
		// CloseDeclarationsRead 를 일부러 안 켠다.
	}
	best, rej := EligibleBundle(in, SiblingIndex{})
	if best == nil {
		t.Fatalf("추천이 nil 이다(사유 %v)", rej)
	}
	if best.Lead.Item.ID != "a-declared" {
		t.Fatalf("축을 안 읽었는데 순서가 바뀌었다 — 선두가 %q 다", best.Lead.Item.ID)
	}
	if best.CloseDeclared {
		t.Fatalf("축을 안 읽었는데 강등으로 찍혔다")
	}
	if strings.Contains(best.Reason, "종료 선언") {
		t.Fatalf("축을 안 읽었는데 Reason 이 선언을 말한다: %q", best.Reason)
	}
	for _, r := range rej {
		if strings.Contains(r.Detail, "종료 선언") {
			t.Fatalf("축을 안 읽었는데 원장에 근거가 남았다: %q", r.Detail)
		}
	}
}

// 키는 있는데 수가 0인 선언은 강등하지 않는다.
//
// ★ 지금 store 가 그런 항목을 안 만들지만, 이 필드는 판정의 **입력 계약**이고
// 계약은 호출부 하나가 지금 어떻게 짜여 있는지와 별개로 서 있어야 한다
// (TestEmptyResourceHolderDoesNotBlock 이 같은 이유로 서 있다). 0건을 강등으로
// 읽으면 "선언이 있었다"가 아니라 "맵에 키가 있었다"가 큐 순서를 바꾼다.
func TestEligibleBundleZeroCountCloseDeclarationDoesNotDemote(t *testing.T) {
	best, rej := EligibleBundle(EligibleInput{
		Self:       "S1",
		Candidates: []Candidate{cand("a-declared", 0, nil), cand("z-clean", 0, nil)},
		CloseDeclarations: map[string]model.CloseDeclaration{
			"a-declared": {Last: t0, LastSession: "S-EMPTY"}, // Count()==0
		},
		CloseDeclarationsRead: true,
	}, SiblingIndex{})
	if best == nil {
		t.Fatalf("추천이 nil 이다(사유 %v)", rej)
	}
	if best.Lead.Item.ID != "a-declared" || best.CloseDeclared {
		t.Fatalf("0건 선언으로 강등했다 — 선두 %q, CloseDeclared=%v", best.Lead.Item.ID, best.CloseDeclared)
	}
}

// 이 축이 **무엇을 몇 번 밀어냈는지**가 원장에 남는지 본다.
//
// ★ 안 남기면 pick_eval 의 not-top 줄이 "밀렸다"만 말하고 "왜"는 안 말한다.
// 그러면 이 축이 실제로 발화한 상태와 아예 안 도는 상태가 원장에서 같아 보이고,
// "조용히 버리는 것이 하나도 없다"가 형식만 지켜지고 목적은 안 지켜진다.
//
// ★ 대조를 함께 둔다 — 선언이 **없는** 항목의 not-top 줄에 이 조각이 붙으면,
// 원장에서 세는 수가 축의 발화 수가 아니라 그냥 not-top 수가 된다.
func TestEligibleBundleNotTopLedgersWhyCloseDeclared(t *testing.T) {
	in := EligibleInput{
		Self:                  "S1",
		Candidates:            []Candidate{cand("a-declared", 0, nil), cand("z-clean", 0, nil)},
		CloseDeclarations:     map[string]model.CloseDeclaration{"a-declared": decl(1, 0, "done")},
		CloseDeclarationsRead: true,
	}
	_, rej := EligibleBundle(in, SiblingIndex{})
	var detail string
	for _, r := range rej {
		if r.Item == "a-declared" && r.Reason == RejectNotTop {
			detail = r.Detail
		}
	}
	if detail == "" {
		t.Fatalf("전제가 깨졌다 — 강등된 항목의 not-top 줄이 없다: %v", rej)
	}
	// 기존 문장은 그대로 살아 있어야 한다. 덮어쓰면 "누구에게 밀렸나"가 사라진다.
	for _, want := range []string{"적격이지만 추천 묶음에 없다", "종료 선언 1건 이상", "mode=done"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("not-top 사유가 %q 를 안 싣는다: %q", want, detail)
		}
	}

	// 대조: 선언이 하나도 없으면 어느 not-top 줄에도 이 조각이 안 붙는다.
	clean := in
	clean.CloseDeclarations = map[string]model.CloseDeclaration{}
	_, rej2 := EligibleBundle(clean, SiblingIndex{})
	for _, r := range rej2 {
		if strings.Contains(r.Detail, "종료 선언") {
			t.Fatalf("선언이 없는데 근거가 붙었다(%s): %q", r.Item, r.Detail)
		}
	}
}

// 강등된 후보가 둘 이상이고 각자 다른 선언을 가졌을 때, 각 줄이 제 값을 다는지 본다.
// 안 남기면 `c.Item.ID` 를 `best.Lead.Item.ID` 로 바꿔치기 변이가 통과한다 —
// 교차오염(한 후보의 줄에 다른 후보의 값이 보기)을 못 잡는다.
//
// ★ 처방 문구가 갈려야 교차오염이 문자열로 드러난다. done 은 "이미 랜딩됐을 수 있다" 이고
// dropped 는 "이미 버리기로 판정됐을 수 있다"라 둘이 다르다(closeDeclaredDetail 참고).
func TestEligibleBundleNotTopEachLedgerCarriesOwnDeclaration(t *testing.T) {
	in := EligibleInput{
		Self: "S1",
		Candidates: []Candidate{
			cand("a-done-decl", 0, nil),
			cand("b-dropped-decl", 1, nil),
			cand("z-lead", 2, nil),
		},
		CloseDeclarations: map[string]model.CloseDeclaration{
			"a-done-decl":    decl(1, 0, "done"),
			"b-dropped-decl": decl(0, 1, "dropped"),
		},
		CloseDeclarationsRead: true,
	}
	_, rej := EligibleBundle(in, SiblingIndex{})

	// 각 후보의 not-top 줄을 찾는다.
	rejByID := make(map[string]string)
	for _, r := range rej {
		if r.Reason == RejectNotTop {
			rejByID[r.Item] = r.Detail
		}
	}

	// a-done-decl 의 줄: done 처방 있어야 하고, dropped 처방은 없어야 한다.
	aDoneDetail := rejByID["a-done-decl"]
	if aDoneDetail == "" {
		t.Fatalf("a-done-decl 의 not-top 줄이 없다: %v", rej)
	}
	if !strings.Contains(aDoneDetail, "이미 랜딩됐을 수 있다") {
		t.Fatalf("a-done-decl 줄에 done 처방이 없다: %q", aDoneDetail)
	}
	if strings.Contains(aDoneDetail, "이미 버리기로 판정됐을 수 있다") {
		t.Fatalf("a-done-decl 줄에 dropped 처방이 섞였다 — 교차오염이다: %q", aDoneDetail)
	}

	// b-dropped-decl 의 줄: dropped 처방 있어야 하고, done 처방은 없어야 한다.
	bDroppedDetail := rejByID["b-dropped-decl"]
	if bDroppedDetail == "" {
		t.Fatalf("b-dropped-decl 의 not-top 줄이 없다: %v", rej)
	}
	if !strings.Contains(bDroppedDetail, "이미 버리기로 판정됐을 수 있다") {
		t.Fatalf("b-dropped-decl 줄에 dropped 처방이 없다: %q", bDroppedDetail)
	}
	if strings.Contains(bDroppedDetail, "이미 랜딩됐을 수 있다") {
		t.Fatalf("b-dropped-decl 줄에 done 처방이 섞였다 — 교차오염이다: %q", bDroppedDetail)
	}
}
