package ledger

import (
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
