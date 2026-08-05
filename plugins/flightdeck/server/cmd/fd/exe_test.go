package main

import (
	"errors"
	"strings"
	"testing"
)

func TestExeLinesPlainPath(t *testing.T) {
	got := ExeLines("/home/u/.local/state/flightdeck/bin/fd", nil)
	if len(got) != 1 {
		t.Fatalf("교체 안 된 자리인데 줄이 %d개다: %v", len(got), got)
	}
	if !strings.HasSuffix(got[0], "/bin/fd") {
		t.Fatalf("경로를 잘못 냈다: %q", got[0])
	}
	if strings.Contains(got[0], "deleted") {
		t.Fatalf("표식이 새어 나왔다: %q", got[0])
	}
}

// ★ 이 갈래가 이 함수의 존재 이유다 — 실제 프로세스로는 못 만든다.
func TestExeLinesReplacedBinaryIsAnnounced(t *testing.T) {
	got := ExeLines("/home/u/.local/state/flightdeck/bin/fd (deleted)", nil)
	if len(got) != 2 {
		t.Fatalf("교체된 자리인데 줄이 %d개다 — 사실만 찍고 뜻을 안 낸 것이다: %v", len(got), got)
	}
	if strings.Contains(got[0], "deleted") {
		t.Fatalf("경로 줄에 커널 표식이 남았다: %q", got[0])
	}
	if !strings.HasSuffix(got[0], "/bin/fd") {
		t.Fatalf("경로에서 표식만 떼야 한다: %q", got[0])
	}
	// 사람이 다음에 할 일이 그 줄에 있어야 한다. 없으면 빌드를 다시 하러 간다.
	if !strings.Contains(got[1], "재기동") {
		t.Fatalf("무엇을 해야 하는지가 없다: %q", got[1])
	}
}

func TestExeLinesErrorIsNotSilent(t *testing.T) {
	got := ExeLines("", errors.New("readlink /proc/self/exe: permission denied"))
	if len(got) != 1 || !strings.Contains(got[0], "permission denied") {
		t.Fatalf("오류를 삼켰다: %v", got)
	}
}

// 경로 안에 우연히 같은 글자가 있어도 **끝에 붙은 것만** 표식이다.
func TestExeLinesOnlyTrimsTheSuffix(t *testing.T) {
	got := ExeLines("/opt/my (deleted) dir/fd", nil)
	if len(got) != 1 {
		t.Fatalf("경로 중간의 문자열을 표식으로 읽었다: %v", got)
	}
}
