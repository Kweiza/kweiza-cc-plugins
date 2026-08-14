package mcpsrv

import (
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// 자원 점유 줄은 **0건일 때도 나온다.**
//
// ★ 이 축은 예전에 「쓰는 사람에게만 보이는 축」이었다. 기본 보드는 점유가 0건이면 줄을
// 통째로 뺐고(그래서 안 써 본 세션은 축의 존재조차 못 봤다), detail 만 「자원 점유 없음」을
// 냈는데 그 문장이 **「아무도 안 쥐었다」와 「걸 자리가 없다」를 겸했다**.
//
// 실측(2026-08-14 · 판단 01KZYXQ4ZJ3P19K2JMRDSJWSSN): Dell 스테이징을 점유한 세션이
// `자원 점유 없음` 을 읽고 **「fd 에 스테이징 배타 자원 축이 아예 없다」**로 결론낸 뒤 그
// 오독을 다른 프로젝트 원장에 증거로 남겼다. 실제로는 자원명이 자유 문자열이라
// land(resources:["env:dell"]) 한 줄이면 그날 배타가 섰다. 일반화는 이틀 전에 랜딩돼
// 있었고(454d49a) 그 뒤 자원 홀드 73건 중 landing 밖은 1건뿐이었다.
//
// 그러니 이 시험이 잠그는 것은 문구가 아니라 **축의 가시성**이다: 두 화면 다 자원 줄을
// 내고, 0건 줄은 「걸 자리가 없다」로 안 읽히게 거는 법을 함께 말한다.
func TestBoardShowsResourceAxisEvenWithNoHolds(t *testing.T) {
	v := service.BoardView{
		Project: model.Project{ID: "sample-platform", DefaultBranch: "main"},
		At:      t0, Window: 8 * time.Hour,
	}

	for _, tc := range []struct {
		name   string
		detail bool
	}{{"brief", false}, {"detail", true}} {
		t.Run(tc.name, func(t *testing.T) {
			got := RenderBoard(v, BoardRenderOptions{Now: t0, Detail: tc.detail})
			if !strings.Contains(got, "자원 점유:") {
				t.Fatalf("점유 0건인데 자원 줄이 통째로 없다 — 이 축은 안 써 본 세션에게 안 보인다:\n%s", got)
			}
			// 0건이 「걸 자리가 없다」로 안 읽혀야 한다.
			if !strings.Contains(got, "걸 자리가 없다는 뜻이 아니다") {
				t.Fatalf("0건 줄이 「아무도 안 쥐었다」와 「걸 자리가 없다」를 겸한다:\n%s", got)
			}
			// 거는 법이 같은 줄에 있어야 한다 — 축이 있다는 것만 알면 부를 방법을 또 찾아야 한다.
			if !strings.Contains(got, "land(resources:") {
				t.Fatalf("0건 줄이 거는 법을 안 말한다:\n%s", got)
			}
		})
	}
}

// 점유가 있으면 **자원명과 점유자**를 낸다 — 자원명 자체가 이 축의 교재다.
//
// ★ fd 는 자원 이름의 열거를 갖지 않는다(자유 문자열). 그래서 「어떤 이름을 쓰나」에
// 답하는 유일한 실물이 지금 걸려 있는 이름이다. 이 줄이 이름을 뭉개면 규약은 DESIGN 에만
// 남고 화면에서는 사라진다.
func TestBoardResourceLineNamesResourceAndHolder(t *testing.T) {
	v := service.BoardView{
		Project: model.Project{ID: "sample-platform", DefaultBranch: "main"},
		At:      t0, Window: 8 * time.Hour,
		Held: []model.ResourceHold{
			{Resource: "env:dell", SessionID: "01KZW5RD3WSHCRGXZZZDM76AQ9"},
			{Resource: "landing", SessionID: "01KZYZJ07VV6QT0R60K86VJ9T4"},
		},
	}

	for _, tc := range []struct {
		name   string
		detail bool
	}{{"brief", false}, {"detail", true}} {
		t.Run(tc.name, func(t *testing.T) {
			got := RenderBoard(v, BoardRenderOptions{Now: t0, Detail: tc.detail})
			for _, want := range []string{"env:dell", "landing"} {
				if !strings.Contains(got, want) {
					t.Fatalf("자원명 %q 가 화면에 없다 — 이름이 이 축의 유일한 교재다:\n%s", want, got)
				}
			}
			if !strings.Contains(got, ShortID("01KZW5RD3WSHCRGXZZZDM76AQ9")) {
				t.Fatalf("점유자가 안 보인다 — 누구에게 물어야 하는지가 사라진다:\n%s", got)
			}
			// 점유가 있을 때는 0건 안내가 섞이면 안 된다.
			if strings.Contains(got, "걸 자리가 없다는 뜻이 아니다") {
				t.Fatalf("점유가 있는데 0건 안내가 붙었다:\n%s", got)
			}
		})
	}
}
