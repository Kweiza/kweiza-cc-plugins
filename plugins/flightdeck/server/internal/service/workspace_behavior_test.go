package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// wsFixture 는 루트 + 멤버 둘짜리 워크스페이스를 세우고 세 세션을 연다.
//
// ★ 실물 저장소를 쓴다. 이 축들(자원 배타·교차 선행·겹침)은 전부 명부가 **커밋된
// 파일에서** 읽힌 뒤에만 서는데, 그 경로를 건너뛰면 시험이 재는 것은 판정 함수뿐이고
// 배선은 하나도 안 잠긴다 — 이 항목이 고치려는 결함이 정확히 «축은 있는데 안 꽂혔다»다.
type wsFixture struct {
	svc        *Service
	root       string // 루트 레포 절대경로
	rootSess   string
	memberASes string
	memberBSes string
}

func newWSFixture(t *testing.T) *wsFixture {
	t.Helper()
	svc, _ := newSvc(t)
	root := wsRepo(t, twoMemberYAML, "member-a", "member-b")

	f := &wsFixture{svc: svc, root: root}
	f.rootSess = openSession(t, svc, "repo", root, root, "cc-root", "").Session.ID

	a := filepath.Join(root, "member-a")
	f.memberASes = openSession(t, svc, "search-api", a, a, "cc-a", "").Session.ID
	b := filepath.Join(root, "member-b")
	f.memberBSes = openSession(t, svc, "member-b", b, b, "cc-b", "").Session.ID
	return f
}

// ─────────────────────────────────────────────────────────────────────────────
// project 인자 관문
// ─────────────────────────────────────────────────────────────────────────────

// 루트 세션은 멤버 프로젝트에 항목을 만들고 집고 끝낼 수 있다.
//
// 선점 행은 **대상 프로젝트에** 서고 그 행이 가리키는 세션은 **루트 카드**다 —
// 카드를 복제하지 않는 것이 이 축의 판정이다(카드가 프로젝트 수만큼 늘면 /clear 표류가 곱해진다).
func TestRootSessionWorksInMemberProject(t *testing.T) {
	f := newWSFixture(t)
	ctx := context.Background()

	if _, err := f.svc.AddItem(ctx, AddItemInput{
		Project: "search-api", SessionID: f.rootSess,
		ID: "member-task", Title: "멤버 과제", Body: "루트에서 만든다",
	}); err != nil {
		t.Fatalf("멤버 프로젝트에 항목 등록 실패: %v", err)
	}
	res, err := f.svc.Pick(ctx, PickInput{
		Project: "search-api", SessionID: f.rootSess, ItemID: "member-task",
	})
	if err != nil {
		t.Fatalf("멤버 항목 선점 실패: %v", err)
	}
	if res.Claim == nil || res.Claim.SessionID != f.rootSess {
		t.Fatalf("선점이 루트 카드를 안 가리킨다: %+v", res.Claim)
	}
	// ★ 워크트리 명령은 **멤버 레포 경로** 기준이어야 한다 — 루트 레포에 워크트리를
	//   만들면 그 브랜치가 엉뚱한 저장소에 선다.
	setup := strings.Join(res.Setup, "\n")
	if !strings.Contains(setup, filepath.Join(f.root, "member-a")) {
		t.Fatalf("워크트리 명령이 멤버 경로가 아니다:\n%s", setup)
	}

	// 같은 카드의 finish 는 통과한다.
	if _, err := f.svc.Finish(ctx, FinishInput{
		Project: "search-api", SessionID: f.rootSess, ItemID: "member-task",
		Outcome: model.ItemDone, Body: "끝냈다",
	}); err != nil {
		t.Fatalf("멤버 항목 마무리 실패: %v", err)
	}
}

// 명부 밖 프로젝트는 **거절**한다 — 다만 «없는 이름»은 지금까지의 오류에 맡긴다.
func TestProjectArgRefusesOutsideTheRoster(t *testing.T) {
	f := newWSFixture(t)
	ctx := context.Background()

	// ① 실재하지만 명부 밖인 프로젝트 — 거절이다.
	other := newRepo(t)
	openSession(t, f.svc, "outsider", other, other, "cc-out", "")
	_, err := f.svc.AddItem(ctx, AddItemInput{
		Project: "outsider", SessionID: f.rootSess,
		ID: "x", Title: "t", Body: "b",
	})
	if err == nil {
		t.Fatal("명부 밖 프로젝트에 등록이 통과했다")
	}
	if !strings.Contains(err.Error(), "워크스페이스") {
		t.Fatalf("사유가 명부를 안 말한다: %v", err)
	}

	// ② 아예 없는 이름 — **이 관문이 안 막는다.** 지금까지의 오류(없는 프로젝트)가
	//    그대로 나가야 한다: 「이름을 고쳐라」와 「명부를 고쳐라」는 사람이 할 일이 다르다.
	_, err = f.svc.AddItem(ctx, AddItemInput{
		Project: "아예-없는-이름", SessionID: f.rootSess,
		ID: "y", Title: "t", Body: "b",
	})
	if err == nil {
		t.Fatal("없는 프로젝트에 등록이 통과했다")
	}
	if strings.Contains(err.Error(), "워크스페이스") {
		t.Fatalf("없는 이름을 명부 사유로 덮었다: %v", err)
	}
}

// 워크스페이스가 아닌 프로젝트에서 남의 프로젝트를 지목하면 거절한다.
func TestProjectArgOutsideWorkspaceIsRefused(t *testing.T) {
	svc, _ := newSvc(t)
	solo := newRepo(t)
	other := newRepo(t)
	s1 := openSession(t, svc, "solo", solo, solo, "cc-1", "").Session.ID
	openSession(t, svc, "other", other, other, "cc-2", "")

	_, err := svc.AddItem(context.Background(), AddItemInput{
		Project: "other", SessionID: s1, ID: "x", Title: "t", Body: "b",
	})
	if err == nil {
		t.Fatal("워크스페이스가 아닌데 남의 프로젝트에 등록이 통과했다")
	}
	if !strings.Contains(err.Error(), "워크스페이스가 아니다") {
		t.Fatalf("사유=%v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 자원 배타 — 멤버 둘이 같은 이름을 잡으면 줄을 선다
// ─────────────────────────────────────────────────────────────────────────────

func TestExclusiveResourceIsSharedAcrossMembers(t *testing.T) {
	f := newWSFixture(t)
	ctx := context.Background()

	a, err := f.svc.Land(ctx, LandInput{
		Project: "search-api", SessionID: f.memberASes, Resources: []string{"env:dell"},
	})
	if err != nil {
		t.Fatalf("멤버 A 의 자원 취득 실패: %v", err)
	}
	if a.State != "turn" {
		t.Fatalf("멤버 A 가 차례를 못 받았다: %+v", a)
	}
	// ★ 스코프는 **루트**다. 이 값이 멤버 자신이면 배타가 워크스페이스로 안 넓어진 것이다.
	if a.Scope != "repo" {
		t.Fatalf("스코프=%q — 루트(repo)여야 배타가 워크스페이스에 선다", a.Scope)
	}

	b, err := f.svc.Land(ctx, LandInput{
		Project: "member-b", SessionID: f.memberBSes, Resources: []string{"env:dell"},
	})
	if err != nil {
		t.Fatalf("멤버 B 의 줄 서기 실패: %v", err)
	}
	if b.State != "waiting" {
		t.Fatalf("멤버 B 가 %q 다 — 형제가 쥔 자원이라 waiting 이어야 한다: %+v", b.State, b)
	}

	// 루트 세션도 같은 줄이다 — 아니면 루트와 멤버가 서로를 안 막는다.
	r, err := f.svc.Land(ctx, LandInput{
		Project: "repo", SessionID: f.rootSess, Resources: []string{"env:dell"},
	})
	if err != nil {
		t.Fatalf("루트의 줄 서기 실패: %v", err)
	}
	if r.State != "waiting" {
		t.Fatalf("루트가 %q 다 — 멤버가 쥔 자원이라 waiting 이어야 한다", r.State)
	}

	// 반납하면 다음 사람이 받는다.
	if _, err := f.svc.LandReport(ctx, LandReportInput{
		Project: "search-api", SessionID: f.memberASes, Kind: model.LandingLeftOK,
	}); err != nil {
		t.Fatalf("멤버 A 반납 실패: %v", err)
	}
	again, err := f.svc.Land(ctx, LandInput{
		Project: "member-b", SessionID: f.memberBSes, Resources: []string{"env:dell"},
	})
	if err != nil {
		t.Fatalf("멤버 B 재진입 실패: %v", err)
	}
	if again.State != "turn" {
		t.Fatalf("반납 뒤에도 멤버 B 가 %q 다: %+v", again.State, again)
	}
}

// 랜딩 레인은 **레포별 그대로**다 — 멤버 17개가 병렬로 랜딩하는 것이 정상이다.
func TestLandingLaneStaysPerRepo(t *testing.T) {
	f := newWSFixture(t)
	ctx := context.Background()

	a, err := f.svc.Land(ctx, LandInput{Project: "search-api", SessionID: f.memberASes})
	if err != nil {
		t.Fatalf("멤버 A 랜딩 레인 실패: %v", err)
	}
	b, err := f.svc.Land(ctx, LandInput{Project: "member-b", SessionID: f.memberBSes})
	if err != nil {
		t.Fatalf("멤버 B 랜딩 레인 실패: %v", err)
	}
	if a.State != "turn" || b.State != "turn" {
		t.Fatalf("랜딩이 직렬화됐다: A=%s B=%s — 레포마다 별개여야 한다", a.State, b.State)
	}
	if a.Scope != "search-api" || b.Scope != "member-b" {
		t.Fatalf("랜딩 스코프가 접혔다: A=%q B=%q", a.Scope, b.Scope)
	}
}

// 한 줄에 두 스코프를 섞으면 **거절한다** — 어느 쪽으로 접어도 틀린다.
func TestMixedResourceScopesAreRefused(t *testing.T) {
	f := newWSFixture(t)
	_, err := f.svc.Land(context.Background(), LandInput{
		Project: "search-api", SessionID: f.memberASes,
		Resources: []string{"landing", "env:dell"},
	})
	if err == nil {
		t.Fatal("스코프가 갈린 자원 집합이 통과했다")
	}
	if !strings.Contains(err.Error(), "스코프가 다른 자원") {
		t.Fatalf("사유=%v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 교차 프로젝트 선행
// ─────────────────────────────────────────────────────────────────────────────

// 총괄 항목(루트)이 멤버 항목을 선행으로 두면, 그것이 끝나기 전에는 추천에서 빠지고
// **탈락 사유에 프로젝트가 찍힌다**.
func TestCrossProjectAfterBlocksAndNamesTheProject(t *testing.T) {
	f := newWSFixture(t)
	ctx := context.Background()

	if _, err := f.svc.AddItem(ctx, AddItemInput{
		Project: "search-api", SessionID: f.rootSess,
		ID: "detail", Title: "세부", Body: "멤버 레포의 일",
	}); err != nil {
		t.Fatalf("멤버 항목 등록 실패: %v", err)
	}
	if _, err := f.svc.AddItem(ctx, AddItemInput{
		Project: "repo", SessionID: f.rootSess,
		ID: "umbrella", Title: "총괄", Body: "세부가 끝나야 한다",
		After: []model.After{{Item: "detail", Project: "search-api"}},
	}); err != nil {
		t.Fatalf("총괄 항목 등록 실패: %v", err)
	}

	res, err := f.svc.Pick(ctx, PickInput{Project: "repo", SessionID: f.rootSess})
	if err != nil {
		t.Fatalf("추천 실패: %v", err)
	}
	if res.Mode != PickNone {
		t.Fatalf("선행이 안 끝났는데 추천됐다: %+v", res.Item)
	}
	// ★ **Detail 을 본다 — Reason 은 사유 코드다.** 코드에는 프로젝트가 안 실리고
	//   실려서도 안 된다(코드는 분포 집계의 축이라 값이 섞이면 못 센다). 사람이 읽는
	//   자리는 Detail 이고, 이 시험이 재는 것도 그쪽이다.
	joined := ""
	for _, r := range res.Rejected {
		joined += r.Reason + " " + r.Detail + "\n"
	}
	if !strings.Contains(joined, "search-api/detail") {
		t.Fatalf("탈락 사유에 프로젝트가 안 찍혔다:\n%s", joined)
	}

	// 선행을 끝내면 풀린다 — 「기다리면 풀린다」가 실제로 성립하는지가 이 축의 본체다.
	if _, err := f.svc.Finish(ctx, FinishInput{
		Project: "search-api", SessionID: f.rootSess, ItemID: "detail",
		Outcome: model.ItemDone, Body: "세부를 끝냈다",
	}); err != nil {
		t.Fatalf("세부 마무리 실패: %v", err)
	}
	res, err = f.svc.Pick(ctx, PickInput{Project: "repo", SessionID: f.rootSess})
	if err != nil {
		t.Fatalf("추천 실패: %v", err)
	}
	if res.Mode != PickRecommended || res.Item == nil || res.Item.ID != "umbrella" {
		t.Fatalf("선행이 끝났는데 안 풀렸다: mode=%s item=%+v", res.Mode, res.Item)
	}
}

// 멤버 항목을 폐기하면 총괄 항목이 **침묵하지 않는다** — 폐기 관문이 이름을 댄다.
func TestDroppingACrossProjectDependencyIsNotSilent(t *testing.T) {
	f := newWSFixture(t)
	ctx := context.Background()

	addItemAs(t, f.svc, "search-api", f.rootSess, "detail")
	if _, err := f.svc.AddItem(ctx, AddItemInput{
		Project: "repo", SessionID: f.rootSess,
		ID: "umbrella", Title: "총괄", Body: "세부가 끝나야 한다",
		After: []model.After{{Item: "detail", Project: "search-api"}},
	}); err != nil {
		t.Fatalf("총괄 항목 등록 실패: %v", err)
	}
	claimed(t, f.svc, "search-api", f.rootSess, "detail")

	_, err := f.svc.Finish(ctx, FinishInput{
		Project: "search-api", SessionID: f.rootSess, ItemID: "detail",
		Outcome: model.ItemDropped, CloseReason: "안 한다", Body: "폐기한다",
	})
	if err == nil {
		t.Fatal("교차 프로젝트 종속이 있는데 폐기가 조용히 통과했다")
	}
	if !strings.Contains(err.Error(), "repo/umbrella") {
		t.Fatalf("거절 문면이 기다리는 항목의 프로젝트를 안 댄다: %v", err)
	}
	// ★ **수(Dependents)도 함께 잰다.** 이름 목록(DependentItems)만 재면 그쪽 질의만
	//   잠기는데, 두 함수는 서로 다른 SQL 이고 «살아 있음»의 정의가 갈리면 관문 문구와
	//   추천 순위가 서로 다른 세상을 본다(store 의 Dependents 주석이 그 규율이다).
	n, derr := f.svc.Store().Dependents(ctx, "search-api", "detail")
	if derr != nil {
		t.Fatalf("종속 수 조회 실패: %v", derr)
	}
	if n != 1 {
		t.Fatalf("종속 수=%d — 다른 프로젝트에서 하나가 기다리므로 1이어야 한다", n)
	}
}

// 후속을 멤버 프로젝트에 만든다 — 판단 링크는 프로젝트를 넘어 이어진다.
func TestFollowupCanLandInAMemberProject(t *testing.T) {
	f := newWSFixture(t)
	ctx := context.Background()

	addItemAs(t, f.svc, "repo", f.rootSess, "umbrella")
	claimed(t, f.svc, "repo", f.rootSess, "umbrella")

	res, err := f.svc.Finish(ctx, FinishInput{
		Project: "repo", SessionID: f.rootSess, ItemID: "umbrella",
		Outcome: model.ItemDone, Body: "총괄을 끝내고 세부를 멤버로 넘긴다",
		Followups: []FollowupInput{{
			ID: "detail-in-member", Title: "세부", Body: "멤버 레포의 일",
			Project: "search-api",
		}},
	})
	if err != nil {
		t.Fatalf("교차 프로젝트 후속 실패: %v", err)
	}
	if len(res.Followups) != 1 || res.Followups[0].Project != "search-api" {
		t.Fatalf("후속이 멤버 프로젝트에 안 앉았다: %+v", res.Followups)
	}
	// 그 항목이 실제로 그 프로젝트의 큐에 있나 — 응답만 보면 «만들었다고 말만» 한 것과 구별이 안 된다.
	if _, err := f.svc.Store().GetItem(ctx, "search-api", "detail-in-member"); err != nil {
		t.Fatalf("멤버 큐에 후속이 없다: %v", err)
	}

	// 명부 밖 프로젝트를 지목한 후속은 거절이다.
	addItemAs(t, f.svc, "repo", f.rootSess, "umbrella-2")
	claimed(t, f.svc, "repo", f.rootSess, "umbrella-2")
	_, err = f.svc.Finish(ctx, FinishInput{
		Project: "repo", SessionID: f.rootSess, ItemID: "umbrella-2",
		Outcome: model.ItemDone, Body: "본문",
		Followups: []FollowupInput{{
			ID: "nope", Title: "t", Body: "b", Project: "아예-없는-이름",
		}},
	})
	if err == nil {
		t.Fatal("명부 밖 프로젝트의 후속이 통과했다")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 겹침 — 같은 파일을 두 레포 좌표에서 만진다
// ─────────────────────────────────────────────────────────────────────────────

// 루트 세션이 `member-a/server/foo.go` 를, 멤버 세션이 `server/foo.go` 를 만지면
// **같은 파일**이다. 좌표계가 달라 문자열로는 안 맞고, 그 침묵이 이 축의 결함이었다.
func TestOverlapCrossesTheWorkspace(t *testing.T) {
	f := newWSFixture(t)
	ctx := context.Background()

	// 멤버 세션이 자기 좌표로 파일을 만진다.
	if err := f.svc.Beat(ctx, f.memberASes, model.SignalTool,
		[]string{filepath.Join(f.root, "member-a", "server", "foo.go")}); err != nil {
		t.Fatalf("멤버 발자국 실패: %v", err)
	}

	// 루트 세션의 처방이 그 겹침을 본다.
	if err := f.svc.Beat(ctx, f.rootSess, model.SignalTool,
		[]string{filepath.Join(f.root, "member-a", "server", "foo.go")}); err != nil {
		t.Fatalf("루트 발자국 실패: %v", err)
	}
	res, err := f.svc.Prescriptions(ctx, f.rootSess)
	if err != nil {
		t.Fatalf("처방 실패: %v", err)
	}
	// ★ **키에 상대 세션 id 가 박혀 있는지까지 본다**(`overlap:<세션>`). 앞선 판은
	//   접두만 재서, 형제 축을 통째로 꺼도 초록이었다 — 같은 프로젝트의 다른 카드와
	//   우연히 겹치면 그 키도 `overlap:` 으로 시작하기 때문이다. 재려던 것은
	//   「**저 멤버 세션**과 겹쳤나」이므로 상대를 지목해야 한다.
	want := "overlap:" + f.memberASes
	var keys []string
	for _, p := range res.Shown {
		keys = append(keys, p.Key)
	}
	found := false
	for _, k := range keys {
		if k == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("루트 세션이 멤버 세션(%s)과의 겹침을 못 봤다 — 처방 키: %v", f.memberASes, keys)
	}

	// ★ **반대 방향도 본다** — 수용 조건이 「겹침 처방이 «양쪽»에 난다」이다.
	//   한쪽만 나면 루트에서 일하는 사람만 조율하고 멤버 쪽은 모른 채 같은 파일을 고친다.
	back, err := f.svc.Prescriptions(ctx, f.memberASes)
	if err != nil {
		t.Fatalf("멤버 쪽 처방 실패: %v", err)
	}
	wantBack := "overlap:" + f.rootSess
	var backKeys []string
	okBack := false
	for _, p := range back.Shown {
		backKeys = append(backKeys, p.Key)
		if p.Key == wantBack {
			okBack = true
		}
	}
	if !okBack {
		t.Fatalf("멤버 세션이 루트 세션(%s)과의 겹침을 못 봤다 — 처방 키: %v", f.rootSess, backKeys)
	}
}
