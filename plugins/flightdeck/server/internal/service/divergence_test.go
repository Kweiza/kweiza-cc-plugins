package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

func sess(id, project, machine, worktree, cc string) model.Session {
	return model.Session{ID: id, Project: project, MachineID: machine, Worktree: worktree, CCSessionID: cc}
}

func TestJudgeIdentityDivergenceNamesTheAxis(t *testing.T) {
	in := OpenSessionInput{Project: "p1", MachineID: "m1", Worktree: "/w", CCSessionID: "cc1"}

	cases := []struct {
		name   string
		others []model.Session
		want   []string // "axis:existing" 들
	}{
		{"갈림이 없으면 빈 것", nil, nil},
		{
			name:   "machine 이 다르다",
			others: []model.Session{sess("s-old", "p1", "m2", "/w", "cc1")},
			want:   []string{"machine:m2"},
		},
		{
			name:   "project 가 다르다",
			others: []model.Session{sess("s-old", "p2", "m1", "/w", "cc1")},
			want:   []string{"project:p2"},
		},
		{
			name:   "둘 다 다르면 둘 다 낸다",
			others: []model.Session{sess("s-old", "p2", "m2", "/w", "cc1")},
			want:   []string{"machine:m2", "project:p2"},
		},
		{
			// ★ 같은 옛 값을 든 행이 셋이어도 **줄은 하나**다. 읽는 쪽이 알아야 하는 것은
			//   "어떤 값으로 갈렸나"이지 "몇 행이 그 값을 들었나"가 아니다.
			name: "같은 값은 접는다",
			others: []model.Session{
				sess("s-a", "p1", "m2", "/w", "cc1"),
				sess("s-b", "p1", "m2", "/w2", "cc1"),
				sess("s-c", "p1", "m2", "/w3", "cc1"),
			},
			want: []string{"machine:m2"},
		},
		{
			name: "다른 값은 각각 낸다",
			others: []model.Session{
				sess("s-a", "p1", "m3", "/w", "cc1"),
				sess("s-b", "p1", "m2", "/w2", "cc1"),
			},
			want: []string{"machine:m2", "machine:m3"}, // 정렬 고정
		},
		{
			// 빈 값은 갈림이 아니다 — 없는 것과 다른 것은 다르다.
			name:   "빈 값은 세지 않는다",
			others: []model.Session{sess("s-a", "p1", "", "/w", "cc1")},
			want:   nil,
		},
	}

	for _, c := range cases {
		var got []string
		for _, d := range JudgeIdentityDivergence(in, c.others) {
			got = append(got, string(d.Axis)+":"+d.Existing)
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: %v 여야 하는데 %v 다", c.name, c.want, got)
		}
	}
}

// 문구는 사람이 읽는 자리다 — 무엇이 갈렸는지 값 둘이 다 있어야 한다.
func TestRenderDivergenceCarriesBothValues(t *testing.T) {
	got := RenderDivergence([]Divergence{
		{Axis: AxisMachine, Incoming: "m1", Existing: "m2", SessionID: "s-old"},
	})
	for _, want := range []string{"machine", "m1", "m2", "s-old"} {
		if !strings.Contains(got, want) {
			t.Errorf("문구에 %q 가 없다: %s", want, got)
		}
	}
	if RenderDivergence(nil) != "" {
		t.Error("갈림이 없는데 문구를 냈다")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 실물 경로 — 좌표계는 **이벤트 원장**이다. 로그는 형식이 안 정해져 단정할 수 없고,
// 원장은 이 기능이 남기기로 한 바로 그것이다.
// ─────────────────────────────────────────────────────────────────────────────

const divKind = "session.identity_divergence"

// 훅이 한 프롬프트에 여러 프로세스로 세션을 열어도 **원장에 한 줄**이어야 한다.
func TestIdentityDivergenceIsLoggedOncePerSessionAndMachine(t *testing.T) {
	svc, st := newSvc(t)
	ctx := context.Background()
	repo := t.TempDir()

	open := func(machine string) {
		t.Helper()
		if _, err := svc.OpenSession(ctx, OpenSessionInput{
			Project: "p1", ProjectPath: repo, MachineID: machine, Hostname: machine,
			Worktree: repo, CCSessionID: "cc-fanout",
		}); err != nil {
			t.Fatalf("세션 열기 실패(machine=%s): %v", machine, err)
		}
	}

	open("m-first")
	// ── 대조가 성립했는지 먼저 단정한다 ─────────────────────────────────────
	// 첫 세션만으로 이벤트가 나오면 아래 셈이 갈림을 재는 것이 아니게 된다.
	if n := countDivEvents(t, st); n != 0 {
		t.Fatalf("전제가 깨졌다 — 갈림이 없는데 이벤트가 %d건이다", n)
	}

	// 같은 대화 · 같은 워크트리 · **다른 머신**으로 네 번(훅의 프롬프트당 최대 프로세스 수).
	for i := 0; i < 4; i++ {
		open("m-second")
	}

	if n := countDivEvents(t, st); n != 1 {
		t.Errorf("원장에 갈림 이벤트가 %d건이다 — (세션, 머신) 조합당 1건이어야 한다.\n"+
			"created 일 때만 남기는 접기가 깨졌다면 프롬프트마다 원장이 4배로 증폭된다", n)
	}
}

// 머신이 또 달라지면 그것은 **새 사실**이라 한 줄 더 남아야 한다.
func TestAnotherMachineIsAnotherEvent(t *testing.T) {
	svc, st := newSvc(t)
	ctx := context.Background()
	repo := t.TempDir()

	for _, m := range []string{"m-a", "m-b", "m-c"} {
		if _, err := svc.OpenSession(ctx, OpenSessionInput{
			Project: "p1", ProjectPath: repo, MachineID: m, Hostname: m,
			Worktree: repo, CCSessionID: "cc-many",
		}); err != nil {
			t.Fatalf("세션 열기 실패(machine=%s): %v", m, err)
		}
	}
	// m-a 는 갈림 없음. m-b 와 m-c 가 각각 한 줄.
	if n := countDivEvents(t, st); n != 2 {
		t.Errorf("갈림 이벤트가 %d건이다 — 머신 셋 중 뒤 둘이 각각 남아 2건이어야 한다", n)
	}
}

// 갈림이 없으면 **아무것도 안 남긴다** — 정상 경로가 원장을 채우면 원장이 못 쓰게 된다.
func TestNoDivergenceLeavesTheLedgerAlone(t *testing.T) {
	svc, st := newSvc(t)
	ctx := context.Background()
	repo := t.TempDir()

	for i := 0; i < 3; i++ {
		if _, err := svc.OpenSession(ctx, OpenSessionInput{
			Project: "p1", ProjectPath: repo, MachineID: "m1", Hostname: "m1",
			Worktree: repo, CCSessionID: "cc-clean",
		}); err != nil {
			t.Fatalf("세션 열기 실패: %v", err)
		}
	}
	if n := countDivEvents(t, st); n != 0 {
		t.Errorf("갈림이 없는데 이벤트가 %d건 남았다", n)
	}
}

// countDivEvents 는 원장에 남은 갈림 이벤트 수다.
//
// 좌표계를 원장으로 잡은 이유: 로그는 형식이 안 정해져 단정할 수 없고, 원장은 이 기능이
// **남기기로 한 바로 그것**이다. "로그를 찍었다"는 단정은 무엇이 남았는지 말하지 못한다.
func countDivEvents(t *testing.T, st *store.Store) int {
	t.Helper()
	n, err := st.CountEvents(context.Background(), divKind, time.Time{})
	if err != nil {
		t.Fatalf("이벤트 조회 실패: %v", err)
	}
	return n
}
