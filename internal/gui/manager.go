package gui

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// CatalogManager 는 pulse-sessions.json 의 상시 감시자다 (GUI 설계 §3.3):
// mtime 2초 폴링(200ms 디바운스, 실패 시 1s 재시도 1회) + Downloads 신규 파일
// 감지. 파싱 오류 시 마지막 정상본을 유지한다.
type CatalogManager struct {
	Path         string
	DownloadsDir string // 빈 값이면 Downloads 감시 안 함
	OnChange     func(reason string)

	mu            sync.Mutex
	cat           *Catalog
	loadErr       string
	pendingImport string // Downloads 에서 발견된 가져오기 후보
	startedAt     time.Time
}

func (m *CatalogManager) Run(ctx context.Context) {
	m.startedAt = time.Now()
	m.reload(false)
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	dl := time.NewTicker(3 * time.Second)
	defer dl.Stop()
	var lastMod time.Time
	if fi, err := os.Stat(m.Path); err == nil {
		lastMod = fi.ModTime()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			fi, err := os.Stat(m.Path)
			if err != nil {
				continue
			}
			if !fi.ModTime().Equal(lastMod) {
				lastMod = fi.ModTime()
				time.Sleep(200 * time.Millisecond) // 쓰기 도중 읽기 회피 (§3.3)
				m.reload(true)
			}
		case <-dl.C:
			m.scanDownloads()
		}
	}
}

func (m *CatalogManager) reload(retry bool) {
	cat, err := LoadCatalog(m.Path)
	if err != nil && retry {
		time.Sleep(time.Second)
		cat, err = LoadCatalog(m.Path)
	}
	m.mu.Lock()
	if err != nil {
		if os.IsNotExist(err) {
			m.loadErr = ""
		} else {
			m.loadErr = err.Error() // 마지막 정상본(m.cat)은 유지 (§3.3)
		}
	} else {
		m.cat, m.loadErr = cat, ""
	}
	m.mu.Unlock()
	if m.OnChange != nil {
		if err != nil && !os.IsNotExist(err) {
			m.OnChange("parse_error")
		} else if err == nil {
			m.OnChange("reloaded")
		}
	}
}

// scanDownloads 는 기동 이후 생성된 pulse-sessions*.json 을 찾는다 (§3.3).
func (m *CatalogManager) scanDownloads() {
	if m.DownloadsDir == "" {
		return
	}
	matches, _ := filepath.Glob(filepath.Join(m.DownloadsDir, "pulse-sessions*.json"))
	var candidates []string
	for _, p := range matches {
		fi, err := os.Stat(p)
		if err != nil || fi.ModTime().Before(m.startedAt) {
			continue
		}
		// 브라우저 임시 파일 제외
		if strings.HasSuffix(p, ".crdownload") || strings.HasSuffix(p, ".part") {
			continue
		}
		candidates = append(candidates, p)
	}
	if len(candidates) == 0 {
		return
	}
	sort.Slice(candidates, func(i, j int) bool {
		fi, _ := os.Stat(candidates[i])
		fj, _ := os.Stat(candidates[j])
		return fi.ModTime().After(fj.ModTime())
	})
	m.mu.Lock()
	newFound := m.pendingImport != candidates[0]
	m.pendingImport = candidates[0]
	m.mu.Unlock()
	if newFound && m.OnChange != nil {
		m.OnChange("download_found")
	}
}

// Import 는 발견된 파일을 카탈로그 위치로 이동한다 (복사 후 원본 삭제 —
// 토큰 파일을 Downloads 에 방치하지 않는다, §3.3).
func (m *CatalogManager) Import() error {
	m.mu.Lock()
	src := m.pendingImport
	m.mu.Unlock()
	if src == "" {
		return os.ErrNotExist
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Dir(m.Path)); err != nil {
		return err
	}
	if err := os.WriteFile(m.Path, b, 0o600); err != nil {
		return err
	}
	os.Remove(src)
	m.mu.Lock()
	m.pendingImport = ""
	m.mu.Unlock()
	m.reload(true)
	return nil
}

// Snapshot 은 (카탈로그, 로드오류, 가져오기 후보 존재) 를 돌려준다.
func (m *CatalogManager) Snapshot() (*Catalog, string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cat, m.loadErr, m.pendingImport != ""
}
