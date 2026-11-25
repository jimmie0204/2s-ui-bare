package main

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const maxBodySize = 32 << 20

type AgentOptions struct {
	ConfigPath   string
	BinaryPath   string
	ServiceName  string
	TokenFile    string
	V2RayAPI     string
	MetadataFile string
}

type Agent struct {
	opts  AgentOptions
	token []byte
	mu    sync.Mutex
}

func NewAgent(opts AgentOptions) (*Agent, error) {
	if opts.ConfigPath == "" || opts.BinaryPath == "" || opts.ServiceName == "" || opts.TokenFile == "" {
		return nil, errors.New("config, binary, service and token-file are required")
	}
	token, err := os.ReadFile(opts.TokenFile)
	if err != nil {
		return nil, fmt.Errorf("read token file: %w", err)
	}
	token = bytes.TrimSpace(token)
	if len(token) < 32 {
		return nil, errors.New("token must contain at least 32 bytes")
	}
	return &Agent{opts: opts, token: token}, nil
}

func (a *Agent) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", a.auth(a.health))
	mux.HandleFunc("GET /v1/info", a.auth(a.info))
	mux.HandleFunc("GET /v1/inbounds", a.auth(a.inbounds))
	mux.HandleFunc("GET /v1/stats", a.auth(a.stats))
	mux.HandleFunc("POST /v1/config/validate", a.auth(a.validate))
	mux.HandleFunc("POST /v1/config/apply", a.auth(a.apply))
	mux.HandleFunc("POST /v1/core/restart", a.auth(a.restart))
	// 兼容 2S-UI master 当前 NodeService 使用的 apiv2 接口。
	mux.HandleFunc("GET /app/apiv2/status", a.auth(a.compatStatus))
	mux.HandleFunc("GET /app/apiv2/onlines", a.auth(a.compatOnlines))
	mux.HandleFunc("GET /app/apiv2/inbounds", a.auth(a.compatInbounds))
	mux.HandleFunc("GET /app/apiv2/clients", a.auth(a.compatClients))
	mux.HandleFunc("POST /app/apiv2/save", a.auth(a.compatSave))
	mux.HandleFunc("POST /app/apiv2/restartSb", a.auth(a.restart))
	return securityHeaders(mux)
}

func (a *Agent) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		value := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(value, prefix) || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(strings.TrimPrefix(value, prefix))), a.token) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *Agent) health(w http.ResponseWriter, _ *http.Request) {
	result := map[string]any{
		"ok":    true,
		"agent": "singbox-agent",
		"time":  time.Now().Unix(),
		"core":  map[string]any{"running": a.coreRunning()},
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *Agent) info(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"agent":       "singbox-agent",
		"config_path": a.opts.ConfigPath,
		"service":     a.opts.ServiceName,
		"core_binary": a.opts.BinaryPath,
		"v2ray_api":   a.opts.V2RayAPI != "",
	})
}

type inboundSummary struct {
	Tag   string `json:"tag"`
	Type  string `json:"type"`
	Users []any  `json:"users,omitempty"`
}

func (a *Agent) inbounds(w http.ResponseWriter, _ *http.Request) {
	config, err := a.readConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var root struct {
		Inbounds []map[string]any `json:"inbounds"`
	}
	if err := json.Unmarshal(config, &root); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid sing-box JSON: "+err.Error())
		return
	}
	result := make([]inboundSummary, 0, len(root.Inbounds))
	for _, raw := range root.Inbounds {
		summary := inboundSummary{}
		if value, ok := raw["tag"].(string); ok {
			summary.Tag = value
		}
		if value, ok := raw["type"].(string); ok {
			summary.Type = value
		}
		if value, ok := raw["users"].([]any); ok {
			summary.Users = value
		}
		result = append(result, summary)
	}
	writeJSON(w, http.StatusOK, map[string]any{"inbounds": result})
}

type configRequest struct {
	Config  json.RawMessage `json:"config"`
	Restart bool            `json:"restart"`
}

func (a *Agent) validate(w http.ResponseWriter, r *http.Request) {
	request, err := decodeConfigRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.validateConfig(request.Config); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"valid": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": true})
}

func (a *Agent) apply(w http.ResponseWriter, r *http.Request) {
	request, err := decodeConfigRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	request.Config = a.syncStatsUsers(request.Config)
	if err := a.validateConfig(request.Config); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	backup, err := a.atomicWrite(request.Config)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if request.Restart {
		if err := a.restartCore(); err != nil {
			_ = os.WriteFile(a.opts.ConfigPath, backup, 0600)
			writeError(w, http.StatusBadGateway, "core restart failed; configuration rolled back: "+err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"applied": true, "restarted": request.Restart})
}

func (a *Agent) restart(w http.ResponseWriter, _ *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.restartCore(); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"restarted": true})
}

func decodeConfigRequest(r *http.Request) (configRequest, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodySize+1))
	if err != nil {
		return configRequest{}, err
	}
	if len(body) > maxBodySize {
		return configRequest{}, errors.New("request body too large")
	}
	var request configRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return configRequest{}, err
	}
	if len(request.Config) == 0 || !json.Valid(request.Config) {
		return configRequest{}, errors.New("config must be valid JSON")
	}
	return request, nil
}

func (a *Agent) readConfig() ([]byte, error) { return os.ReadFile(a.opts.ConfigPath) }

func (a *Agent) validateConfig(config []byte) error {
	cmd := exec.Command(a.opts.BinaryPath, "check", "-c", a.opts.ConfigPath)
	cmd.Stdin = bytes.NewReader(config)
	// sing-box check reads the configured path, so validate the candidate through
	// a private temporary file instead of mutating the live configuration.
	dir := filepath.Dir(a.opts.ConfigPath)
	file, err := os.CreateTemp(dir, ".singbox-agent-check-*.json")
	if err != nil {
		return fmt.Errorf("create validation file: %w", err)
	}
	name := file.Name()
	defer os.Remove(name)
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(config); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	cmd = exec.Command(a.opts.BinaryPath, "check", "-c", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sing-box check failed: %s", strings.TrimSpace(string(output)))
	}
	return nil
}

func (a *Agent) atomicWrite(config []byte) ([]byte, error) {
	old, err := a.readConfig()
	if err != nil {
		return nil, fmt.Errorf("read current config: %w", err)
	}
	dir := filepath.Dir(a.opts.ConfigPath)
	file, err := os.CreateTemp(dir, ".singbox-agent-*.json")
	if err != nil {
		return nil, err
	}
	name := file.Name()
	defer os.Remove(name)
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return nil, err
	}
	if _, err := file.Write(config); err != nil {
		file.Close()
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(name, a.opts.ConfigPath); err != nil {
		return nil, fmt.Errorf("replace config: %w", err)
	}
	return old, nil
}

func (a *Agent) restartCore() error {
	output, err := exec.Command("systemctl", "restart", a.opts.ServiceName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl restart %s: %s", a.opts.ServiceName, strings.TrimSpace(string(output)))
	}
	return nil
}

func (a *Agent) coreRunning() bool {
	return exec.Command("systemctl", "is-active", "--quiet", a.opts.ServiceName).Run() == nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": message})
}
