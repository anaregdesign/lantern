//
//  Generated code. Do not modify.
//  source: graph/v1/changes.proto
//

import "package:connectrpc/connect.dart" as connect;
import "changes.pb.dart" as graphv1changes;
import "changes.connect.spec.dart" as specs;

/// Public CDC never exposes the private replication protocol. Every event is an
/// invalidation of an exact committed identity. Value mode requires explicit
/// identity/value CDC and data-read grants and additionally samples
/// the current local live value; it is not a causally ordered event-sourcing log.
extension type LanternChangeServiceClient (connect.Transport _transport) {
  Stream<graphv1changes.WatchChangesResponse> watchChanges(
    graphv1changes.WatchChangesRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).server(
      specs.LanternChangeService.watchChanges,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }
}
