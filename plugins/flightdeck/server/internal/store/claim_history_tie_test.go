package store

import (
	"context"
	"testing"
	"time"
)

// TestClaimHolderAtBreaksTimestampTiesByInsertOrder 는 재생의 **동점 처리**를 잠근다.
//
// ★ 왜 서비스 경로로 못 재는가. 저장 표기가 마이크로초(timeLayout)라 같은 `at` 을 만들려면
// 두 LogEvent 가 같은 마이크로초에 떨어져야 하는데, 그것은 기계 속도에 달린 일이라
// 시험으로 만들면 **깜빡이는 시험**이 된다. 그래서 여기서는 원장 행을 직접 세운다 —
// 이 함수가 읽는 것이 결국 그 행들이고, event 표는 INSERT 만 열려 있다
// (event_no_update·event_no_delete 가 나머지를 잠근다).
//
// 동점이 실물에서 드문 것은 맞다. 그러나 드문 것과 답이 뒤집히는 것은 다른 문제이고,
// `ORDER BY at` 만 쓰면 SQLite 가 같은 `at` 안의 순서를 보장하지 않아 **같은 질의가
// 선점과 반납 사이를 오간다.** 그 흔들림은 화면에 안 뜬다.
func TestClaimHolderAtBreaksTimestampTiesByInsertOrder(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	holder := mustSession(t, s, "p", "cc-주인")

	// 선점과 반납을 **같은 시각**으로 박는다. 삽입 순서만이 둘을 가른다.
	at := fmtTime(time.Now().UTC())
	insertEventAt(t, s, at, "p", holder.ID, `{"item":"fd-x"}`, "item.claim")
	insertEventAt(t, s, at, "p", holder.ID, `{"item":"fd-x"}`, "item.finish")

	got, err := s.ClaimHolderAt(ctx, "p", "fd-x", time.Now().UTC())
	if err != nil {
		t.Fatalf("이력 조회 실패: %v", err)
	}
	if got != "" {
		t.Fatalf("같은 시각의 선점·반납에서 점유자를 %q 라 했다 — 기대 \"\"\n"+
			"반납이 나중에 삽입됐으므로 나중이다. `at` 만으로 정렬하면 이 답이 흔들린다", got)
	}

	// ── 대조: 순서를 뒤집으면 답도 뒤집혀야 한다. 이것이 없으면 위 단정은
	// "무조건 빈 값을 낸다"로도 초록불이 난다.
	at2 := fmtTime(time.Now().UTC().Add(time.Second))
	insertEventAt(t, s, at2, "p", holder.ID, `{"item":"fd-y"}`, "item.finish")
	insertEventAt(t, s, at2, "p", holder.ID, `{"item":"fd-y"}`, "item.claim")

	got, err = s.ClaimHolderAt(ctx, "p", "fd-y", time.Now().UTC().Add(2*time.Second))
	if err != nil {
		t.Fatalf("이력 조회 실패: %v", err)
	}
	if got != holder.ID {
		t.Fatalf("같은 시각에 반납→선점 순인데 점유자를 %q 라 했다 — 기대 %q", got, holder.ID)
	}
}

// insertEventAt 는 `at` 을 지정해 원장 행 하나를 세운다. **시험 전용**이다 —
// 생산 경로(LogEvent)는 시각을 스스로 찍으므로 동점을 만들 수단이 없다.
func insertEventAt(t *testing.T, s *Store, at, project, sessionID, payload, kind string) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO event (at, project, session_id, kind, payload) VALUES (?,?,?,?,?)`,
		at, project, sessionID, kind, payload); err != nil {
		t.Fatalf("원장 행 삽입 실패(%s): %v", kind, err)
	}
}
