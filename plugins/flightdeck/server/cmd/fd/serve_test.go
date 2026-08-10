package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/api"
)

// drainProbe 는 api.Handler 를 만족하는 시험용 표면이다.
//
// ★ 이 타입이 필요한 것 자체가 이 축의 계약이다 — api.Serve 는 종료를 통지받을 수 있는
// 핸들러만 받는다. 맨 *http.ServeMux 를 넘기던 앞선 판은 통지 경로가 빠진 채로 돌았고,
// 컴파일러가 그것을 못 잡았다.
type drainProbe struct {
	http.Handler
	drained chan struct{}
	once    sync.Once
}

func newDrainProbe(h http.Handler) *drainProbe {
	if h == nil {
		h = http.NewServeMux()
	}
	return &drainProbe{Handler: h, drained: make(chan struct{})}
}

func (d *drainProbe) Drain() { d.once.Do(func() { close(d.drained) }) }

var _ api.Handler = (*drainProbe)(nil)

// TestServeWithWatcherJoinsWatcherBeforeReturning 은 Critical 리뷰 회귀 시험이다.
//
// ★ 실제 api.Serve 를 127.0.0.1:0 에 띄우고(다른 세션의 :7420 과 안 부딪힌다),
// 실제 goroutine 스케줄로 "감시기의 exec 시도가 기록되기 전에는 serveWithWatcher 가
// 반환하지 않는다"를 단언한다. drain 악수(served 채널)만 확인한 단위 시험(selfwatch_test.go)은
// 주입한 drain 클로저가 즉시·항상 반환해 이 위험(goroutine 을 안 join 하면 exec 전에
// 프로세스가 죽을 수 있다)을 못 본다 — 이 시험이 그 축을 덮는다.
func TestServeWithWatcherJoinsWatcherBeforeReturning(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	w := newSelfWatcher(log, "/tmp/does-not-matter.db", "")
	w.watching = true
	w.reason = ""
	w.exePath = "/fake/fd"
	w.start = id(10, 1000)
	w.interval = 10 * time.Millisecond // 시험이 30초를 기다리지 않게

	w.stat = func(string) (ExeID, error) { return id(11, 2000), nil } // 항상 "교체됨"
	w.verify = func(context.Context, string, string) (string, error) {
		return "1d044b2 · test", nil
	}
	execStarted := make(chan struct{})
	w.execSelf = func(string, []string, []string) error {
		close(execStarted)
		time.Sleep(50 * time.Millisecond) // exec 가 "느리게 기록되는" 상황을 흉내낸다
		return nil
	}

	done := make(chan int, 1)
	go func() {
		done <- serveWithWatcher(context.Background(), "127.0.0.1:0", newDrainProbe(nil), log, w)
	}()

	select {
	case <-execStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("exec 시도가 안 왔다 — 감시기가 아예 안 돌았다")
	}

	// exec 가 아직 안 끝났는데(50ms sleep 중) 반환하면 join 이 안 된 것이다.
	select {
	case <-done:
		t.Fatal("exec 가 끝나기 전에 serveWithWatcher 가 반환했다 — 감시기 goroutine 을 join 안 한다")
	case <-time.After(15 * time.Millisecond):
	}

	select {
	case got := <-done:
		if got != 0 {
			t.Fatalf("종료코드 %d 다", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("exec 이후에도 반환하지 않았다 — 어딘가 매달렸다")
	}
}

// TestServeWithWatcherReturnsFailWhenExecFails 는 Critical (b) 회귀 시험이다.
//
// ★ execSelf 가 실패하면 Status().Outcome=="failed" 가 남는다. join 없이 반환하면
// 이 검사(serve.go 의 su.Outcome=="failed")가 그 값이 채워지기 전에 읽혀 종료코드 0을
// 낸다 — "재기동이 필요하다"는 신호가 정확히 그 실패 경우에 안 나온다.
func TestServeWithWatcherReturnsFailWhenExecFails(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	w := newSelfWatcher(log, "/tmp/does-not-matter.db", "")
	w.watching = true
	w.reason = ""
	w.exePath = "/fake/fd"
	w.start = id(10, 1000)
	w.interval = 10 * time.Millisecond

	w.stat = func(string) (ExeID, error) { return id(11, 2000), nil }
	w.verify = func(context.Context, string, string) (string, error) {
		return "1d044b2 · test", nil
	}
	w.execSelf = func(string, []string, []string) error {
		time.Sleep(30 * time.Millisecond)
		return errors.New("가짜 exec 실패")
	}

	got := serveWithWatcher(context.Background(), "127.0.0.1:0", newDrainProbe(nil), log, w)
	if got != 1 {
		t.Fatalf("exec 가 실패했는데 종료코드가 %d 다", got)
	}
}

// TestServeDrainsHandlerBeforeExec 는 조합 축(감시기 ↔ api.Serve ↔ 핸들러)을 붙든다.
//
// ★ **exec 시점에 종료 통지가 이미 가 있어야 한다.** 안 그러면 수명이 정해지지 않은
// 응답이 매달린 채로 프로세스가 갈아치워지고, 그 사이 셧다운 유예가 통째로 쓰인다.
// 이 순서는 api.Serve 안에 있어(Drain → Shutdown → 반환 → close(served) → exec)
// 여기서는 인과로만 확인한다 — sleep 도 시계 단정도 없다.
//
// 이 시험이 `serve.go` 의 옛 "우아한 마무리가 아니다" 주석을 실제로 뒤집는 자리다.
func TestServeDrainsHandlerBeforeExec(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	w := newSelfWatcher(log, "/tmp/does-not-matter.db", "")
	w.watching = true
	w.reason = ""
	w.exePath = "/fake/fd"
	w.start = id(10, 1000)
	w.interval = 10 * time.Millisecond

	w.stat = func(string) (ExeID, error) { return id(11, 2000), nil }
	w.verify = func(context.Context, string, string) (string, error) {
		return "1d044b2 · test", nil
	}

	probe := newDrainProbe(nil)
	var drainedAtExec atomic.Bool
	w.execSelf = func(string, []string, []string) error {
		select {
		case <-probe.drained:
			drainedAtExec.Store(true)
		default:
		}
		return nil
	}

	if got := serveWithWatcher(context.Background(), "127.0.0.1:0", probe, log, w); got != 0 {
		t.Fatalf("종료코드 %d 다", got)
	}
	if !drainedAtExec.Load() {
		t.Fatal("exec 시점에 종료 통지가 아직 안 갔다 — 스트림이 매달린 채로 프로세스가 갈아치워진다")
	}
}

// sameSelfUpdate 는 두 api.SelfUpdateStatus 가 같은가다.
//
// reflect.DeepEqual 을 안 쓴다 — time.Time 은 단조 시계 판독과 *Location 을 안에 들고
// 있어 "같은 순간"인데 DeepEqual 이 거짓을 내는 배치가 있다. LastAt 만 nil 여부 + Equal
// 로 보고, 나머지는 전부 비교 가능한 필드(bool·string)라 구조체 `==` 하나로 끝난다.
// 이 모양이라 **필드가 늘어도 이 함수는 안 고쳐도 된다**(== 가 새 필드를 자동으로 센다).
func sameSelfUpdate(a, b api.SelfUpdateStatus) bool {
	if (a.LastAt == nil) != (b.LastAt == nil) {
		return false
	}
	if a.LastAt != nil && !a.LastAt.Equal(*b.LastAt) {
		return false
	}
	a.LastAt, b.LastAt = nil, nil
	return a == b
}

// TestSelfUpdateStatusOfCarriesEachFieldSeparately 는 cmd/fd → internal/api 매핑을 잠근다.
//
// ★ **이 선이 안 잠겨 있었다.** 2026-08-07 실측: 클로저에서 `Uncovered: st.Uncovered`
// 한 줄을 지워도 `go test ./cmd/fd ./internal/api` 가 둘 다 ok 였다. 순수 판정
// (newSelfWatcher)·조립(newServeWatcher)·선 넘기(api.HealthzOf)·화면(RenderHealth)이
// 각자 잠긴 채로 **그 넷을 잇는 변환만** 투명했다. 그 상태에서 /healthz 는 다시
// watching=true 하나만 내고, 화면은 이 브랜치 이전과 정확히 같아진다 — 회귀가 무증상이다.
//
// ★ **필드별로 가른다.** 한 갈래가 전 필드를 한꺼번에 단정하면 다음에 필드가 늘어도
// 그 갈래는 초록인 채로 남는다(늘어난 필드를 아무도 안 넣으니 단정도 안 는다).
//
// ★ 갈래마다 **출력 전체**를 단정한다. 해당 필드 하나만 보면 `Uncovered: st.Stalled`
// 같은 붙여넣기 오염이 통과한다 — 값은 도착하는데 엉뚱한 키로 도착하고, 그 둘은
// 처방이 정반대다(handlers_meta.go 의 "한 필드로 접지 마라"와 같은 축).
//
// ★ 표 끝의 덮개 검사가 selfUpdateStatus 에 필드가 늘면 이 시험을 **먼저** 빨간불로
// 만든다. 그것이 없으면 "필드별 갈래"라는 규율이 다음 사람에게 안 전달된다.
func TestSelfUpdateStatusOfCarriesEachFieldSeparately(t *testing.T) {
	at := time.Date(2026, 8, 7, 2, 9, 20, 0, time.UTC)

	cases := []struct {
		name  string
		field string // selfUpdateStatus 의 필드 이름 — ★ 덮개는 이 값으로 센다
		in    selfUpdateStatus
		want  api.SelfUpdateStatus
	}{
		{
			name: "아무 일도 없으면 아무것도 안 나간다", // 영값 갈래(field 없음)
			in:   selfUpdateStatus{},
			want: api.SelfUpdateStatus{},
		},
		{
			name:  "보고 있다",
			field: "Watching",
			in:    selfUpdateStatus{Watching: true},
			want:  api.SelfUpdateStatus{Watching: true},
		},
		{
			name:  "왜 안 보는지",
			field: "Reason",
			in:    selfUpdateStatus{Reason: "이 서버는 컨테이너다(/.dockerenv)"},
			want:  api.SelfUpdateStatus{Reason: "이 서버는 컨테이너다(/.dockerenv)"},
		},
		{
			// ★ 시간 변환의 값 갈래 — time.Time 이 *time.Time 으로 건너간다.
			name:  "시도한 시각",
			field: "LastAt",
			in:    selfUpdateStatus{Watching: true, LastAt: at},
			want:  api.SelfUpdateStatus{Watching: true, LastAt: &at},
		},
		{
			// ★ 시간 변환의 영값 갈래. 이것이 nil 이 아니면 api 의 omitempty 가 안 걸려
			// "시도가 있었는데 시각이 1년 1월 1일"이라는 응답이 나간다. 부재와 제로는 다른 말이다.
			name:  "시도가 없었으면 시각은 부재다",
			field: "LastAt",
			in:    selfUpdateStatus{Watching: true},
			want:  api.SelfUpdateStatus{Watching: true}, // LastAt == nil
		},
		{
			name:  "어디서",
			field: "From",
			in:    selfUpdateStatus{From: "07e5df4"},
			want:  api.SelfUpdateStatus{From: "07e5df4"},
		},
		{
			name:  "어디로",
			field: "To",
			in:    selfUpdateStatus{To: "1d044b2"},
			want:  api.SelfUpdateStatus{To: "1d044b2"},
		},
		{
			name:  "결과",
			field: "Outcome",
			in:    selfUpdateStatus{Outcome: "refused"},
			want:  api.SelfUpdateStatus{Outcome: "refused"},
		},
		{
			name:  "전문",
			field: "Detail",
			in:    selfUpdateStatus{Detail: "selfcheck exit 1 — 증분 계획이 거절된다"},
			want:  api.SelfUpdateStatus{Detail: "selfcheck exit 1 — 증분 계획이 거절된다"},
		},
		{
			// 일시 고장. Uncovered 와 **다른 키**로 도착해야 한다.
			name:  "지금 못 잰다",
			field: "Stalled",
			in:    selfUpdateStatus{Watching: true, Stalled: "실행 파일을 못 쟀다: no such file or directory"},
			want:  api.SelfUpdateStatus{Watching: true, Stalled: "실행 파일을 못 쟀다: no such file or directory"},
		},
		{
			// ★ 이 브랜치가 침묵을 깨려고 새로 만든 축이다. 여기서 조용히 떨어지면
			// /healthz 는 watching=true 만 내고 화면은 이 브랜치 이전과 같아진다.
			name:  "구조적으로 못 덮는 갈래",
			field: "Uncovered",
			in:    selfUpdateStatus{Watching: true, Uncovered: "이 실행 파일 이름에는 소스 트리가 박혀 있다(런처 bin/fd)"},
			want:  api.SelfUpdateStatus{Watching: true, Uncovered: "이 실행 파일 이름에는 소스 트리가 박혀 있다(런처 bin/fd)"},
		},
	}

	covered := map[string]bool{}
	for _, c := range cases {
		if c.field != "" {
			covered[c.field] = true
		}
		t.Run(c.name, func(t *testing.T) {
			if got := selfUpdateStatusOf(c.in); !sameSelfUpdate(got, c.want) {
				t.Fatalf("변환이 갈렸다\n입력: %+v\n나온 것: %+v\n나와야 할 것: %+v", c.in, got, c.want)
			}
		})
	}

	// ★ 덮개 검사. selfUpdateStatus 에 필드가 늘면 매핑보다 **이 시험이 먼저** 빨간불이 된다.
	typ := reflect.TypeOf(selfUpdateStatus{})
	for i := 0; i < typ.NumField(); i++ {
		if name := typ.Field(i).Name; !covered[name] {
			t.Fatalf("selfUpdateStatus.%s 를 재는 갈래가 없다 — "+
				"필드를 늘렸으면 selfUpdateStatusOf 와 이 표에 **둘 다** 실어라", name)
		}
	}
}

// TestSelfUpdateStatusOfLosesNoFieldAcrossTheLine 은 양쪽 타입의 **필드 집합**이 같은가다.
//
// ★ 위 표는 cmd/fd 쪽에 필드가 느는 것을 잡는다. 반대 방향은 못 잡는다 — api 쪽에만
// 필드가 늘면 표는 그 필드를 모르고, 매핑은 그 자리를 영값으로 둔 채 전부 초록이다.
// 이 선이 존재하는 이유가 정확히 "cmd/fd 의 판정을 api 로 **다 옮긴다**"라서, 이름이
// 갈리는 것 자체가 결함이다.
//
// 타입은 안 본다 — LastAt 은 time.Time ↔ *time.Time 으로 **일부러** 갈려 있다(부재를
// 표현할 수 있어야 해서). 이 시험이 붙드는 것은 이름 집합 하나다.
//
// 이 시험이 울리면 할 일은 둘 중 하나다: 새 필드를 selfUpdateStatusOf 에 싣거나,
// 안 싣는 것이 판정이면 **그 사유를 여기 예외로 적어라**. 조용히 지우지 마라.
func TestSelfUpdateStatusOfLosesNoFieldAcrossTheLine(t *testing.T) {
	names := func(v any) map[string]bool {
		typ := reflect.TypeOf(v)
		out := map[string]bool{}
		for i := 0; i < typ.NumField(); i++ {
			out[typ.Field(i).Name] = true
		}
		return out
	}
	src := names(selfUpdateStatus{})
	dst := names(api.SelfUpdateStatus{})

	for name := range src {
		if !dst[name] {
			t.Fatalf("selfUpdateStatus.%s 가 api.SelfUpdateStatus 에 없다 — 이 축은 선을 못 넘는다", name)
		}
	}
	for name := range dst {
		if !src[name] {
			t.Fatalf("api.SelfUpdateStatus.%s 에 대응하는 cmd/fd 필드가 없다 — "+
				"이 키는 영원히 영값으로 나간다(읽는 쪽은 그것을 사실로 읽는다)", name)
		}
	}
}

// TestServeAPIOptionsWiresLoginScreen 은 배선 누락을 잡는다.
//
// ★ 이 시험이 없으면 배선 빠짐이 **조용하다.** LoginScreen 이 nil 이면 api 는 JSON 401 로
// 접히므로 서버는 멀쩡히 뜨고 REST 도 다 돌고, 오직 브라우저에서만 폼 대신 JSON 이 뜬다.
// serve.go 가 조립을 순수 함수로 뽑아둔 근거가 정확히 이것이다.
func TestServeAPIOptionsWiresLoginScreen(t *testing.T) {
	opt := serveAPIOptions("tok", 60, slog.Default(), false, nil, nil)
	if opt.LoginScreen == nil {
		t.Fatal("LoginScreen 이 nil 이다 — 브라우저가 폼 대신 JSON 401 을 본다")
	}

	// 실제로 폼을 그리는지 본다. nil 아님만 재면 func(...){} 빈 몸통도 통과한다.
	rec := httptest.NewRecorder()
	opt.LoginScreen(rec, httptest.NewRequest("GET", "/", nil),
		api.LoginView{Error: "토큰이 일치하지 않는다", Next: "/?project=x"})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("상태가 %d 다 — 401 이어야 한다", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `name="token"`) {
		t.Fatal("토큰 입력이 없다")
	}
	// ★ 두 LoginView 사이에서 필드가 뒤바뀌지 않았는지 본다. 같은 타입의 문자열 둘이라
	// Error 와 Next 를 맞바꿔도 컴파일이 통과한다.
	if !strings.Contains(body, "토큰이 일치하지 않는다") {
		t.Fatal("사유가 안 실렸다 — 어댑터가 Error 를 잘못 옮겼다")
	}
	if !strings.Contains(body, `value="/?project=x"`) {
		t.Fatal("돌아갈 자리가 안 실렸다 — 어댑터가 Next 를 잘못 옮겼다")
	}
}
