package main

import (
	"context"
	"strings"
	"testing"
)

// `--body -` 는 **세 명령 전부**에서 stdin 을 읽는다.
//
// ★ 좌표계가 "서버가 실제로 갖게 된 body" 인 이유: 플래그 파싱만 보는 시험은 이 축을
// 원리적으로 못 본다. add 는 도움말에 `-` 를 선언해 놓고 읽는 코드가 없었는데,
// 플래그는 정상적으로 파싱됐다 — 파싱은 맞았고 **그 값을 안 쓴 것**이 결함이었다.
// 그래서 오류도 안 나고 `-` 한 글자가 본문으로 저장됐다.
//
// 실물 피해: 그렇게 등록된 항목(fd-item-move)은 고칠 방법이 없어 폐기됐는데
// **id 가 전역 유일이라 회수되지 않아 그 이름이 영구히 죽었다.** fd-item-relocate 는
// 그래서 생긴 이름이다. 이 시험은 그 사고가 다시 나지 않게 하는 자리다.

func TestBodyDashReadsStdinInEveryCommand(t *testing.T) {
	const body = "stdin 으로 들어온 본문\n둘째 줄도 온전해야 한다"

	t.Run("add", func(t *testing.T) {
		h := newHarness(t)
		code, out := h.runEnv(h.env, body, "add", "--id", "t-stdin-1", "--title", "제목", "--body", "-")
		if code != 0 {
			t.Fatalf("add 가 %d 로 끝났다:\n%s", code, out)
		}
		it, err := h.st.GetItem(context.Background(), h.project, "t-stdin-1")
		if err != nil {
			t.Fatalf("항목 조회 실패: %v", err)
		}
		// ★ 이 단정이 이 항목의 본체다. 앞서 여기 들어 있던 값은 "-" 한 글자였다.
		if it.Body != body {
			t.Errorf("서버가 가진 본문이 다르다.\n원한 것: %q\n실제:   %q\n"+
				"%q 라면 stdin 을 아예 안 읽은 것이다 — 플래그 값을 그대로 저장했다는 뜻이다.",
				body, it.Body, "-")
		}
	})

	t.Run("note", func(t *testing.T) {
		h := newHarness(t)
		code, out := h.runEnv(h.env, body, "note", "--kind", "decision", "--body", "-")
		if code != 0 {
			t.Fatalf("note 가 %d 로 끝났다:\n%s", code, out)
		}
		js := h.judgments("decision")
		if len(js) != 1 {
			t.Fatalf("판단이 %d건이다 — 1건이어야 한다", len(js))
		}
		if js[0].Body != body {
			t.Errorf("서버가 가진 본문이 다르다.\n원한 것: %q\n실제:   %q", body, js[0].Body)
		}
	})

	t.Run("finish", func(t *testing.T) {
		h := newHarness(t)
		if code, out := h.runEnv(h.env, "", "add", "--id", "t-stdin-2", "--title", "제목",
			"--body", "본문"); code != 0 {
			t.Fatalf("전제 구성 실패 — add 가 %d 로 끝났다:\n%s", code, out)
		}
		if code, out := h.runEnv(h.env, "", "pick", "t-stdin-2"); code != 0 {
			t.Fatalf("전제 구성 실패 — pick 이 %d 로 끝났다:\n%s", code, out)
		}
		code, out := h.runEnv(h.env, body, "finish", "t-stdin-2", "--outcome", "done",
			"--title", "끝", "--body", "-")
		if code != 0 {
			t.Fatalf("finish 가 %d 로 끝났다:\n%s", code, out)
		}
		js := h.judgments("handoff")
		if len(js) != 1 {
			t.Fatalf("핸드오프가 %d건이다 — 1건이어야 한다", len(js))
		}
		if js[0].Body != body {
			t.Errorf("서버가 가진 본문이 다르다.\n원한 것: %q\n실제:   %q", body, js[0].Body)
		}
	})
}

// `-` 가 **아니면** stdin 을 안 읽는다.
//
// ★ 이 축을 안 지키면 이 항목을 고치다가 더 나쁜 결함을 되살린다: 앞선 판은 본문이 비면
// stdin 을 EOF 까지 읽어서 **stdin 이 열려 있는 곳에서 영원히 멈췄고**(훅·Bash 도구가 그 환경),
// 훅 경로에서는 훅 JSON 페이로드를 판단 본문으로 삼았다.
func TestBodyWithoutDashNeverTouchesStdin(t *testing.T) {
	h := newHarness(t)
	const stdin = "이 문자열이 본문이 되면 안 된다"

	code, out := h.runEnv(h.env, stdin, "add", "--id", "t-nodash", "--title", "제목", "--body", "직접 준 본문")
	if code != 0 {
		t.Fatalf("add 가 %d 로 끝났다:\n%s", code, out)
	}
	it, err := h.st.GetItem(context.Background(), h.project, "t-nodash")
	if err != nil {
		t.Fatalf("항목 조회 실패: %v", err)
	}
	if it.Body != "직접 준 본문" {
		t.Errorf("본문이 %q 다 — 플래그로 준 값이어야 한다", it.Body)
	}
	if strings.Contains(it.Body, stdin) {
		t.Error("stdin 내용이 본문에 섞였다 — `-` 없이 stdin 을 읽었다")
	}
}

// 도움말 문구와 동작이 **한 자리에서 온다**.
//
// 이 시험이 지키는 것은 문구 자체가 아니라 **사본이 늘지 않는 것**이다. 앞서 add 는 문구를
// 걸어 놓고 안 읽었고, note 는 코드를 고친 뒤에도 문구가 옛 동작("비면 읽는다")을 말했다.
func TestBodyHelpTextIsSharedAndMatchesBehaviour(t *testing.T) {
	if !strings.Contains(bodyFlagHelp, "-") || !strings.Contains(bodyFlagHelp, "stdin") {
		t.Errorf("도움말이 `-` 와 stdin 을 안 말한다: %q", bodyFlagHelp)
	}
	// 옛 문구가 되살아나면 잡는다 — "비면 읽는다"는 위 시험이 금지하는 동작이다.
	if strings.Contains(bodyFlagHelp, "비면") {
		t.Errorf("도움말이 '비면 읽는다'고 말한다 — 코드는 `-` 일 때만 읽는다: %q", bodyFlagHelp)
	}
}
