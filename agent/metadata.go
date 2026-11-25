package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// 订阅公网元数据只保存客户端连接地址和代理层覆盖项，不保存用户密码。
type subscriptionMetadata struct {
	Inbounds map[string]map[string]any `yaml:"inbounds" json:"inbounds"`
}

func (a *Agent) loadMetadata() (subscriptionMetadata, error) {
	if strings.TrimSpace(a.opts.MetadataFile) == "" {
		return subscriptionMetadata{}, nil
	}
	raw, err := os.ReadFile(a.opts.MetadataFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return subscriptionMetadata{}, nil
		}
		return subscriptionMetadata{}, fmt.Errorf("read metadata: %w", err)
	}
	var metadata subscriptionMetadata
	if err := yaml.Unmarshal(raw, &metadata); err != nil {
		return subscriptionMetadata{}, fmt.Errorf("parse metadata: %w", err)
	}
	return metadata, nil
}

func metadataAddress(meta map[string]any) (map[string]any, bool) {
	server, _ := meta["server"].(string)
	if strings.TrimSpace(server) == "" {
		return nil, false
	}
	port := number(meta["port"])
	if port == 0 {
		port = number(meta["server_port"])
	}
	if port == 0 {
		return nil, false
	}
	address := map[string]any{"server": server, "server_port": port}
	if remark, ok := meta["remark"].(string); ok && remark != "" {
		address["remark"] = remark
	}
	if tls, ok := meta["tls"].(map[string]any); ok {
		address["tls"] = tls
	} else if sni, ok := meta["sni"].(string); ok && sni != "" {
		address["tls"] = map[string]any{"enabled": true, "server_name": sni}
	}
	return address, true
}

func number(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case uint:
		return int(v)
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(v)
		return n
	default:
		return 0
	}
}

func metadataOutJSON(meta map[string]any) map[string]any {
	if value, ok := meta["out_json"].(map[string]any); ok {
		return cloneMap(value)
	}
	result := map[string]any{}
	for _, key := range []string{"udp_relay_mode", "server_ports", "hop_interval", "hop_start", "hop_end"} {
		if value, ok := meta[key]; ok {
			result[key] = value
		}
	}
	return result
}
