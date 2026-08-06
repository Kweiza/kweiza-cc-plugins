package ledger

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kweiza/flightdeck/internal/store"
)

// maxLineBytes 는 한 줄의 상한이다.
//
// ★ bufio.Scanner 의 기본 상한은 64KB 인데 지금 DB 의 최장 판단 본문이 74,227B 다 —
// 기본값을 쓰면 실제 데이터 한 행에서 곧바로 "token too long" 이 난다.
// cmd/fd/outbox.go 가 같은 이유로 8MB 를 준다.
const maxLineBytes = 8 << 20

// Read 는 dir 의 원장 파일 일곱(manifest + JSONL 여섯)을 되읽는다.
func Read(dir string) (store.LedgerDump, Manifest, error) {
	var d store.LedgerDump
	var m Manifest

	body, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		return d, m, fmt.Errorf("매니페스트를 읽지 못했다(%q): %w", clip(dir, 200), err)
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return d, m, fmt.Errorf("매니페스트 해석 실패(%q): %w", clip(dir, 200), err)
	}
	if m.Format != FormatName {
		return d, m, fmt.Errorf("이 디렉토리는 판단 원장이 아니다(format=%q)", clip(m.Format, 64))
	}
	if m.FormatVersion != FormatVersion {
		return d, m, fmt.Errorf("원장 형식 버전이 %d 인데 이 바이너리는 %d 를 안다",
			m.FormatVersion, FormatVersion)
	}

	if d.Machines, err = readLines[store.LedgerMachine](dir, machinesFile); err != nil {
		return d, m, err
	}
	if d.Projects, err = readLines[store.LedgerProject](dir, projectsFile); err != nil {
		return d, m, err
	}
	if d.Sessions, err = readLines[store.LedgerSession](dir, sessionsFile); err != nil {
		return d, m, err
	}
	if d.Judgments, err = readLines[store.LedgerJudgment](dir, judgmentsFile); err != nil {
		return d, m, err
	}
	if d.Links, err = readLines[store.LedgerLink](dir, linksFile); err != nil {
		return d, m, err
	}
	if d.Snapshots, err = readLines[store.LedgerSnapshot](dir, snapshotsFile); err != nil {
		return d, m, err
	}

	// ★ 매니페스트가 적은 건수와 실제 행 수를 대조한다. 세대 혼합의 둘째 방벽이다 —
	//   format·format_version 만 보면 앞 몇 파일만 새 세대인 자리가 그대로 통과한다.
	if err := JudgeCounts(m.Counts, CountsOf(d)); err != nil {
		return d, m, fmt.Errorf("%w (%s)", err, clip(dir, 200))
	}
	return d, m, nil
}

// readLines 는 JSONL 한 파일을 T 슬라이스로 읽는다.
//
// ★ 행이 0개면 nil 을 낸다(var out []T 의 영값). 이것이 실측으로 확인된 계약이다 —
// internal/store 의 readLedgerJudgments 류도 똑같이 `var out []T` 로 시작해 0행이면
// nil 을 낸다. 즉 DB 쪽 원본과 이 되읽기가 "표가 비었다"를 같은 값(nil)으로 표현하므로
// reflect.DeepEqual 왕복이 저절로 닫힌다. 여기서 인위적으로 빈 non-nil 슬라이스로
// 바꾸면 오히려 실제 DB 원본과의 대칭이 깨진다(TestReadRoundTripsEmptyLedger 참고).
func readLines[T any](dir, name string) ([]T, error) {
	f, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		return nil, fmt.Errorf("원장 파일을 열지 못했다(%q): %w", clip(name, 64), err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)

	var out []T
	for line := 0; sc.Scan(); line++ {
		raw := bytes.TrimSpace(sc.Bytes())
		if len(raw) == 0 {
			continue
		}
		var v T
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("원장 행 해석 실패(%s 의 %d번째 줄): %w", clip(name, 64), line+1, err)
		}
		out = append(out, v)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("원장 파일 순회 실패(%q): %w", clip(name, 64), err)
	}
	return out, nil
}
