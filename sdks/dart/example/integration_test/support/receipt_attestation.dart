import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:flutter/services.dart';

const receiptAttestationFileName = 'lantern-receipt-attestation.json';
const installedReceiptBinaryChannel = MethodChannel(
  'lantern/physical_receipt_attestation',
);

final _commitPattern = RegExp(r'^[0-9a-f]{40}$');
final _runIdPattern = RegExp(r'^[0-9a-f]{32}$');
final _targetPattern = RegExp(r'^integration_test/[a-z][a-z0-9_]*_test\.dart$');
final _scenarioPattern = RegExp(r'^[a-z][a-z0-9_]*$');
final _digestPattern = RegExp(r'^[0-9a-f]{64}$');

bool _matchesExactly(RegExp pattern, String value) =>
    pattern.firstMatch(value)?.group(0) == value;

class InstalledReceiptBinary {
  const InstalledReceiptBinary({
    required this.platform,
    required this.packageId,
    required this.sha256,
  });

  final String platform;
  final String packageId;
  final String sha256;
}

/// Hashes the app's installed APK or installed iOS Dart AOT executable.
Future<InstalledReceiptBinary> readInstalledReceiptBinary() async {
  final response = await installedReceiptBinaryChannel
      .invokeMapMethod<String, String>('installedBinary');
  if (response == null ||
      response.length != 3 ||
      !response.keys.toSet().containsAll({'platform', 'packageId', 'path'})) {
    throw StateError('Installed receipt binary identity is unavailable');
  }
  final platform = response['platform']!;
  final packageId = response['packageId']!;
  final path = response['path']!;
  final expectedPackage = switch (platform) {
    'android' => 'com.anaregdesign.lantern_example',
    'ios' => 'com.anaregdesign.lanternExample',
    _ => throw StateError('Unsupported installed receipt platform'),
  };
  if (packageId != expectedPackage ||
      !path.startsWith('/') ||
      (platform == 'android' && !path.endsWith('.apk')) ||
      (platform == 'ios' && !path.endsWith('/Frameworks/App.framework/App'))) {
    throw StateError('Installed receipt binary identity is invalid');
  }
  final binary = File(path);
  if (await binary.length() == 0) {
    throw StateError('Installed receipt binary is empty');
  }
  final digest = (await sha256.bind(binary.openRead()).first).toString();
  return InstalledReceiptBinary(
    platform: platform,
    packageId: packageId,
    sha256: digest,
  );
}

typedef ReceiptCleanup = FutureOr<void> Function();
typedef _RunFailure = ({String phase, Object error, StackTrace stack});

class ReceiptAttestationFailure implements Exception {
  ReceiptAttestationFailure(this.phases, this.errors);

  final List<String> phases;
  final List<Object> errors;

  @override
  String toString() => 'Receipt attestation failed during ${phases.join(', ')}';
}

/// Only an actual receipt target should invoke this runner; it is not a test.
class ReceiptAttestation {
  factory ReceiptAttestation.fromBuild({
    required String target,
    required Set<String> requiredScenarios,
  }) => ReceiptAttestation(
    testedCommit: const String.fromEnvironment('LANTERN_TESTED_COMMIT'),
    target: target,
    runId: const String.fromEnvironment('LANTERN_RECEIPT_RUN_ID'),
    requiredScenarios: requiredScenarios,
  );

  ReceiptAttestation({
    required this.testedCommit,
    required this.target,
    required this.runId,
    required Set<String> requiredScenarios,
    File? output,
  }) : _requiredScenarios = Set.unmodifiable(requiredScenarios),
       _output =
           output ??
           File('${Directory.systemTemp.path}/$receiptAttestationFileName') {
    if (!_matchesExactly(_commitPattern, testedCommit) ||
        !_matchesExactly(_targetPattern, target) ||
        !_matchesExactly(_runIdPattern, runId) ||
        _requiredScenarios.isEmpty ||
        _requiredScenarios.any(
          (scenario) => !_matchesExactly(_scenarioPattern, scenario),
        )) {
      throw ArgumentError(
        'Receipt attestation identity or scenario set is invalid',
      );
    }
  }

  final String testedCommit;
  final String target;
  final String runId;
  final Set<String> _requiredScenarios;
  final File _output;
  final List<String> _completedScenarios = [];
  final List<ReceiptCleanup> _cleanups = [];
  DateTime? _startedAt;
  InstalledReceiptBinary? _binary;
  bool _running = false;

  void registerCleanup(ReceiptCleanup cleanup) {
    if (!_running) throw StateError('Receipt test is not running');
    _cleanups.add(cleanup);
  }

  /// Records completion only after every assertion in [verify] succeeds.
  Future<void> verifyScenario(
    String scenario,
    Future<void> Function() verify,
  ) async {
    if (!_running) throw StateError('Receipt test is not running');
    if (!_requiredScenarios.contains(scenario)) {
      throw ArgumentError.value(
        scenario,
        'scenario',
        'Not a required scenario',
      );
    }
    if (_completedScenarios.contains(scenario)) {
      throw StateError('Receipt scenario was already completed');
    }
    await verify();
    if (!_running || _completedScenarios.contains(scenario)) {
      throw StateError('Receipt scenario finished outside its test body');
    }
    _completedScenarios.add(scenario);
    await _write(status: 'running', phase: 'body');
  }

  /// A pass is written only after every registered cleanup succeeds.
  Future<void> run(Future<void> Function(ReceiptAttestation) body) async {
    if (_startedAt != null) throw StateError('Receipt test already started');
    _startedAt = DateTime.now().toUtc();
    if (await _output.exists()) await _output.delete();
    _binary = await readInstalledReceiptBinary();
    if (!_matchesExactly(_digestPattern, _binary!.sha256)) {
      throw StateError('Installed receipt binary digest is invalid');
    }
    await _write(status: 'running', phase: 'body');
    _running = true;
    final failures = <_RunFailure>[];
    try {
      await body(this);
    } catch (error, stack) {
      failures.add((phase: 'body', error: error, stack: stack));
    }
    _running = false;
    for (final cleanup in _cleanups.reversed) {
      try {
        await cleanup();
      } catch (error, stack) {
        failures.add((phase: 'cleanup', error: error, stack: stack));
      }
    }
    try {
      final installed = await readInstalledReceiptBinary();
      final initial = _binary!;
      if (installed.platform != initial.platform ||
          installed.packageId != initial.packageId ||
          installed.sha256 != initial.sha256) {
        throw StateError('Installed receipt binary changed during test');
      }
    } catch (error, stack) {
      failures.add((phase: 'attestation', error: error, stack: stack));
    }
    if (failures.isEmpty &&
        _completedScenarios.length != _requiredScenarios.length) {
      failures.add((
        phase: 'incomplete',
        error: StateError('Required receipt scenarios did not complete'),
        stack: StackTrace.current,
      ));
    }
    final finishedAt = DateTime.now().toUtc();
    if (finishedAt.isBefore(_startedAt!)) {
      failures.add((
        phase: 'attestation',
        error: StateError('Device UTC clock moved backwards'),
        stack: StackTrace.current,
      ));
    }
    final failureType = failures.any((failure) => failure.phase == 'cleanup')
        ? 'cleanup'
        : failures.isEmpty
        ? null
        : failures.first.phase;
    try {
      await _write(
        status: failures.isEmpty ? 'passed' : 'failed',
        phase: failureType ?? 'complete',
        finishedAt: finishedAt,
        failureType: failureType,
      );
    } catch (error, stack) {
      failures.add((phase: 'attestation', error: error, stack: stack));
    }
    if (failures.length == 1) {
      Error.throwWithStackTrace(failures.single.error, failures.single.stack);
    }
    if (failures.isNotEmpty) {
      Error.throwWithStackTrace(
        ReceiptAttestationFailure(
          [for (final failure in failures) failure.phase],
          [for (final failure in failures) failure.error],
        ),
        failures.first.stack,
      );
    }
  }

  Future<void> _write({
    required String status,
    required String phase,
    DateTime? finishedAt,
    String? failureType,
  }) async {
    final binary = _binary!;
    final fields = <String, Object>{
      'schema': 1,
      'kind': 'physical_receipt_attestation',
      'contentFree': true,
      'testedCommit': testedCommit,
      'target': target,
      'runId': runId,
      'platform': binary.platform,
      'packageId': binary.packageId,
      'installedBinarySha256': binary.sha256,
      'startedAt': _startedAt!.toIso8601String(),
      'completedScenarios': List<String>.of(_completedScenarios),
      'status': status,
      'phase': phase,
    };
    if (finishedAt != null) {
      fields['finishedAt'] = finishedAt.toIso8601String();
    }
    if (failureType != null) fields['failureType'] = failureType;
    final temporary = File('${_output.path}.tmp');
    await temporary.writeAsString(jsonEncode(fields), flush: true);
    await temporary.rename(_output.path);
  }
}
