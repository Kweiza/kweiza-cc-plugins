package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
)

// 표시 — **훅 stdout 과 CLI 출력**만 여기 있다.
//
// 보드·추천·판단·마무리의 본문 렌더는 internal/mcpsrv 의 순수 함수를 그대로 쓴다.
// 여기서 다시 쓰면 같은 사실에 두 어휘가 생기고, 그때 어느 쪽이 참인지 말해 주는 자리가 없다.
// 이 파일에 남는 것은 **다른 표면에는 없는 것**뿐이다: SessionStart 의 additionalContext.
//
// ★ 이 문자열들이 소비자 좌표계 그 자체다. 에이전트가 실제로 읽는 것은 구조체가 아니라
// 이 문장이고, 시험은 이 함수의 출력을 단정한다.

// SessionStartInput 은 SessionStart 훅이 낼 additionalContext 의 재료다.
type SessionStartInput struct {
	Now        time.Time
	Banner     string // 서버 상태 배너. 미도달이면 L1 문안, 도달이면 스큐 배너(없으면 빈 문자열)
	ServerURL  string
	SessionID  string
	Created    bool
	Project    string
	Worktree   string
	Claims     []string
	Board      string // mcpsrv.RenderBoard 결과. 캐시라면 그 사실은 Banner 가 나른다
	BoardStale bool
	Asks       []model.Judgment
	Blocked    []model.Judgment
	Pending    int    // 아웃박스에 남아 있는 판단 수 — 고정 자리 + 옛 채널 자리 합계(hookSessionStart 참고)
	Notice     string // 도구가 스스로 못 한 것(예: machine-id 를 못 적었다)
}

// RenderSessionStart 는 SessionStart 훅이 stdout 으로 내는 additionalContext 본문이다. 순수 함수다.
//
// ★ 배너가 **맨 앞**이다. 조용히 두면 에이전트가 조정 기구가 있는 줄 알고 움직이고,
// 그것이 L1 에서 가장 비싼 실패다(설계 §7).
// ★ 선점이 없다는 사실도 **문장으로** 낸다. 빈 줄로 두면 "안 쥐었다"와 "이 축을 안 봤다"가
// 구분되지 않는다.
func RenderSessionStart(in SessionStartInput) string {
	var b strings.Builder
	if in.Banner != "" {
		b.WriteString(in.Banner)
		b.WriteString("\n\n")
	}
	if in.SessionID != "" {
		verb := "재개"
		if in.Created {
			verb = "신규"
		}
		fmt.Fprintf(&b, "flightdeck 세션 %s(%s) · 프로젝트 %s · %s\n",
			verb, in.SessionID, in.Project, clip(in.Worktree, 160))
	} else {
		fmt.Fprintf(&b, "flightdeck 세션 미등록 — 서버에 닿으면 등록된다(%s)\n", clip(in.ServerURL, 120))
	}
	if len(in.Claims) > 0 {
		fmt.Fprintf(&b, "내 선점: %s — 이 세션은 이미 이것을 쥐고 있다\n", strings.Join(in.Claims, " "))
	} else {
		b.WriteString("내 선점: 없음 — 무엇을 집을지는 pick 이 고른다\n")
	}
	if in.Pending > 0 {
		fmt.Fprintf(&b, "아직 못 보낸 판단 %d건이 이 머신에 쌓여 있다 — 서버가 살아나면 자동 재생된다\n", in.Pending)
	}
	if in.Notice != "" {
		fmt.Fprintf(&b, "! %s\n", clip(in.Notice, 400))
	}
	if in.Board != "" {
		b.WriteString("\n")
		if in.BoardStale {
			b.WriteString("아래 보드는 **캐시**다(위 배너의 시각 기준).\n")
		}
		b.WriteString(in.Board)
		b.WriteString("\n")
	}
	if n := len(in.Asks) + len(in.Blocked); n > 0 {
		fmt.Fprintf(&b, "\n■ 미확인 %d건 — 남이 남긴 요청·막힘이다\n", n)
		for _, j := range in.Asks {
			fmt.Fprintf(&b, "  [ask] %s\n", clip(firstLine(j.Title, j.Body), 200))
		}
		for _, j := range in.Blocked {
			fmt.Fprintf(&b, "  [blocked] %s\n", clip(firstLine(j.Title, j.Body), 200))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// RenderHealth 는 `fd doctor` 의 서버 절이다. 순수 함수다.
//
// ★ **못 쟀다와 0 을 가른다.** disk_known=false 인데 0.0% 를 찍으면
// "가득 찼다"는 값이 되고, 그 순간 이 줄의 판별력이 사라진다.
func RenderHealth(h healthzResponse, reachable bool, url string) string {
	if !reachable {
		return fmt.Sprintf("■ 서버(%s) — 미도달", clip(url, 120))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "■ 서버(%s) — ok=%v · api=%s · db=%v",
		clip(url, 120), h.OK, h.APIVersion, h.DBOK)
	if h.DBError != "" {
		fmt.Fprintf(&b, "\n    DB 오류: %s", clip(h.DBError, 300))
	}
	if h.DiskKnown {
		fmt.Fprintf(&b, "\n    디스크 여유 %.1f%%", h.DiskFreePct)
	} else {
		fmt.Fprintf(&b, "\n    디스크 여유 **못 쟀다**(0%% 가 아니다): %s", clip(h.DiskError, 200))
	}
	fmt.Fprintf(&b, "\n    인증: 토큰 설정 %v · 루프백 개방 %v", h.Auth.TokenSet, h.Auth.LoopbackOpen)
	if h.Auth.Notice != "" {
		fmt.Fprintf(&b, "\n    %s", clip(h.Auth.Notice, 300))
	}
	if s := SkewBanner(clientAPIVersion, h.APIVersion); s != "" {
		fmt.Fprintf(&b, "\n    %s", s)
	}
	return b.String()
}

// PrescriptionLine 은 낼 처방 하나다.
//
// 태그가 서버 응답(internal/judge.Prescription 의 json 태그)과 맞아야 한다 —
// 어긋나면 처방이 조용히 빈 목록이 된다. hook_stop_test.go 의 통합 시험이 그 축을 잡는다.
type PrescriptionLine struct {
	Key  string `json:"key"`
	Text string `json:"text"`
}

// RenderPrescriptions 는 훅 stdout 에 실을 문구다. 순수 함수다.
//
// ★ 0건이면 **빈 문자열이다.** 빈 머리글을 매 턴 내면 컨텍스트를 먹고,
// 그러면 세션이 이 채널 자체를 읽지 않게 된다 — 설계 §4 가 고발한 상시 점등의 다른 얼굴이다.
func RenderPrescriptions(shown []PrescriptionLine, folded int) string {
	if len(shown) == 0 && folded == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "flightdeck 처방 %d건 — 지금 남기지 않으면 남의 화면에 안 뜬다\n", len(shown)+folded)
	for _, p := range shown {
		fmt.Fprintf(&b, "  %s\n", strings.ReplaceAll(p.Text, "\n", "\n  "))
	}
	if folded > 0 {
		fmt.Fprintf(&b, "  … %d건을 접었다. 접힌 것도 이미 발화된 것이라 다시 안 뜬다\n", folded)
	}
	return strings.TrimRight(b.String(), "\n")
}

func firstLine(title, body string) string {
	if t := strings.TrimSpace(title); t != "" {
		return t
	}
	for _, l := range strings.Split(body, "\n") {
		if s := strings.TrimSpace(l); s != "" {
			return s
		}
	}
	return "(본문 없음)"
}
