import 'dart:convert';

import 'package:http/http.dart' as http;

/// 后端 API 客户端。
///
/// 安全说明：仅保存 JWT 到内存（Stage 6 接入 flutter_secure_storage），
/// 不落盘任何密钥；真实交易需在 App 内二次确认（生物识别）后才允许。
class ApiClient {
  ApiClient({String? baseUrl})
      : baseUrl = baseUrl ??
            const String.fromEnvironment('API_BASE', defaultValue: 'http://10.0.2.2:8080');

  String baseUrl;
  String? _token;

  bool get hasToken => _token != null;
  set token(String? value) => _token = value;

  Map<String, String> get _headers => {
        'Content-Type': 'application/json',
        if (_token != null) 'Authorization': 'Bearer $_token',
      };

  Future<Map<String, dynamic>> _get(String path) async {
    final res = await http.get(Uri.parse('$baseUrl$path'), headers: _headers);
    return _decode(res);
  }

  Future<Map<String, dynamic>> _post(String path, Map<String, dynamic> body) async {
    final res = await http.post(Uri.parse('$baseUrl$path'),
        headers: _headers, body: jsonEncode(body));
    return _decode(res);
  }

  Map<String, dynamic> _decode(http.Response res) {
    final text = utf8.decode(res.bodyBytes);
    final body = text.isEmpty ? <String, dynamic>{} : jsonDecode(text) as Map<String, dynamic>;
    if (res.statusCode >= 400) {
      throw Exception(body['error'] ?? 'HTTP ${res.statusCode}');
    }
    return body;
  }

  Future<Map<String, dynamic>> health() => _get('/healthz');

  Future<Map<String, dynamic>> login(String email, String password) async {
    final body = await _post('/api/v1/auth/login', {'email': email, 'password': password});
    _token = body['token'] as String?;
    return body;
  }

  Future<Map<String, dynamic>> signals({int limit = 50}) =>
      _get('/api/v1/signals?limit=$limit');

  Future<Map<String, dynamic>> positions({String chain = ''}) =>
      _get('/api/v1/positions${chain.isEmpty ? '' : '?chain=$chain'}');

  Future<Map<String, dynamic>> subscription() => _get('/api/v1/subscription');

  Future<Map<String, dynamic>> pause(String reason, {int minutes = 60}) =>
      _post('/api/v1/system/pause', {'reason': reason, 'duration_minutes': minutes});

  Future<Map<String, dynamic>> resume() => _post('/api/v1/system/resume', {});
}
