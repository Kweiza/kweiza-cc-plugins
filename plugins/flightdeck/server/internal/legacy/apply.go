package legacy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kweiza/flightdeck/internal/model"
	"github.com/kweiza/flightdeck/internal/store"
)

// Applied 는 실제로 들어간 것의 수다. 계획의 수와 다르면 그 자체가 결함이다.
type Applied struct {
	Sessions  int
	Judgments int
	Items     int
	Snapshots int
}

// LegacyMachineID 는 이관이 만드는 기계 정체다.
//
// 레거시 카드에는 machine_id 가 없다. 세션 정체는 (machine_id, worktree, cc_session_id)
// 3중키인데(설계 §3), 그중 둘만 원본에 있으므로 나머지 하나를 **고정 상수**로 둔다.
// 지어낸 값을 머신마다 다르게 만들면 같은 카드를 두 번 넣었을 때 중복이 안 걸린다.
const LegacyMachineID = "legacy-import"

// Apply 는 계획을 집행한다. **판정하지 않는다** — 무엇을 넣고 무엇을 뺄지는 [PlanImport] 가 이미 정했다.
//
// 전부 한 트랜잭션이다. 반쯤 들어간 DB 는 원본과 대조할 기준이 없어 롤백 판단조차 못 한다 —
// "DB 파일 삭제 + 재실행"이라는 되돌리기가 성립하려면 상태가 둘(전혀 없음 · 전부 있음)뿐이어야 한다.
func Apply(ctx context.Context, st *store.Store, p ImportPlan, projectPath string) (Applied, error) {
	var out Applied
	err := st.Tx(ctx, func(tx *store.Tx) error {
		now := time.Now().UTC()
		if err := tx.UpsertProject(model.Project{
			ID: p.Project, Path: projectPath, DefaultBranch: "main", CreatedAt: now,
		}); err != nil {
			return fmt.Errorf("프로젝트 등록 실패(%q): %w", clip(p.Project, 64), err)
		}
		if err := tx.UpsertMachine(model.Machine{
			ID: LegacyMachineID, Hostname: LegacyMachineID, FirstSeen: now, LastSeen: now,
		}); err != nil {
			return fmt.Errorf("이관용 machine 등록 실패: %w", err)
		}

		// ── 세션과 그 절
		for _, ps := range p.Sessions {
			s, _, err := tx.OpenSession(p.Project, LegacyMachineID, ps.Card.Worktree, ps.CCSessionID, ps.Card.Desc)
			if err != nil {
				return fmt.Errorf("세션 등록 실패(%s): %w", clip(ps.Card.File, 64), err)
			}
			if err := tx.SetSessionState(s.ID, ps.State, ps.BlockedWhy); err != nil {
				return fmt.Errorf("세션 상태 기록 실패(%s): %w", clip(ps.Card.File, 64), err)
			}
			out.Sessions++
			for _, sec := range ps.Sections {
				// ★ 절 이름이 **원문 그대로** title 이다. 여기가 비규약 절이 살아남는 자리다.
				if _, err := tx.AddJudgment(model.Judgment{
					Project: p.Project, SessionID: s.ID, At: ps.Card.Updated,
					Kind: sec.Kind, Title: sec.Name, Body: sec.Body,
				}); err != nil {
					return fmt.Errorf("세션 절 저장 실패(%s · %q): %w",
						clip(ps.Card.File, 64), clip(sec.Name, 40), err)
				}
				out.Judgments++
			}
		}

		// ── 큐 항목 (핸드오프 링크가 항목 id 를 가리키므로 항목이 먼저다)
		//
		// ★ `tx.AddItem` 은 `service.AddItem` 이 아니다 — 그쪽의 경로 좌표계 관문
		// (judgeItemPathsCoordinate)을 안 거친다. 그것이 이 자리의 **의도**다:
		// item.paths 로 가는 세 문(add · finish followup · 이관) 중 이관의 관문은
		// PlanImport 에 있고(plan.go, "bad_path_coordinate"), 통과한 경로만 계획에
		// 담겨 여기 온다. 여기서 다시 판정하면 판정이 실행 본문에 흩어져 시험이
		// 그 사본을 단정하게 된다 — 이 함수가 "판정하지 않는다"고 못박은 이유다.
		//
		// 즉 여기 관문이 없는 것은 누락이 아니다. 관문을 옮기려면 계획 쪽에서 옮겨라.
		for _, pi := range p.Items {
			if err := tx.AddItem(pi.Item); err != nil {
				return fmt.Errorf("큐 항목 저장 실패(%s): %w", clip(pi.Item.ID, 64), err)
			}
			out.Items++
		}
		for _, pi := range p.Issues {
			if err := tx.AddItem(pi.Item); err != nil {
				return fmt.Errorf("대시보드 이슈 저장 실패(%s): %w", clip(pi.Item.ID, 64), err)
			}
			out.Items++
		}
		for _, pi := range p.Blockers {
			if err := tx.AddItem(pi.Item); err != nil {
				return fmt.Errorf("대시보드 막힘 저장 실패(%s): %w", clip(pi.Item.ID, 64), err)
			}
			out.Items++
		}

		// ── 핸드오프: 통째로 blob. 큐가 경로 문자열로 걸던 포인터가 여기서 FK 가 된다
		linkedBy := map[string][]string{}
		for _, pi := range p.Items {
			if pi.HandoffRel != "" {
				linkedBy[pi.HandoffRel] = append(linkedBy[pi.HandoffRel], pi.Item.ID)
			}
		}
		for _, h := range p.Handoffs {
			j := model.Judgment{
				Project: p.Project, At: h.At, Kind: model.JudgmentHandoff,
				Title: h.File, Body: h.Body,
			}
			for _, id := range linkedBy[h.Rel] {
				j.Links = append(j.Links, model.JudgmentLink{TargetKind: "item", TargetID: id})
			}
			if _, err := tx.AddJudgment(j); err != nil {
				return fmt.Errorf("핸드오프 저장 실패(%s): %w", clip(h.File, 120), err)
			}
			out.Judgments++
		}

		// ── 랜딩 서사: git log 도 대시보드도 아닌 곳에만 있는 것("무엇이 달라졌나")
		for _, l := range p.Landings {
			at, err := ParseDashAt(l.At)
			if err != nil {
				// PlanImport 가 이미 걸렀다. 여기 오면 계획과 집행이 어긋난 것이다.
				return fmt.Errorf("계획과 집행이 어긋났다 — 랜딩 시각(%q): %w", clip(l.At, 40), err)
			}
			// body·note 는 같은 자리의 두 이름이다. 둘 다 있는 레코드가 생기면
			// 어느 쪽도 버리지 않고 이어 붙인다 — 둘 중 하나를 고르면 그 선택이 어디에도 안 남는다.
			body := strings.TrimSpace(l.Body + "\n\n" + l.Note)
			j := model.Judgment{
				Project: p.Project, At: at, Kind: model.JudgmentHandoff,
				Title: l.Title, Body: body,
			}
			if l.Commit != "" {
				j.Links = append(j.Links, model.JudgmentLink{TargetKind: "commit", TargetID: l.Commit})
			}
			if _, err := tx.AddJudgment(j); err != nil {
				return fmt.Errorf("랜딩 서사 저장 실패(%s): %w", clip(l.Title, 80), err)
			}
			out.Judgments++
		}

		// ── 진척 스냅숏: 근거 없이는 못 들어간다(스키마 CHECK)
		for _, pp := range p.Parts {
			if err := tx.PutSnapshot(model.Snapshot{
				Project: p.Project, Key: pp.Key, Value: pp.Value,
				Method: model.SnapshotManual, Evidence: pp.Evidence,
				InputDigest: pp.Digest, ComputedAt: now,
			}); err != nil {
				return fmt.Errorf("진척 스냅숏 저장 실패(%s): %w", clip(pp.Key, 80), err)
			}
			out.Snapshots++
		}

		tx.LogEvent("legacy_import", p.Project, "", map[string]any{
			"sessions": out.Sessions, "items": out.Items,
			"judgments": out.Judgments, "snapshots": out.Snapshots,
			"rejected": len(p.Rejects), "gone": len(p.Gone),
		})
		return nil
	})
	if err != nil {
		return Applied{}, err
	}
	return out, nil
}
