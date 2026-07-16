import AppKit

// Usage: swift generate_icon.swift <output.png>
// Draws the RsyncGUI 2.0 icon: dark squircle + gradient line-art sync glyph,
// matching the smartview icon language (dark slab, single stroked glyph).
guard CommandLine.arguments.count > 1 else {
    print("Usage: swift generate_icon.swift <output.png>")
    exit(1)
}
let outURL = URL(fileURLWithPath: CommandLine.arguments[1])

let S: CGFloat = 1024
let image = NSImage(size: NSSize(width: S, height: S))
image.lockFocus()

// --- background squircle ---
let margin = S * 0.02
let bgRect = NSRect(x: margin, y: margin, width: S - margin * 2, height: S - margin * 2)
let corner = bgRect.width * 0.2237
let squircle = NSBezierPath(roundedRect: bgRect, xRadius: corner, yRadius: corner)

NSGradient(colors: [
    NSColor(calibratedRed: 0.075, green: 0.085, blue: 0.125, alpha: 1.0), // bottom
    NSColor(calibratedRed: 0.125, green: 0.140, blue: 0.200, alpha: 1.0), // top
])!.draw(in: squircle, angle: 90)

NSColor.white.withAlphaComponent(0.07).setStroke()
let innerStroke = NSBezierPath(roundedRect: bgRect.insetBy(dx: 3, dy: 3), xRadius: corner - 3, yRadius: corner - 3)
innerStroke.lineWidth = 6
innerStroke.stroke()

// --- glyph drawn white, used later as gradient mask ---
func makeGlyphMask() -> NSImage {
    let img = NSImage(size: NSSize(width: S, height: S))
    img.lockFocus()
    NSColor.white.setStroke()
    NSColor.white.setFill()

    let center = NSPoint(x: S / 2, y: S / 2)
    let radius = S * 0.235
    let lineWidth = S * 0.062

    func rad(_ deg: CGFloat) -> CGFloat { deg * .pi / 180 }
    func pointAt(_ deg: CGFloat) -> NSPoint {
        NSPoint(x: center.x + radius * cos(rad(deg)), y: center.y + radius * sin(rad(deg)))
    }

    // Two opposing arcs (clockwise motion) with filled triangular arrowheads.
    for offset in [CGFloat(0), CGFloat(180)] {
        let startDeg = 155 + offset   // arrowhead end
        let endDeg = 35 + offset      // tail end
        let arc = NSBezierPath()
        arc.appendArc(withCenter: center, radius: radius,
                      startAngle: endDeg, endAngle: startDeg, clockwise: false)
        arc.lineWidth = lineWidth
        arc.lineCapStyle = .round
        arc.stroke()

        // Arrowhead: filled triangle at the arc's leading end, tangent-aligned.
        let tip = pointAt(startDeg)
        let tangent = NSPoint(x: -sin(rad(startDeg)), y: cos(rad(startDeg))) // counterclockwise dir
        // motion is from endDeg toward startDeg (counterclockwise), so head points along +tangent
        let headLen = S * 0.085
        let headWidth = S * 0.062
        let normal = NSPoint(x: cos(rad(startDeg)), y: sin(rad(startDeg)))
        let forward = NSPoint(x: tip.x + tangent.x * headLen, y: tip.y + tangent.y * headLen)
        let left = NSPoint(x: tip.x + normal.x * headWidth, y: tip.y + normal.y * headWidth)
        let right = NSPoint(x: tip.x - normal.x * headWidth, y: tip.y - normal.y * headWidth)
        let tri = NSBezierPath()
        tri.move(to: forward)
        tri.line(to: left)
        tri.line(to: right)
        tri.close()
        tri.fill()
    }

    img.unlockFocus()
    return img
}

// Gradient clipped to the glyph via destination-in compositing.
let glyphMask = makeGlyphMask()
let gradientLayer = NSImage(size: NSSize(width: S, height: S))
gradientLayer.lockFocus()
NSGradient(colors: [
    NSColor(calibratedRed: 0.310, green: 0.820, blue: 1.000, alpha: 1.0), // cyan
    NSColor(calibratedRed: 0.357, green: 0.424, blue: 1.000, alpha: 1.0), // indigo
])!.draw(in: NSRect(x: 0, y: 0, width: S, height: S), angle: -45)
glyphMask.draw(in: NSRect(x: 0, y: 0, width: S, height: S),
               from: .zero, operation: .destinationIn, fraction: 1.0)
gradientLayer.unlockFocus()

gradientLayer.draw(in: NSRect(x: 0, y: 0, width: S, height: S),
                   from: .zero, operation: .sourceOver, fraction: 1.0)

image.unlockFocus()

guard let tiff = image.tiffRepresentation,
      let rep = NSBitmapImageRep(data: tiff),
      let png = rep.representation(using: .png, properties: [:]) else {
    print("Errore nella generazione del PNG")
    exit(1)
}
try png.write(to: outURL)
print("Icona scritta in \(outURL.path)")
