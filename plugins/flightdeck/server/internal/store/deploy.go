package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// eventServerDeploy 는 **실행 파일이 바뀐 기동**이다. 기동 자체가 아니다.
const eventServerDeploy = "server.deploy"

// NoteServerBuild 는 이 기동의 실행 파일 정체를 원장의 마지막 배포와 견주고, 다르면
// 배포로 적는다. 배포 시각을 아는 유일한 길이다.
//
// ★ 왜 기동 시각에 관측하나. 성공한 자기 갱신은 `syscall.Exec` 로 프로세스가 갈아치워져
// **원장에 한 행도 안 남는다**(DESIGN §10, 그리고 그것이 옳다 — 교체 직전에 쓰면 그 쓰기가
// 실패해도 아무도 모른다). 그래서 교체를 관측하는 대신 **교체된 뒤의 자기 자신을** 관측한다.
// 이 길은 자기 갱신뿐 아니라 수동 교체·컨테이너 이미지 갱신도 같이 잡는다 — 원장이 알고
// 싶은 것은 "누가 바꿨나"가 아니라 "지금 도는 것이 언제부터 이것인가"다.
//
// ★ 기동마다 적지 않는다. 서버는 갱신 말고도 다시 뜬다(수동 재기동·재부팅·컨테이너 재시작).
// 그것까지 적으면 "마지막 배포"가 "마지막 기동"이 되어 이 축이 뜻을 잃는다.
//
// ★ 관측 못 한 정체는 **아무 말도 안 한다.** ExeID 는 관측 실패를 "관측 안 됨"으로 내는데
// (selfwatch.go — 모르는 것은 같은 것이 아니다), 그것을 정체로 받으면 관측이 흔들릴 때마다
// 가짜 배포가 남고 그 시각으로 자른 지표가 근거 없이 리셋된다. 빈 값이면 false 를 답하고
// 원장을 안 건드린다.
//
// exe 는 안정 식별자면 무엇이든 된다. 지금 호출부는 ExeID.String()(ino·size·mtime)을 준다.
func (s *Store) NoteServerBuild(ctx context.Context, exe string) (deployed bool, err error) {
	exe = strings.TrimSpace(exe)
	if exe == "" {
		return false, nil
	}
	var payload string
	row := s.db.QueryRowContext(ctx,
		`SELECT payload FROM event WHERE kind=? ORDER BY id DESC LIMIT 1`, eventServerDeploy)
	switch err := row.Scan(&payload); {
	case errors.Is(err, sql.ErrNoRows):
		// 기준선이 없다 — 이 관측이 기준선이 된다.
	case err != nil:
		return false, fmt.Errorf("마지막 배포 조회 실패: %w", err)
	default:
		var last struct {
			Exe string `json:"exe"`
		}
		// ★ payload 를 못 읽으면 배포로 친다. 여기서 침묵하면 정체를 잃은 채로 영영
		//   "안 바뀌었다"가 되어, 진짜 배포가 와도 원장이 모른다.
		if jerr := json.Unmarshal([]byte(payload), &last); jerr == nil && last.Exe == exe {
			return false, nil
		}
	}
	if err := s.TryLogEvent(ctx, eventServerDeploy, "", "", map[string]any{"exe": exe}); err != nil {
		return false, fmt.Errorf("배포 기록 실패(exe=%q): %w", clip(exe, 120), err)
	}
	return true, nil
}

// LastDeployAt 은 지금 도는 실행 파일이 자리 잡은 시각이다.
//
// ★ **못 잼과 0 을 가른다.** 배포 기록이 없으면 ok=false 이고, 그때 영값 시각을 창의
// 시작으로 쓰면 "전 역사"가 조용히 창이 된다 — 호출부가 그 갈래를 반드시 다뤄야 한다.
func (s *Store) LastDeployAt(ctx context.Context) (at time.Time, ok bool, err error) {
	var raw string
	row := s.db.QueryRowContext(ctx,
		`SELECT at FROM event WHERE kind=? ORDER BY id DESC LIMIT 1`, eventServerDeploy)
	switch err := row.Scan(&raw); {
	case errors.Is(err, sql.ErrNoRows):
		return time.Time{}, false, nil
	case err != nil:
		return time.Time{}, false, fmt.Errorf("마지막 배포 시각 조회 실패: %w", err)
	}
	parsed, perr := parseTime(raw)
	if perr != nil {
		return time.Time{}, false, fmt.Errorf("마지막 배포 시각 해석 실패: %w", perr)
	}
	return parsed, true, nil
}
