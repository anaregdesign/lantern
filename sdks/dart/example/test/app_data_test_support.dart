import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/protobuf.dart' as codec;
import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline_adapter.dart';
import 'package:lantern_client_offline_sqlite/lantern_client_offline_sqlite.dart';
import 'package:lantern_example/app_data_session.dart';
import 'package:lantern_example/installation_policy.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

final class AppClock {
  DateTime now = DateTime.utc(2026, 10, 15);
  final alarms = <AppAlarm>[];
  DateTime call() => now;
  OfflineSessionAlarm schedule(Duration delay, void Function() callback) {
    final value = AppAlarm(now.add(delay), callback);
    alarms.add(value);
    if (alarms.where((a) => !a.canceled).length > 1) {
      throw StateError('More than one runtime alarm');
    }
    return value;
  }

  Future<void> advance(Duration duration) async {
    now = now.add(duration);
    for (var turn = 0; turn < 100; turn++) {
      final due = alarms
          .where((a) => !a.canceled && !a.at.isAfter(now))
          .toList();
      for (final alarm in due) {
        alarm.canceled = true;
        alarm.callback();
      }
      for (var flush = 0; flush < 8; flush++) {
        await Future<void>.delayed(Duration.zero);
      }
      alarms.removeWhere((a) => a.canceled);
      if (!alarms.any((a) => !a.at.isAfter(now))) return;
    }
    fail('Unbounded runtime wakeup');
  }

  Future<T> settle<T>(Future<T> work) async {
    var done = false;
    T? result;
    Object? error;
    StackTrace? trace;
    work.then(
      (v) {
        result = v;
        done = true;
      },
      onError: (Object e, StackTrace s) {
        error = e;
        trace = s;
        done = true;
      },
    );
    for (var i = 0; i < 200 && !done; i++) {
      await advance(const Duration(milliseconds: 25));
    }
    if (!done) fail('Finite common consumer did not settle');
    if (error != null) Error.throwWithStackTrace(error!, trace!);
    return result as T;
  }
}

final class AppAlarm implements OfflineSessionAlarm {
  AppAlarm(this.at, this.callback);
  final DateTime at;
  final void Function() callback;
  bool canceled = false;
  @override
  void cancel() => canceled = true;
}

final class AppHarness {
  AppHarness(this.profile);
  final AppDataProfile profile;
  final clock = AppClock();
  late final wire = AppWire(clock);
  late final client = LanternClient.connect(
    Uri.parse('https://example.test'),
    transport: wire,
    clock: clock.call,
    onClose: () async {
      closes++;
    },
  );
  late final binding = LanternSessionBinding.application(
    serviceOrigin: client.endpoint,
    securityDomain: 'app-host-fixture',
    accountIdentity: 'fixture',
    authorityBinding: 'current',
  );
  Directory? directory;
  late AppDataSessionFactory factory;
  late AppDataSession owner;
  LanternDataClient get data => owner.data!;
  var closes = 0, policies = 0;
  Future<void> open() async {
    sqfliteFfiInit();
    directory = await Directory.systemTemp.createTemp('lantern-common-app-');
    final policy = SqliteInstallationPolicy(
      identity: 'isolated-app-host-fixture',
      version: 1,
      evaluate: (_) async {
        policies++;
        return SqliteInstallationEvidence.verified(
          installationId: 'host-fixture',
        );
      },
    );
    factory = AppDataSessionFactory(
      client: client,
      binding: binding,
      profile: profile,
      legacyPartition: 'fixture',
      clock: clock.call,
      schedule: clock.schedule,
      databaseFactory: databaseFactoryFfiNoIsolate,
      resolveLocation: () async =>
          AppSqliteLocation('${directory!.path}/data.db', policy, null),
    );
    final first = factory.open();
    expect(identical(first, factory.open()), true);
    owner = await first;
  }

  Future<void> close() async {
    await closeAccountData(owner, client);
    await directory?.delete(recursive: true);
  }
}

/// Public codec/transport fixture; no generated SDK imports or alternate engine.
final class AppWire implements connect.Transport {
  AppWire(this.clock);
  final AppClock clock;
  final vertices = <String, Map<String, Object?>>{};
  final edges = <String, Map<String, Object?>>{};
  final calls = <String>[];
  final bodies = <(String, Map<String, dynamic>)>[];
  bool available = true;
  int blocked = 0;
  Completer<void>? holdRead;
  String _edge(Map<String, dynamic> value) =>
      '${value['tail']}\u0000${value['head']}';
  @override
  Future<connect.UnaryResponse<I, O>> unary<I extends Object, O extends Object>(
    connect.Spec<I, O> spec,
    I input, [
    connect.CallOptions? options,
  ]) async {
    final method = spec.procedure.split('/').last;
    calls.add(method);
    if (!available) {
      blocked++;
      throw connect.ConnectException(connect.Code.unavailable, 'host outage');
    }
    const json = codec.JsonCodec();
    final request =
        jsonDecode(utf8.decode(json.encode(input))) as Map<String, dynamic>;
    bodies.add((method, request));
    Map<String, Object?> response;
    switch (method) {
      case 'GetReceiptCapability':
        String bytes(int count, int fill) =>
            base64Encode(List.filled(count, fill));
        response = {
          'enabled': true,
          'serverNowUnixMs': '${clock.now.millisecondsSinceEpoch}',
          'policy': {
            'deploymentEpoch': bytes(16, 1),
            'fingerprint': bytes(32, 2),
            'retentionMs': '86400000',
            'maxEntries': '1000',
            'maxBytes': '1000000',
          },
          'endpoint': {'nodeId': bytes(16, 3), 'generation': bytes(16, 4)},
          'supportedMutations': [1, 2, 3, 4, 5],
        };
      case 'PutVertices':
        final outcomes = <int>[];
        for (final raw in request['vertices'] as List) {
          final value = Map<String, dynamic>.from(raw as Map),
              key = value['key'] as String;
          if (request['ifAbsent'] == true && vertices.containsKey(key)) {
            outcomes.add(4);
          } else {
            vertices[key] = value;
            outcomes.add(1);
          }
        }
        response = {'outcomes': outcomes};
      case 'GetVertices':
        // Capture before a held old reply; returning it must still be fenced.
        response = {
          'vertices': [
            for (final key in request['keys'] as List)
              if (vertices.containsKey(key)) vertices[key],
          ],
          'missing': [
            for (final key in request['keys'] as List)
              if (!vertices.containsKey(key)) key,
          ],
        };
        await holdRead?.future;
      case 'DeleteVertices':
        final existed = [
          for (final key in request['keys'] as List)
            vertices.remove(key) != null,
        ];
        response = {
          'deleted': existed.where((v) => v).length,
          'existed': existed,
        };
      case 'PutEdges':
        for (final raw in request['edges'] as List) {
          final value = Map<String, dynamic>.from(raw as Map);
          edges[_edge(value)] = value;
        }
        response = {
          'outcomes': [for (final _ in request['edges'] as List) 1],
        };
      case 'AddEdges':
        final weights = <double>[];
        for (final raw in request['edges'] as List) {
          final value = Map<String, dynamic>.from(raw as Map),
              key = _edge(value);
          final weight =
              ((edges[key]?['weight'] as num?) ?? 0).toDouble() +
              (value['weight'] as num).toDouble();
          edges[key] = {...value, 'weight': weight};
          weights.add(weight);
        }
        response = {'written': weights.length, 'effectiveWeights': weights};
      case 'GetEdges':
        final refs = request['edges'] as List;
        response = {
          'edges': [
            for (final raw in refs)
              if (edges.containsKey(
                _edge(Map<String, dynamic>.from(raw as Map)),
              ))
                edges[_edge(Map<String, dynamic>.from(raw))],
          ],
          'missing': [
            for (final raw in refs)
              if (!edges.containsKey(
                _edge(Map<String, dynamic>.from(raw as Map)),
              ))
                raw,
          ],
        };
      case 'Illuminate':
        response = {
          'graph': {
            'vertices': vertices.values.toList(),
            'edges': edges.values.toList(),
          },
        };
      case 'GetReceiptStatuses':
        response = {
          'statuses': [
            for (final id in request['operationIds'] as List)
              {'operationId': id, 'state': 2},
          ],
        };
      default:
        throw StateError('Unexpected app fixture RPC $method');
    }
    final output = spec.outputFactory();
    json.decode(Uint8List.fromList(utf8.encode(jsonEncode(response))), output);
    return connect.UnaryResponse(
      spec,
      connect.Headers(),
      output,
      connect.Headers(),
    );
  }

  @override
  Future<connect.StreamResponse<I, O>>
  stream<I extends Object, O extends Object>(
    connect.Spec<I, O> spec,
    Stream<I> input, [
    connect.CallOptions? options,
  ]) => throw UnimplementedError();
}
