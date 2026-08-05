package service

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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

// 항목이 선언한 경로도 카드의 워크트리 밖이면 발자국이 되지 않는다.
//
// ★ 이것은 Beat 와 **다른 문**이다. 4530e3c 는 문이 셋이라고 적었지만(Beat ·
// api.NormalizeFootprints · legacy.PlanImport) 넷이다. Pick 이 선점 트랜잭션 안에서
// item.Paths 를 origin=claimed 로 **직접** Touch 한다(pick.go 의 "항목이 선언한 경로를
// 이 세션의 발자국으로 남긴다" 절).
//
// 그 경로는 상류(AddItem·AddFollowup)의 judgeItemPathsCoordinate 만 지나온다. 그리고
// 그 관문이 쓰는 judge.JudgePathCoordinate 는 자기 주석에 이렇게 적어 놨다 —
// "★ 포함 축('이 경로가 프로젝트 안인가')도 여기 없다". POSIX 절대경로는 흠이 없어
// 통과하므로, `fd add --path /tmp/남의레포/x.go` 는 선점하는 순간 발자국이 된다.
//
// 실측(2026-08-05): claimed 발자국 140건 중 절대경로는 0건이다. 그것은 관문이 막아서가
// 아니라 아직 그런 항목을 아무도 안 만들었기 때문이다 — **닫힌 문이 아니라 아직 아무도
// 안 지나간 열린 문이다.**
func TestPickDropsItemPathsOutsideTheCardWorktree(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	sess := openSession(t, s, "p", repo, repo, "cc-pick-containment", "포함축-pick")

	// 저장소 밖의 사본. 좌표계 관문을 흠 없이 통과하는 POSIX 절대경로다.
	outside := filepath.Join(t.TempDir(), "scratchpad", "repo", "internal", "judge", "paths.go")
	addItem(t, s, "p", "batch7", []string{"internal/judge/", outside}, nil)

	res, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: sess.Session.ID, ItemID: "batch7"})
	if err != nil {
		t.Fatalf("바깥 경로 때문에 선점이 죽었다 — 거절이 아니라 버리는 축이다: %v", err)
	}

	// ── ① 선점은 산다. 항목 하나가 큐를 멈추면 안 된다(4530e3c 의 이관 규율과 같은 자리).
	if res.Mode != PickClaimed {
		t.Fatalf("선점 모드 = %s, want %s", res.Mode, PickClaimed)
	}

	// ── ② 안쪽 경로만 발자국이 된다.
	fps, err := st.FootprintPaths(ctx(), sess.Session.ID)
	if err != nil {
		t.Fatalf("발자국 조회 실패: %v", err)
	}
	if len(fps) != 1 || fps[0] != "internal/judge/" {
		t.Fatalf("발자국 = %v, 기대 [internal/judge/].\n"+
			"항목이 선언한 워크트리 밖 경로가 claimed 발자국이 되면 겹침 축이 오염된다 — "+
			"Beat 쪽은 4530e3c 가 막았는데 이 문만 열려 있으면 그것이 바로 항목이 없애려던 "+
			"'반쪽 발화'다", fps)
	}

	// ── ③ 조용히 사라지지 않는다. 원장에 건수와 경로가 남는다.
	evs, err := st.ListSessionEvents(ctx(), sess.Session.ID, "item.claim", time.Time{})
	if err != nil {
		t.Fatalf("이벤트 조회 실패: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("item.claim 이벤트가 %d건이다, want 1: %+v", len(evs), evs)
	}
	payload := evs[0].Payload
	if !strings.Contains(payload, `"outside":1`) {
		t.Errorf("원장에 포함 축이 버린 건수가 안 남았다: %s", payload)
	}
	// ★ 기존 "paths" 칸의 의미는 **안 바꾼다**(선언된 전부). Beat 의 count 가 두 번
	// 의미를 바꿔 원장 질의가 세 정의를 걸치게 된 전례가 바로 옆에 있다.
	// paths - outside 로 실제 Touch 수를 복원한다.
	if !strings.Contains(payload, `"paths":2`) {
		t.Errorf("선언된 경로 수(2)가 안 남았다 — 이 칸의 의미를 바꾸면 옛 질의가 조용히 틀린다: %s", payload)
	}
	if !strings.Contains(payload, "scratchpad") {
		t.Errorf("버린 경로가 원장에 안 남았다 — 지워진 발자국은 아무 데도 안 나타난다: %s", payload)
	}
}

// 대조 — 관문이 너무 넓어지지 않았는지. item.Paths 는 **보통 저장소 상대경로**이고
// 상대경로는 상대화할 것이 없어도 "안"이다(RelPathWithin 의 계약).
//
// 이 갈래가 없으면 관문이 "절대경로가 아닌 것을 전부 버린다"로 망가져도 위 시험은
// 초록이다. 그러면 claimed 발자국 축이 통째로 죽는데, 그 침묵은 오염보다 나쁘다 —
// 착수 직후 구간은 브랜치 diff 가 정의상 비어 있어 이 축만이 그 구간을 덮는다.
func TestPickKeepsRelativeItemPathsAsDeclared(t *testing.T) {
	s, st := newSvc(t)
	repo := newRepo(t)
	sess := openSession(t, s, "p", repo, repo, "cc-pick-containment-inside", "포함축-pick-대조")

	declared := []string{
		"internal/judge/paths.go",
		"internal/service/",
		// 저장소 **안**의 절대경로. 상대화해서 남긴다(Beat 와 같은 규율).
		filepath.Join(repo, "cmd", "fd", "hook.go"),
	}
	addItem(t, s, "p", "batch8", declared, nil)

	if _, err := s.Pick(ctx(), PickInput{Project: "p", SessionID: sess.Session.ID, ItemID: "batch8"}); err != nil {
		t.Fatalf("선점 실패: %v", err)
	}

	fps, err := st.FootprintPaths(ctx(), sess.Session.ID)
	if err != nil {
		t.Fatalf("발자국 조회 실패: %v", err)
	}
	if len(fps) != len(declared) {
		t.Fatalf("발자국이 %d건이다, want %d — 포함 관문이 상대경로까지 버렸다: %v",
			len(fps), len(declared), fps)
	}
	// ★ 상대경로는 **원본 그대로** 남아야 한다. 관문이 RelPathWithin 의 반환값을 그냥
	// 쓰면 filepath.Clean 이 후행 슬래시를 지워 "internal/service/" 가
	// "internal/service" 가 된다. 그 슬래시는 item.Paths 에서 **디렉토리 표기**이고
	// 겹침 축이 그것을 읽는다 — len 만 세는 단정으로는 이 회귀가 안 잡힌다.
	if !containsPath(fps, "internal/service/") {
		t.Fatalf("디렉토리 표기의 후행 슬래시가 사라졌다: %v.\n"+
			"item.Paths 는 훅이 준 파일 경로가 아니라 사람이 선언한 경로다 — "+
			"정규화하면 겹침 축의 디렉토리 판정이 조용히 바뀐다", fps)
	}
	// 안쪽 절대경로는 rel 로 옮겨서 남는다 — 원본 그대로 남으면 겹침 축에서 아무와도 안 맞는다.
	want := filepath.ToSlash(filepath.Join("cmd", "fd", "hook.go"))
	if !containsPath(fps, want) {
		t.Fatalf("워크트리 안 절대경로가 %q 로 안 옮겨졌다: %v", want, fps)
	}
}

func containsPath(paths []string, want string) bool {
	for _, p := range paths {
		if p == want {
			return true
		}
	}
	return false
}

// ─────────────────────────────────────────────────────────────────────────────
// 발자국으로 들어가는 문은 **둘**이다 — 전수로 잠근다
// ─────────────────────────────────────────────────────────────────────────────
//
// 왜 전수인가 — 이 축은 **한 문씩 고치면 더 나빠지는** 종류다. 관문이 어떤 문에서
// 발화하고 어떤 문에서 배신하면, 다음 사람은 "관문이 있다"만 배우고 없는 자리를 못
// 본다(항목 fd-containment-gate-only-on-one-of-three-doors 가 이것을 "반쪽 발화는
// 균일한 부재보다 나쁘다"로 적었다). 실제로 그 상태가 실재했다 — 4530e3c 가 Beat 를
// 닫았을 때 Pick 은 열린 채였고, **항목의 표에는 Pick 이 아예 없었다.**
//
// 각 문이 실제로 밖 경로를 버리는지는 위 행동 시험들이 본다. 이 시험이 잠그는 것은
// **문의 개수**다 — 문이 늘면 빨간불이 켜지고, 그 사람이 이 주석과 행동 시험을 읽는다
// (indexnotation_test.go 가 같은 규율로 표기 규약을 지킨다).
func TestFootprintDoorsAreExactlyTwo(t *testing.T) {
	root := serverRoot(t)

	// 문이 아닌 자리 하나: 저장 계층 자신이다(Store.Touch 가 Tx.Touch 로 넘긴다).
	// 판정이 아니라 배관이므로 관문이 여기 있으면 안 된다 — 있으면 호출부마다 다른
	// 규율(거절/버림)을 못 쓴다.
	const plumbing = "internal/store/session.go"

	wantDoors := map[string]string{
		"internal/service/session.go": "Beat — 훅이 관측한 경로(origin=observed)",
		"internal/service/pick.go":    "Pick — 항목이 선언한 경로(origin=claimed)",
	}

	touchRe := regexp.MustCompile(`\.Touch\(`)
	found := map[string]bool{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if !touchRe.Match(b) {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		found[filepath.ToSlash(rel)] = true
		return nil
	})
	if err != nil {
		t.Fatalf("소스 전수 실패: %v", err)
	}

	for f := range found {
		if f == plumbing {
			continue
		}
		if _, ok := wantDoors[f]; !ok {
			t.Errorf("발자국을 만드는 문이 하나 늘었다: %s\n"+
				"이 축은 문마다 관문이 다르면 무너진다 — 새 문에도 포함 축(RelPathWithin)을 "+
				"태우고, 이 시험의 목록과 containment_test.go 의 행동 시험을 함께 더해라.\n"+
				"관문 없이 늘리면 '관문이 있다'만 배우고 없는 자리는 아무도 못 본다", f)
		}
	}
	for f, why := range wantDoors {
		if !found[f] {
			t.Errorf("문 %s(%s)가 사라졌다 — 이 시험의 목록이 실제와 갈렸다", f, why)
		}
	}
	if !found[plumbing] {
		t.Errorf("저장 계층(%s)에서 Touch 가 사라졌다 — 이 시험의 좌표가 틀렸다", plumbing)
	}
}
