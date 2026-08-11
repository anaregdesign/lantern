/// Experimental storage-neutral offline Repository support for Lantern.
///
/// The public surface is pure Dart. Applications own persistent-store adapters,
/// encryption, signed-in partitions, and foreground scheduling. Durable writes
/// are Put-only; legacy Add records are decoded solely for fail-closed terminal
/// migration and authorized inspection.
library;

export 'src/codec.dart';
export 'src/conformance.dart';
export 'src/errors.dart';
export 'src/memory_store.dart';
export 'src/remote.dart';
export 'src/repository.dart';
export 'src/store.dart';
export 'src/types.dart';
