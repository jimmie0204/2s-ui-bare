package main

import (
	"encoding/json"
	"sort"
)

// syncStatsUsers 把当前入站用户同步到 sing-box v2ray_api.stats.users。
// 只有配置本身已经启用 v2ray_api 且 Agent 配置了 V2RayAPI 地址时才修改，
// 避免 Agent 擅自改变未启用统计的现有配置。
func (a *Agent) syncStatsUsers(raw []byte) []byte {
	if a.opts.V2RayAPI == "" {
		return raw
	}
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		return raw
	}
	experimental, ok := root["experimental"].(map[string]any)
	if !ok {
		return raw
	}
	v2ray, ok := experimental["v2ray_api"].(map[string]any)
	if !ok {
		return raw
	}
	stats, ok := v2ray["stats"].(map[string]any)
	if !ok {
		return raw
	}
	if enabled, ok := stats["enabled"].(bool); !ok || !enabled {
		return raw
	}

	users := map[string]bool{}
	if inbounds, ok := root["inbounds"].([]any); ok {
		for _, item := range inbounds {
			inbound, _ := item.(map[string]any)
			list, _ := inbound["users"].([]any)
			for _, item := range list {
				user, _ := item.(map[string]any)
				name, _ := user["name"].(string)
				if name != "" {
					users[name] = true
				}
			}
		}
	}
	result := make([]string, 0, len(users))
	for name := range users {
		result = append(result, name)
	}
	sort.Strings(result)
	stats["users"] = result
	encoded, err := json.Marshal(root)
	if err != nil {
		return raw
	}
	return encoded
}
