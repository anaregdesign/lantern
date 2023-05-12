# lantern: key-vertex-store
![lantern](https://github.com/anaregdesign/lantern/assets/6128022/d0484704-707d-4dcb-b780-4bbd318c444c)


In recent years, many applications, recommender, fraud detection, SNS ... are based on a graph structure. 
And these applications have got to be more and more real-time and dynamic.
There are so many graph-based database, but almost all of them is not suitable for real-time applications or backend for web apps.

We've just needed a simple graph structure, but not highly theorized algorithms such as ontology, global shortest path, etc.
Lantern is a in-memory `key-vertex-store` for real-time graph applications. It behaves like a key-value store, but it can explore neighbor vertices(values) based on graph structure.

Lantern is a online-transactional data store. All vertices or edges will be expired as time passes, just like a relationship in the real world.

## Related Projects
- [lantern](https://github.com/anaregdesign/lantern): this repository
- [lantern-proto](https://github.com/anaregdesign/lantern-proto): protobuf definitions for lantern
- [lantern-cli](https://github.com/anaregdesign/lantern-cli): CLI for lantern
- [papaya](https://github.com/anaregdesign/papaya): Core algorithm and utilities for lantern

## Example
### Running lantern-server
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


![Asset 1](https://github.com/anaregdesign/lantern/assets/6128022/3c685f41-e503-4e07-81aa-2c2a0ea94ba6)

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

And we can get vertices and edges with `get` command.

```shell
> get vertex a
{"String_":"A"}
OK (768.012µs)
> get edge a b
1.000000
OK (591.764µs)
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
> illuminate neighbor a 3 1 false
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


### Exploring graph structure: Shortest-path tree
Let's put more vertices and edges like this.

![Asset 2](https://github.com/anaregdesign/lantern/assets/6128022/32391225-d8f9-4fd8-98f4-57e1fc1d86be)

You can explore shortest-path tree from a seed vertex with `illuminate spt_cost` or `illuminate spt_relevance` command.

If you set a target as `spt_cost`, lantern will calculate shortest-path tree with a weight of edges.

```shell
illuminate spt_cost a 2 2 false
```

![Asset 3](https://github.com/anaregdesign/lantern/assets/6128022/30c614d1-d4ac-4136-982f-8464bda8797a)


If you set a target as `spt_relevance`, lantern will calculate shortest-path tree with inverse of weight `1 / weight`.

```shell
illuminate spt_relevance a 2 2 false
```

![Asset 4](https://github.com/anaregdesign/lantern/assets/6128022/6ea9c9a0-0a11-4d72-809c-0ef30f3f0d57)


## SDK
### Golang
This is short example of how to use lantern in Golang [[source](https://github.com/anaregdesign/lantern/blob/main/client/example/main.go)].
