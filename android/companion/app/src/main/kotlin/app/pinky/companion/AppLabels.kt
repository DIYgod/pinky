package app.pinky.companion

import android.content.Context
import android.content.pm.PackageManager
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.Executors

/**
 * Display names of apps. The first lookup of a package loads its resources,
 * which takes 100 ms or more on older phones, so names load on a background
 * thread: a tree never waits for one, and a window change starts loading
 * the new app's name before the next tree asks for it.
 */
class AppLabels(private val context: Context) {
    private val labels = ConcurrentHashMap<String, String>()
    private val pending: MutableSet<String> = ConcurrentHashMap.newKeySet()
    private val loader = Executors.newSingleThreadExecutor { r ->
        Thread(r, "pinky-labels").apply {
            isDaemon = true
            priority = Thread.MIN_PRIORITY
        }
    }

    /** Returns the name if it is loaded; otherwise starts loading it and returns null. */
    fun get(pkg: String): String? {
        val label = labels[pkg]
        if (label != null) return label.ifEmpty { null }
        warm(pkg)
        return null
    }

    /** Starts loading a package's name if it is not known yet. */
    fun warm(pkg: String) {
        if (pkg.isEmpty() || labels.containsKey(pkg) || !pending.add(pkg)) return
        loader.execute {
            val label = try {
                val pm = context.packageManager
                pm.getApplicationLabel(pm.getApplicationInfo(pkg, 0)).toString()
            } catch (_: PackageManager.NameNotFoundException) {
                ""
            }
            labels[pkg] = label
            pending.remove(pkg)
        }
    }

    fun shutdown() {
        loader.shutdownNow()
    }
}
