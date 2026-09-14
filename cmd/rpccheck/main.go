// Command rpccheck 检查 RPC 端点连通性（Go net/http 视角，逐端点不故障切换）。
//
// 用法：
//
//	bin/rpccheck -url https://rpc.example/v2/KEY        # 直测任意端点（日志自动打码）
//	bin/rpccheck -config configs/config.yaml            # 检查配置里的全部端点
//	bin/rpccheck -config configs/config.yaml -chain base
//
// 输出含 eth_chainId（确证端点实际对应的网络）与延迟。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"time"

	"meme-bot/internal/config"
)

func main() {
	configPath := flag.String("config", "configs/config.yaml", "配置文件路径（与 -url 二选一）")
	url := flag.String("url", "", "直接测试单个 RPC 端点")
	only := flag.String("chain", "", "只检查指定链")
	timeout := flag.Duration("timeout", 20*time.Second, "单请求超时")
	flag.Parse()

	client := &http.Client{Timeout: *timeout}
	failed, checked := 0, 0

	check := func(name, endpoint string) {
		checked++
		chainID, err := rpcCall(context.Background(), client, endpoint, "eth_chainId", nil)
		if err != nil {
			failed++
			fmt.Println("  FAIL", config.MaskURL(endpoint), "->", err)
			return
		}
		start := time.Now()
		head, err := rpcCall(context.Background(), client, endpoint, "eth_blockNumber", nil)
		elapsed := time.Since(start).Round(time.Millisecond)
		if err != nil {
			failed++
			fmt.Println("  FAIL", config.MaskURL(endpoint), "->", err)
			return
		}
		fmt.Println("  OK  ", config.MaskURL(endpoint), "chainId=", chainID, "head=", head, "latency=", elapsed)
	}

	if *url != "" {
		fmt.Println("直接测试：")
		check("url", *url)
		fmt.Println("共检查", checked, "个端点，失败", failed, "个")
		if failed > 0 {
			os.Exit(2)
		}
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "加载配置失败:", err)
		os.Exit(1)
	}

	names := make([]string, 0, len(cfg.Chains))
	for name := range cfg.Chains {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if *only != "" && name != *only {
			continue
		}
		ch := cfg.Chains[name]
		if len(ch.RPCURLs) == 0 {
			continue
		}
		fmt.Println(name, "(evm_chain_id=", ch.EVMChainID, ")")
		for _, endpoint := range ch.RPCURLs {
			check(name, endpoint)
		}
	}
	fmt.Println("共检查", checked, "个端点，失败", failed, "个")
	if failed > 0 {
		os.Exit(2)
	}
}

func rpcCall(ctx context.Context, client *http.Client, endpoint, method string, params []any) (string, error) {
	if params == nil {
		params = []any{}
	}
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var out struct {
		Result string `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Error != nil {
		return "", fmt.Errorf("RPC: %s", out.Error.Message)
	}
	return out.Result, nil
}
