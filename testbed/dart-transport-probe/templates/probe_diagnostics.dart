import 'dart:convert';

/// The mobile runner consumes only these bounded, single-line JSON events.
/// Never stringify an exception, endpoint, header, certificate, or result here.
class ProbeDiagnostics {
  ProbeDiagnostics({
    required this.transport,
    required this.errorCode,
    void Function(String)? writeLine,
  }) : _writeLine = writeLine ?? print {
    if (transport != 'connect' && transport != 'grpc') {
      throw ArgumentError('unsupported probe transport');
    }
  }

  final String transport;
  final String Function(Object) errorCode;
  final void Function(String) _writeLine;
  String _scenario = 'plaintext';
  String _method = 'none';
  String? _failedMethod;
  String? _failureCode;
  bool _markerCompleted = false;

  /// Catch the whole entrypoint so Flutter never prints raw unhandled errors.
  /// Failure remains blocking through its terminal marker and absent success.
  Future<void> run(Future<void> Function() operation) async {
    try {
      await operation();
      if (!_markerCompleted) {
        throw StateError('the final marker probe did not complete');
      }
      _emit('result', 'success');
    } catch (error, stackTrace) {
      _emit(
        'result',
        'failure',
        method: _failedMethod ?? _method,
        code: _failureCode ?? errorCode(error),
        stackTrace: stackTrace,
      );
    }
  }

  Future<void> scenario(String name, Future<void> Function() operation) async {
    if (!_scenarios.contains(name)) {
      throw ArgumentError('unsupported probe scenario');
    }
    _scenario = name;
    _method = 'none';
    _failedMethod = null;
    _failureCode = null;
    _emit('scenario', 'start');
    try {
      await operation();
    } catch (error, stackTrace) {
      _failureCode ??= errorCode(error);
      _emit(
        'scenario',
        _expectsFailure ? 'expected_failure' : 'failure',
        method: _failedMethod ?? _method,
        code: _failureCode,
        stackTrace: stackTrace,
      );
      if (!_expectsFailure) rethrow;
      _failedMethod = null;
      _failureCode = null;
      _method = 'none';
      return;
    }
    if (_expectsFailure) {
      _failureCode = 'unexpected_success';
      _emit('scenario', 'failure', code: _failureCode);
      throw StateError('negative probe unexpectedly succeeded');
    }
    _emit('scenario', 'success');
    _method = 'none';
    if (name == 'marker') _markerCompleted = true;
  }

  void rpc({
    required String method,
    required String state,
    String? code,
    StackTrace? stackTrace,
  }) {
    if (!_methods.contains(method) || !_rpcStates.contains(state)) return;
    if (state == 'start') _method = method;
    if (state == 'failure' && _failedMethod == null) {
      _failedMethod = method;
      _failureCode = code ?? 'unknown';
    }
    _emit(
      'rpc',
      state == 'failure' && _expectsFailure ? 'expected_failure' : state,
      method: method,
      code: code,
      stackTrace: stackTrace,
    );
  }

  bool get _expectsFailure =>
      _scenario == 'wrong_host' || _scenario == 'missing_auth';

  void _emit(
    String kind,
    String state, {
    String method = 'none',
    String? code,
    StackTrace? stackTrace,
  }) {
    final stack = stackTrace == null ? <String>[] : _safeStack(stackTrace);
    final event = <String, Object>{
      'schema': 1,
      'transport': transport,
      'kind': kind,
      'scenario': _scenario,
      'method': method,
      'state': state,
      'code': code == null ? 'ok' : (_codes.contains(code) ? code : 'unknown'),
      if (stack.isNotEmpty) 'stack': stack,
    };
    _writeLine('LANTERN_PROBE_EVENT ${jsonEncode(event)}');
  }
}

const _scenarios = <String>{
  'plaintext',
  'wrong_host',
  'missing_auth',
  'trusted_tls',
  'marker',
};
const _methods = <String>{
  'PutVertex',
  'GetVertex',
  'BackupSnapshot',
  'ChannelShutdown',
};
const _rpcStates = <String>{'start', 'success', 'failure'};
const _codes = <String>{
  'ok',
  'cancelled',
  'unknown',
  'invalid_argument',
  'deadline_exceeded',
  'not_found',
  'already_exists',
  'permission_denied',
  'resource_exhausted',
  'failed_precondition',
  'aborted',
  'out_of_range',
  'unimplemented',
  'internal',
  'unavailable',
  'data_loss',
  'unauthenticated',
  'assertion',
  'configuration',
  'unexpected_success',
};

List<String> _safeStack(StackTrace stackTrace) {
  // Only literal known Dart basenames and bounded numeric positions survive.
  // A stack's function names, path prefixes, and any other text are discarded.
  final frame = RegExp(
    r'(?:^|[/\\])(probe\.dart|mobile_main\.dart|main\.dart|probe_diagnostics\.dart):([0-9]{1,7}):([0-9]{1,7})\)?$',
  );
  final text = stackTrace.toString();
  final bounded = text.length > 16384 ? text.substring(0, 16384) : text;
  final frames = <String>[];
  for (final line in const LineSplitter().convert(bounded)) {
    final match = frame.firstMatch(line);
    if (match != null) {
      frames.add('${match[1]}:${match[2]}:${match[3]}');
      if (frames.length == 8) break;
    }
  }
  return frames;
}
