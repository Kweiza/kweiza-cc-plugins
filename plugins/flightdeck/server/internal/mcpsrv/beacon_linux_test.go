//go:build linux

// ★ 이 파일이 리눅스 전용인 이유. 여기 시험들은 **비콘이 실제로 심긴 것**을 단정하는데,
// 심기는 window.StartedOf 로 부모의 시작 시각을 읽어야 성립하고 그 함수는 리눅스 밖에서
// ErrUnsupported 를 낸다(proc_other.go). 즉 이 기능 자체가 리눅스에서만 도는 것이라,
// 다른 플랫폼에서 t.Skip 으로 초록을 내는 대신 **빌드 대상에서 뺀다** — 스킵은
// "돌았는데 통과했다"와 구분이 안 가서 초록의 뜻을 흐린다
// (cmd/fd/hook_beacon_test.go 가 같은 판단을 같은 이유로 한다).
//
// ★ 비콘이 **없을 때**를 재는 시험(심기 가드·폴백·WithBeaconDir 격리)은 이식 가능하므로
// beacon_test.go 에 그대로 둔다 — 그쪽은 모든 플랫폼에서 돌아야 하는 단정이다.
package mcpsrv

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/service"
	"github.com/kweiza/flightdeck/internal/window"
)

func TestPlantsWhenIdentityIsWhole(t *testing.T) {
	dir := t.TempDir()
	s := newServerWithBeacon(t, dir, "cc-1")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(ents) != 1 {
		t.Fatalf("비콘 파일이 %d개다, 1개여야 한다", len(ents))
	}
	_ = s
}

// ★ 이것이 이 기능의 본체다. 훅이 비콘에 새 cc 를 적어 두면,
// 옛 cc 를 든 MCP 프로세스가 카드를 열 때 **새 cc 로 연다** — 그래서 카드가 한 장이 된다.
func TestEnsureSessionPrefersTheBeaconCC(t *testing.T) {
	dir := t.TempDir()
	s := newServerWithBeacon(t, dir, "cc-stale") // 프로세스의 env cc 는 낡았다
	k, ok := s.BeaconKey()
	if !ok {
		t.Fatal("비콘 좌표가 없다")
	}
	if _, err := window.SaveIdentity(dir, k, "cc-fresh", "card-A", time.Unix(0, 0)); err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}

	// 도구를 한 번 부른다 — ensureSession 은 게을러서 그 전에는 안 돈다.
	callBoardOnce(t, s)

	if got := lastOpenSessionCC(t, s); got != "cc-fresh" {
		t.Fatalf("MCP 가 %q 로 카드를 열었다, 비콘의 %q 여야 한다", got, "cc-fresh")
	}
}

// ★ 사유를 두 번 말하지 않는다. window.Load 의 오류는 이미 "비콘을 못 읽었다(파일명)" 로
// 시작한다 — 그 앞에 같은 말을 또 붙이면 화면에 그 구절이 겹쳐서 뜨고, 겹친 문구는
// 사람이 진짜 원인(파일명·errno)에 닿기 전에 읽기를 멈추게 한다.
func TestBeaconMissDoesNotSayTheSameThingTwice(t *testing.T) {
	dir := t.TempDir()
	s := newServerWithBeacon(t, dir, "cc-1")
	if _, ok := s.BeaconKey(); !ok {
		t.Fatal("비콘 좌표가 없다")
	}
	if err := os.RemoveAll(dir); err != nil { // 심어 둔 비콘을 없앤다 — 읽기가 실패하는 갈래
		t.Fatalf("비콘 디렉토리를 못 지웠다: %v", err)
	}
	got := s.beaconMiss()
	if got == "" {
		t.Fatal("비콘이 없는데 사유가 비었다 — why 가 그 자리에서 침묵하면 폴백 문구가 할 말이 없다")
	}
	if n := strings.Count(got, "비콘을 못 읽었다"); n != 1 {
		t.Errorf("같은 말이 %d번 겹쳤다: %q", n, got)
	}
}

// cardsInWorktree 는 이 워크트리의 카드를 **서비스에서 직접** 센다.
// 응답 문자열로 세면 지금 시험하는 렌더가 전제까지 통과시킨다(순환).
func cardsInWorktree(t *testing.T, svc *service.Service, project, worktree string) []string {
	t.Helper()
	view, err := svc.Board(context.Background(), project, service.BoardOptions{})
	if err != nil {
		t.Fatalf("보드 조회 실패: %v", err)
	}
	var out []string
	for _, c := range view.Sessions {
		if c.View.Session.Worktree == worktree {
			out = append(out, c.View.Session.ID+"/"+c.View.Session.CCSessionID)
		}
	}
	return out
}

// boardText 는 board 를 한 번 부르고 응답 본문을 낸다.
func boardText(t *testing.T, s *Server) string {
	t.Helper()
	frames := serve(t, s, call("board", map[string]any{}))
	if len(frames) != 1 {
		t.Fatalf("board 응답이 %d개다, 1개여야 한다", len(frames))
	}
	body, isErr := toolText(t, frames[0])
	if isErr {
		t.Fatalf("board 호출이 실패했다:\n%s", body)
	}
	return body
}

// ★★ 수리가 **성공한 뒤에는 보드가 조용해야 한다.**
//
// 비콘의 cc(훅이 적은 새 값)와 env 의 cc(exec 때 주입된 뒤 안 바뀌는 옛 값)가 갈리는 것은
// 사고가 아니라 이 기능의 **정상 상태**다 — /clear 한 번이면 그렇게 된다. 그런데 표류 판정을
// env cc 로 하면, ensureSession 이 방금 비콘 cc 로 연 **자기 카드**가 자기 자신의 쌍둥이로
// 잡힌다. 그러면 /clear 뒤 남은 대화 내내 모든 board 호출이 없는 표류를 고발한다.
//
// 이 단정이 열세 번의 태스크 리뷰를 통과한 사각이다 — 어느 태스크도 "고친 뒤의 침묵"을
// 자기 것으로 갖지 않았다.
func TestBoardIsSilentAfterASuccessfulRepair(t *testing.T) {
	dir := t.TempDir()
	s, svc, repo := newServerWithBeaconAndSvc(t, dir, "cc-stale")
	k, ok := s.BeaconKey()
	if !ok {
		t.Fatal("비콘 좌표가 없다")
	}
	// 훅이 /clear 뒤에 적어 둔 상태를 흉내낸다: 새 cc 와 그 카드.
	if _, err := window.SaveIdentity(dir, k, "cc-fresh", "card-A", time.Unix(0, 0)); err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}

	body := boardText(t, s)

	// ── 전제를 **독립된 경로로** 세운다 ─────────────────────────────────────
	// ① 카드를 정말 비콘의 cc 로 열었다(수리가 성립했다).
	if got := lastOpenSessionCC(t, s); got != "cc-fresh" {
		t.Fatalf("전제가 깨졌다 — 카드를 %q 로 열었다, 비콘의 cc-fresh 여야 한다", got)
	}
	// ② 이 워크트리의 카드는 한 장뿐이다. 두 장이면 침묵이 옳지 않다.
	if cards := cardsInWorktree(t, svc, s.id.ProjectID, repo); len(cards) != 1 {
		t.Fatalf("전제가 깨졌다 — 카드가 %d장이다(%v)", len(cards), cards)
	}

	// ── 요구: 아무 말도 안 한다 ─────────────────────────────────────────────
	// "갈린" 만으로 세지 않는다 — 머신 id 경고에도 그 글자가 있다("…세션이 갈린다").
	if strings.Contains(body, "cc_session_id 가 갈린") || strings.Contains(body, "cc-stale") {
		t.Errorf("수리가 끝났는데 보드가 자기 카드를 표류로 고발한다 — "+
			"판정을 env cc(cc-stale)로 하고 있다는 신호다:\n%s", body)
	}
}

// ★ 그래도 **진짜 다른 창**은 계속 이름으로 말한다. 위 시험이 요구하는 침묵을
// "알림을 꺼서" 얻으면 이 시험이 빨개진다 — 같은 워크트리에 창이 다섯인 환경에서
// 카드가 여러 장인 이유를 말해 주는 자리가 그것 하나뿐이다.
func TestBoardStillNamesAnotherWindowAfterARepair(t *testing.T) {
	dir := t.TempDir()
	s, svc, repo := newServerWithBeaconAndSvc(t, dir, "cc-stale")
	k, ok := s.BeaconKey()
	if !ok {
		t.Fatal("비콘 좌표가 없다")
	}
	if _, err := window.SaveIdentity(dir, k, "cc-fresh", "card-A", time.Unix(0, 0)); err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}
	// 같은 (머신·워크트리) 에 **다른 창**의 카드. claude 부모가 다른 별개 대화라
	// 훅은 이것을 영영 안 합친다 — 합치면 안 되는 것이 맞다.
	if _, err := svc.OpenSession(context.Background(), service.OpenSessionInput{
		Project: s.id.ProjectID, ProjectPath: repo,
		MachineID: "testhost", Hostname: "testhost", Worktree: repo,
		CCSessionID: "cc-another-window",
	}); err != nil {
		t.Fatalf("전제 구성 실패 — 다른 창의 카드를 못 만들었다: %v", err)
	}

	body := boardText(t, s)

	if cards := cardsInWorktree(t, svc, s.id.ProjectID, repo); len(cards) != 2 {
		t.Fatalf("전제가 깨졌다 — 카드가 %d장이다(%v), 2장이어야 한다", len(cards), cards)
	}
	if !strings.Contains(body, "cc-another-window") {
		t.Errorf("다른 창의 카드를 이름으로 말하지 않는다 — 카드가 왜 두 장인지 알 길이 없다:\n%s", body)
	}
	// 내가 든 값으로 **비콘의 cc** 를 말한다. env 의 옛 값을 말하면 사람이 엉뚱한 것을 찾는다.
	if !strings.Contains(body, "cc-fresh") || strings.Contains(body, "cc-stale") {
		t.Errorf("내가 든 값을 env 의 옛 cc 로 말한다 — 카드를 연 값(cc-fresh)이어야 한다:\n%s", body)
	}
	// 그리고 **한 건**이다 — 자기 카드는 쌍둥이가 아니다.
	if !strings.Contains(body, "1건 더 있다") {
		t.Errorf("표류 건수가 1건이 아니다 — 자기 카드까지 세고 있다는 신호다:\n%s", body)
	}
}
