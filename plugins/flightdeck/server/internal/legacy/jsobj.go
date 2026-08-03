package legacy

import (
	"fmt"
	"strconv"
	"strings"
)

// 대시보드의 DATA 블록은 JSON 이 아니라 **JS 객체 리터럴**이다.
// 키에 따옴표가 없고, 문자열은 작은따옴표이며, 주석과 뒤따르는 쉼표가 들어 있고,
// `<` 를 `\x3c` 로 이스케이프한 자리가 있다(항목 제목이 `</script>` 를 담아 페이지를
// 통째로 끊어 먹은 사고 뒤에 그렇게 바뀌었다). 그래서 encoding/json 으로는 못 읽는다.
//
// 여기서 JS 를 다 지원할 이유는 없다. 이 파서는 **DATA 블록이 실제로 쓰는 부분집합**만
// 읽고 그 밖의 것은 전부 줄 번호를 담아 거절한다 — 조용히 건너뛰면 그 자리의 값이
// 통째로 사라지고, 사라졌다는 사실이 어디에도 안 남는다(이 이관이 겨냥하는 바로 그 실패다).

// ParseJSObject 는 JS 객체 리터럴 하나를 읽는다.
//
// 반환값의 자료형은 map[string]any · []any · string · float64 · bool · nil 뿐이다.
// 실패는 **줄 번호와 무엇을 봤는지**를 담아 올린다.
func ParseJSObject(src string) (map[string]any, error) {
	p := &jsParser{src: []rune(src), line: 1}
	p.skip()
	v, err := p.value()
	if err != nil {
		return nil, err
	}
	p.skip()
	if p.i < len(p.src) {
		return nil, p.errf("객체가 끝난 뒤에 %q 가 더 있다 — DATA 블록의 경계를 잘못 잡았을 수 있다", p.peekN(24))
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("최상위가 객체가 아니다(%T)", v)
	}
	return m, nil
}

type jsParser struct {
	src  []rune
	i    int
	line int
}

func (p *jsParser) errf(format string, a ...any) error {
	return fmt.Errorf("DATA %d행: %s", p.line, fmt.Sprintf(format, a...))
}

func (p *jsParser) peekN(n int) string {
	end := p.i + n
	if end > len(p.src) {
		end = len(p.src)
	}
	return clip(string(p.src[p.i:end]), n)
}

func (p *jsParser) adv() rune {
	r := p.src[p.i]
	if r == '\n' {
		p.line++
	}
	p.i++
	return r
}

// skip 은 공백과 주석 둘(// 줄 · /* 블록 */)을 건너뛴다.
func (p *jsParser) skip() {
	for p.i < len(p.src) {
		r := p.src[p.i]
		switch {
		case r == ' ' || r == '\t' || r == '\r' || r == '\n':
			p.adv()
		case r == '/' && p.i+1 < len(p.src) && p.src[p.i+1] == '/':
			for p.i < len(p.src) && p.src[p.i] != '\n' {
				p.adv()
			}
		case r == '/' && p.i+1 < len(p.src) && p.src[p.i+1] == '*':
			p.adv()
			p.adv()
			for p.i < len(p.src) {
				if p.src[p.i] == '*' && p.i+1 < len(p.src) && p.src[p.i+1] == '/' {
					p.adv()
					p.adv()
					break
				}
				p.adv()
			}
		default:
			return
		}
	}
}

func (p *jsParser) value() (any, error) {
	if p.i >= len(p.src) {
		return nil, p.errf("값이 있어야 할 자리에서 입력이 끝났다")
	}
	switch r := p.src[p.i]; {
	case r == '{':
		return p.object()
	case r == '[':
		return p.array()
	case r == '\'' || r == '"':
		return p.str()
	case r == '-' || (r >= '0' && r <= '9'):
		return p.number()
	case r == 'n' && p.word("null"):
		return nil, nil
	case r == 't' && p.word("true"):
		return true, nil
	case r == 'f' && p.word("false"):
		return false, nil
	default:
		return nil, p.errf("값을 읽지 못했다 — %q 부터", p.peekN(24))
	}
}

// word 는 리터럴 하나를 소비한다. 뒤에 식별자 문자가 이어지면 소비하지 않는다
// (`nullish` 를 `null` 로 읽어 뒤를 통째로 어긋나게 만들지 않기 위해서다).
func (p *jsParser) word(w string) bool {
	rs := []rune(w)
	if p.i+len(rs) > len(p.src) {
		return false
	}
	for k, r := range rs {
		if p.src[p.i+k] != r {
			return false
		}
	}
	if j := p.i + len(rs); j < len(p.src) && isIdentRune(p.src[j]) {
		return false
	}
	for range rs {
		p.adv()
	}
	return true
}

func isIdentRune(r rune) bool {
	return r == '_' || r == '$' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func (p *jsParser) object() (map[string]any, error) {
	p.adv() // '{'
	out := map[string]any{}
	for {
		p.skip()
		if p.i >= len(p.src) {
			return nil, p.errf("객체가 닫히지 않았다")
		}
		if p.src[p.i] == '}' {
			p.adv()
			return out, nil
		}
		key, err := p.key()
		if err != nil {
			return nil, err
		}
		p.skip()
		if p.i >= len(p.src) || p.src[p.i] != ':' {
			return nil, p.errf("키 %q 뒤에 ':' 가 없다", clip(key, 40))
		}
		p.adv()
		p.skip()
		v, err := p.value()
		if err != nil {
			return nil, err
		}
		if _, dup := out[key]; dup {
			// 같은 키가 두 번 나오면 JS 는 뒤를 채택한다. 조용히 따라가면 앞 값이
			// 흔적 없이 사라진다 — 이관에서 그것이 곧 원문 소실이다.
			return nil, p.errf("객체에 키 %q 가 두 번 있다 — 앞 값이 조용히 사라지므로 거절한다", clip(key, 40))
		}
		out[key] = v
		p.skip()
		if p.i < len(p.src) && p.src[p.i] == ',' {
			p.adv()
			continue
		}
		if p.i < len(p.src) && p.src[p.i] == '}' {
			p.adv()
			return out, nil
		}
		return nil, p.errf("키 %q 의 값 뒤에 ',' 도 '}' 도 없다 — %q 부터", clip(key, 40), p.peekN(24))
	}
}

func (p *jsParser) key() (string, error) {
	if p.i < len(p.src) && (p.src[p.i] == '\'' || p.src[p.i] == '"') {
		return p.str()
	}
	start := p.i
	for p.i < len(p.src) && isIdentRune(p.src[p.i]) {
		p.adv()
	}
	if p.i == start {
		return "", p.errf("키를 읽지 못했다 — %q 부터", p.peekN(24))
	}
	return string(p.src[start:p.i]), nil
}

func (p *jsParser) array() ([]any, error) {
	p.adv() // '['
	out := []any{}
	for {
		p.skip()
		if p.i >= len(p.src) {
			return nil, p.errf("배열이 닫히지 않았다")
		}
		if p.src[p.i] == ']' {
			p.adv()
			return out, nil
		}
		v, err := p.value()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		p.skip()
		if p.i < len(p.src) && p.src[p.i] == ',' {
			p.adv()
			continue
		}
		if p.i < len(p.src) && p.src[p.i] == ']' {
			p.adv()
			return out, nil
		}
		return nil, p.errf("배열 원소 뒤에 ',' 도 ']' 도 없다 — %q 부터", p.peekN(24))
	}
}

// str 은 따옴표 문자열 하나를 읽는다.
//
// ★ 줄바꿈을 문자열 안에 허용하지 않는다. JS 도 허용하지 않는데, 이 파일에서
// 실제로 그 실수가 두 번 났고 페이지가 12커밋 동안 백지였다. 여기서 관대하게 받으면
// 그 결함을 이관이 조용히 흡수해 버려 원본이 깨져 있다는 사실이 안 보인다.
func (p *jsParser) str() (string, error) {
	quote := p.adv()
	var b strings.Builder
	for {
		if p.i >= len(p.src) {
			return "", p.errf("문자열이 닫히지 않았다")
		}
		r := p.adv()
		switch r {
		case quote:
			return b.String(), nil
		case '\n':
			return "", p.errf("문자열 안에서 줄이 바뀌었다 — 작은따옴표 문자열은 줄을 넘지 못한다")
		case '\\':
			if p.i >= len(p.src) {
				return "", p.errf("이스케이프가 문자열 끝에서 끊겼다")
			}
			e := p.adv()
			switch e {
			case 'n':
				b.WriteRune('\n')
			case 't':
				b.WriteRune('\t')
			case 'r':
				b.WriteRune('\r')
			case 'b':
				b.WriteRune('\b')
			case 'f':
				b.WriteRune('\f')
			case 'v':
				b.WriteRune('\v')
			case '0':
				b.WriteRune(0)
			case '\\', '\'', '"', '/', '`':
				b.WriteRune(e)
			case 'x':
				v, err := p.hex(2)
				if err != nil {
					return "", err
				}
				b.WriteRune(rune(v))
			case 'u':
				v, err := p.hex(4)
				if err != nil {
					return "", err
				}
				b.WriteRune(rune(v))
			default:
				return "", p.errf("모르는 이스케이프 \\%c — 조용히 넘기면 그 문자가 사라진다", e)
			}
		default:
			b.WriteRune(r)
		}
	}
}

func (p *jsParser) hex(n int) (int64, error) {
	if p.i+n > len(p.src) {
		return 0, p.errf("\\x·\\u 이스케이프의 자릿수가 모자라다")
	}
	s := string(p.src[p.i : p.i+n])
	v, err := strconv.ParseInt(s, 16, 32)
	if err != nil {
		return 0, p.errf("16진 이스케이프를 읽지 못했다(%q): %v", s, err)
	}
	for k := 0; k < n; k++ {
		p.adv()
	}
	return v, nil
}

func (p *jsParser) number() (float64, error) {
	start := p.i
	if p.src[p.i] == '-' {
		p.adv()
	}
	for p.i < len(p.src) {
		r := p.src[p.i]
		if (r >= '0' && r <= '9') || r == '.' || r == 'e' || r == 'E' || r == '+' || r == '-' {
			p.adv()
			continue
		}
		break
	}
	s := string(p.src[start:p.i])
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, p.errf("수를 읽지 못했다(%q): %v", clip(s, 40), err)
	}
	return f, nil
}
