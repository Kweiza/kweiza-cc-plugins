package judge

import "testing"

func TestDetectUnnormalizedSplit(t *testing.T) {
	const (
		m  = "machine-1"
		cc = "cc-aaa"
		wt = "/repo"
	)
	cases := []struct {
		name  string
		cards []SplitCard
		want  int // 보고 건수
		why   string
	}{
		{
			name: "조상·자손이면 보고한다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: wt, CCSessionID: cc},
				{SessionID: "s2", MachineID: m, Worktree: wt + "/plugins/flightdeck/server", CCSessionID: cc},
			},
			want: 1,
			why:  "정규화가 돌았다면 둘 다 /repo 로 접혔을 것이다",
		},
		{
			name: "형제 트리는 보고하지 않는다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/repo/.flightdeck/worktrees/A", CCSessionID: cc},
				{SessionID: "s2", MachineID: m, Worktree: "/repo/.flightdeck/worktrees/B", CCSessionID: cc},
			},
			want: 0,
			why:  "서로 다른 git 워크트리다 — 정당하게 갈린 것이고 병합 때 실제로 충돌한다",
		},
		{
			name: "cc 가 다르면 보고하지 않는다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: wt, CCSessionID: "cc-aaa"},
				{SessionID: "s2", MachineID: m, Worktree: wt + "/sub", CCSessionID: "cc-bbb"},
			},
			want: 0,
			why:  "다른 대화가 한 트리 안에서 일하는 것은 이 제품의 정상 흐름이다",
		},
		{
			name: "빈 cc 끼리는 보고하지 않는다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: wt, CCSessionID: ""},
				{SessionID: "s2", MachineID: m, Worktree: wt + "/sub", CCSessionID: ""},
			},
			want: 0,
			why:  "못 읽음을 값으로 접으면 관측이 깨진 순간 이 축이 거짓 초록을 낸다",
		},
		{
			name: "머신이 다르면 보고하지 않는다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: "machine-1", Worktree: wt, CCSessionID: cc},
				{SessionID: "s2", MachineID: "machine-2", Worktree: wt + "/sub", CCSessionID: cc},
			},
			want: 0,
			why:  "다른 머신의 같은 경로는 같은 트리가 아니다",
		},
		{
			name: "경로 성분 경계를 지킨다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/repo", CCSessionID: cc},
				{SessionID: "s2", MachineID: m, Worktree: "/repo-backup", CCSessionID: cc},
			},
			want: 0,
			why:  "/repo 는 /repo-backup 의 조상이 아니다 — 문자열 접두로 보면 오답이 난다",
		},
		{
			name: "한 대화 안의 무관한 갈림 쌍을 둘 다 보고한다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/a", CCSessionID: cc},
				{SessionID: "s2", MachineID: m, Worktree: "/a/b", CCSessionID: cc},
				{SessionID: "s3", MachineID: m, Worktree: "/x", CCSessionID: cc},
				{SessionID: "s4", MachineID: m, Worktree: "/x/y", CCSessionID: cc},
			},
			want: 2,
			why:  "대표 하나만 내면 /x↔/x/y 가 조용히 사라진다 — 실측에서 한 대화가 워크트리 16개에 걸쳤다",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DetectUnnormalizedSplit(c.cards)
			if len(got) != c.want {
				t.Fatalf("보고 %d건, 원하는 것 %d건 — %s\n입력: %+v\n결과: %+v",
					len(got), c.want, c.why, c.cards, got)
			}
		})
	}
}

// 대조 단정 — 함수가 항상 빈 결과를 내도 위 시험 다섯이 초록이다.
// 이 시험이 그 거짓 초록을 막는다.
func TestDetectUnnormalizedSplitReportsDetail(t *testing.T) {
	got := DetectUnnormalizedSplit([]SplitCard{
		{SessionID: "s1", MachineID: "m", Worktree: "/repo", CCSessionID: "cc"},
		{SessionID: "s2", MachineID: "m", Worktree: "/repo/a", CCSessionID: "cc"},
		{SessionID: "s3", MachineID: "m", Worktree: "/repo/a/b", CCSessionID: "cc"},
	})
	if len(got) != 1 {
		t.Fatalf("보고 %d건, 원하는 것 1건: %+v", len(got), got)
	}
	r := got[0]
	if r.Ancestor != "/repo" {
		t.Errorf("조상 %q, 원하는 것 %q", r.Ancestor, "/repo")
	}
	if len(r.Descendants) != 2 {
		t.Errorf("자손 %d개, 원하는 것 2개: %v", len(r.Descendants), r.Descendants)
	}
	if len(r.SessionIDs) != 3 {
		t.Errorf("카드 %d장, 원하는 것 3장: %v", len(r.SessionIDs), r.SessionIDs)
	}
	if r.CCSessionID != "cc" {
		t.Errorf("cc %q, 원하는 것 %q", r.CCSessionID, "cc")
	}
}

// 사슬은 최상위 하나가 덮는다 — 중첩 보고를 내면 같은 카드가 두 번 세어진다.
func TestDetectUnnormalizedSplitReportsOnlyTopmostInAChain(t *testing.T) {
	got := DetectUnnormalizedSplit([]SplitCard{
		{SessionID: "s1", MachineID: "m", Worktree: "/a", CCSessionID: "cc"},
		{SessionID: "s2", MachineID: "m", Worktree: "/a/b", CCSessionID: "cc"},
		{SessionID: "s3", MachineID: "m", Worktree: "/a/b/c", CCSessionID: "cc"},
	})
	if len(got) != 1 {
		t.Fatalf("보고 %d건, 원하는 것 1건 — 사슬은 최상위 하나가 덮는다: %+v", len(got), got)
	}
	if got[0].Ancestor != "/a" || len(got[0].Descendants) != 2 {
		t.Fatalf("조상 %q · 자손 %v — 원하는 것 /a 와 자손 2개", got[0].Ancestor, got[0].Descendants)
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
	want := DetectUnnormalizedSplit(in)
	if len(want) != 2 {
		t.Fatalf("보고 %d건, 원하는 것 2건: %+v", len(want), want)
	}
	for i := 0; i < 200; i++ {
		got := DetectUnnormalizedSplit(in)
		for j := range got {
			if got[j].MachineID != want[j].MachineID || got[j].Ancestor != want[j].Ancestor {
				t.Fatalf("%d회차에서 순서가 흔들렸다: %+v vs %+v", i, got, want)
			}
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
