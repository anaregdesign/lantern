import 'package:lantern_client/lantern_client.dart';

/// Singular facades and ordinary Server-handling Futures over atomic admission.
mixin OfflineBoundWrites {
  /// Local admission and retained-result observation port.
  LanternMutationControl get mutations;

  /// Atomically admits aligned inputs and awaits every terminal Server handling result.
  Future<MutationBatchReply<PutOutcome>> putVertices(
    Iterable<VertexInput> inputs, {
    MutationOptions? options,
  }) async {
    final request = MutationRequest.putVertices(inputs);
    if (request.inputs.isEmpty) return MutationBatchReply<PutOutcome>(const []);
    final ticket = await mutations.submit(
      request,
      options: MutationOptions(
        operationId: options?.operationId,
        admissionCancellation: options?.admissionCancellation,
        wait: options?.wait ?? const WaitOptions(),
      ),
    );
    return (await ticket.completion!).reply!;
  }

  /// One-item facade preserving the ordinary Server-handling completion contract.
  Future<MutationReply<PutOutcome>> putVertex(
    VertexInput input, {
    MutationOptions? options,
  }) async => (await putVertices([input], options: options)).items.single;

  /// Atomically admits aligned inputs and awaits every terminal Server handling result.
  Future<MutationBatchReply<PutOutcome>> putVerticesIfAbsent(
    Iterable<VertexInput> inputs, {
    MutationOptions? options,
  }) async {
    final request = MutationRequest.putVerticesIfAbsent(inputs);
    if (request.inputs.isEmpty) return MutationBatchReply<PutOutcome>(const []);
    final ticket = await mutations.submit(
      request,
      options: MutationOptions(
        operationId: options?.operationId,
        admissionCancellation: options?.admissionCancellation,
        wait: options?.wait ?? const WaitOptions(),
      ),
    );
    return (await ticket.completion!).reply!;
  }

  /// One-item facade preserving the ordinary Server-handling completion contract.
  Future<MutationReply<PutOutcome>> putVertexIfAbsent(
    VertexInput input, {
    MutationOptions? options,
  }) async =>
      (await putVerticesIfAbsent([input], options: options)).items.single;

  /// Atomically admits aligned inputs and awaits every terminal Server handling result.
  Future<MutationBatchReply<PutOutcome>> putEdges(
    Iterable<EdgeInput> inputs, {
    MutationOptions? options,
  }) async {
    final request = MutationRequest.putEdges(inputs);
    if (request.inputs.isEmpty) return MutationBatchReply<PutOutcome>(const []);
    final ticket = await mutations.submit(
      request,
      options: MutationOptions(
        operationId: options?.operationId,
        admissionCancellation: options?.admissionCancellation,
        wait: options?.wait ?? const WaitOptions(),
      ),
    );
    return (await ticket.completion!).reply!;
  }

  /// One-item facade preserving the ordinary Server-handling completion contract.
  Future<MutationReply<PutOutcome>> putEdge(
    EdgeInput input, {
    MutationOptions? options,
  }) async => (await putEdges([input], options: options)).items.single;

  /// Atomically admits aligned inputs and awaits every terminal Server handling result.
  Future<MutationBatchReply<double>> addEdges(
    Iterable<EdgeInput> inputs, {
    MutationOptions? options,
  }) async {
    final request = MutationRequest.addEdges(inputs);
    if (request.inputs.isEmpty) return MutationBatchReply<double>(const []);
    final ticket = await mutations.submit(
      request,
      options: MutationOptions(
        operationId: options?.operationId,
        admissionCancellation: options?.admissionCancellation,
        wait: options?.wait ?? const WaitOptions(),
      ),
    );
    return (await ticket.completion!).reply!;
  }

  /// One-item facade preserving the ordinary Server-handling completion contract.
  Future<MutationReply<double>> addEdge(
    EdgeInput input, {
    MutationOptions? options,
  }) async => (await addEdges([input], options: options)).items.single;

  /// Atomically admits aligned inputs and awaits every terminal Server handling result.
  Future<MutationBatchReply<bool>> deleteVertices(
    Iterable<String> inputs, {
    MutationOptions? options,
  }) async {
    final request = MutationRequest.deleteVertices(inputs);
    if (request.inputs.isEmpty) return MutationBatchReply<bool>(const []);
    final ticket = await mutations.submit(
      request,
      options: MutationOptions(
        operationId: options?.operationId,
        admissionCancellation: options?.admissionCancellation,
        wait: options?.wait ?? const WaitOptions(),
      ),
    );
    return (await ticket.completion!).reply!;
  }

  /// One-item facade preserving the ordinary Server-handling completion contract.
  Future<MutationReply<bool>> deleteVertex(
    String input, {
    MutationOptions? options,
  }) async => (await deleteVertices([input], options: options)).items.single;

  /// Atomically admits aligned inputs and awaits every terminal Server handling result.
  Future<MutationBatchReply<bool>> deleteEdges(
    Iterable<EdgeRef> inputs, {
    MutationOptions? options,
  }) async {
    final request = MutationRequest.deleteEdges(inputs);
    if (request.inputs.isEmpty) return MutationBatchReply<bool>(const []);
    final ticket = await mutations.submit(
      request,
      options: MutationOptions(
        operationId: options?.operationId,
        admissionCancellation: options?.admissionCancellation,
        wait: options?.wait ?? const WaitOptions(),
      ),
    );
    return (await ticket.completion!).reply!;
  }

  /// One-item facade preserving the ordinary Server-handling completion contract.
  Future<MutationReply<bool>> deleteEdge(
    EdgeRef input, {
    MutationOptions? options,
  }) async => (await deleteEdges([input], options: options)).items.single;

  /// Atomically admits aligned inputs and awaits every terminal Server handling result.
  Future<MutationBatchReply<bool>> deleteEdgeContributions(
    Iterable<EdgeContributionRef> inputs, {
    MutationOptions? options,
  }) async {
    final request = MutationRequest.deleteEdgeContributions(inputs);
    if (request.inputs.isEmpty) return MutationBatchReply<bool>(const []);
    final ticket = await mutations.submit(
      request,
      options: MutationOptions(
        operationId: options?.operationId,
        admissionCancellation: options?.admissionCancellation,
        wait: options?.wait ?? const WaitOptions(),
      ),
    );
    return (await ticket.completion!).reply!;
  }

  /// One-item facade preserving the ordinary Server-handling completion contract.
  Future<MutationReply<bool>> deleteEdgeContribution(
    EdgeContributionRef input, {
    MutationOptions? options,
  }) async =>
      (await deleteEdgeContributions([input], options: options)).items.single;
}
