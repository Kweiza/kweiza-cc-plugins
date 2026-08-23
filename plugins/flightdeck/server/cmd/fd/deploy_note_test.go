package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

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
	dbp1 := filepath.Join(t.TempDir(), "fd.db")
	// ★ 적용은 기동에서 분리돼 있다(설계 §7 ①) — 열기 전에 올린다.
	if err := store.Migrate(context.Background(), dbp1, nil); err != nil {
		t.Fatalf("DB 적용 실패: %v", err)
	}
	st, err := store.Open(dbp1)
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

	// ★ **적힌 정체가 그 관측에서 나왔는지**까지 본다. 여기까지 안 보면 noteBuild 가 상수
	// 하나를 적어도 위 단정이 전부 통과한다(첫 호출은 기준선이 없어 쓰고, 둘째는 같은
	// 문자열이라 안 쓴다). 그 상태에서는 **진짜 배포가 와도 원장이 영영 모른다** —
	// 이 축이 막으려던 것 자체다.
	evs, err := st.ListEvents(ctx, "server.deploy", time.Time{}, 10)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("배포 이벤트 %d건, 원하는 것 1", len(evs))
	}
	var got struct {
		Exe string `json:"exe"`
	}
	if err := json.Unmarshal([]byte(evs[0].Payload), &got); err != nil {
		t.Fatalf("payload 해석 실패(%q): %v", evs[0].Payload, err)
	}
	if want := buildIdentity(exeIDOfPath); got.Exe != want {
		t.Errorf("적힌 정체 %q, 관측한 정체 %q — 원장이 실행 파일이 아닌 무언가를 적고 있다",
			got.Exe, want)
	}
}

// buildIdentity 는 관측 실패를 **빈 문자열**로 번역한다 — `"관측 안 됨"` 을 그대로 흘리지 않는다.
//
// ★ 이 갈래는 실물로 못 만든다(`/proc/self/exe` 는 시험 안에서 항상 읽힌다). 그래서 stat 을
// 주입한다. 가드가 없으면 ExeID.String() 이 `"관측 안 됨"` 을 내고 store 는 그것을 정상
// 정체로 받아 배포 한 건을 적는다 — 관측이 흔들릴 때마다 가짜 배포가 쌓이고, 그 시각으로
// 자른 지표가 근거 없이 리셋된다.
func TestBuildIdentityIsEmptyWhenUnobserved(t *testing.T) {
	for _, c := range []struct {
		name string
		stat func(string) (ExeID, error)
	}{
		{"stat 이 실패한다", func(string) (ExeID, error) { return ExeID{}, errors.New("없다") }},
		{"stat 은 되는데 관측이 안 됐다", func(string) (ExeID, error) { return ExeID{OK: false, Ino: 7}, nil }},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := buildIdentity(c.stat); got != "" {
				t.Errorf("buildIdentity = %q, 원하는 것 빈 문자열 — 이 값이 원장에 정체로 적힌다", got)
			}
		})
	}

	// 관측되면 그 값을 그대로 낸다 — 빈 문자열만이 "모른다"의 표현이어야 한다.
	ok := ExeID{OK: true, Dev: 1, Ino: 2, Size: 3, MtimeNano: 4}
	if got := buildIdentity(func(string) (ExeID, error) { return ok, nil }); got != ok.String() {
		t.Errorf("buildIdentity = %q, 원하는 것 %q", got, ok.String())
	}
}
