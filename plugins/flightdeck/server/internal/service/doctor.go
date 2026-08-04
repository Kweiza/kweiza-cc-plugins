package service

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// 진단 — 무엇이 관측됐고 무엇이 안 됐는지를 **이름으로** 낸다.
//
// 설계 §13 의 마지막 절: 플랫폼 동작은 표류한다. 같은 문제를 먼저 푼 플러그인은
// 세션 id 를 CLAUDE_ENV_FILE 경로에서 뽑는데 2.1.220 에는 그 환경변수가 아예 없고,
// 그 탐지는 지금 조용히 None 을 돌려준다. **부재를 기본값으로 접으면 그 사실이 영영 안 보인다.**

// Health 는 /healthz 가 내는 것이다.
type Health struct {
	OK          bool      `json:"ok"`
	APIVersion  string    `json:"api_version"`
	DBOK        bool      `json:"db_ok"`
	DBPath      string    `json:"db_path"`
	DBError     string    `json:"db_error,omitempty"`
	DiskFreePct float64   `json:"disk_free_pct"`
	DiskKnown   bool      `json:"disk_known"` // false 면 위 숫자는 값이 아니다. 0%와 "못 쟀다"를 가른다
	DiskError   string    `json:"disk_error,omitempty"`
	At          time.Time `json:"at"`
}

// Health 는 서버가 지금 답할 수 있는 상태다.
//
// **오류를 반환하지 않는다.** 헬스체크가 실패로 끊기면 "무엇이 고장났나"를 담을 자리가
// 사라진다 — 고장 자체가 이 함수의 출력이다.
func (s *Service) Health(ctx context.Context) Health {
	h := Health{APIVersion: APIVersion, At: s.now(), DiskFreePct: -1}
	if s.st == nil {
		h.DBError = "저장 계층이 없다 — 서버가 DB 없이 기동했다"
		return h
	}
	h.DBPath = s.st.Path()
	if err := s.st.DB().PingContext(ctx); err != nil {
		h.DBError = clip(err.Error(), 400)
		s.log.ErrorContext(ctx, "DB 접속 확인 실패", "path", clip(h.DBPath, 200), "error", err.Error())
	} else {
		h.DBOK = true
	}

	pct, err := diskFreePct(dirOf(h.DBPath))
	if err != nil {
		h.DiskError = clip(err.Error(), 400)
		s.log.WarnContext(ctx, "디스크 여유 측정 실패", "path", clip(h.DBPath, 200), "error", err.Error())
	} else {
		h.DiskFreePct, h.DiskKnown = pct, true
	}

	h.OK = h.DBOK
	return h
}

// DoctorAxis 는 플랫폼 축 하나의 관측 결과다.
//
// Observed=false 를 **이름과 함께** 낸다. 이름 없이 "일부 축이 없다"만 내면
// 어느 탐지가 깨졌는지 알 수 없고, 그러면 플랫폼이 바뀐 날 아무도 눈치채지 못한다.
type DoctorAxis struct {
	Name     string `json:"name"`
	Observed bool   `json:"observed"`
	Value    string `json:"value,omitempty"`
	Detail   string `json:"detail"` // 왜 필요한 축인가 · 없으면 무엇이 깨지나. 항상 채운다
}

// ProjectAxis 는 프로젝트 하나의 git 도달성이다.
type ProjectAxis struct {
	Project   string `json:"project"`
	Path      string `json:"path"`
	Readable  bool   `json:"readable"`
	HeadSHA   string `json:"head_sha,omitempty"`
	Worktrees int    `json:"worktrees"`
	Detail    string `json:"detail"`
}

// DoctorReport 는 진단 한 벌이다.
type DoctorReport struct {
	At       time.Time     `json:"at"`
	Health   Health        `json:"health"`
	Platform []DoctorAxis  `json:"platform"`
	Projects []ProjectAxis `json:"projects"`
}

// platformAxes 는 §13 이 기대는 플랫폼 축의 목록이다.
//
// ★ CLAUDE_ENV_FILE 이 여기 있는 이유: 그것은 **없는 것이 정상**인 축이다(2.1.220 에서 사라졌다).
// 그래도 재서 낸다 — 다시 생기면 그 사실이 여기 뜨고, 없는 채로 두면
// "우리가 이 축을 안 본다"와 "플랫폼이 안 준다"가 구분되지 않는다.
var platformAxes = []struct{ name, why string }{
	{"CLAUDE_CODE_SESSION_ID", "세션 정체의 원천. 없으면 MCP 도구가 자기가 어느 세션인지 모른다"},
	{"CLAUDE_PROJECT_DIR", "프로젝트 루트. 없으면 cwd 로 대신하지만 그 둘이 다를 수 있다"},
	{"CLAUDECODE", "Claude Code 가 띄운 프로세스인지의 표식"},
	{"CLAUDE_CODE_ENTRYPOINT", "어느 입구로 띄웠나(cli 등)"},
	{"CLAUDE_CODE_SSE_PORT", "부모 프로세스의 SSE 포트. 부모 대조에 쓴다"},
	{"CLAUDE_PLUGIN_ROOT", "훅이 절대경로로 부르는 기준. **버전이 들어가므로 저장하지 않는다**"},
	{"CLAUDE_PLUGIN_DATA", "캐시(재생성 가능한 열화 상태)를 두는 곳. PLUGIN_ROOT 는 갱신마다 바뀌므로 거기 두면 안 된다. 아웃박스·격리 보관소는 채널 무관한 고정 자리(~/.flightdeck/outbox)에 있다"},
	{"CLAUDE_ENV_FILE", "2.1.220 에는 없는 것이 정상이다 — 먼저 푼 플러그인이 여기서 세션 id 를 뽑다 조용히 None 이 됐다"},
}

// ProbePlatform 은 환경 축을 실제로 재서 결과를 낸다.
//
// get 을 인자로 받는 이유는 시험이 이 함수를 직접 부를 수 있게 하기 위해서다 —
// os.Getenv 를 본문에 박으면 시험이 전역 환경을 흔들어야 하고, 그러면 병렬 시험이 서로를 깬다.
func ProbePlatform(get func(string) (string, bool), cwd string, cwdErr error) []DoctorAxis {
	out := make([]DoctorAxis, 0, len(platformAxes)+1)
	for _, a := range platformAxes {
		v, ok := get(a.name)
		axis := DoctorAxis{Name: a.name, Observed: ok && v != "", Detail: a.why}
		if axis.Observed {
			axis.Value = clip(v, 200)
		}
		out = append(out, axis)
	}
	cwdAxis := DoctorAxis{Name: "cwd", Detail: "MCP stdio 서버의 cwd 가 곧 세션의 워크트리다(설계 §13)"}
	if cwdErr != nil {
		cwdAxis.Detail += " — 측정 실패: " + clip(cwdErr.Error(), 200)
	} else if strings.TrimSpace(cwd) != "" {
		cwdAxis.Observed, cwdAxis.Value = true, clip(cwd, 200)
	}
	return append(out, cwdAxis)
}

// Doctor 는 플랫폼 축과 프로젝트 git 도달성을 실제로 재서 낸다.
func (s *Service) Doctor(ctx context.Context) DoctorReport {
	now := s.now()
	cwd, cwdErr := os.Getwd()
	rep := DoctorReport{
		At:       now,
		Health:   s.Health(ctx),
		Platform: ProbePlatform(s.getenv, cwd, cwdErr),
	}

	projects, err := s.st.ListProjects(ctx)
	if err != nil {
		s.log.ErrorContext(ctx, "진단: 프로젝트 목록 조회 실패", "error", err.Error())
		rep.Projects = []ProjectAxis{{
			Detail: "프로젝트 목록을 못 읽었다: " + clip(err.Error(), 400),
		}}
		return rep
	}
	for _, p := range projects {
		axis := ProjectAxis{Project: p.ID, Path: p.Path}
		if strings.TrimSpace(p.Path) == "" {
			axis.Detail = "경로가 비어 있다 — git 파생이 통째로 죽는다"
			rep.Projects = append(rep.Projects, axis)
			continue
		}
		g := s.git(p.Path)
		if r, err := g.Ref(ctx, "HEAD"); err != nil {
			axis.Detail = "HEAD 를 못 읽었다: " + clip(err.Error(), 400)
		} else {
			axis.Readable, axis.HeadSHA = true, r.SHA
			axis.Detail = fmt.Sprintf("HEAD=%s (%s)", shortSHA(r.SHA), clip(r.Subject, 80))
		}
		if wts, err := g.Worktrees(ctx); err != nil {
			axis.Detail += " · 워크트리 목록 실패: " + clip(err.Error(), 200)
		} else {
			axis.Worktrees = len(wts)
		}
		rep.Projects = append(rep.Projects, axis)
	}

	missing := 0
	for _, a := range rep.Platform {
		if !a.Observed {
			missing++
		}
	}
	s.log.InfoContext(ctx, "진단",
		"count", len(rep.Projects), "skipped", missing, "db_ok", rep.Health.DBOK)
	return rep
}

// lookupEnv 는 기본 환경 조회다.
func lookupEnv(k string) (string, bool) { return os.LookupEnv(k) }

func dirOf(p string) string {
	if p == "" {
		return "."
	}
	i := strings.LastIndex(p, string(os.PathSeparator))
	if i <= 0 {
		return "."
	}
	return p[:i]
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
