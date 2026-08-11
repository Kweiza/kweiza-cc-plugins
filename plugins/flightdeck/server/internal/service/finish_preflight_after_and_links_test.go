package service

import (
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 이 파일이 잠그는 것: **판단을 죽이지 않고 거절한다.**
//
// `Finish` 의 넷(판단·후속·종료·반납)은 한 트랜잭션이고, 그 안에서 오류를 올리면 **판단이
// 함께 롤백된다.** 판단은 이 시스템에서 원리적으로 파생 불가한 유일한 자산이라(설계 §5),
// 입력이 틀렸다는 사실은 **tx 진입 전에** 말해야 한다. 세션의 손에 본문이 남아 있는 그
// 자리에서 거절하면 되부르면 되고, tx 안에서 죽으면 그 본문은 사라진다.
//
// 나머지 입력(title·body·경로 좌표·자격·중복 id)은 이미 전단 관문으로 옮겨져 있었고,
// **이 둘만 tx 안에 남아 있었다**(2026-08-06 랜딩 전 전수 리뷰가 지목).
//
//	· followups[].after 의 형식 위반 → store.ValidateAfter 가 tx 안 addAfter 에서 올린다
//	· links[].target_kind 가 열거 밖  → judgment_link 의 CHECK 위반으로 tx 안에서 죽는다
//
// ★ **심장 단정은 `item.finish` 이벤트가 0건이라는 것이다.** 그 이벤트는 tx 안에서 **가장
// 먼저 예약**되고 `store` 의 지연 발신이 롤백 뒤에도 흘려보낸다(그것은 의도다 — 무엇을
// 시도했다 실패했나가 원장에 남아야 한다). 그러므로 그 이벤트가 **없다**는 것은 tx 에
// 진입조차 안 했다는 뜻이고, 그것이 "전단에서 막았다"의 유일한 기계적 증거다. 판단 0건만
// 세면 tx 안에서 죽어 롤백된 경우와 구별되지 않는다 — 그 구별이 이 항목의 전부다.

// after 의 축이 0개면 전단에서 거절한다 — tx 에 들어가지 않는다.
func TestFinishRefusesEmptyFollowupAfterBeforeTheTransaction(t *testing.T) {
	assertRefusedBeforeTx(t, GateFollowupAfter, func(me SessionResult) FinishInput {
		return FinishInput{
			Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
			Outcome: model.ItemDone, Title: "끝냈다", Body: "판단 본문",
			Followups: []FollowupInput{{
				ID: "new-followup", Title: "제목", Body: "본문",
				// 축이 하나도 안 채워졌다. MCP 로 도달 가능하다 —
				// afterSchema 에 required 가 없어 클라이언트가 못 막는다.
				After: []model.After{{}},
			}},
		}
	})
}

// after 의 축이 둘이면 전단에서 거절한다 — 정확히 하나여야 한다.
func TestFinishRefusesAmbiguousFollowupAfterBeforeTheTransaction(t *testing.T) {
	assertRefusedBeforeTx(t, GateFollowupAfter, func(me SessionResult) FinishInput {
		return FinishInput{
			Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
			Outcome: model.ItemDone, Title: "끝냈다", Body: "판단 본문",
			Followups: []FollowupInput{{
				ID: "new-followup", Title: "제목", Body: "본문",
				After: []model.After{{Item: "some-item", SHA: "deadbeef"}},
			}},
		}
	})
}

// links 의 target_kind 가 열거 밖이면 전단에서 거절한다 — CHECK 까지 가지 않는다.
//
// 이 갈래는 HTTP 경로(internal/api/handlers_items.go)로 도달한다. 그쪽은 target_kind 를
// 문자열로 그대로 받아 model.JudgmentLink 에 넣는다.
func TestFinishRefusesUnknownLinkKindBeforeTheTransaction(t *testing.T) {
	assertRefusedBeforeTx(t, GateJudgmentLinkKind, func(me SessionResult) FinishInput {
		return FinishInput{
			Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
			Outcome: model.ItemDone, Title: "끝냈다", Body: "판단 본문",
			Links: []model.JudgmentLink{{TargetKind: "pull-request", TargetID: "42"}},
		}
	})
}

// target_kind 가 비어도 같은 관문이 잡는다 — 빈 값은 CHECK 도 통과하지 못한다.
func TestFinishRefusesEmptyLinkKindBeforeTheTransaction(t *testing.T) {
	assertRefusedBeforeTx(t, GateJudgmentLinkKind, func(me SessionResult) FinishInput {
		return FinishInput{
			Project: "p", SessionID: me.Session.ID, ItemID: "batch7",
			Outcome: model.ItemDone, Title: "끝냈다", Body: "판단 본문",
			Links: []model.JudgmentLink{{TargetKind: "", TargetID: "x"}},
		}
	})
}

// assertRefusedBeforeTx 는 이 파일의 네 시험이 공유하는 골격이다.
//
// 잠그는 것 넷: ⓐ 거절된다 ⓑ **`item.finish` 가 0건**(tx 미진입) ⓒ 판단이 0건 ⓓ 거절
// 이벤트가 어느 관문인지 이름을 댄다. ⓑ 가 심장이고 ⓒ 만으로는 롤백과 구별이 안 된다.
func assertRefusedBeforeTx(t *testing.T, wantGate FinishGate, build func(SessionResult) FinishInput) {
	t.Helper()
	s, st := newSvc(t)
	repo, wt := newRepoWithWorktree(t, "feat")
	me := openSession(t, s, "p", repo, wt, "cc-1", "트랙2")
	addItem(t, s, "p", "batch7", nil, nil)
	claimed(t, s, "p", me.Session.ID, "batch7")

	if _, err := s.Finish(ctx(), build(me)); err == nil {
		t.Fatalf("틀린 입력인데 마무리가 성공했다 — 이 시험의 전제가 깨졌다")
	}

	// ⓑ 심장. tx 에 들어갔다면 이 이벤트가 롤백 뒤에도 흘러와 1건이 된다.
	if n := countRows(t, st, `SELECT count(*) FROM event WHERE kind='item.finish'`); n != 0 {
		t.Errorf("트랜잭션에 진입했다 — item.finish 가 %d건이다. "+
			"전단에서 막지 않으면 이 자리에서 **판단이 롤백된다**", n)
	}
	// ⓒ 판단이 하나도 안 남았다(전단 거절이므로 애초에 쓰지 않았다).
	if n := countRows(t, st, `SELECT count(*) FROM judgment`); n != 0 {
		t.Errorf("거절인데 판단이 %d건 남았다", n)
	}
	// ⓓ 어느 관문이 끊었는지 원장이 말한다.
	p := readEventPayload(t, st, "item.finish.refused")
	if p["gate"] != string(wantGate) {
		t.Errorf("거절 관문이 %q 가 아니라 %v 다 — 사유를 못 좁힌다", wantGate, p["gate"])
	}
	if p["item"] != "batch7" {
		t.Errorf("거절 이벤트의 항목 좌표가 틀렸다: %v", p)
	}
	// 항목은 아직 안 닫혔고 선점도 살아 있다 — 세션이 고쳐서 되부를 수 있는 상태여야 한다.
	// (claimed 다. 위 claimed() 헬퍼가 pick 으로 선점을 만들었으므로 open 이 아니다.)
	if n := countRows(t, st,
		`SELECT count(*) FROM item WHERE project='p' AND id='batch7' AND state='claimed'`); n != 1 {
		var got string
		_ = st.DB().QueryRowContext(ctx(),
			`SELECT state FROM item WHERE project='p' AND id='batch7'`).Scan(&got)
		t.Errorf("거절이 항목 상태를 건드렸다(지금 %q) — 되부를 수 없게 된다", got)
	}
	if n := countRows(t, st,
		`SELECT count(*) FROM claim WHERE project='p' AND item_id='batch7' AND released_at IS NULL`); n != 1 {
		t.Errorf("거절이 선점을 반납해 버렸다 — 되부르려면 다시 집어야 한다")
	}
}
