import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'claim_probe.dart';

const _scenarios = [
  'schema',
  'enqueue',
  'claim',
  'confirmation',
  'cursor-chunk',
  'cursor-final',
  'checkpoint-reset',
  'wipe',
];
const _timeout = Duration(seconds: 30);
const _receiptTimeout = Duration(minutes: 2);
const _receiptMutations = <String>[
  'PutVertices',
  'DeleteVertices',
  'DeleteEdges',
  'AddEdges',
];

/// Runs real SIGKILL/reopen boundaries and prints only aggregate case counts.
Future<void> main(List<String> arguments) async {
  if (Platform.isWindows) {
    stderr.writeln('crash_probe_requires_posix_sigkill');
    exitCode = 1;
    return;
  }
  if (arguments.length == 1 && arguments.single == '--receipt') {
    await _runReceiptCrash();
    return;
  }
  if (arguments.isNotEmpty) {
    stderr.writeln('crash_probe_invalid_arguments');
    exitCode = 1;
    return;
  }
  final directory = await Directory.systemTemp.createTemp(
    'lantern-sqlite-crash-',
  );
  var verified = 0;
  var stage = 'start';
  try {
    for (final scenario in _scenarios) {
      for (final boundary in ['before', 'after']) {
        final path = '${directory.path}/$scenario-$boundary.db';
        stage = 'crash';
        await _crash(scenario, boundary, path);
        stage = 'verify';
        await _verify(scenario, boundary, path);
        verified++;
      }
    }
    stage = 'cross_process_claim';
    final claims = await runClaimProbe();
    stdout.writeln(
      jsonEncode({
        'scenarios': _scenarios.length,
        'sigkill': verified,
        'fresh_process_verified': verified,
        'cross_process_claim': claims,
      }),
    );
  } catch (_) {
    stderr.writeln('crash_probe_failed_after_$verified:$stage');
    exitCode = 1;
  } finally {
    await directory.delete(recursive: true);
  }
}

Future<void> _runReceiptCrash() async {
  final value = Platform.environment['LANTERN_DART_RECEIPT_ENDPOINT'];
  final token = Platform.environment['LANTERN_DART_RECEIPT_TOKEN'];
  final endpoint = value == null ? null : Uri.tryParse(value);
  if (endpoint == null ||
      endpoint.scheme != 'http' ||
      !['127.0.0.1', 'localhost', '::1'].contains(endpoint.host) ||
      token == null ||
      token.isEmpty) {
    stderr.writeln('receipt_crash_probe_requires_local_auth_wal_fixture');
    exitCode = 1;
    return;
  }

  final directory = await Directory.systemTemp.createTemp(
    'lantern-sqlite-receipt-crash-',
  );
  _ResponseDroppingProxy? proxy;
  var stage = 'start';
  try {
    proxy = await _ResponseDroppingProxy.bind(
      endpoint,
      drops: {for (final rpc in _receiptMutations) rpc: 1},
    );
    final path = '${directory.path}/receipt.db';
    stage = 'response_loss_sigkill';
    await _crash(
      'receipt',
      'after',
      path,
      proxyEndpoint: proxy.endpoint,
      timeout: _receiptTimeout,
    );
    _requireReceiptCounts(proxy);
    final statusesBeforeReopen = proxy.forwarded('GetReceiptStatuses');
    if (statusesBeforeReopen < _receiptMutations.length) {
      throw StateError('initial_status_first');
    }
    var precedingStatuses = 0;
    var observedMutations = 0;
    for (final rpc in proxy.requestTrace) {
      if (rpc == 'GetReceiptStatuses') precedingStatuses++;
      if (_receiptMutations.contains(rpc)) {
        if (precedingStatuses <= observedMutations) {
          throw StateError('initial_status_order');
        }
        observedMutations++;
      }
    }
    if (observedMutations != _receiptMutations.length) {
      throw StateError('initial_mutation_count');
    }

    stage = 'sqlite_reopen_status_first';
    await _verify(
      'receipt',
      'after',
      path,
      proxyEndpoint: proxy.endpoint,
      timeout: _receiptTimeout,
    );
    _requireReceiptCounts(proxy);
    if (proxy.forwarded('GetReceiptStatuses') <
        statusesBeforeReopen + _receiptMutations.length) {
      throw StateError('reopened_status_first');
    }
    stdout.writeln(
      jsonEncode({
        'receipt_scenarios': 1,
        'sigkill': 1,
        'fresh_process_verified': 1,
        'response_drops': _receiptMutations.length,
        'status_first_reconciled': _receiptMutations.length,
      }),
    );
  } on Object catch (error) {
    stderr.writeln('receipt_crash_probe_failed:$stage:${error.runtimeType}');
    exitCode = 1;
  } finally {
    if (proxy != null) await proxy.close();
    await directory.delete(recursive: true);
  }
}

void _requireReceiptCounts(_ResponseDroppingProxy proxy) {
  if (proxy.forwardFailures != 0) throw StateError('proxy_transport_failure');
  for (final rpc in _receiptMutations) {
    if (proxy.forwarded(rpc) != 1 || proxy.dropped(rpc) != 1) {
      throw StateError('mutation_resent_or_not_committed');
    }
  }
}

Future<Process> _start(
  String mode,
  String scenario,
  String boundary,
  String path, {
  Uri? proxyEndpoint,
}) => Process.start(
  Platform.resolvedExecutable,
  [
    'run',
    File.fromUri(Platform.script.resolve('crash_worker.dart')).path,
    mode,
    scenario,
    boundary,
    path,
  ],
  environment: proxyEndpoint == null
      ? null
      : {'LANTERN_DART_RECEIPT_PROXY_ENDPOINT': proxyEndpoint.toString()},
);

Future<void> _crash(
  String scenario,
  String boundary,
  String path, {
  Uri? proxyEndpoint,
  Duration timeout = _timeout,
}) async {
  final process = await _start(
    'crash',
    scenario,
    boundary,
    path,
    proxyEndpoint: proxyEndpoint,
  );
  final ready = Completer<int>();
  final stderrDrained = process.stderr.drain<void>();
  final subscription = process.stdout
      .transform(utf8.decoder)
      .transform(const LineSplitter())
      .listen(
        (line) {
          try {
            final message = jsonDecode(line) as Map<String, Object?>;
            if (message['event'] != 'ready' || ready.isCompleted) {
              throw StateError('marker');
            }
            ready.complete(message['pid']! as int);
          } catch (_) {
            if (!ready.isCompleted) ready.completeError(StateError('marker'));
          }
        },
        onDone: () {
          if (!ready.isCompleted) ready.completeError(StateError('early_exit'));
        },
      );
  int? workerPid;
  var killed = false;
  var exited = false;
  try {
    workerPid = await ready.future.timeout(timeout);
    if (!Process.killPid(workerPid, ProcessSignal.sigkill)) {
      throw StateError('sigkill');
    }
    killed = true;
    final result = await process.exitCode.timeout(timeout);
    exited = true;
    if (result == 0) throw StateError('normal_exit');
    await stderrDrained;
  } finally {
    if (!killed && workerPid != null && workerPid != process.pid) {
      Process.killPid(workerPid, ProcessSignal.sigkill);
    }
    if (!exited) {
      process.kill(ProcessSignal.sigkill);
      await process.exitCode.timeout(timeout);
    }
    await subscription.cancel();
  }
}

Future<void> _verify(
  String scenario,
  String boundary,
  String path, {
  Uri? proxyEndpoint,
  Duration timeout = _timeout,
}) async {
  final process = await _start(
    'verify',
    scenario,
    boundary,
    path,
    proxyEndpoint: proxyEndpoint,
  );
  var exited = false;
  final output = process.stdout.transform(utf8.decoder).join();
  final stderrDrained = process.stderr.drain<void>();
  try {
    final result = await process.exitCode.timeout(timeout);
    exited = true;
    await stderrDrained;
    final message = jsonDecode(await output) as Map<String, Object?>;
    if (result != 0 || message['event'] != 'verified') {
      throw StateError('verification');
    }
  } finally {
    if (!exited) {
      process.kill(ProcessSignal.sigkill);
      await process.exitCode.timeout(timeout);
    }
  }
}

/// Drops selected responses only after the real server completes each call.
final class _ResponseDroppingProxy {
  _ResponseDroppingProxy._(
    this._server,
    this._upstreamEndpoint,
    Map<String, int> drops,
  ) : _remainingDrops = Map<String, int>.of(drops) {
    _upstream.autoUncompress = false;
    _server.listen((request) => unawaited(_forward(request)));
  }

  static Future<_ResponseDroppingProxy> bind(
    Uri upstreamEndpoint, {
    required Map<String, int> drops,
  }) async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    return _ResponseDroppingProxy._(server, upstreamEndpoint, drops);
  }

  final HttpServer _server;
  final Uri _upstreamEndpoint;
  final HttpClient _upstream = HttpClient();
  final Map<String, int> _remainingDrops;
  final Map<String, int> _forwarded = <String, int>{};
  final Map<String, int> _dropped = <String, int>{};
  final List<String> _requestTrace = <String>[];
  int _forwardFailures = 0;

  Uri get endpoint =>
      Uri(scheme: 'http', host: _server.address.host, port: _server.port);

  int forwarded(String rpc) => _forwarded[rpc] ?? 0;

  int dropped(String rpc) => _dropped[rpc] ?? 0;

  int get forwardFailures => _forwardFailures;

  List<String> get requestTrace => List<String>.unmodifiable(_requestTrace);

  Future<void> close() async {
    await _server.close(force: true);
    _upstream.close(force: true);
  }

  Future<void> _forward(HttpRequest downstream) async {
    final rpc = downstream.uri.pathSegments.isEmpty
        ? ''
        : downstream.uri.pathSegments.last;
    _forwarded[rpc] = (_forwarded[rpc] ?? 0) + 1;
    _requestTrace.add(rpc);
    try {
      final upstreamRequest = await _upstream.openUrl(
        downstream.method,
        _upstreamEndpoint.resolveUri(downstream.uri),
      );
      _copyHeaders(downstream.headers, upstreamRequest.headers);
      await upstreamRequest.addStream(downstream);
      final upstreamResponse = await upstreamRequest.close();
      final responseBytes = await upstreamResponse.fold<BytesBuilder>(
        BytesBuilder(copy: false),
        (builder, bytes) => builder..add(bytes),
      );

      final remaining = _remainingDrops[rpc] ?? 0;
      if (remaining > 0) {
        _remainingDrops[rpc] = remaining - 1;
        _dropped[rpc] = (_dropped[rpc] ?? 0) + 1;
        final socket = await downstream.response.detachSocket(
          writeHeaders: false,
        );
        socket.destroy();
        return;
      }
      downstream.response.statusCode = upstreamResponse.statusCode;
      _copyHeaders(upstreamResponse.headers, downstream.response.headers);
      downstream.response.add(responseBytes.takeBytes());
      await downstream.response.close();
    } on Object {
      _forwardFailures++;
      try {
        downstream.response.statusCode = HttpStatus.badGateway;
        await downstream.response.close();
      } on Object {
        // A detached client socket cannot receive the failure response.
      }
    }
  }

  static void _copyHeaders(HttpHeaders source, HttpHeaders target) {
    const ignored = <String>{
      'connection',
      'content-length',
      'host',
      'keep-alive',
      'proxy-authenticate',
      'proxy-authorization',
      'te',
      'trailer',
      'transfer-encoding',
      'upgrade',
    };
    source.forEach((name, values) {
      if (!ignored.contains(name.toLowerCase())) {
        target.set(name, values);
      }
    });
  }
}
