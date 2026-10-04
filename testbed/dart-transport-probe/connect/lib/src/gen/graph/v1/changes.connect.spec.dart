//
//  Generated code. Do not modify.
//  source: graph/v1/changes.proto
//

import "package:connectrpc/connect.dart" as connect;
import "changes.pb.dart" as graphv1changes;

/// Public CDC never exposes the private replication protocol. Every event is an
/// invalidation of an exact committed identity. Value mode requires explicit
/// identity/value CDC and data-read grants and additionally samples
/// the current local live value; it is not a causally ordered event-sourcing log.
abstract final class LanternChangeService {
  /// Fully-qualified name of the LanternChangeService service.
  static const name = 'graph.v1.LanternChangeService';

  static const watchChanges = connect.Spec(
    '/$name/WatchChanges',
    connect.StreamType.server,
    graphv1changes.WatchChangesRequest.new,
    graphv1changes.WatchChangesResponse.new,
  );
}
