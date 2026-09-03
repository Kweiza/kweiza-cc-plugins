package mcpsrv

import (
	"testing"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// TestLiveOfCarriesPathDeltaIntoLiveSession 는 liveOf 가 카드의 View.PathDelta 를
// judge.LiveSession.Delta 로 옮기는지 잰다.
//
// ★ 이 시험이 왜 있나. mcpsrv.go 의 liveOf 안 `Delta: c.View.PathDelta` 한 줄이
// **board 표면**의 꼬리 겹침이 상대 규모를 내는 유일한 배선이다. 최종 리뷰가 변이로
// 확인했다 — 이 줄을 지워도 `go test -count=1 ./...` 가 전 패키지 초록이었다. 배선이
// 통째로 죽어도 어떤 관문도 못 잡는다는 뜻이었다. 이 시험이 그 배선을 직접 잠근다:
// 이 시험을 지운 채로 그 줄을 지우면 이 시험이 먼저 빨개진다.
//
// ★ 쌍둥이 배선은 반대편 패키지에 있다 — internal/service/board.go 의 liveFor
// (**pick 표면**이 쓴다). 그쪽은 internal/service/live_delta_wiring_test.go 의
// TestLiveForCarriesPathDeltaIntoLiveSession 이 같은 방식으로 잠근다. 둘은 짝이고,
// 한쪽만 고치면 board 와 pick 의 꼬리 문구가 갈린다(이 파일의 liveOf 주석이 그
// 비대칭을 직접 경고한다).
//
// 실물 git 저장소는 안 태운다 — service.SessionCard 를 손으로 채우면 이 축만 골라
// 잠글 수 있다.
func TestLiveOfCarriesPathDeltaIntoLiveSession(t *testing.T) {
	c := service.SessionCard{
		View: model.SessionView{
			Session: model.Session{ID: "s1", Label: "세션1"},
			Paths:   []string{"DESIGN.md", "other.go"},
			PathDelta: map[string]model.LineDelta{
				"DESIGN.md": {Added: 47, Removed: 1},
			},
		},
	}

	got := liveOf(service.BoardView{Sessions: []service.SessionCard{c}})
	if len(got) != 1 {
		t.Fatalf("%d건, 원하는 것 1건", len(got))
	}
	d, ok := got[0].Delta["DESIGN.md"]
	if !ok {
		t.Fatalf("liveOf 결과에 DESIGN.md 규모 키가 없다 — Delta 배선이 안 됐다: %+v", got[0])
	}
	if d.Added != 47 || d.Removed != 1 {
		t.Fatalf("규모가 안 옮겨졌다: 받은 값 %+v, 기대 {Added:47 Removed:1}", d)
	}
	// other.go 는 규모를 못 잰 경로다 — 키가 아예 없어야 한다("못 읽었다"는 0 이 아니라
	// 키 부재다). 있으면 liveOf 가 규모를 조작해 낸 것이다.
	if _, ok := got[0].Delta["other.go"]; ok {
		t.Fatalf("liveOf 가 못 잰 경로에 규모 키를 만들어 냈다 — 0 과 '못 읽었다'가 섞였다")
	}
}

// 형제 프로젝트의 세션이 겹침 목록에 **들어간다**.
//
// ★ 이것이 없으면 워크스페이스에서 같은 파일을 두 레포 좌표에서 만지는 겹침이
// 원리적으로 안 보인다 — 좌표계가 달라서지 안 겹쳐서가 아니다. 서버가 이미 이
// 프로젝트의 좌표로 옮겨 실으므로, 이 계층이 하는 일은 **버리지 않는 것** 하나다.
func TestLiveOfIncludesSiblingSessions(t *testing.T) {
	view := service.BoardView{
		Sessions: []service.SessionCard{{View: model.SessionView{
			Session: model.Session{ID: "mine"}, Paths: []string{"a.go"},
		}}},
		SiblingLive: []judge.LiveSession{{ID: "sib", Label: "형제", Paths: []string{"b.go"}}},
	}
	got := liveOf(view)
	if len(got) != 2 {
		t.Fatalf("%d건 — 자기 1 + 형제 1 이어야 한다: %+v", len(got), got)
	}
	// 자기 레포가 먼저다 — 지금 손대는 파일이 먼저 보여야 한다(동점일 때만 사는 순서지만,
	// 그 의도가 코드에 있으면 시험도 그것을 지킨다).
	if got[0].ID != "mine" || got[1].ID != "sib" {
		t.Fatalf("순서=%s,%s — 자기 카드가 먼저여야 한다", got[0].ID, got[1].ID)
	}
}
