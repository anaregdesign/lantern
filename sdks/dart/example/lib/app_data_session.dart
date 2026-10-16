import 'dart:async';

import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline/lantern_client_offline_adapter.dart';
import 'package:lantern_client_offline_sqlite/lantern_client_offline_sqlite.dart';
import 'package:sqflite/sqflite.dart' show DatabaseFactory;

import 'installation_policy.dart';

enum AppDataProfile { direct, memory, durable, durableWithoutReadCache }

/// Startup failed and its owned data lifetime could not be retired safely.
/// Keep both failures for the account owner's replacement barrier.
final class AppDataStartupFailure implements Exception {
  const AppDataStartupFailure(
    this.startupError,
    this.startupTrace,
    this.retirementError,
    this.retirementTrace,
  );
  final Object startupError, retirementError;
  final StackTrace startupTrace, retirementTrace;
  @override
  String toString() => 'AppDataStartupFailure(retirementBlocked)';
}

/// Captured account generation, invalidated synchronously before any join.
final class AppDataLifetime {
  AppDataLifetime({this.current});
  final bool Function()? current;
  bool _live = true;
  bool get isCurrent => _live && (current?.call() ?? true);
  void invalidate() => _live = false;
  void check() {
    if (!isCurrent) throw const OfflineDisposedException();
  }
}

/// One stable composition per immutable account lifetime. Widgets share its
/// startup Future; they do not construct policy closures or replay schedulers.
final class AppDataSessionFactory {
  AppDataSessionFactory({
    required this.client,
    required this.binding,
    required this.profile,
    required this.legacyPartition,
    this.pinnedResponder = false,
    this.resolveLocation,
    this.databaseFactory,
    this.clock,
    this.schedule,
    this.changePrefix = '',
    AppDataLifetime? lifetime,
  }) : lifetime = lifetime ?? AppDataLifetime();
  final AppDataLifetime lifetime;
  final LanternClient client;
  final LanternSessionBinding binding;
  final AppDataProfile profile;
  final String legacyPartition;
  final bool pinnedResponder;
  final Future<AppSqliteLocation> Function()? resolveLocation;
  final DatabaseFactory? databaseFactory;
  final OfflineClock? clock;
  final OfflineSessionSchedule? schedule;
  final String changePrefix;
  Future<AppDataSession>? _opening;
  OfflineVerifiedCredentials? _credentials;
  bool get _live => lifetime.isCurrent;
  bool? _activateOnOpen;

  Future<AppDataSession> open({
    OfflineVerifiedCredentials? freshCredentials,
    bool activate = true,
  }) {
    if (!_live) return Future.error(const OfflineDisposedException());
    if (_opening != null &&
        (!identical(_credentials, freshCredentials) ||
            _activateOnOpen != activate)) {
      return Future.error(const OfflineOwnerConflictException());
    }
    _credentials = freshCredentials;
    _activateOnOpen = activate;
    return _opening ??= _open(freshCredentials, activate);
  }

  Future<AppDataSession> _open(
    OfflineVerifiedCredentials? credentials,
    bool activate,
  ) async {
    try {
      return await _openSession(credentials, activate);
    } on OfflineStartupRetirementException catch (error) {
      throw AppDataStartupFailure(
        error.startupError,
        error.startupTrace,
        error.retirementError,
        error.retirementTrace,
      );
    }
  }

  Future<AppDataSession> _openSession(
    OfflineVerifiedCredentials? credentials,
    bool activate,
  ) async {
    lifetime.check();
    if (profile == AppDataProfile.direct) {
      return AppDataSession._(
        DirectLanternDataClient(client, binding: binding),
        null,
        lifetime,
      );
    }
    final incoming = pinnedResponder
        ? LanternChangeSource(
            binding: binding,
            prefix: changePrefix,
            currentBinding: () => binding,
            route: LanternSingleResponderRoute.certified(
              client: client,
              responderId: binding.serviceOrigin.toString(),
              isCurrent: () => _live,
            ),
          )
        : null;
    OfflineLanternSession session;
    if (profile == AppDataProfile.memory) {
      session = await OfflineLanternSession.open(
        client: client,
        binding: binding,
        store: MemoryOfflineStore(),
        durability: AdmissionDurability.memory,
        transferAllowed: false,
        authorityCurrent: () => lifetime.isCurrent,
        clock: clock,
        schedule: schedule,
        incomingChanges: incoming,
      );
    } else {
      final location =
          await (resolveLocation?.call() ??
              MobileInstallationPolicy().resolve(
                binding: binding,
                legacyPartition: legacyPartition,
              ));
      lifetime.check();
      final result = await SqliteOfflineStore.openSession(
        path: location.path,
        client: client,
        binding: binding,
        installationPolicy: location.policy,
        adoption: location.adoption,
        databaseFactory: databaseFactory,
        clock: clock,
        schedule: schedule,
        cacheReads: profile != AppDataProfile.durableWithoutReadCache,
        transferAllowed: false,
        authorityCurrent: () => lifetime.isCurrent,
        incomingChanges: incoming,
      );
      if (result case SqliteRestoreHeld(:final reason)) {
        return AppDataSession._(null, reason, lifetime);
      }
      session = (result as SqliteSessionReady).session;
    }
    try {
      lifetime.check();
      if (credentials != null) {
        await session.acceptVerifiedCredentials(credentials);
      }
      lifetime.check();
      final result = AppDataSession._(session, null, lifetime);
      if (activate) await result.activate();
      return result;
    } catch (error, stack) {
      try {
        await session.close();
      } catch (retirementError, retirementTrace) {
        throw AppDataStartupFailure(
          error,
          stack,
          retirementError,
          retirementTrace,
        );
      }
      Error.throwWithStackTrace(error, stack);
    }
  }
}

/// Owns ready common data access or a held startup with no admission capability.
/// The account owner also closes its raw client (including held/failed starts).
final class AppDataSession {
  AppDataSession._(this.data, this.restoreHeld, this._lifetime);
  final LanternDataClient? data;
  final SqliteRestoreReason? restoreHeld;
  final AppDataLifetime _lifetime;
  Future<void> activate() async {
    _lifetime.check();
    if (data case final OfflineLanternSession session) {
      await session.setTransferAllowed(true);
    }
    _lifetime.check();
  }

  Future<void>? _closing;
  Future<void> _lifecycle = Future.value();
  final _retired = StreamController<void>.broadcast(sync: true);
  Stream<void> get retired => _retired.stream;
  bool get isClosed => _closing != null;
  Future<void> suspend() => _changeLifecycle(false);
  Future<void> resume() => _changeLifecycle(true);
  Future<void> _changeLifecycle(bool foreground) {
    if (isClosed) return _closing!;
    return _lifecycle = _lifecycle.then((_) async {
      if (isClosed) return;
      if (data case final OfflineLanternSession session) {
        if (foreground) {
          await session.resume();
        } else {
          await session.suspend();
        }
      }
    });
  }

  Future<void> close() {
    if (_closing != null) return _closing!;
    final completion = Completer<void>();
    _closing = completion.future;
    _lifetime.invalidate();
    _retired.add(null);
    unawaited(
      (() async {
        Object? failure;
        StackTrace? trace;
        try {
          await _lifecycle;
        } catch (error, stack) {
          failure = error;
          trace = stack;
        }
        try {
          await data?.close();
        } catch (error, stack) {
          failure ??= error;
          trace ??= stack;
        }
        unawaited(_retired.close());
        if (failure != null) Error.throwWithStackTrace(failure, trace!);
      })().then<void>(completion.complete, onError: completion.completeError),
    );
    return _closing!;
  }
}

/// Always join transport close, retaining the first (data retirement) failure.
Future<void> closeAccountData(
  AppDataSession? data,
  LanternClient client,
) async {
  Object? failure;
  StackTrace? trace;
  try {
    await data?.close();
  } catch (error, stack) {
    failure = error;
    trace = stack;
  }
  try {
    await client.close();
  } catch (error, stack) {
    failure ??= error;
    trace ??= stack;
  }
  if (failure != null) Error.throwWithStackTrace(failure, trace!);
}
