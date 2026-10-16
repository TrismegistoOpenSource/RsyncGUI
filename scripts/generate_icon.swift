// SPDX-License-Identifier: MIT

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

// --- background full-bleed ---
// Niente margine e niente angoli arrotondati disegnati da noi: da macOS 26
// (Tahoe) il sistema appoggia le icone non native sul PROPRIO contenitore e
// ne ritaglia lui la forma. Un'icona che si disegna il proprio squircle
// dentro un margine trasparente lascia spuntare quel contenitore chiaro
// tutt'intorno — è il "bordo bianco" segnalato dall'utente il 04/08/2026.
// Riempiendo l'intera tela, la forma la dà macOS e il contenitore sparisce.
let bgRect = NSRect(x: 0, y: 0, width: S, height: S)
let squircle = NSBezierPath(rect: bgRect)

NSGradient(colors: [
    NSColor(srgbRed: 0.075, green: 0.085, blue: 0.125, alpha: 1.0), // bottom
    NSColor(srgbRed: 0.125, green: 0.140, blue: 0.200, alpha: 1.0), // top
])!.draw(in: squircle, angle: 90)


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
    NSColor(srgbRed: 0.103, green: 0.804, blue: 1.000, alpha: 1.0), // cyan, più saturo
    NSColor(srgbRed: 0.250, green: 0.216, blue: 1.000, alpha: 1.0), // indigo, più saturo
])!.draw(in: NSRect(x: 0, y: 0, width: S, height: S), angle: -45)
glyphMask.draw(in: NSRect(x: 0, y: 0, width: S, height: S),
               from: .zero, operation: .destinationIn, fraction: 1.0)
gradientLayer.unlockFocus()

gradientLayer.draw(in: NSRect(x: 0, y: 0, width: S, height: S),
                   from: .zero, operation: .sourceOver, fraction: 1.0)

image.unlockFocus()

guard let tiff = image.tiffRepresentation,
      var rep = NSBitmapImageRep(data: tiff) else {
    print("Errore nella generazione del PNG")
    exit(1)
}
// Il backing store di NSImage lavora nello spazio colore calibrato del
// display (es. un profilo ICC custom), non sRGB: senza questa conversione
// i colori scritti con NSColor(srgbRed:) escono leggermente smorzati nel
// PNG rispetto ai valori numerici dichiarati. Verificato pixel per pixel.
if let converted = rep.converting(to: .sRGB, renderingIntent: .perceptual) {
    rep = converted
}
guard let png = rep.representation(using: .png, properties: [:]) else {
    print("Errore nella generazione del PNG")
    exit(1)
}
try png.write(to: outURL)
print("Icona scritta in \(outURL.path)")
