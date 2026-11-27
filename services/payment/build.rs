use std::env;
use std::path::PathBuf;

fn main() -> Result<(), Box<dyn std::error::Error>> {
    // Prefer explicit PROTOC, else user-local ~/.local/bin/protoc, else system PATH (Docker/CI).
    if env::var_os("PROTOC").is_none() {
        if let Ok(home) = env::var("HOME") {
            let local_protoc = PathBuf::from(&home).join(".local/bin/protoc");
            if local_protoc.exists() {
                env::set_var("PROTOC", &local_protoc);
            }
            let local_include = PathBuf::from(&home).join(".local/include");
            if local_include.exists() && env::var_os("PROTOC_INCLUDE").is_none() {
                env::set_var("PROTOC_INCLUDE", &local_include);
            }
        }
    }

    let payment_proto = PathBuf::from("../../proto/nexusmart/payment/v1/payment.proto");
    let user_proto = PathBuf::from("../../proto/nexusmart/user/v1/user.proto");
    let proto_root = PathBuf::from("../../proto");

    println!("cargo:rerun-if-changed={}", payment_proto.display());
    println!("cargo:rerun-if-changed={}", user_proto.display());
    println!("cargo:rerun-if-changed=migrations");

    let mut includes = vec![proto_root];
    if let Ok(extra_inc) = env::var("PROTOC_INCLUDE") {
        let p = PathBuf::from(extra_inc);
        if p.exists() {
            includes.push(p);
        }
    }
    let sys_include = PathBuf::from("/usr/include");
    if sys_include.join("google/protobuf/timestamp.proto").exists() {
        includes.push(sys_include);
    }

    tonic_build::configure()
        .build_server(true)
        .build_client(true)
        .compile_protos(&[payment_proto, user_proto], &includes)?;

    Ok(())
}
