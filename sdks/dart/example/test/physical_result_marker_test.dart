import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:flutter_test/flutter_test.dart';

import '../integration_test/support/physical_result_marker.dart';
import '../integration_test/support/receipt_attestation.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  final messenger =
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  late Directory sandbox;
  late File marker;

  setUp(() async {
    sandbox = await Directory.systemTemp.createTemp('physical-result-marker-');
    marker = File('${sandbox.path}/result.json');
  });

  tearDown(() async {
    messenger.setMockMethodCallHandler(installedReceiptBinaryChannel, null);
    await sandbox.delete(recursive: true);
  });

  Future<Map<String, dynamic>> saved() async =>
      jsonDecode(await marker.readAsString()) as Map<String, dynamic>;

  test('ordinary result does not require a native installed binary', () async {
    var lookups = 0;
    messenger.setMockMethodCallHandler(installedReceiptBinaryChannel, (
      call,
    ) async {
      lookups++;
      throw StateError('ordinary smoke must not inspect the native binary');
    });
    final result = PhysicalResultMarker(
      'result.json',
      kind: 'physical_mobile_smoke_on_device_result',
      requireInstalledBinary: false,
      output: marker,
    );
    await result.recordOutcome('passed', 'complete');
    expect(lookups, 0);
    expect((await saved()).keys.toSet(), {
      'schema',
      'kind',
      'contentFree',
      'status',
      'phase',
      'startedAt',
      'updatedAt',
    });
  });

  for (final (platform, packageId, relativePath) in [
    ('android', 'com.anaregdesign.lantern_example', 'base.apk'),
    ('ios', 'com.anaregdesign.lanternExample', 'Frameworks/App.framework/App'),
  ]) {
    test(
      '$platform qualification hashes only the final passed marker',
      () async {
        final installed = File('${sandbox.path}/$relativePath');
        await installed.parent.create(recursive: true);
        await installed.writeAsString('installed $platform AOT bytes');
        var lookups = 0;
        messenger.setMockMethodCallHandler(installedReceiptBinaryChannel, (
          call,
        ) async {
          lookups++;
          expect(call.method, 'installedBinary');
          return {
            'platform': platform,
            'packageId': packageId,
            'path': installed.path,
          };
        });
        final result = PhysicalResultMarker(
          'result.json',
          kind: 'physical_identity_cdc_on_device_result',
          requireInstalledBinary: true,
          output: marker,
        );
        await result.recordPhase('live_invalidation');
        expect(lookups, 0);
        expect((await saved()).keys.toSet(), {
          'schema',
          'kind',
          'contentFree',
          'status',
          'phase',
          'startedAt',
          'updatedAt',
        });

        await result.recordOutcome('passed', 'complete');
        final finalMarker = await saved();
        expect(lookups, 1);
        expect(finalMarker['status'], 'passed');
        expect(finalMarker['phase'], 'complete');
        expect(finalMarker['platform'], platform);
        expect(finalMarker['packageId'], packageId);
        expect(
          finalMarker['installedBinarySha256'],
          sha256
              .convert(utf8.encode('installed $platform AOT bytes'))
              .toString(),
        );
        expect(finalMarker.containsKey('failureType'), isFalse);
      },
    );
  }

  test(
    'qualification fails closed when native installed bytes vanish',
    () async {
      final installed = File('${sandbox.path}/base.apk');
      await installed.writeAsString('installed AOT bytes');
      messenger.setMockMethodCallHandler(
        installedReceiptBinaryChannel,
        (call) async => {
          'platform': 'android',
          'packageId': 'com.anaregdesign.lantern_example',
          'path': installed.path,
        },
      );
      final result = PhysicalResultMarker(
        'result.json',
        kind: 'physical_mobile_smoke_on_device_result',
        requireInstalledBinary: true,
        output: marker,
      );
      await result.recordPhase('cleanup');
      await installed.delete();

      await expectLater(
        result.recordOutcome('passed', 'complete'),
        throwsA(isA<FileSystemException>()),
      );
      final failed = await saved();
      expect(failed['status'], 'failed');
      expect(failed['phase'], 'attestation');
      expect(failed['failureType'], 'attestation');
      expect(failed.containsKey('installedBinarySha256'), isFalse);
    },
  );
}
