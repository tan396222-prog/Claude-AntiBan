import AppKit
import CoreImage
import ScreenCaptureKit

@MainActor
final class MacWindowStream: NSObject, SCStreamOutput, SCStreamDelegate {
    static let shared = MacWindowStream()
    private var stream: SCStream?
    private var key = ""
    private var generation = UUID().uuidString
    private var number: UInt64 = 0
    private var sequence: String { generation + "-" + String(number) }
    private var buffer: CVPixelBuffer?
    private var expiry: Task<Void, Never>?
    private var waiters: [UUID: CheckedContinuation<Void, Never>] = [:]
    private let images = CIContext()

    nonisolated func stream(_ stream: SCStream, didOutputSampleBuffer sample: CMSampleBuffer, of type: SCStreamOutputType) {
        guard type == .screen,
              let attachments = CMSampleBufferGetSampleAttachmentsArray(sample, createIfNecessary: false) as? [[SCStreamFrameInfo: Any]],
              let status = attachments.first?[.status] as? Int else { return }
        let pixels = sample.isValid ? sample.imageBuffer : nil
        let changed = (attachments.first?[.dirtyRects] as? [Any])?.isEmpty != true
        Task { @MainActor [weak self] in
            guard let self, self.stream === stream else { return }
            if status == SCFrameStatus.complete.rawValue, let pixels {
                if self.buffer != nil && !changed { return }
                self.buffer = pixels
                self.number += 1
            } else if status != SCFrameStatus.idle.rawValue {
                self.buffer = nil
                self.number += 1
            }
            self.wake()
        }
    }

    nonisolated func stream(_ stream: SCStream, didStopWithError error: Error) {
        Task { @MainActor [weak self] in
            guard let self, self.stream === stream else { return }
            self.stream = nil
            self.buffer = nil
            self.wake()
        }
    }

    private func stop() async {
        let previous = stream
        stream = nil
        buffer = nil
        key = ""
        wake()
        try? await previous?.stopCapture()
    }

    private func wake() {
        let pending = waiters.values
        waiters.removeAll()
        for continuation in pending { continuation.resume() }
    }

    private func waitForFrame(after expected: String, seconds: Double) async throws {
        let id = UUID()
        let timeout = Task { @MainActor [weak self] in
            do { try await Task.sleep(nanoseconds: UInt64(seconds * 1_000_000_000)) }
            catch { return }
            self?.waiters.removeValue(forKey: id)?.resume()
        }
        defer { timeout.cancel() }
        await withTaskCancellationHandler {
            await withCheckedContinuation { continuation in
                if Task.isCancelled || stream == nil || expected != sequence { continuation.resume() }
                else { waiters[id] = continuation }
            }
        } onCancel: {
            Task { @MainActor [weak self] in self?.waiters.removeValue(forKey: id)?.resume() }
        }
        try Task.checkCancellation()
    }

    private func renew() {
        expiry?.cancel()
        expiry = Task { [weak self] in
            do { try await Task.sleep(nanoseconds: 1_500_000_000) }
            catch { return }
            await self?.stop()
        }
    }

    func unchanged(after: String?) -> Bool {
        guard stream != nil, buffer != nil, after == sequence else { return false }
        renew()
        return true
    }

    func frame(filter: SCContentFilter, configuration: SCStreamConfiguration, identity: String, after: String?, wait: Bool = false) async throws -> (CGImage?, String) {
        expiry?.cancel()
        defer { renew() }
        if stream == nil || key != identity {
            await stop()
            generation = UUID().uuidString
            number = 0
            key = identity
            configuration.minimumFrameInterval = CMTime(value: 1, timescale: 30)
            configuration.queueDepth = 3
            configuration.pixelFormat = kCVPixelFormatType_32BGRA
            let next = SCStream(filter: filter, configuration: configuration, delegate: self)
            try next.addStreamOutput(self, type: .screen, sampleHandlerQueue: DispatchQueue(label: "cage.window.frames"))
            stream = next
            do { try await next.startCapture() }
            catch { await stop(); throw error }
        }
        // Only the first frame of a newly configured stream needs to wait.
        let deadline = Date().addingTimeInterval(5)
        while buffer == nil && stream != nil && Date() < deadline {
            try await waitForFrame(after: sequence, seconds: max(0, deadline.timeIntervalSinceNow))
        }
        if wait, buffer != nil, after == sequence {
            // Revalidate window ownership and the console session at least every
            // two seconds even when ScreenCaptureKit reports no changed pixels.
            try await waitForFrame(after: sequence, seconds: 2)
        }
        guard key == identity else { throw Failure(7, "window_changed") }
        guard let buffer else { await stop(); throw Failure(8, "capture_no_frame") }
        let sequence = self.sequence
        if sequence == after { return (nil, sequence) }
        let image = CIImage(cvPixelBuffer: buffer)
        guard let output = images.createCGImage(image, from: image.extent) else { throw Failure(8, "capture_conversion_failed") }
        return (output, sequence)
    }
}
