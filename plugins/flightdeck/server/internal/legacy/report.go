package legacy

import (
	"fmt"
	"io"
	"strings"
)

// RenderPlan 은 dry-run 출력이다. **이것이 이 도구의 소비자 좌표계다** —
// 시험은 이 문자열을 단정하고, 사람이 착수 여부를 정할 때 보는 것도 이 문자열이다.
//
// 출력은 둘이다: **건수 대조표**와 **해석 실패 전량 목록**.
// 요약만 내면 "몇 건 실패"라는 숫자가 남고, 무엇이 왜 빠졌는지는 영영 알 수 없다 —
// 이 레포는 이미 두 번 원문을 영구 소실했고 두 번 다 그 사실이 나중에야 드러났다.
func RenderPlan(w io.Writer, p ImportPlan, applied bool) {
	mode := "예행(--dry-run) — DB 를 한 바이트도 건드리지 않았다"
	if applied {
		mode = "적용(--apply) — 아래대로 DB 에 넣었다"
	}
	fmt.Fprintf(w, "fd import · 프로젝트 %s\n%s\n\n", p.Project, mode)

	fmt.Fprintln(w, "── 건수 대조")
	fmt.Fprintf(w, "  %-16s %8s %12s %8s %8s\n", "원본", "발견", "바이트", "넣음", "거절")
	for _, c := range p.Counts {
		fmt.Fprintf(w, "  %-16s %8d %12d %8d %8d\n", c.Source, c.Found, c.Bytes, c.Accept, c.Reject)
	}
	fmt.Fprintf(w, "  %-16s %8s %12s %8s %8s\n", "", "", "", "", "")
	fmt.Fprintf(w, "  판단 행: 핸드오프 %d + 세션 절 %d + 랜딩 서사 %d\n",
		len(p.Handoffs), countSections(p), len(p.Landings))
	fmt.Fprintf(w, "  스냅숏 %d · 대시보드에서 온 항목 %d(이슈 %d · 막힘 %d)\n\n",
		len(p.Parts), len(p.Issues)+len(p.Blockers), len(p.Issues), len(p.Blockers))

	// ── 해석 실패 전량. 하나도 접지 않는다.
	fatal, soft := splitFatal(p.Rejects)
	fmt.Fprintf(w, "── 거절 · 통째로 안 넣는 것 (%d건)\n", len(fatal))
	if len(fatal) == 0 {
		fmt.Fprintln(w, "  (없음)")
	}
	for _, r := range fatal {
		fmt.Fprintf(w, "  ✗ [%s] %s%s\n      %s\n", r.Code, r.Path, fieldSuffix(r.Field), r.Detail)
	}
	fmt.Fprintf(w, "\n── 거절 · 그 필드만 못 옮긴 것 (%d건)\n", len(soft))
	if len(soft) == 0 {
		fmt.Fprintln(w, "  (없음)")
	}
	for _, r := range soft {
		fmt.Fprintf(w, "  · [%s] %s%s\n      %s\n", r.Code, r.Path, fieldSuffix(r.Field), r.Detail)
	}

	fmt.Fprintf(w, "\n── 끊긴 포인터 · FK 로 못 옮긴 것 (%d건)\n", len(p.Gone))
	if len(p.Gone) == 0 {
		fmt.Fprintln(w, "  (없음)")
	}
	for _, g := range p.Gone {
		fmt.Fprintf(w, "  gone [%s] %s → %s\n      %s\n", g.Kind, g.From, g.Target, g.Detail)
	}

	fmt.Fprintf(w, "\n── 비규약 절 · 보존하되 분류하지 못한 것 (%d건)\n", len(p.Unclassified))
	if len(p.Unclassified) == 0 {
		fmt.Fprintln(w, "  (없음)")
	}
	for _, s := range p.Unclassified {
		fmt.Fprintf(w, "  보존 %s · `## %s`\n", s.File, s.Name)
	}

	if len(p.SkippedAxes) > 0 {
		fmt.Fprintf(w, "\n── 일부러 안 옮기는 축 (%d건 — 파생이라 손 기재를 되들이지 않는다)\n", len(p.SkippedAxes))
		for _, s := range p.SkippedAxes {
			fmt.Fprintf(w, "  건너뜀 %s\n", s)
		}
	}

	if len(p.Notes) > 0 {
		fmt.Fprintf(w, "\n── 원본과 다르게 넣는 것 (%d건)\n", len(p.Notes))
		for _, n := range p.Notes {
			fmt.Fprintf(w, "  ! %s\n", n)
		}
	}

	fmt.Fprintln(w, "\n── 되돌리기")
	fmt.Fprintln(w, "  원본은 읽기만 했다. 되돌리기는 DB 파일 삭제 + 재실행이다.")
	fmt.Fprintln(w, "  옛 형식으로 되쓰려면 `fd export --to-legacy --out <디렉토리>`.")
	if !applied {
		fmt.Fprintln(w, "\n실제로 넣으려면 --apply 를 붙여라. 기본값은 예행이다.")
	}
}

func countSections(p ImportPlan) int {
	n := 0
	for _, s := range p.Sessions {
		n += len(s.Sections)
	}
	return n
}

func splitFatal(rs []Reject) (fatal, soft []Reject) {
	for _, r := range rs {
		if r.Fatal {
			fatal = append(fatal, r)
		} else {
			soft = append(soft, r)
		}
	}
	return fatal, soft
}

func fieldSuffix(f string) string {
	if strings.TrimSpace(f) == "" {
		return ""
	}
	return " · " + f
}
