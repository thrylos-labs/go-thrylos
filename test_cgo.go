package main

/*
#cgo LDFLAGS: -L${SRCDIR}/lib -lthrylos_revm
#include <stdint.h>

typedef struct { uint8_t bytes[20]; } CAddress;

uint64_t revm_reserve_nonce(void* executor, CAddress address);
void revm_release_nonce(void* executor, CAddress address, uint64_t nonce);
uint64_t revm_get_next_nonce(void* executor, CAddress address);
*/
import "C"

func main() {
    // Test if CGO can find the functions
    _ = C.revm_reserve_nonce
    _ = C.revm_release_nonce
    _ = C.revm_get_next_nonce
}
