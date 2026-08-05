package service

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 포함 축과 좌표계 축은 **다른 축**이다. RelPathWithin 이 그 둘을 가른다.
//
// 이 시험이 없으면 다음 사람은 within 을 "상대화에 성공했나"로 읽는다. 그 둘은
// 다르다 — 상대경로 입력은 상대화할 것이 없어도 안이고, 볼륨이 다른 절대경로는
// 상대화에 실패해도 "밖"이라고 말하지 않는다.
func TestRelPathWithinSeparatesContainmentFromCoordinate(t *testing.T) {
	root := "/repo"

	cases := []struct {
		name       string
		root, in   string
		wantRel    string
		wantWithin bool
		why        string
	}{
		{
			name: "트리 안의 절대경로는 상대화되고 안이다",
			root: root, in: "/repo/cmd/fd/hook.go",
			wantRel: "cmd/fd/hook.go", wantWithin: true,
		},
		{
			name: "트리 밖은 원본을 남기고 밖이라고 말한다",
			root: root, in: "/tmp/claude/scratchpad/mut/repo/cmd/fd/hook.go",
			wantRel: "/tmp/claude/scratchpad/mut/repo/cmd/fd/hook.go", wantWithin: false,
			why: "옮길 좌표가 없으므로 지어내지 않는다 — 그러나 밖이라는 사실은 나른다",
		},
		{
			name: "문자열 접두로 자르지 않는다",
			root: "/a/b", in: "/a/bc/d.go",
			wantRel: "/a/bc/d.go", wantWithin: false,
			why: "접두로 하면 /a/bc/d.go 가 c/d.go 로 둔갑해 남의 저장소 파일이 이 저장소인 척한다",
		},
		{
			name: "형제 워크트리는 밖이다",
			root: "/repo/.flightdeck/worktrees/A", in: "/repo/.flightdeck/worktrees/B/cmd/x.go",
			wantRel: "/repo/.flightdeck/worktrees/B/cmd/x.go", wantWithin: false,
			why: "일부러 이렇다 — 형제 워크트리의 같은 이름 파일은 **다른 파일**이고, " +
				"카드의 워크트리 기준이어야 겹침 축이 병합 충돌 축과 일치한다",
		},
		{
			name: "상대경로는 상대화할 것이 없어도 안이다",
			root: root, in: "cmd/fd/hook.go",
			wantRel: "cmd/fd/hook.go", wantWithin: true,
			why: "이미 저장소 좌표계다 — 이것을 밖으로 접으면 git 이 주는 경로가 전부 죽는다",
		},
		{
			name: "root 를 모르면 판정하지 않는다(fail-open)",
			root: "", in: "/anywhere/x.go",
			wantRel: "/anywhere/x.go", wantWithin: true,
			why: "못 읽음을 '밖'으로 세면 워크트리를 못 읽은 세션의 발자국이 통째로 사라진다",
		},
		{
			name: "빈 경로는 안도 밖도 아니다 — 빈 rel 로 호출부가 거른다",
			root: root, in: "   ",
			wantRel: "", wantWithin: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rel, within := RelPathWithin(c.root, c.in)
			if rel != c.wantRel || within != c.wantWithin {
				t.Errorf("RelPathWithin(%q, %q) = (%q, %v), 기대 (%q, %v)\n%s",
					c.root, c.in, rel, within, c.wantRel, c.wantWithin, c.why)
			}
			// RelPath 는 이 함수의 껍질이어야 한다. 갈라지면 두 좌표계가 생긴다.
			if got := RelPath(c.root, c.in); got != rel {
				t.Errorf("RelPath 가 RelPathWithin 과 갈렸다: %q vs %q", got, rel)
			}
		})
	}
}

// 카드의 워크트리 밖 경로는 발자국이 되지 않는다 — 확정 재현의 모양 그대로다.
//
// 서브에이전트가 `cp -r` 로 저장소를 스크래치패드에 떴고, 그 사본의 경로가 발자국으로
// 기록돼 Stop 훅이 "항목을 선점하지 않고 고치고 있다"는 처방을 쐈다. 그 순간 실제
// 저장소와 그 세션의 워크트리는 둘 다 `git status` 0줄이었다.
func TestBeatDropsPathsOutsideTheCardWorktree(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	sess := openSession(t, s, "p", repo, repo, "cc-containment", "포함축")

	inside := filepath.Join(repo, "plugins", "flightdeck", "server", "cmd", "fd", "hook.go")
	// 저장소 밖의 스크래치패드 사본. 좌표계는 흠 없는 POSIX 절대경로라 좌표계 관문을
	// **통과한다** — 그래서 포함 축이 따로 필요했다.
	outside := filepath.Join(t.TempDir(), "scratchpad", "mut", "repo",
		"plugins", "flightdeck", "server", "cmd", "fd", "hook.go")

	if err := s.Beat(ctx(), sess.Session.ID, model.SignalTool, []string{inside, outside}); err != nil {
		t.Fatalf("바깥 경로 때문에 신호가 죽었다 — 거절이 아니라 버리는 축이다: %v", err)
	}

	// ── ① 신호는 산다. 훅을 죽이면 세션이 보드에서 사라진다.
	sig, err := st.Signals(ctx(), sess.Session.ID)
	if err != nil {
		t.Fatalf("신호 조회 실패: %v", err)
	}
	if _, ok := sig[model.SignalTool]; !ok {
		t.Fatalf("tool 신호가 안 남았다: %v", sig)
	}

	// ── ② 안쪽 경로만 발자국이 된다.
	fps, err := st.FootprintPaths(ctx(), sess.Session.ID)
	if err != nil {
		t.Fatalf("발자국 조회 실패: %v", err)
	}
	want := filepath.ToSlash(filepath.Join("plugins", "flightdeck", "server", "cmd", "fd", "hook.go"))
	if len(fps) != 1 || fps[0] != want {
		t.Fatalf("발자국 = %v, 기대 [%s].\n"+
			"저장소 밖 사본이 발자국에 들어가면 겹침 축과 처방이 그것을 근거로 발화한다 — "+
			"그 경로는 rel 로 못 옮겨 절대경로로 남고, 아무와도 안 겹치면서 판정만 오염시킨다",
			fps, want)
	}

	// ── ③ 조용히 사라지지 않는다. 원장에 건수와 경로가 남는다.
	evs, err := st.ListSessionEvents(ctx(), sess.Session.ID, "session.beat", time.Time{})
	if err != nil {
		t.Fatalf("이벤트 조회 실패: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("session.beat 이벤트가 %d건이다, want 1: %+v", len(evs), evs)
	}
	payload := evs[0].Payload
	if !strings.Contains(payload, `"outside":1`) {
		t.Errorf("원장에 포함 축이 버린 건수가 안 남았다: %s", payload)
	}
	if !strings.Contains(payload, `"count":1`) {
		t.Errorf("원장의 count 가 실제로 Touch 한 수와 다르다: %s", payload)
	}
	// ★ 좌표계 거절과 **따로** 센다. 합치면 무엇이 왜 사라졌는지가 다시 뭉개진다.
	if !strings.Contains(payload, `"rejected":0`) {
		t.Errorf("좌표계 거절이 0인데 그렇게 안 남았다 — 두 축이 한 칸에 합쳐졌다: %s", payload)
	}
	if !strings.Contains(payload, "scratchpad") {
		t.Errorf("버린 경로가 원장에 안 남았다 — 지워진 발자국은 아무 데도 안 나타난다: %s", payload)
	}
}

// 대조 — 관문이 너무 넓어지지 않았는지. 워크트리 안이면 깊어도 전부 남아야 한다.
//
// 이 갈래가 없으면 "전부 버린다"로 망가져도 위 시험은 초록이다(그쪽은 안쪽 경로가
// 하나뿐이라 0건이어도 len 단정에서만 걸린다). 여기서는 여러 건을 한 번에 본다.
func TestBeatKeepsEveryPathInsideTheWorktree(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	sess := openSession(t, s, "p", repo, repo, "cc-containment-inside", "포함축-대조")

	in := []string{
		filepath.Join(repo, "cmd", "fd", "hook.go"),
		filepath.Join(repo, "internal", "judge", "paths.go"),
		// 저장소 **안**의 워크트리 디렉토리. 세션이 주 저장소에서 열렸으므로 안이다.
		filepath.Join(repo, ".flightdeck", "worktrees", "X", "internal", "service", "session.go"),
	}
	if err := s.Beat(ctx(), sess.Session.ID, model.SignalTool, in); err != nil {
		t.Fatalf("비트 실패: %v", err)
	}

	fps, err := st.FootprintPaths(ctx(), sess.Session.ID)
	if err != nil {
		t.Fatalf("발자국 조회 실패: %v", err)
	}
	if len(fps) != len(in) {
		t.Fatalf("발자국이 %d건이다, want %d — 포함 관문이 너무 넓어져 안쪽 경로까지 버렸다: %v",
			len(fps), len(in), fps)
	}
}
