use std::{
    collections::BTreeMap,
    env,
    error::Error,
    fs::{self, OpenOptions},
    io::{BufRead, BufReader, Write},
    net::{SocketAddr, TcpListener, TcpStream},
    process::{Child, Command, ExitStatus, Stdio},
    thread,
    time::{Duration, Instant},
};

use serde::Deserialize;
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

pub(crate) struct GoServer {
    child: Child,
    port: u16,
    native: Option<NativeFixture>,
    _files: Option<TempDir>,
}

fn write_private_fixture(path: &std::path::Path, bytes: Vec<u8>) -> std::io::Result<()> {
    let mut options = OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    options.open(path)?.write_all(&bytes)
}

impl GoServer {
    pub(crate) fn start(overrides: &[(&str, &str)]) -> Result<Self, Box<dyn Error>> {
        let binary = env::var_os("LANTERN_RUST_TEST_SERVER")
            .filter(|value| !value.is_empty())
            .ok_or("set LANTERN_RUST_TEST_SERVER to the built production Go server")?;
        let listener = TcpListener::bind("127.0.0.1:0")?;
        let port = listener.local_addr()?.port();
        drop(listener);

        let mut command = Command::new(binary);
        command
            .env("LANTERN_PORT", port.to_string())
            .env("LANTERN_METRICS_ADDR", "")
            .env_remove("LANTERN_AUTH_TOKENS")
            .env("LANTERN_TLS_CERT_FILE", "")
            .env("LANTERN_TLS_KEY_FILE", "")
            .env("LANTERN_TLS_CLIENT_CA_FILE", "")
            .env_remove("LANTERN_RUST_TEST_SERVER")
            .env_remove("LANTERN_RUST_TEST_AUTH_FIXTURE")
            .stdin(Stdio::null())
            .stdout(Stdio::inherit())
            .stderr(Stdio::inherit());
        for (name, value) in overrides {
            command.env(name, value);
        }
        Ok(Self {
            child: command.spawn()?,
            port,
            native: None,
            _files: None,
        })
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
            &credentials,
            serde_json::to_vec(&[TEST_TOKEN, SECOND_TEST_TOKEN])?,
        )?;
        let override_file = files.path().join("overrides.json");
        let configured: BTreeMap<&str, &str> = overrides.iter().copied().collect();
        write_private_fixture(
            &override_file,
            serde_json::to_vec(&vec![configured; count])?,
        )?;
        let listeners = (0..if count > 1 { count * 2 } else { count })
            .map(|_| TcpListener::bind("127.0.0.1:0"))
            .collect::<Result<Vec<_>, _>>()?;
        let ports = listeners
            .iter()
            .map(|listener| Ok(listener.local_addr()?.port()))
            .collect::<Result<Vec<_>, std::io::Error>>()?;
        let public = ports[..count]
            .iter()
            .map(u16::to_string)
            .collect::<Vec<_>>()
            .join(",");
        let peers = ports[count..]
            .iter()
            .map(u16::to_string)
            .collect::<Vec<_>>()
            .join(",");
        drop(listeners);
        let mut command = Command::new(fixture);
        command
            .args(["-directory"])
            .arg(files.path().join("trust"))
            .args([
                "-public-ports",
                &public,
                "-peer-ports",
                &peers,
                "-tokens-file",
            ])
            .arg(&credentials)
            .arg("-overrides-file")
            .arg(&override_file)
            .arg("-serve")
            .arg(server)
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::inherit());
        if mtls {
            command.arg("-public-mtls");
        }
        if receipts {
            command.arg("-receipt");
        }
        let child = command.spawn()?;
        let mut result = Self {
            child,
            port: ports[0],
            native: None,
            _files: Some(files),
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
            return Err(
                "native fixture failed certified readiness; inspect its private node log".into(),
            );
        }
        let native: NativeFixture = serde_json::from_str(&line)?;
        if native.nodes.len() != count {
            return Err("native fixture node count drift".into());
        }
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
        let deadline = Instant::now() + Duration::from_secs(15);
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
