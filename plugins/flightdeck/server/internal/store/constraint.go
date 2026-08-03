package store

import (
	"errors"
	"fmt"

	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// 제약 위반을 **타입 있는 오류**로 옮기는 자리.
//
// ★ 왜 필요한가: 스키마의 제약이 이 레포의 방어선이라(schema.sql 주석 참조) 위반은
// 드물지 않다 — 항목 id 오타·재등록, 등록 안 된 프로젝트로 판단 쓰기가 전부 여기로 온다.
// 그런데 위반이 그냥 드라이버 오류로 올라가면 표면의 ClassifyError 가 그것을 아는 타입으로
// 못 가려 **500 으로 내보낸다**. 그러면 셋이 한꺼번에 나빠진다:
//
//	① 에이전트와 사람이 "서버가 고장났다"로 읽고 조사를 엉뚱한 데로 돌린다
//	② 500 은 멱등 표에 저장되지 않는 등급이라 재시도가 계속 하류로 들어간다
//	③ 응답에 고칠 거리가 없다 — 무엇이 중복인지, 무엇이 등록 안 됐는지를 못 말한다
//
// ★ 판별을 **드라이버 문구의 문자열 매칭으로 하지 않는다.** 그 문구는 판올림마다
// 조용히 바뀌고, 바뀌는 날 이 계층은 아무 소리 없이 다시 500 을 내기 시작한다.
// 대신 드라이버가 실제로 노출하는 것에 기댄다 — modernc.org/sqlite 는 확장 결과 코드를
// 켜고(conn.extendedResultCodes(true)) 오류를 *sqlite.Error 로 올리며 Code() 로 그 값을 준다.
// 실물로 확인한 값: PK 중복 1555 · UNIQUE 2067 · FK 787 · CHECK 275 · 트리거 ABORT 1811.
//
// 판정 자체는 순수 함수(JudgeConstraintCode)에 있고 시험이 그 함수를 직접 부른다.
// 다중 조건이라 불리언이 아니라 **사유**를 돌려준다 — 사유가 없으면 "접지 않기로 했다"와
// "이 축을 아예 안 본다"가 구분되지 않는다.

// ConflictKind 는 제약 위반의 종류다. **처방이 다르므로 뭉개지 않는다.**
type ConflictKind string

const (
	// ConflictDuplicate 는 같은 좌표가 이미 있다는 뜻이다(PK·UNIQUE).
	// 처방: 다른 id 를 쓰거나 이미 있는 것을 써라.
	ConflictDuplicate ConflictKind = "duplicate"

	// ConflictMissingRef 는 가리키는 좌표가 없다는 뜻이다(FK).
	// 처방: 가리킨 것을 먼저 등록해라. 중복과 정반대의 처방이라 같은 값에 담으면 안 된다.
	ConflictMissingRef ConflictKind = "missing_ref"
)

// ConflictTarget 은 어느 표의 제약이 걸렸나다. **기계가 읽는 값이다** —
// 사람이 읽을 이름과 처방은 표면 계층(api)이 이 값으로 조립한다.
// 저장 계층에 한국어 응답 문구를 두면 같은 문구가 표면마다 두 벌이 된다.
type ConflictTarget string

const (
	TargetItem             ConflictTarget = "item"
	TargetItemAfter        ConflictTarget = "item_after"
	TargetClaim            ConflictTarget = "claim"
	TargetJudgment         ConflictTarget = "judgment"
	TargetJudgmentLink     ConflictTarget = "judgment_link"
	TargetSnapshot         ConflictTarget = "snapshot"
	TargetSession          ConflictTarget = "session"
	TargetSessionWorkspace ConflictTarget = "session_workspace"
	TargetSignal           ConflictTarget = "signal"
	TargetFootprint        ConflictTarget = "footprint"
	TargetResourceHold     ConflictTarget = "resource_hold"
	TargetCounter          ConflictTarget = "counter"
	TargetRefState         ConflictTarget = "ref_state"
	TargetChangeSet        ConflictTarget = "change_set"
	TargetIdempotency      ConflictTarget = "idempotency"
)

// ConflictTargets 는 이 패키지가 쓰는 대상 전부다.
//
// 표면이 대상마다 문구를 갖는지 시험이 이 목록으로 전수 확인한다 —
// 목록이 없으면 대상을 새로 늘린 날 그 하나만 문구 없이 새어 나가고, 그 사실이 안 보인다.
func ConflictTargets() []ConflictTarget {
	return []ConflictTarget{
		TargetItem, TargetItemAfter, TargetClaim,
		TargetJudgment, TargetJudgmentLink, TargetSnapshot,
		TargetSession, TargetSessionWorkspace, TargetSignal, TargetFootprint,
		TargetResourceHold, TargetCounter,
		TargetRefState, TargetChangeSet, TargetIdempotency,
	}
}

// ConflictError 는 제약 위반 하나다.
//
// **좌표를 담는다** — 무엇이 중복인지(ID) 무엇을 가리켰는지(RefHint)를 못 내면
// 소비자는 무엇을 고쳐야 하는지 알 수 없고, 그러면 이 타입을 만든 이유가 사라진다.
// 원인 전문은 Err 에 그대로 물려 둔다(로그로만 나간다).
type ConflictError struct {
	Kind    ConflictKind
	Target  ConflictTarget
	Project string
	ID      string
	// RefHint 는 이 쓰기가 가리키는 좌표들이다(예: "프로젝트 cp · 세션 01J…").
	// FK 위반은 **어느 FK 인지**를 드라이버가 코드로 말해 주지 않으므로 —
	// 문구를 파싱해 알아내는 것은 이 파일이 금지하는 바로 그 우회다 —
	// 호출부가 자기 표의 FK 목록을 정적으로 적어 넘긴다.
	RefHint string
	Code    int    // SQLite 확장 결과 코드. 로그·진단용
	Reason  string // JudgeConstraintCode 가 낸 사유 전문
	Err     error  // 드라이버 원문
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%s 제약 위반(%s, project=%q id=%q, code=%d): %s: %v",
		e.Target, e.Kind, clip(e.Project, 64), clip(e.ID, 64), e.Code, e.Reason, e.Err)
}

func (e *ConflictError) Unwrap() error { return e.Err }

// ConstraintVerdict 는 결과 코드 하나의 판정이다. Reason 은 **항상** 채운다.
type ConstraintVerdict struct {
	// Kind 가 비면 접지 않는다 — 그 오류는 500 으로 남아야 한다.
	Kind   ConflictKind
	Reason string
}

// JudgeConstraintCode 는 SQLite 확장 결과 코드를 도메인 축으로 옮긴다. 순수 함수다.
//
// ★ CHECK·NOT NULL·트리거 ABORT 는 **일부러 접지 않는다.** 그 셋은 전부 1차 방어가
// 앞에 있고(ValidateFinish·ValidateHolder·ValidateAfter·AddJudgment 의 본문 검사),
// 그것들을 지나 DB 까지 닿았다면 서버가 불변식을 깨는 행을 만들었다는 뜻이다.
// 그것은 호출자가 고칠 거리가 아니라 서버의 결함이므로 500 이 맞다.
// 4xx 로 접으면 그 결함이 "사용자 잘못"으로 분류돼 영영 안 고쳐진다.
func JudgeConstraintCode(code int) ConstraintVerdict {
	switch code {
	case 0:
		return ConstraintVerdict{Reason: "드라이버 결과 코드가 없다 — 제약 위반이 아니다"}

	case sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY, sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_ROWID:
		return ConstraintVerdict{Kind: ConflictDuplicate,
			Reason: fmt.Sprintf("같은 좌표가 이미 있다(코드 %d) — 오타·재등록에서 흔한 정상적인 거절이다", code)}

	case sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY:
		return ConstraintVerdict{Kind: ConflictMissingRef,
			Reason: fmt.Sprintf("가리키는 좌표가 등록돼 있지 않다(코드 %d) — 먼저 등록해야 하는 것이 있다", code)}

	case sqlite3.SQLITE_CONSTRAINT_CHECK, sqlite3.SQLITE_CONSTRAINT_NOTNULL, sqlite3.SQLITE_CONSTRAINT_TRIGGER:
		return ConstraintVerdict{
			Reason: fmt.Sprintf("불변식 위반이다(코드 %d) — 1차 방어를 지나 DB 까지 닿았다는 뜻이라 "+
				"호출자가 고칠 거리가 아니다. 서버 결함으로 남긴다", code)}

	case sqlite3.SQLITE_CONSTRAINT:
		return ConstraintVerdict{
			Reason: "제약 위반인데 확장 코드가 없어 종류를 못 가린다 — 접지 않고 서버 결함으로 남긴다"}
	}

	if code&0xff == sqlite3.SQLITE_CONSTRAINT {
		return ConstraintVerdict{
			Reason: fmt.Sprintf("아는 축이 아닌 제약 위반이다(코드 %d) — 접지 않고 서버 결함으로 남긴다", code)}
	}
	return ConstraintVerdict{
		Reason: fmt.Sprintf("제약 위반이 아니다(코드 %d, 기본 코드 %d)", code, code&0xff)}
}

// ConstraintCode 는 오류 사슬에서 SQLite 확장 결과 코드를 꺼낸다. 없으면 0 이다.
//
// errors.As 로 찾으므로 **감싼 오류 안에서도** 찾아낸다 — 저장 계층이 fmt.Errorf 로
// 문맥을 덧붙인 뒤에도 이 축이 살아 있어야 한다.
func ConstraintCode(err error) int {
	var se *sqlite.Error
	if errors.As(err, &se) {
		return se.Code()
	}
	return 0
}

// conflictOf 는 쓰기 오류가 접을 수 있는 제약 위반이면 타입 있는 오류로 옮긴다.
// 아니면 nil 이다(호출부가 원래 오류를 그대로 감싸 올린다).
func conflictOf(err error, t ConflictTarget, project, id, refHint string) *ConflictError {
	code := ConstraintCode(err)
	v := JudgeConstraintCode(code)
	if v.Kind == "" {
		return nil
	}
	return &ConflictError{
		Kind: v.Kind, Target: t, Project: project, ID: id, RefHint: refHint,
		Code: code, Reason: v.Reason, Err: err,
	}
}

// writeTarget 은 쓰기 한 건의 좌표다. writeErr 의 인자를 한 덩어리로 묶는다.
type writeTarget struct {
	Target  ConflictTarget
	Project string
	ID      string
	// RefHint 는 이 표가 가리키는 FK 들을 사람이 읽을 한 줄로 적은 것이다.
	// **정적으로 적는다** — 드라이버는 어느 FK 인지 말해 주지 않는다.
	RefHint string
}

// writeErr 는 쓰기 오류 하나를 올린다.
//
// 제약 위반이면 *ConflictError, 아니면 문맥을 덧붙인 원래 오류다.
// 모든 쓰기가 이 한 자리를 지나게 해서 "어떤 표는 접고 어떤 표는 안 접는" 비대칭을 없앤다 —
// 비대칭은 결함의 신호이고, 실제로 이 결함이 그 모양이었다(자원 점유만 타입 있는 오류였다).
func writeErr(err error, t writeTarget, format string, args ...any) error {
	if err == nil {
		return nil
	}
	if c := conflictOf(err, t.Target, t.Project, t.ID, t.RefHint); c != nil {
		return c
	}
	return fmt.Errorf(format+": %w", append(append([]any{}, args...), err)...)
}
