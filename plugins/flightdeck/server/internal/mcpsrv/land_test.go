package mcpsrv

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/service"
)

// 이 파일의 소비자 좌표계는 둘이다: **RenderLand 의 사람이 읽는 텍스트**(순수 함수라
// 값을 직접 넣어 본다)와 **land 도구 한 번의 tools/call 응답**(디스패치·게이트·거절이
// 실제로 얽히는 자리라 MCP 왕복으로 본다).

// ─────────────────────────────────────────────────────────────────────────────
// RenderLand — 다섯 갈래
// ─────────────────────────────────────────────────────────────────────────────

func TestRenderLandTurn(t *testing.T) {
	got := RenderLand(service.LandResult{State: "turn", RowID: 5}, t0)
	for _, want := range []string{"네 차례다", "줄 행 5", "result", "leave"} {
		if !strings.Contains(got, want) {
			t.Errorf("turn 응답에 %q 가 없다:\n%s", want, got)
		}
	}
}

// TestRenderLandWaitingWithHolder 는 대기 갈래가 앞사람 세션·획득 경과·마지막 신호 나이
// 셋을 나란히 낸다는 것을 본다 — 브리프가 못박은 표시 요건이다.
func TestRenderLandWaitingWithHolder(t *testing.T) {
	acquired := t0.Add(-10 * time.Minute)
	signal := t0.Add(-2 * time.Minute)
	got := RenderLand(service.LandResult{
		State: "waiting", RowID: 9, Position: 2,
		Holder: &service.LaneHolder{SessionID: "01ARZ3HOLDER", AcquiredAt: acquired, LastSignalAt: &signal},
	}, t0)
	for _, want := range []string{"2번째", "줄 행 9", "01ARZ3HO", "10분", "2분"} {
		if !strings.Contains(got, want) {
			t.Errorf("waiting 응답에 %q 가 없다:\n%s", want, got)
		}
	}
}

// TestRenderLandWaitingHolderWithoutSignal 은 신호가 아예 없던 점유자를 침묵하지 않고
// "없다"고 말한다는 것이다 — 못 읽음과 없음을 가르는 이 레포의 규율이 표시에도 이어진다.
func TestRenderLandWaitingHolderWithoutSignal(t *testing.T) {
	got := RenderLand(service.LandResult{
		State: "waiting", RowID: 3, Position: 2,
		Holder: &service.LaneHolder{SessionID: "s1", AcquiredAt: t0.Add(-1 * time.Minute)},
	}, t0)
	if !strings.Contains(got, "마지막 신호 없음") {
		t.Errorf("신호가 없는 점유자인데 그 사실이 안 보인다:\n%s", got)
	}
}

// TestRenderLandWaitingWithoutHolder 는 두 표가 어긋나 맨 앞인데 아무도 안 쥔 상태를
// 침묵하지 않는다 — service.Land 주석의 "점유자를 지어내지 않는다"가 표시에도 이어진다.
func TestRenderLandWaitingWithoutHolder(t *testing.T) {
	got := RenderLand(service.LandResult{State: "waiting", RowID: 1, Position: 1}, t0)
	if !strings.Contains(got, "쥔 사람이 없다") {
		t.Errorf("점유자 부재가 응답에 없다:\n%s", got)
	}
}

func TestRenderLandReclaimedCarriesReason(t *testing.T) {
	got := RenderLand(service.LandResult{State: "reclaimed", RowID: 7, Reason: "10분 넘게 신호가 없었다"}, t0)
	for _, want := range []string{"네 것이 아니다", "10분 넘게 신호가 없었다"} {
		if !strings.Contains(got, want) {
			t.Errorf("reclaimed 응답에 %q 가 없다:\n%s", want, got)
		}
	}
}

// TestRenderLandReclaimedHeaderNeverContradictsTheReason — reclaimed 는 "내가 점유자가
// 아니다" **전부**를 접는 낱말이라 도달 갈래가 셋이다(service.laneNotMine). 머리글이
// "회수됐다"를 단정하면 그중 둘("레인을 쥔 적이 없다" · "줄에 선 기록이 없다")에서
// 한 문장 안의 앞뒤가 정면 충돌한다 — 사용자에게 나가는 거짓 문장이다.
//
// 세 갈래의 실제 사유 문자열(service/landing.go 의 laneLeftReason·laneNotMine 이 만든다)을
// 그대로 넣고, 머리글이 사유와 싸우지 않는지를 본다.
func TestRenderLandReclaimedHeaderNeverContradictsTheReason(t *testing.T) {
	reasons := []string{
		"4시간째 무응답이라 사람이 회수한다",                          // 진짜 회수됨(left_detail)
		"레인을 쥔 적이 없다 — 줄 행은 아직 살아 있으니 land 로 차례를 확인해라", // 대기 중
		"이 프로젝트 줄에 선 기록이 없다 — 먼저 land 로 줄을 서라",         // 줄에 선 적 없음
	}
	for _, reason := range reasons {
		got := RenderLand(service.LandResult{State: "reclaimed", RowID: 3, Reason: reason}, t0)
		if !strings.Contains(got, reason) {
			t.Errorf("사유가 응답에서 사라졌다(%q):\n%s", reason, got)
		}
		// 머리글이 "회수됐다"를 단정하면 아래 둘에서 거짓이 된다. 사유 자체가 회수를
		// 말하는 것은 참이므로, 사유를 **뺀** 나머지에 그 낱말이 있는지를 본다.
		head := strings.Replace(got, reason, "", 1)
		if strings.Contains(head, "회수") {
			t.Errorf("사유가 %q 인데 머리글이 회수를 단정한다 — 한 문장 안에서 앞뒤가 충돌한다:\n%s",
				reason, got)
		}
	}
}

func TestRenderLandReleasedAndLeft(t *testing.T) {
	if got := RenderLand(service.LandResult{State: "released", RowID: 4}, t0); !strings.Contains(got, "반납했다") {
		t.Errorf("released 응답이 반납을 안 말한다:\n%s", got)
	}
	if got := RenderLand(service.LandResult{State: "left", RowID: 4}, t0); !strings.Contains(got, "빠졌다") {
		t.Errorf("left 응답이 이탈을 안 말한다:\n%s", got)
	}
}

// TestRenderLandNeverMentionsLaneTurn — 브리프의 핵심 금지: 아직 없는 통로(lane-turn 처방,
// 설계 단계 ③)를 가리키는 문구를 내면 안 된다. 다섯 갈래 전부를 훑는다.
func TestRenderLandNeverMentionsLaneTurn(t *testing.T) {
	signal := t0.Add(-1 * time.Minute)
	cases := []service.LandResult{
		{State: "turn", RowID: 1},
		{State: "waiting", RowID: 2, Position: 2, Holder: &service.LaneHolder{SessionID: "s", AcquiredAt: t0, LastSignalAt: &signal}},
		{State: "waiting", RowID: 3, Position: 1},
		{State: "released", RowID: 4},
		{State: "left", RowID: 5},
		{State: "reclaimed", RowID: 6, Reason: "사유"},
	}
	for _, c := range cases {
		got := RenderLand(c, t0)
		if strings.Contains(got, "lane-turn") {
			t.Errorf("state=%s 응답이 아직 없는 처방 lane-turn 을 가리킨다:\n%s", c.State, got)
		}
	}
}

// TestRenderLandUnknownStateStaysLoud 는 service.LandResult.State 가 이 레포의 다섯 낱말과
// 어긋나도 침묵하지 않는다는 것이다 — KnownTool/디스패치 어긋남과 같은 규율.
func TestRenderLandUnknownStateStaysLoud(t *testing.T) {
	got := RenderLand(service.LandResult{State: "bogus", RowID: 1}, t0)
	if !strings.Contains(got, "모르는 상태") || !strings.Contains(got, "bogus") {
		t.Errorf("모르는 상태를 침묵으로 접었다:\n%s", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// land 도구 왕복 — 디스패치·게이트·거절
// ─────────────────────────────────────────────────────────────────────────────

// TestLandNoArgsJoinsAndGetsTurn 은 빈 레인에 처음 서는 세션이 그 자리에서 차례를 받는다는
// 것이다. 세션 귀속(ensureSession)이 land 호출 하나로 일어난다는 것도 함께 본다.
func TestLandNoArgsJoinsAndGetsTurn(t *testing.T) {
	repo := newRepo(t)
	svc, _ := newSvc(t)
	srv := newServer(t, svc, repo, fullEnv(repo))

	frames := serve(t, srv, call("land", map[string]any{}))
	if len(frames) != 1 {
		t.Fatalf("응답이 %d개다", len(frames))
	}
	body, isErr := toolText(t, frames[0])
	if isErr {
		t.Fatalf("land 가 실패했다:\n%s", body)
	}
	if !strings.Contains(body, "네 차례다") {
		t.Errorf("빈 레인의 첫 세션이 차례를 못 받았다:\n%s", body)
	}
}

// TestLandReleaseArgIsRefused 는 pick 의 steal_reason 거절과 같은 판정이 land 에도
// 그대로 있다는 것이다 — 한 서버가 선점 회수는 거절하고 레인 회수는 허용하면
// 그 거절 문구가 화면에서 거짓이 된다.
func TestLandReleaseArgIsRefused(t *testing.T) {
	repo := newRepo(t)
	svc, _ := newSvc(t)
	srv := newServer(t, svc, repo, fullEnv(repo))

	frames := serve(t, srv, call("land", map[string]any{"release": "그만 좀 물려줘"}))
	body, isErr := toolText(t, frames[0])
	if !isErr {
		t.Fatalf("release 인자가 거절되지 않았다:\n%s", body)
	}
	for _, want := range []string{"거절", "회수하지 않는다", "fd lane release --row"} {
		if !strings.Contains(body, want) {
			t.Errorf("거절 응답에 %q 가 없다:\n%s", want, body)
		}
	}
}

// TestLandResultAndLeaveTogetherIsRefused 는 서버가 모호한 인자를 조용히 하나로 고르지
// 않는다는 것이다 — 보고와 이탈은 원장에 남는 결과가 다르다.
func TestLandResultAndLeaveTogetherIsRefused(t *testing.T) {
	repo := newRepo(t)
	svc, _ := newSvc(t)
	srv := newServer(t, svc, repo, fullEnv(repo))

	frames := serve(t, srv, call("land", map[string]any{"result": "ok", "leave": "그만둔다"}))
	body, isErr := toolText(t, frames[0])
	if !isErr {
		t.Fatalf("result 와 leave 동시 입력이 거절되지 않았다:\n%s", body)
	}
	if !strings.Contains(body, "다른 동작") {
		t.Errorf("모호함을 설명하지 않는다:\n%s", body)
	}
}

// TestLandReportRoundTrip 은 줄을 서서 차례를 받은 세션이 result=ok 로 보고하면
// 레인을 반납한다는 것이다.
func TestLandReportRoundTrip(t *testing.T) {
	repo := newRepo(t)
	svc, _ := newSvc(t)
	srv := newServer(t, svc, repo, fullEnv(repo))

	frames := serve(t, srv,
		call("land", map[string]any{}),
		call("land", map[string]any{"result": "ok"}),
	)
	if len(frames) != 2 {
		t.Fatalf("응답이 %d개다", len(frames))
	}
	joined, isErr := toolText(t, frames[0])
	if isErr {
		t.Fatalf("줄 서기가 실패했다:\n%s", joined)
	}
	reported, isErr := toolText(t, frames[1])
	if isErr {
		t.Fatalf("보고가 실패했다:\n%s", reported)
	}
	if !strings.Contains(reported, "반납했다") {
		t.Errorf("보고 응답이 반납을 안 말한다:\n%s", reported)
	}
}

// TestLandLeaveIsIdempotent 는 줄에 선 적 없는 세션의 leave 도 실패가 아니라는 것이다 —
// service.LandLeave 의 멱등 규율이 도구 표면까지 그대로 온다.
func TestLandLeaveIsIdempotent(t *testing.T) {
	repo := newRepo(t)
	svc, _ := newSvc(t)
	srv := newServer(t, svc, repo, fullEnv(repo))

	frames := serve(t, srv, call("land", map[string]any{"leave": "줄에 선 적도 없이 그만둔다"}))
	body, isErr := toolText(t, frames[0])
	if isErr {
		t.Fatalf("선 적 없는 leave 가 실패로 났다:\n%s", body)
	}
	if !strings.Contains(body, "빠졌다") {
		t.Errorf("leave 응답이 이탈을 안 말한다:\n%s", body)
	}
}

// TestLandRefusesWithoutSessionIdentity 는 정체 게이트가 land 를 실제로 막는다는 것이다 —
// sessionBoundTools 표에 land 를 빼먹으면 이 시험 하나도 안 깨진다는 것이 브리프의 경고였다.
func TestLandRefusesWithoutSessionIdentity(t *testing.T) {
	repo := newRepo(t)
	svc, _ := newSvc(t)
	// CLAUDE_CODE_SESSION_ID 를 뺀다 — 프로젝트 좌표만 있고 세션 정체가 없다.
	srv := newServer(t, svc, repo, map[string]string{EnvProjectDir: repo})

	frames := serve(t, srv, call("land", map[string]any{}))
	body, isErr := toolText(t, frames[0])
	if !isErr {
		t.Fatalf("세션 정체 없이 land 가 통과했다 — 원장에 세션 좌표 없는 행이 남는다:\n%s", body)
	}
	if !strings.Contains(body, EnvSessionID) {
		t.Errorf("거절 사유가 결손 축을 안 말한다:\n%s", body)
	}
}

// TestLandWaitingShowsTheFrontRunner 는 레인을 이미 쥔 세션이 있을 때 뒤에 선 세션이
// "너는 N번째다" 와 함께 앞사람 좌표를 실제로 받는다는 것이다 — 왕복 하나로
// 디스패치(toolLand)·서비스(Land)·표시(RenderLand)가 다 맞물린 것을 본다.
//
// 앞사람은 서비스 계층을 직접 불러 세운다: MCP 서버 하나는 환경(worktree·cc_session)
// 하나에 묶여 있어 같은 서버로 "다른 세션"을 흉내 낼 수 없다(설계 §13).
func TestLandWaitingShowsTheFrontRunner(t *testing.T) {
	repo := newRepo(t)
	svc, _ := newSvc(t)
	project := filepath.Base(repo)

	other, err := svc.OpenSession(context.Background(), service.OpenSessionInput{
		Project: project, ProjectPath: repo, MachineID: "other-machine",
		Hostname: "other-machine", Worktree: repo, CCSessionID: "cc-other",
	})
	if err != nil {
		t.Fatalf("앞사람 세션을 못 열었다: %v", err)
	}
	if _, err := svc.Land(context.Background(), service.LandInput{
		Project: project, SessionID: other.Session.ID,
	}); err != nil {
		t.Fatalf("앞사람이 레인을 못 잡았다: %v", err)
	}

	srv := newServer(t, svc, repo, fullEnv(repo))
	frames := serve(t, srv, call("land", map[string]any{}))
	body, isErr := toolText(t, frames[0])
	if isErr {
		t.Fatalf("land 가 실패했다:\n%s", body)
	}
	for _, want := range []string{"2번째", ShortID(other.Session.ID)} {
		if !strings.Contains(body, want) {
			t.Errorf("대기 응답에 %q 가 없다:\n%s", want, body)
		}
	}
}
