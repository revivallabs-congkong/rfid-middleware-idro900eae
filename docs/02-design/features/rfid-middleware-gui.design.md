# rfid-middleware-gui Design Document

> **Summary**: GUI(트레이 + 로컬 웹 UI) 전환 설계. 이 버전은 **세션 카탈로그 기반
> 세션 선택·토큰 자동 매칭**(§3)을 확정 설계하고, 나머지 영역은 CTO 결정
> (plan §6)을 골격으로 두어 이후 반복에서 상세화한다.
>
> **Plan**: `docs/01-plan/features/rfid-middleware-gui.plan.md` (FR-17~20 이 이 문서 §3)
> **Date**: 2026-09-02 · **Status**: Draft v0.2

---

## 1. 아키텍처 골격 (plan §6 확정 사항 요약)

- 단일 exe. 기동 시 `app.lock` 중재로 **호스팅 모드**(GUI가 `app.Run` in-process
  소유) / **관측 모드**(서비스 실행 중, 읽기 전용) 분기.
- 렌더링: go:embed 정적 자산 + `127.0.0.1` ephemeral 포트 HTTP + 기본 브라우저.
  트레이 상주(cgo-free). SSE 로그 스트림은 drop-oldest ring buffer 경유.
- 코어 변경은 additive 만: status 필드 확장, `session.Probe`, 잠금 헬퍼,
  **reader 설정에 `sessionId` 선택 필드 추가**(§3.5 — 이번 설계로 1건 추가됨).

## 2. 화면 구성 (개요)

| 화면 | 내용 |
| --- | --- |
| 대시보드 | 신호등, 리더 카드(연결·게이트 상태·마지막 태그/성공·**선택된 세션 이름**), 큐 잔량 |
| 세션 선택 | §3 의 카탈로그 목록에서 리더별 세션 지정 |
| 로그 | SSE 실시간 뷰 (최근 2000행) |
| 현장 점검 | 마법사 0~5단계 (plan FR-10~16) |

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
    },
    {
      "id": "session2",
      "name": "session2",
      "unitName": "Session 2",
      "tokenLabel": "",
      "pulseToken": "…",
      "issuedAt": "2026-09-01T16:26:53+09:00"
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

**호환 규칙**
- 미들웨어는 **알 수 없는 필드를 무시**한다(관용 파싱) — congkong-v3 가 필드를
  추가해도 기존 GUI가 깨지지 않는다. 파괴적 변경 시에만 `version` 을 올린다.
- 콘솔의 기존 「현황·토큰 다운로드」 xlsx 2번째 시트와 컬럼이 1:1 대응
  (부스 관리코드→id, 부스 이름→name, 연결 유닛→unitName, 토큰 라벨→tokenLabel,
  토큰→pulseToken, 발급일→issuedAt). 서버 구현은 같은 쿼리에 직렬화만 바꾸면 된다.

**보안 규칙 (파일 취급)**
- 실토큰 전문이 담기므로: 저장소·메신저에 올리지 않는다. 미들웨어 `.gitignore`
  에 `pulse-sessions.json` 추가. GUI 는 첫 로드 시 파일 ACL 이 느슨하면 경고
  (마법사 0단계 항목과 동일 검사).

### 3.3 파일 위치와 반영(덮어쓰기 = 즉시 반영)

- 위치: `config.json` 의 새 선택 필드 `sessionsFile` (기본값: config.json 과
  같은 디렉토리의 `pulse-sessions.json`). 절대/상대(=config 기준) 모두 허용.
- **상시 감시**: GUI 프로세스가 살아 있는 동안 `sessionsFile` 의 mtime 을
  **2초 폴링** — 화면이 어디에 있든 파일을 덮어쓰면 즉시 재로드되고, 대시보드에
  "카탈로그 갱신됨" 토스트 + 세션 화면 목록 자동 갱신. 수동 새로고침 버튼도 유지.
  (fsnotify 의존성 대신 폴링 — 파일 1개라 비용 무시 가능. mtime 변경 후 200ms
  디바운스로 쓰기 도중 읽기 회피, 파싱 실패 시 1s 후 1회 재시도)
- **다운로드 폴더 자동 감지(가져오기)**: 운영자가 콘솔에서 받은 파일은 보통
  `%USERPROFILE%\Downloads` 에 떨어진다. GUI 는 Downloads 의
  `pulse-sessions*.json` (브라우저 중복 저장 `(1)` 패턴 포함) 신규 생성을 감시해
  "새 카탈로그를 발견했습니다 — 가져올까요?" 프롬프트 → 확인 시 `sessionsFile`
  위치로 **이동**(복사 후 원본 삭제 — 토큰 파일을 Downloads 에 방치하지 않기 위함).
  자동 적용은 하지 않는다(운영자 확인 필수).
- 파일 없음: 목록 화면에 "파일을 이 위치에 놓으세요: <경로>" 안내 + 양식 예시 링크.
- 파싱 오류: 마지막 정상 로드본을 유지한 채 오류 배너 표시(빈 목록으로 덮지 않음).

### 3.4 선택 → 자동 매칭 → 검증 시퀀스

```text
운영자: 리더(gate-a) 행에서 [세션 선택] → 목록에서 "세션1" 클릭
GUI:
 1. 카탈로그에서 id=session1 의 pulseToken 을 꺼낸다 (화면 표시는 fingerprint 8자만)
 2. config.json 갱신: readers[gate-a].pulseToken = 토큰, .sessionId = "session1"
    (원자적 쓰기: temp + rename. 실패 시 원본 불변)
 3. 호스팅 모드면 코어 재기동(ADR-007: 설정 반영은 재시작), 관측 모드면
    "서비스 재시작 필요" 안내 + 재시작 버튼
 4. preflight 결과 대조:
    - PREFLIGHT_OK 이고 boothName==name && unitName==unitName && eventName==eventName
      → 리더 카드에 "세션1 ✓" 확정 표시
    - 이름 불일치 → 경고 "카탈로그와 서버 정보가 다릅니다 — 파일이 낡았거나
      다른 이벤트의 파일입니다. 콘솔에서 다시 내보내 교체하세요."
    - 404 (TOKEN_REVOKED) → "이 세션의 토큰이 회수되었습니다 — 콘솔에서 재발급 후
      파일을 갱신하세요." (카탈로그 해당 행에 ⚠ 배지)
```

- 같은 세션을 두 리더에 지정하려 하면 확인 대화상자(허용은 하되 중복 경고 —
  게이트 2개가 같은 부스를 볼 수도 있으므로 금지하지 않는다).
- 큐에 pending 이 있는 리더의 세션 변경은 **차단**하고 `queue resume` 화면으로
  안내한다 — 코어의 rebind suspension(설계서 §8.2)과 같은 원칙을 GUI가 선제 적용.

### 3.5 코어/설정의 additive 변경

| 변경 | 내용 | 호환성 |
| --- | --- | --- |
| config `readers[].sessionId` | 선택 필드(string). 코어는 로드·보존만 하고 동작에 사용하지 않음(표시·대조용 주석) | 기존 config 유효 유지 |
| config `sessionsFile` | 선택 필드. GUI 만 사용 | 〃 |
| status.json `readers[].sessionId` | 관측 모드 GUI 가 서비스의 세션 지정을 표시할 수 있도록 기록 | JSON 필드 추가 |

CLI(콘솔) 경로는 지금처럼 pulseToken 직접 기입도 계속 지원한다 — 카탈로그는
GUI 의 입력 수단일 뿐, config.json 이 런타임 SSOT 라는 사실은 불변.

### 3.6 오류 처리 표

| 상황 | GUI 동작 |
| --- | --- |
| 파일 없음 / 경로 오류 | 안내 + 양식 예시. 기존 config 토큰으로는 계속 동작 |
| `version` ≠ 1 | 전체 거부 + "프로그램 업데이트 또는 파일 재내보내기 필요" |
| 토큰 형식 위반 항목 | 해당 항목만 제외, 항목명과 함께 경고 |
| `id` 중복 | 뒤의 항목 무시 + 경고 |
| 카탈로그 토큰 ≠ config 토큰 (같은 sessionId) | 리더 카드에 "카탈로그가 갱신됨 — 다시 선택하면 새 토큰 적용" 배지 + 원클릭 재적용 버튼(확인 후 재기동) |
| 리더가 TOKEN_REVOKED 로 중단 중 + 갱신된 카탈로그에 같은 sessionId 의 새 토큰 존재 | **"새 토큰으로 재개" 원클릭** — 토큰 반영 → `queue resume` 흐름(send/discard 선택) → 재기동. 토큰 회수 복구를 파일 덮어쓰기 한 번으로 단축 |
| exportedAt 이 7일 이상 과거 | 정보 배너 (오류 아님) |

### 3.7 테스트 (마법사·자동 테스트에 추가)

- 단위: 양식 파서(정상/각 오류 케이스), 원자적 config 쓰기, fingerprint 만 노출
  (**토큰 전문이 SSE·DOM 문자열에 없음을 테스트로 고정** — plan NFR 보안과 동일 원칙)
- 통합: 선택→config 반영→가짜 서버 preflight 대조(일치/불일치/404) 3분기
- 마법사 0단계에 "카탈로그 파일 존재·버전·ACL" 점검 항목 추가

---

## 4. 이후 상세화 예정 (다음 설계 반복)

- 대시보드/로그 화면 와이어프레임, SSE 이벤트 계약(JSON 스키마)
- 마법사 각 단계 화면·상태 기계
- M0 스파이크 결과 반영(트레이·AttachConsole fallback 확정)

## Version History

| Version | Date | Changes |
|---------|------|---------|
| 0.2 | 2026-09-02 | 세션 카탈로그(양식 v1·선택·자동 매칭) 확정 설계 추가 |
| 0.1 | 2026-09-02 | 골격 (CTO 결정 요약) |
