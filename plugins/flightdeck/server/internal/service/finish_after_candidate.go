package service

import (
	"context"

	"github.com/kweiza/flightdeck/internal/judge"
)

// afterCandidateAxis 는 이 축이 파생 사유에 쓰는 이름이다. 한 자리에만 둔다 —
// 화면과 시험이 같은 문자열을 봐야 "왜 후보가 안 나왔나"를 되짚을 수 있다.
const afterCandidateAxis = "after-candidate"

// afterCandidate 는 이번에 만든 후속이 선행(`dep_sha`)으로 걸 수 있는 sha 다.
// 낼 수 없으면 빈 문자열이고, **왜 못 냈는지는 반드시 사유로 남긴다.**
//
// ─── 왜 이 함수가 있나 ──────────────────────────────────────────────────────
//
// `landed_ref` 는 Tier A 에서 영영 NULL 이다. 설계 §3 이 그렇게 정했다 — "러너가 실제로
// fast-forward 한 sha 만 들어간다. 기존 도구가 **'메인 트리의 지금 HEAD'를 적어 남의
// 커밋이 박히던 결함(3회 관측)**이 여기서 소멸한다." Tier A 에는 러너가 없으므로 채울
// 값이 없는 것이 맞다.
//
// **그 대가가 이 함수의 존재 이유다.** 후속을 쓰는 사람이 걸어야 할 sha 를 어디서도 못
// 얻는다. 실측된 사고: `fd-fingerprint-inputs-drift-between-two-build-sites` 의 전제가
// 3일간 미랜딩이었는데 큐 어디에도 그 사실이 없었고, 그래서 선행이 안 걸렸고,
// `pick` 이 그것을 기아 78h **1순위**로 냈다. 집은 세션은 코드를 열고서야 "본문이 말하는
// 두 목록이 main 에 없다"를 발견했다. 배제 축(`after-unmet-sha`)은 멀쩡히 살아 있었다 —
// 병목은 축이 아니라 **그 축에 넣을 값이 안 나온다**는 것이었다.
//
// ─── 왜 브랜치 head 인가(기본 브랜치 tip 이 아니라) ─────────────────────────
//
// §3 이 없앤 결함이 정확히 "지금 tip 을 적는 것"이다. 브랜치 head 는 **이 작업 자신**이라
// 그 결함을 원리적으로 안 밟는다. 뜻도 더 정확하다 — "내 작업이 랜딩된 뒤"이지 "그 시각에
// 우연히 tip 이던 남의 커밋 뒤"가 아니다.
//
// 그리고 이 값은 `landed_ref` 가 **아니다.** 저쪽은 "이 항목이 어디에 랜딩됐나"라는
// 사실 주장이라 틀리면 거짓 기록이 되지만, `dep_sha` 는 "이 sha 가 조상이면 진행하라"는
// 조건이라 보수적으로 틀려도 **더 엄격해질 뿐**이다. 두 칸의 무게가 다르다.
//
// ─── 왜 조상일 때만 내나 ────────────────────────────────────────────────────
//
// 랜딩 전에 내면 후속이 그 sha 를 걸어도 `pick` 이 즉시 충족으로 읽어 아무것도 안
// 기다린다. 선행이 걸린 것처럼 보이는데 실제로는 안 걸린 상태 — 안 내느니 나쁘다.
//
// ─── git reads 를 안 올리는 이유 ────────────────────────────────────────────
//
// 실패는 `d` 에 사유로 옮기지만 성공한 읽기는 **세지 않는다.** `FreshnessOf` 는
// reads>0 이면 신선도를 `Source="git"` 으로 올리는데, 이 응답의 주 값(항목·판단·후속·
// 수지)은 전부 DB 에서 온다. 부가 한 줄 때문에 reads 를 올리면 화면이 **DB 값들을 git
// 관측인 것처럼** 말하게 된다.
func (s *Service) afterCandidate(ctx context.Context, project, sessionID string, followups int, d *derive) string {
	// 후속이 없으면 걸 자리도 없다. git 을 한 번도 안 부른다 — 마무리의 흔한 경로가
	// 이쪽이고, 여기에 git 호출을 얹으면 모든 finish 가 그 비용을 낸다.
	if followups == 0 {
		return ""
	}

	sess, err := s.st.GetSession(ctx, sessionID)
	if err != nil {
		d.fail(afterCandidateAxis, err)
		return ""
	}
	if sess.Worktree == "" {
		d.note(afterCandidateAxis, "이 세션에 워크트리 좌표가 없다 — 어느 브랜치의 head 인지 물을 자리가 없다")
		return ""
	}

	proj, err := s.st.GetProject(ctx, project)
	if err != nil {
		d.fail(afterCandidateAxis, err)
		return ""
	}

	// ★ 별도 derive 로 받아 **사유만** 옮긴다. 위 「git reads」 절이 이유다.
	gd := &derive{}
	branch, head := s.worktreeFacts(ctx, s.git(proj.Path), sess.Worktree, gd)
	for _, f := range gd.failures {
		d.note(afterCandidateAxis, f.Axis+": "+f.Detail)
	}

	// ★ **branch 가 비면 폴백이다 — 그 sha 를 쓰면 안 된다.** worktreeFacts 는 워크트리
	// 목록에서 경로를 못 찾으면 `Ref(ctx, "HEAD")` 로 sha 만 건지는데, 그 HEAD 는 저장소
	// 경로(= 메인 트리)의 것이지 이 세션 브랜치의 것이 아니다. 그것을 후보로 내면 이
	// 함수가 피하려던 바로 그 결함("메인 트리의 지금 HEAD")을 되살린다.
	if branch == "" || head == "" {
		d.note(afterCandidateAxis,
			"이 세션의 브랜치 head 를 못 짚었다(워크트리 목록에 경로가 없거나 목록 자체가 실패했다) — "+
				"메인 트리의 HEAD 로 대신하지 않는다")
		return ""
	}

	anc, err := s.git(proj.Path).Ancestry(ctx, head, proj.DefaultBranch)
	if err != nil {
		d.fail(afterCandidateAxis, err)
		return ""
	}
	switch anc {
	case judge.AncestryYes:
		return head
	case judge.AncestryNo:
		d.note(afterCandidateAxis,
			"브랜치 "+clip(branch, 120)+" 가 아직 "+clip(proj.DefaultBranch, 60)+" 의 조상이 아니다 — "+
				"랜딩 전이라 선행 후보를 안 낸다(내면 후속이 즉시 충족으로 읽혀 아무것도 안 기다린다)")
	case judge.AncestryBadRef:
		d.note(afterCandidateAxis,
			"기본 브랜치 "+clip(proj.DefaultBranch, 60)+" 나 head "+clip(head, 40)+" 를 git 이 모른다(rc=128) — "+
				"기다려도 안 풀린다. 프로젝트의 default_branch 를 확인해라")
	default:
		d.note(afterCandidateAxis, "조상 판정이 "+anc.String()+" 다 — 조회가 안 돌았다")
	}
	return ""
}
