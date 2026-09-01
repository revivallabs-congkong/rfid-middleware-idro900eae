# rfid-middleware-gui Design Document

> **Summary**: GUI(트레이 + 로컬 웹 UI) 전환 설계 완성본. 화면·API 계약·현장 점검
> 마법사·코어 additive 변경·수명주기·설치 패키지를 확정한다. GUI 기술 세부
> (§9)만 M0 스파이크 결과 대기.
>
> **Plan**: `docs/01-plan/features/rfid-middleware-gui.plan.md`
> **Date**: 2026-09-02 · **Status**: Draft v0.95 (design-validator C-1~C-7·W·U 반영)

---

## 1. 아키텍처 골격 (plan §6 확정 사항 요약)

- 단일 exe. 기동 시 `app.lock` 중재로 **호스팅 모드**(GUI가 `app.Run` in-process
  소유) / **관측 모드**(서비스 실행 중, 읽기 전용+제어는 CLI 재실행) 분기.
- 렌더링: go:embed 정적 자산 + `127.0.0.1` ephemeral 포트 HTTP + 기본 브라우저.
  트레이 상주(cgo-free). 로그 스트림은 drop-oldest ring buffer 경유(§6.3).
- 코어 변경은 additive 만 (§6). 파이프라인 불변식 불변. `app.Run` 은 시그니처
  유지하되 **Options 에 선획득 잠금 주입 필드가 추가**된다(§6.5 — GUI 호스팅의
  전제 조건).

### 1.1 용어·식별자 정의 (혼동 방지)

| 용어 | 정의 |
| --- | --- |
| **토큰 fingerprint** | `domain.Secret.Fingerprint()`(SHA-256 기반 24자 hex)의 **앞 8자**. 기존 CLI 관례(`main.go` cmdValidate)와 동일 |
| **cfg fingerprint** | config.json 을 정규화(토큰 필드는 각자의 토큰 fingerprint 로 치환)한 JSON 바이트의 SHA-256 앞 8자 hex. 토큰 전문은 해시 입력에도 넣지 않는다 |
| 마법사 단계 id | 문자열 `"0" "1" "2" "3a" "3b" "4" "5"` (API·화면·리포트 공통) |

---

## 2. 화면 상세

### 2.1 공통 레이아웃

```text
┌────────────────────────────────────────────────────────────┐
│ ● 정상 운영 중        [호스팅 모드]   v0.2.0 · cfg 5f3a9c1e │ ← 상단바(신호등·모드·버전·cfg fingerprint)
├──────────┬─────────────────────────────────────────────────┤
│ 대시보드 │  (선택된 탭 내용)                                │
│ 세션     │                                                 │
│ 로그     │                                                 │
│ 현장점검 │                                                 │
└──────────┴─────────────────────────────────────────────────┘
토스트: 우하단 (카탈로그 갱신·오류 등), 5s 자동 소멸(오류는 수동 닫기)
```

**온보딩(최초 실행) 화면**: `config.json` 이 없으면 대시보드 대신 표시.
내장 템플릿(config.example 동등)을 기반으로 ① 카탈로그 가져오기(§3.3 Downloads
감지 포함) ② 세션 선택 ③ 리더 addr 확인(기본 `192.168.9.6:5578`) 3단계로
config.json 을 생성한다. 설정 "편집기"는 여전히 범위 외(plan §2.2) — 온보딩은
**최초 생성 마법사**로 한정하며 기존 config 가 있으면 절대 나타나지 않는다.

### 2.2 신호등 판정 규칙 (우선순위 위에서부터, 첫 일치 적용)

| 순위 | 조건 (State §4.3 필드 기준) | 신호등 | 헤드라인 문구 |
| --- | --- | --- | --- |
| 1 | `senderState != "RUNNING"` (실제 리터럴: `"HALTED_REQUEST_BUG"`) | 🔴 오류 | "전송이 전역 중단됨 — 프로그램 결함, 개발팀 연락 필요. 스캔은 계속 저장되는 중" |
| 2 | 어느 리더든 `gateState == SUSPENDED_TOKEN` | 🔴 오류 | "'{리더}' 토큰이 회수됨 — 새 카탈로그 파일로 재개 필요" |
| 3 | 어느 리더든 `SUSPENDED_CONFIG` / `SUSPENDED_REBIND` | 🔴 오류 | 조치 문장 표(§2.3) |
| 4 | 어느 리더든 `connState == "DISCONNECTED"` 이고 `now - connSince > 30s` | 🔴 오류 | "'{리더}' 리더 연결 끊김 — 전원·케이블 확인" |
| 5 | `ntp.skewSec` 존재하고 `\|skewSec\| ≥ 240` | 🔴 오류 | "PC 시계가 서버와 4분 이상 어긋남 — 체크인이 거부될 수 있음, 시간 동기화 필요" |
| 6 | `oldestCheckedAt` 경과가 `queueMaxAgeHours × 0.9` 초과 (코어 경고 `app.go` 의 maxAge×9/10 과 동일 기준) | 🔴 오류 | "미전송 기록이 곧 만료됨 — 네트워크 복구 필요" |
| 7 | 어느 리더든 `PREFLIGHT_RETRY`, 또는 `DISCONNECTED` 30초 이내 | 🟡 주의 | "서버/리더 연결 재시도 중" |
| 8 | 어느 리더든 `ACTIVE_WARNING` | 🟡 주의 | "중복 방지(쿨다운)가 꺼져 있음 — 운영진에 설정 요청" |
| 9 | `queueDepth > 0` 이고 `now - queueNonEmptySince > 5m` | 🟡 주의 | "미전송 {n}건 대기 중 — 인터넷 연결 확인" |
| 10 | `catalog.updateAvailable == true` / `ntp.checked == false` | 🟡 주의 | 해당 안내 |
| 11 | 그 외 | 🟢 정상 | "정상 운영 중 · 이번 실행 체크인 {n}건" |

관측 모드에서 status.json `updatedAt` 이 15초 이상 과거이면: 🔴 "서비스가 응답하지
않음 — 서비스 상태 확인" (하트비트 5s 의 3배).

### 2.3 대시보드 — 리더 카드

카드 필드: 리더 id · 선택된 세션 이름(✓/⚠) · 연결 상태 · 게이트 상태(번역문) ·
마지막 태그 시각 · 마지막 성공 시각 · 대기 큐 n건 · [세션 선택] [재개] 버튼.

**상태 → 조치 문장 번역표**:

| 내부 상태 | 표시 문구 | 조치 안내(1줄) |
| --- | --- | --- |
| `PREFLIGHT_PENDING` | 서버 확인 대기 | 잠시 후 자동 진행됩니다 |
| `PREFLIGHT_RETRY` | 서버 연결 재시도 중 | 인터넷 연결을 확인하세요 |
| `ACTIVE` | 정상 | — |
| `ACTIVE_WARNING` | 정상 (중복 방지 꺼짐) | 운영진에게 쿨다운 설정을 요청하세요 |
| `SUSPENDED_TOKEN` | 토큰 회수됨 — 중단 | 새 카탈로그 파일을 받아 "새 토큰으로 재개"를 누르세요 |
| `SUSPENDED_CONFIG` | 서버 응답 이상 — 중단 | 개발팀에 연락하세요 (서버 계약 위반) |
| `SUSPENDED_REBIND` | 세션이 바뀜 — 확인 필요 | 대기 중 기록의 전송/폐기를 선택해 재개하세요 |
| conn `DISCONNECTED` (≤30s) | 리더 재접속 중 | 잠시 기다리세요 — 자동 재접속합니다 |
| conn `DISCONNECTED` (>30s) | 리더 연결 끊김 | 리더 전원·케이블을 확인하세요. 다른 프로그램(YAT 등)이 리더에 접속해 있으면 종료 후 리더 전원을 재투입하세요 |

connState 는 `CONNECTED`/`DISCONNECTED` 2값 + `connSince`(전이 시각)로 표현하고
"재접속 중" 표현은 GUI 가 30초 경과로 구분한다 (코어의 무한 재접속 루프에는
별도 RECONNECTING 상태가 없음 — §6.6).

### 2.4 로그 화면

- SSE 실시간 수신, 최근 2000행(가상 스크롤), 일시정지 버튼.
- 필터: 레벨(**debug**/info/warn/error), 리더 id, 이벤트명 부분 일치.
- 행 렌더: `HH:MM:SS  [레벨]  이벤트  요약필드` — raw JSON 은 행 클릭 시 펼침.
- **마스킹은 GUI 백엔드에서**: EPC 끝 4자리 `****`, 송출 직전 `logging.Redact`
  (§6.7) 재통과(이중 방어). 브라우저에는 마스킹된 문자열만 도달.
- 관측 모드 tail: `logs/` 에서 사전순 최신 `middleware-*.jsonl` 을 fd 로 follow,
  5초마다 더 새로운 파일명 존재를 확인해 회전 시 전환(전환 시점 이후 행만 송출,
  중복 없음).

### 2.5 세션 화면 · 2.6 현장 점검 화면

세션 화면은 §3 그대로. 현장 점검 화면은 §5 상태 기계를 렌더.

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
> (합의 상태: 브리프 전달됨, 서버 구현 대기) — 양식 변경 시 두 문서를 함께 갱신한다.

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
      "pulseToken": "(소문자 64자리 hex 전문 — 예시 생략)",
      "issuedAt": "2026-09-01T15:14:56+09:00"
    }
  ]
}
```

| 필드 | 필수 | 형식·규칙 |
| --- | --- | --- |
| `version` | ✅ | 정수 `1`. 다른 값이면 파일 전체 거부(오류 표시) |
| `eventName` | ✅ | 이벤트 표시명. preflight `eventName` 과 대조 |
| `exportedAt` | ✅ | RFC3339. "카탈로그 기준 시각" 표시(낡은 파일 감지) |
| `sessions[]` | ✅ | 1개 이상. 배열 순서대로 표시 |
| `sessions[].id` | ✅ | 부스 관리코드. 안정 식별자(config `sessionId` 저장값). 파일 내 유일 |
| `sessions[].name` | ✅ | 부스 이름. preflight `boothName` 과 대조 |
| `sessions[].unitName` | ✅ | 연결 유닛 이름. preflight `unitName` 과 대조 |
| `sessions[].tokenLabel` | — | 보조 표시. 빈 문자열 허용 |
| `sessions[].pulseToken` | ✅ | 소문자 64자리 hex 전문. 형식 위반 시 해당 항목만 제외+경고 |
| `sessions[].issuedAt` | — | RFC3339 발급 시각(보조 표시) |

**호환 규칙**: 카탈로그 파서는 알 수 없는 필드 무시(관용 파싱 — 서버가 필드 추가
가능). 이는 **카탈로그 파일에만** 적용된다 — config.json 은 지금처럼 엄격
(`DisallowUnknownFields`) 유지. 파괴적 변경 시에만 `version` 을 올린다.
콘솔 기존 xlsx 2번째 시트와 컬럼 1:1 대응.

**보안 규칙**: 실토큰 전문 파일 — 저장소·메신저 금지, `.gitignore` 등록(완료),
GUI 는 로드 시 파일 ACL 이 느슨하면 경고. 화면·API 에는 토큰 fingerprint(§1.1)만.

### 3.3 파일 위치와 반영(덮어쓰기 = 즉시 반영)

- 위치: config 의 선택 필드 `sessionsFile` (기본: config 옆 `pulse-sessions.json`).
- **상시 감시**: GUI 생존 동안 mtime 2초 폴링 — 덮어쓰면 즉시 재로드, 토스트 +
  목록 갱신. mtime 변경 후 200ms 디바운스, 파싱 실패 시 1s 후 1회 재시도.
- **Downloads 자동 감지**: `KnownFolders` API 로 실제 Downloads 경로 해석
  (OneDrive 리다이렉션 대응 — M0 확인 항목). `pulse-sessions*.json` 신규 생성
  감지 → "가져올까요?" 확인 → `sessionsFile` 위치로 **이동**(원본 삭제).
  자동 적용 없음(운영자 확인 필수).
- 파일 없음: 안내 + 양식 예시. 파싱 오류: 마지막 정상본 유지 + 오류 배너.

### 3.4 선택 → 자동 매칭 → 검증 시퀀스

```text
운영자: 리더(gate-a) 행에서 [세션 선택] → 목록에서 "세션1" 클릭
GUI:
 1. 중복 검사: 그 세션이 이미 다른 리더에 지정돼 있으면 선택 거부
    ("한 세션(토큰)은 한 리더에만 지정할 수 있습니다" — 코어 config 검증이
    리더 간 토큰 중복을 거부하는 정책과 일치. 복수 게이트가 필요하면 콘솔에서
    부스별 토큰을 추가 발급해 카탈로그에 별도 항목으로 내려받는다)
 2. config 갱신(§6.8 writer): readers[gate-a].pulseToken·sessionId 반영.
    쓰기 전 config.json.bak 백업 → temp+rename 원자적 교체
 3. 적용:
    - 호스팅 모드: 코어 graceful 정지(grace 10s) → 재기동. 재기동 실패 시
      .bak 롤백 후 재기동 + 오류 표시 (성공 시 .bak 삭제)
    - 관측 모드: "서비스 재시작 필요" 안내 + [재시작] 버튼(UAC)
 4. preflight 결과 대조:
    - OK & booth/unit/event 이름 일치 → "세션1 ✓"
    - 이름 불일치 → "카탈로그와 서버 정보가 다릅니다 — 파일이 낡았거나 다른
      이벤트의 파일입니다. 콘솔에서 다시 내보내 교체하세요."
    - 404 → "이 세션의 토큰이 회수되었습니다 — 콘솔에서 재발급 후 파일을 갱신하세요."
```

**pending 큐가 있는 리더의 세션 변경**: 즉시 차단하고 두 갈래 안내 —
"① 대기 {n}건 전송이 끝난 뒤 변경(권장) ② 지금 변경: 정지 → 대기 기록
전송/폐기 선택 → 새 세션 적용 → 재개" (②는 §3.6 재개 오케스트레이션과 동일 경로).

### 3.5 코어/설정의 additive 변경 (카탈로그 관련)

| 변경 | 내용 | 호환성 |
| --- | --- | --- |
| config `readers[].sessionId` | 선택 필드. 코어는 로드·보존만(표시·대조용) | 신규 exe 는 기존 config 유효 |
| config `sessionsFile` | 선택 필드. GUI 만 사용 | 〃 |
| status.json `readers[].sessionId` | 관측 모드 표시용 | 필드 추가 |

⚠️ **역방향 호환 주의**: 새 필드가 쓰인 config 를 **구버전 exe(구 서비스)** 가
읽으면 `DisallowUnknownFields` 로 로드 실패한다(crash loop). 단일 exe 라 평시엔
발생하지 않지만, 다운그레이드 시에는 config 에서 두 필드를 지워야 한다 —
릴리스 노트 고정 문구로 명시하고, 설치 패키지는 exe 교체 시 항상 전체 교체.

### 3.6 오류 처리 표

| 상황 | GUI 동작 |
| --- | --- |
| 파일 없음 / 경로 오류 | 안내 + 양식 예시. 기존 config 토큰으로 계속 동작 |
| `version` ≠ 1 | 전체 거부 + "프로그램 업데이트 또는 파일 재내보내기 필요" |
| 토큰 형식 위반 항목 | 해당 항목만 제외 + 경고 |
| `id` 중복 | 뒤의 항목 무시 + 경고 |
| 카탈로그 토큰 ≠ config 토큰 (같은 sessionId) | `catalog.updateAvailable=true` → 배지 + 원클릭 재적용(§3.4 2~4 재실행) |
| TOKEN_REVOKED 중단 + 새 카탈로그에 같은 sessionId 새 토큰 | **"새 토큰으로 재개" 원클릭**: ① 코어/서비스 정지 ② config 갱신(백업·롤백 §3.4 와 동일) ③ `app.QueueResume`(send/discard 운영자 선택 — suspended 상태이므로 성립) ④ 기동. 관측 모드는 각 단계가 자기 exe CLI + UAC 로 수행됨 |
| exportedAt 7일 초과 과거 | 정보 배너 (`/api/catalog` 의 `stale=true`) |

### 3.7 테스트

- 단위: 양식 파서 각 분기, 원자적 config 쓰기·백업·롤백, **토큰 전문이 API
  응답·SSE·DOM·리포트 어디에도 없음을 테스트로 고정**
- 통합: 선택→config→가짜 서버 preflight 대조(일치/불일치/404), 재개
  오케스트레이션(정지→쓰기→resume→기동) 왕복
- 마법사 0단계에 카탈로그 존재·버전·ACL 점검 포함

---

## 4. 로컬 HTTP API 계약 (GUI 프론트 ↔ GUI 백엔드)

### 4.1 공통 규칙

- 바인드 `127.0.0.1:0`(ephemeral). 기동 시 32byte 난수 **nonce**, 모든 경로는
  `/{nonce}/...` 아래. 브라우저는 `http://127.0.0.1:{port}/{nonce}/` 로 오픈.
- `Host` 검증 + 상태 변경 POST 는 `Origin` 동일 출처 검증 + body `{"confirm":true}`.
- 응답 봉투: `{"ok":true,"data":…}` / `{"ok":false,"error":{"code":"…","message":"한국어"}}`
  (bkit 표준 봉투와 다른 로컬 전용 약식 — 단일 소비자라 의도적 편차).
- **오류 코드 집합**: `invalid_request` `confirm_required` `not_found`
  `conflict_busy`(마법사 실행 중 등) `lock_held` `uac_denied` `catalog_error`
  `core_restart_failed`(롤백 수행됨) `internal`.
- CSP `default-src 'self'`, 외부 요청 0. **토큰 전문은 어떤 응답에도 없다.**

### 4.2 엔드포인트

| 메서드·경로 | 용도 | 응답 data 요약 |
| --- | --- | --- |
| `GET /api/state` | 종합 상태 (폴링 fallback 겸용) | §4.3 State |
| `GET /api/events` | SSE 스트림 | §4.4 |
| `GET /api/catalog` | 세션 목록 | `{eventName, exportedAt, stale, sessions[]{id,name,unitName,tokenLabel,issuedAt,tokenFingerprint,revokedSuspect,assignedReaderId}}` |
| `POST /api/catalog/refresh` | 수동 재로드 | 갱신된 catalog |
| `POST /api/catalog/import` | Downloads 가져오기 확정 | 〃 |
| `POST /api/readers/{id}/session` | `{sessionId, confirm}` §3.4 시퀀스 실행 | 적용 결과·검증 결과 |
| `POST /api/readers/{id}/resume` | `{pending:"send"\|"discard", confirm}` §3.6 오케스트레이션 | resume 결과 |
| `POST /api/control/core` | `{action:"restart"\|"stop", confirm}` (호스팅) | — |
| `POST /api/control/service` | `{action:"start"\|"stop"\|"restart", confirm}` (관측, CLI+UAC) | — |
| `POST /api/wizard/start` | `{steps:["0","1","2","3a","3b","4"], readerId?}` | run id (§5. 3a 는 §5.3 confirm 게이트 통과 전 실행 안 됨) |
| `POST /api/wizard/confirm` | `{step:"3a", confirm}` — 실체크인 단일 관문 | — |
| `POST /api/wizard/abort` | 중단 | — |
| `GET /api/wizard/report` | 마지막 리포트 (redacted) | §5.4 |
| `GET /api/meta` | 메타 | `{version, cfgFingerprint, mode, port, paths:{config,dataDir,sessionsFile,reports}}` |

### 4.3 State 객체 (status.json v2 §6.1 의 상위 집합; `signal`·`headline` 은 GUI 계산)

```json
{
  "mode": "hosting",
  "signal": "green",
  "headline": "정상 운영 중 · 이번 실행 체크인 152건",
  "senderState": "RUNNING",
  "queueDepth": 0,
  "queueNonEmptySince": null,
  "oldestCheckedAt": null,
  "ntp": {"checked": true, "skewSec": 1, "at": "…"},
  "logDropped": 0,
  "updatedAt": "2026-09-02T11:00:03+09:00",
  "readers": [{
    "id": "gate-a", "sessionId": "session1", "sessionName": "세션1",
    "sessionVerified": true,
    "connState": "CONNECTED", "connSince": "…",
    "gateState": "ACTIVE", "gateText": "정상", "actionText": "",
    "lastTagAt": "…", "lastSuccessAt": "…", "pending": 0,
    "boothName": "세션1", "unitName": "Session 1", "cooldownSec": 60
  }],
  "catalog": {"loaded": true, "exportedAt": "…", "stale": false,
              "updateAvailable": false, "pendingImport": false},
  "successSinceStart": 152
}
```

### 4.4 SSE 이벤트

| 타입 | 페이로드 | 발생원 |
| --- | --- | --- |
| `log` | 마스킹된 코어 JSONL 1행 | 호스팅: ring buffer / 관측: 로그 tail(§2.4) |
| `state` | §4.3 State 전체 (변경 시 + 5s 하트비트) | gates·conn 변경, status 갱신 |
| `catalog` | `{reason:"reloaded"\|"parse_error"\|"download_found"}` | 파일 감시 |
| `wizard` | §5.3 단계 진행 객체 | 마법사 실행기 |

브라우저 미접속 시에도 코어는 정상 동작(SSE 는 관측 창구일 뿐).

---

## 5. 현장 점검 마법사 상세

### 5.1 불변식 (plan FR-16, 검증 후 개정)

- (a) 마법사 **자신은** 운영 큐·gate 테이블에 쓰지 않는다 — 2단계는 no-op
  `gate.Persister`, 3b·4단계는 `{dataDir}_selftest` 격리. 단 3a(실태그 관측)는
  운영자의 물리적 스캔이 **운영 파이프라인을 정상 경로로** 통과하는 것을
  관측만 한다 — 이것은 운영 행위이지 마법사의 쓰기가 아니다.
- (b) 실서버 체크인이 생기는 3a 는 `POST /api/wizard/confirm` 단일 관문 통과
  후에만 진행된다(다이얼로그 문구 §5.3).
- (c) 마법사는 코어를 정지시키지 않는다. 리더 소켓(단일 접속)과 실태그 스캔이
  필요한 단계는 **코어를 통해 관측**하는 방식으로 설계되어 충돌이 없다(§5.3).

### 5.2 상태 기계

```text
IDLE ──start──▶ RUNNING(step k) ──단계 종료──▶ 다음 step … ──▶ SUMMARY
                   │        └─ 실패 시: §5.3 "실패 → 이후" 규칙 적용(계속/비활성)
                   └──abort──▶ SUMMARY(중단 표기)
SUMMARY ──[리포트 저장/다시 실행]──▶ IDLE
동시 실행 1개(`conflict_busy`). 실행 중 세션 변경·resume 버튼 잠금.
```

### 5.3 단계 정의

| # | 단계 | 실행 내용 (재사용 코드) | PASS | WARN | FAIL → 이후 |
| --- | --- | --- | --- | --- | --- |
| 0 | 설정·환경 | `config.Load`, `health.CheckDiskDir`, `health.CheckNTP`(서비스 동작 여부) + **`congkong.ProbeSkew`**(§6.9, HTTP Date 기반 skew), config·카탈로그 ACL 검사, `powercfg /q` 절전 정책 | 전부 통과 | ACL 느슨/절전 켜짐/카탈로그 없음/`CheckNTP` 미동작이지만 skew 정상 | 설정 무효·dataDir 쓰기 불가·\|skew\|≥240s → 중단 권고(계속 가능) |
| 1 | 리더 연결 | 코어(호스팅/서비스)가 리더 점유 중이면 **connState/lastTagAt 로 대체 판정**; 코어 미실행 시에만 `session.Probe`(§6.6) 직접 | 대체판정: CONNECTED / 직접: **8s 내**(dial 5s+cmd 2s+여유) INIT 완료+펌웨어 | 직접 Probe RTT>4s | timeout → "다른 프로그램(YAT 등) 점유 또는 전원·케이블 — 클라이언트 종료 후 리더 전원 재투입" 표시, 계속 가능 |
| 2 | 서버 preflight | **`congkong.Client.Preflight` 1회 호출**(timeout 10s, 실패 시 1회 재시도 — `sender.Preflight.Run` 의 무한 backoff 루프는 쓰지 않음) + 임시 `gate.Registry`(no-op Persister) 로 분류만 재사용 | 200 & cooldownSec>0 & booth/unit/event 가 선택 세션과 일치 | cooldown=0 / 이름 불일치 | 404·5xx·2회 실패 → 3a·3b 비활성 |
| 3a | 실태그 관측 (E2E) | **confirm 관문** 후 60초 동안 코어 이벤트(ring buffer/로그 tail)에서 `SEND_RESULT` 대기 — 운영자가 시험용 태그를 실제 스캔 | 200 관측 (재스캔 시 409 관측은 보너스 표기) | 409 만 관측(이미 체크인된 태그) | 60s 무이벤트·400/404 → 원인문 표시 |
| 3b | 무해 모드 | `{dataDir}_selftest` + **대상 리더 1개로 좁힌 사본 config** 로 replay 경로 실행: 합성 라인 **`>T3400{미등록EPC}`**(실장비 관측 PC=3400, `testdata/reader-lines` 참조) 1건 → 파싱→큐→HTTP→분류 전 구간. 리더 소켓 불필요 | 404 `BARCODE_NOT_FOUND` | — | 그 외 응답 |
| 4 | 오프라인 재생(선택) | `_selftest` 사본 config 의 apiHost 를 `127.0.0.1:1`(도달 불가)로 바꿔 1건 적재 → 원 apiHost 사본으로 재기동해 드레인 | 적재 확인 + 원 `checkedAt` 보존 드레인(404 무해 EPC 이므로 체크인 미생성) | — | 드레인 실패 |
| 5 | 요약 | §5.4 리포트 | — | — | — |

3a 확인 다이얼로그: "실제 서버에 체크인 기록이 생성됩니다. 반드시 시험용 태그를
사용하세요. 진행할까요?" — 기본 버튼 취소.

### 5.4 리포트

- 사람용 `.txt` + 기계용 `.json`, 파일명 `field-check-{YYYYMMDD-HHMM}`,
  저장 `{dataDir}\reports\`, "파일 위치 열기" 버튼.
- 내용: 단계별 pass/warn/fail + 관측값(펌웨어, RTT, boothName, HTTP status,
  skewSec) + 버전 + cfg fingerprint.
- **Redaction**: 직렬화 직전 `logging.Redact`(§6.7) 통과 + EPC 끝 4자 마스킹.
  **토큰·EPC 전문 부재를 단위 테스트로 고정**(실토큰 주입 → 출력 바이트 검사).

### 5.5 `_selftest` 격리 규칙

- 경로 `{dataDir}_selftest`(형제). 시작 시 생성, SUMMARY 진입 시 큐 DB 삭제
  (리포트만 보존). lock 분리로 운영 코어와 병행 가능.
- 사본 config 는 **대상 리더 1개만** 포함(그 외 리더의 preflight·세션이 뜨지
  않도록). 3b/4 는 미등록 EPC 만 사용 — 서버에 체크인 레코드를 만들지 않는다.

---

## 6. 코어 additive 변경 상세

### 6.1 status.json v2 스키마

기존 필드 유지 + 추가(모두 additive). **`"schema": 2`** 마커 신설 — 관측 모드
GUI 가 구버전(마커 없음=v1) 파일을 구분해 "서비스 업데이트 필요"를 안내한다.
`senderState` 는 기존 그대로 `string(domain.SenderState)` — 리터럴
`"RUNNING"`/`"HALTED_REQUEST_BUG"` (기존 CLI 소비자 계약 불변).

```json
{
  "schema": 2,
  "updatedAt": "RFC3339",
  "senderState": "RUNNING",
  "queueDepth": 0, "queueNonEmptySince": "RFC3339|생략",
  "oldestCheckedAt": "RFC3339|생략",
  "pid": 1234, "version": "v0.2.0", "mode": "service|hosting|cli",
  "startedAt": "RFC3339",
  "ntp": {"checked": true, "skewSec": 1, "at": "RFC3339"},
  "successSinceStart": 152,
  "readers": [{
    "id": "gate-a", "gateState": "...", "gateReason": "...",
    "eventName": "...", "boothName": "...", "unitName": "...", "cooldownSec": 60,
    "connState": "CONNECTED|DISCONNECTED", "connSince": "RFC3339",
    "sessionId": "session1",
    "lastTagAt": "RFC3339", "lastSuccessAt": "RFC3339", "pending": 0
  }]
}
```

- `lastTagAt`/`lastSuccessAt` 실기록(M1 필수), `successSinceStart` 는 프로세스
  기동 이후 Complete 카운터(재기동 시 0 — 화면 문구도 "이번 실행").
- 기록 주기: 변경 이벤트 구동 + 5s 하트비트(`WriteStatus` 원자성 유지).

### 6.2 `session.Probe`

```go
// 접속→INIT(버전 조회까지)→Stop→종료 1회 왕복 진단. 재접속 루프·Inventory 없음.
// 전체 예산 8s (dial 5s + cmd 2s + 여유). 코어가 리더 점유 중일 때 부르지 말 것
// — 호출자는 §5.3 #1 의 대체 판정 규칙을 따른다.
func Probe(ctx context.Context, cfg Config, dial DialFunc) (ProbeResult, error)
type ProbeResult struct { Firmware string; RTT time.Duration }
```

### 6.3 로그 ring buffer (GUI Echo 탭)

`gui.NewLogRing(4096행, drop-oldest)` — `Write()` 는 복사 후 즉시 반환(코어 방향
무블록), 별도 고루틴이 SSE 구독자에 flush. 드랍 누계는 State `logDropped` 로 노출.
(`logging.Log` 가 mutex 안에서 Echo 를 동기 호출하므로 필수 조건.)

### 6.4 변경 목록 총괄

| # | 변경 | 비고 |
| --- | --- | --- |
| 1 | `lastTagAt`/`lastSuccessAt` 실기록 + status v2 필드(§6.1) | M1 |
| 2 | `app.Options.PreacquiredLock` (§6.5) | M2 전제 |
| 3 | `session.Probe` (§6.2) + conn 보고 콜백 (§6.6) | M1/M3 |
| 4 | `gate.Registry.Subscribe()` fan-out (§6.6) | M1 |
| 5 | `logging.Redact` export (§6.7) | M3 |
| 6 | `congkong.ProbeSkew` (§6.9) + skew 의 status 승격 | M1 |
| 7 | config `sessionsFile`·`readers[].sessionId` (§3.5) | M1 |
| 8 | config writer 계층 (§6.8 — gui 패키지, 코어 무변경) | M2 |

### 6.5 잠금 위임 (검증 C-1 해소)

`app.Run` 내부의 `acquireLock` 은 **같은 프로세스의 재획득도 거부**하므로, GUI 가
잠금을 쥔 채 `app.Run` 을 부를 수 없다. additive 해결:

```go
type Options struct { …; PreacquiredLock func() /* release */ }
// nil 이면 지금처럼 스스로 획득. 주어지면 획득 생략, 종료 시 release 호출.
```

GUI 흐름: `app.AcquireLock(dataDir)` (exported 헬퍼) → 성공 시 호스팅 모드로
release 를 Options 에 주입 / 실패 시 관측 모드. TOCTOU 없음(잠금을 놓지 않는다).

### 6.6 conn 상태 보고와 gates 구독 (검증 W-1·W-5 해소)

- `session.Config` 에 `OnConn func(state ConnState)` 콜백(additive) —
  `serve` 진입 시 CONNECTED, 종료 시 DISCONNECTED 보고. app 이 원자값+`connSince`
  로 유지해 status 에 기록. RECONNECTING 상태는 만들지 않는다(§2.3).
- `gate.Registry.Changed()` 는 **sender 전용 단일 소비자**(버퍼 1) — GUI 가
  읽으면 sender 깨우기 신호를 훔친다. `Subscribe() (<-chan struct{}, cancel)`
  fan-out 을 추가하고 Changed() 는 내부적으로 그 위에 얹는다. GUI·status 기록기는
  Subscribe 만 사용.

### 6.7 `logging.Redact` export (검증 W-6 해소)

현 `forbiddenKeys` 는 비공개 **키 필터**다. export 형태:
`logging.Redact(map[string]any) map[string]any` — 금칙 키 제거 + 값 스캔
(64hex 토큰 패턴 → fingerprint 치환, EPC 패턴 → 끝 4자 마스킹). 코어 로그 경로는
기존 그대로(성능 불변), GUI 송출·리포트 직렬화만 이 함수를 통과한다.

### 6.8 config writer (검증 U-2 해소 — gui 패키지에 신설, 코어 무변경)

- 원본 파일 바이트를 읽어 `map[string]any` 로 파싱 → 대상 리더의
  `pulseToken`/`sessionId` 만 치환 → indent 2 marshal → `config.json.bak` 백업 →
  temp+fsync+rename. 성공적으로 재기동까지 마치면 .bak 삭제, 실패 시 롤백(§3.4).
- 쓰기 직후 `config.Load` 로 재검증해 코어가 못 읽을 파일이 남지 않게 한다.

### 6.9 시계 skew 프로브 (검증 C-5 해소)

`sender.Preflight` 의 Date 헤더 skew 계산(`HasDateSkew/DateSkew`)을 재사용해
`congkong.ProbeSkew(ctx, apiHost) (skew time.Duration, err)` 를 신설
(`GET /v3` 급의 무해 요청 1회). app 은 기동 시 + 1시간 주기로 호출해
status `ntp.skewSec` 를 갱신하고, preflight 가 계산한 최신 skew 로도 덮는다.
`ntp.checked` = `CheckNTP().QueryOK || skew 관측 성공`.

---

## 7. 트레이·수명주기

### 7.1 기동 흐름

```text
exe 기동(인자 없음) → gui.lock 단일 인스턴스 확인(이미 있으면 기존 브라우저 재오픈 후 종료)
→ config 로드(없으면 §2.1 온보딩) → app.AcquireLock 시도
→ 성공: 호스팅 모드, PreacquiredLock 주입해 app.Run goroutine / 실패: 관측 모드
→ 127.0.0.1 HTTP + nonce → 트레이 아이콘 → 기본 브라우저 오픈
인자 있음 → AttachConsole → 기존 CLI 동작 (GUI 미기동)
```

### 7.2 트레이 메뉴

`상태: 정상 운영 중`(비활성, 신호등 동기화) / `화면 열기` / `현장 점검…` /
`서비스 모드 안내` / 구분선 / `종료`

### 7.3 종료

- 브라우저 창 닫기 = 아무 일 없음(트레이가 수명 소유).
- 트레이 `종료`: 호스팅 모드면 확인("체크인 수집이 중단됩니다") → 코어 ctx
  cancel → **grace 10s** → HTTP 종료 → 트레이 제거. 관측 모드는 즉시 종료
  ("서비스는 계속 동작" 1줄). 로그아웃/시스템 종료 시그널도 동일 grace 경로.

---

## 8. 설치 패키지 (Inno Setup — plan M4)

| 설치 단계 | 내용 |
| --- | --- |
| 파일 | `%ProgramFiles%\CongKong\RFID Middleware\rfid-middleware.exe` + README |
| 데이터 | `%ProgramData%\CongKong\RFIDMiddleware` 생성. ACL: SYSTEM·Administrators **+ 설치 실행 사용자 계정에 Modify** (`icacls … /grant "{user}:(OI)(CI)M"`) — 비관리자 세션의 호스팅 모드가 config·카탈로그·큐·`_selftest` 를 쓸 수 있어야 함(검증 C-7). 마법사 0단계 ACL 검사도 이 3주체 구성을 정상으로 판정 |
| config | 기존 파일 보존, 없으면 example 배치(온보딩이 완성) |
| 서비스 | 체크박스 "무인 상주(서비스) 등록"(기본 켬) → `service install` |
| 바로가기 | 시작 메뉴 + 바탕화면 (GUI 기동) |
| 절전 | 체크박스 "행사용 전원 설정 적용" → `powercfg` 절전/최대절전 해제 |
| 방화벽 | 불필요(127.0.0.1) — 문서에 명시만 |
| 제거 | 서비스 중지+해제, 프로그램 삭제. **dataDir(큐·로그)는 보존** 안내 |

빌드: GitHub Actions windows 러너 `iscc` → `rfid-middleware-setup-{ver}.exe`
+ SHA-256 Release 자산. zip(무설치)도 병행 — 단, exe 부분 교체 금지(§3.5 주의).

---

## 9. M0 스파이크 반영 대기 항목

- `-H=windowsgui` + AttachConsole 실기기 UX (실패 시 2-exe 분리 fallback)
- cgo-free systray 라이브러리 확정 (실패 시 트레이 제거, 창 상주 대체)
- 브라우저 자동 오픈·방화벽 무프롬프트 확인
- `KnownFolders` 기반 Downloads 경로(OneDrive 리다이렉션) 확인

## 10. 수용 기준 (Gap 분석 기준점)

| ID | 시나리오 | 근거 FR |
| --- | --- | --- |
| G1 | 더블클릭 기동 → 트레이 + 브라우저 대시보드, cmd 창 없음 | FR-01,08 |
| G2 | 서비스 실행 중 기동 → 관측 모드 배지 + 읽기 전용 동작 | FR-02 |
| G3 | 신호등이 §2.2 규칙대로 전이 (시나리오 6종 시뮬레이션 — HALTED_REQUEST_BUG·토큰회수·리더끊김30s·skew·큐적체·정상) | FR-03 |
| G4 | 태그 스캔 → 500ms 내 카드·로그 반영, 카드 필드 전부 §2.3 정의와 일치 | FR-04, NFR |
| G5 | 카탈로그 덮어쓰기 → 2s+α 내 갱신 토스트; Downloads 감지 → 가져오기; §3.6 오류 7분기 각각 표대로 동작 | FR-17,19 |
| G6 | 세션 선택 → 매칭 → preflight ✓/불일치/404 3분기 + 중복 세션 거부 + 재기동 실패 롤백 | FR-18 |
| G7 | 토큰 전문이 API·SSE·DOM·리포트 어디에도 없음 (자동 테스트) + cfg/토큰 fingerprint 가 §1.1 정의대로 | FR-20, FR-09 |
| G8 | 마법사 0~3b 완주 + redacted 리포트 저장 (운영 gate·큐 불변 검증 포함) | FR-10~16 |
| G9 | 트레이 종료 → grace 10s 내 정리, 재기동 시 큐 복구 | FR-07 |
| G10 | 기존 CLI 전 서브커맨드 회귀 없음, `go test -race`·교차빌드 그린 | FR-08 |
| G11 | 관측 모드에서 서비스 stop→resume(send/discard)→start 왕복이 GUI 버튼만으로 완료 | FR-06 |
| G12 | 마법사 4단계: 차단 중 적재 → 복구 후 원 checkedAt 보존 드레인 | FR-14 |
| G13 | nonce 없는 경로·타 Origin POST·127.0.0.1 외 접속이 전부 거부됨 | NFR 보안 |

## Version History

| Version | Date | Changes |
|---------|------|---------|
| 0.95 | 2026-09-02 | design-validator 반영: 잠금 위임(§6.5), 세션 중복 금지(§3.4), 재개 오케스트레이션(§3.6), 마법사 3a 관측 재설계·3b 합성 규칙(§5), skew 프로브(§6.9), State 필드·리터럴 정정, ACL 사용자 부여(§8), fingerprint 정의(§1.1), G11~13, 미정의 13건 해소 |
| 0.9 | 2026-09-02 | 화면·API·마법사·코어 변경·수명주기·설치 상세화 |
| 0.3 | 2026-09-02 | 카탈로그 즉시 반영·Downloads 가져오기 강화 |
| 0.2 | 2026-09-02 | 세션 카탈로그(양식 v1·선택·자동 매칭) 확정 설계 |
| 0.1 | 2026-09-02 | 골격 (CTO 결정 요약) |
