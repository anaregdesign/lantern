import 'dart:async';
import 'dart:io';

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

  HttpClient createHttpClient() {
    if (_closed) throw StateError('Physical relay is closed');
    return HttpClient()
      ..connectionTimeout = const Duration(seconds: 5)
      ..findProxy = ((_) => 'DIRECT')
      ..connectionFactory = (url, proxyHost, proxyPort) {
        if (_closed ||
            url.scheme != endpoint.scheme ||
            url.host != endpoint.host ||
            url.port != endpoint.port ||
            proxyHost != null) {
          throw StateError('Physical relay route changed');
        }
        return Socket.startConnect(_server.address, _server.port);
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
