# 운영 API Smoke Test 절차 (계획서 단계 2 마무리)

> 대상: 프로토콜 SSOT `rfid-middleware-protocol.ko.md` §10 체크리스트 1~3.
> ⚠️ 실토큰은 이 저장소·테스트 리포트·로그 첨부에 절대 기록하지 않는다.

## 0. 사전 확인 (완료 — 2026-09-01)

실토큰 없이 가능한 항목은 미리 검증했다:

- 운영 엔드포인트 `https://api.congkong.net/v3/pulse-tokens/{token}` 도달 확인.
- 무효 토큰(전체 0) GET → `404 {"message":"resource not found","code":404,"data":null}`.
  `data`가 null 이므로 분류기(`internal/congkong/classifier.go`)는
  `TOKEN_REVOKED_OR_INVALID` → reader 송신 중단으로 처리한다. 기대와 일치.
- 무효 토큰 설정으로 replay 드라이런 → `SCAN_ENQUEUED` 후 preflight 404 로
  `TOKEN_SUSPENDED`(readerId=gate-a) 기록, drain 후 정상 종료. 아래 명령 그대로 재현됨.

## 1. 운영진에게 요청할 것

| 항목 | 용도 |
| --- | --- |
| 테스트 게이트의 64자리 펄스 토큰 | 전 시나리오 공통 (cooldownSec > 0 설정 확인 포함) |
| 페어링된 테스트 EPC 1개 | 200 성공 → 409 중복 확인 |
| 미등록 EPC 1개 | 404 `fail:barcode-not-found` (무시·계속) 확인 |
| (가능하면) 토큰 회수 시험 시간대 | 404 토큰 → reader 중단 → `queue resume` 재개 확인 |

## 2. 준비

1. 저장소 루트에 `config.json` 생성 (**gitignore 대상** — 커밋되지 않음):
   `config.example.json` 을 복사한 뒤 `addr` 의 `PORT` placeholder 를 임의 유효
   포트(예: `192.168.9.6:4001`)로, `pulseToken` 을 실토큰으로 교체.
   replay 는 TCP 접속을 하지 않으므로 addr 은 형식만 유효하면 된다.
2. `dataDir` 은 로컬 임시 경로로 변경 — **절대 경로만 허용**된다
   (상대 경로는 설정 검증에서 거부됨). 저장소 밖 경로를 권장한다.
3. 시험 후 `config.json` 의 토큰을 즉시 삭제하거나 파일을 제거한다.

## 3. 시나리오와 기대 결과

replay 는 CRLF 프레이머 이후 운영과 동일 경로(파서→디바운스→큐→전송)를 탄다.

```bash
# ① 기동 preflight — B1
#    로그에 boothName / cooldownSec 이 찍히고 cooldownSec > 0 인지 확인
printf '>T3000<테스트EPC>\r\n' | ./rfid-middleware replay --stdin --reader gate-a --config config.json
```

| # | 입력 | 기대 로그 클래스 | 프로토콜 §10 |
| --- | --- | --- | --- |
| 1 | (기동 시 자동 GET) | `PREFLIGHT_OK` — boothName·cooldownSec>0 (0 이면 `ACTIVE_WARNING` 상태로 반복 경고) | 항목 1 |
| 2 | 페어링 EPC 1회 | `CHECKIN_SUCCESS` (200) | 항목 2 |
| 3 | 같은 EPC 재실행 (debounceSec 지난 뒤, 쿨다운 내) | `CHECKIN_DUPLICATE_SUCCESS` (409) | 항목 3 |
| 4 | 미등록 EPC | `BARCODE_NOT_FOUND` (404, drop 후 계속) | — |
| 5 | (선택) 토큰 회수 후 스캔 | `TOKEN_REVOKED_OR_INVALID` → reader 송신 중단 | — |

- 3번: 같은 프로세스 내 재스캔은 debounce 에 걸려 요청이 안 나가므로,
  replay 를 두 번 실행하되 `dataDir` 를 유지해 durable debounce 만료(60s) 후 재송신 확인.
  쿨다운이 debounce 보다 길어야 409 가 관측된다 (짧으면 200 이 두 번 — 운영진에 쿨다운 확인).
- 5번 후 재개: `./rfid-middleware queue resume --reader gate-a --pending discard --config config.json`

## 4. 통과 기준·기록

- §10 항목 1~3 전부 기대 클래스로 관측되면 단계 2 종료.
- 리포트에는 **로그 클래스·HTTP status 만** 기록한다. 토큰·attendee 응답 본문은 기록 금지
  (로그 자체가 redaction 되지만, 첨부 전 재확인).
- 결과는 계획서 §9.2 추적표 B1 증거로 남긴다.
