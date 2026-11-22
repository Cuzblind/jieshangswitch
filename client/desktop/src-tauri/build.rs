fn main() {
    println!("cargo:rerun-if-changed=binaries/build.json");
    let metadata =
        std::fs::read_to_string("binaries/build.json").expect("Run pnpm build:engine first");
    let metadata: serde_json::Value =
        serde_json::from_str(&metadata).expect("Invalid engine build metadata");
    let origin = metadata["origin"].as_str().expect("Missing engine origin");
    assert_eq!(
        metadata["target"].as_str(),
        Some(std::env::var("TARGET").unwrap().as_str()),
        "Build the engine for the same native target"
    );
    println!(
        "cargo:rustc-env=JIESHANGAI_COMPILED_VERSION={}",
        metadata["version"].as_str().unwrap()
    );
    assert!(!origin.contains(['\r', '\n']), "Invalid origin");
    println!("cargo:rustc-env=JIESHANGAI_COMPILED_ORIGIN={origin}");
    println!(
        "cargo:rustc-env=JIESHANGAI_TARGET={}",
        std::env::var("TARGET").unwrap()
    );
    tauri_build::build();

    // Windows: Embed Common Controls v6 manifest for test binaries
    //
    // When running `cargo test`, the generated test executables don't include
    // the standard Tauri application manifest. Without Common Controls v6,
    // `tauri::test` calls fail with STATUS_ENTRYPOINT_NOT_FOUND.
    //
    // This workaround:
    // 1. Embeds the manifest into test binaries via /MANIFEST:EMBED
    // 2. Uses /MANIFEST:NO for the main binary to avoid duplicate resources
    //    (Tauri already handles manifest embedding for the app binary)
    #[cfg(target_os = "windows")]
    {
        let manifest_path = std::path::PathBuf::from(
            std::env::var("CARGO_MANIFEST_DIR").expect("missing CARGO_MANIFEST_DIR"),
        )
        .join("common-controls.manifest");
        let manifest_arg = format!("/MANIFESTINPUT:{}", manifest_path.display());

        println!("cargo:rustc-link-arg=/MANIFEST:EMBED");
        println!("cargo:rustc-link-arg={}", manifest_arg);
        // Avoid duplicate manifest resources in binary builds.
        println!("cargo:rustc-link-arg-bins=/MANIFEST:NO");
        println!("cargo:rerun-if-changed={}", manifest_path.display());
    }
}
