package aggregator

import "context"

// Client 是聚合器的统一契约：执行层只依赖本接口，
// 1inch / OpenOcean / 其它聚合器均可实现；Jupiter 因交易模型不同（返回未签名交易）
// 使用独立的 QuoteRaw + BuildTx 方法，不实现本接口。
type Client interface {
	// Name 返回聚合器名称（用于审计与订单记录）。
	Name() string
	// Quote 获取报价（用于滑点与成交质量校验）。
	Quote(ctx context.Context, req Request) (*Route, error)
	// Build 构建可签名的 swap 交易。
	Build(ctx context.Context, req Request) (*Route, error)
}

var _ Client = (*OneInch)(nil)
