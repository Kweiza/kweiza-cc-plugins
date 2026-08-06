package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func id(ino uint64, mtime int64) ExeID {
	return ExeID{OK: true, Dev: 1, Ino: ino, Size: 100, MtimeNano: mtime}
}

func TestDecideDoesNothingWhenUnchanged(t *testing.T) {
	a := id(10, 1000)
	got, why := Decide(a, a, ExeID{}, nil)
	if got != ActNothing {
		t.Fatalf("안 바뀌었는데 %v 다 — %s", got, why)
	}
}

func TestDecideVerifiesWhenInodeChanged(t *testing.T) {
	got, why := Decide(id(10, 1000), id(11, 1000), ExeID{}, nil)
	if got != ActVerify {
		t.Fatalf("아이노드가 바뀌었는데 %v 다 — %s", got, why)
	}
	if strings.TrimSpace(why) == "" {
		t.Fatal("사유가 비었다")
	}
}

func TestDecideVerifiesWhenOnlyMtimeChanged(t *testing.T) {
	// 같은 자리에 같은 크기로 덮어써도 교체다. mv 가 아니라 cp 로 배포하는 경로가 있다.
	if got, _ := Decide(id(10, 1000), id(10, 2000), ExeID{}, nil); got != ActVerify {
		t.Fatalf("mtime 만 바뀌었는데 %v 다", got)
	}
}

// ★ stat 실패는 교체가 아니다. exec 할 대상이 없는데 exec 로 가면 서버가 사라진다.
func TestDecideDoesNothingWhenStatFails(t *testing.T) {
	got, why := Decide(id(10, 1000), ExeID{}, ExeID{}, errors.New("no such file"))
	if got != ActNothing {
		t.Fatalf("stat 이 실패했는데 %v 다 — %s", got, why)
	}
	if !strings.Contains(why, "no such file") {
		t.Fatalf("사유가 원인을 안 나른다: %q", why)
	}
}

// ★ 같은 고장난 바이너리를 30초마다 다시 돌리지 않는다.
func TestDecideSkipsAlreadyFailedBuild(t *testing.T) {
	bad := id(11, 1000)
	if got, _ := Decide(id(10, 1000), bad, bad, nil); got != ActNothing {
		t.Fatalf("이미 실패한 판인데 %v 다", got)
	}
}

// 사람이 고쳐서 파일이 **또** 바뀌면 다시 시도한다.
func TestDecideRetriesAfterTheFileChangesAgain(t *testing.T) {
	bad := id(11, 1000)
	if got, _ := Decide(id(10, 1000), id(12, 3000), bad, nil); got != ActVerify {
		t.Fatalf("고친 뒤인데 %v 다", got)
	}
}

func TestSameRequiresBothOK(t *testing.T) {
	a := id(10, 1000)
	if a.Same(ExeID{}) {
		t.Fatal("관측 안 된 값과 같다고 했다")
	}
	if (ExeID{}).Same(ExeID{}) {
		t.Fatal("둘 다 관측 안 됐는데 같다고 했다 — 그것은 '같다'가 아니라 '모른다'다")
	}
}

// tick 은 감시기를 정확히 한 회차만 돌린다.
func (w *selfWatcher) tick(ctx context.Context, drain func()) Action {
	return w.step(ctx, drain)
}

// binDir 에 "" 를 주는 것은 **못 덮는 갈래를 안 켠다**는 뜻이다(견줄 자리가 없다).
// 그 축은 TestNewSelfWatcherNamesTheBranchItCannotCover 가 따로 잠근다 — 여기 섞으면
// 감시 사슬 시험 전부가 Uncovered 문구에 매인다.
func newTestWatcher(t *testing.T) *selfWatcher {
	t.Helper()
	w := newSelfWatcher(slog.New(slog.DiscardHandler), "/tmp/does-not-matter.db", "")
	w.start = id(10, 1000)
	w.exePath = "/fake/fd"
	return w
}

func TestWatcherDoesNothingWhenBinaryUnchanged(t *testing.T) {
	w := newTestWatcher(t)
	w.stat = func(string) (ExeID, error) { return id(10, 1000), nil }
	w.verify = func(context.Context, string, string) (string, error) {
		t.Fatal("검증하면 안 된다")
		return "", nil
	}
	w.execSelf = func(string, []string, []string) error { t.Fatal("exec 하면 안 된다"); return nil }

	if got := w.tick(context.Background(), func() { t.Fatal("드레인하면 안 된다") }); got != ActNothing {
		t.Fatalf("%v 다", got)
	}
}

// ★ 이 시험이 이 태스크의 본체다 — 프로세스를 안 죽이고 exec 를 단언한다.
func TestWatcherExecsAfterVerifyPasses(t *testing.T) {
	w := newTestWatcher(t)
	w.stat = func(string) (ExeID, error) { return id(11, 2000), nil }
	w.verify = func(context.Context, string, string) (string, error) { return "1d044b2 · 2026-08-05T00:11:57Z", nil }

	drained := false
	var gotExe string
	var gotArgs []string
	w.execSelf = func(exe string, argv, env []string) error {
		if !drained {
			// ★ 드레인이 먼저인 이유는 요청을 살리기 위해서가 **아니다.** 드레인은
			// serveCtx 를 취소하므로 인플라이트 요청 컨텍스트도 함께 끊긴다(api.go 의
			// BaseContext). 끊긴 쓰기의 안전은 클라이언트 아웃박스 + 멱등키가 준다(설계 §3①).
			// 순서가 계약인 진짜 이유는 **포트다**: 리스너가 닫히기 전에 exec 하면
			// 새 이미지가 이미 쓰이고 있는 주소에 붙으려다 죽는다.
			t.Fatal("드레인 전에 exec 했다 — 새 이미지가 아직 열려 있는 포트에 붙으려다 죽는다")
		}
		gotExe, gotArgs = exe, argv
		return nil
	}

	if got := w.tick(context.Background(), func() { drained = true }); got != ActExec {
		t.Fatalf("%v 다", got)
	}
	if gotExe != "/fake/fd" {
		t.Fatalf("exec 경로가 %q 다", gotExe)
	}
	if len(gotArgs) == 0 || gotArgs[0] != os.Args[0] {
		t.Fatalf("argv 를 그대로 안 넘겼다: %v", gotArgs)
	}
}

func TestWatcherRefusesAndRemembersFailedBuild(t *testing.T) {
	w := newTestWatcher(t)
	w.stat = func(string) (ExeID, error) { return id(11, 2000), nil }
	calls := 0
	w.verify = func(context.Context, string, string) (string, error) {
		calls++
		return "", errors.New("selfcheck exit 1 — 증분 계획이 거절된다")
	}
	w.execSelf = func(string, []string, []string) error { t.Fatal("검증에 실패했는데 exec 했다"); return nil }

	if got := w.tick(context.Background(), func() {}); got != ActRefuse {
		t.Fatalf("1회차가 %v 다", got)
	}
	// 2회차 — 같은 판이면 다시 검증하지 않는다
	if got := w.tick(context.Background(), func() {}); got != ActNothing {
		t.Fatalf("2회차가 %v 다", got)
	}
	if calls != 1 {
		t.Fatalf("같은 고장난 판을 %d번 검증했다", calls)
	}

	st := w.Status()
	if st.Outcome != "refused" || !strings.Contains(st.Detail, "selfcheck") {
		t.Fatalf("거절이 상태에 안 남았다: %+v", st)
	}
	if st.LastAt.IsZero() {
		t.Fatal("시도 시각이 안 남았다")
	}
}

// ★ TOCTOU 회귀 시험. stat(Decide 용) 과 verify 사이에는 창이 없지만, verify 자체가
// 걸리는 시간(≤selfVerifyTimeout) 동안 파일이 또 바뀔 수 있다 — 그러면 방금 검증한
// 것은 지금 디스크에 있는 파일이 아니다. drain 없이 물러나야 한다.
func TestWatcherRefusesWhenBinaryChangesDuringVerify(t *testing.T) {
	w := newTestWatcher(t)
	calls := 0
	w.stat = func(string) (ExeID, error) {
		calls++
		if calls == 1 {
			return id(11, 2000), nil // Decide 가 보는 "now"
		}
		return id(12, 3000), nil // verify 뒤 재확인 — 그새 또 바뀌었다
	}
	w.verify = func(context.Context, string, string) (string, error) {
		return "1d044b2 · 2026-08-05T00:11:57Z", nil // 검증 자체는 통과한다
	}
	w.execSelf = func(string, []string, []string) error {
		t.Fatal("검증한 판이 이미 지나간 판인데 exec 했다")
		return nil
	}

	got := w.tick(context.Background(), func() { t.Fatal("검증한 판이 아닌데 드레인했다") })
	if got != ActRefuse {
		t.Fatalf("%v 다", got)
	}
	if calls != 2 {
		t.Fatalf("재확인 stat 을 안 했다(calls=%d)", calls)
	}
	// ★ 이 갈래도 화면까지 가야 한다. 서버 로그를 안 보는 사람에게 "검증까지 통과했는데
	// 왜 안 바뀌나"의 답이 아무 데도 없었다.
	if st := w.Status(); st.Outcome != "refused" || !strings.Contains(st.Detail, "또 바뀌었다") {
		t.Fatalf("TOCTOU 건너뜀이 상태에 안 남았다: %+v", st)
	}
	// 다음 회차가 새 판을 다시 봐야 하므로 lastFail 은 안 타야 한다.
	if w.lastFail.OK {
		t.Fatal("늦었을 뿐인 판을 실패로 태웠다 — 다음 회차가 그 판을 다시 안 본다")
	}
}

// ★ 종료 의사는 **신호 컨텍스트**로 묻는다. step 에 들어오는 ctx 는 watchCtx 이고
// 그것은 stopWatch() 로만 끊긴다 — 즉 api.Serve 가 이미 돌아온 뒤다. 운영자가 방금
// SIGTERM 을 보낸 순간에 watchCtx 는 멀쩡하다. 그 상태로 검증이 통과하면
// "멈추라는 요청 뒤에 fd serve 가 되살아난다".
func TestWatcherDoesNotExecWhenShutdownRequestedDuringVerify(t *testing.T) {
	w := newTestWatcher(t)
	w.stat = func(string) (ExeID, error) { return id(11, 2000), nil }

	shutdown := false
	w.shutdownRequested = func() bool { return shutdown }
	w.verify = func(context.Context, string, string) (string, error) {
		shutdown = true // 검증이 도는 동안 SIGTERM 이 왔다
		return "1d044b2 · 2026-08-05T00:11:57Z", nil
	}
	w.execSelf = func(string, []string, []string) error {
		t.Fatal("종료 요청이 왔는데 exec 했다")
		return nil
	}

	// ctx 는 **안 끊는다** — 운영 경로에서 watchCtx 는 이 시점에 멀쩡하기 때문이다.
	got := w.tick(context.Background(), func() { t.Fatal("종료 요청이 왔는데 드레인했다") })
	if got != ActNothing {
		t.Fatalf("%v 다", got)
	}
}

// ★ 종료 중에 자식이 죽어서 난 검증 실패는 **후보의 잘못이 아니다.**
// 그것을 거절로 적으면 정상 종료 로그에 `selfcheck 실패(signal: killed)` 가 남고,
// lastFail 이 멀쩡한 판을 태워 다음 기동이 그 판을 다시 안 본다.
func TestWatcherDoesNotRecordRefusalWhenVerifyIsKilledByShutdown(t *testing.T) {
	w := newTestWatcher(t)
	w.stat = func(string) (ExeID, error) { return id(11, 2000), nil }
	w.shutdownRequested = func() bool { return true }
	w.verify = func(context.Context, string, string) (string, error) {
		// 실제 verifyWithSelfcheck 가 내는 모양이다 — exec.CommandContext 가 자식을 죽였다.
		return "", errors.New("selfcheck 실패(signal: killed): ")
	}
	w.execSelf = func(string, []string, []string) error { t.Fatal("종료 중인데 exec 했다"); return nil }

	if got := w.tick(context.Background(), func() { t.Fatal("드레인했다") }); got != ActNothing {
		t.Fatalf("%v 다", got)
	}
	if st := w.Status(); st.Outcome != "" {
		t.Fatalf("정상 종료인데 거절이 기록됐다: %+v", st)
	}
	if w.lastFail.OK {
		t.Fatal("멀쩡한 판을 실패로 태웠다")
	}
}

// ★ 진짜 창은 drain() **전체**다. drain 은 api.Serve 의 셧다운 유예만큼 매달릴 수 있고,
// 그 사이에 온 SIGTERM 은 <-served 를 그 종료로 풀어 준다 — 아무 저항 없이 exec 에 닿는다.
func TestWatcherDoesNotExecWhenShutdownArrivesDuringDrain(t *testing.T) {
	w := newTestWatcher(t)
	w.stat = func(string) (ExeID, error) { return id(11, 2000), nil }
	w.verify = func(context.Context, string, string) (string, error) { return "1d044b2", nil }

	shutdown := false
	w.shutdownRequested = func() bool { return shutdown }
	w.execSelf = func(string, []string, []string) error {
		t.Fatal("드레인 중 종료 요청이 왔는데 exec 했다 — 되돌릴 수 없는 자리다")
		return nil
	}

	if got := w.tick(context.Background(), func() { shutdown = true }); got != ActNothing {
		t.Fatalf("%v 다", got)
	}
}

// ★ 반대편 회귀. stopWatch() 는 close(served) **직후** 정상적으로 불린다 —
// 드레인 뒤에 watchCtx 를 종료 의사로 읽으면 멀쩡한 재기동이 매번 접혀 기능이 죽는다.
func TestWatcherStillExecsWhenWatchCtxIsCanceledDuringDrain(t *testing.T) {
	w := newTestWatcher(t)
	w.stat = func(string) (ExeID, error) { return id(11, 2000), nil }
	w.verify = func(context.Context, string, string) (string, error) { return "1d044b2", nil }
	w.shutdownRequested = func() bool { return false } // 사람은 안 껐다

	ctx, cancel := context.WithCancel(context.Background())
	execd := false
	w.execSelf = func(string, []string, []string) error { execd = true; return nil }

	// serveWithWatcher 가 하는 것 그대로: 드레인이 풀린 직후 stopWatch().
	if got := w.tick(ctx, func() { cancel() }); got != ActExec {
		t.Fatalf("%v 다 — 정상적인 stopWatch() 를 종료 의사로 읽었다", got)
	}
	if !execd {
		t.Fatal("exec 를 안 했다")
	}
}

// ★ 컨테이너에서는 감시를 아예 안 켠다. 켜 두면 "보는 중"이라고 말하면서
// 영원히 안 올 교체를 기다린다 — 침묵보다 나쁘다(따라오고 있다고 믿게 만든다).
func TestContainerIsNotWatched(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		hasDockerEnv, hasDataDir bool
		wantContainer            bool
	}{
		{"맨몸", false, false, false},
		{"/.dockerenv 있음", true, false, true},
		{"/data 볼륨만 있음", false, true, true},
		{"둘 다", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, why := containerVerdict(tc.hasDockerEnv, tc.hasDataDir)
			if got != tc.wantContainer {
				t.Fatalf("컨테이너 판정 = %v, 기대 %v (사유 %q)", got, tc.wantContainer, why)
			}
			if got && !strings.Contains(why, "컨테이너") {
				t.Fatalf("사유가 컨테이너를 안 말한다: %q", why)
			}
			if !got && strings.TrimSpace(why) != "" {
				t.Fatalf("컨테이너가 아닌데 사유가 있다: %q", why)
			}
		})
	}
}

// ★ 이 시험이 1번 결함의 본체다. 실행 파일을 영영 못 재는 감시기는 **아무것도 못 하는데**
// 지금까지 어느 화면에도 안 떴다 — /healthz 는 watching=true 만 말하고 `fd doctor` 는
// "보는 중 — 아직 교체를 못 봤다"라고 찍었다. 서버는 옛 코드로 영원히 산다.
func TestWatcherReportsThatItCannotMeasureTheBinary(t *testing.T) {
	var buf bytes.Buffer
	w := newTestWatcher(t)
	w.log = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	w.stat = func(string) (ExeID, error) { return ExeID{}, errors.New("no such file or directory") }

	for i := 0; i < 3; i++ {
		if got := w.tick(context.Background(), func() { t.Fatal("드레인했다") }); got != ActNothing {
			t.Fatalf("%d회차가 %v 다", i+1, got)
		}
	}

	st := w.Status()
	if !strings.Contains(st.Stalled, "no such file") {
		t.Fatalf("못 잰다는 사실이 상태에 안 남았다: %+v", st)
	}
	// ★ 30초 티커가 같은 줄을 쌓으면 그 로그는 읽히지 않는 배경이 된다 — 침묵과 같다.
	if n := strings.Count(buf.String(), "실행 파일을 못 재고 있다"); n != 1 {
		t.Fatalf("같은 사유를 %d번 찍었다 — 한 번만 찍어야 한다:\n%s", n, buf.String())
	}
}

// 다시 재지면 그 사실을 지운다. 지나간 고장을 현재형으로 남기면 반대 방향으로 거짓말한다.
func TestWatcherClearsTheStallOnceItCanMeasureAgain(t *testing.T) {
	w := newTestWatcher(t)
	broken := true
	w.stat = func(string) (ExeID, error) {
		if broken {
			return ExeID{}, errors.New("no such file or directory")
		}
		return id(10, 1000), nil // 기준값 그대로 — 교체는 없었다
	}

	w.tick(context.Background(), func() {})
	if w.Status().Stalled == "" {
		t.Fatal("막힌 사실이 안 남았다")
	}
	broken = false
	w.tick(context.Background(), func() {})
	if s := w.Status().Stalled; s != "" {
		t.Fatalf("회복했는데 여전히 막혔다고 말한다: %q", s)
	}
}

// stat 이 오류 없이 관측 실패(OK=false)를 내는 갈래도 같은 대접을 받아야 한다.
func TestWatcherReportsUnmeasurableEvenWithoutAnError(t *testing.T) {
	w := newTestWatcher(t)
	w.stat = func(string) (ExeID, error) { return ExeID{}, nil }

	if got := w.tick(context.Background(), func() {}); got != ActNothing {
		t.Fatalf("%v 다", got)
	}
	if st := w.Status(); strings.TrimSpace(st.Stalled) == "" {
		t.Fatalf("관측 실패가 상태에 안 남았다: %+v", st)
	}
}

func TestWatcherStatusSaysWatchingFalseWhenUnsupported(t *testing.T) {
	w := newSelfWatcher(slog.New(slog.DiscardHandler), "/tmp/x.db", "")
	w.watching = false
	w.reason = "이 플랫폼은 자기 재기동을 지원하지 않는다"
	st := w.Status()
	if st.Watching {
		t.Fatal("안 보고 있는데 watching=true 다")
	}
	if strings.TrimSpace(st.Reason) == "" {
		t.Fatal("왜 안 보는지가 비었다 — 빈 상태는 '아직 갱신이 없었다'로 읽힌다")
	}
}

// ★ **감시가 구조적으로 못 덮는 갈래에 이름이 붙어야 한다.**
//
// 런처가 바이너리 이름에 소스 트리를 박고 그 경로에는 플러그인 버전이 들어가므로, 그 자리에서
// 도는 서버는 버전이 오르는 갱신을 **영영 못 본다** — Decide 가 영원히 "그대로다"를 낸다.
// 그것을 watching=true 만으로 말하면 침묵보다 나쁜 틀린 안심이다.
//
// 견주는 것은 **부모 디렉토리뿐**이다. 이름 규칙(소스 경로를 접는 키)의 주인은 런처 하나라
// 그 사본을 Go 쪽에 두지 않는다 — 그래서 이 시험도 이름은 한 번도 안 짓는다.
func TestNewSelfWatcherNamesTheBranchItCannotCover(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("실행 파일 자리를 못 읽는다: %v", err)
	}
	log := slog.New(slog.DiscardHandler)
	// 감시 자체가 안 켜지는 배치(컨테이너·비유닉스)에서는 이 축 앞에서 갈린다. 그때는
	// watching=false 와 reason 이 이미 사실을 말하므로 여기서 잴 것이 없다.
	if base := newSelfWatcher(log, "/tmp/does-not-matter.db", ""); !base.watching {
		t.Skipf("이 배치는 감시를 아예 안 켠다(%s) — Uncovered 는 그 뒤의 축이다", base.reason)
	}

	// ★ **한 자리를 두 이름으로 부른 경우.** 이 갈래가 없어서 문자열 비교가 통째로 통과했고,
	// 그동안 실물에서는 이 축이 **항상** 침묵했다 — 리눅스의 os.Executable 은 /proc/self/exe 라
	// 링크를 다 푼 경로를 주고 binDir 은 HOME 을 그대로 이어 붙인 안 푼 경로다. `~/.cache` 를
	// 큰 디스크로 옮긴 머신 · `/home -> /var/home` 배포판 · NFS 홈이 전부 그 모양이다.
	// exe.go 의 심볼릭 링크 ★ 와 **같은 축이고, 그래서 같은 함수(sameDir)가 답해야 한다.**
	sameAlias := filepath.Join(t.TempDir(), "same")
	otherDir := t.TempDir()
	otherAlias := filepath.Join(t.TempDir(), "other")
	if err := os.Symlink(filepath.Dir(exe), sameAlias); err != nil {
		t.Skipf("심볼릭 링크를 못 만든다: %v", err)
	}
	if err := os.Symlink(otherDir, otherAlias); err != nil {
		t.Skipf("심볼릭 링크를 못 만든다: %v", err)
	}

	cases := []struct {
		name   string
		binDir string
		want   bool // Uncovered 가 차는가
	}{
		{"런처 자리에서 돈다 — 버전이 오르면 아무도 이 파일을 안 덮는다", filepath.Dir(exe), true},
		{"끝 슬래시가 붙어도 같은 자리다(Clean 이 흡수한다)", filepath.Dir(exe) + "/", true},
		{"심볼릭 링크로 부른 같은 자리 — 문자열이 갈려도 inode 로 알아본다", sameAlias, true},
		{"다른 자리면 이름이 안 갈린다 — 침묵한다", otherDir, false},
		// ★ 위 갈래의 대조. inode 비교가 "링크면 무조건 같다"로 무너지면 여기가 빨간불이다.
		{"심볼릭 링크라도 가리키는 곳이 다르면 다른 자리다", otherAlias, false},
		{"자리를 계산할 수 없다(HOME 도 FD_STATE_DIR 도 없다) — 침묵한다", "", false},
		{"공백뿐인 값은 자리가 아니다", "   ", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newSelfWatcher(log, "/tmp/does-not-matter.db", c.binDir)
			st := w.Status()
			if got := strings.TrimSpace(st.Uncovered) != ""; got != c.want {
				t.Fatalf("Uncovered 가 찼는가 = %v, 원한 것 %v (binDir=%q): %q", got, c.want, c.binDir, st.Uncovered)
			}
			// ★ **감시를 끄지 않는다.** 같은 소스 트리의 재빌드는 여전히 이 자리를 덮으므로
			// watching=false 는 과보고다 — 그러면 도는 축까지 "안 본다"로 접힌다.
			if !st.Watching {
				t.Fatalf("못 덮는 갈래를 이유로 감시를 껐다: %+v", st)
			}
			// ★ **Stalled 로 새면 안 된다.** 저쪽은 회복되는 일시 고장이고 이쪽은 회복이 없다.
			if strings.TrimSpace(st.Stalled) != "" {
				t.Fatalf("못 덮는 갈래가 Stalled 로 접혔다: %+v", st)
			}
			if !c.want {
				return
			}
			// 사람이 할 일이 문구에 있어야 한다 — 사유만 있고 처방이 없으면 화면이 답을 안 준다.
			for _, want := range []string{"버전이 오르면", "재기동"} {
				if !strings.Contains(st.Uncovered, want) {
					t.Fatalf("%q 가 사유에 없다: %q", want, st.Uncovered)
				}
			}
		})
	}
}
