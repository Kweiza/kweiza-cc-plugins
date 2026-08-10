package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/buildinfo"
)

// REST 클라이언트 — **모든 서브명령이 이것만 쓴다.**
//
// 클라이언트가 서비스 계층을 직접 부르면 다른 머신에서 같은 바이너리가 못 돈다.
// 그래서 여기가 유일한 통로이고, 열화(L1)도 전부 이 한 자리에서 일어난다.

// DefaultURL 은 서버 주소의 기본값이다.
const DefaultURL = "http://127.0.0.1:7420"

// APIError 는 서버가 상태코드로 거절한 것이다. **미도달과 다른 축이다.**
//
// 서버의 오류 본문(code·message·guidance·request_id)을 해석해 나른다 —
// request_id 가 없으면 사용자 신고와 서버 로그를 이을 열쇠가 사라지고,
// guidance 가 없으면 호출자가 무엇을 고쳐야 하는지 모른 채 같은 호출을 반복한다.
type APIError struct {
	Status    int
	Path      string
	Code      string
	Message   string
	Guidance  string
	RequestID string
	Raw       string
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = clip(e.Raw, 400)
	}
	s := fmt.Sprintf("서버가 %d 로 거절했다: %s", e.Status, msg)
	if e.Guidance != "" {
		s += "\n" + e.Guidance
	}
	if e.RequestID != "" {
		s += fmt.Sprintf("\n(request_id=%s · 서버 로그의 같은 값이 원인 전문이다)", clip(e.RequestID, 64))
	}
	return s
}

// parseAPIError 는 오류 본문을 해석한다. 해석에 실패해도 **원문을 버리지 않는다** —
// 형식이 바뀐 날 응답이 통째로 사라지면 원인을 볼 자리가 없다.
func parseAPIError(status int, path string, raw []byte) *APIError {
	e := &APIError{Status: status, Path: path, Raw: string(raw)}
	var body errBody
	if err := json.Unmarshal(raw, &body); err == nil {
		e.Code, e.Message = body.Error.Code, body.Error.Message
		e.Guidance, e.RequestID = body.Error.Guidance, body.Error.RequestID
	}
	return e
}

// Client 는 서버 하나에 붙는 클라이언트다.
//
// ★ **축이 둘이고, 둘은 성질이 다르다. 뭉개면 틀린 처방이 나온다.**
//
// 이 주석은 오래 "호출자가 전부 순차라 안전하다"고 적어 뒀는데, 그 문장은
// **한 프로세스 안에서만** 참이었다. 파일로 공유되는 상태는 프로세스를 가로질러 다툰다.
//
// ── 프로세스 **내부** 축 — 오늘 안전하다
//
//	Session(아래 필드). mcpBackend 가 호출마다 갈아 쓰지만, `fd mcp` 는 한 프로세스가
//	한 세션이라 매번 같은 값이 들어간다. mcpsrv.Serve 의 프레임 루프도 순차다.
//	그 순차성을 TestServeNeverOverlapsBackend 가 붙들고 있다 — 프레임 루프를 병렬로
//	바꾸는 커밋은 거기서 빨강을 보고, 그때 이 필드를 인자로 내려야 한다.
//
// ── 프로세스 **간** 축 — 대부분 잠금으로 닫았다(outbox_lock.go). 남은 자리는 아래에 적는다
//
//	아웃박스 자리는 채널과 무관하게 머신당 하나로 일부러 고정돼 있다(설계 §7).
//	그래서 세션마다 뜨는 `fd mcp`, 세션 시작마다 뜨는 훅, 사람이 치는 셸 명령이
//	**같은 파일**을 다툰다. 재현으로 확인된 피해 셋:
//
//	 1. Outbox.Append 의 List→검사→쓰기 (outbox.go)
//	 2. Outbox.Replay 의 List→send→되쓰기 — **가장 무겁다.** 스냅숏을 되쓰면 그 사이
//	    들어온 판단이 격리에도 안 남고 삭제된다(실측 33/300). 이 항목이 처음 지목한
//	    셋에는 이것이 없었다.
//	 3. Cache.Put 의 tmp 경로 (cache.go)
//
//	★ **프로세스 내 sync.Mutex 로는 이 셋을 하나도 못 막는다.** 다른 fd 프로세스는
//	그 뮤텍스가 있는 줄도 모른다. 그리고 **잠금만 넣고 되쓰기를 병합으로 안 바꾸면
//	오히려 나빠진다**(실측: 유실 50/300 → 279/300). 잠금이 순서를 결정적으로 만들어
//	낡은 스냅숏이 거의 항상 이기기 때문이다.
//
//	★ **닫히지 않은 자리 둘을 명시한다** — "프로세스 간 축을 닫았다"로 읽히면 안 된다.
//	 · quarantine 의 rejected.jsonl O_APPEND 는 잠금 밖이다. 겹치면 같은 줄이 두 번
//	   들어간다(실측 199/200 — 다만 main 도 198/200 이라 이 커밋의 회귀는 아니다).
//	   추가 전용이라 **유실이 아니라 중복**이고, 피해는 doctor 집계가 부푸는 정도다.
//	 · 잠금을 예산 안에 못 잡으면 무잠금 병합으로 떨어진다(fail-open). 큐가 깊고 세션이
//	   몰리면 실제로 일어난다 — 수치는 outbox_lock.go 의 queueLockBudget 주석에 있다.
//
// ★ -race 는 이 축을 **원리적으로 못 본다.** 이유가 옛 주석과 다르다 — "동시 진입이
// 없어서"가 아니라, 동시 진입이 **있는데도** 공유 상태가 Go 메모리가 아니라 파일이라서다.
// 다섯 축을 재현하는 내내 DATA RACE 는 0건이었다. 이 축의 관문은 -race 가 아니라
// **큐 내용에 대한 바이트 단정**이다(outbox_concurrency_test.go).
type Client struct {
	URL    string
	Token  string
	HTTP   *http.Client
	Cache  *Cache
	Outbox *Outbox
	// Legacy 는 아웃박스가 채널마다 갈려 있던 시절의 큐다. **옮기지 않는다** —
	// 재생이 제자리에서 돌려 전송으로 비우고, 마지막 줄까지 나가면 keep() 이 파일을 지운다.
	// (os.Rename 청구로 고정 자리에 흡수하려던 앞선 설계는 반증됐다 — 스펙 §4.)
	Legacy []*Outbox
	Log    *slog.Logger
	Now    func() time.Time

	// Endpoint 는 URL·Token 을 **어디서 읽었는지**다. 값은 위 두 필드가 들고 있고
	// 여기 있는 것은 그 출처와 경고다 — 값이 예상과 다를 때 "왜 저 주소인가"에 답할 자리다.
	Endpoint Endpoint

	// Session 은 멱등 키의 앞 절반이다. 없을 수 있다(세션 열기 전).
	//
	// ★ **공유 가변 상태다.** 실측(이 커밋 시점): 쓰는 자리 17(app.go 2 · cmds.go 6 ·
	// hook.go 3 · mcpbackend.go 6), 읽는 자리 5(client.go 의 KeyFor 둘 · app.go 셋).
	// 그중 mcpbackend.go 의 여섯(Pick 둘 · Note · AddItem · Finish · land)은 **호출마다**
	// 갈아 쓴다. 옛 주석은 "읽는 자리는 KeyFor 하나뿐 · mcpbackend 의 넷"이라 적었는데
	// 둘 다 낡았었다 — 세는 문장은 늘 낡으므로 고칠 때 함께 다시 세라.
	// 쓰기와 읽기 사이에 잠금이 없으므로 겹치면 그 자리에서 깨진다 —
	// 문자열은 (ptr,len) 둘이라 찢긴 값이 나올 수 있고, 그 값이 곧 멱등 키다.
	//
	// `fd mcp` 안에서는 한 프로세스가 한 세션이라(mcpsrv 의 ensureSession 이 한 번만 연다)
	// 갈아 쓰는 값이 매번 **같다** — 세션이 서로 섞이는 사고는 원리적으로 안 난다.
	// 그것이 이 자리를 지금 고치지 않기로 한 이유다. 다만 찢김은 그와 별개로 남는다.
	Session string
}

// newClient 는 클라이언트를 조립한다.
//
// ★ 주소·토큰의 우선순위는 **한 자리에서** 정한다(ResolveEndpoint). 여기서 다시 풀면
// CLI·훅·MCP 가 각자 다른 규칙을 갖게 되고, 이 레포는 "같은 판정을 두 자리에 두면
// 한쪽만 고칠 때 조용히 어긋난다"를 세 번 겪었다.
func newClient(sd StateDir, get func(string) (string, bool), home string, log *slog.Logger) *Client {
	ep := ResolveEndpoint(get, home)
	url, token := ep.URL, ep.Token
	timeout := 5 * time.Second
	if v, ok := get("FD_TIMEOUT"); ok {
		if d, err := time.ParseDuration(strings.TrimSpace(v)); err == nil && d > 0 {
			timeout = d
		}
	}
	// ★ 로거를 큐에 물린다. 큐 잠금을 못 잡아 무잠금으로 떨어지는 갈래가 있는데,
	// 그것이 조용하면 "잠금을 넣었다"가 거짓 안심이 된다 — 무잠금 갈래는 오늘과 같은
	// 동작이라 나빠지진 않지만, 얼마나 자주 일어나는지는 보여야 한다.
	ob := newOutbox(get, home).withLogger(log)
	var legacy []*Outbox
	for _, d := range LegacyOutboxDirs(get, home, ob.Dir()) {
		legacy = append(legacy, newOutboxAt(d).withLogger(log))
	}
	return &Client{
		Endpoint: ep, // 출처를 들고 있는다 — fd doctor·fd setup 이 '왜 저 주소인가'를 찍는다
		URL:      url,
		Token:    token,
		HTTP:     &http.Client{Timeout: timeout},
		Cache:    newCache(sd),
		Outbox:   ob,
		Legacy:   legacy,
		Log:      log,
		Now:      func() time.Time { return time.Now().UTC() },
	}
}

// do 는 요청 하나를 보낸다. 미도달은 ErrUnreachable 로, 상태코드 거절은 APIError 로 가른다.
// do 는 요청 하나를 보낸다. 세 번째 반환값은 **서버가 이 요청을 재생으로 접었는가**다.
//
// ★ 이 값을 버리면 안 된다. 쓰기 중 일부는 **내용 해시**로 멱등 키를 만들므로
// (같은 세션이 같은 본문을 두 번 쓰면 같은 키가 된다) 서버가 첫 응답을 그대로 돌려준다.
// 그 사실을 안 나르면 도구가 "저장했다"고 말하는데 원장에는 아무것도 안 늘어난다 —
// 판단은 파생 불가한 유일한 자산이고 추가 전용이라, 삼켜진 노트는 영영 안 보인다.
// 그리고 판별할 방법이 하나도 없다: 응답 문구도 판단 id 도 첫 호출과 글자 그대로 같다.
func (c *Client) do(ctx context.Context, method, path string, body any, idem string) ([]byte, bool, error) {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, false, fmt.Errorf("요청 직렬화 실패: %w", err)
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, rdr)
	if err != nil {
		return nil, false, fmt.Errorf("요청 생성 실패: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if idem != "" {
		req.Header.Set("Idempotency-Key", idem)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %s: %v", ErrUnreachable, clip(c.URL+path, 200), err)
	}
	defer resp.Body.Close()
	raw, rerr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if rerr != nil {
		return nil, false, fmt.Errorf("%w: 응답 본문 읽기 실패: %v", ErrUnreachable, rerr)
	}
	if resp.StatusCode >= 400 {
		if Unreachable(nil, resp.StatusCode) {
			return nil, false, fmt.Errorf("%w: 상태 %d: %s", ErrUnreachable, resp.StatusCode, clip(string(raw), 300))
		}
		return nil, false, parseAPIError(resp.StatusCode, path, raw)
	}
	return raw, resp.Header.Get("Idempotency-Replayed") == "true", nil
}

// Read 는 읽기 요청이다. 성공하면 캐시하고, **미도달이면 캐시를 배너와 함께** 낸다.
//
// fresh=false 인 응답에는 반드시 Banner 가 붙는다 — 침묵하면 낡은 값이 현재 사실인 척한다.
//
// ★ **이 함수는 JudgeOffline 을 한 번도 안 본다.** 성공한 GET 을 조건 없이 캐시하고
// 미도달이면 조건 없이 꺼낸다. 그 무조건성이 이 함수의 계약이고, 그래서
// **어떤 표면을 GET 으로 만들 것인가가 곧 열화 정책의 결정**이다.
//
// ★ 그래서 land 는 세 갈래가 **전부 POST 다**(Write 를 탄다). "지금 내 차례인가"를
// GET 으로 만들면 서버가 죽은 뒤 30분 전의 "네 차례다"가 그대로 나오고, 세션은 레인을
// 안 쥔 채 랜딩을 시작한다. 배타가 깨지는 것이 아니라 **우회된다** — 서버는 내내 옳고
// 아무 로그도 안 남는다. Healthz 가 캐시를 안 타고 c.do 직행인 것과 **같은 판정이다**:
// "서버가 살아 있나"에 캐시로 답하면 그 질문 자체가 무의미해지듯,
// "지금 내 차례인가"에 캐시로 답하면 그 질문 자체가 무의미해진다.
// 레인을 GET 으로 **읽는** 표면은 보드 절이다 — 그것은 "누가 쥐었나"를 보여 주는 화면이지
// "내가 쥐었나"에 답하는 취득 경로가 아니다. 둘을 한 표면으로 합치면 이 구분이 사라진다.
type ReadResult struct {
	Body   []byte
	Fresh  bool
	At     time.Time // 이 값이 관측된 시각(신선하면 지금, 아니면 캐시된 시각)
	Banner string    // 낡았을 때만 채운다
}

func (c *Client) Read(ctx context.Context, path string) (ReadResult, error) {
	raw, _, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err == nil {
		now := c.Now()
		if cerr := c.Cache.Put(path, raw, now); cerr != nil {
			// 캐시 실패는 이 요청의 실패가 아니다. 다만 삼키지 않는다 —
			// 조용히 버리면 서버가 죽은 날 캐시가 왜 비었는지 아무도 모른다.
			c.Log.Warn("캐시 보관 실패", "route", clip(path, 120), "error", cerr.Error())
		}
		return ReadResult{Body: raw, Fresh: true, At: now}, nil
	}
	if !Unreachable(err, 0) {
		return ReadResult{}, err
	}
	now := c.Now()
	ent, cerr := c.Cache.Get(path)
	if cerr != nil {
		c.Log.Error("미도달이고 캐시도 없다",
			"route", clip(path, 120), "error", err.Error(), "reason", cerr.Error())
		return ReadResult{Banner: StaleBanner(now, c.Cache.LastContact(), c.URL)},
			fmt.Errorf("%w · 캐시도 없다: %v", err, cerr)
	}
	return ReadResult{
		Body:   ent.Body,
		Fresh:  false,
		At:     ent.At,
		Banner: StaleBanner(now, ent.At, c.URL),
	}, nil
}

// Write 는 쓰기 요청이다. 미도달일 때의 처방은 **JudgeOffline 이 정한다** —
// 이 함수가 직접 판정하면 그 표가 본문에 흩어지고 시험이 사본을 단정하게 된다.
type WriteResult struct {
	Body []byte
	Sent bool
	// Replayed 는 서버가 이 요청을 **재생으로 접었는가**다.
	// 참이면 **새로 만들어진 것이 없다** — 소비자는 반드시 문구를 갈라야 한다.
	// 쓰기 중 일부는 내용 해시로 멱등 키를 만들므로(같은 세션이 같은 본문을 두 번 쓰면 같은 키)
	// 이 축을 삼키면 도구가 "저장했다"고 말하는데 원장은 그대로다.
	Replayed bool
	Mode     OfflineMode
	Reason   string // Sent=false 일 때 무엇을 했고 왜인지. 항상 채운다
}

func (c *Client) Write(ctx context.Context, cmd, path string, body any) (WriteResult, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return WriteResult{}, fmt.Errorf("요청 직렬화 실패: %w", err)
	}
	key := c.KeyFor(cmd, path, buf)

	raw, replayed, err := c.do(ctx, http.MethodPost, path, body, key)
	if err == nil {
		if replayed {
			// 같은 키가 이미 있었다 = 이 호출은 아무것도 새로 만들지 않았다.
			// 문구를 가르는 것이 전부다 — 삼키면 "저장했다"가 거짓이 된다.
			return WriteResult{Body: raw, Sent: true, Replayed: true, Mode: "",
				Reason: "서버에 같은 요청이 이미 있었다 — 첫 응답을 그대로 돌려준다(새로 만든 것은 없다)"}, nil
		}
		return WriteResult{Body: raw, Sent: true, Mode: "", Reason: "서버가 받았다"}, nil
	}
	if !Unreachable(err, 0) {
		return WriteResult{}, err
	}

	v := JudgeOffline(cmd)
	res := WriteResult{Mode: v.Mode, Reason: v.Reason}
	switch v.Mode {
	case OfflineOutbox:
		// ★ 둘째 방어. JudgeOffline 이 "쌓아라"라고 해도 **적격 집합 밖이면 안 쌓는다.**
		//   두 정책이 어긋난 것 자체가 사고이므로 조용히 한쪽을 따르지 않는다 —
		//   따르는 쪽이 아웃박스면 재연결 때 남의 레인을 뺏는 요청이 재생된다.
		if ok, why := OutboxEligible(cmd, path); !ok {
			c.Log.Error("아웃박스 적격이 아니다 — 쌓지 않는다",
				"mode", clip(cmd, 40), "route", clip(path, 120), "reason", why)
			res.Mode, res.Reason = OfflineRefuse,
				"열화 표는 아웃박스라 했지만 적격 집합이 아니다("+why+") — "+
					"두 정책이 어긋났으므로 아무것도 쌓지 않는다"
			return res, fmt.Errorf("%w · %s: %s", ErrUnreachable, cmd, res.Reason)
		}
		e := OutboxEntry{Key: key, At: c.Now(), Path: path, Body: json.RawMessage(buf)}
		if oerr := c.Outbox.Append(e); oerr != nil {
			c.Log.Error("아웃박스 적재 실패 — 이 판단은 사라진다",
				"route", clip(path, 120), "error", oerr.Error())
			return res, fmt.Errorf("아웃박스에도 못 쌓았다: %w", oerr)
		}
		c.Log.Info("아웃박스에 쌓았다", "route", clip(path, 120), "count", 1)
		return res, nil
	case OfflineDrop:
		c.Log.Info("미도달 — 버렸다", "route", clip(path, 120), "reason", v.Reason)
		return res, nil
	default: // OfflineRefuse · OfflineCache(쓰기에는 캐시 처방이 없다)
		return res, fmt.Errorf("%w · %s: %s", ErrUnreachable, cmd, v.Reason)
	}
}

// KeyFor 는 이 쓰기의 Idempotency-Key 다.
//
// 정책은 IdempotencyStable 이 정한다 — 본문에서 판정하면 시험이 그 표의 사본을
// 단정하게 되고, 그러면 alloc 이 고정되는 회귀가 조용히 샌다.
func (c *Client) KeyFor(cmd, path string, body []byte) string {
	stable, reason := IdempotencyStable(cmd)
	if !stable {
		c.Log.Debug("멱등 키를 새로 만든다", "mode", clip(cmd, 40), "reason", reason)
		return FreshKey(c.Session)
	}
	return IdempotencyKey(c.Session, append([]byte(path+"\n"), body...))
}

// Flush 는 쌓인 판단을 재생한다. **모든 명령의 앞에서 불린다** —
// 재연결을 감지하는 별도 기구를 만들지 않는다(감지 기구는 자기가 안 돌 때 조용하다).
//
// ★ 고정 큐를 돌고 **옛 채널 자리 큐도 함께 돈다.** 큐마다 독립이라 한쪽이 막혀도
// 다른 쪽은 나간다 — 한 큐의 정체가 다른 큐를 인질로 잡지 않는다.
func (c *Client) Flush(ctx context.Context) ReplayResult {
	return c.flushAll(ctx, func(_ *Outbox, e OutboxEntry) error {
		var body any
		if uerr := json.Unmarshal(e.Body, &body); uerr != nil {
			// 해석 불가한 줄은 보낼 수 없다. 버리지 않고 남긴 채 사유를 올린다.
			return fmt.Errorf("본문 해석 실패: %w", uerr)
		}
		_, _, err := c.do(ctx, http.MethodPost, e.Path, body, e.Key)
		return err
	})
}

// flushAll 은 큐 전부를 돌고 결과를 합산한다. 전송 함수를 인자로 받는 이유는
// 시험이 서버 없이 갈래를 볼 수 있어야 해서다(하네스를 띄우면 그 갈래가 안 보인다).
func (c *Client) flushAll(ctx context.Context, send func(*Outbox, OutboxEntry) error) ReplayResult {
	var total ReplayResult
	var details []string
	for _, ob := range append([]*Outbox{c.Outbox}, c.Legacy...) {
		res, err := ob.Replay(ctx, func(ctx context.Context, e OutboxEntry) error {
			return send(ob, e)
		})
		total.Sent += res.Sent
		total.Rejected += res.Rejected
		total.Remaining += res.Remaining
		switch {
		case err != nil:
			c.Log.Error("아웃박스 재생 실패", "dir", ob.Dir(), "error", err.Error(), "count", res.Sent)
			details = append(details, ob.Dir()+": "+err.Error())
		case res.Remaining > 0 || res.Rejected > 0:
			// ★ 이 가지가 없어서 **완전 침묵**이었다. 옛 코드는 err!=nil 이거나 Sent>0
			// 일 때만 로그를 냈는데, 남거나 격리만 된 경우가 정확히 err==nil·Sent==0 이다.
			// 큐가 여럿이 된 지금 그 침묵은 "어느 큐가 왜 안 나갔나"에 답할 자리를 없앤다(§9).
			c.Log.Warn("아웃박스가 안 비었다", "dir", ob.Dir(),
				"sent", res.Sent, "remaining", res.Remaining, "rejected", res.Rejected)
			details = append(details, ob.Dir()+": "+res.Detail)
		case res.Sent > 0:
			c.Log.Info("아웃박스 재생", "dir", ob.Dir(), "count", res.Sent)
		}
	}
	switch {
	case len(details) > 0:
		total.Detail = strings.Join(details, " · ")
	case total.Sent > 0:
		total.Detail = fmt.Sprintf("판단 %d건을 재생했다", total.Sent)
	default:
		total.Detail = "대기 중인 판단이 없다"
	}
	return total
}

// LegacyLeftovers 는 옛 자리 큐에 아직 남아 있는 것이다. **읽기만 한다.**
//
// 빈 자리는 안 낸다 — 없는 것을 찍으면 사람이 헛것을 쫓는다.
func (c *Client) LegacyLeftovers() []Leftover {
	var out []Leftover
	for _, ob := range c.Legacy {
		lo := ob.leftover()
		if lo.Pending == 0 && lo.Rejected == 0 && lo.Err == "" {
			continue
		}
		out = append(out, lo)
	}
	return out
}

// healthzResponse 는 /healthz 본문이다(internal/api.HealthzBody 와 같은 모양).
//
// 미도달이면 오류를 낸다 — **캐시로 대신하지 않는다.**
// "서버가 살아 있나"에 캐시로 답하면 그 질문 자체가 무의미해진다.
type healthzResponse struct {
	OK          bool    `json:"ok"`
	APIVersion  string  `json:"api_version"`
	DBOK        bool    `json:"db_ok"`
	DBError     string  `json:"db_error"`
	DiskFreePct float64 `json:"disk_free_pct"`
	DiskKnown   bool    `json:"disk_known"`
	DiskError   string  `json:"disk_error"`
	Auth        struct {
		TokenSet bool `json:"token_set"`
		// LoopbackOpen 은 **관측**이다 — 설정이 열려 있고 루프백 도달이 실제로 있었을 때만 참.
		LoopbackOpen bool `json:"loopback_open"`
		// LoopbackConfigured 는 설정값이다. 옛 서버는 이 축을 안 내므로 거짓으로 온다 —
		// 그 침묵은 Build.Known 이 이미 알리는 "판이 낡았다"와 같은 갈래다.
		LoopbackConfigured bool   `json:"loopback_configured"`
		Notice             string `json:"notice"`
	} `json:"auth"`
	// Build 는 서버 **프로세스**의 빌드 좌표다. 이 축을 안 내는 옛 서버면 Known 이 거짓이고,
	// 그 부재 자체가 "판이 이 축을 알리기 전만큼 낡았다"는 신호다 — 0값으로 접히지 않는다.
	Build buildinfo.Coord `json:"build"`
	// SelfUpdate 는 서버의 자동 갱신 축이다(internal/api.SelfUpdateStatus 와 같은 모양).
	// 이 축을 안 내는 옛 서버면 전부 제로값이다 — Watching=false·Reason="" 이 되고,
	// 그 침묵은 "안 보고 있다"가 아니라 "이 축을 아직 모른다"다. 옛 서버 대조는
	// Build.Known 이 이미 그 사실을 알린다.
	// LedgerBackup 은 매시간 판단 원장 백업 축이다(internal/api.LedgerBackupStatus 와 같은 모양).
	// 이 축을 안 내는 옛 서버면 Running=false·Reason="" 이 되고, 그 침묵은 "안 돌고 있다"가
	// 아니라 "이 축을 아직 모른다"다 — SelfUpdate 와 같은 갈래이고 Build.Known 이 그것을 알린다.
	LedgerBackup struct {
		Running bool   `json:"running"`
		Reason  string `json:"reason"`
		LastAt  string `json:"last_at"`
		Outcome string `json:"outcome"`
		Detail  string `json:"detail"`
		Route   string `json:"route"`
	} `json:"ledger_backup"`
	SelfUpdate struct {
		Watching bool   `json:"watching"`
		Reason   string `json:"reason"`
		Stalled  string `json:"stalled"`
		// Uncovered 는 보고 있는데도 **구조적으로 못 덮는 갈래**다(감시 자리의 이름에 소스
		// 트리가 박혀 있어 버전이 오르는 갱신을 영영 못 본다). Stalled 와 다른 축이라 따로 받는다 —
		// 이 축을 안 내는 옛 서버면 빈 문자열이고, 그 침묵은 Build.Known 이 이미 설명한다.
		Uncovered string `json:"uncovered"`
		LastAt    string `json:"last_at"`
		From      string `json:"from"`
		To        string `json:"to"`
		Outcome   string `json:"outcome"`
		Detail    string `json:"detail"`
	} `json:"self_update"`
}

func (c *Client) Healthz(ctx context.Context) (healthzResponse, error) {
	var h healthzResponse
	raw, _, err := c.do(ctx, http.MethodGet, "/healthz", nil, "")
	if err != nil {
		return h, err
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		return h, fmt.Errorf("healthz 해석 실패: %w", err)
	}
	return h, nil
}
