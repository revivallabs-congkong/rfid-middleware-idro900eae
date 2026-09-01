package gui

import (
	"bufio"
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/revivallabs-congkong/rfid-middleware-idro900eae/internal/health"
)

// Observer 는 관측 모드의 데이터 원천이다 (GUI 설계 §4.3, §2.4):
// status.json 1초 폴링 + 당일 로그 파일 tail.
type Observer struct {
	DataDir          string
	Mode             string
	QueueMaxAgeHours int
	Server           *Server
}

func (o *Observer) Run(ctx context.Context) {
	go o.tailLogs(ctx)
	statusPath := filepath.Join(o.DataDir, "status.json")
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var lastPush time.Time
	var lastState string
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		now := time.Now()
		s, err := health.ReadStatus(statusPath)
		st := BuildState(s, err, o.Mode, now, o.QueueMaxAgeHours)
		// 변경 시 즉시, 아니면 5초 하트비트
		key := st.Signal + st.Headline + st.UpdatedAt
		if key != lastState || now.Sub(lastPush) >= 5*time.Second {
			o.Server.PushState(st)
			lastState, lastPush = key, now
		}
	}
}

// tailLogs 는 logs/ 의 사전순 최신 middleware-*.jsonl 을 따라간다 (GUI 설계 §2.4).
// 최초 열기 시 파일 끝으로 이동해 과거 로그를 쏟지 않는다.
func (o *Observer) tailLogs(ctx context.Context) {
	dir := filepath.Join(o.DataDir, "logs")
	var f *os.File
	var rd *bufio.Reader
	var current string
	defer func() {
		if f != nil {
			f.Close()
		}
	}()
	checkNewer := time.NewTicker(5 * time.Second)
	defer checkNewer.Stop()
	poll := time.NewTicker(300 * time.Millisecond)
	defer poll.Stop()

	openLatest := func(seekEnd bool) {
		latest := latestLogFile(dir)
		if latest == "" || latest == current {
			return
		}
		if f != nil {
			f.Close()
			f = nil
		}
		nf, err := os.Open(latest)
		if err != nil {
			return
		}
		if seekEnd {
			nf.Seek(0, io.SeekEnd)
		}
		f, rd, current = nf, bufio.NewReader(nf), latest
	}
	openLatest(true)

	for {
		select {
		case <-ctx.Done():
			return
		case <-checkNewer.C:
			openLatest(false) // 회전된 새 파일은 처음부터 (전환 이후 행)
		case <-poll.C:
			if rd == nil {
				openLatest(true)
				continue
			}
			for {
				line, err := rd.ReadBytes('\n')
				if len(line) > 1 {
					o.Server.PushLog(line[:len(line)-1])
				}
				if err != nil {
					break
				}
			}
		}
	}
}

func latestLogFile(dir string) string {
	matches, err := filepath.Glob(filepath.Join(dir, "middleware-*.jsonl"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	sort.Strings(matches)
	return matches[len(matches)-1]
}
