package app.pinky.companion

import android.content.Context
import android.net.nsd.NsdManager
import android.net.nsd.NsdServiceInfo
import android.os.Handler
import android.os.Looper
import android.util.Log
import java.net.InetAddress
import java.net.NetworkInterface

/**
 * Finds this phone's own Wireless debugging services on the network
 * (adbd's DNS-SD records): CONNECT, the port the debugging connection
 * listens on, which changes each time Wireless debugging is turned on, and
 * PAIRING, the port of "Pair device with pairing code", there only while
 * that dialog is open. Only records of this phone count: other phones on
 * the Wi-Fi advertise theirs too.
 *
 * onPort gets the port, on the main thread, and 0 when the service went
 * away. Pinky never turns Wireless debugging on: it only looks.
 */
class AdbServices(context: Context, private val type: String, private val onPort: (Int) -> Unit) {

    private val nsd = context.getSystemService(NsdManager::class.java)
    private val main = Handler(Looper.getMainLooper())
    private var listener: NsdManager.DiscoveryListener? = null
    private var ours: String? = null // the service name of this phone's record
    private val queue = ArrayDeque<NsdServiceInfo>()
    private var resolving = false

    val running: Boolean get() = listener != null

    fun start() {
        if (listener != null) return
        val l = object : NsdManager.DiscoveryListener {
            override fun onDiscoveryStarted(serviceType: String) {}
            override fun onDiscoveryStopped(serviceType: String) {}
            override fun onStartDiscoveryFailed(serviceType: String, errorCode: Int) {
                Log.w(TAG, "discovery of $type failed: $errorCode")
                main.post { if (listener === this) listener = null }
            }
            override fun onStopDiscoveryFailed(serviceType: String, errorCode: Int) {}
            override fun onServiceFound(info: NsdServiceInfo) {
                main.post {
                    if (listener !== this) return@post
                    queue.addLast(info)
                    resolveNext()
                }
            }
            override fun onServiceLost(info: NsdServiceInfo) {
                main.post {
                    if (listener === this && info.serviceName == ours) {
                        ours = null
                        onPort(0)
                    }
                }
            }
        }
        listener = l
        try {
            nsd.discoverServices(type, NsdManager.PROTOCOL_DNS_SD, l)
        } catch (e: Exception) {
            Log.w(TAG, "could not look for $type", e)
            listener = null
        }
    }

    fun stop() {
        val l = listener ?: return
        listener = null
        ours = null
        queue.clear()
        try {
            nsd.stopServiceDiscovery(l)
        } catch (_: Exception) {
        }
    }

    // NsdManager resolves one service at a time before Android 14.
    @Suppress("DEPRECATION")
    private fun resolveNext() {
        if (resolving || listener == null) return
        val info = queue.removeFirstOrNull() ?: return
        resolving = true
        val l = listener
        try {
            nsd.resolveService(info, object : NsdManager.ResolveListener {
                override fun onResolveFailed(serviceInfo: NsdServiceInfo, errorCode: Int) {
                    main.post {
                        resolving = false
                        if (errorCode == NsdManager.FAILURE_ALREADY_ACTIVE && listener === l) queue.addLast(info)
                        main.postDelayed({ resolveNext() }, if (errorCode == NsdManager.FAILURE_ALREADY_ACTIVE) 300 else 0)
                    }
                }

                override fun onServiceResolved(serviceInfo: NsdServiceInfo) {
                    val own = isOwn(serviceInfo.host)
                    main.post {
                        resolving = false
                        if (listener === l && own && serviceInfo.port > 0) {
                            ours = serviceInfo.serviceName
                            onPort(serviceInfo.port)
                        }
                        resolveNext()
                    }
                }
            })
        } catch (e: Exception) {
            Log.w(TAG, "could not resolve ${info.serviceName}", e)
            resolving = false
        }
    }

    companion object {
        private const val TAG = "PinkyAdbServices"
        const val CONNECT = "_adb-tls-connect._tcp"
        const val PAIRING = "_adb-tls-pairing._tcp"

        /** Whether addr is one of this phone's own addresses. */
        fun isOwn(addr: InetAddress?): Boolean {
            if (addr == null) return false
            if (addr.isLoopbackAddress) return true
            return try {
                NetworkInterface.getNetworkInterfaces()?.toList().orEmpty().any { ni ->
                    ni.inetAddresses.toList().any { it == addr }
                }
            } catch (_: Exception) {
                false
            }
        }
    }
}
