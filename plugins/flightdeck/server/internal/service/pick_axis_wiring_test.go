package service

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// pick 의 포인터 축은 **모든 갈래에서** 채워진다
// ─────────────────────────────────────────────────────────────────────────────
//
// 이 저장소가 포인터로 세운 축들(QueueOpen·PathCheck·CloseDeclared·Bundle)의 계약은
// 하나다: nil = "이 응답은 그 축을 안 읽었다". 그래서 **읽을 수 있었는데 안 채운 갈래**는
// 관측한 적 없는 사실을 응답에 심는다 — 렌더가 nil 을 보고 "낡은 캐시이거나 서버가 이
// 축을 모르는 판이다"를 찍는데, 그것은 현행 서버의 신선한 온라인 응답이다.
//
// 그 결함이 실제로 두 번 났다. 한 번은 Bundle 축(pickExplicit 이 안 채웠다), 한 번은
// CloseDeclared 축(같은 함수가 같은 실패를 새 축에 그대로 반복했다). 두 번 다 축을
// **더한** 사람이 갈래 하나를 못 보고 지나갔고, 계층별 시험은 원리적으로 그것을 못 잰다:
// judge 시험은 EligibleInput 을, render 시험은 PickResult 를 손으로 조립하기 때문이다
// (pick_wiring_test.go 머리말이 같은 말을 한다).
//
// 그래서 소스를 직접 훑는다 — 선례는 pick_report_truth_test.go 의
// TestPickExplicitHasNoFatalReturnAfterCommit(커밋 뒤 구간의 치명적 반환) ·
// indexnotation_test.go(순번 표기) · gofmt_gate_test.go 다.
// **더하는 사람의 빨간불이 켜져야 그 사람이 규율을 읽는다.**
//
// ★ 축 목록은 **소스에서 뽑는다**(구조체의 포인터 필드). 시험에 목록을 적으면 새 축이
// 들어와도 이 관문은 조용하다 — sort_axis_doc_test.go 의 하드코딩 축 목록이 리뷰에서
// 정확히 그 이유로 지적됐다. 갈래 목록도 소스에서 뽑되 **집합이 달라지면 실패한다**
// (wantPickResultBranches·wantBundleMemberBranches) — 새 갈래는 새 예외 판단을 요구하므로
// 사람이 한 번 봐야 한다.
//
// ★ 이 관문이 못 잡는 것(문자열 매칭의 한계다. 넓히지 않고 적어 둔다):
//
//  1. `res.PathCheck = nil` 처럼 명시적으로 nil 을 대입하면 "채웠다"로 센다. 이 관문은
//     배선의 존재를 재지 값을 재지 않는다 — 값은 pick_wiring_test.go 의 행동 시험이
//     잰다. 둘은 짝이고, 어느 한쪽만으로는 부족하다.
//  2. 축을 값 타입으로 강등하면 목록에서 빠져 조용해진다. 그 구멍은 아래
//     TestPickAxesThatDocumentThePointerContractAreStillPointers 가 닫는다.
//  3. 반환 변수 이름이 `res` 가 아니거나 구성원이 `BundleMember{}` 리터럴/그 변수로
//     안 만들어지면 좌표를 잃는다. 아래 전제 단정들이 그 이름을 못박는다.
//  4. 지배 판정은 gofmt 들여쓰기로 한다. 같은 깊이의 다른 블록에서 대입해도 통과할 수
//     있다 — 정확히 하려면 CFG 가 필요하고 이 관문은 거기까지 안 간다.
//
// ★ **이 넷이 전부라는 보장은 없다.** 이 목록은 산문이고 아무도 안 재므로 다섯째 구멍은
// 누가 밟기 전까지 여기 안 적힌다. 이미 찾은 것을 잃지 않으려 적는 것이지 다음 것을
// 찾아 주지 않는다 — 새 우회가 의심되면 이 목록을 다시 읽지 말고 아래 코드를 처음부터
// 끝까지 읽어라.
//
// 그 단서를 **예외 표에는 일부러 안 붙였다.** 표는 산문이 아니라 기계가 양방향으로 닫는다:
// 빠진 자리는 missing 으로, 실제로는 채워지는 예외와 없는 갈래·축의 예외는 stale 로,
// 예외로만 덮인 축은 판별력 항으로 각각 붉어진다. 거기에 "전부는 아닐 수 있다"를 적으면
// 다음 사람이 붉은 예외 검사를 "원래 불완전한 목록"으로 읽고 넘긴다 — 없는 겸손이
// 있는 관문을 끈다.

// pickAxisPointerFieldRe 는 구조체 본문의 포인터 필드 한 줄이다(탭 하나 깊이 · 타입이 `*`).
var pickAxisPointerFieldRe = regexp.MustCompile("^\t([A-Z]\\w*)\\s+\\*[\\w.\\[\\]]+")

// pickAxisPointerDocRe 는 "이 필드가 포인터인 것 자체가 계약이다" 라고 적은 doc 주석이다.
// BundleMember.Claimed 의 "포인터로 승격하지 않기로 했다" 는 일부러 안 걸린다.
var pickAxisPointerDocRe = regexp.MustCompile(`\*\*포인터다\.\*\*|포인터인 이유`)

// pickAxisProducerRe 는 갈래를 내는 함수 이름이다. Pick 은 디스패처라 뺀다 —
// 그 함수의 res 는 아래 셋 중 하나가 이미 통째로 채워 준 값이다.
var pickAxisProducerRe = regexp.MustCompile(`^pick[A-Z]\w*$`)

func pickAxisSource(t *testing.T) string {
	t.Helper()
	p := filepath.Join(serverRoot(t), "internal", "service", "pick.go")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("pick.go 를 못 읽었다(%s): %v", p, err)
	}
	return string(b)
}

// pickAxisStructBlock 은 `type <name> struct {` 의 본문이다(중괄호 안).
func pickAxisStructBlock(t *testing.T, src, name string) string {
	t.Helper()
	head := "\ntype " + name + " struct {\n"
	i := strings.Index(src, head)
	if i < 0 {
		t.Fatalf("%s 선언을 못 찾았다 — 이 관문의 좌표가 틀렸다", name)
	}
	rest := src[i+len(head):]
	j := strings.Index(rest, "\n}\n")
	if j < 0 {
		t.Fatalf("%s 선언의 끝을 못 찾았다 — 이 관문의 좌표가 틀렸다", name)
	}
	return rest[:j]
}

// pickAxisFields 는 구조체의 포인터 필드 이름을 선언 순서로 낸다.
func pickAxisFields(t *testing.T, src, name string) []string {
	t.Helper()
	var out []string
	for _, ln := range strings.Split(pickAxisStructBlock(t, src, name), "\n") {
		if m := pickAxisPointerFieldRe.FindStringSubmatch(ln); m != nil {
			out = append(out, m[1])
		}
	}
	if len(out) == 0 {
		t.Fatalf("%s 에서 포인터 축을 하나도 못 뽑았다 — 정규식이 소스와 갈리면 "+
			"이 관문은 아무것도 안 지키면서 초록이 된다", name)
	}
	return out
}

// pickAxisRegion 은 최상위 함수 하나의 소스다.
type pickAxisRegion struct {
	name  string
	lines []string
	first int // pick.go 안에서 lines[0] 의 0-based 줄 번호
}

// pickAxisRegions 는 파일을 최상위 func 단위로 자른다.
func pickAxisRegions(src string) map[string]pickAxisRegion {
	lines := strings.Split(src, "\n")
	var starts []int
	for i, ln := range lines {
		if strings.HasPrefix(ln, "func ") {
			starts = append(starts, i)
		}
	}
	nameRe := regexp.MustCompile(`^func (?:\([^)]*\) )?(\w+)`)
	out := map[string]pickAxisRegion{}
	for k, s := range starts {
		e := len(lines)
		if k+1 < len(starts) {
			e = starts[k+1]
		}
		m := nameRe.FindStringSubmatch(lines[s])
		if m == nil {
			continue
		}
		out[m[1]] = pickAxisRegion{name: m[1], lines: lines[s:e], first: s}
	}
	return out
}

func pickAxisIndent(ln string) int {
	n := 0
	for n < len(ln) && ln[n] == '\t' {
		n++
	}
	return n
}

// pickAxisDominating 은 emit 줄에 닿으려면 반드시 지나친 줄들을 낸다(emit 부터 거꾸로).
//
// gofmt 는 블록 깊이를 탭으로 정확히 표현한다 — **단, 그 깊이가 판 의존이다.**
// go1.27 이 다중값 return 의 복합 리터럴 들여쓰기를 바꿨다(Fixes #7195). 이 훑기가 그런
// 자리에 닿으면 **빨개지는 게 아니라 조용히 눈이 먼다** — 지배 관계를 잘못 세고도 초록이다.
// 오늘 이 훑기 대상에 갈림 자리는 0건이지만 그것은 설계가 아니라 운이다
// (갈림 자체는 gofmt_era_split_gate_test.go 가 모듈 전체에서 막는다).
// emit 줄보다 얕아지는 지점마다 허용 깊이를
// 낮추면 남는 것은 emit 을 감싸는 블록들의 앞부분 — 즉 emit 을 실행했다면 반드시 실행된
// 줄들이다. 더 얕은 블록의 형제 가지(if 의 다른 갈래)는 깊이로 걸러진다.
func pickAxisDominating(lines []string, emit int) []string {
	cur := pickAxisIndent(lines[emit])
	out := []string{lines[emit]}
	for i := emit - 1; i >= 0; i-- {
		ln := lines[i]
		if strings.TrimSpace(ln) == "" {
			continue
		}
		d := pickAxisIndent(ln)
		if d < cur {
			cur = d
		}
		if d <= cur {
			out = append(out, ln)
		}
	}
	return out
}

// pickAxisAssigns 는 이 줄이 `<recv>.<axis>` 에 대입하는가다.
//
// 비교(==·!=·<=·>=)와 주석 줄은 뺀다. 복합 리터럴 `PickResult{... Axis: ...}` 도 대입으로
// 센다 — 그것이 pickRecommend·pickExplicit 이 실제로 쓰는 모양이다.
func pickAxisAssigns(ln, recv, axis string) bool {
	if strings.HasPrefix(strings.TrimSpace(ln), "//") {
		return false
	}
	i := strings.Index(ln, "=")
	if i < 0 {
		return false
	}
	if i+1 < len(ln) && ln[i+1] == '=' {
		return false
	}
	if i > 0 && strings.ContainsRune("!<>", rune(ln[i-1])) {
		return false
	}
	if regexp.MustCompile(`\b` + recv + `\.` + axis + `\b`).MatchString(ln[:i]) {
		return true
	}
	return strings.Contains(ln, "PickResult{") &&
		regexp.MustCompile(`\b`+axis+`:`).MatchString(ln)
}

func pickAxisAnyAssigns(lines []string, recv, field string) bool {
	for _, ln := range lines {
		if pickAxisAssigns(ln, recv, field) {
			return true
		}
	}
	return false
}

// pickAxisBranch 는 갈래 하나와 그 갈래가 채운 축들이다.
type pickAxisBranch struct {
	key    string // "<함수>/<표식>"
	line   int    // pick.go 의 1-based 줄
	filled map[string]bool
}

// ─────────────────────────────────────────────────────────────────────────────
// 예외 — "안 채우는 것이 옳다" 는 **근거를 달아야** 산다
// ─────────────────────────────────────────────────────────────────────────────
//
// 키는 `<갈래>|<축>` 이다. 근거가 짧으면 실패한다 — 빈 문자열로 축을 끄는 것을 막는 유일한
// 수단이다. 그리고 **낡은 예외도 실패한다**: 그 자리가 실제로 채워지면 예외는 거짓말이
// 되고, 거짓말하는 예외 표는 다음 사람에게 "여긴 원래 안 채운다"로 읽혀 진짜 결함을 덮는다.
var pickResultAxisWaivers = map[string]string{
	"pickRecommend/PickNone|Item": "적격 0건이라 낼 항목 자체가 없다 — 채우면 없는 항목을 추천한 것이 된다",
	"pickRecommend/PickNone|Claim": "아무것도 선점하지 않았다 — non-nil 이면 응답이 " +
		"원장에 없는 쓰기를 보고한다",
	"pickRecommend/PickNone|PathCheck": "항목이 없으면 검사할 경로가 없다 — " +
		"PickResult.PathCheck 주석이 '적격 0건에도 nil 이다' 로 못박은 그 자리다",
	"pickRecommend/PickNone|CloseDeclared": "항목이 없으면 원장에서 볼 대상이 없다 — " +
		"PathCheck 과 같은 이유이고 같은 주석이 계약을 적어 뒀다",
	"pickRecommend/PickNone|Bundle": "선두가 없으면 방사형으로 붙일 이웃도 없다 — " +
		"PickResult.Bundle 주석의 '적격 0건에도 nil 이다' 그대로다",
	"pickRecommend/PickNone|AfterCheck": "항목이 없으면 판정할 선행이 없다 — PathCheck·" +
		"CloseDeclared 와 같은 이유다. 게다가 이 축의 0값은 '충족됐다' 라서, 채우면 " +
		"없는 항목에 대해 관측한 적 없는 통과를 단정하게 된다",
	"pickRecommend/PickRecommended|Claim": "추천은 선점이 아니다 — 여기서 Claim 을 채우면 " +
		"아직 안 집은 항목을 집었다고 말하게 된다(PickRecommended 의 정의 자체다)",
}

var pickMemberAxisWaivers = map[string]string{
	"pickRecommend/미시도|Rejection": "추천 경로는 집기를 시도조차 안 했다 — BundleMember.Claimed " +
		"계약상 Claimed=false + Rejection=nil 이 '이 축을 아예 안 봤다' 다",
	"pickBundle/집음|Rejection": "집은 구성원에 사유를 달면 원장과 어긋난다 — " +
		"TestPickBundleMemberWithUnreadableNotesIsStillClaimed 가 nil 을 단정한다",
	"pickBundle/못집음|PathCheck": "선점에 실패해 이 축을 실제로 안 읽었다 — nil 이 계약 그대로다. " +
		"렌더도 못 집은 구성원 절을 사유 줄에서 끊어(renderBundle 의 continue) 이 축을 찍지 않는다",
}

// wantPickResultBranches 는 오늘 소스에서 발견되어야 하는 PickResult 갈래다.
var wantPickResultBranches = []string{
	"pickBundle/위임",
	"pickExplicit/PickClaimed",
	"pickExplicit/PickResumed",
	"pickRecommend/PickNone",
	"pickRecommend/PickRecommended",
}

// wantBundleMemberBranches 는 구성원이 Members 에 붙는 세 자리다.
var wantBundleMemberBranches = []string{
	"pickBundle/못집음",
	"pickBundle/집음",
	"pickRecommend/미시도",
}

// TestPickPointerAxesAreFilledOnEveryBranch 는 PickResult 의 포인터 축이 다섯 갈래를
// 다 채우는지 소스로 잰다.
func TestPickPointerAxesAreFilledOnEveryBranch(t *testing.T) {
	src := pickAxisSource(t)
	axes := pickAxisFields(t, src, "PickResult")
	regions := pickAxisRegions(src)

	// 전제 ① — PickResult 를 내는 함수는 Pick(디스패처) 아니면 pick* 뿐이다.
	// 다른 이름의 생산자가 생기면 이 관문이 그 갈래를 통째로 못 본다.
	for name, r := range regions {
		if !strings.Contains(strings.Join(r.lines, "\n"), "(PickResult, error) {") {
			continue
		}
		if name != "Pick" && !pickAxisProducerRe.MatchString(name) {
			t.Fatalf("PickResult 를 내는 함수 %q 가 pick* 이 아니다 — 이 관문은 그 갈래를 "+
				"안 본다. 이름을 맞추거나 pickAxisProducerRe 를 넓혀라", name)
		}
	}

	// 전제 ② — Pick 이 모든 갈래 뒤에 &res 로 부르는 공통 꼬리를 찾는다.
	// 지금은 fillQueueOpen 하나다: QueueOpen 은 **선점 쓰기가 끝난 뒤에** 세야 해서
	// 생산자가 아니라 거기서 채운다(Pick 의 ★ 주석). 그 자리를 갈래마다 요구하면
	// 이 관문이 옳은 설계를 거절하게 된다.
	tailFilled := map[string]bool{}
	tails := regexp.MustCompile(`s\.(\w+)\([^)]*&res\)`).
		FindAllStringSubmatch(strings.Join(regions["Pick"].lines, "\n"), -1)
	if len(tails) == 0 {
		t.Fatal("Pick 이 &res 로 부르는 꼬리가 하나도 없다 — QueueOpen 배선의 좌표가 사라졌다")
	}
	for _, m := range tails {
		for _, ln := range regions[m[1]].lines {
			for _, a := range axes {
				if pickAxisAssigns(ln, "res", a) {
					tailFilled[a] = true
				}
			}
		}
	}

	delegateRe := regexp.MustCompile(`^\tres, \w+ := s\.(pick[A-Z]\w*)\(`)
	modeRe := regexp.MustCompile(`Pick[A-Z]\w*`)

	var branches []pickAxisBranch
	for _, n := range pickAxisProducerNames(regions) {
		r := regions[n]
		for i, ln := range r.lines {
			if strings.TrimSpace(ln) != "return res, nil" {
				continue
			}
			dom := pickAxisDominating(r.lines, i)
			filled := map[string]bool{}
			for a := range tailFilled {
				filled[a] = true
			}
			label := "위임"
			for _, dl := range dom {
				for _, a := range axes {
					if pickAxisAssigns(dl, "res", a) {
						filled[a] = true
					}
				}
				// 위임 — 이 갈래의 res 는 다른 생산자가 통째로 채워 온 값이다.
				if m := delegateRe.FindStringSubmatch(dl); m != nil {
					for _, cl := range regions[m[1]].lines {
						for _, a := range axes {
							if pickAxisAssigns(cl, "res", a) {
								filled[a] = true
							}
						}
					}
				}
				// 갈래 이름은 이 반환이 내는 mode 다. 없으면 위임 갈래다.
				if label == "위임" && pickAxisAssigns(dl, "res", "Mode") {
					if mm := modeRe.FindString(dl[strings.Index(dl, "=")+1:]); mm != "" {
						label = mm
					}
				}
			}
			branches = append(branches, pickAxisBranch{
				key: n + "/" + label, line: r.first + i + 1, filled: filled})
		}
	}

	pickAxisAssertBranchSet(t, "PickResult", branches, wantPickResultBranches)
	pickAxisReport(t, "PickResult", branches, axes, pickResultAxisWaivers)
}

// TestBundleMemberPointerAxesAreFilledOnEveryBranch 는 구성원 축을 같은 방식으로 잰다.
//
// 구성원의 갈래는 반환이 아니라 **Members 에 붙는 자리**다(추천 후보 · 묶음에서 집은 것 ·
// 묶음에서 못 집은 것). 셋은 서로 다른 사실을 말하므로 축의 의무도 갈린다.
func TestBundleMemberPointerAxesAreFilledOnEveryBranch(t *testing.T) {
	src := pickAxisSource(t)
	axes := pickAxisFields(t, src, "BundleMember")
	regions := pickAxisRegions(src)
	ctorRe := regexp.MustCompile(`(\w+)\s*:?=\s*BundleMember\{`)

	var branches []pickAxisBranch
	for _, n := range pickAxisProducerNames(regions) {
		r := regions[n]
		for i, ln := range r.lines {
			if !strings.Contains(ln, "Members = append(") {
				continue
			}
			// 리터럴이 여러 줄이면 중괄호가 맞을 때까지 이어 붙인다.
			emit := ln
			for j := i; strings.Count(emit, "{") > strings.Count(emit, "}"); j++ {
				emit += "\n" + r.lines[j+1]
			}
			dom := pickAxisDominating(r.lines, i)
			recv := ""
			for _, dl := range dom {
				if m := ctorRe.FindStringSubmatch(dl); m != nil {
					recv = m[1]
					break
				}
			}
			if recv == "" && !strings.Contains(emit, "BundleMember{") {
				t.Fatalf("%s:%d 의 구성원이 BundleMember 리터럴로도 변수로도 안 만들어진다 — "+
					"이 관문이 좌표를 잃었다", n, r.first+i+1)
			}
			filled := map[string]bool{}
			for _, a := range axes {
				if strings.Contains(emit, "BundleMember{") &&
					regexp.MustCompile(`\b`+a+`:`).MatchString(emit) {
					filled[a] = true
				}
				if recv != "" && pickAxisAnyAssigns(dom, recv, a) {
					filled[a] = true
				}
			}
			label := "미시도"
			switch {
			case recv != "" && pickAxisAnyAssigns(dom, recv, "Claimed"):
				label = "집음"
			case recv != "" && pickAxisAnyAssigns(dom, recv, "Rejection"):
				label = "못집음"
			}
			branches = append(branches, pickAxisBranch{
				key: n + "/" + label, line: r.first + i + 1, filled: filled})
		}
	}

	pickAxisAssertBranchSet(t, "BundleMember", branches, wantBundleMemberBranches)
	pickAxisReport(t, "BundleMember", branches, axes, pickMemberAxisWaivers)
}

func pickAxisProducerNames(regions map[string]pickAxisRegion) []string {
	var names []string
	for n := range regions {
		if pickAxisProducerRe.MatchString(n) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

func pickAxisAssertBranchSet(t *testing.T, what string, got []pickAxisBranch, want []string) {
	t.Helper()
	keys := make([]string, 0, len(got))
	for _, b := range got {
		keys = append(keys, b.key)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") == strings.Join(want, ",") {
		return
	}
	t.Fatalf("%s 의 갈래 집합이 달라졌다.\n  발견: %v\n  기대: %v\n"+
		"갈래가 늘었으면 목록에 더하고 그 갈래가 축을 다 채우는지 직접 판단해라 — "+
		"새 갈래는 새 예외 판단을 요구한다. 줄었으면 예외 표에서 죽은 키를 걷어내라.",
		what, keys, want)
}

// pickAxisReport 는 (갈래 × 축) 격자를 예외 표와 대조한다.
func pickAxisReport(t *testing.T, what string, branches []pickAxisBranch,
	axes []string, waivers map[string]string) {
	t.Helper()

	if len(branches) == 0 || len(axes) == 0 {
		t.Fatalf("%s: 갈래 %d개 · 축 %d개 — 하나라도 0이면 이 관문은 아무것도 안 보면서 초록이다",
			what, len(branches), len(axes))
	}

	seen := map[string]bool{}
	required := map[string]int{}
	var missing, stale, thin []string
	for _, b := range branches {
		for _, a := range axes {
			k := b.key + "|" + a
			seen[k] = true
			why, waived := waivers[k]
			switch {
			case waived && b.filled[a]:
				stale = append(stale, k)
			case waived && len([]rune(why)) < 20:
				thin = append(thin, k)
			case waived: // 근거가 달린 예외다
			case b.filled[a]:
				required[a]++
			default:
				missing = append(missing, k+"  (pick.go:"+strconv.Itoa(b.line)+")")
			}
		}
	}
	for k := range waivers {
		if !seen[k] {
			stale = append(stale, k+"  (그런 갈래·축이 없다)")
		}
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("%s 의 포인터 축이 안 채워진 갈래가 %d자리다:\n  %s\n\n"+
			"이 축들의 계약은 nil = \"이 응답은 그 축을 안 읽었다\" 다. 읽을 수 있었는데 안 채우면 "+
			"렌더가 신선한 온라인 응답에 '낡은 캐시이거나 서버가 이 축을 모르는 판이다'를 찍는다 — "+
			"두 원인 다 거짓이고, 그걸 읽은 세션은 있지도 않은 서버 스큐를 고치러 간다.\n"+
			"안 채우는 것이 옳다면 예외 표에 **근거와 함께** 등록해라(근거 20자 이상).",
			what, len(missing), strings.Join(missing, "\n  "))
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Errorf("%s 의 예외가 낡았다(%d자리) — 실제로는 채워지거나 그런 자리가 없는데 "+
			"'안 채운다'로 적혀 있다:\n  %s\n"+
			"거짓말하는 예외 표는 다음 사람에게 '여긴 원래 안 채운다'로 읽혀 진짜 결함을 덮는다.",
			what, len(stale), strings.Join(stale, "\n  "))
	}
	if len(thin) > 0 {
		sort.Strings(thin)
		t.Errorf("%s 의 예외에 근거가 없다(%d자리):\n  %s\n"+
			"근거 없는 예외는 축을 끄는 스위치일 뿐이다 — 왜 안 채우는 것이 옳은지를 적어라.",
			what, len(thin), strings.Join(thin, "\n  "))
	}
	// 판별력 — 예외로만 덮인 축은 이 관문이 아무것도 안 지키는 축이다.
	for _, a := range axes {
		if required[a] == 0 {
			t.Errorf("%s 의 축 %q 가 어느 갈래에서도 필수가 아니다 — 이 축에 대해 이 관문은 꺼져 있다",
				what, a)
		}
	}
}

// TestPickAxesThatDocumentThePointerContractAreStillPointers 는 위 두 관문의 **입력**을
// 지킨다.
//
// 축 목록을 소스에서 뽑는 대가가 이것이다: 축을 값 타입으로 강등하면 목록에서 빠져 관문이
// 조용해진다. 그런데 이 저장소는 그 계약을 doc 주석에 이미 적어 뒀다("★ **포인터다.**" ·
// "★ 포인터인 이유가 둘이다"). 그 문장이 있는 필드는 포인터여야 한다.
func TestPickAxesThatDocumentThePointerContractAreStillPointers(t *testing.T) {
	src := pickAxisSource(t)
	fieldRe := regexp.MustCompile("^\t([A-Z]\\w*)\\s+(\\S+)")

	claimed := 0
	for _, name := range []string{"PickResult", "BundleMember"} {
		var doc []string
		for _, ln := range strings.Split(pickAxisStructBlock(t, src, name), "\n") {
			if strings.HasPrefix(strings.TrimSpace(ln), "//") {
				doc = append(doc, ln)
				continue
			}
			m := fieldRe.FindStringSubmatch(ln)
			if m == nil {
				continue
			}
			if pickAxisPointerDocRe.MatchString(strings.Join(doc, "\n")) {
				claimed++
				if !strings.HasPrefix(m[2], "*") {
					t.Errorf("%s.%s 의 doc 이 포인터 계약을 적어 놓고 타입은 %q 다 — 값 타입이면 "+
						"'안 읽었다'와 '읽었고 0건'이 한 값으로 접히고, 축 배선 관문도 이 축을 "+
						"목록에서 잃는다", name, m[1], m[2])
				}
			}
			doc = doc[:0]
		}
	}
	if claimed < 4 {
		t.Fatalf("포인터 계약을 적은 필드가 %d개뿐이다 — 넷(QueueOpen·PathCheck·CloseDeclared·"+
			"Bundle)은 있어야 한다. 주석 표식이 바뀌었다면 이 관문은 눈이 먼 것이다", claimed)
	}
}
