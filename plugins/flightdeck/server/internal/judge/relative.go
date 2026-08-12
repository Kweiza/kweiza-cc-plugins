package judge

import "strings"

// RelativeTo 는 from 에서 응답할 때 to 로 가는 **상대 참조**다. 순수 함수다.
//
// ★ 이 함수가 있는 이유. 경로 접두를 벗겨 넘기는 리버스 프록시(`/dcp-dev-board/` 같은 것)
// 뒤에서는 서버가 접두를 모른다. 경로만 있는 절대경로(`/`)를 Location 이나 폼 action 에
// 실으면 브라우저가 접두 **밖**으로 나가고, 프록시는 응답 헤더를 고쳐 쓰지 않는다.
// 상대 참조는 접두를 **몰라도** 맞는 자리를 가리킨다 — 접두를 서버 설정으로 받는 처방을
// 기각한 이유가 이것이다(아는 순간 그 값이 배포와 어긋날 자리가 생긴다).
//
// ★ 깊이는 **마지막 슬래시까지의 슬래시 수**로 센다. RFC 3986 의 상대 해석이 문서 URL 의
// 마지막 마디를 버리고 남은 자리에 붙이기 때문이다(`/a/b` 의 기준은 `/a/`).
// 빈 마디(`//`)도 한 마디로 센다 — 해석 알고리즘이 그것을 마디로 세므로, 정규화해서
// 걷어내면 그만큼 덜 올라가 다시 없는 자리를 가리킨다.
//
// ★ **`./` 를 언제나 붙인다.** 생략하면 쿼리만 있는 목표에서 조용히 틀린다 —
// `?project=x` 는 상대 참조로서 base 의 **경로를 유지**하므로 `/dcp-dev-board/login?project=x`
// 에 착지한다. 로그인 화면으로 되돌아가는 것이고, 증상은 "토큰이 맞는데 로그인이 안 된다"
// 로 보인다. 규칙을 하나로 두어 그 함정을 구조에서 없앤다.
func RelativeTo(from, to string) string {
	// ★ to 는 이 서버 안의 절대경로여야 한다. `//` 로 시작하면 스킴 상대 URL 이라
	// 브라우저가 다른 호스트로 나간다 — 호출부의 JudgeNext 가 이미 막지만, 순수 함수는
	// 자기 방어를 진다. 못 읽은 것은 통과시키지 않고 뿌리로 접는다.
	//
	// ★ 점 마디도 막는다. `/../../etc` 같은 값은 호스트를 안 바꾸므로 오픈 리다이렉트는
	// 아니지만 접두 **밖**으로 나간다 — 이 함수가 막으려는 것이 정확히 그것이다.
	// 호출부의 JudgeNext 는 url.Parse 와 RequestURI() 를 쓰는데 그 둘이 점 마디를
	// 정규화하지 않아 그대로 통과시킨다(실측). 그래서 이 자리에서 막는다.
	if !strings.HasPrefix(to, "/") || strings.HasPrefix(to, "//") || strings.Contains(to, "..") {
		return "./"
	}
	rest := to[1:]

	// ★ from 의 점 마디를 먼저 걷어낸다. 브라우저는 base URL 을 RFC 3986 §5.2.4 의
	// remove_dot_segments 로 정규화한 **뒤** 상대 참조를 해석하기 때문이다. 안 걷어내면
	// `/actions/../` 같은 요청에서 깊이가 실제보다 크게 나와 접두 **밖**으로 나간다(실측).
	//
	// ★ 뒤 슬래시를 보존한다. RFC 의 remove_dot_segments("/a/b/../") 는 "/a/" 인데
	// path.Clean 은 "/a" 를 낸다 — 그 한 칸 차이가 깊이를 하나 어긋내고, 어긋난 깊이는
	// 접두 밖 착지로 나타난다.
	from = removeDotSegments(from)

	// ★ 슬래시가 아예 없는 from(빈 문자열 · OPTIONS 의 `*`)은 뿌리로 본다. 못 읽은 것을
	// 깊이 0 으로 접는 쪽이 안전하다 — 그 경우의 최악은 "이 이상한 자리에서만 안 통한다"
	// 이고, 반대로 과하게 올라가면 접두 **밖**으로 나가 프록시 배포 전체가 깨진다.
	depth := 0
	if slash := strings.LastIndex(from, "/"); slash >= 0 {
		depth = strings.Count(from[:slash+1], "/") - 1
	}
	if depth <= 0 {
		return "./" + rest
	}
	return strings.Repeat("../", depth) + rest
}

// removeDotSegments 는 RFC 3986 §5.2.4 의 remove_dot_segments 알고리즘을 그대로 옮긴 것이다.
// 점 마디(`.` · `..`)만 걷어내고 그 밖의 모든 마디는 손대지 않는다.
//
// ★ path.Clean 을 안 쓰는 이유. path.Clean 은 빈 마디(`//`)까지 하나로 접는다
// (`path.Clean("/a//b") == "/a/b"`) — 그런데 TestRelativeTo 의
// `{"/a//b", "/login", "../../login"}` 은 빈 마디를 **한 마디로 세는** 해석 규칙을
// 이미 잠갔다(브라우저의 상대 참조 해석 알고리즘이 빈 마디는 안 건드리고 점 마디만
// 걷기 때문이다). path.Clean 을 쓰면 그 시험이 깨진다 — 그래서 RFC 알고리즘을 손으로
// 옮겨 점 마디만 걷고 빈 마디는 그대로 둔다.
//
// ★ 뒤 슬래시가 살아남는다. remove_dot_segments("/a/b/../") 는 "/a/" 다 — 마지막
// "/../" 를 걷어낸 자리에 "/" 하나가 그대로 남기 때문이다(아래 스위치의 "/../" 갈래가
// 입력을 "/" 로 바꾸고, 그 "/" 가 다음 순회에서 output 에 그대로 옮겨진다). path.Clean
// 은 이 자리에서 "/a" 를 내 뒤 슬래시를 잃는다 — RelativeTo 의 위 주석이 말하는
// "한 칸 차이"가 이것이다.
func removeDotSegments(path string) string {
	var out string
	in := path
	for in != "" {
		switch {
		case strings.HasPrefix(in, "../"):
			in = in[3:]
		case strings.HasPrefix(in, "./"):
			in = in[2:]
		case strings.HasPrefix(in, "/./"):
			in = "/" + in[3:]
		case in == "/.":
			in = "/"
		case strings.HasPrefix(in, "/../"):
			in = "/" + in[4:]
			out = dropLastSegment(out)
		case in == "/..":
			in = "/"
			out = dropLastSegment(out)
		case in == "." || in == "..":
			in = ""
		default:
			// 첫 마디를 output 으로 옮긴다 — 맨 앞의 "/"(있으면) 와 다음 "/" 전까지
			// (없으면 끝까지). 빈 마디("//" 의 사이)도 이 갈래를 타서 그대로 옮겨진다.
			end := 0
			if strings.HasPrefix(in, "/") {
				end = 1
			}
			if next := strings.Index(in[end:], "/"); next >= 0 {
				end += next
			} else {
				end = len(in)
			}
			out += in[:end]
			in = in[end:]
		}
	}
	return out
}

// dropLastSegment 는 output 버퍼에서 마지막 마디와 그 앞의 "/" 를 걷어낸다 —
// remove_dot_segments 가 "/../" 를 만났을 때 하는 일이다.
func dropLastSegment(out string) string {
	if i := strings.LastIndex(out, "/"); i >= 0 {
		return out[:i]
	}
	return ""
}
