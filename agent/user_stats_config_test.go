package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSyncStatsUsers(t *testing.T) {
	a := &Agent{opts: AgentOptions{V2RayAPI: "127.0.0.1:10085"}}
	input := []byte(`{"experimental":{"v2ray_api":{"listen":"127.0.0.1:10085","stats":{"enabled":true,"users":["old"]}}},"inbounds":[{"type":"vless","users":[{"name":"bob","uuid":"u"},{"name":"alice","uuid":"v"}]}]}`)
	var root map[string]any
	if err := json.Unmarshal(a.syncStatsUsers(input), &root); err != nil {
		t.Fatal(err)
	}
	stats := root["experimental"].(map[string]any)["v2ray_api"].(map[string]any)["stats"].(map[string]any)
	if got := stats["users"]; !reflect.DeepEqual(got, []any{"alice", "bob"}) {
		t.Fatalf("users=%v", got)
	}
}

func TestSyncStatsUsersDoesNotCreateAPI(t *testing.T) {
	a := &Agent{opts: AgentOptions{V2RayAPI: "127.0.0.1:10085"}}
	input := []byte(`{"inbounds":[{"type":"vless","users":[{"name":"bob"}]}]}`)
	if got := string(a.syncStatsUsers(input)); got != string(input) {
		t.Fatalf("config changed: %s", got)
	}
}
