package mcpsrv

import (
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// pick 은 티클러라는 사실과 그 기한을 낸다.
//
// 2026-08-23 에 이 구멍으로 한 세션이 통째로 헛일했다. 08-21 세션이
// `fd-folded-keys-have-no-priority-on-return` 에 `tickler` 를 달아 굶김 축에서 뺐는데,
// 이틀 뒤 pick 이 그것을 **후보 7건 중 1순위로 추천**하면서 티클러라는 말을 한 마디도
// 안 했다. 그 세션은 집어서 같은 SQL 을 돌리고 같은 미충족을 봤다.
//
// `judge.FiresOn` 이 막으려고 만들어진 2026-08-12 사건(세 시간 반에 네 세션이 같은
// 재측)과 같은 것이다. 그때 축을 만들어 놓고 **픽업 경로에 안 꽂았다** — 유일한
// 발화처인 queueItemAge 는 `board detail` 에서만 불리는데, 픽업 절차는
// `board`(기본) → `pick` 이라 그 화면을 지나지 않는다.
func TestRenderPickNamesTheLeadTickler(t *testing.T) {
	got := RenderPick(service.PickResult{
		Mode: service.PickRecommended, Reason: "1순위다",
		Item: &model.Item{
			ID: "lead", Title: "선두", State: model.ItemOpen, CreatedAt: t0,
			Labels: []string{"tickler", "fires:2026-08-30"},
		},
	}, t0)

	if !strings.Contains(got, "티클러") {
		t.Fatalf("선두가 티클러인데 pick 이 그 말을 안 한다 — 이것이 08-23 재연을 낳은 침묵이다:\n%s", got)
	}
	if !strings.Contains(got, "08-30 발화") {
		t.Fatalf("발화일이 화면에 없다 — 나이만 있는 티클러는 뜻이 없다:\n%s", got)
	}
	if strings.Contains(got, "지났다") {
		t.Fatalf("기한 전인데 지났다고 한다(now=%s, 발화 08-30):\n%s", t0.Format("01-02"), got)
	}
}

// 기한이 지났으면 그 사실도 낸다 — 지난 기한이 안 지난 것과 같아 보이면 아무도 안 연다.
// (queueItemAge 의 같은 시험과 한 쌍이다.)
func TestRenderPickSaysTheLeadTicklerIsDue(t *testing.T) {
	got := RenderPick(service.PickResult{
		Mode: service.PickRecommended, Reason: "1순위다",
		Item: &model.Item{
			ID: "lead", Title: "선두", State: model.ItemOpen, CreatedAt: t0,
			Labels: []string{"tickler", "fires:2026-07-01"},
		},
	}, t0)

	if !strings.Contains(got, "07-01 발화·지났다") {
		t.Fatalf("기한이 지났는데 화면이 안 말한다:\n%s", got)
	}
}

// ★ 발화일 **없는** 티클러가 이 결함의 실물이다 — 08-23 에 집힌 그 항목이
// 정확히 `["tickler"]` 뿐이었다. 없는 날짜를 지어내지는 않되, **없다는 사실**은
// 말해야 한다. 침묵하면 티클러가 아닌 것과 화면에서 구별되지 않는다.
func TestRenderPickTellsWhenTheTicklerHasNoFiringDate(t *testing.T) {
	got := RenderPick(service.PickResult{
		Mode: service.PickRecommended, Reason: "1순위다",
		Item: &model.Item{
			ID: "lead", Title: "선두", State: model.ItemOpen, CreatedAt: t0,
			Labels: []string{"tickler"},
		},
	}, t0)

	if !strings.Contains(got, "티클러") {
		t.Fatalf("티클러라는 사실이 사라졌다:\n%s", got)
	}
	if !strings.Contains(got, "fires:") {
		t.Fatalf("발화일이 없다는 사실을 안 말한다 — 이 상태가 08-23 재연을 낳았다:\n%s", got)
	}
	// 없는 날짜를 지어내지 않는다.
	if strings.Contains(got, "발화)") || strings.Contains(got, "지났다") {
		t.Fatalf("fires 가 없는데 기한을 지어냈다:\n%s", got)
	}
}

// 티클러가 아니면 이 줄이 아예 없다 — 상시 점등된 줄은 판별력이 0이 된다(§4).
// fires 만 달린 항목도 마찬가지다: 이 축은 티클러 표기 안에서만 산다.
func TestRenderPickStaysSilentWhenTheLeadIsNotATickler(t *testing.T) {
	got := RenderPick(service.PickResult{
		Mode: service.PickRecommended, Reason: "1순위다",
		Item: &model.Item{
			ID: "lead", Title: "선두", State: model.ItemOpen, CreatedAt: t0,
			Labels: []string{"repo:code", "fires:2026-08-30"},
		},
	}, t0)

	if strings.Contains(got, "티클러") || strings.Contains(got, "08-30") {
		t.Fatalf("티클러가 아닌데 이 축이 나왔다:\n%s", got)
	}
}

// 자리를 못박는다 — 항목 절 **안**, 본문 4000자보다 **앞**이다.
//
// 종료 선언 축이 같은 이유로 이 자리를 잡았다(render_close_declared_test.go). 응답
// 꼬리로 밀리면 본문과 묶음 절을 지나야 보이는 줄이 되고, 그것은 이 축이 겨냥한 독자
// (집기 전에 읽는 세션)에게 사실상 안 보이는 것과 같다. 08-23 세션은 본문을 끝까지
// 읽고서야 티클러임을 알았다 — 그때는 이미 집은 뒤였다.
func TestRenderPickPutsTheTicklerLineBeforeTheBody(t *testing.T) {
	got := RenderPick(service.PickResult{
		Mode: service.PickRecommended, Reason: "1순위다",
		Item: &model.Item{
			ID: "lead", Title: "선두", State: model.ItemOpen, CreatedAt: t0,
			Labels: []string{"tickler", "fires:2026-08-30"},
			Body:   "본문이다",
		},
	}, t0)

	head := strings.Index(got, "\n▸ lead")
	mark := strings.Index(got, "\n티클러")
	body := strings.Index(got, "\n본문:")
	if head < 0 || mark < 0 || body < 0 {
		t.Fatalf("머리줄(%d)·티클러 줄(%d)·본문(%d) 중 없는 것이 있다:\n%s", head, mark, body, got)
	}
	if head >= mark || mark >= body {
		t.Fatalf("티클러 줄이 항목 머리줄 뒤·본문 앞이 아니다(머리줄 %d · 티클러 %d · 본문 %d):\n%s",
			head, mark, body, got)
	}
}

// 항목이 없으면 이 줄도 없다 — 관측할 대상이 없다.
func TestRenderPickOmitsTicklerLineWhenThereIsNoItem(t *testing.T) {
	got := RenderPick(service.PickResult{
		Mode: service.PickNone, Reason: "적격 항목이 0건이다", Scope: "후보 = 열린 항목 0건",
	}, t0)

	if strings.Contains(got, "티클러") {
		t.Fatalf("항목이 없는데 티클러 줄이 나왔다:\n%s", got)
	}
}

// ★ 구성원마다 **제 값**을 받는지를 절 안에서 단정한다.
//
// 전체 문자열에 대한 strings.Contains 는 **출력을 넓히는 모든 변경을 통과시킨다** —
// 이 파일의 경로 축·종료 선언 축이 실측으로 그렇게 죽어 있었다. 그래서 네 값을 서로
// 다르게 깔고 bundleMemberSegment 로 잘라 그 안에서만 본다. 선두 것을 구성원에
// 복사하는 변이가 여기서 죽는다.
//
// ★ 못 집은 구성원(Rejection≠nil)을 반드시 하나 넣는다. 그 갈래는 renderBundle 의
// continue 로 절을 끊으므로, 줄을 continue 아래에 두는 구현은 **여기서만** 죽는다.
// 그리고 그 자리가 중요한 이유가 있다: 못 집은 구성원이야말로 다음 세션이 다시
// 집으러 오는 항목이고, 그것이 티클러라면 다시 오는 것 자체가 헛걸음이다.
func TestRenderPickGivesEachBundleMemberItsOwnTickler(t *testing.T) {
	got := RenderPick(service.PickResult{
		Mode: service.PickClaimed, Reason: "선두를 선점했다", Branch: "lead",
		Item: &model.Item{
			ID: "lead", Title: "선두", State: model.ItemClaimed, CreatedAt: t0,
			Labels: []string{"tickler", "fires:2026-08-30"},
		},
		Bundle: &service.BundleInfo{
			Reason: "의존자 합 0 · 묶음 5건 · 선두 lead",
			Members: []service.BundleMember{
				{
					// 기한 전 — 선두와 **다른** 날짜다.
					Item: model.Item{ID: "m-future", Title: "기한 전", State: model.ItemClaimed, CreatedAt: t0,
						Labels: []string{"tickler", "fires:2026-09-17"}},
					Claimed: true,
				},
				{
					// 못 집은 구성원 — continue 갈래. 여기 줄이 없으면 이 시험만 붉어진다.
					Item: model.Item{ID: "m-due", Title: "기한 지남", State: model.ItemClaimed, CreatedAt: t0,
						Labels: []string{"tickler", "fires:2026-07-01"}},
					Rejection: &model.Rejection{Item: "m-due", Reason: judge.RejectClaimed, Detail: "세션 S2 가 선점했다"},
				},
				{
					// 티클러인데 기한이 없다 — 08-23 재연을 낳은 그 상태다.
					Item: model.Item{ID: "m-bare", Title: "기한 없음", State: model.ItemOpen, CreatedAt: t0,
						Labels: []string{"tickler"}},
					Link: judge.Link{Item: "m-bare", Detail: "세션이 함께 지정했다"},
				},
				{
					// 티클러가 아니다 — 이 절에는 축이 아예 없어야 한다.
					Item: model.Item{ID: "m-plain", Title: "평범", State: model.ItemOpen, CreatedAt: t0,
						Labels: []string{"repo:code"}},
					Link: judge.Link{Item: "m-plain", Detail: "세션이 함께 지정했다"},
				},
			},
		},
	}, t0)

	for _, tc := range []struct {
		id       string
		want     []string
		unwanted []string
	}{
		{"m-future", []string{"티클러", "09-17 발화"}, []string{"지났다", "08-30", "07-01"}},
		{"m-due", []string{"티클러", "07-01 발화·지났다"}, []string{"08-30", "09-17"}},
		{"m-bare", []string{"티클러", "fires:"}, []string{"발화)", "지났다", "08-30", "09-17", "07-01"}},
		{"m-plain", nil, []string{"티클러", "발화"}},
	} {
		seg := bundleMemberSegment(t, got, tc.id)
		for _, w := range tc.want {
			if !strings.Contains(seg, w) {
				t.Errorf("구성원 %s 의 절에 %q 가 없다 — 제 값을 못 받았다:\n%s", tc.id, w, seg)
			}
		}
		for _, u := range tc.unwanted {
			if strings.Contains(seg, u) {
				t.Errorf("구성원 %s 의 절에 %q 가 있다 — 남의 값이 새 들어왔다:\n%s", tc.id, u, seg)
			}
		}
	}
}

// 이 축은 **표시뿐이다.** 기한이 지나도 승격시키지 않고 아무것도 안 막는다
// (judge/tickler.go 의 ★ 둘 · 설계 §5·§8). 표시를 판정으로 넓히는 변경이 오면
// 이 시험이 그것을 가리킨다 — 티클러라는 이유로 추천·선점이 달라지면 안 된다.
func TestRenderPickTicklerIsDisplayOnly(t *testing.T) {
	mk := func(labels []string) string {
		return RenderPick(service.PickResult{
			Mode: service.PickClaimed, Reason: "선두를 선점했다", Branch: "lead",
			Item: &model.Item{
				ID: "lead", Title: "선두", State: model.ItemClaimed, CreatedAt: t0,
				Labels: labels, Body: "본문이다",
			},
			Claim: &model.Claim{At: t0},
		}, t0)
	}
	plain := mk([]string{"repo:code"})
	tick := mk([]string{"tickler", "fires:2026-07-01"})

	// 티클러 줄 하나를 뺀 나머지가 글자 그대로 같아야 한다 — 머리줄·브랜치·선점 시각
	// 어느 것도 티클러 때문에 달라지지 않는다.
	var stripped []string
	for _, line := range strings.Split(tick, "\n") {
		if strings.HasPrefix(line, "티클러") {
			continue
		}
		stripped = append(stripped, line)
	}
	if got := strings.Join(stripped, "\n"); got != plain {
		t.Fatalf("티클러가 표시 밖으로 샜다 — 줄 하나를 뺀 나머지가 달라졌다.\n--- 티클러 ---\n%s\n--- 평범 ---\n%s", got, plain)
	}
}
