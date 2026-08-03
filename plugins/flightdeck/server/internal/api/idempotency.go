package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// 멱등 — 모든 쓰기는 Idempotency-Key 를 받는다(설계 §6).
//
// ★ 키를 **필수**로 두는 이유: 없어도 되게 두면 재시도의 안전성이 클라이언트의
// 자발적 규율이 되고, 그 규율은 반드시 뒤쪽부터 빠진다. 훅은 타임아웃으로 끊고
// 다시 부르는 경로가 정상 동작이라 이 축이 없으면 신호·발자국·항목이 조용히 두 번 들어간다.

// IdemVerdict 는 키 검사 결과다. 사유를 항상 채운다.
type IdemVerdict struct {
	OK     bool
	Reason string
}

// JudgeIdempotencyKey 는 쓰기 요청의 키가 성립하는지 본다. 순수 함수다.
//
// 읽기(GET·HEAD)에는 키를 요구하지 않는다 — 재생할 부작용이 없다.
func JudgeIdempotencyKey(method, key string) IdemVerdict {
	if !isWrite(method) {
		return IdemVerdict{OK: true, Reason: "읽기 요청이라 키를 요구하지 않는다"}
	}
	k := strings.TrimSpace(key)
	switch {
	case k == "":
		return IdemVerdict{Reason: "Idempotency-Key 헤더가 없다 — 모든 쓰기에 필요하다"}
	case len(k) > 200:
		return IdemVerdict{Reason: fmt.Sprintf("Idempotency-Key 가 %d자다 — 200자 이하여야 한다", len(k))}
	}
	for _, r := range k {
		if unicode.IsControl(r) || r == ' ' {
			return IdemVerdict{Reason: "Idempotency-Key 에 공백이나 제어문자가 있다"}
		}
	}
	return IdemVerdict{OK: true, Reason: "키가 있다"}
}

// isWrite 는 부작용이 있는 메서드인지 본다.
func isWrite(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// Fingerprint 는 "같은 요청인가"를 판정할 지문이다. 순수 함수다.
//
// 메서드·경로·본문 셋을 전부 넣는다. 키만 보면 클라이언트가 키를 재사용했을 때
// **다른 요청에 남의 응답을 돌려주게 된다** — 그것이 이 축의 유일한 위험이다.
func Fingerprint(method, path string, body []byte) string {
	h := sha256.New()
	h.Write([]byte(strings.ToUpper(method)))
	h.Write([]byte{0})
	h.Write([]byte(path))
	h.Write([]byte{0})
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// IdemMatch 는 같은 키로 다시 온 요청을 어떻게 할지 판정한다. 순수 함수다.
type IdemMatch struct {
	Replay   bool // 저장된 응답을 그대로 낸다
	Conflict bool // 같은 키에 다른 요청이다 — 거절한다
	Reason   string
}

// JudgeIdemMatch 는 저장된 지문과 이번 지문을 대조한다. 순수 함수다.
func JudgeIdemMatch(stored, current string) IdemMatch {
	switch {
	case stored == "":
		return IdemMatch{Reason: "이 키로 처리된 요청이 없다 — 새로 처리한다"}
	case stored == current:
		return IdemMatch{Replay: true, Reason: "같은 키에 같은 요청이다 — 저장된 응답을 그대로 낸다"}
	default:
		return IdemMatch{Conflict: true,
			Reason: "같은 Idempotency-Key 로 다른 내용의 요청이 왔다 — 키를 재사용하면 남의 응답을 받게 된다"}
	}
}

// idemEntry 는 키 하나의 처리 상태다.
//
// done 채널이 있는 이유: 같은 키의 요청이 **동시에** 오면 뒤엣것이 앞엣것의
// 결과를 기다려야 한다. 안 기다리면 둘 다 처리로 들어가 멱등이 깨진다.
type idemEntry struct {
	fingerprint string
	done        chan struct{}
	closeOnce   sync.Once // 결과 확정은 한 번뿐이다. 두 번 닫으면 그 자리에서 서버가 죽는다
	at          time.Time

	status int
	body   []byte
	ctype  string
	cached bool // 5xx 는 저장하지 않는다 — 일시 장애를 영구 응답으로 굳히면 안 된다
}

// idemStore 는 키 → 결과 표다. 메모리 전용이다.
//
// 재기동하면 비는데, 그것이 옳다: 서버가 죽었다 살아난 뒤의 재시도는
// **상태가 실제로 어떤지 모르는 상황**이라 재생이 아니라 새 처리가 맞다.
type idemStore struct {
	mu      sync.Mutex
	entries map[string]*idemEntry
	ttl     time.Duration
	max     int
	now     func() time.Time
}

func newIdemStore(ttl time.Duration, max int, now func() time.Time) *idemStore {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	if max <= 0 {
		max = 4096
	}
	return &idemStore{entries: map[string]*idemEntry{}, ttl: ttl, max: max, now: now}
}

// begin 은 키를 선점하거나 기존 결과를 돌려준다.
//
// 돌려주는 것 셋 중 하나다 — 새로 처리하라(entry, false, nil) ·
// 기존 결과를 재생하라(entry, true, nil) · 충돌이다(nil, false, err).
func (s *idemStore) begin(ctx context.Context, key, fingerprint string) (*idemEntry, bool, *IdemMatch) {
	for {
		s.mu.Lock()
		s.evictLocked()
		e, ok := s.entries[key]
		if !ok {
			e = &idemEntry{fingerprint: fingerprint, done: make(chan struct{}), at: s.now()}
			s.entries[key] = e
			s.mu.Unlock()
			return e, false, nil
		}
		stored := e.fingerprint
		done := e.done
		cached := e.cached
		s.mu.Unlock()

		m := JudgeIdemMatch(stored, fingerprint)
		if m.Conflict {
			return nil, false, &m
		}

		select {
		case <-done:
		case <-ctx.Done():
			return nil, false, &IdemMatch{Reason: "앞선 같은 키의 요청을 기다리다 취소됐다"}
		}
		if !cached {
			// 앞 요청이 5xx 로 끝나 저장되지 않았다. 이 키를 비우고 다시 처리한다.
			s.mu.Lock()
			if cur, ok := s.entries[key]; ok && cur == e && !cur.cached {
				delete(s.entries, key)
			}
			s.mu.Unlock()
			continue
		}
		return e, true, &m
	}
}

// finish 는 처리 결과를 저장하고 대기자를 깨운다.
func (s *idemStore) finish(key string, e *idemEntry, status int, ctype string, body []byte) {
	s.mu.Lock()
	e.status, e.ctype, e.body = status, ctype, body
	e.cached = status < 500
	if !e.cached {
		delete(s.entries, key) // 일시 장애는 재시도가 가능해야 한다
	}
	s.mu.Unlock()
	e.closeOnce.Do(func() { close(e.done) })
}

// evictLocked 는 만료·초과분을 걷어낸다. 호출자가 잠금을 쥐고 있어야 한다.
func (s *idemStore) evictLocked() {
	cut := s.now().Add(-s.ttl)
	for k, e := range s.entries {
		select {
		case <-e.done:
			if e.at.Before(cut) {
				delete(s.entries, k)
			}
		default: // 처리 중인 것은 나이와 무관하게 남긴다
		}
	}
	if len(s.entries) <= s.max {
		return
	}
	type kv struct {
		k string
		t time.Time
	}
	var done []kv
	for k, e := range s.entries {
		select {
		case <-e.done:
			done = append(done, kv{k, e.at})
		default:
		}
	}
	sort.Slice(done, func(i, j int) bool { return done[i].t.Before(done[j].t) })
	for _, d := range done {
		if len(s.entries) <= s.max {
			return
		}
		delete(s.entries, d.k)
	}
}
