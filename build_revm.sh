#!/bin/bash

set -euo pipefail

echo "🦀 Building revm wrapper..."

cd revm_wrapper

# Keep the Rust archive aligned with the Go linker target on macOS.
if [[ "$(uname -s)" == "Darwin" ]]; then
    export MACOSX_DEPLOYMENT_TARGET="${MACOSX_DEPLOYMENT_TARGET:-26.0}"
    export RUSTFLAGS="${RUSTFLAGS:-} -C link-arg=-mmacosx-version-min=${MACOSX_DEPLOYMENT_TARGET}"
    echo "🍎 Using macOS deployment target ${MACOSX_DEPLOYMENT_TARGET}"
fi

# Build for macOS (static library)
cargo build --release

# Copy the static library
mkdir -p ../lib
cp target/release/libthrylos_revm.a ../lib/ 2>/dev/null || \
cp target/release/libthrylos_revm.dylib ../lib/libthrylos_revm.a 2>/dev/null || \
echo "⚠️  Static library not found, trying to convert dynamic library..."

# If only .so was created, create a symlink as fallback
if [ ! -f ../lib/libthrylos_revm.a ] && [ -f target/release/libthrylos_revm.so ]; then
    ln -sf target/release/libthrylos_revm.so ../lib/libthrylos_revm.a
fi

echo "✅ Built library copied to lib/"
echo ""
echo "🎉 revm wrapper built successfully!"
echo "Next steps:"
echo "1. Copy revm_executor.go to core/evm/"
echo "2. Add WorldState contract methods"
echo "3. Integrate with transaction executor"
echo "4. Run tests!"
