package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// 바이너리 캐시의 상한 — `~/.cache/flightdeck/bin/fd-<접은 소스 트리>` 를 몇 벌까지 두는가.
//
// 이 파일이 존재하는 이유는 **이름에 소스 트리가 박혔기 때문**이다(런처 bin/fd 의 자리 블록).
// 키의 입력인 `$CLAUDE_PLUGIN_ROOT/server` 에는 플러그인 **버전이 들어가 있고**, 이 저장소는
// 이틀에 여섯 번 릴리스했다 — 거기에 워크트리마다 제 이름을 갖는 것까지 더하면 자리 수는
// 시간에 비례해 는다. 한 벌이 22MB 다. 런처는 자기 이름 하나만 알고 남의 이름은 모르니
// 상한을 잡을 수 있는 자리는 여기뿐이고, 없으면 22MB×N 이 영원히 쌓인다(keep 3 이면 ~66MB).
//
// ★ 자리를 계산하는 규칙은 여기 없다. 그것은 BinCacheDir(env.go)와 런처가 갖는다.
// 이 파일은 **주어진 디렉토리 안에서 무엇을 지울지**만 판정한다.

// binCacheKeep 는 남길 벌 수다.
//
// 3 인 이유: 한 머신에서 동시에 살아 있는 소스 트리는 대개 「설치본 + 지금 워크트리」 둘이고,
// 릴리스 직후에는 옛 설치본이 잠깐 하나 더 걸친다. 2 로 하면 그 겹치는 순간 매번 재빌드가
// 뜨고, 크게 잡으면 상한이 상한 노릇을 못 한다.
const binCacheKeep = 3

// binCachePrefix 는 런처가 짓는 산출물의 접두다. 이름에서 **이것만** 본다.
const binCachePrefix = "fd-"

// BinEntry 는 바이너리 캐시 디렉토리의 항목 하나다. 판정에 필요한 두 축만 담는다.
//
// 이름을 안 담고 경로를 담는 이유: 지울 대상을 낼 때 호출부가 디렉토리를 다시 붙이면
// 그 조립 규칙이 두 자리에 살게 된다. 여기서 낸 것을 그대로 os.Remove 에 넘긴다.
type BinEntry struct {
	Path    string
	ModTime time.Time
}

// PruneBinCache 는 바이너리 캐시에서 **지울 경로**를 고른다. 순수 함수다.
//
// 환경 조회도 파일시스템 접근도 이 안에 없다 — env.go:18-20 의 규율이다. 디렉토리를 읽는
// 일은 readBinCache 가 하고, 지우는 일은 pruneBinCache 가 한다. 판정만 여기 산다.
//
// 규칙은 셋이다.
//
//   - `fd-` 접두인 항목만 지운다. **남의 파일일 수 있다** — FD_STATE_DIR 를 켠 사용자는
//     그 디렉토리를 자기 것과 나눠 쓸 수 있고, 그때 이름을 안 가리는 GC 는 청소가 아니라
//     파괴다. 접두를 안 가진 옛 이름(`fd`)도 이 규칙 덕에 그대로 남는다 — 옛 자리를
//     지우지도 옮기지도 않는다는 결정(설계 migration 절)과 같은 자리다.
//   - mtime 최신 keep 벌은 남긴다. mtime 은 '최근에 쓰였다'의 대리 지표다: 런처가 다시 지을
//     때마다 `mv -f` 가 새 시각을 찍으므로, 실제로 도는 채널들이 쓰는 자리가 위쪽에 모인다.
//   - self 는 keep 밖이어도 **무조건** 남긴다(아래).
//
// ★ **키를 해독하지 않는다.** 이름에서 소스 경로를 되짚는 역함수를 여기 두면 접음 규칙이
// 두 벌이 되고(런처의 셸 치환 + Go 의 역치환), 한쪽만 고치는 날 조용히 어긋난다 —
// 이 저장소가 이미 세 번 겪은 사고다(client.go 의 newClient 주석). 그래서 이 함수는
// 어느 항목이 어느 트리 것인지 **영영 모른다.** 알 필요도 없다: 여기서 재는 축은 나이다.
//
// ★ 런처의 임시 파일(`tmp="$bin.$$"`)도 `fd-` 접두를 갖는다. 그것을 이름으로 가려내지
// 않는다 — 가려내려면 키의 문법을 여기서 다시 정의해야 하고, 그게 방금 금지한 둘째 사본이다.
// 대신 mtime 이 막는다: 짓는 중인 파일은 항상 가장 새것이라 keep 안에 든다. 설령 지워져도
// (서로 다른 트리 넷이 같은 순간에 빌드하는 경우) 빌더는 제 fd 를 계속 쥐어 빌드는 끝나고
// `mv` 만 실패하며, 런처는 그 실패에 종료코드 0 으로 답한다(bin/fd 의 fail-open).
//
// ★ **살아 있는 세션의 바이너리를 지워도 안전하다.** unlink·rename 은 디렉토리 엔트리만
// 건드리고 inode 는 안 건드리므로, 이미 exec 된 프로세스는 제 판을 끝까지 문다 —
// 리눅스는 그 사실을 커널 링크(`/proc/<pid>/exe -> …(deleted)`)에 그대로 적어 둔다 —
// 다만 **우리 코드가 그 표식을 보는 일은 없다**(os.Executable 이 읽는 그 자리에서 먼저 뗀다.
// 근거·실측표는 exe.go 의 deletedSuffix ★ 하나에 있고, 여기 다시 적지 않는다).
// 다음 호출은 파일이 없는 것을 보고 다시 짓는다(웜 0.5~1.3초).
// 재빌드는 예외가 아니라 런처의 **정상 경로**다: 판정이 mtime 이고 플러그인 설치가 소스
// mtime 을 설치 시각으로 찍으므로 릴리스마다 어차피 다시 짓는다. 그래서 이 GC 의 최악은
// '어느 세션이 1초 더 기다린다'이지 '무엇을 잃는다'가 아니다.
//
// ★ **그 '최악'은 한 구간을 안 세고 한 말이다 — exec 직전.** 위 inode 논증은 **이미 exec 된**
// 프로세스에만 선다. 런처가 `-x` 로 파일을 보고 `exec` 을 부르기까지는 아직 아무도 inode 를
// 안 물었으므로, 그 창에 이 GC 가 지우면 exec 자체가 ENOENT 로 실패하고 bash 는 127 로 죽는다.
// 창은 syscall 두 개 사이다 — 실측 ~35µs(strace 아래 255µs), 런처 1회(~1.7ms)의 2%. 초당
// 200회 지우는 적대적 루프로 증폭해야 재현된다(ELF 대상 7,126회 중 127 이 29회 = 0.41%).
// 자연 조건에서는 이 GC 가 SessionStart 마다 몇 건이고 그것도 트리가 넷 이상일 때뿐이라
// 핫 키 삭제 한 건당 1e-4 급이다. 그 앞의 훨씬 넓은 창(빌드 판정 뒤 `find` 14ms)은 런처의
// `-x` 재검사가 잡아 **종료코드 0** 으로 끝난다 — 거기서 남는 것은 사유 문구뿐이다.
//
// **여기서 막지 않는다.** 막으려면 "지금 누가 이 파일을 exec 하려는 중인가"를 알아야 하는데
// 그걸 아는 자리가 없고, 락을 새로 놓는 것은 22MB 를 아끼자고 훅 경로에 잠금을 하나 더 다는
// 교환이다. 창의 양쪽 끝이 둘 다 런처의 줄이니 **창의 주인도 런처**이고, 닫는다면 닫는 자리도
// 거기다 — 그때 쓰는 것은 `shopt -s execfail` 이다. bash 는 그것 없이는 exec 실패에서 `||` 를
// 보기도 전에 비대화형 셸을 끝내므로 `|| { …; exit 0; }` 만으로는 **안 잡힌다**(실측).
// 이 파일이 지는 몫은 **그 창을 안 적힌 채로 두지 않는 것**이다.
//
// ★ 이 창이 깨는 계약이 무엇인지도 정확히 적는다 — 런처 머리의 대문자 계약은 "**Go 가 없어도**
// 종료코드 0"이지 "항상 0"이 아니었다. exec 뒤의 종료코드는 애초에 fd 의 것이다(`fd land` 는
// 내 차례가 아니면 **의도적으로 1**). 그래서 이것은 랜딩을 막는 축이 아니라 **알고 있어야 하는
// 창**이다: 자가 치유되고(다음 호출이 다시 짓는다) 방향도 `fd land && <랜딩>` 에는 안전한 쪽이다.
//
// 그런데도 self 를 남기는 이유는 안전이 아니라 **낭비**다. 지금 이 프로세스가 도는 자리는
// 방금 쓰인 자리이므로 곧 또 쓰인다. 그것이 keep 밖으로 밀렸다는 것은 그사이 다른 트리가
// 셋 이상 지어졌다는 뜻인데(동시 세션이 20~30건이면 흔하다), 그때 이 자리를 지우면 다음
// 훅이 같은 것을 다시 짓는다 — 22MB 를 아끼려고 매 훅마다 1초를 태우는 교환이다.
//
// mtime 동률은 **경로 오름차순**으로 깬다. 한 훅이 지은 여러 벌이 같은 시각을 갖는 자리가
// 있고(파일시스템에 따라 mtime 해상도가 1초다), 순서를 안 정해 두면 같은 입력이 실행마다
// 다른 답을 낸다 — 시험이 못 잠그는 함수가 되고, 매번 다른 것이 지워진다.
//
// 지울 것이 없으면 **nil** 이다. 빈 슬라이스와 섞어 내지 않는다 — 두 표현이 섞이면
// reflect.DeepEqual 로 단정하는 시험이 내용이 아니라 표현 때문에 갈린다.
func PruneBinCache(entries []BinEntry, keep int, self string) []string {
	if keep < 0 {
		// ★ **이 줄은 지금 거동을 안 바꾼다.** 아래 비교가 `i < keep`(i≥0)라 음수는 keep 0 과
		// 답이 같다 — 전부 지운다. 이 줄만 지우는 뮤테이션은 시험이 못 잡는다(실제로 돌려
		// 확인했다). 그러니 앞서 여기 적혀 있던 "음수를 그대로 쓰면 '전부 유지'로 뒤집힌다"는
		// **거꾸로**였다. 음수가 가는 방향은 전부 유지(무해)가 아니라 **캐시 전멸**이다.
		//
		// 그런데도 남기는 이유는 입구에서 뜻을 못 박기 위해서다. 비교 방식이 바뀌는 날
		// (`len(cand)-keep` 로 뒤에서 세거나 `cand[keep:]` 로 자르는 순간) 음수는 곧장 다른
		// 뜻이 되거나 panic 이 된다. 그때 지켜야 하는 계약은 "음수 keep 은 keep 0 과 **같은
		// 답**"이고, 그 계약 쪽은 bincache_test.go 의 표가 잰다 — 이 줄은 그 답을 앞으로도
		// 내게 하는 구현이지 계약 자체가 아니다.
		keep = 0
	}

	// self 에 붙은 커널 표식을 뗀다 — **방어로만 산다.** 이 자리에는 한때 "이 GC 가 한 번
	// 돌고 나면 이 프로세스 자신이 지워진 자리를 도는 상태가 되고, 그때 os.Executable 은
	// `…/fd-… (deleted)` 를 준다"고 적혀 있었다. **앞 절반만 참이다.** 상태는 정말 그렇게
	// 되지만 표식은 안 올라온다 — stdlib 이 링크를 읽은 그 자리에서 먼저 뗀다(exe.go 의
	// deletedSuffix ★ 가 그 판정과 실측표의 주인이다. 표식 상수도 그쪽 것이라 여기 문자열을
	// 다시 적지 않는다). 실물 호출부가 넘기는 self 는 os.Executable 의 값이므로 아래
	// CutSuffix 는 한 번도 안 걸린다.
	//
	// 그런데도 부르는 이유 둘. ⑴ 안 걸리면 **항등**이라 값이 0 이다. ⑵ 이 함수는 self 를
	// 인자로 받는 순수 함수라, 언젠가 호출부가 readlink 한 값을 넘기면 그날 이쪽은 이미 옳다.
	//
	// ★ self 보호가 실제로 꺼지는 갈래는 표식이 아니라 **표기**다 — 푼 경로와 안 푼 경로가
	// 문자열로 안 맞는 쪽이고, 그것은 아래 심볼릭 링크 ★ 가 따로 답한다. 표식을 근거로
	// 세워 두면 진짜 위험이 이미 처리된 것처럼 읽힌다.
	selfPath := ""
	if s := strings.TrimSpace(self); s != "" {
		s, _ = strings.CutSuffix(s, deletedSuffix)
		selfPath = filepath.Clean(s)
	}

	// ★ 심볼릭 링크를 풀지 않는다(EvalSymlinks 는 파일시스템을 만지고, 이 함수는 순수하다).
	//   못 알아본 경우의 결과는 '지워도 되는 것을 안 지운다'가 아니라 '남길 것을 지운다'인데,
	//   그것이 안전한 이유가 바로 위 inode 논의다 — 그래서 여기서 그 값을 치를 수 있다.
	cand := make([]BinEntry, 0, len(entries))
	for _, e := range entries {
		p := strings.TrimSpace(e.Path)
		if p == "" || !strings.HasPrefix(filepath.Base(p), binCachePrefix) {
			continue
		}
		cand = append(cand, BinEntry{Path: filepath.Clean(p), ModTime: e.ModTime})
	}
	sort.Slice(cand, func(i, j int) bool {
		if !cand[i].ModTime.Equal(cand[j].ModTime) {
			return cand[i].ModTime.After(cand[j].ModTime)
		}
		return cand[i].Path < cand[j].Path
	})

	var out []string
	for i, e := range cand {
		if i < keep {
			continue
		}
		if selfPath != "" && e.Path == selfPath {
			continue
		}
		out = append(out, e.Path)
	}
	return out
}

// readBinCache 는 디렉토리 하나를 읽어 판정의 입력을 만든다. 얇은 어댑터다.
//
// ★ **여기에 판정을 두지 않는다.** 이름 조건 하나를 이 반복문에 얹는 순간 규칙이 두 자리에
// 살게 되고, 그러면 PruneBinCache 의 시험이 실제 거동을 더는 안 잠근다. 여기서 거르는 것은
// '판정할 수 없는 것'(디렉토리·사라진 파일)뿐이다.
//
// 디렉토리가 없는 것은 **결함이 아니다** — 이 머신이 아직 아무것도 안 지었거나 HOME 이
// 없어 런처가 짓기를 거절한 상태다(window.Prune 이 같은 자리에서 같은 답을 낸다).
func readBinCache(dir string) ([]BinEntry, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("바이너리 캐시 디렉토리를 못 읽었다(%s): %w", dir, err)
	}
	out := make([]BinEntry, 0, len(ents))
	for _, e := range ents {
		if e.IsDir() {
			continue // 런처는 여기에 디렉토리를 안 짓는다. 남의 트리를 재귀로 지우지 않는다
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue // 훑는 사이 사라졌다 — 다른 세션의 `mv -f` 다. 정상 경로이지 오류가 아니다
		}
		out = append(out, BinEntry{Path: filepath.Join(dir, e.Name()), ModTime: info.ModTime()})
	}
	return out, nil
}

// pruneBinCache 는 바이너리 캐시의 상한을 실제로 잡는다.
//
// ★ **훅에서만 한다.** hook.go 의 pruneWindows 옆이 그 자리이고 이유도 같다 —
// SessionStart 타임아웃이 10초(plugins/flightdeck/hooks/hooks.json)라 디렉토리 하나를 훑을
// 여유가 있는 쪽이고, MCP 는 도구 응답 지연에 민감하다(그 지연은 매 도구 호출마다 사람이
// 기다리는 시간이다). 여기는 그 선례보다 더 싸다: 파일 몇 개의 stat 이고 내용은 안 읽는다.
//
// 실패는 Debug 로만 남긴다. 캐시가 안 잘린 것에 대해 사용자가 지금 할 수 있는 일이 없고
// (자리와 나이는 fd doctor 가 말로 찍는다), 화면에 얹으면 매 세션 시작마다 같은 줄이 뜬다.
func (a *App) pruneBinCache() {
	if a.binDir == "" {
		return // HOME 도 FD_STATE_DIR 도 없다 — 그때는 런처도 아무것도 안 짓는다(BinCacheDir)
	}
	ents, err := readBinCache(a.binDir)
	if err != nil {
		a.log.Debug("바이너리 캐시를 못 읽었다", "dir", a.binDir, "error", err.Error())
		return
	}
	// os.Executable 이 실패하면 self 는 빈 문자열이고 그 실행분에는 self 보호가 없다.
	// **그대로 진행한다** — 최악이 재빌드 한 번(0.5~1.3초)이라, 못 읽었다는 이유로 GC 를
	// 통째로 끄면 잃는 쪽(22MB×N 무한 누적)이 명백히 더 크다.
	self, _ := os.Executable()
	for _, p := range PruneBinCache(ents, binCacheKeep, self) {
		if rerr := os.Remove(p); rerr != nil && !os.IsNotExist(rerr) {
			a.log.Debug("바이너리 캐시 항목을 못 지웠다", "path", p, "error", rerr.Error())
		}
	}
}
