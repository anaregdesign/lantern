use std::{error::Error, future::Future, marker::PhantomData, pin::Pin, sync::Arc, time::Duration};

use bytes::Buf;
use http::{Uri, uri::PathAndQuery};
use prost::Message;
use rustls::RootCertStore;
use rustls_pki_types::{CertificateDer, PrivateKeyDer, pem::PemObject};
use tokio::time::{Instant, sleep, timeout_at};
use tonic::{
    Code, Request, Response, Status,
    codec::{Codec, DecodeBuf, Decoder, EncodeBuf, Encoder},
    metadata::MetadataValue,
    transport::{Certificate, Channel, ClientTlsConfig, Endpoint, Identity},
};
use tonic_health::pb::{
    HealthCheckRequest, health_check_response::ServingStatus, health_client::HealthClient,
};

use crate::{
    ContribId, LanternError, SearchDetails, contrib::ContribIdGenerator,
    generated::graph::v1::lantern_service_client::LanternServiceClient,
};

const DEFAULT_CONNECT_TIMEOUT: Duration = Duration::from_secs(5);
const DEFAULT_UNARY_TIMEOUT: Duration = Duration::from_secs(15);
const DEFAULT_MESSAGE_LIMIT: usize = 16 * 1024 * 1024;
const MAX_MESSAGE_LIMIT: usize = 64 * 1024 * 1024;
const DEFAULT_BATCH_CHUNK_SIZE: usize = 1_000;
const MAX_BATCH_CHUNK_SIZE: usize = 65_536;
const HEALTH_SERVICE: &str = "graph.v1.LanternService";

/// Error type returned by an application-owned bearer-token provider.
pub type TokenError = Box<dyn Error + Send + Sync>;

/// A short-lived credential fetched separately for each data-plane attempt.
///
/// The client never persists a returned token. A provider can rotate tokens
/// across calls and retries; Health v1 never invokes it.
pub trait TokenProvider: Send + Sync {
    fn token(&self) -> Pin<Box<dyn Future<Output = Result<String, TokenError>> + Send + '_>>;
}

/// One overall budget for a unary call, including credentials and backoff.
#[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
#[non_exhaustive]
pub enum CallOptions {
    #[default]
    Default,
    After(Duration),
    NoDeadline,
}

/// Budgets for one true server stream, independent of the unary deadline.
///
/// Both limits are opt-in. `lifetime` includes authentication and opening the
/// stream; `idle` restarts for each received frame. Either limit cancels the
/// stream on expiry. Neither setting enables a retry or a background reader.
#[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
pub struct StreamOptions {
    pub lifetime: Option<Duration>,
    pub idle: Option<Duration>,
}

impl StreamOptions {
    pub fn with_lifetime(mut self, lifetime: Duration) -> Self {
        self.lifetime = Some(lifetime);
        self
    }

    pub fn with_idle(mut self, idle: Duration) -> Self {
        self.idle = Some(idle);
        self
    }

    pub(crate) fn validate(self) -> Result<(), LanternError> {
        self.deadline().map(|_| ())
    }

    fn deadline(self) -> Result<Option<Instant>, LanternError> {
        if let Some(idle) = self.idle {
            validate_timeout(idle)?;
        }
        self.lifetime
            .map(|lifetime| {
                validate_timeout(lifetime)?;
                Instant::now()
                    .checked_add(lifetime)
                    .ok_or(LanternError::InvalidConfig(
                        "stream lifetime exceeds the clock range",
                    ))
            })
            .transpose()
    }
}

/// Automatic retries are off unless explicitly enabled.
///
/// At most three total attempts are allowed. Only classified unary reads and
/// identical unconditional Put payloads can use this policy. Other mutations
/// and streams never acquire retry eligibility from this setting.
#[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
#[non_exhaustive]
pub enum RetryPolicy {
    #[default]
    Disabled,
    Unavailable {
        max_attempts: u8,
    },
}

impl RetryPolicy {
    fn attempts(self) -> u8 {
        match self {
            Self::Disabled => 1,
            Self::Unavailable { max_attempts } => max_attempts,
        }
    }
}

enum Roots {
    Native,
    #[cfg(feature = "bundled-roots")]
    Bundled,
    PrivateCa(Vec<u8>),
}

struct ClientIdentity {
    cert_pem: Vec<u8>,
    key_pem: Vec<u8>,
}

/// Single-endpoint connection policy. A builder holds no open connection.
pub struct LanternClientBuilder {
    endpoint: String,
    connect_timeout: Duration,
    unary_timeout: Duration,
    encode_limit: usize,
    decode_limit: usize,
    batch_chunk_size: usize,
    server_max_batch_size: Option<usize>,
    auto_contribution_ids: bool,
    token_provider: Option<Arc<dyn TokenProvider>>,
    credentialed_h2c_development: bool,
    roots: Roots,
    identity: Option<ClientIdentity>,
    retry: RetryPolicy,
}

struct ClientState {
    channel: Channel,
    endpoint_identity: String,
    unary_timeout: Duration,
    encode_limit: usize,
    decode_limit: usize,
    batch_chunk_size: usize,
    server_max_batch_size: Option<usize>,
    contribution_ids: Option<ContribIdGenerator>,
    token_provider: Option<Arc<dyn TokenProvider>>,
    retry: RetryPolicy,
}

/// Cloneable, `Send + Sync` transport over one shared Tonic channel.
#[derive(Clone)]
pub struct LanternClient {
    state: Arc<ClientState>,
}

impl LanternClient {
    /// Prepare a single `http://` h2c or verified `https://` endpoint.
    pub fn builder(endpoint: impl AsRef<str>) -> LanternClientBuilder {
        LanternClientBuilder {
            endpoint: endpoint.as_ref().to_owned(),
            connect_timeout: DEFAULT_CONNECT_TIMEOUT,
            unary_timeout: DEFAULT_UNARY_TIMEOUT,
            encode_limit: DEFAULT_MESSAGE_LIMIT,
            decode_limit: DEFAULT_MESSAGE_LIMIT,
            batch_chunk_size: DEFAULT_BATCH_CHUNK_SIZE,
            server_max_batch_size: None,
            auto_contribution_ids: false,
            token_provider: None,
            credentialed_h2c_development: false,
            roots: Roots::Native,
            identity: None,
            retry: RetryPolicy::Disabled,
        }
    }

    /// Check Health v1 on the primary listener, with no credentials attached.
    /// Only a `SERVING` response is successful.
    pub async fn ping(&self) -> Result<(), LanternError> {
        self.ping_with_options(CallOptions::Default).await
    }

    /// Check Health v1 with an explicit unary deadline override.
    pub async fn ping_with_options(&self, options: CallOptions) -> Result<(), LanternError> {
        let health = self
            .call_unary(
                HealthCheckRequest {
                    service: HEALTH_SERVICE.into(),
                },
                options,
                RetryClass::ReadOnly,
                AuthMode::None,
                |channel, encode, decode| {
                    HealthClient::new(channel)
                        .max_encoding_message_size(encode)
                        .max_decoding_message_size(decode)
                },
                |client, request| Box::pin(client.check(request)),
            )
            .await?;
        require_serving(health.status)
    }

    /// Read a current-v2 cache binding with one authenticated attempt. Legacy
    /// scalar versions are refused; this does not authorize any later call.
    pub async fn current_authority_binding(
        &self,
        options: CallOptions,
    ) -> Result<crate::CurrentAuthorityBinding, LanternError> {
        use crate::generated::graph::v1::{
            GetCurrentPrincipalRequest,
            lantern_security_service_client::LanternSecurityServiceClient,
        };
        let response = self
            .call_unary(
                GetCurrentPrincipalRequest {},
                options,
                RetryClass::Never,
                AuthMode::Data,
                |channel, encode, decode| {
                    LanternSecurityServiceClient::new(channel)
                        .max_encoding_message_size(encode)
                        .max_decoding_message_size(decode)
                },
                |client, request| Box::pin(client.get_current_principal(request)),
            )
            .await?;
        crate::CurrentAuthorityBinding::from_wire(
            response
                .version
                .ok_or(LanternError::Protocol("missing current authority version"))?,
        )
    }

    /// Open exactly one data-plane server stream. Keep the raw protobuf frame
    /// until it has been checked for unknown fields before decoding it into
    /// generated types; Prost otherwise discards newly introduced oneof arms.
    pub(crate) async fn raw_data_stream<R>(
        &self,
        message: R,
        path: &'static str,
        options: StreamOptions,
    ) -> Result<DataStream, LanternError>
    where
        R: Message + Send + 'static,
    {
        let actual = message.encoded_len();
        if actual > self.state.encode_limit {
            return Err(LanternError::MessageTooLarge {
                actual,
                limit: self.state.encode_limit,
            });
        }
        let deadline = options.deadline()?;
        let idle_deadline = options
            .idle
            .map(|idle| {
                Instant::now()
                    .checked_add(idle)
                    .ok_or(LanternError::InvalidConfig(
                        "stream idle timeout exceeds the clock range",
                    ))
            })
            .transpose()?;
        let open = async {
            let mut request = Request::new(message);
            if let Some(provider) = &self.state.token_provider {
                let token = provider
                    .token()
                    .await
                    .map_err(LanternError::TokenProvider)?;
                request
                    .metadata_mut()
                    .insert("authorization", bearer_header(&token)?);
            }
            let mut grpc = tonic::client::Grpc::new(self.state.channel.clone())
                .max_encoding_message_size(self.state.encode_limit)
                .max_decoding_message_size(self.state.decode_limit);
            grpc.ready().await.map_err(|error| {
                LanternError::from(Status::unknown(format!("Service was not ready: {error}")))
            })?;
            let response = grpc
                .server_streaming(
                    request,
                    PathAndQuery::from_static(path),
                    RawCodec::<R>::default(),
                )
                .await
                .map_err(LanternError::from)?;
            Ok(response.into_inner())
        };
        let stream = match earlier_stream_deadline(deadline, idle_deadline) {
            Some(end) => timeout_at(end, open)
                .await
                .unwrap_or_else(|_| Err(stream_timeout_error(deadline, idle_deadline)))?,
            None => open.await?,
        };
        Ok(DataStream {
            stream: Some(stream),
            deadline,
            idle: options.idle,
            idle_deadline,
        })
    }

    #[allow(dead_code)]
    pub(crate) async fn data_unary<R, S, F>(
        &self,
        request: R,
        options: CallOptions,
        retry_class: RetryClass,
        rpc: F,
    ) -> Result<S, LanternError>
    where
        R: Message + Clone + Send + 'static,
        S: Send + 'static,
        F: for<'a> Fn(&'a mut LanternServiceClient<Channel>, Request<R>) -> RpcFuture<'a, S>
            + Send
            + Sync,
    {
        let deadline = self.deadline_for(options)?;
        self.data_unary_at(request, deadline, retry_class, rpc)
            .await
    }

    pub(crate) async fn data_unary_at<R, S, F>(
        &self,
        request: R,
        deadline: Option<Instant>,
        retry_class: RetryClass,
        rpc: F,
    ) -> Result<S, LanternError>
    where
        R: Message + Clone + Send + 'static,
        S: Send + 'static,
        F: for<'a> Fn(&'a mut LanternServiceClient<Channel>, Request<R>) -> RpcFuture<'a, S>
            + Send
            + Sync,
    {
        self.call_unary_at(
            request,
            deadline,
            retry_class,
            AuthMode::Data,
            |channel, encode, decode| {
                LanternServiceClient::new(channel)
                    .max_encoding_message_size(encode)
                    .max_decoding_message_size(decode)
            },
            rpc,
        )
        .await
    }

    pub(crate) fn deadline_for(
        &self,
        options: CallOptions,
    ) -> Result<Option<Instant>, LanternError> {
        match options {
            CallOptions::Default => Some(self.state.unary_timeout),
            CallOptions::After(duration) if duration.is_zero() => {
                return Err(LanternError::InvalidConfig(
                    "unary timeout must be positive",
                ));
            }
            CallOptions::After(duration) => Some(duration),
            CallOptions::NoDeadline => None,
        }
        .map(|duration| {
            Instant::now()
                .checked_add(duration)
                .ok_or(LanternError::InvalidConfig(
                    "unary timeout exceeds clock range",
                ))
        })
        .transpose()
    }

    pub(crate) fn batch_chunk_size(&self) -> usize {
        self.state.batch_chunk_size.min(
            self.state
                .server_max_batch_size
                .unwrap_or(MAX_BATCH_CHUNK_SIZE),
        )
    }

    pub(crate) fn encode_limit(&self) -> usize {
        self.state.encode_limit
    }

    pub(crate) fn endpoint_identity(&self) -> &str {
        &self.state.endpoint_identity
    }

    pub(crate) fn mint_contribution_ids(
        &self,
        indices: &[usize],
    ) -> Result<Option<Vec<ContribId>>, LanternError> {
        self.state
            .contribution_ids
            .as_ref()
            .map(|source| source.next_ids(indices))
            .transpose()
    }

    async fn call_unary<C, R, S, MakeClient, F>(
        &self,
        message: R,
        options: CallOptions,
        retry_class: RetryClass,
        auth: AuthMode,
        make_client: MakeClient,
        rpc: F,
    ) -> Result<S, LanternError>
    where
        R: Message + Clone + Send + 'static,
        S: Send + 'static,
        C: Send,
        MakeClient: Fn(Channel, usize, usize) -> C + Send + Sync,
        F: for<'a> Fn(&'a mut C, Request<R>) -> RpcFuture<'a, S> + Send + Sync,
    {
        let deadline = self.deadline_for(options)?;
        self.call_unary_at(message, deadline, retry_class, auth, make_client, rpc)
            .await
    }

    async fn call_unary_at<C, R, S, MakeClient, F>(
        &self,
        message: R,
        deadline: Option<Instant>,
        retry_class: RetryClass,
        auth: AuthMode,
        make_client: MakeClient,
        rpc: F,
    ) -> Result<S, LanternError>
    where
        R: Message + Clone + Send + 'static,
        S: Send + 'static,
        C: Send,
        MakeClient: Fn(Channel, usize, usize) -> C + Send + Sync,
        F: for<'a> Fn(&'a mut C, Request<R>) -> RpcFuture<'a, S> + Send + Sync,
    {
        let actual = message.encoded_len();
        if actual > self.state.encode_limit {
            return Err(LanternError::MessageTooLarge {
                actual,
                limit: self.state.encode_limit,
            });
        }

        let attempts = if retry_class.retryable() {
            self.state.retry.attempts()
        } else {
            1
        };
        for attempt in 0..attempts {
            if deadline.is_some_and(|end| Instant::now() >= end) {
                return Err(LanternError::DeadlineExceeded);
            }
            let call = async {
                let mut request = Request::new(message.clone());
                if matches!(auth, AuthMode::Data)
                    && let Some(provider) = &self.state.token_provider
                {
                    let token = provider
                        .token()
                        .await
                        .map_err(LanternError::TokenProvider)?;
                    request
                        .metadata_mut()
                        .insert("authorization", bearer_header(&token)?);
                }
                if let Some(end) = deadline {
                    let remaining = end.saturating_duration_since(Instant::now());
                    if remaining.is_zero() {
                        return Err(LanternError::DeadlineExceeded);
                    }
                    request.set_timeout(remaining);
                }
                let mut client = make_client(
                    self.state.channel.clone(),
                    self.state.encode_limit,
                    self.state.decode_limit,
                );
                rpc(&mut client, request)
                    .await
                    .map(Response::into_inner)
                    .map_err(LanternError::from)
            };
            let result = match deadline {
                Some(end) => timeout_at(end, call)
                    .await
                    .unwrap_or(Err(LanternError::DeadlineExceeded)),
                None => call.await,
            };
            match result {
                Err(LanternError::Rpc(ref failure))
                    if failure.status().code() == Code::Unavailable
                        && matches!(failure.search_details(), SearchDetails::Absent)
                        && attempt + 1 < attempts =>
                {
                    wait_backoff(full_jitter(attempt), deadline).await?;
                }
                other => return other,
            }
        }
        Err(LanternError::Protocol("retry loop ended without a result"))
    }
}

/// One bounded, demand-driven gRPC stream. Dropping it releases the HTTP/2
/// response and cancels server work; no task owns a hidden stream clone.
pub(crate) struct DataStream {
    stream: Option<tonic::Streaming<Vec<u8>>>,
    deadline: Option<Instant>,
    idle: Option<Duration>,
    idle_deadline: Option<Instant>,
}

impl DataStream {
    pub(crate) async fn next(&mut self) -> Result<Option<Vec<u8>>, LanternError> {
        let Some(stream) = &mut self.stream else {
            return Ok(None);
        };
        let read = async { stream.message().await.map_err(LanternError::from) };
        let result = match earlier_stream_deadline(self.deadline, self.idle_deadline) {
            Some(end) => timeout_at(end, read)
                .await
                .unwrap_or_else(|_| Err(stream_timeout_error(self.deadline, self.idle_deadline))),
            None => read.await,
        };
        if matches!(result, Ok(Some(_))) {
            self.idle_deadline = self.idle.map(|idle| Instant::now() + idle);
        } else {
            self.stream = None;
        }
        result
    }
}

fn earlier_stream_deadline(lifetime: Option<Instant>, idle: Option<Instant>) -> Option<Instant> {
    match (lifetime, idle) {
        (Some(lifetime), Some(idle)) => Some(lifetime.min(idle)),
        (Some(deadline), None) | (None, Some(deadline)) => Some(deadline),
        (None, None) => None,
    }
}

fn stream_timeout_error(lifetime: Option<Instant>, idle: Option<Instant>) -> LanternError {
    if idle.is_some_and(|idle| lifetime.is_none_or(|lifetime| idle < lifetime)) {
        LanternError::StreamIdleTimeout
    } else {
        LanternError::DeadlineExceeded
    }
}

/// Decode complete gRPC frames without losing unknown protobuf fields.
/// Domain decoders check every frame's round-trip length before accepting it.
struct RawCodec<R>(PhantomData<R>);

impl<R> Default for RawCodec<R> {
    fn default() -> Self {
        Self(PhantomData)
    }
}

struct RawEncoder<R>(PhantomData<R>);
struct RawDecoder;

impl<R: Message + Send + 'static> Codec for RawCodec<R> {
    type Encode = R;
    type Decode = Vec<u8>;
    type Encoder = RawEncoder<R>;
    type Decoder = RawDecoder;

    fn encoder(&mut self) -> Self::Encoder {
        RawEncoder(PhantomData)
    }

    fn decoder(&mut self) -> Self::Decoder {
        RawDecoder
    }
}

impl<R: Message + Send + 'static> Encoder for RawEncoder<R> {
    type Item = R;
    type Error = Status;

    fn encode(&mut self, item: R, dst: &mut EncodeBuf<'_>) -> Result<(), Status> {
        item.encode(dst)
            .map_err(|error| Status::internal(format!("cannot encode stream request: {error}")))
    }
}

impl Decoder for RawDecoder {
    type Item = Vec<u8>;
    type Error = Status;

    fn decode(&mut self, src: &mut DecodeBuf<'_>) -> Result<Option<Vec<u8>>, Status> {
        let size = src.remaining();
        Ok(Some(src.copy_to_bytes(size).to_vec()))
    }
}

fn require_serving(status: i32) -> Result<(), LanternError> {
    if status == ServingStatus::Serving as i32 {
        Ok(())
    } else {
        Err(LanternError::HealthNotServing(status))
    }
}

#[derive(Clone, Copy)]
enum AuthMode {
    None,
    Data,
}

#[allow(dead_code)]
#[derive(Clone, Copy)]
pub(crate) enum RetryClass {
    ReadOnly,
    UnconditionalPut,
    Never,
}

impl RetryClass {
    fn retryable(self) -> bool {
        matches!(self, Self::ReadOnly | Self::UnconditionalPut)
    }
}

pub(crate) type RpcFuture<'a, T> =
    Pin<Box<dyn Future<Output = Result<Response<T>, Status>> + Send + 'a>>;

fn full_jitter(failed_attempt: u8) -> Duration {
    let ceiling_ms = (100_u64 << failed_attempt).min(2_000);
    Duration::from_nanos(fastrand::u64(0..=ceiling_ms * 1_000_000))
}

async fn wait_backoff(delay: Duration, deadline: Option<Instant>) -> Result<(), LanternError> {
    match deadline {
        Some(end) if Instant::now() >= end => Err(LanternError::DeadlineExceeded),
        Some(end) => timeout_at(end, sleep(delay))
            .await
            .map_err(|_| LanternError::DeadlineExceeded),
        None => {
            sleep(delay).await;
            Ok(())
        }
    }
}

fn bearer_header(token: &str) -> Result<MetadataValue<tonic::metadata::Ascii>, LanternError> {
    if token.is_empty()
        || !token
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || b"-._~+/=".contains(&byte))
    {
        return Err(LanternError::InvalidToken);
    }
    MetadataValue::try_from(format!("Bearer {token}").as_str())
        .map_err(|_| LanternError::InvalidToken)
}

impl LanternClientBuilder {
    pub fn connect_timeout(mut self, timeout: Duration) -> Result<Self, LanternError> {
        validate_timeout(timeout)?;
        self.connect_timeout = timeout;
        Ok(self)
    }

    pub fn unary_timeout(mut self, timeout: Duration) -> Result<Self, LanternError> {
        validate_timeout(timeout)?;
        self.unary_timeout = timeout;
        Ok(self)
    }

    /// Set per-message encode/decode caps (defaults: 16 MiB each, maximum: 64 MiB).
    pub fn message_limits(mut self, encode: usize, decode: usize) -> Result<Self, LanternError> {
        if encode == 0 || decode == 0 || encode > MAX_MESSAGE_LIMIT || decode > MAX_MESSAGE_LIMIT {
            return Err(LanternError::InvalidConfig(
                "message limits must each be between 1 byte and 64 MiB",
            ));
        }
        self.encode_limit = encode;
        self.decode_limit = decode;
        Ok(self)
    }

    pub fn batch_chunk_size(mut self, size: usize) -> Result<Self, LanternError> {
        if !(1..=MAX_BATCH_CHUNK_SIZE).contains(&size) {
            return Err(LanternError::InvalidConfig(
                "batch chunk size must be between 1 and 65536",
            ));
        }
        self.batch_chunk_size = size;
        Ok(self)
    }

    /// Honor a known lower server `max_batch_size` when planning RPC chunks.
    /// A mismatched higher limit will still fail explicitly at the server.
    pub fn server_max_batch_size(mut self, size: usize) -> Result<Self, LanternError> {
        if size == 0 {
            return Err(LanternError::InvalidConfig(
                "server max batch size must be positive",
            ));
        }
        self.server_max_batch_size = Some(size);
        Ok(self)
    }

    /// Mint one stable CSPRNG-backed ID per missing Add input. This is not
    /// durable replay authorization; automatic Add retry remains disabled.
    pub fn auto_contribution_ids(mut self, enabled: bool) -> Self {
        self.auto_contribution_ids = enabled;
        self
    }

    pub fn retry(mut self, retry: RetryPolicy) -> Result<Self, LanternError> {
        if let RetryPolicy::Unavailable { max_attempts } = retry
            && !(1..=3).contains(&max_attempts)
        {
            return Err(LanternError::InvalidConfig(
                "retry policy permits 1 to 3 total attempts",
            ));
        }
        self.retry = retry;
        Ok(self)
    }

    pub fn token_provider(mut self, provider: Arc<dyn TokenProvider>) -> Self {
        self.token_provider = Some(provider);
        self
    }

    /// Assert that bearer-over-h2c is used ONLY for trusted single-instance
    /// development. The SDK cannot detect server topology. External clients of
    /// authenticated HA deployments must use certificate-verified HTTPS.
    pub fn allow_credentialed_h2c_for_single_instance_development(mut self, allow: bool) -> Self {
        self.credentialed_h2c_development = allow;
        self
    }

    /// Select native OS roots (also the default) for certificate verification.
    pub fn tls_native_roots(mut self) -> Self {
        self.roots = Roots::Native;
        self
    }

    /// Select Mozilla roots only if the `bundled-roots` feature is enabled.
    #[cfg(feature = "bundled-roots")]
    pub fn tls_bundled_roots(mut self) -> Self {
        self.roots = Roots::Bundled;
        self
    }

    /// Trust only the provided parseable CA PEM (not native/bundled roots).
    pub fn tls_private_ca_pem(mut self, ca_pem: impl AsRef<[u8]>) -> Result<Self, LanternError> {
        let ca_pem = ca_pem.as_ref().to_vec();
        let certs = CertificateDer::pem_slice_iter(&ca_pem)
            .collect::<Result<Vec<_>, _>>()
            .map_err(|_| LanternError::InvalidConfig("invalid CA PEM"))?;
        if certs.is_empty() {
            return Err(LanternError::InvalidConfig(
                "CA PEM contains no certificates",
            ));
        }
        let mut roots = RootCertStore::empty();
        for cert in certs {
            roots.add(cert).map_err(|_| {
                LanternError::InvalidConfig("CA PEM contains an invalid certificate")
            })?;
        }
        self.roots = Roots::PrivateCa(ca_pem);
        Ok(self)
    }

    /// Present a PEM certificate and private key when the server requires mTLS.
    pub fn tls_client_identity(
        mut self,
        cert_pem: impl AsRef<[u8]>,
        key_pem: impl AsRef<[u8]>,
    ) -> Result<Self, LanternError> {
        let cert_pem = cert_pem.as_ref().to_vec();
        let key_pem = key_pem.as_ref().to_vec();
        if CertificateDer::pem_slice_iter(&cert_pem)
            .next()
            .transpose()
            .map_err(|_| LanternError::InvalidConfig("invalid client certificate PEM"))?
            .is_none()
        {
            return Err(LanternError::InvalidConfig(
                "client certificate PEM contains no certificates",
            ));
        }
        let private_key = PrivateKeyDer::pem_slice_iter(&key_pem)
            .next()
            .transpose()
            .map_err(|_| LanternError::InvalidConfig("invalid client private key PEM"))?
            .ok_or(LanternError::InvalidConfig(
                "client private key PEM contains no private key",
            ))?;
        rustls::crypto::ring::default_provider()
            .key_provider
            .load_private_key(private_key)
            .map_err(|_| LanternError::InvalidConfig("invalid client private key"))?;
        self.identity = Some(ClientIdentity { cert_pem, key_pem });
        Ok(self)
    }

    /// Connect without redirects, endpoint discovery, or plaintext fallback.
    pub async fn connect(self) -> Result<LanternClient, LanternError> {
        let uri: Uri = self
            .endpoint
            .parse()
            .map_err(|_| LanternError::InvalidEndpoint("expected an absolute HTTP(S) URL"))?;
        let secure = match uri.scheme_str() {
            Some("http") => false,
            Some("https") => true,
            _ => {
                return Err(LanternError::InvalidEndpoint(
                    "expected http:// or https://",
                ));
            }
        };
        let authority = uri
            .authority()
            .ok_or(LanternError::InvalidEndpoint("missing host"))?;
        if authority.host().is_empty() || authority.as_str().contains('@') {
            return Err(LanternError::InvalidEndpoint(
                "missing host or embedded credentials",
            ));
        }
        if uri
            .path_and_query()
            .is_some_and(|part| part.as_str() != "/")
        {
            return Err(LanternError::InvalidEndpoint(
                "endpoint must not contain a path or query",
            ));
        }
        if !secure {
            if self.token_provider.is_some() && !self.credentialed_h2c_development {
                return Err(LanternError::InvalidConfig(
                    "credentialed h2c requires trusted single-instance development opt-in",
                ));
            }
            if self.identity.is_some() || !matches!(self.roots, Roots::Native) {
                return Err(LanternError::InvalidConfig(
                    "TLS trust and client identity require an https:// endpoint",
                ));
            }
        }

        let endpoint_identity = uri.to_string();
        let mut endpoint = Endpoint::from_shared(self.endpoint)
            .map_err(LanternError::Transport)?
            .connect_timeout(self.connect_timeout);
        if secure {
            let mut tls = match self.roots {
                Roots::Native => ClientTlsConfig::new().with_native_roots(),
                #[cfg(feature = "bundled-roots")]
                Roots::Bundled => ClientTlsConfig::new().with_webpki_roots(),
                Roots::PrivateCa(pem) => {
                    ClientTlsConfig::new().ca_certificate(Certificate::from_pem(pem))
                }
            };
            if let Some(identity) = self.identity {
                tls = tls.identity(Identity::from_pem(identity.cert_pem, identity.key_pem));
            }
            endpoint = endpoint.tls_config(tls).map_err(LanternError::Transport)?;
        }
        let channel = endpoint.connect().await.map_err(LanternError::Transport)?;
        Ok(LanternClient {
            state: Arc::new(ClientState {
                channel,
                endpoint_identity,
                unary_timeout: self.unary_timeout,
                encode_limit: self.encode_limit,
                decode_limit: self.decode_limit,
                batch_chunk_size: self.batch_chunk_size,
                server_max_batch_size: self.server_max_batch_size,
                contribution_ids: if self.auto_contribution_ids {
                    Some(ContribIdGenerator::new()?)
                } else {
                    None
                },
                token_provider: self.token_provider,
                retry: self.retry,
            }),
        })
    }
}

fn validate_timeout(timeout: Duration) -> Result<(), LanternError> {
    if timeout.is_zero() || Instant::now().checked_add(timeout).is_none() {
        return Err(LanternError::InvalidConfig(
            "timeout must be positive and within the clock range",
        ));
    }
    Ok(())
}

#[cfg(test)]
mod tests;
