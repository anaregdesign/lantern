use std::{
    collections::BTreeMap,
    env,
    error::Error,
    fs::{self, File},
    io::{BufRead, BufReader, Read, Write},
    net::{SocketAddr, TcpStream},
    process::{Child, Command, ExitStatus, Stdio},
    thread,
    time::{Duration, Instant},
};

use serde::{Deserialize, Serialize};
use tempfile::TempDir;

// Deterministic local fixture credentials; never installed in production.
pub(crate) const TEST_TOKEN: &str = "lnt_m1_AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE";
pub(crate) const SECOND_TEST_TOKEN: &str = "lnt_m1_AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI";

#[derive(Deserialize)]
struct NativeNode {
    public_origin: String,
}
#[derive(Deserialize)]
struct NativeFixture {
    nodes: Vec<NativeNode>,
    ca_file: String,
    client_cert_file: String,
    client_key_file: String,
}

// The production server binds port zero itself. Read its actual listener
// address without opening and releasing a competing reservation socket.
struct StartupLog(tempfile::NamedTempFile);

impl StartupLog {
    fn bound_address(&self) -> Result<Option<SocketAddr>, Box<dyn Error>> {
        // Startup metadata is small; never scan an unbounded child log.
        let reader = BufReader::new(self.0.reopen()?.take(64 * 1024));
        for line in reader.lines() {
            let line = line?;
            let Ok(event) = serde_json::from_str::<serde_json::Value>(&line) else {
                continue;
            };
            if event.get("msg").and_then(|value| value.as_str()) != Some("lantern server starting")
            {
                continue;
            }
            let address: SocketAddr = event
                .get("addr")
                .and_then(|value| value.as_str())
                .ok_or("production Lantern listener address missing")?
                .parse()?;
            if address.port() == 0 {
                return Err("production Lantern listener port is still zero".into());
            }
            return Ok(Some(address));
        }
        Ok(None)
    }
}

impl Drop for StartupLog {
    fn drop(&mut self) {
        // GoServer reaps its child first. Preserve all original stderr in the
        // test log, including failures before a bound address was published.
        let copied = self
            .0
            .reopen()
            .and_then(|mut file| std::io::copy(&mut file, &mut std::io::stderr()));
        if let Err(error) = copied {
            eprintln!("failed to retain real-wire Lantern stderr: {error}");
        }
    }
}

pub(crate) struct GoServer {
    child: Child,
    port: u16,
    native: Option<NativeFixture>,
    _files: Option<TempDir>,
    _startup_log: Option<StartupLog>,
    startup_deadline: Option<Instant>,
}

fn write_private_fixture(
    fixture: &std::ffi::OsStr,
    path: &std::path::Path,
    bytes: Vec<u8>,
) -> std::io::Result<()> {
    let mut child = Command::new(fixture)
        .arg("-private-input")
        .arg(path)
        .stdin(Stdio::piped())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()?;
    let written = child
        .stdin
        .take()
        .ok_or_else(|| std::io::Error::other("private input pipe missing"))?
        .write_all(&bytes);
    let status = child.wait()?;
    written?;
    if !status.success() {
        return Err(std::io::Error::other(
            "native private input creation failed",
        ));
    }
    Ok(())
}

fn fixture_failure_category(path: &std::path::Path) -> &'static str {
    let mut bytes = Vec::new();
    if File::open(path)
        .and_then(|file| file.take(4096).read_to_end(&mut bytes))
        .is_err()
    {
        return "unknown";
    }
    let text = String::from_utf8_lossy(&bytes);
    for line in text.lines() {
        if let Some(category) = line.strip_prefix("authfixture_failure:") {
            return match category {
                "configuration" => "configuration",
                "generation" => "generation",
                "spawn" => "spawn",
                "readiness" => "readiness",
                "publication" => "publication",
                "exit" => "exit",
                "private_input" => "private_input",
                _ => "unknown",
            };
        }
    }
    "unknown"
}

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "snake_case")]
enum ReadinessReason {
    CaRead,
    CaParse,
    ClientIdentity,
    ChildExit,
    Request,
    Timeout,
}

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "snake_case")]
enum ReadinessProbe {
    None,
    TlsCertificate,
    Timeout,
    Dial,
    Transport,
    Http,
    Json,
    Health,
    Capabilities,
}

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "snake_case")]
enum ReadinessServerLog {
    Unavailable,
    None,
    Initialization,
    BindAddressInUse,
    ServerExit,
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct ReadinessDiagnostic {
    reason: ReadinessReason,
    node: u8,
    port: u16,
    elapsed_ms: u64,
    probe: ReadinessProbe,
    http_status: u16,
    child_exited: bool,
    child_exit_code: i64,
    server_log: ReadinessServerLog,
}

fn native_startup_failure(path: &std::path::Path) -> String {
    let mut message = format!(
        "native fixture failed certified readiness: {}",
        fixture_failure_category(path)
    );
    // Retain only a bounded, typed record before GoServer drops its TempDir.
    // Never copy arbitrary private errors, response bodies or child log text.
    if let Ok(file) = File::open(path) {
        for line in BufReader::new(file.take(4096))
            .lines()
            .map_while(Result::ok)
        {
            if let Some(raw) = line.strip_prefix("authfixture_readiness:") {
                if let Ok(diagnostic) = serde_json::from_str::<ReadinessDiagnostic>(raw)
                    && diagnostic.node < 8
                    && diagnostic.http_status <= 599
                    && let Ok(safe) = serde_json::to_string(&diagnostic)
                {
                    message.push(' ');
                    message.push_str(&safe);
                }
                break;
            }
        }
    }
    message
}

impl GoServer {
    pub(crate) fn start(overrides: &[(&str, &str)]) -> Result<Self, Box<dyn Error>> {
        let binary = env::var_os("LANTERN_RUST_TEST_SERVER")
            .filter(|value| !value.is_empty())
            .ok_or("set LANTERN_RUST_TEST_SERVER to the built production Go server")?;
        if overrides.iter().any(|(name, _)| {
            matches!(
                *name,
                "LANTERN_PORT" | "LANTERN_LOG_LEVEL" | "LANTERN_LOG_FORMAT"
            )
        }) {
            return Err("port and startup logging are fixture-owned settings".into());
        }
        let startup_log = StartupLog(tempfile::NamedTempFile::new()?);
        let deadline = Instant::now() + Duration::from_secs(15);
        let mut command = Command::new(binary);
        command
            .env("LANTERN_PORT", "0")
            .env("LANTERN_LOG_LEVEL", "info")
            .env("LANTERN_LOG_FORMAT", "json")
            .env("LANTERN_METRICS_ADDR", "")
            .env_remove("LANTERN_AUTH_TOKENS")
            .env("LANTERN_TLS_CERT_FILE", "")
            .env("LANTERN_TLS_KEY_FILE", "")
            .env("LANTERN_TLS_CLIENT_CA_FILE", "")
            .env_remove("LANTERN_RUST_TEST_SERVER")
            .env_remove("LANTERN_RUST_TEST_AUTH_FIXTURE")
            .stdin(Stdio::null())
            .stdout(Stdio::inherit())
            .stderr(Stdio::from(startup_log.0.reopen()?));
        for (name, value) in overrides {
            command.env(name, value);
        }
        let mut result = Self {
            child: command.spawn()?,
            port: 0,
            native: None,
            _files: None,
            _startup_log: Some(startup_log),
            startup_deadline: Some(deadline),
        };
        result.wait_for_bound_port()?;
        Ok(result)
    }

    fn wait_for_bound_port(&mut self) -> Result<(), Box<dyn Error>> {
        let deadline = self.readiness_deadline();
        loop {
            if let Some(status) = self.child.try_wait()? {
                return Err(format!(
                    "production Lantern exited before listener readiness: {status}"
                )
                .into());
            }
            if let Some(address) = self
                ._startup_log
                .as_ref()
                .ok_or("production Lantern startup log missing")?
                .bound_address()?
            {
                self.port = address.port();
                return Ok(());
            }
            if Instant::now() >= deadline {
                return Err("production Lantern bound address did not arrive in 15 seconds".into());
            }
            thread::sleep(Duration::from_millis(25));
        }
    }

    pub(crate) fn readiness_deadline(&self) -> Instant {
        self.startup_deadline
            .unwrap_or_else(|| Instant::now() + Duration::from_secs(15))
    }

    pub(crate) fn start_authenticated(
        overrides: &[(&str, &str)],
        mtls: bool,
        receipts: bool,
    ) -> Result<Self, Box<dyn Error>> {
        Self::start_authenticated_cohort(1, overrides, mtls, receipts)
    }
    pub(crate) fn start_authenticated_cohort(
        count: usize,
        overrides: &[(&str, &str)],
        mtls: bool,
        receipts: bool,
    ) -> Result<Self, Box<dyn Error>> {
        if !(1..=8).contains(&count) {
            return Err("invalid native fixture cohort".into());
        }
        let server =
            env::var_os("LANTERN_RUST_TEST_SERVER").ok_or("production Server binary required")?;
        let fixture = env::var_os("LANTERN_RUST_TEST_AUTH_FIXTURE")
            .ok_or("native authfixture binary required")?;
        let files = tempfile::tempdir()?;
        let credentials = files.path().join("credentials.json");
        write_private_fixture(
            &fixture,
            &credentials,
            serde_json::to_vec(&[TEST_TOKEN, SECOND_TEST_TOKEN])?,
        )?;
        let override_file = files.path().join("overrides.json");
        let configured: BTreeMap<&str, &str> = overrides.iter().copied().collect();
        write_private_fixture(
            &fixture,
            &override_file,
            serde_json::to_vec(&vec![configured; count])?,
        )?;
        let diagnostic_path = files.path().join("fixture-startup.log");
        write_private_fixture(&fixture, &diagnostic_path, vec![b'\n'])?;
        let diagnostics = fs::OpenOptions::new().append(true).open(&diagnostic_path)?;
        let mut command = Command::new(fixture);
        command
            .args(["-directory"])
            .arg(files.path().join("trust"))
            .arg("-allocated-nodes")
            .arg(count.to_string())
            .arg("-tokens-file")
            .arg(&credentials)
            .arg("-overrides-file")
            .arg(&override_file)
            .arg("-serve")
            .arg(server)
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::from(diagnostics));
        if mtls {
            command.arg("-public-mtls");
        }
        if receipts {
            command.arg("-receipt");
        }
        let child = command.spawn()?;
        let mut result = Self {
            child,
            port: 0,
            native: None,
            _files: Some(files),
            _startup_log: None,
            startup_deadline: None,
        };
        // A failed readiness/JSON read still owns the supervisor for cleanup.
        let mut line = String::new();
        let stdout = result
            .child
            .stdout
            .take()
            .ok_or("fixture metadata pipe missing")?;
        BufReader::new(stdout).read_line(&mut line)?;
        if line.is_empty() {
            return Err(native_startup_failure(&diagnostic_path).into());
        }
        let native: NativeFixture = serde_json::from_str(&line)?;
        if native.nodes.len() != count {
            return Err("native fixture node count drift".into());
        }
        result.port = native.nodes[0]
            .public_origin
            .rsplit_once(':')
            .ok_or("invalid native public origin")?
            .1
            .parse()?;
        result.native = Some(native);
        Ok(result)
    }
    pub(crate) fn endpoint(&self, node: usize) -> Result<&str, Box<dyn Error>> {
        Ok(&self
            .native
            .as_ref()
            .ok_or("native fixture required")?
            .nodes
            .get(node)
            .ok_or("invalid fixture node")?
            .public_origin)
    }
    pub(crate) fn ca_pem(&self) -> Result<Vec<u8>, Box<dyn Error>> {
        Ok(fs::read(
            &self
                .native
                .as_ref()
                .ok_or("native fixture required")?
                .ca_file,
        )?)
    }
    pub(crate) fn client_identity(&self) -> Result<(Vec<u8>, Vec<u8>), Box<dyn Error>> {
        let native = self.native.as_ref().ok_or("native fixture required")?;
        Ok((
            fs::read(&native.client_cert_file)?,
            fs::read(&native.client_key_file)?,
        ))
    }
    pub(crate) fn port(&self) -> u16 {
        self.port
    }

    pub(crate) fn try_wait(&mut self) -> Result<Option<ExitStatus>, Box<dyn Error>> {
        Ok(self.child.try_wait()?)
    }

    pub(crate) fn wait_for_listener(&mut self) -> Result<(), Box<dyn Error>> {
        let addr = SocketAddr::from(([127, 0, 0, 1], self.port));
        let deadline = self.readiness_deadline();
        loop {
            if let Some(status) = self.child.try_wait()? {
                return Err(format!(
                    "production Lantern exited before listener readiness: {status}"
                )
                .into());
            }
            if TcpStream::connect_timeout(&addr, Duration::from_millis(200)).is_ok() {
                return Ok(());
            }
            if Instant::now() >= deadline {
                return Err("production Lantern listener did not start in 15 seconds".into());
            }
            thread::sleep(Duration::from_millis(25));
        }
    }
}

impl Drop for GoServer {
    fn drop(&mut self) {
        // The supervisor owns production descendants. Ask it to reap the
        // cohort before removing private state, including on failed startup.
        if self._files.is_some() {
            if let Some(mut input) = self.child.stdin.take() {
                let _ = input.write_all(b"shutdown\n");
                let _ = input.flush();
            }
            let deadline = Instant::now() + Duration::from_secs(15);
            while Instant::now() < deadline {
                if matches!(self.child.try_wait(), Ok(Some(_))) {
                    return;
                }
                thread::sleep(Duration::from_millis(25));
            }
        }
        match self.child.try_wait() {
            Ok(Some(_)) => return,
            Ok(None) => {
                if let Err(error) = self.child.kill() {
                    eprintln!("failed to stop real-wire Lantern server: {error}");
                }
            }
            Err(error) => eprintln!("failed to inspect real-wire Lantern server: {error}"),
        }
        if let Err(error) = self.child.wait() {
            eprintln!("failed to reap real-wire Lantern server: {error}");
        }
    }
}

#[cfg(test)]
mod diagnostic_tests {
    use super::*;

    #[test]
    fn readiness_detail_survives_cleanup_without_private_text() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("diagnostic");
        let record = serde_json::json!({
            "reason": "child_exit", "node": 0, "port": 12345,
            "elapsed_ms": 37, "probe": "dial", "http_status": 0,
            "child_exited": true, "child_exit_code": 23,
            "server_log": "bind_address_in_use"
        });
        fs::write(&path, format!("private-token-key-body\nauthfixture_failure:readiness\nauthfixture_readiness:{record}\n")).unwrap();
        let retained = native_startup_failure(&path);
        drop(dir);
        assert!(!path.exists());
        assert!(retained.contains(r#""reason":"child_exit""#));
        assert!(retained.contains(r#""child_exit_code":23"#));
        assert!(retained.contains(r#""port":12345"#));
        assert!(retained.contains(r#""elapsed_ms":37"#));
        assert!(!retained.contains("private-token-key-body"));

        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("diagnostic");
        for (field, value) in [
            ("reason", serde_json::json!("private-token-key-body")),
            ("probe", serde_json::json!("private-token-key-body")),
            ("server_log", serde_json::json!("private-token-key-body")),
            ("body", serde_json::json!("private-token-key-body")),
            ("node", serde_json::json!(8)),
            ("port", serde_json::json!(65536)),
            ("elapsed_ms", serde_json::json!(-1)),
            ("http_status", serde_json::json!(600)),
        ] {
            let mut invalid = record.clone();
            invalid[field] = value;
            fs::write(
                &path,
                format!("authfixture_failure:readiness\nauthfixture_readiness:{invalid}\n"),
            )
            .unwrap();
            assert_eq!(
                native_startup_failure(&path),
                "native fixture failed certified readiness: readiness"
            );
        }
        fs::write(
            &path,
            format!("{}\nauthfixture_readiness:{record}\n", "x".repeat(4096)),
        )
        .unwrap();
        assert_eq!(
            native_startup_failure(&path),
            "native fixture failed certified readiness: unknown"
        );
    }

    #[test]
    fn only_fixed_fixture_categories_leave_private_logs() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("diagnostic");
        for (content, expected) in [
            ("authfixture_failure:readiness\n", "readiness"),
            ("authfixture_failure:private credential\n", "unknown"),
            ("raw private key content\n", "unknown"),
            (
                "authfixture_failure:exit extra private content\n",
                "unknown",
            ),
        ] {
            fs::write(&path, content).unwrap();
            assert_eq!(fixture_failure_category(&path), expected);
        }
        fs::write(&path, vec![b'x'; 8192]).unwrap();
        assert_eq!(fixture_failure_category(&path), "unknown");
    }

    #[test]
    fn startup_port_comes_only_from_the_bound_listener() -> Result<(), Box<dyn Error>> {
        let log = StartupLog(tempfile::NamedTempFile::new()?);
        fs::write(
            log.0.path(),
            concat!(
                "{\"msg\":\"lantern starting\",\"port\":0}\n",
                "{\"msg\":\"metrics server starting\",\"addr\":\"127.0.0.1:9090\"}\n",
                "{\"msg\":\"lantern server starting\",\"addr\":\"[::]:45678\"}\n"
            ),
        )?;
        assert_eq!(log.bound_address()?, Some("[::]:45678".parse()?));
        for event in [
            r#"{"msg":"lantern server starting"}"#,
            r#"{"msg":"lantern server starting","addr":"[::]:0"}"#,
            r#"{"msg":"lantern server starting","addr":"not a socket"}"#,
        ] {
            fs::write(log.0.path(), event)?;
            assert!(log.bound_address().is_err());
        }
        fs::write(
            log.0.path(),
            r#"{"msg":"lantern server starting","addr":"127.0.0.1:45679"}"#,
        )?;
        assert_eq!(log.bound_address()?, Some("127.0.0.1:45679".parse()?));
        fs::write(
            log.0.path(),
            r#"{"msg":"lantern server starting","addr":"[::]:"#,
        )?;
        assert_eq!(log.bound_address()?, None);
        let mut oversized = vec![b'x'; 64 * 1024];
        oversized.extend_from_slice(
            b"\n{\"msg\":\"lantern server starting\",\"addr\":\"[::]:45678\"}\n",
        );
        fs::write(log.0.path(), oversized)?;
        assert_eq!(log.bound_address()?, None);
        // Keep the synthetic diagnostic fixture quiet when its log is replayed.
        fs::write(log.0.path(), "")?;
        Ok(())
    }

    #[tokio::test]
    #[ignore = "set LANTERN_RUST_TEST_SERVER to the production Go server binary"]
    async fn real_wire_fixture_owns_ephemeral_listener_on_return() -> Result<(), Box<dyn Error>> {
        async fn require_serving(server: &GoServer) -> Result<(), Box<dyn Error>> {
            use tonic_health::pb::{
                HealthCheckRequest, health_check_response::ServingStatus,
                health_client::HealthClient,
            };
            let check = async {
                let channel = tonic::transport::Endpoint::from_shared(format!(
                    "http://127.0.0.1:{}",
                    server.port()
                ))?
                .connect_timeout(Duration::from_secs(1))
                .connect()
                .await?;
                let mut request = tonic::Request::new(HealthCheckRequest {
                    service: "graph.v1.LanternService".into(),
                });
                request.set_timeout(Duration::from_secs(1));
                let response = HealthClient::new(channel).check(request).await?;
                assert_eq!(response.get_ref().status, ServingStatus::Serving as i32);
                Ok::<(), Box<dyn Error>>(())
            };
            tokio::time::timeout_at(
                tokio::time::Instant::from_std(server.readiness_deadline()),
                check,
            )
            .await?
        }

        let mut first = GoServer::start(&[])?;
        let mut second = GoServer::start(&[])?;
        assert_ne!(first.port(), 0);
        assert_ne!(first.port(), second.port());
        for server in [&mut first, &mut second] {
            assert!(server.try_wait()?.is_none());
            let log = server._startup_log.as_ref().ok_or("startup log missing")?;
            let address = log.bound_address()?.ok_or("bound address missing")?;
            assert_eq!(address.port(), server.port());
            let events = BufReader::new(log.0.reopen()?.take(64 * 1024)).lines();
            let mut requested_ephemeral_port = false;
            for line in events {
                let Ok(event) = serde_json::from_str::<serde_json::Value>(&line?) else {
                    continue;
                };
                if event["msg"] == "lantern starting" && event["port"] == 0 {
                    requested_ephemeral_port = true;
                }
            }
            assert!(requested_ephemeral_port, "the child must request port zero");
            // Bound-port discovery and wire readiness share the original budget.
            let deadline = server.readiness_deadline();
            server.wait_for_listener()?;
            require_serving(server).await?;
            assert_eq!(server.readiness_deadline(), deadline);
        }

        // Rebinding a live address need not fail on Windows. Prove ownership
        // through the existing connection closing when its child is reaped,
        // while the other live child's actual Health endpoint keeps serving.
        let deadline = first.readiness_deadline();
        deadline
            .checked_duration_since(Instant::now())
            .ok_or("first fixture startup deadline expired")?;
        let timeout = tokio::time::Instant::from_std(deadline);
        let mut connection = tokio::time::timeout_at(
            timeout,
            tokio::net::TcpStream::connect(SocketAddr::from(([127, 0, 0, 1], first.port()))),
        )
        .await
        .map_err(|error| format!("ownership connection exceeded startup deadline: {error}"))?
        .map_err(|error| format!("ownership connection failed: {error}"))?;
        drop(first);
        deadline
            .checked_duration_since(Instant::now())
            .ok_or("first fixture startup deadline expired during cleanup")?;
        // A reset socket may reject setsockopt on macOS. Use the original
        // absolute deadline without configuring a socket after child exit.
        let closed = tokio::time::timeout_at(
            timeout,
            tokio::io::AsyncReadExt::read(&mut connection, &mut [0]),
        )
        .await
        .map_err(|error| format!("owned child close exceeded startup deadline: {error}"))?;
        deadline
            .checked_duration_since(Instant::now())
            .ok_or("first fixture startup deadline expired during close observation")?;
        match closed {
            Ok(0) => {}
            Err(error)
                if matches!(
                    error.kind(),
                    std::io::ErrorKind::ConnectionReset | std::io::ErrorKind::ConnectionAborted
                ) => {}
            result => {
                return Err(format!("owned child connection did not close: {result:?}").into());
            }
        }
        assert!(second.try_wait()?.is_none());
        require_serving(&second).await?;
        Ok(())
    }
}
