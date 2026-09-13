import 'package:flutter/material.dart';
import 'package:intl/intl.dart';

import '../../core/api_client.dart';
import '../../l10n/app_localizations.dart';

/// 信号流页面（文案本地化 + locale 感知的数字/百分比格式化）。
class SignalsPage extends StatefulWidget {
  const SignalsPage({super.key, required this.api});

  final ApiClient api;

  @override
  State<SignalsPage> createState() => _SignalsPageState();
}

class _SignalsPageState extends State<SignalsPage> {
  List<Map<String, dynamic>> _signals = const [];
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final res = await widget.api.signals(limit: 100);
      final list =
          (res['signals'] as List?)?.cast<Map<String, dynamic>>() ?? const [];
      if (!mounted) return;
      setState(() {
        _signals = list;
        _error = null;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = e.toString());
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);

    if (_error != null) {
      return RefreshIndicator(
        onRefresh: _load,
        child: ListView(
          padding: const EdgeInsets.all(16),
          children: [Text(l10n.signalsLoadFailed(_error!))],
        ),
      );
    }
    if (_signals.isEmpty) {
      return RefreshIndicator(
        onRefresh: _load,
        child: ListView(
          padding: const EdgeInsets.all(16),
          children: [Text(l10n.signalsEmpty)],
        ),
      );
    }

    final currency = NumberFormat.currency(
      locale: l10n.localeName,
      symbol: r'$',
      decimalDigits: 2,
    );
    final percent = NumberFormat.decimalPercentPattern(
      locale: l10n.localeName,
      decimalDigits: 0,
    );

    return RefreshIndicator(
      onRefresh: _load,
      child: ListView.separated(
        padding: const EdgeInsets.all(16),
        itemCount: _signals.length,
        separatorBuilder: (_, __) => const SizedBox(height: 8),
        itemBuilder: (context, i) {
          final s = _signals[i];
          final status = s['status']?.toString() ?? '';
          final amountUsd = (s['amount_usd'] as num?)?.toDouble() ?? 0;
          final liquidityUsd = (s['liquidity_usd'] as num?)?.toDouble() ?? 0;
          final decay = (s['decay'] as num?)?.toDouble() ?? 0;

          return Card(
            child: ListTile(
              title: Text(
                '${s['chain']} · ${_short(s['token']?.toString() ?? '')}',
              ),
              subtitle: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  const SizedBox(height: 4),
                  Text(
                    '${l10n.signalsSource} ${s['source']} · '
                    '${l10n.commonAmount} ${currency.format(amountUsd)}',
                  ),
                  Text(
                    '${l10n.commonLiquidity} ${currency.format(liquidityUsd)} · '
                    '${l10n.signalsDecay} ${percent.format(decay)}',
                    style: const TextStyle(fontSize: 11, color: Colors.white54),
                  ),
                ],
              ),
              trailing: Chip(
                label: Text(
                  l10n.signalStatus(status),
                  style: const TextStyle(fontSize: 11),
                ),
                backgroundColor: _statusColor(status),
              ),
            ),
          );
        },
      ),
    );
  }

  Color _statusColor(String status) {
    switch (status) {
      case 'confirmed':
        return const Color(0x333DDC97);
      case 'pending':
        return const Color(0x33FFB84D);
      case 'rejected':
        return const Color(0x33FF5C5C);
      default:
        return const Color(0x33FFFFFF);
    }
  }

  String _short(String addr) => addr.length > 14
      ? '${addr.substring(0, 6)}…${addr.substring(addr.length - 4)}'
      : addr;
}
