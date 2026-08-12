# 배포 관측을 바인드 성공 뒤로 옮긴다

- 항목: `fd-deploy-note-precedes-bind`
- 선행: `fd-ledger-has-no-deploy-time` (2026-08-08 handoff — "리스너를 열기 전에 적는 것을 안 고쳤다")
- 날짜: 2026-08-11

## 문제

`cmd/fd` 의 `noteBuild` 가 `api.Serve` 보다 **먼저** 돈다(`serve.go:216` vs `serve.go:242`).
그래서 포트를 이미 다른 프로세스가 물고 있어 곧바로 죽는 기동도 `server.deploy` 를 남긴다.
그러면 `LastDeployAt` 이 **한 번도 응답한 적 없는 바이너리**의 시각을 낸다 — 그 함수의
독스트링("지금 도는 실행 파일이 자리 잡은 시각")과 사실이 어긋난다.

재현은 준비된 함정이다. 컨테이너가 `:7420` 을 물고 도는데 사람이 README 의
`go run ./cmd/fd serve` 를 치면, `compose.yaml` 과 README 의 기본값이 같은 포트라 즉시 부딪힌다.

★ **그리고 같은 DB 다.** `compose.yaml:35` 이 `~/.flightdeck:/data` 를 마운트하므로
컨테이너의 `/data/fd.db` 는 호스트의 `~/.flightdeck/fd.db` 와 같은 파일이다. 호스트에서 띄운
임시 바이너리는 `/data` 가 없어 `~/.flightdeck/fd.db` 를 여는데, 그것이 컨테이너가 쥔 바로 그
원장이다. 오염이 실재한다.

2차 피해도 있다: 임시 바이너리가 마지막 배포로 남으면, 그 다음 컨테이너 **재기동**(배포가
아닌)이 exe 불일치로 배포 한 건을 또 만든다.

**지금 피해가 안 보이는 이유**: `LastDeployAt` 을 읽는 프로덕션 소비자가 하나도 없다(시험뿐).
`AckWindow` 의 `since` 후보로 대기 중이고(`service/service.go:77-83`), 그 배선이 붙는 순간
오염된 시각이 지표의 절단선이 된다.

## 범위 결정 — 바인드까지

"서비스한 기동만 배포로 센다"를 어디까지 미느냐를 물었고, **바인드까지**로 정했다.

기각한 것과 이유:

- **바인드 + 정본 주소만** — 다른 포트로 뜬 임시 `go run` 까지 막지만 "정본 주소"라는 근거
  없는 상수가 하나 는다. 컨테이너가 `--addr` 를 바꾸면 배포가 통째로 안 적힌다.
- **첫 요청 응답까지** — 조용한 서버는 배포를 영영 못 적고, 배포 시각이 "첫 손님이 온 시각"
  으로 밀린다. 원장이 알고 싶은 것("지금 도는 것이 언제부터 이것인가")과 어긋난다.
- **닫는다(반증 채택)** — 위의 같은-DB 사실과 2차 피해 때문에 "가짜 한 행은 다음 진짜 배포가
  덮는다"가 성립하지 않는다. 재기동이 배포로 잡히는 쪽은 덮이지 않고 늘어난다.

**남는 한계를 정직하게 적는다**: 다른 포트로 뜬 임시 `go run` 은 바인드에 성공하므로 이 정의
안에서 여전히 배포로 적힌다. 그것은 거짓 관측이 아니라 이 정의의 경계다. 2차 피해는 후속으로
낸다.

## 설계

### ① 표면 — `internal/api`

```go
// Listen 은 주소를 연다. 바인드 성공/실패를 값으로 낸다.
func Listen(ctx context.Context, addr string, log *slog.Logger) (net.Listener, error)

// Serve 는 이미 열린 리스너 위에서 돈다. 리스너 소유권을 가져간다.
func Serve(ctx context.Context, ln net.Listener, h Handler, log *slog.Logger) error
```

`api.go:433-438` 의 `net.Listen` + 실패 로그가 통째로 `Listen` 으로 옮겨간다. 그 아래
(BaseContext 떼기 · `done` 채널 · 2단 셧다운)는 손대지 않는다. `Listen` 이 `ctx` 를 받는
이유는 그 실패 로그가 `log.ErrorContext(ctx, ...)` 라서다 — 상관키를 잃지 않으려면 함께
옮겨야 한다.

**콜백이 아니라 값인 이유.** `ready func()` 를 `Serve` 에 넘기는 안을 기각했다 —
`serveAPIOptions` 의 ★ 주석이 이 저장소가 이미 치른 값을 적어 뒀다: 콜백을 받으면 시험이
넘길 수 있는 것이 `nil` 뿐이라 **콜백 안이 안 잠긴다**(2026-08-07 실측). 바인드 성공을 값으로
내면 순서가 호출부에서 자명해지고, 순서를 어기는 것은 컴파일이 아니라 시험이 잡는다.

**리스너 소유권**은 `Serve` 가 가져간다. `http.Server.Serve` 가 반환할 때 리스너를 닫으므로
새 정리 경로가 안 생긴다. `Listen` 과 `Serve` 사이에는 실패 경로를 두지 않는다.

**부수 소득 둘**:
- `serve_drain_test.go:22-30` 의 `serveAddrFromLog` 가 **주소를 얻는 수단으로는 죽는다**.
  주소를 쓰는 시험 3곳이 `ln.Addr()` 를 직접 읽어 로그 파싱과 5초 폴링이 사라진다. 헬퍼
  자체는 남는다 — 요청을 안 보내는 시험 하나가 "서버가 떴다"를 기다리는 데 쓴다.
- `PortAdvice` 가 제 갈래에만 붙는다. 지금은 `serveWithWatcher` 의 `serveErr` 갈래가 바인드
  실패와 "리스너가 스스로 죽음"을 섞어 받아 후자에도 포트 처방을 붙인다.

### ② 순서 — `cmd/fd/runServe`

```
DB 열기
  → 조립(svc · webH · watcher · ledgerJob · handler)
  → api.Listen
      실패 → PortAdvice 를 찍고 return 1        ★ 원장을 안 건드린다
  → noteBuild(...)                              ★ 바인드 성공 뒤
  → log.Info("기동", route: ln.Addr())          ★ :0 이면 실제 포트가 찍힌다
  → serveWithWatcher(ctx, ln, handler, log, watcher)
```

조립을 `Listen` **앞**에 둔다. 리스너가 열린 순간부터 backlog 가 쌓이므로 준비가 끝난 뒤 여는
것이 맞다. `noteBuild` 는 SQLite 쓰기 한 번이라 그 사이 대기는 무시할 수 있다.

`serveWithWatcher` 는 `addr string` 대신 `ln net.Listener` 를 받는다(`serve.go:322`).
드레인 악수(`drainServe()` → `<-served`)와 감시기 join(`<-watchDone`)은 그대로다 — 리스너를
누가 열었는지는 그 인과에 영향이 없다.

### ③ 시험

**새 회귀 시험** — `cmd/fd/TestServeSkipsDeployNoteWhenBindFails`

리스너를 하나 미리 열어 그 실제 주소를 `runServe` 에 `--addr` 로, `--db` 에 `t.TempDir()` 아래
경로를 준다. 단정 둘:

1. `runServe` 가 1 을 반환한다.
2. **그 DB 의 `server.deploy` 가 0행이다.**

지금 코드에서 이 시험은 빨간불이다(현재는 1행이 적힌다). `runServe` 를 실물로 부르므로 순서
전체를 잠근다 — `runServe` 가 통째로 뮤테이션 투명하다는 기존 서술(`newServeWatcher` 주석)에
이 갈래만큼 구멍을 낸다. 바인드 실패 갈래는 즉시 반환하므로 감시기도 백업 잡도 안 뜬다.

**성공 갈래**는 기존 `cmd/fd/deploy_note_test.go` 가 이미 잡는다(`noteBuild` 를 직접 부르고
적힌 정체가 그 관측에서 나왔는지까지 본다). 건드리지 않는다.

**마이그레이션** — 시그니처 변경을 따라가는 기계적 수정:
- `cmd/fd/serve_test.go:70, 119, 158` — `"127.0.0.1:0"` → `api.Listen` 으로 연 리스너
- `internal/api/serve_drain_test.go:99, 201, 273, 325` — 같은 변경
- `internal/api/serve_drain_test.go:100, 202, 274` — `serveAddrFromLog(...)` → `ln.Addr().String()`
  (326 은 빼고 그대로 둔다 — 아래를 보라)
- `serveAddrFromLog` 헬퍼는 남는다. 네 호출부 중 셋은 위처럼 사라지지만, 요청을 안 보내는
  시험 하나(`TestServeShutdownLogsDrainMs`)가 주소가 아니라 "서버가 떴다"를 기다리는
  동기화 수단으로 계속 쓴다 — 호출부가 0이 되지 않는다.

### ④ 거짓이 되는 주석

셋이 사실과 어긋나게 된다. 함께 고친다.

- `store/deploy.go:35-41` — ★ "아직 못 가르는 것: 뜨지 못한 기동" 절 전체. 고쳐졌으므로
  **남는 경계**(바인드에 성공한 임시 기동)로 다시 쓴다.
- `store/deploy.go:71-74` — `LastDeployAt` 독스트링. 지금 임시로 붙은 "처음 관측된 시각"
  단서가 필요 없어진다. "지금 도는 실행 파일이 자리 잡은 시각"이 이제 참이다.
- `serve.go:136-146` — `noteBuild` 주석에 **왜 바인드 뒤인지**를 넣는다. 순서가 이 함수의
  계약이 되었으므로 그것을 아는 자리가 있어야 한다.

DESIGN.md 는 안 만진다 — 이 한계를 애초에 적지 않았고(§7 · §10 어디에도 바인드 순서 서술이
없다), 다른 세션이 576행을 쥐고 있어 겹침도 피한다.

## 후속

- **임시 `go run` 이 공유 원장을 오염시킨다.** 바인드에 성공한 임시 기동은 이 정의 안에서
  배포로 적히고, `~/.flightdeck:/data` 마운트 때문에 그것이 컨테이너의 원장이다. 그 결과
  다음 컨테이너 재기동이 배포로 잡힌다. 가르는 축(정본 주소? 실행 파일 자리?)과 그것이
  과잉인지를 따로 판단해야 한다.

## 관문

랜딩 전 다섯 줄. **cwd 를 출력에 남긴다** — 관문이 전부 "무출력이면 통과" 형태라, 모듈 밖에서
돌면 아무것도 안 보고 조용히 통과한다.

```
cd <워크트리>/plugins/flightdeck/server && pwd
gofmt -l .
go vet ./...
GOOS=windows GOARCH=amd64 go vet ./...
GOOS=darwin GOARCH=arm64 go vet ./...
go test ./internal/... ./cmd/fd/ -count=1
```

교차 vet 을 `go build` 로 대신하지 않는다 — `build` 는 `_test.go` 를 건너뛰어 시험 코드에
대해 관문이 열려 있다. 이 작업은 시험 7곳을 고치므로 특히 그렇다.
