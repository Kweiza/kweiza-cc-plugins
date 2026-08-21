package judge

import (
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 선행을 기다렸다가 풀린 묶음이 같은 층에서 먼저 온다 (2026-08-21).
//
// 실측이 이 축의 근거다(원장, 창 2026-08-11~08-20). after 해소 판정은 pick 호출마다
// 즉석 계산돼서 **적격은 즉시 열리는데**, 순위에는 한 줄도 안 닿았다:
// 해소→첫 집힘이 중앙 3.0시간인데 p90 이 119시간이고, 해소됐는데 아직 안 집힌 열린 항목이
// **20건**(중앙 3.3일 · 최대 18.2일 · 합 117일)이다. 그 20건 중 15건은 해소 뒤 pick_eval 에
// 12~103회 올랐고 **전부 not-top** 이었다 — 자격만 열리고 순서는 그대로였다.
//
// ★ 축의 자리는 "기아와 같은 층, 그 안에서 먼저"다. 기아를 **안 덮는다** —
// 굶은 것이 먼저라는 §4 근거가 그대로 살아야 한다. 그래도 문제의 20건이 풀리는 이유는
// 그중 15건이 **이미 기아 영역**이라 굶김 전용 갈래 안에서 서로 밀고 있었기 때문이다.

// 굶은 묶음끼리 — 이 시험이 축의 자리를 정한다.
//
// ★ lessBundle 의 굶김 전용 갈래(`if a.Starved`)는 무조건 return 한다. 축을 그 갈래
// **뒤**에 두면 굶은 묶음끼리는 영영 안 읽히고, 이 축이 겨냥한 인구(20건 중 15건)에
// 대해 통째로 무동작이 된다 — CloseDeclared 축이 같은 이유로 갈래 앞에 놓였다.
//
// 축 격리: cleared 를 **더 어리게**(굶김 갈래는 최고령순) 두고 id 도 뒤로 보낸다.
// 축이 갈래 뒤로 내려가면 stale 이 이겨서 반드시 붉어진다.
func TestLessBundleClearedAfterWinsAmongStarved(t *testing.T) {
	cleared := Bundle{Lead: cand("z-cleared", 0, nil), Oldest: t0.Add(time.Hour), Starved: true, AfterCleared: true}
	stale := Bundle{Lead: cand("a-stale", 0, nil), Oldest: t0, Starved: true}
	if !lessBundle(cleared, stale) {
		t.Fatalf("둘 다 굶었을 때 '선행이 풀렸다'가 안 읽혔다 — 축이 굶김 전용 갈래 뒤에 있으면\n" +
			"해소 뒤 안 집힌 20건 중 15건(이미 기아 영역)에 대해 무동작이다")
	}
	if lessBundle(stale, cleared) {
		t.Fatalf("역방향이 대칭이 아니다 — 선행을 안 기다린 쪽이 그대로 이겼다")
	}
}

// 안 굶은 묶음끼리도 같은 축이 돈다.
//
// 축 격리: ②(묶음 크기)·③(최고령)·④(id)를 **전부 stale 편으로** 몰아 둔다.
// cleared 편은 이 축 하나뿐이라, 축을 지우면 반드시 붉어진다.
func TestLessBundleClearedAfterWinsAmongUnstarved(t *testing.T) {
	cleared := Bundle{Lead: cand("z-cleared", 0, nil), Oldest: t0.Add(72 * time.Hour), AfterCleared: true}
	stale := Bundle{
		Lead:    cand("a-stale", 0, nil),
		Members: []Candidate{cand("m1", 0, nil), cand("m2", 0, nil)},
		Oldest:  t0,
	}
	if !lessBundle(cleared, stale) {
		t.Fatalf("안 굶은 묶음끼리 '선행이 풀렸다'가 안 읽혔다:\n선행을 기다린 항목은 이미 한 번 대기했고, 지금 안 하면 또 밀린다")
	}
	if lessBundle(stale, cleared) {
		t.Fatalf("역방향이 대칭이 아니다")
	}
}

// ★ 기아를 **안 덮는다.** 굶은 쪽이 여전히 먼저다 — 이 축은 기아 위가 아니라 같은 층이다.
//
// 이 시험이 없으면 축을 Starved 앞에 놓아도 위 둘이 초록이라, 사용자가 고른 배치
// ("기아와 같은 층, 그 안에서 먼저")와 다른 것이 조용히 들어온다.
func TestLessBundleStarvedStillBeatsClearedAfter(t *testing.T) {
	starved := Bundle{Lead: cand("z-starved", 0, nil), Oldest: t0.Add(72 * time.Hour), Starved: true}
	cleared := Bundle{Lead: cand("a-cleared", 0, nil), Oldest: t0, AfterCleared: true}
	if !lessBundle(starved, cleared) {
		t.Fatalf("선행이 풀린 안 굶은 묶음이 굶은 묶음을 이겼다 — 이 축은 기아를 덮으면 안 된다(§4)")
	}
}

// ①(의존자 합)도 이 축보다 앞이다. 순서를 안 바꾼다.
func TestLessBundleDependentsStillBeatClearedAfter(t *testing.T) {
	dep := Bundle{Lead: cand("z-dep", 0, nil), Oldest: t0.Add(72 * time.Hour), Dependents: 2}
	cleared := Bundle{Lead: cand("a-cleared", 0, nil), Oldest: t0, AfterCleared: true, Dependents: 1}
	if !lessBundle(dep, cleared) {
		t.Fatalf("의존자 합이 '선행이 풀렸다'에 밀렸다 — 축 ①은 그대로여야 한다")
	}
}

// bundleAround 가 그 사실을 **선두에서** 읽는다.
//
// ★ 선두 기준인 이유는 StarveOldest·CloseDeclared 와 같다 — "이 축은 '지금 새로 집어도
// 되나'에 답하고, 그 질문의 주어는 브랜치를 받는 선두다". 그리고 선두는 fit 에 든 것이므로
// (EligibleBundle 이 fit 전원을 각각 선두로 세운다) 그 선행은 **이미 전부 충족된 것**이다.
// 흡수된 구성원은 미충족 선행을 가질 수 있어서(blockedOnlyBy) 구성원까지 보면 뜻이 뒤집힌다.
func TestBundleAroundMarksClearedAfterFromLeadOnly(t *testing.T) {
	withAfter := cand("has-after", 0, nil, model.After{Item: "dep-done"})
	plain := cand("no-after", 0, nil)

	b := bundleAround(withAfter, []Candidate{withAfter}, nil, nil)
	if !b.AfterCleared {
		t.Fatalf("선두가 선행을 걸고 fit 에 들었는데 AfterCleared 가 안 붙었다 — fit 에 들었다는 것이 곧 충족됐다는 뜻이다")
	}

	b2 := bundleAround(plain, []Candidate{plain}, nil, nil)
	if b2.AfterCleared {
		t.Fatalf("선행이 없는 선두에 AfterCleared 가 붙었다 — 그러면 축이 상시 참이 되어 판별력이 0이다(§4)")
	}
}
