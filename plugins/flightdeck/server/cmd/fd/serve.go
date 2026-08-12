package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/kweiza/flightdeck/internal/api"
	"github.com/kweiza/flightdeck/internal/service"
	"github.com/kweiza/flightdeck/internal/store"
	"github.com/kweiza/flightdeck/internal/web"
)

// `fd serve` — **배선만 한다.**
//
// REST 는 internal/api, 화면은 internal/web, 조합은 internal/service 다.
// 여기서 핸들러를 다시 쓰면 같은 판정이 두 벌이 되고, 두 벌은 반드시 표류한다(설계 원칙 ③).
// 이 파일이 하는 일은 셋뿐이다: DB 를 열고 · 두 표면을 한 주소에 붙이고 · 종료를 다룬다.

// DefaultDBPath 는 DB 파일 자리를 고른다. 순수 함수다.
//
// 컨테이너에는 /data 볼륨이 마운트돼 있고(설계 §2), 로컬에서는 홈 아래를 쓴다.
// 마지막 폴백(임시 디렉토리)은 값은 나오지만 재부팅하면 사라지므로 호출부가 그 사실을 로그에 남긴다.
func DefaultDBPath(get func(string) (string, bool), home string, dataDirExists bool) string {
	if v, ok := get("FD_DB"); ok && strings.TrimSpace(v) != "" {
		return filepath.Clean(v)
	}
	if dataDirExists {
		return "/data/fd.db"
	}
	if strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".flightdeck", "fd.db")
	}
	return filepath.Join(os.TempDir(), "flightdeck", "fd.db")
}

// PortAdvice 는 기동 실패 원인을 **처방이 붙은 한 줄**로 옮긴다. 순수 함수다.
//
// 설계 §7 은 "포트 선점 → 기동 시 확인 후 **사유를 남기고 종료**"를 요구한다.
// 사유만 남기면 컨테이너가 재시작 루프에 빠진 채 로그에 같은 줄만 쌓인다 —
// 무엇을 하면 되는지가 같은 줄에 있어야 한다.
func PortAdvice(addr string, err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "address already in use"):
		return fmt.Sprintf("%s 를 이미 다른 프로세스가 쓰고 있다 — "+
			"`ss -ltnp 'sport = :%s'` 로 점유자를 확인해 끄거나 --addr 로 다른 포트를 줘라",
			addr, portOf(addr))
	case strings.Contains(msg, "permission denied"):
		return fmt.Sprintf("%s 를 열 권한이 없다 — 1024 미만 포트는 특권이 필요하다. 7420 같은 상위 포트를 써라", addr)
	case strings.Contains(msg, "cannot assign requested address"):
		return fmt.Sprintf("%s 의 주소를 이 호스트에 붙일 수 없다 — 컨테이너 안이라면 0.0.0.0 으로 열어야 한다", addr)
	default:
		return fmt.Sprintf("%s 를 열지 못했다: %s", addr, clip(err.Error(), 300))
	}
}

func portOf(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 && i+1 < len(addr) {
		return addr[i+1:]
	}
	return addr
}

// buildHandler 는 REST 와 화면을 한 주소에 붙인다.
//
// 순서가 계약이다: /api/v1/ · /healthz · /metrics · /events 는 REST 가 받고,
// 나머지 전부는 화면이 받는다(화면이 자기 404 를 낸다).
// ★ 조립이 한 자리다. 화면은 api.Options.Fallback 으로 들어가 **같은 게이트 사슬**을 탄다.
//
// 앞선 판은 바깥 mux 에서 화면을 붙였고, 그래서 대시보드의 쓰기 둘이 인증·한도·멱등·
// 패닉복구·액세스로그·지표를 전부 우회했다(토큰을 켠 배포에서 비루프백 무인증 폐기가
// 실제로 성공했다). 조립을 두 자리로 나누면 한쪽만 잠긴다.
// serveAPIOptions 는 serve 가 api 표면에 넘길 옵션을 만든다. 순수 함수다.
//
// ★ 순수로 뽑아 둔 이유가 이 항목 자체다. 조립이 serve 본문 안에만 있으면 축 하나가
// 빠져도(예: 컨테이너 판정을 안 넘겨도) 전 스위트가 초록이고, 어긋남은 운영에서만 보인다.
// 정확히 그 모양이었다 — /healthz 는 "루프백은 통과한다"고 광고하는데 배선상 아무도
// 통과하지 못했고, 아무 시험도 그것을 안 잡았다.
//
// ★ **콜백이 아니라 감시기를 받는다.** 앞 판은 `selfUpdate func() api.SelfUpdateStatus`
// 를 받았는데, 그러면 시험이 넘길 수 있는 것이 `nil` 뿐이라 **콜백 안이 안 잠긴다** —
// 2026-08-07 실측: runServe 의 그 클로저를 `api.SelfUpdateStatus{}` 로 바꿔도 전 패키지가
// 초록이었다(self_update 가 통째로 영값이 되어 나가는 회귀가 조용히 통과한다).
// 감시기를 받으면 콜백을 만드는 책임이 이 순수 함수로 들어와 시험이 왕복을 흔들 수 있고,
// 호출부에서 축을 빠뜨리는 것은 **컴파일 에러**가 된다.
//
// ★ 이것으로 배선이 다 잠기는 것은 아니다 — 여기에 `nil` 을 명시로 넘기는 뮤테이션은
// 여전히 통과한다. 조립을 밖으로 뺄수록 안 잠긴 자리가 한 칸씩 얕아질 뿐 사라지지는
// 않는다(runServe 자신은 시험이 없다 — FD_TOKEN 도 같은 처지다). 여기서 멈추는 근거는
// **실패 모양**이다: 토큰이 끊기면 인증이 통째로 열려 시끄럽게 드러나고, self_update 가
// 끊기면 화면이 아무 말도 안 하는 **침묵**이라 이 축만 한 칸 더 뺐다.
func serveAPIOptions(token string, ratePerMinute int, log *slog.Logger, inContainer bool,
	watcher *selfWatcher, ledgerJob *ledgerBackupJob, requireTokenOnLoopback bool) api.Options {
	opt := api.Options{
		Token:         token,
		RatePerMinute: ratePerMinute,
		Log:           log,
		InContainer:   inContainer,
		// ★ 기본값(false)이 설계의 기본 동작이다 — 그것을 여기서 바꾸지 않는다.
		// 이 필드가 배선을 타야 하는 이유는 아래 플래그 주석에 있다.
		RequireTokenOnLoopback: requireTokenOnLoopback,
	}
	// ★ nil 감시기면 콜백을 안 단다. api 쪽은 SelfUpdate 가 nil 이면 그 절을 통째로
	// 빼므로(handlers_meta.go), "감시기가 없다"와 "감시기가 빈 값을 답한다"가 안 섞인다.
	if watcher != nil {
		opt.SelfUpdate = func() api.SelfUpdateStatus { return selfUpdateStatusOf(watcher.Status()) }
	}
	// 같은 규율 — nil 이면 콜백을 안 단다. api 쪽이 "배선 안 됨"을 사유로 말한다.
	if ledgerJob != nil {
		opt.LedgerBackup = func() api.LedgerBackupStatus { return ledgerBackupStatusOf(ledgerJob.State()) }
	}
	// ★ 화면 로그인 렌더러. **여기서 두 LoginView 를 잇는다** — web 이 api 를 import 하지
	// 않으므로 타입이 둘이고, 그 둘을 아는 자리는 이 조립 함수뿐이다.
	//
	// ★ nil 로 두면 실패가 **조용하다**: 서버는 뜨고 REST 도 다 도는데 브라우저에서만
	// 폼 대신 JSON 401 이 뜬다. 그 모양은 운영에서 사람이 봐야 발견되고, 정확히 그런
	// 침묵이 이 함수를 순수 함수로 뽑게 만든 사고였다(위 ★ 참고).
	opt.LoginScreen = func(w http.ResponseWriter, r *http.Request, v api.LoginView) {
		web.LoginScreen(w, r, web.LoginView{Error: v.Error, Next: v.Next, Action: v.Action})
	}
	return opt
}

func buildHandler(svc *service.Service, webH http.Handler, opt api.Options) api.Handler {
	opt.Fallback = webH
	return api.NewServer(svc, opt)
}

// noteBuild 는 이 기동이 새 실행 파일인지를 원장에 남긴다. **기동을 안 막는다.**
//
// ★ **호출 자리가 계약이다 — api.Listen 이 성공한 뒤여야 한다.** 리스너가 열리기 전에
// 부르면 포트를 이미 물린 기동도 배포를 남기고, LastDeployAt 이 한 번도 응답한 적 없는
// 바이너리의 시각을 낸다. 이 함수 안에는 그것을 막을 수단이 없다(바인드를 모른다) —
// 그래서 runServe 의 순서를 시험이 직접 잠근다(deploy_note_bind_test.go).
//
// ★ 관측을 `/proc/self/exe` 에서 한다 — 감시기의 기준값과 같은 자리다(newSelfWatcher 의 ★).
// 경로를 stat 하면 파일이 이미 교체된 뒤에는 "지금 도는 이미지"가 아니라 다음 이미지를 잰다.
//
// ★ 감시기를 안 탄다. 감시기는 컨테이너와 미지원 플랫폼에서 일찍 물러나는데(detectContainer),
// 배포는 그 두 곳에서도 일어난다 — 오히려 컨테이너에서는 이미지 갱신이 유일한 배포 경로다.
//
// ★ `id.OK` 를 반드시 본다. ExeID.String() 은 관측 실패를 `"관측 안 됨"` 이라는 **문자열**로
// 내므로, 그대로 넘기면 그것이 정체가 되어 "관측 실패 → 진짜 빌드"가 배포 한 건으로 잡힌다.
// 모르는 것은 아무 말도 안 하는 것이 맞다.
func noteBuild(ctx context.Context, st *store.Store, log *slog.Logger) {
	exe := buildIdentity(exeIDOfPath)
	if exe == "" {
		log.Debug("실행 파일 정체를 못 읽어 배포 관측을 건너뛴다")
		return
	}
	deployed, derr := st.NoteServerBuild(ctx, exe)
	switch {
	case derr != nil:
		// 침묵하지 않는다. 이 축이 조용히 죽으면 "배포 뒤"로 자르는 지표가 옛 시각에 얼어붙는다.
		log.Warn("배포 관측을 원장에 못 남겼다", "exe", exe, "error", derr.Error())
	case deployed:
		log.Info("새 실행 파일로 떴다 — 배포로 적었다", "exe", exe)
	}
}

// buildIdentity 는 관측된 실행 파일의 정체다. **관측 못 했으면 빈 문자열**이다. 순수 함수다.
//
// ★ 이것이 따로 있는 이유는 `ExeID.String()` 이 관측 실패를 `"관측 안 됨"` 이라는 **평범한
// 문자열**로 내기 때문이다. 그 값을 정체로 넘기면 원장이 그것을 하나의 빌드로 받아, 관측이
// 흔들릴 때마다 "관측 안 됨 → 진짜 빌드"가 배포 한 건씩 만든다. 빈 문자열만이 store 가
// 무시하기로 약속한 값이라(NoteServerBuild), 그 번역을 여기서 한 번에 한다.
//
// stat 을 인자로 받는 이유는 시험이 관측 실패를 만들 수 있어야 해서다 — `/proc/self/exe` 는
// 시험 안에서 항상 읽히므로 실패 갈래를 실물로는 못 만든다.
func buildIdentity(stat func(string) (ExeID, error)) string {
	id, err := stat("/proc/self/exe")
	if err != nil || !id.OK {
		return ""
	}
	return id.String()
}

// runServe 는 `fd serve` 다.
func runServe(args []string, env func(string) (string, bool), log *slog.Logger) int {
	fs := newFlagSet("serve")
	addr := fs.String("addr", envOr(env, "FD_ADDR", ":7420"), "수신 주소")
	dbPath := fs.String("db", "", "SQLite 파일 경로(비면 자동)")
	rate := fs.Int("rate-per-minute", 0, "원격 주소당 분당 요청 상한(0 이면 무제한)")
	// ★ 루프백 면제를 끄는 스위치다. **기본값은 면제 열림이고 그것을 안 바꾼다** —
	// 로컬 루프백으로 토큰 없이 붙는 세션이 이 제품의 정상 사용이라, 기본값을 뒤집으면
	// 그것들이 한꺼번에 전부 깨진다.
	//
	// ★ 이 플래그가 필요한 자리는 **리버스 프록시가 같은 호스트에 있는 배포**다.
	// 면제 판정은 RemoteAddr 이므로(auth.go 의 IsLoopback) 그 프록시를 거친 요청이
	// 전부 127.0.0.1 로 도착한다 — 토큰을 켜 뒀는데 바깥에서 오는 요청 전부가
	// 무인증으로 통과한다. 컨테이너 배포는 해당 없다: 브리지 게이트웨이가 172.x 라
	// 루프백으로 안 보인다(login.go 가 그 사실을 이미 적어 뒀다).
	//
	// ★ 환경변수를 안 만든다. 이 저장소의 불리언 설정은 전부 플래그이고
	// (migrate.go·project.go), 불리언 환경변수는 선례가 없다. 한 축을 위해 새 관례를
	// 만들면 다음 사람이 어느 쪽이 규칙인지 모른다.
	requireTokenOnLoopback := fs.Bool("require-token-on-loopback", false,
		"루프백 요청에도 토큰을 요구한다(리버스 프록시가 같은 호스트에 있으면 켜라)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	home, _ := os.UserHomeDir()
	path := *dbPath
	if strings.TrimSpace(path) == "" {
		_, derr := os.Stat("/data")
		path = DefaultDBPath(env, home, derr == nil)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Error("DB 디렉토리를 만들지 못해 기동을 중단한다",
			"db_path", clip(path, 200), "error", err.Error())
		return 1
	}

	st, err := store.OpenWithLogger(path, log)
	if err != nil {
		log.Error("DB 를 열지 못해 기동을 중단한다", "db_path", clip(path, 200), "error", err.Error())
		return 1
	}
	// ★ 자동 갱신 경로에서는 이 defer 가 **안 돈다.** syscall.Exec 는 스택째 버린다.
	// 그래도 되는 근거: exec 뒤에도 같은 프로세스라 POSIX 락이 자기 자신과 안 부딪히고,
	// 커밋된 것은 WAL 파일에 남아 새 이미지가 그대로 이어 읽는다. 잃는 것은 이 로그 한 줄이다.
	defer func() {
		if cerr := st.Close(); cerr != nil {
			log.Error("DB 닫기 실패", "error", cerr.Error())
		}
	}()

	svc := service.New(st, log)
	token := envOr(env, "FD_TOKEN", "")
	webH := web.New(svc, web.WithLogger(log))
	// ★ watcher 를 buildHandler 보다 먼저 만든다 — api.Options.SelfUpdate 콜백이
	// 감시기의 Status() 를 물어야 하므로, 조립 시점에 감시기가 이미 있어야 한다.
	watcher := newServeWatcher(log, env, home, path)
	inContainer, _ := detectContainer()
	// ★ 잡을 핸들러보다 먼저 만든다 — api.Options.LedgerBackup 콜백이 이 잡의 State() 를
	//   물으므로 조립 시점에 이미 있어야 한다(감시기와 같은 이유).
	ledgerJob := newLedgerBackupJob(log, st, LedgerOutDir(env, home, inContainer), ledgerBackupInterval)
	handler := buildHandler(svc, webH, serveAPIOptions(token, *rate, log, inContainer, watcher, ledgerJob, *requireTokenOnLoopback))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ★ 리스너를 조립 **뒤에** 연다. 열린 순간부터 backlog 가 쌓이므로 받을 준비가
	// 끝난 뒤 여는 것이 맞다.
	ln, lerr := api.Listen(ctx, *addr, log)
	if lerr != nil {
		// 처방은 여기 붙는다 — 바인드 실패를 아는 유일한 자리다.
		log.Error("서버를 띄우지 못했다", "route", clip(*addr, 120),
			"error", lerr.Error(), "reason", PortAdvice(*addr, lerr))
		return 1
	}

	// ★ **바인드 성공 뒤다.** 리스너가 열리기 전에 적으면 포트를 이미 물린 기동도
	// 배포로 남고, 그러면 LastDeployAt 이 한 번도 응답한 적 없는 바이너리의 시각을 낸다.
	// 이 순서가 계약이라 시험이 실물로 잠근다(TestServeSkipsDeployNoteWhenBindFails).
	//
	// ★ ctx 가 아니라 Background 를 준다 — 관측은 신호 컨텍스트의 수명과 무관하고,
	// SIGTERM 이 방금 왔다고 배포 관측이 잘려서는 안 된다.
	noteBuild(context.Background(), st, log)

	// ★ 판단 원장 주기 백업(설계 §7). serve 가 소유하는 티커다 — selfwatch 와 같은 모양이고,
	//   이 프로세스가 이미 그 DB 를 쥐고 있어 여는 쪽을 한 벌 더 만들 이유가 없다.
	//   ctx 로 묶여 있어 종료·자동 갱신 드레인에서 함께 선다.
	go ledgerJob.Run(ctx)

	log.Info("기동", "route", clip(ln.Addr().String(), 120), "db_path", clip(path, 200),
		"api_version", service.APIVersion, "auth_required", token != "",
		"ledger_out", clip(ledgerJob.route, 200))

	return serveWithWatcher(ctx, ln, handler, log, watcher)
}

// newServeWatcher 는 runServe 의 감시기 조립이다. 두 줄짜리인데도 **밖으로 뺀 이유는
// 시험이다.**
//
// ★ 조립부 안에 있으면 `binDir` 을 통째로 `""` 로 바꿔도 전 패키지가 초록이다(2026-08-07
// 뮤테이션 실측: `go test ./cmd/fd ./internal/api` 둘 다 ok). 그러면 self_update.uncovered
// 축은 **순수 판정(newSelfWatcher)·렌더(RenderHealth)·선(HealthzOf)이 다 잠긴 채 스위치만
// 안 잠긴** 상태가 된다 — 이 브랜치가 고친 회귀(버전이 오르는 갱신을 서버가 영영 못 본다)가
// 리팩터링 한 번에 조용히 돌아오고, DESIGN.md §7 이 "그 갈래는 /healthz 의
// self_update.uncovered 가 말한다"고 한 약속만 남는다. serveAPIOptions 를 순수로 뽑은 것,
// serveWithWatcher 를 runServe 에서 뺀 것과 **같은 규율**이다(위 두 주석).
//
// runServe 가 통째로 뮤테이션 투명한 것 자체는 이 브랜치가 만든 성질이 아니다(예: FD_TOKEN
// 을 끊어도 초록이다). 그래도 이 한 자리를 특별 취급하는 이유는 **실패 모양**이다 — 인증이
// 끊기면 표면이 시끄럽게 열리지만, 이쪽이 끊기면 실패가 구조적으로 **침묵**이고 이 필드의
// 존재 이유가 정확히 그 침묵을 깨는 것이다.
func newServeWatcher(log *slog.Logger, env func(string) (string, bool), home, dbPath string) *selfWatcher {
	// ★ 자리 계산의 주인은 BinCacheDir 하나다(app.go 의 binDir 주석) — 여기서 다시 조립하지
	// 않는다. 감시기는 이 값을 **견주기만** 한다: 자기 실행 파일이 런처가 소스 지문으로 이름
	// 붙인 자리에 있으면, 플러그인 버전이 오르는 갱신은 이 자리를 영영 안 덮는다.
	binDir, _ := BinCacheDir(env, home)
	return newSelfWatcher(log, dbPath, binDir)
}

// selfUpdateStatusOf 는 감시기의 상태를 API 표면의 모양으로 옮긴다. 순수 함수다.
//
// ★ **클로저 안에 있으면 이 변환이 통째로 뮤테이션 투명하다.** 2026-08-07 실측:
// 클로저에서 `Uncovered: st.Uncovered` 한 줄을 지워도 `go test ./cmd/fd ./internal/api`
// 가 둘 다 ok 였다. 조립(newServeWatcher)과 선 넘기(api.HealthzOf)와 화면(RenderHealth)은
// 각자 잠겨 있었는데, **그 셋을 잇는 이 변환만** 아무 시험에도 안 걸렸다. 그러면 판정은
// 살아 있고 값만 안 도착하는 상태가 되고, /healthz 는 다시 `watching=true` 하나만 낸다 —
// 이 브랜치가 없앤 무증상 회귀 그대로다. 밖으로 뺀 이유가 그것 하나다(newServeWatcher ·
// serveAPIOptions · serveWithWatcher 와 **같은 규율**).
//
// ★ LastAt 이 이 함수의 유일한 비자명 갈래다: cmd/fd 는 time.Time(제로값 = 시도 없음),
// api 는 *time.Time(nil = 시도 없음)이다. IsZero() 로 가른다 — 값 그대로 &st.LastAt 을
// 넘기면 "시도 없음"이 유효한 시각으로 실려 나가고, api 쪽 omitempty 는 nil 만 보므로
// 그 거짓을 못 걸러낸다. 부재와 제로를 가르는 방어가 이 세 줄뿐이라는 뜻이다
// (api.TestHealthzOmitsLastAtWhenNoAttemptEver 는 nil 을 직접 받아 omitempty 만 잰다).
//
// 필드를 걸러내지 않는다 — 이 선의 판정은 전부 상류(selfUpdateStatus)에 살고, 여기서
// 다시 고르면 같은 판정이 두 벌이 된다. 그래서 시험도 "필드가 하나라도 안 실리면
// 빨간불"로 짜여 있다(serve_test.go).
func selfUpdateStatusOf(st selfUpdateStatus) api.SelfUpdateStatus {
	out := api.SelfUpdateStatus{
		Watching: st.Watching, Reason: st.Reason, Stalled: st.Stalled,
		Uncovered: st.Uncovered,
		From:      st.From, To: st.To, Outcome: st.Outcome, Detail: st.Detail,
	}
	if !st.LastAt.IsZero() {
		at := st.LastAt
		out.LastAt = &at
	}
	return out
}

// serveWithWatcher 는 REST 서버를 감시기와 함께 돌린다. `runServe` 가 이것을 부르는
// 얇은 껍데기다 — 실제 api.Serve·실제 goroutine 스케줄로 드레인 악수를 시험하려면
// 이 조합을 따로 부를 수 있어야 한다(serve_test.go 를 본다).
//
// ★ 감시기에게는 **자기만의 취소 손잡이**(watchCtx)를 준다. 서버 ctx 를 그대로 주면
// 드레인(= 그 ctx 취소)이 감시기 자신도 죽여서 exec 까지 못 간다.
//
// ★ 감시기 goroutine 을 **join** 한다(watchDone). drain(=close(served))이 끝났다고
// 곧바로 이 함수가 반환하면, 감시기의 exec 시도가 끝나기 전에 이 프로세스가 os.Exit
// 에 닿을 수 있다 — (a) 성공 exec 인데 그 사실이 기록되기 전에 프로세스가 죽어
// 재기동이 사람이 끈 것과 구별 안 되거나, (b) exec 실패(exit 1) 안전망이 그 실패를
// 아직 못 본 채로 통과해 버린다. <-watchDone 이 그 창을 닫는다.
//
// 유계인 근거: stopWatch() 뒤 verify 중이던 감시기는 exec.CommandContext 가 자식을
// 죽여 selfVerifyTimeout 안에 verify 가 돌아온다. drain 중이던 감시기는 served 가
// 이미 닫혀 있어 <-served 에 안 막힌다. 그 외에는 Run 이 ctx.Done() 으로 바로
// 돌아온다. 그래서 <-watchDone 은 못 매달린다.
//
// ★ 그 대가로 감시기는 **종료 의사를 스스로 알 수 없게 된다.** watchCtx 는 SIGTERM 으로
// 안 끊기고(stopWatch() 는 api.Serve 가 돌아온 **뒤에** 불린다), serveCtx 는 감시기 자신의
// 드레인으로도 끊긴다. 그래서 신호 컨텍스트인 ctx 를 그대로 읽는 술어를 따로 건네준다 —
// exec 직전 두 자리가 그것으로 묻는다.
func serveWithWatcher(ctx context.Context, ln net.Listener, h api.Handler, log *slog.Logger, w *selfWatcher) int {
	// ★ 리스너가 닫힌 뒤에도 로그에 쓸 수 있도록 먼저 잡아 둔다.
	route := ln.Addr().String()
	watchCtx, stopWatch := context.WithCancel(context.Background())
	defer stopWatch()
	serveCtx, drainServe := context.WithCancel(ctx)
	defer drainServe()

	w.shutdownRequested = func() bool { return ctx.Err() != nil }

	served := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		w.Run(watchCtx, func() {
			// ★ 드레인은 **인플라이트를 마무리한다.** api.Serve 가 요청 컨텍스트를 serveCtx
			// 에서 떼어 두었고(BaseContext), 여기서 그 ctx 를 취소하면 Serve 가 ① 수명이
			// 정해지지 않은 응답(SSE)에 종료를 알리고 ② srv.Shutdown 으로 남은 요청을
			// 기다린다. api.ShutdownGrace 를 넘긴 것만 끊기고 그때는 ERROR 가 뜬다.
			//
			// ★ 그 대가로 exec 가 **최대 그 유예만큼 늦는다.** 그 값이 자기 갱신 반응 시간의
			// 항이다(아래 selfUpdateReactionBudget). 실측으로는 수명이 정해진 요청의 최댓값이
			// 0.864초라 통상 그 만큼도 안 늦는다.
			drainServe()
			<-served // 리스너가 실제로 닫힐 때까지 기다린다 — 그 전에 exec 하면 포트가 겹친다
		})
	}()

	serveErr := api.Serve(serveCtx, ln, h, log)
	close(served) // 드레인 중인 감시기를 먼저 풀어 준다
	stopWatch()   // 감시 중이면 세운다. 드레인 중이면 served 가 이미 닫혀 있어 안 막힌다
	<-watchDone   // ★ exec 시도가 끝나기 전에는 이 프로세스가 돌아오지 않는다

	if serveErr != nil {
		// api.Serve 가 이미 원인 전문을 남겼다.
		// ★ 여기에 PortAdvice 를 안 붙인다 — 바인드는 이 함수에 들어오기 전에 이미
		// 성공했다. 이 갈래는 포트 선점이 아니라 리스너가 스스로 죽은 것이고(포트 회수·
		// fd 고갈), 거기에 "ss -ltnp 로 점유자를 확인해라"를 붙이면 사람을 엉뚱한 데로 보낸다.
		log.Error("서버가 멈춰 내려간다", "route", clip(route, 120), "error", serveErr.Error())
		return 1
	}
	// 드레인이 자동 갱신 때문이었으면 exec 가 이미 이 프로세스를 갈아치웠다.
	// 여기에 도달했다는 것은 exec 가 실패했거나 사람이 껐다는 뜻이다.
	if su := w.Status(); su.Outcome == "failed" {
		log.Error("자동 갱신이 실패해 서버가 내려간 상태다 — 재기동이 필요하다",
			"detail", clip(su.Detail, 400))
		return 1
	}
	log.Info("종료", "route", clip(route, 120))
	return 0
}

func envOr(env func(string) (string, bool), key, def string) string {
	if v, ok := env(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}
