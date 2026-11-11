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

    private fun connect(): LocalSocket {
        val s = LocalSocket()
        s.connect(LocalSocketAddress(BuildConfig.CORE_SOCKET, LocalSocketAddress.Namespace.ABSTRACT))
        return s
    }

    /** Sends one command and returns its reply; throws when the core is not there or says no. */
    fun call(req: JSONObject): JSONObject {
        connect().use { s ->
            val w = OutputStreamWriter(s.outputStream, Charsets.UTF_8)
            w.write(req.toString())
            w.write("\n")
            w.flush()
            val line = BufferedReader(InputStreamReader(s.inputStream, Charsets.UTF_8)).readLine()
                ?: throw IllegalStateException("the core closed the connection")
            val r = JSONObject(line)
            if (!r.optBoolean("ok")) throw IllegalStateException(r.optString("error", "the core said no"))
            return r
        }
    }

    /** Runs call off the main thread; done gets the error message, or null. */
    fun send(req: JSONObject, done: (String?) -> Unit = {}) {
        io.execute {
            done(
                try {
                    call(req)
                    null
                } catch (e: Exception) {
                    e.message ?: e.toString()
                },
            )
        }
    }

    /**
     * Asks this app's accessibility service, over its companion socket, to
     * turn on-phone mode on or off (PinkyAccessibilityService.setOnPhone):
     * on starts the core. Throws when the service is off.
     */
    fun setOnPhone(enabled: Boolean) {
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
     * reached (it is retried every second). While the core is not there it
     * turns on-phone mode on, which starts it once the service is on.
     * Close the result to stop.
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
                    onDown(e.message ?: e.toString())
                    try {
                        setOnPhone(true)
                    } catch (_: Exception) {
                        // The service is off: the screen says so.
                    }
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
