package gui

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
)

//go:embed assets/index.html assets/app.js
var assets embed.FS

// Meta 는 /api/meta 응답이다 (GUI 설계 §4.2).
type Meta struct {
	Version        string `json:"version"`
	CfgFingerprint string `json:"cfgFingerprint,omitempty"`
	Mode           string `json:"mode"`
	Port           int    `json:"port"`
	ConfigPath     string `json:"configPath,omitempty"`
	DataDir        string `json:"dataDir,omitempty"`
	ConfigError    string `json:"configError,omitempty"`
	DataDirError   string `json:"dataDirError,omitempty"` // 데이터 폴더 쓰기 불가(권한 등)
}

type sseMsg struct {
	event string
	data  []byte
}

// Server 는 127.0.0.1 전용 GUI 백엔드다 (GUI 설계 §4.1).
type Server struct {
	nonce string
	ln    net.Listener
	meta  Meta

	mu      sync.Mutex
	state   State
	subs    map[int]chan sseMsg
	nextSub int

	// ServiceControl 은 "start"|"stop" 을 수행한다 (관측 모드 — CLI+UAC).
	ServiceControl func(action string) error
	// Hooks 는 오케스트레이터(guiApp)가 꽂는 동작들이다.
	Hooks Hooks
}

type apiError struct {
	Code    string
	Message string
}

type Hooks struct {
	ApplySession func(readerID, sessionID string) (any, *apiError)
	Resume       func(readerID, pending, sessionID string) (any, *apiError)
	CoreControl  func(action string) *apiError
	CatalogView   func() any
	CatalogOp     func(op string) *apiError
	CatalogUpload func(content []byte) *apiError
	ConfigView    func() any
	ConfigSave    func(e ConfigEdit) (any, *apiError)
	WizardStart   func(readerID string, steps []string) *apiError
	WizardConfirm func()
	WizardAbort   func()
	WizardState   func() any
	WizardReport  func() (any, *apiError)
}

func NewServer(meta Meta) (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		ln.Close()
		return nil, err
	}
	meta.Port = ln.Addr().(*net.TCPAddr).Port
	s := &Server{nonce: hex.EncodeToString(b), ln: ln, meta: meta, subs: map[int]chan sseMsg{}}
	return s, nil
}

// URL 은 브라우저로 열 진입점이다 (nonce 포함 — GUI 설계 §4.1).
func (s *Server) URL() string {
	return fmt.Sprintf("http://127.0.0.1:%d/%s/", s.meta.Port, s.nonce)
}

func (s *Server) Serve() error {
	mux := http.NewServeMux()
	prefix := "/" + s.nonce
	mux.HandleFunc(prefix+"/", s.guard(s.handleIndex))
	mux.HandleFunc(prefix+"/app.js", s.guard(func(w http.ResponseWriter, r *http.Request) {
		b, _ := assets.ReadFile("assets/app.js")
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Write(b)
	}))
	mux.HandleFunc(prefix+"/api/state", s.guard(s.handleState))
	mux.HandleFunc(prefix+"/api/meta", s.guard(s.handleMeta))
	mux.HandleFunc(prefix+"/api/system", s.guard(func(w http.ResponseWriter, r *http.Request) {
		installed, auto, running := autoStartInfo()
		writeOK(w, map[string]any{"serviceInstalled": installed, "autoStart": auto, "serviceRunning": running, "mode": s.meta.Mode})
	}))
	mux.HandleFunc(prefix+"/api/events", s.guard(s.handleEvents))
	mux.HandleFunc(prefix+"/api/control/service", s.guard(s.handleServiceControl))
	mux.HandleFunc("GET "+prefix+"/api/catalog", s.guard(func(w http.ResponseWriter, r *http.Request) {
		if s.Hooks.CatalogView == nil {
			writeErr(w, 400, "catalog_error", "미지원")
			return
		}
		writeOK(w, s.Hooks.CatalogView())
	}))
	mux.HandleFunc("POST "+prefix+"/api/catalog/{op}", s.guard(func(w http.ResponseWriter, r *http.Request) {
		if !s.confirmed(w, r) {
			return
		}
		if e := s.Hooks.CatalogOp(r.PathValue("op")); e != nil {
			writeErr(w, 400, e.Code, e.Message)
			return
		}
		writeOK(w, s.Hooks.CatalogView())
	}))
	mux.HandleFunc("POST "+prefix+"/api/catalog-upload", s.guard(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Content string `json:"content"`
			Confirm bool   `json:"confirm"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !body.Confirm || body.Content == "" {
			writeErr(w, 400, "invalid_request", "파일 내용이 필요합니다")
			return
		}
		if e := s.Hooks.CatalogUpload([]byte(body.Content)); e != nil {
			writeErr(w, 400, e.Code, e.Message)
			return
		}
		writeOK(w, s.Hooks.CatalogView())
	}))
	mux.HandleFunc("POST "+prefix+"/api/readers/{id}/session", s.guard(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			SessionID string `json:"sessionId"`
			Confirm   bool   `json:"confirm"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !body.Confirm {
			writeErr(w, 400, "confirm_required", "확인이 필요합니다")
			return
		}
		res, e := s.Hooks.ApplySession(r.PathValue("id"), body.SessionID)
		if e != nil {
			writeErr(w, 400, e.Code, e.Message)
			return
		}
		writeOK(w, res)
	}))
	mux.HandleFunc("POST "+prefix+"/api/readers/{id}/resume", s.guard(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Pending   string `json:"pending"`
			SessionID string `json:"sessionId"`
			Confirm   bool   `json:"confirm"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !body.Confirm {
			writeErr(w, 400, "confirm_required", "확인이 필요합니다")
			return
		}
		if body.Pending != "send" && body.Pending != "discard" {
			writeErr(w, 400, "invalid_request", "pending 은 send|discard")
			return
		}
		res, e := s.Hooks.Resume(r.PathValue("id"), body.Pending, body.SessionID)
		if e != nil {
			writeErr(w, 400, e.Code, e.Message)
			return
		}
		writeOK(w, res)
	}))
	mux.HandleFunc("GET "+prefix+"/api/config", s.guard(func(w http.ResponseWriter, r *http.Request) {
		if s.Hooks.ConfigView == nil {
			writeErr(w, 400, "invalid_request", "미지원")
			return
		}
		writeOK(w, s.Hooks.ConfigView())
	}))
	mux.HandleFunc("POST "+prefix+"/api/config", s.guard(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Config  ConfigEdit `json:"config"`
			Confirm bool       `json:"confirm"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !body.Confirm {
			writeErr(w, 400, "confirm_required", "확인이 필요합니다")
			return
		}
		res, e := s.Hooks.ConfigSave(body.Config)
		if e != nil {
			writeErr(w, 400, e.Code, e.Message)
			return
		}
		writeOK(w, res)
	}))
	mux.HandleFunc("GET "+prefix+"/api/wizard", s.guard(func(w http.ResponseWriter, r *http.Request) {
		writeOK(w, s.Hooks.WizardState())
	}))
	mux.HandleFunc("POST "+prefix+"/api/wizard/start", s.guard(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ReaderID string   `json:"readerId"`
			Steps    []string `json:"steps"`
			Confirm  bool     `json:"confirm"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !body.Confirm {
			writeErr(w, 400, "confirm_required", "확인이 필요합니다")
			return
		}
		if len(body.Steps) == 0 {
			writeErr(w, 400, "invalid_request", "점검 단계를 선택하세요")
			return
		}
		if e := s.Hooks.WizardStart(body.ReaderID, body.Steps); e != nil {
			writeErr(w, 400, e.Code, e.Message)
			return
		}
		writeOK(w, s.Hooks.WizardState())
	}))
	mux.HandleFunc("POST "+prefix+"/api/wizard/confirm", s.guard(func(w http.ResponseWriter, r *http.Request) {
		if !s.confirmed(w, r) {
			return
		}
		s.Hooks.WizardConfirm()
		writeOK(w, s.Hooks.WizardState())
	}))
	mux.HandleFunc("POST "+prefix+"/api/wizard/abort", s.guard(func(w http.ResponseWriter, r *http.Request) {
		s.Hooks.WizardAbort()
		writeOK(w, map[string]bool{"aborted": true})
	}))
	mux.HandleFunc("POST "+prefix+"/api/wizard/report", s.guard(func(w http.ResponseWriter, r *http.Request) {
		res, e := s.Hooks.WizardReport()
		if e != nil {
			writeErr(w, 400, e.Code, e.Message)
			return
		}
		writeOK(w, res)
	}))
	mux.HandleFunc("POST "+prefix+"/api/control/core", s.guard(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Action  string `json:"action"`
			Confirm bool   `json:"confirm"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !body.Confirm {
			writeErr(w, 400, "confirm_required", "확인이 필요합니다")
			return
		}
		if e := s.Hooks.CoreControl(body.Action); e != nil {
			writeErr(w, 400, e.Code, e.Message)
			return
		}
		writeOK(w, map[string]string{"action": body.Action})
	}))
	// nonce 없는 모든 경로는 404 (기본 mux 동작)
	return http.Serve(s.ln, mux)
}

// guard 는 Host 검증 + (POST) Origin 동일 출처 검증이다 (GUI 설계 §4.1, G13).
func (s *Server) guard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wantHost := fmt.Sprintf("127.0.0.1:%d", s.meta.Port)
		if r.Host != wantHost {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet {
			if o := r.Header.Get("Origin"); o != "" && o != "http://"+wantHost {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:")
		h(w, r)
	}
}

// confirmed 는 body 의 confirm:true 를 강제한다 (단순 op 용).
func (s *Server) confirmed(w http.ResponseWriter, r *http.Request) bool {
	var body struct {
		Confirm bool `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !body.Confirm {
		writeErr(w, 400, "confirm_required", "확인이 필요합니다")
		return false
	}
	return true
}

func writeOK(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": data})
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"ok": false, "error": map[string]string{"code": code, "message": msg},
	})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, "/") {
		http.NotFound(w, r)
		return
	}
	b, _ := assets.ReadFile("assets/index.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(b)
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	st := s.state
	s.mu.Unlock()
	writeOK(w, st)
}

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	writeOK(w, s.meta)
}

func (s *Server) handleServiceControl(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "invalid_request", "POST 만 허용됩니다")
		return
	}
	var body struct {
		Action  string `json:"action"`
		Confirm bool   `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "invalid_request", "본문 파싱 실패")
		return
	}
	if !body.Confirm {
		writeErr(w, 400, "confirm_required", "확인이 필요합니다")
		return
	}
	switch body.Action {
	case "start", "stop", "auto-on", "auto-off":
	default:
		writeErr(w, 400, "invalid_request", "action 은 start|stop|auto-on|auto-off")
		return
	}
	if s.ServiceControl == nil {
		writeErr(w, 400, "invalid_request", "이 모드에서는 서비스 제어를 지원하지 않습니다")
		return
	}
	if err := s.ServiceControl(body.Action); err != nil {
		writeErr(w, 500, "uac_denied", "실행 실패: "+err.Error())
		return
	}
	writeOK(w, map[string]string{"action": body.Action})
}

// PushState 는 상태를 갱신하고 SSE 구독자에게 알린다.
func (s *Server) PushState(st State) {
	b, err := json.Marshal(st)
	if err != nil {
		return
	}
	s.mu.Lock()
	s.state = st
	s.mu.Unlock()
	s.broadcast(sseMsg{event: "state", data: b})
}

// PushLog 는 마스킹된 로그 1행을 SSE 로 보낸다.
func (s *Server) PushLog(line []byte) {
	s.broadcast(sseMsg{event: "log", data: maskLogLine(line)})
}

func (s *Server) broadcast(m sseMsg) {
	s.mu.Lock()
	for _, ch := range s.subs {
		select {
		case ch <- m:
		default: // 느린 구독자는 드랍 — 코어/관측 루프를 블록하지 않는다
		}
	}
	s.mu.Unlock()
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "internal", "streaming 미지원")
		return
	}
	ch := make(chan sseMsg, 256)
	s.mu.Lock()
	id := s.nextSub
	s.nextSub++
	s.subs[id] = ch
	st := s.state
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.subs, id)
		s.mu.Unlock()
	}()

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	// 접속 즉시 현재 상태 1회
	if b, err := json.Marshal(st); err == nil {
		fmt.Fprintf(w, "event: state\ndata: %s\n\n", b)
		fl.Flush()
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case m := <-ch:
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", m.event, m.data)
			fl.Flush()
		}
	}
}
