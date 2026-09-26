import 'dart:convert';
import 'dart:io';

const _journalKind = 'physical_receipt_restart_journal';
const _journalFields = <String>{
  'schema',
  'kind',
  'phase',
  'testedCommit',
  'target',
  'runId',
  'platform',
  'packageId',
  'installedBinarySha256',
  'startedAt',
  'preparedAt',
  'firstPid',
  'requiredScenarios',
  'completedScenarios',
  'requiredCleanups',
};
const _maxJournalBytes = 16 * 1024;
const _maxReceiptRun = Duration(hours: 4);

/// Private, durable handoff between two launches of the same installed target.
final class ReceiptRestartJournal {
  ReceiptRestartJournal({
    required this.testedCommit,
    required this.target,
    required this.runId,
    required this.platform,
    required this.packageId,
    required this.installedBinarySha256,
    required this.startedAt,
    required this.preparedAt,
    required this.firstPid,
    required List<String> requiredScenarios,
    required List<String> completedScenarios,
    required List<String> requiredCleanups,
  }) : requiredScenarios = List.unmodifiable(requiredScenarios),
       completedScenarios = List.unmodifiable(completedScenarios),
       requiredCleanups = List.unmodifiable(requiredCleanups) {
    if (firstPid <= 0 ||
        startedAt.isUtc != true ||
        preparedAt.isUtc != true ||
        preparedAt.isBefore(startedAt) ||
        preparedAt.isAfter(startedAt.add(_maxReceiptRun)) ||
        !_validNames(this.requiredScenarios) ||
        !_validNames(this.requiredCleanups) ||
        !_validNames(this.completedScenarios) ||
        this.requiredScenarios.isEmpty ||
        this.requiredCleanups.isEmpty ||
        this.completedScenarios.isEmpty ||
        this.completedScenarios.length >= this.requiredScenarios.length ||
        !this.completedScenarios.toSet().difference(
          this.requiredScenarios.toSet(),
        ).isEmpty) {
      throw StateError('Invalid receipt restart handoff');
    }
  }

  final String testedCommit;
  final String target;
  final String runId;
  final String platform;
  final String packageId;
  final String installedBinarySha256;
  final DateTime startedAt;
  final DateTime preparedAt;
  final int firstPid;
  final List<String> requiredScenarios;
  final List<String> completedScenarios;
  final List<String> requiredCleanups;

  Map<String, Object> toJson() => {
    'schema': 2,
    'kind': _journalKind,
    'phase': 'awaiting_sigkill',
    'testedCommit': testedCommit,
    'target': target,
    'runId': runId,
    'platform': platform,
    'packageId': packageId,
    'installedBinarySha256': installedBinarySha256,
    'startedAt': startedAt.toIso8601String(),
    'preparedAt': preparedAt.toIso8601String(),
    'firstPid': firstPid,
    'requiredScenarios': requiredScenarios,
    'completedScenarios': completedScenarios,
    'requiredCleanups': requiredCleanups,
  };

  Future<void> write(File file) async {
    if (await file.exists()) {
      throw StateError('Receipt restart handoff already exists');
    }
    await file.parent.create(recursive: true);
    final temporary = File('${file.path}.tmp');
    if (await temporary.exists()) {
      throw StateError('Incomplete receipt restart handoff exists');
    }
    await temporary.writeAsString(jsonEncode(toJson()), flush: true);
    await temporary.rename(file.path);
  }

  static Future<ReceiptRestartJournal> read(File file) async {
    final length = await file.length();
    if (length == 0 || length > _maxJournalBytes) {
      throw StateError('Receipt restart handoff is empty or oversized');
    }
    final text = await file.readAsString();
    late final Object? decoded;
    try {
      decoded = jsonDecode(text);
    } on FormatException {
      throw StateError('Receipt restart handoff is malformed');
    }
    if (decoded is! Map<String, dynamic> ||
        decoded.keys.toSet().difference(_journalFields).isNotEmpty ||
        decoded.keys.length != _journalFields.length ||
        jsonEncode(decoded) != text ||
        decoded['schema'] != 2 ||
        decoded['kind'] != _journalKind ||
        decoded['phase'] != 'awaiting_sigkill' ||
        decoded['testedCommit'] is! String ||
        decoded['target'] is! String ||
        decoded['runId'] is! String ||
        decoded['platform'] is! String ||
        decoded['packageId'] is! String ||
        decoded['installedBinarySha256'] is! String ||
        decoded['firstPid'] is! int ||
        decoded['requiredScenarios'] is! List<dynamic> ||
        decoded['completedScenarios'] is! List<dynamic> ||
        decoded['requiredCleanups'] is! List<dynamic>) {
      throw StateError('Receipt restart handoff has invalid fields');
    }
    final startedAt = _readUtc(decoded['startedAt']);
    final preparedAt = _readUtc(decoded['preparedAt']);
    final scenarios = _readNames(decoded['requiredScenarios']);
    final completed = _readNames(decoded['completedScenarios']);
    final cleanups = _readNames(decoded['requiredCleanups']);
    return ReceiptRestartJournal(
      testedCommit: decoded['testedCommit'] as String,
      target: decoded['target'] as String,
      runId: decoded['runId'] as String,
      platform: decoded['platform'] as String,
      packageId: decoded['packageId'] as String,
      installedBinarySha256: decoded['installedBinarySha256'] as String,
      startedAt: startedAt,
      preparedAt: preparedAt,
      firstPid: decoded['firstPid'] as int,
      requiredScenarios: scenarios,
      completedScenarios: completed,
      requiredCleanups: cleanups,
    );
  }
}

bool _validNames(List<String> names) =>
    names.length == names.toSet().length &&
    names.every((name) => RegExp(r'^[a-z][a-z0-9_]*$').hasMatch(name)) &&
    names.join('\n') == (List<String>.of(names)..sort()).join('\n');

List<String> _readNames(Object? value) {
  if (value is! List<dynamic> || value.any((item) => item is! String)) {
    throw StateError('Receipt restart scenario or cleanup list is invalid');
  }
  return List<String>.from(value);
}

DateTime _readUtc(Object? value) {
  if (value is! String || !value.endsWith('Z')) {
    throw StateError('Receipt restart timestamp is invalid');
  }
  late final DateTime parsed;
  try {
    parsed = DateTime.parse(value);
  } on FormatException {
    throw StateError('Receipt restart timestamp is invalid');
  }
  if (!parsed.isUtc || parsed.toIso8601String() != value) {
    throw StateError('Receipt restart timestamp is not canonical UTC');
  }
  return parsed;
}
