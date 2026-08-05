package mcpsrv

import (
	"context"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// 리뷰 라운드 2 — **응답이 하지 않은 일을 세지 않는다.**
//
// 이 파일의 좌표계는 전부 **에이전트가 실제로 읽는 문자열**이다. 구조체 필드를
// 세는 단정은 이 축을 원리적으로 못 잡는다: 결함은 전부 "필드는 맞는데 문장이
// 다른 수를 말한다" 였다.

// bundleResult 는 선두 1 + 구성원 n 짜리 선점 응답을 짓는다.
// claimed[i] 가 그 구성원을 집었는지다.
func bundleResult(mode service.PickMode, ids []string, claimed []bool) service.PickResult {
	res := service.PickResult{
		Mode: mode, Reason: "사유", Branch: "lead",
		Item:   &model.Item{ID: "lead", Title: "선두", State: model.ItemClaimed, Paths: []string{"lead.go"}, CreatedAt: t0},
		Bundle: &service.BundleInfo{Reason: "묶음 사유"},
	}
	for i, id := range ids {
		m := service.BundleMember{
			Item:    model.Item{ID: id, Title: id + " 제목", State: model.ItemOpen, CreatedAt: t0},
			Link:    judge.Link{Item: id, Detail: "세션이 함께 지정했다"},
			Claimed: claimed[i],
		}
		if !claimed[i] {
			m.Rejection = &model.Rejection{Item: id, Reason: judge.RejectClaimed, Detail: "세션 S2 가 선점했다"}
		}
		res.Bundle.Members = append(res.Bundle.Members, m)
	}
	return res
}

// TestRenderPickOverlapScopeCountsOnlyClaimedMembers 는 finding 3 을 잠근다.
//
// 실측 시나리오: 3건을 요청해 1건(선두)만 집혔는데 응답이 "묶음 3건의 경로를 전부
// 합쳐서 봤다"고 말했다. 겹침을 실제로 계산한 service.pickBundle 은 **집은 구성원의
// 경로만** allPaths 에 합친다 — 즉 실제 커버리지는 1건이었다. 그 문장을 믿은 세션은
// 겹침 0건을 못 집은 2건에까지 적용하고, 겹침 축은 정확히 그 결론을 막으려고 있다.
func TestRenderPickOverlapScopeCountsOnlyClaimedMembers(t *testing.T) {
	got := RenderPick(bundleResult(service.PickClaimed,
		[]string{"m-a", "m-b"}, []bool{false, false}), t0)

	if strings.Contains(got, "묶음 3건의 경로를 전부 합쳐서 봤다") {
		t.Fatalf("1건만 집었는데 3건을 합쳐 봤다고 말한다:\n%s", got)
	}
	if !strings.Contains(got, "겹침 판정 범위: 항목 lead 의 경로만 봤다") {
		t.Fatalf("실제로 본 범위(선두 경로 하나)를 말하지 않는다:\n%s", got)
	}
	// 못 집은 것들은 **이름으로** 불려야 한다. 수만 말하면 어느 항목의 경로가
	// 판정 밖인지 못 가린다.
	for _, want := range []string{"안 집은 구성원 2건", "m-a", "m-b", "안 들어갔다"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q 가 없다 — 판정 밖의 항목이 침묵으로 사라진다:\n%s", want, got)
		}
	}
}

// 절반만 집힌 묶음은 **집힌 수**를 말한다(요청 수도, 1도 아니다).
func TestRenderPickOverlapScopeCountsThePartiallyClaimedBundle(t *testing.T) {
	got := RenderPick(bundleResult(service.PickClaimed,
		[]string{"m-a", "m-b"}, []bool{true, false}), t0)

	if !strings.Contains(got, "겹침 판정 범위: 묶음 2건의 경로를 전부 합쳐서 봤다") {
		t.Fatalf("집은 2건을 말하지 않는다:\n%s", got)
	}
	if strings.Contains(got, "묶음 3건의 경로") {
		t.Fatalf("요청 수(3)를 커버리지로 말한다:\n%s", got)
	}
	if !strings.Contains(got, "안 집은 구성원 1건(m-b)") {
		t.Fatalf("못 집은 구성원을 이름으로 안 부른다:\n%s", got)
	}
}

// TestRenderPickBranchLineCountsOnlyClaimedMembers 는 finding 3 의 나머지 절반이다.
//
// "N건을 이 워크트리에서 함께 한다"는 **행동 지시**다. 못 집은 항목까지 세면 세션은
// 남이 쥔 항목을 자기 브랜치에서 고친다.
func TestRenderPickBranchLineCountsOnlyClaimedMembers(t *testing.T) {
	got := RenderPick(bundleResult(service.PickClaimed,
		[]string{"m-a", "m-b"}, []bool{false, false}), t0)

	if strings.Contains(got, "3건을 이 워크트리에서 함께 한다") {
		t.Fatalf("못 집은 2건까지 워크트리에 얹는다고 말한다:\n%s", got)
	}
	if !strings.Contains(got, "구성원 2건은 이 응답이 못 집었다") {
		t.Fatalf("무엇이 빠졌는지 브랜치 절에서 말하지 않는다:\n%s", got)
	}

	// 추천(아직 아무것도 안 집은 상태)은 "못 집었다"가 아니라 "아직 선점 전"이다 —
	// 두 사실을 같은 문장으로 찍으면 판정이 지어 준 묶음이 실패 목록으로 보인다.
	rec := bundleResult(service.PickRecommended, []string{"m-a"}, []bool{false})
	rec.Bundle.Members[0].Rejection = nil // 추천 후보는 시도 자체가 없다
	recGot := RenderPick(rec, t0)
	if !strings.Contains(recGot, "구성원 1건은 아직 선점 전이라") {
		t.Fatalf("추천을 '못 집었다'로 찍는다:\n%s", recGot)
	}
}

// TestRenderPickResumedHeaderNamesBundle 은 finding 4 의 렌더 쪽 절반이다.
//
// 재개 갈래만 묶음 수를 통째로 빠뜨리고 있었다 — 묶음 3건을 쥔 채 돌아온 세션의
// 머리줄이 단독 재개와 글자 하나 다르지 않았다.
func TestRenderPickResumedHeaderNamesBundle(t *testing.T) {
	got := RenderPick(bundleResult(service.PickResumed,
		[]string{"m-a", "m-b"}, []bool{true, true}), t0)

	first := got[:strings.IndexByte(got, '\n')]
	if !strings.Contains(first, "묶음 3건 중 3건을 쥐고 있다") {
		t.Fatalf("재개 머리줄이 묶음 수를 말하지 않는다: %q\n%s", first, got)
	}
	// 재개는 **쓰기가 아니다.** 머리줄이 "선점했다"로 읽히면 안 된다.
	if strings.HasPrefix(first, "pick · 선점했다") {
		t.Fatalf("재개를 선점으로 찍는다: %q", first)
	}

	solo := service.PickResult{Mode: service.PickResumed, Reason: "사유",
		Item: &model.Item{ID: "lead", State: model.ItemClaimed, CreatedAt: t0}}
	soloFirst := RenderPick(solo, t0)
	if !strings.HasPrefix(soloFirst, "pick · 재개 — 이미 내 선점이다") {
		t.Fatalf("묶음 축이 없는 재개의 머리줄이 바뀌었다:\n%s", soloFirst)
	}
}

// TestRenderBundleEmptyStatesItsScope 는 finding 5 의 렌더 쪽 절반이다.
//
// 구성원 0건이 나오는 갈래가 둘인데 말하는 사실이 다르다: 추천은 "찾아봤는데 없다",
// item_id 경로는 "애초에 안 찾았다". 범위 문장을 안 찍으면 둘이 같은 화면이 되고,
// 세션은 이 항목에 형제가 없다고 잘못 결론짓는다. 예전에는 구성원이 **있을 때만**
// 범위를 찍어서, 정작 0건일 때 그 뜻이 침묵으로 사라졌다.
func TestRenderBundleEmptyStatesItsScope(t *testing.T) {
	const scope = "관찰한 후보는 전체 5건이다 · 형제 축은 이번에 못 읽었다"
	got := RenderPick(service.PickResult{
		Mode: service.PickRecommended, Reason: "사유",
		Item:   &model.Item{ID: "solo", State: model.ItemOpen, CreatedAt: t0},
		Bundle: &service.BundleInfo{Reason: "의존자 합 0 · 묶음 1건", Scope: scope},
	}, t0)

	if !strings.Contains(got, scope) {
		t.Fatalf("구성원 0건인데 그 0건이 무슨 뜻인지 안 말한다:\n%s", got)
	}
	// 겹침 범위도 선두 하나로 좁혀 말해야 한다 — 꼬리의 "겹침 없음"이
	// 이 세션 전체에 대한 판정으로 읽히면 안 된다.
	if !strings.Contains(got, "겹침 판정 범위: 항목 solo 의 경로만 봤다") {
		t.Fatalf("겹침 범위를 안 말한다:\n%s", got)
	}
}

// TestRenderBundleAbsenceMeansOnlyUnread 는 finding 5 의 나머지 절반이다.
//
// nil 의 뜻을 **하나로** 지킨다. 서비스의 세 갈래(추천 · item_id 선점/재개 · 묶음)가
// 전부 non-nil 을 내므로, nil 이 남는 길은 구서버와 옛 캐시 둘뿐이다 — 그러니
// 부재 문장이 그 둘을 대는 것이 이제 참이다. 한때 이 문장은 현행 서버의 신선한
// 응답에도 붙었고, 그때는 두 원인이 **다 거짓**이었다.
//
// 그리고 이 갈래는 겹침 범위를 **단정하지 않는다**: 축을 안 읽은 응답이라 어떤 경로
// 집합을 봤는지도 알 수 없다. 모르는 것을 "선두 경로만 봤다"로 메우면 부재를 값으로
// 접는 같은 실패를 한 칸 옆에서 반복하는 것이 된다.
func TestRenderBundleAbsenceMeansOnlyUnread(t *testing.T) {
	got := RenderPick(service.PickResult{
		Mode: service.PickClaimed, Reason: "사유",
		Item: &model.Item{ID: "solo", State: model.ItemClaimed, CreatedAt: t0},
		// Bundle 은 nil — 구서버나 옛 캐시가 낸 응답의 모양이다.
	}, t0)

	if !strings.Contains(got, "묶음: 이 응답은 그 축을 읽지 않았다") {
		t.Fatalf("축 부재를 아예 안 말한다:\n%s", got)
	}
	// 현행 서버의 단독 선점은 더 이상 이 갈래를 안 지난다 — 그러니 그것을 원인으로
	// 대면 안 된다(그 문구가 남아 있으면 pickExplicit 의 고침이 되돌려진 것이다).
	if strings.Contains(got, "item_id 하나를 지정한 호출이라 이웃을 안 찾았거나") {
		t.Fatalf("현행 서버가 안 내는 갈래를 원인으로 댄다:\n%s", got)
	}
	// 축을 안 읽었으면 겹침 범위도 단정하면 안 된다.
	if strings.Contains(got, "겹침 판정 범위:") {
		t.Fatalf("축을 안 읽은 응답이 겹침 범위를 단정한다:\n%s", got)
	}
	if !strings.Contains(got, "어떤 경로 집합을 보고 나온 값인지도 이 응답만으로는 알 수 없다") {
		t.Fatalf("겹침이 무엇을 본 값인지 모른다는 사실을 침묵한다:\n%s", got)
	}
}

// TestRenderBundleUnaccountedNamesEveryMissingID 는 finding 1 의 렌더 쪽이다.
func TestRenderBundleUnaccountedNamesEveryMissingID(t *testing.T) {
	if RenderBundleUnaccounted(nil) != "" {
		t.Fatal("빠진 것이 없는데 문장을 냈다 — 정상 응답에 상시 경고가 붙는다")
	}
	got := RenderBundleUnaccounted([]string{"sk-b", "sk-c"})
	for _, want := range []string{"sk-b", "sk-c", "쥐었다는 근거가 응답 어디에도 없다"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q 가 없다:\n%s", want, got)
		}
	}
}

// TestRenderFinishNamesItemsStillHeld 는 finding 6 을 잠근다.
//
// finish 는 항목 하나만 닫는데 pick 은 묶음을 집는다. 그 비대칭을 말하는 표면이
// 하나도 없어서, 남은 선점은 만료도 세션 종료 반납도 없이 영원히 남았다.
func TestRenderFinishNamesItemsStillHeld(t *testing.T) {
	held := []string{"mem-1", "mem-2"}
	got := RenderFinish(service.FinishResult{
		Item:      model.Item{ID: "lead", State: model.ItemDone},
		Judgment:  model.Judgment{ID: "J1", Kind: model.JudgmentHandoff, Body: "본문"},
		Followups: []model.Item{{ID: "next"}},
		StillHeld: &held,
	})
	for _, want := range []string{"아직 쥐고 있는", "mem-1", "mem-2", "남이 못 집는다"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q 가 없다 — 남은 선점이 침묵으로 사라진다:\n%s", want, got)
		}
	}
	// 자동으로 닫아 주는 것처럼 읽히면 안 된다.
	if strings.Contains(got, "함께 닫았다") {
		t.Fatalf("안 한 일을 했다고 말한다:\n%s", got)
	}
}

// 진짜 0건과 "못 읽었다"를 가른다. 하나만 지키면 `len(*StillHeld) > 0` 같은
// '정리'가 시험 전부를 통과하면서 조회 실패를 "남은 선점 없음"으로 바꿔 놓는다.
func TestRenderFinishSeparatesNoHoldsFromUnread(t *testing.T) {
	none := []string{}
	zero := RenderFinish(service.FinishResult{
		Item: model.Item{ID: "lead", State: model.ItemDone}, StillHeld: &none,
	})
	if !strings.Contains(zero, "쥔 항목은 이제 0건이다") {
		t.Fatalf("진짜 0건을 안 말한다:\n%s", zero)
	}
	if strings.Contains(zero, "못 읽었다") {
		t.Fatalf("진짜 0건을 미관측으로 접었다:\n%s", zero)
	}

	unread := RenderFinish(service.FinishResult{
		Item: model.Item{ID: "lead", State: model.ItemDone}, StillHeld: nil,
	})
	if !strings.Contains(unread, "못 읽었다") {
		t.Fatalf("미관측을 침묵으로 접었다:\n%s", unread)
	}
	if strings.Contains(unread, "쥔 항목은 이제 0건이다") {
		t.Fatalf("미관측을 0건으로 단정했다:\n%s", unread)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// finding 1 — 도구 표면도 보낸 것과 돌아온 것을 대조한다
// ─────────────────────────────────────────────────────────────────────────────

// backendIgnoringItemIDs 는 **item_ids 를 모르는 서버**를 흉내 낸다.
//
// cae53bd 판이 이랬다: 그 필드를 조용히 버리고 선두 하나만 집는다. api_version 은
// 양쪽 다 "1" 이라 SkewBanner 가 안 뜨므로, 도구가 스스로 대조하지 않으면 이 스큐는
// **원리적으로** 안 보인다. 뒤에 있는 것은 실물 service 다 — 바꾸는 것은 그 한 가지뿐이다.
type backendIgnoringItemIDs struct{ *service.Service }

func (b backendIgnoringItemIDs) Pick(ctx context.Context, in service.PickInput) (service.PickResult, error) {
	if len(in.ItemIDs) > 0 {
		in = service.PickInput{Project: in.Project, SessionID: in.SessionID, ItemID: in.ItemIDs[0]}
	}
	return b.Service.Pick(ctx, in)
}

// TestToolPickFlagsIDsTheResponseNeverAccountsFor 는 MCP 표면의 finding 1 이다.
//
// CLI 는 종료코드로 말하지만 MCP 에는 종료코드가 없다 — 그 자리를 isError 가 맡는다.
// 본문은 지우지 않는다: 선두는 실제로 집혔고 그 브랜치·워크트리 명령은 유효하다.
func TestToolPickFlagsIDsTheResponseNeverAccountsFor(t *testing.T) {
	repo := newRepo(t)
	svc, st := newSvc(t)
	srv := New(backendIgnoringItemIDs{svc}, discard(),
		WithEnv(env(fullEnv(repo))), WithCwd(repo, nil), WithHostname("testhost", nil))

	frames := serve(t, srv,
		call("add", map[string]any{"id": "sk-a", "title": "제목", "body": "본문"}),
		call("add", map[string]any{"id": "sk-b", "title": "제목", "body": "본문"}),
		call("add", map[string]any{"id": "sk-c", "title": "제목", "body": "본문"}),
		call("pick", map[string]any{"item_ids": []string{"sk-a", "sk-b", "sk-c"}}),
	)
	text, isErr := toolText(t, frames[3])

	// 대조 전제: 정말 **선두만** 집혔나. 양쪽을 다 단정한다 — 부정만 하면
	// 프로젝트 id 를 틀리게 적어도 "아무것도 안 집혔다"로 통과한다.
	if cl, err := st.GetClaim(context.Background(), "repo", "sk-a"); err != nil || cl.ReleasedAt != nil {
		t.Fatalf("전제가 깨졌다 — 선두 sk-a 조차 원장에 없다(좌표가 틀렸을 수 있다): %+v %v", cl, err)
	}
	for _, id := range []string{"sk-b", "sk-c"} {
		if cl, err := st.GetClaim(context.Background(), "repo", id); err == nil && cl.ReleasedAt == nil {
			t.Fatalf("전제가 깨졌다 — %s 가 실제로 선점됐다: %+v", id, cl)
		}
	}

	if !isErr {
		t.Fatalf("아무도 안 쥔 항목 둘을 두고 정상 응답을 냈다:\n%s", text)
	}
	for _, want := range []string{"sk-b", "sk-c", "쥐었다는 근거가 응답 어디에도 없다"} {
		if !strings.Contains(text, want) {
			t.Fatalf("%q 가 응답에 없다 — 세션은 안 쥔 것을 쥐었다고 믿는다:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "브랜치: sk-a") {
		t.Fatalf("성공한 절반(선두)의 맥락까지 버렸다:\n%s", text)
	}
}

// 대조 — 현행 서버에서는 이 경고가 안 나오고 isError 도 아니다.
// 없으면 "항상 isError" 도 위 시험을 통과한다.
func TestToolPickStaysCleanWhenEveryIDIsAccountedFor(t *testing.T) {
	repo := newRepo(t)
	svc, _ := newSvc(t)
	srv := newServer(t, svc, repo, fullEnv(repo))

	frames := serve(t, srv,
		call("add", map[string]any{"id": "ok-a", "title": "제목", "body": "본문"}),
		call("add", map[string]any{"id": "ok-b", "title": "제목", "body": "본문"}),
		call("pick", map[string]any{"item_ids": []string{"ok-a", "ok-b"}}),
	)
	text, isErr := toolText(t, frames[2])
	if isErr {
		t.Fatalf("전부 설명된 응답이 오류로 나왔다:\n%s", text)
	}
	if strings.Contains(text, "설명하지 않는다") {
		t.Fatalf("전부 설명된 응답에 경고가 붙었다:\n%s", text)
	}
}
