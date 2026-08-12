package mcpsrv

import (
	"strings"
	"testing"
)

// 이 파일은 **응답을 본문과 꼬리로 가르는 도구**와 그 도구 자체를 지키는 시험을 함께 둔다.
//
// ★ 왜 필요한가. 도구 응답은 여러 절(본문 · 판단 · 겹침 · 미확인 · 꼬리)을 한 문자열로
// 이어 붙여 낸다. 그 문자열 전체를 haystack 으로 쓰면 **꼬리의 한 줄이 본문의 단정을
// 만족시키고, 본문이 통째로 비어도 초록이다.** internal/web 이 같은 병으로 거짓 초록
// 둘을 냈고, 이 패키지에서도 실측으로 둘을 찾았다(아래 ★실측 참고).
//
// ★ 왜 도구에 시험이 붙나. 잘못 자르는 헬퍼는 **좁힌 척만 하고 아무도 모른다.** 경계를
// 못 찾는 것은 Fatal 이라 시끄럽지만 나머지 둘은 조용하다:
//
//	① 경계가 새는 것 — 꼬리까지 담으면 좁히기 전과 같아진다.
//	② 빈 조각을 돌려주는 것 — 빈 haystack 은 mustMiss 를 **전부** 통과시킨다.
//	   좁힐수록 시험이 약해지는 방향의 실패라 가장 늦게 발견된다.
//
// TestBodyAndTailCutAtTheRealBoundary 가 그 둘을 막는다.
//
// ★ **진입점은 넷이지만 조립은 한 자리다** — `render.go` 의 `joinTail`:
//
//	① Server.withTail        — add · note · finish · alloc · land · 거절 · 열화 · 오류
//	② RenderBoard 의 opt.Tail — board (joinAll 이 부른다)
//	③ toolPick 정상 갈래      — pick
//	④ toolPick 미회계 갈래    — pick (회계 경고가 **본문의 끝**에 붙고 그다음이 꼬리다)
//
// 원래는 셋이 조립을 각각 손으로 적었고, 그래서 **변이가 한 자리만 덮었다**: 처음 실측에서
// withTail 에만 변이를 넣었더니 board·pick 시험 다섯이 "본문 없이 통과"로 **잘못 보였다** —
// 본문이 비어서가 아니라 변이가 그 경로에 안 닿았을 뿐이었다. 하마터면 없는 결함 다섯을
// 고치러 갔다. 지금은 `joinTail` 한 자리에 변이를 넣으면 넷이 다 빨개진다(실측).
//
// **그래도 아래 시험은 넷을 다 지난다.** 조립이 하나로 모였다는 사실 자체가 시험 대상이다 —
// 누가 다시 한 갈래에서 손으로 이으면 여기서 잡아야 한다.
//
// ★ **어디에 안 쓰나.** 이 헬퍼는 도구 응답(toolText 를 지난 문자열)에만 쓴다.
// RenderBoard·RenderPick 같은 순수 함수를 직접 부르는 시험은 꼬리가 **원리적으로 없다** —
// Tail 옵션을 안 주면 joinAll 이 아무것도 안 붙인다. 그런 haystack 에 bodyOf 를 걸면
// 표지가 없어 Fatal 이다. 이 패키지 시험 대부분이 그쪽이고, 그것은 결함이 아니다.
//
// ★ **부정 단정은 좁히지 않는다.** "여기 있어야 한다"는 좁혀야 뜻이 생기지만
// "어디에도 없어야 한다"는 넓게 둬야 뜻이 있다 — 본문으로 좁힌 mustMiss 는 그 문자열이
// 꼬리로 새어 나가는 날 못 잡는다. render_accounting_test.go 의 "설명하지 않는다"가 그 예다.
const tailMarker = "── 꼬리 ──"

// bodyOf 는 도구 응답에서 꼬리를 뗀 **본문**이다.
//
// ★ tailMarker 를 제품 코드의 상수에서 빌리지 않고 여기 다시 적는다. 빌리면 제품이
// 표지를 바꿀 때 시험이 조용히 따라가고, **경계가 바뀐 사실 자체가 관측되지 않는다.**
//
// ★ 표지가 본문 안에 있으면(판단 본문에 사람이 그 문자열을 쓴 경우) 첫 번째에서 자른다 —
// 본문이 실제보다 짧아져 단정이 빨개지는 쪽이다. 안전한 방향이라 그대로 둔다.
func bodyOf(t *testing.T, resp string) string {
	t.Helper()
	i := strings.Index(resp, tailMarker)
	if i < 0 {
		t.Fatalf("응답에 꼬리 표지(%q)가 없다 — 이 헬퍼로 좁힌 단정은 전부 여기서 멈춘다:\n%s",
			tailMarker, resp)
	}
	body := strings.TrimSpace(resp[:i])
	if body == "" {
		t.Fatalf("본문이 빈 조각이다 — 빈 haystack 에 건 mustMiss 는 무조건 초록이다:\n%s", resp)
	}
	return body
}

// tailOf 는 꼬리 표지부터 끝까지다. 꼬리를 재려는 단정은 이쪽을 쓴다 —
// 응답 전체로 재면 본문이 꼬리의 단정을 대신 만족시킬 수 있다(반대 방향의 같은 병이다).
func tailOf(t *testing.T, resp string) string {
	t.Helper()
	i := strings.Index(resp, tailMarker)
	if i < 0 {
		t.Fatalf("응답에 꼬리 표지(%q)가 없다:\n%s", tailMarker, resp)
	}
	return resp[i:]
}

// TestBodyAndTailCutAtTheRealBoundary 는 **헬퍼가 실제로 좁히는지**를 잰다.
// 이 시험이 없으면 헬퍼가 조각을 잘못 잘라도 그 위에 세운 단정들이 조용히 뜻을 잃는다.
//
// 합성 문자열이 아니라 **실제 도구 응답**으로 잰다. 합성으로 재면 제품이 꼬리를 붙이는
// 방식을 바꿔도(경로가 셋이다) 이 시험은 계속 초록이다.
func TestBodyAndTailCutAtTheRealBoundary(t *testing.T) {
	repo := newRepo(t)
	svc, _ := newSvc(t)
	srv := newServer(t, svc, repo, fullEnv(repo))

	frames := serve(t, srv,
		call("add", map[string]any{"id": "tb-1", "title": "경계 시험용 항목", "body": "본문이다"}),
		call("board", map[string]any{}),
		call("pick", map[string]any{"item_id": "tb-1"}),
	)
	if len(frames) != 3 {
		t.Fatalf("응답이 %d개다", len(frames))
	}

	// ★ **네 번째 경로 — pick 의 미회계 갈래다.** `toolPick` 은 조립을 두 갈래에서
	// 각각 손으로 적는데, 이 갈래는 회계 경고를 본문과 꼬리 **사이에** 끼운다.
	// 백엔드가 item_ids 를 조용히 버리는 서버에서만 나오므로 픽스처를 따로 세운다.
	repo2 := newRepo(t)
	svc2, _ := newSvc(t)
	srv2 := New(backendIgnoringItemIDs{svc2}, discard(),
		WithEnv(env(fullEnv(repo2))), WithCwd(repo2, nil), WithHostname("testhost", nil))
	frames2 := serve(t, srv2,
		call("add", map[string]any{"id": "tb-a", "title": "제목", "body": "본문"}),
		call("add", map[string]any{"id": "tb-b", "title": "제목", "body": "본문"}),
		call("pick", map[string]any{"item_ids": []string{"tb-a", "tb-b"}}),
	)
	if len(frames2) != 3 {
		t.Fatalf("미회계 픽스처의 응답이 %d개다", len(frames2))
	}
	// 이 갈래는 isError=true 로 나가는 것이 정상이다 — 그것을 재는 시험은 따로 있다.
	unaccounted, isErr := toolText(t, frames2[2])
	if !isErr {
		t.Fatalf("미회계 갈래가 정상 응답으로 나왔다 — 픽스처가 그 갈래를 안 지났다:\n%s", unaccounted)
	}

	// ★ 꼬리 누수는 **고정 낱말로 재면 안 된다.** 처음 이 시험은 "알림"·"겹침"을 꼬리
	// 전용 표지로 삼았는데 곧바로 빨개졌다 — add 본문이 "경로가 없으면 이 항목은 겹침
	// 축에 안 잡힌다"를 낸다. 본문과 꼬리가 같은 낱말을 쓴다(internal/web 의 "디스크"가
	// 세 자리에 있던 것과 같은 구조다). 그래서 꼬리의 **실제 줄**을 그때그때 읽어 그
	// 줄이 본문에 있는지로 잰다. 문구가 바뀌어도 따라가고, 낱말 겹침에 안 속는다.
	for _, c := range []struct {
		name     string // 꼬리를 붙이는 경로의 이름
		resp     string
		bodyMark string // 그 경로의 **본문**만 내는 표지
	}{
		{"withTail(add)", okText(t, frames[0]), "add · tb-1"},
		{"RenderBoard 의 opt.Tail(board)", okText(t, frames[1]), "보드 ·"},
		{"toolPick 의 손수 연결(pick)", okText(t, frames[2]), "pick · 선점했다"},
		{"toolPick 의 미회계 갈래(pick)", unaccounted, "브랜치: tb-a"},
	} {
		resp := c.resp
		body := bodyOf(t, resp)
		tail := tailOf(t, resp)

		// ① 본문이 자기 표지를 담는다. 안 담으면 엉뚱한 자리를 잘랐다는 뜻이다.
		mustHave(t, body, c.bodyMark, c.name+" 의 본문에 본문 표지가 없다")
		// ② 본문이 꼬리의 어느 줄도 안 담는다 — 경계가 새는지를 재는 자리다.
		//    표지 줄 자체는 빼고 본다(bodyOf 가 그것으로 자르므로 동어반복이 된다).
		for _, line := range strings.Split(tail, "\n")[1:] {
			if strings.TrimSpace(line) == "" {
				continue
			}
			mustMiss(t, body, line, c.name+" 의 본문이 꼬리 줄까지 담았다 — 좁힌 척이 된다")
		}
		// ③ 꼬리는 자기 표지를 담고 본문 표지는 안 담는다(반대 방향의 누수).
		mustHave(t, tail, tailMarker, c.name+" 의 꼬리에 꼬리 표지가 없다")
		mustMiss(t, tail, c.bodyMark, c.name+" 의 꼬리가 본문을 담았다")
		// ④ 본문과 꼬리를 합치면 응답 전체가 된다 — 사이에 **아무것도 안 흘린다.**
		//    이것이 없으면 헬퍼가 가운데 한 절을 통째로 버려도 ①~③이 다 초록이다.
		if len(body)+len(tail) > len(resp) {
			t.Fatalf("%s 에서 본문(%d)+꼬리(%d)가 응답(%d)보다 길다 — 조각이 겹쳤다",
				c.name, len(body), len(tail), len(resp))
		}
		if !strings.HasSuffix(strings.TrimSpace(resp), strings.TrimSpace(tail)) {
			t.Fatalf("%s 의 꼬리가 응답의 끝이 아니다 — 꼬리 뒤에 무언가 더 있다:\n%s", c.name, resp)
		}
		// ⑤ 본문과 꼬리 사이는 **정확히 빈 줄 하나**(개행 둘)다.
		//
		// ★ **이 단정은 오늘의 결함을 안 잡았다 — 미래 위험을 막는다.** 정직하게 적는다:
		// 네 경로가 지금 다 개행 둘이다. 그런데 미회계 갈래의 코드는 `+"\n"+tail` 로
		// **하나만 적는다** — 앞의 `RenderBundleUnaccounted` 가 개행으로 끝나서
		// 결과적으로 둘이 될 뿐이다. **우연히 맞는다.**
		//
		// 실측(변이): 그 함수의 끝 개행을 지우자 이 갈래만 `개행이 1개다` 로 빨개졌다.
		// 조립을 경로마다 손으로 적는 한, 한쪽의 무관해 보이는 문구 수정이 다른 쪽의
		// 빈 줄을 지운다. 그것이 이 항목이 없애려는 구조다.
		i := strings.Index(resp, tailMarker)
		gap := len(resp[:i]) - len(strings.TrimRight(resp[:i], "\n"))
		if gap != 2 {
			t.Errorf("%s 에서 본문과 꼬리 사이 개행이 %d개다(2여야 한다) — 조립 규율이 경로마다 다르다",
				c.name, gap)
		}
	}
}

// okText 는 성공이어야 하는 응답의 본문이다. 오류면 그 자리에서 멈춘다 —
// 케이스 리터럴 안에서 isError 를 따로 받을 수 없어 이 껍질을 둔다.
func okText(t *testing.T, f outFrame) string {
	t.Helper()
	s, isErr := toolText(t, f)
	if isErr {
		t.Fatalf("성공이어야 할 응답이 오류다:\n%s", s)
	}
	return s
}
