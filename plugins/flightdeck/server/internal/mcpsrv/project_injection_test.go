package mcpsrv

import (
	"strings"
	"testing"
)

// 주입이 **빈 값으로** 오면 그것도 주입이다 — 옛 규칙으로 되돌아가지 않는다.
//
// ★ 이 구분이 이 시험의 전부다. 진입점(cmd/fd)이 git 을 못 읽으면 프로젝트 id 를 **일부러
// 비워** 보낸다("지어내지 않는다"). 그런데 이 계층이 `if b.projectID != ""` 로만 봤을 때는
// 그 빈 값을 "주입이 아예 없다"와 같게 접어, 옛 폴백(경로의 마지막 성분)이 **같은 이름을
// 다시 지어냈다.** 실측: WithProject("", dir) → ProjectID="wt".
//
// 그러면 진입점의 고침이 이 한 줄에서 통째로 무효가 된다 — 그리고 조용하다.
func TestEmptyProjectInjectionDoesNotWakeTheLegacyFallback(t *testing.T) {
	dir := "/tmp/some-worktree/wt"
	env := func(k string) (string, bool) {
		if k == EnvSessionID {
			return "cc-1", true
		}
		return "", false
	}
	srv := New(nil, nil,
		WithEnv(env), WithCwd(dir, nil), WithHostname("h", nil),
		WithProject("", dir), // ← 진입점이 "모른다"고 말한 상태
		WithMachine("m-1"), WithWorktree(dir))

	id := srv.Identity()
	if id.ProjectID != "" {
		t.Fatalf("빈 주입을 무시하고 좌표를 지어냈다: %q (옛 결함 그대로다)", id.ProjectID)
	}
	if !containsAxis(id.Missing, axisProject) {
		t.Fatalf("결손 축에 project 가 없다: %v — 배너가 그 사실을 말할 수 없다", id.Missing)
	}
}

// 주입이 **아예 없으면** 옛 규칙 그대로다 — 이 패키지를 단독으로 쓰는 경우다.
//
// ★ 위 시험만 있으면 `if b.projectID != ""` 를 통째로 지우는 것으로도 통과한다. 그러면
// 주입 없는 사용처가 좌표를 잃는다. 두 갈래가 **다르게** 남는지를 여기서 잠근다.
func TestAbsentProjectInjectionStillFallsBackWithAWarning(t *testing.T) {
	dir := "/tmp/some-worktree/wt"
	env := func(k string) (string, bool) {
		if k == EnvSessionID {
			return "cc-1", true
		}
		return "", false
	}
	srv := New(nil, nil,
		WithEnv(env), WithCwd(dir, nil), WithHostname("h", nil),
		WithMachine("m-1"), WithWorktree(dir)) // ← WithProject 를 안 준다

	id := srv.Identity()
	if id.ProjectID != "wt" {
		t.Fatalf("주입 없는 폴백이 깨졌다: %q", id.ProjectID)
	}
	if !strings.Contains(id.Banner(), "경로의 마지막 성분") {
		t.Fatalf("폴백을 썼다는 사실이 배너에 없다:\n%s", id.Banner())
	}
}

// 배너가 **실제로 되는 것만** 약속한다.
//
// ★ GateTool 은 ProjectID 가 비면 board·alloc 을 포함해 **전부** 거절한다(identity.go 의
// 첫 갈래가 sessionBoundTools 검사보다 앞이다). 그런데 배너는 결손이 무엇이든
// "되는 것: 읽기(board)·발번(alloc)" 을 찍었다 — 프로젝트 축이 빠진 경우에 **거짓말**이다.
//
// 이 자리는 여태 도달 불가라 아무도 안 봤다: 폴백이 ProjectID 를 늘 채워서 첫 갈래가 거의
// 안 걸렸기 때문이다. 위 두 시험이 그 길을 열었으므로 이 문구도 같이 참이 돼야 한다.
func TestBannerDoesNotPromiseBoardWithoutAProjectCoordinate(t *testing.T) {
	id := Identity{Missing: []string{"project"}}
	banner := id.Banner()

	if ok, _ := GateTool("board", id); ok {
		t.Fatal("전제가 깨졌다 — GateTool 이 프로젝트 없이 board 를 통과시킨다. 이 시험을 다시 써라")
	}
	if strings.Contains(banner, "되는 것: 읽기(board)") {
		t.Fatalf("배너가 막힌 도구를 된다고 약속한다:\n%s", banner)
	}
	if !strings.Contains(banner, "board") {
		t.Fatalf("board 가 왜 안 되는지를 아무 데서도 안 말한다:\n%s", banner)
	}
}
