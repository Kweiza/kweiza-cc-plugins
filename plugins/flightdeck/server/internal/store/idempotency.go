package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// 멱등 기록 — 재기동을 넘겨야 하는 쓰기의 응답을 보관한다.
//
// ★ 왜 DB 인가: 메모리에만 두면 서버를 재기동하는 순간 기억이 통째로 사라진다.
// 그런데 그 조합이 나는 상황이 정확히 설계 §7 이 겨냥한 시나리오다 —
// 서버가 죽어 클라이언트 아웃박스가 쌓이고, 살아나서 재생이 돈다. 그때 서버는
// 방금 재기동해 기억이 비어 있고, **판단은 추가 전용이라 중복이 안 지워진다.**
//
// ★ 무엇을 남기나: 중복이 영구히 남는 쓰기만이다. 그 판정은 표면 계층의 순수 함수가
// 하고(api.JudgePersistIdempotency) 이 계층은 시키는 대로 저장한다 —
// "어느 라우트인가"는 HTTP 의 개념이라 저장 계층이 알면 두 벌이 된다.
//
// ★ 5xx 는 저장하지 않는다. 그 규율은 스키마의 CHECK 로도 걸려 있고 여기서도 먼저 막는다 —
// CHECK 위반 문구는 "무엇이 왜 거절됐나"를 말하지 않는다(1차 방어와 최후 방어).

// IdemRecord 는 처리 끝난 쓰기 하나의 응답이다.
type IdemRecord struct {
	Key         string
	Fingerprint string
	Status      int
	ContentType string
	Body        []byte
	At          time.Time
}

// ValidateIdemRecord 는 저장 가능한 기록인지 본다. 순수 함수다.
//
// 사유를 문장으로 낸다 — 호출부가 사용자에게 옮길 말이 있어야 하고,
// 무엇보다 "5xx 를 저장하려 했다"는 사실 자체가 규율 위반의 신호라 사유가 필요하다.
func ValidateIdemRecord(r IdemRecord) error {
	switch {
	case r.Key == "":
		return errors.New("멱등 키가 비었다")
	case r.Fingerprint == "":
		return errors.New("멱등 지문이 비었다 — 지문이 없으면 키 재사용을 탐지할 축이 사라진다")
	case r.Status < 100 || r.Status >= 600:
		return fmt.Errorf("상태코드가 HTTP 범위 밖이다(%d)", r.Status)
	case r.Status >= http.StatusInternalServerError:
		return fmt.Errorf("5xx(%d)는 저장하지 않는다 — 일시 장애를 영구 응답으로 굳히면 "+
			"하류가 복구된 뒤에도 같은 실패만 돌려주게 된다", r.Status)
	default:
		return nil
	}
}

// PutIdemRecord 는 기록 하나를 저장하고, 같은 트랜잭션에서 낡은 것을 청소한다.
//
// 청소를 같은 자리에 둔 이유: 별도 주기 작업으로 빼면 그 작업이 안 도는 배포에서
// 이 표만 무한히 큰다. 저장이 곧 청소이면 안 도는 경로가 없다.
//
//   - ttl 이하는 남긴다. 0 이하면 청소하지 않는다.
//   - max 를 넘으면 오래된 것부터 걷어낸다. 0 이하면 개수 제한이 없다.
func (s *Store) PutIdemRecord(ctx context.Context, r IdemRecord, ttl time.Duration, max int) error {
	if err := ValidateIdemRecord(r); err != nil {
		return fmt.Errorf("멱등 기록 저장 거절(key=%q): %w", clip(r.Key, 64), err)
	}
	if r.At.IsZero() {
		r.At = nowStamp()
	}
	return s.Tx(ctx, func(t *Tx) error {
		if _, err := t.tx.ExecContext(t.ctx, `
			INSERT INTO idempotency(key, fingerprint, status, ctype, body, at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET
			  fingerprint = excluded.fingerprint, status = excluded.status,
			  ctype = excluded.ctype, body = excluded.body, at = excluded.at`,
			r.Key, r.Fingerprint, r.Status, r.ContentType, r.Body, fmtTime(r.At)); err != nil {
			return writeErr(err, writeTarget{Target: TargetIdempotency, ID: r.Key},
				"멱등 기록 저장 실패(key=%q status=%d)", clip(r.Key, 64), r.Status)
		}
		if ttl > 0 {
			if _, err := t.tx.ExecContext(t.ctx,
				`DELETE FROM idempotency WHERE at < ?`, fmtTime(r.At.Add(-ttl))); err != nil {
				return fmt.Errorf("멱등 기록 만료 청소 실패(ttl=%s): %w", ttl, err)
			}
		}
		if max > 0 {
			if _, err := t.tx.ExecContext(t.ctx, `
				DELETE FROM idempotency WHERE key IN (
				  SELECT key FROM idempotency ORDER BY at DESC, key DESC LIMIT -1 OFFSET ?)`,
				max); err != nil {
				return fmt.Errorf("멱등 기록 초과분 청소 실패(max=%d): %w", max, err)
			}
		}
		return nil
	})
}

// GetIdemRecord 는 키 하나의 기록을 읽는다. 없으면 ErrNotFound 를 감싼 오류다.
func (s *Store) GetIdemRecord(ctx context.Context, key string) (IdemRecord, error) {
	var r IdemRecord
	var at string
	err := s.db.QueryRowContext(ctx, `
		SELECT key, fingerprint, status, ctype, body, at FROM idempotency WHERE key = ?`, key).
		Scan(&r.Key, &r.Fingerprint, &r.Status, &r.ContentType, &r.Body, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return r, fmt.Errorf("멱등 기록 %q 가 %w", clip(key, 64), ErrNotFound)
	}
	if err != nil {
		return r, fmt.Errorf("멱등 기록 조회 실패(key=%q): %w", clip(key, 64), err)
	}
	if r.At, err = parseTime(at); err != nil {
		return r, err
	}
	return r, nil
}

// CountIdemRecords 는 지금 남아 있는 기록 수다. 진단·시험용이다.
func (s *Store) CountIdemRecords(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM idempotency`).Scan(&n); err != nil {
		return 0, fmt.Errorf("멱등 기록 수 조회 실패: %w", err)
	}
	return n, nil
}
