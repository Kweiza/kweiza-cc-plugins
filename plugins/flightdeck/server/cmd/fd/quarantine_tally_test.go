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

// 키 없는 격리 기록은 **옛 것이다** — doctor 가 그 사실을 말한다.
//
// ★★ **이 시험도 뒤집혔다.** 앞선 판은 doctor 가 "그 줄은 큐에서 안 빠진다"를 말하는지
// 봤다. 그때는 참이었다 — 빈 키끼리 별칭이라 `settle` 이 지울 수 없었다. `fillMissingKeys`
// 가 읽는 자리에서 키를 채우면서 **새로 격리되는 줄에는 빈 키가 없다.** 그래서 이 수에
// 남아 세이는 것은 그 변경 **전에** 격리된 옛 줄뿐이다.
//
// ★ 이 자리를 안 고치면 화면이 오늘도 그 줄이 생긴다고 말한다 — 사람은 없는 결함을 쫓는다.
// 그리고 격리는 비우는 경로가 없어 그 옛 줄이 영원히 남으므로, 이 수는 **0 으로 안 간다.**
func TestDoctorSaysKeylessRejectedRecordsAreOld(t *testing.T) {
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
		t.Errorf("doctor 가 키 없는 격리 기록을 갈라 안 냈다:\n%s", out)
	}
	if !strings.Contains(out, "옛 기록") {
		t.Errorf("doctor 가 그 기록이 옛 것이라고 말하지 않는다 — 그대로 두면 사람이 "+
			"오늘도 빈 키 줄이 생기는 줄 알고 없는 결함을 쫓는다:\n%s", out)
	}
	// ★ 여기서는 격리가 **전부** 키 없는 기록이다. 그러면 큐에서 빠진 것이 하나도 없으므로
	//   머리줄이 "큐에서 뺐다"고 말하면 안 된다 — 화면 안에서 자기모순을 만들지 않는다.
	if strings.Contains(out, "큐에서 뺐다") {
		t.Errorf("격리가 전부 키 없는 기록인데도 doctor 가 '큐에서 뺐다'고 말한다:\n%s", out)
	}
}

// 키 없는 줄은 **키를 얻고 한 번만 나간다** — 재생마다 다시 보내지지 않는다.
//
// ★★ **이 시험은 뒤집힌 것이다.** 앞선 판의 이름은 `…IsResentAndRequarantinedOnEveryReplay`
// 였고, 재생 횟수만큼 다시 보내지는 것을 **사실로 못박는** 시험이었다. 그때는 그것이
// 참이었다: 빈 키끼리 서로 별칭이라 `settle` 이 그 줄을 지울 수 없었고(하나를 지우면
// 나머지가 함께 사라진다) 그래서 큐에 영원히 남아 매번 다시 나갔다.
//
// ★ `fillMissingKeys` 가 **별칭을 없애면서** 그 이유가 통째로 사라졌다. 이제 그 줄은
// 자기 키를 갖고 일반 경로로 간다 — 보내지고, 결과에 따라 지워지거나 격리된다.
// 여기서 재는 것은 **전송이 몇 번 일어나는가**다. 그 수가 재생 횟수를 따라가면
// 헛 POST 가 fd 호출마다 반복되던 상태로 돌아간 것이다.
func TestKeylessLineIsSentOnceAfterGettingAKey(t *testing.T) {
	dir := t.TempDir()
	o := newOutboxAt(dir)
	// 키 없는 줄 하나. 손편집·부분 기록으로 들어오는 모양이다.
	if err := o.keep([]OutboxEntry{{Key: "", Path: "/api/v1/judgments", Body: []byte(`{}`)}}); err != nil {
		t.Fatalf("큐를 못 심었다: %v", err)
	}
	sends := 0
	reject := func(_ context.Context, _ OutboxEntry) error {
		sends++
		return parseAPIError(400, "/api/v1/judgments",
			[]byte(`{"error":{"code":"invalid_key","message":"멱등 키가 비었다"}}`))
	}

	const replays = 3
	for i := 0; i < replays; i++ {
		if _, err := o.Replay(t.Context(), reject); err != nil {
			t.Fatalf("%d번째 재생이 실패했다: %v", i+1, err)
		}
	}

	// ★ 핵심 단정. 앞선 판에서는 이 값이 3 이었다(재생마다 한 번).
	if sends != 1 {
		t.Errorf("재생 %d회에 전송이 %d회다 — 1 이어야 한다. 이 수가 재생 횟수를 따라가면 "+
			"그 줄이 큐에서 안 빠지는 것이고, 헛 POST 가 fd 호출마다 난다", replays, sends)
	}
	pend, err := o.List()
	if err != nil {
		t.Fatalf("큐를 못 읽었다: %v", err)
	}
	if len(pend) != 0 {
		t.Errorf("큐에 %d건 남았다 — 격리됐으므로 0 이어야 한다. "+
			"앞선 판은 여기가 영영 1 이라 doctor 가 '대기 1건'을 계속 찍었다", len(pend))
	}
	// ★ **판단은 버려지지 않았다.** 큐에서 뺀 것과 없앤 것은 다르다(설계 §9).
	rej, err := o.Rejected()
	if err != nil {
		t.Fatalf("격리를 못 읽었다: %v", err)
	}
	if len(rej) != 1 {
		t.Fatalf("격리가 %d건이다 — 1 이어야 한다: %+v", len(rej), rej)
	}
	// ★ 격리된 줄은 **채워진 키**를 들고 있어야 한다 — 빈 키로 격리되면 그 줄은
	//   격리 자리에서도 여전히 별칭이고, TallyRejected 가 그것을 영영 못 센다.
	if rej[0].Entry.Key == "" {
		t.Error("격리된 줄의 키가 비었다 — 채운 키로 격리돼야 그 자리에서도 식별된다")
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
	// ★ 이제 둘 다 **채워진 키**를 들고 격리되므로 키 없는 건수는 0 이고 고유 판단은 2 다.
	//   앞선 판에서는 둘 다 빈 키로 격리돼 `Keyless=2 · Judgments=0` 이었다 —
	//   즉 격리 자리에서 그 둘은 **식별 불가**했다. 그것이 이 항목이 없앤 상태다.
	if got := TallyRejected(rej); got.Keyless != 0 || got.Judgments != 2 {
		t.Fatalf("서로 다른 빈 키 판단 둘이 키없음=%d · 고유판단=%d 로 격리됐다 — 0/2 여야 한다. "+
			"고유판단이 1 이면 둘이 같은 키를 얻은 것이고, 그것은 남의 판단을 지운다",
			got.Keyless, got.Judgments)
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

// 빈 키에 키를 부여한 것을 **말한다.** 조용히 고치지 않는다.
//
// ★ 이것은 파일 안의 값을 바꾸는 동작이다. 조용하면 다음 사람이 격리 파일에서 본 키가
// 어디서 왔는지 모른다 — 작성기가 만든 키와 구분이 안 된다.
//
// ★ 앞선 판의 이 시험은 경고가 "재생 대상에서 **안 뺀다**"를 말하는지 봤다. 그 문구는
// 그때 참이었고(그 줄은 큐에 남아 매번 다시 나갔다) 이제 거짓이다 — 키를 얻은 줄은
// 일반 경로로 가서 보내지고 빠진다.
func TestFillingAMissingKeyIsAnnounced(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	o := newOutboxAt(dir).withLogger(
		slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	if err := o.keep([]OutboxEntry{{Key: "", Path: "/api/v1/judgments"}}); err != nil {
		t.Fatalf("큐를 못 심었다: %v", err)
	}
	if _, err := o.List(); err != nil {
		t.Fatalf("조회가 실패했다: %v", err)
	}

	got := buf.String()
	if !strings.Contains(got, "키를 부여했다") {
		t.Fatalf("빈 키를 채우고도 아무 말을 안 했다 — 파일 안의 값을 조용히 바꾸면 안 된다:\n%s", got)
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

// ── 막지 않고 말한다 ─────────────────────────────────────────────────────────

// 격리된 키는 **다시 쌓을 수 있다.** Append 가 그것을 막지 않는다.
//
// ★★ 이것이 `fd-append-is-blind-to-quarantine-history` 의 판정이다. 막는 쪽이 자연스러워
// 보이지만 **4xx 사유에는 상태 의존이 실재한다** — `claim_held`("항목을 세션이 쥐고 있다") ·
// `missing_ref`("가리키는 좌표가 없다") · `item_closed`. 잠금은 풀리고 좌표는 나중에 생길
// 수 있다. 막으면 그 판단은 사람이 격리 파일을 손으로 뒤지기 전까지 **영영 안 간다**(설계 §9).
// 이 머신에 실제로 남아 있는 격리 1건이 바로 그 종류다(409 `missing_ref`).
//
// ★ 그리고 막는 검사는 값이 **O(격리 크기)** 다 — `Append` 를 O(1) 로 만든 작업을 되돌리고,
// 격리는 비우는 경로가 없어 계속 자란다. §9 를 안 어기는 피해를 §9 를 어기는 확률과
// 맞바꾸는 그 거래는 이 저장소가 이미 한 번 거절했다.
func TestAppendStillAcceptsAKeyThatWasAlreadyQuarantined(t *testing.T) {
	o := mkOutbox(t)
	e := entry("s1:once-rejected")
	if err := o.quarantine(RejectedEntry{
		Entry: e, Reason: "서버가 409 로 거절했다: 가리키는 좌표가 없다", At: o.stamp(),
	}); err != nil {
		t.Fatalf("격리를 못 심었다: %v", err)
	}
	if err := o.Append(e); err != nil {
		t.Fatalf("전에 격리된 키를 다시 못 쌓았다: %v — 막으면 잠금이 풀리거나 좌표가 "+
			"생겨도 그 판단은 영영 안 간다", err)
	}
	pend, err := o.List()
	if err != nil {
		t.Fatalf("큐를 못 읽었다: %v", err)
	}
	if len(pend) != 1 || pend[0].Key != e.Key {
		t.Fatalf("큐가 %+v 다 — 그 키 1건이어야 한다", pend)
	}
	// 격리 기록도 그대로 남아 있어야 한다. 다시 쌓는 것과 이력을 지우는 것은 다르다.
	rej, err := o.Rejected()
	if err != nil || len(rej) != 1 {
		t.Errorf("격리 기록이 %d건이다(err=%v) — 다시 쌓았다고 이력이 사라지면 안 된다", len(rej), err)
	}
}

// doctor 는 **전에 거절당한 판단이 큐에 다시 있다**고 말한다.
//
// ★ 막지 않는 대신 말한다. 이 자리는 **값이 0 이다** — doctor 는 큐와 격리를 이미 둘 다
// 읽었고, 여기서 하는 것은 두 집합의 교집합을 세는 것뿐이다. 안 말하면 사람은 그 판단이
// 전에 400 을 받았다는 사실을 알 방법이 없고, 같은 거절이 반복돼도 격리에 새 줄이
// 안 생기므로(같은 사건은 한 이름) **화면 어디에도 안 나온다.**
func TestDoctorNamesJudgmentsRejectedBeforeAndQueuedAgain(t *testing.T) {
	h := newHarness(t)
	dir, _ := OutboxPath(envOf(h.env), homeDir(envOf(h.env)))
	const key = "s1:comes-back"
	seedRejected(t, dir, [2]string{key, "서버가 409 로 거절했다: 가리키는 좌표가 없다"})
	o := newOutboxAt(dir)
	if err := o.Append(OutboxEntry{Key: key, At: time.Unix(1, 0).UTC(),
		Path: "/api/v1/judgments", Body: []byte(`{"kind":"decision","body":"다시 남긴다"}`)}); err != nil {
		t.Fatalf("큐에 못 쌓았다: %v", err)
	}

	code, out := h.run("", "doctor")
	if code != 0 {
		t.Fatalf("doctor 가 %d 로 끝났다:\n%s", code, out)
	}
	if !strings.Contains(out, "다시 있다") {
		t.Errorf("doctor 가 전에 거절당한 판단이 큐에 다시 있다는 것을 안 낸다:\n%s", out)
	}
	if !strings.Contains(out, key) {
		t.Errorf("doctor 가 그 키를 안 찍었다 — 어느 판단인지 없으면 사람이 확인할 자리가 없다:\n%s", out)
	}
	// ★ **막았다고 말하면 안 된다.** 그 판단은 큐에 그대로 있고 다음 재생이 보낸다.
	if strings.Contains(out, "막았다") || strings.Contains(out, "거절했다 — 이미") {
		t.Errorf("doctor 가 막았다고 말한다 — 막지 않는 것이 판정이다:\n%s", out)
	}
}

// ── 비우는 경로가 없는 자리 ──────────────────────────────────────────────────

// doctor 는 보관 자리의 **크기**와 **비우는 경로가 없다**는 사실을 함께 낸다.
//
// ★★ 이것이 `fd-rejected-and-failopen-files-have-no-retention-path` 의 판정이다.
// **회전도 상한도 안 만들었다**: 이 머신 실측(2026-08-11)에서 고정 자리에는 아웃박스
// 디렉토리 자체가 없고 옛 자리의 격리가 577바이트·1건, fail-open 기록은 없다 —
// **압력이 실물로 관측된 적이 없다.** 근거 없이 회전을 만들면 "어느 시점 이후를 못 본다"는
// 새 구멍이 열리고, 그것은 이 저장소가 없애려는 종류의 침묵이다.
//
// ★ 그래서 하는 것은 **그 자리를 화면에 두는 것**이다. 언젠가 커졌을 때 그 수가 거기 있고
// 그때 근거를 갖고 판정한다. 상한 없는 자리를 화면 밖에 두는 것이 이 항목이 지적한 결함이다.
func TestDoctorReportsRetentionSizeAndThatNothingClearsIt(t *testing.T) {
	h := newHarness(t)
	dir, _ := OutboxPath(envOf(h.env), homeDir(envOf(h.env)))
	seedRejected(t, dir, [2]string{"s1:aaaa", "서버가 400 로 거절했다: 본문이 비었다"})

	code, out := h.run("", "doctor")
	if code != 0 {
		t.Fatalf("doctor 가 %d 로 끝났다:\n%s", code, out)
	}
	if !strings.Contains(out, "보관 자리") {
		t.Fatalf("doctor 가 보관 자리를 안 낸다:\n%s", out)
	}
	if !strings.Contains(out, "비우는 경로가 없다") {
		t.Errorf("doctor 가 그 자리에 상한이 없다는 사실을 안 말한다 — 크기만 내면 "+
			"사람은 언젠가 저절로 줄어드는 줄 안다:\n%s", out)
	}
	// ★ 0 이 아니어야 한다 — 위에서 심은 격리가 실제로 세이는지까지 본다.
	//   문구만 있고 수가 0 이면 그 화면은 아무것도 안 재는 것과 같다.
	if strings.Contains(out, "격리 0B") {
		t.Errorf("격리를 심었는데 크기가 0B 다 — 재는 자리가 실제 파일에 안 닿는다:\n%s", out)
	}
}

// 보관 크기는 **두 형식을 합쳐** 잰다.
//
// ★ 격리를 사건당 파일로 옮겼어도 옛 `rejected.jsonl` 은 비우는 경로가 없어 그 자리에
// 남는다. 한쪽만 재면 그 잔량이 화면에서 사라지고, 사라진 잔량은 아무도 안 판정한다.
func TestRetentionCountsBothQuarantineFormats(t *testing.T) {
	o := mkOutbox(t)
	seedRejected(t, o.Dir(), [2]string{"s1:old", "옛 형식 줄"})
	legacyOnly := o.Retention()
	if legacyOnly.Rejected == 0 {
		t.Fatalf("옛 형식만 있을 때 격리 크기가 0 이다: %+v", legacyOnly)
	}
	if err := o.quarantine(RejectedEntry{
		Entry: entry("s1:new"), Reason: "새 형식 사건", At: o.stamp()}); err != nil {
		t.Fatalf("격리를 못 심었다: %v", err)
	}
	both := o.Retention()
	if both.Rejected <= legacyOnly.Rejected {
		t.Errorf("사건당 파일을 더했는데 크기가 %d → %d 다 — 두 형식을 합쳐 재야 한다",
			legacyOnly.Rejected, both.Rejected)
	}
}
