package mcpsrv

import (
	"os"
	"testing"
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
