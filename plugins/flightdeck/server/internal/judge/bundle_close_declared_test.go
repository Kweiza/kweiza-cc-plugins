package judge

import (
	"testing"
	"time"
)

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
