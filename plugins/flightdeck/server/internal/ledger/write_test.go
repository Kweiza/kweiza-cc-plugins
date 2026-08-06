package ledger

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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
