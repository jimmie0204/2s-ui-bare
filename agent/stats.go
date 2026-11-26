package main

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	v2rayapi "github.com/sagernet/sing-box/experimental/v2rayapi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type userTraffic struct {
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

var userStatName = regexp.MustCompile(`^user>>>(.+)>>>traffic>>>(uplink|downlink)$`)

// stats 通过 sing-box experimental.v2ray_api 读取用户计数器。
// 没有配置 V2RayAPI 时返回空数据，不把总流量错误分摊给用户。
func (a *Agent) readUserTraffic(ctx context.Context) (map[string]userTraffic, error) {
	result := map[string]userTraffic{}
	if strings.TrimSpace(a.opts.V2RayAPI) == "" {
		return result, nil
	}
	conn, err := grpc.DialContext(ctx, a.opts.V2RayAPI, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		return nil, fmt.Errorf("connect sing-box v2ray_api: %w", err)
	}
	defer conn.Close()
	request := &v2rayapi.QueryStatsRequest{
		Patterns: []string{"^user>>>.+>>>traffic>>>(uplink|downlink)$"},
		Regexp:   true,
	}
	var response v2rayapi.QueryStatsResponse
	// sing-box 1.14.4 将服务名改为 v2ray.core.app.stats.command.StatsService；
	// 先调用新名称，失败后兼容旧版本的 experimental.v2rayapi 服务名。
	err = conn.Invoke(ctx, "/v2ray.core.app.stats.command.StatsService/QueryStats", request, &response)
	if err != nil {
		stats, legacyErr := v2rayapi.NewStatsServiceClient(conn).QueryStats(ctx, request)
		if legacyErr != nil {
			return nil, fmt.Errorf("query sing-box user stats: %w", err)
		}
		response = *stats
	}
	stats := &response
	for _, item := range stats.Stat {
		match := userStatName.FindStringSubmatch(item.GetName())
		if len(match) != 3 {
			continue
		}
		traffic := result[match[1]]
		if match[2] == "uplink" {
			traffic.Up = item.GetValue()
		} else {
			traffic.Down = item.GetValue()
		}
		result[match[1]] = traffic
	}
	return result, nil
}

func (a *Agent) stats(w http.ResponseWriter, _ *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	traffic, err := a.readUserTraffic(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user":    traffic,
		"source":  "sing-box experimental.v2ray_api",
		"enabled": a.opts.V2RayAPI != "",
	})
}
