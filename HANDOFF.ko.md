# 인수인계 — 다음 세션 시작 지점

> 작성: 2026-09-01. 이 저장소는 초기 커밋 `c400995` 상태이며 리모트가 아직 없다.

## 지금까지 된 것

- 계획서·설계서 검토 후 5건 반영 완료 (계획서 v1.1):
  - `docs/features/pulse/rfid-middleware-idro900eae-development-plan.ko.md`
  - `docs/02-design/features/rfid-middleware-idro900eae.design.md`
  - 핵심 정정: **400 error bind 시 현재 건 보존**(삭제 아님), cooldown=0 송신 유지,
    설정 스키마 통일, 토큰 회수 중 스캔 유실 명시
- 서버 계약은 server repo `origin/develop`의 `featurev3/check/pulse/` 실코드로 검증함
  (meta 응답에 `action` 필드 존재 — 미들웨어는 무시, 토큰은 소문자 64 hex)
- 개발 단계 0~2 + 리더 세션 + Windows 서비스 골격 구현 완료:
  - 수용 기준 **B1~B7 자동 테스트 통과** (`internal/app/app_test.go`, `internal/sender/sender_test.go`)
  - RDR1·RDR2 는 가짜 리더 테스트로 검증 (`internal/reader/session/session_test.go`)
  - `go vet ./...`, `go test -race ./...` 전부 통과
  - `GOOS=windows GOARCH=amd64` 교차 빌드 확인 (약 15MB exe)
- 구조·불변식·수용 기준 매핑은 설계서 §4, §13, §14.6 이 기준. 운영 방법은 `README.ko.md`.

## 남은 단계 (순서대로)

1. **리모트 저장소 생성·푸시**
   - revivallabs-congkong org 에 `rfid-middleware-idro900eae` 생성 (gh CLI 사용 가능)
   - `git remote add origin ... && git push -u origin main` — CI(.github/workflows/ci.yml)가 test+race+교차빌드를 돈다
2. **운영 API smoke test (계획서 단계 2 마무리)**
   - 운영진에게 요청: 테스트 게이트의 64자리 펄스 토큰, 페어링된 테스트 EPC, 미등록 EPC, 가능하면 토큰 회수 시험 시간대
   - `rfid-middleware replay --stdin --reader <id> --config <실토큰 설정>` 으로 프로토콜 §10 체크리스트 1~3 확인
   - ⚠️ 실토큰은 저장소·테스트 리포트에 기록 금지
3. **실장비 통합 (계획서 단계 3)**
   - **IDRO900EAE TCP 데이터 포트 현장 확인** (벤더 문서에 없음) → `IDRO900EAE-settings.md` §2.1 표와 dev-spec §8 에 기입
   - 실태그 EPC 가 페어링 값과 같은 계열인지 확인, 전원 재투입/절단/10초 유지 시험 (RDR1~RDR3)
   - 관측한 raw 리더 라인을 `testdata/reader-lines/` 회귀 픽스처로 추가
4. **Windows 상주화·출시 (계획서 단계 4)**
   - 깨끗한 Windows 노트북에서 `service install` → 재부팅 자동 시작 → 큐 복구 확인
   - 설정 파일 ACL(icacls), 절전 해제 정책, SHA-256 checksum 패키징 — README §Windows 설치 참조

## 참고

- 실행 중 상태 확인: `rfid-middleware status --data-dir <dataDir>` (status.json)
- 토큰 회수 후 재개: 서비스 중지 → `queue resume --reader <id> --pending send|discard` → 시작
- 설정 예시 `config.example.json` 의 `addr` 은 `PORT` placeholder 라 검증에서 의도적으로 거부됨
