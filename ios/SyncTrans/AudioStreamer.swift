import AVFoundation

// AudioStreamer captures microphone audio and emits 16 kHz mono PCM16 frames —
// exactly what the relay (and OpenAI's STT) expects on binary WebSocket frames.
//
// Low-lag design: the AVAudioSession is configured and activated up front in
// prepareSession() (called on screen appear), so the only work on the record
// button tap is engine.start(), which returns in milliseconds. The expensive
// session activation / route setup is already done.
final class AudioStreamer {
    private let engine = AVAudioEngine()
    private var converter: AVAudioConverter?
    private var onFrame: ((Data) -> Void)?
    private var running = false

    // The relay expects this exact format (protocol.SampleRate, mono, Int16 LE).
    private let targetFormat = AVAudioFormat(
        commonFormat: .pcmFormatInt16,
        sampleRate: 16000,
        channels: 1,
        interleaved: true
    )!

    // prepareSession configures + activates the audio session ahead of time.
    // Call from ContentView.onAppear. .measurement mode gives a rawer signal
    // (no aggressive AGC), which suits transcription; switch to .default if your
    // input ends up too quiet.
    func prepareSession() {
        let session = AVAudioSession.sharedInstance()
        try? session.setCategory(.record, mode: .measurement)
        try? session.setActive(true)
    }

    // requestPermission prompts for microphone access (iOS 17+ API).
    func requestPermission(_ completion: @escaping (Bool) -> Void) {
        AVAudioApplication.requestRecordPermission { granted in
            DispatchQueue.main.async { completion(granted) }
        }
    }

    // start installs the capture tap and starts the engine. onFrame is called on
    // an audio render thread with one PCM16 chunk per buffer.
    func start(onFrame: @escaping (Data) -> Void) throws {
        guard !running else { return }
        self.onFrame = onFrame

        let input = engine.inputNode
        let inputFormat = input.outputFormat(forBus: 0)
        converter = AVAudioConverter(from: inputFormat, to: targetFormat)

        input.installTap(onBus: 0, bufferSize: 2048, format: inputFormat) { [weak self] buffer, _ in
            self?.process(buffer, inputFormat: inputFormat)
        }
        engine.prepare()
        try engine.start()
        running = true
    }

    // stop tears down capture. Safe to call when not running.
    func stop() {
        guard running else { return }
        engine.inputNode.removeTap(onBus: 0)
        engine.stop()
        converter = nil
        onFrame = nil
        running = false
    }

    // process resamples one hardware buffer to 16 kHz mono Int16 and forwards the
    // raw little-endian bytes.
    private func process(_ buffer: AVAudioPCMBuffer, inputFormat: AVAudioFormat) {
        guard let converter, let onFrame else { return }

        let ratio = targetFormat.sampleRate / inputFormat.sampleRate
        let capacity = AVAudioFrameCount((Double(buffer.frameLength) * ratio).rounded(.up)) + 64
        guard let out = AVAudioPCMBuffer(pcmFormat: targetFormat, frameCapacity: capacity) else { return }

        // The converter pulls input via this block; we have exactly one buffer to
        // give, then report no more data for this call.
        var fed = false
        let inputBlock: AVAudioConverterInputBlock = { _, status in
            if fed {
                status.pointee = .noDataNow
                return nil
            }
            fed = true
            status.pointee = .haveData
            return buffer
        }

        var error: NSError?
        converter.convert(to: out, error: &error, withInputFrom: inputBlock)
        guard error == nil, out.frameLength > 0, let channel = out.int16ChannelData else { return }

        let byteCount = Int(out.frameLength) * MemoryLayout<Int16>.size
        onFrame(Data(bytes: channel[0], count: byteCount))
    }
}
