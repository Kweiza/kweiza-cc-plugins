package mcpsrv

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// 이 파일은 fd-unread-axes-have-no-names-on-screen 항목의 본체다.
//
// 실측(2026-09-17): `board`(간단 화면)가 "못 읽은 축 2개"만 내고 이름은 detail=true 로
// 미뤘는데, 정작 그 안내를 받은 세션은 board --detail 로도 못 찾아 status --help →
// 소스 읽기 → dashboard.json curl 을 거쳐서야 "세션 하나가 유령 워크트리를 가리킨다"에
// 닿았다. 그래서 간단 화면에도 이름을 낸다 — 다만 상한을 둔다(FailureAxisBriefLimit).
//
// 이 파일은 **그 변경이 닿는 자리**(RenderBoard 의 비-detail 절)만 겨눈다. 접기·200자
// 절단 자체의 시험은 render_failures_fold_test.go 가 RenderPick 을 통해 이미 갖고 있다 —
// 여기서 같은 것을 RenderBoard 로 다시 재지 않는다(사본을 단정하면 변이가 샌다).

// briefBoard 는 세션 없이 파생 실패만 실은 최소 보드다. 카드·겹침 계산을 안 태우고
// "간단 화면의 파생 실패 절" 하나만 겨눈다.
func briefBoard(fs []service.DerivedFailure) service.BoardView {
	return service.BoardView{
		Project: model.Project{ID: "axis-sample"},
		At:      t0, Window: 8 * time.Hour,
		Derived: service.Derived{
			Freshness: model.Freshness{Source: "git", ObservedAt: t0},
			Failures:  fs,
		},
	}
}

// TestRenderBoardBriefNamesFailedAxis 는 요구 ⑴이다 — 실패가 있으면 축 이름이
// **간단 화면**(Detail 안 준 기본값)에도 난다. 예전에는 "detail=true 로 축 이름과
// 원인을 본다"는 안내만 내고 이름 자체는 없었다.
func TestRenderBoardBriefNamesFailedAxis(t *testing.T) {
	v := briefBoard([]service.DerivedFailure{
		{Axis: "widget-axis-zzz", Detail: "위젯 원인을 못 읽었다"},
	})
	got := RenderBoard(v, BoardRenderOptions{Now: t0})

	if !strings.Contains(got, "widget-axis-zzz") {
		t.Fatalf("간단 화면에 축 이름이 없다 — 여전히 detail=true 로 미룬다:\n%s", got)
	}
	if !strings.Contains(got, "위젯 원인을 못 읽었다") {
		t.Fatalf("간단 화면에 원인이 없다:\n%s", got)
	}
}

// TestRenderBoardBriefSilentWhenNoFailures 는 요구 ⑵다 — 실패가 없으면 파생 실패 절이
// 통째로 없어야 한다. "축 이름을 낸다"는 변경이 실패 0건인 흔한 경우까지 침묵을 깨면
// 안 된다.
func TestRenderBoardBriefSilentWhenNoFailures(t *testing.T) {
	v := briefBoard(nil)
	got := RenderBoard(v, BoardRenderOptions{Now: t0})

	if strings.Contains(got, "못 읽은") {
		t.Fatalf("실패가 0건인데 못 읽은 축 절이 붙었다:\n%s", got)
	}
}

// TestRenderBoardBriefCapsAtLimitAndSaysHowManyMore 는 요구 ⑶이다 — 상한
// (FailureAxisBriefLimit)을 넘으면 이름을 전부 펼치지 않고 "…N줄 더"로 접는다.
// 세션 다수가 한꺼번에 실패해도 간단 화면이 그 하나 때문에 무한정 길어지지 않는다.
func TestRenderBoardBriefCapsAtLimitAndSaysHowManyMore(t *testing.T) {
	var fs []service.DerivedFailure
	// 서로 접히지 않는 축(uncommitted 쌍이 아니다) 아홉 개 — 상한(6)을 셋 넘긴다.
	for i := 0; i < 9; i++ {
		fs = append(fs, service.DerivedFailure{
			Axis: fmt.Sprintf("solo-axis-%d", i), Detail: "원인",
		})
	}
	v := briefBoard(fs)
	got := RenderBoard(v, BoardRenderOptions{Now: t0})

	if !strings.Contains(got, "못 읽은 파생 9축:") {
		t.Fatalf("축 수 9가 머리줄에 없다:\n%s", got)
	}
	if strings.Contains(got, "solo-axis-8") {
		t.Fatalf("상한(%d)을 넘은 이름까지 펼쳤다 — 간단 화면이 무한정 길어질 수 있다:\n%s",
			FailureAxisBriefLimit, got)
	}
	if !strings.Contains(got, "3줄 더") {
		t.Fatalf("상한을 넘겼는데 남은 수를 안 말한다:\n%s", got)
	}
}

// TestRenderFailureAxesClipsLongDetailAndMarksIt 는 요구 ⑷다 — 사유(Detail)가 길면
// 화면에서 잘리고, 잘렸다는 사실이 남는다(말줄임표). 전문은 dashboard.json 이 낸다
// (JSON 은 이 절단을 안 거친다 — service.Derived 자체는 안 바꾼다).
//
// RenderFailureAxes(cmd/fd 가 부르는 새 export)로 직접 겨눈다 — `fd open` 이 실제로
// 이 함수를 부른다.
func TestRenderFailureAxesClipsLongDetailAndMarksIt(t *testing.T) {
	long := strings.Repeat("가", 300) // 200자 상한을 넘긴다. clip 이 룬 단위로 잰다.
	d := service.Derived{Failures: []service.DerivedFailure{
		{Axis: "solo-axis-long", Detail: long},
	}}
	lines := RenderFailureAxes(d, 0)
	joined := strings.Join(lines, "\n")

	if strings.Contains(joined, long) {
		t.Fatalf("300자 원인을 안 자르고 그대로 냈다 — 절단이 없다:\n%s", joined)
	}
	if !strings.Contains(joined, "…") {
		t.Fatalf("잘렸는데 잘렸다는 표시(말줄임표)가 없다:\n%s", joined)
	}
}

// TestRenderBoardBriefFailureNamesDoNotLeakIntoFirstLine 는 요구 ⑸다 — 첫 줄
// ("보드 · … · 파생 …")의 형식은 그대로다. 이름은 **다음 줄(들)**에만 붙는다.
// 이 형식은 여러 시험과 훅·배너가 그대로 단정하므로 여기서 흔들리면 안 된다.
func TestRenderBoardBriefFailureNamesDoNotLeakIntoFirstLine(t *testing.T) {
	v := briefBoard([]service.DerivedFailure{
		{Axis: "widget-axis-zzz", Detail: "위젯 원인을 못 읽었다"},
	})
	got := RenderBoard(v, BoardRenderOptions{Now: t0})

	first := strings.SplitN(got, "\n", 2)[0]
	if !strings.Contains(first, "보드 · axis-sample") {
		t.Fatalf("첫 줄이 보드 머리 형식이 아니다: %q", first)
	}
	if !strings.Contains(first, "파생 git@") || !strings.Contains(first, "못 읽은 축 1개") {
		t.Fatalf("첫 줄이 FormatFreshness 형식(수만)을 안 지킨다: %q", first)
	}
	if strings.Contains(first, "widget-axis-zzz") {
		t.Fatalf("첫 줄에 축 이름이 새어 들어갔다 — FormatFreshness 형식이 바뀐 것이다: %q", first)
	}
	if !strings.Contains(got, "widget-axis-zzz") {
		t.Fatalf("축 이름이 어디에도 없다 — 첫 줄 밖에서도 나야 한다:\n%s", got)
	}
}
