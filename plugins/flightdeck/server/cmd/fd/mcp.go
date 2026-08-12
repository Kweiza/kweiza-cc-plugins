package main

import (
	"context"
	"io"
	"log/slog"
	"strings"

	"github.com/kweiza/flightdeck/internal/mcpsrv"
	"github.com/kweiza/flightdeck/internal/service"
)

// `fd mcp` — **배선만 한다.** 프로토콜과 도구 8개는 internal/mcpsrv 에 있다.
//
// ★ 이 명령은 **DB 를 열지 않는다.** 다른 서브명령과 똑같이 FD_URL·FD_TOKEN 으로
// 조정 서버에 붙는다(설계 원칙 ③: "정합성 경로는 REST, MCP 는 그 위의 얇은 껍데기").
//
// 앞선 판은 여기서 로컬 SQLite 를 열어 service 계층을 직접 꽂았다. 그러면 셋이 깨진다:
//
//  1. **알림이 통째로 안 뜬다.** SSE 허브는 internal/api 의 server 안에 있어
//     그 밖에서 일어난 쓰기는 발행 지점을 지나가지 않는다. 도구 8개가 에이전트의
//     유일한 쓰기 표면이므로(설계 §6) 실제 조정 트래픽의 대부분이 알림 축에서 사라졌다.
//  2. **같은 파일에 프로세스가 줄을 선다.** 세션이 10개면 이 프로세스도 10개이고
//     전부 `_txlock=immediate` 로 같은 DB 를 쓴다. 쓰기 주체를 서버 하나로 모으면 그 경합이 없다.
//  3. **다른 머신에서 안 된다.** 서버가 원격이면 이 명령만 자기 로컬 DB 를 보게 되고,
//     그것은 다른 세션이 하나도 없는 빈 보드다.

// MCPDBNotice 는 이 프로세스가 어느 주소를 보는지 한 줄로 만든다. 순수 함수다.
//
// 판정 축은 하나다: **토큰 없이 원격을 가리키고 있는가.** 루프백은 서버가 토큰을 면제하므로
// (설계 §6) 그 조합만 정상이고, 원격+무토큰이면 이 프로세스의 쓰기는 전부 401 로 끊긴다.
// 침묵하면 그 401 이 "서버 미도달"로 보여 원인이 주소가 아니라 네트워크로 오진된다.
func MCPDBNotice(fdURL, token string) string {
	u := strings.TrimSpace(fdURL)
	if u == "" || strings.TrimSpace(token) != "" {
		return ""
	}
	if strings.Contains(u, "127.0.0.1") || strings.Contains(u, "localhost") || strings.Contains(u, "[::1]") {
		return ""
	}
	return "FD_URL 이 원격(" + clip(u, 120) + ")인데 FD_TOKEN 이 비어 있다 — " +
		"루프백 면제는 이 주소에 안 걸리므로 쓰기가 401 로 끊긴다. " +
		"그 401 은 화면에서 '서버 미도달' 과 비슷하게 보이니 주소부터 확인해라."
}

// runMCP 는 `fd mcp` 다.
func runMCP(ctx context.Context, app *App, log *slog.Logger, stdin io.Reader, stdout io.Writer) int {
	if notice := MCPDBNotice(app.cli.URL, app.cli.Token); notice != "" {
		log.Warn("MCP 가 원격 서버를 무토큰으로 가리킨다", "route", clip(app.cli.URL, 200), "reason", notice)
	}
	log.Info("MCP 기동", "route", clip(app.cli.URL, 200), "api_version", service.APIVersion)

	// 쌓여 있던 판단을 먼저 재생한다 — 다른 서브명령과 같은 자리, 같은 이유다.
	// 재연결을 감지하는 별도 기구를 만들지 않는다(감지 기구는 자기가 안 돌 때 조용하다).
	if res := app.cli.Flush(ctx); res.Sent > 0 {
		log.Info("MCP 기동 시 아웃박스 재생", "count", res.Sent, "skipped", res.Remaining)
	}

	// ★ 프로젝트 좌표를 **여기서 넘긴다.** mcpsrv 가 스스로 풀면 경로의 마지막 성분이 되고,
	// 워크트리에서 그것은 워크트리 이름이라 유령 프로젝트가 생긴다(실물로 재현했다).
	// 옳은 규칙(git 주 저장소)은 App 이 이미 풀어 뒀으므로 그 값을 그대로 쓴다.
	//
	// 머신 id 도 같은 이유로 넘긴다. mcpsrv 가 스스로 풀면 hostname 이 되는데, 진입점은
	// 상태에 보관하는 안정 id 를 쓴다 — 그 둘이 달라 **한 세션이 보드에 카드 3장**으로 떴다.
	// 이 프로세스는 이미 App 을 들고 있으므로 값을 새로 만들 필요가 애초에 없었다.
	//
	// ★ 비콘 디렉토리도 **반드시 여기서 넘긴다.** WithBeaconDir 이 없으면 mcpsrv 는
	// "심지 않는다"로 조용히 넘어간다(그 옵션의 존재 이유 자체가 시험을 개발자의 진짜
	// 홈에서 격리하는 것이다) — 그래서 이 인자를 빠뜨리면 프로덕션에서 비콘이 영영 안 심기는데
	// 시험은 전부 초록으로 남는다. App 이 이미 사다리의 유일한 주인(BeaconDir)으로 값을 풀어
	// 뒀으므로 여기서 다시 풀지 않는다 — 두 자리에서 풀면 반드시 어긋난다(env.go 참고).
	srv := mcpsrv.New(newMCPBackend(app), log,
		mcpsrv.WithProject(app.proj.ID, app.proj.Path),
		mcpsrv.WithMachine(app.machine),
		// 워크트리도 여기서 넣는다. resolveProject 가 --show-toplevel 로 이미 푼 값이고,
		// 이 한 줄이 없으면 MCP 는 자기 cwd 를 워크트리로 봐서 훅과 3중키가 갈린다 —
		// 저장소 하위 디렉토리에서 연 창이 카드 두 장이 된다(TestHookAndMCPAgreeOnWorktreeFromSubdir).
		mcpsrv.WithWorktree(app.proj.Worktree),
		mcpsrv.WithBeaconDir(app.beaconDir))
	if err := srv.Serve(ctx, stdin, stdout); err != nil {
		log.Error("MCP 서버 종료", "error", err.Error())
		return 1
	}
	return 0
}
