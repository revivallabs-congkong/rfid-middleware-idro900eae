# CongKong RFID 체크인 미들웨어 (IDRO900EAE)

IDRO900EAE UHF RFID 리더가 읽은 태그(EPC)를 CongKong 서버 체크인 API 로 전달하는
Windows 상주 프로그램. Go 단일 바이너리, CGO 없음.

- 계획서: `congkong-v3/docs/features/pulse/rfid-middleware-idro900eae-development-plan.ko.md`
- 설계서: `congkong-v3/docs/02-design/features/rfid-middleware-idro900eae.design.md`
- 리더 SSOT: `IDRO900EAE-settings.md` / 서버 SSOT: `rfid-middleware-protocol.ko.md` v1.1
- 이 저장소 안 문서: [운영 API smoke 절차](docs/ops-smoke.ko.md) · [인수인계](HANDOFF.ko.md)

## 빠른 시작

```text
동작 흐름:  리더 TCP (>T... CRLF 라인) → EPC 파싱 → 60초 디바운스
           → SQLite 큐 (오프라인 24h 보존) → POST /v3/pulse-tokens/{token}/check-in
```

1. `config.example.json` 을 `config.json` 으로 복사하고 `addr`(리더 IP:데이터포트),
   `pulseToken`(운영진에게 발급받은 64자 hex), `dataDir` 를 채운다.
   토큰은 콩콩 콘솔에서 전달받는다 — 게이트(부스) 화면 펄스 토큰 섹션의
   **「URL 복사」**(토큰 포함 URL), 또는 세션 펄스 목록의 **「현황·토큰 다운로드」**
   xlsx 2번째 시트(게이트·유닛·토큰·URL 일괄). 받은 파일·URL 은 이 저장소에 넣지 않는다.
2. `rfid-middleware validate-config --config config.json` 으로 검증한다.
3. 개발 PC 에서는 `run` 으로 foreground 실행, 현장 노트북에서는 아래
   [Windows 설치](#windows-설치-관리자-powershell)대로 서비스 등록한다.
4. 실행 상태는 `rfid-middleware status --data-dir <dataDir>` 로 확인한다
   (송신 상태, 큐 잔량, 리더별 게이트 상태·마지막 태그/성공 시각이
   status.json 기준으로 출력된다).

⚠️ 펄스 토큰은 이 저장소·이슈·로그 첨부 어디에도 기록하지 않는다.
`config.json` 은 gitignore 대상이다.

## 빌드

```bash
go build ./...
GOOS=windows GOARCH=amd64 go build -o dist/rfid-middleware.exe ./cmd/rfid-middleware
```

## 실행 모드

```text
rfid-middleware run --config config.json                # foreground (콘솔 로그 echo)
rfid-middleware replay --stdin --reader gate-a --config config.json
rfid-middleware replay --file fixture.ndjson --ndjson --reader gate-a --config config.json
rfid-middleware validate-config --config config.json
rfid-middleware status --data-dir "C:\ProgramData\CongKong\RFIDMiddleware"
rfid-middleware queue resume --reader gate-a --pending send|discard --config config.json
rfid-middleware service install|uninstall|start|stop --config config.json   # Windows 전용
```

- `replay` 는 실제 TCP 리더 없이 CRLF 프레이머 이후 전체 경로(파서→디바운스→큐→전송)를
  운영과 동일하게 태운다. NDJSON 형식(`{"receivedAt":"RFC3339","chunks":["hex..."]}`)은
  chunk 경계와 수신 시각까지 재현한다. `--drain`(기본 켜짐)은 입력 소진 후 큐를
  비우고 종료한다. 운영 API 상대 시험 절차는 [docs/ops-smoke.ko.md](docs/ops-smoke.ko.md) 참조.
- `queue resume` 은 토큰 회수/재바인딩으로 중단된 리더를 서비스 중지 상태에서 재개한다.
  `--pending send` 는 쌓인 큐를 재전송, `--pending discard` 는 폐기 후 재개한다.

## 설정

`config.example.json` 참조. 주석 없는 엄격한 JSON 이며 알 수 없는 필드는 오류다.

| 필드 | 기본 | 범위 |
| --- | --- | --- |
| `version` | (필수) | 1 |
| `apiHost` | (필수) | HTTPS (loopback 은 HTTP 허용 — 테스트용) |
| `dataDir` | (필수) | 절대 경로. 큐 DB·로그·status.json 위치 |
| `debounceSec` | 60 | 1~3600 |
| `queueMaxAgeHours` | 24 | 1~24 (서버 `CheckedAtMaxAge` 계약) |
| `requestTimeoutSec` | 10 | 1~30 |
| `powerGain` | 300 | 50~300, **0.1dBm 단위** (300=30dBm). 오인식(원거리 태그) 시 하향 |
| `buzzer` | 0 | **0=무음(기본), 1=비프음**. 접속 초기화 때마다 리더에 반영 |
| `readers[]` | (필수 1~8) | `id`, `addr`(host:port), `pulseToken`(64 hex) |

리더 TCP 데이터 포트는 벤더 문서에 명시가 없으나 **`5578` 로 확인됨**
(2026-09-01, SSOT `IDRO900EAE-settings.md` §2.1). 리더 IP 를 현장 대역에 맞춰
변경한 경우에만 `addr` 의 host 부분을 수정하면 된다. `PORT` 같은 미확정
placeholder 는 설정 검증에서 거부된다.

## Windows 설치 (관리자 PowerShell)

```powershell
mkdir "C:\ProgramData\CongKong\RFIDMiddleware"
copy config.json "C:\ProgramData\CongKong\RFIDMiddleware\config.json"
# 설정 파일 ACL 제한 (SYSTEM/Administrators 만)
icacls "C:\ProgramData\CongKong\RFIDMiddleware\config.json" /inheritance:r `
  /grant "SYSTEM:F" /grant "Administrators:F"
.\rfid-middleware.exe service install --config "C:\ProgramData\CongKong\RFIDMiddleware\config.json"
.\rfid-middleware.exe service start
```

- 서비스는 Automatic (Delayed Start), 실패 시 1분 후 자동 재시작.
- 행사 중 노트북 절전/최대 절전은 꺼 둔다 (전원 옵션).
- NTP 동기화 필수: `w32tm /query /status` 확인. 시계가 틀어지면 서버가
  `checkedAt` 을 400 으로 폐기한다.

## 운영 메모

- **`READER_CONNECTED`↔`READER_DISCONNECTED` 반복 (`INIT_VERSION 응답 timeout`)**:
  다른 프로그램(YAT 등 터미널)이 리더 데이터 포트를 점유 중이면 TCP 접속은 되지만
  응답이 오지 않아 이 패턴이 된다. 다른 클라이언트를 완전히 종료하고 **리더 전원을
  재투입**하면 자동 복구된다 (2026-09-01 현장 확인).
- **토큰 회수(404)**: 해당 리더만 송신 중단(`SUSPENDED_TOKEN`)되고 반복 ERROR 가 남는다.
  새 토큰 설정 → 서비스 중지 → `queue resume` → 서비스 시작으로만 재개한다.
  회수~재발급 사이의 스캔은 큐에 적재되지 않는다(의도된 동작).
- **400 error bind**: 미들웨어 자신의 요청 형식 버그 — 전역 송신 중단. 스캔은 계속
  큐에 쌓이며 수정 배포 후 원래 시각으로 재전송된다(24시간 만료 한도 내).
- **오프라인**: 5xx/타임아웃/연결 실패는 5s→30s→2m 백오프로 재시도하며, 프로세스
  재시작 후에도 큐가 유지된다. 원래 `checkedAt` 은 절대 갱신되지 않는다.
- **로그**: `dataDir\logs\middleware-YYYYMMDD.jsonl` (10 MiB×10, 14일).
  참가자 이름·전화·이메일·서버 응답 원문·전체 토큰은 어떤 로그에도 남지 않는다.

## 테스트

```bash
go test ./...
go test -race ./...
```

수용 기준 매핑은 설계서 §14.6 참조. B1~B7 은 자동 테스트로, RDR1~RDR3 은
가짜 리더 테스트 + 실장비 단계(3단계)에서 검증한다.
