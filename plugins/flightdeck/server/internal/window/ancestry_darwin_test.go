//go:build darwin

package window

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// darwin 에서 **계보 조인의 재료가 읽히는가.**
//
// ★ 이 시험이 지키는 것은 코드가 아니라 **판정의 근거**다. 앞선 판이 계보를
// 「❌ 맥에서 안 돈다」 한 줄로 닫았는데, 그 실측은 용법 A(부모의 **환경**에서 세션 id 를
// 읽는다)만 잰 것이었다. 용법 B — 부모 pid 를 **조인 키**로 쓰는 길 — 는 아무도 안 쟀고,
// 이 시험이 그것을 처음 쟀다(2026-09-01): **읽힌다.**
//
// 그래서 `proc_other.go` 의 `ErrUnsupported` 는 **불가능해서가 아니라 구현이 없어서**다.
// 그 구분이 화면에서 사라지면 「이 플랫폼에서는 원리적으로 안 된다」는 거짓이 문서에 남는다 —
// 실제로 한 번 남았다(DESIGN §14 ④ 의 뒤집힘 조건 4번이 그 정정이다).
//
// 이 시험이 빨개지면 그 근거가 무너진 것이다. 그때 고칠 것은 시험이 아니라 판정이다.
func TestDarwinCanReadAncestryForBeaconJoin(t *testing.T) {
	self := os.Getpid()

	kp, err := unix.SysctlKinfoProc("kern.proc.pid", self)
	if err != nil {
		t.Fatalf("SysctlKinfoProc 실패 — 이 길은 막혔다: %v", err)
	}
	ppid := int(kp.Eproc.Ppid)
	sec, usec := kp.Proc.P_starttime.Sec, kp.Proc.P_starttime.Usec
	t.Logf("self=%d ppid=%d started=%d.%06d", self, ppid, sec, usec)

	if ppid <= 0 {
		t.Fatalf("ppid 가 %d 다 — 조인 키로 못 쓴다", ppid)
	}
	if sec == 0 {
		t.Fatalf("시작 시각이 0 이다 — pid 재사용을 못 가른다")
	}

	// 조상 사슬을 끝까지 타 본다 — Ancestors 가 요구하는 것이 이 순회다.
	chain := []int{self}
	for pid, depth := self, 0; depth < 24; depth++ {
		k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
		if err != nil {
			t.Logf("깊이 %d 에서 끊겼다(pid=%d): %v", depth, pid, err)
			break
		}
		pp := int(k.Eproc.Ppid)
		if pp <= 0 || pp == pid {
			break
		}
		chain = append(chain, pp)
		pid = pp
	}
	t.Logf("조상 사슬 %d단계 %v", len(chain), chain)
	if len(chain) < 2 {
		t.Fatalf("조상을 하나도 못 탔다 — 조인이 원리적으로 안 선다")
	}

	// ★ 같은 pid 를 두 번 읽으면 **같은 시작 시각**이어야 한다 — 키가 안정적이어야
	// 훅이 심은 비콘을 MCP 가 다시 찾을 수 있다.
	again, err := unix.SysctlKinfoProc("kern.proc.pid", self)
	if err != nil {
		t.Fatalf("두 번째 읽기 실패: %v", err)
	}
	if again.Proc.P_starttime.Sec != sec || again.Proc.P_starttime.Usec != usec {
		t.Fatalf("시작 시각이 읽을 때마다 다르다(%d.%06d vs %d.%06d) — 조인 키가 못 된다",
			sec, usec, again.Proc.P_starttime.Sec, again.Proc.P_starttime.Usec)
	}
	t.Log("시작 시각이 두 번 읽어도 같다 — 조인 키로 안정적이다")
}
