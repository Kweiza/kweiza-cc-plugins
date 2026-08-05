package judge

import (
	"reflect"
	"testing"
)

// 이 저장소의 실제 배치를 그대로 쓴다 — 링크 워크트리가 저장소 루트 **아래** 산다.
var testRoots = []string{
	"/repo",
	"/repo/.flightdeck/worktrees/A",
	"/repo/.flightdeck/worktrees/B",
}

func TestDetectUnnormalizedSplit(t *testing.T) {
	const (
		m  = "machine-1"
		cc = "cc-aaa"
	)
	// ★ wantUn(버려진 카드 수)을 **반드시** 단정한다. 이것이 없으면 `owningRoot` 가
	//   언제나 "" 를 내도(= 카드를 전부 버려도) want:0 케이스가 전부 초록이다 —
	//   실측으로 14건 중 10건이 그렇게 통과했다. want 만 보는 표는 "안 보고했다"와
	//   "판정을 아예 못 했다"를 구분하지 못한다.
	cases := []struct {
		name   string
		cards  []SplitCard
		roots  []string
		want   int
		wantUn int
		why    string
	}{
		{
			name: "같은 트리에 값이 둘이면 보고한다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/repo", CCSessionID: cc},
				{SessionID: "s2", MachineID: m, Worktree: "/repo/plugins/flightdeck/server", CCSessionID: cc},
			},
			roots: testRoots,
			want:  1,
			why:   "정규화가 돌았다면 둘 다 /repo 로 적혔을 것이다",
		},
		{
			name: "저장소 루트와 링크 워크트리 루트는 보고하지 않는다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/repo", CCSessionID: cc},
				{SessionID: "s2", MachineID: m, Worktree: "/repo/.flightdeck/worktrees/A", CCSessionID: cc},
			},
			roots: testRoots,
			want:  0,
			why:   "둘 다 자기 트리의 루트다 — 정규화가 도는 클라이언트가 만드는 정당한 모양이고, 실측 거짓 양성 56%의 원인이었다",
		},
		{
			name: "지워진 워크트리도 관례로 복원해 보고하지 않는다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/repo", CCSessionID: cc},
				{SessionID: "s2", MachineID: m, Worktree: "/repo/.flightdeck/worktrees/GONE", CCSessionID: cc},
			},
			roots: []string{"/repo"}, // GONE 은 git 이 모른다 — 이미 지워졌다
			want:  0,
			why:   "원장의 링크-워크트리 경로 93개 중 81개가 이미 지워진 것이다. 살아 있는 루트만 보면 거짓 양성 84%가 난다",
		},
		{
			name: "지워진 워크트리 안의 하위 디렉토리는 그 트리로 보고한다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/repo/.flightdeck/worktrees/GONE", CCSessionID: cc},
				{SessionID: "s2", MachineID: m, Worktree: "/repo/.flightdeck/worktrees/GONE/plugins/flightdeck/server", CCSessionID: cc},
			},
			roots: []string{"/repo"},
			want:  1,
			why:   "복원된 트리 안에서 값이 둘이면 그것은 진짜 갈림이다 — 복원이 거짓 음성을 만들면 안 된다",
		},
		{
			name: "링크 워크트리 안의 하위 디렉토리는 보고한다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/repo/.flightdeck/worktrees/A", CCSessionID: cc},
				{SessionID: "s2", MachineID: m, Worktree: "/repo/.flightdeck/worktrees/A/plugins/flightdeck/server", CCSessionID: cc},
			},
			roots: testRoots,
			want:  1,
			why:   "소유 트리가 A 로 같은데 값이 둘이다 — 소유 루트를 가장 긴 것으로 골라야 여기가 /repo 로 흡수되지 않는다",
		},
		{
			name: "형제 워크트리 둘은 보고하지 않는다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/repo/.flightdeck/worktrees/A", CCSessionID: cc},
				{SessionID: "s2", MachineID: m, Worktree: "/repo/.flightdeck/worktrees/B", CCSessionID: cc},
			},
			roots: testRoots,
			want:  0,
			why:   "서로 다른 git 워크트리다 — 같은 repo-상대 경로를 만지면 병합 때 실제로 충돌한다",
		},
		{
			name: "한 대화가 트리 둘에서 각각 안 접혔으면 보고 둘",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/repo", CCSessionID: cc},
				{SessionID: "s2", MachineID: m, Worktree: "/repo/plugins", CCSessionID: cc},
				{SessionID: "s3", MachineID: m, Worktree: "/repo/.flightdeck/worktrees/A", CCSessionID: cc},
				{SessionID: "s4", MachineID: m, Worktree: "/repo/.flightdeck/worktrees/A/plugins", CCSessionID: cc},
			},
			roots: testRoots,
			want:  2,
			why:   "트리마다 따로 보고한다 — 대표 하나만 내면 나머지가 조용히 사라진다",
		},
		{
			name: "cc 가 다르면 보고하지 않는다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/repo", CCSessionID: "cc-aaa"},
				{SessionID: "s2", MachineID: m, Worktree: "/repo/plugins", CCSessionID: "cc-bbb"},
			},
			roots: testRoots,
			want:  0,
			why:   "다른 대화가 한 트리 안에서 일하는 것은 이 제품의 정상 흐름이다",
		},
		{
			name: "빈 cc 끼리는 보고하지 않는다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/repo", CCSessionID: ""},
				{SessionID: "s2", MachineID: m, Worktree: "/repo/plugins", CCSessionID: ""},
			},
			roots: testRoots,
			want:  0,
			why:   "못 읽음을 값으로 접으면 관측이 깨진 순간 이 축이 거짓 초록을 낸다",
		},
		{
			name: "머신이 다르면 보고하지 않는다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: "machine-1", Worktree: "/repo", CCSessionID: cc},
				{SessionID: "s2", MachineID: "machine-2", Worktree: "/repo/plugins", CCSessionID: cc},
			},
			roots: testRoots,
			want:  0,
			why:   "다른 머신의 같은 경로는 같은 트리가 아니다",
		},
		{
			name: "루트를 모르면 아무것도 보고하지 않는다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/repo", CCSessionID: cc},
				{SessionID: "s2", MachineID: m, Worktree: "/repo/plugins", CCSessionID: cc},
			},
			roots:  nil,
			want:   0,
			wantUn: 2,
			why:    "git 을 못 읽었을 때 추측으로 보고하면 실측 기준 거짓 양성이 56%다. 다만 **버렸다고 말한다**",
		},
		{
			name: "소유 트리를 못 찾은 경로는 건너뛰고 센다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/elsewhere", CCSessionID: cc},
				{SessionID: "s2", MachineID: m, Worktree: "/elsewhere/sub", CCSessionID: cc},
			},
			roots:  testRoots,
			want:   0,
			wantUn: 2,
			why:    "git 이 모르는 트리에 대고 '접혔어야 한다'를 판정할 근거가 없다 — 대신 침묵하지 않는다",
		},
		{
			name: ".claude 관례도 같이 본다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/repo", CCSessionID: cc},
				{SessionID: "s2", MachineID: m, Worktree: "/repo/.claude/worktrees/C", CCSessionID: cc},
			},
			roots: []string{"/repo"},
			want:  0,
			why:   "하네스가 만드는 자리다. 이 갈래가 없으면 원장의 .claude/worktrees 경로 36개가 전부 거짓 양성이 된다",
		},
		{
			name: "중첩 관례 경로에서 안쪽 트리도 루트로 본다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/repo/.flightdeck/worktrees/A", CCSessionID: cc},
				{SessionID: "s2", MachineID: m, Worktree: "/repo/.flightdeck/worktrees/A/.claude/worktrees/B", CCSessionID: cc},
			},
			roots: []string{"/repo"},
			want:  0,
			why:   "첫 매치에서 멈추면 B 가 루트 목록에 안 들어가 정상 정규화된 두 루트가 한 보고로 묶인다",
		},
		{
			// ★ 이 케이스의 roots 를 줄이지 마라. testRoots 를 쓰면 /repo-backup 이 자기
			//   자신에 매칭돼 그룹이 갈리고, 그러면 isDescendant 를 HasPrefix 로 바꿔도
			//   초록이 나온다(실제로 그렇게 죽어 있었다).
			name: "경로 성분 경계를 지킨다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/repo", CCSessionID: cc},
				{SessionID: "s2", MachineID: m, Worktree: "/repo-backup/sub", CCSessionID: cc},
			},
			roots:  []string{"/repo"},
			want:   0,
			wantUn: 1,
			why:    "/repo 는 /repo-backup/sub 의 조상이 아니다 — 문자열 접두로 보면 둘이 한 트리로 묶인다",
		},
		{
			// ★ roots 를 `" /repo/sub/.. "` 로 두는 것이 이 케이스의 핵심이다.
			//   루트 쪽 TrimSpace 가 빠지면 루트가 상대 경로가 되어 filepath.Rel 이
			//   에러를 내고 카드 셋이 전부 버려진다 — wantUn 0 이 깨져 잡힌다.
			//   ★ 다만 **Clean 은 이 케이스가 못 잡는다.** owningRoot 가 쓰는
			//   filepath.Rel 이 인자를 내부에서 다시 Clean 하므로 루트 쪽 Clean 을
			//   지워도 매칭은 그대로다. 달라지는 것은 보고에 적히는 Root 값뿐이고,
			//   그것은 TestDetectUnnormalizedSplitReportsDetail 이 잠근다.
			name: "같은 트리를 여러 표기로 적어도 한 값으로 본다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/repo", CCSessionID: cc},
				{SessionID: "s2", MachineID: m, Worktree: "/repo/", CCSessionID: cc},
				{SessionID: "s3", MachineID: m, Worktree: "/repo/sub/..", CCSessionID: cc},
			},
			roots: []string{" /repo/sub/.. "},
			want:  0,
			why:   "Clean·TrimSpace 가 없으면 표기 차이가 갈림으로 보고되거나 카드가 통째로 버려진다",
		},
		{
			name: "경로가 아무 데도 안 가리키면 세어서 낸다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: ".", CCSessionID: cc},
				{SessionID: "s2", MachineID: m, Worktree: "a/..", CCSessionID: cc},
			},
			roots:  testRoots,
			want:   0,
			wantUn: 2,
			why:    "3중키는 섰는데 경로가 없다 — 조용히 넘기면 판정 대상이던 카드가 흔적 없이 사라진다",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, un := DetectUnnormalizedSplit(c.cards, c.roots)
			if len(got) != c.want {
				t.Fatalf("보고 %d건, 원하는 것 %d건 — %s\n입력: %+v\n결과: %+v",
					len(got), c.want, c.why, c.cards, got)
			}
			if un != c.wantUn {
				t.Fatalf("버린 카드 %d장, 원하는 것 %d장 — 보고 수가 맞아도 "+
					"카드를 전부 버렸다면 판정을 못 한 것이다\n입력: %+v", un, c.wantUn, c.cards)
			}
		})
	}
}

// 대조 단정 — 함수가 항상 빈 결과를 내도 위 표의 다수가 통과한다(want 0 이 많다).
// 이 시험이 보고의 **내용**까지 잠근다.
//
// ★ 첫 루트를 `" /repo/sub/.. "` 로 두는 것이 의도된 것이다. 루트 쪽 `filepath.Clean`
// 을 지워도 **매칭은 안 깨진다** — `owningRoot` 가 쓰는 `filepath.Rel` 이 인자를
// 내부에서 다시 Clean 하기 때문이다. 깨지는 것은 보고에 **적히는 값**뿐이라
// (`Root` 가 `/repo/sub/..` 로 나간다) `want 0`/`wantUn` 만 보는 표로는 그 회귀를
// 영영 못 잡는다. 여기서 `r.Root` 를 정규화된 값으로 못박아 그 자리를 막는다.
func TestDetectUnnormalizedSplitReportsDetail(t *testing.T) {
	got, _ := DetectUnnormalizedSplit([]SplitCard{
		{SessionID: "s1", MachineID: "m", Worktree: "/repo", CCSessionID: "cc"},
		{SessionID: "s2", MachineID: "m", Worktree: "/repo/a", CCSessionID: "cc"},
		{SessionID: "s3", MachineID: "m", Worktree: "/repo/a/b", CCSessionID: "cc"},
	}, []string{" /repo/sub/.. ", "/repo/.flightdeck/worktrees/A", "/repo/.flightdeck/worktrees/B"})
	if len(got) != 1 {
		t.Fatalf("보고 %d건, 원하는 것 1건: %+v", len(got), got)
	}
	r := got[0]
	if r.Root != "/repo" {
		t.Errorf("트리 %q, 원하는 것 %q", r.Root, "/repo")
	}
	if want := []string{"/repo", "/repo/a", "/repo/a/b"}; !reflect.DeepEqual(r.Recorded, want) {
		t.Errorf("기록된 값 %v, 원하는 것 %v", r.Recorded, want)
	}
	if want := []string{"s1", "s2", "s3"}; !reflect.DeepEqual(r.SessionIDs, want) {
		t.Errorf("카드 %v, 원하는 것 %v", r.SessionIDs, want)
	}
	if r.CCSessionID != "cc" {
		t.Errorf("cc %q, 원하는 것 %q", r.CCSessionID, "cc")
	}
}

// 소유 루트는 **가장 긴** 것이어야 한다. 가장 짧은 것을 고르면 링크 워크트리가
// 통째로 저장소 루트에 흡수되고, 그 순간 거짓 양성 56%가 돌아온다.
func TestOwningRootPicksTheLongestMatch(t *testing.T) {
	got, un := DetectUnnormalizedSplit([]SplitCard{
		{SessionID: "s1", MachineID: "m", Worktree: "/repo", CCSessionID: "cc"},
		{SessionID: "s2", MachineID: "m", Worktree: "/repo/.flightdeck/worktrees/A", CCSessionID: "cc"},
		{SessionID: "s3", MachineID: "m", Worktree: "/repo/.flightdeck/worktrees/B", CCSessionID: "cc"},
	}, testRoots)
	if len(got) != 0 {
		t.Fatalf("보고 %d건, 원하는 것 0건 — 셋 다 자기 트리의 루트다: %+v", len(got), got)
	}
	if un != 0 {
		t.Fatalf("버린 카드 %d장, 원하는 것 0장 — 보고 0건이 '판정을 못 해서'면 안 된다", un)
	}
}

// 버려진 카드는 **세어서 낸다.** 침묵하면 "갈림 없음"과 "그 트리를 못 알아봤다"가
// 화면에서 같아진다 — 이 저장소가 반복해서 겪은 실패 모양이다.
func TestDetectUnnormalizedSplitCountsUnattributed(t *testing.T) {
	_, n := DetectUnnormalizedSplit([]SplitCard{
		{SessionID: "s1", MachineID: "m", Worktree: "/repo", CCSessionID: "cc"},
		{SessionID: "s2", MachineID: "m", Worktree: "/elsewhere", CCSessionID: "cc"},
		{SessionID: "s3", MachineID: "m", Worktree: "/nowhere/deep", CCSessionID: "cc"},
	}, []string{"/repo"})
	if n != 2 {
		t.Fatalf("버린 카드 %d장, 원하는 것 2장 — 어느 트리에도 안 붙은 카드를 세지 않으면 침묵이 된다", n)
	}
}

// 정렬이 결정적인가 — 같은 cc 가 머신 둘에서 보고를 만들 때가 유일한 흔들림 자리다.
// 맵 순회 순서가 결과로 새면 같은 입력이 매번 다른 문서를 낸다.
func TestDetectUnnormalizedSplitIsDeterministic(t *testing.T) {
	in := []SplitCard{
		{SessionID: "s1", MachineID: "machine-1", Worktree: "/repo", CCSessionID: "cc"},
		{SessionID: "s2", MachineID: "machine-1", Worktree: "/repo/sub", CCSessionID: "cc"},
		{SessionID: "s3", MachineID: "machine-2", Worktree: "/repo", CCSessionID: "cc"},
		{SessionID: "s4", MachineID: "machine-2", Worktree: "/repo/sub", CCSessionID: "cc"},
	}
	want, _ := DetectUnnormalizedSplit(in, testRoots)
	if len(want) != 2 {
		t.Fatalf("보고 %d건, 원하는 것 2건: %+v", len(want), want)
	}
	for i := 0; i < 200; i++ {
		got, _ := DetectUnnormalizedSplit(in, testRoots)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%d회차에서 결과가 흔들렸다:\n%+v\nvs\n%+v", i, got, want)
		}
	}
}

func TestSameConversation(t *testing.T) {
	if !SameConversation("cc-a", "cc-a") {
		t.Error("같은 cc 는 같은 대화다")
	}
	if SameConversation("cc-a", "cc-b") {
		t.Error("다른 cc 는 다른 대화다")
	}
	if SameConversation("", "") {
		t.Error("빈 cc 끼리는 같지 않다 — 못 읽음을 값으로 접으면 안 된다")
	}
}
