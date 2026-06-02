# Lantern — Agent Instructions

Lantern は in-memory `key-vertex-store`(グラフベース KVS)。gRPC サーバとして起動し、頂点・辺に TTL を持たせて時間と共に減衰させる。詳細は [README.md](README.md) 参照。

## モノレポ構成

過去は 4 リポジトリ(`lantern` / `lantern-proto` / `lantern-cli` / `papaya`)に分かれていたが、**本リポジトリに集約済み**。

| パス | 由来 | 役割 |
|---|---|---|
| `server/` | lantern (本リポ) | gRPC サーバ(google/wire で DI) |
| `client/` | lantern (本リポ) | Go クライアント SDK |
| `cli/` | 旧 [`lantern-cli`](https://github.com/anaregdesign/lantern-cli) | 対話 CLI (cobra + promptui) |
| `proto/` | 旧 [`lantern-proto`](https://github.com/anaregdesign/lantern-proto) | `.proto` ソース(buf で再生成) |
| `gen/go/` | 旧 `lantern-proto/go` | 生成済み Go バインディング |
| `pkg/papaya/` | 旧 [`papaya`](https://github.com/anaregdesign/papaya) | グラフ・キャッシュ・NLP コアアルゴリズム |

`go.mod` の module path は `github.com/anaregdesign/lantern` のまま。旧リポへの外部依存はすべて除去済み。

## アーキテクチャ要点

- **gRPC service**: [server/service/service.go](server/service/service.go) が `LanternService`(`Illuminate`, `GetVertex`, `PutVertex`, `AddEdge`, `PutEdge`, `DeleteVertex`, `DeleteEdge`)を実装。
- **DI**: [google/wire](https://github.com/google/wire) を使用。[server/cmd/wire.go](server/cmd/wire.go) が定義、[server/cmd/wire_gen.go](server/cmd/wire_gen.go) は生成物 — **手で編集しない**。プロバイダを追加・変更したら `make wire`(または `wire ./server/cmd`)で再生成。
- **Provider**: [server/provider/provider.go](server/provider/provider.go) が `Config`(env vars `LANTERN_PORT`, `LANTERN_DEFAULT_TTL_SECONDS`)、`net.Listener`、`grpc.Server`、`papaya/cache/graph.GraphCache` を組み立てる。`NewListener` は wire 経由で `*Config` を受け取る形に修正済み。
- **クライアント SDK**: [client/client.go](client/client.go) は薄い gRPC ラッパ。[client/value.go](client/value.go) は Go の native 型 ↔ `pb.Vertex` の変換を `nativeVertex.asVertex()` と `Vertex.*Value()` でハンドル。**新しい値型を追加する場合は両方向**(`asVertex` と各 `*Value()` メソッド)を必ず更新。
- **減衰モデル**: edge は **加算的(additive)** で個別 TTL を持つ。`AddEdge` と `PutEdge` の挙動差(冪等性)に注意 — [client/example/main.go](client/example/main.go) の説明参照。

## ビルド / 実行 / テスト / 生成

```bash
go build -v ./...                # ビルド(CI と同じ)
go test -v ./...                 # テスト
make wire                        # wire コード再生成(要 go install github.com/google/wire/cmd/wire@latest)
make proto                       # proto から Go コード再生成(要 buf)
go run ./server/cmd              # サーバ起動 (:6380)
go run ./cli                     # CLI 起動
docker build -t lantern .        # コンテナビルド (Go 1.26-alpine)
```

CI: [.github/workflows/go.yml](.github/workflows/go.yml) が PR/push で `go build` + `go test`(Go 1.26、actions/checkout@v4、setup-go@v5)。[.github/workflows/docker-publish.yml](.github/workflows/docker-publish.yml) が `v*.*.*` タグ push で ghcr.io へ publish + cosign keyless 署名(cosign v2、`--yes`)。

## 規約・落とし穴

- **Go version**: `go.mod` は `1.26`、Dockerfile は `golang:1.26-alpine`。バージョンを上げる場合は `go.mod` / Dockerfile / `.github/workflows/go.yml` の `go-version` 3 箇所を同時に更新する。
- **wire の generic 制限**: `service.go` の `// Avoiding bug of 'wire'. Generic type is not supported.` コメントが示すとおり、wire は generic 型引数を扱えないため、`GraphCache[string, *Vertex]` を直接 provider から返す形になっている。generic 化したくなったら先にこの制約を確認。
- **proto 再生成**: `proto/graph/v1/graph.proto` の `option go_package` は `github.com/anaregdesign/lantern/gen/go/graph/v1`。`make proto` は `buf generate proto` を呼び `gen/go` 配下を作り直す。`buf.work.yaml` / `buf.gen.yaml` はリポルート。
- **テスト不足**: server/service レイヤ・wire 配線・client 通信パスにテストが無い。新規変更を入れる場合は **同じ PR で最低限のテーブルテストを追加** することを推奨。

## ドキュメント / リンク

- 使い方の全体像: [README.md](README.md)
- クライアント SDK の網羅例: [client/example/main.go](client/example/main.go)
