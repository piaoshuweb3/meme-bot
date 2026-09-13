import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';

import 'core/api_client.dart';
import 'features/dashboard/dashboard_page.dart';
import 'features/positions/positions_page.dart';
import 'features/signals/signals_page.dart';
import 'l10n/app_localizations.dart';

void main() {
  runApp(const MemeBotApp());
}

/// 应用根组件：负责 locale 状态与本地化代理装配。
///
/// 国际化要点：
///   - locale 使用 BCP 47（zh-CN / en-US），由 [AppLocalizations.supportedLocales] 声明；
///   - 未显式选择语言时跟随系统 locale（[platformDispatcher.locale]）；
///   - 所有面向用户的文案都来自 [AppLocalizations]，页面内零硬编码。
class MemeBotApp extends StatefulWidget {
  const MemeBotApp({super.key});

  @override
  State<MemeBotApp> createState() => _MemeBotAppState();
}

class _MemeBotAppState extends State<MemeBotApp> {
  Locale? _locale;

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
        locale: _locale,
        onLocaleChanged: (next) => setState(() => _locale = next),
      ),
    );
  }
}

/// 底部导航容器（总览 / 信号 / 持仓）。
class HomeShell extends StatefulWidget {
  const HomeShell({
    super.key,
    required this.locale,
    required this.onLocaleChanged,
  });

  final Locale? locale;
  final ValueChanged<Locale?> onLocaleChanged;

  @override
  State<HomeShell> createState() => _HomeShellState();
}

class _HomeShellState extends State<HomeShell> {
  int _index = 0;
  late final ApiClient _api = ApiClient();

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final pages = <Widget>[
      DashboardPage(api: _api),
      SignalsPage(api: _api),
      PositionsPage(api: _api),
    ];

    return Scaffold(
      appBar: AppBar(
        title: Text(l10n.appTitle),
        actions: [
          _languageMenu(context),
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
          NavigationDestination(
            icon: const Icon(Icons.dashboard_outlined),
            label: l10n.navOverview,
          ),
          NavigationDestination(
            icon: const Icon(Icons.bolt_outlined),
            label: l10n.navSignals,
          ),
          NavigationDestination(
            icon: const Icon(Icons.account_balance_wallet_outlined),
            label: l10n.navPositions,
          ),
        ],
      ),
    );
  }

  /// 语言切换菜单：跟随系统 / 简体中文 / English。
  Widget _languageMenu(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    return PopupMenuButton<Locale?>(
      tooltip: l10n.commonLanguage,
      icon: const Icon(Icons.translate),
      onSelected: widget.onLocaleChanged,
      itemBuilder: (ctx) => [
        PopupMenuItem<Locale?>(
          value: null,
          child: Text('${l10n.commonLanguage} · ${l10n.navOverview == '总览' ? '系统' : 'System'}'),
        ),
        const PopupMenuItem<Locale?>(
          value: Locale('zh', 'CN'),
          child: Text('简体中文'),
        ),
        const PopupMenuItem<Locale?>(
          value: Locale('en', 'US'),
          child: Text('English'),
        ),
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
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: Text(l10n.settingsCancel),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, controller.text.trim()),
            child: Text(l10n.settingsSave),
          ),
        ],
      ),
    );
    if (value != null && value.isNotEmpty) {
      setState(() => _api.baseUrl = value);
    }
  }
}
