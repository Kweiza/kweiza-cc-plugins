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

// JudgeRestoreSchema 는 이 원장을 **되쓸 수 있는가**를 판정한다. 순수 함수다.
//
// ★ 왜 Read 가 아니라 여기인가. 되읽기 자체는 관대해야 한다 — 옛 원장을 열어 **보는**
// 것은 안전하고, 사고가 난 날 가장 먼저 하는 일이 그것이다. 위험한 것은 세대가 다른
// 원장을 이 판의 DB 에 **되쓰는** 것이다. 그래서 안전핀을 복원 경로에만 건다.
//
// ★ 어느 방향이든 거절한다.
//
//	원장 < 바이너리 — 그 뒤 생긴 컬럼이 JSONL 에 없다. 되쓰기는 목록대로 인자를 채우므로
//	  없는 값이 영값으로 들어가 NULL 이어야 할 자리가 ""가 되거나 NOT NULL 위반으로
//	  트랜잭션 전체가 죽는다. 앞쪽이 더 나쁘다 — 조용하다.
//	원장 > 바이너리 — 이 바이너리가 모르는 컬럼이 실려 온다. 구 바이너리로 신 DB 를
//	  여는 것을 PlanMigration 이 이미 거절하는 것과 같은 이유다.
//
// 세대를 맞추는 것은 이 명령이 할 수 있는 일이 아니다. "그 판의 바이너리로 넣어라"가
// 조용히 넣는 것보다 복구 가능하다.
func JudgeRestoreSchema(ledgerVersion, codeVersion int) error {
	if ledgerVersion == codeVersion {
		return nil
	}
	return fmt.Errorf("이 원장은 스키마 %d 판에서 떴는데 이 바이너리는 %d 를 안다 "+
		"— 세대가 다른 원장을 되쓰면 컬럼이 조용히 어긋난다. 스키마 %d 판의 바이너리로 "+
		"되쓰거나, 그 판에서 뜬 원장을 써라", ledgerVersion, codeVersion, ledgerVersion)
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
