package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// binNow 는 표의 기준 시각이다. 실제 시각을 안 쓴다 — 시험이 지금 시각에 기대면
// 시각 해상도가 낮은 자리에서 동률이 우연히 생겼다 없어졌다 한다.
var binNow = time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)

// binAt 은 표를 짧게 쓰기 위한 것이다. ageMin 이 클수록 옛것이다.
func binAt(dir, name string, ageMin int) BinEntry {
	return BinEntry{
		Path:    filepath.Join(dir, name),
		ModTime: binNow.Add(-time.Duration(ageMin) * time.Minute),
	}
}

// PruneBinCache 는 **지울 것만** 낸다. 이 표가 잠그는 것은 세 규칙이다:
// 접두가 아닌 것은 절대 안 지운다 · 최신 keep 벌은 남는다 · self 는 keep 밖이어도 남는다.
func TestPruneBinCache(t *testing.T) {
	const dir = "/h/.cache/flightdeck/bin"
	p := func(name string) string { return filepath.Join(dir, name) }

	cases := []struct {
		name    string
		entries []BinEntry
		keep    int
		self    string
		want    []string
	}{
		{
			// ★ 이 갈래가 이 함수의 가장 무거운 계약이다. FD_STATE_DIR 를 켠 사용자는
			//    이 디렉토리를 자기 것과 나눠 쓸 수 있고, 옛 이름(`fd`)은 접두가 아니라
			//    남는다 — 옛 자리를 지우지도 옮기지도 않는다는 결정과 같은 자리다.
			name: "fd- 접두가 아닌 것은 아무리 낡아도 대상이 아니다",
			entries: []BinEntry{
				binAt(dir, "fd-%2fa", 0),
				binAt(dir, "fd-%2fb", 10),
				binAt(dir, "fd-%2fc", 20),
				binAt(dir, "fd-%2fd", 30),
				binAt(dir, "fd", 9999),        // 옛 런처가 짓던 이름
				binAt(dir, "notes.txt", 9999), // 남의 파일
				binAt(dir, "README", 9999),
				binAt(dir, "fdx-%2fe", 9999), // 접두가 `fd-` 가 아니다
			},
			keep: 3, self: "",
			want: []string{p("fd-%2fd")},
		},
		{
			name: "mtime 최신 keep 벌만 남는다",
			entries: []BinEntry{
				binAt(dir, "fd-a", 0),
				binAt(dir, "fd-b", 1),
				binAt(dir, "fd-c", 2),
				binAt(dir, "fd-d", 3),
				binAt(dir, "fd-e", 4),
			},
			keep: 2, self: "",
			want: []string{p("fd-c"), p("fd-d"), p("fd-e")},
		},
		{
			// 입력 순서가 mtime 순서와 무관해도 답은 나이로 정해진다.
			name: "입력 순서가 아니라 나이로 고른다",
			entries: []BinEntry{
				binAt(dir, "fd-old", 99),
				binAt(dir, "fd-new", 0),
				binAt(dir, "fd-mid", 5),
			},
			keep: 2, self: "",
			want: []string{p("fd-old")},
		},
		{
			// ★ 지금 도는 자리는 방금 쓰인 자리라 곧 또 쓰인다. 지우면 다음 훅이
			//    같은 것을 다시 짓는다 — 22MB 를 아끼려고 1초를 태우는 교환이다.
			name: "self 는 keep 밖이어도 유지한다",
			entries: []BinEntry{
				binAt(dir, "fd-a", 0),
				binAt(dir, "fd-b", 1),
				binAt(dir, "fd-c", 2),
				binAt(dir, "fd-d", 3),
			},
			keep: 2, self: p("fd-d"),
			want: []string{p("fd-c")},
		},
		{
			// GC 가 한 번 돌고 나면 이 프로세스 자신이 지워진 자리를 돌 수 있다.
			// 그때 os.Executable 은 커널 표식이 붙은 경로를 준다 — 그대로 비교하면
			// self 보호가 아무 신호 없이 꺼진다.
			name: "self 에 (deleted) 표식이 붙어도 유지한다",
			entries: []BinEntry{
				binAt(dir, "fd-a", 0),
				binAt(dir, "fd-b", 1),
				binAt(dir, "fd-c", 2),
				binAt(dir, "fd-d", 3),
			},
			keep: 2, self: p("fd-d") + deletedSuffix,
			want: []string{p("fd-c")},
		},
		{
			name:    "빈 입력",
			entries: nil,
			keep:    3, self: p("fd-a"),
			want: nil,
		},
		{
			name: "keep 이 항목 수보다 크면 아무것도 안 지운다",
			entries: []BinEntry{
				binAt(dir, "fd-a", 0),
				binAt(dir, "fd-b", 1),
			},
			keep: 5, self: "",
			want: nil,
		},
		{
			// mtime 이 같은 여러 벌이 실제로 생긴다(한 훅이 연달아 짓고, 파일시스템에 따라
			// 해상도가 1초다). 순서를 안 정해 두면 같은 입력이 실행마다 다른 답을 낸다.
			name: "mtime 동률은 경로 오름차순으로 깬다",
			entries: []BinEntry{
				binAt(dir, "fd-c", 0),
				binAt(dir, "fd-a", 0),
				binAt(dir, "fd-b", 0),
			},
			keep: 1, self: "",
			want: []string{p("fd-b"), p("fd-c")},
		},
		{
			name: "keep 0 이면 self 만 남는다",
			entries: []BinEntry{
				binAt(dir, "fd-a", 0),
				binAt(dir, "fd-b", 1),
			},
			keep: 0, self: p("fd-b"),
			want: []string{p("fd-a")},
		},
		{
			// ★ 여기서 거는 계약은 **답**이다 — "음수 keep 은 keep 0 과 같은 답(전부 삭제)".
			//    위 갈래의 `keep 0 이면 self 만 남는다` 와 짝이다. 구현 쪽 가드
			//    (bincache.go 의 `if keep < 0`)를 잠그지는 **못한다**: 지금 비교가 `i < keep`
			//    라 가드를 지워도 답이 같아서 이 줄이 초록이다(뮤테이션으로 확인했다).
			//    앞서 여기 적혀 있던 "'전부 유지'로 뒤집힌다"는 방향이 거꾸로였다 —
			//    음수가 가는 쪽은 전부 유지가 아니라 전부 삭제다.
			name:    "음수 keep 은 keep 0 과 같은 답을 낸다(전부 삭제)",
			entries: []BinEntry{binAt(dir, "fd-a", 0)},
			keep:    -1, self: "",
			want: []string{p("fd-a")},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := PruneBinCache(c.entries, c.keep, c.self)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("지울 목록이\n  %v\n인데\n  %v\n여야 한다", got, c.want)
			}
			// 표현도 계약이다 — 지울 것이 없으면 **nil** 이다. 빈 슬라이스와 섞어 내면
			// DeepEqual 로 단정하는 시험이 내용이 아니라 표현 때문에 갈린다.
			if len(c.want) == 0 && got != nil {
				t.Fatalf("지울 것이 없는데 nil 이 아니다: %#v", got)
			}
		})
	}
}

// 같은 입력을 어떤 순서로 줘도 같은 답이어야 한다.
//
// os.ReadDir 는 이름순으로 주지만 그건 어댑터의 사정이고, 이 함수의 계약은 아니다.
// 순서에 답이 흔들리면 매 훅마다 다른 것이 지워지고 그 사실이 어디에도 안 뜬다.
func TestPruneBinCacheIsOrderIndependent(t *testing.T) {
	const dir = "/h/.cache/flightdeck/bin"
	base := []BinEntry{
		binAt(dir, "fd-a", 0),
		binAt(dir, "fd-b", 1),
		binAt(dir, "fd-c", 2),
		binAt(dir, "fd-d", 2), // 동률
		binAt(dir, "fd-e", 9),
	}
	want := PruneBinCache(base, 2, "")

	rev := make([]BinEntry, 0, len(base))
	for i := len(base) - 1; i >= 0; i-- {
		rev = append(rev, base[i])
	}
	if got := PruneBinCache(rev, 2, ""); !reflect.DeepEqual(got, want) {
		t.Fatalf("입력 순서가 답을 바꾼다: %v vs %v", got, want)
	}
}

// PruneBinCache 는 **입력을 안 망가뜨린다.** 호출부가 같은 슬라이스를 다시 쓰거나
// (진단 표시 등) 두 번 부를 수 있는데, 그때 답이 달라지면 원인을 못 찾는다.
func TestPruneBinCacheDoesNotMutateInput(t *testing.T) {
	const dir = "/h/.cache/flightdeck/bin"
	in := []BinEntry{
		binAt(dir, "fd-c", 2),
		binAt(dir, "fd-a", 0),
		binAt(dir, "fd-b", 1),
	}
	before := append([]BinEntry(nil), in...)
	first := PruneBinCache(in, 1, "")
	if !reflect.DeepEqual(in, before) {
		t.Fatalf("입력 슬라이스가 뒤바뀌었다: %v → %v", before, in)
	}
	if second := PruneBinCache(in, 1, ""); !reflect.DeepEqual(first, second) {
		t.Fatalf("두 번 부르니 답이 다르다: %v vs %v", first, second)
	}
}

// 어댑터는 **판정을 안 한다.** 거르는 것은 '판정할 수 없는 것'(디렉토리)뿐이고,
// 디렉토리가 아예 없는 것은 결함이 아니다(아직 아무것도 안 지었다는 뜻이다).
func TestReadBinCacheReadsWithoutJudging(t *testing.T) {
	dir := t.TempDir()

	// 접두가 아닌 것도 **그대로 올린다** — 무엇을 지울지는 PruneBinCache 만 정한다.
	for _, name := range []string{"fd-%2fa", "fd-%2fb", "fd", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o755); err != nil {
			t.Fatalf("준비 실패(%s): %v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("준비 실패(sub): %v", err)
	}

	got, err := readBinCache(dir)
	if err != nil {
		t.Fatalf("읽기 실패: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("항목이 %d개다 — 파일 4개만 올라야 한다(디렉토리는 뺀다): %v", len(got), got)
	}
	for _, e := range got {
		if filepath.Base(e.Path) == "sub" {
			t.Fatalf("디렉토리가 목록에 들어갔다 — 남의 트리를 재귀로 지울 뻔했다: %v", got)
		}
		if filepath.Dir(e.Path) != dir {
			t.Fatalf("경로가 절대 경로로 안 조립됐다(%q) — 그대로 os.Remove 에 넘어간다", e.Path)
		}
		if e.ModTime.IsZero() {
			t.Fatalf("mtime 이 비었다(%q) — 나이 판정이 통째로 무의미해진다", e.Path)
		}
	}
}

// 디렉토리가 없는 것은 **결함이 아니다**. 오류로 올리면 훅이 매번 Debug 줄을 뱉고,
// 정작 진짜 실패(권한 등)와 구분이 안 된다.
func TestReadBinCacheTreatsMissingDirAsEmpty(t *testing.T) {
	got, err := readBinCache(filepath.Join(t.TempDir(), "없는자리"))
	if err != nil {
		t.Fatalf("없는 디렉토리를 오류로 올렸다: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("없는 디렉토리에서 항목이 나왔다: %v", got)
	}
}

// ★ 훅이 이 GC 를 **실제로 부르는가.** 좌표계는 파일시스템이다.
//
// 위 시험들은 판정(PruneBinCache)과 어댑터(readBinCache)를 잠그지만, 그 둘이 아무 데서도
// 안 불려도 전부 초록이다 — 단위 시험은 소비자가 그 값을 쓰는지를 원리적으로 못 본다.
// 실제로 hook.go 의 `a.pruneBinCache()` **한 줄만** 지우면 cmd/fd 가 통째로 초록이었다.
// 그 줄이 리베이스에서 떨어져 나가면 22MB×N 이 상한 없이 다시 쌓이고(이 파일 머리말),
// 실패를 Debug 로만 남기는 설계라 화면에도 로그에도 아무 신호가 없다. doctor 도 자리와
// 사유만 찍지 항목 수·용량은 안 찍으므로, 침묵으로 사는 회귀다.
//
// ★ **형제에게는 선례가 없다.** 바로 위 줄인 `a.pruneWindows()`(hook.go)도 훅 경로에서
// 아무도 안 잰다 — 저장소 전체에 window.Prune 을 훅 너머로 단정하는 시험이 없다. 즉 이
// 시험은 선례를 따른 것이 아니라 **이 부류의 첫 자리**다. 여기 먼저 두는 이유는 bincache.go
// 가 파일 하나째로 그 한 줄을 위해 존재하기 때문이고, 형제 쪽은 후속으로 남긴다.
//
// ★ 진짜 홈을 안 건드린다: 하네스가 FD_STATE_DIR·HOME 을 가짜로 못박으므로 binDir 은
// `h.state/bin` 이다. 그 전제를 결과보다 **먼저** 단정한다 — 안 그러면 자리가 어긋나
// 아무것도 안 지워진 초록과 "GC 가 돌았다"가 구분이 안 된다.
func TestSessionStartHookPrunesBinCache(t *testing.T) {
	h := newHarness(t)
	cwd := t.TempDir()

	binDir := filepath.Join(h.state, "bin")
	if got, src := BinCacheDir(envOf(h.env), h.home); got != binDir {
		t.Fatalf("대조 전제가 깨졌다 — BinCacheDir=%q(%s), 심는 자리는 %q", got, src, binDir)
	}
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatalf("가짜 바이너리 캐시를 못 만들었다: %v", err)
	}

	// 옛것 → 새것. keep=3 이므로 앞의 둘이 대상이다.
	base := time.Now().Add(-24 * time.Hour)
	old := []string{"fd-a", "fd-b"}
	fresh := []string{"fd-c", "fd-d", "fd-e"}
	for i, name := range append(append([]string{}, old...), fresh...) {
		p := filepath.Join(binDir, name)
		if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
			t.Fatalf("심기 실패(%s): %v", name, err)
		}
		mt := base.Add(time.Duration(i) * time.Hour)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatalf("mtime 설정 실패(%s): %v", name, err)
		}
	}
	// 접두가 아닌 것 둘 — 옛 이름(`fd`)과 남의 파일. 이 GC 는 청소지 파괴가 아니다.
	// 훅을 거쳐도 그 판정이 그대로 서는지를 여기서 함께 잰다.
	others := []string{"fd", "남의파일"}
	for _, name := range others {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("심기 실패(%s): %v", name, err)
		}
	}

	if code, out := h.run(sessionStartPayload("cc-session-uuid-1", cwd), "hook", "session-start"); code != 0 {
		t.Fatalf("SessionStart 훅 종료코드 %d:\n%s", code, out)
	}

	for _, name := range old {
		if _, err := os.Stat(filepath.Join(binDir, name)); err == nil {
			t.Errorf("%s 가 남았다 — 훅이 GC 를 안 불렀다(keep=%d). "+
				"그 한 줄이 없으면 22MB×N 이 상한 없이 쌓이고 아무 신호도 안 난다",
				name, binCacheKeep)
		}
	}
	for _, name := range append(append([]string{}, fresh...), others...) {
		if _, err := os.Stat(filepath.Join(binDir, name)); err != nil {
			t.Errorf("%s 가 사라졌다 — 청소가 아니라 파괴다: %v", name, err)
		}
	}
}
