// Package replay 는 stdin/파일을 TCP 리더와 동일한 event 경로로 재생한다 (설계서 §10).
// CRLF framer 이후의 전체 경로(파서→admission→sender)는 운영과 같다.
package replay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/clock"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/domain"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/logging"
	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/reader/protocol"
)

type Handler func(domain.TagRead)

type Runner struct {
	ReaderID string
	Handler  Handler
	Log      *logging.Logger
	Clock    clock.Clock
}

// ndjsonRecord 는 chunk 경계와 수신 시각을 재현하는 확장 fixture 형식이다.
// {"receivedAt":"RFC3339","chunks":["3e54...","..."]}
type ndjsonRecord struct {
	ReceivedAt string   `json:"receivedAt"`
	Chunks     []string `json:"chunks"`
}

// RunRaw 는 일반 raw 라인 파일을 재생한다. 라인에 CRLF 가 없으면 delimiter 를
// 붙이며, 재생 순간을 receivedAt 으로 사용한다.
func (r *Runner) RunRaw(ctx context.Context, in io.Reader) error {
	framer := protocol.NewFramer(protocol.DefaultMaxLine)
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64*1024), 64*1024)
	for sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := bytes.TrimRight(sc.Bytes(), "\r")
		chunk := append(append([]byte{}, line...), 0x0D, 0x0A)
		r.consume(framer.Push(chunk), time.Time{})
	}
	return sc.Err()
}

// RunNDJSON 은 확장 fixture 를 재생한다. 각 record 의 receivedAt 을 태그
// 수신 시각으로 사용해 오프라인 시나리오의 원래 시각을 재현한다.
func (r *Runner) RunNDJSON(ctx context.Context, in io.Reader) error {
	framer := protocol.NewFramer(protocol.DefaultMaxLine)
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		if ctx.Err() != nil {
			return ctx.Err()
		}
		text := bytes.TrimSpace(sc.Bytes())
		if len(text) == 0 {
			continue
		}
		var rec ndjsonRecord
		if err := json.Unmarshal(text, &rec); err != nil {
			return fmt.Errorf("fixture %d행 파싱 실패: %w", lineNo, err)
		}
		var at time.Time
		if rec.ReceivedAt != "" {
			t, err := time.Parse(time.RFC3339, rec.ReceivedAt)
			if err != nil {
				return fmt.Errorf("fixture %d행 receivedAt: %w", lineNo, err)
			}
			at = t
		}
		for _, ch := range rec.Chunks {
			raw, err := hex.DecodeString(ch)
			if err != nil {
				return fmt.Errorf("fixture %d행 chunk hex: %w", lineNo, err)
			}
			r.consume(framer.Push(raw), at)
		}
	}
	return sc.Err()
}

func (r *Runner) consume(res protocol.FrameResult, at time.Time) {
	for i := 0; i < res.Dropped; i++ {
		r.Log.Warnf("READER_LINE_TOO_LONG", logging.F{"readerId": r.ReaderID})
	}
	for _, raw := range res.Lines {
		line := protocol.Parse(raw)
		switch line.Kind {
		case protocol.KindTag:
			receivedAt := at
			if receivedAt.IsZero() {
				receivedAt = r.Clock.Now()
			}
			r.Handler(domain.TagRead{
				ReaderID:   r.ReaderID,
				EPC:        line.Tag.EPC,
				ReceivedAt: receivedAt,
				RawLine:    line.Raw,
			})
		case protocol.KindBadTag:
			r.Log.Warnf("READER_BAD_TAG", logging.F{"readerId": r.ReaderID, "raw": line.Raw, "message": line.Err})
		case protocol.KindUnknown:
			r.Log.Warnf("READER_UNKNOWN_LINE", logging.F{"readerId": r.ReaderID, "raw": line.Raw})
		default:
			r.Log.Debugf("READER_RAW", logging.F{"readerId": r.ReaderID, "raw": line.Raw})
		}
	}
}
