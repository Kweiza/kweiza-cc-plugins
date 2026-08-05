package store

import "strings"

// 없음(404) 의 좌표 — **무엇이 없었는지**를 타입이 들고 다닌다.
//
// ★ 이 파일이 생긴 이유. 앞선 판은 없음을 `fmt.Errorf("항목 %s/%s 가 %w", …, ErrNotFound)` 로만
// 만들었고, 좌표가 **문자열 안에만** 있었다. 표면 계층(internal/api)은 규율상 응답 문구를
// err.Error() 에서 만들지 않으므로(그 문자열에는 SQL·파일 경로·드라이버 이름이 섞인다)
// 그 좌표를 쓸 방법이 없었고, 결국 404 를 좌표 없는 일반 문구 하나로 접었다.
// 그래서 **오타 난 항목 id 와 프로젝트 미등록과 이미 반납된 선점이 글자 그대로 같은 화면**이 됐다.
//
// 404 는 여러 사유를 하나로 합류시키므로 좌표가 없으면 조사가 사용자 신고 충실도에 의존한다.
// 처방은 ConflictError 와 같다 — **타입이 도메인 필드를 들고, 문구는 표면이 그 필드로 조립한다.**
// 그러면 내부 좌표가 새는 것이 검사로 막히는 것이 아니라 구조적으로 불가능해진다.

// NotFoundKind 는 무엇이 없었는지의 종류다.
//
// 값이 그대로 사람이 읽는 이름이다. 기계 좌표(영문 열거)와 표시 문구를 가르지 않는 이유:
// 이 축의 소비자가 응답 문구 하나뿐이라, 가르면 같은 목록이 두 벌이 되고 두 벌은 표류한다.
type NotFoundKind string

const (
	NFProject      NotFoundKind = "프로젝트"
	NFMachine      NotFoundKind = "머신"
	NFItem         NotFoundKind = "항목"
	NFClaim        NotFoundKind = "선점"
	NFLiveClaim    NotFoundKind = "살아 있는 선점"
	NFSession      NotFoundKind = "세션"
	NFJudgment     NotFoundKind = "판단"
	NFSnapshot     NotFoundKind = "스냅숏"
	NFResourceHold NotFoundKind = "자원 점유"
	NFIdempotency  NotFoundKind = "멱등 기록"
	NFRefState     NotFoundKind = "ref 관측"
	NFChangeSet    NotFoundKind = "변경집합"

	// NFLiveLandingRow 는 NFLiveClaim 과 같은 자리다. 이미 빠진 줄 행은 지워지지 않고
	// 이력으로 남으므로, "행이 아예 없다"와 "이미 빠졌다"를 SELECT 를 하나 더 붙여
	// 가르지 않는다 — 갈라봐야 회수 화면이 두 경우에 같은 문구를 낼 뿐이다.
	NFLiveLandingRow NotFoundKind = "살아 있는 랜딩 줄 행"
)

// NotFoundKinds 는 종류 **전부**다.
//
// ConflictTargets 와 같은 자리다 — 표면 계층의 시험이 이 목록으로 처방표를 전수 확인한다.
// 종류를 하나 늘리고 처방을 안 채우면 그 하나만 기본 문구로 새어 나가는데,
// 기본 문구는 무엇을 고쳐야 하는지 말하지 못한다.
func NotFoundKinds() []NotFoundKind {
	return []NotFoundKind{
		NFProject, NFMachine, NFItem, NFClaim, NFLiveClaim, NFSession,
		NFJudgment, NFSnapshot, NFResourceHold, NFIdempotency, NFRefState, NFChangeSet,
		NFLiveLandingRow,
	}
}

// NotFoundError 는 조회 대상이 없다는 사실을 **좌표와 함께** 나른다.
//
// 여기 실리는 값은 전부 **호출자가 방금 보낸 것**이다(프로젝트·항목·세션 id).
// 내부 이름이 아니므로 응답에 실어도 서버 내부가 새지 않는다 — 그것이 이 타입과
// "err.Error() 를 응답에 쓰지 않는다"는 규율이 양립하는 이유다.
//
// 그래도 **외부에서 온 값이라 반드시 잘라 싣는다.** 절단·제어문자 제거는 생성자(notFound)가
// 하고, 소비 계층(internal/api)이 한 번 더 한다. 가드는 소비 계층에 있어야 하지만
// 원천에서도 자르면 로그 한 줄이 통째로 오염되는 경로가 먼저 막힌다.
type NotFoundError struct {
	Kind    NotFoundKind
	Project string // 프로젝트 축이 없는 대상(프로젝트·머신·세션·멱등 기록)은 빈 문자열
	ID      string // 그 대상의 좌표. 3중키 조회처럼 id 가 없으면 빈 문자열
	Note    string // 좌표가 없을 때 대신 적는 한정어("3중키에 해당하는")
}

// What 은 "무엇이 없었나" 한 조각이다 — "항목 cp/t9-x" · "프로젝트 cp" · "3중키에 해당하는 세션".
//
// 조사(을/를·이/가)를 붙이지 않는다. 종류 이름과 좌표가 둘 다 가변이라 어느 조사가 맞는지
// 조립 시점에 알 수 없고, 틀린 조사는 매 응답에 남는다(ConflictAdvice 와 같은 규율).
func (e *NotFoundError) What() string {
	switch {
	case e == nil:
		return ""
	case e.Note != "":
		return e.Note + " " + string(e.Kind)
	case e.Project != "" && e.ID != "":
		return string(e.Kind) + " " + e.Project + "/" + e.ID
	case e.ID != "":
		return string(e.Kind) + " " + e.ID
	default:
		return string(e.Kind)
	}
}

func (e *NotFoundError) Error() string {
	if e == nil {
		return ""
	}
	return e.What() + " 가 " + ErrNotFound.Error()
}

// Unwrap 이 ErrNotFound 를 내므로 기존 `errors.Is(err, store.ErrNotFound)` 는 전부 그대로 산다.
// 좌표를 쓰고 싶은 자리만 errors.As 로 이 타입을 집으면 된다.
func (e *NotFoundError) Unwrap() error { return ErrNotFound }

// notFound 는 좌표가 있는 없음이다.
func notFound(kind NotFoundKind, project, id string) *NotFoundError {
	return &NotFoundError{
		Kind:    kind,
		Project: clip(strings.TrimSpace(project), 64),
		ID:      clip(strings.TrimSpace(id), 200),
	}
}

// notFoundNote 는 좌표를 특정할 수 없는 없음이다(3중키 조회처럼 id 자체가 없는 경우).
//
// 좌표를 지어내지 않는다 — 지어내면 그것이 새 거짓이 되고, 조사하는 쪽은
// 그 값이 실제로 조회에 쓰인 값이라고 믿는다.
func notFoundNote(kind NotFoundKind, note string) *NotFoundError {
	return &NotFoundError{Kind: kind, Note: clip(strings.TrimSpace(note), 120)}
}
