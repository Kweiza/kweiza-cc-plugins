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

// Match 는 계보 대조를 통과한 비콘 하나다.
type Match struct {
	Beacon Beacon
	Key    Key
}

// Find 는 내 조상 사슬 위에 있는 비콘을 찾는다.
//
// ★ **디렉토리를 훑지 않는다.** 조상 pid 로 파일 이름을 조립해 직접 연다.
// 훑어서 "하나뿐이니 이것이겠지"로 고르면, 같은 머신·같은 워크트리에 창이 다섯인 이 환경에서
// **남의 창 비콘을 집어 그 창의 카드를 이 대화의 cc 로 rekey 한다**(설계 개정 ③).
// 그 창의 선점과 판단이 통째로 딴 대화에 붙는 것이라 이 설계에서 가장 나쁜 결과다.
//
// 못 찾은 사유를 함께 낸다. 사유가 없으면 폴백 문구가 "왜 수리가 안 됐나"에 답할 수 없다.
func Find(dir, machineID string, ancestors []int, startedOf func(int) (string, error)) (Match, bool, string) {
	if len(ancestors) == 0 {
		return Match{}, false, "조상 사슬을 못 걸었다 — 이 플랫폼에서 프로세스 계보를 읽을 수 없다"
	}
	var last string
	for _, pid := range ancestors {
		started, err := startedOf(pid)
		if err != nil {
			last = "조상 " + strconv.Itoa(pid) + " 의 시작 시각을 못 읽었다: " + err.Error()
			continue
		}
		k := Key{MachineID: machineID, ClaudePID: pid, Started: started}
		b, err := Load(dir, k)
		if err != nil {
			continue // 이 조상은 비콘을 안 남겼다. 위로 계속 간다
		}
		if b.ClaudePID != pid || b.ClaudeStarted != started || b.MachineID != machineID {
			// 파일 이름은 맞는데 내용이 딴 좌표다 — 손상됐거나 남의 것이다.
			last = "비콘 내용의 좌표가 파일 이름과 안 맞는다(pid " + strconv.Itoa(pid) + ")"
			continue
		}
		return Match{Beacon: b, Key: k}, true, ""
	}
	if last == "" {
		last = "조상 사슬 어디에도 이 머신의 비콘이 없다(조상 " + strconv.Itoa(len(ancestors)) + "개를 봤다)"
	}
	return Match{}, false, last
}

// Prune 은 죽은 창의 비콘을 지운다.
//
// ★ **지우기만 한다.** 남의 파일을 고쳐 쓰지 않으므로 다른 창의 rename 과 안 싸운다.
// 지우려는 순간 그 창이 살아 있었다면 다음 심기가 다시 만든다 — 손해가 없다.
//
// ★★ **판정 대상은 내 머신의 비콘뿐이다.** 홈 하나를 머신 여럿이 공유할 수 있고
// (Key 에 MachineID 축이 있는 이유가 그것이다 — beacon.go 가 NFS 를 이름으로 적어 뒀다),
// 그 홈에서는 alive 가 **남의 머신에 대해 답할 자격이 없는** 질문이 된다. 이 머신의
// 프로세스 표에 남의 pid 가 없는 것은 당연하므로, 거르지 않으면 머신 A 의 훅 한 번이
// 머신 B 의 비콘을 전부 지운다 — 그러면 B 의 창들은 각자 MCP 가 다시 뜰 때까지
// **아무 신호 없이** 표류 수리를 잃는다. 지우는 쪽의 손해가 안 지우는 쪽보다 크다.
//
// ★ machineID 가 비면 아무것도 안 지운다. 심기가 Key.Valid() 를 요구해 빈 머신 id 로는
// 애초에 비콘이 안 생기므로 지울 것도 없고, 여기서 "빈 값은 전부 통과"로 접으면
// 머신 축이 끊긴 배선 하나가 이 디렉토리를 통째로 비운다.
//
// 디렉토리가 없는 것은 오류가 아니다. 첫 실행이 그렇다.
func Prune(dir, machineID string, alive func(int) bool) (removed int, err error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("비콘 디렉토리를 못 읽었다(%s): %w", dir, err)
	}
	if strings.TrimSpace(machineID) == "" {
		return 0, nil
	}
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		raw, rerr := os.ReadFile(filepath.Join(dir, name))
		if rerr != nil {
			continue
		}
		b, derr := Decode(raw)
		if derr != nil {
			continue // 못 읽는 파일을 지우지 않는다 — 남이 쓰는 중일 수 있다
		}
		if b.MachineID != machineID {
			continue // 남의 머신이다. 이 프로세스는 그 pid 가 살았는지 알 수 없다
		}
		if b.ClaudePID > 0 && !alive(b.ClaudePID) {
			if os.Remove(filepath.Join(dir, name)) == nil {
				removed++
			}
		}
	}
	return removed, nil
}
