package main

import "strings"

// deletedSuffix 는 리눅스가 `/proc/<pid>/exe` 에 붙이는 표식이다.
//
// 프로세스가 연 파일이 그 뒤 교체·삭제되면 커널이 심볼릭 링크를 이렇게 낸다.
// `os.Executable` 이 그 링크를 읽으므로 이 표식이 그대로 올라온다.
const deletedSuffix = " (deleted)"

// ExeLines 는 지금 도는 실행 파일에 대해 doctor 가 찍을 줄들이다. 순수 함수다.
//
// ★ **파일이 아니라 프로세스를 찍는다.** 2026-08-05 실측: 설치 자리의 파일은 8분 전
// 최신으로 교체돼 있었는데(`vcs.revision` = 그 시각의 main tip) 그 자리를 도는 프로세스는
// 14시간 전 코드였다 — `/proc/<pid>/exe -> …/fd (deleted)`. 파일을 재는 진단은 그 상황에서
// "최신"이라 답하고, 응답하는 코드는 옛 것이다.
//
// 그래서 이 줄이 없으면 "판을 올렸는데 왜 그대로냐"에 답할 자리가 없다. 답은 **재기동**이고,
// 그 사실이 여기 없으면 사람은 빌드를 다시 하러 간다(그래 봐야 파일만 또 바뀐다).
//
// os.Executable 을 안에서 부르지 않고 그 반환을 **인자로 받는다** — 시험이 두 갈래를
// 다 줄 수 있어야 한다. 실제 프로세스로는 교체된 갈래를 만들 수 없다.
func ExeLines(exe string, err error) []string {
	if err != nil {
		return []string{"! 실행 파일 자리를 못 읽었다: " + clip(err.Error(), 200)}
	}
	if path, ok := strings.CutSuffix(exe, deletedSuffix); ok {
		return []string{
			"실행 파일 " + path,
			"! 그 자리의 파일은 **교체됐다** — 이 프로세스는 옛 코드를 계속 돈다. " +
				"새 판으로 돌리려면 재기동해야 한다(빌드를 다시 해도 안 바뀐다).",
		}
	}
	return []string{"실행 파일 " + exe}
}
