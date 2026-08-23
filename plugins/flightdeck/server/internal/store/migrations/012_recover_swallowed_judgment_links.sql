-- 012 · 인자 삼킴으로 죽은 판단↔항목 링크 21행을 되살린다 (schema_version 11 → 12)
--
-- 도구 호출 계층이 인자를 옆 인자에 통째로 삼켜 보낸 자국이 원장에 39건 있다
-- (mcpsrv/arg_swallow.go 머리말이 그 전수다). 그중 이 증분이 손대는 것은 **판단이 어느
-- 항목에도 안 걸린 21행**뿐이다. 나머지 18건에는 손대지 않으며, 왜 안 하는지가 아래 ★ 셋이다.
--
-- ★ 왜 지금 넣을 수 있나: **상류가 막혔고 신규 유입이 0이다.**
--   관문 a8c2786(0.28.0)이 callTool 에서 이 모양을 거절한다. 2026-08-23 04:3x 재측에서
--   조임 술어(swallowedBy 와 같은 술어)로 원장을 전수로 훑어 **여전히 39건**이었다 —
--   관문 랜딩 시점(2026-08-22)의 39건과 같은 수다. 대상 집합이 고정됐다는 뜻이고,
--   그래서 아래 21쌍을 값으로 못박을 수 있다.
--
-- ★ 무엇을 되살리나 — **이 머리말이 그 복구의 유일한 원장이다.**
--   되살린 뒤에는 어떤 질의도 "이 21행이 왜 여기 있는지"를 못 낸다. judgment_link 는
--   행 자체에 출처를 안 담기 때문이다.
--
--     item_id 가 삼켜진 판단 18건 → 링크 18행
--     followups 가 삼켜진 판단  1건 → 링크  3행 (그 JSON 안의 항목 셋)
--                                    ────
--                                     21행
--
--   전부 프로젝트 context-platform 이고, 판단의 project 와 대상 항목의 project 가 같다.
--   삼켜진 값은 판단 본문 꼬리에 **그대로** 남아 있어 추측이 한 글자도 안 들어갔다.
--
-- ★★ 왜 링크만인가 — **판단 제목 20건은 물리적으로 못 채운다.**
--   같은 사고로 judgment.title 20건이 비었고(2026-08-23 실측: 오염 20건 전부 Δ0.0s 의
--   판단 title 이 비어 있다, 대조군 0), 잃은 제목은 item.close_reason 꼬리에 온전히 있다.
--   그런데 judgment 에는 `judgment_no_update` 트리거가 걸려 있다 — RAISE(ABORT) 다.
--   **그 트리거는 "빈 자리를 채우는 것"과 "덮어쓰는 것"을 안 가른다.** 항목
--   fd-repair-39-rows-killed-by-arg-swallow 의 본문은 제목 채우기를 "덮어쓰기가 아니라
--   잃는 것이 없다"로 분류했는데, 실측하면 그 전제가 틀렸다. 링크는 별도 표에 **행을 더하는
--   것**이라 그 규율에 안 걸리고, 그래서 이 증분의 범위가 링크 하나로 좁혀졌다.
--
-- ★★ 그래서 item.close_reason 20건도 안 건드린다 — **그 꼬리가 잃은 제목의 유일한 사본이다.**
--   그것을 "마크업 정리"로 자르면 제목 20개가 영구 소실된다. judgment 는 추가 전용이라
--   복구 경로가 0이다. 이 증분이 close_reason 에 한 글자도 안 쓰는 것이 그 판정의 이행이다.
--   (설계 §J 계층에 같은 금지를 적었다. 셋 중 하나만 고치면 갈린다.)
--
-- ★ 2026-08-19 판정("이미 박힌 죽은 링크 12행은 안 고친다")과 어긋나지 않는다. 둘이 다르다.
--   ⒜ **조작이 다르다.** 저쪽은 target_project 를 채우는 UPDATE(opUpdateSet)라 "판단 표를
--      사후에 고쳐 쓰는 선례"가 되는 것이 치를 값이었다. 여기는 없는 행을 INSERT 하는 것이라
--      기존 행을 한 글자도 안 바꾼다 — 그 값을 안 치른다.
--   ⒝ **그 판정이 스스로 적어 둔 뒤집기 조건이 충족됐다.** 조건은 "죽은 링크의 대상이 열린
--      항목인 경우가 생길 때"였고, 이 21행의 대상 중 셋이 open 이다
--      (owner-audit-worm-blocks-e2e-reset ×2 · t7-audit-event-ledger-build ×1).
--      나머지 18행의 대상은 done 이라 저쪽 논리대로면 수신자가 0인데, INSERT 의 비용이
--      0에 가까워 가르는 값이 없다 — 가르면 done 이 다시 열릴 때 같은 판정을 또 해야 한다.
--
-- ★ 왜 되돌릴 수 있나(= TestBundledMigrationsAreAdditive 의 갈래 (b) 근거.
--   같은 근거를 migrate_guard_test.go 의 destructiveExempt[12] 에 적었다):
--     ⒜ **기존 행을 한 글자도 안 바꾼다.** 조작은 judgment_link 에 대한 INSERT 하나뿐이고
--        judgment·item·close_reason 은 읽기만 한다. 되돌리기는 이 21행의 DELETE 다
--        (PK 가 (judgment_id, target_kind, target_id) 라 정확히 지목된다).
--     ⒝ **넣는 값이 결정적이다.** 판단 본문 꼬리에서 읽은 항목 id 를 값으로 못박았고,
--        그 본문은 추가 전용이라 변하지 않는다 — 언제 다시 돌려도 같은 21행이다.
--     ⒞ 판올림 전에 PlanMigration 이 VACUUM INTO 백업을 뜨고, 깨지면
--        `fd migrate --rollback` 이 그 백업으로 되돌린다.
--
-- ★ 멱등이다. NOT EXISTS 가 이미 있는 링크를 거른다 — 두 번 돌려도 21행 그대로다.
--   신규 설치 DB 에서도 안전하다: judgment 조인이 0행을 내므로 아무것도 안 들어간다.
--   그 무해함이 이 증분을 조건부로 쓴 이유다. judgment_link.judgment_id 에는
--   `REFERENCES judgment(id)` 가 걸려 있어, 조건 없이 VALUES 로 밀어 넣으면 신규 DB 에서
--   FK 위반으로 **적용 자체가 실패한다.**
--
-- ★ target_project 는 **NULL 로 둔다.** 값을 넣어도 읽는 쪽이
--   COALESCE(target_project, judgment.project) 로 같은 값을 내지만(증분 009), 그 컬럼의
--   규약은 "채워지는 것은 **교차 프로젝트 링크뿐**"이다(model.JudgmentLink.TargetProject 의 ★).
--   이 21행은 판단과 대상이 **같은 프로젝트**라 코드가 만들었으면 NULL 이 들어갔을 자리다.
--   복구는 "원래 들어갔어야 할 모양"을 재현하는 것이지 새 모양을 만드는 것이 아니다 —
--   값을 넣으면 되살린 링크만 나머지 8009행과 다르게 생기고, 그 차이는 아무 의미도 안 싣는다.

WITH recovered(jid, iid) AS (
  VALUES
    -- item_id 가 삼켜진 판단 18건
    ('01KZQ4HYKP95V3WVJKV2P99GPZ', 'e2e-sa-owners-roundtrip'),
    ('01KZQ5DEZHVV6PN71SWJ1CFETE', 'e2e-sa-owners-roundtrip'),
    ('01KZQ80G9QKZFZXWYQP4NMV1ZK', 'e2e-sa-owners-roundtrip'),
    ('01KZQ826YZNC3ERYCWK0XG4NVD', 'e2e-sa-owners-roundtrip'),
    ('01KZQ8KSK6T52QBNAE3ZBYE81R', 'e2e-sa-owner-last-effective-guard'),
    ('01KZQ8KZ3AX42YXBD51SKK4MCA', 'e2e-assert-no-firing-alerts'),
    ('01KZQ8MAWKBFZR63WHW49Q14RF', 'owner-audit-worm-blocks-e2e-reset'),
    ('01KZQ8XJP5NCTCBDD2VDYJ3Q66', 'e2e-sa-owner-last-effective-guard'),
    ('01KZQ9BP1Q5PP3G6RM6Y7RF2EK', 'e2e-assert-no-firing-alerts'),
    ('01KZQ9T63RZB3JKVGBTNV7ENGR', 'owner-audit-worm-blocks-e2e-reset'),
    ('01KZQ9TXDKSP0PHMM66FKRSP13', 't7-audit-event-ledger-build'),
    ('01KZQC2R08BZH3GT3TXZEJ121V', 'alerts-rules-without-positive-assertion'),
    ('01KZQC36Z435B7SXF8K0AQ17KQ', 'nodeport-allow-band-reruling'),
    ('01KZQFZ8MTQTS13XTM9JXTW9E0', 'e2e-assert-no-firing-alerts'),
    ('01M0496A14ZGF977DQ4G1Z06FK', 'remodel-console-selfserve'),
    ('01M04ADSZSFZ56XAT9NF4719NF', 'remodel-console-selfserve'),
    ('01M04AF78Z9B3M5JJWPBHFHBFZ', 'remodel-console-selfserve'),
    ('01M04CPTWYH1PVS6P4F5ZMR36F', 'remodel-console-selfserve'),
    -- followups 가 삼켜진 판단 1건 → 그 JSON 이 실었던 항목 셋
    ('01KZQ8HEWK5ZQBB0JRTG7BZVW1', 'e2e-sa-owner-last-effective-guard'),
    ('01KZQ8HEWK5ZQBB0JRTG7BZVW1', 'e2e-assert-no-firing-alerts'),
    ('01KZQ8HEWK5ZQBB0JRTG7BZVW1', 'owner-audit-worm-blocks-e2e-reset')
)
INSERT INTO judgment_link (judgment_id, target_kind, target_id, target_project)
SELECT r.jid, 'item', r.iid, NULL
  FROM recovered r
  JOIN judgment j ON j.id = r.jid
 WHERE EXISTS (SELECT 1 FROM item i WHERE i.id = r.iid AND i.project = j.project)
   AND NOT EXISTS (SELECT 1 FROM judgment_link l
                    WHERE l.judgment_id = r.jid
                      AND l.target_kind = 'item'
                      AND l.target_id = r.iid);
