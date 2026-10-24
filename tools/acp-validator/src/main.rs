
mod checks;
mod report;

use std::collections::HashSet;
use std::time::Duration;

use clap::Parser;

const DEFAULT_CALLBACKS_TRIGGER: &str = r#"{"command":"run_scenario","scenario":"callbacks"}"#;

const DEFAULT_STREAMING_TRIGGER: &str = r#"{"command":"run_scenario","scenario":"session_updates"}"#;

#[derive(Parser, Debug)]
#[command(
    name = "acp-validator",
    about = "Conformance-checking ACP client for validating ACP Agent implementations",
    version
)]
struct Cli {
    #[arg(long)]
    agent: String,

    #[arg(long, value_delimiter = ',')]
    checks: Option<Vec<String>>,

    #[arg(long)]
    json: bool,

    #[arg(long, default_value_t = 15)]
    timeout: u64,

    #[arg(long, default_value = DEFAULT_CALLBACKS_TRIGGER)]
    permission_trigger: String,

    #[arg(long)]
    fs_trigger: Option<String>,

    #[arg(long)]
    cancel_trigger: Option<String>,

    #[arg(long, default_value = DEFAULT_STREAMING_TRIGGER)]
    streaming_trigger: String,
}

#[tokio::main]
async fn main() -> std::process::ExitCode {
    if std::env::var_os("RUST_LOG").is_some() {
        use tracing_subscriber::{EnvFilter, layer::SubscriberExt, util::SubscriberInitExt};
        tracing_subscriber::registry()
            .with(EnvFilter::from_default_env())
            .with(tracing_subscriber::fmt::layer().with_writer(std::io::stderr))
            .init();
    }

    let cli = Cli::parse();

    let enabled = cli.checks.as_ref().map(|names| {
        names
            .iter()
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty())
            .collect::<HashSet<String>>()
    });

    let config = checks::Config {
        agent_command: cli.agent.clone(),
        timeout: Duration::from_secs(cli.timeout),
        permission_trigger: cli.permission_trigger.clone(),
        fs_trigger: cli.fs_trigger.clone().unwrap_or_else(|| cli.permission_trigger.clone()),
        cancel_trigger: cli.cancel_trigger.clone().unwrap_or_else(|| cli.permission_trigger.clone()),
        streaming_trigger: cli.streaming_trigger.clone(),
        enabled,
    };

    let results = checks::run_all(&config).await;

    if cli.json {
        report::print_json(&results);
    } else {
        report::print_table(&results);
    }

    std::process::ExitCode::from(report::exit_code(&results) as u8)
}
