package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/kweiza/flightdeck/internal/mcpsrv"
	"github.com/kweiza/flightdeck/internal/service"
	"github.com/kweiza/flightdeck/internal/store"
)

// `fd mcp` — **배선만 한다.** 프로토콜과 도구 6개는 internal/mcpsrv 에 있다.
//
// ★ mcpsrv 는 service 계층을 직접 받는다(REST 를 거치지 않는다). 그래서 이 경로는
// **DB 파일에 직접 닿을 수 있는 머신에서만** 돈다 — 서버가 다른 머신에 있으면
// 이 명령은 자기 로컬 DB 를 열게 되고, 그것은 다른 세션들이 안 보이는 빈 보드다.
// 지금은 그 사실을 **기동 로그에 남기고 진행한다**: 조용히 빈 보드를 내면
// "아무도 없다"와 "다른 DB 를 보고 있다"가 구분되지 않는다.

// MCPDBNotice 는 이 프로세스가 어느 DB 를 보는지, 그리고 그것이 FD_URL 과
// 어긋날 수 있다는 사실을 한 줄로 만든다. 순수 함수다.
//
// 판정 축은 하나다: **FD_URL 이 원격을 가리키는데 MCP 는 로컬 파일을 연다**면
// 그 둘은 다른 사실을 본다. 침묵하면 그 어긋남이 "다른 세션이 하나도 없다"로 보인다.
func MCPDBNotice(dbPath, fdURL string) string {
	u := strings.TrimSpace(fdURL)
	if u == "" || strings.Contains(u, "127.0.0.1") || strings.Contains(u, "localhost") ||
		strings.Contains(u, "[::1]") {
		return ""
	}
	return "FD_URL 이 원격(" + clip(u, 120) + ")을 가리키는데 MCP 는 로컬 DB(" +
		clip(dbPath, 200) + ")를 연다 — 이 두 표면은 서로 다른 사실을 본다. " +
		"같은 머신에서 fd serve 를 돌리고 있는 것이 아니라면 보드가 비어 보인다."
}

// runMCP 는 `fd mcp` 다.
func runMCP(ctx context.Context, env func(string) (string, bool), log *slog.Logger,
	stdin io.Reader, stdout io.Writer) int {

	home, _ := os.UserHomeDir()
	_, derr := os.Stat("/data")
	path := DefaultDBPath(env, home, derr == nil)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Error("MCP: DB 디렉토리를 만들지 못했다", "db_path", clip(path, 200), "error", err.Error())
		return 1
	}
	st, err := store.OpenWithLogger(path, log)
	if err != nil {
		// ★ 여기서 죽어도 세션은 뜬다(MCP 서버 하나가 실패할 뿐이다).
		//   그래도 사유는 남긴다 — 도구가 통째로 안 보이는 날 이 줄이 유일한 원천이다.
		log.Error("MCP: DB 를 열지 못했다", "db_path", clip(path, 200), "error", err.Error())
		return 1
	}
	defer func() {
		if cerr := st.Close(); cerr != nil {
			log.Error("MCP: DB 닫기 실패", "error", cerr.Error())
		}
	}()

	if notice := MCPDBNotice(path, envOr(env, "FD_URL", "")); notice != "" {
		log.Warn("MCP 와 CLI 가 다른 사실을 본다", "db_path", clip(path, 200), "reason", notice)
	}
	log.Info("MCP 기동", "db_path", clip(path, 200), "api_version", service.APIVersion)

	if err := mcpsrv.Run(ctx, service.New(st, log), stdin, stdout, log); err != nil {
		log.Error("MCP 서버 종료", "error", err.Error())
		return 1
	}
	return 0
}
