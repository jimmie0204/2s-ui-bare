package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testAgent(t *testing.T) (*Agent, string) {
	t.Helper()
	dir := t.TempDir()
	config := filepath.Join(dir, "config.json")
	if err := os.WriteFile(config, []byte(`{"inbounds":[{"type":"vless","tag":"vless-in","users":[{"name":"alice","uuid":"u"}]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte(strings.Repeat("a", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := NewAgent(AgentOptions{ConfigPath: config, BinaryPath: "/usr/bin/true", ServiceName: "not-running", TokenFile: tokenFile})
	if err != nil {
		t.Fatal(err)
	}
	return a, strings.Repeat("a", 64)
}

func TestAuthAndInbounds(t *testing.T) {
	a, token := testAgent(t)
	s := httptest.NewServer(a.Handler())
	defer s.Close()

	resp, err := http.Get(s.URL + "/v1/inbounds")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	resp.Body.Close()

	req, _ := http.NewRequest(http.MethodGet, s.URL+"/v1/inbounds", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var payload struct {
		Inbounds []inboundSummary `json:"inbounds"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Inbounds) != 1 || payload.Inbounds[0].Tag != "vless-in" {
		t.Fatalf("payload=%+v", payload)
	}
}

func TestValidateUsesTemporaryFile(t *testing.T) {
	a, token := testAgent(t)
	s := httptest.NewServer(a.Handler())
	defer s.Close()
	body := `{"config":{"log":{"level":"info"}},"restart":false}`
	req, _ := http.NewRequest(http.MethodPost, s.URL+"/v1/config/validate", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// /bin/true is used as a deterministic validation stub in this unit test.
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, data)
	}
}
