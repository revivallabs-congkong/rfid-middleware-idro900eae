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
- 구조·불변식·수용 기준 매핑은 설계서 §4, §13, §14.6 이 기준. 운영 방법은 `README.md`.

## 남은 단계 (순서대로)

1. ~~**리모트 저장소 생성·푸시**~~ ✅ 완료 (2026-09-01)
   - https://github.com/revivallabs-congkong/rfid-middleware-idro900eae (private)
   - 첫 CI 통과 (test+race+교차빌드). annotation: actions/checkout@v4·setup-go@v5 의
     Node 20 deprecated 경고 — 동작엔 문제 없음, 여유 있을 때 버전 업
2. ~~**운영 API smoke test (계획서 단계 2 마무리)**~~ ✅ **완료** (2026-09-01)
   - §10 항목 1(PREFLIGHT_OK, cooldownSec=60)·2(실태그 200)·3(재스캔 409) 전부 실측 통과,
     콘솔 체크인 기록도 확인됨. 미등록 EPC 404 drop, 무효 토큰 `TOKEN_SUSPENDED` 도 검증
   - 페어링 태그: `E2801170000002155EDD7076` (실태그 EPC = 페어링 값, 200 으로 실증)
3. **실장비 통합 (계획서 단계 3)** — 대부분 완료 (2026-09-01 현장 시험)
   - ~~TCP 데이터 포트 확인~~ ✅ `5578` 확정 — settings §2.1·dev-spec §8·config.example.json 반영
   - ✅ RDR1(절단→자동 재접속→인벤토리 재개, 2회 실증), RDR2(재초기화 시 파서 무결,
     `READER_UNKNOWN_LINE` 없음). 실장비 raw 라인(펌웨어 EAE26081902, **>T PC=3400**)을
     `testdata/reader-lines/idro900eae-real-20260901.txt` + 파서 회귀 테스트로 추가
   - ⚠️ 현장 노하우: 다른 클라이언트(YAT)가 포트 점유 시 `INIT_VERSION timeout` 반복
     — 클라이언트 종료 + 리더 전원 재투입으로 해결 (README 운영 메모 기재)
   - **남은 것**: RDR3 정식 시험(debounceSec=60 으로 태그 10초 유지 → 전송 1건),
     오프라인 차단→복구 재생(§10 항목 5), (선택) 토큰 회수 시험
4. **Windows 상주화·출시 (계획서 단계 4)** — 패키징 완료, 설치 검증 남음
   - ✅ 정식 릴리스 **v0.1.0** 배포 (2026-09-01): exe + SHA-256 + config.example.json,
     릴리스 노트에 검증 상태·설치 요약 포함
   - 남은 것: 깨끗한 Windows 노트북에서 `service install` → 재부팅 자동 시작 → 큐 복구
     확인, 설정 파일 ACL(icacls)·절전 해제 정책 적용 — README §Windows 설치 참조
   - 남은 현장 시험: RDR3(debounce 60, 10초 유지→1건), 오프라인 차단→복구, (선택) 토큰 회수

## 참고

- 실행 중 상태 확인: `rfid-middleware status --data-dir <dataDir>` (status.json)
- 토큰 회수 후 재개: 서비스 중지 → `queue resume --reader <id> --pending send|discard` → 시작
- 설정 예시 `config.example.json` 의 `addr` 은 `PORT` placeholder 라 검증에서 의도적으로 거부됨
