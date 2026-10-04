import 'package:lantern_client/lantern_client.dart';

/// Fixed, content-free stages shared by the crash worker and its supervisor.
enum ProbeStage {
  arguments,
  open,
  capability,
  seedVertices,
  seedEdges,
  enqueue,
  prepared,
  drain,
  unresolved,
  applied,
  reopen,
  reconcile,
  originals,
  ordinary,
}

Map<String, Object> probeFailureMarker(ProbeStage stage, Object error) => {
  'event': 'failed',
  'stage': stage.name,
  'code': switch (error) {
    LanternException(:final code) => 'transport_${code.name}',
    StateError() => 'contract',
    _ => 'unexpected',
  },
};

/// Reject arbitrary child data rather than copying it into public diagnostics.
String? parseProbeFailure(Map<String, Object?> message) {
  if (message.length != 3 || message['event'] != 'failed') return null;
  final stage = message['stage'];
  final code = message['code'];
  if (!ProbeStage.values.any((value) => value.name == stage) ||
      !{
        'contract',
        'unexpected',
        for (final value in LanternCode.values) 'transport_${value.name}',
      }.contains(code)) {
    return null;
  }
  return 'worker:$stage:$code';
}

bool isProbeFailureCode(String value) {
  final parts = value.split(':');
  return parts.length == 3 &&
      parts.first == 'worker' &&
      parseProbeFailure({
            'event': 'failed',
            'stage': parts[1],
            'code': parts[2],
          }) ==
          value;
}
