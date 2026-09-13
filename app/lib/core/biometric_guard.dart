import 'package:flutter/material.dart';
import 'package:local_auth/local_auth.dart';

/// 高风险操作的二次确认（生物识别优先，降级为显式对话框）。
///
/// 安全约定：
///   - 平仓、熔断、修改风控参数等**不可逆/高影响**动作必须经过本守卫；
///   - 设备支持生物识别时走系统弹窗（BiometricPrompt / Face ID）；
///   - 设备不支持或认证失败时**不静默放行**，而是要求用户在应用内显式确认，
///     并把将要执行的动作与影响写清楚（人为确认环节始终保留）。
class BiometricGuard {
  BiometricGuard({LocalAuthentication? auth}) : _auth = auth ?? LocalAuthentication();

  final LocalAuthentication _auth;

  /// 设备是否具备生物识别或设备凭据能力。
  Future<bool> isAvailable() async {
    try {
      final supported = await _auth.isDeviceSupported();
      final canCheck = await _auth.canCheckBiometrics;
      return supported && canCheck;
    } catch (_) {
      return false;
    }
  }

  /// 请求确认；返回 true 表示用户已授权执行。
  ///
  /// [reason] 会展示在系统弹窗中（请写明动作，例如"确认熔断交易"）。
  Future<bool> confirm(
    BuildContext context, {
    required String reason,
    required String actionLabel,
  }) async {
    if (await isAvailable()) {
      try {
        final ok = await _auth.authenticate(
          localizedReason: reason,
          options: const AuthenticationOptions(biometricOnly: false, stickyAuth: true),
        );
        if (ok) return true;
        // 认证被取消/失败 → 继续走显式确认（不直接放行）
      } catch (_) {
        // 平台异常 → 继续走显式确认
      }
    }

    if (!context.mounted) return false;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(actionLabel),
        content: Text(reason),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx, false), child: const Text('取消')),
          FilledButton(onPressed: () => Navigator.pop(ctx, true), child: const Text('确认执行')),
        ],
      ),
    );
    return confirmed ?? false;
  }
}
