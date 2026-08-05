package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// 리뷰 라운드 2 — **응답이 원장과 어긋나지 않는다.**
//
// 이 파일이 지키는 것 셋:
//  1. 커밋된 선점은 어떤 갈래에서도 "못 집었다"로 보고되지 않는다(finding 2).
//  2. 아무것도 안 쓴 재개가 "집었다"로 보고되지 않는다(finding 4).
//  3. 묶음 축을 읽은 응답이 "안 읽었다"로 보이지 않는다(finding 5).
//
// 셋 다 원장은 옳고 **문장만 거짓말하던** 결함이다. 그래서 단정의 좌표계는
// "필드가 채워졌나"가 아니라 "원장이 말하는 것과 응답이 말하는 것이 같나"다.

// breakJudgmentTime 은 항목에 **해석 불가한 시각**을 가진 판단 행을 심는다.
//
// ★ 제품 소스를 안 건드리고 "커밋 뒤 읽기 실패"를 만드는 문이다.
// store.scanJudgment 이 at 을 parseTime 으로 푸는데, 그 실패가
// JudgmentsForItem → linkedJudgments 로 올라온다. 그 자리가 정확히
// **선점 트랜잭션이 커밋된 다음**이다.
//
// UPDATE 가 아니라 INSERT 인 이유: 판단 표는 추가 전용이라 트리거가 UPDATE·DELETE 를
// 막는다("judgment 는 추가 전용이다 — 정정은 새 행 + supersedes 로 남겨라").
// 그 제약은 이 시험이 우회할 것이 아니다 — 운영에서도 이런 행은 **INSERT 로만**
// 생길 수 있으므로, 심는 방식까지 실제 가능한 경로를 따르는 편이 맞다.
func breakJudgmentTime(t *testing.T, st *store.Store, itemID string) {
	t.Helper()
	id := "J-broken-" + itemID // 항목마다 다른 id 여야 한다(PK)
	// session_id 는 **비운다.** 세션에 걸면 sessionCards 가 pick 초입에서 먼저
	// 이 행을 읽고 죽어서, 정작 재현하려는 "커밋 **뒤**" 갈래에 도달하지 못한다.
	if _, err := st.DB().ExecContext(ctx(),
		`INSERT INTO judgment(id, project, session_id, at, kind, title, body)
		 VALUES (?, 'p', NULL, '해석불가', 'decision', '깨진 시각', '본문')`, id); err != nil {
		t.Fatalf("깨진 판단 행을 못 넣었다: %v", err)
	}
	if _, err := st.DB().ExecContext(ctx(),
		`INSERT INTO judgment_link(judgment_id, target_kind, target_id) VALUES (?, 'item', ?)`,
		id, itemID); err != nil {
		t.Fatalf("깨진 판단 링크를 못 넣었다: %v", err)
	}
	// 대조가 성립하지 않으면 아래 단정은 아무것도 안 지킨다.
	if _, err := st.JudgmentsForItem(ctx(), "p", itemID); err == nil {
		t.Fatal("전제가 깨졌다 — 시각이 해석 불가인데 판단 조회가 성공한다")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// finding 2 — 커밋된 선점은 실패로 보고되지 않는다
// ─────────────────────────────────────────────────────────────────────────────

// TestPickKeepsCommittedClaimWhenPostCommitReadFails 는 단독 경로를 잠근다.
//
// 예전 동작: 선점 Tx 가 커밋된 **뒤** linkedJudgments 가 실패하면 그 오류를 그대로
// 올려 요청이 통째로 죽었다. 그런데 선점 행은 남는다 — schema.sql 에 만료가 없고,
// 세션이 닫혀도 선점을 푸는 코드가 없고, store.JudgeClaim 은 점유자가 있으면
// 생존 검사 없이 거절한다. 즉 그 항목은 **사람이 강제로 풀 때까지 아무도 못 집는다.**
func TestPickKeepsCommittedClaimWhenPostCommitReadFails(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	addItem(t, s, "p", "solo", []string{"services/a.go"}, nil)
	breakJudgmentTime(t, st, "solo")

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID, ItemID: "solo"})
	if err != nil {
		t.Fatalf("커밋 뒤 읽기 실패로 요청 전체가 죽었다 — 선점은 남았는데 세션은 못 집은 줄 안다: %v", err)
	}
	if res.Mode != PickClaimed {
		t.Fatalf("mode 가 %q 다 — claimed 를 기대했다", res.Mode)
	}

	// ★ 좌표계는 원장이다. "응답이 집었다고 말한다"만으로는 부족하다.
	cl, cerr := st.GetClaim(ctx(), "p", "solo")
	if cerr != nil || cl.ReleasedAt != nil || cl.SessionID != me.Session.ID {
		t.Fatalf("응답은 집었다는데 원장에 이 세션의 선점이 없다: %+v %v", cl, cerr)
	}

	// 그리고 **못 읽었다는 사실**이 응답에 남아야 한다. 침묵으로 접으면
	// "판단 0건"과 "판단을 못 읽었다"가 같은 화면이 된다.
	if len(res.Notes) != 0 {
		t.Fatalf("판단을 못 읽는 상태인데 %d건이 실렸다", len(res.Notes))
	}
	var named bool
	for _, f := range res.Derived.Failures {
		if strings.HasPrefix(f.Axis, "notes:") {
			named = true
		}
	}
	if !named {
		t.Fatalf("판단을 못 읽은 사실이 응답 어디에도 없다: %+v", res.Derived.Failures)
	}
}

// TestPickBundleMemberWithUnreadableNotesIsStillClaimed 는 묶음 경로를 잠근다.
//
// 이것이 리뷰어가 실측한 그 모양이다: 구성원의 선점 Tx 는 **커밋됐는데**
// 응답은 Claimed=false · Rejection="claim-failed" 를 적었다.
func TestPickBundleMemberWithUnreadableNotesIsStillClaimed(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	addItem(t, s, "p", "b-lead", []string{"services/a.go"}, nil)
	addItem(t, s, "p", "b-mem", []string{"services/b.go"}, nil)
	breakJudgmentTime(t, st, "b-mem")

	res, err := s.Pick(ctx(), PickInput{
		Project: "p", SessionID: me.Session.ID, ItemIDs: []string{"b-lead", "b-mem"}})
	if err != nil {
		t.Fatalf("묶음 선점이 죽었다: %v", err)
	}
	if res.Bundle == nil || len(res.Bundle.Members) != 1 {
		t.Fatalf("구성원 절이 없다: %+v", res.Bundle)
	}
	m := res.Bundle.Members[0]

	// 원장이 먼저다.
	cl, cerr := st.GetClaim(ctx(), "p", "b-mem")
	held := cerr == nil && cl.ReleasedAt == nil && cl.SessionID == me.Session.ID
	if !held {
		t.Fatalf("전제가 깨졌다 — 구성원 선점이 원장에 없다: %+v %v", cl, cerr)
	}
	if !m.Claimed {
		t.Fatalf("원장은 이 세션이 %s 를 쥐었다는데 응답은 못 집었다고 한다(rejection=%+v) — "+
			"만료도 반납도 없는 판이라 그 항목은 아무도 못 집는 상태로 남는다", m.Item.ID, m.Rejection)
	}
	if m.Rejection != nil {
		t.Fatalf("집었는데 탈락 사유가 붙었다: %+v", m.Rejection)
	}
	// 사유 문장도 원장과 같은 수를 말해야 한다.
	if !strings.Contains(res.Reason, "2건을 집었다") {
		t.Fatalf("2건을 집었는데 사유가 다른 수를 말한다: %q", res.Reason)
	}
}

// TestPickExplicitHasNoFatalReturnAfterCommit 는 위 둘을 **구조로** 지킨다.
//
// 행동 시험 둘은 오늘 아는 커밋 뒤 읽기(항목 재조회 · 연결된 판단)만 덮는다.
// 내일 누가 커밋 뒤에 조회를 하나 더 붙이고 `return PickResult{}, err` 를 적으면
// 같은 결함이 그대로 돌아오는데, 그때 그 사람의 시험은 초록이다.
// 그래서 소스를 직접 본다 — 빨간불이 **더하는 사람**에게 켜져야 그 사람이 규율을 읽는다.
func TestPickExplicitHasNoFatalReturnAfterCommit(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("서버 루트를 못 찾았다: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "internal", "service", "pick.go"))
	if err != nil {
		t.Fatalf("pick.go 를 못 읽었다: %v", err)
	}
	text := string(src)

	const marker = "★★ 여기부터는 **커밋 뒤**다"
	i := strings.Index(text, marker)
	if i < 0 {
		t.Fatalf("커밋 뒤 구간 표식(%q)이 사라졌다 — 이 가드가 볼 좌표가 없다", marker)
	}
	// 표식이 있는 **줄의 처음**으로 물러난다. 안 그러면 구간의 첫 줄이 주석 가운데서
	// 시작해 `//` 접두를 잃고, 규율을 적은 그 줄이 규율 위반으로 잡힌다.
	i = strings.LastIndex(text[:i], "\n") + 1
	// 구간의 끝은 pickExplicit 의 끝, 즉 그 다음 최상위 func 선언이다.
	end := strings.Index(text[i:], "\nfunc ")
	if end < 0 {
		t.Fatal("표식 뒤에 다음 함수가 없다 — 좌표가 틀렸다")
	}
	region := text[i : i+end]

	// 대조 전제: 이 구간이 정말 pickExplicit 의 꼬리인가.
	if !strings.Contains(region, "res.Mode, res.Claim = PickClaimed, &claim") {
		t.Fatalf("표식 뒤 구간이 선점 완료 구간이 아니다 — 이 가드는 꺼진 것이다:\n%s", region)
	}

	for n, ln := range strings.Split(region, "\n") {
		// 주석은 건너뛴다 — 규율을 **적은 줄**이 규율 위반으로 잡히면 안 된다.
		if trimmed := strings.TrimSpace(ln); strings.HasPrefix(trimmed, "//") {
			continue
		}
		if strings.Contains(ln, "return PickResult{}") {
			t.Fatalf("커밋 뒤 구간(pick.go 표식 +%d줄)에 치명적 반환이 있다:\n  %s\n\n"+
				"이 자리에서 오류를 올리면 **이미 커밋된 선점이 실패로 보고된다.** "+
				"묶음 경로는 그것을 Claimed=false·claim-failed 로 적고, 그 항목은 "+
				"만료도 세션 종료 반납도 없는 판이라 사람이 강제로 풀 때까지 아무도 못 집는다. "+
				"실패는 d.note 로 이름만 남기고 응답은 '집었다 · 다만 반쪽이다'로 내라.", n+1, strings.TrimSpace(ln))
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// finding 4 — 재개는 "집었다"고 말하지 않는다
// ─────────────────────────────────────────────────────────────────────────────

// TestPickBundleResumeDoesNotSayItClaimed 는 재출력이 쓰기인 척하는 것을 막는다.
//
// 두 번째 호출은 pickExplicit 의 재개 갈래를 타 **아무것도 쓰지 않는다**(선점 시각을
// 덮으면 "언제부터 쥐고 있나"가 사라지고 그 값이 회수 판단의 축이라 일부러 그렇다).
// 그런데 사유 문장은 무조건 "선점했다 · N건을 집었다"였다.
func TestPickBundleResumeDoesNotSayItClaimed(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	for _, id := range []string{"r-lead", "r-m1"} {
		addItem(t, s, "p", id, []string{"services/" + id + ".go"}, nil)
	}
	ids := []string{"r-lead", "r-m1"}

	first, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID, ItemIDs: ids})
	if err != nil {
		t.Fatalf("첫 묶음 선점 실패: %v", err)
	}
	if !strings.Contains(first.Reason, "2건을 집었다") {
		t.Fatalf("전제가 깨졌다 — 첫 호출이 2건을 집었다고 말하지 않는다: %q", first.Reason)
	}
	before := countRows(t, st, `SELECT count(*) FROM claim`)

	again, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID, ItemIDs: ids})
	if err != nil {
		t.Fatalf("재개 실패: %v", err)
	}
	// 대조 전제: 정말 아무것도 안 썼는가.
	if after := countRows(t, st, `SELECT count(*) FROM claim`); after != before {
		t.Fatalf("전제가 깨졌다 — 재개인데 선점 행이 %d→%d 로 늘었다", before, after)
	}
	if again.Mode != PickResumed {
		t.Fatalf("두 번째 호출의 mode 가 %q 다 — resumed 를 기대했다", again.Mode)
	}

	if strings.Contains(again.Reason, "집었다") {
		t.Fatalf("아무것도 안 썼는데 집었다고 말한다: %q", again.Reason)
	}
	if strings.Contains(again.Reason, "선두 r-lead 를 선점했다") {
		t.Fatalf("재개를 선점으로 말한다: %q", again.Reason)
	}
	// 수는 참이면 살린다 — 쥔 것은 정말 2건이다.
	for _, want := range []string{"묶음 2건", "2건을 이 세션이 이미 쥐고 있다", "재출력"} {
		if !strings.Contains(again.Reason, want) {
			t.Fatalf("%q 가 사유에 없다 — 무엇을 쥐고 있는지가 사라졌다: %q", want, again.Reason)
		}
	}
}

// 섞인 갈래도 정직해야 한다: 선두는 재개인데 구성원은 새로 집었다.
// 이 갈래를 "전부 집었다"로도 "하나도 안 집었다"로도 접으면 안 된다.
func TestPickBundleMixedResumeAndClaimNamesBothCounts(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	for _, id := range []string{"x-lead", "x-m1"} {
		addItem(t, s, "p", id, []string{"services/" + id + ".go"}, nil)
	}
	if _, err := s.Pick(ctx(), PickInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "x-lead"}); err != nil {
		t.Fatalf("선두 단독 선점 실패: %v", err)
	}

	res, err := s.Pick(ctx(), PickInput{
		Project: "p", SessionID: me.Session.ID, ItemIDs: []string{"x-lead", "x-m1"}})
	if err != nil {
		t.Fatalf("묶음 선점 실패: %v", err)
	}
	if !strings.Contains(res.Reason, "2건을 이 세션이 쥐고 있고") ||
		!strings.Contains(res.Reason, "1건을 이번에 새로 집었다") {
		t.Fatalf("쥔 수와 새로 집은 수를 갈라 말하지 않는다: %q", res.Reason)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// finding 5 — item_id 경로도 묶음 축을 읽는다
// ─────────────────────────────────────────────────────────────────────────────

// TestPickExplicitCarriesBundleAxis 는 nil 의 뜻을 **하나로** 지킨다.
//
// 이 브랜치가 세운 계약: nil = 이 응답은 그 축을 안 읽었다(구서버 · 옛 캐시),
// 구성원 0건 = 읽었고 함께 낼 것이 없었다. 이 경로가 nil 을 내면 화면은
// "낡은 캐시이거나 서버가 이 축을 모르는 판이다"를 찍는데, 이것은 현행 서버의
// **신선한 온라인 응답**이다 — 두 원인 다 거짓이고, 그걸 읽은 세션은 있지도 않은
// 서버 스큐를 고치러 간다. pick 다섯 갈래 중 셋이 이 자리를 지난다.
func TestPickExplicitCarriesBundleAxis(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "나")
	addItem(t, s, "p", "e1", []string{"services/a.go"}, nil)

	claim, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID, ItemID: "e1"})
	if err != nil {
		t.Fatalf("선점 실패: %v", err)
	}
	if claim.Bundle == nil {
		t.Fatal("item_id 로 집었는데 묶음 축이 nil 이다 — 화면이 '서버가 이 축을 모른다'를 찍는다")
	}
	if len(claim.Bundle.Members) != 0 {
		t.Fatalf("이웃을 찾지도 않았는데 구성원이 있다: %+v", claim.Bundle.Members)
	}
	// 구성원 0건이 "이웃이 없다"로 읽히면 안 된다 — 안 찾은 것이다.
	if !strings.Contains(claim.Bundle.Scope, "안 봤다") {
		t.Fatalf("구성원 0건의 뜻을 범위가 말하지 않는다: %q", claim.Bundle.Scope)
	}

	// 재개 갈래도 같은 축을 낸다 — 여기만 nil 이면 재개 응답에서 결함이 되살아난다.
	resume, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID, ItemID: "e1"})
	if err != nil {
		t.Fatalf("재개 실패: %v", err)
	}
	if resume.Mode != PickResumed {
		t.Fatalf("전제가 깨졌다 — mode 가 %q 다", resume.Mode)
	}
	if resume.Bundle == nil {
		t.Fatal("재개 응답의 묶음 축이 nil 이다")
	}

	// 그리고 묶음 선두로 들어와도 pickBundle 이 자기 BundleInfo 로 덮어써야 한다 —
	// 여기 값이 새어 나가면 진짜 묶음이 "이웃을 안 찾았다"고 말하게 된다.
	addItem(t, s, "p", "e2", []string{"services/b.go"}, nil)
	addItem(t, s, "p", "e3", []string{"services/c.go"}, nil)
	b, err := s.Pick(ctx(), PickInput{
		Project: "p", SessionID: me.Session.ID, ItemIDs: []string{"e2", "e3"}})
	if err != nil {
		t.Fatalf("묶음 선점 실패: %v", err)
	}
	if b.Bundle == nil || len(b.Bundle.Members) != 1 {
		t.Fatalf("묶음 응답의 구성원이 1건이 아니다: %+v", b.Bundle)
	}
	if strings.Contains(b.Bundle.Scope, "안 봤다") {
		t.Fatalf("단독 경로의 범위 문장이 묶음 응답에 샜다: %q", b.Bundle.Scope)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// finding 1 — 응답이 무엇을 설명했는지는 순수 함수가 센다
// ─────────────────────────────────────────────────────────────────────────────

func TestAccountedIDsReadsEveryPlaceAnIDCanLive(t *testing.T) {
	res := PickResult{
		Item:   &model.Item{ID: "lead"},
		Branch: "lead",
		Bundle: &BundleInfo{Members: []BundleMember{
			{Item: model.Item{ID: "claimed-mem"}, Claimed: true},
			// 조회조차 실패한 구성원 — 계약상 Item.ID 만 채워진다.
			{Item: model.Item{ID: "ghost"},
				Rejection: &model.Rejection{Item: "ghost", Reason: RejectClaimNotFound}},
		}},
	}
	got := strings.Join(res.AccountedIDs(), ",")
	if got != "lead,claimed-mem,ghost" {
		t.Fatalf("설명한 id 집합이 %q 다 — 못 집은 구성원도 '설명했다'에 든다", got)
	}

	// 묶음 축이 없는 응답(구서버)은 선두 하나만 설명한다. 그것이 이 대조의 요점이다.
	old := PickResult{Item: &model.Item{ID: "lead"}, Branch: "lead"}
	if got := strings.Join(old.AccountedIDs(), ","); got != "lead" {
		t.Fatalf("묶음 없는 응답이 %q 를 설명했다고 한다", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// finding 6 — finish 는 남은 선점을 이름으로 부른다
// ─────────────────────────────────────────────────────────────────────────────

// TestFinishNamesItemsStillHeld 는 묶음과 finish 의 비대칭을 응답이 말하게 한다.
//
// finish 는 항목을 **하나만** 닫는다(항목마다 자기 판단이 필요하다 — 그 설계는
// 안 바꾼다). 그런데 pick 은 이제 묶음을 집는다. 그래서 3건을 집은 세션이 finish 를
// 한 번 부르면 2건이 선점된 채 남는데, 그 사실을 말하는 표면이 하나도 없었다:
// Tx.FinishItem 은 닫은 항목만 반납하고, 세션이 생존 창을 벗어나면 보드에서도
// 사라진다. 만료도 세션 종료 반납도 없으므로 그 2건은 **사람이 강제로 풀 때까지
// 다른 어떤 세션도 못 집는다.**
func TestFinishNamesItemsStillHeld(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "나")
	ids := []string{"f-lead", "f-m1", "f-m2"}
	for _, id := range ids {
		addItem(t, s, "p", id, []string{"services/" + id + ".go"}, nil)
	}
	if _, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: me.Session.ID, ItemIDs: ids}); err != nil {
		t.Fatalf("묶음 선점 실패: %v", err)
	}
	// 대조 전제: 정말 3건을 쥐었나.
	if held, err := st.ClaimedItems(ctx(), me.Session.ID); err != nil || len(held) != 3 {
		t.Fatalf("전제가 깨졌다 — 쥔 항목이 %v(%v)", held, err)
	}

	out, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "f-lead", Outcome: model.ItemDone,
		Title: "선두 마무리", Body: "왜 그렇게 했는지",
		Followups: []FollowupInput{{ID: "f-next", Title: "후속", Body: "다음에 할 것"}},
	})
	if err != nil {
		t.Fatalf("마무리 실패: %v", err)
	}
	if out.StillHeld == nil {
		t.Fatal("남은 선점 축이 nil 이다 — 렌더가 '못 읽었다'로 찍는데 실은 읽을 수 있었다")
	}
	got := strings.Join(*out.StillHeld, ",")
	if got != "f-m1,f-m2" {
		t.Fatalf("남은 선점이 %q 다 — f-m1,f-m2 를 기대했다(닫은 항목은 빠지고 나머지는 전부 온다)", got)
	}
}

// 진짜 0건은 **0건이라고 말한다.** nil 로 접으면 렌더가 "못 읽었다"를 찍고,
// 그러면 남은 선점이 없는 세션까지 매번 `fd status` 를 돌리게 된다.
func TestFinishStillHeldIsEmptyNotNilWhenNothingRemains(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "나")
	addItem(t, s, "p", "only", nil, nil)
	claimed(t, s, "p", me.Session.ID, "only")

	out, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "only", Outcome: model.ItemDone,
		Title: "마무리", Body: "본문",
		Followups: []FollowupInput{{ID: "only-next", Title: "후속", Body: "다음"}},
	})
	if err != nil {
		t.Fatalf("마무리 실패: %v", err)
	}
	if out.StillHeld == nil {
		t.Fatal("읽을 수 있었는데 nil 이다 — 관측과 미관측이 접혔다")
	}
	if len(*out.StillHeld) != 0 {
		t.Fatalf("남은 선점이 %v 다 — 0건이어야 한다(방금 닫은 것을 다시 세면 안 된다)", *out.StillHeld)
	}
}
