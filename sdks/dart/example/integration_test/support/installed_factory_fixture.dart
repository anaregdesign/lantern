import 'dart:async';
import 'dart:io';

import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline_adapter.dart';
import 'package:lantern_example/app_data_session.dart';

const installedFactoryProfileScenarios = {
  AppDataProfile.direct: 'factory_direct',
  AppDataProfile.memory: 'factory_memory',
  AppDataProfile.durable: 'factory_durable',
  AppDataProfile.durableWithoutReadCache: 'factory_durable_without_read_cache',
};

final installedFactoryScenarios = {
  ...installedFactoryProfileScenarios.values,
  'immutable_account_a_b_a',
  'shared_remote_wipe_before_swap',
  'installation_restore_ownership',
};

/// Extracts the exact string kind for assertions and asynchronous predicates.
/// Decoded VertexValue objects do not have structural equality.
String? installedFactoryStringPayload(VertexValue? value) =>
    value is StringValue ? value.value : null;

/// Owns raw fixture errors while keeping observations attached through close.
/// Only the typed terminal error during an explicit retirement is expected.
final class InstalledFactoryObservations {
  bool _retiring = false, _retired = false;
  Object? _failure;
  StackTrace? _trace;
  int closedErrors = 0;

  StreamSubscription<T> listen<T>(Stream<T> stream, void Function(T) receive) {
    if (_retiring || _retired) throw StateError('Observation owner retired');
    var closed = false;
    return stream.listen(
      (value) {
        if (closed || _retired) {
          _fail(
            StateError('Observation published after retirement'),
            StackTrace.current,
          );
        }
        receive(value);
      },
      onError: (Object error, StackTrace trace) {
        if (_retiring &&
            !closed &&
            error is LanternDataException &&
            error.reason == DataFailure.closed) {
          closed = true;
          closedErrors++;
        } else {
          _fail(error, trace);
        }
      },
    );
  }

  void _fail(Object error, StackTrace trace) {
    _failure ??= error;
    _trace ??= trace;
  }

  void check() {
    if (_failure case final failure?) {
      Error.throwWithStackTrace(failure, _trace!);
    }
  }

  Future<void> retire(Future<void> Function() close) async {
    check();
    if (_retiring || _retired) throw StateError('Observation owner retired');
    _retiring = true;
    try {
      await close();
      // Direct close queues typed errors; Offline may silently cancel upstream.
      await Future<void>.delayed(Duration.zero);
      check();
    } finally {
      _retiring = false;
      _retired = true;
    }
  }
}

/// Relays opaque TCP bytes. HTTPS still authenticates the original URL, not the
/// loopback relay. Cutting both sockets also interrupts existing WatchChanges.
final class PhysicalSocketRelay {
  PhysicalSocketRelay._(this.endpoint, this._server) {
    _server.listen((socket) {
      final work = _bridge(socket);
      _work.add(work);
      unawaited(work.whenComplete(() => _work.remove(work)));
    }, onError: (Object error) => _failure ??= error);
  }

  static Future<PhysicalSocketRelay> open(
    Uri endpoint, {
    bool allowInsecure = false,
  }) async {
    if (endpoint.host.isEmpty ||
        endpoint.userInfo.isNotEmpty ||
        endpoint.hasQuery ||
        endpoint.hasFragment ||
        (endpoint.scheme != 'https' &&
            !(allowInsecure && endpoint.scheme == 'http'))) {
      throw ArgumentError('Invalid physical relay endpoint');
    }
    return PhysicalSocketRelay._(
      endpoint,
      await ServerSocket.bind(InternetAddress.loopbackIPv4, 0),
    );
  }

  final Uri endpoint;
  final ServerSocket _server;
  final _sockets = <Socket>{};
  final _work = <Future<void>>{};
  bool _available = true, _closed = false;
  int _epoch = 0;
  Object? _failure;
  Future<void>? _closing;
  int acceptedConnections = 0, rejectedConnections = 0, cutConnections = 0;
  int transportDisconnects = 0;
  int get activeConnections => _sockets.length;

  HttpClient createHttpClient({SecurityContext? context}) {
    if (_closed) throw StateError('Physical relay is closed');
    return HttpClient(context: context)
      ..connectionTimeout = const Duration(seconds: 5)
      ..findProxy = ((_) => 'DIRECT')
      ..connectionFactory = (url, proxyHost, proxyPort) async {
        if (_closed ||
            url.scheme != endpoint.scheme ||
            url.host != endpoint.host ||
            url.port != endpoint.port ||
            proxyHost != null) {
          throw StateError('Physical relay route changed');
        }
        final task = await Socket.startConnect(_server.address, _server.port);
        if (url.scheme == 'http') return task;

        // A custom direct connectionFactory must supply TLS itself. The TCP
        // peer is loopback, but TLS authenticates the original endpoint host.
        final connected = Completer<Socket>();
        Socket? owned;
        var canceled = false;
        unawaited(() async {
          try {
            final raw = owned = await task.socket;
            if (canceled) {
              raw.destroy();
              return;
            }
            final secure = await SecureSocket.secure(
              raw,
              host: endpoint.host,
              context: context,
            );
            owned = secure;
            // TLS may complete after cancellation detached the raw Socket.
            if (canceled) {
              secure.destroy();
              return;
            }
            connected.complete(secure);
          } catch (error, stack) {
            owned?.destroy();
            if (!connected.isCompleted) connected.completeError(error, stack);
          }
        }());
        return ConnectionTask.fromSocket(connected.future, () {
          canceled = true;
          task.cancel();
          owned?.destroy();
          if (!connected.isCompleted) {
            connected.completeError(
              const SocketException('Physical relay connection canceled'),
            );
          }
        });
      };
  }

  void setAvailable(bool available) {
    if (_closed) throw StateError('Physical relay is closed');
    _available = available;
    if (!available) {
      _epoch++;
      cutConnections += _sockets.length;
      for (final socket in _sockets.toList()) {
        socket.destroy();
      }
    }
  }

  Future<void> _bridge(Socket incoming) async {
    acceptedConnections++;
    if (_closed || !_available) {
      rejectedConnections++;
      incoming.destroy();
      return;
    }
    _sockets.add(incoming);
    final epoch = _epoch;
    Socket? outgoing;
    try {
      outgoing = await Socket.connect(
        endpoint.host,
        endpoint.port,
        timeout: const Duration(seconds: 5),
      );
      if (_closed || !_available || epoch != _epoch) return;
      _sockets.add(outgoing);
      await Future.wait([
        incoming.addStream(outgoing).whenComplete(incoming.close),
        outgoing.addStream(incoming).whenComplete(outgoing.close),
      ]);
    } on SocketException {
      // Peer resets, including canceled CDC, are transport loss, not relay faults.
      transportDisconnects++;
    } catch (error) {
      _failure ??= error;
    } finally {
      incoming.destroy();
      outgoing?.destroy();
      _sockets.remove(incoming);
      _sockets.remove(outgoing);
    }
  }

  void check() {
    if (_failure != null) {
      throw StateError('Physical relay failed: ${_failure.runtimeType}');
    }
  }

  Future<void> close() => _closing ??= _close();
  Future<void> _close() async {
    _closed = true;
    for (final socket in _sockets.toList()) {
      socket.destroy();
    }
    await _server.close();
    await Future.wait(_work.toList());
    check();
  }
}

/// Counts real runtime alarms without replacing wall time or retry deadlines.
final class PhysicalSessionSchedule {
  final _alarms = <_PhysicalAlarm>{};
  int get activeAlarms => _alarms.length;
  OfflineSessionAlarm call(Duration delay, void Function() callback) {
    late _PhysicalAlarm alarm;
    alarm = _PhysicalAlarm(
      Timer(delay, () {
        _alarms.remove(alarm);
        callback();
      }),
      () => _alarms.remove(alarm),
    );
    _alarms.add(alarm);
    return alarm;
  }
}

final class _PhysicalAlarm implements OfflineSessionAlarm {
  _PhysicalAlarm(this._timer, this._remove);
  final Timer _timer;
  final void Function() _remove;
  @override
  void cancel() {
    _timer.cancel();
    _remove();
  }
}
