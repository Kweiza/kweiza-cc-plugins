package mcpsrv

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/service"
	"github.com/kweiza/flightdeck/internal/window"
)

// ★ 이 파일은 **모든 플랫폼에서 돈다.** 여기 단정은 전부 "비콘이 없을 때"의 것이라
// window.StartedOf 가 없어도 성립한다 — 오히려 그 플랫폼에서 특히 지켜야 하는 것들이다
// (심기 가드 · 폴백 · WithBeaconDir 없이는 개발자의 진짜 홈에 안 쓴다).
// 비콘이 실제로 심긴 것을 단정하는 시험은 beacon_linux_test.go 에 있다.

// newServerWithBeacon 은 newServer(server_test.go:74)의 꼴을 그대로 따르되
// WithBeaconDir 를 더하고, cc 는 CLAUDE_CODE_SESSION_ID 를 주거나 빼서 조절한다.
func newServerWithBeacon(t *testing.T, dir, cc string) *Server {
	t.Helper()
	s, _, _ := newServerWithBeaconAndSvc(t, dir, cc)
	return s
}

// newServerWithBeaconAndSvc 는 같은 서버를 만들되 **그 서버가 실제로 쓰는** 서비스와
// 워크트리를 함께 낸다. 표류 시험은 남의 카드를 하나 더 심어야 하는데, 그것을 응답
// 문자열이 아니라 서비스에 직접 넣어야 순환 전제가 안 된다(drift_test.go 머리 참고).
func newServerWithBeaconAndSvc(t *testing.T, dir, cc string) (*Server, *service.Service, string) {
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
	), svc, repo
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

// ★ why 는 사람이 **원인에 도달하라고** 있는 자리다. 그래서 틀린 원인을 이름으로 대는 것이
// 아무 이름도 안 대는 것보다 나쁘다. 리눅스 밖에서는 StartedOf 가 ErrUnsupported 를 내는데,
// 그 오류를 삼키고 "부모가 claude 가 아니다"라고 말하면 읽는 사람이 아무 문제도 없는
// 자기 프로세스 계보를 뒤진다.
func TestBeaconMissReasonNamesTheUnsupportedPlatform(t *testing.T) {
	got := beaconMissReason(fmt.Errorf("부모(pid 7)의 시작 시각을 못 읽었다: %w", window.ErrUnsupported))
	if !strings.Contains(got, "플랫폼") {
		t.Errorf("ErrUnsupported 인데 플랫폼을 이름으로 대지 않는다: %q", got)
	}
	if strings.Contains(got, "부모") {
		t.Errorf("틀린 원인을 댄다 — 계보를 읽을 수 없는 플랫폼이지 부모가 이상한 것이 아니다: %q", got)
	}
	// 그 밖의 사유는 오류가 이미 자기 말을 갖고 있다. 덧칠하지 않는다.
	if got := beaconMissReason(errors.New("이 프로세스의 정체가 반쪽이라 비콘 좌표를 만들 수 없다")); got != "이 프로세스의 정체가 반쪽이라 비콘 좌표를 만들 수 없다" {
		t.Errorf("사유를 덧칠했다: %q", got)
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
