package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
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

func newTestWatcher(t *testing.T) *selfWatcher {
	t.Helper()
	w := newSelfWatcher(slog.New(slog.DiscardHandler), "/tmp/does-not-matter.db")
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
			t.Fatal("드레인 전에 exec 했다 — 인플라이트 요청이 통째로 끊긴다")
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

func TestWatcherStatusSaysWatchingFalseWhenUnsupported(t *testing.T) {
	w := newSelfWatcher(slog.New(slog.DiscardHandler), "/tmp/x.db")
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
