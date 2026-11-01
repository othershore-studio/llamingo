plugins {
    id("com.android.application")
}

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
        versionCode = 1
        versionName = "0.1-spike"
    }

    buildTypes {
        release {
            isMinifyEnabled = false
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
