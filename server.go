package butcherie

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/tebeka/selenium"
)

// buildMux registers all HTTP handlers and returns the mux.
func (s *Server) buildMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/navigate", s.handleNavigate)
	mux.HandleFunc("/current_page/click", s.handleClick)
	mux.HandleFunc("/current_page/source", s.handleSource)
	mux.HandleFunc("/shutdown", s.handleShutdown)
	return mux
}

// writeJSON encodes v as JSON and writes it with the given HTTP status code.
func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// handleStatus handles GET /status.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Status())
}

// handleNavigate handles POST /navigate.
func (s *Server) handleNavigate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		URL string `json:"url"`
		LoadOptions
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, PageResponse{Errors: []string{"invalid request: " + err.Error()}})
		return
	}
	if req.URL == "" {
		writeJSON(w, http.StatusBadRequest, PageResponse{Errors: []string{"url is required"}})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Subscribe to document response before navigating.
	captureResp := s.cdp.captureDocumentResponse()

	if err := s.wd.Get(req.URL); err != nil {
		writeJSON(w, http.StatusInternalServerError, PageResponse{Errors: []string{err.Error()}})
		return
	}

	time.Sleep(s.cfg.PostActionDelay)

	ctx := r.Context()
	loadErr := s.ensureLoaded(ctx, req.LoadOptions)

	// Collect page response (status code + headers).
	respDone := make(chan struct{})
	close(respDone) // already done navigating; drain immediately
	statusCode, headers := captureResp(respDone)

	html, err := s.wd.PageSource()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, PageResponse{Errors: []string{err.Error()}})
		return
	}

	resp := PageResponse{
		HTML:       html,
		StatusCode: statusCode,
		Headers:    headers,
	}
	httpStatus := http.StatusOK
	if loadErr != nil {
		resp.Errors = []string{loadErr.Error()}
		if isDeadlineErr(loadErr) {
			httpStatus = http.StatusGatewayTimeout
		}
	}
	writeJSON(w, httpStatus, resp)
}

// handleClick handles POST /current_page/click.
func (s *Server) handleClick(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		By    string `json:"by"`
		Value string `json:"value"`
		LoadOptions
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, PageResponse{Errors: []string{"invalid request: " + err.Error()}})
		return
	}
	if req.By == "" || req.Value == "" {
		writeJSON(w, http.StatusBadRequest, PageResponse{Errors: []string{"by and value are required"}})
		return
	}

	by := seleniumBy(req.By)
	if by == "" {
		writeJSON(w, http.StatusBadRequest, PageResponse{Errors: []string{"unsupported by strategy: " + req.By}})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	elem, err := s.wd.FindElement(by, req.Value)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, PageResponse{Errors: []string{"element not found: " + err.Error()}})
		return
	}

	captureResp := s.cdp.captureDocumentResponse()

	if err := elem.Click(); err != nil {
		writeJSON(w, http.StatusInternalServerError, PageResponse{Errors: []string{"click failed: " + err.Error()}})
		return
	}

	time.Sleep(s.cfg.PostActionDelay)

	ctx := r.Context()
	loadErr := s.ensureLoaded(ctx, req.LoadOptions)

	respDone := make(chan struct{})
	close(respDone)
	statusCode, headers := captureResp(respDone)

	html, err := s.wd.PageSource()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, PageResponse{Errors: []string{err.Error()}})
		return
	}

	resp := PageResponse{
		HTML:       html,
		StatusCode: statusCode,
		Headers:    headers,
	}
	httpStatus := http.StatusOK
	if loadErr != nil {
		resp.Errors = []string{loadErr.Error()}
		if isDeadlineErr(loadErr) {
			httpStatus = http.StatusGatewayTimeout
		}
	}
	writeJSON(w, httpStatus, resp)
}

// handleSource handles GET /current_page/source.
func (s *Server) handleSource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	html, err := s.wd.PageSource()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, PageResponse{Errors: []string{err.Error()}})
		return
	}
	writeJSON(w, http.StatusOK, PageResponse{HTML: html})
}

// handleShutdown handles POST /shutdown.
func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "shutting down"})
	go func() {
		time.Sleep(500 * time.Millisecond)
		_ = s.Shutdown()
	}()
}

// seleniumBy maps the API's "by" strategy strings to selenium constants.
func seleniumBy(by string) string {
	switch by {
	case "link_text":
		return selenium.ByLinkText
	case "partial_link_text":
		return selenium.ByPartialLinkText
	case "id":
		return selenium.ByID
	case "css":
		return selenium.ByCSSSelector
	case "xpath":
		return selenium.ByXPATH
	case "name":
		return selenium.ByName
	case "class_name":
		return selenium.ByClassName
	case "tag_name":
		return selenium.ByTagName
	default:
		return ""
	}
}

func isDeadlineErr(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}
