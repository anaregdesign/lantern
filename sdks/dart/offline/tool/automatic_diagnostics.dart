import 'dart:async';
import 'dart:convert';
import 'dart:io';

typedef ProbeBound = Future<T> Function<T>(Future<T> future, Duration limit);

/// Private, opt-in probe instrumentation. Never used by a production session.
final class AutomaticDiagnostics {
  AutomaticDiagnostics({
    required this.write,
    int Function()? micros,
    ProbeBound? bound,
  }) : micros = micros ?? (Stopwatch()..start()).elapsedMicrosecondsValue,
       bound = bound ?? _bound;

  final void Function(Map<String, Object?>) write;
  final int Function() micros;
  final ProbeBound bound;
  String phase = 'setup';
  Map<String, Object?> Function() database = () => {};
  Map<String, Object?> Function() passive = () => {};
  bool timedOut = false, ticketResolved = false, cleanupConfirmed = true;
  int? ticketResolvedAt, waitStartedAt, settlementStartedAt;

  static Future<T> _bound<T>(Future<T> future, Duration limit) =>
      future.timeout(limit);

  void emit(String event, Map<String, Object?> value) => write({
    'schema': 1,
    'qualifies': false,
    'contentFree': true,
    'event': event,
    'phase': phase,
    'elapsedMicros': micros(),
    ...value,
  });

  Map<String, Object?> snapshot() => {
    'timeoutLatched': timedOut,
    'ticketResolved': ticketResolved,
    'ticketResolvedAtMicros': ticketResolvedAt,
    'waitElapsedMicros': waitStartedAt == null
        ? null
        : micros() - waitStartedAt!,
    'settlementElapsedMicros': settlementStartedAt == null
        ? null
        : micros() - settlementStartedAt!,
    'passive': passive(),
    'database': database(),
  };

  Future<void> waitTicket(Future<Object?> ticket) async {
    waitStartedAt = micros();
    final observed = ticket.then((_) {
      ticketResolved = true;
      ticketResolvedAt = micros();
    });
    try {
      await bound(observed, const Duration(seconds: 30));
    } on TimeoutException {
      // Synchronous evidence before cleanup can settle a final response.
      timedOut = true;
      emit('deadline', snapshot());
      rethrow;
    }
  }

  Future<bool> inspectStopped({
    required Future<void> Function() suspend,
    required Future<Map<String, Object?>> Function() snapshot,
  }) async {
    phase = 'failure-inspection';
    try {
      await bound(suspend(), const Duration(seconds: 5));
    } catch (_) {
      cleanupConfirmed = false;
      emit('joinUnconfirmed', this.snapshot());
      return false;
    }
    emit('joined', this.snapshot());
    try {
      final stopped = await bound(snapshot(), const Duration(seconds: 2));
      emit('postJoin', {'stateAfterDiagnosticSuspension': stopped});
    } catch (_) {
      emit('snapshotUnavailable', this.snapshot());
    }
    return true;
  }

  Future<void> close(Future<void> Function() close) async {
    try {
      await bound(close(), const Duration(seconds: 5));
      emit('closed', snapshot());
    } catch (_) {
      cleanupConfirmed = false;
      emit('closeUnconfirmed', snapshot());
      rethrow;
    }
  }
}

extension on Stopwatch {
  int elapsedMicrosecondsValue() => elapsedMicroseconds;
}

final class ProbeHistogram {
  int count = 0, totalMicros = 0, maximumMicros = 0;
  final buckets = List<int>.filled(6, 0);
  void add(int value) {
    count++;
    totalMicros += value;
    if (value > maximumMicros) maximumMicros = value;
    final cutoffs = [100, 1000, 10000, 100000, 1000000];
    var bucket = 0;
    while (bucket < cutoffs.length && value > cutoffs[bucket]) {
      bucket++;
    }
    buckets[bucket]++;
  }

  Map<String, Object?> snapshot() => {
    'count': count,
    'totalMicros': totalMicros,
    'maximumMicros': maximumMicros,
    'histogramLeMicros': [100, 1000, 10000, 100000, 1000000, null],
    'histogramCounts': [...buckets],
  };
}

/// Only aggregate process data; no environment, identifiers or raw /proc text.
Map<String, Object?> probeProcessCounters() {
  final result = <String, Object?>{'rssBytes': ProcessInfo.currentRss};
  if (!Platform.isLinux) {
    return {...result, 'cpuTicks': null, 'ioBytes': null};
  }
  try {
    final raw = File('/proc/self/stat').readAsStringSync();
    final fields = raw.substring(raw.lastIndexOf(')') + 2).split(' ');
    result['cpuTicks'] = int.parse(fields[11]) + int.parse(fields[12]);
  } catch (_) {
    result['cpuTicks'] = null;
  }
  try {
    final fields = <String, int>{};
    for (final line in File('/proc/self/io').readAsLinesSync()) {
      final pair = line.split(':');
      if (['read_bytes', 'write_bytes', 'rchar', 'wchar'].contains(pair[0])) {
        fields[pair[0]] = int.parse(pair[1].trim());
      }
    }
    result['ioBytes'] = fields;
  } catch (_) {
    result['ioBytes'] = null;
  }
  return result;
}

void Function(Map<String, Object?>) probeJsonWriter(File output) => (event) {
  final line = '${jsonEncode(event)}\n';
  output.writeAsStringSync(line, mode: FileMode.append, flush: true);
  stderr.write(line);
};
