part of 'client.dart';

// connectrpc's dart:io adapter awaits openUrl before observing the request's
// AbortSignal. A disconnected device can leave that await pending past the SDK
// deadline. Keep the upstream adapter for the rest of the HTTP exchange, but
// race connection opening against the same signal it handles afterward.
final class _AbortableOpenHttpClient implements io.HttpClient {
  _AbortableOpenHttpClient(this._client, this._signal);

  final io.HttpClient _client;
  final connect.AbortSignal? _signal;

  @override
  Future<io.HttpClientRequest> openUrl(String method, Uri url) {
    final opening = _client.openUrl(method, url);
    final signal = _signal;
    if (signal == null) return opening;

    connect.ConnectException? aborted;
    final canceled = signal.future.then<io.HttpClientRequest>((error) {
      aborted = error;
      throw error;
    });
    final opened = opening.then((request) {
      if (aborted case final error?) {
        try {
          request.abort(error);
        } catch (_) {
          // Preserve the original cancellation/deadline outcome. The call has
          // already completed; a late request cannot send any body through it.
        }
        throw error;
      }
      return request;
    });
    return Future.any([canceled, opened]);
  }

  @override
  dynamic noSuchMethod(Invocation invocation) =>
      throw UnsupportedError('only openUrl is used by the Connect I/O adapter');
}
