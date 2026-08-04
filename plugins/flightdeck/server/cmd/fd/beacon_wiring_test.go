package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/window"
)

// ★ 사다리의 주인은 하나다. cmd/fd 가 자기 판본을 갖게 두면
// client.go 의 newClient 주석이 "같은 판단이 두 자리에 살아 세 번 데였다"고 적어 둔 그 실수가 반복된다.
func TestBeaconDirDelegatesToTheOneOwner(t *testing.T) {
	env := envOf(map[string]string{"FD_STATE_DIR": "/pin"})
	gotPath, gotSrc := BeaconDir(env, "/home/u")
	wantPath, wantSrc := window.Dir(env, "/home/u")
	if gotPath != wantPath || gotSrc != wantSrc {
		t.Fatalf("BeaconDir = (%q,%q), window.Dir = (%q,%q) — 사다리가 두 벌이다",
			gotPath, gotSrc, wantPath, wantSrc)
	}
}

// rekey 는 a.cli.do 로 보낸다. a.cli.Write 로 보내면 JudgeOffline 과 IdempotencyStable 의
// default 가 "정책이 정의되어 있지 않다"로 거절해 서버가 안 닿을 때마다 실패한다.
// 그리고 rekey 는 아웃박스에 쌓을 것이 아니다 — 다음 SessionStart 훅이 어차피 다시 시도한다.
//
// 단정 좌표계는 하네스 규율 그대로다: 요청을 셌다는 단정은 "보냈다"만 말하고
// "저장됐다"는 말하지 못한다. 그래서 (a) 서버가 실제로 갖게 된 값으로 경로를 확인하고,
// (b) 서버 불통일 때 아웃박스가 실제로 안 늘어나는지를 잰다.
func TestRekeyPostsAndDoesNotQueueOffline(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	app := newApp(envOf(h.env), quietLogger(), h.state, strings.NewReader(""))
	opened, _, err := app.OpenSession(ctx, "cc-rekey-open-1", "")
	if err != nil {
		t.Fatalf("세션 열기 실패: %v", err)
	}
	id := opened.Session.ID
	if id == "" {
		t.Fatal("대조 전제가 깨졌다 — 세션 id 가 비었다")
	}

	// ── ① 서버가 살아 있을 때: /api/v1/sessions/<id>/rekey 로 실제로 갔는가.
	// 서버가 실제로 갖게 된 값(h.st.GetSession)으로 잰다 — 응답만 보면 클라이언트가
	// 자기 응답을 스스로 지어내도 통과한다.
	res, err := app.Rekey(ctx, id, "cc-rekey-new-1")
	if err != nil {
		t.Fatalf("rekey 실패: %v", err)
	}
	if res.CCSessionID != "cc-rekey-new-1" {
		t.Fatalf("rekey 응답의 cc_session_id 가 %q 다 — 기대 cc-rekey-new-1", res.CCSessionID)
	}
	stored, err := h.st.GetSession(ctx, id)
	if err != nil {
		t.Fatalf("서버에서 세션을 못 읽었다: %v", err)
	}
	if stored.CCSessionID != "cc-rekey-new-1" {
		t.Fatalf("서버에 저장된 cc_session_id 가 %q 다 — "+
			"/api/v1/sessions/{id}/rekey 로 실제로 안 갔다는 신호다", stored.CCSessionID)
	}

	// ── ② 서버가 미도달일 때: 아웃박스에 안 쌓이는가.
	// a.cli.Write 를 썼다면 KeyFor→IdempotencyStable("rekey") 의 default 로 새 키를 만든 뒤
	// Write 가 미도달을 잡아 JudgeOffline("rekey") 의 default(OfflineRefuse)로 거절하거나
	// 표에 있었다면 아웃박스에 쌓았을 것이다. a.cli.do 를 직접 쓰면 그 판정 자체를 안 거치므로
	// 애초에 아웃박스에 쌓일 길이 없다 — 그 사실을 여기서 잰다.
	before, err := app.cli.Outbox.List()
	if err != nil {
		t.Fatalf("아웃박스 조회 실패: %v", err)
	}
	h.down() // 대조 전제: 정말 미도달인지 스스로 확인하고 죽인다(harness 규율)

	_, err = app.Rekey(ctx, id, "cc-rekey-new-2")
	if err == nil {
		t.Fatal("서버를 죽였는데 rekey 가 성공했다 — 대조 전제가 깨졌다")
	}
	if !Unreachable(err, 0) {
		t.Fatalf("미도달 오류가 아니다: %v", err)
	}

	after, err := app.cli.Outbox.List()
	if err != nil {
		t.Fatalf("아웃박스 조회 실패: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("서버 미도달인데 아웃박스가 %d → %d 로 늘었다 — "+
			"rekey 가 a.cli.Write 경로를 탄 신호다", len(before), len(after))
	}
}

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
