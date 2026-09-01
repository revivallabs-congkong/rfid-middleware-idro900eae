// Package congkong 은 CongKong 서버와의 HTTP 계약을 구현한다.
// SSOT: docs/features/pulse/rfid-middleware-protocol.ko.md v1.1
// URL·token·request body·response body 는 로그로 내보내지 않는다 (설계서 §8.1).
package congkong

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/domain"
)

const (
	// ResponseBodyLimit 은 response body 읽기 상한이다 (설계서 §8.1: 64 KiB).
	ResponseBodyLimit = 64 * 1024
)

type Client struct {
	base    *url.URL
	hc      *http.Client
	timeout time.Duration
}

func New(apiHost string, timeout time.Duration) (*Client, error) {
	u, err := url.Parse(apiHost)
	if err != nil {
		return nil, fmt.Errorf("apiHost 파싱 실패")
	}
	return &Client{
		base: u,
		hc: &http.Client{
			// redirect 자동 추적 금지 — POST 의 301/302 가 token 을 다른 위치로
			// 전달하는 것을 막는다 (설계서 §8.1).
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		timeout: timeout,
	}, nil
}

// ProbeSkew 는 서버 Date 헤더와 로컬 시각의 차이를 잰다 (GUI 설계 §6.9).
// 토큰 없는 무해 GET 1회 — 상태 코드는 무관하고 Date 헤더만 쓴다.
func (c *Client) ProbeSkew(ctx context.Context) (time.Duration, bool) {
	u := *c.base
	u.Path = "/v3"
	res := c.do(ctx, "GET", u.String(), nil)
	return res.DateSkew, res.HasDateSkew
}

// tokenPath 는 token 원문이 URL 에 들어가는 유일한 지점이다.
func (c *Client) tokenPath(token domain.Secret, suffix string) string {
	u := *c.base
	u.Path = "/v3/pulse-tokens/" + token.Raw() + suffix
	return u.String()
}

// HTTPResult 는 한 번의 요청 결과다. TransportErr 는 URL(token) 을 포함하지
// 않도록 정화된 오류다.
type HTTPResult struct {
	Status       int
	Body         []byte
	BodyErr      error
	TransportErr error
	// DateSkew 는 서버 Date 헤더와 로컬 시각의 차이 (보조 진단, 설계서 §12.1).
	DateSkew    time.Duration
	HasDateSkew bool
}

// CheckIn 은 단건 체크인 POST 다. 한 요청당 한 EPC (R2).
func (c *Client) CheckIn(ctx context.Context, token domain.Secret, epc, checkedAt string) HTTPResult {
	body, err := json.Marshal(struct {
		BarcodeString string `json:"barcodeString"`
		CheckedAt     string `json:"checkedAt"`
	}{BarcodeString: epc, CheckedAt: checkedAt})
	if err != nil {
		return HTTPResult{TransportErr: fmt.Errorf("request encode 실패: %w", err)}
	}
	return c.do(ctx, http.MethodPost, c.tokenPath(token, "/check-in"), body)
}

// Preflight 는 기동 시 토큰 자기 검증 GET 이다 (계획서 §6.4).
func (c *Client) Preflight(ctx context.Context, token domain.Secret) HTTPResult {
	return c.do(ctx, http.MethodGet, c.tokenPath(token, ""), nil)
}

func (c *Client) do(ctx context.Context, method, fullURL string, body []byte) HTTPResult {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, fullURL, reader)
	if err != nil {
		return HTTPResult{TransportErr: sanitizeErr(err)}
	}
	if body != nil {
		// Content-Type 은 request builder 내부에서 항상 설정한다 (설계서 §8.1).
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return HTTPResult{TransportErr: sanitizeErr(err)}
	}
	defer resp.Body.Close()

	res := HTTPResult{Status: resp.StatusCode}
	if d := resp.Header.Get("Date"); d != "" {
		if serverTime, perr := http.ParseTime(d); perr == nil {
			res.DateSkew = time.Since(serverTime)
			res.HasDateSkew = true
		}
	}
	b, rerr := io.ReadAll(io.LimitReader(resp.Body, ResponseBodyLimit))
	res.Body = b
	if rerr != nil {
		res.BodyErr = sanitizeErr(rerr)
	}
	return res
}

// sanitizeErr 는 오류 문자열에서 URL(token 포함)을 제거한다.
// url.Error 는 전체 URL 을 포함하므로 내부 오류만 남긴다.
func sanitizeErr(err error) error {
	if err == nil {
		return nil
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		inner := ue.Err
		if errors.Is(inner, context.DeadlineExceeded) {
			return errors.New("request timeout")
		}
		return fmt.Errorf("%s 실패: %v", ue.Op, inner)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("request timeout")
	}
	return err
}
