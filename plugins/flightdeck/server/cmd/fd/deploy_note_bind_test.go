package main

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/store"
)

// TestServeSkipsDeployNoteWhenBindFails 는 **순서**를 붙든다 — 뜨지도 못한 기동은
// 원장에 배포를 안 남긴다.
//
// ★ runServe 를 실물로 부른다. 이 성질은 어느 순수 함수에도 안 살고 오직 runServe 의
// 호출 순서에만 살기 때문이다 — 조각을 따로 부르는 시험은 그 순서를 통째로 못 본다.
// 바인드 실패 갈래는 즉시 반환하므로(감시기도 백업 잡도 안 뜬다) 실물로 돌릴 수 있다.
//
// ★ 재현은 실제로 났던 것이다: 컨테이너가 :7420 을 물고 도는데 사람이 README 의
// `go run ./cmd/fd serve` 를 치면, compose 가 ~/.flightdeck:/data 를 마운트하므로
// 그 임시 바이너리가 **컨테이너와 같은 원장**에 배포를 적고 죽는다.
func TestServeSkipsDeployNoteWhenBindFails(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("자리를 못 잡았다: %v", err)
	}
	t.Cleanup(func() { busy.Close() })

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "fd.db")
	// FD_LEDGER 를 시험 자리로 돌린다 — 사람 홈에 백업 디렉토리를 안 만든다.
	env := func(k string) (string, bool) {
		if k == "FD_LEDGER" {
			return filepath.Join(dir, "ledger"), true
		}
		return "", false
	}

	got := runServe([]string{"--addr", busy.Addr().String(), "--db", dbPath}, env, quietLogger())
	if got != 1 {
		t.Fatalf("이미 물린 포트인데 runServe 가 %d 를 냈다 — 바인드 실패가 종료코드에 안 나온다", got)
	}

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("DB 를 못 열었다: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	evs, err := st.ListEvents(context.Background(), "server.deploy", time.Time{}, 10)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(evs) != 0 {
		t.Fatalf("뜨지도 못한 기동이 배포를 %d건 남겼다 — LastDeployAt 이 한 번도 응답한 적 "+
			"없는 바이너리의 시각을 낸다", len(evs))
	}
}
