package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// setLabelsFixture 는 항목 하나가 있는 저장소를 연다.
//
// 열기 헬퍼는 이 패키지의 기존 이름 `newStore` 를 쓴다(브리프가 가정한
// `openTestStore` 는 이 패키지에 없다). `newStore` 는 project·machine 을 안 넣으므로
// `seed` 로 project "p" 를 먼저 등록한다 — item 표가 project(id) 를 FK 로 물어서,
// 안 넣으면 AddItem 이 FK 위반으로 죽는다(item.go 의 스키마 주석 참고).
func setLabelsFixture(t *testing.T) (*Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	if err := s.AddItem(ctx, model.Item{
		Project: "p", ID: "it", Title: "제목", Body: "본문", Labels: []string{"a"},
	}); err != nil {
		t.Fatalf("항목 준비 실패: %v", err)
	}
	return s, ctx
}

func TestSetLabelsReplacesAndIsReadBack(t *testing.T) {
	s, ctx := setLabelsFixture(t)

	if err := s.SetLabels(ctx, "p", "it", []string{"a", "tickler"}, "sess"); err != nil {
		t.Fatalf("SetLabels 실패: %v", err)
	}
	it, err := s.GetItem(ctx, "p", "it")
	if err != nil {
		t.Fatalf("되읽기 실패: %v", err)
	}
	if len(it.Labels) != 2 || it.Labels[0] != "a" || it.Labels[1] != "tickler" {
		t.Errorf("labels 가 %v 다 — [a tickler] 여야 한다(순서 포함)", it.Labels)
	}
}

// 없는 항목에 조용히 성공하면 항목 id 오타 하나에 도구가 성공을 보고하고
// 원장에는 아무것도 안 남는다 — affectedOne 주석이 적은 그 결함이다.
func TestSetLabelsRefusesMissingItem(t *testing.T) {
	s, ctx := setLabelsFixture(t)

	err := s.SetLabels(ctx, "p", "없는-항목", []string{"x"}, "sess")
	if err == nil {
		t.Fatal("없는 항목에 SetLabels 가 성공했다 — 오타 하나에 성공이 보고된다")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("오류가 %v 다 — ErrNotFound 여야 한다", err)
	}
}

// 이 쓰기는 되돌리는 코드가 없다. 무엇이 붙어 있었는지가 바꾸는 순간 사라지므로
// before 를 원장에 남긴다 — RemoveAfter 가 item.after.cut 을 남기는 것과 같은 규율이다.
//
// 원장을 읽는 자리는 브리프가 가정한 `RecentEvents` 대신 이 패키지의 기존 메서드
// `ListEvents(ctx, kind, since, limit)` 를 쓴다(event_tx_outcome_test.go·store_test.go 의
// 기존 시험이 쓰는 것과 같은 자리) — project 축은 없지만 kind="item.label" 로 좁히면
// 이 시험의 목적(이벤트가 남았는지·세션이 실렸는지)에는 충분하다.
func TestSetLabelsWritesLedgerWithBeforeAndAfter(t *testing.T) {
	s, ctx := setLabelsFixture(t)

	if err := s.SetLabels(ctx, "p", "it", []string{"tickler"}, "sess"); err != nil {
		t.Fatalf("SetLabels 실패: %v", err)
	}

	evs, err := s.ListEvents(ctx, "item.label", time.Time{}, 20)
	if err != nil {
		t.Fatalf("원장 조회 실패: %v", err)
	}
	var found bool
	for _, e := range evs {
		if e.Kind != "item.label" {
			continue
		}
		found = true
		if e.SessionID != "sess" {
			t.Errorf("이벤트의 세션이 %q 다 — sess 여야 한다", e.SessionID)
		}

		// ★ **페이로드를 푼다.** Kind 와 SessionID 만 보면 LogEvent 에서 before 키가
		// 사라져도 이 시험이 조용히 통과한다 — 그런데 before 야말로 이 표면이 메우려던
		// 공백이다(사고 당시 원장의 흔적은 판단 하나뿐이었다). 이름이 …WithBeforeAndAfter
		// 인 시험이 before·after 를 안 재면 그 이름이 거짓이다.
		var payload struct {
			Item   string   `json:"item"`
			Before []string `json:"before"`
			After  []string `json:"after"`
		}
		if uerr := json.Unmarshal([]byte(e.Payload), &payload); uerr != nil {
			t.Fatalf("이벤트 페이로드를 못 읽었다: %v (원문 %q)", uerr, e.Payload)
		}
		if got := strings.Join(payload.Before, ","); got != "a" {
			t.Errorf("원장의 before 가 %q 다 — a 여야 한다", got)
		}
		if got := strings.Join(payload.After, ","); got != "tickler" {
			t.Errorf("원장의 after 가 %q 다 — tickler 여야 한다", got)
		}
		if payload.Item != "it" {
			t.Errorf("원장의 item 이 %q 다 — it 여야 한다", payload.Item)
		}
	}
	if !found {
		t.Errorf("원장에 item.label 이 없다 — 이 표면이 메우려던 공백이 그대로 남는다(사고 당시 흔적은 판단 하나뿐이었다)")
	}
}
