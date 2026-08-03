package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

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
// 404 는 store.ErrNotFound 로 감싼다 — mcpsrv 의 not-found 처방(프로젝트 미등록 안내)이
// 그 축을 보고 있고, 그 처방은 이 계층이 아니라 거기에 있어야 한다.
func (b *mcpBackend) apiError(what string, err error) error {
	var ae *APIError
	if !errors.As(err, &ae) {
		return err
	}
	switch {
	case ae.Status == http.StatusNotFound:
		return fmt.Errorf("%s: %w", ae.Message, store.ErrNotFound)
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
// REST 표면에 "종류별 최근 판단" 엔드포인트가 **없다**(설계 §6 의 표에 없다). 있는 것은
// 전문 검색(q 필수)과 화면 한 장분(dashboard.json)뿐이라 후자를 쓴다 —
// 없는 표면을 새로 열지 않는 것이 이 전환의 범위이기도 하다(배관 교체지 표면 변경이 아니다).
// 세션 카드·큐는 안 쓰므로 queue=false 로 그만큼은 덜어 낸다.
func (b *mcpBackend) RecentNotes(ctx context.Context, project string, limit int) ([]model.Judgment, error) {
	if strings.TrimSpace(project) == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	path := fmt.Sprintf("/api/v1/dashboard.json?project=%s&queue=false&notes=true&note_limit=%d",
		urlValue(project), limit)
	raw, deg, err := b.read(ctx, "board", "board", path)
	if err != nil {
		return nil, err
	}
	var v service.BoardView
	if uerr := json.Unmarshal(raw, &v); uerr != nil {
		return nil, fmt.Errorf("알림 응답 해석 실패: %w", uerr)
	}
	out := append(append([]model.Judgment(nil), v.Asks...), v.Blocked...)
	if deg != nil {
		return out, deg
	}
	return out, nil
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
