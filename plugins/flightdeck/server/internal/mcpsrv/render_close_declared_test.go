package mcpsrv

import (
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/judge"
	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/service"
)

// 종료 선언 축 — **닫히지 못한 항목이 큐의 머리에 서는 것**을 화면이 말하는 자리다.
//
// 사고의 모양: 08-04 에 finish 가 선점 표류로 거절·롤백됐고(판단 본문 10300바이트가
// 통째로 죽었다) 그 사실이 원장에 그대로 남아 있는데, 08-05 의 pick 이 같은 항목을
// **1순위 선두**로 추천하면서 그 신호를 한 글자도 말하지 않았다. 이 파일의 시험은
// 전부 "응답이 그 사실을 말하는가"를 문자열 좌표계에서 잰다.

// TestRenderCloseDeclaredNeverStaysSilent 는 네 갈래가 **전부 말하는지**를 본다.
//
// ★ renderPathCheck 이 이상이 없어도 한 줄을 찍는 그 이유 그대로다 — 침묵하면
// "선언이 없다"와 "이 축을 안 봤다"가 같은 화면이 되고, 그러면 원장 조회가 통째로
// 실패한 날에도 pick 은 평소와 똑같아 보인다.
//
// ★ 처방이 mode 로 갈린다. done 은 이미 랜딩됐을 수 있고 dropped 는 이미 버리기로
// 판정됐을 수 있다 — 다음 세션이 확인할 것이 서로 다르다(랜딩 이력을 볼 것인가,
// 버린 판단을 읽을 것인가). 그래서 갈래마다 **그 갈래에만 있는 문구**를 단정하고,
// 다른 갈래의 문구가 새어 들어오지 않는 것도 같이 본다 — 둘을 맞바꾸는 변이는
// want 만으로는 안 죽는다.
//
// ★ 수는 **하한**이다. store 의 doc 이 못박은 계약이다(event.go:255-258:
// "소비자의 문구가 '정확히 N건'이 아니라 '적어도 N건'으로 말해야 한다").
// 0건 갈래에서도 그 말을 한다 — 0 이야말로 안 써진 마무리에 가장 잘 속는 값이다.
func TestRenderCloseDeclaredNeverStaysSilent(t *testing.T) {
	at := time.Date(2026, 8, 4, 23, 54, 37, 0, time.UTC)
	cases := []struct {
		name string
		d    *model.CloseDeclaration
		want []string
		deny []string
	}{
		{
			name: "축을 못 읽었다",
			d:    nil,
			want: []string{"종료 선언: ", "이 축을 안 읽었다"},
			deny: []string{"이미 랜딩됐을 수 있다", "이미 버리기로 판정됐을 수 있다", "하한"},
		},
		{
			name: "읽었는데 0건",
			d:    &model.CloseDeclaration{},
			want: []string{"종료 선언: ", "관측되지 않았다", "이 수는 하한이다"},
			deny: []string{"이 축을 안 읽었다", "이미 랜딩됐을 수 있다", "이미 버리기로 판정됐을 수 있다"},
		},
		{
			name: "done 선언",
			d: &model.CloseDeclaration{
				Done: 2, Last: at, LastSession: "01LEADSESSION", LastMode: "done",
			},
			want: []string{
				"종료 선언: 롤백된 마무리 선언 적어도 2건(done 2 · dropped 0)",
				"마지막 2026-08-04 23:54", "세션 01LEADSE…", "mode=done",
				"done 2건: 이미 랜딩됐을 수 있다.",
				"연결된 판단부터 읽어라.", "이 수는 하한이다",
			},
			deny: []string{"이미 버리기로 판정됐을 수 있다", "이 축을 안 읽었다", "관측되지 않았다"},
		},
		{
			name: "dropped 선언",
			d: &model.CloseDeclaration{
				Dropped: 1, Last: at, LastSession: "01LEADSESSION", LastMode: "dropped",
			},
			want: []string{
				"종료 선언: 롤백된 마무리 선언 적어도 1건(done 0 · dropped 1)",
				"dropped 1건: 이미 버리기로 판정됐을 수 있다.",
				"연결된 판단부터 읽어라.", "이 수는 하한이다",
			},
			deny: []string{"이미 랜딩됐을 수 있다", "이 축을 안 읽었다", "관측되지 않았다"},
		},
		{
			// 둘 다 0이 아니면 **둘 다** 낸다. 하나로 뭉치면 처방이 갈리는 사실이 사라진다.
			// 세션 id 는 event.session_id 에서 오는데 그 열은 NULL 을 받는다(schema.sql:367,
			// store 가 str(session) 으로 빈 문자열을 낸다) — 빈 값을 그대로 찍으면
			// "세션  · mode=" 가 되어 잘린 줄로 읽힌다.
			name: "둘 다 있고 세션은 비었다",
			d: &model.CloseDeclaration{
				Done: 1, Dropped: 1, Last: at, LastSession: "", LastMode: "dropped",
			},
			want: []string{
				"종료 선언: 롤백된 마무리 선언 적어도 2건(done 1 · dropped 1)",
				"done 1건: 이미 랜딩됐을 수 있다.",
				"dropped 1건: 이미 버리기로 판정됐을 수 있다.",
				"세션 미상",
			},
			deny: []string{"이 축을 안 읽었다", "관측되지 않았다"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := renderCloseDeclared(c.d, "")
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Fatalf("%q 가 없다:\n%s", w, got)
				}
			}
			for _, d := range c.deny {
				if strings.Contains(got, d) {
					t.Fatalf("이 갈래에 없어야 할 %q 가 있다 — 갈래가 서로 새고 있다:\n%s", d, got)
				}
			}
			// 끝은 반드시 개행이다. 안 그러면 다음 줄이 이 줄에 붙어 두 사실이 한 줄이 된다.
			if !strings.HasSuffix(got, "\n") {
				t.Fatalf("마지막 개행이 없다: %q", got)
			}
			// 들여쓰기는 **모든 줄에** 붙는다. 첫 줄만 밀면 이어지는 줄이 구성원 절에서
			// 선두의 발화로 읽힌다 — 경로 축의 `fd move` 줄이 정확히 그 함정을 밟은 적이 있고
			// (render_test.go:1450-1455) 그래서 거기도 줄 단위로 잠갔다.
			indented := renderCloseDeclared(c.d, "    ")
			for _, line := range strings.Split(strings.TrimRight(indented, "\n"), "\n") {
				if !strings.HasPrefix(line, "    ") {
					t.Fatalf("들여쓰기가 안 붙은 줄이 있다: %q\n%s", line, indented)
				}
			}
		})
	}
}

// TestRenderCloseDeclaredStealsNoCountedString 은 새 줄이 **이미 세어지고 있는 문자열**을
// 하나도 밟지 않는 것을 잠근다.
//
// ★ 왜 이것이 시험이어야 하나. 이 저장소의 pick 렌더 시험 여럿이 개수와 절 분할을
// 문자열 하나에 걸어 두고 있다: `경로 실재: ` 4개(render_test.go:1411) ·
// `fd move ` 1개(:1442) · `브랜치: ` 1개(render_test.go:238) · 구성원 절을 자르는
// `"\n  "+표식+" "`(bundleMemberSegment, render_lines_test.go:241-243).
// 새 문장이 그중 하나를 우연히 품으면 엉뚱한 시험이 붉어지고, 더 나쁘게는 절 경계가
// 밀려 **격리 단정이 조용히 무의미해진다.**
//
// ★ nil 문구는 renderPathCheck·renderBundle 의 nil 문구와도 **글자가 달라야 한다.**
// 그 문장("이 응답은 그 축을 읽지 않았다 — …")을 그대로 복제하면
// render_test.go:1415-1435 가 그것을 구성원 절에 붙은 **남의 경로 판정**으로 읽어
// 붉어진다(그 시험은 unreadSum 을 남의 절에서 못 찾는 것으로 격리를 잰다).
// 그래서 여기서 그 문자열 자체를 금지 목록에 넣는다.
func TestRenderCloseDeclaredStealsNoCountedString(t *testing.T) {
	at := time.Date(2026, 8, 4, 23, 54, 37, 0, time.UTC)
	var all strings.Builder
	for _, d := range []*model.CloseDeclaration{
		nil,
		{},
		{Done: 3, Last: at, LastSession: "01LEADSESSION", LastMode: "done"},
		{Done: 1, Dropped: 2, Last: at, LastSession: "01LEADSESSION", LastMode: "dropped"},
	} {
		all.WriteString(renderCloseDeclared(d, ""))
		all.WriteString(renderCloseDeclared(d, "    "))
	}
	got := all.String()

	for _, banned := range []string{
		"경로 실재: ", "브랜치: ", "fd move ", "겹침 판정 범위:",
		"안 들어갔다", "겹침을 관측하지 않았다", "묶을 게 없어 단독이다",
		"이 응답은 그 축을 읽지 않았다 — 낡은 캐시이거나 서버가 이 축을 모르는 판이다.",
	} {
		if strings.Contains(got, banned) {
			t.Fatalf("종료 선언 줄이 이미 세어지는 문자열 %q 를 밟았다:\n%s", banned, got)
		}
	}
	// 구성원 절의 경계는 `"\n  " + 표식 + " "` 하나에 걸려 있다.
	for _, mark := range []string{markClaimed, markRejected, markProposed} {
		if strings.Contains("\n"+got, "\n  "+mark+" ") {
			t.Fatalf("종료 선언 줄이 구성원 머리줄 접두(%q)로 시작한다 — 절이 그 자리에서 잘린다:\n%s", mark, got)
		}
	}
}

// TestRenderPickCarriesTheLeadCloseDeclaration 은 **선두**의 종료 선언을 못박는다.
//
// ★ 이 사고의 주인공이 선두다. 08-04 에 롤백된 finish 가 원장에 남은 항목이
// 08-05 22:54 의 pick 에서 후보 26건 중 **1순위 선두**로 추천됐다. 그런데
// renderBundle 은 BundleInfo 하나만 받고 Members 는 정의상 선두 제외라 **선두를
// 모른다** — 구성원 자리에만 심으면 이 사고를 낳은 바로 그 항목에 대해 응답이
// 정확히 침묵한다. 그래서 선두 갈래는 별도 단정이 필요하다.
//
// 묶음이 없는 응답(Bundle=nil)으로 본다. 묶음 절이 있으면 어느 구성원 줄이
// 이 단정을 대신 통과시킬 수 있다.
func TestRenderPickCarriesTheLeadCloseDeclaration(t *testing.T) {
	got := RenderPick(service.PickResult{
		Mode: service.PickRecommended, Reason: "1순위다",
		Item: &model.Item{ID: "lead", Title: "선두", State: model.ItemOpen, CreatedAt: t0},
		CloseDeclared: &model.CloseDeclaration{
			Done: 1, Last: time.Date(2026, 8, 4, 23, 54, 37, 0, time.UTC),
			LastSession: "01LEADSESSION", LastMode: "done",
		},
	}, t0)

	const want = "\n종료 선언: 롤백된 마무리 선언 적어도 1건(done 1 · dropped 0) — " +
		"마지막 2026-08-04 23:54 · 세션 01LEADSE… · mode=done\n"
	if !strings.Contains(got, want) {
		t.Fatalf("선두의 종료 선언 줄이 0칸 들여쓰기로 제 값을 안 낸다:\n%s", got)
	}
	if !strings.Contains(got, "이미 랜딩됐을 수 있다") {
		t.Fatalf("done 처방(이미 랜딩됐을 수 있다)이 없다 — 다음 세션이 무엇을 확인할지 모른다:\n%s", got)
	}

	// 자리도 못박는다 — 항목 절 **안**, `경로 실재:` 바로 뒤다. 응답 꼬리로 밀리면
	// 본문 4000자와 묶음 절을 지나야 보이는 줄이 되고, 그것은 이 축이 겨냥한 독자
	// (집기 전에 읽는 세션)에게 사실상 안 보이는 것과 같다.
	head := strings.Index(got, "\n▸ lead")
	axis := strings.Index(got, "\n경로 실재: ")
	decl := strings.Index(got, "\n종료 선언: ")
	if head < 0 || axis < 0 || decl < 0 {
		t.Fatalf("항목 머리줄(%d)·경로 축(%d)·종료 선언(%d) 중 없는 줄이 있다:\n%s", head, axis, decl, got)
	}
	if head >= axis || axis >= decl {
		t.Fatalf("종료 선언 줄이 항목 절 안 `경로 실재:` 뒤가 아니다(머리줄 %d · 경로 축 %d · 종료 선언 %d):\n%s",
			head, axis, decl, got)
	}
}

// 선두의 축이 nil 이면 그 사실을 말한다 — 침묵이 "선언 없음"으로 읽히면 안 된다.
// (TestRenderPickSaysTheAxisWasNotReadWhenVerdictIsNil 과 같은 규율이다.)
func TestRenderPickSaysTheCloseAxisWasNotReadWhenNil(t *testing.T) {
	got := RenderPick(service.PickResult{
		Mode: service.PickRecommended, Reason: "1순위다",
		Item: &model.Item{ID: "lead", Title: "선두", State: model.ItemOpen, CreatedAt: t0},
	}, t0)

	if !strings.Contains(got, "\n종료 선언: 이 응답은 이 축을 안 읽었다") {
		t.Fatalf("종료 선언 축이 nil 인데 그 사실을 말하지 않는다 — 못 읽음이 0건으로 접힌다:\n%s", got)
	}
}

// 항목이 없으면 이 줄도 없다 — 관측할 대상이 없다.
// (TestRenderPickOmitsPathAxisWhenThereIsNoItem 과 같은 규율이다.)
func TestRenderPickOmitsCloseDeclarationWhenThereIsNoItem(t *testing.T) {
	got := RenderPick(service.PickResult{
		Mode: service.PickNone, Reason: "적격 항목이 0건이다", Scope: "후보 = 열린 항목 0건",
	}, t0)

	if strings.Contains(got, "종료 선언:") {
		t.Fatalf("항목이 없는데 종료 선언 줄이 나왔다:\n%s", got)
	}
}

// TestRenderPickGivesEachBundleMemberItsOwnCloseDeclaration 은 구성원마다 **제 값**을
// 받는지를 절 안에서 단정한다. render_test.go:1372 의 경로 축 시험과 같은 좌표계다 —
// 전체 문자열에 대한 strings.Contains 는 **출력을 넓히는 모든 변경을 통과시킨다.**
// 실측으로 확인된 것도 그것이다: renderPathCheck 의 인자를 Members[0] 것으로 바꿔도
// 전 스위트가 초록이었다. 그래서 다섯 값을 **서로 다르게** 깔고 bundleMemberSegment 로
// 잘라 그 안에서만 본다 — 선두 것을 구성원에 복사하는 변이가 여기서 죽는다.
//
// ★ 못 집은 구성원(Rejection≠nil)을 반드시 하나 넣는다. 그 갈래는 render.go:1215 의
// continue 로 절을 끊으므로, 줄을 continue 아래에 두는 구현은 **여기서만** 죽는다.
// 그리고 그 자리가 중요한 이유가 있다: 못 집은 구성원이야말로 다음 세션이 다시
// 집으러 오는 항목이다.
//
// ★ 오등록 시험이 Members[1] 에 값을 둔 것과 같은 이유로, 여기서도 값이 다른 구성원을
// Members[0] 아닌 자리에 섞는다 — Members[0] 만 쓰는 변이가 정답과 구별되게.
func TestRenderPickGivesEachBundleMemberItsOwnCloseDeclaration(t *testing.T) {
	var (
		leadAt    = time.Date(2026, 8, 4, 23, 54, 37, 0, time.UTC)
		doneAt    = time.Date(2026, 8, 5, 1, 0, 0, 0, time.UTC)
		droppedAt = time.Date(2026, 8, 6, 2, 0, 0, 0, time.UTC)
	)
	const (
		leadOwn    = "롤백된 마무리 선언 적어도 2건(done 2 · dropped 0) — 마지막 2026-08-04 23:54 · 세션 01LEADSE… · mode=done"
		doneOwn    = "롤백된 마무리 선언 적어도 1건(done 1 · dropped 0) — 마지막 2026-08-05 01:00 · 세션 01MEMDON… · mode=done"
		droppedOwn = "롤백된 마무리 선언 적어도 3건(done 0 · dropped 3) — 마지막 2026-08-06 02:00 · 세션 01MEMDRO… · mode=dropped"
		cleanOwn   = "원장에서 하나도 못 봤다"
		unreadOwn  = "이 응답은 이 축을 안 읽었다"
	)
	res := service.PickResult{
		Mode: service.PickClaimed, Reason: "선두를 선점했다", Branch: "lead",
		Item: &model.Item{ID: "lead", Title: "선두", State: model.ItemClaimed, CreatedAt: t0},
		CloseDeclared: &model.CloseDeclaration{
			Done: 2, Last: leadAt, LastSession: "01LEADSESSION", LastMode: "done",
		},
		Bundle: &service.BundleInfo{
			Reason: "의존자 합 0 · 묶음 5건 · 선두 lead",
			Members: []service.BundleMember{
				{
					Item:    model.Item{ID: "m-done", Title: "done 선언", State: model.ItemClaimed, CreatedAt: t0},
					Claimed: true,
					CloseDeclared: &model.CloseDeclaration{
						Done: 1, Last: doneAt, LastSession: "01MEMDONE01", LastMode: "done",
					},
				},
				{
					// 못 집은 구성원 — continue 갈래. 여기 줄이 없으면 이 시험만 붉어진다.
					Item:      model.Item{ID: "m-dropped", Title: "dropped 선언", State: model.ItemClaimed, CreatedAt: t0},
					Rejection: &model.Rejection{Item: "m-dropped", Reason: judge.RejectClaimed, Detail: "세션 S2 가 선점했다"},
					CloseDeclared: &model.CloseDeclaration{
						Dropped: 3, Last: droppedAt, LastSession: "01MEMDROP01", LastMode: "dropped",
					},
				},
				{
					// 축은 읽었고 이 항목엔 선언이 없다 — nil 과 **다른 사실**이다.
					Item:          model.Item{ID: "m-clean", Title: "선언 없음", State: model.ItemOpen, CreatedAt: t0},
					Link:          judge.Link{Item: "m-clean", Detail: "세션이 함께 지정했다"},
					CloseDeclared: &model.CloseDeclaration{},
				},
				{
					// 축 자체를 안 읽었다 — 구서버·옛 캐시.
					Item:    model.Item{ID: "m-unread", Title: "축 못 읽음", State: model.ItemClaimed, CreatedAt: t0},
					Claimed: true,
				},
			},
		},
	}
	got := RenderPick(res, t0)

	// ① 선두 1 + 구성원 4 = 다섯. ("nil 이면 건너뛴다" 변이와 줄 삭제 변이가 여기서 죽는다.)
	if n := strings.Count(got, "종료 선언: "); n != 5 {
		t.Fatalf("종료 선언 줄이 %d개다 — 선두 1 + 구성원 4 = 5여야 한다:\n%s", n, got)
	}
	// ② 선두는 0칸이다. 구성원 줄(4칸)이 이 단정을 대신 통과시키지 못한다.
	if !strings.Contains(got, "\n종료 선언: "+leadOwn+"\n") {
		t.Fatalf("선두의 종료 선언이 0칸 들여쓰기로 제 값을 안 낸다:\n%s", got)
	}

	// ③ 각자 **제 것**이다. 자기 절 안에 자기 값이 4칸으로 있고, 남의 값은 없다.
	segs := map[string]string{
		"m-done":    bundleMemberSegment(t, got, "m-done"),
		"m-dropped": bundleMemberSegment(t, got, "m-dropped"),
		"m-clean":   bundleMemberSegment(t, got, "m-clean"),
		"m-unread":  bundleMemberSegment(t, got, "m-unread"),
	}
	own := map[string]string{
		"m-done": doneOwn, "m-dropped": droppedOwn, "m-clean": cleanOwn, "m-unread": unreadOwn,
	}
	all := []string{leadOwn, doneOwn, droppedOwn, cleanOwn, unreadOwn}
	for id, seg := range segs {
		if !strings.Contains(seg, "\n    종료 선언: "+own[id]) {
			t.Fatalf("구성원 %s 의 절에 자기 종료 선언이 4칸 들여쓰기로 없다:\n%s\n전체:\n%s", id, seg, got)
		}
		for _, other := range all {
			if other == own[id] {
				continue
			}
			if strings.Contains(seg, other) {
				t.Fatalf("구성원 %s 의 절에 남의 종료 선언이 붙었다(%q):\n%s\n전체:\n%s", id, other, seg, got)
			}
		}
	}

	// ④ 처방은 mode 로 갈린다. 둘을 맞바꾸는 변이는 ③ 으로는 안 죽는다 —
	//    수와 시각만 봐도 ③ 은 통과하기 때문이다.
	if !strings.Contains(segs["m-done"], "done 1건: 이미 랜딩됐을 수 있다.") ||
		strings.Contains(segs["m-done"], "이미 버리기로 판정됐을 수 있다") {
		t.Fatalf("done 구성원의 처방이 틀렸다:\n%s\n전체:\n%s", segs["m-done"], got)
	}
	if !strings.Contains(segs["m-dropped"], "dropped 3건: 이미 버리기로 판정됐을 수 있다.") ||
		strings.Contains(segs["m-dropped"], "이미 랜딩됐을 수 있다") {
		t.Fatalf("dropped 구성원의 처방이 틀렸다:\n%s\n전체:\n%s", segs["m-dropped"], got)
	}

	// ⑤ 못 집은 구성원에게도 나온다 — render.go:1215 의 continue **위**여야 한다.
	if !strings.Contains(segs["m-dropped"], "못 집었다: ") {
		t.Fatalf("전제 실패 — m-dropped 가 못 집은 구성원이 아니다:\n%s", segs["m-dropped"])
	}

	// ⑥ 기존 개수 단정을 **같은 출력에서** 함께 본다. 새 줄이 절 경계나 개수를
	//    밟았는지는 순수 함수 시험으로는 못 잰다 — 밟히는 자리가 renderBundle 의
	//    조립 결과이기 때문이다.
	//
	//    `경로 실재:` 는 못 집은 구성원(m-dropped)에서는 안 나온다 — render.go 의
	//    continue 가 그 축(renderPathCheck, line ~1250)까지 끊기 때문이다. 이건
	//    이 태스크가 손댄 자리가 아니라 원래부터 그런 동작이라, 여기서는
	//    선두 1 + **집어 본** 구성원 3(m-done · m-clean · m-unread) = 4 다.
	if n := strings.Count(got, "경로 실재: "); n != 4 {
		t.Fatalf("경로 실재 줄이 %d개다 — 선두 1 + 못 집은 것 뺀 구성원 3 = 4여야 한다(새 줄이 그 축을 밟았다):\n%s", n, got)
	}
	heads := strings.Count(got, "\n  "+markClaimed+" ") +
		strings.Count(got, "\n  "+markRejected+" ") +
		strings.Count(got, "\n  "+markProposed+" ")
	if heads != 4 {
		t.Fatalf("구성원 머리줄이 %d개다 — 새 줄이 구성원 절 경계를 밟았다:\n%s", heads, got)
	}
}
