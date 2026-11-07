plugins {
    alias(libs.plugins.android.library) apply false
    // Pins the Kotlin Gradle plugin AGP's built-in Kotlin compiles with.
    alias(libs.plugins.kotlin.jvm) apply false
    alias(libs.plugins.kotlin.serialization) apply false
}
