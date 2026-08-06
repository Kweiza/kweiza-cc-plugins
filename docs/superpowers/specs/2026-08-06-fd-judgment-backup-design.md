# 판단 백업 — `fd export --judgments` 설계

항목: `fd-judgment-backup-missing` · 브랜치 `fd-judgment-backup-missing` (base `29d9041`)
작성: 2026-08-06

## 0. 앵커 규약

**이 문서는 DESIGN.md 의 절대 행번호를 쓰지 않는다.** 형제 브랜치 둘(`fd-banner-legacy-guard`,
`fd-design-says-three-skills-but-four-exist`)이 DESIGN.md 의 30줄 블록을 지우고 있어, 랜딩 순서에
따라 §7 이하가 약 30줄 이동한다. 앵커는 절 제목·인용문·함수 이름으로 잡는다.

---

## 1. 왜

`judgment`·`judgment_link`·`snapshot` 은 §5 가 "원리적으로 파생 불가"로 못박은 자산이고,
지금 `~/.flightdeck/fd.db` 파일 하나 안에만 있다. 설계 §7 은 이것을 매시간 내보내
별도 bare git 레포에 커밋한다고 약속했으나 **구현이 없다.**

### 실측 (2026-08-06)

| | 항목 본문(08-04) | 지금 |
|---|---|---|
| `judgment` | 330행 | **984행** |
| `judgment_link` | 354행 | **1,413행** |
| `snapshot` | 12행 | 12행 |

본문 합 1.78MB · 최장 본문 **74,227B** · 프로젝트 분포는 `context-platform` 681 ·
`kweiza-cc-plugins` 302 · `stt-meetings` 1. `project` NULL 0행, `session_id` NULL 168행,
`title` NULL 17행, `supersedes` NOT NULL 16행.

`journal.git` 없음 · `logs/` 없음. `~/.flightdeck` 에 `.bak` 계열 4개 38MB 가 쌓여 있고
정리 코드는 0건이다. **이틀 만에 판단이 3배로 늘었다 — 위험이 커졌지 줄지 않았다.**

### 항목 본문의 근거 하나가 지금은 거짓이다

항목은 `grep -rn "journal" server/ --include=*.go → 0건` 을 근거로 들었다. 지금 이 grep 은
**9건**을 낸다. 전부 SQLite `journal_mode(WAL)` 용례(`store.go`, `verdict_test.go`)이고 판단
저널과 무관하므로 **결론은 여전히 참**이지만, 다음 사람이 같은 grep 을 돌리면 "있네"로 읽는다.
이 문서와 §7 각주는 그 오독을 막는 문장을 포함한다.

---

## 2. 결정된 것

| 축 | 결정 | 근거 |
|---|---|---|
| 복구 등급 | **무손실** — 되읽으면 표가 복원된다 | §5 가 파생 불가로 못박은 자산이다. 읽기용 아카이브는 `supersedes`·link·`input_digest` 를 잃는다 |
| 범위 | **DB 전량** (프로젝트 구분 없음) | 깨지는 것은 파일 하나다. `--project` 하나면 984 중 681만 나가고 `snapshot` 12행은 전부 한 프로젝트에 있다 |
| 선점 범위 | **①문서 실측 + ②손으로 부르는 명령**까지 | 항목 본문·§11 "떼어낼 조각부터". ③(매시간·bare 레포·별도 볼륨)은 followup |
| 아웃박스 | **안 덮는다.** 문서가 그 구멍을 명시한다 | `~/.local/state/flightdeck/outbox/rejected.jsonl` 에 DB 에 못 들어간 판단 1건이 실재한다. 넣으면 상태 디렉토리 갈림(`machine-id` 두 벌)과 시각 표기 차이(나노초 vs 마이크로초)까지 이 명령의 문제가 된다 |
| 격리 | **이행이 필요하면 거절 + 단일 읽기 Tx + tmp→rename** | 아래 §4 |

---

## 3. 표면

```
fd export --judgments --out <디렉토리>
```

기존 `runExport` 안의 분기다. 새 서브명령이 아니므로 DESIGN §6 의 CLI 목록은 안 바뀌고,
원칙 ②가 요구하는 "MCP 도구를 안 늘렸다" 문장이 그대로 성립한다.

### 플래그 계약

- `--to-legacy` 와 **배타.** 둘 다 없으면 rc=2, 둘 다 있으면 rc=2.
  `runImport` 의 `--apply`/`--dry-run` 모순 처리가 선례다.
  기존 거절 문구 `지금 있는 되쓰기 형식은 --to-legacy 하나다` 를 둘을 낸 문구로 정정한다.
- `--out` 필수. 기존 가드와 문구를 그대로 상속한다.
- `--project` 를 **명시하면 거절**한다(rc=2, "판단 백업은 DB 전량이다").
  조용히 무시하면 백업이 반쪽인 걸 아무도 모른다.
  **거절은 `fs.Visit` 로 본 명시적 플래그에만 적용한다** — `FD_PROJECT` 는 훅이 항상 심으므로
  환경변수까지 거절 조건에 넣으면 정상 세션에서 이 명령이 아예 안 돈다.
- `--db` 와 `--force` 는 기존 그대로. **둘 다 이미 `runExport` 에 있다** — 새 플래그는
  `--judgments` 하나뿐이다.

### 출력 자리 판정

`legacy.InspectOutTarget` 을 그대로 부르되, `--judgments` 모드의 판정만 다르게 접는다:

| 상태 | 판정 |
|---|---|
| 비어 있음 | 통과 |
| `manifest.json` 이 우리 형식 | **통과** (갱신으로 본다) |
| 그 밖에 파일이 있음 | `--force` 요구 (`ForceAllows` 의 `not-empty` 그대로) |
| git 작업 트리 안 | **거절, `--force` 로도 안 됨** (기존 그대로) |
| `.claude/` 존재(`has-legacy`) | `--force` 요구 — 원장을 살아 있는 레거시 트리에 쏟는 것은 실수일 공산이 크다 |

> `has-legacy` 를 "안 본다"로 적었던 것을 구현에서 바꿨다. `JudgeOutTarget` 은
> `git-worktree` → `has-legacy` → `not-empty` 순으로 **하나만** 낸다. `has-legacy` 를 특별히
> 무시하려면 판정 함수를 갈라야 하는데, 그 갈래가 사는 값이 "레거시 트리에도 `--force` 없이
> 쓴다"뿐이다 — 그건 사는 게 아니라 잃는 것이다.

`git-worktree` 가드를 상속하는 이유: 그 취지는 "사용자 레포에 산출물을 쏟지 마라"이고
2~3MB JSONL 넷에도 그대로 유효하다. ③에서 `journal.git` 작업본에 쓸 때 이 벽을 마주하지만,
**아직 오지 않은 요구를 근거로 지금 있는 안전장치를 빼지 않는다.** 그때 가른다.

`manifest.json` 인식이 없으면 두 번째 실행부터 매번 `--force` 를 요구받는다 — 백업은 같은 자리에
계속 쓰는 것이 정상 사용이다.

### 출력

기존 export 의 4블록 형식을 따른다.

```
fd export --judgments · DB 전량 → /tmp/backup
판단 984 · 링크 1413 · 스냅숏 12 (파일 4)
── 이 백업이 안 덮는 것 (2건)
  · 아웃박스에 갇힌 판단(pending·rejected) — 이 명령은 DB 만 읽는다
  · judgment_fts — 되읽기 때 삽입 트리거가 다시 만든다(손실 0)
```

**안 덮는 것을 코드가 열거하고 시험이 문다.** `legacy.RoundTripLosses` 와 같은 형태다 —
이 저장소는 이미 "손실을 코드가 열거하고 `roundtrip_test.go` 가 그 목록대로만 잃는지 단정하는"
문법을 갖고 있다.

종료 코드는 기존 규약 그대로: 2=인자/가드 거절, 1=실행 실패, 0=성공.
실패 경로마다 `a.log.Error` 와 `fmt.Fprintf(out, ...)` 를 둘 다 낸다.

---

## 4. 격리 — 백업이 백업 대상을 안 바꾼다

### 열기

`store.Open`(=`OpenWithLogger`)을 **쓰지 않는다.** 그것은 `verifyPragmas` 뒤 반드시 `s.migrate`
를 돌고, 판정에 따라 증분을 적용하며 그 앞에서 `VACUUM INTO '<db>.bak-<UTC>'` 를 뜬다.

대신:

1. `ProbeMigration(ctx, path)` 으로 먼저 판정한다. 이 함수는 `readMigrationState` 로 읽기만 하고
   `PlanMigration` 이라는 **같은 순수 함수**로 판정한다.
2. 판정이 "이행이 필요하다"면 **백업을 거절한다**(rc=1, 사유 명시).
   이 바이너리가 DB 를 이행시킬 수 있다는 뜻이고, 백업이 그 계기가 되어서는 안 된다.
3. 통과하면 **백업 전용 DSN** 으로 열되 `s.migrate` 를 타지 않는다. `store` 패키지 안의 새 파일
   이므로 `Store{db, path, log}` 를 직접 조립할 수 있다.

백업 DSN 은 기본 `dsn()` 에서 두 곳이 다르다:

| pragma | 기본 | 백업 | 왜 |
|---|---|---|---|
| `_txlock` | `immediate` | **`deferred`** | 읽기 스냅숏을 잡되 서버 쓰기를 안 막는다(아래) |
| `journal_mode(WAL)` | 건다 | **안 건다** | `journal_mode` 설정은 파일을 바꿀 수 있는 유일한 pragma 다. 이미 WAL 인 파일은 되읽기가 그대로 `wal` 을 내므로 `verifyPragmas` 는 통과한다 |
| `busy_timeout(5000)` · `foreign_keys(1)` | 건다 | 그대로 | `verifyPragmas` 를 재사용하려면 필요하다 |

`verifyPragmas` 는 그대로 돌린다 — 드라이버가 모르는 pragma 이름을 조용히 무시하므로
"DSN 에 적었다"는 "걸렸다"의 근거가 못 된다는 기존 판단이 백업에도 그대로 적용된다.

**`mode=ro` 를 쓰지 않는 이유**: 서버가 죽은 채 `-wal` 이 남아 있으면 읽기 전용 연결은 WAL 복구를
못 해 열기 자체가 실패한다. 백업은 정확히 그 상황에서 돌아야 한다. "이행이 필요하면 거절"은
`mode=ro` 보다 강하다 — 애초에 바꿀 수 있는 상태로 안 들어간다.

실측: 지금 `schema_version=4 == SchemaVersion=4` 라 이 거절은 오늘 발동하지 않는다.
코드가 5로 오르는 날 발동하고, 그날이 바로 위험한 날이다.

### 일관 스냅숏

세 표를 **트랜잭션 하나 안에서** 읽는다. 지금 `ExportLegacy` 는 세 질의를 트랜잭션 밖에서 돌아,
서버가 도는 중이면 표 사이에 커밋이 섞여 **링크가 가리키는 판단이 없는 산출물**이 원리적으로
가능하다. 무손실 등급에서 그것은 이미 깨진 백업이다.

**`Store.Tx` 를 재사용하지 않는다.** 기본 DSN 에 `_txlock=immediate` 가 걸려 있어 그 함수는
BEGIN IMMEDIATE 이고, 주석이 대가를 명시해 뒀다 — *"읽기 전용 작업도 이 함수를 거치면 쓰기 잠금을
잡아 서로 직렬화된다. 그래서 읽기는 Tx 를 안 거치고 Store 의 조회 메서드가 `s.db` 로 바로
질의한다."* 백업이 984행·1.78MB 를 읽는 동안 서버의 모든 쓰기가 대기하게 된다.

대신 백업은 자기 커넥션을 **`_txlock=deferred`** 로 열고 `BeginTx` 로 읽기 스냅숏을 잡는다.
어차피 §4 의 열기가 `store.Open` 을 안 쓰므로 DSN 을 자기가 정한다. WAL 에서 BEGIN DEFERRED 의
첫 SELECT 가 읽기 스냅숏을 잡고 트랜잭션 내내 그것이 유지되며, 서버의 쓰기를 막지 않는다.

deferred 의 알려진 위험(읽기 스냅숏 뒤 쓰기 승격이 `SQLITE_BUSY` 로 즉시 실패하고 `busy_timeout`
이 안 듣는다)은 **백업이 쓰기를 하지 않으므로 원리적으로 없다.** 승격 자체가 발생할 자리가 없다.

`VACUUM INTO` 사본에서 뽑는 안은 기각한다 — 18MB 임시 파일이 매번 뜨고 언제 지울지가 또 결정이며,
`~/.flightdeck` 에 이미 정리 안 된 백업이 38MB 쌓여 있다. 산출물이 2~3MB 인 것에 비해 과하다.

### 쓰기

`outbox.go` 선례대로 **tmp→rename**. 프로세스마다 다른 tmp 이름을 쓰고 실패 시 tmp 를 지운다.
`legacy/export.go` 의 `os.WriteFile` 직접 쓰기는 중간 실패 시 반쪽 파일을 남긴다 —
판단 자산에는 맞지 않는다.

---

## 5. 산출물

`--out` 디렉토리 아래 파일 넷. 이미 있으면 덮어쓴다(③에서 git 이 이력을 갖는다).

| 파일 | 내용 | 정렬 |
|---|---|---|
| `judgments.jsonl` | `judgment` 전량 | `ORDER BY id` — ULID 라 생성순이고 안정 |
| `judgment_links.jsonl` | `judgment_link` 전량 | `ORDER BY judgment_id, target_kind, target_id` |
| `snapshots.jsonl` | `snapshot` 전량 | `ORDER BY project, key` |
| `manifest.json` | 형식·스키마 버전·시각·건수 | — |

### 결정적 출력

**같은 DB 를 두 번 내보내면 같은 바이트가 난다.** ③에서 매시간 git 커밋할 때 "안 바뀌었으면 커밋이
없다"가 성립해야 하고, 그게 안 되면 자동 백업이 매시간 무의미한 커밋을 쌓는다. 정렬을 못박는
이유가 이것이다. `SearchJudgments` 는 `ORDER BY rank` 라 이 경로에 애초에 부적합하다.

### 표별 분리

표 구조를 그대로 옮겨야 되읽기 순서가 명시적이고, link 1,413행이 독립적으로 diff 된다.
링크를 judgment 줄에 중첩하면 `fillLinks` 의 N+1 을 984회 감수하거나 묶음 조회를 새로 만들어야
하는데, 별도 파일이면 링크 표를 한 번에 읽고 끝이다.

### 필드 표기 — raw DTO, `model` 경유 금지

- **snake_case** 로 DB 컬럼 이름 그대로.
- 시각은 **DB 원문 문자열 그대로**(`store.go` 의 `timeLayout`, 폭 고정 마이크로초).
  Go 의 `time.Time` JSON 마셜은 후행 0을 지워 폭이 흔들리고, 그러면 사전순 정렬이 시간순과
  어긋나며 DB 원문과 글자 단위 대조도 안 된다.
- NULL 은 **JSON `null`**. `sql.NullString` 을 그대로 받는다.

`model.Judgment` 를 안 거치는 이유: `nullStr`/`str` 이 NULL↔`""` 를 접는다. 지금 데이터에서는 그
접힘이 왕복으로 닫히지만, 닫힌다는 보장은 "빈 문자열이 저장된 행이 원리적으로 없다"는 별도 논증에
의존한다. raw DTO 로 가면 그 논증 자체가 필요 없다.

`model` 에 json 태그를 붙이는 안은 기각한다 — `finish`·`pick`·`board` 응답의 판단 객체 키가
동시에 바뀐다(지금은 Go 대문자 이름이 그대로 나가고 있다).

### `manifest.json`

```json
{
  "format": "fd-judgment-backup",
  "format_version": 1,
  "schema_version": 4,
  "exported_at": "2026-08-06T00:00:00.000000Z",
  "counts": {"judgments": 984, "judgment_links": 1413, "snapshots": 12}
}
```

`schema_version` 이 무손실의 안전핀이다. 스키마가 5로 오른 뒤 4로 뜬 백업을 되읽으면 조용히
깨지는데, 매니페스트가 있으면 거절할 수 있다. `format` 은 출력 자리 판정(§3)이 자기 산출물을
알아보는 데도 쓰인다.

### 안 담는 것

`judgment_fts` 와 그림자 표 넷. `judgment_fts_ins` 가 AFTER INSERT 트리거라 되읽기 때 자동으로
다시 채워진다 — **손실 0이다.** `rowid` 도 안 담는다. 복원 후 rowid 는 원본과 달라지지만
안정 식별자는 `judgment.id` 뿐이고 FTS 조인은 트리거가 같은 rowid 로 맞춘다.

---

## 6. 코드 배치

```
internal/store/backup.go     (새) ProbeMigration 게이트 + 마이그레이션 없는 deferred 열기
                                  + 읽기 스냅숏 Tx + 전량 조회 3개 + raw DTO 3개 + 되쓰기
internal/ledger/export.go    (새) JSONL 인코딩 · manifest
internal/ledger/write.go     (새) tmp→rename 쓰기
internal/ledger/read.go      (새) 되읽기
internal/ledger/losses.go    (새) 안 덮는 것 목록 — 순수 함수
internal/ledger/outguard.go  (새) IsOurOutput — manifest 를 알아본다
internal/store/judgment.go   snapshotCols + ListSnapshots 순삽입
internal/web/query.go        snapshots 삭제 (store 로 올린다)
internal/web/page.go         위 호출부 한 줄
cmd/fd/migrate.go            runExport 에 --judgments 분기 (--to-legacy 분기 무변경)
cmd/fd/main.go               usage 한 줄
plugins/flightdeck/DESIGN.md §7 두 곳 + §9 한 줄
```

**패키지 이름이 `backup` 이 아니라 `ledger` 인 이유**: 이 저장소에서 `backup`·`BackupSuffix`·
`<db>.bak-*` 는 이미 마이그레이션 직전 `VACUUM INTO` DB 파일 사본을 뜻한다. 두 개념이 같은
낱말을 쓰면 오류 문구와 로그에서 섞인다. `journal` 도 안 쓴다 — `journal_mode` 와 grep 이 겹친다.

`internal/ledger/` 는 `internal/legacy/` 와 대칭이다 — 인코딩·파일 쓰기는 `store` 의 일이 아니다.
출력 자리 가드는 `cmd/fd` 층에서 기존 `legacy.InspectOutTarget`/`JudgeOutTarget`/`ForceAllows` 를
그대로 부르고 `ledger.IsOurOutput` 만 더한다. 그래야 `ledger → legacy` import 도, 같은 SQL 두 벌도
안 생긴다(같은 판정을 두 자리에 두는 것은 `cmds.go` 가 금지한 형태다).

### 전량 조회를 `store` 에 두는 이유

`Store.DB()` raw SQL 은 "시험과 진단 전용" 주석과 어긋나고(`internal/web` 이 이미 다섯 번 어겼다),
`limit=100000` 흉내는 `legacy/export.go` 가 스스로 위험이라 적은 형태다
("상한에 걸려 조용히 잘리면 되쓴 트리가 원본보다 적어지고, 그 차이는 세어 보기 전에는 안 보인다").
`snapshot` 은 `store` 에 나열 함수가 아예 없다 — 유일한 나열 질의가 `internal/web/query.go` 의
unexported `snapshots` 다. `store` 에 만들고 `web` 이 그것을 쓰게 고친다.

### `restore.go` 를 만드는 이유

무손실 등급을 시험이 실제로 증명하려면 되읽는 코드가 있어야 한다. 시험 파일 안에 두면 "생산 코드에
없는 경로를 시험이 발명"하는 형태가 되고, 그 경로는 검증된 적 없는 채로 남는다. 함수로 두면
후속에서 `fd import --judgments` 를 배선만 하면 된다.

되읽기 정책:
- **빈 표 전제.** `judgment` 는 `judgment_no_update`·`judgment_no_delete` 트리거로 UPDATE·DELETE
  가 물리적으로 금지돼 있어, 잘못 넣은 행을 고치거나 지울 수 없다. 중복 id 는 거절한다.
- FK 폐포 때문에 `project`·`session` 행이 먼저 있어야 한다. `supersedes` 자기참조가 실제로 16행
  있으므로 `PRAGMA defer_foreign_keys = ON`(`store/move.go` 선례)을 쓰거나 삽입 순서를 보장한다.
- **이번 범위는 `judgment`·`judgment_link`·`snapshot` 셋만 복원한다.** `project`·`session`·
  `machine` 은 백업 대상이 아니므로, 되읽기는 그 셋이 이미 있는 DB 를 전제한다. 이 제약을 손실
  목록에 적고 시험이 문다.

### ask 재선언

내가 낸 ask 판단(`01KZA2DX…`)은 "`store/judgment.go` 파일 끝에 순삽입"이라고 선언했다.
새 파일 `store/backup.go` 로 바꾼다 — 기존 17함수 무변경이라는 정신은 같지만 파일이 늘었으므로
`note(kind:"ask", supersedes:…)` 로 다시 알린다. `internal/web/query.go` 도 한 줄 고치므로
그것도 함께 낸다.

---

## 7. 문서(①)

### §7 실패·처방 표 — 볼륨 손상 행

마이그레이션 행이 받은 것과 **같은 6단 형식**을 붙인다: 표 행 꼬리의 볼드 인라인 포인터 →
`**⚠ …(2026-08-06 실측).**` 두 줄 문단 → `| 처방 | 상태 | 실제 |` 3열 표 → 유지 판단 →
만료 조건 → 만료를 지키는 것.

그 행이 약속한 처방이 넷인데 이번에 생기는 건 하나뿐이라, 표가 그걸 정확히 낸다:

| 처방 | 상태 | 실제 |
|---|---|---|
| 판단을 DB 밖으로 내보내기 | 있음 | `fd export --judgments` |
| 매시간 자동 실행 | **없음** | 주기 작업 자리가 없다 — 티커는 SSE 하트비트·`selfwatch` 둘뿐, 컨테이너에 cron 없음, `compose.yaml` 은 서비스 하나 |
| DB 와 다른 볼륨 | **없음** | `compose.yaml` 이 "Tier A 의 한계"로 접어 뒀다 |
| 6시간 `VACUUM INTO` | **없음** | `VACUUM INTO` 는 마이그레이션 직전 1회다(`Store.backup`). 정기가 아니다 |

**만료 조건과 그것을 지키는 것.** 마이그레이션 각주는 만료를 `TestBundledMigrationsAreAdditive`
라는 시험이 지킨다. 백업 축에서 "매시간"은 시험이 못 잡는다 — 그래서 그 자리에 **followup 항목
id 를 적는다.** fd 큐가 만료 조건의 보관소다(§11 "떼어낼 조각부터"와 같은 문법).

`grep journal → 0건` 이 이제 9건을 내며 전부 `journal_mode` 라는 것도 각주에 한 줄 적는다.

### §7 판단 백업 문단

세 곳을 고친다.

1. 대상이 `judgment`+`snapshot` 인데 **무손실 복원의 최소 폐포는 그것으로 안 닫힌다.**
   `judgment.project → project`, `judgment.session_id → session`, `session → machine`,
   `snapshot.project → project`, `judgment.supersedes → judgment` 가 전부 FK 이고
   `foreign_keys=1` 이 켜져 있다. 이번 백업은 `judgment`·`judgment_link`·`snapshot` 셋을 담고,
   **`project`·`session`·`machine` 이 있는 DB 를 복원 전제로 삼는다.** 그 전제를 문단에 적는다.
2. "마크다운/JSONL" 을 JSONL 하나로 좁힌다.
3. **아웃박스 구멍 한 줄** — 이 백업은 DB 만 덮으므로 아웃박스에 갇힌 판단은 안 잡힌다(실측 1건).

`/data/journal.git` 배치도(§2)는 **안 만진다.** 그 모순은 `compose.yaml` 이 이미 "백업 잡이 생기는
시점에 별도 볼륨으로 가른다"로 접어 뒀고, 그 시점이 ③이다. ①②만 하는 동안 그 주석은 여전히 참이다.

### §9

export 서술 옆에 `fd export --judgments` 한 줄. §6 의 CLI 목록은 `export` 가 이미 있으므로
**안 고친다.**

---

## 8. 시험

**빨간불 먼저.** 판정은 순수 함수로, 단정은 소비자 좌표계로(§12).

| 시험 | 무엇을 잠그나 |
|---|---|
| **왕복 무손실** — 원본 DB → JSONL → 빈 DB → 표 단위 대조 | 사용자가 고른 등급을 증명하는 **유일한** 시험 |
| 결정성 — 같은 DB 를 두 번 내보내면 같은 바이트 | ③의 git 커밋 전제 |
| 74,227B 한 줄 | `bufio.Scanner` 기본 상한 64KB 초과. 지금 데이터로 재현되는 값이라 픽스처에 실물 크기를 넣는다. 읽는 쪽은 `outbox.readEntries` 처럼 버퍼를 키운다 |
| 원본 불변 | `migrate_test.go` 의 Size+ModTime 비교와 WAL/`-shm` 부재 확인을 그대로 재사용 |
| 이행 필요 시 거절 | `PlanMigration` 이 이행을 요구하면 백업이 안 돈다 |
| NULL ↔ `null` | `project`·`session_id`·`title`·`supersedes` 넷 |
| tmp→rename 중간 실패 | 반쪽 파일이 안 남는다 |
| 손실 목록 | 열거한 것만 잃는다 (`roundtrip_test.go` 선례) |
| 배선 | `--out` 없으면 거절 · `--to-legacy` 와 배타 · `--project` 명시는 거절하되 `FD_PROJECT` 는 무시 |
| `IsOurOutput` | 두 번째 실행이 `--force` 없이 돈다 · 남의 디렉토리는 `--force` 요구 · git 트리는 거절 |

하네스는 `cmd/fd/harness_test.go` 의 `h.run`(같은 프로세스, 종료코드+stdout)을 쓴다.
**stderr 는 하네스가 안 돌려주므로** 플래그 파싱 오류 문구는 단정할 수 없다 — 사람이 읽는 줄은
전부 `out` 으로 낸다는 기존 규약을 지킨다.
export/import 시험은 `h.closeStore()` 로 하네스 DB 핸들을 먼저 닫고 돌린 뒤 다시 여는 규약을
따른다(같은 파일을 두 커넥션이 여는 상황을 안 만든다).

교차 빌드 관문은 `go build` 가 아니라 **`go vet`** 로 돈다 — `go build` 는 `_test.go` 를 건너뛴다.

---

## 9. 안 하는 것 (followups)

| 후속 | 왜 지금 안 하나 |
|---|---|
| ③ 매시간 · bare 레포 · 별도 볼륨 | 주기 작업 자리가 코드에 없다(선례 0건). serve 티커 / 호스트 cron / compose 두 번째 서비스 중 무엇을 세울지가 독립 결정이고, `compose.yaml` 이 볼륨 분리를 이 시점에 걸어 뒀다 |
| 아웃박스 합류 | `rejected.jsonl` 1건이 실재하지만, 넣으면 상태 디렉토리 갈림과 시각 표기 차이가 이 명령의 문제가 된다. 별도 축이다 |
| `fd import --judgments` CLI 표면 | 함수는 있고 배선만 없다 |
| `project`·`session`·`machine` 백업 | 무손실 복원의 FK 폐포에 필요하지만 §5 는 이것들을 파생 불가로 안 꼽았다. 폐포를 닫을지는 별도 판단 |
| `legacy/outguard.go` 의 범용 판정이 `legacy` 패키지에 사는 것 | 이름과 자리가 안 맞지만 이번에 옮기면 남의 자리를 건드린다 |

---

## 10. 반증

이 설계가 과잉이라면 근거는 하나다 — **"DB 파일 손상이 실제로 안 난다".**
그렇다면 §7 의 `볼륨·DB 손상` 행 자체를 지워야지, 처방만 남겨 두면 안 된다.

지금 반대 방향의 실측이 있다: `~/.flightdeck` 에 사람이 손으로 만든 사본(`.before-purge-aaron`)이
있고, 항목이 08-04 에 관측한 `.before-import`·`.before-move`·`.pre-redo` 셋은 지금 없다 —
손으로 지웠다는 뜻이다. **백업 자산이 사람 손으로 관리되고 있다.**
