// System-output audio for the already authorized per-user GUI window agent.
import AppKit
import AVFoundation
import ScreenCaptureKit

private let audioSampleRate = 48000
private let audioChannels = 2
private let audioFrameBytes = 4

private func audioGUIAvailable() -> Bool {
    var info = stat()
    return stat("/dev/console", &info) == 0 && info.st_uid == getuid() && info.st_uid >= 500 &&
        (CGSessionCopyCurrentDictionary() as? [String: Any])?["CGSSessionScreenIsLocked"] as? Bool != true
}

private func writeAudioBytes(_ fd: Int32, _ data: Data) throws {
    try data.withUnsafeBytes { bytes in
        var offset = 0
        while offset < bytes.count {
            let written = Darwin.write(fd, bytes.baseAddress!.advanced(by: offset), bytes.count - offset)
            if written < 0 && errno == EINTR { continue }
            guard written > 0 else { throw Failure(7, "audio_connection_closed") }
            offset += written
        }
    }
}

private final class AudioPCMConverter {
    private let outputFormat = AVAudioFormat(commonFormat: .pcmFormatInt16, sampleRate: Double(audioSampleRate), channels: AVAudioChannelCount(audioChannels), interleaved: true)!
    private var converter: AVAudioConverter?
    private var inputFormat: AVAudioFormat?

    func convert(_ input: AVAudioPCMBuffer) throws -> Data {
        guard input.format.sampleRate == Double(audioSampleRate), input.format.channelCount == audioChannels,
              input.frameLength > 0, input.frameLength <= 16384 else { throw Failure(7, "audio_format_invalid") }
        if inputFormat != input.format {
            converter = AVAudioConverter(from: input.format, to: outputFormat)
            inputFormat = input.format
        }
        guard let converter, let output = AVAudioPCMBuffer(pcmFormat: outputFormat, frameCapacity: input.frameLength) else {
            throw Failure(7, "audio_format_invalid")
        }
        try converter.convert(to: output, from: input)
        let buffer = output.audioBufferList.pointee.mBuffers
        let count = Int(output.frameLength) * audioFrameBytes
        guard count <= Int(buffer.mDataByteSize), let data = buffer.mData else { throw Failure(7, "audio_format_invalid") }
        return Data(bytes: data, count: count)
    }

    func convert(_ sample: CMSampleBuffer) throws -> Data {
        guard sample.isValid, let description = sample.formatDescription else { throw Failure(7, "audio_format_invalid") }
        guard let basic = CMAudioFormatDescriptionGetStreamBasicDescription(description),
              basic.pointee.mFormatID == kAudioFormatLinearPCM, basic.pointee.mBytesPerFrame > 0,
              basic.pointee.mSampleRate == Double(audioSampleRate), basic.pointee.mChannelsPerFrame == audioChannels else {
            throw Failure(7, "audio_format_invalid")
        }
        let format = AVAudioFormat(cmAudioFormatDescription: description)
        var required = 0
        var block: CMBlockBuffer?
        let flags = UInt32(kCMSampleBufferFlag_AudioBufferList_Assure16ByteAlignment)
        _ = CMSampleBufferGetAudioBufferListWithRetainedBlockBuffer(sample, bufferListSizeNeededOut: &required,
            bufferListOut: nil, bufferListSize: 0, blockBufferAllocator: kCFAllocatorDefault,
            blockBufferMemoryAllocator: kCFAllocatorDefault, flags: flags, blockBufferOut: &block)
        guard required > 0, required <= 4096 else { throw Failure(7, "audio_format_invalid") }
        let memory = UnsafeMutableRawPointer.allocate(byteCount: required, alignment: 16)
        defer { memory.deallocate() }
        let list = memory.bindMemory(to: AudioBufferList.self, capacity: 1)
        let status = CMSampleBufferGetAudioBufferListWithRetainedBlockBuffer(sample, bufferListSizeNeededOut: nil,
            bufferListOut: list, bufferListSize: required, blockBufferAllocator: kCFAllocatorDefault,
            blockBufferMemoryAllocator: kCFAllocatorDefault, flags: flags, blockBufferOut: &block)
        guard status == noErr, let input = AVAudioPCMBuffer(pcmFormat: format, bufferListNoCopy: list, deallocator: nil) else {
            throw Failure(7, "audio_format_invalid")
        }
        return try withExtendedLifetime(block) { try convert(input) }
    }
}

private final class AudioSamples: NSObject, SCStreamOutput, SCStreamDelegate, @unchecked Sendable {
    private let lock = NSLock()
    private let converter = AudioPCMConverter() // Owned by the serial sample queue.
    private var pending = Data()
    private var failed = false
    private let limit = audioSampleRate * audioChannels * 2 / 5 // At most 200 ms.

    func stream(_ stream: SCStream, didOutputSampleBuffer sampleBuffer: CMSampleBuffer, of type: SCStreamOutputType) {
        guard type == .audio, sampleBuffer.isValid, CMSampleBufferDataIsReady(sampleBuffer), CMSampleBufferGetNumSamples(sampleBuffer) > 0 else { return }
        do {
            append(try converter.convert(sampleBuffer))
        } catch {
            stop()
        }
    }

    func stream(_ stream: SCStream, didStopWithError error: Error) {
        stop()
    }

    func append(_ pcm: Data) {
        lock.lock(); defer { lock.unlock() }
        if !failed {
            pending.append(pcm)
            if pending.count > limit { pending.removeFirst(pending.count - limit) }
        }
    }

    func stop() {
        lock.lock(); failed = true; pending.removeAll(); lock.unlock()
    }

    func next() throws -> Data {
        lock.lock(); defer { lock.unlock() }
        guard !failed else { throw Failure(7, "audio_capture_failed") }
        let count = min(pending.count, 3840) // 20 ms, whole interleaved frames.
        let result = Data(pending.prefix(count))
        pending.removeFirst(count)
        return result
    }
}

enum MacAudioCapture {
    static func status() -> [String: Any] {
        let gui = audioGUIAvailable()
        let permission = CGPreflightScreenCaptureAccess()
        return ["supported": true, "available": gui && permission, "format": "s16le",
                "sample_rate": audioSampleRate, "channels": audioChannels,
                "reason": !gui ? "requires_gui_login" : (!permission ? "screen_recording_permission" : "")]
    }

    static func respond(_ fd: Int32) async {
        var stream: SCStream?
        var ready = false
        do {
            try requireGuest()
            let epoch = try await MacGUISession.shared.validate()
            var timeout = timeval(tv_sec: 2, tv_usec: 0)
            setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, &timeout, socklen_t(MemoryLayout.size(ofValue: timeout)))
            guard audioGUIAvailable() else { throw Failure(4, "requires_gui_login") }
            // The existing setup-screen action opens the guest's system consent
            // UI. Audio status and playback never approve or request it silently.
            guard CGPreflightScreenCaptureAccess() else { throw Failure(6, "screen_recording_permission") }
            let content = try await SCShareableContent.excludingDesktopWindows(true, onScreenWindowsOnly: true)
            guard let display = content.displays.first else { throw Failure(7, "audio_display_unavailable") }
            let filter = SCContentFilter(display: display, excludingApplications: [], exceptingWindows: [])
            let configuration = SCStreamConfiguration()
            configuration.capturesAudio = true
            configuration.sampleRate = audioSampleRate
            configuration.channelCount = audioChannels
            configuration.width = 2
            configuration.height = 2
            configuration.minimumFrameInterval = CMTime(value: 1, timescale: 1)
            configuration.queueDepth = 3
            let samples = AudioSamples()
            let capture = SCStream(filter: filter, configuration: configuration, delegate: samples)
            stream = capture
            try capture.addStreamOutput(samples, type: .audio, sampleHandlerQueue: DispatchQueue(label: "dev.cage.GuestAudio.samples"))
            try await capture.startCapture()
            guard audioGUIAvailable() else { throw Failure(4, "requires_gui_login") }
            let header: [String: Any] = ["ready": true, "format": "s16le", "sample_rate": audioSampleRate, "channels": audioChannels]
            try writeAudioBytes(fd, JSONSerialization.data(withJSONObject: header) + Data([10]))
            ready = true
            var checked = Date.distantPast
            while !Task.isCancelled {
                if Date().timeIntervalSince(checked) >= 0.5 {
                    guard audioGUIAvailable(), CGPreflightScreenCaptureAccess() else { break }
                    try await MacGUISession.shared.validate(epoch)
                    checked = Date()
                }
                var byte: UInt8 = 0
                let incoming = recv(fd, &byte, 1, MSG_PEEK | MSG_DONTWAIT)
                if incoming == 0 || incoming > 0 { break }
                if errno != EAGAIN && errno != EWOULDBLOCK && errno != EINTR { break }
                let packet = try samples.next()
                if packet.isEmpty { try await Task.sleep(nanoseconds: 20_000_000) }
                else { try writeAudioBytes(fd, packet) }
            }
        } catch {
            GuestDiagnostic.record("window", (error as? Failure)?.message ?? "audio_capture_failed")
            if !ready {
                let reason = (error as? Failure)?.message ?? "audio_capture_failed"
                if let data = try? JSONSerialization.data(withJSONObject: ["error": reason]) {
                    try? writeAudioBytes(fd, data + Data([10]))
                }
            }
        }
        if let stream { try? await stream.stopCapture() }
    }

    static func selfTest() throws {
        let format = AVAudioFormat(commonFormat: .pcmFormatFloat32, sampleRate: 48000, channels: 2, interleaved: false)!
        let input = AVAudioPCMBuffer(pcmFormat: format, frameCapacity: 4)!
        input.frameLength = 4
        let left: [Float] = [-0.75, 0, 0.5, 0.75]
        let right: [Float] = [0.75, 0.5, 0, -0.75]
        for index in 0..<4 {
            input.floatChannelData![0][index] = left[index]
            input.floatChannelData![1][index] = right[index]
        }
        let converter = AudioPCMConverter()
        let bytes = try converter.convert(input)
        guard bytes.count == 16 else { throw Failure(7, "audio_test_length") }
        for index in 0..<8 {
            let value = Int16(bitPattern: UInt16(bytes[index * 2]) | UInt16(bytes[index * 2 + 1]) << 8)
            let expected = index % 2 == 0 ? left[index / 2] : right[index / 2]
            guard abs(Double(value) / 32768 - Double(expected)) < 0.001 else { throw Failure(7, "audio_test_channels") }
        }
        let again = try converter.convert(input)
        guard again == bytes else { throw Failure(7, "audio_test_continuity") }
        var timing = CMSampleTimingInfo(duration: CMTime(value: 1, timescale: 48000), presentationTimeStamp: .zero, decodeTimeStamp: .invalid)
        var sample: CMSampleBuffer?
        let created = CMSampleBufferCreate(allocator: kCFAllocatorDefault, dataBuffer: nil, dataReady: false,
            makeDataReadyCallback: nil, refcon: nil, formatDescription: format.formatDescription,
            sampleCount: 4, sampleTimingEntryCount: 1, sampleTimingArray: &timing,
            sampleSizeEntryCount: 0, sampleSizeArray: nil, sampleBufferOut: &sample)
        guard created == noErr, let sample,
              CMSampleBufferSetDataBufferFromAudioBufferList(sample, blockBufferAllocator: kCFAllocatorDefault,
                blockBufferMemoryAllocator: kCFAllocatorDefault, flags: 0, bufferList: input.audioBufferList) == noErr else {
            throw Failure(7, "audio_test_sample_creation")
        }
        CMSampleBufferSetDataReady(sample)
        guard try converter.convert(sample) == bytes else { throw Failure(7, "audio_test_sample_conversion") }
        let queued = AudioSamples()
        queued.append(Data(repeating: 0, count: 10000))
        queued.append(Data(repeating: 1, count: 40000))
        var collected = Data()
        while true {
            let packet = try queued.next()
            if packet.isEmpty { break }
            guard packet.count <= 3840, packet.count % 4 == 0 else { throw Failure(7, "audio_test_packet_size") }
            collected.append(packet)
        }
        guard collected == Data(repeating: 1, count: 38400) else { throw Failure(7, "audio_test_queue_limit") }
        queued.stop()
        do {
            _ = try queued.next()
            throw Failure(7, "audio_test_stop")
        } catch let error as Failure {
            guard error.message == "audio_capture_failed" else { throw error }
        }
        print("macOS audio: CoreMedia samples, 48 kHz stereo s16le, consecutive buffers, bounded queue and stopped capture passed")
    }
}
