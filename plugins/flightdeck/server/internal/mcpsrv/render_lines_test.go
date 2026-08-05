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

// 적대적 감사 라운드 3 — **사람이 읽는 줄이 무엇을 나르는가.**
//
// 여기 있는 시험은 전부 "전 스위트를 통과하는 변이"로 먼저 실증한 자리다.
// 좌표계는 응답 문자열이고, 단정은 **붙여서** 본다(`못 집었다: ` + 코드).
// 떨어뜨려 보면 같은 글자가 다른 줄에서 새어 나와 Contains 가 통과한다 —
// 실제로 사유 코드 "claimed" 는 선두의 상태 표기 `[claimed]` 에서 샜다.

// TestRenderPickNotesAreFullOnlyWhenHeld 는 설계 §6 의 판단 규율을 못박는다.
//
// 지정 선점·재개는 **전문**, 추천은 **제목만**이다. 두 규칙을 맞바꾸면 전 스위트가
// 초록이었다(실측). 뒤바뀌면 둘 다 깨진다: 방금 집은 세션은 앞선 판단의 본문을
// 영영 못 보고(집은 뒤 판단을 읽는 것이 pickup 절차의 전부다), 아직 안 집은 추천은
// 후보마다 본문을 게워 내 컨텍스트 예산이 후보 수에 비례해 탄다.
func TestRenderPickNotesAreFullOnlyWhenHeld(t *testing.T) {
	const body = "이 축은 따로 뺀다 — 마이그레이션과 같이 가면 되돌리기가 안 된다"
	notes := []model.Judgment{{
		Kind: model.JudgmentDecision, At: t0, Title: "쪼갠 이유", Body: body,
	}}
	render := func(mode service.PickMode, state model.ItemState) string {
		return RenderPick(service.PickResult{
			Mode: mode, Reason: "사유",
			Item:  &model.Item{ID: "lead", Title: "선두", State: state, CreatedAt: t0},
			Notes: notes,
		}, t0)
	}

	for _, mode := range []service.PickMode{service.PickClaimed, service.PickResumed} {
		got := render(mode, model.ItemClaimed)
		if !strings.Contains(got, "쪼갠 이유") {
			t.Fatalf("%s: 판단 제목이 없다:\n%s", mode, got)
		}
		if !strings.Contains(got, body) {
			t.Fatalf("%s: 쥔 항목인데 판단 본문이 없다 — 집은 뒤 판단을 읽는 것이 pickup 의 전부다:\n%s", mode, got)
		}
		if strings.Contains(got, "제목만") {
			t.Fatalf("%s: 쥔 항목에 '제목만' 약속이 붙었다:\n%s", mode, got)
		}
	}

	rec := render(service.PickRecommended, model.ItemOpen)
	if !strings.Contains(rec, "쪼갠 이유") {
		t.Fatalf("추천에 판단 제목이 없다:\n%s", rec)
	}
	if strings.Contains(rec, body) {
		t.Fatalf("추천이 판단 본문을 실었다 — 후보마다 컨텍스트를 태운다:\n%s", rec)
	}
	if !strings.Contains(rec, "제목만") {
		t.Fatalf("추천이 '지금은 제목뿐'이라는 사실을 안 말한다 — 본문이 없는 것이 누락으로 읽힌다:\n%s", rec)
	}
}

// TestRenderPickUnclaimedMemberNamesItsReasonCode 는 못 집은 구성원의 **사유 코드**를
// 그 줄에 붙여서 못박는다.
//
// 실측: `못 집었다:` 줄에서 코드를 빼도 전 스위트가 초록이었다. 기존 단정이
// `strings.Contains(got, judge.RejectClaimed)` 였는데, 그 글자는 선두 머리줄의
// 상태 표기 `[claimed]` 에서 새어 나와 코드가 사라져도 통과했다.
//
// ★ 이 코드가 없으면 무엇이 깨지나. 사유는 세션의 **다음 행동을 가르는 축**이다:
// claimed 면 그 세션에게 물어야 하고(note ask), after-unmet-item 이면 선행이 끝나기를
// 기다려야 하며, closed 면 애초에 지정이 틀린 것이다. Detail 은 사람 말이라 판마다
// 문장이 바뀌지만 코드는 pick_eval 분포로 집계되는 값이다 — 코드가 화면에서 빠지면
// 셋이 "못 집었다" 하나로 뭉개진다.
func TestRenderPickUnclaimedMemberNamesItsReasonCode(t *testing.T) {
	got := RenderPick(service.PickResult{
		Mode: service.PickClaimed, Reason: "선두를 선점했다",
		Item:   &model.Item{ID: "lead", Title: "선두", State: model.ItemClaimed, CreatedAt: t0},
		Branch: "lead",
		Bundle: &service.BundleInfo{Members: []service.BundleMember{
			{
				Item:      model.Item{ID: "mem-held", Title: "남이 쥠", CreatedAt: t0},
				Rejection: &model.Rejection{Item: "mem-held", Reason: judge.RejectClaimed, Detail: "세션 S2 가 선점했다"},
			},
			{
				Item:      model.Item{ID: "mem-after", Title: "선행 미충족", CreatedAt: t0},
				Rejection: &model.Rejection{Item: "mem-after", Reason: judge.AfterUnmetItem, Detail: "선행 t3-z 가 안 끝났다"},
			},
		}},
	}, t0)

	// ① 코드는 `못 집었다:` **바로 뒤**에 온다. 떨어뜨려 보면 다른 줄의 같은 글자가 통과시킨다.
	// ② 그리고 구성원마다 **제 코드**다 — 하나를 상수로 박는 변이가 여기서 죽는다.
	for id, code := range map[string]string{"mem-held": judge.RejectClaimed, "mem-after": judge.AfterUnmetItem} {
		seg := bundleMemberSegment(t, got, id)
		if !strings.Contains(seg, "못 집었다: "+code) {
			t.Fatalf("구성원 %s 의 사유 코드 %q 가 그 줄에 없다:\n%s\n전체:\n%s", id, code, seg, got)
		}
	}
	if strings.Contains(bundleMemberSegment(t, got, "mem-held"), judge.AfterUnmetItem) {
		t.Fatalf("구성원 절에 남의 사유 코드가 붙었다:\n%s", got)
	}
}

// TestRenderBundleSoloKeepsReasonAndScopeApart 는 구성원 0건 갈래의 두 줄이
// **서로 바뀌지 않는지**를 본다. 구성원이 있는 갈래는
// TestRenderPickCarriesBundleScopeWhenMembersExist 가 이미 물고 있는데, 0건 갈래는
// 범위 문장이 응답 어딘가에 있기만 하면 통과해서 둘을 맞바꿔도 초록이었다(실측).
//
// ★ 뒤바뀌면 무엇이 깨지나. 두 줄이 답하는 질문이 다르다 — Reason 은 "왜 이것이
// 선두인가"(정렬 네 키의 값), Scope 는 "이웃을 무엇까지 봤나"다. 구성원 0건이
// 나오는 갈래가 둘인데(찾아봤는데 없다 · 애초에 안 찾았다) 그것을 가르는 유일한
// 통로가 Scope 다. 자리가 바뀌면 `묶음 범위: 의존자 합 0 · 묶음 1건` 이 찍히고,
// 그것을 읽은 세션은 "형제 축은 이번에 못 읽었다"는 고백을 못 본 채 이 항목에
// 형제가 없다고 결론짓는다.
func TestRenderBundleSoloKeepsReasonAndScopeApart(t *testing.T) {
	const (
		reason = "의존자 합 0 · 묶음 1건 · 선두 solo"
		scope  = "관찰한 후보는 전체 5건이다 · 형제 축은 이번에 못 읽었다"
	)
	got := RenderPick(service.PickResult{
		Mode: service.PickRecommended, Reason: "사유",
		Item:   &model.Item{ID: "solo", Title: "단독", State: model.ItemOpen, CreatedAt: t0},
		Bundle: &service.BundleInfo{Reason: reason, Scope: scope},
	}, t0)

	if !strings.Contains(got, "\n  묶음 범위: "+scope+"\n") {
		t.Fatalf("범위 문장이 `묶음 범위:` 줄에 안 실렸다:\n%s", got)
	}
	if !strings.Contains(got, "\n  "+reason+"\n") {
		t.Fatalf("묶음 사유가 제 줄에 없다:\n%s", got)
	}
	if strings.Contains(got, "묶음 범위: "+reason) {
		t.Fatalf("사유가 범위 자리에 찍혔다 — 안 본 것을 봤다고 말하게 된다:\n%s", got)
	}
}

// TestRenderFailuresNamesTheAxisAndItsCause 는 파생 실패 절이 **목록을 나르는지**를 본다.
//
// 실측: 머리줄(`못 읽은 파생 N축:`)만 남기고 축별 줄을 통째로 지워도 전 스위트가
// 초록이었다. 수는 남고 내용이 사라지는 모양이라 화면만 보면 정상으로 보인다.
//
// ★ 무엇이 깨지나. 이 절은 "무엇을 못 읽었나"에 답하는 유일한 자리다. 수만 남으면
// 세션은 git 을 못 읽은 것인지 형제 축을 못 읽은 것인지 못 가르고, 그러면 **어느
// 침묵을 값 0 으로 읽으면 안 되는지**도 모른 채 진행한다. 이 저장소가 파생 축마다
// 부재와 0 을 갈라 놓은 이유가 통째로 무의미해진다.
//
// 구분자(`  · ` · ` — `)는 일부러 안 못박는다 — 축 이름과 원인이 **같은 줄에**
// 있는지만 본다. 표시 형식까지 박으면 문구를 다듬을 때마다 이 시험이 붉어진다.
func TestRenderFailuresNamesTheAxisAndItsCause(t *testing.T) {
	want := map[string]string{
		"git-head": "fatal: not a git repository",
		"sibling":  "판단 링크 조회 실패: context deadline exceeded",
	}
	var fs []service.DerivedFailure
	for axis, detail := range want {
		fs = append(fs, service.DerivedFailure{Axis: axis, Detail: detail})
	}
	got := RenderPick(service.PickResult{
		Mode: service.PickNone, Reason: "적격 0건이다",
		Derived: service.Derived{Failures: fs},
	}, t0)

	if !strings.Contains(got, "못 읽은 파생 2축:") {
		t.Fatalf("실패 축 수를 안 말한다:\n%s", got)
	}
	for axis, detail := range want {
		if !sameLine(got, axis, detail) {
			t.Fatalf("축 %q 와 그 원인 %q 가 같은 줄에 없다 — 수만 남고 무엇을 못 읽었는지가 사라졌다:\n%s",
				axis, detail, got)
		}
	}
}

// sameLine 은 두 조각이 **한 줄 안에** 같이 있는지다. 구분자·여백에 안 묶이려고 쓴다.
func sameLine(rendered, a, b string) bool {
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, a) && strings.Contains(line, b) {
			return true
		}
	}
	return false
}

// TestRenderPickClaimTimeIsTheObservedOne 은 `선점 시각` 줄이 **실제 값을 나르는지**를 본다.
//
// 실측: 그 줄을 통째로 상수 문자열로 바꿔도 전 스위트가 초록이었다 — 이 줄을 짚는
// 렌더 시험이 하나도 없었다.
//
// ★ 무엇이 깨지나. 이 줄은 "이 선점이 언제부터인가"에 답하는 유일한 자리이고,
// 회수 판단(오래 쥔 채 신호가 없는 항목을 남이 가져갈지)의 입력이다. 상수로 굳으면
// 사흘 묵은 선점이 방금 집은 것처럼 보인다 — 화면은 늘 정상이고, 회수는 영영 안 일어난다.
//
// 나이는 **now 를 옮겨서** 본다. 한 시점만 보면 상수와 구분이 안 된다.
func TestRenderPickClaimTimeIsTheObservedOne(t *testing.T) {
	at := t0.Add(-26 * time.Hour)
	res := service.PickResult{
		Mode: service.PickResumed, Reason: "재개다",
		Item:  &model.Item{ID: "lead", Title: "선두", State: model.ItemClaimed, CreatedAt: t0},
		Claim: &model.Claim{ItemID: "lead", At: at},
	}

	got := RenderPick(res, t0)
	if !sameLine(got, "선점 시각", at.UTC().Format("2006-01-02 15:04")) {
		t.Fatalf("선점 시각이 실제 선점 시각이 아니다(기대 %s):\n%s", at.UTC(), got)
	}
	if !sameLine(got, "선점 시각", FormatAge(26*time.Hour)) {
		t.Fatalf("선점 나이가 %q 가 아니다:\n%s", FormatAge(26*time.Hour), got)
	}

	// 시계가 이틀 흐르면 나이도 따라 움직인다 — 상수로 박힌 줄이 여기서 죽는다.
	later := RenderPick(res, t0.Add(48*time.Hour))
	if !sameLine(later, "선점 시각", FormatAge(26*time.Hour+48*time.Hour)) {
		t.Fatalf("시계가 흘렀는데 선점 나이가 안 움직인다:\n%s", later)
	}
	if sameLine(later, "선점 시각", FormatAge(26*time.Hour)) {
		t.Fatalf("옛 나이가 그대로 남았다 — 이 줄은 now 를 안 본다:\n%s", later)
	}
}

// TestRenderBundleMemberCountMatchesTheMembers 는 구성원 절 머리줄의 **수**를 못박는다.
//
// 실측: 그 수를 상수 1 로 박아도 전 스위트가 초록이었다 — 이 머리줄을 짚는 시험
// (TestRenderPickBundleSectionHeaderAndMemberNotes)이 구성원을 하나만 넣어서,
// 상수 1 과 진짜 1 이 구분되지 않았다. 그래서 여기서는 **셋**을 넣는다.
//
// ★ 무엇이 깨지나. 이 수는 "이 워크트리에 몇 건이 얹혀 가나"의 요약이고, 절을 끝까지
// 안 읽는 세션이 실제로 보는 유일한 수다. 3건짜리 묶음이 "구성원 1건"으로 보이면
// 나머지 둘은 집혔는데도 아무도 손대지 않는다 — 묶음이 있으나 마나가 된다.
func TestRenderBundleMemberCountMatchesTheMembers(t *testing.T) {
	got := RenderPick(bundleResult(service.PickClaimed,
		[]string{"m-a", "m-b", "m-c"}, []bool{true, false, true}), t0)

	if !strings.Contains(got, "묶음 구성원 3건 (선두는 위의 항목이다):") {
		t.Fatalf("구성원 절 머리줄이 실제 구성원 수를 안 말한다:\n%s", got)
	}
	// 머리줄의 수와 **실제로 찍힌 구성원 머리줄 수**가 같아야 한다 — 한쪽만 보면
	// "수는 맞는데 절이 잘렸다"와 "절은 맞는데 수가 틀렸다"를 못 가른다.
	n := strings.Count(got, "\n  "+markClaimed+" ") +
		strings.Count(got, "\n  "+markRejected+" ") +
		strings.Count(got, "\n  "+markProposed+" ")
	if n != 3 {
		t.Fatalf("구성원 머리줄이 %d개다 — 머리줄이 말한 3건과 다르다:\n%s", n, got)
	}
}

// TestRenderPickNamesEveryMemberOutsideTheOverlapScope 는 판정 밖 구성원을
// **하나도 빠짐없이** 이름으로 부르는지 본다.
//
// 실측: 이름 목록을 첫 하나로 잘라도 전 스위트가 초록이었다 — 기존 시험이 안 집은
// 구성원을 하나만 두어서, 목록과 첫 원소가 구분되지 않았다. 그래서 여기서는 **둘**을 만든다.
//
// ★ 무엇이 깨지나. 이 줄의 존재 이유가 "겹침 0건을 묶음 전체에 적용하지 마라"이고,
// 그 경고는 **어느 항목인지**가 있어야 쓸 수 있다. 둘 중 하나만 불리면 나머지 하나는
// 화면 어디에도 안 남고, 그 항목의 경로는 관측된 적 없이 안전으로 읽힌다.
func TestRenderPickNamesEveryMemberOutsideTheOverlapScope(t *testing.T) {
	got := RenderPick(bundleResult(service.PickClaimed,
		[]string{"m-a", "m-b", "m-c"}, []bool{true, false, false}), t0)

	if !strings.Contains(got, "안 집은 구성원 2건") {
		t.Fatalf("판정 밖 구성원 수가 2건이 아니다:\n%s", got)
	}
	for _, id := range []string{"m-b", "m-c"} {
		if !sameLine(got, "안 집은 구성원", id) {
			t.Fatalf("판정 밖 구성원 %s 가 그 줄에서 이름으로 안 불린다 — 관측 안 한 경로가 안전으로 읽힌다:\n%s", id, got)
		}
	}
}

// TestRenderPickResumedHeaderKeepsRequestedApartFromHeld 는 재개 머리줄의 **두 수**를
// 가른다. 기존 시험(TestRenderPickResumedHeaderNamesBundle)은 3건 중 3건 — 둘이 같은
// 경우만 봐서, `n` 과 `heldN` 을 맞바꿔도 글자가 하나도 안 바뀌었다(실측: 초록).
//
// ★ 무엇이 깨지나. 재개는 세션이 브랜치로 돌아왔을 때 "내가 지금 몇 건을 쥐고 있나"를
// 읽는 첫 줄이다. 뒤바뀌면 3건 중 2건을 쥔 세션이 "2건 중 3건" 을 보고 못 쥔 하나까지
// 자기 것으로 여긴다 — 그 항목은 남이 쥐고 있고, 결국 남의 항목을 자기 브랜치에서 고친다.
func TestRenderPickResumedHeaderKeepsRequestedApartFromHeld(t *testing.T) {
	got := RenderPick(bundleResult(service.PickResumed,
		[]string{"m-a", "m-b"}, []bool{true, false}), t0)
	first := got[:strings.IndexByte(got, '\n')]
	if !strings.Contains(first, "묶음 3건 중 2건을 쥐고 있다") {
		t.Fatalf("재개 머리줄이 요청 규모와 쥔 수를 안 가른다: %q\n%s", first, got)
	}
}

// TestRenderPickKeepsReasonApartFromScope 는 머리줄 밑의 두 줄이 **서로 바뀌지
// 않는지**를 본다. 실측: `사유:` 와 `범위:` 의 값을 맞바꿔도 전 스위트가 초록이었다 —
// 두 값이 응답 어딘가에 있기만 하면 통과하는 단정뿐이었기 때문이다.
//
// ★ 무엇이 깨지나. 둘은 다른 질문에 답한다 — Reason 은 "왜 이것인가 · 왜 못 골랐나",
// Scope 는 "무엇을 후보로 봤나"다. 적격 0건 응답에서 Reason 은 큐가 왜 비었는지를
// 말하는 **유일한 줄**이고, 그 자리에 후보 범위가 찍히면 세션은 "선행이 안 끝났다"와
// "전부 남이 쥐었다"를 구분할 근거를 잃는다. 게다가 Scope 가 빈 응답에서는
// 뒤바뀐 순간 Reason 이 화면에서 통째로 사라진다(범위 줄은 Scope 가 비면 안 찍힌다).
func TestRenderPickKeepsReasonApartFromScope(t *testing.T) {
	const (
		reason = "적격 0건이다 — 열린 항목 셋 다 선행이 안 끝났다"
		scope  = "후보 = 열린 항목 3건"
	)
	got := RenderPick(service.PickResult{
		Mode: service.PickNone, Reason: reason, Scope: scope,
	}, t0)

	if !strings.Contains(got, "\n사유: "+reason+"\n") {
		t.Fatalf("사유 줄이 사유를 안 나른다:\n%s", got)
	}
	if !strings.Contains(got, "\n범위: "+scope+"\n") {
		t.Fatalf("범위 줄이 범위를 안 나른다:\n%s", got)
	}

	// Scope 가 빈 응답에서도 사유는 반드시 남는다 — 뒤바뀐 코드는 여기서 사유를 잃는다.
	noScope := RenderPick(service.PickResult{Mode: service.PickNone, Reason: reason}, t0)
	if !strings.Contains(noScope, "\n사유: "+reason+"\n") {
		t.Fatalf("범위가 없는 응답에서 사유가 사라졌다:\n%s", noScope)
	}
}

// TestRenderPickCarriesEveryWorktreeCommand 는 워크트리 준비 명령을 **하나도 빠뜨리지
// 않는지** 본다. 실측: 마지막 명령을 빼도 전 스위트가 초록이었다 — 기존 단정이
// `git worktree add …` 한 줄만 짚고 있었다.
//
// ★ 무엇이 깨지나. 이 목록의 소비자는 사람이 아니라 **에이전트의 Bash 도구**이고,
// 마지막 줄이 새 워크트리로 들어가는 `cd` 다. 그것이 빠지면 앞의 둘만 실행한 세션이
// 저장소 루트에 그대로 서 있게 되고, 그 뒤의 모든 편집·커밋이 사람이 쓰는 트리에서
// 일어난다 — 워크트리로 일을 여는 규율 전체가 조용히 무효가 된다.
func TestRenderPickCarriesEveryWorktreeCommand(t *testing.T) {
	setup := service.SetupCommands("/home/a/proj", "main", "t5-iam")
	if len(setup) == 0 {
		t.Fatal("전제 실패 — 준비 명령이 비었다")
	}
	got := RenderPick(service.PickResult{
		Mode: service.PickClaimed, Reason: "선점했다",
		Item:   &model.Item{Project: "proj", ID: "t5-iam", Title: "제목", State: model.ItemClaimed, CreatedAt: t0},
		Branch: "t5-iam", Setup: setup,
	}, t0)

	for i, c := range setup {
		if !strings.Contains(got, c) {
			t.Fatalf("준비 명령 %d/%d 가 응답에 없다: %q\n%s", i+1, len(setup), c, got)
		}
	}
}

// TestRenderPickItemHeadCarriesItsState 는 항목 머리줄이 **그 항목의 상태**를 나르는지
// 본다. 실측: 상태 자리를 상수 "open" 으로 박아도 전 스위트가 초록이었다.
//
// ★ 무엇이 깨지나. 상태는 "이것이 지금 무엇인가"(열림·선점·끝남)이고, 재개 응답에서
// 세션이 자기 선점을 확인하는 자리이기도 하다. 상수 open 이면 이미 선점된 항목이
// 열린 항목으로 보이고, 그걸 읽은 세션은 남이 쥔 것을 집을 수 있다고 판단한다 —
// 머리줄은 응답에서 사람이 가장 먼저 읽는 줄이라 뒤의 어떤 줄도 이 오독을 못 되돌린다.
func TestRenderPickItemHeadCarriesItsState(t *testing.T) {
	cases := []struct {
		mode  service.PickMode
		state model.ItemState
	}{
		{service.PickRecommended, model.ItemOpen},
		{service.PickClaimed, model.ItemClaimed},
	}
	for _, c := range cases {
		got := RenderPick(service.PickResult{
			Mode: c.mode, Reason: "사유",
			Item: &model.Item{ID: "lead", Title: "선두", State: c.state, CreatedAt: t0},
		}, t0)

		var head string
		for _, line := range strings.Split(got, "\n") {
			if strings.HasPrefix(line, "▸ ") {
				head = line
				break
			}
		}
		if head == "" {
			t.Fatalf("%s: 항목 머리줄이 없다:\n%s", c.mode, got)
		}
		if !strings.Contains(head, "lead") || !strings.Contains(head, string(c.state)) {
			t.Fatalf("%s: 머리줄 %q 가 id·상태(%s)를 안 나른다:\n%s", c.mode, head, c.state, got)
		}
	}
}

// TestRenderPickCarriesEveryAfterWithItsKind 는 `선행:` 줄을 못박는다. 실측: 그 줄을
// 통째로 지워도 전 스위트가 초록이었다 — formatAfter 를 짚는 시험이 하나도 없었다.
//
// ★ 무엇이 깨지나. 선행은 종류마다 **기다리는 대상이 다르다**: item 은 다른 항목이
// 끝나기를, sha 는 그 커밋이 랜딩하기를, job 은 그 작업이 끝나기를 기다린다. 줄이
// 사라지면 세션은 자기가 무엇을 기다리는지 모른 채 시작하고, 종류 표시가 뭉개지면
// (item 을 sha 로) 엉뚱한 곳을 들여다본다. 빈 선행이 스키마를 우회해 들어온 경우까지
// 이 줄이 말하게 되어 있다 — 침묵하면 그 사고가 화면에서 지워진다.
func TestRenderPickCarriesEveryAfterWithItsKind(t *testing.T) {
	got := RenderPick(service.PickResult{
		Mode: service.PickClaimed, Reason: "선점했다",
		Item: &model.Item{ID: "lead", Title: "선두", State: model.ItemClaimed, CreatedAt: t0,
			After: []model.After{{Item: "t3-z"}, {SHA: "47421b4"}, {Job: "migrate-7"}}},
	}, t0)

	for _, want := range []string{"item:t3-z", "sha:47421b4", "job:migrate-7"} {
		if !sameLine(got, "선행:", want) {
			t.Fatalf("선행 %q 가 선행 줄에 없다:\n%s", want, got)
		}
	}
}

// TestFormatFreshnessCarriesStateSourceAndFailureCount 는 꼬리 한 줄이 나르는 값 셋을
// 각각 못박는다. 실측: 최신/낡음을 맞바꿔도, 출처를 상수 "git" 으로 박아도, 못 읽은
// 축 수를 상수로 박아도 전 스위트가 초록이었다(mcpsrv 쪽 단정이 하나도 없었다 —
// internal/web 의 같은 이름 함수만 시험이 있었다).
//
// ★ 무엇이 깨지나. 이 줄은 "지금 보고 있는 것이 언제·어디서 온 값인가"에 답한다.
// 낡음/최신이 뒤집히면 서버가 죽어 굳은 화면이 현재 사실인 척하고 — 이 축이 존재하는
// 이유가 정확히 그것을 막는 것이다 — 출처가 상수면 db 재생(파생 실패)이 git 실측으로
// 보인다. 못 읽은 축 수가 상수면 "몇 개를 못 봤나"가 사라진다.
func TestFormatFreshnessCarriesStateSourceAndFailureCount(t *testing.T) {
	obs := t0.Add(-3 * time.Minute)

	fresh := FormatFreshness(service.Derived{
		Freshness: model.Freshness{Source: "git", ObservedAt: obs},
	})
	if !strings.Contains(fresh, "최신") || strings.Contains(fresh, "낡음") {
		t.Fatalf("낡지 않은 파생을 낡음으로 말한다: %q", fresh)
	}
	if !strings.Contains(fresh, "git") {
		t.Fatalf("출처가 없다: %q", fresh)
	}
	if strings.Contains(fresh, "못 읽은 축") {
		t.Fatalf("실패가 없는데 못 읽은 축을 말한다: %q", fresh)
	}

	stale := FormatFreshness(service.Derived{
		Freshness: model.Freshness{Source: "db", ObservedAt: obs, Stale: true},
		Failures: []service.DerivedFailure{
			{Axis: "git-head", Detail: "x"}, {Axis: "sibling", Detail: "y"},
		},
	})
	if !strings.Contains(stale, "낡음") || strings.Contains(stale, "최신") {
		t.Fatalf("낡은 파생을 최신으로 말한다 — 굳은 화면이 현재 사실인 척한다: %q", stale)
	}
	if !strings.Contains(stale, "db") || strings.Contains(stale, "git@") {
		t.Fatalf("출처가 db 인데 그렇게 안 말한다: %q", stale)
	}
	if !strings.Contains(stale, "못 읽은 축 2개") {
		t.Fatalf("못 읽은 축 수가 실제 실패 수와 다르다: %q", stale)
	}
}

// TestRenderFailuresSaysHowManyItCutOff 는 잘린 축 수를 못박는다. 실측: `… N축 더`
// 줄을 지워도 전 스위트가 초록이었다.
//
// ★ 무엇이 깨지나. 목록에 상한이 있다는 것을 화면이 말하지 않으면, 여섯 줄이 전부인
// 응답과 스물을 못 읽은 응답이 같은 모양이 된다 — 안 보인 것을 침묵하지 않는다는
// 규율이 정확히 이 줄이다.
func TestRenderFailuresSaysHowManyItCutOff(t *testing.T) {
	var fs []service.DerivedFailure
	for i := 0; i < 9; i++ {
		fs = append(fs, service.DerivedFailure{Axis: fmt.Sprintf("축-%d", i), Detail: "원인"})
	}
	got := RenderPick(service.PickResult{
		Mode: service.PickNone, Reason: "적격 0건이다",
		Derived: service.Derived{Failures: fs},
	}, t0)

	if !strings.Contains(got, "못 읽은 파생 9축:") {
		t.Fatalf("실패 축 수가 9가 아니다:\n%s", got)
	}
	if !strings.Contains(got, "3축 더") {
		t.Fatalf("잘라 낸 3축을 침묵한다 — 여섯이 전부인 응답과 같은 화면이 된다:\n%s", got)
	}
}

// TestRenderPickCarriesEveryPathOfLeadAndMembers 는 경로 목록을 못박는다.
// 실측: 선두의 `경로:` 줄도, 구성원의 `경로:` 줄도 각각 지워도 초록이었다.
//
// ★ 무엇이 깨지나. 경로는 겹침 판정의 **입력**이고, 세션이 "내가 만질 파일이 이것"을
// 확인하는 유일한 자리다. 사라지면 겹침 0건이 무엇에 대한 0건인지 알 수 없고,
// 오등록(다른 프로젝트의 경로)도 눈으로 못 잡는다.
func TestRenderPickCarriesEveryPathOfLeadAndMembers(t *testing.T) {
	res := bundleResult(service.PickClaimed, []string{"m-a"}, []bool{true})
	res.Bundle.Members[0].Item.Paths = []string{"services/api/x.go", "services/api/y.go"}
	got := RenderPick(res, t0)

	if !sameLine(got, "경로:", "lead.go") {
		t.Fatalf("선두 경로가 없다:\n%s", got)
	}
	for _, p := range []string{"services/api/x.go", "services/api/y.go"} {
		if !sameLine(got, "경로:", p) {
			t.Fatalf("구성원 경로 %q 가 없다:\n%s", p, got)
		}
	}
}

// TestRenderPickNoteCountsMatchTheNotes 는 판단 절 머리줄의 **수**를 두 자리에서
// 못박는다(선두 절 · 구성원 절). 실측: 둘 다 상수 1 로 박아도 초록이었다 — 기존
// 시험이 판단을 하나만 넣어 상수 1 과 진짜 1 이 구분되지 않았다. 그래서 **둘**을 넣는다.
//
// ★ 무엇이 깨지나. 이 수는 "이 항목에 달린 앞선 판단이 몇 건인가"이고, 절을 끝까지
// 안 읽는 세션이 보는 유일한 요약이다. 늘 1 로 보이면 둘째 판단부터는 존재 자체가
// 화면에서 사라진 것과 같다 — 되돌리기 비싼 결정이 바로 그 자리에 쌓인다.
func TestRenderPickNoteCountsMatchTheNotes(t *testing.T) {
	notes := []model.Judgment{
		{Kind: model.JudgmentDecision, At: t0, Title: "첫 판단", Body: "본문 하나"},
		{Kind: model.JudgmentHandoff, At: t0, Title: "둘째 판단", Body: "본문 둘"},
	}
	res := bundleResult(service.PickClaimed, []string{"m-a"}, []bool{true})
	res.Notes = notes
	res.Bundle.Members[0].Notes = notes
	got := RenderPick(res, t0)

	if !strings.Contains(got, "\n연결된 판단 2건 (전문):") {
		t.Fatalf("선두 판단 절이 2건을 안 말한다:\n%s", got)
	}
	if !strings.Contains(got, "    연결된 판단 2건 (전문):") {
		t.Fatalf("구성원 판단 절이 2건을 안 말한다:\n%s", got)
	}
	for _, want := range []string{"첫 판단", "둘째 판단", "본문 하나", "본문 둘"} {
		if !strings.Contains(got, want) {
			t.Fatalf("판단 %q 가 응답에 없다:\n%s", want, got)
		}
	}
}

// TestRenderPickUnclaimedMemberTellsWhatToDoNext 는 못 집은 구성원 뒤의 **행동 지시**를
// 못박는다. 그 두 줄을 지워도 전 스위트가 초록이었다(실측).
//
// ★ 안 그러면 무엇이 깨지나. 사유 코드는 "왜 못 집었나"에만 답한다 — 세션이 다음에
// 무엇을 해야 하는지는 이 줄에만 있다. 묶음의 값은 "선두는 원자, 구성원은 최선 노력"인데,
// 그 규율은 **못 집은 구성원을 두고 진행해도 된다는 것을 세션이 알 때만** 성립한다.
// 이 줄이 사라지면 세션은 실패한 구성원 앞에서 멈추거나(묶음이 사실상 원자가 된다),
// 남에게 알리지 않고 그 항목을 조용히 버린다 — 그 항목은 이 판에 선점 만료도 반납도
// 없어서 사람이 손대기 전까지 아무도 못 집는다.
func TestRenderPickUnclaimedMemberTellsWhatToDoNext(t *testing.T) {
	got := RenderPick(service.PickResult{
		Mode: service.PickClaimed, Reason: "선두를 선점했다",
		Item:   &model.Item{ID: "lead", Title: "선두", State: model.ItemClaimed, CreatedAt: t0},
		Branch: "lead",
		Bundle: &service.BundleInfo{Members: []service.BundleMember{
			{
				Item:      model.Item{ID: "mem-held", Title: "남이 쥠", CreatedAt: t0},
				Rejection: &model.Rejection{Item: "mem-held", Reason: judge.RejectClaimed, Detail: "세션 S2 가 선점했다"},
			},
			{Item: model.Item{ID: "mem-ok", Title: "집힌 것", CreatedAt: t0}, Claimed: true},
		}},
	}, t0)

	// 지시는 **그 구성원 절 안**에 있어야 한다 — 응답 아무 데나 있으면 어느 구성원
	// 이야기인지 사람이 못 짚는다.
	seg := bundleMemberSegment(t, got, "mem-held")
	for _, want := range []string{"나머지를 진행한다", `note(kind:"ask")`} {
		if !strings.Contains(seg, want) {
			t.Fatalf("못 집은 구성원 절에 행동 지시 %q 가 없다:\n%s\n전체:\n%s", want, seg, got)
		}
	}
	// 집힌 구성원에는 안 붙는다 — 상시 점등된 지시는 판별력이 0이다.
	if strings.Contains(bundleMemberSegment(t, got, "mem-ok"), "나머지를 진행한다") {
		t.Fatalf("집힌 구성원에도 실패 지시가 붙었다:\n%s", got)
	}
}
