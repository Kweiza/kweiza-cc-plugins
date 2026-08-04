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
// ★ **이 Client 는 "한 번에 한 호출" 전제 위에 서 있다. 동시 호출에 안전하지 않다.**
//
// 이 파일도, Cache 도, Outbox 도 잠금이 하나도 없다(cmd/fd 비시험 코드에 sync·atomic 0건).
// 지금 그것이 문제가 안 되는 이유는 **호출자가 전부 순차**라서다:
//
//   - CLI 서브명령은 한 프로세스가 명령 하나를 처리하고 끝난다.
//   - `fd mcp` 는 mcpsrv.Serve 의 프레임 루프가 프레임을 하나씩 돈다.
//
// 즉 여기 초록은 "지금 안전하다"이지 **"병렬화해도 안전하다"가 아니다.** 겹치면 셋이 깨진다:
//
//  1. Session — 아래 필드 주석. mcpBackend 가 호출마다 갈아 쓴다.
//  2. Outbox.Append — 중복 키 검사가 List→검사→쓰기라 겹치면 둘 다 통과한다(outbox.go).
//  3. Cache.Put — 키마다 tmp 경로가 하나뿐이라 같은 경로에 동시에 쓰면 섞인다(cache.go).
//
// -race 는 이것을 **원리적으로 못 본다** — 동시 진입이 없으니 볼 경합이 없다.
// 그래서 전제를 시험으로 묶어 뒀다: internal/mcpsrv 의 TestServeNeverOverlapsBackend.
// 프레임 루프를 병렬로 바꾸는 커밋은 거기서 빨강을 보고, 위 셋을 함께 고쳐야 초록이 된다.
type Client struct {
	URL    string
	Token  string
	HTTP   *http.Client
	Cache  *Cache
	Outbox *Outbox
	Log    *slog.Logger
	Now    func() time.Time

	// Endpoint 는 URL·Token 을 **어디서 읽었는지**다. 값은 위 두 필드가 들고 있고
	// 여기 있는 것은 그 출처와 경고다 — 값이 예상과 다를 때 "왜 저 주소인가"에 답할 자리다.
	Endpoint Endpoint

	// Session 은 멱등 키의 앞 절반이다. 없을 수 있다(세션 열기 전).
	//
	// ★ **공유 가변 상태다.** 읽는 자리는 KeyFor 하나뿐이지만, 쓰는 자리는 여럿이고
	// 그중 mcpbackend.go 의 넷(Pick·Note·AddItem·Finish)은 **호출마다** 갈아 쓴다.
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
	return &Client{
		Endpoint: ep, // 출처를 들고 있는다 — fd doctor·fd setup 이 '왜 저 주소인가'를 찍는다
		URL:      url,
		Token:    token,
		HTTP:     &http.Client{Timeout: timeout},
		Cache:    newCache(sd),
		Outbox:   newOutbox(sd),
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
func (c *Client) Flush(ctx context.Context) ReplayResult {
	res, err := c.Outbox.Replay(ctx, func(ctx context.Context, e OutboxEntry) error {
		var body any
		if uerr := json.Unmarshal(e.Body, &body); uerr != nil {
			// 해석 불가한 줄은 보낼 수 없다. 버리지 않고 남긴 채 사유를 올린다.
			return fmt.Errorf("본문 해석 실패: %w", uerr)
		}
		_, _, err := c.do(ctx, http.MethodPost, e.Path, body, e.Key)
		return err
	})
	if err != nil {
		c.Log.Error("아웃박스 재생 실패", "error", err.Error(), "count", res.Sent)
	} else if res.Sent > 0 {
		c.Log.Info("아웃박스 재생", "count", res.Sent, "skipped", res.Remaining)
	}
	return res
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
		TokenSet     bool   `json:"token_set"`
		LoopbackOpen bool   `json:"loopback_open"`
		Notice       string `json:"notice"`
	} `json:"auth"`
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
