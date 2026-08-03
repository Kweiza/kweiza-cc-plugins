package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 아웃박스 — 오프라인에서 쌓이는 **판단·노트만**. 재연결 시 멱등 재생한다.
//
// 왜 이것만인가: 판단은 원리적으로 재생성 불가한 유일한 자산이다(설계 §7).
// 잃어도 다시 만들면 되는 것에 재생 기구를 만들지 않는다 — 재생 기구는
// 충돌 판정을 요구하고, 그러면 복잡도가 곱해진다.

// OutboxEntry 는 아직 못 보낸 쓰기 하나다.
type OutboxEntry struct {
	Key  string          `json:"key"` // Idempotency-Key. 이 값이 중복 재생을 막는 유일한 축이다
	At   time.Time       `json:"at"`
	Path string          `json:"path"`
	Body json.RawMessage `json:"body"`
}

// IdempotencyStable 은 이 명령의 멱등 키를 **본문으로 고정할지** 정한다. 순수 함수다.
//
// 축은 하나다: **같은 응답을 다시 받아도 되는가.**
//
//   - 고정(true) — 같은 본문의 재시도는 한 건이다. 아웃박스 재생이 이 축에 기댄다.
//   - 새로(false) — 응답에 "지금"이 실려 있어 재사용하면 낡은 답이 나간다.
//
// ★ alloc 을 고정하면 **두 호출이 같은 번호를 받는다** — 이 도구가 없애려는 바로 그 사고다.
// 그래서 이 판정을 불리언 하나로 두지 않고 사유를 함께 돌려준다: 다음 명령이 늘 때
// "왜 이쪽인가"를 물을 자리가 있어야 한다.
func IdempotencyStable(cmd string) (bool, string) {
	switch strings.TrimSpace(cmd) {
	case "note", "finish", "add":
		return true, "같은 본문의 재시도는 한 건이다 — 아웃박스 재생과 훅 재시도가 이 축에 기댄다"
	case "alloc":
		return false, "고정하면 두 호출이 같은 번호를 받는다 — 발번의 존재 이유가 사라진다"
	case "open":
		return false, "응답에 지금 상태(신규 여부·선점 목록)가 실려 있어 고정하면 낡은 답이 재생된다"
	case "beat":
		return false, "신호는 시각이 값이다 — 고정하면 두 번째 신호부터 서버에 안 닿는다"
	case "pick", "claim":
		return false, "선점 결과는 지금 상태다 — 고정하면 남이 반납한 뒤에도 옛 거절이 재생된다"
	default:
		return false, "모르는 명령이라 고정하지 않는다 — 고정은 '응답을 재사용하겠다'는 선언이고, " +
			"그것을 기본값으로 두면 새 명령마다 조용히 낡은 답이 나간다"
	}
}

// FreshKey 는 재사용하지 않을 멱등 키다. 순수 함수는 아니다(난수를 쓴다).
//
// 프로토콜이 모든 쓰기에 키를 요구하므로(설계 §6) 고정이 위험한 명령에도 키는 있어야 한다.
// 난수가 없으면 나노초로 대신한다 — 값이 없는 것보다 낫고, 그 경우도 재사용되지 않는다.
func FreshKey(session string) string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return IdempotencyKey(session, []byte(fmt.Sprintf("nonce-%d", time.Now().UnixNano())))
	}
	s := strings.TrimSpace(session)
	if s == "" {
		s = "nosession"
	}
	return clip(s, 64) + ":" + hex.EncodeToString(buf)
}

// IdempotencyKey 는 쓰기 하나의 **고정** 멱등 키다. 순수 함수다.
//
// 설계 §6 의 형식은 `<session>:<seq>` 다. 세션이 없을 수 있는 경로(훅이 세션 열기 전)가
// 있으므로 본문 해시를 seq 자리에 쓴다 — **같은 세션이 같은 본문을 두 번 쌓으면 한 건이다.**
// 시각을 넣지 않는 이유가 그것이다: 시각을 넣으면 재시도마다 키가 달라져 멱등이 이름뿐이 된다.
func IdempotencyKey(session string, body []byte) string {
	sum := sha256.Sum256(body)
	s := strings.TrimSpace(session)
	if s == "" {
		s = "nosession"
	}
	return clip(s, 64) + ":" + hex.EncodeToString(sum[:12])
}

// Outbox 는 상태 디렉토리 아래의 대기열 하나다. 파일 하나에 JSONL 로 쌓는다.
type Outbox struct{ path string }

func newOutbox(sd StateDir) *Outbox {
	return &Outbox{path: filepath.Join(sd.sub("outbox"), "pending.jsonl")}
}

// Append 는 쓰기 하나를 쌓는다. **같은 키가 이미 있으면 쌓지 않는다** —
// 훅은 실패하면 재시도되므로, 그대로 두면 한 판단이 여러 줄이 된다.
func (o *Outbox) Append(e OutboxEntry) error {
	cur, err := o.List()
	if err != nil {
		return err
	}
	for _, x := range cur {
		if x.Key == e.Key {
			return nil // 이미 쌓여 있다. 조용히 넘어가도 되는 유일한 경우다
		}
	}
	if err := os.MkdirAll(filepath.Dir(o.path), 0o755); err != nil {
		return fmt.Errorf("아웃박스 디렉토리 생성 실패: %w", err)
	}
	buf, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("아웃박스 직렬화 실패: %w", err)
	}
	f, err := os.OpenFile(o.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("아웃박스 열기 실패: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(buf, '\n')); err != nil {
		return fmt.Errorf("아웃박스 기록 실패: %w", err)
	}
	return nil
}

// List 는 대기 중인 전부를 순서대로 낸다. 파일이 없으면 빈 목록이다(오류가 아니다).
//
// ★ 깨진 줄을 **조용히 버리지 않는다.** 이 파일은 재생성 불가한 자산이므로
// 해석 실패는 오류로 올려 사람이 보게 한다(설계 §9 "조용히 버리는 것이 하나도 없어야 한다").
func (o *Outbox) List() ([]OutboxEntry, error) {
	f, err := os.Open(o.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("아웃박스 읽기 실패: %w", err)
	}
	defer f.Close()

	var out []OutboxEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // 판단 본문은 길 수 있다
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var e OutboxEntry
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			return out, fmt.Errorf("아웃박스 %d번째 줄을 해석하지 못했다: %w", line, err)
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return out, fmt.Errorf("아웃박스 주사 실패: %w", err)
	}
	return out, nil
}

// keep 은 남길 항목만 다시 쓴다(원자 교체).
func (o *Outbox) keep(entries []OutboxEntry) error {
	if len(entries) == 0 {
		if err := os.Remove(o.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("아웃박스 비우기 실패: %w", err)
		}
		return nil
	}
	var b strings.Builder
	for _, e := range entries {
		buf, err := json.Marshal(e)
		if err != nil {
			return fmt.Errorf("아웃박스 직렬화 실패: %w", err)
		}
		b.Write(buf)
		b.WriteByte('\n')
	}
	tmp := o.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("아웃박스 기록 실패: %w", err)
	}
	if err := os.Rename(tmp, o.path); err != nil {
		return fmt.Errorf("아웃박스 교체 실패: %w", err)
	}
	return nil
}

// ReplayResult 는 재생 한 번의 결과다. 건수와 **왜 남았는지**를 함께 낸다.
type ReplayResult struct {
	Sent      int
	Remaining int
	Detail    string
}

// Replay 는 쌓인 것을 순서대로 보낸다.
//
// 성공한 것만 지운다. **미도달이면 그 자리에서 멈춘다** — 뒤엣것을 계속 시도하면
// 순서가 뒤집히고(판단은 시간축이 의미다), 매번 전량 재시도로 서버가 살아난 순간
// 실패 폭풍이 난다.
func (o *Outbox) Replay(ctx context.Context, send func(context.Context, OutboxEntry) error) (ReplayResult, error) {
	entries, err := o.List()
	if err != nil {
		return ReplayResult{}, err
	}
	if len(entries) == 0 {
		return ReplayResult{Detail: "대기 중인 판단이 없다"}, nil
	}
	sent := 0
	stopReason := ""
	left := []OutboxEntry(nil)
	for i, e := range entries {
		if err := send(ctx, e); err != nil {
			stopReason = fmt.Sprintf("%d번째(%s)에서 멈췄다: %v", i, clip(e.Key, 40), err)
			left = entries[i:]
			break
		}
		sent++
	}
	if err := o.keep(left); err != nil {
		return ReplayResult{Sent: sent, Remaining: len(left)}, err
	}
	res := ReplayResult{Sent: sent, Remaining: len(left)}
	switch {
	case res.Remaining == 0:
		res.Detail = fmt.Sprintf("판단 %d건을 재생했다", sent)
	default:
		res.Detail = fmt.Sprintf("판단 %d건 재생 · %d건 남았다 — %s", sent, res.Remaining, stopReason)
	}
	return res, nil
}
