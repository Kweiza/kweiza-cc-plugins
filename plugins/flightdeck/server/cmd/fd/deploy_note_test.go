package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kweiza/flightdeck/internal/store"
)

// noteBuild 는 **실물 실행 파일**을 관측해 원장에 적고, 같은 이미지로 다시 떠도 안 늘린다.
//
// ★ store 시험(TestNoteServerBuildRecordsOnlyWhenBinaryChanged)은 정체 문자열을 주입해
// 셈만 잠근다. 여기서 잠그는 것은 그 위 한 겹 — **관측이 실제로 되는가**다. `/proc/self/exe`
// 를 못 읽거나 ExeID.OK 가 거짓이면 이 함수는 아무 말도 안 해야 하고, 읽히면 정확히 한 번
// 적어야 한다. 시험 바이너리도 실행 파일이므로 이 경로가 그대로 돈다.
func TestNoteBuildObservesRealBinaryOnce(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "fd.db"))
	if err != nil {
		t.Fatalf("DB 를 못 열었다: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	log := quietLogger()

	count := func() int {
		t.Helper()
		at, ok, err := st.LastDeployAt(ctx)
		if err != nil {
			t.Fatalf("LastDeployAt: %v", err)
		}
		if ok && at.IsZero() {
			t.Fatal("배포가 있다는데 시각이 영값이다")
		}
		if !ok {
			return 0
		}
		return 1
	}

	if n := count(); n != 0 {
		t.Fatalf("빈 원장인데 배포 기록이 있다")
	}

	noteBuild(ctx, st, log)
	if _, err := exeIDOfPath("/proc/self/exe"); err != nil {
		t.Skipf("이 플랫폼에서 실행 파일 정체를 못 읽는다(%v) — 관측 축이 없으므로 건너뛴다", err)
	}
	if n := count(); n != 1 {
		t.Fatalf("실행 파일을 관측했는데 배포가 안 적혔다")
	}
	first, _, _ := st.LastDeployAt(ctx)

	// 같은 이미지로 다시 뜬다 — 재기동이지 배포가 아니다.
	noteBuild(ctx, st, log)
	second, ok, err := st.LastDeployAt(ctx)
	if err != nil || !ok {
		t.Fatalf("재기동 뒤 LastDeployAt(ok=%v err=%v)", ok, err)
	}
	if !second.Equal(first) {
		t.Errorf("같은 실행 파일로 다시 떴는데 배포 시각이 움직였다: %v → %v — "+
			"이러면 '마지막 배포'가 '마지막 기동'이 되어 이 축이 뜻을 잃는다", first, second)
	}
}
