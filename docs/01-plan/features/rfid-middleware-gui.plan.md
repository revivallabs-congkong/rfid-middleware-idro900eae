# rfid-middleware-gui Planning Document

> **Summary**: cmd 창으로 구동하는 RFID 체크인 미들웨어를 GUI(트레이 + 로컬 웹 UI)로 전환하고, 현장 점검 마법사(내장 테스트 프로세스)를 탑재한다.
>
> **Project**: congkong-pulse-rfid-middleware-idro900eae
> **Version**: v0.1.0 기준 (현장 검증 완료 빌드)
> **Author**: Jun-Yeong Park (CTO 리드 에이전트 논의 반영)
> **Date**: 2026-09-02
> **Status**: Draft (CTO 결정 반영 v1.0)

---

## 1. Overview

### 1.1 Purpose

현장 운영자(비개발자)가 cmd 창 없이 미들웨어를 구동·관찰·점검할 수 있게 한다.
"지금 정상인가"를 한눈에 보여주고, 현장 설치 시 점검(리더 연결·서버 preflight·
태그 스캔 확인)을 프로그램 안의 테스트 프로세스(마법사)로 수행한다.

### 1.2 Background

- v0.1.0 은 콘솔 `run` 또는 Windows 서비스로만 구동 — JSONL 로그를 읽을 수 있는
  사람만 상태 판단이 가능하다.
- 2026-09-01 현장 시험에서 확인된 노하우(YAT 포트 점유 → INIT timeout 반복,
  NTP 오차 → 400 폐기 등)가 로그 한 줄에 머물러 있어 신규 운영자가 알 수 없다.
- HANDOFF 잔여 과제(RDR3, 오프라인 차단→복구, 재부팅 검증)를 반복 가능한
  운영자 액션으로 승격할 기회.

### 1.3 Related Documents

- CTO 결정서: 본 문서 §6 에 요지 수록 (2026-09-02 bkit:cto-lead 논의)
- 기존 설계서: `congkong-v3/docs/02-design/features/rfid-middleware-idro900eae.design.md`
- 운영 문서: `README.md`, `docs/field-test.ko.md`, `HANDOFF.ko.md`

---

## 2. Scope

### 2.1 In Scope

- [ ] 단일 exe 유지: 기동 시 `app.lock` 획득 여부로 **호스팅 모드**(GUI가 코어를
      in-process 소유) / **관측 모드**(서비스 실행 중 → 읽기 전용) 자동 분기
- [ ] 트레이 상주 + 임베드된 127.0.0.1 HTTP + 기본 브라우저 렌더링 (go:embed, CGO 없음)
- [ ] 상태 화면: 신호등(정상/경고/오류), 리더 카드, 실시간 로그 뷰(SSE), 큐 잔량
- [ ] **현장 점검 마법사** (내장 테스트 프로세스, §3.1 FR-10~16)
- [ ] 코어 additive 변경: `LastTagAt`/`LastSuccessAt` 기록, status.json 필드 확장
      (pid·version·mode·connState·ntp), 이벤트 구동 status 기록, `session.Probe`
- [ ] 서비스 start/stop·queue resume 을 GUI 버튼으로 (기존 CLI 경로 재사용, UAC 경유)
- [ ] `-H=windowsgui` + AttachConsole 로 CLI 하위 호환 유지
- [ ] **세션 카탈로그 파일 기반 세션 선택 + 토큰 자동 매칭** (양식 v1, 설계서 §3)

### 2.2 Out of Scope

- 원격(다른 PC/모바일) 접속 — HTTP 는 127.0.0.1 명시 바인드, 원격 노출 금지
- 설정 편집 GUI (v2 후보 — v1 은 config.json 직접 편집 유지)
- 다국어 (한국어 단일)
- 코어 파이프라인(큐·전송·분류 불변식) 변경 — GUI 는 부가 계층
- WebView2/Fyne/Wails 등 런타임·CGO 의존 기술 (§6 결정 2에서 탈락)

---

## 3. Requirements

### 3.1 Functional Requirements

| ID | Requirement | Priority | Status |
|----|-------------|----------|--------|
| FR-01 | 단일 exe, 더블클릭 기동 시 트레이 상주 + 브라우저 상태 화면 자동 오픈 | High | Pending |
| FR-02 | `app.lock` 중재로 호스팅/관측 모드 자동 분기, 화면에 현재 모드 배지 표시 | High | Pending |
| FR-03 | 신호등 1개로 종합 상태 표시, 모든 경고에 "다음 행동 1줄" 문장 병기 | High | Pending |
| FR-04 | 리더별 카드: 연결 상태, 게이트 상태(조치 문장으로 번역), 마지막 태그/성공 시각 | High | Pending |
| FR-05 | 실시간 로그 뷰: SSE 푸시, 최근 2000행, drop-oldest ring buffer 경유(코어 블록 금지) | High | Pending |
| FR-06 | 서비스 start/stop, `queue resume`(send/discard) 버튼 — 파괴적 동작은 한국어 설명 + 재확인 | Medium | Pending |
| FR-07 | 닫기=트레이 최소화, 종료는 확인 다이얼로그 (호스팅 모드 태그 유실 방지) | High | Pending |
| FR-08 | CLI 하위 호환: 인자 있는 기동은 AttachConsole 로 기존 서브커맨드 동작 유지 | High | Pending |
| FR-09 | 버전 + 설정 fingerprint(8자) 화면 상시 노출 | Low | Pending |
| FR-10 | 마법사 0단계: 설정·dataDir·NTP·config ACL 점검 (NTP 를 가시적 pass/fail 로 승격) | High | Pending |
| FR-11 | 마법사 1단계: 리더 TCP 연결+INIT+펌웨어 확인 (`session.Probe`), 실패 시 YAT 포트 점유 노하우 안내 | High | Pending |
| FR-12 | 마법사 2단계: 서버 preflight — **no-op gate.Persister 주입**으로 운영 gate 불변 | High | Pending |
| FR-13 | 마법사 3단계: 실태그 E2E(200→409, 명시적 확인 클릭 후) / 무해 모드(미등록 EPC 404) — `_selftest` 별도 dataDir | High | Pending |
| FR-14 | 마법사 4단계(선택): 오프라인 차단→복구 재생 — HANDOFF §10 항목 5를 상시 재현 가능하게 | Medium | Pending |
| FR-15 | 마법사 5단계: pass/warn/fail 요약 리포트 + 원클릭 내보내기 (logging 금칙 필드 정책 경유) | High | Pending |
| FR-16 | 마법사 불변식: 운영 큐·gate 불변경, 실체크인은 명시적 확인 후에만 | High | Pending |
| FR-17 | 세션 카탈로그: `pulse-sessions.json` 덮어쓰기 시 **상시 감시로 즉시 반영**, Downloads 폴더 신규 파일 자동 감지 + 원클릭 가져오기(이동) | High | Pending |
| FR-18 | GUI에서 리더별 세션 선택 → **토큰 자동 매칭**(카탈로그에서 주입) → config 반영 → preflight로 boothName/unitName 교차 검증 | High | Pending |
| FR-19 | 카탈로그 양식 v1 준수 (§6.4, congkong-v3 콘솔이 이 양식으로 내보냄). 미들웨어는 알 수 없는 필드 무시(관용 파싱) | High | Pending |
| FR-20 | 세션 목록·화면 어디에도 토큰 전문 미표시 (fingerprint 8자만) | High | Pending |

### 3.2 Non-Functional Requirements

| Category | Criteria | Measurement Method |
|----------|----------|-------------------|
| 성능 | 태그 스캔→화면 표시 ≤ 500ms(호스팅), SSE ≤ 1s; **GUI가 코어 처리량에 영향 0** (Echo 탭 ring buffer 필수) | 부하 재생 테스트 + race 테스트 |
| 성능 | 상주 RSS < 150MB, exe 크기 증가 ≤ +2MB | 실기기 측정 |
| 보안 | 토큰 전문 GUI·SSE·리포트 어디에도 금지(fingerprint 8자만), EPC 끝 4자리 마스킹 | **유출 부재 단위 테스트로 고정** |
| 보안 | 127.0.0.1 명시 바인드 + ephemeral 포트 + 1회용 nonce URL + Origin 검증, CSP self, 외부 CDN 금지 | 코드 리뷰 + 통합 테스트 |
| 신뢰성 | 종료 시 서비스와 동일한 grace(10s)로 in-flight 정리; 이중 기동 불가(`app.lock`) | 통합 테스트 |
| 빌드 | CGO 없음, macOS→Windows 교차 빌드 유지, 단일 exe | 기존 CI |

---

## 4. Success Criteria

### 4.1 Definition of Done

- [ ] 운영자가 **cmd 창을 한 번도 열지 않고** 하루 운영 가능 (M2 종료 기준)
- [ ] 비개발자가 문서 없이 마법사 완주 (M3 종료 기준)
- [ ] 리포트에 토큰/EPC 전문 부재가 테스트로 고정됨
- [ ] HANDOFF 잔여 과제(RDR3·오프라인 복구·재부팅 검증)가 마법사/패키징으로 해소
- [ ] 기존 CLI·서비스 경로 회귀 없음 (`go test -race` + 교차빌드 CI 그린)

### 4.2 Quality Criteria

- [ ] 신규 코드 핵심 경로(모드 중재, ring buffer, 마법사 격리) 단위 테스트
- [ ] `go vet` / CI 그린, 깨끗한 Windows 노트북 복사 실행 검증

---

## 5. Risks and Mitigation (CTO 결정서 §4)

| Risk | Impact | Likelihood | Mitigation |
|------|--------|------------|------------|
| R1: GUI가 코어 수명주기 인질 — 창 닫힘/로그아웃 시 미판독 태그 영구 유실 | High | Medium | 트레이 상주 + 닫기=최소화 + 종료 확인; 무인 장시간 운영은 서비스 모드가 기본임을 배지로 안내; 절전 정책을 마법사 0단계에 포함 |
| R2: 교차빌드/런타임 실패 (windowsgui+AttachConsole·systray·브라우저 자동실행) — macOS에서 검증 불가 | High | Medium | **M0 스파이크를 실기기에서 최우선 수행**, 항목별 fallback 사전 확정(2-exe 분리/트레이 제거/수동 URL) |
| R3: 자체 점검이 운영 오염 (가짜 체크인·gate 덮어쓰기·리포트 유출) | High | Low | `_selftest` dataDir + no-op Persister + 명시 확인 클릭 + 유출 부재 테스트 고정 |
| R4: 로컬 HTTP 무인증 → 동일 PC 타 프로세스 접근 | Medium | Low | ephemeral 포트 + nonce + Origin 검증 + 읽기 전용 기본 + 파괴적 동작 재확인·UAC |

---

## 6. Architecture Considerations (CTO 결정 확정)

### 6.1 Project Level

Dynamic (기존 프로젝트 레벨 유지 — Go 단일 바이너리 + 기능 모듈 구조).

### 6.2 Key Architectural Decisions

| Decision | Options | Selected | Rationale |
|----------|---------|----------|-----------|
| 프로세스 토폴로지 | (a) 서비스+GUI 컴패니언 / (b) GUI 단일 통합 / (c) 브라우저 유일 조작면 | **(a)+(b) 하이브리드**: 단일 exe가 `app.lock` 획득 결과로 호스팅/관측 모드 자동 분기 | 코어가 이미 이중 기동을 잠금으로 차단 — 잠금 중재가 앱 2개 없이 양쪽 장점 확보. (b) 단독은 창 닫힘=태그 유실, (a) 단독은 관리자 권한 필요로 "복사 실행" 제약 위반 |
| GUI 기술 | Fyne / Wails / lxn-walk / **go:embed+로컬HTTP+브라우저** + systray | **go:embed 로컬 웹 UI + cgo-free systray** (walk 는 예비안) | Fyne=CGO 필요, Wails=교차빌드 불가+WebView2 선설치(오프라인 venue 위험). 채택안은 CGO 0·런타임 의존 0·기존 Vue/TS 역량 재사용 |
| GUI↔코어 통신 | named pipe / 코어 HTTP / **모드별 분리** | 호스팅=in-process(`Echo` 탭+`gates.Snapshot`), 관측=status.json 폴링+로그 tail, 제어=자기 exe CLI 재실행 | **코어에 새 IPC 표면을 만들지 않는다** — 토큰 보유 프로세스의 공격면 불증가. status.json 은 temp+fsync+rename 이라 torn read 없음 |
| 콘솔 하위 호환 | 별도 exe / **windowsgui+AttachConsole** | 단일 exe 유지, M0 에서 UX 수용 불가 판명 시 2-exe 분리 fallback | 단일 exe 배포 선호 |
| 배포 방식 | zip 복사 실행 / **설치 패키지(Inno Setup)** | **설치 패키지 채택** (2026-09-02 제약 완화): 서비스 등록·ACL·절전 정책을 설치가 자동화. zip 은 fallback 유지 | 사전 설치·세팅이 필요하면 설치 프로그램으로 해결 가능해짐. 단, GUI 기술 선택은 불변 — WebView2 계열 탈락 근거는 설치가 아니라 **macOS 교차 빌드 불가** |
| 코어 변경 | — | **additive 5건만**: LastTagAt/LastSuccessAt 기록(현재 선언만 되고 미기록 — M1 필수), status 필드 확장, 이벤트 구동 기록, 잠금 헬퍼 export, `session.Probe` | `app.Run` 시그니처 불변, 파이프라인 불변식 유지 |

### 6.3 필수 기술 조건 (선택이 아닌 조건)

- `logging.Log` 가 mutex 안에서 `echo.Write()` 를 동기 호출하므로, GUI Echo 탭은
  **반드시 drop-oldest ring buffer + 별도 flush 고루틴**으로 감싼다 — 느린 UI 소비자가
  전체 파이프라인을 블록하는 것을 차단.
- 마법사 2단계는 `sender.Preflight` 가 gate 를 영속화하므로 **no-op `gate.Persister`
  주입**이 정답 (1-메서드 인터페이스).

---

## 7. Milestones (테스트 프로세스 포함)

| M | 내용 | 기간 | 종료 기준 (테스트 게이트) |
|---|------|------|--------------------------|
| **M0** | 빌드 리스크 스파이크: windowsgui+AttachConsole / cgo-free systray 교차빌드 / go:embed+127.0.0.1+브라우저 자동실행 | 2일 | **실기기**에서 3항목 검증, 실패 항목은 fallback 확정. 이전에 UI 코드 작성 금지 |
| **M1** | 관측 GUI(읽기 전용) + 코어 additive 변경 | 3~4일 | 서비스 상태를 GUI만으로 판단, `go test -race`·교차빌드 CI 그린 |
| **M2** | 호스팅 GUI: 잠금 중재 분기, in-process app.Run + Echo→SSE(ring buffer), 트레이 상주 | 3일 | cmd 없이 하루 운영, 종료 grace 10s 통합 테스트 |
| **M3** | 현장 점검 마법사 0~5단계 + `_selftest` 격리 + redacted 리포트 | 4~5일 | 비개발자 완주, 유출 부재 테스트 고정, RDR3·오프라인 복구가 마법사로 재현 |
| **M4** | **설치 패키지** 제작(Inno Setup, CI windows 러너에서 빌드): 서비스 등록·config ACL·절전 해제 정책·시작 메뉴·제거 지원 + SHA-256. 깨끗한 노트북 검증 | 2~3일 | 설치→재부팅 자동 시작→큐 복구 원클릭 검증 (HANDOFF 단계 4 잔여 동시 완료). zip 복사 실행도 fallback 으로 계속 지원 |

합계 14~16 작업일. **M0 가 게이트** — 스파이크 결과 전 GUI 기술 선택은 되돌릴 수 있는 상태 유지.

---

## 8. Next Steps

1. [ ] 설계서 작성: `/pdca design rfid-middleware-gui` (마법사 단계별 시퀀스·화면 와이어·API 계약)
2. [ ] M0 스파이크 착수 (설계와 병렬 가능 — 실기기 필요)
3. [ ] 구현 시작 (`/pdca do`)

---

## Version History

| Version | Date | Changes | Author |
|---------|------|---------|--------|
| 1.0 | 2026-09-02 | CTO 리드 논의 결과 반영 초안 | Jun-Yeong Park + CTO agent |
