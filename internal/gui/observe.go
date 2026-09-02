package gui

import (
	"bufio"
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// tailLogs 는 관측 모드에서 logs/ 의 사전순 최신 middleware-*.jsonl 을
// 따라간다 (GUI 설계 §2.4). 최초 열기 시 파일 끝으로 이동해 과거 로그를
// 쏟지 않고, 5초마다 회전(더 새로운 파일명)을 확인한다.
// (호스팅 모드는 tail 대신 in-process LogRing 을 쓴다 — §6.3)
func tailLogs(ctx context.Context, dataDir string, srv *Server) {
	dir := filepath.Join(dataDir, "logs")
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
					srv.PushLog(line[:len(line)-1])
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
