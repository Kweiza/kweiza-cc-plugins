package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wsRepo 는 루트 레포 하나와 그 안의 멤버 레포들을 만든다.
//
// 멤버는 **루트 레포 안의 디렉토리**이면서 자기 git 저장소다 — 그것이 이 배치의 모양이고,
// 루트의 `.gitignore` 가 그 폴더를 무시한다(README 절차가 그것을 못박는다).
func wsRepo(t *testing.T, yaml string, members ...string) string {
	t.Helper()
	root := newRepo(t)
	for _, m := range members {
		dir := filepath.Join(root, m)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("멤버 디렉토리 생성 실패(%s): %v", m, err)
		}
		runGit(t, dir, "init", "-q", "-b", "main", ".")
		writeFile(t, dir, "README.md", m+"\n")
		runGit(t, dir, "add", "-A")
		runGit(t, dir, "commit", "-q", "-m", "init "+m)
	}
	if yaml != "" {
		writeFile(t, root, ".flightdeck.yaml", yaml)
		// ★ **커밋한다.** 안 하면 이 시험이 통과해도 아무것도 안 증명한다 —
		//   서버가 읽는 것은 커밋된 파일이고, 트리만 고쳐 두면 명부가 늘 비어 나온다.
		runGit(t, root, "add", "-A")
		runGit(t, root, "commit", "-q", "-m", "명부")
	}
	return root
}

const twoMemberYAML = `schema: 1
name: cp-root
workspace:
  members:
    - project: search-api
      path: member-a
    - path: member-b
`

// 세션을 열면 루트 레포의 커밋된 명부가 캐시에 들어온다.
func TestOpenSessionAdoptsTheDeclaredRoster(t *testing.T) {
	svc, st := newSvc(t)
	root := wsRepo(t, twoMemberYAML, "member-a", "member-b")

	res := openSession(t, svc, "repo", root, root, "cc-ws-1", "")
	if !res.Workspace.Active() {
		t.Fatalf("워크스페이스가 안 섰다 — 사유: %q", res.WorkspaceDetail)
	}
	if got := res.Workspace.MemberIDs(); strings.Join(got, ",") != "member-b,search-api" {
		t.Fatalf("멤버=%v — [member-b search-api] 여야 한다(선언 id 가 이기고, 없으면 경로 마지막 마디)", got)
	}
	if res.Workspace.Root != "repo" {
		t.Fatalf("루트=%q", res.Workspace.Root)
	}
	if !strings.Contains(res.WorkspaceDetail, "멤버 2건") {
		t.Errorf("사유가 무엇을 했는지 안 말한다: %q", res.WorkspaceDetail)
	}

	// 캐시가 실제로 들어갔나 — 응답만 보면 «읽었지만 안 저장한» 것과 구별이 안 된다.
	got, err := st.WorkspaceMembers(context.Background(), "repo")
	if err != nil {
		t.Fatalf("명부 조회 실패: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("표에 %d건 — 2건이어야 한다", len(got))
	}
}

// 멤버 레포에서 띄운 세션은 **자기가 어느 워크스페이스의 멤버인지** 안다.
//
// 루트는 명부의 주인이라 project_member 의 행으로 안 나오고, 멤버는 주인이 아니라
// 자기 명부를 못 갖는다 — 그래서 Roster 가 두 방향을 다 본다. 이 시험이 ② 방향이다.
func TestMemberSessionSeesTheWorkspaceFromTheOtherSide(t *testing.T) {
	svc, _ := newSvc(t)
	root := wsRepo(t, twoMemberYAML, "member-a", "member-b")

	// 루트 세션이 먼저 명부를 심는다(실사용도 이 순서다 — 루트에서 띄운다).
	openSession(t, svc, "repo", root, root, "cc-ws-root", "")

	memberPath := filepath.Join(root, "member-a")
	res := openSession(t, svc, "search-api", memberPath, memberPath, "cc-ws-member", "")
	if !res.Workspace.Active() {
		t.Fatalf("멤버 쪽에서 워크스페이스가 안 보인다 — 사유: %q", res.WorkspaceDetail)
	}
	if res.Workspace.Root != "repo" {
		t.Fatalf("멤버가 본 루트=%q — repo 여야 한다", res.Workspace.Root)
	}
	// ★ 형제도 보여야 한다. 자원 배타가 «워크스페이스 하나»로 서려면 멤버가 형제의
	//   존재를 알아야 하고, 자기 행 하나만 읽으면 그 스코프를 못 만든다.
	if !res.Workspace.Knows("member-b") {
		t.Errorf("형제 member-b 를 모른다 — 명부=%v", res.Workspace.MemberIDs())
	}
}

// 명부가 없는 프로젝트는 **아무것도 안 바뀐다** — 단일 레포 전건이 이 갈래다.
func TestOpenSessionWithoutDeclarationChangesNothing(t *testing.T) {
	svc, st := newSvc(t)
	repo := newRepo(t) // .flightdeck.yaml 이 없다

	res := openSession(t, svc, "repo", repo, repo, "cc-solo", "")
	if res.Workspace.Active() {
		t.Fatalf("워크스페이스가 아닌데 섰다: %+v", res.Workspace)
	}
	if !strings.Contains(res.WorkspaceDetail, "워크스페이스가 아니다") {
		t.Errorf("사유가 부재를 안 말한다: %q", res.WorkspaceDetail)
	}
	if n := countRows(t, st, `SELECT count(*) FROM project_member`); n != 0 {
		t.Fatalf("명부 행이 %d건 생겼다", n)
	}
}

// 파일은 있는데 `workspace:` 블록이 없어도 같다.
func TestConfigWithoutWorkspaceBlockChangesNothing(t *testing.T) {
	svc, st := newSvc(t)
	root := wsRepo(t, "schema: 1\nname: solo\nlabels:\n  values: [a]\n")

	res := openSession(t, svc, "repo", root, root, "cc-noblock", "")
	if res.Workspace.Active() {
		t.Fatalf("블록이 없는데 워크스페이스가 섰다")
	}
	if !strings.Contains(res.WorkspaceDetail, "workspace 블록이 없다") {
		t.Errorf("사유=%q", res.WorkspaceDetail)
	}
	if n := countRows(t, st, `SELECT count(*) FROM project_member`); n != 0 {
		t.Fatalf("명부 행이 %d건 생겼다", n)
	}
}

// 파싱이 실패하면 **캐시를 안 건드리고 사유를 낸다.** 반쯤 읽은 명부로 덮으면
// 자원 배타가 조용히 좁아진다.
func TestBrokenDeclarationKeepsThePreviousRoster(t *testing.T) {
	svc, st := newSvc(t)
	root := wsRepo(t, twoMemberYAML, "member-a", "member-b")
	openSession(t, svc, "repo", root, root, "cc-1", "")
	if n := countRows(t, st, `SELECT count(*) FROM project_member`); n != 2 {
		t.Fatalf("전제가 안 섰다: 명부 %d건", n)
	}

	// 명부를 못 읽는 꼴로 고쳐 커밋한다(플로우 스타일 — 이 파서가 거절하는 축).
	writeFile(t, root, ".flightdeck.yaml", "workspace: {members: [{path: member-a}]}\n")
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-q", "-m", "명부를 깬다")

	res := openSession(t, svc, "repo", root, root, "cc-2", "")
	if !strings.Contains(res.WorkspaceDetail, "못 읽었다") {
		t.Errorf("사유가 파싱 실패를 안 말한다: %q", res.WorkspaceDetail)
	}
	if n := countRows(t, st, `SELECT count(*) FROM project_member`); n != 2 {
		t.Fatalf("깨진 파일이 명부를 %d건으로 바꿨다 — 안 건드려야 한다", n)
	}
	// ★ 그리고 **다음에도 말한다.** sha 를 찍어 두면 이 오류가 한 번 뜨고 영영 조용해진다.
	res2 := openSession(t, svc, "repo", root, root, "cc-3", "")
	if !strings.Contains(res2.WorkspaceDetail, "못 읽었다") {
		t.Errorf("두 번째 세션에서 침묵했다: %q", res2.WorkspaceDetail)
	}
}

// 명부에서 멤버를 빼면 캐시에서도 빠진다 — 통째 교체라 유령이 안 남는다.
func TestRemovingAMemberFromTheFileRemovesItFromTheCache(t *testing.T) {
	svc, st := newSvc(t)
	root := wsRepo(t, twoMemberYAML, "member-a", "member-b")
	openSession(t, svc, "repo", root, root, "cc-1", "")

	writeFile(t, root, ".flightdeck.yaml", "workspace:\n  members:\n    - path: member-b\n")
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-q", "-m", "멤버 하나를 뺀다")

	res := openSession(t, svc, "repo", root, root, "cc-2", "")
	if got := res.Workspace.MemberIDs(); strings.Join(got, ",") != "member-b" {
		t.Fatalf("멤버=%v — member-b 하나여야 한다", got)
	}
	if n := countRows(t, st, `SELECT count(*) FROM project_member`); n != 1 {
		t.Fatalf("표에 %d건 남았다 — 통째 교체라 1건이어야 한다", n)
	}
}

// sha 가 그대로면 파일을 다시 안 읽는다 — 세션 열기가 프롬프트마다 도는 자리다.
func TestRosterIsNotRereadWhenTheBranchHasNotMoved(t *testing.T) {
	svc, _ := newSvc(t)
	root := wsRepo(t, twoMemberYAML, "member-a", "member-b")
	openSession(t, svc, "repo", root, root, "cc-1", "")

	res := openSession(t, svc, "repo", root, root, "cc-2", "")
	if !strings.Contains(res.WorkspaceDetail, "다시 안 읽었다") {
		t.Errorf("두 번째 세션이 파일을 또 읽었다: %q", res.WorkspaceDetail)
	}
	// 명부 자체는 그대로 서 있어야 한다 — 안 읽는 것과 안 아는 것은 다르다.
	if !res.Workspace.Active() {
		t.Fatalf("안 읽었다고 명부까지 잃었다: %+v", res.Workspace)
	}
}

// 루트 자신을 멤버로 등재하면 거절한다 — 자원 정규화가 자기 참조가 된다.
func TestSelfAsMemberIsRefused(t *testing.T) {
	svc, st := newSvc(t)
	// 경로는 다른데 마지막 마디가 루트 프로젝트 id 와 같다.
	root := wsRepo(t, "workspace:\n  members:\n    - path: nested/repo\n", "nested/repo")

	res := openSession(t, svc, "repo", root, root, "cc-self", "")
	if res.Workspace.Active() {
		t.Fatalf("자기 참조 명부가 섰다: %+v", res.Workspace)
	}
	if !strings.Contains(res.WorkspaceDetail, "루트 자신과 같은") {
		t.Errorf("사유=%q", res.WorkspaceDetail)
	}
	if n := countRows(t, st, `SELECT count(*) FROM project_member`); n != 0 {
		t.Fatalf("명부 행이 %d건 생겼다", n)
	}
}

// 멤버 경로는 루트 상대로 저장되고, 절대경로는 **한 자리에서만** 만들어진다.
func TestMemberRepoPathJoinsFromTheRoot(t *testing.T) {
	svc, _ := newSvc(t)
	root := wsRepo(t, twoMemberYAML, "member-a", "member-b")
	res := openSession(t, svc, "repo", root, root, "cc-1", "")

	got, err := svc.MemberRepoPath(context.Background(), res.Workspace, "search-api")
	if err != nil {
		t.Fatalf("경로 해석 실패: %v", err)
	}
	if want := filepath.Join(root, "member-a"); got != want {
		t.Fatalf("경로=%q, 기대 %q", got, want)
	}
	// 루트 자신을 물으면 루트 경로다 — 그 갈래를 호출부마다 다시 판정하지 않게 여기서 접는다.
	if got, err := svc.MemberRepoPath(context.Background(), res.Workspace, "repo"); err != nil || got != root {
		t.Fatalf("루트 경로=%q err=%v — %q 여야 한다", got, err, root)
	}
	// 명부 밖은 거절이다 — 오타가 조용히 새 프로젝트를 만드는 것을 막는 자리다.
	if _, err := svc.MemberRepoPath(context.Background(), res.Workspace, "남의-레포"); err == nil {
		t.Fatal("명부 밖 프로젝트를 통과시켰다")
	}
}
