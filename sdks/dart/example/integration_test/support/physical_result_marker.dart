import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:connectrpc/connect.dart' as connect;
import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';

/// Content-free result that CoreDevice can retrieve when VM-service discovery
/// cannot report an integration test's outcome.
class PhysicalResultMarker {
  PhysicalResultMarker(String fileName, {required this.kind})
    : _startedAt = DateTime.now().toUtc(),
      _file = File('${Directory.systemTemp.path}/$fileName');

  final DateTime _startedAt;
  final File _file;
  final String kind;
  String phase = 'setup';
  String? cleanupFailureType;

  void addTrackedTearDown(FutureOr<dynamic> Function() cleanup) {
    addTearDown(() async {
      try {
        await cleanup();
      } catch (error) {
        cleanupFailureType ??= safePhysicalFailureType(error);
        rethrow;
      }
    });
  }

  Future<void> recordPhase(String nextPhase) =>
      recordOutcome('running', nextPhase);

  Future<void> recordOutcome(
    String status,
    String nextPhase, {
    String? failureType,
  }) async {
    phase = nextPhase;
    final fields = <String, Object>{
      'schema': 1,
      'kind': kind,
      'contentFree': true,
      'status': status,
      'phase': phase,
      'startedAt': _startedAt.toIso8601String(),
      'updatedAt': DateTime.now().toUtc().toIso8601String(),
    };
    if (failureType != null) fields['failureType'] = failureType;
    final encoded = jsonEncode(fields);
    final temporaryFile = File('${_file.path}.tmp');
    await temporaryFile.writeAsString(encoded, flush: true);
    await temporaryFile.rename(_file.path);
  }
}

String safePhysicalFailureType(Object error) => switch (error) {
  TestFailure() || AssertionError() => 'assertion',
  TimeoutException() => 'timeout',
  SocketException() || HandshakeException() => 'network',
  OfflineRemoteFailure() || connect.ConnectException() => 'remote',
  FileSystemException() => 'filesystem',
  FormatException() || TypeError() => 'format',
  StateError() => 'state',
  _ => 'other',
};
