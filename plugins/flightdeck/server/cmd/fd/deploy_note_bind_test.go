package main

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
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

	// ★ quietLogger() 대신 로그를 붙잡는다. 종료코드 1 과 0행은 바인드 실패에 고유하지
	// 않다 — 최종 리뷰가 runServe 초입(`home, _ := os.UserHomeDir()` 앞)에 바인드와 무관한
	// `return 1` 을 실측으로 넣어 확인했다: 종료코드 1 은 DB 디렉토리·DB 열기 등 어느 조기
	// 반환에서도 나오고, 0행 단정도 시험이 나중에 여는 store.Open(dbPath) 가 DB 를 새로
	// 만들기 때문에 runServe 가 그 DB 를 아예 안 열었어도 만족된다. 그래서 바인드 고유
	// 증거를 하나 더 단정한다 — serve.go 의 Listen 실패 갈래에서만 남기는
	// "서버를 띄우지 못했다" 로그 레코드다. 그 갈래는 api.Serve 도 ledgerJob.Run 도 안 뜨는
	// 단일 고루틴이라 버퍼 동기화가 필요 없다.
	// ★ 적용은 기동에서 분리돼 있다(설계 §7 ①) — runServe 가 열기 전에 올린다.
	if err := store.Migrate(context.Background(), dbPath, nil); err != nil {
		t.Fatalf("DB 적용 실패: %v", err)
	}
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))

	got := runServe([]string{"--addr", busy.Addr().String(), "--db", dbPath}, env, log)
	if got != 1 {
		t.Fatalf("이미 물린 포트인데 runServe 가 %d 를 냈다 — 바인드 실패가 종료코드에 안 나온다", got)
	}

	if !strings.Contains(logs.String(), "서버를 띄우지 못했다") {
		t.Fatalf("바인드 실패 로그가 안 남았다 — 종료코드 1 과 0행만으로는 DB 실패 등 바인드와 "+
			"무관한 조기 반환도 이 시험을 통과시킨다. 로그: %s", logs.String())
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
