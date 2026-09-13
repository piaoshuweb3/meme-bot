import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';

import 'core/api_client.dart';
import 'core/realtime_client.dart';
import 'core/realtime_hub.dart';
import 'core/secure_store.dart';
import 'features/dashboard/dashboard_page.dart';
import 'features/positions/positions_page.dart';
import 'features/signals/signals_page.dart';
import 'l10n/app_localizations.dart';

/// 默认后端地址（Android 模拟器用 10.0.2.2 访问宿主机）。
const defaultApiBase = String.fromEnvironment('API_BASE', defaultValue: 'http://10.0.2.2:8080');

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();

  // 从安全存储恢复会话与偏好：令牌只存平台安全区（不可用时降级为内存）
  final store = SecureStore();
  final baseUrl = await store.readBaseUrl() ?? defaultApiBase;
  final token = await store.readToken();
  final localeTag = await store.readLocale();

  runApp(MemeBotApp(store: store, baseUrl: baseUrl, token: token, initialLocaleTag: localeTag));
}

/// 应用根组件：本地化 + REST 客户端 + 实时连接 + 安全存储。
class MemeBotApp extends StatefulWidget {
  const MemeBotApp({
    super.key,
    required this.store,
    required this.baseUrl,
    this.token,
    this.initialLocaleTag,
  });

  final SecureStore store;
  final String baseUrl;
  final String? token;
  final String? initialLocaleTag;

  @override
  State<MemeBotApp> createState() => _MemeBotAppState();
}

class _MemeBotAppState extends State<MemeBotApp> {
  Locale? _locale;

  @override
  void initState() {
    super.initState();
    final tag = widget.initialLocaleTag;
    if (tag != null && tag.isNotEmpty) {
      final parts = tag.split('-');
      _locale = parts.length > 1 ? Locale(parts[0], parts[1]) : Locale(parts[0]);
    }
  }

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      debugShowCheckedModeBanner: false,
      onGenerateTitle: (ctx) => AppLocalizations.of(ctx).appTitle,
      locale: _locale,
      supportedLocales: AppLocalizations.supportedLocales,
      localizationsDelegates: const [
        AppLocalizations.delegate,
        GlobalMaterialLocalizations.delegate,
        GlobalWidgetsLocalizations.delegate,
        GlobalCupertinoLocalizations.delegate,
      ],
      theme: ThemeData.dark(useMaterial3: true).copyWith(
        scaffoldBackgroundColor: const Color(0xFF0B1020),
        cardColor: const Color(0xFF141A2E),
        colorScheme: const ColorScheme.dark(primary: Color(0xFF4F8CFF)),
      ),
      home: HomeShell(
        store: widget.store,
        baseUrl: widget.baseUrl,
        token: widget.token,
        onLocaleChanged: (next) async {
          setState(() => _locale = next);
          await widget.store.writeLocale(next == null ? '' : next.toLanguageTag());
        },
      ),
    );
  }
}

/// 底部导航容器（总览 / 信号 / 持仓），持有全局实时连接。
class HomeShell extends StatefulWidget {
  const HomeShell({
    super.key,
    required this.store,
    required this.baseUrl,
    required this.token,
    required this.onLocaleChanged,
  });

  final SecureStore store;
  final String baseUrl;
  final String? token;
  final ValueChanged<Locale?> onLocaleChanged;

  @override
  State<HomeShell> createState() => _HomeShellState();
}

class _HomeShellState extends State<HomeShell> {
  int _index = 0;
  late final ApiClient _api = ApiClient(baseUrl: widget.baseUrl)..token = widget.token;
  late final RealtimeHub _hub = RealtimeHub(baseUrl: widget.baseUrl);

  @override
  void initState() {
    super.initState();
    _hub.start();
  }

  @override
  void dispose() {
    _hub.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final pages = <Widget>[
      DashboardPage(api: _api, hub: _hub),
      SignalsPage(api: _api, hub: _hub),
      PositionsPage(api: _api, hub: _hub),
    ];

    return Scaffold(
      appBar: AppBar(
        title: Text(l10n.appTitle),
        actions: [
          _realtimeBadge(l10n),
          _languageMenu(l10n),
          IconButton(
            tooltip: l10n.settingsBaseUrlTitle,
            icon: const Icon(Icons.settings),
            onPressed: () => _editBaseUrl(context),
          ),
        ],
      ),
      body: pages[_index],
      bottomNavigationBar: NavigationBar(
        selectedIndex: _index,
        onDestinationSelected: (i) => setState(() => _index = i),
        destinations: [
          NavigationDestination(icon: const Icon(Icons.dashboard_outlined), label: l10n.navOverview),
          NavigationDestination(icon: const Icon(Icons.bolt_outlined), label: l10n.navSignals),
          NavigationDestination(icon: const Icon(Icons.account_balance_wallet_outlined), label: l10n.navPositions),
        ],
      ),
    );
  }

  /// 实时通道状态徽章（绿=实时 / 灰=降级轮询）。
  Widget _realtimeBadge(AppLocalizations l10n) {
    return ValueListenableBuilder<RealtimeStatus>(
      valueListenable: _hub.status,
      builder: (context, status, _) {
        final live = status == RealtimeStatus.open;
        return Padding(
          padding: const EdgeInsets.symmetric(horizontal: 8),
          child: Chip(
            visualDensity: VisualDensity.compact,
            backgroundColor: live ? const Color(0x333DDC97) : const Color(0x33FFFFFF),
            label: Text(
              live ? l10n.realtimeLive : l10n.realtimePolling,
              style: const TextStyle(fontSize: 11),
            ),
          ),
        );
      },
    );
  }

  Widget _languageMenu(AppLocalizations l10n) {
    return PopupMenuButton<Locale?>(
      tooltip: l10n.commonLanguage,
      icon: const Icon(Icons.translate),
      onSelected: widget.onLocaleChanged,
      itemBuilder: (ctx) => const [
        PopupMenuItem<Locale?>(value: null, child: Text('跟随系统 / System')),
        PopupMenuItem<Locale?>(value: Locale('zh', 'CN'), child: Text('简体中文')),
        PopupMenuItem<Locale?>(value: Locale('en', 'US'), child: Text('English')),
      ],
    );
  }

  Future<void> _editBaseUrl(BuildContext context) async {
    final l10n = AppLocalizations.of(context);
    final controller = TextEditingController(text: _api.baseUrl);
    final value = await showDialog<String>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(l10n.settingsBaseUrlTitle),
        content: TextField(
          controller: controller,
          keyboardType: TextInputType.url,
          autofocus: true,
          decoration: InputDecoration(
            hintText: l10n.settingsBaseUrlHint,
            helperText: l10n.settingsBaseUrlHelp,
          ),
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx), child: Text(l10n.settingsCancel)),
          FilledButton(onPressed: () => Navigator.pop(ctx, controller.text.trim()), child: Text(l10n.settingsSave)),
        ],
      ),
    );
    if (value != null && value.isNotEmpty) {
      await widget.store.writeBaseUrl(value);
      setState(() => _api.baseUrl = value);
    }
  }
}
