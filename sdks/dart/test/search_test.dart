import 'dart:async';

import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.connect.spec.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.pb.dart' as graph;
import 'package:test/test.dart';

void main() {
  test(
    'one-shot search preserves every composable wire option and ranked result',
    () async {
      late graph.SearchVerticesRequest captured;
      final transport = FakeTransportBuilder()
          .unary<graph.SearchVerticesRequest, graph.SearchVerticesResponse>(
            LanternService.searchVertices,
            (request, context) {
              captured = request.clone();
              return graph.SearchVerticesResponse(
                hits: [graph.SearchHit(key: 'p:a', score: 3.5)],
              );
            },
          )
          .build();
      final client = _client(transport);

      final result = await client.searchVertices(
        'lantern graph',
        searchOptions: const SearchOptions(
          limit: 7,
          prefix: 'p:',
          matchMode: SearchMatchMode.minShouldMatch,
          minShouldMatch: 1,
          fuzziness: 2,
          prefixTerms: true,
        ),
      );

      expect(result, isA<SearchEnabled>());
      expect((result as SearchEnabled).hits.single.key, 'p:a');
      expect(result.hits.single.score, 3.5);
      expect(captured.query, 'lantern graph');
      expect(captured.limit, 7);
      expect(captured.prefix, 'p:');
      expect(captured.hasOptions(), isTrue);
      expect(captured.options.matchMode, graph.MatchMode.MATCH_MODE_MIN_SHOULD);
      expect(captured.options.minShouldMatch, 1);
      expect(captured.options.phrase, isFalse);
      expect(captured.options.fuzziness, 2);
      expect(captured.options.prefixTerms, isTrue);
    },
  );

  test('phrase alone is forwarded with server-default membership', () async {
    late graph.SearchVerticesRequest captured;
    final transport = FakeTransportBuilder()
        .unary<graph.SearchVerticesRequest, graph.SearchVerticesResponse>(
          LanternService.searchVertices,
          (request, context) {
            captured = request.clone();
            return graph.SearchVerticesResponse();
          },
        )
        .build();
    await _client(transport).searchVertices(
      'alpha beta',
      searchOptions: const SearchOptions(phrase: true),
    );
    expect(captured.options.matchMode, graph.MatchMode.MATCH_MODE_UNSPECIFIED);
    expect(captured.options.phrase, isTrue);
  });

  test('minimum mode accepts the server threshold sentinel', () async {
    var calls = 0;
    final transport = FakeTransportBuilder()
        .unary<graph.SearchVerticesRequest, graph.SearchVerticesResponse>(
          LanternService.searchVertices,
          (request, context) {
            calls++;
            expect(
              request.options.matchMode,
              graph.MatchMode.MATCH_MODE_MIN_SHOULD,
            );
            expect(request.options.minShouldMatch, 0);
            return graph.SearchVerticesResponse();
          },
        )
        .build();
    final client = _client(transport);
    await client.searchVertices(
      'alpha beta',
      searchOptions: const SearchOptions(
        matchMode: SearchMatchMode.minShouldMatch,
      ),
    );
    await client.searchVertices(
      'alpha beta',
      searchOptions: const SearchOptions(
        matchMode: SearchMatchMode.minShouldMatch,
        minShouldMatch: 0,
      ),
    );
    expect(calls, 2);
  });

  test('omitted relevance controls omit SearchOptions', () async {
    late graph.SearchVerticesRequest captured;
    final transport = FakeTransportBuilder()
        .unary<graph.SearchVerticesRequest, graph.SearchVerticesResponse>(
          LanternService.searchVertices,
          (request, context) {
            captured = request.clone();
            return graph.SearchVerticesResponse();
          },
        )
        .build();
    final result = await _client(transport).searchVertices('none');
    expect((result as SearchEnabled).hits, isEmpty);
    expect(captured.hasOptions(), isFalse);
  });

  test('page forwards cursor and maps full-vertex snapshot metadata', () async {
    late graph.SearchVerticesRequest captured;
    final transport = FakeTransportBuilder()
        .unary<graph.SearchVerticesRequest, graph.SearchVerticesResponse>(
          LanternService.searchVertices,
          (request, context) {
            captured = request.clone();
            return graph.SearchVerticesResponse(
              hits: [
                graph.SearchHit(
                  key: 'doc/1',
                  score: 3.5,
                  vertex: graph.Vertex(key: 'doc/1', string: 'alpha'),
                  projectionStatus: graph
                      .SearchHitProjectionStatus
                      .SEARCH_HIT_PROJECTION_STATUS_SNAPSHOT,
                ),
              ],
              nextCursor: [4, 5],
              effectiveLimit: 1,
              truncated: true,
            );
          },
        )
        .build();

    final page = await _client(transport).searchVerticesPage(
      'alpha',
      searchOptions: const SearchOptions(
        limit: 1,
        cursor: [1, 2, 3],
        projection: SearchProjection.fullVertex,
      ),
    );

    expect(captured.cursor, [1, 2, 3]);
    expect(
      captured.projection,
      graph.SearchProjection.SEARCH_PROJECTION_FULL_VERTEX,
    );
    expect(page.hits.single.key, 'doc/1');
    expect(page.hits.single.vertex?.value, isA<StringValue>());
    expect((page.hits.single.vertex?.value as StringValue).value, 'alpha');
    expect(
      page.hits.single.projectionStatus,
      SearchHitProjectionStatus.snapshot,
    );
    expect(page.nextCursor, [4, 5]);
    expect(page.effectiveLimit, 1);
    expect(page.truncated, isTrue);
    expect(page.continuationLimited, isFalse);
  });

  test('stream follows opaque cursors lazily', () async {
    final cursors = <List<int>>[];
    final responses = [
      graph.SearchVerticesResponse(
        hits: [graph.SearchHit(key: 'doc/1', score: 2)],
        nextCursor: [9],
        effectiveLimit: 1,
        truncated: true,
      ),
      graph.SearchVerticesResponse(
        hits: [graph.SearchHit(key: 'doc/2', score: 1)],
        effectiveLimit: 1,
      ),
    ];
    final transport = FakeTransportBuilder()
        .unary<graph.SearchVerticesRequest, graph.SearchVerticesResponse>(
          LanternService.searchVertices,
          (request, context) {
            cursors.add(List<int>.from(request.cursor));
            return responses.removeAt(0);
          },
        )
        .build();

    final hits = await _client(transport)
        .searchVerticesStream(
          'alpha',
          searchOptions: const SearchOptions(limit: 1),
        )
        .toList();

    expect(hits.map((hit) => hit.key), ['doc/1', 'doc/2']);
    expect(cursors, [
      <int>[],
      [9],
    ]);
  });

  test(
    'stream surfaces a typed bounded-tail error after retained hits',
    () async {
      final transport = FakeTransportBuilder()
          .unary<graph.SearchVerticesRequest, graph.SearchVerticesResponse>(
            LanternService.searchVertices,
            (request, context) => graph.SearchVerticesResponse(
              hits: [graph.SearchHit(key: 'doc/1', score: 1)],
              effectiveLimit: 1,
              truncated: true,
              continuationLimited: true,
            ),
          )
          .build();
      final keys = <String>[];

      try {
        await for (final hit in _client(
          transport,
        ).searchVerticesStream('alpha')) {
          keys.add(hit.key);
        }
        fail('bounded continuation should fail after the retained tail');
      } on LanternSearchContinuationLimitedException catch (error) {
        expect(keys, ['doc/1']);
        expect(error.searchReason, SearchErrorReason.searchContinuationLimited);
      }
    },
  );

  test('stale cursor maps ABORTED detail to a dedicated exception', () async {
    final transport = FakeTransportBuilder()
        .unary<graph.SearchVerticesRequest, graph.SearchVerticesResponse>(
          LanternService.searchVertices,
          (request, context) => throw connect.ConnectException(
            connect.Code.aborted,
            'search cursor is stale',
            details: [
              connect.ErrorDetail(
                'graph.v1.SearchErrorDetail',
                graph.SearchErrorDetail(
                  reason: graph.SearchErrorReason.SEARCH_CURSOR_STALE,
                ).writeToBuffer(),
              ),
            ],
          ),
        )
        .build();

    await expectLater(
      _client(transport).searchVerticesPage(
        'alpha',
        searchOptions: const SearchOptions(cursor: [1]),
      ),
      throwsA(isA<LanternSearchCursorStaleException>()),
    );
  });

  test('invalid cursor retains its machine-readable reason', () async {
    final transport = FakeTransportBuilder()
        .unary<graph.SearchVerticesRequest, graph.SearchVerticesResponse>(
          LanternService.searchVertices,
          (request, context) => throw connect.ConnectException(
            connect.Code.invalidArgument,
            'search cursor does not belong to this request',
            details: [
              connect.ErrorDetail(
                'graph.v1.SearchErrorDetail',
                graph.SearchErrorDetail(
                  reason: graph.SearchErrorReason.SEARCH_CURSOR_INVALID,
                ).writeToBuffer(),
              ),
            ],
          ),
        )
        .build();

    await expectLater(
      _client(transport).searchVerticesPage('alpha'),
      throwsA(
        isA<LanternInvalidArgumentException>().having(
          (error) => error.searchReason,
          'searchReason',
          SearchErrorReason.searchCursorInvalid,
        ),
      ),
    );
  });

  test('search-disabled is a calm typed result', () async {
    final transport = FakeTransportBuilder()
        .unary<graph.SearchVerticesRequest, graph.SearchVerticesResponse>(
          LanternService.searchVertices,
          (request, context) => throw connect.ConnectException(
            connect.Code.failedPrecondition,
            'search is disabled',
            details: [
              connect.ErrorDetail(
                'graph.v1.SearchErrorDetail',
                graph.SearchErrorDetail(
                  reason: graph.SearchErrorReason.SEARCH_DISABLED,
                ).writeToBuffer(),
              ),
            ],
          ),
        )
        .build();
    final result = await _client(transport).searchVertices('q');
    expect(result, isA<SearchDisabled>());
    expect(
      (result as SearchDisabled).cause,
      isA<LanternFailedPreconditionException>(),
    );
    expect(result.cause.searchReason, SearchErrorReason.searchDisabled);
  });

  test('positions-disabled is not misclassified as search disabled', () async {
    final transport = FakeTransportBuilder()
        .unary<graph.SearchVerticesRequest, graph.SearchVerticesResponse>(
          LanternService.searchVertices,
          (request, context) => throw connect.ConnectException(
            connect.Code.failedPrecondition,
            'phrase search requires positional postings',
            details: [
              connect.ErrorDetail(
                'graph.v1.SearchErrorDetail',
                graph.SearchErrorDetail(
                  reason: graph.SearchErrorReason.SEARCH_POSITIONS_DISABLED,
                ).writeToBuffer(),
              ),
            ],
          ),
        )
        .build();
    await expectLater(
      _client(transport).searchVertices(
        'alpha beta',
        searchOptions: const SearchOptions(phrase: true),
      ),
      throwsA(
        isA<LanternFailedPreconditionException>().having(
          (error) => error.searchReason,
          'searchReason',
          SearchErrorReason.searchPositionsDisabled,
        ),
      ),
    );
  });

  test('work-budget error exposes bounded reason and work kind', () async {
    final transport = FakeTransportBuilder()
        .unary<graph.SearchVerticesRequest, graph.SearchVerticesResponse>(
          LanternService.searchVertices,
          (request, context) => throw connect.ConnectException(
            connect.Code.resourceExhausted,
            'posting budget exhausted',
            details: [
              connect.ErrorDetail(
                'graph.v1.SearchErrorDetail',
                graph.SearchErrorDetail(
                  reason: graph.SearchErrorReason.SEARCH_WORK_BUDGET_EXHAUSTED,
                  workKind: 'posting_visits',
                ).writeToBuffer(),
              ),
            ],
          ),
        )
        .build();
    await expectLater(
      _client(transport).searchVertices('alpha'),
      throwsA(
        isA<LanternResourceExhaustedException>()
            .having(
              (error) => error.searchReason,
              'searchReason',
              SearchErrorReason.searchWorkBudgetExhausted,
            )
            .having(
              (error) => error.searchWorkKind,
              'searchWorkKind',
              'posting_visits',
            ),
      ),
    );
  });

  test('admission saturation remains distinct from work exhaustion', () async {
    final transport = FakeTransportBuilder()
        .unary<graph.SearchVerticesRequest, graph.SearchVerticesResponse>(
          LanternService.searchVertices,
          (request, context) => throw connect.ConnectException(
            connect.Code.resourceExhausted,
            'search admission saturated',
            details: [
              connect.ErrorDetail(
                'graph.v1.SearchErrorDetail',
                graph.SearchErrorDetail(
                  reason: graph.SearchErrorReason.SEARCH_ADMISSION_SATURATED,
                ).writeToBuffer(),
              ),
            ],
          ),
        )
        .build();
    await expectLater(
      _client(transport).searchVertices('alpha'),
      throwsA(
        isA<LanternResourceExhaustedException>().having(
          (error) => error.searchReason,
          'searchReason',
          SearchErrorReason.searchAdmissionSaturated,
        ),
      ),
    );
  });

  test('index budget reason remains typed', () async {
    final transport = FakeTransportBuilder()
        .unary<graph.SearchVerticesRequest, graph.SearchVerticesResponse>(
          LanternService.searchVertices,
          (request, context) => throw connect.ConnectException(
            connect.Code.resourceExhausted,
            'document budget exhausted',
            details: [
              connect.ErrorDetail(
                'graph.v1.SearchErrorDetail',
                graph.SearchErrorDetail(
                  reason: graph.SearchErrorReason.SEARCH_INDEX_BUDGET_EXHAUSTED,
                  workKind: 'document_bytes',
                ).writeToBuffer(),
              ),
            ],
          ),
        )
        .build();
    await expectLater(
      _client(transport).searchVertices('alpha'),
      throwsA(
        isA<LanternResourceExhaustedException>()
            .having(
              (error) => error.searchReason,
              'searchReason',
              SearchErrorReason.searchIndexBudgetExhausted,
            )
            .having(
              (error) => error.searchWorkKind,
              'searchWorkKind',
              'document_bytes',
            ),
      ),
    );
  });

  test('invalid relevance combinations fail before transport', () async {
    var calls = 0;
    final transport = FakeTransportBuilder()
        .unary<graph.SearchVerticesRequest, graph.SearchVerticesResponse>(
          LanternService.searchVertices,
          (request, context) {
            calls++;
            return graph.SearchVerticesResponse();
          },
        )
        .build();
    final client = _client(transport);
    for (final options in [
      const SearchOptions(fuzziness: 3),
      const SearchOptions(minShouldMatch: 1),
      const SearchOptions(matchMode: SearchMatchMode.any, minShouldMatch: 1),
      const SearchOptions(phrase: true, matchMode: SearchMatchMode.all),
      const SearchOptions(phrase: true, fuzziness: 1),
      const SearchOptions(phrase: true, prefixTerms: true),
      const SearchOptions(limit: -1),
    ]) {
      await expectLater(
        client.searchVertices('q', searchOptions: options),
        throwsA(isA<LanternInvalidArgumentException>()),
      );
    }
    expect(calls, 0);
  });

  test(
    'incremental search debounces and emits only the latest query',
    () async {
      final requests = <String>[];
      final transport = FakeTransportBuilder()
          .unary<graph.SearchVerticesRequest, graph.SearchVerticesResponse>(
            LanternService.searchVertices,
            (request, context) {
              requests.add(request.query);
              return graph.SearchVerticesResponse(
                hits: [graph.SearchHit(key: request.query, score: 1)],
              );
            },
          )
          .build();
      final session = _client(transport).incrementalSearch(
        options: const IncrementalSearchOptions(
          debounce: Duration(milliseconds: 10),
        ),
      );
      addTearDown(session.dispose);
      final result = session.updates.firstWhere(
        (update) => update.phase == SearchUpdatePhase.results,
      );
      session.search('a');
      session.search('ab');

      expect((await result).query, 'ab');
      expect(requests, ['ab']);
    },
  );

  test(
    'newer query cancels and stale success or error cannot overwrite',
    () async {
      final firstStarted = Completer<void>();
      final firstCanceled = Completer<void>();
      final first = Completer<graph.SearchVerticesResponse>();
      final transport = FakeTransportBuilder()
          .unary<graph.SearchVerticesRequest, graph.SearchVerticesResponse>(
            LanternService.searchVertices,
            (request, context) {
              if (request.query == 'old') {
                firstStarted.complete();
                context.signal.future.then((_) {
                  if (!firstCanceled.isCompleted) firstCanceled.complete();
                });
                return first.future;
              }
              return graph.SearchVerticesResponse(
                hits: [graph.SearchHit(key: 'new', score: 2)],
              );
            },
          )
          .build();
      final session = _client(transport).incrementalSearch(
        options: const IncrementalSearchOptions(
          debounce: Duration(milliseconds: 80),
        ),
      );
      addTearDown(session.dispose);
      final delivered = <SearchUpdate>[];
      final subscription = session.updates.listen(delivered.add);
      addTearDown(subscription.cancel);

      session.search('old');
      await firstStarted.future;
      session.search('new');
      // Cancellation belongs to the input call, before the replacement's
      // debounce expires.
      await firstCanceled.future.timeout(const Duration(milliseconds: 30));
      await session.updates.firstWhere(
        (update) =>
            update.query == 'new' && update.phase == SearchUpdatePhase.results,
      );
      first.completeError(StateError('late old error'));
      await Future<void>.delayed(const Duration(milliseconds: 10));

      expect(
        delivered.where(
          (update) =>
              update.query == 'old' &&
              (update.phase == SearchUpdatePhase.results ||
                  update.phase == SearchUpdatePhase.error),
        ),
        isEmpty,
      );
      expect(delivered.last.query, 'new');
      expect(delivered.last.hits.single.key, 'new');
    },
  );

  test(
    'empty/minimum input is explicit idle and dispose cancels active RPC',
    () async {
      var calls = 0;
      final active = Completer<void>();
      var canceled = false;
      final transport = FakeTransportBuilder()
          .unary<graph.SearchVerticesRequest, graph.SearchVerticesResponse>(
            LanternService.searchVertices,
            (request, context) async {
              calls++;
              active.complete();
              await context.signal.future;
              canceled = true;
              throw connect.ConnectException(connect.Code.canceled, 'disposed');
            },
          )
          .build();
      final session = _client(transport).incrementalSearch(
        options: const IncrementalSearchOptions(
          debounce: Duration.zero,
          minimumQueryLength: 2,
        ),
      );
      final states = <SearchUpdate>[];
      session.updates.listen(states.add);
      session.search('');
      session.search('x');
      expect(states.map((state) => state.phase), [
        SearchUpdatePhase.idle,
        SearchUpdatePhase.idle,
      ]);
      expect(calls, 0);
      session.search('xy');
      await active.future;
      await session.dispose();
      await Future<void>.delayed(Duration.zero);
      expect(canceled, isTrue);
      expect(calls, 1);
    },
  );
}

LanternClient _client(connect.Transport transport) => LanternClient.connect(
  Uri.parse('https://example.test'),
  transport: transport,
);
