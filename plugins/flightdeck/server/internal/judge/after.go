package judge

import (
	"fmt"

	"github.com/kweiza/flightdeck/internal/model"
)

// AncestryResult 는 `git merge-base --is-ancestor <dep_sha> <base>` 의 결과다.
//
// **git 의 종료코드 세 값을 세 값으로 나른다.** 기존 도구는 이 자리를 `if git … ; then`
// 하나로 접어 rc=1 과 rc=128 을 같은 값으로 만들었다. 그러면 **오타와 "아직"이 뭉개져**
// 항목 하나가 영구히 굶는데, 출력에는 "아직 안 됐다"로만 보인다 —
// 기다리면 풀린다고 믿게 만드는 상태가 영원히 지속된다.
type AncestryResult int

const (
	AncestryUnknown AncestryResult = iota // 아직 조회하지 않았다. "아니다"가 아니다
	AncestryYes                           // 조상이다 (git rc=0)
	AncestryNo                            // 아직 조상이 아니다 (git rc=1) — 기다리면 풀린다
	AncestryBadRef                        // 그런 ref 가 없다 (git rc=128) — 기다려도 안 풀린다
	// AncestryOrphan 은 커밋은 있는데 **어느 브랜치에도 없다**(rc=1 + `branch --contains` 가 빔).
	// 랜딩이 커밋을 재생하면 원래 sha 가 이 상태가 된다 — 내용은 들어갔는데 조상 판정은
	// 영원히 rc=1 이다. AncestryNo 로 접으면 "기다리면 풀린다"가 되어 항목이 영구히 굶는다.
	AncestryOrphan
)

func (a AncestryResult) String() string {
	switch a {
	case AncestryUnknown:
		return "unknown"
	case AncestryYes:
		return "yes"
	case AncestryNo:
		return "no"
	case AncestryBadRef:
		return "bad-ref"
	case AncestryOrphan:
		return "orphan"
	default:
		// 열거 밖 값도 침묵하지 않는다. 숫자 그대로 실어야 어디서 샜는지 보인다.
		return fmt.Sprintf("ancestry(%d)", int(a))
	}
}

// AfterFacts 는 선행 조건 판정에 필요한 **사실**이다. 판정이 아니다.
//
// 세 맵 모두 **키 부재와 값을 가른다.** 부재 = "조회하지 않았다"이고,
// 그것을 "충족되지 않았다"로 접으면 조회를 빠뜨린 버그가 정상적인 대기로 보인다.
type AfterFacts struct {
	ItemStates  map[string]model.ItemState // dep_item -> 항목 상태
	JobStates   map[string]string          // dep_job  -> 잡 상태 (schema.sql 의 job.state 문자열)
	SHAAncestry map[string]AncestryResult  // dep_sha  -> 조상 판정 결과
}

// 선행 조건 미충족의 사유 코드.
//
// **처방이 다르면 코드가 다르다.** 이 축들을 한 코드로 뭉개면 탈락 사유 분포가
// "무엇을 고쳐야 하나"에 답하지 못한다.
const (
	AfterUnmetItem  = "after-unmet-item"  // 선행 항목이 아직 안 끝났다 — 기다리면 풀린다
	AfterDroppedDep = "after-dropped-dep" // 선행 항목이 폐기됐다 — **영영 안 풀린다**
	AfterUnmetJob   = "after-unmet-job"   // 선행 잡이 아직 안 끝났다 — 기다리면 풀린다
	AfterFailedJob  = "after-failed-job"  // 선행 잡이 실패·정지했다 — 재실행 없이는 안 풀린다
	AfterUnmetSHA   = "after-unmet-sha"   // 아직 조상이 아니다(rc=1) — 기다리면 풀린다
	AfterBadRef     = "after-bad-ref"     // 그런 ref 가 없다(rc=128) — **오타이거나 지워졌다**
	AfterOrphanSHA  = "after-orphan-sha"  // 커밋은 있는데 어느 브랜치에도 없다 — 재생됐다. **영영 안 풀린다**
	AfterUnknown    = "after-unknown"     // 조회하지 않았다 — 판정 자체를 못 했다
	AfterMalformed  = "after-malformed"   // 셋 중 정확히 하나가 아니다 — 스키마 CHECK 를 우회해 들어왔다
	AfterBadState   = "after-bad-state"   // 열거에 없는 상태 문자열 — 스키마와 코드가 어긋났다
)

// AfterSatisfied 는 선행 조건이 전부 충족됐는지 판정한다.
//
// 미충족 사유를 **전부** 돌려준다. 첫 사유에서 끊으면 "하나 풀었더니 또 하나"가 반복되고,
// 그러면 무엇을 기다려야 하는지 한 번에 알 수 없다.
// 순서는 입력 순서 그대로다 — 같은 입력에 같은 답이어야 한다.
//
// 선행이 없으면 충족이다(reasons 는 비어 있다).
func AfterSatisfied(after []model.After, f AfterFacts) (ok bool, reasons []string) {
	for _, a := range after {
		if r := afterOneReason(a, f); r != "" {
			reasons = append(reasons, r)
		}
	}
	return len(reasons) == 0, reasons
}

// afterOneReason 은 선행 하나를 판정해 미충족 사유를 낸다. 충족이면 빈 문자열이다.
func afterOneReason(a model.After, f AfterFacts) string {
	// 셋 중 정확히 하나. 스키마의 CHECK 와 같은 불변식이지만 여기서 다시 본다 —
	// 이 함수는 DB 를 안 거친 입력(REST 본문·이관 파서)도 받는다.
	n := 0
	for _, s := range []string{a.Item, a.Job, a.SHA} {
		if s != "" {
			n++
		}
	}
	if n != 1 {
		return fmt.Sprintf("%s: Item·Job·SHA 중 정확히 하나여야 하는데 %d개다(item=%q job=%q sha=%q)",
			AfterMalformed, n, a.Item, a.Job, a.SHA)
	}

	switch {
	case a.Item != "":
		st, known := f.ItemStates[a.Item]
		if !known {
			return fmt.Sprintf("%s: dep_item=%s 의 상태를 조회하지 않았다", AfterUnknown, a.Item)
		}
		switch st {
		case model.ItemDone:
			return ""
		case model.ItemDropped:
			// 기다림의 끝이 없다. 이 사유를 "아직"과 같은 코드로 내면 항목이 영구히 굶는다.
			return fmt.Sprintf("%s: dep_item=%s 이(가) 폐기됐다 — 기다려도 안 풀린다. 선행을 고쳐라",
				AfterDroppedDep, a.Item)
		case model.ItemOpen, model.ItemClaimed:
			return fmt.Sprintf("%s: dep_item=%s state=%s", AfterUnmetItem, a.Item, st)
		default:
			return fmt.Sprintf("%s: dep_item=%s 의 상태 %q 가 열거에 없다", AfterBadState, a.Item, string(st))
		}

	case a.Job != "":
		st, known := f.JobStates[a.Job]
		if !known {
			return fmt.Sprintf("%s: dep_job=%s 의 상태를 조회하지 않았다", AfterUnknown, a.Job)
		}
		switch st {
		case "ok", "bypassed":
			// bypassed 는 사람이 근거를 남기고 우회한 것이라 충족으로 친다.
			// 그 판단을 여기서 다시 뒤집으면 사람의 명시적 결정이 조용히 무시된다.
			return ""
		case "queued", "running":
			return fmt.Sprintf("%s: dep_job=%s state=%s", AfterUnmetJob, a.Job, st)
		case "fail", "stalled":
			return fmt.Sprintf("%s: dep_job=%s state=%s — 재실행 없이는 안 풀린다", AfterFailedJob, a.Job, st)
		default:
			return fmt.Sprintf("%s: dep_job=%s 의 상태 %q 가 열거에 없다", AfterBadState, a.Job, st)
		}

	default: // a.SHA != ""
		switch f.SHAAncestry[a.SHA] { // 키 부재는 AncestryUnknown(0) 이고, 그것이 의도한 값이다
		case AncestryYes:
			return ""
		case AncestryNo:
			return fmt.Sprintf("%s: dep_sha=%s 이(가) 아직 조상이 아니다(git rc=1)", AfterUnmetSHA, a.SHA)
		case AncestryBadRef:
			return fmt.Sprintf("%s: dep_sha=%s 라는 ref 가 없다(git rc=128) — 오타이거나 지워진 커밋이다. 기다려도 안 풀린다",
				AfterBadRef, a.SHA)
		case AncestryOrphan:
			// 기다림의 끝이 없다 — AfterDroppedDep 와 같은 부류다. 그리고 여기서는 **집행 동사를
			// 함께 낸다**: 실측된 세 건이 전부 "내용은 이미 main 에 있는데 sha 만 재생됐다"였고,
			// 그때 할 일은 기다리기가 아니라 끊기다.
			return fmt.Sprintf("%s: dep_sha=%s 은(는) 커밋은 있는데 어느 브랜치에도 없다 — "+
				"랜딩이 재생했거나 브랜치가 버려졌다. 기다려도 안 풀린다. "+
				"내용이 이미 들어갔으면 `fd after cut <이 항목> --sha %s` 로 끊어라",
				AfterOrphanSHA, a.SHA, a.SHA)
		case AncestryUnknown:
			return fmt.Sprintf("%s: dep_sha=%s 의 조상 여부를 조회하지 않았다", AfterUnknown, a.SHA)
		default:
			return fmt.Sprintf("%s: dep_sha=%s 의 조상 판정값 %s 가 열거에 없다",
				AfterBadState, a.SHA, f.SHAAncestry[a.SHA])
		}
	}
}
