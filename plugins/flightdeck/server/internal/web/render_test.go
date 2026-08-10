package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
	"github.com/kweiza/flightdeck/internal/store"
)

// ★ 이 파일의 소비자 좌표계는 **렌더된 HTML 문자열**이다.
//
// 화면 모델(Page·SessionRow…)을 단정하면 "구조체에는 있는데 템플릿이 안 찍는다"를
// 원리적으로 못 본다 — 그것이 정확히 이 대시보드가 막으려는 실패(빈칸이 사실인 척하는 것)의 모양이다.
// 그래서 여기서는 구조체를 한 번도 들여다보지 않고 문자열만 단정한다.

func TestMain(m *testing.M) {
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	os.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

type fixture struct {
	t    *testing.T
	st   *store.Store
	svc  *service.Service
	h    http.Handler
	repo string
	wt   string
}

const testProject = "cp"

// fixtureOpt 는 하네스의 축을 바꾼다. 지금 있는 축은 시계 하나다.
type fixtureOpt func(*fixtureConfig)

type fixtureConfig struct{ clock func() time.Time }

// withClock 은 **서비스와 화면 양쪽에 같은 시계**를 준다.
//
// 한쪽만 주면 경과가 두 좌표계에서 계산돼 시험이 무엇을 재는지 알 수 없게 된다.
// 경과를 숫자로 단정하려면 시계가 서야 한다 — 안 그러면 전부 "방금"이라
// 대기 경과·획득 경과·신호 나이가 **같은 값을 세 번 찍어도** 시험이 초록이다.
func withClock(f func() time.Time) fixtureOpt {
	return func(c *fixtureConfig) { c.clock = f }
}

func newFixture(t *testing.T, opts ...fixtureOpt) *fixture {
	t.Helper()
	var cfg fixtureConfig
	for _, o := range opts {
		o(&cfg)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.OpenWithLogger(filepath.Join(t.TempDir(), "fd.db"), log)
	if err != nil {
		t.Fatalf("DB 열기 실패: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	var svcOpts []service.Option
	webOpts := []Option{WithLogger(log), WithRefresh(7)}
	if cfg.clock != nil {
		svcOpts = append(svcOpts, service.WithClock(cfg.clock))
		webOpts = append(webOpts, WithClock(cfg.clock))
	}
	svc := service.New(st, log, svcOpts...)
	return &fixture{
		t: t, st: st, svc: svc,
		h: New(svc, webOpts...),
	}
}

// withRepo 는 실물 git 저장소와 워크트리를 붙인다.
// 파생 축(브랜치·ahead·ref 관측)이 실제로 도는 것이 이 시험들의 전제다.
func (f *fixture) withRepo(branch string) *fixture {
	f.t.Helper()
	base, err := filepath.EvalSymlinks(f.t.TempDir())
	if err != nil {
		f.t.Fatalf("임시 경로 해석 실패: %v", err)
	}
	f.repo = filepath.Join(base, "repo")
	if err := os.MkdirAll(f.repo, 0o755); err != nil {
		f.t.Fatalf("디렉토리 생성 실패: %v", err)
	}
	f.git(f.repo, "init", "-q", "-b", "main", ".")
	if err := os.WriteFile(filepath.Join(f.repo, "README.md"), []byte("hi\n"), 0o644); err != nil {
		f.t.Fatalf("파일 쓰기 실패: %v", err)
	}
	f.git(f.repo, "add", "-A")
	f.git(f.repo, "commit", "-q", "-m", "init")
	f.wt = filepath.Join(base, "wt-"+branch)
	f.git(f.repo, "worktree", "add", "-q", "-b", branch, f.wt)
	return f
}

func (f *fixture) git(dir string, args ...string) string {
	f.t.Helper()
	full := append([]string{"-C", dir, "-c", "user.name=fd test",
		"-c", "user.email=fd@test.invalid", "-c", "commit.gpgsign=false"}, args...)
	out, err := exec.Command("git", full...).CombinedOutput()
	if err != nil {
		f.t.Fatalf("준비용 git %v 실패: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *fixture) openSession(cc, label string) model.Session {
	f.t.Helper()
	res, err := f.svc.OpenSession(context.Background(), service.OpenSessionInput{
		Project: testProject, ProjectPath: f.repo, MachineID: "m1", Hostname: "testhost",
		Worktree: f.wt, CCSessionID: cc, Label: label,
	})
	if err != nil {
		f.t.Fatalf("세션 열기 실패: %v", err)
	}
	return res.Session
}

func (f *fixture) addItem(id, title string, paths []string, after []model.After) model.Item {
	f.t.Helper()
	it, err := f.svc.AddItem(context.Background(), service.AddItemInput{
		Project: testProject, ID: id, Title: title, Body: id + " 본문",
		Paths: paths, After: after,
	})
	if err != nil {
		f.t.Fatalf("항목 등록 실패(%s): %v", id, err)
	}
	return it
}

// get 은 대시보드 한 장을 받아 온다.
func (f *fixture) get(query string) (int, string) {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/"+query, nil)
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func (f *fixture) post(path string, form url.Values) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	return rec
}

func mustContain(t *testing.T, html, want, why string) {
	t.Helper()
	if !strings.Contains(html, want) {
		t.Fatalf("HTML 에 %q 가 없다 — %s", want, why)
	}
}

func mustNotContain(t *testing.T, html, bad, why string) {
	t.Helper()
	if strings.Contains(html, bad) {
		t.Fatalf("HTML 에 %q 가 있다 — %s", bad, why)
	}
}

// ─────────────────────────────────────────────────────────────────────────────

func TestPageHasSixSectionsAndSurvivesZeroLiveSessions(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	// 프로젝트만 등록하고 세션은 연 뒤 done 으로 내려 살아 있는 세션을 0건으로 만든다.
	sess := f.openSession("cc-1", "트랙2")
	if err := f.svc.SetState(context.Background(), sess.ID, model.SessionDone, ""); err != nil {
		t.Fatalf("상태 전이 실패: %v", err)
	}

	code, html := f.get("")
	if code != http.StatusOK {
		t.Fatalf("status = %d, 기대 200\n%s", code, html)
	}
	// ① 0건이 빈칸이 아니라 문장이다.
	// ★ 섹션 ①이 선점을 필터로 쓰면서 0건의 **뜻이 바뀌었다**: 예전에는 "창 안에 신호가
	//    없다"였고 지금은 "아무도 항목을 안 쥐고 있다"다. 후자는 **정상 상태**라, 문장이
	//    그 사실을 말해야 사람이 서버 장애를 찾아 헤매지 않는다.
	//
	// ★ 셋 다 ①의 문장이므로 ① 안에서 잰다. 특히 창 표기("2시간")는 페이지 전체에 걸면
	//    아무 신호 나이·경과나 만족시킨다 — 이 픽스처가 아니라 **데이터가 바뀌는 날**
	//    조용히 거짓 초록이 되는 모양이다.
	now := nowSectionOf(t, html)
	mustContain(t, now, "잡혀 있는 작업 0건", "0건을 빈칸으로 두면 화면이 아무 말도 안 한다")
	mustContain(t, now, "서버 장애가 아니다", "0건이 정상 상태라는 것을 화면이 말해야 한다")
	mustContain(t, now, "2시간", "자른 창을 안 밝히면 다른 절의 0건 뜻이 정해지지 않는다")

	// ② 섹션 여섯이 전부 있다(그 이상도 만들지 않는다).
	// ★ 여기는 **페이지 전체가 맞다** — 재는 것이 절의 내용이 아니라 골격이고,
	//    절 하나를 잘라서는 "여섯이 다 있다"를 원리적으로 못 잰다.
	for _, h := range []string{
		"① 지금", "② 미확인 결과", "③ 큐", "④ 랜딩 이력", "⑤ 막힘", "⑥ 판단 검색",
	} {
		mustContain(t, html, h, "설계 §6 의 섹션이 빠졌다")
	}
	if n := strings.Count(html, "<section"); n != 6 {
		t.Fatalf("섹션 %d개, 기대 정확히 6개 — 설계가 정한 수다", n)
	}

	// ③ 모든 패널에 파생 표기가 붙는다. 페이지 전체가 맞다 — 세는 대상이 절 여섯의 합이다.
	if n := strings.Count(html, "(파생: "); n < 6 {
		t.Fatalf("파생 표기 %d개, 섹션마다 하나씩 최소 6개여야 한다:\n%s", n, html)
	}

	// ④ "죽었다"류 생존 판정을 쓰지 않는다.
	// ★ 페이지 전체가 맞다 — 이 금지는 특정 절의 규칙이 아니라 **화면 전체의 어휘 규율**이고,
	//    절 하나로 좁히면 나머지 다섯 절에 그 말이 새로 생겨도 안 잡힌다.
	for _, bad := range []string{"죽었다", "죽은 세션", "무갱신 경고"} {
		mustNotContain(t, html, bad, "생존 판정 어휘를 만들지 않는다(설계 §4)")
	}
}

// TestDashboardSaysWhatTheWindowCutOff 는 섹션 ①이 MCP board 와 같은 것을 침묵하지
// 않는다는 것을 단정한다. 웹 대시보드도 같은 service.BoardView 로 화면을 만들므로,
// 창 밖으로 잘린 건수를 이 표면에서만 조용히 빠뜨리면 "그런 세션이 없다"와
// "안 보여 준다"가 여기서만 구분되지 않는다.
func TestDashboardSaysWhatTheWindowCutOff(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	now := time.Now().UTC()

	// 숨은 세션 — 개시 시각을 창(기본 2시간) 밖인 3시간 전으로 되돌린다.
	// ★ 신호는 안 심는다 — openSession 은 Beat 를 안 부르므로 이 세션은 애초에
	// signal 행이 0건이다. opened_at 만으로 ListLive 의 창 판정이 완결된다.
	//
	// ★ 이 SQL 우회는 이제 **필수가 아니다.** Tx.OpenSession 이 개시 시각을 주입받으므로
	// 시계를 미는 것만으로 같은 상태가 된다(injected_clock_test.go 가 그 경로를 잠근다).
	// 그런데도 여기 남긴 이유: 이 시험이 재는 것은 창 판정이 아니라 **창 밖 건수의 표기**이고,
	// 픽스처를 주입 시계로 바꾸면 이 파일의 형제 단정들이 함께 흔들린다. 축이 다르므로
	// 이 항목에 얹지 않았다.
	hidden := f.openSession("cc-hidden", "숨은 세션")
	hiddenAt := now.Add(-3 * time.Hour).Format("2006-01-02T15:04:05.000000Z")
	if _, err := f.st.DB().Exec(`UPDATE session SET opened_at = ? WHERE id = ?`, hiddenAt, hidden.ID); err != nil {
		t.Fatalf("세션 개시 시각 되돌리기 실패: %v", err)
	}
	// 보이는 세션 하나 — 대조군.
	f.openSession("cc-visible", "보이는 세션")

	code, html := f.get("")
	if code != http.StatusOK {
		t.Fatalf("status = %d, 기대 200\n%s", code, html)
	}
	// 창 표기는 ①이 낸다(.Live.OutOfWindow). 다른 절에도 "N건"이 널려 있으므로 ① 안에서 잰다.
	mustContain(t, nowSectionOf(t, html), "창 밖 1건",
		"창 밖 건수를 안 말한다 — MCP 표면은 말하는데 웹만 침묵한다")
	// 어휘 금지는 화면 전체의 규율이라 페이지 전체가 맞다(위 시험의 ④와 같은 이유).
	for _, bad := range []string{"죽었다", "죽은 세션"} {
		mustNotContain(t, html, bad, "생존 판정 어휘를 만들지 않는다(설계 §4)")
	}
}

func TestLiveSessionShowsFourSignalAgesAndNoFootprintExplicitly(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	sess := f.openSession("cc-1", "트랙2")
	// ★ 선점을 붙인다. 섹션 ①은 선점을 든 카드만 내므로 선점이 없으면 이 시험이 재려는
	//    축(신호 다섯 병렬 표기·발자국 명시)이 화면에 아예 안 나온다. 이 시험의 의도는
	//    필터가 아니라 **카드 안의 표기**다.
	f.claimOne(sess.ID, "it-signals")
	if err := f.svc.Beat(context.Background(), sess.ID, model.SignalPrompt, nil); err != nil {
		t.Fatalf("신호 기록 실패: %v", err)
	}

	_, html := f.get("")
	// 재는 것이 전부 **① 의 카드 안 표기**다. 페이지 전체에 걸면 "mcp"·"feat" 같은 짧은
	// 문자열을 다른 절이 우연히 만족시킨다(mcp 는 ⑤의 판단 본문에도, feat 는 어느 경로
	// 문자열에도 나올 수 있다).
	now := nowSectionOf(t, html)

	// 신호 넷(+push)을 나란히, 없는 것은 "없음"으로.
	for _, label := range []string{"prompt(사람)", "tool(도구)", "mcp", "commit", "push"} {
		mustContain(t, now, label, "신호 종류를 빼면 '안 왔다'와 '이 화면이 안 본다'가 같아진다")
	}
	mustContain(t, now, "tool(도구) 없음", "안 온 신호를 0값으로 접으면 1970년에 온 신호가 된다")

	// ★ 발자국 없음을 명시한다. 커밋도 편집도 안 하는 세션은 경로 축에서 아무도 안 막고,
	//   **안 막는다는 사실이 화면에 있어야** 한다.
	mustContain(t, now, "발자국 없음", "발자국 0건을 빈칸으로 두면 '안 막는다'는 사실이 사라진다")

	// 브랜치는 실물 git 에서 파생된다(0값과 '못 읽음'을 가르는 축).
	mustContain(t, now, "feat", "워크트리 브랜치가 파생되지 않았다")
	mustNotContain(t, now, "브랜치 못 읽음", "실물 저장소인데 브랜치를 못 읽었다")
}

func TestSessionWithFootprintDoesNotSayNoFootprint(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	sess := f.openSession("cc-1", "트랙2")
	f.claimOne(sess.ID, "it-footprint") // 섹션 ①은 선점을 든 카드만 낸다
	// 훅이 주는 절대경로 발자국.
	if err := f.svc.Beat(context.Background(), sess.ID, model.SignalTool,
		[]string{filepath.Join(f.wt, "internal/web/web.go")}); err != nil {
		t.Fatalf("발자국 기록 실패: %v", err)
	}

	_, html := f.get("")
	// ★ mustNotContain 을 ① 안에서 재는 것이 형제 시험보다 더 중요하다. 페이지 전체에
	//   걸면 **다른 절이나 다른 카드**의 "발자국 없음" 하나가 이 시험을 빨갛게 만들 수
	//   있고, 그 실패는 이 카드와 아무 상관이 없다. 재려는 것은 이 카드의 표기다.
	now := nowSectionOf(t, html)
	mustContain(t, now, "internal/web/web.go", "발자국 경로가 화면에 없다")
	mustNotContain(t, now, "발자국 없음", "발자국이 있는데 없다고 했다")
}

func TestStaleSnapshotIsMarkedAndCurrentOneIsNot(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	f.openSession("cc-1", "트랙2")

	// 보드가 기본 브랜치 ref 를 관측해 보관하게 한 번 렌더한다.
	if code, _ := f.get(""); code != http.StatusOK {
		t.Fatalf("사전 렌더 실패: %d", code)
	}

	// ★ 대조의 전제를 **결과를 읽기 전에** 단정한다.
	//   현재 입력(기본 브랜치 관측)이 없으면 두 스냅숏이 모두 unknown 으로 떨어져
	//   "낡음 표시가 붙었다"는 단정이 통과해 버린다.
	ref, err := f.st.GetRefState(context.Background(), testProject, "main")
	if err != nil {
		t.Fatalf("전제 실패 — 기본 브랜치 관측이 없다: %v", err)
	}
	if ref.SHA == "" {
		t.Fatal("전제 실패 — 관측된 sha 가 비었다. 이 상태로는 낡음 대조 자체가 성립하지 않는다")
	}
	head := f.git(f.repo, "rev-parse", "HEAD")
	if ref.SHA != head {
		t.Fatalf("전제 실패 — 관측 sha %q 가 실제 HEAD %q 와 다르다", ref.SHA, head)
	}

	ctx := context.Background()
	if err := f.st.PutSnapshot(ctx, model.Snapshot{
		Project: testProject, Key: "progress.pct", Value: "62",
		Method: model.SnapshotManual, Evidence: "12파트 전수 판정 2026-07-30",
		InputDigest: "0000000000000000000000000000000000000000",
	}); err != nil {
		t.Fatalf("스냅숏 저장 실패: %v", err)
	}
	if err := f.st.PutSnapshot(ctx, model.Snapshot{
		Project: testProject, Key: "coverage.pct", Value: "41",
		Method: model.SnapshotCommand, InputDigest: head,
	}); err != nil {
		t.Fatalf("스냅숏 저장 실패: %v", err)
	}

	_, html := f.get("")
	snap := snapshotSectionOf(t, html)
	mustContain(t, snap, "progress.pct", "스냅숏이 화면에 없다")
	mustContain(t, snap, "이 숫자는 낡았다", "input_digest 가 다른데 낡음 표시가 안 붙었다")
	if n := strings.Count(snap, "이 숫자는 낡았다"); n != 1 {
		t.Fatalf("낡음 표시 %d개, 기대 1개 — 현재 입력과 같은 스냅숏에도 붙었다면 상시 점등이다", n)
	}
	mustContain(t, snap, "판정 당시 입력이 현재 입력과 같다", "현재인 스냅숏의 판정 사유가 없다")
	// 근거 없는 숫자를 못 넣게 하는 규율이 화면에도 남는다.
	mustContain(t, snap, "12파트 전수 판정", "manual 스냅숏의 근거가 화면에 없다")
}

// snapshotSectionOf 는 ④ 안의 **스냅숏 절만** 잘라낸다. laneSectionOf 와 같은 형식이다.
//
// ★ ④ 하나로 좁히는 것으로는 모자란다. ④ 안에는 표가 셋(랜딩 이력·레인·스냅숏) 있고
// 셋 다 시각과 경과를 같은 어법으로 찍는다 — 낡음 표시의 **개수**를 세는 단정이
// 특히 그렇다. 절 안에서 재라는 이 패키지의 규율은 <section> 단위가 아니라
// **한 사실을 내는 자리** 단위다.
func snapshotSectionOf(t *testing.T, html string) string {
	t.Helper()
	sec := sectionOf(t, html, "landing")
	i := strings.Index(sec, "스냅숏 — 재계산이 불가능한 수치")
	if i < 0 {
		t.Fatal("스냅숏 절이 ④ 안에 없다")
	}
	sec = sec[i:]
	if j := strings.Index(sec, "<h3"); j > 0 {
		sec = sec[:j]
	}
	return sec
}

func TestSnapshotWithoutCurrentInputIsNotShownAsCurrent(t *testing.T) {
	// git 이 아닌 디렉토리 — 기본 브랜치 관측이 생기지 않는다.
	f := newFixture(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("임시 경로 해석 실패: %v", err)
	}
	f.repo, f.wt = base, base
	f.openSession("cc-1", "트랙2")

	if err := f.st.PutSnapshot(context.Background(), model.Snapshot{
		Project: testProject, Key: "progress.pct", Value: "62",
		Method: model.SnapshotCommand, InputDigest: "abc123",
	}); err != nil {
		t.Fatalf("스냅숏 저장 실패: %v", err)
	}

	_, html := f.get("")
	snap := snapshotSectionOf(t, html)
	mustContain(t, snap, "대조할 축이 없다", "대조 못 한 것을 '현재'로 접으면 근거 없는 숫자가 근거 있는 척한다")
	mustNotContain(t, snap, "판정 당시 입력이 현재 입력과 같다", "대조하지 않았는데 같다고 했다")
	// 파생이 통째로 실패해도 화면은 산다 — 다만 침묵하지 않는다.
	// ★ 여기는 **페이지 전체가 맞다**. 재는 것이 "이 절이 말한다"가 아니라 "화면 어딘가가
	//    침묵하지 않는다"이고, 파생 실패는 절마다 붙는 pfail 템플릿이 낸다 — 어느 절이
	//    말하는지를 이 시험이 정하면 그것은 다른 사실을 잠그는 것이다.
	mustContain(t, html, "못 읽은 파생", "파생 실패 축이 화면에 없다")
}

func TestItemTitleIsEscaped(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	f.openSession("cc-1", "트랙2")
	const payload = `<script>alert(1)</script>`
	f.addItem("t5-xss", payload+" 항목", nil, nil)

	_, html := f.get("")
	// ★ 여기는 **페이지 전체여야 한다**. 이스케이프는 절의 성질이 아니라 페이지의 성질이고,
	//    한 절로 좁히는 순간 나머지 다섯 절로 새는 payload 를 이 시험이 못 본다 — 좁히기가
	//    시험을 약하게 만드는 자리다.
	mustNotContain(t, html, payload, "항목 제목이 이스케이프 없이 그대로 샜다 — 저장 XSS 다")
	mustContain(t, html, "&lt;script&gt;alert(1)&lt;/script&gt;", "제목이 이스케이프된 형태로도 안 보인다")
	// 검색 상자(질의 문자열)도 같은 축이다.
	_, html = f.get("?project=" + testProject + "&q=" + url.QueryEscape(payload))
	mustNotContain(t, html, payload, "검색어가 그대로 샜다")
}

func TestWriteFormsAreAtMostFourAndAllRequireReason(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	sess := f.openSession("cc-1", "트랙2")
	f.addItem("t5-a", "A", []string{"internal/web/"}, nil)
	// 선점 하나를 만들어 회수 폼이 실제 대상을 갖게 한다.
	if _, err := f.svc.Pick(context.Background(), service.PickInput{
		Project: testProject, SessionID: sess.ID, ItemID: "t5-a",
	}); err != nil {
		t.Fatalf("선점 실패: %v", err)
	}

	_, html := f.get("")

	// 폼은 다섯이다: Tier A 쓰기 셋 + 프로젝트 고르기 GET 하나 + 로그아웃 하나.
	// Tier B 버튼 둘은 폼이 아니라 비활성 <button> 이라 여기 안 센다.
	//
	// ★ 이 상한이 지키는 것은 **개수가 아니라 성질**이다 — 파생물에 손대는 폼이
	// 하나라도 늘면 대시보드가 다시 손 기재 저장소가 되고, 그것이 이 제품이
	// 없애려던 병목 1위다. 여유를 안 둔다: 늘리려면 이 줄을 고치면서
	// "그 폼이 무엇을 쓰는가"에 먼저 답하게 만드는 것이 이 락의 목적이다.
	// 로그아웃 폼(Task 7)이 다섯째로 들어온 것은 예외다 — 파생물을 전혀 쓰지
	// 않고 세션 쿠키만 지운다. 그래서 이 상한만 하나 올리고 아래 사유 필수
	// 개수(3)는 그대로 둔다: 로그아웃에는 사유를 물을 대상(무엇을, 왜)이 없다.
	if n := strings.Count(html, "<form"); n > 5 {
		t.Fatalf("폼 %d개 — 다섯을 넘었다. 파생물에 손대는 폼이 늘면 대시보드가 다시 손 기재 저장소가 된다", n)
	}
	// 그중 쓰기(POST)는 넷이다: Tier A 셋(선점 회수 · 항목 폐기 · 랜딩 줄 행 회수)
	// + 로그아웃 하나. 로그아웃은 Tier A 가 아니다 — 파생물이 아니라 세션을
	// 지운다. 줄 행 회수가 Tier A 인 이유는 **이 서버가 실제로 그 일을 하기
	// 때문**이다 — 레인에 자동 만료가 없어서 사람이 푸는 이 길이 유일한 탈출구다.
	if n := strings.Count(html, `method="post"`); n != 4 {
		t.Fatalf("POST 폼 %d개, 기대 4개(선점 회수·항목 폐기·랜딩 줄 행 회수·로그아웃). "+
			"남은 비활성 버튼(잡 우회 기록)은 Tier B 라 폼이 아니다", n)
	}
	// 그리고 셋 다 사유가 필수다.
	if n := strings.Count(html, `name="reason" required`); n != 3 {
		t.Fatalf("사유 필수 입력 %d개, 기대 3개 — 사유 없는 회수·폐기는 되짚을 수 없다", n)
	}
	// Tier B 버튼은 지우지 않고 비활성으로 남긴다("없다"와 "안 본다"를 가른다).
	// 각자 **자기 절**에 있어야 한다 — 페이지 어딘가에 있기만 하면 되는 것이 아니다.
	mustContain(t, sectionOf(t, html, "landing"), "레인 정지/재개(사유 필수) · Tier B",
		"④의 Tier B 버튼 자리가 사라졌다")
	mustContain(t, sectionOf(t, html, "unacked"), "잡 우회 기록(사유 필수) · Tier B",
		"②의 Tier B 버튼 자리가 사라졌다")

	// 선점이 **회수 폼의** 선택지로 올라온다.
	// ★ ① 안에서 재야 한다. 같은 `<option value="t5-a">` 가 ③의 폐기 폼에도 있어서
	//    페이지 전체에 걸면 회수 폼이 그 항목을 통째로 빠뜨려도 폐기 폼이 이 단정을
	//    혼자 만족시킨다 — 이 시험이 재려는 것은 정확히 회수 폼이다.
	mustContain(t, nowSectionOf(t, html), `<option value="t5-a">`, "회수 대상이 회수 폼에 없다")
}

func TestQueueShowsRejectionDistributionAndDependencies(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	sess := f.openSession("cc-1", "트랙2")
	f.addItem("t5-blocked", "선행이 없는 항목", nil, []model.After{{Item: "t5-ghost"}})

	// 인자 없는 pick 이 판정 원장을 남긴다(지정 선점은 안 남긴다).
	res, err := f.svc.Pick(context.Background(), service.PickInput{
		Project: testProject, SessionID: sess.ID,
	})
	if err != nil {
		t.Fatalf("추천 실패: %v", err)
	}
	// ★ 전제 단정 — 탈락 줄이 실제로 생겼는가. 안 생겼으면 아래 단정은 아무것도 안 지킨다.
	if len(res.Rejected) == 0 {
		t.Fatalf("전제 실패 — 탈락 줄이 0건이다(mode=%s reason=%s)", res.Mode, res.Reason)
	}

	_, html := f.get("")
	// 다섯 다 ③이 내는 사실이다. 항목 id 는 특히 여러 절에 나온다(①의 회수 폼·③의 표·
	// ③의 폐기 폼) — 페이지 전체에 걸면 "큐 표에 있다"가 아니라 "어딘가에 있다"를 잰다.
	queue := sectionOf(t, html, "queue")
	mustContain(t, queue, "탈락 사유 분포", "분포 표가 없다")
	mustContain(t, queue, "after-unknown", "탈락 사유 코드가 화면에 없다 — 큐가 다시 블랙박스가 된다")
	mustContain(t, queue, "t5-blocked", "탈락한 항목 id 가 없다")
	mustContain(t, queue, "항목 t5-ghost", "선행(의존)이 화면에 없다")
	mustContain(t, queue, "not-top 은 거르는 축이 아니라", "not-top 을 분포에서 뺀 사실이 화면에 없다")
}

func TestEmptyPickLedgerSaysSoInsteadOfShowingZeroDistribution(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	f.openSession("cc-1", "트랙2")

	_, html := f.get("")
	mustContain(t, sectionOf(t, html, "queue"), "큐 판정 기록이 0건이다",
		"'분포가 0'과 '판정이 한 번도 안 돌았다'는 다른 사실이다")
}

func TestBlockedPanelShowsNoteResourceAndDiskAxis(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	sess := f.openSession("cc-1", "트랙2")
	ctx := context.Background()

	if _, err := f.svc.Note(ctx, service.NoteInput{
		Project: testProject, SessionID: sess.ID, Kind: model.JudgmentBlocked,
		Title: "스테이징이 안 뜬다", Body: "이미지 반입이 실패한다 — 원인은 옛 태그다",
	}); err != nil {
		t.Fatalf("판단 저장 실패: %v", err)
	}
	if _, err := f.st.AcquireResource(ctx, testProject, "staging",
		store.Holder{SessionID: sess.ID}, time.Time{}); err != nil {
		t.Fatalf("자원 점유 실패: %v", err)
	}

	_, html := f.get("")
	// ★ "디스크"가 이 시험의 급소다. 같은 낱말이 **header 의 상단 줄**에도 있어서
	//    (dashboard.gohtml 의 `디스크: <span class="lv-…">`) 페이지 전체에 걸면
	//    ⑤에서 자원 임계 축이 통째로 사라져도 header 가 이 단정을 혼자 만족시킨다.
	//    나머지 셋도 ⑤가 내는 사실이라 같은 자리에서 잰다.
	blocked := sectionOf(t, html, "blocked")
	mustContain(t, blocked, "스테이징이 안 뜬다", "막힘 판단이 화면에 없다")
	mustContain(t, blocked, "staging", "쥐어진 자원이 화면에 없다")

	// ★ 자원 임계 축은 **두 칸**이다. 옛 단정은 `"디스크"` 하나였는데, 그 낱말이 세 자리에
	//   있어서(header 의 상단 줄 · ⑤의 라벨 · JudgeDisk 가 만든 판정 문장 "디스크 여유 …")
	//   어느 하나만 살아 있어도 초록이었다. 절로 좁히는 것만으로는 안 낫는다 — ⑤ 안에서도
	//   라벨과 값이 같은 낱말을 낸다(실측: 라벨을 "저장 공간"으로 바꿔도 시험은 초록이었다).
	//   레인의 대기·획득을 <td>/<b> 로 갈랐던 것과 같은 문법으로, 여기서는 라벨과 판정을
	//   **각각** 못박는다.
	mustContain(t, blocked, "자원 임계 — 디스크:",
		"⑤에 자원 임계 라벨이 없다 — header 의 같은 낱말이 이 자리를 대신하면 안 된다")
	mustContain(t, blocked, "디스크 여유",
		"임계 판정 문장이 없다 — 라벨만 있고 값이 비면 화면은 축이 있는 척만 한다")
	mustContain(t, blocked, "임계 경고는 자원에만 붙인다",
		"경고를 어디에 붙이는지가 화면에 없으면 상시 점등이 다시 자란다")
}

func TestJudgmentSearchRendersHitsAndSaysWhenEmpty(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	sess := f.openSession("cc-1", "트랙2")
	if _, err := f.svc.Note(context.Background(), service.NoteInput{
		Project: testProject, SessionID: sess.ID, Kind: model.JudgmentHandoff,
		Title: "batch7 랜딩", Body: "컨슈머 수렴 대기를 반입 스크립트로 옮겼다",
	}); err != nil {
		t.Fatalf("판단 저장 실패: %v", err)
	}

	// ★ ⑥ 안에서 잰다. 판단은 ⑤(막힘·요청)에도 같은 note 템플릿으로 찍히므로, 페이지
	//    전체에 걸면 검색이 아무것도 안 돌려줘도 ⑤의 목록이 이 단정을 만족시킬 수 있다.
	_, html := f.get("?project=" + testProject + "&q=" + url.QueryEscape("컨슈머"))
	mustContain(t, sectionOf(t, html, "search"), "batch7 랜딩", "검색 결과가 ⑥에 없다")

	_, html = f.get("?project=" + testProject + "&q=" + url.QueryEscape("없는말임"))
	mustContain(t, sectionOf(t, html, "search"), "검색 결과 0건",
		"'결과 없음'과 '질의가 깨져서 못 돌았음'을 같은 빈칸으로 두면 안 된다")
}

func TestUnknownProjectIs404AndNamesIt(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	f.openSession("cc-1", "트랙2")

	code, html := f.get("?project=없는프로젝트")
	if code != http.StatusNotFound {
		t.Fatalf("status = %d, 기대 404", code)
	}
	// 페이지 전체가 맞다 — 404 응답에는 절이 아예 없다(템플릿이 다르다).
	mustContain(t, html, "등록돼 있지 않다", "왜 비었는지를 말하지 않았다")
	mustContain(t, html, testProject, "고를 수 있는 프로젝트 목록이 없다")
}

func TestNoProjectsPageStillRenders(t *testing.T) {
	f := newFixture(t)
	code, html := f.get("")
	if code != http.StatusOK {
		t.Fatalf("status = %d, 기대 200", code)
	}
	// 페이지 전체가 맞다 — 프로젝트가 0건이면 {{if .HasProject}} 가 <main> 을 통째로
	// 안 내므로 절 자체가 존재하지 않는다.
	mustContain(t, html, "등록된 프로젝트가 없다", "빈 서버가 아무 말도 안 했다")
	mustContain(t, html, "<html", "페이지가 깨졌다")
}

func TestAutoRefreshHasSSEAndMetaFallback(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	f.openSession("cc-1", "트랙2")

	_, html := f.get("")
	// ★ 이 시험 전체가 **페이지 전체여야 한다**. 재는 것이 <head> 와 </main> 뒤의
	//    <script>·<style> 이라 여섯 절 중 어디에도 속하지 않는다 — 절로 좁히면
	//    sectionOf 가 이 문자열들을 원리적으로 못 본다.
	// SSE 가 있으면 SSE.
	mustContain(t, html, "new EventSource(path)", "SSE 경로가 없다")
	mustContain(t, html, `var path = "events";`, "SSE 엔드포인트가 페이지에 없다")
	// ★ **상대경로여야 한다.** 문자열이 아니라 그 성질을 단정한다 —
	// 절대경로면 리버스 프록시의 경로 접두 뒤에서 브라우저가 원점의 /events 를 찾아가
	// 구독이 조용히 실패하고, 그러면 화면은 뜨는데 영원히 안 갱신된다
	// (스트림이 안 열렸으니 메타 리프레시 폴백도 안 켜진다).
	mustNotContain(t, html, `var path = "\/`, "SSE 경로가 절대경로다 — 경로 접두 뒤에서 구독이 죽는다")
	// 없으면(스크립트가 아예 안 돌면) 메타 리프레시 폴백.
	mustContain(t, html, `<noscript><meta http-equiv="refresh" content="7">`,
		"스크립트 없이도 갱신되는 폴백이 없다")
	// 외부 의존이 없다 — 자족적이어야 한다.
	for _, bad := range []string{"http://", "https://", "<link", "cdn"} {
		mustNotContain(t, html, bad, "자족적이어야 한다(CDN·외부 폰트·외부 스타일 금지)")
	}

	// SSE 를 안 거는 배치에서도 페이지는 성립한다.
	h2 := New(f.svc, WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))), WithSSEPath(""))
	rec := httptest.NewRecorder()
	h2.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("SSE 없는 배치에서 status = %d", rec.Code)
	}
	body := rec.Body.String()
	mustContain(t, body, `var path = "";`, "SSE 를 안 거는 설정이 페이지에 안 나타났다")
	mustContain(t, body, "http-equiv=\"refresh\"", "SSE 가 없으면 메타 리프레시가 받아야 한다")
	mustContain(t, body, "① 지금", "SSE 가 없다고 페이지가 반쪽이 됐다")
}

func TestDarkAndLightBothStyled(t *testing.T) {
	f := newFixture(t).withRepo("feat")
	f.openSession("cc-1", "트랙2")
	_, html := f.get("")
	// 페이지 전체가 맞다 — <style> 은 <head> 안이라 절 밖이다.
	mustContain(t, html, "prefers-color-scheme: dark", "다크 모드 스타일이 없다")
	mustContain(t, html, "color-scheme: light dark", "라이트·다크 둘 다 선언돼야 한다")
}

// claimOne 은 항목 하나를 등록하고 그 세션에 선점시킨다.
//
// ★ 섹션 ①이 **선점을 든 카드만** 내기 때문에 필요하다. 카드 안의 표기를 재는 시험은
// 그 카드가 화면에 나오게 만들어 놓고 재야 한다 — 선점을 안 붙이면 단정이 재려는 축이
// 아니라 필터를 재게 되고, 그것은 다른 시험의 일이다.
func (f *fixture) claimOne(sessionID, itemID string) {
	f.t.Helper()
	f.addItem(itemID, itemID+" 제목", nil, nil)
	if _, err := f.svc.Pick(context.Background(), service.PickInput{
		Project: testProject, SessionID: sessionID, ItemID: itemID,
	}); err != nil {
		f.t.Fatalf("선점 실패(%s): %v", itemID, err)
	}
}
