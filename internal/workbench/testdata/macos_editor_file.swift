// Locate the exact temporary file in the real guest editor's Explorer image.
// No desktop capture or external service is involved.
import AppKit
import Vision

guard CommandLine.arguments.count == 3,
      let image = NSImage(contentsOfFile: CommandLine.arguments[1]),
      let cg = image.cgImage(forProposedRect: nil, context: nil, hints: nil) else { exit(2) }
let name = CommandLine.arguments[2]
let request = VNRecognizeTextRequest()
request.recognitionLevel = .accurate
request.usesLanguageCorrection = false
try VNImageRequestHandler(cgImage: cg).perform([request])
for result in request.results ?? [] {
    guard result.topCandidates(3).contains(where: { $0.string.hasSuffix(name) }) else { continue }
    let b = result.boundingBox
    // A matching name in the tab bar or a Quick Open field does not identify
    // the Explorer item. Require the left pane below the workbench toolbar.
    guard b.midX < 0.32, b.midY < 0.85 else { continue }
    print(Int(b.midX * CGFloat(cg.width)), Int((1 - b.midY) * CGFloat(cg.height)))
    exit(0)
}
exit(1)
