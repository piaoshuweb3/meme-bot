import 'package:flutter/foundation.dart';

import 'realtime_client.dart';

/// 实时事件中枢：全局**单一** WebSocket 连接，页面通过 ValueNotifier 订阅。
///
/// 设计取舍：
///   - 单一连接（而非每页一条）：省流量省电，连接状态集中可见；
///   - 用 ValueNotifier 分发：不引入状态管理依赖；
///   - 断线时页面继续用既有轮询刷新（降级而非失效）。
class RealtimeHub {
  RealtimeHub({required String baseUrl}) {
    // Dart 不允许在字段初始化器中引用实例方法，故在构造体内装配
    _client = RealtimeClient(
      baseUrl: baseUrl,
      onEvent: _dispatch,
      onStatus: _handleStatus,
    );
  }

  late final RealtimeClient _client;

  /// 连接状态（页面据此显示"实时 / 轮询（降级）"）。
  final ValueNotifier<RealtimeStatus> status =
      ValueNotifier<RealtimeStatus>(RealtimeStatus.connecting);

  /// 最近一次信号事件。
  final ValueNotifier<Map<String, dynamic>?> lastSignal =
      ValueNotifier<Map<String, dynamic>?>(null);

  /// 最近一次持仓事件。
  final ValueNotifier<Map<String, dynamic>?> lastPosition =
      ValueNotifier<Map<String, dynamic>?>(null);

  /// 最近一次告警事件。
  final ValueNotifier<Map<String, dynamic>?> lastAlert =
      ValueNotifier<Map<String, dynamic>?>(null);

  /// 建立连接（失败自动重连）。
  void start() => _client.connect();

  void _handleStatus(RealtimeStatus value) => status.value = value;

  void _dispatch(RealtimeEvent evt) {
    switch (evt.type) {
      case RealtimeEvent.typeSignal:
        if (evt.data != null) lastSignal.value = evt.data;
        break;
      case RealtimeEvent.typePosition:
        if (evt.data != null) lastPosition.value = evt.data;
        break;
      case RealtimeEvent.typeAlert:
        if (evt.data != null) lastAlert.value = evt.data;
        break;
      default:
        break; // system 等仅用于链路确认
    }
  }

  /// 释放资源。
  Future<void> dispose() async {
    await _client.dispose();
    status.dispose();
    lastSignal.dispose();
    lastPosition.dispose();
    lastAlert.dispose();
  }
}
