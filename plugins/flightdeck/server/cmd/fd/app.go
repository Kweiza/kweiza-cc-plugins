package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/service"
)

// App 은 클라이언트 한 실행분의 좌표다. 서브명령·훅·MCP 가 전부 이것을 공유한다.
type App struct {
	env     func(string) (string, bool)
	log     *slog.Logger
	sd      StateDir
	cli     *Client
	proj    ProjectCoord
	machine string
	notice  string // 도구가 스스로 못 한 것. 침묵하지 않는다
	host    string
	stdin   io.Reader // note·finish 의 본문이 여기서 온다. os.Stdin 을 본문에 박으면 시험이 못 준다
	now     func() time.Time
}

func newApp(env func(string) (string, bool), log *slog.Logger, cwd string, stdin io.Reader) *App {
	home, _ := os.UserHomeDir()
	sd := ResolveStateDir(env, home)
	mid, warn := MachineID(sd)
	host, herr := os.Hostname()
	if herr != nil {
		host = "unknown"
		warn = strings.TrimSpace(warn + " · hostname 을 못 읽었다: " + herr.Error())
	}
	a := &App{
		env: env, log: log, sd: sd,
		cli:     newClient(sd, env, log),
		proj:    resolveProject(env, cwd),
		machine: mid,
		notice:  warn,
		host:    host,
		stdin:   stdin,
		now:     func() time.Time { return time.Now().UTC() },
	}
	return a
}

// ccSessionID 는 이 세션의 Claude Code UUID 다.
//
// ★ **인자로 만들지 않는다**(설계 §13). MCP stdio 서버 환경의 CLAUDE_CODE_SESSION_ID 이고
// 훅은 stdin 페이로드의 session_id 다. 파생 가능한 값에 파라미터를 두면 틀린 값이 들어온다.
func (a *App) ccSessionID(fromHook string) string {
	if s := strings.TrimSpace(fromHook); s != "" {
		return s
	}
	if v, ok := a.env("CLAUDE_CODE_SESSION_ID"); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return ""
}

const sessionCachePath = "/local/session"

// OpenSession 은 세션을 열고 결과를 낸다. 미도달이면 **캐시된 마지막 세션**을 낸다.
//
// stale=true 로 온 결과의 세션 id 는 지금 서버에 없을 수도 있다 — 그래서 그 사실을
// 호출부가 배너로 나른다. 조용히 쓰면 "등록됐다"는 거짓이 화면에 남는다.
func (a *App) OpenSession(ctx context.Context, ccSession, label string) (res service.SessionResult, stale bool, err error) {
	in := openReq{
		Project: a.proj.ID, ProjectPath: a.proj.Path, MachineID: a.machine,
		Hostname: a.host, Worktree: a.proj.Worktree, CCSessionID: ccSession, Label: label,
	}
	// 세션 열기는 **고정 키를 쓰지 않는다** — 응답에 지금 상태(신규 여부·선점 목록)가
	// 실려 있어 고정하면 낡은 답이 재생된다. 중복 등록은 3중키가 이미 막는다.
	raw, werr := a.cli.do(ctx, "POST", "/api/v1/sessions", in, FreshKey(a.cli.Session))
	if werr == nil {
		if uerr := json.Unmarshal(raw, &res); uerr != nil {
			return res, false, fmt.Errorf("세션 응답 해석 실패: %w", uerr)
		}
		a.cli.Session = res.Session.ID
		key := sessionCachePath + "/" + ccSession
		if cerr := a.cli.Cache.Put(key, raw, a.now()); cerr != nil {
			a.log.Warn("세션 캐시 보관 실패", "error", cerr.Error())
		}
		return res, false, nil
	}
	if !Unreachable(werr, 0) {
		return res, false, werr
	}
	ent, cerr := a.cli.Cache.Get(sessionCachePath + "/" + ccSession)
	if cerr != nil {
		return res, true, fmt.Errorf("%w · 이 세션의 캐시도 없다: %v", werr, cerr)
	}
	if uerr := json.Unmarshal(ent.Body, &res); uerr != nil {
		return res, true, fmt.Errorf("세션 캐시 해석 실패: %w", uerr)
	}
	a.cli.Session = res.Session.ID
	return res, true, nil
}

// clientAPIVersion 은 이 바이너리가 아는 계약 버전이다.
// 서버와 같은 상수를 쓴다 — 두 벌로 두면 스큐 배너가 자기 자신을 못 본다.
const clientAPIVersion = service.APIVersion

// Board 는 보드 한 장이다. 미도달이면 캐시 + 배너다.
//
// 경로가 `/api/v1/dashboard.json` 인 이유: 설계 §6 의 REST 표에 board 라는 표면이 없다.
// 화면 한 장분의 값을 내는 그 엔드포인트가 보드의 정본이다.
func (a *App) Board(ctx context.Context, self string) (v service.BoardView, banner string, err error) {
	path := fmt.Sprintf("/api/v1/dashboard.json?project=%s&self=%s",
		urlValue(a.proj.ID), urlValue(self))
	rr, rerr := a.cli.Read(ctx, path)
	if rerr != nil {
		return v, rr.Banner, rerr
	}
	if uerr := json.Unmarshal(rr.Body, &v); uerr != nil {
		return v, rr.Banner, fmt.Errorf("보드 응답 해석 실패: %w", uerr)
	}
	return v, rr.Banner, nil
}

// ServerBanner 는 서버 상태 배너다. 도달하면 스큐만 보고, 미도달이면 L1 문안이다.
func (a *App) ServerBanner(ctx context.Context) (banner string, reachable bool) {
	h, err := a.cli.Healthz(ctx)
	if err != nil {
		if Unreachable(err, 0) {
			return StaleBanner(a.now(), a.cli.Cache.LastContact(), a.cli.URL), false
		}
		return fmt.Sprintf("⚠ 조정 서버가 응답했으나 거절했다(%s): %s",
			clip(a.cli.URL, 120), clip(err.Error(), 200)), false
	}
	b := SkewBanner(clientAPIVersion, h.APIVersion)
	if !h.DBOK {
		b = strings.TrimSpace(b + "\n⚠ 서버는 살아 있으나 DB 가 열려 있지 않다 — 쓰기가 전부 실패한다.")
	}
	if h.DiskKnown && h.DiskFreePct >= 0 && h.DiskFreePct < 5 {
		b = strings.TrimSpace(b + fmt.Sprintf("\n⚠ 서버 디스크 여유 %.1f%% — 임계다.", h.DiskFreePct))
	}
	return b, true
}

// urlValue 는 질의 문자열 값 하나를 이스케이프한다.
// 손으로 %02X 를 찍지 않는다 — 멀티바이트 문자에서 조용히 깨지고, 프로젝트 id 는 한글일 수 있다.
func urlValue(s string) string { return url.QueryEscape(s) }
