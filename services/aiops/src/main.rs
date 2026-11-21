use std::net::SocketAddr;

use tokio::net::TcpListener;
use tokio::signal;
use tokio::sync::watch;
use tracing::{error, info};

use nexusmart_aiops::config::Config;
use nexusmart_aiops::engine::AiopsEngine;
use nexusmart_aiops::http::build_router;
use nexusmart_aiops::metrics::Metrics;
use nexusmart_aiops::telemetry::init_telemetry;

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    let config = Config::from_env();
    let _tracer_provider = init_telemetry(&config.service_name, config.otel_endpoint.as_deref());

    info!(
        service = "aiops",
        ?config,
        "starting NexusMart AIOps engine"
    );

    let metrics = Metrics::new();
    let engine = AiopsEngine::new(config.clone(), metrics);

    let (shutdown_tx, shutdown_rx) = watch::channel(false);

    let poller_handle = tokio::spawn(engine.clone().run_background_poller(shutdown_rx.clone()));

    let signal_tx = shutdown_tx.clone();
    tokio::spawn(async move {
        wait_for_shutdown_signal().await;
        let _ = signal_tx.send(true);
    });

    let http_addr = SocketAddr::from(([0, 0, 0, 0], config.http_port));
    let listener = TcpListener::bind(http_addr).await?;
    let router = build_router(engine);
    let mut http_shutdown = shutdown_rx.clone();

    info!(service = "aiops", addr = %http_addr, "AIOps HTTP server listening");
    let http_res = axum::serve(listener, router)
        .with_graceful_shutdown(async move {
            let _ = http_shutdown.changed().await;
        })
        .await
        .map_err(anyhow::Error::from);

    let _ = shutdown_tx.send(true);
    let _ = poller_handle.await;

    if let Some(tp) = _tracer_provider {
        if let Err(e) = tp.shutdown() {
            error!(service = "aiops", error = ?e, "failed shutting down OTel tracer provider");
        }
    }

    http_res?;
    info!(
        service = "aiops",
        "NexusMart AIOps engine shut down cleanly"
    );
    Ok(())
}

async fn wait_for_shutdown_signal() {
    let ctrl_c = async {
        signal::ctrl_c()
            .await
            .expect("failed to install Ctrl+C handler");
    };

    #[cfg(unix)]
    let terminate = async {
        signal::unix::signal(signal::unix::SignalKind::terminate())
            .expect("failed to install SIGTERM handler")
            .recv()
            .await;
    };

    #[cfg(not(unix))]
    let terminate = std::future::pending::<()>();

    tokio::select! {
        _ = ctrl_c => {},
        _ = terminate => {},
    }

    info!(service = "aiops", "shutdown signal received");
}
