package mcpsrv

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

var t0 = time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)

func TestFormatAge(t *testing.T) {
	cases := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"초", 42 * time.Second, "42초"},
		{"분", 12 * time.Minute, "12분"},
		{"시간", 3*time.Hour + 7*time.Minute, "3시간 7분"},
		{"일", 50 * time.Hour, "2일 2시간"},

		// ── 표 밖 케이스 ──
		{"0", 0, "0초"},
		{"음수(시계 역전)", -5 * time.Second, "0초"},
		{"딱 1분", time.Minute, "1분"},
		{"딱 1시간", time.Hour, "1시간 0분"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := FormatAge(c.d); got != c.want {
				t.Fatalf("FormatAge(%v) = %q, 기대 %q", c.d, got, c.want)
			}
		})
	}
}

// TestFormatSignalsKeepsFourAxesApart 는 신호를 합치지 않는다는 §4 규율을 문자열로 단정한다.
func TestFormatSignalsKeepsFourAxesApart(t *testing.T) {
	sig := map[model.SignalKind]time.Time{
		model.SignalPrompt: t0.Add(-2 * time.Minute),
		model.SignalTool:   t0.Add(-40 * time.Second),
	}
	got := FormatSignals(sig, t0)
	if !strings.Contains(got, "prompt 2분") || !strings.Contains(got, "tool 40초") {
		t.Fatalf("신호 두 축이 나란히 안 나온다: %q", got)
	}
	// 안 온 종류는 0값으로 채우지 않는다 — 부재와 0을 가른다.
	if strings.Contains(got, "commit") {
		t.Fatalf("한 번도 안 온 신호가 찍혔다: %q", got)
	}
	if got := FormatSignals(nil, t0); got != "신호 없음" {
		t.Fatalf("신호 0건에 %q — '신호 없음'이어야 한다", got)
	}
}

// TestRenderTailSeparatesZeroFromUnobserved 는 이 제품의 뿌리 원인 하나를 문자열로 막는다:
// "겹침 없음"과 "이 축을 안 본다"가 같은 화면이 되면 도구가 자기가 무엇을 안 보는지 모른다.
func TestRenderTailSeparatesZeroFromUnobserved(t *testing.T) {
	zero := RenderTail(TailInput{Now: t0, NotesObserved: true, OverlapsObserved: true})
	unobs := RenderTail(TailInput{Now: t0, NotesObserved: true, OverlapsObserved: false})

	if !strings.Contains(zero, "겹침: 없음") {
		t.Fatalf("겹침 0건이 '없음'으로 안 나온다:\n%s", zero)
	}
	if strings.Contains(unobs, "겹침: 없음") {
		t.Fatalf("안 읽은 축이 '없음'으로 나왔다 — 이 둘이 같으면 축이 조용히 죽는다:\n%s", unobs)
	}
	if !strings.Contains(unobs, "읽지 않았다") {
		t.Fatalf("안 읽었다는 사실이 꼬리에 없다:\n%s", unobs)
	}

	withOverlap := RenderTail(TailInput{
		Now: t0, NotesObserved: true, OverlapsObserved: true,
		Overlaps: []judge.Overlap{{SessionID: "01ABCDEFGH", Label: "트랙2",
			Pairs: [][2]string{{"pipeline/", "pipeline/x.py"}}}},
	})
	if !strings.Contains(withOverlap, "거르지 않고 알린다") ||
		!strings.Contains(withOverlap, "pipeline/↔pipeline/x.py") {
		t.Fatalf("겹침이 무엇끼리인지 안 보인다:\n%s", withOverlap)
	}

	// 알림 축도 같은 규율이다.
	notes := RenderTail(TailInput{
		Now: t0, NotesObserved: true, OverlapsObserved: true,
		Notes: []model.Judgment{{Kind: model.JudgmentAsk, SessionID: "01ZZZZZZZZ",
			At: t0.Add(-9 * time.Minute), Title: "contracts/ 는 건드리지 마라"}},
	})
	if !strings.Contains(notes, "contracts/ 는 건드리지 마라") || !strings.Contains(notes, "9분 전") {
		t.Fatalf("알림 본문·나이가 꼬리에 없다:\n%s", notes)
	}
	if !strings.Contains(notes, "확인 원장이 없다") {
		t.Fatalf("'미확인'을 '최근'으로 근사했다는 사실이 안 적혀 있다:\n%s", notes)
	}
}

// TestRenderPickCarriesBranchAndWorktree 는 설계 §6 의
// "pick 꼬리에 브랜치·워크트리 명령이 온다"를 응답 문자열로 단정한다.
func TestRenderPickCarriesBranchAndWorktree(t *testing.T) {
	item := model.Item{
		Project: "proj", ID: "t5-iam", Title: "IAM 컬럼 상한", Body: "본문이다",
		Paths: []string{"services/console-api/"}, State: model.ItemOpen, CreatedAt: t0,
	}
	res := service.PickResult{
		Mode: service.PickRecommended, Reason: "1순위다", Scope: "후보 = 열린 항목 3건",
		Item: &item, Branch: item.ID,
		Setup: service.SetupCommands("/home/a/proj", "main", item.ID),
		Rejected: []model.Rejection{
			{Item: "t4-x", Reason: judge.RejectClaimed, Detail: "세션 01AB 가 선점했다"},
			{Item: "t6-y", Reason: judge.AfterUnmetItem, Detail: "선행 t3-z 가 안 끝났다"},
		},
	}
	got := RenderPick(res, t0)

	if !strings.Contains(got, "브랜치: t5-iam") {
		t.Fatalf("브랜치 이름이 없다:\n%s", got)
	}
	if !strings.Contains(got, "git worktree add '.flightdeck/worktrees/t5-iam' -b t5-iam 'main'") {
		t.Fatalf("워크트리 준비 명령이 없다:\n%s", got)
	}
	// 기계가 세는 값(사유 코드)은 사람 말로 풀지 않고 그대로 보인다.
	for _, code := range []string{judge.RejectClaimed, judge.AfterUnmetItem} {
		if !strings.Contains(got, code) {
			t.Fatalf("탈락 사유 코드 %q 가 응답에 없다:\n%s", code, got)
		}
	}
	if !strings.Contains(got, "아직 선점하지 않았다") {
		t.Fatalf("추천이 선점으로 오해될 수 있다:\n%s", got)
	}

	// id 가 안전하지 않아 명령을 못 만든 경우 — 침묵하지 않는다.
	bad := res
	bad.Branch = "--evil"
	bad.Setup = service.SetupCommands("/home/a/proj", "main", "--evil")
	if out := RenderPick(bad, t0); !strings.Contains(out, "워크트리 준비 명령을 만들지 않았다") {
		t.Fatalf("명령을 못 만든 사실이 응답에 없다:\n%s", out)
	}
}

// TestRenderPickCarriesQueueSizeInEveryMode 는 네 모드 어느 쪽으로 들어와도
// 같은 이름의 같은 줄을 본다는 것이다 — 세션이 모드를 보고 어디를 읽을지 고르지 않아도 된다.
func TestRenderPickCarriesQueueSizeInEveryMode(t *testing.T) {
	n := 5
	for _, mode := range []service.PickMode{
		service.PickRecommended, service.PickClaimed, service.PickResumed, service.PickNone,
	} {
		got := RenderPick(service.PickResult{Mode: mode, Reason: "사유다", QueueOpen: &n}, t0)
		if !strings.Contains(got, "큐 열림 5건") {
			t.Fatalf("%s 모드 응답에 큐 열림 수가 없다:\n%s", mode, got)
		}
	}
}

// TestRenderPickNeverCallsAnAbsentQueueSizeZero 가 이 설계에서 가장 중요한 시험이다.
//
// nil 이 되는 경로는 구버전 서버(SkewBanner 가 안 잡는다) · 필드가 생기기 전의 캐시 ·
// 조회 실패 셋이다. 그것을 "큐 열림 0건" 으로 찍으면 신선한 온라인 응답이 거짓을 단정하고,
// none 모드에는 그 모순을 드러낼 항목조차 없어 에이전트가 "큐가 비었다" 로 읽고 세션을 접는다.
// 스큐 구간에서만 나타나는 실패라 사람이 재현하기 어렵다 — 이 시험이 유일한 방벽이다.
func TestRenderPickNeverCallsAnAbsentQueueSizeZero(t *testing.T) {
	got := RenderPick(service.PickResult{Mode: service.PickNone, Reason: "적격 0건이다"}, t0)
	if strings.Contains(got, "큐 열림 0건") {
		t.Fatalf("부재를 0건으로 단정했다:\n%s", got)
	}
	if !strings.Contains(got, "이 응답에 없다") {
		t.Fatalf("부재를 침묵으로 접었다 — 안 본 것을 침묵하지 않는다:\n%s", got)
	}
}

// TestRenderPickPrintsAGenuineZero 는 부재의 반대편을 못박는다.
//
// nil 이 "0건" 이 되면 안 된다는 것은 다른 시험이 지킨다. 이 시험은 그 반대 —
// **진짜 0건이 부재로 접히면 안 된다**. 둘 중 하나만 지키면 `*QueueOpen > 0` 같은
// '정리'가 시험을 전부 통과하면서 빈 큐를 "서버가 안 냈다" 로 바꿔 놓는다.
func TestRenderPickPrintsAGenuineZero(t *testing.T) {
	zero := 0
	got := RenderPick(service.PickResult{Mode: service.PickNone, Reason: "적격 0건이다", QueueOpen: &zero}, t0)
	if !strings.Contains(got, "큐 열림 0건") {
		t.Fatalf("진짜 0건이 숫자로 안 나왔다:\n%s", got)
	}
	if strings.Contains(got, "이 응답에 없다") {
		t.Fatalf("진짜 0건을 부재로 접었다:\n%s", got)
	}
}

// ★ 이 시험이 이 태스크에서 가장 중요하다.
// 묶음 축이 없는 응답(구서버·옛 캐시)이 "묶을 게 없다"로 읽히면 안 된다.
func TestRenderPickNeverCallsAnAbsentBundleSolo(t *testing.T) {
	got := RenderPick(service.PickResult{
		Mode: service.PickRecommended, Reason: "1순위다", Bundle: nil,
	}, t0)
	if !strings.Contains(got, "이 응답은 그 축을 읽지 않았다") {
		t.Fatalf("묶음 축 부재를 안 말한다:\n%s", got)
	}
	if strings.Contains(got, "묶을 게 없어 단독이다") {
		t.Fatalf("안 읽은 축을 '단독'으로 단정했다:\n%s", got)
	}
}

// 구성원 0건이면 단독이라고 **말한다**. 침묵하면 부재와 같은 화면이 된다.
func TestRenderPickSaysSoloWhenBundleIsEmpty(t *testing.T) {
	got := RenderPick(service.PickResult{
		Mode: service.PickRecommended, Reason: "1순위다",
		Bundle: &service.BundleInfo{Reason: "의존자 합 0 · 묶음 1건"},
	}, t0)
	if !strings.Contains(got, "단독") {
		t.Fatalf("단독임을 안 말한다:\n%s", got)
	}
}

func TestRenderPickShowsWhyEachMemberIsBundled(t *testing.T) {
	res := service.PickResult{
		Mode: service.PickRecommended, Reason: "1순위다",
		Item:   &model.Item{ID: "lead", Title: "선두", State: model.ItemOpen, CreatedAt: t0},
		Branch: "lead",
		Bundle: &service.BundleInfo{
			Reason: "의존자 합 0 · 묶음 2건 · 최고령 2026-08-04 23:50 · 선두 lead",
			Members: []service.BundleMember{{
				Item: model.Item{ID: "mem", Title: "구성원", State: model.ItemOpen,
					Paths: []string{"x.go"}, CreatedAt: t0},
				Link: judge.Link{Item: "mem",
					Axes:   []judge.BundleAxis{judge.AxisSibling, judge.AxisAfter},
					Detail: "판단 J1 가 둘을 함께 가리킨다 · 선행이 같다(sha:47421b4)"},
			}},
		},
	}
	got := RenderPick(res, t0)
	for _, want := range []string{"mem", "묶은 근거", "sibling", "after", "J1", "47421b4"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q 가 응답에 없다:\n%s", want, got)
		}
	}
	// 브랜치는 선두 하나다.
	if strings.Count(got, "브랜치: ") != 1 {
		t.Fatalf("브랜치 줄이 하나가 아니다:\n%s", got)
	}
}

// 못 집은 구성원은 사유 코드 그대로 보인다.
func TestRenderPickShowsUnclaimedMemberReason(t *testing.T) {
	res := service.PickResult{
		Mode: service.PickClaimed, Reason: "선두를 선점했다",
		Item: &model.Item{ID: "lead", Title: "선두", State: model.ItemClaimed, CreatedAt: t0},
		Bundle: &service.BundleInfo{Members: []service.BundleMember{{
			Item:      model.Item{ID: "blocked", Title: "막힘", CreatedAt: t0},
			Claimed:   false,
			Rejection: &model.Rejection{Item: "blocked", Reason: judge.RejectClaimed, Detail: "세션 S2 가 선점했다"},
		}}},
	}
	got := RenderPick(res, t0)
	for _, want := range []string{"못 집었다", judge.RejectClaimed, "S2"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q 가 응답에 없다:\n%s", want, got)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 리뷰 라운드 1 — findings 1·2·3·4
// ─────────────────────────────────────────────────────────────────────────────

// TestRenderPickBundleMemberMarksDistinguishThreeStates 는 finding 1·2 를 잠근다.
//
// Claimed 필드 하나로 표식을 가르면(!Claimed) "아직 안 시도함"(추천 후보, Rejection=nil)과
// "시도했지만 실패함"(Rejection!=nil)이 같은 표식이 된다. 리뷰어가 실측한 시나리오:
// 4건 추천에서 넷 다 ✗ 로 찍히면 에이전트가 "셋이 탈락했다"로 읽고 판정이 방금
// 지어 준 묶음을 버린 채 혼자 다시 집는다. 세 표식을 각자 다른 구성원 id 옆에
// 정확히 박아서, 어느 둘을 맞바꿔도 최소 하나는 어긋나게 만든다(finding 2 의
// "완전히 뒤집을 수 있는 술어" 문제 — 표식 둘만으로는 스왑이 시험을 안 건드렸다).
func TestRenderPickBundleMemberMarksDistinguishThreeStates(t *testing.T) {
	res := service.PickResult{
		Mode: service.PickRecommended, Reason: "1순위다",
		Item: &model.Item{ID: "lead", Title: "선두", State: model.ItemOpen, CreatedAt: t0},
		Bundle: &service.BundleInfo{
			Members: []service.BundleMember{
				{Item: model.Item{ID: "claimed-mem", Title: "집음", State: model.ItemClaimed, CreatedAt: t0},
					Claimed: true},
				{Item: model.Item{ID: "rejected-mem", Title: "거절됨", CreatedAt: t0},
					Rejection: &model.Rejection{Item: "rejected-mem", Reason: judge.RejectClaimed, Detail: "세션 S2 가 선점했다"}},
				{Item: model.Item{ID: "proposed-mem", Title: "제안됨", State: model.ItemOpen, CreatedAt: t0},
					Link: judge.Link{Item: "proposed-mem",
						Axes: []judge.BundleAxis{judge.AxisSibling}, Detail: "판단 J2"}},
			},
		},
	}
	got := RenderPick(res, t0)
	for _, want := range []string{
		"\n  + claimed-mem — 집음",
		"\n  ✗ rejected-mem — 거절됨",
		"\n  ○ proposed-mem — 제안됨",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q 가 응답에 없다(세 표식이 구분되지 않는다):\n%s", want, got)
		}
	}
}

// TestRenderPickStatesOverlapScopeCoversTheWholeBundle 은 finding 3 의 절반을 잠근다.
//
// 브리프가 이 줄에 준 근거는 "이 줄이 없으면 꼬리의 겹침: 줄이 선두 경로만 본
// 결과로 읽힌다"다 — 침묵 방지용 줄인데 리뷰어가 통째로 지워도 스위트가 초록이었다.
// 대조도 함께 못박는다: 묶음이 없으면 두 문구 다 안 나와야 한다(묶음 전용 문구가
// 단독 pick 에 남으면 그 자체가 새로운 오독이다).
func TestRenderPickStatesOverlapScopeCoversTheWholeBundle(t *testing.T) {
	res := service.PickResult{
		Mode: service.PickClaimed, Reason: "선점했다",
		Item:   &model.Item{ID: "lead", Title: "선두", State: model.ItemClaimed, CreatedAt: t0},
		Branch: "lead",
		Bundle: &service.BundleInfo{Members: []service.BundleMember{{
			Item: model.Item{ID: "mem", Title: "구성원", State: model.ItemOpen, CreatedAt: t0}, Claimed: true,
		}}},
	}
	got := RenderPick(res, t0)
	if !strings.Contains(got, "겹침 판정 범위: 묶음 2건의 경로를 전부 합쳐서 봤다") {
		t.Fatalf("묶음 겹침 범위 문장이 없다:\n%s", got)
	}
	if !strings.Contains(got, "묶음 선두의 id 다. 2건을 이 워크트리에서 함께 한다.") {
		t.Fatalf("브랜치가 묶음 선두라는 사실이 없다:\n%s", got)
	}

	// ★ 대조의 모양이 리뷰 라운드 2 finding 5 로 바뀌었다.
	//
	// 예전 대조는 Bundle 을 nil 로 두고 "겹침 판정 범위:" 가 **안 나와야 한다"**고
	// 했다. 그런데 nil 은 이 브랜치의 계약상 "이 응답은 그 축을 안 읽었다" 하나만
	// 뜻해야 하고, 현행 서버의 단독 선점은 축을 **읽는다**(pickExplicit 이 구성원
	// 0건짜리 BundleInfo 를 낸다). 즉 옛 대조는 실재하지 않는 응답 모양에 대고
	// 단정하고 있었고, 그 대가로 진짜 단독 응답이 겹침 범위를 침묵했다 —
	// 꼬리의 "겹침: 없음"이 이 세션 전체에 대한 판정으로 읽히는 자리다.
	//
	// 그래서 대조를 **실재하는 모양**(구성원 0건, non-nil)으로 바꾸고, 지키려던
	// 것은 그대로 지킨다: 묶음 전용 문구가 단독 pick 에 새면 안 된다.
	solo := service.PickResult{
		Mode: service.PickClaimed, Reason: "선점했다",
		Item: &model.Item{ID: "lead", Title: "선두", State: model.ItemClaimed, CreatedAt: t0}, Branch: "lead",
		Bundle: &service.BundleInfo{Reason: "이웃을 찾지 않았다", Scope: "이웃 후보를 아예 안 봤다"},
	}
	soloGot := RenderPick(solo, t0)
	// 묶음 전용 문구는 여전히 새면 안 된다 — 그것이 이 대조의 원래 목적이다.
	if strings.Contains(soloGot, "묶음 2건의 경로") || strings.Contains(soloGot, "묶음 1건의 경로") {
		t.Fatalf("구성원이 없는데 묶음 단위 겹침 문구가 나왔다:\n%s", soloGot)
	}
	if strings.Contains(soloGot, "묶음 선두의 id 다") {
		t.Fatalf("구성원이 없는데 묶음 브랜치 설명이 나왔다:\n%s", soloGot)
	}
	// 그러나 **침묵하지도 않는다**: 겹침이 무엇을 본 값인지는 말해야 한다.
	if !strings.Contains(soloGot, "겹침 판정 범위: 항목 lead 의 경로만 봤다") {
		t.Fatalf("단독 응답이 겹침 범위를 침묵한다 — 꼬리의 '겹침 없음'이 세션 전체로 읽힌다:\n%s", soloGot)
	}
}

// TestRenderPickHeaderNamesBundleSize 는 finding 3 의 나머지 절반이다 — 머리줄이
// 묶음 크기를 반영한다는 것을 **머리줄 자체**에서 못박는다. Reason 문구가
// 비슷한 말("묶음 N건 중 M건을 집었다")을 이미 담고 있어서, 머리줄 분기가
// 통째로 사라져도 그 문구 하나로 다른 시험이 계속 초록일 수 있었다
// (실측: 리뷰어가 머리줄 분기를 지워도 스위트가 안 빨개졌다). HasPrefix 로
// 정확히 첫 줄을 겨눈다.
func TestRenderPickHeaderNamesBundleSize(t *testing.T) {
	rec := RenderPick(service.PickResult{
		Mode: service.PickRecommended, Reason: "사유",
		Bundle: &service.BundleInfo{Members: []service.BundleMember{
			{Item: model.Item{ID: "a"}}, {Item: model.Item{ID: "b"}},
		}},
	}, t0)
	if !strings.HasPrefix(rec, "pick · 추천 묶음 3건 — **아직 선점하지 않았다**\n") {
		t.Fatalf("추천 머리줄이 묶음 크기를 안 말한다:\n%s", rec)
	}

	claimed := RenderPick(service.PickResult{
		Mode: service.PickClaimed, Reason: "사유",
		Bundle: &service.BundleInfo{Members: []service.BundleMember{
			{Item: model.Item{ID: "a"}, Claimed: true},
			{Item: model.Item{ID: "b"}, Rejection: &model.Rejection{Item: "b", Reason: judge.RejectClaimed}},
		}},
	}, t0)
	if !strings.HasPrefix(claimed, "pick · 선점했다 — 묶음 3건 중 2건\n") {
		t.Fatalf("선점 머리줄이 묶음 크기·집은 수를 안 말한다:\n%s", claimed)
	}
}

// TestRenderPickBundleSectionHeaderAndMemberNotes 는 finding 3 의 나머지 둘을 잠근다:
// 구성원 절 머리줄("묶음 구성원 N건")과, 집은 구성원에 실리는 연결된 판단 전문.
// 둘 다 브리프가 명시한 출력인데 어느 시험도 짚지 않고 있었다.
func TestRenderPickBundleSectionHeaderAndMemberNotes(t *testing.T) {
	res := service.PickResult{
		Mode: service.PickClaimed, Reason: "사유",
		Item: &model.Item{ID: "lead", Title: "선두", State: model.ItemClaimed, CreatedAt: t0},
		Bundle: &service.BundleInfo{Members: []service.BundleMember{{
			Item:    model.Item{ID: "mem", Title: "구성원", State: model.ItemOpen, CreatedAt: t0},
			Claimed: true,
			Notes:   []model.Judgment{{Kind: model.JudgmentDecision, At: t0, Title: "설계 노트", Body: "본문이다"}},
		}}},
	}
	got := RenderPick(res, t0)
	if !strings.Contains(got, "묶음 구성원 1건 (선두는 위의 항목이다):") {
		t.Fatalf("구성원 절 머리줄이 없다:\n%s", got)
	}
	if !strings.Contains(got, "연결된 판단 1건 (전문):") ||
		!strings.Contains(got, "설계 노트") || !strings.Contains(got, "본문이다") {
		t.Fatalf("구성원에 연결된 판단 전문이 없다:\n%s", got)
	}
}

// TestRenderPickShowsBundleEvidenceEvenWithoutAxes 는 finding 4 를 잠근다.
//
// pickBundle(item_ids 로 지정한 묶음 전체 경로)이 만드는 Link 는 Axes 가 없다 —
// 판정 없이 세션이 그대로 지정했기 때문이다(pick.go:427, Link{Detail: "세션이 함께
// 지정했다"}). len(Axes)>0 으로만 게이트를 걸면 **item_ids 로 집는 경로 전체**의
// 구성원이 "왜 묶였나" 줄을 영원히 못 낸다.
func TestRenderPickShowsBundleEvidenceEvenWithoutAxes(t *testing.T) {
	res := service.PickResult{
		Mode: service.PickClaimed, Reason: "선점했다",
		Item: &model.Item{ID: "lead", Title: "선두", State: model.ItemClaimed, CreatedAt: t0},
		Bundle: &service.BundleInfo{Members: []service.BundleMember{{
			Item:    model.Item{ID: "mem", Title: "구성원", State: model.ItemOpen, CreatedAt: t0},
			Link:    judge.Link{Item: "mem", Detail: "세션이 함께 지정했다"}, // Axes 없음
			Claimed: true,
		}}},
	}
	got := RenderPick(res, t0)
	if !strings.Contains(got, "묶은 근거: 세션이 함께 지정했다") {
		t.Fatalf("축이 없어도 근거 문장이 나와야 한다:\n%s", got)
	}
}

// synthBoard 는 세션 n개짜리 보드를 짓는다(순수 함수 시험용).
func synthBoard(n int) service.BoardView {
	v := service.BoardView{
		Project: model.Project{ID: "sample-platform", Path: "/home/a/p", DefaultBranch: "main"},
		At:      t0, Window: 8 * time.Hour,
		Derived: service.Derived{Freshness: model.Freshness{Source: "git", ObservedAt: t0}},
	}
	for i := 0; i < n; i++ {
		v.Sessions = append(v.Sessions, service.SessionCard{
			View: model.SessionView{
				Session: model.Session{
					ID:    fmt.Sprintf("01SESSION%04d", i),
					Label: fmt.Sprintf("트랙 %d — 파이프라인 색인 경로 정리와 계약 개정 반영", i),
					State: model.SessionActive,
				},
				Signals: map[model.SignalKind]time.Time{
					model.SignalPrompt: t0.Add(-time.Duration(i) * time.Minute),
					model.SignalTool:   t0.Add(-time.Duration(i) * time.Second),
				},
				Paths: []string{
					"pipeline/indexer/", "contracts/search-index/", "services/data-api/",
					"deploy/k3s/base/", "tools/staging-load-images.sh", "Makefile",
				},
				HasFootprint: true,
				Claims:       []string{fmt.Sprintf("t%d-item", i)},
				Branch:       fmt.Sprintf("t%d-item", i),
				AheadMain:    i,
			},
			BranchKnown: true, AheadKnown: true,
		})
	}
	for i := 0; i < 5; i++ {
		v.OpenItems = append(v.OpenItems, model.Item{
			ID: fmt.Sprintf("q-%d", i), Title: "열린 항목 제목", State: model.ItemOpen})
	}
	return v
}

// TestRenderBoardBudget 은 설계 §6 의 "board 기본 1,200토큰"을 단정한다.
//
// ★ 대조를 **먼저** 단정한다: detail 출력이 예산을 넘지 않으면 이 시험은
// 자르는 코드를 아예 통과하지 않은 채 초록을 낸다(빈 표에 UPDATE 를 걸어 놓고
// 트리거를 시험했다고 믿은 실패와 같은 모양이다).
func TestRenderBoardBudget(t *testing.T) {
	v := synthBoard(30)
	tail := RenderTail(TailInput{Now: t0, NotesObserved: true, OverlapsObserved: true})

	detail := RenderBoard(v, BoardRenderOptions{Detail: true, Now: t0, Tail: tail})
	if got := EstimateTokens(detail); got <= BoardTokenBudget {
		t.Fatalf("대조가 성립하지 않았다: detail 출력이 %d토큰이라 예산 %d 를 안 넘는다 — "+
			"이 입력으로는 자르는 경로가 안 돈다", got, BoardTokenBudget)
	}

	brief := RenderBoard(v, BoardRenderOptions{Now: t0, Tail: tail})
	if got := EstimateTokens(brief); got > BoardTokenBudget {
		t.Fatalf("기본 출력이 %d토큰이다 — 상한 %d\n%s", got, BoardTokenBudget, brief)
	}
	// 조용히 자르지 않는다.
	if !strings.Contains(brief, "접었다") {
		t.Fatalf("잘랐는데 잘랐다는 사실이 없다:\n%s", brief)
	}
	if !strings.Contains(brief, "detail=true") {
		t.Fatalf("전부 보는 방법이 안 적혀 있다:\n%s", brief)
	}
	// 꼬리는 예산 안에 함께 든다 — 예산 밖에 두면 이 단정이 실제 응답을 안 보게 된다.
	if !strings.Contains(brief, "── 꼬리 ──") {
		t.Fatalf("꼬리가 보드 출력에 없다:\n%s", brief)
	}

	// 세션이 적으면 자르지 않는다(상한이 상시 발동하면 판별력이 0이 된다).
	small := RenderBoard(synthBoard(2), BoardRenderOptions{Now: t0, Tail: tail})
	if strings.Contains(small, "접었다") {
		t.Fatalf("세션 2건인데 잘랐다:\n%s", small)
	}
}

// heavyTail 은 **실제로 관측된 모양의** 무거운 고정분을 만든다 — 겹침 다수 + 표류 배너.
//
// ★ TestRenderBoardBudget 이 쓰는 꼬리는 알림 0·겹침 0·배너 없음이라 두 줄짜리다.
// 그래서 그 시험은 "고정분이 크면 어떻게 되나"를 **원리적으로 못 본다.** 카드가 0장이 된
// 2026-08-05 의 실제 응답이 초록으로 지나간 이유가 그것이다. 이 헬퍼가 그 구멍을 메운다.
// countCards 는 렌더된 보드에 실제로 남은 카드 수다.
// synthBoard 의 id 는 ShortID 로 "01SESSIO…" 까지만 찍히므로 그 접두로 센다.
func countCards(rendered string) int { return strings.Count(rendered, "01SESSIO") }

func heavyTail(overlaps, twins int) string {
	var ol []judge.Overlap
	for i := 0; i < overlaps; i++ {
		ol = append(ol, judge.Overlap{
			SessionID: fmt.Sprintf("01OTHER%04d", i),
			Pairs: [][2]string{
				{".superpowers/sdd/2026-08-05-x/progress.md", ".superpowers/sdd/2026-08-05-x/progress.md"},
				{"plugins/flightdeck/server/internal/service/board.go", "plugins/flightdeck/server/internal/service/board.go"},
				{"plugins/flightdeck/server/internal/mcpsrv/render.go", "plugins/flightdeck/server/internal/mcpsrv/render.go"},
			},
		})
	}
	var tw []CoordinateTwin
	for i := 0; i < twins; i++ {
		tw = append(tw, CoordinateTwin{
			SessionID:   fmt.Sprintf("01KZ7CARD%013d", i),
			CCSessionID: fmt.Sprintf("%08d-6ca4-4321-9912-f713e791f3fe", i),
		})
	}
	return RenderTail(TailInput{
		Now: t0, NotesObserved: true, OverlapsObserved: true, Overlaps: ol,
		Banner: RenderDrift(tw, "ce5c2e79-767f-4e85-8893-52a0219f6d9a", ""),
	})
}

// TestRenderBoardKeepsCardFloorWhenFixedPartIsHuge 는 **카드 0장인 보드를 금지한다.**
//
// 2026-08-05 01:12 UTC 실측: 살아 있는 세션 34건인데 카드 0장, "34건을 접었다" 한 줄만.
// 고정분(머리·발·꼬리·배너)이 예산 1200 을 156% 먹어서 예산 루프가 첫 블록에서 즉시
// break 했다. 그 출력은 예산도 못 지키고(고정분이 이미 넘었다) 본체도 100% 잃는다.
func TestRenderBoardKeepsCardFloorWhenFixedPartIsHuge(t *testing.T) {
	// ── ① 실측된 그 모양 ── 2026-08-05 01:12 의 응답(세션 34건·겹침 16건·쌍둥이 10건)이
	// 이제 카드를 낸다. **예산 안이라고는 단정하지 않는다** — 고정분이 그만큼 크면 예산을
	// 넘기는 것이 설계된 거동이고(바닥이 예산을 이긴다), 그때 넘겼다고 말하는지를 대신 본다.
	observed := RenderBoard(synthBoard(34), BoardRenderOptions{Now: t0, Tail: heavyTail(16, 10)})
	if cards := countCards(observed); cards < 1 {
		t.Fatalf("실측된 그 입력에서 카드가 0장이다 — 카드 0장인 보드는 보드가 아니다:\n%s", observed)
	}
	// 상한 둘이 실제로 걸렸는지 — 이 두 단정이 drift·꼬리 상한의 회귀 가드다.
	if !strings.Contains(observed, "11건 더") {
		t.Fatalf("겹침 16건인데 꼬리 상한이 안 걸렸다:\n%s", observed)
	}
	if !strings.Contains(observed, "7건 더") {
		t.Fatalf("쌍둥이 10건인데 배너 상한이 안 걸렸다:\n%s", observed)
	}

	// ── ② 바닥 자체 ── 고정분이 예산을 넘기는 갈래를 직접 지난다.
	// 상한 둘이 붙은 뒤로는 현실적인 꼬리로 예산을 못 넘기므로(그것이 상한의 목적이다)
	// 예산을 좁혀서 그 갈래를 만든다 — 바닥은 예산 값과 무관한 성질이어야 한다.
	tail := heavyTail(16, 10)
	const tight = 400

	// 대조 먼저: 고정분만으로 이 예산을 넘겨야 바닥이 발동하는 갈래를 실제로 지난다.
	empty := RenderBoard(service.BoardView{
		Project: model.Project{ID: "sample-platform", DefaultBranch: "main"},
		At:      t0, Window: 8 * time.Hour,
	}, BoardRenderOptions{Now: t0, Tail: tail, Budget: tight})
	if got := EstimateTokens(empty); got <= tight {
		t.Fatalf("대조가 성립하지 않았다: 카드 0장인 고정분이 %d토큰이라 예산 %d 를 안 넘는다 — "+
			"이 입력으로는 바닥이 발동하는 갈래가 안 돈다", got, tight)
	}

	got := RenderBoard(synthBoard(34), BoardRenderOptions{Now: t0, Tail: tail, Budget: tight})

	// ★ **깨질 수 없는 계약을 리터럴로 단정한다.** 여기를 `cards < boardCardFloor` 로 쓰면
	// 시험이 자기가 지켜야 할 상수를 자기 기준으로 재게 되어, 바닥을 0 으로 만드는 변이가
	// `0 < 0`(거짓)으로 **초록을 낸다** — 실제로 그렇게 썼다가 변이 시험에서 잡혔다.
	// 계약은 "0장이면 안 된다"이고 3은 조율값이다. 둘을 따로 단정한다.
	cards := countCards(got)
	if cards < 1 {
		t.Fatalf("카드가 0장이다 — 카드 0장인 보드는 보드가 아니다:\n%s", got)
	}
	if cards < boardCardFloor {
		t.Fatalf("카드가 %d장이다 — 바닥 %d장을 못 지켰다:\n%s", cards, boardCardFloor, got)
	}
	// 예산을 넘겼다는 사실과 **넘긴 주체**를 말한다. 조용히 넘기면 계약이 거짓이 된다.
	if !strings.Contains(got, "고정분") {
		t.Fatalf("예산을 넘겼는데 무엇이 넘겼는지 안 말한다:\n%s", got)
	}
	// 접은 사실은 여전히 따로 말한다 — 원인이 둘이라 뭉치면 손댈 자리를 못 찾는다.
	if !strings.Contains(got, "접었다") {
		t.Fatalf("접었는데 접었다는 사실이 없다:\n%s", got)
	}

	// ── ③ 바닥은 예산이 넉넉할 때 **아무것도 안 바꾼다.** 상시 발동하면 판별력이 0이 된다.
	light := RenderBoard(synthBoard(34), BoardRenderOptions{Now: t0,
		Tail: RenderTail(TailInput{Now: t0, NotesObserved: true, OverlapsObserved: true})})
	if strings.Contains(light, "고정분") {
		t.Fatalf("가벼운 꼬리인데 예산 초과를 말한다 — 바닥이 상시 발동한다:\n%s", light)
	}
	if got := EstimateTokens(light); got > BoardTokenBudget {
		t.Fatalf("가벼운 꼬리인데 기본 출력이 %d토큰이다 — 상한 %d", got, BoardTokenBudget)
	}
}

// TestCardCapsItsOwnNoteLines 는 **카드 바닥의 비용**에 상한이 있는지 본다.
//
// boardCardFloor 는 예산을 이기고 카드를 남긴다. 그 카드 한 장의 크기에 상한이 없으면
// 바닥이 예산을 얼마나 넘길지도 상한이 없다 — 실측(2026-08-05): 남은 카드 3장에
// 사건 줄이 8개 붙어 예산을 531토큰 넘겼고 그 줄들이 초과분의 대부분이었다.
func TestCardCapsItsOwnNoteLines(t *testing.T) {
	const n = 6
	var asks []model.Judgment
	for i := 0; i < n; i++ {
		asks = append(asks, model.Judgment{
			Kind: model.JudgmentAsk, SessionID: "01SESSION0000", At: t0.Add(-time.Duration(i) * time.Minute),
			Title: fmt.Sprintf("요청 %d — 만질 자리 전부를 낸다", i)})
	}
	got := noteLines("01SESSION0000", asks, nil, t0)

	shown := 0
	for _, l := range got {
		if strings.Contains(l, "[ask ") {
			shown++
		}
	}
	if shown > cardNoteLimit {
		t.Fatalf("사건 줄이 %d개다 — 상한 %d\n%v", shown, cardNoteLimit, got)
	}
	if !strings.Contains(strings.Join(got, "\n"), fmt.Sprintf("%d건 더", n-cardNoteLimit)) {
		t.Fatalf("잘랐는데 몇 건을 잘랐는지 안 말한다:\n%v", got)
	}

	// 대조 — 상한 이하면 전부 나오고 "더" 줄이 안 붙는다.
	few := noteLines("01SESSION0000", asks[:1], nil, t0)
	if len(few) != 1 {
		t.Fatalf("사건 1건인데 줄이 %d개다:\n%v", len(few), few)
	}
	// 남의 사건은 애초에 안 센다 — 상한이 그 판정을 바꾸면 안 된다.
	if other := noteLines("01OTHER", asks, nil, t0); len(other) != 0 {
		t.Fatalf("남의 사건이 내 카드에 실렸다:\n%v", other)
	}
}

// TestRenderTailCapsOverlapLines 는 꼬리의 **바깥 차원**에 상한이 있는지 본다.
//
// 안쪽 차원(겹침 한 건 안의 경로쌍)은 원래 4개로 잘렸는데 겹침 **건수**는 안 잘렸다.
// 꼬리는 모든 응답에 붙고 board 에서는 고정분이라, 그 축이 세션 수에 O(N) 으로 자랐다.
func TestRenderTailCapsOverlapLines(t *testing.T) {
	const n = 16
	got := heavyTail(n, 0)

	lines := strings.Count(got, "  · 01OTHER")
	if lines > tailOverlapLimit {
		t.Fatalf("겹침 줄이 %d개다 — 상한 %d\n%s", lines, tailOverlapLimit, got)
	}
	// 자른 것을 조용히 하지 않는다. 그리고 **건수는 참값**이 나와야 한다 —
	// 상한을 건수에도 적용하면 화면이 "겹침 5건"이라 거짓말을 한다.
	if !strings.Contains(got, fmt.Sprintf("겹침 %d건", n)) {
		t.Fatalf("머리줄이 참 건수 %d 를 안 낸다:\n%s", n, got)
	}
	if !strings.Contains(got, fmt.Sprintf("%d건 더", n-tailOverlapLimit)) {
		t.Fatalf("잘랐는데 몇 건을 잘랐는지 안 말한다:\n%s", got)
	}

	// 대조 — 상한 이하면 전부 낸다.
	few := heavyTail(2, 0)
	if strings.Contains(few, "건 더") {
		t.Fatalf("겹침 2건인데 잘랐다:\n%s", few)
	}
	if c := strings.Count(few, "  · 01OTHER"); c != 2 {
		t.Fatalf("겹침 2건인데 줄이 %d개다:\n%s", c, few)
	}
}

// TestRenderBoardKeepsUnknownApartFromZero 는 0값과 "못 읽었다"를 화면에서 가른다.
func TestRenderBoardKeepsUnknownApartFromZero(t *testing.T) {
	v := synthBoard(1)
	v.Sessions[0].BranchKnown = false
	v.Sessions[0].AheadKnown = false
	v.Sessions[0].View.Paths = nil
	v.Sessions[0].View.HasFootprint = false

	got := RenderBoard(v, BoardRenderOptions{Now: t0})
	if !strings.Contains(got, "브랜치 ?(못 읽음)") {
		t.Fatalf("브랜치를 못 읽은 사실이 안 보인다:\n%s", got)
	}
	if !strings.Contains(got, "발자국 없음") || !strings.Contains(got, "아무도 안 막는다") {
		t.Fatalf("발자국 없는 세션이 아무도 안 막는다는 사실이 화면에 없다:\n%s", got)
	}

	// 대조: 읽은 경우에는 그 문구가 없어야 한다.
	ok := RenderBoard(synthBoard(1), BoardRenderOptions{Now: t0})
	if strings.Contains(ok, "못 읽음") {
		t.Fatalf("읽은 브랜치에 '못 읽음'이 붙었다:\n%s", ok)
	}
}

// TestBoardCardCarriesItsOwnAsk 는 사건이 그것을 남긴 세션의 카드에 붙는다는 것을 단정한다.
// 전역 꼬리만으로는 누가 남겼는지가 안 이어진다.
func TestBoardCardCarriesItsOwnAsk(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	v := service.BoardView{
		// 선점을 붙인다 — 화면 ①은 선점을 든 카드만 낸다. 이 시험의 의도는 필터가
		// 아니라 **사건이 자기 카드에 붙는가**이므로 카드가 나오게 해 놓고 잰다.
		Sessions: []service.SessionCard{
			{View: model.SessionView{Session: model.Session{ID: "01AAA"}, Claims: []string{"it-aaa"}}},
			{View: model.SessionView{Session: model.Session{ID: "01BBB"}, Claims: []string{"it-bbb"}}},
		},
		Asks: []model.Judgment{
			{ID: "j1", SessionID: "01AAA", At: now.Add(-12 * time.Minute),
				Title: "mcpbackend.go 를 잡는다"},
		},
	}
	got := RenderBoard(v, BoardRenderOptions{Now: now, Detail: true})

	lines := strings.Split(got, "\n")
	var aaaIdx, askIdx, bbbIdx int = -1, -1, -1
	for i, l := range lines {
		switch {
		case strings.Contains(l, "01AAA"):
			aaaIdx = i
		case strings.Contains(l, "mcpbackend.go 를 잡는다"):
			if askIdx < 0 {
				askIdx = i
			}
		case strings.Contains(l, "01BBB"):
			bbbIdx = i
		}
	}
	if askIdx < 0 {
		t.Fatalf("사건이 어디에도 없다:\n%s", got)
	}
	if !(aaaIdx < askIdx && askIdx < bbbIdx) {
		t.Fatalf("사건이 01AAA 카드 안에 없다 (aaa=%d ask=%d bbb=%d):\n%s", aaaIdx, askIdx, bbbIdx, got)
	}
	if !strings.Contains(lines[askIdx], "12분") {
		t.Fatalf("사건의 나이가 없다: %q", lines[askIdx])
	}
}

// TestFoldKeepsEventCardsOverSilentOnes 는 예산이 자를 때 **사건이 붙은 카드가 조용한 카드보다
// 먼저 남는다**는 것을 단정한다. 이것이 없으면 사건을 카드에 붙여도 예산이 그걸 먼저 버린다.
func TestFoldKeepsEventCardsOverSilentOnes(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	var sessions []service.SessionCard
	for i := 0; i < 20; i++ {
		sessions = append(sessions, service.SessionCard{
			View: model.SessionView{
				Session: model.Session{ID: fmt.Sprintf("01S%02d", i)},
				Paths:   []string{"some/long/path/that/costs/tokens.go"},
				// 선점을 붙인다 — ①이 선점을 든 카드만 내므로, 안 붙이면 이 시험이
				// 재려는 축(예산이 사건 붙은 카드를 먼저 남기나)이 아니라 필터를 재게 된다.
				Claims: []string{fmt.Sprintf("it-%02d", i)},
			},
		})
	}
	v := service.BoardView{
		Sessions: sessions,
		Asks: []model.Judgment{
			{ID: "j1", SessionID: "01S19", At: now, Title: "마지막 세션이 남긴 요청"},
		},
	}
	got := RenderBoard(v, BoardRenderOptions{Now: now, Budget: 300})

	if !strings.Contains(got, "01S19") {
		t.Fatalf("사건이 붙은 카드가 접혔다:\n%s", got)
	}
	if !strings.Contains(got, "접었다") {
		t.Fatalf("예산 300 인데 아무것도 안 접혔다:\n%s", got)
	}
}

// TestFoldAlwaysKeepsSelfFirst: 나는 언제나 첫 카드다.
func TestFoldAlwaysKeepsSelfFirst(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	var sessions []service.SessionCard
	for i := 0; i < 20; i++ {
		sessions = append(sessions, service.SessionCard{
			// ★ 자기 카드에도 선점을 준다. 규칙에 예외가 없어서(선점 없으면 자기 카드도
			// 안 낸다), 선점 없이 이 시험을 두면 "나는 언제나 첫 카드다"가 아니라
			// "나도 접힌다"를 재게 된다 — 그 축은 web 의 TestNowSectionGivesSelfNoException 이 잰다.
			View:   model.SessionView{Session: model.Session{ID: fmt.Sprintf("01S%02d", i)}, Claims: []string{fmt.Sprintf("it-%02d", i)}},
			IsSelf: i == 19,
		})
	}
	got := RenderBoard(service.BoardView{Sessions: sessions},
		BoardRenderOptions{Now: now, Self: "01S19", Budget: 300})
	if !strings.Contains(got, "01S19") {
		t.Fatalf("내 카드가 접혔다:\n%s", got)
	}
}

func TestRenderFinishAndNoteAndAdd(t *testing.T) {
	fin := RenderFinish(service.FinishResult{
		Item:      model.Item{ID: "t5-iam", State: model.ItemDone},
		Judgment:  model.Judgment{ID: "01J", Kind: model.JudgmentHandoff, Body: "본문"},
		Followups: []model.Item{{ID: "t6-next"}},
		Released:  []string{"staging"},
	})
	for _, want := range []string{"t5-iam", "done", "t6-next", "staging", "한 트랜잭션"} {
		if !strings.Contains(fin, want) {
			t.Fatalf("finish 응답에 %q 가 없다:\n%s", want, fin)
		}
	}

	note := RenderNote(service.NoteResult{
		Judgment:   model.Judgment{ID: "01K", Kind: model.JudgmentAsk, Body: "가나다"},
		Recipients: []string{"01AAAAAAAAAA", "01BBBBBBBBBB"},
	})
	if !strings.Contains(note, "2건이 읽는다") {
		t.Fatalf("이 노트를 받을 세션 수가 없다:\n%s", note)
	}
	// 0건과 "안 봤다"를 가른다.
	if got := RenderNote(service.NoteResult{Judgment: model.Judgment{ID: "x", Kind: model.JudgmentAsk}}); !strings.Contains(got, "읽을 다른 세션이 없다") {
		t.Fatalf("받을 세션 0건이 명시되지 않는다:\n%s", got)
	}

	add := RenderAdd(model.Item{ID: "t7-x", Title: "제목", State: model.ItemOpen})
	if !strings.Contains(add, "브랜치 이름이 된다: t7-x") {
		t.Fatalf("add 응답이 브랜치 이름을 안 알린다:\n%s", add)
	}
	if !strings.Contains(add, "경로 0") {
		t.Fatalf("경로 0건이 겹침 축에 안 잡힌다는 사실이 없다:\n%s", add)
	}
}

// TestBoardSaysWhatTheWindowCutOff 는 창 밖으로 잘린 것을 **침묵시키지 않는다.**
// 창은 표시 구간이지 생존 판정이 아니다(설계 §4).
//
// ★ 창 값은 3시간으로 고른다 — 지금 기본값(2h, service.DefaultLiveWindow)도
// 옛 하드코딩 값(8h, 0113b35 이전 기본값)도 아닌 제3의 값이라, 문구가 하드코딩된
// 숫자를 그대로 찍으면 **반드시** 어긋난다. v.Window 를 실제로 안 읽으면 이 시험이 잡는다.
func TestBoardSaysWhatTheWindowCutOff(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	window := 3 * time.Hour
	got := RenderBoard(service.BoardView{
		Sessions:      []service.SessionCard{{View: model.SessionView{Session: model.Session{ID: "01AAA"}}}},
		Window:        window,
		OutOfWindow:   9,
		OldestOutside: now.Add(-7 * time.Hour),
	}, BoardRenderOptions{Now: now})

	if !strings.Contains(got, "창 밖 9건") {
		t.Fatalf("창 밖 건수를 안 말한다:\n%s", got)
	}
	// ★ 어떻게 보는지는 v.Window 에서 파생돼야 한다 — 하드코딩된 숫자가 아니라.
	if !strings.Contains(got, FormatAge(window)) {
		t.Fatalf("창 밖 문구가 실제 창(%s)을 안 말한다 — 하드코딩된 값을 찍고 있을 수 있다:\n%s",
			FormatAge(window), got)
	}
	if strings.Contains(got, "8h") || strings.Contains(got, "8시간") {
		t.Fatalf("창 밖 문구에 옛 하드코딩 값(8h, 0113b35 이전 기본값)이 남아 있다:\n%s", got)
	}
	// ★ MCP board 도구는 window 인자를 받지 않는다(tools.go) — 없는 손잡이를
	//   돌리라고 하면 그 문구 자체가 결함이다(설계가 도구 수를 일곱으로 눌러 잡는다 —
	//   그 수는 protocol_test.go 의 TestToolTableIsSeven 이 잠근다).
	if strings.Contains(got, "window=") {
		t.Fatalf("존재하지 않는 window 인자를 돌리라고 한다:\n%s", got)
	}
	if strings.Contains(got, "죽") {
		t.Fatalf("생존 판정 낱말이 들어갔다 — 설계 §4 위반:\n%s", got)
	}
}

// pickWith 는 경로 실재 판정 하나를 실은 pick 결과를 만든다.
func pickWith(v *judge.ItemPathVerdict, paths []string) service.PickResult {
	item := model.Item{
		Project: "proj", ID: "t-path", Title: "제목", Body: "본문",
		Paths: paths, State: model.ItemOpen, CreatedAt: t0,
	}
	return service.PickResult{
		Mode: service.PickClaimed, Reason: "선점했다", Scope: "지정된 항목 1건",
		Item: &item, Branch: item.ID, PathCheck: v,
	}
}

func TestRenderPickNamesMisregistrationAndTheWayBack(t *testing.T) {
	got := RenderPick(pickWith(&judge.ItemPathVerdict{
		Kind: judge.KindMisregistered, Suggest: "kweiza-cc-plugins",
		Summary: "1개 전부 이 프로젝트(context-platform)에 없다 — kweiza-cc-plugins 에는 있다. 오등록일 수 있다.",
	}, []string{"plugins/x.go"}), t0)

	for _, want := range []string{
		"경로 실재:",
		"오등록일 수 있다",
		"fd move t-path --project kweiza-cc-plugins",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q 가 없다:\n%s", want, got)
		}
	}
}

// ★ V1(유일 지목 조건이 없는 규칙)의 헛발화 5건이 여기서 죽는다.
// 여럿이 지목될 때 되돌리는 명령을 내면 그것이 곧 오등록 단정이다.
func TestRenderPickDoesNotPrescribeMoveWhenAmbiguous(t *testing.T) {
	got := RenderPick(pickWith(&judge.ItemPathVerdict{
		Kind: judge.KindAmbiguous, Candidates: []string{"a", "b", "c"},
		Summary: "1개 전부 이 프로젝트에 없다. 등록된 다른 3개 프로젝트(a, b, c)에도 같은 이름이 있어 어느 하나를 지목하지 못한다 — 근거로 쓰지 않는다.",
	}, []string{"docs/"}), t0)

	if !strings.Contains(got, "지목하지 못한다") {
		t.Fatalf("모호하다는 사실이 없다:\n%s", got)
	}
	if strings.Contains(got, "fd move") {
		t.Fatalf("여럿이 지목됐는데 되돌리는 명령을 냈다 — 그것이 곧 오등록 단정이다:\n%s", got)
	}
}

func TestRenderPickStatesThePathAxisEvenWhenClean(t *testing.T) {
	got := RenderPick(pickWith(&judge.ItemPathVerdict{
		Kind: judge.KindOK, Summary: "2개 중 2개가 이 프로젝트(proj)에 있다.",
	}, []string{"a.go", "b.go"}), t0)

	if !strings.Contains(got, "경로 실재:") {
		t.Fatalf("이상이 없어도 경로 축 줄은 있어야 한다 — 침묵하면 '이상 없다'와 '안 봤다'가 같은 화면이 된다:\n%s", got)
	}
}

// nil 은 침묵이 아니다. 낡은 캐시가 "이상 없다"처럼 보이면 안 된다.
func TestRenderPickSaysTheAxisWasNotReadWhenVerdictIsNil(t *testing.T) {
	got := RenderPick(pickWith(nil, []string{"a.go"}), t0)

	if !strings.Contains(got, "읽지 않았다") {
		t.Fatalf("판정이 nil 인데 그 사실을 말하지 않는다:\n%s", got)
	}
}

// 적격 0건에는 항목이 없으므로 이 줄도 없다.
func TestRenderPickOmitsPathAxisWhenThereIsNoItem(t *testing.T) {
	got := RenderPick(service.PickResult{
		Mode: service.PickNone, Reason: "적격 항목이 0건이다", Scope: "후보 = 열린 항목 0건",
	}, t0)

	if strings.Contains(got, "경로 실재:") {
		t.Fatalf("항목이 없는데 경로 축 줄이 나왔다:\n%s", got)
	}
}

// TestRenderBoardLaneNilStaysSilent 는 v.Lane == nil(안 읽었다)일 때 레인 절 자체가
// 안 나온다는 것을 잠근다. **찍을 말이 없으면 아예 안 찍는다** — "0건"으로 지어내면
// "안 읽었다"와 "질의는 돌았는데 0건이다"가 같은 문구가 된다.
func TestRenderBoardLaneNilStaysSilent(t *testing.T) {
	got := RenderBoard(service.BoardView{
		Sessions: []service.SessionCard{{View: model.SessionView{Session: model.Session{ID: "01AAA"}}}},
	}, BoardRenderOptions{Now: t0})

	if strings.Contains(got, "레인") {
		t.Fatalf("Lane 이 nil 인데 레인 절이 찍혔다 — '안 읽었다'와 '0건'이 같은 문구가 됐다:\n%s", got)
	}
}

// TestRenderBoardLaneEmptySaysTheQueryRan 은 브리프의 핵심 요구다: Lane 이 있지만
// Entries 가 빈 것과 Lane 자체가 nil 인 것을 렌더가 **다른 문장**으로 낸다.
// 0건 문장은 "질의는 돌았다"를 반드시 말해야 한다 — 안 그러면 위 시험과 이 시험의
// 두 출력이 우연히 같아질 수 있고, 그러면 화면에서 둘이 구분 안 된다.
func TestRenderBoardLaneEmptySaysTheQueryRan(t *testing.T) {
	got := RenderBoard(service.BoardView{
		Sessions: []service.SessionCard{{View: model.SessionView{Session: model.Session{ID: "01AAA"}}}},
		Lane:     &service.LaneView{Entries: []service.LaneEntry{}},
	}, BoardRenderOptions{Now: t0})

	if !strings.Contains(got, "레인") {
		t.Fatalf("Lane 이 비었을 뿐 nil 이 아닌데 레인 절이 안 찍혔다:\n%s", got)
	}
	if !strings.Contains(got, "질의는 돌았다") {
		t.Fatalf("0건 문구가 '질의는 돌았다'를 안 말한다 — nil 과 구분이 안 된다:\n%s", got)
	}

	// 대조군: nil 일 때와 정확히 갈라야 한다.
	nilGot := RenderBoard(service.BoardView{
		Sessions: []service.SessionCard{{View: model.SessionView{Session: model.Session{ID: "01AAA"}}}},
	}, BoardRenderOptions{Now: t0})
	if got == nilGot {
		t.Fatalf("Lane==nil 출력과 Lane 빈 슬라이스 출력이 똑같다 — 두 상태가 화면에서 안 갈린다")
	}
}

// laneEntrySegment 는 렌더된 레인 절에서 세션 하나에 해당하는 항목 조각만 잘라낸다
// (`N.<세션>(행R·대기AGE전MARK)` 모양이라, marker 부터 그 항목을 닫는 `)` 까지가 경계다).
//
// 시험이 문자열 전체에 "점유" 가 있는지만 보면 그 표시가 **엉뚱한 항목에 붙어도** 통과한다 —
// 그 조각만 떼어 봐야 표시가 뒤바뀐 버그를 잡는다.
func laneEntrySegment(t *testing.T, s, marker string) string {
	t.Helper()
	i := strings.Index(s, marker)
	if i < 0 {
		t.Fatalf("표시 %q 를 렌더 결과에서 못 찾았다:\n%s", marker, s)
	}
	rest := s[i:]
	end := strings.IndexByte(rest, ')')
	if end < 0 {
		t.Fatalf("표시 %q 의 항목이 ')' 로 안 닫힌다:\n%s", marker, s)
	}
	return rest[:end+1]
}

// TestRenderBoardLaneListsEntriesAndMarksTheHolder 는 줄 항목이 실제로 나오는지,
// 그리고 지금 점유자가 어느 항목인지 표시가 갈리는지를 본다.
//
// ★ 단정을 **그 항목의 조각에** 붙인다(laneEntrySegment). 문자열 전체에 "점유" 가 있는지만
// 보면 그 표시가 대기자 쪽에 잘못 붙어도(뒤바뀐 버그) 통과한다 — 실제로 앞선 판이 그 모양의
// 시험이었다.
func TestRenderBoardLaneListsEntriesAndMarksTheHolder(t *testing.T) {
	enq := t0.Add(-90 * time.Second)
	got := RenderBoard(service.BoardView{
		Sessions: []service.SessionCard{{View: model.SessionView{Session: model.Session{ID: "01AAA"}}}},
		Lane: &service.LaneView{
			Holder: &service.LaneHolder{SessionID: "01HOLDERSESSION", AcquiredAt: t0.Add(-1 * time.Minute)},
			Entries: []service.LaneEntry{
				{RowID: 11, SessionID: "01HOLDERSESSION", EnqueuedAt: enq},
				{RowID: 12, SessionID: "01WAITERSESSION", EnqueuedAt: t0.Add(-10 * time.Second)},
			},
		},
	}, BoardRenderOptions{Now: t0})

	if !strings.Contains(got, "레인 2건") {
		t.Fatalf("레인 항목 수(2건)가 안 나온다:\n%s", got)
	}
	if !strings.Contains(got, ShortID("01HOLDERSESSION")) || !strings.Contains(got, ShortID("01WAITERSESSION")) {
		t.Fatalf("줄에 선 세션 둘이 다 안 보인다:\n%s", got)
	}

	holderSeg := laneEntrySegment(t, got, ShortID("01HOLDERSESSION"))
	waiterSeg := laneEntrySegment(t, got, ShortID("01WAITERSESSION"))

	// 점유자 쪽 조각에만 표시가 붙어야 한다 — 대기자 조각에 있으면 표시가 뒤바뀐 것이다.
	if !strings.Contains(holderSeg, "점유") {
		t.Fatalf("지금 점유자 조각에 '점유' 표시가 없다: %q\n전체:\n%s", holderSeg, got)
	}
	if strings.Contains(waiterSeg, "점유") {
		t.Fatalf("대기자 조각에 '점유' 표시가 붙었다 — 점유자·대기자 표시가 뒤바뀌었다: %q\n전체:\n%s", waiterSeg, got)
	}
}

// TestRenderBoardLaneHolderWithoutQueueRowIsNeverSilent — Entries 가 완전히 비었는데 Holder 가
// 있는 상태는 이 레포에서 가장 위험한 불변식 위반이다: landing.go 의 "살아 있는 랜딩 점유에는
// 반드시 대응하는 살아 있는 줄 행이 있다"가 깨졌다는 뜻이고, 그 축은
// TestLiveLandingHoldAlwaysHasALiveQueueRow(internal/service/landing_test.go)가 동작으로 잠근다.
//
// 0건 분기가 Holder 유무를 안 가르면 이 상태가 "비어 있음(질의는 돌았다)"으로 조용히 접힌다 —
// 정확히 이 상태에서 경고가 필요한데 그 경고 분기(l.Holder != nil && !laneHolderIsQueued(l))에
// 영원히 안 닿는다(len(l.Entries)==0 조기 반환이 앞을 막는다). 이 시험은 그 도달성을 잠근다.
//
// ★ 그리고 **회수 판정용 두 나이**(설계 §9 ①)를 여기서도 단정한다. 이 분기는 회수 판정이
// 가장 절실한 화면인데(정상 경로와 달리 항목 조각이 하나도 없어 나이를 실을 자리가 머리밖에
// 없다) 두 숫자가 둘 다 빠져 있었다. 특히 Holder.LastSignalAt 은 LandingLane 이 채워 두고도
// renderLane 이 전 함수 통틀어 한 번도 안 읽던 필드였다 —
// TestLandingQueueHasAProductionReader 가 잡으려는 "계산만 되고 읽는 쪽이 0건"의 필드 판이다.
// 신호 나이는 획득 경과와 **다른 값**을 픽스처에 심는다(45초 vs 2분): 같은 값이면 한 숫자를
// 두 번 찍어도 시험이 초록이다.
func TestRenderBoardLaneHolderWithoutQueueRowIsNeverSilent(t *testing.T) {
	ghostSignal := t0.Add(-45 * time.Second)
	got := RenderBoard(service.BoardView{
		Sessions: []service.SessionCard{{View: model.SessionView{Session: model.Session{ID: "01AAA"}}}},
		Lane: &service.LaneView{
			Holder: &service.LaneHolder{
				SessionID: "01GHOSTHOLDER", AcquiredAt: t0.Add(-2 * time.Minute),
				LastSignalAt: &ghostSignal,
			},
			Entries: []service.LaneEntry{},
		},
	}, BoardRenderOptions{Now: t0})

	if strings.Contains(got, "비어 있음") {
		t.Fatalf("점유자가 있는데 '비어 있음'으로 찍혔다 — 가장 위험한 불변식 위반이 조용히 접혔다:\n%s", got)
	}
	if !strings.Contains(got, "⚠") {
		t.Fatalf("정합 어긋남 경고가 안 찍혔다(불변식 위반인데 화면이 침묵한다):\n%s", got)
	}
	if !strings.Contains(got, ShortID("01GHOSTHOLDER")) {
		t.Fatalf("어느 세션이 점유했는지가 안 보인다 — 경고는 있는데 누구 것인지 답을 못한다:\n%s", got)
	}

	// ① 획득 경과 — 낱말은 정상 경로 머리(`(점유 획득 %s전)`)와 같아야 한다. 한 화면 안에서
	//    두 축으로 읽히면 회수 판정이 두 어휘를 대조해야 한다.
	if !strings.Contains(got, "점유 획득 2분전") {
		t.Fatalf("점유 획득 경과가 안 보인다 — 회수를 판정할 첫 숫자가 가장 절실한 화면에 없다:\n%s", got)
	}
	// ② 마지막 신호 나이 — 항목별 조각(`신호 %s전`)과 같은 낱말.
	if !strings.Contains(got, "신호 45초전") {
		t.Fatalf("점유자의 마지막 신호 나이가 안 보인다 — 채워져 있는데 읽는 쪽이 0건이다:\n%s", got)
	}

	// 신호가 한 번도 없는 점유자는 침묵이 아니라 "없음"으로 낸다(못 읽음과 없음을 가르는 규율).
	// 이 대조가 없으면 nil 갈래를 통째로 지워도 위 단정이 초록이다.
	noSignal := RenderBoard(service.BoardView{
		Sessions: []service.SessionCard{{View: model.SessionView{Session: model.Session{ID: "01AAA"}}}},
		Lane: &service.LaneView{
			Holder:  &service.LaneHolder{SessionID: "01GHOSTHOLDER", AcquiredAt: t0.Add(-2 * time.Minute)},
			Entries: []service.LaneEntry{},
		},
	}, BoardRenderOptions{Now: t0})
	if !strings.Contains(noSignal, "신호 없음") {
		t.Fatalf("신호가 없는 점유자의 그 사실이 안 보인다 — 침묵은 '못 읽었다'와 구분이 안 된다:\n%s", noSignal)
	}
}

// TestRenderBoardLaneHolderMissingFromANonEmptyQueueIsNeverSilent — **같은 불변식의 부분
// 어긋남 갈래**다: 줄에 사람은 있는데 그중 아무도 점유자가 아니다.
//
// 0건 갈래(위 시험)만 잠그고 이쪽을 비워 두면 그 비대칭이 다음 리팩터에서 잠기지 않은 쪽을
// 조용히 지운다 — 실제로 이 분기(render.go 의 `l.Holder != nil && !laneHolderIsQueued(l)`)는
// 통째로 지워도 전 시험이 초록이었다. 이 경고는 **화면이 침묵하면 사고가 안 보이는** 부류라
// 회귀가 자기 신고를 안 한다: 줄만 보면 정상으로 읽히고, 레인은 아무도 못 잡는다.
func TestRenderBoardLaneHolderMissingFromANonEmptyQueueIsNeverSilent(t *testing.T) {
	// ★ 네 나이를 **전부 다른 값**으로 심는다(획득 3분 · 점유자 신호 47분 · 대기 30초 ·
	//   대기자 신호 9초). 같은 값이 둘이면 한 숫자를 두 번 찍는 구현도 초록이 된다.
	ghostSignal := t0.Add(-47 * time.Minute)
	waiterSignal := t0.Add(-9 * time.Second)
	got := RenderBoard(service.BoardView{
		Sessions: []service.SessionCard{{View: model.SessionView{Session: model.Session{ID: "01AAA"}}}},
		Lane: &service.LaneView{
			Holder: &service.LaneHolder{
				SessionID: "01GHOSTHOLDER", AcquiredAt: t0.Add(-3 * time.Minute),
				LastSignalAt: &ghostSignal,
			},
			Entries: []service.LaneEntry{
				{RowID: 21, SessionID: "01WAITERSESSION", EnqueuedAt: t0.Add(-30 * time.Second), LastSignalAt: &waiterSignal},
			},
		},
	}, BoardRenderOptions{Now: t0})

	if !strings.Contains(got, "⚠") {
		t.Fatalf("점유자가 줄 목록에 없는데 경고가 없다 — 줄만 보면 정상으로 읽히고 레인은 아무도 못 잡는다:\n%s", got)
	}
	if !strings.Contains(got, ShortID("01GHOSTHOLDER")) {
		t.Fatalf("경고가 어느 세션의 점유인지 말하지 않는다 — 누구를 회수해야 하는지 답이 없다:\n%s", got)
	}
	// ★ 회수 판정용 **두 나이 중 뒤엣것**(설계 §9 의 절 끝 점유자 신호 문단. ① 은 신호
	//   나이를 대기 줄 항목에만 요구한다). 획득 경과는 머리에 이미 있는데
	//   점유자의 신호 나이는 이 화면 어디에도 없었다 — 항목마다 붙는 `신호 %s전` 은 줄에
	//   **있는** 세션들의 것이고, 점유자는 정의상 그 목록에 없다. 완전 어긋남 갈래는
	//   두 나이를 다 싣는데 이쪽만 안 실어서, 같은 불변식의 형제 갈래가 비대칭이었다.
	//
	//   경고 꼬리를 **통째로** 단정한다. 전체 문자열에서 "신호 47분전"만 찾으면 그 나이가
	//   엉뚱한 자리에 붙어도 통과하고, "신호 없음"류는 대기자 조각이 대신 통과시킨다.
	if want := "의 줄 행이 안 보인다(정합 어긋남) · 신호 47분전"; !strings.Contains(got, want) {
		t.Fatalf("부분 어긋남 경고에 점유자의 신호 나이가 없다 — 회수를 판정하는 사람이\n"+
			"'누구'만 듣고 '얼마나 조용한가'는 되물어야 한다.\n찾는 것: %q\n전체:\n%s", want, got)
	}

	// 신호가 한 번도 안 온 점유자는 침묵이 아니라 "없음"으로 낸다(못 읽음/없음을 가르는 규율).
	// 대기자에게는 신호를 심어 둔다 — 안 그러면 대기자 조각의 "신호 없음"이 이 단정을 대신 통과시킨다.
	noSignal := RenderBoard(service.BoardView{
		Sessions: []service.SessionCard{{View: model.SessionView{Session: model.Session{ID: "01AAA"}}}},
		Lane: &service.LaneView{
			Holder: &service.LaneHolder{SessionID: "01GHOSTHOLDER", AcquiredAt: t0.Add(-3 * time.Minute)},
			Entries: []service.LaneEntry{
				{RowID: 21, SessionID: "01WAITERSESSION", EnqueuedAt: t0.Add(-30 * time.Second), LastSignalAt: &waiterSignal},
			},
		},
	}, BoardRenderOptions{Now: t0})
	if want := "의 줄 행이 안 보인다(정합 어긋남) · 신호 없음"; !strings.Contains(noSignal, want) {
		t.Fatalf("신호가 없는 점유자의 그 사실이 부분 어긋남 경고에 안 보인다 — 빈칸은 '못 읽었다'와 구분이 안 된다.\n찾는 것: %q\n전체:\n%s", want, noSignal)
	}
	// 대조: 점유자가 줄에 **있으면** 이 경고가 나오면 안 된다(상시 발동하면 판별력이 0이 된다).
	ok := RenderBoard(service.BoardView{
		Sessions: []service.SessionCard{{View: model.SessionView{Session: model.Session{ID: "01AAA"}}}},
		Lane: &service.LaneView{
			Holder: &service.LaneHolder{SessionID: "01WAITERSESSION", AcquiredAt: t0.Add(-3 * time.Minute)},
			Entries: []service.LaneEntry{
				{RowID: 21, SessionID: "01WAITERSESSION", EnqueuedAt: t0.Add(-30 * time.Second)},
			},
		},
	}, BoardRenderOptions{Now: t0})
	if strings.Contains(ok, "⚠") {
		t.Fatalf("정합이 맞는데 어긋남 경고가 찍혔다 — 경고가 흔해지면 판별력이 0이 된다:\n%s", ok)
	}
}

// TestRenderBoardLaneShowsTheTwoAgesAHumanJudgesReclaimBy — 설계 §9 ① 이 요구하는 두 숫자:
// 점유자의 **획득 경과**와 항목마다의 **마지막 신호 나이**.
//
// ★ 이 기능은 자동 만료를 안 만들었고 그 근거가 "사람이 나이를 보고 판정한다"다. 그런데
// 판정하는 사람은 대기자가 아니라 **보드를 보는 사람**이라, 이 두 축이 보드에서 빠지면
// 그 근거가 통째로 빈다. LaneEntry.LastSignalAt 은 service 가 항목마다 질의(N+1)로
// 계산해 놓고도 보드 경로에서 읽는 쪽이 0건이었다 — 계산만 되고 안 읽히는 필드는
// 나중에 "그 축은 이미 있다"의 거짓 근거가 된다(session_workspace 함정의 필드 판).
func TestRenderBoardLaneShowsTheTwoAgesAHumanJudgesReclaimBy(t *testing.T) {
	holderSignal := t0.Add(-4 * time.Minute)
	got := RenderBoard(service.BoardView{
		Sessions: []service.SessionCard{{View: model.SessionView{Session: model.Session{ID: "01AAA"}}}},
		Lane: &service.LaneView{
			Holder: &service.LaneHolder{
				SessionID: "01HOLDERSESSION", AcquiredAt: t0.Add(-2 * time.Hour),
				LastSignalAt: &holderSignal,
			},
			Entries: []service.LaneEntry{
				{RowID: 11, SessionID: "01HOLDERSESSION", EnqueuedAt: t0.Add(-3 * time.Hour), LastSignalAt: &holderSignal},
				{RowID: 12, SessionID: "01WAITERSESSION", EnqueuedAt: t0.Add(-10 * time.Second)},
			},
		},
	}, BoardRenderOptions{Now: t0})

	// ① 점유자의 획득 경과 — 레인 줄 머리에 있다.
	if !strings.Contains(got, "획득") || !strings.Contains(got, "2시간") {
		t.Fatalf("점유자의 획득 경과가 안 보인다 — 회수를 판정할 첫 숫자가 화면에 없다:\n%s", got)
	}

	// ② 마지막 신호 나이 — **그 항목의 조각에** 붙어 있어야 한다. 문자열 전체에 있는지만
	//    보면 나이가 엉뚱한 항목에 붙어도 통과한다.
	holderSeg := laneEntrySegment(t, got, ShortID("01HOLDERSESSION"))
	if !strings.Contains(holderSeg, "신호 4분전") {
		t.Fatalf("점유자 항목에 마지막 신호 나이가 없다: %q\n전체:\n%s", holderSeg, got)
	}

	// 신호가 없는 세션은 침묵이 아니라 "없음"으로 낸다(못 읽음/없음을 가르는 규율).
	waiterSeg := laneEntrySegment(t, got, ShortID("01WAITERSESSION"))
	if !strings.Contains(waiterSeg, "신호 없음") {
		t.Fatalf("신호가 없는 대기자의 그 사실이 안 보인다: %q\n전체:\n%s", waiterSeg, got)
	}
}

// TestRenderFinishSaysWhatItLinkedInsteadOfCreated 는 "만들었다"와 "이었다"를 화면에서 가른다.
//
// 같은 줄에 담으면 응답이 "후속 2건 등록"이라고 말하는데 큐에는 1건만 는다 — 세션이
// 자기가 큐에 무엇을 했는지 못 본다(그 축이 finishBalanceLines 의 존재 이유다).
//
// ★ 단정은 **문장을 통째로** 한다. id 만 찾으면(`"spun-off-axis"` 가 어딘가 있는가) 두 줄을
//
//	합쳐 "후속 2건 등록: brand-new, spun-off-axis" 를 찍어도 초록이다 — 이 주석이 막겠다고
//	적은 바로 그 거짓말이 통과한다(사보타주 실측으로 확인했다). 그리고 맨 `"이었다"` 는 꼬리줄
//	"…한 트랜잭션이었다"(render.go:1383)에 걸려 잇기 줄이 아예 없어도 참이니 쓰지 않는다.
//
// ★ `"안 썼다"` 가 이 화면의 **정직성 관문**이다. 오늘까지 followupSchema 가 id·title·body 를
//
//	셋 다 필수로 받았으므로(tools.go:67) 돌고 있는 세션은 예외 없이 셋을 다 싣는다.
//	잇기 갈래는 그 title·body 를 읽지도 저장하지도 않는다 — followupPlan.Link 가 []string 이고,
//	store 에 항목 본문을 고치는 메서드가 아예 없다(store/item.go 전수). 게다가 이 변경
//	**전에는** 같은 입력이 "후속 N건은 안 넣었다"로 시끄럽게 나왔다(render.go:1349).
//	화면이 여기서 침묵하면 그 신호가 조용해지는 쪽으로 퇴행하고, 세션은 자기가 적어 보낸
//	본문이 어딘가 반영됐다고 믿고 떠난다 — 설계 §3 이 이름 붙인 "조용한 거짓"이다.
func TestRenderFinishSaysWhatItLinkedInsteadOfCreated(t *testing.T) {
	out := RenderFinish(service.FinishResult{
		Item:            model.Item{ID: "batch7", State: model.ItemDone},
		Judgment:        model.Judgment{ID: "j1", Kind: model.JudgmentHandoff, Body: "본문"},
		Followups:       []model.Item{{ID: "brand-new"}},
		LinkedFollowups: []string{"spun-off-axis"},
	})
	for _, want := range []string{
		"후속 1건 등록: brand-new",
		"기존 항목 1건을 후속으로 **이었다**: spun-off-axis",
		"안 썼다",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("화면에 %q 가 없다:\n%s", want, out)
		}
	}
	// 합쳐진 줄은 개수가 거짓이 된다 — 그것이 이 시험이 잠그는 회귀다.
	for _, banned := range []string{"후속 2건 등록", "후속 0건"} {
		if strings.Contains(out, banned) {
			t.Fatalf("화면에 %q 가 떴다:\n%s", banned, out)
		}
	}
	// ★ 이 브랜치의 존재 이유가 "judgment_link.target_id 에 FK 가 없다"이다(schema.sql:265).
	//   같은 화면이 "FK 로 이어졌다"고 말하면 우리가 없앤 거짓말 하나를 우리가 되살린다.
	if strings.Contains(out, "FK") {
		t.Fatalf("화면이 아직 'FK 로 이어졌다'고 말한다 — judgment_link.target_id 에는 FK 가 없다(schema.sql:265):\n%s", out)
	}
}

// TestRenderFinishSaysZeroOnlyWhenNothingWasCreatedOrLinked 는 0건 문구의 조건을 잠근다.
//
// len(Followups)==0 만 보면 잇기만 한 마무리에서 "지금 add 로 넣어라"가 떠서 방금 이은 것을
// 부정한다.
func TestRenderFinishSaysZeroOnlyWhenNothingWasCreatedOrLinked(t *testing.T) {
	linkedOnly := RenderFinish(service.FinishResult{
		Item:            model.Item{ID: "batch7", State: model.ItemDone},
		Judgment:        model.Judgment{ID: "j1", Kind: model.JudgmentHandoff, Body: "본문"},
		LinkedFollowups: []string{"spun-off-axis"},
	})
	if strings.Contains(linkedOnly, "후속 0건") {
		t.Fatalf("잇기만 한 마무리에 '후속 0건' 이 떴다:\n%s", linkedOnly)
	}
	// 0건 줄이 사라진 것만으로는 부족하다 — 잇기를 "등록"으로 찍어도 그 단정은 참이다.
	if strings.Contains(linkedOnly, "건 등록") {
		t.Fatalf("잇기만 했는데 '등록' 이 떴다(만든 적 없다):\n%s", linkedOnly)
	}
	if !strings.Contains(linkedOnly, "기존 항목 1건을 후속으로") {
		t.Fatalf("잇기 줄이 없다:\n%s", linkedOnly)
	}
	none := RenderFinish(service.FinishResult{
		Item:     model.Item{ID: "batch7", State: model.ItemDone},
		Judgment: model.Judgment{ID: "j1", Kind: model.JudgmentHandoff, Body: "본문"},
	})
	if !strings.Contains(none, "후속 0건") {
		t.Fatalf("정말 0건인데 그 줄이 없다:\n%s", none)
	}
}

// 건너뛴 후속은 **화면에 나온다.** 응답 구조체에만 있으면 세션은 못 본다.
//
// ★ 이 줄이 없으면 finish 의 흡수가 조용한 거짓이 된다 — "후속 1건 등록"만 보고
// 세션이 떠나는데, 실제로 그 id 로는 아무것도 안 들어갔다.
//
// ★ 사유를 화면 첫 줄에 박지 않는 이유: 건너뜀 갈래가 둘이다(만들 대상이 tx 사이에 생겼다 ·
// 이을 대상이 tx 사이에 사라졌다). 한쪽을 첫 줄에 박으면 다른 쪽에서 거짓이 된다.
func TestRenderFinishSaysWhichFollowupsWereSkipped(t *testing.T) {
	out := RenderFinish(service.FinishResult{
		Item:             model.Item{ID: "batch7", State: model.ItemDone},
		Judgment:         model.Judgment{ID: "j1", Kind: model.JudgmentHandoff, Body: "본문"},
		Followups:        []model.Item{{ID: "batch8"}},
		SkippedFollowups: []string{"taken-id"},
	})
	// ★ 셋을 다 단정한다. 이름과 "안 넣었다"는 **첫 줄 하나에** 있어서, 둘만 보면
	//   사유를 실은 둘째 줄이 통째로 사라져도 초록이다 — 그 사유를 잠그는 다른 시험이
	//   화면에 없으므로, 그 순간 이 축에 남는 것은 이름 하나뿐이 된다.
	//   "사유는 원장에 있다"는 갈래가 몇 개로 늘든 안 늙는 조각이라 여기 고정한다.
	for _, want := range []string{"taken-id", "안 넣었다", "사유는 원장에 있다"} {
		if !strings.Contains(out, want) {
			t.Fatalf("건너뛴 후속 화면에 %q 가 없다 — 세션은 안 들어간 것을 들어간 줄 알거나, "+
				"왜 안 들어갔는지 물을 자리를 못 찾는다:\n%s", want, out)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 전수 리뷰 — 묶음 구성원의 경로 축 · 묶음 범위 공개
// ─────────────────────────────────────────────────────────────────────────────

// bundleMemberSegment 는 렌더 결과에서 구성원 **하나의 절만** 잘라 낸다.
//
// ★ 이 헬퍼가 없으면 이 부류가 통째로 안 잡힌다. 전체 문자열에 대한
// strings.Contains 는 **출력을 넓히는 모든 변경을 통과시킨다** — 선두의 판정이
// 구성원 셋에 그대로 복사돼도, 옆 구성원 것이 붙어도 "있다"는 다 참이기 때문이다.
// 실측으로 확인한 것도 그것이다: renderPathCheck 의 인자를 Members[0] 것으로
// 바꿔도 전 스위트가 초록이었다. 그래서 구성원 머리줄("\n  <표식> <id> — ")부터
// 다음 머리줄 직전까지를 잘라, **그 안에서만** 단정한다.
func bundleMemberSegment(t *testing.T, rendered, id string) string {
	t.Helper()
	marks := []string{markClaimed, markRejected, markProposed}
	start := -1
	for _, mark := range marks {
		if i := strings.Index(rendered, "\n  "+mark+" "+id+" — "); i >= 0 {
			start = i + 1 // 자기 머리줄부터 — 앞의 개행은 버린다
			break
		}
	}
	if start < 0 {
		t.Fatalf("구성원 %s 의 머리줄이 없다:\n%s", id, rendered)
	}
	rest := rendered[start:]
	end := len(rest)
	for _, mark := range marks {
		if i := strings.Index(rest, "\n  "+mark+" "); i >= 0 && i < end {
			end = i
		}
	}
	return rest[:end]
}

// TestRenderPickGivesEachBundleMemberItsOwnPathVerdict 는 묶음 구성원의
// `경로 실재` 줄을 못박는다. 실측: 그 줄을 **통째로 지워도**, 심지어 **엉뚱한
// 항목 id 로 `fd move` 를 내도** 전 스위트가 초록이었다.
//
// ★ 왜 이 줄이 지켜져야 하나. 이 축이 내는 `fd move <id> --project X` 가
// **유일한 행동 지시**다. 오등록은 워크트리에서 띄운 세션이 자기가 어디에
// 넣는지 모른 채 add 할 때 나고(RenderAdd 의 주석이 그 실물 10건을 적는다),
// 묶음으로 집힌 구성원은 그 화면조차 안 거친다 — 이 줄이 사라지면 그 항목의
// 오등록을 사람이 알 통로가 없다.
//
// ★ 세 갈래를 **서로 다른 값**으로 깐다: 정상 · 오등록 · 축을 못 읽음.
// 값이 같으면 "선두 것이 복사됐다"와 "각자 제 것을 받았다"가 같은 화면이 되고,
// 그러면 이 시험이 지키려는 것을 정확히 못 지킨다. 오등록 구성원을 **선두가
// 아닌 자리**(Members[1])에 두는 것도 같은 이유다 — Members[0] 에 두면
// "Members[0].Item.ID 를 쓴다"는 변이가 정답과 구별되지 않는다.
func TestRenderPickGivesEachBundleMemberItsOwnPathVerdict(t *testing.T) {
	const (
		leadSum   = "선두 경로 1개 중 1개가 이 프로젝트(proj)에 있다."
		okSum     = "mem-ok 경로 2개 중 2개가 이 프로젝트(proj)에 있다."
		movedSum  = "mem-moved 경로 1개 전부 이 프로젝트에 없다 — kweiza-cc-plugins 에는 있다. 오등록일 수 있다."
		unreadSum = "이 응답은 그 축을 읽지 않았다 — 낡은 캐시이거나 서버가 이 축을 모르는 판이다."
	)
	res := service.PickResult{
		Mode: service.PickClaimed, Reason: "선점했다",
		Item:      &model.Item{Project: "proj", ID: "lead", Title: "선두", State: model.ItemClaimed, Paths: []string{"a.go"}, CreatedAt: t0},
		Branch:    "lead",
		PathCheck: &judge.ItemPathVerdict{Kind: judge.KindOK, Summary: leadSum},
		Bundle: &service.BundleInfo{
			Reason: "의존자 합 0 · 묶음 4건 · 선두 lead",
			Members: []service.BundleMember{
				{
					Item:      model.Item{Project: "proj", ID: "mem-ok", Title: "정상", State: model.ItemClaimed, Paths: []string{"b.go", "c.go"}, CreatedAt: t0},
					Claimed:   true,
					PathCheck: &judge.ItemPathVerdict{Kind: judge.KindOK, Summary: okSum},
				},
				{
					Item:    model.Item{Project: "proj", ID: "mem-moved", Title: "오등록", State: model.ItemClaimed, Paths: []string{"plugins/x.go"}, CreatedAt: t0},
					Claimed: true,
					PathCheck: &judge.ItemPathVerdict{
						Kind: judge.KindMisregistered, Suggest: "kweiza-cc-plugins", Summary: movedSum,
					},
				},
				{
					Item:      model.Item{Project: "proj", ID: "mem-unread", Title: "안 읽음", State: model.ItemOpen, CreatedAt: t0},
					Link:      judge.Link{Item: "mem-unread", Detail: "세션이 함께 지정했다"},
					PathCheck: nil, // 구서버·옛 캐시
				},
			},
		},
	}
	got := RenderPick(res, t0)

	// ① 구성원마다 한 줄씩, 빠짐없이. 선두까지 넷이다.
	// (줄 삭제 변이와 "nil 이면 건너뛴다" 변이가 여기서 죽는다 — 후자는 셋만 남긴다.)
	if n := strings.Count(got, "경로 실재: "); n != 4 {
		t.Fatalf("경로 실재 줄이 %d개다 — 선두 1 + 구성원 3 = 4여야 한다:\n%s", n, got)
	}

	// ② 각자 **제 것**이다. 자기 절 안에 자기 요약이 있고, 남의 요약은 없다.
	segs := map[string]string{
		"mem-ok":     bundleMemberSegment(t, got, "mem-ok"),
		"mem-moved":  bundleMemberSegment(t, got, "mem-moved"),
		"mem-unread": bundleMemberSegment(t, got, "mem-unread"),
	}
	own := map[string]string{"mem-ok": okSum, "mem-moved": movedSum, "mem-unread": unreadSum}
	all := []string{leadSum, okSum, movedSum, unreadSum}
	for id, seg := range segs {
		// 구성원 절 안에서는 4칸 들여쓴다 — 바로 위의 "경로: <목록>" 과 같은 깊이다.
		if !strings.Contains(seg, "\n    경로 실재: "+own[id]) {
			t.Fatalf("구성원 %s 의 절에 자기 경로 판정이 없다:\n%s\n전체:\n%s", id, seg, got)
		}
		for _, other := range all {
			if other == own[id] {
				continue
			}
			if strings.Contains(seg, other) {
				t.Fatalf("구성원 %s 의 절에 남의 경로 판정이 붙었다(%q):\n%s\n전체:\n%s", id, other, seg, got)
			}
		}
	}

	// ③ 행동 지시는 **그 구성원의 id 로** 나온다. 하나뿐이고, 오등록인 자리에만 있다.
	if !strings.Contains(segs["mem-moved"], "`fd move mem-moved --project kweiza-cc-plugins`") {
		t.Fatalf("오등록 구성원에 되돌리는 명령이 자기 id 로 안 나온다:\n%s\n전체:\n%s", segs["mem-moved"], got)
	}
	if n := strings.Count(got, "fd move "); n != 1 {
		t.Fatalf("`fd move` 가 %d번 나온다 — 오등록으로 판정된 항목 하나에만 나와야 한다:\n%s", n, got)
	}
	for _, wrong := range []string{"fd move lead", "fd move mem-ok", "fd move mem-unread"} {
		if strings.Contains(got, wrong) {
			t.Fatalf("오등록이 아닌 항목에 %q 를 지시했다 — 그것이 곧 오등록 단정이다:\n%s", wrong, got)
		}
	}
	// 되돌리는 명령도 그 구성원 아래에 딸려 들여쓴다(들여쓰기가 풀리면 선두의 지시로 읽힌다).
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "fd move ") && !strings.HasPrefix(line, "    ") {
			t.Fatalf("되돌리는 명령이 구성원 아래로 안 들어갔다: %q\n전체:\n%s", line, got)
		}
	}
}

// TestRenderPickCarriesBundleScopeWhenMembersExist 는 `묶음 범위:` 공개 줄을
// 못박는다. 실측: 구성원이 있는 갈래의 그 줄을 지워도 전 스위트가 초록이었다
// (구성원 0건 갈래는 TestRenderBundleEmptyStatesItsScope 가 이미 물고 있다).
//
// ★ 이 줄은 **"형제 축을 못 읽었다"는 고백을 나르는 유일한 통로**다. 조용히
// 사라지면 서버가 부분 관측을 전체 관측처럼 보고하게 되고, 그걸 읽은 세션은
// 이 묶음이 이웃 전부를 본 결과라고 결론짓는다 — 판정이 절반만 돌아간 날에도
// 화면이 평소와 똑같아지는, 이 저장소가 반복해서 맞은 그 실패다.
//
// 추천과 선점 **양쪽**에서 본다. 두 모드는 머리줄부터 다른 분기라, 한쪽만
// 보면 나머지 한쪽에서 이 줄이 빠져도 초록이 유지될 수 있다.
func TestRenderPickCarriesBundleScopeWhenMembersExist(t *testing.T) {
	const scope = "관찰한 후보는 전체 5건이다 · 형제 축은 이번에 못 읽었다"
	cases := []struct {
		name    string
		mode    service.PickMode
		state   model.ItemState
		claimed bool
	}{
		{"추천", service.PickRecommended, model.ItemOpen, false},
		{"선점", service.PickClaimed, model.ItemClaimed, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RenderPick(service.PickResult{
				Mode: c.mode, Reason: "사유",
				Item:   &model.Item{ID: "lead", Title: "선두", State: c.state, CreatedAt: t0},
				Branch: "lead",
				Bundle: &service.BundleInfo{
					Reason: "의존자 합 0 · 묶음 2건 · 선두 lead",
					Scope:  scope,
					Members: []service.BundleMember{{
						Item:    model.Item{ID: "mem", Title: "구성원", State: c.state, CreatedAt: t0},
						Claimed: c.claimed,
					}},
				},
			}, t0)

			// 절 자체의 줄로 나와야 한다 — Reason("왜 이 묶음인가") 뒤에 붙는 별도 줄이다.
			if !strings.Contains(got, "\n묶음 범위: "+scope+"\n") {
				t.Fatalf("구성원이 있는데 묶음 범위가 화면에 안 실린다 — 부분 관측이 전체 관측으로 읽힌다:\n%s", got)
			}
			// Reason 과 뭉개지지도 않는다: 둘은 서로 다른 사실이다(왜 묶였나 vs 무엇을 봤나).
			if !strings.Contains(got, "\n왜 이 묶음인가: 의존자 합 0 · 묶음 2건 · 선두 lead\n") {
				t.Fatalf("묶음 사유 줄이 사라졌다:\n%s", got)
			}
		})
	}
}
