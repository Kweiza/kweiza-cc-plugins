package store

import (
	"context"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 형제 카드 선점 조회의 **조인 축 셋**을 각각 잠근다.
//
// ★ 왜 축마다 시험이 필요한가. 대화는 (project, machine_id, cc_session_id) 로 정의했는데,
// 축 하나를 빼도 시험이 안 죽으면 그 축은 근거 없이 넣은 것이다. 이 파일이 그 되돌림을
// 미리 잠근다 — WHERE 절에서 축 하나를 지우면 대응하는 서브테스트 **하나만** 빨개져야 한다.
//
// 이 조회는 카드를 합치지 않는다. 카드 3중키도 claim 의 소유 축(session_id)도 그대로이고,
// 넓히는 것은 **읽기 하나뿐**이다.
func TestSiblingClaimedItemsSeparatesEachJoinAxis(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")
	seed(t, s, "q")
	if err := s.UpsertMachine(ctx, model.Machine{ID: "m2", Hostname: "laptop"}); err != nil {
		t.Fatal(err)
	}

	// 기준 카드 — 이 카드가 "나"이고, 형제를 찾는 주체다.
	me, _, err := s.OpenSession(ctx, "p", "m1", "/w/main", "cc-A", "")
	if err != nil {
		t.Fatal(err)
	}

	// 축마다 카드 하나씩 세우고 각자 항목을 쥐게 한다.
	cards := []struct {
		name              string
		project, machine  string
		worktree, cc      string
		item              string
		wantSeenAsSibling bool
		why               string
	}{
		{
			name: "워크트리만 다르면 형제다", project: "p", machine: "m1",
			worktree: "/w/tree2", cc: "cc-A", item: "it-sibling", wantSeenAsSibling: true,
			why: "이것이 `git worktree add` 로 갈린 카드다 — 이 축이 안 보이면 결함이 그대로다",
		},
		{
			name: "프로젝트가 다르면 남이다", project: "q", machine: "m1",
			worktree: "/w/tree3", cc: "cc-A", item: "it-otherproject", wantSeenAsSibling: false,
			why: "WHERE 에서 c.project 가 빠지면 남의 프로젝트 선점이 이 대화 것으로 보인다",
		},
		{
			name: "머신이 다르면 남이다", project: "p", machine: "m2",
			worktree: "/w/tree4", cc: "cc-A", item: "it-othermachine", wantSeenAsSibling: false,
			why: "WHERE 에서 s.machine_id 가 빠지면 다른 머신의 같은 cc 가 형제로 접힌다",
		},
		{
			name: "대화가 다르면 남이다", project: "p", machine: "m1",
			worktree: "/w/tree5", cc: "cc-B", item: "it-otherconversation", wantSeenAsSibling: false,
			why: "WHERE 에서 s.cc_session_id 가 빠지면 배타 방벽이 통째로 사라진다 — 남의 작업이 내 선점처럼 보인다",
		},
	}

	for _, c := range cards {
		sess, _, err := s.OpenSession(ctx, c.project, c.machine, c.worktree, c.cc, "")
		if err != nil {
			t.Fatalf("%s: 카드 등록 실패: %v", c.name, err)
		}
		if sess.ID == me.ID {
			t.Fatalf("%s: 기준 카드와 같은 카드가 나왔다 — 3중키가 안 갈렸다", c.name)
		}
		if err := s.AddItem(ctx, model.Item{
			Project: c.project, ID: c.item, Title: "t", Body: "b", CreatedAt: time.Now(),
		}); err != nil {
			t.Fatalf("%s: 항목 등록 실패: %v", c.name, err)
		}
		if _, err := s.ClaimItem(ctx, c.project, c.item, sess.ID); err != nil {
			t.Fatalf("%s: 선점 실패: %v", c.name, err)
		}
	}

	got, err := s.SiblingClaimedItems(ctx, "p", "m1", "cc-A", me.ID)
	if err != nil {
		t.Fatalf("형제 선점 조회 실패: %v", err)
	}
	seen := map[string]bool{}
	for _, id := range got {
		seen[id] = true
	}
	for _, c := range cards {
		t.Run(c.name, func(t *testing.T) {
			if seen[c.item] != c.wantSeenAsSibling {
				t.Fatalf("%q 가 형제로 %v 인데 %v 로 나왔다 — %s\n전체: %v",
					c.item, c.wantSeenAsSibling, seen[c.item], c.why, got)
			}
		})
	}
}

// TestSiblingClaimedItemsExcludesMyOwnCard 는 **자기 자신을 형제로 세지 않는다**를 잠근다.
//
// 이 축이 없으면 처방의 게이트가 자기 선점을 두 번 보게 되고, in.Claims 가 비었는데
// SiblingClaims 만 차는 상태(=이 항목이 겨냥한 바로 그 상태)와 구분이 안 된다.
func TestSiblingClaimedItemsExcludesMyOwnCard(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")

	me, _, err := s.OpenSession(ctx, "p", "m1", "/w/main", "cc-A", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddItem(ctx, model.Item{
		Project: "p", ID: "it-mine", Title: "t", Body: "b", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimItem(ctx, "p", "it-mine", me.ID); err != nil {
		t.Fatal(err)
	}
	// 대조 전제: 카드 스코프 조회로는 분명히 보인다.
	if own, _ := s.ClaimedItems(ctx, me.ID); len(own) != 1 {
		t.Fatalf("전제가 깨졌다 — 내 카드에 선점이 없다: %v", own)
	}

	got, err := s.SiblingClaimedItems(ctx, "p", "m1", "cc-A", me.ID)
	if err != nil {
		t.Fatalf("형제 선점 조회 실패: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("자기 자신의 선점이 형제로 잡혔다: %v", got)
	}
}

// TestSiblingClaimedItemsIgnoresAnEmptyConversation 은 **빈 cc 는 같은 대화가 아니다**를 잠근다.
//
// judge.sameConversation 이 같은 규칙을 쓴다. 저장층과 판정층이 여기서 갈리면 그 어긋남은
// 어느 화면에도 안 뜬다 — 빈 cc 카드 둘이 서로의 선점을 보게 되어 배타가 조용히 새어 나간다.
func TestSiblingClaimedItemsIgnoresAnEmptyConversation(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")

	me, _, err := s.OpenSession(ctx, "p", "m1", "/w/main", "cc-A", "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.SiblingClaimedItems(ctx, "p", "m1", "", me.ID)
	if err != nil {
		t.Fatalf("형제 선점 조회 실패: %v", err)
	}
	if got != nil {
		t.Fatalf("빈 cc 인데 형제를 찾았다: %v", got)
	}
}

// TestSiblingReleasedItemsSeesOnlyThisTurn 은 반납 축이 **구간**을 지키는지 본다.
//
// since 가 무시되면 옛날에 끝낸 항목이 영원히 "방금 끝냈다"로 보이고, 그러면 미선점 처방이
// 그 대화에서 영구히 꺼진다 — 이 수리가 만들 수 있는 가장 비싼 거짓 음성이다.
func TestSiblingReleasedItemsSeesOnlyThisTurn(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed(t, s, "p")

	me, _, err := s.OpenSession(ctx, "p", "m1", "/w/main", "cc-A", "")
	if err != nil {
		t.Fatal(err)
	}
	sib, _, err := s.OpenSession(ctx, "p", "m1", "/w/tree2", "cc-A", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddItem(ctx, model.Item{
		Project: "p", ID: "it-old", Title: "t", Body: "b", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimItem(ctx, "p", "it-old", sib.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetItemState(ctx, "p", "it-old", model.ItemDone, ""); err != nil {
		t.Fatal(err)
	}

	// 방금 반납됐으므로 넉넉히 과거를 기준으로 하면 보인다.
	recent, err := s.SiblingReleasedItems(ctx, "p", "m1", "cc-A", me.ID, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("형제 반납 조회 실패: %v", err)
	}
	if len(recent) != 1 || recent[0] != "it-old" {
		t.Fatalf("이 구간의 형제 반납이 안 보인다: %v", recent)
	}

	// 같은 반납을 미래 기준으로 물으면 안 보여야 한다 — 구간이 살아 있다는 증거다.
	future, err := s.SiblingReleasedItems(ctx, "p", "m1", "cc-A", me.ID, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("형제 반납 조회 실패: %v", err)
	}
	if len(future) != 0 {
		t.Fatalf("구간 밖 반납이 잡혔다: %v — since 가 무시되면 미선점 처방이 영구히 꺼진다", future)
	}
}
