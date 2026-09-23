// Package plugintests 는 이 마켓플레이스 저장소의 플러그인들을 무는 시험 모듈이다.
//
// 플러그인들(grafik-bar · session-handoff)에는 Go 코드가 없다 — 훅 배선, 셸 스크립트,
// 스킬 문서뿐이다. 그것들이 조용히 죽는 모양(경로 오타, 모르는 이벤트 이름, 깨진
// frontmatter, 상태줄이 안 뜨는 스크립트)을 이 모듈의 시험이 문다. 돌리는 법:
//
//	cd tests && go test ./...
//
// 표준 라이브러리만 쓴다. 상태줄·setup 시험은 bash 와 jq 를 부르고, 없으면 그 사실을
// 밝히며 해당 축만 건너뛴다.
package plugintests
