package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 격리 집계 — **줄 수와 판단 수는 다른 수다.**
//
// ★ 이 파일이 지키는 것은 하나다: `fd doctor` 의 격리 줄이 **재지 않은 것을 잰 척하지 않는다.**
//
// 배경(재현으로 확정된 것이다. 인용 전에 이 문단을 읽어라):
// 겹친 재생 둘은 잠금 **밖에서** 각자 스냅숏을 뜨고(outbox.go:467) 각자 send 하고
// (outbox.go:482) 각자 영구 거절 판정을 받는다(outbox.go:488→102-105). 그래서 격리는
// 두 번 **결정**되고, 그 뒤에 무엇을 잠가도 append 는 두 번 난다. 300라운드 × 6판 실측:
//
//	기준선(오늘)                286·289·293·295·295·299 / 300 중복
//	quarantine 을 잠금 안으로     284·287·290·292·294·296 / 300 중복   ← 분포가 통째로 겹친다
//	손실(격리에도 큐에도 없음)     0/300, 모든 판
//
// 그리고 12판 전부에서 오차 0으로 성립한 항등식:
//
//	2×(두 줄 라운드) + 1×(한 줄 라운드) == send 호출 합계
//	→ 읽으면: **격리 줄 수 = 4xx 를 받은 send 횟수.** 잠금은 좌변도 우변도 안 건드린다.
//
// 그래서 이 축의 피해는 "잃는다"가 아니라 항목 스스로 적은 대로 "doctor 집계가 부푼다"이고,
// 처방은 **쓰기 경로가 아니라 세는 자리**에 있다. 아래가 그 자리를 붙든다.
//
// ★★ **개정: 위 중복은 그 뒤에 닫혔다 — 잠금이 아니라 이름으로.** 격리가 사건당 파일이
// 되면서 판별자(격리된 항목 전체 + 사유)가 파일 이름이 됐고, 겹친 재생 둘은 같은 이름을
// 얻어 둘째가 EEXIST 로 끝난다. 위 실측표는 **여전히 유효하다** — 그것이 말하는 것은
// "잠금으로는 못 닫는다"이고 그 판정은 안 바뀌었다. 바뀐 것은 다른 길이 있었다는 것이다.
// `TestConcurrentReplaysQuarantineTheSameEventOnlyOnce`(outbox_concurrency_test.go)가
// 그 완료 조건(300라운드에서 두 건 이상 0)을 두 형식 모두에 대해 붙든다.
// 아래 시험들은 **세는 자리**를 계속 붙든다: 옛 `rejected.jsonl` 은 비우는 경로가 없어
// 그 자리에 영원히 남고, 거기 든 중복은 오늘도 그대로다.

// seedRejected 는 격리 파일을 직접 심는다. (키, 사유) 쌍을 순서대로 받는다.
//
// ★ 순서를 인자로 받는 이유: 실제 배치 경합이 만드는 파일의 다수가 `A B A B`(흩어짐)이지
// `A A B B`(이웃)가 아니다 — 2건 큐·300라운드 실측에서 흩어진 중복이 142~199/300 이었다.
// 이웃만 접는 구현은 그 다수에 안 닿으므로, 심는 순서를 시험이 정할 수 있어야 한다.
func seedRejected(t *testing.T, dir string, pairs ...[2]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("격리 자리를 못 만들었다(%s): %v", dir, err)
	}
	var b strings.Builder
	for i, p := range pairs {
		r := RejectedEntry{
			Entry:  OutboxEntry{Key: p[0], At: time.Unix(int64(1700000000+i), 0).UTC(), Path: "/api/v1/judgments"},
			Reason: p[1],
			At:     time.Unix(int64(1700000000+i), 0).UTC(),
		}
		buf, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("격리 직렬화 실패: %v", err)
		}
		b.Write(buf)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, rejectedName), []byte(b.String()), 0o600); err != nil {
		t.Fatalf("격리 파일을 못 썼다: %v", err)
	}
}

// 흩어진 배치에서도 고유 판단 수를 센다.
//
// ★ **이 시험 하나가 두 가지 틀린 구현을 동시에 죽인다.** 둘 다 나머지 시험 전부와
// 패키지 전량을 초록으로 통과하는 것을 변이 실험으로 확인했다:
//
//   - **사유로 접는 판** — `JudgeReplayFailure` 의 4xx 가지는 사유를 상태코드와 서버
//     메시지로만 짓는다(outbox.go:102-105). 키도 경로도 안 들어간다. 그래서 **서로 다른
//     두 판단이 같은 400 을 받으면 사유가 바이트 동일**하고, 사유로 접으면 재생성 불가한
//     판단 둘이 한 건으로 보고된다 — doctor 가 **아래로** 거짓말하는 방향이다.
//   - **이웃만 접는 판** — 위 주석의 실측대로 실제 파일의 다수가 흩어져 있어서, 항목이
//     없애려던 부푼 수가 그대로 남는다.
//
// 그러니 판별자는 **파일 전체에 걸친 키 집합**이어야 한다.
func TestTallyRejectedCountsDistinctJudgmentsInAnInterleavedBatch(t *testing.T) {
	const same = "서버가 400 로 거절했다 — 같은 요청은 몇 번을 보내도 같은 답이다: 항목이 잠겨 있다"
	got := TallyRejected([]RejectedEntry{
		{Entry: OutboxEntry{Key: "s1:aaaa"}, Reason: same},
		{Entry: OutboxEntry{Key: "s1:bbbb"}, Reason: same},
		{Entry: OutboxEntry{Key: "s1:aaaa"}, Reason: same},
		{Entry: OutboxEntry{Key: "s1:bbbb"}, Reason: same},
	})
	if got.Lines != 4 {
		t.Errorf("줄 수를 %d 로 셌다 — 파일에 있는 그대로(4)여야 한다", got.Lines)
	}
	if got.Judgments != 2 {
		t.Errorf("고유 판단을 %d 건으로 셌다 — 키가 둘이니 2 다. "+
			"1 이면 사유로 접은 것이고(판단 하나를 지웠다), 4 면 이웃만 본 것이다", got.Judgments)
	}
}

// 키 없는 줄은 **절대 안 접는다.**
//
// ★ `settle`(outbox.go:361-371)이 같은 이유로 큐에서 빈 키 줄을 안 지운다: 빈 키끼리
// 서로 별칭이 되므로, 접으면 **서로 다른 판단들이 한 건으로** 뭉개진다. 세는 자리에서
// 그 방어를 되돌리면 doctor 가 아래로 거짓말한다 — 부푸는 것보다 나쁜 방향이다(설계 §9).
func TestTallyRejectedNeverFoldsKeylessLines(t *testing.T) {
	const same = "서버가 400 로 거절했다 — 같은 요청은 몇 번을 보내도 같은 답이다: 멱등 키가 비었다"
	got := TallyRejected([]RejectedEntry{
		{Entry: OutboxEntry{Key: ""}, Reason: same},
		{Entry: OutboxEntry{Key: ""}, Reason: same},
		{Entry: OutboxEntry{Key: ""}, Reason: same},
	})
	if got.Keyless != 3 {
		t.Errorf("키 없는 줄을 %d 로 셌다 — 3 이어야 한다(하나도 안 접는다)", got.Keyless)
	}
	if got.Judgments != 0 {
		t.Errorf("빈 키를 고유 판단 %d 건으로 셌다 — 빈 키는 판단을 식별하지 못하므로 0 이다", got.Judgments)
	}
	if got.Lines != 3 {
		t.Errorf("줄 수를 %d 로 셌다 — 3 이어야 한다", got.Lines)
	}
}

// doctor 는 **줄 수와 판단 수를 갈라** 낸다.
//
// ★ 오늘은 `len(rej)` 하나만 찍는다(cmds.go:852). 그래서 겹친 재생이 남긴 두 줄이
// "격리된 판단 2건"으로 나오고, 사람은 판단 둘이 거절된 줄 안다. 항목이 지목한 피해가
// 정확히 이것이다.
func TestDoctorSeparatesRejectedLinesFromDistinctJudgments(t *testing.T) {
	h := newHarness(t)
	dir, _ := OutboxPath(envOf(h.env), homeDir(envOf(h.env)))
	const same = "서버가 400 로 거절했다 — 같은 요청은 몇 번을 보내도 같은 답이다: 항목이 잠겨 있다"
	seedRejected(t, dir, [2]string{"s1:aaaa", same}, [2]string{"s1:aaaa", same})

	code, out := h.run("", "doctor")
	if code != 0 {
		t.Fatalf("doctor 가 %d 로 끝났다:\n%s", code, out)
	}
	if !strings.Contains(out, "격리 기록 2건 · 고유 판단 1건") {
		t.Errorf("doctor 가 줄 수와 판단 수를 안 갈랐다 — 겹친 재생의 두 줄이 판단 둘로 읽힌다:\n%s", out)
	}
}

// doctor 는 격리 줄마다 **키를 함께** 찍는다.
//
// ★ 안 찍으면 위 시험이 요구한 `2줄 · 1건` 을 사람이 화면에서 검증할 수 없다 — 어느 두
// 줄이 같은 판단인지 알 방법이 없기 때문이다. 집계만 바꾸고 근거를 안 내면 그 수는
// 도구를 믿으라는 요구가 된다.
func TestDoctorPrintsTheKeyOfEachRejectedLine(t *testing.T) {
	h := newHarness(t)
	dir, _ := OutboxPath(envOf(h.env), homeDir(envOf(h.env)))
	seedRejected(t, dir,
		[2]string{"s1:aaaa", "서버가 400 로 거절했다: 항목이 잠겨 있다"},
		[2]string{"s1:aaaa", "서버가 404 로 거절했다: 항목이 사라졌다"})

	code, out := h.run("", "doctor")
	if code != 0 {
		t.Fatalf("doctor 가 %d 로 끝났다:\n%s", code, out)
	}
	if !strings.Contains(out, "s1:aaaa") {
		t.Errorf("doctor 가 격리 줄의 키를 안 찍었다 — 건수의 근거가 화면에 없다:\n%s", out)
	}
	// ★ 두 사유가 **둘 다** 남아야 한다. 같은 키라고 파일에서 접었다면 나중 사실
	//   ("항목이 사라졌다")이 사라진다 — 그것이 쓰기 쪽 중복 제거를 거절한 이유다.
	if !strings.Contains(out, "항목이 잠겨 있다") || !strings.Contains(out, "항목이 사라졌다") {
		t.Errorf("같은 키의 두 사유 중 하나가 화면에 없다 — 거절 사건은 둘이었다:\n%s", out)
	}
}

// 키 없는 격리 줄은 **큐에서 안 빠졌다** — doctor 가 그 사실을 말한다.
//
// ★ 오늘 doctor 는 격리 줄 전체에 "영구 거절이라 큐에서 뺐다(버리지 않았다)"를 붙인다
// (cmds.go:852). 빈 키 줄에 대해서는 **거짓이다**: `settle`(outbox.go:366-371)이 그 줄을
// 일부러 큐에 남기고, `Replay`(481-482)에는 빈 키 가드가 없어 재생마다 다시 보내고 다시
// 거절당한다. `Flush` 는 모든 명령 앞에서 돌므로(client.go:357-358) 그 **전송**이 fd 호출마다
// 반복된다. 생성기는 여전히 열려 있다 — 닫으면 "그 판단이 영영 안 간다"는 §9 질문이 새로
// 열리고 그것은 독립 판정이다(후속으로 등록했다).
//
// ★ 앞선 판은 이 자리를 "격리 파일이 fd 호출당 한 줄씩 자란다 — 유일한 무한 증가원"이라고
// 적었다. **격리가 사건당 파일이 된 뒤로 그것은 거짓이다**: 같은 판단의 같은 거절은 한
// 이름이라 파일이 안 는다. 반복되는 것은 파일이 아니라 전송이고, 화면도 그렇게 말해야 한다.
func TestDoctorSaysKeylessRejectedLinesAreStillInTheQueue(t *testing.T) {
	h := newHarness(t)
	dir, _ := OutboxPath(envOf(h.env), homeDir(envOf(h.env)))
	seedRejected(t, dir,
		[2]string{"", "서버가 400 로 거절했다: 멱등 키가 비었다"},
		[2]string{"", "서버가 400 로 거절했다: 멱등 키가 비었다"})

	code, out := h.run("", "doctor")
	if code != 0 {
		t.Fatalf("doctor 가 %d 로 끝났다:\n%s", code, out)
	}
	if !strings.Contains(out, "키 없는 2건") {
		t.Errorf("doctor 가 키 없는 격리 줄을 갈라 안 냈다:\n%s", out)
	}
	if !strings.Contains(out, "큐에서 안 빠진다") {
		t.Errorf("doctor 가 '큐에서 뺐다'만 말한다 — 빈 키 줄에는 거짓이다:\n%s", out)
	}
	// ★ 여기서는 격리 줄이 **전부** 키 없는 줄이다. 그러면 큐에서 빠진 것이 하나도 없으므로
	//   머리줄이 "큐에서 뺐다"고 말하면 안 된다 — 아래 줄이 그것을 부정하는 화면은
	//   사람에게 둘 중 무엇이 참인지 고르게 시킨다.
	if strings.Contains(out, "큐에서 뺐다") {
		t.Errorf("격리 줄이 전부 키 없는데도 doctor 가 '큐에서 뺐다'고 말한다 — "+
			"그 줄들은 큐에 그대로 있다:\n%s", out)
	}
}

// 키 없는 줄은 재생마다 **다시 보내지고 다시 격리된다** — 큐에는 그대로 남는다.
//
// ★ 이것이 격리 파일의 유일한 무한 증가원이고, 이 시험은 그것을 **닫지 않고 못박는다.**
// 닫는 것("보내지 말고 큐에서도 빼라")은 "그 판단이 영영 안 간다"는 §9 질문을 새로 열어
// 독립 판정이 필요하다 — 후속으로 냈다. 여기서는 동작을 사실로 고정해서, 위쪽
// `settle` 의 경고 문구가 **다시 거짓으로 돌아가지 못하게** 한다: 그 문구는 오래
// "재생 대상에서 뺀다"라고 적혀 있었는데 `Replay` 에는 키 가드가 없다.
func TestKeylessLineIsResentAndRequarantinedOnEveryReplay(t *testing.T) {
	dir := t.TempDir()
	o := newOutboxAt(dir)
	// 키 없는 줄 하나. 손편집·부분 기록으로 들어오는 모양이다.
	if err := o.keep([]OutboxEntry{{Key: "", Path: "/api/v1/judgments", Body: []byte(`{}`)}}); err != nil {
		t.Fatalf("큐를 못 심었다: %v", err)
	}
	reject := func(_ context.Context, _ OutboxEntry) error {
		return parseAPIError(400, "/api/v1/judgments",
			[]byte(`{"error":{"code":"invalid_key","message":"멱등 키가 비었다"}}`))
	}

	const replays = 3
	for i := 0; i < replays; i++ {
		if _, err := o.Replay(t.Context(), reject); err != nil {
			t.Fatalf("%d번째 재생이 실패했다: %v", i+1, err)
		}
	}

	rej, err := o.Rejected()
	if err != nil {
		t.Fatalf("격리를 못 읽었다: %v", err)
	}
	// ★★ **이 단정은 뒤집힌 것이다.** 격리가 O_APPEND JSONL 이던 판에서는 재생마다 한 줄이
	// 늘어 `replays` 개였고, 그 증가가 이 파일의 유일한 무한 증가원이라고 적혀 있었다.
	// 격리가 **사건당 파일**이 되면서 같은 판단이 같은 사유로 다시 거절된 것은 같은 이름을
	// 얻어 EEXIST 로 끝난다 — **무한 증가가 닫혔다.**
	//
	// ★ 이것이 "빈 키를 접은 것"이 아닌 이유: 접힌 것은 **판단이 아니라 같은 사건의 반복**이다.
	// 그 판단은 여전히 큐에 남아 있고(아래 단정) 격리 자리에도 한 벌 있다. 서로 **다른**
	// 빈 키 판단 둘은 본문이 다르므로 판별자가 갈라 준다 — 바로 아래 시험이 그 축을 붙든다.
	if got := TallyRejected(rej); got.Keyless != 1 {
		t.Errorf("재생 %d회에 키 없는 격리가 %d 건이다 — 같은 판단의 같은 거절은 한 사건이라 "+
			"1 이어야 한다. 이 수가 재생 횟수를 따라가면 격리 자리가 fd 호출마다 자란다",
			replays, got.Keyless)
	}
	pend, err := o.List()
	if err != nil {
		t.Fatalf("큐를 못 읽었다: %v", err)
	}
	if len(pend) != 1 {
		t.Errorf("키 없는 줄이 큐에 %d 개 남았다 — settle 이 일부러 남기므로 1 이어야 한다", len(pend))
	}
}

// 서로 **다른** 빈 키 판단은 같은 사유로 거절돼도 **안 접힌다.**
//
// ★ 위 시험이 "같은 사건의 반복은 한 건"을 잰다면, 이 시험은 그 접기가 **어디서 멈추는지**를
// 잰다. 빈 키끼리는 서로 별칭이라(settle 이 같은 이유로 그 줄을 절대 안 지운다) 키만으로
// 판별하면 서로 다른 판단이 한 파일로 뭉개진다 — 세는 자리를 고치려다 판단을 잃는 것이고
// 그것이 §9 위반이다. 그래서 판별자에 **본문**이 들어간다.
func TestDifferentKeylessJudgmentsAreNeverFoldedTogether(t *testing.T) {
	dir := t.TempDir()
	o := newOutboxAt(dir)
	if err := o.keep([]OutboxEntry{
		{Key: "", Path: "/api/v1/judgments", Body: []byte(`{"body":"판단 하나"}`)},
		{Key: "", Path: "/api/v1/judgments", Body: []byte(`{"body":"판단 둘"}`)},
	}); err != nil {
		t.Fatalf("큐를 못 심었다: %v", err)
	}
	reject := func(_ context.Context, _ OutboxEntry) error {
		return parseAPIError(400, "/api/v1/judgments",
			[]byte(`{"error":{"code":"invalid_key","message":"멱등 키가 비었다"}}`))
	}
	if _, err := o.Replay(t.Context(), reject); err != nil {
		t.Fatalf("재생 실패: %v", err)
	}
	rej, err := o.Rejected()
	if err != nil {
		t.Fatalf("격리를 못 읽었다: %v", err)
	}
	if got := TallyRejected(rej); got.Keyless != 2 {
		t.Fatalf("서로 다른 빈 키 판단 둘이 %d 건으로 격리됐다 — 2 여야 한다. "+
			"뭉쳐졌다면 판별자가 본문을 안 보는 것이고, 그것은 남의 판단을 지운다", got.Keyless)
	}
	var bodies []string
	for _, r := range rej {
		bodies = append(bodies, string(r.Entry.Body))
	}
	for _, want := range []string{"판단 하나", "판단 둘"} {
		if !strings.Contains(strings.Join(bodies, "|"), want) {
			t.Errorf("격리에 %q 가 없다: %v", want, bodies)
		}
	}
}

// settle 의 경고는 **하지 않는 일을 약속하지 않는다.**
//
// ★ 그 문구는 오래 "재생 대상에서 뺀다(fd doctor 가 그 자리를 찍는다)"였고 둘 다 거짓이다:
// `Replay` 에 키 가드가 없고, doctor 는 대기 **건수**만 찍지 그 줄의 자리를 안 찍는다.
// 문구를 안 지키면 다음 사람이 "이미 처리된 줄"로 읽고 위 시험이 못박은 무한 증가를 못 본다.
func TestKeylessWarningDoesNotPromiseExclusionFromReplay(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	o := newOutboxAt(dir).withLogger(
		slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	if err := o.keep([]OutboxEntry{{Key: "", Path: "/api/v1/judgments"}}); err != nil {
		t.Fatalf("큐를 못 심었다: %v", err)
	}
	if _, err := o.settle(map[string]bool{}, "", 1); err != nil {
		t.Fatalf("settle 이 실패했다: %v", err)
	}

	got := buf.String()
	if !strings.Contains(got, "키 없는 줄이 큐에 있다") {
		t.Fatalf("빈 키 경고가 아예 안 나왔다:\n%s", got)
	}
	if !strings.Contains(got, "안 뺀다") {
		t.Errorf("경고가 '재생 대상에서 안 뺀다'는 사실을 말하지 않는다 — "+
			"그 줄은 재생마다 다시 보내진다:\n%s", got)
	}
}

// 옛 자리 줄에도 같은 축이 있다.
//
// ★ 이 자리를 빼면 처방이 **이 머신에 안 닿는다** — 실측: 이 머신의 격리는
// `~/.local/state/flightdeck/outbox/rejected.jsonl` 의 1줄이 전부이고 고정 자리
// (`~/.flightdeck/outbox`)에는 파일이 없다. 옛 자리 줄은 건수만 찍으므로(cmds.go:860)
// 여기서 안 가르면 겹친 재생의 흔적이 이 머신에서는 영영 안 보인다.
func TestDoctorSeparatesLegacyRejectedLinesFromDistinctJudgments(t *testing.T) {
	h := newHarness(t)
	legacyHome := filepath.Join(h.home, ".local", "state", "flightdeck", "outbox")
	const same = "서버가 400 로 거절했다: 항목이 잠겨 있다"
	seedRejected(t, legacyHome, [2]string{"old:aaaa", same}, [2]string{"old:aaaa", same})

	code, out := h.run("", "doctor")
	if code != 0 {
		t.Fatalf("doctor 가 %d 로 끝났다:\n%s", code, out)
	}
	if !strings.Contains(out, "격리 기록 2건 · 고유 판단 1건") {
		t.Errorf("옛 자리 줄이 줄 수와 판단 수를 안 갈랐다 — 이 머신의 격리가 전부 이 자리에 있다:\n%s", out)
	}
}
