import 'dart:async';
import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.connect.spec.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.pb.dart' as wire;
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline/src/session_runtime.dart';
import 'package:test/test.dart';

void main() {
  late _Fixture h;
  setUp(() => h = _Fixture());
  tearDown(() => h.close());

  test(
    'active clock alarm detects rollback and recovery without an application read',
    () async {
      final session = await h.open();
      final values = <LanternTraversalSnapshot>[];
      final sub = session
          .watchIlluminate('seed', traversal: const PprOptions())
          .listen(values.add);
      h.subscriptions.add(sub);
      await h.clock.tick(const Duration(milliseconds: 50));
      expect(
        values.any((v) => v.confirmed.origin == ObservationOrigin.server),
        isTrue,
      );
      final high = h.clock.wall;
      h.clock.wall = high.subtract(const Duration(hours: 1));
      h.unavailable = true;
      await h.clock.tick(const Duration(seconds: 30), advanceWall: false);
      await h.clock.tick(const Duration(milliseconds: 50), advanceWall: false);
      expect(values.last.confirmed.state, ConfirmedState.unknown);
      final calls = h.calls;
      h.clock.wall = high;
      h.unavailable = false;
      await h.clock.tick(const Duration(seconds: 30), advanceWall: false);
      await h.clock.tick(const Duration(milliseconds: 50));
      expect(h.calls, greaterThan(calls));
      expect(values.last.confirmed.origin, ObservationOrigin.server);
      expect(h.clock.callbacks, lessThan(30));
      expect(h.clock.active, lessThanOrEqualTo(1));
      await sub.cancel();
      h.subscriptions.clear();
      final stopped = h.calls;
      await h.clock.tick(const Duration(seconds: 90));
      expect(h.calls, stopped);
      expect(h.clock.active, 0);
    },
  );

  test(
    'identical queries coalesce across shared handles; last owner tears down',
    () async {
      final one = await h.open(), two = await h.open();
      final results = <LanternTraversalSnapshot>[];
      for (final session in [one, two]) {
        h.subscriptions.add(
          session
              .watchIlluminate('seed', traversal: const PprOptions())
              .listen(results.add),
        );
      }
      await h.clock.tick(const Duration(milliseconds: 50));
      expect(h.calls, 1);
      expect(
        results
            .where((v) => v.confirmed.origin == ObservationOrigin.server)
            .length,
        2,
      );
      await one.close();
      h.sessions.remove(one);
      await h.clock.tick(const Duration(seconds: 30));
      expect(h.calls, 2);
      await two.close();
      h.sessions.remove(two);
      expect(h.clock.active, 0);
    },
  );

  test(
    'explicit route conflicts fail before network and preserve current owner',
    () async {
      await h.open();
      final different = LanternSingleResponderRoute.certified(
        client: h.client,
        responderId: 'same-node',
        isCurrent: () => true,
      );
      await expectLater(
        h.open(route: different),
        throwsA(isA<OfflineOwnerConflictException>()),
      );
      final source = LanternChangeSource(
        route: h.route,
        binding: h.binding,
        currentBinding: () => h.binding,
      );
      await expectLater(
        h.open(route: different, incoming: source),
        throwsA(isA<OfflineArgumentException>()),
      );
      expect(h.calls, 0);
    },
  );

  test(
    'required bootstrap hides earlier certified history before the tail connects',
    () async {
      final first = await h.open();
      final future = first.illuminate('seed', traversal: const PprOptions());
      await h.clock.tick(const Duration(milliseconds: 50));
      expect((await future).confirmed.state, ConfirmedState.present);
      await first.close();
      h.sessions.remove(first);
      // A new transport/owner explicitly asserts continuity, but required bootstrap wins.
      h.resetClient();
      final source = LanternChangeSource(
        route: h.route,
        binding: h.binding,
        currentBinding: () => h.binding,
      );
      final second = await h.open(incoming: source, transfer: false);
      final local = await second.illuminate(
        'seed',
        traversal: const PprOptions(),
        options: const TraversalReadOptions(source: ReadSource.cacheOnly),
      );
      expect(local.confirmed.state, ConfirmedState.unknown);
      expect(local.confirmed.cause, ObservationCause.unavailable);
      expect(h.calls, 1);
    },
  );
}

final class _Fixture {
  final store = MemoryOfflineStore();
  final clock = _Clock();
  final endpoint = Uri.parse('http://localhost:6380');
  late final LanternSessionBinding binding = LanternSessionBinding.application(
    serviceOrigin: endpoint,
    securityDomain: 'tests',
    accountIdentity: 'account',
    authorityBinding: 'a',
  );
  late LanternClient client;
  late LanternSingleResponderRoute route;
  final sessions = <OfflineLanternSession>[];
  final subscriptions = <StreamSubscription<Object?>>[];
  int calls = 0;
  bool unavailable = false;
  _Fixture() {
    resetClient();
  }
  void resetClient() {
    client = LanternClient.connect(
      endpoint,
      allowInsecure: true,
      clock: clock.now,
      transport: FakeTransportBuilder()
          .unary<wire.IlluminateRequest, wire.IlluminateResponse>(
            LanternService.illuminate,
            (r, _) {
              calls++;
              if (unavailable) {
                throw connect.ConnectException(
                  connect.Code.unavailable,
                  'fixture outage',
                );
              }
              return wire.IlluminateResponse(graph: wire.Graph());
            },
          )
          .build(),
    );
    route = LanternSingleResponderRoute.certified(
      client: client,
      responderId: 'same-node',
      isCurrent: () => true,
      continuesRetainedResponder: (_) => true,
    );
  }

  Future<OfflineLanternSession> open({
    LanternSingleResponderRoute? route,
    LanternChangeSource? incoming,
    bool transfer = true,
  }) async {
    final session = await OfflineLanternSession.openStore(
      client: client,
      storeKey: store,
      createStore: () async => store,
      closeStore: (_) async {},
      binding: binding,
      durability: AdmissionDurability.memory,
      clock: clock.now,
      schedule: clock.schedule,
      readRoute: route ?? this.route,
      incomingChanges: incoming,
      transferAllowed: transfer,
    );
    sessions.add(session);
    return session;
  }

  Future<void> close() async {
    for (final sub in subscriptions) {
      await sub.cancel();
    }
    for (final s in sessions) {
      await s.close();
    }
    await client.close();
    expect(clock.active, 0);
  }
}

// Alarm elapsed time is independent of wall time, just like Timer. Tests never
// change the host clock or sleep a physical device.
final class _Clock {
  DateTime wall = DateTime.utc(2026, 10, 15, 12);
  Duration elapsed = Duration.zero;
  final alarms = <_Alarm>[];
  int callbacks = 0;
  DateTime now() => wall;
  int get active => alarms.where((a) => !a.canceled).length;
  OfflineSessionAlarm schedule(Duration delay, void Function() callback) {
    final alarm = _Alarm(elapsed + delay, callback);
    alarms.add(alarm);
    expect(active, lessThanOrEqualTo(1));
    return alarm;
  }

  Future<void> tick(Duration by, {bool advanceWall = true}) async {
    for (var i = 0; i < 4; i++) {
      await Future<void>.delayed(Duration.zero);
    }
    elapsed += by;
    if (advanceWall) wall = wall.add(by);
    for (var turn = 0; turn < 100; turn++) {
      for (var i = 0; i < 4; i++) {
        await Future<void>.delayed(Duration.zero);
      }
      final due = alarms.where((a) => !a.canceled && a.at <= elapsed).toList();
      if (due.isEmpty) return;
      for (final a in due) {
        a.canceled = true;
        callbacks++;
        a.callback();
      }
    }
    fail('central alarm spun for 100 finite turns');
  }
}

final class _Alarm implements OfflineSessionAlarm {
  _Alarm(this.at, this.callback);
  final Duration at;
  final void Function() callback;
  bool canceled = false;
  @override
  void cancel() => canceled = true;
}
