package service

import (
	"context"
	"testing"
)

// 이 파일이 지키는 것은 **주장 하나**다: "워크트리 축은 session_workspace 가 이미 갖고 있다."
//
// 그 주장은 조회 키에서 worktree 를 빼자는 안을 기각할 때 근거로 쓰였는데,
// **저장에만 참이고 표시에는 거짓이었다** — ListWorkspaces 의 비시험 호출자가 0건이라
// 카드·보드·겹침 어디에도 그 축이 안 나타난다. 게다가 담긴 값도 새롭지 않다:
// 실제로 도는 유일한 writer 가 넣는 primary 의 path 는 session.worktree 와 같은 값이다.
//
// 산문으로만 적어 두면 다음 사람이 같은 근거를 다시 쓴다. 그래서 지금 참인 상태를
// 시험으로 박는다. 이 시험이 빨개지는 것은 결함이 아니라 **주석의 만료 통지**다 —
// 표에 새로운 값이 들어오기 시작했다는 뜻이고, 그때 둘 중 하나를 해야 한다:
// 보드에 내보내거나, AddWorkspace 주석의 "담긴 값도 새롭지 않다"를 고치거나.

func TestWorkspaceTableHoldsNothingNewYet(t *testing.T) {
	svc, st := newSvc(t)
	ctx := context.Background()
	repo := t.TempDir()

	res, err := svc.OpenSession(ctx, OpenSessionInput{
		Project: "p1", ProjectPath: repo, MachineID: "m1", Hostname: "m1",
		Worktree: repo, CCSessionID: "cc-ws",
	})
	if err != nil {
		t.Fatalf("세션 열기 실패: %v", err)
	}

	ws, err := st.ListWorkspaces(ctx, res.Session.ID)
	if err != nil {
		t.Fatalf("워크스페이스 조회 실패: %v", err)
	}

	// ── 대조가 성립했는지 먼저 단정한다 ─────────────────────────────────────
	// 행이 아예 없으면 아래 검사는 "빈 것을 봤다"가 되어 아무것도 안 지킨다.
	if len(ws) == 0 {
		t.Fatal("전제가 깨졌다 — 세션을 열었는데 워크스페이스 행이 0건이다. writer 가 사라졌다")
	}

	if len(ws) != 1 {
		t.Errorf("워크스페이스가 %d건이다 — 지금은 1건(primary)이어야 한다.\n"+
			"부 워크스페이스가 실제로 들어오기 시작했다면 이 표는 더 이상 '세션 행의 사본'이 아니다:\n"+
			"  · 보드 카드에 '이 세션이 만진 트리들'로 내보내는 것을 다시 검토해라\n"+
			"  · store/session.go 의 AddWorkspace 주석(담긴 값이 새롭지 않다)을 고쳐라", len(ws))
	}
	for _, w := range ws {
		if !w.IsPrimary {
			t.Errorf("primary 가 아닌 행이 있다(path=%s) — 위와 같은 검토가 필요하다", w.Path)
		}
		if w.Path != res.Session.Worktree {
			t.Errorf("워크스페이스 경로 %q 가 세션의 워크트리 %q 와 다르다 — "+
				"이 표가 드디어 새 값을 담기 시작했다는 뜻이다", w.Path, res.Session.Worktree)
		}
	}
}

// 같은 세션을 다시 열어도 행이 늘지 않는다 — ON CONFLICT 가 그 일을 한다.
// 훅이 프롬프트당 여러 프로세스로 여는 경로가 있으므로(hook.go) 이 축은 실제로 눌린다.
func TestReopeningASessionDoesNotGrowTheWorkspaceTable(t *testing.T) {
	svc, st := newSvc(t)
	ctx := context.Background()
	repo := t.TempDir()

	var id string
	for i := 0; i < 4; i++ {
		res, err := svc.OpenSession(ctx, OpenSessionInput{
			Project: "p1", ProjectPath: repo, MachineID: "m1", Hostname: "m1",
			Worktree: repo, CCSessionID: "cc-ws-again",
		})
		if err != nil {
			t.Fatalf("%d회째 세션 열기 실패: %v", i+1, err)
		}
		id = res.Session.ID
	}
	ws, err := st.ListWorkspaces(ctx, id)
	if err != nil {
		t.Fatalf("워크스페이스 조회 실패: %v", err)
	}
	if len(ws) != 1 {
		t.Errorf("네 번 열었더니 워크스페이스가 %d건이다 — 1건이어야 한다", len(ws))
	}
}
