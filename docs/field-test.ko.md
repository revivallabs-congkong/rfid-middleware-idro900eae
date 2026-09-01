# 현장 시험 절차 — Windows 노트북 + IDRO900EAE 실장비 (계획서 단계 3)

> 목표: 운영 smoke 잔여 항목(프로토콜 §10 2·3)과 리더 수용 기준 RDR1~RDR3 실증,
> raw 리더 라인 회귀 픽스처 수집.
> ⚠️ 실토큰은 저장소·리포트·스크린샷 어디에도 남기지 않는다.

## 0. 준비물

- Windows 10/11 노트북 (관리자 권한), 리더와 같은 스위치/직결 이더넷
- IDRO900EAE + 전원, 페어링 완료된 UHF 태그 1개 이상
- `rfid-middleware.exe` — GitHub Release 에서 다운로드 후 SHA-256 대조
- 펄스 토큰 — 콩콩 콘솔 「현황·토큰 다운로드」 xlsx 2번째 시트
  (시험용 시트는 Release v0.1.0-rc.1 에 첨부돼 있음 — exe 와 함께 다운로드)

## 1. 네트워크 연결

리더 기본 IP `192.168.9.6`, 데이터 포트 `5578` (확정값, settings §2.1).

```powershell
# 이더넷 어댑터에 리더 대역 고정 IP 부여 (어댑터 이름은 ncpa.cpl 로 확인)
netsh interface ip set address name="이더넷" static 192.168.9.100 255.255.255.0

# 리더는 ping 에 응답하지 않을 수 있음 — TCP 로 확인
Test-NetConnection 192.168.9.6 -Port 5578   # TcpTestSucceeded: True 여야 함
```

## 2. 설정 (Phase A 용)

작업 폴더(예: `C:\rfid-test`)에 `config.json` 생성:

```json
{
  "version": 1,
  "apiHost": "https://api.congkong.net",
  "dataDir": "C:\\rfid-test\\data",
  "debounceSec": 5,
  "queueMaxAgeHours": 24,
  "requestTimeoutSec": 10,
  "powerGain": 300,
  "buzzer": 1,
  "logLevel": "debug",
  "readers": [
    { "id": "gate-a", "addr": "192.168.9.6:5578", "pulseToken": "<실토큰>" }
  ]
}
```

- `debounceSec: 5` 는 §10 항목 3(409 관측)용 시험값 — 쿨다운 60초 안에 재전송이
  나가야 하므로 낮춘다. Phase C 에서 60 으로 되돌린다.
- `logLevel: "debug"` 는 모든 raw 리더 라인을 `READER_RAW` 로 남긴다 (픽스처 수집용).
- NTP 확인: `w32tm /query /status` — 시계가 틀어지면 서버가 400 으로 폐기한다.

## 3. Phase A — 접속 + §10 항목 2·3 (체크인 200 → 재스캔 409)

```powershell
.\rfid-middleware.exe run --config C:\rfid-test\config.json
```

| 순서 | 행동 | 기대 로그 |
| --- | --- | --- |
| 1 | 기동 | `READER_CONNECTED` → `READER_INVENTORY_STARTED`, `PREFLIGHT_OK` (boothName·cooldownSec=60) |
| 2 | 페어링 태그를 리더에 잠깐 대기 | `SCAN_ENQUEUED` (epc 필드가 페어링 값과 일치하는지 확인) → `SEND_RESULT` httpStatus=200 `CHECKIN_SUCCESS` |
| 3 | 10초쯤 뒤 같은 태그 재스캔 | `SEND_RESULT` httpStatus=409 `CHECKIN_DUPLICATE_SUCCESS` |
| 4 | 미페어링 태그(있으면) | `SEND_RESULT` httpStatus=404 `BARCODE_NOT_FOUND` 후 정상 계속 |

2번에서 **실태그 EPC 가 페어링 값과 같은 계열**(자릿수·프리픽스)인지 콘솔 기록과
대조한다 — 다르면 페어링 정책 재검토가 필요하므로 관측값을 기록.

## 4. Phase B — RDR1·RDR2 (전원 재투입 / 절단 복구)

middleware 는 켜 둔 채:

| 순서 | 행동 | 기대 로그 |
| --- | --- | --- |
| 1 | 리더 전원 뽑기 | `READER_DISCONNECTED` |
| 2 | 10초 뒤 전원 재투입 | 자동 재접속 `READER_CONNECTED` → 초기화 재전송 → `READER_INVENTORY_STARTED` |
| 3 | 태그 스캔 | `SCAN_ENQUEUED` 재개 (RDR1 통과) |
| 4 | 이더넷 케이블만 뽑았다 꽂기 | 1~3 과 동일 흐름 |

초기화 시퀀스(Stop→설정→Inventory)가 재접속마다 다시 나가므로 RDR2(파서가
Stop/설정 응답에 안 깨짐)는 이 과정에서 함께 실증된다 — `READER_UNKNOWN_LINE`
warning 이 없어야 함.

## 5. Phase C — RDR3 (10초 유지 → 전송 1건)

1. middleware 중지, `config.json` 의 `debounceSec` 을 `60` 으로 변경 후 재시작.
2. 태그를 리더 앞에 **10초 이상** 계속 둔다.
3. `SCAN_ENQUEUED`/`SEND_RESULT` 가 **1건만** 발생해야 한다 (리더 폭주 + 디바운스 실증).

## 6. 오프라인 재생 확인 (§10 항목 5, 선택)

1. 이더넷은 유지한 채 인터넷(공유기 업링크 등)을 잠깐 차단 → 태그 스캔
   → `NETWORK_FAILURE` 재시도 로그, 큐 적재 확인 (`status` 명령).
2. 복구 → 원래 `checkedAt` 으로 재전송되는지, 콘솔 방문 기록 시각이 스캔 시각인지 확인.

## 7. 픽스처 회수·마무리

1. `C:\rfid-test\data\logs\middleware-YYYYMMDD.jsonl` 을 회수한다 — 토큰·PII 는
   원래 로그에 없으므로 파일 자체는 안전하나, 공유 전 한 번 훑어본다.
2. `READER_RAW` 라인들의 `raw` 값을 정리해 `testdata/reader-lines/` 에 회귀
   픽스처로 추가한다 (개발 머신에서 커밋).
3. 결과를 dev-spec §7 수용 기준 3개(RDR1~RDR3)와 §10 체크리스트에 체크하고,
   특이 관측(문서와 다른 라인 형식 등)은 원문과 함께 기록한다.
4. 시험 후 `config.json` 의 실토큰을 삭제한다.
