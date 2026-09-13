import 'package:flutter_secure_storage/flutter_secure_storage.dart';

/// 会话与偏好的安全存储。
///
/// 安全基线（与后端一致）：
///   - 令牌**只存平台安全区**：Android Keystore / iOS Keychain；
///   - 绝不写日志、绝不落明文文件；
///   - 平台不支持时（例如桌面调试）降级为**内存存储**：进程结束即丢失，
///     宁可要求重新登录，也不把令牌写到磁盘。
class SecureStore {
  SecureStore({FlutterSecureStorage? storage})
      : _storage = storage ??
            const FlutterSecureStorage(
              aOptions: AndroidOptions(encryptedSharedPreferences: true),
              iOptions: IOSOptions(accessibility: KeychainAccessibility.first_unlock),
            );

  final FlutterSecureStorage _storage;
  final Map<String, String> _memory = {};
  bool _fallback = false;

  static const tokenKey = 'memebot.token';
  static const baseUrlKey = 'memebot.baseUrl';
  static const localeKey = 'memebot.locale';

  /// 是否处于内存降级模式（UI 可提示"本次令牌不会持久化"）。
  bool get isFallback => _fallback;

  Future<String?> readToken() => _read(tokenKey);
  Future<void> writeToken(String value) => _write(tokenKey, value);
  Future<void> clearToken() => _delete(tokenKey);

  Future<String?> readBaseUrl() => _read(baseUrlKey);
  Future<void> writeBaseUrl(String value) => _write(baseUrlKey, value);

  Future<String?> readLocale() => _read(localeKey);
  Future<void> writeLocale(String value) => _write(localeKey, value);

  Future<void> _write(String key, String value) async {
    if (_fallback) {
      _memory[key] = value;
      return;
    }
    try {
      await _storage.write(key: key, value: value);
    } catch (_) {
      _fallback = true;
      _memory[key] = value;
    }
  }

  Future<String?> _read(String key) async {
    if (_fallback) {
      return _memory[key];
    }
    try {
      return await _storage.read(key: key);
    } catch (_) {
      _fallback = true;
      return _memory[key];
    }
  }

  Future<void> _delete(String key) async {
    if (_fallback) {
      _memory.remove(key);
      return;
    }
    try {
      await _storage.delete(key: key);
    } catch (_) {
      _fallback = true;
      _memory.remove(key);
    }
  }
}
