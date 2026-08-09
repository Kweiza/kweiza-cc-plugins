package mcpsrv

import (
	"strings"
	"testing"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
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
