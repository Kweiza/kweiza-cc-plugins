package store

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

// mustMigrate 는 시험이 쓸 DB 를 이 바이너리의 스키마까지 올린다.
//
// ★ 이 헬퍼가 생긴 이유는 설계 §7 처방 ①(적용을 기동에서 분리)이다. 그 전까지 시험은
// Open() 한 번으로 빈 DB 를 만들고 스키마까지 세웠는데, 이제 Open 은 안 맞는 DB 를 **거절**한다.
//
// ★ **판정을 여기서 다시 하지 않는다.** Migrate 가 이미 멱등이라(MigrateNone 이면 아무것도
// 안 한다) 같은 경로에 두 번 불러도 안전하다. 시험마다 "올려야 하나" 를 재게 하면 그 판정이
// 시험 수만큼 복제되고, 복제된 판정은 본체가 바뀔 때 함께 안 바뀐다.
func mustMigrate(t *testing.T, path string) {
	t.Helper()
	if err := Migrate(context.Background(), path, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("DB 적용 실패(%s): %v", path, err)
	}
}

// mustMigrateTo 는 target 판까지만 올린다.
//
// ★ 중간 판을 시험하는 자리가 생겼다: 증분 011 이 item_dependents 를 걷은 뒤로, 010 이
// "값을 비운다"를 보려면 **10판에서 멈춰야** 한다. 최신까지 올리면 표 자체가 없어서
// 010 이 무엇을 했는지 볼 자리가 사라진다.
func mustMigrateTo(t *testing.T, path string, target int) {
	t.Helper()
	if err := MigrateTo(context.Background(), path, target, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("DB 적용 실패(%s, target=%d): %v", path, target, err)
	}
}
