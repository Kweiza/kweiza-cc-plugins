package service

import (
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 이 파일이 잠그는 부류: **원장이 이미 말한 뒤**(커밋 완료) 보조 조회가 실패하면, 이미 만든
// 결과를 버리지 않고 못 읽은 사실을 고백하며 낸다. pick.go 의 notesOrNote 가 선례이고
// (pick_notes_partial_test.go), 여기는 같은 부류의 남은 둘 — Note()·Finish() — 이다.
//
// 왜 비싼가: 둘 다 판단(AddJudgment)을 커밋한 뒤의 자리다. 판단 표는 추가 전용이라
// ("덮어쓰기는 없다") 세션이 500 을 보고 재시도하면 같은 판단이 **두 행** 남는다.
// 파생 불가한 유일한 자산에 유령 중복이 쌓이는 것이라, 잃는 것이 응답 하나가 아니다.

func hasAxis(fs []DerivedFailure, axis string) bool {
	for _, f := range fs {
		if f.Axis == axis {
			return true
		}
	}
	return false
}

// Note 는 판단을 커밋한 뒤 수신자 파생이 실패해도 저장 확인을 낸다.
//
// ★ 격리 수법: signal 표를 숨긴다. Note 의 트랜잭션(judgment·judgment_link·event)은 signal
// 을 안 읽고 — 판단 저장의 Beat 는 지워졌다(Note 본문 주석) — 수신자 파생(sessionCards →
// ListLive)만 signal 의 EXISTS 를 지난다(store/session.go). landing_test.go 의
// TestLaneReleaseJudgmentSaysWhenTheSignalCouldNotBeRead 와 같은 수법이다.
func TestNoteSurvivesUnreadableRecipients(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "")

	if _, err := st.DB().ExecContext(ctx(), `ALTER TABLE signal RENAME TO signal_hidden`); err != nil {
		t.Fatalf("signal 표 숨기기 실패(시험 전제 준비): %v", err)
	}

	res, err := s.Note(ctx(), NoteInput{
		Project: "p", SessionID: me.Session.ID,
		Kind: model.JudgmentDecision, Title: "제목", Body: "본문",
	})
	if err != nil {
		t.Fatalf("수신자 파생이 실패했다고 note 가 오류를 올렸다 — 판단은 이미 커밋됐다. "+
			"세션이 이 오류를 보고 재시도하면 같은 판단이 두 행 남는다(추가 전용):\n%v", err)
	}
	if res.Judgment.ID == "" {
		t.Fatal("응답에 판단 id 가 없다 — 저장 확인이 응답의 존재 이유다")
	}
	// 원장 확인 — 응답이 말한 판단이 실제로 있다. 이것이 "재시도 안 해도 된다"의 근거다.
	if _, gerr := st.GetJudgment(ctx(), res.Judgment.ID); gerr != nil {
		t.Fatalf("응답은 저장됐다는데 원장에서 못 읽는다: %v", gerr)
	}
	// 고백 — 수신자 축을 못 읽었다는 사실이 응답에 있어야 한다. 없으면 recipients=nil 이
	// "받을 세션 없음"으로 읽혀 0(없다)과 못 잼이 같은 화면이 된다.
	if !hasAxis(res.Failures, "recipients") {
		t.Fatalf("수신자를 못 읽었는데 recipients 축 고백이 없다. Failures: %+v", res.Failures)
	}
	if len(res.Recipients) != 0 {
		t.Fatalf("못 읽었다면서 수신자를 냈다: %v — 관측 없는 단정이다", res.Recipients)
	}
}

// GetProject 실패 갈래도 같은 축으로 고백한다 — 이전에는 **침묵**으로 접혔다(perr 무시).
//
// ★ 위 시험과 다른 갈래다. signal 숨기기는 sessionCards(둘째 갈래)만 깨고 GetProject 는
// project 표라 멀쩡하다 — 이 시험이 없으면 첫째 갈래를 하드 500 으로 되돌리거나 d.fail 을
// 지워도 전 스위트가 초록이다(검토가 변이로 실증했다).
//
// ★ 격리 수법: project 행의 created_at 을 오염시킨다. 행 자체는 남아 판단의 FK 는 만족되고
// (트랜잭션이 커밋된다), 커밋 뒤 GetProject 의 parseTime 만 실패한다(store/project.go).
func TestNoteSurvivesUnreadableProject(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "")

	if _, err := st.DB().ExecContext(ctx(),
		`UPDATE project SET created_at='엉망' WHERE id='p'`); err != nil {
		t.Fatalf("project 행 오염 실패(시험 전제 준비): %v", err)
	}

	res, err := s.Note(ctx(), NoteInput{
		Project: "p", SessionID: me.Session.ID,
		Kind: model.JudgmentDecision, Title: "제목", Body: "본문",
	})
	if err != nil {
		t.Fatalf("프로젝트 조회가 실패했다고 note 가 오류를 올렸다 — 판단은 이미 커밋됐다:\n%v", err)
	}
	if _, gerr := st.GetJudgment(ctx(), res.Judgment.ID); gerr != nil {
		t.Fatalf("응답은 저장됐다는데 원장에서 못 읽는다: %v", gerr)
	}
	if !hasAxis(res.Failures, "recipients") {
		t.Fatalf("프로젝트를 못 읽었는데 recipients 축 고백이 없다 — 이 갈래가 옛 침묵으로 "+
			"돌아간 것이다. Failures: %+v", res.Failures)
	}
}

// 정상 경로 짝 단정 — 고백이 상시 점등이면 판별력이 0이다.
func TestNoteRecipientsAxisSilentOnHappyPath(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "")
	other := openSession(t, s, "p", repo, repo+"/b", "cc-2", "")

	res, err := s.Note(ctx(), NoteInput{
		Project: "p", SessionID: me.Session.ID,
		Kind: model.JudgmentDecision, Title: "제목", Body: "본문",
	})
	if err != nil {
		t.Fatalf("Note: %v", err)
	}
	if hasAxis(res.Failures, "recipients") {
		t.Fatalf("정상 경로인데 recipients 고백이 켜져 있다 — 상시 점등은 판별력이 0이다. "+
			"Failures: %+v", res.Failures)
	}
	found := false
	for _, id := range res.Recipients {
		if id == other.Session.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("살아 있는 다른 세션이 수신자에 없다: %v (기대 %s)", res.Recipients, other.Session.ID)
	}
}

// Finish 는 트랜잭션을 커밋한 뒤 항목 되읽기가 실패해도 결과를 낸다.
//
// ★ 격리 수법: item_after 표를 숨긴다. item 표 자체는 트랜잭션(SetItemState 의 UPDATE)이
// 써야 하므로 못 숨긴다. **격리가 서는 진짜 근거는 이 픽스처의 두 영(0)이다**:
//
//	① 선점 뒤 item.add 이벤트가 0건 → 관문(sessionSpawnedOpen)의 후보 루프가 GetItem 을
//	   한 번도 안 부른다(후보마다 부른다 — "이벤트만 본다"가 아니다).
//	② 이 항목이 유일해서 커밋 뒤 열린 항목이 0건 → 큐 수지의 ListOpen 이 **열린 항목마다**
//	   afterOf 로 item_after 를 읽는데(store/item.go), 돌 행이 없어 안 읽는다.
//
// 그래서 이 호출에서 item_after 를 지나는 것은 커밋 뒤 GetItem 의 afterOf 뿐이다.
// 아래 QueueBalance 단정이 ② 를 잠근다 — 픽스처에 열린 항목을 하나 더 두면 ListOpen 이
// 숨긴 표에서 죽어 QueueBalance 가 nil 이 되고, 이 시험은 자기가 이중 실패를 재고 있음을
// 그 단정으로 알게 된다.
//
// ★ done 과 dropped 를 둘 다 돈다 — 합성 응답의 State·CloseReason 이 입력에서 와야지
// 상수로 박히면(예: 항상 done) dropped 응답이 "done 으로 닫았다"는 거짓을 낸다.
func TestFinishSurvivesUnreadableItemReadAfterCommit(t *testing.T) {
	for _, c := range []struct {
		name    string
		outcome model.ItemState
		reason  string
	}{
		{"done", model.ItemDone, ""},
		{"dropped", model.ItemDropped, "판이 바뀌어 뜻을 잃었다"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, st := newSvc(t)
			repo, wt := newRepoWithWorktree(t, "feat")
			me := openSession(t, s, "p", repo, wt, "cc-1", "")
			addItem(t, s, "p", "aux1", nil, nil)
			claimed(t, s, "p", me.Session.ID, "aux1")

			if _, err := st.DB().ExecContext(ctx(), `ALTER TABLE item_after RENAME TO item_after_hidden`); err != nil {
				t.Fatalf("item_after 표 숨기기 실패(시험 전제 준비): %v", err)
			}

			res, err := s.Finish(ctx(), FinishInput{
				Project: "p", SessionID: me.Session.ID, ItemID: "aux1",
				Outcome: c.outcome, Title: "끝냈다", Body: "본문", CloseReason: c.reason,
			})
			if err != nil {
				t.Fatalf("커밋 뒤 항목 되읽기가 실패했다고 finish 가 오류를 올렸다 — 판단·종료·반납은 "+
					"이미 커밋됐다. 세션이 재시도하면 판단이 중복되고 FinishItem 은 선점이 이미 "+
					"반납돼 거절된다:\n%v", err)
			}
			if res.Judgment.ID == "" {
				t.Fatal("응답에 판단 id 가 없다")
			}
			if _, gerr := st.GetJudgment(ctx(), res.Judgment.ID); gerr != nil {
				t.Fatalf("응답은 저장됐다는데 원장에서 못 읽는다: %v", gerr)
			}
			// 아는 사실은 응답에 있어야 한다 — 프로젝트·id·상태·폐기 사유는 방금 커밋한
			// 트랜잭션의 것이다. 렌더 첫 줄("finish · <id> 를 <state> 로 닫았다")이 이 값으로
			// 성립한다.
			if res.Item.Project != "p" || res.Item.ID != "aux1" ||
				res.Item.State != c.outcome || res.Item.CloseReason != c.reason {
				t.Fatalf("커밋으로 아는 사실(프로젝트·id·상태·사유)이 응답에 없다: %+v (기대 p·aux1·%s·%q)",
					res.Item, c.outcome, c.reason)
			}
			// 격리 주석의 ② 를 잠근다 — 큐 수지는 item_after 를 안 지나 살아 있어야 한다.
			// nil 이면 이 시험은 GetItem 하나가 아니라 이중 실패를 재고 있는 것이다.
			if res.QueueBalance == nil {
				t.Fatal("큐 수지가 nil 이다 — ListOpen 이 숨긴 item_after 에 걸렸다. " +
					"픽스처에 열린 항목이 남아 격리가 깨진 것이다(위 격리 주석 ②)")
			}
			// 고백 — 항목 전문을 못 읽었다는 사실이 응답에 있어야 한다.
			if !hasAxis(res.Failures, "item") {
				t.Fatalf("항목 전문을 못 읽었는데 item 축 고백이 없다. Failures: %+v", res.Failures)
			}
			// 원장 확인 — 상태는 실제로 닫혔다.
			var state string
			if err := st.DB().QueryRowContext(ctx(),
				`SELECT state FROM item WHERE project='p' AND id='aux1'`).Scan(&state); err != nil {
				t.Fatalf("항목 상태 확인 실패: %v", err)
			}
			if state != string(c.outcome) {
				t.Fatalf("항목 상태 %q, 원하는 것 %s — 트랜잭션이 커밋됐다는 전제가 틀렸다", state, c.outcome)
			}
		})
	}
}

// 정상 경로 짝 단정 — item 축 고백이 상시 점등이면 판별력이 0이다.
func TestFinishItemAxisSilentOnHappyPath(t *testing.T) {
	s, _ := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "")
	addItem(t, s, "p", "aux2", nil, nil)
	claimed(t, s, "p", me.Session.ID, "aux2")

	res, err := s.Finish(ctx(), FinishInput{
		Project: "p", SessionID: me.Session.ID, ItemID: "aux2",
		Outcome: model.ItemDone, Title: "끝냈다", Body: "본문",
	})
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if len(res.Failures) != 0 {
		t.Fatalf("정상 경로인데 파생 고백이 켜져 있다 — 상시 점등은 판별력이 0이다. "+
			"Failures: %+v", res.Failures)
	}
	// 정상 경로에서는 전문이 온다 — 제목이 그 증거다(항목 제목은 등록 때 필수라 빈 값이 불가능하다).
	if strings.TrimSpace(res.Item.Title) == "" {
		t.Fatalf("정상 경로인데 항목 전문이 비었다: %+v", res.Item)
	}
}
