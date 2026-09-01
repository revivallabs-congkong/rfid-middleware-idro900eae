package congkong

import (
	"encoding/json"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/domain"
)

// 응답 DTO — attendee 필드는 선언하지 않는다. Go decoder 가 미선언 필드를
// 버리므로 PII 는 여기서 이미 사라진다 (설계서 §8.3). response bytes 는
// 분류가 끝나면 즉시 폐기하고 store/logger 로 넘기지 않는다 (불변식 6).
type resultOnly struct {
	Result string `json:"result"`
}

type errorEnvelope struct {
	Message string      `json:"message"`
	Code    int         `json:"code"`
	Data    *resultOnly `json:"data"`
}

// 분류 클래스 상수 (설계서 §8.4 표의 state/log class).
const (
	ClassCheckinSuccess     = "CHECKIN_SUCCESS"
	ClassResponseLost       = "RESPONSE_LOST"
	ClassProtocolAnomaly2XX = "PROTOCOL_ANOMALY_2XX"
	ClassDuplicateSuccess   = "CHECKIN_DUPLICATE_SUCCESS"
	ClassBarcodeNotFound    = "BARCODE_NOT_FOUND"
	ClassTokenRevoked       = "TOKEN_REVOKED_OR_INVALID"
	ClassBarcodeInvalid     = "BARCODE_INVALID"
	ClassBarcodeNoOwner     = "BARCODE_NO_OWNER"
	ClassConditionFailed    = "CONDITION_FAILED"
	ClassCheckedAtFormat    = "CHECKED_AT_FORMAT"
	ClassCheckedAtRange     = "CHECKED_AT_RANGE"
	ClassRequestBindBug     = "REQUEST_BIND_BUG"
	ClassEmptyUIDBug        = "EMPTY_UID_BUG"
	ClassUnknown4XX         = "UNKNOWN_4XX"
	ClassServer5XX          = "SERVER_5XX"
	ClassNetworkFailure     = "NETWORK_FAILURE"
)

// Classify 는 HTTP 결과를 SSOT 프로토콜 §4-2 대로 분류한다 (설계서 §8.4).
// 에러의 result 는 반드시 data.result 로 판별한다 — top-level 금지 (불변식 4).
func Classify(r HTTPResult) (domain.DeliveryDecision, string) {
	if r.TransportErr != nil {
		return domain.DecisionRetry, ClassNetworkFailure
	}
	// body 를 끝까지 읽지 못한 경우는 응답 유실 시나리오 — 같은 checkedAt 으로
	// 재시도하면 서버 쿨다운 멱등성이 409 로 마무리한다 (설계서 §8.4).
	if r.BodyErr != nil {
		return domain.DecisionRetry, ClassResponseLost
	}

	switch {
	case r.Status == 200:
		var ok resultOnly
		if err := json.Unmarshal(r.Body, &ok); err == nil && ok.Result == "success" {
			return domain.DecisionComplete, ClassCheckinSuccess
		}
		// 200 인데 body 비정상 — 서버가 성공 처리했을 수 있으므로 재시도로
		// 중복을 만들지 않고 terminal drop + 치명 protocol anomaly (설계서 §8.4).
		return domain.DecisionDrop, ClassProtocolAnomaly2XX

	case r.Status == 409:
		env := decodeEnvelope(r.Body)
		if env.Data != nil && env.Data.Result == "success:duplication" {
			// 성공과 동일. 응답 checkedAt 은 저장/해석하지 않는다 (불변식 5).
			return domain.DecisionComplete, ClassDuplicateSuccess
		}
		return domain.DecisionDrop, ClassUnknown4XX

	case r.Status == 404:
		env := decodeEnvelope(r.Body)
		if env.Data != nil && env.Data.Result == "fail:barcode-not-found" {
			// 페어링되지 않은 태그 — 무시하고 다음 스캔. 재큐 금지.
			return domain.DecisionDrop, ClassBarcodeNotFound
		}
		// 그 외 모든 404 는 토큰 회수/무효 — 해당 reader 송신 중단 (R9).
		return domain.DecisionSuspendReader, ClassTokenRevoked

	case r.Status == 406:
		if envResult(r.Body) == "fail:barcode-invalid" {
			return domain.DecisionDrop, ClassBarcodeInvalid
		}
		return domain.DecisionDrop, ClassUnknown4XX

	case r.Status == 424:
		if envResult(r.Body) == "fail:barcode-no-owner" {
			return domain.DecisionDrop, ClassBarcodeNoOwner
		}
		return domain.DecisionDrop, ClassUnknown4XX

	case r.Status == 403:
		if envResult(r.Body) == "fail:condition" {
			return domain.DecisionDrop, ClassConditionFailed
		}
		return domain.DecisionDrop, ClassUnknown4XX

	case r.Status == 400:
		env := decodeEnvelope(r.Body)
		switch env.Message {
		case "checkedAt must be RFC3339":
			return domain.DecisionDrop, ClassCheckedAtFormat
		case "checkedAt out of range":
			return domain.DecisionDrop, ClassCheckedAtRange
		case "error bind":
			// 공통 요청 인코더의 자기 버그 — 전역 송신 중단.
			// 트리거한 행은 삭제하지 않고 보존한다 (설계서 §8.5).
			return domain.DecisionHaltGlobal, ClassRequestBindBug
		case "barcodeString or invitationCode required":
			return domain.DecisionDrop, ClassEmptyUIDBug
		}
		return domain.DecisionDrop, ClassUnknown4XX

	case r.Status >= 500:
		return domain.DecisionRetry, ClassServer5XX

	case r.Status >= 400:
		// 여기 없는 4xx 는 버리고 카운트만 (프로토콜 §4-2).
		return domain.DecisionDrop, ClassUnknown4XX

	default:
		// 1xx/3xx 등 예상 밖 status — 성공 여부를 알 수 없으므로 2xx anomaly 와
		// 같은 이유로 terminal drop 한다.
		return domain.DecisionDrop, ClassProtocolAnomaly2XX
	}
}

func decodeEnvelope(body []byte) errorEnvelope {
	var env errorEnvelope
	_ = json.Unmarshal(body, &env) // malformed 는 zero 값 → 보수적 분기로 흐른다
	return env
}

func envResult(body []byte) string {
	env := decodeEnvelope(body)
	if env.Data == nil {
		return ""
	}
	return env.Data.Result
}

// PreflightMeta 는 200 preflight body 를 최소 DTO 로 해석한다.
func PreflightMeta(body []byte) (domain.GateMeta, bool) {
	var m domain.GateMeta
	if err := json.Unmarshal(body, &m); err != nil {
		return domain.GateMeta{}, false
	}
	// boothName 없는 200 은 계약 위반으로 본다.
	if m.BoothName == "" && m.EventName == "" && m.UnitName == "" {
		return domain.GateMeta{}, false
	}
	return m, true
}
