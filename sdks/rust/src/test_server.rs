use std::{
    env,
    error::Error,
    net::{SocketAddr, TcpListener, TcpStream},
    process::{Child, Command, ExitStatus, Stdio},
    thread,
    time::{Duration, Instant},
};

pub(crate) struct GoServer {
    child: Child,
    port: u16,
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
            .env("LANTERN_AUTH_TOKENS", "")
            .env("LANTERN_TLS_CERT_FILE", "")
            .env("LANTERN_TLS_KEY_FILE", "")
            .env("LANTERN_TLS_CLIENT_CA_FILE", "")
            .env_remove("LANTERN_RUST_TEST_SERVER")
            .stdin(Stdio::null())
            .stdout(Stdio::inherit())
            .stderr(Stdio::inherit());
        for (name, value) in overrides {
            command.env(name, value);
        }
        Ok(Self {
            child: command.spawn()?,
            port,
        })
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
