use std::collections::BTreeMap;

use crate::{
    CallOptions, Edge, LanternClient, LanternError, Objective, Reduction, Vertex, Weighting,
    generated::graph::v1::{
        BfsParams, IlluminateRequest, IlluminateResponse, LocalCommunityParams, PprParams,
        illuminate_request::Params,
    },
    transport::RetryClass,
    value::validate_key,
};

/// A typed traversal family. PPR returns a synthetic relevance star; BFS and
/// community return real graph edges, potentially with transformed weights.
#[derive(Clone, Debug, PartialEq)]
pub enum TraversalFamily {
    Bfs(BfsOptions),
    Ppr(PprOptions),
    LocalCommunity(CommunityOptions),
}

#[derive(Clone, Debug, PartialEq)]
pub struct TraversalOptions {
    pub family: TraversalFamily,
    pub weighting: Weighting,
    pub vertex_prefix: String,
}

impl TraversalOptions {
    pub fn bfs(step: u32, fan_out: u32) -> Self {
        Self {
            family: TraversalFamily::Bfs(BfsOptions::new(step, fan_out)),
            weighting: Weighting::Unspecified,
            vertex_prefix: String::new(),
        }
    }

    pub fn ppr(top_n: u32) -> Self {
        Self {
            family: TraversalFamily::Ppr(PprOptions {
                top_n,
                ..PprOptions::default()
            }),
            weighting: Weighting::Unspecified,
            vertex_prefix: String::new(),
        }
    }

    pub fn community(max_size: u32) -> Self {
        Self {
            family: TraversalFamily::LocalCommunity(CommunityOptions {
                max_size,
                ..CommunityOptions::default()
            }),
            weighting: Weighting::Unspecified,
            vertex_prefix: String::new(),
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq)]
pub struct BfsOptions {
    pub step: u32,
    pub fan_out: u32,
    pub objective: Objective,
    pub reduction: Reduction,
}

impl BfsOptions {
    pub fn new(step: u32, fan_out: u32) -> Self {
        Self {
            step,
            fan_out,
            objective: Objective::Unspecified,
            reduction: Reduction::Unspecified,
        }
    }
}

#[derive(Clone, Copy, Debug, Default, PartialEq)]
pub struct PprOptions {
    /// Zero requests the server's default traversal result cap.
    pub top_n: u32,
    /// Zero selects the server's default restart probability.
    pub restart_prob: f32,
    /// Zero selects the server's default residual threshold.
    pub epsilon: f32,
}

#[derive(Clone, Copy, Debug, PartialEq)]
pub struct CommunityOptions {
    /// Zero requests the server's default traversal result cap.
    pub max_size: u32,
    pub restart_prob: f32,
    pub epsilon: f32,
    pub reduction: Reduction,
    pub objective: Objective,
}

impl Default for CommunityOptions {
    fn default() -> Self {
        Self {
            max_size: 0,
            restart_prob: 0.0,
            epsilon: 0.0,
            reduction: Reduction::Unspecified,
            objective: Objective::Unspecified,
        }
    }
}

/// A PPR link is not a stored edge. It has a relevance mass, not an
/// expiration; real BFS/community edges retain their protobuf expiration.
#[derive(Clone, Debug, PartialEq)]
pub enum GraphEdge {
    Traversed(Edge),
    PprRelevance { mass: f32 },
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct Graph {
    pub vertices: BTreeMap<String, Vertex>,
    pub edges: BTreeMap<String, BTreeMap<String, GraphEdge>>,
}

impl LanternClient {
    /// Fetch one server-computed subgraph. Traversal has one unary deadline
    /// and never silently switches between the three algorithm families.
    pub async fn illuminate(
        &self,
        seed: impl Into<String>,
        options: TraversalOptions,
    ) -> Result<Graph, LanternError> {
        self.illuminate_with_options(seed, options, CallOptions::Default)
            .await
    }

    pub async fn illuminate_with_options(
        &self,
        seed: impl Into<String>,
        options: TraversalOptions,
        call_options: CallOptions,
    ) -> Result<Graph, LanternError> {
        let seed = seed.into();
        let request = traversal_request(&seed, &options)?;
        let response = self
            .data_unary(request, call_options, RetryClass::ReadOnly, |svc, req| {
                Box::pin(svc.illuminate(req))
            })
            .await?;
        decode_graph(response, &seed, &options.family)
    }
}

fn traversal_request(
    seed: &str,
    options: &TraversalOptions,
) -> Result<IlluminateRequest, LanternError> {
    validate_key(seed).map_err(LanternError::InvalidInput)?;
    let params = match &options.family {
        TraversalFamily::Bfs(bfs) => {
            if bfs.step == 0 || bfs.fan_out == 0 {
                return Err(LanternError::InvalidInput(
                    "BFS step and fan_out must be positive",
                ));
            }
            Params::Bfs(BfsParams {
                step: bfs.step,
                fan_out: bfs.fan_out,
                objective: bfs.objective.into(),
                reduction: bfs.reduction.into(),
            })
        }
        TraversalFamily::Ppr(ppr) => {
            validate_push_knobs(ppr.restart_prob, ppr.epsilon)?;
            Params::Ppr(PprParams {
                top_n: ppr.top_n,
                restart_prob: ppr.restart_prob,
                epsilon: ppr.epsilon,
            })
        }
        TraversalFamily::LocalCommunity(community) => {
            validate_push_knobs(community.restart_prob, community.epsilon)?;
            Params::Community(LocalCommunityParams {
                max_size: community.max_size,
                restart_prob: community.restart_prob,
                epsilon: community.epsilon,
                reduction: community.reduction.into(),
                objective: community.objective.into(),
            })
        }
    };
    Ok(IlluminateRequest {
        seed: seed.into(),
        weighting: options.weighting.into(),
        vertex_prefix: options.vertex_prefix.clone(),
        params: Some(params),
    })
}

fn validate_push_knobs(restart_prob: f32, epsilon: f32) -> Result<(), LanternError> {
    if !restart_prob.is_finite() || restart_prob != 0.0 && !(0.0..1.0).contains(&restart_prob) {
        return Err(LanternError::InvalidInput(
            "restart_prob must be zero (server default) or strictly between zero and one",
        ));
    }
    if !epsilon.is_finite() || epsilon < 0.0 {
        return Err(LanternError::InvalidInput(
            "epsilon must be zero (server default) or positive",
        ));
    }
    Ok(())
}

fn decode_graph(
    response: IlluminateResponse,
    seed: &str,
    family: &TraversalFamily,
) -> Result<Graph, LanternError> {
    let wire = response
        .graph
        .ok_or(LanternError::Protocol("traversal response has no graph"))?;
    let mut graph = Graph::default();
    for vertex in wire.vertices {
        vertex.validate_response()?;
        if graph.vertices.insert(vertex.key.clone(), vertex).is_some() {
            return Err(LanternError::Protocol("duplicate traversal vertex"));
        }
    }
    for edge in wire.edges {
        edge.validate_response()?;
        if !graph.vertices.contains_key(&edge.tail) || !graph.vertices.contains_key(&edge.head) {
            return Err(LanternError::Protocol(
                "traversal edge endpoint is absent from graph",
            ));
        }
        let tail = edge.tail.clone();
        let head = edge.head.clone();
        let link = match family {
            TraversalFamily::Ppr(_) => {
                if edge.tail != seed || edge.expiration.is_some() || !edge.weight.is_finite() {
                    return Err(LanternError::Protocol(
                        "invalid synthetic PPR relevance link",
                    ));
                }
                GraphEdge::PprRelevance { mass: edge.weight }
            }
            TraversalFamily::Bfs(_) | TraversalFamily::LocalCommunity(_) => {
                GraphEdge::Traversed(edge)
            }
        };
        if graph
            .edges
            .entry(tail)
            .or_default()
            .insert(head, link)
            .is_some()
        {
            return Err(LanternError::Protocol("duplicate traversal edge"));
        }
    }
    Ok(graph)
}

#[cfg(test)]
mod tests;
