package congkong

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/domain"
)

const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// SSOT 프로토콜 §4-1 실물 봉투 fixture (계획서 §6.4: 409 봉투 형태 고정)
const (
	body200OK = `{"result":"success","action":"in","checkedAt":"2026-09-18T10:23:45+09:00",
	  "attendee":{"fullName":"홍길동","membershipName":"콩콩","membershipPosition":"매니저",
	  "mobileNumber":"01012345678","emailAddress":"hong@example.com"}}`
	body409Dup = `{"message":"already checked in","code":409,"InnerError":{},
	  "data":{"result":"success:duplication","action":"in","checkedAt":"2026-09-18T10:00:00+09:00",
	  "attendee":{"fullName":"홍길동"}}}`
	body404Barcode = `{"message":"cannot find barcode","code":404,"InnerError":{},
	  "data":{"result":"fail:barcode-not-found","action":"","checkedAt":"2026-09-18T10:23:45+09:00",
	  "attendee":{"fullName":""}}}`
	body404Token   = `{"message":"resource not found","code":404,"InnerError":null,"data":null}`
	body406        = `{"message":"barcode invalid","code":406,"data":{"result":"fail:barcode-invalid"}}`
	body424        = `{"message":"no owner","code":424,"data":{"result":"fail:barcode-no-owner"}}`
	body403        = `{"message":"condition","code":403,"data":{"result":"fail:condition"}}`
	body400RFC     = `{"message":"checkedAt must be RFC3339","code":400,"data":null}`
	body400Range   = `{"message":"checkedAt out of range","code":400,"data":{"reason":"too-old"}}`
	body400Bind    = `{"message":"error bind","code":400,"data":null}`
	body400NoUID   = `{"message":"barcodeString or invitationCode required","code":400,"data":null}`
	bodyPreflight  = `{"eventName":"WCE 2026","boothName":"A 게이트","unitName":"본관","action":"in","cooldownSec":60}`
	bodyPreflight0 = `{"eventName":"WCE 2026","boothName":"A 게이트","unitName":"본관","cooldownSec":0}`
)

func TestClassifierTable(t *testing.T) {
	cases := []struct {
		name     string
		res      HTTPResult
		decision domain.DeliveryDecision
		class    string
	}{
		{"200-success", HTTPResult{Status: 200, Body: []byte(body200OK)}, domain.DecisionComplete, ClassCheckinSuccess},
		{"200-malformed", HTTPResult{Status: 200, Body: []byte(`<html>`)}, domain.DecisionDrop, ClassProtocolAnomaly2XX},
		{"200-body-lost", HTTPResult{Status: 200, Body: nil, BodyErr: errors.New("reset")}, domain.DecisionRetry, ClassResponseLost},
		{"409-duplication", HTTPResult{Status: 409, Body: []byte(body409Dup)}, domain.DecisionComplete, ClassDuplicateSuccess},
		{"409-unknown", HTTPResult{Status: 409, Body: []byte(`{"message":"x","code":409,"data":null}`)}, domain.DecisionDrop, ClassUnknown4XX},
		{"404-barcode", HTTPResult{Status: 404, Body: []byte(body404Barcode)}, domain.DecisionDrop, ClassBarcodeNotFound},
		{"404-token", HTTPResult{Status: 404, Body: []byte(body404Token)}, domain.DecisionSuspendReader, ClassTokenRevoked},
		{"404-empty-body", HTTPResult{Status: 404, Body: nil}, domain.DecisionSuspendReader, ClassTokenRevoked},
		{"406", HTTPResult{Status: 406, Body: []byte(body406)}, domain.DecisionDrop, ClassBarcodeInvalid},
		{"424", HTTPResult{Status: 424, Body: []byte(body424)}, domain.DecisionDrop, ClassBarcodeNoOwner},
		{"403", HTTPResult{Status: 403, Body: []byte(body403)}, domain.DecisionDrop, ClassConditionFailed},
		{"400-rfc3339", HTTPResult{Status: 400, Body: []byte(body400RFC)}, domain.DecisionDrop, ClassCheckedAtFormat},
		{"400-range", HTTPResult{Status: 400, Body: []byte(body400Range)}, domain.DecisionDrop, ClassCheckedAtRange},
		{"400-bind", HTTPResult{Status: 400, Body: []byte(body400Bind)}, domain.DecisionHaltGlobal, ClassRequestBindBug},
		{"400-no-uid", HTTPResult{Status: 400, Body: []byte(body400NoUID)}, domain.DecisionDrop, ClassEmptyUIDBug},
		{"400-unknown", HTTPResult{Status: 400, Body: []byte(`{"message":"?","code":400}`)}, domain.DecisionDrop, ClassUnknown4XX},
		{"418-unknown-4xx", HTTPResult{Status: 418, Body: []byte(`{}`)}, domain.DecisionDrop, ClassUnknown4XX},
		{"500", HTTPResult{Status: 500, Body: []byte(`{}`)}, domain.DecisionRetry, ClassServer5XX},
		{"503", HTTPResult{Status: 503, Body: nil}, domain.DecisionRetry, ClassServer5XX},
		{"timeout", HTTPResult{TransportErr: errors.New("request timeout")}, domain.DecisionRetry, ClassNetworkFailure},
		{"malformed-json-4xx", HTTPResult{Status: 403, Body: []byte(`not json`)}, domain.DecisionDrop, ClassUnknown4XX},
		{"302-redirect", HTTPResult{Status: 302, Body: nil}, domain.DecisionDrop, ClassProtocolAnomaly2XX},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, cls := Classify(c.res)
			if d != c.decision || cls != c.class {
				t.Errorf("got (%v, %s), want (%v, %s)", d, cls, c.decision, c.class)
			}
		})
	}
}

// B6: 400 세 종류(시계/자기버그/빈 UID)가 구분된다
func TestAcceptanceB6Distinguishes400s(t *testing.T) {
	d1, c1 := Classify(HTTPResult{Status: 400, Body: []byte(body400RFC)})
	d2, c2 := Classify(HTTPResult{Status: 400, Body: []byte(body400Bind)})
	d3, c3 := Classify(HTTPResult{Status: 400, Body: []byte(body400NoUID)})
	if c1 == c2 || c2 == c3 || c1 == c3 {
		t.Errorf("400 3종이 같은 클래스: %s %s %s", c1, c2, c3)
	}
	if d1 != domain.DecisionDrop || d2 != domain.DecisionHaltGlobal || d3 != domain.DecisionDrop {
		t.Errorf("400 3종 decision 오류: %v %v %v", d1, d2, d3)
	}
}

func TestPreflightMeta(t *testing.T) {
	m, ok := PreflightMeta([]byte(bodyPreflight))
	if !ok || m.BoothName != "A 게이트" || m.CooldownSec != 60 || m.EventName != "WCE 2026" {
		t.Errorf("meta = %+v ok=%v", m, ok)
	}
	m0, ok := PreflightMeta([]byte(bodyPreflight0))
	if !ok || m0.CooldownSec != 0 {
		t.Errorf("cooldown 0 meta = %+v", m0)
	}
	if _, ok := PreflightMeta([]byte(`<html>`)); ok {
		t.Error("malformed 는 실패해야 함")
	}
	if _, ok := PreflightMeta([]byte(`{}`)); ok {
		t.Error("빈 객체는 계약 위반")
	}
}

// --- 클라이언트 통합 (httptest) ---

func TestClientCheckIn(t *testing.T) {
	var gotPath, gotCT, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		b := make([]byte, r.ContentLength)
		r.Body.Read(b)
		gotBody = string(b)
		w.Write([]byte(body200OK))
	}))
	defer srv.Close()

	c, err := New(srv.URL, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	res := c.CheckIn(context.Background(), domain.NewSecret(testToken), "E28068940000501EC2205F7B", "2026-09-01T10:00:00+09:00")
	if res.Status != 200 || res.TransportErr != nil {
		t.Fatalf("res = %+v", res)
	}
	if gotPath != "/v3/pulse-tokens/"+testToken+"/check-in" {
		t.Errorf("path = %s", gotPath)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q — 누락 시 400 error bind (보안 회귀)", gotCT)
	}
	if !strings.Contains(gotBody, `"barcodeString":"E28068940000501EC2205F7B"`) ||
		!strings.Contains(gotBody, `"checkedAt":"2026-09-01T10:00:00+09:00"`) {
		t.Errorf("body = %s", gotBody)
	}
	// 본문은 barcodeString/checkedAt 만 (계획서 §6.4)
	if strings.Contains(gotBody, "boothID") || strings.Contains(gotBody, "action") {
		t.Errorf("금지 필드 포함: %s", gotBody)
	}
}

func TestClientPreflight(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v3/pulse-tokens/"+testToken {
			t.Errorf("잘못된 요청: %s %s", r.Method, r.URL.Path)
		}
		w.Write([]byte(bodyPreflight))
	}))
	defer srv.Close()
	c, _ := New(srv.URL, 10*time.Second)
	res := c.Preflight(context.Background(), domain.NewSecret(testToken))
	if res.Status != 200 {
		t.Fatalf("status = %d", res.Status)
	}
	if m, ok := PreflightMeta(res.Body); !ok || m.BoothName != "A 게이트" {
		t.Errorf("meta 해석 실패: %+v", m)
	}
}

func TestClientTimeoutErrHasNoToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, 50*time.Millisecond)
	res := c.CheckIn(context.Background(), domain.NewSecret(testToken), "ABCD", "2026-09-01T10:00:00+09:00")
	if res.TransportErr == nil {
		t.Fatal("timeout 이어야 함")
	}
	if strings.Contains(res.TransportErr.Error(), testToken) {
		t.Errorf("오류에 토큰 노출: %v", res.TransportErr)
	}
	d, cls := Classify(res)
	if d != domain.DecisionRetry || cls != ClassNetworkFailure {
		t.Errorf("timeout 분류 오류: %v %s", d, cls)
	}
}

func TestClientConnectionRefusedErrHasNoToken(t *testing.T) {
	c, _ := New("http://127.0.0.1:1", 500*time.Millisecond)
	res := c.CheckIn(context.Background(), domain.NewSecret(testToken), "ABCD", "2026-09-01T10:00:00+09:00")
	if res.TransportErr == nil {
		t.Fatal("연결 실패여야 함")
	}
	if strings.Contains(res.TransportErr.Error(), testToken) {
		t.Errorf("오류에 토큰 노출: %v", res.TransportErr)
	}
}

func TestClientDoesNotFollowRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1/evil", http.StatusFound)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, time.Second)
	res := c.CheckIn(context.Background(), domain.NewSecret(testToken), "ABCD", "2026-09-01T10:00:00+09:00")
	if res.Status != http.StatusFound {
		t.Errorf("redirect 를 따라가면 안 됨: %+v", res.Status)
	}
}

func TestClientBodyLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		big := strings.Repeat("A", ResponseBodyLimit*2)
		w.Write([]byte(big))
	}))
	defer srv.Close()
	c, _ := New(srv.URL, time.Second)
	res := c.Preflight(context.Background(), domain.NewSecret(testToken))
	if len(res.Body) > ResponseBodyLimit {
		t.Errorf("body 상한 초과: %d", len(res.Body))
	}
}
