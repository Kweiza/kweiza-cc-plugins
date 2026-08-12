package service

import (
	"context"
	"strings"
	"testing"
)

// 클라이언트가 프로젝트 좌표를 **못 풀었다고 말해도** 이미 열린 세션은 계속 산다.
//
// ★ 이 시험이 막는 것은 이 브랜치가 **만들 뻔한 회귀**다.
//
// 진입점이 git 실패 시 프로젝트 id 를 안 짓게 되면(그것이 이 항목의 본체다), 그 좌표는
// 빈 채로 여기 온다. 그런데 세션 정체는 (machine, worktree, cc_session) 3중키이고
// **project 가 그 키에 안 들어간다** — 즉 서버는 프로젝트 없이도 이 세션이 누구인지 안다.
//
// 그러니 빈 좌표를 무조건 거절하면, git 이 **일시적으로** 안 읽히는 순간(워크트리가 막
// 만들어지는 중이거나 지워지는 중)에 훅이 물면 살아 있는 세션의 신호가 조용히 사라진다.
// 옛 동작에서는 (엉뚱한 이름으로나마) 성공하던 쓰기다 — a168c20 이 같은 모양의 회귀를
// 한 번 겪었고 리뷰가 잡았다(핸드오프 판단의 「내 고침이 회귀를 만들었다」).
//
// 그래서 순서를 이렇게 둔다: **3중키로 먼저 찾고, 찾으면 그 세션의 프로젝트로 잇는다.**
// 지어내는 것이 아니다 — 이 세션이 **처음 열릴 때 스스로 등록한** 좌표를 되읽는 것이다.
func TestEmptyProjectResumesTheExistingSessionByItsTriple(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	repo := t.TempDir()

	first, err := svc.OpenSession(ctx, OpenSessionInput{
		Project: "real-project", ProjectPath: repo,
		MachineID: "m-1", Hostname: "h", Worktree: repo, CCSessionID: "cc-1",
	})
	if err != nil {
		t.Fatalf("첫 세션이 안 열렸다: %v", err)
	}

	// 같은 3중키인데 프로젝트 좌표만 없다 — git 이 잠깐 안 읽힌 순간이다.
	again, err := svc.OpenSession(ctx, OpenSessionInput{
		Project: "", ProjectPath: "",
		MachineID: "m-1", Hostname: "h", Worktree: repo, CCSessionID: "cc-1",
	})
	if err != nil {
		t.Fatalf("살아 있는 세션이 좌표 하나 때문에 죽었다: %v", err)
	}
	if again.Session.ID != first.Session.ID {
		t.Fatalf("같은 3중키인데 다른 세션이 됐다: %q vs %q", again.Session.ID, first.Session.ID)
	}
	if again.Project.ID != "real-project" {
		t.Fatalf("이어붙인 프로젝트가 틀렸다: %q", again.Project.ID)
	}
	if again.Created {
		t.Fatal("재개여야 하는데 신규로 열렸다")
	}
}

// 그러나 **처음 열리는** 세션은 거절한다 — 이어붙일 과거가 없으면 지어낼 수밖에 없다.
//
// ★ 이것이 항목의 본체다. 옛 동작은 여기서 디렉토리 이름으로 프로젝트를 **자동 등록**했다.
func TestEmptyProjectIsRefusedForABrandNewSession(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	repo := t.TempDir()

	_, err := svc.OpenSession(ctx, OpenSessionInput{
		Project: "", ProjectPath: repo,
		MachineID: "m-1", Hostname: "h", Worktree: repo, CCSessionID: "cc-new",
	})
	if err == nil {
		t.Fatal("좌표 없는 새 세션이 그냥 열렸다 — 어느 프로젝트에 귀속됐는지 아무도 모른다")
	}

	// 거절은 **고칠 거리를 줘야 한다.** 항목 본문이 "정해야 할 것"으로 남긴 축이다:
	// 「지금 거절 문구가 사람에게 고칠 거리를 주는가 — 「git 을 못 읽었다」를 먼저 말해야 한다」.
	// 서버는 클라이언트가 왜 못 풀었는지 모르지만, **어디를 봐야 하는지**는 말할 수 있다.
	msg := err.Error()
	if !strings.Contains(msg, "git") {
		t.Fatalf("사유가 원인을 안 가리킨다 — 이 문구로는 못 고친다:\n%s", msg)
	}
	if !strings.Contains(msg, "FD_PROJECT") {
		t.Fatalf("탈출구(명시 지정)를 안 말한다:\n%s", msg)
	}
}

// 좌표가 비었는데 3중키의 과거도 없으면 **프로젝트를 만들지 않는다.**
//
// ★ 위 시험은 "거절한다"만 본다. 거절하면서 프로젝트를 먼저 만들어 두는 구현도 그 시험을
// 통과하는데, 그러면 고아 프로젝트라는 이 항목의 피해가 그대로 남는다.
func TestRefusedEmptyProjectLeavesNoGhostProject(t *testing.T) {
	svc, st := newSvc(t)
	ctx := context.Background()
	repo := t.TempDir()

	_, _ = svc.OpenSession(ctx, OpenSessionInput{
		Project: "", ProjectPath: repo,
		MachineID: "m-1", Hostname: "h", Worktree: repo, CCSessionID: "cc-new",
	})

	projects, err := st.ListProjects(ctx)
	if err != nil {
		t.Fatalf("프로젝트 목록 조회 실패: %v", err)
	}
	if len(projects) != 0 {
		var names []string
		for _, p := range projects {
			names = append(names, p.ID)
		}
		t.Fatalf("거절했는데 프로젝트가 남았다: %v", names)
	}
}
