//
//  Generated code. Do not modify.
//  source: graph/v1/security_peer.proto
//

import "package:connectrpc/connect.dart" as connect;
import "security_peer.pb.dart" as graphv1security_peer;

/// Private mTLS workload plane only. This service is never mounted on the
/// public data/browser listener, including in OFF mode.
abstract final class LanternSecurityPeerService {
  /// Fully-qualified name of the LanternSecurityPeerService service.
  static const name = 'graph.v1.LanternSecurityPeerService';

  static const renewPolicyLease = connect.Spec(
    '/$name/RenewPolicyLease',
    connect.StreamType.unary,
    graphv1security_peer.RenewPolicyLeaseRequest.new,
    graphv1security_peer.RenewPolicyLeaseResponse.new,
  );
}
