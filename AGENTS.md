# Lantern — Agent Instructions

Lantern は in-memory `key-vertex-store`(グラフベース KVS)。gRPC サーバとして起動し、頂点・辺に TTL を持たせて時間と共に減衰させる。詳細は [README.md](README.md) 参照。

## 🎯 現在の最優先方針: マルチレポ → モノレポ集約

このリポジトリは過去、以下 4 レポジトリに分かれて開発されていた。今後は **本リポジトリに段階的に取り込み統合する** ことが最優先方針:

| 旧リポ | 現在の依存先 | 役割 | 集約後の想定パス |
|---|---|---|---|
| `anaregdesign/lantern` (本リポ) | — | gRPC サーバ + Go クライアント SDK | `server/`, `client/` (既存) |
| [`anaregdesign/lantern-proto`](https://github.com/anaregdesign/lantern-proto) | `go.mod` 経由で取り込み | `.proto` 定義と生成済み Go コード | `proto/`, `gen/go/` (予定) |
| [`anaregdesign/lantern-cli`](https://github.com/anaregdesign/lantern-cli) | 別バイナリ | 対話 CLI (`put` / `get` / `illuminate`) | `cli/` (予定) |
| [`anaregdesign/papaya`](https://github.com/anaregdesign/papaya) | `go.mod` 経由 | グラフ・キャッシュ・NLP 等のコアアルゴリズム | `internal/papaya/` か `pkg/graph/` (予定) |

**作業の進め方:**
- 集約は一気にやらず、PR を **レポ単位で分割**(例: まず lantern-proto → 次に papaya → 最後に lantern-cli)。
- 取り込み時は **commit history を維持** するのが望ましい(`git subtree add` または `git filter-repo` を検討。ユーザに方針確認すること)。
- 取り込み後は `go.mod` から該当 `require` を外し、import path を `github.com/anaregdesign/lantern/...` 配下へ書き換える。**全テスト & `go build ./...` が通るところまで** を 1 PR の単位とする。
- `lantern-proto` を取り込んだ段階で `buf` / `protoc` の生成系を Makefile に追加すること。
- 集約計画を変更する場合は本セクションを必ず更新する。

## アーキテクチャ要点

- **gRPC service**: [server/service/service.go](server/service/service.go) が `LanternService`(`Illuminate`, `GetVertex`, `PutVertex`, `AddEdge`, `PutEdge`, `DeleteVertex`, `DeleteEdge`)を実装。
- **DI**: [google/wire](https://github.com/google/wire) を使用。[server/cmd/wire.go](server/cmd/wire.go) が定義、[server/cmd/wire_gen.go](server/cmd/wire_gen.go) は生成物 — **手で編集しない**。プロバイダ追加時は `make` で再生成。
- **Provider**: [server/provider/provider.go](server/provider/provider.go) が `Config`(env vars `LANTERN_PORT`, `LANTERN_DEFAULT_TTL_SECONDS`)、`net.Listener`、`grpc.Server`、`papaya/cache/graph.GraphCache` を組み立てる。
- **クライアント SDK**: [client/client.go](client/client.go) は薄い gRPC ラッパ。[client/value.go](client/value.go) は Go の native 型 ↔ `pb.Vertex` の変換を `nativeVertex.asVertex()` と `Vertex.*Value()` でハンドル。**新しい値型を追加する場合は両方向**(`asVertex` と各 `*Value()` メソッド)を必ず更新。
- **減衰モデル**: edge は **加算的(additive)** で個別 TTL を持つ。`AddEdge` と `PutEdge` の挙動差(冪等性)に注意 — [client/example/main.go](client/example/main.go) の説明参照。

## ビルド / 実行 / テスト

```bash
go build -v ./...                # ビルド(CI と同じ)
go test -v ./...                 # テスト(現状 client/value_test.go のみ)
make                             # wire コード再生成(google/wire が必要: go install github.com/google/wire/cmd/wire@latest)
go run ./server/cmd              # ローカル起動 (:6380)
docker build -t lantern .        # コンテナビルド (Go 1.20.4-alpine)
```

CI: [.github/workflows/go.yml](.github/workflows/go.yml) が PR/push で `go build` + `go test`、[.github/workflows/docker-publish.yml](.github/workflows/docker-publish.yml) が `v*.*.*` タグ push で ghcr.io へ publish + cosign 署名。

## 規約・落とし穴

- **Go version**: `go.mod` は `1.20`、Dockerfile は `1.20.4-alpine3.17`。集約・モダナイズ時は Go 本体も更新するか方針を確認(`go.mod` / Dockerfile / `setup-go` 3 箇所同時)。
- **依存はピン留め**: `lantern-proto v0.4.1`, `papaya v0.4.0`。集約完了までこれらをむやみに上げない(壊れた場合 fork 元に PR を出すか集約を先に進める)。
- **テスト不足**: server/service レイヤ・wire 配線・client 通信パスにテストが無い。新規変更を入れる場合は **同じ PR で最低限のテーブルテストを追加** することを推奨。
- **wire の generic 制限**: `service.go` の `// Avoiding bug of 'wire'. Generic type is not supported.` コメントが示すとおり、wire は generic 型引数を扱えないため、`GraphCache[string, *Vertex]` を直接 provider から返す形になっている。集約時に generic 化したくなったらまずこの制約を確認。
- **`server/provider/provider.go` のバグ候補**: `NewListener` が `NewConfig()` を再度呼んでおり、`NewConfig` の wire 結果を使っていない。集約や refactor のついでに `*Config` を受け取る形に直すと良い(挙動は同等だが env 読み直しが発生)。

## ドキュメント / リンク

- 使い方の全体像: [README.md](README.md)
- クライアント SDK の網羅例: [client/example/main.go](client/example/main.go)
- 旧レポ: [lantern-proto](https://github.com/anaregdesign/lantern-proto) / [lantern-cli](https://github.com/anaregdesign/lantern-cli) / [papaya](https://github.com/anaregdesign/papaya)
