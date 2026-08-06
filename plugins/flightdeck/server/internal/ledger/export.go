package ledger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kweiza/flightdeck/internal/store"
)

const (
	// FormatName 은 manifest 의 형식 이름이다. 출력 자리 판정이 자기 산출물을
	// 알아보는 데도 쓰인다(outguard.go).
	FormatName = "fd-judgment-backup"
	// FormatVersion 은 이 산출물 배치의 버전이다. 파일 이름이나 줄 구조가 바뀌면 올린다.
	//
	// 2: FK 폐포를 닫으며 machines·projects·sessions 셋이 늘었다. 버전 1 산출물로 복원하면
	//    세션 걸린 판단(실측 85%)이 FK 위반으로 전부 롤백되므로, Read 가 그것을 거절하는 것이 맞다.
	FormatVersion = 2

	judgmentsFile = "judgments.jsonl"
	linksFile     = "judgment_links.jsonl"
	snapshotsFile = "snapshots.jsonl"
	machinesFile  = "machines.jsonl"
	projectsFile  = "projects.jsonl"
	sessionsFile  = "sessions.jsonl"

	// ManifestName 은 매니페스트 파일 이름이다.
	ManifestName = "manifest.json"
)

// Counts 는 내보낸 행 수다.
type Counts struct {
	Machines  int `json:"machines"`
	Projects  int `json:"projects"`
	Sessions  int `json:"sessions"`
	Judgments int `json:"judgments"`
	Links     int `json:"judgment_links"`
	Snapshots int `json:"snapshots"`
}

// CountsOf 는 원장 한 벌의 행 수다.
//
// ★ Encode 가 매니페스트에 적을 때와 Read 가 그것을 대조할 때가 **같은 함수**를 쓴다.
// 두 자리에 두면 "적은 수"와 "센 수"의 정의가 갈리고, 그러면 대조가 조용히 무의미해진다.
func CountsOf(d store.LedgerDump) Counts {
	return Counts{
		Machines:  len(d.Machines),
		Projects:  len(d.Projects),
		Sessions:  len(d.Sessions),
		Judgments: len(d.Judgments),
		Links:     len(d.Links),
		Snapshots: len(d.Snapshots),
	}
}

// JudgeCounts 는 매니페스트가 적은 건수와 실제로 읽힌 행 수를 대조한다. 순수 함수다.
//
// ★ 왜 필요한가. 쓰기가 파일 단위로만 원자적이던 동안, 같은 자리에 두 번째로 내보내다
// 실패하면 앞 몇 파일만 새 세대가 됐다(매니페스트가 정렬상 데이터 셋보다 먼저 착지했다).
// 그때 manifest.counts.sessions 는 303 인데 sessions.jsonl 은 250줄이었고,
// Read 는 format·format_version 만 보고 **그대로 통과했다.** 그 원장으로 복원하면
// 새 판단이 가리키는 세션이 없어 커밋 시점 FK 검사에서 판단 전량이 롤백된다.
//
// 쓰기 쪽 무효화(Write 의 교체 단)가 첫 방벽이고 이것이 둘째다. 첫 방벽이 프로세스
// 급사로 못 돌았거나, 산출물이 손으로·다른 도구로 뒤섞인 경우를 여기서 잡는다.
//
// 불리언이 아니라 **어느 표가 몇 대 몇인지**를 담은 오류를 낸다 — 그것이 없으면
// "원장이 이상하다"만 알고 무엇이 옛 세대인지는 모른다.
func JudgeCounts(want, got Counts) error {
	var bad []string
	for _, c := range []struct {
		name      string
		want, got int
	}{
		{"machines", want.Machines, got.Machines},
		{"projects", want.Projects, got.Projects},
		{"sessions", want.Sessions, got.Sessions},
		{"judgments", want.Judgments, got.Judgments},
		{"judgment_links", want.Links, got.Links},
		{"snapshots", want.Snapshots, got.Snapshots},
	} {
		if c.want != c.got {
			bad = append(bad, fmt.Sprintf("%s: 매니페스트 %d건 · 실제 %d줄", c.name, c.want, c.got))
		}
	}
	if len(bad) == 0 {
		return nil
	}
	return fmt.Errorf("원장의 세대가 섞였다 — 매니페스트가 적은 건수와 실제 행 수가 다르다(%s). "+
		"쓰기가 도중에 실패해 일부 파일만 새 세대로 바뀐 자리다. 이대로 복원하면 "+
		"커밋 시점 FK 검사에서 판단 전량이 롤백된다", strings.Join(bad, ", "))
}

// Manifest 는 산출물의 머리다.
//
// SchemaVersion 이 무손실의 안전핀이다 — 스키마가 오른 뒤 옛 버전으로 뜬 원장을 되읽으면
// 조용히 깨지는데, 이 값이 있으면 거절할 수 있다.
type Manifest struct {
	Format        string `json:"format"`
	FormatVersion int    `json:"format_version"`
	SchemaVersion int    `json:"schema_version"`
	ExportedAt    string `json:"exported_at"`
	Counts        Counts `json:"counts"`
}

// Encode 는 원장을 파일 이름 → 바이트로 인코딩한다. 파일을 쓰지 않는다.
//
// ★ 같은 입력은 같은 바이트를 낸다. exportedAt 을 인자로 받는 이유가 그것이다 —
// 함수 안에서 time.Now() 를 부르면 결정성이 깨지고, 그러면 매시간 git 커밋이
// 내용이 안 바뀌어도 새 커밋을 쌓는다.
//
// ★ json.Encoder 대신 json.Marshal + 손수 개행을 쓴다. Encoder 는 Encode 마다 개행을
// 붙여 주지만 SetEscapeHTML(false) 를 안 끄면 본문의 <, >, & 를 이스케이프한다 —
// 판단 본문에 코드가 들어가는 이 저장소에서 그 치환은 원문 대조를 깨뜨린다.
func Encode(d store.LedgerDump, schemaVersion int, exportedAt string) (map[string][]byte, Manifest, error) {
	m := Manifest{
		Format:        FormatName,
		FormatVersion: FormatVersion,
		SchemaVersion: schemaVersion,
		ExportedAt:    exportedAt,
		Counts:        CountsOf(d),
	}

	machines, err := encodeLines(len(d.Machines), func(i int) any { return d.Machines[i] })
	if err != nil {
		return nil, m, fmt.Errorf("머신 인코딩 실패: %w", err)
	}
	projects, err := encodeLines(len(d.Projects), func(i int) any { return d.Projects[i] })
	if err != nil {
		return nil, m, fmt.Errorf("프로젝트 인코딩 실패: %w", err)
	}
	sessions, err := encodeLines(len(d.Sessions), func(i int) any { return d.Sessions[i] })
	if err != nil {
		return nil, m, fmt.Errorf("세션 인코딩 실패: %w", err)
	}
	judgments, err := encodeLines(len(d.Judgments), func(i int) any { return d.Judgments[i] })
	if err != nil {
		return nil, m, fmt.Errorf("판단 인코딩 실패: %w", err)
	}
	links, err := encodeLines(len(d.Links), func(i int) any { return d.Links[i] })
	if err != nil {
		return nil, m, fmt.Errorf("링크 인코딩 실패: %w", err)
	}
	snapshots, err := encodeLines(len(d.Snapshots), func(i int) any { return d.Snapshots[i] })
	if err != nil {
		return nil, m, fmt.Errorf("스냅숏 인코딩 실패: %w", err)
	}

	// 매니페스트는 사람이 열어 보는 파일이라 들여쓴다. JSONL 셋은 한 줄 한 행이라 안 들여쓴다.
	manifest, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, m, fmt.Errorf("매니페스트 인코딩 실패: %w", err)
	}
	manifest = append(manifest, '\n')

	return map[string][]byte{
		machinesFile:  machines,
		projectsFile:  projects,
		sessionsFile:  sessions,
		judgmentsFile: judgments,
		linksFile:     links,
		snapshotsFile: snapshots,
		ManifestName:  manifest,
	}, m, nil
}

// encodeLines 는 n개 행을 한 줄에 하나씩 JSON 으로 쓴다.
func encodeLines(n int, at func(int) any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// ★ HTML 이스케이프를 끈다. 켜져 있으면 판단 본문의 <, >, & 가 < 류로 바뀌어
	//   DB 원문과 글자가 달라지고, 그러면 원문 대조가 불가능해진다.
	enc.SetEscapeHTML(false)
	for i := 0; i < n; i++ {
		if err := enc.Encode(at(i)); err != nil { // Encode 가 줄 끝에 개행을 붙인다
			return nil, err
		}
	}
	return buf.Bytes(), nil
}
