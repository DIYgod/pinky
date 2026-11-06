package app.pinky.companion

import android.accessibilityservice.AccessibilityService
import android.accessibilityservice.AccessibilityServiceInfo
import android.content.Intent
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import android.util.Log
import android.view.accessibility.AccessibilityEvent
import org.json.JSONObject

/**
 * The accessibility service Pinky turns on over adb for the length of a
 * task. While it is connected it serves the companion protocol (see
 * docs/notes/companion-protocol.md); it never changes what the phone does.
 */
class PinkyAccessibilityService : AccessibilityService() {

    val labels = AppLabels(this)
    val events = EventHub(labels)

    lateinit var tree: TreeDumper
        private set

    lateinit var textSetter: TextSetter
        private set

    /** Turn off after this long without a connection; 0 keeps the service on. */
    @Volatile
    var idleDisableMs = DEFAULT_IDLE_DISABLE_MS
        private set

    @Volatile
    var includeNotImportant = false
        private set

    private var server: CompanionServer? = null
    private val main = Handler(Looper.getMainLooper())

    override fun onCreate() {
        super.onCreate()
        tree = TreeDumper(this)
        textSetter = TextSetter(this)
    }

    override fun onServiceConnected() {
        super.onServiceConnected()
        includeNotImportant =
            serviceInfo.flags and AccessibilityServiceInfo.FLAG_INCLUDE_NOT_IMPORTANT_VIEWS != 0
        server?.stop()
        server = CompanionServer(this).also { it.start() }
        main.removeCallbacks(watchdog)
        main.postDelayed(watchdog, WATCHDOG_INTERVAL_MS)
        Log.i(TAG, "connected")
    }

    override fun onAccessibilityEvent(event: AccessibilityEvent?) {
        if (event != null) events.onEvent(event)
    }

    override fun onInterrupt() {}

    override fun onUnbind(intent: Intent?): Boolean {
        shutdown()
        return super.onUnbind(intent)
    }

    override fun onDestroy() {
        shutdown()
        labels.shutdown()
        super.onDestroy()
    }

    /** Applies a config request and returns the resulting settings. */
    fun configure(req: JSONObject): String {
        if (req.has("include_not_important")) {
            val v = req.getBoolean("include_not_important")
            val info = serviceInfo
            val flag = AccessibilityServiceInfo.FLAG_INCLUDE_NOT_IMPORTANT_VIEWS
            info.flags = if (v) info.flags or flag else info.flags and flag.inv()
            serviceInfo = info
            includeNotImportant = v
            // Cached nodes were fetched with the old flag.
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) clearCache()
        }
        if (req.has("idle_disable_ms")) {
            idleDisableMs = req.getLong("idle_disable_ms").coerceAtLeast(0)
        }
        val j = Json()
        j.beginObject()
        j.field("include_not_important", includeNotImportant)
        j.field("idle_disable_ms", idleDisableMs)
        j.endObject()
        return j.toString()
    }

    private fun shutdown() {
        main.removeCallbacks(watchdog)
        server?.stop()
        server = null
        events.closeAll()
    }

    // A backstop for a gateway that crashed or lost the phone without
    // turning the service off: no connection for idleDisableMs turns it off.
    private val watchdog = object : Runnable {
        override fun run() {
            val s = server ?: return
            val limit = idleDisableMs
            if (limit > 0 && s.connectionCount == 0 && SystemClock.uptimeMillis() - s.lastActivity >= limit) {
                Log.i(TAG, "no connection for ${limit / 1000} s, turning the service off")
                disableSelf()
                return
            }
            main.postDelayed(this, WATCHDOG_INTERVAL_MS)
        }
    }

    companion object {
        private const val TAG = "PinkyService"
        const val DEFAULT_IDLE_DISABLE_MS = 3 * 60_000L
        private const val WATCHDOG_INTERVAL_MS = 10_000L
    }
}
