plugins {
    id("com.android.application")
}

// Release signing reads the keystore path + credentials from gradle properties
// (~/.gradle/gradle.properties) or env vars, so no key material lives in the repo.
// If unset, the release build is left unsigned; debug builds are unaffected.
val releaseKeystore = (project.findProperty("llamingo.keystore") as String?) ?: System.getenv("LLAMINGO_KEYSTORE")

android {
    namespace = "studio.annoyances.llamingo"
    compileSdk = 36

    defaultConfig {
        applicationId = "studio.annoyances.llamingo"
        minSdk = 29
        // Target 34 (not 35/36): exempt from Android 15/16 forced edge-to-edge, so
        // the status bar and keyboard are handled by classic insets + adjustResize.
        // Bump to 35+ for Play, handling edge-to-edge insets properly then.
        targetSdk = 34
        versionCode = 4
        versionName = "0.1.2"
    }

    signingConfigs {
        if (releaseKeystore != null) {
            create("release") {
                storeFile = file(releaseKeystore)
                storePassword = (project.findProperty("llamingo.storePassword") as String?) ?: System.getenv("LLAMINGO_STORE_PASSWORD")
                keyAlias = (project.findProperty("llamingo.keyAlias") as String?) ?: System.getenv("LLAMINGO_KEY_ALIAS")
                keyPassword = (project.findProperty("llamingo.keyPassword") as String?) ?: System.getenv("LLAMINGO_KEY_PASSWORD")
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            if (releaseKeystore != null) {
                signingConfig = signingConfigs.getByName("release")
            }
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

dependencies {
    // The gomobile-bound core + embedded UI. Built by `gomobile bind` and copied
    // into app/libs/ before the build (see mobile/android/README).
    implementation(files("libs/llamingo.aar"))
}
