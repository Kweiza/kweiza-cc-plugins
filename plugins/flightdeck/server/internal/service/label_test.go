package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// mustAddItem 은 항목을 등록한다. 필요하면 프로젝트도 함께 만든다.
//
// 이 패키지에는 mustAddItem 이 없다 — 다른 시험들은 st.AddItem 을 직접 부른다
// (cut_after_test.go 의 seedCut 이 선례다). 여기서는 시험 넷이 항목 등록을 반복하므로
// 이 파일 안에서만 쓰는 작은 헬퍼로 묶는다. UpsertProject 는 ON CONFLICT DO UPDATE 라
// 여러 번 불러도 안전하다(store/project.go).
func mustAddItem(t *testing.T, st *store.Store, it model.Item) {
	t.Helper()
	ctx := context.Background()
	if err := st.UpsertProject(ctx, model.Project{ID: it.Project, Path: "/repo/" + it.Project}); err != nil {
		t.Fatalf("프로젝트 등록 실패: %v", err)
	}
	if err := st.AddItem(ctx, it); err != nil {
		t.Fatalf("항목 등록 실패(%s): %v", it.ID, err)
	}
}

// 실제로 무엇이 바뀌었는지를 낸다 — 요청한 것이 아니라.
//
// 이미 있는 것을 --add 하거나 없는 것을 --rm 해도 거절하지 않는다(집합 연산의
// 멱등). 대신 Added·Removed 가 비어서 화면이 "실제로 더한 것: 없음"을 말할 수
// 있게 한다. 조용한 무변화는 안 만든다.
func TestSetLabelsReportsWhatActuallyChanged(t *testing.T) {
	ctx := context.Background()
	s, st := newSvc(t)
	mustAddItem(t, st, model.Item{Project: "p", ID: "it", Title: "제목", Body: "본문", Labels: []string{"a"}})

	res, err := s.SetLabels(ctx, LabelInput{
		Project: "p", SessionID: "sess", ItemID: "it",
		Add: []string{"a", "tickler"}, // "a" 는 이미 있다
		Rm:  []string{"zzz"},          // "zzz" 는 없다
	})
	if err != nil {
		t.Fatalf("SetLabels 실패: %v", err)
	}
	if got := strings.Join(res.Added, ","); got != "tickler" {
		t.Errorf("Added 가 %q 다 — tickler 만이어야 한다(a 는 이미 있었다)", got)
	}
	if len(res.Removed) != 0 {
		t.Errorf("Removed 가 %v 다 — 비어야 한다(zzz 는 없었다)", res.Removed)
	}
	if got := strings.Join(res.Before, ","); got != "a" {
		t.Errorf("Before 가 %q 다 — a 여야 한다", got)
	}
	if got := strings.Join(res.After, ","); got != "a,tickler" {
		t.Errorf("After 가 %q 다 — a,tickler 여야 한다", got)
	}
	if got := strings.Join(res.Item.Labels, ","); got != "a,tickler" {
		t.Errorf("되읽은 항목의 labels 가 %q 다 — 저장된 값이어야 한다", got)
	}
}

// 조용한 무작업을 안 만든다. 둘 다 비면 쓰기를 시작하기 전에 거절한다 —
// 오프라인이면 그 왕복이 아웃박스에 쌓이는 쓰기가 되기 때문이다(runAfterCut 과 같은 규율).
//
// ★ 오류가 **꼭 *RefusedError 여야 한다**(리뷰 Important) — errors.New 였을 때는
// api.ClassifyError 의 화이트리스트 어느 갈래에도 안 걸려 500 internal 로 나갔다.
// 이 갈래는 label 도구가 item_id 하나만 필수로 받으므로(tools.go) MCP 에서 정상
// 도달 가능하다. 타입까지 단정해야 그 회귀를 다시 못 들어오게 막는다.
func TestSetLabelsRefusesEmptyRequestBeforeTouchingTheStore(t *testing.T) {
	ctx := context.Background()
	s, st := newSvc(t)
	mustAddItem(t, st, model.Item{Project: "p", ID: "it", Title: "제목", Body: "본문"})

	_, err := s.SetLabels(ctx, LabelInput{Project: "p", SessionID: "sess", ItemID: "it"})
	if err == nil {
		t.Fatal("add·rm 이 둘 다 비었는데 통과했다 — 조용한 무작업이다")
	}
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("오류가 %T(%v) 다 — *RefusedError 여야 한다(500 으로 새지 않아야 한다)", err, err)
	}
	if refused.Guidance == "" {
		t.Error("거절에 Guidance 가 없다 — 이 저장소의 거절은 처방을 함께 낸다")
	}
}

func TestSetLabelsRefusesEmptyItemID(t *testing.T) {
	ctx := context.Background()
	s, _ := newSvc(t)

	if _, err := s.SetLabels(ctx, LabelInput{Project: "p", SessionID: "sess", Add: []string{"x"}}); err == nil {
		t.Fatal("항목 id 가 비었는데 통과했다")
	}
}

// 끝난 항목의 꼬리표는 안 고친다 — 꼬리표가 뜻을 갖는 곳은 굶김 축 하나이고
// 그 축은 열린 항목만 본다. 아무 데도 안 닿으면서 원장만 늘린다.
//
// ★ store 의 ItemClosedError 가 **아니어야 한다.** 그 타입은 상태 전이용이라
// Want(되돌리려던 상태)를 담고, api/errors.go 가 그것을 "이미 %s 다 — %s 로
// 되돌릴 수 없다"로 찍는다. 꼬리표 수정에는 Want 에 넣을 값이 없어서 현재 상태를
// 넣으면 "이미 done 다 — done 로 되돌릴 수 없다"가 사용자에게 나간다.
// RefusedError 여야 하고, 그래야 Guidance 를 실을 수 있다.
func TestSetLabelsRefusesClosedItemWithGuidance(t *testing.T) {
	ctx := context.Background()
	s, st := newSvc(t)
	mustAddItem(t, st, model.Item{
		Project: "p", ID: "closed", Title: "제목", Body: "본문",
		State: model.ItemDone,
	})

	_, err := s.SetLabels(ctx, LabelInput{
		Project: "p", SessionID: "sess", ItemID: "closed", Add: []string{"tickler"},
	})
	if err == nil {
		t.Fatal("끝난 항목에 꼬리표를 달았는데 통과했다")
	}
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("오류가 %T(%v) 다 — *RefusedError 여야 한다", err, err)
	}
	if refused.Guidance == "" {
		t.Error("거절에 Guidance 가 없다 — 이 저장소의 거절은 처방을 함께 낸다")
	}
}
