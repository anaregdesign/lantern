import 'dart:convert';

import 'errors.dart';
import 'types.dart';

/// Maximum origins retained in one local CDC cursor.
const offlineMaxChangeOrigins = 128;

/// Maximum origin-progress records across every partition of one store.
const offlineMaxChangeOriginsPerStore = 4096;

/// Maximum identities and prefixes in one projected CDC chunk.
const offlineMaxChangeInvalidations = 1024;

/// Maximum resident identities returned by one recovery scan or read batch.
const offlineMaxResidentRevalidationBatch = 128;

final _maxSequence = (BigInt.one << 64) - BigInt.one;
final _originPattern = RegExp(r'^[0-9a-f]{32}$');

/// A portable, immutable vector of fully applied origin sequences.
///
/// These are last-applied sequences, not HLCs. A future Subscribe consumer
/// resumes at each sequence plus one, rejecting uint64 exhaustion explicitly.
/// An absent origin has not been observed. This local metadata grants no access
/// to a server graph and contains no credentials.
final class OfflineChangeCursor {
  /// Copies and validates the complete last-applied vector.
  OfflineChangeCursor(Map<String, BigInt> sequences)
    : sequences = Map<String, BigInt>.unmodifiable(sequences) {
    if (sequences.length > offlineMaxChangeOrigins) {
      throw const OfflineCapacityException();
    }
    for (final entry in sequences.entries) {
      _validateOrigin(entry.key);
      _validateSequence(entry.value);
    }
  }

  /// Last fully applied sequence per lower-case, 16-byte origin hex ID.
  final Map<String, BigInt> sequences;

  /// Encodes uint64 values losslessly as decimal strings in sorted key order.
  Map<String, String> toJson() {
    final origins = sequences.keys.toList()..sort();
    return {for (final origin in origins) origin: sequences[origin].toString()};
  }

  /// Decodes canonical cursor metadata, failing closed on malformed values.
  factory OfflineChangeCursor.fromJson(Object? value) {
    try {
      if (value is! Map<String, Object?>) {
        throw const OfflineCodecException();
      }
      return OfflineChangeCursor({
        for (final entry in value.entries)
          entry.key: _decodeSequence(entry.value),
      });
    } on OfflineException {
      throw const OfflineCodecException();
    }
  }
}

/// One bounded identity-only chunk of a projected explicit mutation.
///
/// Chunk indexes start at zero. The cursor advances only after [isLast].
/// Vertex prefixes match literal key prefixes, never SQL wildcard patterns.
/// Pending writes are not canceled or confirmed by invalidation.
final class OfflineChangeChunk {
  /// Validates one chunk without copying any graph values or credentials.
  OfflineChangeChunk({
    required this.origin,
    required this.sequence,
    required this.chunkIndex,
    required this.isLast,
    Iterable<OfflineEntityKey> keys = const [],
    Iterable<String> vertexPrefixes = const [],
  }) : keys = List<OfflineEntityKey>.unmodifiable(keys),
       vertexPrefixes = List<String>.unmodifiable(vertexPrefixes) {
    _validateOrigin(origin);
    _validateSequence(sequence);
    if (sequence == BigInt.zero || chunkIndex < 0 || chunkIndex > 65535) {
      throw const OfflineArgumentException();
    }
    if (this.keys.length + this.vertexPrefixes.length >
        offlineMaxChangeInvalidations) {
      throw const OfflineCapacityException();
    }
    final bytes =
        this.keys.fold<int>(
          0,
          (sum, key) => sum + utf8.encode(key.canonical).length,
        ) +
        this.vertexPrefixes.fold<int>(
          0,
          (sum, prefix) => sum + utf8.encode(prefix).length,
        );
    if (bytes > 1024 * 1024) throw const OfflineCapacityException();
  }

  /// Lower-case hex encoding of the mutation origin's 16-byte node ID.
  final String origin;

  /// Original uint64 mutation sequence, represented without signed truncation.
  final BigInt sequence;

  /// Zero-based chunk index within this mutation.
  final int chunkIndex;

  /// Whether this chunk completes the projected mutation.
  final bool isLast;

  /// Exact vertex or edge identities to invalidate.
  final List<OfflineEntityKey> keys;

  /// Literal vertex-key prefixes whose resident cache entries are invalidated.
  final List<String> vertexPrefixes;
}

/// Durable assembly progress for one origin's projected mutation.
///
/// Adapters persist this alongside cache invalidation in the same transaction.
/// Keeping progress permits reopen between chunks without advancing the public
/// cursor prematurely or accepting an orphan final chunk.
final class OfflineChangeProgress {
  /// Creates validated progress; zero is the initial last-applied sequence.
  OfflineChangeProgress({
    required this.completedSequence,
    this.pendingSequence,
    this.nextChunk = 0,
  }) {
    _validateSequence(completedSequence);
    final pending = pendingSequence;
    if (pending == null) {
      if (nextChunk != 0) throw const OfflineArgumentException();
    } else {
      _validateSequence(pending);
      if (pending <= completedSequence || nextChunk < 1 || nextChunk > 65535) {
        throw const OfflineArgumentException();
      }
    }
  }

  /// Last fully applied mutation sequence.
  final BigInt completedSequence;

  /// Mutation currently awaiting additional chunks, if any.
  final BigInt? pendingSequence;

  /// Next expected chunk index for [pendingSequence].
  final int nextChunk;

  /// Returns new progress, or null for an already committed duplicate chunk.
  ///
  /// Missing or interleaved chunks fail closed. Sequence gaps themselves belong
  /// to the Subscribe checkpoint/gap protocol, since projection filters may
  /// omit unrelated mutations. This method never establishes cache freshness.
  OfflineChangeProgress? accept(OfflineChangeChunk chunk) {
    if (chunk.sequence <= completedSequence) return null;
    if (pendingSequence != null) {
      if (chunk.sequence != pendingSequence) {
        throw const OfflineChangeGapException();
      }
      if (chunk.chunkIndex < nextChunk) return null;
      if (chunk.chunkIndex != nextChunk) {
        throw const OfflineChangeGapException();
      }
    } else if (chunk.chunkIndex != 0) {
      throw const OfflineChangeGapException();
    }
    return chunk.isLast
        ? OfflineChangeProgress(completedSequence: chunk.sequence)
        : OfflineChangeProgress(
            completedSequence: completedSequence,
            pendingSequence: chunk.sequence,
            nextChunk: chunk.chunkIndex + 1,
          );
  }

  /// Canonical metadata suitable for snapshot and database codecs.
  Map<String, Object?> toJson() => {
    'completed': completedSequence.toString(),
    'pending': pendingSequence?.toString(),
    'nextChunk': nextChunk,
  };

  /// Restores only canonical, internally consistent progress.
  factory OfflineChangeProgress.fromJson(Object? value) {
    try {
      if (value is! Map<String, Object?> ||
          value.length != 3 ||
          !value.keys.toSet().containsAll({
            'completed',
            'pending',
            'nextChunk',
          }) ||
          value['nextChunk'] is! int) {
        throw const OfflineCodecException();
      }
      return OfflineChangeProgress(
        completedSequence: _decodeSequence(value['completed']),
        pendingSequence: value['pending'] == null
            ? null
            : _decodeSequence(value['pending']),
        nextChunk: value['nextChunk']! as int,
      );
    } on OfflineException {
      throw const OfflineCodecException();
    }
  }
}

void _validateOrigin(String origin) {
  if (!_originPattern.hasMatch(origin)) throw const OfflineArgumentException();
}

void _validateSequence(BigInt sequence) {
  if (sequence < BigInt.zero || sequence > _maxSequence) {
    throw const OfflineArgumentException();
  }
}

BigInt _decodeSequence(Object? value) {
  if (value is! String || !RegExp(r'^(0|[1-9][0-9]{0,19})$').hasMatch(value)) {
    throw const OfflineCodecException();
  }
  final result = BigInt.parse(value);
  _validateSequence(result);
  return result;
}
