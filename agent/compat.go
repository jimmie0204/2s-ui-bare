package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// 这些类型只覆盖 2S-UI master 节点同步所需的最小数据面。
type compatInbound struct {
	ID    uint             `json:"id"`
	Type  string           `json:"type"`
	Tag   string           `json:"tag"`
	Users []map[string]any `json:"users,omitempty"`
	Raw   map[string]any   `json:"-"`
}

type compatClient struct {
	ID       uint            `json:"id"`
	Name     string          `json:"name"`
	Enable   bool            `json:"enable"`
	Config   json.RawMessage `json:"config"`
	Inbounds []uint          `json:"inbounds"`
	Links    []any           `json:"links"`
	Volume   int64           `json:"volume"`
	Expiry   int64           `json:"expiry"`
	Up       int64           `json:"up"`
	Down     int64           `json:"down"`
	Group    string          `json:"group"`
	LimitIP  int             `json:"limitIp"`
}

func (a *Agent) compatStatus(w http.ResponseWriter, _ *http.Request) {
	writeCompat(w, http.StatusOK, map[string]any{
		"cpu": 0,
		"mem": map[string]any{"current": 0, "total": 0},
		"sys": map[string]any{"appVersion": "singbox-agent"},
		"sbd": map[string]any{"running": a.coreRunning(), "version": "unknown"},
	})
}

func (a *Agent) compatOnlines(w http.ResponseWriter, _ *http.Request) {
	writeCompat(w, http.StatusOK, map[string]any{"user": []string{}})
}

func (a *Agent) compatInbounds(w http.ResponseWriter, r *http.Request) {
	config, err := a.readConfig()
	if err != nil {
		writeCompatError(w, http.StatusInternalServerError, err)
		return
	}
	inbounds, err := parseCompatInbounds(config)
	if err != nil {
		writeCompatError(w, http.StatusUnprocessableEntity, err)
		return
	}
	metadata, err := a.loadMetadata()
	if err != nil {
		writeCompatError(w, http.StatusUnprocessableEntity, err)
		return
	}

	ids := parseIDFilter(r.URL.Query().Get("id"))
	result := make([]map[string]any, 0, len(inbounds))
	for _, inbound := range inbounds {
		if len(ids) > 0 && !ids[inbound.ID] {
			continue
		}
		// 2S-UI 的节点导入页至少需要这些字段；Raw 中的其它 sing-box
		// 参数也一并保留，后续适配器可以据此生成订阅链接。
		row := cloneMap(inbound.Raw)
		row["id"] = inbound.ID
		row["type"] = inbound.Type
		row["tag"] = inbound.Tag
		row["users"] = inbound.Users
		if meta, ok := metadata.Inbounds[inbound.Tag]; ok {
			if address, ok := metadataAddress(meta); ok {
				row["addrs"] = []map[string]any{address}
			}
			if outJSON := metadataOutJSON(meta); len(outJSON) > 0 {
				row["out_json"] = outJSON
			}
			// 元数据中的客户端参数可以覆盖服务端配置中相同的字段，
			// 例如公网侧的 TLS/SNI、Hy2 速率和混淆参数。
			for _, key := range []string{"transport", "tls", "up_mbps", "down_mbps", "tcp_fast_open", "congestion_control"} {
				if value, ok := meta[key]; ok {
					row[key] = value
				}
			}
			// 兼容用户提供的 Clash 风格 Hy2 元数据：
			// obfs: salamander + obfs-password: xxx。
			if obfsType, ok := meta["obfs"].(string); ok && obfsType != "" {
				obfs := map[string]any{"type": obfsType}
				if password, ok := meta["obfs-password"].(string); ok {
					obfs["password"] = password
				}
				row["obfs"] = obfs
			} else if value, ok := meta["obfs"]; ok {
				row["obfs"] = value
			}
		}
		result = append(result, row)
	}
	writeCompat(w, http.StatusOK, map[string]any{"inbounds": result})
}

func (a *Agent) compatClients(w http.ResponseWriter, _ *http.Request) {
	config, err := a.readConfig()
	if err != nil {
		writeCompatError(w, http.StatusInternalServerError, err)
		return
	}
	inbounds, err := parseCompatInbounds(config)
	if err != nil {
		writeCompatError(w, http.StatusUnprocessableEntity, err)
		return
	}
	traffic, trafficErr := a.readUserTrafficWithTimeout()
	if trafficErr != nil {
		writeCompatError(w, http.StatusBadGateway, trafficErr)
		return
	}
	clients := collectCompatClients(inbounds, traffic)
	writeCompat(w, http.StatusOK, map[string]any{"clients": clients})
}

func (a *Agent) compatSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeCompatError(w, http.StatusBadRequest, err)
		return
	}
	if r.FormValue("object") != "clients" {
		writeCompatError(w, http.StatusNotImplemented, errors.New("bare node currently supports clients only"))
		return
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(r.FormValue("data")), &payload); err != nil {
		writeCompatError(w, http.StatusBadRequest, fmt.Errorf("invalid client payload: %w", err))
		return
	}
	config, err := a.readConfig()
	if err != nil {
		writeCompatError(w, http.StatusInternalServerError, err)
		return
	}
	updated, err := applyCompatClient(config, r.FormValue("action"), payload)
	if err != nil {
		writeCompatError(w, http.StatusUnprocessableEntity, err)
		return
	}
	updated = a.syncStatsUsers(updated)
	if err := a.validateConfig(updated); err != nil {
		writeCompatError(w, http.StatusUnprocessableEntity, err)
		return
	}
	a.mu.Lock()
	_, writeErr := a.atomicWrite(updated)
	if writeErr == nil {
		writeErr = a.restartCore()
	}
	a.mu.Unlock()
	if writeErr != nil {
		writeCompatError(w, http.StatusBadGateway, writeErr)
		return
	}
	writeCompat(w, http.StatusOK, map[string]any{"saved": true})
}

func parseCompatInbounds(raw []byte) ([]compatInbound, error) {
	var root struct {
		Inbounds []map[string]any `json:"inbounds"`
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	result := make([]compatInbound, 0, len(root.Inbounds))
	for index, item := range root.Inbounds {
		typ, _ := item["type"].(string)
		tag, _ := item["tag"].(string)
		if typ == "" || tag == "" {
			continue
		}
		users, _ := item["users"].([]any)
		mapped := make([]map[string]any, 0, len(users))
		for _, value := range users {
			if user, ok := value.(map[string]any); ok {
				mapped = append(mapped, user)
			}
		}
		result = append(result, compatInbound{ID: uint(index + 1), Type: typ, Tag: tag, Users: mapped, Raw: item})
	}
	return result, nil
}

func (a *Agent) readUserTrafficWithTimeout() (map[string]userTraffic, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return a.readUserTraffic(ctx)
}

func collectCompatClients(inbounds []compatInbound, traffic map[string]userTraffic) []compatClient {
	byName := map[string]*compatClient{}
	for _, inbound := range inbounds {
		for _, user := range inbound.Users {
			name, _ := user["name"].(string)
			if name == "" {
				continue
			}
			client := byName[name]
			if client == nil {
				client = &compatClient{Name: name, Enable: true, Group: "@cluster", Links: []any{}, Config: json.RawMessage(`{}`)}
				byName[name] = client
			}
			client.Inbounds = append(client.Inbounds, inbound.ID)
			if currentTraffic, ok := traffic[name]; ok {
				client.Up = currentTraffic.Up
				client.Down = currentTraffic.Down
			}
			block := map[string]any{}
			for key, value := range user {
				block[key] = value
			}
			block["name"] = name
			var current map[string]any
			_ = json.Unmarshal(client.Config, &current)
			current[inbound.Type] = block
			encoded, _ := json.Marshal(current)
			client.Config = encoded
		}
	}
	result := make([]compatClient, 0, len(byName))
	for _, client := range byName {
		result = append(result, *client)
	}
	return result
}

func applyCompatClient(raw []byte, action string, payload map[string]any) ([]byte, error) {
	name, _ := payload["name"].(string)
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("client name is required")
	}
	config, err := decodeRoot(raw)
	if err != nil {
		return nil, err
	}
	inbounds, err := parseCompatInbounds(raw)
	if err != nil {
		return nil, err
	}
	ids := parseAnyIDs(payload["inbounds"])
	if action == "del" {
		ids = nil
	}

	// 先从所有目标入站移除同名用户，避免 edit 造成重复；对于 del，移除后直接保存。
	for _, inbound := range inbounds {
		users, _ := config["inbounds"].([]any)
		if int(inbound.ID) > len(users) {
			continue
		}
		row, _ := users[inbound.ID-1].(map[string]any)
		current, _ := row["users"].([]any)
		kept := make([]any, 0, len(current))
		for _, item := range current {
			user, _ := item.(map[string]any)
			if userName, _ := user["name"].(string); userName != name {
				kept = append(kept, item)
			}
		}
		row["users"] = kept
	}
	if action != "del" {
		configBlocks := decodeClientConfig(payload["config"])
		users, _ := config["inbounds"].([]any)
		for _, inbound := range inbounds {
			if !ids[inbound.ID] {
				continue
			}
			row, _ := users[inbound.ID-1].(map[string]any)
			block := cloneMap(configBlocks[inbound.Type])
			if block == nil {
				block = map[string]any{}
			}
			block["name"] = name
			rowUsers, _ := row["users"].([]any)
			row["users"] = append(rowUsers, block)
		}
	}
	return json.Marshal(config)
}

func decodeRoot(raw []byte) (map[string]any, error) {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	if _, ok := root["inbounds"].([]any); !ok {
		return nil, errors.New("config.inbounds must be an array")
	}
	return root, nil
}

func decodeClientConfig(value any) map[string]map[string]any {
	result := map[string]map[string]any{}
	encoded, _ := json.Marshal(value)
	var blocks map[string]map[string]any
	if json.Unmarshal(encoded, &blocks) == nil {
		return blocks
	}
	return result
}

func parseIDFilter(value string) map[uint]bool {
	result := map[uint]bool{}
	for _, part := range strings.Split(value, ",") {
		id, err := strconv.ParseUint(strings.TrimSpace(part), 10, 32)
		if err == nil && id > 0 {
			result[uint(id)] = true
		}
	}
	return result
}

func parseAnyIDs(value any) map[uint]bool {
	result := map[uint]bool{}
	items, _ := value.([]any)
	for _, item := range items {
		switch id := item.(type) {
		case float64:
			if id > 0 {
				result[uint(id)] = true
			}
		case string:
			if n, err := strconv.ParseUint(id, 10, 32); err == nil {
				result[uint(n)] = true
			}
		}
	}
	return result
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func writeCompat(w http.ResponseWriter, status int, value any) {
	writeJSON(w, status, map[string]any{"success": true, "obj": value})
}

func writeCompatError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"success": false, "msg": err.Error()})
}
