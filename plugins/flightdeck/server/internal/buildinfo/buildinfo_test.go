package buildinfo

import (
	"runtime/debug"
	"strings"
	"testing"
)

func bi(kv ...string) *debug.BuildInfo {
	b := &debug.BuildInfo{}
	for i := 0; i+1 < len(kv); i += 2 {
		b.Settings = append(b.Settings, debug.BuildSetting{Key: kv[i], Value: kv[i+1]})
	}
	return b
}

func TestOfReadsVCSSettings(t *testing.T) {
	c := Of(bi("vcs", "git",
		"vcs.revision", "07e5df4264f27a4b1bcac34b2dcd50cd76d51e2e",
		"vcs.time", "2026-08-04T23:24:26Z",
		"vcs.modified", "false"), true)

	if !c.Known {
		t.Fatalf("Known 이 거짓이다 — 사유 %q", c.Reason)
	}
	if c.Revision != "07e5df4264f27a4b1bcac34b2dcd50cd76d51e2e" || c.Time != "2026-08-04T23:24:26Z" {
		t.Fatalf("좌표를 잘못 읽었다: %+v", c)
	}
	if c.Modified {
		t.Fatal("vcs.modified=false 인데 참으로 읽었다")
	}
}

// ★ 이 갈래가 실측이다 — `.claude/plugins/data/.../bin/fd` 에 `build vcs=…` 줄이 하나도 없었다.
// 좌표 없음을 **빈 값이 아니라 사유**로 낸다. 빈 값이면 "같다"로 읽힌다.
func TestOfWithoutVCSSaysWhy(t *testing.T) {
	for _, tc := range []struct {
		name string
		bi   *debug.BuildInfo
		ok   bool
	}{
		{"스탬프 없음", bi("GOARCH", "amd64"), true},
		{"revision 만 없음", bi("vcs", "git", "vcs.time", "2026-08-04T23:24:26Z"), true},
		{"읽기 실패", nil, false},
		{"nil 인데 ok", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Of(tc.bi, tc.ok)
			if c.Known {
				t.Fatalf("좌표가 없는데 Known 이 참이다: %+v", c)
			}
			if strings.TrimSpace(c.Reason) == "" {
				t.Fatal("사유가 비었다 — 침묵하면 '같다'로 읽힌다")
			}
			if got := Short(c); got != c.Reason {
				t.Fatalf("Short 이 사유를 안 냈다: %q", got)
			}
		})
	}
}

// ★ 제로값 Coord — 이 축을 안 내는 서버의 응답을 역직렬화하면 이것이 나온다.
// Of 를 거치지 않으므로 Reason 이 비어 있고, 그대로 찍으면 화면에 빈칸이 남는다.
// **실물 관측에서 `서버 판 ` 이 빈칸으로 찍혀 잡았다** — 단위 시험은 Of 가 만든 값만 보고 있었다.
func TestZeroCoordStillSaysSomething(t *testing.T) {
	var c Coord // json 에 "build" 키가 없을 때 나오는 바로 그 값
	got := Short(c)
	if strings.TrimSpace(got) == "" {
		t.Fatal("제로값이 빈칸을 냈다 — 이 침묵이 이 항목이 고치려는 것이다")
	}
	if !strings.Contains(got, "없다") {
		t.Fatalf("부재를 말하지 않는다: %q", got)
	}
}

func TestVintageBannerFiresOnlyWhenTheyDiffer(t *testing.T) {
	a := Of(bi("vcs.revision", "aaaaaaa1111", "vcs.time", "2026-08-04T00:00:00Z"), true)
	b := Of(bi("vcs.revision", "bbbbbbb2222", "vcs.time", "2026-08-05T00:00:00Z"), true)
	unknown := Of(bi(), true)

	if got := VintageBanner(a, a); got != "" {
		t.Fatalf("같은 좌표인데 배너가 났다: %q", got)
	}
	if got := VintageBanner(unknown, unknown); got != "" {
		t.Fatalf("양쪽 다 모르는데 배너가 났다 — 낼 말이 없다: %q", got)
	}

	// ★ 이 시험이 이 항목의 본체다. api_version 이 같아도(대조에 안 들어간다) 판 나이는 갈릴 수 있다.
	got := VintageBanner(a, b)
	if got == "" {
		t.Fatal("좌표가 다른데 배너가 안 났다 — 이 침묵이 이 항목이 고치려는 것이다")
	}
	if !strings.Contains(got, "aaaaaaa") || !strings.Contains(got, "bbbbbbb") {
		t.Fatalf("배너가 두 값을 다 안 냈다: %q", got)
	}

	// 한쪽만 모르는 갈래도 침묵하지 않는다 — 대조가 성립 안 한다는 사실 자체가 정보다.
	if got := VintageBanner(a, unknown); !strings.Contains(got, "서버") {
		t.Fatalf("서버 좌표 부재를 안 알렸다: %q", got)
	}
	if got := VintageBanner(unknown, b); !strings.Contains(got, "bbbbbbb") {
		t.Fatalf("클라이언트 좌표 부재 갈래가 서버 값을 안 냈다: %q", got)
	}
}

func TestShortIsSevenChars(t *testing.T) {
	if got := ShortRev("07e5df4264f27a4b1bcac34b2dcd50cd76d51e2e"); got != "07e5df4" {
		t.Fatalf("ShortRev = %q", got)
	}
	if got := ShortRev("abc"); got != "abc" {
		t.Fatalf("짧은 값을 잘랐다: %q", got)
	}
}

func TestModifiedIsShown(t *testing.T) {
	c := Of(bi("vcs.revision", "deadbeefcafe", "vcs.modified", "true"), true)
	if !strings.Contains(Short(c), "커밋 안 된") {
		t.Fatalf("더러운 빌드를 안 알렸다: %q", Short(c))
	}
}

// Self 는 내용을 못 시험한다(시험 바이너리 자신의 정보가 온다). 죽지 않는 것만 본다.
func TestSelfDoesNotPanic(t *testing.T) {
	c := Self()
	if !c.Known && strings.TrimSpace(c.Reason) == "" {
		t.Fatal("Self 가 모른다면서 사유가 없다")
	}
}

// ★ 이 시험이 주입 축의 본체다 — **실측(2026-08-06)이 이 갈래를 강제했다.**
// 워크트리(부모 저장소 안)에서 빌드하면 go 는 부모의 HEAD 를 찍는다. 관측:
// 워크트리 HEAD `5144c66` 위에서 빌드했는데 스탬프는 `3f7b497`(부모 main tip)이었다.
// **부재가 아니라 그럴듯한 오답이라, 주입값이 있으면 그것이 이겨야 한다.**
func TestResolvePrefersInjectedOverAWrongGitStamp(t *testing.T) {
	// go 가 찍은 것 — 부모 HEAD 다. 값이 있고 Known 이라 그대로 두면 오답이 이긴다.
	wrong := Of(bi("vcs.revision", "3f7b497952a7d92e8fbf66cbf95552cbc7790d03",
		"vcs.time", "2026-08-06T07:09:34Z", "vcs.modified", "true"), true)
	if !wrong.Known {
		t.Fatal("전제가 깨졌다 — go 스탬프가 Known 이어야 이 갈래가 성립한다")
	}

	c := Resolve("6190af7d27d25d15390df31ddfd0b69366", "2026-08-06T11:40:00Z", wrong)

	if !c.Known {
		t.Fatalf("주입값이 있는데 모른다고 한다 — 사유 %q", c.Reason)
	}
	if c.Source != SourceFingerprint {
		t.Fatalf("출처가 지문이 아니다: %q", c.Source)
	}
	if strings.HasPrefix(c.Revision, "3f7b497") {
		t.Fatal("go 의 오답이 주입값을 이겼다 — 이 역전이 이 항목의 본체다")
	}
	if c.Revision != "6190af7d27d25d15390df31ddfd0b69366" || c.Time != "2026-08-06T11:40:00Z" {
		t.Fatalf("주입값을 잘못 실었다: %+v", c)
	}
	// 지문은 작업 트리의 **내용**에서 나온다 — 부모 저장소의 더러움을 물려받지 않는다.
	if c.Modified {
		t.Fatal("지문 좌표가 go 의 vcs.modified 를 물려받았다")
	}
}

// 주입이 없으면 go 스탬프가 그대로 간다 — 손빌드가 좌표를 잃지 않는다.
func TestResolveFallsBackToTheGitStamp(t *testing.T) {
	git := Of(bi("vcs.revision", "aaaaaaa1111", "vcs.time", "2026-08-04T00:00:00Z"), true)

	for _, fp := range []string{"", "   ", "\n"} {
		c := Resolve(fp, "", git)
		if c.Source != SourceGit || c.Revision != "aaaaaaa1111" {
			t.Fatalf("주입값 %q 일 때 go 스탬프로 안 떨어졌다: %+v", fp, c)
		}
	}
}

// 둘 다 없으면 — 주입도 안 됐고 VCS 스탬프도 없다 — 사유를 낸다. 빈칸은 "같다"로 읽힌다.
func TestResolveWithNeitherStillSaysWhy(t *testing.T) {
	c := Resolve("", "", Of(bi("GOARCH", "amd64"), true))
	if c.Known {
		t.Fatalf("아무 좌표도 없는데 Known 이다: %+v", c)
	}
	if strings.TrimSpace(Short(c)) == "" {
		t.Fatal("사유가 비었다")
	}
}

// ★ 지문을 sha 로 **오독하면 안 된다.** 둘은 뜻이 다르다 — sha 는 커밋을 가리키고
// 지문은 소스 내용을 가리킨다. 화면에서 구별되지 않으면 사람은 `git show` 를 치러 간다.
func TestShortMarksAFingerprintAsNotASha(t *testing.T) {
	fp := Resolve("6190af7d27d25d15390df31ddfd0b69366", "2026-08-06T11:40:00Z", Coord{})
	got := Short(fp)
	if !strings.Contains(got, "src:") {
		t.Fatalf("지문이 sha 처럼 보인다 — 표시에 출처가 없다: %q", got)
	}
	if !strings.Contains(got, "6190af7") {
		t.Fatalf("지문 값을 안 냈다: %q", got)
	}

	git := Of(bi("vcs.revision", "07e5df4264f27a4b1bcac34b2dcd50cd76d51e2e"), true)
	if strings.Contains(Short(git), "src:") {
		t.Fatalf("git 좌표에 지문 표시가 붙었다: %q", Short(git))
	}
}

// ★ 출처가 다르면 **대조 자체가 성립하지 않는다.** git sha 와 소스 지문은 같은 소스에서도
// 절대 같은 값이 안 나온다 — 그대로 비교하면 배너가 **항상** 뜨고, 항상 뜨는 경고는 배경이 된다.
func TestVintageBannerRefusesToCompareAcrossSources(t *testing.T) {
	git := Of(bi("vcs.revision", "aaaaaaa1111"), true)
	fp := Resolve("6190af7d27d25d15390df31ddfd0b69366", "", Coord{})

	got := VintageBanner(fp, git)
	if got == "" {
		t.Fatal("출처가 어긋났는데 침묵했다 — 대조 불가는 그 자체가 정보다")
	}
	if strings.Contains(got, "판 나이가 다르다") {
		t.Fatalf("비교할 수 없는 두 값을 비교했다고 말한다: %q", got)
	}
	if !strings.Contains(got, "출처") {
		t.Fatalf("무엇이 어긋났는지 안 밝혔다: %q", got)
	}

	// 같은 출처끼리는 종전대로 — 같으면 침묵, 다르면 배너.
	if got := VintageBanner(fp, fp); got != "" {
		t.Fatalf("같은 지문인데 배너가 났다: %q", got)
	}
	other := Resolve("ffffffff0000000000000000000000000", "", Coord{})
	if got := VintageBanner(fp, other); !strings.Contains(got, "판 나이가 다르다") {
		t.Fatalf("지문끼리 다른데 판 나이 배너가 안 났다: %q", got)
	}
}

// ★ 구 판 서버의 응답에는 `source` 키가 없다. 그 좌표는 정의상 go 스탬프다
// (주입 축 이전 판에는 다른 출처가 존재하지 않았다). 빈 출처를 제3의 출처로 취급하면
// 멀쩡한 대조가 "출처 어긋남"으로 죽는다.
func TestEmptySourceCountsAsGit(t *testing.T) {
	old := Coord{Known: true, Revision: "aaaaaaa1111"} // json 에 source 가 없던 판
	now := Of(bi("vcs.revision", "aaaaaaa1111"), true)

	if got := VintageBanner(now, old); got != "" {
		t.Fatalf("같은 sha 인데 빈 출처 때문에 배너가 났다: %q", got)
	}
}

// 부재 사유는 **가장 흔한 실제 원인**을 먼저 말해야 한다. 실측(2026-08-06):
// 부재를 만드는 것은 워크트리가 아니라 `.git` 이 없는 소스 트리다 —
// 플러그인 캐시와 컨테이너 빌드 컨텍스트가 그렇고, 그 둘이 사용자 전원의 경로다.
// (워크트리는 오히려 스탬프가 **있다** — 부모 HEAD 라 틀릴 뿐이다.)
func TestNoVCSReasonNamesTheRealCause(t *testing.T) {
	got := Short(Of(bi("GOARCH", "amd64"), true))
	for _, want := range []string{".git", "주입"} {
		if !strings.Contains(got, want) {
			t.Fatalf("사유가 %q 를 안 말한다: %q", want, got)
		}
	}
}
