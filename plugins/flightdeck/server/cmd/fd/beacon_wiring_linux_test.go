//go:build linux

// ★ 이 파일이 리눅스 전용인 이유. 아래 시험은 runMCP 가 **비콘 파일을 실제로 남기는지**를
// 단정하는데, 심기는 window.StartedOf 로 부모의 시작 시각을 읽어야 성립하고 그 함수는
// 리눅스 밖에서 ErrUnsupported 를 낸다(internal/window/proc_other.go). 즉 이 기능 자체가
// 리눅스에서만 도는 것이라, 다른 플랫폼에서 t.Skip 으로 초록을 내는 대신 **빌드 대상에서
// 뺀다** — 스킵은 "돌았는데 통과했다"와 구분이 안 가서 초록의 뜻을 흐린다
// (hook_beacon_test.go 가 같은 판단을 같은 이유로 한다).
//
// 같은 파일에 있던 나머지 둘(사다리 위임 · rekey 전송 경로)은 비콘 파일에 기대지 않으므로
// beacon_wiring_test.go 에 그대로 두고 모든 플랫폼에서 돌린다.
package main

import (
	"context"
	"os"
	"strings"
	"testing"
)

// mcp.go 가 mcpsrv.New 에 WithBeaconDir 을 실제로 넘기는지 확인한다.
//
// ⚠ 이 시험이 없으면 mcp.go 에서 그 인자를 지워도 전 시험이 초록으로 남는다 — T8 은
// "WithBeaconDir 이 없으면 심지 않는다"로 설계했고(시험이 개발자의 진짜 홈에 못 쓰게 막는
// 장치다), 그 결과 옵션 누락이 컴파일 오류도 런타임 오류도 아닌 **조용한 기능 부재**가 된다.
//
// 그래서 시험이 mcpsrv.New 를 직접 조립하지 않고 **운영 진입점(runMCP)을 그대로 태운다** —
// 시험이 만든 배선에 시험이 단정하면 "mcp.go 가 정말 주입하는가"라는 축을 원리적으로 못 본다
// (TestOneClaudeSessionIsOneRowAcrossChannels 와 같은 규율).
//
// 비콘은 mcpsrv.New 안에서 기동 시 1회 심긴다(도구 호출 전) — 그래서 stdin 을 즉시 EOF 로
// 두어 도구를 하나도 안 부르고, 네트워크도 안 탄다(빈 아웃박스의 Flush 는 no-op).
func TestRunMCPWiresBeaconDir(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	stateDir := t.TempDir()
	const cc = "cc-beacon-wiring-1"

	env := map[string]string{
		"HOME":         home,
		"FD_STATE_DIR": stateDir,
		"FD_PROJECT":   "beacon-wiring-proj",
		"FD_LOG":       "error",
	}

	// mcpsrv.New 는 옵션이 없으면 프로세스의 실제 env·cwd 를 읽는다(운영 조건이 그것이다 —
	// MCP stdio 서버의 cwd 가 프로젝트 디렉토리다). 시험 프로세스의 그 둘을 실제로 맞춘다
	// — TestOneClaudeSessionIsOneRowAcrossChannels 와 같은 이유.
	t.Setenv("CLAUDE_CODE_SESSION_ID", cc)
	t.Setenv("CLAUDE_PROJECT_DIR", dir)
	t.Chdir(dir)

	app := newApp(envOf(env), quietLogger(), dir, strings.NewReader(""))
	if app.beaconDir == "" {
		t.Fatal("대조 전제가 깨졌다 — App.beaconDir 가 비었다")
	}

	var out strings.Builder
	if code := runMCP(context.Background(), app, quietLogger(), strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runMCP 종료코드 %d:\n%s", code, out.String())
	}

	entries, err := os.ReadDir(app.beaconDir)
	if err != nil {
		t.Fatalf("비콘 디렉토리를 못 읽었다(%s): %v", app.beaconDir, err)
	}
	if len(entries) == 0 {
		t.Fatal("runMCP 를 돌렸는데 비콘 파일이 하나도 안 생겼다 — " +
			"mcp.go 의 mcpsrv.New 호출에서 WithBeaconDir 이 빠졌을 때 나는 바로 이 실패다")
	}
}
