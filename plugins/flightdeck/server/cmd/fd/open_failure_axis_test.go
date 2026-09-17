package main

import (
	"testing"
)

// TestOpenPrintsFailedAxisNamesNotJustTheCount 는 `fd open` 배선의 본체다.
//
// internal/mcpsrv 의 새 시험 다섯은 전부 렌더러(RenderBoard 등)를 직접 부르는 유닛
// 시험이라, runOpen 안의 RenderFailureAxes 호출을 통째로 지워도 초록이었다 — 오늘
// 사고의 실제 경로가 CLI(`fd open`)였는데 그 배선만 미검증으로 남을 뻔했다.
//
// git 을 못 읽는 워크트리를 FD_WORKTREE 로 갈아 끼운다(wire_test.go 의
// TestMoveSuggestionSurvivesFromFilesystemToScreen 과 같은 수법 — t.TempDir() 는 git
// 저장소가 아니다). service.OpenSession 은 이런 워크트리에서도 세션 등록 자체는
// 살린다(internal/service/session_test.go 의 TestOpenSessionSurvivesGitFailureButSaysSo
// 가 이미 못박은 계약) — 다만 Derived.Failures 에 "refs"·"worktrees" 축이 실린다.
// `fd open` 출력이 그 축 이름을 실제로 화면에 내는지가 이 시험의 전부다.
func TestOpenPrintsFailedAxisNamesNotJustTheCount(t *testing.T) {
	h := newHarness(t)

	notGit := t.TempDir() // git 저장소가 아니다 — rev-parse 가 실패한다
	env := map[string]string{}
	for k, v := range h.env {
		env[k] = v
	}
	env["FD_WORKTREE"] = notGit

	code, out := h.runEnv(env, "", "open", "--label", "축이름배선")
	if code != 0 {
		t.Fatalf("git 을 못 읽는다고 open 이 죽으면 안 된다(%d): %s", code, out)
	}

	mustContain(t, "open stdout", out,
		"못 읽은 파생", // renderFailures 의 머리줄 — 이름 절이 통째로 없으면 이것부터 없다
		"refs",    // service.OpenSession 이 못 읽는 축 이름(session_test.go 와 같은 값)
		"worktrees",
	)
}
