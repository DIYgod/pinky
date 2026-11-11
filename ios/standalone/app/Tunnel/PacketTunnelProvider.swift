import NetworkExtension
import os

/// The packet tunnel does two things.
///
/// It reflects: packets for 10.7.0.1 come into the tunnel and go back into
/// the phone with their IPv4 source and destination swapped, so a connection
/// to 10.7.0.1 reaches the phone itself (at 10.7.0.2, the tunnel's own
/// address), where remotepairingd answers as it would a computer on the
/// network. Swapping the two addresses leaves the IP and TCP checksums as
/// they were. LocalDevVPN does the same.
///
/// remotepairingd refuses connections from ::1, hence the detour.
///
/// And it can host the engine: a network extension keeps running while
/// other apps are in front, which the app does not. The extension's own
/// sockets skip its tunnel, though: routed, a connection to 10.7.0.1 leaves
/// over Wi-Fi, so the engine binds its sockets to the tunnel interface.
final class PacketTunnelProvider: NEPacketTunnelProvider {
    private let log = Logger(subsystem: Pinky.tunnel, category: "tunnel")
    private var watcher: EventWatcher?

    override func startTunnel(options: [String: NSObject]?, completionHandler: @escaping (Error?) -> Void) {
        let iface = (options?["iface"] as? String) ?? "10.7.0.2"
        let mask = (options?["mask"] as? String) ?? "255.255.255.252"
        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: "10.7.0.1")
        let v4 = NEIPv4Settings(addresses: [iface], subnetMasks: [mask])
        v4.includedRoutes = [NEIPv4Route(destinationAddress: "10.7.0.1", subnetMask: "255.255.255.255")]
        v4.excludedRoutes = [.default()]
        settings.ipv4Settings = v4
        settings.mtu = 1500
        let engine = (options?["engine"] as? NSNumber)?.boolValue ?? true
        let addr = (options?["addr"] as? String) ?? "10.7.0.1:49152"
        log.log("pinky tunnel: \(iface, privacy: .public)/\(mask, privacy: .public) to \(addr, privacy: .public)")
        setTunnelNetworkSettings(settings) { [weak self] error in
            guard let self else { return }
            if let error {
                completionHandler(error)
                return
            }
            self.reflect()
            if engine {
                let out = PinkyCore.start(host: "extension", addr: addr)
                self.log.log("pinky engine start: \(String(describing: out), privacy: .public)")
                let w = EventWatcher()
                w.start()
                self.watcher = w
            }
            completionHandler(nil)
        }
    }

    private func reflect() {
        packetFlow.readPackets { [weak self] packets, protocols in
            guard let self else { return }
            var out = packets
            for i in out.indices where out[i].count >= 20 && out[i][out[i].startIndex] >> 4 == 4 {
                out[i].withUnsafeMutableBytes { b in
                    for j in 0..<4 { b.swapAt(12 + j, 16 + j) }
                }
            }
            self.packetFlow.writePackets(out, withProtocols: protocols)
            self.reflect()
        }
    }

    override func stopTunnel(with reason: NEProviderStopReason, completionHandler: @escaping () -> Void) {
        log.log("pinky tunnel stop: \(reason.rawValue, privacy: .public)")
        PinkyStop()
        completionHandler()
    }

    override func handleAppMessage(_ messageData: Data, completionHandler: ((Data?) -> Void)?) {
        DispatchQueue.global(qos: .userInitiated).async {
            guard PinkyCore.isStarted else {
                completionHandler?(Data(#"{"ok":false,"error":"the engine runs in the app, not in the tunnel"}"#.utf8))
                return
            }
            completionHandler?(PinkyCore.call(data: messageData))
        }
    }
}
