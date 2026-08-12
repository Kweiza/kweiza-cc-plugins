package api

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// publish 의 **봉투** 세 인자(kind·project·session_id)를 라우트마다 못박는다.
//
// `publish_detail_test.go` 가 detail 을 닫았다. 그 바깥의 세 인자는 안 닫혀 있었다 —
// `s.publish(r, kind, project, sessionID, detail)` 의 51축(호출 17곳 × 3)을 하나씩
// 상수로 바꿔 전수로 쟀더니 **32축이 살아남았다**(실측 2026-08-12):
//
//	kind     14축 — session.open·claim·lane 셋만 이미 물려 있었다
//	project   2축 — 나머지는 **허브의 라우팅 키**라 구독 필터가 간접으로 잡고 있었다
//	session  16축 — session.open 하나를 뺀 전부. 앞 항목이 실측한 그 16곳이다
//
// ★ 세 축은 실패 모양이 서로 다르다. kind 가 틀리면 소비자가 **무슨 일인지** 모르고,
// project 가 틀리면 이벤트가 **아예 안 가며**(Hub.Publish 가 구독자 project 로 거른다),
// session_id 가 틀리면 **누구의 행위인지** 모른다. 그래서 한 표에서 셋을 나란히 재되
// 단정은 축마다 따로 세운다 — 접으면 어느 축이 죽었는지 화면에 안 남는다.
//
// ★ 구독은 `Subscribe("")`(전 프로젝트)다. 프로젝트로 구독하면 project 축이 "값이
// 틀렸다"가 아니라 "이벤트가 안 왔다"로 나타나 두 사건이 한 단정에 접힌다.

// 이 표가 다루는 두 좌표. 서로 달라야 project 축의 짝이 성립한다.
const (
	envProjectA = testProject // "cp"
	envProjectB = "cq"
)

// envelopeCase 는 라우트 하나다.
//
// prepare 는 구독 **전에** 그 좌표의 상태를 만들고, fire 는 구독 **후에** 정확히
// 이벤트 한 건을 낸다. fire 가 돌려주는 것은 그 이벤트가 실어야 할 세션 좌표다 —
// 대부분 부른 세션이지만, session.open 은 방금 연 세션이고 발번·스냅숏은 빈 값이다.
type envelopeCase struct {
	name    string
	kind    string
	prepare func(t *testing.T, e *env, project, sess string)
	fire    func(t *testing.T, e *env, project, sess string) string
}

// TestPublishEnvelopeCarriesKindProjectAndSession 는 라우트마다 봉투 세 축을 잰다.
func TestPublishEnvelopeCarriesKindProjectAndSession(t *testing.T) {
	for _, c := range envelopeCases() {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, nil)
			sessA := e.openIn(t, envProjectA, "cc-env-a")
			sessB := e.openIn(t, envProjectB, "cc-env-b")

			c.prepare(t, e, envProjectA, sessA)
			c.prepare(t, e, envProjectB, sessB)

			sub := e.srv.hub.Subscribe("")
			defer e.srv.hub.Unsubscribe(sub)

			wantSessA := c.fire(t, e, envProjectA, sessA)
			ev1 := nextEvent(t, sub)
			wantSessB := c.fire(t, e, envProjectB, sessB)
			ev2 := nextEvent(t, sub)

			// ① kind — 라우트의 이름이다. 두 호출 다 같은 이름이어야 한다.
			for i, ev := range []Event{ev1, ev2} {
				if ev.Kind != c.kind {
					t.Errorf("[kind] %d번째 이벤트가 %q 로 발행됐다(기대 %q)", i+1, ev.Kind, c.kind)
				}
			}

			// ② project — 라우팅 키. 좌표가 서로 다른 두 요청을 나란히 잰다.
			if ev1.Project != envProjectA || ev2.Project != envProjectB {
				t.Errorf("[project] 이벤트가 %q·%q 를 실었다(기대 %q·%q)",
					ev1.Project, ev2.Project, envProjectA, envProjectB)
			}

			// ③ session_id — 누구의 행위인가.
			if wantSessA == "" || wantSessB == "" {
				// 세션 좌표가 **없는** 쓰기다(요청 본문에 session_id 필드가 없다).
				// 짝이 원리적으로 없으므로 "비어 있다"를 단정한다 — 여기에 무언가
				// 실리면 그것은 이 라우트가 알 수 없는 값이다.
				if ev1.SessionID != "" || ev2.SessionID != "" {
					t.Errorf("[session] 세션 좌표가 없는 쓰기인데 %q·%q 가 실렸다",
						ev1.SessionID, ev2.SessionID)
				}
				return
			}
			if wantSessA == wantSessB {
				t.Fatalf("[session] 짝이 안 갈렸다 — 두 기대값이 둘 다 %q 다. "+
					"이 축은 그 값을 박은 상수도 통과하므로 지금 아무것도 안 재고 있다", wantSessA)
			}
			if ev1.SessionID != wantSessA {
				t.Errorf("[session] 첫 이벤트가 %q 를 실었다(기대 %q)", ev1.SessionID, wantSessA)
			}
			if ev2.SessionID != wantSessB {
				t.Errorf("[session] 둘째 이벤트가 %q 를 실었다(기대 %q)", ev2.SessionID, wantSessB)
			}
		})
	}
}

// noPrepare 는 준비가 필요 없는 라우트의 자리다.
func noPrepare(*testing.T, *env, string, string) {}

// envelopeCases 는 publish 하는 라우트 전부다.
//
// ★ **전수여야 한다.** 표에서 라우트가 빠지면 그 자리는 "안 물렸다"가 아니라 "안 쟀다"인데
// 화면이 같다. 그래서 아래 TestEveryPublishSiteIsInTheEnvelopeTable 이 소스의 publish
// 호출 수와 이 표를 대조한다.
func envelopeCases() []envelopeCase {
	// lane.release 는 회수할 줄 행 번호를 준비 단계에서 알아야 한다.
	laneRows := map[string]int{}

	itemID := func(prefix, project string) string { return prefix + "-" + project }

	claimItem := func(t *testing.T, e *env, project, sess, id string) {
		t.Helper()
		e.okBody(t, e.write(http.MethodPost, "/api/v1/items/"+id+"/claim",
			map[string]any{"project": project, "session_id": sess}), "선점("+id+")")
	}

	return []envelopeCase{{
		name: "session.open",
		kind: "session.open",
		// 여는 것 자체가 재려는 쓰기다.
		prepare: noPrepare,
		fire: func(t *testing.T, e *env, project, sess string) string {
			return e.openIn(t, project, "cc-fire-"+project)
		},
	}, {
		name:    "session.state",
		kind:    "session.state",
		prepare: noPrepare,
		fire: func(t *testing.T, e *env, project, sess string) string {
			e.okBody(t, e.write(http.MethodPatch, "/api/v1/sessions/"+sess,
				map[string]any{"state": "paused"}), "상태 변경")
			return sess
		},
	}, {
		name:    "session.rekey",
		kind:    "session.rekey",
		prepare: noPrepare,
		fire: func(t *testing.T, e *env, project, sess string) string {
			e.okBody(t, e.write(http.MethodPost, "/api/v1/sessions/"+sess+"/rekey",
				map[string]any{"cc_session_id": "cc-rekeyed-" + project}), "rekey")
			return sess
		},
	}, {
		name:    "session.signal",
		kind:    "session.signal",
		prepare: noPrepare,
		fire: func(t *testing.T, e *env, project, sess string) string {
			e.okBody(t, e.write(http.MethodPost, "/api/v1/sessions/"+sess+"/signals",
				map[string]any{"kind": "prompt"}), "신호")
			return sess
		},
	}, {
		name:    "session.workspace",
		kind:    "session.workspace",
		prepare: noPrepare,
		fire: func(t *testing.T, e *env, project, sess string) string {
			e.okBody(t, e.write(http.MethodPost, "/api/v1/sessions/"+sess+"/workspaces",
				map[string]any{"project": project, "path": filepath.Join(e.repo, project),
					"is_primary": false}), "작업 트리 추가")
			return sess
		},
	}, {
		name:    "judgment.note",
		kind:    "judgment.note",
		prepare: noPrepare,
		fire: func(t *testing.T, e *env, project, sess string) string {
			e.okBody(t, e.write(http.MethodPost, "/api/v1/judgments", map[string]any{
				"project": project, "session_id": sess, "kind": "decision",
				"title": "결정", "body": "근거를 적는다",
			}), "판단")
			return sess
		},
	}, {
		name: "counter.alloc",
		kind: "counter.alloc",
		// ★ 세션 좌표가 **없다** — counterRequest 에 session_id 필드가 없다. 발번은
		// 프로젝트의 사건이지 세션의 사건이 아니다.
		prepare: noPrepare,
		fire: func(t *testing.T, e *env, project, sess string) string {
			e.okBody(t, e.write(http.MethodPost, "/api/v1/counters/rev/next",
				map[string]any{"project": project}), "발번")
			return ""
		},
	}, {
		name: "snapshot.put",
		kind: "snapshot.put",
		// ★ 여기도 세션 좌표가 없다(snapshotRequest 에 session_id 필드가 없다).
		prepare: noPrepare,
		fire: func(t *testing.T, e *env, project, sess string) string {
			e.okBody(t, e.write(http.MethodPut, "/api/v1/snapshots/part.pct", map[string]any{
				"project": project, "value": "3", "method": "command",
				"evidence": "명령으로 쟀다", "input_digest": "d",
			}), "스냅숏")
			return ""
		},
	}, {
		name:    "item.add",
		kind:    "item.add",
		prepare: noPrepare,
		fire: func(t *testing.T, e *env, project, sess string) string {
			e.okBody(t, e.write(http.MethodPost, "/api/v1/items", map[string]any{
				"project": project, "session_id": sess, "id": itemID("env-add", project),
				"title": "제목", "body": "본문",
			}), "항목 등록")
			return sess
		},
	}, {
		name: "item.claimed",
		kind: "item.claimed",
		prepare: func(t *testing.T, e *env, project, sess string) {
			e.addItemIn(t, project, sess, itemID("env-claim", project), nil)
		},
		fire: func(t *testing.T, e *env, project, sess string) string {
			claimItem(t, e, project, sess, itemID("env-claim", project))
			return sess
		},
	}, {
		name: "claim.reclaim",
		kind: "claim.reclaim",
		prepare: func(t *testing.T, e *env, project, sess string) {
			e.addItemIn(t, project, sess, itemID("env-rc", project), nil)
			claimItem(t, e, project, sess, itemID("env-rc", project))
		},
		// ★ 세션 좌표가 요청 본문에 **없다** — 서버가 결과에서 꺼내 싣는다(res.Holder).
		// 회수당한 쪽이 누구인지는 이 한 줄 말고 답할 자리가 없다.
		fire: func(t *testing.T, e *env, project, sess string) string {
			e.okBody(t, e.write(http.MethodPost,
				"/api/v1/items/"+itemID("env-rc", project)+"/claim/release", map[string]any{
					"project": project, "actor": "사람", "reason": "신호가 없다",
				}), "선점 회수")
			return sess
		},
	}, {
		name: "item.finish",
		kind: "item.finish",
		prepare: func(t *testing.T, e *env, project, sess string) {
			e.addItemIn(t, project, sess, itemID("env-fin", project), nil)
			claimItem(t, e, project, sess, itemID("env-fin", project))
		},
		fire: func(t *testing.T, e *env, project, sess string) string {
			e.okBody(t, e.write(http.MethodPost,
				"/api/v1/items/"+itemID("env-fin", project)+"/finish", map[string]any{
					"project": project, "session_id": sess, "outcome": "done",
					"title": "닫았다", "body": "landed: 표면 하나를 얹었다",
				}), "마무리")
			return sess
		},
	}, {
		name: "item.move",
		kind: "item.move",
		prepare: func(t *testing.T, e *env, project, sess string) {
			e.addItemIn(t, project, sess, itemID("env-mv", project), nil)
			e.openIn(t, "dst-"+project, "cc-dst-"+project) // 대상 프로젝트 등록
		},
		// ★ 이 라우트의 project 는 **출발 프로젝트**다. 그래서 이 축이 갈리는 것이
		// 곧 "어디서 어디로 옮겼나"의 절반이다.
		fire: func(t *testing.T, e *env, project, sess string) string {
			e.okBody(t, e.write(http.MethodPost,
				"/api/v1/items/"+itemID("env-mv", project)+"/move", map[string]any{
					"project": project, "session_id": sess, "to": "dst-" + project,
				}), "이동")
			return sess
		},
	}, {
		name: "item.after.cut",
		kind: "item.after.cut",
		prepare: func(t *testing.T, e *env, project, sess string) {
			e.addItemIn(t, project, sess, itemID("env-cut", project), map[string]any{
				"after": []map[string]any{{"item": "env-dep"}},
			})
		},
		fire: func(t *testing.T, e *env, project, sess string) string {
			e.okBody(t, e.write(http.MethodPost,
				"/api/v1/items/"+itemID("env-cut", project)+"/after/cut", map[string]any{
					"project": project, "session_id": sess,
					"dep": map[string]any{"item": "env-dep"},
				}), "선행 절단")
			return sess
		},
	}, {
		name:    "lane.acquire",
		kind:    "lane.acquire",
		prepare: noPrepare,
		fire: func(t *testing.T, e *env, project, sess string) string {
			e.okBody(t, e.write(http.MethodPost, "/api/v1/landing", map[string]any{
				"project": project, "session_id": sess, "mode": LandModeAcquire,
			}), "랜딩 취득")
			return sess
		},
	}, {
		name: "lane.release",
		kind: "lane.release",
		prepare: func(t *testing.T, e *env, project, sess string) {
			got := e.okBody(t, e.write(http.MethodPost, "/api/v1/landing", map[string]any{
				"project": project, "session_id": sess, "mode": LandModeAcquire,
			}), "랜딩 취득")
			laneRows[project] = intOf(t, got, "row_id")
		},
		// ★ 여기도 세션 좌표가 요청 본문에 없다 — res.SessionID 다.
		fire: func(t *testing.T, e *env, project, sess string) string {
			e.okBody(t, e.write(http.MethodPost,
				"/api/v1/landing/rows/"+itoa(laneRows[project])+"/release", map[string]any{
					"project": project, "actor": "사람", "reason": "신호가 없다",
				}), "줄 행 회수")
			return sess
		},
	}}
}

// addItemIn 은 지정한 프로젝트에 항목 하나를 등록한다.
func (e *env) addItemIn(t *testing.T, project, sess, id string, extra map[string]any) {
	t.Helper()
	req := map[string]any{
		"project": project, "session_id": sess, "id": id,
		"title": id + " 제목", "body": id + " 본문",
	}
	for k, v := range extra {
		req[k] = v
	}
	e.okBody(t, e.write(http.MethodPost, "/api/v1/items", req), "항목 등록("+id+")")
}

// publishCall 은 소스에서 발행 자리를 세는 패턴이다.
var publishCall = regexp.MustCompile(`s\.publish\(r, `)

// TestEveryPublishSiteIsInTheEnvelopeTable 는 **표가 전수인지**를 소스와 대조한다.
//
// ★ 이 시험이 없으면 위 표는 시간이 지나며 조용히 낡는다. publish 가 하나 늘어도 표는
// 그대로 초록이고, 안 재진 그 자리는 "안 물렸다"가 아니라 "안 쟀다"인데 **화면이 같다.**
// 이 레포가 전수 측정을 반복해서 쓰는 이유가 정확히 그 구분이다.
//
// 표에 없는 자리를 상수 하나로 세는 것이 아니라 **소스를 세서** 대조한다 — 수를 두 번
// 적으면 그 둘이 갈린 날 어느 쪽이 참인지 말해 주는 자리가 없다.
func TestEveryPublishSiteIsInTheEnvelopeTable(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("패키지 디렉토리를 못 읽었다: %v", err)
	}
	sites := 0
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("%s 를 못 읽었다: %v", name, err)
		}
		sites += len(publishCall.FindAll(raw, -1))
	}
	// 대조축 먼저 — 0건이면 아래 비교가 "표도 0건"인 변이에 대해 공짜로 참이 된다.
	if sites == 0 {
		t.Fatal("소스에서 publish 자리를 하나도 못 찾았다 — 이 시험이 아무것도 안 재고 있다")
	}

	// handlePatchSession 의 되읽기 실패 갈래는 표에 **없다**. 결정론적으로 못 밟기
	// 때문이고(근거: patch_session_partial_test.go), 그래서 이 하나만 예외로 센다.
	const unreachable = 1
	if got, want := len(envelopeCases())+unreachable, sites; got != want {
		t.Fatalf("표가 %d 자리(못 밟는 갈래 %d 포함)를 다루는데 소스에는 publish 가 %d 자리다 — "+
			"새로 생긴 발행 자리가 표에 없으면 그 자리는 안 재진 것이다", got, unreachable, want)
	}
}
