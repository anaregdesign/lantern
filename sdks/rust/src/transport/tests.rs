use std::{
    error::Error,
    fs,
    net::TcpListener,
    sync::{
        Mutex,
        atomic::{AtomicUsize, Ordering},
    },
};

use super::*;
use crate::{
    RpcErrorKind, SearchDetails, SearchErrorReason, Vertex, VertexInput, VertexValue,
    generated::graph::v1::{
        AddEdgeRequest, DeleteVertexRequest, GetServerStatusRequest, GetServerStatusResponse,
        GetVertexRequest, GetVertexResponse, PutVertexRequest, PutVertexResponse,
        SearchVerticesRequest, SearchVerticesResponse,
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

        let files = TempDir::new()?;
        fs::write(files.path().join("server.pem"), server_cert.pem())?;
        fs::write(files.path().join("server.key"), server_key.serialize_pem())?;
        fs::write(files.path().join("ca.pem"), &ca_pem)?;

        Ok(Self {
            files,
            ca_pem,
            other_ca_pem,
            client_cert_pem: client_cert.pem().into_bytes(),
            client_key_pem: client_key.serialize_pem().into_bytes(),
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

    let mut health = HealthClient::new(client.state.channel.clone());
    let mut stream = health
        .watch(HealthCheckRequest {
            service: HEALTH_SERVICE.into(),
        })
        .await?
        .into_inner();
    drop(health);
    drop(limited);
    drop(client);
    let observed = tokio::time::timeout(Duration::from_secs(2), stream.message())
        .await??
        .ok_or("Health Watch ended before first status")?;
    assert_eq!(observed.status, ServingStatus::Serving as i32);
    drop(stream);
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
    let certs = TestCertificates::generate()?;
    let env = certs.tls_env(true)?;
    let borrowed: Vec<(&str, &str)> = env
        .iter()
        .map(|(name, value)| (*name, value.as_str()))
        .chain([("LANTERN_AUTH_TOKENS", "first,second")])
        .collect();
    let mut server = GoServer::start(&borrowed)?;
    server.wait_for_listener()?;
    let endpoint = format!("https://localhost:{}", server.port());

    let no_identity = LanternClient::builder(&endpoint)
        .tls_private_ca_pem(&certs.ca_pem)?
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

    let token = Arc::new(RotatingToken::new("first"));
    let client = LanternClient::builder(&endpoint)
        .tls_private_ca_pem(&certs.ca_pem)?
        .tls_client_identity(&certs.client_cert_pem, &certs.client_key_pem)?
        .token_provider(token.clone())
        .connect()
        .await?;
    client.ping().await?;
    assert_eq!(token.calls(), 0, "Health must not read or attach a token");
    assert!(!get_status(&client).await?.go_version.is_empty());
    token.rotate("second");
    assert!(!get_status(&client).await?.go_version.is_empty());
    token.rotate("wrong");
    let denied = rpc_failure(get_status(&client).await.expect_err("invalid token"));
    assert_eq!(denied.kind(), RpcErrorKind::Unauthenticated);
    assert_eq!(token.calls(), 3);

    let auth_free = LanternClient::builder(&endpoint)
        .tls_private_ca_pem(&certs.ca_pem)?
        .tls_client_identity(&certs.client_cert_pem, &certs.client_key_pem)?
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
async fn real_wire_authenticated_development_and_search_details() -> TestResult {
    let mut server = GoServer::start(&[("LANTERN_AUTH_TOKENS", "development")])?;
    server.wait_for_listener()?;
    let endpoint = format!("http://127.0.0.1:{}", server.port());
    let token = Arc::new(RotatingToken::new("development"));
    assert!(matches!(
        LanternClient::builder(&endpoint)
            .token_provider(token.clone())
            .connect()
            .await,
        Err(LanternError::InvalidConfig(_))
    ));
    let client = LanternClient::builder(&endpoint)
        .token_provider(token.clone())
        .allow_credentialed_h2c_for_single_instance_development(true)
        .connect()
        .await?;
    client.ping().await?;
    assert_eq!(token.calls(), 0);
    assert!(!get_status(&client).await?.go_version.is_empty());
    client.put_vertex(VertexInput::nil("rust:authed")).await?;
    assert!(client.get_vertex("rust:authed").await?.is_nil());
    let unauthenticated = LanternClient::builder(&endpoint).connect().await?;
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
    let client = LanternClient::builder(&endpoint).connect().await?;
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
