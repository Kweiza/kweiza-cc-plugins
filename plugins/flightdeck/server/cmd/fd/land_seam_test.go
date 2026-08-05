package main

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
	"github.com/kweiza/flightdeck/internal/store"
)

// 랜딩 레인의 **이음매** 시험.
//
// 닫는 것이 하나다: wire.go 의 요청 구조체와 internal/api 의 것이 필드 이름으로만 이어져
// 있고, 갈라지면 서버가 **오류 없이 0값을 받는다.** 이 축은 알려진 결함이고(판단 01KZ56B7…)
// 지금까지 시험이 없었다.
//
// ★ 구조체를 눈으로 대조하지 않는다 — 사본을 단정하는 시험은 아무것도 안 지킨다.
// 여기서는 실물 서버에 붙여 왕복시키고, 단정의 좌표계를 셋으로 둔다:
// **CLI stdout · 서버가 실제로 갖게 된 원장(landing_queue·resource_hold·judgment) · 종료코드.**
//
// ★ 레인은 프로젝트당 하나뿐이라 한 번 물리면 그 프로젝트의 랜딩이 전원 정지한다.
// 그래서 마지막 시험(회수)이 이 파일에서 가장 중요하다 — 그것이 **유일한 탈출구**다.

// runAs 는 **다른 Claude Code 세션**으로 fd 한 번을 돌린다.
//
// 레인의 성질(둘째 세션이 서면 줄이 선다)은 세션이 둘일 때만 나타난다. 한 세션으로만
// 시험하면 재진입 경로만 밟고 "순서"라는 축을 통째로 안 본다.
func (h *harness) runAs(cc string, args ...string) (int, string) {
	h.t.Helper()
	e := map[string]string{}
	for k, v := range h.env {
		e[k] = v
	}
	e["CLAUDE_CODE_SESSION_ID"] = cc
	return h.runEnv(e, "", args...)
}

// laneLive 는 지금 줄에 살아 있는 행 전부다.
func laneLive(t *testing.T, h *harness) []model.LandingRow {
	t.Helper()
	rows, err := h.st.ListLandingQueue(context.Background(), h.project)
	if err != nil {
		t.Fatalf("줄 조회 실패: %v", err)
	}
	return rows
}

// laneHolder 는 지금 레인을 쥔 세션이다. 아무도 없으면 ("", false).
func laneHolder(t *testing.T, h *harness) (string, bool) {
	t.Helper()
	hold, err := h.st.HeldBy(context.Background(), h.project, service.LaneResource)
	switch {
	case err == nil:
		return hold.SessionID, true
	case errors.Is(err, store.ErrNotFound):
		return "", false
	default:
		t.Fatalf("점유 조회 실패: %v", err)
		return "", false
	}
}

// laneRowByID 는 닫힌 행까지 포함해 한 행을 읽는다.
//
// ListLandingQueue 는 **살아 있는 행만** 낸다 — 그래서 "어떤 종류로, 어떤 사유로 닫혔나"는
// 그것으로 볼 수 없다. 그 두 값이 이 시험의 진짜 좌표다(Kind·Detail 이 서버에 닿았는가).
func laneRowByID(t *testing.T, h *harness, id int64) (kind, detail string) {
	t.Helper()
	row := h.st.DB().QueryRowContext(context.Background(),
		`SELECT coalesce(left_kind,''), coalesce(left_detail,'') FROM landing_queue WHERE id = ?`, id)
	if err := row.Scan(&kind, &detail); err != nil {
		t.Fatalf("줄 행 %d 를 못 읽었다: %v", id, err)
	}
	return kind, detail
}

// ─────────────────────────────────────────────────────────────────────────────
// ① CLI → REST → 원장. 필드 이름 하나만 어긋나도 여기서 깨진다
// ─────────────────────────────────────────────────────────────────────────────

func TestLandSeamRoundTripsThroughRealServer(t *testing.T) {
	h := newHarness(t)

	// ── ① 첫 세션이 선다. 줄이 비어 있으므로 그 자리에서 차례가 온다 ──
	code, out := h.runAs("cc-lane-a", "land")
	if code != 0 {
		t.Fatalf("첫 land 가 %d 로 끝났다 — 빈 레인이면 차례가 와야 한다:\n%s", code, out)
	}
	// 차례 안내는 **이 채널에서 실제로 부를 수 있는 이름**이어야 한다. 공유 렌더가
	// 도구 인자 이름(result)으로 적어 두므로, CLI 는 그것을 자기 플래그로 옮겨 준다.
	mustContain(t, "land stdout", out, "네 차례다", "fd land --ok")

	rows := laneLive(t, h)
	if len(rows) != 1 {
		t.Fatalf("줄에 %d행이다 — 1행이어야 한다", len(rows))
	}
	rowA := rows[0]
	holder, held := laneHolder(t, h)
	if !held || holder != rowA.SessionID {
		t.Fatalf("차례라고 답했는데 점유가 %q(있음=%v) 다 — 배타가 서버에 안 닿았다", holder, held)
	}

	// ── ② 둘째 세션이 선다. **줄이 서야 한다** ──
	code, out = h.runAs("cc-lane-b", "land")
	if code == 0 {
		t.Fatalf("남이 쥔 레인인데 land 가 0 으로 끝났다 — `fd land && <랜딩>` 이 그대로 통과한다:\n%s", out)
	}
	mustContain(t, "둘째 land stdout", out, "너는 2번째다")
	if len(laneLive(t, h)) != 2 {
		t.Fatalf("둘째가 섰는데 줄이 %d행이다", len(laneLive(t, h)))
	}
	if holder2, _ := laneHolder(t, h); holder2 != rowA.SessionID {
		t.Fatalf("둘째가 섰더니 점유자가 %q 로 바뀌었다 — 배타가 깨졌다", holder2)
	}

	// ── ③ 첫 세션이 fail 로 보고한다. **kind·detail 이 서버에 닿아야 한다** ──
	//    이 단정이 이 파일의 핵심이다: 이름이 어긋나면 kind 가 빈 값으로 도착하고,
	//    서버는 "ok 또는 fail 이어야 한다"로 거절한다(그래서 조용하지 않다).
	//    detail 은 그보다 더 조용하다 — 빈 값으로 도착해도 fail 이면 거절되지만,
	//    ok 였다면 아무 오류 없이 **사유가 통째로 사라진 채** 원장에 남는다.
	code, out = h.runAs("cc-lane-a", "land", "--fail", "테스트가 빨강이라 접었다")
	if code != 0 {
		t.Fatalf("보고가 %d 로 끝났다:\n%s", code, out)
	}
	mustContain(t, "보고 stdout", out, "반납했다")

	kind, detail := laneRowByID(t, h, rowA.ID)
	if kind != string(model.LandingLeftFail) {
		t.Fatalf("줄 행이 %q 로 닫혔다 — fail 이어야 한다. wire 의 kind 필드 이름을 의심해라", kind)
	}
	if detail != "테스트가 빨강이라 접었다" {
		t.Fatalf("사유가 서버에 안 닿았다(%q) — wire 의 detail 필드 이름을 의심해라", detail)
	}
	if holder3, held3 := laneHolder(t, h); held3 {
		t.Fatalf("보고했는데 점유가 %q 로 남아 있다 — 레인이 물렸다", holder3)
	}

	// ── ④ 둘째 세션의 차례가 온다. **차례를 미는 것은 다음 호출이다**(지연 부여) ──
	code, out = h.runAs("cc-lane-b", "land")
	if code != 0 {
		t.Fatalf("앞사람이 반납했는데 둘째 land 가 %d 다:\n%s", code, out)
	}
	mustContain(t, "둘째의 차례 stdout", out, "네 차례다")

	// ── ⑤ 둘째가 이탈한다. 이탈은 detail 이 필수이고, 점유도 함께 놓아야 한다 ──
	rowB := laneLive(t, h)[0]
	code, out = h.runAs("cc-lane-b", "land", "--leave", "다음 세션에 넘긴다")
	if code != 0 {
		t.Fatalf("이탈이 %d 로 끝났다:\n%s", code, out)
	}
	mustContain(t, "이탈 stdout", out, "줄에서 빠졌다")
	kind, detail = laneRowByID(t, h, rowB.ID)
	if kind != string(model.LandingLeftLeave) || detail != "다음 세션에 넘긴다" {
		t.Fatalf("이탈이 원장에 %q/%q 로 남았다", kind, detail)
	}
	if holder4, held4 := laneHolder(t, h); held4 {
		t.Fatalf("이탈했는데 점유가 %q 로 남아 있다 — 대응하는 줄 행이 없는 점유는 레인을 영영 막는다", holder4)
	}
	if n := len(laneLive(t, h)); n != 0 {
		t.Fatalf("전부 빠졌는데 줄에 %d행이 남았다", n)
	}
}

// 사유 없는 --fail·--leave 는 **그 자리에서** 거절돼야 한다.
//
// 사유가 원장에 안 남는 이탈은 나중에 "다시 서면 통과할 종류였나"에 답할 수 없다.
// 이 시험이 없으면 CLI 가 빈 문자열을 보내고 서버가 거절하는 것과, CLI 가 그 플래그를
// 아예 안 읽는 것이 구분되지 않는다.
func TestLandRefusesLeavingWithoutAReason(t *testing.T) {
	h := newHarness(t)
	if code, out := h.runAs("cc-lane-r", "land"); code != 0 {
		t.Fatalf("전제가 깨졌다 — 첫 land 가 %d 다:\n%s", code, out)
	}
	code, out := h.runAs("cc-lane-r", "land", "--fail", "")
	if code == 0 {
		t.Fatalf("사유 없는 --fail 이 통과했다:\n%s", out)
	}
	if !strings.Contains(out, "사유") {
		t.Errorf("거절이 무엇이 없어서인지 말하지 않는다:\n%s", out)
	}
	// 대조: 거절됐으므로 레인은 **그대로 쥐고 있어야 한다.** 반쪽만 돌아가면
	// "거절했다"고 말해 놓고 점유만 풀린 상태가 된다.
	if _, held := laneHolder(t, h); !held {
		t.Error("거절당한 보고가 점유를 풀었다 — 거절은 아무것도 하지 않아야 한다")
	}
}

// `fd land --ok` 도 **실제로 왕복시킨다.**
//
// ★ 앞선 판에서 이 갈래는 시험 밖에 있었다. `--ok` 를 언급하는 시험 둘이 **보내기 전에**
// 끝났기 때문이다(하나는 --leave 와 함께 줘서 종료코드 2 로 거절, 하나는 `--ok=false` 라
// acquire 로 접힘). 왕복은 --fail 과 --leave 만 했다.
//
// 그래서 cmds.go 의 ok 갈래에 model.LandingLeftFail 을 붙여넣어도 **전 시험이 초록이었다.**
// 그 변이의 결과는 조용하지 않고 영구적이다: landing_queue 는 추가 전용에 가까운 이력이라
// `fd land --ok` 한 번이 원장에 `left_kind='fail'` 을 박고, 다음 사람은 그 프로젝트가
// 검증 실패로 접힌 랜딩투성이인 줄 안다. 이 태스크의 산출물이 이음매 시험인데
// **가장 흔한 호출**이 그 밖에 있는 것 자체가 산출물의 구멍이었다.
func TestLandReportOKReachesTheLedgerAsOK(t *testing.T) {
	h := newHarness(t)
	if code, out := h.runAs("cc-lane-ok", "land"); code != 0 {
		t.Fatalf("전제가 깨졌다 — 레인을 못 잡았다(%d):\n%s", code, out)
	}
	row := laneLive(t, h)[0]

	code, out := h.runAs("cc-lane-ok", "land", "--ok")
	if code != 0 {
		t.Fatalf("--ok 보고가 %d 로 끝났다:\n%s", code, out)
	}
	mustContain(t, "--ok stdout", out, "반납했다")

	kind, detail := laneRowByID(t, h, row.ID)
	if kind != string(model.LandingLeftOK) {
		t.Fatalf("--ok 로 보고했는데 줄 행이 %q 로 닫혔다 — 이 이력은 되돌릴 수 없다", kind)
	}
	// ★ ok 는 사유가 **면제**다(store.ValidateLandingLeave). CLI 가 빈 자리를 채우려고
	//   무언가를 지어내면 그 문장이 원장에 남는다 — 지어낸 사실이 없어야 한다.
	if detail != "" {
		t.Fatalf("--ok 인데 사유 %q 가 붙었다 — 아무도 쓰지 않은 문장이다", detail)
	}
	if holder, held := laneHolder(t, h); held {
		t.Fatalf("--ok 로 보고했는데 점유가 %q 로 남아 있다", holder)
	}
	if n := len(laneLive(t, h)); n != 0 {
		t.Fatalf("--ok 로 보고했는데 줄에 %d행이 남았다", n)
	}
}

// reclaimed 가 **전선을 건너는 첫 시험**이다.
//
// ★ 지금까지 reclaimed 는 service 시험과 RenderLand 순수 시험에만 있었다. 뒤엣것은
// 사유 문자열의 **사본**을 자기 손으로 넣어 보는 것이라(이 파일 머리의 규율) 서버가
// 무엇을 내는지는 아무것도 안 지킨다. 그래서 "대기 중인 세션이 실수로 보고했을 때
// 셸이 무엇을 찍고 무엇을 반환하며 자기 줄 행이 살아남는가"는 어느 시험도 안 봤다.
//
// ★ 종료코드가 이 시험의 절반이다. reclaimed 에 0 을 내면
// `fd land --fail "..." && <랜딩>` 이 그대로 통과하고, 남의 레인에 보고한 세션이 랜딩한다.
//
// ★ 나머지 절반은 **오타 한 번이 순번을 못 날린다**는 것이다 — 줄 행은 살아 있고
// 앞사람의 점유는 그대로여야 한다.
func TestWaitingSessionsReportIsRefusedAllTheWayToTheShell(t *testing.T) {
	h := newHarness(t)
	if code, out := h.runAs("cc-lane-w1", "land"); code != 0 {
		t.Fatalf("전제가 깨졌다 — 앞사람이 레인을 못 잡았다(%d):\n%s", code, out)
	}
	if code, out := h.runAs("cc-lane-w2", "land"); code == 0 {
		t.Fatalf("남이 쥔 레인인데 둘째 land 가 0 으로 끝났다:\n%s", out)
	}
	rows := laneLive(t, h)
	if len(rows) != 2 {
		t.Fatalf("전제가 깨졌다 — 줄이 %d행이다(기대 2)", len(rows))
	}
	front, mine := rows[0], rows[1]

	// 대기 중인 세션이 --fail 을 친다. 실제 경로는 오타이거나, 앞선 세션의 습관이다.
	code, out := h.runAs("cc-lane-w2", "land", "--fail", "잘못 눌렀다")
	if code != 1 {
		t.Fatalf("남의 레인에 보고했는데 종료코드가 %d 다 — 1이어야 한다:\n%s", code, out)
	}
	mustContain(t, "대기 중 보고 stdout", out, "이 레인은 네 것이 아니다", "차례를 확인")
	// ★ 안 놓은 것을 놓았다고 말하면 그 세션은 랜딩해도 된다고 믿는다.
	if strings.Contains(out, "반납했다") {
		t.Fatalf("쥔 적 없는 레인을 반납했다고 답했다:\n%s", out)
	}

	// ── 원장 축 ──
	after := laneLive(t, h)
	if len(after) != 2 || after[1].ID != mine.ID {
		t.Fatalf("보고 뒤 줄이 %+v 다 — 대기자의 줄 행이 사라졌다(기대: 행 %d 가 살아 있음)",
			after, mine.ID)
	}
	if kind, detail := laneRowByID(t, h, mine.ID); kind != "" || detail != "" {
		t.Fatalf("대기자의 줄 행이 %q/%q 로 닫혔다 — 남의 레인에 보고한 것이 자기 자리를 없앴다",
			kind, detail)
	}
	if holder, held := laneHolder(t, h); !held || holder != front.SessionID {
		t.Fatalf("대기자의 보고가 앞사람의 점유를 건드렸다: %q(있음=%v), 기대 %q",
			holder, held, front.SessionID)
	}
}

// 둘 이상을 함께 주면 도구가 조용히 하나를 고르지 않는다. `--ok=false` 도 반납이 아니다.
//
// ★ 뒤엣것이 이 시험의 핵심이다: 불리언을 **준 것만** 보면 `--ok=false` 라는 표기가
// 반납으로 둔갑하고, 그 반납은 되돌릴 수 없다.
func TestLandNeverGuessesWhichActionYouMeant(t *testing.T) {
	h := newHarness(t)
	if code, out := h.runAs("cc-lane-amb", "land"); code != 0 {
		t.Fatalf("전제가 깨졌다 — 레인을 못 잡았다(%d):\n%s", code, out)
	}

	code, out := h.runAs("cc-lane-amb", "land", "--ok", "--leave", "빠진다")
	if code != 2 {
		t.Fatalf("--ok 와 --leave 를 함께 줬는데 종료코드가 %d 다:\n%s", code, out)
	}
	if _, held := laneHolder(t, h); !held {
		t.Fatal("모호한 인자로 레인이 풀렸다 — 조용히 하나를 골랐다")
	}

	// `--ok=false` 는 "보고하겠다"가 아니다. 줄 서기로 접히고 점유는 그대로여야 한다.
	code, out = h.runAs("cc-lane-amb", "land", "--ok=false")
	if code != 0 {
		t.Fatalf("--ok=false 가 %d 로 끝났다 — 재진입한 점유자는 차례를 다시 받는다:\n%s", code, out)
	}
	if _, held := laneHolder(t, h); !held {
		t.Fatalf("--ok=false 가 레인을 반납했다 — 준 것만 보고 값을 안 봤다:\n%s", out)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ② 탈출구 — 물린 레인을 사람이 회수한다
// ─────────────────────────────────────────────────────────────────────────────

// ★ 이 시험이 이 태스크의 존재 이유다.
//
// 자동 만료가 없고, 세션 정체가 (machine, worktree, cc_session_id) 라 **죽은 세션 명의로
// land(leave) 를 부를 방법이 없다.** 이 명령이 없으면 복구가 sqlite3 직접 UPDATE 뿐이고,
// 그 경로에는 판단이 한 줄도 안 남는다.
func TestLaneReleaseIsTheOnlyWayOutOfAStuckLane(t *testing.T) {
	h := newHarness(t)
	if code, out := h.runAs("cc-lane-dead", "land"); code != 0 {
		t.Fatalf("전제가 깨졌다 — 레인을 쥐지 못했다(%d):\n%s", code, out)
	}
	stuck := laneLive(t, h)[0]
	if _, held := laneHolder(t, h); !held {
		t.Fatal("전제가 깨졌다 — 회수할 점유가 없다")
	}
	before := len(h.judgments(model.JudgmentDecision))

	// ★ 회수하는 사람은 **그 세션이 아니다.** 세션 좌표(CLAUDE_CODE_SESSION_ID)를
	//   아예 지운 환경으로 부른다 — 죽은 세션을 되살릴 수 없는 것이 이 명령의 전제이고,
	//   여기서 세션을 요구하면 탈출구가 다시 막힌다.
	e := map[string]string{}
	for k, v := range h.env {
		e[k] = v
	}
	delete(e, "CLAUDE_CODE_SESSION_ID")
	code, out := h.runEnv(e, "", "lane", "release",
		"--row", itoa(stuck.ID), "--reason", "세션이 죽었고 3시간째 신호가 없다",
		"--actor", "당번-아론")
	if code != 0 {
		t.Fatalf("회수가 %d 로 끝났다:\n%s", code, out)
	}
	mustContain(t, "회수 stdout", out, "회수", itoa(stuck.ID))

	if holder, held := laneHolder(t, h); held {
		t.Fatalf("회수했는데 점유가 %q 로 남아 있다 — 탈출구가 아니다", holder)
	}
	if n := len(laneLive(t, h)); n != 0 {
		t.Fatalf("회수했는데 줄에 %d행이 남았다 — 점유만 풀면 줄 행이 유령으로 남는다", n)
	}
	kind, detail := laneRowByID(t, h, stuck.ID)
	if kind != string(model.LandingLeftForce) {
		t.Fatalf("회수된 행이 %q 로 닫혔다 — force 여야 한다", kind)
	}
	if detail != "세션이 죽었고 3시간째 신호가 없다" {
		t.Fatalf("회수 사유가 서버에 안 닿았다(%q) — wire 의 reason 필드 이름을 의심해라", detail)
	}

	// ★ 판단이 남아야 한다. 이것이 sqlite3 직접 UPDATE 와 이 명령의 **유일한 차이**다.
	js := h.judgments(model.JudgmentDecision)
	if len(js) != before+1 {
		t.Fatalf("회수했는데 판단이 %d건 늘었다 — 1건이어야 한다", len(js)-before)
	}
	body := js[len(js)-1].Body
	if !strings.Contains(body, "세션이 죽었고 3시간째 신호가 없다") {
		t.Fatalf("회수 판단에 사유가 없다:\n%s", body)
	}
	// ★ **actor 가 이 이음매에서 가장 조용한 축이다.** 이름이 어긋나 빈 값으로 도착하면
	//   서버는 오류 없이 "행위자: 대시보드(사람)" 라고 적는다 — 거짓 사실이 불변 기록에
	//   영구히 박히고, 아무 시험도 안 걸린다.
	if !strings.Contains(body, "행위자: 당번-아론") {
		t.Fatalf("회수한 사람이 판단에 안 남았다 — wire 의 actor 필드 이름을 의심해라:\n%s", body)
	}

	// 대조: 이제 남이 설 수 있다. 회수가 "화면만 지웠다"가 아니어야 한다.
	if code, out := h.runAs("cc-lane-next", "land"); code != 0 {
		t.Fatalf("회수 뒤에도 레인을 못 잡는다(%d):\n%s", code, out)
	}
}

// `--actor` **없이** 회수해도 누가 했는지가 판단에 남는다.
//
// ★ 이 갈래가 하필 **거짓 문장이 실제로 나오는 갈래**다. actor 가 빈 채로 서버에 닿으면
// service 는 "행위자: 대시보드(사람). 세션이 아니라 사람이 누른 것이므로 …" 라고 적는다 —
// 셸에서 부른 회수인데 원장에는 대시보드가 눌렀다고 **영구히** 남는다.
// 명시 갈래(--actor)만 왕복시키면 "플래그 없음 → USER@host → wire actor → 판단 본문"이라는
// 합성이 한 번도 증명되지 않고, 그 조용한 축은 순수 함수 시험으로는 원리적으로 안 보인다.
func TestLaneReleaseWithoutActorFlagStillRecordsWhoDidIt(t *testing.T) {
	h := newHarness(t)
	if code, out := h.runAs("cc-lane-noactor", "land"); code != 0 {
		t.Fatalf("전제가 깨졌다 — 레인을 못 잡았다(%d):\n%s", code, out)
	}
	stuck := laneLive(t, h)[0]
	before := len(h.judgments(model.JudgmentDecision))

	// 죽은 세션의 레인을 사람이 끊는 상황 그대로: 세션 좌표는 없고 셸 좌표만 있다.
	e := map[string]string{}
	for k, v := range h.env {
		e[k] = v
	}
	delete(e, "CLAUDE_CODE_SESSION_ID")
	e["USER"] = "당번유저"

	code, out := h.runEnv(e, "", "lane", "release",
		"--row", itoa(stuck.ID), "--reason", "신호가 4시간째 없다")
	if code != 0 {
		t.Fatalf("--actor 없는 회수가 %d 로 끝났다:\n%s", code, out)
	}
	js := h.judgments(model.JudgmentDecision)
	if len(js) != before+1 {
		t.Fatalf("판단이 %d건 늘었다 — 1건이어야 한다", len(js)-before)
	}
	body := js[len(js)-1].Body
	if !strings.Contains(body, "행위자: 당번유저@") {
		t.Fatalf("셸 좌표가 판단에 안 남았다 — laneActor 폴백이 서버까지 안 간다:\n%s", body)
	}
	// ★ 진짜 단정: 거짓 문장이 **안** 들어갔다.
	if strings.Contains(body, "대시보드(사람)") {
		t.Fatalf("셸에서 부른 회수가 원장에 '대시보드가 눌렀다'로 남았다:\n%s", body)
	}
}

// 사유 없는 회수는 거절된다. **사유가 원장에 안 남는 회수는 나중에 되짚을 수 없다.**
func TestLaneReleaseRefusesWithoutAReason(t *testing.T) {
	h := newHarness(t)
	if code, _ := h.runAs("cc-lane-x", "land"); code != 0 {
		t.Fatal("전제가 깨졌다 — 레인을 못 잡았다")
	}
	row := laneLive(t, h)[0]
	code, out := h.run("", "lane", "release", "--row", itoa(row.ID))
	if code == 0 {
		t.Fatalf("사유 없는 회수가 통과했다:\n%s", out)
	}
	if _, held := laneHolder(t, h); !held {
		t.Fatal("거절당한 회수가 점유를 풀었다")
	}
	// 번호 없는 회수도 마찬가지다 — 무엇을 회수할지가 없다.
	if code, out := h.run("", "lane", "release", "--reason", "사유는 있다"); code == 0 {
		t.Fatalf("번호 없는 회수가 통과했다:\n%s", out)
	}
	// 모르는 하위 명령은 조용히 무시하지 않는다.
	if code, out := h.run("", "lane", "grab"); code == 0 {
		t.Fatalf("모르는 lane 하위 명령이 0 으로 끝났다:\n%s", out)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ③ 이음매가 갈라지면 **조용하지 않다**
// ─────────────────────────────────────────────────────────────────────────────

// mode 가 0값으로 도착하는 것이 이 이음매의 최악 형태다.
//
// 서버가 빈 mode 를 acquire 로 접으면, 반납하려던 호출이 **다시 줄에 서는 것**이 되고
// 레인은 안 풀린다. 그 어긋남은 어느 화면에도 안 뜬다. 그래서 mode 를 필수로 두었고,
// 이 시험이 그 판정을 잠근다.
func TestLandRefusesAMissingModeLoudly(t *testing.T) {
	h := newHarness(t)
	cli := newClient(ResolveStateDir(envOf(h.env), ""), envOf(h.env), h.home, quietLogger())
	ctx := context.Background()

	// 세션 하나를 만들어 좌표를 얻는다(mode 말고는 전부 옳은 요청이어야 한다).
	app := newApp(envOf(h.env), quietLogger(), h.home, strings.NewReader(""))
	sess, err := app.sessionID(ctx, "cc-lane-mode")
	if err != nil {
		t.Fatalf("전제가 깨졌다 — 세션을 못 열었다: %v", err)
	}

	// mode 없이 보낸다 = 필드 이름이 어긋나 0값으로 도착한 것과 같은 상태다.
	_, _, derr := cli.do(ctx, "POST", landingPath, map[string]any{
		"project": h.project, "session_id": sess,
	}, FreshKey(sess))
	var ae *APIError
	if !errors.As(derr, &ae) {
		t.Fatalf("mode 없는 요청이 %v 로 끝났다 — 400 이어야 한다", derr)
	}
	if ae.Status != 400 {
		t.Fatalf("mode 없는 요청이 %d 다 — 400 이어야 한다: %s", ae.Status, ae.Message)
	}
	if !strings.Contains(ae.Message, "mode") {
		t.Errorf("거절이 무엇이 문제인지 말하지 않는다: %s", ae.Message)
	}
	// ★ 진짜 단정: **아무 줄 행도 안 생겼다.** 빈 mode 를 acquire 로 접었다면 여기서 걸린다.
	if n := len(laneLive(t, h)); n != 0 {
		t.Fatalf("거절된 요청이 줄 행 %d개를 만들었다 — 빈 mode 가 줄 서기로 접혔다", n)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ④ MCP 도 같은 레인을 본다 — 배선이 갈리면 여기서 깨진다
// ─────────────────────────────────────────────────────────────────────────────

// mcpbackend 의 셋은 CLI 와 **같은 경로·같은 본문**을 써야 한다. 갈라지면 MCP 의 land 만
// 404 를 받거나(경로) 조용히 0값을 보내고(이름), 그 비대칭은 "MCP 가 고장났다"로만 보인다.
func TestMCPLandUsesTheSameLaneAsTheCLI(t *testing.T) {
	h := newHarness(t)
	rig := newMCPRig(t, h, "cc-mcp-land")

	frames := mcpServe(t, rig, mcpCall("land", map[string]any{}))
	text, isErr := mcpText(t, frames[0])
	if isErr {
		t.Fatalf("MCP land 가 실패했다:\n%s", text)
	}
	mustContain(t, "MCP land 응답", text, "네 차례다")
	rows := laneLive(t, h)
	if len(rows) != 1 {
		t.Fatalf("MCP land 뒤 줄이 %d행이다 — REST 를 안 탔거나 다른 프로젝트를 봤다", len(rows))
	}
	holder, held := laneHolder(t, h)
	if !held {
		t.Fatal("MCP land 가 차례라고 답했는데 점유가 없다")
	}

	// 같은 레인인가 — **CLI 가 그 세션으로 서면 줄을 서야 한다.**
	if code, out := h.runAs("cc-lane-after-mcp", "land"); code == 0 {
		t.Fatalf("MCP 가 쥔 레인인데 CLI 가 차례를 받았다 — 두 경로가 다른 레인을 본다:\n%s", out)
	}

	// 보고+반납도 같은 자리로 간다(kind·detail 이 닿는지까지 본다).
	frames = mcpServe(t, rig, mcpCall("land", map[string]any{
		"result": "fail", "detail": "MCP 쪽 사유",
	}))
	if text, isErr = mcpText(t, frames[0]); isErr {
		t.Fatalf("MCP land 보고가 실패했다:\n%s", text)
	}
	if h2, still := laneHolder(t, h); still && h2 == holder {
		t.Fatal("MCP 가 보고했는데 점유가 그대로다")
	}
	kind, detail := laneRowByID(t, h, rows[0].ID)
	if kind != string(model.LandingLeftFail) || detail != "MCP 쪽 사유" {
		t.Fatalf("MCP 보고가 원장에 %q/%q 로 남았다 — mcpbackend 의 본문 필드를 의심해라", kind, detail)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ⑤ 열화 — 넷이 각각 다른 사유로 거절된다. 캐시도 아웃박스도 아니다
// ─────────────────────────────────────────────────────────────────────────────

// 서버가 죽으면 land 는 **아무것도 하지 않는다.** 캐시된 "네 차례다"가 나오면 안 된다.
//
// ★ 이 시험이 지키는 것: 온라인에서 받은 "네 차례다"가 캐시에 들어가지 않는다.
// 들어가면 서버가 죽은 뒤 그 문장이 그대로 다시 나오고, 세션은 레인을 안 쥔 채 랜딩한다.
// 배타가 깨지는 것이 아니라 우회된다 — 서버는 내내 옳고 아무 로그도 안 남는다.
func TestLandIsNeverAnsweredFromCache(t *testing.T) {
	h := newHarness(t)
	if code, out := h.runAs("cc-lane-off", "land"); code != 0 {
		t.Fatalf("전제가 깨졌다 — 온라인 land 가 %d 다:\n%s", code, out)
	}

	// 캐시 파일 자체를 본다. "우연히 안 났다"와 "구조적으로 안 넣는다"를 가르는 자리다.
	cache := newCache(ResolveStateDir(envOf(h.env), ""))
	if _, err := cache.Get(landingPath); err == nil {
		t.Fatal("land 응답이 캐시에 들어갔다 — 서버가 죽으면 옛 '네 차례다'가 그대로 나온다")
	}

	h.down()
	code, out := h.runAs("cc-lane-off", "land")
	if code == 0 {
		t.Fatalf("서버가 죽었는데 land 가 0 으로 끝났다:\n%s", out)
	}
	if strings.Contains(out, "네 차례다") {
		t.Fatalf("미도달인데 '네 차례다'가 나왔다 — 캐시가 배타를 우회했다:\n%s", out)
	}
	mustContain(t, "미도달 land stdout", out, "배타의 정본이 서버의 DB 제약")

	// 반납·이탈·회수도 각각 제 사유로 거절된다. **아웃박스에 쌓이지 않는다.**
	for _, c := range []struct {
		args    []string
		mustSay string
	}{
		{[]string{"land", "--fail", "사유"}, "남의 점유를 반납한다"},
		{[]string{"land", "--leave", "사유"}, "남의 점유를 반납한다"},
		{[]string{"lane", "release", "--row", "1", "--reason", "사유"}, "사람의 판단이라 재생 대상이 아니다"},
	} {
		code, out := h.runAs("cc-lane-off", c.args...)
		if code == 0 {
			t.Errorf("%v 가 미도달에서 0 으로 끝났다:\n%s", c.args, out)
		}
		if !strings.Contains(out, c.mustSay) {
			t.Errorf("%v 의 거절 사유에 %q 가 없다:\n%s", c.args, c.mustSay, out)
		}
	}

	// ★ 원장 단정 — 아웃박스에 한 줄도 안 쌓였다.
	pend, err := newOutbox(envOf(h.env), h.home).List()
	if err != nil {
		t.Fatalf("아웃박스 조회 실패: %v", err)
	}
	if len(pend) != 0 {
		t.Fatalf("레인 명령이 아웃박스에 %d건 쌓였다 — 재생 시점에는 남이 잡고 있을 수 있다: %+v", len(pend), pend)
	}
}

// 열화 사유 넷이 **서로 다른 말을 한다.**
//
// 뭉개면 다음 사람이 그중 하나(대개 반납)만 아웃박스로 연다 — "어차피 놓을 건데
// 나중에 보내면 되지 않나"가 가장 그럴듯해 보이기 때문이다.
func TestLandDegradeReasonsAreDistinct(t *testing.T) {
	cases := []struct {
		cmd     string
		mustSay string
	}{
		{CmdLandAcquire, "배타의 정본이 서버의 DB 제약"},
		{CmdLandReport, "남의 점유를 반납한다"},
		{CmdLandLeave, "남의 점유를 반납한다"},
		{CmdLaneRelease, "사람의 판단이라 재생 대상이 아니다"},
	}
	seen := map[string]string{}
	for _, c := range cases {
		v := JudgeOffline(c.cmd)
		if v.Mode != OfflineRefuse {
			t.Errorf("%s 의 처방이 %q 다 — 넷 다 거절이어야 한다", c.cmd, v.Mode)
		}
		if !strings.Contains(v.Reason, c.mustSay) {
			t.Errorf("%s 의 사유에 %q 가 없다: %q", c.cmd, c.mustSay, v.Reason)
		}
		seen[c.cmd] = v.Reason
	}
	// 취득 · 반납/이탈 · 회수는 **세 가지 다른 사유**여야 한다.
	if seen[CmdLandAcquire] == seen[CmdLandReport] {
		t.Error("취득과 반납의 사유가 같다 — 두 거절은 다른 이유로 거절된다")
	}
	if seen[CmdLaneRelease] == seen[CmdLandReport] {
		t.Error("회수와 반납의 사유가 같다")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ⑥ 아웃박스 방어 — 적격 집합 == {note}, 적격 경로 == /api/v1/judgments
// ─────────────────────────────────────────────────────────────────────────────

// ★ 이 시험이 없으면 offline.go 의 아웃박스 가지에 낱말 하나를 끼워 넣는 것으로
// 새 명령이 아웃박스에 들어간다. 아웃박스는 **재연결 시점에** 재생되고,
// 그때 세상은 달라져 있다 — 그 레인은 이미 남이 잡고 있을 수 있다.
func TestOutboxEligibleSetIsExactlyNote(t *testing.T) {
	if ok, why := OutboxEligible("note", judgmentsPath); !ok {
		t.Fatalf("판단이 아웃박스 적격에서 빠졌다 — 오프라인 판단이 전부 사라진다: %s", why)
	}
	// 명령 축.
	for _, cmd := range []string{
		CmdLandAcquire, CmdLandReport, CmdLandLeave, CmdLaneRelease,
		"pick", "claim", "add", "finish", "alloc", "beat", "open", "move", "board", "",
	} {
		ok, why := OutboxEligible(cmd, judgmentsPath)
		if ok {
			t.Errorf("%q 가 아웃박스 적격으로 나왔다 — 적격 집합은 {note} 하나다", cmd)
		}
		if strings.TrimSpace(why) == "" {
			t.Errorf("%q 를 거절하면서 사유가 비었다", cmd)
		}
	}
	// 경로 축 — 명령 이름은 클라이언트가 붙이는 라벨이라 그것만 보면 우회가 생긴다.
	for _, p := range []string{
		landingPath, "/api/v1/items", "/api/v1/judgments/x", "/api/v2/judgments", "", "/",
	} {
		if ok, _ := OutboxEligible("note", p); ok {
			t.Errorf("note 라는 이름으로 %q 가 아웃박스에 들어간다", p)
		}
	}
	// 사유는 어떤 입력에도 비지 않는다.
	if _, why := OutboxEligible("note", judgmentsPath); strings.TrimSpace(why) == "" {
		t.Error("적격 판정의 사유가 비었다")
	}
}

// 두 정책이 어긋나면(JudgeOffline 은 쌓으라는데 적격 집합 밖) **아무것도 쌓지 않는다.**
//
// 이 시험은 그 어긋남을 실제로 만들어 본다 — JudgeOffline 을 고치지 않고,
// 아웃박스 처방이 붙은 명령(note)을 **다른 경로로** 보낸다.
func TestOutboxGuardRefusesWhenPoliciesDisagree(t *testing.T) {
	h := newHarness(t)
	cli := newClient(ResolveStateDir(envOf(h.env), ""), envOf(h.env), h.home, quietLogger())
	h.down()

	res, err := cli.Write(context.Background(), "note", landingPath, map[string]any{"x": 1})
	if err == nil {
		t.Fatal("적격 집합 밖 경로가 아웃박스에 쌓였다 — 재생이 그 경로를 다시 친다")
	}
	if res.Mode != OfflineRefuse {
		t.Errorf("처방이 %q 다 — 두 정책이 어긋났으면 거절이어야 한다", res.Mode)
	}
	if !strings.Contains(res.Reason, "적격") {
		t.Errorf("거절 사유가 무엇이 어긋났는지 말하지 않는다: %q", res.Reason)
	}
	pend, lerr := cli.Outbox.List()
	if lerr != nil {
		t.Fatalf("아웃박스 조회 실패: %v", lerr)
	}
	if len(pend) != 0 {
		t.Fatalf("거절했는데 아웃박스에 %d건이 쌓였다", len(pend))
	}
}

// 멱등 키는 **고정하지 않는다.** 고정하면 대기 중인 세션이 land 를 다시 부를 때
// 첫 응답("너는 3번째다")이 영원히 재생돼 차례가 왔는데도 안 온 것으로 보인다.
func TestLandKeysAreNeverStable(t *testing.T) {
	for _, cmd := range []string{CmdLandAcquire, CmdLandReport, CmdLandLeave, CmdLaneRelease} {
		stable, why := IdempotencyStable(cmd)
		if stable {
			t.Errorf("%s 의 멱등 키가 고정이다 — 낡은 자리가 재생된다: %s", cmd, why)
		}
		if strings.Contains(why, "모르는 명령") {
			t.Errorf("%s 가 표 밖으로 떨어졌다 — 아는 명령인데 사유가 %q 다", cmd, why)
		}
	}
	// 같은 인자로 두 번 만들어도 키가 달라야 한다(FreshKey 축).
	c := &Client{Session: "s1", Log: quietLogger()}
	k1 := c.KeyFor(CmdLandAcquire, landingPath, []byte(`{"mode":"acquire"}`))
	k2 := c.KeyFor(CmdLandAcquire, landingPath, []byte(`{"mode":"acquire"}`))
	if k1 == k2 {
		t.Fatalf("같은 본문의 land 두 번이 같은 키를 받았다(%q) — 둘째 호출이 첫 응답을 재생한다", k1)
	}
}

// 종료코드는 "요청이 성공했나"가 아니라 **"지금 랜딩해도 되는가"** 에 답한다. 순수 함수라 직접 부른다.
//
// ★ 모르는 상태를 0 으로 접으면 상태 낱말이 하나 늘어난 날 그 낱말이 조용히
// "랜딩해도 된다"가 된다. 그 조용함이 이 기능이 없애려는 사고 그 자체다.
func TestLandExitCodeAnswersWhetherYouMayLand(t *testing.T) {
	cases := []struct {
		state string
		want  int
	}{
		{"turn", 0},
		{"released", 0},
		{"left", 0},
		{"waiting", 1},
		{"reclaimed", 1},
		// 표 밖: 서버가 새 낱말을 내면 **랜딩하면 안 된다**로 접는다.
		{"", 1},
		{"granted", 1},
		{"TURN", 1},
	}
	for _, c := range cases {
		if got := LandExitCode(c.state); got != c.want {
			t.Errorf("LandExitCode(%q) = %d, 기대 %d", c.state, got, c.want)
		}
	}
}

// 회수한 사람을 **지어내지 않는다.** 이 값은 판단 본문에 불변으로 박힌다.
func TestLaneActorNeverInvents(t *testing.T) {
	if got := laneActor("사람", "cc-1", "u", "h"); got != "사람" {
		t.Errorf("명시한 actor 를 안 썼다: %q", got)
	}
	if got := laneActor("", "cc-1", "u", "h"); got != "cc-1" {
		t.Errorf("세션 좌표가 있는데 안 썼다: %q", got)
	}
	if got := laneActor("", "", "u", "h"); got != "u@h" {
		t.Errorf("사람 좌표 조립이 %q 다", got)
	}
	// ★ 아무것도 모르면 **빈 문자열**이다. "cli" 같은 고정 문자열로 채우면
	//   그 판단이 영원히 "모름"을 "cli 가 했음"으로 말한다.
	if got := laneActor("", "", "", ""); got != "" {
		t.Errorf("아무 좌표도 없는데 %q 를 지어냈다", got)
	}
}

// itoa 는 시험이 줄 행 번호를 CLI 인자로 넘길 때 쓴다.
func itoa(n int64) string { return strconv.FormatInt(n, 10) }
