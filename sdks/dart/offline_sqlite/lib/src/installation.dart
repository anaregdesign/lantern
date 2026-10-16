// Copyright 2026 anaregdesign. Licensed under the Apache License, Version 2.0.
part of '../lantern_client_offline_sqlite.dart';

/// A startup result. A held store exposes no session or admission capability.
sealed class SqliteSessionStartup {
  const SqliteSessionStartup();
}

/// Installation and durable graph validation completed before runtime creation.
final class SqliteSessionReady extends SqliteSessionStartup {
  /// Wraps the successfully qualified owner handle.
  const SqliteSessionReady(this.session);

  /// Refcounted foreground handle; close it when its consumer retires.
  final OfflineLanternSession session;
}

/// Existing state is preserved without migration, maintenance or data traffic.
final class SqliteRestoreHeld extends SqliteSessionStartup {
  /// Describes a preserved store which did not enter automatic mode.
  const SqliteRestoreHeld(this.reason);

  /// Application-visible reason without private storage content.
  final SqliteRestoreReason reason;
}

/// Content-free reasons; no path, account, credential or payload is included.
enum SqliteRestoreReason {
  /// The application cannot establish current-installation continuity.
  continuityUnknown,

  /// An explicit copy or restore indication is present.
  restoreIndicated,

  /// Independently retained installation evidence is missing.
  installationMissing,

  /// Database and independent installation evidence disagree.
  installationMismatch,

  /// A previously adopted store lost its installation stamp.
  markerMissing,

  /// The durable installation stamp is malformed.
  markerMalformed,

  /// Markerless legacy data lacks trusted adoption provenance or exact mapping.
  legacyAssertionMissing,

  /// A zero-byte database or orphaned SQLite companion is present.
  incompleteStore,
}

/// Startup failures that require attention rather than a replacement database.
enum SqliteStartupFailure {
  /// The exact source, destination or prior adoption conflicts.
  scopeConflict,

  /// Platform exclusion or installation verification failed.
  policyFailure,
}

/// Content-free failure of qualified automatic startup.
final class SqliteStartupException implements Exception {
  /// Carries a stable failure category.
  const SqliteStartupException(this.reason);

  /// The failed startup boundary.
  final SqliteStartupFailure reason;
  @override
  String toString() => 'SqliteStartupException(${reason.name})';
}

/// Input to the application policy, while the canonical native guard is held.
final class SqliteInstallationRequest {
  const SqliteInstallationRequest._(
    this.path,
    this.binding,
    this.hasSqliteState,
  );

  /// Canonical reserved path; do not log this private application location.
  final String path;

  /// Verified destination identity for this immutable lifetime.
  final LanternSessionBinding binding;

  /// Includes the DB, WAL, SHM and rollback journal, even a zero-byte file.
  final bool hasSqliteState;
  @override
  String toString() => 'SqliteInstallationRequest(redacted)';
}

/// Application evidence, independent of the database's own marker.
///
/// `verified` asserts that backup/transfer exclusion has been established and
/// checked and that the independent installation signal has known continuity.
/// It does not assert arbitrary manual rollback detection. Policy/API errors
/// must throw; never turn them into a fresh installation.
final class SqliteInstallationEvidence {
  /// Asserts the exclusion and independent continuity contract above.
  SqliteInstallationEvidence.verified({
    required String installationId,
    this.legacyAdoptionVerified = false,
  }) : installationId = installationId,
       hold = null {
    _installationText(installationId);
  }

  /// Preserves uncertain state without creating any automatic handle.
  const SqliteInstallationEvidence.held(SqliteRestoreReason reason)
    : installationId = null,
      legacyAdoptionVerified = false,
      hold = reason;

  /// Bounded nonsecret independent installation signal, absent on hold.
  final String? installationId;

  /// Explicit trusted upgrade/current-installation provenance. Authentication,
  /// a filename, new UUID or newly enabled exclusion alone cannot establish it.
  final bool legacyAdoptionVerified;

  /// Reason to stop before SQLite write-open, if any.
  final SqliteRestoreReason? hold;
  @override
  String toString() => 'SqliteInstallationEvidence(redacted)';
}

/// Stable application-owned policy. Reuse this object for shared handles.
///
/// The callback runs under core reservation and native ownership, before SQLite
/// write-open. It may verify platform state but must not issue data/CDC/receipt
/// calls. Retire and join the live owner before changing an external restore
/// decision. Host tests must supply an explicit isolated fixture policy.
final class SqliteInstallationPolicy {
  /// Creates a stable policy, reused for matching owner handles.
  SqliteInstallationPolicy({
    required this.identity,
    required this.version,
    required this.evaluate,
  }) {
    _installationText(identity);
    if (version < 1 || version > 0x7fffffff) {
      throw const OfflineArgumentException();
    }
  }

  /// Bounded application-owned policy name, not a credential or account ID.
  final String identity;

  /// Explicit version of the installation/exclusion policy.
  final int version;

  /// Platform verification under the provisional owner, before write-open.
  final Future<SqliteInstallationEvidence> Function(SqliteInstallationRequest)
  evaluate;
}

/// Exact application-verified legacy mapping, never a second runtime identity.
///
/// Construct only after verifying the original endpoint/account/security-domain
/// mapping. Installation provenance is a separate assertion in the policy.
final class SqliteScopeAdoption {
  /// Asserts an exact verified identity mapping; confers no Server authority.
  SqliteScopeAdoption.verified({
    required this.canonicalPath,
    required this.sourcePartition,
    required LanternSessionBinding destination,
  }) : destinationPartition = destination.stableIdentity,
       authorityBinding = destination.authorityBinding {
    _validatePartition(sourcePartition);
    _validatePartition(destinationPartition);
    if (sourcePartition == destinationPartition ||
        canonicalPath != NativeOfflineOwner.canonicalPath(canonicalPath)) {
      throw const OfflineArgumentException();
    }
  }

  /// Exact reserved database; a permit never authorizes another file.
  final String canonicalPath;

  /// Original local identity; not derived from credential bytes or UID.
  final String sourcePartition;

  /// The sole stable identity used by the bound runtime after adoption.
  final String destinationPartition;

  /// Verified authority for binding preserved intent, with pause rules intact.
  final String authorityBinding;
  @override
  String toString() => 'SqliteScopeAdoption(redacted)';
}

void _installationText(String value) {
  if (value.isEmpty ||
      utf8.encode(value).length > 256 ||
      value.contains('\u0000')) {
    throw const OfflineArgumentException();
  }
}

final class _RestoreHold implements Exception {
  const _RestoreHold(this.reason);
  final SqliteRestoreReason reason;
}

// Tracks failed connection cleanup even if openDatabase/onUpgrade never returns
// a store. The native capability must remain held in that case.
final class _SqlOpenAttempt {
  bool cleanupFailed = false;
  Database? openingConnection;
  bool openReturned = false;
  Future<void> cleanup(
    Database db,
    Object? startup,
    StackTrace? startupTrace,
  ) async {
    try {
      await db.close();
    } catch (retirement, trace) {
      cleanupFailed = true;
      final safe = _safeDatabaseError(retirement);
      throw OfflineStartupRetirementException(
        startup == null ? safe : _safeDatabaseError(startup),
        startupTrace ?? trace,
        safe,
        trace,
      );
    }
  }
}

final class _SqlInitialization {
  _SqlInitialization(this.policy, this.binding, this.adoption, this.guard);
  final SqliteInstallationPolicy policy;
  final LanternSessionBinding binding;
  final SqliteScopeAdoption? adoption;
  final NativeOfflineOwner guard;
  late Map<String, Object?> marker;
  bool fresh = false;
  bool needsAdoption = false;
  bool initialized = false;

  Future<void> inspect(DatabaseFactory factory, _SqlOpenAttempt attempt) async {
    guard.check(requireDatabase: false);
    var present = false;
    for (final suffix in ['', '-wal', '-shm', '-journal']) {
      if (await FileSystemEntity.type('${guard.path}$suffix') !=
          FileSystemEntityType.notFound) {
        present = true;
      }
    }
    final SqliteInstallationEvidence evidence;
    try {
      evidence = await policy.evaluate(
        SqliteInstallationRequest._(guard.path, binding, present),
      );
    } on SqliteStartupException {
      rethrow;
    } catch (_) {
      throw const SqliteStartupException(SqliteStartupFailure.policyFailure);
    }
    guard.check(requireDatabase: false);
    if (evidence.hold case final reason?) throw _RestoreHold(reason);
    marker = {
      'id': 1,
      'format': 1,
      'installation_id': evidence.installationId!,
      'policy_id': policy.identity,
      'policy_version': policy.version,
    };
    final mapping = adoption;
    if (mapping != null &&
        (mapping.canonicalPath != guard.path ||
            mapping.destinationPartition != binding.stableIdentity ||
            mapping.authorityBinding != binding.authorityBinding)) {
      throw const SqliteStartupException(SqliteStartupFailure.scopeConflict);
    }
    if (!present) {
      // A requested adoption cannot silently create a replacement empty store.
      if (mapping != null) {
        throw const SqliteStartupException(SqliteStartupFailure.scopeConflict);
      }
      fresh = true;
      return;
    }
    final file = File(guard.path);
    if (!await file.exists() || await file.length() == 0) {
      throw const _RestoreHold(SqliteRestoreReason.incompleteStore);
    }
    final db = await factory.openDatabase(
      guard.path,
      options: OpenDatabaseOptions(readOnly: true, singleInstance: false),
    );
    Object? startup;
    StackTrace? startupTrace;
    try {
      final version = await db.getVersion();
      final id = (await db.rawQuery(
        'PRAGMA application_id',
      )).single.values.single;
      if (id != _applicationId || version < 1 || version > _schemaVersion) {
        throw const OfflineSchemaException();
      }
      final format = await db.query('store_metadata', limit: 2);
      if (format.length != 1 ||
          format.single['key'] != 'format' ||
          format.single['value'] != 'lantern-offline-$version') {
        throw const OfflineSchemaException();
      }
      if (version < 9) {
        needsAdoption = true;
      } else {
        final rows = await db.query('installation_binding', limit: 2);
        if (rows.isEmpty) {
          if ((await db.query('scope_adoptions', limit: 1)).isNotEmpty) {
            throw const _RestoreHold(SqliteRestoreReason.markerMissing);
          }
          needsAdoption = true;
        } else {
          if (rows.length != 1 || !_validInstallationMarker(rows.single)) {
            throw const _RestoreHold(SqliteRestoreReason.markerMalformed);
          }
          if (!_sameMarker(rows.single, marker)) {
            throw const _RestoreHold(SqliteRestoreReason.installationMismatch);
          }
        }
      }
      if (needsAdoption &&
          (!evidence.legacyAdoptionVerified || mapping == null)) {
        throw const _RestoreHold(SqliteRestoreReason.legacyAssertionMissing);
      }
      guard.check();
    } catch (error, stack) {
      startup = error;
      startupTrace = stack;
      rethrow;
    } finally {
      await attempt.cleanup(db, startup, startupTrace);
    }
  }

  // Called in onCreate/onUpgrade's transaction, or in one current-schema
  // transaction. Never executes a second time after an uncertain commit reply.
  Future<void> initialize(
    DatabaseExecutor sql,
    OfflineStoreLimits limits, {
    bool created = false,
  }) async {
    if (fresh && !created) {
      throw const SqliteStartupException(SqliteStartupFailure.scopeConflict);
    }
    guard.check(requireDatabase: false);
    final rows = await sql.query('installation_binding');
    if (rows.isEmpty) {
      if (!fresh && !needsAdoption) throw const OfflineSchemaException();
      await sql.insert('installation_binding', marker);
    } else if (rows.length != 1 || !_sameMarker(rows.single, marker)) {
      throw const SqliteStartupException(SqliteStartupFailure.scopeConflict);
    }
    final tx = _SqlTransaction(sql, limits);
    try {
      await tx.validateAll();
      if (adoption case final mapping?) {
        await _adoptScope(sql, tx, mapping, marker, limits);
      }
      await tx.validateAll();
      await _validateInstallation(sql);
      guard.check(requireDatabase: false);
      initialized = true;
    } finally {
      await tx.finish();
    }
  }
}

bool _validInstallationMarker(Map<String, Object?> row) {
  try {
    if (row['id'] != 1 ||
        row['format'] != 1 ||
        row['policy_version'] is! int ||
        (row['policy_version']! as int) < 1 ||
        (row['policy_version']! as int) > 0x7fffffff) {
      return false;
    }
    _installationText(row['installation_id']! as String);
    _installationText(row['policy_id']! as String);
    return true;
  } catch (_) {
    return false;
  }
}

bool _sameMarker(Map<String, Object?> a, Map<String, Object?> b) =>
    _validInstallationMarker(a) &&
    [
      'id',
      'format',
      'installation_id',
      'policy_id',
      'policy_version',
    ].every((key) => a[key] == b[key]);

Future<void> _validateInstallation(DatabaseExecutor sql) async {
  final rows = await sql.query('installation_binding', limit: 2);
  if (rows.length > 1 ||
      (rows.isNotEmpty && !_validInstallationMarker(rows.single))) {
    throw const OfflineSchemaException();
  }
  final adoption = await sql.rawQuery(
    '''SELECT a.*,p.partition_id FROM scope_adoptions a
    LEFT JOIN partitions p ON p.partition_id=a.destination_partition
    WHERE p.partition_id IS NULL OR a.format<>1 OR
      a.source_partition=a.destination_partition LIMIT 1''',
  );
  if (adoption.isNotEmpty) throw const OfflineSchemaException();
  final conflict = await sql.rawQuery(
    '''SELECT 1 FROM scope_adoptions a
    LEFT JOIN installation_binding i ON i.id=1
    WHERE i.id IS NULL OR a.installation_id<>i.installation_id OR
      a.policy_id<>i.policy_id OR a.policy_version<>i.policy_version LIMIT 1''',
  );
  if (conflict.isNotEmpty) throw const OfflineSchemaException();
}
