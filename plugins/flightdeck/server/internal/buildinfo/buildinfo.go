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

// 좌표의 **출처**다. 두 값을 대조하기 전에 이것부터 봐야 한다 —
// git sha 와 소스 지문은 같은 소스에서도 절대 같은 값이 안 나온다.
const (
	// SourceGit 은 go 가 심은 VCS 스탬프다. 정확할 때는 커밋을 정확히 가리키지만,
	// **워크트리에서는 부모 HEAD 를 찍어 그럴듯하게 틀린다**(실측 2026-08-06, 아래 Self 주석).
	SourceGit = "git"
	// SourceFingerprint 는 빌드가 `-ldflags -X` 로 박은 **소스 트리 내용 지문**이다.
	// git 이 없는 자리(플러그인 캐시·컨테이너 빌드 컨텍스트)에서도 나오고,
	// 워크트리에서도 부모가 아니라 **실제로 빌드한 소스**를 가리킨다.
	SourceFingerprint = "fingerprint"
)

// Coord 는 빌드 좌표다.
//
// ★ **Known 이 먼저다.** false 면 나머지 필드는 값이 아니라 빈칸이다.
// 좌표가 없는 것은 예외가 아니라 **정상 갈래**다 — 실측(2026-08-05)에서
// `~/.claude/plugins/data/.../bin/fd` 에는 `build vcs=…` 줄이 하나도 없었다.
// 그때 빈 문자열을 찍고 넘어가면 "좌표가 같다"로 읽힌다 — 그것이 이 항목이 고치려는 침묵이다.
type Coord struct {
	Known bool `json:"known"`
	// Source 는 이 좌표가 어디서 왔는가다(SourceGit · SourceFingerprint).
	// ★ **빈 값은 제3의 출처가 아니라 구 판이다** — 주입 축 이전에는 출처가 git 하나뿐이었다.
	// 빈 값을 모르는 출처로 취급하면 멀쩡한 대조가 "출처 어긋남"으로 죽는다(sourceOf).
	Source   string `json:"source,omitempty"`
	Revision string `json:"revision,omitempty"` // git sha 전체 또는 소스 지문
	Time     string `json:"time,omitempty"`     // 커밋 시각 또는 빌드 시각 (RFC3339)
	Modified bool   `json:"modified,omitempty"` // 커밋 안 된 변경 위에서 빌드했다(git 출처에만 뜻이 있다)
	// Reason 은 Known=false 일 때 **왜 없는지**다. 침묵 대신 사유를 낸다.
	Reason string `json:"reason,omitempty"`
}

// noVCSReason 은 좌표가 없는 이유를 사람이 다음에 할 일로 옮긴다.
//
// ★ **첫 원인을 실측이 바꿨다(2026-08-06).** 종전 문구는 "git worktree 에서 빌드했다"를
// 첫째로 들었는데 그것이 사람을 엉뚱한 데로 보냈다 — 저장소 **안** 워크트리는 스탬프가
// 오히려 **있다**(부모 HEAD 라 틀릴 뿐이다). 부재를 실제로 만드는 것은 `.git` 이 아예
// 없는 소스 트리다: 플러그인 캐시(`~/.claude/plugins/cache/…` 는 git 클론이 아니다)와
// 컨테이너 빌드 컨텍스트(`server/` 만 COPY 된다). 그 둘이 **사용자 전원이 거치는 경로**다.
// 그래서 이 갈래에서 할 일은 git 을 찾는 것이 아니라 **주입이 왜 안 됐는지**를 보는 것이다.
const noVCSReason = "빌드 좌표가 없다 — 이 소스 트리에 .git 이 없고(플러그인 캐시·컨테이너 " +
	"빌드 컨텍스트가 그렇다) 빌드가 소스 지문도 주입하지 않았다. 런처 bin/fd 나 Dockerfile 을 " +
	"안 거친 빌드이거나 지문 계산 도구(sha256sum/shasum)가 없던 것이다"

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
	c.Source = SourceGit
	return c
}

// injectedFingerprint · injectedTime 은 **빌드가 박아 넣는** 좌표다. 링커가 채운다:
//
//	-ldflags "-X github.com/kweiza/flightdeck/internal/buildinfo.injectedFingerprint=<hex>
//	          -X github.com/kweiza/flightdeck/internal/buildinfo.injectedTime=<RFC3339>"
//
// ★ 왜 sha 가 아니라 지문인가 — **주입하는 자리에 git 이 없다.** 런처는 플러그인 캐시에서,
// Dockerfile 은 `server/` 만 COPY 한 컨텍스트에서 빌드한다. 둘 다 sha 를 읽을 방법이 없다.
// 그런데 **두 자리가 해시하는 범위가 정확히 같다**(양쪽 다 `server/` 트리다). 같은 규칙으로
// 같은 범위를 해시하면 sha 없이도 "이 두 판이 같은 소스인가"에 정확히 답한다 —
// 그것이 이 축의 목적 전부다.
var (
	injectedFingerprint string
	injectedTime        string
)

// Resolve 는 주입값과 go 스탬프 중 **무엇을 믿을지** 정한다. 순수 함수다.
//
// ★ **주입이 이긴다.** 부재를 이기는 것이 아니라 **오답을 이긴다** — 실측(2026-08-06)에서
// 워크트리 HEAD `5144c66` 위에서 빌드한 바이너리가 `3f7b497`(부모 main tip)을 자신 있게
// 찍었다. go 는 위로 걸어 올라가 부모의 `.git` 을 뿌리로 잡기 때문이다. 부재라면 침묵하면
// 되지만 이쪽은 틀린 값을 내므로, 실제로 빌드한 소스를 아는 쪽이 우선이어야 한다.
//
// 주입이 없으면 go 값을 그대로 쓴다 — 손빌드가 좌표를 통째로 잃지는 않게 한다.
func Resolve(fingerprint, at string, git Coord) Coord {
	fingerprint = strings.TrimSpace(fingerprint)
	if fingerprint == "" {
		return git
	}
	// Modified 를 물려받지 않는다. 지문은 작업 트리의 **내용**에서 나오므로
	// "커밋 안 된 변경"이라는 개념 자체가 이미 값 안에 녹아 있다.
	return Coord{
		Known:    true,
		Source:   SourceFingerprint,
		Revision: fingerprint,
		Time:     strings.TrimSpace(at),
	}
}

// Self 는 지금 도는 **이 프로세스**의 좌표다.
//
// ★ **go 의 VCS 스탬프만으로는 이 축이 서지 않는다.** 2026-08-06 에 빌드 경로 셋을 다 쟀고,
// **정확한 좌표를 내는 경로가 하나도 없었다**:
//
//   - 런처 `bin/fd`(플러그인 캐시에서 빌드 — **사용자 전원의 경로**): 캐시는 git 클론이
//     아니라 `.git` 이 없다 → **스탬프 부재**.
//   - `server/Dockerfile`(컨테이너 — 지금 도는 공유 서버): 컨텍스트가 `server/` 뿐이라
//     역시 `.git` 이 없다 → **스탬프 부재**. 살아 있는 :7420 이 `known=false` 를 냈다.
//   - 워크트리 직접 빌드(개발 세션의 기본값): go 가 위로 걸어 올라가 **부모의** `.git` 을
//     뿌리로 잡는다. 워크트리 HEAD `5144c66` 위에서 빌드했는데 스탬프는 `3f7b497`(부모
//     main tip)이었고 `vcs.modified` 는 **부모 트리의** 더러움을 반영했다 —
//     부재보다 나쁘다. **그럴듯하게 틀린다.**
//
// 그래서 빌드가 **소스 지문을 주입**하고(Resolve), 주입이 go 스탬프를 이긴다.
// 주입 경로를 안 거친 손빌드는 여전히 go 값을 쓰므로 워크트리에서는 부모 HEAD 가 나온다 —
// 그 갈래는 Source 가 `git` 으로 표시되는 것으로 구별할 수 있게 두었다.
//
// ★ 파일이 아니라 프로세스다. 실측(2026-08-05)에서 `/proc/<pid>/exe` 가 `(deleted)` 인
// 채로, 같은 자리의 파일은 이미 최신으로 교체돼 있었다 — 파일을 재는 진단은 그 상황에서
// "최신"이라 답하고 응답하는 코드는 14시간 전 것이었다. 프로세스만이 자기 나이를 안다.
func Self() Coord { return Resolve(injectedFingerprint, injectedTime, Of(debug.ReadBuildInfo())) }

// sourceOf 는 좌표의 출처를 낸다. **빈 값은 git 이다** — 주입 축 이전 판의 응답에는
// `source` 키가 없고, 그때 Known 인 좌표는 정의상 go 스탬프였다.
func sourceOf(c Coord) string {
	if c.Source == "" {
		return SourceGit
	}
	return c.Source
}

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
	// ★ 지문에 `src:` 를 붙인다. 안 붙이면 7자 hex 가 **sha 로 읽히고**, 사람은
	// `git show` 를 치러 갔다가 "그런 객체가 없다"를 본다. 값이 아니라 값의 뜻이 다르다.
	if sourceOf(c) == SourceFingerprint {
		s = "src:" + s
	}
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
	case sourceOf(client) != sourceOf(server):
		// ★ **비교하면 안 되는 갈래다.** git sha 와 소스 지문은 같은 소스에서도 절대
		// 같은 값이 안 나온다 — 그냥 비교하면 배너가 **항상** 뜨고, 항상 뜨는 경고는
		// 배경이 되어 안 읽힌다. 대조 불가라는 사실 자체를 낸다.
		return "⚠ 두 좌표의 출처가 다르다: 클라이언트 " + Short(client) + " · 서버 " + Short(server) +
			". 한쪽은 git sha, 다른 쪽은 소스 지문이라 값끼리 비교할 수 없다 — " +
			"한쪽이 주입 경로(런처 bin/fd · Dockerfile)를 안 거친 빌드다."
	case client.Revision == server.Revision:
		return ""
	}
	return fmt.Sprintf("⚠ 판 나이가 다르다: 클라이언트 %s · 서버 %s. "+
		"api_version 은 계약이 깨질 때만 오르므로 이 어긋남은 스큐 배너에 안 잡힌다 — "+
		"축 하나가 조용히 빠진 응답을 받고 있을 수 있다.",
		Short(client), Short(server))
}
