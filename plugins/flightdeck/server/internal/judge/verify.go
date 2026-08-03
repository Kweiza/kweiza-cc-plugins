package judge

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// ─────────────────────────────────────────────────────────────────────────────
// 사유 문자열의 형식 — 이 패키지 전체가 이 모양을 쓴다
//
//	"<코드>: <상세>"   예: "after-bad-ref: dep_sha=abc1234 — 그런 ref 가 없다(git rc=128)"
//
// 코드는 기계가 세는 축이고(탈락 사유 분포), 상세는 사람이 읽는 자리다.
// 둘을 한 문자열에 담되 **가르는 함수를 하나만 둔다** — 소비자마다 자기 방식으로 파싱하면
// 문구가 바뀌는 순간 조용히 어긋난다.
// ─────────────────────────────────────────────────────────────────────────────

// SplitReason 은 사유 문자열을 코드와 상세로 가른다.
// 구분자가 없으면 전부가 코드다(상세 없는 사유도 유효하다).
func SplitReason(reason string) (code, detail string) {
	code, detail, found := strings.Cut(reason, ":")
	if !found {
		return reason, ""
	}
	return code, strings.TrimSpace(detail)
}

// 검증 판정의 사유 코드.
const (
	VerifyOKCode         = "verify-ok"
	VerifyNoMatchCode    = "verify-no-match"    // 아직 안 끝났거나 실패다
	VerifyMultiMatchCode = "verify-multi-match" // 판정 기준 자체가 틀렸다
)

// VerifyOK 는 검증(예: `make ci`)이 통과했는지를 **종료코드가 아니라 로그로** 판정한다.
//
// 종료코드를 믿었다가 두 번 거짓 보고를 냈다. 래퍼가 0을 내면서 안쪽이 실패한 경우와
// 로그 중간의 초록만 보고 통과로 단정한 경우다. 그래서 판정은 하나뿐이다:
// **마지막 꼬리 줄 안에 okLineRe 에 줄 전체가 매치되는 행이 정확히 1개.**
//
// 꼬리를 몇 줄로 자를지(tail_lines)는 호출자 몫이다 — 이 함수는 받은 것만 본다.
//
// 반환 셋이 각각 다른 사건이다:
//
//	ok=true             통과. reason 에 몇 행이 표식이었는지가 실린다
//	ok=false, err=nil   판정했고 통과가 아니다. reason 이 0개인지 2개 이상인지를 가른다
//	err!=nil            **판정할 수 없었다.** 이때 reason 은 비어 있다 —
//	                    "검증이 실패했다"와 "판정 불가"를 한 문자열로 뭉개면
//	                    설정 오타가 검증 실패로 보고되고, 그 둘은 처방이 정반대다
//	                    (하나는 코드를 고치고 하나는 .flightdeck.yaml 을 고친다).
func VerifyOK(logTail []string, okLineRe string) (ok bool, reason string, err error) {
	hits, err := VerifyMatchLines(logTail, okLineRe)
	if err != nil {
		return false, "", err
	}
	switch len(hits) {
	case 1:
		return true, fmt.Sprintf("%s: %d행", VerifyOKCode, hits[0]), nil
	case 0:
		// 0개와 2개 이상을 다른 사유로 가르는 이유는 처방이 완전히 다르기 때문이다.
		// 0개는 "더 기다리거나 로그를 읽어라", 2개 이상은 "판정 기준을 고쳐라"다.
		//
		// 적용 패턴을 함께 싣는다 — anchorFullLine 이 설정에 적힌 문자열과 **다른** 정규식을
		// 돌리기 때문이다(`^PASS|CI OK$` 를 준 사람은 자기가 쓴 것과 다른 판정을 받는다).
		// 그 사실이 사유에 없으면 "왜 안 맞았나"에 답할 원천이 없다.
		return false, fmt.Sprintf("%s: 꼬리 %d줄 안에 매치가 없다 — 아직 안 끝났거나 실패했다(적용 패턴 %q)",
			VerifyNoMatchCode, countLines(logTail), clipPat(anchorFullLine(okLineRe))), nil
	default:
		return false, fmt.Sprintf("%s: %d줄이 매치했다(%s행) — 표식이 유일하지 않으니 판정 기준이 틀렸다(적용 패턴 %q)",
			VerifyMultiMatchCode, len(hits), joinInts(hits), clipPat(anchorFullLine(okLineRe))), nil
	}
}

// VerifyMatchLines 는 매치된 행 번호(1부터)를 전부 돌려준다.
//
// VerifyOK 가 이 위에 얹힌다. 판정과 별개로 **어느 행이 매치했는지**를 낼 수 있어야
// 2개 이상 매치를 사람이 그 자리에서 고칠 수 있다 — 사유만 주고 좌표를 안 주면
// 로그 60줄을 다시 눈으로 훑게 된다.
func VerifyMatchLines(logTail []string, okLineRe string) ([]int, error) {
	// 아래 두 가드는 **다른 사건**이라 둘 다 있어야 한다.
	//
	//   여기(입력 계층)  — 설정이 비었다. 사람이 ok_line 을 안 채운 것이다.
	//   아래(소비 계층)  — 패턴이 빈 줄에 매치한다. 사람이 잘못 쓴 것이다.
	//
	// 앞선 리뷰는 전자를 후자의 특수 사례로 보고 지우라고 했는데, 그것은 `""` 에만 참이다.
	// `"   "`(공백뿐)은 `^(?:   )$` 가 되어 빈 줄에 매치하지 **않으므로**(공백 3칸 줄에 매치한다)
	// 아래 가드를 그냥 지난다. 실측으로 확인했다. 처방도 다르다 —
	// 전자는 "설정을 채워라", 후자는 "패턴을 좁혀라"다.
	if strings.TrimSpace(okLineRe) == "" {
		return nil, fmt.Errorf("ok_line 이 비어 있다 — 무엇이 통과인지 정의되지 않았다")
	}
	re, err := regexp.Compile(anchorFullLine(okLineRe))
	if err != nil {
		return nil, fmt.Errorf("ok_line 정규식을 컴파일할 수 없다(원문 %q): %w", okLineRe, err)
	}
	// ★ 가드는 **소비 계층**에 둔다 — 값이 들어오는 자리가 아니라 그 값이 실제로 쓰이는 자리에.
	//
	// 앞선 판은 여기서 `strings.TrimSpace(okLineRe) == ""` 만 봤다. 그것은 빈 **문자열**을
	// 막을 뿐 빈 **매치**를 못 막는다. 실측으로 `^`·`$`·`^$`·`()`·`^()$` 다섯 패턴이 전부
	// 그 검사를 지나 앵커되면서 `^(?:)$` 가 됐고, 실패한 CI 로그(빈 줄 하나 포함)를
	// `ok=true` 로 통과시켰다. 현실적 유입 경로는 오타가 아니라 `(CI OK: .*)?` 같은 선택 그룹이다.
	//
	// 그래서 검사 대상을 문자열이 아니라 **컴파일된 정규식의 동작**으로 옮긴다.
	// 이러면 빈 문자열·공백뿐인 문자열은 이 검사의 특수 사례가 되어 따로 막을 필요가 없다.
	//
	// 이 판정은 랜딩 게이트의 유일한 판정식이다(DESIGN §8). 여기가 새면 깨진 브랜치가 main 에 오른다.
	if re.MatchString("") {
		return nil, fmt.Errorf(
			"ok_line %q 가 빈 줄에 매치한다 — 로그 꼬리의 빈 줄 하나가 통과로 읽힌다(적용 패턴 %q)",
			clipPat(okLineRe), clipPat(re.String()))
	}
	var hits []int
	n := 0
	for _, chunk := range logTail {
		// 원소 하나에 여러 줄이 들어와도 줄로 쪼갠다. 호출자가 꼬리를 통째로 넘기는 경로가
		// 실재하고, 안 쪼개면 줄 전체 정규식이 **영원히 0개**를 내면서 그 사유가
		// "아직 안 끝났다"로 보고된다 — 가장 나쁜 종류의 조용한 오답이다.
		for _, line := range strings.Split(chunk, "\n") {
			n++
			// CRLF 로 캡처된 로그의 끝 CR 만 걷어낸다. 그 이상 다듬지 않는다 —
			// 공백까지 없애면 "표식 뒤에 뭔가 더 붙은 줄"이 통과한다.
			line = strings.TrimSuffix(line, "\r")
			if re.MatchString(line) {
				hits = append(hits, n)
			}
		}
	}
	return hits, nil
}

// anchorFullLine 은 패턴을 **줄 전체** 정규식으로 만든다.
//
// 부분 문자열로 찾으면 그 문구를 인용한 산문이 경계를 옮긴다 — 실제로 난 결함이다.
// 성공 표식을 부분 문자열 grep 으로 찾다가, 그 표식을 인용한 문서 줄이 통과했다.
//
// 호출자가 이미 `^…$` 를 준 경우(.flightdeck.yaml 의 ok_line 은 규약상 그렇게 쓴다)
// 이중 앵커가 되지 않게 양끝의 앵커를 한 번 걷어낸 뒤 다시 감싼다.
// `(?:…)` 로 감싸는 것이 핵심이다 — 그냥 `^…$` 로 붙이면 최상위 교대(`a|b`)가
// `^a` 와 `b$` 로 쪼개져 **한쪽만 앵커된다.**
func anchorFullLine(pat string) string {
	body := strings.TrimPrefix(pat, "^") // 하나만 걷어낸다. `^^` 는 호출자의 의도로 본다
	if endsWithUnescapedDollar(body) {
		body = body[:len(body)-1]
	}
	return "^(?:" + body + ")$"
}

// endsWithUnescapedDollar 는 끝의 `$` 가 앵커인지 리터럴(`\$`)인지 가른다.
//
// 앞의 역슬래시 개수가 홀수면 `$` 가 이스케이프된 것이므로 **걷어내면 안 된다** —
// 걷어내면 `^cost: 100\$` 같은 패턴이 `^cost: 100\)$` 로 깨진다.
func endsWithUnescapedDollar(s string) bool {
	if !strings.HasSuffix(s, "$") {
		return false
	}
	backslashes := 0
	for i := len(s) - 2; i >= 0 && s[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 == 0
}

// countLines 는 꼬리의 실제 줄 수다(원소 수가 아니다 — 원소 하나에 여러 줄이 올 수 있다).
func countLines(logTail []string) int {
	n := 0
	for _, chunk := range logTail {
		n += strings.Count(chunk, "\n") + 1
	}
	return n
}

func joinInts(ns []int) string {
	parts := make([]string, 0, len(ns))
	for _, n := range ns {
		parts = append(parts, strconv.Itoa(n))
	}
	return strings.Join(parts, ", ")
}

// clipPat 은 사유·오류에 실을 패턴을 자르고 제어문자를 걷어낸다.
//
// 외부에서 온 문자열(.flightdeck.yaml 의 ok_line)이 그대로 로그와 도구 응답에 실리므로
// 로그 주입을 막는다. 길이는 사람이 읽을 만큼만 남긴다.
func clipPat(s string) string {
	const max = 120
	var b strings.Builder
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' {
			b.WriteByte(' ')
			continue
		}
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
		if b.Len() >= max {
			return b.String() + "…"
		}
	}
	return b.String()
}
