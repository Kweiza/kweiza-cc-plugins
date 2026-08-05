package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
)

// 무시 경로 — 발자국 축에 **겹칠 수 없는 것**이 들어오는 것을 막는다.
//
// PostToolUse 훅이 준 경로가 그대로 footprint 로 간다. 그 안에 git 이 무시하는 스크래치가
// 섞이면 셋이 한꺼번에 망가진다: 표류 처방이 스크래치를 근거로 헛발화하고, board 의
// "지금 만지는 경로"(커밋 전 의도를 나르는 유일한 축)가 오염되고, 무엇보다 두 세션이
// 각자 워크트리에서 같은 이름의 스크래치를 쓰면 경로 성분이 같아 **물리적으로 충돌할 수
// 없는 것**에 겹침이 뜬다.
//
// 대조가 선명하다 — 같은 축의 다른 원천인 UncommittedPaths 는 `git status --porcelain`
// 이라 무시된 파일을 애초에 안 낸다. 한 축에 원천이 둘인데 한쪽만 규칙을 지켰다.
//
// ★ 판정을 **그 경로가 든 트리**에게 묻는다. 주 저장소 한 자리에서 일괄로 물으면 안 된다 —
// 이 레포는 `.git/info/exclude` 에 `.flightdeck/` 이 있어 워크트리 안의 진짜 소스가 전부
// "무시됨"으로 나온다(실측: 무시 판정 13종 중 10종이 DESIGN.md·itempaths.go 같은 추적
// 대상이었다). 그걸 걸렀다면 그 세션들이 겹침 축에서 통째로 사라졌을 것이다.
//
// ★ 경로 문자열을 하드코딩하지 않는다(`.superpowers/` · `.flightdeck/` 목록). 그 목록은
// 반드시 낡고, 낡았다는 사실이 안 보인다. 정본은 git 의 무시 규칙이다.
//
// ★ 여기는 **포함 축이 아니다.** "이 경로가 이 세션의 트리 안인가"는 git 의 무시 규칙이
// 대답할 수 있는 질문이 아니다 — 서브에이전트가 `cp -r` 로 스크래치패드에 뜬 저장소
// 사본은 `.git` 까지 함께 복사되므로 **그 사본 트리에게 물으면 추적 대상**으로 나온다.
// 위 ★ 규율("그 경로가 든 트리에게 묻는다")이 옳게 작동한 결과이지 결함이 아니다.
// 그래서 `/tmp/…/scratchpad/mut/repo/…/hook.go` 가 이 관문을 멀쩡히 통과했다.
// 포함 축은 서버 쪽 service.Beat 가 세운다(service.RelPathWithin) — 여기의 fail-open
// 설계는 그대로 둔다. 지워진 발자국은 아무 데도 안 나타난다.

// GroupPathsByDir 는 경로를 담긴 디렉토리별로 묶는다. 순수 함수다.
//
// 묶는 이유는 프로세스 수다 — check-ignore 는 --stdin 으로 여러 경로를 한 번에 받으므로
// 디렉토리마다 1회면 된다. 실측상 PostToolUse 비트는 702건 전부가 경로 1개였다(100%).
//
// 절대경로가 아니면 안 묶는다. 어느 트리에게 물어야 할지 정할 좌표가 없기 때문이고,
// 그때는 **버리지 않고 그대로 남긴다**(호출부가 그렇게 쓴다).
func GroupPathsByDir(paths []string) map[string][]string {
	out := map[string][]string{}
	for _, p := range paths {
		if strings.TrimSpace(p) == "" || !filepath.IsAbs(p) {
			continue
		}
		d := filepath.Dir(filepath.Clean(p))
		out[d] = append(out[d], p)
	}
	return out
}

// KeepUnignored 는 무시 목록에 **정확히 일치하는** 것만 뺀다. 순수 함수다.
//
// 접두 일치를 하지 않는다. 무시 판정은 git 이 경로마다 내리고 이 함수는 그 답을 그대로
// 쓴다 — 여기서 접두로 확장하면 git 이 "무시 안 한다"고 답한 경로까지 같이 지워진다.
func KeepUnignored(paths []string, ignored map[string]bool) []string {
	if len(ignored) == 0 {
		return paths
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if !ignored[p] {
			out = append(out, p)
		}
	}
	return out
}

// DropIgnoredPaths 는 git 이 무시한다고 **명시한** 경로만 뺀다. log 는 nil 이어도 된다.
//
// ★ 실패 방향이 한쪽으로 고정돼 있다. git 이 없든, 저장소 밖이든, 디렉토리가 사라졌든,
// 판정을 못 하면 그 경로는 **남는다**. 남은 것은 화면에서 사람이 걸러 읽을 수 있지만
// 지워진 발자국은 아무 데도 안 나타난다 — 그 세션이 그 파일을 만진다는 사실이 겹침 축에서
// 통째로 사라지고, 사라졌다는 사실조차 안 보인다. 훅이 전부 fail-open 인 것과 같은 이유다.
func DropIgnoredPaths(log *slog.Logger, paths []string) []string {
	if len(paths) == 0 {
		return paths
	}
	ignored := map[string]bool{}
	for dir, group := range GroupPathsByDir(paths) {
		out, err := gitCheckIgnore(dir, group)
		if err != nil {
			// 판정을 못 했다. 이 묶음은 통째로 남는다.
			if log != nil {
				log.Debug("무시 판정을 못 했다 — 그 경로들은 그대로 둔다",
					"dir", clip(dir, 200), "count", len(group), "error", clip(err.Error(), 200))
			}
			continue
		}
		for _, p := range out {
			ignored[p] = true
		}
	}
	return KeepUnignored(paths, ignored)
}

// gitCheckIgnore 는 dir 이 속한 트리에게 무시 여부를 묻는다.
//
// 종료코드 규약이 셋이다 — 0(하나 이상 맞음) · 1(하나도 안 맞음, **오류가 아니다**) ·
// 그 밖(저장소 밖·디렉토리 없음 등). 1 을 오류로 접으면 "안 맞았다"가 판정 실패로 뭉개져
// 아무것도 안 걸러지고, 반대로 그 밖을 0 으로 접으면 판정 못 한 것을 지우게 된다.
func gitCheckIgnore(dir string, paths []string) ([]string, error) {
	cmd := exec.Command("git", "-C", dir, "check-ignore", "--stdin")
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\n") + "\n")
	var errb strings.Builder
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 1 {
			return nil, fmt.Errorf("git check-ignore(%s): %w: %s", dir, err, clip(errb.String(), 200))
		}
		// 종료코드 1 — 하나도 안 맞았다. 정상이다.
		return nil, nil
	}
	var hits []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimRight(l, "\r"); strings.TrimSpace(l) != "" {
			hits = append(hits, l)
		}
	}
	return hits, nil
}
