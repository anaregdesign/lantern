import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline_sqlite/lantern_client_offline_sqlite.dart';
import 'package:path/path.dart' as paths;
import 'package:sqflite/sqflite.dart' as sqflite;

import 'support/receipt_attestation.dart';
import 'support/receipt_physical_fixture.dart';
import 'support/receipt_scenarios.dart';

const _partition = 'physical-receipt';
const _expiresIn = Duration(hours: 8);
const _actionDeadline = Duration(minutes: 4);
const _maxFloat32 = 3.4028234663852886e38;
const _restartCleanups = <String>{
  'remote_graph',
  'repository',
  'sqlite_store',
  'proxy_client',
  'direct_client',
  'fixture',
  'journal',
  'directory',
  'operator_phase',
};

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  test('receipt mutations survive a real physical SIGKILL', () async {
    final platform = switch ((Platform.isAndroid, Platform.isIOS)) {
      (true, false) => 'android',
      (false, true) => 'ios',
      _ => throw StateError('Receipt matrix requires a physical phone'),
    };
    final run = ReceiptAttestation.fromBuild(
      target: receiptMatrixTarget,
      requiredScenarios: requiredReceiptScenarios(platform),
      requiredRestartCleanups: _restartCleanups,
    );
    final root = Directory(
      paths.join(
        await sqflite.getDatabasesPath(),
        'lantern-physical-receipt-${run.runId}',
      ),
    );
    final journal = File(paths.join(root.path, 'restart.json'));
    final database = paths.join(root.path, 'offline.db');
    final keys = _ReceiptKeys(run.runId);
    final actions = _OperatorActions(run.runId);

    if (await root.exists()) {
      await run.resumeAfterRestart(journal, (attestation) async {
        await _verifyAfterKill(
          attestation,
          PhysicalReceiptFixture.fromBuild(),
          keys,
          actions,
          root,
          journal,
          database,
        );
      });
      return;
    }
    await run.prepareForRestart(journal, (attestation) async {
      await _prepareForKill(
        attestation,
        PhysicalReceiptFixture.fromBuild(),
        keys,
        actions,
        root,
        database,
      );
    });
    await actions.announce('sigkill_now');
    await Future<void>.delayed(const Duration(minutes: 20));
    throw StateError('Receipt app was not SIGKILLed after preparation');
  }, timeout: const Timeout(Duration(hours: 4)));
}

Future<void> _prepareForKill(
  ReceiptAttestation run,
  PhysicalReceiptFixture fixture,
  _ReceiptKeys keys,
  _OperatorActions actions,
  Directory root,
  String database,
) async {
  await root.create(recursive: true);
  run.registerCleanup(() => root.delete(recursive: true));
  run.registerCleanup(actions.clear);
  await actions.clear();
  final direct = fixture.client(fixture.endpoint);
  run.registerCleanup(direct.close);
  run.registerCleanup(fixture.close);
  run.registerCleanup(() => _cleanupRemote(direct, keys));
  final proxy = fixture.client(fixture.proxyEndpoint);
  run.registerCleanup(proxy.close);
  final store = await SqliteOfflineStore.open(path: database);
  run.registerCleanup(store.close);
  var repository = OfflineLanternRepository(
    store: store,
    remote: LanternClientOfflineRemote(direct),
    config: _directConfig(),
  );
  run.registerCleanup(() => repository.dispose());

  await _verifyTransport(run, fixture, direct, proxy);
  if (Platform.isIOS) {
    await _verifyIosPrivacy(run, fixture, direct, actions);
  }
  await _verifyConditionalPut(run, direct, repository, keys);
  await _verifyVertexDelete(run, direct, repository, keys);
  await _verifyEdgeDelete(run, direct, repository, keys);
  await _verifyContributionAdd(run, direct, repository, store, keys);
  await _verifyOverflow(direct, repository, keys);
  await _verifyRadioRecovery(run, direct, repository, keys, actions);
  if (Platform.isAndroid) {
    await _verifyAndroidIdle(run, direct, actions);
  }
  await repository.dispose();
  repository = OfflineLanternRepository(
    store: store,
    remote: LanternClientOfflineRemote(proxy),
    config: _ambiguousConfig(),
  );
  await _verifyCommittedLoss(run, fixture, direct, repository, store, keys);
  // Intentionally leave SQLite and both clients open until the host kills
  // this process; cleanup is reconstructed from this run ID after relaunch.
}

Future<void> _verifyAfterKill(
  ReceiptAttestation run,
  PhysicalReceiptFixture fixture,
  _ReceiptKeys keys,
  _OperatorActions actions,
  Directory root,
  File journal,
  String database,
) async {
  run.registerCleanup(
    () => root.delete(recursive: true),
    restartObligation: 'directory',
  );
  run.registerCleanup(actions.clear, restartObligation: 'operator_phase');
  run.registerCleanup(
    () => journal.delete(),
    restartObligation: 'journal',
  );
  run.registerCleanup(fixture.close, restartObligation: 'fixture');
  final direct = fixture.client(fixture.endpoint);
  run.registerCleanup(direct.close, restartObligation: 'direct_client');
  final proxy = fixture.client(fixture.proxyEndpoint);
  run.registerCleanup(proxy.close, restartObligation: 'proxy_client');
  final store = await SqliteOfflineStore.open(path: database);
  run.registerCleanup(store.close, restartObligation: 'sqlite_store');
  final repository = OfflineLanternRepository(
    store: store,
    remote: LanternClientOfflineRemote(proxy),
    config: OfflineConfig(
      clock: () => DateTime.now().toUtc().add(const Duration(minutes: 6)),
      jitter: (_) => Duration.zero,
      maxConcurrency: 1,
      maxConcurrencyPerPartition: 1,
    ),
  );
  run.registerCleanup(
    repository.dispose,
    restartObligation: 'repository',
  );
  run.registerCleanup(
    () => _cleanupRemote(direct, keys),
    restartObligation: 'remote_graph',
  );

  await run.verifyScenario('receipt_real_sigkill_sqlite_reopen', () async {
    expect(store.path, database);
    final pending = await store.transaction(
      (transaction) => transaction.outbox(_partition),
    );
    expect(pending, hasLength(4));
    expect(
      pending.map((record) => record.attemptCount),
      everyElement(1),
    );
    expect(
      pending.map((record) => record.receipt?.state),
      everyElement(OfflineReceiptReconciliationState.statusRequired),
    );
    for (final operation in keys.responseLossOperations) {
      final status = await repository.getWriteStatus(_partition, operation);
      expect(status?.items.single.state, OfflineWriteState.retryScheduled);
    }
  });

  await run.verifyScenario('receipt_relaunch_status_first_no_resend', () async {
    expect(await repository.drain(_partition), 4);
    final trace = await fixture.proxyTrace();
    trace.assertRecoveredWithoutResend();
    await _expectResult<OfflineVertexPutReceiptResult>(
      repository,
      keys.operation('lost_put'),
      (result) => expect(result.outcome, PutOutcome.conditionNotMet),
    );
    await _expectResult<OfflineVertexDeleteReceiptResult>(
      repository,
      keys.operation('lost_vertex_delete'),
      (result) => expect(result.existed, isTrue),
    );
    await _expectResult<OfflineEdgeDeleteReceiptResult>(
      repository,
      keys.operation('lost_edge_delete'),
      (result) => expect(result.existed, isTrue),
    );
    await _expectResult<OfflineEdgeAddReceiptResult>(
      repository,
      keys.operation('lost_add'),
      (result) => expect(result.effectiveWeight, 4),
    );
    final original = await direct.getVertex(keys.vertex('lost_put'));
    expect((original.value as StringValue).value, 'original');
    await _missingVertex(direct, keys.vertex('lost_vertex_delete'));
    await _missingEdge(direct, keys.edge('lost_edge_delete'));
    await _missingEdge(direct, keys.edge('lost_add'));
    expect(
      await store.transaction((transaction) => transaction.outbox(_partition)),
      isEmpty,
    );
  });

  await run.verifyScenario('receipt_nonfinite_derived_float32', () async {
    await _expectResult<OfflineEdgeAddReceiptResult>(
      repository,
      keys.operation('overflow'),
      (result) => expect(_float32Bits(result.effectiveWeight), 0x7f800000),
    );
    await _missingEdge(direct, keys.edge('overflow'));
  });
}

OfflineConfig _directConfig() => OfflineConfig(
  maxConcurrency: 1,
  maxConcurrencyPerPartition: 1,
  baseRetryDelay: const Duration(seconds: 1),
  maxRetryDelay: const Duration(seconds: 1),
  jitter: (_) => Duration.zero,
);

OfflineConfig _ambiguousConfig() => OfflineConfig(
  maxConcurrency: 1,
  maxConcurrencyPerPartition: 1,
  baseRetryDelay: const Duration(minutes: 5),
  maxRetryDelay: const Duration(minutes: 5),
  jitter: (ceiling) => ceiling,
);

Future<void> _verifyTransport(
  ReceiptAttestation run,
  PhysicalReceiptFixture fixture,
  LanternClient direct,
  LanternClient proxy,
) async {
  await run.verifyScenario('receipt_authenticated_trusted_tls', () async {
    expect(await fixture.token(), isNotEmpty);
    final capability = await direct.getReceiptCapability();
    final viaProxy = await proxy.getReceiptCapability();
    expect(capability, isA<ReceiptCapabilityEnabled>());
    expect(viaProxy, isA<ReceiptCapabilityEnabled>());
    final enabled = capability as ReceiptCapabilityEnabled;
    expect((viaProxy as ReceiptCapabilityEnabled).endpoint, enabled.endpoint);
    expect(enabled.policy.retention, greaterThan(const Duration(hours: 4)));
    for (final mutation in ReceiptMutationKind.values) {
      expect(enabled.supports(mutation), isTrue);
    }
    final missingToken = LanternClient.connect(
      fixture.endpoint,
      retryPolicy: const RetryPolicy(maxAttempts: 1),
    );
    try {
      await expectLater(
        missingToken.getReceiptCapability(),
        throwsA(isA<LanternUnauthenticatedException>()),
      );
    } finally {
      await missingToken.close();
    }
  });

  await run.verifyScenario('receipt_untrusted_tls_rejected', () async {
    final untrusted = fixture.client(fixture.untrustedEndpoint);
    try {
      await untrusted.getReceiptCapability();
      fail('An untrusted certificate unexpectedly passed validation');
    } on LanternUnavailableException catch (error) {
      final cause = '${error.cause}';
      expect(
        cause.contains('CERTIFICATE_VERIFY_FAILED') ||
            cause.contains('Hostname mismatch') ||
            cause.contains('certificate verify failed'),
        isTrue,
        reason: 'A refusal or timeout is not negative TLS evidence',
      );
    } finally {
      await untrusted.close();
    }
  });
}

Future<void> _verifyConditionalPut(
  ReceiptAttestation run,
  LanternClient direct,
  OfflineLanternRepository repository,
  _ReceiptKeys keys,
) => run.verifyScenario('receipt_conditional_put_exact', () async {
  final expiration = DateTime.now().toUtc().add(_expiresIn);
  expect(
    await direct.putVertex(
      VertexInput(
        key: keys.vertex('existing'),
        value: VertexValue.string('original'),
        expiresAt: expiration,
      ),
    ),
    PutOutcome.appliedAndLive,
  );
  final write = await repository.putVerticesIfAbsent(
    partitionId: _partition,
    operationId: keys.operation('conditional'),
    inputs: [
      VertexInput(
        key: keys.vertex('existing'),
        value: VertexValue.string('replacement'),
        expiresAt: expiration,
      ),
      VertexInput(
        key: keys.vertex('created'),
        value: VertexValue.string('created'),
        expiresAt: expiration,
      ),
    ],
  );
  expect(write.itemCount, 2);
  expect(await repository.drain(_partition), 2);
  final status = await repository.getWriteStatus(_partition, write.operationId);
  expect(status?.items.map((item) => item.itemIndex), [0, 1]);
  expect(
    (status!.items[0].receiptResult as OfflineVertexPutReceiptResult).outcome,
    PutOutcome.conditionNotMet,
  );
  expect(
    (status.items[1].receiptResult as OfflineVertexPutReceiptResult).outcome,
    PutOutcome.appliedAndLive,
  );
  expect(
    (await direct.getVertex(keys.vertex('existing'))).value,
    isA<StringValue>().having((value) => value.value, 'value', 'original'),
  );
  expect(
    (await direct.getVertex(keys.vertex('created'))).value,
    isA<StringValue>().having((value) => value.value, 'value', 'created'),
  );
});

Future<void> _verifyVertexDelete(
  ReceiptAttestation run,
  LanternClient direct,
  OfflineLanternRepository repository,
  _ReceiptKeys keys,
) => run.verifyScenario('receipt_vertex_delete_exact', () async {
  expect(
    await direct.putVertex(
      VertexInput(
        key: keys.vertex('delete_vertex'),
        value: VertexValue.int32(7),
        expiresIn: _expiresIn,
      ),
    ),
    PutOutcome.appliedAndLive,
  );
  final write = await repository.deleteVertices(
    partitionId: _partition,
    operationId: keys.operation('vertex_delete'),
    keys: [keys.vertex('delete_vertex'), keys.vertex('missing_vertex')],
  );
  expect(write.itemCount, 2);
  expect(await repository.drain(_partition), 2);
  final status = await repository.getWriteStatus(_partition, write.operationId);
  expect(status?.items.map((item) => item.itemIndex), [0, 1]);
  expect(
    (status!.items[0].receiptResult as OfflineVertexDeleteReceiptResult).existed,
    isTrue,
  );
  expect(
    (status.items[1].receiptResult as OfflineVertexDeleteReceiptResult).existed,
    isFalse,
  );
  await _missingVertex(direct, keys.vertex('delete_vertex'));
  await _missingVertex(direct, keys.vertex('missing_vertex'));
});

Future<void> _verifyEdgeDelete(
  ReceiptAttestation run,
  LanternClient direct,
  OfflineLanternRepository repository,
  _ReceiptKeys keys,
) => run.verifyScenario('receipt_edge_delete_exact', () async {
  final present = keys.edge('delete_edge');
  final missing = keys.edge('missing_edge');
  expect(
    await direct.putEdge(
      EdgeInput(
        tail: present.tail,
        head: present.head,
        weight: 1,
        expiresIn: _expiresIn,
      ),
    ),
    PutOutcome.appliedAndLive,
  );
  final write = await repository.deleteEdges(
    partitionId: _partition,
    operationId: keys.operation('edge_delete'),
    edges: [present, missing],
  );
  expect(write.itemCount, 2);
  expect(await repository.drain(_partition), 2);
  final status = await repository.getWriteStatus(_partition, write.operationId);
  expect(status?.items.map((item) => item.itemIndex), [0, 1]);
  expect(
    (status!.items[0].receiptResult as OfflineEdgeDeleteReceiptResult).existed,
    isTrue,
  );
  expect(
    (status.items[1].receiptResult as OfflineEdgeDeleteReceiptResult).existed,
    isFalse,
  );
  await _missingEdge(direct, present);
  await _missingEdge(direct, missing);
});

Future<void> _verifyContributionAdd(
  ReceiptAttestation run,
  LanternClient direct,
  OfflineLanternRepository repository,
  SqliteOfflineStore store,
  _ReceiptKeys keys,
) => run.verifyScenario('receipt_contribution_add_after_delete', () async {
  final edge = keys.edge('contribution');
  final expiration = DateTime.now().toUtc().add(_expiresIn);
  expect(
    await direct.putEdge(
      EdgeInput(
        tail: edge.tail,
        head: edge.head,
        weight: 1,
        expiresAt: expiration,
      ),
    ),
    PutOutcome.appliedAndLive,
  );
  expect(await direct.deleteEdge(edge), isTrue);
  await _missingEdge(direct, edge);
  final first = EdgeInput(
    tail: edge.tail,
    head: edge.head,
    weight: 2,
    expiresAt: expiration,
    contribId: _contribution(1),
  );
  final second = EdgeInput(
    tail: edge.tail,
    head: edge.head,
    weight: 3,
    expiresAt: expiration,
    contribId: _contribution(2),
  );
  final write = await repository.addEdges(
    partitionId: _partition,
    operationId: keys.operation('contribution'),
    inputs: [first, second],
  );
  final prepared = await store.transaction(
    (transaction) => transaction.outbox(_partition),
  );
  final original = prepared
      .where(
        (record) =>
            record.operationId == write.operationId && record.itemIndex == 0,
      )
      .single
      .receipt!
      .context;
  expect(await repository.drain(_partition), 2);
  final status = await repository.getWriteStatus(_partition, write.operationId);
  expect(status?.items.map((item) => item.itemIndex), [0, 1]);
  expect(
    (status!.items[0].receiptResult as OfflineEdgeAddReceiptResult)
        .effectiveWeight,
    2,
  );
  expect(
    (status.items[1].receiptResult as OfflineEdgeAddReceiptResult)
        .effectiveWeight,
    5,
  );
  expect((await direct.getEdge(edge)).weight, 5);
  expect(await direct.deleteEdge(edge), isTrue);
  await _missingEdge(direct, edge);
  expect((await direct.addEdgeWithReceipt(first, context: original)).effectiveWeight, 2);
  await expectLater(
    direct.addEdgeWithReceipt(
      EdgeInput(
        tail: edge.tail,
        head: edge.head,
        weight: 6,
        expiresAt: expiration,
        contribId: _contribution(1),
      ),
      context: original,
    ),
    throwsA(isA<LanternInvalidArgumentException>()),
  );
  for (final inputs in [
    [EdgeInput(tail: edge.tail, head: edge.head, weight: 1, contribId: Uint8List(23))],
    [
      EdgeInput(tail: edge.tail, head: edge.head, weight: 1, contribId: _contribution(3)),
      EdgeInput(tail: edge.tail, head: edge.head, weight: 1, contribId: _contribution(3)),
    ],
  ]) {
    await expectLater(
      repository.addEdges(partitionId: _partition, inputs: inputs),
      throwsA(isA<OfflineArgumentException>()),
    );
  }
  await _missingEdge(direct, edge);
});

Future<void> _verifyOverflow(
  LanternClient direct,
  OfflineLanternRepository repository,
  _ReceiptKeys keys,
) async {
  final edge = keys.edge('overflow');
  expect(
    await direct.putEdge(
      EdgeInput(
        tail: edge.tail,
        head: edge.head,
        weight: _maxFloat32,
        expiresIn: _expiresIn,
      ),
    ),
    PutOutcome.appliedAndLive,
  );
  final add = await repository.addEdge(
    partitionId: _partition,
    operationId: keys.operation('overflow'),
    input: EdgeInput(
      tail: edge.tail,
      head: edge.head,
      weight: _maxFloat32,
      expiresIn: _expiresIn,
      contribId: _contribution(4),
    ),
  );
  expect(await repository.drain(_partition), 1);
  await _expectResult<OfflineEdgeAddReceiptResult>(
    repository,
    add.operationId,
    (result) => expect(_float32Bits(result.effectiveWeight), 0x7f800000),
  );
  expect(_float32Bits((await direct.getEdge(edge)).weight), 0x7f800000);
  expect(await direct.deleteEdge(edge), isTrue);
  await _missingEdge(direct, edge);
  // Its durable aggregate is asserted again on the second process, before
  // the non-finite scenario is marked complete.
}

Future<void> _verifyRadioRecovery(
  ReceiptAttestation run,
  LanternClient direct,
  OfflineLanternRepository repository,
  _ReceiptKeys keys,
  _OperatorActions actions,
) => run.verifyScenario('receipt_radio_foreground_recovery', () async {
  final capability = await direct.getReceiptCapability();
  expect(capability, isA<ReceiptCapabilityEnabled>());
  final write = await repository.putVertexIfAbsent(
    partitionId: _partition,
    operationId: keys.operation('radio'),
    input: VertexInput(
      key: keys.vertex('radio'),
      value: VertexValue.string('recovered'),
      expiresIn: _expiresIn,
    ),
  );
  await actions.announce('radio_disable');
  await _waitForTransportFailure(direct.getReceiptCapability);
  expect(await repository.drain(_partition), 0);
  final pending = await repository.getWriteStatus(_partition, write.operationId);
  expect(pending?.items.single.state, OfflineWriteState.retryScheduled);
  expect(pending?.items.single.attemptCount, 0);
  await actions.announce('radio_restore_foreground');
  await _waitForCapability(direct, (capability as ReceiptCapabilityEnabled).endpoint);
  await Future<void>.delayed(const Duration(seconds: 2));
  expect(await repository.drain(_partition), 1);
  await _expectResult<OfflineVertexPutReceiptResult>(
    repository,
    write.operationId,
    (result) => expect(result.outcome, PutOutcome.appliedAndLive),
  );
  expect(
    (await direct.getVertex(keys.vertex('radio'))).value,
    isA<StringValue>().having((value) => value.value, 'value', 'recovered'),
  );
});

Future<void> _verifyIosPrivacy(
  ReceiptAttestation run,
  PhysicalReceiptFixture fixture,
  LanternClient direct,
  _OperatorActions actions,
) => run.verifyScenario('ios_local_network_privacy_denial_retry', () async {
  final capability = await direct.getReceiptCapability();
  expect(capability, isA<ReceiptCapabilityEnabled>());
  final lan = fixture.client(fixture.lanEndpoint);
  try {
    await actions.announce('ios_deny_local_network');
    await _waitForTransportFailure(
      lan.getReceiptCapability,
      localPrivacy: true,
    );
    await actions.announce('ios_allow_local_network');
    await _waitForCapability(
      lan,
      (capability as ReceiptCapabilityEnabled).endpoint,
    );
  } finally {
    await lan.close();
  }
});

Future<void> _verifyAndroidIdle(
  ReceiptAttestation run,
  LanternClient direct,
  _OperatorActions actions,
) => run.verifyScenario('android_doze_like_pause', () async {
  final capability = await direct.getReceiptCapability();
  expect(capability, isA<ReceiptCapabilityEnabled>());
  var paused = false;
  var resumed = false;
  final lifecycle = AppLifecycleListener(
    onPause: () => paused = true,
    onResume: () => resumed = true,
  );
  try {
    await actions.announce('android_enter_idle');
    final deadline = DateTime.now().add(_actionDeadline);
    while (!(paused && resumed)) {
      if (DateTime.now().isAfter(deadline)) {
        throw StateError('Android app did not pause and resume');
      }
      await Future<void>.delayed(const Duration(milliseconds: 250));
    }
    await _waitForCapability(
      direct,
      (capability as ReceiptCapabilityEnabled).endpoint,
    );
  } finally {
    lifecycle.dispose();
  }
});

Future<void> _verifyCommittedLoss(
  ReceiptAttestation run,
  PhysicalReceiptFixture fixture,
  LanternClient direct,
  OfflineLanternRepository repository,
  SqliteOfflineStore store,
  _ReceiptKeys keys,
) => run.verifyScenario('receipt_committed_response_loss', () async {
  final expiration = DateTime.now().toUtc().add(_expiresIn);
  expect(
    await direct.putVertex(
      VertexInput(
        key: keys.vertex('lost_put'),
        value: VertexValue.string('original'),
        expiresAt: expiration,
      ),
    ),
    PutOutcome.appliedAndLive,
  );
  expect(
    await direct.putVertex(
      VertexInput(
        key: keys.vertex('lost_vertex_delete'),
        value: VertexValue.string('delete'),
        expiresAt: expiration,
      ),
    ),
    PutOutcome.appliedAndLive,
  );
  for (final name in ['lost_edge_delete', 'lost_add']) {
    final edge = keys.edge(name);
    expect(
      await direct.putEdge(
        EdgeInput(
          tail: edge.tail,
          head: edge.head,
          weight: 1,
          expiresAt: expiration,
        ),
      ),
      PutOutcome.appliedAndLive,
    );
  }
  expect(await direct.deleteEdge(keys.edge('lost_add')), isTrue);
  await _missingEdge(direct, keys.edge('lost_add'));

  await repository.putVertexIfAbsent(
    partitionId: _partition,
    operationId: keys.operation('lost_put'),
    input: VertexInput(
      key: keys.vertex('lost_put'),
      value: VertexValue.string('replacement'),
      expiresAt: expiration,
    ),
  );
  await repository.deleteVertex(
    partitionId: _partition,
    operationId: keys.operation('lost_vertex_delete'),
    key: keys.vertex('lost_vertex_delete'),
  );
  await repository.deleteEdge(
    partitionId: _partition,
    operationId: keys.operation('lost_edge_delete'),
    edge: keys.edge('lost_edge_delete'),
  );
  await repository.addEdge(
    partitionId: _partition,
    operationId: keys.operation('lost_add'),
    input: EdgeInput(
      tail: keys.edge('lost_add').tail,
      head: keys.edge('lost_add').head,
      weight: 4,
      expiresAt: expiration,
      contribId: _contribution(5),
    ),
  );
  final beforeSend = await store.transaction(
    (transaction) => transaction.outbox(_partition),
  );
  expect(beforeSend, hasLength(4));
  expect(
    beforeSend.map((record) => record.operationId).toSet(),
    keys.responseLossOperations.toSet(),
  );
  expect(
    beforeSend.map((record) => record.receipt?.operationId).toSet().length,
    4,
  );
  expect(
    beforeSend.map((record) => record.attemptCount),
    everyElement(0),
  );
  expect(await repository.drain(_partition), 0);
  final ambiguous = await store.transaction(
    (transaction) => transaction.outbox(_partition),
  );
  expect(ambiguous, hasLength(4));
  for (final record in ambiguous) {
    expect(record.attemptCount, 1);
    expect(record.diagnosticCode, 'receipt_response_unknown');
    expect(
      record.receipt!.state,
      OfflineReceiptReconciliationState.statusRequired,
    );
    final server = await direct.getReceiptStatus(record.receipt!.operationId);
    expect(server.state, ReceiptStatusState.confirmed);
    expect(server.receipt, isNotNull);
  }
  expect((await direct.getEdge(keys.edge('lost_add'))).weight, 4);
  expect(await direct.deleteEdge(keys.edge('lost_add')), isTrue);
  await _missingEdge(direct, keys.edge('lost_add'));
  final trace = await fixture.proxyTrace();
  trace.assertInitialLoss();
  await fixture.sealProxyHandoff();
});

Future<void> _waitForTransportFailure(
  Future<Object?> Function() attempt, {
  bool localPrivacy = false,
}) async {
  final deadline = DateTime.now().add(_actionDeadline);
  while (DateTime.now().isBefore(deadline)) {
    try {
      await attempt();
    } on LanternUnavailableException catch (error) {
      if (!localPrivacy) return;
      final cause = '${error.cause}';
      if (cause.contains('No route to host') ||
          cause.contains('Operation not permitted')) {
        return;
      }
      throw StateError('iOS Local Network denial was not observed');
    }
    await Future<void>.delayed(const Duration(milliseconds: 500));
  }
  throw StateError('Physical network denial was not observed');
}

Future<void> _waitForCapability(
  LanternClient client,
  ReceiptEndpoint endpoint,
) async {
  final deadline = DateTime.now().add(_actionDeadline);
  while (DateTime.now().isBefore(deadline)) {
    try {
      final capability = await client.getReceiptCapability();
      if (capability is! ReceiptCapabilityEnabled ||
          capability.endpoint != endpoint) {
        throw StateError('Receipt responder changed during physical recovery');
      }
      return;
    } on LanternUnavailableException {
      await Future<void>.delayed(const Duration(milliseconds: 500));
    }
  }
  throw StateError('Physical receipt transport did not recover');
}

Future<void> _expectResult<T extends OfflineReceiptResult>(
  OfflineLanternRepository repository,
  String operationId,
  void Function(T result) verify,
) async {
  final status = await repository.getWriteStatus(_partition, operationId);
  expect(status?.items, hasLength(1));
  final item = status!.items.single;
  expect(item.state, OfflineWriteState.confirmed);
  expect(item.attemptCount, 1);
  expect(item.receiptResult, isA<T>());
  verify(item.receiptResult! as T);
}

Future<void> _missingVertex(LanternClient client, String key) =>
    expectLater(client.getVertex(key), throwsA(isA<LanternNotFoundException>()));

Future<void> _missingEdge(LanternClient client, EdgeRef edge) =>
    expectLater(client.getEdge(edge), throwsA(isA<LanternNotFoundException>()));

Future<void> _cleanupRemote(LanternClient client, _ReceiptKeys keys) async {
  await client.deleteVertices(keys.vertices);
  await client.deleteEdges(keys.edges);
}

Uint8List _contribution(int suffix) => Uint8List(24)..[23] = suffix;

int _float32Bits(double value) =>
    (ByteData(4)..setFloat32(0, value, Endian.big)).getUint32(0, Endian.big);

final class _ReceiptKeys {
  const _ReceiptKeys(this.runId);

  final String runId;
  String get prefix => 'physical-receipt:$runId:';
  String vertex(String name) => '$prefix$name';
  EdgeRef edge(String name) =>
      EdgeRef('${prefix}${name}_tail', '${prefix}${name}_head');
  String operation(String name) => 'receipt-$runId-$name';

  Iterable<String> get vertices => [
    'existing', 'created', 'delete_vertex', 'missing_vertex',
    'lost_put', 'lost_vertex_delete', 'radio',
  ].map(vertex);
  Iterable<EdgeRef> get edges => [
    'delete_edge', 'missing_edge', 'contribution', 'overflow',
    'lost_edge_delete', 'lost_add',
  ].map(edge);
  Iterable<String> get responseLossOperations => [
    'lost_put', 'lost_vertex_delete', 'lost_edge_delete', 'lost_add',
  ].map(operation);
}

final class _OperatorActions {
  const _OperatorActions(this.runId);

  final String runId;
  File get _file => File(
    '${Directory.systemTemp.path}/lantern-receipt-operator-phase.json',
  );

  Future<void> announce(String action) async {
    final pending = File('${_file.path}.tmp');
    await pending.writeAsString(
      jsonEncode({
        'schema': 1,
        'kind': 'physical_receipt_operator_phase',
        'runId': runId,
        'action': action,
        'recordedAt': DateTime.now().toUtc().toIso8601String(),
      }),
      flush: true,
    );
    await pending.rename(_file.path);
  }

  Future<void> clear() async {
    if (await _file.exists()) await _file.delete();
  }
}
