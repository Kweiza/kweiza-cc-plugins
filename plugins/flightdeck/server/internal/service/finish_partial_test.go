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
// ★ 격리 수법: item_after 표를 숨긴다. item 표 자체는 트랜잭션(SetItemState 의 UPDATE)과
// 큐 수지(ListOpen — afterOf 를 안 부른다)가 써야 하므로 못 숨긴다. 커밋 뒤의
// GetItem 만 afterOf 로 item_after 를 읽는다 — 후속이 0건이면 그 표를 지나는 다른 조회가
// 이 호출에 없다(잇기·만들기 계획이 비고, 관문은 item.add 이벤트만 본다).
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
			// 아는 사실은 응답에 있어야 한다 — id·상태·폐기 사유는 방금 커밋한 트랜잭션의 것이다.
			// 렌더 첫 줄("finish · <id> 를 <state> 로 닫았다")이 이 값으로 성립한다.
			if res.Item.ID != "aux1" || res.Item.State != c.outcome || res.Item.CloseReason != c.reason {
				t.Fatalf("커밋으로 아는 사실(id·상태·사유)이 응답에 없다: %+v (기대 %s·%q)",
					res.Item, c.outcome, c.reason)
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
