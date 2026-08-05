// Package buildinfo 는 **이 프로세스가 어느 소스에서 나왔는가**를 읽는 한 자리다.
//
// ★ 왜 별도 패키지인가 — 서버(`internal/api`)와 클라이언트(`cmd/fd`)가 **같은 축**을 읽어
// 서로 대조해야 한다. 양쪽에 각각 두면 사다리가 두 벌이 되고, 두 벌은 반드시 표류한다
// (`TestBeaconDirDelegatesToTheOneOwner` 가 같은 이유로 서 있다). 대조하는 두 값이 서로
// 다른 규칙으로 만들어지면 그 대조는 아무것도 증명하지 않는다.
package buildinfo

import (
	"fmt"
	"runtime/debug"
	"strings"
)

// Coord 는 빌드 좌표다.
//
// ★ **Known 이 먼저다.** false 면 나머지 필드는 값이 아니라 빈칸이다.
// 좌표가 없는 것은 예외가 아니라 **정상 갈래**다 — 실측(2026-08-05)에서
// `~/.claude/plugins/data/.../bin/fd` 에는 `build vcs=…` 줄이 하나도 없었다.
// git 워크트리 밖에서 빌드했거나 `-buildvcs=false` 면 그렇게 된다.
// 그때 빈 문자열을 찍고 넘어가면 "좌표가 같다"로 읽힌다 — 그것이 이 항목이 고치려는 침묵이다.
type Coord struct {
	Known    bool   `json:"known"`
	Revision string `json:"revision,omitempty"` // vcs.revision (전체 sha)
	Time     string `json:"time,omitempty"`     // vcs.time (RFC3339)
	Modified bool   `json:"modified,omitempty"` // 커밋 안 된 변경 위에서 빌드했다
	// Reason 은 Known=false 일 때 **왜 없는지**다. 침묵 대신 사유를 낸다.
	Reason string `json:"reason,omitempty"`
}

// noVCSReason 은 좌표가 없는 이유를 사람이 다음에 할 일로 옮긴다.
//
// ★ "git worktree 에서 빌드했다"를 **첫 원인으로 적는다.** 이 레포는 항목마다 워크트리를
// 파므로 그 경로가 기본값이고, 실측(2026-08-05)에서 저장소 밖 워크트리 빌드는 스탬프가
// 하나도 안 붙었다 — `.git` 이 디렉토리가 아니라 파일이라 go 가 VCS 뿌리로 안 본다.
const noVCSReason = "이 바이너리에 VCS 스탬프가 없다 — git worktree 에서 빌드했거나" +
	"(.git 이 파일이면 go 가 VCS 뿌리로 안 본다) -buildvcs=false 다"

// Of 는 build info 하나에서 좌표를 뽑는다. 순수 함수다.
//
// `debug.ReadBuildInfo` 를 직접 부르지 않는 이유: 시험 바이너리는 자기 자신의 정보를
// 내므로 내용을 시험할 수 없다. 읽는 것과 해석하는 것을 가른다.
func Of(bi *debug.BuildInfo, ok bool) Coord {
	if !ok || bi == nil {
		return Coord{Reason: "빌드 정보를 못 읽었다 — go build 로 만든 바이너리가 아닐 수 있다"}
	}
	var c Coord
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			c.Revision = s.Value
		case "vcs.time":
			c.Time = s.Value
		case "vcs.modified":
			c.Modified = s.Value == "true"
		}
	}
	// revision 이 이 축의 정체다. time 만 있고 revision 이 없으면 대조할 것이 없다.
	if strings.TrimSpace(c.Revision) == "" {
		return Coord{Reason: noVCSReason}
	}
	c.Known = true
	return c
}

// Self 는 지금 도는 **이 프로세스**의 좌표다.
//
// ★ **이 축이 못 보는 것을 여기 적어 둔다**(§13 — 안 잰 축을 잰 척하지 않는다).
// go 의 VCS 스탬프는 **git worktree 를 안 따라간다.** 2026-08-05 에 둘 다 쟀다:
//
//   - 워크트리가 부모 저장소 **안**에 있으면(이 레포의 `.flightdeck/worktrees/…` 가 그렇다)
//     go 가 위로 걸어 올라가 부모의 `.git` 디렉토리를 뿌리로 잡고 **부모의 HEAD** 를 찍는다.
//     브랜치에 커밋이 있어도 `vcs.modified=false` 다 — **그럴듯하게 틀린다.**
//   - 워크트리가 저장소 **밖**이면 `.git` 이 파일이라 뿌리를 못 찾고 **스탬프가 아예 없다**.
//
// 그래서 이 축은 "배포된 판이 main 대비 낡았나"에는 답하지만, **브랜치 빌드끼리는 못 가른다.**
// 같은 main tip 에서 판 두 브랜치의 빌드는 좌표가 같다.
// 이 항목이 잡으려던 증상(배포판이 조용히 낡음)에는 그대로 유효하다.
//
// ★ 파일이 아니라 프로세스다. 실측(2026-08-05)에서 `/proc/<pid>/exe` 가 `(deleted)` 인
// 채로, 같은 자리의 파일은 이미 최신으로 교체돼 있었다 — 파일을 재는 진단은 그 상황에서
// "최신"이라 답하고 응답하는 코드는 14시간 전 것이었다. 프로세스만이 자기 나이를 안다.
func Self() Coord { return Of(debug.ReadBuildInfo()) }

// zeroReason 은 **제로값 Coord** 의 사유다.
//
// ★ 이 갈래를 실물이 찾아냈다. Of 가 만든 Coord 는 항상 Reason 을 들지만, 이 축을 안 내는
// 서버의 응답을 역직렬화하면 필드 자체가 없어 `Coord{}` 가 나온다 — Known=false·Reason="".
// 그대로 찍으면 `서버 판 ` 이라는 **빈칸**이 되고, 그 침묵이 정확히 이 항목이 고치려는 것이다.
// 부재를 아는 것과 부재를 말하는 것은 다르다.
const zeroReason = "좌표 없음 — 이 응답에 그 축이 아예 없다(이 축을 알리기 전 판이다)"

// Short 는 한 줄 표시용이다. 좌표가 없으면 사유를 낸다 — **어떤 경우에도 빈칸을 내지 않는다.**
func Short(c Coord) string {
	if !c.Known {
		if strings.TrimSpace(c.Reason) == "" {
			return zeroReason
		}
		return c.Reason
	}
	s := ShortRev(c.Revision)
	if c.Time != "" {
		s += " · " + c.Time
	}
	if c.Modified {
		s += " · 커밋 안 된 변경 위에서 빌드됨"
	}
	return s
}

// ShortRev 는 sha 앞 7자다. 순수 함수다.
func ShortRev(rev string) string {
	if len(rev) > 7 {
		return rev[:7]
	}
	return rev
}

// VintageBanner 는 두 좌표를 대조해 **다를 때만** 한 줄을 낸다. 순수 함수다.
//
// ★ `SkewBanner` 로는 이 갈래를 못 잡는다. 그쪽은 `api_version` 만 보는데 그 값은
// **계약이 깨질 때만** 오른다 — 판이 37커밋 벌어져도 계약이 그대로면 "1" == "1" 이다.
// 실제로 그 구간에서 `pick` 의 큐 열림 줄이 통째로 사라졌고 아무 신호도 안 났다.
//
// **모르는 쪽이 있으면 그 사실을 낸다.** 한쪽이라도 좌표가 없으면 대조 자체가 성립하지
// 않는데, 침묵하면 "같다"로 읽힌다.
func VintageBanner(client, server Coord) string {
	switch {
	case !client.Known && !server.Known:
		return "" // 양쪽 다 모른다 — 낼 말이 없다. 자리는 doctor 가 사유와 함께 찍는다
	case !server.Known:
		return "⚠ 서버가 빌드 좌표를 안 낸다 — 이 축을 알리기 전 판이거나 VCS 스탬프 없이 빌드됐다. " +
			"판 나이 차이를 여기서는 가릴 수 없다."
	case !client.Known:
		return "⚠ 이 클라이언트에 빌드 좌표가 없다 — 서버는 " + ShortRev(server.Revision) +
			" 다. 대조할 자기 값이 없어 판 나이 차이를 못 가린다."
	case client.Revision == server.Revision:
		return ""
	}
	return fmt.Sprintf("⚠ 판 나이가 다르다: 클라이언트 %s · 서버 %s. "+
		"api_version 은 계약이 깨질 때만 오르므로 이 어긋남은 스큐 배너에 안 잡힌다 — "+
		"축 하나가 조용히 빠진 응답을 받고 있을 수 있다.",
		Short(client), Short(server))
}
