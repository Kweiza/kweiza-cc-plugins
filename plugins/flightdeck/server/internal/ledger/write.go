package ledger

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// WriteStage 는 원장 쓰기가 어느 단에서 실패했는지다. 이 값이 되쓸 자리의 상태를 정한다.
type WriteStage string

const (
	// StagePrepare 는 tmp 일곱을 쓰는 단이다. 되쓸 자리는 아직 한 글자도 안 바뀌었다.
	StagePrepare WriteStage = "prepare"
	// StageCommit 은 tmp 를 제자리로 옮기는 단이다. 매니페스트를 이미 걷어냈으므로
	// 이 단에 들어간 순간부터 되쓸 자리는 "원장이 아닌 상태"다.
	StageCommit WriteStage = "commit"
)

// WriteVerdict 는 실패한 쓰기가 되쓸 자리에 무엇을 남겼는지다.
type WriteVerdict struct {
	Code   string
	Intact bool // 되쓸 자리가 그대로인가
	Reason string
}

// JudgeWriteFailure 는 실패한 원장 쓰기가 되쓸 자리에 남긴 상태를 판정한다. 순수 함수다.
//
// ★ 불리언이 아니라 **사유**를 낸다. "쓰기가 실패했다"만 알면 되쓸 자리가 그대로인지
// 반쯤 덮였는지가 구분되지 않는데, 그 둘은 사용자가 다음에 할 일이 완전히 다르다 —
// 전자는 앞선 백업이 살아 있고 후자는 그 자리를 더 이상 백업으로 믿으면 안 된다.
// 이 저장소의 runFinish 가 "끝났는데 못 닫은 상태를 그대로 낸다"로 규율화한 바로 그 자리다.
func JudgeWriteFailure(stage WriteStage, moved int) WriteVerdict {
	if stage == StagePrepare {
		return WriteVerdict{Code: "untouched", Intact: true,
			Reason: "되쓸 자리는 한 글자도 안 바뀌었다 — 임시 파일을 쓰는 동안 실패했다. " +
				"앞선 산출물이 있었다면 그대로 남아 있다"}
	}
	return WriteVerdict{Code: "half-covered", Intact: false,
		Reason: fmt.Sprintf("되쓸 자리가 반쯤 덮였다 — 데이터 파일 %d개가 새 세대로 바뀌었고 나머지는 옛 세대다. "+
			"세대가 섞인 원장으로 복원하면 새 판단이 가리키는 세션이 옛 파일에 없어 "+
			"커밋 시점 FK 검사에서 판단 전량이 롤백된다. 그래서 매니페스트(%s)를 걷어내 이 자리를 "+
			"무효화했다 — 이 자리는 더 이상 원장이 아니다. 실패 원인을 걷어낸 뒤 같은 자리에 "+
			"--force 로 다시 내보내면 복구된다(무효화된 자리는 자기 산출물로 안 보이므로 --force 가 필요하다)",
			moved, ManifestName)}
}

// WriteError 는 원장 쓰기 실패다. **되쓸 자리의 상태 판정을 함께 실어 온다.**
// 그래야 CLI 가 판정을 다시 하지 않고 그대로 인쇄한다(같은 판정을 두 자리에 두지 않는다).
type WriteError struct {
	Verdict WriteVerdict
	Err     error
}

func (e *WriteError) Error() string { return fmt.Sprintf("%v — %s", e.Err, e.Verdict.Reason) }
func (e *WriteError) Unwrap() error { return e.Err }

func newWriteError(stage WriteStage, moved int, err error) error {
	return &WriteError{Verdict: JudgeWriteFailure(stage, moved), Err: err}
}

// Write 는 인코딩된 파일들을 dir 에 쓴다. 만든 파일 이름을 정렬해 낸다.
//
// ★ 단이 둘이다. 먼저 **일곱을 다 tmp 로 쓰고**(준비), 그 다음 제자리로 옮긴다(교체).
// 파일마다 쓰고-옮기기를 반복하면 중간 실패가 세대 혼합을 만든다 — 같은 자리에 두 번
// 내보내는 것이 축복된 정상 경로라(outguard.go) 그 자리엔 옛 세대 일곱이 이미 있고,
// 앞 넷만 새것으로 바뀌면 모자란 파일이 하나도 없는 채로 원장이 거짓말을 한다.
//
// ★ 매니페스트가 **커밋 지점**이다. 교체 단은 옛 매니페스트를 먼저 걷어내고, 데이터
// 여섯을 옮기고, 새 매니페스트를 맨 마지막에 착지시킨다. 그래서 이 자리의 불변식은
// 하나로 줄어든다 — **manifest.json 이 있으면 그 옆의 여섯은 그것과 같은 세대다.**
// 매니페스트를 마지막으로 옮기기만 해서는 안 고쳐진다: 데이터 둘째까지 옮기고 죽으면
// 새 데이터 + 옛 매니페스트로 혼합이 똑같이 생긴다. 먼저 걷어내야 그 상태가 "원장 아님"이 된다.
//
// ★ 이 무효화가 **선제적**인 이유. 실패한 뒤에 치우는 방식은 프로세스가 급사하면
// 아예 안 돈다(SIGKILL·전원 차단). 먼저 걷어내면 그 창 안의 어떤 죽음도 "매니페스트 없음"으로
// 끝난다. 대가는 정직하게 적는다: 교체 단이 첫 rename 에서 실패하면 데이터는 옛 세대
// 그대로인데 매니페스트만 잃는다. tmp 일곱이 그 자리에 다 쓰인 뒤라 재실행이 곧 복구다.
func Write(files map[string][]byte, dir string) ([]string, error) {
	if _, ok := files[ManifestName]; !ok {
		return nil, fmt.Errorf("원장 쓰기에 %s 가 없다 — 매니페스트가 이 쓰기의 커밋 지점이라 "+
			"그것 없이는 자리를 닫을 방법이 없다", ManifestName)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("원장 디렉토리 생성 실패(%q): %w", clip(dir, 200), err)
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	// ★ map 순회는 순서가 흔들린다. 정렬해야 반환 목록과 출력이 실행마다 같다.
	sort.Strings(names)

	// ── 준비 단 ── 일곱을 다 tmp 로 쓴다. 여기서 실패하면 되쓸 자리는 그대로다.
	tmps := make(map[string]string, len(names))
	// 남은 tmp 를 치운다. 이미 옮겨진 것은 ENOENT 라 무해하다.
	// ★ 실패해도 치운다. os.WriteFile 은 O_CREATE|O_TRUNC 로 먼저 만들고 쓰므로
	//   ENOSPC 로 죽어도 파일은 남고, 이름이 유일해진 뒤로는 아무도 안 치운다.
	cleanup := func() {
		for _, tmp := range tmps {
			os.Remove(tmp)
		}
	}
	for _, name := range names {
		path := filepath.Join(dir, name)
		// ★ tmp 이름은 프로세스마다 다르다. 고정 이름이면 떨어진 갈래 둘이 같은 tmp 에
		//   O_TRUNC 로 쓰고, 그러면 서로의 바이트가 섞인 채 rename 된다.
		tmp := fmt.Sprintf("%s.%s.tmp", path, tmpNonce())
		// 0600 — 판단 본문이라 남이 못 읽게 한다.
		if err := os.WriteFile(tmp, files[name], 0o600); err != nil {
			os.Remove(tmp)
			cleanup()
			return nil, newWriteError(StagePrepare, 0,
				fmt.Errorf("원장 기록 실패(%q): %w", clip(path, 200), err))
		}
		tmps[name] = tmp
	}

	// ── 교체 단 ── 첫 동작이 옛 매니페스트를 걷어내는 것이다(위 주석의 불변식).
	manifestPath := filepath.Join(dir, ManifestName)
	if err := os.Remove(manifestPath); err != nil && !os.IsNotExist(err) {
		// 아직 아무것도 안 옮겼고 매니페스트도 그대로다 — 자리는 온전하다.
		cleanup()
		return nil, newWriteError(StagePrepare, 0,
			fmt.Errorf("옛 매니페스트를 걷어내지 못했다(%q): %w", clip(manifestPath, 200), err))
	}

	moved := 0
	for _, name := range names {
		if name == ManifestName {
			continue // 맨 마지막에 착지한다 — 그것이 커밋이다
		}
		if err := os.Rename(tmps[name], filepath.Join(dir, name)); err != nil {
			cleanup()
			return nil, newWriteError(StageCommit, moved,
				fmt.Errorf("원장 교체 실패(%q): %w", clip(filepath.Join(dir, name), 200), err))
		}
		moved++
	}
	if err := os.Rename(tmps[ManifestName], manifestPath); err != nil {
		cleanup()
		return nil, newWriteError(StageCommit, moved,
			fmt.Errorf("원장 교체 실패(%q): %w", clip(manifestPath, 200), err))
	}
	return names, nil
}

// tmpNonce 는 임시 파일 이름에 붙일 조각이다. pid 만으로는 부족하다 —
// 한 프로세스 안 두 고루틴도 같은 tmp 를 다툴 수 있다.
// (cmd/fd/outbox.go 에 같은 함수가 있지만 그것은 package main 이라 여기서 못 쓴다.)
func tmpNonce() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d-%x", os.Getpid(), time.Now().UnixNano())
	}
	return fmt.Sprintf("%d-%s", os.Getpid(), hex.EncodeToString(b[:]))
}

// clip 은 오류에 실을 외부 문자열을 자른다.
// (store·legacy 에 같은 함수가 있으나 둘 다 unexported 다.)
func clip(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n]) + "…"
}
