package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withExecutable 은 이 프로세스가 **다른 자리에서 도는 것처럼** 보이게 한다.
//
// 이음매의 사유는 osExecutable(selfwatch.go)에 있다. 여기서는 그 사유 하나만 다시 적는다 —
// 시험 바이너리는 go-build 임시 자리에서 돌고 BinCacheDir 은 항상 `<자리>/bin` 을 내므로,
// 가짜 HOME 을 어떻게 줘도 "런처 자리에서 돈다"는 배치를 **진짜 os.Executable 로는 못 만든다**.
//
// 자리는 **실제로 만든다**(빈 파일). newSelfWatcher 는 /proc/self/exe 를 못 읽는 판에서
// 이 경로를 stat 해 기준값을 잡으므로, 없는 경로를 주면 리눅스 밖에서만 갈래가 달라진다.
//
// ★ **이 파일에는 t.Parallel() 을 넣지 마라.** osExecutable 은 패키지 전역이라 이 헬퍼가
// 그것을 갈아 끼우고 t.Cleanup 이 되돌린다 — 병렬로 돌면 한 시험의 Cleanup 이 다른 시험이
// 세워 둔 값을 지운다(같은 시험 안에서 ①과 ②가 순서대로 갈아 끼우는 것도 그 순서에 기댄다).
// 지금 아무도 안 쓰지만, 없는 금지는 다음 사람이 "왜 여기만 병렬이 아니지" 하며 넣는다.
func withExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("가짜 실행 파일 자리를 못 만들었다: %v", err)
	}
	if err := os.WriteFile(path, nil, 0o700); err != nil {
		t.Fatalf("가짜 실행 파일을 못 만들었다: %v", err)
	}
	prev := osExecutable
	t.Cleanup(func() { osExecutable = prev })
	osExecutable = func() (string, error) { return path, nil }
}

// ★ **조립이 감시기에 런처 자리를 실제로 먹이는가.**
//
// self_update.uncovered 는 순수 판정(newSelfWatcher)·렌더(RenderHealth)·선(HealthzOf)이
// 각각 잠겨 있는데, 그 셋을 켜는 스위치는 `serve` 가 BinCacheDir 의 답을 감시기에 넘기는
// **한 자리**뿐이었다. 2026-08-07 뮤테이션 실측: 그 인자를 `""` 로 바꿔도
// `go test ./cmd/fd ./internal/api` 가 둘 다 초록이었다 — 즉 이 브랜치가 고친 회귀(플러그인
// 버전이 오르면 새 이름의 파일이 지어져 도는 서버를 아무도 안 덮는다)가 리팩터링 한 번에
// 조용히 돌아올 수 있었고, 그 실패 모양은 **침묵**이라 운영에서도 안 보인다.
// DESIGN.md §7 은 "그 갈래는 /healthz 의 self_update.uncovered 가 말한다"고 약속한다 —
// 그 약속을 끝에서 끝까지 붙드는 시험이 이것이다.
//
// runMCP 를 그대로 태우는 TestRunMCPWiresBeaconDir 과 같은 규율이되, 진입점이 `runServe`
// 자체는 아니다: 그쪽은 포트를 열고 신호를 걸고 DB 를 연다. 그래서 조립만 newServeWatcher
// 로 뽑아 **그 함수를 그대로** 부른다 — 시험이 자기 배선을 새로 짜면 "serve.go 가 정말
// 넘기는가"라는 축을 원리적으로 못 본다.
func TestServeWatcherFeedsLauncherDirToWatcher(t *testing.T) {
	home := t.TempDir()
	env := envOf(map[string]string{"HOME": home})
	want, src := BinCacheDir(env, home)
	if want == "" {
		t.Fatalf("대조 전제가 깨졌다 — 가짜 HOME 인데 자리가 안 나왔다(%s)", src)
	}
	db := filepath.Join(home, "fd.db")

	// ── ① 런처가 지은 자리에서 도는 배치. 여기서 침묵하면 배선이 끊긴 것이다.
	//
	// 이름은 아무거나 준다 — 자리 판정은 **부모 디렉토리만** 본다(이름 규칙의 주인은 런처
	// 하나이고 그 사본을 Go 에 두지 않는다). 그래서 이 시험도 키를 한 번도 안 짓는다.
	withExecutable(t, filepath.Join(want, "fd-이름은-안-본다"))

	w := newServeWatcher(quietLogger(), env, home, db)
	st := w.Status()
	if !st.Watching {
		// 감시 자체가 안 켜지는 배치(컨테이너·비유닉스)는 이 축 앞에서 갈린다.
		t.Skipf("이 배치는 감시를 아예 안 켠다(%s) — Uncovered 는 그 뒤의 축이다", st.Reason)
	}
	if strings.TrimSpace(st.Uncovered) == "" {
		t.Fatalf("조립이 감시기에 런처 자리(%s)를 안 먹였다 — 버전이 오르는 갱신을 "+
			"이 서버가 영영 못 본다는 사실이 /healthz 까지 안 간다: %+v", want, st)
	}

	// ── ② 대조. 자리 **밖**에서 도는 배치는 침묵해야 한다.
	//
	// ★ 이것이 없으면 위 단정은 "Uncovered 가 늘 켜져 있다"로도 통과한다 — 그러면 이 시험이
	// 재는 것은 배선이 아니라 상수 하나다. 침묵을 함께 잠가야 ①이 자리를 재는 것이 된다.
	outside := filepath.Join(t.TempDir(), "fd-이름은-안-본다")
	withExecutable(t, outside)
	if st := newServeWatcher(quietLogger(), env, home, db).Status(); strings.TrimSpace(st.Uncovered) != "" {
		t.Fatalf("런처 자리(%s) 밖(%s)에서 도는데 Uncovered 가 찼다 — "+
			"조립이 자리를 견주는 것이 아니라 무조건 켜고 있다: %q", want, outside, st.Uncovered)
	}
}

// ★ **조립이 감시기의 상태를 api 표면까지 실제로 나르는가.**
//
// 위 시험은 감시기가 자리를 **받는** 것까지 잠갔고, 변환(selfUpdateStatusOf)은 serve_test.go
// 가 필드별로 잠갔다. 그런데 그 둘을 **잇는 콜백 한 자리**는 여전히 투명했다 — 2026-08-07
// 실측: runServe 의 클로저를 `api.SelfUpdateStatus{}` 로 바꿔도 `go test ./...` 전 패키지가
// 초록이었다. self_update 절이 통째로 영값으로 나가는 회귀가 조용히 통과한다는 뜻이고,
// 그때 화면은 이 브랜치 **이전과 정확히 같아진다**(watching 조차 안 나온다).
//
// serveAPIOptions 가 콜백 대신 감시기를 받게 되면서 그 자리가 이 시험의 사정권에 들어왔다.
// 조립을 밖으로 뺄수록 안 잠긴 자리가 한 칸씩 얕아질 뿐 사라지지는 않는다는 것도 그 함수
// 주석에 적어 뒀다 — 여기서 멈춘 근거는 **실패 모양이 침묵**이라는 것이다.
func TestServeAPIOptionsCarriesTheWatcherStatus(t *testing.T) {
	home := t.TempDir()
	env := envOf(map[string]string{"HOME": home})
	want, src := BinCacheDir(env, home)
	if want == "" {
		t.Fatalf("대조 전제가 깨졌다 — 가짜 HOME 인데 자리가 안 나왔다(%s)", src)
	}
	withExecutable(t, filepath.Join(want, "fd-이름은-안-본다"))

	w := newServeWatcher(quietLogger(), env, home, filepath.Join(home, "fd.db"))
	st := w.Status()
	if !st.Watching {
		t.Skipf("이 배치는 감시를 아예 안 켠다(%s) — 나르기는 그 뒤의 축이다", st.Reason)
	}

	opt := serveAPIOptions("tok", 60, quietLogger(), false, w)
	if opt.SelfUpdate == nil {
		t.Fatal("감시기를 줬는데 조립이 self_update 콜백을 안 달았다 — /healthz 에서 그 절이 통째로 빠진다")
	}
	got := opt.SelfUpdate()
	if !got.Watching {
		t.Fatalf("감시기의 상태가 api 표면까지 안 간다 — 조립이 빈 값을 물린다: %+v", got)
	}
	if strings.TrimSpace(got.Uncovered) != strings.TrimSpace(st.Uncovered) {
		t.Fatalf("Uncovered 가 조립을 건너면서 갈렸다: 감시기=%q · api=%q", st.Uncovered, got.Uncovered)
	}

	// ── 대조. 감시기가 없으면 콜백을 **안 단다.**
	//
	// ★ 이것이 없으면 위 단정은 "늘 콜백을 단다"로도 통과한다. 그리고 nil 감시기에 콜백을
	// 달면 부르는 순간 패닉이고, api 는 "감시기가 없다"와 "감시기가 빈 값을 답한다"를
	// 가를 근거를 잃는다(handlers_meta.go 는 SelfUpdate 가 nil 이면 그 절을 통째로 뺀다).
	if serveAPIOptions("tok", 60, quietLogger(), false, nil).SelfUpdate != nil {
		t.Fatal("감시기가 nil 인데 콜백을 달았다 — 부르면 패닉이고, api 가 그 절을 뺄 근거를 잃는다")
	}
}
