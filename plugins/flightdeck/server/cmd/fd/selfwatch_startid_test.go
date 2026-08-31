package main

import (
	"errors"
	"testing"
)

// 기준값 결정의 관문.
//
// ★ 이 파일이 무는 것은 **맥에서만 터지던 패닉**이다. `/proc/self/exe` 는 리눅스에만 있어
// 맥은 언제나 두 번째 갈래인데, 앞선 판이 그 갈래를 `else if id, err := …` 로 써서 `err` 을
// 섀도잉했다. 블록의 조건이 `err == nil` 이므로 안에서 부른 `err.Error()` 는 항상 nil
// 역참조였다 — `newSelfWatcher` 가 통째로 패닉했고 `fd serve` 와 selfwatch 시험 13개가
// 함께 죽었다. 리눅스는 첫 갈래로 가므로 **그쪽에서는 영영 안 보였다.**
//
// 그래서 이 시험은 **stat 을 주입해** 두 갈래를 다 돌린다. 플랫폼에 기대면 리눅스 CI 가
// 두 번째 갈래를 영원히 안 밟고, 그것이 이 결함을 숨긴 바로 그 구조다.

func TestResolveStartIDUsesProcSelfExeWhenItReads(t *testing.T) {
	want := id(10, 1000)
	var asked []string
	stat := func(p string) (ExeID, error) {
		asked = append(asked, p)
		return want, nil
	}
	got, procErr, err := resolveStartID(stat, "/some/fd")
	if err != nil {
		t.Fatalf("err=%v — 성공해야 한다", err)
	}
	if procErr != nil {
		t.Fatalf("procErr=%v — 첫 갈래로 갔으면 경고 사유가 없어야 한다", procErr)
	}
	if got != want {
		t.Fatalf("기준값이 %+v 다, %+v 를 기대했다", got, want)
	}
	// 첫 갈래가 성공하면 경로는 **안 재야** 한다 — 그 자리가 지금 도는 이미지라서다.
	if len(asked) != 1 || asked[0] != "/proc/self/exe" {
		t.Fatalf("stat 호출이 %v 다 — /proc/self/exe 하나여야 한다", asked)
	}
}

// TestResolveStartIDFallsBackToPathAndKeepsTheReason 이 **회귀를 막는 줄**이다.
//
// 맥이 언제나 타는 갈래이고, 여기서 `procErr` 이 nil 이면 호출부의 `procErr.Error()` 가
// 그대로 패닉이다.
func TestResolveStartIDFallsBackToPathAndKeepsTheReason(t *testing.T) {
	want := id(20, 2000)
	boom := errors.New("no such file or directory")
	stat := func(p string) (ExeID, error) {
		if p == "/proc/self/exe" {
			return ExeID{}, boom
		}
		return want, nil
	}
	got, procErr, err := resolveStartID(stat, "/some/fd")
	if err != nil {
		t.Fatalf("err=%v — 경로로 잴 수 있으면 성공이다", err)
	}
	if got != want {
		t.Fatalf("기준값이 %+v 다, %+v 를 기대했다", got, want)
	}
	// ★ 핵심. 값이 있어야 하고, 그 값을 **호출부가 .Error() 로 부를 수 있어야** 한다.
	if procErr == nil {
		t.Fatal("procErr 이 nil 이다 — 호출부의 procErr.Error() 가 nil 역참조로 패닉한다. " +
			"이것이 맥에서 fd serve 와 selfwatch 시험 13개를 죽였던 그 결함이다")
	}
	if !errors.Is(procErr, boom) {
		t.Fatalf("procErr 이 %v 다 — /proc/self/exe 를 못 읽은 사유여야 한다", procErr)
	}
	_ = procErr.Error() // 패닉하지 않는다는 것 자체가 단정이다
}

func TestResolveStartIDFailsWhenNeitherReads(t *testing.T) {
	procBoom := errors.New("proc 없음")
	pathBoom := errors.New("경로도 없음")
	stat := func(p string) (ExeID, error) {
		if p == "/proc/self/exe" {
			return ExeID{}, procBoom
		}
		return ExeID{}, pathBoom
	}
	_, procErr, err := resolveStartID(stat, "/some/fd")
	if err == nil {
		t.Fatal("둘 다 못 쟀는데 err 이 nil 이다")
	}
	if !errors.Is(err, pathBoom) {
		t.Fatalf("err 이 %v 다 — 경로 실패 사유여야 한다", err)
	}
	// 실패 갈래에서도 첫 사유를 잃지 않는다 — 둘은 다른 사실이다.
	if !errors.Is(procErr, procBoom) {
		t.Fatalf("procErr 이 %v 다 — /proc/self/exe 실패 사유가 살아 있어야 한다", procErr)
	}
}
