package store

import (
	"context"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 원장 읽기는 DB 전량이다 — 프로젝트로 안 거른다.
// project 가 NULL 인 판단이 스키마상 가능하고, WHERE project = ? 는 그런 행을 절대 못 잡는다.
func TestReadLedgerCoversAllProjectsAndNullProject(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seed(t, s, "p1")
	seed(t, s, "p2")

	linkJudgment(t, s, "p1", model.JudgmentDecision, "i1")
	linkJudgment(t, s, "p2", model.JudgmentAsk, "i2")
	// project 를 비우면 nullStr 이 NULL 로 넣는다. FK 를 아예 안 탄다.
	if _, err := s.AddJudgment(ctx, model.Judgment{Kind: model.JudgmentNow, Body: "프로젝트 없는 판단"}); err != nil {
		t.Fatalf("project 없는 판단 저장 실패: %v", err)
	}

	d, err := s.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("ReadLedger 실패: %v", err)
	}
	if len(d.Judgments) != 3 {
		t.Fatalf("판단이 %d건이다 — 3건을 기대한다", len(d.Judgments))
	}
	var nullProject int
	for _, j := range d.Judgments {
		if j.Project == nil {
			nullProject++
		}
	}
	if nullProject != 1 {
		t.Errorf("project=NULL 판단이 %d건 — 1건을 기대한다. 포인터가 아니면 NULL 과 \"\" 가 안 갈린다", nullProject)
	}
}

// 판단 정렬은 id 순이다. ULID 라 생성순이고, 같은 DB 면 같은 바이트가 나와야 한다.
func TestReadLedgerIsDeterministicallyOrdered(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seed(t, s, "p")
	for i := 0; i < 5; i++ {
		linkJudgment(t, s, "p", model.JudgmentDecision, "i1", "i2")
	}

	first, err := s.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("ReadLedger 실패: %v", err)
	}
	second, err := s.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("ReadLedger 재실행 실패: %v", err)
	}
	for i := range first.Judgments {
		if first.Judgments[i].ID != second.Judgments[i].ID {
			t.Fatalf("두 번 읽었더니 순서가 달라졌다: %d번째 %q vs %q",
				i, first.Judgments[i].ID, second.Judgments[i].ID)
		}
		if i > 0 && first.Judgments[i-1].ID >= first.Judgments[i].ID {
			t.Fatalf("id 오름차순이 아니다: %q >= %q", first.Judgments[i-1].ID, first.Judgments[i].ID)
		}
	}
	for i := 1; i < len(first.Links); i++ {
		p, c := first.Links[i-1], first.Links[i]
		if p.JudgmentID > c.JudgmentID {
			t.Fatalf("링크가 judgment_id 순이 아니다: %q > %q", p.JudgmentID, c.JudgmentID)
		}
	}
}

// 시각은 DB 원문 문자열 그대로다. time.Time 으로 접으면 마셜이 후행 0을 지워
// 폭이 흔들리고, 그러면 사전순 정렬이 시간순과 어긋난다(store.go 의 timeLayout 주석).
func TestReadLedgerKeepsRawTimestampString(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seed(t, s, "p")
	linkJudgment(t, s, "p", model.JudgmentDecision, "i1")

	d, err := s.ReadLedger(ctx)
	if err != nil {
		t.Fatalf("ReadLedger 실패: %v", err)
	}
	at := d.Judgments[0].At
	if len(at) != len("2006-01-02T15:04:05.000000Z") {
		t.Fatalf("at 이 폭 고정이 아니다(%q, %d자) — DB 원문을 그대로 실어야 한다", at, len(at))
	}
}
