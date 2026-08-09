package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 예약 이벤트는 **자기 트랜잭션이 어떻게 끝났는지를 스스로 말한다.**
//
// ★ 왜 이 축이 있나. Tx.LogEvent 로 예약된 이벤트는 롤백 갈래에서도 흘러간다(store.go 의
// flushDeferred). 그 성질 덕에 "무엇을 시도했다 실패했나"가 남지만, 대가로 원장의
// item.finish 에는 성공한 마무리와 롤백된 마무리가 같이 들어 있다. 결말이 payload 에
// 없으면 소비자는 항목 상태로 되추론할 수밖에 없는데, 그 되추론은 실측으로 죽었다
// (QueueReproduction 의 ★ 와 DESIGN §10 의 표).
//
// ★ 상수가 아니라 **wire 문자열**을 문다. 이 값은 원장에 영구히 남아 나중에 사람이
// 질의로 직접 읽는 값이고(DESIGN §10 의 재측 절차가 그렇게 적혀 있다), 상수 이름만 물면
// 값이 바뀌어도 초록이 유지된다.
func TestDeferredEventsCarryTheirTransactionOutcome(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")

	if err := s.Tx(ctx, func(tx *Tx) error {
		tx.LogEvent("committed.kind", "p", "", map[string]any{"item": "i1"})
		return nil
	}); err != nil {
		t.Fatalf("커밋 갈래 실패: %v", err)
	}
	boom := errors.New("일부러 실패")
	if err := s.Tx(ctx, func(tx *Tx) error {
		tx.LogEvent("rolledback.kind", "p", "", map[string]any{"item": "i2"})
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("롤백 오류가 안 올라왔다: %v", err)
	}

	for _, c := range []struct{ kind, want string }{
		{"committed.kind", `"tx":"committed"`},
		{"rolledback.kind", `"tx":"rolled_back"`},
	} {
		evs, err := s.ListEvents(ctx, c.kind, time.Time{}, 10)
		if err != nil {
			t.Fatalf("%s 조회 실패: %v", c.kind, err)
		}
		if len(evs) != 1 {
			t.Fatalf("%s 이벤트가 %d건이다, want 1", c.kind, len(evs))
		}
		if !strings.Contains(evs[0].Payload, c.want) {
			t.Errorf("%s payload 에 결말이 없다(want %s): %s", c.kind, c.want, evs[0].Payload)
		}
		// 결말을 얹느라 호출자가 실은 칸을 잃으면 안 된다 — 원장의 나머지 축이 통째로 죽는다.
		if !strings.Contains(evs[0].Payload, `"item":"i`) {
			t.Errorf("%s payload 에서 호출자가 실은 칸이 사라졌다: %s", c.kind, evs[0].Payload)
		}
	}
}

// 호출자의 맵을 **안 고친다.**
//
// 지금 Tx.LogEvent 호출부가 전부 인라인 리터럴이라는 사실은 호출부의 성질이지 이 함수의
// 성질이 아니다. 같은 맵을 두 번 쓰는 호출자가 생기는 날 값이 조용히 달라진다.
func TestDeferredEventMarkingDoesNotMutateCallerPayload(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")

	payload := map[string]any{"item": "i1"}
	_ = s.Tx(ctx, func(tx *Tx) error {
		tx.LogEvent("caller.map", "p", "", payload)
		return errors.New("일부러 실패")
	})
	if _, ok := payload["tx"]; ok {
		t.Fatalf("호출자가 만든 맵에 결말이 찍혔다 — 사본을 안 떴다: %+v", payload)
	}
}

// 원장에서 파생하는 새 축은 DESIGN 에 이름이 있어야 한다.
//
// ★ 선례가 같은 패키지에 있다 — close_declaration_doc_test.go 가 같은 방향(코드 → 문서)의
// 같은 관문이다. 방향을 뒤집지 않는 이유도 그쪽 주석 그대로다: DESIGN 은 구현보다 앞설 수
// 있다(§0 머리말).
//
// 아래 두 문자열은 **앵커**다. 문서 표현을 바꾸려면 이 시험도 같이 고쳐라.
func TestTxOutcomeAxisIsNamedInDesign(t *testing.T) {
	src, err := os.ReadFile("event.go")
	if err != nil {
		t.Fatalf("store/event.go 를 못 읽었다 — 이 시험의 좌표가 틀렸다: %v", err)
	}
	if !strings.Contains(string(src), "func markTxOutcome") {
		t.Skip("markTxOutcome 이 아직 없다 — 이 관문의 전제가 안 섰다")
	}
	p := filepath.Join("..", "..", "..", "DESIGN.md")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("DESIGN.md 를 못 읽었다(%s) — 이 시험의 좌표가 틀렸다: %v", p, err)
	}
	for _, want := range []string{
		"TxOutcomeKey",               // §10 의 고침 문단
		"`payload.tx='rolled_back'`", // §10 의 재측 절차
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("원장에 결말을 찍는데 DESIGN 에 %q 가 없다 — "+
				"재측 절차(§10)가 그 표시를 모르면 기한 판정이 옛 규칙으로 난다", want)
		}
	}
}
