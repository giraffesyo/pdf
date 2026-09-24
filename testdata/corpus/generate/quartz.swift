// Renders page.html with CoreText into a Quartz PDFContext, the way macOS
// apps print: swift quartz.swift  (writes ../quartz-coretext.pdf)
import AppKit
import CoreText

let html = try! Data(contentsOf: URL(fileURLWithPath: "page.html"))
let text = try! NSAttributedString(data: html, options: [.documentType: NSAttributedString.DocumentType.html, .characterEncoding: String.Encoding.utf8.rawValue], documentAttributes: nil)
var media = CGRect(x: 0, y: 0, width: 612, height: 792)
let ctx = CGContext(URL(fileURLWithPath: "../quartz-coretext.pdf") as CFURL, mediaBox: &media, [kCGPDFContextCreator: "giraffesyo/pdf corpus" as CFString] as CFDictionary)!
let setter = CTFramesetterCreateWithAttributedString(text)
var pos = 0
while pos < text.length {
    ctx.beginPDFPage(nil)
    let path = CGPath(rect: media.insetBy(dx: 72, dy: 72), transform: nil)
    let frame = CTFramesetterCreateFrame(setter, CFRange(location: pos, length: 0), path, nil)
    CTFrameDraw(frame, ctx)
    let r = CTFrameGetVisibleStringRange(frame)
    pos += r.length
    ctx.endPDFPage()
    if r.length == 0 { break }
}
ctx.closePDF()
