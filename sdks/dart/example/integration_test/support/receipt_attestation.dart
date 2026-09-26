import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:flutter/services.dart';

import 'receipt_restart_journal.dart';

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
    Set<String> requiredRestartCleanups = const {},
  }) => ReceiptAttestation(
    testedCommit: const String.fromEnvironment('LANTERN_TESTED_COMMIT'),
    target: target,
    runId: const String.fromEnvironment('LANTERN_RECEIPT_RUN_ID'),
    requiredScenarios: requiredScenarios,
    requiredRestartCleanups: requiredRestartCleanups,
  );

  ReceiptAttestation({
    required this.testedCommit,
    required this.target,
    required this.runId,
    required Set<String> requiredScenarios,
    Set<String> requiredRestartCleanups = const {},
    File? output,
    int Function()? processId,
  }) : _requiredScenarios = Set.unmodifiable(requiredScenarios),
       _requiredRestartCleanups = Set.unmodifiable(requiredRestartCleanups),
       _processId = processId ?? _currentPid,
       _output =
           output ??
           File('${Directory.systemTemp.path}/$receiptAttestationFileName') {
    if (!_matchesExactly(_commitPattern, testedCommit) ||
        !_matchesExactly(_targetPattern, target) ||
        !_matchesExactly(_runIdPattern, runId) ||
        _requiredScenarios.isEmpty ||
        _requiredScenarios.any(
          (scenario) => !_matchesExactly(_scenarioPattern, scenario),
        ) ||
        _requiredRestartCleanups.any(
          (name) => !_matchesExactly(_scenarioPattern, name),
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
  final Set<String> _requiredRestartCleanups;
  final int Function() _processId;
  final File _output;
  final List<String> _completedScenarios = [];
  final List<ReceiptCleanup> _cleanups = [];
  final Set<String> _registeredRestartCleanups = {};
  DateTime? _startedAt;
  DateTime? _preparedAt;
  DateTime? _resumedAt;
  InstalledReceiptBinary? _binary;
  String? _restartReceiptIdentitySha256;
  bool _running = false;

  String get restartReceiptIdentitySha256 {
    final digest = _restartReceiptIdentitySha256;
    if (!_running || _resumedAt == null || digest == null) {
      throw StateError('Verified receipt restart identity is unavailable');
    }
    return digest;
  }

  void registerCleanup(ReceiptCleanup cleanup, {String? restartObligation}) {
    if (!_running) throw StateError('Receipt test is not running');
    if (_resumedAt != null) {
      if (restartObligation == null ||
          !_requiredRestartCleanups.contains(restartObligation) ||
          !_registeredRestartCleanups.add(restartObligation)) {
        throw StateError('Invalid receipt restart cleanup obligation');
      }
    }
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
    await _write(
      status: 'running',
      phase: _resumedAt == null ? 'body' : 'verify',
    );
  }

  /// A pass is written only after every registered cleanup succeeds.
  Future<void> run(Future<void> Function(ReceiptAttestation) body) async {
    if (_startedAt != null) throw StateError('Receipt test already started');
    if (_requiredRestartCleanups.isNotEmpty) {
      throw StateError('Restart attestation requires prepare and resume');
    }
    _startedAt = DateTime.now().toUtc();
    if (await _output.exists()) await _output.delete();
    _binary = await readInstalledReceiptBinary();
    if (!_matchesExactly(_digestPattern, _binary!.sha256)) {
      throw StateError('Installed receipt binary digest is invalid');
    }
    await _write(status: 'running', phase: 'body');
    await _finishRun(body);
  }

  /// Writes a non-passing durable handoff while the app and SQLite stay open.
  Future<void> prepareForRestart(
    File journal,
    Future<String> Function(ReceiptAttestation) body,
  ) async {
    if (_startedAt != null || _requiredRestartCleanups.isEmpty) {
      throw StateError('Receipt restart preparation is invalid');
    }
    _startedAt = DateTime.now().toUtc();
    if (await _output.exists()) await _output.delete();
    if (await journal.exists() || await File('${journal.path}.tmp').exists()) {
      throw StateError('A receipt restart handoff already exists');
    }
    _binary = await readInstalledReceiptBinary();
    if (!_matchesExactly(_digestPattern, _binary!.sha256)) {
      throw StateError('Installed receipt binary digest is invalid');
    }
    await _write(status: 'running', phase: 'body');
    _running = true;
    final failures = <_RunFailure>[];
    try {
      final receiptIdentitySha256 = await body(this);
      if (_completedScenarios.isEmpty ||
          _completedScenarios.length == _requiredScenarios.length) {
        throw StateError('Receipt restart has no verified handoff');
      }
      _preparedAt = DateTime.now().toUtc();
      final handoff = ReceiptRestartJournal(
        testedCommit: testedCommit,
        target: target,
        runId: runId,
        platform: _binary!.platform,
        packageId: _binary!.packageId,
        installedBinarySha256: _binary!.sha256,
        startedAt: _startedAt!,
        preparedAt: _preparedAt!,
        firstPid: _processId(),
        receiptIdentitySha256: receiptIdentitySha256,
        requiredScenarios: _requiredScenarios.toList()..sort(),
        completedScenarios: _completedScenarios.toList()..sort(),
        requiredCleanups: _requiredRestartCleanups.toList()..sort(),
      );
      await handoff.write(journal);
      await _write(status: 'running', phase: 'awaiting_sigkill');
    } catch (error, stack) {
      failures.add((phase: 'body', error: error, stack: stack));
    }
    _running = false;
    if (failures.isEmpty) return;
    for (final cleanup in _cleanups.reversed) {
      try {
        await cleanup();
      } catch (error, stack) {
        failures.add((phase: 'cleanup', error: error, stack: stack));
      }
    }
    await _write(
      status: 'failed',
      phase: failures.any((failure) => failure.phase == 'cleanup')
          ? 'cleanup'
          : 'body',
      finishedAt: DateTime.now().toUtc(),
      failureType: failures.any((failure) => failure.phase == 'cleanup')
          ? 'cleanup'
          : 'body',
    );
    _throwFailures(failures);
  }

  /// Requires a real second process before continuing the prepared run.
  Future<void> resumeAfterRestart(
    File journal,
    Future<void> Function(ReceiptAttestation) body,
  ) async {
    if (_startedAt != null || _requiredRestartCleanups.isEmpty) {
      throw StateError('Receipt restart verification is invalid');
    }
    if (!await _output.exists()) {
      throw StateError('Receipt restart prepare marker is missing');
    }
    final markerBytes = await _output.readAsBytes();
    await _output.delete();
    if (markerBytes.isEmpty || markerBytes.length > 64 * 1024) {
      throw StateError('Receipt restart prepare marker is invalid');
    }
    late final Object? decodedMarker;
    try {
      decodedMarker = jsonDecode(utf8.decode(markerBytes));
    } on FormatException {
      throw StateError('Receipt restart prepare marker is malformed');
    }
    final markerText = utf8.decode(markerBytes);
    final handoff = await ReceiptRestartJournal.read(journal);
    if (decodedMarker is! Map<String, dynamic> ||
        jsonEncode(decodedMarker) != markerText ||
        decodedMarker.keys.toSet().difference({
          'schema',
          'kind',
          'contentFree',
          'testedCommit',
          'target',
          'runId',
          'platform',
          'packageId',
          'installedBinarySha256',
          'startedAt',
          'completedScenarios',
          'status',
          'phase',
        }).isNotEmpty ||
        decodedMarker.length != 13 ||
        decodedMarker['schema'] != 1 ||
        decodedMarker['kind'] != 'physical_receipt_attestation' ||
        decodedMarker['contentFree'] is! bool ||
        decodedMarker['contentFree'] != true ||
        decodedMarker['status'] != 'running' ||
        decodedMarker['phase'] != 'awaiting_sigkill' ||
        decodedMarker['testedCommit'] != testedCommit ||
        decodedMarker['target'] != target ||
        decodedMarker['runId'] != runId ||
        decodedMarker['platform'] != handoff.platform ||
        decodedMarker['packageId'] != handoff.packageId ||
        decodedMarker['installedBinarySha256'] !=
            handoff.installedBinarySha256 ||
        decodedMarker['startedAt'] != handoff.startedAt.toIso8601String() ||
        decodedMarker['completedScenarios'] is! List<dynamic> ||
        (decodedMarker['completedScenarios'] as List<dynamic>).length !=
            handoff.completedScenarios.length ||
        (decodedMarker['completedScenarios'] as List<dynamic>)
            .toSet()
            .difference(handoff.completedScenarios.toSet())
            .isNotEmpty ||
        handoff.testedCommit != testedCommit ||
        handoff.target != target ||
        handoff.runId != runId ||
        handoff.requiredScenarios
            .toSet()
            .difference(_requiredScenarios)
            .isNotEmpty ||
        handoff.requiredScenarios.length != _requiredScenarios.length ||
        handoff.requiredCleanups
            .toSet()
            .difference(_requiredRestartCleanups)
            .isNotEmpty ||
        handoff.requiredCleanups.length != _requiredRestartCleanups.length ||
        handoff.firstPid == _processId()) {
      throw StateError('Receipt restart identity or phase is invalid');
    }
    final installed = await readInstalledReceiptBinary();
    if (installed.platform != handoff.platform ||
        installed.packageId != handoff.packageId ||
        installed.sha256 != handoff.installedBinarySha256) {
      throw StateError('Installed receipt binary changed across restart');
    }
    final resumedAt = DateTime.now().toUtc();
    if (resumedAt.isBefore(handoff.preparedAt) ||
        resumedAt.isAfter(handoff.startedAt.add(const Duration(hours: 4)))) {
      throw StateError('Receipt restart is outside the run window');
    }
    _startedAt = handoff.startedAt;
    _preparedAt = handoff.preparedAt;
    _resumedAt = resumedAt;
    _binary = installed;
    _restartReceiptIdentitySha256 = handoff.receiptIdentitySha256;
    _completedScenarios.addAll(handoff.completedScenarios);
    await _write(status: 'running', phase: 'verify');
    await _finishRun(body);
  }

  Future<void> _finishRun(
    Future<void> Function(ReceiptAttestation) body,
  ) async {
    _running = true;
    final failures = <_RunFailure>[];
    try {
      await body(this);
    } catch (error, stack) {
      failures.add((phase: 'body', error: error, stack: stack));
    }
    _running = false;
    if (_resumedAt != null &&
        _registeredRestartCleanups.length != _requiredRestartCleanups.length) {
      failures.add((
        phase: 'incomplete',
        error: StateError('Required restart cleanups were not registered'),
        stack: StackTrace.current,
      ));
    }
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
    _throwFailures(failures);
  }

  void _throwFailures(List<_RunFailure> failures) {
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
      'schema': _resumedAt == null ? 1 : 2,
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
    if (_resumedAt != null) {
      fields['restart'] = <String, Object>{
        'preparedAt': _preparedAt!.toIso8601String(),
        'resumedAt': _resumedAt!.toIso8601String(),
        'processChanged': true,
      };
    }
    if (finishedAt != null) {
      fields['finishedAt'] = finishedAt.toIso8601String();
    }
    if (failureType != null) fields['failureType'] = failureType;
    final temporary = File('${_output.path}.tmp');
    await temporary.writeAsString(jsonEncode(fields), flush: true);
    await temporary.rename(_output.path);
  }
}

int _currentPid() => pid;
