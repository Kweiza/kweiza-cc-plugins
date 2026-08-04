package window

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const stamp = "2006-01-02T15:04:05Z"

// Load 는 좌표 하나의 비콘을 읽는다. 없으면 오류다.
func Load(dir string, k Key) (Beacon, error) {
	raw, err := os.ReadFile(filepath.Join(dir, k.FileName()))
	if err != nil {
		return Beacon{}, fmt.Errorf("비콘을 못 읽었다(%s): %w", k.FileName(), err)
	}
	return Decode(raw)
}

// Plant 는 MCP 가 자기 창의 자리를 표시하는 일이다.
//
// ★ **병합이다, 덮어쓰기가 아니다**(설계 개정 ②). MCP 는 대화 중간에 재기동되고,
// 그때 통째로 쓰면 훅이 방금 고쳐 놓은 cc·session_id 를 이 프로세스의 낡은 env 값으로
// 되돌린다 — 그리고 ensureSession 이 그 낡은 값을 집어 고치려던 버그를 재현한다.
// 그래서 이미 있는 비콘의 **정체 두 필드는 절대 안 건드린다.**
//
// ★ 좌표가 반쪽이거나 cc 가 없으면 **아무것도 안 쓴다**(설계 개정 ①).
// Cursor 처럼 claude 가 아닌 부모 밑에서 뜬 MCP 는 어떤 훅도 못 맞추는 pid 로 키를 잡게 되므로,
// 그 자리에 파일을 남기면 가지치기 대상만 늘고 얻는 것이 없다.
func Plant(dir string, k Key, worktree, cc string, now time.Time) (Beacon, error) {
	if !k.Valid() {
		return Beacon{}, fmt.Errorf("창 좌표가 반쪽이라 심지 않는다(machine=%q pid=%d started=%q)",
			k.MachineID, k.ClaudePID, k.Started)
	}
	if strings.TrimSpace(cc) == "" {
		return Beacon{}, fmt.Errorf("cc_session_id 가 없어 심지 않는다 — 이 프로세스는 자기가 어느 대화인지 모른다")
	}

	b := Beacon{
		ClaudePID: k.ClaudePID, ClaudeStarted: k.Started, MachineID: k.MachineID,
		Worktree: worktree, CCSessionID: cc, UpdatedAt: now.UTC().Format(stamp),
	}
	if old, err := Load(dir, k); err == nil {
		if strings.TrimSpace(old.CCSessionID) != "" {
			b.CCSessionID = old.CCSessionID
		}
		b.SessionID = old.SessionID
	}
	if err := write(dir, k, b); err != nil {
		return Beacon{}, err
	}
	return b, nil
}

// SaveIdentity 는 훅이 현재 cc 와 그 카드를 적는 일이다. 좌표는 보존한다.
func SaveIdentity(dir string, k Key, cc, sessionID string, now time.Time) (Beacon, error) {
	if !k.Valid() {
		return Beacon{}, fmt.Errorf("창 좌표가 반쪽이라 적지 않는다")
	}
	b, err := Load(dir, k)
	if err != nil {
		// 비콘이 없으면 훅이 처음 만드는 것이다. 좌표는 인자에서 온다.
		b = Beacon{ClaudePID: k.ClaudePID, ClaudeStarted: k.Started, MachineID: k.MachineID}
	}
	if strings.TrimSpace(cc) != "" {
		b.CCSessionID = cc
	}
	if strings.TrimSpace(sessionID) != "" {
		b.SessionID = sessionID
	}
	b.UpdatedAt = now.UTC().Format(stamp)
	if err := write(dir, k, b); err != nil {
		return Beacon{}, err
	}
	return b, nil
}

// write 는 비콘 하나를 원자적으로 적는다.
//
// ★ 임시 파일 이름에 **자기 pid 를 넣는다.** 키마다 고정 임시 경로를 쓰면 같은 창의 훅과 MCP 가
// 동시에 쓸 때 서로의 임시 파일을 덮는다 — cmd/fd 의 Cache.Put 이 가진 바로 그 결함이다
// (cmd/fd/client.go:67-83 이 스스로 동시성 안전하지 않다고 적어 뒀다).
func write(dir string, k Key, b Beacon) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("비콘 디렉토리를 못 만들었다(%s): %w", dir, err)
	}
	raw, err := Encode(b)
	if err != nil {
		return err
	}
	final := filepath.Join(dir, k.FileName())
	tmp := final + ".tmp." + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("비콘 임시 파일을 못 적었다: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("비콘을 제자리로 못 옮겼다: %w", err)
	}
	return nil
}
