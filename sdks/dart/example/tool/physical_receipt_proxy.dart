import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:path/path.dart' as paths;

const _mutationRpcs = <String>{
  'PutVertices',
  'DeleteVertices',
  'DeleteEdges',
  'AddEdges',
};
const _allowedRpcs = <String>{
  'GetReceiptCapability',
  'GetReceiptStatuses',
  ..._mutationRpcs,
};
const _controlPath = '/_receipt_matrix_status';
const _handoffEvent = 'AwaitingSigkill';
const _servicePath = '/graph.v1.LanternService/';
const _maxBodyBytes = 1024 * 1024;
const _maxTraceEntries = 256;

Future<void> main() async {
  try {
    final configPath = Platform.environment['LANTERN_RECEIPT_PROXY_CONFIG'];
    if (configPath == null || !paths.isAbsolute(configPath)) {
      throw StateError('private_proxy_config_required');
    }
    final configFile = File(configPath);
    if (await FileSystemEntity.type(configPath, followLinks: false) !=
            FileSystemEntityType.file ||
        ((await configFile.stat()).mode & 0x3f) != 0) {
      throw StateError('private_proxy_config_permissions');
    }
    final config = ReceiptProxyConfig.parse(await configFile.readAsString());
    final context = SecurityContext(withTrustedRoots: true)
      ..useCertificateChain(config.certificateChain)
      ..usePrivateKey(config.privateKey);
    final server = await HttpServer.bindSecure(
      config.listenHost,
      config.listenPort,
      context,
    );
    final proxy = ReceiptResponseDropProxy(
      server,
      config.upstream,
      config.token,
    );
    ProcessSignal.sigint.watch().listen((_) => unawaited(proxy.close()));
    ProcessSignal.sigterm.watch().listen((_) => unawaited(proxy.close()));
    stdout.writeln('RECEIPT_PROXY_READY');
    await proxy.done;
  } on Object catch (error) {
    stderr.writeln('RECEIPT_PROXY_FAILED type=${error.runtimeType}');
    exitCode = 1;
  }
}

final class ReceiptProxyConfig {
  ReceiptProxyConfig._({
    required this.listenHost,
    required this.listenPort,
    required this.certificateChain,
    required this.privateKey,
    required this.upstream,
    required this.token,
  });

  final String listenHost;
  final int listenPort;
  final String certificateChain;
  final String privateKey;
  final Uri upstream;
  final String token;

  static ReceiptProxyConfig parse(String text) {
    final Object? decoded = jsonDecode(text);
    const fields = {
      'listenHost',
      'listenPort',
      'certificateChain',
      'privateKey',
      'upstream',
      'controlToken',
    };
    if (decoded is! Map<String, dynamic> ||
        decoded.keys.toSet().difference(fields).isNotEmpty ||
        decoded.length != fields.length ||
        decoded['listenPort'] is! int ||
        (decoded['listenPort'] as int) <= 0 ||
        (decoded['listenPort'] as int) > 65535) {
      throw const FormatException('invalid_private_proxy_configuration');
    }
    String requiredString(String field) {
      final value = decoded[field];
      if (value is! String || value.isEmpty) {
        throw const FormatException('invalid_private_proxy_configuration');
      }
      return value;
    }

    final upstream = Uri.tryParse(requiredString('upstream'));
    if (upstream == null ||
        (upstream.scheme != 'https' &&
            (upstream.scheme != 'http' ||
                !{'127.0.0.1', '::1', 'localhost'}.contains(upstream.host))) ||
        upstream.userInfo.isNotEmpty ||
        upstream.path.isNotEmpty ||
        upstream.query.isNotEmpty ||
        upstream.fragment.isNotEmpty) {
      throw const FormatException('invalid_private_proxy_upstream');
    }
    final certificate = requiredString('certificateChain');
    final key = requiredString('privateKey');
    if (!paths.isAbsolute(certificate) || !paths.isAbsolute(key)) {
      throw const FormatException('invalid_private_proxy_tls_material');
    }
    return ReceiptProxyConfig._(
      listenHost: requiredString('listenHost'),
      listenPort: decoded['listenPort'] as int,
      certificateChain: certificate,
      privateKey: key,
      upstream: upstream,
      token: requiredString('controlToken'),
    );
  }
}

/// A host-only, test-specific HTTPS proxy with one committed socket drop per RPC.
final class ReceiptResponseDropProxy {
  ReceiptResponseDropProxy(
    HttpServer server,
    this._upstream,
    this._controlToken,
  ) : _server = server {
    _http.autoUncompress = false;
    _server.listen(
      (request) => unawaited(_handle(request)),
      onDone: () {
        if (!_done.isCompleted) _done.complete();
      },
    );
  }

  final HttpServer _server;
  final Uri _upstream;
  final String _controlToken;
  final HttpClient _http = HttpClient();
  final Completer<void> _done = Completer<void>();
  final Map<String, int> _forwarded = {};
  final Map<String, int> _dropped = {};
  final List<String> _trace = [];
  var _failures = 0;

  Future<void> get done => _done.future;

  Future<void> close() async {
    await _server.close(force: true);
    _http.close(force: true);
    if (!_done.isCompleted) _done.complete();
  }

  Future<void> _handle(HttpRequest request) async {
    if (request.uri.path == _controlPath) {
      await _control(request);
      return;
    }
    final uri = request.uri;
    if (request.method != 'POST' ||
        uri.hasScheme ||
        uri.hasAuthority ||
        uri.hasQuery ||
        uri.hasFragment ||
        !uri.path.startsWith(_servicePath) ||
        !_allowedRpcs.contains(uri.path.substring(_servicePath.length))) {
      request.response.statusCode = HttpStatus.badRequest;
      await request.response.close();
      return;
    }
    final rpc = uri.path.substring(_servicePath.length);
    if (_trace.length >= _maxTraceEntries) {
      _failures++;
      request.response.statusCode = HttpStatus.serviceUnavailable;
      await request.response.close();
      return;
    }
    _trace.add(rpc);
    _forwarded.update(rpc, (count) => count + 1, ifAbsent: () => 1);
    try {
      final payload = await _bounded(request);
      final upstreamRequest = await _http.openUrl(
        request.method,
        _upstream.resolveUri(uri),
      );
      _copyHeaders(request.headers, upstreamRequest.headers);
      upstreamRequest.add(payload);
      final upstreamResponse = await upstreamRequest.close();
      final response = await _bounded(upstreamResponse);
      if (_mutationRpcs.contains(rpc) &&
          (_dropped[rpc] ?? 0) == 0 &&
          upstreamResponse.statusCode == HttpStatus.ok) {
        _dropped[rpc] = 1;
        final socket = await request.response.detachSocket(writeHeaders: false);
        socket.destroy();
        return;
      }
      request.response.statusCode = upstreamResponse.statusCode;
      _copyHeaders(upstreamResponse.headers, request.response.headers);
      request.response.add(response);
      await request.response.close();
    } on Object catch (error) {
      _failures++;
      stderr.writeln('RECEIPT_PROXY_REQUEST_FAILED type=${error.runtimeType}');
      try {
        request.response.statusCode = HttpStatus.badGateway;
        await request.response.close();
      } on Object catch (closeError) {
        stderr.writeln(
          'RECEIPT_PROXY_RESPONSE_UNAVAILABLE type=${closeError.runtimeType}',
        );
      }
    }
  }

  Future<void> _control(HttpRequest request) async {
    if (request.headers.value(HttpHeaders.authorizationHeader) !=
        'Bearer $_controlToken') {
      request.response.statusCode = HttpStatus.unauthorized;
      await request.response.close();
      return;
    }
    if (request.method == 'POST') {
      if (_trace.contains(_handoffEvent) ||
          _failures != 0 ||
          _mutationRpcs.any(
            (rpc) => _forwarded[rpc] != 1 || _dropped[rpc] != 1,
          ) ||
          (_forwarded['GetReceiptStatuses'] ?? 0) < _mutationRpcs.length ||
          _trace.length >= _maxTraceEntries) {
        request.response.statusCode = HttpStatus.conflict;
      } else {
        _trace.add(_handoffEvent);
        request.response.statusCode = HttpStatus.noContent;
      }
      await request.response.close();
      return;
    }
    if (request.method != 'GET') {
      request.response.statusCode = HttpStatus.methodNotAllowed;
      await request.response.close();
      return;
    }
    request.response.headers.contentType = ContentType.json;
    request.response.headers.set(HttpHeaders.cacheControlHeader, 'no-store');
    request.response.write(
      jsonEncode({
        'schema': 1,
        'forwarded': _forwarded,
        'dropped': _dropped,
        'trace': _trace,
        'failures': _failures,
      }),
    );
    await request.response.close();
  }

  static Future<Uint8List> _bounded(Stream<List<int>> stream) async {
    final bytes = BytesBuilder(copy: false);
    await for (final chunk in stream) {
      if (bytes.length + chunk.length > _maxBodyBytes) {
        throw StateError('receipt_proxy_body_limit');
      }
      bytes.add(chunk);
    }
    return bytes.takeBytes();
  }

  static void _copyHeaders(HttpHeaders source, HttpHeaders target) {
    const ignored = {
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
      if (!ignored.contains(name.toLowerCase())) target.set(name, values);
    });
  }
}
