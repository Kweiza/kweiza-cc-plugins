package service

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// 이 패키지의 시험은 **임시 DB + 실물 git 저장소**로 돈다.
//
// 가짜 리더만 쓰면 "내가 만든 값을 내가 읽는다"가 되어, git 이 판올림에서 출력을 바꾸거나
// 우리가 인자를 잘못 준 것은 원리적으로 안 잡힌다. 주입은 실물로 만들기 어려운 실패
// (읽기 도중의 타임아웃 등)에만 쓴다.

// TestMain 은 시험이 도는 머신의 git 설정에서 격리한다.
func TestMain(m *testing.M) {
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	os.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	// 실패 경로 시험이 많아 기본 로거를 그대로 두면 ERROR 줄이 시험 출력을 덮는다.
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

func newSvc(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dbp1 := filepath.Join(t.TempDir(), "fd.db")
	// ★ 적용은 기동에서 분리돼 있다(설계 §7 ①) — 열기 전에 올린다.
	if err := store.Migrate(context.Background(), dbp1, nil); err != nil {
		t.Fatalf("DB 적용 실패: %v", err)
	}
	st, err := store.OpenWithLogger(dbp1, log)
	if err != nil {
		t.Fatalf("DB 열기 실패: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return New(st, log), st
}

// newSvcWithClock 은 newSvc 와 같되 시계를 주입한다.
//
// ★ 시간을 고정하려고 두는 것이 아니다. **시계가 불리는 자리가 곧 창**인 갈래를 잠글 때 쓴다 —
// 주입한 함수 안에서 DB 를 건드리면 그 지점이 결정론적인 경합 창이 된다. 이 패키지 머리의
// 규율("주입은 실물로 만들기 어려운 실패에만 쓴다")과 같은 자리이고, 선례는
// outOfWindowLister(service.go:93-99)다.
func newSvcWithClock(t *testing.T, clock func() time.Time) (*Service, *store.Store) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dbp2 := filepath.Join(t.TempDir(), "fd.db")
	// ★ 적용은 기동에서 분리돼 있다(설계 §7 ①) — 열기 전에 올린다.
	if err := store.Migrate(context.Background(), dbp2, nil); err != nil {
		t.Fatalf("DB 적용 실패: %v", err)
	}
	st, err := store.OpenWithLogger(dbp2, log)
	if err != nil {
		t.Fatalf("DB 열기 실패: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return New(st, log, WithClock(clock)), st
}

// runGit 은 시험이 저장소를 **준비**할 때 쓰는 git 이다(피시험 코드가 아니다).
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{
		"-C", dir,
		"-c", "user.name=fd test",
		"-c", "user.email=fd@test.invalid",
		"-c", "commit.gpgsign=false",
	}, args...)
	out, err := exec.Command("git", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("준비용 git %v 실패: %v\n%s", args, err, out)
	}
	return string(out)
}

// tmpBase 는 심볼릭 링크를 푼 임시 경로다 — macOS 의 /tmp 는 링크라
// git 이 돌려주는 워크트리 경로와 안 맞는다.
func tmpBase(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("임시 경로 해석 실패: %v", err)
	}
	return base
}

// newRepo 는 커밋 하나가 있는 저장소를 만든다.
func newRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(tmpBase(t), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("디렉토리 생성 실패: %v", err)
	}
	runGit(t, repo, "init", "-q", "-b", "main", ".")
	writeFile(t, repo, "README.md", "hello\n")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-q", "-m", "init")
	return repo
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("디렉토리 생성 실패: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("파일 쓰기 실패(%s): %v", rel, err)
	}
}

// openSession 은 시험용 세션 하나를 연다.
func openSession(t *testing.T, s *Service, project, projectPath, worktree, ccID, label string) SessionResult {
	t.Helper()
	res, err := s.OpenSession(context.Background(), OpenSessionInput{
		Project: project, ProjectPath: projectPath, MachineID: "m1", Hostname: "testhost",
		Worktree: worktree, CCSessionID: ccID, Label: label,
	})
	if err != nil {
		t.Fatalf("세션 열기 실패(cc=%s): %v", ccID, err)
	}
	return res
}

// addItem 은 시험용 큐 항목 하나를 넣는다.
func addItem(t *testing.T, s *Service, project, id string, paths []string, after []model.After) model.Item {
	t.Helper()
	it, err := s.AddItem(context.Background(), AddItemInput{
		Project: project, ID: id, Title: id + " 제목", Body: id + " 본문",
		Paths: paths, After: after,
	})
	if err != nil {
		t.Fatalf("항목 등록 실패(%s): %v", id, err)
	}
	return it
}

// countRows 는 시험이 원장을 직접 볼 때 쓴다(pick_eval 의 소비자는 SQL 질의다).
func countRows(t *testing.T, st *store.Store, q string, args ...any) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("행 세기 실패(%s): %v", q, err)
	}
	return n
}

func ctx() context.Context { return context.Background() }
