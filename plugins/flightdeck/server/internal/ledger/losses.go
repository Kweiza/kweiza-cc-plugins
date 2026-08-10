// Package ledger 는 판단의 FK 폐포(machine · project · session · judgment · judgment_link ·
// snapshot)를 JSONL 로 내보내고 되읽는다.
//
// ★ 이름이 "backup" 이 아닌 이유: internal/store 에서 backup·BackupSuffix·<db>.bak-* 는
// 이미 마이그레이션 직전 VACUUM INTO 로 뜨는 DB 파일 사본을 뜻한다. 두 개념이 같은 낱말을
// 쓰면 오류 문구와 로그에서 섞인다.
package ledger

import (
	"sort"
	"strings"
)

// 링크가 가리키는 대상이 **복원된 DB 에서** 어떻게 되는가.
const (
	linkFateRestored = "복원된다"
	linkFateDangling = "링크는 살고 대상은 없다"
	linkFateNotARow  = "애초에 DB 행이 아니다"
)

// linkKindFate 는 judgment_link.target_kind 별 운명이다.
//
// ★ 이 표를 손으로 정하지 않는다 — 스키마가 정하고 losses_link_test.go 가 규칙 셋으로
// 재계산해 댄다(폐포 안이면 복원 · 폐포 밖 표면 대상 없음 · 표가 아니면 손실 아님).
// 그 관문이 있는 이유: 폐포가 닫히면서 session 이 원장에 들어왔는데 손실 목록은 여전히
// "링크가 가리키는 항목은 복원된 DB 에 없다"를 통째로 말하고 있었다. 실측하면 링크
// 2,805건 중 session 축이 959건(34%)이고 **그 959건은 전부 자기 세션 행을 찾는다.**
// 손실을 실제보다 **크게** 말하는 것도 이 저장소의 손실 열거 규율 위반이다 —
// 다음 사람이 복원 결과를 잘못 예상한다.
var linkKindFate = map[string]string{
	"session": linkFateRestored, // 폐포 안이다
	"item":    linkFateDangling, // 폐포 밖 표
	"job":     linkFateDangling, // 폐포 밖 표
	"commit":  linkFateNotARow,  // git sha 다. 원본 DB 에도 행이 없었으니 잃은 것이 없다
}

// linkFateSentence 는 위 표를 한 문장으로 낸다. 순수 함수다.
//
// 정렬은 결정성을 위한 것이다 — 이 문자열이 매 실행 사용자 화면에 인쇄되므로 회차마다
// 순서가 흔들리면 안 바뀐 산출물이 바뀐 것처럼 보인다.
func linkFateSentence() string {
	byFate := map[string][]string{}
	for k, f := range linkKindFate {
		byFate[f] = append(byFate[f], "`"+k+"`")
	}
	fates := make([]string, 0, len(byFate))
	for f := range byFate {
		fates = append(fates, f)
	}
	sort.Strings(fates)

	parts := make([]string, 0, len(fates))
	for _, f := range fates {
		ks := byFate[f]
		sort.Strings(ks)
		parts = append(parts, strings.Join(ks, "·")+" → "+f)
	}
	return strings.Join(parts, " · ")
}

// Losses 는 이 내보내기가 **덮지 않는 것** 전량이다.
//
// 순수 함수로 두는 이유: 시험이 이 목록을 직접 부르고, 명령이 그대로 출력한다.
// 산문에만 적어 두면 코드가 더 잃기 시작해도 아무도 모른다
// (internal/legacy 의 RoundTripLosses 가 같은 규율의 선례다).
func Losses() []string {
	return []string{
		"아웃박스에 갇힌 판단(pending·rejected) — 이 명령은 DB 만 읽는다. " +
			"서버가 거절해 DB 에 못 들어간 판단은 상태 디렉토리의 JSONL 에만 남는다",
		"`judgment_fts` 와 그림자 표 넷 — judgment_fts_ins 가 AFTER INSERT 트리거라 " +
			"되읽기 때 자동으로 다시 채워진다. 손실 0이다",
		"`rowid` — 복원 후 원본과 달라진다. 안정 식별자는 judgment.id 뿐이고 " +
			"FTS 조인은 트리거가 같은 rowid 로 맞춘다",
		"폐포 밖 표 전부(`item`·`job`·`counter`·`event`·`landing_queue` 등) — 원장은 판단의 FK 폐포 " +
			"여섯 표만 담는다. `judgment_link.target_id` 는 FK 가 아니라(CHECK 만) 링크 자체는 " +
			"**언제나** 복원되고, 그것이 가리키는 대상은 종류마다 갈린다: " + linkFateSentence(),
	}
}
