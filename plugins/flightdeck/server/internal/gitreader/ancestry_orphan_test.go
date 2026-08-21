package gitreader

import (
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/judge"
)

// 재생·유실된 커밋을 "아직 조상이 아니다"와 가른다 (2026-08-21).
//
// ★ 이 저장소의 랜딩은 커밋을 **재생한다**. 브랜치가 머지되면서 새 sha 가 서고 원래 sha 는
// 어느 브랜치에도 안 남는다 — 그러면 `merge-base --is-ancestor` 가 영원히 rc=1 이다.
// 내용은 들어갔는데 판정은 영원히 "아직"이라, 그 sha 를 선행으로 건 항목이 영구히 굶는다.
//
// 실측(2026-08-21, kweiza-cc-plugins): after-unmet-sha 로 막힌 **열린 항목 셋**이 전부 이 경로였고
// 셋 다 티클러였다. 세 dep_sha 는 `git cat-file -e` 는 통과하고 `git branch -a --contains` 는
// 비어 있었으며, 내용은 다른 sha 로 main 에 들어가 있었다(6ef65db→a419130 · e776805→7a18807 ·
// a6c68b4→6903562).
//
// ★ 판정을 **rc=1 일 때만** 덧댄다. rc=0(조상이다)에는 물을 것이 없고, rc=128 은 이미 갈려 있다.
func TestAncestryTellsOrphanFromNotYet(t *testing.T) {
	ctx := ctxT(t)
	repo := newRepo(t)
	write(t, repo, "a.txt", "a\n")
	base := commit(t, repo, "첫 커밋")

	// ── 살아 있는 브랜치 위의 커밋: 아직 조상이 아니다(기다리면 풀린다)
	runGit(t, repo, "checkout", "-q", "-b", "alive")
	write(t, repo, "b.txt", "b\n")
	onBranch := commit(t, repo, "살아 있는 브랜치의 커밋")
	runGit(t, repo, "checkout", "-q", "main")

	// ── 브랜치를 지운 커밋: 커밋은 남지만 어느 브랜치에도 없다(재생·유실과 같은 모양)
	runGit(t, repo, "checkout", "-q", "-b", "doomed")
	write(t, repo, "c.txt", "c\n")
	orphan := commit(t, repo, "곧 브랜치가 지워질 커밋")
	runGit(t, repo, "checkout", "-q", "main")
	runGit(t, repo, "branch", "-q", "-D", "doomed")

	r := New(repo)

	// 대조 전제 — 둘 다 커밋으로 실재해야 이 시험이 성립한다.
	if out := strings.TrimSpace(runGit(t, repo, "cat-file", "-t", orphan)); out != "commit" {
		t.Fatalf("대조 전제가 깨졌다 — 고아 sha 가 커밋이 아니다(%q)", out)
	}
	if got, err := r.Ancestry(ctx, base, "main"); err != nil || got != judge.AncestryYes {
		t.Fatalf("대조 전제가 깨졌다 — 조상인 sha 가 %v(%v) 다", got, err)
	}

	got, err := r.Ancestry(ctx, orphan, "main")
	if err != nil {
		t.Fatalf("고아 sha 판정에서 오류: %v", err)
	}
	if got != judge.AncestryOrphan {
		t.Fatalf("어느 브랜치에도 없는 커밋이 %v 로 나왔다 — %v 여야 한다.\n"+
			"'아직'으로 접으면 랜딩이 재생한 sha 를 건 항목이 영구히 굶는다(실측 3건)", got, judge.AncestryOrphan)
	}

	got, err = r.Ancestry(ctx, onBranch, "main")
	if err != nil {
		t.Fatalf("살아 있는 브랜치 sha 판정에서 오류: %v", err)
	}
	if got != judge.AncestryNo {
		t.Fatalf("살아 있는 브랜치 위의 커밋이 %v 로 나왔다 — %v 여야 한다.\n"+
			"이 갈래는 기다리면 실제로 풀린다. 덮으면 정상 대기가 '영영 안 풀린다'로 오진된다", got, judge.AncestryNo)
	}
}
