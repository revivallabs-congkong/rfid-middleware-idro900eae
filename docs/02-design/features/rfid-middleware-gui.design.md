# rfid-middleware-gui Design Document

> **Summary**: GUI(트레이 + 로컬 웹 UI) 전환 설계 완성본. 화면·API 계약·현장 점검
> 마법사·코어 additive 변경·수명주기·설치 패키지를 확정한다. GUI 기술 세부
> (§9)만 M0 스파이크 결과 대기.
>
> **Plan**: `docs/01-plan/features/rfid-middleware-gui.plan.md`
> **Date**: 2026-09-02 · **Status**: Draft v0.9 (M0 스파이크 반영 전)

---

## 1. 아키텍처 골격 (plan §6 확정 사항 요약)

- 단일 exe. 기동 시 `app.lock` 중재로 **호스팅 모드**(GUI가 `app.Run` in-process
  소유) / **관측 모드**(서비스 실행 중, 읽기 전용+제어는 CLI 재실행) 분기.
- 렌더링: go:embed 정적 자산 + `127.0.0.1` ephemeral 포트 HTTP + 기본 브라우저.
  트레이 상주(cgo-free). 로그 스트림은 drop-oldest ring buffer 경유(§6.3).
- 코어 변경은 additive 만 (§6). 파이프라인 불변식·`app.Run` 시그니처 불변.

## 2. 화면 상세

### 2.1 공통 레이아웃

```text
┌────────────────────────────────────────────────────────────┐
│ ● 정상 운영 중        [호스팅 모드]   v0.2.0 · cfg 8623c9a7 │ ← 상단바(신호등·모드·버전)
├──────────┬─────────────────────────────────────────────────┤
│ 대시보드 │  (선택된 탭 내용)                                │
│ 세션     │                                                 │
│ 로그     │                                                 │
│ 현장점검 │                                                 │
└──────────┴─────────────────────────────────────────────────┘
토스트: 우하단 (카탈로그 갱신·오류 등), 5s 자동 소멸(오류는 수동 닫기)
```

### 2.2 신호등 판정 규칙 (우선순위 위에서부터, 첫 일치 적용)

| 순위 | 조건 | 신호등 | 헤드라인 문구 |
| --- | --- | --- | --- |
| 1 | `senderState == HALTED` (400 error bind) | 🔴 오류 | "전송이 전역 중단됨 — 프로그램 결함, 개발팀 연락 필요. 스캔은 계속 저장되는 중" |
| 2 | 어느 리더든 `SUSPENDED_TOKEN` | 🔴 오류 | "'{리더}' 토큰이 회수됨 — 새 카탈로그 파일로 재개 필요" |
| 3 | 어느 리더든 `SUSPENDED_CONFIG` / `SUSPENDED_REBIND` | 🔴 오류 | 조치 문장 표(§2.3) 참조 |
| 4 | 어느 리더든 `connState == DISCONNECTED` 30초 초과 지속 | 🔴 오류 | "'{리더}' 리더 연결 끊김 — 전원·케이블 확인" |
| 5 | NTP `skewSec ≥ 240` | 🔴 오류 | "PC 시계가 서버와 4분 이상 어긋남 — 체크인이 거부될 수 있음, 시간 동기화 필요" |
| 6 | 큐 최고령 항목이 20시간 초과 (24h 만료 임박) | 🔴 오류 | "미전송 기록이 곧 만료됨 — 네트워크 복구 필요" |
| 7 | 어느 리더든 `PREFLIGHT_RETRY` / `RECONNECTING` | 🟡 주의 | "서버/리더 연결 재시도 중" |
| 8 | 어느 리더든 `ACTIVE_WARNING` (cooldown=0) | 🟡 주의 | "중복 방지(쿨다운)가 꺼져 있음 — 운영진에 설정 요청" |
| 9 | `queueDepth > 0` 이 5분 지속 | 🟡 주의 | "미전송 {n}건 대기 중 — 인터넷 연결 확인" |
| 10 | 카탈로그 갱신 미적용 배지 존재 / NTP 미확인 | 🟡 주의 | 해당 안내 |
| 11 | 그 외 (모든 리더 ACTIVE·CONNECTED, 큐 0) | 🟢 정상 | "정상 운영 중 · 오늘 체크인 {n}건" |

관측 모드에서 status.json 이 15초 이상 갱신되지 않으면: 🔴 "서비스가 응답하지
않음 — 서비스 상태 확인" (WriteStatus 하트비트 5s 의 3배).

### 2.3 대시보드 — 리더 카드

카드 필드: 리더 id · 선택된 세션 이름(✓/⚠) · 연결 상태 · 게이트 상태(번역문) ·
마지막 태그 시각 · 마지막 성공 시각 · 대기 큐 n건 · [세션 선택] [재개] 버튼.

**상태 → 조치 문장 번역표** (`gateState`, `preflight.go` 의 기존 reason 문구 계승):

| 내부 상태 | 표시 문구 | 조치 안내(1줄) |
| --- | --- | --- |
| `PREFLIGHT_PENDING` | 서버 확인 대기 | 잠시 후 자동 진행됩니다 |
| `PREFLIGHT_RETRY` | 서버 연결 재시도 중 | 인터넷 연결을 확인하세요 |
| `ACTIVE` | 정상 | — |
| `ACTIVE_WARNING` | 정상 (중복 방지 꺼짐) | 운영진에게 쿨다운 설정을 요청하세요 |
| `SUSPENDED_TOKEN` | 토큰 회수됨 — 중단 | 새 카탈로그 파일을 받아 "새 토큰으로 재개"를 누르세요 |
| `SUSPENDED_CONFIG` | 서버 응답 이상 — 중단 | 개발팀에 연락하세요 (서버 계약 위반) |
| `SUSPENDED_REBIND` | 세션이 바뀜 — 확인 필요 | 대기 중 기록의 전송/폐기를 선택해 재개하세요 |
| conn `RECONNECTING` | 리더 재접속 중 | 리더 전원과 케이블을 확인하세요. 다른 프로그램(YAT 등)이 리더에 접속해 있으면 종료 후 리더 전원을 재투입하세요 |
| conn `DISCONNECTED` | 리더 연결 끊김 | 〃 |

### 2.4 로그 화면

- SSE 로 실시간 수신, 화면은 최근 2000행 유지(가상 스크롤), 일시정지 버튼.
- 필터: 레벨(info/warn/error), 리더 id, 이벤트명 부분 일치.
- 행 렌더: `HH:MM:SS  [레벨]  이벤트  요약필드` — raw JSON 은 행 클릭 시 펼침.
- **마스킹은 서버(GUI 백엔드)에서**: EPC 끝 4자리 `****`, 토큰류 필드는 코어
  로깅이 이미 배제하지만 GUI 송출 직전 금칙 키 재검사(이중 방어). 브라우저에는
  마스킹된 문자열만 도달한다.

### 2.5 세션 화면 · 2.6 현장 점검 화면

세션 화면은 §3(확정 설계) 그대로. 현장 점검 화면은 §5 마법사 상태 기계를 렌더.

---

## 3. 세션 카탈로그 — 선택·토큰 자동 매칭 (FR-17~20 확정 설계)

### 3.1 개념

- 운영자는 GUI에서 **세션 이름을 고를 뿐**, 토큰을 보거나 복사하지 않는다.
- 세션 목록의 원천은 **사람이 갱신하는 파일 1개**(`pulse-sessions.json`).
  congkong-v3 콘솔이 §3.2 양식으로 내보내고, 운영자는 그 파일을 지정 위치에
  덮어쓰기만 하면 목록에 반영된다.

```text
콩콩 콘솔 (congkong-v3) ──내보내기──▶ pulse-sessions.json ──사람이 복사──▶ 노트북
                                                │
GUI: 파일 로드·감시 ──세션 선택──▶ 토큰 자동 매칭 ──▶ config.json 반영 ──▶ preflight 교차 검증
```

### 3.2 파일 양식 v1 (SSOT — congkong-v3 내보내기 계약)

> 서버 측 구현 브리프: `congkong-v3/docs/features/pulse/pulse-sessions-export-brief.ko.md`
> — 양식 변경 시 두 문서를 함께 갱신한다.

**파일명** `pulse-sessions.json`, **인코딩** UTF-8(BOM 없음), **형식** JSON.

```json
{
  "version": 1,
  "eventName": "콩콩 컨퍼런스 - 이벤트 테크 리더",
  "exportedAt": "2026-09-02T10:00:00+09:00",
  "sessions": [
    {
      "id": "session1",
      "name": "세션1",
      "unitName": "Session 1",
      "tokenLabel": "Gate A",
      "pulseToken": "8623c9a7…(64자리 소문자 hex 전문)…",
      "issuedAt": "2026-09-01T15:14:56+09:00"
    }
  ]
}
```

| 필드 | 필수 | 형식·규칙 |
| --- | --- | --- |
| `version` | ✅ | 정수 `1`. 다른 값이면 미들웨어가 파일 전체 거부(오류 표시) |
| `eventName` | ✅ | 이벤트 표시명. preflight `eventName` 과 대조에 사용 |
| `exportedAt` | ✅ | RFC3339. 화면에 "카탈로그 기준 시각"으로 표시(낡은 파일 감지) |
| `sessions[]` | ✅ | 1개 이상. 배열 순서대로 표시 |
| `sessions[].id` | ✅ | 부스 관리코드. **안정 식별자** — config `sessionId` 에 저장되는 값. 파일 내 유일해야 함 |
| `sessions[].name` | ✅ | 부스 이름(표시용). preflight `boothName` 과 대조 |
| `sessions[].unitName` | ✅ | 연결 유닛 이름. preflight `unitName` 과 대조 |
| `sessions[].tokenLabel` | — | 토큰 라벨(있으면 목록에 보조 표시). 빈 문자열 허용 |
| `sessions[].pulseToken` | ✅ | **소문자 64자리 hex 전문**. 형식 위반 시 해당 항목만 목록에서 제외하고 경고 |
| `sessions[].issuedAt` | — | RFC3339 발급 시각(보조 표시) |

**호환 규칙**: 미들웨어는 알 수 없는 필드를 무시(관용 파싱). 파괴적 변경 시에만
`version` 을 올린다. 콘솔 기존 xlsx 2번째 시트와 컬럼 1:1 대응.

**보안 규칙**: 실토큰 전문 파일 — 저장소·메신저 금지, `.gitignore` 등록(완료),
GUI 는 로드 시 파일 ACL 이 느슨하면 경고.

### 3.3 파일 위치와 반영(덮어쓰기 = 즉시 반영)

- 위치: config 의 선택 필드 `sessionsFile` (기본: config 옆 `pulse-sessions.json`).
- **상시 감시**: GUI 생존 동안 mtime 2초 폴링 — 덮어쓰면 즉시 재로드, 토스트 +
  목록 갱신. mtime 변경 후 200ms 디바운스, 파싱 실패 시 1s 후 1회 재시도.
- **Downloads 자동 감지**: `%USERPROFILE%\Downloads` 의 `pulse-sessions*.json`
  신규 생성 감지 → "가져올까요?" 확인 → `sessionsFile` 위치로 **이동**(원본 삭제).
  자동 적용 없음(운영자 확인 필수).
- 파일 없음: 안내 + 양식 예시. 파싱 오류: 마지막 정상본 유지 + 오류 배너.

### 3.4 선택 → 자동 매칭 → 검증 시퀀스

```text
운영자: 리더(gate-a) 행에서 [세션 선택] → 목록에서 "세션1" 클릭
GUI:
 1. 카탈로그에서 id=session1 의 pulseToken 을 꺼낸다 (화면 표시는 fingerprint 8자만)
 2. config.json 갱신: readers[gate-a].pulseToken = 토큰, .sessionId = "session1"
    (원자적 쓰기: temp + rename. 실패 시 원본 불변)
 3. 호스팅 모드면 코어 재기동(ADR-007), 관측 모드면 "서비스 재시작 필요" 안내 + 버튼
 4. preflight 결과 대조:
    - OK & boothName==name & unitName==unitName & eventName==eventName → "세션1 ✓"
    - 이름 불일치 → "카탈로그와 서버 정보가 다릅니다 — 파일이 낡았거나 다른
      이벤트의 파일입니다. 콘솔에서 다시 내보내 교체하세요."
    - 404 → "이 세션의 토큰이 회수되었습니다 — 콘솔에서 재발급 후 파일을 갱신하세요."
```

- 같은 세션의 복수 리더 지정: 확인 후 허용(중복 경고만).
- pending 큐가 있는 리더의 세션 변경은 차단 → `queue resume` 화면 안내.

### 3.5 코어/설정의 additive 변경 (카탈로그 관련)

| 변경 | 내용 | 호환성 |
| --- | --- | --- |
| config `readers[].sessionId` | 선택 필드. 코어는 로드·보존만(표시·대조용) | 기존 config 유효 |
| config `sessionsFile` | 선택 필드. GUI 만 사용 | 〃 |
| status.json `readers[].sessionId` | 관측 모드 표시용 기록 | 필드 추가 |

### 3.6 오류 처리 표

| 상황 | GUI 동작 |
| --- | --- |
| 파일 없음 / 경로 오류 | 안내 + 양식 예시. 기존 config 토큰으로 계속 동작 |
| `version` ≠ 1 | 전체 거부 + "프로그램 업데이트 또는 파일 재내보내기 필요" |
| 토큰 형식 위반 항목 | 해당 항목만 제외 + 경고 |
| `id` 중복 | 뒤의 항목 무시 + 경고 |
| 카탈로그 토큰 ≠ config 토큰 (같은 sessionId) | "카탈로그가 갱신됨" 배지 + 원클릭 재적용(확인 후 재기동) |
| TOKEN_REVOKED 중단 + 새 카탈로그에 같은 sessionId 새 토큰 | **"새 토큰으로 재개" 원클릭** — 토큰 반영 → `queue resume`(send/discard 선택) → 재기동 |
| exportedAt 7일 초과 과거 | 정보 배너 |

### 3.7 테스트

- 단위: 양식 파서 각 분기, 원자적 config 쓰기, **토큰 전문이 API 응답·SSE·DOM
  문자열에 없음을 테스트로 고정**
- 통합: 선택→config→가짜 서버 preflight 대조(일치/불일치/404)
- 마법사 0단계에 카탈로그 존재·버전·ACL 점검 포함

---

## 4. 로컬 HTTP API 계약 (GUI 프론트 ↔ GUI 백엔드)

### 4.1 공통 규칙

- 바인드 `127.0.0.1:0`(ephemeral). 기동 시 32byte 난수 **nonce** 생성, 모든 경로는
  `/{nonce}/...` 아래. 브라우저는 `http://127.0.0.1:{port}/{nonce}/` 로 오픈.
- 모든 요청에서 `Host == 127.0.0.1:{port}` 검증, 상태 변경 POST 는 `Origin` 동일
  출처 검증 + JSON body `{"confirm": true}` (UI 확인 다이얼로그 통과 표식).
- 응답 봉투: 성공 `{"ok":true, "data":…}` / 실패 `{"ok":false, "error":{"code":"…","message":"한국어 문구"}}`.
- CSP `default-src 'self'`, 외부 요청 0 (오프라인 venue).
- **토큰 전문은 어떤 응답에도 없다** — 카탈로그 API 도 fingerprint(8자)만.

### 4.2 엔드포인트

| 메서드·경로 | 용도 | 응답 data 요약 |
| --- | --- | --- |
| `GET /api/state` | 종합 상태 (2s 폴링 fallback 겸용) | §4.3 State 객체 |
| `GET /api/events` | SSE 스트림 | §4.4 이벤트 |
| `GET /api/catalog` | 세션 목록 | `{eventName, exportedAt, stale, sessions[]{id,name,unitName,tokenLabel,issuedAt,fingerprint,revokedSuspect}}` |
| `POST /api/catalog/refresh` | 수동 재로드 | 갱신된 catalog |
| `POST /api/catalog/import` | Downloads 감지 파일 가져오기 확정 | 〃 |
| `POST /api/readers/{id}/session` | `{sessionId, confirm}` 세션 매칭 적용 | 적용 결과 + 재기동 여부 |
| `POST /api/readers/{id}/resume` | `{pending:"send"\|"discard", confirm}` | resume 결과 (기존 `app.QueueResume` 재사용) |
| `POST /api/control/core` | `{action:"restart"\|"stop", confirm}` (호스팅 모드) | — |
| `POST /api/control/service` | `{action:"start"\|"stop", confirm}` (관측 모드, 자기 exe CLI + UAC) | — |
| `POST /api/wizard/start` | `{steps:[0..5], readerId?, allowRealCheckin:false}` | wizard run id |
| `POST /api/wizard/confirm` | 3단계 실체크인 진행 확인 `{confirm}` | — |
| `POST /api/wizard/abort` | 중단 | — |
| `GET /api/wizard/report` | 마지막 리포트 (redacted) | §5.4 리포트 객체 |
| `GET /api/meta` | 버전, cfg fingerprint, 모드, 경로들 | — |

### 4.3 State 객체 (status.json v2 의 상위 집합)

```json
{
  "mode": "hosting",
  "signal": "green",
  "headline": "정상 운영 중 · 오늘 체크인 152건",
  "senderState": "RUNNING",
  "queueDepth": 0,
  "oldestCheckedAt": null,
  "ntp": {"checked": true, "skewSec": 1},
  "updatedAt": "2026-09-02T11:00:03+09:00",
  "readers": [{
    "id": "gate-a", "sessionId": "session1", "sessionName": "세션1",
    "sessionVerified": true,
    "connState": "CONNECTED", "gateState": "ACTIVE",
    "gateText": "정상", "actionText": "",
    "lastTagAt": "…", "lastSuccessAt": "…", "pending": 0,
    "boothName": "세션1", "unitName": "Session 1", "cooldownSec": 60
  }],
  "catalog": {"loaded": true, "exportedAt": "…", "pendingImport": false},
  "todaySuccess": 152
}
```

관측 모드에서는 status.json(v2, §6.1)에서 파생하고 `signal` 계산은 GUI 가 수행.

### 4.4 SSE 이벤트

`event:` 타입 4종, `data:` 는 JSON 1줄:

| 타입 | 페이로드 | 발생원 |
| --- | --- | --- |
| `log` | 마스킹된 코어 JSONL 1행 | 호스팅: ring buffer / 관측: 로그 파일 tail |
| `state` | §4.3 State 전체 (변경 시 + 5s 하트비트) | gates 변경·status 갱신 |
| `catalog` | `{reason:"reloaded"\|"parse_error"\|"download_found"}` | 파일 감시 |
| `wizard` | §5.3 단계 진행 객체 | 마법사 실행기 |

브라우저 미접속 시에도 코어는 정상 동작(SSE 는 관측 창구일 뿐).

---

## 5. 현장 점검 마법사 상세

### 5.1 불변식 (plan FR-16)

(a) 운영 큐·gate 테이블 불변경 — 2단계는 no-op `gate.Persister`, 3·4단계는
`{dataDir}_selftest` 별도 dataDir. (b) 실서버 체크인 생성(3a)은 명시적 확인 후에만.
(c) 마법사 실행 중 호스팅 코어는 **일시 정지하지 않는다** — 운영과 병행 가능해야
하므로 1단계(리더 TCP)는 코어가 리더를 점유 중이면 "운영 중 — 코어 연결 상태로
대체 판정"으로 자동 전환한다(리더 단일 소켓 제약).

### 5.2 상태 기계

```text
IDLE ──start──▶ RUNNING(step k) ──단계 종료──▶ 다음 step … ──▶ SUMMARY
                   │        └─ step 실패: 결과 기록 후 계속/중단 규칙(§5.3) 적용
                   └──abort──▶ SUMMARY(중단 표기)
SUMMARY ──[리포트 저장/다시 실행]──▶ IDLE
동시 실행 1개만. 실행 중 세션 변경·resume 버튼은 잠금.
```

### 5.3 단계 정의 (판정 기준 확정)

| # | 단계 | 실행 내용 (재사용 코드) | PASS | WARN | FAIL → 이후 |
| --- | --- | --- | --- | --- | --- |
| 0 | 설정·환경 | `config.Load`, `health.CheckDiskDir`, `health.CheckNTP`, config·카탈로그 ACL 검사, 절전 정책 조회(`powercfg /q` 파싱) | 전부 통과 | ACL 느슨/절전 켜짐/카탈로그 없음 | 설정 무효·dataDir 쓰기 불가·NTP skew≥240s → **중단 권고**(계속 가능) |
| 1 | 리더 연결 | `session.Probe`(§6.2) 리더별. 코어 점유 시 connState 로 대체 | 5s 내 INIT 완료+펌웨어 획득 (또는 코어 CONNECTED) | RTT>2s | timeout → 안내문("다른 프로그램 점유·전원 재투입") 표시, 계속 가능 |
| 2 | 서버 preflight | `sender.Preflight` + **no-op Persister** + 임시 Registry, 리더별 | ACTIVE & cooldownSec>0, booth/unit/event 가 카탈로그 선택과 일치 | cooldown=0 / 이름 불일치 | 404·5xx 지속 → 3단계 비활성 |
| 3a | 실태그 E2E | **확인 다이얼로그** 후 `_selftest` dataDir 로 미니 파이프라인 기동, 운영자 태그 스캔 대기(60s) | 1차 200 + 재스캔 409 | 409 만 관측(이미 체크인된 태그) | 400/404/timeout → 원인문 표시 |
| 3b | 무해 모드 | `replay.Runner` 로 미등록 EPC 1건 송신 (`_selftest`) | 404 BARCODE_NOT_FOUND | — | 그 외 응답 |
| 4 | 오프라인 재생(선택) | `_selftest` 에서 apiHost 를 도달 불가 포트로 바꾼 사본 config 로 1건 큐 적재 → 원 config 복귀 후 드레인 | 적재 확인 + 복구 후 원 checkedAt 으로 200/409 | — | 드레인 실패 |
| 5 | 요약 | §5.4 리포트 생성 | — | — | — |

3a 확인 다이얼로그 문구: "실제 서버에 체크인 기록이 생성됩니다. 반드시 시험용
태그를 사용하세요. 진행할까요?" — 기본 버튼은 취소.

### 5.4 리포트

- 형식: 사람용 `.txt` + 기계용 `.json` 쌍. 파일명 `field-check-{YYYYMMDD-HHMM}`.
- 내용: 단계별 pass/warn/fail + 핵심 관측값(펌웨어, RTT, boothName, HTTP status,
  skewSec) + 프로그램 버전 + cfg fingerprint.
- **Redaction**: 직렬화 직전 `logging` 의 금칙 필드 정책(exported)을 통과시키고,
  EPC 끝 4자 마스킹. **토큰·EPC 전문 부재를 단위 테스트로 고정** (리포트 생성기에
  실토큰 주입 → 출력 바이트에 전문 미포함 assert).
- 저장 위치: `{dataDir}\reports\`. GUI 에서 "파일 위치 열기" 버튼.

### 5.5 `_selftest` 격리 규칙

- 경로: `{dataDir}_selftest` (운영 dataDir 형제). 마법사 시작 시 생성, SUMMARY
  진입 시 큐 DB 삭제(리포트만 보존). 운영 dataDir 와 lock 이 분리되므로 호스팅
  코어와 병행 실행 가능.
- `_selftest` 실행은 동일 토큰을 쓰므로 서버 관점 트래픽은 발생 — 3b/4 는
  미등록 EPC 만 사용해 체크인 레코드를 만들지 않는다.

---

## 6. 코어 additive 변경 상세

### 6.1 status.json v2 스키마

기존 필드 유지 + 추가(굵게). 기존 소비자(`status` CLI)는 필드 추가에 영향 없음.

```json
{
  "updatedAt": "RFC3339",
  "senderState": "RUNNING|HALTED",
  "queueDepth": 0,
  "oldestCheckedAt": "RFC3339|생략",
  "pid": 1234, "version": "v0.2.0", "mode": "service|hosting|cli",
  "startedAt": "RFC3339",
  "ntp": {"checked": true, "skewSec": 1},
  "todaySuccess": 152,
  "readers": [{
    "id": "gate-a", "gateState": "...", "gateReason": "...",
    "eventName": "...", "boothName": "...", "unitName": "...", "cooldownSec": 60,
    "connState": "CONNECTED|RECONNECTING|DISCONNECTED",
    "sessionId": "session1",
    "lastTagAt": "RFC3339", "lastSuccessAt": "RFC3339",
    "pending": 0
  }]
}
```

- `lastTagAt`/`lastSuccessAt` **실기록**(현재 선언만 존재 — M1 필수), `connState`
  는 session 이 gates 와 별개로 app 에 보고하는 원자값.
- 기록 주기: gates/conn 변경 이벤트 구동 + 5s 하트비트 (`WriteStatus` 원자성 유지).

### 6.2 `session.Probe`

```go
// Probe 는 접속→INIT(버전 조회까지)→Stop→종료의 1회 왕복 진단이다.
// Run 과 달리 재접속 루프·Inventory 진입을 하지 않는다.
func Probe(ctx context.Context, cfg Config, dial DialFunc) (ProbeResult, error)
type ProbeResult struct { Firmware string; RTT time.Duration }
```

### 6.3 로그 ring buffer (GUI Echo 탭)

```go
// logging.Echo 에 꽂는 non-blocking writer. 코어 방향으로 절대 블록하지 않는다.
gui.NewLogRing(capacity 4096행, drop-oldest) — Write()는 복사 후 즉시 반환,
별도 고루틴이 SSE 구독자에게 flush. 드랍 발생 시 카운터를 state 에 노출.
```

`logging.Log` 가 mutex 안에서 Echo 를 동기 호출하므로 이 완충은 필수 조건이다.

### 6.4 기타

- `app.AcquireLock(dataDir)` exported 헬퍼(모드 중재용, 기존 `acquireLock` 위임).
- `gate.Registry` 변경 통지: `Watch() <-chan struct{}` (coalesced) 추가.
- config: `sessionsFile`, `readers[].sessionId` (§3.5). 그 외 스키마 불변.

---

## 7. 트레이·수명주기

### 7.1 기동 흐름

```text
exe 기동(인자 없음) → 단일 GUI 인스턴스 확인(gui.lock; 이미 있으면 기존 브라우저 재오픈 후 종료)
→ config 로드(없으면 온보딩 화면으로 진행) → app.lock 시도
→ 성공: 호스팅 모드, app.Run goroutine 기동 / 실패: 관측 모드
→ 127.0.0.1 HTTP + nonce → 트레이 아이콘 → 기본 브라우저 오픈
인자 있음 → AttachConsole → 기존 CLI 동작 (GUI 미기동)
```

### 7.2 트레이 메뉴

`상태: 정상 운영 중`(비활성, 신호등 동기화) / `화면 열기` / `현장 점검…` /
`서비스 모드 안내` / 구분선 / `종료`

### 7.3 종료

- 브라우저 창 닫기 = 아무 일 없음(트레이가 수명 소유).
- 트레이 `종료`: 호스팅 모드면 확인 다이얼로그("체크인 수집이 중단됩니다") →
  코어 ctx cancel → **grace 10s**(서비스와 동일) 내 in-flight 정리 → HTTP 종료 →
  트레이 제거. 관측 모드는 즉시 종료(서비스는 계속 돈다는 안내 1줄).
- Windows 로그아웃/종료 시그널도 동일 grace 경로.

---

## 8. 설치 패키지 (Inno Setup — plan M4)

| 설치 단계 | 내용 |
| --- | --- |
| 파일 | `%ProgramFiles%\CongKong\RFID Middleware\rfid-middleware.exe` + README |
| 데이터 | `%ProgramData%\CongKong\RFIDMiddleware` 생성, `icacls` 로 SYSTEM/Administrators 제한 (config·카탈로그가 놓일 곳) |
| config | 기존 파일 있으면 보존, 없으면 example 배치 |
| 서비스 | 체크박스 "무인 상주(서비스) 등록" → `service install` 실행 (기본 켬) |
| 바로가기 | 시작 메뉴 + 바탕화면 (GUI 기동) |
| 절전 | 체크박스 "행사용 전원 설정 적용" → `powercfg` 절전/최대절전 해제 |
| 방화벽 | 불필요(127.0.0.1 바인드) — 설치 문서에 명시만 |
| 제거 | 서비스 중지+해제, 프로그램 삭제. **dataDir(큐·로그)는 보존**하고 안내 |

빌드: GitHub Actions windows 러너에서 `iscc` → `rfid-middleware-setup-{ver}.exe`
+ SHA-256 을 Release 자산으로. zip(무설치)도 병행 산출.

---

## 9. M0 스파이크 반영 대기 항목

- `-H=windowsgui` + AttachConsole 실기기 UX (실패 시 2-exe 분리 fallback)
- cgo-free systray 라이브러리 확정 (실패 시 트레이 제거, 창 상주로 대체)
- 브라우저 자동 오픈·방화벽 무프롬프트 확인
- Downloads 감시의 OneDrive 리다이렉션 경로(`KnownFolders`) 처리 확인

## 10. 수용 기준 (Gap 분석 기준점)

| ID | 시나리오 | 근거 FR |
| --- | --- | --- |
| G1 | 더블클릭 기동 → 트레이 + 브라우저 대시보드, cmd 창 없음 | FR-01,08 |
| G2 | 서비스 실행 중 기동 → 관측 모드 배지 + 읽기 전용 동작 | FR-02 |
| G3 | 신호등이 §2.2 규칙대로 전이 (시나리오 6종 시뮬레이션) | FR-03 |
| G4 | 태그 스캔 → 500ms 내 카드·로그 반영 (호스팅) | NFR 성능 |
| G5 | 카탈로그 덮어쓰기 → 2s+α 내 목록 갱신 토스트; Downloads 감지 → 가져오기 | FR-17 |
| G6 | 세션 선택 → 토큰 매칭 → preflight ✓ / 불일치 경고 / 404 안내 3분기 | FR-18 |
| G7 | 토큰 전문이 API·SSE·DOM·리포트 어디에도 없음 (자동 테스트) | FR-20, NFR 보안 |
| G8 | 마법사 0~3b 완주 + redacted 리포트 저장 (운영 gate·큐 불변 검증 포함) | FR-10~16 |
| G9 | 트레이 종료 → grace 10s 내 정리, 재기동 시 큐 복구 | FR-07 |
| G10 | 기존 CLI 전 서브커맨드 회귀 없음, `go test -race`·교차빌드 그린 | FR-08 |

## Version History

| Version | Date | Changes |
|---------|------|---------|
| 0.9 | 2026-09-02 | 화면·API 계약·마법사·코어 변경·수명주기·설치 패키지 상세화 (M0 반영 전 완성본) |
| 0.3 | 2026-09-02 | 카탈로그 즉시 반영·Downloads 가져오기 강화 |
| 0.2 | 2026-09-02 | 세션 카탈로그(양식 v1·선택·자동 매칭) 확정 설계 |
| 0.1 | 2026-09-02 | 골격 (CTO 결정 요약) |
