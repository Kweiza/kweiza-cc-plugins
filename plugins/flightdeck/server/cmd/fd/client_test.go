package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ReadFresh 는 성공을 캐시에 안 넣고, 서버가 죽으면 캐시에서 안 꺼내고 그대로 오류다.
// (Client.Read 의 무조건 캐시와 정반대 — Healthz 와 같은 판정이다.)
func TestReadFreshNeverTouchesTheCache(t *testing.T) {
	const path = "/lane/wait"
	// 본문은 반드시 유효한 JSON 이어야 한다 — Cache.Put 이 응답을 json.RawMessage 로
	// 담기 때문에(cache.go), 순수 텍스트를 쓰면 Put 자체가 조용히 실패해(Warn 로그만 남고
	// 삼켜지지 않지만 quietLogger 가 Error 미만을 버린다) "ReadFresh 가 안 넣어서"인지
	// "애초에 못 넣어서"인지 이 시험이 못 가른다.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"live"}`))
	}))

	cli := &Client{
		URL:   srv.URL,
		HTTP:  &http.Client{Timeout: time.Second},
		Cache: newCache(StateDir{Path: t.TempDir()}),
		Log:   quietLogger(),
		Now:   func() time.Time { return time.Now().UTC() },
	}
	ctx := context.Background()

	// 살아 있는 동안 ReadFresh 를 먼저 불러 성공을 확인하고, 캐시가 비어 있는지 본다.
	body, err := cli.ReadFresh(ctx, path)
	if err != nil {
		t.Fatalf("살아 있는 서버에 대한 ReadFresh 가 실패했다: %v", err)
	}
	if !strings.Contains(string(body), "live") {
		t.Fatalf("ReadFresh 본문이 %q 다", body)
	}
	if _, cerr := cli.Cache.Get(path); cerr == nil {
		t.Fatal("ReadFresh 가 성공 응답을 캐시에 넣었다 — 계약 위반이다")
	}

	// 이제 Read 를 한 번 불러 캐시를 채운다(대조의 전제 — Read 만의 몫이다. ReadFresh 는
	// 방금 같은 경로를 두 번 불렀는데도 캐시를 안 채웠다).
	if _, err := cli.Read(ctx, path); err != nil {
		t.Fatalf("대조 전제가 깨졌다 — 살아 있는데 Read 가 실패했다: %v", err)
	}

	srv.Close()

	// Close 후: ReadFresh 는 캐시가 있어도(방금 Read 가 채웠다) 안 본다 — 그대로 오류다.
	if _, err := cli.ReadFresh(ctx, path); err == nil {
		t.Fatal("서버가 죽었는데 ReadFresh 가 오류를 안 냈다 — 캐시를 재생했다는 뜻이다")
	}

	// 대조: 같은 경로를 Read 로 부르면 캐시가 재생된다. 두 함수의 차이를 여기서 못박는다.
	res, err := cli.Read(ctx, path)
	if err != nil {
		t.Fatalf("Read 는 캐시가 있으면 미도달에도 오류를 내면 안 된다: %v", err)
	}
	if res.Fresh {
		t.Fatal("서버가 죽었는데 Read 가 Fresh 를 켰다 — 대조 전제가 깨졌다")
	}
	if !strings.Contains(string(res.Body), "live") {
		t.Fatalf("Read 캐시 재생 본문이 %q 다", res.Body)
	}
}
