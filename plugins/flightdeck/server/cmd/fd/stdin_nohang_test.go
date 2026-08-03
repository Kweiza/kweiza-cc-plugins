package main

import (
	"strings"
	"testing"
	"time"
)

// blockingReader 는 **EOF 가 절대 오지 않는** stdin 이다.
//
// 이 타입이 이 시험의 전부다. 기존 시험들은 본문을 인자로 주거나 이미 닫힌 리더를 써서
// "본문이 없으면 stdin 을 읽는다"는 폴백을 **원리적으로 못 봤다** —
// 닫힌 리더는 즉시 EOF 라 폴백이 있어도 안 멈춘다.
// 실제 훅·에이전트 환경의 stdin 은 열려 있고 EOF 가 안 온다.
type blockingReader struct{ ch chan struct{} }

func (b blockingReader) Read([]byte) (int, error) { <-b.ch; return 0, nil }

// 본문 없는 쓰기 명령은 **stdin 을 안 읽고 즉시 거절**해야 한다.
//
// 앞선 판은 EOF 까지 읽었고, 스모크에서 3분 넘게 멈췄다. 훅 경로는 더 나쁘다 —
// 거기 stdin 은 훅 JSON 페이로드라, 읽으면 그것을 판단 본문으로 삼는다.
func TestWritesDoNotBlockOnStdinWhenBodyIsMissing(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
	}{
		{"note", []string{"note", "--kind", "now"}},
		{"finish", []string{"finish", "some-item", "--outcome", "done"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			blocked := blockingReader{ch: make(chan struct{})}
			defer close(blocked.ch) // 시험이 끝나면 풀어 준다(고루틴이 새면 -race 가 잡는다)

			done := make(chan string, 1)
			go func() {
				_, out := h.runWithStdin(blocked, c.args...)
				done <- out
			}()

			select {
			case out := <-done:
				// 거절 자체는 물론, **무엇을 적어야 하는지**가 그 자리에 있어야 한다.
				if !strings.Contains(out, "본문") {
					t.Errorf("본문이 없다는 사실을 안 말한다:\n%s", out)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("%s 가 stdin 을 기다리며 멈췄다 — 훅·에이전트 환경에서 세션이 통째로 멈춘다", c.name)
			}
		})
	}
}

// `--body -` 는 명시적 요청이므로 stdin 을 읽는다.
func TestExplicitDashStillReadsStdin(t *testing.T) {
	h := newHarness(t)
	_, out := h.runWithStdin(strings.NewReader("파이프로 온 본문"), "note", "--kind", "now", "--body", "-")
	if !strings.Contains(out, "저장했다") {
		t.Errorf("--body - 가 stdin 을 안 읽었다:\n%s", out)
	}
}
