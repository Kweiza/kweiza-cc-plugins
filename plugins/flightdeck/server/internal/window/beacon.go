// Package window 는 Claude Code 창 하나를 가리키는 비콘을 다룬다.
//
// ★ 왜 이 패키지가 internal 에 있나. 쓰는 쪽이 둘이다 — 심는 것은 internal/mcpsrv,
// 읽고 고치는 것은 cmd/fd 의 훅이다. cmd/fd 는 package main 이라 mcpsrv 가 임포트할 수 없으므로
// 두 쪽이 공유하는 판단은 여기 말고 살 자리가 없다.
//
// ★ 그리고 그 판단은 **한 벌이어야 한다.** claude_started 를 얻는 헬퍼가 두 벌이 되면
// 쓰는 쪽과 읽는 쪽의 문자열이 갈려 pid 재사용 방어가 조용히 죽는다.
package window

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Key 는 창 하나의 좌표다. 파일명이 곧 이 값이다.
//
// ★ 축이 셋인 이유. machine 은 여러 머신이 한 홈을 공유(NFS)할 때, pid 는 창끼리,
// started 는 pid 재사용을 가른다. 셋 중 하나라도 빠지면 남의 창 비콘을 자기 것으로 읽는 길이 열린다.
type Key struct {
	MachineID string
	ClaudePID int
	Started   string
}

// FileName 은 이 좌표의 파일 이름이다.
//
// ★ machine id 는 hostname 에서 온다(mcpsrv/identity.go). 즉 **외부 입력**이고
// 경로 구분자가 들어올 수 있다. 그대로 쓰면 디렉토리를 벗어나므로 안전하게 바꾼다.
// scrub 함수는 여러 입력이 같은 파일명으로 붕괴하지 않도록 전사 함수(injective)여야 한다 —
// 다르면 여러 머신이 한 파일을 공유하게 된다.
func (k Key) FileName() string {
	return scrub(k.MachineID) + "-" + strconv.Itoa(k.ClaudePID) + "-" + scrub(k.Started) + ".json"
}

// Valid 는 이 좌표로 파일을 만들어도 되는지다. 빈 축이 있으면 안 된다 —
// 빈 값끼리는 서로 같아 보여서 다른 창이 한 파일을 공유하게 된다.
func (k Key) Valid() bool {
	return strings.TrimSpace(k.MachineID) != "" && k.ClaudePID > 0 && strings.TrimSpace(k.Started) != ""
}

func scrub(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "_%02x", c)
		}
	}
	out := b.String()
	if out == "" {
		return "x"
	}
	return out
}

// Beacon 은 창 하나가 남긴 내용이다.
//
// ★ CCSessionID 와 SessionID 는 **훅만 쓴다.** MCP 는 자리가 비었을 때 초벌로 채울 뿐
// 이미 있는 값을 덮지 않는다 — MCP 는 대화 중간에 재기동되고, 그때 덮으면 훅이 방금 고친
// 값을 낡은 env 값으로 되돌린다(설계 개정 ②).
type Beacon struct {
	ClaudePID     int    `json:"claude_pid"`
	ClaudeStarted string `json:"claude_started"`
	MachineID     string `json:"machine_id"`
	Worktree      string `json:"worktree"`
	CCSessionID   string `json:"cc_session_id"`
	SessionID     string `json:"session_id"`
	UpdatedAt     string `json:"updated_at"`
}

// Key 는 이 비콘이 주장하는 좌표다.
func (b Beacon) Key() Key {
	return Key{MachineID: b.MachineID, ClaudePID: b.ClaudePID, Started: b.ClaudeStarted}
}

func Encode(b Beacon) ([]byte, error) {
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("비콘 직렬화 실패: %w", err)
	}
	return append(raw, '\n'), nil
}

func Decode(raw []byte) (Beacon, error) {
	var b Beacon
	if err := json.Unmarshal(raw, &b); err != nil {
		return Beacon{}, fmt.Errorf("비콘이 JSON 이 아니다(%d바이트): %w", len(raw), err)
	}
	return b, nil
}
