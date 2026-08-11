package api

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"testing"
)

// TestListenReportsBindFailure 는 바인드 성공/실패가 **값**임을 붙든다.
//
// ★ 이 시험이 가능해진 것 자체가 이 변경의 요지다. 앞선 판은 net.Listen 이 Serve 안에
// 묻혀 있어 "리스너가 열렸나"를 밖에서 물을 수 없었고, 그래서 배포 관측이 그 사실에
// 매달릴 수 없었다(cmd/fd 의 noteBuild).
func TestListenReportsBindFailure(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("자리를 못 잡았다: %v", err)
	}
	t.Cleanup(func() { busy.Close() })

	log := slog.New(slog.DiscardHandler)

	ln, err := Listen(context.Background(), busy.Addr().String(), log)
	if err == nil {
		ln.Close()
		t.Fatal("이미 물린 포트인데 Listen 이 성공했다 — 바인드 실패가 값으로 안 나온다")
	}
	if !strings.Contains(err.Error(), busy.Addr().String()) {
		t.Errorf("오류에 주소가 없다: %v — 호출부가 처방을 붙일 근거를 잃는다", err)
	}

	// 성공 갈래는 리스너를 실제로 준다.
	ok, err := Listen(context.Background(), "127.0.0.1:0", log)
	if err != nil {
		t.Fatalf("빈 포트인데 Listen 이 실패했다: %v", err)
	}
	t.Cleanup(func() { ok.Close() })
	if ok.Addr() == nil || ok.Addr().String() == "" {
		t.Error("리스너에 주소가 없다 — 호출부가 :0 의 실제 포트를 못 읽는다")
	}
}
