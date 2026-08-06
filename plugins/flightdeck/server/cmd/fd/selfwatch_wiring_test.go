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
