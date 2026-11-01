// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package studio.annoyances.llamingo;

import android.app.Activity;
import android.os.Build;
import android.os.Bundle;
import android.webkit.WebSettings;
import android.webkit.WebView;

import studio.annoyances.llamingo.mobile.Mobile;

// MainActivity is the whole shell (Option A): start the gomobile-bound core
// in-process (idempotent), then load its loopback server (API + embedded Svelte
// UI) in a WebView. A plain WebView has no window.runtime, so the UI's apiOrigin
// resolves to "" (same origin).
//
// Window insets: the app targets SDK 34, so it's exempt from Android 15/16's
// forced edge-to-edge — the framework insets the content below the status bar and
// (with windowSoftInputMode=adjustResize) lifts it above the keyboard, without any
// manual inset handling. Revisit with proper edge-to-edge insets when targeting 35+.
public class MainActivity extends Activity {
    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);

        WebView web = new WebView(this);
        WebSettings s = web.getSettings();
        s.setJavaScriptEnabled(true);
        s.setDomStorageEnabled(true);
        if (Build.VERSION.SDK_INT >= 33) {
            s.setAlgorithmicDarkeningAllowed(true);
        }
        setContentView(web);

        try {
            long port = Mobile.start(getFilesDir().getAbsolutePath(), "");
            web.loadUrl("http://127.0.0.1:" + port + "/");
        } catch (Exception e) {
            web.loadData("<h1>core failed to start</h1><pre>" + e + "</pre>",
                    "text/html; charset=utf-8", null);
        }
    }
}
