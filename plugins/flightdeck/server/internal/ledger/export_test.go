package ledger

import (
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/store"
)

func ptr(s string) *string { return &s }

func sampleDump() store.LedgerDump {
	return store.LedgerDump{
		Judgments: []store.LedgerJudgment{
			{ID: "01A", Project: ptr("p"), SessionID: nil,
				At: "2026-08-06T00:00:01.000000Z", Kind: "decision",
				Title: nil, Body: "본문", Supersedes: nil},
			{ID: "01B", Project: ptr("p"), SessionID: ptr("s1"),
				At: "2026-08-06T00:00:02.000000Z", Kind: "ask",
				Title: ptr("제목"), Body: "둘째", Supersedes: ptr("01A")},
		},
		Links: []store.LedgerLink{
			{JudgmentID: "01A", TargetKind: "item", TargetID: "i1"},
		},
		Snapshots: []store.LedgerSnapshot{
			{Project: "p", Key: "k", Value: "1", Method: "command",
				Evidence: nil, InputDigest: nil, ComputedAt: "2026-08-06T00:00:00.000000Z"},
		},
	}
}

// 같은 입력은 같은 바이트를 낸다. 이것이 없으면 매시간 git 커밋이 무의미한 커밋을 쌓는다.
func TestEncodeIsDeterministic(t *testing.T) {
	d := sampleDump()
	a, _, err := Encode(d, 4, "2026-08-06T00:00:00.000000Z")
	if err != nil {
		t.Fatalf("Encode 실패: %v", err)
	}
	b, _, err := Encode(d, 4, "2026-08-06T00:00:00.000000Z")
	if err != nil {
		t.Fatalf("Encode 재실행 실패: %v", err)
	}
	for name, av := range a {
		if string(av) != string(b[name]) {
			t.Errorf("%s 가 두 번 인코딩에서 달라졌다", name)
		}
	}
}

// NULL 은 JSON null 이다. "" 로 나가면 되읽기가 NULL 과 빈 문자열을 못 가른다.
func TestEncodeKeepsNullAsJSONNull(t *testing.T) {
	files, _, err := Encode(sampleDump(), 4, "2026-08-06T00:00:00.000000Z")
	if err != nil {
		t.Fatalf("Encode 실패: %v", err)
	}
	first := strings.SplitN(string(files["judgments.jsonl"]), "\n", 2)[0]
	for _, want := range []string{`"session_id":null`, `"title":null`, `"supersedes":null`} {
		if !strings.Contains(first, want) {
			t.Errorf("%s 가 없다:\n%s", want, first)
		}
	}
	if strings.Contains(first, `"session_id":""`) {
		t.Error("NULL 이 빈 문자열로 나갔다")
	}
}

// 시각은 DB 원문 그대로, 폭 고정이다.
func TestEncodeKeepsRawTimestamp(t *testing.T) {
	files, _, err := Encode(sampleDump(), 4, "2026-08-06T00:00:00.000000Z")
	if err != nil {
		t.Fatalf("Encode 실패: %v", err)
	}
	if !strings.Contains(string(files["judgments.jsonl"]), `"at":"2026-08-06T00:00:01.000000Z"`) {
		t.Errorf("시각이 원문이 아니다:\n%s", files["judgments.jsonl"])
	}
}

// 한 줄이 한 행이다. 그리고 파일 넷이 나온다.
func TestEncodeProducesFourFilesAndLinePerRow(t *testing.T) {
	files, m, err := Encode(sampleDump(), 4, "2026-08-06T00:00:00.000000Z")
	if err != nil {
		t.Fatalf("Encode 실패: %v", err)
	}
	for _, name := range []string{"judgments.jsonl", "judgment_links.jsonl", "snapshots.jsonl", ManifestName} {
		if _, ok := files[name]; !ok {
			t.Errorf("%s 가 없다", name)
		}
	}
	lines := strings.Count(strings.TrimRight(string(files["judgments.jsonl"]), "\n"), "\n") + 1
	if lines != 2 {
		t.Errorf("판단 줄이 %d개 — 2개를 기대한다", lines)
	}
	if m.SchemaVersion != 4 || m.FormatVersion != FormatVersion || m.Format != FormatName {
		t.Errorf("manifest 가 이상하다: %+v", m)
	}
	if m.Counts.Judgments != 2 || m.Counts.Links != 1 || m.Counts.Snapshots != 1 {
		t.Errorf("건수가 틀리다: %+v", m.Counts)
	}
}

// 74KB 본문이 한 줄로 나간다 — 지금 DB 의 실제 최댓값이 74,227B 다.
// 읽는 쪽이 bufio.Scanner 기본 상한(64KB)을 쓰면 여기서 곧바로 죽는다.
func TestEncodeHandlesBodyOverScannerDefault(t *testing.T) {
	d := sampleDump()
	d.Judgments[0].Body = strings.Repeat("가", 30000) // UTF-8 로 90,000B
	files, _, err := Encode(d, 4, "2026-08-06T00:00:00.000000Z")
	if err != nil {
		t.Fatalf("Encode 실패: %v", err)
	}
	first := strings.SplitN(string(files["judgments.jsonl"]), "\n", 2)[0]
	if len(first) < 64*1024 {
		t.Fatalf("픽스처가 상한보다 작다(%dB) — 이 시험이 아무것도 안 본다", len(first))
	}
}

// 손실 목록은 순수 함수다. 산문에만 적어 두면 코드가 더 잃기 시작해도 아무도 모른다.
func TestLossesNamesTheKnownGaps(t *testing.T) {
	joined := strings.Join(Losses(), "\n")
	for _, want := range []string{"아웃박스", "judgment_fts", "project", "session", "machine"} {
		if !strings.Contains(joined, want) {
			t.Errorf("손실 목록에 %q 축이 없다: %v", want, Losses())
		}
	}
}
