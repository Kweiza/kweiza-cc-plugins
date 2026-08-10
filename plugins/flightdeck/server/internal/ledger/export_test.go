package ledger

import (
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/store"
)

func ptr(s string) *string { return &s }

func sampleDump() store.LedgerDump {
	return store.LedgerDump{
		// machine 은 NULL 가능 컬럼이 없다 — 섞을 축이 없으므로 한 행이면 충분하다.
		Machines: []store.LedgerMachine{
			{ID: "m1", Hostname: "host-1",
				FirstSeen: "2026-08-01T00:00:00.000000Z", LastSeen: "2026-08-06T00:00:00.000000Z"},
		},
		// project 는 remote_url·config·config_from_sha 셋이 NULL 가능하다 — 한 행은 전부 NULL,
		// 한 행은 전부 값이 있게 섞는다.
		Projects: []store.LedgerProject{
			{ID: "p", Path: "/repo/p", RemoteURL: nil, DefaultBranch: "main",
				Config: nil, ConfigFromSHA: nil, CreatedAt: "2026-08-01T00:00:00.000000Z"},
			{ID: "p2", Path: "/repo/p2", RemoteURL: ptr("git@example.com:p2.git"), DefaultBranch: "trunk",
				Config: ptr(`{"lane":1}`), ConfigFromSHA: ptr("deadbeef"), CreatedAt: "2026-08-02T00:00:00.000000Z"},
		},
		// session 은 label·blocked_why 둘이 NULL 가능하다 — 한 행은 둘 다 NULL(active),
		// 한 행은 둘 다 값이 있다(blocked).
		Sessions: []store.LedgerSession{
			{ID: "s1", Project: "p", MachineID: "m1", Worktree: "/w/s1", CCSessionID: "cc1",
				Label: nil, State: "active", BlockedWhy: nil, OpenedAt: "2026-08-06T00:00:00.000000Z"},
			{ID: "s2", Project: "p2", MachineID: "m1", Worktree: "/w/s2", CCSessionID: "cc2",
				Label: ptr("표시 이름"), State: "blocked", BlockedWhy: ptr("왜 막혔는지"),
				OpenedAt: "2026-08-06T00:00:03.000000Z"},
		},
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

// 한 줄이 한 행이다. 그리고 파일 일곱(JSONL 여섯 + manifest)이 나온다.
func TestEncodeProducesSevenFilesAndLinePerRow(t *testing.T) {
	files, m, err := Encode(sampleDump(), 4, "2026-08-06T00:00:00.000000Z")
	if err != nil {
		t.Fatalf("Encode 실패: %v", err)
	}
	for _, name := range []string{
		"machines.jsonl", "projects.jsonl", "sessions.jsonl",
		"judgments.jsonl", "judgment_links.jsonl", "snapshots.jsonl", ManifestName,
	} {
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
	if m.Counts.Machines != 1 || m.Counts.Projects != 2 || m.Counts.Sessions != 2 ||
		m.Counts.Judgments != 2 || m.Counts.Links != 1 || m.Counts.Snapshots != 1 {
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
	for _, want := range []string{"아웃박스", "judgment_fts", "폐포 밖", "judgment_link.target_id"} {
		if !strings.Contains(joined, want) {
			t.Errorf("손실 목록에 %q 축이 없다: %v", want, Losses())
		}
	}
}

// ③ SetEscapeHTML(false) 계약 — 판단 본문의 `<`·`>`·`&` 가 그대로 나가야 한다.
//
// ★ 이 계약에 시험이 없었다. 픽스처 어디에도 그 세 글자가 없어서 export.go 에서
// `(false)` → `(true)` 로 바꿔도 전 시험이 초록이었다(실측). 판단 본문에 코드가 자주
// 들어가는 저장소라 그 치환은 **원문 대조를 깨뜨린다** — DB 의 `<` 가 파일에서 `<`
// 가 되면 백업이 원문이 아니게 되고, 무손실 등급이 글자 단위에서 무너진다.
func TestEncodeDoesNotEscapeHTMLInBodies(t *testing.T) {
	// 따옴표는 일부러 안 넣는다 — `\"` 는 JSON 자신의 이스케이프라 이 축과 무관하고,
	// 넣으면 원문 대조가 그 이유로 실패해 이 시험이 무엇을 재는지 흐려진다.
	const raw = `if a < b && c > d { return <tag> }`
	d := sampleDump()
	d.Judgments[0].Body = raw
	d.Judgments[0].Title = ptr(raw)

	files, _, err := Encode(d, 4, "2026-08-06T00:00:00.000000Z")
	if err != nil {
		t.Fatalf("Encode 실패: %v", err)
	}
	got := string(files[judgmentsFile])
	// encoding/json 은 HTML 이스케이프가 켜지면 이 셋을 \u00XX 로 바꾼다.
	for _, esc := range []string{`\u003c`, `\u003e`, `\u0026`} {
		if strings.Contains(got, esc) {
			t.Errorf("판단 본문의 글자가 %s 로 이스케이프됐다 — DB 원문과 글자가 달라져 "+
				"원문 대조가 불가능해진다:\n%s", esc, got)
		}
	}
	if !strings.Contains(got, raw) {
		t.Errorf("판단 본문이 원문 그대로 안 실렸다:\n  원본: %s\n  파일: %s", raw, got)
	}
	// 되읽어도 원문 그대로여야 한다 — 인코딩만 보면 디코딩 쪽 회귀를 못 본다.
	dir := t.TempDir()
	if _, err := Write(files, dir); err != nil {
		t.Fatalf("Write 실패: %v", err)
	}
	back, _, err := Read(dir)
	if err != nil {
		t.Fatalf("Read 실패: %v", err)
	}
	if back.Judgments[0].Body != raw {
		t.Errorf("왕복에서 본문 글자가 바뀌었다:\n  원본: %s\n  왕복: %s", raw, back.Judgments[0].Body)
	}
}
