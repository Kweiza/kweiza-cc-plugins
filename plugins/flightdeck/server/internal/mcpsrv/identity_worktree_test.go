package mcpsrv

import (
	"strings"
	"testing"
)

// 주입된 워크트리가 cwd 를 이겨야 한다.
//
// 이 계층이 스스로 푸는 규칙(워크트리 = cwd)은 저장소 하위 디렉토리에서 훅의
// `--show-toplevel` 과 갈리고, 3중키의 둘째 축이 갈리면 한 창이 카드 두 장으로 열린다.
// WithProject·WithMachine 이 먼저 같은 이유로 생겼다 — 이것은 그 셋째다.
func TestWithWorktreeBeatsCwd(t *testing.T) {
	const root = "/repo"
	const sub = "/repo/plugins/flightdeck/server"

	srv := New(nil, discard(),
		WithEnv(env(map[string]string{
			EnvSessionID:  "cc-1",
			EnvProjectDir: root,
		})),
		WithCwd(sub, nil),
		WithHostname("host", nil),
		WithWorktree(root),
	)
	id := srv.Identity()
	if id.Worktree != root {
		t.Errorf("주입이 안 이겼다 — 워크트리 %q (기대 %q)", id.Worktree, root)
	}
	// cwd 관측 자체는 지운 것이 아니다. 지우면 "무엇이 갈렸나"를 doctor 가 못 잰다.
	if id.Cwd != sub {
		t.Errorf("cwd 관측이 사라졌다: %q (기대 %q)", id.Cwd, sub)
	}
	for _, w := range id.Warnings {
		if strings.Contains(w, "워크트리를 cwd 로 정했다") {
			t.Errorf("주입했는데 폴백 경고가 남았다: %q", w)
		}
	}
}

// 주입이 없으면 옛 규칙(cwd)으로 떨어지되 **그 사실을 남겨야 한다.**
//
// ★ 침묵하면 "주입이 끊겼다"와 "원래 그렇다"가 구분되지 않는다. 그 침묵이 머신 축에서
// 이 결함을 오래 살렸고(mcpsrv.go 의 WithMachine 머리말), 워크트리 축도 같은 이유로 오래 살았다.
func TestWithoutWorktreeFallsBackToCwdAndSaysSo(t *testing.T) {
	const sub = "/repo/plugins/flightdeck/server"

	srv := New(nil, discard(),
		WithEnv(env(map[string]string{
			EnvSessionID:  "cc-1",
			EnvProjectDir: "/repo",
		})),
		WithCwd(sub, nil),
		WithHostname("host", nil),
	)
	id := srv.Identity()
	if id.Worktree != sub {
		t.Errorf("주입이 없으면 cwd 여야 한다 — 워크트리 %q (기대 %q)", id.Worktree, sub)
	}
	found := false
	for _, w := range id.Warnings {
		if strings.Contains(w, "워크트리를 cwd 로 정했다") {
			found = true
		}
	}
	if !found {
		t.Errorf("폴백했는데 그 사실을 안 남겼다 — 경고: %v", id.Warnings)
	}
}

// 빈 주입은 주입이 아니다 — 지어낸 좌표로 카드를 여느니 cwd 로 떨어지고 말하는 쪽이 낫다.
func TestWithWorktreeIgnoresBlank(t *testing.T) {
	const sub = "/repo/plugins/flightdeck/server"
	for _, blank := range []string{"", "   ", "\t"} {
		srv := New(nil, discard(),
			WithEnv(env(map[string]string{
				EnvSessionID:  "cc-1",
				EnvProjectDir: "/repo",
			})),
			WithCwd(sub, nil),
			WithHostname("host", nil),
			WithWorktree(blank),
		)
		if got := srv.Identity().Worktree; got != sub {
			t.Errorf("빈 주입(%q)이 좌표를 갈아엎었다: %q (기대 %q)", blank, got, sub)
		}
	}
}
