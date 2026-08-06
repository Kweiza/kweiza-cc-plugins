// Package ledger 는 판단 원장(judgment · judgment_link · snapshot)을 JSONL 로 내보내고
// 되읽는다.
//
// ★ 이름이 "backup" 이 아닌 이유: internal/store 에서 backup·BackupSuffix·<db>.bak-* 는
// 이미 마이그레이션 직전 VACUUM INTO 로 뜨는 DB 파일 사본을 뜻한다. 두 개념이 같은 낱말을
// 쓰면 오류 문구와 로그에서 섞인다.
package ledger

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
		"`project`·`session`·`machine` 표 — 무손실 복원의 FK 폐포에 필요하지만 원장 밖이다. " +
			"되읽기는 이 셋이 이미 있는 DB 를 전제한다",
	}
}
