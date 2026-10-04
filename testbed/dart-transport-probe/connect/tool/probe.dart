import 'dart:convert';
import 'dart:io';

import 'package:lantern_connect_transport_probe/probe.dart';

Future<void> main(List<String> args) async {
  if (args.isEmpty) {
    stderr.writeln(
      'usage: dart run tool/probe.dart <base-url> [private-token-file]',
    );
    exitCode = 64;
    return;
  }
  final result = await runProbe(
    Uri.parse(args[0]),
    token: args.length > 1 ? File(args[1]).readAsStringSync().trim() : null,
    caPath: Platform.environment['LANTERN_PROBE_CA_CERT'],
  );
  stdout.writeln(jsonEncode(result));
}
