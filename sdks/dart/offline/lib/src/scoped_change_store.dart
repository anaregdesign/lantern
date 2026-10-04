import 'dart:convert';
import 'dart:typed_data';

import 'change_store.dart';
import 'errors.dart';
import 'types.dart';

/// Maximum bytes in one opaque public CDC cursor.
const offlineMaxScopedCursorBytes = 8192;

/// Maximum retained opaque cursors across one store.
const offlineMaxScopedCursorsPerStore = 4096;

/// Server-issued checkpoint bytes, without origin or authorization semantics.
final class OfflineScopedChangeCursor {
  /// Copies a nonempty, bounded opaque checkpoint.
  OfflineScopedChangeCursor(List<int> bytes)
    : _bytes = Uint8List.fromList(bytes) {
    if (bytes.isEmpty ||
        bytes.length > offlineMaxScopedCursorBytes ||
        bytes.any((byte) => byte < 0 || byte > 255)) {
      throw const OfflineArgumentException();
    }
  }

  final Uint8List _bytes;

  /// Returns a defensive copy for an application-owned durable store.
  Uint8List toBytes() => Uint8List.fromList(_bytes);

  /// Canonical snapshot encoding; this never decodes the Server contents.
  String toJson() => base64Encode(_bytes);

  /// Restores only a canonical, bounded encoding.
  factory OfflineScopedChangeCursor.fromJson(Object? value) {
    try {
      if (value is! String || value.length > 10924) {
        throw const OfflineCodecException();
      }
      final bytes = base64Decode(value);
      if (base64Encode(bytes) != value) throw const OfflineCodecException();
      return OfflineScopedChangeCursor(bytes);
    } on Object {
      throw const OfflineCodecException();
    }
  }

  @override
  String toString() => 'OfflineScopedChangeCursor(<opaque>)';
}

/// One bounded, value-free public identity frame.
///
/// Apply every key in the same transaction before installing [cursor]. A null
/// cursor is an intermediate frame and must preserve the prior durable cursor.
final class OfflineScopedChangeFrame {
  /// Validates an immutable identity projection without credentials or values.
  OfflineScopedChangeFrame({
    Iterable<OfflineEntityKey> keys = const [],
    this.cursor,
    this.bootstrap = false,
  }) : keys = List<OfflineEntityKey>.unmodifiable(keys) {
    if (this.keys.length > offlineMaxChangeInvalidations ||
        this.keys.fold<int>(
              0,
              (sum, key) => sum + utf8.encode(key.canonical).length,
            ) >
            1 << 20) {
      throw const OfflineCapacityException();
    }
    if ((this.keys.isEmpty && cursor == null) ||
        bootstrap && (this.keys.isNotEmpty || cursor == null) ||
        this.keys.any(
          (key) => key.kind == OfflineEntityKind.vertex
              ? key.vertexKey!.isEmpty
              : key.tail!.isEmpty || key.head!.isEmpty,
        )) {
      throw const OfflineArgumentException();
    }
  }

  /// Exact logical resource identities; no raw origin/sequence or payload.
  final List<OfflineEntityKey> keys;

  /// Optional completed checkpoint, atomically committed after invalidation.
  final OfflineScopedChangeCursor? cursor;

  /// The first publication cut of an explicitly requested bootstrap tail.
  final bool bootstrap;
}
