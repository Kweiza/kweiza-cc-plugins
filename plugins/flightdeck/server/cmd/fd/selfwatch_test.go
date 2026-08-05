package main

import (
	"errors"
	"strings"
	"testing"
)

func id(ino uint64, mtime int64) ExeID {
	return ExeID{OK: true, Dev: 1, Ino: ino, Size: 100, MtimeNano: mtime}
}

func TestDecideDoesNothingWhenUnchanged(t *testing.T) {
	a := id(10, 1000)
	got, why := Decide(a, a, ExeID{}, nil)
	if got != ActNothing {
		t.Fatalf("안 바뀌었는데 %v 다 — %s", got, why)
	}
}

func TestDecideVerifiesWhenInodeChanged(t *testing.T) {
	got, why := Decide(id(10, 1000), id(11, 1000), ExeID{}, nil)
	if got != ActVerify {
		t.Fatalf("아이노드가 바뀌었는데 %v 다 — %s", got, why)
	}
	if strings.TrimSpace(why) == "" {
		t.Fatal("사유가 비었다")
	}
}

func TestDecideVerifiesWhenOnlyMtimeChanged(t *testing.T) {
	// 같은 자리에 같은 크기로 덮어써도 교체다. mv 가 아니라 cp 로 배포하는 경로가 있다.
	if got, _ := Decide(id(10, 1000), id(10, 2000), ExeID{}, nil); got != ActVerify {
		t.Fatalf("mtime 만 바뀌었는데 %v 다", got)
	}
}

// ★ stat 실패는 교체가 아니다. exec 할 대상이 없는데 exec 로 가면 서버가 사라진다.
func TestDecideDoesNothingWhenStatFails(t *testing.T) {
	got, why := Decide(id(10, 1000), ExeID{}, ExeID{}, errors.New("no such file"))
	if got != ActNothing {
		t.Fatalf("stat 이 실패했는데 %v 다 — %s", got, why)
	}
	if !strings.Contains(why, "no such file") {
		t.Fatalf("사유가 원인을 안 나른다: %q", why)
	}
}

// ★ 같은 고장난 바이너리를 30초마다 다시 돌리지 않는다.
func TestDecideSkipsAlreadyFailedBuild(t *testing.T) {
	bad := id(11, 1000)
	if got, _ := Decide(id(10, 1000), bad, bad, nil); got != ActNothing {
		t.Fatalf("이미 실패한 판인데 %v 다", got)
	}
}

// 사람이 고쳐서 파일이 **또** 바뀌면 다시 시도한다.
func TestDecideRetriesAfterTheFileChangesAgain(t *testing.T) {
	bad := id(11, 1000)
	if got, _ := Decide(id(10, 1000), id(12, 3000), bad, nil); got != ActVerify {
		t.Fatalf("고친 뒤인데 %v 다", got)
	}
}

func TestSameRequiresBothOK(t *testing.T) {
	a := id(10, 1000)
	if a.Same(ExeID{}) {
		t.Fatal("관측 안 된 값과 같다고 했다")
	}
	if (ExeID{}).Same(ExeID{}) {
		t.Fatal("둘 다 관측 안 됐는데 같다고 했다 — 그것은 '같다'가 아니라 '모른다'다")
	}
}
