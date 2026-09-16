# 항목을 제자리에서 고치는 표면 — 설계

- 항목: `fd-item-amend-surface`
- 날짜: 2026-09-16
- 여는 판정: DESIGN §11 「항목 본문(`title`·`body`) 수정 — 일반 amend」
- 선례: `2026-08-12-item-label-surface-design.md` (같은 §11 표의 옆 행을 연 설계)

---

## 1. 무엇이 없었나 — 그리고 그 부재는 결정이었다

`item` 표의 `title`·`body`·`paths` 를 고치는 경로가 REST·MCP·CLI 어디에도 없다. 쓰는 자리는
`AddItem` 의 INSERT 하나뿐이고, 살아 있는 `UPDATE item SET` 은 전부 `state`·`close_reason`·
`closed_at`·`landed_ref`·`project`·`labels` 계열이다.

이 부재는 빈자리가 아니라 **판정**이었다. `service/move.go` 가 `move` 의 범위를 프로젝트 한
축으로 못박으며 그 이유를 적었다 — 일반 amend 로 번지면 "무엇을 고칠 수 있나"가 표면마다
달라지고 그 차이를 아무도 못 따라간다. §11 이 그 판단을 문서로 올렸고,
`store/item_body_immutable_test.go` 가 **부재를 주장하는 문장**을 관문으로 지켰다.

같은 §11 줄이 출구도 함께 적어 뒀다:

> **다만 영구 결정으로 못박지 않는다** — 열 때 물을 것은 "amend 를 만들까"가 아니라
> **"열린 항목의 틀린 전제를 다음 `pick` 이 보게 하려면 무엇이 필요한가"** 다.

이 문서는 그 질문에 답한다.

---

## 2. 여는 근거 — 정정 경로가 새는 자리 넷

지금의 정정 수단은 `note(item_id=…)` 를 얹는 것이고, `pick` 이 선점·재개 때 그것을 전문으로
낸다. 사용자 실사용에서 그 경로가 네 자리에서 샌다. 넷 다 같은 빈도로 부딪힌다고 보고됐다.

| 새는 자리 | 무엇이 일어나나 |
|---|---|
| ⓐ 원문이 정본처럼 읽힌다 | 정정을 얹어도 다음 세션은 항목 본문을 먼저 읽고 틀린 전제대로 움직인다. 두 글이 나란히 오면 어느 쪽이 살아 있는지를 **읽는 쪽이** 판정해야 한다 |
| ⓑ 제목만 나는 화면에 안 보인다 | 추천 화면·보드·대시보드는 제목만 낸다. 제목 자체가 틀렸을 때 정정이 닿을 자리가 없다 |
| ⓒ 닫힌 것에 안 닿는다 | 큐가 `state='open'` 이라 닫힌 항목에 얹은 정정은 읽힐 경로가 없다 |
| ⓓ 새 행을 쌓는 것이 과하다 | 오타·형식·리네임 추종까지 판단 한 건을 쌓게 되어 원장이 정정 잡음으로 불어난다 |

**리네임 추종이 실제 빈도의 상당분이다.** 문서나 디렉토리 이름이 바뀌어 본문 안의 좌표가
낡거나, 내용은 그대로인데 이름만 바뀐 경우다. 이 부류는 ⓓ 의 전형이다 — 판단으로 남길 값이
없는데 판단밖에 수단이 없다.

**이 설계가 푸는 것은 ⓐⓑⓓ 다.** ⓒ 는 절반만 푼다(§10 참조).

---

## 3. 축 — 셋으로 못박는다

고칠 수 있는 것은 `item` 의 **`title` · `body` · `paths`** 다. `label` 이 축 하나로 못박힌 것과
같은 좁기이고, 넓히는 것은 이 문서의 범위가 아니다.

| 축 | 연다 | 왜 |
|---|---|---|
| `title` | ✅ | ⓑ 의 유일한 해법. 제목이 화면에 나는 유일한 창이다 |
| `body` | ✅ | ⓐ 의 해법. `pick` 이 정본처럼 내는 글이 곧 이것이다 |
| `paths` | ✅ | 리네임 추종이 여기서 끝난다. 다만 **겹침 축을 움직인다** — §7 |
| `labels` | ❌ | `label` 이 이미 있다. 두 동사가 같은 컬럼을 물면 §11 이 경고한 그 분기가 생긴다 |
| `item_after`(선행) | ❌ | §11 의 별도 행이고 그 판정은 안 건드린다 |
| `state`·`close_reason` | ❌ | 상태 전이는 `finish`·`claim` 의 몫이다. 폐기 사유는 CHECK 가 붙들고 있다 |
| `judgment.body` | ❌ | 트리거가 물리적으로 막는다. §9 |

**닫힌 항목도 고칠 수 있다.** `note` 가 닫힌 것에 안 닿는 이유는 `pick` 이 열린 것만 주기
때문인데, `amend` 는 `item` 표를 직접 무므로 그 제약이 없다. 랜딩된 항목도 같다 — 본문은
기록이지 실행이 아니고, `landed_ref` 는 이 동사가 안 건드린다.

---

## 4. 저장 — 옛 값은 전용 표에 전문으로 남는다

증분 `016_item_revision.sql`. 표 하나 + 트리거 둘. 파괴적 조작이 없으므로
`migrate_guard_test.go` 의 `destructiveExempt` 등록은 필요 없다.

```sql
CREATE TABLE item_revision (
  project    TEXT NOT NULL,
  item_id    TEXT NOT NULL,
  rev        INTEGER NOT NULL,   -- 1부터
  at         TEXT NOT NULL,
  session_id TEXT REFERENCES session(id),
  title      TEXT NOT NULL,      -- ★ 고치기 **직전**의 값
  body       TEXT NOT NULL,
  paths      TEXT NOT NULL,
  reason     TEXT NOT NULL CHECK (reason <> ''),
  PRIMARY KEY (project, item_id, rev),
  FOREIGN KEY (project, item_id) REFERENCES item(project, id)
);
```

### 이 행은 새 값이 아니라 옛 값을 담는다

현재 값은 `item` 한 곳에만 있다. 그래서 두 표가 어긋날 자리가 **원리적으로** 없고,
`rev` 를 역순으로 이으면 원문까지 복원된다.

반대로 새 값을 쌓으면 "현재"가 두 곳에 생긴다. 그 부류의 조용한 불일치가 이 저장소에서
이미 한 번 값을 치렀다 — `judgment_link.target_project` 가 없던 동안 링크 행은 들어가고
응답은 성공인데 수신자에게는 영영 안 보였다(DESIGN §3, 죽은 링크 12행).

### 추가 전용 — `judgment` 와 같은 문구로 잠근다

```sql
CREATE TRIGGER item_revision_no_update BEFORE UPDATE ON item_revision
BEGIN SELECT RAISE(ABORT, 'item_revision 은 추가 전용이다 — 개정은 새 rev 로 쌓아라'); END;

CREATE TRIGGER item_revision_no_delete BEFORE DELETE ON item_revision
BEGIN SELECT RAISE(ABORT, 'item_revision 은 추가 전용이다 — 지우면 복구 경로가 0이 된다'); END;
```

이력을 고칠 수 있으면 이력이 아니다. 본문을 제자리에서 고치는 값을 치르는 대신, 그 값이
사라지지 않는다는 보장을 여기서 산다.

### `reason` 은 필수다

한 구절이면 된다. 근거는 이 스키마가 같은 결을 이미 두 번 썼다는 것이다 —
`item` 의 `CHECK (state <> 'dropped' OR close_reason …)`, `snapshot` 의
`CHECK (method <> 'manual' OR evidence …)`. 사유 없는 수정은 나중에 되짚을 수 없고,
되짚을 사람이 `item_revision` 을 여는 유일한 이유가 그것이다.

### `rev` 의 경합

`MAX(rev)+1` 을 쓰기 트랜잭션 안에서 뽑는다. `_txlock=immediate` 가 두 세션의 동시
`amend` 사이를 닫는다 — `RemoveProject` 가 재-셈과 삭제 사이를 닫는 것과 같은 수단이다.
PK `(project, item_id, rev)` 가 그래도 새면 그때는 조용한 덮어쓰기가 아니라 제약 위반으로
터진다.

---

## 5. 표면 — 전용 동사 하나

### REST (정본)

```
POST /api/v1/items/{id}/amend
{"title"?: string, "body"?: string, "paths"?: []string, "reason": string, "item_project"?: string}
```

- `title`·`body`·`paths` 는 **준 것만 고친다.** 생략과 "빈 값으로 바꿔라"가 갈리므로 포인터로
  받는다. 셋 다 안 주면 거절한다.
- 응답은 요청한 것이 아니라 **실제 변화분**을 낸다. 같은 값을 다시 써도 거절하지 않지만
  "고쳤다"고만 말하면 안 바뀐 것을 바뀐 줄 안다. `label` 이 같은 이유로 같은 것을 한다.
- 새 `rev` 번호를 응답에 싣는다 — 되돌릴 좌표가 그 자리에서 나와야 한다.

### MCP — 9번째 도구

```
Name:        "amend"
Description: "항목의 제목·본문·경로를 고친다. 옛 값은 개정 이력에 남는다."   (35 runes / 상한 90)
InputSchema: item_id(필수) · title? · body? · paths? · reason(필수) · project?
```

도구 하나가 느는 값은 `label` 때와 같은 계산이다 — 세션 시작에 실리는 고정비는 **이름
하나**뿐이고, 규율은 전부 응답(`RenderAmend`)으로 간다. 도구 설명에는 규율 산문을 안 넣는다.

### CLI

```
fd amend --item <id> --title … --body … --path … --reason … [--item-project …]
```

`--body` 는 기존 `bodyFlagHelp` 의 stdin 규율(`-` 이면 stdin)을 그대로 쓴다. `--path` 는
`add` 와 같이 반복 지정이고, **한 번이라도 주면 목록 전체를 그것으로 바꾼다**(부분 추가·삭제가
아니다 — `label` 의 add/rm 과 다른 모양인 이유는 경로가 집합이 아니라 선언이기 때문이다).

`fd` 명령 목록이 `… add amend finish …` 로 늘어난다.

### 함께 닫는 비대칭 — `fd note --supersedes`

`supersedes` 는 REST 와 MCP `note` 에는 있는데 CLI `fd note` 에는 플래그가 없다. 플래그 하나를
더한다. 이 비대칭은 `--item-project` 가 없어서 "거절이 막힌 길을 가리켰던" 것과 같은 모양이다.

### 오프라인은 거절한다

`JudgeOffline` 표에 `amend` → `OfflineRefuse` 를 사유와 함께 등록한다.

`note` 와 다르다. `amend` 는 읽고-고치는 쓰기라, 아웃박스가 재생하는 시점의 현재 값이 쌓을
때와 다르면 `item_revision` 이 **거짓 이전값**을 담는다. 옛 값을 지키려고 만든 표가 거짓을
담는 것이 이 기능의 최악 실패다. §11 이 "선점의 오프라인 재생"을 안 만든 것과 같은 결이다 —
재생 시 충돌 판정이 필요한 것에 재생 기구를 만들지 않는다.

### 멱등 키

내용 해시로 안정화한다. 같은 세션이 같은 수정을 두 번 보내면 재생으로 접혀 `rev` 가 헛돌지
않는다. 대가는 **거절당한 뒤 같은 본문으로 재시도하면 같은 거절이 재생되는 것**이고, 응답이
그 자리에서 그렇게 말한다(관측된 함정이다 — `finish` 관문에서 같은 일이 있었다).

---

## 6. 관문 전환 — 부재 그물을 유일 작성자 그물로

`store/item_body_immutable_test.go` 는 **부재를 주장하는 문장**을 지키는 그물이다. 부재를 여는
순간 그 그물은 목적을 잃는다. **지우면 안 된다** — 지우면 §11 이 경고한 "표면마다 무엇을
고칠 수 있나가 갈리는 것"을 막을 것이 하나도 안 남는다.

| 지금 | 바꾼 뒤 |
|---|---|
| `title`·`body` 를 무는 `UPDATE item` 은 **0건** | 그 UPDATE 는 **정확히 `store/amend.go` 한 곳** |
| `paths` 는 안 문다 | `paths` 도 문다 — 이제 수정 축이다 |
| DESIGN 앵커 = "전수로 없다" | DESIGN 앵커 = "한 곳에서만 고친다" |

새 단정 하나를 더한다: **그 UPDATE 와 `INSERT INTO item_revision` 이 같은 함수 안에 있어야
한다.** `go/ast` 로 잰다. 이력 없는 수정 경로가 나중에 조용히 생기는 것이 §4 의 값을 통째로
무효로 만드는 유일한 길이다.

파일 이름을 `item_body_single_writer_test.go` 로 옮긴다(`git mv`). 이름이 `immutable` 인 채로
두면 다음 사람이 파일 이름만 보고 반대 사실을 믿는다 — 이 파일의 주석이 스스로 경고한
"코드가 바뀌어도 안 깨지는 문장"의 한 종류다.

기존 그물의 자기 점검 셋(`scanned == 0` · `inserts == 0` · `updates == 0`)은 그대로 산다.
좌표가 밀리면 0건이 "깨끗하다"가 아니라 "아무것도 안 봤다"인데 둘은 화면에서 구분되지 않는다.

---

## 7. 파급 — `paths` 는 남의 화면을 움직인다

세 축 중 유일하게 다른 세션에 보이는 것이 `paths` 다. 고치면 `board`·`pick` 의 경로 겹침
판정이 즉시 다른 답을 낸다.

대응은 `label` 이 tickler 규율을 응답에 싣는 것과 같다 — **`amend` 응답이 "이 수정으로 겹치게
된 세션 N" 을 그 자리에서 낸다.** 고친 사람이 그 사실을 알 다른 경로가 없기 때문이다.

세지 못하면 숫자 대신 **세지 못했다는 사실**을 낸다. 0 과 못 잼을 가른다 — `finish` 가 커밋
뒤 보조 조회 실패를 다루는 방식과 같다.

---

## 8. 낡는 문장들 — 같은 커밋에서 고친다

| 자리 | 지금 | 바꾼 뒤 |
|---|---|---|
| DESIGN §11 「항목 본문 수정」 행 | "안 만든다" | `label` 행과 같은 꼴로 취소선 + **"열었다 (2026-09-16)"** + 근거 + **무엇은 여전히 안 여는가** |
| DESIGN §3 **제목 줄** | "SQLite 파일 하나, 테이블 26개, 계층 셋" | 27개 + Q 계층에 `item_revision` 항목 |
| DESIGN §6 MCP 표 | 도구 8개 | 9개 + `amend` 행 |
| DESIGN §6 MCP `note` 행 | 인자 `kind, body, item_id?` | `supersedes` 추가 — **코드에 이미 있는데 표에 없었다** |
| DESIGN §6 CLI 목록 | `… add finish …` | `… add amend finish …` |
| `mcpsrv/protocol_test.go` | `TestToolTableIsEight` | `TestToolTableIsNine` (이름과 목록 둘 다 하드코딩이다) |
| `store/backup.go` | 표 목록에 `item_revision` 없음 | 추가 — 빠뜨리면 기기 이동 때 개정 이력만 조용히 사라진다 |
| `store/schema_table_count_test.go` | `want` 목록 26개 | 27개 — **정렬 순서대로** 넣는다(`item` 과 `item_after` 사이). 이름 목록을 통째로 못박는 시험이라 수만 맞추면 안 된다 |

**§6 `note` 행은 이 작업과 무관하게 이미 낡아 있었다.** `land` 구가 2026-08-12 에 코드로만
들어가고 문서에 안 온 것과 같은 모양이라, 발견한 김에 함께 맞춘다.

---

## 9. 판단(J 계층)은 안 건드린다

`judgment` 는 `BEFORE UPDATE`·`BEFORE DELETE` 트리거가 `RAISE(ABORT)` 한다. 그 근거는
**남의 절을 덮어써 원문이 영구 소실된 사고 2회**다. 이 설계는 그 트리거에 한 글자도 안 쓴다.

판단 쪽에서 이 작업이 하는 것은 CLI 플래그 하나(`--supersedes`)뿐이다. 고칠 수단은 이미
있었고 한 표면에서만 못 불렀다.

---

## 10. 범위 밖 — 그리고 그것이 남기는 빚

**읽기 도달률은 이 스펙이 안 푼다.** §2 의 ⓒ 가 여기 남는다:

- 닫힌 항목에 걸린 판단을 읽을 경로 (큐가 `state='open'` 이라 `pick` 이 안 준다)
- `pick` 이 내는 판단 전문에서 정정된 행을 접고 정정본을 먼저 내는 것
  (`service/board.go` 는 이미 정정당한 행을 빼지만 `pick` 경로는 별개다)

쓰기 표면과 읽기 도달률은 다른 문제이고, 한 스펙에 끼우면 둘 다 흐려진다. `finish` 의
`followups` 로 등록한다.

그 밖에 안 하는 것: 축 넓히기(§3 의 ❌ 행 전부) · 웹 UI 의 수정 버튼 · 되돌리기(`revert`) 동사.
되돌리기는 `item_revision` 이 있으면 나중에 값싸게 얹을 수 있고, 지금 만들면 쓰이지 않는
동사가 하나 더 는다.

---

## 11. 검증 좌표

### 단위 시험

| 무엇 | 어디 |
|---|---|
| 준 것만 고친다 — 생략된 축은 안 변한다 | `store/amend_test.go` |
| 옛 값이 `rev` 에 **직전 값으로** 들어간다 | `store/amend_test.go` |
| 두 번 고치면 `rev` 가 1·2 로 쌓이고 역순 복원이 원문을 낸다 | `store/amend_test.go` |
| `item_revision` UPDATE·DELETE 가 ABORT 한다 | `store/constraint_test.go` — 실제로 넣어 보고 잰다. `store_test.go` 는 스키마 **문자열**에 `CREATE TRIGGER judgment_no_update` 가 있는지만 보므로 그 축과 다르다 |
| `reason` 이 비면 거절한다 | `store/amend_test.go` · `api/input_refusal_test.go` |
| 셋 다 안 주면 거절한다 | `service/amend_test.go` |
| 닫힌 항목·랜딩된 항목도 고쳐진다 | `service/amend_test.go` |
| 응답이 **실제 변화분**을 낸다(같은 값 재지정은 변화 0) | `mcpsrv/render_amend_test.go` |
| `paths` 를 고치면 겹침 세션 수가 응답에 온다 / 못 세면 그 사실이 온다 | `mcpsrv/render_amend_test.go` |
| 오프라인에서 거절하고 아웃박스에 **안 쌓는다** | `cmd/fd/offline_test.go` |
| export → import 왕복에서 개정 이력이 보존된다 | `store/backup_test.go` |

단정은 소비자의 좌표계로 쓴다 — MCP 응답 문자열·CLI stdout 실물이다(§12 시험 규율).

### 관문

| 관문 | 무엇이 바뀌나 |
|---|---|
| `store/item_body_single_writer_test.go` | 부재 → 유일 작성자. 전환 자체가 이 작업의 산출물이다 |
| `mcpsrv/protocol_test.go` | 도구 9개 + 설명 90자 상한(그대로) |
| `store/schema_table_count_test.go` | 선언 표 27개 |
| `api/design_route_table_test.go` | 새 라우트가 표에 등록돼야 한다 |
| `store/migrate_guard_test.go` | 016 은 파괴적 조작 0 — 예외 등록 없이 통과해야 한다 |

### 변이 확인

새 관문은 **망가진 것을 넣어 빨간불을 먼저 본다.** 최소 둘:

1. `amend.go` **밖**에서 `UPDATE item SET title` 을 치는 파일을 임시로 넣는다 → 빨간
2. `amend.go` 안에서 `INSERT INTO item_revision` 을 지운다 → 빨간

둘 다 **어느 줄이 잡았는지**까지 본다. 옆 축이 잡으면 재려던 주장은 미검증이다.
