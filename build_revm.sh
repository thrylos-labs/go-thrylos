#!/bin/bash
# build_revm.sh
# Builds the revm Rust library and sets up Go bindings

set -e

echo "🦀 Building revm wrapper..."

# Navigate to Rust project
cd revm_wrapper

# Build for release (optimized)
cargo build --release

# Create lib directory if it doesn't exist
mkdir -p ../lib

# Copy the library
if [[ "$OSTYPE" == "linux-gnu"* ]]; then
    # Linux
    cp target/release/libthrylos_revm.so ../lib/
    echo "✅ Built Linux shared library: lib/libthrylos_revm.so"
elif [[ "$OSTYPE" == "darwin"* ]]; then
    # macOS
    cp target/release/libthrylos_revm.dylib ../lib/libthrylos_revm.so
    echo "✅ Built macOS dynamic library: lib/libthrylos_revm.so"
elif [[ "$OSTYPE" == "msys" || "$OSTYPE" == "win32" ]]; then
    # Windows
    cp target/release/thrylos_revm.dll ../lib/
    echo "✅ Built Windows DLL: lib/thrylos_revm.dll"
fi

cd ..

echo ""
echo "🎉 revm wrapper built successfully!"
echo ""
echo "Next steps:"
echo "1. Copy revm_executor.go to core/evm/"
echo "2. Add WorldState contract methods"
echo "3. Integrate with transaction executor"
echo "4. Run tests!"
