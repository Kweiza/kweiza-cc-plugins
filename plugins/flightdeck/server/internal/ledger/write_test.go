package ledger

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/store"
)

// 쓰기는 원자적이다 — tmp 에 쓰고 rename 한다. 중간에 죽어도 반쪽 파일이 안 남는다.
func TestWriteLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	files, _, err := Encode(sampleDump(), 4, "2026-08-06T00:00:00.000000Z")
	if err != nil {
		t.Fatalf("Encode 실패: %v", err)
	}
	written, err := Write(files, dir)
	if err != nil {
		t.Fatalf("Write 실패: %v", err)
	}
	if len(written) != 7 {
		t.Errorf("파일이 %d개 — 7개를 기대한다: %v", len(written), written)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("훑기 실패: %v", err)
	}
	for _, e := range ents {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("임시 파일이 남았다: %s", e.Name())
		}
	}
	if len(ents) != 7 {
		t.Errorf("디렉토리에 %d개가 있다 — 7개를 기대한다", len(ents))
	}
}

// gen2 는 sampleDump 와 **행 수가 다른** 둘째 세대다.
//
// ★ 행 수가 달라야 세대 혼합이 관측된다. 같은 수면 옛 파일과 새 파일이 구별되지 않아
// 아래 시험들이 초록인 채로 아무것도 안 본다.
func gen2() store.LedgerDump {
	d := sampleDump()
	d.Sessions = d.Sessions[:1] // 2 → 1
	d.Judgments = append(d.Judgments, store.LedgerJudgment{
		ID: "01C", Project: ptr("p"), SessionID: ptr("s1"),
		At: "2026-08-06T00:00:09.000000Z", Kind: "decision", Body: "셋째",
	})
	return d
}

// 교체 도중 실패해도 **세대가 섞인 원장이 안 남는다**.
//
// ★ 이 시험이 겨냥하는 결함: Write 가 파일 단위로만 원자적이라, 같은 자리에 두 번째로
// 내보내다 중간에 실패하면 앞 몇 파일은 새 세대, 뒤 몇 파일은 옛 세대가 됐다.
// 매니페스트가 데이터 파일 셋보다 **먼저** 착지하는 순서(정렬상 4번째)라
// manifest.counts 는 새 값인데 sessions.jsonl 은 옛 것이었고, Read 가 그대로 통과했다.
// 그 원장으로 복원하면 새 판단이 가리키는 세션이 없어 defer_foreign_keys 커밋 검사에서
// **판단 전량이 롤백된다** — 되읽기가 성공했다고 말한 뒤에.
// 게다가 IsOurOutput 이 참이라 다음 실행이 --force 도 안 묻고 조용히 지나갔다.
//
// 같은 자리에 두 번 쓰는 것은 축복된 정상 경로다(outguard.go · migrate.go:183).
func TestWriteCommitFailureLeavesNoMixedGeneration(t *testing.T) {
	dir := t.TempDir()

	// 1차 — 정상 착지.
	files1, m1, err := Encode(sampleDump(), 4, "2026-08-06T00:00:00.000000Z")
	if err != nil {
		t.Fatalf("Encode 실패: %v", err)
	}
	if _, err := Write(files1, dir); err != nil {
		t.Fatalf("1차 Write 실패: %v", err)
	}

	// 교체를 실패시킨다 — 대상 이름 자리에 디렉토리를 세우면 os.Rename 이 EISDIR 로 죽는다.
	// (실제 원인은 ENOSPC·EACCES·EXDEV 여도 같은 자리에서 같은 모양으로 실패한다.)
	victim := filepath.Join(dir, projectsFile)
	if err := os.Remove(victim); err != nil {
		t.Fatalf("희생 파일 제거 실패: %v", err)
	}
	if err := os.Mkdir(victim, 0o755); err != nil {
		t.Fatalf("희생 디렉토리 생성 실패: %v", err)
	}

	// 2차 — 교체 단에서 죽는다.
	files2, m2, err := Encode(gen2(), 4, "2026-08-07T00:00:00.000000Z")
	if err != nil {
		t.Fatalf("Encode 실패: %v", err)
	}
	if m1.Counts.Sessions == m2.Counts.Sessions {
		t.Fatalf("전제가 깨졌다 — 두 세대의 세션 수가 같다(%d) — 혼합이 관측되지 않는다", m1.Counts.Sessions)
	}
	_, err = Write(files2, dir)
	if err == nil {
		t.Fatal("교체가 실패해야 하는데 성공했다 — 이 시험이 아무것도 안 본다")
	}

	// ── 단정 ──
	// (a) 사용자가 받는 문구가 "되쓸 자리가 반쯤 덮였다"를 담는다.
	var werr *WriteError
	if !errors.As(err, &werr) {
		t.Fatalf("WriteError 가 아니다(자리 상태를 실어 오지 않는다): %v", err)
	}
	if werr.Verdict.Intact {
		t.Errorf("교체 단 실패인데 '자리가 온전하다'고 판정했다: %+v", werr.Verdict)
	}
	if !strings.Contains(err.Error(), "반쯤 덮였다") {
		t.Errorf("오류 문구가 되쓸 자리의 상태를 안 적는다: %v", err)
	}

	// (b) 이 자리는 더 이상 원장이 아니다 — 다음 실행이 조용히 지나가면 안 된다.
	if IsOurOutput(dir) {
		t.Error("세대가 섞인 자리를 여전히 자기 산출물로 알아본다 — 다음 실행이 --force 도 안 묻고 덮는다")
	}

	// (c) 되읽기가 이 자리를 유효한 원장으로 받아들이지 않는다.
	if _, _, rerr := Read(dir); rerr == nil {
		t.Error("세대가 섞인 자리를 유효한 원장으로 되읽었다 — 복원이 FK 로 판단 전량을 롤백시킨다")
	}
}

// 준비 단에서 실패하면 되쓸 자리는 **한 글자도 안 바뀐다**.
// 앞선 세대가 그대로 읽혀야 한다 — 실패한 재실행이 성한 백업을 잡아먹으면 안 된다.
func TestWritePrepareFailureLeavesTargetIntact(t *testing.T) {
	dir := t.TempDir()

	want := sampleDump()
	files1, wantM, err := Encode(want, 4, "2026-08-06T00:00:00.000000Z")
	if err != nil {
		t.Fatalf("Encode 실패: %v", err)
	}
	if _, err := Write(files1, dir); err != nil {
		t.Fatalf("1차 Write 실패: %v", err)
	}

	// 준비 단(tmp 쓰기)에서 죽인다. 없는 하위 경로로 가는 이름 하나면 os.WriteFile 이
	// ENOENT 로 실패한다 — 실제 원인은 ENOSPC 가 흔하지만 죽는 자리는 같다.
	files2, _, err := Encode(gen2(), 4, "2026-08-07T00:00:00.000000Z")
	if err != nil {
		t.Fatalf("Encode 실패: %v", err)
	}
	files2["없는하위/x.jsonl"] = []byte("x\n")

	if _, err := Write(files2, dir); err == nil {
		t.Fatal("준비 단이 실패해야 하는데 성공했다 — 이 시험이 아무것도 안 본다")
	} else {
		var werr *WriteError
		if !errors.As(err, &werr) {
			t.Fatalf("WriteError 가 아니다: %v", err)
		}
		if !werr.Verdict.Intact {
			t.Errorf("자리를 안 건드렸는데 '반쯤 덮였다'고 판정했다: %+v", werr.Verdict)
		}
	}

	// 1차 세대가 온전히 그대로다.
	got, gotM, err := Read(dir)
	if err != nil {
		t.Fatalf("1차 세대를 되읽지 못했다 — 실패한 2차가 자리를 건드렸다: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("1차 세대가 바뀌었다:\n원본 %+v\n되읽음 %+v", want, got)
	}
	if !reflect.DeepEqual(wantM, gotM) {
		t.Errorf("1차 매니페스트가 바뀌었다:\n%+v\n%+v", wantM, gotM)
	}
	// 임시 파일이 안 남는다.
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("훑기 실패: %v", err)
	}
	for _, e := range ents {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("실패한 쓰기가 임시 파일을 남겼다: %s", e.Name())
		}
	}
}

// 되읽기는 매니페스트의 건수를 **실제 행 수와 대조한다**.
//
// ★ 두 번째 방벽이다. 첫 방벽(교체 단의 무효화)이 프로세스 급사 등으로 못 돌았을 때,
// 세대가 섞인 자리를 유효한 원장으로 받아들이면 안 된다. format·format_version 만 보면
// 그것이 그대로 통과한다 — 실측으로 그랬다.
func TestReadRejectsCountMismatch(t *testing.T) {
	dir := t.TempDir()
	files, _, err := Encode(sampleDump(), 4, "2026-08-06T00:00:00.000000Z")
	if err != nil {
		t.Fatalf("Encode 실패: %v", err)
	}
	if _, err := Write(files, dir); err != nil {
		t.Fatalf("Write 실패: %v", err)
	}
	// 세션 파일만 옛 세대(1행)로 되돌린다 — manifest 는 2행이라고 적어 두고 있다.
	old, _, err := Encode(gen2(), 4, "2026-08-06T00:00:00.000000Z")
	if err != nil {
		t.Fatalf("Encode 실패: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, sessionsFile), old[sessionsFile], 0o600); err != nil {
		t.Fatalf("세션 파일 바꿔치기 실패: %v", err)
	}

	_, _, err = Read(dir)
	if err == nil {
		t.Fatal("건수가 안 맞는 원장을 그대로 통과시켰다")
	}
	if !strings.Contains(err.Error(), "sessions") {
		t.Errorf("어느 표가 안 맞는지 안 적는다: %v", err)
	}
}

// 목록은 정렬돼 나온다 — 출력이 실행마다 흔들리면 안 된다.
func TestWriteReturnsSortedNames(t *testing.T) {
	dir := t.TempDir()
	files, _, _ := Encode(sampleDump(), 4, "2026-08-06T00:00:00.000000Z")
	written, err := Write(files, dir)
	if err != nil {
		t.Fatalf("Write 실패: %v", err)
	}
	for i := 1; i < len(written); i++ {
		if written[i-1] > written[i] {
			t.Fatalf("정렬이 안 됐다: %v", written)
		}
	}
}

// 자기 산출물을 알아본다 — 두 번째 실행이 --force 없이 돌아야 한다.
func TestIsOurOutput(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, dir string)
		want  bool
	}{
		{"빈 자리", func(t *testing.T, dir string) {}, false},
		{"우리 산출물", func(t *testing.T, dir string) {
			files, _, _ := Encode(sampleDump(), 4, "2026-08-06T00:00:00.000000Z")
			if _, err := Write(files, dir); err != nil {
				t.Fatalf("Write 실패: %v", err)
			}
		}, true},
		{"남의 manifest", func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, ManifestName),
				[]byte(`{"format":"남의것"}`), 0o600); err != nil {
				t.Fatalf("쓰기 실패: %v", err)
			}
		}, false},
		{"깨진 manifest", func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, ManifestName),
				[]byte(`{{{`), 0o600); err != nil {
				t.Fatalf("쓰기 실패: %v", err)
			}
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			c.setup(t, dir)
			if got := IsOurOutput(dir); got != c.want {
				t.Errorf("IsOurOutput=%v — 기대 %v", got, c.want)
			}
		})
	}
}

// 파일 왕복: 인코딩 → 쓰기 → 되읽기가 원본과 같아야 한다.
func TestReadRoundTripsEncodedFiles(t *testing.T) {
	dir := t.TempDir()
	want := sampleDump()
	files, wantM, err := Encode(want, 4, "2026-08-06T00:00:00.000000Z")
	if err != nil {
		t.Fatalf("Encode 실패: %v", err)
	}
	if _, err := Write(files, dir); err != nil {
		t.Fatalf("Write 실패: %v", err)
	}
	got, gotM, err := Read(dir)
	if err != nil {
		t.Fatalf("Read 실패: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("원장이 왕복에서 달라졌다:\n원본 %+v\n되읽음 %+v", want, got)
	}
	if !reflect.DeepEqual(wantM, gotM) {
		t.Errorf("매니페스트가 달라졌다:\n%+v\n%+v", wantM, gotM)
	}
}

// 64KB 를 넘는 줄을 읽는다 — 지금 DB 의 최장 본문이 74,227B 다.
func TestReadHandlesLongLines(t *testing.T) {
	dir := t.TempDir()
	want := sampleDump()
	want.Judgments[0].Body = strings.Repeat("가", 30000)
	files, _, _ := Encode(want, 4, "2026-08-06T00:00:00.000000Z")
	if _, err := Write(files, dir); err != nil {
		t.Fatalf("Write 실패: %v", err)
	}

	// ★ 픽스처가 실제로 64KB 상한을 넘는지 Read 보다 먼저 단정한다. 이 상수가 나중에
	//   줄어들면(리팩터링 실수 등) 이 단정이 없으면 시험은 계속 초록인 채 8MB 버퍼
	//   계약을 아무것도 안 본다 — Read 가 그냥 성공해 버리기 때문이다. 실제로 디스크에
	//   쓰인 줄을 잰다(readLines 가 실제로 읽는 대상). Task 5 의
	//   TestEncodeHandlesBodyOverScannerDefault 가 같은 단정을 쓴다.
	written, err := os.ReadFile(filepath.Join(dir, judgmentsFile))
	if err != nil {
		t.Fatalf("쓰인 파일을 못 읽었다: %v", err)
	}
	first := strings.SplitN(string(written), "\n", 2)[0]
	if len(first) < 64*1024 {
		t.Fatalf("픽스처가 상한보다 작다(%dB) — 이 시험이 아무것도 안 본다", len(first))
	}

	got, _, err := Read(dir)
	if err != nil {
		t.Fatalf("긴 줄 되읽기 실패(bufio.Scanner 기본 상한 64KB 를 넘었는가): %v", err)
	}
	if got.Judgments[0].Body != want.Judgments[0].Body {
		t.Error("긴 본문이 달라졌다")
	}
}

// 빈 원장의 왕복 — Task 5 리뷰가 남긴 함정.
//
// ★ encodeLines 는 원소가 0개면 bytes.Buffer 를 한 번도 안 써서 Bytes() 가 nil 을 낸다.
// 그러면 Write 가 빈 파일을 쓰고, readLines 는 그 빈 파일에서 Scan 을 한 번도 못 돌려
// out(var out []T, 초기값 nil)이 그대로 nil 로 남는다. 실제 DB 쪽도 마찬가지다 —
// internal/store 의 readLedgerJudgments 류가 전부 `var out []T` 로 시작해 0행이면 nil 을
// 낸다. 즉 원본(0건인 표)과 되읽음이 둘 다 nil 이라 reflect.DeepEqual 이 참이다.
// 이 시험이 그 사실을 실측으로 고정한다 — 우연이 아니라 계약으로 못박는다.
func TestReadRoundTripsEmptyLedger(t *testing.T) {
	dir := t.TempDir()
	want := store.LedgerDump{} // 세 슬라이스 다 nil — Go 영값
	files, wantM, err := Encode(want, 4, "2026-08-06T00:00:00.000000Z")
	if err != nil {
		t.Fatalf("Encode 실패: %v", err)
	}
	if _, err := Write(files, dir); err != nil {
		t.Fatalf("Write 실패: %v", err)
	}
	got, gotM, err := Read(dir)
	if err != nil {
		t.Fatalf("Read 실패: %v", err)
	}
	if got.Judgments != nil || got.Links != nil || got.Snapshots != nil {
		t.Errorf("빈 원장 되읽기가 nil 이 아니다: %+v", got)
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("빈 원장이 왕복에서 달라졌다:\n원본 %+v\n되읽음 %+v", want, got)
	}
	if wantM.Counts.Judgments != 0 || wantM.Counts.Links != 0 || wantM.Counts.Snapshots != 0 {
		t.Errorf("빈 원장의 건수가 0 이 아니다: %+v", wantM.Counts)
	}
	if !reflect.DeepEqual(wantM, gotM) {
		t.Errorf("매니페스트가 달라졌다:\n%+v\n%+v", wantM, gotM)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 순수 판정 셋을 표로 문다 — 지금까지 이 셋을 **직접 부르는 시험이 하나도 없었다**
// ─────────────────────────────────────────────────────────────────────────────
//
// 통합 시험이 변이에서 무는 것은 확인됐지만, 판정을 표로 무는 선례가 이 저장소에 있다
// (legacy/outguard_test.go 의 TestJudgeOutTarget · judge/paths_test.go 의
// TestJudgePathCoordinate). 판정이 순수 함수인 이유가 "시험이 직접 부른다"인데 아무도
// 안 부르면 그 이유가 반쯤 비어 있다.

// TestJudgeWriteFailureDoesNotInventLeftovers 는 교체 단의 **세 갈래**를 가른다.
//
// ★ moved == total 일 때(데이터를 다 옮기고 매니페스트 rename 만 실패) 예전 문구는
// 고정으로 "나머지는 옛 세대다"라고 말했다. 그 경우 남은 옛 세대는 **없다.** 행동 지침은
// 여전히 옳았지만(무효화됐다 · --force 로 재실행), 이 저장소는 모르는 것을 지어내지
// 않는 것을 규율로 삼는다 — 파생을 사실로 적는 자리였다.
//
// moved == 0 도 실재한다: 교체 단 첫 rename 이 실패하면 매니페스트는 이미 걷어냈는데
// 데이터는 한 개도 안 바뀌었다(Write 의 doc 이 그 창을 이미 적어 뒀다).
func TestJudgeWriteFailureDoesNotInventLeftovers(t *testing.T) {
	const total = 6 // 데이터 여섯 + 매니페스트 하나

	t.Run("준비 단은 자리를 안 건드린다", func(t *testing.T) {
		v := JudgeWriteFailure(StagePrepare, 0, total)
		if !v.Intact {
			t.Errorf("준비 단인데 자리가 온전하지 않다고 한다: %+v", v)
		}
	})

	// 갈래를 가르는 **실체**는 두 세대가 함께 있느냐다 — 그때만 FK 롤백 피해가 성립한다.
	// 낱말 하나로 재지 않는다: 정직한 문구는 "남은 옛 세대는 없다"처럼 같은 낱말을 쓰면서
	// 부정하므로, 부분 문자열로 재면 정직한 쪽이 거짓말하는 쪽과 같아 보인다.
	cases := []struct {
		name          string
		moved         int
		wantMixedHarm bool // 세대 혼합 피해를 말해야 하는가
		wantLeftovers bool // "나머지"(남은 옛 세대)가 있다고 말해야 하는가
	}{
		{"하나도 못 옮겼다 — 전부 옛 세대다", 0, false, false},
		{"섞였다", 3, true, true},
		{"다 옮기고 매니페스트만 못 얹었다 — 옛 세대가 없다", total, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := JudgeWriteFailure(StageCommit, c.moved, total)
			if v.Intact {
				t.Errorf("교체 단은 매니페스트를 이미 걷어냈다 — 자리가 온전할 수 없다: %+v", v)
			}
			// 어느 갈래든 복구 방법은 같고 반드시 실려야 한다.
			if !strings.Contains(v.Reason, "--force") {
				t.Errorf("복구 방법(--force 재실행)이 사유에 없다: %s", v.Reason)
			}
			if got := strings.Contains(v.Reason, "세대가 섞인"); got != c.wantMixedHarm {
				t.Errorf("moved=%d/%d 에서 세대 혼합 피해 언급이 %v 인데 %v 여야 한다 — "+
					"두 세대가 함께 있을 때만 그 피해가 성립한다:\n  %s",
					c.moved, total, got, c.wantMixedHarm, v.Reason)
			}
			if got := strings.Contains(v.Reason, "나머지"); got != c.wantLeftovers {
				t.Errorf("moved=%d/%d 에서 '나머지' 언급이 %v 인데 %v 여야 한다 — "+
					"없는 옛 세대를 말하면 파생을 사실로 적는 것이다:\n  %s",
					c.moved, total, got, c.wantLeftovers, v.Reason)
			}
		})
	}
}

// TestCountsOfMapsEveryTable 는 표마다 자기 슬라이스를 센다는 것을 문다.
// 길이를 전부 다르게 줘서 두 칸이 뒤바뀌면 반드시 걸리게 한다.
func TestCountsOfMapsEveryTable(t *testing.T) {
	d := store.LedgerDump{
		Machines:  make([]store.LedgerMachine, 1),
		Projects:  make([]store.LedgerProject, 2),
		Sessions:  make([]store.LedgerSession, 3),
		Judgments: make([]store.LedgerJudgment, 4),
		Links:     make([]store.LedgerLink, 5),
		Snapshots: make([]store.LedgerSnapshot, 6),
	}
	want := Counts{Machines: 1, Projects: 2, Sessions: 3, Judgments: 4, Links: 5, Snapshots: 6}
	if got := CountsOf(d); got != want {
		t.Errorf("행 수가 표와 안 맞는다(칸이 뒤바뀌었나).\n  얻음: %+v\n  기대: %+v", got, want)
	}
}

// TestJudgeCountsNamesEveryMismatchedTable 는 어긋난 표를 **전부** 이름으로 말하는지 본다.
// 첫 어긋남에서 멈추면 사용자가 한 번에 한 표씩만 알게 된다.
func TestJudgeCountsNamesEveryMismatchedTable(t *testing.T) {
	base := Counts{Machines: 1, Projects: 2, Sessions: 3, Judgments: 4, Links: 5, Snapshots: 6}
	if err := JudgeCounts(base, base); err != nil {
		t.Fatalf("같은 수인데 어긋났다고 한다: %v", err)
	}

	got := Counts{Machines: 1, Projects: 9, Sessions: 3, Judgments: 4, Links: 5, Snapshots: 99}
	err := JudgeCounts(base, got)
	if err == nil {
		t.Fatal("두 표가 어긋났는데 통과했다 — 세대가 섞인 원장이 그대로 복원된다")
	}
	msg := err.Error()
	for _, want := range []string{"projects", "snapshots"} {
		if !strings.Contains(msg, want) {
			t.Errorf("어긋난 표 %q 를 안 부른다:\n  %s", want, msg)
		}
	}
	for _, notWant := range []string{"machines", "sessions", "judgments", "judgment_links"} {
		if strings.Contains(msg, notWant) {
			t.Errorf("맞는 표 %q 를 어긋났다고 부른다:\n  %s", notWant, msg)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 급사가 남긴 tmp 는 **뒤이은 실행이** 치운다
// ─────────────────────────────────────────────────────────────────────────────
//
// Write 의 cleanup 은 **자기 실행이 만든** tmp 만 안다. 프로세스가 준비 단에서 급사하면
// (SIGKILL·전원 차단) 최대 일곱 개가 남고, 뒤이은 성공적 실행도 그것을 안 치운다.
// 판단 본문이 든 몇 MB 짜리가 급사마다 쌓이고 not-empty 가드가 세는 항목 수를 부풀린다.
//
// ★ 나이로 가른다. "저 tmp 를 만든 프로세스가 살아 있나"는 이 자리에서 알 수 없고,
// pid 의 생사로 판정하는 길은 이 저장소가 이미 기각했다(schema.sql 의 session 표가
// "pid 死를 근거로 살아 있는 세션을 죽었다고 판정한 사고가 실재한다"고 적어 뒀다).
func TestSweepStaleTmpsTakesOnlyOldOnesAndOnlyOurs(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	names := []string{"judgments.jsonl", ManifestName}

	write := func(fname string, age time.Duration) string {
		p := filepath.Join(dir, fname)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatalf("픽스처 쓰기 실패(%s): %v", fname, err)
		}
		at := now.Add(-age)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatalf("시각 조정 실패(%s): %v", fname, err)
		}
		return p
	}

	stale := write("judgments.jsonl.999-deadbeef.tmp", 2*time.Hour)
	staleManifest := write(ManifestName+".999-cafe.tmp", 25*time.Hour)
	// 살아 있는 실행의 in-flight tmp. 이것을 지우면 그 실행은 교체 단 rename 에서
	// 죽고, 그때는 매니페스트를 이미 걷어낸 뒤라 **그 자리가 무효화된다.**
	inflight := write("judgments.jsonl.123-beef.tmp", time.Minute)
	// 남의 파일. 우리 이름이 아니면 아무리 낡아도 안 건드린다.
	foreign := write("someone-else.tmp", 100*time.Hour)
	foreign2 := write("notours.jsonl.1-2.tmp", 100*time.Hour)
	// 실물 산출물. .tmp 가 아니므로 대상이 아니다.
	real := write("judgments.jsonl", 100*time.Hour)

	if got := sweepStaleTmps(dir, names, now); got != 2 {
		t.Errorf("걷어낸 수가 %d다 — 낡은 우리 tmp 둘만 대상이다", got)
	}
	for _, p := range []string{stale, staleManifest} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("낡은 잔해가 남았다: %s", p)
		}
	}
	for _, p := range []string{inflight, foreign, foreign2, real} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("건드리면 안 되는 파일이 사라졌다: %s (%v)", p, err)
		}
	}
}

// Write 가 그 청소를 실제로 부르는지 — 순수 함수만 시험하면 배선이 빠져도 초록이다.
func TestWriteSweepsDebrisFromAnEarlierCrash(t *testing.T) {
	dir := t.TempDir()
	debris := filepath.Join(dir, "judgments.jsonl.999-deadbeef.tmp")
	if err := os.WriteFile(debris, []byte("옛 급사의 잔해"), 0o600); err != nil {
		t.Fatalf("잔해 쓰기 실패: %v", err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(debris, old, old); err != nil {
		t.Fatalf("시각 조정 실패: %v", err)
	}

	if _, err := Write(map[string][]byte{
		"judgments.jsonl": []byte("{}\n"),
		ManifestName:      []byte("{}\n"),
	}, dir); err != nil {
		t.Fatalf("쓰기 실패: %v", err)
	}
	if _, err := os.Stat(debris); !os.IsNotExist(err) {
		t.Error("성공한 실행이 앞선 급사의 잔해를 그대로 뒀다 — " +
			"판단 본문이 든 몇 MB 가 급사마다 쌓이고 not-empty 항목 수를 부풀린다")
	}
}

// TestJudgeRestoreSchemaRefusesAnyMismatch 는 복원의 **안전핀**을 문다.
//
// ★ 지금까지 manifest.schema_version 을 보는 자리가 하나도 없었다 — Read 도
// store.WriteLedger 도 안 봤고, "무손실의 안전핀"이라는 주석만 있었다. 스키마가 5로
// 오른 뒤 4로 뜬 원장을 되읽으면 5에서 생긴 컬럼이 JSONL 에 없어 영값으로 들어가고,
// NULL 이어야 할 자리가 ""가 되거나 NOT NULL 위반으로 트랜잭션 전체가 죽는다.
// 반대 방향은 이 바이너리가 모르는 컬럼이 실려 오는 것이다.
//
// 어느 방향이든 거절한다. 세대를 맞추는 것은 이 명령이 할 수 있는 일이 아니고,
// 조용히 넣는 것보다 "그 판의 바이너리로 넣어라"가 복구 가능하다.
func TestJudgeRestoreSchemaRefusesAnyMismatch(t *testing.T) {
	if err := JudgeRestoreSchema(4, 4); err != nil {
		t.Errorf("같은 판인데 거절했다: %v", err)
	}
	for _, c := range []struct{ ledger, code int }{{3, 4}, {5, 4}} {
		err := JudgeRestoreSchema(c.ledger, c.code)
		if err == nil {
			t.Errorf("원장 %d · 바이너리 %d 를 그대로 통과시켰다 — "+
				"세대가 다른 원장을 되쓰면 조용히 깨진다", c.ledger, c.code)
			continue
		}
		// 두 수를 다 말해야 운영자가 어느 판의 바이너리를 찾아야 하는지 안다.
		for _, want := range []string{strconv.Itoa(c.ledger), strconv.Itoa(c.code)} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("거절 문구가 %q 를 안 말한다: %v", want, err)
			}
		}
	}
}
