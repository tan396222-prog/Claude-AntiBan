import Cocoa

let source = URL(fileURLWithPath: CommandLine.arguments[1])
let folder = CommandLine.arguments[2]
guard let image = NSImage(contentsOf: source) else {
    fatalError("Cannot load icon source: \(source.path)")
}
try FileManager.default.createDirectory(atPath: folder, withIntermediateDirectories: true)

for (name, size) in [("icon_16x16", 16), ("icon_16x16@2x", 32), ("icon_32x32", 32), ("icon_32x32@2x", 64), ("icon_128x128", 128), ("icon_128x128@2x", 256), ("icon_256x256", 256), ("icon_256x256@2x", 512), ("icon_512x512", 512), ("icon_512x512@2x", 1024)] {
    let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: size, pixelsHigh: size, bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
    NSGraphicsContext.current?.imageInterpolation = .high
    let scale = CGFloat(size) / 1024
    let transform = NSAffineTransform()
    transform.scale(by: scale)
    transform.concat()
    let bounds = NSRect(x: 60, y: 60, width: 904, height: 904)
    NSBezierPath(roundedRect: bounds, xRadius: 210, yRadius: 210).addClip()
    image.draw(in: bounds, from: .zero, operation: .sourceOver, fraction: 1)
    NSGraphicsContext.restoreGraphicsState()
    try rep.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: folder + "/" + name + ".png"))
}
