package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 롤백된 finish 를 **실제로 일으켜** 그 이벤트가 원장에 남는 것을 본다.
//
// ★ 이벤트를 손으로 심으면 이 축의 전제 자체를 안 밟는다. 이 설계 전부가
// "Tx.LogEvent 는 롤백 뒤에도 흘러간다"(store.go 의 flushDeferred)에 얹혀 있는데,
// LogEvent 를 직접 부르면 그 문장을 시험이 한 번도 통과하지 않는다. 그래서 여기서는
// service.Finish 의 트랜잭션 모양을 그대로 재현한다 — 첫 문장에서 이벤트를 예약하고,
// 그 뒤 FinishItem 이 선점 표류로 거절해 tx 전체가 롤백된다.
func TestCloseDeclarationsByItemSeesRolledBackFinish(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	owner := mustSession(t, s, "p", "cc-owner")
	drifted := mustSession(t, s, "p", "cc-drifted")
	mustItem(t, s, "p", "it-1")

	if _, err := s.ClaimItem(ctx, "p", "it-1", owner.ID, time.Time{}); err != nil {
		t.Fatalf("선점 실패: %v", err)
	}

	err := s.Tx(ctx, func(tx *Tx) error {
		tx.LogEvent("item.finish", "p", drifted.ID, map[string]any{
			"item": "it-1", "mode": string(model.ItemDone),
			"count": 0, "linked": 0, "bytes": 10300,
		})
		return tx.FinishItem("p", "it-1", drifted.ID, model.ItemDone, "")
	})
	var held *ClaimHeldError
	if !errors.As(err, &held) {
		t.Fatalf("선점 표류 거절이 *ClaimHeldError 가 아니다: %T %v", err, err)
	}

	// 전제 ① — 정말 롤백됐다. 항목은 남의 선점 그대로다.
	it, err := s.GetItem(ctx, "p", "it-1")
	if err != nil {
		t.Fatalf("항목 조회 실패: %v", err)
	}
	if it.State != model.ItemClaimed {
		t.Fatalf("롤백이 안 됐다 — 항목 상태가 %q 다(claimed 여야 한다)", it.State)
	}

	// 전제 ② — 그런데 선언은 원장에 남았다. 이 두 줄이 이 설계의 토대다.
	got, err := s.CloseDeclarationsByItem(ctx, "p")
	if err != nil {
		t.Fatalf("종료 선언 조회 실패: %v", err)
	}
	d, ok := got["it-1"]
	if !ok {
		t.Fatalf("롤백된 finish 가 원장에 안 남았거나 안 집혔다: %+v", got)
	}
	if d.Done != 1 || d.Dropped != 0 || d.Count() != 1 {
		t.Errorf("mode 별 수가 다르다: %+v (Done=1 Dropped=0 이어야 한다)", d)
	}
	if d.LastSession != drifted.ID {
		t.Errorf("마지막 선언 세션이 다르다: got %q, want %q — 사유 문구가 이 id 를 부른다",
			d.LastSession, drifted.ID)
	}
	if d.LastMode != string(model.ItemDone) {
		t.Errorf("마지막 선언 mode 가 다르다: got %q, want %q", d.LastMode, model.ItemDone)
	}
	if d.Last.IsZero() {
		t.Errorf("마지막 선언 시각이 안 찍혔다 — 사유 문구가 시각 없이 나간다")
	}
}

// 같은 항목에 대한 선언 여럿이 접히고, mode 는 갈려서 담긴다.
// 그리고 **성공한 마무리도 함께 센다** — 롤백 판정은 store 의 일이 아니다.
func TestCloseDeclarationsByItemFoldsRepeatsAndSeparatesModes(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	owner := mustSession(t, s, "p", "cc-owner")
	driftA := mustSession(t, s, "p", "cc-drift-A")
	driftB := mustSession(t, s, "p", "cc-drift-B")
	mustItem(t, s, "p", "it-1")
	mustItem(t, s, "p", "it-2")

	if _, err := s.ClaimItem(ctx, "p", "it-1", owner.ID, time.Time{}); err != nil {
		t.Fatalf("선점 실패: %v", err)
	}

	// 표류한 세션이 남의 항목을 세 번 닫으려 한다. 셋 다 롤백된다.
	attempts := []struct {
		session string
		mode    model.ItemState
		reason  string
	}{
		{session: driftA.ID, mode: model.ItemDone},
		{session: driftA.ID, mode: model.ItemDropped, reason: "중복이라 버린다"},
		{session: driftB.ID, mode: model.ItemDropped, reason: "다시 봐도 중복이다"},
	}
	for i, a := range attempts {
		err := s.Tx(ctx, func(tx *Tx) error {
			tx.LogEvent("item.finish", "p", a.session, map[string]any{
				"item": "it-1", "mode": string(a.mode), "count": 0, "bytes": 3000,
			})
			return tx.FinishItem("p", "it-1", a.session, a.mode, a.reason)
		})
		var held *ClaimHeldError
		if !errors.As(err, &held) {
			t.Fatalf("%d번째 시도가 선점 표류로 안 죽었다: %T %v", i+1, err, err)
		}
	}

	// it-2 는 제 세션이 제대로 닫는다 — 성공한 마무리다.
	if _, err := s.ClaimItem(ctx, "p", "it-2", owner.ID, time.Time{}); err != nil {
		t.Fatalf("it-2 선점 실패: %v", err)
	}
	if err := s.Tx(ctx, func(tx *Tx) error {
		tx.LogEvent("item.finish", "p", owner.ID, map[string]any{
			"item": "it-2", "mode": string(model.ItemDone), "count": 1, "bytes": 500,
		})
		return tx.FinishItem("p", "it-2", owner.ID, model.ItemDone, "")
	}); err != nil {
		t.Fatalf("정상 마무리가 실패했다: %v", err)
	}

	got, err := s.CloseDeclarationsByItem(ctx, "p")
	if err != nil {
		t.Fatalf("종료 선언 조회 실패: %v", err)
	}

	d := got["it-1"]
	if d.Done != 1 || d.Dropped != 2 || d.Count() != 3 {
		t.Errorf("it-1 의 mode 별 수가 다르다: %+v (Done=1 Dropped=2 Count=3 이어야 한다)", d)
	}
	if d.LastMode != string(model.ItemDropped) || d.LastSession != driftB.ID {
		t.Errorf("마지막 선언이 최신 것이 아니다: mode=%q session=%q, want mode=%q session=%q\n"+
			"ORDER BY id DESC 의 첫 행이 최신인데 나중 행이 덮어썼을 수 있다",
			d.LastMode, d.LastSession, model.ItemDropped, driftB.ID)
	}

	// ★ 성공한 마무리도 여기 있다. store 는 롤백을 판정하지 않는다 —
	// 그 판정에 필요한 항목 상태를 쥔 것은 service 다.
	if s2 := got["it-2"]; s2.Done != 1 {
		t.Errorf("성공한 마무리가 빠졌다: %+v — store 가 롤백 판정을 하고 있다", s2)
	}
}

// 프로젝트를 안 넘는다.
//
// ★ 이 축이 특히 중요하다. 실측 3건이 정확히 그 모양이다 — context-platform 에서 친 finish 인데
// 항목은 kweiza-cc-plugins 에 있다. 그것은 좌표 오류지 표류가 아니고, 프로젝트 스코프를 안 걸면
// 남의 프로젝트 선언이 이 프로젝트 항목의 강등 근거로 둔갑한다.
func TestCloseDeclarationsByItemIsPerProject(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	seed(t, s, "q")
	a := mustSession(t, s, "p", "cc-A")
	b := mustSession(t, s, "q", "cc-B")

	s.LogEvent(ctx, "item.finish", "p", a.ID, map[string]any{"item": "it-1", "mode": "done"})
	s.LogEvent(ctx, "item.finish", "q", b.ID, map[string]any{"item": "it-1", "mode": "dropped"})
	s.LogEvent(ctx, "item.finish", "q", b.ID, map[string]any{"item": "it-9", "mode": "done"})

	got, err := s.CloseDeclarationsByItem(ctx, "p")
	if err != nil {
		t.Fatalf("종료 선언 조회 실패: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("남의 프로젝트가 섞였다: %+v", got)
	}
	if d := got["it-1"]; d.Done != 1 || d.Dropped != 0 {
		t.Errorf("같은 id 의 남의 프로젝트 선언이 접혔다: %+v", d)
	}
}

// 못 읽는 행은 안 센다 — 그리고 **다른 종류의 이벤트도 안 센다**.
//
// ★ 여기서는 이벤트를 손으로 심는다. 앞의 시험이 "롤백돼도 흘러간다"는 전제를 이미 밟았고,
// 이쪽이 재는 것은 파서의 그물이라 실제 롤백으로는 이 입력들을 만들 수 없다(payload 를
// 쓰는 것은 service.Finish 하나뿐이고 그것은 언제나 온전한 JSON 을 쓴다).
func TestCloseDeclarationsByItemSkipsUnreadableRows(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	a := mustSession(t, s, "p", "cc-A")

	// 셀 것 하나. 이것이 없으면 "전부 0"이 그물 덕인지 조회가 죽은 건지 못 가른다.
	s.LogEvent(ctx, "item.finish", "p", a.ID, map[string]any{"item": "it-ok", "mode": "done"})

	rows := []struct {
		name    string
		kind    string
		payload any
		why     string
	}{
		{
			name: "payload 가 JSON 이 아니다", kind: "item.finish", payload: nil,
			why: "직렬화 자체가 안 되는 값은 애초에 안 써지므로 아래에서 raw 로 심는다",
		},
		{
			name: "item 이 없다", kind: "item.finish",
			payload: map[string]any{"mode": "done", "count": 3},
			why:     "어느 항목의 것인지 모르면 셀 자리가 없다 — 세면 수만 늘고 대상이 없다",
		},
		{
			name: "item 이 공백뿐이다", kind: "item.finish",
			payload: map[string]any{"item": "   ", "mode": "done"},
			why:     "eventItemID 와 같은 규율로 trim 한 뒤 빈 것은 버린다",
		},
		{
			name: "mode 를 모른다", kind: "item.finish",
			payload: map[string]any{"item": "it-unknown-mode", "mode": "abandoned"},
			why:     "처방이 mode 로 갈린다 — 모르는 값을 한쪽에 몰면 화면이 원인을 단정한다",
		},
		{
			name: "mode 가 아예 없다", kind: "item.finish",
			payload: map[string]any{"item": "it-no-mode", "count": 1},
			why:     "옛 판의 payload 가 이 모양일 수 있다. 조용히 done 으로 접지 않는다",
		},
		{
			name: "종류가 다르다", kind: "item.finish_followups_missing",
			payload: map[string]any{"item": "it-other-kind", "mode": "done"},
			why: "실측 24건 전수가 20~181초 안에 재호출돼 성공했고 24개 항목 전부 done 이다 — " +
				"관문이 제 일을 한 기록이지 사고가 아니다",
		},
	}
	for _, r := range rows {
		if r.payload == nil {
			// LogEvent 는 nil payload 를 "{}" 로 쓴다. 깨진 JSON 은 그 경로로 못 만들므로
			// 직접 넣는다 — 옛 판이 남긴 행이나 손으로 만진 원장이 이 모양이다.
			if _, err := s.db.ExecContext(ctx,
				`INSERT INTO event(at, project, session_id, kind, payload) VALUES (?, ?, ?, ?, ?)`,
				fmtTime(nowStamp()), "p", a.ID, r.kind, `{"item": `); err != nil {
				t.Fatalf("%s: 심기 실패: %v", r.name, err)
			}
			continue
		}
		s.LogEvent(ctx, r.kind, "p", a.ID, r.payload)
	}

	got, err := s.CloseDeclarationsByItem(ctx, "p")
	if err != nil {
		t.Fatalf("종료 선언 조회 실패: %v", err)
	}
	if len(got) != 1 || got["it-ok"].Done != 1 {
		t.Fatalf("못 읽는 행이 섞였다: %+v\n%s", got, "기대는 it-ok 하나(Done=1)뿐이다")
	}
	for _, r := range rows {
		if r.payload == nil {
			continue
		}
		id, _ := r.payload.(map[string]any)["item"].(string)
		if id == "" {
			continue
		}
		if _, ok := got[id]; ok {
			t.Errorf("%s: %q 가 집혔다\n%s", r.name, id, r.why)
		}
	}
}

// 상한이 실제로 문다 — 그리고 **오래된 쪽부터** 잘린다.
//
// ★ 상수 5000 을 시험이 못 밟으면 그 수는 근거가 아니라 장식이다. 5000행을 심는 시험은
// 너무 느리므로 속살(closeDeclarationsByItem)에 상한을 열어 두고 여기서 2로 민다.
func TestCloseDeclarationsByItemCutsOldestFirst(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	a := mustSession(t, s, "p", "cc-A")

	s.LogEvent(ctx, "item.finish", "p", a.ID, map[string]any{"item": "it-old", "mode": "done"})
	s.LogEvent(ctx, "item.finish", "p", a.ID, map[string]any{"item": "it-mid", "mode": "dropped"})
	s.LogEvent(ctx, "item.finish", "p", a.ID, map[string]any{"item": "it-new", "mode": "done"})

	got, err := s.closeDeclarationsByItem(ctx, "p", 2)
	if err != nil {
		t.Fatalf("종료 선언 조회 실패: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("상한이 안 물었다: %+v", got)
	}
	if _, ok := got["it-old"]; ok {
		t.Errorf("가장 오래된 선언이 남았다 — ORDER BY 방향이 뒤집혔다: %+v", got)
	}
	if got["it-new"].Done != 1 || got["it-mid"].Dropped != 1 {
		t.Errorf("최근 둘이 안 집혔다: %+v", got)
	}

	// 상한이 0 이하면 아무것도 안 낸다. QueueReproduction 이 같은 자리를 같은 모양으로 막는다.
	empty, err := s.closeDeclarationsByItem(ctx, "p", 0)
	if err != nil {
		t.Fatalf("상한 0 은 오류가 아니다: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("상한 0 인데 %d건이 나왔다", len(empty))
	}
}

// 선언이 하나도 없으면 **빈 맵**이다. nil 도 오류도 아니다.
//
// ★ nil 을 "안 읽었다"로 쓰지 않는다. Go 의 nil 맵 조회는 zero 를 내므로 nil 과 빈 맵이
// 소비자 쪽에서 바이트 단위로 같은 출력이 되고, 그러면 "선언 0건"과 "이 축을 못 읽었다"를
// 가를 관측점이 하나도 없다. 그 둘을 가르는 것은 호출부의 두 번째 반환값(bool)이다.
func TestCloseDeclarationsByItemEmptyIsEmptyMapNotError(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")

	got, err := s.CloseDeclarationsByItem(ctx, "p")
	if err != nil {
		t.Fatalf("선언 0건은 오류가 아니다: %v", err)
	}
	if got == nil {
		t.Fatalf("nil 맵이 나왔다 — 부재는 빈 맵으로 낸다")
	}
	if len(got) != 0 {
		t.Fatalf("빈 원장인데 %d건이 나왔다: %+v", len(got), got)
	}
}
