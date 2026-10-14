// Copyright 2026 anaregdesign. Licensed under the Apache License, Version 2.0.
import 'dart:ffi';

import 'package:ffi/ffi.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';

@Native<Int Function(Pointer<Utf8>, Pointer<Utf8>, Int)>(
  symbol: 'lantern_owner_resolve',
)
external int _resolve(Pointer<Utf8> path, Pointer<Utf8> output, int size);

@Native<Int Function(Pointer<Utf8>, Pointer<Uint64>)>(
  symbol: 'lantern_owner_acquire',
)
external int _acquire(Pointer<Utf8> path, Pointer<Uint64> handle);

@Native<Int Function(Uint64, Int)>(symbol: 'lantern_owner_check')
external int _checkNative(int handle, int requireDb);

@Native<Int Function(Uint64)>(symbol: 'lantern_owner_release')
external int _release(int handle);

/// Internal platform guard. It deliberately has no finalizer: losing the Dart
/// owner keeps the native guard held until this process exits. No FD is exposed.
final class NativeOfflineOwner {
  NativeOfflineOwner._(this.path, this._handle);

  /// Canonical path used for both SQLite and the sidecar.
  final String path;
  final int _handle;
  bool _active = true;

  /// Validates and resolves an application-owned local DB path.
  static String canonicalPath(String path) => using((arena) {
    if (path.isEmpty || path.length >= 4096 || path.contains('\u0000')) {
      throw const OfflineArgumentException();
    }
    final output = arena<Uint8>(4096).cast<Utf8>();
    _check(_resolve(path.toNativeUtf8(allocator: arena), output, 4096));
    return output.toDartString();
  });

  /// Reserves one native open-file-description lock.
  static NativeOfflineOwner acquire(String path) => using((arena) {
    final canonical = canonicalPath(path);
    final handle = arena<Uint64>();
    _check(_acquire(canonical.toNativeUtf8(allocator: arena), handle));
    return NativeOfflineOwner._(canonical, handle.value);
  });

  /// Checks file identities without opening or closing the database.
  void check({bool requireDatabase = true}) {
    if (!_active) throw const OfflineDisposedException();
    _check(_checkNative(_handle, requireDatabase ? 1 : 0));
  }

  /// Invalidates the guard before one native close; safe to repeat.
  void release() {
    if (!_active) return;
    _active = false;
    _check(_release(_handle));
  }

  static void _check(int result) {
    switch (result) {
      case 0:
        return;
      case 1:
        throw const OfflineArgumentException();
      case 2:
        throw const OfflineOwnerConflictException();
      case 3:
        throw const OfflineCapacityException();
      case 5:
        throw const OfflineDisposedException();
      default:
        throw const OfflineOwnerStorageException();
    }
  }
}
