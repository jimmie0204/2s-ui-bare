// singbox-agent 为裸 sing-box 提供受限的远程管理 API。
// 它不替换 sing-box，也不保存中心用户数据库；中心面板通过它安全地读取和应用配置。
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
)

func main() {
	listen := flag.String("listen", envOr("SINGBOX_AGENT_LISTEN", "127.0.0.1:2088"), "监听地址")
	configPath := flag.String("config", envOr("SINGBOX_CONFIG", "/etc/sing-box/config.json"), "sing-box 配置文件")
	binary := flag.String("binary", envOr("SINGBOX_BINARY", "/usr/local/bin/sing-box"), "sing-box 可执行文件")
	service := flag.String("service", envOr("SINGBOX_SERVICE", "sing-box"), "systemd 服务名")
	tokenFile := flag.String("token-file", envOr("SINGBOX_AGENT_TOKEN_FILE", "/etc/singbox-agent/token"), "Bearer token 文件")
	v2rayAPI := flag.String("v2ray-api", envOr("SINGBOX_V2RAY_API", ""), "sing-box v2ray_api gRPC 地址；为空则关闭用户流量统计")
	metadataFile := flag.String("metadata", envOr("SINGBOX_AGENT_METADATA", "/etc/singbox-agent/metadata.yaml"), "订阅公网元数据文件")
	flag.Parse()

	a, err := NewAgent(AgentOptions{
		ConfigPath:   *configPath,
		BinaryPath:   *binary,
		ServiceName:  *service,
		TokenFile:    *tokenFile,
		V2RayAPI:     *v2rayAPI,
		MetadataFile: *metadataFile,
	})
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("singbox-agent listening on %s, config=%s", *listen, *configPath)
	server := &http.Server{
		Addr:              *listen,
		Handler:           a.Handler(),
		ReadHeaderTimeout: 5 * 1e9,
		ReadTimeout:       30 * 1e9,
		WriteTimeout:      30 * 1e9,
		IdleTimeout:       60 * 1e9,
	}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
