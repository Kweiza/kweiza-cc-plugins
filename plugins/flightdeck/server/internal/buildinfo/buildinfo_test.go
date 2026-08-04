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
