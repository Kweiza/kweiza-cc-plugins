package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/kweiza/flightdeck/internal/store"
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
//
// ★ 세션 이전 경로(JudgePreSessionPath — /login·/logout)도 면제한다. 이 판정을 미들웨어
// 본문이 아니라 여기(순수 함수) 안에 두는 이유: 미들웨어에서 갈래를 치면 시험이 그 조건의
// **사본**을 단정하게 되고, 면제 경로가 하나 늘 때 판정과 시험이 따로 논다.
//
// ★ 폼에 키를 심는 우회는 기각했다. 로그인은 키 형식(`<session>:<seq>`)이 요구하는 세션이
// 아직 없어 **원리적으로 키를 못 가진다** — 만들어 심으면 그 키가 재사용돼, 틀린 토큰으로
// 한 번 실패한 뒤 맞는 토큰을 넣어도 멱등 표가 첫 실패 응답을 재생한다.
func JudgeIdempotencyKey(method, path, key string) IdemVerdict {
	if !isWrite(method) {
		return IdemVerdict{OK: true, Reason: "읽기 요청이라 키를 요구하지 않는다"}
	}
	if JudgePreSessionPath(path) {
		return IdemVerdict{OK: true,
			Reason: "세션이 생기기 전의 경로라 <session>:<seq> 키를 만들 수 없다"}
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

// PersistVerdict 는 이 쓰기의 멱등 기억을 **재기동 너머로** 남길지의 판정이다.
// 사유를 항상 채운다 — 사유가 없으면 "안 남기기로 했다"와 "이 라우트를 아예 안 본다"가
// 구분되지 않고, 라우트가 하나 늘 때 조용히 뒤엣것이 된다.
type PersistVerdict struct {
	Persist bool
	Reason  string
}

// JudgePersistIdempotency 는 라우트 하나를 판정한다. 순수 함수다.
//
// ★ 축은 하나다: **재생을 놓치면 중복이 영구히 남는가.**
//
// 메모리 표는 프로세스가 죽으면 통째로 사라진다. 그런데 그 조합이 나는 상황이
// 정확히 설계 §7 이 겨냥한 시나리오다 — 서버가 죽어 아웃박스가 쌓이고, 살아나서
// 재생이 돈다. 그때 서버는 방금 재기동해 기억이 비어 있다.
//
// 그래서 **중복이 안 지워지는 쓰기만** DB 로 내린다. 나머지(신호·발자국·세션 열기·
// 워크스페이스·스냅숏)는 전부 upsert 라 두 번 들어와도 같은 한 행이고, 남기면
// 이득 없이 초당 오는 쓰기가 표를 채운다. 선점은 정반대 이유로 안 남긴다 —
// 응답이 **지금 상태**라 재생하면 남이 반납한 뒤에도 옛 거절이 나간다.
//
// r.Pattern 을 쓰지 않는 이유: 이 판정은 라우터 **앞**에서 필요하고, 그 시점에는
// ServeMux 가 아직 패턴을 안 채웠다. 그래서 경로 조각을 직접 본다.
func JudgePersistIdempotency(method, path string) PersistVerdict {
	if !isWrite(method) {
		return PersistVerdict{Reason: "읽기 요청이라 재생할 부작용이 없다"}
	}
	seg := pathSegments(path)
	post := strings.EqualFold(strings.TrimSpace(method), http.MethodPost)

	switch {
	case post && len(seg) == 3 && seg[0] == "api" && seg[1] == "v1" && seg[2] == "items":
		return PersistVerdict{Persist: true,
			Reason: "항목은 (project,id) 가 PK 라 두 번 들어오면 한쪽이 거절되고, " +
				"재기동 뒤의 재시도는 그 거절을 '만들지 못했다'로 받는다"}

	case post && len(seg) == 5 && seg[0] == "api" && seg[1] == "v1" && seg[2] == "items" && seg[4] == "finish":
		return PersistVerdict{Persist: true,
			Reason: "종료는 판단 저장과 후속 항목 등록을 함께 한다 — 판단은 추가 전용이라 중복이 안 지워진다"}

	case post && len(seg) == 3 && seg[0] == "api" && seg[1] == "v1" && seg[2] == "judgments":
		return PersistVerdict{Persist: true,
			Reason: "판단은 추가 전용이다(UPDATE·DELETE 를 트리거가 막는다) — 중복이 들어가면 되돌릴 방법이 없다"}

	case post && len(seg) == 5 && seg[0] == "api" && seg[1] == "v1" && seg[2] == "counters" && seg[4] == "next":
		return PersistVerdict{Persist: true,
			Reason: "발번은 되돌릴 수 없다 — 재생을 놓치면 같은 요청이 번호를 하나 더 태운다"}

	// ── 랜딩 레인 둘은 **일부러 안 남긴다.** 기본 가지로 떨어뜨려도 결과는 같지만,
	//    둘 다 "왜 안 남기나"가 기본 문구와 다른 사유라 여기에 적는다. 특히 회수는
	//    판단을 하나 만드는 쓰기라, 사유가 없으면 다음 사람이 "추가 전용인데 왜 안 남기지"에
	//    답을 못 찾고 표를 넓히러 간다.
	case post && len(seg) == 3 && seg[0] == "api" && seg[1] == "v1" && seg[2] == "landing":
		return PersistVerdict{
			Reason: "응답이 '지금 내 차례인가'라 재생하면 앞사람이 반납한 뒤에도 옛 자리가 나간다 — " +
				"선점과 같은 처지다. 메모리 표로 충분하다"}

	case post && len(seg) == 6 && seg[0] == "api" && seg[1] == "v1" && seg[2] == "landing" &&
		seg[3] == "rows" && seg[5] == "release":
		return PersistVerdict{
			Reason: "회수는 판단을 남기지만 중복이 원리적으로 안 생긴다 — 두 번째 호출은 그 줄 행이 " +
				"이미 죽어서 거절된다. 메모리 표로 충분하다"}

	case post && len(seg) == 6 && seg[0] == "api" && seg[1] == "v1" && seg[2] == "items" &&
		seg[4] == "claim" && seg[5] == "release":
		return PersistVerdict{
			Reason: "선점 회수도 판단을 남기지만 중복이 원리적으로 안 생긴다 — 두 번째 호출은 " +
				"살아 있는 선점이 없어서 거절된다. 메모리 표로 충분하다"}
	}
	return PersistVerdict{
		Reason: "중복이 영구히 남지 않는 쓰기다(upsert 이거나 응답이 지금 상태다) — " +
			"메모리 표로 충분하고, 남기면 이득 없이 표만 큰다"}
}

// pathSegments 는 경로를 빈 조각 없이 자른다. 순수 함수다.
//
// 문자열 접두 검사(strings.HasPrefix)를 쓰지 않는 이유: `/api/v1/itemsXYZ` 가
// `/api/v1/items` 로 읽히는 것을 원리적으로 막지 못한다. 경계는 구조로 잡는다.
func pathSegments(path string) []string {
	var out []string
	for _, s := range strings.Split(path, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
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

// idemStore 는 키 → 결과 표다. **두 층이다.**
//
//	메모리 — 전부. 같은 프로세스 안의 재시도와 동시 요청을 여기서 접는다
//	DB     — 중복이 영구히 남는 쓰기만(JudgePersistIdempotency 가 고른다)
//
// 앞 판은 메모리 하나뿐이었고, 그 주석은 "재기동하면 비는 것이 옳다"고 적혀 있었다.
// 그 판단이 틀렸다: 재기동 직후가 바로 클라이언트 아웃박스 재생이 도는 순간이고,
// 판단은 추가 전용이라 그때 들어간 중복은 되돌릴 수 없다.
type idemStore struct {
	mu      sync.Mutex
	entries map[string]*idemEntry
	ttl     time.Duration
	max     int
	now     func() time.Time

	// db 가 nil 이면 메모리 전용으로 돈다(시험이 그 축을 대조로 쓴다).
	db  idemBacking
	log *slog.Logger
}

// idemBacking 은 멱등 기록의 영속 계층이다.
//
// 인터페이스로 둔 이유는 시험이 **실패하는 저장소**를 끼울 수 있어야 하기 때문이다 —
// 저장이 실패했을 때 요청을 죽이지 않고 사유를 남기는지가 이 계층의 규율이고,
// 실물 DB 로는 그 축을 만들 수 없다.
type idemBacking interface {
	GetIdemRecord(ctx context.Context, key string) (store.IdemRecord, error)
	PutIdemRecord(ctx context.Context, r store.IdemRecord, ttl time.Duration, max int) error
}

func newIdemStore(ttl time.Duration, max int, now func() time.Time) *idemStore {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	if max <= 0 {
		max = 4096
	}
	return &idemStore{
		entries: map[string]*idemEntry{}, ttl: ttl, max: max, now: now,
		log: slog.Default(),
	}
}

// loadPersisted 는 DB 에 남은 기록을 찾는다. 없으면 (nil, nil).
//
// ★ 조회 실패는 **삼키지 않되 요청을 죽이지도 않는다.** 영속 계층이 고장 나도
// 메모리 층은 여전히 같은 프로세스 안의 재시도를 막으므로 진행이 옳고,
// 다만 그 사이 "재기동을 넘는 보장"이 꺼져 있다는 사실은 로그에 남아야 한다.
func (s *idemStore) loadPersisted(ctx context.Context, key string) *idemEntry {
	if s.db == nil {
		return nil
	}
	rec, err := s.db.GetIdemRecord(ctx, key)
	switch {
	case err == nil:
	case errors.Is(err, store.ErrNotFound):
		return nil
	default:
		s.log.WarnContext(ctx, "멱등 기록 조회 실패 — 재기동을 넘는 멱등 보장이 이 요청에는 없다",
			"error", err.Error())
		return nil
	}
	e := &idemEntry{
		fingerprint: rec.Fingerprint,
		done:        make(chan struct{}),
		at:          rec.At,
		status:      rec.Status,
		body:        rec.Body,
		ctype:       rec.ContentType,
		cached:      true,
	}
	close(e.done)
	return e
}

// begin 은 키를 선점하거나 기존 결과를 돌려준다.
//
// 돌려주는 것 셋 중 하나다 — 새로 처리하라(entry, false, nil) ·
// 기존 결과를 재생하라(entry, true, nil) · 충돌이다(nil, false, err).
//
// persist 는 이 라우트가 재기동을 넘겨야 하는가다(JudgePersistIdempotency 의 판정).
func (s *idemStore) begin(ctx context.Context, key, fingerprint string, persist bool) (*idemEntry, bool, *IdemMatch) {
	// ★ DB 를 **먼저** 본다. 메모리가 비어 있는 유일한 이유가 재기동이고,
	//   그 순간이 바로 이 층이 존재하는 이유다.
	if persist {
		if e := s.loadPersisted(ctx, key); e != nil {
			m := JudgeIdemMatch(e.fingerprint, fingerprint)
			if m.Conflict {
				return nil, false, &m
			}
			return e, true, &m
		}
	}
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
//
// 5xx 는 어느 층에도 저장하지 않는다 — 일시 장애를 영구 응답으로 굳히면
// 하류가 복구된 뒤에도 같은 실패만 돌려주게 된다. 이 정책은 앞 판 그대로다.
func (s *idemStore) finish(ctx context.Context, key string, e *idemEntry,
	status int, ctype string, body []byte, persist bool) {

	s.mu.Lock()
	e.status, e.ctype, e.body = status, ctype, body
	e.cached = status < 500
	if !e.cached {
		delete(s.entries, key) // 일시 장애는 재시도가 가능해야 한다
	}
	s.mu.Unlock()
	e.closeOnce.Do(func() { close(e.done) })

	if !persist || s.db == nil || !e.cached {
		return
	}
	// ★ 저장 실패로 요청을 죽이지 않는다 — 처리는 이미 끝났고 응답도 나갔다.
	//   다만 삼키지도 않는다: 이 줄이 없으면 "재기동을 넘는 보장이 꺼졌다"는 사실이
	//   중복 판단이 실제로 들어갈 때까지 아무 데도 안 남는다.
	rec := store.IdemRecord{
		Key: key, Fingerprint: e.fingerprint, Status: status,
		ContentType: ctype, Body: body, At: s.now(),
	}
	if err := s.db.PutIdemRecord(ctx, rec, s.ttl, s.max); err != nil {
		s.log.WarnContext(ctx, "멱등 기록 저장 실패 — 재기동하면 이 쓰기의 재시도가 중복이 된다",
			"error", err.Error(), "status", status)
	}
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
