import groovy.json.JsonSlurper

plugins {
    id("com.android.application")
}

// versionName is Pinky's version (cmd/pinky/mygo.json) and versionCode the
// number of commits up to the one built, so a build of a newer commit
// installs over an older one and the Go driver can compare the installed
// app with the APK Pinky bundles. -Ppinky.versionCode=N overrides the count.
val companionVersionName: String = providers
    .fileContents(rootProject.layout.projectDirectory.file("../../cmd/pinky/mygo.json"))
    .asText
    .map { (JsonSlurper().parseText(it) as Map<*, *>)["version"] as String }
    .get()

fun git(vararg args: String): String? = try {
    val out = providers.exec {
        commandLine("git", *args)
        isIgnoreExitValue = true
    }
    if (out.result.get().exitValue == 0) out.standardOutput.asText.get().trim() else null
} catch (_: Exception) {
    null
}

val companionVersionCode: Int = providers.gradleProperty("pinky.versionCode").orNull?.toInt() ?: run {
    if (git("rev-parse", "--is-shallow-repository") == "true") {
        throw GradleException(
            "android/companion: versionCode is the commit count, but this clone is shallow. " +
                "Fetch the whole history (git fetch --unshallow, or fetch-depth: 0 for actions/checkout) " +
                "or pass -Ppinky.versionCode=N.",
        )
    }
    git("rev-list", "--count", "HEAD")?.toIntOrNull()
        ?: throw GradleException("android/companion: cannot count commits with git; pass -Ppinky.versionCode=N.")
}

android {
    namespace = "app.pinky.companion"
    compileSdk {
        version = release(36) {
            minorApiLevel = 1
        }
    }

    defaultConfig {
        applicationId = "app.pinky.companion"
        minSdk = 29
        targetSdk = 36
        versionCode = companionVersionCode
        versionName = companionVersionName
    }

    signingConfigs {
        // Committed on purpose: every build, from any machine, is signed
        // with this key, so a newer build installs over an older one. It
        // guards nothing; never sign anything else with it.
        create("pinky") {
            storeFile = rootProject.file("pinky-companion-debug.keystore")
            storePassword = "android"
            keyAlias = "pinky-companion"
            keyPassword = "android"
        }
    }

    buildTypes {
        debug {
            signingConfig = signingConfigs.getByName("pinky")
        }
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"))
            signingConfig = signingConfigs.getByName("pinky")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    buildFeatures {
        buildConfig = true
    }
}

/**
 * Writes assets/pinky-companion.json with the version, so that Pinky can
 * read the version of an APK it bundles without the Android SDK: the APK
 * is a zip file.
 */
abstract class CompanionVersionAsset : DefaultTask() {
    @get:Input
    abstract val versionName: Property<String>

    @get:Input
    abstract val versionCode: Property<Int>

    @get:OutputDirectory
    abstract val outputDir: DirectoryProperty

    @TaskAction
    fun write() {
        val dir = outputDir.get().asFile
        dir.mkdirs()
        val name = versionName.get().replace("\\", "\\\\").replace("\"", "\\\"")
        dir.resolve("pinky-companion.json").writeText(
            "{\"package\":\"app.pinky.companion\",\"version_name\":\"$name\",\"version_code\":${versionCode.get()}}\n",
        )
    }
}

val companionVersionAsset = tasks.register<CompanionVersionAsset>("companionVersionAsset") {
    versionName.set(companionVersionName)
    versionCode.set(companionVersionCode)
    outputDir.set(layout.buildDirectory.dir("generated/pinky/assets"))
}

androidComponents {
    onVariants { variant ->
        variant.sources.assets?.addGeneratedSourceDirectory(companionVersionAsset, CompanionVersionAsset::outputDir)
    }
}
