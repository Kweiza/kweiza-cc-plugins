package store

import (
	"context"
	"testing"
)

func countDeploys(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM event WHERE kind='server.deploy'`).Scan(&n); err != nil {
		t.Fatalf("배포 이벤트 세기 실패: %v", err)
	}
	return n
}

// 같은 실행 파일로 몇 번을 다시 떠도 배포는 **한 번**이다.
//
// ★ 이것이 이 축의 전부다. 기동마다 행을 남기면 "마지막 기동"과 "마지막 배포"가 같아져
// 배포 시각이라는 축이 사라진다 — 서버는 갱신 말고도 여러 이유로 다시 뜬다(수동 재기동,
// 머신 재부팅, 컨테이너 재시작). 실행 파일이 바뀐 기동만 세야 그 축이 뜻을 갖는다.
func TestNoteServerBuildRecordsOnlyWhenBinaryChanged(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	// 첫 관측은 배포다 — 그 이전을 원장이 모르므로 여기서부터 셀 수 있다.
	deployed, err := s.NoteServerBuild(ctx, "ino=1 size=100 mtime=10")
	if err != nil {
		t.Fatalf("NoteServerBuild 첫 호출: %v", err)
	}
	if !deployed {
		t.Error("첫 관측이 배포로 안 잡혔다 — 원장에 기준선이 없으면 그 시점이 기준선이다")
	}
	if n := countDeploys(t, s); n != 1 {
		t.Fatalf("배포 이벤트 %d건, 원하는 것 1", n)
	}

	// 같은 실행 파일로 두 번 더 뜬다 — 재기동이지 배포가 아니다.
	for i := 0; i < 2; i++ {
		deployed, err := s.NoteServerBuild(ctx, "ino=1 size=100 mtime=10")
		if err != nil {
			t.Fatalf("NoteServerBuild 재기동 %d: %v", i, err)
		}
		if deployed {
			t.Errorf("재기동 %d 이 배포로 잡혔다 — 실행 파일이 그대로다", i)
		}
	}
	if n := countDeploys(t, s); n != 1 {
		t.Errorf("배포 이벤트 %d건, 원하는 것 1 — 재기동마다 행이 늘면 이 축이 '마지막 기동'이 된다", n)
	}

	// 실행 파일이 바뀌면 배포다.
	deployed, err = s.NoteServerBuild(ctx, "ino=2 size=180 mtime=20")
	if err != nil {
		t.Fatalf("NoteServerBuild 새 빌드: %v", err)
	}
	if !deployed {
		t.Error("실행 파일이 바뀌었는데 배포로 안 잡혔다")
	}
	if n := countDeploys(t, s); n != 2 {
		t.Errorf("배포 이벤트 %d건, 원하는 것 2", n)
	}
}

// 관측 못 한 실행 파일은 배포로 세지 않는다.
//
// ★ ExeID 는 관측 실패를 "관측 안 됨" 문자열로 낸다(selfwatch.go). 그것을 정체로 받으면
// 관측이 흔들릴 때마다 가짜 배포가 남고, 그 시각으로 자른 지표가 근거 없이 리셋된다.
// 모르는 것은 같은 것도 다른 것도 아니다 — 아무 말도 안 하는 것이 맞다.
func TestNoteServerBuildIgnoresUnobservedBinary(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	for _, exe := range []string{"", "   "} {
		deployed, err := s.NoteServerBuild(ctx, exe)
		if err != nil {
			t.Fatalf("NoteServerBuild(%q): %v", exe, err)
		}
		if deployed {
			t.Errorf("빈 정체(%q)가 배포로 잡혔다", exe)
		}
	}
	if n := countDeploys(t, s); n != 0 {
		t.Errorf("배포 이벤트 %d건, 원하는 것 0 — 관측 못 한 것을 배포로 적었다", n)
	}

	// 그 뒤 진짜 관측이 오면 그것은 첫 배포다.
	if deployed, err := s.NoteServerBuild(ctx, "ino=1 size=1 mtime=1"); err != nil || !deployed {
		t.Fatalf("관측된 첫 빌드가 배포로 안 잡혔다(deployed=%v err=%v)", deployed, err)
	}
}

// LastDeployAt 은 **0 과 못 잼을 가른다.**
func TestLastDeployAtSeparatesUnknownFromZero(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	if _, ok, err := s.LastDeployAt(ctx); err != nil {
		t.Fatalf("LastDeployAt(빈 원장): %v", err)
	} else if ok {
		t.Error("배포 기록이 없는데 ok=true 다 — 영값 시각을 '배포 시각'으로 읽으면 전 역사가 창이 된다")
	}

	if _, err := s.NoteServerBuild(ctx, "ino=1 size=1 mtime=1"); err != nil {
		t.Fatalf("NoteServerBuild: %v", err)
	}
	first, ok, err := s.LastDeployAt(ctx)
	if err != nil || !ok {
		t.Fatalf("첫 배포 뒤 LastDeployAt(ok=%v err=%v)", ok, err)
	}
	if first.IsZero() {
		t.Error("배포 시각이 영값이다")
	}

	// 새 빌드가 오면 **마지막** 배포로 옮겨간다.
	if _, err := s.NoteServerBuild(ctx, "ino=2 size=2 mtime=2"); err != nil {
		t.Fatalf("NoteServerBuild 2: %v", err)
	}
	second, ok, err := s.LastDeployAt(ctx)
	if err != nil || !ok {
		t.Fatalf("둘째 배포 뒤 LastDeployAt(ok=%v err=%v)", ok, err)
	}
	if second.Before(first) {
		t.Errorf("마지막 배포가 뒤로 갔다: %v → %v", first, second)
	}
}
