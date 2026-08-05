package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// landing_queue 저장층. 각 시험이 무엇을 잠그는지는 그 시험의 docstring 에 있다.
//
// ★ 시험 **개수**를 여기 적지 않는다. 세어 두면 하나 붙일 때마다 이 줄이 조용히 거짓이
// 되는데, 산문은 빨개지지 않으므로 아무도 모른다 — 실제로 "여덟"이라고 적힌 채 열셋까지
// 자라 있었다. 수는 grep 이 세면 되고, 여기엔 세지 않아도 되는 문장만 둔다.

// mustEnqueue 는 EnqueueLanding 을 단발 트랜잭션으로 감싼 시험 전용 헬퍼다.
//
// Tx.EnqueueLanding 에는 Store 짝이 없다(서비스 계층이 다른 쓰기와 묶어 쓰는 것이
// 설계 의도라 Task 3 은 단발 래퍼를 만들지 않는다) — 그래서 시험이 매번 s.Tx 상용구를
// 쓰지 않도록 여기서만 감싼다.
func mustEnqueue(t *testing.T, s *Store, project, sessionID string) model.LandingRow {
	t.Helper()
	var row model.LandingRow
	if err := s.Tx(context.Background(), func(tx *Tx) error {
		var err error
		row, err = tx.EnqueueLanding(project, sessionID)
		return err
	}); err != nil {
		t.Fatalf("랜딩 줄 서기 실패(project=%s session=%s): %v", project, sessionID, err)
	}
	return row
}

// TestEnqueueLandingIsReentrantWithinTheSameLiveRow — 같은 세션이 두 번 서면 같은 행이다.
// 재진입이 새 행을 만들면 부를 때마다 맨 뒤로 밀린다.
func TestEnqueueLandingIsReentrantWithinTheSameLiveRow(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	a := mustSession(t, s, "p", "cc-A")

	first := mustEnqueue(t, s, "p", a.ID)
	second := mustEnqueue(t, s, "p", a.ID)

	if first.ID != second.ID {
		t.Errorf("재진입이 새 행을 만들었다: 처음 id=%d 재진입 id=%d — 부를 때마다 맨 뒤로 밀린다",
			first.ID, second.ID)
	}
	if !first.EnqueuedAt.Equal(second.EnqueuedAt) {
		t.Errorf("재진입이 대기 시작 시각을 바꿨다: %v → %v", first.EnqueuedAt, second.EnqueuedAt)
	}
}

// TestEnqueueLandingAfterLeavingGoesToTheBack — 닫힌 뒤 다시 서면 새 행이고 id 가 더 크다.
// 굶주림 판정(검증 실패 = 맨 뒤)이 이 성질 위에 있다.
func TestEnqueueLandingAfterLeavingGoesToTheBack(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	a := mustSession(t, s, "p", "cc-A")

	first := mustEnqueue(t, s, "p", a.ID)
	if err := s.CloseLandingRow(ctx, "p", first.ID, model.LandingLeftLeave, "잠깐 자리 비움"); err != nil {
		t.Fatal(err)
	}

	second := mustEnqueue(t, s, "p", a.ID)
	if second.ID <= first.ID {
		t.Errorf("닫힌 뒤 다시 선 행이 맨 뒤가 아니다: 처음 id=%d 다시 선 id=%d", first.ID, second.ID)
	}
}

// TestEnqueueLandingReentryDoesNotPoisonTheTransaction — 재진입이 SQLite 제약 위반을 거치는데
// 그 뒤 같은 트랜잭션에서 다른 쓰기가 계속 성립한다(문장 단위 롤백 확인).
func TestEnqueueLandingReentryDoesNotPoisonTheTransaction(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	a := mustSession(t, s, "p", "cc-A")
	b := mustSession(t, s, "p", "cc-B")

	err := s.Tx(ctx, func(tx *Tx) error {
		if _, err := tx.EnqueueLanding("p", a.ID); err != nil {
			return fmt.Errorf("첫 서기: %w", err)
		}
		// 재진입 — 부분 유니크 인덱스 위반을 내부에서 직접 거친다. SQLite 의 기본
		// 충돌 해법(ABORT)은 이 INSERT 문 하나만 되돌리고 트랜잭션 자체는 안 죽는다.
		// 그 성질을 다음 줄의 성공으로 직접 확인한다.
		if _, err := tx.EnqueueLanding("p", a.ID); err != nil {
			return fmt.Errorf("재진입: %w", err)
		}
		// 같은 트랜잭션에서 다른 세션의 쓰기가 계속 성립해야 한다 — 문장 단위 롤백이
		// 아니라 트랜잭션 단위 롤백이었다면 이 호출도 이미 죽은 트랜잭션 위에서 실패한다.
		if _, err := tx.EnqueueLanding("p", b.ID); err != nil {
			return fmt.Errorf("재진입 뒤 다른 세션의 쓰기: %w", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("트랜잭션이 실패했다 — 재진입이 트랜잭션을 오염시켰다: %v", err)
	}

	// 커밋 후에도 둘 다 살아 있어야 한다.
	if _, err := s.LiveLandingRow(ctx, "p", a.ID); err != nil {
		t.Errorf("a 의 살아 있는 줄 행이 없다: %v", err)
	}
	if _, err := s.LiveLandingRow(ctx, "p", b.ID); err != nil {
		t.Errorf("b 의 살아 있는 줄 행이 없다: %v", err)
	}
}

// TestEnqueueLandingRefusesEmptyCoordsWithItsOwnWords — 빈 좌표를 **FK 보다 먼저** 거절한다.
//
// ★ "오류가 났다"만 보면 이 시험은 가드를 하나도 안 잠근다. project·session 이 둘 다
// REFERENCES 라, EnqueueLanding 머리의 네 줄을 통째로 지워도 삽입은 어차피 FK(787)로
// 막히기 때문이다 — 실제로 지우고 돌리면 err != nil 은 그대로이고 문구만
// "landing_queue 제약 위반(missing_ref … 787)" 로 바뀐다. 그 번호를 받은 사람은
// **무엇이 비었는지** 모른다. 그래서 여기서는 문구를 단정한다: DB 제약은 최종 방어이지
// 1차 방어가 아니라는 이 레포의 규율(ValidateLandingLeave·left_kind CHECK 와 같은 축)을
// 지키는 유일한 관문이 문구다.
//
// 오류 문구를 바꾸면 이 시험이 빨개진다. 그것이 의도다 — 가드가 살아 있다는 것을
// 가드 **자신의 말**로만 구분할 수 있다.
func TestEnqueueLandingRefusesEmptyCoordsWithItsOwnWords(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	a := mustSession(t, s, "p", "cc-A")

	// mustEnqueue 는 실패를 Fatal 로 삼키므로 거절 갈래엔 못 쓴다 —
	// TestEnqueueLandingReentryDoesNotPoisonTheTransaction 처럼 s.Tx 를 직접 연다.
	for _, c := range []struct{ name, project, sessionID string }{
		{"project 가 비었다", "", a.ID},
		{"session 이 비었다", "p", ""},
		{"둘 다 비었다", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := s.Tx(ctx, func(tx *Tx) error {
				_, e := tx.EnqueueLanding(c.project, c.sessionID)
				return e
			})
			if err == nil {
				t.Fatal("빈 좌표가 통과했다")
			}
			if !strings.Contains(err.Error(), "랜딩 줄 좌표가 비었다") {
				t.Errorf("가드가 아니라 다른 것이 막았다(FK 였을 것이다): %v", err)
			}
		})
	}

	// 거절이 절반만 적용되지 않았는지 — 행이 남았다면 가드가 삽입 **뒤에** 섰다는 뜻이다.
	if _, err := s.LiveLandingRow(ctx, "p", a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("거절 뒤에 줄 행이 남았다: %v", err)
	}
	// ── 대조 전제: 채운 좌표는 **같은 경로로** 통과한다 ──
	// 없으면 "가드가 막았다"와 "이 판에서는 EnqueueLanding 이 늘 실패한다"가 안 갈린다.
	mustEnqueue(t, s, "p", a.ID)
}

// TestValidateLandingLeave — ok·finish 는 사유 면제, fail·leave·force 는 사유 필수, 모르는 종류는 거절.
func TestValidateLandingLeave(t *testing.T) {
	cases := []struct {
		name    string
		kind    model.LandingLeftKind
		detail  string
		wantErr bool
	}{
		{"ok 는 사유 불요", model.LandingLeftOK, "", false},
		{"finish 는 사유 불요", model.LandingLeftFinish, "", false},
		{"fail 은 사유 필수(없으면 거절)", model.LandingLeftFail, "", true},
		{"fail + 사유", model.LandingLeftFail, "검증 실패: lint", false},
		{"leave 는 사유 필수(없으면 거절)", model.LandingLeftLeave, "", true},
		{"leave + 사유", model.LandingLeftLeave, "잠깐 자리 비움", false},
		{"force 는 사유 필수(없으면 거절)", model.LandingLeftForce, "", true},
		{"force + 사유", model.LandingLeftForce, "세션이 죽은 것을 확인함", false},
		{"모르는 종류는 사유가 있어도 거절", model.LandingLeftKind("bogus"), "아무 사유", true},
		// 공백만 있는 사유는 사유가 아니다. `detail == ""` 만 보면 이 값이 Go 가드와
		// DB CHECK(left_detail <> '')를 **둘 다** 통과해 공백이 사유로 원장에 박힌다.
		{"fail + 공백만 있는 사유는 거절", model.LandingLeftFail, "   ", true},
		{"leave + 탭·줄바꿈만 있는 사유는 거절", model.LandingLeftLeave, "\t\n", true},
		{"force + 공백만 있는 사유는 거절", model.LandingLeftForce, " ", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := ValidateLandingLeave(c.kind, c.detail); (err != nil) != c.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, c.wantErr)
			}
		})
	}
}

// TestCloseLandingRowRefusesForceWithoutReason — 사유 없는 회수는 되짚을 수 없다.
func TestCloseLandingRowRefusesForceWithoutReason(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	a := mustSession(t, s, "p", "cc-A")
	row := mustEnqueue(t, s, "p", a.ID)

	if err := s.CloseLandingRow(ctx, "p", row.ID, model.LandingLeftForce, ""); err == nil {
		t.Fatal("사유 없는 강제 회수가 통과했다 — 되짚을 수 없는 회수가 된다")
	}
	// 거절 뒤에도 행은 여전히 살아 있어야 한다 — 절반만 적용되는 결과는 없다.
	if _, err := s.LiveLandingRow(ctx, "p", a.ID); err != nil {
		t.Errorf("거절됐는데 줄 행이 사라졌다: %v", err)
	}
	if err := s.CloseLandingRow(ctx, "p", row.ID, model.LandingLeftForce, "세션 머신이 재부팅됨"); err != nil {
		t.Fatalf("사유를 채운 강제 회수가 실패했다: %v", err)
	}
}

// TestLandingQueueOneLivePerSessionIsEnforcedByTheIndex — 앱 판정이 아니라 인덱스가 막는다.
// (인덱스를 DROP 하고 같은 삽입이 통과하는지로 변이 검증)
func TestLandingQueueOneLivePerSessionIsEnforcedByTheIndex(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	a := mustSession(t, s, "p", "cc-A")

	insertLive := func() error {
		_, err := s.db.ExecContext(ctx, `
			INSERT INTO landing_queue(project, session_id, enqueued_at, left_at, left_kind, left_detail)
			VALUES (?, ?, ?, NULL, NULL, NULL)`, "p", a.ID, fmtTime(nowStamp()))
		return err
	}

	// ── 대조 전제: 인덱스가 있는 채로는 같은 세션의 두 번째 살아 있는 행이 거절된다 ──
	if err := insertLive(); err != nil {
		t.Fatalf("전제가 깨졌다 — 첫 삽입이 실패했다: %v", err)
	}
	if err := insertLive(); err == nil {
		t.Fatal("전제가 깨졌다 — 인덱스가 있는데 같은 세션의 중복 삽입이 통과했다")
	}

	// ── 변이: 인덱스를 지우면 같은 삽입이 통과해야 한다 ──
	// 통과하면 "한 세션 = 한 줄"을 지키는 것이 앱 판정이 아니라 이 인덱스였다는 뜻이다.
	if _, err := s.db.ExecContext(ctx, `DROP INDEX landing_queue_one_live_per_session`); err != nil {
		t.Fatalf("인덱스 삭제 실패: %v", err)
	}
	if err := insertLive(); err != nil {
		t.Fatalf("인덱스를 지웠는데도 중복 삽입이 막혔다 — 앱 어딘가에 숨은 판정이 있다는 뜻이라 "+
			"이 시험의 전제 자체가 틀렸다: %v", err)
	}
}

// TestLandingLeftKindEnumIsEnforcedBySchema — left_kind 열거를 **스키마가** 막는다.
//
// ★ 이 CHECK 는 Go 경로로는 원리적으로 못 밟는다: left_kind 를 쓰는 모든 경로가 먼저
// ValidateLandingLeave 를 지나 모르는 종류를 거절하기 때문이다. 그래서 003 의 열거에
// 오타가 있어도(예: 'ok' 가 빠져도) 전 시험이 초록이다 —
// TestFreshInstallAndUpgradeProduceTheSameSchema 도 못 잡는다(신규·업그레이드가 같은
// 파일에서 오므로 오타가 대칭으로 들어간다). 003 주석이 스스로 "Go 가 1차 방어이고
// 이것이 최종 방어다"라고 적었는데, 그 방어가 존재한다는 증거가 이 시험뿐이다.
//
// 형제(TestLandingQueueOneLivePerSessionIsEnforcedByTheIndex)와 같은 모양으로 **대조 전제**를
// 함께 단정한다: 같은 문장이 'force' 로는 통과해야 한다. 안 그러면 "CHECK 가 막았다"와
// "다른 제약(left_at/left_kind 짝 · left_detail 비었음 · FK)이 막았다"가 안 갈린다.
func TestLandingLeftKindEnumIsEnforcedBySchema(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	a := mustSession(t, s, "p", "cc-A")

	// 저장층을 통하지 않고 **raw INSERT** 로 넣는다 — Go 가드를 지나면 이 CHECK 에 닿지 않는다.
	// 나머지 두 CHECK 를 만족시키려고 left_at·left_detail 을 함께 채운다.
	insertClosed := func(kind string) error {
		_, err := s.db.ExecContext(ctx, `
			INSERT INTO landing_queue(project, session_id, enqueued_at, left_at, left_kind, left_detail)
			VALUES (?, ?, ?, ?, ?, ?)`,
			"p", a.ID, fmtTime(nowStamp()), fmtTime(nowStamp()), kind, "사람이 회수함")
		return err
	}

	// ── 대조 전제: 열거에 있는 종류는 같은 문장이 통과한다 ──
	if err := insertClosed("force"); err != nil {
		t.Fatalf("대조가 깨졌다 — 열거 안의 종류 'force' 도 거절됐다. 그러면 아래 거절이 "+
			"열거 CHECK 때문인지 다른 제약 때문인지 갈리지 않는다: %v", err)
	}

	// ── 본 단정: 열거 밖의 종류는 거절된다 ──
	if err := insertClosed("bogus"); err == nil {
		t.Fatal("left_kind='bogus' 가 통과했다 — 003 의 열거 CHECK 가 없거나 낱말이 빠졌다. " +
			"Go 경로가 전부 ValidateLandingLeave 를 지나므로 이 시험이 그 방어의 유일한 증거다")
	}

	// 열거의 다섯 낱말이 **전부** 살아 있는지 본다. 하나가 빠지면 그 종류로 닫는 경로가
	// 런타임에 죽는데, Go 가드는 통과시키므로 그 사실이 배포될 때까지 안 보인다.
	for _, kind := range []string{"ok", "fail", "leave", "finish", "force"} {
		if err := insertClosed(kind); err != nil {
			t.Errorf("열거에 있어야 할 left_kind=%q 가 스키마에서 거절됐다 — "+
				"그 종류로 줄 행을 닫는 경로가 런타임에 죽는다: %v", kind, err)
		}
	}
}

// TestFrontLandingRowIsTheSmallestLiveID — 순서 집행이 걸리는 유일한 자리.
func TestFrontLandingRowIsTheSmallestLiveID(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	a := mustSession(t, s, "p", "cc-A")
	b := mustSession(t, s, "p", "cc-B")
	c := mustSession(t, s, "p", "cc-C")

	ra := mustEnqueue(t, s, "p", a.ID)
	rb := mustEnqueue(t, s, "p", b.ID)
	rc := mustEnqueue(t, s, "p", c.ID)

	front, err := s.FrontLandingRow(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if front.ID != ra.ID {
		t.Fatalf("맨 앞이 가장 작은 id 가 아니다: front=%d 기대=%d", front.ID, ra.ID)
	}

	// a 가 빠지면 b 가 맨 앞이 된다 — id 순서만 본다.
	if err := s.CloseLandingRow(ctx, "p", ra.ID, model.LandingLeftOK, ""); err != nil {
		t.Fatal(err)
	}
	front, err = s.FrontLandingRow(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if front.ID != rb.ID {
		t.Fatalf("맨 앞이 b 가 아니다: %+v (기대 id=%d)", front, rb.ID)
	}

	// 줄이 완전히 비면 ErrNotFound.
	if err := s.CloseLandingRow(ctx, "p", rb.ID, model.LandingLeftOK, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseLandingRow(ctx, "p", rc.ID, model.LandingLeftOK, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FrontLandingRow(ctx, "p"); !errors.Is(err, ErrNotFound) {
		t.Errorf("빈 큐의 맨 앞 조회가 ErrNotFound 가 아니다: %v", err)
	}
}

// TestListLandingQueueKeepsOrderAndDoesNotFilterByWindow — 창 밖 세션이 맨 앞에서 막는 상황이야말로
// 사람이 봐야 하는 상황이다. 거르면 "줄이 비었는데 아무도 못 잡는다"가 된다.
func TestListLandingQueueKeepsOrderAndDoesNotFilterByWindow(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	a := mustSession(t, s, "p", "cc-A")
	b := mustSession(t, s, "p", "cc-B")

	ra := mustEnqueue(t, s, "p", a.ID)
	rb := mustEnqueue(t, s, "p", b.ID)

	// a 는 신호를 한 번도 안 보냈다 — 생존 창 밖이다(사실 아예 신호가 없다, 창 밖의 극단).
	// b 는 방금 신호를 보냈다. 그래도 두 행 다 목록에 남아야 한다.
	if err := s.Beat(ctx, b.ID, model.SignalTool, time.Now()); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListLandingQueue(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("큐 길이가 2 여야 하는데 %d — 창 밖(무신호) 세션이 걸러졌을 수 있다: %+v", len(got), got)
	}
	if got[0].ID != ra.ID || got[1].ID != rb.ID {
		t.Errorf("순서가 순번(id)과 다르다: got=[%d,%d] 기대=[%d,%d]",
			got[0].ID, got[1].ID, ra.ID, rb.ID)
	}
}

// TestCloseLandingRowBySessionClosesTheGhostAndIsIdempotent — 줄을 안 선 세션이 마무리하는 것은 정상이고
// 여기서 ErrNotFound 를 올리면 finish 트랜잭션이 롤백돼 핸드오프 판단이 사라진다.
func TestCloseLandingRowBySessionClosesTheGhostAndIsIdempotent(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()

	// 줄을 한 번도 안 선 세션. finish 는 landing 상태와 무관하게 항상 불릴 수 있어야 한다.
	ghost := mustSession(t, s, "p", "cc-ghost")
	if err := s.CloseLandingRowBySession(ctx, "p", ghost.ID, model.LandingLeftFinish, ""); err != nil {
		t.Fatalf("줄을 한 번도 안 선 세션의 마무리가 오류를 냈다 — finish 트랜잭션을 통째로 "+
			"롤백시키는 결함이다: %v", err)
	}

	// 실제로 줄을 선 세션은 정상적으로 닫힌다.
	b := mustSession(t, s, "p", "cc-B")
	mustEnqueue(t, s, "p", b.ID)
	if err := s.CloseLandingRowBySession(ctx, "p", b.ID, model.LandingLeftFinish, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LiveLandingRow(ctx, "p", b.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("닫혔어야 하는데 여전히 살아 있다고 나온다: %v", err)
	}

	// 멱등: 이미 닫힌 뒤 재호출해도 오류 없이 통과한다.
	if err := s.CloseLandingRowBySession(ctx, "p", b.ID, model.LandingLeftFinish, ""); err != nil {
		t.Fatalf("이미 닫힌 뒤 재호출이 실패했다(멱등해야 한다): %v", err)
	}
}

// TestLastSignalAnswersForSessionsOutsideTheWindow — 레인 점유자가 창 밖일 때가 정확히 알아야 할 때다.
func TestLastSignalAnswersForSessionsOutsideTheWindow(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	a := mustSession(t, s, "p", "cc-A")

	// 신호가 하나도 없으면 ok=false 다.
	if _, ok, err := s.LastSignal(ctx, a.ID); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Error("신호가 없는데 ok=true 다")
	}

	old := time.Now().Add(-6 * time.Hour) // 어떤 생존 창보다도 확실히 밖이다
	if err := s.Beat(ctx, a.ID, model.SignalTool, old); err != nil {
		t.Fatal(err)
	}

	// ★ 창으로 거르지 않는다 — 몇 시간 전 신호라도 그대로 낸다.
	got, ok, err := s.LastSignal(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("신호가 있는데 ok=false 다")
	}
	if !got.Equal(old.UTC().Truncate(time.Microsecond)) {
		t.Errorf("창 밖 신호가 그대로 안 나온다: %v (기대 %v)", got, old.UTC())
	}

	// 여러 종류 중 가장 최근 것을 낸다.
	newer := time.Now()
	if err := s.Beat(ctx, a.ID, model.SignalPush, newer); err != nil {
		t.Fatal(err)
	}
	got, _, err = s.LastSignal(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(newer.UTC().Truncate(time.Microsecond)) {
		t.Errorf("가장 최근 신호가 아니다: %v (기대 %v)", got, newer.UTC())
	}
}

// TestTxListLandingQueueSeesTheRowInsertedInTheSameTransaction — 순번을 트랜잭션 안에서
// 세려면 Tx 판이 있어야 한다. 밖에서 읽으면 방금 넣은 행이 커밋 전이라 안 보이고,
// 그러면 서비스가 **자기 자신이 빠진 줄**에서 자기 순번을 센다.
func TestTxListLandingQueueSeesTheRowInsertedInTheSameTransaction(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	a := mustSession(t, s, "p", "cc-A")

	if err := s.Tx(ctx, func(tx *Tx) error {
		row, err := tx.EnqueueLanding("p", a.ID)
		if err != nil {
			return err
		}
		inside, err := tx.ListLandingQueue("p")
		if err != nil {
			return err
		}
		if len(inside) != 1 || inside[0].ID != row.ID {
			t.Errorf("같은 트랜잭션에서 넣은 행이 목록에 없다: %+v", inside)
		}
		// 대조: 같은 순간 트랜잭션 **밖** 판은 아직 아무것도 못 본다.
		// 이 차이가 Tx 판을 더한 이유 전부다.
		outside, err := s.ListLandingQueue(ctx, "p")
		if err != nil {
			return err
		}
		if len(outside) != 0 {
			t.Errorf("커밋 전인데 트랜잭션 밖에서 %d행이 보인다 — 대조가 성립하지 않는다", len(outside))
		}
		return nil
	}); err != nil {
		t.Fatalf("트랜잭션이 실패했다: %v", err)
	}
}

// TestLastLandingRowReadsClosedRowsThatLiveLandingRowCannot — 회수된 세션에게 **왜**
// 레인을 잃었는지 답하려면 이미 닫힌 행의 left_detail 을 읽어야 한다.
// LiveLandingRow 는 그 자리에서 ErrNotFound 라 사유를 못 낸다.
func TestLastLandingRowReadsClosedRowsThatLiveLandingRowCannot(t *testing.T) {
	s := newStore(t)
	seed(t, s, "p")
	ctx := context.Background()
	a := mustSession(t, s, "p", "cc-A")

	closed := mustEnqueue(t, s, "p", a.ID)
	if err := s.CloseLandingRow(ctx, "p", closed.ID, model.LandingLeftForce, "사람이 회수했다"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LiveLandingRow(ctx, "p", a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("사전 조건이 깨졌다 — 닫은 행이 아직 살아 있다: %v", err)
	}

	last := mustLastLandingRow(t, s, "p", a.ID)
	if last.ID != closed.ID || last.LeftKind != model.LandingLeftForce || last.LeftDetail != "사람이 회수했다" {
		t.Fatalf("닫힌 행의 사유가 안 실렸다 — 회수된 세션에게 답할 것이 사라진다: %+v", last)
	}

	// 다시 서면 **살아 있는 행**이 최신이다(그 행에는 사유가 없다).
	again := mustEnqueue(t, s, "p", a.ID)
	last = mustLastLandingRow(t, s, "p", a.ID)
	if last.ID != again.ID || last.LeftAt != nil {
		t.Fatalf("다시 선 뒤에도 닫힌 행이 최신으로 나온다: %+v", last)
	}

	// 선 적이 없는 세션은 ErrNotFound 다 — 0값과 부재를 가른다.
	b := mustSession(t, s, "p", "cc-B")
	if err := s.Tx(ctx, func(tx *Tx) error {
		_, err := tx.LastLandingRow("p", b.ID)
		return err
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("선 적 없는 세션에 ErrNotFound 가 아니다: %v", err)
	}
}

// mustLastLandingRow 는 Tx.LastLandingRow 를 단발 트랜잭션으로 감싼 시험 전용 헬퍼다
// (Store 짝을 안 둔 이유는 mustEnqueue 와 같다 — 호출부가 서비스의 트랜잭션 안뿐이다).
func mustLastLandingRow(t *testing.T, s *Store, project, sessionID string) model.LandingRow {
	t.Helper()
	var row model.LandingRow
	if err := s.Tx(context.Background(), func(tx *Tx) error {
		var err error
		row, err = tx.LastLandingRow(project, sessionID)
		return err
	}); err != nil {
		t.Fatalf("마지막 줄 행 조회 실패(project=%s session=%s): %v", project, sessionID, err)
	}
	return row
}
