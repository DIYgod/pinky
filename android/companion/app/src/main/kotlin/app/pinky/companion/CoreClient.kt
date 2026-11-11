package app.pinky.companion

import android.net.LocalSocket
import android.net.LocalSocketAddress
import org.json.JSONObject
import java.io.BufferedReader
import java.io.Closeable
import java.io.InputStreamReader
import java.io.OutputStreamWriter
import java.util.concurrent.Executors

/**
 * Pinky's screens' side of the core's control socket
 * (internal/standalone/control.go): line JSON on an abstract socket that
 * only this app's uid may open. Every call blocks; run it off the main
 * thread.
 */
object CoreClient {

    private val io = Executors.newCachedThreadPool()

    /**
     * The core said no. code names the reason when the screens know it
     * (control.go's Code constants: no_device, no_key, no_model, no_task,
     * busy), else it is empty.
     */
    class Refused(val code: String, message: String) : Exception(message)

    private fun connect(): LocalSocket {
        val s = LocalSocket()
        s.connect(LocalSocketAddress(BuildConfig.CORE_SOCKET, LocalSocketAddress.Namespace.ABSTRACT))
        return s
    }

    /** Sends one command and returns its reply; throws when the core is not there, or Refused when it says no. */
    fun call(req: JSONObject): JSONObject {
        connect().use { s ->
            val w = OutputStreamWriter(s.outputStream, Charsets.UTF_8)
            w.write(req.toString())
            w.write("\n")
            w.flush()
            val line = BufferedReader(InputStreamReader(s.inputStream, Charsets.UTF_8)).readLine()
                ?: throw IllegalStateException("the core closed the connection")
            val r = JSONObject(line)
            if (!r.optBoolean("ok")) throw Refused(r.optString("code"), r.optString("error", "the core said no"))
            return r
        }
    }

    /** Runs call off the main thread; done gets what went wrong, or null. */
    fun send(req: JSONObject, done: (Exception?) -> Unit = {}) {
        io.execute {
            done(
                try {
                    call(req)
                    null
                } catch (e: Exception) {
                    e
                },
            )
        }
    }

    /** Runs work on this client's threads, off the main thread. */
    fun background(work: () -> Unit) {
        io.execute(work)
    }

    /**
     * Asks this app's accessibility service, over its companion socket, to
     * turn on-phone mode on or off (PinkyAccessibilityService.setOnPhone):
     * on starts the core. Returns the service's reply, whose core_error
     * says why the core did not start, if it failed. Throws when the
     * service is off.
     */
    fun setOnPhone(enabled: Boolean): JSONObject {
        LocalSocket().use { s ->
            s.connect(LocalSocketAddress(BuildConfig.SOCKET_NAME, LocalSocketAddress.Namespace.ABSTRACT))
            val w = OutputStreamWriter(s.outputStream, Charsets.UTF_8)
            w.write(JSONObject().put("id", 1).put("cmd", "on_phone").put("enabled", enabled).toString())
            w.write("\n")
            w.flush()
            val line = BufferedReader(InputStreamReader(s.inputStream, Charsets.UTF_8)).readLine()
                ?: throw IllegalStateException("the service closed the connection")
            val r = JSONObject(line)
            if (!r.optBoolean("ok")) {
                throw IllegalStateException(r.optJSONObject("error")?.optString("message") ?: "the service said no")
            }
            return r.optJSONObject("result") ?: JSONObject()
        }
    }

    fun cmd(name: String, vararg fields: Pair<String, Any>): JSONObject {
        val j = JSONObject().put("cmd", name)
        for ((k, v) in fields) j.put(k, v)
        return j
    }

    /**
     * Follows the state: onState gets each state the core sends, on a
     * background thread, and onDown the reason while the core cannot be
     * reached (it is retried every second): why the core failed to start
     * when it did, else what went wrong with the socket. While the core is
     * not there it turns on-phone mode on, which starts it once the
     * service is on. Close the result to stop.
     */
    fun subscribe(onState: (JSONObject) -> Unit, onDown: (String) -> Unit): Closeable {
        val sub = Subscription(onState, onDown)
        io.execute(sub)
        return sub
    }

    private class Subscription(
        private val onState: (JSONObject) -> Unit,
        private val onDown: (String) -> Unit,
    ) : Runnable, Closeable {

        @Volatile
        private var closed = false

        @Volatile
        private var socket: LocalSocket? = null

        override fun run() {
            while (!closed) {
                try {
                    val s = connect()
                    socket = s
                    if (closed) break
                    val w = OutputStreamWriter(s.outputStream, Charsets.UTF_8)
                    w.write("{\"cmd\":\"subscribe\"}\n")
                    w.flush()
                    val r = BufferedReader(InputStreamReader(s.inputStream, Charsets.UTF_8))
                    while (!closed) {
                        val line = r.readLine() ?: break
                        val st = JSONObject(line).optJSONObject("state") ?: continue
                        onState(st)
                    }
                } catch (e: Exception) {
                    if (closed) break
                    val failed = try {
                        setOnPhone(true).optString("core_error")
                    } catch (_: Exception) {
                        "" // The service is off: the screen says so.
                    }
                    onDown(failed.ifEmpty { e.message ?: e.toString() })
                } finally {
                    try {
                        socket?.close()
                    } catch (_: Exception) {
                    }
                    socket = null
                }
                if (!closed) Thread.sleep(1000)
            }
        }

        override fun close() {
            closed = true
            try {
                socket?.close()
            } catch (_: Exception) {
            }
        }
    }
}
