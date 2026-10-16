import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math';
import 'dart:typed_data';

import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/protobuf.dart' as codec;
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline/lantern_client_offline_adapter.dart';

import 'automatic_diagnostics.dart';

/// Adds one fixed automatic-session mode to the existing resource probes.
/// Immediate deterministic wire replies measure client work, not Server speed.
Future<Map<String, Object?>> automaticBaseline({
  AutomaticDiagnostics? diagnostics,
  Future<Map<String, Object?>> Function()? stoppedSnapshot,
  required List<int> itemsPerScope,
  required Future<
    ({OfflineLanternSession session, Future<BaselineCounts> Function() inspect})
  >
  Function(
    int index,
    LanternClient client,
    LanternSessionBinding binding,
    OfflineSessionSchedule schedule,
  )
  open,
}) async {
  final rss = ProcessInfo.currentRss;
  final sessions = <OfflineLanternSession>[];
  final inspectors = <Future<BaselineCounts> Function()>[];
  final bindings = <LanternSessionBinding>[];
  final clients = <LanternClient>[];
  final wires = <BaselineWire>[];
  final subscriptions = <StreamSubscription<Object?>>[];
  final alarms = BaselineAlarms(diagnostics);
  final traffic = BaselineTraffic(diagnostics);
  diagnostics?.passive = () => {
    'transport': traffic.snapshot(),
    'alarms': alarms.snapshot(),
    'process': probeProcessCounters(),
  };
  final now = DateTime.now().toUtc();
  final expiry = now.add(const Duration(hours: 1));
  var closed = false, failed = false, mayClose = true;
  Future<void> closeSessions() async {
    for (final session in sessions.reversed) {
      await session.close();
    }
  }

  try {
    for (var i = 0; i < itemsPerScope.length; i++) {
      final wire = BaselineWire(expiry, traffic);
      final client = LanternClient.connect(
        Uri.parse('http://localhost:6380'),
        allowInsecure: true,
        transport: wire,
        retryPolicy: const RetryPolicy(maxAttempts: 1),
      );
      final binding = LanternSessionBinding.application(
        serviceOrigin: client.endpoint,
        securityDomain: 'automatic-baseline',
        accountIdentity: 'scope-$i',
        authorityBinding: 'a',
      );
      clients.add(client);
      wires.add(wire);
      bindings.add(binding);
      final opened = await open(i, client, binding, alarms.schedule);
      sessions.add(opened.session);
      inspectors.add(opened.inspect);
    }
    diagnostics?.phase = 'enqueue';
    final tickets = <MutationTicket<PutOutcome>>[];
    final enqueue = Stopwatch()..start();
    for (var i = 0; i < sessions.length; i++) {
      tickets.add(
        await sessions[i].mutations.submit(
          MutationRequest.putVertices([
            for (var j = 0; j < itemsPerScope[i]; j++)
              VertexInput(
                key: 'put-$j',
                value: VertexValue.int32(j),
                expiresAt: expiry,
              ),
          ]),
        ),
      );
    }
    enqueue.stop();
    final settle = Stopwatch()..start();
    diagnostics?.phase = 'settlement';
    diagnostics?.settlementStartedAt = diagnostics.micros();
    diagnostics?.emit('settlementStart', diagnostics.snapshot());
    for (final session in sessions) {
      await session.setTransferAllowed(true);
    }
    final ticket = Future.wait([
      for (var i = 0; i < sessions.length; i++)
        sessions[i].mutations.reobserve(tickets[i].reference),
    ]);
    if (diagnostics == null) {
      await ticket.timeout(const Duration(seconds: 30));
    } else {
      await diagnostics.waitTicket(ticket);
    }
    for (var turn = 0; turn < 500; turn++) {
      final counts = await Future.wait(inspectors.map((inspect) => inspect()));
      if (counts.every((c) => c.outbox == 0)) break;
      if (turn == 499) {
        throw StateError('automatic_baseline:durable_settlement');
      }
      await Future<void>.delayed(const Duration(milliseconds: 10));
    }
    settle.stop();
    diagnostics?.emit('settled', diagnostics.snapshot());
    diagnostics?.phase = 'traversal';
    final data = sessions.first;
    final options = <TraversalOptions>[
      const BfsOptions(step: 2, fanOut: 2),
      const PprOptions(),
      const LocalCommunityOptions(),
    ];
    final ready = <Future<void>>[];
    for (final traversal in options) {
      final first = Completer<void>();
      subscriptions.add(
        data.watchIlluminate('v0', traversal: traversal).listen((s) {
          if (s.confirmed.state == ConfirmedState.present &&
              !first.isCompleted) {
            first.complete();
          }
        }),
      );
      ready.add(first.future);
    }
    final vertexReady = Completer<void>();
    subscriptions.add(
      data.watchVertex('v0').listen((s) {
        if (s.confirmed.state == ConfirmedState.present &&
            !vertexReady.isCompleted) {
          vertexReady.complete();
        }
      }),
    );
    ready.add(vertexReady.future);
    await Future.wait(ready).timeout(const Duration(seconds: 5));
    final entitySamples = <int>[];
    for (var i = 0; i < 32; i++) {
      final watch = Stopwatch()..start();
      final read = await data.getVertex(
        'v0',
        options: const GetOptions(source: ReadSource.serverOnly),
      );
      baselineRequire(
        read.confirmed.state == ConfirmedState.present,
        'entity_read',
      );
      entitySamples.add(watch.elapsedMicroseconds);
    }
    const families = ['bfs', 'ppr', 'community'];
    final querySamples = <String, List<int>>{
      for (final family in families) family: [],
    };
    final queryPhase = Stopwatch()..start();
    // The approved phase has 32 total samples, drawn in fixed round-robin
    // order across the three families (11/11/10), not 32 per family.
    for (var i = 0; i < 32; i++) {
      final family = i % options.length;
      final watch = Stopwatch()..start();
      final read = await data.illuminate(
        'v0',
        traversal: options[family],
        options: const TraversalReadOptions(source: ReadSource.serverOnly),
      );
      baselineRequire(
        read.confirmed.value!.vertices.length == 64 &&
            read.confirmed.value!.allEdges.length == 128,
        'graph_dimensions',
      );
      querySamples[families[family]]!.add(watch.elapsedMicroseconds);
    }
    queryPhase.stop();
    var outbox = 0, claims = 0, operations = 0, retainedBytes = 0;
    for (final inspect in inspectors) {
      final counts = await inspect();
      outbox += counts.outbox;
      claims += counts.claims;
      operations += counts.operations;
      retainedBytes += counts.memoryBytes;
    }
    final close = Stopwatch()..start();
    for (final subscription in subscriptions) {
      await subscription.cancel();
    }
    subscriptions.clear();
    diagnostics?.phase = 'close';
    if (diagnostics == null) {
      await closeSessions();
    } else {
      await diagnostics.close(closeSessions);
    }
    close.stop();
    closed = true;
    final count = itemsPerScope.reduce((a, b) => a + b);
    final rssDelta = max(0, ProcessInfo.currentRss - rss);
    final result = <String, Object?>{
      'baseline': 'initial-1766-automatic',
      'scopes': sessions.length,
      'items': count,
      'graphVertices': 64,
      'graphEdges': 128,
      'activeQueries': 4,
      'entitySamples': entitySamples.length,
      'traversalSamples': querySamples.values.fold<int>(
        0,
        (n, v) => n + v.length,
      ),
      'traversalSamplesPerFamily': {
        for (final e in querySamples.entries) e.key: e.value.length,
      },
      'enqueueMillis': enqueue.elapsedMilliseconds,
      'settleMillis': settle.elapsedMilliseconds,
      'closeMillis': close.elapsedMilliseconds,
      'entityReadMicros': baselinePercentiles(entitySamples),
      'traversalMicros': {
        for (final e in querySamples.entries)
          e.key: baselinePercentiles(e.value),
      },
      'traversalPhaseMillis': queryPhase.elapsedMilliseconds,
      'rssDeltaBytes': rssDelta,
      'memorySnapshotBytes': retainedBytes,
      'peakSends': traffic.peakSends,
      'peakReads': traffic.peakReads,
      'peakTraversals': traffic.peakTraversals,
      'peakAlarms': alarms.peak,
      'finalAlarms': alarms.active,
      'finalInFlight': traffic.active,
      'finalSubscriptions': subscriptions.length,
      'finalOutbox': outbox,
      'finalClaims': claims,
      'retainedOperations': operations,
    };
    void requireMeasurement(bool value, String label) {
      if (!value) {
        // Preserve measured failure values; a failed ceiling must not erase
        // the baseline or invite selecting a later, faster sample.
        stderr.writeln(jsonEncode({...result, 'failedBudget': label}));
        throw StateError('automatic_baseline:$label');
      }
    }

    requireMeasurement(enqueue.elapsedMilliseconds <= 5000, 'enqueue_budget');
    requireMeasurement(settle.elapsedMilliseconds <= 30000, 'settle_budget');
    requireMeasurement(close.elapsedMilliseconds <= 5000, 'close_budget');
    requireMeasurement(
      queryPhase.elapsedMilliseconds <= 5000,
      'traversal_phase_budget',
    );
    requireMeasurement(
      [
        ...entitySamples,
        ...querySamples.values.expand((v) => v),
      ].every((v) => v <= 1000000),
      'sample_budget',
    );
    requireMeasurement(
      rssDelta <= 512 * 1024 * 1024 && retainedBytes <= 4 * 1024 * 1024,
      'memory_budget',
    );
    requireMeasurement(
      wires.fold(0, (n, w) => n + w.sends) == count &&
          outbox == 0 &&
          claims == 0,
      'settlement',
    );
    requireMeasurement(
      traffic.peakSends <= 4 && wires.every((w) => w.peakSends <= 2),
      'send_permits',
    );
    requireMeasurement(
      traffic.peakReads <= 8 && traffic.peakTraversals <= 2,
      'read_permits',
    );
    requireMeasurement(
      wires.every((w) => w.peakReads <= 4 && w.peakTraversals <= 1),
      'scope_read_permits',
    );
    requireMeasurement(
      alarms.active == 0 &&
          alarms.peak <= sessions.length &&
          traffic.active == 0,
      'joined',
    );
    diagnostics?.emit('completed', {
      'measurements': result,
      ...diagnostics.snapshot(),
    });
    return result;
  } catch (_) {
    failed = true;
    if (diagnostics != null) {
      diagnostics.emit('failure', diagnostics.snapshot());
      mayClose = await diagnostics.inspectStopped(
        suspend: () async {
          for (final session in sessions) {
            await session.suspend();
          }
        },
        snapshot: () async => {
          'durable': await stoppedSnapshot?.call(),
          'healthAfterDiagnosticSuspension': [
            for (final session in sessions)
              await session.syncStatus.first.then(
                (s) => {
                  'activeMutations': s.activeMutations,
                  'pause': s.pause.name,
                  'lifecycle': s.lifecycle.name,
                  'reachability': s.reachability.name,
                  'deferred': s.deferred,
                },
              ),
          ],
        },
      );
    }
    rethrow;
  } finally {
    for (final sub in subscriptions) {
      await sub.cancel();
    }
    if (!closed && mayClose) {
      if (diagnostics == null) {
        await closeSessions();
      } else {
        try {
          await diagnostics.close(closeSessions);
        } catch (_) {
          if (!failed) rethrow;
        }
      }
    }
    if (mayClose && (diagnostics?.cleanupConfirmed ?? true)) {
      for (final client in clients) {
        await client.close();
      }
    }
  }
}

typedef BaselineCounts = ({
  int outbox,
  int claims,
  int operations,
  int memoryBytes,
});

void baselineRequire(bool value, String label) {
  if (!value) throw StateError('automatic_baseline:$label');
}

Map<String, int> baselinePercentiles(List<int> values) {
  final sorted = [...values]..sort();
  return {
    for (final p in [50, 99])
      'p$p': sorted[(sorted.length * p / 100).ceil() - 1],
  };
}

final class BaselineAlarms {
  BaselineAlarms([this.diagnostics]);
  final AutomaticDiagnostics? diagnostics;
  int active = 0, peak = 0, scheduled = 0, fired = 0, canceled = 0;
  int? lastFire;
  final Map<_Alarm, int> _deadlines = {};
  Map<String, Object?> snapshot() => {
    'scheduled': scheduled,
    'fired': fired,
    'canceled': canceled,
    'active': active,
    'lastFireMicros': lastFire,
    'outstandingDeadlineMicros': _deadlines.values.toList()..sort(),
  };
  OfflineSessionAlarm schedule(Duration delay, void Function() callback) {
    active++;
    scheduled++;
    peak = max(peak, active);
    late _Alarm alarm;
    alarm = _Alarm(delay, callback, (didFire) {
      active--;
      _deadlines.remove(alarm);
      if (didFire) {
        fired++;
        lastFire = diagnostics?.micros();
      } else {
        canceled++;
      }
    });
    if (diagnostics != null) {
      _deadlines[alarm] = diagnostics!.micros() + delay.inMicroseconds;
    }
    return alarm;
  }
}

final class _Alarm implements OfflineSessionAlarm {
  _Alarm(Duration delay, void Function() callback, this.finished) {
    timer = Timer(delay, () {
      if (live) {
        live = false;
        finished(true);
        callback();
      }
    });
  }
  final void Function(bool) finished;
  late Timer timer;
  bool live = true;
  @override
  void cancel() {
    if (live) {
      live = false;
      timer.cancel();
      finished(false);
    }
  }
}

final class BaselineTraffic {
  BaselineTraffic([this.diagnostics]);
  final AutomaticDiagnostics? diagnostics;
  int sends = 0, reads = 0, traversals = 0;
  int peakSends = 0, peakReads = 0, peakTraversals = 0;
  int started = 0, replies = 0;
  int? firstProgress, lastProgress;
  final List<Map<String, int>> milestones = [];
  int get active => sends + reads;
  void reply() {
    replies++;
    final now = diagnostics?.micros();
    firstProgress ??= now;
    lastProgress = now;
    if (now != null && replies % 10 == 0 && milestones.length < 10) {
      milestones.add({'replies': replies, 'elapsedMicros': now});
    }
  }

  Map<String, Object?> snapshot() => {
    'sendsStarted': started,
    'repliesReturned': replies,
    'inFlight': active,
    'firstProgressMicros': firstProgress,
    'lastProgressMicros': lastProgress,
    'replyMilestones': [
      for (final row in milestones) {...row},
    ],
  };
}

final class BaselineWire implements connect.Transport {
  BaselineWire(this.expiry, this.traffic);
  final DateTime expiry;
  final BaselineTraffic traffic;
  Future<void> Function(String, Map<String, dynamic>)? before;
  int calls = 0;
  int sends = 0, activeSends = 0, activeReads = 0, activeTraversals = 0;
  int peakSends = 0, peakReads = 0, peakTraversals = 0;
  Map<String, Object> vertex(String key) => {
    'key': key,
    'string': 'x',
    'expiration': expiry.toIso8601String(),
  };
  @override
  Future<connect.UnaryResponse<I, O>> unary<I extends Object, O extends Object>(
    connect.Spec<I, O> spec,
    I input, [
    connect.CallOptions? options,
  ]) async {
    final method = spec.procedure.split('/').last;
    calls++;
    final send = method == 'PutVertices', traversal = method == 'Illuminate';
    if (send) {
      sends++;
      activeSends++;
      traffic.sends++;
      traffic.started++;
      peakSends = max(peakSends, activeSends);
      traffic.peakSends = max(traffic.peakSends, traffic.sends);
    } else {
      activeReads++;
      traffic.reads++;
      peakReads = max(peakReads, activeReads);
      traffic.peakReads = max(traffic.peakReads, traffic.reads);
      if (traversal) {
        activeTraversals++;
        traffic.traversals++;
        peakTraversals = max(peakTraversals, activeTraversals);
        traffic.peakTraversals = max(
          traffic.peakTraversals,
          traffic.traversals,
        );
      }
    }
    try {
      await Future<void>.delayed(Duration.zero);
      const json = codec.JsonCodec();
      final request =
          jsonDecode(utf8.decode(json.encode(input))) as Map<String, dynamic>;
      await before?.call(method, request);
      final response = switch (method) {
        'PutVertices' => {
          'outcomes': [for (final _ in request['vertices'] as List) 1],
        },
        'GetVertices' => {
          'vertices': [
            for (final key in request['keys'] as List) vertex(key as String),
          ],
        },
        'Illuminate' => {
          'graph': {
            'vertices': [for (var i = 0; i < 64; i++) vertex('v$i')],
            'edges': [
              for (var i = 0; i < 64; i++)
                for (final step in [1, 2])
                  {
                    'tail': 'v$i',
                    'head': 'v${(i + step) % 64}',
                    'weight': 1,
                    'expiration': expiry.toIso8601String(),
                  },
            ],
          },
        },
        _ => throw StateError('unexpected baseline method $method'),
      };
      final output = spec.outputFactory();
      json.decode(
        Uint8List.fromList(utf8.encode(jsonEncode(response))),
        output,
      );
      if (send) traffic.reply();
      return connect.UnaryResponse(
        spec,
        connect.Headers(),
        output,
        connect.Headers(),
      );
    } finally {
      if (send) {
        activeSends--;
        traffic.sends--;
      } else {
        activeReads--;
        traffic.reads--;
        if (traversal) {
          activeTraversals--;
          traffic.traversals--;
        }
      }
    }
  }

  @override
  Future<connect.StreamResponse<I, O>>
  stream<I extends Object, O extends Object>(
    connect.Spec<I, O> spec,
    Stream<I> input, [
    connect.CallOptions? options,
  ]) => throw UnimplementedError();
}
