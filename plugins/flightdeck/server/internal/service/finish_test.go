package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// claimed 는 항목 하나를 이 세션의 선점 상태로 만든다.
func claimed(t *testing.T, s *Service, project, sessionID, itemID string) {
	t.Helper()
	res, err := s.Pick(ctx(), PickInput{Project: project, SessionID: sessionID, ItemID: itemID})
	if err != nil {
		t.Fatalf("선점 실패(%s): %v", itemID, err)
	}
	if res.Mode != PickClaimed {
		t.Fatalf("선점 상태를 못 만들었다: %s", res.Mode)
	}
}

func TestFinishWithoutBodyRefusesAndSaysWhatToWrite(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")

	// 대조 성립 단정 — 읽기 전에 먼저.
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 0 {
		t.Fatalf("사전 조건이 깨졌다 — 판단이 이미 %d건 있다", n)
	}

	_, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone, Body: "",
	})
	if err == nil {
		t.Fatalf("본문 없는 마무리는 거절돼야 한다")
	}

	// ★ 소비자 좌표계 — MCP·REST 가 사용자에게 그대로 보이는 것은 이 문자열이다.
	msg := err.Error()
	for _, want := range []string{
		"왜 그렇게 했나",
		"무엇을 기각했나",
		"일부러 안 한 것",
		"확인했으나 못 한 것",
		"followups",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("거절 응답에 %q 가 없다 — 무엇을 적어야 하는지를 그 자리에서 내야 한다:\n%s", want, msg)
		}
	}

	// 거절은 아무것도 쓰지 않는다.
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 0 {
		t.Fatalf("거절했는데 판단이 %d건 저장됐다", n)
	}
	it, err := st.GetItem(ctx(), "p", "batch7")
	if err != nil {
		t.Fatalf("항목 조회 실패: %v", err)
	}
	if it.State != model.ItemClaimed {
		t.Fatalf("거절했는데 항목 상태가 %s 로 바뀌었다", it.State)
	}
}

func TestFinishRollsBackEverythingWhenFollowupFails(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "batch7", nil, nil)
	addItem(t, s, "p", "already-there", nil, nil) // 후속과 id 가 부딪힐 항목
	claimed(t, s, "p", me.Session.ID, "batch7")

	// ★ 대조가 성립하는지 **결과를 읽기 전에** 단정한다.
	//   ① 충돌 상대가 실제로 있어야 후속 등록이 정말 실패한다
	//   ② 끝내려는 항목이 선점 상태여야 "종료가 롤백됐다"가 의미를 가진다
	//   ③ 판단이 0건이어야 "판단도 안 남았다"를 말할 수 있다
	if n := countRows(t, st, `SELECT count(*) FROM item WHERE project='p' AND id='already-there'`); n != 1 {
		t.Fatalf("사전 조건이 깨졌다 — 충돌 상대가 %d건이다", n)
	}
	before, err := st.GetItem(ctx(), "p", "batch7")
	if err != nil {
		t.Fatalf("항목 조회 실패: %v", err)
	}
	if before.State != model.ItemClaimed {
		t.Fatalf("사전 조건이 깨졌다 — batch7 상태가 %s 다", before.State)
	}
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 0 {
		t.Fatalf("사전 조건이 깨졌다 — 판단이 이미 %d건 있다", n)
	}

	_, err = s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone,
		Title:   "batch7 랜딩",
		Body:    "①…②…③…④…",
		Followups: []FollowupInput{
			{ID: "already-there", Title: "후속", Body: "본문"},
		},
	})
	if err == nil {
		t.Fatalf("id 가 부딪히는 후속은 실패해야 한다")
	}
	if !strings.Contains(err.Error(), "후속 항목 already-there 등록 실패") {
		t.Fatalf("실패 사유가 후속 등록임을 말하지 않는다: %v", err)
	}

	// ★ 한 트랜잭션이었는가 — 넷 전부가 되돌아가야 한다.
	after, err := st.GetItem(ctx(), "p", "batch7")
	if err != nil {
		t.Fatalf("항목 조회 실패: %v", err)
	}
	if after.State != model.ItemClaimed {
		t.Fatalf("후속이 실패했는데 항목이 %s 로 종료됐다 — 한 트랜잭션이 아니다", after.State)
	}
	if after.ClosedAt != nil {
		t.Fatalf("closed_at 이 남았다: %v", after.ClosedAt)
	}
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 0 {
		t.Fatalf("판단이 %d건 남았다 — 후속 실패에 함께 롤백되지 않았다", n)
	}
	cl, err := st.GetClaim(ctx(), "p", "batch7")
	if err != nil {
		t.Fatalf("선점 조회 실패: %v", err)
	}
	if cl.ReleasedAt != nil {
		t.Fatalf("선점이 반납됐다 — 종료가 롤백됐으면 반납도 롤백돼야 한다")
	}
	// 계측은 트랜잭션과 별개로 남는다 — "무엇을 시도했다 실패했나"가 감사 원장의 존재 이유다.
	if n := countRows(t, st, `SELECT count(*) FROM event WHERE kind='item.finish'`); n != 1 {
		t.Fatalf("실패한 시도가 원장에 %d건 — 롤백돼도 남아야 한다", n)
	}
	// 그리고 **왜** 실패했는지도 남아야 한다. 시도만 남으면 실패율은 세지되
	// 무엇을 고쳐야 하는지는 답하지 못한다.
	var payload string
	if err := st.DB().QueryRowContext(ctx(),
		`SELECT payload FROM event WHERE kind='item.finish.fail' ORDER BY id DESC LIMIT 1`).
		Scan(&payload); err != nil {
		t.Fatalf("실패 사유 이벤트가 없다: %v", err)
	}
	if !strings.Contains(payload, "already-there") {
		t.Fatalf("실패 사유에 무엇이 부딪혔는지가 없다: %s", payload)
	}
}

func TestFinishIsOneCallForJudgmentFollowupCloseAndRelease(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "batch7", []string{"pipeline/"}, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")

	// 이 세션이 자원을 쥔 상태로 시작한다(반납이 마무리의 네 번째 몫이다).
	if _, err := st.AcquireResource(ctx(), "p", "staging",
		store.Holder{SessionID: me.Session.ID}); err != nil {
		t.Fatalf("자원 점유 준비 실패: %v", err)
	}

	res, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone,
		Title:   "batch7 랜딩",
		Body:    "① 왜 그렇게 했나 … ④ 확인했으나 못 한 것 …",
		Followups: []FollowupInput{
			{ID: "batch8", Title: "다음 배치", Body: "batch7 에서 나온 후속", Paths: []string{"pipeline/"}},
		},
	})
	if err != nil {
		t.Fatalf("마무리 실패: %v", err)
	}

	if res.Item.State != model.ItemDone {
		t.Fatalf("항목이 안 닫혔다: %s", res.Item.State)
	}
	if len(res.Followups) != 1 || res.Followups[0].ID != "batch8" {
		t.Fatalf("후속이 안 들어갔다: %+v", res.Followups)
	}
	if got, err := st.GetItem(ctx(), "p", "batch8"); err != nil || got.State != model.ItemOpen {
		t.Fatalf("후속이 열린 항목으로 안 남았다: %+v %v", got, err)
	}
	if len(res.Released) != 1 || res.Released[0] != "staging" {
		t.Fatalf("자원이 안 반납됐다: %v", res.Released)
	}
	if _, err := st.HeldBy(ctx(), "p", "staging"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("자원 점유가 살아 있다: %v", err)
	}
	cl, err := st.GetClaim(ctx(), "p", "batch7")
	if err != nil || cl.ReleasedAt == nil {
		t.Fatalf("선점이 안 반납됐다: %+v %v", cl, err)
	}

	// 판단 ↔ 항목·후속이 FK 로 이어져야 한다. 경로 문자열 포인터였다면 끊어질 자리다.
	j, err := st.GetJudgment(ctx(), res.Judgment.ID)
	if err != nil {
		t.Fatalf("판단 조회 실패: %v", err)
	}
	if j.Kind != model.JudgmentHandoff {
		t.Fatalf("판단 종류가 %s 다", j.Kind)
	}
	links := map[string]bool{}
	for _, l := range j.Links {
		links[l.TargetKind+":"+l.TargetID] = true
	}
	for _, want := range []string{"item:batch7", "item:batch8"} {
		if !links[want] {
			t.Fatalf("판단 링크 %q 가 없다: %v", want, j.Links)
		}
	}
}

func TestFinishRefusesSomeoneElsesItem(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	other := openSession(t, s, "p", repo, repo, "cc-2", "트랙7")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", other.Session.ID, "batch7")

	_, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
		Outcome: model.ItemDone, Body: "본문",
	})
	if err == nil {
		t.Fatalf("남이 쥔 항목은 끝낼 수 없어야 한다")
	}
	var held *store.ClaimHeldError
	if !errors.As(err, &held) || held.Holder != other.Session.ID {
		t.Fatalf("점유자를 담은 오류여야 한다: %T %v", err, err)
	}
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 0 {
		t.Fatalf("거절했는데 판단이 %d건 남았다 — 판단만 남고 종료가 안 되는 반쪽 상태다", n)
	}
}

func TestNoteCountsRecipientsAndKeepsHistoryAppendOnly(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	other := openSession(t, s, "p", repo, repo, "cc-2", "트랙7")

	first, err := s.Note(ctx(), NoteInput{
		Project: "p", SessionID: me.Session.ID, Kind: model.JudgmentAsk,
		Title: "건드리면 곤란한 것", Body: "contracts/ 는 이번 주기에 내가 고친다",
	})
	if err != nil {
		t.Fatalf("판단 저장 실패: %v", err)
	}
	if len(first.Recipients) != 1 || first.Recipients[0] != other.Session.ID {
		t.Fatalf("받을 세션이 틀렸다: %v", first.Recipients)
	}

	// 정정은 새 행 + supersedes 다. 덮어쓰기 경로가 존재하지 않는다.
	second, err := s.Note(ctx(), NoteInput{
		Project: "p", SessionID: me.Session.ID, Kind: model.JudgmentAsk,
		Body: "정정: contracts/raw-envelope 만 내가 고친다", Supersedes: first.Judgment.ID,
	})
	if err != nil {
		t.Fatalf("정정 저장 실패: %v", err)
	}
	if second.Judgment.ID == first.Judgment.ID {
		t.Fatalf("정정이 같은 행을 덮었다")
	}
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 2 {
		t.Fatalf("판단이 %d건이다 — 원문이 남아야 한다", n)
	}

	if _, err := s.Note(ctx(), NoteInput{
		Project: "p", SessionID: me.Session.ID, Kind: model.JudgmentKind("메모"), Body: "x",
	}); err == nil {
		t.Fatalf("열거 밖 종류는 거절돼야 한다")
	}
	if _, err := s.Note(ctx(), NoteInput{
		Project: "p", SessionID: me.Session.ID, Kind: model.JudgmentNow, Body: "   ",
	}); err == nil {
		t.Fatalf("공백만 든 본문은 거절돼야 한다")
	}
}

func TestAllocIsAtomicAndStartsAtOne(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	openSession(t, s, "p", repo, repo, "cc-1", "")

	first, err := s.Alloc(ctx(), "p", "contract_revision")
	if err != nil {
		t.Fatalf("발번 실패: %v", err)
	}
	if first != 1 {
		t.Fatalf("첫 발급이 %d 다 — 0 은 '아직 안 씀'과 구분돼야 한다", first)
	}
	second, err := s.Alloc(ctx(), "p", "contract_revision")
	if err != nil {
		t.Fatalf("발번 실패: %v", err)
	}
	if second != 2 {
		t.Fatalf("두 번째 발급이 %d 다", second)
	}
	if _, err := s.Alloc(ctx(), "p", ""); err == nil {
		t.Fatalf("이름 없는 발번은 거절돼야 한다")
	}
}

func TestHealthAndDoctorNameWhatWasNotObserved(t *testing.T) {
	repo := newRepo(t)
	s, _ := newSvc(t)
	openSession(t, s, "p", repo, repo, "cc-1", "")

	h := s.Health(ctx())
	if !h.OK || !h.DBOK {
		t.Fatalf("DB 가 살아 있는데 헬스가 %+v 다", h)
	}
	if h.APIVersion == "" {
		t.Fatalf("api_version 이 비었다 — 클라이언트·서버 스큐를 알릴 축이다")
	}
	if h.DiskKnown && (h.DiskFreePct < 0 || h.DiskFreePct > 100) {
		t.Fatalf("디스크 여유가 %f%% 다", h.DiskFreePct)
	}
	if !h.DiskKnown && h.DiskError == "" {
		t.Fatalf("못 쟀으면 사유가 있어야 한다 — 0%%와 '못 쟀다'를 뭉개면 상시 빨간불이 된다")
	}

	// 환경을 주입해 "무엇이 관측되고 무엇이 안 됐는지"를 이름으로 내는지 본다.
	sd := New(s.st, nil, WithEnv(func(k string) (string, bool) {
		if k == "CLAUDE_CODE_SESSION_ID" {
			return "cc-1", true
		}
		return "", false
	}))
	rep := sd.Doctor(ctx())
	seen := map[string]bool{}
	for _, a := range rep.Platform {
		seen[a.Name] = a.Observed
	}
	if !seen["CLAUDE_CODE_SESSION_ID"] {
		t.Fatalf("관측된 축이 관측으로 안 잡혔다")
	}
	if _, ok := seen["CLAUDE_PLUGIN_ROOT"]; !ok {
		t.Fatalf("관측 안 된 축이 목록에서 통째로 빠졌다 — 부재를 기본값으로 접으면 그 사실이 영영 안 보인다")
	}
	if len(rep.Projects) != 1 || !rep.Projects[0].Readable || rep.Projects[0].HeadSHA == "" {
		t.Fatalf("프로젝트 git 도달성을 실제로 재지 않았다: %+v", rep.Projects)
	}
}

// finish 의 followup 은 t.AddItem 을 직접 불러 add(AddItem)의 좌표계 검증 루프를
// 거치지 않는 우회 문이었다 — followup 경로도 같은 관문(judgeItemPathsCoordinate)을
// 거쳐야 한다. 안 그러면 같은 사람이 같은 세션에서 add 는 거절당하고 finish 는
// 조용히 통과하는 반쪽 관문이 된다.
func TestFinishRejectsFollowupWithBadCoordinatePaths(t *testing.T) {
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-coord", "좌표계")
	addItem(t, s, "p", "coord-main", nil, nil)
	claimed(t, s, "p", me.Session.ID, "coord-main")

	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 0 {
		t.Fatalf("사전 조건이 깨졌다 — 판단이 이미 %d건 있다", n)
	}

	_, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "coord-main",
		Outcome: model.ItemDone,
		Title:   "coord-main 랜딩",
		Body:    "① … ④ …",
		Followups: []FollowupInput{
			{ID: "coord-fu", Title: "다음 배치", Body: "본문",
				Paths: []string{"a/b.go", `internal\api\x.go`}},
		},
	})
	if err == nil {
		t.Fatal("좌표계가 틀린 후속 경로를 통과시켰다")
	}
	msg := err.Error()
	// 몇 번째 후속인지와, 그 후속 안에서 몇 번째 경로인지를 **둘 다** 말해야 한다.
	//
	// ★ 둘 다 1-based 다. 후속은 하나뿐이므로 "1번째 후속"이고, 그 안에서 틀린 것은
	// 두 번째 경로(`internal\api\x.go`)이므로 "2번째 경로"다. 전에는 각각 "0번째"·
	// "1번째"였다 — 사람이 세는 수와 어긋나 있었다.
	if !strings.Contains(msg, "1번째 후속") {
		t.Errorf("몇 번째 후속인지 안 말한다(1-based 여야 한다): %s", msg)
	}
	if !strings.Contains(msg, "2번째 경로") {
		t.Errorf("후속 안에서 몇 번째 경로인지 안 말한다(1-based 여야 한다): %s", msg)
	}
	if !strings.Contains(msg, "백슬래시") {
		t.Errorf("원인(백슬래시)을 안 짚는다: %s", msg)
	}

	// 거절이면 아무것도 안 쓴다 — 다른 followup 검증 실패와 같은 규율(§ 위 롤백 시험).
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 0 {
		t.Fatalf("거절했는데 판단이 %d건 저장됐다", n)
	}
}

// 정상 좌표계의 followup 경로는 그대로 통과해야 한다 — 관문이 정상 입력까지 막으면
// 그것도 침묵만큼 나쁘다.
func TestFinishAcceptsFollowupWithGoodCoordinatePaths(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-coord-ok", "좌표계")
	addItem(t, s, "p", "coord-ok-main", nil, nil)
	claimed(t, s, "p", me.Session.ID, "coord-ok-main")

	res, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "coord-ok-main",
		Outcome: model.ItemDone,
		Title:   "coord-ok-main 랜딩",
		Body:    "① … ④ …",
		Followups: []FollowupInput{
			{ID: "coord-ok-fu", Title: "다음 배치", Body: "본문",
				Paths: []string{"internal/api/x.go", "Makefile"}},
		},
	})
	if err != nil {
		t.Fatalf("좌표계가 맞는 후속 경로를 거절했다: %v", err)
	}
	if len(res.Followups) != 1 || len(res.Followups[0].Paths) != 2 {
		t.Fatalf("후속 경로가 안 들어갔다: %+v", res.Followups)
	}
}
