import com.android.build.api.variant.LibraryVariant
import javax.inject.Inject

plugins {
    alias(libs.plugins.android.library)
    alias(libs.plugins.kotlin.serialization)
    `maven-publish`
}

group = providers.gradleProperty("GROUP").get()
version = providers.gradleProperty("VERSION_NAME").get()

android {
    namespace = "org.tunaos.proton"
    compileSdk = 37

    defaultConfig {
        // Tuna Mail's minSdk; scripts/build-aar.sh passes the same to gomobile.
        minSdk = 24
        consumerProguardFiles("consumer-rules.pro")
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    buildFeatures {
        buildConfig = false
    }

    publishing {
        singleVariant("release") {
            withSourcesJar()
        }
    }
}

kotlin {
    explicitApi()
    compilerOptions {
        jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17)
    }
}

dependencies {
    api(libs.kotlinx.coroutines.core)
    api(libs.kotlinx.serialization.json)

    testImplementation(libs.junit)
    testImplementation(libs.kotlin.test.junit)
    testImplementation(libs.kotlinx.coroutines.test)
}

// The Go library, bound by gomobile (scripts/build-aar.sh). Its generated
// Java classes and its libgojni.so for each ABI are compiled into this AAR,
// so the published artifact is self-contained.
val gomobileDir = rootProject.layout.projectDirectory.dir(
    providers.gradleProperty("gomobileDir").orElse("../build/gomobile"),
)

abstract class UnpackGomobile : DefaultTask() {
    @get:InputFile
    abstract val aar: RegularFileProperty

    @get:InputFile
    abstract val sources: RegularFileProperty

    @get:OutputDirectory
    abstract val javaDir: DirectoryProperty

    @get:OutputDirectory
    abstract val jniDir: DirectoryProperty

    @get:Inject
    abstract val files: FileSystemOperations

    @get:Inject
    abstract val archives: ArchiveOperations

    @TaskAction
    fun unpack() {
        files.sync {
            from(archives.zipTree(sources)) { include("**/*.java") }
            into(javaDir)
        }
        files.sync {
            from(archives.zipTree(aar)) { include("jni/**") }
            eachFile { relativePath = RelativePath(true, *relativePath.segments.drop(1).toTypedArray()) }
            includeEmptyDirs = false
            into(jniDir)
        }
    }
}

androidComponents {
    onVariants { variant: LibraryVariant ->
        val unpack = tasks.register<UnpackGomobile>("unpackGomobile${variant.name.replaceFirstChar(Char::uppercase)}") {
            aar.set(gomobileDir.file("proton-sdk-android.aar"))
            sources.set(gomobileDir.file("proton-sdk-android-sources.jar"))
        }
        variant.sources.java?.addGeneratedSourceDirectory(unpack, UnpackGomobile::javaDir)
        variant.sources.jniLibs?.addGeneratedSourceDirectory(unpack, UnpackGomobile::jniDir)
    }
}

publishing {
    publications {
        register<MavenPublication>("release") {
            artifactId = "proton-sdk-android"
            afterEvaluate { from(components["release"]) }
            pom {
                name.set("proton-sdk-android")
                description.set(
                    "Unofficial Proton Mail client library for Android: Kotlin wrapper over the Go protonsdk",
                )
                url.set("https://github.com/tuna-os/proton-unofficial-sdk")
                licenses {
                    license {
                        name.set("GPL-3.0-or-later")
                        url.set("https://www.gnu.org/licenses/gpl-3.0.html")
                    }
                }
                scm {
                    url.set("https://github.com/tuna-os/proton-unofficial-sdk")
                    connection.set("scm:git:https://github.com/tuna-os/proton-unofficial-sdk.git")
                }
            }
        }
    }
    repositories {
        // A plain Maven layout, zipped into the release's -maven.zip.
        maven {
            name = "build"
            url = uri(layout.buildDirectory.dir("maven"))
        }
    }
}

val mavenZip = tasks.register<Zip>("mavenZip") {
    description = "Zips the published Maven repository for the GitHub release."
    dependsOn("publishReleasePublicationToBuildRepository")
    from(layout.buildDirectory.dir("maven"))
    archiveFileName.set("proton-sdk-android-$version-maven.zip")
    destinationDirectory.set(layout.buildDirectory.dir("dist"))
}

tasks.register<Copy>("dist") {
    description = "Collects the release assets: the AAR and the Maven zip."
    dependsOn(mavenZip)
    from(tasks.named("bundleReleaseAar")) { rename { "proton-sdk-android-$version.aar" } }
    into(layout.buildDirectory.dir("dist"))
}
