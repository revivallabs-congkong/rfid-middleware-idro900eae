# 수정 완료 보고서 — RFID 미들웨어 v0.3.0 (pulse-middleware-v2)

| | |
|---|---|
| 저장소 | `revivallabs-congkong/rfid-middleware-idro900eae` |
| 버전 | v0.2.2 → **v0.3.0** |
| 완료일 | 2026-09-06 |
| 릴리즈 | https://github.com/revivallabs-congkong/rfid-middleware-idro900eae/releases/tag/v0.3.0 |
| 근거 문서 | 가이드 `pulse-gate-binding-rfid-guide.ko.md` v1.2 §6 / 플랜 `pulse-middleware-v2.plan.md` v0.2 §3.1 / 프로토콜 v1.2 |
| 범위 준수 | **서버·프로토콜·카탈로그 양식·config 스키마 무변경.** 미들웨어 코드만 변경 |

## 1. 요구사항별 결과

| # | 요구 | 반영 | 파일 | 커밋 |
|---|---|:--:|---|---|
| **FR-04** | rebind 가드 2차 조건 `prev.Meta != meta`(구조체 전체) → `prev.Meta.BoothName != meta.BoothName`. 다른 게이트 토큰은 정지 유지 | ✅ | `internal/sender/preflight.go` | `fix:` **1e22786** (별도) |
| **FR-01** | `SessionVerified = boothName==카탈로그 name && gate∈{ACTIVE,ACTIVE_WARNING}`. unitName 비교 제거, 카드엔 meta unitName 그대로 | ✅ | `internal/gui/run.go` (`sessionVerified()` 추출) | `feat:` **4e26ddc** |
| **FR-02** | 활성 리더 meta 60초(±10s) 재조회. 200+계약 준수만 반영, 그 외 무시(상태·fingerprint 불변). 변화 시에만 `Gates.Set`+`META_REFRESHED` 1건 | ✅ | `internal/sender/metarefresh.go`(신규), `internal/app/app.go` | `feat:` **4e26ddc** |
| **FR-02a** | cooldownSec 0↔양수 → ACTIVE↔ACTIVE_WARNING (reason 문구 preflight 동일) | ✅ | `internal/sender/metarefresh.go` | 〃 |
| **FR-02b** | 재조회는 rebind 가드 미경유. fingerprint 비교·변경 없음(기존 값 보존), 404를 회수 신호로 쓰지 않음 | ✅ | 〃 | 〃 |
| **FR-03** | 카탈로그 헤더 "유닛"→"세션(내보내기 시점)"(+1열 "게이트"), 리더 카드 "현재 세션:" 라벨, 마법사 "게이트/현재 세션" | ✅ | `internal/gui/assets/app.js`, `internal/gui/wizard.go` | `feat:` **4e26ddc** |
| **DOC-02** | HANDOFF v0.3.0 항목, session-scheduler 머리말 "보류 — pulse-middleware-v2로 대체" | ✅ | `HANDOFF.ko.md`, `docs/01-plan/features/session-scheduler.plan.md` | `docs:` **3321b31** |

> **구현 주의(FR-02b 관련)**: `store.SetGate`는 meta≠nil일 때 `token_fingerprint=fingerprint`를 함께 기록한다. 빈 fingerprint를 넘기면 DB 컬럼이 지워지므로, 재조회는 **게이트 엔트리의 기존 fingerprint를 그대로 재전달**해 값을 보존한다(비교·변경 없음, rebind 키 불변).

## 2. "하지 말 것" 준수
- `internal/gui/catalog.go:89`(빈 unitName 제외) **미변경**
- config 스키마·`readers[].sessionId`·SQLite 스키마 **미변경**
- 토큰 원문 로그/status.json 유출 없음 — `domain.Secret` 유지, `META_REFRESHED`는 readerId·unitName·cooldownSec만 기록(토큰 없음)

## 3. 신규 단위 테스트
- **가드(a)**: 같은 boothName + 다른 fingerprint + unitName 변경 → **ACTIVE**(`TestPreflightSameGateChangedMetaProceeds`); 다른 boothName → **REBIND**(기존 `TestPreflightRebindDetection` 유지)
- **재조회(b)**: 값 변경 시 갱신+fingerprint 보존+Set 1회 / 무변경 시 Set 0회 / 5xx·404·body위반 시 상태·fingerprint 불변·Set 0회 / 비활성 리더는 서버 미호출 / 주기 60±10s 범위
- **쿨다운(c)**: 0↔60 → ACTIVE_WARNING↔ACTIVE
- **FR-01 배지**: booth 일치+활성=✓, booth 불일치·비활성=✗ (unitName 무관)

## 4. 수용 기준
- `go vet ./...` ✅ / `go test -race ./...` ✅ (B1~B7·RDR1~3·GUI 전부 유지)
- `GOOS=windows GOARCH=amd64` 교차빌드 ✅ / CI 통과·설치 패키지 산출 ✅
- 부하: 활성 리더당 tick 1회(8대 ≤ 8건/분), 무변경 시 SQLite 쓰기 0건 — 테스트로 계수 확인 ✅

## 5. 시나리오 T1~T5

| | 내용 | 상태 |
|---|---|---|
| T1 | 콘솔 세션 전환 → 60초 내 카드 세션명 갱신·배지 ✓ 유지·status.json 갱신 | ⏳ **실서버 실측 대기**(설치 후 현장) |
| T2 | 전환 후 같은 게이트 토큰 재발급 → ACTIVE | ✅ 단위 등가 검증 |
| T3 | 다른 게이트 토큰 → REBIND | ✅ 단위 등가 검증 |
| T4 | cooldown 0↔양수 전환 | ✅ 단위 등가 검증 |
| T5 | 오프라인 중 재조회 실패 → 상태 불변 / 복구 후 자동 갱신 | ✅ 실패-불변 단위 검증 / ⏳ 복구 자동 갱신은 현장 실측 대기 |

T1·T5(복구)는 httptest+fake clock으로 로직 등가 검증했고, 실서버/테스트 이벤트 실측은 v0.3.0 설치 후 진행 예정(USB 준비되면).

## 6. 서버측에 알릴 사항
- **계약 해석 불일치 없음.** 가이드 v1.2 §6 / 플랜 §3.1 / 프로토콜 v1.2와 일치하게 구현했으며, 문서 수정이 필요한 지점은 발견되지 않았다.
- 참고: FR-04 가드는 이제 **boothName만**으로 게이트 동일성을 판정한다. 리스크표(플랜 §5)대로 **콘솔에서 게이트(부스) 이름을 변경하면** 같은 게이트를 "다른 게이트"로 오인해 REBIND가 걸린다 — "게이트 이름은 한 번 만들면 불변" 운영 규칙 유지가 전제다.

## 7. 남은 것
- USB 준비되면 v0.3.0 설치 파일을 현장 노트북에 갱신하고 **T1·T5(복구) 실서버 실측** 진행.
