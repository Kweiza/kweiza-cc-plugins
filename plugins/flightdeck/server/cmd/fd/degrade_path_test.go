package main

import (
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 열화(L1) **전 경로** 통합 시험 — 설계 §7 이 못박은 자리다.
//
//	"서버를 죽인 상태에서 open → note → pick(캐시) → finish(아웃박스) → 재기동 → 재생
//	 전 경로가 성공하는 통합 시험을 둔다. 열화 경로는 안 돌리면 썩고,
//	 그러면 정확히 필요한 순간에 없다."
//
// degrade_test.go 는 **판정 하나씩**을 본다(오프라인 note 는 쌓이나 · pick 은 거절되나).
// 이 파일이 보는 것은 그 사이의 이음매다: L0 에서 만든 캐시가 L1 에서 실제로 쓰이는가 ·
// L1 에서 쌓인 것이 **재기동을 넘어** 원장에 정확히 한 번 들어가는가 ·
// 거절이 침묵이 아니라 그 자리에서 대안을 주는가.
//
// ★ 단정의 좌표계는 셋뿐이다 — **CLI stdout · 상태 디렉토리의 파일 · 서버가 실제로 갖게 된 원장**.
//   "함수가 오류를 안 냈다"는 이 파일 어디에도 없다.
//
// ★ 각 단계마다 **대조 전제를 결과보다 먼저** 단정한다. 전제를 안 세우면
//   "재생이 중복을 안 만들었다"와 "재전송이 아예 안 일어났다"가 같은 초록이 된다 —
//   앞 판의 이 자리가 정확히 그 거짓 초록이었다.

// withTimeBudget 는 이 시험이 얼마나 걸려도 되는지를 못박는다.
//
// 열화는 대기가 섞인 경로라 **느려도 된다.** 다만 상한이 없으면 "느려졌다"가
// 아무 데도 안 남고 회귀가 그대로 굳는다.
//
// ★ 이 함수는 하드 스톱이 아니다 — 진짜로 멈추는 것은 `go test -timeout`(기본 10분)이다.
// 여기서 하는 것은 **예산 초과를 그 시험의 실패로 남기는 것**이고, 그래서
// t.Errorf 다(t.Fatalf 는 정리 단계에서 부를 수 없다).
// 이 파일의 대기는 전부 상한이 있다: HTTP 는 클라이언트 타임아웃(FD_TIMEOUT, 기본 5초),
// 나머지는 파일 조작이라 대기 구간이 없다.
func withTimeBudget(t *testing.T, budget time.Duration) {
	t.Helper()
	start := time.Now()
	t.Cleanup(func() {
		if d := time.Since(start); d > budget {
			t.Errorf("이 시험이 %s 걸렸다 — 예산은 %s 다. 느려도 되지만 상한은 있다",
				d.Round(time.Millisecond), budget)
		}
	})
}

// degradeBudget 은 열화 시험 하나의 상한이다. 실측이 1초 안쪽이라 넉넉히 잡았다 —
// 빡빡하게 잡으면 붐비는 머신에서 이 시험이 조정 결함과 무관하게 빨간불을 낸다.
const degradeBudget = 60 * time.Second

// ─────────────────────────────────────────────────────────────────────────────
// ① L0 → L1 → L0 왕복
// ─────────────────────────────────────────────────────────────────────────────

func TestDegradedFullPathL0ToL1AndBack(t *testing.T) {
	withTimeBudget(t, degradeBudget)
	h := newHarness(t)
	const itemID = "t7-degrade"

	// ── L0: 서버가 살아 있는 구간. 여기서 만든 것이 L1 의 재료다 ──
	if code, out := h.run("", "open"); code != 0 {
		t.Fatalf("세션 열기 실패(%d): %s", code, out)
	}
	if code, out := h.run("", "add", "--id", itemID, "--title", "열화 경로",
		"--body", "서버가 죽어도 판단은 잃지 않는다", "--path", "server/"); code != 0 {
		t.Fatalf("항목 등록 실패(%d): %s", code, out)
	}
	// 읽기 캐시를 **일부러** 채운다. 안 채우면 아래 "L1 에서 캐시가 나온다"가
	// "원래 캐시가 없었다"와 구분되지 않는다.
	if code, out := h.run("", "status"); code != 0 {
		t.Fatalf("온라인 status 실패(%d): %s", code, out)
	}

	sd := ResolveStateDir(envOf(h.env), "")
	ob := newOutbox(sd)

	// ── 대조 전제 ①: 서버가 정말 이 항목을 갖고 있다 ──
	// 없으면 아래의 선점 거절이 "오프라인이라 거절"이 아니라 "없는 항목이라 실패"가 된다.
	if _, err := h.st.GetItem(t.Context(), h.project, itemID); err != nil {
		t.Fatalf("전제가 깨졌다 — 서버에 항목 %s 가 없다: %v", itemID, err)
	}
	// ── 대조 전제 ②: 아웃박스가 비어 있다. 여기서 늘어난 것만이 L1 이 쌓은 것이다 ──
	if pend, err := ob.List(); err != nil || len(pend) != 0 {
		t.Fatalf("전제가 깨졌다 — 시작부터 아웃박스에 %d건이다(err=%v)", len(pend), err)
	}
	// ── 대조 전제 ③: 캐시가 정말 생겼다 ──
	if got := h.cacheFiles(sd); len(got) == 0 {
		t.Fatal("전제가 깨졌다 — 온라인 읽기 뒤에도 캐시 파일이 하나도 없다")
	}

	// ── L1: 서버를 죽인다. down() 이 "정말 미도달인가"를 결과보다 먼저 단정한다 ──
	h.down()

	// ① note — 판단은 재생성 불가한 유일한 자산이라 **종료코드 0** 이어야 한다.
	for _, body := range []string{"왜 이 순서로 했나", "무엇을 기각했나"} {
		code, out := h.run("", "note", "--kind", "decision", "--body", body)
		if code != 0 {
			t.Fatalf("오프라인 note 가 실패했다(%d): %s", code, out)
		}
		mustContain(t, "오프라인 note stdout", out, "아웃박스에 쌓았다")
	}

	// ② 읽기 — 캐시 + **낡음 배너**. 침묵하면 낡은 값이 현재 사실인 척한다.
	code, out := h.run("", "status")
	if code != 0 {
		t.Fatalf("오프라인 status 가 실패했다(%d):\n%s", code, out)
	}
	mustContain(t, "오프라인 status stdout", out,
		"⚠ 조정 서버 미도달",
		"되는 것: 코드 작성·커밋·조사 전부",
		"안 되는 것: 새 항목 선점",
	)

	// ③ 선점 — **거절**. 조용히 성공하면 배타가 그 자리에서 거짓이 된다.
	code, out = h.run("", "pick", itemID)
	if code == 0 {
		t.Fatalf("오프라인 선점이 성공으로 끝났다 — 배타는 서버만 보장할 수 있다:\n%s", out)
	}
	mustContain(t, "오프라인 pick stdout", out,
		"선점은 오프라인에서 안 된다",
		"배타는 서버만 보장할 수 있",
	)

	// ④ finish — 거절하되 **그 자리에서 대안을 준다.**
	//
	// ★ 설계 §7 의 한 줄 요약은 `finish(아웃박스)` 라고 적혀 있다. 그러나 같은 절의
	//   규범 문장은 "쓰기: judgment·note 만 아웃박스에 쌓이고 재연결 시 멱등 재생"이고,
	//   finish 는 판단 저장 + 후속 등록 + 종료 + 자원 반납을 한 트랜잭션으로 하는 합성 쓰기다.
	//   반쪽만 쌓으면 그 원자성이 거짓이 된다. 구현은 규범 문장을 따라 거절하고,
	//   이 시험은 **구현의 실제 동작**을 단정한다 — 다만 거절이 막다른 길이 되면 안 되므로
	//   "판단만 남기려면 note 를 써라"가 같은 화면에 있는지까지 본다.
	code, out = h.run("", "finish", itemID, "--outcome", "done", "--body", "핸드오프 본문")
	if code == 0 {
		t.Fatalf("오프라인 finish 가 성공으로 끝났다 — 원자성이 거짓이 된다:\n%s", out)
	}
	mustContain(t, "오프라인 finish stdout", out,
		"한 트랜잭션",
		"판단만 남기려면 note 를 써라",
	)

	// ⑤ 아웃박스에 **판단만** 들어갔는가. 선점·마무리가 새면 재생될 때 배타가 깨진다.
	pend, err := ob.List()
	if err != nil {
		t.Fatalf("아웃박스 조회 실패: %v", err)
	}
	if len(pend) != 2 {
		t.Fatalf("아웃박스가 %d건이다 — 판단 2건만 쌓여야 한다: %s", len(pend), outboxPaths(pend))
	}
	for _, e := range pend {
		if e.Path != "/api/v1/judgments" {
			t.Fatalf("판단이 아닌 쓰기가 아웃박스에 샜다: %s — 재생되면 그때 배타가 깨진다", e.Path)
		}
	}

	// ── 대조 전제 ④: 재생 전에 서버에 정말 없다 ──
	// 이 단정은 DB 를 닫기 **전**에 해야 한다(아래 재기동이 DB 를 닫고 다시 연다).
	if got := len(h.judgments(model.JudgmentDecision)); got != 0 {
		t.Fatalf("전제가 깨졌다 — 서버가 죽은 동안 판단 %d건이 들어갔다", got)
	}

	// ── L0 복귀: **컨테이너 교체**다. 볼륨은 그대로, 프로세스는 새로 ──
	h.restartProcess()

	code, out = h.run("", "status")
	if code != 0 {
		t.Fatalf("복귀 후 status 실패(%d): %s", code, out)
	}
	if strings.Contains(out, "조정 서버 미도달") {
		t.Fatalf("서버가 살아났는데 미도달 배너가 남아 있다:\n%s", out)
	}

	// ── 원장 대조: 쌓인 것이 전부, 정확히 한 번 들어갔는가 ──
	js := h.judgments(model.JudgmentDecision)
	if len(js) != 2 {
		t.Fatalf("재생 후 판단이 %d건이다 — 2건이어야 한다", len(js))
	}
	mustContain(t, "재생된 판단 본문", js[0].Body+"|"+js[1].Body, "왜 이 순서로 했나", "무엇을 기각했나")
	if pend, err := ob.List(); err != nil || len(pend) != 0 {
		t.Fatalf("재생 뒤에도 아웃박스에 %d건 남았다(err=%v)", len(pend), err)
	}

	// ── 왕복이 닫혔는가: L1 에서 거절당한 선점이 L0 에서는 실제로 된다 ──
	if code, out := h.run("", "pick", itemID); code != 0 {
		t.Fatalf("복귀 후 선점 실패(%d): %s", code, out)
	}
	cl, err := h.st.GetClaim(t.Context(), h.project, itemID)
	if err != nil || cl.ReleasedAt != nil {
		t.Fatalf("복귀 후 선점 행이 없다: %+v %v", cl, err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ② 아웃박스 재생은 **정확히 한 번** — 재생 도중 프로세스가 죽어도
// ─────────────────────────────────────────────────────────────────────────────

// 재생은 "보낸다 → 파일을 절단한다" 두 단계다. 그 사이에서 프로세스가 죽으면
// **이미 보낸 것이 파일에 그대로 남고**, 다음 실행이 같은 항목을 다시 보낸다.
// 그때 중복을 막을 수 있는 것은 서버의 멱등뿐이고, 판단은 추가 전용이라
// 한 번 들어간 중복은 되돌릴 방법이 없다.
//
// ★ 앞 판의 이 자리는 거짓 초록이었다: 아웃박스가 이미 비어 있어 재전송이 아예 안 일어났고,
// 그래서 "판단이 그대로"는 멱등의 근거가 아니라 "아무 일도 안 일어남"의 근거였다.
// 그 상태에서는 서버가 멱등 기억을 통째로 잃어도 시험이 초록이다.
// 그래서 여기서는 ⓐ 아웃박스 파일 **원문을 되돌려** 죽음을 흉내 내고,
// ⓑ 서버를 **컨테이너 교체**로 재기동하며(메모리 층이 통째로 사라진다),
// ⓒ 결과를 읽기 전에 **재전송이 정말 일어났는지**를 서버 지표로 먼저 단정한다.
func TestOutboxReplayIsExactlyOnceAcrossProcessDeathAndRestart(t *testing.T) {
	withTimeBudget(t, degradeBudget)
	h := newHarness(t)
	if code, out := h.run("", "open"); code != 0 {
		t.Fatalf("세션 열기 실패(%d): %s", code, out)
	}
	h.down()

	bodies := []string{"판단 하나", "판단 둘", "판단 셋"}
	for _, b := range bodies {
		if code, out := h.run("", "note", "--kind", "decision", "--body", b); code != 0 {
			t.Fatalf("오프라인 note 실패(%d): %s", code, out)
		}
	}

	sd := ResolveStateDir(envOf(h.env), "")
	ob := newOutbox(sd)
	outboxPath := filepath.Join(sd.sub("outbox"), "pending.jsonl")

	// ── 대조 전제 ①: 정말 3건이 쌓였다 ──
	pend, err := ob.List()
	if err != nil {
		t.Fatalf("아웃박스 조회 실패: %v", err)
	}
	if len(pend) != len(bodies) {
		t.Fatalf("전제가 깨졌다 — 아웃박스가 %d건이다(%d건이어야 한다)", len(pend), len(bodies))
	}
	// 파일 원문을 뜬다. 항목을 다시 Append 하는 것과 다른 축이다 —
	// Append 는 도구를 거쳐 **새로 쌓는 것**이고, 여기서 흉내 내려는 것은
	// "보냈는데 절단 전에 죽어 파일이 그대로 남은 상태"다.
	snapshot, err := os.ReadFile(outboxPath)
	if err != nil {
		t.Fatalf("아웃박스 파일을 못 읽었다(%s): %v", outboxPath, err)
	}
	if len(snapshot) == 0 {
		t.Fatal("전제가 깨졌다 — 아웃박스 파일이 비어 있다")
	}
	// ── 대조 전제 ②: 재생 전 서버에 판단이 0건 ──
	if got := len(h.judgments(model.JudgmentDecision)); got != 0 {
		t.Fatalf("전제가 깨졌다 — 재생 전인데 판단이 %d건이다", got)
	}

	// ── 1차 재생: 컨테이너 교체로 살린다 ──
	h.restartProcess()
	if code, out := h.run("", "status"); code != 0 {
		t.Fatalf("1차 재생(status) 실패(%d): %s", code, out)
	}
	if got := len(h.judgments(model.JudgmentDecision)); got != len(bodies) {
		t.Fatalf("1차 재생 뒤 판단이 %d건이다 — %d건이어야 한다", got, len(bodies))
	}
	if left, err := ob.List(); err != nil || len(left) != 0 {
		t.Fatalf("1차 재생 뒤 아웃박스에 %d건 남았다(err=%v)", len(left), err)
	}

	// ── 재생 도중 죽음: 보낸 뒤 절단 전에 죽으면 파일이 그대로다 ──
	if err := os.WriteFile(outboxPath, snapshot, 0o600); err != nil {
		t.Fatalf("아웃박스 원문 복원 실패: %v", err)
	}
	// ── 대조 전제 ③: 되돌린 것이 **같은 키**인가. 키가 다르면 멱등이 아니라 새 요청이다 ──
	again, err := ob.List()
	if err != nil {
		t.Fatalf("아웃박스 조회 실패: %v", err)
	}
	if len(again) != len(pend) {
		t.Fatalf("전제가 깨졌다 — 복원 뒤 아웃박스가 %d건이다", len(again))
	}
	for i := range again {
		if again[i].Key != pend[i].Key {
			t.Fatalf("전제가 깨졌다 — 복원한 키가 다르다(%q vs %q)", again[i].Key, pend[i].Key)
		}
	}

	// ── 서버도 재기동한다. 멱등 기억이 프로세스 메모리뿐이면 여기서 사라진다 ──
	h.restartProcess()
	if code, out := h.run("", "status"); code != 0 {
		t.Fatalf("2차 재생(status) 실패(%d): %s", code, out)
	}

	// ── 대조 전제 ④(결과를 읽기 **전에**): 재전송이 정말 일어났는가 ──
	//
	// 좌표계는 아웃박스 파일이다. Outbox.Replay 는 **성공한 것만** 지우므로,
	// 파일이 비었다는 것은 3건이 실제로 서버에 나가서 전부 2xx 를 받았다는 뜻이다.
	// 이 축은 서버가 그것을 새로 만들었든 재생으로 접었든 **똑같이 성립한다** —
	// 그래서 아래의 본 판정과 독립이고, 전제로 쓸 수 있다.
	if left, err := ob.List(); err != nil || len(left) != 0 {
		t.Fatalf("전제가 깨졌다 — 2차 재생 뒤 아웃박스에 %d건 남았다(err=%v). "+
			"재전송이 안 끝났으면 아래 단정은 아무것도 안 본다", len(left), err)
	}

	// ── 본 판정: 판단은 추가 전용이라 중복이 들어가면 되돌릴 수 없다 ──
	js := h.judgments(model.JudgmentDecision)
	if len(js) != len(bodies) {
		t.Fatalf("같은 항목을 다시 배달했더니 판단이 %d건이 됐다 — %d건이어야 한다.\n"+
			"서버는 방금 재기동했으므로 이것을 막을 수 있는 것은 **DB 로 내려간 멱등 기억**뿐이다.",
			len(js), len(bodies))
	}

	// ── 왜 안 늘었나: "서버가 접었다"와 "요청이 애초에 안 갔다"를 가른다 ──
	// 위 전제가 요청이 간 것을 이미 보였으므로, 이 값은 그 3건이 **재생으로 접혔다**는
	// 사실을 못박는다. 지표는 재기동으로 0 에서 시작한 새 인스턴스의 것이다.
	metrics := h.metricsText()
	replays, ok := metricValue(metrics, "flightdeck_idempotent_replays_total")
	if !ok {
		t.Fatalf("/metrics 에 재생 계열이 없다:\n%s", clip(metrics, 2000))
	}
	if int(replays) != len(bodies) {
		t.Fatalf("서버가 재생으로 접은 요청이 %d건이다 — %d건이어야 한다", int(replays), len(bodies))
	}
	if conflicts, ok := metricValue(metrics, "flightdeck_idempotent_conflicts_total"); ok && conflicts != 0 {
		t.Fatalf("같은 키에 다른 요청으로 거절된 것이 %d건이다 — 재생 본문이 첫 요청과 달라졌다", int(conflicts))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ③ 상태 파일은 ${CLAUDE_PLUGIN_DATA} 아래다 — ${CLAUDE_PLUGIN_ROOT} 가 아니다
// ─────────────────────────────────────────────────────────────────────────────

// ${CLAUDE_PLUGIN_ROOT} 에는 플러그인 **버전이 들어간다**(설계 §13). 갱신되면 경로가
// 바뀌고 옛 자리는 지워지므로, 거기 쌓인 판단은 갱신 한 번에 사라진다.
//
// ★ 단정의 좌표계는 판정 함수가 아니라 **파일시스템에 실제로 생긴 파일**이다.
// ResolveStateDir 단위 시험은 "무엇을 고르는가"만 보고, 소비자(캐시·아웃박스)가
// 그 값을 실제로 쓰는지는 원리적으로 못 본다.
func TestOfflineStateLandsUnderPluginDataNotPluginRoot(t *testing.T) {
	withTimeBudget(t, degradeBudget)
	h := newHarness(t)

	base := t.TempDir()
	data := filepath.Join(base, "plugin-data")
	root := filepath.Join(base, "plugin-root", "flightdeck", "0.1.0") // 갱신마다 바뀌는 자리
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("PLUGIN_ROOT 흉내를 못 만들었다: %v", err)
	}

	// ★ 손으로 env 를 만들지 않는다 — runEnv 주석이 경고하는 그대로, 손으로 만들면
	// HOME 을 잊고 시험이 진짜 홈을 건드린다(실측 2026-08-05).
	// unpinnedEnv 가 FD_STATE_DIR 를 빼고 가짜 홈을 함께 주는 정식 갈래다.
	env := h.unpinnedEnv(map[string]string{
		"CLAUDE_PLUGIN_DATA": data,
		"CLAUDE_PLUGIN_ROOT": root,
	})
	if sd := ResolveStateDir(envOf(env), ""); sd.Path != filepath.Join(data, "flightdeck") {
		t.Fatalf("전제가 깨졌다 — 이 환경의 상태 디렉토리가 %q 다(%q 를 기대했다, 사유 %q)",
			sd.Path, filepath.Join(data, "flightdeck"), sd.Source)
	}

	// ── L0 읽기: 캐시가 PLUGIN_DATA 아래 생긴다 ──
	if code, out := h.runEnv(env, "", "status"); code != 0 {
		t.Fatalf("온라인 status 실패(%d): %s", code, out)
	}
	cacheDir := filepath.Join(data, "flightdeck", "cache")
	ents, err := os.ReadDir(cacheDir)
	if err != nil || len(ents) == 0 {
		t.Fatalf("캐시가 %s 아래 생기지 않았다(err=%v, %d개)", cacheDir, err, len(ents))
	}

	// ── L1 쓰기: 아웃박스도 그 아래 ──
	h.down()
	if code, out := h.runEnv(env, "", "note", "--kind", "decision", "--body", "경로 축 시험"); code != 0 {
		t.Fatalf("오프라인 note 실패(%d): %s", code, out)
	}
	pending := filepath.Join(data, "flightdeck", "outbox", "pending.jsonl")
	if _, err := os.Stat(pending); err != nil {
		t.Fatalf("아웃박스가 %s 에 없다 — 판단이 어디에 쌓였는지 모른다: %v", pending, err)
	}

	// ── 본 판정: PLUGIN_ROOT 아래에는 **아무것도** 안 생겨야 한다 ──
	var strays []string
	werr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			strays = append(strays, p)
		}
		return nil
	})
	if werr != nil {
		t.Fatalf("PLUGIN_ROOT 순회 실패: %v", werr)
	}
	if len(strays) > 0 {
		t.Fatalf("${CLAUDE_PLUGIN_ROOT} 아래에 파일이 생겼다 — 플러그인 갱신 한 번에 사라진다: %v", strays)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ④ SessionStart 는 L1 에서도 배너를 내고, 어떤 입력에도 종료코드 0 이다
// ─────────────────────────────────────────────────────────────────────────────

// hook_test.go 의 fail-open 표는 **아웃박스가 빈** 상태에서만 돈다.
// 여기서는 "못 보낸 판단이 쌓여 있는" 상태 — 즉 L1 이 실제로 오래 지속된 상태 — 에서
// 같은 축을 다시 본다. 그리고 그 사실이 additionalContext 에 문장으로 나오는지 단정한다:
// 안 나오면 세션은 자기 머신에 미배달 판단이 있다는 것을 알 길이 없다.
func TestSessionStartUnderL1ReportsPendingJudgmentsAndStaysFailOpen(t *testing.T) {
	withTimeBudget(t, degradeBudget)
	h := newHarness(t)
	if code, out := h.run("", "open"); code != 0 {
		t.Fatalf("세션 열기 실패(%d): %s", code, out)
	}
	h.down()

	for _, b := range []string{"막힌 이유", "기각한 대안"} {
		if code, out := h.run("", "note", "--kind", "decision", "--body", b); code != 0 {
			t.Fatalf("오프라인 note 실패(%d): %s", code, out)
		}
	}
	// ── 대조 전제: 정말 2건이 쌓였다 ──
	pend, err := newOutbox(ResolveStateDir(envOf(h.env), "")).List()
	if err != nil || len(pend) != 2 {
		t.Fatalf("전제가 깨졌다 — 아웃박스가 %d건이다(err=%v)", len(pend), err)
	}

	code, out := h.run(`{"session_id":"cc-session-uuid-1","cwd":"/tmp","hook_event_name":"SessionStart"}`,
		"hook", "session-start")
	if code != 0 {
		t.Fatalf("SessionStart 훅이 종료코드 %d 를 냈다 — 세션이 안 뜬다:\n%s", code, out)
	}
	ctxText := additionalContext(t, out)
	mustContain(t, "L1 additionalContext", ctxText,
		"⚠ 조정 서버 미도달",
		"안 되는 것: 새 항목 선점",
		"아직 못 보낸 판단 2건",
		"내 선점:",
	)
	// 배너는 **맨 앞**이다. 뒤에 있으면 긴 보드에 묻힌다.
	if !strings.HasPrefix(strings.TrimSpace(ctxText), "⚠") {
		t.Fatalf("배너가 맨 앞이 아니다:\n%s", ctxText)
	}

	// 아웃박스가 차 있는 상태에서도 어떤 입력에도 0 이다.
	for _, in := range []struct{ name, stdin string }{
		{"빈 stdin", ""},
		{"깨진 JSON", "{이건 JSON 이 아니다"},
		{"session_id 가 숫자", `{"session_id":123}`},
	} {
		if code, out := h.run(in.stdin, "hook", "session-start"); code != 0 {
			t.Fatalf("아웃박스가 차 있을 때 %s 로 종료코드 %d 가 났다:\n%s", in.name, code, out)
		}
	}
	// 훅은 재생을 시도만 하고 실패한다 — 판단이 사라지면 안 된다.
	if left, err := newOutbox(ResolveStateDir(envOf(h.env), "")).List(); err != nil || len(left) != 2 {
		t.Fatalf("훅을 돌린 뒤 아웃박스가 %d건이다 — 미도달인데 판단이 사라졌다(err=%v)", len(left), err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 보조
// ─────────────────────────────────────────────────────────────────────────────

// metricValue 는 Prometheus 노출 포맷에서 계열 하나의 값을 뽑는다. 순수 함수다.
//
// ★ **없음과 0 을 가른다.** 없는 계열에 0 을 돌려주면 "재전송이 0건이었다"와
// "그 계열을 이 서버가 아예 안 낸다"가 같은 값이 되고, 그러면 대조 전제가
// 조용히 성립한 것처럼 보인다 — 대조가 조용히 실패하면서 기대한 숫자를 그대로 내는 모양이다.
func metricValue(text, series string) (float64, bool) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.LastIndex(line, " ")
		if i < 0 {
			continue
		}
		if strings.TrimSpace(line[:i]) != series {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(line[i+1:]), 64)
		if err != nil {
			return 0, false
		}
		return v, true
	}
	return 0, false
}

func TestMetricValueSeparatesMissingFromZero(t *testing.T) {
	const doc = "# HELP x 설명\n# TYPE x counter\nx 0\n" +
		"flightdeck_idempotent_replays_total 3\n" +
		"flightdeck_requests_total{route=\"POST /api/v1/judgments\",status=\"201\"} 7\n"
	cases := []struct {
		series string
		want   float64
		ok     bool
	}{
		{"x", 0, true},
		{"flightdeck_idempotent_replays_total", 3, true},
		{`flightdeck_requests_total{route="POST /api/v1/judgments",status="201"}`, 7, true},
		{"없는_계열", 0, false},
		// ★ 표 밖: 접두가 같은 다른 계열을 잘못 집으면 안 된다.
		{"flightdeck_idempotent", 0, false},
		{"flightdeck_requests_total", 0, false},
	}
	for _, c := range cases {
		got, ok := metricValue(doc, c.series)
		if ok != c.ok || got != c.want {
			t.Fatalf("%q → (%v, %v), (%v, %v) 를 기대했다", c.series, got, ok, c.want, c.ok)
		}
	}
}

// metricsText 는 **지금 붙어 있는 서버 인스턴스**의 /metrics 원문이다.
// 재기동하면 이 값이 0 에서 다시 시작하므로, 재기동 뒤의 요청 수를 그대로 셀 수 있다.
func (h *harness) metricsText() string {
	h.t.Helper()
	res, err := h.srv.Client().Get(h.srv.URL + "/metrics")
	if err != nil {
		h.t.Fatalf("/metrics 를 못 읽었다: %v", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		h.t.Fatalf("/metrics 본문 읽기 실패: %v", err)
	}
	if res.StatusCode != 200 {
		h.t.Fatalf("/metrics 가 %d 를 냈다: %s", res.StatusCode, clip(string(body), 300))
	}
	return string(body)
}

// cacheFiles 는 상태 디렉토리에 실제로 생긴 캐시 파일 목록이다.
func (h *harness) cacheFiles(sd StateDir) []string {
	h.t.Helper()
	ents, err := os.ReadDir(sd.sub("cache"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			out = append(out, e.Name())
		}
	}
	return out
}

func outboxPaths(es []OutboxEntry) string {
	var b strings.Builder
	for i, e := range es {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(e.Path)
	}
	return b.String()
}

// additionalContext 는 훅 stdout 에서 에이전트가 실제로 읽는 본문을 꺼낸다.
func additionalContext(t *testing.T, stdout string) string {
	t.Helper()
	var payload struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &payload); err != nil {
		t.Fatalf("훅 stdout 이 JSON 이 아니다: %v\n%s", err, stdout)
	}
	if payload.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Fatalf("hookEventName 이 %q 다", payload.HookSpecificOutput.HookEventName)
	}
	return payload.HookSpecificOutput.AdditionalContext
}
