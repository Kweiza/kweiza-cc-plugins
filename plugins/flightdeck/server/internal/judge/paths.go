// Package judge 는 flightdeck 의 판정 로직을 담는다.
//
// 이 패키지에 있는 것은 전부 순수 함수다 — 상태도 I/O 도 없다.
// 판정이 핸들러 본문에 흩어지면 시험이 그 로직의 **사본**을 단정하게 되고,
// 그러면 변이가 조용히 새어 나간다. 시험은 반드시 여기 있는 함수를 직접 부른다.
//
// 그리고 다중 조건 판정은 불리언이 아니라 **사유**를 돌려준다.
// 사유가 없으면 "조건 A 때문에 탈락"과 "이 축을 아예 안 본다"가 구분되지 않고,
// 그 구분이 안 되는 도구는 두 번째 세션부터 무시된다.
package judge

import (
	"fmt"
	"strings"
)

// PathsOverlap 은 두 경로 집합이 겹치는지 판정한다.
//
// 기존 도구의 이 자리에 결함이 있었다. 모든 토큰 끝에 "/" 를 붙여 디렉토리로 정규화했기 때문에
// 파일형 토큰이 **자기 자신과도 안 겹쳤다**:
//
//	PathsOverlap(["Makefile"], ["Makefile"])          → 겹침 없음   ← 결함
//	PathsOverlap([".gitleaks.toml"], [".gitleaks.toml tools/x.sh"]) → 겹침 없음  ← 결함
//
// 실측 시점 큐 226건 중 파일형 토큰이 33건이었고 그 전부의 경로 축이 죽어 있었다.
// 그래서 여기서는 **경로 성분(component) 단위**로 비교한다 — 문자열 접두가 아니라.
// 문자열 접두로 하면 "tool/" 이 "tools/" 를 덮는 반대 방향 오탐이 생긴다.
//
// 판정 규칙: 두 경로가 같거나, 한쪽이 다른 쪽의 **조상 디렉토리**이면 겹친다.
func PathsOverlap(a, b []string) bool {
	for _, x := range a {
		cx := components(x)
		if len(cx) == 0 {
			continue
		}
		for _, y := range b {
			cy := components(y)
			if len(cy) == 0 {
				continue
			}
			if pathRelated(cx, cy) {
				return true
			}
		}
	}
	return false
}

// OverlapPairs 는 겹치는 (a, b) 쌍을 전부 돌려준다.
// 사용자에게 "무엇이 겹치는지"를 보여야 하므로 불리언만으로는 부족하다 —
// 거르지 않고 알리는 것이 이 도구의 규율이고, 알리려면 무엇이 겹쳤는지 말할 수 있어야 한다.
func OverlapPairs(a, b []string) [][2]string {
	var out [][2]string
	seen := map[[2]string]bool{}
	for _, x := range a {
		cx := components(x)
		if len(cx) == 0 {
			continue
		}
		for _, y := range b {
			cy := components(y)
			if len(cy) == 0 {
				continue
			}
			if pathRelated(cx, cy) {
				k := [2]string{x, y}
				if !seen[k] {
					seen[k] = true
					out = append(out, k)
				}
			}
		}
	}
	return out
}

// pathRelated 은 한쪽이 다른 쪽과 같거나 조상인지 본다.
// 성분 단위이므로 "tool" 과 "tools" 는 절대 관련되지 않는다.
func pathRelated(x, y []string) bool {
	n := len(x)
	if len(y) < n {
		n = len(y)
	}
	for i := 0; i < n; i++ {
		if x[i] != y[i] {
			return false
		}
	}
	// 여기까지 왔으면 짧은 쪽이 긴 쪽의 접두 성분열이다 = 같거나 조상이다.
	return true
}

// components 는 경로를 성분으로 쪼갠다.
// 앞뒤 "/", 중복 "/", "." 성분을 걷어낸다. ".." 는 걷어내지 않는다 —
// 등록 목록에 ".." 가 들어오는 것은 입력 오류이고, 조용히 정규화하면 그 오류가 안 보인다.
func components(p string) []string {
	p = strings.TrimSpace(p)
	if p == "" {
		return nil
	}
	var out []string
	for _, c := range strings.Split(p, "/") {
		if c == "" || c == "." {
			continue
		}
		out = append(out, c)
	}
	return out
}

// ── 좌표계 관문 ──────────────────────────────────────────────────────────
//
// 이 판정이 있는 이유는 위 components 가 슬래시만 성분 구분자로 보기 때문이다.
// 백슬래시 경로가 저장까지 도달하면 성분 1개가 되어 git 이 주는 경로와 **절대 안 겹치고**,
// 그 결과는 오류가 아니라 '겹침 없음'이라 정상 응답과 구분되지 않는다.
//
// ★ 고치는 자리를 components 가 아니라 **입구**로 잡은 것이 이 설계의 핵심 판단이다.
// POSIX 에서 백슬래시는 파일명에 쓸 수 있는 합법 문자라, components 가 그것을 구분자로
// 보면 `a\b` 라는 정상 파일이 `a/b` 와 겹친다고 오탐한다. 침묵을 오탐과 바꾸는 거래다.
// 근거 전문은 스펙 §3.2 에 있다.

// CoordinateVerdict 는 경로 좌표계 판정 결과다. 사유는 항상 채운다.
//
// ★ 같은 패키지의 ItemPathVerdict 와 **다른 축**이다 — 저쪽은 "경로가 그 프로젝트에
// 실재하는가"(파일시스템 관측)이고, 이쪽은 "경로가 이 서버의 좌표계인가"(문자열 형태)다.
// 둘을 합치지 마라. 실재 판정은 I/O 결과를 받아야 하고 이것은 순수 문자열 판정이다.
type CoordinateVerdict struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason"`
}

// RejectedPath 는 관문이 버린 경로 하나와 그 사유다.
//
// ★ 원본 경로를 그대로 나른다. 정규화한 값을 실으면 사람이 자기가 넣은 것을 못 알아본다.
type RejectedPath struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// coordinateGuidance 는 거절마다 붙는 처방이다.
// "안 된다"만 말하는 거절은 사람이 못 고치고, 못 고치는 거절은 무시된다.
const coordinateGuidance = "이 서버는 POSIX 좌표계(슬래시)만 받는다 — " +
	"Windows 에서는 WSL 로 띄우고 /mnt/c/… 경로를 써라"

// JudgePathCoordinate 는 경로가 이 서버의 좌표계에 맞는지 판정한다. 순수 함수다.
//
// 빈 경로는 **통과시킨다** — 빈 값의 처리는 호출부마다 다르고(worktree 는 거절,
// 발자국은 무시) 그 판정을 여기서 가로채면 호출부의 사유가 이 함수의 사유로 덮인다.
//
// ".." 성분도 통과시킨다. 그것은 실재하는 문제이지만 좌표계 축이 아니다 —
// 상대경로 정책 전반을 함께 정해야 하므로 분리했다(스펙 §6).
func JudgePathCoordinate(p string) CoordinateVerdict {
	q := strings.TrimSpace(p)
	switch {
	case q == "":
		return CoordinateVerdict{OK: true,
			Reason: "빈 경로다 — 좌표계 축은 통과시킨다(호출부가 따로 다룬다)"}
	case isWindowsDriveAbs(q):
		return CoordinateVerdict{Reason: fmt.Sprintf(
			"%q 는 Windows 드라이브 절대경로다. %s", clipPath(q), coordinateGuidance)}
	case strings.HasPrefix(q, `\\`):
		return CoordinateVerdict{Reason: fmt.Sprintf(
			"%q 는 Windows UNC 경로다. %s", clipPath(q), coordinateGuidance)}
	case strings.ContainsRune(q, '\\'):
		return CoordinateVerdict{Reason: fmt.Sprintf(
			"%q 에 백슬래시가 들어 있다. %s. "+
				"정말 파일명에 백슬래시가 있는 것이라면 이 도구는 그 경로를 지원하지 않는다 — "+
				"POSIX 에서는 합법 문자이지만 겹침 판정이 성분을 가르지 못한다",
			clipPath(q), coordinateGuidance)}
	}
	return CoordinateVerdict{OK: true, Reason: "슬래시 좌표계다"}
}

// isWindowsDriveAbs 는 "C:\…" · "C:/…" 형태인지 본다.
//
// 구분자까지 함께 보는 이유는 "a:b" 같은 정상 POSIX 파일명을 드라이브로 오인하지 않기
// 위해서다 — 콜론은 POSIX 파일명에 합법이다.
func isWindowsDriveAbs(p string) bool {
	if len(p) < 3 || p[1] != ':' {
		return false
	}
	c := p[0]
	if !(('A' <= c && c <= 'Z') || ('a' <= c && c <= 'z')) {
		return false
	}
	return p[2] == '\\' || p[2] == '/'
}

// FilterPathCoordinate 는 목록을 관문에 태워 통과분과 버린 것을 가른다. 순수 함수다.
//
// 거르는 쪽과 버린 쪽을 **둘 다** 돌려주는 것이 요점이다. 버린 것을 안 돌려주면
// 호출부가 "몇 개가 사라졌는지"를 말할 수 없고, 그 침묵이 이 항목이 없애려는 것이다.
func FilterPathCoordinate(paths []string) (kept []string, rejected []RejectedPath) {
	for _, p := range paths {
		if v := JudgePathCoordinate(p); !v.OK {
			rejected = append(rejected, RejectedPath{Path: p, Reason: v.Reason})
			continue
		}
		kept = append(kept, p)
	}
	return kept, rejected
}

// clipPath 는 사유에 싣는 경로를 자른다.
// 사유가 화면을 덮으면 사유가 없는 것과 같아진다(verify.go 의 clipPat 과 같은 이유다).
func clipPath(p string) string {
	const n = 200
	rs := []rune(p)
	if len(rs) <= n {
		return p
	}
	return string(rs[:n]) + "…"
}
