package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/buildinfo"
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

	// Unreadable 은 **세다 걸린** 큐다. 각 원소는 "<자리>: <사유>".
	//
	// ★ Pending 과 갈라 둔 이유는 하나다 — **0 과 '못 쟀다'를 가른다.** 셀 수 없는 큐는
	// Pending 에 0 으로 들어가는데(Leftover.Pending 이 0값으로 남는다), 그러면 배너에서
	// "아무것도 안 쌓였다"와 구별이 안 되고 0 은 '깨끗하다'로 읽힌다. RenderHealth 가
	// disk_known 에 대해 이미 같은 규율을 쓴다.
	Unreadable []string
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
	// ★ 셀 수 없었던 큐는 **위 건수와 따로** 낸다. 합치면 0 에 묻히고, 묻히면 침묵이다.
	//
	// ★ **"막혀 있다"고 단정하지 마라.** Leftover.Err 은 대기열 읽기 실패와 격리 파일 읽기
	// 실패를 **한 칸에 담는다**(outbox.go 의 leftover). 그래서 격리만 손상된 큐도 여기 오는데,
	// 그 큐의 대기열은 멀쩡히 세어져 위 줄이 "자동 재생된다"를 이미 말한 뒤다 — 단정하면
	// 배너가 두 줄 사이에서 스스로를 반박한다. 아는 것만 말하고 판정은 doctor 에 넘긴다.
	for _, u := range in.Unreadable {
		fmt.Fprintf(&b, "아웃박스를 못 셌다 — %s\n"+
			"  이 자리는 재생이나 적재가 막혀 있을 수 있다. `fd doctor` 가 어느 파일인지 찍는다\n",
			clip(u, 300))
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
	// ★ 서버가 도는 판. **부재도 찍는다** — 이 축을 안 내는 서버라는 사실 자체가
	// "판이 이 축을 알리기 전만큼 낡았다"는 신호이고, 침묵하면 그 신호가 사라진다.
	fmt.Fprintf(&b, "\n    서버 판 %s", buildinfo.Short(h.Build))
	for _, line := range selfUpdateLines(h) {
		fmt.Fprintf(&b, "\n    %s", line)
	}
	// ★ 설정과 관측을 **따로** 찍는다. 한 값으로 접으면 "면제를 껐다"(의도한 상태)와
	// "면제는 켰는데 아무도 못 받는다"(배선 결함)가 화면에서 같아지는데, 처방이 정반대다.
	// 앞선 판은 `루프백 개방` 한 값만 냈고 그 값이 설정이었다 — 컨테이너 배포에서
	// 그 줄이 참인 설정을 말하며 거짓인 결론을 읽게 했다.
	fmt.Fprintf(&b, "\n    인증: 토큰 설정 %v · 루프백 면제 설정 %v · 루프백 도달 %v",
		h.Auth.TokenSet, h.Auth.LoopbackConfigured, h.Auth.LoopbackOpen)
	if h.Auth.Notice != "" {
		fmt.Fprintf(&b, "\n    %s", clip(h.Auth.Notice, 300))
	}
	if s := SkewBanner(clientAPIVersion, h.APIVersion); s != "" {
		fmt.Fprintf(&b, "\n    %s", s)
	}
	// 스큐 배너 **다음**에, 따로 낸다. 계약 버전이 같아도 판 나이는 갈릴 수 있고
	// 그 구간이 정확히 이 줄이 없어 침묵했던 자리다.
	if v := buildinfo.VintageBanner(buildinfo.Self(), h.Build); v != "" {
		fmt.Fprintf(&b, "\n    %s", v)
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
		// ★ 문구가 사실을 따라 두 번 바뀌었다. 첫 판은 "접힌 것도 이미 발화된 것이라 다시
		// 안 뜬다"였고 실제로 그랬다 — 그래서 접힌 처방은 세션이 **한 번도 못 본 채** 사라졌다.
		// 2026-08-06 이 그것을 "같은 조건이면 다시 뜬다"로 고쳤는데, 그 "조건"이 **그 경로를
		// 다시 만지는 것**이라 다발이 끝나면 여전히 안 돌아왔다. 2026-08-09 에 접힌 턴이 자기
		// 창을 물려주게 되면서 조건이 사라졌다 — 이제 **다음 턴**이다.
		//
		// 세션이 읽는 것은 이 한 줄이라 조건을 적으면 안 된다: "같은 조건이면"을 남겨 두면
		// 세션은 밀린 처방을 보려고 안 해도 될 일을 한다.
		fmt.Fprintf(&b, "  … %d건을 접었다. 접힌 것은 발화로 안 세므로 다음 턴에 올라온다\n", folded)
	}
	return strings.TrimRight(b.String(), "\n")
}

// selfUpdateLines 는 자동 갱신 축을 사람이 읽을 줄로 옮긴다. 순수 함수다.
//
// ★ **안 보고 있다는 사실을 침묵으로 두지 않는다.** 컨테이너·비유닉스·감시기 기동 실패는
// 전부 "이 서버는 자기를 안 따라간다"이고, 그 상태에서 아무 줄도 안 내면
// 읽는 쪽은 따라오고 있다고 믿는다(설계 §13).
func selfUpdateLines(h healthzResponse) []string {
	su := h.SelfUpdate
	if !su.Watching {
		reason := strings.TrimSpace(su.Reason)
		if reason == "" {
			reason = "사유를 안 냈다 — 이 축을 알리기 전 판일 수 있다"
		}
		return []string{"자동 갱신  **안 본다** — " + clip(reason, 300)}
	}
	var lines []string
	// ★ **막힌 것을 "보는 중"으로 찍지 않는다.** 감시기는 켜져 있는데 실행 파일을 못 재는
	// 상태(삭제·권한·마운트 소실)는 영원히 이어질 수 있고, 그때 "보는 중 — 아직 교체를
	// 못 봤다"는 정반대의 안심을 준다. 서버는 옛 코드로 계속 산다.
	if s := strings.TrimSpace(su.Stalled); s != "" {
		lines = append(lines, "자동 갱신  **막혔다** — "+clip(s, 300))
	}
	// ★ **못 덮는 갈래를 "보는 중"으로 접지 않는다.** 이름에 소스 지문이 박힌 자리를 감시하는
	// 프로세스는 플러그인 버전이 오르는 갱신을 영영 못 본다 — 그 사실을 안 말하면 화면이
	// "따라오고 있다"고 거짓말한다(설계 §13). **막혔다**와 다른 문구인 것은 처방이 다르기
	// 때문이다: 저쪽은 못 재는 원인을 고치는 것이고, 이쪽은 고칠 것이 없고 사람이 재기동한다.
	if s := strings.TrimSpace(su.Uncovered); s != "" {
		lines = append(lines, "자동 갱신  **한 갈래를 못 덮는다** — "+clip(s, 300))
	}
	if su.Outcome == "" {
		if len(lines) == 0 {
			lines = append(lines, "자동 갱신  보는 중 — 아직 교체를 못 봤다")
		}
		return lines
	}
	// ★ "failed" 는 **/healthz 로는 못 온다** — 그 값은 drain() 뒤에만 쓰이고, 그때
	// 리스너는 이미 닫혔으며 프로세스는 곧 비0으로 죽는다(serve.go 가 그것을 읽는다).
	// 그래도 갈래를 남기는 것은 구조상 방어다: 이 함수는 순수 함수라 값이 어디서 오든
	// 이름을 말할 수 있어야 하고, 없으면 다음 사람이 "왜 이 값엔 시험이 없나"를 뒤진다.
	label := map[string]string{"refused": "**거절**", "failed": "**실패**"}[su.Outcome]
	if label == "" {
		label = clip(su.Outcome, 40)
	}
	head := "자동 갱신  " + label
	if strings.TrimSpace(su.LastAt) != "" {
		head += " (" + clip(su.LastAt, 40) + ")"
	}
	lines = append(lines, head)
	// ★ 화살표를 매달아 두지 않는다. 거절 경로 중 To 가 빈 채로 오는 것이 알려진 한계라
	// (Task 4 지연 항목), From·To 중 하나만 비어도 "07e5df4 → " 처럼 끝을 침묵으로
	// 남기면 부재가 안 보인다 — 빈 쪽을 "(미상)"으로 채워 그 자리를 말로 남긴다.
	if su.From != "" || su.To != "" {
		from, to := su.From, su.To
		if from == "" {
			from = "(미상)"
		}
		if to == "" {
			to = "(미상)"
		}
		lines = append(lines, "  "+clip(from, 80)+" → "+clip(to, 80))
	}
	if strings.TrimSpace(su.Detail) != "" {
		lines = append(lines, "  "+clip(su.Detail, 400))
	}
	return lines
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
