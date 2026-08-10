package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"
)

// 열화 판정 시험 — 단정의 좌표계는 **세션이 실제로 읽는 문장**이다.
//
// Mode 만 단정하면 "거절했다"와 "왜 거절했는지 말한다"가 구분되지 않는다.
// 이 제품이 겨냥하는 실패가 정확히 그것이라(사유 없는 탈락은 두 번째 세션부터 무시된다)
// 사유 문자열까지 단정한다.

func TestJudgeOfflineTable(t *testing.T) {
	cases := []struct {
		cmd     string
		want    OfflineMode
		mustSay string
	}{
		{"note", OfflineOutbox, "파생 불가"},
		{"status", OfflineCache, "낡음 배너"},
		{"board", OfflineCache, "낡음 배너"},
		{"next", OfflineCache, "낡음 배너"},
		{"doctor", OfflineCache, "낡음 배너"},
		{"beat", OfflineDrop, "다시 만들면 되는"},
		{"pick", OfflineRefuse, "배타는 서버만 보장할 수 있"},
		{"claim", OfflineRefuse, "배타는 서버만 보장할 수 있"},
		{"open", OfflineCache, "서버 발급 id"},
		{"add", OfflineRefuse, "전역 유일"},
		{"finish", OfflineRefuse, "한 트랜잭션"},
		{"alloc", OfflineRefuse, "같은 번호"},
		// 랜딩 레인 넷. 전부 거절이지만 **사유가 셋으로 갈린다** — 뭉개면 다음 사람이
		// 그중 하나(대개 반납)만 아웃박스로 연다.
		{CmdLandAcquire, OfflineRefuse, "배타의 정본이 서버의 DB 제약"},
		{CmdLandReport, OfflineRefuse, "남의 점유를 반납한다"},
		{CmdLandLeave, OfflineRefuse, "남의 점유를 반납한다"},
		{CmdLaneRelease, OfflineRefuse, "사람의 판단이라 재생 대상이 아니다"},
		{CmdClaimRelease, OfflineRefuse, "사람의 판단이라 재생 대상이 아니다"},
		// 프로젝트 삭제 — 아는 명령이라 명시 갈래다(최종 리뷰 Important-3). 사유가
		// default("정의돼 있지 않다")와 달라야 한다 — 서버가 죽은 머신은 정확히 사람이
		// 잔해를 치우려 드는 순간이라, "이 명령은 설계가 안 됐다"로 읽히는 사유는 여기서
		// 제일 나쁘게 걸린다.
		{CmdProjectRemove, OfflineRefuse, "지금 상태"},
	}
	for _, c := range cases {
		t.Run(c.cmd, func(t *testing.T) {
			got := JudgeOffline(c.cmd)
			if got.Mode != c.want {
				t.Fatalf("%s: 처방이 %q 다, %q 를 기대했다 (사유: %s)", c.cmd, got.Mode, c.want, got.Reason)
			}
			if !strings.Contains(got.Reason, c.mustSay) {
				t.Fatalf("%s: 사유에 %q 가 없다 — 받은 사유: %q", c.cmd, c.mustSay, got.Reason)
			}
		})
	}
}

// ★ 표 밖 케이스. 기본값이 조용히 붙는 것을 막는 축이라 표에 없는 것을 일부러 넣는다.
//
// "정책이 정의돼 있지 않다"와 "정책상 거절이다"는 다른 사실이다 — 뭉개면
// 새 명령이 생기는 날 아무도 정하지 않은 열화 정책이 조용히 붙는다.
func TestJudgeOfflineUnknownCommandRefusesAndSaysWhy(t *testing.T) {
	for _, cmd := range []string{"export", "import", "watch", "", "PICK", "note ", "  "} {
		v := JudgeOffline(cmd)
		if cmd == "note " || cmd == "  " {
			// 공백은 다듬는다. "note " 는 note 와 같은 명령이다.
			continue
		}
		if v.Mode != OfflineRefuse {
			t.Fatalf("표 밖 명령 %q 를 %q 로 처리했다 — 기본값이 조용히 붙었다", cmd, v.Mode)
		}
		if !strings.Contains(v.Reason, "열화 정책이 정의돼 있지 않다") {
			t.Fatalf("표 밖 명령 %q 의 사유가 '정의돼 있지 않다'라고 말하지 않는다: %q", cmd, v.Reason)
		}
	}
	// 공백만 다른 것은 같은 명령이어야 한다(대문자는 아니다 — 명령은 소문자 고정이다).
	if got := JudgeOffline("note "); got.Mode != OfflineOutbox {
		t.Fatalf("앞뒤 공백이 명령을 바꿨다: %q", got.Mode)
	}
	if got := JudgeOffline("PICK"); got.Mode != OfflineRefuse ||
		!strings.Contains(got.Reason, "정의돼 있지 않다") {
		t.Fatalf("대소문자가 다른 것을 같은 명령으로 봤다: %+v", got)
	}
}

// 모든 판정은 사유를 채운다. 공허한 단정(결과만 찍고 왜인지 안 남기는 것)을 막는 축이다.
func TestJudgeOfflineAlwaysGivesReason(t *testing.T) {
	for _, cmd := range []string{"note", "status", "beat", "pick", "open", "add",
		"finish", "alloc", "board", "next", "doctor", "무엇이든",
		CmdLandAcquire, CmdLandReport, CmdLandLeave, CmdLaneRelease, CmdClaimRelease,
		CmdProjectRemove} {
		if strings.TrimSpace(JudgeOffline(cmd).Reason) == "" {
			t.Fatalf("%q 의 사유가 비었다", cmd)
		}
	}
}

func TestUnreachableSeparatesTransportFromStatus(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		want   bool
	}{
		{"연결 거부", syscall.ECONNREFUSED, 0, true},
		{"호스트 도달 불가", syscall.EHOSTUNREACH, 0, true},
		{"url.Error 로 감싼 것", &url.Error{Op: "Post", URL: "http://x", Err: syscall.ECONNREFUSED}, 0, true},
		{"net.Error(타임아웃)", timeoutErr{}, 0, true},
		{"우리 표식", fmt.Errorf("%w: 붙었다", ErrUnreachable), 0, true},
		{"400 은 도달했다", nil, 400, false},
		{"404 는 도달했다", nil, 404, false},
		{"409 는 도달했다", nil, 409, false},
		{"500 은 도달했다 — 서버가 답했다", nil, 500, false},
		{"502 는 상류에 못 닿았다", nil, 502, true},
		{"503 도", nil, 503, true},
		{"504 도", nil, 504, true},
		{"성공", nil, 200, false},
		// 표 밖: 도메인 오류(우리 표식이 아닌 일반 오류)는 미도달이 아니다.
		{"평범한 오류", errors.New("본문 해석 실패"), 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Unreachable(c.err, c.status); got != c.want {
				t.Fatalf("%s: %v 를 기대했는데 %v (err=%v status=%d)", c.name, c.want, got, c.err, c.status)
			}
		})
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string { return "i/o timeout" }
func (timeoutErr) Timeout() bool { return true }
func (timeoutErr) Temporary() bool {
	return true
}

var _ net.Error = timeoutErr{}

// L1 배너는 설계 §7 의 문안이다. 세션이 읽는 것은 이 문자열뿐이라 문면을 단정한다.
func TestStaleBannerSaysWhatWorksAndWhatDoesNot(t *testing.T) {
	now := time.Date(2026, 8, 3, 14, 39, 0, 0, time.UTC)
	at := now.Add(-37 * time.Minute)
	got := StaleBanner(now, at, "http://localhost:7420")

	for _, want := range []string{
		"⚠ 조정 서버 미도달",
		"http://localhost:7420",
		"37분 전",
		"되는 것: 코드 작성·커밋·조사 전부",
		"안 되는 것: 새 항목 선점",
		"그 뒤 남이 무엇을 집었는지는 알 수 없다",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("배너에 %q 가 없다:\n%s", want, got)
		}
	}
}

// 캐시가 아예 없는 경우를 **다르게 말한다.** 같은 문장을 내면
// "37분 전 스냅숏이 있다"와 "아무 스냅숏도 없다"가 화면에서 같아진다.
func TestStaleBannerSaysWhenThereIsNoCacheAtAll(t *testing.T) {
	now := time.Date(2026, 8, 3, 14, 39, 0, 0, time.UTC)
	got := StaleBanner(now, time.Time{}, "http://127.0.0.1:7420")
	if !strings.Contains(got, "캐시 없음") {
		t.Fatalf("캐시가 없는데 그 사실을 말하지 않는다:\n%s", got)
	}
	if !strings.Contains(got, "누가 무엇을 집었는지 알 방법이 지금 없다") {
		t.Fatalf("캐시 부재의 결과를 말하지 않는다:\n%s", got)
	}
	if strings.Contains(got, "시점의 스냅숏이다") {
		t.Fatalf("캐시가 없는데 스냅숏이 있는 것처럼 말한다:\n%s", got)
	}
}

func TestSkewBannerIsSilentWhenVersionsMatch(t *testing.T) {
	if got := SkewBanner("1", "1"); got != "" {
		t.Fatalf("같은 버전인데 배너를 냈다 — 상시 점등된 경고는 판별력이 0 이다: %q", got)
	}
	got := SkewBanner("1", "2")
	if !strings.Contains(got, "클라이언트 1") || !strings.Contains(got, "서버 2") {
		t.Fatalf("스큐 배너가 양쪽 버전을 말하지 않는다: %q", got)
	}
	if got := SkewBanner("1", ""); !strings.Contains(got, "api_version 을 알리지 않았다") {
		t.Fatalf("서버가 버전을 안 냈다는 사실을 말하지 않는다: %q", got)
	}
}

func TestHumanAge(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{5 * time.Second, "5초 전"},
		{90 * time.Second, "1분 전"},
		{37 * time.Minute, "37분 전"},
		{2*time.Hour + 5*time.Minute, "2시간 5분 전"},
		{50 * time.Hour, "2일 전"},
		{-30 * time.Second, "30초 전"}, // 표 밖: 시계가 뒤로 간 경우도 값을 낸다
	}
	for _, c := range cases {
		if got := humanAge(c.d); got != c.want {
			t.Fatalf("%v → %q, %q 를 기대했다", c.d, got, c.want)
		}
	}
}
