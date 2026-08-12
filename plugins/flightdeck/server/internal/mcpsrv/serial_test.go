package mcpsrv

import (
	"context"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// 이 파일이 지키는 것은 **프레임 루프가 한 번에 한 프레임을 돈다는 전제**다.
//
// ★ 이 전제는 mcpsrv 안에서 끝나지 않는다. `fd mcp` 의 운영 배선인 cmd/fd 의 Client 가
// 통째로 그 위에 서 있다:
//
//   - mcpBackend 가 호출마다 app.cli.Session 을 갈아 쓴다(mcpbackend.go 의 일곱 자리 —
//     Pick 둘·Note·AddItem·Finish·land·label). 그 값의 소비자는 멱등 키다(client.go 의 KeyFor).
//
// ★ **이 시험이 지키는 범위가 좁아졌다.** 옛 주석은 여기에 Outbox.Append 와 Cache.Put 도
// 함께 적어 뒀는데, 그 둘은 프로세스 **간** 축이라 애초에 이 시험이 못 보는 자리였다 —
// 프레임 루프를 아무리 순차로 돌려도 다른 fd 프로세스가 같은 파일을 만진다. 그래서
// 그 둘은 이 전제 밖에서 이미 닫혔다(cmd/fd/outbox_lock.go 의 프로세스 간 잠금,
// cache.go 의 호출별 tmp). 여기 남은 것은 **한 프로세스 안의 축 하나**뿐이다.
//
// -race 는 이 축을 **원리적으로 못 본다** — 동시 진입이 아예 없으므로 볼 경합이 없다.
// 그래서 초록이 아무것도 보증하지 않는다. 이 시험이 그 자리를 메운다: 디스패치를
// 병렬로 바꾸는 커밋은 여기서 빨강을 보고, Session 을 함께 고쳐야 초록으로 돌아온다.

// serialProbe 는 백엔드 호출의 **동시 진입**을 센다.
//
// enter 와 leave 사이에 창(window)을 둔다. 창이 없으면 병렬 디스패치라도 고루틴 하나가
// 다른 하나가 뜨기 전에 끝나 버려 겹침을 놓치고, 그러면 이 시험은 병렬화된 뒤에도
// 초록이라 아무것도 안 묶는다.
//
// 반대 방향은 창과 무관하게 **구조적으로** 안전하다: 순차 디스패치에서는 두 호출이
// 동시에 떠 있는 상태 자체가 성립하지 않으므로 max 가 1 을 넘을 수 없다.
// 즉 이 시험은 타이밍 때문에 헛빨강이 나지 않는다 — 빨강은 겹쳤다는 사실 하나뿐이다.
type serialProbe struct {
	Backend
	window time.Duration

	mu       sync.Mutex
	inflight int
	max      int
	calls    int
	overlaps map[string]int
}

func newSerialProbe(be Backend, window time.Duration) *serialProbe {
	return &serialProbe{Backend: be, window: window, overlaps: map[string]int{}}
}

// enter 는 진입을 기록하고 **퇴장 함수**를 낸다. `defer p.enter("X")()` 로 쓴다 —
// 앞쪽은 즉시 돌고(진입) 뒤쪽만 지연된다(퇴장).
func (p *serialProbe) enter(what string) func() {
	p.mu.Lock()
	p.inflight++
	p.calls++
	if p.inflight > p.max {
		p.max = p.inflight
	}
	if p.inflight > 1 {
		p.overlaps[what]++
	}
	p.mu.Unlock()

	time.Sleep(p.window)

	return func() {
		p.mu.Lock()
		p.inflight--
		p.mu.Unlock()
	}
}

func (p *serialProbe) report() (maxInflight, calls int, overlapped []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k := range p.overlaps {
		overlapped = append(overlapped, k)
	}
	sort.Strings(overlapped)
	return p.max, p.calls, overlapped
}

// ── Backend 전면 ─────────────────────────────────────────────────────────────
// 열세 개를 **전부** 감싼다. 하나만 감싸면 "그 하나가 안 겹쳤다"만 참이 되는데,
// 갈아쓰기가 있는 자리는 Pick·Note·AddItem·Finish·SetLabels 다섯이고 파일을 다시 쓰는 자리는
// 그 밖에도 있다. 전제는 백엔드 표면 전체에 걸린 것이므로 시험도 전체를 봐야 한다.

func (p *serialProbe) OpenSession(ctx context.Context, in service.OpenSessionInput) (service.SessionResult, error) {
	defer p.enter("OpenSession")()
	return p.Backend.OpenSession(ctx, in)
}

func (p *serialProbe) Beat(ctx context.Context, sessionID string, kind model.SignalKind, paths []string) error {
	defer p.enter("Beat")()
	return p.Backend.Beat(ctx, sessionID, kind, paths)
}

func (p *serialProbe) Board(ctx context.Context, project string, opt service.BoardOptions) (service.BoardView, error) {
	defer p.enter("Board")()
	return p.Backend.Board(ctx, project, opt)
}

func (p *serialProbe) Pick(ctx context.Context, in service.PickInput) (service.PickResult, error) {
	defer p.enter("Pick")()
	return p.Backend.Pick(ctx, in)
}

func (p *serialProbe) Note(ctx context.Context, in service.NoteInput) (service.NoteResult, error) {
	defer p.enter("Note")()
	return p.Backend.Note(ctx, in)
}

func (p *serialProbe) AddItem(ctx context.Context, in service.AddItemInput) (model.Item, error) {
	defer p.enter("AddItem")()
	return p.Backend.AddItem(ctx, in)
}

func (p *serialProbe) Finish(ctx context.Context, in service.FinishInput) (service.FinishResult, error) {
	defer p.enter("Finish")()
	return p.Backend.Finish(ctx, in)
}

func (p *serialProbe) Alloc(ctx context.Context, project, counter string) (int64, error) {
	defer p.enter("Alloc")()
	return p.Backend.Alloc(ctx, project, counter)
}

func (p *serialProbe) Land(ctx context.Context, in service.LandInput) (service.LandResult, error) {
	defer p.enter("Land")()
	return p.Backend.Land(ctx, in)
}

func (p *serialProbe) LandReport(ctx context.Context, in service.LandReportInput) (service.LandResult, error) {
	defer p.enter("LandReport")()
	return p.Backend.LandReport(ctx, in)
}

func (p *serialProbe) LandLeave(ctx context.Context, in service.LandLeaveInput) (service.LandResult, error) {
	defer p.enter("LandLeave")()
	return p.Backend.LandLeave(ctx, in)
}

func (p *serialProbe) RecentNotes(ctx context.Context, project string, limit int) ([]model.Judgment, error) {
	defer p.enter("RecentNotes")()
	return p.Backend.RecentNotes(ctx, project, limit)
}

func (p *serialProbe) SetLabels(ctx context.Context, in service.LabelInput) (service.LabelResult, error) {
	defer p.enter("SetLabels")()
	return p.Backend.SetLabels(ctx, in)
}

// 컴파일 시점에 계약을 못 박는다 — Backend 에 메서드가 늘면 여기도 늘어야 한다.
var _ Backend = (*serialProbe)(nil)

// ─────────────────────────────────────────────────────────────────────────────

// TestServeNeverOverlapsBackend 는 프레임 루프가 백엔드를 **겹쳐 부르지 않는다**를 단정한다.
//
// 이 시험을 깨는 프로덕션 변경은 정해져 있다: Serve 의 `resp, respond := s.handle(ctx, f.line)`
// 를 고루틴으로 빼는 것. 그렇게 바꾸는 사람은 파일 머리의 셋(cli.Session · Outbox · Cache)을
// 함께 고쳐야 하고, 이 시험이 그 사실을 화면에 띄우는 자리다.
func TestServeNeverOverlapsBackend(t *testing.T) {
	repo := newRepo(t)
	svc, _ := newSvc(t)
	probe := newSerialProbe(svc, 25*time.Millisecond)

	srv := New(probe, discard(),
		WithEnv(env(fullEnv(repo))),
		WithCwd(repo, nil),
		WithHostname("testhost", nil),
	)

	// 갈아쓰기가 있는 네 자리(add·pick·note·finish)를 한 번씩 태운다.
	frames := serve(t, srv,
		call("add", map[string]any{"id": "t5-iam", "title": "제목", "body": "본문"}),
		call("pick", map[string]any{"item_id": "t5-iam"}),
		call("note", map[string]any{"kind": "decision", "body": "왜 그렇게 했나"}),
		call("finish", map[string]any{
			"item_id": "t5-iam", "outcome": "done",
			"title": "랜딩", "body": "무엇을 기각했나",
		}),
	)

	// ── 단정 순서가 이 시험의 절반이다 ───────────────────────────────────────
	// 겹침을 **도구 성공보다 먼저** 본다. 디스패치가 병렬이 되면 add·pick·finish 의
	// 순서가 깨져 도구가 먼저 실패하는데, 그 상태에서 도구 성공을 먼저 단정하면
	// 화면에 뜨는 것이 "finish 가 실패했다"가 되고 **진짜 원인인 겹침이 안 보인다.**
	// 실제로 그 모양을 한 번 보고 이 순서로 고쳤다.

	if len(frames) != 4 {
		t.Fatalf("응답이 %d개다 — 도구 4개를 태웠다: %+v", len(frames), frames)
	}

	maxInflight, calls, overlapped := probe.report()

	// 도구가 전부 이른 거절로 끝나면 백엔드는 거의 안 불리고, 그러면 max=1 은
	// "안 겹쳤다"가 아니라 "볼 것이 없었다"가 된다 — 통과하면서 아무것도 안 보는 시험.
	if calls < 8 {
		t.Fatalf("전제가 깨졌다 — 백엔드 호출이 %d건뿐이다. 겹침을 볼 표본이 없다", calls)
	}

	if maxInflight != 1 {
		t.Fatalf("백엔드에 동시 진입이 %d건 있었다(겹친 메서드: %s).\n"+
			"프레임 루프를 병렬로 바꿨다면, 같은 커밋에서 아래를 함께 고쳐야 한다:\n"+
			"  · cmd/fd/mcpbackend.go — app.cli.Session 갈아쓰기 일곱 자리(Pick 둘·Note·AddItem·Finish·land·label)\n"+
			"    고칠 방법은 잠금이 아니라 **인자화**다. Session 은 KeyFor 에만 필요하고 호출자가\n"+
			"    이미 세션을 손에 들고 있으므로, 넘겨 주면 공유 가변 상태 자체가 없어진다.\n"+
			"아웃박스·캐시의 프로세스 간 축은 이미 닫혀 있다(cmd/fd/outbox_lock.go · cache.go 의 tmpPath).\n"+
			"자세한 갈래는 client.go 의 Client 주석에 있다.",
			maxInflight, strings.Join(overlapped, ", "))
	}

	// 여기부터는 응답이 **넣은 순서대로** 나온다는 사실 위에 선다 — 바로 위에서 단정한 것이다.
	for i, name := range []string{"add", "pick", "note", "finish"} {
		text, isErr := toolText(t, frames[i])
		if isErr {
			t.Fatalf("전제가 깨졌다 — %s 가 실패했다. 백엔드를 끝까지 안 태운 시험은 겹침도 못 본다:\n%s",
				name, text)
		}
	}
}
