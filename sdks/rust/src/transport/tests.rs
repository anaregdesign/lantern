use std::{
    error::Error,
    fs,
    io::Cursor,
    net::TcpListener,
    sync::{
        Mutex,
        atomic::{AtomicUsize, Ordering},
    },
};

use super::*;
use crate::{
    AddInput, BackupFormat, CdcCursor, ContribId, EdgeContributionRef, EdgeInput, PutOutcome,
    RestoreOptions, RpcErrorKind, SearchDetails, SearchErrorReason, Vertex, VertexInput,
    VertexValue,
    generated::graph::v1::{
        AddEdgeRequest, AddEdgesRequest, DeleteEdgeContributionsRequest, DeleteEdgesRequest,
        DeleteVertexRequest, DeleteVerticesRequest, EdgeContributionKey, EdgeKey,
        GetReceiptCapabilityRequest, GetReceiptCapabilityResponse, GetReceiptStatusesRequest,
        GetServerStatusRequest, GetServerStatusResponse, GetVertexRequest, GetVertexResponse,
        MutationReceiptContext, MutationReceiptState, PutVertexRequest, PutVertexResponse,
        PutVerticesRequest, SearchVerticesRequest, SearchVerticesResponse, receipt_result,
    },
    test_server::GoServer,
};
use prost::Message;
use rcgen::{
    BasicConstraints, CertificateParams, ExtendedKeyUsagePurpose, IsCa, KeyPair, KeyUsagePurpose,
};
use tempfile::TempDir;
use tokio::sync::Notify;
use tonic::codegen::Bytes;
use tonic_health::pb::HealthCheckResponse;
use tonic_types::pb::Status as RichStatus;

type TestResult = Result<(), Box<dyn Error>>;

struct RotatingToken {
    current: Mutex<String>,
    calls: AtomicUsize,
}

impl RotatingToken {
    fn new(initial: &str) -> Self {
        Self {
            current: Mutex::new(initial.into()),
            calls: AtomicUsize::new(0),
        }
    }

    fn rotate(&self, next: &str) {
        *self.current.lock().expect("token mutex poisoned") = next.into();
    }

    fn calls(&self) -> usize {
        self.calls.load(Ordering::SeqCst)
    }
}

impl TokenProvider for RotatingToken {
    fn token(&self) -> Pin<Box<dyn Future<Output = Result<String, TokenError>> + Send + '_>> {
        self.calls.fetch_add(1, Ordering::SeqCst);
        let token = self.current.lock().expect("token mutex poisoned").clone();
        Box::pin(async move { Ok(token) })
    }
}

async fn get_status(client: &LanternClient) -> Result<GetServerStatusResponse, LanternError> {
    client
        .data_unary(
            GetServerStatusRequest {},
            CallOptions::Default,
            RetryClass::ReadOnly,
            |service, request| Box::pin(service.get_server_status(request)),
        )
        .await
}

async fn get_vertex(
    client: &LanternClient,
    key: impl Into<String>,
) -> Result<GetVertexResponse, LanternError> {
    client
        .data_unary(
            GetVertexRequest { key: key.into() },
            CallOptions::Default,
            RetryClass::ReadOnly,
            |service, request| Box::pin(service.get_vertex(request)),
        )
        .await
}

async fn put_vertex(
    client: &LanternClient,
    vertex: Vertex,
) -> Result<PutVertexResponse, LanternError> {
    client
        .data_unary(
            PutVertexRequest {
                vertex: Some(vertex),
                if_absent: false,
                receipt_context: None,
            },
            CallOptions::Default,
            RetryClass::UnconditionalPut,
            |service, request| Box::pin(service.put_vertex(request)),
        )
        .await
}

async fn search(
    client: &LanternClient,
    query: impl Into<String>,
    cursor: Vec<u8>,
) -> Result<SearchVerticesResponse, LanternError> {
    client
        .data_unary(
            SearchVerticesRequest {
                query: query.into(),
                limit: 2,
                prefix: String::new(),
                options: None,
                cursor,
                projection: 0,
            },
            CallOptions::Default,
            RetryClass::ReadOnly,
            |service, request| Box::pin(service.search_vertices(request)),
        )
        .await
}

fn rpc_failure(error: LanternError) -> crate::RpcFailure {
    match error {
        LanternError::Rpc(failure) => failure,
        other => panic!("expected original gRPC failure, got {other}"),
    }
}

fn controlled_client(
    retry: RetryPolicy,
    provider: Option<Arc<dyn TokenProvider>>,
) -> LanternClient {
    LanternClient {
        state: Arc::new(ClientState {
            channel: Endpoint::from_static("http://127.0.0.1:1").connect_lazy(),
            endpoint_identity: "http://127.0.0.1:1".into(),
            unary_timeout: Duration::from_secs(15),
            encode_limit: DEFAULT_MESSAGE_LIMIT,
            decode_limit: DEFAULT_MESSAGE_LIMIT,
            batch_chunk_size: DEFAULT_BATCH_CHUNK_SIZE,
            server_max_batch_size: None,
            contribution_ids: None,
            token_provider: provider,
            retry,
        }),
    }
}

async fn scripted<R: Message + Clone + Send + 'static>(
    client: &LanternClient,
    request: R,
    retry_class: RetryClass,
    failures: Vec<Code>,
    options: CallOptions,
    payloads: Arc<Mutex<Vec<Vec<u8>>>>,
) -> Result<(), LanternError> {
    let failures = Arc::new(failures);
    client
        .data_unary::<_, GetServerStatusResponse, _>(
            request,
            options,
            retry_class,
            move |_service, request| {
                let failures = failures.clone();
                let payloads = payloads.clone();
                Box::pin(async move {
                    let attempt = {
                        let mut sent = payloads.lock().expect("payload mutex poisoned");
                        sent.push(request.get_ref().encode_to_vec());
                        sent.len() - 1
                    };
                    if let Some(code) = failures.get(attempt) {
                        Err(Status::new(*code, "scripted response"))
                    } else {
                        Ok(Response::new(GetServerStatusResponse::default()))
                    }
                })
            },
        )
        .await
        .map(|_| ())
}

fn attempts(payloads: &Arc<Mutex<Vec<Vec<u8>>>>) -> usize {
    payloads.lock().expect("payload mutex poisoned").len()
}

#[tokio::test]
async fn retry_only_unavailable_on_classified_methods_with_identical_payloads() {
    let payloads = Arc::new(Mutex::new(Vec::new()));
    let read = GetServerStatusRequest {};
    let disabled = controlled_client(RetryPolicy::Disabled, None);
    let failure = rpc_failure(
        scripted(
            &disabled,
            read,
            RetryClass::ReadOnly,
            vec![Code::Unavailable],
            CallOptions::Default,
            payloads.clone(),
        )
        .await
        .expect_err("retries are disabled by default"),
    );
    assert_eq!(failure.kind(), RpcErrorKind::Unavailable);
    assert_eq!(attempts(&payloads), 1);

    let enabled = controlled_client(RetryPolicy::Unavailable { max_attempts: 3 }, None);
    let payloads = Arc::new(Mutex::new(Vec::new()));
    scripted(
        &enabled,
        GetServerStatusRequest {},
        RetryClass::ReadOnly,
        vec![Code::Unavailable, Code::Unavailable],
        CallOptions::Default,
        payloads.clone(),
    )
    .await
    .expect("third read attempt succeeds");
    assert_eq!(attempts(&payloads), 3);
    assert!(
        payloads
            .lock()
            .expect("payload mutex poisoned")
            .windows(2)
            .all(|pair| pair[0] == pair[1])
    );

    let payloads = Arc::new(Mutex::new(Vec::new()));
    let put = PutVertexRequest {
        vertex: Some(Vertex {
            key: "rust:identical-put".into(),
            expiration: Some(crate::Timestamp {
                seconds: 2_000_000_000,
                nanos: 0,
            }),
            value: Some(VertexValue::String("same payload".into())),
        }),
        if_absent: false,
        receipt_context: None,
    };
    scripted(
        &enabled,
        put,
        RetryClass::UnconditionalPut,
        vec![Code::Unavailable],
        CallOptions::Default,
        payloads.clone(),
    )
    .await
    .expect("identical unconditional Put may retry");
    {
        let sent = payloads.lock().expect("payload mutex poisoned");
        assert_eq!(sent.len(), 2);
        assert_eq!(
            sent[0], sent[1],
            "retries must not recompute TTL or payload"
        );
    }

    let unsafe_methods = [
        "receipt-less Add",
        "conditional Put",
        "exact Delete",
        "capped prefix Delete",
        "stream",
        "unknown operation",
    ];
    for method in unsafe_methods {
        let payloads = Arc::new(Mutex::new(Vec::new()));
        let result = scripted(
            &enabled,
            DeleteVertexRequest {
                key: format!("rust:{method}"),
                receipt_context: None,
            },
            RetryClass::Never,
            vec![Code::Unavailable],
            CallOptions::Default,
            payloads.clone(),
        )
        .await;
        assert!(matches!(result, Err(LanternError::Rpc(_))), "{method}");
        assert_eq!(attempts(&payloads), 1, "{method}");
    }

    let payloads = Arc::new(Mutex::new(Vec::new()));
    let add = AddEdgeRequest {
        ..Default::default()
    };
    assert!(matches!(
        scripted(
            &enabled,
            add,
            RetryClass::Never,
            vec![Code::Unavailable],
            CallOptions::Default,
            payloads.clone(),
        )
        .await,
        Err(LanternError::Rpc(_))
    ));
    assert_eq!(attempts(&payloads), 1);

    let payloads = Arc::new(Mutex::new(Vec::new()));
    let result = scripted(
        &enabled,
        DeleteEdgeContributionsRequest {
            contributions: vec![EdgeContributionKey {
                tail: "rust:tail".into(),
                head: "rust:head".into(),
                contrib_id: vec![1; 24],
            }],
            receipt_context: None,
        },
        RetryClass::Never,
        vec![Code::Unavailable],
        CallOptions::Default,
        payloads.clone(),
    )
    .await;
    assert!(matches!(result, Err(LanternError::Rpc(_))));
    assert_eq!(
        attempts(&payloads),
        1,
        "selective Delete cannot replay after response loss"
    );

    for code in [
        Code::InvalidArgument,
        Code::DeadlineExceeded,
        Code::Cancelled,
        Code::Unauthenticated,
        Code::PermissionDenied,
        Code::ResourceExhausted,
        Code::Internal,
        Code::Unknown,
    ] {
        let payloads = Arc::new(Mutex::new(Vec::new()));
        let error = scripted(
            &enabled,
            GetServerStatusRequest {},
            RetryClass::ReadOnly,
            vec![code],
            CallOptions::Default,
            payloads.clone(),
        )
        .await
        .expect_err("only unavailable is retryable");
        assert_eq!(rpc_failure(error).status().code(), code);
        assert_eq!(attempts(&payloads), 1);
    }
}

#[tokio::test]
async fn retry_fetches_a_fresh_token_each_attempt() {
    let token = Arc::new(RotatingToken::new("first"));
    let client = controlled_client(
        RetryPolicy::Unavailable { max_attempts: 2 },
        Some(token.clone()),
    );
    let attempts = Arc::new(AtomicUsize::new(0));
    let attempts_for_call = attempts.clone();
    let token_for_call = token.clone();
    client
        .call_unary(
            HealthCheckRequest {
                service: HEALTH_SERVICE.into(),
            },
            CallOptions::Default,
            RetryClass::ReadOnly,
            AuthMode::Data,
            |channel, _, _| HealthClient::new(channel),
            move |_service, request| {
                let attempts = attempts_for_call.clone();
                let token = token_for_call.clone();
                Box::pin(async move {
                    let ordinal = attempts.fetch_add(1, Ordering::SeqCst);
                    let expected = if ordinal == 0 {
                        "Bearer first"
                    } else {
                        "Bearer second"
                    };
                    assert_eq!(
                        request
                            .metadata()
                            .get("authorization")
                            .expect("bearer")
                            .to_str()
                            .unwrap(),
                        expected
                    );
                    if ordinal == 0 {
                        token.rotate("second");
                        Err(Status::unavailable("first response lost"))
                    } else {
                        Ok(Response::new(HealthCheckResponse {
                            status: ServingStatus::Serving as i32,
                        }))
                    }
                })
            },
        )
        .await
        .expect("second attempt succeeds");
    assert_eq!(token.calls(), 2);
    assert_eq!(attempts.load(Ordering::SeqCst), 2);
}

#[tokio::test]
async fn unrecognized_unavailable_details_never_enable_retry() {
    let client = controlled_client(RetryPolicy::Unavailable { max_attempts: 3 }, None);
    let unknown = RichStatus {
        code: Code::Unavailable as i32,
        message: "future detail".into(),
        details: vec![prost_types::Any {
            type_url: "type.googleapis.com/future.WorkFailure".into(),
            value: vec![1, 2, 3],
        }],
    }
    .encode_to_vec();

    for details in [vec![0xff], unknown] {
        let attempts = Arc::new(AtomicUsize::new(0));
        let count = attempts.clone();
        let result: Result<HealthCheckResponse, LanternError> = client
            .call_unary(
                HealthCheckRequest {
                    service: HEALTH_SERVICE.into(),
                },
                CallOptions::Default,
                RetryClass::ReadOnly,
                AuthMode::None,
                |channel, _, _| HealthClient::new(channel),
                move |_service, _request| {
                    let count = count.clone();
                    let raw = details.clone();
                    Box::pin(async move {
                        count.fetch_add(1, Ordering::SeqCst);
                        Err(Status::with_details(
                            Code::Unavailable,
                            "unrecognized response",
                            Bytes::from(raw),
                        ))
                    })
                },
            )
            .await;
        let failure = rpc_failure(result.expect_err("unknown detail cannot be retried"));
        assert_eq!(failure.kind(), RpcErrorKind::Unavailable);
        assert!(matches!(
            failure.search_details(),
            SearchDetails::Malformed { .. } | SearchDetails::Unknown { .. }
        ));
        assert_eq!(attempts.load(Ordering::SeqCst), 1);
    }
}

struct FailingToken;

impl TokenProvider for FailingToken {
    fn token(&self) -> Pin<Box<dyn Future<Output = Result<String, TokenError>> + Send + '_>> {
        Box::pin(async { Err(std::io::Error::other("private provider diagnostic").into()) })
    }
}

#[tokio::test]
async fn provider_failure_does_not_become_an_empty_token_or_retry() {
    let client = controlled_client(
        RetryPolicy::Unavailable { max_attempts: 3 },
        Some(Arc::new(FailingToken)),
    );
    let payloads = Arc::new(Mutex::new(Vec::new()));
    let error = scripted(
        &client,
        GetServerStatusRequest {},
        RetryClass::ReadOnly,
        vec![],
        CallOptions::Default,
        payloads.clone(),
    )
    .await
    .expect_err("provider failure must stop the call");
    assert!(matches!(error, LanternError::TokenProvider(_)));
    assert_eq!(attempts(&payloads), 0);
    assert!(!error.to_string().contains("private provider diagnostic"));
    assert!(!format!("{error:?}").contains("private provider diagnostic"));
}

struct PendingToken;

impl TokenProvider for PendingToken {
    fn token(&self) -> Pin<Box<dyn Future<Output = Result<String, TokenError>> + Send + '_>> {
        Box::pin(std::future::pending())
    }
}

#[tokio::test]
async fn stream_budgets_cover_pending_auth_before_the_first_frame() {
    let client = controlled_client(RetryPolicy::Disabled, Some(Arc::new(PendingToken)));
    assert!(matches!(
        client
            .bootstrap_identities(StreamOptions::default().with_idle(Duration::ZERO))
            .await,
        Err(LanternError::InvalidConfig(_))
    ));
    let mut idle = client
        .bootstrap_identities(StreamOptions::default().with_idle(Duration::from_millis(20)))
        .await
        .unwrap();
    assert!(matches!(
        idle.next_event().await,
        Err(LanternError::StreamIdleTimeout)
    ));
    let mut lifetime = client
        .subscribe_full_mutations(
            CdcCursor::new(),
            StreamOptions::default().with_lifetime(Duration::from_millis(20)),
        )
        .await
        .unwrap();
    assert!(matches!(
        lifetime.next_mutation().await,
        Err(LanternError::DeadlineExceeded)
    ));
}

fn slow_health_rpc<'a>(
    _: &'a mut HealthClient<Channel>,
    _: Request<HealthCheckRequest>,
) -> RpcFuture<'a, HealthCheckResponse> {
    Box::pin(async {
        sleep(Duration::from_millis(80)).await;
        Ok(Response::new(HealthCheckResponse {
            status: ServingStatus::Serving as i32,
        }))
    })
}

#[tokio::test]
async fn one_deadline_covers_provider_attempts_and_backoff() -> Result<(), LanternError> {
    let mut client = controlled_client(RetryPolicy::Disabled, None);
    Arc::get_mut(&mut client.state)
        .expect("sole client owner")
        .unary_timeout = Duration::from_millis(20);
    assert!(matches!(
        client
            .call_unary(
                HealthCheckRequest {
                    service: HEALTH_SERVICE.into()
                },
                CallOptions::Default,
                RetryClass::ReadOnly,
                AuthMode::None,
                |channel, _, _| HealthClient::new(channel),
                slow_health_rpc,
            )
            .await,
        Err(LanternError::DeadlineExceeded)
    ));

    let ready = client
        .call_unary(
            HealthCheckRequest {
                service: HEALTH_SERVICE.into(),
            },
            CallOptions::After(Duration::from_millis(250)),
            RetryClass::ReadOnly,
            AuthMode::None,
            |channel, _, _| HealthClient::new(channel),
            slow_health_rpc,
        )
        .await?;
    assert_eq!(ready.status, ServingStatus::Serving as i32);
    let ready = client
        .call_unary(
            HealthCheckRequest {
                service: HEALTH_SERVICE.into(),
            },
            CallOptions::NoDeadline,
            RetryClass::ReadOnly,
            AuthMode::None,
            |channel, _, _| HealthClient::new(channel),
            slow_health_rpc,
        )
        .await?;
    assert_eq!(ready.status, ServingStatus::Serving as i32);

    let waiting_provider = controlled_client(
        RetryPolicy::Unavailable { max_attempts: 3 },
        Some(Arc::new(PendingToken)),
    );
    let payloads = Arc::new(Mutex::new(Vec::new()));
    assert!(matches!(
        scripted(
            &waiting_provider,
            GetServerStatusRequest {},
            RetryClass::ReadOnly,
            vec![],
            CallOptions::After(Duration::from_millis(20)),
            payloads.clone()
        )
        .await,
        Err(LanternError::DeadlineExceeded)
    ));
    assert_eq!(attempts(&payloads), 0);

    let retry = controlled_client(RetryPolicy::Unavailable { max_attempts: 3 }, None);
    let calls = Arc::new(AtomicUsize::new(0));
    let calls_for_rpc = calls.clone();
    let result: Result<HealthCheckResponse, LanternError> = retry
        .call_unary(
            HealthCheckRequest {
                service: HEALTH_SERVICE.into(),
            },
            CallOptions::After(Duration::from_millis(65)),
            RetryClass::ReadOnly,
            AuthMode::None,
            |channel, _, _| HealthClient::new(channel),
            move |_service, _request| {
                let calls = calls_for_rpc.clone();
                Box::pin(async move {
                    calls.fetch_add(1, Ordering::SeqCst);
                    sleep(Duration::from_millis(50)).await;
                    Err(Status::unavailable("attempt lost"))
                })
            },
        )
        .await;
    assert!(matches!(result, Err(LanternError::DeadlineExceeded)));
    assert!((1..=2).contains(&calls.load(Ordering::SeqCst)));
    Ok(())
}

#[tokio::test]
async fn dropping_retry_wait_and_mutation_future_stops_work() {
    let completed = Arc::new(AtomicUsize::new(0));
    let completion = completed.clone();
    let wait = tokio::spawn(async move {
        wait_backoff(Duration::from_secs(30), None)
            .await
            .expect("wait");
        completion.fetch_add(1, Ordering::SeqCst);
    });
    tokio::task::yield_now().await;
    wait.abort();
    assert!(
        wait.await
            .expect_err("backoff was cancelled")
            .is_cancelled()
    );
    assert_eq!(completed.load(Ordering::SeqCst), 0);

    let client = controlled_client(RetryPolicy::Unavailable { max_attempts: 3 }, None);
    let entered = Arc::new(Notify::new());
    let never_reply = Arc::new(Notify::new());
    let called = Arc::new(AtomicUsize::new(0));
    let entered_rpc = entered.clone();
    let held_rpc = never_reply.clone();
    let called_rpc = called.clone();
    let task = tokio::spawn(async move {
        client
            .data_unary::<_, PutVertexResponse, _>(
                PutVertexRequest {
                    vertex: Some(Vertex {
                        key: "rust:ambiguous-write".into(),
                        expiration: None,
                        value: Some(VertexValue::String("data".into())),
                    }),
                    if_absent: false,
                    receipt_context: None,
                },
                CallOptions::NoDeadline,
                RetryClass::UnconditionalPut,
                move |_service, _request| {
                    let entered_rpc = entered_rpc.clone();
                    let held_rpc = held_rpc.clone();
                    let called_rpc = called_rpc.clone();
                    Box::pin(async move {
                        called_rpc.fetch_add(1, Ordering::SeqCst);
                        entered_rpc.notify_one();
                        held_rpc.notified().await;
                        Err(Status::unavailable("no response after write"))
                    })
                },
            )
            .await
    });
    entered.notified().await;
    task.abort();
    assert!(
        task.await
            .expect_err("write future was cancelled")
            .is_cancelled()
    );
    never_reply.notify_one();
    tokio::task::yield_now().await;
    assert_eq!(called.load(Ordering::SeqCst), 1);
    // There is no acknowledged result; the caller cannot infer whether a
    // canceled mutation committed before its future was dropped.
}

#[test]
fn jitter_is_bounded_without_a_fixed_delay() {
    for failed_attempt in 0..=2 {
        let ceiling = Duration::from_millis(100_u64 << failed_attempt);
        for _ in 0..50 {
            assert!(full_jitter(failed_attempt) <= ceiling);
        }
    }
}

struct TestCertificates {
    files: TempDir,
    ca_pem: Vec<u8>,
    other_ca_pem: Vec<u8>,
    client_cert_pem: Vec<u8>,
    client_key_pem: Vec<u8>,
}

impl TestCertificates {
    fn generate() -> Result<Self, Box<dyn Error>> {
        let mut ca_params = CertificateParams::new(Vec::<String>::new())?;
        ca_params.is_ca = IsCa::Ca(BasicConstraints::Unconstrained);
        ca_params.key_usages = vec![
            KeyUsagePurpose::KeyCertSign,
            KeyUsagePurpose::DigitalSignature,
        ];
        let ca_key = KeyPair::generate()?;
        let ca = ca_params.self_signed(&ca_key)?;
        let ca_pem = ca.pem().into_bytes();

        let mut server_params = CertificateParams::new(vec!["localhost".into()])?;
        server_params.extended_key_usages = vec![ExtendedKeyUsagePurpose::ServerAuth];
        let server_key = KeyPair::generate()?;
        let server_cert = server_params.signed_by(&server_key, &ca, &ca_key)?;

        let mut client_params = CertificateParams::new(vec!["rust-client".into()])?;
        client_params.extended_key_usages = vec![ExtendedKeyUsagePurpose::ClientAuth];
        let client_key = KeyPair::generate()?;
        let client_cert = client_params.signed_by(&client_key, &ca, &ca_key)?;

        let mut other_params = CertificateParams::new(Vec::<String>::new())?;
        other_params.is_ca = IsCa::Ca(BasicConstraints::Unconstrained);
        other_params.key_usages = vec![KeyUsagePurpose::KeyCertSign];
        let other_key = KeyPair::generate()?;
        let other_ca_pem = other_params.self_signed(&other_key)?.pem().into_bytes();

        let target = std::path::Path::new(env!("CARGO_MANIFEST_DIR")).join("target");
        fs::create_dir_all(&target)?;
        let files = tempfile::Builder::new()
            .prefix("rust-tls-")
            .tempdir_in(&target)?;
        fs::write(files.path().join("server.pem"), server_cert.pem())?;
        fs::write(files.path().join("server.key"), server_key.serialize_pem())?;
        fs::write(files.path().join("ca.pem"), &ca_pem)?;
        let client_cert_pem = client_cert.pem().into_bytes();
        let client_key_pem = client_key.serialize_pem().into_bytes();
        fs::write(files.path().join("client.pem"), &client_cert_pem)?;
        fs::write(files.path().join("client.key"), &client_key_pem)?;

        Ok(Self {
            files,
            ca_pem,
            other_ca_pem,
            client_cert_pem,
            client_key_pem,
        })
    }

    fn tls_env(&self, mtls: bool) -> Result<Vec<(&'static str, String)>, Box<dyn Error>> {
        let path = |file| -> Result<String, Box<dyn Error>> {
            Ok(self
                .files
                .path()
                .join(file)
                .to_str()
                .ok_or("non-UTF-8 TLS path")?
                .into())
        };
        let mut env = vec![
            ("LANTERN_TLS_CERT_FILE", path("server.pem")?),
            ("LANTERN_TLS_KEY_FILE", path("server.key")?),
        ];
        if mtls {
            env.push(("LANTERN_TLS_CLIENT_CA_FILE", path("ca.pem")?));
        }
        Ok(env)
    }
}

fn start_tls_server(certs: &TestCertificates, mtls: bool) -> Result<GoServer, Box<dyn Error>> {
    let env = certs.tls_env(mtls)?;
    let borrowed: Vec<(&str, &str)> = env
        .iter()
        .map(|(name, value)| (*name, value.as_str()))
        .collect();
    let mut server = GoServer::start(&borrowed)?;
    server.wait_for_listener()?;
    Ok(server)
}

fn test_receipt_context(
    capability: &GetReceiptCapabilityResponse,
    seed: u8,
    count: usize,
) -> MutationReceiptContext {
    let epoch = &capability
        .policy
        .as_ref()
        .expect("receipt policy")
        .deployment_epoch;
    assert_eq!(epoch.len(), 16);
    let issued = capability
        .server_now_unix_ms
        .checked_sub(1_000)
        .expect("positive server clock");
    let operation_ids = (0..count)
        .map(|index| {
            let mut id = vec![0_u8; 49];
            id[0] = 1;
            id[1..17].copy_from_slice(epoch);
            id[17..25].copy_from_slice(&issued.to_be_bytes());
            id[25] = seed;
            id[26] = u8::try_from(index + 1).expect("bounded fixture");
            id
        })
        .collect();
    MutationReceiptContext {
        operation_ids,
        logical_call_id: vec![seed; 16],
        endpoint: capability.endpoint.clone(),
    }
}

fn assert_send_sync<T: Send + Sync>() {}

#[test]
fn client_can_be_shared_across_tasks() {
    assert_send_sync::<LanternClient>();
}

#[test]
fn invalid_configuration_is_rejected_before_connection() {
    assert!(
        LanternClient::builder("http://localhost")
            .message_limits(0, 1)
            .is_err()
    );
    assert!(
        LanternClient::builder("http://localhost")
            .message_limits(1, MAX_MESSAGE_LIMIT + 1)
            .is_err()
    );
    assert!(
        LanternClient::builder("http://localhost")
            .retry(RetryPolicy::Unavailable { max_attempts: 4 })
            .is_err()
    );
    assert!(
        LanternClient::builder("http://localhost")
            .batch_chunk_size(0)
            .is_err()
    );
    assert!(
        LanternClient::builder("https://localhost")
            .tls_private_ca_pem(b"not a certificate")
            .is_err()
    );
    assert!(
        LanternClient::builder("https://localhost")
            .tls_client_identity(b"not a certificate", b"not a private key")
            .is_err()
    );
    assert!(
        LanternClient::builder("http://localhost")
            .connect_timeout(Duration::ZERO)
            .is_err()
    );
    assert!(
        LanternClient::builder("http://localhost")
            .unary_timeout(Duration::ZERO)
            .is_err()
    );
}

#[test]
fn pem_configuration_rejects_missing_and_malformed_material() {
    let certs = TestCertificates::generate().expect("test certificates");
    assert!(matches!(
        LanternClient::builder("https://localhost").tls_private_ca_pem(b"not a certificate"),
        Err(LanternError::InvalidConfig(
            "CA PEM contains no certificates"
        ))
    ));
    assert!(matches!(
        LanternClient::builder("https://localhost")
            .tls_private_ca_pem(b"-----BEGIN CERTIFICATE-----\nAA==\n-----END CERTIFICATE-----"),
        Err(LanternError::InvalidConfig(
            "CA PEM contains an invalid certificate"
        ))
    ));
    assert!(matches!(
        LanternClient::builder("https://localhost")
            .tls_client_identity(&certs.client_cert_pem, b"not a private key"),
        Err(LanternError::InvalidConfig(
            "client private key PEM contains no private key"
        ))
    ));
    assert!(matches!(
        LanternClient::builder("https://localhost").tls_client_identity(
            &certs.client_cert_pem,
            b"-----BEGIN PRIVATE KEY-----\n@@\n-----END PRIVATE KEY-----",
        ),
        Err(LanternError::InvalidConfig(
            "invalid client private key PEM"
        ))
    ));
    assert!(
        LanternClient::builder("https://localhost")
            .tls_private_ca_pem(&certs.ca_pem)
            .expect("valid CA")
            .tls_client_identity(&certs.client_cert_pem, &certs.client_key_pem)
            .is_ok()
    );
}

#[test]
fn health_status_requires_serving_even_for_unknown_values() {
    assert!(require_serving(ServingStatus::Serving as i32).is_ok());
    for status in [
        ServingStatus::Unknown as i32,
        ServingStatus::NotServing as i32,
        1000,
    ] {
        assert!(matches!(
            require_serving(status),
            Err(LanternError::HealthNotServing(actual)) if actual == status
        ));
    }
}

#[tokio::test]
async fn endpoint_and_plaintext_credential_policy_fail_closed() {
    for invalid in [
        "localhost:6380",
        "ftp://localhost:6380",
        "http://user:password@localhost:6380",
        "http://localhost:6380/data",
        "https://localhost:6380?token=secret",
        "http://",
    ] {
        assert!(
            matches!(
                LanternClient::builder(invalid).connect().await,
                Err(LanternError::InvalidEndpoint(_))
            ),
            "accepted invalid endpoint shape"
        );
    }
    let token = Arc::new(RotatingToken::new("secret"));
    assert!(matches!(
        LanternClient::builder("http://127.0.0.1:6380")
            .token_provider(token)
            .connect()
            .await,
        Err(LanternError::InvalidConfig(_))
    ));
    assert!(matches!(
        LanternClient::builder("http://127.0.0.1:6380")
            .tls_private_ca_pem(TestCertificates::generate().expect("certificates").ca_pem)
            .expect("valid CA")
            .connect()
            .await,
        Err(LanternError::InvalidConfig(_))
    ));
}

#[tokio::test]
#[ignore = "set LANTERN_RUST_TEST_SERVER to the production Go server binary"]
async fn real_wire_h2c_limits_search_and_stream() -> TestResult {
    let mut server = GoServer::start(&[("LANTERN_SEARCH_ENABLED", "false")])?;
    server.wait_for_listener()?;
    let endpoint = format!("http://127.0.0.1:{}", server.port());
    let client = LanternClient::builder(&endpoint).connect().await?;
    client.ping().await?;

    let missing = rpc_failure(
        get_vertex(&client, "rust:missing")
            .await
            .expect_err("missing key"),
    );
    assert_eq!(missing.kind(), RpcErrorKind::NotFound);

    let large_value = "a".repeat(4 * 1024 * 1024 + 64);
    let key = format!("rust:large:{}", server.port());
    put_vertex(
        &client,
        Vertex {
            key: key.clone(),
            expiration: None,
            value: Some(VertexValue::String(large_value.clone())),
        },
    )
    .await?;
    let saved = get_vertex(&client, &key)
        .await?
        .vertex
        .ok_or("missing value after Put")?;
    assert_eq!(saved.value, Some(VertexValue::String(large_value)));

    let limited = LanternClient::builder(&endpoint)
        .message_limits(DEFAULT_MESSAGE_LIMIT, 1024 * 1024)?
        .connect()
        .await?;
    let over_limit = rpc_failure(get_vertex(&limited, &key).await.expect_err("decode limit"));
    assert_eq!(over_limit.kind(), RpcErrorKind::Other(Code::OutOfRange));
    assert_eq!(over_limit.status().code(), Code::OutOfRange);

    let disabled = rpc_failure(
        search(&client, "sample", vec![])
            .await
            .expect_err("disabled search"),
    );
    assert_eq!(disabled.kind(), RpcErrorKind::FailedPrecondition);
    assert!(matches!(
        disabled.search_details(),
        SearchDetails::Known { reason: SearchErrorReason::SearchDisabled, work_kind }
            if work_kind.is_empty()
    ));
    assert!(!disabled.status().details().is_empty());

    // The public readiness checker supports the auth-exempt unary probe.
    // Health Watch is deliberately unimplemented by Connect grpchealth.
    let mut health = HealthClient::new(client.state.channel.clone());
    let unsupported = health
        .watch(HealthCheckRequest {
            service: HEALTH_SERVICE.into(),
        })
        .await;
    let unsupported = match unsupported {
        Err(status) => status,
        Ok(response) => response
            .into_inner()
            .message()
            .await
            .expect_err("unexpected Health Watch message"),
    };
    assert_eq!(unsupported.code(), Code::Unimplemented);
    drop(health);
    drop(limited);
    drop(client);

    Ok(())
}

#[tokio::test]
#[ignore = "set LANTERN_RUST_TEST_SERVER to the production Go server binary"]
async fn real_wire_https_roots_and_invalid_certificates() -> TestResult {
    let certs = TestCertificates::generate()?;
    let server = start_tls_server(&certs, false)?;
    let endpoint = format!("https://localhost:{}", server.port());

    let client = LanternClient::builder(&endpoint)
        .tls_private_ca_pem(&certs.ca_pem)?
        .connect()
        .await?;
    client.ping().await?;
    assert!(!get_status(&client).await?.go_version.is_empty());

    assert!(matches!(
        LanternClient::builder(&endpoint).connect().await,
        Err(LanternError::Transport(_))
    ));
    assert!(matches!(
        LanternClient::builder(&endpoint)
            .tls_private_ca_pem(&certs.other_ca_pem)?
            .connect()
            .await,
        Err(LanternError::Transport(_))
    ));
    assert!(matches!(
        LanternClient::builder(format!("https://127.0.0.1:{}", server.port()))
            .tls_private_ca_pem(&certs.ca_pem)?
            .connect()
            .await,
        Err(LanternError::Transport(_))
    ));

    let plaintext = LanternClient::builder(format!("http://localhost:{}", server.port()))
        .connect_timeout(Duration::from_millis(200))?
        .connect()
        .await;
    if let Ok(client) = plaintext {
        assert!(
            client.ping().await.is_err(),
            "TLS server accepted plaintext"
        );
    }
    Ok(())
}

#[tokio::test]
#[ignore = "set LANTERN_RUST_TEST_SERVER to the production Go server binary"]
async fn real_wire_mtls_and_bearer_rotation() -> TestResult {
    let mut server = GoServer::start_authenticated(&[], true, false)?;
    server.wait_for_listener()?;
    let endpoint = server.endpoint(0)?;
    let ca = server.ca_pem()?;
    let (client_cert, client_key) = server.client_identity()?;

    let no_identity = LanternClient::builder(endpoint)
        .tls_private_ca_pem(&ca)?
        .connect()
        .await;
    match no_identity {
        Ok(client) => assert!(
            client.ping().await.is_err(),
            "server accepted missing client cert"
        ),
        Err(LanternError::Transport(_)) => {}
        Err(other) => return Err(format!("unexpected mTLS failure: {other}").into()),
    }

    let token = Arc::new(RotatingToken::new(crate::test_server::TEST_TOKEN));
    let client = LanternClient::builder(endpoint)
        .tls_private_ca_pem(&ca)?
        .tls_client_identity(&client_cert, &client_key)?
        .token_provider(token.clone())
        .connect()
        .await?;
    client.ping().await?;
    assert_eq!(token.calls(), 0, "Health must not read or attach a token");
    assert!(!get_status(&client).await?.go_version.is_empty());
    token.rotate(crate::test_server::SECOND_TEST_TOKEN);
    assert!(!get_status(&client).await?.go_version.is_empty());
    token.rotate("wrong");
    let denied = rpc_failure(get_status(&client).await.expect_err("invalid token"));
    assert_eq!(denied.kind(), RpcErrorKind::Unauthenticated);
    assert_eq!(token.calls(), 3);

    let auth_free = LanternClient::builder(endpoint)
        .tls_private_ca_pem(&ca)?
        .tls_client_identity(&client_cert, &client_key)?
        .connect()
        .await?;
    auth_free.ping().await?;
    assert_eq!(
        rpc_failure(get_status(&auth_free).await.expect_err("token required")).kind(),
        RpcErrorKind::Unauthenticated
    );
    Ok(())
}

#[tokio::test]
#[ignore = "set LANTERN_RUST_TEST_SERVER to the production Go server binary"]
async fn real_wire_authenticated_streams_have_independent_budgets_and_no_retry() -> TestResult {
    use crate::{ChangeProjection, WatchChangesOptions};
    let mut server = GoServer::start_authenticated(&[], true, false)?;
    server.wait_for_listener()?;
    let (certificate, key) = server.client_identity()?;
    let token = Arc::new(RotatingToken::new(crate::test_server::TEST_TOKEN));
    let client = LanternClient::builder(server.endpoint(0)?)
        .tls_private_ca_pem(server.ca_pem()?)?
        .tls_client_identity(certificate, key)?
        .token_provider(token.clone())
        .retry(RetryPolicy::Unavailable { max_attempts: 3 })?
        .unary_timeout(Duration::from_millis(100))?
        .connect()
        .await?;
    let options = || WatchChangesOptions {
        prefix: "rust:authenticated-stream:".into(),
        bootstrap: true,
        ..Default::default()
    };
    let mut bootstrap = client
        .watch_changes(options(), StreamOptions::default())
        .await?;
    assert_eq!(
        token.calls(),
        1,
        "one fresh credential for the single stream open"
    );
    assert!(bootstrap.next_frame().await?.bootstrap);
    drop(bootstrap);
    client
        .put_vertex(VertexInput::nil("rust:authenticated-stream:v"))
        .await?;
    let mut archive = Vec::new();
    let manifest = client
        .write_backup(
            &mut archive,
            "rust:authenticated-stream:",
            BackupFormat::LengthDelimitedProtobuf,
            StreamOptions::default(),
        )
        .await?;
    assert_eq!((manifest.vertex_count(), manifest.edge_count()), (1, 0));
    assert_eq!(token.calls(), 3);
    let mut lifetime = client
        .watch_changes(
            options(),
            StreamOptions::default().with_lifetime(Duration::from_millis(300)),
        )
        .await?;
    assert!(lifetime.next_frame().await?.bootstrap);
    let began = Instant::now();
    let expired = lifetime.next_frame().await;
    assert!(
        matches!(expired, Err(LanternError::DeadlineExceeded))
            || matches!(expired, Err(LanternError::Rpc(ref failure)) if failure.kind() == RpcErrorKind::DeadlineExceeded
            ),
        "unexpected stream lifetime result"
    );
    assert!(
        began.elapsed() >= Duration::from_millis(200),
        "stream inherited the 100 ms unary deadline"
    );
    token.rotate("wrong");
    let before = token.calls();
    let denied = client
        .watch_changes(options(), StreamOptions::default())
        .await;
    let error = match denied {
        Err(error) => error,
        Ok(mut stream) => stream
            .next_frame()
            .await
            .expect_err("invalid credential opened stream"),
    };
    assert_eq!(rpc_failure(error).kind(), RpcErrorKind::Unauthenticated);
    assert_eq!(
        token.calls(),
        before + 1,
        "stream must not retry an auth failure"
    );
    token.rotate(crate::test_server::SECOND_TEST_TOKEN);
    let mut restored = client
        .watch_changes(
            WatchChangesOptions {
                projection: ChangeProjection::Identity,
                ..options()
            },
            StreamOptions::default(),
        )
        .await?;
    assert!(restored.next_frame().await?.bootstrap);
    Ok(())
}

#[tokio::test]
#[ignore = "set LANTERN_RUST_TEST_SERVER to the production Go server binary"]
async fn real_wire_authenticated_ha_resumes_on_another_responder_and_backs_up() -> TestResult {
    use crate::{ChangeInvalidation, ChangeProjection, WatchChangesOptions};
    let mut cohort = GoServer::start_authenticated_cohort(2, &[], false, false)?;
    cohort.wait_for_listener()?;
    let first = LanternClient::builder(cohort.endpoint(0)?)
        .tls_private_ca_pem(cohort.ca_pem()?)?
        .token_provider(Arc::new(RotatingToken::new(crate::test_server::TEST_TOKEN)))
        .connect()
        .await?;
    let second = LanternClient::builder(cohort.endpoint(1)?)
        .tls_private_ca_pem(cohort.ca_pem()?)?
        .token_provider(Arc::new(RotatingToken::new(crate::test_server::TEST_TOKEN)))
        .connect()
        .await?;
    assert!(second.replication_status().await?.enabled);
    let prefix = "rust:ha-stream:";
    let first_key = format!("{prefix}tail");
    let second_key = format!("{prefix}head");
    let mut first_stream = first
        .watch_changes(
            WatchChangesOptions {
                prefix: prefix.into(),
                projection: ChangeProjection::Identity,
                bootstrap: true,
                cursor: None,
            },
            StreamOptions::default().with_idle(Duration::from_secs(5)),
        )
        .await?;
    assert!(first_stream.next_frame().await?.bootstrap);
    first.put_vertex(VertexInput::int64(&first_key, 31)).await?;
    let event = first_stream.next_frame().await?;
    assert_eq!(
        event.invalidations,
        [ChangeInvalidation::Vertex {
            key: first_key.clone(),
            current: None
        }]
    );
    let cursor = event
        .cursor
        .ok_or("visible mutation has no final opaque cursor")?;
    drop(first_stream);
    let mut synchronized = false;
    for _ in 0..150 {
        if let Ok(value) = second.get_vertex(&first_key).await {
            synchronized = value.int64_value() == Some(31);
            if synchronized {
                break;
            }
        }
        sleep(Duration::from_millis(100)).await;
    }
    assert!(
        synchronized,
        "native HA peer did not apply the source mutation"
    );
    let mut resumed = second
        .watch_changes(
            WatchChangesOptions {
                prefix: prefix.into(),
                projection: ChangeProjection::Identity,
                bootstrap: false,
                cursor: Some(cursor),
            },
            StreamOptions::default().with_idle(Duration::from_secs(10)),
        )
        .await?;
    first.put_vertex(VertexInput::nil(&second_key)).await?;
    // Fixed empty progress frames do not represent hidden mutations. Wait for
    // the visible invalidation without inspecting private origin/sequence data.
    let next = tokio::time::timeout(Duration::from_secs(10), async {
        loop {
            let frame = resumed.next_frame().await?;
            if !frame.invalidations.is_empty() {
                return Ok::<_, LanternError>(frame);
            }
        }
    })
    .await??;
    assert_eq!(
        next.invalidations,
        [ChangeInvalidation::Vertex {
            key: second_key.clone(),
            current: None
        }]
    );
    assert!(next.cursor.is_some());
    drop(resumed);

    first
        .put_edge(EdgeInput::new(&first_key, &second_key, 2.0))
        .await?;
    first
        .add_edge(AddInput::new(EdgeInput::new(&first_key, &second_key, 3.0)))
        .await?;
    let mut folded = false;
    for _ in 0..150 {
        if let Ok(edge) = second.get_edge(&first_key, &second_key).await {
            folded = edge.weight == 5.0;
            if folded {
                break;
            }
        }
        sleep(Duration::from_millis(100)).await;
    }
    assert!(
        folded,
        "authenticated HA responder did not fold both edge writes"
    );
    let mut bytes = Vec::new();
    let manifest = second
        .write_backup(
            &mut bytes,
            prefix,
            BackupFormat::LengthDelimitedProtobuf,
            StreamOptions::default(),
        )
        .await?;
    assert_eq!((manifest.vertex_count(), manifest.edge_count()), (2, 1));
    assert!(!bytes.is_empty());
    let mut destination_server = GoServer::start(&[])?;
    destination_server.wait_for_listener()?;
    let destination =
        LanternClient::builder(format!("http://127.0.0.1:{}", destination_server.port()))
            .connect()
            .await?;
    let report = destination
        .restore_backup(
            &mut Cursor::new(bytes),
            &manifest,
            RestoreOptions::default().with_batch_size(1)?,
        )
        .await?;
    assert_eq!((report.completed_vertices, report.completed_edges), (2, 1));
    assert_eq!(
        destination.get_vertex(&first_key).await?.int64_value(),
        Some(31)
    );
    assert_eq!(
        destination.get_edge(&first_key, &second_key).await?.weight,
        5.0
    );
    Ok(())
}

#[tokio::test]
#[ignore = "set native production Server and authfixture binaries"]
async fn real_wire_public_cdc_and_receipt_status_cover_all_mutation_families() -> TestResult {
    use crate::{ChangeInvalidation, ChangeProjection, ChangeStream, WatchChangesOptions};
    let mut server = GoServer::start_authenticated(&[], true, true)?;
    server.wait_for_listener()?;
    let (cert, key_pem) = server.client_identity()?;
    let client = LanternClient::builder(server.endpoint(0)?)
        .tls_private_ca_pem(server.ca_pem()?)?
        .tls_client_identity(cert, key_pem)?
        .token_provider(Arc::new(RotatingToken::new(crate::test_server::TEST_TOKEN)))
        .connect()
        .await?;
    let capability: GetReceiptCapabilityResponse = client
        .data_unary(
            GetReceiptCapabilityRequest {},
            CallOptions::Default,
            RetryClass::ReadOnly,
            |service, request| Box::pin(service.get_receipt_capability(request)),
        )
        .await?;
    assert!(
        capability.enabled,
        "production receipt WAL must be certified"
    );
    let mut changes = client
        .watch_changes(
            WatchChangesOptions {
                prefix: "rust:receipt-stream:".into(),
                projection: ChangeProjection::Identity,
                bootstrap: true,
                cursor: None,
            },
            StreamOptions::default().with_idle(Duration::from_secs(10)),
        )
        .await?;
    assert!(changes.next_frame().await?.bootstrap);
    async fn visible(stream: &mut ChangeStream) -> Result<Vec<ChangeInvalidation>, LanternError> {
        loop {
            let frame = stream.next_frame().await?;
            if !frame.invalidations.is_empty() {
                assert!(frame.cursor.is_some());
                return Ok(frame.invalidations);
            }
        }
    }
    let tail = "rust:receipt-stream:tail";
    let head = "rust:receipt-stream:head";
    let key = EdgeKey {
        tail: tail.into(),
        head: head.into(),
    };
    let deleted = client
        .data_unary(
            DeleteEdgesRequest {
                edges: vec![key.clone()],
                receipt_context: Some(test_receipt_context(&capability, 1, 1)),
            },
            CallOptions::Default,
            RetryClass::Never,
            |service, request| Box::pin(service.delete_edges(request)),
        )
        .await?;
    assert_eq!(deleted.existed, [false]);
    assert_eq!(
        visible(&mut changes).await?,
        [ChangeInvalidation::Edge {
            identity: crate::EdgeRef::new(tail, head),
            current: None,
        }]
    );
    let put = client
        .data_unary(
            PutVerticesRequest {
                vertices: vec![
                    Vertex {
                        key: tail.into(),
                        value: Some(VertexValue::Int32(1)),
                        expiration: None,
                    },
                    Vertex {
                        key: head.into(),
                        value: Some(VertexValue::Nil(true)),
                        expiration: None,
                    },
                ],
                if_absent: false,
                receipt_context: Some(test_receipt_context(&capability, 2, 2)),
            },
            CallOptions::Default,
            RetryClass::Never,
            |service, request| Box::pin(service.put_vertices(request)),
        )
        .await?;
    assert_eq!(put.outcomes, [PutOutcome::AppliedAndLive as i32; 2]);
    assert_eq!(
        visible(&mut changes).await?,
        [
            ChangeInvalidation::Vertex {
                key: tail.into(),
                current: None
            },
            ChangeInvalidation::Vertex {
                key: head.into(),
                current: None
            },
        ]
    );
    let refused = client
        .data_unary(
            PutVerticesRequest {
                vertices: vec![Vertex {
                    key: tail.into(),
                    value: Some(VertexValue::Int32(999)),
                    expiration: None,
                }],
                if_absent: true,
                receipt_context: Some(test_receipt_context(&capability, 6, 1)),
            },
            CallOptions::Default,
            RetryClass::Never,
            |service, request| Box::pin(service.put_vertices(request)),
        )
        .await?;
    assert_eq!(refused.outcomes, [PutOutcome::ConditionNotMet as i32]);
    let first_id = ContribId::new([1; 24])?;
    let second_id = ContribId::new([2; 24])?;
    let add = client
        .data_unary(
            AddEdgesRequest {
                edges: vec![
                    crate::Edge {
                        tail: tail.into(),
                        head: head.into(),
                        weight: 2.0,
                        expiration: None,
                    },
                    crate::Edge {
                        tail: tail.into(),
                        head: head.into(),
                        weight: 3.0,
                        expiration: None,
                    },
                ],
                contrib_ids: vec![first_id.as_bytes().to_vec(), second_id.as_bytes().to_vec()],
                receipt_context: Some(test_receipt_context(&capability, 3, 2)),
            },
            CallOptions::Default,
            RetryClass::Never,
            |service, request| Box::pin(service.add_edges(request)),
        )
        .await?;
    assert_eq!(add.effective_weights, [2.0, 5.0]);
    let added = visible(&mut changes).await?;
    assert!(added.iter().all(|item| matches!(item, ChangeInvalidation::Edge {identity, current: None} if identity.tail == tail && identity.head == head)));
    assert_eq!(client.get_vertex(tail).await?.int32_value(), Some(1));
    assert!(
        client
            .delete_edge_contribution(EdgeContributionRef::new(tail, head, first_id))
            .await?
    );
    let _ = visible(&mut changes).await?;
    let removed = client
        .delete_edge_contributions([EdgeContributionRef::new(tail, head, second_id)])
        .await?;
    assert_eq!(removed.existed, [true]);
    let _ = visible(&mut changes).await?;
    let false_delete = client
        .data_unary(
            DeleteEdgeContributionsRequest {
                contributions: vec![EdgeContributionKey {
                    tail: tail.into(),
                    head: head.into(),
                    contrib_id: second_id.as_bytes().to_vec(),
                }],
                receipt_context: Some(test_receipt_context(&capability, 4, 1)),
            },
            CallOptions::Default,
            RetryClass::Never,
            |service, request| Box::pin(service.delete_edge_contributions(request)),
        )
        .await?;
    assert_eq!(false_delete.existed, [false]);
    let deleted = client
        .data_unary(
            DeleteVerticesRequest {
                keys: vec![tail.into()],
                receipt_context: Some(test_receipt_context(&capability, 5, 1)),
            },
            CallOptions::Default,
            RetryClass::Never,
            |service, request| Box::pin(service.delete_vertices(request)),
        )
        .await?;
    assert_eq!(deleted.existed, [true]);
    // Public CDC contains invalidation identities only. Original receipt
    // outcomes are retrieved separately, authorized by their original resource.
    let expected = [
        (1, vec![receipt_result::Result::DeleteEdgeExisted(false)]),
        (
            2,
            vec![receipt_result::Result::PutVertexOutcome(PutOutcome::AppliedAndLive as i32); 2],
        ),
        (
            3,
            vec![
                receipt_result::Result::AddEdgeEffectiveWeight(2.0),
                receipt_result::Result::AddEdgeEffectiveWeight(5.0),
            ],
        ),
        (
            4,
            vec![receipt_result::Result::DeleteEdgeContributionExisted(false)],
        ),
        (5, vec![receipt_result::Result::DeleteVertexExisted(true)]),
        (
            6,
            vec![receipt_result::Result::PutVertexOutcome(
                PutOutcome::ConditionNotMet as i32,
            )],
        ),
    ];
    for (seed, results) in expected {
        let context = test_receipt_context(&capability, seed, results.len());
        let response = client
            .data_unary(
                GetReceiptStatusesRequest {
                    operation_ids: context.operation_ids.clone(),
                },
                CallOptions::Default,
                RetryClass::ReadOnly,
                |service, request| Box::pin(service.get_receipt_statuses(request)),
            )
            .await?;
        assert_eq!(response.statuses.len(), results.len());
        for (index, (status, expected)) in response.statuses.iter().zip(results).enumerate() {
            assert_eq!(status.operation_id, context.operation_ids[index]);
            assert_eq!(status.state, MutationReceiptState::Confirmed as i32);
            let receipt = status
                .receipt
                .as_ref()
                .ok_or("confirmed status lacks receipt")?;
            assert_eq!(receipt.item_index as usize, index);
            assert_eq!(receipt.item_count as usize, context.operation_ids.len());
            assert_eq!(
                receipt
                    .original_result
                    .as_ref()
                    .and_then(|value| value.result),
                Some(expected)
            );
        }
    }
    drop(changes);
    Ok(())
}

#[tokio::test]
#[ignore = "set LANTERN_RUST_TEST_SERVER to the production Go server binary"]
async fn real_wire_authenticated_https_and_search_details() -> TestResult {
    let mut server = GoServer::start_authenticated(&[], false, false)?;
    server.wait_for_listener()?;
    let endpoint = server.endpoint(0)?;
    let token = Arc::new(RotatingToken::new(crate::test_server::TEST_TOKEN));
    assert!(matches!(
        LanternClient::builder("http://127.0.0.1:6380")
            .token_provider(token.clone())
            .connect()
            .await,
        Err(LanternError::InvalidConfig(_))
    ));
    let client = LanternClient::builder(endpoint)
        .token_provider(token.clone())
        .tls_private_ca_pem(server.ca_pem()?)?
        .connect()
        .await?;
    client.ping().await?;
    assert_eq!(token.calls(), 0);
    assert!(!get_status(&client).await?.go_version.is_empty());
    client.put_vertex(VertexInput::nil("rust:authed")).await?;
    assert!(client.get_vertex("rust:authed").await?.is_nil());
    let unauthenticated = LanternClient::builder(endpoint)
        .tls_private_ca_pem(server.ca_pem()?)?
        .connect()
        .await?;
    assert!(matches!(
        unauthenticated.get_vertex("rust:authed").await,
        Err(LanternError::Rpc(ref failure))
            if failure.kind() == RpcErrorKind::Unauthenticated
    ));

    let work = rpc_failure(
        search(&client, "z".repeat(16 * 1024 + 1), vec![])
            .await
            .expect_err("query work limit"),
    );
    assert_eq!(work.kind(), RpcErrorKind::ResourceExhausted);
    assert!(matches!(
        work.search_details(),
        SearchDetails::Known {
            reason: SearchErrorReason::SearchWorkBudgetExhausted,
            work_kind
        } if work_kind == "query_bytes"
    ));

    let cursor = rpc_failure(
        search(&client, "sample", vec![1, 2, 3])
            .await
            .expect_err("invalid cursor"),
    );
    assert_eq!(cursor.kind(), RpcErrorKind::InvalidArgument);
    assert!(matches!(
        cursor.search_details(),
        SearchDetails::Known { reason: SearchErrorReason::SearchCursorInvalid, work_kind }
            if work_kind.is_empty()
    ));

    token.rotate("invalid\nheader");
    assert!(matches!(
        get_status(&client).await,
        Err(LanternError::InvalidToken)
    ));
    assert_eq!(token.calls(), 6);
    Ok(())
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
#[ignore = "set LANTERN_RUST_TEST_SERVER to the production Go server binary"]
async fn real_wire_cloned_concurrency_and_disconnect() -> TestResult {
    let mut server = GoServer::start(&[])?;
    server.wait_for_listener()?;
    let endpoint = format!("http://127.0.0.1:{}", server.port());
    let client = LanternClient::builder(endpoint).connect().await?;
    let mut calls = Vec::new();
    for _ in 0..16 {
        let shared = client.clone();
        calls.push(tokio::spawn(async move {
            shared.ping().await?;
            get_status(&shared)
                .await
                .map(|response| response.go_version)
        }));
    }
    for call in calls {
        assert!(!call.await??.is_empty());
    }
    drop(server);
    let disconnected = rpc_failure(
        get_status(&client)
            .await
            .expect_err("a stopped server must be unavailable"),
    );
    assert_ne!(
        disconnected.status().code(),
        Code::Ok,
        "disconnect must not look like a successful read: {:?}",
        disconnected.kind()
    );
    Ok(())
}

#[tokio::test]
async fn missing_listener_is_a_transport_failure() -> TestResult {
    let listener = TcpListener::bind("127.0.0.1:0")?;
    let endpoint = format!("http://{}", listener.local_addr()?);
    drop(listener);
    assert!(matches!(
        LanternClient::builder(endpoint)
            .connect_timeout(Duration::from_millis(300))?
            .connect()
            .await,
        Err(LanternError::Transport(_))
    ));
    Ok(())
}
