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
//
// ★ **뜨지 못한 기동은 여기까지 안 온다.** 호출부(cmd/fd 의 runServe)가 api.Listen 이
// 성공한 **뒤에만** noteBuild 를 부른다 — 포트를 이미 물려 곧바로 죽는 기동은 원장에
// 닿기 전에 조기 반환한다(TestServeSkipsDeployNoteWhenBindFails 가 그 순서를 붙든다).
//
// ★ **남는 경계 하나: 바인드에 성공한 임시 기동.** 다른 포트로 띄운 `go run` 은 실제로
// 리스너를 열므로 이 정의 안에서 배포로 적힌다. 그것이 이론상 오염인 이유는 compose 가
// `~/.flightdeck:/data` 를 마운트해 **호스트의 임시 기동과 컨테이너가 같은 DB** 를 열기
// 때문이다. 그러면 그 임시 정체가 마지막 배포로 남고, 다음 컨테이너 재기동(배포가 아닌)이
// exe 불일치로 배포를 또 만든다.
//
// ★ **그런데 안 가르기로 했다 — 실측이 0건이다**(2026-08-12, 원장 4일치).
// `server.deploy` 7건이 전부 **크기가 단조 증가**했다. 15.1MB → 15.5MB, 코드가 늘면서
// 커지는 진짜 배포의 모양이다. 오염이 있었다면 즉시 드러났을 것이다 — Dockerfile 이
// `-ldflags="-s -w"` 로 심볼을 벗기는데 로컬 `go build` 는 안 벗어서 **22.8MB**,
// 47% 크다. 그런 값 하나가 끼면 단조성이 깨진다. 안 깨졌다.
//
// 가르는 축 셋을 다 검토하고 버렸다. **정본 주소**는 근거 없는 상수가 하나 늘고 컨테이너가
// `--addr` 를 바꾸면 배포가 통째로 안 적힌다. **실행 파일 자리**(`/tmp/go-build…/exe/fd`)는
// 휴리스틱이고 `BinCacheDir` 과의 관계를 새로 정리해야 한다. **DB 경로**는 사람의 규율에
// 의존한다 — 대신 그 사실을 README 에 적는 쪽을 골랐다(그 절이 "도커 없이 돌리려면"이라
// 컨테이너와의 공존을 애초에 전제하지 않는다).
//
// **이 판단을 뒤집을 조건**: `LastDeployAt` 이 실제 소비자를 얻을 때다(지금은 시험뿐 —
// `service.AckWindow` 의 `since` 후보로 대기 중이다). 그 배선을 붙이는 사람은 이 파일을
// 어차피 열어야 하므로 여기 적어 둔다 — 그때는 **이미 오염된 원장을 안고 시작한다**는 것을
// 알고 미루는 것이어야 한다. 위 실측을 다시 돌려 단조성이 여전한지부터 봐라.
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
//
// ★ 이 값은 **리스너가 열린 기동**의 시각이다(NoteServerBuild 의 ★). 앞선 판에서 이
// 독스트링이 거짓이던 기간이 있었고 — 바인드 전에 적혀 뜨지도 못한 바이너리의 시각이
// 실렸다 — 지금은 순서가 그것을 막는다.
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
