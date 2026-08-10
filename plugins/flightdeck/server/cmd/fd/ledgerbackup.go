package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/ledger"
	"github.com/kweiza/flightdeck/internal/store"
)

// 판단 원장을 **주기적으로** 내보낸다 — 설계 §7 의 "매시간 판단 백업" 처방.
//
// ★ 왜 serve 안 티커인가. 셋 중에서 골랐다(호스트 cron · compose 두 번째 서비스 · 여기).
// selfwatch 가 정확히 이 모양의 선례다 — serve 가 소유하는 티커가 주기마다 실제 일을 한다.
// 호스트 cron 은 컨테이너 자족성을 깬다(이 배포는 `docker compose up -d` 하나가 전부다).
// 둘째 서비스는 잡 하나 때문에 배포 표면을 두 배로 만들고, 그 서비스도 자기 안에 주기
// 장치가 또 필요하다. 그리고 이 프로세스는 **이미 그 DB 를 쥐고 있다** — 다른 자리에서
// 돌리면 여는 쪽을 한 벌 더 만들고 잠금 모드를 또 정해야 한다.
//
// ★ 이 잡이 관측되는 유일한 자리는 **로그**다. /healthz 에 축을 안 냈다 — 그러면
// internal/api 표면이 늘고 이 항목의 경로 밖이다. 대신 회차마다 INFO(썼다/건너뛰었다)와
// 실패마다 ERROR 를 남긴다. **이것은 알고 남기는 구멍이다**: 잡이 조용히 실패하면
// `docker logs` 를 보기 전까지 아무도 모르고, 그동안 설계 문서는 "백업이 돈다"고 말한다.
// 후속 항목이 그 축을 낸다.
const ledgerBackupInterval = time.Hour

// LedgerOutDir 는 원장 산출물 자리를 정한다. 순수 함수다.
//
// ★ DB 와 **다른 볼륨**이어야 한다(설계 §7). compose.yaml 이 그 분리를 "백업 잡이 생기는
// 시점에" 로 접어 뒀고 그 시점이 여기다. 다만 정직하게 적는다 — 기본값은 DB 와 같은
// 디스크의 형제 디렉토리다. 진짜 분리는 FD_LEDGER 를 다른 매체로 겨눠야 성립한다.
// 이 함수가 사는 것은 **마운트를 가를 수 있는 자리**이지 분리 그 자체가 아니다.
func LedgerOutDir(get func(string) (string, bool), home string, dataDirExists bool) string {
	if v, ok := get("FD_LEDGER"); ok && strings.TrimSpace(v) != "" {
		return filepath.Clean(v)
	}
	if dataDirExists {
		return "/ledger"
	}
	if strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".flightdeck-ledger")
	}
	return filepath.Join(os.TempDir(), "flightdeck-ledger")
}

// ledgerDataUnchanged 는 이 회차의 데이터 파일이 자리의 것과 바이트 동일한지 본다.
//
// ★ 매니페스트는 뺀다. exported_at 이 회차마다 새로 찍혀서 **내용이 하나도 안 바뀐
// 회차도 매니페스트만은 늘 다르다.** 그것까지 세면 이 비교가 언제나 거짓이 되고,
// 안 바뀐 원장을 매시간 다시 쓰게 된다(그리고 ③ 매시간 git 커밋이 붙는 날에는
// 무의미한 커밋이 매시간 쌓인다 — 결정적 출력을 계약으로 삼은 이유가 그것인데
// 매니페스트가 그 계약 밖에 있다).
//
// 하나라도 못 읽으면 "바뀌었다"로 본다. 자리가 비었거나 반쯤 덮인 상태가 그것이고,
// 둘 다 다시 쓰는 것이 옳다.
func ledgerDataUnchanged(files map[string][]byte, dir string) bool {
	seen := 0
	for name, want := range files {
		if name == ledger.ManifestName {
			continue
		}
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !bytes.Equal(got, want) {
			return false
		}
		seen++
	}
	// 데이터 파일이 하나도 없는 인코딩은 이 잡의 입력이 아니다 — 같다고 말하면
	// 빈 자리를 "이미 최신"으로 오인한다.
	return seen > 0
}

// ledgerBackupOnce 는 한 회차다. 실제로 썼으면 true 를 낸다.
//
// ★ store.Open 을 다시 안 부른다. 이 프로세스가 쥔 핸들을 그대로 쓴다 —
// ReadLedger 는 살아 있는 DB 에서 부르도록 설계돼 있고(한 트랜잭션 안에서 여섯 표),
// 여기서 OpenLedger 를 또 열면 같은 파일에 커넥션이 두 벌 생긴다.
func ledgerBackupOnce(ctx context.Context, st *store.Store, outDir, at string) (bool, error) {
	dump, err := st.ReadLedger(ctx)
	if err != nil {
		return false, err
	}
	files, _, err := ledger.Encode(dump, store.SchemaVersion, at)
	if err != nil {
		return false, err
	}
	if ledgerDataUnchanged(files, outDir) {
		return false, nil
	}
	if _, err := ledger.Write(files, outDir); err != nil {
		return false, err
	}
	return true, nil
}

// runLedgerBackup 은 주기 잡이다. ctx 가 끝나면 돌아온다.
//
// ★ 기동 직후에 한 번 돈다. 그래야 서버가 오래 안 떠 있던 판에서도 최신 원장이 곧바로
// 생기고, 안 바뀐 회차는 위 비교가 걸러 준다(읽기와 인코딩만 돌고 쓰기는 없다).
//
// ★ 실패해도 서버를 안 죽인다. 백업이 실패했다고 판단을 못 받는 것이 더 나쁘다 —
// 그 실패는 ERROR 로 남고 다음 회차가 다시 시도한다.
func runLedgerBackup(ctx context.Context, log *slog.Logger, st *store.Store, outDir string, every time.Duration) {
	tick := func() {
		wrote, err := ledgerBackupOnce(ctx, st, outDir, nowStampString())
		switch {
		case err != nil:
			log.Error("판단 원장 백업 실패 — 다음 회차에 다시 시도한다",
				"route", clip(outDir, 200), "error", err.Error())
		case wrote:
			log.Info("판단 원장 백업", "route", clip(outDir, 200), "outcome", "wrote")
		default:
			log.Info("판단 원장 백업", "route", clip(outDir, 200), "outcome", "unchanged")
		}
	}

	tick()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			tick()
		}
	}
}
