package main

import (
	"context"
	"io"
	"log/slog"
	"strings"

	"github.com/kweiza/flightdeck/internal/mcpsrv"
	"github.com/kweiza/flightdeck/internal/service"
)

// `fd mcp` — **배선만 한다.** 프로토콜과 도구 6개는 internal/mcpsrv 에 있다.
//
// ★ 이 명령은 **DB 를 열지 않는다.** 다른 서브명령과 똑같이 FD_URL·FD_TOKEN 으로
// 조정 서버에 붙는다(설계 원칙 ③: "정합성 경로는 REST, MCP 는 그 위의 얇은 껍데기").
//
// 앞선 판은 여기서 로컬 SQLite 를 열어 service 계층을 직접 꽂았다. 그러면 셋이 깨진다:
//
//  1. **알림이 통째로 안 뜬다.** SSE 허브는 internal/api 의 server 안에 있어
//     그 밖에서 일어난 쓰기는 발행 지점을 지나가지 않는다. 도구 6개가 에이전트의
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

	if err := mcpsrv.Run(ctx, newMCPBackend(app), stdin, stdout, log); err != nil {
		log.Error("MCP 서버 종료", "error", err.Error())
		return 1
	}
	return 0
}
