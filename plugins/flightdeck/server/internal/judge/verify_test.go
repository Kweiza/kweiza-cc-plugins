package judge

import (
	"regexp"
	"strings"
	"testing"
)

// 소비자의 좌표계로 쓴다: 이 함수를 부르는 쪽이 보는 것은 (통과인가 / 어떤 사유인가 /
// 판정을 못 했나) 셋뿐이다. 그래서 단정도 그 셋으로만 한다 — 정규식을 어떻게 감쌌는지는
// 구현의 개념이라 단정에 끌어오지 않는다.
func TestVerifyOK(t *testing.T) {
	const ciOK = `^CI OK: .* dev-[0-9a-f]{7,}$`

	cases := []struct {
		name     string
		tail     []string
		pattern  string
		wantOK   bool
		wantCode string // err 를 기대하는 경우 "" — 사유가 없어야 한다
		wantErr  bool
	}{
		// ── 매치 개수 0 / 1 / 2 ──
		{
			name: "표식이 정확히 1개면 통과",
			tail: []string{
				"go test ./... ok",
				"CI OK: 11단계 dev-9d2ada8",
			},
			pattern: ciOK, wantOK: true, wantCode: VerifyOKCode,
		},
		{
			name: "표식이 없으면 실패 — 아직 안 끝났거나 실패했다",
			tail: []string{
				"go test ./... ok",
				"image-sign: 서명 키를 찾을 수 없다",
			},
			pattern: ciOK, wantOK: false, wantCode: VerifyNoMatchCode,
		},
		{
			name: "표식이 2개면 판정 기준이 틀린 것이다",
			tail: []string{
				"CI OK: 11단계 dev-9d2ada8",
				"재실행",
				"CI OK: 11단계 dev-c8206a9",
			},
			pattern: ciOK, wantOK: false, wantCode: VerifyMultiMatchCode,
		},
		{
			name:    "빈 로그도 0개다",
			tail:    nil,
			pattern: ciOK, wantOK: false, wantCode: VerifyNoMatchCode,
		},

		// ── 부분 문자열이었으면 통과했을 줄 (이 함수의 존재 이유) ──
		{
			name: "표식을 인용한 산문은 통과시키지 않는다",
			tail: []string{
				`참고: "CI OK: 11단계 dev-9d2ada8" 줄이 있어야 통과다`,
			},
			pattern: ciOK, wantOK: false, wantCode: VerifyNoMatchCode,
		},
		{
			name: "호출자가 앵커를 안 줘도 줄 전체로 본다",
			tail: []string{
				`문서에는 CI OK: 11단계 dev-9d2ada8 라고 적혀 있다`,
			},
			pattern: `CI OK: .* dev-[0-9a-f]{7,}`, wantOK: false, wantCode: VerifyNoMatchCode,
		},

		// ── 앵커 처리 ──
		{
			name: "호출자가 준 ^…$ 가 이중 앵커로 깨지지 않는다",
			tail: []string{"CI OK"}, pattern: `^CI OK$`,
			wantOK: true, wantCode: VerifyOKCode,
		},
		{
			name: "앞 앵커만 줘도 뒤가 잠긴다 — 표식 뒤에 뭔가 붙은 줄은 표식이 아니다",
			tail: []string{"CI OK 인 줄 알았는데 아니었다"}, pattern: `^CI OK`,
			wantOK: false, wantCode: VerifyNoMatchCode,
		},
		{
			name: "뒤 앵커만 줘도 앞이 잠긴다",
			tail: []string{"어제의 CI OK"}, pattern: `CI OK$`,
			wantOK: false, wantCode: VerifyNoMatchCode,
		},
		{
			name: "최상위 교대가 한쪽만 앵커되지 않는다",
			tail: []string{"모두 PASS"}, pattern: `PASS|CI OK`,
			wantOK: false, wantCode: VerifyNoMatchCode,
		},
		{
			name: "최상위 교대의 다른 가지는 그대로 매치한다",
			tail: []string{"CI OK"}, pattern: `PASS|CI OK`,
			wantOK: true, wantCode: VerifyOKCode,
		},

		// ── 표 밖: `$` 가 앵커가 아니라 리터럴인 경우 ──
		{
			name: "이스케이프된 달러는 앵커가 아니다(뒤 앵커 없음)",
			tail: []string{`비용: 100$`}, pattern: `^비용: 100\$`,
			wantOK: true, wantCode: VerifyOKCode,
		},
		{
			name: "이스케이프된 달러 뒤에 진짜 앵커가 온 경우",
			tail: []string{`비용: 100$`}, pattern: `^비용: 100\$$`,
			wantOK: true, wantCode: VerifyOKCode,
		},

		// ── 표 밖: 입력 표기의 흔들림 ──
		{
			name:    "CRLF 로 캡처된 로그의 끝 CR",
			tail:    []string{"CI OK: 11단계 dev-9d2ada8\r"},
			pattern: ciOK, wantOK: true, wantCode: VerifyOKCode,
		},
		{
			name:    "원소 하나에 꼬리 전체가 통째로 들어와도 줄로 본다",
			tail:    []string{"go test ok\nCI OK: 11단계 dev-9d2ada8\n"},
			pattern: ciOK, wantOK: true, wantCode: VerifyOKCode,
		},
		{
			name:    "표식 뒤에 공백이 붙으면 표식이 아니다",
			tail:    []string{"CI OK: 11단계 dev-9d2ada8 "},
			pattern: ciOK, wantOK: false, wantCode: VerifyNoMatchCode,
		},

		// ── 판정 불가 (검증 실패와 다른 사건) ──
		{
			name: "정규식이 깨졌으면 판정 불가다",
			tail: []string{"CI OK: 11단계 dev-9d2ada8"}, pattern: `^CI OK: [$`,
			wantOK: false, wantCode: "", wantErr: true,
		},
		{
			name: "ok_line 이 비어 있으면 판정 불가다",
			tail: []string{""}, pattern: "",
			wantOK: false, wantCode: "", wantErr: true,
		},
		{
			name: "공백뿐인 ok_line 도 판정 불가다",
			tail: []string{"   "}, pattern: "   ",
			wantOK: false, wantCode: "", wantErr: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, reason, err := VerifyOK(c.tail, c.pattern)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, 기대한 err 유무 = %v", err, c.wantErr)
			}
			if ok != c.wantOK {
				t.Errorf("통과 여부 = %v, 기대 = %v (사유 %q)", ok, c.wantOK, reason)
			}
			if c.wantErr {
				// 판정 불가에 실패 사유를 실으면 "검증이 실패했다"와 한 문자열로 뭉개진다.
				if reason != "" {
					t.Errorf("판정 불가인데 사유가 실렸다: %q", reason)
				}
				return
			}
			code, detail := SplitReason(reason)
			if code != c.wantCode {
				t.Errorf("사유 코드 = %q, 기대 = %q (전문 %q)", code, c.wantCode, reason)
			}
			if detail == "" {
				t.Errorf("사유에 상세가 없다: %q — 코드만으로는 왜인지 알 수 없다", reason)
			}
		})
	}
}

// 위 표의 "인용한 산문" 케이스가 **실제로 무는지**를 먼저 증명한다.
// 부분 문자열 검색이었다면 통과했을 줄이라는 것이 성립하지 않으면
// 그 케이스는 아무것도 지키지 않으면서 초록이 된다.
func TestQuotedProseWouldHavePassedSubstringSearch(t *testing.T) {
	const raw = `CI OK: .* dev-[0-9a-f]{7,}`
	const prose = `참고: "CI OK: 11단계 dev-9d2ada8" 줄이 있어야 통과다`

	// 대조 조건: 앵커 없는 검색은 이 줄에서 매치한다.
	if !regexp.MustCompile(raw).MatchString(prose) {
		t.Fatalf("대조가 성립하지 않는다 — 부분 문자열 검색조차 이 줄에 안 걸린다: %q", prose)
	}
	ok, reason, err := VerifyOK([]string{prose}, raw)
	if err != nil {
		t.Fatalf("판정 불가: %v", err)
	}
	if ok {
		t.Errorf("인용한 산문이 통과했다 (사유 %q)", reason)
	}
}

func TestVerifyMatchLinesGivesCoordinates(t *testing.T) {
	tail := []string{
		"1행",
		"CI OK: a dev-1111111",
		"3행\n4행", // 원소 하나에 두 줄
		"CI OK: b dev-2222222",
	}
	got, err := VerifyMatchLines(tail, `^CI OK: .* dev-[0-9a-f]{7,}$`)
	if err != nil {
		t.Fatalf("판정 불가: %v", err)
	}
	// 2행과 5행이다 — 원소 번호가 아니라 **줄 번호**여야 한다.
	if len(got) != 2 || got[0] != 2 || got[1] != 5 {
		t.Errorf("매치 행 = %v, 기대 = [2 5]", got)
	}
}

// 사유 코드 셋이 서로 다른 문자열이어야 한다.
// 하나라도 같으면 "안 끝났다"와 "기준이 틀렸다"가 같은 칸으로 세어진다.
func TestVerifyReasonCodesAreDistinct(t *testing.T) {
	codes := []string{VerifyOKCode, VerifyNoMatchCode, VerifyMultiMatchCode}
	seen := map[string]bool{}
	for _, c := range codes {
		if c == "" {
			t.Fatalf("빈 사유 코드가 있다: %v", codes)
		}
		if seen[c] {
			t.Fatalf("사유 코드가 겹친다: %q (%v)", c, codes)
		}
		seen[c] = true
	}
}

func TestVerifyMultiMatchReasonNamesTheLines(t *testing.T) {
	// 2개 이상일 때 "몇 행"이 없으면 60줄을 눈으로 다시 훑게 된다.
	_, reason, err := VerifyOK([]string{
		"CI OK: a dev-1111111",
		"CI OK: b dev-2222222",
	}, `^CI OK: .* dev-[0-9a-f]{7,}$`)
	if err != nil {
		t.Fatalf("판정 불가: %v", err)
	}
	if !strings.Contains(reason, "1") || !strings.Contains(reason, "2") {
		t.Errorf("사유에 매치 행 좌표가 없다: %q", reason)
	}
}

func TestSplitReason(t *testing.T) {
	cases := []struct {
		in, code, detail string
	}{
		{"verify-ok: 2행", "verify-ok", "2행"},
		{"after-bad-ref", "after-bad-ref", ""},
		{"", "", ""},
		// 상세 안에 콜론이 또 있어도 코드는 첫 콜론 앞이다.
		{"resource-held: 자원 staging: 세션 S2", "resource-held", "자원 staging: 세션 S2"},
	}
	for _, c := range cases {
		code, detail := SplitReason(c.in)
		if code != c.code || detail != c.detail {
			t.Errorf("SplitReason(%q) = (%q, %q), 기대 = (%q, %q)", c.in, code, detail, c.code, c.detail)
		}
	}
}

// 아래 둘은 리뷰가 찾은 거짓 초록을 닫는다.
// 앞선 판의 가드는 입력 문자열만 봐서 "빈 줄에 매치하는 패턴"을 통째로 놓쳤고,
// 시험에는 `""`·`"   "` 두 줄이 있어 이 축이 덮인 것처럼 읽혔다.

func TestPatternsThatMatchAnEmptyLineAreRefused(t *testing.T) {
	// 실패한 CI 로그. 빈 줄이 정확히 하나 있다 — 이것이 "표식 1개"로 읽히면
	// 깨진 브랜치가 통과로 보고된다(DESIGN §8 의 유일한 판정식이다).
	log := []string{"go build ./...", "", "make: *** [ci] Error 1"}
	// 현실적 유입 경로는 오타가 아니라 선택 그룹이다 — `(CI OK: .*)?` 는 사람이 자연스럽게 쓴다.
	for _, pat := range []string{"^", "$", "^$", "()", "^()$", "(CI OK: .*)?", "x*", "(?:)"} {
		t.Run(pat, func(t *testing.T) {
			ok, reason, err := VerifyOK(log, pat)
			if err == nil {
				t.Fatalf("빈 줄에 매치하는 패턴 %q 가 거절되지 않았다: ok=%v reason=%q", pat, ok, reason)
			}
			// 판정 불가는 검증 실패와 다른 사건이므로 reason 이 비어야 한다.
			if reason != "" {
				t.Errorf("판정 불가인데 사유가 실렸다: %q", reason)
			}
		})
	}
}

func TestBlankOkLineIsItsOwnFailure(t *testing.T) {
	// `""` 와 `"   "` 는 "설정을 안 채웠다"이고, 위 시험의 패턴들은 "패턴을 잘못 썼다"다.
	// 처방이 다르므로 오류 문구도 달라야 한다 — 한 문구로 뭉개면 어느 쪽을 고칠지 알 수 없다.
	for _, pat := range []string{"", "   ", "\t"} {
		_, _, err := VerifyOK([]string{"x"}, pat)
		if err == nil {
			t.Fatalf("빈 ok_line %q 가 거절되지 않았다", pat)
		}
		if !strings.Contains(err.Error(), "비어 있다") {
			t.Errorf("빈 설정과 잘못된 패턴의 오류가 뭉개졌다: %v", err)
		}
	}
	// 그리고 공백뿐인 패턴은 소비 계층 가드를 **지나간다**는 사실을 못박아 둔다.
	// 이 단정이 없으면 "위 가드 하나면 충분하다"는 잘못된 정리가 다시 들어온다.
	if re := "^(?:   )$"; !regexpMatchesEmpty(re) {
		t.Logf("확인: %q 는 빈 줄에 매치하지 않는다 — 그래서 입력 계층 가드가 따로 필요하다", re)
	} else {
		t.Errorf("전제가 깨졌다: %q 가 빈 줄에 매치한다", re)
	}
}

func regexpMatchesEmpty(pat string) bool {
	re, err := regexp.Compile(pat)
	if err != nil {
		return false
	}
	return re.MatchString("")
}
