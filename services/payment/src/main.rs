use std::net::SocketAddr;
use std::time::Duration;

use sqlx::postgres::PgPoolOptions;
use tokio::net::TcpListener;
use tokio::signal;
use tokio::sync::watch;
use tonic::transport::Server as TonicServer;
use tracing::{error, info};

use nexusmart_payment::auth::GrpcAuth;
use nexusmart_payment::config::Config;
use nexusmart_payment::grpc::PaymentGrpcServer;
use nexusmart_payment::http::build_router;
use nexusmart_payment::metrics::Metrics;
use nexusmart_payment::service::PaymentService;
use nexusmart_payment::telemetry::init_telemetry;
use nexusmart_payment::workers;

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    let config = Config::from_env();
    let _tracer_provider = init_telemetry(&config.service_name, config.otel_endpoint.as_deref());

    info!(?config, "starting NexusMart Payment service");

    let pool = PgPoolOptions::new()
        .max_connections(20)
        .acquire_timeout(Duration::from_secs(10))
        .connect(&config.database_url)
        .await?;

    // Auto-run embedded SQLx migrations on startup
    sqlx::migrate!("./migrations").run(&pool).await?;
    info!("database migrations applied cleanly");

    let metrics = Metrics::new();
    let service = PaymentService::new(pool, config.clone(), metrics);

    let (shutdown_tx, shutdown_rx) = watch::channel(false);

    // Spawn background workers
    let exp_handle = tokio::spawn(workers::run_expiration_scanner(
        service.clone(),
        shutdown_rx.clone(),
    ));
    let outbox_handle = tokio::spawn(workers::run_outbox_relay(
        service.clone(),
        shutdown_rx.clone(),
    ));
    let recon_handle = tokio::spawn(workers::run_reconciliation_scheduler(
        service.clone(),
        shutdown_rx.clone(),
    ));
    let consumer_handle = tokio::spawn(workers::run_nats_order_consumer(
        service.clone(),
        shutdown_rx.clone(),
    ));

    // Spawn shutdown signal listener
    let signal_tx = shutdown_tx.clone();
    tokio::spawn(async move {
        wait_for_shutdown_signal().await;
        let _ = signal_tx.send(true);
    });

    let grpc_auth = GrpcAuth::new(&config.jwt_secret, &config.service_jwt_secret)?;

    // HTTP server on port 8086 (Axum)
    let http_addr = SocketAddr::from(([0, 0, 0, 0], config.http_port));
    let http_listener = TcpListener::bind(http_addr).await?;
    let http_router = build_router(service.clone(), grpc_auth.clone());
    let mut http_shutdown = shutdown_rx.clone();

    let http_future = async move {
        info!(addr = %http_addr, "HTTP server listening");
        axum::serve(http_listener, http_router)
            .with_graceful_shutdown(async move {
                let _ = http_shutdown.changed().await;
            })
            .await
            .map_err(anyhow::Error::from)
    };

    // gRPC server on port 50056 (Tonic)
    let grpc_addr = SocketAddr::from(([0, 0, 0, 0], config.grpc_port));
    let grpc_service = PaymentGrpcServer::new(service, grpc_auth).into_service();
    let mut grpc_shutdown = shutdown_rx.clone();

    let grpc_future = async move {
        info!(addr = %grpc_addr, "gRPC server listening");
        TonicServer::builder()
            .add_service(grpc_service)
            .serve_with_shutdown(grpc_addr, async move {
                let _ = grpc_shutdown.changed().await;
            })
            .await
            .map_err(anyhow::Error::from)
    };

    let (http_res, grpc_res) = tokio::join!(http_future, grpc_future);
    let _ = shutdown_tx.send(true);

    let _ = tokio::join!(exp_handle, outbox_handle, recon_handle, consumer_handle);

    if let Some(tp) = _tracer_provider {
        if let Err(e) = tp.shutdown() {
            error!(error = ?e, "failed shutting down OTel tracer provider");
        }
    }

    http_res?;
    grpc_res?;

    info!("NexusMart Payment service shut down cleanly");
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

    info!("shutdown signal received");
}
