#!/bin/sh
# Binds protonsdk for Android with gomobile:
#   build/gomobile/proton-sdk-android.aar          classes.jar + jni/<abi>/libgojni.so
#   build/gomobile/proton-sdk-android-sources.jar  the generated Java sources
# The Kotlin wrapper (android/) compiles both into its own AAR.
#
# Needs Go, gomobile and gobind at the golang.org/x/mobile version go.mod
# pins, and an NDK in ANDROID_NDK_HOME (the Tuna Mail build image has all
# three). ANDROID_API is the app's minSdk.
set -eu

cd "$(dirname "$0")/.."
api=${ANDROID_API:-24}
out=build/gomobile
mkdir -p "$out"

# gomobile builds in a temporary module of its own, which cannot use
# vendor/; it resolves the same versions from go.sum through the module cache.
GOFLAGS=-mod=mod gomobile bind \
    -target=android/arm64,android/amd64 \
    -androidapi "$api" \
    -javapkg org.tunaos.proton.gomobile \
    -trimpath \
    -ldflags='-s -w' \
    -o "$out/proton-sdk-android.aar" \
    ./protonsdk

ls -l "$out"
