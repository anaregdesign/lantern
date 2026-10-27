import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'claim_probe.dart';
import 'claim_protocol.dart';
import 'probe_protocol.dart';

const _scenarios = [
  'schema',
  'enqueue',
  'claim',
  'confirmation',
  'cursor-chunk',
  'cursor-final',
  'checkpoint-reset',
  'scoped-partial',
  'scoped-final',
  'scoped-reset',
  'wipe',
];
const _timeout = Duration(seconds: 30);
const _receiptTimeout = Duration(minutes: 2);
const _receiptMutations = <String>[
  'PutVertices',
  'DeleteVertices',
  'DeleteEdges',
  'AddEdges',
  'DeleteEdgeContributions',
];

/// Runs real SIGKILL/reopen boundaries with bounded content-free observations.
Future<void> main(List<String> arguments) async {
  if (Platform.isWindows) {
    stderr.writeln('crash_probe_requires_posix_sigkill');
    exitCode = 1;
    return;
  }
  if (arguments.length == 1 && arguments.single == '--receipt') {
    await _runReceiptCrash();
    if (exitCode == 0) await _runBoundReceiptCrash();
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
  } on Object catch (error) {
    stderr.writeln(
      'crash_probe_failed_after_$verified:$stage:${_failureCode(error)}',
    );
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
      endpoint.scheme != 'https' ||
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
    stderr.writeln('receipt_crash_probe_failed:$stage:${_failureCode(error)}');
    if (proxy != null) {
      for (final rpc in _receiptMutations) {
        final rejected = proxy.rejected(rpc);
        if (rejected > 0) {
          stderr.writeln(
            'receipt_upstream_rejected:$rpc:$rejected:${proxy.rejectedStatus(rpc)}',
          );
        }
      }
    }
    exitCode = 1;
  } finally {
    if (proxy != null) await proxy.close();
    await directory.delete(recursive: true);
  }
}

// Only fixture-owned fixed categories cross the public diagnostic boundary.
// An arbitrary exception message may contain credentials or record contents.
String _failureCode(Object error) {
  final claim = claimFailureCode(error);
  if (claim != null) return claim;
  if (error is TimeoutException) return 'timeout';
  if (error is StateError && isProbeFailureCode(error.message)) {
    return error.message;
  }
  if (error is StateError &&
      const {
        'initial_status_first',
        'initial_status_order',
        'initial_mutation_count',
        'reopened_status_first',
        'bound_initial_status_query',
        'bound_reopened_status_first',
        'proxy_transport_failure',
        'mutation_resent_or_not_committed',
        'marker',
        'early_exit',
        'sigkill',
        'normal_exit',
        'verification',
        'cleanup',
        'collector',
      }.contains(error.message)) {
    return error.message;
  }
  return 'unexpected_failure';
}

void _requireReceiptCounts(_ResponseDroppingProxy proxy) {
  if (proxy.forwardFailures != 0) throw StateError('proxy_transport_failure');
  for (final rpc in _receiptMutations) {
    if (proxy.forwarded(rpc) != 1 ||
        proxy.dropped(rpc) != 1 ||
        proxy.rejected(rpc) != 0) {
      throw StateError('mutation_resent_or_not_committed');
    }
  }
}

Future<Process> _start(
  String mode,
  String scenario,
  String boundary,
  String path, {
  required ProbeLifecycle lifecycle,
  Uri? proxyEndpoint,
}) async {
  // Persist the launch observation before awaiting process creation itself.
  _logLifecycle(lifecycle);
  try {
    final process = await Process.start(
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
    lifecycle.record(ProbePhase.spawned);
    return process;
  } on Object {
    _logLifecycle(lifecycle);
    rethrow;
  }
}

void _logLifecycle(ProbeLifecycle lifecycle) => stderr.writeln(
  '\ncrash_probe_child_lifecycle:${jsonEncode(lifecycle.snapshot())}',
);

ProbeCollector<Object?> _observeStderr(
  Process process,
  ProbeLifecycle lifecycle,
) {
  final lines = ProbeJsonLines(
    onMessage: lifecycle.observeWorker,
    onInvalid: () {}, // Raw child stderr and build-hook output stay discarded.
  );
  return ProbeCollector(
    process.stderr,
    onData: lines.add,
    onDone: () {
      lines.finish();
      return null;
    },
  );
}

Future<void> _crash(
  String scenario,
  String boundary,
  String path, {
  Uri? proxyEndpoint,
  Duration timeout = _timeout,
}) async {
  final lifecycle = ProbeLifecycle(
    mode: 'crash',
    scenario: scenario,
    boundary: boundary,
  );
  final process = await _start(
    'crash',
    scenario,
    boundary,
    path,
    lifecycle: lifecycle,
    proxyEndpoint: proxyEndpoint,
  );
  final ready = Completer<int>();
  final stderrCollector = _observeStderr(process, lifecycle);
  final lines = ProbeJsonLines(
    onMessage: (message) {
      try {
        final failure = parseProbeFailure(message);
        if (failure != null && !ready.isCompleted) {
          ready.completeError(StateError(failure));
          return;
        }
        if (message['event'] != 'ready' || ready.isCompleted) {
          throw StateError('marker');
        }
        ready.complete(message['pid']! as int);
      } catch (_) {
        if (!ready.isCompleted) ready.completeError(StateError('marker'));
      }
    },
    onInvalid: () {
      if (!ready.isCompleted) ready.completeError(StateError('marker'));
    },
  );
  final stdoutCollector = ProbeCollector<Object?>(
    process.stdout,
    onData: lines.add,
    onDone: () {
      lines.finish();
      if (!ready.isCompleted) ready.completeError(StateError('early_exit'));
      return null;
    },
  );
  lifecycle.collect(stdout: stdoutCollector, stderr: stderrCollector);
  int? workerPid;
  var killed = false;
  var exited = false;
  await withProbeCleanup(
    body: () async {
      final readyPid = await waitForProbe<int>(
        ready.future,
        timeout,
        lifecycle,
      );
      workerPid = readyPid;
      lifecycle.observeWorkerPid(sameAsDirect: readyPid == process.pid);
      lifecycle.record(ProbePhase.ready);
      lifecycle.record(ProbePhase.sigkill);
      if (!lifecycle.observeKill(
        ProbeKill.worker,
        () => Process.killPid(readyPid, ProcessSignal.sigkill),
      )) {
        throw StateError('sigkill');
      }
      killed = true;
      final result = await waitForProbe(process.exitCode, timeout, lifecycle);
      exited = true;
      lifecycle.observeExit(result);
      lifecycle.record(ProbePhase.exited);
      if (result == 0) throw StateError('normal_exit');
    },
    cleanup: () async {
      try {
        try {
          if (!killed && workerPid != null && workerPid != process.pid) {
            lifecycle.observeKill(
              ProbeKill.workerCleanup,
              () => Process.killPid(workerPid!, ProcessSignal.sigkill),
            );
          }
        } finally {
          await cleanupProbe(
            exited: exited,
            kill: () => process.kill(ProcessSignal.sigkill),
            exitCode: process.exitCode,
            timeout: timeout,
            lifecycle: lifecycle,
          );
        }
      } finally {
        _logLifecycle(lifecycle);
      }
    },
  );
}

Future<void> _verify(
  String scenario,
  String boundary,
  String path, {
  Uri? proxyEndpoint,
  Duration timeout = _timeout,
}) async {
  final lifecycle = ProbeLifecycle(
    mode: 'verify',
    scenario: scenario,
    boundary: boundary,
  );
  final process = await _start(
    'verify',
    scenario,
    boundary,
    path,
    lifecycle: lifecycle,
    proxyEndpoint: proxyEndpoint,
  );
  var exited = false;
  final messageBuffer = ProbeMessageBuffer();
  final stdoutCollector = ProbeCollector(
    process.stdout,
    onData: messageBuffer.add,
    onDone: messageBuffer.finish,
  );
  final stderrCollector = _observeStderr(process, lifecycle);
  lifecycle.collect(stdout: stdoutCollector, stderr: stderrCollector);
  int? result;
  await withProbeCleanup(
    body: () async {
      final code = await waitForProbe(process.exitCode, timeout, lifecycle);
      result = code;
      exited = true;
      lifecycle.observeExit(code);
      lifecycle.record(ProbePhase.exited);
    },
    cleanup: () async {
      try {
        await cleanupProbe(
          exited: exited,
          kill: () => process.kill(ProcessSignal.sigkill),
          exitCode: process.exitCode,
          timeout: timeout,
          lifecycle: lifecycle,
        );
      } finally {
        _logLifecycle(lifecycle);
      }
    },
    validate: () => validateProbeVerification(result!, stdoutCollector),
  );
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
  final HttpClient _upstream = HttpClient(
    context: SecurityContext(withTrustedRoots: false)
      ..setTrustedCertificates(
        Platform.environment['LANTERN_DART_RECEIPT_CA_FILE'] ??
            (throw StateError('native receipt CA is required')),
      ),
  );
  final Map<String, int> _remainingDrops;
  final Map<String, int> _forwarded = <String, int>{};
  final Map<String, int> _dropped = <String, int>{};
  final Map<String, int> _rejected = <String, int>{};
  final Map<String, int> _rejectedStatus = <String, int>{};
  final List<String> _requestTrace = <String>[];
  int _forwardFailures = 0;

  Uri get endpoint =>
      Uri(scheme: 'http', host: _server.address.host, port: _server.port);

  int forwarded(String rpc) => _forwarded[rpc] ?? 0;

  int dropped(String rpc) => _dropped[rpc] ?? 0;

  int rejected(String rpc) => _rejected[rpc] ?? 0;

  int rejectedStatus(String rpc) => _rejectedStatus[rpc] ?? 0;

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

      if (_receiptMutations.contains(rpc) &&
          upstreamResponse.statusCode != HttpStatus.ok) {
        _rejected[rpc] = (_rejected[rpc] ?? 0) + 1;
        _rejectedStatus[rpc] = upstreamResponse.statusCode;
      }
      final remaining = _remainingDrops[rpc] ?? 0;
      if (remaining > 0 && upstreamResponse.statusCode == HttpStatus.ok) {
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

Future<void> _runBoundReceiptCrash() async {
  final endpoint = Uri.parse(
    Platform.environment['LANTERN_DART_RECEIPT_ENDPOINT']!,
  );
  final directory = await Directory.systemTemp.createTemp(
    'lantern-bound-receipt-crash-',
  );
  final proxy = await _ResponseDroppingProxy.bind(
    endpoint,
    drops: {for (final rpc in _receiptMutations) rpc: 1},
  );
  try {
    final path = '${directory.path}/bound.db';
    await _crash(
      'bound-receipt',
      'after',
      path,
      proxyEndpoint: proxy.endpoint,
      timeout: _receiptTimeout,
    );
    _requireReceiptCounts(proxy);
    final initialStatus = proxy.forwarded('GetReceiptStatuses');
    // Durable unsent proof permits the first send without an absence lookup.
    // After the committed response is lost, every reopen must reconcile first.
    if (initialStatus != 0) throw StateError('bound_initial_status_query');
    await _verify(
      'bound-receipt',
      'after',
      path,
      proxyEndpoint: proxy.endpoint,
      timeout: _receiptTimeout,
    );
    _requireReceiptCounts(proxy);
    if (proxy.forwarded('GetReceiptStatuses') < initialStatus + 5) {
      throw StateError('bound_reopened_status_first');
    }
    stdout.writeln(
      jsonEncode({
        'bound_receipt_scenarios': 1,
        'sigkill': 1,
        'fresh_process_verified': 1,
        'response_drops': 5,
        'status_first_reconciled': 5,
      }),
    );
  } catch (error) {
    stderr.writeln('bound_receipt_crash_probe_failed:${_failureCode(error)}');
    exitCode = 1;
  } finally {
    await proxy.close();
    await directory.delete(recursive: true);
  }
}
