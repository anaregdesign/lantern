import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:lantern_client/lantern_client.dart';

const _receiptMutations = <String>{
  'PutVertices',
  'DeleteVertices',
  'DeleteEdges',
  'AddEdges',
};
const _proxyControlPath = '/_receipt_matrix_status';
const _proxyTraceLimit = 256;
const _fixtureBodyLimit = 32 * 1024;

/// Private, compile-time fixture addresses never enter the public attestation.
final class PhysicalReceiptFixture {
  PhysicalReceiptFixture._({
    required this.endpoint,
    required this.proxyEndpoint,
    required this.untrustedEndpoint,
    required this.lanEndpoint,
    required this.tokenEndpoint,
  }) {
    _tokenHttp.connectionTimeout = const Duration(seconds: 8);
    _controlHttp.connectionTimeout = const Duration(seconds: 8);
  }

  factory PhysicalReceiptFixture.fromBuild() => PhysicalReceiptFixture._(
    endpoint: _https(
      const String.fromEnvironment('LANTERN_RECEIPT_ENDPOINT'),
      base: true,
    ),
    proxyEndpoint: _https(
      const String.fromEnvironment('LANTERN_RECEIPT_PROXY_ENDPOINT'),
      base: true,
    ),
    untrustedEndpoint: _https(
      const String.fromEnvironment('LANTERN_RECEIPT_UNTRUSTED_ENDPOINT'),
      base: true,
    ),
    lanEndpoint: _https(
      const String.fromEnvironment('LANTERN_RECEIPT_LAN_ENDPOINT'),
      base: true,
    ),
    tokenEndpoint: _https(
      const String.fromEnvironment('LANTERN_RECEIPT_TOKEN_ENDPOINT'),
      base: false,
    ),
  );

  final Uri endpoint;
  final Uri proxyEndpoint;
  final Uri untrustedEndpoint;
  final Uri lanEndpoint;
  final Uri tokenEndpoint;
  final HttpClient _tokenHttp = HttpClient();
  final HttpClient _controlHttp = HttpClient();
  String? _cachedToken;

  static Uri _https(String value, {required bool base}) {
    final uri = Uri.tryParse(value);
    if (uri == null ||
        uri.scheme != 'https' ||
        uri.host.isEmpty ||
        uri.userInfo.isNotEmpty ||
        uri.fragment.isNotEmpty ||
        (base && (uri.hasQuery || uri.path.isNotEmpty))) {
      throw StateError('Receipt fixture requires platform-trusted HTTPS');
    }
    return uri;
  }

  Future<String> token() async {
    final cached = _cachedToken;
    if (cached != null) return cached;
    final request = await _tokenHttp.getUrl(tokenEndpoint).timeout(
      const Duration(seconds: 8),
    );
    request.persistentConnection = false;
    final response = await request.close().timeout(
      const Duration(seconds: 8),
    );
    if (response.statusCode != HttpStatus.ok) {
      throw StateError('Receipt token fixture rejected authentication');
    }
    final decoded = jsonDecode(utf8.decode(await _bounded(response)));
    if (decoded is! Map<String, dynamic> ||
        decoded['access_token'] is! String ||
        (decoded['access_token'] as String).isEmpty) {
      throw StateError('Receipt token fixture returned an invalid token');
    }
    return _cachedToken = decoded['access_token'] as String;
  }

  LanternClient client(Uri uri) => LanternClient.connect(
    uri,
    tokenProvider: token,
    retryPolicy: const RetryPolicy(maxAttempts: 1),
    defaultTimeout: const Duration(seconds: 8),
  );

  Future<ReceiptProxyTrace> proxyTrace() async {
    final request = await _controlHttp.getUrl(
      proxyEndpoint.resolve(_proxyControlPath),
    );
    request.headers.set(HttpHeaders.authorizationHeader, 'Bearer ${await token()}');
    final response = await request.close();
    if (response.statusCode != HttpStatus.ok) {
      throw StateError('Receipt proxy control is unavailable');
    }
    final Object? decoded = jsonDecode(utf8.decode(await _bounded(response)));
    return ReceiptProxyTrace.parse(decoded);
  }

  Future<void> sealProxyHandoff() async {
    final request = await _controlHttp.postUrl(
      proxyEndpoint.resolve(_proxyControlPath),
    );
    request.headers.set(HttpHeaders.authorizationHeader, 'Bearer ${await token()}');
    final response = await request.close();
    await response.drain<void>();
    if (response.statusCode != HttpStatus.noContent) {
      throw StateError('Receipt proxy refused the SIGKILL handoff');
    }
  }

  void close() {
    _controlHttp.close(force: true);
    _tokenHttp.close(force: true);
  }

  static Future<Uint8List> _bounded(Stream<List<int>> source) async {
    final bytes = BytesBuilder(copy: false);
    await for (final chunk in source) {
      if (bytes.length + chunk.length > _fixtureBodyLimit) {
        throw StateError('Receipt fixture response is oversized');
      }
      bytes.add(chunk);
    }
    return bytes.takeBytes();
  }
}

/// Sanitized private proxy counts and ordering, never an operator pass label.
final class ReceiptProxyTrace {
  ReceiptProxyTrace._(
    this.forwarded,
    this.dropped,
    this.trace,
    this.failures,
  );

  final Map<String, int> forwarded;
  final Map<String, int> dropped;
  final List<String> trace;
  final int failures;

  static ReceiptProxyTrace parse(Object? decoded) {
    if (decoded is! Map<String, dynamic> ||
        decoded.keys.toSet().difference({
          'schema', 'forwarded', 'dropped', 'trace', 'failures',
        }).isNotEmpty ||
        decoded.length != 5 ||
        decoded['schema'] != 1 ||
        decoded['failures'] is! int ||
        (decoded['failures'] as int) < 0 ||
        decoded['forwarded'] is! Map<String, dynamic> ||
        decoded['dropped'] is! Map<String, dynamic> ||
        decoded['trace'] is! List<dynamic>) {
      throw StateError('Receipt proxy trace is invalid');
    }
    final forwarded = _counts(decoded['forwarded'] as Map<String, dynamic>);
    final dropped = _counts(decoded['dropped'] as Map<String, dynamic>);
    final events = decoded['trace'] as List<dynamic>;
    if (events.length > _proxyTraceLimit ||
        events.any(
          (event) =>
              event is! String ||
              !{
                'GetReceiptCapability',
                'GetReceiptStatuses',
                'AwaitingSigkill',
                ..._receiptMutations,
              }.contains(event),
        )) {
      throw StateError('Receipt proxy event trace is invalid');
    }
    return ReceiptProxyTrace._(
      forwarded,
      dropped,
      List<String>.from(events),
      decoded['failures'] as int,
    );
  }

  static Map<String, int> _counts(Map<String, dynamic> value) {
    final result = <String, int>{};
    for (final entry in value.entries) {
      if (!{'GetReceiptCapability', 'GetReceiptStatuses', ..._receiptMutations}
              .contains(entry.key) ||
          entry.value is! int ||
          (entry.value as int) < 0) {
        throw StateError('Receipt proxy counts are invalid');
      }
      result[entry.key] = entry.value as int;
    }
    return result;
  }

  void assertInitialLoss() {
    if (failures != 0 ||
        _receiptMutations.any(
          (rpc) => forwarded[rpc] != 1 || dropped[rpc] != 1,
        )) {
      throw StateError('Committed receipt response loss was not observed');
    }
    var statuses = 0;
    var mutations = 0;
    for (final event in trace) {
      if (event == 'GetReceiptStatuses') statuses++;
      if (_receiptMutations.contains(event)) {
        if (statuses <= mutations) {
          throw StateError('Receipt mutation preceded status lookup');
        }
        mutations++;
      }
    }
    if (mutations != _receiptMutations.length ||
        trace.contains('AwaitingSigkill')) {
      throw StateError('Receipt proxy already crossed a kill boundary');
    }
  }

  void assertRecoveredWithoutResend() {
    if (failures != 0 ||
        _receiptMutations.any(
          (rpc) => forwarded[rpc] != 1 || dropped[rpc] != 1,
        )) {
      throw StateError('A receipt mutation was resent after SIGKILL');
    }
    final boundary = trace.indexOf('AwaitingSigkill');
    if (boundary < 0 ||
        trace.lastIndexOf('AwaitingSigkill') != boundary ||
        trace.sublist(boundary + 1).where(
          (event) => event == 'GetReceiptStatuses',
        ).length <
            _receiptMutations.length ||
        trace.sublist(boundary + 1).any(_receiptMutations.contains)) {
      throw StateError('Receipt relaunch did not reconcile status first');
    }
  }
}
