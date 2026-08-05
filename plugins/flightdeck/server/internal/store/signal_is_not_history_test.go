package store

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// signal 표는 현재값이다 — 이력이 아니다
// ─────────────────────────────────────────────────────────────────────────────
//
// signal 의 PK 는 (session_id, kind) 다(schema.sql:91-96). 세션×종류마다 정확히
// 한 행이고 갱신된다. 그래서 이 표에는 **횟수·간격·분포·과거 시점이라는 값이
// 원리적으로 존재하지 않는다.** 답할 수 있는 것은 "이 세션의 이 종류 최신 신호
// 시각" 하나뿐이다. 그것들의 재료는 event(kind='session.beat') 뿐이다 — 추가
// 전용이 트리거로 강제되고 Service.Beat 호출마다 1행이라 스로틀이 없다.
//
// ★ 이 시험이 왜 서 있나 — **주석은 이미 한 번 실패했다.**
//
// 같은 사실이 judge/prescribe.go 에 f19ea7e(2026-08-04)로 이미 적혀 있었다.
// 그런데 **그 다음날** 큐 항목 fd-live-window-baseline 이 "signal 표로 마지막
// 신호 후 다음 신호까지의 간격 분포를 재라"로 쓰였다. 그 항목을 집은 세션이
// 재려다 벽에 부딪혔고("못 잰다"), 그 사고 보고서가 이 시험을 낳은 항목
// fd-item-premise-signal-table-has-no-history 다.
//
// 즉 이 축은 "아는 사실이 옮겨지지 않는다"가 **실측된** 자리다. 그러므로 사람이
// 읽는 자리(DESIGN §3 정의줄 · §4 신호 표 · 이 표 위 주석)에 사실을 두는 것과
// 별개로, 기계가 지키는 자리를 여기 세운다. 주석 한 장을 더 늘리는 것으로는
// 같은 실패가 반복될 것이라는 근거가 이미 있다.
//
// 같은 규율의 선례: service/indexnotation_test.go(표기 규약) ·
// store/schema_table_count_test.go(선언 표 수) · store/migrate_guard_test.go(파괴적
// 조작) · service/containment_test.go. **더하는 사람의 빨간불이 켜져야 그 사람이
// 규약을 읽는다.**

// signalMeasureRes 는 "signal 로 <계측>을 하라" 류를 양방향으로 찾는다.
//
// 한 줄 안에서만 본다. 문장이 줄을 넘어가면 안 걸리는데(DESIGN.md §10 의 정본이
// 실제로 그렇다) 그것을 넓히지 않는다 — 넓히면 정상 산문의 오탐이 급격히 는다.
// 이 그물의 목적은 완전 검출이 아니라 **가장 흔한 한 줄짜리 지시문**을 잡는 것이다.
var signalMeasureRes = []*regexp.Regexp{
	regexp.MustCompile(`(signal|신호)[^\n]{0,40}(간격|분포|횟수|이력|누적|추세)`),
	regexp.MustCompile(`(간격|분포|횟수|이력|누적|추세)[^\n]{0,40}(signal|신호)`),
}

// deniesHistoryRe 는 "그 표로는 못 한다"를 말하는 줄을 통과시킨다.
//
// 이 표의 한계를 **정확히 적는 문장**은 정의상 signal 과 계측 낱말을 함께 담는다.
// 그래서 위 그물에 정본이 반드시 걸린다 — 부정 표지로 갈라내는 것이 유일한 길이다.
// 파일:행 화이트리스트를 안 쓴 이유가 이것이다: 정본이 한 줄만 밀려도 그 방식은
// 조용히 눈이 먼다(schema_table_count_test.go 가 표 이름으로 세는 것과 같은 규율).
var deniesHistoryRe = regexp.MustCompile(
	`쓸 수 없다|값이 없다|값 자체가 없다|이력이 없다|못 잰다|못 센다|존재하지 않는다|한 행이고 갱신된다`)

// signalGuardRoot 는 이 시험이 훑는 레포 루트다.
//
// store 패키지는 plugins/flightdeck/server/internal/store 에서 돈다 — 다섯 단계 위다.
func signalGuardRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "..", ".."))
	if err != nil {
		t.Fatalf("레포 루트를 못 찾았다: %v", err)
	}
	// 좌표가 밀리면 이 시험은 아무것도 안 보면서 초록이 된다. 못박아 둔다.
	for _, must := range []string{
		"plugins/flightdeck/DESIGN.md",
		"plugins/flightdeck/server/go.mod",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(must))); err != nil {
			t.Fatalf("레포 루트(%s)에 %s 가 없다 — 이 시험의 좌표가 틀렸다: %v", root, must, err)
		}
	}
	return root
}

// signalGuardInScope 는 훑을 파일인지 답한다.
//
// ★ 범위는 **살아 있는 규약 표면**이다: plugins/flightdeck 아래의 .md·.sql·.go(비시험).
//
// docs/superpowers/{plans,specs} 는 **일부러 뺐다.** 그것은 날짜가 박힌 실행 기록이지
// 앞으로 지킬 규약이 아니다. 지나간 계획서를 규약처럼 지키게 만들면 두 가지가 나쁘다:
// 기록을 고쳐야 하고(그러면 그때 무엇을 생각했는지가 사라진다), 이 관문이 과거를
// 정화하는 일에 묶여 **새로 쓰는 문장을 막는다는 본래 목적**이 흐려진다. 그 파일들의
// 틀린 전제는 같은 항목에서 개정 블록으로 따로 정정했다 — 원문은 남기고 위에 덧붙이는
// 방식이다(이 저장소가 판단·event 원장에 쓰는 "고치지 않고 새 행으로 정정한다"와 같다).
//
// 시험 파일 자신도 뺀다 — 규약을 설명하려면 위반 문장을 인용해야 한다.
func signalGuardInScope(rel string) bool {
	rel = filepath.ToSlash(rel)
	if !strings.HasPrefix(rel, "plugins/flightdeck/") {
		return false
	}
	base := filepath.Base(rel)
	if strings.HasSuffix(base, "_test.go") {
		return false
	}
	switch {
	case strings.HasSuffix(base, ".md"), strings.HasSuffix(base, ".sql"), strings.HasSuffix(base, ".go"):
		return true
	}
	return false
}

// TestSignalTableIsNotProposedAsHistory 는 살아 있는 규약 표면에서 signal 표를
// 이력·계측 재료로 지목한 문장을 전수로 찾는다.
func TestSignalTableIsNotProposedAsHistory(t *testing.T) {
	root := signalGuardRoot(t)

	var offenders []string
	var scanned, canonical int

	werr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// 워크트리·빌드 산출물로 들어가면 같은 파일을 여러 번 세고 느려진다.
			switch d.Name() {
			case ".git", ".flightdeck", "node_modules":
				return fs.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		if !signalGuardInScope(rel) {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		scanned++
		for i, ln := range strings.Split(string(b), "\n") {
			hit := false
			for _, re := range signalMeasureRes {
				if re.MatchString(ln) {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
			if deniesHistoryRe.MatchString(ln) {
				canonical++
				continue
			}
			offenders = append(offenders,
				filepath.ToSlash(rel)+":"+itoa(i+1)+"  "+strings.TrimSpace(ln))
		}
		return nil
	})
	if werr != nil {
		t.Fatalf("전수 훑기가 실패했다: %v", werr)
	}

	// 눈을 뜨고 있는지 본다. 범위나 정규식이 밀리면 offenders 0 은 "깨끗하다"가
	// 아니라 "아무것도 안 봤다"다 — 둘은 화면에서 구분되지 않는다.
	if scanned == 0 {
		t.Fatalf("범위 안 파일을 한 개도 못 읽었다 — 이 시험이 아무것도 안 보고 있다")
	}
	if canonical == 0 {
		t.Fatalf("정본 문장을 한 줄도 못 봤다(파일 %d개를 훑었다) — 그물이나 좌표가 밀렸다. "+
			"judge/prescribe.go 의 'tool 신호 횟수는 쓸 수 없다' 절이 걸려야 정상이다", scanned)
	}

	if len(offenders) > 0 {
		t.Errorf("signal 표를 이력·계측 재료로 지목한 자리가 %d 곳이다(파일 %d개를 훑었다):\n  %s\n\n"+
			"signal 의 PK 는 (session_id, kind) 다 — 세션×종류마다 한 행이고 갱신되므로 "+
			"횟수·간격·분포·과거 시점이라는 값이 존재하지 않는다(schema.sql:91-96).\n"+
			"그 재료는 event(kind='session.beat') 뿐이다 — 추가 전용이고 스로틀이 없다.\n"+
			"한계를 적는 문장이라면 그 줄에 '값이 없다'·'쓸 수 없다'처럼 부정을 함께 적어라.",
			len(offenders), scanned, strings.Join(offenders, "\n  "))
	}
}

// TestSignalGuardActuallyCatches 는 위 그물이 실제로 무엇을 잡고 무엇을 통과시키는지
// 못박는다. 전수 시험은 레포가 깨끗하면 초록이라, 그물이 죽어도 그것만으로는 안 보인다.
func TestSignalGuardActuallyCatches(t *testing.T) {
	caught := []string{
		// 이 사고를 실제로 낳은 문장이다(큐 항목 fd-live-window-baseline 본문).
		`signal 표로 '마지막 신호 후 다음 신호까지의 간격 분포'를 재서`,
		`title="보드 생존 창의 근거를 signal 간격 분포로 만든다",`,
		`겹치는 빈도를 신호 간격으로 근사하면`,
		`signal 표에서 tool 횟수를 세면 된다`,
		`간격 분포는 signal 표에 있다`,
	}
	for _, s := range caught {
		if signalGuardOffends(s) {
			continue
		}
		t.Errorf("그물이 놓쳤다: %q", s)
	}

	passed := []string{
		// 정본 셋 — 전부 signal 과 계측 낱말을 함께 담지만 한계를 말한다.
		"// ★ 그리고 **`tool` 신호 횟수는 쓸 수 없다.** signal 표의 PK 가 (session_id, kind) 라",
		"**신호 표는 종류별로 한 행이고 갱신된다 — `tool` 신호 \"횟수\"라는 값이 없다.**",
		"`signal` 표는 PK 가 (session_id, kind) 라 종류별 한 행이고 갱신되므로 이력이 없다",
		// 계측과 무관한 정상 산문.
		"- `signal` — 생존 \"사실\"이지 판정이 아니다.",
		"마지막 신호와 나이가 온다",
	}
	for _, s := range passed {
		if !signalGuardOffends(s) {
			continue
		}
		t.Errorf("그물이 정상 문장을 잡았다: %q", s)
	}
}

// signalGuardOffends 는 한 줄이 위반인지 답한다(전수 시험과 같은 판정이다).
func signalGuardOffends(ln string) bool {
	for _, re := range signalMeasureRes {
		if !re.MatchString(ln) {
			continue
		}
		return !deniesHistoryRe.MatchString(ln)
	}
	return false
}

// itoa 는 strconv 를 끌어오지 않으려는 것이 아니라, 이 파일이 쓰는 유일한 변환이라
// 의존을 한 줄로 두려는 것이다.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
