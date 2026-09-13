import 'package:flutter/material.dart';
import 'package:intl/intl.dart';

import '../../core/api_client.dart';
import '../../l10n/app_localizations.dart';

/// 总览页：后端状态、熔断控制、关键指标（文案全部本地化）。
class DashboardPage extends StatefulWidget {
  const DashboardPage({super.key, required this.api});

  final ApiClient api;

  @override
  State<DashboardPage> createState() => _DashboardPageState();
}

class _DashboardPageState extends State<DashboardPage> {
  Map<String, dynamic>? _health;
  String? _error;
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final h = await widget.api.health();
      if (!mounted) return;
      setState(() {
        _health = h;
        _error = null;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = e.toString());
    }
  }

  Future<void> _toggle(bool pause) async {
    final l10n = AppLocalizations.of(context);
    setState(() => _busy = true);
    try {
      if (pause) {
        await widget.api.pause(l10n.riskPauseReason);
      } else {
        await widget.api.resume();
      }
      await _load();
    } catch (e) {
      if (mounted) setState(() => _error = e.toString());
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final chains =
        (_health?['chains'] as List?)?.cast<String>() ?? const <String>[];
    final numberFormat = NumberFormat.decimalPattern(l10n.localeName);

    return RefreshIndicator(
      onRefresh: _load,
      child: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          if (_error != null)
            Card(
              color: const Color(0x33FF5C5C),
              child: Padding(
                padding: const EdgeInsets.all(12),
                child: Text(
                  l10n.healthUnavailable(widget.api.baseUrl, _error!),
                  // 屏幕阅读器优先播报错误
                  semanticsLabel: l10n.healthUnavailable(widget.api.baseUrl, _error!),
                ),
              ),
            ),
          _statCard(
            l10n.healthMode,
            _health?['mode']?.toString() ?? '—',
            hint: _health?['dry_run'] == true
                ? l10n.healthDryRunHint
                : l10n.healthLiveHint,
          ),
          _statCard(
            l10n.healthChains,
            numberFormat.format(chains.length),
            hint: chains.join(' · '),
          ),
          _statCard(
            l10n.healthDatabase,
            _health?['database'] == true
                ? l10n.healthConnected
                : l10n.healthNotConnected,
            hint: _health?['database'] == true
                ? l10n.healthDatabaseOkHint
                : l10n.healthDatabaseDownHint,
          ),
          _statCard(
            l10n.healthSigner,
            _health?['signer'] == true
                ? l10n.healthLoaded
                : l10n.healthNotLoaded,
          ),
          const SizedBox(height: 8),
          Card(
            child: Padding(
              padding: const EdgeInsets.all(12),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(l10n.riskTitle, style: const TextStyle(fontSize: 16)),
                  const SizedBox(height: 8),
                  Text(
                    l10n.riskDescription,
                    style: const TextStyle(color: Colors.white70, fontSize: 12),
                  ),
                  const SizedBox(height: 12),
                  Wrap(
                    spacing: 12,
                    runSpacing: 8,
                    children: [
                      FilledButton(
                        onPressed: _busy ? null : () => _toggle(true),
                        child: Text(l10n.riskPause),
                      ),
                      OutlinedButton(
                        onPressed: _busy ? null : () => _toggle(false),
                        child: Text(l10n.riskResume),
                      ),
                    ],
                  ),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _statCard(String label, String value, {String? hint}) {
    return Card(
      child: ListTile(
        title: Text(
          label,
          style: const TextStyle(fontSize: 12, color: Colors.white54),
        ),
        subtitle: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              value,
              style: const TextStyle(fontSize: 20, color: Colors.white),
            ),
            if (hint != null && hint.isNotEmpty)
              Text(
                hint,
                style: const TextStyle(fontSize: 11, color: Colors.white38),
              ),
          ],
        ),
      ),
    );
  }
}
