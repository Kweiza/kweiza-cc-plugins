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
	cases := []struct {
		name  string
		cards []SplitCard
		roots []string
		want  int
		why   string
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
			roots: nil,
			want:  0,
			why:   "git 을 못 읽었을 때 추측으로 보고하면 실측 기준 거짓 양성이 56%다",
		},
		{
			name: "소유 트리를 못 찾은 경로는 건너뛴다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/elsewhere", CCSessionID: cc},
				{SessionID: "s2", MachineID: m, Worktree: "/elsewhere/sub", CCSessionID: cc},
			},
			roots: testRoots,
			want:  0,
			why:   "git 이 모르는 트리에 대고 '접혔어야 한다'를 판정할 근거가 없다",
		},
		{
			name: "경로 성분 경계를 지킨다",
			cards: []SplitCard{
				{SessionID: "s1", MachineID: m, Worktree: "/repo", CCSessionID: cc},
				{SessionID: "s2", MachineID: m, Worktree: "/repo-backup", CCSessionID: cc},
			},
			roots: []string{"/repo", "/repo-backup"},
			want:  0,
			why:   "/repo 는 /repo-backup 의 조상이 아니다 — 문자열 접두로 보면 오답이 난다",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DetectUnnormalizedSplit(c.cards, c.roots)
			if len(got) != c.want {
				t.Fatalf("보고 %d건, 원하는 것 %d건 — %s\n입력: %+v\n결과: %+v",
					len(got), c.want, c.why, c.cards, got)
			}
		})
	}
}

// 대조 단정 — 함수가 항상 빈 결과를 내도 위 표가 통과하는 케이스가 많다.
// 이 시험이 보고의 **내용**까지 잠근다.
func TestDetectUnnormalizedSplitReportsDetail(t *testing.T) {
	got := DetectUnnormalizedSplit([]SplitCard{
		{SessionID: "s1", MachineID: "m", Worktree: "/repo", CCSessionID: "cc"},
		{SessionID: "s2", MachineID: "m", Worktree: "/repo/a", CCSessionID: "cc"},
		{SessionID: "s3", MachineID: "m", Worktree: "/repo/a/b", CCSessionID: "cc"},
	}, testRoots)
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
	got := DetectUnnormalizedSplit([]SplitCard{
		{SessionID: "s1", MachineID: "m", Worktree: "/repo", CCSessionID: "cc"},
		{SessionID: "s2", MachineID: "m", Worktree: "/repo/.flightdeck/worktrees/A", CCSessionID: "cc"},
		{SessionID: "s3", MachineID: "m", Worktree: "/repo/.flightdeck/worktrees/B", CCSessionID: "cc"},
	}, testRoots)
	if len(got) != 0 {
		t.Fatalf("보고 %d건, 원하는 것 0건 — 셋 다 자기 트리의 루트다: %+v", len(got), got)
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
	want := DetectUnnormalizedSplit(in, testRoots)
	if len(want) != 2 {
		t.Fatalf("보고 %d건, 원하는 것 2건: %+v", len(want), want)
	}
	for i := 0; i < 200; i++ {
		got := DetectUnnormalizedSplit(in, testRoots)
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
