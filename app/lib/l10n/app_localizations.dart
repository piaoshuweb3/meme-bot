import 'package:flutter/material.dart';

/// 应用本地化实现（跟随标准 BCP 47 locale：zh-CN / en-US）。
///
/// 设计说明：
///   - 文案集中在 [_zh] / [_en] 字典中，UI 层通过强类型 getter 访问，杜绝硬编码；
///   - 与 `lib/l10n/app_en.arb` / `app_zh.arb` 保持 key 一致（ARB 为官方翻译源）；
///   - 手写 delegate 而非依赖 `flutter gen-l10n` 生成，保证任何环境下都能直接编译运行。
class AppLocalizations {
  AppLocalizations(this.locale);

  final Locale locale;

  /// 支持的 locale（顺序即回退顺序）。
  static const List<Locale> supportedLocales = [
    Locale('zh', 'CN'),
    Locale('en', 'US'),
  ];

  static const LocalizationsDelegate<AppLocalizations> delegate =
      _AppLocalizationsDelegate();

  static AppLocalizations of(BuildContext context) {
    return Localizations.of<AppLocalizations>(context, AppLocalizations)!;
  }

  String _t(String key, [Map<String, String>? args]) {
    final dict = _dicts[locale.languageCode] ?? _zh;
    var value = dict[key] ?? _zh[key] ?? key;
    if (args != null) {
      args.forEach((name, replacement) {
        value = value.replaceAll('{$name}', replacement);
      });
    }
    return value;
  }

  /// 供 `Intl` 使用的 locale 标识（`zh_CN` / `en_US` 形式）。
  ///
  /// 说明：Intl 要求「语言_国家」下划线形式；Dart 的 `Locale` 无国家码时退化为语言码。
  String get localeName => locale.countryCode == null || locale.countryCode!.isEmpty
      ? locale.languageCode
      : '${locale.languageCode}_${locale.countryCode}';

  // ---- 通用 ----
  String get appTitle => _t('appTitle');
  String get commonRefresh => _t('commonRefresh');
  String get commonRetry => _t('commonRetry');
  String get commonLoading => _t('commonLoading');
  String get commonLanguage => _t('commonLanguage');
  String get commonChain => _t('commonChain');
  String get commonToken => _t('commonToken');
  String get commonAmount => _t('commonAmount');
  String get commonLiquidity => _t('commonLiquidity');
  String get commonStatus => _t('commonStatus');
  String get commonEntryPrice => _t('commonEntryPrice');
  String get commonCurrentPrice => _t('commonCurrentPrice');
  String get commonPnl => _t('commonPnl');

  // ---- 导航 ----
  String get navOverview => _t('navOverview');
  String get navSignals => _t('navSignals');
  String get navPositions => _t('navPositions');

  // ---- 设置 ----
  String get settingsBaseUrlTitle => _t('settingsBaseUrlTitle');
  String get settingsBaseUrlHint => _t('settingsBaseUrlHint');
  String get settingsBaseUrlHelp => _t('settingsBaseUrlHelp');
  String get settingsCancel => _t('settingsCancel');
  String get settingsSave => _t('settingsSave');

  // ---- 系统状态 ----
  String get healthMode => _t('healthMode');
  String get healthChains => _t('healthChains');
  String get healthDatabase => _t('healthDatabase');
  String get healthSigner => _t('healthSigner');
  String get healthConnected => _t('healthConnected');
  String get healthNotConnected => _t('healthNotConnected');
  String get healthLoaded => _t('healthLoaded');
  String get healthNotLoaded => _t('healthNotLoaded');
  String get healthDatabaseOkHint => _t('healthDatabaseOkHint');
  String get healthDatabaseDownHint => _t('healthDatabaseDownHint');
  String get healthDryRunHint => _t('healthDryRunHint');
  String get healthLiveHint => _t('healthLiveHint');
  String healthUnavailable(String base, String error) =>
      _t('healthUnavailable', {'base': base, 'error': error});

  // ---- 实时通道 ----
  String get realtimeLive => _t('realtimeLive');
  String get realtimePolling => _t('realtimePolling');

  // ---- 风控 ----
  String get riskTitle => _t('riskTitle');
  String get riskDescription => _t('riskDescription');
  String get riskPause => _t('riskPause');
  String get riskResume => _t('riskResume');
  String get riskPauseReason => _t('riskPauseReason');

  // ---- 信号 ----
  String get signalsTitle => _t('signalsTitle');
  String get signalsEmpty => _t('signalsEmpty');
  String get signalsSource => _t('signalsSource');
  String get signalsDecay => _t('signalsDecay');
  String signalsLoadFailed(String error) =>
      _t('signalsLoadFailed', {'error': error});

  String signalStatus(String status) {
    switch (status) {
      case 'pending':
        return _t('signalsStatusPending');
      case 'confirmed':
        return _t('signalsStatusConfirmed');
      case 'executed':
        return _t('signalsStatusExecuted');
      case 'expired':
        return _t('signalsStatusExpired');
      case 'rejected':
        return _t('signalsStatusRejected');
      default:
        return status;
    }
  }

  // ---- 持仓 ----
  String get positionsTitle => _t('positionsTitle');
  String get positionsEmpty => _t('positionsEmpty');

  // ---- 登录 ----
  String get loginEmail => _t('loginEmail');
  String get loginPassword => _t('loginPassword');
  String get loginSignIn => _t('loginSignIn');

  // ---------------------------------------------------------------------------
  // 字典（与 ARB 文件一一对应）
  // ---------------------------------------------------------------------------

  static const Map<String, Map<String, String>> _dicts = {
    'zh': _zh,
    'en': _en,
  };

  static const Map<String, String> _zh = {
    'appTitle': 'meme-bot',
    'navOverview': '总览',
    'navSignals': '信号',
    'navPositions': '持仓',
    'commonRefresh': '刷新',
    'commonRetry': '重试',
    'commonLoading': '加载中…',
    'commonLanguage': '语言',
    'commonChain': '链',
    'commonToken': '代币',
    'commonAmount': '金额',
    'commonLiquidity': '流动性',
    'commonStatus': '状态',
    'commonEntryPrice': '入场价',
    'commonCurrentPrice': '现价',
    'commonPnl': '浮动盈亏',
    'settingsBaseUrlTitle': '后端地址',
    'settingsBaseUrlHint': 'http://10.0.2.2:8080',
    'settingsBaseUrlHelp': 'Android 模拟器访问宿主机用 10.0.2.2；真机用局域网 IP',
    'settingsCancel': '取消',
    'settingsSave': '保存',
    'healthMode': '运行模式',
    'healthChains': '已就绪链',
    'healthDatabase': '数据库',
    'healthSigner': '签名器',
    'healthConnected': '已连接',
    'healthNotConnected': '未连接',
    'healthLoaded': '已加载',
    'healthNotLoaded': '未加载',
    'healthDatabaseOkHint': 'SaaS 功能可用',
    'healthDatabaseDownHint': '降级模式',
    'healthDryRunHint': 'dry_run：不发送真实交易',
    'healthLiveHint': 'live：请谨慎操作',
    'healthUnavailable': '无法连接后端（{base}）：{error}',
    'riskTitle': '风险控制',
    'riskDescription': '熔断后所有交易（含 Agent 发起）都会被拒绝，直到手动恢复。',
    'riskPause': '一键熔断',
    'riskResume': '恢复交易',
    'riskPauseReason': 'App 人工熔断',
    'signalsTitle': '信号流',
    'signalsEmpty': '暂无活跃信号。\n信号需同时满足：安全过滤 + 流动性门槛 + 大额买入 + 冷却窗口。',
    'signalsSource': '来源',
    'signalsDecay': '衰减',
    'signalsStatusPending': '待确认',
    'signalsStatusConfirmed': '已确认',
    'signalsStatusExecuted': '已执行',
    'signalsStatusExpired': '已过期',
    'signalsStatusRejected': '已拒绝',
    'realtimeLive': '实时',
    'realtimePolling': '轮询（降级）',
    'signalsLoadFailed': '加载失败：{error}',
    'positionsTitle': '持仓',
    'positionsEmpty': '当前无持仓',
    'loginEmail': '邮箱',
    'loginPassword': '密码',
    'loginSignIn': '登录',
  };

  static const Map<String, String> _en = {
    'appTitle': 'meme-bot',
    'navOverview': 'Overview',
    'navSignals': 'Signals',
    'navPositions': 'Positions',
    'commonRefresh': 'Refresh',
    'commonRetry': 'Retry',
    'commonLoading': 'Loading…',
    'commonLanguage': 'Language',
    'commonChain': 'Chain',
    'commonToken': 'Token',
    'commonAmount': 'Amount',
    'commonLiquidity': 'Liquidity',
    'commonStatus': 'Status',
    'commonEntryPrice': 'Entry price',
    'commonCurrentPrice': 'Mark price',
    'commonPnl': 'Unrealized PnL',
    'settingsBaseUrlTitle': 'Backend URL',
    'settingsBaseUrlHint': 'http://10.0.2.2:8080',
    'settingsBaseUrlHelp':
        'Use 10.0.2.2 for the Android emulator; use your LAN IP on a physical device',
    'settingsCancel': 'Cancel',
    'settingsSave': 'Save',
    'healthMode': 'Mode',
    'healthChains': 'Ready chains',
    'healthDatabase': 'Database',
    'healthSigner': 'Signer',
    'healthConnected': 'Connected',
    'healthNotConnected': 'Not connected',
    'healthLoaded': 'Loaded',
    'healthNotLoaded': 'Not loaded',
    'healthDatabaseOkHint': 'SaaS features available',
    'healthDatabaseDownHint': 'Degraded mode',
    'healthDryRunHint': 'dry_run: no real trades are sent',
    'healthLiveHint': 'live: handle with care',
    'healthUnavailable': 'Cannot reach backend ({base}): {error}',
    'riskTitle': 'Risk control',
    'riskDescription':
        'While paused, every order (including agent-initiated ones) is rejected until you resume manually.',
    'riskPause': 'Pause trading',
    'riskResume': 'Resume trading',
    'riskPauseReason': 'Manual pause from mobile app',
    'signalsTitle': 'Signal stream',
    'signalsEmpty':
        'No active signals yet.\nA signal must pass the security filter, liquidity floor, large-buy check and cooldown window.',
    'signalsSource': 'Source',
    'signalsDecay': 'Decay',
    'signalsStatusPending': 'Pending',
    'signalsStatusConfirmed': 'Confirmed',
    'signalsStatusExecuted': 'Executed',
    'signalsStatusExpired': 'Expired',
    'signalsStatusRejected': 'Rejected',
    'realtimeLive': 'Live',
    'realtimePolling': 'Polling (fallback)',
    'signalsLoadFailed': 'Failed to load: {error}',
    'positionsTitle': 'Positions',
    'positionsEmpty': 'No open positions',
    'loginEmail': 'Email',
    'loginPassword': 'Password',
    'loginSignIn': 'Sign in',
  };
}

class _AppLocalizationsDelegate
    extends LocalizationsDelegate<AppLocalizations> {
  const _AppLocalizationsDelegate();

  @override
  bool isSupported(Locale locale) => AppLocalizations.supportedLocales
      .any((l) => l.languageCode == locale.languageCode);

  @override
  Future<AppLocalizations> load(Locale locale) async =>
      AppLocalizations(locale);

  @override
  bool shouldReload(_AppLocalizationsDelegate old) => false;
}
