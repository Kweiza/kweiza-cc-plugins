package store

import (
	"strings"
	"testing"

	"github.com/kweiza/flightdeck/internal/model"
)

// 판정 순수 함수의 시험.
//
// 이 파일의 단정은 전부 **호출부가 보는 것**을 본다 — 사유 문자열과 오류 문구다.
// 구현의 개념(어느 분기를 탔나)을 빌려 쓰면 "사유가 비어 있다"는 축을 원리적으로 못 본다.

// ─────────────────────────────────────────────────────────────────────────────
// PlanMigration
// ─────────────────────────────────────────────────────────────────────────────

func TestPlanMigration(t *testing.T) {
	cases := []struct {
		name       string
		hasTable   bool
		dbVersion  int
		objects    int
		wantAction MigrationAction
		wantBackup bool
		// 사유에 반드시 들어 있어야 하는 조각. 사유가 비면 "왜 거절인지"를 운영자가 못 안다.
		wantInReason string
	}{
		{"빈 파일이면 새로 적용", false, 0, 0, MigrateApply, false, "빈 DB"},
		{"버전표 없이 객체가 있으면 거절", false, 0, 12, MigrateReject, false, "schema_version 표가 없는데"},
		{"버전표만 있고 기록이 없으면 백업하고 적용", true, 0, 1, MigrateApply, true, "곧바로 끊긴"},
		// ★ schema.sql 은 멱등이 아니다(IF NOT EXISTS 가 schema_version 하나뿐).
		//   객체가 이미 있는 위에 다시 적용하면 "table already exists" 로 죽으면서
		//   DB 를 반쯤 만진 상태로 남긴다. 시도하지 않고 사유를 말하고 멈춘다.
		{"객체가 있는데 버전 기록이 없으면 거절", true, 0, 30, MigrateReject, false, "멱등이 아니라"},
		{"버전이 맞으면 아무것도 안 한다", true, 1, 30, MigrateNone, false, "이미 맞다"},
		{"DB 가 더 높으면 거절", true, 2, 30, MigrateReject, false, "바이너리를 올려라"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := PlanMigration(c.hasTable, c.dbVersion, c.objects, 1)
			if got.Action != c.wantAction {
				t.Errorf("Action = %q, want %q (사유: %s)", got.Action, c.wantAction, got.Reason)
			}
			if got.Backup != c.wantBackup {
				t.Errorf("Backup = %v, want %v (사유: %s)", got.Backup, c.wantBackup, got.Reason)
			}
			if !strings.Contains(got.Reason, c.wantInReason) {
				t.Errorf("사유에 %q 가 없다: %q", c.wantInReason, got.Reason)
			}
		})
	}
}

// DB 가 더 낮은데 올릴 경로가 없으면 거절이다.
// 지금은 v1 뿐이라 도달 경로가 없지만, 조용히 통과시키면
// v2 를 넣는 순간 "모르는 구 스키마 위에서 도는" 상태가 침묵으로 열린다.
func TestPlanMigrationRejectsOlderDBWithoutUpgradePath(t *testing.T) {
	p := PlanMigration(true, 1, 40, 3)
	if p.Action != MigrateReject {
		t.Errorf("db=1 code=3 인데 %q 를 냈다(사유: %s)", p.Action, p.Reason)
	}
	if !strings.Contains(p.Reason, "업그레이드 경로가 없다") {
		t.Errorf("사유가 이유를 말하지 않는다: %q", p.Reason)
	}
}

// 표 밖 케이스 ①: 사유는 **어떤 입력에도** 비면 안 된다.
// 표는 고른 몇 줄만 보므로, 안 고른 조합에서 사유가 빈 것을 원리적으로 못 잡는다.
func TestPlanMigrationAlwaysGivesReason(t *testing.T) {
	for _, hasTable := range []bool{false, true} {
		for dbVer := 0; dbVer <= 4; dbVer++ {
			for _, objects := range []int{0, 1, 2, 50} {
				for _, code := range []int{1, 2, 3} {
					p := PlanMigration(hasTable, dbVer, objects, code)
					if strings.TrimSpace(p.Reason) == "" {
						t.Fatalf("사유가 비었다: hasTable=%v db=%d objects=%d code=%d → %+v",
							hasTable, dbVer, objects, code, p)
					}
					switch p.Action {
					case MigrateNone, MigrateApply, MigrateReject:
					default:
						t.Fatalf("알 수 없는 판정 %q", p.Action)
					}
				}
			}
		}
	}
}

// 표 밖 케이스 ②: 미래 버전은 **어떤 조합에서도** 열리면 안 된다.
// 구 바이너리가 신 DB 를 여는 것이 이 함수가 막는 유일한 사고다.
func TestPlanMigrationNeverOpensFutureDB(t *testing.T) {
	for code := 1; code <= 5; code++ {
		for dbVer := code + 1; dbVer <= code+3; dbVer++ {
			for _, objects := range []int{1, 40} {
				p := PlanMigration(true, dbVer, objects, code)
				if p.Action != MigrateReject {
					t.Errorf("db=%d code=%d 인데 %q 를 냈다(사유: %s)", dbVer, code, p.Action, p.Reason)
				}
			}
		}
	}
}

// 표 밖 케이스 ③: 백업 없이 기존 데이터 위에 스키마를 적용하는 조합이 있으면 안 된다.
func TestPlanMigrationNeverAppliesOverDataWithoutBackup(t *testing.T) {
	for _, hasTable := range []bool{false, true} {
		for dbVer := 0; dbVer <= 2; dbVer++ {
			for _, objects := range []int{0, 1, 2, 99} {
				p := PlanMigration(hasTable, dbVer, objects, 1)
				if p.Action == MigrateApply && objects > 1 && !p.Backup {
					t.Errorf("객체 %d개 위에 백업 없이 적용한다: hasTable=%v db=%d (사유: %s)",
						objects, hasTable, dbVer, p.Reason)
				}
			}
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// JudgeClaim
// ─────────────────────────────────────────────────────────────────────────────

func TestJudgeClaim(t *testing.T) {
	cases := []struct {
		name         string
		found        bool
		state        model.ItemState
		holder       string
		requester    string
		wantOK       bool
		wantResume   bool
		wantInReason string
	}{
		{"열린 미선점 항목", true, model.ItemOpen, "", "S1", true, false, "선점 가능"},
		{"이미 자기 것이면 재개", true, model.ItemClaimed, "S1", "S1", true, true, "이미 이 세션의 선점"},
		{"남이 쥐고 있으면 그 이름이 사유에", true, model.ItemClaimed, "S2", "S1", false, false, "S2"},
		{"없는 항목", false, "", "", "S1", false, false, "그런 항목이 없다"},
		{"끝난 항목", true, model.ItemDone, "", "S1", false, false, "이미 끝난"},
		{"폐기된 항목", true, model.ItemDropped, "", "S1", false, false, "폐기된"},
		{"요청자가 비었다", true, model.ItemOpen, "", "", false, false, "요청자"},
		// 표 밖: 점유자는 없는데 상태만 claimed 인 불일치. 조용히 통과시키면 그 흔적이 사라진다.
		{"점유자 없는 claimed 상태", true, model.ItemClaimed, "", "S1", true, false, "점유자가 없다"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := JudgeClaim(c.found, c.state, c.holder, c.requester)
			if v.OK != c.wantOK {
				t.Errorf("OK = %v, want %v (사유: %s)", v.OK, c.wantOK, v.Reason)
			}
			if v.Resume != c.wantResume {
				t.Errorf("Resume = %v, want %v (사유: %s)", v.Resume, c.wantResume, v.Reason)
			}
			if !strings.Contains(v.Reason, c.wantInReason) {
				t.Errorf("사유에 %q 가 없다: %q", c.wantInReason, v.Reason)
			}
		})
	}
}

// 표 밖 케이스: 어떤 입력에도 사유가 비면 안 되고,
// **점유자가 있는데 거절이면 사유에 그 이름이 반드시 있어야 한다** —
// 이름이 없으면 누구에게 물어야 하는지 몰라 다시 추측이 시작된다.
func TestJudgeClaimAlwaysNamesTheHolder(t *testing.T) {
	states := []model.ItemState{model.ItemOpen, model.ItemClaimed, model.ItemDone, model.ItemDropped, "이상한값"}
	holders := []string{"", "S1", "01JABCDEF"}
	requesters := []string{"", "S1", "S9"}
	for _, found := range []bool{false, true} {
		for _, st := range states {
			for _, h := range holders {
				for _, r := range requesters {
					v := JudgeClaim(found, st, h, r)
					closed := st == model.ItemDone || st == model.ItemDropped
					if strings.TrimSpace(v.Reason) == "" {
						t.Fatalf("사유가 비었다: found=%v state=%q holder=%q req=%q", found, st, h, r)
					}
					// 점유자 지명 의무는 **열린 항목에 한정된다.** 닫힌 항목은 점유자가 누구든
					// 사실이 "이 항목은 끝났다"이고, 그때 점유자를 지명하면 "저 사람에게 물어라"라는
					// 엉뚱한 처방이 된다. 축을 이 조건으로 좁히는 것이 맞다.
					if !v.OK && found && !closed && h != "" && h != r && r != "" {
						if !strings.Contains(v.Reason, h) {
							t.Errorf("열린 항목인데 점유자 %q 가 사유에 없다: %q", h, v.Reason)
						}
					}
					if v.Resume && !v.OK {
						t.Errorf("Resume 인데 OK 가 아니다: found=%v state=%q holder=%q req=%q", found, st, h, r)
					}
					// ★ 리뷰가 찾은 거짓 초록을 닫는다.
					//
					// 이 스윕은 90조합을 전수로 돌면서도 "닫힌 항목은 재개할 수 없다"를 안 단정했다.
					// 그래서 JudgeClaim(true, ItemDone, "S1", "S1") → OK=true Resume=true 가
					// 스윕을 그대로 지났다. 전수 스윕이 있으면 그 축이 다 덮인 것처럼 읽히는데,
					// 스윕의 값어치는 **무엇을 단정하느냐**로 정해진다.
					if closed && (v.OK || v.Resume) {
						t.Errorf("닫힌 항목(state=%q)이 선점 가능으로 판정됐다: OK=%v Resume=%v 사유=%q",
							st, v.OK, v.Resume, v.Reason)
					}
				}
			}
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 인자 검증 순수 함수들
// ─────────────────────────────────────────────────────────────────────────────

func TestValidateSnapshot(t *testing.T) {
	base := model.Snapshot{Project: "p", Key: "k", Value: "42", Method: model.SnapshotCommand}
	cases := []struct {
		name         string
		mut          func(*model.Snapshot)
		wantErr      bool
		wantInReason string
	}{
		{"command 는 근거가 없어도 된다", func(s *model.Snapshot) {}, false, ""},
		{"manual 인데 근거 없음", func(s *model.Snapshot) { s.Method = model.SnapshotManual }, true, "근거"},
		{"manual + 근거", func(s *model.Snapshot) {
			s.Method = model.SnapshotManual
			s.Evidence = "12파트 전수 판정 2026-08-03"
		}, false, ""},
		// 표 밖: 공백만 든 근거. 스키마 CHECK(evidence <> '')는 이걸 통과시킨다 —
		// 그래서 1차 방어가 여기 있어야 한다.
		{"manual + 공백만 든 근거", func(s *model.Snapshot) {
			s.Method = model.SnapshotManual
			s.Evidence = "   \n\t "
		}, true, "근거"},
		{"project 누락", func(s *model.Snapshot) { s.Project = "" }, true, "project"},
		{"key 누락", func(s *model.Snapshot) { s.Key = "" }, true, "key"},
		{"모르는 method", func(s *model.Snapshot) { s.Method = "guess" }, true, "method"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := base
			c.mut(&s)
			err := ValidateSnapshot(s)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, c.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), c.wantInReason) {
				t.Errorf("오류에 %q 가 없다: %v", c.wantInReason, err)
			}
		})
	}
}

func TestValidateHolder(t *testing.T) {
	cases := []struct {
		name     string
		h        Holder
		wantErr  bool
		contains string
	}{
		{"세션만", Holder{SessionID: "S1"}, false, ""},
		{"잡만", Holder{JobID: "J1"}, false, ""},
		{"둘 다", Holder{SessionID: "S1", JobID: "J1"}, true, "둘 다 왔다"},
		{"둘 다 비었다", Holder{}, true, "비었다"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateHolder(c.h)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, c.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), c.contains) {
				t.Errorf("오류에 %q 가 없다: %v", c.contains, err)
			}
		})
	}
}

func TestValidateAfter(t *testing.T) {
	cases := []struct {
		name    string
		a       model.After
		wantErr bool
	}{
		{"항목 하나", model.After{Item: "t5-x"}, false},
		{"잡 하나", model.After{Job: "J1"}, false},
		{"sha 하나", model.After{SHA: "c8206a9"}, false},
		{"전부 빔", model.After{}, true},
		{"둘", model.After{Item: "t5-x", SHA: "c8206a9"}, true},
		{"셋", model.After{Item: "a", Job: "b", SHA: "c"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := ValidateAfter(c.a); (err != nil) != c.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, c.wantErr)
			}
		})
	}
}

func TestValidateFinish(t *testing.T) {
	cases := []struct {
		name    string
		state   model.ItemState
		reason  string
		wantErr bool
	}{
		{"done 은 사유 불요", model.ItemDone, "", false},
		{"dropped 는 사유 필수", model.ItemDropped, "", true},
		{"dropped + 사유", model.ItemDropped, "계약 개정으로 무의미해짐", false},
		{"open 은 종료 상태가 아니다", model.ItemOpen, "", true},
		{"claimed 는 종료 상태가 아니다", model.ItemClaimed, "", true},
		{"모르는 값", "finished", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := ValidateFinish(c.state, c.reason); (err != nil) != c.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, c.wantErr)
			}
		})
	}
}

func TestValidateSessionState(t *testing.T) {
	cases := []struct {
		name    string
		state   model.SessionState
		why     string
		wantErr bool
	}{
		{"active", model.SessionActive, "", false},
		{"paused", model.SessionPaused, "", false},
		{"done", model.SessionDone, "", false},
		{"blocked 는 사유 필수", model.SessionBlocked, "", true},
		{"blocked + 사유", model.SessionBlocked, "계약 세션 대기", false},
		{"모르는 값", "zombie", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := ValidateSessionState(c.state, c.why); (err != nil) != c.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, c.wantErr)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// CheckPragmas · FTSQuery
// ─────────────────────────────────────────────────────────────────────────────

func TestCheckPragmas(t *testing.T) {
	ok := map[string]string{"busy_timeout": "5000", "foreign_keys": "1", "journal_mode": "wal"}
	if err := CheckPragmas(ok); err != nil {
		t.Fatalf("정상 값인데 거절했다: %v", err)
	}
	// 대소문자는 허용한다 — SQLite 가 journal_mode 를 판에 따라 다르게 낸다.
	if err := CheckPragmas(map[string]string{
		"busy_timeout": "5000", "foreign_keys": "1", "journal_mode": "WAL"}); err != nil {
		t.Errorf("대문자 WAL 을 거절했다: %v", err)
	}

	// ★ 가장 위험한 조합: foreign_keys 가 안 걸린 것. FK 위반이 조용히 통과한다.
	bad := map[string]string{"busy_timeout": "5000", "foreign_keys": "0", "journal_mode": "wal"}
	err := CheckPragmas(bad)
	if err == nil {
		t.Fatal("foreign_keys=0 인데 통과시켰다")
	}
	if !strings.Contains(err.Error(), "foreign_keys") {
		t.Errorf("오류가 어느 pragma 인지 말하지 않는다: %v", err)
	}

	// 표 밖: 아예 못 읽은 경우와 값이 틀린 경우는 처방이 다르므로 문구가 달라야 한다.
	missing := CheckPragmas(map[string]string{"busy_timeout": "5000", "foreign_keys": "1"})
	if missing == nil {
		t.Fatal("journal_mode 가 없는데 통과시켰다")
	}
	if !strings.Contains(missing.Error(), "읽지 못함") {
		t.Errorf("부재와 불일치를 구분하지 않는다: %v", missing)
	}
}

func TestFTSQuery(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"단어 하나", "핸드오프", `"핸드오프"`},
		{"두 단어는 AND", "랜딩 실패", `"랜딩" "실패"`},
		{"빈 문자열", "", ""},
		{"공백만", "   \t ", ""},
		// FTS5 문법 문자가 그대로 가면 구문 오류로 죽고, 그러면
		// "결과 없음"과 "질의가 깨짐"이 같은 빈 목록으로 접힌다.
		{"하이픈", "not-done", `"not-done"`},
		{"큰따옴표는 겹쳐 이스케이프", `say "hi"`, `"say" """hi"""`},
		{"별표", "tools*", `"tools*"`},
		{"괄호와 콜론", "a:(b)", `"a:(b)"`},
		{"OR 도 리터럴이 된다", "a OR b", `"a" "OR" "b"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := FTSQuery(c.raw); got != c.want {
				t.Errorf("FTSQuery(%q) = %q, want %q", c.raw, got, c.want)
			}
		})
	}
}
