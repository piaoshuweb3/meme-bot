package main

import "runtime"

// 构建信息，可由 -ldflags 注入：
//
//	go build -ldflags "-X main.version=1.0.0 -X main.commit=$(git rev-parse --short HEAD) -X main.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
//
// 见 deploy/Dockerfile 与 Makefile。
var (
	version   = "dev"
	commit    = "none"
	buildTime = "unknown"
)

// buildInfo 返回构建信息（用于日志与运维面板展示）。
func buildInfo() map[string]string {
	return map[string]string{
		"version":    version,
		"commit":     commit,
		"build_time": buildTime,
		"go_version": runtime.Version(),
	}
}
