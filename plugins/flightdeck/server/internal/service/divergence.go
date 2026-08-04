package service

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kweiza/flightdeck/internal/model"
)

// 정체 갈림 관측 — **판정이 아니라 관측이다.**
//
// ★ 이 자리가 생긴 이유: 머신 id 가 채널마다 갈려 한 대화가 보드에 카드 세 장으로 뜬 결함이
// 오래 살았는데, 오래 산 이유는 값이 갈렸다는 것을 **아무 데서도 말하지 않았기 때문**이다.
// 클라이언트 두 자리를 고쳐도 환경 축이 하나 더 생기면 같은 침묵이 반복된다
// (project 축이 특히 그렇다 — CLI·훅은 FD_PROJECT 가 이기는 사슬로 풀고,
// MCP 는 CLAUDE_PROJECT_DIR|cwd 의 마지막 성분이며 FD_PROJECT 를 아예 안 본다.
// 지금 안 갈리는 것은 cmd/fd 의 WithProject 주입 덕분이지 축이 안전해서가 아니다).
//
// ★ **키는 안 바꾼다.** 세션 정체는 (machine, worktree, cc) 3중키 그대로다. 갈렸다고 합치면
// 워크트리 축이 주는 보증(경로 재사용 시 옛 행과 안 합쳐진다 · 조상 트리 등록을 안 물려받는다)이
// 사라진다. 여기서 하는 일은 사실을 남기는 것뿐이다.

// DivergenceAxis 는 무엇이 갈렸는지다.
type DivergenceAxis string

const (
	AxisMachine DivergenceAxis = "machine"
	AxisProject DivergenceAxis = "project"
)

// Divergence 는 같은 대화에 다른 좌표가 들어온 사실 하나다.
type Divergence struct {
	Axis      DivergenceAxis
	Incoming  string // 이번에 들어온 값
	Existing  string // 이미 있던 값
	SessionID string // 그 옛 값을 들고 있는 세션 행
}

// JudgeIdentityDivergence 는 이번 입력과 기존 행들을 대 보고 갈린 축을 낸다. 순수 함수다.
//
// ★ 축마다 **값 하나당 한 줄**로 접는다. 같은 옛 machine 을 든 행이 셋이면 줄도 셋이 되는데,
// 읽는 쪽이 알아야 하는 것은 "어떤 값으로 갈렸나"이지 "몇 행이 그 값을 들었나"가 아니다.
// 접지 않으면 카드가 늘어날수록 같은 사실이 그만큼 반복된다.
func JudgeIdentityDivergence(in OpenSessionInput, others []model.Session) []Divergence {
	seen := map[string]bool{}
	var out []Divergence
	add := func(axis DivergenceAxis, existing, sessionID string) {
		if strings.TrimSpace(existing) == "" {
			return
		}
		k := string(axis) + "\x00" + existing
		if seen[k] {
			return
		}
		seen[k] = true
		incoming := in.MachineID
		if axis == AxisProject {
			incoming = in.Project
		}
		out = append(out, Divergence{
			Axis: axis, Incoming: incoming, Existing: existing, SessionID: sessionID,
		})
	}
	for _, o := range others {
		if o.MachineID != in.MachineID {
			add(AxisMachine, o.MachineID, o.ID)
		}
		if o.Project != in.Project {
			add(AxisProject, o.Project, o.ID)
		}
	}
	// 순서를 고정한다 — 로그와 이벤트 페이로드가 실행마다 달라지면 비교가 안 된다.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Axis != out[j].Axis {
			return out[i].Axis < out[j].Axis
		}
		return out[i].Existing < out[j].Existing
	})
	return out
}

// RenderDivergence 는 갈림들을 한 줄 사유로 만든다. 순수 함수다.
func RenderDivergence(ds []Divergence) string {
	if len(ds) == 0 {
		return ""
	}
	parts := make([]string, 0, len(ds))
	for _, d := range ds {
		parts = append(parts, fmt.Sprintf("%s: 들어온 값 %q · 이미 있던 값 %q(세션 %s)",
			d.Axis, clip(d.Incoming, 64), clip(d.Existing, 64), clip(d.SessionID, 64)))
	}
	return strings.Join(parts, " · ")
}

// divergencePayload 는 이벤트 원장에 실을 모양이다.
func divergencePayload(in OpenSessionInput, ds []Divergence) map[string]any {
	axes := make([]map[string]string, 0, len(ds))
	for _, d := range ds {
		axes = append(axes, map[string]string{
			"axis": string(d.Axis), "incoming": clip(d.Incoming, 64),
			"existing": clip(d.Existing, 64), "existing_session": clip(d.SessionID, 64),
		})
	}
	return map[string]any{
		"cc_session": clip(in.CCSessionID, 64),
		"worktree":   clip(in.Worktree, 200),
		"axes":       axes,
	}
}
