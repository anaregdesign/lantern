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
import org.apache.lucene.queryparser.classic.MultiFieldQueryParser;
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
import java.security.MessageDigest;
import java.time.Instant;
import java.util.ArrayList;
import java.util.HexFormat;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.function.Supplier;

/**
 * BaselineRunner indexes the provider-generated key/value fields for the
 * golden relevance corpora through stock Lucene BM25 and records, for every
 * query, the top-K document ids in ranked order. The Go side validates the
 * artifact provenance and computes both engines' metrics with the exact same
 * functions; the production comparison lives in server/provider so it can
 * invoke the unexported vertex projection without breaking core's leaf
 * dependency boundary.
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
                new AnalyzerSpec("standard", "StandardAnalyzer", true, StandardAnalyzer::new),
                new AnalyzerSpec("english", "EnglishAnalyzer", true, EnglishAnalyzer::new)));
        plan.put("ja", List.of(
                new AnalyzerSpec("cjk", "CJKAnalyzer", true, CJKAnalyzer::new),
                new AnalyzerSpec("kuromoji", "JapaneseAnalyzer", false, JapaneseAnalyzer::new)));
        plan.put("mixed", List.of(
                new AnalyzerSpec("cjk", "CJKAnalyzer", true, CJKAnalyzer::new),
                new AnalyzerSpec("kuromoji", "JapaneseAnalyzer", false, JapaneseAnalyzer::new)));

        Gson gson = new GsonBuilder().disableHtmlEscaping().setPrettyPrinting().create();
        Path projectedPath = dataDir.resolve("projected_fields.json");
        ProjectedFixture projectedFixture = gson.fromJson(
                Files.readString(projectedPath, StandardCharsets.UTF_8), ProjectedFixture.class);

        Map<String, Object> out = new LinkedHashMap<>();
        out.put("engine", "lucene-" + Version.LATEST);
        out.put("generated", Instant.now().toString());
        Map<String, String> hashes = new LinkedHashMap<>();
        for (String corpusName : plan.keySet()) {
            Path path = dataDir.resolve(corpusName + ".json");
            hashes.put("testdata/" + corpusName + ".json", sha256(path));
        }
        Map<String, Object> provenance = new LinkedHashMap<>();
        provenance.put("corpus_sha256", hashes);
        provenance.put("projected_fields_sha256", sha256(projectedPath));
        provenance.put("fixture_format", "typed-string-vertex-fields-v1");
        provenance.put("projection_version", "vertex-fields-v2");
        provenance.put("analyzer_version", "script-aware-v2");
        provenance.put("scorer_config", "field-weighted-bm25:k1=1.2,b=0.75,key=1.75,value=1,gram=0.2,proximity=0.3");
        provenance.put("generation_command", "testbed/lucene-baseline/run.sh");
        out.put("provenance", provenance);
        Map<String, Object> runs = new LinkedHashMap<>();
        out.put("runs", runs);

        for (Map.Entry<String, List<AnalyzerSpec>> entry : plan.entrySet()) {
            String corpusName = entry.getKey();
            Corpus corpus = readCorpus(gson, dataDir.resolve(corpusName + ".json"));
            ProjectedCorpus projected = projectedFixture.corpora.get(corpusName);
            if (projected == null) {
                throw new IOException("projected fixture missing corpus: " + corpusName);
            }
            for (AnalyzerSpec spec : entry.getValue()) {
                Map<String, List<String>> results = rank(corpus, projected, spec.analyzer.get());
                Map<String, Object> run = new LinkedHashMap<>();
                run.put("corpus", corpusName);
                run.put("analyzer", spec.label);
                run.put("blocking", spec.blocking);
                run.put("results", results);
                runs.put(corpusName + "-" + spec.name, run);
                System.err.printf("ran %s-%s: %d queries%n", corpusName, spec.name, results.size());
            }
        }

        Files.writeString(outPath, gson.toJson(out) + "\n", StandardCharsets.UTF_8);
        System.err.println("wrote " + outPath);
    }

    /** rank indexes the corpus and returns each query's top-K doc ids. */
    private static Map<String, List<String>> rank(
            Corpus corpus, ProjectedCorpus projected, Analyzer analyzer) throws Exception {
        try (Directory dir = new ByteBuffersDirectory()) {
            IndexWriterConfig cfg = new IndexWriterConfig(analyzer);
            cfg.setSimilarity(new BM25Similarity());
            try (IndexWriter writer = new IndexWriter(dir, cfg)) {
                for (ProjectedDoc d : projected.docs) {
                    Document doc = new Document();
                    doc.add(new StringField("id", d.id, Field.Store.YES));
                    for (ProjectedField field : d.fields) {
                        doc.add(new TextField(field.name, field.text, Field.Store.NO));
                    }
                    writer.addDocument(doc);
                }
            }
            try (IndexReader reader = DirectoryReader.open(dir)) {
                IndexSearcher searcher = new IndexSearcher(reader);
                searcher.setSimilarity(new BM25Similarity());
                StoredFields stored = reader.storedFields();
                Map<String, Float> boosts = Map.of("key", 1.75f, "value", 1.0f);
                QueryParser parser = new MultiFieldQueryParser(
                        new String[]{"key", "value"}, analyzer, boosts);

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

    private static String sha256(Path path) throws Exception {
        byte[] raw = Files.readAllBytes(path);
        return HexFormat.of().formatHex(
                MessageDigest.getInstance("SHA-256").digest(raw));
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
        final boolean blocking;
        final Supplier<Analyzer> analyzer;

        AnalyzerSpec(String name, String label, boolean blocking, Supplier<Analyzer> analyzer) {
            this.name = name;
            this.label = label;
            this.blocking = blocking;
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

    private static final class ProjectedFixture {
        Map<String, ProjectedCorpus> corpora;
    }

    private static final class ProjectedCorpus {
        List<ProjectedDoc> docs;
    }

    private static final class ProjectedDoc {
        String id;
        List<ProjectedField> fields;
    }

    private static final class ProjectedField {
        String name;
        String text;
    }

    private BaselineRunner() {
    }
}
