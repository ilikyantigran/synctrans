import Foundation

// RelayClient is the app controller: it owns the WebSocket to the relay, drives
// the AudioStreamer, speaks the wire protocol (internal/protocol on the Go
// side), and publishes UI state. ContentView observes it.
//
// Lifecycle of one recording:
//   tap  → start():  connect WS (async) + start audio immediately (low lag)
//   talk → audio frames stream up as binary; interim/segment/translation come
//          down and update `segments` / `interim`
//   tap  → stop():   stop audio, but keep the socket open for a short grace
//          period so the final phrase + its translation still arrive, then
//          disconnect. We deliberately do NOT send a "stop" control frame: that
//          tears the relay down at once and cancels the in-flight translation.
final class RelayClient: NSObject, ObservableObject {
    // Config (bound to UI). host is "ip:port" — set it to your Mac's LAN address.
    @Published var host: String = "192.168.1.10:8080"
    @Published var sourceLang: Language = .hebrew
    @Published var targetLang: Language = .english

    // State (read by UI).
    @Published private(set) var segments: [Segment] = []
    @Published private(set) var interim: String = ""
    @Published private(set) var isRecording = false
    @Published private(set) var isConnected = false
    @Published private(set) var status: String = "idle"

    private let audio = AudioStreamer()
    private lazy var session = URLSession(configuration: .default)
    private var task: URLSessionWebSocketTask?
    private var graceWork: DispatchWorkItem?

    // Called from ContentView.onAppear.
    func onAppear() {
        audio.prepareSession()
        audio.requestPermission { [weak self] granted in
            if !granted { self?.status = "microphone access denied" }
        }
    }

    func toggle() {
        if isRecording { stop() } else { start() }
    }

    func clear() {
        segments = []
        interim = ""
    }

    // MARK: - Recording

    private func start() {
        graceWork?.cancel()
        graceWork = nil
        connect()
        do {
            try audio.start { [weak self] data in self?.send(data) }
        } catch {
            status = "audio error: \(error.localizedDescription)"
            disconnect()
            return
        }
        isRecording = true
        status = "recording"
    }

    private func stop() {
        audio.stop()
        isRecording = false
        status = "finishing…"
        // Grace window: let the last phrase finalize and its translation come
        // back before we close the socket.
        let work = DispatchWorkItem { [weak self] in
            self?.disconnect()
            self?.status = "idle"
        }
        graceWork = work
        DispatchQueue.main.asyncAfter(deadline: .now() + 4, execute: work)
    }

    // MARK: - WebSocket

    private func connect() {
        guard task == nil else { return }
        guard let url = URL(string: "ws://\(host)/ws?src=\(sourceLang.code)&tgt=\(targetLang.code)") else {
            status = "bad server address"
            return
        }
        let t = session.webSocketTask(with: url)
        task = t
        t.resume()
        receive()
    }

    // send is called from the audio render thread. URLSession serializes sends.
    private func send(_ data: Data) {
        task?.send(.data(data)) { error in
            if let error { print("ws send: \(error)") }
        }
    }

    // receive loops on the socket. The completion fires off-main, so all state
    // mutation (and the next receive call, which touches `task`) is hopped to
    // main to keep `task` single-threaded.
    private func receive() {
        task?.receive { [weak self] result in
            DispatchQueue.main.async {
                guard let self else { return }
                switch result {
                case .failure:
                    self.isConnected = false
                    self.task = nil
                case .success(let message):
                    if case .string(let text) = message,
                       let data = text.data(using: .utf8),
                       let msg = try? JSONDecoder().decode(ServerMessage.self, from: data) {
                        self.handle(msg)
                    }
                    self.receive()
                }
            }
        }
    }

    private func handle(_ m: ServerMessage) {
        switch m.type {
        case "ready":
            isConnected = true
            status = "connected"
        case "interim":
            interim = m.text ?? ""
        case "segment":
            interim = ""
            if let id = m.segId {
                segments.append(Segment(id: id, source: m.text ?? "", translation: nil))
            }
        case "translation":
            if let id = m.segId, let i = segments.firstIndex(where: { $0.id == id }) {
                segments[i].translation = m.text
            }
        case "error":
            status = m.message ?? "relay error"
        default:
            break
        }
    }

    private func disconnect() {
        task?.cancel(with: .goingAway, reason: nil)
        task = nil
        isConnected = false
    }
}
