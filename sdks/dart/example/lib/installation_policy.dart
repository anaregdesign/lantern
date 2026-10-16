import 'dart:async';
import 'dart:io';
import 'dart:math';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline_sqlite/lantern_client_offline_sqlite.dart';

import 'offline_session.dart' show offlineDatabaseFileName;

/// Integrators supply independent current-installation upgrade provenance.
/// Authentication, filenames and newly created markers are not such evidence.
typedef LegacyInstallationAssertion =
    Future<bool> Function(SqliteInstallationRequest request);

/// Fixed path/policy/adoption inputs, reused for this account's owner.
final class AppSqliteLocation {
  const AppSqliteLocation(this.path, this.policy, this.adoption);
  final String path;
  final SqliteInstallationPolicy policy;
  final SqliteScopeAdoption? adoption;
}

/// Android/iOS installation continuity, independent of the database marker.
/// There is no implicit desktop/web policy. Host fixtures inject their own.
final class MobileInstallationPolicy {
  MobileInstallationPolicy({this.assertLegacyInstallation});
  final LegacyInstallationAssertion? assertLegacyInstallation;
  static const channel = MethodChannel('lantern/offline_installation');
  static const _markerKey = 'lantern.offline.installation.v1';
  static const _secure = FlutterSecureStorage(
    iOptions: IOSOptions(
      accessibility: KeychainAccessibility.unlocked_this_device,
      synchronizable: false,
    ),
    aOptions: AndroidOptions(resetOnError: false, migrateWithBackup: false),
  );

  Future<AppSqliteLocation> resolve({
    required LanternSessionBinding binding,
    required String legacyPartition,
  }) async {
    if (kIsWeb ||
        (defaultTargetPlatform != TargetPlatform.android &&
            defaultTargetPlatform != TargetPlatform.iOS)) {
      throw const SqliteStartupException(SqliteStartupFailure.policyFailure);
    }
    final filename = offlineDatabaseFileName(
      binding.serviceOrigin,
      legacyPartition,
    );
    // Native creates only the protected directory here (needed for realpath and
    // the provisional lock). No DB, installation marker or migration is opened.
    final locations = await channel.invokeMapMethod<String, Object?>('paths', {
      'filename': filename,
    });
    final fresh = locations?['fresh'] as String?,
        legacy = locations?['legacy'] as String?;
    if (fresh == null || legacy == null || fresh == legacy) {
      throw const SqliteStartupException(SqliteStartupFailure.policyFailure);
    }
    final hasFresh = await _hasState(fresh),
        hasLegacy = await _hasState(legacy);
    if (hasFresh && hasLegacy) {
      throw const SqliteStartupException(SqliteStartupFailure.scopeConflict);
    }
    final selected = SqliteOfflineStore.canonicalAutomaticPath(
      hasLegacy ? legacy : fresh,
    );
    final policy = SqliteInstallationPolicy(
      identity: 'lantern-example-mobile-installation',
      version: 1,
      evaluate: (request) async {
        // Recheck both exact locations under the selected store's native guard.
        if (await _hasState(hasLegacy ? fresh : legacy)) {
          throw const SqliteStartupException(
            SqliteStartupFailure.scopeConflict,
          );
        }
        final asserted =
            request.hasSqliteState &&
            await (assertLegacyInstallation?.call(request) ??
                Future.value(false));
        final allowCreate = !request.hasSqliteState || asserted;
        final lease = await channel.invokeMapMethod<String, Object?>('begin', {
          'filename': filename,
        });
        final token = lease?['token'] as String?;
        if (token == null) {
          throw const SqliteStartupException(
            SqliteStartupFailure.policyFailure,
          );
        }
        SqliteInstallationEvidence? evidence;
        Object? failure;
        StackTrace? trace;
        try {
          final sentinel = lease?['installationId'] as String?;
          final secure = defaultTargetPlatform == TargetPlatform.iOS
              ? await _secure.read(key: _markerKey)
              : sentinel;
          if (sentinel != null && !_valid(sentinel) ||
              secure != null && !_valid(secure)) {
            evidence = const SqliteInstallationEvidence.held(
              SqliteRestoreReason.markerMalformed,
            );
          } else if (sentinel != null && secure != null && sentinel != secure) {
            evidence = const SqliteInstallationEvidence.held(
              SqliteRestoreReason.installationMismatch,
            );
          } else if ((sentinel == null || secure == null) && !allowCreate) {
            evidence = const SqliteInstallationEvidence.held(
              SqliteRestoreReason.installationMissing,
            );
          } else {
            final identity = sentinel ?? secure ?? _newIdentity();
            // The native installation lock serializes this compare/create across
            // account stores and isolates. Never rewrite an existing marker.
            if (defaultTargetPlatform == TargetPlatform.iOS && secure == null) {
              await _secure.write(key: _markerKey, value: identity);
              if (await _secure.read(key: _markerKey) != identity) {
                throw const SqliteStartupException(
                  SqliteStartupFailure.policyFailure,
                );
              }
            }
            await channel.invokeMethod<void>('commit', {
              'token': token,
              'installationId': identity,
            });
            evidence = SqliteInstallationEvidence.verified(
              installationId: identity,
              legacyAdoptionVerified: asserted,
            );
          }
        } catch (error, stack) {
          failure = error;
          trace = stack;
        }
        try {
          await channel.invokeMethod<void>('end', {'token': token});
        } catch (error, stack) {
          failure ??= error;
          trace ??= stack;
        }
        if (failure != null) Error.throwWithStackTrace(failure, trace!);
        return evidence!;
      },
    );
    return AppSqliteLocation(
      selected,
      policy,
      hasLegacy
          ? SqliteScopeAdoption.verified(
              canonicalPath: selected,
              sourcePartition: legacyPartition,
              destination: binding,
            )
          : null,
    );
  }

  static bool _valid(String value) => RegExp(r'^[0-9a-f]{64}$').hasMatch(value);
  static String _newIdentity() {
    final random = Random.secure();
    return List.generate(
      32,
      (_) => random.nextInt(256).toRadixString(16).padLeft(2, '0'),
    ).join();
  }

  static Future<bool> _hasState(String db) async {
    for (final suffix in ['', '-wal', '-shm', '-journal']) {
      if (await FileSystemEntity.type('$db$suffix', followLinks: false) !=
          FileSystemEntityType.notFound) {
        return true;
      }
    }
    return false;
  }
}
