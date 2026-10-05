import 'package:grpc/grpc.dart';
import 'package:lantern_grpc_transport_probe/probe.dart';

import '../../templates/probe_diagnostics_test_cases.dart';

void main() {
  diagnosticsTests(
    transport: 'grpc',
    errorCode: probeErrorCode,
    deadlineError: GrpcError.deadlineExceeded(
      'Bearer secret-token personal-data',
    ),
  );
}
