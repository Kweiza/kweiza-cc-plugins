package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
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

	// Tries 는 **서버에 닿았는데** 실패한 횟수다. 없으면 0 이다(옛 파일과 호환된다).
	//
	// 미도달은 안 센다 — 못 보낸 것은 이 줄이 나쁘다는 증거가 아니다.
	//
	// ★ 이 필드가 없어서 **영구히 막힌 줄이 큐를 영구히 막았다.** 실측(2026-08-04):
	// 서버가 살아 있는데 8/3 12:43 판단 하나가 재생되지 않은 채 남아 있었고,
	// 아웃박스 파일 mtime 이 그날 10:31 로 갱신돼 있었다 — keep() 은 Replay 만 부르므로
	// **재생은 돌았고 전송이 거절당했다**는 뜻이다. 거절은 몇 번을 다시 보내도 같은 답인데
	// Replay 는 그 자리에서 멈추고 뒤엣것을 통째로 남겼다.
	Tries int `json:"tries,omitempty"`
}

// RejectedEntry 는 **영구 거절로 판정돼 격리된** 줄이다.
//
// ★ 버리지 않는다. 판단은 재생성 불가한 유일한 자산이라(설계 §7·§9 "조용히 버리는 것이
// 하나도 없어야 한다") 큐에서 빼되 파일로 옮긴다. 큐를 비우는 것과 기록을 없애는 것은 다르다.
type RejectedEntry struct {
	Entry  OutboxEntry `json:"entry"`
	Reason string      `json:"reason"` // 왜 영구로 판정했나. **항상 채운다**
	At     time.Time   `json:"at"`     // 격리한 시각
}

// maxReplayTries 는 분류가 안 되는 실패를 몇 번까지 참는가다.
//
// ★ 상태코드만으로 가르면 안 되는 이유가 실측에 있다. 세션이 사라진 뒤의 판단 재생처럼
// 하류에서 FK 로 깨지는 실패는 서버가 **500 으로 낸다**(ClassifyError 의 기본 가지).
// 500 은 정의상 "일시 장애일 수 있다"라 재시도가 옳은데, 그 줄은 영원히 500 이다.
// 그래서 시도 횟수라는 둘째 축을 둔다 — 분류가 못 가르는 것을 시간이 가른다.
//
// 5인 이유: 진짜 서버 장애 한 번(재기동·배포)은 명령 몇 번 안에 끝나고, 그보다 오래
// 실패하는 줄은 장애가 아니라 그 줄의 문제다. 크게 잡으면 큐가 그만큼 오래 막힌다.
const maxReplayTries = 5

// ReplayVerdict 는 실패 하나를 어떻게 다룰지다. 사유는 항상 채운다.
type ReplayVerdict struct {
	Permanent bool
	Reason    string
}

// JudgeReplayFailure 는 재생 실패 하나를 분류한다. 순수 함수다.
//
// 축 둘을 본다. **상태코드**(같은 답이 반복되는가)와 **시도 횟수**(분류가 못 가르는데
// 계속 실패하는가). 하나만으로는 부족하다 — 위 maxReplayTries 주석의 500 사례가 그 증거다.
//
// ★ 모르는 실패는 **영구로 접지 않는다.** 판단은 재생성 불가하므로 의심스러우면 남긴다.
// 다만 남기는 것이 영원이 되지 않게 시도 횟수가 상한을 준다.
func JudgeReplayFailure(err error, tries int) ReplayVerdict {
	if err == nil {
		return ReplayVerdict{}
	}
	// ★ **미도달은 횟수에 안 센다. 순서가 여기서 중요하다.**
	// 서버가 꺼져 있는 동안 명령을 다섯 번 돌리는 것은 완전히 정상이고, 그 다섯 번은
	// 그 줄에 대해 **아무것도 말해 주지 않는다** — 못 보낸 것이지 거절당한 것이 아니다.
	// 횟수를 먼저 보면 오프라인이 길다는 이유만으로 멀쩡한 판단이 격리된다.
	// 앞선 판에서 실제로 그렇게 썼고, 훅의 L1 시험이 그것을 잡았다.
	if Unreachable(err, 0) {
		return ReplayVerdict{Reason: "서버에 못 닿았다 — 다음 재생에서 다시 보낸다(미도달은 횟수에 안 센다)"}
	}
	// 여기부터는 **서버에 닿았는데 실패한** 것이다. 그때만 횟수가 뜻을 갖는다.
	if tries+1 >= maxReplayTries {
		return ReplayVerdict{Permanent: true, Reason: fmt.Sprintf(
			"서버에 닿았는데 %d번 연속 실패했다 — 일시 장애로 보기에는 너무 오래다. 마지막 오류: %s",
			tries+1, clip(err.Error(), 300))}
	}
	var ae *APIError
	if errors.As(err, &ae) {
		switch {
		case ae.Status == http.StatusRequestTimeout || ae.Status == http.StatusTooManyRequests:
			return ReplayVerdict{Reason: fmt.Sprintf("서버가 %d 로 되물렀다 — 다시 보낸다", ae.Status)}
		case ae.Status >= 400 && ae.Status < 500:
			return ReplayVerdict{Permanent: true, Reason: fmt.Sprintf(
				"서버가 %d 로 거절했다 — 같은 요청은 몇 번을 보내도 같은 답이다: %s",
				ae.Status, clip(ae.Message, 300))}
		}
	}
	return ReplayVerdict{Reason: "분류할 수 없는 실패 — 판단을 잃지 않으려고 남긴다: " +
		clip(err.Error(), 200)}
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
	case CmdLandAcquire, CmdLandReport, CmdLandLeave, CmdLaneRelease:
		// ★ 기본 가지도 false 라 동작은 같지만, 사유가 다르다. 기본 문구는 "모르는 명령이라"라고
		//   말하는데 이 넷은 아는 명령이다 — 그대로 두면 다음 사람이 "표에 없으니 넣어야겠다"
		//   하고 위쪽(고정) 목록에 넣는다. 고정하면 대기 중인 세션이 land 를 다시 부를 때
		//   **첫 응답("너는 3번째다")이 영원히 재생돼** 차례가 왔는데도 오지 않은 것으로 보인다.
		return false, "응답이 지금 상태다(내 자리·점유자) — 선점과 같은 처지라 고정하면 낡은 답이 재생된다"
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

// 대기열·격리 파일의 이름. 한 자리에 모은다 — 옛 자리 재생이 이 이름으로 큐를 찾으므로
// 두 자리에 흩어 두면 한쪽만 고칠 때 그 큐가 조용히 안 보이게 된다.
const (
	pendingName  = "pending.jsonl"
	rejectedName = "rejected.jsonl"
)

// Outbox 는 디렉토리 하나의 대기열이다. 파일 하나에 JSONL 로 쌓는다.
//
// ★ 예전에는 상태 디렉토리 아래였고, 그 자리가 채널마다 갈려서 셸에서 쌓인 판단을
// 훅·MCP 가 영영 못 보내는 결함이 있었다(OutboxPath 주석에 판정 전문이 있다).
// 지금은 새 쓰기가 고정 자리로 가고, 옛 자리는 **같은 타입의 값을 하나씩 만들어**
// 재생이 함께 돈다(Client.Legacy). 큐 하나가 이 값 하나다.
type Outbox struct {
	dir    string // 대기열·격리 파일이 있는 디렉토리
	source string // 왜 이 자리인가. fd doctor 가 찍는다 — machineSrc 가 선례다
	// now 는 격리 시각을 찍는 시계다. 시험이 갈아 끼울 자리이기도 하다.
	now func() time.Time
	// log 는 **잠금을 못 잡아 무잠금으로 떨어졌다**를 말할 자리다. 없어도 된다(nil 안전).
	//
	// ★ 이 필드가 없으면 fail-open 이 침묵이 된다. 무잠금 갈래는 오늘과 같은 동작이라
	// 나빠지지는 않지만, 그것이 **얼마나 자주 일어나는지**를 아무도 못 보는 상태로 두면
	// "잠금을 넣었다"가 거짓 안심이 된다(설계 §9 — 조용히 버리는 것이 하나도 없어야 한다).
	log *slog.Logger
}

// withLogger 는 잠금 경고를 받을 로거를 꽂는다. Client 가 큐를 만든 직후에 부른다.
func (o *Outbox) withLogger(l *slog.Logger) *Outbox {
	o.log = l
	return o
}

// warn 은 로거가 없어도 안전하다.
func (o *Outbox) warn(msg string, args ...any) {
	if o.log == nil {
		return
	}
	o.log.Warn(msg, args...)
}

func newOutbox(get func(string) (string, bool), home string) *Outbox {
	dir, src := OutboxPath(get, home)
	o := newOutboxAt(dir)
	o.source = src
	return o
}

// newOutboxAt 은 자리를 직접 주는 생성자다. 옛 자리 큐(Client.Legacy)와 시험이 쓴다.
func newOutboxAt(dir string) *Outbox {
	return &Outbox{
		dir:    dir,
		source: "직접 지정",
		now:    func() time.Time { return time.Now().UTC() },
	}
}

// Dir·Source 는 fd doctor 가 "어디를, 왜"를 찍기 위한 자리다.
func (o *Outbox) Dir() string    { return o.dir }
func (o *Outbox) Source() string { return o.source }

// pendingPath·rejectedPath 는 두 파일의 자리다. 같은 디렉토리에 둔다 —
// 같은 축의 같은 자산이고, 격리는 제 큐 옆에 남아야 '어디서 온 것인가'가 안 사라진다.
func (o *Outbox) pendingPath() string  { return filepath.Join(o.dir, pendingName) }
func (o *Outbox) rejectedPath() string { return filepath.Join(o.dir, rejectedName) }

// stamp 는 지금이다. 시계가 안 꽂혔어도 값을 낸다.
func (o *Outbox) stamp() time.Time {
	if o.now == nil {
		return time.Now().UTC()
	}
	return o.now()
}

// Append 는 쓰기 하나를 쌓는다. **같은 키가 이미 있으면 쌓지 않는다** —
// 훅은 실패하면 재시도되므로, 그대로 두면 한 판단이 여러 줄이 된다.
//
// ★ 그 중복 검사는 List→검사→O_APPEND 라 **그 사이에 남이 끼면 서로를 못 본다.**
// 그래서 프로세스 간 잠금 아래에서 돈다(outbox_lock.go).
//
// ★ **피해는 중복이 아니라 삭제였다.** 이 주석은 오래 "한 판단이 여러 줄이 된다"만
// 적어 뒀는데, 재현해 보니 더 무거운 것이 있었다: 재생이 도는 동안 Append 한 줄은
// 재생의 스냅숏에 없어서 keep() 의 전량 재기록에 통째로 지워진다. 격리에도 안 남고,
// Append 를 부른 쪽은 err=nil 을 받아 "쌓았다"를 찍는다. 멱등 키는 그것을 못 막는다 —
// 막을 줄 자체가 파일에서 사라지기 때문이다.
//
// 잠금을 못 잡으면 **무잠금으로 진행한다**(fail-open). 훅 예산을 넘기며 기다리는 것보다
// 오늘과 같은 상태로 떨어지는 쪽이 낫다 — 다만 조용히 떨어지지는 않는다.
func (o *Outbox) Append(e OutboxEntry) error {
	locked, err := withQueueLock(o.dir, queueLockBudget, func() error { return o.appendLocked(e) })
	if locked {
		return err
	}
	o.warn("큐 잠금 없이 쌓는다 — 재생과 겹치면 이 판단이 사라질 수 있다",
		"dir", o.dir, "supported", queueLockSupported, "error", errText(err))
	return o.appendLocked(e)
}

func (o *Outbox) appendLocked(e OutboxEntry) error {
	cur, err := o.List()
	if err != nil {
		return err
	}
	for _, x := range cur {
		if x.Key == e.Key {
			return nil // 이미 쌓여 있다. 조용히 넘어가도 되는 유일한 경우다
		}
	}
	if err := os.MkdirAll(filepath.Dir(o.pendingPath()), 0o755); err != nil {
		return fmt.Errorf("아웃박스 디렉토리 생성 실패: %w", err)
	}
	buf, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("아웃박스 직렬화 실패: %w", err)
	}
	f, err := os.OpenFile(o.pendingPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
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
func (o *Outbox) List() ([]OutboxEntry, error) { return readEntries(o.pendingPath()) }

// readEntries 는 JSONL 대기열 파일 하나를 읽는다.
//
// ★ 깨진 줄을 **조용히 버리지 않는다.** 이 파일은 재생성 불가한 자산이므로
// 해석 실패는 **읽은 데까지와 함께** 오류로 올려 사람이 보게 한다(설계 §9).
func readEntries(path string) ([]OutboxEntry, error) {
	f, err := os.Open(path)
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
// settle 은 재생 한 번의 결과를 파일에 반영한다. 남은 건수를 낸다.
//
// ★ **스냅숏을 되쓰지 않는다. 이것이 이 축의 수정 전부다.**
// 옛 구현은 재생을 시작할 때 뜬 스냅숏에서 처리한 것을 뺀 `left` 를 통째로 되썼다.
// 그 사이 남이 Append 한 줄은 스냅숏에 없으므로 그 되쓰기에 **삭제된다**(실측 33/300).
// 그리고 남이 이미 지운 줄은 스냅숏에 남아 있으므로 **되살아난다**(실측 300/300).
// 여기서는 잠금 안에서 파일을 다시 읽고 **내가 처리한 키만 빼서** 되쓴다. 그러면
// 새로 들어온 줄은 내 관심 밖이라 그대로 살고, 남이 지운 줄은 애초에 안 읽힌다.
//
// ★ Tries 는 **지금 파일값에 +1** 이다. 스냅숏값+1 로 쓰면 겹친 재생 둘이 같은 값에서
// 출발해 같은 값을 써서 시도 하나가 사라진다(실측 299/300). 시도는 가산이라야 맞다.
func (o *Outbox) settle(done map[string]bool, bumpedKey string, snapshotLeft int) (int, error) {
	remaining := snapshotLeft
	merge := func() error {
		cur, err := o.List()
		if err != nil {
			return err
		}
		out := make([]OutboxEntry, 0, len(cur))
		for _, e := range cur {
			if done[e.Key] {
				continue // 보냈거나 격리했다
			}
			if e.Key == bumpedKey {
				e.Tries++
			}
			out = append(out, e)
		}
		remaining = len(out)
		return o.keep(out)
	}
	locked, err := withQueueLock(o.dir, queueLockBudget, merge)
	if locked {
		return remaining, err
	}
	// 잠금을 못 잡아도 **병합으로** 처리한다. 스냅숏 되쓰기로 되돌아가지 않는다 —
	// 잠금 없이도 다시 읽고 빼는 쪽이 창을 훨씬 좁힌다(오늘보다 나쁘지 않다).
	o.warn("큐 잠금 없이 재생 결과를 반영한다 — 겹친 재생이 있으면 판단이 사라질 수 있다",
		"dir", o.dir, "supported", queueLockSupported, "error", errText(err))
	return remaining, merge()
}

// errText 는 nil 오류를 빈 문자열로 낸다. 로그 인자에서 <nil> 을 안 보이게 한다.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// tmpNonce 는 임시 파일 이름에 붙일 조각이다. **pid 만으로는 부족하다** —
// 한 프로세스 안 두 고루틴도 같은 tmp 를 다툴 수 있고, 프레임 루프를 병렬화하는 날
// pid-only 판은 그 자리에서 조용히 무력해진다.
//
// 난수를 못 얻으면 나노초로 대신한다(FreshKey 가 같은 자리에서 같은 선택을 한다).
func tmpNonce() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d-%x", os.Getpid(), time.Now().UnixNano())
	}
	return fmt.Sprintf("%d-%s", os.Getpid(), hex.EncodeToString(b[:]))
}

func (o *Outbox) keep(entries []OutboxEntry) error {
	if len(entries) == 0 {
		if err := os.Remove(o.pendingPath()); err != nil && !os.IsNotExist(err) {
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
	// ★ tmp 이름은 **프로세스마다 다르다.** 고정 이름(`pending.jsonl.tmp`)이면 잠금을
	// 못 잡아 떨어진 갈래 둘이 같은 tmp 에 O_TRUNC 로 쓰고, 그러면 서로의 바이트가
	// 섞인 채 rename 된다. 잠금이 있으면 거의 안 닿는 자리지만, fail-open 갈래가
	// 남아 있는 한 그 자리는 열려 있다.
	tmp := fmt.Sprintf("%s.%s.tmp", o.pendingPath(), tmpNonce())
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("아웃박스 기록 실패: %w", err)
	}
	if err := os.Rename(tmp, o.pendingPath()); err != nil {
		os.Remove(tmp) // 이름이 유일해졌으니 실패한 tmp 는 아무도 안 치운다
		return fmt.Errorf("아웃박스 교체 실패: %w", err)
	}
	return nil
}

// ReplayResult 는 재생 한 번의 결과다. 건수와 **왜 남았는지**를 함께 낸다.
type ReplayResult struct {
	Rejected  int // 영구 거절로 판정해 격리한 건수
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
	sent, rejected := 0, 0
	stopReason, rejectReason := "", ""
	left := []OutboxEntry(nil)
	// done 은 **내가 실제로 처리한** 키다(보냈거나 격리했다). 아래 병합이 이것만 뺀다.
	// bumpedKey 는 시도 횟수를 올려야 하는 키다 — 멈추는 자리는 하나뿐이라 한 개다.
	done := map[string]bool{}
	bumpedKey := ""
	for i, e := range entries {
		err := send(ctx, e)
		if err == nil {
			sent++
			done[e.Key] = true
			continue
		}
		v := JudgeReplayFailure(err, e.Tries)
		if v.Permanent {
			// ★ 격리하고 **계속 간다.** 여기서 멈추면 영원히 실패할 줄 하나가 뒤엣것을
			// 통째로 인질로 잡는다 — 실측된 그 상태가 정확히 이것이었다.
			// 순서 보증은 깨지지 않는다: 보낸 것들 사이의 순서는 그대로이고,
			// 격리된 줄은 애초에 안 보낸다.
			e.Tries++
			if qerr := o.quarantine(RejectedEntry{Entry: e, Reason: v.Reason, At: o.stamp()}); qerr != nil {
				// 격리에 실패하면 **버리지 않는다.** 큐에 남겨 두는 쪽이 잃는 것보다 낫다.
				stopReason = fmt.Sprintf("%d번째(%s)를 격리하지 못했다: %v", i+1, clip(e.Key, 40), qerr)
				left = append(left, entries[i:]...)
				break
			}
			rejected++
			done[e.Key] = true
			if rejectReason == "" {
				rejectReason = v.Reason
			}
			continue
		}
		// 일시 실패는 **그 자리에서 멈춘다** — 뒤엣것을 계속 시도하면 순서가 뒤집히고
		// (판단은 시간축이 의미다) 매번 전량 재시도로 실패 폭풍이 난다.
		//
		// ★ 미도달은 **세지 않는다.** 못 보낸 것은 그 줄에 대한 정보가 아니다
		// (JudgeReplayFailure 의 같은 주석). 세면 오프라인이 길다는 이유만으로 격리된다.
		if !Unreachable(err, 0) {
			e.Tries++
			bumpedKey = e.Key
		}
		left = append(left, e)
		left = append(left, entries[i+1:]...)
		stopReason = fmt.Sprintf("%d번째(%s)에서 멈췄다(%d회째): %v",
			i+1, clip(e.Key, 40), e.Tries, err)
		break
	}
	remaining, err := o.settle(done, bumpedKey, len(left))
	if err != nil {
		return ReplayResult{Sent: sent, Remaining: len(left), Rejected: rejected}, err
	}
	res := ReplayResult{Sent: sent, Remaining: remaining, Rejected: rejected}
	switch {
	case res.Remaining == 0 && res.Rejected == 0:
		res.Detail = fmt.Sprintf("판단 %d건을 재생했다", sent)
	case res.Remaining == 0:
		res.Detail = fmt.Sprintf("판단 %d건 재생 · %d건은 영구 거절이라 격리했다(%s) — %s",
			sent, res.Rejected, o.rejectedPath(), rejectReason)
	default:
		res.Detail = fmt.Sprintf("판단 %d건 재생 · %d건 격리 · %d건 남았다 — %s",
			sent, res.Rejected, res.Remaining, stopReason)
	}
	return res, nil
}

// quarantine 은 영구 거절된 줄을 격리 파일로 옮긴다. **추가 전용이다.**
func (o *Outbox) quarantine(r RejectedEntry) error {
	if err := os.MkdirAll(filepath.Dir(o.rejectedPath()), 0o755); err != nil {
		return fmt.Errorf("격리 디렉토리 생성 실패: %w", err)
	}
	buf, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("격리 직렬화 실패: %w", err)
	}
	f, err := os.OpenFile(o.rejectedPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("격리 파일 열기 실패: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(buf, '\n')); err != nil {
		return fmt.Errorf("격리 기록 실패: %w", err)
	}
	return nil
}

// Rejected 는 격리된 줄 전부다. 파일이 없으면 빈 목록이다(오류가 아니다).
func (o *Outbox) Rejected() ([]RejectedEntry, error) { return readRejected(o.rejectedPath()) }

// Leftover 는 옛 자리 하나에 아직 남아 있는 것이다.
type Leftover struct {
	Dir      string
	Pending  int    // 대기열 줄 수
	Rejected int    // 격리 줄 수 — 이것은 안 비워진다(보관소는 제 큐 옆에 남는다)
	Err      string // 셀 수 없었으면 그 사유. 비어 있을 수 있다
}

// leftover 는 이 큐에 남은 것을 **읽기만 해서** 센다.
//
// ★ 보내지 않는다. 진단이 부작용을 가지면 "찍어 봤더니 상태가 달라졌다"가 되고,
// 그러면 진단을 믿을 수 없다. 재생은 Flush 경로에서만 돈다.
func (o *Outbox) leftover() Leftover {
	lo := Leftover{Dir: o.dir}
	if es, err := o.List(); err != nil {
		lo.Err = err.Error()
	} else {
		lo.Pending = len(es)
	}
	if rs, err := o.Rejected(); err != nil {
		lo.Err = strings.TrimSpace(lo.Err + " " + err.Error())
	} else {
		lo.Rejected = len(rs)
	}
	return lo
}

// readRejected 는 격리 파일 하나를 읽는다. doctor 의 잔량 합산도 이것을 쓴다.
func readRejected(path string) ([]RejectedEntry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("격리 파일 읽기 실패: %w", err)
	}
	var out []RejectedEntry
	for i, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r RejectedEntry
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			// ★ i+1 이다. 같은 파일의 readOutbox 는 line++ 로 이미 1-based 였고
			// 여기만 range 인덱스를 그대로 실어, **한 파일 안에 두 규약이 공존했다.**
			return out, fmt.Errorf("격리 %d번째 줄 해석 실패: %w", i+1, err)
		}
		out = append(out, r)
	}
	return out, nil
}
