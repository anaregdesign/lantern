# lantern: An graph based key-vertex-store
![lantern](https://github.com/anaregdesign/lantern/assets/6128022/d0484704-707d-4dcb-b780-4bbd318c444c)


In recent years, many applications, recommender, fraud detection, SNS ... are based on a graph structure. 
And these applications have got to be more and more real-time and dynamic.
There are so many graph-based database, but almost all of them are not optimized for online applications or backend for web apps.
We've just needed a simple graph structure, but not highly theorized algorithms such as ontology, global shortest path, etc.

Lantern is a in-memory `key-vertex-store` which is optimized for online applications. It behaves like a key-value store, but it can explore neighbor vertices(values) based on graph structure.

Lantern is a online-transactional data store. All vertices or edges will be expired as time passes, just like a relationship in the real world.

Lantern is a grpc-based application. We can access lantern from any languages which supports grpc.

## Related Projects
- [lantern](https://github.com/anaregdesign/lantern): this repository
- [lantern-proto](https://github.com/anaregdesign/lantern-proto): protobuf definitions for lantern
- [lantern-cli](https://github.com/anaregdesign/lantern-cli): CLI for lantern
- [papaya](https://github.com/anaregdesign/papaya): Core algorithm and utilities for lantern

## Example
### Run lantern-server
We can run lantern-server with docker. Kick the following command and lantern-server will be running on `localhost:6380`.
```shell
docker run -p 6380:6380 ghcr.io/anaregdesign/lantern:v0.4.2
```

### Install lantern-cli
Binaries are available on [releases](https://github.com/anaregdesign/lantern-cli/releases) page.

If you want to build lantern-cli manually, run the following commands.
```shell
git clone https://github.com/anaregdesign/lantern-cli.git
cd lantern-cli
go build
./lantern-cli --host localhost --port 6380
```

### Put vertices and edges
So, let's put simple graph like this into lantern. 

![Asset 5](https://github.com/anaregdesign/lantern/assets/6128022/bdac71a9-d860-4a27-8bb7-3c5442d8d5f4)

We can put vertices and edges with `put` command.

```shell
./lantern-cli --host localhost --port 6380
> put vertex a A
OK (454.695µs)
> put vertex b B
OK (768.012µs)
> put edge a b 1
OK (642.748µs)
```
You know, `put vertex` command takes 3 arguments, `key`, `value` and `ttl`. `ttl` is optional. If you don't set `ttl`, lantern-cli will set `ttl` to 1 year.
And `put edge` command takes 4 arguments, `tail`, `head`, `weight` and `ttl`. `ttl` is optional, too.

```shell
put vertex <key:string> <value:string> [<ttl:int>]
put edge <tail:string> <head:string> <weight:float> [<ttl:int>]
```

```shell

And we can get vertices and edges with `get` command.

```shell
> get vertex a
{"String_":"A"}
OK (768.012µs)
> get edge a b
1.000000
OK (591.764µs)
```
`get vertex` command takes 1 argument, `key`. And `get edge` command takes 2 arguments, `tail` and `head`.

```shell
get vertex <key:string>
get edge <tail:string> <head:string>
```

If you want to explore neighbor vertices, use `illuminate neighbor` command. 

`illuminate neighbor` command takes 4 arguments, `seed`, `step`, `k` and `tfidf`. And you don't have to consider about `tfidf` for now.

```shell
illuminate neighbor <seed:string> <step:int> <k:int> <tfidf:bool>
```
* `seed`: key of seed vertex
* `step`: number of steps to explore
* `k`: maximum number of neighbors to explore from each vertices. If you set `k` to 3, top 3 neighbors will be explored from each vertices.
* `tfidf`: selection rules of top `k` neighbors. Once you set `tfidf` to `true`, top `k` neighbors will be selected by TF-IDF algorithm. If you set `tfidf` to `false`, top `k` neighbors will be selected by weight of edges.

e.g.
```shell
> illuminate neighbor a 1 1 false
{
    "vertices": {
        "a": {
            "Value": {
                "String_": "A"
            }
        },
        "b": {
            "Value": {
                "String_": "B"
            }
        }
    },
    "edges": {
        "a": {
            "b": 1
        }
    }
}
```

Then we got seed vertex `a`, its adjacent vertex `b` and weight of the edge `a -> b` 1.0.


### Exploring graph structure: Extract subgraph
Let's put more vertices and edges like this.

![Asset 6](https://github.com/anaregdesign/lantern/assets/6128022/c1a35db5-a230-4b66-a24f-372ded1f814c)

`illuminate` command extracts subgraph from whole graph.

Once you set `step` to 1, you can explore subgraph, seed vertex and its adjacent vertices and its edges.

```shell
> illuminate neighbor a 1 2 false
{
	"vertices": {
		...
	},
	"edges": {
		"a": {
			"b": 1,
			"c": 1
		}
	}
}
```
![Asset 9](https://github.com/anaregdesign/lantern/assets/6128022/486e892e-a3c3-4cf3-bcb7-501db6cfed13)


### Exploring graph structure: shortest path tree
First parameter of `illuminate` can be `neighbor`, `spt_cost`, `spt_relevance`, `mst_cost` or `mst_relevance`. And `spt` means shortest path tree, mst means minimum spanning tree.

So, you can explore shortest-path tree from a seed vertex with `illuminate spt_cost` or `illuminate spt_relevance` command.

If you set a target as `spt_cost`, lantern will calculate shortest-path tree with a weight of edges.

```shell
> illuminate spt_cost a 2 2 false
{
	"vertices": {
		...
	},
	"edges": {
		"a": {
			"b": 1
		},
		"b": {
			"c": 1
		},
		"c": {
			"d": 1
		}
	}
}

```

![Asset 7](https://github.com/anaregdesign/lantern/assets/6128022/14843e9f-53b3-4bb9-9dd6-51c60f020aff)

If you set a target as `spt_relevance`, lantern will calculate shortest-path tree with inverse of weight `1 / weight`.

```shell
> illuminate spt_relevance a 2 2 false

{
	"vertices": {
		...
	},
	"edges": {
		"a": {
			"b": 1,
			"c": 3
		},
		"b": {
			"d": 4
		}
	}
}
```

![Asset 8](https://github.com/anaregdesign/lantern/assets/6128022/4c5d6606-5266-4df9-8a8d-7a617e3a672a)

## SDK
### Golang
This is short example of how to use lantern in Golang [[source](https://github.com/anaregdesign/lantern/blob/main/client/example/main.go)].
