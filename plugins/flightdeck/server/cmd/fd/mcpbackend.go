package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/kweiza/flightdeck/internal/api"
	"github.com/kweiza/flightdeck/internal/mcpsrv"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
	"github.com/kweiza/flightdeck/internal/store"
)

// mcpBackend 는 mcpsrv.Backend 의 **REST 구현**이다.
//
// ★ 이 파일이 있는 이유는 `fd mcp` 가 로컬 SQLite 를 직접 열지 않게 하기 위해서다.
// 직접 열면 셋이 깨진다: ① SSE 허브가 internal/api 안에 있어 MCP 가 만든 변화가
// 알림에 한 줄도 안 뜬다 ② 세션 수만큼의 프로세스가 같은 DB 파일에 줄을 선다
// ③ 서버가 다른 머신이면 이 도구는 아무도 없는 빈 보드를 본다.
//
// **클라이언트를 두 벌로 만들지 않는다.** 여기 있는 것은 전부 `fd status|note|pick|…` 이
// 쓰는 것과 같은 *Client 다 — 그래서 열화(L1)·멱등 키·캐시·아웃박스가 한 자리에 있고,
// 그 정책은 JudgeOffline·IdempotencyStable(순수 함수)이 정한 그대로 이 경로에도 적용된다.
//
// ─── 순차 전제 ───────────────────────────────────────────────────────────────
//
// ★ **이 백엔드는 한 번에 한 호출만 도는 전제 위에 있다.** 그 전제의 보증인은
// mcpsrv.Serve 의 프레임 루프다 — 읽기만 고루틴이고 handle 은 루프 본문에서 인라인으로 돈다.
//
// 아래 여섯 자리(Pick 둘 · Note · AddItem · Finish · land)가 호출마다
// `b.app.cli.Session` 을 갈아 쓴다. 클라이언트를 한 벌로 둔 대가이고, 지금은 공유가
// 아니라 **직렬 재사용**이라 안전하다.
//
// 병렬로 도구를 돌리기 시작하는 순간 이 자리가 깨진다. 그런데 깨지는 것이 여기만이 아니라서
// 이 여섯만 고치면 **더 조용한 결함이 남는다** — 무엇이 함께 깨지는지는 client.go 의
// Client 타입 주석에 적어 뒀다. 다만 그 목록의 **프로세스 간 축(아웃박스·캐시)은 이미
// 닫혔다**(outbox_lock.go). 여기 남은 것은 프로세스 **내부** 축뿐이다.
//
// -race 로는 못 잡는다(동시 진입이 없으니 볼 경합이 없다). 그래서 전제를 시험으로 묶었다:
// internal/mcpsrv 의 **TestServeNeverOverlapsBackend**. 프레임 루프를 병렬로 바꾸는 커밋은
// 거기서 빨강을 보고, 셋을 함께 고쳐야 초록으로 돌아온다.
type mcpBackend struct {
	app *App
}

// newMCPBackend 는 REST 백엔드 하나를 만든다.
func newMCPBackend(app *App) *mcpBackend { return &mcpBackend{app: app} }

// 컴파일 시점에 계약을 못 박는다 — 메서드가 하나라도 어긋나면 그 자리에서 빨간불이다.
var _ mcpsrv.Backend = (*mcpBackend)(nil)

// degraded 는 쓰기 한 번의 열화 결과를 mcpsrv 좌표계로 옮긴다.
//
// 처방 이름을 **번역하지 않는다** — OfflineMode 와 DegradedMode 는 같은 축의 같은 값이고,
// 여기서 이름을 갈아 끼우면 "무엇을 했나"가 계층마다 다른 말이 된다.
func degraded(what string, res WriteResult, banner string) *mcpsrv.Degraded {
	return &mcpsrv.Degraded{
		What:   what,
		Mode:   mcpsrv.DegradedMode(res.Mode),
		Reason: res.Reason,
		Banner: banner,
	}
}

// write 는 쓰기 하나를 보내고 열화를 mcpsrv 좌표계로 옮긴다.
//
// **Sent=false 를 성공으로 접지 않는다.** 접는 순간 아웃박스에 쌓인 판단이
// "저장했다"로 화면에 뜨고, 거절된 선점이 "선점했다"로 뜬다.
func (b *mcpBackend) write(ctx context.Context, cmd, path string, body any) ([]byte, error) {
	res, err := b.app.cli.Write(ctx, cmd, path, body)
	if err != nil {
		if Unreachable(err, 0) {
			return nil, degraded(cmd, WriteResult{Mode: res.Mode, Reason: reasonOr(res, err)}, b.banner())
		}
		return nil, b.apiError(cmd, err)
	}
	if !res.Sent {
		return nil, degraded(cmd, res, b.banner())
	}
	if res.Replayed {
		// 서버는 멀쩡했고 호출도 성공했다. 다만 **새로 만들어진 것이 없다.**
		// 값(첫 응답)은 그대로 쓸 수 있으므로 (값, 표식) 둘 다 낸다.
		return res.Body, &mcpsrv.Degraded{
			What: cmd, Mode: mcpsrv.DegradedReplay, Reason: res.Reason,
		}
	}
	return res.Body, nil
}

// read 는 읽기 하나를 보낸다. 캐시로 답했으면 값과 **함께** 열화를 낸다.
//
// (값, 열화) 둘 다 내는 것이 이 함수의 계약이다 — 캐시된 보드는 쓸 수 있는 값이고,
// 동시에 지금 사실이 아니다. 둘 중 하나만 내면 그 사실 하나가 사라진다.
// what 과 cmd 를 나눠 받는 이유: 소비자가 부른 이름(도구)과 열화 정책의 키(명령)가
// 항상 같지는 않다 — `pick` 을 인자 없이 부르면 그것은 REST 의 `next` 다.
// 하나로 합치면 응답 첫 줄이 **부른 적 없는 이름**을 말하게 되고, 읽는 쪽은
// 자기가 뭘 잘못 불렀나부터 의심한다.
func (b *mcpBackend) read(ctx context.Context, what, cmd, path string) ([]byte, *mcpsrv.Degraded, error) {
	rr, err := b.app.cli.Read(ctx, path)
	if err != nil {
		if Unreachable(err, 0) {
			// ★ 여기서 처방을 cache 로 찍으면 **거짓말이 된다** — 캐시가 없어서 온 자리다.
			//   "캐시된 마지막 응답을 냈다"고 말해 놓고 값이 없으면 읽는 쪽은
			//   빈 화면을 낡은 스냅숏으로 믿는다. 아무것도 못 냈으면 못 냈다고 한다.
			return nil, nil, &mcpsrv.Degraded{
				What: what, Mode: mcpsrv.DegradedRefuse,
				Reason: "서버에 못 닿았고 이 머신에 캐시된 응답도 하나도 없다 — " +
					"누가 무엇을 집었는지 알 방법이 지금 없다",
				Banner: rr.Banner, Cause: err,
			}
		}
		return nil, nil, b.apiError(what, err)
	}
	if rr.Fresh {
		return rr.Body, nil, nil
	}
	return rr.Body, &mcpsrv.Degraded{
		What: what, Mode: mcpsrv.DegradedCache,
		Reason: JudgeOffline(cmd).Reason, Banner: rr.Banner,
	}, nil
}

// banner 는 지금 이 머신이 아는 마지막 접속 시각으로 만든 L1 배너다.
func (b *mcpBackend) banner() string {
	return StaleBanner(b.app.now(), b.app.cli.Cache.LastContact(), b.app.cli.URL)
}

// notFoundRelay 는 서버가 낸 404 를 **문구를 건드리지 않고** 올린다.
//
// ★ `fmt.Errorf("%s: %w", msg, store.ErrNotFound)` 를 쓰면 안 된다. 표식의 문구가 "없다"라서
// 소비자 문장 끝에 한 번 더 붙고, 그러면 도구 응답이
// "찾는 것이 없다: 항목 cp/t9-x 가 없다: 없다" 가 된다 — 실제로 그 모양이었다.
//
// guidance 를 함께 나르는 이유는 mcpsrv.NotFoundCarrier 주석에 있다: 종류별 처방표는
// 정본 표면 한 곳에만 있어야 하고, 이 계층은 그것을 옮기기만 한다.
type notFoundRelay struct{ msg, guidance string }

func (e *notFoundRelay) Error() string            { return e.msg }
func (e *notFoundRelay) Unwrap() error            { return store.ErrNotFound }
func (e *notFoundRelay) NotFoundGuidance() string { return e.guidance }

func reasonOr(res WriteResult, err error) string {
	if strings.TrimSpace(res.Reason) != "" {
		return res.Reason
	}
	return clip(err.Error(), 400)
}

// apiError 는 서버가 상태코드로 거절한 것을 mcpsrv 가 아는 오류로 되옮긴다.
//
// ★ 문구를 파싱해 what/reason 을 복원하지 않는다. 서버는 이미 "<무엇> 거절: <사유>" 를
// **조립해서** 보냈고, 그 문장을 다시 쪼개면 문구가 바뀌는 날 조용히 어긋난다.
// 대신 이 호출이 무엇이었는지(what)는 **호출부가 알고 있으므로** 그것을 쓰고,
// 서버 문구가 그 접두로 시작할 때만 접두를 떼어 이중 표기를 막는다.
//
// 404 는 store.ErrNotFound 표식을 단 채로 **서버가 조립한 문구와 처방을 그대로** 올린다.
// 그 판정은 이 계층이 아니라 정본 표면(internal/api 의 NotFoundAdvice)에 있어야 한다.
func (b *mcpBackend) apiError(what string, err error) error {
	var ae *APIError
	if !errors.As(err, &ae) {
		return err
	}
	switch {
	case ae.Status == http.StatusNotFound:
		return &notFoundRelay{msg: ae.Message, guidance: ae.Guidance}
	case ae.Status >= 400 && ae.Status < 500:
		reason := strings.TrimPrefix(ae.Message, what+" 거절: ")
		guidance := ae.Guidance
		if ae.RequestID != "" {
			guidance = strings.TrimSpace(guidance + "\n(request_id=" + clip(ae.RequestID, 64) +
				" · 서버 로그의 같은 값이 원인 전문이다)")
		}
		return &service.RefusedError{What: what, Reason: reason, Guidance: guidance}
	default:
		return err
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// mcpsrv.Backend
// ─────────────────────────────────────────────────────────────────────────────

func (b *mcpBackend) OpenSession(ctx context.Context, in service.OpenSessionInput) (service.SessionResult, error) {
	res, stale, err := b.app.openSession(ctx, openReq{
		Project: in.Project, ProjectPath: in.ProjectPath, DefaultBranch: in.DefaultBranch,
		MachineID: in.MachineID, Hostname: in.Hostname, Worktree: in.Worktree,
		CCSessionID: in.CCSessionID, Label: in.Label,
	})
	if err != nil {
		if Unreachable(err, 0) {
			// openSession 은 미도달이면 **이미 캐시를 봤다.** 여기 온 것은 그것도 없었다는 뜻이라
			// 처방을 cache 로 찍으면 거짓이 된다 — 낼 값이 없었으므로 아무것도 안 한 것이다.
			return res, &mcpsrv.Degraded{
				What: "session open", Mode: mcpsrv.DegradedRefuse,
				Reason: "서버에 못 닿았고 이 머신에 캐시된 세션도 없다 — " +
					"세션 좌표가 없으면 판단·선점을 귀속할 곳이 없다. 세션 id 는 서버가 발급한다",
				Banner: b.banner(), Cause: err,
			}
		}
		return res, b.apiError("session open", err)
	}
	if stale {
		// 캐시된 세션이다. 값은 쓰되 **지금 서버에 그 세션이 있는지는 모른다.**
		return res, &mcpsrv.Degraded{
			What: "session open", Mode: mcpsrv.DegradedCache,
			Reason: JudgeOffline("open").Reason, Banner: b.banner(),
		}
	}
	return res, nil
}

func (b *mcpBackend) Beat(ctx context.Context, sessionID string, kind model.SignalKind, paths []string) error {
	_, err := b.write(ctx, "beat", "/api/v1/sessions/"+urlPath(sessionID)+"/signals",
		beatReq{Kind: string(kind), Paths: paths})
	return err
}

func (b *mcpBackend) Board(ctx context.Context, project string, opt service.BoardOptions) (service.BoardView, error) {
	var v service.BoardView
	path := fmt.Sprintf("/api/v1/dashboard.json?project=%s&self=%s&queue=%t&notes=%t",
		urlValue(project), urlValue(opt.Self), opt.IncludeQueue, opt.IncludeNotes)
	if opt.Window > 0 {
		path += "&window=" + urlValue(opt.Window.String())
	}
	if opt.NoteLimit > 0 {
		path += fmt.Sprintf("&note_limit=%d", opt.NoteLimit)
	}
	raw, deg, err := b.read(ctx, "board", "board", path)
	if err != nil {
		return v, err
	}
	if uerr := json.Unmarshal(raw, &v); uerr != nil {
		return v, fmt.Errorf("보드 응답 해석 실패: %w", uerr)
	}
	if deg != nil {
		return v, deg
	}
	return v, nil
}

func (b *mcpBackend) Pick(ctx context.Context, in service.PickInput) (service.PickResult, error) {
	var r service.PickResult
	// ★ 분기 순서가 중요하다. ItemIDs 검사를 ItemID == "" 검사 뒤에 두면 item_ids 만
	// 준 묶음 요청이(ItemID 는 비었으니) 추천 경로로 빠져 아무것도 안 집는다 —
	// 그것이 이 함수가 고치는 결함이었다(TestMCPBackendPickBundleDoesNotFallToRecommend
	// 가 이 순서를 잠근다: 뒤집으면 그 시험이 빨개진다).
	if len(in.ItemIDs) > 0 {
		// 묶음 선점 — 경로는 선두(ItemIDs[0])다. 본문은 전체 순서를 그대로 싣는다.
		// ★ 공유 상태를 갈아 쓴다 — 이 파일 머리의 "순차 전제" 절을 보라.
		b.app.cli.Session = in.SessionID
		raw, err := b.write(ctx, "pick", "/api/v1/items/"+urlPath(in.ItemIDs[0])+"/claim",
			claimReq{Project: in.Project, SessionID: in.SessionID, ItemIDs: in.ItemIDs})
		if err != nil {
			return r, err
		}
		if uerr := json.Unmarshal(raw, &r); uerr != nil {
			return r, fmt.Errorf("묶음 선점 응답 해석 실패: %w", uerr)
		}
		return r, nil
	}
	// 인자 없는 pick 은 **추천**이라 읽기다(GET /items/next 는 선점하지 않는다).
	// 인자 있는 pick 만 쓰기이고, 그것이 오프라인에서 거절되는 축이다.
	if strings.TrimSpace(in.ItemID) == "" {
		path := fmt.Sprintf("/api/v1/items/next?project=%s&session_id=%s",
			urlValue(in.Project), urlValue(in.SessionID))
		raw, deg, err := b.read(ctx, "pick", "next", path)
		if err != nil {
			return r, err
		}
		if uerr := json.Unmarshal(raw, &r); uerr != nil {
			return r, fmt.Errorf("추천 응답 해석 실패: %w", uerr)
		}
		if deg != nil {
			return r, deg
		}
		return r, nil
	}
	// ★ 공유 상태를 갈아 쓴다 — 이 파일 머리의 "순차 전제" 절을 보라.
	b.app.cli.Session = in.SessionID
	raw, err := b.write(ctx, "pick", "/api/v1/items/"+urlPath(in.ItemID)+"/claim",
		claimReq{Project: in.Project, SessionID: in.SessionID})
	if err != nil {
		return r, err
	}
	if uerr := json.Unmarshal(raw, &r); uerr != nil {
		return r, fmt.Errorf("선점 응답 해석 실패: %w", uerr)
	}
	return r, nil
}

func (b *mcpBackend) Note(ctx context.Context, in service.NoteInput) (service.NoteResult, error) {
	var r service.NoteResult
	// ★ 공유 상태를 갈아 쓴다 — 이 파일 머리의 "순차 전제" 절을 보라.
	b.app.cli.Session = in.SessionID
	raw, err := b.write(ctx, "note", "/api/v1/judgments", noteReq{
		Project: in.Project, SessionID: in.SessionID, Kind: string(in.Kind),
		Title: in.Title, Body: in.Body, ItemID: in.ItemID,
		Supersedes: in.Supersedes, Links: toLinkWire(in.Links),
	})
	if err != nil {
		return r, err
	}
	if uerr := json.Unmarshal(raw, &r); uerr != nil {
		return r, fmt.Errorf("판단 응답 해석 실패: %w", uerr)
	}
	return r, nil
}

func (b *mcpBackend) AddItem(ctx context.Context, in service.AddItemInput) (model.Item, error) {
	// ★ 공유 상태를 갈아 쓴다 — 이 파일 머리의 "순차 전제" 절을 보라.
	b.app.cli.Session = in.SessionID
	raw, err := b.write(ctx, "add", "/api/v1/items", addReq{
		Project: in.Project, SessionID: in.SessionID, ID: in.ID,
		Title: in.Title, Body: in.Body, Paths: in.Paths, Labels: in.Labels,
		After: toAfterWire(in.After),
	})
	if err != nil {
		return model.Item{}, err
	}
	// 응답은 항목을 `{"item": …}` 로 감싼다(internal/api). 감싸지 않은 것으로 읽으면
	// 필드가 전부 0값이 되고 **등록은 됐는데 화면이 빈 줄을 내는** 모양이 된다.
	var wrap struct {
		Item model.Item `json:"item"`
	}
	if uerr := json.Unmarshal(raw, &wrap); uerr != nil {
		return model.Item{}, fmt.Errorf("항목 응답 해석 실패: %w", uerr)
	}
	if strings.TrimSpace(wrap.Item.ID) == "" {
		return model.Item{}, fmt.Errorf("등록은 됐으나 응답에서 항목을 못 찾았다 — 응답 형식이 바뀌었다: %s",
			clip(string(raw), 300))
	}
	return wrap.Item, nil
}

func (b *mcpBackend) Finish(ctx context.Context, in service.FinishInput) (service.FinishResult, error) {
	var r service.FinishResult
	fs := make([]followupReq, 0, len(in.Followups))
	for _, f := range in.Followups {
		fs = append(fs, followupReq{
			ID: f.ID, Title: f.Title, Body: f.Body,
			Paths: f.Paths, Labels: f.Labels, After: toAfterWire(f.After),
		})
	}
	// ★ 공유 상태를 갈아 쓴다 — 이 파일 머리의 "순차 전제" 절을 보라.
	b.app.cli.Session = in.SessionID
	raw, err := b.write(ctx, "finish", "/api/v1/items/"+urlPath(in.ItemID)+"/finish", finishReq{
		Project: in.Project, SessionID: in.SessionID, Outcome: string(in.Outcome),
		Title: in.Title, Body: in.Body, CloseReason: in.CloseReason,
		Followups: fs, Links: toLinkWire(in.Links),
	})
	if err != nil {
		return r, err
	}
	if uerr := json.Unmarshal(raw, &r); uerr != nil {
		return r, fmt.Errorf("마무리 응답 해석 실패: %w", uerr)
	}
	return r, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// 랜딩 레인 — 셋이 한 경로다
// ─────────────────────────────────────────────────────────────────────────────

// land 는 랜딩 표면 한 번을 보내고 응답을 옮긴다.
//
// 셋(Land·LandReport·LandLeave)이 이 하나를 쓰는 이유: 경로도 응답 타입도 하나이고,
// 다른 것은 mode 와 열화 명령 이름뿐이다. 세 벌로 적으면 경로 리터럴이 세 개가 되고
// (그중 하나만 오타 나는 것이 정확히 이 태스크가 막으려는 결함이다), 응답 해석 문구도
// 세 벌이 된다.
//
// ★ 회수(ReleaseLaneRow)는 여기 없다. mcpsrv.Backend 에 그 메서드가 없고, 없는 것이
// 판정이다 — land 도구는 release 를 **거절 사유로만** 안다(mcpsrv.toolLand).
func (b *mcpBackend) land(ctx context.Context, cmd string, req landReq) (service.LandResult, error) {
	var r service.LandResult
	// ★ 공유 상태를 갈아 쓴다 — 이 파일 머리의 "순차 전제" 절을 보라.
	b.app.cli.Session = req.SessionID
	raw, err := b.write(ctx, cmd, landingPath, req)
	if err != nil {
		return r, err
	}
	if uerr := json.Unmarshal(raw, &r); uerr != nil {
		return r, fmt.Errorf("랜딩 응답 해석 실패: %w", uerr)
	}
	// state 는 응답의 뼈대다(RenderLand 가 그것으로 갈린다). 비어 오면 필드 이름이
	// 어긋난 것이고, 그대로 두면 "모르는 상태"가 아니라 **빈 화면**이 나간다.
	if strings.TrimSpace(r.State) == "" {
		return r, fmt.Errorf("랜딩 응답에 state 가 없다 — 응답 형식이 바뀌었다: %s", clip(string(raw), 300))
	}
	return r, nil
}

func (b *mcpBackend) Land(ctx context.Context, in service.LandInput) (service.LandResult, error) {
	return b.land(ctx, CmdLandAcquire, landReq{
		Project: in.Project, SessionID: in.SessionID, Mode: api.LandModeAcquire,
	})
}

func (b *mcpBackend) LandReport(ctx context.Context, in service.LandReportInput) (service.LandResult, error) {
	return b.land(ctx, CmdLandReport, landReq{
		Project: in.Project, SessionID: in.SessionID, Mode: api.LandModeReport,
		Kind: string(in.Kind), Detail: in.Detail,
	})
}

func (b *mcpBackend) LandLeave(ctx context.Context, in service.LandLeaveInput) (service.LandResult, error) {
	return b.land(ctx, CmdLandLeave, landReq{
		Project: in.Project, SessionID: in.SessionID, Mode: api.LandModeLeave,
		Detail: in.Detail,
	})
}

func (b *mcpBackend) Alloc(ctx context.Context, project, counter string) (int64, error) {
	raw, err := b.write(ctx, "alloc", "/api/v1/counters/"+urlPath(counter)+"/next",
		counterReq{Project: project})
	if err != nil {
		return 0, err
	}
	var ar allocResp
	if uerr := json.Unmarshal(raw, &ar); uerr != nil {
		return 0, fmt.Errorf("발번 응답 해석 실패: %w", uerr)
	}
	return ar.Value, nil
}

// RecentNotes 는 꼬리에 실을 ask·blocked 다.
//
// ★ 앞선 판은 dashboard.json 을 `queue=false` 로 쳤다. 그래도 서버는 **세션 카드 파생을
// 통째로 돌린다** — git worktree list + 세션마다 ChangedPaths·UncommittedPaths·UncommittedDelta.
// 꼬리는 모든 도구 응답에 붙으므로(설계 §6) 그 비용이 **도구 호출 1회마다** 얹혔고,
// 그 사실이 어디에도 안 떴다. 지금은 꼬리 전용 표면 하나를 쓴다.
//
// 이 호출의 계측은 /metrics 의 `route="GET /api/v1/notices"` 로 갈라져 뜨고,
// 파생 자체의 비용은 `flightdeck_session_card_derives_total` 이 센다.
func (b *mcpBackend) RecentNotes(ctx context.Context, project string, limit int) ([]model.Judgment, error) {
	if strings.TrimSpace(project) == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	path := fmt.Sprintf("/api/v1/notices?project=%s&limit=%d", urlValue(project), limit)
	raw, deg, err := b.read(ctx, "board", "board", path)
	if err != nil {
		return nil, err
	}
	var v struct {
		Notes []model.Judgment `json:"notes"`
	}
	if uerr := json.Unmarshal(raw, &v); uerr != nil {
		return nil, fmt.Errorf("알림 응답 해석 실패: %w", uerr)
	}
	if deg != nil {
		return v.Notes, deg
	}
	return v.Notes, nil
}

func toAfterWire(in []model.After) []afterWire {
	if len(in) == 0 {
		return nil
	}
	out := make([]afterWire, 0, len(in))
	for _, a := range in {
		out = append(out, afterWire{Item: a.Item, Job: a.Job, SHA: a.SHA})
	}
	return out
}

func toLinkWire(in []model.JudgmentLink) []linkWire {
	if len(in) == 0 {
		return nil
	}
	out := make([]linkWire, 0, len(in))
	for _, l := range in {
		out = append(out, linkWire{TargetKind: l.TargetKind, TargetID: l.TargetID})
	}
	return out
}
