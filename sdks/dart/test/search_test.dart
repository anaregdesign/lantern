import 'dart:async';

import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.connect.spec.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.pb.dart' as graph;
import 'package:test/test.dart';

void main() {
  test(
    'one-shot search preserves every wire option and ranked result',
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
          phrase: true,
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
      expect(captured.options.phrase, isTrue);
      expect(captured.options.fuzziness, 2);
      expect(captured.options.prefixTerms, isTrue);
    },
  );

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

  test('search-disabled is a calm typed result', () async {
    final transport = FakeTransportBuilder()
        .unary<graph.SearchVerticesRequest, graph.SearchVerticesResponse>(
          LanternService.searchVertices,
          (request, context) => throw connect.ConnectException(
            connect.Code.failedPrecondition,
            'search is disabled',
          ),
        )
        .build();
    final result = await _client(transport).searchVertices('q');
    expect(result, isA<SearchDisabled>());
    expect(
      (result as SearchDisabled).cause,
      isA<LanternFailedPreconditionException>(),
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
      const SearchOptions(
        matchMode: SearchMatchMode.minShouldMatch,
        minShouldMatch: 0,
      ),
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
      final first = Completer<graph.SearchVerticesResponse>();
      final transport = FakeTransportBuilder()
          .unary<graph.SearchVerticesRequest, graph.SearchVerticesResponse>(
            LanternService.searchVertices,
            (request, context) {
              if (request.query == 'old') {
                firstStarted.complete();
                return first.future;
              }
              return graph.SearchVerticesResponse(
                hits: [graph.SearchHit(key: 'new', score: 2)],
              );
            },
          )
          .build();
      final session = _client(transport).incrementalSearch(
        options: const IncrementalSearchOptions(debounce: Duration.zero),
      );
      addTearDown(session.dispose);
      final delivered = <SearchUpdate>[];
      final subscription = session.updates.listen(delivered.add);
      addTearDown(subscription.cancel);

      session.search('old');
      await firstStarted.future;
      session.search('new');
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
