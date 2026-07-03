package dev.lantern.baseline;

import com.google.gson.Gson;
import com.google.gson.GsonBuilder;

import org.apache.lucene.analysis.Analyzer;
import org.apache.lucene.analysis.cjk.CJKAnalyzer;
import org.apache.lucene.analysis.en.EnglishAnalyzer;
import org.apache.lucene.analysis.ja.JapaneseAnalyzer;
import org.apache.lucene.analysis.standard.StandardAnalyzer;
import org.apache.lucene.document.Document;
import org.apache.lucene.document.Field;
import org.apache.lucene.document.StringField;
import org.apache.lucene.document.TextField;
import org.apache.lucene.index.DirectoryReader;
import org.apache.lucene.index.IndexReader;
import org.apache.lucene.index.IndexWriter;
import org.apache.lucene.index.IndexWriterConfig;
import org.apache.lucene.index.StoredFields;
import org.apache.lucene.queryparser.classic.QueryParser;
import org.apache.lucene.search.IndexSearcher;
import org.apache.lucene.search.Query;
import org.apache.lucene.search.ScoreDoc;
import org.apache.lucene.search.TopDocs;
import org.apache.lucene.search.similarities.BM25Similarity;
import org.apache.lucene.store.ByteBuffersDirectory;
import org.apache.lucene.store.Directory;
import org.apache.lucene.util.Version;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Instant;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.function.Supplier;

/**
 * BaselineRunner replays the golden relevance corpora
 * (core/search/relevance/testdata/{en,ja,mixed}.json) through stock Lucene
 * BM25 and records, for every query, the top-K document ids in ranked order.
 * The Go side (core/search/relevance, TestLuceneBaselineComparison) computes
 * the metrics from these runs with the exact same metric functions Lantern is
 * scored with, so the two engines can never drift apart formula-wise.
 *
 * <p>Engine configuration is deliberately "what you get out of the box":
 * default BM25Similarity (k1 = 1.2, b = 0.75), the classic QueryParser with
 * its default OR operator and the query text escaped (queries are plain text,
 * not Lucene syntax), and the stock analyzers per corpus — StandardAnalyzer
 * and EnglishAnalyzer for en, CJKAnalyzer (the bigram baseline the epic's
 * definition of done is measured against, #886) and kuromoji's
 * JapaneseAnalyzer (a stretch reference, non-blocking) for ja and mixed.
 */
public final class BaselineRunner {

    /** Ranking depth recorded per query; mirrors relevance.EvalDepth. */
    private static final int TOP_K = 50;

    public static void main(String[] args) throws Exception {
        if (args.length != 2) {
            System.err.println("usage: BaselineRunner <corpora-dir> <output-json>");
            System.exit(2);
        }
        Path dataDir = Path.of(args[0]);
        Path outPath = Path.of(args[1]);

        Map<String, List<AnalyzerSpec>> plan = new LinkedHashMap<>();
        plan.put("en", List.of(
                new AnalyzerSpec("standard", "StandardAnalyzer", StandardAnalyzer::new),
                new AnalyzerSpec("english", "EnglishAnalyzer", EnglishAnalyzer::new)));
        plan.put("ja", List.of(
                new AnalyzerSpec("cjk", "CJKAnalyzer", CJKAnalyzer::new),
                new AnalyzerSpec("kuromoji", "JapaneseAnalyzer", JapaneseAnalyzer::new)));
        plan.put("mixed", List.of(
                new AnalyzerSpec("cjk", "CJKAnalyzer", CJKAnalyzer::new),
                new AnalyzerSpec("kuromoji", "JapaneseAnalyzer", JapaneseAnalyzer::new)));

        Gson gson = new GsonBuilder().disableHtmlEscaping().setPrettyPrinting().create();

        Map<String, Object> out = new LinkedHashMap<>();
        out.put("engine", "lucene-" + Version.LATEST);
        out.put("generated", Instant.now().toString());
        Map<String, Object> runs = new LinkedHashMap<>();
        out.put("runs", runs);

        for (Map.Entry<String, List<AnalyzerSpec>> entry : plan.entrySet()) {
            String corpusName = entry.getKey();
            Corpus corpus = readCorpus(gson, dataDir.resolve(corpusName + ".json"));
            for (AnalyzerSpec spec : entry.getValue()) {
                Map<String, List<String>> results = rank(corpus, spec.analyzer.get());
                Map<String, Object> run = new LinkedHashMap<>();
                run.put("corpus", corpusName);
                run.put("analyzer", spec.label);
                run.put("results", results);
                runs.put(corpusName + "-" + spec.name, run);
                System.err.printf("ran %s-%s: %d queries%n", corpusName, spec.name, results.size());
            }
        }

        Files.writeString(outPath, gson.toJson(out) + "\n", StandardCharsets.UTF_8);
        System.err.println("wrote " + outPath);
    }

    /** rank indexes the corpus and returns each query's top-K doc ids. */
    private static Map<String, List<String>> rank(Corpus corpus, Analyzer analyzer) throws Exception {
        try (Directory dir = new ByteBuffersDirectory()) {
            IndexWriterConfig cfg = new IndexWriterConfig(analyzer);
            cfg.setSimilarity(new BM25Similarity());
            try (IndexWriter writer = new IndexWriter(dir, cfg)) {
                for (Doc d : corpus.docs) {
                    Document doc = new Document();
                    doc.add(new StringField("id", d.id, Field.Store.YES));
                    doc.add(new TextField("text", d.text, Field.Store.NO));
                    writer.addDocument(doc);
                }
            }
            try (IndexReader reader = DirectoryReader.open(dir)) {
                IndexSearcher searcher = new IndexSearcher(reader);
                searcher.setSimilarity(new BM25Similarity());
                StoredFields stored = reader.storedFields();
                QueryParser parser = new QueryParser("text", analyzer);

                Map<String, List<String>> results = new LinkedHashMap<>();
                for (QueryDef q : corpus.queries) {
                    // Escape first: query text is plain language, and characters
                    // like the hyphen in "b-tree" or braces in JSON-ish queries
                    // must reach the analyzer, not the query syntax.
                    Query query = parser.parse(QueryParser.escape(q.text));
                    TopDocs top = searcher.search(query, TOP_K);
                    List<String> ids = new ArrayList<>(top.scoreDocs.length);
                    for (ScoreDoc sd : top.scoreDocs) {
                        ids.add(stored.document(sd.doc).get("id"));
                    }
                    results.put(q.id, ids);
                }
                return results;
            }
        }
    }

    private static Corpus readCorpus(Gson gson, Path path) throws IOException {
        Corpus corpus = gson.fromJson(Files.readString(path, StandardCharsets.UTF_8), Corpus.class);
        if (corpus == null || corpus.docs == null || corpus.queries == null) {
            throw new IOException("malformed corpus: " + path);
        }
        return corpus;
    }

    /** One engine configuration to run a corpus through. */
    private static final class AnalyzerSpec {
        final String name;
        final String label;
        final Supplier<Analyzer> analyzer;

        AnalyzerSpec(String name, String label, Supplier<Analyzer> analyzer) {
            this.name = name;
            this.label = label;
            this.analyzer = analyzer;
        }
    }

    // Fixture mirror of relevance.Corpus; qrels are ignored here on purpose —
    // judging stays on the Go side, this runner only records rankings.
    private static final class Corpus {
        List<Doc> docs;
        List<QueryDef> queries;
    }

    private static final class Doc {
        String id;
        String text;
    }

    private static final class QueryDef {
        String id;
        String text;
    }

    private BaselineRunner() {
    }
}
