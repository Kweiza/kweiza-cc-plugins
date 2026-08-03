package legacy

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Source 는 읽을 원본 트리다. **이 패키지는 여기에 한 바이트도 쓰지 않는다.**
//
// 두 레포에 살아 있는 병렬 세션이 지금도 일하고 있고, 되돌리기는 "DB 파일 삭제 + 재실행"이다.
// 원본을 안 건드리는 것이 그 롤백이 성립하는 유일한 근거다.
type Source struct {
	CodeRoot string // `.claude/{sessions,queue,handoffs}` 를 품은 레포 루트
	DocsRoot string // `slides/status.html` 을 품은 레포 루트. 비면 대시보드 축을 안 본다
}

// Found 는 원본에서 **발견한** 것의 수와 바이트다. 해석 성공 여부와 무관하다.
//
// 대조표의 왼쪽 열이다. 이 값이 없으면 "해석 성공 200건"이 200건 중 200건인지
// 260건 중 200건인지 구분되지 않는다 — 대조표의 존재 이유가 바로 그 구분이다.
type Found struct {
	Files int
	Bytes int64
}

// Scan 은 원본을 훑은 결과다. 판정은 하지 않는다 — 그것은 [PlanImport] 몫이다.
type Scan struct {
	Sessions []SessionCard
	Items    []QueueItem
	Handoffs []Handoff
	Dash     Dashboard
	DashSeen bool
	DashPath string

	Found   map[string]Found // "sessions"|"queue"|"handoffs"|"dashboard"
	Rejects []Reject
}

var queueBuckets = []string{"items", "claims", "done", "dropped"}

// ScanSource 는 원본 넷을 훑어 읽는다.
//
// 디렉토리가 통째로 없으면 그것은 오류가 아니다(그 축을 안 쓰는 설치가 있다).
// 다만 **있는데 못 읽는 것**은 오류다 — 조용히 0건으로 접으면 "없다"와 "못 봤다"가 같아진다.
func ScanSource(src Source) (Scan, error) {
	sc := Scan{Found: map[string]Found{}}

	if strings.TrimSpace(src.CodeRoot) != "" {
		if err := scanSessions(&sc, src.CodeRoot); err != nil {
			return sc, err
		}
		if err := scanQueue(&sc, src.CodeRoot); err != nil {
			return sc, err
		}
		if err := scanHandoffs(&sc, src.CodeRoot); err != nil {
			return sc, err
		}
	}
	if strings.TrimSpace(src.DocsRoot) != "" {
		if err := scanDashboard(&sc, src.DocsRoot); err != nil {
			return sc, err
		}
	}
	sortRejects(sc.Rejects)
	return sc, nil
}

// mdFiles 는 디렉토리의 `*.md` 를 이름순으로 낸다. 없는 디렉토리는 빈 목록이다.
func mdFiles(dir string) ([]os.DirEntry, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("원본 디렉토리를 읽지 못했다(%q): %w", clip(dir, 200), err)
	}
	var out []os.DirEntry
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

func scanSessions(sc *Scan, root string) error {
	dir := filepath.Join(root, ".claude", "sessions")
	ents, err := mdFiles(dir)
	if err != nil {
		return err
	}
	f := sc.Found["sessions"]
	for _, e := range ents {
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return fmt.Errorf("세션 카드를 읽지 못했다(%q): %w", clip(e.Name(), 120), err)
		}
		f.Files++
		f.Bytes += int64(len(data))
		card, rs := ParseSessionCard(e.Name(), data)
		sc.Rejects = append(sc.Rejects, rs...)
		sc.Sessions = append(sc.Sessions, card)
	}
	sc.Found["sessions"] = f
	return nil
}

func scanQueue(sc *Scan, root string) error {
	f := sc.Found["queue"]
	for _, bucket := range queueBuckets {
		dir := filepath.Join(root, ".claude", "queue", bucket)
		ents, err := mdFiles(dir)
		if err != nil {
			return err
		}
		for _, e := range ents {
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				return fmt.Errorf("큐 항목을 읽지 못했다(%s/%s): %w", bucket, clip(e.Name(), 120), err)
			}
			f.Files++
			f.Bytes += int64(len(data))
			it, rs := ParseQueueItem(bucket, e.Name(), data)
			sc.Rejects = append(sc.Rejects, rs...)
			sc.Items = append(sc.Items, it)
		}
	}
	sc.Found["queue"] = f
	return nil
}

func scanHandoffs(sc *Scan, root string) error {
	dir := filepath.Join(root, ".claude", "handoffs")
	ents, err := mdFiles(dir)
	if err != nil {
		return err
	}
	f := sc.Found["handoffs"]
	for _, e := range ents {
		p := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("핸드오프를 읽지 못했다(%q): %w", clip(e.Name(), 120), err)
		}
		f.Files++
		f.Bytes += int64(len(data))
		at, from := HandoffTime(e.Name(), fileModTime(e))
		h := Handoff{
			File: e.Name(),
			// 큐의 `handoff:` 는 레포 루트 기준 상대경로 문자열이다. 슬래시로 고정한다 —
			// filepath 로 만들면 판이 바뀔 때 구분자가 달라져 대조가 조용히 전부 어긋난다.
			Rel:    ".claude/handoffs/" + e.Name(),
			Body:   string(data),
			At:     at,
			AtFrom: from,
		}
		if strings.TrimSpace(h.Body) == "" {
			sc.Rejects = append(sc.Rejects, Reject{
				Source: "handoff", Path: h.Rel, Code: "empty",
				Detail: "핸드오프 본문이 비었다 — 판단 표는 본문이 비면 받지 않는다(스키마 CHECK)", Fatal: true,
			})
			continue
		}
		sc.Handoffs = append(sc.Handoffs, h)
	}
	sc.Found["handoffs"] = f
	return nil
}

// fileModTime 은 mtime 폴백용 시각이다. 못 읽으면 영시각을 낸다 —
// 그러면 HandoffTime 이 "mtime" 축을 냈다는 사실과 함께 0001년이 보고에 그대로 뜬다.
// 지금 시각으로 메우면 그 실패가 정상값으로 위장한다.
func fileModTime(e os.DirEntry) time.Time {
	info, err := e.Info()
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

func scanDashboard(sc *Scan, root string) error {
	p := filepath.Join(root, "slides", "status.html")
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("대시보드를 읽지 못했다(%q): %w", clip(p, 200), err)
	}
	sc.DashSeen = true
	sc.DashPath = "slides/status.html"
	sc.Found["dashboard"] = Found{Files: 1, Bytes: int64(len(data))}

	block, err := ExtractDataBlock(string(data))
	if err != nil {
		sc.Rejects = append(sc.Rejects, Reject{
			Source: "dashboard", Path: sc.DashPath, Code: "boundary",
			Detail: err.Error(), Fatal: true,
		})
		return nil
	}
	d, rs := ParseDashboard(block)
	sc.Rejects = append(sc.Rejects, rs...)
	sc.Dash = d
	return nil
}
