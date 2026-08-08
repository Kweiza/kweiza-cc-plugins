package service

import (
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 이 파일이 잠그는 것: DESIGN §5 「쓰기 뒤 조회가 실패하면」의 D 금지 —
// 되돌릴 수 없게 쓴 뒤에는 보조 조회가 실패해도 결과를 버리지 않는다.
//
// 전수(2026-08-09)에서 그 규칙을 어긴 자리가 다섯이었고 셋은 채널 근거가 주석에 없었다.
// 여기는 그중 서비스 계층 둘이다(셋째는 api 계층이라 handlers_session_partial_test.go).
//
// ★ 격리 수법은 item_after 표 숨기기다 — GetItem 만 afterOf 로 그 표를 지난다.
// 쓰기(AddItem·MoveItem)는 item 표를 쓰므로 그 표 자체는 못 숨긴다.

// AddItem 은 등록을 커밋한 뒤 되읽기가 실패해도 등록 확인을 낸다.
//
// ★ 이 자리가 제일 나빴다. 항목은 큐에 **들어가 있는데** 호출자는 오류만 받고,
// 재시도하면 store 가 중복으로 거절한다 — 세션은 "못 만들었다"고 믿는데 큐에는 있고
// 다시 만들 수도 없는 상태가 된다. 화면(RenderAdd)이 쓰는 값은 전부 입력으로 아는
// 것이라(id·프로젝트·상태·제목·경로·선행) 못 읽은 것은 서버가 채운 CreatedAt 뿐이다.
func TestAddItemSurvivesUnreadableReadBack(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "")

	if _, err := st.DB().ExecContext(ctx(),
		`ALTER TABLE item_after RENAME TO item_after_hidden`); err != nil {
		t.Fatalf("item_after 표 숨기기 실패(시험 전제 준비): %v", err)
	}

	got, err := s.AddItem(ctx(), AddItemInput{
		Project: "p", SessionID: me.Session.ID, ID: "aux-add",
		Title: "제목", Body: "본문", Paths: []string{"services/x"},
	})
	if err != nil {
		t.Fatalf("등록은 커밋됐는데 되읽기 실패로 오류를 올렸다 — 항목은 큐에 있고 세션은 "+
			"실패로 안다. 재시도하면 중복으로 거절된다:\n%v", err)
	}
	// 입력으로 아는 사실은 전부 있어야 한다 — RenderAdd 의 모든 줄이 이 값으로 선다.
	if got.ID != "aux-add" || got.Project != "p" || got.State != model.ItemOpen ||
		got.Title != "제목" || len(got.Paths) != 1 {
		t.Fatalf("등록으로 아는 사실이 응답에 없다: %+v", got)
	}
	// 원장 확인 — 실제로 들어가 있다.
	var n int
	if err := st.DB().QueryRowContext(ctx(),
		`SELECT count(*) FROM item WHERE project='p' AND id='aux-add'`).Scan(&n); err != nil {
		t.Fatalf("항목 확인 실패: %v", err)
	}
	if n != 1 {
		t.Fatalf("항목이 큐에 %d건 — 커밋됐다는 전제가 틀렸다", n)
	}
}

// 정상 경로 짝 — 되읽기가 되면 서버가 채운 값까지 온다.
func TestAddItemReturnsStoredItemOnHappyPath(t *testing.T) {
	s, _ := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "")

	got, err := s.AddItem(ctx(), AddItemInput{
		Project: "p", SessionID: me.Session.ID, ID: "aux-add2",
		Title: "제목", Body: "본문",
	})
	if err != nil {
		t.Fatalf("AddItem: %v", err)
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("정상 경로인데 서버가 채운 CreatedAt 이 비었다 — 되읽기 값이 아니라 " +
			"입력을 그대로 돌려준 것이다")
	}
}

// MoveItem 의 되읽기 실패 갈래는 **결정론적으로 못 밟는다** — 그 사실을 여기 적어 둔다.
//
// ★ 왜 못 밟나. AddItem 은 선행이 없으면 쓰기가 item_after 를 안 건드리므로 그 표를 숨기면
// 되읽기(GetItem→afterOf)만 깨지는 격리가 선다. MoveItem 은 다르다 — store.MoveItem 이
// 트랜잭션 **안에서** t.GetItem(원본 프로젝트) 를 부르고(move.go, 항목 존재·선점 판정),
// 그것도 같은 afterOf 를 지난다. 그래서 그 표를 숨기면 **쓰기 전에** 죽고, 그것은 이 부류가
// 아니라 정상적인 거절이다(아무것도 안 썼으므로 오류를 올리는 것이 옳다).
// 서비스가 store 를 인터페이스가 아니라 구조체로 들고 있어 조회 하나만 주입해 깨뜨릴 수도 없다.
//
// ★ 그래서 잠그는 것은 **계약**이다: MoveResult 가 Derived 를 싣고(못 읽음을 나를 자리가
// 있다), 정상 경로에서는 그 자리가 비어 있다. 실패 갈래의 동작은 코드 주석과 DESIGN §5 가
// 지고, 이 시험은 그 자리가 **존재한다**는 것까지만 지킨다. 같은 성격의 선례가 finish.go 의
// 중복 흡수 갈래다("이 갈래도 시험이 결정론적으로는 못 밟는다").
func TestMoveResultCarriesADerivedSeat(t *testing.T) {
	var res MoveResult
	res.Failures = append(res.Failures, DerivedFailure{Axis: "item", Detail: "못 읽었다"})
	if !hasAxis(res.Failures, "item") {
		t.Fatal("MoveResult 에 파생 고백을 실을 자리가 없다 — 되읽기가 실패하면 결과를 " +
			"통째로 버리는 길밖에 안 남는다(DESIGN §5 가 금지한 D)")
	}
}

// 정상 경로 짝 — 고백이 상시 점등이면 판별력이 0이다.
func TestMoveItemAxisSilentOnHappyPath(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	me := openSession(t, s, "p", repo, repo, "cc-1", "")
	addItem(t, s, "p", "aux-move2", nil, nil)
	if err := st.UpsertProject(ctx(), model.Project{ID: "q", Path: "/repo/q", DefaultBranch: "main"}); err != nil {
		t.Fatalf("대상 프로젝트 등록 실패: %v", err)
	}

	res, err := s.MoveItem(ctx(), MoveInput{
		Project: "p", ItemID: "aux-move2", To: "q", SessionID: me.Session.ID,
	})
	if err != nil {
		t.Fatalf("MoveItem: %v", err)
	}
	if len(res.Failures) != 0 {
		t.Fatalf("정상 경로인데 고백이 켜져 있다: %+v", res.Failures)
	}
	if res.Item.Title == "" {
		t.Fatal("정상 경로인데 항목 전문이 비었다 — 되읽기 값이 아니다")
	}
}
