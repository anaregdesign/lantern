import 'package:connectrpc/connect.dart';
import 'package:lantern_connect_transport_probe/probe.dart';

import '../../templates/probe_diagnostics_test_cases.dart';

void main() {
  diagnosticsTests(
    transport: 'connect',
    errorCode: probeErrorCode,
    deadlineError: ConnectException(
      Code.deadlineExceeded,
      'Bearer secret-token personal-data',
    ),
  );
}
