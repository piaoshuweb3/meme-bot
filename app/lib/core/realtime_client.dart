import 'dart:async';
import 'dart:convert';
import 'dart:io';

/// 实时事件（与服务端 /ws 协议一致）。
class RealtimeEvent {
  const RealtimeEvent({required this.type, this.data});

  final String type;
  final Map<String, dynamic>? data;

  /// 解析服务端事件；非法载荷返回 null（调用方直接忽略，不打断连接）。
  static RealtimeEvent? tryParse(Object? raw) {
    if (raw is! String || raw.isEmpty) return null;
    try {
      final decoded = jsonDecode(raw);
      if (decoded is! Map) return null;
      final type = decoded['type']?.toString() ?? '';
      if (type.isEmpty) return null;
      final data = decoded['data'];
      return RealtimeEvent(
        type: type,
        data: data is Map ? data.cast<String, dynamic>() : null,
      );
    } catch (_) {
      return null;
    }
  }

  /// 常用事件类型。
  static const typeSignal = 'signal';
  static const typePosition = 'position';
  static const typeAlert = 'alert';
  static const typeSystem = 'system';
}

/// 连接状态。
enum RealtimeStatus { connecting, open, closed }

/// 轻量 WebSocket 客户端：指数退避重连 + 状态回调。
///
/// 为什么不用第三方包：dart:io 的 [WebSocket] 已满足需求（Android/iOS 原生可用），
/// 少一层依赖、行为完全可控。
class RealtimeClient {
  RealtimeClient({
    required this.baseUrl,
    required this.onEvent,
    this.onStatus,
    this.path = '/ws',
    this.maxBackoff = const Duration(seconds: 15),
  });

  final String baseUrl;
  final void Function(RealtimeEvent evt) onEvent;
  final void Function(RealtimeStatus status)? onStatus;
  final String path;
  final Duration maxBackoff;

  WebSocket? _socket;
  Timer? _retry;
  bool _disposed = false;
  int _attempt = 0;

  /// 由 REST 基址推导 WS 地址（http→ws / https→wss）。
  static Uri buildUri(String baseUrl, String path) {
    final uri = Uri.parse(baseUrl.trim());
    final scheme = uri.scheme == 'https' ? 'wss' : 'ws';
    return uri.replace(scheme: scheme, path: path);
  }

  /// 建立连接（失败自动重连）。
  void connect() {
    if (_disposed) return;
    onStatus?.call(RealtimeStatus.connecting);

    final uri = buildUri(baseUrl, path);
    WebSocket.connect(uri.toString()).then((socket) {
      if (_disposed) {
        socket.close();
        return;
      }
      _socket = socket;
      _attempt = 0;
      onStatus?.call(RealtimeStatus.open);
      socket.listen(
        (message) {
          final evt = RealtimeEvent.tryParse(message);
          if (evt != null) onEvent(evt);
        },
        onDone: _scheduleReconnect,
        onError: (_) {
          socket.close();
          _scheduleReconnect();
        },
        cancelOnError: true,
      );
    }).catchError((_) {
      _scheduleReconnect();
    });
  }

  void _scheduleReconnect() {
    if (_disposed) return;
    onStatus?.call(RealtimeStatus.closed);
    _attempt += 1;
    final seconds = 1 << (_attempt - 1).clamp(0, 4); // 1,2,4,8,16s
    final delay = seconds > maxBackoff.inSeconds ? maxBackoff : Duration(seconds: seconds);
    _retry?.cancel();
    _retry = Timer(delay, connect);
  }

  /// 关闭连接并停止重连。
  Future<void> dispose() async {
    _disposed = true;
    _retry?.cancel();
    try {
      await _socket?.close();
    } catch (_) {
      // 忽略关闭异常
    }
    _socket = null;
  }
}
