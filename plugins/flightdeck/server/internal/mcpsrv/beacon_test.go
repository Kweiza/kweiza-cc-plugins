package mcpsrv

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/service"
	"github.com/kweiza/flightdeck/internal/window"
)

// newServerWithBeacon 은 newServer(server_test.go:74)의 꼴을 그대로 따르되
// WithBeaconDir 를 더하고, cc 는 CLAUDE_CODE_SESSION_ID 를 주거나 빼서 조절한다.
func newServerWithBeacon(t *testing.T, dir, cc string) *Server {
	t.Helper()
	svc, _ := newSvc(t)
	repo := newRepo(t)
	envs := fullEnv(repo)
	if cc == "" {
		delete(envs, EnvSessionID)
	} else {
		envs[EnvSessionID] = cc
	}
	return New(svc, discard(),
		WithEnv(env(envs)),
		WithCwd(repo, nil),
		WithHostname("testhost", nil),
		WithBeaconDir(dir),
	)
}

// newServerWithoutBeaconDir 는 정체는 온전하지만 WithBeaconDir 를 안 준 서버다.
func newServerWithoutBeaconDir(t *testing.T) *Server {
	t.Helper()
	svc, _ := newSvc(t)
	repo := newRepo(t)
	return New(svc, discard(),
		WithEnv(env(fullEnv(repo))),
		WithCwd(repo, nil),
		WithHostname("testhost", nil),
	)
}

// ★ 이 시험이 개정 ①(가드)과 시험 격리를 함께 지킨다.
// mcpsrv 에는 cmd/fd 의 TestUnpinnedEnvNeverReachesTheRealHome 같은 방어가 없다.
// WithBeaconDir 이 없을 때 기본 경로로 떨어지면 go test 가 개발자의 진짜
// ~/.flightdeck/windows/ 에 파일을 쓴다.
func TestNoBeaconDirMeansNoWrite(t *testing.T) {
	s := newServerWithoutBeaconDir(t)
	if _, ok := s.BeaconKey(); ok {
		t.Fatal("비콘 디렉토리를 안 줬는데 심을 좌표가 있다고 한다")
	}
}

func TestPlantsWhenIdentityIsWhole(t *testing.T) {
	dir := t.TempDir()
	s := newServerWithBeacon(t, dir, "cc-1")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(ents) != 1 {
		t.Fatalf("비콘 파일이 %d개다, 1개여야 한다", len(ents))
	}
	_ = s
}

// ★ 설계 개정 ① — Cursor 가 띄운 MCP 는 부모가 claude 가 아니고 CLAUDE_CODE_SESSION_ID 가 없다.
// 거기서 심으면 어떤 훅도 영영 못 맞추는 pid 로 키가 잡힌다.
func TestDoesNotPlantWhenIdentityIsHalf(t *testing.T) {
	dir := t.TempDir()
	_ = newServerWithBeacon(t, dir, "") // cc 없음
	ents, _ := os.ReadDir(dir)
	if len(ents) != 0 {
		t.Fatalf("정체가 반쪽인데 비콘을 %d개 심었다", len(ents))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ensureSession 이 비콘의 cc 를 우선하는지
// ─────────────────────────────────────────────────────────────────────────────

// callBoardOnce 는 도구를 한 번 부른다 — ensureSession 은 게을러서(mcpsrv.go:473 주석)
// 도구를 한 번도 안 부르면 세션 행도 안 생긴다. board 를 고른 이유는 세션 귀속을
// 요구하지 않는 도구 중 하나라 전제(성공)를 세우기 쉬워서다.
func callBoardOnce(t *testing.T, s *Server) {
	t.Helper()
	frames := serve(t, s, call("board", map[string]any{}))
	if len(frames) != 1 {
		t.Fatalf("board 응답이 %d개다, 1개여야 한다", len(frames))
	}
	if body, isErr := toolText(t, frames[0]); isErr {
		t.Fatalf("board 호출이 실패했다:\n%s", body)
	}
}

// lastOpenSessionCC 는 ensureSession 이 실제로 어떤 cc 로 카드를 열었는지 서비스에서
// 직접 확인한다. 응답 문자열을 파싱해서 대조하지 않는다 — 그 문자열을 만드는 경로가
// 바로 이 시험이 대조하려는 대상이면 순환이 된다(drift_test.go 의
// TestBoardShowsCCDriftInTheResponse 가 같은 이유로 svc 를 직접 친다).
//
// s.be 는 이 파일의 헬퍼가 실제 *service.Service 를 꽂아 만든 것이므로 캐스팅이 안전하다.
func lastOpenSessionCC(t *testing.T, s *Server) string {
	t.Helper()
	svc, ok := s.be.(*service.Service)
	if !ok {
		t.Fatalf("백엔드가 *service.Service 가 아니다: %T", s.be)
	}
	s.mu.Lock()
	sessionID := s.sessionID
	s.mu.Unlock()
	if sessionID == "" {
		t.Fatal("세션이 아직 안 열렸다 — callBoardOnce 를 먼저 불러야 한다")
	}
	view, err := svc.Board(context.Background(), s.id.ProjectID, service.BoardOptions{})
	if err != nil {
		t.Fatalf("보드 조회 실패: %v", err)
	}
	for _, c := range view.Sessions {
		if c.View.Session.ID == sessionID {
			return c.View.Session.CCSessionID
		}
	}
	t.Fatalf("세션 %q 이 보드에 없다", sessionID)
	return ""
}

// ★ 이것이 이 기능의 본체다. 훅이 비콘에 새 cc 를 적어 두면,
// 옛 cc 를 든 MCP 프로세스가 카드를 열 때 **새 cc 로 연다** — 그래서 카드가 한 장이 된다.
func TestEnsureSessionPrefersTheBeaconCC(t *testing.T) {
	dir := t.TempDir()
	s := newServerWithBeacon(t, dir, "cc-stale") // 프로세스의 env cc 는 낡았다
	k, ok := s.BeaconKey()
	if !ok {
		t.Fatal("비콘 좌표가 없다")
	}
	if _, err := window.SaveIdentity(dir, k, "cc-fresh", "card-A", time.Unix(0, 0)); err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}

	// 도구를 한 번 부른다 — ensureSession 은 게을러서 그 전에는 안 돈다.
	callBoardOnce(t, s)

	if got := lastOpenSessionCC(t, s); got != "cc-fresh" {
		t.Fatalf("MCP 가 %q 로 카드를 열었다, 비콘의 %q 여야 한다", got, "cc-fresh")
	}
}

// ★ 비콘이 없으면 오늘 거동이다 — 자기 env cc 로 연다. 새 실패 모드를 만들지 않는다.
func TestEnsureSessionFallsBackToItsOwnCC(t *testing.T) {
	dir := t.TempDir()
	s := newServerWithBeacon(t, dir, "cc-own")
	os.RemoveAll(dir) // 비콘을 없앤다
	callBoardOnce(t, s)
	if got := lastOpenSessionCC(t, s); got != "cc-own" {
		t.Fatalf("폴백이 %q 로 열었다, %q 여야 한다", got, "cc-own")
	}
}
