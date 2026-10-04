//
//  Generated code. Do not modify.
//  source: graph/v1/security_peer.proto
//

import "package:connectrpc/connect.dart" as connect;
import "security_peer.pb.dart" as graphv1security_peer;
import "security_peer.connect.spec.dart" as specs;

/// Private mTLS workload plane only. This service is never mounted on the
/// public data/browser listener, including in OFF mode.
extension type LanternSecurityPeerServiceClient (connect.Transport _transport) {
  Future<graphv1security_peer.RenewPolicyLeaseResponse> renewPolicyLease(
    graphv1security_peer.RenewPolicyLeaseRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternSecurityPeerService.renewPolicyLease,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }
}
