import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart';

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
  'receiptIdentitySha256',
};
const _maxJournalBytes = 16 * 1024;
const _maxReceiptRun = Duration(hours: 4);
final _digestPattern = RegExp(r'^[0-9a-f]{64}$');
const _receiptMutations = <String>{
  'vertexPut',
  'vertexDelete',
  'edgeDelete',
  'edgeAdd',
};

typedef ReceiptHandoffIdentity = ({
  String logicalOperationId,
  String recordId,
  String mutation,
  List<int> receiptOperationId,
  List<int> receiptGroupId,
});

String receiptIdentitySha256(Iterable<ReceiptHandoffIdentity> identities) {
  final items = identities.toList();
  if (items.length != 4 ||
      items.any(
        (item) =>
            item.logicalOperationId.isEmpty ||
            item.recordId.isEmpty ||
            item.receiptOperationId.length != 49 ||
            item.receiptGroupId.length != 16,
      ) ||
      items.map((item) => item.logicalOperationId).toSet().length != 4 ||
      items.map((item) => item.recordId).toSet().length != 4 ||
      items
              .map((item) => base64Url.encode(item.receiptOperationId))
              .toSet()
              .length !=
          4 ||
      items
              .map((item) => base64Url.encode(item.receiptGroupId))
              .toSet()
              .length !=
          4 ||
      items
          .map((item) => item.mutation)
          .toSet()
          .difference(_receiptMutations)
          .isNotEmpty ||
      items.map((item) => item.mutation).toSet().length != 4) {
    throw StateError('Invalid receipt identity handoff');
  }
  items.sort(
    (left, right) =>
        left.logicalOperationId.compareTo(right.logicalOperationId),
  );
  return sha256
      .convert(
        utf8.encode(
          jsonEncode([
            for (final item in items)
              [
                item.logicalOperationId,
                item.recordId,
                item.mutation,
                base64Url.encode(item.receiptOperationId),
                base64Url.encode(item.receiptGroupId),
              ],
          ]),
        ),
      )
      .toString();
}

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
    required this.receiptIdentitySha256,
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
        !_digestPattern.hasMatch(receiptIdentitySha256) ||
        !_validNames(this.requiredScenarios) ||
        !_validNames(this.requiredCleanups) ||
        !_validNames(this.completedScenarios) ||
        this.requiredScenarios.isEmpty ||
        this.requiredCleanups.isEmpty ||
        this.completedScenarios.isEmpty ||
        this.completedScenarios.length >= this.requiredScenarios.length ||
        this.completedScenarios
            .toSet()
            .difference(this.requiredScenarios.toSet())
            .isNotEmpty) {
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
  final String receiptIdentitySha256;
  final List<String> requiredScenarios;
  final List<String> completedScenarios;
  final List<String> requiredCleanups;

  Map<String, Object> toJson() => {
    'schema': 3,
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
    'receiptIdentitySha256': receiptIdentitySha256,
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
        decoded['schema'] != 3 ||
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
        decoded['requiredCleanups'] is! List<dynamic> ||
        decoded['receiptIdentitySha256'] is! String) {
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
      receiptIdentitySha256: decoded['receiptIdentitySha256'] as String,
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
