package app.pinky.companion

import android.app.Notification
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import android.util.Log
import android.view.accessibility.AccessibilityEvent
import java.io.IOException
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.LinkedBlockingQueue
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger

/**
 * Turns accessibility events into protocol events and fans them out to
 * subscribed connections. Events arrive on the main thread; each subscriber
 * has its own queue and writer thread, so a slow client never blocks the
 * service.
 */
class EventHub(private val labels: AppLabels) {

    /** Uptime of the last event that means the screen changed. */
    @Volatile
    var lastChangeUptime = 0L
        private set

    private val subscribers = CopyOnWriteArrayList<Subscriber>()
    private val main = Handler(Looper.getMainLooper())

    // Content changes come in bursts; send at most one per CONTENT_INTERVAL_MS.
    private var contentPending = 0
    private var contentPackage: String? = null
    private var contentLastTime = 0L
    private var contentLastEmit = 0L
    private var contentFlushScheduled = false

    fun subscribe(conn: Connection, kinds: Set<String>): Subscriber {
        val s = Subscriber(conn, kinds)
        subscribers.add(s)
        return s
    }

    fun unsubscribe(s: Subscriber) {
        subscribers.remove(s)
        s.close()
    }

    fun closeAll() {
        subscribers.forEach { it.close() }
        subscribers.clear()
    }

    /** Called on the main thread for every accessibility event. */
    fun onEvent(e: AccessibilityEvent) {
        when (e.eventType) {
            AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED -> {
                markChanged()
                e.packageName?.let { labels.warm(it.toString()) }
                if (!wanted(KIND_WINDOW)) return
                val j = begin(KIND_WINDOW, e)
                j.fieldIfNotEmpty("class", e.className)
                j.fieldIfNotEmpty("title", joinText(e))
                j.field("window", e.windowId)
                emit(KIND_WINDOW, j)
            }

            AccessibilityEvent.TYPE_WINDOWS_CHANGED -> {
                markChanged()
                if (!wanted(KIND_WINDOWS)) return
                val j = begin(KIND_WINDOWS, e)
                j.field("window", e.windowId)
                j.name("changes").beginArray()
                windowChangeNames(e.windowChanges).forEach { j.value(it) }
                j.endArray()
                emit(KIND_WINDOWS, j)
            }

            AccessibilityEvent.TYPE_WINDOW_CONTENT_CHANGED,
            AccessibilityEvent.TYPE_VIEW_SCROLLED,
            AccessibilityEvent.TYPE_VIEW_TEXT_CHANGED -> {
                markChanged()
                if (!wanted(KIND_CONTENT)) return
                onContent(e)
            }

            AccessibilityEvent.TYPE_NOTIFICATION_STATE_CHANGED -> {
                val data = e.parcelableData
                if (data is Notification) {
                    if (!wanted(KIND_NOTIFICATION)) return
                    val j = begin(KIND_NOTIFICATION, e)
                    val extras = data.extras
                    j.fieldIfNotEmpty("title", extras?.getCharSequence(Notification.EXTRA_TITLE))
                    val body = extras?.getCharSequence(Notification.EXTRA_TEXT)
                        ?: extras?.getCharSequence(Notification.EXTRA_BIG_TEXT)
                    j.fieldIfNotEmpty("text", body ?: joinText(e))
                    j.fieldIfNotEmpty("category", data.category)
                    emit(KIND_NOTIFICATION, j)
                } else {
                    // Toasts arrive as notification events with no Notification,
                    // also when SystemUI draws them for apps targeting API 30+.
                    if (!wanted(KIND_TOAST)) return
                    val j = begin(KIND_TOAST, e)
                    j.field("text", toastText(e))
                    emit(KIND_TOAST, j)
                }
            }
        }
    }

    /**
     * Blocks until no change event arrived for quietMs, counted from the
     * call at the earliest, or until timeoutMs passed.
     */
    fun waitIdle(quietMs: Long, timeoutMs: Long): String {
        val start = SystemClock.uptimeMillis()
        var settled: Boolean
        while (true) {
            val now = SystemClock.uptimeMillis()
            val quietFor = now - maxOf(lastChangeUptime, start)
            if (quietFor >= quietMs) {
                settled = true
                break
            }
            if (now - start >= timeoutMs) {
                settled = false
                break
            }
            Thread.sleep(minOf(quietMs - quietFor, 25L).coerceAtLeast(1L))
        }
        val now = SystemClock.uptimeMillis()
        val j = Json()
        j.beginObject()
        j.field("settled", settled)
        j.field("waited_ms", now - start)
        if (lastChangeUptime > 0) j.field("last_change_ms_ago", now - lastChangeUptime)
        j.endObject()
        return j.toString()
    }

    /**
     * The toast's message. SystemUI's toasts (Android 12+) also report the
     * app's name, which is dropped when the event has more than one text.
     */
    private fun toastText(e: AccessibilityEvent): String {
        val parts = e.text.mapNotNull { it?.toString()?.trim() }.filter { it.isNotEmpty() }
        val label = e.packageName?.let { labels.get(it.toString()) }
        val message = if (parts.size > 1 && label != null) parts.filter { it != label } else parts
        return message.joinToString(" ")
    }

    private fun markChanged() {
        lastChangeUptime = SystemClock.uptimeMillis()
    }

    private fun wanted(kind: String): Boolean = subscribers.any { kind in it.kinds }

    private fun onContent(e: AccessibilityEvent) {
        contentPending++
        contentPackage = e.packageName?.toString()
        contentLastTime = eventTime(e)
        val now = SystemClock.uptimeMillis()
        if (now - contentLastEmit >= CONTENT_INTERVAL_MS) {
            flushContent()
        } else if (!contentFlushScheduled) {
            contentFlushScheduled = true
            main.postAtTime({
                contentFlushScheduled = false
                flushContent()
            }, contentLastEmit + CONTENT_INTERVAL_MS)
        }
    }

    private fun flushContent() {
        if (contentPending == 0) return
        val j = Json()
        j.beginObject()
        j.field("event", KIND_CONTENT)
        j.field("time", contentLastTime)
        j.fieldIfNotEmpty("package", contentPackage)
        j.field("count", contentPending)
        contentPending = 0
        contentLastEmit = SystemClock.uptimeMillis()
        emit(KIND_CONTENT, j)
    }

    private fun begin(kind: String, e: AccessibilityEvent): Json {
        val j = Json()
        j.beginObject()
        j.field("event", kind)
        j.field("time", eventTime(e))
        j.fieldIfNotEmpty("package", e.packageName)
        // AccessibilityEvent.getDisplayId is public from API 33.
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) j.field("display", e.displayId)
        return j
    }

    private fun emit(kind: String, j: Json) {
        j.endObject()
        val line = j.toString()
        for (s in subscribers) {
            if (kind in s.kinds) s.offer(line)
        }
    }

    /** A connection that receives events. */
    inner class Subscriber(private val conn: Connection, val kinds: Set<String>) {
        private val queue = LinkedBlockingQueue<String>(QUEUE_CAPACITY)
        private val dropped = AtomicInteger()

        @Volatile
        private var closed = false
        private var thread: Thread? = null

        /** Starts delivery. Called after the subscribe reply went out. */
        fun start() {
            thread = Thread(::loop, "pinky-events").apply {
                isDaemon = true
                start()
            }
        }

        fun offer(line: String) {
            if (!closed && !queue.offer(line)) dropped.incrementAndGet()
        }

        fun close() {
            closed = true
            thread?.interrupt()
        }

        private fun loop() {
            try {
                while (!closed) {
                    val line = queue.poll(1, TimeUnit.SECONDS) ?: continue
                    val lost = dropped.getAndSet(0)
                    if (lost > 0) conn.send("{\"event\":\"dropped\",\"time\":${System.currentTimeMillis()},\"count\":$lost}")
                    conn.send(line)
                }
            } catch (_: InterruptedException) {
            } catch (e: IOException) {
                Log.d(TAG, "event subscriber gone: $e")
            } finally {
                subscribers.remove(this)
            }
        }
    }

    companion object {
        private const val TAG = "PinkyEvents"
        private const val CONTENT_INTERVAL_MS = 100L
        private const val QUEUE_CAPACITY = 2048

        const val KIND_WINDOW = "window"
        const val KIND_WINDOWS = "windows"
        const val KIND_CONTENT = "content"
        const val KIND_TOAST = "toast"
        const val KIND_NOTIFICATION = "notification"

        val ALL_KINDS = setOf(KIND_WINDOW, KIND_WINDOWS, KIND_CONTENT, KIND_TOAST, KIND_NOTIFICATION)
        val DEFAULT_KINDS = setOf(KIND_WINDOW, KIND_TOAST, KIND_NOTIFICATION)

        /** Converts an event's uptime timestamp to wall-clock milliseconds. */
        fun eventTime(e: AccessibilityEvent): Long =
            System.currentTimeMillis() - (SystemClock.uptimeMillis() - e.eventTime)

        private fun joinText(e: AccessibilityEvent): String =
            e.text.filterNotNull().joinToString(" ") { it.toString() }.trim()

        private fun windowChangeNames(changes: Int): List<String> {
            val names = mutableListOf<String>()
            if (changes and AccessibilityEvent.WINDOWS_CHANGE_ADDED != 0) names += "added"
            if (changes and AccessibilityEvent.WINDOWS_CHANGE_REMOVED != 0) names += "removed"
            if (changes and AccessibilityEvent.WINDOWS_CHANGE_TITLE != 0) names += "title"
            if (changes and AccessibilityEvent.WINDOWS_CHANGE_BOUNDS != 0) names += "bounds"
            if (changes and AccessibilityEvent.WINDOWS_CHANGE_LAYER != 0) names += "layer"
            if (changes and AccessibilityEvent.WINDOWS_CHANGE_ACTIVE != 0) names += "active"
            if (changes and AccessibilityEvent.WINDOWS_CHANGE_FOCUSED != 0) names += "focused"
            if (changes and AccessibilityEvent.WINDOWS_CHANGE_ACCESSIBILITY_FOCUSED != 0) names += "accessibility_focused"
            if (changes and AccessibilityEvent.WINDOWS_CHANGE_PARENT != 0) names += "parent"
            if (changes and AccessibilityEvent.WINDOWS_CHANGE_CHILDREN != 0) names += "children"
            if (changes and AccessibilityEvent.WINDOWS_CHANGE_PIP != 0) names += "pip"
            return names
        }
    }
}
