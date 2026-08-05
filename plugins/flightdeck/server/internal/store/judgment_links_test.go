package store

import (
	"context"
	"reflect"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// linkJudgment 는 판단 하나를 항목들에 매단다. finish 가 만드는 모양 그대로다
// (finish.go:148-152 가 끝낸 항목과 후속 전부를 한 handoff 판단에 매단다).
func linkJudgment(t *testing.T, s *Store, project string, kind model.JudgmentKind, items ...string) string {
	t.Helper()
	links := make([]model.JudgmentLink, 0, len(items))
	for _, it := range items {
		links = append(links, model.JudgmentLink{TargetKind: "item", TargetID: it})
	}
	j, err := s.AddJudgment(context.Background(), model.Judgment{
		Project: project, Kind: kind, Body: "본문", Links: links,
	})
	if err != nil {
		t.Fatalf("판단 저장 실패(%v): %v", items, err)
	}
	return j.ID
}

func TestJudgmentLinksForItemsGroupsByItem(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "P")

	mustItem(t, s, "P", "a")
	mustItem(t, s, "P", "b")
	mustItem(t, s, "P", "c")

	// J1 이 a·b 를 함께 가리킨다 = a 와 b 는 형제다.
	linkJudgment(t, s, "P", model.JudgmentHandoff, "a", "b")
	// J2 는 a 만.
	linkJudgment(t, s, "P", model.JudgmentAsk, "a")

	got, err := s.JudgmentLinksForItems(ctx, "P", []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("조회 실패: %v", err)
	}
	if len(got["a"]) != 2 {
		t.Fatalf("a 의 판단이 2건이어야 한다: %v", got["a"])
	}
	if len(got["b"]) != 1 {
		t.Fatalf("b 의 판단이 1건이어야 한다: %v", got["b"])
	}
	// 링크 없는 항목은 **키가 없다** — 빈 슬라이스를 넣으면 "없다"와 "안 봤다"가 접힌다.
	if _, ok := got["c"]; ok {
		t.Fatalf("링크 없는 항목에 키가 생겼다: %v", got["c"])
	}
	// a 와 b 가 같은 판단을 공유하는지가 형제 축의 전부다.
	shared := false
	for _, ja := range got["a"] {
		for _, jb := range got["b"] {
			if ja == jb {
				shared = true
			}
		}
	}
	if !shared {
		t.Fatalf("a·b 가 공유하는 판단이 없다: a=%v b=%v", got["a"], got["b"])
	}
}

// 사전순이어야 SiblingIndex 의 근거 문구가 흔들리지 않는다.
func TestJudgmentLinksForItemsSortsIDs(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "P")
	mustItem(t, s, "P", "x")
	for i := 0; i < 5; i++ {
		linkJudgment(t, s, "P", model.JudgmentAsk, "x")
	}
	got, err := s.JudgmentLinksForItems(ctx, "P", []string{"x"})
	if err != nil {
		t.Fatalf("조회 실패: %v", err)
	}
	sorted := append([]string(nil), got["x"]...)
	for i := 1; i < len(sorted); i++ {
		if sorted[i-1] > sorted[i] {
			t.Fatalf("사전순이 아니다: %v", got["x"])
		}
	}
	if !reflect.DeepEqual(got["x"], sorted) {
		t.Fatalf("정렬이 안 됐다: %v", got["x"])
	}
}

// 다른 프로젝트의 판단이 새면 안 된다.
func TestJudgmentLinksForItemsIsProjectScoped(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "P")
	seed(t, s, "Q")
	mustItem(t, s, "P", "same-id")
	mustItem(t, s, "Q", "same-id")
	linkJudgment(t, s, "Q", model.JudgmentAsk, "same-id")

	got, err := s.JudgmentLinksForItems(ctx, "P", []string{"same-id"})
	if err != nil {
		t.Fatalf("조회 실패: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("다른 프로젝트의 판단이 샜다: %v", got)
	}
}

// 빈 입력에 질의를 쏘지 않는다. IN () 는 SQLite 구문 오류다.
func TestJudgmentLinksForItemsEmptyInput(t *testing.T) {
	s := newStore(t)
	got, err := s.JudgmentLinksForItems(context.Background(), "P", nil)
	if err != nil {
		t.Fatalf("빈 입력에서 오류가 났다: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("빈 입력에 결과가 있다: %v", got)
	}
}
