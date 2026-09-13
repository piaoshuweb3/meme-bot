import 'package:flutter/material.dart';
import 'package:intl/intl.dart';

import '../../core/api_client.dart';
import '../../core/realtime_hub.dart';
import '../../l10n/app_localizations.dart';

/// 持仓页面（文案本地化 + locale 感知的价格与盈亏格式化）。
class PositionsPage extends StatefulWidget {
  const PositionsPage({super.key, required this.api, required this.hub});

  final ApiClient api;
  final RealtimeHub hub;

  @override
  State<PositionsPage> createState() => _PositionsPageState();
}

class _PositionsPageState extends State<PositionsPage> {
  List<Map<String, dynamic>> _positions = const [];
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
    widget.hub.lastPosition.addListener(_onRealtimePosition);
  }

  @override
  void dispose() {
    widget.hub.lastPosition.removeListener(_onRealtimePosition);
    super.dispose();
  }

  /// 实时更新对应持仓的标记价（无需整表重拉）。
  void _onRealtimePosition() {
    final incoming = widget.hub.lastPosition.value;
    if (incoming == null || !mounted) return;
    final id = incoming['id']?.toString();
    if (id == null) return;
    setState(() {
      for (var i = 0; i < _positions.length; i++) {
        if (_positions[i]['id']?.toString() == id) {
          _positions[i] = {..._positions[i], ...incoming};
          break;
        }
      }
    });
  }

  Future<void> _load() async {
    try {
      final res = await widget.api.positions();
      final list =
          (res['positions'] as List?)?.cast<Map<String, dynamic>>() ?? const [];
      if (!mounted) return;
      setState(() {
        _positions = list;
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
    if (_positions.isEmpty) {
      return RefreshIndicator(
        onRefresh: _load,
        child: ListView(
          padding: const EdgeInsets.all(16),
          children: [Text(l10n.positionsEmpty)],
        ),
      );
    }

    final priceFormat = NumberFormat.currency(
      locale: l10n.localeName,
      symbol: r'$',
      decimalDigits: 4,
    );
    final percent = NumberFormat.decimalPercentPattern(
      locale: l10n.localeName,
      decimalDigits: 2,
    );

    return RefreshIndicator(
      onRefresh: _load,
      child: ListView.separated(
        padding: const EdgeInsets.all(16),
        itemCount: _positions.length,
        separatorBuilder: (_, __) => const SizedBox(height: 8),
        itemBuilder: (context, i) {
          final p = _positions[i];
          final entry = (p['entry_price_usd'] as num?)?.toDouble() ?? 0;
          final current = (p['current_price_usd'] as num?)?.toDouble() ?? 0;
          final pnl = entry > 0 ? (current - entry) / entry : 0.0;
          final isUp = pnl >= 0;

          return Card(
            child: ListTile(
              title: Text('${p['chain']} · ${p['token_symbol'] ?? p['token']}'),
              subtitle: Padding(
                padding: const EdgeInsets.only(top: 4),
                child: Text(
                  '${l10n.commonEntryPrice} ${priceFormat.format(entry)} · '
                  '${l10n.commonCurrentPrice} ${priceFormat.format(current)}',
                ),
              ),
              trailing: Text(
                // 颜色 + 箭头符号双编码，色觉障碍用户同样可辨
                '${isUp ? '▲' : '▼'} ${percent.format(pnl.abs())}',
                semanticsLabel:
                    '${l10n.commonPnl}: ${percent.format(pnl)}',
                style: TextStyle(
                  color: isUp ? const Color(0xFF3DDC97) : const Color(0xFFFF5C5C),
                  fontWeight: FontWeight.w600,
                ),
              ),
            ),
          );
        },
      ),
    );
  }
}
