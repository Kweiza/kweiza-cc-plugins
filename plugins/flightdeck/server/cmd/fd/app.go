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

	"github.com/kweiza/flightdeck/internal/buildinfo"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// App 은 클라이언트 한 실행분의 좌표다. 서브명령·훅·MCP 가 전부 이것을 공유한다.
type App struct {
	env        func(string) (string, bool)
	log        *slog.Logger
	sd         StateDir
	cli        *Client
	proj       ProjectCoord
	machine    string
	notice     string // 도구가 스스로 못 한 것. 침묵하지 않는다
	machineSrc string // machine-id 를 읽은 자리. doctor 가 찍는다 — 값이 갈리면 여기가 원인이다
	beaconDir  string // 창 비콘 디렉토리(BeaconDir). mcp.go 가 mcpsrv.WithBeaconDir 에 그대로 넘긴다
	// beaconSrc 는 그 자리를 **고른 사유**다. machineSrc 가 선례다 — 값이 예상과 다를 때
	// "왜 저 값인가"에 답할 자리가 없으면 /proc 을 뒤지게 된다. 새 정체 경로를 더해 놓고
	// 같은 줄을 빼먹었던 것이 fd-doctor-beacon-axis 다.
	beaconSrc string
	host      string
	stdin     io.Reader // note·finish 의 본문이 여기서 온다. os.Stdin 을 본문에 박으면 시험이 못 준다
	now       func() time.Time
}

func newApp(env func(string) (string, bool), log *slog.Logger, cwd string, stdin io.Reader) *App {
	home := homeDir(env)
	sd := ResolveStateDir(env, home)
	mid, midSrc, warn := MachineID(env, home)
	beaconDir, beaconSrc := BeaconDir(env, home)
	host, herr := os.Hostname()
	if herr != nil {
		host = "unknown"
		warn = strings.TrimSpace(warn + " · hostname 을 못 읽었다: " + herr.Error())
	}
	cli := newClient(sd, env, home, log)
	// 설정 파일의 경고(깨졌다·권한이 넓다)를 같은 자리로 합류시킨다 —
	// notice 는 "도구가 스스로 못 한 것"의 자리이고, 조용히 사라지면 안 된다.
	if w := strings.TrimSpace(cli.Endpoint.Warn); w != "" {
		warn = strings.TrimSpace(warn + " · " + w)
	}
	a := &App{
		env: env, log: log, sd: sd,
		cli:        cli,
		proj:       resolveProject(env, cwd),
		machine:    mid,
		machineSrc: midSrc,
		beaconDir:  beaconDir,
		beaconSrc:  beaconSrc,
		notice:     warn,
		host:       host,
		stdin:      stdin,
		now:        func() time.Time { return time.Now().UTC() },
	}
	return a
}

// homeDir 는 홈 디렉토리다. **주입된 환경이 먼저다.**
//
// os.UserHomeDir 는 프로세스 환경을 직접 읽으므로 시험이 그것을 못 바꾼다. 그 상태로
// machine-id 자리를 홈에 두면 시험이 **사용자의 진짜 machine-id 를 읽고 쓴다** —
// 시험이 운영 상태를 오염시키고, 그 오염은 다음 세션에서야 드러난다.
func homeDir(get func(string) (string, bool)) string {
	if v, ok := get("HOME"); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	h, _ := os.UserHomeDir()
	return h
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
	return a.openSession(ctx, openReq{
		Project: a.proj.ID, ProjectPath: a.proj.Path, MachineID: a.machine,
		Hostname: a.host, Worktree: a.proj.Worktree, CCSessionID: ccSession, Label: label,
	})
}

// openSession 은 **주어진 정체 그대로** 세션을 연다.
//
// 좌표를 인자로 받는 갈래가 따로 있는 이유: MCP 서버는 자기 정체를 스스로 관측하고
// (mcpsrv.ResolveIdentity — 설계 §13), 그 값이 이 App 의 좌표와 다를 수 있다.
// 여기서 조용히 App 의 좌표로 갈아 끼우면 도구가 관측한 정체와 원장에 남는 정체가
// 갈라지고, 그 어긋남은 어느 화면에도 안 뜬다.
func (a *App) openSession(ctx context.Context, in openReq) (res service.SessionResult, stale bool, err error) {
	ccSession := in.CCSessionID
	// 세션 열기는 **고정 키를 쓰지 않는다** — 응답에 지금 상태(신규 여부·선점 목록)가
	// 실려 있어 고정하면 낡은 답이 재생된다. 중복 등록은 3중키가 이미 막는다.
	raw, _, werr := a.cli.do(ctx, "POST", "/api/v1/sessions", in, FreshKey(a.cli.Session))
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

// moveSessionCache 는 세션 캐시를 새 cc 키로 옮긴다. rekey 가 성공한 직후에만 부른다.
//
// ★ openSession 은 응답을 sessionCachePath+"/"+cc 에 캐시한다(위 참조). cc 가 갈리면
// 그 키가 낡아 오프라인 읽기가 빗나가고 "이 세션의 캐시도 없다"가 된다 —
// **서버가 안 닿는 순간에만** 드러나는 결함이라, 안 옮기면 정작 필요한 그날 아무도 못 찾는다.
//
// 옛 키는 지우지 않는다. 지우는 쪽이 얻는 것은 파일 하나이고, 잃는 것은 rekey 직후
// 옛 cc 로 들어오는 실행(예: 아직 안 끝난 다른 채널)의 마지막 스냅숏이다.
func (a *App) moveSessionCache(oldCC, newCC string) {
	if oldCC == "" || newCC == "" || oldCC == newCC {
		return
	}
	ent, err := a.cli.Cache.Get(sessionCachePath + "/" + oldCC)
	if err != nil {
		return // 옮길 것이 없다. 캐시가 없는 것은 결함이 아니다
	}
	if err := a.cli.Cache.Put(sessionCachePath+"/"+newCC, ent.Body, a.now()); err != nil {
		a.log.Warn("세션 캐시 키 이전 실패", "error", err.Error())
	}
}

// Rekey 는 /clear·compact 로 갈린 대화의 새 cc 를 카드에 반영한다.
//
// ★ a.cli.do 를 쓴다 — a.cli.Write 가 아니다. Write 는 JudgeOffline·IdempotencyStable 을 거치는데
// 둘 다 "rekey" 를 모르는 명령으로 보고 "정책이 정의되어 있지 않다"로 거절한다(offline.go 의
// default 갈래). 그러면 서버가 안 닿을 때마다 이 호출이 실패한다. 그리고 rekey 는 애초에
// 오프라인 큐에 쌓을 일이 아니다 — 다음 SessionStart 훅이 어차피 다시 시도하고, 그때는
// 그 시점의 cc 가 맞다. 낡은 rekey 를 나중에 재생하면 오히려 틀린 값을 심는다.
func (a *App) Rekey(ctx context.Context, sessionID, cc string) (model.Session, error) {
	raw, _, err := a.cli.do(ctx, "POST", "/api/v1/sessions/"+urlPath(sessionID)+"/rekey",
		rekeyReq{CCSessionID: cc}, FreshKey(a.cli.Session))
	if err != nil {
		return model.Session{}, err
	}
	var out model.Session
	if uerr := json.Unmarshal(raw, &out); uerr != nil {
		return model.Session{}, fmt.Errorf("rekey 응답 해석 실패: %w", uerr)
	}
	return out, nil
}

// CloseSession 은 카드를 done 으로 내린다. **관측이지 판정이 아니다** —
// 사람이(또는 /clear 가) "이 세션은 끝났다"고 말해 준 것을 적는 것뿐이다.
// 무응답·나이·pid 에서 죽음을 추론하는 자리는 이 도구 어디에도 없다.
//
// ★ a.cli.do 를 쓴다 — Rekey 와 같은 이유(offline.go 의 정책표가 모르는 명령을 거절한다)에
// 하나가 더 있다. **닫기는 지금의 사실이지 나중에 재생할 사실이 아니다.** 오프라인 큐에
// 쌓아 두면 그 사이 되살아나 일하고 있는 세션을 나중에 다시 죽인다.
// 서버가 안 닿으면 그 사실을 그대로 올린다 — 조용히 성공한 척하지 않는다.
func (a *App) CloseSession(ctx context.Context, sessionID, why string) (model.Session, error) {
	raw, _, err := a.cli.do(ctx, "PATCH", "/api/v1/sessions/"+urlPath(sessionID),
		patchStateReq{State: string(model.SessionDone), Why: why}, FreshKey(a.cli.Session))
	if err != nil {
		return model.Session{}, err
	}
	var out struct {
		Session model.Session `json:"session"`
	}
	if uerr := json.Unmarshal(raw, &out); uerr != nil {
		return model.Session{}, fmt.Errorf("세션 닫기 응답 해석 실패: %w", uerr)
	}
	return out.Session, nil
}

// FindSession 은 이 좌표의 세션을 **찾기만** 한다. 없으면 오류다(만들지 않는다).
//
// ★ Client 에 새 메서드를 안 만든다 — 범용 Read 가 캐시·열화까지 이미 갖고 있고,
// 같은 갈래를 둘로 만들면 한쪽만 고칠 때 조용히 어긋난다.
func (a *App) FindSession(ctx context.Context, ccSession string) (model.Session, error) {
	q := url.Values{
		"machine":  {a.machine},
		"worktree": {a.proj.Worktree},
		"cc":       {ccSession},
	}
	res, err := a.cli.Read(ctx, "/api/v1/sessions?"+q.Encode())
	if err != nil {
		return model.Session{}, err
	}
	var body struct {
		Session model.Session `json:"session"`
	}
	if uerr := json.Unmarshal(res.Body, &body); uerr != nil {
		return model.Session{}, fmt.Errorf("세션 조회 응답 해석 실패: %w", uerr)
	}
	return body.Session, nil
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
	// ★ 스큐 배너와 **따로** 붙인다. 계약 버전이 같아도 판 나이는 갈릴 수 있고,
	// 실제로 그 구간에서 pick 응답의 축 하나가 통째로 사라진 채 아무 신호도 안 났다.
	// 둘을 한 문장으로 접으면 api_version 이 같은 그 구간이 다시 침묵한다.
	if v := buildinfo.VintageBanner(buildinfo.Self(), h.Build); v != "" {
		b = strings.TrimSpace(b + "\n" + v)
	}
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
