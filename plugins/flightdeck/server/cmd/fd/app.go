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
	"github.com/kweiza/flightdeck/internal/mcpsrv"
	"github.com/kweiza/flightdeck/internal/model"
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
	// harness 는 **선언된** 하네스다(DESIGN 「14. 하네스 축」). 빈 값은 「미상」이고
	// claude 로 접지 않는다 — 환경으로는 못 가르기 때문이다.
	harness    string
	notice     string // 도구가 스스로 못 한 것. 침묵하지 않는다
	machineSrc string // machine-id 를 읽은 자리. doctor 가 찍는다 — 값이 갈리면 여기가 원인이다
	beaconDir  string // 창 비콘 디렉토리(BeaconDir). mcp.go 가 mcpsrv.WithBeaconDir 에 그대로 넘긴다
	// beaconSrc 는 그 자리를 **고른 사유**다. machineSrc 가 선례다 — 값이 예상과 다를 때
	// "왜 저 값인가"에 답할 자리가 없으면 /proc 을 뒤지게 된다. 새 정체 경로를 더해 놓고
	// 같은 줄을 빼먹었던 것이 fd-doctor-beacon-axis 다.
	beaconSrc string
	// binDir 는 **바이너리 캐시 디렉토리**(BinCacheDir)다 — 셸 런처가 빌드 산출물을 두는
	// 그 자리와 같은 값이어야 한다. 소비자가 둘이다: hook.go 의 pruneBinCache 가 이
	// 디렉토리를 훑어 상한을 잡고, doctor 의 ExeLines 가 "지금 도는 실행 파일이 그 자리
	// 안인가"를 이것과 견준다.
	//
	// ★ **자리 계산의 주인은 BinCacheDir 하나다.** 여기서 filepath.Join(home, ".cache", …) 을
	//   다시 조립하면 같은 판단이 두 자리에 살게 되고, 그때 어긋남은 화면에 안 뜬다 —
	//   doctor 가 멀쩡한 프로세스에 "자리 밖이다"라고 조용히 거짓 경보를 낸다
	//   (client.go 의 newClient 주석이 적어 둔 규율. 이 레포는 그 사고를 세 번 겪었다).
	// ★ **디렉토리까지만이다.** 파일 이름(fd-<접은 소스 트리>)의 키 규칙은 런처가 유일한
	//   주인이고, 그 역함수를 Go 에 두지 않는다. 그래서 이 필드에 파일 이름을 붙이지 마라.
	// ★ **빈 문자열일 수 있다** — 형제들(MachineIDPath·ConfigPath·OutboxPath·BeaconDir)과
	//   다른 유일한 갈래다. HOME 도 FD_STATE_DIR 도 없으면 런처 자신이 짓기를 거절하기
	//   때문이고(공용 /tmp 에는 실행 파일을 안 놓는다 — 남이 심은 것을 exec 하게 된다),
	//   그때는 소비부도 침묵해야 한다: 훑을 자리가 없고, 견줄 자리도 없다. 안 잰 축을 잰
	//   척하지 않는다(설계 §13). 그래서 두 소비부가 각자 먼저 빈 값을 가른다.
	binDir string
	// binSrc 는 그 자리를 **고른 사유**다. beaconSrc·machineSrc 가 선례이고,
	// **binDir 이 비어도 항상 채워진다** — '자리가 없다'는 것 자체가 사유를 갖는 판정이다.
	binSrc string
	host   string
	stdin  io.Reader // note·finish 의 본문이 여기서 온다. os.Stdin 을 본문에 박으면 시험이 못 준다
	now    func() time.Time
}

func newApp(env func(string) (string, bool), log *slog.Logger, cwd string, stdin io.Reader) *App {
	home := homeDir(env)
	sd := ResolveStateDir(env, home)
	mid, midSrc, warn := MachineID(env, home)
	beaconDir, beaconSrc := BeaconDir(env, home)
	// ★ 바이너리 캐시 자리는 **여기서 한 번만** 계산한다. 소비자가 둘이라(GC · doctor 의
	// 자리 축) 각자 부르게 두면 두 답이 갈릴 수 있고, 그러면 GC 가 훑는 디렉토리와
	// doctor 가 견주는 디렉토리가 서로 다른 채로 둘 다 초록이 된다.
	binDir, binSrc := BinCacheDir(env, home)
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
	proj := resolveProject(env, cwd)
	// ★ **프로젝트 좌표를 못 푼 사실도 같은 자리로 합류시킨다.** 그것이 없으면 이 고침이
	// 「조용한 오등록」을 「조용한 무등록」으로 바꾸는 데 그친다 — 셋 중 둘이 침묵하기 때문이다:
	// 훅은 서버 거절을 log.Warn 으로 삼키고(훅이 세션을 막으면 안 된다), MCP 는 도구를
	// 부르기 전까지 아무 말이 없다. 사람이 찾아가지 않고도 보는 표면은 SessionStart 배너뿐이다.
	//
	// notice 를 고른 이유는 그 셋을 이미 한 자리로 모으고 있어서다 — 배너(hook.go) ·
	// `fd doctor`(cmds.go) · 기동 로그(main.go). 새 통로를 안 판다.
	//
	// 사유(proj.Detail)를 **그대로 나른다.** 「git 을 못 읽었다」의 실물이 거기 있고, 여기서
	// 요약하면 두 자리가 같은 사실을 다르게 말하게 된다.
	if strings.TrimSpace(proj.ID) == "" {
		warn = strings.TrimSpace(warn + " 프로젝트 좌표를 못 풀어 이 실행은 프로젝트에 귀속되지 않는다(" +
			clip(proj.Detail, 300) + "). git 저장소 안에서 부르거나 FD_PROJECT 로 명시해라 — 지어내지 않는다.")
	}
	a := &App{
		env: env, log: log, sd: sd,
		cli:        cli,
		proj:       proj,
		machine:    mid,
		machineSrc: midSrc,
		beaconDir:  beaconDir,
		beaconSrc:  beaconSrc,
		binDir:     binDir,
		binSrc:     binSrc,
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
	cc, _ := a.sessionAxis(fromHook, "이 명령")
	return cc
}

// sessionAxis 는 이 실행의 세션 id 와, **못 쓰는 경우의 사유**다.
//
// ★ 사유를 함께 내는 이유: 세션 축이 안 서는 경우가 둘인데 **문장이 서로 다르다.**
// 못 읽은 것(탐지가 깨졌다)과 둘이 동시에 읽힌 것(어느 창인지 모른다)은 다른 일이고,
// 후자에 "못 읽었다"를 찍으면 사람을 **없는 결함으로** 보낸다 — 축은 읽혔고, 둘인 것이 문제다.
//
// ★ **훑기를 여기서 다시 짜지 마라.** 옛 코드가 [EnvSessionID, EnvCodexSessionID] 를
// 하드코딩으로 순회하는 사본을 들고 있었고, 그래서 mcpsrv 에 관문을 세워도 맨손 CLI 는
// 그대로 뚫렸다. identity.go 가 "이 파일이 유일한 정체의 원천"이라 선언했으므로
// 판정은 mcpsrv.ProbeSession 하나에서만 나온다.
func (a *App) sessionAxis(fromHook, what string) (cc, why string) {
	if s := strings.TrimSpace(fromHook); s != "" {
		// 훅 페이로드의 session_id 는 **관측이 아니라 배달**이다 — 환경이 둘이든 셋이든
		// 이 값이 정본이므로 부딪힘 판정에 걸리지 않는다.
		return s, ""
	}
	// ★ 환경 갈래는 **하네스별로 이름이 다르다**(DESIGN 「14. 하네스 축」).
	// 선언이 있으면 그 이름만 본다 — 선언이 관측을 이긴다.
	if name := mcpsrv.SessionEnvFor(a.harness); name != "" {
		if v, ok := a.env(name); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v), ""
		}
		return "", ""
	}
	// 선언이 없다 — 훑되, 몇 개가 찼는지를 함께 본다.
	sid, picked, found := mcpsrv.ProbeSession(a.env)
	if r := mcpsrv.HarnessConflictReason(found, what, picked); r != "" {
		return "", r
	}
	return sid, ""
}

// sessionEnvName 은 이 실행이 세션 id 를 찾는 환경변수 이름이다.
//
// ★ 안내 문구가 이 값을 불러야 한다. "CLAUDE_CODE_SESSION_ID 를 못 읽었다" 를 박아 두면
// codex 세션에서 **없는 변수를 가리키는 안내**가 나오고, 그것은 이 도구가 안 하기로 한 일이다.
func (a *App) sessionEnvName() string {
	if name := mcpsrv.SessionEnvFor(a.harness); name != "" {
		return name
	}
	return mcpsrv.EnvSessionID
}

// tail 은 CLI 응답 꼬리다 — **미확인 알림과 정체 사유.**
//
// ★ 왜 CLI 에도 있어야 하나. Tail 을 채우는 자리가 mcpsrv 하나뿐이라, MCP 표면이 없는
// 창(codex 는 오늘 훅 전용이다)에서는 **남이 남긴 ask·blocked 가 영영 안 보인다.**
// 조율은 그 알림을 읽는 데서 시작하므로, 꼬리가 없다는 것은 그 창이 조율 밖에 있다는
// 뜻이다 — 보드에는 멀쩡히 떠 있으면서.
//
// 렌더는 mcpsrv.RenderTail 을 **그대로** 쓴다. 사본을 만들면 두 화면이 같은 사실을
// 다르게 말하게 되고, 그 어긋남은 아무 시험도 안 깬다.
func (a *App) tail(ctx context.Context) string {
	in := mcpsrv.TailInput{Banner: a.notice, Now: a.now()}
	notes, err := a.recentNotes(ctx)
	if err != nil {
		// 못 읽었으면 **못 읽었다고 한다.** 조용히 비우면 "알림 없음"과 구별되지 않는다.
		in.NotesError = clip(err.Error(), 300)
	} else {
		in.Notes, in.NotesObserved = notes, true
	}
	return mcpsrv.RenderTail(in)
}

// printTail 은 본문 뒤에 꼬리를 잇는다. 꼬리가 비면 아무것도 안 찍는다.
func (a *App) printTail(ctx context.Context, out io.Writer) {
	if t := strings.TrimSpace(a.tail(ctx)); t != "" {
		fmt.Fprintln(out, "\n── 꼬리 ──")
		fmt.Fprintln(out, t)
	}
}

// recentNotes 는 **다른** 세션이 남긴 최근 ask·blocked 다.
//
// 저장 계층을 직접 안 읽는다 — 클라이언트는 서버 머신이 아닐 수 있다(설계 원칙 ③).
// 자기 세션 것을 빼는 것과 상한은 mcpsrv.FilterNotes 가 쥔다: 같은 판정을 여기 다시
// 쓰면 두 벌이 표류한다.
func (a *App) recentNotes(ctx context.Context) ([]model.Judgment, error) {
	if strings.TrimSpace(a.proj.ID) == "" {
		return nil, nil
	}
	rr, err := a.cli.Read(ctx, fmt.Sprintf("/api/v1/notices?project=%s&limit=20", urlValue(a.proj.ID)))
	if err != nil {
		return nil, err
	}
	var v struct {
		Notes []model.Judgment `json:"notes"`
	}
	if uerr := json.Unmarshal(rr.Body, &v); uerr != nil {
		return nil, fmt.Errorf("알림 응답 해석 실패: %w", uerr)
	}
	return mcpsrv.FilterNotes(v.Notes, a.cli.Session, mcpsrv.TailNoteLimit), nil
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
		Harness: a.harness,
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
		a.adoptResolvedProject(res.Project.ID)
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
	// ★ 캐시 갈래도 채택한다 — 캐시된 raw 는 **그때 서버가 실제로 낸** res.Project 를
	// 그대로 담고 있다(Put 이 서버 응답 그대로를 저장한다, 위 참고). 여기서 안 채택하면
	// 오프라인 동안 나가는 큐잉 쓰기가 여전히 미등록 이름을 싣고, 서버가 살아난 뒤
	// 재생될 때 똑같은 FK 위반으로 죽는다.
	a.adoptResolvedProject(res.Project.ID)
	return res, true, nil
}

// adoptResolvedProject 는 서버가 **실제로 연 프로젝트**를 이 프로세스의 좌표로 채택한다.
//
// ★ I-1(최종 리뷰). e81831b 뒤로 세션 정체(machine·worktree·cc 3중키)가 이미 프로젝트
// P 로 열려 있는 상태에서 다른(미등록) 프로젝트 이름 Q 로 다시 열면, 서버는 세션을
// P 로 **정상 재개**시키고 Q 를 등록하지 않는다(internal/service/session.go 의
// "자동 등록 전에 3중키 세션을 본다") — 그것이 고아 프로젝트를 막은 방법이다. 문제는
// 응답의 res.Project 가 P 인데 이 프로세스의 a.proj.ID 는 여전히 Q 로 남는다는
// 것이었다. 그 뒤 이 프로세스 안에서 나가는 모든 쓰기(note·add·pick·finish·alloc·
// after cut·move·lane/claim release — 전부 a.sessionID 를 거쳐 결국 이 함수를
// 부른 뒤 a.proj.ID 를 그대로 싣는다, cmds.go)가 미등록 이름 Q 로 나가 FK 위반으로
// 죽었다. 실측: 서버는 res.Project.ID="real" 을 냈는데 뒤이은 note 쓰기가
// project="지어낸이름" 으로 나가 "FOREIGN KEY constraint failed"를 받았다 —
// 판단을 못 남기는 세션이 된 것이다.
//
// ★ **여기 한 곳에서 고치는 이유.** a.proj.ID 를 읽는 쓰기 호출부가 cmds.go 에
// 여덟 곳 이상(note·add·pick·finish·alloc·next·lane·claim·after cut·move…) +
// hook.go 의 hookPreCompact 하나다. 그 자리마다 "응답을 보고 좌표를 고쳐라"를
// 각자 심으면 새 쓰기 명령이 늘 때마다 또 잊을 수 있다 — 이 결함 자체가 그 모양이다
// (hookSessionStart 가 res.Project 를 한 번도 안 본 것). 반면 **모든 경로가
// a.OpenSession → a.openSession 을 거친다**(a.sessionID 도 내부에서 a.OpenSession 을
// 부른다, cmds.go) — 그래서 그 응답을 해석하는 이 자리 하나만 고치면 전부 닫힌다.
//
// ★ **MCP 경로에도 안전하다.** mcpBackend.OpenSession 은 이 함수를 자기 좌표
// (mcpsrv.ResolveIdentity 가 관측한 것)로 부르는데, 그 뒤 MCP 의 note·add 등은
// a.proj.ID 를 안 읽는다 — mcpbackend.go 의 각 메서드가 service.XxxInput.Project 를
// 호출자(mcpsrv)로부터 직접 받는다. 유일하게 a.proj.ID 를 읽는 MCP 쪽 자리는
// mcp.go 의 WithProject(app.proj.ID, …) 인데 그것은 **서버 기동 시 한 번**만 읽고,
// OpenSession 은 그보다 항상 뒤에 불린다 — 그래서 여기서 값을 바꿔도 이미 넘긴
// 옵션은 안 흔들린다. 무조건 채택해도 되는 이유다.
func (a *App) adoptResolvedProject(serverProject string) {
	serverProject = strings.TrimSpace(serverProject)
	if serverProject == "" || serverProject == a.proj.ID {
		return
	}
	a.log.Warn("요청한 프로젝트가 등록돼 있지 않아 서버가 다른 프로젝트로 세션을 열었다 — "+
		"이 프로세스의 후속 쓰기는 그 프로젝트를 쓴다",
		"requested", a.proj.ID, "using", serverProject)
	a.proj.ID = serverProject
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

// SessionByID 는 카드 한 장을 **id 로** 읽는다. 3중키 좌표를 안 보낸다.
//
// ★ 그것이 이 메서드의 존재 이유다. FindSession 은 (machine, worktree, cc) 셋을 보내므로
// 그 셋 중 하나라도 이 프로세스에서 다르게 풀리면 다른 카드를 가리킨다 — 그리고 cc 는
// rekey 로 실제로 갈린다. id 를 보내면 해석할 좌표가 없어 세 입구가 갈려도 같은 카드다.
//
// FindSession 과 같은 자리를 쓴다(`GET /sessions?id=`) — 범용 Read 가 캐시·열화를
// 이미 갖고 있고, 같은 갈래를 둘로 만들면 한쪽만 고칠 때 조용히 어긋난다.
// 선점 목록을 **함께** 받는다 — 카드를 닫을지는 그것으로 판정하고, 따로 물으면
// 두 호출 사이가 창이 된다(OpenSession 갈래가 res.Claims 를 쓰는 것과 같은 어법).
//
// ★ claims 를 **포인터로** 받는다. 값으로 받으면 「선점 0건」과 「이 서버는 선점을
// 안 센다」가 둘 다 빈 슬라이스라 구분되지 않고, 그 구분이 없으면 이 입구를 모르는
// 낡은 서버에 붙은 날 **선점을 든 카드가 조용히 닫힌다.** 안 센 응답은 거절이다.
func (a *App) SessionByID(ctx context.Context, id string) (model.Session, []string, error) {
	res, err := a.cli.Read(ctx, "/api/v1/sessions?"+url.Values{"id": {id}}.Encode())
	if err != nil {
		return model.Session{}, nil, err
	}
	var body struct {
		Session model.Session `json:"session"`
		Claims  *[]string     `json:"claims"`
	}
	if uerr := json.Unmarshal(res.Body, &body); uerr != nil {
		return model.Session{}, nil, fmt.Errorf("세션 조회 응답 해석 실패: %w", uerr)
	}
	if body.Claims == nil {
		return model.Session{}, nil, fmt.Errorf(
			"서버가 이 카드의 선점을 안 셌다(claims 가 응답에 없다) — 이 응답으로는 닫아도 되는지 판정할 수 없다. " +
				"서버가 이 입구를 모르는 낡은 버전이다: fd update")
	}
	return body.Session, *body.Claims, nil
}

// clientAPIVersion 은 이 바이너리가 아는 계약 버전이다.
// 서버와 같은 상수를 쓴다 — 두 벌로 두면 스큐 배너가 자기 자신을 못 본다.
const clientAPIVersion = service.APIVersion

// BoardQuery 는 `fd status` 가 보드에 거는 선택 축이다.
//
// ★ 구조체로 받는 이유: 인자가 둘 다 «있으면 바꾸고 없으면 그대로»라 위치 인자로 두면
// 호출부가 `a.Board(ctx, self, "", false)` 처럼 뜻 없는 제로값을 두 개 적게 된다.
type BoardQuery struct {
	// Project 는 볼 프로젝트다. 비면 이 세션의 것이다 — 값이 있으면 서버가 워크스페이스
	// 명부로 검증한다(명부 밖 이름은 거절이다: service.GateTargetProject).
	Project string
	// Workspace 는 형제 프로젝트의 한 줄 요약을 함께 받는다.
	Workspace bool
}

// TargetProject 는 이 호출이 쓸 프로젝트다 — 인자가 이기고, 비면 이 세션의 것이다.
//
// ★ **검증하지 않는다.** 명부는 서버에 있고 이 프로세스는 그것을 안 본다. 여기서
// 아는 이름 목록의 사본을 들면 그것이 곧 표류한다 — mcpsrv.Server.target 과 같은 판정이고
// 같은 이유다. 거절 문면은 서버의 관문 하나가 낸다.
func (a *App) TargetProject(explicit string) string {
	if p := strings.TrimSpace(explicit); p != "" {
		return p
	}
	return a.proj.ID
}

// Board 는 보드 한 장이다. 미도달이면 캐시 + 배너다.
//
// 경로가 `/api/v1/dashboard.json` 인 이유: 설계 §6 의 REST 표에 board 라는 표면이 없다.
// 화면 한 장분의 값을 내는 그 엔드포인트가 보드의 정본이다.
func (a *App) Board(ctx context.Context, self string, opt BoardQuery) (v service.BoardView, banner string, err error) {
	path := fmt.Sprintf("/api/v1/dashboard.json?project=%s&self=%s&workspace=%t",
		urlValue(a.TargetProject(opt.Project)), urlValue(self), opt.Workspace)
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
